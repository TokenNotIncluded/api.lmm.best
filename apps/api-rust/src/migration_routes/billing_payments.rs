//! Isolated, provider-safe subscription payment routes.
//!
//! This module owns only the route and transaction semantics. Production wiring
//! must supply authenticated identities, a cache invalidator, and real provider
//! adapters; it intentionally has no fallback or no-op implementation.

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::Bytes,
    extract::{RawQuery, Request, State},
    http::{HeaderMap, HeaderValue, Method, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use hmac::{Hmac, Mac};
use secrecy::SecretString;
use serde::Deserialize;
use serde_json::{Value, json};
use sha2::Sha256;
use sqlx::{PgPool, Row};
use std::{
    collections::BTreeMap,
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};
use subtle::ConstantTimeEq;
use thiserror::Error;
use uuid::Uuid;

use crate::auth::{CriticalRateLimitOutcome, DashboardAuth};
use crate::{ClientIpKey, RequestContext, legacy_empty_response};

const EPAY: &str = "epay";
const STRIPE: &str = "stripe";
const CREEM: &str = "creem";
const WAFFO_PANCAKE: &str = "waffo_pancake";
const SUBSCRIPTION_INFO_CACHE_PREFIX: &str = "new-api:subscription_plan_info:v1:sub:";
// The legacy candidate router below still exposes this path for the isolated
// test-instance surface.  The normal listener uses the balance-only router
// above, so keep the compatibility mount explicitly marked as a non-owning
// split route for the repository route-coverage gate.
const BALANCE_PAY_PATH: &str = "/api/subscription/balance/pay";
const INVALIDATE_COMPLETED_PAYMENT_CACHE: &str = r#"
redis.call('DEL', KEYS[1])
if ARGV[1] == '1' then
  redis.call('DEL', KEYS[2])
elseif tonumber(ARGV[2]) > 0 and redis.call('EXISTS', KEYS[2]) == 1 then
  redis.call('HINCRBY', KEYS[2], 'Quota', -tonumber(ARGV[2]))
end
return 1
"#;

#[derive(Clone)]
pub struct BillingHttpState {
    repository: Arc<dyn BillingRepository>,
    authorizer: Arc<dyn BillingAuthorizer>,
    checkout: Arc<dyn CheckoutProvider>,
    epay: Arc<dyn EpayVerifier>,
    stripe: Arc<dyn StripeWebhookVerifier>,
    cache: Arc<dyn BillingCache>,
    compliance: Arc<dyn PaymentCompliance>,
    config: BillingConfig,
}

/// Dependencies for the balance-only subscription purchase route.
///
/// Balance purchases do not contact a payment provider. Keeping this state
/// separate from [`BillingHttpState`] prevents an accidentally incomplete
/// checkout or webhook adapter from becoming part of the production route
/// just because the balance ledger is ready.
#[derive(Clone)]
pub struct SubscriptionBalancePayState {
    repository: Arc<dyn BillingRepository>,
    authorizer: Arc<dyn BillingAuthorizer>,
    cache: Arc<dyn BillingCache>,
    compliance: Arc<dyn PaymentCompliance>,
    dashboard_auth: Option<Arc<dyn DashboardAuth>>,
    payment_access: Arc<dyn BillingPaymentAccess>,
    quota_per_unit: i64,
}

impl SubscriptionBalancePayState {
    #[must_use]
    pub fn new(
        repository: Arc<dyn BillingRepository>,
        authorizer: Arc<dyn BillingAuthorizer>,
        cache: Arc<dyn BillingCache>,
        compliance: Arc<dyn PaymentCompliance>,
        quota_per_unit: i64,
    ) -> Self {
        Self {
            repository,
            authorizer,
            cache,
            compliance,
            dashboard_auth: None,
            payment_access: Arc::new(AllowBillingPaymentAccess),
            quota_per_unit,
        }
    }

    /// Installs the listener-owned dashboard boundary used by the normal
    /// listener. This performs the Go ConsoleAccessGate before UserAuth and
    /// supplies the shared IP-keyed critical limiter.
    #[must_use]
    pub fn with_dashboard_auth(mut self, auth: Arc<dyn DashboardAuth>) -> Self {
        self.dashboard_auth = Some(auth);
        self
    }

    /// Installs the PostgreSQL-backed PaymentAccessGate equivalent.
    #[must_use]
    pub fn with_payment_access(mut self, access: Arc<dyn BillingPaymentAccess>) -> Self {
        self.payment_access = access;
        self
    }
}

/// Runtime boundaries required by billing routes. Keeping them together makes
/// listener composition auditable and prevents constructor argument drift as
/// payment providers are added or frozen.
#[derive(Clone)]
pub struct BillingDependencies {
    pub repository: Arc<dyn BillingRepository>,
    pub authorizer: Arc<dyn BillingAuthorizer>,
    pub checkout: Arc<dyn CheckoutProvider>,
    pub epay: Arc<dyn EpayVerifier>,
    pub stripe: Arc<dyn StripeWebhookVerifier>,
    pub cache: Arc<dyn BillingCache>,
    pub compliance: Arc<dyn PaymentCompliance>,
}

impl BillingHttpState {
    #[must_use]
    pub fn new(dependencies: BillingDependencies, config: BillingConfig) -> Self {
        Self {
            repository: dependencies.repository,
            authorizer: dependencies.authorizer,
            checkout: dependencies.checkout,
            epay: dependencies.epay,
            stripe: dependencies.stripe,
            cache: dependencies.cache,
            compliance: dependencies.compliance,
            config,
        }
    }
}

#[derive(Clone, Debug)]
pub struct BillingConfig {
    pub creem_webhook_secret: Arc<str>,
    pub wallet_url: Arc<str>,
    pub quota_per_unit: i64,
}

impl Default for BillingConfig {
    fn default() -> Self {
        Self {
            creem_webhook_secret: Arc::from(""),
            wallet_url: Arc::from("/wallet"),
            quota_per_unit: 0,
        }
    }
}

/// Payment capability gate. Implementations must make the decision at request
/// time: payment compliance is operator state, not a process-start snapshot.
#[async_trait]
pub trait PaymentCompliance: Send + Sync {
    async fn is_confirmed(&self) -> Result<bool, BillingError>;
}

/// The legacy PaymentAccessGate rejects accounts marked by an administrator
/// or by the derived LinuxDO-email rule before a balance purchase is parsed.
#[async_trait]
pub trait BillingPaymentAccess: Send + Sync {
    async fn allowed(&self, user_id: i64) -> Result<bool, BillingError>;
}

/// Test/fixture default. Production wiring must replace this with
/// [`PgBillingPaymentAccess`] so the route cannot bypass payment restrictions.
pub struct AllowBillingPaymentAccess;

#[async_trait]
impl BillingPaymentAccess for AllowBillingPaymentAccess {
    async fn allowed(&self, _: i64) -> Result<bool, BillingError> {
        Ok(true)
    }
}

#[derive(Clone)]
pub struct PgBillingPaymentAccess {
    pg: PgPool,
}

impl PgBillingPaymentAccess {
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self { pg }
    }
}

#[async_trait]
impl BillingPaymentAccess for PgBillingPaymentAccess {
    async fn allowed(&self, user_id: i64) -> Result<bool, BillingError> {
        let row = sqlx::query(
            "SELECT COALESCE(to_jsonb(users)->>'payment_restriction_flags', '0') AS flags, COALESCE(email, '') AS email FROM users WHERE id = $1 AND deleted_at IS NULL",
        )
        .bind(user_id)
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| BillingError::Storage)?
        .ok_or(BillingError::Storage)?;
        let flags_text: String = row.try_get("flags").map_err(|_| BillingError::Storage)?;
        let flags = flags_text
            .parse::<i64>()
            .map_err(|_| BillingError::Storage)?;
        let email: String = row.try_get("email").map_err(|_| BillingError::Storage)?;
        let linuxdo_email = email
            .trim()
            .rsplit_once('@')
            .is_some_and(|(_, domain)| domain.eq_ignore_ascii_case("linux.do"));
        Ok(flags == 0 && !linuxdo_email)
    }
}

/// PostgreSQL-backed compliance gate matching the legacy `payment_setting`
/// contract. The two options are read on every payment or callback request so
/// an administrator can immediately freeze payments without a process restart.
#[derive(Clone)]
pub struct PgPaymentCompliance {
    pg: PgPool,
}

impl PgPaymentCompliance {
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self { pg }
    }
}

