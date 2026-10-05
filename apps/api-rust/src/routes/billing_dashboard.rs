//! Legacy OpenAI-compatible billing dashboard reads.
//!
//! The original routes sit behind `TokenAuth`, rather than dashboard-session
//! authentication.  Keeping that distinction in this slice is important:
//! dashboard bearer tokens are not relay API keys and must never be accepted
//! here.  The HTTP layer owns only the legacy response shape; token validation
//! and durable option reads are injected so the same code has a strict HTTP
//! seam in tests and a PostgreSQL implementation at runtime.

use std::{
    net::{IpAddr, Ipv4Addr},
    sync::Arc,
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    extract::{Request, State},
    http::{HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::get,
};
use rust_decimal::Decimal;
use serde::Serialize;
use serde_json::{Value, json};
use sqlx::{PgPool, Row};

use crate::RequestContext;

const DEFAULT_QUOTA_PER_UNIT: i64 = 500_000;
const MAX_WALLET_QUOTA: i64 = 9_007_199_254_740_991;
const DEFAULT_USD_EXCHANGE_RATE: f64 = 7.3;

/// The authenticated API-token context used by the legacy handlers.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct BillingDashboardPrincipal {
    pub token_id: i64,
    pub user_id: i64,
    pub remain_quota: i64,
    pub used_quota: i64,
    pub unlimited_quota: bool,
    pub expired_time: i64,
}

/// Server-derived data passed to the relay-token boundary.  The listener
/// provides the canonical client IP after applying its trusted-proxy policy.
#[derive(Clone, Debug)]
pub struct BillingDashboardRequest {
    pub authorization: Option<String>,
    pub client_ip: IpAddr,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum BillingDashboardAuthError {
    Unauthorized,
    Forbidden,
    Unavailable,
}

/// Token authentication boundary for the OpenAI billing compatibility API.
///
/// A production adapter must validate a relay API token, not a dashboard
/// session.  It deliberately receives only the credential header, matching
/// the legacy route's `Authorization` source.
#[async_trait]
pub trait BillingDashboardAuthorizer: Send + Sync {
    async fn authorize(
        &self,
        request: BillingDashboardRequest,
    ) -> Result<BillingDashboardPrincipal, BillingDashboardAuthError>;
}

/// PostgreSQL implementation of the legacy `TokenAuth` policy for this read
/// surface.  It does not update access counters or create cache state.
#[derive(Clone)]
pub struct PgBillingDashboardAuthorizer {
    pg: PgPool,
}

impl PgBillingDashboardAuthorizer {
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self { pg }
    }
}

#[async_trait]
impl BillingDashboardAuthorizer for PgBillingDashboardAuthorizer {
    async fn authorize(
        &self,
        request: BillingDashboardRequest,
    ) -> Result<BillingDashboardPrincipal, BillingDashboardAuthError> {
        let credential = request
            .authorization
            .as_deref()
            .and_then(legacy_token_key)
            .ok_or(BillingDashboardAuthError::Unauthorized)?;
        let now = unix_seconds();
        let row = sqlx::query(
            "SELECT t.id, t.user_id, t.remain_quota, t.used_quota, \
                    t.unlimited_quota, t.expired_time, COALESCE(t.allow_ips, '') AS allow_ips \
             FROM tokens t JOIN users u ON u.id = t.user_id \
             WHERE t.key = $1 AND t.deleted_at IS NULL AND u.deleted_at IS NULL \
               AND t.status = 1 AND u.status = 1 \
               AND (t.expired_time = -1 OR t.expired_time >= $2) \
               AND (t.unlimited_quota OR t.remain_quota > 0) \
             LIMIT 1",
        )
        .bind(credential)
        .bind(now)
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| BillingDashboardAuthError::Unavailable)?
        .ok_or(BillingDashboardAuthError::Unauthorized)?;

        let principal = BillingDashboardPrincipal {
            token_id: row
                .try_get("id")
                .map_err(|_| BillingDashboardAuthError::Unavailable)?,
            user_id: row
                .try_get("user_id")
                .map_err(|_| BillingDashboardAuthError::Unavailable)?,
            remain_quota: row
                .try_get("remain_quota")
                .map_err(|_| BillingDashboardAuthError::Unavailable)?,
            used_quota: row
                .try_get("used_quota")
                .map_err(|_| BillingDashboardAuthError::Unavailable)?,
            unlimited_quota: row
                .try_get("unlimited_quota")
                .map_err(|_| BillingDashboardAuthError::Unavailable)?,
            expired_time: row
                .try_get("expired_time")
                .map_err(|_| BillingDashboardAuthError::Unavailable)?,
        };
        let allow_ips: String = row
            .try_get("allow_ips")
            .map_err(|_| BillingDashboardAuthError::Unavailable)?;
        if !ip_is_allowed(request.client_ip, &allow_ips) {
            return Err(BillingDashboardAuthError::Forbidden);
        }
        (principal.token_id > 0 && principal.user_id > 0)
            .then_some(principal)
            .ok_or(BillingDashboardAuthError::Unauthorized)
    }
}

/// Legacy display setting read by the billing handlers.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum QuotaDisplay {
    Usd,
    Cny,
    Tokens,
}

