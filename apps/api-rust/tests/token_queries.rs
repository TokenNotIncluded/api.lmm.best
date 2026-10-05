use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{Request, StatusCode},
};
use lmm_api_rs::{
    ClientIpKey,
    auth::CriticalRateLimitOutcome,
    routes::{
        public_catalog::{
            AccountBalanceRateLimiter, PublicCatalogStoreError, ValkeyAccountBalanceRateLimiter,
        },
        token_queries::{TokenQueryState, router},
    },
};
use serde_json::{Value, json};
use sqlx::{PgPool, postgres::PgPoolOptions};
use std::{
    sync::{Arc, Mutex},
    time::Duration,
};
use tower::ServiceExt;

#[derive(Default)]
struct Allowed(Mutex<Vec<i64>>);
#[async_trait]
impl AccountBalanceRateLimiter for Allowed {
    async fn check(&self, user: i64) -> Result<CriticalRateLimitOutcome, PublicCatalogStoreError> {
        self.0.lock().unwrap().push(user);
        Ok(CriticalRateLimitOutcome::Allowed)
    }
}
fn request(path: &str, authorization: &str) -> Request<Body> {
    let mut request = Request::get(path)
        .header("authorization", authorization)
        .body(Body::empty())
        .unwrap();
    request
        .extensions_mut()
        .insert(ClientIpKey("127.0.0.1".into()));
    request
}
async fn body(response: axum::response::Response) -> Value {
    serde_json::from_slice(&to_bytes(response.into_body(), 65536).await.unwrap()).unwrap()
}

#[tokio::test]
async fn only_exact_bearer_api_keys_reach_token_storage_and_oauth_is_rejected() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgresql://fixture:fixture@127.0.0.1:1/unused")
        .unwrap();
    let limiter = Arc::new(Allowed::default());
    let app = router(TokenQueryState::new(pg, limiter.clone()));
    for (path, key) in [
        ("/v1/usage", ""),
        ("/v1/usage", "raw-key"),
        ("/v1/usage", "Basic api-key"),
        ("/v1/usage", "Bearer lmm_access"),
        ("/v1/usage?access_token=", "Bearer valid"),
        ("/v1/usage?key=lmm_access", "Bearer valid"),
    ] {
        let response = app.clone().oneshot(request(path, key)).await.unwrap();
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(response.headers()["pragma"], "no-cache");
        assert_eq!(
            body(response).await,
            json!({"valid":false,"error":"invalid_api_key"})
        );
    }
    for (header, value) in [
        ("x-lmm-group", "default"),
        ("x-api-key", "LMM_access"),
        ("x-goog-api-key", "oauth_managed_key"),
        ("sec-websocket-protocol", "lmm_access"),
    ] {
        let mut probe = request("/v1/usage", "Bearer valid");
        probe.headers_mut().insert(header, value.parse().unwrap());
        assert_eq!(
            app.clone().oneshot(probe).await.unwrap().status(),
            StatusCode::UNAUTHORIZED
        );
    }
    assert!(limiter.0.lock().unwrap().is_empty());
}

