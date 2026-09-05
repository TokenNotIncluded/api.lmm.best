// RelayConvertError intentionally preserves structured conversion context by
// value; boxing it here would only move the same public API cost downstream.
#![allow(clippy::result_large_err)]

//! OpenAI Chat Completions and Responses relay HTTP boundary.
//!
//! The service behind this boundary owns token authentication, channel
//! selection, upstream I/O, billing/logging and Valkey mutations.  Keeping
//! those operations behind [`OpenAiRelayService`] makes the HTTP layer use the
//! typed protocol conversion boundary without duplicating the transactional
//! relay policy.

use std::{
    net::IpAddr,
    sync::Arc,
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

use crate::{
    RequestContext,
    conversion_observability::{
        ClientAbortGuard, ConversionObserver, ConversionResult, ConverterVersion, FailureReason,
        FeatureClass, MetricLabels, StreamTiming, global_observer,
    },
    protocol_rollout::{ProtocolRolloutControl, RolloutContext},
    protocol_route_gate::{RouteGateBlocker, RouteGateDecision, RouteGateDetails, decide_route},
    protocol_runtime_registry::validated_current_registry,
    route_ownership::{OwnershipEvidence, RouteOwnershipScope},
};
use async_trait::async_trait;
use axum::{
    Router,
    body::{Body, to_bytes},
    extract::{Request, State},
    http::{HeaderMap, HeaderName, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::post,
};
use futures_util::StreamExt;
use lmm_contracts::relay::{
    CanonicalRequest, CanonicalResponse, CanonicalStreamEvent, ConversionPlan, Direction, Fidelity,
    Protocol, RelayConvertError, ValidatedRegistry, canonical_response_to_openai_chat,
    canonical_response_to_openai_responses, response_events_to_openai_chunks,
    response_events_to_responses,
};
use serde::Serialize;
use sqlx::{PgPool, Row};

const MAX_RELAY_BODY_BYTES: usize = 64 * 1024 * 1024;
const MAX_UPSTREAM_ERROR_BODY_BYTES: usize = 1024 * 1024;
const COMPACT_MODEL_SUFFIX: &str = "-openai-compact";

/// Request metadata preserved for the relay service.
#[derive(Clone, Debug)]
pub struct OpenAiRelayRequest {
    /// Exact endpoint selected by the caller.  `protocol` alone cannot
    /// distinguish Chat Completions from the legacy Completions endpoint.
    pub endpoint: OpenAiRelayEndpoint,
    /// The API dialect requested by the caller.
    pub protocol: Protocol,
    /// The server request identifier used by audit records and legacy errors.
    pub request_id: String,
    /// Credentials and provider-compatible request headers.
    pub headers: HeaderMap,
    /// Typed, provider-neutral request content.
    pub request: CanonicalRequest,
    /// Original JSON bytes.  The transactional adapter uses these to replay a
    /// failed attempt and to retain provider fields which are intentionally
    /// outside the canonical cross-provider subset.
    pub raw_body: Vec<u8>,
}

/// OpenAI endpoint families sharing the relay executor.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum OpenAiRelayEndpoint {
    /// `POST /v1/completions`.
    Completions,
    /// `POST /v1/chat/completions`.
    ChatCompletions,
    /// `POST /v1/responses`.
    Responses,
    /// `POST /v1/responses/compact`.
    ResponsesCompact,
}

/// Request data used by the authentication gate before the JSON body is read.
#[derive(Clone, Debug)]
pub struct OpenAiRelayAuthorization {
    /// The server request identifier used by audit records and legacy errors.
    pub request_id: String,
    /// Credentials and provider-compatible request headers.
    pub headers: HeaderMap,
    /// Canonical client address established at the listener boundary.  It is
    /// deliberately not derived from a forwarded request header here.
    pub client_ip: Option<IpAddr>,
}

/// A non-streaming or streaming relay result after all side effects commit.
#[derive(Debug)]
pub struct OpenAiRelayResult {
    /// The selected upstream's successful HTTP status.
    pub status: StatusCode,
    /// Safe upstream response headers to forward to the API client.
    pub headers: HeaderMap,
    /// Canonical result content.
    pub body: OpenAiRelayBody,
}

/// Successful canonical relay content.
pub enum OpenAiRelayBody {
    /// A completed response.
    Complete(CanonicalResponse),
    /// Ordered SSE events. The service must include terminal usage/events.
    Stream(Vec<CanonicalStreamEvent>),
    /// An OpenAI-compatible upstream response whose wire body is already in
    /// the caller's requested dialect.  This preserves unknown provider
    /// fields and streams bytes as they arrive instead of buffering SSE.
    Upstream {
        /// Upstream content type, retained because it controls JSON versus SSE
        /// client handling.
        content_type: Option<HeaderValue>,
        /// Upstream byte stream after hop-by-hop headers are removed.
        body: Body,
    },
}

impl std::fmt::Debug for OpenAiRelayBody {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Complete(response) => formatter.debug_tuple("Complete").field(response).finish(),
            Self::Stream(events) => formatter.debug_tuple("Stream").field(events).finish(),
            Self::Upstream { content_type, .. } => formatter
                .debug_struct("Upstream")
                .field("content_type", content_type)
                .field("body", &"<stream>")
                .finish(),
        }
    }
}

/// A selected OpenAI-compatible upstream.  Channel selection and its secret
/// ownership remain in the transactional relay service; the client never
/// accepts this data from request headers.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct OpenAiUpstreamTarget {
    /// Base URL of the selected channel, with an optional path prefix.
    pub base_url: String,
    /// Credential to inject for the selected channel.
    pub api_key: String,
}

/// Concrete wire adapter for OpenAI-compatible selected channels.
///
/// The caller must have completed token validation, channel selection and any
/// pre-consumption before invoking this client.  It performs no retry because
/// retries must be coordinated with channel health and accounting, but every
/// call owns a fresh replay of [`OpenAiRelayRequest::raw_body`].
#[derive(Clone)]
pub struct OpenAiUpstreamClient {
    client: reqwest::Client,
    response_header_timeout: Duration,
}

impl OpenAiUpstreamClient {
    /// Creates an adapter with a finite response-header deadline.
    #[must_use]
    pub fn new(client: reqwest::Client, response_header_timeout: Duration) -> Self {
        Self {
            client,
            response_header_timeout,
        }
    }

    /// Forwards one selected request and preserves successful JSON or SSE wire
    /// output.  Non-success responses are converted to a safe relay failure so
    /// the transactional caller can apply legacy channel-error/retry policy.
    pub async fn forward(
        &self,
        target: &OpenAiUpstreamTarget,
        request: &OpenAiRelayRequest,
    ) -> Result<OpenAiRelayResult, OpenAiRelayFailure> {
        let url = upstream_url(&target.base_url, request.endpoint)
            .map_err(|()| invalid_target_failure())?;
        let upstream_request =
            copy_upstream_headers(self.client.post(url), &request.headers, &target.api_key)
                .body(request.raw_body.clone());
        let upstream = tokio::time::timeout(self.response_header_timeout, upstream_request.send())
            .await
            .map_err(|_| upstream_transport_failure("upstream response timed out"))?
            .map_err(|_| upstream_transport_failure("upstream request failed"))?;
        let status = upstream.status();
        let headers = upstream.headers().clone();
        if !status.is_success() {
            return Err(upstream_http_failure(status, headers, upstream).await);
        }
        let content_type = headers.get(header::CONTENT_TYPE).cloned();
        Ok(OpenAiRelayResult {
            status,
            headers,
            body: OpenAiRelayBody::Upstream {
                content_type,
                body: Body::from_stream(upstream.bytes_stream()),
            },
        })
    }
}

/// A legacy-compatible relay failure produced by auth, selection or upstream I/O.
#[derive(Clone, Debug)]
pub struct OpenAiRelayFailure {
    /// HTTP status returned to the caller.
    pub status: StatusCode,
    /// Legacy/OpenAI error code. An empty code is valid for token failures.
    pub code: String,
    /// Error text before the request-id suffix is added.
    pub message: String,
    /// Safe upstream error headers to forward, such as `retry-after`.
    pub headers: HeaderMap,
}

impl OpenAiRelayFailure {
    /// Creates a failure without provider headers.
    #[must_use]
    pub fn new(status: StatusCode, code: impl Into<String>, message: impl Into<String>) -> Self {
        Self {
            status,
            code: code.into(),
            message: message.into(),
            headers: HeaderMap::new(),
        }
    }
}

