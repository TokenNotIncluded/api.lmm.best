//! Legacy-compatible user Waffo and Waffo Pancake top-up routes.
//!
//! Provider calls sit behind [`TopUpGateway`].  The router never creates an
//! HTTP client or reads provider secrets, so incomplete composition fails
//! closed rather than accidentally sending a payment request.

use std::{
    collections::BTreeMap,
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::{Body, to_bytes},
    extract::{Request, State},
    http::{HeaderMap, HeaderName, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::post,
};
use chrono::{SecondsFormat, Utc};
use secrecy::SecretString;
use serde::{Deserialize, Serialize, de::DeserializeOwned};
use serde_json::{Value, json};
use sqlx::{PgPool, Row};
use uuid::Uuid;

use crate::auth::{
    AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth, DashboardUser, UserAuthPolicyError,
    enforce_user_auth, user_auth_message,
};
use crate::{ClientIpKey, RequestContext};

const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const DEFAULT_QUOTA_PER_UNIT: i64 = 500_000;
const WAFFO: &str = "waffo";
const WAFFO_PANCAKE: &str = "waffo_pancake";

/// The only provider boundary required by this four-route slice.  The
/// production adapter must verify/sign provider requests; tests use an
/// in-process recorder.  It intentionally has no permissive default.
#[async_trait]
pub trait TopUpGateway: Send + Sync {
    async fn create_waffo(&self, request: WaffoCheckout) -> Result<String, ()>;
    async fn create_waffo_pancake(&self, request: PancakeCheckout) -> Result<PancakeSession, ()>;
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct WaffoCheckout {
    pub order_id: String,
    pub amount: String,
    pub currency: String,
    pub user_id: i64,
    pub user_email: String,
    pub pay_method_type: String,
    pub pay_method_name: String,
    pub notify_url: String,
    pub return_url: String,
    pub failure_return_url: String,
    pub order_requested_at: String,
    pub user_terminal: &'static str,
    pub product_name: &'static str,
    pub goods_name: String,
    pub app_name: String,
    /// Resolved from the current option map for every checkout.  These are
    /// deliberately carried to the gateway boundary instead of being cached
    /// in router state, matching Go's `getWaffoSDK` behavior.
    pub provider: WaffoProviderConfig,
}

#[derive(Clone, PartialEq, Eq)]
pub struct PancakeCheckout {
    pub order_id: String,
    pub product_id: String,
    pub buyer_identity: String,
    pub buyer_email: String,
    pub amount: String,
    pub currency: &'static str,
    pub tax_category: &'static str,
    pub expires_in_seconds: i64,
    /// The Pancake SDK needs these live credentials to construct the session.
    pub provider: PancakeProviderConfig,
}

/// The Waffo environment and credentials selected by the same rules as Go's
/// `getWaffoSDK`: sandbox is the default only when `WaffoSandbox` is true;
/// production is selected otherwise.
#[derive(Clone, PartialEq, Eq)]
pub struct WaffoProviderConfig {
    pub sandbox: bool,
    pub api_key: String,
    pub private_key: String,
    pub public_cert: String,
    pub merchant_id: Option<String>,
}

impl std::fmt::Debug for WaffoProviderConfig {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("WaffoProviderConfig")
            .field("sandbox", &self.sandbox)
            .field("api_key", &"[REDACTED]")
            .field("private_key", &"[REDACTED]")
            .field("public_cert", &"[REDACTED]")
            .field("merchant_id", &self.merchant_id)
            .finish()
    }
}

/// Pancake is only enabled when this complete credential and product binding
/// exists.  Keep secrets out of ordinary debug output.
#[derive(Clone, PartialEq, Eq)]
pub struct PancakeProviderConfig {
    pub merchant_id: String,
    pub private_key: String,
    pub product_id: String,
}

impl std::fmt::Debug for PancakeProviderConfig {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("PancakeProviderConfig")
            .field("merchant_id", &"[REDACTED]")
            .field("private_key", &"[REDACTED]")
            .field("product_id", &self.product_id)
            .finish()
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct PancakeSession {
    pub checkout_url: String,
    pub session_id: String,
    pub expires_at: String,
    pub token: String,
    pub token_expires_at: String,
}

#[derive(Clone)]
pub struct WaffoTopUpState {
    pg: PgPool,
    auth: Arc<dyn DashboardAuth>,
    gateway: Arc<dyn TopUpGateway>,
}

impl WaffoTopUpState {
    #[must_use]
    pub fn new(pg: PgPool, auth: Arc<dyn DashboardAuth>, gateway: Arc<dyn TopUpGateway>) -> Self {
        Self { pg, auth, gateway }
    }
}

/// Fail-closed Waffo checkout adapter for listeners without live provider keys.
pub struct DisabledTopUpGateway;

#[async_trait]
impl TopUpGateway for DisabledTopUpGateway {
    async fn create_waffo(&self, _: WaffoCheckout) -> Result<String, ()> {
        Err(())
    }
    async fn create_waffo_pancake(&self, _: PancakeCheckout) -> Result<PancakeSession, ()> {
        Err(())
    }
}

pub fn router(state: WaffoTopUpState) -> Router {
    Router::new()
        .route("/api/user/waffo/amount", post(waffo_amount))
        .route("/api/user/waffo/pay", post(waffo_pay))
        .route("/api/user/waffo-pancake/amount", post(pancake_amount))
        .route("/api/user/waffo-pancake/pay", post(pancake_pay))
        .with_state(state)
}

#[derive(Default, Deserialize)]
struct PayRequest {
    amount: i64,
    #[serde(default)]
    pay_method_index: Option<i64>,
    #[serde(default)]
    pay_method_type: String,
    #[serde(default)]
    pay_method_name: String,
}

async fn waffo_amount(State(state): State<WaffoTopUpState>, request: Request) -> Response {
    quote(state, request, WAFFO).await
}
async fn pancake_amount(State(state): State<WaffoTopUpState>, request: Request) -> Response {
    quote(state, request, WAFFO_PANCAKE).await
}

async fn quote(state: WaffoTopUpState, request: Request, provider: &str) -> Response {
    let headers = request.headers().clone();
    let actor = match authenticated(&state, &headers).await {
        Ok(v) => v,
        Err(v) => return v,
    };
    with_auth_version(quote_authenticated(state, actor, request, provider).await)
}

async fn quote_authenticated(
    state: WaffoTopUpState,
    actor: DashboardUser,
    request: Request,
    provider: &str,
) -> Response {
    let request: PayRequest = match legacy_json(request).await {
        Ok(v) => v,
        Err(_) => return legacy_error("参数错误"),
    };
    let settings = match settings(&state.pg).await {
        Ok(v) => v,
        Err(_) => return legacy_error("获取用户分组失败"),
    };
    let min = setting_i64(
        &settings,
        if provider == WAFFO {
            "WaffoMinTopUp"
        } else {
            "WaffoPancakeMinTopUp"
        },
        1,
    );
    if request.amount < min {
        return legacy_error(format!("充值数量不能小于 {min}"));
    }
    let group = match user_group(&state.pg, actor.id).await {
        Ok(Some(v)) => v,
        _ => return legacy_error("获取用户分组失败"),
    };
    let money = quote_money(request.amount, &group, provider, &settings);
    if money <= FixedDecimal::cent() {
        return legacy_error("充值金额过低");
    }
    legacy_success(money.fixed(2))
}

async fn waffo_pay(State(state): State<WaffoTopUpState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let actor = match authenticated(&state, &headers).await {
        Ok(v) => v,
        Err(v) => return v,
    };
    if let Some(response) = critical_rate_limit(&state, critical_client_ip(&request)).await {
        return with_auth_version(response);
    }
    with_auth_version(waffo_pay_authenticated(state, actor, request).await)
}

async fn waffo_pay_authenticated(
    state: WaffoTopUpState,
    actor: DashboardUser,
    request: Request,
) -> Response {
    let settings = match settings(&state.pg).await {
        Ok(v) => v,
        Err(_) => return legacy_error("支付配置错误"),
    };
    if !setting_bool(&settings, "WaffoEnabled", false) {
        return legacy_error("Waffo 支付未启用");
    }
    let request: PayRequest = match legacy_json(request).await {
        Ok(v) => v,
        Err(_) => return legacy_error("参数错误"),
    };
    let min = setting_i64(&settings, "WaffoMinTopUp", 1);
    if request.amount < min {
        return legacy_error(format!("充值数量不能小于 {min}"));
    }
    let group = match user_group(&state.pg, actor.id).await {
        Ok(Some(v)) => v,
        _ => return legacy_error("用户不存在"),
    };
    let money = quote_money(request.amount, &group, WAFFO, &settings);
    if money < FixedDecimal::cent() {
        return legacy_error("充值金额过低");
    }
    let (method_type, method_name) = match waffo_method(&settings, &request) {
        Some(v) => v,
        None => return legacy_error("不支持的支付方式"),
    };
    let order_id = order_id("WAFFO", actor.id);
    let stored_amount = normalized_amount(request.amount, &settings);
    if insert_pending(
        &state.pg,
        actor.id,
        stored_amount,
        &money.decimal_string(),
        &order_id,
        WAFFO,
    )
    .await
    .is_err()
    {
        return legacy_error("创建订单失败");
    }
    // Go creates the pending order before constructing its SDK.  Preserve the
    // resulting failed order (and its distinct configuration error) when a
    // live Waffo setting is incomplete.
    let provider = match waffo_provider_config(&settings) {
        Some(value) => value,
        None => {
            let _ = mark_failed(&state.pg, &order_id).await;
            return legacy_error("支付配置错误");
        }
    };
    let currency = setting_string(&settings, "WaffoCurrency")
        .filter(|v| !v.is_empty())
        .unwrap_or_else(|| "USD".to_owned());
    let callback_address = setting_string(&settings, "CustomCallbackAddress")
        .filter(|value| !value.is_empty())
        .or_else(|| setting_string(&settings, "ServerAddress"))
        .unwrap_or_default();
    let site_address = setting_string(&settings, "ServerAddress").unwrap_or_default();
    let return_url = setting_string(&settings, "WaffoReturnUrl")
        .filter(|v| !v.is_empty())
        .unwrap_or_else(|| {
            format!(
                "{}/wallet?show_history=true",
                site_address.trim_end_matches('/')
            )
        });
    let notify_url = setting_string(&settings, "WaffoNotifyUrl")
        .filter(|v| !v.is_empty())
        .unwrap_or_else(|| format!("{callback_address}/api/waffo/webhook"));
    let goods_name = format!("Recharge {} credits", request.amount);
    let checkout = WaffoCheckout {
        order_id: order_id.clone(),
        amount: format_amount(money, &currency),
        currency,
        user_id: actor.id,
        user_email: format!("{}@examples.com", actor.id),
        pay_method_type: method_type,
        pay_method_name: method_name,
        notify_url,
        failure_return_url: return_url.clone(),
        return_url,
        order_requested_at: Utc::now().to_rfc3339_opts(SecondsFormat::Millis, true),
        user_terminal: "WEB",
        product_name: "ONE_TIME_PAYMENT",
        goods_name,
        app_name: setting_string(&settings, "SystemName")
            .filter(|v| !v.trim().is_empty())
            .unwrap_or_else(|| "LMM API".to_owned()),
        provider,
    };
    match state.gateway.create_waffo(checkout).await {
        Ok(payment_url) if !payment_url.trim().is_empty() => {
            legacy_success(json!({"payment_url":payment_url,"order_id":order_id}))
        }
        _ => {
            let _ = mark_failed(&state.pg, &order_id).await;
            legacy_error("拉起支付失败")
        }
    }
}

async fn pancake_pay(State(state): State<WaffoTopUpState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let actor = match authenticated(&state, &headers).await {
        Ok(v) => v,
        Err(v) => return v,
    };
    if let Some(response) = critical_rate_limit(&state, critical_client_ip(&request)).await {
        return with_auth_version(response);
    }
    with_auth_version(pancake_pay_authenticated(state, actor, request).await)
}

async fn pancake_pay_authenticated(
    state: WaffoTopUpState,
    actor: DashboardUser,
    request: Request,
) -> Response {
    let settings = match settings(&state.pg).await {
        Ok(v) => v,
        Err(_) => return legacy_error("Waffo Pancake 配置不完整"),
    };
    let provider = match pancake_provider_config(&settings) {
        Some(value) => value,
        None => return legacy_error("Waffo Pancake 配置不完整"),
    };
    let request: PayRequest = match legacy_json(request).await {
        Ok(v) => v,
        Err(_) => return legacy_error("参数错误"),
    };
    let min = setting_i64(&settings, "WaffoPancakeMinTopUp", 1);
    if request.amount < min {
        return legacy_error(format!("充值数量不能小于 {min}"));
    }
    let user = match top_up_user(&state.pg, actor.id).await {
        Ok(Some(v)) => v,
        _ => return legacy_error("用户不存在"),
    };
    let money = quote_money(request.amount, &user.group, WAFFO_PANCAKE, &settings);
    if money < FixedDecimal::cent() {
        return legacy_error("充值金额过低");
    }
    let order_id = order_id("WAFFO_PANCAKE", actor.id);
    if insert_pending(
        &state.pg,
        actor.id,
        normalized_amount(request.amount, &settings),
        &money.decimal_string(),
        &order_id,
        WAFFO_PANCAKE,
    )
    .await
    .is_err()
    {
        return legacy_error("创建订单失败");
    }
    let checkout = PancakeCheckout {
        order_id: order_id.clone(),
        product_id: provider.product_id.clone(),
        buyer_identity: format!("new-api-user-{}", actor.id),
        buyer_email: user.email.trim().to_owned(),
        amount: money.fixed(2),
        currency: "USD",
        tax_category: "saas",
        expires_in_seconds: 45 * 60,
        provider,
    };
    match state.gateway.create_waffo_pancake(checkout).await {
        Ok(session)
            if !session.checkout_url.trim().is_empty() && !session.session_id.trim().is_empty() =>
        {
            legacy_success(
                json!({"checkout_url":session.checkout_url,"session_id":session.session_id,"expires_at":session.expires_at,"order_id":order_id,"token":session.token,"token_expires_at":session.token_expires_at}),
            )
        }
        _ => {
            let _ = mark_failed(&state.pg, &order_id).await;
            legacy_error("拉起支付失败")
        }
    }
}

async fn authenticated(
    state: &WaffoTopUpState,
    headers: &HeaderMap,
) -> Result<DashboardUser, Response> {
    let token =
        dashboard_token(headers).ok_or_else(|| auth_error(headers, AuthErrorKind::Unauthorized))?;
    match state.auth.self_user(SecretString::from(token)).await {
        Ok(user) => enforce_user_auth(&user)
            .map(|()| user)
            .map_err(|error| user_auth_error(headers, error)),
        Err(error) if error.kind == AuthErrorKind::UserDisabled => {
            Err(user_auth_error(headers, UserAuthPolicyError::UserDisabled))
        }
        Err(error) => Err(auth_error(headers, error.kind)),
    }
}

fn dashboard_token(headers: &HeaderMap) -> Option<String> {
    let value = headers.get(header::AUTHORIZATION)?.to_str().ok()?.trim();
    let mut fields = value.split_whitespace();
    let first = fields.next()?;
    let second = fields.next();
    if fields.next().is_some() {
        return None;
    }
    match second {
        Some(token) if first.eq_ignore_ascii_case("bearer") && !token.is_empty() => {
            Some(token.to_owned())
        }
        None if !first.is_empty() => Some(first.to_owned()),
        _ => None,
    }
}

fn auth_error(headers: &HeaderMap, kind: AuthErrorKind) -> Response {
    let (status, code, message) = match kind {
        AuthErrorKind::TokenExpired => (
            StatusCode::UNAUTHORIZED,
            "AUTH_TOKEN_EXPIRED",
            localized_auth_message(headers, AuthMessage::NotLoggedIn),
        ),
        AuthErrorKind::SessionRevoked => (
            StatusCode::UNAUTHORIZED,
            "AUTH_SESSION_REVOKED",
            localized_auth_message(headers, AuthMessage::NotLoggedIn),
        ),
        AuthErrorKind::Internal => (
            StatusCode::INTERNAL_SERVER_ERROR,
            "AUTH_INTERNAL_ERROR",
            localized_auth_message(headers, AuthMessage::DatabaseError),
        ),
        _ => (
            StatusCode::UNAUTHORIZED,
            "AUTH_UNAUTHORIZED",
            localized_auth_message(headers, AuthMessage::InvalidAccessToken),
        ),
    };
    (
        status,
        Json(json!({"success":false,"code":code,"message":message})),
    )
        .into_response()
}

#[derive(Clone, Copy)]
enum AuthMessage {
    InvalidAccessToken,
    NotLoggedIn,
    DatabaseError,
}

fn localized_auth_message(headers: &HeaderMap, message: AuthMessage) -> &'static str {
    let language = headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.split(',').next())
        .and_then(|value| value.split(';').next())
        .unwrap_or_default()
        .trim()
        .to_ascii_lowercase();
    let traditional = language.starts_with("zh-tw");
    let chinese = language.starts_with("zh");
    match (message, traditional, chinese) {
        (AuthMessage::InvalidAccessToken, true, _) => "無權進行此操作，access token 無效",
        (AuthMessage::InvalidAccessToken, false, true) => "无权进行此操作，access token 无效",
        (AuthMessage::InvalidAccessToken, false, false) => "Unauthorized, invalid access token",
        (AuthMessage::NotLoggedIn, true, _) => "無權進行此操作，未登入且未提供 access token",
        (AuthMessage::NotLoggedIn, false, true) => "无权进行此操作，未登录且未提供 access token",
        (AuthMessage::NotLoggedIn, false, false) => {
            "Unauthorized, not logged in and no access token provided"
        }
        (AuthMessage::DatabaseError, true, _) => "資料庫出錯，請聯繫管理員",
        (AuthMessage::DatabaseError, false, true) => "数据库出错，请联系管理员",
        (AuthMessage::DatabaseError, false, false) => {
            "Database error, please contact the administrator"
        }
    }
}