struct Fixture {
    admin: PgPool,
    pg: PgPool,
    schema: String,
}
impl Fixture {
    async fn new() -> Self {
        let url = std::env::var("LMM_TEST_DATABASE_URL").expect("isolated PostgreSQL");
        let admin = PgPool::connect(&url).await.unwrap();
        let schema = format!("token_queries_{}", uuid::Uuid::new_v4().simple());
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&admin)
            .await
            .unwrap();
        let pg = PgPoolOptions::new()
            .max_connections(4)
            .after_connect({
                let schema = schema.clone();
                move |connection, _| {
                    let statement = format!("SET search_path TO {schema}");
                    Box::pin(async move {
                        sqlx::query(&statement).execute(connection).await?;
                        Ok(())
                    })
                }
            })
            .connect(&url)
            .await
            .unwrap();
        sqlx::raw_sql("CREATE TABLE users(id BIGINT PRIMARY KEY,status BIGINT,quota BIGINT,deleted_at TIMESTAMPTZ);CREATE TABLE tokens(id BIGINT PRIMARY KEY,user_id BIGINT,key TEXT,status BIGINT,expired_time BIGINT,allow_ips TEXT,remain_quota BIGINT,used_quota BIGINT,unlimited_quota BOOLEAN,oauth_managed BOOLEAN,accessed_time BIGINT,deleted_at TIMESTAMPTZ);CREATE TABLE logs(user_id BIGINT,token_id BIGINT,type BIGINT,quota BIGINT,created_at BIGINT);CREATE TABLE options(key TEXT PRIMARY KEY,value TEXT);INSERT INTO users VALUES(1,1,999999,NULL);INSERT INTO tokens VALUES(11,1,'query-key',1,-1,'',8850,12080,FALSE,FALSE,17,NULL);INSERT INTO options VALUES('QuotaPerUnit','100'),('LogConsumeEnabled','true');").execute(&pg).await.unwrap();
        Self { admin, pg, schema }
    }
    fn app(&self) -> axum::Router {
        router(TokenQueryState::new(
            self.pg.clone(),
            Arc::new(Allowed::default()),
        ))
    }
    async fn cleanup(self) {
        self.pg.close().await;
        sqlx::query(&format!("DROP SCHEMA {} CASCADE", self.schema))
            .execute(&self.admin)
            .await
            .unwrap();
        self.admin.close().await;
    }

    async fn pricing_schema(&self) {
        sqlx::raw_sql("ALTER TABLE users ADD COLUMN \"group\" TEXT DEFAULT 'default',ADD COLUMN role BIGINT DEFAULT 1,ADD COLUMN trust_level_override BIGINT DEFAULT 2,ADD COLUMN created_at BIGINT DEFAULT 0,ADD COLUMN last_api_activity_at BIGINT DEFAULT 0,ADD COLUMN console_activated_at BIGINT DEFAULT 0;ALTER TABLE tokens ADD COLUMN \"group\" TEXT DEFAULT '',ADD COLUMN auto_groups TEXT DEFAULT '',ADD COLUMN model_limits_enabled BOOLEAN DEFAULT FALSE,ADD COLUMN model_limits TEXT DEFAULT '';CREATE TABLE models(id BIGSERIAL PRIMARY KEY,model_name TEXT,status BIGINT DEFAULT 1,name_rule BIGINT DEFAULT 0,deleted_at TIMESTAMPTZ);CREATE TABLE abilities(model TEXT,\"group\" TEXT,channel_id BIGINT,enabled BOOLEAN);CREATE TABLE top_ups(user_id BIGINT,status TEXT,credited_quota BIGINT,amount BIGINT,settled_amount_micros BIGINT,expected_amount_micros BIGINT,money DOUBLE PRECISION,payment_provider TEXT,payment_method TEXT,settlement_currency TEXT,create_time BIGINT,complete_time BIGINT);")
            .execute(&self.pg).await.unwrap();
        sqlx::query("INSERT INTO options(key,value) VALUES('CreditsPerUSD','500000'),('LegacyPricingQuotaPerUnit','500000') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value").execute(&self.pg).await.unwrap();
    }
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn configured_token_prices_match_current_go_reference_live_maps_and_limits() {
    let fixture = Fixture::new().await;
    fixture.pricing_schema().await;
    let oracle: Value = if let Ok(path) = std::env::var("LMM_TOKEN_PRICING_GO_ORACLE_OUTPUT") {
        serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap()
    } else {
        serde_json::from_str(include_str!("fixtures/token-pricing-current-go.json")).unwrap()
    };
    for (key, value) in oracle["options"].as_object().unwrap() {
        sqlx::query("INSERT INTO options(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value").bind(key).bind(value.as_str().unwrap()).execute(&fixture.pg).await.unwrap();
    }
    for model in oracle["catalog"].as_array().unwrap() {
        let name = model["model_name"].as_str().unwrap();
        sqlx::query("INSERT INTO models(model_name) VALUES($1)")
            .bind(name)
            .execute(&fixture.pg)
            .await
            .unwrap();
        for group in model["enable_groups"].as_array().unwrap() {
            sqlx::query("INSERT INTO abilities VALUES($1,$2,1,TRUE)")
                .bind(name)
                .bind(group.as_str().unwrap())
                .execute(&fixture.pg)
                .await
                .unwrap();
        }
    }
    for case in oracle["cases"].as_array().unwrap() {
        let group = case["user_group"].as_str().unwrap();
        let groups = case["groups"].as_array().unwrap();
        let discount = case["discount"].as_f64().unwrap();
        let level = if discount == 0.97 {
            2
        } else if discount == 0.94 {
            3
        } else if discount == 0.9 {
            4
        } else {
            1
        };
        sqlx::query("UPDATE users SET \"group\"=$1,trust_level_override=$2 WHERE id=1")
            .bind(group)
            .bind(level as i64)
            .execute(&fixture.pg)
            .await
            .unwrap();
        let token_group = if groups.len() == 1 {
            groups[0].as_str().unwrap()
        } else {
            "auto"
        };
        let auto = if groups.len() == 1 {
            String::new()
        } else {
            case["groups"].to_string()
        };
        let limits = case["limits"]
            .as_object()
            .map(|values| values.keys().cloned().collect::<Vec<_>>().join(","))
            .unwrap_or_default();
        sqlx::query("UPDATE tokens SET \"group\"=$1,auto_groups=$2,model_limits_enabled=$3,model_limits=$4 WHERE id=11").bind(token_group).bind(auto).bind(case["limited"].as_bool().unwrap()).bind(limits).execute(&fixture.pg).await.unwrap();
        let path = format!("/v1/pricing?model={}", case["requested"].as_str().unwrap());
        let response = fixture
            .app()
            .oneshot(request(&path, "Bearer query-key"))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK, "{case}");
        assert_eq!(response.headers()["cache-control"], "no-store");
        let snapshot = body(response).await;
        assert_eq!(snapshot["scope"], "token");
        assert_eq!(snapshot["price_basis"], "configured_base_rates");
        assert_eq!(snapshot["final_cost_depends_on_usage"], true);
        let mut entries = snapshot["data"].as_array().unwrap().clone();
        entries.sort_by_key(|item| {
            (
                item["model"].as_str().unwrap().to_owned(),
                item["group"].as_str().unwrap().to_owned(),
            )
        });
        assert_eq!(
            entries,
            *case["entries"].as_array().unwrap(),
            "Go/Rust pricing case {}",
            case["name"]
        );
    }
    sqlx::query("UPDATE tokens SET \"group\"='default',model_limits_enabled=FALSE WHERE id=11")
        .execute(&fixture.pg)
        .await
        .unwrap();
    sqlx::query("UPDATE options SET value='{\"gpt-4o\":2.5}' WHERE key='ModelRatio'")
        .execute(&fixture.pg)
        .await
        .unwrap();
    let changed = body(
        fixture
            .app()
            .oneshot(request("/v1/pricing?model=gpt-4o", "Bearer query-key"))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(changed["data"][0]["model_ratio"], 2.5);
    sqlx::query("UPDATE users SET \"group\"='default',trust_level_override=2 WHERE id=1")
        .execute(&fixture.pg)
        .await
        .unwrap();
    sqlx::query("INSERT INTO options(key,value) VALUES('CreditsPerUSD','4000000'),('QuotaPerUnit','900000'),('USDExchangeRate','99') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value").execute(&fixture.pg).await.unwrap();
    let calibrated = body(
        fixture
            .app()
            .oneshot(request("/v1/pricing?model=gpt-4o", "Bearer query-key"))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(calibrated["data"][0]["input_price"], 0.60625);
    assert_eq!(calibrated["pricing_schema_version"], 2);
    sqlx::query("DELETE FROM options WHERE key='CreditsPerUSD'")
        .execute(&fixture.pg)
        .await
        .unwrap();
    let unavailable = fixture
        .app()
        .oneshot(request("/v1/pricing?model=gpt-4o", "Bearer query-key"))
        .await
        .unwrap();
    assert_eq!(unavailable.status(), StatusCode::SERVICE_UNAVAILABLE);
    assert_eq!(
        body(unavailable).await["message"],
        "pricing currency units unavailable"
    );
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn token_pricing_checks_permissions_before_query_validation_and_never_mutates_key() {
    let fixture = Fixture::new().await;
    fixture.pricing_schema().await;
    sqlx::raw_sql("INSERT INTO models(model_name) VALUES('gpt-4o');INSERT INTO abilities VALUES('gpt-4o','default',1,TRUE)").execute(&fixture.pg).await.unwrap();
    let long = format!("/v1/pricing?model={}", "x".repeat(513));
    assert_eq!(
        fixture
            .app()
            .oneshot(request(&long, "Bearer unknown"))
            .await
            .unwrap()
            .status(),
        StatusCode::UNAUTHORIZED
    );
    for change in ["status=4", "remain_quota=0"] {
        sqlx::query(&format!("UPDATE tokens SET {change} WHERE id=11"))
            .execute(&fixture.pg)
            .await
            .unwrap();
        let before: Value = sqlx::query_scalar("SELECT to_jsonb(tokens) FROM tokens WHERE id=11")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
        let response = fixture
            .app()
            .oneshot(request(&long, "Bearer query-key"))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(
            body(response).await,
            json!({"success":false,"message":"API key unavailable"})
        );
        assert_eq!(
            before,
            sqlx::query_scalar::<_, Value>("SELECT to_jsonb(tokens) FROM tokens WHERE id=11")
                .fetch_one(&fixture.pg)
                .await
                .unwrap()
        );
        sqlx::query("UPDATE tokens SET status=1,remain_quota=8850 WHERE id=11")
            .execute(&fixture.pg)
            .await
            .unwrap();
    }
    sqlx::query("UPDATE tokens SET \"group\"='private' WHERE id=11")
        .execute(&fixture.pg)
        .await
        .unwrap();
    let response = fixture
        .app()
        .oneshot(request(&long, "Bearer query-key"))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert_eq!(body(response).await["message"], "group unavailable");
    sqlx::query("UPDATE tokens SET \"group\"='' WHERE id=11")
        .execute(&fixture.pg)
        .await
        .unwrap();
    assert_eq!(
        fixture
            .app()
            .oneshot(request(&long, "Bearer query-key"))
            .await
            .unwrap()
            .status(),
        StatusCode::BAD_REQUEST
    );
    assert_eq!(
        fixture
            .app()
            .oneshot(request("/v1/pricing?model=missing", "Bearer query-key"))
            .await
            .unwrap()
            .status(),
        StatusCode::NOT_FOUND
    );
    sqlx::query("UPDATE models SET status=0")
        .execute(&fixture.pg)
        .await
        .unwrap();
    assert_eq!(
        fixture
            .app()
            .oneshot(request("/v1/pricing?model=gpt-4o", "Bearer query-key"))
            .await
            .unwrap()
            .status(),
        StatusCode::NOT_FOUND
    );
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn token_pricing_uses_shared_credited_trust_facts_and_excludes_internal_credits() {
    let fixture = Fixture::new().await;
    fixture.pricing_schema().await;
    sqlx::raw_sql("UPDATE users SET trust_level_override=NULL;UPDATE options SET value='1000' WHERE key='QuotaPerUnit';INSERT INTO options VALUES('ModelRatio','{\"priced\":1}'),('ModelPrice','{}');INSERT INTO models(model_name) VALUES('priced');INSERT INTO abilities VALUES('priced','default',1,TRUE)").execute(&fixture.pg).await.unwrap();
    let now = chrono::Utc::now().timestamp();
    for (provider, method, credit) in [("stripe", "stripe", 500000_i64), ("epay", "ldc", 9999999)] {
        sqlx::query(
            "INSERT INTO top_ups VALUES(1,'success',$1,0,1000000,1000000,1.0,$2,$3,'USD',$4,$4)",
        )
        .bind(credit)
        .bind(provider)
        .bind(method)
        .bind(now)
        .execute(&fixture.pg)
        .await
        .unwrap();
    }
    let quote = body(
        fixture
            .app()
            .oneshot(request("/v1/pricing?model=priced", "Bearer query-key"))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(quote["data"][0]["trust_discount_ratio"], 0.94);
    // USD prices use the immutable Credit anchor, even when legacy QuotaPerUnit changes.
    assert_eq!(quote["data"][0]["input_price"], 1.88);
    sqlx::query("UPDATE top_ups SET status='refunded' WHERE payment_provider='stripe'")
        .execute(&fixture.pg)
        .await
        .unwrap();
    let quote = body(
        fixture
            .app()
            .oneshot(request("/v1/pricing?model=priced", "Bearer query-key"))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(quote["data"][0]["trust_discount_ratio"], 1.0);
    sqlx::query("UPDATE users SET role=10")
        .execute(&fixture.pg)
        .await
        .unwrap();
    sqlx::query("DROP TABLE top_ups")
        .execute(&fixture.pg)
        .await
        .unwrap();
    let quote = body(
        fixture
            .app()
            .oneshot(request("/v1/pricing?model=priced", "Bearer query-key"))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(quote["data"][0]["trust_discount_ratio"], 0.9);
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn persisted_usage_is_exact_token_scoped_utc_and_never_changes_credentials() {
    let fixture = Fixture::new().await;
    let now = chrono::Utc::now().timestamp();
    let start = now - now.rem_euclid(86400);
    for (user, token, kind, quota, created) in [
        (1, 11, 2, 230, now),
        (1, 12, 2, 999, now),
        (2, 11, 2, 999, now),
        (1, 11, 3, 999, now),
        (1, 11, 2, 999, start - 1),
        (1, 11, 2, 999, now + 86400),
    ] {
        sqlx::query("INSERT INTO logs VALUES($1,$2,$3,$4,$5)")
            .bind(user as i64)
            .bind(token as i64)
            .bind(kind as i64)
            .bind(quota as i64)
            .bind(created)
            .execute(&fixture.pg)
            .await
            .unwrap();
    }
    let before: Value = sqlx::query_scalar("SELECT to_jsonb(tokens) FROM tokens WHERE id=11")
        .fetch_one(&fixture.pg)
        .await
        .unwrap();
    let response = fixture
        .app()
        .oneshot(request("/v1/usage", "Bearer sk-query-key"))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(response.headers()["cache-control"], "no-store");
    let snapshot = body(response).await;
    assert_eq!(snapshot["valid"], true);
    assert_eq!(snapshot["currency"], "USD");
    assert_eq!(snapshot["scope"], "token");
    assert_eq!(snapshot["remaining"], 88.5);
    assert_eq!(snapshot["used_total"], 120.8);
    assert_eq!(snapshot["total_quota"], 209.3);
    let read_at = snapshot["updated_at"].as_i64().unwrap();
    assert_eq!(
        snapshot["used_today"],
        if read_at - read_at.rem_euclid(86400) == start {
            json!(2.3)
        } else {
            json!(0.0)
        }
    );
    assert_eq!(snapshot["used_today_source"], "retained_consumption_logs");
    assert_eq!(snapshot["day_timezone"], "UTC");
    assert!(!snapshot.to_string().contains("query-key"));
    let after: Value = sqlx::query_scalar("SELECT to_jsonb(tokens) FROM tokens WHERE id=11")
        .fetch_one(&fixture.pg)
        .await
        .unwrap();
    assert_eq!(before, after);
    sqlx::query("UPDATE tokens SET unlimited_quota=TRUE,status=4 WHERE id=11")
        .execute(&fixture.pg)
        .await
        .unwrap();
    sqlx::query("UPDATE options SET value='false' WHERE key='LogConsumeEnabled'")
        .execute(&fixture.pg)
        .await
        .unwrap();
    sqlx::query("DROP TABLE logs")
        .execute(&fixture.pg)
        .await
        .unwrap();
    let snapshot = body(
        fixture
            .app()
            .oneshot(request("/v1/usage", "bearer query-key"))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(snapshot["valid"], true);
    assert_eq!(snapshot["unlimited"], true);
    assert_eq!(snapshot["remaining"], Value::Null);
    assert_eq!(snapshot["total_quota"], Value::Null);
    assert_eq!(snapshot["used_today"], Value::Null);
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn quota_query_auth_checks_exact_key_expiry_owner_oauth_and_ip_without_status_writes() {
    let fixture = Fixture::new().await;
    for key in [
        "Bearer query-key-1",
        "Bearer query",
        "Bearer sk-query-key-extra",
    ] {
        assert_eq!(
            fixture
                .app()
                .oneshot(request("/v1/usage", key))
                .await
                .unwrap()
                .status(),
            StatusCode::UNAUTHORIZED
        );
    }
    for sql in [
        "UPDATE tokens SET status=2",
        "UPDATE tokens SET expired_time=0",
        "UPDATE tokens SET oauth_managed=TRUE",
        "UPDATE tokens SET deleted_at=NOW()",
        "UPDATE users SET status=2",
        "UPDATE users SET deleted_at=NOW()",
    ] {
        sqlx::query(sql).execute(&fixture.pg).await.unwrap();
        let before: Value = sqlx::query_scalar("SELECT to_jsonb(tokens) FROM tokens")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
        assert_eq!(
            fixture
                .app()
                .oneshot(request("/v1/usage", "Bearer query-key"))
                .await
                .unwrap()
                .status(),
            StatusCode::UNAUTHORIZED,
            "{sql}"
        );
        let after: Value = sqlx::query_scalar("SELECT to_jsonb(tokens) FROM tokens")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
        assert_eq!(before, after);
        sqlx::raw_sql("UPDATE tokens SET status=1,expired_time=-1,oauth_managed=FALSE,deleted_at=NULL;UPDATE users SET status=1,deleted_at=NULL").execute(&fixture.pg).await.unwrap();
    }
    for (ips, status) in [
        ("203.0.113.1", StatusCode::FORBIDDEN),
        ("127.0.0.0/8", StatusCode::OK),
        ("127.0.0.1,203.0.113.1", StatusCode::FORBIDDEN),
        (" \n127.0.0.1\n", StatusCode::OK),
    ] {
        sqlx::query("UPDATE tokens SET allow_ips=$1")
            .bind(ips)
            .execute(&fixture.pg)
            .await
            .unwrap();
        assert_eq!(
            fixture
                .app()
                .oneshot(request("/v1/usage", "Bearer query-key"))
                .await
                .unwrap()
                .status(),
            status,
            "{ips}"
        );
    }
    sqlx::query("UPDATE options SET value='NaN' WHERE key='QuotaPerUnit'")
        .execute(&fixture.pg)
        .await
        .unwrap();
    let response = fixture
        .app()
        .oneshot(request("/v1/usage", "Bearer query-key"))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::INTERNAL_SERVER_ERROR);
    assert_eq!(body(response).await["error"], "quota_query_unavailable");
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey via LMM_TEST_DATABASE_URL and LMM_AUTH_TEST_VALKEY_URL"]
async fn quota_query_limiter_is_shared_per_owner_across_keys_and_instances() {
    let fixture = Fixture::new().await;
    let valkey =
        redis::Client::open(std::env::var("LMM_AUTH_TEST_VALKEY_URL").expect("isolated Valkey"))
            .unwrap();
    let mut cache = valkey.get_multiplexed_async_connection().await.unwrap();
    redis::cmd("DEL")
        .arg("rateLimit:v2:user:quota-query:1")
        .query_async::<()>(&mut cache)
        .await
        .unwrap();
    sqlx::query("INSERT INTO tokens SELECT 12,user_id,'second-key',status,expired_time,allow_ips,remain_quota,used_quota,unlimited_quota,oauth_managed,accessed_time,deleted_at FROM tokens WHERE id=11").execute(&fixture.pg).await.unwrap();
    let make = || {
        router(TokenQueryState::new(
            fixture.pg.clone(),
            Arc::new(ValkeyAccountBalanceRateLimiter::new(
                valkey.clone(),
                2,
                Duration::from_secs(60),
                Duration::from_secs(2),
            )),
        ))
    };
    for key in ["Bearer query-key", "Bearer second-key"] {
        assert_eq!(
            make()
                .oneshot(request("/v1/usage", key))
                .await
                .unwrap()
                .status(),
            StatusCode::OK
        );
    }
    let response = make()
        .oneshot(request("/v1/usage", "Bearer query-key"))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
    assert!(response.headers().contains_key("retry-after"));
    assert!(
        to_bytes(response.into_body(), 1024)
            .await
            .unwrap()
            .is_empty()
    );
    redis::cmd("DEL")
        .arg("rateLimit:v2:user:quota-query:1")
        .query_async::<()>(&mut cache)
        .await
        .unwrap();
    fixture.cleanup().await;
}
