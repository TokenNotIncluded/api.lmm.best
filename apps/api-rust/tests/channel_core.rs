use std::{env, sync::Arc, time::Duration};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{HeaderMap, Request, StatusCode},
};
use lmm_api_rs::routes::channel_core::{
    ChannelAction, ChannelAdminAuthorizer, ChannelCoreState, ChannelError, router,
};
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

struct Deny;

#[async_trait]
impl ChannelAdminAuthorizer for Deny {
    async fn authorize(&self, _: &HeaderMap, _: ChannelAction) -> Result<(), ChannelError> {
        Err(ChannelError::Unauthorized)
    }
}

struct Allow;

#[async_trait]
impl ChannelAdminAuthorizer for Allow {
    async fn authorize(&self, _: &HeaderMap, _: ChannelAction) -> Result<(), ChannelError> {
        Ok(())
    }
}

struct DenySensitiveWrites;

#[async_trait]
impl ChannelAdminAuthorizer for DenySensitiveWrites {
    async fn authorize(&self, _: &HeaderMap, action: ChannelAction) -> Result<(), ChannelError> {
        if matches!(action, ChannelAction::SensitiveWrite) {
            Err(ChannelError::Forbidden)
        } else {
            Ok(())
        }
    }
}

#[tokio::test]
async fn channel_core_router_constructs_with_axum_08_path_syntax() {
    let state = ChannelCoreState {
        pg: PgPoolOptions::new()
            .connect_lazy("postgres://unused")
            .expect("lazy PostgreSQL pool"),
        valkey: redis::Client::open("redis://127.0.0.1/").expect("Valkey client"),
        authorizer: Arc::new(Deny),
        retry_times: 0,
    };
    let _router = router(state);
}

