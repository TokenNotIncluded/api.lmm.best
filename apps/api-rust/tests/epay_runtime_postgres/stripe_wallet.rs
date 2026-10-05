use super::*;
#[path = "stripe_subscription_checkout.rs"]
mod subscription_pay;
use axum::{
    Json,
    extract::State,
    http::StatusCode,
    response::IntoResponse,
    routing::{get, post},
};
use hmac::{Hmac, Mac};
use lmm_api_rs::routes::stripe_creem::{
    self, DisabledStripeCreemGateway, PgStripeCreemStore, StripeCreemAuthorizer, StripeCreemState,
    TopupAuthError, TopupPrincipal,
    stripe_provider::StripeApiClient,
    stripe_wallet::{StripeWalletState, webhook_router},
};
use sha2::Sha256;
use std::{
    sync::{
        Mutex,
        atomic::{AtomicU8, Ordering},
    },
    time::{SystemTime, UNIX_EPOCH},
};

#[async_trait]
impl StripeCreemAuthorizer for Auth {
    async fn principal(&self, _: &HeaderMap) -> Result<TopupPrincipal, TopupAuthError> {
        Ok(TopupPrincipal { user_id: 7 })
    }
}

type CheckoutObservation = (BTreeMap<String, String>, bool);

#[derive(Clone)]
struct Provider {
    pg: PgPool,
    mode: Arc<AtomicU8>,
    sessions: Arc<Mutex<Vec<CheckoutObservation>>>,
}

async fn price(headers: HeaderMap) -> Json<Value> {
    assert_eq!(headers["authorization"], "Bearer sk_fixture");
    Json(json!({"id":"price_fixture","currency":"usd","product":"prod_fixture"}))
}

async fn session(
    State(provider): State<Provider>,
    headers: HeaderMap,
    body: String,
) -> axum::response::Response {
    assert_eq!(headers["authorization"], "Bearer sk_fixture");
    let fields: BTreeMap<String, String> = form_urlencoded::parse(body.as_bytes())
        .into_owned()
        .collect();
    let trade = &fields["client_reference_id"];
    let exists:bool=sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM top_ups WHERE trade_no=$1 AND status='pending' AND expected_amount_micros>0 AND credited_quota>0)").bind(trade).fetch_one(&provider.pg).await.unwrap();
    let amount = fields["line_items[0][price_data][unit_amount]"]
        .parse::<i64>()
        .unwrap();
    provider.sessions.lock().unwrap().push((fields, exists));
    match provider.mode.load(Ordering::SeqCst) {
        1 => (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(json!({"error":{"message":"fixture temporary failure"}})),
        )
            .into_response(),
        2 => Json(
            json!({"url":"https://checkout.stripe.test/cs_fixture","amount_subtotal":amount+1}),
        )
        .into_response(),
        _ => {
            Json(json!({"url":"https://checkout.stripe.test/cs_fixture","amount_subtotal":amount}))
                .into_response()
        }
    }
}

struct StripeHarness {
    fixture: Harness,
    app: Router,
    provider: Provider,
    server: tokio::task::JoinHandle<()>,
}