/// Transactional relay port.
///
/// Implementations authenticate the token, select a channel, execute the typed upstream conversion, and perform the
/// corresponding PostgreSQL/Valkey billing, usage-log and channel-health side
/// effects. A failure must already have applied any required refund/retry policy
/// before it is returned here.
#[async_trait]
pub trait OpenAiRelayService: Send + Sync {
    /// Authenticates a token before request parsing, matching legacy middleware order.
    async fn authenticate(
        &self,
        request: OpenAiRelayAuthorization,
    ) -> Result<(), OpenAiRelayFailure>;

    /// Relays one typed request after authentication and channel selection.
    async fn relay(
        &self,
        request: OpenAiRelayRequest,
    ) -> Result<OpenAiRelayResult, OpenAiRelayFailure>;
}

/// PostgreSQL-backed minimal production executor for OpenAI-compatible
/// channels.
///
/// It is intentionally a vertical, conservative path: PostgreSQL remains the
/// authority for token/user/channel state, a request is pre-charged under row
/// locks, and a failed upstream call refunds the exact reservation.  The
/// legacy schema stores `channels.key` as a server-only text field (rather
/// than an encrypted envelope), so this adapter only resolves it after the
/// channel is selected and never includes it in errors or logs.
#[derive(Clone)]
pub struct PgOpenAiRelayService {
    pg: PgPool,
    upstream: OpenAiUpstreamClient,
    /// Conservative fixed reservation for this vertical slice.  Composition
    /// must supply the legacy model-ratio calculator before this service is
    /// mounted for production ownership.
    quota_per_request: i64,
}

impl PgOpenAiRelayService {
    /// Creates the PostgreSQL executor. `quota_per_request` must be positive;
    /// a zero-cost relay is never a safe production default.
    #[must_use]
    pub fn new(pg: PgPool, upstream: OpenAiUpstreamClient, quota_per_request: i64) -> Self {
        Self {
            pg,
            upstream,
            quota_per_request: quota_per_request.max(1),
        }
    }

    async fn token_for_auth(
        &self,
        headers: &HeaderMap,
        client_ip: Option<IpAddr>,
    ) -> Result<(), OpenAiRelayFailure> {
        let key = token_key(headers).ok_or_else(unauthorized_failure)?;
        let now = epoch_seconds();
        let row = sqlx::query(
            r#"SELECT COALESCE(t.status,1) AS token_status,
                      COALESCE(t.expired_time,-1) AS expired_time,
                      COALESCE(t.remain_quota,0) AS remain_quota,
                      COALESCE(t.unlimited_quota,FALSE) AS unlimited_quota,
                      COALESCE(t.allow_ips,'') AS allow_ips,
                      COALESCE(u.status,1) AS user_status
               FROM tokens t JOIN users u ON u.id=t.user_id
               WHERE t.key=$1 AND t.deleted_at IS NULL AND u.deleted_at IS NULL"#,
        )
        .bind(key)
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| internal_failure())?
        .ok_or_else(unauthorized_failure)?;
        let valid = row.try_get::<i64, _>("token_status").ok() == Some(1)
            && row.try_get::<i64, _>("user_status").ok() == Some(1)
            && row
                .try_get::<i64, _>("expired_time")
                .is_ok_and(|expires| expires == -1 || expires >= now)
            && (row.try_get::<bool, _>("unlimited_quota").unwrap_or(false)
                || row
                    .try_get::<i64, _>("remain_quota")
                    .is_ok_and(|quota| quota > 0));
        if !valid {
            return Err(unauthorized_failure());
        }
        let allowed = row.try_get::<String, _>("allow_ips").unwrap_or_default();
        if !ip_is_allowed(client_ip, &allowed) {
            return Err(OpenAiRelayFailure::new(
                StatusCode::FORBIDDEN,
                "access_denied",
                "your IP is not allowed by this token",
            ));
        }
        Ok(())
    }

    async fn reserve(
        &self,
        request: &OpenAiRelayRequest,
    ) -> Result<Reservation, OpenAiRelayFailure> {
        let key = token_key(&request.headers).ok_or_else(unauthorized_failure)?;
        let now = epoch_seconds();
        let selection_model = channel_selection_model(request.endpoint, &request.request.model);
        let mut tx = self.pg.begin().await.map_err(|_| internal_failure())?;
        // Serialize an idempotency key even before a log row exists.  This is
        // transactional and does not create schema drift in the copied DB.
        sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended($1, 0))")
            .bind(format!("openai-relay:{key}:{}", request.request_id))
            .execute(&mut *tx)
            .await
            .map_err(|_| internal_failure())?;
        let replayed = sqlx::query_scalar::<_, i64>(
            "SELECT 1 FROM logs WHERE token_id=(SELECT id FROM tokens WHERE key=$1 AND deleted_at IS NULL) AND request_id=$2 AND type=2 LIMIT 1",
        )
        .bind(&key)
        .bind(&request.request_id)
        .fetch_optional(&mut *tx)
        .await
        .map_err(|_| internal_failure())?
        .is_some();
        if replayed {
            return Err(OpenAiRelayFailure::new(
                StatusCode::CONFLICT,
                "request_replayed",
                "request id has already been processed",
            ));
        }
        let row = sqlx::query(
            r#"SELECT t.id AS token_id, t.user_id,
                      COALESCE(t.status,1) AS token_status,
                      COALESCE(t.expired_time,-1) AS expired_time,
                      COALESCE(t.remain_quota,0) AS remain_quota,
                      COALESCE(t.unlimited_quota,FALSE) AS unlimited_quota,
                      COALESCE(u.status,1) AS user_status,
                      COALESCE(u.quota,0) AS user_quota,
                      c.id AS channel_id, COALESCE(c.base_url,'') AS base_url,
                      c.key AS channel_key
               FROM tokens t JOIN users u ON u.id=t.user_id
               JOIN abilities a ON a."group"=t."group" AND a.model=$2
                   AND COALESCE(a.enabled,TRUE)
               JOIN channels c ON c.id=a.channel_id
               WHERE t.key=$1 AND t.deleted_at IS NULL AND u.deleted_at IS NULL
                   AND COALESCE(c.status,1)=1
               ORDER BY COALESCE(a.priority,0) DESC, COALESCE(a.weight,0) DESC, c.id
               LIMIT 1 FOR UPDATE OF t,u,c"#,
        )
        .bind(&key)
        .bind(selection_model)
        .fetch_optional(&mut *tx)
        .await
        .map_err(|_| internal_failure())?
        .ok_or_else(no_channel_failure)?;
        let token_status: i64 = row
            .try_get("token_status")
            .map_err(|_| internal_failure())?;
        let user_status: i64 = row.try_get("user_status").map_err(|_| internal_failure())?;
        let expires: i64 = row
            .try_get("expired_time")
            .map_err(|_| internal_failure())?;
        let unlimited: bool = row
            .try_get("unlimited_quota")
            .map_err(|_| internal_failure())?;
        let token_quota: i64 = row
            .try_get("remain_quota")
            .map_err(|_| internal_failure())?;
        let user_quota: i64 = row.try_get("user_quota").map_err(|_| internal_failure())?;
        if token_status != 1 || user_status != 1 || (expires != -1 && expires < now) {
            return Err(unauthorized_failure());
        }
        if (!unlimited && token_quota < self.quota_per_request)
            || user_quota < self.quota_per_request
        {
            return Err(OpenAiRelayFailure::new(
                StatusCode::FORBIDDEN,
                "insufficient_quota",
                "insufficient quota",
            ));
        }
        let token_id: i64 = row.try_get("token_id").map_err(|_| internal_failure())?;
        let user_id: i64 = row.try_get("user_id").map_err(|_| internal_failure())?;
        let channel_id: i64 = row.try_get("channel_id").map_err(|_| internal_failure())?;
        sqlx::query("UPDATE users SET quota=COALESCE(quota,0)-$2,used_quota=COALESCE(used_quota,0)+$2,request_count=COALESCE(request_count,0)+1 WHERE id=$1")
            .bind(user_id).bind(self.quota_per_request).execute(&mut *tx).await.map_err(|_| internal_failure())?;
        sqlx::query("UPDATE tokens SET accessed_time=$2,used_quota=COALESCE(used_quota,0)+$3,remain_quota=CASE WHEN COALESCE(unlimited_quota,FALSE) THEN remain_quota ELSE COALESCE(remain_quota,0)-$3 END WHERE id=$1 AND user_id=$4")
            .bind(token_id).bind(now).bind(self.quota_per_request).bind(user_id).execute(&mut *tx).await.map_err(|_| internal_failure())?;
        sqlx::query("UPDATE channels SET used_quota=COALESCE(used_quota,0)+$2 WHERE id=$1")
            .bind(channel_id)
            .bind(self.quota_per_request)
            .execute(&mut *tx)
            .await
            .map_err(|_| internal_failure())?;
        tx.commit().await.map_err(|_| internal_failure())?;
        let raw_key: String = row.try_get("channel_key").map_err(|_| internal_failure())?;
        let api_key = raw_key
            .lines()
            .map(str::trim)
            .find(|value| !value.is_empty())
            .unwrap_or_default()
            .to_owned();
        let base_url: String = row.try_get("base_url").map_err(|_| internal_failure())?;
        if api_key.is_empty() || base_url.trim().is_empty() {
            self.refund(token_id, user_id, channel_id).await?;
            return Err(no_channel_failure());
        }
        Ok(Reservation {
            token_id,
            user_id,
            channel_id,
            target: OpenAiUpstreamTarget { base_url, api_key },
        })
    }

    async fn refund(
        &self,
        token_id: i64,
        user_id: i64,
        channel_id: i64,
    ) -> Result<(), OpenAiRelayFailure> {
        let mut tx = self.pg.begin().await.map_err(|_| internal_failure())?;
        sqlx::query("UPDATE users SET quota=COALESCE(quota,0)+$2,used_quota=GREATEST(COALESCE(used_quota,0)-$2,0),request_count=GREATEST(COALESCE(request_count,0)-1,0) WHERE id=$1").bind(user_id).bind(self.quota_per_request).execute(&mut *tx).await.map_err(|_| internal_failure())?;
        sqlx::query("UPDATE tokens SET used_quota=GREATEST(COALESCE(used_quota,0)-$2,0),remain_quota=CASE WHEN COALESCE(unlimited_quota,FALSE) THEN remain_quota ELSE COALESCE(remain_quota,0)+$2 END WHERE id=$1 AND user_id=$3").bind(token_id).bind(self.quota_per_request).bind(user_id).execute(&mut *tx).await.map_err(|_| internal_failure())?;
        sqlx::query(
            "UPDATE channels SET used_quota=GREATEST(COALESCE(used_quota,0)-$2,0) WHERE id=$1",
        )
        .bind(channel_id)
        .bind(self.quota_per_request)
        .execute(&mut *tx)
        .await
        .map_err(|_| internal_failure())?;
        tx.commit().await.map_err(|_| internal_failure())
    }

    async fn log_success(
        &self,
        reservation: &Reservation,
        request: &OpenAiRelayRequest,
    ) -> Result<(), OpenAiRelayFailure> {
        sqlx::query("INSERT INTO logs (user_id,created_at,type,content,model_name,quota,channel_id,token_id,\"group\",request_id,is_stream) VALUES ($1,$2,2,'',$3,$4,$5,$6,'',$7,$8)")
            .bind(reservation.user_id).bind(epoch_seconds()).bind(&request.request.model).bind(self.quota_per_request).bind(reservation.channel_id).bind(reservation.token_id).bind(&request.request_id).bind(request.request.stream)
            .execute(&self.pg).await.map_err(|_| internal_failure())?;
        Ok(())
    }
}

