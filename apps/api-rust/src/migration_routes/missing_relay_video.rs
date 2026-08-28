//! OpenAI-compatible video relay routes.
//!
//! Legacy Go registers these endpoints inside `SetRelayRouter`: token
//! authentication, channel selection, rate limiting and accounting happen
//! before the task handler (`RelayTask` / `RelayTaskFetch`) or binary content
//! proxy (`VideoProxy`).  The HTTP slice keeps that ordering explicit through
//! an injected service and, importantly, never decodes a video request or
//! response.  Video creation bodies and content replies can be multipart or
//! binary, while task providers may use streaming responses.

use std::sync::Arc;

use async_trait::async_trait;
use axum::{
    Router,
    extract::{Request, State},
    http::StatusCode,
    response::{IntoResponse, Response},
    routing::{get, post},
};
use serde::Serialize;

use crate::RequestContext;

/// The legacy handler selected by the matched video route.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RelayVideoOperation {
    /// `controller.RelayTask` creates a video-generation task.
    CreateTask,
    /// `controller.RelayTaskFetch` looks up a video-generation task.
    FetchTask,
    /// `controller.VideoProxy` forwards binary video content.
    FetchContent,
    /// `controller.RelayTask` remixes a previously generated video.
    RemixTask,
}

/// Result of the shared legacy relay authorization pipeline.
///
/// Implementations must authenticate the caller and select an eligible
/// channel before returning [`Self::Authorized`].  In particular, an adapter
/// must not trust a channel URL or credential supplied by the request.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum RelayVideoAuthorization {
    Authorized,
    Rejected { status: StatusCode, message: String },
}

/// Boundary between route matching and the relay runtime.
///
/// This is deliberately a request/response pass-through contract.  It keeps
/// multipart creation payloads, upstream headers, binary MP4 bytes and SSE
/// task updates intact.  A concrete adapter owns the Go-compatible
/// token/rate-limit/channel/quota/logging transaction; failures must return a
/// failed authorization or a safe upstream error response rather than
/// forwarding an unauthenticated request.
#[async_trait]
pub trait RelayVideoService: Send + Sync {
    /// Run auth, policy checks and channel distribution before any upstream I/O.
    async fn authorize(&self, request: &Request) -> RelayVideoAuthorization;

    /// Relay an already-authorized request to the selected legacy-compatible
    /// video handler.  The returned response is forwarded verbatim.
    async fn relay(&self, operation: RelayVideoOperation, request: Request) -> Response;
}

/// Injectable state for the independently testable video route slice.
#[derive(Clone)]
pub struct RelayVideoHttpState {
    service: Arc<dyn RelayVideoService>,
}

impl RelayVideoHttpState {
    #[must_use]
    pub fn new(service: Arc<dyn RelayVideoService>) -> Self {
        Self { service }
    }
}

/// Fail-closed video adapter for listeners without a live video provider.
///
/// `authorize` cannot inspect the request body: Axum's `Request<Body>` is not
/// Sync, so an async-trait future must not capture a shared borrow.
#[derive(Clone, Default)]
pub struct FailClosedRelayVideoService;

impl FailClosedRelayVideoService {
    #[must_use]
    pub fn new() -> Self {
        Self
    }
}

#[async_trait]
impl RelayVideoService for FailClosedRelayVideoService {
    async fn authorize(&self, _: &Request) -> RelayVideoAuthorization {
        RelayVideoAuthorization::Rejected {
            status: StatusCode::UNAUTHORIZED,
            message: "Invalid token".to_owned(),
        }
    }

    async fn relay(&self, _: RelayVideoOperation, _: Request) -> Response {
        StatusCode::SERVICE_UNAVAILABLE.into_response()
    }
}

/// Routes migrated from the legacy authenticated relay router.
///
/// They intentionally are not mounted by this module itself: the root router
/// must compose them behind the same global relay middleware as the existing
/// OpenAI relay surface.
pub fn missing_relay_video_router(state: RelayVideoHttpState) -> Router {
    Router::new()
        .route("/v1/video/generations", post(create_task))
        .route("/v1/video/generations/{task_id}", get(fetch_task))
        .route("/v1/videos", post(create_task))
        .route("/v1/videos/{task_id}", get(fetch_task))
        .route("/v1/videos/{task_id}/content", get(fetch_content))
        .route("/v1/videos/{video_id}/remix", post(remix_task))
        .method_not_allowed_fallback(method_mismatch_as_not_found)
        .with_state(state)
}

// Gin's `HandleMethodNotAllowed` is disabled for the frozen Go server, so a
// path that exists only for another HTTP method falls through as a 404.
async fn method_mismatch_as_not_found(_: Request) -> Response {
    StatusCode::NOT_FOUND.into_response()
}

async fn create_task(State(state): State<RelayVideoHttpState>, request: Request) -> Response {
    relay(state, RelayVideoOperation::CreateTask, request).await
}

async fn fetch_task(State(state): State<RelayVideoHttpState>, request: Request) -> Response {
    relay(state, RelayVideoOperation::FetchTask, request).await
}

