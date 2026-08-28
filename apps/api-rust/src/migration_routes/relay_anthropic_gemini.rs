//! Legacy-compatible Anthropic Messages and Gemini GenerateContent relay slice.
//!
//! A production executor is supplied at composition time through
//! [`RelayBackend`].  The HTTP boundary deliberately has no permissive default:
//! callers must explicitly install an implementation that performs the legacy
//! PostgreSQL/Valkey authorization, channel selection, retry, accounting, and
//! provider transport work.

use std::{sync::Arc, time::Instant};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::{Body, to_bytes},
    extract::{Path, Request, State},
    http::{HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{MethodRouter, post},
};
use futures_util::StreamExt;
use lmm_contracts::relay::{Direction, Fidelity, Protocol, ValidatedRegistry};
use serde::Serialize;
use serde_json::{Value, json};

use super::missing_relay_models_billing::{ModelLookupState, model_lookup_method_router};
use super::sse::SseError;
use crate::{
    RequestContext,
    conversion_observability::{
        ClientAbortGuard, ConversionObserver, ConversionResult, ConverterVersion, FailureReason,
        FeatureClass, MetricLabels, StreamTiming, global_observer,
    },
    protocol_rollout::{ProtocolRolloutControl, RolloutContext},
    protocol_route_gate::{self, RouteGateDecision},
    protocol_runtime_registry::validated_current_registry,
    route_ownership::{OwnershipEvidence, RouteOwnershipScope},
};

const REQUEST_ID: &str = "x-request-id";
const LEGACY_REQUEST_ID: &str = "x-oneapi-request-id";
const CHANNEL_ID: &str = "x-oneapi-channel-id";
const MAX_RELAY_BODY_BYTES: usize = 64 * 1024 * 1024;

/// The protocol-specific shape the selected channel must support.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RelayProtocol {
    /// OpenAI-compatible request selection used by legacy model deletion.
    OpenAi,
    /// Anthropic's Messages API.
    Anthropic,
    /// Gemini's GenerateContent API.
    Gemini,
}

/// A token identity established before selecting a relay channel.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RelayIdentity {
    /// Stable token identifier, suitable for accounting without exposing its secret.
    pub token_id: String,
}

/// A selected upstream channel.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RelayChannel {
    /// Legacy channel primary key.
    pub id: i64,
    /// Upstream-specific model name after any configured mapping.
    pub upstream_model: String,
}

/// One request supplied to an upstream relay adapter.
#[derive(Clone, Debug)]
pub struct UpstreamRequest {
    /// Original protocol used by the caller.
    pub protocol: RelayProtocol,
    /// Client-facing model name.
    pub model: String,
    /// Request path selected by the client, without query credentials.
    ///
    /// Gemini uses a wildcard route below `/v1/models/` and `/v1beta/models/`.
    /// Retaining the exact path lets the production adapter choose the matching
    /// provider operation (for example `generateContent`, `countTokens`, or an
    /// embedding operation) instead of silently narrowing the public API to a
    /// hard-coded action list.
    pub request_path: String,
    /// Raw JSON body retained to preserve unimplemented provider fields.
    pub body: Value,
    /// Original request bytes for exact retry replay by the production adapter.
    pub raw_body: Vec<u8>,
    /// Whether the caller expects an event stream.
    pub streaming: bool,
    /// Correlation identifier supplied or generated at the request boundary.
    pub request_id: String,
}

/// Reply returned by a provider adapter after provider-to-client conversion.
#[derive(Debug)]
pub enum UpstreamReply {
    /// A complete provider-compatible JSON value.
    Json(Value),
    /// Ordered provider-compatible SSE events.
    Sse(Vec<RelaySseEvent>),
    /// A single-consumption native Anthropic/Gemini SSE response.
    ///
    /// This variant is intentionally distinct from [`Self::Sse`]: it is only
    /// produced for a same-protocol relay and keeps the provider's bytes in a
    /// backpressured [`Body`] rather than decoding and re-encoding frames.
    NativeSse(Box<NativeSseReply>),
}

/// A successful same-protocol SSE response whose body is consumed exactly once.
///
/// The body owns the upstream `bytes_stream`. Dropping the response before it
/// is fully read therefore drops that stream as well, allowing the HTTP client
/// cancellation to reach the provider without an intermediate queue.
pub struct NativeSseReply {
    status: StatusCode,
    body: Body,
    content_type: Option<HeaderValue>,
}

impl NativeSseReply {
    /// Creates a native response for a same-protocol relay.
    ///
    /// `content_type` is copied from the upstream response when present. The
    /// HTTP boundary supplies the event-stream fallback when it is absent and
    /// always applies its safe `Cache-Control: no-cache` policy.
    #[must_use]
    pub fn new(status: StatusCode, body: Body, content_type: Option<HeaderValue>) -> Self {
        Self {
            status,
            body,
            content_type,
        }
    }
}

impl std::fmt::Debug for NativeSseReply {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("NativeSseReply")
            .field("status", &self.status)
            .field("content_type", &self.content_type)
            .field("body", &"<stream>")
            .finish()
    }
}

/// A provider SSE event after protocol translation and before HTTP framing.
#[derive(Clone, Debug)]
pub struct RelaySseEvent {
    /// Optional SSE event name. Anthropic uses named events; Gemini normally
    /// uses un-named `data` frames.
    pub kind: Option<String>,
    /// JSON payload without `data:` framing.
    pub payload: Value,
}

/// Non-sensitive outcome recorded after channel selection and invocation.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RelayOutcome {
    /// The upstream returned a response.
    Succeeded,
    /// Authentication rejected the request.
    Unauthorized,
    /// No eligible channel was available.
    NoChannel,
    /// An upstream attempt failed.
    UpstreamFailure,
}

/// Boundary between HTTP compatibility and authorization/channel/upstream systems.
#[async_trait]
pub trait RelayBackend: Send + Sync {
    /// Validates a presented API token without retaining its secret.
    async fn authenticate(&self, token: &str) -> Result<RelayIdentity, RelayFailure>;
    /// Selects an eligible channel for a model and protocol.
    async fn select_channel(
        &self,
        identity: &RelayIdentity,
        protocol: RelayProtocol,
        model: &str,
    ) -> Result<RelayChannel, RelayFailure>;
    /// Invokes a selected channel and converts its response to the caller protocol.
    ///
    /// A native Anthropic/Gemini SSE response is returned as
    /// [`UpstreamReply::NativeSse`] and is consumed once by the HTTP boundary;
    /// [`UpstreamReply::Sse`] remains the buffered representation for typed
    /// conversion paths.
    async fn invoke(
        &self,
        channel: &RelayChannel,
        request: UpstreamRequest,
    ) -> Result<UpstreamReply, RelayFailure>;
    /// Records a non-secret audit/usage outcome.
    async fn record_outcome(
        &self,
        identity: Option<&RelayIdentity>,
        channel: Option<&RelayChannel>,
        outcome: RelayOutcome,
    );
}

