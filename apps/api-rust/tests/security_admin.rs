use std::sync::Arc;

use axum::{body::Body, http::Request};
use lmm_api_rs::{
    auth::{AuthConfig, PgValkeyDashboardAuth},
    routes::security_admin::{SecurityAdminState, router},
};
use secrecy::SecretString;
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

#[tokio::test]
async fn security_admin_routes_require_dashboard_auth() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://127.0.0.1:1").expect("lazy Valkey client");
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(
            pg.clone(),
            valkey,
            AuthConfig {
                session_secret: SecretString::from(
                    "security-admin-route-test-secret-012345678901234567890123456789012345678",
                ),
                ..AuthConfig::default()
            },
        )
        .expect("route-test auth adapter"),
    );
    let app = router(SecurityAdminState::new(pg, auth));

    let response = app
        .oneshot(
            Request::get("/api/security/admin/events")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), axum::http::StatusCode::NOT_FOUND);
}

#[tokio::test]
async fn review_run_cleanup_routes_require_dashboard_auth() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://127.0.0.1:1").expect("lazy Valkey client");
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(
            pg.clone(),
            valkey,
            AuthConfig {
                session_secret: SecretString::from(
                    "security-admin-cleanup-test-secret-0123456789012345678901234567890123456",
                ),
                ..AuthConfig::default()
            },
        )
        .expect("route-test auth adapter"),
    );

    for request in [
        Request::get("/api/security/admin/review-runs/cleanup-preview?keep=30")
            .body(Body::empty())
            .expect("preview request"),
        Request::delete("/api/security/admin/review-runs?keep=30")
            .body(Body::empty())
            .expect("cleanup request"),
    ] {
        let response = router(SecurityAdminState::new(pg.clone(), auth.clone()))
            .oneshot(request)
            .await
            .expect("route response");
        assert_eq!(response.status(), axum::http::StatusCode::UNAUTHORIZED);
    }
}