#[tokio::test]
async fn channel_core_router_rejects_an_unauthenticated_request_before_storage_access() {
    let app = router(ChannelCoreState {
        pg: PgPoolOptions::new()
            .connect_lazy("postgres://unused")
            .expect("lazy PostgreSQL pool"),
        valkey: redis::Client::open("redis://127.0.0.1/").expect("Valkey client"),
        authorizer: Arc::new(Deny),
        retry_times: 0,
    });
    let response = app
        .oneshot(
            Request::builder()
                .uri("/api/channel/?p=1&page_size=20")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    let body: serde_json::Value = serde_json::from_slice(
        &to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body"),
    )
    .expect("response JSON");
    assert_eq!(
        body,
        serde_json::json!({"success":false,"message":"Unauthorized, invalid access token","code":"AUTH_UNAUTHORIZED"})
    );
}

#[tokio::test]
async fn channel_core_auth_preflight_runs_before_malformed_json_rejection() {
    let app = router(ChannelCoreState {
        pg: PgPoolOptions::new()
            .connect_lazy("postgres://unused")
            .expect("lazy PostgreSQL pool"),
        valkey: redis::Client::open("redis://127.0.0.1/").expect("Valkey client"),
        authorizer: Arc::new(Deny),
        retry_times: 0,
    });
    let response = app
        .oneshot(
            Request::post("/api/channel/")
                .header("content-type", "application/json")
                .body(Body::from("{"))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    let body: serde_json::Value = serde_json::from_slice(
        &to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body"),
    )
    .expect("response JSON");
    assert_eq!(
        body,
        serde_json::json!({"success":false,"message":"Unauthorized, invalid access token","code":"AUTH_UNAUTHORIZED"})
    );
}

#[tokio::test]
async fn channel_auth_version_is_emitted_only_after_authorization() {
    let authorized = router(ChannelCoreState {
        pg: PgPoolOptions::new()
            .connect_lazy("postgres://unused")
            .expect("lazy PostgreSQL pool"),
        valkey: redis::Client::open("redis://127.0.0.1/").expect("Valkey client"),
        authorizer: Arc::new(Allow),
        retry_times: 7,
    })
    .oneshot(
        Request::get("/api/channel/ops")
            .body(Body::empty())
            .expect("request"),
    )
    .await
    .expect("response");
    assert_eq!(authorized.status(), StatusCode::OK);
    assert_eq!(
        authorized.headers()["auth-version"],
        "864b7076dbcd0a3c01b5520316720ebf"
    );

    let unauthorized = router(ChannelCoreState {
        pg: PgPoolOptions::new()
            .connect_lazy("postgres://unused")
            .expect("lazy PostgreSQL pool"),
        valkey: redis::Client::open("redis://127.0.0.1/").expect("Valkey client"),
        authorizer: Arc::new(Deny),
        retry_times: 7,
    })
    .oneshot(
        Request::get("/api/channel/ops")
            .body(Body::empty())
            .expect("request"),
    )
    .await
    .expect("response");
    assert_eq!(unauthorized.status(), StatusCode::UNAUTHORIZED);
    assert!(!unauthorized.headers().contains_key("auth-version"));
}

#[tokio::test]
async fn channel_status_rejects_an_unmanageable_value_with_the_frozen_go_body() {
    let app = router(ChannelCoreState {
        pg: PgPoolOptions::new()
            .connect_lazy("postgres://unused")
            .expect("lazy PostgreSQL pool"),
        valkey: redis::Client::open("redis://127.0.0.1/").expect("Valkey client"),
        authorizer: Arc::new(Allow),
        retry_times: 0,
    });
    let response = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/channel/1/status")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"status":3}"#))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    let body: serde_json::Value = serde_json::from_slice(
        &to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body"),
    )
    .expect("response JSON");
    assert_eq!(
        body,
        serde_json::json!({"success":false,"message":"Invalid parameters"})
    );
}

#[tokio::test]
async fn channel_status_uses_the_frozen_accept_language_fallback_before_storage_access() {
    let app = router(ChannelCoreState {
        pg: PgPoolOptions::new()
            .connect_lazy("postgres://unused")
            .expect("lazy PostgreSQL pool"),
        valkey: redis::Client::open("redis://127.0.0.1/").expect("Valkey client"),
        authorizer: Arc::new(Allow),
        retry_times: 0,
    });
    let response = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/channel/1/status")
                .header("accept-language", "zh-CN,zh;q=0.9")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"status":3}"#))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    let body: serde_json::Value = serde_json::from_slice(
        &to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body"),
    )
    .expect("response JSON");
    assert_eq!(
        body,
        serde_json::json!({"success":false,"message":"无效的参数"})
    );
}

#[tokio::test]
async fn channel_update_requires_sensitive_write_permission_before_touching_storage_when_key_changes()
 {
    let app = router(ChannelCoreState {
        pg: PgPoolOptions::new()
            .connect_lazy("postgres://unused")
            .expect("lazy PostgreSQL pool"),
        valkey: redis::Client::open("redis://127.0.0.1/").expect("Valkey client"),
        authorizer: Arc::new(DenySensitiveWrites),
        retry_times: 0,
    });
    let response = app
        .oneshot(
            Request::builder()
                .method("PUT")
                .uri("/api/channel/")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"id":1,"name":"new name","type":1,"key":"rotated-secret"}"#,
                ))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::FORBIDDEN);
}

#[tokio::test]
async fn channel_update_rejects_operational_status_before_storage_access() {
    let app = router(ChannelCoreState {
        pg: PgPoolOptions::new()
            .connect_lazy("postgres://unused")
            .expect("lazy PostgreSQL pool"),
        valkey: redis::Client::open("redis://127.0.0.1/").expect("Valkey client"),
        authorizer: Arc::new(Allow),
        retry_times: 0,
    });
    let response = app
        .oneshot(
            Request::builder()
                .method("PUT")
                .uri("/api/channel/")
                .header("accept-language", "zh-CN")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"id":1,"status":2}"#))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    let body: serde_json::Value = serde_json::from_slice(
        &to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body"),
    )
    .expect("response JSON");
    assert_eq!(
        body,
        serde_json::json!({"success":false,"message":"无效的参数"})
    );
}

