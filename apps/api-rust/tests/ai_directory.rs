use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{Request, StatusCode},
};
use lmm_api_rs::{
    auth::{
        AuthBundle, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth,
        DashboardUser, DashboardUserView, LoginOutcome, LoginRequest, LogoutRequest, LogoutResult,
        RequestMetadata, TwoFactorLoginRequest,
    },
    routes::ai_directory::{
        AIDirectoryAdInput, AIDirectoryState, AIDirectoryStore, AdError, MAX_WALLET_QUOTA,
        PgAIDirectoryStore, charge_quota, charge_quota_with_credits_per_usd, charged_amount_usd,
        normalize_ad, router, validate_directory_links,
    },
};
use secrecy::{ExposeSecret, SecretString};
use serde_json::{Value, json};
use sqlx::{PgPool, postgres::PgPoolOptions};
use std::{sync::Arc, time::Duration};
use tower::ServiceExt;

struct Auth;
#[async_trait]
impl DashboardAuth for Auth {
    async fn check_critical_rate_limit(
        &self,
        _: &str,
    ) -> Result<CriticalRateLimitOutcome, AuthError> {
        Ok(CriticalRateLimitOutcome::Allowed)
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
    async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }
    async fn generate_personal_access_token(&self, _: SecretString) -> Result<String, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }
    async fn self_user(&self, token: SecretString) -> Result<DashboardUser, AuthError> {
        let (id, role) = match token.expose_secret() {
            "owner" | "l0" => (1, 1),
            "other" => (2, 1),
            "admin" => (3, 10),
            "root" => (4, 100),
            _ => return Err(AuthError::new(AuthErrorKind::Unauthorized)),
        };
        Ok(DashboardUser {
            id,
            role,
            username: format!("user-{id}"),
            display_name: String::new(),
            status: 1,
            email: String::new(),
            github_id: String::new(),
            discord_id: String::new(),
            oidc_id: String::new(),
            wechat_id: String::new(),
            telegram_id: String::new(),
            group: "default".into(),
            quota: 0,
            used_quota: 0,
            request_count: 0,
            aff_code: String::new(),
            aff_count: 0,
            aff_quota: 0,
            aff_history_quota: 0,
            inviter_id: 0,
            linux_do_id: String::new(),
            setting: "{}".into(),
            stripe_customer: String::new(),
            sidebar_modules: json!({}),
            permissions: json!({}),
        })
    }
}

struct ActivatedAuth;
#[async_trait]
impl DashboardAuth for ActivatedAuth {
    async fn check_critical_rate_limit(
        &self,
        ip: &str,
    ) -> Result<CriticalRateLimitOutcome, AuthError> {
        Auth.check_critical_rate_limit(ip).await
    }
    async fn login(
        &self,
        input: LoginRequest,
        meta: RequestMetadata,
    ) -> Result<LoginOutcome, AuthError> {
        Auth.login(input, meta).await
    }
    async fn login_2fa(
        &self,
        input: TwoFactorLoginRequest,
        meta: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        Auth.login_2fa(input, meta).await
    }
    async fn refresh(
        &self,
        token: SecretString,
        sid: Option<String>,
        meta: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        Auth.refresh(token, sid, meta).await
    }
    async fn logout(&self, input: LogoutRequest) -> Result<LogoutResult, AuthError> {
        Auth.logout(input).await
    }
    async fn generate_personal_access_token(
        &self,
        token: SecretString,
    ) -> Result<String, AuthError> {
        Auth.generate_personal_access_token(token).await
    }
    async fn self_user(&self, token: SecretString) -> Result<DashboardUser, AuthError> {
        Auth.self_user(token).await
    }
    async fn self_user_view_for_optional(
        &self,
        token: SecretString,
    ) -> Result<DashboardUserView, AuthError> {
        let granted = token.expose_secret() != "l0";
        let mut view = Auth.self_user_view_for_optional(token).await?;
        view.developer_access_granted = granted;
        Ok(view)
    }
}

