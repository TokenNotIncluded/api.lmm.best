//! Native Stripe primitives matching the pinned stripe-go/v81 contract.
//!
//! The caller must persist its immutable order before `create_checkout` and
//! retain it on every transport/provider error. This module never credits a
//! wallet or acknowledges a webhook; those require the durable ledger.

use hmac::{Hmac, Mac};
use secrecy::{ExposeSecret, SecretString};
use serde_json::Value;
use sha2::Sha256;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

const STRIPE_API_VERSION: &str = "2025-02-24.acacia";
const RESPONSE_LIMIT: usize = 1024 * 1024;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum StripeProviderError {
    Configuration,
    Transport,
    Rejected,
    InvalidResponse,
    InvalidSignature,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct StripePrice {
    pub product_id: String,
    pub currency: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct StripeSessionRequest {
    pub trade_no: String,
    pub expected_amount_micros: i64,
    pub customer: String,
    pub email: String,
    pub success_url: String,
    pub cancel_url: String,
    pub allow_promotion_codes: bool,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct StripeRecurringPrice {
    pub id: String,
    pub active: bool,
    pub recurring: bool,
    pub currency: String,
    pub unit_amount: i64,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct StripeSubscriptionRequest {
    pub trade_no: String,
    pub price_id: String,
    pub customer: String,
    pub email: String,
    pub return_url: String,
}

#[derive(Clone)]
pub struct StripeApiClient {
    client: reqwest::Client,
    base: reqwest::Url,
}

impl StripeApiClient {
    /// Explicit fixture injection, restricted to literal loopback HTTP. The
    /// ordinary constructor always uses Stripe's HTTPS endpoint.
    pub fn loopback(endpoint: &str) -> Result<Self, StripeProviderError> {
        let base = reqwest::Url::parse(endpoint).map_err(|_| StripeProviderError::Configuration)?;
        if base.scheme() != "http"
            || base.host_str() != Some("127.0.0.1")
            || !base.username().is_empty()
            || base.password().is_some()
        {
            return Err(StripeProviderError::Configuration);
        }
        let mut client = Self::new()?;
        client.base = base;
        Ok(client)
    }
    pub fn new() -> Result<Self, StripeProviderError> {
        let client = reqwest::Client::builder()
            .timeout(Duration::from_secs(80))
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .map_err(|_| StripeProviderError::Configuration)?;
        Ok(Self {
            client,
            base: reqwest::Url::parse("https://api.stripe.com/")
                .map_err(|_| StripeProviderError::Configuration)?,
        })
    }

    pub async fn price(
        &self,
        secret: &SecretString,
        price_id: &str,
    ) -> Result<StripePrice, StripeProviderError> {
        let price = self.read_price(secret, price_id).await?;
        let product = price
            .get("product")
            .and_then(|product| {
                product
                    .as_str()
                    .or_else(|| product.get("id").and_then(Value::as_str))
            })
            .filter(|product| !product.trim().is_empty())
            .ok_or(StripeProviderError::InvalidResponse)?;
        let currency = price
            .get("currency")
            .and_then(Value::as_str)
            .map(|currency| currency.trim().to_ascii_uppercase())
            .filter(|currency| !currency.is_empty())
            .ok_or(StripeProviderError::InvalidResponse)?;
        Ok(StripePrice {
            product_id: product.to_owned(),
            currency,
        })
    }

    async fn read_price(
        &self,
        secret: &SecretString,
        price_id: &str,
    ) -> Result<Value, StripeProviderError> {
        validate_key(secret)?;
        if price_id.trim().is_empty() {
            return Err(StripeProviderError::Configuration);
        }
        let mut url = self.base.clone();
        url.path_segments_mut()
            .map_err(|_| StripeProviderError::Configuration)?
            .clear()
            .extend(["v1", "prices", price_id]);
        self.request(
            self.client
                .get(url)
                .bearer_auth(secret.expose_secret())
                .header("Stripe-Version", STRIPE_API_VERSION),
        )
        .await
    }

    pub async fn recurring_price(
        &self,
        secret: &SecretString,
        price_id: &str,
    ) -> Result<StripeRecurringPrice, StripeProviderError> {
        let value = self.read_price(secret, price_id).await?;
        Ok(StripeRecurringPrice {
            id: value
                .get("id")
                .and_then(Value::as_str)
                .unwrap_or("")
                .to_owned(),
            active: value
                .get("active")
                .and_then(Value::as_bool)
                .unwrap_or(false),
            recurring: value.get("recurring").is_some_and(Value::is_object),
            currency: value
                .get("currency")
                .and_then(Value::as_str)
                .unwrap_or("")
                .to_owned(),
            unit_amount: value
                .get("unit_amount")
                .and_then(Value::as_i64)
                .unwrap_or(0),
        })
    }

    pub async fn create_subscription_checkout(
        &self,
        secret: &SecretString,
        input: &StripeSubscriptionRequest,
    ) -> Result<String, StripeProviderError> {
        let mut form = vec![
            ("client_reference_id".into(), input.trade_no.clone()),
            ("success_url".into(), input.return_url.clone()),
            ("cancel_url".into(), input.return_url.clone()),
            ("mode".into(), "subscription".into()),
            ("line_items[0][price]".into(), input.price_id.clone()),
            ("line_items[0][quantity]".into(), "1".into()),
            (
                "metadata[subscription_trade_no]".into(),
                input.trade_no.clone(),
            ),
            (
                "metadata[subscription_price_id]".into(),
                input.price_id.trim().to_owned(),
            ),
            (
                "subscription_data[metadata][subscription_trade_no]".into(),
                input.trade_no.clone(),
            ),
            (
                "subscription_data[metadata][subscription_price_id]".into(),
                input.price_id.trim().to_owned(),
            ),
        ];
        customer_fields(&mut form, &input.customer, &input.email);
        let checkout = self.post_checkout(secret, &form).await?;
        // The Go subscription controller validates the recurring Price before
        // creating the session, then returns its URL without a subtotal check.
        Ok(checkout
            .get("url")
            .and_then(Value::as_str)
            .unwrap_or("")
            .to_owned())
    }

    pub async fn create_checkout(
        &self,
        secret: &SecretString,
        price: &StripePrice,
        request: &StripeSessionRequest,
    ) -> Result<String, StripeProviderError> {
        validate_key(secret)?;
        if price.product_id.trim().is_empty()
            || request.trade_no.is_empty()
            || request.success_url.is_empty()
            || request.cancel_url.is_empty()
        {
            return Err(StripeProviderError::Configuration);
        }
        let minor = micros_to_minor(request.expected_amount_micros, &price.currency)?;
        let mut form = vec![
            ("client_reference_id".to_owned(), request.trade_no.clone()),
            ("success_url".into(), request.success_url.clone()),
            ("cancel_url".into(), request.cancel_url.clone()),
            ("mode".into(), "payment".into()),
            (
                "allow_promotion_codes".into(),
                request.allow_promotion_codes.to_string(),
            ),
            ("line_items[0][quantity]".into(), "1".into()),
            (
                "line_items[0][price_data][currency]".into(),
                price.currency.to_ascii_lowercase(),
            ),
            (
                "line_items[0][price_data][product]".into(),
                price.product_id.clone(),
            ),
            (
                "line_items[0][price_data][unit_amount]".into(),
                minor.to_string(),
            ),
        ];
        customer_fields(&mut form, &request.customer, &request.email);
        let checkout = self.post_checkout(secret, &form).await?;
        let subtotal = checkout
            .get("amount_subtotal")
            .and_then(Value::as_i64)
            .ok_or(StripeProviderError::InvalidResponse)?;
        if minor_to_micros(subtotal, &price.currency)? != request.expected_amount_micros {
            return Err(StripeProviderError::InvalidResponse);
        }
        checkout
            .get("url")
            .and_then(Value::as_str)
            .filter(|url| !url.trim().is_empty())
            .map(str::to_owned)
            .ok_or(StripeProviderError::InvalidResponse)
    }

    async fn post_checkout(
        &self,
        secret: &SecretString,
        form: &[(String, String)],
    ) -> Result<Value, StripeProviderError> {
        validate_key(secret)?;
        self.request(
            self.client
                .post(
                    self.base
                        .join("v1/checkout/sessions")
                        .map_err(|_| StripeProviderError::Configuration)?,
                )
                .bearer_auth(secret.expose_secret())
                .header("Stripe-Version", STRIPE_API_VERSION)
                // Generate once for this logical SDK call. All its retries
                // must send the same key and identical request bytes.
                .header("Idempotency-Key", uuid::Uuid::new_v4().to_string())
                .form(form),
        )
        .await
    }

    async fn request(
        &self,
        builder: reqwest::RequestBuilder,
    ) -> Result<Value, StripeProviderError> {
        let request = builder
            .build()
            .map_err(|_| StripeProviderError::Configuration)?;
        for attempt in 0..=2 {
            let copy = request
                .try_clone()
                .ok_or(StripeProviderError::Configuration)?;
            let (result, retry) = match self.client.execute(copy).await {
                Err(error) => {
                    let retry = retry_transport(&error);
                    (Err(StripeProviderError::Transport), retry)
                }
                Ok(response) => {
                    let status = response.status();
                    let advice = response
                        .headers()
                        .get("Stripe-Should-Retry")
                        .and_then(|v| v.to_str().ok())
                        .unwrap_or("")
                        .to_owned();
                    match read_response(response).await {
                        Err(error) => (Err(error), true),
                        Ok(value) => {
                            let retry = if advice == "false" {
                                false
                            } else if advice == "true" {
                                true
                            } else {
                                status.as_u16() == 409
                                    || status.is_server_error()
                                    || (status.as_u16() == 429
                                        && value.pointer("/error/code").and_then(Value::as_str)
                                            == Some("lock_timeout"))
                            };
                            (
                                if status.is_success() {
                                    Ok(value)
                                } else {
                                    Err(StripeProviderError::Rejected)
                                },
                                retry,
                            )
                        }
                    }
                }
            };
            if !retry || attempt == 2 {
                return result;
            }
            // stripe-go/v81: 500 ms base, quadratic backoff, 75–100% jitter,
            // never below the base. Only two retries are enabled by default.
            let nanos = 500_000_000_u64 * (1 + attempt * attempt);
            let jitter = rand::random_range(0..nanos / 4);
            tokio::time::sleep(Duration::from_nanos((nanos - jitter).max(500_000_000))).await;
        }
        unreachable!("the final retry returns its result")
    }
}

fn retry_transport(error: &reqwest::Error) -> bool {
    use std::error::Error;
    if error.is_builder() || error.is_redirect() {
        return false;
    }
    // reqwest's outer message omits the rustls certificate reason. Inspect
    // its causes so an unknown certificate authority is not retried.
    let mut cause: Option<&(dyn Error + 'static)> = Some(error);
    while let Some(error) = cause {
        let message = error.to_string();
        if message.contains("UnknownIssuer") || message.contains("unknown certificate authority") {
            return false;
        }
        cause = error.source();
    }
    true
}

fn customer_fields(form: &mut Vec<(String, String)>, customer: &str, email: &str) {
    if customer.is_empty() {
        form.push(("customer_creation".into(), "always".into()));
        if !email.is_empty() {
            form.push(("customer_email".into(), email.to_owned()));
        }
    } else {
        form.push(("customer".into(), customer.to_owned()));
    }
}

async fn read_response(mut response: reqwest::Response) -> Result<Value, StripeProviderError> {
    if response
        .content_length()
        .is_some_and(|size| size > RESPONSE_LIMIT as u64)
    {
        return Err(StripeProviderError::InvalidResponse);
    }
    let mut body = Vec::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| StripeProviderError::Transport)?
    {
        if body
            .len()
            .checked_add(chunk.len())
            .is_none_or(|size| size > RESPONSE_LIMIT)
        {
            return Err(StripeProviderError::InvalidResponse);
        }
        body.extend_from_slice(&chunk);
    }
    serde_json::from_slice(&body).map_err(|_| StripeProviderError::InvalidResponse)
}

fn validate_key(secret: &SecretString) -> Result<(), StripeProviderError> {
    if secret.expose_secret().starts_with("sk_") || secret.expose_secret().starts_with("rk_") {
        Ok(())
    } else {
        Err(StripeProviderError::Configuration)
    }
}

fn minor_divisor(currency: &str) -> Result<i64, StripeProviderError> {
    let currency = currency.trim().to_ascii_uppercase();
    if currency.is_empty() {
        return Err(StripeProviderError::Configuration);
    }
    Ok(
        if matches!(
            currency.as_str(),
            "BIF"
                | "CLP"
                | "DJF"
                | "GNF"
                | "JPY"
                | "KMF"
                | "KRW"
                | "MGA"
                | "PYG"
                | "RWF"
                | "UGX"
                | "VND"
                | "VUV"
                | "XAF"
                | "XOF"
                | "XPF"
        ) {
            1_000_000
        } else {
            10_000
        },
    )
}

pub fn micros_to_minor(amount: i64, currency: &str) -> Result<i64, StripeProviderError> {
    let divisor = minor_divisor(currency)?;
    if amount <= 0 || amount % divisor != 0 {
        return Err(StripeProviderError::Configuration);
    }
    Ok(amount / divisor)
}

pub fn minor_to_micros(amount: i64, currency: &str) -> Result<i64, StripeProviderError> {
    if amount <= 0 {
        return Err(StripeProviderError::InvalidResponse);
    }
    amount
        .checked_mul(minor_divisor(currency)?)
        .ok_or(StripeProviderError::InvalidResponse)
}

/// Authenticate the exact wire body. Every event type remains available to
/// the caller (including refunds and recurring invoices); authentication alone
/// must never be interpreted as successful settlement.
pub fn verify_stripe_event(
    raw: &[u8],
    header: &str,
    secret: &SecretString,
    now: SystemTime,
) -> Result<Value, StripeProviderError> {
    if header.is_empty() || secret.expose_secret().is_empty() {
        return Err(StripeProviderError::InvalidSignature);
    }
    let mut timestamp = 0_i64;
    let mut signatures = Vec::new();
    for pair in header.split(',') {
        let parts: Vec<_> = pair.split('=').collect();
        if parts.len() != 2 {
            return Err(StripeProviderError::InvalidSignature);
        }
        match parts[0] {
            "t" => {
                timestamp = parts[1]
                    .parse()
                    .map_err(|_| StripeProviderError::InvalidSignature)?
            }
            "v1" => {
                if let Ok(signature) = hex::decode(parts[1]) {
                    signatures.push(signature);
                }
            }
            _ => {}
        }
    }
    let now_nanos = i128::try_from(
        now.duration_since(UNIX_EPOCH)
            .map_err(|_| StripeProviderError::InvalidSignature)?
            .as_nanos(),
    )
    .map_err(|_| StripeProviderError::InvalidSignature)?;
    // stripe-go checks only age > 300 s. Future signed timestamps are accepted
    // by that pinned SDK; do not silently change the comparison to abs(age).
    if now_nanos - i128::from(timestamp) * 1_000_000_000 > 300_000_000_000 {
        return Err(StripeProviderError::InvalidSignature);
    }
    let mut mac = Hmac::<Sha256>::new_from_slice(secret.expose_secret().as_bytes())
        .map_err(|_| StripeProviderError::InvalidSignature)?;
    mac.update(timestamp.to_string().as_bytes());
    mac.update(b".");
    mac.update(raw);
    if !signatures
        .iter()
        .any(|signature| mac.clone().verify_slice(signature).is_ok())
    {
        return Err(StripeProviderError::InvalidSignature);
    }
    let event: Value =
        serde_json::from_slice(raw).map_err(|_| StripeProviderError::InvalidResponse)?;
    if !event.is_object() && !event.is_null() {
        return Err(StripeProviderError::InvalidResponse);
    }
    Ok(event)
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{
        Json, Router,
        extract::State,
        http::HeaderMap,
        routing::{get, post},
    };
    use serde_json::json;
    use std::{
        collections::BTreeMap,
        sync::{Arc, Mutex},
    };

    fn signature(raw: &[u8], timestamp: i64, key: &str) -> String {
        let mut mac = Hmac::<Sha256>::new_from_slice(key.as_bytes()).unwrap();
        mac.update(format!("{timestamp}.").as_bytes());
        mac.update(raw);
        format!(
            "t={timestamp},v1={}",
            hex::encode(mac.finalize().into_bytes())
        )
    }

    #[test]
    fn stripe_authentication_handles_rotation_timestamp_and_exact_bytes() {
        let raw = br#"{"type":"refund.created","data":{"object":{"id":"re_1"}}}"#;
        let key = SecretString::from("whsec_fixture");
        let now = UNIX_EPOCH + Duration::from_secs(1_700_000_000);
        let signed = signature(raw, 1_700_000_000, key.expose_secret());
        assert_eq!(
            verify_stripe_event(raw, &format!("v1=bad,{signed},v1=00"), &key, now).unwrap()["type"],
            "refund.created"
        );
        assert!(
            verify_stripe_event(
                raw,
                &signature(raw, 1_699_999_699, key.expose_secret()),
                &key,
                now
            )
            .is_err()
        );
        assert!(
            verify_stripe_event(
                raw,
                &signature(raw, 1_700_000_001, key.expose_secret()),
                &key,
                now
            )
            .is_ok()
        );
        assert!(verify_stripe_event(b"{}", &signed, &key, now).is_err());
        assert!(verify_stripe_event(raw, &format!("{signed},garbage"), &key, now).is_err());
        assert!(verify_stripe_event(raw, &signed, &SecretString::from("other"), now).is_err());
        for invalid in [b"[]".as_slice(), b"123", b"\"event\""] {
            let signed = signature(invalid, 1_700_000_000, key.expose_secret());
            assert_eq!(
                verify_stripe_event(invalid, &signed, &key, now),
                Err(StripeProviderError::InvalidResponse)
            );
        }
    }

    #[test]
    fn stripe_minor_units_never_round_a_nonrepresentable_settlement() {
        assert_eq!(micros_to_minor(1_230_000, "USD").unwrap(), 123);
        assert_eq!(micros_to_minor(123_000_000, "jpy").unwrap(), 123);
        assert!(micros_to_minor(1_000_001, "USD").is_err());
        assert!(micros_to_minor(1_230_000, "JPY").is_err());
        assert!(minor_to_micros(i64::MAX, "USD").is_err());
    }

    type Calls = Arc<Mutex<Vec<BTreeMap<String, String>>>>;
    async fn price(headers: HeaderMap) -> Json<Value> {
        assert_eq!(headers["authorization"], "Bearer sk_fixture");
        Json(json!({"currency":"usd","product":{"id":"prod_fixture"}}))
    }
    async fn checkout(State(calls): State<Calls>, headers: HeaderMap, body: String) -> Json<Value> {
        assert_eq!(headers["stripe-version"], STRIPE_API_VERSION);
        let fields: BTreeMap<String, String> = form_urlencoded::parse(body.as_bytes())
            .into_owned()
            .collect();
        let subtotal = if fields.get("client_reference_id").map(String::as_str) == Some("mismatch")
        {
            1
        } else {
            123
        };
        calls.lock().unwrap().push(fields);
        Json(json!({"url":"https://checkout.stripe.test/cs_fixture","amount_subtotal":subtotal}))
    }

    #[tokio::test]
    async fn checkout_uses_canonical_amount_product_and_one_item_not_platform_quantity() {
        let calls = Calls::default();
        let app = Router::new()
            .route("/v1/prices/price_fixture", get(price))
            .route("/v1/checkout/sessions", post(checkout))
            .with_state(calls.clone());
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let server = tokio::spawn(async move {
            axum::serve(listener, app).await.unwrap();
        });
        let mut client = StripeApiClient::new().unwrap();
        client.base = reqwest::Url::parse(&format!("http://{address}/")).unwrap();
        let secret = SecretString::from("sk_fixture");
        let product = client.price(&secret, "price_fixture").await.unwrap();
        let mut request = StripeSessionRequest {
            trade_no: "ref_fixture".into(),
            expected_amount_micros: 1_230_000,
            customer: String::new(),
            email: "buyer@example.com".into(),
            success_url: "https://console.example/usage-logs".into(),
            cancel_url: "https://console.example/wallet".into(),
            allow_promotion_codes: true,
        };
        assert_eq!(
            client
                .create_checkout(&secret, &product, &request)
                .await
                .unwrap(),
            "https://checkout.stripe.test/cs_fixture"
        );
        let fields = calls.lock().unwrap()[0].clone();
        assert_eq!(fields["line_items[0][price_data][unit_amount]"], "123");
        assert_eq!(fields["line_items[0][price_data][product]"], "prod_fixture");
        assert_eq!(fields["line_items[0][quantity]"], "1");
        assert_eq!(fields["customer_creation"], "always");
        assert_eq!(fields["customer_email"], "buyer@example.com");
        assert!(!fields.contains_key("line_items[0][price]"));
        request.trade_no = "mismatch".into();
        assert_eq!(
            client
                .create_checkout(&secret, &product, &request)
                .await
                .unwrap_err(),
            StripeProviderError::InvalidResponse
        );
        request.expected_amount_micros = 1;
        assert_eq!(
            client
                .create_checkout(&secret, &product, &request)
                .await
                .unwrap_err(),
            StripeProviderError::Configuration
        );
        assert_eq!(
            calls.lock().unwrap().len(),
            2,
            "invalid local amount must never contact Stripe"
        );
        server.abort();
    }

    #[tokio::test]
    async fn stripe_sdk_retry_advice_errors_and_idempotency_match_pinned_backend() {
        use axum::{http::StatusCode, response::IntoResponse};
        type RetryCalls = Arc<Mutex<Vec<(String, String)>>>;
        #[derive(Clone)]
        struct Fixture {
            status: u16,
            advice: &'static str,
            body: &'static str,
            calls: RetryCalls,
        }
        async fn provider(
            State(fixture): State<Fixture>,
            headers: HeaderMap,
            body: String,
        ) -> axum::response::Response {
            let mut calls = fixture.calls.lock().unwrap();
            calls.push((
                headers["idempotency-key"].to_str().unwrap().to_owned(),
                body,
            ));
            if calls.len() == 1 {
                (
                    StatusCode::from_u16(fixture.status).unwrap(),
                    [("stripe-should-retry", fixture.advice)],
                    fixture.body,
                )
                    .into_response()
            } else {
                Json(json!({"url":"https://checkout.stripe.test/recovered"})).into_response()
            }
        }
        for (status, advice, body, expected_calls) in [
            (503, "false", r#"{"error":{"message":"no retry"}}"#, 1),
            (429, "", r#"{"error":{"code":"rate_limit"}}"#, 1),
            (429, "", r#"{"error":{"code":"lock_timeout"}}"#, 2),
            (409, "", r#"{"error":{"message":"conflict"}}"#, 2),
            (400, "true", r#"{"error":{"message":"retry requested"}}"#, 2),
            // SDK decode errors are retried before HTTP advice is consulted.
            (200, "false", "{", 2),
        ] {
            let calls = RetryCalls::default();
            let service = Router::new()
                .route("/v1/checkout/sessions", post(provider))
                .with_state(Fixture {
                    status,
                    advice,
                    body,
                    calls: calls.clone(),
                });
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            let endpoint = format!("http://{}/", listener.local_addr().unwrap());
            let server = tokio::spawn(async move {
                axum::serve(listener, service).await.unwrap();
            });
            let result = StripeApiClient::loopback(&endpoint)
                .unwrap()
                .create_subscription_checkout(
                    &SecretString::from("sk_fixture"),
                    &StripeSubscriptionRequest {
                        trade_no: "sub_ref_fixture".into(),
                        price_id: "price_fixture".into(),
                        customer: "".into(),
                        email: "buyer@example.test".into(),
                        return_url: "https://console.example/wallet".into(),
                    },
                )
                .await;
            if expected_calls == 1 {
                assert_eq!(result, Err(StripeProviderError::Rejected));
            } else {
                assert_eq!(result.unwrap(), "https://checkout.stripe.test/recovered");
            }
            let calls = calls.lock().unwrap();
            assert_eq!(calls.len(), expected_calls);
            assert!(!calls[0].0.is_empty());
            assert!(
                calls.iter().all(|call| call == &calls[0]),
                "key and payload must survive retries unchanged"
            );
            server.abort();
        }
    }
}
