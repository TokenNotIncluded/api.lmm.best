//! Legacy-compatible dynamic `/:mode/mj` Midjourney media-task routes.
//!
//! The route layer deliberately keeps persistence, counters, and upstream I/O behind
//! [`MidjourneyBackend`].  [`PgMidjourneyBackend`] is the production adapter;
//! test-only mock backends live beside their contract tests rather than in the
//! deployed route module.

use std::{
    net::{IpAddr, SocketAddr},
    str::FromStr,
    sync::Arc,
    time::{Duration, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Extension, Json, Router,
    body::{Body, Bytes, to_bytes},
    extract::{Path, State},
    http::{HeaderMap, HeaderValue, StatusCode, Uri, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use hmac::{Hmac, Mac};
use serde::Deserialize;
use serde_json::{Value, json};
use sha2::Sha256;
use sqlx::{PgPool, Row};

use crate::RequestContext;

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, rename_all = "camelCase")]
struct MidjourneyRequest {
    prompt: String,
    custom_id: String,
    action: String,
    index: i64,
    state: String,
    task_id: String,
    content: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, rename_all = "camelCase")]
struct SwapFaceRequest {
    source_base64: String,
    target_base64: String,
}

/// Authenticated identity used to scope task reads and durable accounting.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct MidjourneyIdentity {
    /// Stable legacy user id.
    pub user_id: i64,
    /// Non-secret token identifier for usage records.
    pub token_id: String,
}

/// A buffered upstream JSON reply.  Midjourney routes are never SSE relays.
#[derive(Clone, Debug)]
pub struct BufferedJsonReply {
    /// Upstream HTTP status.
    pub status: StatusCode,
    /// Content type returned to the compatibility caller.
    pub content_type: HeaderValue,
    /// Parsed upstream JSON payload.
    pub body: Value,
}

/// A persisted public image reference, looked up exclusively by Midjourney id.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct StoredImage {
    /// URL saved in PostgreSQL by the task lifecycle.
    pub url: String,
}

/// Image-fetch result after URL and resolved-address validation.
///
/// A successful response owns an Axum body backed directly by the upstream
/// byte stream.  Keeping the error form separate makes it impossible for a
/// non-200 provider body to be mistaken for image bytes.
pub enum ImageReply {
    /// A successful upstream image response.
    Stream {
        /// Only this response header is retained by the legacy image proxy.
        content_type: HeaderValue,
        /// Backpressure-aware upstream byte stream.
        body: Body,
    },
    /// A bounded, buffered non-success upstream response.
    Error {
        /// Upstream HTTP status.
        status: StatusCode,
        /// Text body exposed through the legacy JSON error envelope.
        body: String,
    },
}

/// Durable post-submit effect owned by the production PostgreSQL/Valkey adapter.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct TaskEffect {
    /// Selected compatibility mode (for example `mj`).
    pub mode: String,
    /// The concrete submit operation.
    pub operation: String,
    /// Task id returned by the upstream.
    pub task_id: String,
    /// Normalized legacy action recorded in PostgreSQL.
    pub action: String,
    /// Prompt retained for task inspection and child operations.
    pub prompt: String,
    /// Opaque caller state retained with the task.
    pub state: String,
    /// Original provider response code before replay normalization.
    pub code: i64,
    /// Provider response description.
    pub description: String,
    /// Provider response properties used for code-21 completion snapshots.
    pub properties: Value,
    /// Channel that actually received this submission.
    pub channel_id: i64,
    /// Quota charged for this accepted submission.
    pub quota: i64,
}

/// Upstream reply plus the immutable persistence context selected before I/O.
#[derive(Clone, Debug)]
pub struct SubmitReply {
    /// Buffered provider JSON response.
    pub response: BufferedJsonReply,
    /// Exact database/accounting effect to apply if the response is accepted.
    pub effect: TaskEffect,
}

/// Failure categories which retain the legacy JSON error envelope.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum MidjourneyFailure {
    /// Missing, malformed, or rejected user/token credentials.
    Unauthorized,
    /// Authenticated credentials are not allowed from this request origin.
    Forbidden,
    /// A selected upstream could not be reached.
    Upstream,
    /// The upstream returned a body that cannot be parsed as JSON.
    InvalidUpstreamJson,
    /// The requested persisted task does not belong to the caller.
    NotFound,
    /// A required PostgreSQL operation could not complete.
    Storage,
    /// Legacy request validation failure.
    Request(&'static str),
    /// The public image URL or one of its resolved addresses was blocked.
    BlockedImage,
}

/// Boundary for authentication, relational task persistence, accounting, and upstream I/O.
///
/// Implementations must insert/charge only from [`Self::record_submit`] after a 200
/// response whose code is accepted by [`accepts_submit`].  Midjourney task routes do
/// not intentionally create Valkey task keys; Valkey rate-limit effects remain owned
/// by the application-wide auth/distribution layer.
#[async_trait]
pub trait MidjourneyBackend: Send + Sync {
    /// Authenticates a legacy bearer token without retaining the secret.
    async fn authenticate(
        &self,
        headers: &HeaderMap,
        client_ip: Option<IpAddr>,
    ) -> Result<MidjourneyIdentity, MidjourneyFailure>;
    /// Buffers an upstream submit response.
    async fn submit(
        &self,
        identity: &MidjourneyIdentity,
        mode: &str,
        operation: &str,
        headers: &HeaderMap,
        body: Value,
    ) -> Result<SubmitReply, MidjourneyFailure>;
    /// Persists the accepted task and applies the legacy relational accounting transaction.
    async fn record_submit(
        &self,
        identity: &MidjourneyIdentity,
        effect: TaskEffect,
    ) -> Result<(), MidjourneyFailure>;
    /// Proxies a protected task read after authentication and user scoping.
    async fn task_read(
        &self,
        identity: &MidjourneyIdentity,
        operation: &str,
        task_id: &str,
        headers: &HeaderMap,
        body: Option<Value>,
    ) -> Result<BufferedJsonReply, MidjourneyFailure>;
    /// Legacy unscoped image lookup retained for source compatibility only.
    ///
    /// HTTP image routes never call this method. Implementations that expose
    /// image data must implement [`Self::image_for_owned`] instead.
    async fn image_for(&self, task_id: &str) -> Result<StoredImage, MidjourneyFailure>;
    /// Finds a public image reference scoped to the signed URL owner.
    ///
    /// The default is deliberately fail-closed so an adapter cannot expose an
    /// image merely by implementing the legacy unscoped hook.
    async fn image_for_owned(
        &self,
        _user_id: i64,
        _task_id: &str,
    ) -> Result<StoredImage, MidjourneyFailure> {
        Err(MidjourneyFailure::NotFound)
    }
    /// Fetches a previously validated image URL.
    async fn fetch_image(&self, url: &str) -> Result<ImageReply, MidjourneyFailure>;
}

/// Router state for the independently mergeable Midjourney slice.
#[derive(Clone)]
pub struct MidjourneyHttpState {
    backend: Arc<dyn MidjourneyBackend>,
    image_signing_secret: Option<Arc<[u8]>>,
}

impl MidjourneyHttpState {
    /// Creates state from an application-owned backend.
    #[must_use]
    pub fn new(backend: Arc<dyn MidjourneyBackend>) -> Self {
        Self {
            backend,
            image_signing_secret: image_signing_secret_from_env(),
        }
    }

    /// Overrides the deployment-wide image-signing secret for an isolated
    /// listener or deterministic contract test.
    #[must_use]
    pub fn with_image_signing_secret(mut self, secret: impl AsRef<[u8]>) -> Self {
        self.image_signing_secret = image_signing_secret_from_bytes(secret.as_ref());
        self
    }
}

/// Builds the complete standalone Midjourney compatibility router, including
/// the static `/mj` aliases used by callers that do not mount the neighbouring
/// media-task slice.
pub fn media_midjourney_router(state: MidjourneyHttpState) -> Router {
    dynamic_routes().merge(static_routes()).with_state(state)
}

/// Builds only the dynamic `/:mode/mj` aliases.
///
/// The isolated candidate listener also mounts [`crate::migration_routes::media_tasks::media_task_router`]
/// for the static `/mj`, Suno, Kling, and Jimeng families.  Keeping this
/// dynamic-only view separate prevents Axum from rejecting the intentional
/// static `/mj` overlap while preserving the full router used by the normal
/// listener.
pub fn media_midjourney_dynamic_router(state: MidjourneyHttpState) -> Router {
    dynamic_routes().with_state(state)
}