#[async_trait]
impl PaymentCompliance for PgPaymentCompliance {
    async fn is_confirmed(&self) -> Result<bool, BillingError> {
        let rows = sqlx::query(
            "SELECT key, value FROM options WHERE key IN ('payment_setting.compliance_confirmed', 'payment_setting.compliance_terms_version')",
        )
        .fetch_all(&self.pg)
        .await
        .map_err(|_| BillingError::Storage)?;
        let mut values = BTreeMap::new();
        for row in rows {
            values.insert(
                row.try_get::<String, _>("key")
                    .map_err(|_| BillingError::Storage)?,
                row.try_get::<String, _>("value")
                    .map_err(|_| BillingError::Storage)?,
            );
        }
        Ok(values
            .get("payment_setting.compliance_confirmed")
            .is_some_and(|value| value.eq_ignore_ascii_case("true") || value == "1")
            && values
                .get("payment_setting.compliance_terms_version")
                .is_some_and(|value| value == "v1"))
    }
}

/// Safe default for test instances and incomplete listener composition.
/// It intentionally freezes every payment path rather than allowing a stale
/// configuration value to enable financial side effects.
pub struct DisabledPaymentCompliance;

#[async_trait]
impl PaymentCompliance for DisabledPaymentCompliance {
    async fn is_confirmed(&self) -> Result<bool, BillingError> {
        Ok(false)
    }
}

/// Mounts only the PostgreSQL/Valkey-backed balance purchase route.
///
/// The route deliberately has no provider or callback surface. The global
/// API limiter is composed by the listener; this local middleware preserves
/// Go's ordering by authenticating before Axum attempts to deserialize JSON.
pub fn subscription_balance_pay_router(state: SubscriptionBalancePayState) -> Router {
    Router::new()
        .route(
            "/api/subscription/balance/pay",
            post(subscription_balance_pay),
        )
        .route_layer(middleware::from_fn_with_state(
            state.clone(),
            subscription_balance_auth_boundary,
        ))
        .with_state(state)
}

/// Mounts provider checkout and callback routes without the balance-pay path.
///
/// The normal listener already owns [`subscription_balance_pay_router`]; this
/// sibling router keeps those surfaces from overlapping while still letting
/// the isolated candidate merge both families through [`billing_payments_router`].
pub fn billing_provider_payments_router(state: BillingHttpState) -> Router {
    Router::new()
        .route("/api/subscription/epay/pay", post(epay_pay))
        .route("/api/subscription/stripe/pay", post(stripe_pay))
        .route("/api/subscription/creem/pay", post(creem_pay))
        .route(
            "/api/subscription/waffo-pancake/pay",
            post(waffo_pancake_pay),
        )
        .route(
            "/api/subscription/epay/notify",
            get(epay_notify).post(epay_notify),
        )
        .route(
            "/api/subscription/epay/return",
            get(epay_return).post(epay_return),
        )
        .route("/api/stripe/webhook", post(stripe_webhook))
        .route("/api/creem/webhook", post(creem_webhook))
        // Go's UserAuth middleware runs before the JSON body is bound for
        // every user-initiated payment. Callback/webhook routes remain
        // intentionally outside this fence and use provider verification.
        .route_layer(middleware::from_fn_with_state(
            state.clone(),
            billing_payment_auth_boundary,
        ))
        .with_state(state)
}

pub fn billing_payments_router(state: BillingHttpState) -> Router {
    Router::new()
        .route(BALANCE_PAY_PATH, post(balance_pay))
        .route_layer(middleware::from_fn_with_state(
            state.clone(),
            billing_payment_auth_boundary,
        ))
        .with_state(state.clone())
        .merge(billing_provider_payments_router(state))
}

fn is_user_payment_route(path: &str, method: &Method) -> bool {
    *method == Method::POST
        && matches!(
            path,
            "/api/subscription/epay/pay"
                | "/api/subscription/stripe/pay"
                | "/api/subscription/creem/pay"
                | "/api/subscription/waffo-pancake/pay"
                | "/api/subscription/balance/pay"
        )
}

async fn billing_payment_auth_boundary(
    State(state): State<BillingHttpState>,
    request: Request,
    next: Next,
) -> Response {
    if !is_user_payment_route(request.uri().path(), request.method()) {
        return next.run(request).await;
    }
    if state.authorizer.user_id(request.headers()).await.is_err() {
        return payment_error(StatusCode::UNAUTHORIZED, "Unauthorized");
    }
    next.run(request).await
}

async fn subscription_balance_auth_boundary(
    State(state): State<SubscriptionBalancePayState>,
    request: Request,
    next: Next,
) -> Response {
    if request.method() != Method::POST || request.uri().path() != "/api/subscription/balance/pay" {
        return next.run(request).await;
    }
    if let Some(auth) = state.dashboard_auth.as_ref() {
        let Some(token) = dashboard_credential(request.headers()) else {
            return console_not_found();
        };
        let user = match auth
            .self_user_view_for_optional(SecretString::from(token.to_owned()))
            .await
        {
            Ok(user) => user,
            Err(_) => return console_not_found(),
        };
        if !user.developer_access_granted {
            return console_not_found();
        }
    }
    let user_id = match state.authorizer.user_id(request.headers()).await {
        Ok(user_id) => user_id,
        Err(_) => return payment_error(StatusCode::UNAUTHORIZED, "Unauthorized"),
    };
    match state.payment_access.allowed(user_id).await {
        Ok(true) => {}
        Ok(false) => return with_auth_version(payment_access_denied()),
        Err(_) => {
            return with_auth_version(payment_error(
                StatusCode::INTERNAL_SERVER_ERROR,
                "Unable to verify payment access.",
            ));
        }
    }
    if let Some(auth) = state.dashboard_auth.as_ref() {
        let Some(client_ip) = request_client_ip(&request) else {
            return with_auth_version(legacy_empty_response(
                StatusCode::INTERNAL_SERVER_ERROR,
                None,
            ));
        };
        match auth.check_critical_rate_limit(&client_ip).await {
            Ok(CriticalRateLimitOutcome::Allowed) => {}
            Ok(CriticalRateLimitOutcome::Rejected {
                retry_after_seconds,
            }) => {
                return with_auth_version(legacy_empty_response(
                    StatusCode::TOO_MANY_REQUESTS,
                    Some(retry_after_seconds),
                ));
            }
            Err(_) => {
                return with_auth_version(legacy_empty_response(
                    StatusCode::INTERNAL_SERVER_ERROR,
                    None,
                ));
            }
        }
    }
    // Go's controller checks payment compliance before binding the JSON body,
    // but only after the UserAuth, PaymentAccessGate, and CriticalRateLimit
    // middleware have run. Keep this ordering so a rejected request consumes
    // the same limiter decision without exposing a body-binding error.
    if let Ok(false) | Err(_) = state.compliance.is_confirmed().await {
        return with_auth_version(payment_error(
            StatusCode::OK,
            "payment compliance is required",
        ));
    }
    with_auth_version(next.run(request).await)
}

#[async_trait]
pub trait BillingAuthorizer: Send + Sync {
    async fn user_id(&self, headers: &HeaderMap) -> Result<i64, BillingError>;
}

/// Production payment identity adapter. It derives the user only from the
/// signed dashboard credential, never from caller-controlled headers or JSON.
#[derive(Clone)]
pub struct DashboardBillingAuthorizer {
    auth: Arc<dyn DashboardAuth>,
}

impl DashboardBillingAuthorizer {
    #[must_use]
    pub fn new(auth: Arc<dyn DashboardAuth>) -> Self {
        Self { auth }
    }
}

#[async_trait]
impl BillingAuthorizer for DashboardBillingAuthorizer {
    async fn user_id(&self, headers: &HeaderMap) -> Result<i64, BillingError> {
        let token = dashboard_credential(headers).ok_or(BillingError::Unauthorized)?;
        let user = self
            .auth
            .self_user(SecretString::from(token.to_owned()))
            .await
            .map_err(|_| BillingError::Unauthorized)?;
        if user.id <= 0 || user.status != 1 {
            return Err(BillingError::Unauthorized);
        }
        Ok(user.id)
    }
}

fn dashboard_credential(headers: &HeaderMap) -> Option<&str> {
    let value = headers.get(header::AUTHORIZATION)?.to_str().ok()?;
    let mut parts = value.split_whitespace();
    let first = parts.next()?;
    let second = parts.next();
    if parts.next().is_some() {
        return None;
    }
    match second {
        Some(token) if first.eq_ignore_ascii_case("bearer") && !token.is_empty() => Some(token),
        None if !first.is_empty() => Some(first),
        _ => None,
    }
}