fn with_auth_version(mut response: Response) -> Response {
    response.headers_mut().insert(
        HeaderName::from_static("auth-version"),
        HeaderValue::from_static(AUTH_VERSION),
    );
    response
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
    state: &WaffoTopUpState,
    client_ip: Option<String>,
) -> Option<Response> {
    let Some(client_ip) = client_ip else {
        let mut response = Response::new(Body::empty());
        *response.status_mut() = StatusCode::INTERNAL_SERVER_ERROR;
        return Some(response);
    };
    match state.auth.check_critical_rate_limit(&client_ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => None,
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => {
            let mut response = Response::new(Body::empty());
            *response.status_mut() = StatusCode::TOO_MANY_REQUESTS;
            if retry_after_seconds > 0
                && let Ok(value) = HeaderValue::from_str(&retry_after_seconds.to_string())
            {
                response.headers_mut().insert(header::RETRY_AFTER, value);
            }
            Some(response)
        }
        Err(_) => {
            let mut response = Response::new(Body::empty());
            *response.status_mut() = StatusCode::INTERNAL_SERVER_ERROR;
            Some(response)
        }
    }
}

fn user_auth_error(headers: &HeaderMap, error: UserAuthPolicyError) -> Response {
    let status = match error {
        UserAuthPolicyError::InsufficientPrivilege => StatusCode::FORBIDDEN,
        UserAuthPolicyError::UserDisabled | UserAuthPolicyError::InvalidUserInfo => {
            StatusCode::UNAUTHORIZED
        }
    };
    let code = match error {
        UserAuthPolicyError::UserDisabled => "AUTH_USER_DISABLED",
        UserAuthPolicyError::InsufficientPrivilege => "AUTH_INSUFFICIENT_PRIVILEGE",
        UserAuthPolicyError::InvalidUserInfo => "AUTH_USER_INVALID",
    };
    (
        status,
        Json(json!({
            "success": false,
            "code": code,
            "message": user_auth_message(
                error,
                headers.get(header::ACCEPT_LANGUAGE).and_then(|value| value.to_str().ok()),
            ),
        })),
    )
        .into_response()
}
fn legacy_success(data: impl Serialize) -> Response {
    Json(json!({"message":"success","data":data})).into_response()
}
fn legacy_error(data: impl Serialize) -> Response {
    Json(json!({"message":"error","data":data})).into_response()
}