fn dynamic_routes() -> Router<MidjourneyHttpState> {
    Router::new()
        .route("/{mode}/mj/image/{id}", get(image))
        .route("/{mode}/mj/insight-face/swap", post(submit_swap))
        .route("/{mode}/mj/submit/action", post(submit_action))
        .route("/{mode}/mj/submit/blend", post(submit_blend))
        .route("/{mode}/mj/submit/change", post(submit_change))
        .route("/{mode}/mj/submit/describe", post(submit_describe))
        .route("/{mode}/mj/submit/edits", post(submit_edits))
        .route("/{mode}/mj/submit/imagine", post(submit_imagine))
        .route("/{mode}/mj/submit/modal", post(submit_modal))
        .route("/{mode}/mj/submit/shorten", post(submit_shorten))
        .route(
            "/{mode}/mj/submit/simple-change",
            post(submit_simple_change),
        )
        .route(
            "/{mode}/mj/submit/upload-discord-images",
            post(submit_upload_discord_images),
        )
        .route("/{mode}/mj/submit/video", post(submit_video))
        .route("/{mode}/mj/task/{id}/fetch", get(task_fetch))
        .route("/{mode}/mj/task/{id}/image-seed", get(task_image_seed))
        .route(
            "/{mode}/mj/task/list-by-condition",
            post(task_list_by_condition),
        )
}

fn static_routes() -> Router<MidjourneyHttpState> {
    Router::new()
        .route("/mj/image/{id}", get(static_image))
        .route("/mj/insight-face/swap", post(static_submit_swap))
        .route("/mj/submit/action", post(static_submit_action))
        .route("/mj/submit/blend", post(static_submit_blend))
        .route("/mj/submit/change", post(static_submit_change))
        .route("/mj/submit/describe", post(static_submit_describe))
        .route("/mj/submit/edits", post(static_submit_edits))
        .route("/mj/submit/imagine", post(static_submit_imagine))
        .route("/mj/submit/modal", post(static_submit_modal))
        .route("/mj/submit/shorten", post(static_submit_shorten))
        .route(
            "/mj/submit/simple-change",
            post(static_submit_simple_change),
        )
        .route(
            "/mj/submit/upload-discord-images",
            post(static_submit_upload_discord_images),
        )
        .route("/mj/submit/video", post(static_submit_video))
        .route("/mj/task/{id}/fetch", get(static_task_fetch))
        .route("/mj/task/{id}/image-seed", get(static_task_image_seed))
        .route(
            "/mj/task/list-by-condition",
            post(static_task_list_by_condition),
        )
}

macro_rules! submit_handler {
    ($name:ident, $operation:literal) => {
        async fn $name(
            State(state): State<MidjourneyHttpState>,
            Path(mode): Path<String>,
            context: Option<Extension<RequestContext>>,
            headers: HeaderMap,
            body: Bytes,
        ) -> Response {
            let client_ip = request_client_ip(context.as_ref());
            let request_id = request_id(context.as_ref());
            submit(
                state, mode, $operation, client_ip, request_id, headers, body,
            )
            .await
        }
    };
}

macro_rules! static_submit_handler {
    ($name:ident, $operation:literal) => {
        async fn $name(
            State(state): State<MidjourneyHttpState>,
            context: Option<Extension<RequestContext>>,
            headers: HeaderMap,
            body: Bytes,
        ) -> Response {
            let client_ip = request_client_ip(context.as_ref());
            let request_id = request_id(context.as_ref());
            submit(
                state,
                "mj".to_owned(),
                $operation,
                client_ip,
                request_id,
                headers,
                body,
            )
            .await
        }
    };
}

submit_handler!(submit_swap, "insight-face/swap");
submit_handler!(submit_action, "action");
submit_handler!(submit_blend, "blend");
submit_handler!(submit_change, "change");
submit_handler!(submit_describe, "describe");
submit_handler!(submit_edits, "edits");
submit_handler!(submit_imagine, "imagine");
submit_handler!(submit_modal, "modal");
submit_handler!(submit_shorten, "shorten");
submit_handler!(submit_simple_change, "simple-change");
submit_handler!(submit_upload_discord_images, "upload-discord-images");
submit_handler!(submit_video, "video");
static_submit_handler!(static_submit_swap, "insight-face/swap");
static_submit_handler!(static_submit_action, "action");
static_submit_handler!(static_submit_blend, "blend");
static_submit_handler!(static_submit_change, "change");
static_submit_handler!(static_submit_describe, "describe");
static_submit_handler!(static_submit_edits, "edits");
static_submit_handler!(static_submit_imagine, "imagine");
static_submit_handler!(static_submit_modal, "modal");
static_submit_handler!(static_submit_shorten, "shorten");
static_submit_handler!(static_submit_simple_change, "simple-change");
static_submit_handler!(static_submit_upload_discord_images, "upload-discord-images");
static_submit_handler!(static_submit_video, "video");

async fn submit(
    state: MidjourneyHttpState,
    mode: String,
    operation: &str,
    client_ip: Option<IpAddr>,
    request_id: Option<&str>,
    headers: HeaderMap,
    bytes: Bytes,
) -> Response {
    let identity = match state.backend.authenticate(&headers, client_ip).await {
        Ok(identity) => identity,
        Err(error) => return failure_with_request_id(error, request_id),
    };
    let body = match parse_submit_body(operation, &bytes) {
        Ok(body) => body,
        Err(error) => return failure_with_request_id(error, request_id),
    };
    let submitted = match state
        .backend
        .submit(&identity, &mode, operation, &headers, body)
        .await
    {
        Ok(reply) => reply,
        Err(error) => return failure_with_request_id(error, request_id),
    };
    let accepted =
        submitted.response.status == StatusCode::OK && accepts_submit(&submitted.response.body);
    let mut body = submitted.response.body;
    if accepted {
        if let Err(error) = state
            .backend
            .record_submit(&identity, submitted.effect)
            .await
        {
            return failure_with_request_id(error, request_id);
        }
        normalize_replay_code(&mut body);
    }
    json_response(
        submitted.response.status,
        submitted.response.content_type,
        body,
    )
}

async fn task_fetch(
    State(state): State<MidjourneyHttpState>,
    Path((_, id)): Path<(String, String)>,
    context: Option<Extension<RequestContext>>,
    headers: HeaderMap,
) -> Response {
    task_read(state, context.as_ref(), headers, "fetch", &id, None).await
}
async fn static_task_fetch(
    State(state): State<MidjourneyHttpState>,
    Path(id): Path<String>,
    context: Option<Extension<RequestContext>>,
    headers: HeaderMap,
) -> Response {
    task_read(state, context.as_ref(), headers, "fetch", &id, None).await
}
async fn task_image_seed(
    State(state): State<MidjourneyHttpState>,
    Path((_, id)): Path<(String, String)>,
    context: Option<Extension<RequestContext>>,
    headers: HeaderMap,
) -> Response {
    task_read(state, context.as_ref(), headers, "image-seed", &id, None).await
}
async fn static_task_image_seed(
    State(state): State<MidjourneyHttpState>,
    Path(id): Path<String>,
    context: Option<Extension<RequestContext>>,
    headers: HeaderMap,
) -> Response {
    task_read(state, context.as_ref(), headers, "image-seed", &id, None).await
}
async fn task_list_by_condition(
    State(state): State<MidjourneyHttpState>,
    Path((_,)): Path<(String,)>,
    context: Option<Extension<RequestContext>>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    let body = match parse_json_object(&body, "do_request_failed") {
        Ok(body) => body,
        Err(error) => return failure_with_request_id(error, request_id(context.as_ref())),
    };
    task_read(
        state,
        context.as_ref(),
        headers,
        "list-by-condition",
        "",
        Some(body),
    )
    .await
}
async fn static_task_list_by_condition(
    State(state): State<MidjourneyHttpState>,
    context: Option<Extension<RequestContext>>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    let body = match parse_json_object(&body, "do_request_failed") {
        Ok(body) => body,
        Err(error) => return failure_with_request_id(error, request_id(context.as_ref())),
    };
    task_read(
        state,
        context.as_ref(),
        headers,
        "list-by-condition",
        "",
        Some(body),
    )
    .await
}

async fn task_read(
    state: MidjourneyHttpState,
    context: Option<&Extension<RequestContext>>,
    headers: HeaderMap,
    operation: &str,
    id: &str,
    body: Option<Value>,
) -> Response {
    let identity = match state
        .backend
        .authenticate(&headers, request_client_ip(context))
        .await
    {
        Ok(identity) => identity,
        Err(error) => return failure_with_request_id(error, request_id(context)),
    };
    match state
        .backend
        .task_read(&identity, operation, id, &headers, body)
        .await
    {
        Ok(reply) => json_response(reply.status, reply.content_type, reply.body),
        Err(error) => failure_with_request_id(error, request_id(context)),
    }
}