struct Reservation {
    token_id: i64,
    user_id: i64,
    channel_id: i64,
    target: OpenAiUpstreamTarget,
}

#[async_trait]
impl OpenAiRelayService for PgOpenAiRelayService {
    async fn authenticate(
        &self,
        request: OpenAiRelayAuthorization,
    ) -> Result<(), OpenAiRelayFailure> {
        self.token_for_auth(&request.headers, request.client_ip)
            .await
    }

    async fn relay(
        &self,
        request: OpenAiRelayRequest,
    ) -> Result<OpenAiRelayResult, OpenAiRelayFailure> {
        let reservation = self.reserve(&request).await?;
        match self.upstream.forward(&reservation.target, &request).await {
            Ok(result) => {
                if let Err(error) = self.log_success(&reservation, &request).await {
                    self.refund(
                        reservation.token_id,
                        reservation.user_id,
                        reservation.channel_id,
                    )
                    .await?;
                    return Err(error);
                }
                Ok(result)
            }
            Err(error) => {
                self.refund(
                    reservation.token_id,
                    reservation.user_id,
                    reservation.channel_id,
                )
                .await?;
                Err(error)
            }
        }
    }
}

/// Shared state for the OpenAI relay HTTP routes.
#[derive(Clone)]
pub struct OpenAiRelayHttpState {
    service: Arc<dyn OpenAiRelayService>,
    version: Arc<str>,
    protocol_rollout: ProtocolRolloutControl,
    validated_registry: Option<Arc<ValidatedRegistry>>,
}

impl OpenAiRelayHttpState {
    /// Creates relay state with the legacy version response header value.
    #[must_use]
    pub fn new(service: Arc<dyn OpenAiRelayService>, version: impl Into<Arc<str>>) -> Self {
        Self {
            service,
            version: version.into(),
            protocol_rollout: ProtocolRolloutControl::default(),
            validated_registry: validated_current_registry().ok().map(Arc::new),
        }
    }

    /// Replaces the default runtime controls with the process-shared rollout
    /// holder and validated capability snapshot.
    #[must_use]
    pub fn with_protocol_runtime(
        mut self,
        rollout: ProtocolRolloutControl,
        registry: Arc<ValidatedRegistry>,
    ) -> Self {
        self.protocol_rollout = rollout;
        self.validated_registry = Some(registry);
        self
    }
}

/// Builds the OpenAI Chat Completions and Responses relay routes.
pub fn openai_relay_router(state: OpenAiRelayHttpState) -> Router {
    Router::new()
        .route("/v1/completions", post(completions))
        .route("/v1/chat/completions", post(chat_completions))
        .route("/v1/responses", post(responses))
        .route("/v1/responses/compact", post(responses_compact))
        .with_state(state)
}

async fn completions(State(state): State<OpenAiRelayHttpState>, request: Request) -> Response {
    relay(
        state,
        request,
        Protocol::OpenAi,
        OpenAiRelayEndpoint::Completions,
    )
    .await
}

async fn chat_completions(State(state): State<OpenAiRelayHttpState>, request: Request) -> Response {
    relay(
        state,
        request,
        Protocol::OpenAi,
        OpenAiRelayEndpoint::ChatCompletions,
    )
    .await
}

async fn responses(State(state): State<OpenAiRelayHttpState>, request: Request) -> Response {
    relay(
        state,
        request,
        Protocol::OpenAiResponses,
        OpenAiRelayEndpoint::Responses,
    )
    .await
}

async fn responses_compact(
    State(state): State<OpenAiRelayHttpState>,
    request: Request,
) -> Response {
    relay(
        state,
        request,
        Protocol::OpenAiResponses,
        OpenAiRelayEndpoint::ResponsesCompact,
    )
    .await
}

