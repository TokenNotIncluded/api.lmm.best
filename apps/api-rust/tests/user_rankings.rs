use std::sync::Arc;

use axum::{body::Body, http::Request};
use lmm_api_rs::{
    auth::{AuthConfig, PgValkeyDashboardAuth},
    routes::user_rankings::{UserRankingsState, router},
};
use secrecy::SecretString;
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

#[tokio::test]
async fn user_rankings_conceal_anonymous_discovery_before_database_access() {
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
                    "user-rankings-route-test-secret-012345678901234567890123456789",
                ),
                ..AuthConfig::default()
            },
        )
        .expect("route-test auth adapter"),
    );
    let app = router(UserRankingsState::new(pg, auth));

    let response = app
        .oneshot(
            Request::get("/api/rankings/users")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), axum::http::StatusCode::NOT_FOUND);
}