async fn image(
    State(state): State<MidjourneyHttpState>,
    Path((_, id)): Path<(String, String)>,
    uri: Uri,
) -> Response {
    image_response(state, id, uri).await
}

async fn static_image(
    State(state): State<MidjourneyHttpState>,
    Path(id): Path<String>,
    uri: Uri,
) -> Response {
    image_response(state, id, uri).await
}

async fn image_response(state: MidjourneyHttpState, id: String, uri: Uri) -> Response {
    let Some(user_id) = signed_image_owner(&uri, &id, state.image_signing_secret.as_deref()) else {
        return invalid_image_signature_response();
    };
    let stored = match state.backend.image_for_owned(user_id, &id).await {
        Ok(stored) => stored,
        Err(MidjourneyFailure::NotFound) => {
            return (
                StatusCode::NOT_FOUND,
                Json(json!({"error":"midjourney_task_not_found"})),
            )
                .into_response();
        }
        Err(_) => {
            return (
                StatusCode::INTERNAL_SERVER_ERROR,
                Json(json!({"error":"http_get_image_failed"})),
            )
                .into_response();
        }
    };
    if !safe_image_url(&stored.url) {
        return (
            StatusCode::FORBIDDEN,
            Json(json!({"error":"request blocked: unsafe image URL"})),
        )
            .into_response();
    }
    match state.backend.fetch_image(&stored.url).await {
        Ok(ImageReply::Stream { content_type, body }) => {
            let mut response = Response::new(body);
            *response.status_mut() = StatusCode::OK;
            response
                .headers_mut()
                .insert(header::CONTENT_TYPE, content_type);
            response
        }
        Ok(ImageReply::Error { status, body }) => {
            (status, Json(json!({"error":body}))).into_response()
        }
        Err(MidjourneyFailure::BlockedImage) => (
            StatusCode::FORBIDDEN,
            Json(json!({"error":"request blocked: unsafe image URL"})),
        )
            .into_response(),
        Err(_) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            Json(json!({"error":"http_get_image_failed"})),
        )
            .into_response(),
    }
}

fn invalid_image_signature_response() -> Response {
    (
        StatusCode::UNAUTHORIZED,
        Json(json!({"error":"midjourney_image_signature_invalid"})),
    )
        .into_response()
}

type HmacSha256 = Hmac<Sha256>;

pub(crate) fn image_signing_secret_from_env() -> Option<Arc<[u8]>> {
    std::env::var("SESSION_SECRET")
        .ok()
        .and_then(|secret| image_signing_secret_from_bytes(secret.as_bytes()))
}

fn image_signing_secret_from_bytes(secret: &[u8]) -> Option<Arc<[u8]>> {
    (!secret.is_empty()).then(|| Arc::from(secret.to_vec().into_boxed_slice()))
}

/// Returns the owner encoded by a Go-compatible Midjourney image URL.
///
/// Go signs `midjourney-image-v1:<uid>:<task-id>` with `SESSION_SECRET` and
/// hex-encodes the HMAC-SHA256 output. Missing, malformed, or mismatched
/// values return `None` so callers can use one fail-closed response path.
pub(crate) fn signed_image_owner(uri: &Uri, task_id: &str, secret: Option<&[u8]>) -> Option<i64> {
    let secret = secret.filter(|secret| !secret.is_empty())?;
    let mut uid = None;
    let mut signature = None;
    for (key, value) in form_urlencoded::parse(uri.query().unwrap_or_default().as_bytes()) {
        match key.as_ref() {
            "uid" if uid.is_none() => uid = Some(value.into_owned()),
            "sig" if signature.is_none() => signature = Some(value.into_owned()),
            _ => {}
        }
    }
    let user_id = uid?.parse::<i64>().ok().filter(|user_id| *user_id > 0)?;
    if task_id.is_empty() {
        return None;
    }
    let provided = hex::decode(signature?).ok()?;
    let mut mac = HmacSha256::new_from_slice(secret).ok()?;
    mac.update(format!("midjourney-image-v1:{user_id}:{task_id}").as_bytes());
    mac.verify_slice(&provided).ok()?;
    Some(user_id)
}

fn request_client_ip(context: Option<&Extension<RequestContext>>) -> Option<IpAddr> {
    context.and_then(|Extension(context)| context.client_ip)
}

fn request_id(context: Option<&Extension<RequestContext>>) -> Option<&str> {
    context.map(|Extension(context)| context.request_id.as_str())
}

fn parse_json_object(
    bytes: &[u8],
    failure_description: &'static str,
) -> Result<Value, MidjourneyFailure> {
    let body = serde_json::from_slice::<Value>(bytes)
        .map_err(|_| MidjourneyFailure::Request(failure_description))?;
    body.is_object()
        .then_some(body)
        .ok_or(MidjourneyFailure::Request(failure_description))
}

fn parse_submit_body(operation: &str, bytes: &[u8]) -> Result<Value, MidjourneyFailure> {
    let body = parse_json_object(bytes, "bind_request_body_failed")?;
    if operation == "insight-face/swap" {
        let request = serde_json::from_value::<SwapFaceRequest>(body.clone())
            .map_err(|_| MidjourneyFailure::Request("bind_request_body_failed"))?;
        if request.source_base64.is_empty() || request.target_base64.is_empty() {
            return Err(MidjourneyFailure::Request(
                "sour_base64_and_target_base64_is_required",
            ));
        }
        return Ok(body);
    }
    let request = serde_json::from_value::<MidjourneyRequest>(body.clone())
        .map_err(|_| MidjourneyFailure::Request("bind_request_body_failed"))?;
    match operation {
        "imagine" if request.prompt.is_empty() => {
            Err(MidjourneyFailure::Request("prompt_is_required"))
        }
        "action" if request.custom_id.is_empty() => {
            Err(MidjourneyFailure::Request("custom_id_is_required"))
        }
        "action" if plus_action(&request.custom_id).is_none() => {
            Err(MidjourneyFailure::Request("unknown_action"))
        }
        "change" if request.task_id.is_empty() => {
            Err(MidjourneyFailure::Request("task_id_is_required"))
        }
        "change" if request.action.is_empty() => {
            Err(MidjourneyFailure::Request("action_is_required"))
        }
        "change" if request.index == 0 => Err(MidjourneyFailure::Request("index_is_required")),
        "simple-change" if request.content.is_empty() => {
            Err(MidjourneyFailure::Request("content_is_required"))
        }
        "simple-change" if simple_change(&request.content).is_none() => {
            Err(MidjourneyFailure::Request("content_parse_failed"))
        }
        "modal" | "video" if request.task_id.is_empty() => {
            Err(MidjourneyFailure::Request("task_id_is_required"))
        }
        _ => Ok(body),
    }
}
fn accepts_submit(body: &Value) -> bool {
    matches!(body.get("code").and_then(Value::as_i64), Some(1 | 21 | 22))
}
fn normalize_replay_code(body: &mut Value) {
    if matches!(body.get("code").and_then(Value::as_i64), Some(21 | 22)) {
        body["code"] = json!(1);
    }
}
fn json_response(status: StatusCode, content_type: HeaderValue, body: Value) -> Response {
    let mut response = (status, Json(body)).into_response();
    response
        .headers_mut()
        .insert(header::CONTENT_TYPE, content_type);
    response
}
fn failure_with_request_id(error: MidjourneyFailure, request_id: Option<&str>) -> Response {
    let with_request_id = |message: &str| {
        request_id.map_or_else(
            || message.to_owned(),
            |request_id| format!("{message} (request id: {request_id})"),
        )
    };
    match error {
        MidjourneyFailure::Unauthorized => (StatusCode::UNAUTHORIZED, Json(json!({"error":{"message":with_request_id("Invalid token"),"type":"new_api_error","code":""}}))).into_response(),
        MidjourneyFailure::Forbidden => (StatusCode::FORBIDDEN, Json(json!({"error":{"message":"access denied","type":"new_api_error","code":"access_denied"}}))).into_response(),
        MidjourneyFailure::NotFound => (StatusCode::BAD_REQUEST, Json(json!({"code":4,"description":"task_no_found ","type":"upstream_error"}))).into_response(),
        MidjourneyFailure::Upstream => (StatusCode::BAD_REQUEST, Json(json!({"code":5,"description":"do_request_failed ","type":"upstream_error"}))).into_response(),
        MidjourneyFailure::InvalidUpstreamJson => (StatusCode::BAD_REQUEST, Json(json!({"code":5,"description":"unmarshal_response_body_failed ","type":"upstream_error"}))).into_response(),
        MidjourneyFailure::Storage => (StatusCode::INTERNAL_SERVER_ERROR, Json(json!({"code":500,"description":"database_error","type":"server_error"}))).into_response(),
        MidjourneyFailure::Request(description) => (StatusCode::BAD_REQUEST, Json(json!({"code":4,"description":format!("{description} "),"type":"upstream_error"}))).into_response(),
        MidjourneyFailure::BlockedImage => (StatusCode::FORBIDDEN, Json(json!({"error":"request blocked: unsafe image URL"}))).into_response(),
    }
}

