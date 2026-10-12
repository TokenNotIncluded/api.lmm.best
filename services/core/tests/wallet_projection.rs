//! Narrow current-wallet acceptance against SQLx-owned isolated PostgreSQL DBs.
use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Request, StatusCode},
};
use lmm_core::{
    accounts::{Account, AccountKind},
    billing::{
        Credits,
        ledger::{Action, Ledger, Outcome, Request as LedgerRequest},
    },
    core_schema,
    financial_profile::{self, ProfileError},
    funding::FundingPolicy,
    identity::{BrowserAuth, BrowserRegistration, BrowserSessionMetadata, IdentityStore},
    legacy_auth::{self, LegacyAuthConfig},
};
use serde_json::Value;
use sqlx::PgPool;
use tower::ServiceExt;
#[path = "common/runtime.rs"]
mod runtime;

fn app(store: IdentityStore) -> Router {
    legacy_auth::router(
        store,
        LegacyAuthConfig::new("https://wallet.example.invalid", &[73; 32]).unwrap(),
    )
}
async fn person(store: &IdentityStore, name: &str) -> (BrowserAuth, String, i64) {
    let browser = BrowserAuth::new(store.clone(), &[73; 32]).unwrap();
    browser
        .register(&BrowserRegistration {
            username: name,
            password: "synthetic-Only-739",
            email: "",
        })
        .await
        .unwrap();
    let bundle = browser
        .login(
            name,
            "synthetic-Only-739",
            &BrowserSessionMetadata::default(),
        )
        .await
        .unwrap();
    let user = bundle.data["user"]["id"].as_i64().unwrap();
    (
        browser,
        bundle.data["access_token"].as_str().unwrap().to_owned(),
        user,
    )
}
async fn current(app: &Router, secret: Option<&str>) -> (StatusCode, Value) {
    let mut request = Request::builder().uri("/api/user/self");
    if let Some(secret) = secret {
        request = request.header("authorization", format!("Bearer {secret}"));
    }
    let response = app
        .clone()
        .oneshot(request.body(Body::empty()).unwrap())
        .await
        .unwrap();
    assert_eq!(response.headers()["cache-control"], "no-store");
    let status = response.status();
    let data =
        serde_json::from_slice(&to_bytes(response.into_body(), 65536).await.unwrap()).unwrap();
    (status, data)
}
async fn post(ledger: &Ledger, key: &str, action: Action) {
    assert!(matches!(
        ledger
            .execute(&LedgerRequest {
                scope: "wallet-projection-test".into(),
                operation_key: key.into(),
                actor_user_id: None,
                reason: "Synthetic isolated fixture".into(),
                action,
            })
            .await
            .unwrap(),
        Outcome::Posted { .. }
    ));
}
async fn fund(ledger: &Ledger, account: i64, amount: i64) {
    ledger.open_wallet(account).await.unwrap();
    post(
        ledger,
        &format!("fund-{account}"),
        Action::Credit {
            account_id: account,
            amount_units: Credits::try_from(amount).unwrap(),
            payment_reference: format!("synthetic-{account}"),
        },
    )
    .await;
}
async fn setup(pool: &PgPool) -> IdentityStore {
    core_schema::init(pool).await.unwrap();
    IdentityStore::from_pool(pool.clone())
        .with_registration_enabled(true)
        .with_financial_profile_enabled(true)
}