/// Frozen Go mounts these routes below `UserAuth`, then calls `ShouldBindJSON`
/// inside each handler.  Reading JSON only after authentication preserves that
/// ordering and accepts a JSON body regardless of Content-Type.
async fn legacy_json<T: DeserializeOwned + Default>(request: Request) -> Result<T, ()> {
    let bytes = to_bytes(request.into_body(), usize::MAX)
        .await
        .map_err(|_| ())?;
    let mut decoder = serde_json::Deserializer::from_slice(&bytes);
    Option::<T>::deserialize(&mut decoder)
        .map(|value| value.unwrap_or_default())
        .map_err(|_| ())
}

async fn settings(pg: &PgPool) -> Result<BTreeMap<String, String>, ()> {
    let rows = sqlx::query("SELECT key,value FROM options WHERE key = ANY($1)")
        .bind(
            &[
                "WaffoEnabled",
                "WaffoMinTopUp",
                "WaffoUnitPrice",
                "WaffoPancakeMinTopUp",
                "WaffoPancakeUnitPrice",
                "WaffoCurrency",
                "WaffoNotifyUrl",
                "WaffoReturnUrl",
                "WaffoSandbox",
                "WaffoApiKey",
                "WaffoPrivateKey",
                "WaffoPublicCert",
                "WaffoSandboxApiKey",
                "WaffoSandboxPrivateKey",
                "WaffoSandboxPublicCert",
                "WaffoMerchantId",
                "WaffoPancakeMerchantID",
                "WaffoPancakePrivateKey",
                "WaffoPancakeProductID",
                "general_setting.quota_display_type",
                "DisplayInCurrencyEnabled",
                "QuotaPerUnit",
                "TopupGroupRatio",
                "payment_setting.amount_discount",
                "SystemName",
                "ServerAddress",
                "CustomCallbackAddress",
                "WaffoPayMethods",
            ][..],
        )
        .fetch_all(pg)
        .await
        .map_err(|_| ())?;
    Ok(rows
        .into_iter()
        .filter_map(|row| Some((row.try_get("key").ok()?, row.try_get("value").ok()?)))
        .collect())
}
async fn user_group(pg: &PgPool, user_id: i64) -> Result<Option<String>, ()> {
    sqlx::query_scalar("SELECT \"group\" FROM users WHERE id=$1 AND deleted_at IS NULL")
        .bind(user_id)
        .fetch_optional(pg)
        .await
        .map_err(|_| ())
}

