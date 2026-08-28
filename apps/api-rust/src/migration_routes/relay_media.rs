//! OpenAI media relay surface.
//!
//! This module deliberately does not parse request bodies.  Audio transcription
//! and image editing requests are multipart, file content is binary, and some
//! upstreams stream their response bodies.  Passing the Axum request and
//! response through the service boundary intact preserves all three cases.

use std::{
    collections::HashSet,
    net::IpAddr,
    sync::Arc,
    time::{Duration, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Router,
    body::{Body, to_bytes},
    extract::{Request, State},
    http::{HeaderMap, HeaderName, Method, StatusCode, header},
    response::{IntoResponse, Response},
    routing::post,
};
use sqlx::{PgPool, Row};

use crate::RequestContext;

const DEFAULT_MAX_ATTEMPTS: u8 = 2;
const DEFAULT_MAX_REQUEST_BYTES: usize = 64 * 1024 * 1024;

/// An already-selected legacy channel.  The channel key stays server-side and
/// is deliberately never copied from a caller header.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct MediaUpstreamTarget {
    /// Canonical channel base URL, without an endpoint path.
    pub base_url: String,
    /// Credential injected as the channel's upstream bearer credential.
    pub api_key: String,
}

/// Concrete HTTP client used by the production channel adapter.
///
/// Construct it with a `reqwest::Client` that has `rustls-tls`, `stream`, and a
/// finite client-wide timeout enabled.  The additional timeout here bounds the
/// time to upstream response headers; it avoids an indefinitely stalled
/// connection even when a caller accidentally supplies a less restrictive
/// client.
#[derive(Clone)]
pub struct MediaUpstreamClient {
    client: reqwest::Client,
    response_header_timeout: Duration,
    max_attempts: u8,
}

impl MediaUpstreamClient {
    /// Creates a retrying upstream client.  Attempts must be positive.
    #[must_use]
    pub fn new(client: reqwest::Client, response_header_timeout: Duration) -> Self {
        Self {
            client,
            response_header_timeout,
            max_attempts: DEFAULT_MAX_ATTEMPTS,
        }
    }

    /// Overrides the number of attempts for safe replay tests or deployments
    /// that have an explicit idempotency policy.
    #[must_use]
    pub const fn with_max_attempts(mut self, max_attempts: u8) -> Self {
        self.max_attempts = max_attempts;
        self
    }

    /// Sends the exact media payload to the selected channel and returns an
    /// Axum response backed by the upstream byte stream.  Request bodies are
    /// buffered by the outer transactional adapter so retrying cannot consume
    /// an already-drained multipart stream.
    pub async fn forward(
        &self,
        target: &MediaUpstreamTarget,
        method: Method,
        path_and_query: &str,
        inbound_headers: &HeaderMap,
        body: Vec<u8>,
        allow_retry: bool,
    ) -> Result<Response, MediaUpstreamError> {
        let url = upstream_url(&target.base_url, path_and_query)?;
        // A test instance must never turn a copied production channel into an
        // internet egress path.  Only an explicit loopback mock is usable;
        // production has no such restriction.
        if test_instance_enabled() && !is_loopback_mock_target(&url) {
            return Err(MediaUpstreamError::InvalidTarget);
        }
        let attempts = if allow_retry {
            self.max_attempts.max(1)
        } else {
            1
        };
        let mut last_error = None;
        for attempt in 0..attempts {
            let request = copy_request_headers(
                self.client.request(method.clone(), url.clone()),
                inbound_headers,
                &target.api_key,
            )
            .body(body.clone());
            match tokio::time::timeout(self.response_header_timeout, request.send()).await {
                Ok(Ok(response)) => return Ok(stream_response(response)),
                Ok(Err(error)) => last_error = Some(MediaUpstreamError::Transport(error)),
                Err(_) => last_error = Some(MediaUpstreamError::Timeout),
            }
            if attempt + 1 < attempts {
                tokio::task::yield_now().await;
            }
        }
        Err(last_error.unwrap_or(MediaUpstreamError::Timeout))
    }
}