#[sqlx::test(migrations = false)]
async fn real_wallet_reservations_and_other_user_are_not_conflated(pool: PgPool) {
    let store = setup(&pool).await;
    let (_, secret, user) = person(&store, "wallet-owner").await;
    let (_, other_secret, _) = person(&store, "wallet-other").await;
    let account = store.authorize(&secret).await.unwrap().owner_account_id;
    let other_account = store
        .authorize(&other_secret)
        .await
        .unwrap()
        .owner_account_id;
    let ledger = Ledger::from_pool(pool.clone());
    fund(&ledger, account, 1000).await;
    fund(&ledger, other_account, 777).await;
    post(
        &ledger,
        "reserve",
        Action::Reserve {
            account_id: account,
            amount_units: Credits::try_from(280).unwrap(),
        },
    )
    .await;
    let before: (i64, i64) = sqlx::query_as("SELECT (SELECT count(*) FROM core_billing.ledger_journals),(SELECT count(*) FROM core_billing.ledger_entries)").fetch_one(&pool).await.unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    role.optional("web_identity").await;
    let runtime_pool = PgPool::connect(&role.url).await.unwrap();
    let app =
        app(IdentityStore::from_pool(runtime_pool.clone()).with_financial_profile_enabled(true));
    for _ in 0..2 {
        let (status, body) = current(&app, Some(&secret)).await;
        assert_eq!(status, StatusCode::OK, "{body:?}");
        let data = &body["data"];
        assert_eq!(data["id"], user);
        assert_eq!(data["quota"], 720);
        assert_eq!(data["wallet_spendable_quota"], 720);
        assert_eq!(data["wallet_reserved_quota"], 280);
        assert_eq!(data["wallet_debt"], 0);
        assert_eq!(data["wallet_unit"], "core_credit");
        assert_eq!(data["wallet_money_basis"], "unknown");
        assert_eq!(data["wallet_money_available"], false);
    }
    assert_eq!(
        current(&app, Some(&other_secret)).await.1["data"]["quota"],
        777
    );
    let after: (i64, i64) = sqlx::query_as("SELECT (SELECT count(*) FROM core_billing.ledger_journals),(SELECT count(*) FROM core_billing.ledger_entries)").fetch_one(&pool).await.unwrap();
    assert_eq!(before, after);
    runtime_pool.close().await;
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn imported_personal_web_session_reads_funded_wallet_without_historical_counters(
    pool: PgPool,
) {
    let store = setup(&pool).await;
    let account: i64 = sqlx::query_scalar(
        "INSERT INTO core_identity.accounts(kind) VALUES ('personal') RETURNING id",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    sqlx::query("INSERT INTO core_identity.users(id,personal_account_id) VALUES (773,$1)")
        .bind(account)
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query("INSERT INTO core_identity.user_profiles(user_id,username,legacy_bcrypt_allowed) VALUES (773,'wallet-import',TRUE)")
        .execute(&pool).await.unwrap();
    sqlx::query("INSERT INTO core_identity.password_logins(login_name,user_id,password_hash) VALUES ('wallet-import',773,$1)")
        .bind(bcrypt::hash("synthetic-Only-739",4).unwrap()).execute(&pool).await.unwrap();
    let browser = BrowserAuth::new(store.clone(), &[73; 32]).unwrap();
    let bundle = browser
        .login(
            "wallet-import",
            "synthetic-Only-739",
            &BrowserSessionMetadata::default(),
        )
        .await
        .unwrap();
    assert_eq!(bundle.data["user"]["financial_projection_available"], false);
    let secret = bundle.data["access_token"].as_str().unwrap();
    let ledger = Ledger::from_pool(pool.clone());
    fund(&ledger, account, 1000).await;
    post(
        &ledger,
        "import-reserve",
        Action::Reserve {
            account_id: account,
            amount_units: Credits::try_from(280).unwrap(),
        },
    )
    .await;
    let (status, body) = current(&app(store), Some(secret)).await;
    assert_eq!(status, StatusCode::OK, "{body:?}");
    let data = &body["data"];
    assert_eq!(data["financial_projection_available"], true);
    assert_eq!(data["quota"], 720);
    assert_eq!(data["wallet_reserved_quota"], 280);
    assert_eq!(data["usage_projection_available"], false);
    assert!(data.get("used_quota").is_none());
    assert!(data.get("request_count").is_none());
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM core_billing.account_opening_counters")
            .fetch_one(&pool)
            .await
            .unwrap(),
        0
    );
}

#[sqlx::test(migrations = false)]
async fn zero_wallet_does_not_create_ledger_or_fake_currency(pool: PgPool) {
    let store = setup(&pool).await;
    let (_, secret, _) = person(&store, "wallet-zero").await;
    let (status, body) = current(&app(store), Some(&secret)).await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["data"]["financial_projection_available"], true);
    assert_eq!(body["data"]["quota"], 0);
    assert_eq!(body["data"]["wallet_reserved_quota"], 0);
    assert_eq!(body["data"]["wallet_money_available"], false);
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT count(*) FROM core_billing.ledger_accounts WHERE owner_account_id IS NOT NULL"
        )
        .fetch_one(&pool)
        .await
        .unwrap(),
        0
    );
}