fn input(id: &str, bid: i64) -> AIDirectoryAdInput {
    AIDirectoryAdInput {
        name: " Example ".into(),
        url: "https://example.com".into(),
        summary: " Useful ".into(),
        description: " Detail ".into(),
        bid_cents: bid,
        expected_quota: bid * 10,
        request_id: id.into(),
    }
}
fn request(method: &str, path: &str, token: Option<&str>, body: impl Into<Body>) -> Request<Body> {
    let mut builder = Request::builder()
        .method(method)
        .uri(path)
        .header("content-type", "application/json");
    if let Some(token) = token {
        builder = builder.header("authorization", format!("Bearer {token}"));
    }
    builder.body(body.into()).unwrap()
}
async fn json_body(response: axum::response::Response) -> Value {
    serde_json::from_slice(&to_bytes(response.into_body(), 1024 * 1024).await.unwrap()).unwrap()
}

#[test]
fn quote_and_url_normalization_match_current_go_oracle() {
    let oracle: Value = if let Ok(path) = std::env::var("LMM_AI_DIRECTORY_GO_ORACLE_OUTPUT") {
        serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap()
    } else {
        serde_json::from_str(include_str!("fixtures/ai-directory-current-go.json")).unwrap()
    };
    for vector in oracle["quotes"].as_array().unwrap() {
        assert_eq!(vector["legacy_quota_per_unit"], "500000");
        let outcome = charge_quota_with_credits_per_usd(
            vector["bid_cents"].as_i64().unwrap(),
            vector["credits_per_usd"].as_str().unwrap(),
        );
        if vector["error"] == "" {
            assert_eq!(
                outcome.unwrap(),
                vector["quota"].as_i64().unwrap(),
                "{vector}"
            )
        } else {
            assert_eq!(
                outcome.unwrap_err().to_string(),
                vector["error"].as_str().unwrap(),
                "{vector}"
            )
        }
    }
    for vector in oracle["amounts"].as_array().unwrap() {
        assert_eq!(
            charged_amount_usd(
                vector["charged_quota"].as_i64().unwrap(),
                vector["credits_per_usd"].as_str().unwrap()
            )
            .as_deref(),
            vector["charged_amount_usd"].as_str(),
            "{vector}",
        );
    }
    for vector in oracle["urls"].as_array().unwrap() {
        let mut ad = input("directory-oracle-0001", 100);
        ad.url = vector["input"].as_str().unwrap().into();
        let outcome = normalize_ad(ad);
        if vector["error"] == "" {
            assert_eq!(
                outcome.unwrap().url,
                vector["normalized"].as_str().unwrap(),
                "{vector}"
            )
        } else {
            assert_eq!(
                outcome.unwrap_err().to_string(),
                vector["error"].as_str().unwrap(),
                "{vector}"
            )
        }
    }
}

#[test]
fn directory_configuration_requires_complete_typed_entries_and_preserves_disabled_entries() {
    let entry = json!({"id":"one","name":"One","url":"https://example.com","category":"chat","summary":"","description":"","enabled":false,"extra":"preserved"});
    let valid = json!([entry]);
    assert_eq!(validate_directory_links(&valid.to_string()).unwrap(), valid);
    assert_eq!(validate_directory_links("[]").unwrap(), json!([]));
    for invalid in [
        "null".to_owned(),
        json!([entry.clone(), entry.clone()]).to_string(),
        "[{}]".to_owned(),
    ] {
        assert!(validate_directory_links(&invalid).is_err());
    }
    let mut bad = entry;
    bad["url"] = json!("https://user:pass@example.com");
    assert!(validate_directory_links(&json!([bad]).to_string()).is_err());
}

