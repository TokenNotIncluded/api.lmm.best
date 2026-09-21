use std::sync::Arc;

use axum::{body::Body, http::Request};
use lmm_api_rs::{
    auth::{AuthConfig, PgValkeyDashboardAuth},
    routes::open_source_bounties::{OpenSourceBountyState, router},
};
use secrecy::SecretString;
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

#[tokio::test]
async fn public_bounty_write_requires_dashboard_auth_before_draft_validation() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://127.0.0.1:1").expect("lazy Valkey client");
    let auth_config = AuthConfig {
        session_secret: SecretString::from(
            "open-source-bounty-route-test-secret-012345678901234567890123456789",
        ),
        ..AuthConfig::default()
    };
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(pg.clone(), valkey, auth_config)
            .expect("route-test auth adapter"),
    );
    let app = router(OpenSourceBountyState::new(pg, auth));

    let response = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/open-source-bounties")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), axum::http::StatusCode::UNAUTHORIZED);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(&body).expect("failure envelope")["code"],
        "AUTH_UNAUTHORIZED"
    );
}

#[tokio::test]
async fn public_bounty_detail_rejects_an_invalid_project_id_without_database_access() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://127.0.0.1:1").expect("lazy Valkey client");
    let auth_config = AuthConfig {
        session_secret: SecretString::from(
            "open-source-bounty-detail-route-test-secret-012345678901234567890123456789",
        ),
        ..AuthConfig::default()
    };
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(pg.clone(), valkey, auth_config)
            .expect("route-test auth adapter"),
    );
    let app = router(OpenSourceBountyState::new(pg, auth));

    let response = app
        .oneshot(
            Request::get("/api/open-source-bounties/projects/not-an-id")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), axum::http::StatusCode::OK);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(&body).expect("failure envelope"),
        serde_json::json!({
            "success": false,
            "code": "OPEN_SOURCE_BOUNTY_INVALID_ID",
            "message": "invalid open-source bounty identifier"
        })
    );
}

#[tokio::test]
async fn bounty_config_requires_dashboard_auth_before_options_lookup() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://127.0.0.1:1").expect("lazy Valkey client");
    let auth_config = AuthConfig {
        session_secret: SecretString::from(
            "open-source-bounty-config-route-test-secret-012345678901234567890123456789",
        ),
        ..AuthConfig::default()
    };
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(pg.clone(), valkey, auth_config)
            .expect("route-test auth adapter"),
    );
    let app = router(OpenSourceBountyState::new(pg, auth));

    let response = app
        .oneshot(
            Request::get("/api/open-source-bounties/config")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), axum::http::StatusCode::UNAUTHORIZED);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(&body).expect("failure envelope")["code"],
        "AUTH_UNAUTHORIZED"
    );
}

#[tokio::test]
async fn owned_bounty_list_requires_dashboard_auth_before_project_lookup() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://127.0.0.1:1").expect("lazy Valkey client");
    let auth_config = AuthConfig {
        session_secret: SecretString::from(
            "open-source-bounty-mine-route-test-secret-012345678901234567890123456789",
        ),
        ..AuthConfig::default()
    };
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(pg.clone(), valkey, auth_config)
            .expect("route-test auth adapter"),
    );
    let app = router(OpenSourceBountyState::new(pg, auth));

    let response = app
        .oneshot(
            Request::get("/api/open-source-bounties/mine")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), axum::http::StatusCode::UNAUTHORIZED);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(&body).expect("failure envelope")["code"],
        "AUTH_UNAUTHORIZED"
    );
}

#[tokio::test]
async fn accepted_bounty_list_requires_dashboard_auth_before_challenge_lookup() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://127.0.0.1:1").expect("lazy Valkey client");
    let auth_config = AuthConfig {
        session_secret: SecretString::from(
            "open-source-bounty-accepted-route-test-secret-012345678901234567890123456789",
        ),
        ..AuthConfig::default()
    };
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(pg.clone(), valkey, auth_config)
            .expect("route-test auth adapter"),
    );
    let app = router(OpenSourceBountyState::new(pg, auth));

    let response = app
        .oneshot(
            Request::get("/api/open-source-bounties/accepted")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), axum::http::StatusCode::UNAUTHORIZED);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(&body).expect("failure envelope")["code"],
        "AUTH_UNAUTHORIZED"
    );
}

#[tokio::test]
async fn owned_dispute_list_requires_dashboard_auth_before_dispute_lookup() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://127.0.0.1:1").expect("lazy Valkey client");
    let auth_config = AuthConfig {
        session_secret: SecretString::from(
            "open-source-bounty-disputes-route-test-secret-012345678901234567890123456789",
        ),
        ..AuthConfig::default()
    };
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(pg.clone(), valkey, auth_config)
            .expect("route-test auth adapter"),
    );
    let app = router(OpenSourceBountyState::new(pg, auth));

    let response = app
        .oneshot(
            Request::get("/api/open-source-bounties/disputes/mine")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), axum::http::StatusCode::UNAUTHORIZED);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(&body).expect("failure envelope")["code"],
        "AUTH_UNAUTHORIZED"
    );
}

