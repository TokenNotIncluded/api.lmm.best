//! Real TCP/PG18/Valkey proof for the non-provider subscription routes.
//!
//! The fixture comes from `subscription-contracts.json`: PostgreSQL owns every
//! subscription and group transition, while Valkey only receives reconstructible
//! invalidations after a successful commit. External payment gateways are not
//! part of this slice.

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{Request, StatusCode},
};
use lmm_api_rs::{
    auth::{
        AuthBundle, AuthError, AuthErrorKind, DashboardAuth, DashboardUser, LoginOutcome,
        LoginRequest, LogoutRequest, LogoutResult, RequestMetadata, TwoFactorLoginRequest,
    },
    migration_routes::billing_subscriptions::{
        BillingSubscriptionsState, maintenance_once, router,
    },
};
use secrecy::{ExposeSecret, SecretString};
use serde_json::json;
use sqlx::{PgPool, postgres::PgPoolOptions};
use std::{env, sync::Arc, time::Duration};
use tower::ServiceExt;

struct FixtureAuth;

#[async_trait]
impl DashboardAuth for FixtureAuth {
    async fn check_critical_rate_limit(
        &self,
        _: &str,
    ) -> Result<lmm_api_rs::auth::CriticalRateLimitOutcome, AuthError> {
        Ok(lmm_api_rs::auth::CriticalRateLimitOutcome::Allowed)
    }