#[tokio::test]
async fn private_routes_reject_missing_credentials_and_l0_before_database_or_body() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgresql://fixture:fixture@127.0.0.1:1/unused")
        .unwrap();
    let app = router(AIDirectoryState::new(
        Arc::new(PgAIDirectoryStore::new(pg, None)),
        Arc::new(ActivatedAuth),
    ));
    for (method, path) in [
        ("GET", "/api/ai-directory/ads/quote"),
        ("GET", "/api/ai-directory/ads/mine"),
        ("POST", "/api/ai-directory/ads"),
        ("POST", "/api/ai-directory/ads/1/hide"),
    ] {
        let response = app
            .clone()
            .oneshot(request(method, path, None, "{".repeat(9000)))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED, "{path}");
    }
    for path in [
        "/api/ai-directory",
        "/api/ai-directory/ads",
        "/api/ai-directory/ads/mine",
    ] {
        assert_eq!(
            app.clone()
                .oneshot(request("GET", path, Some("l0"), Body::empty()))
                .await
                .unwrap()
                .status(),
            StatusCode::NOT_FOUND
        );
    }
    for token in ["owner", "admin"] {
        assert_eq!(
            app.clone()
                .oneshot(request(
                    "POST",
                    "/api/ai-directory/ads/1/hide",
                    Some(token),
                    Body::empty()
                ))
                .await
                .unwrap()
                .status(),
            StatusCode::FORBIDDEN
        );
    }
    let response = app
        .clone()
        .oneshot(request(
            "GET",
            "/api/ai-directory/ads?offset=-1",
            None,
            Body::empty(),
        ))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::UNPROCESSABLE_ENTITY);
    let mut oversized = request(
        "POST",
        "/api/ai-directory/ads",
        Some("owner"),
        Body::empty(),
    );
    oversized
        .headers_mut()
        .insert("content-length", "8193".parse().unwrap());
    assert_eq!(
        app.clone().oneshot(oversized).await.unwrap().status(),
        StatusCode::PAYLOAD_TOO_LARGE
    );
    assert_eq!(
        app.oneshot(request("POST", "/api/ai-directory/ads", Some("owner"), "{"))
            .await
            .unwrap()
            .status(),
        StatusCode::UNPROCESSABLE_ENTITY
    );
}

