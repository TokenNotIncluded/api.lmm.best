use super::*;
use lmm_api_rs::routes::billing_payments::{
    BillingAuthorizer, BillingConfig, BillingDependencies, BillingError, BillingHttpState,
    DisabledCheckoutProvider, DisabledEpayVerifier, DisabledStripeWebhookVerifier,
    PgBillingRepository, PgPaymentCompliance, ValkeyBillingCache,
    billing_provider_payments_without_stripe_router,
};
use std::sync::atomic::AtomicUsize;

#[async_trait]
impl BillingAuthorizer for Auth {
    async fn user_id(&self, _: &HeaderMap) -> Result<i64, BillingError> {
        Ok(7)
    }
}

#[derive(Clone, Default)]
struct Limit {
    calls: Arc<AtomicUsize>,
    rejected: Arc<AtomicU8>,
}
#[async_trait]
impl TopupAuthorizer for Limit {
    async fn user_id(&self, _: &HeaderMap) -> Result<i64, TopupError> {
        Ok(7)
    }
    async fn check_critical_rate_limit(
        &self,
        _: &str,
    ) -> Result<CriticalRateLimitOutcome, TopupError> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        Ok(if self.rejected.load(Ordering::SeqCst) == 1 {
            CriticalRateLimitOutcome::Rejected {
                retry_after_seconds: 15,
            }
        } else {
            CriticalRateLimitOutcome::Allowed
        })
    }
}

type RecurringSessionCall = (BTreeMap<String, String>, bool, String);

#[derive(Clone)]
struct RecurringProvider {
    pg: PgPool,
    input: Value,
    price_calls: Arc<AtomicUsize>,
    calls: Arc<Mutex<Vec<RecurringSessionCall>>>,
}
async fn recurring_price(
    State(provider): State<RecurringProvider>,
    headers: HeaderMap,
) -> axum::response::Response {
    assert_eq!(
        headers["authorization"],
        "Bearer sk_subscription_checkout_fixture"
    );
    provider.price_calls.fetch_add(1, Ordering::SeqCst);
    if provider.input["price_mode"] == "read_failure" {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(json!({"error":{"message":"fixture price unavailable"}})),
        )
            .into_response();
    }
    let minor = if provider.input["plan_currency"] == "CNY" {
        15
    } else {
        100
    };
    let mut price = json!({"id":"price_plan","active":true,"currency":"usd","unit_amount":minor,
        "recurring":{"interval":"month"},"product":"prod_plan"});
    match provider.input["price_mode"].as_str().unwrap_or("") {
        "inactive" => price["active"] = json!(false),
        "one_time" => {
            price.as_object_mut().unwrap().remove("recurring");
        }
        "wrong_id" => price["id"] = json!("price_other"),
        "wrong_currency" => price["currency"] = json!("cny"),
        "wrong_amount" => price["unit_amount"] = json!(minor + 1),
        _ => {}
    }
    Json(price).into_response()
}
async fn recurring_session(
    State(provider): State<RecurringProvider>,
    headers: HeaderMap,
    body: String,
) -> axum::response::Response {
    assert_eq!(headers["stripe-version"], "2025-02-24.acacia");
    let fields: BTreeMap<String, String> = form_urlencoded::parse(body.as_bytes())
        .into_owned()
        .collect();
    let trade = &fields["client_reference_id"];
    let committed: bool = sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM subscription_orders WHERE trade_no=$1 AND status='pending' AND expected_amount_micros>0 AND plan_snapshot<>'')")
        .bind(trade).fetch_one(&provider.pg).await.unwrap();
    provider.calls.lock().unwrap().push((
        fields,
        committed,
        headers
            .get("idempotency-key")
            .unwrap()
            .to_str()
            .unwrap()
            .to_owned(),
    ));
    if provider.input["fail_checkout"].as_bool().unwrap_or(false) {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(json!({"error":{"message":"fixture checkout unavailable"}})),
        )
            .into_response();
    }
    Json(json!({"id":"cs_subscription","url":"https://checkout.stripe.test/cs_subscription"}))
        .into_response()
}