fn request_client_ip(request: &Request) -> Option<String> {
    request
        .extensions()
        .get::<ClientIpKey>()
        .map(|key| key.0.clone())
        .or_else(|| {
            request
                .extensions()
                .get::<RequestContext>()
                .and_then(|context| context.client_ip)
                .map(|ip| ip.to_string())
        })
}

fn console_not_found() -> Response {
    (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response()
}

fn with_auth_version(mut response: Response) -> Response {
    response.headers_mut().insert(
        "auth-version",
        HeaderValue::from_static("864b7076dbcd0a3c01b5520316720ebf"),
    );
    response
}

fn payment_access_denied() -> Response {
    (
        StatusCode::FORBIDDEN,
        Json(json!({
            "success": false,
            "code": "PAYMENT_UNAVAILABLE",
            "message": "Payment is unavailable for this account."
        })),
    )
        .into_response()
}

#[derive(Clone, Debug)]
pub struct PendingOrder {
    pub trade_no: String,
    pub plan_id: i64,
    pub user_id: i64,
    pub money: String,
    /// The persisted plan currency passed unchanged to the provider boundary.
    pub currency: String,
    pub payment_method: String,
    pub provider: String,
}

#[derive(Clone, Debug)]
pub struct CreateOrder {
    pub user_id: i64,
    pub plan_id: i64,
    pub payment_method: String,
    pub provider: &'static str,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Completion {
    Completed {
        subscription_id: i64,
        user_id: i64,
        quota_charged: i64,
        group_changed: bool,
    },
    AlreadySucceeded,
    Rejected,
}

#[async_trait]
pub trait BillingRepository: Send + Sync {
    async fn create_pending(&self, input: CreateOrder) -> Result<PendingOrder, BillingError>;
    async fn expire(&self, trade_no: &str) -> Result<(), BillingError>;
    async fn fail(&self, trade_no: &str) -> Result<(), BillingError>;
    async fn purchase_with_balance(
        &self,
        user_id: i64,
        plan_id: i64,
        quota_per_unit: i64,
    ) -> Result<Completion, BillingError>;
    async fn complete(
        &self,
        trade_no: &str,
        provider: &str,
        payload: &str,
        method: Option<&str>,
    ) -> Result<Completion, BillingError>;
}

#[async_trait]
pub trait BillingCache: Send + Sync {
    async fn invalidate_completed_payment(
        &self,
        subscription_id: i64,
        user_id: i64,
        quota_charged: i64,
        group_changed: bool,
    );
}

/// Production Valkey invalidator for derived subscription-plan information.
/// It is intentionally not an idempotency store: PostgreSQL's `FOR UPDATE`
/// order lock is the sole completion boundary. Cache invalidation is retried by
/// a duplicate provider callback if this best-effort post-commit operation is
/// temporarily unavailable.
pub struct ValkeyBillingCache {
    client: redis::Client,
}

impl ValkeyBillingCache {
    #[must_use]
    pub fn new(client: redis::Client) -> Self {
        Self { client }
    }
}

#[async_trait]
impl BillingCache for ValkeyBillingCache {
    async fn invalidate_completed_payment(
        &self,
        subscription_id: i64,
        user_id: i64,
        quota_charged: i64,
        group_changed: bool,
    ) {
        if subscription_id <= 0 || user_id <= 0 {
            return;
        }
        let subscription_key = format!("{SUBSCRIPTION_INFO_CACHE_PREFIX}{subscription_id}");
        let user_key = format!("user:{user_id}");
        let result = async {
            let mut connection = self.client.get_multiplexed_async_connection().await?;
            // Cache invalidation must never manufacture a partial `user:`
            // hash after TTL expiry. Such a hash is neither a cache hit nor a
            // valid representation of PostgreSQL, and could otherwise live
            // until a later auth lookup repairs it.
            redis::Script::new(INVALIDATE_COMPLETED_PAYMENT_CACHE)
                .key(subscription_key)
                .key(user_key)
                .arg(if group_changed { 1 } else { 0 })
                .arg(quota_charged.max(0))
                .invoke_async::<i64>(&mut connection)
                .await
        }
        .await;
        if let Err(error) = result {
            tracing::warn!(%error, subscription_id, "subscription cache invalidation failed after committed billing transaction");
        }
    }
}

#[async_trait]
pub trait CheckoutProvider: Send + Sync {
    /// Called before creating a pending order. A frozen adapter therefore has
    /// zero persisted order, external request, or cache side effect.
    async fn ensure_available(&self, _provider: &str) -> Result<(), BillingError> {
        Ok(())
    }
    async fn start(&self, order: &PendingOrder) -> Result<Checkout, BillingError>;
}

#[derive(Clone, Debug)]
pub struct Checkout {
    pub url: String,
    pub data: Value,
}

/// Explicit HTTP adapter for provider bridges. The application composition
/// must supply an endpoint for every enabled provider; unknown providers,
/// transport failures, non-2xx responses, and malformed replies all fail
/// closed. It intentionally does not invent merchant credentials or a fake
/// checkout response.
pub struct HttpCheckoutProvider {
    client: reqwest::Client,
    endpoints: BTreeMap<String, reqwest::Url>,
}

impl HttpCheckoutProvider {
    pub fn new(
        client: reqwest::Client,
        endpoints: BTreeMap<String, String>,
    ) -> Result<Self, BillingError> {
        let endpoints = endpoints
            .into_iter()
            .map(|(provider, endpoint)| {
                let endpoint =
                    reqwest::Url::parse(&endpoint).map_err(|_| BillingError::Provider)?;
                if !matches!(endpoint.scheme(), "https" | "http") {
                    return Err(BillingError::Provider);
                }
                Ok((provider, endpoint))
            })
            .collect::<Result<_, BillingError>>()?;
        Ok(Self { client, endpoints })
    }
}

#[async_trait]
impl CheckoutProvider for HttpCheckoutProvider {
    async fn start(&self, order: &PendingOrder) -> Result<Checkout, BillingError> {
        let endpoint = self
            .endpoints
            .get(&order.provider)
            .ok_or(BillingError::Provider)?;
        let response = self
            .client
            .post(endpoint.clone())
            .json(&json!({
                "trade_no": order.trade_no,
                "plan_id": order.plan_id,
                "user_id": order.user_id,
                "money": order.money,
                "currency": order.currency,
                "payment_method": order.payment_method,
                "provider": order.provider,
            }))
            .send()
            .await
            .map_err(|_| BillingError::Provider)?;
        if !response.status().is_success() {
            return Err(BillingError::Provider);
        }
        let checkout = response
            .json::<CheckoutWire>()
            .await
            .map_err(|_| BillingError::Provider)?;
        if checkout.url.trim().is_empty() {
            return Err(BillingError::Provider);
        }
        Ok(Checkout {
            url: checkout.url,
            data: checkout.data,
        })
    }
}

/// Explicit test-instance/incomplete-composition checkout adapter. It never
/// has endpoints or credentials and rejects before a pending order is written.
pub struct DisabledCheckoutProvider;

#[async_trait]
impl CheckoutProvider for DisabledCheckoutProvider {
    async fn ensure_available(&self, _: &str) -> Result<(), BillingError> {
        Err(BillingError::ProviderFrozen)
    }

    async fn start(&self, _: &PendingOrder) -> Result<Checkout, BillingError> {
        Err(BillingError::ProviderFrozen)
    }
}

#[derive(Deserialize)]
struct CheckoutWire {
    url: String,
    data: Value,
}

#[async_trait]
pub trait EpayVerifier: Send + Sync {
    async fn verify(&self, fields: &BTreeMap<String, String>) -> Result<EpayResult, BillingError>;
}

/// ePay signature verification is deliberately frozen until a vetted Rust
/// implementation is composed with merchant credentials. Do not replace this
/// with a test fake: accepting an unverifiable callback would settle orders.
pub struct DisabledEpayVerifier;

#[async_trait]
impl EpayVerifier for DisabledEpayVerifier {
    async fn verify(&self, _: &BTreeMap<String, String>) -> Result<EpayResult, BillingError> {
        Err(BillingError::ProviderFrozen)
    }
}
#[derive(Clone, Debug)]
pub struct EpayResult {
    pub verified: bool,
    pub trade_success: bool,
    pub trade_no: String,
    pub payment_method: String,
}

#[async_trait]
pub trait StripeWebhookVerifier: Send + Sync {
    async fn verify(&self, raw: &[u8], signature: &str) -> Result<StripeEvent, BillingError>;
}

/// Stripe webhook validation is frozen until a vetted Stripe signing adapter
/// is linked. This has no network path and rejects every callback.
pub struct DisabledStripeWebhookVerifier;

#[async_trait]
impl StripeWebhookVerifier for DisabledStripeWebhookVerifier {
    async fn verify(&self, _: &[u8], _: &str) -> Result<StripeEvent, BillingError> {
        Err(BillingError::ProviderFrozen)
    }
}
#[derive(Clone, Debug)]
pub struct StripeEvent {
    pub kind: String,
    pub trade_no: String,
    pub paid: bool,
    pub complete: bool,
}

#[derive(Debug, Error)]
pub enum BillingError {
    #[error("unauthorized")]
    Unauthorized,
    #[error("invalid request")]
    InvalidRequest,
    #[error("payment provider unavailable")]
    Provider,
    #[error("payment provider is frozen")]
    ProviderFrozen,
    #[error("invalid payment signature")]
    InvalidSignature,
    #[error("billing storage unavailable")]
    Storage,
    #[error("payment rejected")]
    Rejected,
}

#[derive(Deserialize)]
struct PayRequest {
    plan_id: i64,
    #[serde(default)]
    payment_method: String,
}

async fn epay_pay(
    State(state): State<BillingHttpState>,
    headers: HeaderMap,
    Json(request): Json<PayRequest>,
) -> Response {
    start_payment(state, headers, request, EPAY).await
}
async fn stripe_pay(
    State(state): State<BillingHttpState>,
    headers: HeaderMap,
    Json(request): Json<PayRequest>,
) -> Response {
    start_payment(state, headers, request, STRIPE).await
}
async fn creem_pay(
    State(state): State<BillingHttpState>,
    headers: HeaderMap,
    Json(request): Json<PayRequest>,
) -> Response {
    start_payment(state, headers, request, CREEM).await
}
async fn waffo_pancake_pay(
    State(state): State<BillingHttpState>,
    headers: HeaderMap,
    Json(request): Json<PayRequest>,
) -> Response {
    start_payment(state, headers, request, WAFFO_PANCAKE).await
}
async fn balance_pay(
    State(state): State<BillingHttpState>,
    headers: HeaderMap,
    Json(request): Json<PayRequest>,
) -> Response {
    if !payment_compliance_allowed(&state).await {
        return payment_error(StatusCode::OK, "payment compliance is required");
    }
    if request.plan_id <= 0 {
        return payment_error(StatusCode::OK, "参数错误");
    }
    let user_id = match state.authorizer.user_id(&headers).await {
        Ok(id) => id,
        Err(_) => return payment_error(StatusCode::UNAUTHORIZED, "Unauthorized"),
    };
    match state
        .repository
        .purchase_with_balance(user_id, request.plan_id, state.config.quota_per_unit)
        .await
    {
        Ok(Completion::Completed {
            subscription_id,
            user_id,
            quota_charged,
            group_changed,
        }) => {
            state
                .cache
                .invalidate_completed_payment(
                    subscription_id,
                    user_id,
                    quota_charged,
                    group_changed,
                )
                .await;
            Json(json!({"success": true, "message": "", "data": null})).into_response()
        }
        Ok(Completion::AlreadySucceeded) => {
            Json(json!({"success": true, "message": "", "data": null})).into_response()
        }
        Ok(Completion::Rejected) | Err(BillingError::Rejected) => {
            payment_error(StatusCode::OK, "余额支付失败")
        }
        Err(_) => payment_error(StatusCode::OK, "余额支付失败"),
    }
}

async fn subscription_balance_pay(
    State(state): State<SubscriptionBalancePayState>,
    headers: HeaderMap,
    Json(request): Json<PayRequest>,
) -> Response {
    if !payment_compliance_allowed_for(state.compliance.as_ref()).await {
        return payment_error(StatusCode::OK, "payment compliance is required");
    }
    if request.plan_id <= 0 {
        return payment_error(StatusCode::OK, "参数错误");
    }
    let user_id = match state.authorizer.user_id(&headers).await {
        Ok(id) => id,
        Err(_) => return payment_error(StatusCode::UNAUTHORIZED, "Unauthorized"),
    };
    match state
        .repository
        .purchase_with_balance(user_id, request.plan_id, state.quota_per_unit)
        .await
    {
        Ok(Completion::Completed {
            subscription_id,
            user_id,
            quota_charged,
            group_changed,
        }) => {
            state
                .cache
                .invalidate_completed_payment(
                    subscription_id,
                    user_id,
                    quota_charged,
                    group_changed,
                )
                .await;
            Json(json!({"success": true, "message": "", "data": null})).into_response()
        }
        Ok(Completion::AlreadySucceeded) => {
            Json(json!({"success": true, "message": "", "data": null})).into_response()
        }
        Ok(Completion::Rejected) | Err(_) => payment_error(StatusCode::OK, "余额支付失败"),
    }
}

async fn start_payment(
    state: BillingHttpState,
    headers: HeaderMap,
    request: PayRequest,
    provider: &'static str,
) -> Response {
    if !payment_compliance_allowed(&state).await {
        return payment_error(StatusCode::OK, "payment compliance is required");
    }
    if request.plan_id <= 0 {
        return payment_error(StatusCode::OK, "参数错误");
    }
    let user_id = match state.authorizer.user_id(&headers).await {
        Ok(id) => id,
        Err(_) => return payment_error(StatusCode::UNAUTHORIZED, "Unauthorized"),
    };
    // Check provider readiness before a database mutation. This is required
    // for the disabled test-instance adapter's zero-order guarantee.
    if state.checkout.ensure_available(provider).await.is_err() {
        return payment_error(StatusCode::OK, "拉起支付失败");
    }
    let method = normalize_method(&request.payment_method);
    let order = match state
        .repository
        .create_pending(CreateOrder {
            user_id,
            plan_id: request.plan_id,
            payment_method: method,
            provider,
        })
        .await
    {
        Ok(order) => order,
        Err(_) => return payment_error(StatusCode::OK, "创建订单失败"),
    };
    match state.checkout.start(&order).await {
        Ok(checkout) => match provider {
            STRIPE => {
                Json(json!({"message": "success", "data": {"pay_link": checkout.url}}))
                    .into_response()
            }
            CREEM => {
                Json(json!({"message": "success", "data": {"checkout_url": checkout.url, "order_id": order.trade_no}}))
                    .into_response()
            }
            WAFFO_PANCAKE => Json(json!({"message": "success", "data": checkout.data})).into_response(),
            _ => Json(json!({"message": "success", "data": checkout.data, "url": checkout.url})).into_response(),
        },
        Err(_) if provider == EPAY => {
            let _ = state.repository.expire(&order.trade_no).await;
            Json(json!({"success": false, "message": "拉起支付失败"})).into_response()
        }
        Err(_) if provider == WAFFO_PANCAKE => {
            let _ = state.repository.fail(&order.trade_no).await;
            Json(json!({"message": "error", "data": "拉起支付失败"})).into_response()
        }
        Err(_) => Json(json!({"message": "error", "data": "拉起支付失败"})).into_response(),
    }
}

async fn payment_compliance_allowed(state: &BillingHttpState) -> bool {
    payment_compliance_allowed_for(state.compliance.as_ref()).await
}

async fn payment_compliance_allowed_for(compliance: &dyn PaymentCompliance) -> bool {
    match compliance.is_confirmed().await {
        Ok(confirmed) => confirmed,
        Err(error) => {
            tracing::warn!(%error, "payment compliance lookup failed closed");
            false
        }
    }
}

fn normalize_method(method: &str) -> String {
    method.to_owned()
}

async fn epay_notify(
    State(state): State<BillingHttpState>,
    method: Method,
    query: RawQuery,
    body: Bytes,
) -> Response {
    if !payment_compliance_allowed(&state).await {
        return plain("fail");
    }
    let raw = match callback_body(&body) {
        Ok(raw) => raw,
        Err(()) => return plain("fail"),
    };
    let fields = epay_callback_fields(&method, query.0.as_deref(), raw);
    if fields.is_empty() {
        return plain("fail");
    }
    let result = match state.epay.verify(&fields).await {
        Ok(value) if value.verified && value.trade_success => value,
        _ => return plain("fail"),
    };
    match finish(
        &state,
        &result.trade_no,
        EPAY,
        raw,
        Some(&result.payment_method),
    )
    .await
    {
        Ok(_) => plain("success"),
        Err(_) => plain("fail"),
    }
}

async fn epay_return(
    State(state): State<BillingHttpState>,
    method: Method,
    query: RawQuery,
    body: Bytes,
) -> Response {
    if !payment_compliance_allowed(&state).await {
        return redirect(&state.config.wallet_url, "fail");
    }
    let raw = match callback_body(&body) {
        Ok(raw) => raw,
        Err(()) => return redirect(&state.config.wallet_url, "fail"),
    };
    let fields = epay_callback_fields(&method, query.0.as_deref(), raw);
    if fields.is_empty() {
        return redirect(&state.config.wallet_url, "fail");
    }
    let result = match state.epay.verify(&fields).await {
        Ok(value) if value.verified => value,
        _ => return redirect(&state.config.wallet_url, "fail"),
    };
    if !result.trade_success {
        return redirect(&state.config.wallet_url, "pending");
    }
    match finish(
        &state,
        &result.trade_no,
        EPAY,
        raw,
        Some(&result.payment_method),
    )
    .await
    {
        Ok(_) => redirect(&state.config.wallet_url, "success"),
        Err(_) => redirect(&state.config.wallet_url, "fail"),
    }
}

async fn stripe_webhook(
    State(state): State<BillingHttpState>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    if !payment_compliance_allowed(&state).await {
        return StatusCode::FORBIDDEN.into_response();
    }
    let Some(signature) = header_text(&headers, "stripe-signature") else {
        return StatusCode::BAD_REQUEST.into_response();
    };
    let raw = match callback_body(&body) {
        Ok(raw) => raw,
        Err(()) => return StatusCode::BAD_REQUEST.into_response(),
    };
    let event = match state.stripe.verify(&body, &signature).await {
        Ok(event) => event,
        Err(_) => return StatusCode::BAD_REQUEST.into_response(),
    };
    let result = match event.kind.as_str() {
        "checkout.session.completed" if event.complete && event.paid => {
            finish(&state, &event.trade_no, STRIPE, raw, None).await
        }
        "checkout.session.async_payment_succeeded" if event.paid => {
            finish(&state, &event.trade_no, STRIPE, raw, None).await
        }
        "checkout.session.expired" => state
            .repository
            .expire(&event.trade_no)
            .await
            .map(|_| Completion::AlreadySucceeded),
        _ => Ok(Completion::AlreadySucceeded),
    };
    if result.is_ok() {
        StatusCode::OK.into_response()
    } else {
        StatusCode::INTERNAL_SERVER_ERROR.into_response()
    }
}

async fn creem_webhook(
    State(state): State<BillingHttpState>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    if !payment_compliance_allowed(&state).await {
        return StatusCode::FORBIDDEN.into_response();
    }
    let Some(signature) = header_text(&headers, "creem-signature") else {
        return StatusCode::UNAUTHORIZED.into_response();
    };
    if state.config.creem_webhook_secret.is_empty()
        || !hmac_sha256_valid(&body, &signature, &state.config.creem_webhook_secret)
    {
        return StatusCode::UNAUTHORIZED.into_response();
    }
    let raw = match callback_body(&body) {
        Ok(raw) => raw,
        Err(()) => return StatusCode::BAD_REQUEST.into_response(),
    };
    let Ok(event) = serde_json::from_str::<Value>(raw) else {
        return StatusCode::BAD_REQUEST.into_response();
    };
    let event_type = event.get("eventType").and_then(Value::as_str);
    let paid = event
        .pointer("/object/order/status")
        .and_then(Value::as_str)
        == Some("paid");
    if event_type != Some("checkout.completed") || !paid {
        return StatusCode::OK.into_response();
    }
    let Some(trade_no) = event
        .pointer("/object/request_id")
        .and_then(Value::as_str)
        .filter(|value| !value.is_empty())
    else {
        return StatusCode::BAD_REQUEST.into_response();
    };
    match finish(&state, trade_no, CREEM, raw, None).await {
        Ok(_) => StatusCode::OK.into_response(),
        Err(_) => StatusCode::INTERNAL_SERVER_ERROR.into_response(),
    }
}

async fn finish(
    state: &BillingHttpState,
    trade_no: &str,
    provider: &str,
    payload: &str,
    method: Option<&str>,
) -> Result<Completion, BillingError> {
    finish_with(
        state.repository.as_ref(),
        state.cache.as_ref(),
        trade_no,
        provider,
        payload,
        method,
    )
    .await
}

async fn finish_with(
    repository: &dyn BillingRepository,
    cache: &dyn BillingCache,
    trade_no: &str,
    provider: &str,
    payload: &str,
    method: Option<&str>,
) -> Result<Completion, BillingError> {
    let result = repository
        .complete(trade_no, provider, payload, method)
        .await?;
    if let Completion::Completed {
        subscription_id,
        user_id,
        quota_charged,
        group_changed,
    } = result
    {
        cache
            .invalidate_completed_payment(subscription_id, user_id, quota_charged, group_changed)
            .await;
    }
    Ok(result)
}

/// ePay uses `Request.PostForm` for POST callbacks and `URL.Query` for GET
/// callbacks.  In particular, POST must never fall back to a query string:
/// doing so could let malformed JSON complete an order from attacker-controlled
/// URL parameters.
fn epay_callback_fields(
    method: &Method,
    query: Option<&str>,
    raw: &str,
) -> BTreeMap<String, String> {
    if *method == Method::GET {
        query.map_or_else(BTreeMap::new, parse_form)
    } else {
        parse_form(raw)
    }
}

fn callback_body(body: &[u8]) -> Result<&str, ()> {
    std::str::from_utf8(body).map_err(|_| ())
}

fn parse_form(raw: &str) -> BTreeMap<String, String> {
    raw.split('&')
        .filter_map(|pair| {
            pair.split_once('=')
                .map(|(key, value)| (percent_decode(key), percent_decode(value)))
        })
        .collect()
}
fn percent_decode(value: &str) -> String {
    let mut bytes = Vec::with_capacity(value.len());
    let input = value.as_bytes();
    let mut index = 0;
    while index < input.len() {
        match input[index] {
            b'+' => bytes.push(b' '),
            b'%' if index + 2 < input.len() => {
                if let Ok(hex) = u8::from_str_radix(&value[index + 1..index + 3], 16) {
                    bytes.push(hex);
                    index += 2;
                } else {
                    bytes.push(input[index]);
                }
            }
            byte => bytes.push(byte),
        };
        index += 1;
    }
    String::from_utf8_lossy(&bytes).into_owned()
}

fn hmac_sha256_valid(raw: &[u8], provided: &str, secret: &str) -> bool {
    let Ok(mut mac) = Hmac::<Sha256>::new_from_slice(secret.as_bytes()) else {
        return false;
    };
    mac.update(raw);
    let expected = hex(&mac.finalize().into_bytes());
    expected
        .as_bytes()
        .ct_eq(provided.to_ascii_lowercase().as_bytes())
        .into()
}
fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

// Legacy digest helper retained for compatibility with historical fixtures.
pub fn md5_hex(input: &[u8]) -> String {
    let mut data = input.to_vec();
    let bits = (data.len() as u64) * 8;
    data.push(0x80);
    while data.len() % 64 != 56 {
        data.push(0);
    }
    data.extend_from_slice(&bits.to_le_bytes());
    let mut state = [0x6745_2301u32, 0xefcd_ab89, 0x98ba_dcfe, 0x1032_5476];
    for chunk in data.chunks_exact(64) {
        md5_block(&mut state, chunk);
    }
    let mut output = Vec::with_capacity(16);
    for word in state {
        output.extend_from_slice(&word.to_le_bytes());
    }
    hex(&output)
}
fn md5_block(state: &mut [u32; 4], chunk: &[u8]) {
    const S: [u32; 64] = [
        7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22, 7, 12, 17, 22, 5, 9, 14, 20, 5, 9, 14, 20, 5,
        9, 14, 20, 5, 9, 14, 20, 4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23, 4, 11, 16, 23, 6, 10,
        15, 21, 6, 10, 15, 21, 6, 10, 15, 21, 6, 10, 15, 21,
    ];
    const K: [u32; 64] = [
        0xd76aa478, 0xe8c7b756, 0x242070db, 0xc1bdceee, 0xf57c0faf, 0x4787c62a, 0xa8304613,
        0xfd469501, 0x698098d8, 0x8b44f7af, 0xffff5bb1, 0x895cd7be, 0x6b901122, 0xfd987193,
        0xa679438e, 0x49b40821, 0xf61e2562, 0xc040b340, 0x265e5a51, 0xe9b6c7aa, 0xd62f105d,
        0x02441453, 0xd8a1e681, 0xe7d3fbc8, 0x21e1cde6, 0xc33707d6, 0xf4d50d87, 0x455a14ed,
        0xa9e3e905, 0xfcefa3f8, 0x676f02d9, 0x8d2a4c8a, 0xfffa3942, 0x8771f681, 0x6d9d6122,
        0xfde5380c, 0xa4beea44, 0x4bdecfa9, 0xf6bb4b60, 0xbebfbc70, 0x289b7ec6, 0xeaa127fa,
        0xd4ef3085, 0x04881d05, 0xd9d4d039, 0xe6db99e5, 0x1fa27cf8, 0xc4ac5665, 0xf4292244,
        0x432aff97, 0xab9423a7, 0xfc93a039, 0x655b59c3, 0x8f0ccc92, 0xffeff47d, 0x85845dd1,
        0x6fa87e4f, 0xfe2ce6e0, 0xa3014314, 0x4e0811a1, 0xf7537e82, 0xbd3af235, 0x2ad7d2bb,
        0xeb86d391,
    ];
    let mut words = [0u32; 16];
    for (index, word) in words.iter_mut().enumerate() {
        let offset = index * 4;
        *word = u32::from_le_bytes([
            chunk[offset],
            chunk[offset + 1],
            chunk[offset + 2],
            chunk[offset + 3],
        ]);
    }
    let (mut a, mut b, mut c, mut d) = (state[0], state[1], state[2], state[3]);
    for index in 0..64 {
        let (f, g) = match index {
            0..=15 => ((b & c) | (!b & d), index),
            16..=31 => ((d & b) | (!d & c), (5 * index + 1) % 16),
            32..=47 => (b ^ c ^ d, (3 * index + 5) % 16),
            _ => (c ^ (b | !d), (7 * index) % 16),
        };
        let next = b.wrapping_add(
            (a.wrapping_add(f)
                .wrapping_add(K[index])
                .wrapping_add(words[g]))
            .rotate_left(S[index]),
        );
        a = d;
        d = c;
        c = b;
        b = next;
    }
    state[0] = state[0].wrapping_add(a);
    state[1] = state[1].wrapping_add(b);
    state[2] = state[2].wrapping_add(c);
    state[3] = state[3].wrapping_add(d);
}

fn plain(body: &'static str) -> Response {
    (
        StatusCode::OK,
        [(header::CONTENT_TYPE, "text/plain; charset=utf-8")],
        body,
    )
        .into_response()
}
fn redirect(wallet: &str, result: &str) -> Response {
    let separator = if wallet.contains('?') { '&' } else { '?' };
    let location = format!("{wallet}{separator}pay={result}");
    let mut response = StatusCode::FOUND.into_response();
    if let Ok(value) = HeaderValue::from_str(&location) {
        response.headers_mut().insert(header::LOCATION, value);
    }
    response
}
fn payment_error(status: StatusCode, message: &'static str) -> Response {
    (status, Json(json!({"success": false, "message": message}))).into_response()
}
fn header_text(headers: &HeaderMap, name: &str) -> Option<String> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .map(str::to_owned)
}

