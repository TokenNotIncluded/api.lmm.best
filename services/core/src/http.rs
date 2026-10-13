use axum::{
    Json, Router,
    body::{Body, Bytes},
    extract::{Request, State},
    http::StatusCode,
    middleware::Next,
    response::Response,
    routing::get,
};
use http_body::{Body as HttpBody, Frame, SizeHint};
use serde::Serialize;
use serde_json::{Value, json};
use sqlx::PgPool;
use std::{
    future::Future,
    io,
    pin::Pin,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    task::{Context, Poll},
    time::Duration,
};

/// Bound only incoming body reads. A slow client cannot hold graceful shutdown
/// indefinitely, while accepted handlers and financial finalizers have no
/// blanket response timeout.
pub async fn request_body_deadline(request: Request, next: Next) -> Response {
    let (parts, body) = request.into_parts();
    next.run(Request::from_parts(
        parts,
        Body::new(DeadlineBody::new(body, Duration::from_secs(10))),
    ))
    .await
}

struct DeadlineBody {
    inner: Pin<Box<Body>>,
    deadline: Pin<Box<tokio::time::Sleep>>,
    finished: bool,
}
impl DeadlineBody {
    fn new(body: Body, maximum: Duration) -> Self {
        Self {
            inner: Box::pin(body),
            deadline: Box::pin(tokio::time::sleep(maximum)),
            finished: false,
        }
    }
}
impl HttpBody for DeadlineBody {
    type Data = Bytes;
    type Error = axum::Error;
    fn poll_frame(
        mut self: Pin<&mut Self>,
        context: &mut Context<'_>,
    ) -> Poll<Option<Result<Frame<Bytes>, axum::Error>>> {
        if self.finished {
            return Poll::Ready(None);
        }
        if self.deadline.as_mut().poll(context).is_ready() {
            self.finished = true;
            return Poll::Ready(Some(Err(axum::Error::new(io::Error::new(
                io::ErrorKind::TimedOut,
                "request body deadline exceeded",
            )))));
        }
        let result = self.inner.as_mut().poll_frame(context);
        if matches!(&result, Poll::Ready(None | Some(Err(_)))) {
            self.finished = true;
        }
        result
    }
    fn is_end_stream(&self) -> bool {
        self.finished || self.inner.is_end_stream()
    }
    fn size_hint(&self) -> SizeHint {
        if self.finished {
            SizeHint::with_exact(0)
        } else {
            self.inner.size_hint()
        }
    }
}

/// Explicit capability facts, not a claim that the main application is migrated.
#[derive(Clone, Copy, Default, Serialize)]
pub struct Capabilities {
    pub identity: bool,
    pub legacy_web_auth: bool,
    pub console: bool,
    pub models: bool,
    pub models_streaming: bool,
    pub events: bool,
    pub payments: bool,
    pub commerce: bool,
}

#[derive(Clone, Default)]
pub struct PublicStatus {
    pub password_login_enabled: bool,
    pub password_register_enabled: bool,
    pub user_agreement_enabled: bool,
    pub privacy_policy_enabled: bool,
    /// Only set after the browser callback, origin and cookie bridge match.
    /// A client ID is public; provider secrets never enter this projection.
    pub google_client_id: Option<String>,
}

#[derive(Clone)]
struct Readiness {
    pool: PgPool,
    capabilities: Capabilities,
    rpc_running: Option<Arc<AtomicBool>>,
    models_healthy: Option<Arc<AtomicBool>>,
    public_status: PublicStatus,
}

/// Called only after the complete installed schema and every enabled component
/// have been checked. Database loss makes readiness fail, without calling Go.
pub fn configured_router(pool: PgPool, capabilities: Capabilities) -> Router {
    configured_router_with_rpc(pool, capabilities, None, PublicStatus::default())
}

pub fn configured_router_with_rpc(
    pool: PgPool,
    capabilities: Capabilities,
    rpc_running: Option<Arc<AtomicBool>>,
    public_status: PublicStatus,
) -> Router {
    configured_router_with_runtime_status(pool, capabilities, rpc_running, None, public_status)
}

pub fn configured_router_with_runtime_status(
    pool: PgPool,
    capabilities: Capabilities,
    rpc_running: Option<Arc<AtomicBool>>,
    models_healthy: Option<Arc<AtomicBool>>,
    public_status: PublicStatus,
) -> Router {
    let usage = if capabilities.identity {
        crate::usage::router(crate::usage::UsageStore::new(pool.clone()))
    } else {
        Router::new()
    };
    Router::new()
        .route("/health/live", get(live))
        .route("/health/ready", get(ready))
        .route("/health/model-ready", get(model_ready))
        .route("/api/status", get(public_status_response))
        .fallback(unavailable)
        .with_state(Readiness {
            pool,
            capabilities,
            rpc_running,
            models_healthy,
            public_status,
        })
        .merge(usage)
}