fn safe_image_url(value: &str) -> bool {
    let Ok(url) = reqwest::Url::parse(value) else {
        return false;
    };
    if !matches!(url.scheme(), "http" | "https")
        || !url.username().is_empty()
        || url.password().is_some()
    {
        return false;
    }
    let Some(host) = url.host_str() else {
        return false;
    };
    let host = host.trim_matches(['[', ']']);
    if host.eq_ignore_ascii_case("localhost") || host.ends_with(".localhost") {
        return false;
    }
    let port = url.port_or_known_default().unwrap_or_default();
    matches!(port, 80 | 443 | 8080 | 8443) && IpAddr::from_str(host).map_or(true, globally_routable)
}

fn globally_routable(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(ip) => {
            let octets = ip.octets();
            !(ip.is_unspecified()
                || ip.is_loopback()
                || ip.is_private()
                || ip.is_link_local()
                || ip.is_multicast()
                || ip.is_broadcast()
                || ip.is_documentation()
                || octets[0] == 0
                || (octets[0] == 100 && (64..=127).contains(&octets[1]))
                || (octets[0] == 192 && octets[1] == 0 && octets[2] == 0)
                || (octets[0] == 198 && matches!(octets[1], 18 | 19))
                || octets[0] >= 240)
        }
        IpAddr::V6(ip) => {
            if let Some(mapped) = ip.to_ipv4_mapped() {
                return globally_routable(IpAddr::V4(mapped));
            }
            let segments = ip.segments();
            !(ip.is_unspecified()
                || ip.is_loopback()
                || ip.is_multicast()
                || ip.is_unicast_link_local()
                || ip.is_unique_local()
                || (segments[0] == 0x2001 && segments[1] == 0x0db8))
        }
    }
}

fn simple_change(content: &str) -> Option<(String, String, i64)> {
    let mut parts = content.split_whitespace();
    let task_id = parts.next()?;
    let action = parts.next()?.to_ascii_lowercase();
    if parts.next().is_some() || task_id.is_empty() {
        return None;
    }
    if action == "r" {
        return Some((task_id.to_owned(), "REROLL".to_owned(), 1));
    }
    let (prefix, index) = action.split_at_checked(1)?;
    let index = index.parse::<i64>().ok()?;
    if !(1..=4).contains(&index) {
        return None;
    }
    match prefix {
        "u" => Some((task_id.to_owned(), "UPSCALE".to_owned(), index)),
        "v" => Some((task_id.to_owned(), "VARIATION".to_owned(), index)),
        _ => None,
    }
}

fn plus_action(custom_id: &str) -> Option<String> {
    let parts = custom_id.split("::").collect::<Vec<_>>();
    let action = if parts.get(1).is_some_and(|part| *part == "JOB") {
        *parts.get(2)?
    } else {
        *parts.get(1)?
    };
    if action.contains("upsample") {
        Some("UPSCALE".to_owned())
    } else if action == "variation" {
        Some("VARIATION".to_owned())
    } else if action == "low_variation" {
        Some("LOW_VARIATION".to_owned())
    } else if action == "high_variation" {
        Some("HIGH_VARIATION".to_owned())
    } else if action.contains("pan") {
        Some("PAN".to_owned())
    } else if action.contains("reroll") {
        Some("REROLL".to_owned())
    } else if action == "Outpaint" {
        Some("ZOOM".to_owned())
    } else if action == "CustomZoom" {
        Some("CUSTOM_ZOOM".to_owned())
    } else if action == "Inpaint" {
        Some("INPAINT".to_owned())
    } else {
        None
    }
}

fn legacy_token_key(headers: &HeaderMap) -> Option<String> {
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
        .map(str::to_owned)
}

fn ip_is_allowed(client_ip: Option<IpAddr>, raw_limits: &str) -> bool {
    let limits = raw_limits
        .lines()
        .map(|line| line.replace([' ', ','], ""))
        .filter(|line| !line.is_empty())
        .collect::<Vec<_>>();
    if limits.is_empty() {
        return true;
    }
    let Some(client_ip) = client_ip else {
        return false;
    };
    limits.iter().any(|limit| {
        limit
            .parse::<ipnet::IpNet>()
            .is_ok_and(|network| network.contains(&client_ip))
            || limit
                .parse::<IpAddr>()
                .is_ok_and(|address| address == client_ip)
    })
}

/// Concrete connection parameters for the selected Midjourney channel.
///
/// The outer distributor must select this channel from PostgreSQL before
/// constructing the request-scoped adapter.  This keeps channel selection
/// atomic with the rest of the relay pipeline while this module retains the
/// exact provider wire contract.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct MidjourneyChannel {
    /// Legacy channel primary key, used by task/accounting rows.
    pub id: i64,
    /// Provider base URL; only HTTP(S) absolute URLs are accepted.
    pub base_url: String,
    /// Server-side channel credential sent as `mj-api-secret`.
    pub api_key: String,
    /// Quota charged for one accepted provider submission.
    pub quota: i64,
}

/// Runtime Midjourney compatibility switches snapshotted from authoritative options.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct MidjourneySettings {
    /// Preserve `notifyHook` when the legacy notification integration is enabled.
    pub notify_enabled: bool,
    /// Preserve `accountFilter` when account filtering is enabled.
    pub account_filter_enabled: bool,
    /// Remove explicit speed flags from forwarded prompts.
    pub clear_mode_flags: bool,
    /// Require a completed parent task before child actions.
    pub require_successful_parent: bool,
}

impl Default for MidjourneySettings {
    fn default() -> Self {
        Self {
            notify_enabled: false,
            account_filter_enabled: false,
            clear_mode_flags: false,
            require_successful_parent: true,
        }
    }
}

/// Production adapter for Midjourney routes.
///
/// Required constructor inputs are a PostgreSQL 18 pool, a rustls-enabled
/// `reqwest::Client` with a finite whole-request timeout, the selected channel,
/// and the maximum buffered response size.  Midjourney intentionally does not
/// create task-specific Valkey keys: the legacy listener stores task state in
/// PostgreSQL and relies on the outer token/distribution layer for its Valkey
/// cache and concurrency controls.
#[derive(Clone)]
pub struct PgMidjourneyBackend {
    pg: PgPool,
    client: reqwest::Client,
    channel: MidjourneyChannel,
    response_header_timeout: Duration,
    max_response_bytes: usize,
    settings: MidjourneySettings,
}

const TASK_SELECT_BY_ID: &str = "SELECT COALESCE(mj_id,'') AS mj_id,COALESCE(action,'') AS action,COALESCE(prompt,'') AS prompt,COALESCE(prompt_en,'') AS prompt_en,COALESCE(description,'') AS description,COALESCE(state,'') AS state,COALESCE(submit_time,0) AS submit_time,COALESCE(start_time,0) AS start_time,COALESCE(finish_time,0) AS finish_time,COALESCE(image_url,'') AS image_url,COALESCE(video_url,'') AS video_url,COALESCE(video_urls,'') AS video_urls,COALESCE(status,'') AS status,COALESCE(progress,'') AS progress,COALESCE(fail_reason,'') AS fail_reason,COALESCE(buttons,'') AS buttons,COALESCE(properties,'') AS properties FROM midjourneys WHERE user_id=$1 AND mj_id=$2 ORDER BY id LIMIT 1";
const TASK_SELECT_BY_IDS: &str = "SELECT COALESCE(mj_id,'') AS mj_id,COALESCE(action,'') AS action,COALESCE(prompt,'') AS prompt,COALESCE(prompt_en,'') AS prompt_en,COALESCE(description,'') AS description,COALESCE(state,'') AS state,COALESCE(submit_time,0) AS submit_time,COALESCE(start_time,0) AS start_time,COALESCE(finish_time,0) AS finish_time,COALESCE(image_url,'') AS image_url,COALESCE(video_url,'') AS video_url,COALESCE(video_urls,'') AS video_urls,COALESCE(status,'') AS status,COALESCE(progress,'') AS progress,COALESCE(fail_reason,'') AS fail_reason,COALESCE(buttons,'') AS buttons,COALESCE(properties,'') AS properties FROM midjourneys WHERE user_id=$1 AND mj_id=ANY($2) ORDER BY id";