struct TopUpUser {
    group: String,
    email: String,
}

async fn top_up_user(pg: &PgPool, user_id: i64) -> Result<Option<TopUpUser>, ()> {
    sqlx::query("SELECT \"group\",email FROM users WHERE id=$1 AND deleted_at IS NULL")
        .bind(user_id)
        .fetch_optional(pg)
        .await
        .map_err(|_| ())?
        .map(|row| {
            Ok(TopUpUser {
                group: row.try_get("group").map_err(|_| ())?,
                email: row.try_get("email").map_err(|_| ())?,
            })
        })
        .transpose()
}
async fn insert_pending(
    pg: &PgPool,
    user_id: i64,
    amount: i64,
    money: &str,
    trade_no: &str,
    provider: &str,
) -> Result<(), ()> {
    sqlx::query("INSERT INTO top_ups(user_id,amount,money,trade_no,payment_method,payment_provider,create_time,status) VALUES($1,$2,$3,$4,$5,$5,$6,'pending')")
        .bind(user_id)
        .bind(amount)
        // PostgreSQL NUMERIC has an exact text cast.  Persist the full
        // fixed-point result; the Pancake checkout alone is rounded to the
        // provider's two-decimal price snapshot.
        .bind(money)
        .bind(trade_no)
        .bind(provider)
        .bind(now())
        .execute(pg)
        .await
        .map(|_| ())
        .map_err(|_| ())
}
async fn mark_failed(pg: &PgPool, trade_no: &str) -> Result<(), ()> {
    // BEHAVIOR DEVIATION / TEST RISK: frozen Go updates the loaded TopUp row by
    // ID and can overwrite a concurrently completed state.  The candidate
    // intentionally only transitions `pending -> failed`, so a webhook win is
    // not clobbered.  Differential execution must retain this race as an
    // explicit non-parity item rather than silently approving it.
    sqlx::query("UPDATE top_ups SET status='failed' WHERE trade_no=$1 AND status='pending'")
        .bind(trade_no)
        .execute(pg)
        .await
        .map(|_| ())
        .map_err(|_| ())
}
fn now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |v| v.as_secs() as i64)
}
fn order_id(prefix: &str, user_id: i64) -> String {
    format!(
        "{prefix}-{user_id}-{}-{}",
        Utc::now().timestamp_millis(),
        &Uuid::new_v4().simple().to_string()[..6]
    )
}
fn setting_string(settings: &BTreeMap<String, String>, key: &str) -> Option<String> {
    settings.get(key).cloned()
}
fn setting_i64(settings: &BTreeMap<String, String>, key: &str, default: i64) -> i64 {
    setting_string(settings, key)
        .and_then(|v| v.parse().ok())
        .filter(|v: &i64| *v > 0)
        .unwrap_or(default)
}
fn setting_bool(settings: &BTreeMap<String, String>, key: &str, default: bool) -> bool {
    setting_string(settings, key)
        .and_then(|v| v.parse().ok())
        .unwrap_or(default)
}
fn required_setting(settings: &BTreeMap<String, String>, key: &str) -> Option<String> {
    setting_string(settings, key).filter(|value| !value.trim().is_empty())
}