    async fn login(&self, _: LoginRequest, _: RequestMetadata) -> Result<LoginOutcome, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn login_2fa(
        &self,
        _: TwoFactorLoginRequest,
        _: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn refresh(
        &self,
        _: SecretString,
        _: Option<String>,
        _: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn self_user(&self, token: SecretString) -> Result<DashboardUser, AuthError> {
        let role = match token.expose_secret() {
            "admin" => 10,
            "user" => 1,
            _ => return Err(AuthError::new(AuthErrorKind::Unauthorized)),
        };
        Ok(DashboardUser {
            id: 1,
            username: "admin".to_owned(),
            display_name: String::new(),
            role,
            status: 1,
            email: String::new(),
            github_id: String::new(),
            discord_id: String::new(),
            oidc_id: String::new(),
            wechat_id: String::new(),
            telegram_id: String::new(),
            group: "default".to_owned(),
            quota: 0,
            used_quota: 0,
            request_count: 0,
            aff_code: String::new(),
            aff_count: 0,
            aff_quota: 0,
            aff_history_quota: 0,
            inviter_id: 0,
            linux_do_id: String::new(),
            setting: "{}".to_owned(),
            stripe_customer: String::new(),
            sidebar_modules: json!({}),
            permissions: json!({}),
        })
    }

    async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn generate_personal_access_token(&self, _: SecretString) -> Result<String, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }
}

fn smoke_router() -> axum::Router {
    let pool = PgPoolOptions::new()
        .acquire_timeout(Duration::from_millis(200))
        .connect_lazy("postgres://postgres@127.0.0.1:1/billing")
        .expect("valid lazy PostgreSQL test URL");
    router(BillingSubscriptionsState::new(
        pool,
        None,
        Arc::new(FixtureAuth),
    ))
}

fn console_gate_smoke_router() -> axum::Router {
    let pool = PgPoolOptions::new()
        .acquire_timeout(Duration::from_millis(200))
        .connect_lazy("postgres://postgres@127.0.0.1:1/billing")
        .expect("valid lazy PostgreSQL test URL");
    router(
        BillingSubscriptionsState::new(pool, None, Arc::new(FixtureAuth))
            .with_console_access_gate(),
    )
}

async fn error_body(response: axum::response::Response) -> serde_json::Value {
    let bytes = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    serde_json::from_slice(&bytes).expect("legacy JSON error envelope")
}

#[tokio::test]
async fn subscription_routes_should_reject_missing_bearer_token_before_database_access() {
    let response = smoke_router()
        .oneshot(
            Request::builder()
                .method("GET")
                .uri("/api/subscription/plans")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(
        error_body(response).await,
        json!({"success": false, "message": "Unauthorized, invalid access token"})
    );
}

#[tokio::test]
async fn l0_subscription_plan_reads_are_hidden_before_database_access() {
    let response = console_gate_smoke_router()
        .oneshot(
            Request::builder()
                .method("GET")
                .uri("/api/subscription/plans")
                .header("authorization", "Bearer user")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::NOT_FOUND);
    assert_eq!(error_body(response).await, json!({"message": "Not Found"}));
}

#[tokio::test]
async fn l0_subscription_plan_deletes_are_hidden_before_database_access() {
    let response = console_gate_smoke_router()
        .oneshot(
            Request::builder()
                .method("DELETE")
                .uri("/api/subscription/admin/plans/1")
                .header("authorization", "Bearer user")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::NOT_FOUND);
    assert_eq!(error_body(response).await, json!({"message": "Not Found"}));
}

#[tokio::test]
async fn subscription_admin_routes_should_require_an_administrator() {
    let response = smoke_router()
        .oneshot(
            Request::builder()
                .method("GET")
                .uri("/api/subscription/admin/plans")
                .header("authorization", "Bearer user")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert_eq!(
        error_body(response).await,
        json!({
            "success": false,
            "code": "AUTH_INSUFFICIENT_PRIVILEGE",
            "message": "Unauthorized, insufficient privileges"
        })
    );
}

#[tokio::test]
async fn subscription_admin_auth_precedes_malformed_json_rejection() {
    let response = smoke_router()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/subscription/admin/plans")
                .header("content-type", "application/json")
                .body(Body::from("{"))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(
        error_body(response).await,
        json!({
            "success": false,
            "message": "Unauthorized, invalid access token"
        })
    );
}

#[tokio::test]
async fn authenticated_subscription_extractor_failures_preserve_auth_version() {
    let response = smoke_router()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/subscription/admin/plans")
                .header("authorization", "Bearer admin")
                .header("content-type", "application/json")
                .body(Body::from("{"))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(
        response
            .headers()
            .get("auth-version")
            .and_then(|value| value.to_str().ok()),
        Some("864b7076dbcd0a3c01b5520316720ebf")
    );
    assert!(matches!(
        response.status(),
        StatusCode::BAD_REQUEST | StatusCode::UNPROCESSABLE_ENTITY
    ));
}

#[tokio::test]
async fn subscription_self_degrades_database_read_failures_to_empty_lists() {
    let response = smoke_router()
        .oneshot(
            Request::builder()
                .method("GET")
                .uri("/api/subscription/self")
                .header("authorization", "Bearer user")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        error_body(response).await,
        json!({
            "success": true,
            "message": "",
            "data": {
                "billing_preference": "subscription_first",
                "subscriptions": [],
                "all_subscriptions": []
            }
        })
    );
}

#[tokio::test]
async fn subscription_routes_should_preserve_legacy_method_boundaries() {
    let response = smoke_router()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/subscription/plans")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::METHOD_NOT_ALLOWED);
}

/// This is intentionally ignored by default: it fails if the caller has not
/// explicitly supplied disposable loopback PostgreSQL 18 and Valkey services.
#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; no production services"]
async fn subscription_admin_routes_preserve_tcp_contract_atomicity_and_cache_recovery() {
    let database_url = env::var("LMM_BILLING_SUBSCRIPTIONS_TEST_DATABASE_URL")
        .expect("set isolated PostgreSQL 18 URL");
    let valkey_url =
        env::var("LMM_BILLING_SUBSCRIPTIONS_TEST_VALKEY_URL").expect("set isolated Valkey URL");
    let pool = PgPoolOptions::new()
        .max_connections(4)
        .acquire_timeout(Duration::from_secs(3))
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL must be reachable");
    let version: String = sqlx::query_scalar("SHOW server_version")
        .fetch_one(&pool)
        .await
        .expect("PostgreSQL version");
    assert!(
        version.starts_with("18."),
        "requires PostgreSQL 18, got {version}"
    );
    reset_schema(&pool).await;

    let valkey = redis::Client::open(valkey_url).expect("isolated Valkey URL");
    let mut cache = valkey
        .get_multiplexed_async_connection()
        .await
        .expect("Valkey connection");
    redis::cmd("FLUSHDB")
        .query_async::<()>(&mut cache)
        .await
        .expect("reset Valkey");
    seed(&pool).await;

    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("loopback listener");
    let address = listener.local_addr().expect("listener address");
    let app = router(BillingSubscriptionsState::new(
        pool.clone(),
        Some(valkey.clone()),
        Arc::new(FixtureAuth),
    ));
    let server = tokio::spawn(async move { axum::serve(listener, app).await.expect("serve") });
    let client = reqwest::Client::new();
    let bind_url = format!("http://{address}/api/subscription/admin/bind");

    let rollback = client
        .post(&bind_url)
        .bearer_auth("admin")
        .json(&json!({"user_id": 7, "plan_id": 3}))
        .send()
        .await
        .expect("TCP rollback response");
    assert_eq!(rollback.status(), reqwest::StatusCode::OK);
    assert_eq!(
        rollback.headers()[reqwest::header::CONTENT_TYPE],
        "application/json"
    );
    assert_eq!(
        rollback.headers()["auth-version"],
        "864b7076dbcd0a3c01b5520316720ebf"
    );
    assert_eq!(
        rollback.json::<serde_json::Value>().await.expect("JSON")["success"],
        false
    );
    assert_eq!(
        user_group(&pool).await,
        "default",
        "failed insert rolls back upgrade"
    );
    assert_eq!(subscription_count(&pool).await, 0);

    sqlx::query("UPDATE subscription_plans SET total_amount=10 WHERE id=3")
        .execute(&pool)
        .await
        .expect("repair fixture plan");
    redis::cmd("SET")
        .arg("user:7")
        .arg("stale")
        .query_async::<()>(&mut cache)
        .await
        .expect("user cache fixture");
    redis::cmd("SET")
        .arg("new-api:subscription_plan_info:v1:sub:obsolete")
        .arg("stale")
        .query_async::<()>(&mut cache)
        .await
        .expect("plan info cache fixture");
    let created = client
        .post(&bind_url)
        .bearer_auth("admin")
        .json(&json!({"user_id": 7, "plan_id": 3}))
        .send()
        .await
        .expect("TCP create response");
    assert_eq!(created.status(), reqwest::StatusCode::OK);
    assert_eq!(
        created.json::<serde_json::Value>().await.expect("JSON")["success"],
        true
    );
    assert_eq!(user_group(&pool).await, "pro");
    assert_eq!(subscription_count(&pool).await, 1);
    assert_eq!(exists(&mut cache, "user:7").await, 0);
    assert_eq!(
        exists(&mut cache, "new-api:subscription_plan_info:v1:sub:obsolete").await,
        0
    );

    let subscription_id: i64 = sqlx::query_scalar("SELECT id FROM user_subscriptions")
        .fetch_one(&pool)
        .await
        .expect("created subscription id");
    redis::cmd("SET")
        .arg("user:7")
        .arg("stale")
        .query_async::<()>(&mut cache)
        .await
        .expect("user cache fixture");
    let cancelled = client
        .post(format!("http://{address}/api/subscription/admin/user_subscriptions/{subscription_id}/invalidate"))
        .bearer_auth("admin")
        .send()
        .await
        .expect("TCP cancel response");
    assert_eq!(cancelled.status(), reqwest::StatusCode::OK);
    assert_eq!(
        cancelled.json::<serde_json::Value>().await.expect("JSON")["success"],
        true
    );
    assert_eq!(user_group(&pool).await, "default");
    assert_eq!(exists(&mut cache, "user:7").await, 0);

    sqlx::query("INSERT INTO subscription_plans (id,title,price_amount,enabled,total_amount,duration_unit,duration_value,upgrade_group,downgrade_group) VALUES (4,'Disposable',1,TRUE,10,'day',1,'','')")
        .execute(&pool)
        .await
        .expect("unused plan fixture");
    let deleted = client
        .delete(format!("http://{address}/api/subscription/admin/plans/4"))
        .bearer_auth("admin")
        .send()
        .await
        .expect("TCP delete response");
    assert_eq!(deleted.status(), reqwest::StatusCode::OK);
    assert_eq!(
        deleted.headers()["auth-version"],
        "864b7076dbcd0a3c01b5520316720ebf"
    );
    assert_eq!(
        deleted
            .json::<serde_json::Value>()
            .await
            .expect("delete JSON")["success"],
        true
    );
    let audit: String = sqlx::query_scalar(
        "SELECT other FROM logs WHERE content='DELETE /api/subscription/admin/plans/4'",
    )
    .fetch_one(&pool)
    .await
    .expect("atomic plan-delete audit");
    assert_eq!(
        serde_json::from_str::<serde_json::Value>(&audit).expect("audit JSON")["op"]["action"],
        "subscription.plan_delete"
    );
    server.abort();
}

/// This is intentionally ignored by default: it requires disposable
/// PostgreSQL 18 and Valkey, and proves the background maintenance side
/// effects independently from the HTTP route tests.
#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; no production services"]
async fn subscription_maintenance_expires_resets_and_cleans() {
    let database_url = env::var("LMM_BILLING_SUBSCRIPTIONS_TEST_DATABASE_URL")
        .expect("set isolated PostgreSQL 18 URL");
    let valkey_url =
        env::var("LMM_BILLING_SUBSCRIPTIONS_TEST_VALKEY_URL").expect("set isolated Valkey URL");
    let pool = PgPoolOptions::new()
        .max_connections(4)
        .acquire_timeout(Duration::from_secs(3))
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL must be reachable");
    let version: String = sqlx::query_scalar("SHOW server_version")
        .fetch_one(&pool)
        .await
        .expect("PostgreSQL version");
    assert!(
        version.starts_with("18."),
        "requires PostgreSQL 18, got {version}"
    );
    reset_schema(&pool).await;

    let valkey = redis::Client::open(valkey_url).expect("isolated Valkey URL");
    let mut cache = valkey
        .get_multiplexed_async_connection()
        .await
        .expect("Valkey connection");
    redis::cmd("FLUSHDB")
        .query_async::<()>(&mut cache)
        .await
        .expect("reset Valkey");

    let current: i64 = sqlx::query_scalar("SELECT EXTRACT(EPOCH FROM NOW())::BIGINT")
        .fetch_one(&pool)
        .await
        .expect("database clock");
    sqlx::query("INSERT INTO users (id, \"group\", setting, deleted_at) VALUES (7,'pro','{}',NULL),(8,'default','{}',NULL)")
        .execute(&pool)
        .await
        .expect("users fixture");
    sqlx::query("INSERT INTO subscription_plans (id,title,price_amount,enabled,total_amount,duration_unit,duration_value,quota_reset_period,quota_reset_custom_seconds,upgrade_group,downgrade_group) VALUES (3,'Daily',1,TRUE,1000,'day',1,'daily',0,'pro','')")
        .execute(&pool)
        .await
        .expect("plan fixture");
    sqlx::query("INSERT INTO user_subscriptions (id,user_id,plan_id,amount_total,amount_used,start_time,end_time,status,source,last_reset_time,next_reset_time,upgrade_group,prev_user_group,downgrade_group,allow_wallet_overflow,created_at,updated_at) VALUES (11,7,3,1000,50,$1,$2,'active','admin',0,0,'pro','default','',TRUE,$3,$3),(12,8,3,1000,875,$4,$5,'active','admin',$6,1,'','','',TRUE,$7,$7)")
        .bind(current - 172_800)
        .bind(current - 1)
        .bind(current - 1)
        .bind(current - 172_800)
        .bind(current + 172_800)
        .bind(current - 172_800)
        .bind(current - 172_800)
        .execute(&pool)
        .await
        .expect("subscriptions fixture");
    sqlx::query("INSERT INTO subscription_pre_consume_records (request_id,user_id,user_subscription_id,pre_consumed,status,created_at,updated_at) VALUES ('old',8,12,10,'consumed',$1,$1),('fresh',8,12,10,'consumed',$2,$2)")
        .bind(current - 8 * 24 * 60 * 60)
        .bind(current)
        .execute(&pool)
        .await
        .expect("pre-consume fixture");
    redis::cmd("SET")
        .arg("user:7")
        .arg("stale")
        .query_async::<()>(&mut cache)
        .await
        .expect("user cache fixture");

    maintenance_once(&pool, Some(&valkey))
        .await
        .expect("maintenance pass");

    let expired: (String, String) = sqlx::query_as(
        "SELECT status, (SELECT \"group\" FROM users WHERE id=7) FROM user_subscriptions WHERE id=11",
    )
    .fetch_one(&pool)
    .await
    .expect("expired subscription");
    assert_eq!(expired, ("expired".to_owned(), "default".to_owned()));
    let reset: (i64, i64, i64) = sqlx::query_as(
        "SELECT amount_used,last_reset_time,next_reset_time FROM user_subscriptions WHERE id=12",
    )
    .fetch_one(&pool)
    .await
    .expect("reset subscription");
    assert_eq!(reset.0, 0);
    assert!(reset.1 > 0 && reset.2 > current);
    let remaining_records: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM subscription_pre_consume_records")
            .fetch_one(&pool)
            .await
            .expect("pre-consume count");
    assert_eq!(remaining_records, 1);
    assert_eq!(exists(&mut cache, "user:7").await, 0);
}

async fn exists(connection: &mut redis::aio::MultiplexedConnection, key: &str) -> i64 {
    redis::cmd("EXISTS")
        .arg(key)
        .query_async(connection)
        .await
        .expect("cache existence")
}

async fn user_group(pool: &PgPool) -> String {
    sqlx::query_scalar("SELECT \"group\" FROM users WHERE id=7")
        .fetch_one(pool)
        .await
        .expect("user group")
}

async fn subscription_count(pool: &PgPool) -> i64 {
    sqlx::query_scalar("SELECT COUNT(*) FROM user_subscriptions")
        .fetch_one(pool)
        .await
        .expect("subscription count")
}

async fn seed(pool: &PgPool) {
    sqlx::query("INSERT INTO options (key,value) VALUES ('payment_setting.compliance_confirmed','true'),('payment_setting.compliance_terms_version','v1')")
        .execute(pool).await.expect("compliance fixture");
    sqlx::query(
        "INSERT INTO users (id, \"group\", setting, deleted_at) VALUES (7,'default','{}',NULL)",
    )
    .execute(pool)
    .await
    .expect("user fixture");
    sqlx::query("INSERT INTO subscription_plans (id,title,price_amount,enabled,total_amount,duration_unit,duration_value,upgrade_group,downgrade_group) VALUES (3,'Pro',1,TRUE,0,'day',1,'pro','')")
        .execute(pool).await.expect("plan fixture");
}

async fn reset_schema(pool: &PgPool) {
    for table in [
        "logs",
        "subscription_orders",
        "user_subscriptions",
        "subscription_pre_consume_records",
        "subscription_plans",
        "users",
        "options",
    ] {
        sqlx::query(&format!("DROP TABLE IF EXISTS {table} CASCADE"))
            .execute(pool)
            .await
            .expect("drop isolated table");
    }
    sqlx::query("CREATE TABLE options (key TEXT PRIMARY KEY, value TEXT NOT NULL)")
        .execute(pool)
        .await
        .expect("options schema");
    sqlx::query("CREATE TABLE users (id BIGINT PRIMARY KEY, \"group\" TEXT NOT NULL, setting TEXT NOT NULL DEFAULT '{}', deleted_at TIMESTAMPTZ)")
        .execute(pool).await.expect("users schema");
    sqlx::query("CREATE TABLE subscription_plans (id BIGINT PRIMARY KEY, title TEXT NOT NULL, subtitle TEXT, price_amount NUMERIC NOT NULL, currency TEXT, duration_unit TEXT, duration_value BIGINT, custom_seconds BIGINT, enabled BOOLEAN NOT NULL, sort_order BIGINT, allow_balance_pay BOOLEAN, allow_wallet_overflow BOOLEAN, stripe_price_id TEXT, creem_product_id TEXT, waffo_pancake_product_id TEXT, max_purchase_per_user BIGINT, total_amount BIGINT NOT NULL, upgrade_group TEXT, downgrade_group TEXT, quota_reset_period TEXT, quota_reset_custom_seconds BIGINT, created_at BIGINT, updated_at BIGINT)")
        .execute(pool).await.expect("plans schema");
    sqlx::query("CREATE TABLE user_subscriptions (id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL, plan_id BIGINT NOT NULL, amount_total BIGINT NOT NULL CHECK (amount_total > 0), amount_used BIGINT NOT NULL, start_time BIGINT NOT NULL, end_time BIGINT NOT NULL, status TEXT NOT NULL, source TEXT NOT NULL, last_reset_time BIGINT NOT NULL, next_reset_time BIGINT NOT NULL, upgrade_group TEXT NOT NULL, prev_user_group TEXT NOT NULL, downgrade_group TEXT NOT NULL, allow_wallet_overflow BOOLEAN NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)")
        .execute(pool).await.expect("subscriptions schema");
    sqlx::query("CREATE TABLE subscription_pre_consume_records (id BIGSERIAL PRIMARY KEY, request_id TEXT NOT NULL, user_id BIGINT NOT NULL, user_subscription_id BIGINT NOT NULL, pre_consumed BIGINT NOT NULL, status TEXT NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)")
        .execute(pool).await.expect("pre-consume schema");
    sqlx::query(
        "CREATE TABLE subscription_orders (id BIGSERIAL PRIMARY KEY, plan_id BIGINT NOT NULL)",
    )
    .execute(pool)
    .await
    .expect("orders schema");
    sqlx::query("CREATE TABLE logs (user_id BIGINT, created_at BIGINT, type BIGINT, content TEXT, username TEXT, ip TEXT, other TEXT)")
        .execute(pool).await.expect("logs schema");
}
