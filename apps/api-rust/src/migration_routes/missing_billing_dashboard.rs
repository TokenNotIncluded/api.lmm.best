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
use serde::Serialize;
use serde_json::{Value, json};
use sqlx::{PgPool, Row};

use crate::RequestContext;

const DEFAULT_QUOTA_PER_UNIT: f64 = 500_000.0;
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
    pub quota_per_unit: f64,
    pub usd_exchange_rate: f64,
}

impl Default for BillingDashboardSettings {
    fn default() -> Self {
        Self {
            // `common.DisplayTokenStatEnabled` defaults to true in Go.
            display_token_stat_enabled: true,
            quota_display: QuotaDisplay::Usd,
            quota_per_unit: DEFAULT_QUOTA_PER_UNIT,
            usd_exchange_rate: DEFAULT_USD_EXCHANGE_RATE,
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum BillingDashboardStoreError {
    Unavailable,
}

/// Durable data used by the two legacy handlers.
#[async_trait]
pub trait BillingDashboardStore: Send + Sync {
    async fn settings(&self) -> Result<BillingDashboardSettings, BillingDashboardStoreError>;
    async fn user_quota(&self, user_id: i64) -> Result<(i64, i64), BillingDashboardStoreError>;
}

/// PostgreSQL option/user adapter shared by the billing migration surface.
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
             WHERE key IN ('QuotaPerUnit', 'USDExchangeRate', \
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
            let value: String = row
                .try_get("value")
                .map_err(|_| BillingDashboardStoreError::Unavailable)?;
            match key.as_str() {
                "QuotaPerUnit" => {
                    if let Some(value) = positive_finite(&value) {
                        settings.quota_per_unit = value;
                    }
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
        Err(_) => return legacy_error("billing unavailable", "upstream_error"),
    };
    let (remain, used) = match quota_for(&state, principal, settings).await {
        Ok(quota) => quota,
        Err(_) => return legacy_error("billing unavailable", "upstream_error"),
    };
    let amount = if principal.unlimited_quota && settings.display_token_stat_enabled {
        100_000_000.0
    } else {
        display_amount(remain.saturating_add(used), settings)
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
        Err(_) => return legacy_error("billing unavailable", "new_api_error"),
    };
    let (_, used) = match quota_for(&state, principal, settings).await {
        Ok(quota) => quota,
        Err(_) => return legacy_error("billing unavailable", "new_api_error"),
    };
    Json(OpenAiUsage {
        object: "list",
        total_usage: display_amount(used, settings) * 100.0,
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

fn display_amount(quota: i64, settings: BillingDashboardSettings) -> f64 {
    let amount = quota as f64;
    match settings.quota_display {
        QuotaDisplay::Cny => amount / settings.quota_per_unit * settings.usd_exchange_rate,
        QuotaDisplay::Tokens => amount,
        QuotaDisplay::Usd => amount / settings.quota_per_unit,
    }
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
    struct StaticStore(BillingDashboardSettings);

    #[async_trait]
    impl BillingDashboardStore for StaticStore {
        async fn settings(&self) -> Result<BillingDashboardSettings, BillingDashboardStoreError> {
            Ok(self.0)
        }

        async fn user_quota(&self, _: i64) -> Result<(i64, i64), BillingDashboardStoreError> {
            Ok((900, 100))
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
        billing_dashboard_router(BillingDashboardState::new(
            Arc::new(StaticStore(settings)),
            Arc::new(StaticAuthorizer(BillingDashboardPrincipal {
                token_id: 1,
                user_id: 2,
                remain_quota: 750,
                used_quota: 250,
                unlimited_quota: false,
                expired_time: -1,
            })),
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
            quota_per_unit: 500.0,
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
    async fn usage_preserves_cny_and_versioned_alias() -> TestResult {
        let request = Request::builder()
            .uri("/v1/dashboard/billing/usage")
            .body(Body::empty())
            .map_err(|error| test_error(format!("build usage request: {error}")))?;
        let response = router(BillingDashboardSettings {
            quota_display: QuotaDisplay::Cny,
            quota_per_unit: 500.0,
            usd_exchange_rate: 2.0,
            ..BillingDashboardSettings::default()
        })
        .oneshot(request)
        .await
        .map_err(|error| test_error(format!("dispatch usage request: {error}")))?;
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            response_json(response, "usage").await?,
            json!({"object": "list", "total_usage": 100.0})
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

    #[tokio::test]
    async fn all_billing_aliases_return_legacy_token_auth_failure_before_store_reads(
    ) -> TestResult {
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
            let mut request = Request::builder()
                .uri(path)
                .body(Body::empty())
                .map_err(|error| {
                    test_error(format!("build billing auth request for {path}: {error}"))
                })?;
            request.extensions_mut().insert(RequestContext {
                request_id: "billing-fixture-request-id".to_owned(),
                client_ip: None,
            });
            let response = app
                .clone()
                .oneshot(request)
                .await
                .map_err(|error| {
                    test_error(format!("dispatch billing auth request for {path}: {error}"))
                })?;

            assert_eq!(response.status(), StatusCode::NOT_FOUND, "{path}");
            let content_type = response
                .headers()
                .get(header::CONTENT_TYPE)
                .ok_or_else(|| test_error(format!("missing content-type header for {path}")))?;
            assert_eq!(
                content_type,
                "application/json; charset=utf-8",
                "{path}"
            );
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