fn task_json(row: &sqlx::postgres::PgRow) -> Result<Value, MidjourneyFailure> {
    fn text(row: &sqlx::postgres::PgRow, name: &str) -> Result<String, MidjourneyFailure> {
        row.try_get(name).map_err(|_| MidjourneyFailure::Storage)
    }
    fn number(row: &sqlx::postgres::PgRow, name: &str) -> Result<i64, MidjourneyFailure> {
        row.try_get(name).map_err(|_| MidjourneyFailure::Storage)
    }
    fn optional_json(raw: String) -> Value {
        if raw.is_empty() {
            Value::Null
        } else {
            serde_json::from_str(&raw).unwrap_or(Value::Null)
        }
    }

    let video_urls = optional_json(text(row, "video_urls")?);
    let buttons = optional_json(text(row, "buttons")?);
    let properties = optional_json(text(row, "properties")?);
    Ok(json!({
        "id": text(row, "mj_id")?,
        "action": text(row, "action")?,
        "customId": "",
        "botType": "",
        "prompt": text(row, "prompt")?,
        "promptEn": text(row, "prompt_en")?,
        "description": text(row, "description")?,
        "state": text(row, "state")?,
        "submitTime": number(row, "submit_time")?,
        "startTime": number(row, "start_time")?,
        "finishTime": number(row, "finish_time")?,
        "imageUrl": text(row, "image_url")?,
        "videoUrl": text(row, "video_url")?,
        "videoUrls": video_urls,
        "status": text(row, "status")?,
        "progress": text(row, "progress")?,
        "failReason": text(row, "fail_reason")?,
        "buttons": buttons,
        "maskBase64": "",
        "properties": properties,
    }))
}

impl PgMidjourneyBackend {
    /// Builds the real database and HTTP adapter used after channel selection.
    #[must_use]
    pub fn new(
        pg: PgPool,
        client: reqwest::Client,
        channel: MidjourneyChannel,
        response_header_timeout: Duration,
        max_response_bytes: usize,
    ) -> Self {
        Self {
            pg,
            client,
            channel,
            response_header_timeout,
            max_response_bytes,
            settings: MidjourneySettings::default(),
        }
    }

    /// Applies the current authoritative Midjourney option snapshot.
    #[must_use]
    pub fn with_settings(mut self, settings: MidjourneySettings) -> Self {
        self.settings = settings;
        self
    }

    fn endpoint(
        channel: &MidjourneyChannel,
        suffix: &str,
    ) -> Result<reqwest::Url, MidjourneyFailure> {
        let base =
            reqwest::Url::parse(&channel.base_url).map_err(|_| MidjourneyFailure::Upstream)?;
        if !matches!(base.scheme(), "http" | "https") || base.host_str().is_none() {
            return Err(MidjourneyFailure::Upstream);
        }
        base.join(suffix.trim_start_matches('/'))
            .map_err(|_| MidjourneyFailure::Upstream)
    }

    async fn upstream_json(
        &self,
        channel: &MidjourneyChannel,
        method: reqwest::Method,
        suffix: &str,
        headers: &HeaderMap,
        body: Option<Value>,
    ) -> Result<BufferedJsonReply, MidjourneyFailure> {
        let mut request = self
            .client
            .request(method, Self::endpoint(channel, suffix)?);
        for name in [header::CONTENT_TYPE, header::ACCEPT] {
            if let Some(value) = headers.get(&name) {
                request = request.header(name, value);
            }
        }
        request = request.header("mj-api-secret", &channel.api_key);
        if let Some(body) = body {
            request = request.json(&body);
        }
        let response = tokio::time::timeout(self.response_header_timeout, request.send())
            .await
            .map_err(|_| MidjourneyFailure::Upstream)?
            .map_err(|_| MidjourneyFailure::Upstream)?;
        let status = response.status();
        let content_type = response
            .headers()
            .get(header::CONTENT_TYPE)
            .cloned()
            .unwrap_or_else(|| HeaderValue::from_static("application/json; charset=utf-8"));
        let bytes = tokio::time::timeout(
            self.response_header_timeout,
            to_bytes(
                Body::from_stream(response.bytes_stream()),
                self.max_response_bytes,
            ),
        )
        .await
        .map_err(|_| MidjourneyFailure::Upstream)?
        .map_err(|_| MidjourneyFailure::Upstream)?;
        let body =
            serde_json::from_slice(&bytes).map_err(|_| MidjourneyFailure::InvalidUpstreamJson)?;
        Ok(BufferedJsonReply {
            status,
            content_type,
            body,
        })
    }

    async fn channel_by_id(&self, channel_id: i64) -> Result<MidjourneyChannel, MidjourneyFailure> {
        let row = sqlx::query("SELECT id,COALESCE(base_url,'') AS base_url,key,COALESCE(status,1) AS status FROM channels WHERE id=$1")
            .bind(channel_id)
            .fetch_optional(&self.pg)
            .await
            .map_err(|_| MidjourneyFailure::Storage)?
            .ok_or(MidjourneyFailure::Request("get_channel_info_failed"))?;
        if row
            .try_get::<i64, _>("status")
            .map_err(|_| MidjourneyFailure::Storage)?
            != 1
        {
            return Err(MidjourneyFailure::Request("该任务所属渠道已被禁用"));
        }
        Ok(MidjourneyChannel {
            id: row.try_get("id").map_err(|_| MidjourneyFailure::Storage)?,
            base_url: row
                .try_get("base_url")
                .map_err(|_| MidjourneyFailure::Storage)?,
            api_key: row.try_get("key").map_err(|_| MidjourneyFailure::Storage)?,
            quota: self.channel.quota,
        })
    }

    async fn protected_image_get(
        &self,
        initial_url: &str,
    ) -> Result<reqwest::Response, MidjourneyFailure> {
        const MAX_REDIRECTS: usize = 5;
        let mut url =
            reqwest::Url::parse(initial_url).map_err(|_| MidjourneyFailure::BlockedImage)?;
        for redirect_count in 0..=MAX_REDIRECTS {
            if !safe_image_url(url.as_str()) {
                return Err(MidjourneyFailure::BlockedImage);
            }
            let host = url.host_str().ok_or(MidjourneyFailure::BlockedImage)?;
            let port = url
                .port_or_known_default()
                .ok_or(MidjourneyFailure::BlockedImage)?;
            let addresses = tokio::net::lookup_host((host, port))
                .await
                .map_err(|_| MidjourneyFailure::Upstream)?
                .collect::<Vec<SocketAddr>>();
            if addresses.is_empty()
                || addresses
                    .iter()
                    .any(|address| !globally_routable(address.ip()))
            {
                return Err(MidjourneyFailure::BlockedImage);
            }
            let client = reqwest::Client::builder()
                .no_proxy()
                .redirect(reqwest::redirect::Policy::none())
                .connect_timeout(self.response_header_timeout)
                .resolve_to_addrs(host, &addresses)
                .build()
                .map_err(|_| MidjourneyFailure::Upstream)?;
            let response =
                tokio::time::timeout(self.response_header_timeout, client.get(url.clone()).send())
                    .await
                    .map_err(|_| MidjourneyFailure::Upstream)?
                    .map_err(|_| MidjourneyFailure::Upstream)?;
            if !response.status().is_redirection() {
                return Ok(response);
            }
            if redirect_count == MAX_REDIRECTS {
                return Err(MidjourneyFailure::Upstream);
            }
            let location = response
                .headers()
                .get(header::LOCATION)
                .and_then(|value| value.to_str().ok())
                .ok_or(MidjourneyFailure::Upstream)?;
            url = url
                .join(location)
                .map_err(|_| MidjourneyFailure::BlockedImage)?;
        }
        Err(MidjourneyFailure::Upstream)
    }

    fn sanitize_body(&self, body: &mut Value) {
        let Some(object) = body.as_object_mut() else {
            return;
        };
        if !self.settings.account_filter_enabled {
            object.remove("accountFilter");
        }
        if !self.settings.notify_enabled {
            object.remove("notifyHook");
        }
        if self.settings.clear_mode_flags
            && let Some(prompt) = object.get("prompt").and_then(Value::as_str) {
                let prompt = prompt
                    .replace("--fast", "")
                    .replace("--relax", "")
                    .replace("--turbo", "");
                object.insert("prompt".to_owned(), Value::String(prompt));
            }
    }
}

