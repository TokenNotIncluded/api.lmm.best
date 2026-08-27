use std::sync::Arc;

use axum::{body::Body, http::Request};
use lmm_api_rs::{
    auth::{AuthConfig, PgValkeyDashboardAuth},
    migration_routes::hero_sms::{DisabledHeroSmsGateway, HeroSmsState, router},
};
use secrecy::SecretString;
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

#[tokio::test]
async fn hero_sms_routes_require_dashboard_auth() {
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
                    "hero-sms-route-test-secret-012345678901234567890123456789012345678901234",
                ),
                ..AuthConfig::default()
            },
        )
        .expect("route-test auth adapter"),
    );
    let app = router(HeroSmsState::new(
        pg,
        auth,
        Arc::new(DisabledHeroSmsGateway),
    ));

    let routes = [
        ("GET", "/api/hero-sms/email/products"),
        ("GET", "/api/hero-sms/sms/countries"),
        ("GET", "/api/hero-sms/sms/services"),
        ("GET", "/api/hero-sms/sms/operators?country=0"),
        ("GET", "/api/hero-sms/sms/offer?country=0&service=tg"),
        ("POST", "/api/hero-sms/sms/orders"),
        ("GET", "/api/hero-sms/sms/orders"),
        ("GET", "/api/hero-sms/sms/orders/current"),
        ("GET", "/api/hero-sms/sms/orders/current-list"),
        ("DELETE", "/api/hero-sms/sms/history"),
        ("DELETE", "/api/hero-sms/sms/history/order-1"),
        ("GET", "/api/hero-sms/sms/orders/order-1"),
        ("POST", "/api/hero-sms/sms/orders/order-1/complaints"),
        ("POST", "/api/hero-sms/sms/orders/order-1/cancel"),
    ];

    for (method, path) in routes {
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
            axum::http::StatusCode::NOT_FOUND,
            "{method} {path}"
        );
        assert_eq!(
            response.headers().get(axum::http::header::CACHE_CONTROL),
            Some(&axum::http::HeaderValue::from_static("no-store")),
            "{method} {path}",
        );
    }
}
