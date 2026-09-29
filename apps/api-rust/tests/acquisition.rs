use std::sync::Arc;

use axum::{
    body::Body,
    http::{Request, StatusCode, header},
};
use lmm_api_rs::{
    auth::{AuthConfig, PgValkeyDashboardAuth},
    routes::acquisition::{AcquisitionState, PgAcquisitionStore, router},
};
use secrecy::SecretString;
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

fn app() -> axum::Router {
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
                    "acquisition-route-test-secret-012345678901234567890123456789",
                ),
                ..AuthConfig::default()
            },
        )
        .expect("route-test auth adapter"),
    );

    router(AcquisitionState::new(
        PgAcquisitionStore::new(pg),
        auth,
        false,
    ))
}

#[tokio::test]
async fn protected_acquisition_routes_reject_missing_dashboard_auth_before_storage() {
    let app = app();
    for (method, path) in [
        ("POST", "/api/acquisition/consent"),
        ("GET", "/api/acquisition/self-report"),
        ("GET", "/api/admin/acquisition/links"),
    ] {
        let response = app
            .clone()
            .oneshot(
                Request::builder()
                    .method(method)
                    .uri(path)
                    .body(Body::empty())
                    .expect("route request"),
            )
            .await
            .expect("route response");

        assert_eq!(
            response.status(),
            StatusCode::UNAUTHORIZED,
            "{method} {path}"
        );
    }
}

#[tokio::test]
async fn public_visit_fails_closed_without_storage_for_a_malformed_body() {
    let response = app()
        .oneshot(
            Request::post("/api/acquisition/visit")
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from("{"))
                .expect("visit request"),
        )
        .await
        .expect("visit response");

    assert_eq!(response.status(), StatusCode::NO_CONTENT);
    assert_eq!(
        response
            .headers()
            .get(header::CACHE_CONTROL)
            .and_then(|value| value.to_str().ok()),
        Some("no-store, no-cache, must-revalidate, private, max-age=0")
    );
}