async fn relay(
    state: OpenAiRelayHttpState,
    request: Request,
    protocol: Protocol,
    endpoint: OpenAiRelayEndpoint,
) -> Response {
    let observer = global_observer();
    let request_id = request_id(&request);
    let headers = request.headers().clone();
    if let Err(error) = state
        .service
        .authenticate(OpenAiRelayAuthorization {
            request_id: request_id.clone(),
            headers: headers.clone(),
            client_ip: request
                .extensions()
                .get::<RequestContext>()
                .and_then(|context| context.client_ip),
        })
        .await
    {
        return legacy_error(&state, &request_id, error);
    }
    let body = match to_bytes(request.into_body(), MAX_RELAY_BODY_BYTES).await {
        Ok(body) => body,
        Err(_) => {
            observer.record_failure(openai_conversion_labels(
                protocol,
                false,
                ConversionResult::Failure,
            ));
            return legacy_error(
                &state,
                &request_id,
                OpenAiRelayFailure::new(
                    StatusCode::PAYLOAD_TOO_LARGE,
                    "invalid_request_error",
                    "request body too large",
                ),
            );
        }
    };
    let parse_started = Instant::now();
    let canonical = match parse_request(endpoint, &body) {
        Ok(request) => {
            let labels =
                openai_conversion_labels(protocol, request.stream, ConversionResult::Success);
            observer.record_input_bytes(labels, body.len());
            observer.record_conversion_duration(labels, parse_started.elapsed());
            request
        }
        Err(error) => {
            let labels = openai_conversion_labels(protocol, false, ConversionResult::Failure);
            observer.record_input_bytes(labels, body.len());
            observer.record_conversion_duration(labels, parse_started.elapsed());
            observer.record_failure(labels);
            return legacy_error(
                &state,
                &request_id,
                OpenAiRelayFailure::new(
                    StatusCode::BAD_REQUEST,
                    "invalid_request_error",
                    error.to_string(),
                ),
            );
        }
    };
    let stream = canonical.stream;
    let admission = match native_route_admission(
        &state,
        &request_id,
        protocol,
        canonical.model.as_str(),
        stream,
    ) {
        Ok(admission) => admission,
        Err(reason) => {
            observer.record_failure_with_reason(
                openai_conversion_labels(protocol, stream, ConversionResult::Failure),
                reason,
            );
            return legacy_error(&state, &request_id, route_closed_failure());
        }
    };
    let NativeRouteAdmission { registry, details } = admission;
    let plan_started = Instant::now();
    let plan_labels = openai_conversion_labels(protocol, stream, ConversionResult::Success);
    let plan = ConversionPlan::compile_with_validated_registry(
        protocol,
        protocol,
        canonical.model.as_str(),
        &registry,
    );
    observer.record_plan_duration(plan_labels, plan_started.elapsed());
    let compiled_plan = match plan {
        Ok(plan) => plan,
        Err(_) => {
            observer.record_failure_with_reason(plan_labels, FailureReason::RegistryDrift);
            return legacy_error(
                &state,
                &request_id,
                OpenAiRelayFailure::new(
                    StatusCode::BAD_REQUEST,
                    "conversion_unsupported_feature",
                    "requested conversion route is unavailable",
                ),
            );
        }
    };
    if let Err(error) = compiled_plan.enforce(details.loss_policy) {
        observer.record_failure_with_reason(plan_labels, FailureReason::PlanRejected);
        return legacy_error(
            &state,
            &request_id,
            OpenAiRelayFailure::new(
                StatusCode::BAD_REQUEST,
                error.code,
                "requested conversion plan is not executable",
            ),
        );
    }
    let relay_request = OpenAiRelayRequest {
        endpoint,
        protocol,
        request_id: request_id.clone(),
        headers,
        request: canonical,
        raw_body: body.to_vec(),
    };
    let result = match state.service.relay(relay_request).await {
        Ok(result) => result,
        Err(error) => {
            observer.record_failure(openai_conversion_labels(
                protocol,
                stream,
                ConversionResult::Failure,
            ));
            return legacy_error(&state, &request_id, error);
        }
    };
    legacy_success(&state, &request_id, protocol, result)
}

struct NativeRouteAdmission {
    registry: Arc<ValidatedRegistry>,
    details: RouteGateDetails,
}

fn native_route_admission(
    state: &OpenAiRelayHttpState,
    request_id: &str,
    protocol: Protocol,
    model: &str,
    stream: bool,
) -> Result<NativeRouteAdmission, FailureReason> {
    let Some(registry) = state.validated_registry.clone() else {
        return Err(FailureReason::RegistryDrift);
    };
    let context = RolloutContext::new(request_id, protocol, protocol, model, stream);
    let scope = RouteOwnershipScope {
        source: protocol,
        target: protocol,
        stream,
    };
    let evidence = OwnershipEvidence::closed(scope);
    let rollout_snapshot = state.protocol_rollout.snapshot();
    let request_gate = decide_route(
        rollout_snapshot.config(),
        &context,
        &registry,
        Direction::Request,
        &evidence,
    );
    let output_direction = if stream {
        Direction::Stream
    } else {
        Direction::Response
    };
    let output_gate = decide_route(
        rollout_snapshot.config(),
        &context,
        &registry,
        output_direction,
        &evidence,
    );
    if !is_exact_raw_native(&request_gate) {
        return Err(route_gate_failure_reason(&request_gate));
    }
    if !is_exact_raw_native(&output_gate) {
        return Err(route_gate_failure_reason(&output_gate));
    }
    let RouteGateDecision::NativeRaw { details } = output_gate else {
        return Err(FailureReason::Unsupported);
    };
    Ok(NativeRouteAdmission { registry, details })
}

fn is_exact_raw_native(decision: &RouteGateDecision) -> bool {
    let RouteGateDecision::NativeRaw { details } = decision else {
        return false;
    };
    details.capability.as_ref().is_some_and(|capability| {
        capability.quality == Fidelity::Exact && capability.raw_passthrough
    })
}

fn route_gate_failure_reason(decision: &RouteGateDecision) -> FailureReason {
    if decision
        .blockers()
        .iter()
        .any(|blocker| matches!(blocker, RouteGateBlocker::RegistryUnavailable))
    {
        FailureReason::RegistryDrift
    } else {
        FailureReason::Unsupported
    }
}

fn parse_request(
    endpoint: OpenAiRelayEndpoint,
    body: &[u8],
) -> Result<CanonicalRequest, RelayConvertError> {
    match endpoint {
        OpenAiRelayEndpoint::ChatCompletions
        | OpenAiRelayEndpoint::Responses
        | OpenAiRelayEndpoint::ResponsesCompact => minimal_openai_request_to_canonical(body),
        OpenAiRelayEndpoint::Completions => completion_request_to_canonical(body),
    }
}

/// Extracts only the metadata required by the same-protocol transactional
/// relay.  The selected upstream receives [`OpenAiRelayRequest::raw_body`]
/// unchanged, so provider fields, future tools/items and unknown SSE shapes
/// never pass through a closed canonical DTO on a native OpenAI route.
fn minimal_openai_request_to_canonical(body: &[u8]) -> Result<CanonicalRequest, RelayConvertError> {
    let value: serde_json::Value = serde_json::from_slice(body).map_err(RelayConvertError::from)?;
    let model = value
        .get("model")
        .and_then(serde_json::Value::as_str)
        .filter(|model| !model.trim().is_empty())
        .ok_or(RelayConvertError::Missing("model"))?;
    let stream = match value.get("stream") {
        None => false,
        Some(value) => value
            .as_bool()
            .ok_or_else(|| RelayConvertError::Unsupported("stream must be a boolean".to_owned()))?,
    };
    Ok(CanonicalRequest {
        model: model.to_owned(),
        instructions: Vec::new(),
        messages: Vec::new(),
        max_output_tokens: None,
        temperature: None,
        stream,
        tools: Vec::new(),
        tool_choice: None,
        options: lmm_contracts::relay::RequestOptions::default(),
    })
}

fn channel_selection_model(endpoint: OpenAiRelayEndpoint, model: &str) -> String {
    if endpoint == OpenAiRelayEndpoint::ResponsesCompact && !model.ends_with(COMPACT_MODEL_SUFFIX) {
        format!("{model}{COMPACT_MODEL_SUFFIX}")
    } else {
        model.to_owned()
    }
}

fn completion_request_to_canonical(body: &[u8]) -> Result<CanonicalRequest, RelayConvertError> {
    let value: serde_json::Value = serde_json::from_slice(body).map_err(RelayConvertError::from)?;
    let model = value
        .get("model")
        .and_then(serde_json::Value::as_str)
        .filter(|model| !model.trim().is_empty())
        .ok_or(RelayConvertError::Missing("model"))?;
    let prompt = value
        .get("prompt")
        .and_then(serde_json::Value::as_str)
        .ok_or(RelayConvertError::Missing("prompt"))?;
    Ok(CanonicalRequest {
        model: model.to_owned(),
        instructions: Vec::new(),
        messages: vec![lmm_contracts::relay::CanonicalMessage {
            role: lmm_contracts::relay::Role::User,
            parts: vec![lmm_contracts::relay::CanonicalContent::Text {
                text: prompt.to_owned(),
            }],
        }],
        max_output_tokens: value.get("max_tokens").and_then(serde_json::Value::as_u64),
        temperature: value.get("temperature").and_then(serde_json::Value::as_f64),
        stream: value
            .get("stream")
            .and_then(serde_json::Value::as_bool)
            .unwrap_or(false),
        tools: Vec::new(),
        tool_choice: None,
        options: lmm_contracts::relay::RequestOptions {
            top_p: value.get("top_p").and_then(serde_json::Value::as_f64),
            ..Default::default()
        },
    })
}