impl StripeHarness {
    async fn subscription_order(&self, trade: &str) {
        sqlx::query("UPDATE subscription_plans SET title='Purchased plan',upgrade_group='pro',downgrade_group='default',quota_reset_period='custom',quota_reset_custom_seconds=1800 WHERE id=3")
            .execute(&self.fixture.pg).await.unwrap();
        let snapshot: String =
            sqlx::query_scalar("SELECT to_jsonb(p)::text FROM subscription_plans p WHERE id=3")
                .fetch_one(&self.fixture.pg)
                .await
                .unwrap();
        sqlx::query("INSERT INTO subscription_orders(trade_no,payment_provider,status,expected_amount_micros,settlement_currency,provider_product_id,plan_snapshot) VALUES($1,'stripe','pending',1000000,'USD','price_plan',$2)")
            .bind(trade).bind(snapshot).execute(&self.fixture.pg).await.unwrap();
        sqlx::query(
            "UPDATE subscription_plans SET total_amount=9000,title='Changed live plan' WHERE id=3",
        )
        .execute(&self.fixture.pg)
        .await
        .unwrap();
    }
    async fn new() -> Self {
        let fixture = Harness::new().await;
        fixture.checkout_settings().await;
        sqlx::query("CREATE TABLE subscription_orders(id BIGSERIAL PRIMARY KEY,user_id BIGINT DEFAULT 7,plan_id BIGINT DEFAULT 3,money NUMERIC DEFAULT 1,trade_no TEXT UNIQUE,payment_provider TEXT,payment_method TEXT DEFAULT 'stripe',status TEXT,create_time BIGINT DEFAULT 1,complete_time BIGINT DEFAULT 0,provider_payload TEXT DEFAULT '')").execute(&fixture.pg).await.unwrap();
        sqlx::raw_sql(
            &include_str!("../../migrations/0013_payment_extensions.sql")
                .replace("__LMM_APP_SCHEMA__", &fixture.schema),
        )
        .execute(&fixture.pg)
        .await
        .unwrap();
        for (key, value) in [
            ("StripeApiSecret", "sk_fixture"),
            ("StripeWebhookSecret", "whsec_fixture"),
            ("StripePriceId", "price_fixture"),
            ("StripeMinTopUp", "1"),
        ] {
            fixture.option(key, value).await;
        }
        sqlx::raw_sql("CREATE TABLE subscription_plans(id BIGINT PRIMARY KEY,title TEXT NOT NULL DEFAULT 'Plan',price_amount NUMERIC NOT NULL DEFAULT 1,currency TEXT NOT NULL DEFAULT 'USD',duration_unit TEXT NOT NULL DEFAULT 'day',duration_value BIGINT NOT NULL DEFAULT 1,custom_seconds BIGINT NOT NULL DEFAULT 0,total_amount BIGINT NOT NULL DEFAULT 1000,max_purchase_per_user BIGINT NOT NULL DEFAULT 0,upgrade_group TEXT NOT NULL DEFAULT '',downgrade_group TEXT NOT NULL DEFAULT '',quota_reset_period TEXT NOT NULL DEFAULT 'never',quota_reset_custom_seconds BIGINT NOT NULL DEFAULT 0,allow_wallet_overflow BOOLEAN NOT NULL DEFAULT TRUE); INSERT INTO subscription_plans(id) VALUES(3);
            CREATE TABLE user_subscriptions(id BIGSERIAL PRIMARY KEY,user_id BIGINT,plan_id BIGINT,amount_total BIGINT,reset_amount BIGINT,renewal_amount BIGINT,amount_used BIGINT,quota_version BIGINT NOT NULL DEFAULT 0,start_time BIGINT,end_time BIGINT,status TEXT,source TEXT,last_reset_time BIGINT,next_reset_time BIGINT,upgrade_group TEXT,prev_user_group TEXT,downgrade_group TEXT,allow_wallet_overflow BOOLEAN,created_at BIGINT,updated_at BIGINT)")
            .execute(&fixture.pg).await.unwrap();
        let provider = Provider {
            pg: fixture.pg.clone(),
            mode: Arc::new(AtomicU8::new(0)),
            sessions: Arc::new(Mutex::new(vec![])),
        };
        let service = Router::new()
            .route("/v1/prices/price_fixture", get(price))
            .route("/v1/checkout/sessions", post(session))
            .with_state(provider.clone());
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let endpoint = format!("http://{}/", listener.local_addr().unwrap());
        let server = tokio::spawn(async move {
            axum::serve(listener, service).await.unwrap();
        });
        let valkey = redis::Client::open(
            std::env::var("LMM_EPAY_TEST_VALKEY_URL").expect("isolated Valkey URL"),
        )
        .unwrap();
        let native = StripeWalletState::new(
            fixture.pg.clone(),
            valkey,
            Arc::new(Auth),
            StripeApiClient::loopback(&endpoint).unwrap(),
        );
        let state = StripeCreemState::new(
            Arc::new(PgStripeCreemStore::new(fixture.pg.clone())),
            Arc::new(Auth),
            Arc::new(DisabledStripeCreemGateway),
        )
        .with_native_stripe(native.clone());
        let app = stripe_creem::router(state).merge(webhook_router(native));
        Self {
            fixture,
            app,
            provider,
            server,
        }
    }

    async fn pay(&self, body: Value) -> Value {
        let mut request = Request::post("/api/user/stripe/pay")
            .header("content-type", "application/json")
            .body(Body::from(body.to_string()))
            .unwrap();
        request
            .extensions_mut()
            .insert(ClientIpKey("127.0.0.1".into()));
        serde_json::from_str(&http_body(self.app.clone(), request).await).unwrap()
    }

    async fn trade(&self) -> String {
        sqlx::query_scalar("SELECT trade_no FROM top_ups ORDER BY id DESC LIMIT 1")
            .fetch_one(&self.fixture.pg)
            .await
            .unwrap()
    }

    async fn notify(&self, event: Value, secret: &str) -> StatusCode {
        let body = event.to_string();
        let timestamp = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs();
        let mut mac = Hmac::<Sha256>::new_from_slice(secret.as_bytes()).unwrap();
        mac.update(format!("{timestamp}.{body}").as_bytes());
        let signature = format!(
            "t={timestamp},v1={}",
            hex::encode(mac.finalize().into_bytes())
        );
        let mut request = Request::post("/api/stripe/webhook")
            .header("stripe-signature", signature)
            .body(Body::from(body))
            .unwrap();
        request
            .extensions_mut()
            .insert(ClientIpKey("203.0.113.5".into()));
        self.app.clone().oneshot(request).await.unwrap().status()
    }

    async fn clean(self) {
        self.server.abort();
        self.fixture.clean().await;
    }
}

fn paid(trade: &str, event_id: &str, intent: &str, subtotal: i64, total: i64) -> Value {
    json!({"id":event_id,"type":"checkout.session.completed","data":{"object":{"id":"cs_fixture","client_reference_id":trade,"status":"complete","payment_status":"paid","currency":"usd","amount_subtotal":subtotal,"amount_total":total,"payment_intent":intent,"customer":"cus_fixture","customer_details":{"email":"payer-contact@example.net"}}}})
}

fn refund(id: &str, intent: &str, amount: i64) -> Value {
    json!({"id":format!("evt_{id}"),"type":"refund.updated","data":{"object":{"id":id,"payment_intent":intent,"status":"succeeded","currency":"usd","amount":amount}}})
}

fn subscription_checkout(trade: &str) -> Value {
    json!({"id":"evt_subscription_checkout","type":"checkout.session.completed","data":{"object":{"id":"cs_subscription","client_reference_id":trade,"mode":"subscription","status":"complete","payment_status":"paid","amount_total":100,"currency":"usd","subscription":"sub_fixture","metadata":{"subscription_price_id":"price_plan"}}}})
}

fn invoice(trade: &str, id: &str, start: i64, end: i64) -> Value {
    json!({"id":format!("evt_{id}"),"type":"invoice.paid","data":{"object":{"id":id,"status":"paid","total":100,"currency":"usd","subscription":"sub_fixture","subscription_details":{"metadata":{"subscription_trade_no":trade}},"lines":{"data":[{"price":{"id":"price_plan"},"period":{"start":start,"end":end}}]}}}})
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey"]
async fn stripe_subscription_checkout_renewal_replay_and_cancellation_preserve_purchased_snapshot()
-> TestResult {
    let harness = StripeHarness::new().await;
    harness.subscription_order("sub_order").await;
    for _ in 0..2 {
        assert_eq!(
            harness
                .notify(subscription_checkout("sub_order"), "whsec_fixture")
                .await,
            StatusCode::OK
        );
    }
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM user_subscriptions")
            .fetch_one(&harness.fixture.pg)
            .await?,
        1
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT amount_total FROM user_subscriptions")
            .fetch_one(&harness.fixture.pg)
            .await?,
        1000,
        "completion must ignore changed live plan grants"
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT \"group\" FROM users WHERE id=7")
            .fetch_one(&harness.fixture.pg)
            .await?,
        "pro"
    );
    assert_eq!(
        harness.fixture.quota(7).await,
        0,
        "subscription shadow top-up never credits wallet"
    );
    let initial = invoice("sub_order", "in_initial", 4_102_444_800, 4_102_448_400);
    assert_eq!(
        harness.notify(initial.clone(), "whsec_fixture").await,
        StatusCode::OK
    );
    assert_eq!(
        harness.notify(initial, "whsec_fixture").await,
        StatusCode::OK
    );
    sqlx::query("UPDATE user_subscriptions SET amount_total=300,amount_used=200")
        .execute(&harness.fixture.pg)
        .await?;
    sqlx::query("UPDATE subscription_orders SET refunded_amount_micros=500000,refunded_quota=700")
        .execute(&harness.fixture.pg)
        .await?;
    let renewal = invoice("sub_order", "in_renewal", 4_102_448_400, 4_102_452_000);
    assert_eq!(
        harness.notify(renewal.clone(), "whsec_fixture").await,
        StatusCode::OK
    );
    assert_eq!(
        harness.notify(renewal, "whsec_fixture").await,
        StatusCode::OK
    );
    let state: (i64, i64, i64, i64) = sqlx::query_as(
        "SELECT amount_total,amount_used,quota_version,end_time FROM user_subscriptions",
    )
    .fetch_one(&harness.fixture.pg)
    .await?;
    assert_eq!(state, (1000, 0, 1, 4_102_452_000));
    sqlx::query("UPDATE user_subscriptions SET amount_used=17")
        .execute(&harness.fixture.pg)
        .await?;
    assert_eq!(
        harness
            .notify(
                invoice("sub_order", "in_late", 4_102_441_200, 4_102_444_800),
                "whsec_fixture"
            )
            .await,
        StatusCode::OK
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT amount_used FROM user_subscriptions")
            .fetch_one(&harness.fixture.pg)
            .await?,
        17
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM subscription_payment_events")
            .fetch_one(&harness.fixture.pg)
            .await?,
        3
    );
    let mut lifecycle = json!({"id":"evt_canceling","type":"customer.subscription.updated","data":{"object":{"id":"sub_fixture","status":"active","cancel_at_period_end":true,"canceled_at":1,"current_period_start":4102448400_i64,"current_period_end":4102452000_i64,"metadata":{"subscription_trade_no":"sub_order"}}}});
    assert_eq!(
        harness.notify(lifecycle.clone(), "whsec_fixture").await,
        StatusCode::OK
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT status FROM user_subscriptions")
            .fetch_one(&harness.fixture.pg)
            .await?,
        "active"
    );
    lifecycle["type"] = json!("customer.subscription.deleted");
    assert_eq!(
        harness.notify(lifecycle.clone(), "whsec_fixture").await,
        StatusCode::OK
    );
    let canceled: (String, i64) = sqlx::query_as("SELECT status,end_time FROM user_subscriptions")
        .fetch_one(&harness.fixture.pg)
        .await?;
    assert_eq!(canceled.0, "cancelled");
    assert!(
        canceled.1 > 1_700_000_000 && canceled.1 < 4_102_444_800,
        "cancellation is not backdated to canceled_at=1"
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT \"group\" FROM users WHERE id=7")
            .fetch_one(&harness.fixture.pg)
            .await?,
        "default"
    );
    lifecycle["type"] = json!("customer.subscription.updated");
    lifecycle["data"]["object"]["cancel_at_period_end"] = json!(false);
    assert_eq!(
        harness.notify(lifecycle, "whsec_fixture").await,
        StatusCode::OK
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>(
            "SELECT provider_subscription_state FROM subscription_orders"
        )
        .fetch_one(&harness.fixture.pg)
        .await?,
        "canceled"
    );
    harness.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey"]