/// A failure before an upstream HTTP response exists.
#[derive(Debug)]
pub enum MediaUpstreamError {
    /// The configured base URL cannot safely form an endpoint URL.
    InvalidTarget,
    /// Connecting, writing, or reading response headers failed.
    Transport(reqwest::Error),
    /// No response headers arrived before the configured deadline.
    Timeout,
}

impl MediaUpstreamError {
    /// Legacy OpenAI-compatible failure response without leaking target data.
    #[must_use]
    pub fn response(&self) -> Response {
        let message = match self {
            Self::InvalidTarget => "invalid upstream target",
            Self::Transport(_) | Self::Timeout => "upstream request failed",
        };
        (
            StatusCode::BAD_GATEWAY,
            axum::Json(serde_json::json!({"error":{"message":message,"type":"new_api_error","param":"","code":"upstream_error"}})),
        )
            .into_response()
    }
}

fn upstream_url(base_url: &str, path_and_query: &str) -> Result<reqwest::Url, MediaUpstreamError> {
    let base = reqwest::Url::parse(base_url).map_err(|_| MediaUpstreamError::InvalidTarget)?;
    if !matches!(base.scheme(), "http" | "https") || base.host_str().is_none() {
        return Err(MediaUpstreamError::InvalidTarget);
    }
    let path_and_query = path_and_query.strip_prefix('/').unwrap_or(path_and_query);
    base.join(path_and_query)
        .map_err(|_| MediaUpstreamError::InvalidTarget)
}

fn test_instance_enabled() -> bool {
    std::env::var("LMM_RS_TEST_INSTANCE").is_ok_and(|value| value == "1")
}

fn is_loopback_mock_target(url: &reqwest::Url) -> bool {
    url.host_str().is_some_and(|host| {
        host.eq_ignore_ascii_case("localhost")
            || host
                .parse::<IpAddr>()
                .is_ok_and(|address| address.is_loopback())
    })
}

fn copy_request_headers(
    mut request: reqwest::RequestBuilder,
    inbound: &HeaderMap,
    api_key: &str,
) -> reqwest::RequestBuilder {
    let hop_by_hop = request_hop_by_hop_headers(inbound);
    for (name, value) in inbound {
        if hop_by_hop.contains(name) {
            continue;
        }
        request = request.header(name, value);
    }
    if !api_key.is_empty() {
        request = request.header(header::AUTHORIZATION, format!("Bearer {api_key}"));
    }
    request
}

/// Headers that must never cross a proxy boundary.  RFC 9110 also permits a
/// request's `Connection` header to name additional hop-by-hop fields; honor
/// those names instead of accidentally forwarding caller-local metadata to a
/// selected channel.
fn request_hop_by_hop_headers(inbound: &HeaderMap) -> HashSet<HeaderName> {
    let mut headers = [
        "authorization",
        "host",
        "content-length",
        "connection",
        "keep-alive",
        "proxy-authenticate",
        "proxy-authorization",
        "te",
        "trailer",
        "transfer-encoding",
        "upgrade",
    ]
    .into_iter()
    .map(HeaderName::from_static)
    .collect::<HashSet<_>>();

    for value in inbound.get_all(header::CONNECTION) {
        let Ok(value) = value.to_str() else {
            continue;
        };
        for name in value
            .split(',')
            .map(str::trim)
            .filter(|name| !name.is_empty())
        {
            if let Ok(name) = HeaderName::from_bytes(name.as_bytes()) {
                headers.insert(name);
            }
        }
    }
    headers
}

fn stream_response(response: reqwest::Response) -> Response {
    let status = response.status();
    let headers = response.headers().clone();
    let mut output = Response::new(Body::from_stream(response.bytes_stream()));
    *output.status_mut() = status;
    for (name, value) in &headers {
        if !is_hop_by_hop_header(name) {
            output.headers_mut().append(name, value.clone());
        }
    }
    output
}