fn legacy_success(
    state: &OpenAiRelayHttpState,
    request_id: &str,
    protocol: Protocol,
    result: OpenAiRelayResult,
) -> Response {
    let observer = global_observer();
    let mut response = match result.body {
        OpenAiRelayBody::Complete(response) => {
            let labels = openai_conversion_labels(protocol, false, ConversionResult::Success);
            let conversion_started = Instant::now();
            match serialize_complete(protocol, response) {
                Ok(body) => {
                    observer.record_conversion_duration(labels, conversion_started.elapsed());
                    observer.record_events(labels, 1);
                    observer.record_output_bytes(labels, body.len());
                    let mut response = (result.status, body).into_response();
                    response.headers_mut().insert(
                        header::CONTENT_TYPE,
                        HeaderValue::from_static("application/json; charset=utf-8"),
                    );
                    response
                }
                Err(error) => {
                    observer.record_conversion_duration(labels, conversion_started.elapsed());
                    observer.record_failure(labels);
                    return legacy_error(
                        state,
                        request_id,
                        OpenAiRelayFailure::new(
                            StatusCode::BAD_GATEWAY,
                            "upstream_protocol_error",
                            error.to_string(),
                        ),
                    );
                }
            }
        }
        OpenAiRelayBody::Stream(events) => {
            let labels = openai_conversion_labels(protocol, true, ConversionResult::Success);
            let conversion_started = Instant::now();
            let mut timing = StreamTiming::default();
            if !events.is_empty() {
                timing.mark_upstream_event();
            }
            let body = match serialize_sse(protocol, &events) {
                Ok(body) => {
                    observer.record_conversion_duration(labels, conversion_started.elapsed());
                    observer.record_events(labels, usize_to_u64(events.len()));
                    body
                }
                Err(error) => {
                    observer.record_conversion_duration(labels, conversion_started.elapsed());
                    observer.record_failure(labels);
                    return legacy_error(
                        state,
                        request_id,
                        OpenAiRelayFailure::new(
                            StatusCode::BAD_GATEWAY,
                            "upstream_protocol_error",
                            error.to_string(),
                        ),
                    );
                }
            };
            let body =
                observe_body_with_timing(Body::from(body), (*observer).clone(), labels, timing);
            let mut response = Response::new(body);
            *response.status_mut() = result.status;
            response.headers_mut().insert(
                header::CONTENT_TYPE,
                HeaderValue::from_static("text/event-stream; charset=utf-8"),
            );
            response
                .headers_mut()
                .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-cache"));
            response.headers_mut().insert(
                HeaderName::from_static("x-accel-buffering"),
                HeaderValue::from_static("no"),
            );
            response
        }
        OpenAiRelayBody::Upstream { content_type, body } => {
            // Native passthrough records bytes/result only. It intentionally
            // does not decode JSON or inspect provider event names.
            let stream = content_type
                .as_ref()
                .and_then(|value| value.to_str().ok())
                .and_then(|value| value.split(';').next())
                .is_some_and(|value| value.trim().eq_ignore_ascii_case("text/event-stream"));
            let labels = MetricLabels::native_raw(protocol, stream, ConversionResult::Success);
            let body = observe_body_with_timing(
                body,
                (*observer).clone(),
                labels,
                StreamTiming::default(),
            );
            let mut response = Response::new(body);
            *response.status_mut() = result.status;
            if let Some(content_type) = content_type {
                response
                    .headers_mut()
                    .insert(header::CONTENT_TYPE, content_type);
            }
            if stream {
                response.headers_mut().insert(
                    HeaderName::from_static("x-accel-buffering"),
                    HeaderValue::from_static("no"),
                );
            }
            response
        }
    };
    copy_safe_headers(&result.headers, response.headers_mut());
    attach_legacy_headers(state, request_id, &mut response);
    response
}

fn serialize_complete(
    protocol: Protocol,
    response: CanonicalResponse,
) -> Result<String, RelayConvertError> {
    match protocol {
        Protocol::OpenAi => {
            serde_json::to_string(&canonical_response_to_openai_chat(response).value)
                .map_err(RelayConvertError::from)
        }
        Protocol::OpenAiResponses => {
            serde_json::to_string(&canonical_response_to_openai_responses(response).value)
                .map_err(RelayConvertError::from)
        }
        Protocol::Claude | Protocol::Gemini => Err(RelayConvertError::Unsupported(
            "OpenAI serializer does not support this protocol".to_owned(),
        )),
    }
}

fn serialize_sse(
    protocol: Protocol,
    events: &[CanonicalStreamEvent],
) -> Result<String, RelayConvertError> {
    let mut output = String::new();
    match protocol {
        Protocol::OpenAi => {
            for chunk in response_events_to_openai_chunks(events) {
                let json = serde_json::to_string(&chunk).map_err(RelayConvertError::from)?;
                output.push_str("data: ");
                output.push_str(&json);
                output.push_str("\n\n");
            }
        }
        Protocol::OpenAiResponses => {
            for event in response_events_to_responses(events) {
                let json =
                    serde_json::to_string(&event.payload).map_err(RelayConvertError::from)?;
                output.push_str("event: ");
                output.push_str(&event.kind);
                output.push_str("\ndata: ");
                output.push_str(&json);
                output.push_str("\n\n");
            }
        }
        Protocol::Claude | Protocol::Gemini => {
            return Err(RelayConvertError::Unsupported(
                "OpenAI SSE serializer does not support this protocol".to_owned(),
            ));
        }
    }
    output.push_str("data: [DONE]\n\n");
    Ok(output)
}

fn openai_conversion_labels(
    protocol: Protocol,
    stream: bool,
    result: ConversionResult,
) -> MetricLabels {
    let converter_version = match protocol {
        Protocol::OpenAi => ConverterVersion::OpenAiChatV1,
        Protocol::OpenAiResponses => ConverterVersion::OpenAiResponsesV1,
        Protocol::Claude | Protocol::Gemini => ConverterVersion::NativeRawV1,
    };
    MetricLabels::new(
        protocol,
        protocol,
        converter_version,
        0,
        stream,
        if stream {
            FeatureClass::Stream
        } else {
            FeatureClass::Text
        },
        result,
    )
}

fn usize_to_u64(value: usize) -> u64 {
    if value > u64::MAX as usize {
        u64::MAX
    } else {
        value as u64
    }
}

fn observe_body_with_timing(
    body: Body,
    observer: ConversionObserver,
    labels: MetricLabels,
    timing: StreamTiming,
) -> Body {
    let stream = body.into_data_stream();
    if !labels.stream {
        let observed = stream.map(move |item| {
            if let Ok(bytes) = &item {
                observer.record_output_bytes(labels, bytes.len());
            }
            item
        });
        return Body::from_stream(observed);
    }
    let guard = ClientAbortGuard::new(observer.clone(), labels);
    let queue_guard = observer.enter_queue(labels);
    let observed = futures_util::stream::unfold(
        (stream, guard, queue_guard, timing),
        move |(mut stream, mut guard, mut queue_guard, mut timing)| {
            let observer = observer.clone();
            async move {
                match stream.next().await {
                    Some(Ok(bytes)) => {
                        timing.mark_upstream_event();
                        if timing.first_downstream_write_at.is_none() {
                            timing.mark_downstream_write();
                            timing.record_gateway_ttft(&observer, labels);
                        }
                        observer.record_output_bytes(labels, bytes.len());
                        Some((Ok(bytes), (stream, guard, queue_guard, timing)))
                    }
                    Some(Err(error)) => {
                        guard.complete();
                        queue_guard.complete();
                        observer.record_failure(labels);
                        Some((Err(error), (stream, guard, queue_guard, timing)))
                    }
                    None => {
                        guard.complete();
                        queue_guard.complete();
                        None
                    }
                }
            }
        },
    );
    Body::from_stream(observed)
}

fn legacy_error(
    state: &OpenAiRelayHttpState,
    request_id: &str,
    failure: OpenAiRelayFailure,
) -> Response {
    let mut response = (
        failure.status,
        axum::Json(OpenAiErrorEnvelope {
            error: OpenAiError {
                message: format!("{} (request id: {request_id})", failure.message),
                kind: "new_api_error",
                code: failure.code,
            },
        }),
    )
        .into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    copy_safe_headers(&failure.headers, response.headers_mut());
    attach_legacy_headers(state, request_id, &mut response);
    response
}

#[derive(Serialize)]
struct OpenAiErrorEnvelope {
    error: OpenAiError,
}

#[derive(Serialize)]
struct OpenAiError {
    message: String,
    #[serde(rename = "type")]
    kind: &'static str,
    code: String,
}

