//! Legacy-compatible user top-up ePay routes.
//!
//! This slice deliberately keeps payment gateways behind explicit injected
//! boundaries.  A listener which has not supplied verified provider adapters
//! cannot create orders or settle callbacks: it responds with the legacy
//! failure envelope/plain-text acknowledgement and performs no network I/O.
//!
//! Go updates a paid order and wallet quota in separate writes. Rust requires
//! a repository `complete` transaction instead; this is safer for replay but
//! is not side-effect equivalent until a differential fixture proves the
//! production failure boundary. Consequently this module carries no approval
//! credit by itself.

use std::{collections::BTreeMap, sync::Arc};

use crate::{
    ClientIpKey, RequestContext,
    auth::{CriticalRateLimitOutcome, DashboardAuth},
    legacy_empty_response,
};
use async_trait::async_trait;
use axum::{
    Json, Router,
    body::{Body, Bytes, to_bytes},
    extract::{Request, State},
    http::{HeaderMap, Method, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use secrecy::SecretString;
use serde::Deserialize;
use serde_json::{Value, json};

const EPAY: &str = "epay";
const TOPUP_BODY_LIMIT_BYTES: usize = 1_048_576;

/// State for the legacy `/api/user/{pay,epay/notify}` surface.
///
/// There is intentionally no `Default`: constructing a router requires an
/// authenticated identity boundary, an order store, and provider adapters.
#[derive(Clone)]
pub struct UserTopupState {
    authorizer: Arc<dyn TopupAuthorizer>,
    repository: Arc<dyn TopupRepository>,
    epay: Arc<dyn EpayGateway>,
}

impl UserTopupState {
    #[must_use]
    pub fn new(
        authorizer: Arc<dyn TopupAuthorizer>,
        repository: Arc<dyn TopupRepository>,
        epay: Arc<dyn EpayGateway>,
    ) -> Self {
        Self {
            authorizer,
            repository,
            epay,
        }
    }
}

pub fn router(state: UserTopupState) -> Router {
    Router::new()
        .route("/api/user/epay/notify", get(epay_notify).post(epay_notify))
        .route("/api/user/pay", post(epay_pay))
        .with_state(state)
}

/// Auth is deliberately independent from request JSON/form data: callers
/// cannot choose the user that receives a top-up by providing an id field.
#[async_trait]
pub trait TopupAuthorizer: Send + Sync {
    async fn user_id(&self, headers: &HeaderMap) -> Result<i64, TopupError>;

    /// Applies the same IP-keyed critical limiter that Go attaches to both
    /// payment-creation routes.  This is intentionally a required adapter
    /// boundary: a listener cannot compose a live payment route without
    /// wiring it to the listener-owned PostgreSQL/Valkey auth policy.
    async fn check_critical_rate_limit(
        &self,
        client_ip: &str,
    ) -> Result<CriticalRateLimitOutcome, TopupError>;
}

/// Store and completion boundary. Implementations must make `complete` an
/// idempotent transaction: lock by `trade_no`, require the provider match,
/// transition pending orders exactly once, and credit the persisted order's
/// amount (never a callback supplied amount).
#[async_trait]
pub trait TopupRepository: Send + Sync {
    async fn minimum_amount(&self) -> Result<i64, TopupError>;
    async fn payment_method_allowed(&self, method: &str) -> Result<bool, TopupError>;
    /// Calculates the server-authoritative group/price/discount/token-display
    /// conversion before either payment gateway signs a checkout.
    async fn quote(&self, _: CreateTopup) -> Result<QuotedTopup, TopupError> {
        Err(TopupError::Storage)
    }
    async fn create_pending(&self, request: CreateTopup) -> Result<PendingTopup, TopupError>;
    /// Persists a locally prepared ePay order.  ePay signs its checkout before
    /// Go inserts the order, so a conforming implementation must keep the
    /// supplied trade number and money value intact rather than regenerating
    /// either after the checkout has been signed.
    async fn insert_prepared_pending(&self, _: PendingTopup) -> Result<(), TopupError> {
        Err(TopupError::Storage)
    }
    async fn complete(
        &self,
        trade_no: &str,
        provider: &str,
        payment_method: Option<&str>,
        callback: &str,
    ) -> Result<Completion, TopupError>;
    /// Subscription order completion is a separate durable state machine in
    /// Go. A top-up repository without that transaction must reject it rather
    /// than treating `SUBUSR*` as a wallet credit.
    async fn complete_subscription(
        &self,
        _: &str,
        _: &str,
        _: &str,
    ) -> Result<Completion, TopupError> {
        Err(TopupError::Storage)
    }
}

/// The amount is the platform top-up unit after legacy `int64(float64)`
/// truncation. The repository computes and persists money from the user's
/// current group and configured ratio; no client or callback money is trusted.
#[derive(Clone, Debug)]
pub struct CreateTopup {
    pub user_id: i64,
    pub amount: i64,
    pub payment_method: String,
    pub provider: &'static str,
}

/// Server-priced top-up data after group pricing, discounts, and any token
/// display conversion. `stored_amount` deliberately need not equal the user
/// supplied `requested_amount`.
#[derive(Clone, Debug)]
pub struct QuotedTopup {
    pub user_id: i64,
    pub requested_amount: i64,
    pub stored_amount: i64,
    pub money: String,
    pub payment_method: String,
    pub provider: &'static str,
}

#[derive(Clone, Debug)]
pub struct PendingTopup {
    pub trade_no: String,
    pub user_id: i64,
    pub amount: i64,
    /// Exactly two decimal places, calculated from persisted server settings.
    pub money: String,
    pub payment_method: String,
    pub provider: String,
}

/// The ePay checkout and the exact order it signs.  Constructing this value
/// is local (the Go `Purchase` call); persisting it is a separate, later step.
#[derive(Clone, Debug)]
pub struct PreparedEpay {
    pub order: PendingTopup,
    pub checkout: Checkout,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Completion {
    Completed,
    AlreadySucceeded,
    MissingOrWrongProvider,
}

/// ePay’s `Purchase` and `Verify` contracts.  Implementations may construct
/// signed browser parameters locally, but must never use caller-controlled
/// callback URLs or money values.
#[async_trait]
pub trait EpayGateway: Send + Sync {
    async fn available(&self) -> Result<(), TopupError>;
    /// Locally constructs the signed ePay request and its pending order before
    /// the repository is allowed to write.  The default deliberately fails
    /// closed so pre-existing adapters cannot accidentally retain a pending
    /// order on checkout-construction failure.
    async fn prepare(&self, _: &QuotedTopup) -> Result<PreparedEpay, TopupError> {
        Err(TopupError::ProviderFrozen)
    }
    /// Compatibility hook for adapters that only support the old persisted
    /// order API. New ePay adapters must implement `prepare` instead.
    async fn begin(&self, order: &PendingTopup) -> Result<Checkout, TopupError>;
    async fn verify(&self, fields: &EpayCallbackFields) -> Result<EpayCallback, TopupError>;
}

/// Dashboard-session authorizer for the user ePay surface.
///
/// Identity comes only from the listener-owned session authority. Payment
/// creation still requires a live repository and gateway; this type only
/// answers who the caller is and whether the critical limiter allows them.
#[derive(Clone)]
pub struct DashboardTopupAuthorizer {
    auth: Arc<dyn DashboardAuth>,
}

impl DashboardTopupAuthorizer {
    #[must_use]
    pub fn new(auth: Arc<dyn DashboardAuth>) -> Self {
        Self { auth }
    }
}

#[async_trait]
impl TopupAuthorizer for DashboardTopupAuthorizer {
    async fn user_id(&self, headers: &HeaderMap) -> Result<i64, TopupError> {
        let token = headers
            .get(header::AUTHORIZATION)
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.strip_prefix("Bearer "))
            .filter(|token| !token.is_empty())
            .ok_or(TopupError::Unauthorized)?;
        let user = self
            .auth
            .self_user(SecretString::from(token.to_owned()))
            .await
            .map_err(|_| TopupError::Unauthorized)?;
        (user.id > 0 && user.status == 1)
            .then_some(user.id)
            .ok_or(TopupError::Unauthorized)
    }

    async fn check_critical_rate_limit(
        &self,
        client_ip: &str,
    ) -> Result<CriticalRateLimitOutcome, TopupError> {
        self.auth
            .check_critical_rate_limit(client_ip)
            .await
            .map_err(|_| TopupError::Storage)
    }
}

/// Fail-closed order store for listeners that have not wired a live ledger.
pub struct DisabledTopupRepository;

#[async_trait]
impl TopupRepository for DisabledTopupRepository {
    async fn minimum_amount(&self) -> Result<i64, TopupError> {
        Err(TopupError::ProviderFrozen)
    }
    async fn payment_method_allowed(&self, _: &str) -> Result<bool, TopupError> {
        Err(TopupError::ProviderFrozen)
    }
    async fn create_pending(&self, _: CreateTopup) -> Result<PendingTopup, TopupError> {
        Err(TopupError::ProviderFrozen)
    }
    async fn complete(
        &self,
        _: &str,
        _: &str,
        _: Option<&str>,
        _: &str,
    ) -> Result<Completion, TopupError> {
        Err(TopupError::ProviderFrozen)
    }
}

/// Safe adapters for test instances and incomplete listener composition.
/// They are not fakes: all financial operations fail before a write/network
/// request, and all callbacks are rejected.
pub struct DisabledEpayGateway;
#[async_trait]
impl EpayGateway for DisabledEpayGateway {
    async fn available(&self) -> Result<(), TopupError> {
        Err(TopupError::ProviderFrozen)
    }
    async fn begin(&self, _: &PendingTopup) -> Result<Checkout, TopupError> {
        Err(TopupError::ProviderFrozen)
    }
    async fn verify(&self, _: &EpayCallbackFields) -> Result<EpayCallback, TopupError> {
        Err(TopupError::ProviderFrozen)
    }
}
#[derive(Clone, Debug)]
pub struct Checkout {
    pub url: String,
    pub data: Value,
}

/// Raw `url.Values`-compatible callback fields. Go retains non-UTF-8 bytes in
/// strings after `url.QueryUnescape`; converting them through UTF-8 would
/// change the bytes the ePay verifier signs.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct EpayCallbackFields(BTreeMap<Vec<u8>, Vec<u8>>);

impl EpayCallbackFields {
    fn insert_first(&mut self, key: Vec<u8>, value: Vec<u8>) {
        self.0.entry(key).or_insert(value);
    }

    #[must_use]
    pub fn values(&self) -> &BTreeMap<Vec<u8>, Vec<u8>> {
        &self.0
    }

    fn is_empty(&self) -> bool {
        self.0.is_empty()
    }
}

#[derive(Clone, Debug)]
pub struct EpayCallback {
    pub verified: bool,
    pub trade_success: bool,
    pub trade_no: String,
    pub payment_method: String,
}

#[derive(Debug)]
pub enum TopupError {
    Unauthorized,
    Storage,
    Provider,
    ProviderFrozen,
}

#[derive(Deserialize)]
struct PayJson {
    amount: f64,
    #[serde(default)]
    payment_method: String,
}

async fn epay_pay(State(state): State<UserTopupState>, request: Request) -> Response {
    let client_ip = critical_client_ip(&request);
    let (parts, body) = request.into_parts();
    let user_id = match state.authorizer.user_id(&parts.headers).await {
        Ok(id) if id > 0 => id,
        _ => return unauthorized(),
    };
    if let Some(response) = critical_rate_limit(&state, client_ip).await {
        return response;
    }
    let Some(body) = read_bounded_body(body).await else {
        return legacy_error("参数错误: request body too large");
    };
    let request = match parse_pay(&parts.headers, &body, parts.uri.query()) {
        Ok(v) => v,
        Err(message) => return legacy_error(message),
    };
    if !matches!(
        state
            .repository
            .payment_method_allowed(&request.payment_method)
            .await,
        Ok(true)
    ) {
        return legacy_error("支付方式不存在");
    }
    create_epay_checkout(
        &state,
        user_id,
        request.amount as i64,
        request.payment_method,
    )
    .await
}

async fn create_epay_checkout(
    state: &UserTopupState,
    user_id: i64,
    requested_amount: i64,
    payment_method: String,
) -> Response {
    let quote = match quote_topup(state, user_id, requested_amount, payment_method, EPAY).await {
        Ok(quote) => quote,
        Err(QuoteFailure::Minimum(minimum)) => {
            return legacy_error(format!("充值数量不能小于 {minimum}"));
        }
        Err(QuoteFailure::TooLow) => return legacy_error("充值金额过低"),
        Err(QuoteFailure::Configuration) => return legacy_error("获取用户分组失败"),
    };
    if state.epay.available().await.is_err() {
        return legacy_error("当前管理员未配置支付信息");
    }
    let prepared = match state.epay.prepare(&quote).await {
        Ok(v) if prepared_order_matches(&v.order, &quote) => v,
        Ok(_) | Err(_) => return legacy_error("拉起支付失败"),
    };
    if state
        .repository
        .insert_prepared_pending(prepared.order)
        .await
        .is_err()
    {
        return legacy_error("创建订单失败");
    }
    legacy_success(prepared.checkout.data, prepared.checkout.url)
}

async fn quote_topup(
    state: &UserTopupState,
    user_id: i64,
    requested_amount: i64,
    payment_method: String,
    provider: &'static str,
) -> Result<QuotedTopup, QuoteFailure> {
    let minimum = state
        .repository
        .minimum_amount()
        .await
        .map_err(|_| QuoteFailure::Configuration)?;
    if requested_amount < minimum {
        return Err(QuoteFailure::Minimum(minimum));
    }
    let request = CreateTopup {
        user_id,
        amount: requested_amount,
        payment_method,
        provider,
    };
    let quote = state
        .repository
        .quote(request)
        .await
        .map_err(|_| QuoteFailure::Configuration)?;
    if quote.user_id != user_id
        || quote.requested_amount != requested_amount
        || quote.provider != provider
        || quote.stored_amount < 0
    {
        return Err(QuoteFailure::Configuration);
    }
    if !quote
        .money
        .parse::<f64>()
        .is_ok_and(|money| money.is_finite())
    {
        return Err(QuoteFailure::Configuration);
    }
    if quote.money.parse::<f64>().is_ok_and(|money| money < 0.01) {
        return Err(QuoteFailure::TooLow);
    }
    Ok(quote)
}

enum QuoteFailure {
    Minimum(i64),
    TooLow,
    Configuration,
}

fn prepared_order_matches(order: &PendingTopup, quote: &QuotedTopup) -> bool {
    order.user_id == quote.user_id
        && order.amount == quote.stored_amount
        && order.money == quote.money
        && order.payment_method == quote.payment_method
        && order.provider == quote.provider
        && !order.trade_no.is_empty()
}

fn critical_client_ip(request: &Request) -> Option<String> {
    request
        .extensions()
        .get::<ClientIpKey>()
        .map(|value| value.0.clone())
        .or_else(|| {
            request
                .extensions()
                .get::<RequestContext>()
                .and_then(|context| context.client_ip)
                .map(|address| address.to_string())
        })
}

async fn critical_rate_limit(
    state: &UserTopupState,
    client_ip: Option<String>,
) -> Option<Response> {
    let Some(client_ip) = client_ip else {
        return Some(legacy_empty_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            None,
        ));
    };
    match state.authorizer.check_critical_rate_limit(&client_ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => None,
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => Some(legacy_empty_response(
            StatusCode::TOO_MANY_REQUESTS,
            Some(retry_after_seconds),
        )),
        Err(_) => Some(legacy_empty_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            None,
        )),
    }
}