/// PostgreSQL implementation. `complete` uses an order row lock, so duplicate
/// callbacks are idempotent across application processes without a cache lock.
pub struct PgBillingRepository {
    pg: PgPool,
}
impl PgBillingRepository {
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self { pg }
    }

    /// Legacy balance purchase: all ledger, subscription, group, and order
    /// mutations commit together or not at all. This is deliberately private
    /// until the route port is connected in the same atomic change.
    async fn purchase_with_balance(
        &self,
        user_id: i64,
        plan_id: i64,
        quota_per_unit: i64,
    ) -> Result<Completion, BillingError> {
        if user_id <= 0 || plan_id <= 0 || quota_per_unit <= 0 {
            return Err(BillingError::Rejected);
        }
        // Go's `common.QuotaPerUnit` is refreshed when the persisted option is
        // changed. Read the same authoritative option for every purchase so a
        // long-lived Rust listener cannot silently charge with a stale unit.
        // An absent option retains Go's 500k/default supplied by the listener;
        // malformed configured values fail closed before the wallet is locked.
        let quota_per_unit = configured_quota_per_unit(&self.pg, quota_per_unit).await?;
        let mut tx = self.pg.begin().await.map_err(|_| BillingError::Storage)?;
        let plan = sqlx::query("SELECT price_amount::text AS money, enabled, COALESCE(allow_balance_pay, TRUE) allow_balance_pay, COALESCE(max_purchase_per_user, 0) max_purchase_per_user, COALESCE(total_amount, 0) total_amount, COALESCE(duration_unit, 'month') duration_unit, COALESCE(duration_value, 1) duration_value, COALESCE(custom_seconds, 0) custom_seconds, COALESCE(upgrade_group, '') upgrade_group, COALESCE(downgrade_group, '') downgrade_group, COALESCE(allow_wallet_overflow, TRUE) allow_wallet_overflow FROM subscription_plans WHERE id = $1 FOR SHARE")
            .bind(plan_id)
            .fetch_optional(&mut *tx)
            .await
            .map_err(|_| BillingError::Storage)?
            .ok_or(BillingError::Rejected)?;
        let money: String = plan.try_get("money").map_err(|_| BillingError::Storage)?;
        let enabled: bool = plan.try_get("enabled").map_err(|_| BillingError::Storage)?;
        let allows_balance: bool = plan
            .try_get("allow_balance_pay")
            .map_err(|_| BillingError::Storage)?;
        if !enabled || !allows_balance {
            return Err(BillingError::Rejected);
        }
        let charge = balance_charge_decimal(&money, &quota_per_unit)?;
        let maximum: i64 = plan
            .try_get("max_purchase_per_user")
            .map_err(|_| BillingError::Storage)?;
        if maximum > 0 {
            let count: i64 = sqlx::query_scalar(
                "SELECT count(*) FROM user_subscriptions WHERE user_id = $1 AND plan_id = $2",
            )
            .bind(user_id)
            .bind(plan_id)
            .fetch_one(&mut *tx)
            .await
            .map_err(|_| BillingError::Storage)?;
            if count >= maximum {
                return Err(BillingError::Rejected);
            }
        }
        let user = sqlx::query(
            "SELECT quota, \"group\" FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
        )
        .bind(user_id)
        .fetch_optional(&mut *tx)
        .await
        .map_err(|_| BillingError::Storage)?
        .ok_or(BillingError::Rejected)?;
        let quota: i64 = user.try_get("quota").map_err(|_| BillingError::Storage)?;
        if quota < charge {
            return Err(BillingError::Rejected);
        }
        if charge > 0 {
            sqlx::query("UPDATE users SET quota = quota - $2 WHERE id = $1")
                .bind(user_id)
                .bind(charge)
                .execute(&mut *tx)
                .await
                .map_err(|_| BillingError::Storage)?;
        }
        let now = epoch_seconds()?;
        let current_group: String = user.try_get("group").map_err(|_| BillingError::Storage)?;
        let upgrade_group: String = plan
            .try_get("upgrade_group")
            .map_err(|_| BillingError::Storage)?;
        let group_changed = !upgrade_group.is_empty() && upgrade_group != current_group;
        if group_changed {
            sqlx::query("UPDATE users SET \"group\" = $2 WHERE id = $1")
                .bind(user_id)
                .bind(&upgrade_group)
                .execute(&mut *tx)
                .await
                .map_err(|_| BillingError::Storage)?;
        }
        let total: i64 = plan
            .try_get("total_amount")
            .map_err(|_| BillingError::Storage)?;
        let duration = duration_seconds(&plan)?;
        let subscription_id: i64 = sqlx::query_scalar("INSERT INTO user_subscriptions (user_id,plan_id,amount_total,amount_used,start_time,end_time,status,source,last_reset_time,next_reset_time,upgrade_group,prev_user_group,downgrade_group,allow_wallet_overflow,created_at,updated_at) VALUES ($1,$2,$3,0,$4,$5,'active','balance',0,0,$6,$7,$8,$9,$4,$4) RETURNING id")
            .bind(user_id)
            .bind(plan_id)
            .bind(total)
            .bind(now)
            .bind(now.saturating_add(duration))
            .bind(&upgrade_group)
            .bind(if group_changed { &current_group } else { "" })
            .bind(plan.try_get::<String, _>("downgrade_group").map_err(|_| BillingError::Storage)?)
            .bind(plan.try_get::<bool, _>("allow_wallet_overflow").map_err(|_| BillingError::Storage)?)
            .fetch_one(&mut *tx)
            .await
            .map_err(|_| BillingError::Storage)?;
        let trade_no = format!("SUBBALUSR{user_id}NO{}", Uuid::new_v4().simple());
        sqlx::query("INSERT INTO subscription_orders (user_id, plan_id, money, trade_no, payment_method, payment_provider, status, create_time, complete_time, provider_payload) VALUES ($1,$2,CAST($3 AS NUMERIC),$4,'balance','balance','success',$5,$5,$6)")
            .bind(user_id)
            .bind(plan_id)
            .bind(&money)
            .bind(&trade_no)
            .bind(now)
            .bind(format!("charged_quota={charge}"))
            .execute(&mut *tx)
            .await
            .map_err(|_| BillingError::Storage)?;
        tx.commit().await.map_err(|_| BillingError::Storage)?;
        Ok(Completion::Completed {
            subscription_id,
            user_id,
            quota_charged: charge,
            group_changed,
        })
    }
}
#[async_trait]
impl BillingRepository for PgBillingRepository {
    async fn create_pending(&self, input: CreateOrder) -> Result<PendingOrder, BillingError> {
        let mut tx = self.pg.begin().await.map_err(|_| BillingError::Storage)?;
        let plan = sqlx::query("SELECT price_amount::text AS money, COALESCE(NULLIF(currency, ''), 'USD') currency, enabled, COALESCE(max_purchase_per_user, 0) max_purchase_per_user FROM subscription_plans WHERE id = $1 FOR SHARE")
            .bind(input.plan_id)
            .fetch_optional(&mut *tx)
            .await
            .map_err(|_| BillingError::Storage)?
            .ok_or(BillingError::Rejected)?;
        let enabled: bool = plan.try_get("enabled").map_err(|_| BillingError::Storage)?;
        let money: String = plan.try_get("money").map_err(|_| BillingError::Storage)?;
        let currency: String = plan
            .try_get("currency")
            .map_err(|_| BillingError::Storage)?;
        let maximum: i64 = plan
            .try_get("max_purchase_per_user")
            .map_err(|_| BillingError::Storage)?;
        if !enabled || !payment_amount_is_valid(&money) {
            return Err(BillingError::Rejected);
        }
        let user_exists: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL)",
        )
        .bind(input.user_id)
        .fetch_one(&mut *tx)
        .await
        .map_err(|_| BillingError::Storage)?;
        if !user_exists {
            return Err(BillingError::Rejected);
        }
        if maximum > 0 {
            let count: i64 = sqlx::query_scalar(
                "SELECT count(*) FROM user_subscriptions WHERE user_id = $1 AND plan_id = $2",
            )
            .bind(input.user_id)
            .bind(input.plan_id)
            .fetch_one(&mut *tx)
            .await
            .map_err(|_| BillingError::Storage)?;
            if count >= maximum {
                return Err(BillingError::Rejected);
            }
        }
        let trade_no = format!("sub_{}", Uuid::new_v4().simple());
        let now = epoch_seconds()?;
        sqlx::query("INSERT INTO subscription_orders (user_id, plan_id, money, trade_no, payment_method, payment_provider, status, create_time) VALUES ($1,$2,CAST($3 AS NUMERIC),$4,$5,$6,'pending',$7)").bind(input.user_id).bind(input.plan_id).bind(&money).bind(&trade_no).bind(&input.payment_method).bind(input.provider).bind(now).execute(&mut *tx).await.map_err(|_| BillingError::Storage)?;
        tx.commit().await.map_err(|_| BillingError::Storage)?;
        Ok(PendingOrder {
            trade_no,
            plan_id: input.plan_id,
            user_id: input.user_id,
            money,
            currency,
            payment_method: input.payment_method,
            provider: input.provider.to_owned(),
        })
    }
    async fn expire(&self, trade_no: &str) -> Result<(), BillingError> {
        sqlx::query("UPDATE subscription_orders SET status = 'expired', complete_time = $2 WHERE trade_no = $1 AND status = 'pending'").bind(trade_no).bind(epoch_seconds()?).execute(&self.pg).await.map_err(|_| BillingError::Storage)?;
        Ok(())
    }
    async fn fail(&self, trade_no: &str) -> Result<(), BillingError> {
        sqlx::query("UPDATE subscription_orders SET status = 'failed', complete_time = $2 WHERE trade_no = $1 AND status = 'pending'")
            .bind(trade_no)
            .bind(epoch_seconds()?)
            .execute(&self.pg)
            .await
            .map_err(|_| BillingError::Storage)?;
        Ok(())
    }
    async fn purchase_with_balance(
        &self,
        user_id: i64,
        plan_id: i64,
        quota_per_unit: i64,
    ) -> Result<Completion, BillingError> {
        PgBillingRepository::purchase_with_balance(self, user_id, plan_id, quota_per_unit).await
    }
    async fn complete(
        &self,
        trade_no: &str,
        provider: &str,
        payload: &str,
        method: Option<&str>,
    ) -> Result<Completion, BillingError> {
        let mut tx = self.pg.begin().await.map_err(|_| BillingError::Storage)?;
        let row = sqlx::query("SELECT o.user_id,o.plan_id,o.money::text AS money,o.status,o.payment_provider,p.total_amount,p.duration_unit,p.duration_value,p.custom_seconds,p.upgrade_group,p.downgrade_group,p.allow_wallet_overflow,p.quota_reset_period,p.quota_reset_custom_seconds,p.max_purchase_per_user FROM subscription_orders o JOIN subscription_plans p ON p.id=o.plan_id WHERE o.trade_no=$1 FOR UPDATE")
            .bind(trade_no)
            .fetch_optional(&mut *tx)
            .await
            .map_err(|_| BillingError::Storage)?
            .ok_or(BillingError::Rejected)?;
        let actual_provider: String = row
            .try_get("payment_provider")
            .map_err(|_| BillingError::Storage)?;
        let status: String = row.try_get("status").map_err(|_| BillingError::Storage)?;
        if actual_provider != provider {
            return Err(BillingError::Rejected);
        }
        if status == "success" {
            tx.commit().await.map_err(|_| BillingError::Storage)?;
            return Ok(Completion::AlreadySucceeded);
        }
        if status != "pending" {
            return Err(BillingError::Rejected);
        }
        let now = epoch_seconds()?;
        let user_id: i64 = row.try_get("user_id").map_err(|_| BillingError::Storage)?;
        let plan_id: i64 = row.try_get("plan_id").map_err(|_| BillingError::Storage)?;
        let user = sqlx::query(
            "SELECT \"group\" FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
        )
        .bind(user_id)
        .fetch_optional(&mut *tx)
        .await
        .map_err(|_| BillingError::Storage)?
        .ok_or(BillingError::Rejected)?;
        let maximum: i64 = row
            .try_get("max_purchase_per_user")
            .map_err(|_| BillingError::Storage)?;
        if maximum > 0 {
            let count: i64 = sqlx::query_scalar(
                "SELECT count(*) FROM user_subscriptions WHERE user_id = $1 AND plan_id = $2",
            )
            .bind(user_id)
            .bind(plan_id)
            .fetch_one(&mut *tx)
            .await
            .map_err(|_| BillingError::Storage)?;
            if count >= maximum {
                return Err(BillingError::Rejected);
            }
        }
        let total: i64 = row
            .try_get("total_amount")
            .map_err(|_| BillingError::Storage)?;
        let duration: i64 = duration_seconds(&row)?;
        let current_group: String = user.try_get("group").map_err(|_| BillingError::Storage)?;
        let upgrade_group: String = row
            .try_get("upgrade_group")
            .map_err(|_| BillingError::Storage)?;
        let group_changed = !upgrade_group.is_empty() && upgrade_group != current_group;
        if group_changed {
            sqlx::query("UPDATE users SET \"group\" = $2 WHERE id = $1")
                .bind(user_id)
                .bind(&upgrade_group)
                .execute(&mut *tx)
                .await
                .map_err(|_| BillingError::Storage)?;
        }
        let next_reset = next_reset_time(now, now.saturating_add(duration), &row)?;
        let last_reset = if next_reset > 0 { now } else { 0 };
        let downgrade_group: String = row
            .try_get("downgrade_group")
            .map_err(|_| BillingError::Storage)?;
        let allow_wallet_overflow: bool = row
            .try_get("allow_wallet_overflow")
            .map_err(|_| BillingError::Storage)?;
        let subscription_id: i64 = sqlx::query_scalar("INSERT INTO user_subscriptions (user_id,plan_id,amount_total,amount_used,start_time,end_time,status,source,last_reset_time,next_reset_time,upgrade_group,prev_user_group,downgrade_group,allow_wallet_overflow,created_at,updated_at) VALUES ($1,$2,$3,0,$4,$5,'active','order',$6,$7,$8,$9,$10,$11,$4,$4) RETURNING id")
            .bind(user_id)
            .bind(plan_id)
            .bind(total)
            .bind(now)
            .bind(now.saturating_add(duration))
            .bind(last_reset)
            .bind(next_reset)
            .bind(&upgrade_group)
            .bind(if group_changed { &current_group } else { "" })
            .bind(&downgrade_group)
            .bind(allow_wallet_overflow)
            .fetch_one(&mut *tx)
            .await
            .map_err(|_| BillingError::Storage)?;
        let method = method.filter(|method| !method.is_empty());
        sqlx::query("UPDATE subscription_orders SET status='success', complete_time=$2, provider_payload=$3, payment_method=COALESCE($4,payment_method) WHERE trade_no=$1")
            .bind(trade_no)
            .bind(now)
            .bind(payload)
            .bind(method)
            .execute(&mut *tx)
            .await
            .map_err(|_| BillingError::Storage)?;
        let existing_method = sqlx::query_scalar::<_, String>(
            "SELECT payment_method FROM top_ups WHERE trade_no = $1 FOR UPDATE",
        )
        .bind(trade_no)
        .fetch_optional(&mut *tx)
        .await
        .map_err(|_| BillingError::Storage)?;
        if existing_method.as_deref().is_some_and(|existing| {
            !existing.is_empty() && method.is_some_and(|actual| actual != existing)
        }) {
            return Err(BillingError::Rejected);
        }
        let updated = sqlx::query("UPDATE top_ups SET status='success', complete_time=$2, payment_method=CASE WHEN payment_method='' THEN COALESCE($3, payment_method) ELSE payment_method END, payment_provider=$4 WHERE trade_no=$1")
            .bind(trade_no)
            .bind(now)
            .bind(method)
            .bind(provider)
            .execute(&mut *tx)
            .await
            .map_err(|_| BillingError::Storage)?
            .rows_affected();
        if updated == 0 {
            sqlx::query("INSERT INTO top_ups (user_id,amount,money,trade_no,payment_method,payment_provider,create_time,complete_time,status) VALUES ($1,0,CAST($2 AS NUMERIC),$3,$4,$5,$6,$6,'success')").bind(user_id).bind(row.try_get::<String,_>("money").map_err(|_| BillingError::Storage)?).bind(trade_no).bind(method.unwrap_or_default()).bind(provider).bind(now).execute(&mut *tx).await.map_err(|_| BillingError::Storage)?;
        }
        tx.commit().await.map_err(|_| BillingError::Storage)?;
        Ok(Completion::Completed {
            subscription_id,
            user_id,
            quota_charged: 0,
            group_changed,
        })
    }
}
fn epoch_seconds() -> Result<i64, BillingError> {
    i64::try_from(
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|_| BillingError::Storage)?
            .as_secs(),
    )
    .map_err(|_| BillingError::Storage)
}