#[sqlx::test(migrations = false)]
async fn anonymous_api_key_native_nonweb_session_and_revoked_browser_are_refused(pool: PgPool) {
    let store = setup(&pool).await;
    let (browser, secret, user) = person(&store, "wallet-auth").await;
    let app = app(store.clone());
    assert_eq!(current(&app, None).await.0, StatusCode::UNAUTHORIZED);
    sqlx::query("INSERT INTO core_identity.developer_access(user_id,granted,source_reference) VALUES ($1,TRUE,'synthetic-wallet-test-grant')")
        .bind(user).execute(&pool).await.unwrap();
    let key = store
        .issue_key(
            &secret,
            Account {
                kind: AccountKind::Personal,
                id: user,
            },
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    assert_eq!(
        current(&app, Some(&key.secret)).await.0,
        StatusCode::FORBIDDEN
    );
    let team = store.create_team(&secret).await.unwrap();
    let team_key = store
        .issue_key(
            &secret,
            Account {
                kind: AccountKind::Team,
                id: team,
            },
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    assert_eq!(
        current(&app, Some(&team_key.secret)).await.0,
        StatusCode::FORBIDDEN
    );
    let native = store.bootstrap_user(99000, 1).await.unwrap();
    assert_eq!(
        current(&app, Some(&native.secret)).await.0,
        StatusCode::FORBIDDEN
    );
    browser.logout(None, Some(&secret), None).await.unwrap();
    assert_eq!(
        current(&app, Some(&secret)).await.0,
        StatusCode::UNAUTHORIZED
    );
}

#[sqlx::test(migrations = false)]
async fn missing_financial_configuration_is_explicitly_unknown_not_zero(pool: PgPool) {
    let store = setup(&pool).await;
    let (_, secret, _) = person(&store, "wallet-disabled").await;
    let (status, body) = current(&app(IdentityStore::from_pool(pool)), Some(&secret)).await;
    // Identity-only Core still serves identity, but no monetary success claim.
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["data"]["financial_projection_available"], false);
    assert!(body["data"].get("quota").is_none());
}

#[sqlx::test(migrations = false)]
async fn db_unavailable_is_503_without_financial_data(pool: PgPool) {
    let store = setup(&pool).await;
    let (_, secret, _) = person(&store, "wallet-db-fail").await;
    let app = app(store);
    pool.close().await;
    let (status, body) = current(&app, Some(&secret)).await;
    assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE);
    assert_eq!(body["code"], "AUTH_UNAVAILABLE");
    assert!(body.get("data").is_none());
}

#[sqlx::test(migrations = false)]
async fn financial_read_capability_denied_is_503_not_success(pool: PgPool) {
    let store = setup(&pool).await;
    let (_, secret, _) = person(&store, "wallet-read-denied").await;
    let role = runtime::RuntimeRole::create(&pool).await;
    role.optional("web_identity").await;
    sqlx::raw_sql(&format!(
        "REVOKE SELECT ON core_billing.balance_state FROM {}",
        role.name
    ))
    .execute(&pool)
    .await
    .unwrap();
    let app = app(IdentityStore::from_pool(role.pool.clone()).with_financial_profile_enabled(true));
    let (status, body) = current(&app, Some(&secret)).await;
    assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE);
    assert!(body.get("data").is_none());
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn safe_integer_boundary_is_exact_and_larger_balance_is_unknown(pool: PgPool) {
    let store = setup(&pool).await;
    let (_, secret, user) = person(&store, "wallet-boundary").await;
    let account = store.authorize(&secret).await.unwrap().owner_account_id;
    let ledger = Ledger::from_pool(pool.clone());
    fund(&ledger, account, 9_007_199_254_740_991).await;
    let app = app(store);
    assert_eq!(
        current(&app, Some(&secret)).await.1["data"]["quota"],
        9_007_199_254_740_991_i64
    );
    post(
        &ledger,
        "overflow-js",
        Action::Credit {
            account_id: account,
            amount_units: Credits::try_from(1).unwrap(),
            payment_reference: "synthetic-plus-one".into(),
        },
    )
    .await;
    let (status, body) = current(&app, Some(&secret)).await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["data"]["financial_projection_available"], false);
    assert!(body["data"].get("quota").is_none());
    let mut conn = pool.acquire().await.unwrap();
    assert_eq!(
        financial_profile::read_wallet(&mut conn, user).await,
        Err(ProfileError::Unavailable)
    );
}
