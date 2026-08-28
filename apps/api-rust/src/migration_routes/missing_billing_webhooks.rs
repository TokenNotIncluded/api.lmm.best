//! Legacy-compatible Waffo and Waffo Pancake callback routes.
//!
//! Webhook authentication is deliberately an injected, fail-closed boundary:
//! neither provider verifier performs outbound I/O.  The durable processor is
//! also a boundary so its PostgreSQL implementation can lock an order and
//! commit the order, quota and subscription mutations together.  A successful
//! HTTP acknowledgement is therefore only emitted after that transaction has
//! committed (or an already-settled order was observed).

use std::sync::Arc;

use async_trait::async_trait;
use axum::{
    Router,
    body::Bytes,
    extract::{Extension, Path, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::post,
};
use serde::Deserialize;

use crate::RequestContext;

const PANCAKE_SUBSCRIPTION_PREFIX: &str = "WAFFO_PANCAKE_SUB-";

/// Per-request payment availability gate.  It must be queried for every
/// callback so disabling a gateway takes effect without restarting Rust.
#[async_trait]
pub trait WaffoWebhookAvailability: Send + Sync {
    async fn waffo_enabled(&self) -> Result<bool, WebhookFailure>;
    async fn pancake_enabled(&self) -> Result<bool, WebhookFailure>;
}

/// Parses and authenticates Pancake's signed payload.  Implementations must
/// verify the exact received bytes and reject an absent/malformed signature.
#[async_trait]
pub trait PancakeWebhookVerifier: Send + Sync {
    async fn verify(&self, payload: &[u8], signature: &str)
    -> Result<PancakeEvent, WebhookFailure>;
}

/// Waffo's SDK verifies requests and signs both acknowledgement shapes.  The
/// adapter is intentionally local-only: invoking it must not make network I/O.
#[async_trait]
pub trait WaffoWebhookVerifier: Send + Sync {
    async fn verify(&self, payload: &[u8], signature: &str) -> Result<(), WebhookFailure>;
    async fn signed_response(
        &self,
        success: bool,
        message: &str,
    ) -> Result<SignedWaffoResponse, WebhookFailure>;
}

/// Durable payment boundary.  Every `complete_*` method is required to use a
/// single database transaction with a `FOR UPDATE` order lock; duplicate
/// callbacks must return `AlreadySettled` rather than credit a second time.
///
/// The production implementation is also responsible for provider matching,
/// buyer-identity matching and any post-commit cache invalidation.  Keeping
/// these checks behind this seam avoids an HTTP handler accidentally splitting
/// validation from settlement.
#[async_trait]
pub trait WaffoWebhookProcessor: Send + Sync {
    async fn complete_pancake_top_up(
        &self,
        trade_no: &str,
        buyer_identity: &str,
        raw_payload: &[u8],
    ) -> Result<Settlement, WebhookFailure>;

    async fn complete_pancake_subscription(
        &self,
        trade_no: &str,
        buyer_identity: &str,
        raw_payload: &[u8],
    ) -> Result<Settlement, WebhookFailure>;

    async fn complete_waffo_top_up(
        &self,
        trade_no: &str,
        caller_ip: Option<&str>,
        raw_payload: &[u8],
    ) -> Result<Settlement, WebhookFailure>;

    /// Mirrors Go's `UpdatePendingTopUpStatus`: non-success Waffo payments
    /// are best-effort terminal failures and never cause provider retries.
    async fn mark_waffo_top_up_failed(&self, trade_no: &str) -> Result<(), WebhookFailure>;
}

#[derive(Clone)]
pub struct WaffoWebhookState {
    availability: Arc<dyn WaffoWebhookAvailability>,
    pancake: Arc<dyn PancakeWebhookVerifier>,
    waffo: Arc<dyn WaffoWebhookVerifier>,
    processor: Arc<dyn WaffoWebhookProcessor>,
}

impl WaffoWebhookState {
    #[must_use]
    pub fn new(
        availability: Arc<dyn WaffoWebhookAvailability>,
        pancake: Arc<dyn PancakeWebhookVerifier>,
        waffo: Arc<dyn WaffoWebhookVerifier>,
        processor: Arc<dyn WaffoWebhookProcessor>,
    ) -> Self {
        Self {
            availability,
            pancake,
            waffo,
            processor,
        }
    }
}

/// Reports both Waffo families as disabled without consulting operator state.
pub struct DisabledWaffoWebhookAvailability;

#[async_trait]
impl WaffoWebhookAvailability for DisabledWaffoWebhookAvailability {
    async fn waffo_enabled(&self) -> Result<bool, WebhookFailure> {
        Ok(false)
    }
    async fn pancake_enabled(&self) -> Result<bool, WebhookFailure> {
        Ok(false)
    }
}

/// Rejects every Pancake signature until a live verifier is installed.
pub struct DisabledPancakeWebhookVerifier;

#[async_trait]
impl PancakeWebhookVerifier for DisabledPancakeWebhookVerifier {
    async fn verify(&self, _: &[u8], _: &str) -> Result<PancakeEvent, WebhookFailure> {
        Err(WebhookFailure::Unavailable)
    }
}

/// Rejects every Waffo signature and cannot mint an acknowledgement.
pub struct DisabledWaffoWebhookVerifier;

#[async_trait]
impl WaffoWebhookVerifier for DisabledWaffoWebhookVerifier {
    async fn verify(&self, _: &[u8], _: &str) -> Result<(), WebhookFailure> {
        Err(WebhookFailure::Unavailable)
    }
    async fn signed_response(
        &self,
        _: bool,
        _: &str,
    ) -> Result<SignedWaffoResponse, WebhookFailure> {
        Err(WebhookFailure::Unavailable)
    }
}

/// Refuses settlement so an unconfigured listener cannot credit a wallet.
pub struct DisabledWaffoWebhookProcessor;

#[async_trait]
impl WaffoWebhookProcessor for DisabledWaffoWebhookProcessor {
    async fn complete_pancake_top_up(
        &self,
        _: &str,
        _: &str,
        _: &[u8],
    ) -> Result<Settlement, WebhookFailure> {
        Err(WebhookFailure::Unavailable)
    }
    async fn complete_pancake_subscription(
        &self,
        _: &str,
        _: &str,
        _: &[u8],
    ) -> Result<Settlement, WebhookFailure> {
        Err(WebhookFailure::Unavailable)
    }
    async fn complete_waffo_top_up(
        &self,
        _: &str,
        _: Option<&str>,
        _: &[u8],
    ) -> Result<Settlement, WebhookFailure> {
        Err(WebhookFailure::Unavailable)
    }
    async fn mark_waffo_top_up_failed(&self, _: &str) -> Result<(), WebhookFailure> {
        Err(WebhookFailure::Unavailable)
    }
}

/// Mount point for the two unauthenticated, provider-signed callback routes.
pub fn missing_billing_webhooks_router(state: WaffoWebhookState) -> Router {
    Router::new()
        .route("/api/waffo-pancake/webhook/{env}", post(pancake_webhook))
        .route("/api/waffo/webhook", post(waffo_webhook))
        .with_state(state)
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct PancakeEvent {
    pub id: String,
    pub event_type: String,
    pub mode: String,
    pub order_merchant_external_id: String,
    pub merchant_provided_buyer_identity: String,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Settlement {
    Completed,
    AlreadySettled,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct SignedWaffoResponse {
    pub body: Vec<u8>,
    pub signature: String,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum WebhookFailure {
    InvalidSignature,
    InvalidPayload,
    Rejected,
    Unavailable,
}

async fn pancake_webhook(
    State(state): State<WaffoWebhookState>,
    Path(environment): Path<String>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    if !matches!(state.availability.pancake_enabled().await, Ok(true)) {
        return plain(StatusCode::FORBIDDEN, "webhook disabled");
    }
    if !matches!(environment.as_str(), "test" | "prod") {
        return plain(StatusCode::NOT_FOUND, "unknown env");
    }
    let Some(signature) = header(&headers, "x-waffo-signature") else {
        return plain(StatusCode::UNAUTHORIZED, "invalid signature");
    };
    let event = match state.pancake.verify(&body, signature).await {
        Ok(event) => event,
        Err(_) => return plain(StatusCode::UNAUTHORIZED, "invalid signature"),
    };
    // Pancake selects verification material from `mode`; route environment is
    // a second, independent guard against a dashboard endpoint mix-up.
    if !event.mode.trim().eq_ignore_ascii_case(environment.trim()) {
        return plain(StatusCode::OK, "OK");
    }
    if event.event_type.trim() != "order.completed" {
        return plain(StatusCode::OK, "OK");
    }
    let trade_no = event.order_merchant_external_id.trim();
    let identity = event.merchant_provided_buyer_identity.trim();
    if trade_no.is_empty() || identity.is_empty() {
        // The legacy resolver logs permanently unresolvable events and acks
        // them so Pancake does not retry forever.
        return plain(StatusCode::OK, "OK");
    }
    let result = if trade_no.starts_with(PANCAKE_SUBSCRIPTION_PREFIX) {
        state
            .processor
            .complete_pancake_subscription(trade_no, identity, &body)
            .await
    } else {
        state
            .processor
            .complete_pancake_top_up(trade_no, identity, &body)
            .await
    };
    match result {
        Ok(Settlement::Completed | Settlement::AlreadySettled) => plain(StatusCode::OK, "OK"),
        // Exactly matches Go: a transient/transaction failure is retried by
        // Pancake, while the processor classifies malformed/unknown orders as
        // a permanent acknowledgement.
        Err(WebhookFailure::Rejected | WebhookFailure::InvalidPayload) => {
            plain(StatusCode::OK, "OK")
        }
        Err(WebhookFailure::InvalidSignature | WebhookFailure::Unavailable) => {
            plain(StatusCode::INTERNAL_SERVER_ERROR, "retry")
        }
    }
}

async fn waffo_webhook(
    State(state): State<WaffoWebhookState>,
    context: Option<Extension<RequestContext>>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    if !matches!(state.availability.waffo_enabled().await, Ok(true)) {
        return StatusCode::FORBIDDEN.into_response();
    }
    let Some(signature) = header(&headers, "x-signature") else {
        return StatusCode::BAD_REQUEST.into_response();
    };
    if state.waffo.verify(&body, signature).await.is_err() {
        return StatusCode::BAD_REQUEST.into_response();
    }
    let event = match serde_json::from_slice::<WaffoEvent>(&body) {
        Ok(event) => event,
        Err(_) => return waffo_response(&state, false, "invalid payload").await,
    };
    if event.event_type != "PAYMENT_NOTIFICATION" {
        return waffo_response(&state, true, "").await;
    }
    let result = event.result;
    if result.order_status != "PAY_SUCCESS" {
        if !result.merchant_order_id.trim().is_empty() {
            // Go deliberately only logs this error: the acknowledgement remains
            // successful because a failed status is terminal at the provider.
            let _ = state
                .processor
                .mark_waffo_top_up_failed(result.merchant_order_id.trim())
                .await;
        }
        return waffo_response(&state, true, "").await;
    }
    if result.merchant_order_id.trim().is_empty() {
        return waffo_response(&state, false, "missing merchant order id").await;
    }
    let canonical_client_ip =
        context.and_then(|Extension(context)| context.client_ip.map(|ip| ip.to_string()));
    match state
        .processor
        .complete_waffo_top_up(
            result.merchant_order_id.trim(),
            canonical_client_ip.as_deref(),
            &body,
        )
        .await
    {
        Ok(Settlement::Completed | Settlement::AlreadySettled) => {
            waffo_response(&state, true, "").await
        }
        Err(error) => waffo_response(&state, false, error_message(error)).await,
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct WaffoEvent {
    #[serde(default)]
    event_type: String,
    #[serde(default)]
    result: WaffoPaymentResult,
}

#[derive(Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct WaffoPaymentResult {
    #[serde(default)]
    merchant_order_id: String,
    #[serde(default)]
    order_status: String,
}

async fn waffo_response(state: &WaffoWebhookState, success: bool, message: &str) -> Response {
    let signed = match state.waffo.signed_response(success, message).await {
        Ok(response) => response,
        Err(_) => return StatusCode::INTERNAL_SERVER_ERROR.into_response(),
    };
    let mut response = (StatusCode::OK, signed.body).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json"),
    );
    let Ok(signature) = HeaderValue::from_str(&signed.signature) else {
        return StatusCode::INTERNAL_SERVER_ERROR.into_response();
    };
    response.headers_mut().insert("x-signature", signature);
    response
}

fn header<'a>(headers: &'a HeaderMap, name: &str) -> Option<&'a str> {
    headers
        .get(name)?
        .to_str()
        .ok()
        .filter(|value| !value.is_empty())
}

fn error_message(error: WebhookFailure) -> &'static str {
    match error {
        WebhookFailure::InvalidSignature => "invalid signature",
        WebhookFailure::InvalidPayload => "invalid payload",
        WebhookFailure::Rejected => "payment rejected",
        WebhookFailure::Unavailable => "payment unavailable",
    }
}

fn plain(status: StatusCode, body: &'static str) -> Response {
    (status, body).into_response()
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{
        body::{Body, to_bytes},
        http::Request,
    };
    use std::sync::{
        Mutex, MutexGuard,
        atomic::{AtomicBool, Ordering},
    };
    use tower::ServiceExt;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    fn lock_recover<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
        match mutex.lock() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        }
    }

    struct Availability {
        pancake: AtomicBool,
        waffo: AtomicBool,
    }
    #[async_trait]
    impl WaffoWebhookAvailability for Availability {
        async fn waffo_enabled(&self) -> Result<bool, WebhookFailure> {
            Ok(self.waffo.load(Ordering::Relaxed))
        }
        async fn pancake_enabled(&self) -> Result<bool, WebhookFailure> {
            Ok(self.pancake.load(Ordering::Relaxed))
        }
    }
    struct FailingAvailability;
    #[async_trait]
    impl WaffoWebhookAvailability for FailingAvailability {
        async fn waffo_enabled(&self) -> Result<bool, WebhookFailure> {
            Err(WebhookFailure::Unavailable)
        }
        async fn pancake_enabled(&self) -> Result<bool, WebhookFailure> {
            Err(WebhookFailure::Unavailable)
        }
    }
    struct Pancake;
    #[async_trait]
    impl PancakeWebhookVerifier for Pancake {
        async fn verify(&self, _: &[u8], signature: &str) -> Result<PancakeEvent, WebhookFailure> {
            if signature != "valid-pancake" {
                return Err(WebhookFailure::InvalidSignature);
            }
            Ok(PancakeEvent {
                id: "evt-1".into(),
                event_type: "order.completed".into(),
                mode: "test".into(),
                order_merchant_external_id: "WAFFO_PANCAKE_SUB-1".into(),
                merchant_provided_buyer_identity: "new-api-user-1".into(),
            })
        }
    }

    struct MalformedPancake;
    #[async_trait]
    impl PancakeWebhookVerifier for MalformedPancake {
        async fn verify(&self, _: &[u8], _: &str) -> Result<PancakeEvent, WebhookFailure> {
            Err(WebhookFailure::InvalidPayload)
        }
    }
    struct Waffo;
    #[async_trait]
    impl WaffoWebhookVerifier for Waffo {
        async fn verify(&self, _: &[u8], signature: &str) -> Result<(), WebhookFailure> {
            (signature == "valid-waffo")
                .then_some(())
                .ok_or(WebhookFailure::InvalidSignature)
        }
        async fn signed_response(
            &self,
            success: bool,
            message: &str,
        ) -> Result<SignedWaffoResponse, WebhookFailure> {
            Ok(SignedWaffoResponse {
                body: format!(r#"{{"success":{success},"message":"{message}"}}"#).into_bytes(),
                signature: "signed".into(),
            })
        }
    }
    #[derive(Default)]
    struct Processor {
        calls: Mutex<Vec<String>>,
        client_ips: Mutex<Vec<Option<String>>>,
    }
    #[async_trait]
    impl WaffoWebhookProcessor for Processor {
        async fn complete_pancake_top_up(
            &self,
            trade: &str,
            _: &str,
            _: &[u8],
        ) -> Result<Settlement, WebhookFailure> {
            lock_recover(&self.calls).push(format!("pancake-topup:{trade}"));
            Ok(Settlement::Completed)
        }
        async fn complete_pancake_subscription(
            &self,
            trade: &str,
            _: &str,
            _: &[u8],
        ) -> Result<Settlement, WebhookFailure> {
            lock_recover(&self.calls).push(format!("pancake-sub:{trade}"));
            Ok(Settlement::AlreadySettled)
        }
        async fn complete_waffo_top_up(
            &self,
            trade: &str,
            caller_ip: Option<&str>,
            _: &[u8],
        ) -> Result<Settlement, WebhookFailure> {
            lock_recover(&self.calls).push(format!("waffo:{trade}"));
            lock_recover(&self.client_ips).push(caller_ip.map(str::to_owned));
            Ok(Settlement::Completed)
        }
        async fn mark_waffo_top_up_failed(&self, trade: &str) -> Result<(), WebhookFailure> {
            lock_recover(&self.calls).push(format!("failed:{trade}"));
            Ok(())
        }
    }
    fn app(processor: Arc<Processor>) -> Router {
        missing_billing_webhooks_router(WaffoWebhookState::new(
            Arc::new(Availability {
                pancake: AtomicBool::new(true),
                waffo: AtomicBool::new(true),
            }),
            Arc::new(Pancake),
            Arc::new(Waffo),
            processor,
        ))
    }

    fn disabled_app(processor: Arc<Processor>) -> Router {
        missing_billing_webhooks_router(WaffoWebhookState::new(
            Arc::new(Availability {
                pancake: AtomicBool::new(false),
                waffo: AtomicBool::new(false),
            }),
            Arc::new(Pancake),
            Arc::new(Waffo),
            processor,
        ))
    }

    fn malformed_pancake_app(processor: Arc<Processor>) -> Router {
        missing_billing_webhooks_router(WaffoWebhookState::new(
            Arc::new(Availability {
                pancake: AtomicBool::new(true),
                waffo: AtomicBool::new(true),
            }),
            Arc::new(MalformedPancake),
            Arc::new(Waffo),
            processor,
        ))
    }

    fn failing_availability_app(processor: Arc<Processor>) -> Router {
        missing_billing_webhooks_router(WaffoWebhookState::new(
            Arc::new(FailingAvailability),
            Arc::new(Pancake),
            Arc::new(Waffo),
            processor,
        ))
    }

    #[tokio::test]
    async fn pancake_rejects_unsigned_before_settlement() -> TestResult {
        let processor = Arc::new(Processor::default());
        let request = Request::post("/api/waffo-pancake/webhook/test").body(Body::from("{}"))?;
        let response = app(processor.clone()).oneshot(request).await?;
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        let body = to_bytes(response.into_body(), usize::MAX).await?;
        assert_eq!(String::from_utf8(body.to_vec())?, "invalid signature");
        assert!(lock_recover(&processor.calls).is_empty());
        Ok(())
    }

    #[tokio::test]
    async fn disabled_webhooks_preserve_the_legacy_403_response_shapes() -> TestResult {
        let processor = Arc::new(Processor::default());
        let app = disabled_app(processor.clone());
        let pancake_request =
            Request::post("/api/waffo-pancake/webhook/test").body(Body::from("{}"))?;
        let pancake = app.clone().oneshot(pancake_request).await?;
        assert_eq!(pancake.status(), StatusCode::FORBIDDEN);
        assert_eq!(
            pancake
                .headers()
                .get(header::CONTENT_TYPE)
                .and_then(|value| value.to_str().ok()),
            Some("text/plain; charset=utf-8")
        );
        assert!(pancake.headers().get("x-signature").is_none());
        let pancake_body = to_bytes(pancake.into_body(), usize::MAX).await?;
        assert_eq!(
            String::from_utf8(pancake_body.to_vec())?,
            "webhook disabled"
        );

        let waffo_request = Request::post("/api/waffo/webhook").body(Body::from("{}"))?;
        let waffo = app.oneshot(waffo_request).await?;
        assert_eq!(waffo.status(), StatusCode::FORBIDDEN);
        assert!(waffo.headers().get(header::CONTENT_TYPE).is_none());
        assert!(waffo.headers().get("x-signature").is_none());
        assert!(to_bytes(waffo.into_body(), usize::MAX).await?.is_empty());
        assert!(lock_recover(&processor.calls).is_empty());
        Ok(())
    }

    #[tokio::test]
    async fn availability_failure_preserves_the_legacy_403_response_shapes() -> TestResult {
        let processor = Arc::new(Processor::default());
        let app = failing_availability_app(processor.clone());
        let pancake_request =
            Request::post("/api/waffo-pancake/webhook/test").body(Body::from("{}"))?;
        let pancake = app.clone().oneshot(pancake_request).await?;
        assert_eq!(pancake.status(), StatusCode::FORBIDDEN);
        assert_eq!(
            pancake
                .headers()
                .get(header::CONTENT_TYPE)
                .and_then(|value| value.to_str().ok()),
            Some("text/plain; charset=utf-8")
        );
        assert_eq!(
            to_bytes(pancake.into_body(), usize::MAX).await?.as_ref(),
            b"webhook disabled"
        );

        let waffo_request = Request::post("/api/waffo/webhook").body(Body::from("{}"))?;
        let waffo = app.oneshot(waffo_request).await?;
        assert_eq!(waffo.status(), StatusCode::FORBIDDEN);
        assert!(waffo.headers().get(header::CONTENT_TYPE).is_none());
        assert!(to_bytes(waffo.into_body(), usize::MAX).await?.is_empty());
        assert!(lock_recover(&processor.calls).is_empty());
        Ok(())
    }

    #[tokio::test]
    async fn pancake_malformed_payload_is_an_unauthorized_ack_without_settlement() -> TestResult {
        let processor = Arc::new(Processor::default());
        let request = Request::post("/api/waffo-pancake/webhook/test")
            .header("x-waffo-signature", "syntactically-valid")
            .body(Body::from("{not json"))?;
        let response = malformed_pancake_app(processor.clone())
            .oneshot(request)
            .await?;
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        let body = to_bytes(response.into_body(), usize::MAX).await?;
        assert_eq!(String::from_utf8(body.to_vec())?, "invalid signature");
        assert!(lock_recover(&processor.calls).is_empty());
        Ok(())
    }

    #[tokio::test]
    async fn pancake_completed_subscription_acknowledges_idempotent_settlement() -> TestResult {
        let processor = Arc::new(Processor::default());
        let request = Request::post("/api/waffo-pancake/webhook/test")
            .header("x-waffo-signature", "valid-pancake")
            .body(Body::from("{\"signed\":true}"))?;
        let response = app(processor.clone()).oneshot(request).await?;
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            lock_recover(&processor.calls).as_slice(),
            ["pancake-sub:WAFFO_PANCAKE_SUB-1"]
        );
        Ok(())
    }

    #[tokio::test]
    async fn waffo_invalid_signature_does_not_parse_or_settle() -> TestResult {
        let processor = Arc::new(Processor::default());
        let request = Request::post("/api/waffo/webhook")
            .header("x-signature", "wrong")
            .body(Body::from("not json"))?;
        let response = app(processor.clone()).oneshot(request).await?;
        assert_eq!(response.status(), StatusCode::BAD_REQUEST);
        assert!(lock_recover(&processor.calls).is_empty());
        Ok(())
    }

    #[tokio::test]
    async fn waffo_missing_signature_is_a_bodyless_400_without_side_effects() -> TestResult {
        let processor = Arc::new(Processor::default());
        let request = Request::post("/api/waffo/webhook").body(Body::from("{}"))?;
        let response = app(processor.clone()).oneshot(request).await?;
        assert_eq!(response.status(), StatusCode::BAD_REQUEST);
        assert!(to_bytes(response.into_body(), usize::MAX).await?.is_empty());
        assert!(lock_recover(&processor.calls).is_empty());
        Ok(())
    }

    #[tokio::test]
    async fn signed_waffo_malformed_json_returns_a_signed_failure_without_settlement() -> TestResult
    {
        let processor = Arc::new(Processor::default());
        let request = Request::post("/api/waffo/webhook")
            .header("x-signature", "valid-waffo")
            .body(Body::from("{not json"))?;
        let response = app(processor.clone()).oneshot(request).await?;
        assert_eq!(response.status(), StatusCode::OK);
        let response_signature = response
            .headers()
            .get("x-signature")
            .ok_or_else(|| std::io::Error::other("signed Waffo failure is missing x-signature"))?;
        assert_eq!(response_signature, "signed");
        let body = to_bytes(response.into_body(), usize::MAX).await?;
        assert_eq!(
            String::from_utf8(body.to_vec())?,
            r#"{"success":false,"message":"invalid payload"}"#
        );
        assert!(lock_recover(&processor.calls).is_empty());
        Ok(())
    }

    #[tokio::test]
    async fn waffo_success_returns_provider_signed_ack_after_processor_commit() -> TestResult {
        let processor = Arc::new(Processor::default());
        let request = Request::post("/api/waffo/webhook")
            .header("x-signature", "valid-waffo")
            .body(Body::from(r#"{"eventType":"PAYMENT_NOTIFICATION","result":{"merchantOrderId":"trade-1","orderStatus":"PAY_SUCCESS"}}"#))?;
        let response = app(processor.clone()).oneshot(request).await?;
        assert_eq!(response.status(), StatusCode::OK);
        let response_signature = response
            .headers()
            .get("x-signature")
            .ok_or_else(|| std::io::Error::other("successful Waffo ack is missing x-signature"))?;
        assert_eq!(response_signature, "signed");
        assert_eq!(lock_recover(&processor.calls).as_slice(), ["waffo:trade-1"]);
        Ok(())
    }

    #[tokio::test]
    async fn waffo_settlement_uses_canonical_client_ip_not_raw_header() -> TestResult {
        let processor = Arc::new(Processor::default());
        let canonical_ip: std::net::IpAddr = "203.0.113.7".parse()?;
        let payload = r#"{"eventType":"PAYMENT_NOTIFICATION","result":{"merchantOrderId":"trade-1","orderStatus":"PAY_SUCCESS"}}"#;

        for raw_ip in [Some("198.51.100.9"), None, Some("not-an-ip")] {
            let mut request =
                Request::post("/api/waffo/webhook").header("x-signature", "valid-waffo");
            if let Some(raw_ip) = raw_ip {
                request = request.header("x-real-ip", raw_ip);
            }
            let mut request = request.body(Body::from(payload))?;
            request.extensions_mut().insert(RequestContext {
                request_id: "request-1".into(),
                client_ip: Some(canonical_ip),
            });

            let response = app(processor.clone()).oneshot(request).await?;
            assert_eq!(response.status(), StatusCode::OK);
        }

        assert_eq!(
            lock_recover(&processor.client_ips).as_slice(),
            [
                Some("203.0.113.7".to_owned()),
                Some("203.0.113.7".to_owned()),
                Some("203.0.113.7".to_owned()),
            ]
        );
        Ok(())
    }
}