fn request_id(request: &Request) -> String {
    if let Some(context) = request.extensions().get::<RequestContext>() {
        return context.request_id.clone();
    }
    request
        .headers()
        .get("x-oneapi-request-id")
        .and_then(|value| value.to_str().ok())
        .filter(|value| !value.is_empty())
        .map_or_else(|| uuid::Uuid::new_v4().to_string(), str::to_owned)
}

fn token_key(headers: &HeaderMap) -> Option<String> {
    let raw = headers
        .get(header::AUTHORIZATION)
        .or_else(|| headers.get("api-key"))?
        .to_str()
        .ok()?
        .trim();
    let raw = raw
        .strip_prefix("Bearer ")
        .or_else(|| raw.strip_prefix("bearer "))
        .unwrap_or(raw)
        .trim()
        .strip_prefix("sk-")
        .unwrap_or(raw);
    let key = raw.split('-').next().unwrap_or_default().trim();
    (!key.is_empty()).then(|| key.to_owned())
}

fn ip_is_allowed(client_ip: Option<IpAddr>, raw_limits: &str) -> bool {
    let limits = raw_limits
        .lines()
        .map(|line| line.replace([' ', ','], ""))
        .filter(|line| !line.is_empty())
        .collect::<Vec<_>>();
    limits.is_empty()
        || client_ip.is_some_and(|client_ip| {
            limits.iter().any(|limit| {
                limit
                    .parse::<ipnet::IpNet>()
                    .is_ok_and(|network| network.contains(&client_ip))
                    || limit
                        .parse::<IpAddr>()
                        .is_ok_and(|address| address == client_ip)
            })
        })
}

fn epoch_seconds() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| {
            i64::try_from(duration.as_secs()).unwrap_or(i64::MAX)
        })
}

fn unauthorized_failure() -> OpenAiRelayFailure {
    OpenAiRelayFailure::new(StatusCode::UNAUTHORIZED, "", "Invalid token")
}

fn no_channel_failure() -> OpenAiRelayFailure {
    OpenAiRelayFailure::new(
        StatusCode::SERVICE_UNAVAILABLE,
        "channel_not_found",
        "no available channel for this model",
    )
}

fn route_closed_failure() -> OpenAiRelayFailure {
    OpenAiRelayFailure::new(
        StatusCode::SERVICE_UNAVAILABLE,
        "relay_unavailable",
        "relay request could not be completed",
    )
}

fn internal_failure() -> OpenAiRelayFailure {
    OpenAiRelayFailure::new(
        StatusCode::INTERNAL_SERVER_ERROR,
        "internal_error",
        "relay storage operation failed",
    )
}

fn attach_legacy_headers(state: &OpenAiRelayHttpState, request_id: &str, response: &mut Response) {
    if let Ok(version) = HeaderValue::from_str(&state.version) {
        response.headers_mut().insert("x-new-api-version", version);
    }
    if let Ok(request_id) = HeaderValue::from_str(request_id) {
        response
            .headers_mut()
            .insert("x-oneapi-request-id", request_id);
    }
}

fn copy_safe_headers(source: &HeaderMap, target: &mut HeaderMap) {
    for (name, value) in source {
        if !is_hop_by_hop(name)
            && name != header::CONTENT_TYPE
            && name != header::CONTENT_LENGTH
            && name != "x-accel-buffering"
            && name != "x-new-api-version"
            && name != "x-oneapi-request-id"
        {
            target.append(name, value.clone());
        }
    }
}

fn is_hop_by_hop(name: &HeaderName) -> bool {
    matches!(
        name.as_str(),
        "connection"
            | "keep-alive"
            | "proxy-authenticate"
            | "proxy-authorization"
            | "te"
            | "trailer"
            | "transfer-encoding"
            | "upgrade"
    )
}

fn upstream_url(base_url: &str, endpoint: OpenAiRelayEndpoint) -> Result<reqwest::Url, ()> {
    let mut base = reqwest::Url::parse(base_url).map_err(|_| ())?;
    if !matches!(base.scheme(), "http" | "https")
        || base.host_str().is_none()
        || base.query().is_some()
        || base.fragment().is_some()
    {
        return Err(());
    }
    if !base.path().ends_with('/') {
        base.set_path(&format!("{}/", base.path()));
    }
    base.join(upstream_path(endpoint)).map_err(|_| ())
}

const fn upstream_path(endpoint: OpenAiRelayEndpoint) -> &'static str {
    match endpoint {
        OpenAiRelayEndpoint::Completions => "v1/completions",
        OpenAiRelayEndpoint::ChatCompletions => "v1/chat/completions",
        OpenAiRelayEndpoint::Responses => "v1/responses",
        OpenAiRelayEndpoint::ResponsesCompact => "v1/responses/compact",
    }
}

fn copy_upstream_headers(
    mut request: reqwest::RequestBuilder,
    inbound: &HeaderMap,
    api_key: &str,
) -> reqwest::RequestBuilder {
    for (name, value) in inbound {
        if should_forward_upstream_header(name) {
            request = request.header(name, value);
        }
    }
    if !api_key.is_empty() {
        request = request.header(header::AUTHORIZATION, format!("Bearer {api_key}"));
    }
    request
}

fn should_forward_upstream_header(name: &HeaderName) -> bool {
    name != header::HOST
        && name != header::CONTENT_LENGTH
        && name != "x-new-api-version"
        && name != "x-oneapi-request-id"
        && !is_hop_by_hop(name)
        && !is_sensitive_client_credential_header(name)
}

/// Client credentials belong only to the relay authentication boundary.  The
/// selected channel's server credential is injected after this filter, so an
/// inbound value cannot override or accompany it at a third-party upstream.
fn is_sensitive_client_credential_header(name: &HeaderName) -> bool {
    name.as_str().starts_with("sec-websocket-")
        || matches!(
            name.as_str(),
            "authorization"
                | "cookie"
                | "proxy-authorization"
                | "x-api-key"
                | "x-goog-api-key"
                | "api-key"
        )
}

fn invalid_target_failure() -> OpenAiRelayFailure {
    OpenAiRelayFailure::new(
        StatusCode::BAD_GATEWAY,
        "upstream_error",
        "invalid upstream target",
    )
}

fn upstream_transport_failure(message: &'static str) -> OpenAiRelayFailure {
    OpenAiRelayFailure::new(
        StatusCode::INTERNAL_SERVER_ERROR,
        "do_request_failed",
        message,
    )
}

async fn upstream_http_failure(
    status: StatusCode,
    headers: HeaderMap,
    mut response: reqwest::Response,
) -> OpenAiRelayFailure {
    let mut body = Vec::new();
    while body.len() < MAX_UPSTREAM_ERROR_BODY_BYTES {
        let chunk = match response.chunk().await {
            Ok(Some(chunk)) => chunk,
            Ok(None) | Err(_) => break,
        };
        let remaining = MAX_UPSTREAM_ERROR_BODY_BYTES - body.len();
        body.extend_from_slice(&chunk[..chunk.len().min(remaining)]);
    }
    let (code, message) = serde_json::from_slice::<UpstreamErrorEnvelope>(&body)
        .ok()
        .and_then(|envelope| envelope.error)
        .map_or_else(
            || {
                (
                    "upstream_error".to_owned(),
                    "upstream returned an error".to_owned(),
                )
            },
            |error| {
                (
                    error.code.unwrap_or_else(|| "upstream_error".to_owned()),
                    nonempty_upstream_message(error.message),
                )
            },
        );
    OpenAiRelayFailure {
        status,
        code,
        message,
        headers,
    }
}

fn nonempty_upstream_message(message: String) -> String {
    if message.trim().is_empty() {
        "upstream returned an error".to_owned()
    } else {
        message
    }
}

#[derive(serde::Deserialize)]
struct UpstreamErrorEnvelope {
    error: Option<UpstreamError>,
}

#[derive(serde::Deserialize)]
struct UpstreamError {
    #[serde(default)]
    message: String,
    code: Option<String>,
}

#[cfg(test)]
mod tests {
    use std::{sync::Arc, time::Duration};

    use async_trait::async_trait;
    use axum::{
        body::{Body, Bytes},
        http::{Request, Uri},
    };
    use tokio::{net::TcpListener, sync::mpsc, task::JoinHandle};
    use tower::ServiceExt;

    use super::*;

    type TestResult<T = ()> = Result<T, Box<dyn std::error::Error>>;

    fn test_error(message: impl Into<String>) -> Box<dyn std::error::Error> {
        Box::new(std::io::Error::other(message.into()))
    }

