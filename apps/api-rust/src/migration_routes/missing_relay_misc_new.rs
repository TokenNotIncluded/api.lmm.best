//! Remaining legacy relay endpoints whose bodies must remain opaque.
//!
//! `realtime` can be a long-lived upgrade request, while the other two
//! endpoints deliberately accept vendor-specific JSON.  The HTTP layer must
//! therefore authenticate before dispatching, then hand the original request
//! to a transactional relay adapter without parsing or rebuilding it.

use std::sync::Arc;

use async_trait::async_trait;
use axum::{
    Router,
    extract::{Request, State},
    http::{HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use serde::Serialize;

use crate::RequestContext;

/// Exact legacy endpoint selected by the client.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum MissingRelayEndpoint {
    Realtime,
    PgChatCompletions,
    Edits,
    PgImagesGenerations,
    PgImagesEdits,
}

/// A typed authorization failure returned before a request reaches a relay.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct MissingRelayAuthRejection {
    pub status: StatusCode,
    pub code: &'static str,
    pub message: String,
}

/// Result of the shared token-auth/rate-limit/channel-selection boundary.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum MissingRelayAuthorization {
    Authorized,
    Rejected(MissingRelayAuthRejection),
}

/// Relay boundary for endpoints not covered by the typed OpenAI adapters yet.
///
/// Implementations own credential validation, model/channel selection,
/// accounting, retry/refund behavior and outbound I/O.  A handler cannot
/// reach an upstream when authorization fails.
#[async_trait]
pub trait MissingRelayService: Send + Sync {
    async fn authorize(
        &self,
        endpoint: MissingRelayEndpoint,
        request: &Request,
    ) -> MissingRelayAuthorization;

    /// Forward the original request.  Returning an Axum response preserves
    /// SSE, upgrade-related headers and unrecognised provider fields.
    async fn relay(&self, endpoint: MissingRelayEndpoint, request: Request) -> Response;
}

/// State for [`missing_relay_misc_router`].
#[derive(Clone)]
pub struct MissingRelayMiscState {
    service: Arc<dyn MissingRelayService>,
}

impl MissingRelayMiscState {
    #[must_use]
    pub fn new(service: Arc<dyn MissingRelayService>) -> Self {
        Self { service }
    }
}

/// Fail-closed misc-relay adapter for listeners without a live provider.
#[derive(Clone, Default)]
pub struct FailClosedRelayMiscService;

impl FailClosedRelayMiscService {
    #[must_use]
    pub fn new() -> Self {
        Self
    }
}

#[async_trait]
impl MissingRelayService for FailClosedRelayMiscService {
    async fn authorize(
        &self,
        endpoint: MissingRelayEndpoint,
        _: &Request,
    ) -> MissingRelayAuthorization {
        MissingRelayAuthorization::Rejected(MissingRelayAuthRejection {
            status: StatusCode::UNAUTHORIZED,
            code: "AUTH_UNAUTHORIZED",
            message: match endpoint {
                MissingRelayEndpoint::Realtime
                | MissingRelayEndpoint::Edits
                | MissingRelayEndpoint::PgImagesGenerations
                | MissingRelayEndpoint::PgImagesEdits => "Invalid token",
                MissingRelayEndpoint::PgChatCompletions => "Unauthorized, invalid access token",
            }
            .to_owned(),
        })
    }

    async fn relay(&self, _: MissingRelayEndpoint, _: Request) -> Response {
        StatusCode::SERVICE_UNAVAILABLE.into_response()
    }
}

/// Mount point for the remaining three legacy relay paths.
pub fn missing_relay_misc_router(state: MissingRelayMiscState) -> Router {
    Router::new()
        .route("/v1/realtime", get(realtime))
        .route("/pg/chat/completions", post(pg_chat_completions))
        .route("/v1/edits", post(edits))
        .route("/pg/images/generations", post(pg_images_generations))
        .route("/pg/images/edits", post(pg_images_edits))
        .with_state(state)
}

async fn realtime(State(state): State<MissingRelayMiscState>, request: Request) -> Response {
    relay(state, MissingRelayEndpoint::Realtime, request).await
}

async fn pg_chat_completions(
    State(state): State<MissingRelayMiscState>,
    request: Request,
) -> Response {
    relay(state, MissingRelayEndpoint::PgChatCompletions, request).await
}

