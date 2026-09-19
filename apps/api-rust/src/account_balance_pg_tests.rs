//! Exercises the production PostgreSQL store through the mounted HTTP handlers.
use super::PgPublicCatalogStore;
use axum::{
    Router,
    body::{Body, to_bytes},
    http::Request,
};
use lmm_api_rs::routes::{
    api_token::{ApiTokenHttpState, ApiTokenPrincipal, PgValkeyApiTokenService, api_token_router},
    public_catalog::{
        PublicCatalogAuthError, PublicCatalogAuthorizer, PublicCatalogPrincipal,
        PublicCatalogState, ValkeyAccountBalanceRateLimiter, public_catalog_router,
    },
};
use serde_json::{Value, json};
use sqlx::postgres::PgPoolOptions;
use std::{sync::Arc, time::Duration};
use tower::ServiceExt;

struct NoDashboard;
#[async_trait::async_trait]
impl PublicCatalogAuthorizer for NoDashboard {
    async fn principal(
        &self,
        _: &axum::http::HeaderMap,
    ) -> Result<PublicCatalogPrincipal, PublicCatalogAuthError> {
        Err(PublicCatalogAuthError::Unauthorized)
    }
}

async fn query(app: &Router) -> (u16, Value) {
    query_with_key(app, "balance-fixture").await
}

async fn query_with_key(app: &Router, key: &str) -> (u16, Value) {
    let response = app
        .clone()
        .oneshot(
            Request::get("/v1/balance")
                .header("authorization", format!("Bearer sk-{key}"))
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.headers()["cache-control"], "no-store");
    let status = response.status().as_u16();
    let bytes = to_bytes(response.into_body(), 8192).await.unwrap();
    (
        status,
        serde_json::from_slice(&bytes).unwrap_or(Value::Null),
    )
}