fn is_hop_by_hop_header(name: &axum::http::HeaderName) -> bool {
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

/// Performs authentication, channel selection, accounting, and upstream I/O.
///
/// The HTTP layer owns only route selection.  In particular, implementations
/// must authenticate before forwarding and apply the legacy PostgreSQL/Valkey
/// effects for both accepted and rejected relay requests.
#[async_trait]
pub trait RelayMediaService: Send + Sync {
    /// Relay an unmodified OpenAI-compatible media request.
    ///
    /// Returning the upstream response directly preserves status, headers, and
    /// binary or streamed response bodies without JSON re-serialization.
    async fn relay(&self, request: Request) -> Response;
}

/// PostgreSQL-backed media relay adapter.
///
/// This is a deliberately narrow production vertical slice: it validates the
/// legacy token and user state, enforces token IP limits and model limits,
/// selects an enabled compatible channel from PostgreSQL, then records the
/// completed request in the same transaction as quota/counter mutations.  It
/// does not use an in-memory test double for any of those effects.
///
/// A media POST is not blindly retried: image and speech generation are not
/// generally idempotent, so retrying after an unknown upstream write could
/// duplicate work and billing.  `MediaUpstreamClient` retains its bounded
/// retry facility for a caller with an explicit idempotency policy.
#[derive(Clone)]
pub struct PgRelayMediaService {
    pg: PgPool,
    upstream: MediaUpstreamClient,
    quota_per_request: i64,
    max_request_bytes: usize,
}

impl PgRelayMediaService {
    /// Builds the real adapter. `quota_per_request` must be non-negative.
    #[must_use]
    pub fn new(pg: PgPool, upstream: MediaUpstreamClient, quota_per_request: i64) -> Self {
        Self {
            pg,
            upstream,
            quota_per_request: quota_per_request.max(0),
            max_request_bytes: DEFAULT_MAX_REQUEST_BYTES,
        }
    }

    /// Sets the body cap applied before multipart data is buffered for one
    /// exact upstream replay.
    #[must_use]
    pub const fn with_max_request_bytes(mut self, max_request_bytes: usize) -> Self {
        self.max_request_bytes = max_request_bytes;
        self
    }

    async fn authenticate(
        &self,
        headers: &HeaderMap,
        client_ip: Option<IpAddr>,
    ) -> Result<MediaIdentity, MediaFailure> {
        let key = bearer_token(headers).ok_or(MediaFailure::Unauthorized)?;
        let row = sqlx::query("SELECT t.id AS token_id,t.user_id,COALESCE(t.status,1) AS token_status,COALESCE(t.expired_time,-1) AS expired_time,COALESCE(t.remain_quota,0) AS remain_quota,COALESCE(t.unlimited_quota,FALSE) AS unlimited_quota,COALESCE(t.allow_ips,'') AS allow_ips,COALESCE(t.model_limits_enabled,FALSE) AS model_limits_enabled,COALESCE(t.model_limits,'') AS model_limits,COALESCE(t.\"group\",'') AS token_group,COALESCE(u.status,1) AS user_status,COALESCE(u.quota,0) AS user_quota,COALESCE(u.\"group\",'default') AS user_group FROM tokens t JOIN users u ON u.id=t.user_id WHERE t.key=$1 AND t.deleted_at IS NULL AND u.deleted_at IS NULL")
            .bind(key)
            .fetch_optional(&self.pg)
            .await
            .map_err(|_| MediaFailure::Storage)?
            .ok_or(MediaFailure::Unauthorized)?;
        let token_status: i64 = row
            .try_get("token_status")
            .map_err(|_| MediaFailure::Storage)?;
        let expired_time: i64 = row
            .try_get("expired_time")
            .map_err(|_| MediaFailure::Storage)?;
        let remain_quota: i64 = row
            .try_get("remain_quota")
            .map_err(|_| MediaFailure::Storage)?;
        let unlimited_quota: bool = row
            .try_get("unlimited_quota")
            .map_err(|_| MediaFailure::Storage)?;
        if token_status != 1
            || (expired_time != -1 && expired_time < epoch_seconds())
            || (!unlimited_quota && remain_quota < self.quota_per_request)
        {
            return Err(MediaFailure::Unauthorized);
        }
        let user_status: i64 = row
            .try_get("user_status")
            .map_err(|_| MediaFailure::Storage)?;
        if user_status != 1 {
            return Err(MediaFailure::Forbidden);
        }
        let user_quota: i64 = row
            .try_get("user_quota")
            .map_err(|_| MediaFailure::Storage)?;
        if user_quota < self.quota_per_request {
            return Err(MediaFailure::InsufficientQuota);
        }
        let allow_ips: String = row
            .try_get("allow_ips")
            .map_err(|_| MediaFailure::Storage)?;
        if !ip_is_allowed(client_ip, &allow_ips) {
            return Err(MediaFailure::Forbidden);
        }
        Ok(MediaIdentity {
            user_id: row.try_get("user_id").map_err(|_| MediaFailure::Storage)?,
            token_id: row.try_get("token_id").map_err(|_| MediaFailure::Storage)?,
            user_group: row
                .try_get("user_group")
                .map_err(|_| MediaFailure::Storage)?,
            token_group: row
                .try_get("token_group")
                .map_err(|_| MediaFailure::Storage)?,
            model_limits_enabled: row
                .try_get("model_limits_enabled")
                .map_err(|_| MediaFailure::Storage)?,
            model_limits: row
                .try_get("model_limits")
                .map_err(|_| MediaFailure::Storage)?,
        })
    }

    async fn select_channel(
        &self,
        identity: &MediaIdentity,
        model: &str,
    ) -> Result<MediaChannel, MediaFailure> {
        if identity.model_limits_enabled
            && !identity
                .model_limits
                .split(',')
                .any(|allowed| allowed.trim() == model)
        {
            return Err(MediaFailure::Forbidden);
        }
        let group_name = if identity.token_group.trim().is_empty() {
            identity.user_group.as_str()
        } else {
            identity.token_group.as_str()
        };
        // `abilities` is the authoritative model-to-channel distributor table;
        // checking a channel's display `models` string would admit a channel
        // that legacy distribution intentionally disabled for this group.
        let row = sqlx::query("SELECT c.id,COALESCE(c.base_url,'') AS base_url,c.key FROM abilities a JOIN channels c ON c.id=a.channel_id WHERE a.\"group\"=$1 AND a.model=$2 AND COALESCE(a.enabled,TRUE) AND c.status=1 ORDER BY COALESCE(a.priority,0) DESC,COALESCE(a.weight,0) DESC,c.id ASC LIMIT 1")
            .bind(group_name)
            .bind(model)
            .fetch_optional(&self.pg)
            .await
            .map_err(|_| MediaFailure::Storage)?
            .ok_or(MediaFailure::NoChannel)?;
        Ok(MediaChannel {
            id: row.try_get("id").map_err(|_| MediaFailure::Storage)?,
            target: MediaUpstreamTarget {
                base_url: row.try_get("base_url").map_err(|_| MediaFailure::Storage)?,
                api_key: row.try_get("key").map_err(|_| MediaFailure::Storage)?,
            },
        })
    }

    async fn record_success(
        &self,
        identity: &MediaIdentity,
        channel: &MediaChannel,
        model: &str,
        client_ip: Option<IpAddr>,
    ) -> Result<(), MediaFailure> {
        let quota = self.quota_per_request;
        let now = epoch_seconds();
        let mut transaction = self.pg.begin().await.map_err(|_| MediaFailure::Storage)?;
        let user = sqlx::query("UPDATE users SET quota=COALESCE(quota,0)-$2,used_quota=COALESCE(used_quota,0)+$2,request_count=COALESCE(request_count,0)+1 WHERE id=$1 AND status=1 AND deleted_at IS NULL AND COALESCE(quota,0)>=$2")
            .bind(identity.user_id).bind(quota).execute(&mut *transaction).await.map_err(|_| MediaFailure::Storage)?;
        if user.rows_affected() != 1 {
            return Err(MediaFailure::Unauthorized);
        }
        let token = sqlx::query("UPDATE tokens SET accessed_time=$2,used_quota=COALESCE(used_quota,0)+$3,remain_quota=CASE WHEN COALESCE(unlimited_quota,FALSE) THEN remain_quota ELSE COALESCE(remain_quota,0)-$3 END WHERE id=$1 AND user_id=$4 AND status=1 AND deleted_at IS NULL AND (COALESCE(unlimited_quota,FALSE) OR COALESCE(remain_quota,0)>=$3)")
            .bind(identity.token_id).bind(now).bind(quota).bind(identity.user_id).execute(&mut *transaction).await.map_err(|_| MediaFailure::Storage)?;
        if token.rows_affected() != 1 {
            return Err(MediaFailure::Unauthorized);
        }
        sqlx::query(
            "UPDATE channels SET used_quota=COALESCE(used_quota,0)+$2 WHERE id=$1 AND status=1",
        )
        .bind(channel.id)
        .bind(quota)
        .execute(&mut *transaction)
        .await
        .map_err(|_| MediaFailure::Storage)?;
        sqlx::query("INSERT INTO logs (user_id,created_at,type,content,model_name,quota,channel_id,token_id,\"group\",ip) VALUES ($1,$2,2,'',$3,$4,$5,$6,$7,$8)")
            .bind(identity.user_id).bind(now).bind(model).bind(quota).bind(channel.id).bind(identity.token_id).bind(&identity.user_group).bind(client_ip.map_or_else(String::new, |ip| ip.to_string())).execute(&mut *transaction).await.map_err(|_| MediaFailure::Storage)?;
        transaction
            .commit()
            .await
            .map_err(|_| MediaFailure::Storage)
    }
}

#[derive(Debug)]
struct MediaIdentity {
    user_id: i64,
    token_id: i64,
    user_group: String,
    token_group: String,
    model_limits_enabled: bool,
    model_limits: String,
}

#[derive(Debug)]
struct MediaChannel {
    id: i64,
    target: MediaUpstreamTarget,
}

#[derive(Clone, Copy, Debug)]
enum MediaFailure {
    Unauthorized,
    Forbidden,
    InsufficientQuota,
    NoChannel,
    Storage,
}

#[async_trait]
impl RelayMediaService for PgRelayMediaService {
    async fn relay(&self, request: Request) -> Response {
        let request_id = request_id(&request);
        let client_ip = request
            .extensions()
            .get::<RequestContext>()
            .and_then(|context| context.client_ip);
        let headers = request.headers().clone();
        let identity = match self.authenticate(&headers, client_ip).await {
            Ok(identity) => identity,
            Err(error) => return media_failure(error, &request_id),
        };
        let method = request.method().clone();
        let path_and_query = request.uri().path_and_query().map_or_else(
            || request.uri().path().to_owned(),
            |value| value.as_str().to_owned(),
        );
        // The legacy audio transcription/translation handlers call
        // ParseMultipartForm before selecting a model.  A JSON request (or a
        // multipart content type without a boundary) therefore fails with a
        // 500 `count_token_failed` envelope instead of reaching the provider.
        // Keep that malformed-input boundary identical so Rust cannot turn an
        // invalid upload into a billable upstream request.
        if requires_multipart_audio(&path_and_query) && !has_multipart_boundary(&headers) {
            return media_multipart_parse_error(&request_id);
        }
        let body = match to_bytes(request.into_body(), self.max_request_bytes).await {
            Ok(body) => body.to_vec(),
            Err(_) => {
                return media_error(
                    StatusCode::PAYLOAD_TOO_LARGE,
                    "request body too large",
                    &request_id,
                );
            }
        };
        let model = match media_model(&path_and_query, &headers, &body) {
            Some(model) => model,
            None => return media_error(StatusCode::BAD_REQUEST, "model is required", &request_id),
        };
        let body = match normalize_media_body(&path_and_query, &headers, body) {
            Ok(body) => body,
            Err(_) => return media_failure(MediaFailure::Storage, &request_id),
        };
        let channel = match self.select_channel(&identity, &model).await {
            Ok(channel) => channel,
            Err(error) => return media_failure(error, &request_id),
        };
        let response = match self
            .upstream
            .forward(
                &channel.target,
                method,
                &path_and_query,
                &headers,
                body,
                false,
            )
            .await
        {
            Ok(response) => response,
            Err(error) => return error.response(),
        };
        if response.status().is_success()
            && self
                .record_success(&identity, &channel, &model, client_ip)
                .await
                .is_err()
        {
            return media_failure(MediaFailure::Storage, &request_id);
        }
        response
    }
}

fn bearer_token(headers: &HeaderMap) -> Option<&str> {
    fn trim_scheme(value: &str) -> &str {
        value
            .strip_prefix("Bearer ")
            .or_else(|| value.strip_prefix("bearer "))
            .unwrap_or(value)
            .trim()
    }

    let authorization = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .map(trim_scheme)
        .unwrap_or_default();
    let raw = if authorization.is_empty() || authorization == "midjourney-proxy" {
        headers
            .get("mj-api-secret")
            .and_then(|value| value.to_str().ok())
            .map(trim_scheme)
            .unwrap_or_default()
    } else {
        authorization
    };
    let raw = raw.strip_prefix("sk-").unwrap_or(raw);
    raw.split('-')
        .next()
        .filter(|key| !key.is_empty() && *key != "midjourney-proxy")
}

fn media_model(path_and_query: &str, headers: &HeaderMap, body: &[u8]) -> Option<String> {
    if headers
        .get(header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|content_type| content_type.starts_with("application/json"))
    {
        let value = serde_json::from_slice::<serde_json::Value>(body).ok()?;
        return value
            .get("model")
            .and_then(serde_json::Value::as_str)
            .filter(|model| !model.trim().is_empty())
            .map(str::to_owned)
            .or_else(|| media_default_model(path_and_query).map(str::to_owned));
    }
    // Multipart bodies are retained byte-for-byte.  Extract only the small
    // `name="model"` text part needed for channel selection; file fields stay opaque.
    let marker = b"name=\"model\"";
    let start = body
        .windows(marker.len())
        .position(|window| window == marker)?
        + marker.len();
    let value_start = body[start..]
        .windows(4)
        .position(|window| window == b"\r\n\r\n")?
        + start
        + 4;
    let value_end = body[value_start..]
        .windows(2)
        .position(|window| window == b"\r\n")?
        + value_start;
    std::str::from_utf8(&body[value_start..value_end])
        .ok()
        .map(str::trim)
        .filter(|model| !model.is_empty())
        .map(str::to_owned)
        .or_else(|| media_default_model(path_and_query).map(str::to_owned))
}

fn media_default_model(path_and_query: &str) -> Option<&'static str> {
    let path = path_and_query
        .split_once('?')
        .map_or(path_and_query, |(path, _)| path);
    match path {
        "/v1/audio/speech" => Some("tts-1"),
        "/v1/audio/transcriptions" | "/v1/audio/translations" => Some("whisper-1"),
        "/v1/images/generations" => Some("dall-e"),
        "/v1/images/edits" => Some("gpt-image-1"),
        _ => None,
    }
}