#[tokio::test]
async fn channel_update_fails_closed_for_sensitive_and_unknown_fields_before_storage_access() {
    for body in [
        r#"{"id":1,"base_url":"https://provider.invalid"}"#,
        r#"{"id":1,"future_provider_secret":"secret"}"#,
    ] {
        let app = router(ChannelCoreState {
            pg: PgPoolOptions::new()
                .connect_lazy("postgres://unused")
                .expect("lazy PostgreSQL pool"),
            valkey: redis::Client::open("redis://127.0.0.1/").expect("Valkey client"),
            authorizer: Arc::new(DenySensitiveWrites),
            retry_times: 0,
        });
        let response = app
            .oneshot(
                Request::builder()
                    .method("PUT")
                    .uri("/api/channel/")
                    .header("content-type", "application/json")
                    .body(Body::from(body))
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(response.status(), StatusCode::FORBIDDEN);
    }
}

#[tokio::test]
async fn provider_configuration_is_rejected_before_postgres_or_valkey_access() {
    let app = router(ChannelCoreState {
        pg: PgPoolOptions::new()
            .connect_lazy("postgres://unused")
            .expect("lazy PostgreSQL pool"),
        valkey: redis::Client::open("redis://127.0.0.1/").expect("Valkey client"),
        authorizer: Arc::new(Allow),
        retry_times: 0,
    });
    let response = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/channel/")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"mode":"single","channel":{"type":57,"key":"{\"access_token\":\"token\"}"}}"#,
                ))
                .expect("request"),
        )
        .await
        .expect("response");
    let body: serde_json::Value = serde_json::from_slice(
        &to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body"),
    )
    .expect("response JSON");
    assert_eq!(
        body,
        serde_json::json!({"success":false,"message":"Codex key JSON must include account_id"})
    );
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; set LMM_CHANNEL_TEST_ALLOW_SCHEMA_RESET=1, LMM_CHANNEL_TEST_DATABASE_URL, and LMM_CHANNEL_TEST_VALKEY_URL"]
async fn channel_crud_permissions_concurrency_and_cache_recovery_use_real_postgres18_and_valkey() {
    assert_eq!(
        env::var("LMM_CHANNEL_TEST_ALLOW_SCHEMA_RESET").as_deref(),
        Ok("1"),
        "integration test requires LMM_CHANNEL_TEST_ALLOW_SCHEMA_RESET=1"
    );
    let database_url = env::var("LMM_CHANNEL_TEST_DATABASE_URL")
        .expect("LMM_CHANNEL_TEST_DATABASE_URL is required for the real channel test");
    let valkey_url = env::var("LMM_CHANNEL_TEST_VALKEY_URL")
        .expect("LMM_CHANNEL_TEST_VALKEY_URL is required for the real channel test");
    let pool = PgPoolOptions::new()
        .acquire_timeout(Duration::from_secs(3))
        .connect(&database_url)
        .await
        .expect("connect real PostgreSQL");
    reset_schema(&pool).await;
    let valkey = redis::Client::open(valkey_url.as_str()).expect("real Valkey URL");
    let mut cache = valkey
        .get_multiplexed_async_connection()
        .await
        .expect("connect real Valkey");
    redis::cmd("DEL")
        .arg("lmm:channels:generation")
        .query_async::<()>(&mut cache)
        .await
        .expect("clear isolated generation key");
    let app = router(ChannelCoreState {
        pg: pool.clone(),
        valkey,
        authorizer: Arc::new(Allow),
        retry_times: 2,
    });
    let response = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/channel/")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"mode":"single","channel":{"name":"oracle","type":1,"key":"oracle-key","models":"gpt-oracle","group":"default"}}"#,
                ))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    let body: serde_json::Value = serde_json::from_slice(
        &to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body"),
    )
    .expect("response JSON");
    assert_eq!(body, serde_json::json!({"success":true,"message":""}));
    let abilities: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM abilities WHERE channel_id = 1")
        .fetch_one(&pool)
        .await
        .expect("ability count");
    assert_eq!(abilities, 1);
    let updated = app
        .clone()
        .oneshot(
            Request::builder()
                .method("PUT")
                .uri("/api/channel/")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"id":1,"name":"oracle-updated","type":1,"key":"oracle-key","models":"gpt-oracle,gpt-next","group":"default"}"#,
                ))
                .expect("update request"),
        )
        .await
        .expect("update response");
    assert_eq!(updated.status(), StatusCode::OK);
    let updated: serde_json::Value = serde_json::from_slice(
        &to_bytes(updated.into_body(), usize::MAX)
            .await
            .expect("update body"),
    )
    .expect("update JSON");
    assert_eq!(updated["data"]["key"], "");
    assert_eq!(updated["data"]["models"], "gpt-oracle,gpt-next");
    let copied = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/channel/copy/1")
                .body(Body::empty())
                .expect("copy request"),
        )
        .await
        .expect("copy response");
    assert_eq!(copied.status(), StatusCode::OK);
    let copied: serde_json::Value = serde_json::from_slice(
        &to_bytes(copied.into_body(), usize::MAX)
            .await
            .expect("copy body"),
    )
    .expect("copy JSON");
    assert_eq!(copied["data"]["id"], 2);
    let copied_abilities: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM abilities WHERE channel_id = 2")
            .fetch_one(&pool)
            .await
            .expect("copied ability count");
    assert_eq!(copied_abilities, 2);
    let status = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/channel/1/status")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"status":2}"#))
                .expect("status request"),
        )
        .await
        .expect("status response");
    assert_eq!(status.status(), StatusCode::OK);
    let status: serde_json::Value = serde_json::from_slice(
        &to_bytes(status.into_body(), usize::MAX)
            .await
            .expect("status body"),
    )
    .expect("status JSON");
    assert_eq!(
        status,
        serde_json::json!({"success":true,"message":"","data":true})
    );
    let enabled: bool = sqlx::query_scalar("SELECT enabled FROM abilities WHERE channel_id = 1")
        .fetch_one(&pool)
        .await
        .expect("ability status");
    assert!(!enabled);
    let multi = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/channel/")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"mode":"multi_to_single","multi_key_mode":"random","channel":{"name":"multi","type":1,"key":"one\ntwo","models":"gpt-oracle","group":"default"}}"#,
                ))
                .expect("multi-key request"),
        )
        .await
        .expect("multi-key response");
    assert_eq!(multi.status(), StatusCode::OK);
    let multi_key_status = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/channel/multi_key/manage")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"channel_id":3,"action":"disable_key","key_index":0}"#,
                ))
                .expect("multi-key status request"),
        )
        .await
        .expect("multi-key status response");
    let multi_key_status: serde_json::Value = serde_json::from_slice(
        &to_bytes(multi_key_status.into_body(), usize::MAX)
            .await
            .expect("multi-key status body"),
    )
    .expect("multi-key status JSON");
    assert_eq!(multi_key_status["message"], "密钥已禁用");
    let (replayed, concurrent) = tokio::join!(
        app.clone().oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/channel/multi_key/manage")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"channel_id":3,"action":"disable_key","key_index":0}"#,
                ))
                .expect("replay request"),
        ),
        app.clone().oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/channel/multi_key/manage")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"channel_id":3,"action":"disable_key","key_index":1}"#,
                ))
                .expect("concurrent request"),
        ),
    );
    let replayed = replayed.expect("replay response");
    let concurrent = concurrent.expect("concurrent response");
    assert_eq!(replayed.status(), StatusCode::OK);
    assert_eq!(concurrent.status(), StatusCode::OK);
    let key_status: serde_json::Value =
        sqlx::query_scalar("SELECT channel_info FROM channels WHERE id = 3")
            .fetch_one(&pool)
            .await
            .expect("persisted multikey state");
    assert_eq!(key_status["multi_key_status_list"]["0"], 2);
    assert_eq!(key_status["multi_key_status_list"]["1"], 2);
    let fault_app = router(ChannelCoreState {
        pg: pool.clone(),
        valkey: redis::Client::open("redis://127.0.0.1:1/").expect("fault Valkey URL"),
        authorizer: Arc::new(Allow),
        retry_times: 2,
    });
    let cache_fault = fault_app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/channel/")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"mode":"single","channel":{"name":"committed-before-cache-fault","type":1,"key":"fault-key","models":"gpt-oracle","group":"default"}}"#,
                ))
                .expect("fault request"),
        )
        .await
        .expect("fault response");
    assert_eq!(cache_fault.status(), StatusCode::OK);
    let cache_fault: serde_json::Value = serde_json::from_slice(
        &to_bytes(cache_fault.into_body(), usize::MAX)
            .await
            .expect("fault body"),
    )
    .expect("fault JSON");
    assert_eq!(
        cache_fault,
        serde_json::json!({"success":false,"message":"缓存失效失败"})
    );
    let committed_after_fault: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM channels WHERE id = 4")
            .fetch_one(&pool)
            .await
            .expect("durable mutation after cache fault");
    assert_eq!(committed_after_fault, 1);
    let deleted = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/channel/batch")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"ids":[1,2,3,4,4]}"#))
                .expect("batch request"),
        )
        .await
        .expect("batch response");
    let deleted: serde_json::Value = serde_json::from_slice(
        &to_bytes(deleted.into_body(), usize::MAX)
            .await
            .expect("batch body"),
    )
    .expect("batch JSON");
    assert_eq!(deleted["data"], 4);
    let remaining: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM channels")
        .fetch_one(&pool)
        .await
        .expect("remaining channel count");
    assert_eq!(remaining, 0);
    let generation: i64 = redis::cmd("GET")
        .arg("lmm:channels:generation")
        .query_async(&mut cache)
        .await
        .expect("read generation");
    assert_eq!(generation, 9);
}

