use async_trait::async_trait;
use axum::{
    body::Body,
    http::{HeaderMap, Request, StatusCode},
};
use lmm_api_rs::routes::identity_profile::{
    ProfileAuthError, ProfileIdentity, ProfileIdentityResolver, ProfileState, router,
};
use serde_json::Value;
use sqlx::postgres::PgPoolOptions;
use std::{env, sync::Arc};
use tower::ServiceExt;

#[derive(Clone)]
struct VerifiedPrincipal;

#[async_trait]
impl ProfileIdentityResolver for VerifiedPrincipal {
    async fn principal(&self, headers: &HeaderMap) -> Result<ProfileIdentity, ProfileAuthError> {
        if headers
            .get("authorization")
            .and_then(|value| value.to_str().ok())
            == Some("Bearer listener-verified")
        {
            Ok(ProfileIdentity {
                user_id: 7,
                role: 1,
            })
        } else {
            Err(ProfileAuthError::Unauthorized)
        }
    }
}

struct InternalPrincipal;

#[async_trait]
impl ProfileIdentityResolver for InternalPrincipal {
    async fn principal(&self, _: &HeaderMap) -> Result<ProfileIdentity, ProfileAuthError> {
        Err(ProfileAuthError::Internal)
    }
}

fn app() -> axum::Router {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("valid lazy test URL");
    let valkey = redis::Client::open("redis://127.0.0.1/").expect("valid test URL");
    router(ProfileState::new(pool, valkey).with_identity_resolver(Arc::new(VerifiedPrincipal)))
}

fn app_without_listener_principal() -> axum::Router {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("valid lazy test URL");
    let valkey = redis::Client::open("redis://127.0.0.1/").expect("valid test URL");
    router(ProfileState::new(pool, valkey))
}

#[tokio::test]
async fn profile_auth_dependency_failures_keep_go_internal_auth_status_and_code() {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("valid lazy test URL");
    let valkey = redis::Client::open("redis://127.0.0.1/").expect("valid test URL");
    let response =
        router(ProfileState::new(pool, valkey).with_identity_resolver(Arc::new(InternalPrincipal)))
            .oneshot(
                Request::get("/api/user/aff")
                    .header("accept-language", "zh-CN")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");

    assert_eq!(response.status(), StatusCode::INTERNAL_SERVER_ERROR);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("JSON failure envelope"),
        serde_json::json!({
            "success": false,
            "code": "AUTH_INTERNAL_ERROR",
            "message": "数据库出错，请联系管理员"
        })
    );
}