async fn fetch_content(State(state): State<RelayVideoHttpState>, request: Request) -> Response {
    relay(state, RelayVideoOperation::FetchContent, request).await
}

async fn remix_task(State(state): State<RelayVideoHttpState>, request: Request) -> Response {
    relay(state, RelayVideoOperation::RemixTask, request).await
}

async fn relay(
    state: RelayVideoHttpState,
    operation: RelayVideoOperation,
    request: Request,
) -> Response {
    match state.service.authorize(&request).await {
        RelayVideoAuthorization::Authorized => state.service.relay(operation, request).await,
        RelayVideoAuthorization::Rejected { status, message } => {
            legacy_auth_error(status, message, &request)
        }
    }
}

fn legacy_auth_error(status: StatusCode, message: String, request: &Request) -> Response {
    let request_id = request.extensions().get::<RequestContext>().map_or_else(
        || "unknown".to_owned(),
        |context| context.request_id.clone(),
    );
    let mut response = (
        status,
        axum::Json(LegacyAuthErrorEnvelope {
            error: LegacyAuthError {
                message: format!("{message} (request id: {request_id})"),
                kind: "new_api_error",
                code: "",
            },
        }),
    )
        .into_response();
    response.headers_mut().insert(
        axum::http::header::CONTENT_TYPE,
        axum::http::HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

#[derive(Serialize)]
struct LegacyAuthErrorEnvelope {
    error: LegacyAuthError,
}

#[derive(Serialize)]
struct LegacyAuthError {
    message: String,
    #[serde(rename = "type")]
    kind: &'static str,
    code: &'static str,
}

#[cfg(test)]
mod tests {
    use std::sync::{Arc, Mutex};

    use super::*;
    use axum::{
        body::{Body, to_bytes},
        http::{HeaderValue, Request as HttpRequest, header},
    };
    use tower::ServiceExt;

    type RelayCall = (RelayVideoOperation, String, Vec<u8>);
    type RelayCalls = Arc<Mutex<Vec<RelayCall>>>;
    type TestResult<T = ()> = Result<T, Box<dyn std::error::Error + Send + Sync>>;

    fn test_error(
        context: impl std::fmt::Display,
        error: impl std::fmt::Display,
    ) -> Box<dyn std::error::Error + Send + Sync> {
        Box::new(std::io::Error::other(format!("{context}: {error}")))
    }

    #[derive(Clone)]
    struct TestService {
        authorization: RelayVideoAuthorization,
        calls: RelayCalls,
        status: StatusCode,
        response_headers: Vec<(&'static str, &'static str)>,
        response_body: Vec<u8>,
    }

    #[async_trait]
    impl RelayVideoService for TestService {
        async fn authorize(&self, _: &Request) -> RelayVideoAuthorization {
            self.authorization.clone()
        }

        async fn relay(&self, operation: RelayVideoOperation, request: Request) -> Response {
            let path = request.uri().path().to_owned();
            let Ok(body) = to_bytes(request.into_body(), usize::MAX).await else {
                return StatusCode::BAD_GATEWAY.into_response();
            };
            let Ok(mut calls) = self.calls.lock() else {
                return StatusCode::SERVICE_UNAVAILABLE.into_response();
            };
            calls.push((operation, path, body.to_vec()));
            drop(calls);

            let mut response = Response::new(Body::from(self.response_body.clone()));
            *response.status_mut() = self.status;
            for (name, value) in &self.response_headers {
                response
                    .headers_mut()
                    .insert(*name, HeaderValue::from_static(value));
            }
            response
        }
    }

    fn app(authorization: RelayVideoAuthorization, calls: RelayCalls) -> Router {
        missing_relay_video_router(RelayVideoHttpState::new(Arc::new(TestService {
            authorization,
            calls,
            status: StatusCode::ACCEPTED,
            response_headers: vec![("content-type", "video/mp4"), ("x-upstream-id", "video-1")],
            response_body: vec![0, 255, 7],
        })))
    }

    #[tokio::test]
    async fn public_routes_select_the_legacy_handler_and_preserve_opaque_bodies() -> TestResult {
        let cases = [
            (
                "POST",
                "/v1/video/generations",
                RelayVideoOperation::CreateTask,
            ),
            (
                "GET",
                "/v1/video/generations/task-1",
                RelayVideoOperation::FetchTask,
            ),
            ("POST", "/v1/videos", RelayVideoOperation::CreateTask),
            ("GET", "/v1/videos/task-2", RelayVideoOperation::FetchTask),
            (
                "GET",
                "/v1/videos/task-3/content",
                RelayVideoOperation::FetchContent,
            ),
            (
                "POST",
                "/v1/videos/video-1/remix",
                RelayVideoOperation::RemixTask,
            ),
        ];
        for (method, path, operation) in cases {
            let calls = Arc::new(Mutex::new(Vec::new()));
            let request = HttpRequest::builder()
                .method(method)
                .uri(path)
                .header(header::CONTENT_TYPE, "application/octet-stream")
                .body(Body::from(vec![0, 255, 1]))
                .map_err(|error| test_error(format!("build {method} {path} request"), error))?;
            let response = app(RelayVideoAuthorization::Authorized, calls.clone())
                .oneshot(request)
                .await
                .map_err(|error| test_error(format!("receive {method} {path} response"), error))?;
            assert_eq!(response.status(), StatusCode::ACCEPTED, "{path}");
            assert_eq!(
                response.headers()[header::CONTENT_TYPE],
                "video/mp4",
                "{path}"
            );
            assert_eq!(response.headers()["x-upstream-id"], "video-1", "{path}");
            let response_body = to_bytes(response.into_body(), usize::MAX)
                .await
                .map_err(|error| test_error(format!("read {method} {path} response body"), error))?;
            assert_eq!(response_body, vec![0, 255, 7], "{path}");
            let recorded_calls = calls
                .lock()
                .map_err(|error| test_error(format!("lock {method} {path} relay calls"), error))?;
            assert_eq!(
                recorded_calls.as_slice(),
                &[(operation, path.to_owned(), vec![0, 255, 1])],
                "{path}"
            );
        }
        Ok(())
    }

    #[tokio::test]
    async fn rejected_authentication_fails_closed_without_upstream_call() -> TestResult {
        let calls = Arc::new(Mutex::new(Vec::new()));
        let request = HttpRequest::post("/v1/videos/video-1/remix")
            .body(Body::from("{}"))
            .map_err(|error| test_error("build rejected-auth request", error))?;
        let response = app(
            RelayVideoAuthorization::Rejected {
                status: StatusCode::UNAUTHORIZED,
                message: "Invalid token".into(),
            },
            calls.clone(),
        )
        .oneshot(request)
        .await
        .map_err(|error| test_error("receive rejected-auth response", error))?;
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "application/json; charset=utf-8"
        );
        let response_body = to_bytes(response.into_body(), usize::MAX)
            .await
            .map_err(|error| test_error("read rejected-auth response body", error))?;
        let response_text = std::str::from_utf8(&response_body)
            .map_err(|error| test_error("decode rejected-auth response body as UTF-8", error))?;
        assert_eq!(
            response_text,
            r#"{"error":{"message":"Invalid token (request id: unknown)","type":"new_api_error","code":""}}"#
        );
        assert!(
            calls
                .lock()
                .map_err(|error| test_error("lock rejected-auth relay calls", error))?
                .is_empty()
        );
        Ok(())
    }

    #[tokio::test]
    async fn rejected_authentication_uses_the_boundary_request_id_in_the_legacy_envelope(
    ) -> TestResult {
        let calls = Arc::new(Mutex::new(Vec::new()));
        let mut request = HttpRequest::post("/v1/videos/video-1/remix")
            .body(Body::from("{}"))
            .map_err(|error| test_error("build request-id auth request", error))?;
        request.extensions_mut().insert(crate::RequestContext {
            request_id: "video-auth-request-id".to_owned(),
            client_ip: None,
        });
        let response = app(
            RelayVideoAuthorization::Rejected {
                status: StatusCode::UNAUTHORIZED,
                message: "Invalid token".into(),
            },
            calls.clone(),
        )
        .oneshot(request)
        .await
        .map_err(|error| test_error("receive request-id auth response", error))?;

        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "application/json; charset=utf-8"
        );
        let response_body = to_bytes(response.into_body(), usize::MAX)
            .await
            .map_err(|error| test_error("read request-id auth response body", error))?;
        let response_text = std::str::from_utf8(&response_body)
            .map_err(|error| test_error("decode request-id auth response body as UTF-8", error))?;
        assert_eq!(
            response_text,
            r#"{"error":{"message":"Invalid token (request id: video-auth-request-id)","type":"new_api_error","code":""}}"#
        );
        assert!(
            calls
                .lock()
                .map_err(|error| test_error("lock request-id auth relay calls", error))?
                .is_empty()
        );
        Ok(())
    }

    #[tokio::test]
    async fn wrong_methods_and_unknown_paths_do_not_reach_the_relay_boundary() -> TestResult {
        let calls = Arc::new(Mutex::new(Vec::new()));
        let router = app(RelayVideoAuthorization::Authorized, Arc::clone(&calls));
        for (method, path, expected) in [
            ("GET", "/v1/videos", StatusCode::NOT_FOUND),
            ("POST", "/v1/videos/not-a-route", StatusCode::NOT_FOUND),
        ] {
            let request = HttpRequest::builder()
                .method(method)
                .uri(path)
                .body(Body::empty())
                .map_err(|error| test_error(format!("build {method} {path} request"), error))?;
            let response = router
                .clone()
                .oneshot(request)
                .await
                .map_err(|error| test_error(format!("receive {method} {path} response"), error))?;
            assert_eq!(response.status(), expected, "{method} {path}");
        }
        assert!(
            calls
                .lock()
                .map_err(|error| test_error("lock method-boundary relay calls", error))?
                .is_empty()
        );
        Ok(())
    }

    #[test]
    fn route_construction_has_no_duplicate_paths() -> TestResult {
        let calls = Arc::new(Mutex::new(Vec::new()));
        assert!(
            std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                let _ = app(RelayVideoAuthorization::Authorized, calls);
            }))
            .is_ok()
        );
        Ok(())
    }
}