async fn epay_notify(State(state): State<UserTopupState>, request: Request) -> Response {
    let (parts, body) = request.into_parts();
    let fields = match parse_epay_notify_fields(
        &parts.method,
        &parts.headers,
        parts.uri.query(),
        body,
    )
    .await
    {
        Some(fields) if !fields.is_empty() => fields,
        _ => return plain("fail"),
    };
    let verified = match state.epay.verify(&fields).await {
        Ok(v) if v.verified => v,
        _ => return plain("fail"),
    };
    // Legacy sends success after signature validation, including duplicate,
    // unknown, or provider-mismatch notifications; completion is idempotent.
    if verified.trade_success {
        let _ = state
            .repository
            .complete(
                &verified.trade_no,
                EPAY,
                Some(&verified.payment_method),
                &fields_json(&fields),
            )
            .await;
    }
    plain("success")
}

#[derive(Clone)]
struct Pay {
    amount: f64,
    payment_method: String,
}
fn parse_pay(headers: &HeaderMap, body: &[u8], query: Option<&str>) -> Result<Pay, String> {
    let mut amount = 0.0;
    let mut method = String::new();
    let json_body = headers
        .get(header::CONTENT_TYPE)
        .and_then(|v| v.to_str().ok())
        .is_some_and(|v| v.to_ascii_lowercase().contains("application/json"));
    if json_body && let Ok(parsed) = serde_json::from_slice::<PayJson>(body) {
        amount = parsed.amount;
        method = parsed.payment_method;
    }
    // Gin chooses its binder from Content-Type; an unlabelled JSON-looking
    // body is treated as a form, while an invalid JSON body is not reparsed as
    // form data.
    let form = if !json_body {
        parse_form(body)
    } else {
        Default::default()
    };
    if amount <= 0.0 {
        amount = form
            .get("amount")
            .and_then(|v| v.parse().ok())
            .or_else(|| {
                query.and_then(|q| {
                    parse_form(q.as_bytes())
                        .get("amount")
                        .and_then(|v| v.parse().ok())
                })
            })
            .unwrap_or(0.0);
    }
    if method.is_empty() {
        method = form
            .get("payment_method")
            .cloned()
            .or_else(|| query.and_then(|q| parse_form(q.as_bytes()).get("payment_method").cloned()))
            .unwrap_or_default();
    }
    if !amount.is_finite() || amount <= 0.0 {
        return Err("参数错误: amount is required and must be > 0".into());
    }
    Ok(Pay {
        amount,
        payment_method: method,
    })
}
async fn parse_epay_notify_fields(
    method: &Method,
    headers: &HeaderMap,
    query: Option<&str>,
    body: Body,
) -> Option<EpayCallbackFields> {
    if method == Method::GET {
        parse_epay_query_fields(query.unwrap_or_default().as_bytes())
    } else if method == Method::POST && is_urlencoded(headers) {
        parse_epay_post_fields(&read_bounded_body(body).await?)
    } else {
        None
    }
}