#[derive(Debug)]
enum MediaBodyError {
    Serialization(serde_json::Error),
}

impl std::fmt::Display for MediaBodyError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Serialization(error) => {
                write!(formatter, "media body serialization failed: {error}")
            }
        }
    }
}

impl std::error::Error for MediaBodyError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Serialization(error) => Some(error),
        }
    }
}

fn normalize_media_body(
    path_and_query: &str,
    headers: &HeaderMap,
    body: Vec<u8>,
) -> Result<Vec<u8>, MediaBodyError> {
    let path = path_and_query
        .split_once('?')
        .map_or(path_and_query, |(path, _)| path);
    let is_json = headers
        .get(header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.starts_with("application/json"));
    if !is_json || !matches!(path, "/v1/images/generations" | "/v1/images/edits") {
        return Ok(body);
    }
    let Ok(mut value) = serde_json::from_slice::<serde_json::Value>(&body) else {
        return Ok(body);
    };
    let Some(object) = value.as_object_mut() else {
        return Ok(body);
    };
    // The Go image adapter applies these defaults while decoding the request,
    // before it forwards JSON to the selected channel.
    object
        .entry("n")
        .or_insert_with(|| serde_json::Value::from(1));
    if path == "/v1/images/edits" {
        object
            .entry("prompt")
            .or_insert_with(|| serde_json::Value::String(String::new()));
    }
    serde_json::to_vec(&value).map_err(MediaBodyError::Serialization)
}