    fn with_context<T, E: std::fmt::Debug>(
        result: Result<T, E>,
        context: &'static str,
    ) -> TestResult<T> {
        result.map_err(|error| test_error(format!("{context}: {error:?}")))
    }

    fn required<T>(value: Option<T>, context: &'static str) -> TestResult<T> {
        value.ok_or_else(|| test_error(context))
    }

    struct TestRelayService;

    #[async_trait]
    impl OpenAiRelayService for TestRelayService {
        async fn authenticate(
            &self,
            _request: OpenAiRelayAuthorization,
        ) -> Result<(), OpenAiRelayFailure> {
            Ok(())
        }

        async fn relay(
            &self,
            _request: OpenAiRelayRequest,
        ) -> Result<OpenAiRelayResult, OpenAiRelayFailure> {
            Err(internal_failure())
        }
    }

    fn test_state() -> OpenAiRelayHttpState {
        OpenAiRelayHttpState::new(Arc::new(TestRelayService), "test-version")
    }

    #[derive(Clone)]
    struct MockUpstream {
        response: MockUpstreamResponse,
        requests: mpsc::Sender<CapturedUpstreamRequest>,
    }

    #[derive(Clone)]
    struct MockUpstreamResponse {
        status: StatusCode,
        headers: HeaderMap,
        body: Vec<u8>,
    }

    struct CapturedUpstreamRequest {
        authorization: Option<String>,
        path: String,
        body: Bytes,
    }

    async fn mock_upstream(
        State(state): State<MockUpstream>,
        headers: HeaderMap,
        uri: Uri,
        body: Bytes,
    ) -> Response {
        let _ = state
            .requests
            .send(CapturedUpstreamRequest {
                authorization: headers
                    .get(header::AUTHORIZATION)
                    .and_then(|value| value.to_str().ok())
                    .map(str::to_owned),
                path: uri.path().to_owned(),
                body,
            })
            .await;
        (
            state.response.status,
            state.response.headers,
            state.response.body,
        )
            .into_response()
    }