struct CheckoutHarness {
    base: StripeHarness,
    app: Router,
    provider: RecurringProvider,
    server: tokio::task::JoinHandle<()>,
}
impl CheckoutHarness {
    async fn new(input: &Value, limit: Limit) -> Self {
        let base = StripeHarness::new().await;
        let fixture = &base.fixture;
        sqlx::raw_sql("ALTER TABLE subscription_plans ADD COLUMN subtitle TEXT DEFAULT '', ADD COLUMN price_currency_version BIGINT DEFAULT 1, ADD COLUMN enabled BOOLEAN DEFAULT TRUE, ADD COLUMN archived_at BIGINT DEFAULT 0, ADD COLUMN sort_order BIGINT DEFAULT 0, ADD COLUMN allow_balance_pay BOOLEAN DEFAULT TRUE, ADD COLUMN stripe_price_id TEXT DEFAULT 'price_plan', ADD COLUMN creem_product_id TEXT DEFAULT '', ADD COLUMN waffo_pancake_product_id TEXT DEFAULT '', ADD COLUMN waffo_pancake_product_type TEXT DEFAULT 'subscription', ADD COLUMN created_at BIGINT DEFAULT 0, ADD COLUMN updated_at BIGINT DEFAULT 0;")
            .execute(&fixture.pg).await.unwrap();
        sqlx::query("UPDATE subscription_plans SET title='Stripe recurring checkout',enabled=$1,currency=$2,max_purchase_per_user=$3 WHERE id=3")
            .bind(!input["disabled"].as_bool().unwrap_or(false))
            .bind(input["plan_currency"].as_str().unwrap_or("USD"))
            .bind(i64::from(input["purchased"].as_bool().unwrap_or(false)))
            .execute(&fixture.pg).await.unwrap();
        if input["purchased"].as_bool().unwrap_or(false) {
            sqlx::query(
                "INSERT INTO user_subscriptions(user_id,plan_id,status) VALUES(7,3,'expired')",
            )
            .execute(&fixture.pg)
            .await
            .unwrap();
        }
        sqlx::query(
            "UPDATE users SET email=$1,stripe_customer=$2,payment_restriction_flags=0 WHERE id=7",
        )
        .bind(if input["restricted"].as_bool().unwrap_or(false) {
            "payer@linux.do"
        } else {
            "payer@example.test"
        })
        .bind(input["customer"].as_str().unwrap_or(""))
        .execute(&fixture.pg)
        .await
        .unwrap();
        for (key, value) in [
            ("StripeApiSecret", "sk_subscription_checkout_fixture"),
            ("StripePriceId", ""),
            ("USDExchangeRate", "6.8"),
            ("TopUpPlatformUnitsPerCNY", "17"),
            ("ServerAddress", "https://console.example/"),
            (
                "StripeWebhookSecret",
                if input["no_webhook"].as_bool().unwrap_or(false) {
                    ""
                } else {
                    "whsec_fixture"
                },
            ),
            (
                "PayMethods",
                if input["explicit_audience"].as_bool().unwrap_or(false) {
                    r#"[{"type":"stripe","audience_mode":"all"}]"#
                } else {
                    "[]"
                },
            ),
        ] {
            fixture.option(key, value).await;
        }
        if input["fail_order"].as_bool().unwrap_or(false) {
            sqlx::query("ALTER TABLE subscription_orders ADD CONSTRAINT reject_pending_order CHECK(status<>'pending')").execute(&fixture.pg).await.unwrap();
        }
        let provider = RecurringProvider {
            pg: fixture.pg.clone(),
            input: input.clone(),
            price_calls: Arc::new(AtomicUsize::new(0)),
            calls: Arc::new(Mutex::new(vec![])),
        };
        let service = Router::new()
            .route("/v1/prices/price_plan", get(recurring_price))
            .route("/v1/checkout/sessions", post(recurring_session))
            .with_state(provider.clone());
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let endpoint = format!("http://{}/", listener.local_addr().unwrap());
        let server = tokio::spawn(async move {
            axum::serve(listener, service).await.unwrap();
        });
        let valkey =
            redis::Client::open(std::env::var("LMM_EPAY_TEST_VALKEY_URL").unwrap()).unwrap();
        let native = StripeWalletState::new(
            fixture.pg.clone(),
            valkey.clone(),
            Arc::new(limit),
            StripeApiClient::loopback(&endpoint).unwrap(),
        );
        let state = BillingHttpState::new(
            BillingDependencies {
                repository: Arc::new(PgBillingRepository::new(fixture.pg.clone())),
                authorizer: Arc::new(Auth),
                checkout: Arc::new(DisabledCheckoutProvider),
                epay: Arc::new(DisabledEpayVerifier),
                stripe: Arc::new(DisabledStripeWebhookVerifier),
                cache: Arc::new(ValkeyBillingCache::new(valkey)),
                compliance: Arc::new(PgPaymentCompliance::new(fixture.pg.clone())),
            },
            BillingConfig::default(),
        )
        .with_native_stripe(native.clone());
        let app =
            billing_provider_payments_without_stripe_router(state).merge(webhook_router(native));
        Self {
            base,
            app,
            provider,
            server,
        }
    }
    fn request(body: &str) -> Request<Body> {
        let mut request = Request::post("/api/subscription/stripe/pay")
            .header("content-type", "application/json")
            .body(Body::from(body.to_owned()))
            .unwrap();
        request
            .extensions_mut()
            .insert(ClientIpKey("127.0.0.1".into()));
        request
    }
    async fn clean(self) {
        self.server.abort();
        self.base.clean().await;
    }
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL/Valkey; compares 21 live Go subscription checkout vectors"]
async fn stripe_current_go_subscription_checkout_reference_matches() -> TestResult {
    let reference: Value =
        if let Ok(path) = std::env::var("LMM_STRIPE_SUBSCRIPTION_CHECKOUT_GO_ORACLE_OUTPUT") {
            serde_json::from_str(&std::fs::read_to_string(path)?)?
        } else {
            serde_json::from_str(include_str!(
                "../fixtures/stripe-subscription-checkout-current-go-output.json"
            ))?
        };
    let input: Value = serde_json::from_str(include_str!(
        "../fixtures/stripe-subscription-checkout-current-go-input.json"
    ))?;
    assert_eq!(
        reference.as_array().unwrap().len(),
        input.as_array().unwrap().len()
    );
    for (expected, input) in reference
        .as_array()
        .unwrap()
        .iter()
        .zip(input.as_array().unwrap())
    {
        assert_eq!(expected["input"], *input);
        let harness = CheckoutHarness::new(input, Limit::default()).await;
        let response = harness
            .app
            .clone()
            .oneshot(CheckoutHarness::request(
                input["body"].as_str().unwrap_or(r#"{"plan_id":3}"#),
            ))
            .await?;
        assert_eq!(
            response.status().as_u16(),
            expected["http_status"].as_u64().unwrap() as u16,
            "{}",
            input["name"]
        );
        let body: Value =
            serde_json::from_slice(&axum::body::to_bytes(response.into_body(), 65536).await?)?;
        assert_eq!(body, expected["response"], "{}", input["name"]);
        let rows = sqlx::query("SELECT status,money::FLOAT8 AS money,payment_method,payment_provider,expected_amount_micros,settlement_currency,provider_product_id,plan_snapshot FROM subscription_orders ORDER BY id").fetch_all(&harness.base.fixture.pg).await?;
        let mut orders = vec![];
        for row in rows {
            let mut plan: Value = serde_json::from_str(row.try_get::<&str, _>("plan_snapshot")?)?;
            plan.as_object_mut().unwrap().remove("created_at");
            plan.as_object_mut().unwrap().remove("updated_at");
            orders.push(json!({"status":row.try_get::<String,_>("status")?,
                "money":serde_json::from_str::<Value>(&row.try_get::<f64,_>("money")?.to_string())?,
                "payment_method":row.try_get::<String,_>("payment_method")?,"payment_provider":row.try_get::<String,_>("payment_provider")?,
                "expected_amount_micros":row.try_get::<i64,_>("expected_amount_micros")?,"settlement_currency":row.try_get::<String,_>("settlement_currency")?,
                "provider_product_id":row.try_get::<String,_>("provider_product_id")?,"plan_snapshot":plan}));
        }
        assert_eq!(json!(orders), expected["orders"], "{}", input["name"]);
        let calls = harness.provider.calls.lock().unwrap().clone();
        assert_eq!(
            calls.len() as u64,
            expected["session_calls"].as_u64().unwrap(),
            "{}",
            input["name"]
        );
        assert_eq!(
            harness.provider.price_calls.load(Ordering::SeqCst) as u64,
            expected["price_calls"].as_u64().unwrap(),
            "{}",
            input["name"]
        );
        assert_eq!(
            json!(calls.iter().all(|(_, committed, _)| *committed)),
            expected["persisted_before_checkout"]
        );
        let keys_stable = calls.first().is_none_or(|(_, _, key)| {
            !key.is_empty() && calls.iter().all(|(_, _, other)| key == other)
        });
        assert_eq!(json!(keys_stable), expected["idempotency_key_stable"]);
        let mut fields = calls
            .last()
            .map(|(fields, _, _)| fields.clone())
            .unwrap_or_default();
        for key in [
            "client_reference_id",
            "metadata[subscription_trade_no]",
            "subscription_data[metadata][subscription_trade_no]",
        ] {
            if let Some(value) = fields.get_mut(key) {
                *value = "<order>".into();
            }
        }
        assert_eq!(
            json!(fields),
            expected["checkout_fields"],
            "{}",
            input["name"]
        );
        assert!(
            calls.windows(2).all(|pair| pair[0].0 == pair[1].0),
            "retry payload changed"
        );
        harness.clean().await;
    }
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey"]
async fn stripe_subscription_checkout_gates_then_completes_persisted_plan_once() -> TestResult {
    let limit = Limit::default();
    limit.rejected.store(1, Ordering::SeqCst);
    let harness = CheckoutHarness::new(&json!({}), limit.clone()).await;
    let response = harness
        .app
        .clone()
        .oneshot(CheckoutHarness::request("{"))
        .await?;
    assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
    assert_eq!(response.headers()["retry-after"], "15");
    limit.rejected.store(0, Ordering::SeqCst);
    harness
        .base
        .fixture
        .option("payment_setting.compliance_confirmed", "false")
        .await;
    let response = harness
        .app
        .clone()
        .oneshot(CheckoutHarness::request("{"))
        .await?;
    let value: Value =
        serde_json::from_slice(&axum::body::to_bytes(response.into_body(), 65536).await?)?;
    assert!(value["message"].as_str().unwrap().contains("compliance"));
    let mut oversized = CheckoutHarness::request("{}");
    oversized
        .headers_mut()
        .insert("content-length", "16385".parse()?);
    assert_eq!(
        harness.app.clone().oneshot(oversized).await?.status(),
        StatusCode::PAYLOAD_TOO_LARGE
    );
    assert_eq!(limit.calls.load(Ordering::SeqCst), 2);
    harness
        .base
        .fixture
        .option("payment_setting.compliance_confirmed", "true")
        .await;
    let value: Value = serde_json::from_str(
        &http_body(
            harness.app.clone(),
            CheckoutHarness::request(r#"{"plan_id":3}"#),
        )
        .await,
    )?;
    assert_eq!(value["message"], "success");
    let trade: String = sqlx::query_scalar("SELECT trade_no FROM subscription_orders")
        .fetch_one(&harness.base.fixture.pg)
        .await?;
    sqlx::query("UPDATE subscription_plans SET total_amount=9000")
        .execute(&harness.base.fixture.pg)
        .await?;
    for _ in 0..2 {
        assert_eq!(
            harness
                .base
                .notify(subscription_checkout(&trade), "whsec_fixture")
                .await,
            StatusCode::OK
        );
    }
    let (count, total): (i64, i64) =
        sqlx::query_as("SELECT COUNT(*),MAX(amount_total) FROM user_subscriptions")
            .fetch_one(&harness.base.fixture.pg)
            .await?;
    assert_eq!((count, total), (1, 1000));
    assert_eq!(harness.base.fixture.quota(7).await, 0);
    assert_eq!(harness.provider.calls.lock().unwrap().len(), 1);
    harness.clean().await;
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey"]
async fn stripe_subscription_renewal_restores_corrected_full_grant_and_explicit_zero() -> TestResult
{
    for (reset, renewal, expected) in [
        (Some(70_i64), Some(100_i64), 100_i64),
        (Some(0), Some(0), 0),
        (Some(70), None, 70),
        (None, None, 1000),
    ] {
        let harness = StripeHarness::new().await;
        harness.subscription_order("corrected-renewal").await;
        assert_eq!(
            harness
                .notify(subscription_checkout("corrected-renewal"), "whsec_fixture")
                .await,
            StatusCode::OK
        );
        assert_eq!(
            harness
                .notify(
                    invoice(
                        "corrected-renewal",
                        "in_initial",
                        4_102_444_800,
                        4_102_448_400
                    ),
                    "whsec_fixture"
                )
                .await,
            StatusCode::OK
        );
        sqlx::query("UPDATE user_subscriptions SET amount_total=550,amount_used=500,reset_amount=$1,renewal_amount=$2")
            .bind(reset).bind(renewal).execute(&harness.fixture.pg).await?;
        let event = invoice(
            "corrected-renewal",
            "in_renewal",
            4_102_448_400,
            4_102_452_000,
        );
        for _ in 0..2 {
            assert_eq!(
                harness.notify(event.clone(), "whsec_fixture").await,
                StatusCode::OK
            );
        }
        let state: (i64,i64,Option<i64>,Option<i64>,i64) = sqlx::query_as(
            "SELECT amount_total,amount_used,reset_amount,renewal_amount,quota_version FROM user_subscriptions")
            .fetch_one(&harness.fixture.pg).await?;
        assert_eq!(state, (expected, 0, renewal.or(reset), renewal, 1));
        harness.clean().await;
    }
    Ok(())
}
