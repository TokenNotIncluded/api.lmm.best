use async_trait::async_trait;
use axum::{
    Json, Router,
    body::{Body, to_bytes},
    http::{Request, StatusCode},
    response::Response,
    routing::post,
};
use hmac::{Hmac, Mac};
use lmm_api_rs::routes::billing_payments::*;
use sha2::Sha256;
use std::{
    collections::BTreeMap,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, AtomicUsize, Ordering},
    },
};
use tower::ServiceExt;

struct AllowUser;
#[async_trait]
impl BillingAuthorizer for AllowUser {
    async fn user_id(&self, _: &axum::http::HeaderMap) -> Result<i64, BillingError> {
        Ok(7)
    }
}

struct DenyUser;

#[async_trait]
impl BillingAuthorizer for DenyUser {
    async fn user_id(&self, _: &axum::http::HeaderMap) -> Result<i64, BillingError> {
        Err(BillingError::Unauthorized)
    }
}

struct MemoryRepo {
    completions: Mutex<Vec<(String, String)>>,
    failures: Mutex<Vec<String>>,
}
#[async_trait]
impl BillingRepository for MemoryRepo {
    async fn create_pending(&self, input: CreateOrder) -> Result<PendingOrder, BillingError> {
        Ok(PendingOrder {
            trade_no: "trade-1".into(),
            plan_id: input.plan_id,
            user_id: input.user_id,
            money: "1.00".into(),
            currency: "USD".into(),
            payment_method: input.payment_method,
            provider: input.provider.into(),
        })
    }
    async fn expire(&self, _: &str) -> Result<(), BillingError> {
        Ok(())
    }
    async fn fail(&self, trade_no: &str) -> Result<(), BillingError> {
        self.failures
            .lock()
            .expect("failure lock")
            .push(trade_no.into());
        Ok(())
    }
    async fn purchase_with_balance(
        &self,
        _: i64,
        _: i64,
        _: i64,
    ) -> Result<Completion, BillingError> {
        Ok(Completion::Completed {
            subscription_id: 99,
            user_id: 7,
            quota_charged: 0,
            group_changed: false,
        })
    }
    async fn complete(
        &self,
        trade_no: &str,
        provider: &str,
        _: &str,
        _: Option<&str>,
    ) -> Result<Completion, BillingError> {
        self.completions
            .lock()
            .expect("completion lock")
            .push((trade_no.into(), provider.into()));
        Ok(Completion::AlreadySucceeded)
    }
}
struct AcceptEpay;
#[async_trait]
impl EpayVerifier for AcceptEpay {
    async fn verify(&self, fields: &BTreeMap<String, String>) -> Result<EpayResult, BillingError> {
        Ok(EpayResult {
            verified: true,
            trade_success: true,
            trade_no: fields.get("trade_no").cloned().unwrap_or_default(),
            payment_method: "alipay".into(),
        })
    }
}
struct AcceptStripe;
#[async_trait]
impl StripeWebhookVerifier for AcceptStripe {
    async fn verify(&self, _: &[u8], _: &str) -> Result<StripeEvent, BillingError> {
        Err(BillingError::InvalidSignature)
    }
}

struct TestCheckout;
#[async_trait]
impl CheckoutProvider for TestCheckout {
    async fn start(&self, order: &PendingOrder) -> Result<Checkout, BillingError> {
        Ok(Checkout {
            url: format!(
                "https://checkout.test/{}/{}",
                order.provider, order.trade_no
            ),
            data: serde_json::json!({"trade_no": order.trade_no, "provider": order.provider}),
        })
    }
}

struct TestCache;
#[async_trait]
impl BillingCache for TestCache {
    async fn invalidate_completed_payment(&self, _: i64, _: i64, _: i64, _: bool) {}
}

struct MutableCompliance(AtomicBool);
#[async_trait]
impl PaymentCompliance for MutableCompliance {
    async fn is_confirmed(&self) -> Result<bool, BillingError> {
        Ok(self.0.load(Ordering::SeqCst))
    }
}

struct FailingCheckout;
#[async_trait]
impl CheckoutProvider for FailingCheckout {
    async fn start(&self, _: &PendingOrder) -> Result<Checkout, BillingError> {
        Err(BillingError::Provider)
    }
}