#[derive(Clone, Copy, Debug)]
pub struct BillingDashboardSettings {
    pub display_token_stat_enabled: bool,
    pub quota_display: QuotaDisplay,
    pub quota_per_unit: Decimal,
    pub credits_per_usd: Option<Decimal>,
    pub legacy_pricing_quota_per_unit: Option<Decimal>,
    pub usd_exchange_rate: f64,
}

impl Default for BillingDashboardSettings {
    fn default() -> Self {
        Self {
            // `common.DisplayTokenStatEnabled` defaults to true in Go.
            display_token_stat_enabled: true,
            quota_display: QuotaDisplay::Usd,
            quota_per_unit: Decimal::from(DEFAULT_QUOTA_PER_UNIT),
            credits_per_usd: None,
            legacy_pricing_quota_per_unit: None,
            usd_exchange_rate: DEFAULT_USD_EXCHANGE_RATE,
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum BillingDashboardStoreError {
    Unavailable,
    CurrencyUnavailable,
}

impl std::fmt::Display for BillingDashboardStoreError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(match self {
            Self::Unavailable => "billing store unavailable",
            Self::CurrencyUnavailable => "credit currency units are unavailable",
        })
    }
}

impl std::error::Error for BillingDashboardStoreError {}

/// Durable data used by the two legacy handlers.
#[async_trait]
pub trait BillingDashboardStore: Send + Sync {
    async fn settings(&self) -> Result<BillingDashboardSettings, BillingDashboardStoreError>;
    async fn user_quota(&self, user_id: i64) -> Result<(i64, i64), BillingDashboardStoreError>;
}

/// PostgreSQL option/user adapter for dashboard billing reads.
#[derive(Clone)]
pub struct PgBillingDashboardStore {
    pg: PgPool,
}

impl PgBillingDashboardStore {
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self { pg }
    }
}

#[async_trait]
impl BillingDashboardStore for PgBillingDashboardStore {
    async fn settings(&self) -> Result<BillingDashboardSettings, BillingDashboardStoreError> {
        let rows = sqlx::query(
            "SELECT key, value FROM options \
             WHERE key IN ('QuotaPerUnit', 'CreditsPerUSD', 'LegacyPricingQuotaPerUnit', \
                           'USDExchangeRate', \
                           'general_setting.quota_display_type', 'general_setting', \
                           'DisplayTokenStatEnabled')",
        )
        .fetch_all(&self.pg)
        .await
        .map_err(|_| BillingDashboardStoreError::Unavailable)?;
        let mut settings = BillingDashboardSettings::default();
        let mut dotted_display = None;
        let mut aggregate_display = None;
        for row in rows {
            let key: String = row
                .try_get("key")
                .map_err(|_| BillingDashboardStoreError::Unavailable)?;
            let value: Option<String> = row
                .try_get("value")
                .map_err(|_| BillingDashboardStoreError::Unavailable)?;
            let value = value.ok_or_else(|| {
                if matches!(
                    key.as_str(),
                    "QuotaPerUnit" | "CreditsPerUSD" | "LegacyPricingQuotaPerUnit"
                ) {
                    BillingDashboardStoreError::CurrencyUnavailable
                } else {
                    BillingDashboardStoreError::Unavailable
                }
            })?;
            match key.as_str() {
                "QuotaPerUnit" => settings.quota_per_unit = parse_credit_rate(&value)?,
                "CreditsPerUSD" => settings.credits_per_usd = Some(parse_credit_rate(&value)?),
                "LegacyPricingQuotaPerUnit" => {
                    settings.legacy_pricing_quota_per_unit = Some(parse_credit_rate(&value)?);
                }
                "USDExchangeRate" => {
                    if let Some(value) = positive_finite(&value) {
                        settings.usd_exchange_rate = value;
                    }
                }
                "DisplayTokenStatEnabled" => {
                    settings.display_token_stat_enabled =
                        value.eq_ignore_ascii_case("true") || value == "1";
                }
                "general_setting.quota_display_type" => {
                    dotted_display = parse_quota_display_type(&value);
                }
                "general_setting" => aggregate_display = parse_aggregate_display_type(&value),
                _ => {}
            }
        }
        if let Some(display) = dotted_display.or(aggregate_display) {
            settings.quota_display = display;
        }
        validated_credit_basis(settings)?;
        Ok(settings)
    }

    async fn user_quota(&self, user_id: i64) -> Result<(i64, i64), BillingDashboardStoreError> {
        let row = sqlx::query(
            "SELECT quota, used_quota FROM users WHERE id = $1 AND deleted_at IS NULL LIMIT 1",
        )
        .bind(user_id)
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| BillingDashboardStoreError::Unavailable)?
        .ok_or(BillingDashboardStoreError::Unavailable)?;
        Ok((
            row.try_get("quota")
                .map_err(|_| BillingDashboardStoreError::Unavailable)?,
            row.try_get("used_quota")
                .map_err(|_| BillingDashboardStoreError::Unavailable)?,
        ))
    }
}

#[derive(Clone)]
pub struct BillingDashboardState {
    store: Arc<dyn BillingDashboardStore>,
    authorizer: Arc<dyn BillingDashboardAuthorizer>,
}