struct Fixture {
    admin: PgPool,
    pg: PgPool,
    schema: String,
    store: Arc<PgAIDirectoryStore>,
}
impl Fixture {
    async fn new() -> Self {
        let url = std::env::var("LMM_TEST_DATABASE_URL").expect("isolated PostgreSQL is required");
        let admin = PgPool::connect(&url).await.unwrap();
        let schema = format!("ai_directory_{}", uuid::Uuid::new_v4().simple());
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&admin)
            .await
            .unwrap();
        let pg = PgPoolOptions::new()
            .max_connections(12)
            .after_connect({
                let schema = schema.clone();
                move |connection, _| {
                    let sql = format!("SET search_path TO {schema}");
                    Box::pin(async move {
                        sqlx::query(&sql).execute(connection).await?;
                        Ok(())
                    })
                }
            })
            .connect(&url)
            .await
            .unwrap();
        sqlx::raw_sql("CREATE TABLE options(key TEXT PRIMARY KEY,value TEXT);CREATE TABLE users(id BIGINT PRIMARY KEY,username TEXT,quota BIGINT,deleted_at TIMESTAMPTZ);CREATE TABLE logs(id BIGSERIAL PRIMARY KEY,user_id BIGINT,created_at BIGINT,type BIGINT,content TEXT,username TEXT,token_name TEXT,model_name TEXT,quota BIGINT,prompt_tokens BIGINT,completion_tokens BIGINT,use_time BIGINT,is_stream BOOLEAN,channel_id BIGINT,token_id BIGINT,\"group\" TEXT,ip TEXT,other TEXT,request_id TEXT);INSERT INTO users VALUES(1,'owner',1000000,NULL),(2,'other',1000000,NULL),(3,'admin',0,NULL),(4,'root',0,NULL);INSERT INTO options VALUES('QuotaPerUnit','500000'),('CreditsPerUSD','1000'),('AIDirectoryLinks','[]');").execute(&pg).await.unwrap();
        let migration = include_str!("../migrations/0015_current_catalog.sql")
            .replace("__LMM_APP_SCHEMA__", &schema);
        sqlx::raw_sql(&migration).execute(&pg).await.unwrap();
        sqlx::raw_sql(&migration).execute(&pg).await.unwrap();
        let store = Arc::new(PgAIDirectoryStore::new(pg.clone(), None));
        Self {
            admin,
            pg,
            schema,
            store,
        }
    }
    fn app(&self) -> axum::Router {
        router(AIDirectoryState::new(
            self.store.clone(),
            Arc::new(ActivatedAuth),
        ))
    }
    async fn quota(&self, user: i64) -> i64 {
        sqlx::query_scalar("SELECT quota FROM users WHERE id=$1")
            .bind(user)
            .fetch_one(&self.pg)
            .await
            .unwrap()
    }
    async fn cleanup(self) {
        self.pg.close().await;
        sqlx::query(&format!("DROP SCHEMA {} CASCADE", self.schema))
            .execute(&self.admin)
            .await
            .unwrap();
        self.admin.close().await;
    }
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_create_replay_quote_changes_and_concurrency_charge_once() {
    let fixture = Fixture::new().await;
    let mut tasks = tokio::task::JoinSet::new();
    for _ in 0..16 {
        let store = fixture.store.clone();
        tasks.spawn(async move {
            store
                .create(1, input("directory-concurrent-0001", 125))
                .await
        });
    }
    let mut created = 0;
    let mut ids = std::collections::BTreeSet::new();
    while let Some(result) = tasks.join_next().await {
        let (ad, new) = result.unwrap().unwrap();
        created += usize::from(new);
        ids.insert(ad.id);
        assert_eq!(ad.name, "Example");
        assert_eq!(ad.expires_at - ad.paid_at, 30 * 86400);
    }
    assert_eq!(created, 1);
    assert_eq!(ids.len(), 1);
    assert_eq!(fixture.quota(1).await, 998750);
    sqlx::query("UPDATE options SET value='2000' WHERE key='CreditsPerUSD'")
        .execute(&fixture.pg)
        .await
        .unwrap();
    assert!(
        !fixture
            .store
            .create(1, input("directory-concurrent-0001", 125))
            .await
            .unwrap()
            .1
    );
    assert_eq!(
        fixture
            .store
            .create(2, input("directory-concurrent-0001", 125))
            .await
            .unwrap_err(),
        AdError::Conflict
    );
    assert_eq!(
        fixture
            .store
            .create(1, input("directory-changed-price-0002", 125))
            .await
            .unwrap_err(),
        AdError::QuoteChanged
    );
    let mut different = input("directory-concurrent-0001", 125);
    different.description = "different".into();
    assert_eq!(
        fixture.store.create(1, different).await.unwrap_err(),
        AdError::Conflict
    );
    let count: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM ai_directory_ads")
        .fetch_one(&fixture.pg)
        .await
        .unwrap();
    assert_eq!(count, 1);
    let logs: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM logs WHERE type=3")
        .fetch_one(&fixture.pg)
        .await
        .unwrap();
    assert_eq!(logs, 1);
    sqlx::query("UPDATE users SET quota=1 WHERE id=2")
        .execute(&fixture.pg)
        .await
        .unwrap();
    let mut poor = input("directory-insufficient-0003", 100);
    poor.expected_quota = 2000;
    assert_eq!(
        fixture.store.create(2, poor).await.unwrap_err(),
        AdError::Insufficient
    );
    assert_eq!(fixture.quota(2).await, 1);
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_hide_refunds_once_and_wallet_failure_rolls_back_visibility() {
    let fixture = Fixture::new().await;
    let (ad, _) = fixture
        .store
        .create(1, input("directory-refund-0001", 100))
        .await
        .unwrap();
    sqlx::query("UPDATE users SET quota=$1 WHERE id=1")
        .bind(MAX_WALLET_QUOTA)
        .execute(&fixture.pg)
        .await
        .unwrap();
    assert_eq!(
        fixture.store.hide(ad.id).await.unwrap_err(),
        AdError::WalletRange
    );
    assert_eq!(fixture.store.active(0).await.unwrap().0[0].status, "active");
    sqlx::query("UPDATE users SET quota=999000 WHERE id=1")
        .execute(&fixture.pg)
        .await
        .unwrap();
    let mut tasks = tokio::task::JoinSet::new();
    for _ in 0..16 {
        let store = fixture.store.clone();
        let id = ad.id;
        tasks.spawn(async move { store.hide(id).await });
    }
    let mut refunds = 0;
    while let Some(result) = tasks.join_next().await {
        let (hidden, refunded) = result.unwrap().unwrap();
        refunds += usize::from(refunded);
        assert_eq!(hidden.status, "hidden");
        assert!(hidden.hidden_at > 0);
        assert_eq!(hidden.refunded_at, hidden.hidden_at);
    }
    assert_eq!(refunds, 1);
    assert_eq!(fixture.quota(1).await, 1000000);
    assert!(fixture.store.active(0).await.unwrap().0.is_empty());
    let count: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM logs WHERE type=6")
        .fetch_one(&fixture.pg)
        .await
        .unwrap();
    assert_eq!(count, 1);
    assert_eq!(
        fixture.store.hide(99999).await.unwrap_err(),
        AdError::NotFound
    );
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_public_private_pagination_expiry_and_http_contract() {
    let fixture = Fixture::new().await;
    for i in 0..22 {
        fixture
            .store
            .create(1, input(&format!("directory-paging-{i:04}"), 100))
            .await
            .unwrap();
    }
    let (best, _) = fixture
        .store
        .create(2, input("directory-high-bid-0001", 300))
        .await
        .unwrap();
    let (page, more) = fixture.store.active(0).await.unwrap();
    assert!(more);
    assert_eq!(page.len(), 20);
    assert_eq!(page[0].id, best.id);
    let (last, more) = fixture.store.active(20).await.unwrap();
    assert_eq!(last.len(), 3);
    assert!(!more);
    assert_eq!(fixture.store.mine(2).await.unwrap().len(), 1);
    sqlx::query("UPDATE ai_directory_ads SET expires_at=0 WHERE id=$1")
        .bind(best.id)
        .execute(&fixture.pg)
        .await
        .unwrap();
    assert_eq!(
        fixture.store.hide(best.id).await.unwrap_err(),
        AdError::NotFound
    );
    assert_ne!(fixture.store.active(0).await.unwrap().0[0].id, best.id);
    let app = fixture.app();
    let directory = json_body(
        app.clone()
            .oneshot(request("GET", "/api/ai-directory", None, Body::empty()))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(directory, json!({"success":true,"data":{"links":[]}}));
    let page = json_body(
        app.clone()
            .oneshot(request(
                "GET",
                "/api/ai-directory/ads?offset=20",
                None,
                Body::empty(),
            ))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(page["data"]["next_offset"], 22);
    assert_eq!(page["data"]["has_more"], false);
    for item in page["data"]["items"].as_array().unwrap() {
        assert!(item.get("request_id").is_none());
        assert!(item.get("owner_user_id").is_none());
        assert_eq!(item["charged_amount_usd"], "1");
    }
    let quote = json_body(
        app.clone()
            .oneshot(request(
                "GET",
                "/api/ai-directory/ads/quote?bid_cents=125",
                Some("owner"),
                Body::empty(),
            ))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(
        quote["data"],
        json!({"bid_cents":125,"quota":1250,"currency":"USD","pricing_schema_version":2,"duration_days":30,"min_bid_cents":100,"max_bid_cents":1000000})
    );
    let response = app
        .clone()
        .oneshot(request(
            "POST",
            "/api/ai-directory/ads",
            Some("owner"),
            serde_json::to_string(&input("directory-http-create-0001", 100)).unwrap(),
        ))
        .await
        .unwrap();
    let created = json_body(response).await;
    assert_eq!(created["data"]["created"], true);
    let id = created["data"]["ad"]["id"].as_i64().unwrap();
    let response = json_body(
        app.oneshot(request(
            "POST",
            &format!("/api/ai-directory/ads/{id}/hide"),
            Some("root"),
            Body::empty(),
        ))
        .await
        .unwrap(),
    )
    .await;
    assert_eq!(response["data"]["refunded"], true);
    assert_eq!(response["data"]["refunded_quota"], 1000);
    let audit: String =
        sqlx::query_scalar("SELECT other FROM logs WHERE user_id=4 ORDER BY id DESC LIMIT 1")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
    assert_eq!(
        serde_json::from_str::<Value>(&audit).unwrap()["op"]["action"],
        "ai_directory_ad.hide"
    );
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey via LMM_TEST_DATABASE_URL and LMM_AUTH_TEST_VALKEY_URL"]
async fn postgres_cache_and_audit_failures_do_not_reverse_committed_wallet_changes() {
    let fixture = Fixture::new().await;
    let valkey =
        redis::Client::open(std::env::var("LMM_AUTH_TEST_VALKEY_URL").expect("isolated Valkey"))
            .unwrap();
    let mut cache = valkey.get_multiplexed_async_connection().await.unwrap();
    redis::cmd("SET")
        .arg("user:1")
        .arg("stale")
        .query_async::<()>(&mut cache)
        .await
        .unwrap();
    let store = PgAIDirectoryStore::new(fixture.pg.clone(), Some(valkey));
    let (ad, _) = store
        .create(1, input("directory-cache-0001", 100))
        .await
        .unwrap();
    assert_eq!(
        redis::cmd("EXISTS")
            .arg("user:1")
            .query_async::<i64>(&mut cache)
            .await
            .unwrap(),
        0
    );
    sqlx::query("DROP TABLE logs")
        .execute(&fixture.pg)
        .await
        .unwrap();
    let failing = PgAIDirectoryStore::new(
        fixture.pg.clone(),
        Some(redis::Client::open("redis://127.0.0.1:1").unwrap()),
    )
    .with_dependency_timeout(Duration::from_millis(50));
    assert!(failing.hide(ad.id).await.unwrap().1);
    let (_, created) = failing
        .create(1, input("directory-cache-0002", 100))
        .await
        .unwrap();
    assert!(created);
    assert_eq!(fixture.quota(1).await, 999000);
    assert!(
        !failing
            .create(1, input("directory-cache-0002", 100))
            .await
            .unwrap()
            .1
    );
    fixture.cleanup().await;
}

#[test]
fn real_usd_bid_uses_exact_credit_basis_and_safe_ceiling() {
    assert_eq!(
        charge_quota_with_credits_per_usd(100, "3500000").unwrap(),
        3_500_000
    );
    assert_eq!(charge_quota(100, 3_500_000.0).unwrap(), 3_500_000);
    assert_eq!(
        charge_quota_with_credits_per_usd(125, "1000.1").unwrap(),
        1251
    );
    assert_eq!(charge_quota_with_credits_per_usd(100, "1e-30").unwrap(), 1);
    assert_eq!(
        charge_quota_with_credits_per_usd(100, "9007199254740991").unwrap(),
        MAX_WALLET_QUOTA
    );
    assert_eq!(
        charge_quota_with_credits_per_usd(125, "9007199254740991").unwrap_err(),
        AdError::WalletRange
    );
    for rate in [
        "",
        "0",
        "-1",
        "NaN",
        "+Inf",
        "1e400",
        "9007199254740991.0001",
        "9007199254740992",
    ] {
        assert_eq!(
            charge_quota_with_credits_per_usd(100, rate).unwrap_err(),
            AdError::CurrencyUnavailable,
            "{rate}"
        );
    }
    assert_eq!(
        charge_quota_with_credits_per_usd(99, "").unwrap_err(),
        AdError::InvalidBid
    );
    assert_eq!(
        charged_amount_usd(500_000, "3500000").as_deref(),
        Some("0.1428571428571429")
    );
    assert_eq!(
        charged_amount_usd(3_500_000, "3500000").as_deref(),
        Some("1")
    );
    assert_eq!(
        charged_amount_usd(1, "1e-30").as_deref(),
        Some("1000000000000000000000000000000")
    );
    assert_eq!(charged_amount_usd(1, "20000000000000000"), None);
    assert_eq!(charged_amount_usd(500_000, ""), None);
    assert_eq!(charged_amount_usd(0, "3500000").as_deref(), Some("0"));
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_actual_usd_basis_preserves_legacy_replay_refund_and_value_ordering() {
    let fixture = Fixture::new().await;
    sqlx::query("UPDATE options SET value='3500000' WHERE key='CreditsPerUSD'")
        .execute(&fixture.pg)
        .await
        .unwrap();
    sqlx::query("UPDATE users SET quota=10000000 WHERE id=1")
        .execute(&fixture.pg)
        .await
        .unwrap();
    // Q=500,000 is the old pricing unit, not the immutable USD wallet basis.
    assert_eq!(fixture.store.quote(100).await.unwrap(), 3_500_000);
    sqlx::query("UPDATE options SET value='1' WHERE key='QuotaPerUnit'")
        .execute(&fixture.pg)
        .await
        .unwrap();
    assert_eq!(fixture.store.quote(100).await.unwrap(), 3_500_000);
    let legacy = normalize_ad(input("directory-legacy-paid-0001", 2000)).unwrap();
    let legacy_id: i64 = sqlx::query_scalar("INSERT INTO ai_directory_ads(owner_user_id,name,url,summary,description,bid_cents,charged_quota,request_id,status,paid_at,expires_at,hidden_at,refunded_at) VALUES(1,$1,$2,$3,$4,2000,500000,$5,'active',1,$6,0,0) RETURNING id::BIGINT")
        .bind(&legacy.name).bind(&legacy.url).bind(&legacy.summary).bind(&legacy.description).bind(&legacy.request_id).bind(chrono::Utc::now().timestamp()+86400).fetch_one(&fixture.pg).await.unwrap();
    let mut fresh_input = input("directory-real-dollar-0001", 100);
    fresh_input.expected_quota = 3_500_000;
    let mut tasks = tokio::task::JoinSet::new();
    for _ in 0..16 {
        let store = fixture.store.clone();
        let new_input = fresh_input.clone();
        tasks.spawn(async move { store.create(1, new_input).await });
    }
    let mut created = 0;
    let mut fresh_id = 0;
    while let Some(outcome) = tasks.join_next().await {
        let (ad, new) = outcome.unwrap().unwrap();
        created += usize::from(new);
        assert_eq!(ad.charged_quota, 3_500_000);
        assert_eq!(ad.charged_amount_usd.as_deref(), Some("1"));
        if fresh_id != 0 {
            assert_eq!(ad.id, fresh_id);
        }
        fresh_id = ad.id;
    }
    assert_eq!(created, 1);
    assert_eq!(fixture.quota(1).await, 6_500_000);
    let (ads, _) = fixture.store.active(0).await.unwrap();
    assert_eq!(
        ads.iter().map(|ad| ad.id).collect::<Vec<_>>(),
        vec![fresh_id, legacy_id]
    );
    assert_eq!(
        ads[1].charged_amount_usd.as_deref(),
        Some("0.1428571428571429")
    );
    sqlx::query("DELETE FROM options WHERE key='CreditsPerUSD'")
        .execute(&fixture.pg)
        .await
        .unwrap();
    assert_eq!(
        fixture.store.quote(100).await.unwrap_err(),
        AdError::CurrencyUnavailable
    );
    let mut replay = legacy.clone();
    replay.expected_quota = 500_000;
    let (paid, new) = fixture.store.create(1, replay).await.unwrap();
    assert!(!new);
    assert_eq!(paid.charged_quota, 500_000);
    assert_eq!(paid.charged_amount_usd, None);
    assert_eq!(
        fixture
            .store
            .create(1, input("directory-missing-basis-0001", 100))
            .await
            .unwrap_err(),
        AdError::CurrencyUnavailable
    );
    let response = fixture
        .app()
        .oneshot(request(
            "GET",
            "/api/ai-directory/ads/quote?bid_cents=100",
            Some("owner"),
            Body::empty(),
        ))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
    assert_eq!(
        json_body(response).await["code"],
        "AI_DIRECTORY_AD_CURRENCY_UNAVAILABLE"
    );
    let mut tasks = tokio::task::JoinSet::new();
    for _ in 0..16 {
        let store = fixture.store.clone();
        tasks.spawn(async move { store.hide(legacy_id).await });
    }
    let mut refunded = 0;
    while let Some(outcome) = tasks.join_next().await {
        let (ad, new) = outcome.unwrap().unwrap();
        assert_eq!(ad.charged_quota, 500_000);
        assert_eq!(ad.charged_amount_usd, None);
        refunded += usize::from(new);
    }
    assert_eq!(refunded, 1);
    assert_eq!(fixture.quota(1).await, 7_000_000);
    fixture.cleanup().await;
}