/// Reject malformed values and values below the legacy one-cent checkout floor
/// without converting a PostgreSQL `NUMERIC` through binary floating point.
fn payment_amount_is_valid(money: &str) -> bool {
    let money = money.trim();
    if money.is_empty() || money.starts_with('-') {
        return false;
    }
    let (whole, fraction) = money.split_once('.').unwrap_or((money, ""));
    if whole.is_empty()
        || !whole.bytes().all(|byte| byte.is_ascii_digit())
        || !fraction.bytes().all(|byte| byte.is_ascii_digit())
        || fraction.len() > 18
    {
        return false;
    }
    let Ok(whole) = whole.parse::<i128>() else {
        return false;
    };
    let fraction = format!("{fraction:0<18}");
    let Ok(fraction) = fraction.parse::<i128>() else {
        return false;
    };
    whole
        .checked_mul(1_000_000_000_000_000_000)
        .and_then(|value| value.checked_add(fraction))
        .is_some_and(|value| value >= 10_000_000_000_000_000)
}

fn next_reset_time(start: i64, end: i64, row: &sqlx::postgres::PgRow) -> Result<i64, BillingError> {
    let period: String = row
        .try_get("quota_reset_period")
        .map_err(|_| BillingError::Storage)?;
    let custom_seconds: i64 = row
        .try_get("quota_reset_custom_seconds")
        .map_err(|_| BillingError::Storage)?;
    let seconds = match period.as_str() {
        "daily" => 86_400,
        "weekly" => 7 * 86_400,
        "monthly" => 30 * 86_400,
        "custom" if custom_seconds > 0 => custom_seconds,
        "never" | "" => 0,
        _ => return Err(BillingError::Rejected),
    };
    let Some(next) = start.checked_add(seconds) else {
        return Err(BillingError::Rejected);
    };
    Ok(if seconds > 0 && next <= end { next } else { 0 })
}