async fn public_status_response(
    State(state): State<Readiness>,
) -> ([(String, String); 1], Json<Value>) {
    let status = state.public_status;
    let google_client_id = status
        .google_client_id
        .as_deref()
        .filter(|id| !id.trim().is_empty())
        .unwrap_or("");
    let google_enabled = !google_client_id.is_empty();
    (
        [("cache-control".into(), "no-store".into())],
        Json(json!({"success":true,"message":"","data":{
            "system_name":"LMM API","version":env!("CARGO_PKG_VERSION"),
            "password_login_enabled":status.password_login_enabled,
            "register_enabled":status.password_register_enabled,
            "password_register_enabled":status.password_register_enabled,
            "user_agreement_enabled":status.user_agreement_enabled,
            "privacy_policy_enabled":status.privacy_policy_enabled,
            "google_oauth":google_enabled,"google_client_id":google_client_id,
            "oauth_register_enabled":google_enabled && status.password_register_enabled,
            "github_oauth":false,"discord_oauth":false,
            "oidc_enabled":false,"linuxdo_oauth":false,"telegram_oauth":false,
            "wechat_login":false,"passkey_login":false,"email_verification":false,
            "turnstile_check":false,"two_factor_enabled":false,"payments_enabled":false,
            "payments_test_rpc_enabled":state.capabilities.payments,
            "commerce_rpc_enabled":state.capabilities.commerce,
            "assistant":{"enabled":false},"custom_oauth_providers":[],
            "backend_capabilities":{"bounty_notifications":false,"bounty_challenge_cancel":false,
                "bounty_public_read":false,"self_oauth_unbind":false,"responses_websocket":false},
            "configured_components":state.capabilities,
            "capability_scope":"configured_components_only",
            "models_local_synthetic":state.capabilities.models,
            "models_streaming":state.capabilities.models_streaming,
            "external_model_providers_enabled":false
        }})),
    )
}

async fn live() -> Json<Value> {
    Json(json!({"status": "alive", "service": "lmm-core"}))
}

async fn readiness(
    state: Readiness,
    require_models: bool,
) -> (StatusCode, [(String, String); 1], Json<Value>) {
    let database = tokio::time::timeout(
        Duration::from_millis(400),
        sqlx::query("SELECT 1").execute(&state.pool),
    )
    .await
    .is_ok_and(|result| result.is_ok());
    let mut capabilities = state.capabilities;
    let rpc_healthy = state
        .rpc_running
        .as_ref()
        .is_none_or(|running| running.load(Ordering::Acquire));
    capabilities.events &= rpc_healthy;
    capabilities.payments &= rpc_healthy;
    capabilities.commerce &= rpc_healthy;
    let models_healthy = state
        .models_healthy
        .as_ref()
        .is_none_or(|healthy| healthy.load(Ordering::Acquire));
    capabilities.models &= models_healthy;
    capabilities.models_streaming &= models_healthy;
    let ready = database
        && rpc_healthy
        && models_healthy
        && capabilities.identity
        && (!require_models || capabilities.models);
    (
        if ready {
            StatusCode::OK
        } else {
            StatusCode::SERVICE_UNAVAILABLE
        },
        [("cache-control".into(), "no-store".into())],
        Json(
            json!({"status":if ready {"ready"}else{"unavailable"},"service":"lmm-core",
            "database":database,"capabilities":capabilities,
            "readiness_scope":"configured_components","upstream_health_checked":false}),
        ),
    )
}

async fn ready(State(state): State<Readiness>) -> (StatusCode, [(String, String); 1], Json<Value>) {
    readiness(state, false).await
}

async fn model_ready(
    State(state): State<Readiness>,
) -> (StatusCode, [(String, String); 1], Json<Value>) {
    readiness(state, true).await
}

pub fn router() -> Router {
    Router::new()
        .route("/health/live", get(live))
        .route("/health/ready", get(unavailable))
        .fallback(unavailable)
}