async fn edits(State(state): State<MissingRelayMiscState>, request: Request) -> Response {
    relay(state, MissingRelayEndpoint::Edits, request).await
}

async fn pg_images_generations(
    State(state): State<MissingRelayMiscState>,
    request: Request,
) -> Response {
    relay(state, MissingRelayEndpoint::PgImagesGenerations, request).await
}

async fn pg_images_edits(State(state): State<MissingRelayMiscState>, request: Request) -> Response {
    relay(state, MissingRelayEndpoint::PgImagesEdits, request).await
}

async fn relay(
    state: MissingRelayMiscState,
    endpoint: MissingRelayEndpoint,
    request: Request,
) -> Response {
    match state.service.authorize(endpoint, &request).await {
        MissingRelayAuthorization::Authorized => state.service.relay(endpoint, request).await,
        MissingRelayAuthorization::Rejected(rejection) => {
            legacy_error(endpoint, rejection, &request)
        }
    }
}

fn legacy_error(
    endpoint: MissingRelayEndpoint,
    rejection: MissingRelayAuthRejection,
    request: &Request,
) -> Response {
    match endpoint {
        MissingRelayEndpoint::PgChatCompletions => {
            let mut response = (
                rejection.status,
                axum::Json(PgAuthError {
                    success: false,
                    code: rejection.code,
                    message: rejection.message,
                }),
            )
                .into_response();
            response.headers_mut().insert(
                header::CONTENT_TYPE,
                HeaderValue::from_static("application/json; charset=utf-8"),
            );
            response
        }
        MissingRelayEndpoint::Realtime
        | MissingRelayEndpoint::Edits
        | MissingRelayEndpoint::PgImagesGenerations
        | MissingRelayEndpoint::PgImagesEdits => {
            let request_id = request.extensions().get::<RequestContext>().map_or_else(
                || {
                    request
                        .headers()
                        .get("x-oneapi-request-id")
                        .and_then(|value| value.to_str().ok())
                        .unwrap_or("unknown")
                        .to_owned()
                },
                |context| context.request_id.clone(),
            );
            let mut response = (
                rejection.status,
                axum::Json(LegacyErrorEnvelope {
                    error: LegacyError {
                        message: format!("{} (request id: {request_id})", rejection.message),
                        kind: "new_api_error",
                        code: "",
                    },
                }),
            )
                .into_response();
            response.headers_mut().insert(
                header::CONTENT_TYPE,
                HeaderValue::from_static("application/json; charset=utf-8"),
            );
            response
        }
    }
}

#[derive(Serialize)]
struct LegacyErrorEnvelope {
    error: LegacyError,
}

#[derive(Serialize)]
struct LegacyError {
    message: String,
    #[serde(rename = "type")]
    kind: &'static str,
    code: &'static str,
}

#[derive(Serialize)]
struct PgAuthError {
    success: bool,
    code: &'static str,
    message: String,
}

#[cfg(test)]
mod tests {
    use std::sync::{
        Arc,
        atomic::{AtomicUsize, Ordering},
    };