impl BillingDashboardState {
    #[must_use]
    pub fn new(
        store: Arc<dyn BillingDashboardStore>,
        authorizer: Arc<dyn BillingDashboardAuthorizer>,
    ) -> Self {
        Self { store, authorizer }
    }
}

/// All four legacy aliases are kept because SDKs use both versioned and
/// unversioned paths.
pub fn billing_dashboard_router(state: BillingDashboardState) -> Router {
    Router::new()
        .route("/dashboard/billing/subscription", get(subscription))
        .route("/v1/dashboard/billing/subscription", get(subscription))
        .route("/dashboard/billing/usage", get(usage))
        .route("/v1/dashboard/billing/usage", get(usage))
        .with_state(state)
}

async fn subscription(State(state): State<BillingDashboardState>, request: Request) -> Response {
    let principal = match state.authorizer.authorize(request_context(&request)).await {
        Ok(principal) => principal,
        Err(error) => return auth_failure(error, &request),
    };
    let settings = match state.store.settings().await {
        Ok(settings) => settings,
        Err(BillingDashboardStoreError::CurrencyUnavailable) => return currency_error(),
        Err(BillingDashboardStoreError::Unavailable) => {
            return legacy_error("billing unavailable", "upstream_error");
        }
    };
    let anchor = match validated_credit_basis(settings) {
        Ok(anchor) => anchor,
        Err(_) => return currency_error(),
    };
    let (remain, used) = match quota_for(&state, principal, settings).await {
        Ok(quota) => quota,
        Err(_) => return legacy_error("billing unavailable", "upstream_error"),
    };
    let amount = if principal.unlimited_quota && settings.display_token_stat_enabled {
        100_000_000.0
    } else {
        let credits = i128::from(remain) + i128::from(used);
        match real_usd_amount(credits, anchor) {
            Ok(amount) => amount,
            Err(_) => return currency_error(),
        }
    };
    Json(OpenAiSubscription {
        object: "billing_subscription",
        has_payment_method: true,
        soft_limit_usd: amount,
        hard_limit_usd: amount,
        system_hard_limit_usd: amount,
        access_until: principal.expired_time.max(0),
    })
    .into_response()
}

async fn usage(State(state): State<BillingDashboardState>, request: Request) -> Response {
    let principal = match state.authorizer.authorize(request_context(&request)).await {
        Ok(principal) => principal,
        Err(error) => return auth_failure(error, &request),
    };
    let settings = match state.store.settings().await {
        Ok(settings) => settings,
        Err(BillingDashboardStoreError::CurrencyUnavailable) => return currency_error(),
        Err(BillingDashboardStoreError::Unavailable) => {
            return legacy_error("billing unavailable", "new_api_error");
        }
    };
    let anchor = match validated_credit_basis(settings) {
        Ok(anchor) => anchor,
        Err(_) => return currency_error(),
    };
    let (_, used) = match quota_for(&state, principal, settings).await {
        Ok(quota) => quota,
        Err(_) => return legacy_error("billing unavailable", "new_api_error"),
    };
    let amount = match real_usd_amount(i128::from(used), anchor) {
        Ok(amount) => amount,
        Err(_) => return currency_error(),
    };
    let total_usage = amount * 100.0;
    if !total_usage.is_finite() {
        return currency_error();
    }
    Json(OpenAiUsage {
        object: "list",
        total_usage,
    })
    .into_response()
}

async fn quota_for(
    state: &BillingDashboardState,
    principal: BillingDashboardPrincipal,
    settings: BillingDashboardSettings,
) -> Result<(i64, i64), BillingDashboardStoreError> {
    if settings.display_token_stat_enabled {
        Ok((principal.remain_quota, principal.used_quota))
    } else {
        state.store.user_quota(principal.user_id).await
    }
}

// These protocol fields are always real USD; UI display and FX are unrelated.
fn real_usd_amount(credits: i128, anchor: Decimal) -> Result<f64, BillingDashboardStoreError> {
    let denominator = u128::try_from(anchor.mantissa())
        .ok()
        .filter(|denominator| *denominator > 0)
        .ok_or(BillingDashboardStoreError::CurrencyUnavailable)?;
    let numerator = credits.unsigned_abs();
    let mut remainder = numerator % denominator;
    let mut digits = (numerator / denominator).to_string().into_bytes();
    // Exact long division: multiplying a 96-bit Decimal mantissa remainder by
    // ten fits u128. Generate the anchor scale's integer digits and exactly 16
    // fraction digits, then round ONCE using the next digit. A preliminary
    // Decimal division would double-round near a half-unit boundary.
    for _ in 0..anchor.scale() + 16 {
        remainder *= 10;
        digits.push(
            b'0' + u8::try_from(remainder / denominator)
                .map_err(|_| BillingDashboardStoreError::CurrencyUnavailable)?,
        );
        remainder %= denominator;
    }
    if remainder * 10 / denominator >= 5 {
        let mut carry = true;
        for digit in digits.iter_mut().rev() {
            if *digit == b'9' {
                *digit = b'0';
            } else {
                *digit += 1;
                carry = false;
                break;
            }
        }
        if carry {
            digits.insert(0, b'1');
        }
    }
    let integer_length = digits.len() - 16;
    digits.insert(integer_length, b'.');
    if credits < 0 {
        digits.insert(0, b'-');
    }
    let amount = std::str::from_utf8(&digits)
        .ok()
        .and_then(|amount| amount.parse::<f64>().ok())
        .filter(|amount| amount.is_finite() && (credits == 0 || *amount != 0.0))
        .ok_or(BillingDashboardStoreError::CurrencyUnavailable)?;
    Ok(amount)
}