    async fn mock_server(
        response: MockUpstreamResponse,
    ) -> TestResult<(
        String,
        mpsc::Receiver<CapturedUpstreamRequest>,
        JoinHandle<()>,
    )> {
        let listener = with_context(
            TcpListener::bind("127.0.0.1:0").await,
            "bind mock upstream listener",
        )?;
        let address = with_context(listener.local_addr(), "read mock upstream address")?;
        let (sender, receiver) = mpsc::channel(1);
        let app = Router::new()
            .fallback(post(mock_upstream))
            .with_state(MockUpstream {
                response,
                requests: sender,
            });
        let task = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });
        Ok((format!("http://{address}/channel/"), receiver, task))
    }

    fn relay_request(
        endpoint: OpenAiRelayEndpoint,
        raw_body: &[u8],
    ) -> TestResult<OpenAiRelayRequest> {
        let mut headers = HeaderMap::new();
        headers.insert(
            header::AUTHORIZATION,
            HeaderValue::from_static("Bearer tenant-token"),
        );
        headers.insert(
            header::CONTENT_TYPE,
            HeaderValue::from_static("application/json"),
        );
        Ok(OpenAiRelayRequest {
            endpoint,
            protocol: Protocol::OpenAi,
            request_id: "request-1".to_owned(),
            headers,
            request: with_context(
                completion_request_to_canonical(br#"{"model":"mock-model","prompt":"hello"}"#),
                "build canonical relay test request",
            )?,
            raw_body: raw_body.to_vec(),
        })
    }

    #[test]
    fn upstream_url_preserves_channel_path_prefix() -> TestResult {
        let url = with_context(
            upstream_url(
                "https://upstream.example/channel-prefix",
                OpenAiRelayEndpoint::ResponsesCompact,
            ),
            "build selected upstream URL",
        )?;
        assert_eq!(
            url.as_str(),
            "https://upstream.example/channel-prefix/v1/responses/compact"
        );
        Ok(())
    }

    #[test]
    fn native_openai_parse_extracts_metadata_without_filtering_unknown_wire_fields() -> TestResult {
        let body = br#"{"model":"gpt-future","stream":true,"tools":[{"type":"future_tool","opaque":{"x":1}}],"future_request_field":[1,2,3]}"#;
        let canonical = with_context(
            parse_request(OpenAiRelayEndpoint::ChatCompletions, body),
            "parse native Chat Completions metadata",
        )?;
        assert_eq!(canonical.model, "gpt-future");
        assert!(canonical.stream);
        assert!(canonical.messages.is_empty());

        let responses = with_context(
            parse_request(OpenAiRelayEndpoint::Responses, body),
            "parse native Responses metadata",
        )?;
        assert_eq!(responses.model, "gpt-future");
        assert!(responses.stream);
        Ok(())
    }

    #[test]
    fn native_openai_parse_rejects_malformed_stream_metadata_without_reading_other_fields() {
        let body = br#"{"model":"gpt-future","stream":"yes","future":true}"#;
        assert!(matches!(
            parse_request(OpenAiRelayEndpoint::Responses, body),
            Err(RelayConvertError::Unsupported(message))
                if message == "stream must be a boolean"
        ));
    }

    #[test]
    fn default_http_state_admits_validated_native_raw_route() -> TestResult {
        let state = test_state();
        let admission = with_context(
            native_route_admission(&state, "request-1", Protocol::OpenAi, "gpt-future", false),
            "admit the validated native route in the default state",
        )?;

        assert!(
            admission
                .details
                .capability
                .as_ref()
                .is_some_and(|capability| capability.raw_passthrough)
        );
        Ok(())
    }

    #[test]
    fn default_http_state_admits_validated_native_stream_route() -> TestResult {
        let state = test_state();
        let admission = with_context(
            native_route_admission(&state, "request-1", Protocol::OpenAi, "gpt-future", true),
            "admit the validated native stream route in the default state",
        )?;

        assert!(
            admission
                .details
                .capability
                .as_ref()
                .is_some_and(|capability| capability.stream_supported)
        );
        Ok(())
    }

    #[test]
    fn runtime_builder_installs_the_shared_validated_registry() -> TestResult {
        let registry = Arc::new(with_context(
            validated_current_registry(),
            "validate the built-in runtime registry",
        )?);
        let state =
            test_state().with_protocol_runtime(ProtocolRolloutControl::default(), registry.clone());
        let installed = required(
            state.validated_registry.as_ref(),
            "runtime builder did not install the validated registry",
        )?;

        assert!(Arc::ptr_eq(installed, &registry));
        Ok(())
    }

    #[test]
    fn missing_registry_closes_native_route_before_service_relay() {
        let mut state = test_state();
        state.validated_registry = None;

        assert!(matches!(
            native_route_admission(&state, "request-1", Protocol::OpenAi, "gpt-future", false,),
            Err(FailureReason::RegistryDrift)
        ));
    }

    #[tokio::test]
    async fn missing_registry_returns_safe_server_error() -> TestResult {
        let mut state = test_state();
        state.validated_registry = None;
        let request = with_context(
            Request::builder()
                .method("POST")
                .uri("/v1/chat/completions")
                .header(header::AUTHORIZATION, "Bearer tenant-token")
                .body(Body::from(r#"{"model":"gpt-future"}"#)),
            "build missing-registry relay request",
        )?;

        let response = with_context(
            openai_relay_router(state).oneshot(request).await,
            "dispatch missing-registry relay request",
        )?;

        assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
        Ok(())
    }

    #[test]
    fn builder_observes_shared_rollout_replacement_and_rollback() -> TestResult {
        let control = ProtocolRolloutControl::default();
        let registry = Arc::new(with_context(
            validated_current_registry(),
            "validate the built-in runtime registry",
        )?);
        let state = test_state().with_protocol_runtime(control.clone(), registry);
        let initial = with_context(
            native_route_admission(&state, "request-1", Protocol::OpenAi, "gpt-future", false),
            "admit the default native route",
        )?;
        assert!(!initial.details.flag_decision.enabled);

        let mut enabled = crate::protocol_rollout::ProtocolRolloutConfig::default();
        enabled.conversion_engine_v2 = with_context(
            crate::protocol_rollout::FlagConfig::enabled(crate::protocol_rollout::MAX_BASIS_POINTS),
            "construct a bounded full rollout",
        )?;
        with_context(
            control.replace(enabled),
            "install the replacement rollout configuration",
        )?;
        let replaced = with_context(
            native_route_admission(&state, "request-1", Protocol::OpenAi, "gpt-future", false),
            "admit the native route after rollout replacement",
        )?;
        assert!(replaced.details.flag_decision.enabled);

        with_context(
            control.replace_rollback(),
            "install the rollback configuration",
        )?;
        let rolled_back = with_context(
            native_route_admission(&state, "request-1", Protocol::OpenAi, "gpt-future", false),
            "admit the native raw route during rollback",
        )?;
        assert_eq!(
            rolled_back.details.flag_decision.source,
            crate::protocol_rollout::DecisionSource::ConfigRollback
        );
        assert!(!rolled_back.details.flag_decision.enabled);
        Ok(())
    }

    #[test]
    fn upstream_header_copy_strips_client_credentials_and_injects_channel_credential() -> TestResult
    {
        let mut inbound = HeaderMap::new();
        inbound.insert(header::ACCEPT, HeaderValue::from_static("application/json"));
        inbound.insert("x-trace-id", HeaderValue::from_static("trace-123"));
        for (name, value) in [
            (header::AUTHORIZATION, "Bearer tenant-token"),
            (HeaderName::from_static("x-api-key"), "tenant-x-api-key"),
            (
                HeaderName::from_static("x-goog-api-key"),
                "tenant-x-goog-api-key",
            ),
            (HeaderName::from_static("api-key"), "tenant-api-key"),
            (
                HeaderName::from_static("sec-websocket-protocol"),
                "responses, openai-insecure-api-key.tenant-token",
            ),
            (
                HeaderName::from_static("sec-websocket-key"),
                "tenant-websocket-key",
            ),
            (header::COOKIE, "session=tenant-session"),
        ] {
            inbound.insert(name, HeaderValue::from_static(value));
        }

        let request = with_context(
            copy_upstream_headers(
                reqwest::Client::new().post("https://upstream.example/v1/responses"),
                &inbound,
                "channel-secret",
            )
            .build(),
            "build the upstream header passthrough request",
        )?;

        assert_eq!(
            request.headers()[header::AUTHORIZATION],
            "Bearer channel-secret"
        );
        assert_eq!(request.headers()[header::ACCEPT], "application/json");
        assert_eq!(request.headers()["x-trace-id"], "trace-123");
        for name in [
            "x-api-key",
            "x-goog-api-key",
            "api-key",
            "sec-websocket-protocol",
            "sec-websocket-key",
            "cookie",
        ] {
            assert!(
                !request.headers().contains_key(name),
                "sensitive inbound header leaked upstream: {name}"
            );
        }
        Ok(())
    }

    #[tokio::test]
    async fn upstream_adapter_replays_raw_json_and_replaces_tenant_credentials() -> TestResult {
        let mut headers = HeaderMap::new();
        headers.insert(
            header::CONTENT_TYPE,
            HeaderValue::from_static("application/json"),
        );
        headers.insert("x-upstream-trace", HeaderValue::from_static("mock-json"));
        let (base_url, mut received, task) = mock_server(MockUpstreamResponse {
            status: StatusCode::OK,
            headers,
            body: br#"{"opaque_provider_field":true}"#.to_vec(),
        })
        .await?;
        let request = relay_request(
            OpenAiRelayEndpoint::ChatCompletions,
            br#"{"model":"mock-model","messages":[{"role":"user","content":"hello"}],"provider_option":true}"#,
        )?;
        let client = OpenAiUpstreamClient::new(reqwest::Client::new(), Duration::from_secs(1));
        let result = with_context(
            client
                .forward(
                    &OpenAiUpstreamTarget {
                        base_url,
                        api_key: "channel-secret".to_owned(),
                    },
                    &request,
                )
                .await,
            "forward raw JSON to the mock upstream",
        )?;

        let captured = required(
            received.recv().await,
            "mock upstream did not capture the request",
        )?;
        assert_eq!(captured.path, "/channel/v1/chat/completions");
        assert_eq!(
            captured.authorization.as_deref(),
            Some("Bearer channel-secret")
        );
        assert_eq!(captured.body.as_ref(), request.raw_body.as_slice());
        assert_eq!(result.headers["x-upstream-trace"], "mock-json");
        let (body, content_type) = match result.body {
            OpenAiRelayBody::Upstream { body, content_type } => (body, content_type),
            unexpected => {
                return Err(test_error(format!(
                    "expected passthrough upstream body, got {unexpected:?}"
                )));
            }
        };
        let content_type = required(content_type, "upstream response omitted content-type")?;
        assert_eq!(content_type, "application/json");
        let body = with_context(
            to_bytes(body, usize::MAX).await,
            "read the upstream passthrough body",
        )?;
        assert_eq!(body.as_ref(), br#"{"opaque_provider_field":true}"#);
        task.abort();
        Ok(())
    }

    #[tokio::test]
    async fn upstream_adapter_preserves_sse_wire_stream_without_reencoding() -> TestResult {
        let mut headers = HeaderMap::new();
        headers.insert(
            header::CONTENT_TYPE,
            HeaderValue::from_static("text/event-stream; charset=utf-8"),
        );
        let expected = b"data: {\"opaque\":true}\n\ndata: [DONE]\n\n";
        let (base_url, _received, task) = mock_server(MockUpstreamResponse {
            status: StatusCode::OK,
            headers,
            body: expected.to_vec(),
        })
        .await?;
        let client = OpenAiUpstreamClient::new(reqwest::Client::new(), Duration::from_secs(1));
        let result = with_context(
            client
                .forward(
                    &OpenAiUpstreamTarget {
                        base_url,
                        api_key: String::new(),
                    },
                    &relay_request(OpenAiRelayEndpoint::Responses, br#"{"model":"mock-model"}"#)?,
                )
                .await,
            "forward the SSE request to the mock upstream",
        )?;

        let (body, content_type) = match result.body {
            OpenAiRelayBody::Upstream { body, content_type } => (body, content_type),
            unexpected => {
                return Err(test_error(format!(
                    "expected passthrough SSE body, got {unexpected:?}"
                )));
            }
        };
        let content_type = required(content_type, "SSE response omitted content-type")?;
        assert_eq!(content_type, "text/event-stream; charset=utf-8");
        let body = with_context(to_bytes(body, usize::MAX).await, "read the SSE body")?;
        assert_eq!(body.as_ref(), expected);
        task.abort();
        Ok(())
    }

    #[tokio::test]
    async fn upstream_adapter_maps_openai_error_and_retains_retry_header() -> TestResult {
        let mut headers = HeaderMap::new();
        headers.insert(header::RETRY_AFTER, HeaderValue::from_static("2"));
        headers.insert(
            header::CONTENT_TYPE,
            HeaderValue::from_static("application/json"),
        );
        let (base_url, _received, task) = mock_server(MockUpstreamResponse {
            status: StatusCode::TOO_MANY_REQUESTS,
            headers,
            body: br#"{"error":{"message":"rate limited","code":"rate_limit_exceeded"}}"#.to_vec(),
        })
        .await?;
        let client = OpenAiUpstreamClient::new(reqwest::Client::new(), Duration::from_secs(1));
        let result = client
            .forward(
                &OpenAiUpstreamTarget {
                    base_url,
                    api_key: String::new(),
                },
                &relay_request(OpenAiRelayEndpoint::Responses, br#"{"model":"mock-model"}"#)?,
            )
            .await;
        let error = match result {
            Ok(response) => {
                return Err(test_error(format!(
                    "expected an upstream error, got {response:?}"
                )));
            }
            Err(error) => error,
        };

        assert_eq!(error.status, StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(error.code, "rate_limit_exceeded");
        assert_eq!(error.message, "rate limited");
        assert_eq!(error.headers[header::RETRY_AFTER], "2");
        task.abort();
        Ok(())
    }

    #[test]
    fn legacy_token_parser_removes_transport_and_channel_suffixes() {
        let mut headers = HeaderMap::new();
        headers.insert(
            header::AUTHORIZATION,
            HeaderValue::from_static("Bearer sk-tenant-key-channel-7"),
        );
        assert_eq!(token_key(&headers).as_deref(), Some("tenant"));
    }

    #[test]
    fn ip_allow_list_fails_closed_when_listener_has_no_canonical_ip() -> TestResult {
        assert!(ip_is_allowed(None, ""));
        assert!(!ip_is_allowed(None, "127.0.0.1"));
        let loopback = with_context("127.0.0.1".parse(), "parse the canonical loopback IP")?;
        assert!(ip_is_allowed(Some(loopback), "127.0.0.0/8"));
        Ok(())
    }
}