    use super::*;
    use axum::{
        body::Body,
        http::{HeaderValue, Request as HttpRequest, header},
    };
    use tower::ServiceExt;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    fn lock_unpoisoned<T>(mutex: &std::sync::Mutex<T>) -> std::sync::MutexGuard<'_, T> {
        match mutex.lock() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        }
    }

    #[derive(Clone)]
    struct TestService {
        authorization: MissingRelayAuthorization,
        seen: Arc<std::sync::Mutex<Vec<MissingRelayEndpoint>>>,
        relays: Arc<AtomicUsize>,
        relay_status: StatusCode,
        relay_body: Option<String>,
        relay_header: HeaderValue,
    }

    #[async_trait]
    impl MissingRelayService for TestService {
        async fn authorize(
            &self,
            endpoint: MissingRelayEndpoint,
            _: &Request,
        ) -> MissingRelayAuthorization {
            lock_unpoisoned(&self.seen).push(endpoint);
            self.authorization.clone()
        }

        async fn relay(&self, endpoint: MissingRelayEndpoint, request: Request) -> Response {
            lock_unpoisoned(&self.seen).push(endpoint);
            self.relays.fetch_add(1, Ordering::SeqCst);
            let body = self
                .relay_body
                .clone()
                .unwrap_or_else(|| format!("{} {}", request.method(), request.uri()));
            let mut response = Response::new(Body::from(body));
            *response.status_mut() = self.relay_status;
            response
                .headers_mut()
                .insert("x-upstream-id", self.relay_header.clone());
            response
        }
    }

    fn app(
        authorization: MissingRelayAuthorization,
    ) -> (
        Router,
        Arc<std::sync::Mutex<Vec<MissingRelayEndpoint>>>,
        Arc<AtomicUsize>,
    ) {
        app_with_relay(
            authorization,
            StatusCode::OK,
            None,
            HeaderValue::from_static("relay-1"),
        )
    }

    fn app_with_relay(
        authorization: MissingRelayAuthorization,
        relay_status: StatusCode,
        relay_body: Option<String>,
        relay_header: HeaderValue,
    ) -> (
        Router,
        Arc<std::sync::Mutex<Vec<MissingRelayEndpoint>>>,
        Arc<AtomicUsize>,
    ) {
        let seen = Arc::new(std::sync::Mutex::new(Vec::new()));
        let relays = Arc::new(AtomicUsize::new(0));
        let service = TestService {
            authorization,
            seen: Arc::clone(&seen),
            relays: Arc::clone(&relays),
            relay_status,
            relay_body,
            relay_header,
        };
        (
            missing_relay_misc_router(MissingRelayMiscState::new(Arc::new(service))),
            seen,
            relays,
        )
    }

    fn rejected(
        status: StatusCode,
        code: &'static str,
        message: &str,
    ) -> MissingRelayAuthorization {
        MissingRelayAuthorization::Rejected(MissingRelayAuthRejection {
            status,
            code,
            message: message.to_owned(),
        })
    }

    async fn assert_json_response(
        response: Response,
        status: StatusCode,
        expected_body: &str,
    ) -> TestResult {
        assert_eq!(response.status(), status);
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "application/json; charset=utf-8"
        );
        let body = axum::body::to_bytes(response.into_body(), usize::MAX).await?;
        assert_eq!(body, expected_body.as_bytes());
        Ok(())
    }

    #[tokio::test]
    async fn all_public_paths_preserve_method_path_and_selected_endpoint() -> TestResult {
        let cases = [
            (
                "GET",
                "/v1/realtime?model=gpt",
                MissingRelayEndpoint::Realtime,
            ),
            (
                "POST",
                "/pg/chat/completions",
                MissingRelayEndpoint::PgChatCompletions,
            ),
            ("POST", "/v1/edits", MissingRelayEndpoint::Edits),
        ];
        for (method, uri, endpoint) in cases {
            let (router, seen, _) = app(MissingRelayAuthorization::Authorized);
            let request = HttpRequest::builder()
                .method(method)
                .uri(uri)
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(r#"{"unknown_provider_field":true}"#))?;
            let response = router.oneshot(request).await?;
            assert_eq!(response.status(), StatusCode::OK);
            assert_eq!(response.headers()["x-upstream-id"], "relay-1");
            let body = axum::body::to_bytes(response.into_body(), usize::MAX).await?;
            assert!(std::str::from_utf8(&body)?.contains(uri));
            assert_eq!(*lock_unpoisoned(&seen), vec![endpoint, endpoint]);
        }
        Ok(())
    }

    #[tokio::test]
    async fn failed_authorization_is_fail_closed_and_never_calls_upstream() -> TestResult {
        let (router, seen, relays) = app(rejected(
            StatusCode::UNAUTHORIZED,
            "AUTH_UNAUTHORIZED",
            "invalid API key",
        ));
        let mut request = HttpRequest::post("/v1/edits").body(Body::from("{}"))?;
        request.extensions_mut().insert(RequestContext {
            request_id: "edits-auth-request-id".into(),
            client_ip: None,
        });
        let response = router.oneshot(request).await?;
        assert_eq!(relays.load(Ordering::SeqCst), 0);
        assert_eq!(*lock_unpoisoned(&seen), vec![MissingRelayEndpoint::Edits]);
        assert_json_response(
            response,
            StatusCode::UNAUTHORIZED,
            r#"{"error":{"message":"invalid API key (request id: edits-auth-request-id)","type":"new_api_error","code":""}}"#,
        )
        .await?;
        Ok(())
    }

    #[tokio::test]
    async fn pg_rejection_serializes_typed_dashboard_auth_without_request_id() -> TestResult {
        let cases = [
            (
                StatusCode::UNAUTHORIZED,
                "AUTH_TOKEN_EXPIRED",
                "Token expired",
                r#"{"success":false,"code":"AUTH_TOKEN_EXPIRED","message":"Token expired"}"#,
            ),
            (
                StatusCode::FORBIDDEN,
                "AUTH_INSUFFICIENT_PRIVILEGE",
                "Insufficient privileges",
                r#"{"success":false,"code":"AUTH_INSUFFICIENT_PRIVILEGE","message":"Insufficient privileges"}"#,
            ),
            (
                StatusCode::INTERNAL_SERVER_ERROR,
                "AUTH_INTERNAL_ERROR",
                "Internal authorization error",
                r#"{"success":false,"code":"AUTH_INTERNAL_ERROR","message":"Internal authorization error"}"#,
            ),
        ];

        for (status, code, message, expected_body) in cases {
            let (router, _, relays) = app(rejected(status, code, message));
            let mut request = HttpRequest::post("/pg/chat/completions").body(Body::empty())?;
            request.extensions_mut().insert(RequestContext {
                request_id: "pg-request-id".into(),
                client_ip: None,
            });

            let response = router.oneshot(request).await?;
            assert_json_response(response, status, expected_body).await?;
            assert_eq!(relays.load(Ordering::SeqCst), 0);
        }
        Ok(())
    }

    #[tokio::test]
    async fn realtime_and_edits_keep_openai_token_auth_shape() -> TestResult {
        let cases = [
            (
                "GET",
                "/v1/realtime",
                r#"{"error":{"message":"Invalid token (request id: anonymous-request-id)","type":"new_api_error","code":""}}"#,
            ),
            (
                "POST",
                "/v1/edits",
                r#"{"error":{"message":"Invalid token (request id: anonymous-request-id)","type":"new_api_error","code":""}}"#,
            ),
        ];
        for (method, path, expected_body) in cases {
            let (router, _, _) = app(rejected(
                StatusCode::UNAUTHORIZED,
                "AUTH_UNAUTHORIZED",
                "Invalid token",
            ));
            let mut request = HttpRequest::builder()
                .method(method)
                .uri(path)
                .body(Body::empty())?;
            request.extensions_mut().insert(RequestContext {
                request_id: "anonymous-request-id".into(),
                client_ip: None,
            });
            let response = router.oneshot(request).await?;
            assert_json_response(response, StatusCode::UNAUTHORIZED, expected_body).await?;
        }
        Ok(())
    }

    #[tokio::test]
    async fn authorized_relay_preserves_provider_response_opaque() -> TestResult {
        let (router, _, relays) = app_with_relay(
            MissingRelayAuthorization::Authorized,
            StatusCode::TOO_MANY_REQUESTS,
            Some(r#"{"provider":"rate_limited"}"#.to_owned()),
            HeaderValue::from_static("provider-limit"),
        );
        let request = HttpRequest::post("/pg/chat/completions").body(Body::empty())?;
        let response = router.oneshot(request).await?;
        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(response.headers()["x-upstream-id"], "provider-limit");
        assert_eq!(
            axum::body::to_bytes(response.into_body(), usize::MAX).await?,
            &br#"{"provider":"rate_limited"}"#[..]
        );
        assert_eq!(relays.load(Ordering::SeqCst), 1);
        Ok(())
    }

    #[tokio::test]
    async fn wrong_methods_and_unknown_paths_do_not_relay() -> TestResult {
        let (router, seen, relays) = app(MissingRelayAuthorization::Authorized);
        for (method, path, expected) in [
            ("POST", "/v1/realtime", StatusCode::METHOD_NOT_ALLOWED),
            ("GET", "/v1/edits", StatusCode::METHOD_NOT_ALLOWED),
            ("POST", "/v1/not-a-route", StatusCode::NOT_FOUND),
        ] {
            let request = HttpRequest::builder()
                .method(method)
                .uri(path)
                .body(Body::empty())?;
            let response = router.clone().oneshot(request).await?;
            assert_eq!(response.status(), expected, "{method} {path}");
        }
        assert!(lock_unpoisoned(&seen).is_empty());
        assert_eq!(relays.load(Ordering::SeqCst), 0);
        Ok(())
    }
}