/// Boundary fixture: a frozen provider must be rejected before this repository
/// can write a pending order.
struct OrderCountingRepo(AtomicUsize);

#[async_trait]
impl BillingRepository for OrderCountingRepo {
    async fn create_pending(&self, _: CreateOrder) -> Result<PendingOrder, BillingError> {
        self.0.fetch_add(1, Ordering::SeqCst);
        Err(BillingError::Storage)
    }
    async fn expire(&self, _: &str) -> Result<(), BillingError> {
        Ok(())
    }
    async fn fail(&self, _: &str) -> Result<(), BillingError> {
        Ok(())
    }
    async fn purchase_with_balance(
        &self,
        _: i64,
        _: i64,
        _: i64,
    ) -> Result<Completion, BillingError> {
        Err(BillingError::Storage)
    }
    async fn complete(
        &self,
        _: &str,
        _: &str,
        _: &str,
        _: Option<&str>,
    ) -> Result<Completion, BillingError> {
        Err(BillingError::Storage)
    }
}

fn router(repo: Arc<MemoryRepo>) -> axum::Router {
    router_with_compliance(repo, Arc::new(MutableCompliance(AtomicBool::new(true))))
}

fn router_with_compliance(
    repo: Arc<MemoryRepo>,
    compliance: Arc<dyn PaymentCompliance>,
) -> axum::Router {
    router_with_authorizer(repo, compliance, Arc::new(AllowUser))
}

fn router_with_authorizer(
    repo: Arc<MemoryRepo>,
    compliance: Arc<dyn PaymentCompliance>,
    authorizer: Arc<dyn BillingAuthorizer>,
) -> axum::Router {
    billing_payments_router(billing_state(repo, compliance, authorizer))
}

fn billing_state(
    repo: Arc<MemoryRepo>,
    compliance: Arc<dyn PaymentCompliance>,
    authorizer: Arc<dyn BillingAuthorizer>,
) -> BillingHttpState {
    BillingHttpState::new(
        BillingDependencies {
            repository: repo,
            authorizer,
            checkout: Arc::new(TestCheckout),
            epay: Arc::new(AcceptEpay),
            stripe: Arc::new(AcceptStripe),
            cache: Arc::new(TestCache),
            compliance,
        },
        BillingConfig {
            creem_webhook_secret: Arc::from("creem-test-secret"),
            wallet_url: Arc::from("https://console.example.test/wallet"),
            quota_per_unit: 1,
        },
    )
}

#[tokio::test]
async fn payment_auth_precedes_malformed_json_rejection() {
    let repo = Arc::new(MemoryRepo {
        completions: Mutex::new(Vec::new()),
        failures: Mutex::new(Vec::new()),
    });
    let response = router_with_authorizer(
        repo,
        Arc::new(MutableCompliance(AtomicBool::new(true))),
        Arc::new(DenyUser),
    )
    .oneshot(
        Request::builder()
            .method("POST")
            .uri("/api/subscription/balance/pay")
            .header("content-type", "application/json")
            .body(Body::from("{"))
            .expect("request"),
    )
    .await
    .expect("response");
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(
        serde_json::from_str::<serde_json::Value>(&body(response).await).expect("json response"),
        serde_json::json!({"success": false, "message": "Unauthorized"})
    );
}

async fn body(response: Response) -> String {
    String::from_utf8(
        to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("read response")
            .to_vec(),
    )
    .expect("utf8 response")
}

fn creem_signature(body: &[u8]) -> String {
    let mut mac = Hmac::<Sha256>::new_from_slice(b"creem-test-secret").expect("HMAC key");
    mac.update(body);
    mac.finalize()
        .into_bytes()
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}

