//! Legacy user Stripe and Creem top-up endpoints.
//!
//! The handlers own legacy validation and response envelopes only.  Identity,
//! persistence, and provider I/O are injected boundaries: an incomplete
//! listener therefore fails closed and cannot accidentally create a checkout.

use std::{
    collections::BTreeMap,
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{Request, State},
    http::{HeaderMap, StatusCode, header},
    response::{IntoResponse, Response},
    routing::post,
};
use secrecy::{ExposeSecret, SecretString};
use serde::{Deserialize, Serialize, de::DeserializeOwned};
use serde_json::{Value, json};
use sqlx::{PgPool, Row};
use uuid::Uuid;

use crate::auth::{
    AuthErrorKind, DashboardAuth, UserAuthPolicyError, enforce_user_auth, user_auth_message,
};

const STRIPE: &str = "stripe";
const CREEM: &str = "creem";
const MAX_LEGACY_TOPUP_BODY_BYTES: usize = 1024 * 1024;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct TopupPrincipal {
    pub user_id: i64,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum TopupAuthError {
    Unauthorized,
    TokenExpired,
    SessionRevoked,
    Unavailable,
    UserAuth(UserAuthPolicyError),
}

#[async_trait]
pub trait StripeCreemAuthorizer: Send + Sync {
    async fn principal(&self, headers: &HeaderMap) -> Result<TopupPrincipal, TopupAuthError>;
}

/// Production adapter deriving the actor solely from a verified dashboard
/// session, never from request JSON or an arbitrary user-id header.
#[derive(Clone)]
pub struct DashboardStripeCreemAuthorizer {
    auth: Arc<dyn DashboardAuth>,
}

impl DashboardStripeCreemAuthorizer {
    #[must_use]
    pub fn new(auth: Arc<dyn DashboardAuth>) -> Self {
        Self { auth }
    }
}

#[async_trait]
impl StripeCreemAuthorizer for DashboardStripeCreemAuthorizer {
    async fn principal(&self, headers: &HeaderMap) -> Result<TopupPrincipal, TopupAuthError> {
        let token = bearer(headers).ok_or(TopupAuthError::Unauthorized)?;
        match self
            .auth
            .self_user(SecretString::from(token.to_owned()))
            .await
        {
            Ok(user) => enforce_user_auth(&user)
                .map(|()| TopupPrincipal { user_id: user.id })
                .map_err(TopupAuthError::UserAuth),
            Err(error) if error.kind == AuthErrorKind::Unauthorized => {
                Err(TopupAuthError::Unauthorized)
            }
            Err(error) if error.kind == AuthErrorKind::TokenExpired => {
                Err(TopupAuthError::TokenExpired)
            }
            Err(error) if error.kind == AuthErrorKind::SessionRevoked => {
                Err(TopupAuthError::SessionRevoked)
            }
            Err(error) if error.kind == AuthErrorKind::UserDisabled => {
                Err(TopupAuthError::UserAuth(UserAuthPolicyError::UserDisabled))
            }
            Err(_) => Err(TopupAuthError::Unavailable),
        }
    }
}

fn bearer(headers: &HeaderMap) -> Option<&str> {
    let value = headers.get(header::AUTHORIZATION)?.to_str().ok()?.trim();
    let mut fields = value.split_whitespace();
    let first = fields.next()?;
    let second = fields.next();
    if fields.next().is_some() {
        return None;
    }
    match second {
        Some(token) if first.eq_ignore_ascii_case("bearer") && !token.is_empty() => Some(token),
        None if !first.is_empty() => Some(first),
        _ => None,
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct TopupUser {
    pub group: String,
    pub email: String,
    pub username: String,
    pub stripe_customer: String,
}

#[derive(Clone, Debug, PartialEq)]
pub struct StripeSettings {
    pub min_topup: i64,
    pub unit_price: f64,
    pub quota_display_type: String,
    /// Mirrors Go's `common.QuotaPerUnit`, which is a float and may be
    /// configured independently of the display type.
    pub quota_per_unit: f64,
    pub group_ratios: BTreeMap<String, f64>,
    pub amount_discounts: BTreeMap<i64, f64>,
    pub trusted_redirect_domains: Vec<String>,
}

impl Default for StripeSettings {
    fn default() -> Self {
        Self {
            min_topup: 1,
            unit_price: 8.0,
            quota_display_type: "USD".to_owned(),
            quota_per_unit: 500_000.0,
            group_ratios: BTreeMap::new(),
            amount_discounts: BTreeMap::new(),
            trusted_redirect_domains: Vec::new(),
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct CreemProduct {
    pub product_id: String,
    pub name: String,
    pub price: f64,
    pub currency: String,
    pub quota: i64,
}

#[derive(Default, Deserialize)]
struct RawCreemProduct {
    #[serde(
        default,
        rename = "productId",
        deserialize_with = "null_string_is_empty"
    )]
    product_id: String,
    #[serde(default, deserialize_with = "null_string_is_empty")]
    name: String,
    #[serde(default, deserialize_with = "null_f64_is_zero")]
    price: f64,
    #[serde(default, deserialize_with = "null_string_is_empty")]
    currency: String,
    #[serde(default, deserialize_with = "null_i64_is_zero")]
    quota: i64,
}

fn parse_creem_products(raw: &str) -> Result<Vec<CreemProduct>, TopupStoreError> {
    // Go's json.Unmarshal accepts a top-level JSON null into a slice and then
    // reports the requested product as absent. Missing or scalar configuration
    // remains a configuration error.
    serde_json::from_str::<Option<Vec<RawCreemProduct>>>(raw)
        .map_err(|_| TopupStoreError::Unavailable)
        .map(|products| {
            products
                .unwrap_or_default()
                .into_iter()
                .map(|product| CreemProduct {
                    product_id: product.product_id,
                    name: product.name,
                    price: product.price,
                    currency: product.currency,
                    quota: product.quota,
                })
                .collect()
        })
}

#[derive(Clone, Debug, PartialEq)]
pub struct PendingTopup {
    pub trade_no: String,
    pub user_id: i64,
    pub amount: i64,
    /// Keep the Go `float64` value intact; PostgreSQL owns any numeric-column
    /// conversion rather than a pre-insert display rounding step.
    pub money: f64,
    pub payment_method: String,
    pub payment_provider: String,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum TopupStoreError {
    Unavailable,
    NotFound,
    Conflict,
}

/// Durable top-up store. `create_pending` is intentionally a narrow atomic
/// operation so gateways cannot write user-controlled order fields.
#[async_trait]
pub trait StripeCreemStore: Send + Sync {
    async fn user(&self, user_id: i64) -> Result<TopupUser, TopupStoreError>;
    async fn stripe_settings(&self) -> Result<StripeSettings, TopupStoreError>;
    async fn creem_products(&self) -> Result<Vec<CreemProduct>, TopupStoreError>;
    async fn create_pending(&self, order: PendingTopup) -> Result<(), TopupStoreError>;
}

/// PostgreSQL implementation over the migrated legacy tables/options.
#[derive(Clone)]
pub struct PgStripeCreemStore {
    pg: PgPool,
    trusted_redirect_domains: Vec<String>,
}

impl PgStripeCreemStore {
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self {
            pg,
            trusted_redirect_domains: Vec::new(),
        }
    }

    /// Trusted redirect domains remain runtime configuration in the legacy
    /// service.  Callers must inject them rather than silently trusting a
    /// caller-provided URL; the empty default therefore fails closed.
    #[must_use]
    pub fn with_trusted_redirect_domains(mut self, domains: Vec<String>) -> Self {
        self.trusted_redirect_domains = domains;
        self
    }
}

#[async_trait]
impl StripeCreemStore for PgStripeCreemStore {
    async fn user(&self, user_id: i64) -> Result<TopupUser, TopupStoreError> {
        let row = sqlx::query("SELECT COALESCE(\"group\", 'default') AS group_name, COALESCE(email, '') AS email, COALESCE(username, '') AS username, COALESCE(stripe_customer, '') AS stripe_customer FROM users WHERE id=$1 AND deleted_at IS NULL")
            .bind(user_id)
            .fetch_optional(&self.pg)
            .await
            .map_err(|_| TopupStoreError::Unavailable)?
            .ok_or(TopupStoreError::NotFound)?;
        Ok(TopupUser {
            group: row
                .try_get("group_name")
                .map_err(|_| TopupStoreError::Unavailable)?,
            email: row
                .try_get("email")
                .map_err(|_| TopupStoreError::Unavailable)?,
            username: row
                .try_get("username")
                .map_err(|_| TopupStoreError::Unavailable)?,
            stripe_customer: row
                .try_get("stripe_customer")
                .map_err(|_| TopupStoreError::Unavailable)?,
        })
    }

    async fn stripe_settings(&self) -> Result<StripeSettings, TopupStoreError> {
        let rows = sqlx::query("SELECT key, value FROM options WHERE key = ANY($1)")
            .bind(vec![
                "StripeMinTopUp",
                "StripeUnitPrice",
                // `general_setting.quota_display_type` is the registered
                // operation-setting field used by Go at runtime.  The old
                // standalone `QuotaDisplayType` option is not authoritative.
                "general_setting.quota_display_type",
                "QuotaPerUnit",
                "TopupGroupRatio",
                "payment_setting.amount_discount",
                "payment_setting",
            ])
            .fetch_all(&self.pg)
            .await
            .map_err(|_| TopupStoreError::Unavailable)?;
        let options = rows
            .into_iter()
            .try_fold(BTreeMap::new(), |mut values, row| {
                let key: String = row
                    .try_get("key")
                    .map_err(|_| TopupStoreError::Unavailable)?;
                let value: String = row
                    .try_get("value")
                    .map_err(|_| TopupStoreError::Unavailable)?;
                values.insert(key, value);
                Ok::<_, TopupStoreError>(values)
            })?;
        Ok(stripe_settings_from_options(
            &options,
            self.trusted_redirect_domains.clone(),
        ))
    }

    async fn creem_products(&self) -> Result<Vec<CreemProduct>, TopupStoreError> {
        let raw = sqlx::query_scalar::<_, Option<String>>(
            "SELECT value FROM options WHERE key='CreemProducts'",
        )
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| TopupStoreError::Unavailable)?
        .flatten()
        .ok_or(TopupStoreError::NotFound)?;
        parse_creem_products(&raw)
    }

    async fn create_pending(&self, order: PendingTopup) -> Result<(), TopupStoreError> {
        sqlx::query("INSERT INTO top_ups (user_id,amount,money,trade_no,payment_method,payment_provider,create_time,status) VALUES ($1,$2,$3,$4,$5,$6,EXTRACT(EPOCH FROM NOW())::BIGINT,'pending')")
            .bind(order.user_id).bind(order.amount).bind(order.money).bind(&order.trade_no).bind(&order.payment_method).bind(&order.payment_provider)
            .execute(&self.pg).await.map_err(|error| if error.as_database_error().is_some_and(|database| database.code().as_deref() == Some("23505")) { TopupStoreError::Conflict } else { TopupStoreError::Unavailable })?;
        Ok(())
    }
}

fn parsed_i64(raw: Option<&String>, fallback: i64) -> i64 {
    raw.and_then(|value| value.parse().ok()).unwrap_or(fallback)
}

fn stripe_settings_from_options(
    options: &BTreeMap<String, String>,
    trusted_redirect_domains: Vec<String>,
) -> StripeSettings {
    let group_ratios = options
        .get("TopupGroupRatio")
        .and_then(|raw| serde_json::from_str(raw).ok())
        .unwrap_or_default();
    let amount_discounts: BTreeMap<i64, f64> = options
        .get("payment_setting.amount_discount")
        .and_then(|raw| serde_json::from_str(raw).ok())
        // Keep accepting the aggregate shape used by an older Rust fixture;
        // Go's registered config is authoritative at the dotted key above.
        .or_else(|| {
            options
                .get("payment_setting")
                .and_then(|raw| serde_json::from_str::<Value>(raw).ok())
                .and_then(|value| value.get("amount_discount").cloned())
                .and_then(|value| serde_json::from_value(value).ok())
        })
        .unwrap_or_default();
    StripeSettings {
        min_topup: parsed_i64(options.get("StripeMinTopUp"), 1),
        unit_price: finite_f64(options.get("StripeUnitPrice"), 8.0),
        quota_display_type: options
            .get("general_setting.quota_display_type")
            .cloned()
            .unwrap_or_else(|| "USD".to_owned()),
        // Go parses this as a float (`common.QuotaPerUnit`), so retain the
        // fractional value instead of silently falling back to 500_000.
        quota_per_unit: finite_f64(options.get("QuotaPerUnit"), 500_000.0),
        group_ratios,
        amount_discounts,
        trusted_redirect_domains,
    }
}
fn finite_f64(raw: Option<&String>, fallback: f64) -> f64 {
    raw.and_then(|value| value.parse().ok())
        .filter(|value: &f64| value.is_finite())
        .unwrap_or(fallback)
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct StripeCheckoutRequest {
    pub trade_no: String,
    pub customer: String,
    pub email: String,
    pub amount: i64,
    pub success_url: String,
    pub cancel_url: String,
}
#[derive(Clone, Debug, PartialEq)]
pub struct CreemCheckoutRequest {
    pub trade_no: String,
    pub product: CreemProduct,
    pub email: String,
    pub username: String,
}
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum CheckoutError {
    Unavailable,
}

/// Provider boundary. Production must inject configured provider adapters;
/// no HTTP client is constructed by this slice.
#[async_trait]
pub trait StripeCreemGateway: Send + Sync {
    async fn stripe_checkout(
        &self,
        request: StripeCheckoutRequest,
    ) -> Result<String, CheckoutError>;
    async fn creem_checkout(&self, request: CreemCheckoutRequest) -> Result<String, CheckoutError>;
}
pub struct DisabledStripeCreemGateway;
#[async_trait]
impl StripeCreemGateway for DisabledStripeCreemGateway {
    async fn stripe_checkout(&self, _: StripeCheckoutRequest) -> Result<String, CheckoutError> {
        Err(CheckoutError::Unavailable)
    }
    async fn creem_checkout(&self, _: CreemCheckoutRequest) -> Result<String, CheckoutError> {
        Err(CheckoutError::Unavailable)
    }
}

/// Explicitly injected local fixture adapter.  The normal listener must keep
/// using [`DisabledStripeCreemGateway`]; this adapter exists solely for an
/// isolated test listener that needs observable provider payloads and order
/// sequencing without real gateway credentials or egress.
#[derive(Clone)]
pub struct LoopbackStripeCreemGateway {
    endpoint: reqwest::Url,
    client: reqwest::Client,
    stripe_secret: SecretString,
    stripe_price_id: String,
    creem_api_key: SecretString,
    stripe_default_success_url: String,
    stripe_default_cancel_url: String,
}

impl LoopbackStripeCreemGateway {
    /// Builds a provider adapter constrained to one IPv4 loopback fixture.
    /// Secrets are only transmitted in the fixture request header and are
    /// never represented in a response, error, or log message.
    pub fn new(
        endpoint: &str,
        stripe_secret: SecretString,
        stripe_price_id: String,
        creem_api_key: SecretString,
        stripe_default_success_url: String,
        stripe_default_cancel_url: String,
    ) -> Result<Self, CheckoutError> {
        let endpoint = reqwest::Url::parse(endpoint).map_err(|_| CheckoutError::Unavailable)?;
        if endpoint.scheme() != "http" || endpoint.host_str() != Some("127.0.0.1") {
            return Err(CheckoutError::Unavailable);
        }
        Ok(Self {
            endpoint,
            client: reqwest::Client::new(),
            stripe_secret,
            stripe_price_id,
            creem_api_key,
            stripe_default_success_url,
            stripe_default_cancel_url,
        })
    }

    async fn fixture_checkout(
        &self,
        path: &str,
        secret: &SecretString,
        payload: Value,
    ) -> Result<Value, CheckoutError> {
        let url = self
            .endpoint
            .join(path)
            .map_err(|_| CheckoutError::Unavailable)?;
        let response = self
            .client
            .post(url)
            .header("x-fixture-secret", secret.expose_secret())
            .json(&payload)
            .send()
            .await
            .map_err(|_| CheckoutError::Unavailable)?;
        if !response.status().is_success() {
            return Err(CheckoutError::Unavailable);
        }
        response
            .json()
            .await
            .map_err(|_| CheckoutError::Unavailable)
    }

    fn stripe_fixture_payload(
        &self,
        request: &StripeCheckoutRequest,
    ) -> Result<Value, CheckoutError> {
        if self.stripe_price_id.is_empty() {
            return Err(CheckoutError::Unavailable);
        }
        let success_url = if request.success_url.is_empty() {
            &self.stripe_default_success_url
        } else {
            &request.success_url
        };
        let cancel_url = if request.cancel_url.is_empty() {
            &self.stripe_default_cancel_url
        } else {
            &request.cancel_url
        };
        if success_url.is_empty() || cancel_url.is_empty() {
            return Err(CheckoutError::Unavailable);
        }
        Ok(json!({
            "client_reference_id": request.trade_no,
            "customer": request.customer,
            "customer_email": request.email,
            "price_id": self.stripe_price_id,
            "quantity": request.amount,
            "success_url": success_url,
            "cancel_url": cancel_url,
        }))
    }

    fn creem_fixture_payload(request: CreemCheckoutRequest) -> Value {
        let CreemCheckoutRequest {
            trade_no,
            product,
            email,
            username,
        } = request;
        let CreemProduct {
            product_id,
            name,
            quota,
            ..
        } = product;
        json!({
            "product_id": product_id,
            "request_id": trade_no,
            "customer": {"email": email},
            "metadata": {
                "username": username,
                "product_name": name,
                "quota": quota.to_string(),
            },
        })
    }
}

#[async_trait]
impl StripeCreemGateway for LoopbackStripeCreemGateway {
    async fn stripe_checkout(
        &self,
        request: StripeCheckoutRequest,
    ) -> Result<String, CheckoutError> {
        if !(self.stripe_secret.expose_secret().starts_with("sk_")
            || self.stripe_secret.expose_secret().starts_with("rk_"))
        {
            return Err(CheckoutError::Unavailable);
        }
        let payload = self.stripe_fixture_payload(&request)?;
        let response = self
            .fixture_checkout("/stripe/checkout", &self.stripe_secret, payload)
            .await?;
        response
            .get("pay_link")
            .and_then(Value::as_str)
            .filter(|link| !link.trim().is_empty())
            .map(str::to_owned)
            .ok_or(CheckoutError::Unavailable)
    }

    async fn creem_checkout(&self, request: CreemCheckoutRequest) -> Result<String, CheckoutError> {
        if self.creem_api_key.expose_secret().is_empty() {
            return Err(CheckoutError::Unavailable);
        }
        let payload = Self::creem_fixture_payload(request);
        let response = self
            .fixture_checkout("/creem/checkout", &self.creem_api_key, payload)
            .await?;
        response
            .get("checkout_url")
            .and_then(Value::as_str)
            .filter(|link| !link.trim().is_empty())
            .map(str::to_owned)
            .ok_or(CheckoutError::Unavailable)
    }
}

#[derive(Clone)]
pub struct IdentityStripeCreemState {
    store: Arc<dyn StripeCreemStore>,
    authorizer: Arc<dyn StripeCreemAuthorizer>,
    gateway: Arc<dyn StripeCreemGateway>,
}
impl IdentityStripeCreemState {
    #[must_use]
    pub fn new(
        store: Arc<dyn StripeCreemStore>,
        authorizer: Arc<dyn StripeCreemAuthorizer>,
        gateway: Arc<dyn StripeCreemGateway>,
    ) -> Self {
        Self {
            store,
            authorizer,
            gateway,
        }
    }
}

pub fn router(state: IdentityStripeCreemState) -> Router {
    amount_router(state.clone()).merge(pay_router(state))
}

/// Mounts only the deterministic Stripe amount quote.  Checkout lives on
/// [`pay_router`] so the normal listener can adopt the read-only calculation
/// without overlapping the payment paths.
pub fn amount_router(state: IdentityStripeCreemState) -> Router {
    amount_routes().with_state(state)
}

/// Mounts Stripe and Creem checkout.  An unconfigured gateway fails closed
/// before any provider request; the listener supplies the shared dashboard
/// authorizer used by the amount quote.
pub fn pay_router(state: IdentityStripeCreemState) -> Router {
    Router::new()
        .route("/api/user/stripe/pay", post(stripe_pay))
        .route("/api/user/creem/pay", post(creem_pay))
        .with_state(state)
}

fn amount_routes() -> Router<IdentityStripeCreemState> {
    Router::new().route("/api/user/stripe/amount", post(stripe_amount))
}

#[derive(Default, Deserialize)]
struct StripePayRequest {
    #[serde(default, deserialize_with = "null_i64_is_zero")]
    amount: i64,
    #[serde(default, deserialize_with = "null_string_is_empty")]
    payment_method: String,
    #[serde(default, deserialize_with = "null_string_is_empty")]
    success_url: String,
    #[serde(default, deserialize_with = "null_string_is_empty")]
    cancel_url: String,
}
#[derive(Default, Deserialize)]
struct CreemPayRequest {
    #[serde(default, deserialize_with = "null_string_is_empty")]
    product_id: String,
    #[serde(default, deserialize_with = "null_string_is_empty")]
    payment_method: String,
}

async fn actor(
    state: &IdentityStripeCreemState,
    headers: &HeaderMap,
) -> Result<TopupPrincipal, Response> {
    state.authorizer.principal(headers).await.map_err(|error| {
        let (status, code, message) = match error {
            TopupAuthError::TokenExpired => (
                StatusCode::UNAUTHORIZED,
                "AUTH_TOKEN_EXPIRED",
                auth_not_logged_in(headers),
            ),
            TopupAuthError::SessionRevoked => (
                StatusCode::UNAUTHORIZED,
                "AUTH_SESSION_REVOKED",
                auth_not_logged_in(headers),
            ),
            TopupAuthError::Unavailable => (
                StatusCode::INTERNAL_SERVER_ERROR,
                "AUTH_INTERNAL_ERROR",
                "Database error, please contact the administrator",
            ),
            TopupAuthError::Unauthorized => (
                StatusCode::UNAUTHORIZED,
                "AUTH_UNAUTHORIZED",
                "Unauthorized, invalid access token",
            ),
            TopupAuthError::UserAuth(policy) => (
                match policy {
                    UserAuthPolicyError::InsufficientPrivilege => StatusCode::FORBIDDEN,
                    UserAuthPolicyError::UserDisabled | UserAuthPolicyError::InvalidUserInfo => {
                        StatusCode::UNAUTHORIZED
                    }
                },
                match policy {
                    UserAuthPolicyError::UserDisabled => "AUTH_USER_DISABLED",
                    UserAuthPolicyError::InsufficientPrivilege => "AUTH_INSUFFICIENT_PRIVILEGE",
                    UserAuthPolicyError::InvalidUserInfo => "AUTH_USER_INVALID",
                },
                user_auth_message(
                    policy,
                    headers
                        .get(header::ACCEPT_LANGUAGE)
                        .and_then(|value| value.to_str().ok()),
                ),
            ),
        };
        (
            status,
            Json(json!({"success":false,"code":code,"message":message})),
        )
            .into_response()
    })
}
fn legacy(message: &str, data: impl Serialize) -> Response {
    Json(json!({"message":message,"data":data})).into_response()
}

async fn stripe_amount(
    State(state): State<IdentityStripeCreemState>,
    request: Request,
) -> Response {
    let headers = request.headers().clone();
    let actor = match actor(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let request: StripePayRequest = match legacy_json(request).await {
        Ok(request) => request,
        Err(LegacyJsonError::Read) | Err(LegacyJsonError::Invalid) => {
            return legacy("error", "参数错误");
        }
    };
    let settings = match state.store.stripe_settings().await {
        Ok(settings) => settings,
        Err(_) => return legacy("error", "获取用户分组失败"),
    };
    let min = stripe_minimum(&settings);
    if request.amount < min {
        return legacy("error", format!("充值数量不能小于 {min}"));
    }
    let user = match state.store.user(actor.user_id).await {
        Ok(user) => user,
        Err(_) => return legacy("error", "获取用户分组失败"),
    };
    let money = stripe_money(request.amount, &user.group, &settings);
    if money <= 0.01 {
        return legacy("error", "充值金额过低");
    }
    legacy("success", format!("{money:.2}"))
}

async fn stripe_pay(State(state): State<IdentityStripeCreemState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let actor = match actor(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let request: StripePayRequest = match legacy_json(request).await {
        Ok(request) => request,
        Err(LegacyJsonError::Read) | Err(LegacyJsonError::Invalid) => {
            return legacy("error", "参数错误");
        }
    };
    if request.payment_method != STRIPE {
        return legacy("error", "不支持的支付渠道");
    }
    let settings = match state.store.stripe_settings().await {
        Ok(settings) => settings,
        Err(_) => return legacy("error", "拉起支付失败"),
    };
    let min = stripe_minimum(&settings);
    if request.amount < min {
        return legacy(&format!("充值数量不能小于 {min}"), 10);
    }
    if request.amount > 10_000 {
        return legacy("充值数量不能大于 10000", 10);
    }
    if !redirect_allowed(&request.success_url, &settings.trusted_redirect_domains) {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({"message":"支付成功重定向URL不在可信任域名列表中","data":""})),
        )
            .into_response();
    }
    if !redirect_allowed(&request.cancel_url, &settings.trusted_redirect_domains) {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({"message":"支付取消重定向URL不在可信任域名列表中","data":""})),
        )
            .into_response();
    }
    let user = match state.store.user(actor.user_id).await {
        Ok(user) => user,
        Err(_) => return legacy("error", "拉起支付失败"),
    };
    let trade_no = legacy_trade_no("new-api-ref", actor.user_id);
    let link = match state
        .gateway
        .stripe_checkout(StripeCheckoutRequest {
            trade_no: trade_no.clone(),
            customer: user.stripe_customer.clone(),
            email: user.email.clone(),
            amount: request.amount,
            success_url: request.success_url,
            cancel_url: request.cancel_url,
        })
        .await
    {
        Ok(link) if !link.trim().is_empty() => link,
        _ => return legacy("error", "拉起支付失败"),
    };
    let ratio = group_ratio(&user.group, &settings);
    let order = PendingTopup {
        trade_no,
        user_id: actor.user_id,
        amount: request.amount,
        money: (request.amount as f64) * ratio,
        payment_method: STRIPE.to_owned(),
        payment_provider: STRIPE.to_owned(),
    };
    if state.store.create_pending(order).await.is_err() {
        return legacy("error", "创建订单失败");
    }
    legacy("success", json!({"pay_link":link}))
}

async fn creem_pay(State(state): State<IdentityStripeCreemState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let actor = match actor(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let request: CreemPayRequest = match legacy_json(request).await {
        Ok(request) => request,
        Err(LegacyJsonError::Read) => return legacy("error", "read query error"),
        Err(LegacyJsonError::Invalid) => return legacy("error", "参数错误"),
    };
    if request.payment_method != CREEM {
        return legacy("error", "不支持的支付渠道");
    }
    if request.product_id.is_empty() {
        return legacy("error", "请选择产品");
    }
    let products = match state.store.creem_products().await {
        Ok(products) => products,
        Err(_) => return legacy("error", "产品配置错误"),
    };
    let Some(product) = products
        .into_iter()
        .find(|product| product.product_id == request.product_id)
    else {
        return legacy("error", "产品不存在");
    };
    let user = match state.store.user(actor.user_id).await {
        Ok(user) => user,
        Err(_) => return legacy("error", "创建订单失败"),
    };
    let trade_no = legacy_trade_no("creem-api-ref", actor.user_id);
    let order = PendingTopup {
        trade_no: trade_no.clone(),
        user_id: actor.user_id,
        amount: product.quota,
        money: product.price,
        payment_method: CREEM.to_owned(),
        payment_provider: CREEM.to_owned(),
    };
    if state.store.create_pending(order).await.is_err() {
        return legacy("error", "创建订单失败");
    }
    let checkout_url = match state
        .gateway
        .creem_checkout(CreemCheckoutRequest {
            trade_no: trade_no.clone(),
            product,
            email: user.email,
            username: user.username,
        })
        .await
    {
        Ok(link) if !link.trim().is_empty() => link,
        _ => return legacy("error", "拉起支付失败"),
    };
    legacy(
        "success",
        json!({"checkout_url":checkout_url,"order_id":trade_no}),
    )
}

/// The frozen Go handlers run behind `UserAuth`, then call `ShouldBindJSON`.
/// Keeping parsing inside the handler preserves that ordering and intentionally
/// accepts JSON regardless of Content-Type, as `ShouldBindJSON` does.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum LegacyJsonError {
    Read,
    Invalid,
}

async fn legacy_json<T: DeserializeOwned + Default>(
    request: Request,
) -> Result<T, LegacyJsonError> {
    let bytes = to_bytes(request.into_body(), MAX_LEGACY_TOPUP_BODY_BYTES)
        .await
        .map_err(|_| LegacyJsonError::Read)?;
    let mut decoder = serde_json::Deserializer::from_slice(&bytes);
    Option::<T>::deserialize(&mut decoder)
        .map(|request| request.unwrap_or_default())
        .map_err(|_| LegacyJsonError::Invalid)
}

fn null_i64_is_zero<'de, D>(deserializer: D) -> Result<i64, D::Error>
where
    D: serde::Deserializer<'de>,
{
    Option::<i64>::deserialize(deserializer).map(Option::unwrap_or_default)
}

fn null_f64_is_zero<'de, D>(deserializer: D) -> Result<f64, D::Error>
where
    D: serde::Deserializer<'de>,
{
    Option::<f64>::deserialize(deserializer).map(Option::unwrap_or_default)
}

fn null_string_is_empty<'de, D>(deserializer: D) -> Result<String, D::Error>
where
    D: serde::Deserializer<'de>,
{
    Option::<String>::deserialize(deserializer).map(Option::unwrap_or_default)
}

fn legacy_trade_no(prefix: &str, user_id: i64) -> String {
    let now_ms = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_millis());
    let random_suffix = &Uuid::new_v4().simple().to_string()[..4];
    let reference = format!("{prefix}-{user_id}-{now_ms}-{random_suffix}");
    format!("ref_{}", legacy_sha1_hex(reference.as_bytes()))
}

// This is only the historical trade-number digest, not an authentication or
// signature primitive.  Keeping it local avoids a dependency/lockfile change.
fn legacy_sha1_hex(input: &[u8]) -> String {
    let bit_len = (input.len() as u64).wrapping_mul(8);
    let mut bytes = input.to_vec();
    bytes.push(0x80);
    while bytes.len() % 64 != 56 {
        bytes.push(0);
    }
    bytes.extend_from_slice(&bit_len.to_be_bytes());

    let mut h = [
        0x6745_2301_u32,
        0xEFCD_AB89,
        0x98BA_DCFE,
        0x1032_5476,
        0xC3D2_E1F0,
    ];
    for block in bytes.chunks_exact(64) {
        let mut words = [0_u32; 80];
        for (index, chunk) in block.chunks_exact(4).enumerate() {
            // `chunks_exact(4)` guarantees all four indexes are present.
            words[index] = u32::from_be_bytes([chunk[0], chunk[1], chunk[2], chunk[3]]);
        }
        for index in 16..80 {
            words[index] =
                (words[index - 3] ^ words[index - 8] ^ words[index - 14] ^ words[index - 16])
                    .rotate_left(1);
        }
        let (mut a, mut b, mut c, mut d, mut e) = (h[0], h[1], h[2], h[3], h[4]);
        for (index, word) in words.into_iter().enumerate() {
            let (f, k) = match index {
                0..=19 => ((b & c) | ((!b) & d), 0x5A82_7999),
                20..=39 => (b ^ c ^ d, 0x6ED9_EBA1),
                40..=59 => ((b & c) | (b & d) | (c & d), 0x8F1B_BCDC),
                _ => (b ^ c ^ d, 0xCA62_C1D6),
            };
            let next = a
                .rotate_left(5)
                .wrapping_add(f)
                .wrapping_add(e)
                .wrapping_add(k)
                .wrapping_add(word);
            e = d;
            d = c;
            c = b.rotate_left(30);
            b = a;
            a = next;
        }
        h[0] = h[0].wrapping_add(a);
        h[1] = h[1].wrapping_add(b);
        h[2] = h[2].wrapping_add(c);
        h[3] = h[3].wrapping_add(d);
        h[4] = h[4].wrapping_add(e);
    }
    format!(
        "{:08x}{:08x}{:08x}{:08x}{:08x}",
        h[0], h[1], h[2], h[3], h[4]
    )
}

fn auth_not_logged_in(headers: &HeaderMap) -> &'static str {
    let language = headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default()
        .to_ascii_lowercase();
    if language.starts_with("zh-tw") {
        "無權進行此操作，未登入且未提供 access token"
    } else if language.starts_with("zh") {
        "无权进行此操作，未登录且未提供 access token"
    } else {
        "Unauthorized, not logged in and no access token provided"
    }
}