async fn stripe_invoice_before_checkout_retries_receipt_without_granting_twice() -> TestResult {
    let harness = StripeHarness::new().await;
    harness.subscription_order("sub_invoice_first").await;
    sqlx::query("ALTER TABLE subscription_payment_events ADD CONSTRAINT fail_receipt CHECK(settlement_amount_micros<0)").execute(&harness.fixture.pg).await?;
    let first = invoice(
        "sub_invoice_first",
        "in_first",
        4_102_444_800,
        4_102_448_400,
    );
    assert_eq!(
        harness.notify(first.clone(), "whsec_fixture").await,
        StatusCode::INTERNAL_SERVER_ERROR
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM user_subscriptions")
            .fetch_one(&harness.fixture.pg)
            .await?,
        1
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT status FROM subscription_orders")
            .fetch_one(&harness.fixture.pg)
            .await?,
        "success"
    );
    sqlx::query("ALTER TABLE subscription_payment_events DROP CONSTRAINT fail_receipt")
        .execute(&harness.fixture.pg)
        .await?;
    assert_eq!(harness.notify(first, "whsec_fixture").await, StatusCode::OK);
    assert_eq!(
        harness
            .notify(
                invoice(
                    "sub_invoice_first",
                    "in_conflict",
                    4_102_444_800,
                    4_102_448_400
                ),
                "whsec_fixture"
            )
            .await,
        StatusCode::OK
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM subscription_payment_events")
            .fetch_one(&harness.fixture.pg)
            .await?,
        1
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM user_subscriptions")
            .fetch_one(&harness.fixture.pg)
            .await?,
        1
    );
    let mut failed = invoice(
        "sub_invoice_first",
        "in_failed",
        4_102_448_400,
        4_102_452_000,
    );
    failed["type"] = json!("invoice.payment_failed");
    assert_eq!(
        harness.notify(failed, "whsec_fixture").await,
        StatusCode::OK
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT end_time FROM user_subscriptions")
            .fetch_one(&harness.fixture.pg)
            .await?,
        4_102_448_400
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>(
            "SELECT provider_subscription_state FROM subscription_orders"
        )
        .fetch_one(&harness.fixture.pg)
        .await?,
        "past_due"
    );
    harness.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey; current Go lifecycle reference is exported by TestRustStripeSubscriptionCurrentGoOracle"]
async fn stripe_current_go_subscription_reference_matches() -> TestResult {
    let reference: Value =
        if let Ok(path) = std::env::var("LMM_STRIPE_SUBSCRIPTION_GO_ORACLE_OUTPUT") {
            serde_json::from_str(&std::fs::read_to_string(path)?)?
        } else {
            serde_json::from_str(include_str!(
                "../fixtures/stripe-subscription-current-go-output.json"
            ))?
        };
    let harness = StripeHarness::new().await;
    harness.subscription_order("sub_order").await;
    sqlx::query("UPDATE subscription_orders SET plan_snapshot=$1")
        .bind(reference["plan_snapshot"].to_string())
        .execute(&harness.fixture.pg)
        .await?;
    harness.fixture.option("StripePriceId", "").await;
    let mut statuses = vec![];
    for _ in 0..2 {
        statuses.push(
            harness
                .notify(subscription_checkout("sub_order"), "whsec_fixture")
                .await
                .as_u16(),
        );
    }
    assert_eq!(
        json!(
            sqlx::query_scalar::<_, i64>("SELECT amount_total FROM user_subscriptions")
                .fetch_one(&harness.fixture.pg)
                .await?
        ),
        reference["purchased_total"]
    );
    let first = invoice("sub_order", "in_initial", 4_102_444_800, 4_102_448_400);
    for _ in 0..2 {
        statuses.push(
            harness
                .notify(first.clone(), "whsec_fixture")
                .await
                .as_u16(),
        );
    }
    sqlx::query("UPDATE user_subscriptions SET amount_total=300,amount_used=200")
        .execute(&harness.fixture.pg)
        .await?;
    sqlx::query("UPDATE subscription_orders SET refunded_amount_micros=500000,refunded_quota=700")
        .execute(&harness.fixture.pg)
        .await?;
    let next = invoice("sub_order", "in_renewal", 4_102_448_400, 4_102_452_000);
    for _ in 0..2 {
        statuses.push(harness.notify(next.clone(), "whsec_fixture").await.as_u16());
    }
    let renewed=sqlx::query("SELECT amount_total,amount_used,quota_version,start_time,end_time,last_reset_time,next_reset_time FROM user_subscriptions").fetch_one(&harness.fixture.pg).await?;
    for field in [
        "amount_total",
        "amount_used",
        "quota_version",
        "start_time",
        "end_time",
        "last_reset_time",
        "next_reset_time",
    ] {
        assert_eq!(
            json!(renewed.get::<i64, _>(field)),
            reference["renewed"][field],
            "Go/Rust renewal {field}"
        );
    }
    sqlx::query("UPDATE user_subscriptions SET amount_used=17")
        .execute(&harness.fixture.pg)
        .await?;
    statuses.push(
        harness
            .notify(
                invoice("sub_order", "in_late", 4_102_441_200, 4_102_444_800),
                "whsec_fixture",
            )
            .await
            .as_u16(),
    );
    let mut lifecycle = json!({"id":"evt_canceling","type":"customer.subscription.updated","data":{"object":{"id":"sub_fixture","status":"active","cancel_at_period_end":true,"canceled_at":1,"current_period_start":4102448400_i64,"current_period_end":4102452000_i64,"metadata":{"subscription_trade_no":"sub_order"}}}});
    statuses.push(
        harness
            .notify(lifecycle.clone(), "whsec_fixture")
            .await
            .as_u16(),
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT status FROM user_subscriptions")
            .fetch_one(&harness.fixture.pg)
            .await?,
        reference["canceling_status"]
    );
    assert_eq!(
        json!(
            sqlx::query_scalar::<_, i64>("SELECT end_time FROM user_subscriptions")
                .fetch_one(&harness.fixture.pg)
                .await?
        ),
        reference["canceling_end"]
    );
    lifecycle["type"] = json!("customer.subscription.deleted");
    statuses.push(
        harness
            .notify(lifecycle.clone(), "whsec_fixture")
            .await
            .as_u16(),
    );
    lifecycle["type"] = json!("customer.subscription.updated");
    lifecycle["data"]["object"]["cancel_at_period_end"] = json!(false);
    statuses.push(harness.notify(lifecycle, "whsec_fixture").await.as_u16());
    assert_eq!(json!(statuses), reference["statuses"]);
    let final_sub = sqlx::query(
        "SELECT status,amount_used,quota_version,next_reset_time,end_time FROM user_subscriptions",
    )
    .fetch_one(&harness.fixture.pg)
    .await?;
    assert_eq!(
        final_sub.get::<String, _>("status"),
        reference["final_status"]
    );
    for (field, key) in [
        ("amount_used", "final_used"),
        ("quota_version", "final_quota_version"),
        ("next_reset_time", "final_next_reset"),
    ] {
        assert_eq!(json!(final_sub.get::<i64, _>(field)), reference[key]);
    }
    assert_eq!(
        json!(
            final_sub.get::<i64, _>("end_time") > 1_700_000_000
                && final_sub.get::<i64, _>("end_time") < 4_102_444_800
        ),
        reference["cancellation_not_backdated"]
    );
    let order: Value = sqlx::query_scalar("SELECT to_jsonb(o) FROM subscription_orders o")
        .fetch_one(&harness.fixture.pg)
        .await?;
    for (field, key) in [
        ("provider_subscription_state", "provider_state"),
        ("provider_subscription_id", "provider_subscription_id"),
        ("current_period_start", "current_period_start"),
        ("current_period_end", "current_period_end"),
        ("refunded_amount_micros", "refunded_amount_micros"),
        ("refunded_quota", "refunded_quota"),
    ] {
        assert_eq!(
            order[field], reference[key],
            "Go/Rust subscription order {field}"
        );
    }
    for (key, sql) in [
        (
            "receipt_count",
            "SELECT COUNT(*) FROM subscription_payment_events",
        ),
        ("shadow_topup_count", "SELECT COUNT(*) FROM top_ups"),
        (
            "purchase_and_renewal_logs",
            "SELECT COUNT(*) FROM logs WHERE type=1",
        ),
    ] {
        assert_eq!(
            json!(
                sqlx::query_scalar::<_, i64>(sql)
                    .fetch_one(&harness.fixture.pg)
                    .await?
            ),
            reference[key]
        );
    }
    assert_eq!(
        json!(harness.fixture.quota(7).await),
        reference["wallet_quota"]
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT \"group\" FROM users WHERE id=7")
            .fetch_one(&harness.fixture.pg)
            .await?,
        reference["final_group"]
    );
    harness.subscription_order("sub_failure").await;
    sqlx::query("UPDATE subscription_orders SET plan_snapshot=$1 WHERE trade_no='sub_failure'")
        .bind(reference["plan_snapshot"].to_string())
        .execute(&harness.fixture.pg)
        .await?;
    sqlx::query("ALTER TABLE subscription_payment_events ADD CONSTRAINT fail_receipt CHECK(settlement_amount_micros<0) NOT VALID").execute(&harness.fixture.pg).await?;
    let mut event = invoice("sub_failure", "in_failure", 4_102_444_800, 4_102_448_400);
    event["data"]["object"]["subscription"] = json!("sub_failure");
    let status = harness.notify(event.clone(), "whsec_fixture").await;
    let failure = &reference["receipt_failure"];
    assert_eq!(json!(status.as_u16()), failure["status_on_failure"]);
    assert_eq!(
        sqlx::query_scalar::<_, String>(
            "SELECT status FROM subscription_orders WHERE trade_no='sub_failure'"
        )
        .fetch_one(&harness.fixture.pg)
        .await?,
        failure["order_status_on_failure"]
    );
    let count: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM user_subscriptions")
        .fetch_one(&harness.fixture.pg)
        .await?;
    assert_eq!(json!(count - 1), failure["new_entitlements_on_failure"]);
    let receipt_sql = "SELECT COUNT(*) FROM subscription_payment_events WHERE subscription_order_id=(SELECT id FROM subscription_orders WHERE trade_no='sub_failure')";
    assert_eq!(
        json!(
            sqlx::query_scalar::<_, i64>(receipt_sql)
                .fetch_one(&harness.fixture.pg)
                .await?
        ),
        failure["receipt_count_on_failure"]
    );
    sqlx::query("UPDATE user_subscriptions SET amount_used=31 WHERE id=(SELECT user_subscription_id FROM subscription_orders WHERE trade_no='sub_failure')").execute(&harness.fixture.pg).await?;
    sqlx::query("ALTER TABLE subscription_payment_events DROP CONSTRAINT fail_receipt")
        .execute(&harness.fixture.pg)
        .await?;
    assert_eq!(
        json!(harness.notify(event, "whsec_fixture").await.as_u16()),
        failure["status_on_retry"]
    );
    let count: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM user_subscriptions")
        .fetch_one(&harness.fixture.pg)
        .await?;
    assert_eq!(json!(count - 1), failure["new_entitlements_after_retry"]);
    assert_eq!(
        json!(
            sqlx::query_scalar::<_, i64>(receipt_sql)
                .fetch_one(&harness.fixture.pg)
                .await?
        ),
        failure["receipt_count_after_retry"]
    );
    assert_eq!(json!(sqlx::query_scalar::<_,i64>("SELECT amount_used FROM user_subscriptions WHERE id=(SELECT user_subscription_id FROM subscription_orders WHERE trade_no='sub_failure')").fetch_one(&harness.fixture.pg).await?),failure["used_quota_after_retry"]);
    harness.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey"]
async fn stripe_partial_and_full_refunds_replay_safely_and_claw_back_only_the_first_reward()
-> TestResult {
    let harness = StripeHarness::new().await;
    assert_eq!(
        harness
            .pay(json!({"amount":14.6,"payment_method":"stripe"}))
            .await["message"],
        "success"
    );
    let trade = harness.trade().await;
    assert_eq!(
        harness
            .notify(
                paid(&trade, "evt_refundable", "pi_refundable", 100, 100),
                "whsec_fixture"
            )
            .await,
        StatusCode::OK
    );
    for _ in 0..2 {
        assert_eq!(
            harness
                .notify(refund("re_quarter", "pi_refundable", 25), "whsec_fixture")
                .await,
            StatusCode::OK
        );
    }
    assert_eq!(harness.fixture.quota(7).await, 5_475_000);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT aff_quota FROM users WHERE id=1")
            .fetch_one(&harness.fixture.pg)
            .await?,
        75
    );
    assert_eq!(
        harness
            .notify(refund("re_rest", "pi_refundable", 75), "whsec_fixture")
            .await,
        StatusCode::OK
    );
    assert_eq!(harness.fixture.quota(7).await, 0);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT aff_quota FROM users WHERE id=1")
            .fetch_one(&harness.fixture.pg)
            .await?,
        0
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT status FROM referral_rewards")
            .fetch_one(&harness.fixture.pg)
            .await?,
        "revoked"
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM finance_ledger_entries")
            .fetch_one(&harness.fixture.pg)
            .await?,
        2
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM logs WHERE type=6")
            .fetch_one(&harness.fixture.pg)
            .await?,
        2
    );
    let before: i64 = sqlx::query_scalar("SELECT referral_first_top_up_id FROM users WHERE id=7")
        .fetch_one(&harness.fixture.pg)
        .await?;
    assert!(
        before > 0,
        "a refund never reopens first-top-up eligibility"
    );
    harness.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey"]
