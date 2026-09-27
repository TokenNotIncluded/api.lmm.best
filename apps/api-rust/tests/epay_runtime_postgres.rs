//! Real PostgreSQL transaction proofs against the current Go payment contract.
//! Every test owns a fresh schema; no production DSN or default database is used.

use async_trait::async_trait;
use axum::{
    Router,
    body::{Body, to_bytes},
    http::{HeaderMap, Request},
};
use lmm_api_rs::{
    ClientIpKey,
    auth::CriticalRateLimitOutcome,
    routes::{
        billing_payments::md5_hex,
        epay::{
            Completion, CreateTopup, EpayCallback, PgEpayGateway, PgEpayRepository,
            TopupAuthorizer, TopupError, TopupRepository, UserTopupState, router,
        },
    },
};
use rust_decimal::Decimal;
use serde_json::{Value, json};
use sqlx::{PgPool, Row, postgres::PgPoolOptions};
use std::{collections::BTreeMap, sync::Arc};
use std::{error::Error, time::Duration};
use tower::ServiceExt;

type TestResult = Result<(), Box<dyn Error + Send + Sync>>;
const MIGRATION: &str = include_str!("../migrations/0012_payment_runtime.sql");
const MAX_QUOTA: i64 = 9_007_199_254_740_991;

#[path = "epay_runtime_postgres/stripe_wallet.rs"]
mod stripe_wallet;

struct Harness {
    admin: PgPool,
    pg: PgPool,
    schema: String,
}

impl Harness {
    async fn option(&self, key: &str, value: &str) {
        sqlx::query("INSERT INTO options(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value")
            .bind(key).bind(value).execute(&self.pg).await.unwrap();
    }