fn stripe_minimum(settings: &StripeSettings) -> i64 {
    if settings.quota_display_type == "TOKENS" {
        settings
            .min_topup
            .saturating_mul(settings.quota_per_unit as i64)
    } else {
        settings.min_topup
    }
}
fn group_ratio(group: &str, settings: &StripeSettings) -> f64 {
    settings
        .group_ratios
        .get(group)
        .copied()
        .filter(|ratio| ratio.is_finite() && *ratio != 0.0)
        .unwrap_or(1.0)
}
fn stripe_money(amount: i64, group: &str, settings: &StripeSettings) -> f64 {
    let displayed = if settings.quota_display_type == "TOKENS" {
        (amount as f64) / settings.quota_per_unit
    } else {
        amount as f64
    };
    let discount = settings
        .amount_discounts
        .get(&amount)
        .copied()
        .filter(|discount| discount.is_finite() && *discount > 0.0)
        .unwrap_or(1.0);
    displayed * settings.unit_price * group_ratio(group, settings) * discount
}
fn redirect_allowed(raw: &str, trusted: &[String]) -> bool {
    if raw.is_empty() {
        return true;
    }
    let Ok(url) = reqwest::Url::parse(raw) else {
        return false;
    };
    if !matches!(url.scheme(), "http" | "https") {
        return false;
    }
    let Some(host) = url.host_str() else {
        return false;
    };
    let host = host.to_ascii_lowercase();
    trusted.iter().any(|domain| {
        let domain = domain.trim().to_ascii_lowercase();
        !domain.is_empty() && (host == domain || host.strip_suffix(&format!(".{domain}")).is_some())
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{body::Body, http::Request};
    use std::sync::Mutex;
    use tower::ServiceExt;

    #[derive(Default)]
    struct Store {
        orders: Mutex<Vec<PendingTopup>>,
    }
    #[async_trait]
    impl StripeCreemStore for Store {
        async fn user(&self, _: i64) -> Result<TopupUser, TopupStoreError> {
            Ok(TopupUser {
                group: "vip".into(),
                email: "a@example.test".into(),
                username: "a".into(),
                stripe_customer: String::new(),
            })
        }
        async fn stripe_settings(&self) -> Result<StripeSettings, TopupStoreError> {
            Ok(StripeSettings {
                group_ratios: BTreeMap::from([("vip".into(), 1.2)]),
                amount_discounts: BTreeMap::from([(100, 0.8)]),
                trusted_redirect_domains: vec!["example.test".into()],
                ..StripeSettings::default()
            })
        }
        async fn creem_products(&self) -> Result<Vec<CreemProduct>, TopupStoreError> {
            Ok(vec![CreemProduct {
                product_id: "p1".into(),
                name: "Pro".into(),
                price: 3.5,
                currency: "USD".into(),
                quota: 100,
            }])
        }
        async fn create_pending(&self, order: PendingTopup) -> Result<(), TopupStoreError> {
            self.orders.lock().unwrap().push(order);
            Ok(())
        }
    }
    struct Auth;
    #[async_trait]
    impl StripeCreemAuthorizer for Auth {
        async fn principal(&self, headers: &HeaderMap) -> Result<TopupPrincipal, TopupAuthError> {
            if bearer(headers) == Some("ok") {
                Ok(TopupPrincipal { user_id: 7 })
            } else {
                Err(TopupAuthError::Unauthorized)
            }
        }
    }
    struct Gateway;
    #[async_trait]
    impl StripeCreemGateway for Gateway {
        async fn stripe_checkout(&self, _: StripeCheckoutRequest) -> Result<String, CheckoutError> {
            Ok("https://checkout.example.test/s".into())
        }
        async fn creem_checkout(&self, _: CreemCheckoutRequest) -> Result<String, CheckoutError> {
            Ok("https://checkout.example.test/c".into())
        }
    }
    fn app(store: Arc<Store>) -> Router {
        router(IdentityStripeCreemState::new(
            store,
            Arc::new(Auth),
            Arc::new(Gateway),
        ))
    }
    fn request(path: &str, body: Value) -> Request<Body> {
        Request::post(path)
            .header("authorization", "Bearer ok")
            .header("content-type", "application/json")
            .body(Body::from(body.to_string()))
            .unwrap()
    }
    fn request_without_content_type(path: &str, body: Value) -> Request<Body> {
        Request::post(path)
            .header("authorization", "Bearer ok")
            .body(Body::from(body.to_string()))
            .unwrap()
    }
    struct QuoteStore {
        settings: StripeSettings,
    }
    #[async_trait]
    impl StripeCreemStore for QuoteStore {
        async fn user(&self, _: i64) -> Result<TopupUser, TopupStoreError> {
            Ok(TopupUser {
                group: "vip".into(),
                email: "a@example.test".into(),
                username: "a".into(),
                stripe_customer: String::new(),
            })
        }
        async fn stripe_settings(&self) -> Result<StripeSettings, TopupStoreError> {
            Ok(self.settings.clone())
        }
        async fn creem_products(&self) -> Result<Vec<CreemProduct>, TopupStoreError> {
            Ok(Vec::new())
        }
        async fn create_pending(&self, _: PendingTopup) -> Result<(), TopupStoreError> {
            Ok(())
        }
    }
    fn quote_app(settings: StripeSettings) -> Router {
        router(IdentityStripeCreemState::new(
            Arc::new(QuoteStore { settings }),
            Arc::new(Auth),
            Arc::new(DisabledStripeCreemGateway),
        ))
    }
    async fn response_json(app: Router, path: &str, body: Value) -> Value {
        let response = app.oneshot(request(path, body)).await.unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        let body = axum::body::to_bytes(response.into_body(), usize::MAX)
            .await
            .unwrap();
        serde_json::from_slice(&body).unwrap()
    }
    #[tokio::test]
    async fn stripe_amount_keeps_group_ratio_and_preset_discount() {
        let store = Arc::new(Store::default());
        let response = app(store)
            .oneshot(request("/api/user/stripe/amount", json!({"amount":100})))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        let body = axum::body::to_bytes(response.into_body(), usize::MAX)
            .await
            .unwrap();
        assert_eq!(
            serde_json::from_slice::<Value>(&body).unwrap(),
            json!({"message":"success","data":"768.00"})
        );
    }
    #[tokio::test]
    async fn authenticated_quote_keeps_should_bind_json_content_type_independence() {
        let response = app(Arc::new(Store::default()))
            .oneshot(request_without_content_type(
                "/api/user/stripe/amount",
                json!({"amount":100}),
            ))
            .await
            .unwrap();

        assert_eq!(response.status(), StatusCode::OK);
        let body = axum::body::to_bytes(response.into_body(), usize::MAX)
            .await
            .unwrap();
        assert_eq!(
            serde_json::from_slice::<Value>(&body).unwrap(),
            json!({"message":"success","data":"768.00"})
        );
    }
    #[tokio::test]
    async fn null_or_missing_amount_keeps_go_zero_value_validation() {
        for body in [Value::Null, json!({}), json!({"amount":null})] {
            let response = app(Arc::new(Store::default()))
                .oneshot(request("/api/user/stripe/amount", body))
                .await
                .unwrap();
            assert_eq!(response.status(), StatusCode::OK);
            let body = axum::body::to_bytes(response.into_body(), usize::MAX)
                .await
                .unwrap();
            assert_eq!(
                serde_json::from_slice::<Value>(&body).unwrap(),
                json!({"message":"error","data":"充值数量不能小于 1"})
            );
        }
    }
    #[tokio::test]
    async fn string_amount_remains_a_legacy_json_type_error() {
        let body = response_json(
            app(Arc::new(Store::default())),
            "/api/user/stripe/amount",
            json!({"amount":"0"}),
        )
        .await;
        assert_eq!(body, json!({"message":"error","data":"参数错误"}));
    }
    #[test]
    fn creem_products_keep_null_slice_and_null_scalar_field_rules() {
        assert!(parse_creem_products("null").unwrap().is_empty());
        assert_eq!(
            parse_creem_products(
                r#"[{"productId":null,"name":null,"price":null,"currency":null,"quota":null}]"#,
            )
            .unwrap(),
            vec![CreemProduct {
                product_id: String::new(),
                name: String::new(),
                price: 0.0,
                currency: String::new(),
                quota: 0,
            }]
        );
        assert_eq!(
            parse_creem_products(r#""not-an-array""#),
            Err(TopupStoreError::Unavailable)
        );
    }
    #[test]
    fn bearer_matches_legacy_bearer_case_and_bare_token_forms() {
        let mut headers = HeaderMap::new();
        headers.insert(header::AUTHORIZATION, "bEaReR token".parse().unwrap());
        assert_eq!(bearer(&headers), Some("token"));
        headers.insert(header::AUTHORIZATION, "raw-token".parse().unwrap());
        assert_eq!(bearer(&headers), Some("raw-token"));
        headers.insert(header::AUTHORIZATION, "Bearer one two".parse().unwrap());
        assert_eq!(bearer(&headers), None);
    }
    #[test]
    fn legacy_trade_number_uses_the_go_sha1_shape() {
        assert_eq!(
            legacy_sha1_hex(b"abc"),
            "a9993e364706816aba3e25717850c26c9cd0d89d"
        );
        let trade_no = legacy_trade_no("creem-api-ref", 7);
        assert!(trade_no.starts_with("ref_"));
        assert_eq!(trade_no.len(), 44);
        assert!(
            trade_no[4..]
                .chars()
                .all(|character| character.is_ascii_hexdigit())
        );
    }
    #[test]
    fn loopback_fixture_rejects_egress_and_preserves_default_url_payload() {
        assert!(
            LoopbackStripeCreemGateway::new(
                "https://provider.example.test",
                SecretString::from("sk_fixture"),
                "price_fixture".into(),
                SecretString::from("creem_fixture"),
                "https://console.example.test/usage-logs".into(),
                "https://console.example.test/wallet".into(),
            )
            .is_err()
        );

        let gateway = LoopbackStripeCreemGateway::new(
            "http://127.0.0.1:19093/",
            SecretString::from("sk_fixture"),
            "price_fixture".into(),
            SecretString::from("creem_fixture"),
            "https://console.example.test/usage-logs".into(),
            "https://console.example.test/wallet".into(),
        )
        .unwrap();
        assert_eq!(
            gateway
                .stripe_fixture_payload(&StripeCheckoutRequest {
                    trade_no: "ref_trade".into(),
                    customer: String::new(),
                    email: "user@example.test".into(),
                    amount: 3,
                    success_url: String::new(),
                    cancel_url: String::new(),
                })
                .unwrap(),
            json!({
                "client_reference_id":"ref_trade",
                "customer":"",
                "customer_email":"user@example.test",
                "price_id":"price_fixture",
                "quantity":3,
                "success_url":"https://console.example.test/usage-logs",
                "cancel_url":"https://console.example.test/wallet",
            })
        );
        assert_eq!(
            LoopbackStripeCreemGateway::creem_fixture_payload(CreemCheckoutRequest {
                trade_no: "ref_creem".into(),
                product: CreemProduct {
                    product_id: "product_fixture".into(),
                    name: "Pro".into(),
                    price: 3.5,
                    currency: "USD".into(),
                    quota: 100,
                },
                email: "user@example.test".into(),
                username: "light".into(),
            }),
            json!({
                "product_id":"product_fixture",
                "request_id":"ref_creem",
                "customer":{"email":"user@example.test"},
                "metadata":{"username":"light","product_name":"Pro","quota":"100"},
            })
        );
    }
    #[tokio::test]
    async fn loopback_fixture_rejects_empty_provider_credentials_before_network() {
        let empty_stripe = LoopbackStripeCreemGateway::new(
            "http://127.0.0.1:19093/",
            SecretString::from(""),
            "price_fixture".into(),
            SecretString::from("creem_fixture"),
            "https://console.example.test/usage-logs".into(),
            "https://console.example.test/wallet".into(),
        )
        .unwrap();
        assert_eq!(
            empty_stripe
                .stripe_checkout(StripeCheckoutRequest {
                    trade_no: "ref_trade".into(),
                    customer: String::new(),
                    email: String::new(),
                    amount: 1,
                    success_url: String::new(),
                    cancel_url: String::new(),
                })
                .await,
            Err(CheckoutError::Unavailable)
        );

        let empty_creem = LoopbackStripeCreemGateway::new(
            "http://127.0.0.1:19093/",
            SecretString::from("sk_fixture"),
            "price_fixture".into(),
            SecretString::from(""),
            "https://console.example.test/usage-logs".into(),
            "https://console.example.test/wallet".into(),
        )
        .unwrap();
        assert_eq!(
            empty_creem
                .creem_checkout(CreemCheckoutRequest {
                    trade_no: "ref_trade".into(),
                    product: CreemProduct {
                        product_id: "product_fixture".into(),
                        name: "fixture".into(),
                        price: 3.5,
                        currency: "USD".into(),
                        quota: 10,
                    },
                    email: String::new(),
                    username: String::new(),
                })
                .await,
            Err(CheckoutError::Unavailable)
        );
    }
    #[test]
    fn stripe_settings_read_registered_display_type_and_fractional_quota_per_unit() {
        let options = BTreeMap::from([
            ("QuotaDisplayType".into(), "TOKENS".into()),
            ("general_setting.quota_display_type".into(), "CNY".into()),
            ("QuotaPerUnit".into(), "12.5".into()),
            (
                "payment_setting.amount_discount".into(),
                r#"{"100":0.8}"#.into(),
            ),
        ]);

        let settings = stripe_settings_from_options(&options, Vec::new());

        assert_eq!(settings.quota_display_type, "CNY");
        assert_eq!(settings.quota_per_unit, 12.5);
        assert_eq!(settings.amount_discounts.get(&100), Some(&0.8));
    }

    #[test]
    fn stripe_settings_keep_legacy_aggregate_discount_fallback() {
        let options = BTreeMap::from([(
            "payment_setting".into(),
            r#"{"amount_discount":{"100":0.75}}"#.into(),
        )]);
        let settings = stripe_settings_from_options(&options, Vec::new());
        assert_eq!(settings.amount_discounts.get(&100), Some(&0.75));
    }
    #[tokio::test]
    async fn stripe_quote_http_seam_preserves_usd_cny_and_tokens_display_rules() {
        let cases = [("USD", "1200.00"), ("CNY", "1200.00"), ("TOKENS", "48.00")];
        for (display_type, expected) in cases {
            let settings = StripeSettings {
                min_topup: 2,
                unit_price: 10.0,
                quota_display_type: display_type.into(),
                quota_per_unit: 25.0,
                group_ratios: BTreeMap::from([("vip".into(), 1.5)]),
                amount_discounts: BTreeMap::from([(100, 0.8)]),
                trusted_redirect_domains: Vec::new(),
            };
            let body = response_json(
                quote_app(settings),
                "/api/user/stripe/amount",
                json!({"amount":100}),
            )
            .await;
            assert_eq!(body, json!({"message":"success","data":expected}));
        }
    }
    #[test]
    fn stripe_minimum_uses_truncated_non_default_quota_per_unit_only_for_tokens() {
        let mut settings = StripeSettings {
            min_topup: 3,
            quota_per_unit: 12.5,
            ..StripeSettings::default()
        };
        settings.quota_display_type = "TOKENS".into();
        assert_eq!(stripe_minimum(&settings), 36);
        settings.quota_display_type = "CNY".into();
        assert_eq!(stripe_minimum(&settings), 3);
    }
    #[tokio::test]
    async fn stripe_rejects_untrusted_redirect_before_gateway_or_order() {
        let store = Arc::new(Store::default());
        let response = app(store.clone())
            .oneshot(request(
                "/api/user/stripe/pay",
                json!({"amount":2,"payment_method":"stripe","success_url":"https://evil.test"}),
            ))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::BAD_REQUEST);
        assert!(store.orders.lock().unwrap().is_empty());
    }
    #[tokio::test]
    async fn stripe_accepts_trusted_subdomain_with_injected_no_egress_gateway() {
        let store = Arc::new(Store::default());
        let body = response_json(
            app(store.clone()),
            "/api/user/stripe/pay",
            json!({
                "amount":2,
                "payment_method":"stripe",
                "success_url":"https://pay.example.test/complete",
                "cancel_url":"http://example.test/cancel"
            }),
        )
        .await;

        assert_eq!(body["message"], "success");
        let orders = store.orders.lock().unwrap();
        assert_eq!(orders.len(), 1);
        assert_eq!(orders[0].money, 2.4);
    }
    #[tokio::test]
    async fn stripe_provider_failure_is_fail_closed_without_pending_order() {
        let store = Arc::new(Store::default());
        let app = router(IdentityStripeCreemState::new(
            store.clone(),
            Arc::new(Auth),
            Arc::new(DisabledStripeCreemGateway),
        ));
        let response = app
            .oneshot(request(
                "/api/user/stripe/pay",
                json!({"amount":2,"payment_method":"stripe"}),
            ))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        assert!(store.orders.lock().unwrap().is_empty());
    }
    #[tokio::test]
    async fn unauthenticated_user_cannot_quote_or_start_checkout() {
        for (uri, body) in [
            ("/api/user/stripe/amount", r#"{"amount":100}"#),
            (
                "/api/user/stripe/pay",
                r#"{"amount":100,"payment_method":"stripe"}"#,
            ),
            (
                "/api/user/creem/pay",
                r#"{"product_id":"p1","payment_method":"creem"}"#,
            ),
        ] {
            let response = app(Arc::new(Store::default()))
                .oneshot(
                    Request::post(uri)
                        .header(header::CONTENT_TYPE, "application/json")
                        .body(Body::from(body))
                        .unwrap(),
                )
                .await
                .unwrap();
            assert_eq!(response.status(), StatusCode::UNAUTHORIZED, "{uri}");
            assert_eq!(
                response.headers().get(header::CONTENT_TYPE).unwrap(),
                "application/json",
                "{uri}"
            );
            assert_eq!(
                serde_json::from_slice::<Value>(
                    &axum::body::to_bytes(response.into_body(), usize::MAX)
                        .await
                        .unwrap()
                )
                .unwrap(),
                json!({"success":false,"code":"AUTH_UNAUTHORIZED","message":"Unauthorized, invalid access token"}),
                "{uri}"
            );
        }
    }
    #[tokio::test]
    async fn creem_persists_legacy_pending_order_before_checkout() {
        let store = Arc::new(Store::default());
        let response = app(store.clone())
            .oneshot(request(
                "/api/user/creem/pay",
                json!({"product_id":"p1","payment_method":"creem"}),
            ))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        let orders = store.orders.lock().unwrap();
        assert_eq!(orders.len(), 1);
        assert_eq!(orders[0].amount, 100);
        assert_eq!(orders[0].money, 3.5);
    }
    #[tokio::test]
    async fn creem_space_only_product_id_follows_legacy_lookup_not_empty_rejection() {
        let response = app(Arc::new(Store::default()))
            .oneshot(request(
                "/api/user/creem/pay",
                json!({"product_id":"   ","payment_method":"creem"}),
            ))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        let body = axum::body::to_bytes(response.into_body(), usize::MAX)
            .await
            .unwrap();
        assert_eq!(
            serde_json::from_slice::<Value>(&body).unwrap(),
            json!({"message":"error","data":"产品不存在"})
        );
    }
}