fn requires_multipart_audio(path_and_query: &str) -> bool {
    let path = path_and_query
        .split_once('?')
        .map_or(path_and_query, |(path, _)| path);
    matches!(path, "/v1/audio/transcriptions" | "/v1/audio/translations")
}

fn has_multipart_boundary(headers: &HeaderMap) -> bool {
    headers
        .get(header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| {
            value.split(';').next().is_some_and(|media_type| {
                media_type
                    .trim()
                    .eq_ignore_ascii_case("multipart/form-data")
            }) && value
                .split(';')
                .skip(1)
                .any(|parameter| parameter.trim_start().starts_with("boundary="))
        })
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
    UNIX_EPOCH
        .elapsed()
        .map_or(0, |elapsed| elapsed.as_secs() as i64)
}

fn request_id(request: &Request) -> String {
    request
        .extensions()
        .get::<RequestContext>()
        .map(|context| context.request_id.clone())
        .or_else(|| {
            request
                .headers()
                .get("x-oneapi-request-id")
                .and_then(|value| value.to_str().ok())
                .filter(|value| !value.is_empty())
                .map(str::to_owned)
        })
        .unwrap_or_else(|| uuid::Uuid::new_v4().to_string())
}

fn media_failure(error: MediaFailure, request_id: &str) -> Response {
    let (status, message) = match error {
        MediaFailure::Unauthorized => (StatusCode::UNAUTHORIZED, "Invalid token"),
        MediaFailure::Forbidden => (StatusCode::FORBIDDEN, "access denied"),
        MediaFailure::InsufficientQuota => (StatusCode::FORBIDDEN, "insufficient quota"),
        MediaFailure::NoChannel => (
            StatusCode::SERVICE_UNAVAILABLE,
            "relay request could not be completed",
        ),
        MediaFailure::Storage => (
            StatusCode::INTERNAL_SERVER_ERROR,
            "relay request could not be completed",
        ),
    };
    media_error(status, message, request_id)
}