fn parse_credit_rate(raw: &str) -> Result<Decimal, BillingDashboardStoreError> {
    let value = raw.trim();
    if value.len() > 128
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || b"+-.eE".contains(&byte))
    {
        return Err(BillingDashboardStoreError::CurrencyUnavailable);
    }
    // Never let decimal parsing round away a tiny persisted calibration drift.
    let mantissa = value.split(['e', 'E']).next().unwrap_or_default();
    Decimal::from_str_exact(mantissa)
        .map_err(|_| BillingDashboardStoreError::CurrencyUnavailable)?;
    let value = Decimal::from_str_exact(value)
        .or_else(|_| Decimal::from_scientific(value))
        .map_err(|_| BillingDashboardStoreError::CurrencyUnavailable)?;
    if value <= Decimal::ZERO || value > Decimal::from(MAX_WALLET_QUOTA) {
        return Err(BillingDashboardStoreError::CurrencyUnavailable);
    }
    Ok(value)
}

fn validated_credit_basis(
    settings: BillingDashboardSettings,
) -> Result<Decimal, BillingDashboardStoreError> {
    let anchor = settings
        .credits_per_usd
        .ok_or(BillingDashboardStoreError::CurrencyUnavailable)?;
    let legacy = settings
        .legacy_pricing_quota_per_unit
        .ok_or(BillingDashboardStoreError::CurrencyUnavailable)?;
    let maximum = Decimal::from(MAX_WALLET_QUOTA);
    if anchor <= Decimal::ZERO
        || anchor > maximum
        || legacy <= Decimal::ZERO
        || legacy > maximum
        || settings.quota_per_unit != legacy
    {
        return Err(BillingDashboardStoreError::CurrencyUnavailable);
    }
    // Go's retained runtime calibration is float64, then NewFromFloat's
    // shortest decimal. Refuse a persisted Q that cannot survive that same
    // projection; otherwise Rust would accept a basis rejected by Go.
    let runtime_q = settings
        .quota_per_unit
        .to_string()
        .parse::<f64>()
        .map_err(|_| BillingDashboardStoreError::CurrencyUnavailable)?;
    if !runtime_q.is_finite()
        || runtime_q <= 0.0
        || parse_credit_rate(&runtime_q.to_string())? != legacy
    {
        return Err(BillingDashboardStoreError::CurrencyUnavailable);
    }
    Ok(anchor)
}

fn currency_error() -> Response {
    Json(json!({"error": {
        "message": "credit currency units are unavailable", "type": "billing_unavailable",
        "param": "", "code": null,
    }}))
    .into_response()
}

fn parse_quota_display_type(value: &str) -> Option<QuotaDisplay> {
    match value.trim() {
        "CNY" => Some(QuotaDisplay::Cny),
        "TOKENS" => Some(QuotaDisplay::Tokens),
        "USD" | "CUSTOM" => Some(QuotaDisplay::Usd),
        _ => None,
    }
}

fn parse_aggregate_display_type(value: &str) -> Option<QuotaDisplay> {
    serde_json::from_str::<Value>(value)
        .ok()
        .and_then(|setting| {
            setting
                .get("quota_display_type")
                .and_then(Value::as_str)
                .map(str::to_owned)
        })
        .and_then(|value| parse_quota_display_type(&value))
}

#[derive(Serialize)]
struct OpenAiSubscription {
    object: &'static str,
    has_payment_method: bool,
    soft_limit_usd: f64,
    hard_limit_usd: f64,
    system_hard_limit_usd: f64,
    access_until: i64,
}

#[derive(Serialize)]
struct OpenAiUsage {
    object: &'static str,
    total_usage: f64,
}

fn legacy_error(message: &'static str, kind: &'static str) -> Response {
    // The Go handlers deliberately return 200 for storage failures and expose
    // the OpenAI-compatible error object directly, without an API envelope.
    Json(json!({"error": {"message": message, "type": kind}})).into_response()
}

