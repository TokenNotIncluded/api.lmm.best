use std::{env, sync::Arc};

use async_trait::async_trait;
use axum::{Extension, Router, body::Body, http::Request};
use lmm_api_rs::routes::identity_2fa::{
    Identity2FAActor, Identity2FAReadState, Identity2FASession, Identity2FAState,
    SecuritySessionRotation, SecuritySessionRotator, router, status_read_router,
};
use serde_json::{Value, json};
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

struct UnusedRotator;

#[async_trait]
impl SecuritySessionRotator for UnusedRotator {
    async fn rotate_after_security_change(
        &self,
        _: Identity2FAActor,
        _: &Identity2FASession,
        _: &'static str,
        _: i64,
    ) -> Result<SecuritySessionRotation, String> {
        unreachable!("the no-database harness never reaches a session rotation")
    }
}

fn app() -> Router {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("valid lazy PostgreSQL URL");
    let valkey = redis::Client::open("redis://127.0.0.1/").expect("valid Valkey URL");
    router(Identity2FAState::new(pool, valkey, Arc::new(UnusedRotator))).layer(Extension(
        Identity2FAActor {
            user_id: 7,
            role: 1,
        },
    ))
}

fn app_without_actor() -> Router {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("valid lazy PostgreSQL URL");
    let valkey = redis::Client::open("redis://127.0.0.1/").expect("valid Valkey URL");
    router(Identity2FAState::new(pool, valkey, Arc::new(UnusedRotator)))
}

#[test]
fn status_read_router_constructor_is_wired() {
    let _: fn(Identity2FAReadState) -> Router = status_read_router;
}

#[tokio::test]
async fn twofa_status_rejects_a_request_without_listener_identity_before_postgres() {
    let response = app_without_actor()
        .oneshot(
            Request::get("/api/user/2fa/status")
                .body(Body::empty())
                .expect("valid request"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), axum::http::StatusCode::OK);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("failure body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("legacy failure envelope"),
        json!({"success": false, "message": "未登录或用户已被封禁"})
    );
}

#[tokio::test]
async fn twofa_enable_authenticates_before_parsing_a_malformed_request_body() {
    let response = app_without_actor()
        .oneshot(
            Request::post("/api/user/2fa/enable")
                .header("content-type", "application/json")
                .body(Body::from("{"))
                .expect("valid request"),
        )
        .await
        .expect("router responds");

    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("failure body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("legacy failure envelope"),
        json!({"success": false, "message": "未登录或用户已被封禁"})
    );
}

#[tokio::test]
async fn twofa_enable_refuses_to_select_a_session_from_request_json() {
    let response = app()
        .oneshot(
            Request::post("/api/user/2fa/enable")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"code":"123456","session_id":"attacker-selected"}"#,
                ))
                .expect("valid request"),
        )
        .await
        .expect("router responds");

    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("failure body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("legacy failure envelope"),
        json!({"success": false, "message": "服务器内部错误"})
    );
}

async fn spawn_tcp_router(app: Router) -> String {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind test listener");
    let address = listener.local_addr().expect("test listener address");
    tokio::spawn(async move {
        axum::serve(listener, app)
            .await
            .expect("test listener serves requests");
    });
    format!("http://{address}")
}

#[tokio::test]
async fn enable_malformed_json_matches_the_frozen_go_tcp_status_body_and_cache_headers() {
    let base_url = spawn_tcp_router(app()).await;
    let response = reqwest::Client::builder()
        .no_proxy()
        .build()
        .expect("test client")
        .post(format!("{base_url}/api/user/2fa/enable"))
        .header("content-type", "application/json")
        .body("{")
        .send()
        .await
        .expect("Rust TCP response");

    assert_eq!(response.status(), reqwest::StatusCode::OK);
    assert_eq!(
        response.headers()["content-type"],
        "application/json; charset=utf-8"
    );
    assert_eq!(
        response.headers()["cache-control"],
        "no-store, no-cache, must-revalidate, private, max-age=0"
    );
    assert_eq!(response.headers()["pragma"], "no-cache");
    assert_eq!(response.headers()["expires"], "0");
    assert_eq!(
        response
            .json::<Value>()
            .await
            .expect("legacy JSON envelope"),
        json!({"success": false, "message": "参数错误"})
    );
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18; set LMM_IDENTITY_TEST_DATABASE_URL and LMM_IDENTITY_TEST_ALLOW_SCHEMA_RESET=1"]
async fn twofa_enable_reports_an_existing_enabled_factor_from_postgres() {
    assert_eq!(
        env::var("LMM_IDENTITY_TEST_ALLOW_SCHEMA_RESET").as_deref(),
        Ok("1"),
        "integration test requires LMM_IDENTITY_TEST_ALLOW_SCHEMA_RESET=1"
    );
    let database_url =
        env::var("LMM_IDENTITY_TEST_DATABASE_URL").expect("isolated PostgreSQL 18 URL");
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database_url)
        .await
        .expect("connect isolated PostgreSQL");
    sqlx::query("DROP TABLE IF EXISTS two_fas, users CASCADE")
        .execute(&pool)
        .await
        .expect("reset isolated 2FA tables");
    sqlx::query(
        "CREATE TABLE users (id BIGINT PRIMARY KEY, deleted_at TIMESTAMPTZ, auth_version BIGINT)",
    )
    .execute(&pool)
    .await
    .expect("create users");
    sqlx::query("CREATE TABLE two_fas (id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL, secret TEXT NOT NULL, is_enabled BOOLEAN NOT NULL, failed_attempts BIGINT, locked_until TIMESTAMPTZ, deleted_at TIMESTAMPTZ)")
        .execute(&pool)
        .await
        .expect("create two factors");
    sqlx::query("INSERT INTO users (id, auth_version) VALUES (7, 1)")
        .execute(&pool)
        .await
        .expect("seed user");
    sqlx::query("INSERT INTO two_fas (user_id, secret, is_enabled, failed_attempts) VALUES (7, 'JBSWY3DPEHPK3PXP', TRUE, 0)")
        .execute(&pool)
        .await
        .expect("seed enabled factor");
    let valkey = redis::Client::open("redis://127.0.0.1:1/").expect("valid unavailable Valkey URL");
    let response = router(Identity2FAState::new(pool, valkey, Arc::new(UnusedRotator)))
        .layer(Extension(Identity2FAActor {
            user_id: 7,
            role: 1,
        }))
        .layer(Extension(Identity2FASession {
            session_id: "fixture-session".to_owned(),
            client_ip: "127.0.0.1".to_owned(),
            user_agent: "identity-2fa-integration-test".to_owned(),
            cookie_secure: false,
        }))
        .oneshot(
            Request::post("/api/user/2fa/enable")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"code":"123456"}"#))
                .expect("valid request"),
        )
        .await
        .expect("router response");
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("legacy response envelope"),
        json!({"success": false, "message": "2FA已经启用"})
    );
}