/// Stable failures that can be safely exposed by the compatibility layer.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum RelayFailure {
    /// A token is absent, malformed, or invalid.
    Unauthorized,
    /// Current Go `TokenAuth` conceals an absent or invalid relay credential
    /// as the generic public 404 document instead of exposing an auth error.
    ConcealedNotFound,
    /// No channel can serve the requested model.
    NoChannel,
    /// The validated native route capability is unavailable or closed.
    RouteUnavailable,
    /// The upstream did not complete the request.
    Upstream,
    /// The provider returned a protocol response after authentication.
    Provider {
        /// HTTP status returned by the provider adapter.
        status: StatusCode,
        /// Client-protocol JSON response body after adapter conversion.
        body: Value,
    },
    /// The provider returned an SSE frame that cannot be represented without
    /// silently losing data or metadata at this relay boundary.
    Sse(SseError),
}

/// State used by this independently mergeable relay router.
#[derive(Clone)]
pub struct RelayHttpState {
    backend: Arc<dyn RelayBackend>,
    protocol_rollout: ProtocolRolloutControl,
    validated_registry: Option<Arc<ValidatedRegistry>>,
}

impl RelayHttpState {
    /// Creates router state from the application's authorization and relay adapter.
    #[must_use]
    pub fn new(backend: Arc<dyn RelayBackend>) -> Self {
        Self {
            backend,
            protocol_rollout: ProtocolRolloutControl::default(),
            validated_registry: validated_current_registry().ok().map(Arc::new),
        }
    }