fn exact_nonempty_setting(settings: &BTreeMap<String, String>, key: &str) -> Option<String> {
    setting_string(settings, key).filter(|value| !value.is_empty())
}

/// Selects the exact Waffo key triplet for the configured environment.  This
/// is deliberately evaluated per request: option changes take effect on the
/// next checkout just as they do in Go.
fn waffo_provider_config(settings: &BTreeMap<String, String>) -> Option<WaffoProviderConfig> {
    let sandbox = setting_bool(settings, "WaffoSandbox", false);
    let (api_key, private_key, public_cert) = if sandbox {
        (
            exact_nonempty_setting(settings, "WaffoSandboxApiKey")?,
            exact_nonempty_setting(settings, "WaffoSandboxPrivateKey")?,
            exact_nonempty_setting(settings, "WaffoSandboxPublicCert")?,
        )
    } else {
        (
            exact_nonempty_setting(settings, "WaffoApiKey")?,
            exact_nonempty_setting(settings, "WaffoPrivateKey")?,
            exact_nonempty_setting(settings, "WaffoPublicCert")?,
        )
    };
    Some(WaffoProviderConfig {
        sandbox,
        api_key,
        private_key,
        public_cert,
        merchant_id: exact_nonempty_setting(settings, "WaffoMerchantId"),
    })
}

fn pancake_provider_config(settings: &BTreeMap<String, String>) -> Option<PancakeProviderConfig> {
    Some(PancakeProviderConfig {
        merchant_id: required_setting(settings, "WaffoPancakeMerchantID")?,
        private_key: required_setting(settings, "WaffoPancakePrivateKey")?,
        product_id: required_setting(settings, "WaffoPancakeProductID")?,
    })
}

/// Nine-decimal fixed point keeps pricing deterministic and prevents a
/// binary-float round trip before PostgreSQL NUMERIC or Pancake's exact price
/// snapshot.
#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
struct FixedDecimal(i128);

impl FixedDecimal {
    const SCALE: i128 = 1_000_000_000;

    const fn one() -> Self {
        Self(Self::SCALE)
    }

    const fn cent() -> Self {
        Self(Self::SCALE / 100)
    }

    fn from_i64(value: i64) -> Self {
        Self(i128::from(value).saturating_mul(Self::SCALE))
    }

    fn parse(raw: &str) -> Option<Self> {
        let raw = raw.trim();
        if raw.is_empty() || raw.contains(['e', 'E']) {
            return None;
        }
        let (negative, unsigned) = raw.strip_prefix('-').map_or((false, raw), |v| (true, v));
        let unsigned = unsigned.strip_prefix('+').unwrap_or(unsigned);
        let (whole, fraction) = unsigned.split_once('.').unwrap_or((unsigned, ""));
        if whole.is_empty() && fraction.is_empty()
            || !whole.bytes().all(|value| value.is_ascii_digit())
            || !fraction.bytes().all(|value| value.is_ascii_digit())
        {
            return None;
        }
        let whole = if whole.is_empty() {
            0
        } else {
            whole.parse::<i128>().ok()?
        };
        let kept = &fraction[..fraction.len().min(9)];
        let fraction = if kept.is_empty() {
            0
        } else {
            kept.parse::<i128>().ok()? * 10_i128.pow(9_u32 - kept.len() as u32)
        };
        let scaled = whole.checked_mul(Self::SCALE)?.checked_add(fraction)?;
        Some(Self(if negative { -scaled } else { scaled }))
    }

    fn from_json(value: &Value) -> Option<Self> {
        match value {
            Value::Number(value) => Self::parse(&value.to_string()),
            Value::String(value) => Self::parse(value),
            _ => None,
        }
    }

    fn mul(self, rhs: Self) -> Option<Self> {
        self.0
            .checked_mul(rhs.0)
            .map(|value| Self(value / Self::SCALE))
    }

    fn div(self, rhs: Self) -> Option<Self> {
        if rhs.0 == 0 {
            return None;
        }
        self.0
            .checked_mul(Self::SCALE)
            .map(|value| Self(value / rhs.0))
    }