#[tokio::test]
async fn frozen_checkout_rejects_before_creating_an_order_or_contacting_a_provider() {
    let repo = Arc::new(OrderCountingRepo(AtomicUsize::new(0)));
    let repository: Arc<dyn BillingRepository> = repo.clone();
    let app = billing_payments_router(BillingHttpState::new(
        BillingDependencies {
            repository,
            authorizer: Arc::new(AllowUser),
            checkout: Arc::new(DisabledCheckoutProvider),
            epay: Arc::new(DisabledEpayVerifier),
            stripe: Arc::new(DisabledStripeWebhookVerifier),
            cache: Arc::new(TestCache),
            compliance: Arc::new(MutableCompliance(AtomicBool::new(true))),
        },
        BillingConfig {
            creem_webhook_secret: Arc::from(""),
            wallet_url: Arc::from("/wallet"),
            quota_per_unit: 1,
        },
    ));
    let response = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/subscription/stripe/pay")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"plan_id":1}"#))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(repo.0.load(Ordering::SeqCst), 0);
    assert!(matches!(
        DisabledEpayVerifier.verify(&BTreeMap::new()).await,
        Err(BillingError::ProviderFrozen)
    ));
    assert!(matches!(
        DisabledStripeWebhookVerifier
            .verify(b"{}", "anything")
            .await,
        Err(BillingError::ProviderFrozen)
    ));
}

#[tokio::test]
async fn compliance_is_evaluated_at_request_time_not_from_a_startup_snapshot() {
    let repo = Arc::new(MemoryRepo {
        completions: Mutex::new(Vec::new()),
        failures: Mutex::new(Vec::new()),
    });
    let compliance = Arc::new(MutableCompliance(AtomicBool::new(true)));
    let app = router_with_compliance(repo, compliance.clone());
    compliance.0.store(false, Ordering::SeqCst);
    let response = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/subscription/balance/pay")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"plan_id":1}"#))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        serde_json::from_str::<serde_json::Value>(&body(response).await).expect("json response"),
        serde_json::json!({"success": false, "message": "payment compliance is required"})
    );
}

#[tokio::test]
async fn epay_return_should_redirect_to_success_only_after_verified_completion() {
    let repo = Arc::new(MemoryRepo {
        completions: Mutex::new(Vec::new()),
        failures: Mutex::new(Vec::new()),
    });
    let response = router(Arc::clone(&repo))
        .oneshot(
            Request::builder()
                .method("GET")
                .uri("/api/subscription/epay/return?trade_no=trade-1")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::FOUND);
    assert_eq!(
        response
            .headers()
            .get("location")
            .and_then(|value| value.to_str().ok()),
        Some("https://console.example.test/wallet?pay=success")
    );
    assert_eq!(
        repo.completions.lock().expect("completion lock").as_slice(),
        [("trade-1".into(), "epay".into())]
    );
}

#[tokio::test]
async fn epay_post_does_not_fall_back_to_query_after_malformed_json() {
    let repo = Arc::new(MemoryRepo {
        completions: Mutex::new(Vec::new()),
        failures: Mutex::new(Vec::new()),
    });
    let response = router(Arc::clone(&repo))
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/subscription/epay/notify?trade_no=must-not-complete")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"trade_no":"unterminated}"#))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(body(response).await, "fail");
    assert!(repo.completions.lock().expect("completion lock").is_empty());
}

#[tokio::test]
async fn creem_webhook_should_require_a_constant_time_hmac_signature() {
    let repo = Arc::new(MemoryRepo {
        completions: Mutex::new(Vec::new()),
        failures: Mutex::new(Vec::new()),
    });
    let response = router(Arc::clone(&repo))
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/creem/webhook")
                .body(Body::from("{}"))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert!(repo.completions.lock().expect("completion lock").is_empty());
}

#[tokio::test]
async fn creem_rejects_signed_malformed_or_unresolvable_paid_events_without_completion() {
    let repo = Arc::new(MemoryRepo {
        completions: Mutex::new(Vec::new()),
        failures: Mutex::new(Vec::new()),
    });
    let app = router(Arc::clone(&repo));
    let malformed = b"{not json";
    let malformed_response = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/creem/webhook")
                .header("creem-signature", creem_signature(malformed))
                .body(Body::from(malformed.to_vec()))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(malformed_response.status(), StatusCode::BAD_REQUEST);
    assert!(body(malformed_response).await.is_empty());

    let missing_request_id =
        br#"{"eventType":"checkout.completed","object":{"order":{"status":"paid"}}}"#;
    let missing_request_id_response = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/creem/webhook")
                .header("creem-signature", creem_signature(missing_request_id))
                .body(Body::from(missing_request_id.to_vec()))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(
        missing_request_id_response.status(),
        StatusCode::BAD_REQUEST
    );
    assert!(body(missing_request_id_response).await.is_empty());
    assert!(repo.completions.lock().expect("completion lock").is_empty());
}