fn media_error(status: StatusCode, message: &str, request_id: &str) -> Response {
    media_error_with_code(status, message, "", request_id)
}

fn media_multipart_parse_error(request_id: &str) -> Response {
    (
        StatusCode::INTERNAL_SERVER_ERROR,
        axum::Json(serde_json::json!({"error":{"message":format!("error parsing multipart form: multipart boundary not found (request id: {request_id})"),"type":"new_api_error","param":"","code":"count_token_failed"}})),
    )
        .into_response()
}

fn media_error_with_code(
    status: StatusCode,
    message: &str,
    code: &str,
    request_id: &str,
) -> Response {
    (
        status,
        axum::Json(serde_json::json!({"error":{"message":format!("{message} (request id: {request_id})"),"type":"new_api_error","code":code}})),
    )
        .into_response()
}

#[derive(Clone)]
pub struct RelayMediaHttpState {
    service: Arc<dyn RelayMediaService>,
}

impl RelayMediaHttpState {
    #[must_use]
    pub fn new(service: Arc<dyn RelayMediaService>) -> Self {
        Self { service }
    }
}

/// Routes whose payloads must remain opaque to retain OpenAI media semantics.
///
/// `images/variations` and every `files` endpoint intentionally do **not**
/// appear here.  The frozen listener authenticates those requests and returns
/// the legacy fixed error envelope; [`super::relay_misc`] owns that
/// behaviour.  Keeping them out of this forwarding router prevents a
/// successful upstream response from changing that observable contract.
pub fn relay_media_router(state: RelayMediaHttpState) -> Router {
    Router::new()
        .route("/v1/audio/speech", post(relay_media))
        .route("/v1/audio/transcriptions", post(relay_media))
        .route("/v1/audio/translations", post(relay_media))
        .route("/v1/images/generations", post(relay_media))
        .route("/v1/images/edits", post(relay_media))
        .with_state(state)
}

