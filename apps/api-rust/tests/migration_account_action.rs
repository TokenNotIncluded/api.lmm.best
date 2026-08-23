use std::sync::Arc;

use axum::{body::Body, http::Request};
use lmm_api_rs::{
    auth::{AuthConfig, PgValkeyDashboardAuth},
    migration_routes::account_action::{AccountActionState, router},
};
use secrecy::SecretString;
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

#[tokio::test]
async fn account_action_routes_require_dashboard_auth() {
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
                    "account-action-route-test-secret-012345678901234567890123456789",
                ),
                ..AuthConfig::default()
            },
        )
        .expect("route-test auth adapter"),
    );
    let app = router(AccountActionState::new(
        pg,
        valkey,
        auth,
        SecretString::from("account-action-route-test-secret-012345678901234567890123456789"),
    ));

    let response = app
        .oneshot(
            Request::get("/api/account-action-requests")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert!(matches!(
        response.status(),
        axum::http::StatusCode::UNAUTHORIZED | axum::http::StatusCode::FORBIDDEN | axum::http::StatusCode::NOT_FOUND
    ));
}
