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

struct TrustedProfilePrincipal(bool);

#[async_trait]
impl ProfileIdentityResolver for TrustedProfilePrincipal {
    async fn principal(&self, headers: &HeaderMap) -> Result<ProfileIdentity, ProfileAuthError> {
        VerifiedPrincipal.principal(headers).await
    }
    async fn may_manage_ip_bypass(&self, _: ProfileIdentity, _: &HeaderMap) -> bool {
        self.0
    }
}

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
async fn settings_declared_limit_rejects_before_polling_body_but_after_authentication() {
    for (authorization, status) in [
        ("Bearer listener-verified", StatusCode::PAYLOAD_TOO_LARGE),
        ("Bearer forged", StatusCode::UNAUTHORIZED),
    ] {
        let body = Body::from_stream(futures_util::stream::pending::<
            Result<axum::body::Bytes, std::io::Error>,
        >());
        let response = tokio::time::timeout(
            std::time::Duration::from_secs(1),
            app().oneshot(
                Request::put("/api/user/setting")
                    .header("authorization", authorization)
                    .header("content-length", "16385")
                    .body(body)
                    .unwrap(),
            ),
        )
        .await
        .expect("body must not be polled")
        .unwrap();
        assert_eq!(response.status(), status);
        if status == StatusCode::PAYLOAD_TOO_LARGE {
            assert!(
                axum::body::to_bytes(response.into_body(), usize::MAX)
                    .await
                    .unwrap()
                    .is_empty()
            );
        }
    }
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
    sqlx::query("INSERT INTO users (id, setting, username, display_name, password, role, auth_version) VALUES (7, '{\"session_auto_logout\":false}', 'oracle', 'Oracle', 'unused', 1, 1)")
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
    let cached_setting: Option<String> = redis::cmd("HGET")
        .arg("user:7")
        .arg("Setting")
        .query_async(&mut cache)
        .await
        .expect("cache setting");
    assert!(
        cached_setting.is_none(),
        "current Go invalidates the user cache after locale changes"
    );
    // Simulate the next reader filling the cache before a notification write.
    redis::cmd("HSET")
        .arg("user:7")
        .arg("AuthVersion")
        .arg(1)
        .arg("CacheSchema")
        .arg(2)
        .arg("Setting")
        .arg(&setting)
        .query_async::<()>(&mut cache)
        .await
        .expect("refill cache");

    let response = application
        .clone()
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
    assert_eq!(setting["session_auto_logout"], false);
    let cached: Option<String> = redis::cmd("HGET")
        .arg("user:7")
        .arg("Setting")
        .query_async(&mut cache)
        .await
        .expect("notification cache");
    assert!(
        cached.is_none(),
        "current Go notification writes invalidate cached preferences"
    );
    assert_eq!(setting["language"], "en");

    // A concurrent change to the session preference must be observed after
    // the notification writer acquires the user's row lock.
    let mut preference_tx = pool.begin().await.expect("preference transaction");
    sqlx::query("UPDATE users SET setting = '{\"session_auto_logout\":true}' WHERE id=7")
        .execute(&mut *preference_tx)
        .await
        .expect("lock session preference");
    let mut writer = tokio::spawn(
        application.oneshot(
            Request::put("/api/user/setting")
                .header("authorization", "Bearer listener-verified")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"notify_type":"email","quota_warning_threshold":42}"#,
                ))
                .expect("concurrent notification request"),
        ),
    );
    assert!(
        tokio::time::timeout(std::time::Duration::from_millis(50), &mut writer)
            .await
            .is_err()
    );
    preference_tx
        .commit()
        .await
        .expect("commit session preference");
    let response = tokio::time::timeout(std::time::Duration::from_secs(5), writer)
        .await
        .expect("notification unblocked")
        .expect("writer task")
        .expect("notification response");
    assert_eq!(response.status(), StatusCode::OK);
    let stored: String = sqlx::query_scalar("SELECT setting FROM users WHERE id=7")
        .fetch_one(&pool)
        .await
        .expect("concurrent stored preferences");
    let stored: Value = serde_json::from_str(&stored).expect("stored JSON");
    assert_eq!(stored["session_auto_logout"], true);
    assert_eq!(stored["quota_warning_threshold"], 42);
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
    let body: Value = serde_json::from_slice(
        &axum::body::to_bytes(response.into_body(), usize::MAX)
            .await
            .unwrap(),
    )
    .unwrap();
    assert_eq!(
        body["success"], false,
        "Go reports invalidation failure after committing the setting"
    );
    assert_eq!(body["message"], "Update failed");
    let setting: String = sqlx::query_scalar("SELECT setting FROM users WHERE id = 7")
        .fetch_one(&pool)
        .await
        .expect("durable profile setting");
    assert_eq!(
        serde_json::from_str::<Value>(&setting).expect("setting JSON")["language"],
        "zh-CN"
    );
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey via LMM_IDENTITY_TEST_DATABASE_URL and LMM_IDENTITY_TEST_VALKEY_URL"]
async fn privacy_preferences_and_sidebar_shapes_follow_verified_trust_and_preserve_locale() {
    let url = env::var("LMM_IDENTITY_TEST_DATABASE_URL").expect("isolated PostgreSQL");
    let admin = sqlx::PgPool::connect(&url).await.unwrap();
    let schema = format!("profile_preferences_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let pool = PgPoolOptions::new()
        .max_connections(3)
        .after_connect({
            let schema = schema.clone();
            move |connection, _| {
                let command = format!("SET search_path TO {schema}");
                Box::pin(async move {
                    sqlx::query(&command).execute(connection).await?;
                    Ok(())
                })
            }
        })
        .connect(&url)
        .await
        .unwrap();
    sqlx::raw_sql("CREATE TABLE users(id BIGINT PRIMARY KEY,setting TEXT,deleted_at TIMESTAMPTZ,auth_version BIGINT); INSERT INTO users VALUES(7,'{\"language\":\"zh\",\"settlement_currency\":\"CNY\",\"session_auto_logout\":false,\"usage_leaderboard_visibility\":\"public\"}',NULL,1)")
        .execute(&pool).await.unwrap();
    let cache =
        redis::Client::open(env::var("LMM_IDENTITY_TEST_VALKEY_URL").expect("isolated Valkey"))
            .unwrap();
    let application = |trusted| {
        router(
            ProfileState::new(pool.clone(), cache.clone())
                .with_identity_resolver(Arc::new(TrustedProfilePrincipal(trusted))),
        )
    };
    let request = |path: &str, body: Value| {
        Request::put(path)
            .header("authorization", "Bearer listener-verified")
            .header("content-type", "application/json")
            .body(Body::from(body.to_string()))
            .unwrap()
    };
    let change = serde_json::json!({"notify_type":"email","quota_warning_threshold":42,
        "usage_leaderboard_visibility":" HIDDEN ","allow_key_bypass_ip_policy":true,"trust_level":99});
    let response = application(false)
        .oneshot(request("/api/user/setting", change.clone()))
        .await
        .unwrap();
    let body: Value = serde_json::from_slice(
        &axum::body::to_bytes(response.into_body(), usize::MAX)
            .await
            .unwrap(),
    )
    .unwrap();
    assert_eq!(body["success"], true);
    let stored: String = sqlx::query_scalar("SELECT setting FROM users WHERE id=7")
        .fetch_one(&pool)
        .await
        .unwrap();
    let stored: Value = serde_json::from_str(&stored).unwrap();
    assert_eq!(stored["usage_leaderboard_visibility"], "hidden");
    assert!(
        stored.get("allow_key_bypass_ip_policy").is_none(),
        "body trust claims cannot grant the L1 capability"
    );
    assert_eq!(stored["language"], "zh");
    assert_eq!(stored["settlement_currency"], "CNY");
    assert_eq!(stored["session_auto_logout"], false);
    application(true)
        .oneshot(request("/api/user/setting", change))
        .await
        .unwrap();
    let before: String = sqlx::query_scalar("SELECT setting FROM users WHERE id=7")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(
        serde_json::from_str::<Value>(&before).unwrap()["allow_key_bypass_ip_policy"],
        true
    );
    for sidebar in [
        Value::Null,
        serde_json::json!(false),
        serde_json::json!(r#"{"preferences":{"default_route":"//outside.test"}}"#),
    ] {
        let response = application(true)
            .oneshot(request(
                "/api/user/self",
                serde_json::json!({"sidebar_modules":sidebar}),
            ))
            .await
            .unwrap();
        let body: Value = serde_json::from_slice(
            &axum::body::to_bytes(response.into_body(), usize::MAX)
                .await
                .unwrap(),
        )
        .unwrap();
        assert_eq!(body["success"], false);
        let after: String = sqlx::query_scalar("SELECT setting FROM users WHERE id=7")
            .fetch_one(&pool)
            .await
            .unwrap();
        assert_eq!(
            after, before,
            "rejected sidebar writes must leave all preferences unchanged"
        );
    }
    let sidebar = r#"{"modules":{},"preferences":{"density":"compact","hidden":[]}}"#;
    let response = application(true).oneshot(request("/api/user/self", serde_json::json!({"sidebar_modules":sidebar,"language":"en","settlement_currency":"USD"}))).await.unwrap();
    let body: Value = serde_json::from_slice(
        &axum::body::to_bytes(response.into_body(), usize::MAX)
            .await
            .unwrap(),
    )
    .unwrap();
    assert_eq!(body["success"], true);
    let stored: String = sqlx::query_scalar("SELECT setting FROM users WHERE id=7")
        .fetch_one(&pool)
        .await
        .unwrap();
    let stored: Value = serde_json::from_str(&stored).unwrap();
    assert_eq!(stored["sidebar_modules"], sidebar);
    assert_eq!(stored["language"], "zh");
    assert_eq!(stored["settlement_currency"], "CNY");
    assert_eq!(stored["allow_key_bypass_ip_policy"], true);
    pool.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
}