    /// Installs the shared rollout control and validated runtime registry.
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

/// Builds the Anthropic and Gemini relay route forms owned by this slice.
pub fn router(state: RelayHttpState) -> Router {
    router_with_optional_model_lookup(state, None)
}

/// Builds the relay routes with the GET half of the shared OpenAI model route.
///
/// `/v1/models/{model}` has three independent legacy method behaviours: a
/// static-catalogue GET, a Gemini single-segment POST, and an authenticated
/// frozen DELETE.  Axum allows those methods in one `MethodRouter`, but rejects
/// overlapping exact and wildcard routes, so this is the sole composition
/// point for the shared path.
pub fn router_with_model_lookup(state: RelayHttpState, lookup: ModelLookupState) -> Router {
    router_with_optional_model_lookup(state, Some(lookup))
}

fn router_with_optional_model_lookup(
    state: RelayHttpState,
    lookup: Option<ModelLookupState>,
) -> Router {
    let model_methods = lookup
        .map_or_else(MethodRouter::new, model_lookup_method_router)
        // The GET handler closes over its independent lookup state. Convert its
        // otherwise stateless method router to this relay router's state type
        // before adding POST and DELETE handlers below.
        .with_state::<RelayHttpState>(());
    Router::new()
        .route("/v1/messages", post(anthropic_messages))
        .route("/v1/engines/{model}/embeddings", post(gemini_embedding))
        .route(
            "/v1/models/{model}",
            model_methods
                .post(gemini_content_single)
                .delete(delete_openai_model_not_implemented),
        )
        .route(
            "/v1/models/{model}/{*tail}",
            post(gemini_content_tail)
                .get(reject_openai_model_tail)
                .delete(reject_openai_model_tail),
        )
        .route("/v1beta/models/{model}", post(gemini_content_single))
        .route("/v1beta/models/{model}/{*tail}", post(gemini_content_tail))
        .with_state(state)
}

async fn anthropic_messages(State(state): State<RelayHttpState>, request: Request) -> Response {
    relay(&state, request, RelayProtocol::Anthropic, None).await
}

async fn gemini_content_single(
    State(state): State<RelayHttpState>,
    Path(path): Path<String>,
    http_request: Request,
) -> Response {
    gemini_content_for_path(state, path, http_request).await
}

/// Axum separates the first segment from a wildcard tail. Reassemble the
/// legacy capture before extracting the model, preserving the original request
/// URI for the downstream adapter and its exact provider operation selection.
async fn gemini_content_tail(
    State(state): State<RelayHttpState>,
    Path((model, tail)): Path<(String, String)>,
    http_request: Request,
) -> Response {
    gemini_content_for_path(state, format!("{model}/{tail}"), http_request).await
}

async fn gemini_content_for_path(
    state: RelayHttpState,
    path: String,
    http_request: Request,
) -> Response {
    let streaming = gemini_is_streaming(http_request.uri());
    let Some(model) = gemini_model_from_path(&path) else {
        global_observer().record_failure_with_reason(
            relay_observation_labels(RelayProtocol::Gemini, streaming, ConversionResult::Failure),
            FailureReason::InvalidInput,
        );
        return invalid_request(
            RelayProtocol::Gemini,
            "model is required",
            &request_id(&http_request),
        );
    };
    relay(
        &state,
        http_request,
        RelayProtocol::Gemini,
        Some((model, streaming)),
    )
    .await
}

async fn gemini_embedding(
    State(state): State<RelayHttpState>,
    Path(model): Path<String>,
    request: Request,
) -> Response {
    relay(
        &state,
        request,
        RelayProtocol::Gemini,
        Some((&model, false)),
    )
    .await
}

/// Legacy `/v1/models/:model` is an authenticated compatibility route. It
/// shares the exact single-segment registration with the model lookup GET and
/// Gemini POST methods. The old Gin parameter matched one path segment only,
/// so wildcard tails are rejected before invoking relay policy.
///
/// Unlike relay POST routes, the compatibility response stops after the
/// token-auth middleware and never distributes/selects a channel.
async fn delete_openai_model_not_implemented(
    State(state): State<RelayHttpState>,
    Path(model): Path<String>,
    request: Request,
) -> Response {
    let request_id = request_id(&request);
    let path = request.uri().path().to_owned();
    if model.is_empty() || model.contains('/') {
        return openai_not_found(&request_id, &path);
    }
    let Some(token) = token_from_request(&request, RelayProtocol::OpenAi) else {
        state
            .backend
            .record_outcome(None, None, RelayOutcome::Unauthorized)
            .await;
        return openai_failure(&RelayFailure::ConcealedNotFound, &request_id);
    };
    if let Err(error) = state.backend.authenticate(&token).await {
        state
            .backend
            .record_outcome(None, None, outcome_for(&error))
            .await;
        return openai_failure(&error, &request_id);
    }
    let mut response = (
        StatusCode::NOT_IMPLEMENTED,
        Json(LegacyUnavailableEnvelope {
            error: LegacyUnavailableError {
                message: "API not implemented",
                kind: "new_api_error",
                param: "",
                code: "api_not_implemented",
            },
        }),
    )
        .into_response();
    add_compat_headers(&mut response, 0, &request_id);
    response
}

/// Preserves the frozen Go `OpenAIError` JSON field order for model deletion.
#[derive(Serialize)]
struct LegacyUnavailableEnvelope {
    error: LegacyUnavailableError,
}

#[derive(Serialize)]
struct LegacyUnavailableError {
    message: &'static str,
    #[serde(rename = "type")]
    kind: &'static str,
    param: &'static str,
    code: &'static str,
}

/// Gin's `:model` parameter matched one segment.  Keep malformed multi-segment
/// GET and DELETE requests outside the lookup/deletion policies instead of
/// allowing Axum's wildcard tail to turn them into method-not-allowed replies.
async fn reject_openai_model_tail(request: Request) -> Response {
    let request_id = request_id(&request);
    let path = request.uri().path().to_owned();
    openai_not_found(&request_id, &path)
}

async fn relay(
    state: &RelayHttpState,
    request: Request,
    protocol: RelayProtocol,
    gemini_route: Option<(&str, bool)>,
) -> Response {
    let observer = global_observer();
    let request_id = request_id(&request);
    let request_path = request.uri().path().to_owned();
    let stream_hint = gemini_route.is_some_and(|(_, streaming)| streaming);
    let Some(token) = token_from_request(&request, protocol) else {
        observer.record_failure_with_reason(
            relay_observation_labels(protocol, stream_hint, ConversionResult::Failure),
            FailureReason::Unknown,
        );
        state
            .backend
            .record_outcome(None, None, RelayOutcome::Unauthorized)
            .await;
        return failure(protocol, &RelayFailure::ConcealedNotFound, &request_id);
    };
    let identity = match state.backend.authenticate(&token).await {
        Ok(identity) => identity,
        Err(error) => {
            observer.record_failure_with_reason(
                relay_observation_labels(protocol, stream_hint, ConversionResult::Failure),
                relay_failure_reason(&error),
            );
            state
                .backend
                .record_outcome(None, None, outcome_for(&error))
                .await;
            return failure(protocol, &error, &request_id);
        }
    };
    let raw_body = match to_bytes(request.into_body(), MAX_RELAY_BODY_BYTES).await {
        Ok(body) => body.to_vec(),
        Err(_) => {
            let labels = relay_observation_labels(protocol, stream_hint, ConversionResult::Failure);
            observer.record_failure_with_reason(labels, FailureReason::InvalidInput);
            return invalid_request(protocol, "request body too large", &request_id);
        }
    };
    let parse_started = Instant::now();
    let body: Value = match serde_json::from_slice(&raw_body) {
        Ok(body) => body,
        Err(_) => {
            let labels = relay_observation_labels(protocol, stream_hint, ConversionResult::Failure);
            observer.record_input_bytes(labels, raw_body.len());
            observer.record_conversion_duration(labels, parse_started.elapsed());
            observer.record_failure_with_reason(labels, FailureReason::InvalidInput);
            return invalid_request(protocol, "invalid JSON request body", &request_id);
        }
    };
    let (model, streaming) = match gemini_route {
        Some((model, streaming)) => (model.to_owned(), streaming),
        None => (
            body.get("model")
                .and_then(Value::as_str)
                .unwrap_or_default()
                .to_owned(),
            body.get("stream").and_then(Value::as_bool).unwrap_or(false),
        ),
    };
    let request_labels = relay_observation_labels(protocol, streaming, ConversionResult::Success);
    observer.record_input_bytes(request_labels, raw_body.len());
    observer.record_conversion_duration(request_labels, parse_started.elapsed());
    if model.trim().is_empty() {
        observer.record_failure_with_reason(request_labels, FailureReason::InvalidInput);
        return invalid_request(protocol, "model is required", &request_id);
    }
    if let Err(reason) = native_route_is_open(state, protocol, &request_id, &model, streaming) {
        observer.record_failure_with_reason(
            relay_observation_labels(protocol, streaming, ConversionResult::Failure),
            reason,
        );
        return failure(protocol, &RelayFailure::RouteUnavailable, &request_id);
    }
    let channel = match state
        .backend
        .select_channel(&identity, protocol, &model)
        .await
    {
        Ok(channel) => channel,
        Err(error) => {
            observer.record_failure_with_reason(
                relay_observation_labels(protocol, streaming, ConversionResult::Failure),
                relay_failure_reason(&error),
            );
            state
                .backend
                .record_outcome(Some(&identity), None, outcome_for(&error))
                .await;
            return failure(protocol, &error, &request_id);
        }
    };
    let request = UpstreamRequest {
        protocol,
        model: model.to_owned(),
        request_path,
        body,
        raw_body,
        streaming,
        request_id: request_id.clone(),
    };
    match state.backend.invoke(&channel, request).await {
        Ok(reply) => {
            state
                .backend
                .record_outcome(Some(&identity), Some(&channel), RelayOutcome::Succeeded)
                .await;
            success(reply, channel.id, &request_id, protocol, observer)
        }
        Err(error) => {
            observer.record_failure_with_reason(
                relay_observation_labels(protocol, streaming, ConversionResult::Failure),
                relay_failure_reason(&error),
            );
            state
                .backend
                .record_outcome(Some(&identity), Some(&channel), outcome_for(&error))
                .await;
            failure(protocol, &error, &request_id)
        }
    }
}

/// Checks both halves of a native same-protocol request before channel
/// selection or upstream invocation. The rollout snapshot, context, and
/// closed ownership evidence are shared by the request and response/stream
/// capability checks.
fn native_route_is_open(
    state: &RelayHttpState,
    protocol: RelayProtocol,
    request_id: &str,
    model: &str,
    streaming: bool,
) -> Result<(), FailureReason> {
    let rollout_snapshot = state.protocol_rollout.snapshot();
    let Some(registry) = state.validated_registry.as_deref() else {
        return Err(FailureReason::RegistryDrift);
    };
    let wire_protocol = relay_observation_protocol(protocol);
    let context = RolloutContext::new(request_id, wire_protocol, wire_protocol, model, streaming);
    let scope = RouteOwnershipScope {
        source: wire_protocol,
        target: wire_protocol,
        stream: streaming,
    };
    let evidence = OwnershipEvidence::closed(scope);
    let config = rollout_snapshot.config();
    let request_decision = protocol_route_gate::decide_route(
        config,
        &context,
        registry,
        Direction::Request,
        &evidence,
    );
    let output_direction = if streaming {
        Direction::Stream
    } else {
        Direction::Response
    };
    let output_decision =
        protocol_route_gate::decide_route(config, &context, registry, output_direction, &evidence);
    if is_exact_raw_native(&request_decision) && is_exact_raw_native(&output_decision) {
        Ok(())
    } else {
        Err(FailureReason::Unsupported)
    }
}

fn is_exact_raw_native(decision: &RouteGateDecision) -> bool {
    let RouteGateDecision::NativeRaw { details } = decision else {
        return false;
    };
    details.capability.as_ref().is_some_and(|capability| {
        capability.quality == Fidelity::Exact && capability.raw_passthrough
    })
}

/// Mirrors legacy distributor extraction for wildcard Gemini model paths.
/// The Go route accepts every path after `/models/`; its adapter, not the HTTP
/// boundary, determines whether an action is supported.  A slash remains part
/// of the model portion when no colon is present, matching that behavior.
fn gemini_model_from_path(path: &str) -> Option<&str> {
    // The standalone helper also accepts a full legacy request path for
    // parity tests, while Axum supplies the wildcard capture itself here.
    let request = path
        .split_once("/models/")
        .map_or(path, |(_, request)| request)
        .trim_start_matches('/');
    let model = request.split(':').next()?;
    (!model.is_empty()).then_some(model)
}

/// Mirrors `GeminiChatRequest.IsStream`: either native stream action or
/// `alt=sse` requests an SSE response.  The latter matters for callers using
/// `generateContent` with a streaming query rather than the stream action.
fn gemini_is_streaming(uri: &axum::http::Uri) -> bool {
    query_value(uri.query(), "alt").as_deref() == Some("sse")
        || uri.path().contains("streamGenerateContent")
}

fn token_from_request(request: &Request, protocol: RelayProtocol) -> Option<String> {
    let headers = request.headers();
    let bearer = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .and_then(bearer_token);
    let google = headers
        .get("x-goog-api-key")
        .and_then(|value| value.to_str().ok())
        .filter(|value| !value.is_empty())
        .map(str::to_owned)
        .or_else(|| query_value(request.uri().query(), "key").filter(|value| !value.is_empty()));
    match protocol {
        RelayProtocol::Anthropic => headers
            .get("x-api-key")
            .and_then(|value| value.to_str().ok())
            .filter(|value| !value.is_empty())
            .or(bearer)
            .map(str::to_owned),
        RelayProtocol::Gemini => google.or_else(|| bearer.map(str::to_owned)),
        RelayProtocol::OpenAi => google
            .or_else(|| {
                headers
                    .get("x-api-key")
                    .and_then(|value| value.to_str().ok())
                    .filter(|value| !value.is_empty())
                    .map(str::to_owned)
            })
            .or_else(|| bearer.map(str::to_owned)),
    }
}

fn bearer_token(value: &str) -> Option<&str> {
    let mut words = value.split_ascii_whitespace();
    let scheme = words.next()?;
    let token = words.next()?;
    (scheme.eq_ignore_ascii_case("bearer") && !token.is_empty() && words.next().is_none())
        .then_some(token)
}

fn query_value(query: Option<&str>, name: &str) -> Option<String> {
    form_urlencoded::parse(query?.as_bytes())
        .find_map(|(key, value)| (key == name).then_some(value.into_owned()))
}

fn request_id(request: &Request) -> String {
    request
        .extensions()
        .get::<RequestContext>()
        .map_or_else(String::new, |context| context.request_id.clone())
}

fn outcome_for(error: &RelayFailure) -> RelayOutcome {
    match error {
        RelayFailure::Unauthorized | RelayFailure::ConcealedNotFound => RelayOutcome::Unauthorized,
        RelayFailure::NoChannel | RelayFailure::RouteUnavailable => RelayOutcome::NoChannel,
        RelayFailure::Upstream | RelayFailure::Provider { .. } | RelayFailure::Sse(_) => {
            RelayOutcome::UpstreamFailure
        }
    }
}

fn openai_failure(error: &RelayFailure, request_id: &str) -> Response {
    if matches!(error, RelayFailure::ConcealedNotFound) {
        return concealed_not_found();
    }
    let status = match error {
        RelayFailure::Unauthorized => StatusCode::UNAUTHORIZED,
        RelayFailure::ConcealedNotFound => StatusCode::NOT_FOUND,
        RelayFailure::NoChannel | RelayFailure::RouteUnavailable => StatusCode::SERVICE_UNAVAILABLE,
        RelayFailure::Upstream | RelayFailure::Provider { .. } | RelayFailure::Sse(_) => {
            StatusCode::BAD_GATEWAY
        }
    };
    let message = match error {
        RelayFailure::Unauthorized => "Invalid token",
        RelayFailure::ConcealedNotFound => "Not Found",
        RelayFailure::NoChannel
        | RelayFailure::RouteUnavailable
        | RelayFailure::Upstream
        | RelayFailure::Provider { .. }
        | RelayFailure::Sse(_) => "relay request could not be completed",
    };
    let body = if matches!(error, RelayFailure::Unauthorized) {
        json!({"error":{"code":"","message":format!("{message} (request id: {request_id})"),"type":"new_api_error"}})
    } else {
        json!({"error":{"message":format!("{message} (request id: {request_id})"),"type":"new_api_error","param":"","code":""}})
    };
    error_response(status, body, request_id)
}

fn openai_not_found(request_id: &str, path: &str) -> Response {
    let mut response = (
        StatusCode::NOT_FOUND,
        Json(json!({"error":{"message":format!("Invalid URL (DELETE {path})"),"type":"invalid_request_error","param":"","code":""}})),
    )
        .into_response();
    add_compat_headers(&mut response, 0, request_id);
    response
}

fn relay_observation_protocol(protocol: RelayProtocol) -> Protocol {
    match protocol {
        RelayProtocol::OpenAi => Protocol::OpenAi,
        RelayProtocol::Anthropic => Protocol::Claude,
        RelayProtocol::Gemini => Protocol::Gemini,
    }
}

fn relay_observation_labels(
    protocol: RelayProtocol,
    stream: bool,
    result: ConversionResult,
) -> MetricLabels {
    let protocol = relay_observation_protocol(protocol);
    MetricLabels::for_route(
        protocol,
        protocol,
        ConverterVersion::NativeRawV1,
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

fn relay_failure_reason(error: &RelayFailure) -> FailureReason {
    match error {
        RelayFailure::Sse(_) => FailureReason::Stream,
        RelayFailure::Provider { .. } | RelayFailure::Upstream => FailureReason::Upstream,
        // Authentication and channel eligibility have no dedicated closed
        // reason label; keep them in the bounded catch-all rather than
        // misclassifying them as malformed payloads.
        RelayFailure::Unauthorized | RelayFailure::ConcealedNotFound | RelayFailure::NoChannel => {
            FailureReason::Unknown
        }
        RelayFailure::RouteUnavailable => FailureReason::Unsupported,
    }
}

/// Observes native SSE chunks without decoding, buffering, or changing them.
/// The wrapper advances the upstream stream only when Axum asks for the next
/// body chunk, so provider backpressure and cancellation remain intact.
fn observe_native_sse_body(body: Body, observer: ConversionObserver, labels: MetricLabels) -> Body {
    let stream = body.into_data_stream();
    let guard = ClientAbortGuard::new(observer.clone(), labels);
    let queue_guard = observer.enter_queue(labels);
    let timing = StreamTiming::default();
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
                        observer.record_failure_with_reason(labels, FailureReason::Stream);
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

/// Counts bytes from an already serialized buffered response without causing
/// another JSON/SSE encode pass. Bytes are recorded only as the downstream
/// body is consumed.
fn observe_buffered_response(
    response: Response,
    observer: ConversionObserver,
    labels: MetricLabels,
) -> Response {
    let (parts, body) = response.into_parts();
    let observed = body.into_data_stream().map(move |item| {
        if let Ok(bytes) = &item {
            observer.record_output_bytes(labels, bytes.len());
        }
        item
    });
    Response::from_parts(parts, Body::from_stream(observed))
}

fn success(
    reply: UpstreamReply,
    channel_id: i64,
    request_id: &str,
    protocol: RelayProtocol,
    observer: &ConversionObserver,
) -> Response {
    let mut response = match reply {
        UpstreamReply::Json(body) => {
            let labels = relay_observation_labels(protocol, false, ConversionResult::Success);
            observe_buffered_response(Json(body).into_response(), (*observer).clone(), labels)
        }
        UpstreamReply::Sse(events) => {
            let labels = relay_observation_labels(protocol, true, ConversionResult::Success);
            let mut framed = String::new();
            for event in events {
                if let Some(kind) = event.kind {
                    framed.push_str("event: ");
                    framed.push_str(&kind);
                    framed.push('\n');
                }
                framed.push_str("data: ");
                framed.push_str(&event.payload.to_string());
                framed.push_str("\n\n");
            }
            framed.push_str("data: [DONE]\n\n");
            let mut response = (StatusCode::OK, framed).into_response();
            response.headers_mut().insert(
                header::CONTENT_TYPE,
                HeaderValue::from_static("text/event-stream; charset=utf-8"),
            );
            response
                .headers_mut()
                .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-cache"));
            observe_buffered_response(response, (*observer).clone(), labels)
        }
        UpstreamReply::NativeSse(native) => {
            let NativeSseReply {
                status,
                body,
                content_type,
            } = *native;
            let labels = relay_observation_labels(protocol, true, ConversionResult::Success);
            let body = observe_native_sse_body(body, (*observer).clone(), labels);
            let mut response = (status, body).into_response();
            response.headers_mut().insert(
                header::CONTENT_TYPE,
                content_type.unwrap_or_else(|| {
                    HeaderValue::from_static("text/event-stream; charset=utf-8")
                }),
            );
            response
                .headers_mut()
                .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-cache"));
            response
        }
    };
    add_compat_headers(&mut response, channel_id, request_id);
    response
}

fn invalid_request(protocol: RelayProtocol, message: &str, request_id: &str) -> Response {
    let body = match protocol {
        RelayProtocol::OpenAi => {
            json!({"error":{"message":message,"type":"new_api_error","param":"","code":""}})
        }
        RelayProtocol::Anthropic => {
            json!({"type":"error","error":{"type":"invalid_request_error","message":message}})
        }
        RelayProtocol::Gemini => {
            json!({"error":{"code":400,"message":message,"status":"INVALID_ARGUMENT"}})
        }
    };
    error_response(StatusCode::BAD_REQUEST, body, request_id)
}

fn failure(protocol: RelayProtocol, error: &RelayFailure, request_id: &str) -> Response {
    if matches!(error, RelayFailure::ConcealedNotFound) {
        return concealed_not_found();
    }
    if matches!(protocol, RelayProtocol::Anthropic | RelayProtocol::Gemini)
        && matches!(error, RelayFailure::Unauthorized)
    {
        return openai_failure(error, request_id);
    }
    let (status, type_name, gemini_status) = match error {
        RelayFailure::Unauthorized => (
            StatusCode::UNAUTHORIZED,
            "authentication_error",
            "UNAUTHENTICATED",
        ),
        RelayFailure::ConcealedNotFound => (
            StatusCode::NOT_FOUND,
            "authentication_error",
            "UNAUTHENTICATED",
        ),
        RelayFailure::NoChannel | RelayFailure::RouteUnavailable => {
            (StatusCode::SERVICE_UNAVAILABLE, "api_error", "UNAVAILABLE")
        }
        RelayFailure::Upstream | RelayFailure::Sse(_) => {
            (StatusCode::BAD_GATEWAY, "api_error", "UNAVAILABLE")
        }
        RelayFailure::Provider { status, .. } => (*status, "api_error", "UNAVAILABLE"),
    };
    let body = match error {
        RelayFailure::Provider { body, .. } => body.clone(),
        _ => match protocol {
            RelayProtocol::OpenAi => {
                json!({"error":{"message":"relay request could not be completed","type":"new_api_error","param":"","code":""}})
            }
            RelayProtocol::Anthropic => {
                json!({"type":"error","error":{"type":type_name,"message":"relay request could not be completed"}})
            }
            RelayProtocol::Gemini => {
                json!({"error":{"code":status.as_u16(),"message":"relay request could not be completed","status":gemini_status}})
            }
        },
    };
    error_response(status, body, request_id)
}

fn concealed_not_found() -> Response {
    (StatusCode::NOT_FOUND, Json(json!({"message":"Not Found"}))).into_response()
}

fn error_response(status: StatusCode, body: Value, request_id: &str) -> Response {
    let mut response = (status, Json(body)).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    add_compat_headers(&mut response, 0, request_id);
    response
}

fn add_compat_headers(response: &mut Response, channel_id: i64, request_id: &str) {
    if let Ok(value) = HeaderValue::from_str(request_id) {
        response.headers_mut().insert(REQUEST_ID, value);
    }
    if let Ok(value) = HeaderValue::from_str(request_id) {
        response.headers_mut().insert(LEGACY_REQUEST_ID, value);
    }
    if channel_id > 0
        && let Ok(value) = HeaderValue::from_str(&channel_id.to_string())
    {
        response.headers_mut().insert(CHANNEL_ID, value);
    }
}

#[cfg(test)]
mod tests {
    use super::{
        ConversionObserver, ConversionResult, ConverterVersion, FeatureClass, RelayBackend,
        RelayChannel, RelayFailure, RelayHttpState, RelayIdentity, RelayOutcome, RelayProtocol,
        RelaySseEvent, UpstreamReply, UpstreamRequest, failure, gemini_is_streaming,
        gemini_model_from_path, native_route_is_open, observe_native_sse_body, openai_failure,
        outcome_for, query_value, relay_failure_reason, relay_observation_labels,
        relay_observation_protocol, router, success, token_from_request,
    };
    use crate::RequestContext;
    use crate::conversion_observability::{FailureReason, MetricKind};
    use crate::protocol_rollout::ProtocolRolloutControl;
    use crate::protocol_runtime_registry::validated_current_registry;
    use async_trait::async_trait;
    use axum::{
        body::to_bytes,
        http::{Request, StatusCode, Uri, header},
    };
    use serde_json::json;
    use std::sync::{Arc, Mutex};
    use tower::ServiceExt;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    fn lock_unpoisoned<T>(mutex: &Mutex<T>) -> std::sync::MutexGuard<'_, T> {
        match mutex.lock() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        }
    }

    #[derive(Default)]
    struct RecordingBackend {
        tokens: Mutex<Vec<String>>,
        selected_models: Mutex<Vec<String>>,
        request_ids: Mutex<Vec<String>>,
    }

    #[async_trait]
    impl RelayBackend for RecordingBackend {
        async fn authenticate(&self, token: &str) -> Result<RelayIdentity, RelayFailure> {
            lock_unpoisoned(&self.tokens).push(token.to_owned());
            if token == "invalid" {
                Err(RelayFailure::ConcealedNotFound)
            } else {
                Ok(RelayIdentity {
                    token_id: "token-id".to_owned(),
                })
            }
        }

        async fn select_channel(
            &self,
            _identity: &RelayIdentity,
            _protocol: RelayProtocol,
            model: &str,
        ) -> Result<RelayChannel, RelayFailure> {
            lock_unpoisoned(&self.selected_models).push(model.to_owned());
            Ok(RelayChannel {
                id: 7,
                upstream_model: model.to_owned(),
            })
        }

        async fn invoke(
            &self,
            _channel: &RelayChannel,
            request: UpstreamRequest,
        ) -> Result<UpstreamReply, RelayFailure> {
            lock_unpoisoned(&self.request_ids).push(request.request_id.clone());
            Ok(UpstreamReply::Json(json!({
                "request_id": request.request_id,
            })))
        }

        async fn record_outcome(
            &self,
            _identity: Option<&RelayIdentity>,
            _channel: Option<&RelayChannel>,
            _outcome: RelayOutcome,
        ) {
        }
    }

    async fn response_body(
        response: axum::response::Response,
    ) -> Result<String, Box<dyn std::error::Error>> {
        let bytes = to_bytes(response.into_body(), usize::MAX).await?;
        Ok(String::from_utf8(bytes.to_vec())?)
    }

    fn request_with_context(
        uri: &str,
        body: &str,
    ) -> Result<Request<axum::body::Body>, axum::http::Error> {
        let mut request = Request::builder()
            .method("POST")
            .uri(uri)
            .header(header::CONTENT_TYPE, "application/json")
            .body(axum::body::Body::from(body.to_owned()))?;
        request.extensions_mut().insert(RequestContext {
            request_id: "fixed-request-id".to_owned(),
            client_ip: None,
        });
        Ok(request)
    }

    #[test]
    fn default_state_opens_native_request_and_non_stream_response_directions() {
        let state = RelayHttpState::new(Arc::new(RecordingBackend::default()));

        assert!(
            native_route_is_open(
                &state,
                RelayProtocol::Anthropic,
                "request-id",
                "claude-test",
                false,
            )
            .is_ok()
        );
    }

    #[test]
    fn default_state_opens_native_stream_direction() {
        let state = RelayHttpState::new(Arc::new(RecordingBackend::default()));

        assert!(
            native_route_is_open(
                &state,
                RelayProtocol::Gemini,
                "request-id",
                "gemini-test",
                true,
            )
            .is_ok()
        );
    }

    #[test]
    fn runtime_builder_replaces_the_default_registry() -> TestResult {
        let registry = Arc::new(validated_current_registry()?);
        let state = RelayHttpState::new(Arc::new(RecordingBackend::default()))
            .with_protocol_runtime(ProtocolRolloutControl::default(), registry.clone());

        assert!(
            state
                .validated_registry
                .as_ref()
                .is_some_and(|installed| Arc::ptr_eq(installed, &registry))
        );
        Ok(())
    }

    #[tokio::test]
    async fn missing_registry_fails_closed_after_authentication_before_channel_selection()
    -> TestResult {
        let backend = Arc::new(RecordingBackend::default());
        let state = RelayHttpState {
            backend: backend.clone(),
            protocol_rollout: ProtocolRolloutControl::default(),
            validated_registry: None,
        };
        let mut request =
            request_with_context("/v1/messages", r#"{"model":"claude-test","stream":false}"#)?;
        request.headers_mut().insert("x-api-key", "valid".parse()?);

        let response = router(state).oneshot(request).await?;

        assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(lock_unpoisoned(&backend.tokens).as_slice(), ["valid"]);
        assert!(lock_unpoisoned(&backend.selected_models).is_empty());
        assert!(lock_unpoisoned(&backend.request_ids).is_empty());
        Ok(())
    }

    #[test]
    fn provider_credentials_should_override_bearer_in_go_order() -> TestResult {
        let anthropic = Request::builder()
            .header(header::AUTHORIZATION, "Bearer bearer")
            .header("x-api-key", "anthropic")
            .body(axum::body::Body::empty())?;
        assert_eq!(
            token_from_request(&anthropic, RelayProtocol::Anthropic).as_deref(),
            Some("anthropic")
        );

        let gemini = Request::builder()
            .uri("/v1beta/models/gemini:generateContent?key=query")
            .header(header::AUTHORIZATION, "Bearer bearer")
            .body(axum::body::Body::empty())?;
        assert_eq!(
            token_from_request(&gemini, RelayProtocol::Gemini).as_deref(),
            Some("query")
        );

        let gemini_header = Request::builder()
            .uri("/v1beta/models/gemini:generateContent?key=query")
            .header(header::AUTHORIZATION, "Bearer bearer")
            .header("x-goog-api-key", "google")
            .body(axum::body::Body::empty())?;
        assert_eq!(
            token_from_request(&gemini_header, RelayProtocol::Gemini).as_deref(),
            Some("google")
        );

        let openai_model_delete = Request::builder()
            .uri("/v1/models/model?key=query")
            .header(header::AUTHORIZATION, "Bearer bearer")
            .header("x-api-key", "anthropic")
            .header("x-goog-api-key", "google")
            .body(axum::body::Body::empty())?;
        assert_eq!(
            token_from_request(&openai_model_delete, RelayProtocol::OpenAi).as_deref(),
            Some("google")
        );

        let empty_overrides = Request::builder()
            .uri("/v1beta/models/gemini:generateContent?key=")
            .header(header::AUTHORIZATION, "Bearer bearer")
            .header("x-goog-api-key", "")
            .body(axum::body::Body::empty())?;
        assert_eq!(
            token_from_request(&empty_overrides, RelayProtocol::Gemini).as_deref(),
            Some("bearer")
        );
        Ok(())
    }

    #[tokio::test]
    async fn fixed_request_context_id_should_match_body_response_and_upstream() -> TestResult {
        let backend = Arc::new(RecordingBackend::default());
        let mut request =
            request_with_context("/v1/messages", r#"{"model":"claude-test","stream":false}"#)?;
        request.headers_mut().insert("x-api-key", "valid".parse()?);
        let response = router(RelayHttpState::new(backend.clone()))
            .oneshot(request)
            .await?;

        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            response.headers()["x-oneapi-request-id"],
            "fixed-request-id"
        );
        assert_eq!(
            response_body(response).await?,
            r#"{"request_id":"fixed-request-id"}"#
        );
        assert_eq!(
            lock_unpoisoned(&backend.request_ids).as_slice(),
            ["fixed-request-id"]
        );
        Ok(())
    }

    async fn assert_concealed_credentials(
        uri: &str,
        body: &str,
        header_name: &'static str,
    ) -> TestResult {
        let backend = Arc::new(RecordingBackend::default());
        for credential in [None, Some("invalid")] {
            let mut request = request_with_context(uri, body)?;
            if let Some(token) = credential {
                request.headers_mut().insert(header_name, token.parse()?);
            }
            let response = router(RelayHttpState::new(backend.clone()))
                .oneshot(request)
                .await?;
            assert_eq!(response.status(), StatusCode::NOT_FOUND);
            assert_eq!(response_body(response).await?, r#"{"message":"Not Found"}"#);
        }
        Ok(())
    }

    #[tokio::test]
    async fn anthropic_missing_and_invalid_credentials_should_use_go_concealed_not_found()
    -> TestResult {
        assert_concealed_credentials("/v1/messages", r#"{"model":"claude-test"}"#, "x-api-key")
            .await
    }

    #[tokio::test]
    async fn gemini_missing_and_invalid_credentials_should_use_go_concealed_not_found() -> TestResult
    {
        assert_concealed_credentials(
            "/v1beta/models/gemini-test:generateContent",
            r#"{"contents":[]}"#,
            "x-goog-api-key",
        )
        .await
    }

    #[test]
    fn upstream_failure_should_be_recorded_as_an_upstream_outcome() {
        assert_eq!(
            outcome_for(&RelayFailure::Upstream),
            RelayOutcome::UpstreamFailure
        );
    }

    #[test]
    fn observation_labels_map_provider_routes_to_same_protocols() {
        for (route, protocol) in [
            (
                RelayProtocol::OpenAi,
                lmm_contracts::relay::Protocol::OpenAi,
            ),
            (
                RelayProtocol::Anthropic,
                lmm_contracts::relay::Protocol::Claude,
            ),
            (
                RelayProtocol::Gemini,
                lmm_contracts::relay::Protocol::Gemini,
            ),
        ] {
            let labels = relay_observation_labels(route, true, ConversionResult::Success);
            assert_eq!(labels.source_format, protocol);
            assert_eq!(labels.target_format, protocol);
            assert_eq!(labels.converter_version, ConverterVersion::NativeRawV1);
            assert_eq!(labels.feature_class, FeatureClass::Stream);
        }
    }

    #[test]
    fn observation_failure_reasons_remain_closed() {
        assert_eq!(
            relay_failure_reason(&RelayFailure::Upstream),
            FailureReason::Upstream
        );
        assert_eq!(
            relay_failure_reason(&RelayFailure::NoChannel),
            FailureReason::Unknown
        );
        assert_eq!(
            relay_observation_protocol(RelayProtocol::Anthropic),
            lmm_contracts::relay::Protocol::Claude
        );
    }

    #[tokio::test]
    async fn native_sse_observation_preserves_raw_body_and_stream_metrics() -> TestResult {
        let observer = ConversionObserver::default();
        let labels =
            relay_observation_labels(RelayProtocol::Anthropic, true, ConversionResult::Success);
        let raw = b"event: message_start\ndata: {\"type\":\"message_start\"}\n\n";
        let body = observe_native_sse_body(
            axum::body::Body::from(raw.to_vec()),
            observer.clone(),
            labels,
        );
        let bytes = to_bytes(body, usize::MAX).await?;
        assert_eq!(bytes.as_ref(), raw);

        let snapshot = observer.snapshot();
        assert!(snapshot.samples.iter().any(|sample| {
            sample.metric == MetricKind::ConversionOutputBytes && sample.value == raw.len() as u64
        }));
        assert!(
            snapshot
                .samples
                .iter()
                .any(|sample| sample.metric == MetricKind::StreamGatewayTtftSeconds)
        );
        assert!(
            !snapshot
                .samples
                .iter()
                .any(|sample| sample.metric == MetricKind::StreamClientAbortTotal)
        );
        Ok(())
    }

    #[tokio::test]
    async fn buffered_replies_record_wire_output_bytes() -> TestResult {
        let json_observer = ConversionObserver::default();
        let json_body = json!({"ok":true});
        let json_bytes = json_body.to_string().len() as u64;
        let json_response = success(
            UpstreamReply::Json(json_body),
            7,
            "request-id",
            RelayProtocol::Anthropic,
            &json_observer,
        );
        assert_eq!(json_response.status(), StatusCode::OK);
        let json_wire = to_bytes(json_response.into_body(), usize::MAX).await?;
        assert_eq!(json_wire.len() as u64, json_bytes);
        assert!(json_observer.snapshot().samples.iter().any(|sample| {
            sample.metric == MetricKind::ConversionOutputBytes && sample.value == json_bytes
        }));

        let sse_observer = ConversionObserver::default();
        let sse_bytes = "event: message\ndata: {\"text\":\"ok\"}\n\ndata: [DONE]\n\n";
        let sse_response = success(
            UpstreamReply::Sse(vec![RelaySseEvent {
                kind: Some("message".to_owned()),
                payload: json!({"text":"ok"}),
            }]),
            7,
            "request-id",
            RelayProtocol::Anthropic,
            &sse_observer,
        );
        assert_eq!(sse_response.status(), StatusCode::OK);
        let sse_wire = to_bytes(sse_response.into_body(), usize::MAX).await?;
        assert_eq!(sse_wire.len(), sse_bytes.len());
        assert!(sse_observer.snapshot().samples.iter().any(|sample| {
            sample.metric == MetricKind::ConversionOutputBytes
                && sample.value == sse_bytes.len() as u64
        }));
        Ok(())
    }

    #[test]
    fn query_credentials_should_use_form_urlencoding_and_first_value() -> TestResult {
        assert_eq!(
            query_value(Some("key=encoded%2Ftoken%2Bvalue&key=second"), "key"),
            Some("encoded/token+value".to_owned())
        );
        assert_eq!(query_value(Some("key="), "key"), Some(String::new()));
        assert_eq!(
            query_value(Some("key=token+with+spaces"), "key"),
            Some("token with spaces".to_owned())
        );

        let request = Request::builder()
            .uri("/v1beta/models/gemini:generateContent?key=encoded%2Ftoken%2Bvalue&key=second")
            .body(axum::body::Body::empty())?;
        assert_eq!(
            token_from_request(&request, RelayProtocol::Gemini).as_deref(),
            Some("encoded/token+value")
        );
        Ok(())
    }

    #[test]
    fn gemini_header_should_override_decoded_query_and_bearer() -> TestResult {
        let request = Request::builder()
            .uri("/v1beta/models/gemini:generateContent?key=query")
            .header(header::AUTHORIZATION, "Bearer bearer")
            .header("x-goog-api-key", "header")
            .body(axum::body::Body::empty())?;
        assert_eq!(
            token_from_request(&request, RelayProtocol::Gemini).as_deref(),
            Some("header")
        );
        Ok(())
    }

    #[tokio::test]
    async fn provider_failures_should_preserve_body_status_and_request_ids() -> TestResult {
        let body = json!({
            "error": {
                "code": "provider_code",
                "message": "provider message",
                "metadata": {"retryable": true}
            }
        });
        for protocol in [RelayProtocol::Anthropic, RelayProtocol::Gemini] {
            let error = RelayFailure::Provider {
                status: StatusCode::TOO_MANY_REQUESTS,
                body: body.clone(),
            };
            let response = failure(protocol, &error, "provider-request-id");
            assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
            assert_eq!(
                response.headers()[header::CONTENT_TYPE],
                "application/json; charset=utf-8"
            );
            assert_eq!(response.headers()["x-request-id"], "provider-request-id");
            assert_eq!(
                response.headers()["x-oneapi-request-id"],
                "provider-request-id"
            );
            assert_eq!(response_body(response).await?, body.to_string());
            assert_eq!(outcome_for(&error), RelayOutcome::UpstreamFailure);
        }
        Ok(())
    }

    #[test]
    fn gemini_wildcard_model_extraction_should_match_legacy_paths() {
        assert_eq!(
            gemini_model_from_path("/v1beta/models/gemini-2.5-pro:countTokens"),
            Some("gemini-2.5-pro")
        );
        assert_eq!(
            gemini_model_from_path("/v1/models/model/legacy-tail"),
            Some("model/legacy-tail")
        );
        assert_eq!(gemini_model_from_path("/v1beta/models/"), None);
    }

    #[test]
    fn gemini_stream_detection_should_accept_native_action_or_alt_sse() -> TestResult {
        let action: Uri = "/v1beta/models/gemini:streamGenerateContent".parse()?;
        let query: Uri = "/v1beta/models/gemini:generateContent?alt=sse".parse()?;
        let ordinary: Uri = "/v1beta/models/gemini:generateContent".parse()?;

        assert!(gemini_is_streaming(&action));
        assert!(gemini_is_streaming(&query));
        assert!(!gemini_is_streaming(&ordinary));
        Ok(())
    }

    #[tokio::test]
    async fn gemini_unauthorized_should_use_openai_invalid_token_envelope() -> TestResult {
        let response = failure(
            super::RelayProtocol::Gemini,
            &RelayFailure::Unauthorized,
            "request-123",
        );

        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "application/json; charset=utf-8"
        );
        assert_eq!(response.headers()["x-request-id"], "request-123");
        assert_eq!(
            response_body(response).await?,
            r#"{"error":{"code":"","message":"Invalid token (request id: request-123)","type":"new_api_error"}}"#
        );
        Ok(())
    }

    #[tokio::test]
    async fn openai_unauthorized_should_omit_param_from_invalid_token_envelope() -> TestResult {
        let response = openai_failure(&RelayFailure::Unauthorized, "request-456");

        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "application/json; charset=utf-8"
        );
        assert_eq!(response.headers()["x-request-id"], "request-456");
        assert_eq!(
            response_body(response).await?,
            r#"{"error":{"code":"","message":"Invalid token (request id: request-456)","type":"new_api_error"}}"#
        );
        Ok(())
    }
}