fn parse_epay_query_fields(raw: &[u8]) -> Option<EpayCallbackFields> {
    // url.URL.Query discards ParseQuery's error but retains every valid pair.
    // Keep raw decoded bytes because Go strings can contain non-UTF-8 data.
    let mut fields = EpayCallbackFields::default();
    for part in raw
        .split(|byte| *byte == b'&')
        .filter(|part| !part.is_empty())
    {
        if part.contains(&b';') {
            continue;
        }
        let Some((key, value)) = decode_form_pair(part) else {
            continue;
        };
        fields.insert_first(key, value);
    }
    Some(fields)
}

fn parse_epay_post_fields(raw: &[u8]) -> Option<EpayCallbackFields> {
    // Request.ParseForm returns an error for any malformed pair, so the POST
    // webhook rejects the whole body. Unlike Rust String parsing, this keeps
    // `%FF` as byte 0xff for provider signature verification.
    if raw.contains(&b';') {
        return None;
    }
    let mut fields = EpayCallbackFields::default();
    for part in raw
        .split(|byte| *byte == b'&')
        .filter(|part| !part.is_empty())
    {
        let (key, value) = decode_form_pair(part)?;
        fields.insert_first(key, value);
    }
    Some(fields)
}

fn decode_form_pair(part: &[u8]) -> Option<(Vec<u8>, Vec<u8>)> {
    let (key, value) = match part.iter().position(|byte| *byte == b'=') {
        Some(index) => {
            let (key, suffix) = part.split_at(index);
            (key, &suffix[1..])
        }
        None => (part, &[] as &[u8]),
    };
    Some((percent_decode_bytes(key)?, percent_decode_bytes(value)?))
}

