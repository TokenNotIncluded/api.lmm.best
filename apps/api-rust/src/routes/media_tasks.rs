//! Legacy-compatible Midjourney task routes.
//!
//! This is intentionally a thin HTTP boundary.  Authentication, channel
//! selection, SSRF validation, PostgreSQL accounting and upstream I/O belong
//! to [`MediaTaskService`], where they can be kept atomic and tested against
//! disposable infrastructure.  The image proxy is deliberately separate: it
//! is a public route in the legacy listener, whereas every task route is
//! authenticated.

use std::sync::Arc;

use async_trait::async_trait;
use axum::{
    Router,
    body::Body,
    extract::{Path, Request, State},
    http::{StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use serde_json::{Value, json};

use crate::{
    RequestContext,
    routes::media_midjourney::{
        ImageReply, MidjourneyBackend, MidjourneyFailure, PgMidjourneyBackend,
        image_signing_secret_from_env, signed_image_owner,
    },
};

/// A protected Midjourney task operation selected by the HTTP router.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum MediaTaskOperation {
    /// Submit a named Midjourney operation.
    Submit(&'static str),
    /// Fetch one task owned by the authenticated user.
    Fetch,
    /// Fetch the image seed for one task owned by the authenticated user.
    ImageSeed,
    /// List the authenticated user's tasks matching a JSON condition.
    ListByCondition,
    /// Submit a Suno action.  The action remains in the raw path so extensions
    /// added by the legacy provider do not require a Rust enum release.
    SunoSubmit,
    /// Fetch Suno tasks from the POST compatibility endpoint.
    SunoFetch,
    /// Fetch one Suno task by its provider id.
    SunoFetchById,
    /// Submit a Kling image-to-video task.
    KlingImageToVideo,
    /// Read a Kling image-to-video task.
    KlingImageToVideoFetch,
    /// Submit a Kling text-to-video task.
    KlingTextToVideo,
    /// Read a Kling text-to-video task.
    KlingTextToVideoFetch,
    /// Submit a Jimeng task.
    JimengSubmit,
}

/// Boundary to the legacy task-relay implementation.
///
/// The router rejects a missing or malformed bearer credential before this
/// boundary is called. Implementations must validate that bearer credential
/// and distribute **before** any protected operation.  For submissions, they must remove disabled `accountFilter` and
/// `notifyHook` fields before upstream dispatch; only a successful upstream
/// HTTP response may insert the task, consume quota, append the consume log,
/// and increment user/channel counters.  Codes 21 and 22 are replay successes
/// and must be presented as code 1 without adding an idempotency guard.
///
/// The image proxy must look up `mj_id`, validate the resulting URL against
/// SSRF, then stream only successful upstream bytes.  It must not write
/// PostgreSQL or Valkey.
#[async_trait]
pub trait MediaTaskService: Send + Sync {
    /// Handle a protected task request, including the legacy auth boundary.
    async fn protected(&self, operation: MediaTaskOperation, request: Request) -> Response;
    /// Handle the public image proxy after task lookup and SSRF validation.
    async fn public_image(&self, task_id: String, request: Request) -> Response;
}

/// Provider-neutral relay for the non-Midjourney task protocols.
///
/// The relay receives the original request only after the HTTP boundary has
/// rejected missing bearer credentials.  Its production implementation owns
/// the remaining legacy `TokenAuth` and `Distribute` work: it must validate
/// the token, select an eligible channel, relay the request, and commit task,
/// quota, counter, and consumption-log effects together only after a
/// successful upstream submission.  Fetch operations must scope stored tasks
/// to the authenticated user before returning them.
///
/// Keeping this as a request/response pass-through is intentional.  Suno,
/// Kling, and Jimeng have provider-specific JSON transformations, while the
/// common relay owns retry and billing semantics; parsing them here would make
/// it possible for the HTTP route to drift from the transactional relay.
#[async_trait]
pub trait TaskRelayProvider: Send + Sync {
    /// Relay an authenticated static task route through its selected provider.
    async fn relay(&self, operation: MediaTaskOperation, request: Request) -> Response;
}

/// Explicitly unavailable fallback used until the application wires a real
/// task relay.  It fails closed and never invents a provider success.
#[derive(Default)]
pub struct UnconfiguredTaskRelayProvider;

#[async_trait]
impl TaskRelayProvider for UnconfiguredTaskRelayProvider {
    async fn relay(&self, _operation: MediaTaskOperation, _request: Request) -> Response {
        (
            StatusCode::SERVICE_UNAVAILABLE,
            axum::Json(json!({
                "error": {
                    "message": "task relay provider is not configured",
                    "type": "new_api_error",
                    "code": "service_unavailable"
                }
            })),
        )
            .into_response()
    }
}

/// Concrete implementation for the legacy static Midjourney paths.
///
/// The Go listener registers `/mj/...` and `/:mode/mj/...` as two spellings
/// of the same Midjourney protocol.  Keeping the former as an adapter over
/// [`MidjourneyBackend`] means authentication, PostgreSQL task ownership,
/// channel affinity, quota/log transaction and the hardened image fetcher are
/// shared with the dynamic route instead of being reimplemented here.
///
/// Suno, Kling and Jimeng are *not* Midjourney aliases in the Go oracle.  They
/// are delegated unchanged to the injected generic task relay, which keeps
/// their authentication, channel-distribution, retry, and accounting
/// transaction in one provider-aware implementation.
#[derive(Clone)]
pub struct MidjourneyMediaTaskService<B = PgMidjourneyBackend> {
    backend: Arc<B>,
    task_relay: Arc<dyn TaskRelayProvider>,
    image_signing_secret: Option<Arc<[u8]>>,
}

/// Production static-Midjourney service.  It is generic only so contract tests
/// can exercise the real boundary with a deterministic upstream mock.
pub type PgMediaTaskService = MidjourneyMediaTaskService<PgMidjourneyBackend>;

impl<B> MidjourneyMediaTaskService<B> {
    /// Builds the static-path adapter from the same backend used by dynamic MJ.
    #[must_use]
    pub fn new(backend: Arc<B>) -> Self {
        Self::new_with_task_relay(backend, Arc::new(UnconfiguredTaskRelayProvider))
    }

    /// Builds the static-path adapter with the real non-Midjourney task relay.
    #[must_use]
    pub fn new_with_task_relay(backend: Arc<B>, task_relay: Arc<dyn TaskRelayProvider>) -> Self {
        Self {
            backend,
            task_relay,
            image_signing_secret: image_signing_secret_from_env(),
        }
    }

    /// Replaces the non-Midjourney task relay while retaining the same
    /// Midjourney backend. This is useful when assembling the production
    /// listener from independently tested route modules.
    #[must_use]
    pub fn with_task_relay(mut self, task_relay: Arc<dyn TaskRelayProvider>) -> Self {
        self.task_relay = task_relay;
        self
    }

    /// Overrides the deployment-wide image-signing secret for an isolated
    /// listener or deterministic contract test.
    #[must_use]
    pub fn with_image_signing_secret(mut self, secret: impl AsRef<[u8]>) -> Self {
        self.image_signing_secret = (!secret.as_ref().is_empty())
            .then(|| Arc::from(secret.as_ref().to_vec().into_boxed_slice()));
        self
    }
}

#[async_trait]
impl<B> MediaTaskService for MidjourneyMediaTaskService<B>
where
    B: MidjourneyBackend + 'static,
{
    async fn protected(&self, operation: MediaTaskOperation, request: Request) -> Response {
        let (parts, body) = request.into_parts();
        let Some(mj_operation) = static_mj_operation(operation, parts.uri.path()) else {
            return self
                .task_relay
                .relay(operation, Request::from_parts(parts, body))
                .await;
        };
        let client_ip = parts
            .extensions
            .get::<RequestContext>()
            .and_then(|context| context.client_ip);
        let identity = match self.backend.authenticate(&parts.headers, client_ip).await {
            Ok(identity) => identity,
            Err(error) => return media_failure(error),
        };

        let body = match axum::body::to_bytes(body, 2 * 1024 * 1024).await {
            Ok(body) => body,
            Err(_) => return media_failure(MidjourneyFailure::Request("bind_request_body_failed")),
        };

        match mj_operation {
            StaticMjOperation::Submit(operation) => {
                let payload = match parse_mj_submit(operation, &body) {
                    Ok(payload) => payload,
                    Err(error) => return media_failure(error),
                };
                let submitted = match self
                    .backend
                    .submit(&identity, "mj", operation, &parts.headers, payload)
                    .await
                {
                    Ok(reply) => reply,
                    Err(error) => return media_failure(error),
                };
                let accepted = submitted.response.status == StatusCode::OK
                    && matches!(
                        submitted.response.body.get("code").and_then(Value::as_i64),
                        Some(1 | 21 | 22)
                    );
                let mut response_body = submitted.response.body;
                if accepted {
                    if let Err(error) = self
                        .backend
                        .record_submit(&identity, submitted.effect)
                        .await
                    {
                        return media_failure(error);
                    }
                    if matches!(
                        response_body.get("code").and_then(Value::as_i64),
                        Some(21 | 22)
                    ) {
                        response_body["code"] = json!(1);
                    }
                }
                media_json_response(
                    submitted.response.status,
                    submitted.response.content_type,
                    response_body,
                )
            }
            StaticMjOperation::Read { operation, task_id } => {
                match self
                    .backend
                    .task_read(&identity, operation, &task_id, &parts.headers, None)
                    .await
                {
                    Ok(reply) => media_json_response(reply.status, reply.content_type, reply.body),
                    Err(error) => media_failure(error),
                }
            }
            StaticMjOperation::List => {
                let payload = match parse_json_object(&body, "do_request_failed") {
                    Ok(payload) => payload,
                    Err(error) => return media_failure(error),
                };
                match self
                    .backend
                    .task_read(
                        &identity,
                        "list-by-condition",
                        "",
                        &parts.headers,
                        Some(payload),
                    )
                    .await
                {
                    Ok(reply) => media_json_response(reply.status, reply.content_type, reply.body),
                    Err(error) => media_failure(error),
                }
            }
        }
    }

    async fn public_image(&self, task_id: String, request: Request) -> Response {
        let Some(user_id) = signed_image_owner(
            request.uri(),
            &task_id,
            self.image_signing_secret.as_deref(),
        ) else {
            return (
                StatusCode::UNAUTHORIZED,
                axum::Json(json!({"error":"midjourney_image_signature_invalid"})),
            )
                .into_response();
        };
        let stored = match self.backend.image_for_owned(user_id, &task_id).await {
            Ok(stored) => stored,
            Err(MidjourneyFailure::NotFound) => {
                return (
                    StatusCode::NOT_FOUND,
                    axum::Json(json!({"error":"midjourney_task_not_found"})),
                )
                    .into_response();
            }
            Err(_) => {
                return (
                    StatusCode::INTERNAL_SERVER_ERROR,
                    axum::Json(json!({"error":"http_get_image_failed"})),
                )
                    .into_response();
            }
        };
        // `PgMidjourneyBackend::fetch_image` is the single strict URL/DNS/
        // redirect/streaming boundary.  Do not replace it with a convenience
        // reqwest call here: test instances must retain the same SSRF policy.
        match self.backend.fetch_image(&stored.url).await {
            Ok(ImageReply::Stream { content_type, body }) => {
                let mut response = Response::new(body);
                *response.status_mut() = StatusCode::OK;
                response
                    .headers_mut()
                    .insert(header::CONTENT_TYPE, content_type);
                response
            }
            Ok(ImageReply::Error { status, body }) => {
                (status, axum::Json(json!({"error":body}))).into_response()
            }
            Err(MidjourneyFailure::BlockedImage) => (
                StatusCode::FORBIDDEN,
                axum::Json(json!({"error":"request blocked: unsafe image URL"})),
            )
                .into_response(),
            Err(_) => (
                StatusCode::INTERNAL_SERVER_ERROR,
                axum::Json(json!({"error":"http_get_image_failed"})),
            )
                .into_response(),
        }
    }
}

enum StaticMjOperation {
    Submit(&'static str),
    Read {
        operation: &'static str,
        task_id: String,
    },
    List,
}

fn static_mj_operation(operation: MediaTaskOperation, path: &str) -> Option<StaticMjOperation> {
    match operation {
        MediaTaskOperation::Submit(action) => Some(StaticMjOperation::Submit(match action {
            "INSIGHT_FACE_SWAP" => "insight-face/swap",
            "ACTION" => "action",
            "BLEND" => "blend",
            "CHANGE" => "change",
            "DESCRIBE" => "describe",
            "EDITS" => "edits",
            "IMAGINE" => "imagine",
            "MODAL" => "modal",
            "SHORTEN" => "shorten",
            "SIMPLE_CHANGE" => "simple-change",
            "UPLOAD_DISCORD_IMAGES" => "upload-discord-images",
            "VIDEO" => "video",
            _ => return None,
        })),
        MediaTaskOperation::Fetch => Some(StaticMjOperation::Read {
            operation: "fetch",
            task_id: task_id_from_static_path(path)?,
        }),
        MediaTaskOperation::ImageSeed => Some(StaticMjOperation::Read {
            operation: "image-seed",
            task_id: task_id_from_static_path(path)?,
        }),
        MediaTaskOperation::ListByCondition => Some(StaticMjOperation::List),
        MediaTaskOperation::SunoSubmit
        | MediaTaskOperation::SunoFetch
        | MediaTaskOperation::SunoFetchById
        | MediaTaskOperation::KlingImageToVideo
        | MediaTaskOperation::KlingImageToVideoFetch
        | MediaTaskOperation::KlingTextToVideo
        | MediaTaskOperation::KlingTextToVideoFetch
        | MediaTaskOperation::JimengSubmit => None,
    }
}

fn task_id_from_static_path(path: &str) -> Option<String> {
    let segments = path.trim_matches('/').split('/').collect::<Vec<_>>();
    (segments.len() == 4 && segments[0] == "mj" && segments[1] == "task")
        .then(|| segments[2].to_owned())
        .filter(|task_id| !task_id.is_empty())
}

fn parse_json_object(bytes: &[u8], description: &'static str) -> Result<Value, MidjourneyFailure> {
    let value: Value =
        serde_json::from_slice(bytes).map_err(|_| MidjourneyFailure::Request(description))?;
    value
        .is_object()
        .then_some(value)
        .ok_or(MidjourneyFailure::Request(description))
}

fn parse_mj_submit(operation: &str, bytes: &[u8]) -> Result<Value, MidjourneyFailure> {
    let value = parse_json_object(bytes, "bind_request_body_failed")?;
    let required = match operation {
        "imagine" => value
            .get("prompt")
            .and_then(Value::as_str)
            .is_some_and(|value| !value.is_empty())
            .then_some(())
            .ok_or(MidjourneyFailure::Request("prompt_is_required")),
        "action" => value
            .get("customId")
            .and_then(Value::as_str)
            .is_some_and(|value| !value.is_empty())
            .then_some(())
            .ok_or(MidjourneyFailure::Request("custom_id_is_required")),
        "change" => value
            .get("taskId")
            .and_then(Value::as_str)
            .is_some_and(|value| !value.is_empty())
            .then_some(())
            .ok_or(MidjourneyFailure::Request("task_id_is_required")),
        "modal" | "video" => value
            .get("taskId")
            .and_then(Value::as_str)
            .is_some_and(|value| !value.is_empty())
            .then_some(())
            .ok_or(MidjourneyFailure::Request("task_id_is_required")),
        _ => Ok(()),
    };
    required.map(|()| value)
}

fn media_json_response(
    status: StatusCode,
    content_type: axum::http::HeaderValue,
    body: Value,
) -> Response {
    let mut response = (status, axum::Json(body)).into_response();
    response
        .headers_mut()
        .insert(header::CONTENT_TYPE, content_type);
    response
}

fn media_failure(error: MidjourneyFailure) -> Response {
    match error {
        MidjourneyFailure::Unauthorized => (StatusCode::UNAUTHORIZED, axum::Json(json!({"code":401,"description":"unauthorized","type":"auth_error"}))).into_response(),
        MidjourneyFailure::Forbidden => (StatusCode::FORBIDDEN, axum::Json(json!({"error":{"message":"access denied","type":"new_api_error","code":"access_denied"}}))).into_response(),
        MidjourneyFailure::NotFound => (StatusCode::BAD_REQUEST, axum::Json(json!({"code":4,"description":"task_no_found ","type":"upstream_error"}))).into_response(),
        MidjourneyFailure::Upstream => (StatusCode::BAD_REQUEST, axum::Json(json!({"code":5,"description":"do_request_failed ","type":"upstream_error"}))).into_response(),
        MidjourneyFailure::InvalidUpstreamJson => (StatusCode::BAD_REQUEST, axum::Json(json!({"code":5,"description":"unmarshal_response_body_failed ","type":"upstream_error"}))).into_response(),
        MidjourneyFailure::Storage => (StatusCode::INTERNAL_SERVER_ERROR, axum::Json(json!({"code":500,"description":"database_error","type":"server_error"}))).into_response(),
        MidjourneyFailure::Request(description) => (StatusCode::BAD_REQUEST, axum::Json(json!({"code":4,"description":format!("{description} "),"type":"upstream_error"}))).into_response(),
        MidjourneyFailure::BlockedImage => (StatusCode::FORBIDDEN, axum::Json(json!({"error":"request blocked: unsafe image URL"}))).into_response(),
    }
}

/// Router state for the media-task vertical slice.
#[derive(Clone)]
pub struct MediaTaskHttpState {
    service: Arc<dyn MediaTaskService>,
}

impl MediaTaskHttpState {
    /// Creates route state from an application-owned task relay service.
    #[must_use]
    pub fn new(service: Arc<dyn MediaTaskService>) -> Self {
        Self { service }
    }
}

/// Builds the static media-task compatibility routes owned by this slice.
///
/// Dynamic `/:mode/mj/...` aliases require the outer mode dispatcher and are
/// intentionally mounted there, not duplicated in this independent router.
/// Suno, Kling, and Jimeng are static legacy families and share the same
/// protected service boundary so token ownership and accounting cannot drift.
//
// The static Midjourney paths are an intentional split mount: the normal
// listener owns them through `media_midjourney_router`, while the isolated
// test-instance surface uses this service adapter.  Naming these paths as
// *_PATH constants makes that non-owning relationship explicit to the route
// coverage gate instead of treating the two candidate adapters as competing
// production owners.
const MJ_IMAGE_PATH: &str = "/mj/image/{id}";
const MJ_SWAP_PATH: &str = "/mj/insight-face/swap";
const MJ_ACTION_PATH: &str = "/mj/submit/action";
const MJ_BLEND_PATH: &str = "/mj/submit/blend";
const MJ_CHANGE_PATH: &str = "/mj/submit/change";
const MJ_DESCRIBE_PATH: &str = "/mj/submit/describe";
const MJ_EDITS_PATH: &str = "/mj/submit/edits";
const MJ_IMAGINE_PATH: &str = "/mj/submit/imagine";
const MJ_MODAL_PATH: &str = "/mj/submit/modal";
const MJ_SHORTEN_PATH: &str = "/mj/submit/shorten";
const MJ_SIMPLE_CHANGE_PATH: &str = "/mj/submit/simple-change";
const MJ_UPLOAD_DISCORD_IMAGES_PATH: &str = "/mj/submit/upload-discord-images";
const MJ_VIDEO_PATH: &str = "/mj/submit/video";
const MJ_FETCH_PATH: &str = "/mj/task/{id}/fetch";
const MJ_IMAGE_SEED_PATH: &str = "/mj/task/{id}/image-seed";
const MJ_LIST_PATH: &str = "/mj/task/list-by-condition";

/// Mounts the non-Midjourney static task families.
///
/// Midjourney static paths stay on [`media_midjourney_router`]; this sibling
/// router is the production owner for Suno, Kling, and Jimeng.
pub fn media_provider_task_router(state: MediaTaskHttpState) -> Router {
    Router::new()
        .route("/suno/fetch", post(suno_fetch))
        .route("/suno/fetch/{id}", get(suno_fetch_by_id))
        .route("/suno/submit/{action}", post(suno_submit))
        .route("/kling/v1/videos/image2video", post(kling_image_to_video))
        .route("/kling/v1/videos/text2video", post(kling_text_to_video))
        .route("/jimeng/", post(jimeng_submit))
        .with_state(state)
}

pub fn media_task_router(state: MediaTaskHttpState) -> Router {
    Router::new()
        .route(MJ_IMAGE_PATH, get(public_image))
        .route(MJ_SWAP_PATH, post(submit_insight_face_swap))
        .route(MJ_ACTION_PATH, post(submit_action))
        .route(MJ_BLEND_PATH, post(submit_blend))
        .route(MJ_CHANGE_PATH, post(submit_change))
        .route(MJ_DESCRIBE_PATH, post(submit_describe))
        .route(MJ_EDITS_PATH, post(submit_edits))
        .route(MJ_IMAGINE_PATH, post(submit_imagine))
        .route(MJ_MODAL_PATH, post(submit_modal))
        .route(MJ_SHORTEN_PATH, post(submit_shorten))
        .route(MJ_SIMPLE_CHANGE_PATH, post(submit_simple_change))
        .route(
            MJ_UPLOAD_DISCORD_IMAGES_PATH,
            post(submit_upload_discord_images),
        )
        .route(MJ_VIDEO_PATH, post(submit_video))
        .route(MJ_FETCH_PATH, get(fetch))
        .route(MJ_IMAGE_SEED_PATH, get(image_seed))
        .route(MJ_LIST_PATH, post(list_by_condition))
        .with_state(state.clone())
        .merge(media_provider_task_router(state))
}

async fn public_image(
    State(state): State<MediaTaskHttpState>,
    Path(id): Path<String>,
    request: Request,
) -> Response {
    state.service.public_image(id, request).await
}

async fn protected(
    state: MediaTaskHttpState,
    operation: MediaTaskOperation,
    request: Request,
) -> Response {
    // Legacy `TokenAuth` is installed before every route except `/mj/image`.
    // Keeping this coarse check at the HTTP edge prevents an accidentally
    // permissive service implementation from reaching an upstream or writing
    // accounting state for a request without credentials.  Credential lookup,
    // expiry, and channel distribution remain transactional service concerns.
    if !has_bearer_credential(request.headers()) {
        return missing_token_response();
    }
    state.service.protected(operation, request).await
}

fn has_bearer_credential(headers: &axum::http::HeaderMap) -> bool {
    headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.strip_prefix("Bearer "))
        .is_some_and(|token| !token.trim().is_empty())
}

fn missing_token_response() -> Response {
    (
        StatusCode::UNAUTHORIZED,
        [(header::CONTENT_TYPE, "application/json; charset=utf-8")],
        Body::from(r#"{"error":{"message":"Invalid token","type":"new_api_error","code":""}}"#),
    )
        .into_response()
}

macro_rules! submit_handler {
    ($name:ident, $action:literal) => {
        async fn $name(State(state): State<MediaTaskHttpState>, request: Request) -> Response {
            protected(state, MediaTaskOperation::Submit($action), request).await
        }
    };
}

submit_handler!(submit_insight_face_swap, "INSIGHT_FACE_SWAP");
submit_handler!(submit_action, "ACTION");
submit_handler!(submit_blend, "BLEND");
submit_handler!(submit_change, "CHANGE");
submit_handler!(submit_describe, "DESCRIBE");
submit_handler!(submit_edits, "EDITS");
submit_handler!(submit_imagine, "IMAGINE");
submit_handler!(submit_modal, "MODAL");
submit_handler!(submit_shorten, "SHORTEN");
submit_handler!(submit_simple_change, "SIMPLE_CHANGE");
submit_handler!(submit_upload_discord_images, "UPLOAD_DISCORD_IMAGES");
submit_handler!(submit_video, "VIDEO");

async fn fetch(State(state): State<MediaTaskHttpState>, request: Request) -> Response {
    protected(state, MediaTaskOperation::Fetch, request).await
}

async fn image_seed(State(state): State<MediaTaskHttpState>, request: Request) -> Response {
    protected(state, MediaTaskOperation::ImageSeed, request).await
}

async fn list_by_condition(State(state): State<MediaTaskHttpState>, request: Request) -> Response {
    protected(state, MediaTaskOperation::ListByCondition, request).await
}

async fn suno_fetch(State(state): State<MediaTaskHttpState>, request: Request) -> Response {
    protected(state, MediaTaskOperation::SunoFetch, request).await
}

async fn suno_fetch_by_id(State(state): State<MediaTaskHttpState>, request: Request) -> Response {
    protected(state, MediaTaskOperation::SunoFetchById, request).await
}

async fn suno_submit(State(state): State<MediaTaskHttpState>, request: Request) -> Response {
    protected(state, MediaTaskOperation::SunoSubmit, request).await
}

async fn kling_image_to_video(
    State(state): State<MediaTaskHttpState>,
    request: Request,
) -> Response {
    protected(state, MediaTaskOperation::KlingImageToVideo, request).await
}

async fn kling_text_to_video(
    State(state): State<MediaTaskHttpState>,
    request: Request,
) -> Response {
    protected(state, MediaTaskOperation::KlingTextToVideo, request).await
}

async fn jimeng_submit(State(state): State<MediaTaskHttpState>, request: Request) -> Response {
    protected(state, MediaTaskOperation::JimengSubmit, request).await
}

#[cfg(test)]
mod tests {
    use std::sync::{Arc, Mutex};

    use super::*;
    use axum::{
        body::to_bytes,
        http::{Request as HttpRequest, header},
    };
    use tower::ServiceExt;

    type Calls = Arc<Mutex<Vec<(MediaTaskOperation, String, Vec<u8>)>>>;
    type TestResult<T = ()> = Result<T, Box<dyn std::error::Error + Send + Sync>>;

    fn test_error(
        context: impl std::fmt::Display,
        error: impl std::fmt::Display,
    ) -> Box<dyn std::error::Error + Send + Sync> {
        Box::new(std::io::Error::other(format!("{context}: {error}")))
    }

    fn test_message(context: impl Into<String>) -> Box<dyn std::error::Error + Send + Sync> {
        Box::new(std::io::Error::other(context.into()))
    }

    #[derive(Clone)]
    struct CapturingService {
        calls: Calls,
    }

    #[async_trait]
    impl MediaTaskService for CapturingService {
        async fn protected(&self, operation: MediaTaskOperation, request: Request) -> Response {
            let path = request.uri().path().to_owned();
            let body = match to_bytes(request.into_body(), usize::MAX).await {
                Ok(body) => body,
                Err(_) => return StatusCode::INTERNAL_SERVER_ERROR.into_response(),
            };
            let mut calls = match self.calls.lock() {
                Ok(calls) => calls,
                Err(_) => return StatusCode::SERVICE_UNAVAILABLE.into_response(),
            };
            calls.push((operation, path, body.to_vec()));
            drop(calls);
            (
                StatusCode::ACCEPTED,
                [
                    ("content-type", "application/json"),
                    ("x-relay", "provider"),
                ],
                Body::from(r#"{"provider":"received"}"#),
            )
                .into_response()
        }

        async fn public_image(&self, _task_id: String, _request: Request) -> Response {
            StatusCode::NOT_FOUND.into_response()
        }
    }

    fn app(calls: Calls) -> Router {
        media_task_router(MediaTaskHttpState::new(Arc::new(CapturingService {
            calls,
        })))
    }

    #[tokio::test]
    async fn provider_routes_select_the_matching_task_operation_and_preserve_the_request()
    -> TestResult {
        let cases = [
            (
                "POST",
                "/suno/fetch",
                MediaTaskOperation::SunoFetch,
                br#"{"ids":["suno-1"]}"#.as_slice(),
            ),
            (
                "GET",
                "/suno/fetch/suno-2",
                MediaTaskOperation::SunoFetchById,
                b"".as_slice(),
            ),
            (
                "POST",
                "/suno/submit/generate",
                MediaTaskOperation::SunoSubmit,
                br#"{"prompt":"music"}"#.as_slice(),
            ),
            (
                "POST",
                "/kling/v1/videos/image2video",
                MediaTaskOperation::KlingImageToVideo,
                br#"{"image":"data"}"#.as_slice(),
            ),
            (
                "POST",
                "/kling/v1/videos/text2video",
                MediaTaskOperation::KlingTextToVideo,
                br#"{"prompt":"video"}"#.as_slice(),
            ),
            (
                "POST",
                "/jimeng/",
                MediaTaskOperation::JimengSubmit,
                br#"{"req_key":"jimeng_vgfm_t2v_l20"}"#.as_slice(), // gitleaks:allow -- provider model fixture
            ),
        ];

        for (row, (method, path, operation, body)) in cases.into_iter().enumerate() {
            let uri = path.parse::<axum::http::Uri>().map_err(|error| {
                test_error(format!("parse provider route row {row} URI {path}"), error)
            })?;
            let request = HttpRequest::builder()
                .method(method)
                .uri(uri)
                .header(header::AUTHORIZATION, "Bearer test-token")
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(body.to_vec()))
                .map_err(|error| {
                    test_error(format!("build provider route row {row} request"), error)
                })?;
            let calls = Arc::new(Mutex::new(Vec::new()));
            let response = app(calls.clone())
                .oneshot(request)
                .await
                .map_err(|error| test_error(format!("serve provider route row {row}"), error))?;

            assert_eq!(response.status(), StatusCode::ACCEPTED, "{path}");
            let relay_header = response
                .headers()
                .get("x-relay")
                .ok_or_else(|| {
                    test_message(format!("provider route row {row} missing x-relay header"))
                })?
                .to_str()
                .map_err(|error| {
                    test_error(
                        format!("decode provider route row {row} x-relay header"),
                        error,
                    )
                })?;
            assert_eq!(relay_header, "provider", "{path}");
            let response_body =
                to_bytes(response.into_body(), usize::MAX)
                    .await
                    .map_err(|error| {
                        test_error(
                            format!("read provider route row {row} response body"),
                            error,
                        )
                    })?;
            let response_json: Value = serde_json::from_slice(&response_body).map_err(|error| {
                test_error(
                    format!("decode provider route row {row} response JSON"),
                    error,
                )
            })?;
            assert_eq!(response_json, json!({"provider": "received"}), "{path}");
            let recorded_calls = calls.lock().map_err(|error| {
                test_error(format!("lock provider route row {row} calls"), error)
            })?;
            assert_eq!(
                recorded_calls.as_slice(),
                &[(operation, path.to_owned(), body.to_vec())],
                "{path}"
            );
        }
        Ok(())
    }

    #[tokio::test]
    async fn provider_routes_reject_missing_bearer_credentials_before_relaying() -> TestResult {
        let uri = "/suno/fetch"
            .parse::<axum::http::Uri>()
            .map_err(|error| test_error("parse missing-credential request URI", error))?;
        let request = HttpRequest::builder()
            .method("POST")
            .uri(uri)
            .body(Body::from(br#"{"ids":["suno-1"]}"#.as_slice()))
            .map_err(|error| test_error("build missing-credential request", error))?;
        let calls = Arc::new(Mutex::new(Vec::new()));
        let response = app(calls.clone())
            .oneshot(request)
            .await
            .map_err(|error| test_error("serve missing-credential request", error))?;

        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert!(
            calls
                .lock()
                .map_err(|error| test_error("lock missing-credential call rows", error))?
                .is_empty()
        );
        Ok(())
    }

    #[tokio::test]
    async fn unconfigured_provider_fails_closed_as_unavailable() -> TestResult {
        let uri = "/suno/fetch"
            .parse::<axum::http::Uri>()
            .map_err(|error| test_error("parse unconfigured-provider request URI", error))?;
        let request = HttpRequest::builder()
            .uri(uri)
            .body(Body::empty())
            .map_err(|error| test_error("build unconfigured-provider request", error))?;
        let response = UnconfiguredTaskRelayProvider
            .relay(MediaTaskOperation::SunoFetch, request)
            .await;

        assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
        Ok(())
    }
}