#[tokio::test]
async fn self_profile_does_not_trust_client_identity_headers() {
    let response = app_without_listener_principal()
        .oneshot(
            Request::get("/api/user/aff")
                .header("x-user-id", "7")
                .header("x-role", "100")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    let json: Value = serde_json::from_slice(&body).expect("JSON failure envelope");
    assert_eq!(
        json,
        serde_json::json!({
            "success": false,
            "code": "AUTH_UNAUTHORIZED",
            "message": "Unauthorized, invalid access token"
        })
    );
}

#[tokio::test]
async fn company_billing_profile_authenticates_before_parsing() {
    let response = app_without_listener_principal()
        .oneshot(
            Request::put("/api/user/company-billing-profile")
                .header("content-type", "application/json")
                .header("x-user-id", "7")
                .body(Body::from("{"))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("JSON failure envelope")["code"],
        "AUTH_UNAUTHORIZED"
    );
}

#[tokio::test]
async fn company_billing_profile_rejects_client_provider_rules_before_postgres() {
    for body in [
        r#"{"country":"US","isBusiness":true,"useForInvoices":true,"requiredFields":[]}"#,
        r#"{"country":"US","isBusiness":true,"useForInvoices":true,"providerRules":{"requiredFields":[]}}"#,
    ] {
        let response = app()
            .oneshot(
                Request::put("/api/user/company-billing-profile")
                    .header("authorization", "Bearer listener-verified")
                    .header("content-type", "application/json")
                    .body(Body::from(body))
                    .expect("request"),
            )
            .await
            .expect("response");

        assert_eq!(response.status(), StatusCode::BAD_REQUEST);
        let bytes = axum::body::to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("body");
        let payload = serde_json::from_slice::<Value>(&bytes).expect("JSON failure envelope");
        assert_eq!(payload["success"], false);
        assert_eq!(
            payload["message"],
            "Invalid company billing profile request"
        );
        assert!(payload.get("errors").is_none());
    }
}

#[tokio::test]
async fn company_billing_profile_requires_server_owned_identity_fields() {
    let response = app()
        .oneshot(
            Request::put("/api/user/company-billing-profile")
                .header("authorization", "Bearer listener-verified")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"postcode":"10001"}"#))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::UNPROCESSABLE_ENTITY);
    let bytes = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    let payload = serde_json::from_slice::<Value>(&bytes).expect("JSON failure envelope");
    assert_eq!(payload["errors"]["country"], "required");
    assert_eq!(payload["errors"]["isBusiness"], "required");
    assert_eq!(payload["errors"]["useForInvoices"], "required");
}

#[tokio::test]
async fn company_billing_profile_validation_does_not_echo_sensitive_values() {
    let sensitive_name = "N".repeat(256);
    let sensitive_tax_id = "sensitive-tax-value";
    let body = serde_json::json!({
        "country": "US",
        "isBusiness": true,
        "businessName": sensitive_name,
        "taxId": sensitive_tax_id,
        "useForInvoices": true
    })
    .to_string();
    let response = app()
        .oneshot(
            Request::put("/api/user/company-billing-profile")
                .header("authorization", "Bearer listener-verified")
                .header("content-type", "application/json")
                .body(Body::from(body))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::UNPROCESSABLE_ENTITY);
    let bytes = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    let response_text = String::from_utf8(bytes.to_vec()).expect("UTF-8 body");
    assert!(!response_text.contains(&sensitive_name));
    assert!(!response_text.contains(sensitive_tax_id));
    assert_eq!(
        serde_json::from_str::<Value>(&response_text).expect("JSON failure envelope")["errors"]["businessName"],
        "too_long"
    );
}

#[tokio::test]
async fn setting_update_rejects_a_client_supplied_identity_before_postgres() {
    let response = app_without_listener_principal()
        .oneshot(
            Request::put("/api/user/setting")
                .header("content-type", "application/json")
                .header("x-user-id", "7")
                .body(Body::from(r#"{"language":"en"}"#))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("JSON failure envelope")["code"],
        "AUTH_UNAUTHORIZED"
    );
}

#[tokio::test]
async fn profile_write_authenticates_before_parsing_a_malformed_request_body() {
    let response = app_without_listener_principal()
        .oneshot(
            Request::put("/api/user/self")
                .header("content-type", "application/json")
                .body(Body::from("{"))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("JSON failure envelope")["code"],
        "AUTH_UNAUTHORIZED"
    );
}

#[tokio::test]
async fn profile_setting_rejects_an_invalid_notification_type_before_postgres() {
    let response = app()
        .oneshot(
            Request::put("/api/user/setting")
                .header("authorization", "Bearer listener-verified")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"notify_type":"sms","quota_warning_threshold":1}"#,
                ))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::OK);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("JSON failure envelope")["message"],
        "Invalid warning type"
    );
}

#[tokio::test]
async fn profile_setting_null_body_keeps_gin_zero_value_validation() {
    let response = app()
        .oneshot(
            Request::put("/api/user/setting")
                .header("authorization", "Bearer listener-verified")
                .header("content-type", "application/json")
                .body(Body::from("null"))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::OK);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("JSON failure envelope"),
        serde_json::json!({"success": false, "message": "Invalid warning type"})
    );
}

#[tokio::test]
async fn authenticated_profile_handler_errors_preserve_auth_version() {
    let response = app()
        .oneshot(
            Request::put("/api/user/self")
                .header("authorization", "Bearer listener-verified")
                .header("content-type", "application/json")
                .body(Body::from("{"))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        response
            .headers()
            .get("auth-version")
            .and_then(|value| value.to_str().ok()),
        Some("864b7076dbcd0a3c01b5520316720ebf")
    );
}

#[tokio::test]
async fn self_oauth_binding_rejects_non_oauth_fields_before_postgres() {
    let response = app()
        .oneshot(
            Request::delete("/api/user/bindings/email")
                .header("authorization", "Bearer listener-verified")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::OK);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("JSON failure envelope"),
        serde_json::json!({
            "success": false,
            "message": "invalid parameters"
        })
    );
}

#[tokio::test]
async fn self_profile_password_rotation_requires_session_owner_before_postgres() {
    let response = app()
        .oneshot(
            Request::put("/api/user/self")
                .header("authorization", "Bearer listener-verified")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"password":"new-password"}"#))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::OK);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    let json: Value = serde_json::from_slice(&body).expect("JSON failure envelope");
    assert_eq!(json["success"], false);
    assert_eq!(
        json["message"],
        "password rotation requires the authenticated session"
    );
}