/// Selects a production Midjourney channel before delegating to the
/// request-scoped PostgreSQL/upstream adapter.
///
/// `PgMidjourneyBackend` intentionally represents one selected channel so it
/// can preserve channel affinity for child actions.  The legacy HTTP listener
/// still needs an outer distributor for first-time submissions; this adapter
/// owns that missing step and keeps the selected channel secret server-side.
#[derive(Clone)]
pub struct PgMidjourneyDispatchBackend {
    pg: PgPool,
    client: reqwest::Client,
    response_header_timeout: Duration,
    max_response_bytes: usize,
    settings: MidjourneySettings,
}

impl PgMidjourneyDispatchBackend {
    /// Builds a PostgreSQL-backed channel distributor.
    #[must_use]
    pub fn new(
        pg: PgPool,
        client: reqwest::Client,
        response_header_timeout: Duration,
        max_response_bytes: usize,
    ) -> Self {
        Self {
            pg,
            client,
            response_header_timeout,
            max_response_bytes,
            settings: MidjourneySettings::default(),
        }
    }

    /// Applies the authoritative Midjourney option snapshot to every
    /// request-scoped backend created by this distributor.
    #[must_use]
    pub fn with_settings(mut self, settings: MidjourneySettings) -> Self {
        self.settings = settings;
        self
    }

    fn backend_for_channel(&self, channel: MidjourneyChannel) -> PgMidjourneyBackend {
        PgMidjourneyBackend::new(
            self.pg.clone(),
            self.client.clone(),
            channel,
            self.response_header_timeout,
            self.max_response_bytes,
        )
        .with_settings(self.settings.clone())
    }

    fn authentication_backend(&self) -> PgMidjourneyBackend {
        self.backend_for_channel(MidjourneyChannel {
            id: 0,
            base_url: "http://127.0.0.1:9/".to_owned(),
            api_key: String::new(),
            quota: 0,
        })
    }

    async fn select_channel(
        &self,
        identity: &MidjourneyIdentity,
        operation: &str,
        body: &Value,
    ) -> Result<MidjourneyChannel, MidjourneyFailure> {
        let token_id = identity
            .token_id
            .parse::<i64>()
            .map_err(|_| MidjourneyFailure::Storage)?;
        let request = serde_json::from_value::<MidjourneyRequest>(body.clone()).unwrap_or_default();
        let (action, _) = submit_action_and_parent(operation, &request);
        let model = format!(
            "{}{}",
            if action == "SWAP_FACE" {
                "swap_face"
            } else {
                "mj_"
            },
            if action == "SWAP_FACE" {
                String::new()
            } else {
                action.to_ascii_lowercase()
            }
        );
        let model = if action == "SWAP_FACE" {
            "swap_face".to_owned()
        } else {
            model
        };
        let row = sqlx::query(
            r#"SELECT c.id,
                      COALESCE(c.base_url,'') AS base_url,
                      c.key AS channel_key,
                      COALESCE(t."group",'') AS token_group,
                      COALESCE(u."group",'default') AS user_group
                 FROM tokens t
                 JOIN users u ON u.id=t.user_id
                 JOIN abilities a
                   ON a."group"=COALESCE(NULLIF(t."group",''),u."group")
                  AND a.model=$2
                  AND COALESCE(a.enabled,TRUE)
                 JOIN channels c ON c.id=a.channel_id
                WHERE t.id=$1
                  AND t.deleted_at IS NULL
                  AND u.deleted_at IS NULL
                  AND COALESCE(t.status,1)=1
                  AND COALESCE(u.status,1)=1
                  AND COALESCE(c.status,1)=1
                  AND COALESCE(c.type,0) IN (2,5)
                ORDER BY COALESCE(a.priority,0) DESC,
                         COALESCE(a.weight,0) DESC,
                         c.id ASC
                LIMIT 1"#,
        )
        .bind(token_id)
        .bind(&model)
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| MidjourneyFailure::Storage)?
        .ok_or(MidjourneyFailure::Request("get_channel_info_failed"))?;
        let base_url = row
            .try_get::<String, _>("base_url")
            .map_err(|_| MidjourneyFailure::Storage)?;
        let api_key = row
            .try_get::<String, _>("channel_key")
            .map_err(|_| MidjourneyFailure::Storage)?;
        if base_url.trim().is_empty() || api_key.trim().is_empty() {
            return Err(MidjourneyFailure::Request("get_channel_info_failed"));
        }
        let token_group = row
            .try_get::<String, _>("token_group")
            .map_err(|_| MidjourneyFailure::Storage)?;
        let user_group = row
            .try_get::<String, _>("user_group")
            .map_err(|_| MidjourneyFailure::Storage)?;
        let quota = self.model_quota(&model, &token_group, &user_group).await?;
        Ok(MidjourneyChannel {
            id: row.try_get("id").map_err(|_| MidjourneyFailure::Storage)?,
            base_url,
            api_key,
            quota,
        })
    }

    async fn model_quota(
        &self,
        model: &str,
        token_group: &str,
        user_group: &str,
    ) -> Result<i64, MidjourneyFailure> {
        let rows = sqlx::query(
            "SELECT key, value FROM options WHERE key IN ('ModelPrice','ModelRatio','GroupRatio','QuotaPerUnit')",
        )
        .fetch_all(&self.pg)
        .await
        .map_err(|_| MidjourneyFailure::Storage)?;
        let mut values = std::collections::BTreeMap::<String, String>::new();
        for row in rows {
            values.insert(
                row.try_get::<String, _>("key")
                    .map_err(|_| MidjourneyFailure::Storage)?,
                row.try_get::<String, _>("value")
                    .map_err(|_| MidjourneyFailure::Storage)?,
            );
        }
        let object = |key: &str| {
            values
                .get(key)
                .and_then(|raw| serde_json::from_str::<Value>(raw).ok())
                .and_then(|value| value.as_object().cloned())
                .unwrap_or_default()
        };
        let number = |value: Option<&Value>| {
            value.and_then(Value::as_f64).or_else(|| {
                value
                    .and_then(Value::as_str)
                    .and_then(|raw| raw.parse().ok())
            })
        };
        let model_price = number(object("ModelPrice").get(model));
        let quota_per_unit = values
            .get("QuotaPerUnit")
            .and_then(|value| value.parse::<f64>().ok())
            .unwrap_or(500_000.0);
        let selected_group = if !token_group.trim().is_empty() {
            token_group.trim()
        } else if !user_group.trim().is_empty() {
            user_group.trim()
        } else {
            "default"
        };
        let group_ratio = number(object("GroupRatio").get(selected_group)).unwrap_or(1.0);
        let raw = if let Some(price) = model_price {
            price * quota_per_unit * group_ratio
        } else if let Some(ratio) = number(object("ModelRatio").get(model)) {
            ratio / 2.0 * quota_per_unit * group_ratio
        } else {
            return Err(MidjourneyFailure::Request("model_price_not_configured"));
        };
        if !raw.is_finite() || raw < 0.0 || raw >= i64::MAX as f64 {
            return Err(MidjourneyFailure::Request("model_price_not_configured"));
        }
        Ok(raw.trunc() as i64)
    }
}

fn submit_action_and_parent(
    operation: &str,
    request: &MidjourneyRequest,
) -> (String, Option<String>) {
    let simple = simple_change(&request.content);
    match operation {
        "insight-face/swap" => ("SWAP_FACE".to_owned(), None),
        "upload-discord-images" => ("UPLOAD".to_owned(), None),
        "simple-change" => simple.as_ref().map_or(
            ("SIMPLE_CHANGE".to_owned(), None),
            |(task_id, action, _)| (action.clone(), Some(task_id.clone())),
        ),
        "action" => (
            plus_action(&request.custom_id).unwrap_or_else(|| "ACTION".to_owned()),
            (!request.task_id.is_empty()).then(|| request.task_id.clone()),
        ),
        "change" => (
            request.action.clone(),
            (!request.task_id.is_empty()).then(|| request.task_id.clone()),
        ),
        "modal" => ("MODAL".to_owned(), Some(request.task_id.clone())),
        "video" => ("VIDEO".to_owned(), Some(request.task_id.clone())),
        _ => (operation.replace('-', "_").to_ascii_uppercase(), None),
    }
}