async fn unavailable() -> (StatusCode, [(String, String); 1], Json<Value>) {
    // No environment variable can turn an incomplete ledger into a ready core.
    // Never forward an uncharged request, silently call Go, or fake a balance.
    (
        StatusCode::SERVICE_UNAVAILABLE,
        [("cache-control".into(), "no-store".into())],
        Json(
            json!({"error": {"code": "core_migration_incomplete", "message": "Core billing, identity and forwarding are not enabled. Keep production on the existing Go service."}}),
        ),
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{body::Body, http::Request};
    use tower::ServiceExt;

    #[tokio::test]
    async fn process_health_does_not_advertise_billing_readiness() {
        for (path, status) in [
            ("/health/live", StatusCode::OK),
            ("/health/ready", StatusCode::SERVICE_UNAVAILABLE),
            ("/v1/chat/completions", StatusCode::SERVICE_UNAVAILABLE),
            ("/api/user/self", StatusCode::SERVICE_UNAVAILABLE),
        ] {
            let response = router()
                .oneshot(Request::builder().uri(path).body(Body::empty()).unwrap())
                .await
                .unwrap();
            assert_eq!(response.status(), status, "{path}");
        }
    }

    #[tokio::test]
    async fn model_calls_fail_before_any_upstream_or_extension_work() {
        let response = router()
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/v1/responses")
                    .header("authorization", "Bearer a-test-key")
                    .body(Body::from(r#"{"model":"test","stream":true}"#))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(response.headers()["cache-control"], "no-store");
    }

    #[tokio::test]
    async fn configured_readiness_fails_when_database_is_unavailable() {
        let pool = sqlx::postgres::PgPoolOptions::new()
            .connect_lazy("postgresql://synthetic:synthetic@127.0.0.1:1/synthetic")
            .unwrap();
        let app = configured_router(
            pool,
            Capabilities {
                identity: true,
                ..Capabilities::default()
            },
        );
        for path in ["/health/ready", "/health/model-ready"] {
            let response = app
                .clone()
                .oneshot(Request::builder().uri(path).body(Body::empty()).unwrap())
                .await
                .unwrap();
            assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
            assert_eq!(response.headers()["cache-control"], "no-store");
        }
    }

    #[tokio::test]
    async fn google_public_status_is_positive_only_for_the_browser_projection() {
        // Status is a pure projection; this pool is lazy and never connected.
        let pool = sqlx::postgres::PgPoolOptions::new()
            .connect_lazy("postgres://unused@127.0.0.1:1/unused")
            .unwrap();
        for (client_id, registration, enabled) in [
            (None, true, false),
            (Some(" "), true, false),
            (Some("public-client.apps.googleusercontent.com"), true, true),
            (
                Some("public-client.apps.googleusercontent.com"),
                false,
                true,
            ),
        ] {
            let router = configured_router_with_rpc(
                pool.clone(),
                Capabilities::default(),
                None,
                PublicStatus {
                    google_client_id: client_id.map(str::to_owned),
                    password_register_enabled: registration,
                    ..PublicStatus::default()
                },
            );
            let response = router
                .oneshot(
                    Request::builder()
                        .uri("/api/status")
                        .body(Body::empty())
                        .unwrap(),
                )
                .await
                .unwrap();
            assert_eq!(response.status(), StatusCode::OK);
            assert_eq!(response.headers()["cache-control"], "no-store");
            let bytes = axum::body::to_bytes(response.into_body(), 65536)
                .await
                .unwrap();
            let body: Value = serde_json::from_slice(&bytes).unwrap();
            let data = &body["data"];
            assert_eq!(data["google_oauth"], enabled);
            assert_eq!(data["oauth_register_enabled"], enabled && registration);
            assert_eq!(
                data["google_client_id"],
                if enabled { client_id.unwrap() } else { "" }
            );
            assert!(data.get("google_client_secret").is_none());
            assert!(data.get("google_client_secret_file").is_none());
            assert_eq!(data["custom_oauth_providers"], json!([]));
        }
    }

    #[tokio::test]
    async fn input_body_deadline_rejects_stalls_but_does_not_timeout_handlers() {
        let stalled = Body::from_stream(tokio_stream::pending::<Result<Bytes, io::Error>>());
        let body = Body::new(DeadlineBody::new(stalled, Duration::from_millis(10)));
        assert!(axum::body::to_bytes(body, 128).await.is_err());
        let router = Router::new()
            .route(
                "/",
                axum::routing::post(|body: Bytes| async move {
                    tokio::time::sleep(Duration::from_millis(40)).await;
                    (StatusCode::OK, body)
                }),
            )
            .layer(axum::middleware::from_fn(
                |request: Request<Body>, next: Next| async move {
                    let (parts, body) = request.into_parts();
                    next.run(Request::from_parts(
                        parts,
                        Body::new(DeadlineBody::new(body, Duration::from_millis(10))),
                    ))
                    .await
                },
            ));
        let response = router
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/")
                    .body(Body::from("complete"))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            axum::body::to_bytes(response.into_body(), 128)
                .await
                .unwrap(),
            "complete"
        );
    }
}