async fn stripe_refund_insufficient_wallet_and_ledger_failure_are_atomic_retryable_failures()
-> TestResult {
    let harness = StripeHarness::new().await;
    assert_eq!(
        harness
            .pay(json!({"amount":14.6,"payment_method":"stripe"}))
            .await["message"],
        "success"
    );
    let trade = harness.trade().await;
    assert_eq!(
        harness
            .notify(
                paid(&trade, "evt_refundable", "pi_refundable", 100, 100),
                "whsec_fixture"
            )
            .await,
        StatusCode::OK
    );
    sqlx::query("UPDATE users SET quota=1 WHERE id=7")
        .execute(&harness.fixture.pg)
        .await?;
    assert_eq!(
        harness
            .notify(refund("re_full", "pi_refundable", 100), "whsec_fixture")
            .await,
        StatusCode::INTERNAL_SERVER_ERROR
    );
    assert_eq!(harness.fixture.quota(7).await, 1);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM finance_ledger_entries")
            .fetch_one(&harness.fixture.pg)
            .await?,
        0
    );
    sqlx::query("UPDATE users SET quota=7300000 WHERE id=7")
        .execute(&harness.fixture.pg)
        .await?;
    sqlx::query("ALTER TABLE finance_ledger_entries ADD CONSTRAINT fail_refund_ledger CHECK(amount_micros<0)").execute(&harness.fixture.pg).await?;
    assert_eq!(
        harness
            .notify(refund("re_full", "pi_refundable", 100), "whsec_fixture")
            .await,
        StatusCode::INTERNAL_SERVER_ERROR
    );
    assert_eq!(harness.fixture.quota(7).await, 7_300_000);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT aff_quota FROM users WHERE id=1")
            .fetch_one(&harness.fixture.pg)
            .await?,
        75
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT refunded_amount_micros FROM top_ups")
            .fetch_one(&harness.fixture.pg)
            .await?,
        0
    );
    sqlx::query("ALTER TABLE finance_ledger_entries DROP CONSTRAINT fail_refund_ledger")
        .execute(&harness.fixture.pg)
        .await?;
    assert_eq!(
        harness
            .notify(refund("re_full", "pi_refundable", 100), "whsec_fixture")
            .await,
        StatusCode::OK
    );
    assert_eq!(harness.fixture.quota(7).await, 0);
    harness.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey; current Go reference can be regenerated by stripe-current-differential.py"]
