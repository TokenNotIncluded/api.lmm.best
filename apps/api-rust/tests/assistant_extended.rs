use std::sync::Arc;

use axum::{body::Body, http::Request};
use lmm_api_rs::{
    auth::{AuthConfig, DashboardDeveloperAccessPolicy, PgValkeyDashboardAuth},
    routes::assistant::{AssistantRateLimitConfig, AssistantReadState},
    routes::assistant_extended::extended_router,
};
use secrecy::SecretString;
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

#[tokio::test]
async fn assistant_extended_routes_require_dashboard_auth() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://127.0.0.1:1").expect("lazy Valkey client");
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(
            pg.clone(),
            valkey.clone(),
            AuthConfig {
                session_secret: SecretString::from(
                    "assistant-extended-route-test-secret-012345678901234567890123456789",
                ),
                ..AuthConfig::default()
            },
        )
        .expect("route-test auth adapter"),
    );
    let state = AssistantReadState::new(
        pg,
        valkey,
        auth,
        SecretString::from("assistant-extended-route-test-secret-012345678901234567890123456789"),
        AssistantRateLimitConfig {
            enabled: false,
            max_requests: 1,
            window: std::time::Duration::from_secs(1),
            dependency_timeout: std::time::Duration::from_secs(1),
        },
        DashboardDeveloperAccessPolicy::new(false),
    );
    let app = extended_router().with_state(state);

    // `extended_router` is the production constructor under test.
    let _ = extended_router;

    let response = app
        .oneshot(
            Request::get("/api/assistant/conversations")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), axum::http::StatusCode::UNAUTHORIZED);
}