async fn relay_media(State(state): State<RelayMediaHttpState>, request: Request) -> Response {
    if bearer_token(request.headers()).is_none() {
        return media_error(
            StatusCode::UNAUTHORIZED,
            "Invalid token",
            &request_id(&request),
        );
    }
    state.service.relay(request).await
}

#[cfg(test)]
mod tests {
    use std::net::{IpAddr, Ipv4Addr};

    use super::*;

    type TestResult<T = ()> = Result<T, Box<dyn std::error::Error>>;

    fn test_error(context: impl Into<String>) -> Box<dyn std::error::Error> {
        Box::new(std::io::Error::other(context.into()))
    }

    #[test]
    fn extracts_model_without_mutating_multipart_bytes() -> TestResult {
        let body = b"--boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"speech.wav\"\r\n\r\n\x00\xff\r\n--boundary\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nwhisper-1\r\n--boundary--\r\n";
        assert_eq!(
            media_model("/v1/audio/transcriptions", &HeaderMap::new(), body).as_deref(),
            Some("whisper-1")
        );
        assert!(body.windows(2).any(|bytes| bytes == b"\x00\xff"));
        Ok(())
    }

    #[test]
    fn json_model_requires_nonempty_value() -> TestResult {
        let mut headers = HeaderMap::new();
        headers.insert(
            header::CONTENT_TYPE,
            "application/json".parse().map_err(|error| {
                test_error(format!("parse request content-type header: {error}"))
            })?,
        );
        assert_eq!(
            media_model("/v1/audio/speech", &headers, br#"{"model":"tts-1"}"#).as_deref(),
            Some("tts-1")
        );
        assert_eq!(
            media_model("/v1/audio/speech", &headers, br#"{"model":"   "}"#).as_deref(),
            Some("tts-1")
        );
        Ok(())
    }

    #[test]
    fn image_json_defaults_match_legacy_forwarded_payload() -> TestResult {
        let mut headers = HeaderMap::new();
        headers.insert(
            header::CONTENT_TYPE,
            "application/json".parse().map_err(|error| {
                test_error(format!("parse request content-type header: {error}"))
            })?,
        );
        let generations_body = normalize_media_body(
            "/v1/images/generations",
            &headers,
            br#"{"model":"gpt-test","prompt":"hello"}"#.to_vec(),
        )
        .map_err(|error| test_error(format!("normalize generation request body: {error}")))?;
        let generations: serde_json::Value = serde_json::from_slice(&generations_body).map_err(
            |error| test_error(format!("parse normalized generation body JSON: {error}")),
        )?;
        assert_eq!(
            generations,
            serde_json::json!({"model":"gpt-test","prompt":"hello","n":1})
        );
        let edits_body = normalize_media_body(
            "/v1/images/edits",
            &headers,
            br#"{"model":"gpt-test","image":"fixture-image"}"#.to_vec(),
        )
        .map_err(|error| test_error(format!("normalize edit request body: {error}")))?;
        let edits: serde_json::Value = serde_json::from_slice(&edits_body).map_err(|error| {
            test_error(format!("parse normalized edit body JSON: {error}"))
        })?;
        assert_eq!(
            edits,
            serde_json::json!({"model":"gpt-test","image":"fixture-image","n":1,"prompt":""})
        );
        Ok(())
    }

    #[test]
    fn token_ip_policy_fails_closed_when_an_allow_list_is_present() -> TestResult {
        let localhost = IpAddr::V4(Ipv4Addr::LOCALHOST);
        assert!(ip_is_allowed(Some(localhost), "127.0.0.1\n10.0.0.0/8"));
        assert!(!ip_is_allowed(Some(localhost), "10.0.0.0/8"));
        assert!(!ip_is_allowed(None, "127.0.0.1"));
        assert!(ip_is_allowed(None, ""));
        Ok(())
    }

    #[test]
    fn only_loopback_targets_are_test_instance_safe() -> TestResult {
        let ipv4_url = reqwest::Url::parse("http://127.0.0.1:18080/v1/audio/speech")
            .map_err(|error| test_error(format!("parse IPv4 loopback provider URL: {error}")))?;
        assert!(is_loopback_mock_target(&ipv4_url));
        let localhost_url = reqwest::Url::parse("http://localhost:18080/v1/audio/speech")
            .map_err(|error| test_error(format!("parse localhost provider URL: {error}")))?;
        assert!(is_loopback_mock_target(&localhost_url));
        let remote_url = reqwest::Url::parse("https://provider.example/v1/audio/speech")
            .map_err(|error| test_error(format!("parse remote provider URL: {error}")))?;
        assert!(!is_loopback_mock_target(&remote_url));
        Ok(())
    }
}