#[async_trait]
impl MidjourneyBackend for PgMidjourneyBackend {
    async fn authenticate(
        &self,
        headers: &HeaderMap,
        client_ip: Option<IpAddr>,
    ) -> Result<MidjourneyIdentity, MidjourneyFailure> {
        let token = legacy_token_key(headers).ok_or(MidjourneyFailure::Unauthorized)?;
        let now = epoch_seconds();
        let row = sqlx::query("SELECT t.id AS token_id,t.user_id,COALESCE(t.status,1) AS token_status,COALESCE(t.expired_time,-1) AS expired_time,COALESCE(t.remain_quota,0) AS remain_quota,COALESCE(t.unlimited_quota,FALSE) AS unlimited_quota,COALESCE(t.allow_ips,'') AS allow_ips,COALESCE(u.status,1) AS user_status FROM tokens t JOIN users u ON u.id=t.user_id WHERE t.key=$1 AND t.deleted_at IS NULL AND u.deleted_at IS NULL")
            .bind(&token)
            .fetch_optional(&self.pg)
            .await
            .map_err(|_| MidjourneyFailure::Storage)?
            .ok_or(MidjourneyFailure::Unauthorized)?;
        let token_status = row
            .try_get::<i64, _>("token_status")
            .map_err(|_| MidjourneyFailure::Storage)?;
        let expired_time = row
            .try_get::<i64, _>("expired_time")
            .map_err(|_| MidjourneyFailure::Storage)?;
        let remain_quota = row
            .try_get::<i64, _>("remain_quota")
            .map_err(|_| MidjourneyFailure::Storage)?;
        let unlimited_quota = row
            .try_get::<bool, _>("unlimited_quota")
            .map_err(|_| MidjourneyFailure::Storage)?;
        if token_status != 1
            || (expired_time != -1 && expired_time < now)
            || (!unlimited_quota && remain_quota <= 0)
        {
            return Err(MidjourneyFailure::Unauthorized);
        }
        let user_status = row
            .try_get::<i64, _>("user_status")
            .map_err(|_| MidjourneyFailure::Storage)?;
        if user_status != 1 {
            return Err(MidjourneyFailure::Forbidden);
        }
        let allow_ips = row
            .try_get::<String, _>("allow_ips")
            .map_err(|_| MidjourneyFailure::Storage)?;
        if !ip_is_allowed(client_ip, &allow_ips) {
            return Err(MidjourneyFailure::Forbidden);
        }
        Ok(MidjourneyIdentity {
            user_id: row
                .try_get("user_id")
                .map_err(|_| MidjourneyFailure::Storage)?,
            token_id: row
                .try_get::<i64, _>("token_id")
                .map_err(|_| MidjourneyFailure::Storage)?
                .to_string(),
        })
    }

    async fn submit(
        &self,
        identity: &MidjourneyIdentity,
        mode: &str,
        operation: &str,
        headers: &HeaderMap,
        mut body: Value,
    ) -> Result<SubmitReply, MidjourneyFailure> {
        let request = serde_json::from_value::<MidjourneyRequest>(body.clone()).unwrap_or_default();
        let (action, parent_task_id) = submit_action_and_parent(operation, &request);
        let mut channel = self.channel.clone();
        let mut prompt = request.prompt.clone();
        if let Some(parent_task_id) = parent_task_id {
            let row = sqlx::query("SELECT COALESCE(prompt,'') AS prompt,COALESCE(status,'') AS status,channel_id FROM midjourneys WHERE user_id=$1 AND mj_id=$2 ORDER BY id LIMIT 1")
                .bind(identity.user_id)
                .bind(parent_task_id)
                .fetch_optional(&self.pg)
                .await
                .map_err(|_| MidjourneyFailure::Storage)?
                .ok_or(MidjourneyFailure::Request("task_not_found"))?;
            let parent_status = row
                .try_get::<String, _>("status")
                .map_err(|_| MidjourneyFailure::Storage)?;
            if self.settings.require_successful_parent
                && parent_status != "SUCCESS"
                && operation != "modal"
            {
                return Err(MidjourneyFailure::Request("task_status_not_success"));
            }
            prompt = row
                .try_get("prompt")
                .map_err(|_| MidjourneyFailure::Storage)?;
            let channel_id = row
                .try_get("channel_id")
                .map_err(|_| MidjourneyFailure::Storage)?;
            channel = self.channel_by_id(channel_id).await?;
        }
        let quota = if matches!(action.as_str(), "INPAINT" | "CUSTOM_ZOOM") {
            0
        } else {
            channel.quota.max(0)
        };
        if quota > 0 {
            let user_quota = sqlx::query_scalar::<_, i64>(
                "SELECT COALESCE(quota,0) FROM users WHERE id=$1 AND deleted_at IS NULL",
            )
            .bind(identity.user_id)
            .fetch_optional(&self.pg)
            .await
            .map_err(|_| MidjourneyFailure::Storage)?
            .ok_or(MidjourneyFailure::Storage)?;
            if user_quota < quota {
                return Err(MidjourneyFailure::Request("quota_not_enough"));
            }
        }
        self.sanitize_body(&mut body);
        let suffix = if operation == "insight-face/swap" {
            "mj/insight-face/swap".to_owned()
        } else {
            format!("mj/submit/{operation}")
        };
        let response = self
            .upstream_json(
                &channel,
                reqwest::Method::POST,
                &suffix,
                headers,
                Some(body),
            )
            .await?;
        let provider_code = response
            .body
            .get("code")
            .and_then(Value::as_i64)
            .unwrap_or_default();
        let properties = response
            .body
            .get("properties")
            .cloned()
            .unwrap_or(Value::Null);
        Ok(SubmitReply {
            effect: TaskEffect {
                mode: mode.to_owned(),
                operation: operation.to_owned(),
                task_id: response
                    .body
                    .get("result")
                    .and_then(Value::as_str)
                    .unwrap_or_default()
                    .to_owned(),
                action,
                prompt: if operation == "insight-face/swap" {
                    "InsightFace".to_owned()
                } else {
                    prompt
                },
                state: if operation == "insight-face/swap" {
                    String::new()
                } else {
                    request.state
                },
                code: provider_code,
                description: response
                    .body
                    .get("description")
                    .and_then(Value::as_str)
                    .unwrap_or_default()
                    .to_owned(),
                properties,
                channel_id: channel.id,
                quota,
            },
            response,
        })
    }

    async fn record_submit(
        &self,
        identity: &MidjourneyIdentity,
        effect: TaskEffect,
    ) -> Result<(), MidjourneyFailure> {
        let now = epoch_millis();
        let quota = effect.quota.max(0);
        let (status, progress, image_url, start_time, finish_time) = if effect.action == "SWAP_FACE"
        {
            (String::new(), "0%".to_owned(), String::new(), now, 0)
        } else if effect.code == 21 {
            let status = effect
                .properties
                .get("status")
                .and_then(Value::as_str)
                .unwrap_or_default()
                .to_owned();
            let image_url = effect
                .properties
                .get("imageUrl")
                .and_then(Value::as_str)
                .unwrap_or_default()
                .to_owned();
            if status == "SUCCESS" {
                (status, "100%".to_owned(), image_url, now, now)
            } else {
                (status, "0%".to_owned(), image_url, 0, 0)
            }
        } else if effect.code == 1 && effect.action == "UPLOAD" {
            ("SUCCESS".to_owned(), "100%".to_owned(), String::new(), 0, 0)
        } else {
            (String::new(), "0%".to_owned(), String::new(), 0, 0)
        };
        let stored_code = if effect.code == 21 && status == "SUCCESS" {
            1
        } else {
            effect.code
        };
        let mut transaction = self
            .pg
            .begin()
            .await
            .map_err(|_| MidjourneyFailure::Storage)?;
        sqlx::query("INSERT INTO midjourneys (code,user_id,action,mj_id,prompt,prompt_en,description,state,submit_time,start_time,finish_time,image_url,video_url,video_urls,status,progress,fail_reason,channel_id,quota,buttons,properties) VALUES ($1,$2,$3,$4,$5,'',$6,$7,$8,$9,$10,$11,'','',$12,$13,'',$14,$15,'','')")
            .bind(stored_code)
            .bind(identity.user_id)
            .bind(&effect.action)
            .bind(&effect.task_id)
            .bind(&effect.prompt)
            .bind(&effect.description)
            .bind(&effect.state)
            .bind(now)
            .bind(start_time)
            .bind(finish_time)
            .bind(image_url)
            .bind(status)
            .bind(progress)
            .bind(effect.channel_id)
            .bind(quota)
            .execute(&mut *transaction)
            .await
            .map_err(|_| MidjourneyFailure::Storage)?;
        sqlx::query("UPDATE users SET quota=COALESCE(quota,0)-$2,used_quota=COALESCE(used_quota,0)+$2,request_count=COALESCE(request_count,0)+1 WHERE id=$1")
            .bind(identity.user_id)
            .bind(quota)
            .execute(&mut *transaction)
            .await
            .map_err(|_| MidjourneyFailure::Storage)?;
        sqlx::query("UPDATE tokens SET accessed_time=$2,used_quota=COALESCE(used_quota,0)+$3,remain_quota=CASE WHEN COALESCE(unlimited_quota,FALSE) THEN remain_quota ELSE COALESCE(remain_quota,0)-$3 END WHERE id=$1 AND user_id=$4")
            .bind(
                identity
                    .token_id
                    .parse::<i64>()
                    .map_err(|_| MidjourneyFailure::Storage)?,
            )
            .bind(now / 1000)
            .bind(quota)
            .bind(identity.user_id)
            .execute(&mut *transaction)
            .await
            .map_err(|_| MidjourneyFailure::Storage)?;
        sqlx::query("UPDATE channels SET used_quota=COALESCE(used_quota,0)+$2 WHERE id=$1")
            .bind(effect.channel_id)
            .bind(quota)
            .execute(&mut *transaction)
            .await
            .map_err(|_| MidjourneyFailure::Storage)?;
        let model_name = if effect.action == "SWAP_FACE" {
            "swap_face".to_owned()
        } else {
            format!("mj_{}", effect.action.to_ascii_lowercase())
        };
        sqlx::query("INSERT INTO logs (user_id,created_at,type,content,model_name,quota,channel_id,token_id,\"group\") VALUES ($1,$2,2,$3,$4,$5,$6,$7,'')")
            .bind(identity.user_id)
            .bind(now / 1000)
            .bind(format!("操作 {}，ID {}", effect.action, effect.task_id))
            .bind(model_name)
            .bind(quota)
            .bind(effect.channel_id)
            .bind(
                identity
                    .token_id
                    .parse::<i64>()
                    .map_err(|_| MidjourneyFailure::Storage)?,
            )
            .execute(&mut *transaction)
            .await
            .map_err(|_| MidjourneyFailure::Storage)?;
        transaction
            .commit()
            .await
            .map_err(|_| MidjourneyFailure::Storage)
    }