#[tokio::test]
async fn stripe_and_creem_webhooks_fail_closed_with_empty_status_responses() {
    let repo = Arc::new(MemoryRepo {
        completions: Mutex::new(Vec::new()),
        failures: Mutex::new(Vec::new()),
    });
    let app = router(Arc::clone(&repo));
    let missing_stripe_signature = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/stripe/webhook")
                .body(Body::from("{}"))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(missing_stripe_signature.status(), StatusCode::BAD_REQUEST);
    assert!(body(missing_stripe_signature).await.is_empty());

    let invalid_stripe_signature = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/stripe/webhook")
                .header("stripe-signature", "invalid")
                .body(Body::from("{}"))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(invalid_stripe_signature.status(), StatusCode::BAD_REQUEST);
    assert!(body(invalid_stripe_signature).await.is_empty());

    let disabled_payment = router_with_compliance(
        Arc::clone(&repo),
        Arc::new(MutableCompliance(AtomicBool::new(false))),
    )
    .oneshot(
        Request::builder()
            .method("POST")
            .uri("/api/creem/webhook")
            .body(Body::from("{}"))
            .expect("request"),
    )
    .await
    .expect("response");
    assert_eq!(disabled_payment.status(), StatusCode::FORBIDDEN);
    assert!(body(disabled_payment).await.is_empty());
    assert!(repo.completions.lock().expect("completion lock").is_empty());
}

#[tokio::test]
async fn waffo_pancake_checkout_failure_should_mark_the_pending_order_failed() {
    let repo = Arc::new(MemoryRepo {
        completions: Mutex::new(Vec::new()),
        failures: Mutex::new(Vec::new()),
    });
    let app = billing_payments_router(BillingHttpState::new(
        BillingDependencies {
            repository: repo.clone(),
            authorizer: Arc::new(AllowUser),
            checkout: Arc::new(FailingCheckout),
            epay: Arc::new(AcceptEpay),
            stripe: Arc::new(AcceptStripe),
            cache: Arc::new(TestCache),
            compliance: Arc::new(MutableCompliance(AtomicBool::new(true))),
        },
        BillingConfig {
            creem_webhook_secret: Arc::from("creem-test-secret"),
            wallet_url: Arc::from("https://console.example.test/wallet"),
            quota_per_unit: 1,
        },
    ));
    let response = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/subscription/waffo-pancake/pay")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"plan_id":1}"#))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        serde_json::from_str::<serde_json::Value>(&body(response).await).expect("json response"),
        serde_json::json!({"message": "error", "data": "拉起支付失败"})
    );
    assert_eq!(
        repo.failures.lock().expect("failure lock").as_slice(),
        ["trade-1"]
    );
}

#[tokio::test]
async fn balance_payment_should_return_the_legacy_success_envelope() {
    let repo = Arc::new(MemoryRepo {
        completions: Mutex::new(Vec::new()),
        failures: Mutex::new(Vec::new()),
    });
    let response = router(repo)
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/subscription/balance/pay")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"plan_id":1}"#))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        serde_json::from_str::<serde_json::Value>(&body(response).await).expect("json response"),
        serde_json::json!({"success": true, "message": "", "data": null})
    );
}