fn duration_seconds(row: &sqlx::postgres::PgRow) -> Result<i64, BillingError> {
    let unit: String = row
        .try_get("duration_unit")
        .map_err(|_| BillingError::Storage)?;
    let value: i64 = row
        .try_get("duration_value")
        .map_err(|_| BillingError::Storage)?;
    let custom: i64 = row
        .try_get("custom_seconds")
        .map_err(|_| BillingError::Storage)?;
    match unit.as_str() {
        "year" => value.checked_mul(365 * 86_400),
        "month" => value.checked_mul(30 * 86_400),
        "day" => value.checked_mul(86_400),
        "hour" => value.checked_mul(3_600),
        "custom" => Some(custom),
        _ => None,
    }
    .filter(|seconds| *seconds > 0)
    .ok_or(BillingError::Rejected)
}

/// Reads the live Go `common.QuotaPerUnit` option. The Go process uses its
/// default when the option is absent, while an explicitly malformed option
/// becomes zero and consequently rejects balance purchases. Preserve that
/// distinction here instead of silently replacing a bad setting with a safe-
/// looking but financially different default.
async fn configured_quota_per_unit(pg: &PgPool, fallback: i64) -> Result<String, BillingError> {
    let configured = sqlx::query_scalar::<_, String>(
        "SELECT COALESCE(value, '') FROM options WHERE key = 'QuotaPerUnit'",
    )
    .fetch_optional(pg)
    .await;
    let configured = match configured {
        Ok(value) => value,
        // Older isolated billing fixtures intentionally omit the unrelated
        // options table. The deployed schema always has it; retaining the
        // constructor fallback here keeps those fixtures meaningful without
        // masking transport/storage errors from an existing table.
        Err(sqlx::Error::Database(error)) if error.code().as_deref() == Some("42P01") => None,
        Err(_) => return Err(BillingError::Storage),
    };
    Ok(configured.unwrap_or_else(|| fallback.to_string()))
}