async fn stripe_current_go_checkout_settlement_and_refund_reference_matches() -> TestResult {
    let reference: Value = if let Ok(path) = std::env::var("LMM_STRIPE_GO_ORACLE_OUTPUT") {
        serde_json::from_str(&std::fs::read_to_string(path)?)?
    } else {
        serde_json::from_str(include_str!("../fixtures/stripe-current-go-output.json"))?
    };
    let harness = StripeHarness::new().await;
    for (key, value) in reference["currency_options"]
        .as_object()
        .expect("actual Go currency options")
    {
        harness
            .fixture
            .option(key, value.as_str().expect("currency option string"))
            .await;
    }
    harness
        .fixture
        .option("StripePromotionCodesEnabled", "true")
        .await;
    // This exact half-credit input distinguishes flooring from the legacy
    // round-to-nearest implementation without changing the frozen payment.
    let quote = Request::post("/api/user/stripe/amount")
        .header("content-type", "application/json")
        .body(Body::from(r#"{"amount":14.600001}"#))?;
    let quote: Value = serde_json::from_str(&http_body(harness.app.clone(), quote).await)?;
    assert_eq!(quote, reference["quote_response"]);
    let mut response = harness.pay(reference["request"].clone()).await;
    let trade = harness.trade().await;
    let mut expected = reference["response"].clone();
    let go_trade = expected["data"]["trade_no"].as_str().unwrap();
    assert!(go_trade == "<order>" || (go_trade.starts_with("ref_") && go_trade.len() == 44));
    assert_eq!(response["data"]["trade_no"], trade);
    response["data"]["trade_no"] = json!("<order>");
    expected["data"]["trade_no"] = json!("<order>");
    assert_eq!(response, expected);
    let (mut fields, persisted) = harness.provider.sessions.lock().unwrap()[0].clone();
    fields.insert("client_reference_id".into(), "<order>".into());
    assert_eq!(json!(fields), reference["checkout_fields"]);
    assert_eq!(json!(persisted), reference["persisted_before_checkout"]);
    let paid = paid(&trade, "evt_paid", "pi_fixture", 100, 80);
    let statuses = vec![
        harness.notify(paid.clone(), "wrong-secret").await.as_u16(),
        harness.notify(paid.clone(), "whsec_fixture").await.as_u16(),
        harness.notify(paid, "whsec_fixture").await.as_u16(),
    ];
    assert_eq!(json!(statuses), reference["callback_statuses"]);
    assert_eq!(
        json!(harness.fixture.quota(7).await),
        reference["wallet_after_payment"]
    );
    let mut refunds = vec![];
    for _ in 0..2 {
        refunds.push(
            harness
                .notify(refund("re_partial", "pi_fixture", 25), "whsec_fixture")
                .await
                .as_u16(),
        );
    }
    assert_eq!(
        json!(harness.fixture.quota(7).await),
        reference["partial_wallet"]
    );
    refunds.push(
        harness
            .notify(refund("re_final", "pi_fixture", 55), "whsec_fixture")
            .await
            .as_u16(),
    );
    assert_eq!(json!(refunds), reference["refund_statuses"]);
    assert_eq!(
        json!(harness.fixture.quota(7).await),
        reference["final_wallet"]
    );
    let row=sqlx::query("SELECT amount,platform_amount_micros,credited_quota,expected_amount_micros,settled_amount_micros,settlement_currency,refunded_amount_micros,refunded_quota FROM top_ups").fetch_one(&harness.fixture.pg).await?;
    for field in [
        "amount",
        "platform_amount_micros",
        "credited_quota",
        "expected_amount_micros",
        "settled_amount_micros",
        "refunded_amount_micros",
        "refunded_quota",
    ] {
        assert_eq!(
            json!(row.get::<i64, _>(field)),
            reference[field],
            "Go/Rust order {field}"
        );
    }
    assert_eq!(
        row.get::<String, _>("settlement_currency"),
        reference["currency"]
    );
    for (name, sql) in [
        (
            "inviter_aff_quota",
            "SELECT aff_quota FROM users WHERE id=1",
        ),
        (
            "refund_ledger_count",
            "SELECT COUNT(*) FROM finance_ledger_entries",
        ),
        ("topup_logs", "SELECT COUNT(*) FROM logs WHERE type=1"),
        ("refund_logs", "SELECT COUNT(*) FROM logs WHERE type=6"),
    ] {
        assert_eq!(
            json!(
                sqlx::query_scalar::<_, i64>(sql)
                    .fetch_one(&harness.fixture.pg)
                    .await?
            ),
            reference[name],
            "Go/Rust {name}"
        );
    }
    for field in ["email", "stripe_customer"] {
        let actual: String = sqlx::query_scalar(&format!("SELECT {field} FROM users WHERE id=7"))
            .fetch_one(&harness.fixture.pg)
            .await?;
        assert_eq!(
            actual,
            reference[if field == "email" {
                "payer_email"
            } else {
                field
            }]
        );
    }
    let verified = lmm_api_rs::routes::stripe_creem::stripe_provider::verify_stripe_event(
        reference["signature_payload"].as_str().unwrap().as_bytes(),
        reference["signature_header"].as_str().unwrap(),
        &secrecy::SecretString::from("whsec_fixture"),
        UNIX_EPOCH + Duration::from_secs(1_700_000_000),
    )
    .unwrap();
    assert_eq!(verified["id"], "evt_fixed");
    harness.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey"]
async fn stripe_checkout_is_durable_before_provider_and_signed_promoted_payment_credits_once()
-> TestResult {
    let harness = StripeHarness::new().await;
    harness.fixture.coupon("old-expired-reservation").await;
    let response = harness
        .pay(json!({"amount":14.6,"payment_method":"stripe","discount_code":"TEN"}))
        .await;
    assert_eq!(response["message"], "success", "{response}");
    let trade = harness.trade().await;
    let (fields, persisted) = harness.provider.sessions.lock().unwrap()[0].clone();
    assert!(persisted);
    assert_eq!(fields["line_items[0][price_data][unit_amount]"], "90");
    assert_eq!(fields["line_items[0][quantity]"], "1");
    assert_eq!(
        harness
            .notify(paid(&trade, "evt_paid", "pi_paid", 90, 80), "bad-secret")
            .await,
        StatusCode::BAD_REQUEST
    );
    assert_eq!(
        harness
            .notify(paid(&trade, "evt_paid", "pi_paid", 89, 80), "whsec_fixture")
            .await,
        StatusCode::OK
    );
    assert_eq!(harness.fixture.quota(7).await, 0);
    harness.fixture.option("StripePriceId", "").await;
    for _ in 0..2 {
        assert_eq!(
            harness
                .notify(paid(&trade, "evt_paid", "pi_paid", 90, 80), "whsec_fixture")
                .await,
            StatusCode::OK
        );
    }
    assert_eq!(harness.fixture.quota(7).await, 7_300_000);
    let row=sqlx::query("SELECT expected_amount_micros,settled_amount_micros,provider_event_id,provider_transaction_id FROM top_ups").fetch_one(&harness.fixture.pg).await?;
    assert_eq!(row.get::<i64, _>("expected_amount_micros"), 900_000);
    assert_eq!(row.get::<i64, _>("settled_amount_micros"), 800_000);
    assert_eq!(row.get::<String, _>("provider_event_id"), "evt_paid");
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT used_count FROM discount_codes WHERE id=9")
            .fetch_one(&harness.fixture.pg)
            .await?,
        1
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT aff_quota FROM users WHERE id=1")
            .fetch_one(&harness.fixture.pg)
            .await?,
        75
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT email FROM users WHERE id=7")
            .fetch_one(&harness.fixture.pg)
            .await?,
        "payer@example.com"
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT stripe_customer FROM users WHERE id=7")
            .fetch_one(&harness.fixture.pg)
            .await?,
        "cus_fixture"
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM logs WHERE type=1")
            .fetch_one(&harness.fixture.pg)
            .await?,
        2,
        "Go logs both compatible deliveries, but never credits twice"
    );
    harness.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey"]
async fn stripe_provider_failure_retains_order_and_callback_storage_failure_retries_atomically()
-> TestResult {
    let harness = StripeHarness::new().await;
    harness.fixture.coupon("old-expired-reservation").await;
    harness.provider.mode.store(1, Ordering::SeqCst);
    let response = harness
        .pay(json!({"amount":14.6,"payment_method":"stripe","discount_code":"TEN"}))
        .await;
    assert_eq!(response, json!({"message":"error","data":"拉起支付失败"}));
    let trade = harness.trade().await;
    assert!(harness.provider.sessions.lock().unwrap()[0].1);
    sqlx::query("ALTER TABLE referral_ledger_entries ADD CONSTRAINT fail_referral CHECK(quota<0)")
        .execute(&harness.fixture.pg)
        .await?;
    let event = paid(&trade, "evt_retry", "pi_retry", 90, 90);
    assert_eq!(
        harness.notify(event.clone(), "whsec_fixture").await,
        StatusCode::INTERNAL_SERVER_ERROR
    );
    assert_eq!(harness.fixture.quota(7).await, 0);
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT status FROM top_ups")
            .fetch_one(&harness.fixture.pg)
            .await?,
        "pending"
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT used_count FROM discount_codes WHERE id=9")
            .fetch_one(&harness.fixture.pg)
            .await?,
        0
    );
    sqlx::query("ALTER TABLE referral_ledger_entries DROP CONSTRAINT fail_referral")
        .execute(&harness.fixture.pg)
        .await?;
    assert_eq!(harness.notify(event, "whsec_fixture").await, StatusCode::OK);
    assert_eq!(harness.fixture.quota(7).await, 7_300_000);
    harness.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey"]
async fn stripe_reused_payment_intent_and_wrong_currency_cannot_credit_another_order() -> TestResult
{
    let harness = StripeHarness::new().await;
    assert_eq!(
        harness
            .pay(json!({"amount":14.6,"payment_method":"stripe"}))
            .await["message"],
        "success"
    );
    let first = harness.trade().await;
    assert_eq!(
        harness
            .notify(
                paid(&first, "evt_first", "pi_unique", 100, 100),
                "whsec_fixture"
            )
            .await,
        StatusCode::OK
    );
    assert_eq!(
        harness
            .pay(json!({"amount":14.6,"payment_method":"stripe"}))
            .await["message"],
        "success"
    );
    let second = harness.trade().await;
    assert_eq!(
        harness
            .notify(
                paid(&second, "evt_second", "pi_unique", 100, 100),
                "whsec_fixture"
            )
            .await,
        StatusCode::OK
    );
    let mut wrong = paid(&second, "evt_second", "pi_second", 100, 100);
    wrong["data"]["object"]["currency"] = json!("eur");
    assert_eq!(harness.notify(wrong, "whsec_fixture").await, StatusCode::OK);
    assert_eq!(harness.fixture.quota(7).await, 7_300_000);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM top_ups WHERE status='pending'")
            .fetch_one(&harness.fixture.pg)
            .await?,
        1
    );
    assert_eq!(
        harness
            .notify(
                paid(&second, "evt_second", "pi_second", 100, 100),
                "whsec_fixture"
            )
            .await,
        StatusCode::OK
    );
    assert_eq!(harness.fixture.quota(7).await, 14_600_000);
    harness.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey"]
async fn stripe_expiration_releases_coupon_and_subscription_dispatch_precedes_wallet_validation()
-> TestResult {
    let harness = StripeHarness::new().await;
    sqlx::query("INSERT INTO subscription_orders(trade_no,payment_provider,status,expected_amount_micros,settlement_currency,provider_product_id) VALUES('subscription-fixture','stripe','pending',1000000,'USD','price_plan')").execute(&harness.fixture.pg).await?;
    assert_eq!(harness.notify(json!({"id":"evt_subscription","type":"checkout.session.completed","data":{"object":{"client_reference_id":"subscription-fixture","mode":"subscription","status":"complete","payment_status":"paid","amount_total":100,"currency":"usd","subscription":"sub_fixture","metadata":{"subscription_price_id":"price_plan"}}}}),"whsec_fixture").await,StatusCode::OK,
        "subscription dispatch must precede wallet-only subtotal validation");
    harness.fixture.coupon("old-expired-reservation").await;
    assert_eq!(
        harness
            .pay(json!({"amount":14.6,"payment_method":"stripe","discount_code":"TEN"}))
            .await["message"],
        "success"
    );
    let trade = harness.trade().await;
    assert_eq!(harness.notify(json!({"id":"evt_expired","type":"checkout.session.expired","data":{"object":{"status":"expired","client_reference_id":trade}}}),"whsec_fixture").await,StatusCode::OK);
    assert_eq!(
        sqlx::query_scalar::<_, String>(
            "SELECT status FROM discount_code_reservations WHERE top_up_trade_no=$1"
        )
        .bind(&trade)
        .fetch_one(&harness.fixture.pg)
        .await?,
        "released"
    );
    for kind in ["invoice.paid", "customer.subscription.updated"] {
        assert_eq!(harness.notify(json!({"id":"evt_unsupported","type":kind,"data":{"object":{"id":"object_fixture"}}}),"whsec_fixture").await,StatusCode::OK);
    }
    assert_eq!(harness.fixture.quota(7).await, 0);
    harness.clean().await;
    Ok(())
}