    fn decimal_string(self) -> String {
        let sign = if self.0 < 0 { "-" } else { "" };
        let absolute = self.0.abs();
        let whole = absolute / Self::SCALE;
        let fraction = format!("{:09}", absolute % Self::SCALE);
        let fraction = fraction.trim_end_matches('0');
        if fraction.is_empty() {
            format!("{sign}{whole}")
        } else {
            format!("{sign}{whole}.{fraction}")
        }
    }

    fn fixed(self, decimals: u32) -> String {
        debug_assert!(decimals <= 9);
        let divisor = 10_i128.pow(9 - decimals);
        let absolute = self.0.abs();
        let rounded = (absolute + divisor / 2) / divisor;
        let scale = 10_i128.pow(decimals);
        let sign = if self.0 < 0 { "-" } else { "" };
        if decimals == 0 {
            format!("{sign}{rounded}")
        } else {
            format!(
                "{sign}{}.{:0width$}",
                rounded / scale,
                rounded % scale,
                width = decimals as usize
            )
        }
    }
}

fn quote_money(
    amount: i64,
    group: &str,
    provider: &str,
    settings: &BTreeMap<String, String>,
) -> FixedDecimal {
    let original = FixedDecimal::from_i64(amount);
    let quota_display = token_display(settings);
    let displayed = if quota_display {
        original
            .div(positive_setting_decimal(
                settings,
                "QuotaPerUnit",
                FixedDecimal::from_i64(DEFAULT_QUOTA_PER_UNIT),
            ))
            .unwrap_or(original)
    } else {
        original
    };
    let price = positive_setting_decimal(
        settings,
        if provider == WAFFO {
            "WaffoUnitPrice"
        } else {
            "WaffoPancakeUnitPrice"
        },
        FixedDecimal::one(),
    );
    let ratio = json_value(settings, "TopupGroupRatio")
        .and_then(|value| value.get(group).and_then(FixedDecimal::from_json))
        .filter(|value| value.0 > 0)
        .unwrap_or_else(FixedDecimal::one);
    let discount = json_value(settings, "payment_setting.amount_discount")
        .and_then(|value| {
            value
                .get(amount.to_string())
                .and_then(FixedDecimal::from_json)
        })
        .filter(|value| value.0 > 0)
        .unwrap_or_else(FixedDecimal::one);
    displayed
        .mul(price)
        .and_then(|value| value.mul(ratio))
        .and_then(|value| value.mul(discount))
        .unwrap_or(FixedDecimal(0))
}

fn positive_setting_decimal(
    settings: &BTreeMap<String, String>,
    key: &str,
    default: FixedDecimal,
) -> FixedDecimal {
    setting_string(settings, key)
        .and_then(|value| FixedDecimal::parse(&value))
        .filter(|value| value.0 > 0)
        .unwrap_or(default)
}

fn json_value(settings: &BTreeMap<String, String>, key: &str) -> Option<Value> {
    setting_string(settings, key).and_then(|v| serde_json::from_str(&v).ok())
}
fn normalized_amount(amount: i64, settings: &BTreeMap<String, String>) -> i64 {
    if token_display(settings) {
        FixedDecimal::from_i64(amount)
            .div(positive_setting_decimal(
                settings,
                "QuotaPerUnit",
                FixedDecimal::from_i64(DEFAULT_QUOTA_PER_UNIT),
            ))
            .and_then(|value| i64::try_from(value.0 / FixedDecimal::SCALE).ok())
            .unwrap_or(1)
            .max(1)
    } else {
        amount
    }
}

/// `DisplayInCurrencyEnabled` is the legacy Go option. Go maps it to the
/// registered `general_setting.quota_display_type` at startup; keep the same
/// fallback for databases that have not yet been rewritten to the dotted key.
/// The new registered value always wins when both forms exist.
fn token_display(settings: &BTreeMap<String, String>) -> bool {
    if let Some(value) = setting_string(settings, "general_setting.quota_display_type") {
        return value == "TOKENS";
    }
    settings
        .get("DisplayInCurrencyEnabled")
        .is_some_and(|value| value != "true")
}
fn format_amount(amount: FixedDecimal, currency: &str) -> String {
    if matches!(currency, "IDR" | "JPY" | "KRW" | "VND") {
        amount.fixed(0)
    } else {
        amount.fixed(2)
    }
}
fn waffo_method(
    settings: &BTreeMap<String, String>,
    request: &PayRequest,
) -> Option<(String, String)> {
    let methods = waffo_methods(settings);
    if let Some(index) = request.pay_method_index {
        let method = methods.get(usize::try_from(index).ok()?)?;
        return Some((
            method
                .get("pay_method_type")
                .or_else(|| method.get("payMethodType"))
                .or_else(|| method.get("PayMethodType"))?
                .as_str()?
                .to_owned(),
            method
                .get("pay_method_name")
                .or_else(|| method.get("payMethodName"))
                .or_else(|| method.get("PayMethodName"))?
                .as_str()?
                .to_owned(),
        ));
    }
    if request.pay_method_type.trim().is_empty() {
        return Some((String::new(), String::new()));
    }
    methods.into_iter().find_map(|method| {
        let ty = method
            .get("pay_method_type")
            .or_else(|| method.get("payMethodType"))
            .or_else(|| method.get("PayMethodType"))?
            .as_str()?;
        let name = method
            .get("pay_method_name")
            .or_else(|| method.get("payMethodName"))
            .or_else(|| method.get("PayMethodName"))?
            .as_str()?;
        (ty == request.pay_method_type && name == request.pay_method_name)
            .then(|| (ty.to_owned(), name.to_owned()))
    })
}

/// Go's `GetWaffoPayMethods` falls back only when the option is absent, blank,
/// or malformed. A valid `[]` remains an explicit empty allow-list. Keep the
/// same distinction here and accept both the current camel-case JSON keys and
/// legacy spellings used by older clients.
fn waffo_methods(settings: &BTreeMap<String, String>) -> Vec<Value> {
    let Some(raw) = settings.get("WaffoPayMethods") else {
        return default_waffo_methods();
    };
    if raw.trim().is_empty() {
        return default_waffo_methods();
    }
    match serde_json::from_str::<Value>(raw) {
        Ok(Value::Array(methods)) => methods,
        _ => default_waffo_methods(),
    }
}