fn decimal_units(value: &str) -> Result<(i128, i128), BillingError> {
    let value = value.trim();
    if value.starts_with('-') || value.is_empty() {
        return Err(BillingError::Rejected);
    }
    let (whole, fraction) = value.split_once('.').unwrap_or((value, ""));
    if whole.is_empty()
        || !whole.bytes().all(|byte| byte.is_ascii_digit())
        || !fraction.bytes().all(|byte| byte.is_ascii_digit())
        || fraction.len() > 18
    {
        return Err(BillingError::Rejected);
    }
    let scale = 10_i128
        .checked_pow(fraction.len() as u32)
        .ok_or(BillingError::Rejected)?;
    let whole = whole.parse::<i128>().map_err(|_| BillingError::Rejected)?;
    let fraction = if fraction.is_empty() {
        0
    } else {
        fraction
            .parse::<i128>()
            .map_err(|_| BillingError::Rejected)?
    };
    let units = whole
        .checked_mul(scale)
        .and_then(|value| value.checked_add(fraction))
        .ok_or(BillingError::Rejected)?;
    Ok((units, scale))
}

fn balance_charge_decimal(money: &str, quota_per_unit: &str) -> Result<i64, BillingError> {
    let (money_units, money_scale) = decimal_units(money)?;
    let (quota_units, quota_scale) = decimal_units(quota_per_unit)?;
    if quota_units <= 0 {
        return Err(BillingError::Rejected);
    }
    let numerator = money_units
        .checked_mul(quota_units)
        .ok_or(BillingError::Rejected)?;
    let denominator = money_scale
        .checked_mul(quota_scale)
        .ok_or(BillingError::Rejected)?;
    let charge = numerator
        .checked_add(denominator - 1)
        .ok_or(BillingError::Rejected)?
        / denominator;
    i64::try_from(charge).map_err(|_| BillingError::Rejected)
}
