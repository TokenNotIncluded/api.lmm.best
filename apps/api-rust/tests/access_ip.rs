use std::{net::SocketAddr, sync::Arc};

use axum::{
    body::Body,
    extract::ConnectInfo,
    http::{Request, StatusCode, header},
};
use lmm_api_rs::{
    auth::{AuthConfig, PgValkeyDashboardAuth},
    routes::access_ip::{AccessIpState, router},
};
use secrecy::SecretString;
use serde_json::Value;
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

fn route() -> axum::Router {
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
                    "access-ip-route-test-secret-0123456789012345678901234567890123",
                ),
                ..AuthConfig::default()
            },
        )
        .expect("route-test auth adapter"),
    );
    router(AccessIpState::new(pg, auth))
}

#[tokio::test]
async fn user_get_requires_auth_before_database_access() {
    let response = route()
        .oneshot(
            Request::get("/api/user/access-ip")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert!(response.headers().get(header::CACHE_CONTROL).is_none());
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("failure envelope")["code"],
        "AUTH_UNAUTHORIZED"
    );
}

#[tokio::test]
async fn loopback_non_cn_policy_allows_anonymous_without_database_access() {
    let mut request = Request::get("/api/internal/access-ip-policy")
        .body(Body::empty())
        .expect("route request");
    request.extensions_mut().insert(ConnectInfo(
        "127.0.0.1:48200"
            .parse::<SocketAddr>()
            .expect("loopback peer"),
    ));

    let response = route().oneshot(request).await.expect("route response");

    assert_eq!(response.status(), StatusCode::NO_CONTENT);
    assert_eq!(
        response.headers()[header::CACHE_CONTROL],
        "no-store, no-cache, must-revalidate, private, max-age=0"
    );
    assert!(response.headers().get("x-lmm-access-policy").is_none());
}

#[tokio::test]
async fn non_loopback_policy_is_denied_and_marked() {
    let mut request = Request::get("/api/internal/access-ip-policy")
        .body(Body::empty())
        .expect("route request");
    request.extensions_mut().insert(ConnectInfo(
        "198.51.100.20:48200"
            .parse::<SocketAddr>()
            .expect("remote peer"),
    ));

    let response = route().oneshot(request).await.expect("route response");

    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert_eq!(response.headers()["x-lmm-access-policy"], "denied");
    assert_eq!(
        response.headers()[header::CACHE_CONTROL],
        "no-store, no-cache, must-revalidate, private, max-age=0"
    );
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("failure envelope")["code"],
        "INTERNAL_ONLY"
    );
}