    async fn task_read(
        &self,
        identity: &MidjourneyIdentity,
        operation: &str,
        task_id: &str,
        headers: &HeaderMap,
        body: Option<Value>,
    ) -> Result<BufferedJsonReply, MidjourneyFailure> {
        let content_type = HeaderValue::from_static("application/json");
        if operation == "fetch" {
            let row = sqlx::query(TASK_SELECT_BY_ID)
                .bind(identity.user_id)
                .bind(task_id)
                .fetch_optional(&self.pg)
                .await
                .map_err(|_| MidjourneyFailure::Storage)?
                .ok_or(MidjourneyFailure::NotFound)?;
            return Ok(BufferedJsonReply {
                status: StatusCode::OK,
                content_type,
                body: task_json(&row)?,
            });
        }
        if operation == "list-by-condition" {
            let ids = body
                .as_ref()
                .and_then(|value| value.get("ids"))
                .and_then(Value::as_array)
                .ok_or(MidjourneyFailure::Request("do_request_failed"))?
                .iter()
                .map(Value::as_str)
                .collect::<Option<Vec<_>>>()
                .ok_or(MidjourneyFailure::Request("do_request_failed"))?
                .into_iter()
                .map(str::to_owned)
                .collect::<Vec<_>>();
            let rows = if ids.is_empty() {
                Vec::new()
            } else {
                sqlx::query(TASK_SELECT_BY_IDS)
                    .bind(identity.user_id)
                    .bind(&ids)
                    .fetch_all(&self.pg)
                    .await
                    .map_err(|_| MidjourneyFailure::Storage)?
            };
            let tasks = rows.iter().map(task_json).collect::<Result<Vec<_>, _>>()?;
            return Ok(BufferedJsonReply {
                status: StatusCode::OK,
                content_type,
                body: Value::Array(tasks),
            });
        }
        if operation != "image-seed" {
            return Err(MidjourneyFailure::NotFound);
        }
        let channel_id = sqlx::query_scalar::<_, i64>(
            "SELECT channel_id FROM midjourneys WHERE user_id=$1 AND mj_id=$2 ORDER BY id LIMIT 1",
        )
        .bind(identity.user_id)
        .bind(task_id)
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| MidjourneyFailure::Storage)?
        .ok_or(MidjourneyFailure::NotFound)?;
        let channel = self.channel_by_id(channel_id).await?;
        self.upstream_json(
            &channel,
            reqwest::Method::GET,
            &format!("mj/task/{task_id}/image-seed"),
            headers,
            None,
        )
        .await
    }

    async fn image_for(&self, _task_id: &str) -> Result<StoredImage, MidjourneyFailure> {
        Err(MidjourneyFailure::NotFound)
    }

    async fn image_for_owned(
        &self,
        user_id: i64,
        task_id: &str,
    ) -> Result<StoredImage, MidjourneyFailure> {
        let url = sqlx::query_scalar::<_, String>(
            "SELECT image_url FROM midjourneys WHERE user_id=$1 AND mj_id=$2 AND COALESCE(image_url,'')<>'' ORDER BY id DESC LIMIT 1",
        )
        .bind(user_id)
        .bind(task_id)
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| MidjourneyFailure::Storage)?
        .ok_or(MidjourneyFailure::NotFound)?;
        Ok(StoredImage { url })
    }

    async fn fetch_image(&self, url: &str) -> Result<ImageReply, MidjourneyFailure> {
        let response = self.protected_image_get(url).await?;
        let status = response.status();
        if status == StatusCode::OK {
            let content_type = response
                .headers()
                .get(header::CONTENT_TYPE)
                .cloned()
                .unwrap_or_else(|| HeaderValue::from_static("image/jpeg"));
            return Ok(ImageReply::Stream {
                content_type,
                body: Body::from_stream(response.bytes_stream()),
            });
        }
        let bytes = tokio::time::timeout(
            self.response_header_timeout,
            to_bytes(
                Body::from_stream(response.bytes_stream()),
                self.max_response_bytes,
            ),
        )
        .await
        .map_err(|_| MidjourneyFailure::Upstream)?
        .map_err(|_| MidjourneyFailure::Upstream)?;
        Ok(ImageReply::Error {
            status,
            body: String::from_utf8_lossy(&bytes).into_owned(),
        })
    }
}

#[async_trait]
impl MidjourneyBackend for PgMidjourneyDispatchBackend {
    async fn authenticate(
        &self,
        headers: &HeaderMap,
        client_ip: Option<IpAddr>,
    ) -> Result<MidjourneyIdentity, MidjourneyFailure> {
        self.authentication_backend()
            .authenticate(headers, client_ip)
            .await
    }

    async fn submit(
        &self,
        identity: &MidjourneyIdentity,
        mode: &str,
        operation: &str,
        headers: &HeaderMap,
        body: Value,
    ) -> Result<SubmitReply, MidjourneyFailure> {
        let channel = self.select_channel(identity, operation, &body).await?;
        self.backend_for_channel(channel)
            .submit(identity, mode, operation, headers, body)
            .await
    }

    async fn record_submit(
        &self,
        identity: &MidjourneyIdentity,
        effect: TaskEffect,
    ) -> Result<(), MidjourneyFailure> {
        self.authentication_backend()
            .record_submit(identity, effect)
            .await
    }

    async fn task_read(
        &self,
        identity: &MidjourneyIdentity,
        operation: &str,
        task_id: &str,
        headers: &HeaderMap,
        body: Option<Value>,
    ) -> Result<BufferedJsonReply, MidjourneyFailure> {
        self.authentication_backend()
            .task_read(identity, operation, task_id, headers, body)
            .await
    }

    async fn image_for(&self, task_id: &str) -> Result<StoredImage, MidjourneyFailure> {
        self.authentication_backend().image_for(task_id).await
    }

    async fn image_for_owned(
        &self,
        user_id: i64,
        task_id: &str,
    ) -> Result<StoredImage, MidjourneyFailure> {
        self.authentication_backend()
            .image_for_owned(user_id, task_id)
            .await
    }

    async fn fetch_image(&self, url: &str) -> Result<ImageReply, MidjourneyFailure> {
        self.authentication_backend().fetch_image(url).await
    }
}

fn epoch_seconds() -> i64 {
    UNIX_EPOCH
        .elapsed()
        .map_or(0, |elapsed| elapsed.as_secs() as i64)
}

fn epoch_millis() -> i64 {
    UNIX_EPOCH
        .elapsed()
        .map_or(0, |elapsed| elapsed.as_millis() as i64)
}