fn auth_failure(error: BillingDashboardAuthError, request: &Request) -> Response {
    // `TokenAuth` writes this OpenAI-compatible envelope before either billing
    // handler touches its store.  The request ID comes from the listener
    // boundary in normal operation; generate one only for direct route tests.
    let request_id = request.extensions().get::<RequestContext>().map_or_else(
        || uuid::Uuid::new_v4().to_string(),
        |context| context.request_id.clone(),
    );
    let (status, message, code) = match error {
        BillingDashboardAuthError::Unauthorized => {
            let mut response =
                (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response();
            response.headers_mut().insert(
                header::CONTENT_TYPE,
                HeaderValue::from_static("application/json; charset=utf-8"),
            );
            return response;
        }
        BillingDashboardAuthError::Forbidden => (
            StatusCode::FORBIDDEN,
            "您的 IP 不在令牌允许访问的列表中",
            "access_denied",
        ),
        BillingDashboardAuthError::Unavailable => (
            StatusCode::INTERNAL_SERVER_ERROR,
            "Database error, please contact the administrator",
            "",
        ),
    };
    let (request_id, request_id_header) = match HeaderValue::from_str(&request_id) {
        Ok(value) => (request_id, value),
        Err(_) => (
            "request-id-unavailable".to_owned(),
            HeaderValue::from_static("request-id-unavailable"),
        ),
    };
    let mut response = (
        status,
        Json(json!({"error": {
            "message": format!("{message} (request id: {request_id})"),
            "type": "new_api_error",
            "code": code,
        }})),
    )
        .into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
        .headers_mut()
        .insert("x-oneapi-request-id", request_id_header);
    response
}

fn legacy_token_key(value: &str) -> Option<&str> {
    let value = value.trim_start();
    let value = value
        .strip_prefix("Bearer ")
        .or_else(|| value.strip_prefix("bearer "))
        .map_or(value, std::convert::identity)
        .trim();
    let value = value
        .strip_prefix("sk-")
        .map_or(value, std::convert::identity);
    let key = value.split('-').next()?;
    (!key.is_empty()).then_some(key)
}

fn request_context(request: &Request) -> BillingDashboardRequest {
    let authorization = request
        .headers()
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .map(str::to_owned);
    let client_ip = request
        .extensions()
        .get::<RequestContext>()
        .and_then(|context| context.client_ip)
        .or_else(|| {
            request
                .headers()
                .get("x-real-ip")
                .and_then(|value| value.to_str().ok())
                .and_then(|value| value.parse().ok())
        })
        .map_or(IpAddr::V4(Ipv4Addr::UNSPECIFIED), std::convert::identity);
    BillingDashboardRequest {
        authorization,
        client_ip,
    }
}

fn ip_is_allowed(client_ip: IpAddr, raw_limits: &str) -> bool {
    let limits = raw_limits
        .lines()
        .map(|line| line.replace([' ', ','], ""))
        .map(|line| line.trim().to_owned())
        .filter(|limit| !limit.is_empty())
        .collect::<Vec<_>>();
    limits.is_empty()
        || limits.iter().any(|limit| {
            limit
                .parse::<ipnet::IpNet>()
                .is_ok_and(|network| network.contains(&client_ip))
                || limit.parse::<IpAddr>().is_ok_and(|ip| ip == client_ip)
        })
}

fn positive_finite(value: &str) -> Option<f64> {
    let value = value.parse::<f64>().ok()?;
    (value.is_finite() && value > 0.0).then_some(value)
}

fn unix_seconds() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(i64::MAX, |duration| {
            i64::try_from(duration.as_secs()).map_or(i64::MAX, std::convert::identity)
        })
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{
        body::{Body, to_bytes},
        http::Request,
    };
    use std::{
        error::Error,
        io,
        sync::atomic::{AtomicUsize, Ordering},
    };
    use tower::ServiceExt;

    type TestResult<T = ()> = Result<T, Box<dyn Error>>;

    fn test_error(message: impl Into<String>) -> Box<dyn Error> {
        Box::new(io::Error::other(message.into()))
    }

    async fn response_json(response: Response, context: &'static str) -> TestResult<Value> {
        let body = to_bytes(response.into_body(), usize::MAX)
            .await
            .map_err(|error| test_error(format!("read {context} response body: {error}")))?;
        serde_json::from_slice::<Value>(&body)
            .map_err(|error| test_error(format!("decode {context} response JSON: {error}")))
    }

    #[derive(Clone)]
    struct StaticAuthorizer(BillingDashboardPrincipal);

    #[async_trait]
    impl BillingDashboardAuthorizer for StaticAuthorizer {
        async fn authorize(
            &self,
            _: BillingDashboardRequest,
        ) -> Result<BillingDashboardPrincipal, BillingDashboardAuthError> {
            Ok(self.0)
        }
    }

    #[derive(Clone)]
    struct StaticStore {
        settings: BillingDashboardSettings,
        user_quota: (i64, i64),
    }

    #[async_trait]
    impl BillingDashboardStore for StaticStore {
        async fn settings(&self) -> Result<BillingDashboardSettings, BillingDashboardStoreError> {
            Ok(self.settings)
        }

        async fn user_quota(&self, _: i64) -> Result<(i64, i64), BillingDashboardStoreError> {
            Ok(self.user_quota)
        }
    }

    #[derive(Clone)]
    struct RejectingAuthorizer(BillingDashboardAuthError);

    #[async_trait]
    impl BillingDashboardAuthorizer for RejectingAuthorizer {
        async fn authorize(
            &self,
            _: BillingDashboardRequest,
        ) -> Result<BillingDashboardPrincipal, BillingDashboardAuthError> {
            Err(self.0)
        }
    }

    #[derive(Clone, Default)]
    struct CountingStore {
        settings_calls: Arc<AtomicUsize>,
        quota_calls: Arc<AtomicUsize>,
    }

    #[async_trait]
    impl BillingDashboardStore for CountingStore {
        async fn settings(&self) -> Result<BillingDashboardSettings, BillingDashboardStoreError> {
            self.settings_calls.fetch_add(1, Ordering::SeqCst);
            Ok(BillingDashboardSettings::default())
        }

        async fn user_quota(&self, _: i64) -> Result<(i64, i64), BillingDashboardStoreError> {
            self.quota_calls.fetch_add(1, Ordering::SeqCst);
            Ok((0, 0))
        }
    }

    fn router(settings: BillingDashboardSettings) -> Router {
        router_with_principal(
            settings,
            BillingDashboardPrincipal {
                token_id: 1,
                user_id: 2,
                remain_quota: 750,
                used_quota: 250,
                unlimited_quota: false,
                expired_time: -1,
            },
            (900, 100),
        )
    }

    fn router_with_principal(
        settings: BillingDashboardSettings,
        principal: BillingDashboardPrincipal,
        user_quota: (i64, i64),
    ) -> Router {
        billing_dashboard_router(BillingDashboardState::new(
            Arc::new(StaticStore {
                settings,
                user_quota,
            }),
            Arc::new(StaticAuthorizer(principal)),
        ))
    }

    fn rejected_router(store: CountingStore) -> Router {
        billing_dashboard_router(BillingDashboardState::new(
            Arc::new(store),
            Arc::new(RejectingAuthorizer(BillingDashboardAuthError::Unauthorized)),
        ))
    }

    #[tokio::test]
    async fn subscription_preserves_legacy_token_quota_shape() -> TestResult {
        let request = Request::builder()
            .uri("/dashboard/billing/subscription")
            .body(Body::empty())
            .map_err(|error| test_error(format!("build subscription request: {error}")))?;
        let response = router(BillingDashboardSettings {
            quota_per_unit: Decimal::from(500),
            credits_per_usd: Some(Decimal::from(500)),
            legacy_pricing_quota_per_unit: Some(Decimal::from(500)),
            ..BillingDashboardSettings::default()
        })
        .oneshot(request)
        .await
        .map_err(|error| test_error(format!("dispatch subscription request: {error}")))?;
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            response_json(response, "subscription").await?,
            json!({
                "object": "billing_subscription",
                "has_payment_method": true,
                "soft_limit_usd": 2.0,
                "hard_limit_usd": 2.0,
                "system_hard_limit_usd": 2.0,
                "access_until": 0
            })
        );
        Ok(())
    }

    #[tokio::test]
    async fn usage_keeps_usd_cents_despite_cny_preference_and_versioned_alias() -> TestResult {
        let request = Request::builder()
            .uri("/v1/dashboard/billing/usage")
            .body(Body::empty())
            .map_err(|error| test_error(format!("build usage request: {error}")))?;
        let response = router(BillingDashboardSettings {
            quota_display: QuotaDisplay::Cny,
            quota_per_unit: Decimal::from(500),
            credits_per_usd: Some(Decimal::from(500)),
            legacy_pricing_quota_per_unit: Some(Decimal::from(500)),
            usd_exchange_rate: 2.0,
            ..BillingDashboardSettings::default()
        })
        .oneshot(request)
        .await
        .map_err(|error| test_error(format!("dispatch usage request: {error}")))?;
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            response_json(response, "usage").await?,
            json!({"object": "list", "total_usage": 50.0})
        );
        Ok(())
    }

    #[test]
    fn billing_display_type_prefers_registered_dotted_option() -> TestResult {
        assert_eq!(
            parse_quota_display_type("TOKENS"),
            Some(QuotaDisplay::Tokens)
        );
        assert_eq!(
            parse_aggregate_display_type(r#"{"quota_display_type":"CNY"}"#),
            Some(QuotaDisplay::Cny)
        );
        assert_eq!(parse_quota_display_type("CUSTOM"), Some(QuotaDisplay::Usd));
        assert_eq!(parse_quota_display_type("invalid"), None);
        Ok(())
    }

    fn canonical_settings() -> BillingDashboardSettings {
        BillingDashboardSettings {
            credits_per_usd: Some(Decimal::from(3_500_000)),
            legacy_pricing_quota_per_unit: Some(Decimal::from(500_000)),
            ..BillingDashboardSettings::default()
        }
    }

    #[test]
    fn immutable_basis_rejects_missing_invalid_or_drifted_values() -> TestResult {
        assert_eq!(
            validated_credit_basis(canonical_settings())?,
            Decimal::from(3_500_000)
        );
        for invalid in [
            "",
            "0",
            "-1",
            "NaN",
            "Infinity",
            "9007199254740992",
            "500_000",
            "1e999999999",
            "1e-999999999",
            "1.00000000000000000000000000001",
        ] {
            assert!(parse_credit_rate(invalid).is_err(), "{invalid}");
        }
        assert_eq!(parse_credit_rate("3.5e6")?, Decimal::from(3_500_000));
        assert_eq!(parse_credit_rate("500000.000")?, Decimal::from(500_000));
        for settings in [
            BillingDashboardSettings::default(),
            BillingDashboardSettings {
                credits_per_usd: None,
                ..canonical_settings()
            },
            BillingDashboardSettings {
                legacy_pricing_quota_per_unit: None,
                ..canonical_settings()
            },
            BillingDashboardSettings {
                credits_per_usd: Some(Decimal::ZERO),
                ..canonical_settings()
            },
            BillingDashboardSettings {
                credits_per_usd: Some(Decimal::from(-1)),
                ..canonical_settings()
            },
            BillingDashboardSettings {
                credits_per_usd: Some(Decimal::from(MAX_WALLET_QUOTA + 1)),
                ..canonical_settings()
            },
            BillingDashboardSettings {
                quota_per_unit: Decimal::from(500_001),
                ..canonical_settings()
            },
            BillingDashboardSettings {
                legacy_pricing_quota_per_unit: Some(Decimal::ZERO),
                ..canonical_settings()
            },
            BillingDashboardSettings {
                quota_per_unit: Decimal::from_str_exact("500000.00000000000000001")?,
                ..canonical_settings()
            },
            BillingDashboardSettings {
                quota_per_unit: Decimal::from_str_exact("500000.00000000000000001")?,
                legacy_pricing_quota_per_unit: Some(Decimal::from_str_exact(
                    "500000.00000000000000001",
                )?),
                ..canonical_settings()
            },
        ] {
            assert_eq!(
                validated_credit_basis(settings),
                Err(BillingDashboardStoreError::CurrencyUnavailable)
            );
        }
        Ok(())
    }

    #[test]
    fn real_usd_division_matches_go_precision_and_preserves_zero_and_wide_sum() -> TestResult {
        assert_eq!(real_usd_amount(3_500_000, Decimal::from(3_500_000))?, 1.0);
        assert_eq!(real_usd_amount(0, Decimal::from(3_500_000))?, 0.0);
        assert_eq!(
            real_usd_amount(1, Decimal::from(3_500_000))?,
            0.0000002857142857
        );
        let credits = i128::from(i64::MAX) + i128::from(i64::MAX);
        assert_eq!(
            real_usd_amount(credits, Decimal::from(2))?,
            9_223_372_036_854_776_000.0
        );
        assert_eq!(
            real_usd_amount(
                866_666_666_666_668,
                Decimal::from(8_666_666_666_666_667_i64)
            )?,
            0.1000000000000001
        );
        assert_eq!(
            real_usd_amount(
                -866_666_666_666_668,
                Decimal::from(8_666_666_666_666_667_i64)
            )?,
            -0.1000000000000001
        );
        Ok(())
    }

    #[tokio::test]
    async fn all_aliases_return_real_usd_independent_of_display_fx_and_ledger_source() -> TestResult
    {
        for display in [QuotaDisplay::Usd, QuotaDisplay::Cny, QuotaDisplay::Tokens] {
            for fx in [7.0, 9.9, 99.0, 0.0, f64::NAN, f64::INFINITY] {
                for token_statistics in [true, false] {
                    let app = router_with_principal(
                        BillingDashboardSettings {
                            quota_display: display,
                            usd_exchange_rate: fx,
                            display_token_stat_enabled: token_statistics,
                            ..canonical_settings()
                        },
                        BillingDashboardPrincipal {
                            token_id: 1,
                            user_id: 2,
                            remain_quota: 0,
                            used_quota: 3_500_000,
                            unlimited_quota: false,
                            expired_time: -1,
                        },
                        (0, 3_500_000),
                    );
                    for path in [
                        "/dashboard/billing/subscription",
                        "/v1/dashboard/billing/subscription",
                        "/dashboard/billing/usage",
                        "/v1/dashboard/billing/usage",
                    ] {
                        let request = Request::get(path).body(Body::empty())?;
                        let response = app.clone().oneshot(request).await?;
                        assert_eq!(response.status(), StatusCode::OK);
                        let body = response_json(response, path).await?;
                        if path.ends_with("subscription") {
                            assert_eq!(
                                body,
                                json!({"object":"billing_subscription", "has_payment_method":true,
                                "soft_limit_usd":1.0, "hard_limit_usd":1.0, "system_hard_limit_usd":1.0, "access_until":0})
                            );
                        } else {
                            assert_eq!(body, json!({"object":"list", "total_usage":100.0}));
                        }
                    }
                }
            }
        }
        Ok(())
    }

    #[tokio::test]
    async fn signed_balance_and_single_rounding_keep_go_sdk_units() -> TestResult {
        for (quota, anchor, dollars, cents) in [
            (-3_500_000, 3_500_000, -1.0, -100.0),
            (
                866_666_666_666_668,
                8_666_666_666_666_667_i64,
                0.1000000000000001,
                10.00000000000001,
            ),
            (
                -866_666_666_666_668,
                8_666_666_666_666_667_i64,
                -0.1000000000000001,
                -10.00000000000001,
            ),
        ] {
            let settings = BillingDashboardSettings {
                credits_per_usd: Some(Decimal::from(anchor)),
                ..canonical_settings()
            };
            let principal = BillingDashboardPrincipal {
                token_id: 1,
                user_id: 2,
                remain_quota: 0,
                used_quota: quota,
                unlimited_quota: false,
                expired_time: -1,
            };
            let app = router_with_principal(settings, principal, (0, 0));
            let response = app
                .clone()
                .oneshot(Request::get("/dashboard/billing/subscription").body(Body::empty())?)
                .await?;
            assert_eq!(
                response_json(response, "signed subscription").await?["hard_limit_usd"],
                dollars
            );
            let response = app
                .oneshot(Request::get("/dashboard/billing/usage").body(Body::empty())?)
                .await?;
            assert_eq!(
                response_json(response, "signed usage").await?["total_usage"],
                cents
            );
        }
        Ok(())
    }

    #[tokio::test]
    async fn billing_money_errors_and_zero_keep_openai_compatibility_shape() -> TestResult {
        let principal = BillingDashboardPrincipal {
            token_id: 1,
            user_id: 2,
            remain_quota: 0,
            used_quota: 0,
            unlimited_quota: false,
            expired_time: -1,
        };
        for path in [
            "/dashboard/billing/subscription",
            "/v1/dashboard/billing/subscription",
            "/dashboard/billing/usage",
            "/v1/dashboard/billing/usage",
        ] {
            for invalid in [
                BillingDashboardSettings::default(),
                BillingDashboardSettings {
                    quota_per_unit: Decimal::from(700_000),
                    ..canonical_settings()
                },
                BillingDashboardSettings {
                    credits_per_usd: Some(Decimal::ZERO),
                    ..canonical_settings()
                },
            ] {
                let app = router_with_principal(invalid, principal, (0, 0));
                let response = app.oneshot(Request::get(path).body(Body::empty())?).await?;
                assert_eq!(response.status(), StatusCode::OK);
                assert_eq!(
                    response_json(response, path).await?,
                    json!({"error":{
                    "message":"credit currency units are unavailable", "type":"billing_unavailable", "param":"", "code":null}})
                );
            }
            let app = router_with_principal(canonical_settings(), principal, (0, 0));
            let response = app.oneshot(Request::get(path).body(Body::empty())?).await?;
            let body = response_json(response, path).await?;
            assert_eq!(body.get("error"), None);
            if path.ends_with("subscription") {
                assert_eq!(body["hard_limit_usd"], 0.0);
            } else {
                assert_eq!(body["total_usage"], 0.0);
            }
        }
        // The existing unlimited sentinel also requires initialized currency.
        let unlimited = BillingDashboardPrincipal {
            unlimited_quota: true,
            ..principal
        };
        let app = router_with_principal(canonical_settings(), unlimited, (0, 0));
        let response = app
            .oneshot(Request::get("/dashboard/billing/subscription").body(Body::empty())?)
            .await?;
        assert_eq!(
            response_json(response, "unlimited").await?["hard_limit_usd"],
            100_000_000.0
        );
        let app = router_with_principal(BillingDashboardSettings::default(), unlimited, (0, 0));
        let response = app
            .oneshot(Request::get("/dashboard/billing/subscription").body(Body::empty())?)
            .await?;
        assert_eq!(
            response_json(response, "unlimited unavailable").await?["error"]["type"],
            "billing_unavailable"
        );
        Ok(())
    }

    #[tokio::test]
    async fn all_billing_aliases_return_legacy_token_auth_failure_before_store_reads() -> TestResult
    {
        let store = CountingStore::default();
        let settings_calls = Arc::clone(&store.settings_calls);
        let quota_calls = Arc::clone(&store.quota_calls);
        let app = rejected_router(store);

        for path in [
            "/dashboard/billing/subscription",
            "/dashboard/billing/usage",
            "/v1/dashboard/billing/subscription",
            "/v1/dashboard/billing/usage",
        ] {
            let mut request =
                Request::builder()
                    .uri(path)
                    .body(Body::empty())
                    .map_err(|error| {
                        test_error(format!("build billing auth request for {path}: {error}"))
                    })?;
            request.extensions_mut().insert(RequestContext {
                request_id: "billing-fixture-request-id".to_owned(),
                client_ip: None,
            });
            let response = app.clone().oneshot(request).await.map_err(|error| {
                test_error(format!("dispatch billing auth request for {path}: {error}"))
            })?;

            assert_eq!(response.status(), StatusCode::NOT_FOUND, "{path}");
            let content_type = response
                .headers()
                .get(header::CONTENT_TYPE)
                .ok_or_else(|| test_error(format!("missing content-type header for {path}")))?;
            assert_eq!(content_type, "application/json; charset=utf-8", "{path}");
            assert_eq!(
                response_json(response, path).await?,
                json!({"message": "Not Found"}),
                "{path}"
            );
        }

        assert_eq!(settings_calls.load(Ordering::SeqCst), 0);
        assert_eq!(quota_calls.load(Ordering::SeqCst), 0);
        Ok(())
    }

    #[test]
    fn token_normalization_matches_legacy_suffix_handling() -> TestResult {
        assert_eq!(legacy_token_key("Bearer sk-key-channel"), Some("key"));
        assert_eq!(legacy_token_key("bearer key-channel"), Some("key"));
        assert_eq!(legacy_token_key("Bearer "), None);
        Ok(())
    }
}