#[tokio::test]
async fn balance_payment_mount_should_expose_only_the_local_ledger_route() {
    let repo = Arc::new(MemoryRepo {
        completions: Mutex::new(Vec::new()),
        failures: Mutex::new(Vec::new()),
    });
    let app = subscription_balance_pay_router(SubscriptionBalancePayState::new(
        repo,
        Arc::new(AllowUser),
        Arc::new(TestCache),
        Arc::new(MutableCompliance(AtomicBool::new(true))),
        500_000,
    ));

    let method_not_allowed = app
        .clone()
        .oneshot(
            Request::get("/api/subscription/balance/pay")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(method_not_allowed.status(), StatusCode::METHOD_NOT_ALLOWED);

    let unrelated_route = app
        .oneshot(
            Request::post("/api/subscription/stripe/pay")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"plan_id":1}"#))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(unrelated_route.status(), StatusCode::NOT_FOUND);
}

#[tokio::test]
async fn balance_payment_mount_authenticates_before_json_binding() {
    let repo = Arc::new(MemoryRepo {
        completions: Mutex::new(Vec::new()),
        failures: Mutex::new(Vec::new()),
    });
    let response = subscription_balance_pay_router(SubscriptionBalancePayState::new(
        repo,
        Arc::new(DenyUser),
        Arc::new(TestCache),
        Arc::new(MutableCompliance(AtomicBool::new(true))),
        500_000,
    ))
    .oneshot(
        Request::post("/api/subscription/balance/pay")
            .header("content-type", "application/json")
            .body(Body::from("{"))
            .expect("request"),
    )
    .await
    .expect("response");
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(
        serde_json::from_str::<serde_json::Value>(&body(response).await).expect("json response"),
        serde_json::json!({"success": false, "message": "Unauthorized"})
    );
}

#[tokio::test]
async fn stripe_payment_should_return_the_legacy_provider_success_envelope() {
    let repo = Arc::new(MemoryRepo {
        completions: Mutex::new(Vec::new()),
        failures: Mutex::new(Vec::new()),
    });
    let response = router(repo)
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/subscription/stripe/pay")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"plan_id":1}"#))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        serde_json::from_str::<serde_json::Value>(&body(response).await).expect("json response"),
        serde_json::json!({
            "message": "success",
            "data": {"pay_link": "https://checkout.test/stripe/trade-1"}
        })
    );
}

#[tokio::test]
async fn http_checkout_adapter_uses_a_loopback_provider_and_fails_closed() {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("loopback listener");
    let address = listener.local_addr().expect("listener address");
    let app = Router::new().route("/checkout", post(mock_checkout));
    tokio::spawn(async move {
        axum::serve(listener, app)
            .await
            .expect("mock provider server");
    });
    let provider = HttpCheckoutProvider::new(
        reqwest::Client::new(),
        BTreeMap::from([("stripe".to_owned(), format!("http://{address}/checkout"))]),
    )
    .expect("valid local provider endpoint");
    let checkout = provider
        .start(&PendingOrder {
            trade_no: "trade-loopback".into(),
            plan_id: 3,
            user_id: 7,
            money: "1.00".into(),
            currency: "USD".into(),
            payment_method: "stripe".into(),
            provider: "stripe".into(),
        })
        .await
        .expect("mock checkout response");
    assert_eq!(checkout.url, "https://checkout.example.test/trade-loopback");
    assert_eq!(checkout.data["provider"], "stripe");
    assert_eq!(checkout.data["currency"], "USD");
    assert!(matches!(
        provider
            .start(&PendingOrder {
                trade_no: "unconfigured".into(),
                plan_id: 3,
                user_id: 7,
                money: "1.00".into(),
                currency: "USD".into(),
                payment_method: "epay".into(),
                provider: "epay".into(),
            })
            .await,
        Err(BillingError::Provider)
    ));
}

#[tokio::test]
async fn billing_provider_payments_router_mounts_provider_checkout_without_balance_pay() {
    let repo = Arc::new(MemoryRepo {
        completions: Mutex::new(Vec::new()),
        failures: Mutex::new(Vec::new()),
    });
    let app = billing_provider_payments_router(billing_state(
        repo,
        Arc::new(MutableCompliance(AtomicBool::new(true))),
        Arc::new(AllowUser),
    ));

    let stripe = app
        .clone()
        .oneshot(
            Request::post("/api/subscription/stripe/pay")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"plan_id":1}"#))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(stripe.status(), StatusCode::OK);

    let balance = app
        .oneshot(
            Request::post("/api/subscription/balance/pay")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"plan_id":1}"#))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(balance.status(), StatusCode::NOT_FOUND);
}

async fn mock_checkout(Json(payload): Json<serde_json::Value>) -> Json<serde_json::Value> {
    let trade_no = payload["trade_no"].as_str().unwrap_or_default();
    let provider = payload["provider"].as_str().unwrap_or_default();
    let currency = payload["currency"].as_str().unwrap_or_default();
    Json(serde_json::json!({
        "url": format!("https://checkout.example.test/{trade_no}"),
        "data": {"provider": provider, "currency": currency}
    }))
}