async fn reset_schema(pool: &sqlx::PgPool) {
    for statement in [
        "DROP TABLE IF EXISTS abilities",
        "DROP TABLE IF EXISTS channels",
        "CREATE TABLE channels (id BIGSERIAL PRIMARY KEY, type BIGINT NOT NULL DEFAULT 0, key TEXT NOT NULL, open_ai_organization TEXT NOT NULL DEFAULT '', test_model TEXT NOT NULL DEFAULT '', status BIGINT NOT NULL DEFAULT 1, name TEXT NOT NULL DEFAULT '', base_url TEXT NOT NULL DEFAULT '', models TEXT NOT NULL DEFAULT '', \"group\" TEXT NOT NULL DEFAULT 'default', priority BIGINT DEFAULT 0, weight BIGINT DEFAULT 0, tag TEXT, settings TEXT NOT NULL DEFAULT '', setting TEXT NOT NULL DEFAULT '', channel_info JSONB, other TEXT NOT NULL DEFAULT '', model_mapping TEXT NOT NULL DEFAULT '', status_code_mapping TEXT NOT NULL DEFAULT '', param_override TEXT NOT NULL DEFAULT '', header_override TEXT NOT NULL DEFAULT '', remark TEXT, created_time BIGINT NOT NULL DEFAULT 0, test_time BIGINT NOT NULL DEFAULT 0, response_time BIGINT NOT NULL DEFAULT 0, balance DOUBLE PRECISION NOT NULL DEFAULT 0, balance_updated_time BIGINT NOT NULL DEFAULT 0, used_quota BIGINT NOT NULL DEFAULT 0, auto_ban BOOLEAN NOT NULL DEFAULT FALSE, other_info TEXT NOT NULL DEFAULT '')",
        "CREATE TABLE abilities (\"group\" TEXT NOT NULL, model TEXT NOT NULL, channel_id BIGINT NOT NULL, enabled BOOLEAN NOT NULL, priority BIGINT NOT NULL DEFAULT 0, weight BIGINT NOT NULL DEFAULT 0, tag TEXT, PRIMARY KEY (\"group\", model, channel_id))",
    ] {
        sqlx::query(statement)
            .execute(pool)
            .await
            .expect("isolated channel schema statement");
    }
}