fn default_waffo_methods() -> Vec<Value> {
    vec![
        json!({
            "name": "Card",
            "icon": "/pay-card.png",
            "payMethodType": "CREDITCARD,DEBITCARD",
            "payMethodName": "",
        }),
        json!({
            "name": "Apple Pay",
            "icon": "/pay-apple.png",
            "payMethodType": "APPLEPAY",
            "payMethodName": "APPLEPAY",
        }),
        json!({
            "name": "Google Pay",
            "icon": "/pay-google.png",
            "payMethodType": "GOOGLEPAY",
            "payMethodName": "GOOGLEPAY",
        }),
    ]
}

#[cfg(test)]
mod tests {
    use super::*;
    use async_trait::async_trait;
    use axum::{
        body::{Body, to_bytes},
        http::Request,
    };
    use tower::ServiceExt;

    struct RejectingAuth;

    #[async_trait]
    impl DashboardAuth for RejectingAuth {
        async fn check_critical_rate_limit(
            &self,
            _: &str,
        ) -> Result<crate::auth::CriticalRateLimitOutcome, crate::auth::AuthError> {
            Ok(crate::auth::CriticalRateLimitOutcome::Allowed)
        }
        async fn login(
            &self,
            _: crate::auth::LoginRequest,
            _: crate::auth::RequestMetadata,
        ) -> Result<crate::auth::LoginOutcome, crate::auth::AuthError> {
            Err(crate::auth::AuthError::new(AuthErrorKind::Unauthorized))
        }
        async fn login_2fa(
            &self,
            _: crate::auth::TwoFactorLoginRequest,
            _: crate::auth::RequestMetadata,
        ) -> Result<crate::auth::AuthBundle, crate::auth::AuthError> {
            Err(crate::auth::AuthError::new(AuthErrorKind::Unauthorized))
        }
        async fn refresh(
            &self,
            _: SecretString,
            _: Option<String>,
            _: crate::auth::RequestMetadata,
        ) -> Result<crate::auth::AuthBundle, crate::auth::AuthError> {
            Err(crate::auth::AuthError::new(AuthErrorKind::Unauthorized))
        }
        async fn self_user(
            &self,
            _: SecretString,
        ) -> Result<DashboardUser, crate::auth::AuthError> {
            Err(crate::auth::AuthError::new(AuthErrorKind::Unauthorized))
        }
        async fn logout(
            &self,
            _: crate::auth::LogoutRequest,
        ) -> Result<crate::auth::LogoutResult, crate::auth::AuthError> {
            Err(crate::auth::AuthError::new(AuthErrorKind::Unauthorized))
        }
        async fn generate_personal_access_token(
            &self,
            _: SecretString,
        ) -> Result<String, crate::auth::AuthError> {
            Err(crate::auth::AuthError::new(AuthErrorKind::Unauthorized))
        }
    }

    struct NoopGateway;

    #[async_trait]
    impl TopUpGateway for NoopGateway {
        async fn create_waffo(&self, _: WaffoCheckout) -> Result<String, ()> {
            Err(())
        }
        async fn create_waffo_pancake(&self, _: PancakeCheckout) -> Result<PancakeSession, ()> {
            Err(())
        }
    }

    fn app() -> Router {
        router(WaffoTopUpState::new(
            PgPool::connect_lazy("postgres://unused:unused@127.0.0.1:1/unused").unwrap(),
            Arc::new(RejectingAuth),
            Arc::new(NoopGateway),
        ))
    }