#[tokio::test]
async fn self_profile_uses_request_locale_for_rejected_unverified_identity() {
    let response = app()
        .oneshot(
            Request::get("/api/user/aff")
                .header("accept-language", "zh-CN,zh;q=0.9")
                .header("x-user-id", "7")
                .header("x-role", "100")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    let json: Value = serde_json::from_slice(&body).expect("JSON failure envelope");
    assert_eq!(json["message"], "无权进行此操作，access token 无效");
}

#[tokio::test]
#[ignore = "requires the contract-7 PostgreSQL baseline and LMM_IDENTITY_TEST_DATABASE_URL"]
async fn company_billing_profile_get_put_is_owner_scoped_and_cascades_with_user() {
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
    sqlx::query("DELETE FROM users WHERE id = 7")
        .execute(&pool)
        .await
        .expect("reset test user and cascaded profile");
    sqlx::query("INSERT INTO users (id, password) VALUES (7, 'unused')")
        .execute(&pool)
        .await
        .expect("seed owner");

    let unavailable_valkey =
        redis::Client::open("redis://127.0.0.1:1/").expect("valid unavailable Valkey URL");
    let application = router(
        ProfileState::new(pool.clone(), unavailable_valkey)
            .with_identity_resolver(Arc::new(VerifiedPrincipal)),
    );

    let response = application
        .clone()
        .oneshot(
            Request::get("/api/user/company-billing-profile")
                .header("authorization", "Bearer listener-verified")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("absent profile response")["data"],
        Value::Null
    );

    let response = application
        .clone()
        .oneshot(
            Request::put("/api/user/company-billing-profile")
                .header("authorization", "Bearer listener-verified")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"country":"us","isBusiness":true,"postcode":" 10001 ","state":" NY ","businessName":" Example LLC ","taxId":" T-001 ","useForInvoices":true}"#,
                ))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    let payload = serde_json::from_slice::<Value>(&body).expect("saved profile response");
    assert_eq!(payload["data"]["country"], "US");
    assert_eq!(payload["data"]["postcode"], "10001");
    assert_eq!(payload["data"]["useForInvoices"], true);

    let stored: (String, String, String) = sqlx::query_as(
        "SELECT country, business_name, tax_id FROM company_billing_profiles WHERE user_id = 7",
    )
    .fetch_one(&pool)
    .await
    .expect("owner-scoped stored profile");
    assert_eq!(stored.0, "US");
    assert_eq!(stored.1, "Example LLC");
    assert_eq!(stored.2, "T-001");

    let response = application
        .oneshot(
            Request::get("/api/user/company-billing-profile")
                .header("authorization", "Bearer listener-verified")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    let payload = serde_json::from_slice::<Value>(&body).expect("loaded profile response");
    assert_eq!(payload["data"]["businessName"], "Example LLC");

    sqlx::query("DELETE FROM users WHERE id = 7")
        .execute(&pool)
        .await
        .expect("delete owner");
    let remaining: i64 =
        sqlx::query_scalar("SELECT count(*) FROM company_billing_profiles WHERE user_id = 7")
            .fetch_one(&pool)
            .await
            .expect("count cascaded profile");
    assert_eq!(
        remaining, 0,
        "owner deletion must physically remove billing PII"
    );
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; set LMM_IDENTITY_TEST_DATABASE_URL and LMM_IDENTITY_TEST_VALKEY_URL"]
async fn profile_preference_write_updates_postgres_and_refreshes_valkey_user_cache() {
    assert_eq!(
        env::var("LMM_IDENTITY_TEST_ALLOW_SCHEMA_RESET").as_deref(),
        Ok("1"),
        "integration test requires LMM_IDENTITY_TEST_ALLOW_SCHEMA_RESET=1"
    );
    let database_url =
        env::var("LMM_IDENTITY_TEST_DATABASE_URL").expect("isolated PostgreSQL 18 URL");
    let valkey_url = env::var("LMM_IDENTITY_TEST_VALKEY_URL").expect("isolated Valkey URL");
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database_url)
        .await
        .expect("connect isolated PostgreSQL");
    sqlx::query("DROP TABLE IF EXISTS users")
        .execute(&pool)
        .await
        .expect("reset users");
    sqlx::query("CREATE TABLE users (id BIGINT PRIMARY KEY, setting TEXT, deleted_at TIMESTAMPTZ, username TEXT, display_name TEXT, password TEXT, role BIGINT, auth_version BIGINT)")
        .execute(&pool)
        .await
        .expect("create users");
    sqlx::query("INSERT INTO users (id, setting, username, display_name, password, role, auth_version) VALUES (7, '{}', 'oracle', 'Oracle', 'unused', 1, 1)")
        .execute(&pool)
        .await
        .expect("seed user");
    let valkey = redis::Client::open(valkey_url).expect("isolated Valkey URL");
    let mut cache = valkey
        .get_multiplexed_async_connection()
        .await
        .expect("connect isolated Valkey");
    redis::cmd("HSET")
        .arg("user:7")
        .arg("Id")
        .arg(7)
        .arg("AuthVersion")
        .arg(1)
        .arg("CacheSchema")
        .arg(2)
        .arg("Setting")
        .arg("{}")
        .query_async::<()>(&mut cache)
        .await
        .expect("seed cache");
    let application = router(
        ProfileState::new(pool.clone(), valkey).with_identity_resolver(Arc::new(VerifiedPrincipal)),
    );
    let response = application
        .clone()
        .oneshot(
            Request::put("/api/user/self")
                .header("authorization", "Bearer listener-verified")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"language":"en"}"#))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        response.headers()["cache-control"],
        "no-store, no-cache, must-revalidate, private, max-age=0"
    );
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("preference response body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("legacy preference response"),
        serde_json::json!({"success": true, "message": "Update successful", "data": null})
    );
    let setting: String = sqlx::query_scalar("SELECT setting FROM users WHERE id = 7")
        .fetch_one(&pool)
        .await
        .expect("updated setting");
    assert_eq!(
        serde_json::from_str::<Value>(&setting).expect("setting JSON")["language"],
        "en"
    );
    let cached_setting: String = redis::cmd("HGET")
        .arg("user:7")
        .arg("Setting")
        .query_async(&mut cache)
        .await
        .expect("cache setting");
    assert_eq!(
        serde_json::from_str::<Value>(&cached_setting).expect("cached setting JSON")["language"],
        "en"
    );

    let response = application
        .oneshot(
            Request::put("/api/user/setting")
                .header("authorization", "Bearer listener-verified")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"notify_type":"email","quota_warning_threshold":2,"notification_email":"ada@example.test"}"#,
                ))
                .expect("setting request"),
        )
        .await
        .expect("setting response");
    assert_eq!(response.status(), StatusCode::OK);
    let setting: String = sqlx::query_scalar("SELECT setting FROM users WHERE id = 7")
        .fetch_one(&pool)
        .await
        .expect("stored notification setting");
    let setting: Value = serde_json::from_str(&setting).expect("setting JSON");
    assert_eq!(setting["notify_type"], "email");
    assert_eq!(setting["notification_email"], "ada@example.test");
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18; set LMM_IDENTITY_TEST_DATABASE_URL"]
async fn profile_write_keeps_postgres_authoritative_when_valkey_is_unavailable() {
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
    sqlx::query("DROP TABLE IF EXISTS users")
        .execute(&pool)
        .await
        .expect("reset users");
    sqlx::query("CREATE TABLE users (id BIGINT PRIMARY KEY, setting TEXT, deleted_at TIMESTAMPTZ, username TEXT, display_name TEXT, password TEXT, role BIGINT, auth_version BIGINT)")
        .execute(&pool)
        .await
        .expect("create users");
    sqlx::query("INSERT INTO users (id, setting, username, display_name, password, role, auth_version) VALUES (7, '{}', 'oracle', 'Oracle', 'unused', 1, 1)")
        .execute(&pool)
        .await
        .expect("seed user");
    let unavailable_valkey =
        redis::Client::open("redis://127.0.0.1:1/").expect("valid unavailable Valkey URL");
    let application = router(
        ProfileState::new(pool.clone(), unavailable_valkey)
            .with_identity_resolver(Arc::new(VerifiedPrincipal)),
    );

    let response = application
        .oneshot(
            Request::put("/api/user/self")
                .header("authorization", "Bearer listener-verified")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"language":"zh-CN"}"#))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::OK);
    let setting: String = sqlx::query_scalar("SELECT setting FROM users WHERE id = 7")
        .fetch_one(&pool)
        .await
        .expect("durable profile setting");
    assert_eq!(
        serde_json::from_str::<Value>(&setting).expect("setting JSON")["language"],
        "zh-CN"
    );
}