async fn grant(app: &Router, owner: Option<i64>, enabled: bool) -> u16 {
    let mut request = Request::put("/api/token/1/account-balance-access")
        .header("content-type", "application/json")
        .header("authorization", "Bearer sk-balance-fixture");
    if let Some(user_id) = owner {
        request = request.extension(ApiTokenPrincipal {
            user_id,
            role: 1,
            preferred_language: None,
        });
    }
    app.clone()
        .oneshot(
            request
                .body(Body::from(json!({"enabled":enabled}).to_string()))
                .unwrap(),
        )
        .await
        .unwrap()
        .status()
        .as_u16()
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey; run real-integration-gates.sh"]
async fn durable_balance_defaults_revocation_and_read_only_contract() {
    let url = std::env::var("LMM_API_TOKEN_TEST_DATABASE_URL").unwrap();
    let valkey =
        redis::Client::open(std::env::var("LMM_API_TOKEN_TEST_VALKEY_URL").unwrap()).unwrap();
    let admin = PgPoolOptions::new()
        .max_connections(1)
        .connect(&url)
        .await
        .unwrap();
    let schema = format!("balance_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let search_path = format!("SET search_path TO {schema}");
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .after_connect(move |connection, _| {
            let statement = search_path.clone();
            Box::pin(async move {
                sqlx::query(&statement).execute(connection).await?;
                Ok(())
            })
        })
        .connect(&url)
        .await
        .unwrap();
    // A private schema avoids resetting any other suite's tables.
    sqlx::raw_sql("CREATE TABLE users (id BIGINT PRIMARY KEY, status BIGINT, quota BIGINT, deleted_at TIMESTAMPTZ);
        CREATE TABLE tokens (id BIGINT PRIMARY KEY, user_id BIGINT, key TEXT, status BIGINT,
        expired_time BIGINT, allow_ips TEXT, oauth_managed BOOLEAN DEFAULT FALSE,
        account_balance_read BOOLEAN DEFAULT FALSE, deleted_at TIMESTAMPTZ,
        accessed_time BIGINT DEFAULT 123, used_quota BIGINT DEFAULT 456);
        CREATE TABLE options (key TEXT PRIMARY KEY, value TEXT);
        INSERT INTO users VALUES (7, 1, 1250000, NULL);
        INSERT INTO tokens (id,user_id,key,status,expired_time) VALUES (1,7,'balance-fixture',1,-1);")
        .execute(&pool).await.unwrap();
    let app = public_catalog_router(PublicCatalogState::new(
        Arc::new(PgPublicCatalogStore::new(pool.clone())),
        Arc::new(NoDashboard),
    ))
    .merge(api_token_router(ApiTokenHttpState::new(Arc::new(
        PgValkeyApiTokenService::new(pool.clone(), valkey.clone()),
    ))));
    assert_eq!(
        query(&app).await,
        (
            403,
            json!({"valid":false,"error":"account_balance_access_required"})
        )
    );
    assert_ne!(
        grant(&app, None, true).await,
        200,
        "API key cannot supply a dashboard principal"
    );
    assert_eq!(grant(&app, Some(8), true).await, 404);
    assert_eq!(grant(&app, Some(7), true).await, 200);
    let before: Value = sqlx::query_scalar("SELECT to_jsonb(tokens) FROM tokens WHERE id=1")
        .fetch_one(&pool)
        .await
        .unwrap();
    let (status, body) = query(&app).await;
    assert_eq!(
        status, 200,
        "missing QuotaPerUnit must use Go's 500000 default: {body}"
    );
    assert_eq!(body["remaining"], 2.5);
    assert_eq!(body["currency"], "USD");
    assert_eq!(body["scope"], "account");
    assert_eq!(body["consistency"], "persisted_snapshot");
    let now = chrono::Utc::now().timestamp();
    assert!((now - body["updated_at"].as_i64().unwrap()).abs() < 10);
    let after: Value = sqlx::query_scalar("SELECT to_jsonb(tokens) FROM tokens WHERE id=1")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(
        before, after,
        "queries must not change access time or usage"
    );
    let vectors: Vec<Value> = serde_json::from_str(include_str!(
        "../tests/fixtures/account-balance-parity.json"
    ))
    .unwrap();
    for vector in vectors {
        sqlx::query("UPDATE users SET quota=$1")
            .bind(vector["quota"].as_i64().unwrap())
            .execute(&pool)
            .await
            .unwrap();
        sqlx::query("UPDATE tokens SET status=$1,account_balance_read=$2")
            .bind(vector["status"].as_i64().unwrap())
            .bind(vector["grant"].as_bool().unwrap())
            .execute(&pool)
            .await
            .unwrap();
        sqlx::query("INSERT INTO options VALUES ('QuotaPerUnit',$1) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value")
            .bind(vector["unit"].to_string()).execute(&pool).await.unwrap();
        let (status, mut body) = query(&app).await;
        assert_eq!(
            u64::from(status),
            vector["expected_status"].as_u64().unwrap(),
            "{}",
            vector["name"]
        );
        if status == 200 {
            assert!(
                (chrono::Utc::now().timestamp() - body["updated_at"].as_i64().unwrap()).abs() < 10
            );
            body.as_object_mut().unwrap().remove("updated_at");
            assert_eq!(
                body,
                json!({"valid":true,"scope":"account","currency":"USD","remaining":vector["remaining"],"consistency":"persisted_snapshot"})
            );
        } else {
            assert_eq!(body, json!({"valid":false,"error":vector["error"]}));
        }
    }
    sqlx::query("DELETE FROM options")
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query("UPDATE tokens SET status=1,account_balance_read=TRUE")
        .execute(&pool)
        .await
        .unwrap();
    for (quota, remaining) in [(0_i64, 0.0), (-625000, -1.25)] {
        sqlx::query("UPDATE users SET quota=$1")
            .bind(quota)
            .execute(&pool)
            .await
            .unwrap();
        assert_eq!(query(&app).await.1["remaining"], remaining);
        let persisted: i64 = sqlx::query_scalar("SELECT quota FROM users")
            .fetch_one(&pool)
            .await
            .unwrap();
        assert_eq!(persisted, quota);
    }
    sqlx::query("INSERT INTO options VALUES ('QuotaPerUnit', '100'), ('QuotaDisplayType', 'CNY')")
        .execute(&pool)
        .await
        .unwrap();
    assert_eq!(query(&app).await.1["remaining"], -6250.0);
    for invalid in ["", "0", "-1", "NaN", "inf", "invalid"] {
        sqlx::query("UPDATE options SET value=$1 WHERE key='QuotaPerUnit'")
            .bind(invalid)
            .execute(&pool)
            .await
            .unwrap();
        assert_eq!(
            query(&app).await.0,
            503,
            "explicit invalid value must not default"
        );
    }
    sqlx::query("DELETE FROM options WHERE key='QuotaPerUnit'")
        .execute(&pool)
        .await
        .unwrap();
    assert_eq!(grant(&app, Some(7), false).await, 200);
    assert_eq!(
        query(&app).await.0,
        403,
        "same router must see durable revocation immediately"
    );
    assert_eq!(grant(&app, Some(7), true).await, 200);
    // SQL NULL must not become an enabled, never-expiring manual key. Go
    // decodes nullable integer fields as zero and requires oauth_managed=false.
    for change in ["status=NULL", "expired_time=NULL", "oauth_managed=NULL"] {
        sqlx::query(&format!("UPDATE tokens SET {change}"))
            .execute(&pool)
            .await
            .unwrap();
        assert_eq!(query(&app).await.0, 401, "{change}");
        if change == "oauth_managed=NULL" {
            assert_eq!(grant(&app, Some(7), true).await, 404);
        }
        sqlx::query("UPDATE tokens SET status=1,expired_time=-1,oauth_managed=FALSE")
            .execute(&pool)
            .await
            .unwrap();
    }
    for (change, expected) in [
        ("status=4", 200),
        ("status=2", 401),
        ("status=1,expired_time=1", 401),
        ("expired_time=-1,oauth_managed=TRUE", 401),
        ("oauth_managed=FALSE,allow_ips='10.0.0.0/8'", 403),
        ("allow_ips=NULL,deleted_at=NOW()", 401),
    ] {
        sqlx::query(&format!("UPDATE tokens SET {change}"))
            .execute(&pool)
            .await
            .unwrap();
        assert_eq!(query(&app).await.0, expected, "{change}");
    }
    sqlx::query("UPDATE tokens SET deleted_at=NULL; ")
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query("UPDATE users SET status=2")
        .execute(&pool)
        .await
        .unwrap();
    assert_eq!(query(&app).await.0, 401);
    sqlx::query("UPDATE users SET status=1")
        .execute(&pool)
        .await
        .unwrap();
    // Use a unique account ID so no existing suite's limiter key is touched.
    let limiter_owner = chrono::Utc::now().timestamp_micros();
    sqlx::query("UPDATE users SET id=$1")
        .bind(limiter_owner)
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query("UPDATE tokens SET user_id=$1")
        .bind(limiter_owner)
        .execute(&pool)
        .await
        .unwrap();
    let limited = public_catalog_router(
        PublicCatalogState::new(
            Arc::new(PgPublicCatalogStore::new(pool.clone())),
            Arc::new(NoDashboard),
        )
        .with_account_balance_rate_limiter(Arc::new(ValkeyAccountBalanceRateLimiter::new(
            valkey,
            30,
            Duration::from_secs(60),
            Duration::from_secs(2),
        ))),
    );
    for _ in 0..30 {
        assert_eq!(query(&limited).await.0, 200);
    }
    sqlx::query("INSERT INTO tokens (id,user_id,key,status,expired_time,account_balance_read) VALUES (2,$1,'second-balance-key',1,-1,TRUE)")
        .bind(limiter_owner).execute(&pool).await.unwrap();
    assert_eq!(
        query_with_key(&limited, "second-balance-key").await.0,
        429,
        "a second key must share its owner's 30-request allowance"
    );
    pool.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
}