    #[tokio::test]
    async fn waffo_public_payment_seams_reject_missing_dashboard_credential_before_database_or_gateway()
     {
        for uri in [
            "/api/user/waffo/amount",
            "/api/user/waffo/pay",
            "/api/user/waffo-pancake/amount",
            "/api/user/waffo-pancake/pay",
        ] {
            let response = app()
                .oneshot(
                    Request::post(uri)
                        .header(header::CONTENT_TYPE, "application/json")
                        // Go's enclosing `UserAuth` rejects before
                        // `ShouldBindJSON`; an invalid body must not alter
                        // that response or touch a persistence/provider seam.
                        .body(Body::from("not-json"))
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
                    &to_bytes(response.into_body(), 1024).await.unwrap()
                )
                .unwrap(),
                json!({"success":false,"code":"AUTH_UNAUTHORIZED","message":"Unauthorized, invalid access token"}),
                "{uri}"
            );
        }
    }

    #[test]
    fn quote_keeps_group_ratio_discount_and_token_normalization() {
        let settings = BTreeMap::from([
            (
                "general_setting.quota_display_type".to_owned(),
                "TOKENS".to_owned(),
            ),
            ("QuotaPerUnit".to_owned(), "100".to_owned()),
            ("WaffoUnitPrice".to_owned(), "2".to_owned()),
            ("TopupGroupRatio".to_owned(), r#"{"vip":1.5}"#.to_owned()),
            (
                "payment_setting.amount_discount".to_owned(),
                r#"{"200":0.5}"#.to_owned(),
            ),
        ]);
        assert_eq!(
            quote_money(200, "vip", WAFFO, &settings),
            FixedDecimal::parse("3").unwrap()
        );
        assert_eq!(normalized_amount(200, &settings), 2);
    }

    #[test]
    fn token_display_keeps_go_legacy_option_fallback() {
        let legacy_tokens = BTreeMap::from([
            ("DisplayInCurrencyEnabled".to_owned(), "false".to_owned()),
            ("QuotaPerUnit".to_owned(), "100".to_owned()),
        ]);
        assert!(token_display(&legacy_tokens));
        assert_eq!(normalized_amount(200, &legacy_tokens), 2);
        assert_eq!(
            quote_money(200, "default", WAFFO, &legacy_tokens),
            FixedDecimal::parse("2").unwrap()
        );

        let dotted_wins = BTreeMap::from([
            ("DisplayInCurrencyEnabled".to_owned(), "false".to_owned()),
            (
                "general_setting.quota_display_type".to_owned(),
                "USD".to_owned(),
            ),
            ("QuotaPerUnit".to_owned(), "100".to_owned()),
        ]);
        assert!(!token_display(&dotted_wins));
        assert_eq!(normalized_amount(200, &dotted_wins), 200);
    }
    #[test]
    fn payment_method_is_server_allowlisted() {
        let settings = BTreeMap::from([(
            "WaffoPayMethods".to_owned(),
            r#"[{"pay_method_type":"card","pay_method_name":"Visa"}]"#.to_owned(),
        )]);
        assert_eq!(
            waffo_method(
                &settings,
                &PayRequest {
                    amount: 1,
                    pay_method_index: Some(0),
                    pay_method_type: String::new(),
                    pay_method_name: String::new()
                }
            ),
            Some(("card".to_owned(), "Visa".to_owned()))
        );
        assert!(
            waffo_method(
                &settings,
                &PayRequest {
                    amount: 1,
                    pay_method_index: None,
                    pay_method_type: "forged".to_owned(),
                    pay_method_name: "Visa".to_owned()
                }
            )
            .is_none()
        );
    }

    #[test]
    fn payment_methods_match_go_camel_case_and_default_fallbacks() {
        let camel_case = BTreeMap::from([(
            "WaffoPayMethods".to_owned(),
            r#"[{"payMethodType":"APPLEPAY","payMethodName":"APPLEPAY"}]"#.to_owned(),
        )]);
        assert_eq!(
            waffo_method(
                &camel_case,
                &PayRequest {
                    amount: 1,
                    pay_method_index: Some(0),
                    pay_method_type: String::new(),
                    pay_method_name: String::new(),
                },
            ),
            Some(("APPLEPAY".to_owned(), "APPLEPAY".to_owned()))
        );

        let missing = BTreeMap::new();
        assert_eq!(
            waffo_method(
                &missing,
                &PayRequest {
                    amount: 1,
                    pay_method_index: Some(0),
                    pay_method_type: String::new(),
                    pay_method_name: String::new(),
                },
            ),
            Some(("CREDITCARD,DEBITCARD".to_owned(), String::new()))
        );

        let explicit_empty = BTreeMap::from([("WaffoPayMethods".to_owned(), "[]".to_owned())]);
        assert_eq!(
            waffo_method(
                &explicit_empty,
                &PayRequest {
                    amount: 1,
                    pay_method_index: Some(0),
                    pay_method_type: String::new(),
                    pay_method_name: String::new(),
                },
            ),
            None
        );
    }
    #[test]
    fn zero_decimal_currencies_are_not_sent_with_fraction() {
        let amount = FixedDecimal::parse("12.5").unwrap();
        assert_eq!(format_amount(amount, "JPY"), "13");
        assert_eq!(format_amount(amount, "USD"), "12.50");
    }

    #[test]
    fn waffo_uses_complete_sandbox_or_production_key_triplet() {
        let production = BTreeMap::from([
            ("WaffoApiKey".to_owned(), "prod-api".to_owned()),
            ("WaffoPrivateKey".to_owned(), "prod-private".to_owned()),
            ("WaffoPublicCert".to_owned(), "prod-cert".to_owned()),
            ("WaffoMerchantId".to_owned(), "merchant".to_owned()),
        ]);
        assert_eq!(
            waffo_provider_config(&production),
            Some(WaffoProviderConfig {
                sandbox: false,
                api_key: "prod-api".to_owned(),
                private_key: "prod-private".to_owned(),
                public_cert: "prod-cert".to_owned(),
                merchant_id: Some("merchant".to_owned()),
            })
        );

        let sandbox = BTreeMap::from([
            ("WaffoSandbox".to_owned(), "true".to_owned()),
            ("WaffoSandboxApiKey".to_owned(), "sandbox-api".to_owned()),
            (
                "WaffoSandboxPrivateKey".to_owned(),
                "sandbox-private".to_owned(),
            ),
            (
                "WaffoSandboxPublicCert".to_owned(),
                "sandbox-cert".to_owned(),
            ),
        ]);
        assert_eq!(
            waffo_provider_config(&sandbox),
            Some(WaffoProviderConfig {
                sandbox: true,
                api_key: "sandbox-api".to_owned(),
                private_key: "sandbox-private".to_owned(),
                public_cert: "sandbox-cert".to_owned(),
                merchant_id: None,
            })
        );
    }

    #[test]
    fn incomplete_waffo_or_pancake_settings_fail_closed_before_provider_call() {
        let incomplete_production = BTreeMap::from([
            ("WaffoApiKey".to_owned(), "api".to_owned()),
            ("WaffoPrivateKey".to_owned(), "private".to_owned()),
        ]);
        assert!(waffo_provider_config(&incomplete_production).is_none());

        let incomplete_sandbox = BTreeMap::from([
            ("WaffoSandbox".to_owned(), "true".to_owned()),
            ("WaffoSandboxApiKey".to_owned(), "api".to_owned()),
            ("WaffoSandboxPrivateKey".to_owned(), "private".to_owned()),
        ]);
        assert!(waffo_provider_config(&incomplete_sandbox).is_none());

        let pancake = BTreeMap::from([
            ("WaffoPancakeMerchantID".to_owned(), "merchant".to_owned()),
            ("WaffoPancakePrivateKey".to_owned(), "private".to_owned()),
            ("WaffoPancakeProductID".to_owned(), "product".to_owned()),
        ]);
        assert_eq!(
            pancake_provider_config(&pancake),
            Some(PancakeProviderConfig {
                merchant_id: "merchant".to_owned(),
                private_key: "private".to_owned(),
                product_id: "product".to_owned(),
            })
        );
        assert!(
            pancake_provider_config(&BTreeMap::from([(
                "WaffoPancakeMerchantID".to_owned(),
                "merchant".to_owned(),
            )]))
            .is_none()
        );
    }
}