fn percent_decode_bytes(value: &[u8]) -> Option<Vec<u8>> {
    let mut out = Vec::with_capacity(value.len());
    let mut i = 0;
    while i < value.len() {
        match value[i] {
            b'+' => out.push(b' '),
            b'%' => {
                let h = hex(*value.get(i + 1)?)?;
                let l = hex(*value.get(i + 2)?)?;
                out.push((h << 4) | l);
                i += 2;
            }
            byte => out.push(byte),
        }
        i += 1;
    }
    Some(out)
}

fn is_urlencoded(headers: &HeaderMap) -> bool {
    headers
        .get(header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| {
            value.split(';').next().is_some_and(|mime| {
                mime.trim()
                    .eq_ignore_ascii_case("application/x-www-form-urlencoded")
            })
        })
}

fn parse_form(raw: &[u8]) -> BTreeMap<String, String> {
    String::from_utf8_lossy(raw)
        .split('&')
        .filter_map(|part| {
            let (key, value) = part.split_once('=')?;
            Some((percent_decode(key), percent_decode(value)))
        })
        .fold(BTreeMap::new(), |mut fields, (key, value)| {
            fields.entry(key).or_insert(value);
            fields
        })
}
fn percent_decode(value: &str) -> String {
    let mut out = Vec::with_capacity(value.len());
    let bytes = value.as_bytes();
    let mut i = 0;
    while i < bytes.len() {
        match bytes[i] {
            b'+' => out.push(b' '),
            b'%' if i + 2 < bytes.len() => {
                let h = hex(bytes[i + 1]);
                let l = hex(bytes[i + 2]);
                if let (Some(h), Some(l)) = (h, l) {
                    out.push((h << 4) | l);
                    i += 2
                } else {
                    out.push(b'%')
                }
            }
            byte => out.push(byte),
        }
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}
fn hex(b: u8) -> Option<u8> {
    match b {
        b'0'..=b'9' => Some(b - b'0'),
        b'a'..=b'f' => Some(b - b'a' + 10),
        b'A'..=b'F' => Some(b - b'A' + 10),
        _ => None,
    }
}
fn fields_json(fields: &EpayCallbackFields) -> String {
    serde_json::to_string(
        &fields
            .values()
            .iter()
            .map(|(key, value)| json!({"key":key,"value":value}))
            .collect::<Vec<_>>(),
    )
    .unwrap_or_default()
}
async fn read_bounded_body(body: Body) -> Option<Bytes> {
    to_bytes(body, TOPUP_BODY_LIMIT_BYTES).await.ok()
}
fn plain(body: &'static str) -> Response {
    (StatusCode::OK, body).into_response()
}
fn legacy_error(message: impl Into<String>) -> Response {
    Json(json!({"message":"error","data":message.into()})).into_response()
}
fn legacy_success(data: Value, url: String) -> Response {
    Json(json!({"message":"success","data":data,"url":url})).into_response()
}
fn unauthorized() -> Response {
    (
        StatusCode::UNAUTHORIZED,
        Json(json!({"success":false,"code":"AUTH_UNAUTHORIZED","message":"Unauthorized, invalid access token"})),
    )
        .into_response()
}

#[cfg(test)]
mod tests {
    use super::*;
    use async_trait::async_trait;
    use axum::{
        body::{Body, to_bytes},
        http::{HeaderValue, Request},
    };
    use std::sync::{Mutex, MutexGuard};
    use tower::ServiceExt;

    type EpayApp = (Router, Arc<Mutex<Vec<&'static str>>>, Arc<Mutex<usize>>);
    type TestResult<T = ()> = Result<T, Box<dyn std::error::Error + Send + Sync>>;

    fn recover_lock<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
        match mutex.lock() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        }
    }

    fn test_error(message: impl Into<String>) -> Box<dyn std::error::Error + Send + Sync> {
        Box::new(std::io::Error::other(message.into()))
    }

    struct RejectingAuthorizer;

    #[async_trait]
    impl TopupAuthorizer for RejectingAuthorizer {
        async fn user_id(&self, _: &HeaderMap) -> Result<i64, TopupError> {
            Err(TopupError::Unauthorized)
        }

        async fn check_critical_rate_limit(
            &self,
            _: &str,
        ) -> Result<CriticalRateLimitOutcome, TopupError> {
            Ok(CriticalRateLimitOutcome::Allowed)
        }
    }

    struct NoopRepository;

    #[async_trait]
    impl TopupRepository for NoopRepository {
        async fn minimum_amount(&self) -> Result<i64, TopupError> {
            Err(TopupError::Storage)
        }
        async fn payment_method_allowed(&self, _: &str) -> Result<bool, TopupError> {
            Err(TopupError::Storage)
        }
        async fn create_pending(&self, _: CreateTopup) -> Result<PendingTopup, TopupError> {
            Err(TopupError::Storage)
        }
        async fn complete(
            &self,
            _: &str,
            _: &str,
            _: Option<&str>,
            _: &str,
        ) -> Result<Completion, TopupError> {
            Err(TopupError::Storage)
        }
    }

    struct NoopEpay;

    #[async_trait]
    impl EpayGateway for NoopEpay {
        async fn available(&self) -> Result<(), TopupError> {
            Err(TopupError::ProviderFrozen)
        }
        async fn begin(&self, _: &PendingTopup) -> Result<Checkout, TopupError> {
            Err(TopupError::ProviderFrozen)
        }
        async fn verify(&self, _: &EpayCallbackFields) -> Result<EpayCallback, TopupError> {
            Err(TopupError::ProviderFrozen)
        }
    }

    struct AllowingAuthorizer;

    #[async_trait]
    impl TopupAuthorizer for AllowingAuthorizer {
        async fn user_id(&self, _: &HeaderMap) -> Result<i64, TopupError> {
            Ok(42)
        }

        async fn check_critical_rate_limit(
            &self,
            _: &str,
        ) -> Result<CriticalRateLimitOutcome, TopupError> {
            Ok(CriticalRateLimitOutcome::Allowed)
        }
    }

    struct RejectingCriticalAuthorizer;

    #[async_trait]
    impl TopupAuthorizer for RejectingCriticalAuthorizer {
        async fn user_id(&self, _: &HeaderMap) -> Result<i64, TopupError> {
            Ok(42)
        }

        async fn check_critical_rate_limit(
            &self,
            _: &str,
        ) -> Result<CriticalRateLimitOutcome, TopupError> {
            Ok(CriticalRateLimitOutcome::Rejected {
                retry_after_seconds: 37,
            })
        }
    }

    struct RecordingRepository {
        events: Arc<Mutex<Vec<&'static str>>>,
        pending_writes: Arc<Mutex<usize>>,
        fail_insert: bool,
    }

    #[async_trait]
    impl TopupRepository for RecordingRepository {
        async fn minimum_amount(&self) -> Result<i64, TopupError> {
            Ok(1)
        }

        async fn payment_method_allowed(&self, _: &str) -> Result<bool, TopupError> {
            Ok(true)
        }

        async fn quote(&self, input: CreateTopup) -> Result<QuotedTopup, TopupError> {
            Ok(QuotedTopup {
                user_id: input.user_id,
                requested_amount: input.amount,
                stored_amount: input.amount,
                money: "1.00".into(),
                payment_method: input.payment_method,
                provider: input.provider,
            })
        }

        async fn create_pending(&self, _: CreateTopup) -> Result<PendingTopup, TopupError> {
            unreachable!("ePay must prepare before it persists")
        }

        async fn insert_prepared_pending(&self, _: PendingTopup) -> Result<(), TopupError> {
            recover_lock(&self.events).push("insert");
            if self.fail_insert {
                return Err(TopupError::Storage);
            }
            *recover_lock(&self.pending_writes) += 1;
            Ok(())
        }

        async fn complete(
            &self,
            _: &str,
            _: &str,
            _: Option<&str>,
            _: &str,
        ) -> Result<Completion, TopupError> {
            Err(TopupError::Storage)
        }
    }

    struct PreparingEpay {
        events: Arc<Mutex<Vec<&'static str>>>,
        fail_prepare: bool,
    }

    #[async_trait]
    impl EpayGateway for PreparingEpay {
        async fn available(&self) -> Result<(), TopupError> {
            Ok(())
        }

        async fn prepare(&self, input: &QuotedTopup) -> Result<PreparedEpay, TopupError> {
            recover_lock(&self.events).push("prepare");
            if self.fail_prepare {
                return Err(TopupError::Provider);
            }
            Ok(PreparedEpay {
                order: PendingTopup {
                    trade_no: "USR42NOsigned".into(),
                    user_id: input.user_id,
                    amount: input.stored_amount,
                    money: input.money.clone(),
                    payment_method: input.payment_method.clone(),
                    provider: input.provider.into(),
                },
                checkout: Checkout {
                    url: "https://epay.example/checkout".into(),
                    data: json!({"out_trade_no":"USR42NOsigned"}),
                },
            })
        }

        async fn begin(&self, _: &PendingTopup) -> Result<Checkout, TopupError> {
            unreachable!("new ePay flow uses prepare before insert")
        }

        async fn verify(&self, _: &EpayCallbackFields) -> Result<EpayCallback, TopupError> {
            Err(TopupError::ProviderFrozen)
        }
    }

    struct NotifyRepository {
        completions: Arc<Mutex<usize>>,
    }

    #[async_trait]
    impl TopupRepository for NotifyRepository {
        async fn minimum_amount(&self) -> Result<i64, TopupError> {
            Err(TopupError::Storage)
        }

        async fn payment_method_allowed(&self, _: &str) -> Result<bool, TopupError> {
            Err(TopupError::Storage)
        }

        async fn create_pending(&self, _: CreateTopup) -> Result<PendingTopup, TopupError> {
            Err(TopupError::Storage)
        }

        async fn complete(
            &self,
            _: &str,
            _: &str,
            _: Option<&str>,
            _: &str,
        ) -> Result<Completion, TopupError> {
            *recover_lock(&self.completions) += 1;
            Ok(Completion::Completed)
        }
    }

    struct VerifyingEpay {
        expected: EpayCallbackFields,
        verified: bool,
        calls: Arc<Mutex<usize>>,
    }

    #[async_trait]
    impl EpayGateway for VerifyingEpay {
        async fn available(&self) -> Result<(), TopupError> {
            Err(TopupError::ProviderFrozen)
        }

        async fn begin(&self, _: &PendingTopup) -> Result<Checkout, TopupError> {
            Err(TopupError::ProviderFrozen)
        }

        async fn verify(&self, fields: &EpayCallbackFields) -> Result<EpayCallback, TopupError> {
            *recover_lock(&self.calls) += 1;
            if fields != &self.expected {
                return Err(TopupError::Provider);
            }
            Ok(EpayCallback {
                verified: self.verified,
                trade_success: true,
                trade_no: "order-1".into(),
                payment_method: "alipay".into(),
            })
        }
    }

    fn notify_fields(entries: &[(&str, &str)]) -> EpayCallbackFields {
        let mut fields = EpayCallbackFields::default();
        for (key, value) in entries {
            fields.insert_first(key.as_bytes().to_vec(), value.as_bytes().to_vec());
        }
        fields
    }

    fn notify_app(
        expected: EpayCallbackFields,
        verified: bool,
    ) -> (Router, Arc<Mutex<usize>>, Arc<Mutex<usize>>) {
        let verify_calls = Arc::new(Mutex::new(0));
        let completions = Arc::new(Mutex::new(0));
        let router = router(UserTopupState::new(
            Arc::new(RejectingAuthorizer),
            Arc::new(NotifyRepository {
                completions: Arc::clone(&completions),
            }),
            Arc::new(VerifyingEpay {
                expected,
                verified,
                calls: Arc::clone(&verify_calls),
            }),
        ));
        (router, verify_calls, completions)
    }

    async fn notify_response(
        router: Router,
        request: Request<Body>,
    ) -> TestResult<(StatusCode, String)> {
        let response = router.oneshot(request).await?;
        let status = response.status();
        let bytes = to_bytes(response.into_body(), 1024)
            .await
            .map_err(|error| {
                test_error(format!("failed to read ePay notify response body: {error}"))
            })?;
        let body = String::from_utf8(bytes.to_vec()).map_err(|error| {
            test_error(format!(
                "ePay notify response body was not valid UTF-8: {error}"
            ))
        })?;
        Ok((status, body))
    }

    fn epay_app(fail_prepare: bool, fail_insert: bool) -> EpayApp {
        let events = Arc::new(Mutex::new(Vec::new()));
        let pending_writes = Arc::new(Mutex::new(0));
        let router = router(UserTopupState::new(
            Arc::new(AllowingAuthorizer),
            Arc::new(RecordingRepository {
                events: Arc::clone(&events),
                pending_writes: Arc::clone(&pending_writes),
                fail_insert,
            }),
            Arc::new(PreparingEpay {
                events: Arc::clone(&events),
                fail_prepare,
            }),
        ));
        (router, events, pending_writes)
    }

    async fn post_epay(router: Router) -> TestResult<(StatusCode, Value)> {
        let mut request = Request::post("/api/user/pay")
            .header(header::CONTENT_TYPE, "application/json")
            .body(Body::from(r#"{"amount":1,"payment_method":"alipay"}"#))?;
        request
            .extensions_mut()
            .insert(ClientIpKey("203.0.113.9".into()));
        let response = router.oneshot(request).await?;
        let status = response.status();
        let bytes = to_bytes(response.into_body(), 1024)
            .await
            .map_err(|error| test_error(format!("failed to read ePay response body: {error}")))?;
        let body = serde_json::from_slice(&bytes).map_err(|error| {
            test_error(format!("ePay response body was not valid JSON: {error}"))
        })?;
        Ok((status, body))
    }

    #[tokio::test]
    async fn epay_prepare_failure_leaves_no_pending_order() -> TestResult {
        let (router, events, pending_writes) = epay_app(true, false);
        let (status, body) = post_epay(router).await?;

        assert_eq!(status, StatusCode::OK);
        assert_eq!(body, json!({"message":"error","data":"拉起支付失败"}));
        assert_eq!(&*recover_lock(&events), &["prepare"]);
        assert_eq!(*recover_lock(&pending_writes), 0);
        Ok(())
    }

    #[tokio::test]
    async fn epay_insert_failure_keeps_legacy_response_after_local_prepare() -> TestResult {
        let (router, events, pending_writes) = epay_app(false, true);
        let (status, body) = post_epay(router).await?;

        assert_eq!(status, StatusCode::OK);
        assert_eq!(body, json!({"message":"error","data":"创建订单失败"}));
        assert_eq!(&*recover_lock(&events), &["prepare", "insert"]);
        assert_eq!(*recover_lock(&pending_writes), 0);
        Ok(())
    }

    #[tokio::test]
    async fn epay_success_prepares_then_inserts_before_responding() -> TestResult {
        let (router, events, pending_writes) = epay_app(false, false);
        let (status, body) = post_epay(router).await?;

        assert_eq!(status, StatusCode::OK);
        assert_eq!(
            body,
            json!({"message":"success","data":{"out_trade_no":"USR42NOsigned"},"url":"https://epay.example/checkout"})
        );
        assert_eq!(&*recover_lock(&events), &["prepare", "insert"]);
        assert_eq!(*recover_lock(&pending_writes), 1);
        Ok(())
    }

    #[tokio::test]
    async fn payment_critical_limiter_rejects_before_body_or_repository_work() -> TestResult {
        let events = Arc::new(Mutex::new(Vec::new()));
        let pending_writes = Arc::new(Mutex::new(0));
        let router = router(UserTopupState::new(
            Arc::new(RejectingCriticalAuthorizer),
            Arc::new(RecordingRepository {
                events: Arc::clone(&events),
                pending_writes: Arc::clone(&pending_writes),
                fail_insert: false,
            }),
            Arc::new(PreparingEpay {
                events: Arc::clone(&events),
                fail_prepare: false,
            }),
        ));

        for uri in ["/api/user/pay"] {
            let mut request = Request::post(uri)
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(r#"{"amount":1,"payment_method":"alipay"}"#))?;
            request
                .extensions_mut()
                .insert(ClientIpKey("203.0.113.9".into()));
            let response = router.clone().oneshot(request).await?;
            assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS, "{uri}");
            assert_eq!(
                response.headers().get(header::RETRY_AFTER),
                Some(&HeaderValue::from_static("37")),
                "{uri}"
            );
            assert!(
                to_bytes(response.into_body(), 1024).await?.is_empty(),
                "{uri}"
            );
        }
        assert!(recover_lock(&events).is_empty());
        assert_eq!(*recover_lock(&pending_writes), 0);
        Ok(())
    }

    #[tokio::test]
    async fn payment_critical_limiter_fails_closed_without_trusted_client_ip() -> TestResult {
        let (router, events, pending_writes) = epay_app(false, false);
        let response = router
            .oneshot(
                Request::post("/api/user/pay")
                    .header(header::CONTENT_TYPE, "application/json")
                    .body(Body::from(r#"{"amount":1,"payment_method":"alipay"}"#))?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::INTERNAL_SERVER_ERROR);
        assert!(to_bytes(response.into_body(), 1024).await?.is_empty());
        assert!(recover_lock(&events).is_empty());
        assert_eq!(*recover_lock(&pending_writes), 0);
        Ok(())
    }

    #[tokio::test]
    async fn epay_notify_uses_only_the_source_selected_by_its_method() -> TestResult {
        let query_fields = notify_fields(&[("trade_no", "query"), ("sign", "query-sign")]);
        let (router, verify_calls, completions) = notify_app(query_fields, true);
        let (status, body) = notify_response(
            router,
            Request::get("/api/user/epay/notify?trade_no=query&sign=query-sign")
                .body(Body::from("trade_no=body&sign=body-sign"))?,
        )
        .await?;

        assert_eq!(status, StatusCode::OK);
        assert_eq!(body, "success");
        assert_eq!(*recover_lock(&verify_calls), 1);
        assert_eq!(*recover_lock(&completions), 1);

        let body_fields = notify_fields(&[("trade_no", "body"), ("sign", "body-sign")]);
        let (router, verify_calls, completions) = notify_app(body_fields, true);
        let (status, body) = notify_response(
            router,
            Request::post("/api/user/epay/notify?trade_no=query&sign=query-sign")
                .header(
                    header::CONTENT_TYPE,
                    "application/x-www-form-urlencoded; charset=utf-8",
                )
                .body(Body::from("trade_no=body&sign=body-sign"))?,
        )
        .await?;

        assert_eq!(status, StatusCode::OK);
        assert_eq!(body, "success");
        assert_eq!(*recover_lock(&verify_calls), 1);
        assert_eq!(*recover_lock(&completions), 1);
        Ok(())
    }

    #[tokio::test]
    async fn epay_notify_rejects_malformed_or_wrong_transport_before_verification() -> TestResult {
        for request in [
            Request::post("/api/user/epay/notify?trade_no=order-1&sign=valid")
                .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
                .body(Body::from("trade_no=order-1&sign=%"))?,
            Request::post("/api/user/epay/notify?trade_no=order-1&sign=valid")
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(r#"{"trade_no":"order-1","sign":"valid"}"#))?,
        ] {
            let (router, verify_calls, completions) = notify_app(
                notify_fields(&[("trade_no", "order-1"), ("sign", "valid")]),
                true,
            );
            let (status, body) = notify_response(router, request).await?;

            assert_eq!(status, StatusCode::OK);
            assert_eq!(body, "fail");
            assert_eq!(*recover_lock(&verify_calls), 0);
            assert_eq!(*recover_lock(&completions), 0);
        }
        Ok(())
    }

    #[tokio::test]
    async fn epay_get_query_discards_only_malformed_pairs_before_verification() -> TestResult {
        let expected = notify_fields(&[("trade_no", "order-1"), ("sign", "valid")]);
        let (router, verify_calls, completions) = notify_app(expected, true);
        let (status, body) = notify_response(
            router,
            Request::get("/api/user/epay/notify?trade_no=order-1&bad=%ZZ&sign=valid")
                .body(Body::empty())?,
        )
        .await?;

        assert_eq!(status, StatusCode::OK);
        assert_eq!(body, "success");
        assert_eq!(*recover_lock(&verify_calls), 1);
        assert_eq!(*recover_lock(&completions), 1);
        Ok(())
    }

    #[tokio::test]
    async fn epay_notify_invalid_signature_fails_without_completing() -> TestResult {
        let (router, verify_calls, completions) = notify_app(
            notify_fields(&[("trade_no", "order-1"), ("sign", "invalid")]),
            false,
        );
        let (status, body) = notify_response(
            router,
            Request::get("/api/user/epay/notify?trade_no=order-1&sign=invalid")
                .body(Body::empty())?,
        )
        .await?;

        assert_eq!(status, StatusCode::OK);
        assert_eq!(body, "fail");
        assert_eq!(*recover_lock(&verify_calls), 1);
        assert_eq!(*recover_lock(&completions), 0);
        Ok(())
    }

    fn app() -> Router {
        router(UserTopupState::new(
            Arc::new(RejectingAuthorizer),
            Arc::new(NoopRepository),
            Arc::new(NoopEpay),
        ))
    }

    #[tokio::test]
    async fn epay_public_http_methods_keep_their_auth_and_callback_contracts() -> TestResult {
        for uri in ["/api/user/pay"] {
            let response = app()
                .oneshot(
                    Request::post(uri)
                        .header(header::CONTENT_TYPE, "application/json")
                        .body(Body::from(r#"{"amount":10,"payment_method":"alipay"}"#))?,
                )
                .await?;
            assert_eq!(response.status(), StatusCode::UNAUTHORIZED, "{uri}");
            let content_type = response
                .headers()
                .get(header::CONTENT_TYPE)
                .ok_or_else(|| {
                    test_error(format!(
                        "unauthorized ePay response for {uri} omitted Content-Type"
                    ))
                })?;
            assert_eq!(content_type, "application/json", "{uri}");
            let bytes = to_bytes(response.into_body(), 1024)
                .await
                .map_err(|error| {
                    test_error(format!(
                        "failed to read unauthorized ePay response body for {uri}: {error}"
                    ))
                })?;
            let body = serde_json::from_slice::<Value>(&bytes).map_err(|error| {
                test_error(format!(
                    "unauthorized ePay response body for {uri} was not valid JSON: {error}"
                ))
            })?;
            assert_eq!(
                body,
                json!({"success":false,"code":"AUTH_UNAUTHORIZED","message":"Unauthorized, invalid access token"}),
                "{uri}"
            );
        }
        for (method, uri) in [
            ("GET", "/api/user/epay/notify"),
            ("POST", "/api/user/epay/notify"),
        ] {
            let response = app()
                .oneshot(
                    Request::builder()
                        .method(method)
                        .uri(uri)
                        .body(Body::empty())?,
                )
                .await?;
            assert_eq!(response.status(), StatusCode::OK, "{method} {uri}");
            let bytes = to_bytes(response.into_body(), 1024)
                .await
                .map_err(|error| {
                    test_error(format!(
                        "failed to read ePay callback response body for {method} {uri}: {error}"
                    ))
                })?;
            let body = std::str::from_utf8(&bytes).map_err(|error| {
                test_error(format!(
                    "ePay callback response body for {method} {uri} was not UTF-8: {error}"
                ))
            })?;
            assert_eq!(body, "fail", "{method} {uri}");
        }
        Ok(())
    }

    #[test]
    fn form_and_method_compatibility() {
        let fields = parse_form(b"amount=12.9&payment_method=alipay");
        assert_eq!(fields["amount"], "12.9");
        assert_eq!(fields["payment_method"], "alipay");
    }
    #[test]
    fn malformed_percent_encoding_does_not_panic() {
        assert_eq!(percent_decode("a%ZZ+b"), "a%ZZ b");
    }
    #[test]
    fn strict_callback_form_rejects_semicolons_and_keeps_first_duplicate() {
        assert_eq!(
            parse_epay_post_fields(b"sign=first&sign=second&trade_no=order-1"),
            Some(notify_fields(&[("sign", "first"), ("trade_no", "order-1")]))
        );
        assert_eq!(parse_epay_post_fields(b"sign=valid;unexpected=value"), None);
    }

    #[test]
    fn epay_query_preserves_non_utf8_bytes_while_dropping_only_bad_pairs() -> TestResult {
        let fields =
            parse_epay_query_fields(b"sign=%FF&bad=%ZZ&trade_no=order-1").ok_or_else(|| {
                test_error("ePay query parser rejected its valid byte-level invariant")
            })?;
        assert_eq!(fields.values()[b"sign".as_slice()], [0xff]);
        assert_eq!(fields.values()[b"trade_no".as_slice()], b"order-1");
        Ok(())
    }
}