    async fn checkout_settings(&self) {
        for (key, value) in [
            ("PayMethods", r#"[{"type":"alipay","name":"Alipay"}]"#),
            ("PayAddress", "https://pay.example/gateway"),
            ("EpayId", "fixture-merchant"),
            ("EpayKey", "fixture-key"),
            ("ServerAddress", "https://console.example"),
            ("MinTopUp", "1"),
            ("USDExchangeRate", "7.3"),
            ("TopUpPlatformUnitsPerCNY", "2"),
        ] {
            self.option(key, value).await;
        }
    }
    async fn new() -> Self {
        let url = std::env::var("LMM_EPAY_TEST_DATABASE_URL")
            .expect("set LMM_EPAY_TEST_DATABASE_URL to a disposable PostgreSQL database");
        let admin = PgPoolOptions::new()
            .max_connections(2)
            .connect(&url)
            .await
            .unwrap();
        let schema = format!("epay_runtime_{}", uuid::Uuid::new_v4().simple());
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&admin)
            .await
            .unwrap();
        let pg = PgPoolOptions::new()
            .max_connections(12)
            .acquire_timeout(Duration::from_secs(5))
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
        sqlx::raw_sql("CREATE TABLE options(key TEXT PRIMARY KEY,value TEXT NOT NULL);
            CREATE TABLE users(id BIGINT PRIMARY KEY,username TEXT NOT NULL DEFAULT '',email TEXT NOT NULL DEFAULT '',stripe_customer TEXT NOT NULL DEFAULT '',status BIGINT NOT NULL DEFAULT 1,role BIGINT NOT NULL DEFAULT 1,\"group\" TEXT NOT NULL DEFAULT 'default',created_at BIGINT NOT NULL DEFAULT 1,quota BIGINT NOT NULL DEFAULT 0,inviter_id BIGINT NOT NULL DEFAULT 0,aff_quota BIGINT NOT NULL DEFAULT 0,aff_history BIGINT NOT NULL DEFAULT 0,deleted_at TIMESTAMPTZ);
            CREATE TABLE top_ups(id BIGSERIAL PRIMARY KEY,user_id BIGINT NOT NULL,amount BIGINT NOT NULL DEFAULT 0,money NUMERIC NOT NULL DEFAULT 0,trade_no VARCHAR(255) UNIQUE NOT NULL,payment_method VARCHAR(50) NOT NULL,payment_provider VARCHAR(50) NOT NULL DEFAULT '',create_time BIGINT NOT NULL DEFAULT 0,complete_time BIGINT NOT NULL DEFAULT 0,status TEXT NOT NULL);")
            .execute(&pg).await.unwrap();
        let migration = MIGRATION.replace("__LMM_APP_SCHEMA__", &schema);
        sqlx::raw_sql(&migration).execute(&pg).await.unwrap();
        sqlx::raw_sql("CREATE TABLE logs(id BIGSERIAL PRIMARY KEY,user_id BIGINT,created_at BIGINT,type BIGINT,content TEXT,username TEXT,token_name TEXT,model_name TEXT,quota BIGINT,prompt_tokens BIGINT,completion_tokens BIGINT,use_time BIGINT,is_stream BOOLEAN,channel_id BIGINT,token_id BIGINT,\"group\" TEXT,ip TEXT,other TEXT)")
            .execute(&pg).await.unwrap();
        // Additive migration must be safe to rerun on a migrated database.
        sqlx::raw_sql(&migration).execute(&pg).await.unwrap();
        sqlx::raw_sql("INSERT INTO users(id,email,inviter_id) VALUES(1,'inviter@example.com',0),(7,'payer@example.com',1),(8,'other@example.com',0);
            INSERT INTO options VALUES('payment_setting.compliance_confirmed','true'),('payment_setting.compliance_terms_version','v1'),('QuotaForInviter','75');")
            .execute(&pg).await.unwrap();
        Self { admin, pg, schema }
    }

    async fn pending(&self, trade: &str, user_id: i64, discount: i64) {
        sqlx::query("INSERT INTO top_ups(user_id,amount,money,trade_no,payment_method,payment_provider,status,expected_amount_micros,credited_quota,platform_amount_micros,settlement_currency,discount_code_id) VALUES($1,10,10,$2,'alipay','epay','pending',10000000,5000000,10000000,'CNY',$3)")
            .bind(user_id).bind(trade).bind(discount).execute(&self.pg).await.unwrap();
    }

    async fn coupon(&self, trade: &str) {
        sqlx::query("INSERT INTO discount_codes(id,code,discount_percent,created_time,updated_time,max_uses) VALUES(9,'TEN',10,1,1,1)")
            .execute(&self.pg).await.unwrap();
        sqlx::query("INSERT INTO discount_code_reservations(discount_code_id,top_up_trade_no,user_id,status,expires_time,created_time,updated_time) VALUES(9,$1,7,'reserved',1,1,1)")
            .bind(trade).execute(&self.pg).await.unwrap();
    }

    async fn quota(&self, user: i64) -> i64 {
        sqlx::query_scalar("SELECT quota FROM users WHERE id=$1")
            .bind(user)
            .fetch_one(&self.pg)
            .await
            .unwrap()
    }

    async fn clean(self) {
        self.pg.close().await;
        sqlx::query(&format!("DROP SCHEMA {} CASCADE", self.schema))
            .execute(&self.admin)
            .await
            .unwrap();
        self.admin.close().await;
    }
}

fn request_amount(amount: &str, discount_code: &str) -> CreateTopup {
    CreateTopup {
        user_id: 7,
        amount: Decimal::from_str_exact(amount).unwrap(),
        payment_method: "alipay".into(),
        provider: "epay",
        discount_code: discount_code.into(),
    }
}

#[tokio::test]
#[ignore = "requires LMM_EPAY_TEST_DATABASE_URL pointing to disposable PostgreSQL"]
async fn fractional_checkout_snapshots_pricing_and_ignores_stale_builtin_rates() -> TestResult {
    let fixture = Harness::new().await;
    fixture.checkout_settings().await;
    fixture.option("PayMethods",r#"[{"type":"alipay","settlement_currency":"USD","unit_price":"999","settlement_units_per_platform_unit":"888"}]"#).await;
    let repository = PgEpayRepository::new(fixture.pg.clone());
    let quote = repository
        .quote(request_amount("12.345678", ""))
        .await
        .unwrap();
    assert_eq!(quote.money, "6.17");
    assert_eq!(quote.snapshot.settlement_currency, "CNY");
    assert_eq!(quote.snapshot.credited_quota, 6_172_839);
    assert_eq!(quote.snapshot.platform_amount_micros, 12_345_678);
    let pending = repository.create_quoted_pending(quote).await.unwrap();
    fixture.option("QuotaPerUnit", "42").await;
    fixture.option("TopUpPlatformUnitsPerCNY", "10").await;
    let callback = EpayCallback {
        money: "6.17".into(),
        ..evidence(&pending.trade_no, "fractional-provider")
    };
    assert_eq!(
        repository.complete_verified(&callback, "{}").await.unwrap(),
        Completion::Completed
    );
    assert_eq!(
        fixture.quota(7).await,
        6_172_839,
        "settlement must use the order snapshot after options change"
    );
    fixture.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires LMM_EPAY_TEST_DATABASE_URL pointing to disposable PostgreSQL"]
async fn tokens_group_discount_and_private_coupon_preserve_go_order_of_rounding() -> TestResult {
    let fixture = Harness::new().await;
    fixture.checkout_settings().await;
    fixture
        .option("general_setting.quota_display_type", "TOKENS")
        .await;
    fixture
        .option("TopupGroupRatio", r#"{"default":0.8}"#)
        .await;
    fixture
        .option("payment_setting.amount_discount", r#"{"500000":0.9}"#)
        .await;
    sqlx::query("INSERT INTO discount_codes(id,code,discount_percent,min_amount,owner_user_id,created_time,updated_time,max_uses) VALUES(9,'PRIVATE',10,0,7,1,1,1)").execute(&fixture.pg).await?;
    let repository = PgEpayRepository::new(fixture.pg.clone());
    assert_eq!(repository.minimum_amount().await.unwrap(), 500_000);
    let quote = repository
        .quote(request_amount("500000", "private"))
        .await
        .unwrap();
    assert_eq!(quote.money, "0.32");
    assert_eq!(quote.snapshot.credited_quota, 500_000);
    assert_eq!(quote.snapshot.platform_amount_micros, 1_000_000);
    assert_eq!(quote.snapshot.discount_code_id, 9);
    let mut stolen = request_amount("500000", "private");
    stolen.user_id = 8;
    assert!(repository.quote(stolen).await.is_err());
    let (first, second) = tokio::join!(
        repository.create_quoted_pending(quote.clone()),
        repository.create_quoted_pending(quote)
    );
    assert_eq!(usize::from(first.is_ok()) + usize::from(second.is_ok()), 1);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM top_ups")
            .fetch_one(&fixture.pg)
            .await?,
        1
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT count(*) FROM discount_code_reservations WHERE status='reserved'"
        )
        .fetch_one(&fixture.pg)
        .await?,
        1
    );
    fixture.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires LMM_EPAY_TEST_DATABASE_URL pointing to disposable PostgreSQL"]
async fn checkout_enforces_duplicate_policies_audience_unlock_and_limits() -> TestResult {
    let fixture = Harness::new().await;
    fixture.checkout_settings().await;
    let repository = PgEpayRepository::new(fixture.pg.clone());
    for methods in [
        r#"[{"type":"alipay","enabled":"true"},{"type":"alipay","enabled":"false"}]"#,
        r#"[{"type":"alipay","enabled":"maybe"}]"#,
        r#"[{"type":"alipay","audience_mode":"include","audience_email_contains":"other@example.com"}]"#,
        r#"[{"type":"alipay","audience_mode":"include","audience_linuxdo_score_min":"10000"}]"#,
        r#"[{"type":"alipay","min_topup":"2"}]"#,
        r#"[{"type":"alipay","max_topup":"5"},{"type":"alipay","max_topup":"0.5"}]"#,
        r#"[{"type":"alipay","unlock_after_days":"9223372036854775807"}]"#,
    ] {
        fixture.option("PayMethods", methods).await;
        assert!(
            repository.quote(request_amount("14.6", "")).await.is_err(),
            "{methods}"
        );
    }
    fixture
        .option("PayMethods", r#"[{"type":"alipay","audience_mode":"all"}]"#)
        .await;
    sqlx::query("UPDATE users SET payment_restriction_flags=2,email='payer@linux.do' WHERE id=7")
        .execute(&fixture.pg)
        .await?;
    assert!(
        repository.quote(request_amount("14.6", "")).await.is_ok(),
        "explicit all overrides legacy marker"
    );
    fixture.option("PayMethods", r#"[{"type":"alipay"}]"#).await;
    assert!(repository.quote(request_amount("14.6", "")).await.is_err());
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM top_ups")
            .fetch_one(&fixture.pg)
            .await?,
        0
    );
    fixture.clean().await;
    Ok(())
}

struct Auth;
#[async_trait]
impl TopupAuthorizer for Auth {
    async fn user_id(&self, _: &HeaderMap) -> Result<i64, TopupError> {
        Ok(7)
    }
    async fn check_critical_rate_limit(
        &self,
        _: &str,
    ) -> Result<CriticalRateLimitOutcome, TopupError> {
        Ok(CriticalRateLimitOutcome::Allowed)
    }
}

async fn http_body(app: Router, request: Request<Body>) -> String {
    let response = app.oneshot(request).await.unwrap();
    assert_eq!(response.status(), 200);
    String::from_utf8(
        to_bytes(response.into_body(), 1024 * 1024)
            .await
            .unwrap()
            .to_vec(),
    )
    .unwrap()
}

fn signed_callback(trade: &str, money: &str, key: &str) -> String {
    signed_callback_method(trade, money, key, "alipay")
}

fn signed_callback_method(trade: &str, money: &str, key: &str, method: &str) -> String {
    let mut fields = BTreeMap::from([
        ("money", money.to_owned()),
        ("out_trade_no", trade.to_owned()),
        ("trade_no", format!("paid-{trade}")),
        ("trade_status", "TRADE_SUCCESS".into()),
        ("type", method.to_owned()),
    ]);
    let input = fields
        .iter()
        .map(|(name, value)| format!("{name}={value}"))
        .collect::<Vec<_>>()
        .join("&")
        + key;
    fields.insert("sign", md5_hex(input.as_bytes()));
    form_urlencoded::Serializer::new(String::new())
        .extend_pairs(fields)
        .finish()
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL; tests/scripts/epay-current-differential.py regenerates the reference from current Go"]
async fn current_go_checkout_notification_and_rejection_fixtures_match_rust() -> TestResult {
    let inputs: Vec<Value> =
        serde_json::from_str(include_str!("fixtures/epay-current-go-input.json"))?;
    let reference: Value = if let Ok(path) = std::env::var("LMM_EPAY_GO_ORACLE_OUTPUT") {
        serde_json::from_str(&std::fs::read_to_string(path)?)?
    } else {
        serde_json::from_str(include_str!("fixtures/epay-current-go-output.json"))?
    };
    assert_eq!(reference["fixture_count"], inputs.len().to_string());
    for input in inputs {
        let fixture = Harness::new().await;
        fixture.checkout_settings().await;
        fixture
            .option("PayMethods", &input["methods"].to_string())
            .await;
        for (key, value) in input["options"].as_object().unwrap() {
            fixture.option(key, value.as_str().unwrap()).await;
        }
        let app = router(UserTopupState::new(
            Arc::new(Auth),
            Arc::new(PgEpayRepository::new(fixture.pg.clone())),
            Arc::new(PgEpayGateway::new(fixture.pg.clone())),
        ));
        let name = input["name"].as_str().unwrap();
        let method = input["method"].as_str().unwrap();
        let body = input
            .get("body")
            .and_then(Value::as_str)
            .map(str::to_owned)
            .unwrap_or_else(|| {
                format!(
                    r#"{{"amount":{},"payment_method":{}}}"#,
                    input["amount"].as_str().unwrap(),
                    input["method"]
                )
            });
        let mut request = Request::post("/api/user/pay")
            .header("content-type", "application/json")
            .body(Body::from(body))?;
        request
            .extensions_mut()
            .insert(ClientIpKey("127.0.0.1".into()));
        let response: Value = serde_json::from_str(&http_body(app.clone(), request).await)?;
        if response["message"] != "success" {
            let count: i64 = sqlx::query_scalar("SELECT count(*) FROM top_ups")
                .fetch_one(&fixture.pg)
                .await?;
            assert_eq!(
                json!({"response":response,"order_count":count}),
                reference[name],
                "Go/Rust rejection {name}"
            );
            fixture.clean().await;
            continue;
        }
        let data = &response["data"];
        let trade = data["out_trade_no"].as_str().unwrap();
        let money = data["money"].as_str().unwrap();
        let row=sqlx::query("SELECT amount,platform_amount_micros,credited_quota,settlement_currency FROM top_ups WHERE trade_no=$1").bind(trade).fetch_one(&fixture.pg).await?;
        let canonical = data
            .as_object()
            .unwrap()
            .iter()
            .filter(|(key, value)| {
                !matches!(key.as_str(), "sign" | "sign_type") && !value.as_str().unwrap().is_empty()
            })
            .map(|(key, value)| format!("{key}={}", value.as_str().unwrap()))
            .collect::<Vec<_>>()
            .join("&")
            + "fixture-key";
        let signature_valid = data["sign"] == md5_hex(canonical.as_bytes());
        let mut acks = vec![];
        for (amount, key) in [
            (money, "wrong-key"),
            ("0.01", "fixture-key"),
            (money, "fixture-key"),
            (money, "fixture-key"),
        ] {
            let query = signed_callback_method(trade, amount, key, method);
            let request =
                Request::get(format!("/api/user/epay/notify?{query}")).body(Body::empty())?;
            acks.push(http_body(app.clone(), request).await);
        }
        let logs: i64 = sqlx::query_scalar("SELECT count(*) FROM logs WHERE type=1")
            .fetch_one(&fixture.pg)
            .await?;
        let actual = json!({"money":money,"name":data["name"],"url":response["url"],"currency":row.get::<String,_>("settlement_currency"),"amount":row.get::<i64,_>("amount"),"platform_amount_micros":row.get::<i64,_>("platform_amount_micros"),"credited_quota":row.get::<i64,_>("credited_quota"),"signature_valid":signature_valid,"acks":acks,"wallet_quota":fixture.quota(7).await,"topup_logs":logs});
        assert_eq!(
            actual, reference[name],
            "Go/Rust checkout + signed settlement {name}"
        );
        fixture.clean().await;
    }
    let signature=md5_hex(b"money=2.62&out_trade_no=USR7NOfixed&trade_no=provider-fixed&trade_status=TRADE_SUCCESS&type=alipayfixture-key");
    assert_eq!(signature, reference["notification_signature"]);
    Ok(())
}

#[tokio::test]
#[ignore = "requires LMM_EPAY_TEST_DATABASE_URL pointing to disposable PostgreSQL"]
async fn http_checkout_and_signed_callback_use_live_credentials_and_ack_only_committed_credit()
-> TestResult {
    let fixture = Harness::new().await;
    fixture.checkout_settings().await;
    let app = router(UserTopupState::new(
        Arc::new(Auth),
        Arc::new(PgEpayRepository::new(fixture.pg.clone())),
        Arc::new(PgEpayGateway::new(fixture.pg.clone())),
    ));
    let mut request = Request::post("/api/user/pay")
        .header("content-type", "application/json")
        .body(Body::from(r#"{"amount":5.25,"payment_method":"alipay"}"#))?;
    request
        .extensions_mut()
        .insert(ClientIpKey("127.0.0.1".into()));
    let response: Value = serde_json::from_str(&http_body(app.clone(), request).await)?;
    assert_eq!(response["message"], "success");
    assert_eq!(response["url"], "https://pay.example/gateway/submit.php");
    assert_eq!(response["data"]["money"], "2.62");
    assert_eq!(response["data"]["name"], "TUC5.25");
    let trade = response["data"]["out_trade_no"].as_str().unwrap();
    let notify = |query: String| {
        Request::get(format!("/api/user/epay/notify?{query}"))
            .body(Body::empty())
            .unwrap()
    };
    assert_eq!(
        http_body(
            app.clone(),
            notify(signed_callback(trade, "2.61", "fixture-key"))
        )
        .await,
        "fail"
    );
    assert_eq!(fixture.quota(7).await, 0);
    fixture.option("EpayKey", "rotated-key").await;
    assert_eq!(
        http_body(
            app.clone(),
            notify(signed_callback(trade, "2.62", "fixture-key"))
        )
        .await,
        "fail"
    );
    assert_eq!(
        http_body(
            app.clone(),
            notify(signed_callback(trade, "2.62", "rotated-key"))
        )
        .await,
        "success"
    );
    assert_eq!(
        http_body(
            app.clone(),
            notify(signed_callback(trade, "2.62", "rotated-key"))
        )
        .await,
        "success"
    );
    assert_eq!(fixture.quota(7).await, 2_625_000);
    fixture
        .option("payment_setting.compliance_confirmed", "false")
        .await;
    assert_eq!(
        http_body(
            app.clone(),
            notify(signed_callback(trade, "2.62", "rotated-key"))
        )
        .await,
        "fail"
    );
    fixture.clean().await;
    Ok(())
}

fn evidence(trade: &str, transaction: &str) -> EpayCallback {
    EpayCallback {
        verified: true,
        trade_success: true,
        trade_no: trade.into(),
        payment_method: "alipay".into(),
        money: "10.00".into(),
        provider_transaction_id: transaction.into(),
    }
}

#[tokio::test]
#[ignore = "requires LMM_EPAY_TEST_DATABASE_URL pointing to disposable PostgreSQL"]
async fn concurrent_replays_credit_coupon_and_referral_exactly_once() -> TestResult {
    let fixture = Harness::new().await;
    fixture.pending("replayed", 7, 9).await;
    fixture.coupon("replayed").await;
    let repository = PgEpayRepository::new(fixture.pg.clone());
    let callback = evidence("replayed", "provider-1");
    let results = futures_util::future::join_all(
        (0..12).map(|_| repository.complete_verified(&callback, "{}")),
    )
    .await;
    assert_eq!(
        results
            .iter()
            .filter(|result| matches!(result, Ok(Completion::Completed)))
            .count(),
        1
    );
    assert_eq!(
        results
            .iter()
            .filter(|result| matches!(result, Ok(Completion::AlreadySucceeded)))
            .count(),
        11
    );
    assert_eq!(fixture.quota(7).await, 5_000_000);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM logs WHERE type=1")
            .fetch_one(&fixture.pg)
            .await?,
        1
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT used_count FROM discount_codes WHERE id=9")
            .fetch_one(&fixture.pg)
            .await?,
        1
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT status FROM discount_code_reservations")
            .fetch_one(&fixture.pg)
            .await?,
        "consumed"
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT aff_quota FROM users WHERE id=1")
            .fetch_one(&fixture.pg)
            .await?,
        75
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM referral_rewards")
            .fetch_one(&fixture.pg)
            .await?,
        1
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM referral_ledger_entries")
            .fetch_one(&fixture.pg)
            .await?,
        1
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT settled_amount_micros FROM top_ups WHERE trade_no='replayed'"
        )
        .fetch_one(&fixture.pg)
        .await?,
        10_000_000
    );
    fixture.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires LMM_EPAY_TEST_DATABASE_URL and LMM_EPAY_TEST_VALKEY_URL pointing to disposable services"]
async fn logs_and_cache_are_post_commit_effects_and_failure_cannot_unpay_an_order() -> TestResult {
    let fixture = Harness::new().await;
    fixture.pending("logged", 7, 0).await;
    let valkey = redis::Client::open(std::env::var("LMM_EPAY_TEST_VALKEY_URL")?)?;
    let mut connection = valkey.get_multiplexed_async_connection().await?;
    redis::cmd("HSET")
        .arg("user:7")
        .arg("Quota")
        .arg(0)
        .query_async::<i64>(&mut connection)
        .await?;
    let repository = PgEpayRepository::new(fixture.pg.clone()).with_valkey(valkey);
    assert_eq!(
        repository
            .complete_verified_at(&evidence("logged", "log-provider"), "{}", "203.0.113.9")
            .await
            .unwrap(),
        Completion::Completed
    );
    assert_eq!(
        redis::cmd("EXISTS")
            .arg("user:7")
            .query_async::<i64>(&mut connection)
            .await?,
        0
    );
    let log = sqlx::query("SELECT content,ip,other FROM logs")
        .fetch_one(&fixture.pg)
        .await?;
    assert_eq!(
        log.get::<String, _>("content"),
        "使用在线充值成功，充值金额: ＄10.000000 额度，支付金额：10.000000"
    );
    assert_eq!(log.get::<String, _>("ip"), "203.0.113.9");
    let metadata: Value = serde_json::from_str(&log.get::<String, _>("other"))?;
    assert_eq!(metadata["admin_info"]["callback_payment_method"], "epay");
    assert_eq!(metadata["admin_info"]["payment_method"], "alipay");
    assert!(!log.get::<String, _>("other").contains("log-provider"));
    fixture.pending("broken-log", 7, 0).await;
    sqlx::query("DROP TABLE logs").execute(&fixture.pg).await?;
    let repository = PgEpayRepository::new(fixture.pg.clone())
        .with_valkey(redis::Client::open("redis://127.0.0.1:1/0")?);
    assert_eq!(
        repository
            .complete_verified_at(
                &evidence("broken-log", "second-provider"),
                "{}",
                "203.0.113.9"
            )
            .await
            .unwrap(),
        Completion::Completed
    );
    assert_eq!(fixture.quota(7).await, 10_000_000);
    assert_eq!(
        repository
            .complete_verified(&evidence("broken-log", "second-provider"), "{}")
            .await
            .unwrap(),
        Completion::AlreadySucceeded
    );
    assert_eq!(fixture.quota(7).await, 10_000_000);
    fixture.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires LMM_EPAY_TEST_DATABASE_URL pointing to disposable PostgreSQL"]
async fn amount_provider_method_and_missing_snapshots_reject_without_credit() -> TestResult {
    let fixture = Harness::new().await;
    fixture.pending("bound", 7, 0).await;
    let repository = PgEpayRepository::new(fixture.pg.clone());
    for callback in [
        EpayCallback {
            money: "9.99".into(),
            ..evidence("bound", "provider-1")
        },
        EpayCallback {
            money: "10.0000001".into(),
            ..evidence("bound", "provider-1")
        },
        EpayCallback {
            payment_method: "wxpay".into(),
            ..evidence("bound", "provider-1")
        },
        EpayCallback {
            verified: false,
            ..evidence("bound", "provider-1")
        },
        evidence("unknown", "provider-1"),
        evidence("bound", ""),
    ] {
        assert!(repository.complete_verified(&callback, "{}").await.is_err());
    }
    for change in [
        "payment_provider='stripe'",
        "expected_amount_micros=0",
        "credited_quota=0",
        "settlement_currency=''",
        "status='failed'",
    ] {
        sqlx::query(&format!(
            "UPDATE top_ups SET {change} WHERE trade_no='bound'"
        ))
        .execute(&fixture.pg)
        .await?;
        assert!(
            repository
                .complete_verified(&evidence("bound", "provider-1"), "{}")
                .await
                .is_err(),
            "{change}"
        );
        sqlx::query("UPDATE top_ups SET payment_provider='epay',expected_amount_micros=10000000,credited_quota=5000000,settlement_currency='CNY',status='pending'").execute(&fixture.pg).await?;
    }
    assert_eq!(fixture.quota(7).await, 0);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM referral_rewards")
            .fetch_one(&fixture.pg)
            .await?,
        0
    );
    fixture.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires LMM_EPAY_TEST_DATABASE_URL pointing to disposable PostgreSQL"]
async fn one_provider_transaction_cannot_pay_two_concurrent_orders() -> TestResult {
    let fixture = Harness::new().await;
    fixture.pending("first", 7, 0).await;
    fixture.pending("second", 8, 0).await;
    let repository = PgEpayRepository::new(fixture.pg.clone());
    let first = evidence("first", "unique-provider-id");
    let second = evidence("second", "unique-provider-id");
    let results = tokio::join!(
        repository.complete_verified(&first, "{}"),
        repository.complete_verified(&second, "{}")
    );
    assert_eq!(
        usize::from(results.0.is_ok()) + usize::from(results.1.is_ok()),
        1
    );
    assert_eq!(fixture.quota(7).await + fixture.quota(8).await, 5_000_000);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM top_ups WHERE status='success'")
            .fetch_one(&fixture.pg)
            .await?,
        1
    );
    fixture.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires LMM_EPAY_TEST_DATABASE_URL pointing to disposable PostgreSQL"]
async fn wallet_ceiling_and_coupon_failure_roll_back_the_entire_payment() -> TestResult {
    let fixture = Harness::new().await;
    fixture.pending("atomic", 7, 9).await;
    fixture.coupon("atomic").await;
    let repository = PgEpayRepository::new(fixture.pg.clone());
    let callback = evidence("atomic", "atomic-provider-id");
    sqlx::query("UPDATE users SET quota=$1 WHERE id=7")
        .bind(MAX_QUOTA - 1)
        .execute(&fixture.pg)
        .await?;
    assert!(repository.complete_verified(&callback, "{}").await.is_err());
    assert_eq!(fixture.quota(7).await, MAX_QUOTA - 1);
    sqlx::query("UPDATE users SET quota=0 WHERE id=7")
        .execute(&fixture.pg)
        .await?;
    sqlx::query("ALTER TABLE discount_codes ADD CONSTRAINT simulate_coupon_write_failure CHECK(used_count=0)").execute(&fixture.pg).await?;
    assert!(repository.complete_verified(&callback, "{}").await.is_err());
    assert_eq!(fixture.quota(7).await, 0);
    let row =
        sqlx::query("SELECT status,settled_amount_micros,provider_transaction_id FROM top_ups")
            .fetch_one(&fixture.pg)
            .await?;
    assert_eq!(row.get::<String, _>("status"), "pending");
    assert_eq!(row.get::<i64, _>("settled_amount_micros"), 0);
    assert!(
        row.get::<Option<String>, _>("provider_transaction_id")
            .is_none()
    );
    sqlx::query("ALTER TABLE discount_codes DROP CONSTRAINT simulate_coupon_write_failure")
        .execute(&fixture.pg)
        .await?;
    assert_eq!(
        repository.complete_verified(&callback, "{}").await.unwrap(),
        Completion::Completed
    );
    assert_eq!(fixture.quota(7).await, 5_000_000);
    fixture.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires LMM_EPAY_TEST_DATABASE_URL pointing to disposable PostgreSQL"]
async fn referral_ledger_failure_rolls_back_wallet_order_and_coupon_then_retries() -> TestResult {
    let fixture = Harness::new().await;
    fixture.pending("ledger", 7, 9).await;
    fixture.coupon("ledger").await;
    sqlx::query(
        "ALTER TABLE referral_ledger_entries ADD CONSTRAINT simulate_ledger_failure CHECK(quota<0)",
    )
    .execute(&fixture.pg)
    .await?;
    let repository = PgEpayRepository::new(fixture.pg.clone());
    let callback = evidence("ledger", "ledger-provider-id");
    assert!(repository.complete_verified(&callback, "{}").await.is_err());
    assert_eq!(fixture.quota(7).await, 0);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT used_count FROM discount_codes WHERE id=9")
            .fetch_one(&fixture.pg)
            .await?,
        0
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT referral_first_top_up_id FROM users WHERE id=7")
            .fetch_one(&fixture.pg)
            .await?,
        0
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT aff_quota FROM users WHERE id=1")
            .fetch_one(&fixture.pg)
            .await?,
        0
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM referral_rewards")
            .fetch_one(&fixture.pg)
            .await?,
        0
    );
    sqlx::query("ALTER TABLE referral_ledger_entries DROP CONSTRAINT simulate_ledger_failure")
        .execute(&fixture.pg)
        .await?;
    assert_eq!(
        repository.complete_verified(&callback, "{}").await.unwrap(),
        Completion::Completed
    );
    fixture.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires LMM_EPAY_TEST_DATABASE_URL pointing to disposable PostgreSQL"]
async fn two_real_payments_share_one_first_topup_reward_and_ldc_never_grants_it() -> TestResult {
    let fixture = Harness::new().await;
    fixture.pending("ldc", 7, 0).await;
    fixture.pending("cash-1", 7, 0).await;
    fixture.pending("cash-2", 7, 0).await;
    sqlx::query(
        "UPDATE top_ups SET payment_method='ldc',settlement_currency='LDC' WHERE trade_no='ldc'",
    )
    .execute(&fixture.pg)
    .await?;
    let repository = PgEpayRepository::new(fixture.pg.clone());
    let ldc = EpayCallback {
        payment_method: "ldc".into(),
        ..evidence("ldc", "ldc-id")
    };
    assert_eq!(
        repository.complete_verified(&ldc, "{}").await.unwrap(),
        Completion::Completed
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT referral_first_top_up_id FROM users WHERE id=7")
            .fetch_one(&fixture.pg)
            .await?,
        0
    );
    let one = evidence("cash-1", "cash-id-1");
    let two = evidence("cash-2", "cash-id-2");
    let (first, second) = tokio::join!(
        repository.complete_verified(&one, "{}"),
        repository.complete_verified(&two, "{}")
    );
    assert_eq!(first.unwrap(), Completion::Completed);
    assert_eq!(second.unwrap(), Completion::Completed);
    assert_eq!(fixture.quota(7).await, 15_000_000);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM referral_rewards")
            .fetch_one(&fixture.pg)
            .await?,
        1
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT aff_quota FROM users WHERE id=1")
            .fetch_one(&fixture.pg)
            .await?,
        75
    );
    fixture.clean().await;
    Ok(())
}