#[tokio::test]
async fn administrator_dispute_list_requires_dashboard_auth_before_dispute_lookup() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://route-test:route-test@127.0.0.1:1")
        .expect("lazy Valkey client");
    let auth_config = AuthConfig {
        session_secret: SecretString::from(
            "open-source-bounty-admin-disputes-route-test-secret-012345678901234567890123456789",
        ),
        ..AuthConfig::default()
    };
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(pg.clone(), valkey, auth_config)
            .expect("route-test auth adapter"),
    );
    let app = router(OpenSourceBountyState::new(pg, auth));

    let response = app
        .oneshot(
            Request::get("/api/open-source-bounties/disputes/admin?limit=1000")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), axum::http::StatusCode::UNAUTHORIZED);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(&body).expect("failure envelope")["code"],
        "AUTH_UNAUTHORIZED"
    );
}

#[tokio::test]
async fn mcp_token_status_requires_dashboard_auth_before_token_lookup() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://route-test:route-test@127.0.0.1:1")
        .expect("lazy Valkey client");
    let auth_config = AuthConfig {
        session_secret: SecretString::from(
            "open-source-bounty-mcp-token-status-route-test-secret-012345678901234567890123456789",
        ),
        ..AuthConfig::default()
    };
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(pg.clone(), valkey, auth_config)
            .expect("route-test auth adapter"),
    );
    let app = router(OpenSourceBountyState::new(pg, auth));

    let response = app
        .oneshot(
            Request::get("/api/open-source-bounties/mcp-token")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), axum::http::StatusCode::NOT_FOUND);
    assert_eq!(
        response
            .headers()
            .get(axum::http::header::CACHE_CONTROL)
            .and_then(|value| value.to_str().ok()),
        Some("no-store, no-cache, must-revalidate, private, max-age=0")
    );
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(&body).expect("failure envelope"),
        serde_json::json!({"message": "Not Found"})
    );
}

#[tokio::test]
async fn mcp_token_rotation_requires_dashboard_auth_before_token_write() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://route-test:route-test@127.0.0.1:1")
        .expect("lazy Valkey client");
    let auth_config = AuthConfig {
        session_secret: SecretString::from(
            "open-source-bounty-mcp-token-rotate-route-test-secret-012345678901234567890123456789",
        ),
        ..AuthConfig::default()
    };
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(pg.clone(), valkey, auth_config)
            .expect("route-test auth adapter"),
    );
    let app = router(OpenSourceBountyState::new(pg, auth));

    let response = app
        .oneshot(
            Request::post("/api/open-source-bounties/mcp-token")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), axum::http::StatusCode::NOT_FOUND);
    assert_eq!(
        response
            .headers()
            .get(axum::http::header::CACHE_CONTROL)
            .and_then(|value| value.to_str().ok()),
        Some("no-store, no-cache, must-revalidate, private, max-age=0")
    );
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(&body).expect("failure envelope"),
        serde_json::json!({"message": "Not Found"})
    );
}

#[tokio::test]
async fn mcp_token_revocation_requires_dashboard_auth_before_token_delete() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://route-test:route-test@127.0.0.1:1")
        .expect("lazy Valkey client");
    let auth_config = AuthConfig {
        session_secret: SecretString::from(
            "open-source-bounty-mcp-token-revoke-route-test-secret-012345678901234567890123456789",
        ),
        ..AuthConfig::default()
    };
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(pg.clone(), valkey, auth_config)
            .expect("route-test auth adapter"),
    );
    let app = router(OpenSourceBountyState::new(pg, auth));

    let response = app
        .oneshot(
            Request::delete("/api/open-source-bounties/mcp-token")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), axum::http::StatusCode::NOT_FOUND);
    assert_eq!(
        response
            .headers()
            .get(axum::http::header::CACHE_CONTROL)
            .and_then(|value| value.to_str().ok()),
        Some("no-store, no-cache, must-revalidate, private, max-age=0")
    );
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(&body).expect("failure envelope"),
        serde_json::json!({"message": "Not Found"})
    );
}

#[tokio::test]
async fn bounty_notification_routes_require_dashboard_auth_before_ledger_access() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://route-test:route-test@127.0.0.1:1")
        .expect("lazy Valkey client");
    let auth_config = AuthConfig {
        session_secret: SecretString::from(
            "open-source-bounty-notification-route-test-secret-012345678901234567890123456789",
        ),
        ..AuthConfig::default()
    };
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(pg.clone(), valkey, auth_config)
            .expect("route-test auth adapter"),
    );
    let app = router(OpenSourceBountyState::new(pg, auth));

    for (method, uri) in [
        ("GET", "/api/open-source-bounties/notifications"),
        ("POST", "/api/open-source-bounties/notifications/read"),
        ("GET", "/api/open-source-bounties/tips/received"),
        ("POST", "/api/open-source-bounties/tips/received/read"),
        ("POST", "/api/open-source-bounties/tips/17/thank"),
    ] {
        let response = app
            .clone()
            .oneshot(
                Request::builder()
                    .method(method)
                    .uri(uri)
                    .body(Body::empty())
                    .expect("route request"),
            )
            .await
            .expect("route response");

        assert_eq!(
            response.status(),
            axum::http::StatusCode::UNAUTHORIZED,
            "{method} {uri}"
        );
    }
}
