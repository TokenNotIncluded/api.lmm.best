use std::sync::Arc;

use axum::{body::Body, http::Request};
use lmm_api_rs::{
    auth::{AuthConfig, PgValkeyDashboardAuth},
    migration_routes::discount_code::{DiscountCodeState, router},
};
use secrecy::SecretString;
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

#[tokio::test]
async fn discount_code_routes_require_dashboard_auth() {
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
                    "discount-code-route-test-secret-012345678901234567890123456789",
                ),
                ..AuthConfig::default()
            },
        )
        .expect("route-test auth adapter"),
    );
    let app = router(DiscountCodeState::new(pg, auth));

    let response = app
        .oneshot(
            Request::get("/api/discount-code/search")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), axum::http::StatusCode::UNAUTHORIZED);
}
