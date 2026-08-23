//! Legacy-compatible wallet history, redemption, and administrator completion routes.
//!
//! The source-of-truth behaviour is `controller/topup.go`, `controller/user.go`,
//! and `model/{topup,redemption}.go`.  In particular, redemption and manual
//! completion keep the legacy all-or-nothing PostgreSQL transaction boundary:
//! an order/code is locked before it is mutated and quota is credited in that
//! same transaction.

use std::{
    collections::HashMap,
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use axum::{
    Json, Router,
    extract::{Extension, RawQuery, State},
    http::{HeaderMap, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Number, Value, json};
use sqlx::{PgPool, Postgres, Row, Transaction};

use crate::RequestContext;
use crate::auth::{
    AuthErrorKind, DashboardAuth, DashboardUser, DashboardUserView, UserAuthPolicyError,
    enforce_user_auth, enforce_user_auth_view, user_auth_message,
};

const ROLE_ADMIN: i64 = 10;
const REDEMPTION_ENABLED: i64 = 1;
const REDEMPTION_USED: i64 = 3;
const TOPUP_PENDING: &str = "pending";
const TOPUP_SUCCESS: &str = "success";
const DEFAULT_QUOTA_PER_UNIT: f64 = 500_000.0;
const TOPUP_QUERY_WINDOW_SECONDS: i64 = 30 * 24 * 60 * 60;
const SEARCH_COUNT_HARD_LIMIT: i64 = 10_000;

/// Dependencies owned by this isolated migration slice.
#[derive(Clone)]
pub struct IdentityTopupState {
    pool: PgPool,
    auth: Arc<dyn DashboardAuth>,
}

impl IdentityTopupState {
    #[must_use]
    pub fn new(pool: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self { pool, auth }
    }
}

/// Routes retained under the legacy `/api/user` namespace.
pub fn router(state: IdentityTopupState) -> Router {
    topup_read_routes()
        .merge(topup_admin_route())
        .merge(topup_write_route())
        .with_state(state)
}

/// Normal-listener read-only mount. Redemption and manual completion remain
/// isolated until their Go write-side transaction differential is complete.
pub fn read_router(state: IdentityTopupState) -> Router {
    topup_read_routes().with_state(state)
}

/// Mounts only the administrator's read-only top-up history on a normal
/// listener.  Redemption and manual completion remain isolated until their
/// write-side transaction differential is complete.
pub fn admin_read_router(state: IdentityTopupState) -> Router {
    topup_admin_route().with_state(state)
}

fn topup_admin_route() -> Router<IdentityTopupState> {
    Router::new().route("/api/user/topup", get(all_topups))
}

fn topup_write_route() -> Router<IdentityTopupState> {
    Router::new()
        .route("/api/user/topup", post(redeem))
        .route("/api/user/topup/complete", post(complete_topup))
}

fn topup_read_routes() -> Router<IdentityTopupState> {
    Router::new()
        .route("/api/user/topup/info", get(topup_info))
        .route("/api/user/topup/self", get(user_topups))
}

#[derive(Serialize)]
struct Envelope<T: Serialize> {
    success: bool,
    message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<T>,
}

fn ok<T: Serialize>(data: T) -> Response {
    Json(Envelope {
        success: true,
        message: String::new(),
        data: Some(data),
    })
    .into_response()
}

fn fail(message: impl Into<String>) -> Response {
    Json(Envelope::<()> {
        success: false,
        message: message.into(),
        data: None,
    })
    .into_response()
}

fn unauthorized() -> Response {
    (
        StatusCode::UNAUTHORIZED,
        Json(json!({
            "success": false,
            "code": "AUTH_UNAUTHORIZED",
            "message": "Unauthorized, invalid access token"
        })),
    )
        .into_response()
}

fn forbidden() -> Response {
    (
        StatusCode::FORBIDDEN,
        Json(json!({
            "success": false,
            "code": "AUTH_INSUFFICIENT_PRIVILEGE",
            "message": "Unauthorized, insufficient privileges"
        })),
    )
        .into_response()
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

fn bearer(headers: &HeaderMap) -> Option<String> {
    let value = headers.get(header::AUTHORIZATION)?.to_str().ok()?;
    let mut words = value.split_whitespace();
    let first = words.next()?;
    let second = words.next();
    if let Some(token) = second {
        (first.eq_ignore_ascii_case("bearer") && words.next().is_none()).then(|| token.to_owned())
    } else {
        Some(first.to_owned())
    }
}

async fn actor(state: &IdentityTopupState, headers: &HeaderMap) -> Result<DashboardUser, Response> {
    let Some(token) = bearer(headers) else {
        return Err(unauthorized());
    };
    let user = state
        .auth
        .self_user(SecretString::from(token))
        .await
        .map_err(|error| match error.kind {
            AuthErrorKind::UserDisabled => {
                user_auth_error(headers, UserAuthPolicyError::UserDisabled)
            }
            AuthErrorKind::Unauthorized
            | AuthErrorKind::TokenExpired
            | AuthErrorKind::SessionRevoked => unauthorized(),
            _ => unauthorized(),
        })?;
    enforce_user_auth(&user).map_err(|error| user_auth_error(headers, error))?;
    Ok(user)
}

async fn actor_view(
    state: &IdentityTopupState,
    headers: &HeaderMap,
) -> Result<DashboardUserView, Response> {
    let Some(token) = bearer(headers) else {
        return Err(unauthorized());
    };
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(token))
        .await
        .map_err(|error| match error.kind {
            AuthErrorKind::UserDisabled => {
                user_auth_error(headers, UserAuthPolicyError::UserDisabled)
            }
            AuthErrorKind::Unauthorized
            | AuthErrorKind::TokenExpired
            | AuthErrorKind::SessionRevoked => unauthorized(),
            _ => unauthorized(),
        })?;
    enforce_user_auth_view(&user).map_err(|error| user_auth_error(headers, error))?;
    Ok(user)
}

async fn administrator(
    state: &IdentityTopupState,
    headers: &HeaderMap,
) -> Result<DashboardUser, Response> {
    let user = actor(state, headers).await?;
    if user.role < ROLE_ADMIN {
        return Err(forbidden());
    }
    Ok(user)
}

#[derive(Default)]
struct PageQuery {
    p: Option<i64>,
    page_size: Option<i64>,
    ps: Option<i64>,
    size: Option<i64>,
    keyword: Option<String>,
}

impl PageQuery {
    fn page(&self) -> i64 {
        let parsed = self.p.unwrap_or(0);
        if parsed < 1 {
            if parsed != 0 { parsed } else { 1 }
        } else {
            parsed
        }
    }
    fn page_size(&self) -> i64 {
        let mut parsed = self.page_size.unwrap_or(0);
        if parsed == 0 {
            parsed = self.ps.unwrap_or(0);
        }
        if parsed == 0 {
            parsed = self.size.unwrap_or(0);
        }
        if parsed == 0 {
            parsed = 10;
        }
        if parsed > 100 { 100 } else { parsed }
    }
    fn offset(&self) -> i64 {
        self.page().wrapping_sub(1).wrapping_mul(self.page_size())
    }
    fn from_raw(raw: Option<&str>) -> Self {
        Self {
            p: raw_query_i64(raw, "p"),
            page_size: raw_query_i64(raw, "page_size"),
            ps: raw_query_i64(raw, "ps"),
            size: raw_query_i64(raw, "size"),
            keyword: raw_query_string(raw, "keyword"),
        }
    }
}

// Gin's Query ignores failed integer conversions.  Raw parsing keeps that
// compatibility instead of returning Axum's extractor 400 before the route.
fn raw_query_string(raw: Option<&str>, wanted: &str) -> Option<String> {
    raw?.split('&').find_map(|part| {
        let (key, value) = part.split_once('=').unwrap_or((part, ""));
        (key == wanted).then(|| percent_decode_query(value))
    })
}

fn raw_query_i64(raw: Option<&str>, wanted: &str) -> Option<i64> {
    raw_query_string(raw, wanted).and_then(|value| value.parse().ok())
}

fn percent_decode_query(value: &str) -> String {
    let bytes = value.as_bytes();
    let mut decoded = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        match bytes[index] {
            b'+' => decoded.push(b' '),
            b'%' if index + 2 < bytes.len() => {
                let high = hex_nibble(bytes[index + 1]);
                let low = hex_nibble(bytes[index + 2]);
                if let (Some(high), Some(low)) = (high, low) {
                    decoded.push(high * 16 + low);
                    index += 2;
                } else {
                    decoded.push(bytes[index]);
                }
            }
            byte => decoded.push(byte),
        }
        index += 1;
    }
    String::from_utf8(decoded).unwrap_or_else(|_| value.replace('+', " "))
}

const fn hex_nibble(byte: u8) -> Option<u8> {
    match byte {
        b'0'..=b'9' => Some(byte - b'0'),
        b'a'..=b'f' => Some(byte - b'a' + 10),
        b'A'..=b'F' => Some(byte - b'A' + 10),
        _ => None,
    }
}

#[derive(Serialize)]
struct Page<T: Serialize> {
    page: i64,
    page_size: i64,
    total: i64,
    items: Vec<T>,
}

#[derive(Serialize)]
struct TopupRecord {
    id: i64,
    user_id: i64,
    amount: i64,
    credited_quota: i64,
    expected_amount_micros: i64,
    settled_amount_micros: i64,
    settlement_currency: String,
    money: Value,
    trade_no: String,
    payment_method: String,
    payment_provider: String,
    provider_product_id: String,
    provider_store_id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    provider_event_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    provider_transaction_id: Option<String>,
    create_time: i64,
    complete_time: i64,
    status: String,
}

#[derive(Serialize)]
struct TopupSelfRecord {
    id: i64,
    user_id: i64,
    amount: i64,
    money: Value,
    trade_no: String,
    payment_method: String,
    create_time: i64,
    complete_time: i64,
    status: String,
}

impl From<TopupRecord> for TopupSelfRecord {
    fn from(record: TopupRecord) -> Self {
        Self {
            id: record.id,
            user_id: record.user_id,
            amount: record.amount,
            money: record.money,
            trade_no: record.trade_no,
            payment_method: record.payment_method,
            create_time: record.create_time,
            complete_time: record.complete_time,
            status: record.status,
        }
    }
}

fn topup_record(row: &sqlx::postgres::PgRow) -> Result<TopupRecord, sqlx::Error> {
    let money_text: String = row.try_get("money")?;
    let money = money_value(&money_text);
    let top_up_json: Value = row.try_get("top_up_json")?;
    Ok(TopupRecord {
        id: row.try_get("id")?,
        user_id: row.try_get("user_id")?,
        amount: row.try_get("amount")?,
        credited_quota: json_i64(&top_up_json, "credited_quota"),
        expected_amount_micros: json_i64(&top_up_json, "expected_amount_micros"),
        settled_amount_micros: json_i64(&top_up_json, "settled_amount_micros"),
        settlement_currency: json_string(&top_up_json, "settlement_currency"),
        money,
        trade_no: row.try_get("trade_no")?,
        payment_method: row.try_get("payment_method")?,
        payment_provider: row.try_get("payment_provider")?,
        provider_product_id: json_string(&top_up_json, "provider_product_id"),
        provider_store_id: json_string(&top_up_json, "provider_store_id"),
        provider_event_id: json_optional_string(&top_up_json, "provider_event_id"),
        provider_transaction_id: json_optional_string(&top_up_json, "provider_transaction_id"),
        create_time: row.try_get("create_time")?,
        complete_time: row.try_get("complete_time")?,
        status: row.try_get("status")?,
    })
}

fn json_i64(row: &Value, key: &str) -> i64 {
    row.get(key)
        .and_then(|value| value.as_i64().or_else(|| value.as_str()?.parse().ok()))
        .unwrap_or(0)
}

fn json_string(row: &Value, key: &str) -> String {
    row.get(key)
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_owned()
}

fn json_optional_string(row: &Value, key: &str) -> Option<String> {
    row.get(key).and_then(|value| match value {
        Value::Null => None,
        Value::String(value) => Some(value.to_owned()),
        _ => Some(value.to_string()),
    })
}

/// Go's `encoding/json` emits integral `float64` values without a trailing
/// `.0` (for example, `2`, not `2.0`).  PostgreSQL may return either spelling
/// for a NUMERIC column, so normalize integral values before serializing the
/// legacy response while retaining fractional amounts as JSON numbers.
fn money_value(text: &str) -> Value {
    if let Ok(integer) = text.parse::<i64>() {
        return Value::Number(integer.into());
    }
    match text.parse::<f64>() {
        Ok(value) if value.is_finite() && value.fract() == 0.0 => {
            if value >= i64::MIN as f64 && value <= i64::MAX as f64 {
                return Value::Number((value as i64).into());
            }
            Number::from_f64(value)
                .map(Value::Number)
                .unwrap_or_else(|| Value::String(text.to_owned()))
        }
        Ok(value) => Number::from_f64(value)
            .map(Value::Number)
            .unwrap_or_else(|| Value::String(text.to_owned())),
        Err(_) => Value::String(text.to_owned()),
    }
}

const TOPUP_COLUMNS: &str = r#"id, COALESCE(user_id, 0) AS user_id, COALESCE(amount, 0) AS amount,
COALESCE(money, 0)::text AS money, COALESCE(trade_no, '') AS trade_no,
COALESCE(payment_method, '') AS payment_method, COALESCE(payment_provider, '') AS payment_provider,
COALESCE(create_time, 0) AS create_time, COALESCE(complete_time, 0) AS complete_time,
COALESCE(status, '') AS status, to_jsonb(top_ups) AS top_up_json"#;

async fn all_topups(
    State(state): State<IdentityTopupState>,
    headers: HeaderMap,
    RawQuery(raw_query): RawQuery,
) -> Response {
    if administrator(&state, &headers).await.is_err() {
        return forbidden_or_unauthorized(&state, &headers).await;
    }
    list_topups(
        &state.pool,
        &PageQuery::from_raw(raw_query.as_deref()),
        None,
    )
    .await
}

async fn user_topups(
    State(state): State<IdentityTopupState>,
    headers: HeaderMap,
    RawQuery(raw_query): RawQuery,
) -> Response {
    let user = match actor(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return response,
    };
    list_self_topups(
        &state.pool,
        &PageQuery::from_raw(raw_query.as_deref()),
        Some(user.id),
    )
    .await
}

// Preserve the distinction between a missing/invalid credential and a valid ordinary user.
async fn forbidden_or_unauthorized(state: &IdentityTopupState, headers: &HeaderMap) -> Response {
    match actor(state, headers).await {
        Ok(_) => forbidden(),
        Err(response) => response,
    }
}

enum TopupListError {
    InvalidSearch(String),
    Database,
}

impl From<sqlx::Error> for TopupListError {
    fn from(_: sqlx::Error) -> Self {
        Self::Database
    }
}

/// Matches the frozen Go `sanitizeLikePattern` contract used by top-up
/// searches. `%` remains a caller-supplied wildcard; `!` and `_` are escaped
/// for PostgreSQL's explicit `ESCAPE '!'` clause.
fn like_pattern(input: &str) -> Result<String, String> {
    let escaped = input.replace('!', "!!").replace('_', "!_");
    if escaped.contains("%%") {
        return Err("搜索模式中不允许包含连续的 % 通配符".to_owned());
    }
    let wildcard_count = escaped.matches('%').count();
    if wildcard_count > 2 {
        return Err("搜索模式中最多允许包含 2 个 % 通配符".to_owned());
    }
    if wildcard_count > 0 && escaped.replace('%', "").len() < 2 {
        return Err("使用模糊搜索时，关键词长度至少为 2 个字符".to_owned());
    }
    Ok(escaped)
}

async fn list_topups(pool: &PgPool, query: &PageQuery, user_id: Option<i64>) -> Response {
    match fetch_topups(pool, query, user_id).await {
        Ok(page) => ok(page),
        Err(TopupListError::InvalidSearch(message)) => fail(message),
        Err(TopupListError::Database) => fail("系统错误"),
    }
}

async fn list_self_topups(pool: &PgPool, query: &PageQuery, user_id: Option<i64>) -> Response {
    match fetch_topups(pool, query, user_id).await {
        Ok(page) => {
            let Page {
                page,
                page_size,
                total,
                items,
            } = page;
            let items = items.into_iter().map(TopupSelfRecord::from).collect();
            ok(Page {
                page,
                page_size,
                total,
                items,
            })
        }
        Err(TopupListError::InvalidSearch(message)) => fail(message),
        Err(TopupListError::Database) => fail("系统错误"),
    }
}

async fn fetch_topups(
    pool: &PgPool,
    query: &PageQuery,
    user_id: Option<i64>,
) -> Result<Page<TopupRecord>, TopupListError> {
    let keyword = query.keyword.as_deref().unwrap_or("");
    let pattern = like_pattern(keyword).map_err(TopupListError::InvalidSearch)?;
    let cutoff = unix_now().saturating_sub(TOPUP_QUERY_WINDOW_SECONDS);
    let where_sql = if user_id.is_some() {
        "user_id = $1 AND create_time >= $2 AND ($3 = '' OR trade_no LIKE $4 ESCAPE '!')"
    } else {
        "($1 = '' OR trade_no LIKE $2 ESCAPE '!')"
    };
    // Go applies the 10k cap only in its keyword-search helpers.  Ordinary
    // admin/self history uses an uncapped Count inside the same transaction.
    let count_sql = if keyword.is_empty() {
        format!("SELECT COUNT(*) FROM top_ups WHERE {where_sql}")
    } else {
        format!(
            "SELECT COUNT(*) FROM (SELECT 1 FROM top_ups WHERE {where_sql} LIMIT {SEARCH_COUNT_HARD_LIMIT}) limited"
        )
    };
    let list_sql = format!(
        "SELECT {TOPUP_COLUMNS} FROM top_ups WHERE {where_sql} ORDER BY id DESC LIMIT ${} OFFSET ${}",
        if user_id.is_some() { 5 } else { 3 },
        if user_id.is_some() { 6 } else { 4 }
    );
    let mut tx = pool.begin().await?;
    let count_result = if let Some(id) = user_id {
        sqlx::query_scalar::<_, i64>(&count_sql)
            .bind(id)
            .bind(cutoff)
            .bind(keyword)
            .bind(&pattern)
            .fetch_one(&mut *tx)
            .await
    } else {
        sqlx::query_scalar::<_, i64>(&count_sql)
            .bind(keyword)
            .bind(&pattern)
            .fetch_one(&mut *tx)
            .await
    };
    let total = count_result?;
    let rows = if let Some(id) = user_id {
        sqlx::query(&list_sql)
            .bind(id)
            .bind(cutoff)
            .bind(keyword)
            .bind(&pattern)
            .bind(query.page_size())
            .bind(query.offset())
            .fetch_all(&mut *tx)
            .await
    } else {
        sqlx::query(&list_sql)
            .bind(keyword)
            .bind(&pattern)
            .bind(query.page_size())
            .bind(query.offset())
            .fetch_all(&mut *tx)
            .await
    };
    let rows = rows?;
    let items = rows
        .iter()
        .map(topup_record)
        .collect::<Result<Vec<_>, _>>()?;
    tx.commit().await?;
    Ok(Page {
        page: query.page(),
        page_size: query.page_size(),
        total,
        items,
    })
}

#[derive(Deserialize)]
struct RedeemRequest {
    key: String,
}

async fn redeem(
    State(state): State<IdentityTopupState>,
    headers: HeaderMap,
    body: Result<Json<RedeemRequest>, axum::extract::rejection::JsonRejection>,
) -> Response {
    let user = match actor(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return response,
    };
    if !payment_compliance(&state.pool).await.unwrap_or(false) {
        return fail(compliance_message(&headers));
    }
    let Json(request) = match body {
        Ok(value) => value,
        Err(_) => return fail(redeem_failure_message(&user, &headers)),
    };
    if request.key.is_empty() {
        return fail(redeem_failure_message(&user, &headers));
    }
    let mut tx = match state.pool.begin().await {
        Ok(tx) => tx,
        Err(_) => return fail(redeem_failure_message(&user, &headers)),
    };
    let row = match sqlx::query("SELECT id, COALESCE(quota, 0) AS quota, COALESCE(status, 0) AS status, COALESCE(expired_time, 0) AS expired_time FROM redemptions WHERE \"key\" = $1 AND deleted_at IS NULL FOR UPDATE")
        .bind(&request.key).fetch_optional(&mut *tx).await { Ok(Some(row)) => row, _ => return fail(redeem_failure_message(&user, &headers)) };
    let id: i64 = row.get("id");
    let quota: i64 = row.get("quota");
    let status: i64 = row.get("status");
    let expiry: i64 = row.get("expired_time");
    if status != REDEMPTION_ENABLED || (expiry != 0 && expiry < unix_now()) {
        return fail(redeem_failure_message(&user, &headers));
    }
    let changed = match sqlx::query("UPDATE redemptions SET redeemed_time = $1, status = $2, used_user_id = $3 WHERE id = $4 AND status = $5")
        .bind(unix_now()).bind(REDEMPTION_USED).bind(user.id).bind(id).bind(REDEMPTION_ENABLED).execute(&mut *tx).await { Ok(result) => result.rows_affected(), Err(_) => 0 };
    if changed != 1 {
        return fail(redeem_failure_message(&user, &headers));
    }
    let credited = match sqlx::query(
        "UPDATE users SET quota = COALESCE(quota, 0) + $1 WHERE id = $2 AND deleted_at IS NULL",
    )
    .bind(quota)
    .bind(user.id)
    .execute(&mut *tx)
    .await
    {
        Ok(result) => result.rows_affected(),
        Err(_) => 0,
    };
    if credited != 1 || tx.commit().await.is_err() {
        return fail(redeem_failure_message(&user, &headers));
    }
    // Go records this after the redemption transaction commits.  A log failure
    // must not turn a durable redemption into a failed response.
    let _ = record_redeem_log(&state.pool, user.id, &user.username, quota, id).await;
    ok(quota)
}

async fn record_redeem_log(
    pool: &PgPool,
    user_id: i64,
    username: &str,
    quota: i64,
    redemption_id: i64,
) -> Result<(), sqlx::Error> {
    let content = format!(
        "通过兑换码充值 {}，兑换码ID {redemption_id}",
        format_log_quota(pool, quota).await
    );
    sqlx::query("INSERT INTO logs (user_id, created_at, type, content, username, token_name, model_name, quota, prompt_tokens, completion_tokens, use_time, is_stream, channel_id, token_id, \"group\", ip, other) VALUES ($1, $2, 1, $3, $4, '', '', 0, 0, 0, 0, false, 0, 0, '', '', '')")
        .bind(user_id)
        .bind(unix_now())
        .bind(content)
        .bind(username)
        .execute(pool)
        .await?;
    Ok(())
}

#[derive(Deserialize)]
struct CompleteRequest {
    trade_no: String,
}

async fn complete_topup(
    State(state): State<IdentityTopupState>,
    context: Option<Extension<RequestContext>>,
    headers: HeaderMap,
    body: Result<Json<CompleteRequest>, axum::extract::rejection::JsonRejection>,
) -> Response {
    let administrator = match administrator(&state, &headers).await {
        Ok(user) => user,
        Err(_) => return forbidden_or_unauthorized(&state, &headers).await,
    };
    let caller_ip = caller_ip(context);
    let mut audit_success = false;
    let response = async {
        let Json(request) = match body {
            Ok(value) if !value.trade_no.is_empty() => value,
            _ => return fail("参数错误"),
        };
        let mut tx = match state.pool.begin().await {
            Ok(tx) => tx,
            Err(error) => return fail(legacy_database_error(&error)),
        };
        let row = match sqlx::query("SELECT id, COALESCE(user_id, 0) AS user_id, COALESCE(amount, 0) AS amount, COALESCE(money, 0)::text AS money, COALESCE(payment_method, '') AS payment_method, COALESCE(payment_provider, '') AS payment_provider, COALESCE(status, '') AS status, to_jsonb(top_ups) AS top_up_json FROM top_ups WHERE trade_no = $1 FOR UPDATE")
            .bind(&request.trade_no).fetch_optional(&mut *tx).await { Ok(Some(row)) => row, Ok(None) => return fail("充值订单不存在"), Err(_) => return fail("系统错误") };
        let status: String = row.get("status");
        if status == TOPUP_SUCCESS {
            if tx.commit().await.is_err() {
                return fail("系统错误");
            }
            // Frozen Go returns from inside the transaction before copying the
            // order fields into its outer variables, then still emits the
            // best-effort topup log with those zero values.
            let _ = record_manual_topup_log(&state.pool, 0, 0, "0", "", &caller_ip).await;
            audit_success = true;
            return ok(Value::Null);
        }
        if status != TOPUP_PENDING {
            return fail("订单状态不是待支付，无法补单");
        }
        let id: i64 = row.get("id");
        let user_id: i64 = row.get("user_id");
        let amount: i64 = row.get("amount");
        let payment_method: String = row.get("payment_method");
        let provider: String = row.get("payment_provider");
        let money: String = row.get("money");
        // Go loads `common.QuotaPerUnit` from the authoritative options table and
        // uses it while this same completion transaction holds the order lock.
        // Keep that snapshot local to this transaction so an option change cannot
        // split the credited amount from the completion state.
        let quota_per_unit = match quota_per_unit(&mut tx).await {
            Ok(value) => value,
            Err(_) => return fail("系统错误"),
        };
        let units = manual_topup_quota(
            amount,
            &provider,
            &money,
            quota_per_unit,
        )
        .unwrap_or(0);
        if units <= 0 {
            return fail("无效的充值额度");
        }
        // Current Go persists the normalized credited amount on the order
        // while completing it.  Keep the fallback update for older schemas
        // that predate the settlement columns; those schemas remain readable
        // during the staged migration, but current schemas get exact parity.
        let has_credited_quota = sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'top_ups' AND column_name = 'credited_quota')",
        )
        .fetch_one(&mut *tx)
        .await
        .unwrap_or(false);
        let completed_at = unix_now();
        let completion = if has_credited_quota {
            sqlx::query(
                "UPDATE top_ups SET credited_quota = $1, complete_time = $2, status = $3 WHERE id = $4 AND status = $5",
            )
            .bind(units)
            .bind(completed_at)
            .bind(TOPUP_SUCCESS)
            .bind(id)
            .bind(TOPUP_PENDING)
            .execute(&mut *tx)
            .await
        } else {
            sqlx::query(
                "UPDATE top_ups SET complete_time = $1, status = $2 WHERE id = $3 AND status = $4",
            )
            .bind(completed_at)
            .bind(TOPUP_SUCCESS)
            .bind(id)
            .bind(TOPUP_PENDING)
            .execute(&mut *tx)
            .await
        };
        match completion {
            Ok(result) if result.rows_affected() == 1 => {}
            Ok(_) => return fail("系统错误"),
            Err(error) => return fail(legacy_database_error(&error)),
        }
        match sqlx::query(
            "UPDATE users SET quota = COALESCE(quota, 0) + $1 WHERE id = $2 AND deleted_at IS NULL",
        )
        .bind(units)
        .bind(user_id)
        .execute(&mut *tx)
        .await {
            Ok(result) if result.rows_affected() == 1 => {}
            Ok(_) => return fail("系统错误"),
            Err(error) => return fail(legacy_database_error(&error)),
        }
        if let Err(error) = tx.commit().await {
            return fail(legacy_database_error(&error));
        }
        // Go writes this record after committing the authoritative order and
        // quota update. It is deliberately best effort.
        let _ = record_manual_topup_log(
            &state.pool,
            user_id,
            units,
            &money,
            &payment_method,
            &caller_ip,
        )
        .await;
        audit_success = true;
        ok(Value::Null)
    }
    .await;
    // Go's AdminAuth middleware audits every authenticated write, including
    // business failures and idempotent replays, after the handler completes.
    let _ =
        record_manual_topup_audit_log(&state.pool, &administrator, &caller_ip, audit_success).await;
    response
}

async fn record_manual_topup_log(
    pool: &PgPool,
    user_id: i64,
    quota: i64,
    money: &str,
    payment_method: &str,
    caller_ip: &str,
) -> Result<(), sqlx::Error> {
    let username =
        sqlx::query_scalar::<_, String>("SELECT COALESCE(username, '') FROM users WHERE id = $1")
            .bind(user_id)
            .fetch_optional(pool)
            .await?
            .unwrap_or_default();
    let quota_text = format_topup_quota(pool, quota).await;
    let money_text = money
        .parse::<f64>()
        .map(|value| format!("{value:.6}"))
        .unwrap_or_else(|_| money.to_owned());
    let other = json!({
        "admin_info": {
            "server_ip": server_ip(),
            "node_name": node_name(),
            "caller_ip": caller_ip,
            "payment_method": payment_method,
            "callback_payment_method": "admin",
            "version": service_version(),
        }
    })
    .to_string();
    sqlx::query("INSERT INTO logs (user_id, created_at, type, content, username, token_name, model_name, quota, prompt_tokens, completion_tokens, use_time, is_stream, channel_id, token_id, \"group\", ip, other) VALUES ($1, $2, 1, $3, $4, '', '', 0, 0, 0, 0, false, 0, 0, '', $5, $6)")
        .bind(user_id)
        .bind(unix_now())
        .bind(format!("管理员补单成功，充值金额: {quota_text}，支付金额：{money_text}"))
        .bind(username)
        .bind(caller_ip)
        .bind(other)
        .execute(pool)
        .await?;
    Ok(())
}

async fn record_manual_topup_audit_log(
    pool: &PgPool,
    administrator: &DashboardUser,
    caller_ip: &str,
    success: bool,
) -> Result<(), sqlx::Error> {
    let other = json!({
        "admin_info": {
            "admin_id": administrator.id,
            "admin_username": administrator.username,
            "admin_role": administrator.role,
            "auth_method": "session",
        },
        "audit_info": {
            "method": "POST",
            "route": "/api/user/topup/complete",
            "path": "/api/user/topup/complete",
            "status": 200,
            "success": success,
        },
        "op": {
            "action": "user.topup_complete",
        },
    })
    .to_string();
    sqlx::query("INSERT INTO logs (user_id, created_at, type, content, username, token_name, model_name, quota, prompt_tokens, completion_tokens, use_time, is_stream, channel_id, token_id, \"group\", ip, other) VALUES ($1, $2, 3, 'POST /api/user/topup/complete', $3, '', '', 0, 0, 0, 0, false, 0, 0, '', $4, $5)")
        .bind(administrator.id)
        .bind(unix_now())
        .bind(&administrator.username)
        .bind(caller_ip)
        .bind(other)
        .execute(pool)
        .await?;
    Ok(())
}

async fn format_log_quota(pool: &PgPool, quota: i64) -> String {
    format_quota(pool, quota, true).await
}

async fn format_topup_quota(pool: &PgPool, quota: i64) -> String {
    format_quota(pool, quota, false).await
}

async fn format_quota(pool: &PgPool, quota: i64, include_unit: bool) -> String {
    let options = read_options(pool).await.unwrap_or_default();
    let quota_per_unit = options
        .get("QuotaPerUnit")
        .and_then(|value| value.parse::<f64>().ok())
        .filter(|value| value.is_finite())
        .unwrap_or(DEFAULT_QUOTA_PER_UNIT);
    let (display_type, custom_symbol, custom_rate) = quota_display_settings(&options);
    let usd = quota as f64 / quota_per_unit;
    match display_type.as_str() {
        "CNY" => {
            let exchange_rate = options
                .get("USDExchangeRate")
                .and_then(|value| value.parse::<f64>().ok())
                .filter(|value| value.is_finite())
                .unwrap_or(7.3);
            format!(
                "¥{:.6}{}",
                usd * exchange_rate,
                if include_unit { " 额度" } else { "" }
            )
        }
        "CUSTOM" => {
            format!(
                "{custom_symbol}{:.6}{}",
                usd * custom_rate,
                if include_unit { " 额度" } else { "" }
            )
        }
        "TOKENS" if include_unit => format!("{quota} 点额度"),
        "TOKENS" => quota.to_string(),
        _ => format!("＄{usd:.6}{}", if include_unit { " 额度" } else { "" }),
    }
}

fn quota_display_settings(options: &HashMap<String, String>) -> (String, String, f64) {
    let general = options
        .get("general_setting")
        .and_then(|raw| serde_json::from_str::<Value>(raw).ok())
        .unwrap_or_else(|| json!({}));
    let display_type = options
        .get("general_setting.quota_display_type")
        .filter(|value| !value.trim().is_empty())
        .cloned()
        .or_else(|| {
            general
                .get("quota_display_type")
                .and_then(Value::as_str)
                .filter(|value| !value.trim().is_empty())
                .map(str::to_owned)
        })
        .unwrap_or_else(|| "USD".to_owned());
    let custom_symbol = options
        .get("general_setting.custom_currency_symbol")
        .filter(|value| !value.is_empty())
        .cloned()
        .or_else(|| {
            general
                .get("custom_currency_symbol")
                .and_then(Value::as_str)
                .filter(|value| !value.is_empty())
                .map(str::to_owned)
        })
        .unwrap_or_else(|| "¤".to_owned());
    let custom_rate = options
        .get("general_setting.custom_currency_exchange_rate")
        .and_then(|value| value.parse::<f64>().ok())
        .filter(|value| value.is_finite() && *value > 0.0)
        .or_else(|| {
            general
                .get("custom_currency_exchange_rate")
                .and_then(Value::as_f64)
                .filter(|value| value.is_finite() && *value > 0.0)
        })
        .unwrap_or(1.0);
    (display_type, custom_symbol, custom_rate)
}

fn caller_ip(context: Option<Extension<RequestContext>>) -> String {
    context
        .and_then(|Extension(context)| context.client_ip)
        .map(|ip| ip.to_string())
        .unwrap_or_default()
}

fn node_name() -> String {
    std::env::var("NODE_NAME")
        .or_else(|_| std::env::var("HOSTNAME"))
        .or_else(|_| std::fs::read_to_string("/etc/hostname").map(|value| value.trim().to_owned()))
        .unwrap_or_default()
}

fn server_ip() -> String {
    if let Ok(value) = std::env::var("LMM_SERVER_IP")
        && !value.trim().is_empty()
    {
        return value;
    }
    std::net::UdpSocket::bind("0.0.0.0:0")
        .and_then(|socket| {
            socket.connect("192.0.2.1:9")?;
            socket.local_addr()
        })
        .ok()
        .map(|address| address.ip().to_string())
        .unwrap_or_default()
}

fn service_version() -> String {
    std::env::var("VERSION").unwrap_or_else(|_| "v0.0.0".to_owned())
}

fn legacy_database_error(error: &sqlx::Error) -> String {
    if let sqlx::Error::Database(database) = error {
        if let Some(code) = database.code().filter(|code| !code.is_empty()) {
            return format!("ERROR: {} (SQLSTATE {code})", database.message());
        }
        return format!("ERROR: {}", database.message());
    }
    error.to_string()
}

/// Reads the live Go-compatible quota unit from the transaction snapshot.
///
/// `model.UpdateOption` assigns `strconv.ParseFloat`'s zero value on a
/// malformed persisted row.  A missing row leaves Go's historical default in
/// place, so the two cases intentionally differ here too.
async fn quota_per_unit(transaction: &mut Transaction<'_, Postgres>) -> Result<f64, sqlx::Error> {
    let stored = sqlx::query_scalar::<_, String>(
        "SELECT COALESCE(value, '') FROM options WHERE key = 'QuotaPerUnit'",
    )
    .fetch_optional(&mut **transaction)
    .await?;
    Ok(match stored {
        None => DEFAULT_QUOTA_PER_UNIT,
        Some(raw) => raw
            .parse::<f64>()
            .ok()
            .filter(|value| value.is_finite())
            .unwrap_or(0.0),
    })
}

/// Mirrors the frozen Go manual-completion path.  Stripe orders use their
/// stored money amount; every other order uses the stored display amount.  The
/// legacy endpoint intentionally does not gate this calculation on the
/// payment-provider allowlist or on a newer credited-quota column.
fn manual_topup_quota(
    amount: i64,
    provider: &str,
    money: &str,
    quota_per_unit: f64,
) -> Option<i64> {
    if !quota_per_unit.is_finite() {
        return None;
    }
    let quantity = if provider.trim() == "stripe" {
        money.parse::<f64>().ok()?
    } else {
        amount as f64
    };
    let quota = (quantity * quota_per_unit).trunc();
    (quota.is_finite() && quota >= i64::MIN as f64 && quota <= i64::MAX as f64)
        .then_some(quota as i64)
}

async fn topup_info(State(state): State<IdentityTopupState>, headers: HeaderMap) -> Response {
    // Frozen Go exposes the complete payment configuration to every ordinary
    // UserAuth principal. Console activation is not part of this route's
    // authorization contract; individual payment writes keep their own
    // compliance and credential guards.
    let actor = match actor_view(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let options = match read_options(&state.pool).await {
        Ok(options) => options,
        Err(_) => return fail("系统错误"),
    };
    let data = topup_info_data_for_user(&options, actor.developer_access_granted, &actor.group);
    ok(data)
}

#[cfg(test)]
fn topup_info_data(options: &HashMap<String, String>) -> Value {
    topup_info_data_for_user(options, true, "default")
}

fn topup_info_data_for_user(
    options: &HashMap<String, String>,
    developer_access_granted: bool,
    group: &str,
) -> Value {
    let compliant = payment_compliance_values(options);
    let stripe = compliant
        && nonempty(options, "StripeApiSecret")
        && nonempty(options, "StripeWebhookSecret")
        && nonempty(options, "StripePriceId");
    let mut pay_methods = if compliant {
        json_value(options, "PayMethods", default_pay_methods())
            .as_array()
            .cloned()
            .unwrap_or_default()
    } else {
        vec![]
    };
    let epay = compliant
        && nonempty(options, "PayAddress")
        && nonempty(options, "EpayId")
        && nonempty(options, "EpayKey")
        && !pay_methods.is_empty();
    let creem_products = options
        .get("CreemProducts")
        .cloned()
        .unwrap_or_else(|| "[]".to_owned());
    let creem = compliant
        && nonempty(options, "CreemApiKey")
        && !creem_products.trim().is_empty()
        && creem_products.trim() != "[]";
    let sandbox = bool_value(options, "WaffoSandbox");
    let waffo_credentials = if sandbox {
        nonempty(options, "WaffoSandboxApiKey")
            && nonempty(options, "WaffoSandboxPrivateKey")
            && nonempty(options, "WaffoSandboxPublicCert")
    } else {
        nonempty(options, "WaffoApiKey")
            && nonempty(options, "WaffoPrivateKey")
            && nonempty(options, "WaffoPublicCert")
    };
    let waffo = compliant && bool_value(options, "WaffoEnabled") && waffo_credentials;
    let pancake = compliant
        && nonempty(options, "WaffoPancakeMerchantID")
        && nonempty(options, "WaffoPancakePrivateKey")
        && nonempty(options, "WaffoPancakeProductID");
    append_payment_method(
        &mut pay_methods,
        stripe,
        "stripe",
        "Stripe",
        "#635BFF",
        integer(options, "StripeMinTopUp", 1),
    );
    append_payment_method(
        &mut pay_methods,
        pancake,
        "waffo_pancake",
        "Waffo Pancake",
        "#F97316",
        integer(options, "WaffoPancakeMinTopUp", 1),
    );
    append_payment_method(
        &mut pay_methods,
        waffo,
        "waffo",
        "Waffo (Global Payment)",
        "#3B82F6",
        integer(options, "WaffoMinTopUp", 1),
    );
    let (payment_available, min_payment) =
        neutral_topup_availability(options, epay, stripe, creem, waffo, pancake);
    let amount_options = payment_field(
        options,
        "amount_options",
        Value::Array(vec![
            json!(10),
            json!(20),
            json!(50),
            json!(100),
            json!(200),
            json!(500),
        ]),
    );
    let discount = payment_field(
        options,
        "amount_discount",
        Value::Object(Default::default()),
    );
    if !developer_access_granted {
        return json!({
            "developer_access_granted": false,
            "activation_required": true,
            "payment_available": payment_available,
            "min_payment": legacy_number(min_payment),
            "amount_options": amount_options,
            "discount": discount,
            "payment_compliance_confirmed": compliant,
            "payment_compliance_terms_version": "v1",
        });
    }
    json!({
        "enable_online_topup": epay,
        "developer_access_granted": true,
        "enable_stripe_topup": stripe,
        "enable_creem_topup": creem,
        "enable_waffo_topup": waffo,
        "enable_waffo_pancake_topup": pancake,
        "enable_redemption": compliant,
        "payment_compliance_confirmed": compliant,
        "payment_compliance_terms_version": "v1",
        "waffo_pay_methods": if waffo { waffo_pay_methods(options) } else { Value::Null },
        "creem_products": creem_products,
        "pay_methods": pay_methods,
        "topup_group_ratio": legacy_number(topup_group_ratio(options, group)),
        "min_topup": integer(options, "MinTopUp", 1),
        "stripe_min_topup": integer(options, "StripeMinTopUp", 1),
        "waffo_min_topup": integer(options, "WaffoMinTopUp", 1),
        "waffo_pancake_min_topup": integer(options, "WaffoPancakeMinTopUp", 1),
        "amount_options": amount_options,
        "discount": discount,
        "topup_link": options.get("TopUpLink").cloned().unwrap_or_default(),
    })
}

fn neutral_topup_availability(
    options: &HashMap<String, String>,
    online: bool,
    stripe: bool,
    creem: bool,
    waffo: bool,
    pancake: bool,
) -> (bool, f64) {
    let mut minimums = Vec::new();
    let mut add_minimum = |enabled: bool, value: i64| {
        if enabled && value > 0 {
            minimums.push(value as f64);
        }
    };
    add_minimum(online, integer(options, "MinTopUp", 1));
    add_minimum(stripe, integer(options, "StripeMinTopUp", 1));
    add_minimum(waffo, integer(options, "WaffoMinTopUp", 1));
    add_minimum(pancake, integer(options, "WaffoPancakeMinTopUp", 1));
    if creem
        && let Ok(Value::Array(products)) = serde_json::from_str::<Value>(
            options
                .get("CreemProducts")
                .map(String::as_str)
                .unwrap_or("[]"),
        ) {
            for product in products {
                if let Some(price) = product.get("price").and_then(Value::as_f64)
                    && price > 0.0
                {
                    minimums.push(price);
                }
            }
        }
    let available = online || stripe || creem || waffo || pancake;
    let minimum = minimums.into_iter().reduce(f64::min).unwrap_or(0.0);
    (available, minimum)
}

fn topup_group_ratio(options: &HashMap<String, String>, group: &str) -> f64 {
    let ratio = options
        .get("TopupGroupRatio")
        .and_then(|raw| serde_json::from_str::<Value>(raw).ok())
        .and_then(|value| value.get(group).and_then(Value::as_f64))
        .unwrap_or(1.0);
    if ratio == 0.0 { 1.0 } else { ratio }
}

/// Match Go's `encoding/json` spelling for integral `float64` values.
fn legacy_number(value: f64) -> Value {
    if value.is_finite()
        && value.fract() == 0.0
        && value >= i64::MIN as f64
        && value <= i64::MAX as f64
    {
        Value::from(value as i64)
    } else {
        Number::from_f64(value)
            .map(Value::Number)
            .unwrap_or(Value::Null)
    }
}

fn default_pay_methods() -> Value {
    json!([
        {"name": "支付宝", "icon": "SiAlipay", "type": "alipay"},
        {"name": "微信", "icon": "SiWechat", "type": "wxpay"},
        {"name": "自定义1", "icon": "LuCreditCard", "type": "custom1", "min_topup": "50"},
    ])
}

#[derive(Debug, Deserialize, Serialize)]
struct WaffoPayMethod {
    name: String,
    icon: String,
    #[serde(rename = "payMethodType")]
    pay_method_type: String,
    #[serde(rename = "payMethodName")]
    pay_method_name: String,
}

fn default_waffo_pay_methods() -> Value {
    json!([
        {
            "name": "Card",
            "icon": "/pay-card.png",
            "payMethodType": "CREDITCARD,DEBITCARD",
            "payMethodName": ""
        },
        {
            "name": "Apple Pay",
            "icon": "/pay-apple.png",
            "payMethodType": "APPLEPAY",
            "payMethodName": "APPLEPAY"
        },
        {
            "name": "Google Pay",
            "icon": "/pay-google.png",
            "payMethodType": "GOOGLEPAY",
            "payMethodName": "GOOGLEPAY"
        }
    ])
}

/// Match Go's typed `GetWaffoPayMethods` loader: missing/blank/invalid JSON
/// falls back to the built-in list, while a valid empty list remains empty.
fn waffo_pay_methods(options: &HashMap<String, String>) -> Value {
    let Some(raw) = options.get("WaffoPayMethods") else {
        return default_waffo_pay_methods();
    };
    if raw.trim().is_empty() {
        return default_waffo_pay_methods();
    }
    match serde_json::from_str::<Option<Vec<WaffoPayMethod>>>(raw) {
        Ok(Some(methods)) => {
            serde_json::to_value(methods).unwrap_or_else(|_| default_waffo_pay_methods())
        }
        Ok(None) => Value::Null,
        Err(_) => default_waffo_pay_methods(),
    }
}

fn append_payment_method(
    methods: &mut Vec<Value>,
    enabled: bool,
    kind: &str,
    name: &str,
    color: &str,
    min_topup: i64,
) {
    if enabled
        && !methods
            .iter()
            .any(|method| method.get("type").and_then(Value::as_str) == Some(kind))
    {
        methods.push(json!({
            "name": name,
            "type": kind,
            "color": color,
            "min_topup": min_topup.to_string(),
        }));
    }
}

async fn read_options(pool: &PgPool) -> Result<HashMap<String, String>, sqlx::Error> {
    Ok(
        sqlx::query("SELECT key, COALESCE(value, '') AS value FROM options")
            .fetch_all(pool)
            .await?
            .into_iter()
            .map(|row| (row.get("key"), row.get("value")))
            .collect(),
    )
}

async fn payment_compliance(pool: &PgPool) -> Result<bool, sqlx::Error> {
    Ok(payment_compliance_values(&read_options(pool).await?))
}
fn payment_compliance_values(options: &HashMap<String, String>) -> bool {
    let split_options = options
        .get("payment_setting.compliance_confirmed")
        .is_some_and(|value| value == "1" || value.eq_ignore_ascii_case("true"))
        && options
            .get("payment_setting.compliance_terms_version")
            .is_some_and(|value| value == "v1");
    if split_options {
        return true;
    }
    // Existing Go installations also retain the original registered JSON
    // configuration object.  Read it as a compatibility fallback while new
    // instances use the separately auditable compliance option keys above.
    options
        .get("payment_setting")
        .and_then(|raw| serde_json::from_str::<Value>(raw).ok())
        .is_some_and(|value| {
            value
                .get("compliance_confirmed")
                .and_then(Value::as_bool)
                .unwrap_or(false)
                && value
                    .get("compliance_terms_version")
                    .and_then(Value::as_str)
                    == Some("v1")
        })
}
fn payment_field(options: &HashMap<String, String>, field: &str, default: Value) -> Value {
    options
        .get(&format!("payment_setting.{field}"))
        .and_then(|raw| serde_json::from_str(raw).ok())
        .unwrap_or(default)
}
fn json_value(options: &HashMap<String, String>, key: &str, default: Value) -> Value {
    options
        .get(key)
        .and_then(|raw| serde_json::from_str(raw).ok())
        .unwrap_or(default)
}
fn bool_value(options: &HashMap<String, String>, key: &str) -> bool {
    // Waffo's Go option loader uses `value == "true"`, rather than the
    // broader truthy convention used by payment compliance settings.
    options.get(key).is_some_and(|value| value == "true")
}
fn nonempty(options: &HashMap<String, String>, key: &str) -> bool {
    options
        .get(key)
        .is_some_and(|value| !value.trim().is_empty())
}
fn integer(options: &HashMap<String, String>, key: &str, default: i64) -> i64 {
    options
        .get(key)
        .and_then(|value| value.parse().ok())
        .unwrap_or(default)
}
fn compliance_message(headers: &HeaderMap) -> &'static str {
    if headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.to_ascii_lowercase().starts_with("zh"))
    {
        "支付、兑换码、订阅计划和邀请返利功能已禁用。管理员需先确认合规声明后方可启用。"
    } else {
        "Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them."
    }
}

fn redeem_failure_message(user: &DashboardUser, headers: &HeaderMap) -> &'static str {
    let user_language = serde_json::from_str::<Value>(&user.setting)
        .ok()
        .and_then(|setting| setting.get("language")?.as_str().map(str::to_owned));
    let language = user_language.as_deref().or_else(|| {
        headers
            .get(header::ACCEPT_LANGUAGE)
            .and_then(|value| value.to_str().ok())
    });
    let language = language
        .and_then(|value| value.split(',').next())
        .and_then(|value| value.split(';').next())
        .unwrap_or_default()
        .trim()
        .to_ascii_lowercase();
    if language.starts_with("zh-tw") {
        "兌換失敗，請稍後重試"
    } else if language.starts_with("zh") {
        "兑换失败，请稍后重试"
    } else {
        "Redemption failed, please try again later"
    }
}

fn unix_now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs() as i64)
}

#[cfg(test)]
mod tests {
    use super::*;
    use async_trait::async_trait;
    use axum::{
        body::{Body, to_bytes},
        http::Request,
    };
    use sqlx::postgres::PgPoolOptions;
    use tower::ServiceExt;

    #[derive(Clone)]
    struct StaticAuth {
        role: i64,
    }
    #[async_trait]
    impl DashboardAuth for StaticAuth {
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
            Ok(DashboardUser {
                id: 7,
                username: "ordinary".into(),
                display_name: String::new(),
                role: self.role,
                status: 1,
                email: String::new(),
                github_id: String::new(),
                discord_id: String::new(),
                oidc_id: String::new(),
                wechat_id: String::new(),
                telegram_id: String::new(),
                group: "default".into(),
                quota: 0,
                used_quota: 0,
                request_count: 0,
                aff_code: String::new(),
                aff_count: 0,
                aff_quota: 0,
                aff_history_quota: 0,
                inviter_id: 0,
                linux_do_id: String::new(),
                setting: "{}".into(),
                stripe_customer: String::new(),
                sidebar_modules: json!({}),
                permissions: json!({}),
            })
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
    fn app(role: i64) -> Router {
        router(IdentityTopupState::new(
            PgPoolOptions::new()
                .acquire_timeout(std::time::Duration::from_millis(10))
                .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
                .unwrap(),
            Arc::new(StaticAuth { role }),
        ))
    }

    #[test]
    fn bearer_matches_go_bare_and_case_insensitive_forms() {
        for value in ["token", "Bearer token", "bearer token"] {
            let mut headers = HeaderMap::new();
            headers.insert(header::AUTHORIZATION, value.parse().unwrap());
            assert_eq!(bearer(&headers).as_deref(), Some("token"), "{value}");
        }
        let mut headers = HeaderMap::new();
        headers.insert(header::AUTHORIZATION, "Bearer token extra".parse().unwrap());
        assert_eq!(bearer(&headers), None);
    }

    async fn integration_pools() -> Option<(PgPool, PgPool, String)> {
        let database_url = std::env::var("LMM_TEST_DATABASE_URL").ok()?;
        let admin = PgPool::connect(&database_url)
            .await
            .expect("connect isolated PostgreSQL test database");
        let schema = format!("topup_complete_{}", uuid::Uuid::new_v4().simple());
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&admin)
            .await
            .expect("create isolated topup schema");
        let scoped = PgPoolOptions::new()
            .max_connections(1)
            .after_connect({
                let schema = schema.clone();
                move |connection, _metadata| {
                    let statement = format!("SET search_path TO {schema}");
                    Box::pin(async move {
                        sqlx::query(&statement).execute(connection).await?;
                        Ok(())
                    })
                }
            })
            .connect(&database_url)
            .await
            .expect("connect isolated topup schema");
        Some((admin, scoped, schema))
    }

    async fn response_json(response: Response) -> Value {
        serde_json::from_slice(
            &to_bytes(response.into_body(), usize::MAX)
                .await
                .expect("read response body"),
        )
        .expect("response is JSON")
    }

    fn admin_headers() -> HeaderMap {
        let mut headers = HeaderMap::new();
        headers.insert(
            header::AUTHORIZATION,
            "Bearer administrator".parse().unwrap(),
        );
        headers
    }

    #[tokio::test]
    async fn public_topup_reads_retain_the_frozen_unauthorized_envelope() {
        for (method, uri) in [
            ("GET", "/api/user/topup"),
            ("GET", "/api/user/topup/info"),
            ("GET", "/api/user/topup/self"),
        ] {
            let request = Request::builder()
                .method(method)
                .uri(uri)
                .body(Body::empty())
                .unwrap();
            let response = app(1).oneshot(request).await.unwrap();
            assert_eq!(response.status(), StatusCode::UNAUTHORIZED, "{uri}");
            assert_eq!(response.headers()[header::CONTENT_TYPE], "application/json");
            assert_eq!(
                response_json(response).await,
                json!({
                    "success": false,
                    "code": "AUTH_UNAUTHORIZED",
                    "message": "Unauthorized, invalid access token",
                }),
                "{uri}"
            );
        }
    }

    #[tokio::test]
    async fn public_topup_writes_require_a_dashboard_credential() {
        for (method, uri) in [
            ("POST", "/api/user/topup"),
            ("POST", "/api/user/topup/complete"),
        ] {
            let request = Request::builder()
                .method(method)
                .uri(uri)
                .body(Body::empty())
                .unwrap();
            assert_eq!(
                app(1).oneshot(request).await.unwrap().status(),
                StatusCode::UNAUTHORIZED,
                "{uri}"
            );
        }
    }

    #[tokio::test]
    async fn ordinary_users_cannot_reach_administrator_topup_seams() {
        for (method, uri) in [
            ("GET", "/api/user/topup"),
            ("POST", "/api/user/topup/complete"),
        ] {
            let request = Request::builder()
                .method(method)
                .uri(uri)
                .header(header::AUTHORIZATION, "Bearer ordinary")
                .body(Body::empty())
                .unwrap();
            assert_eq!(
                app(1).oneshot(request).await.unwrap().status(),
                StatusCode::FORBIDDEN,
                "{uri}"
            );
        }
    }

    #[tokio::test]
    async fn raw_pagination_keeps_bad_integer_requests_inside_the_legacy_handler() {
        for uri in [
            "/api/user/topup?p=-1&size=-1",
            "/api/user/topup?p=-1&size=0",
            "/api/user/topup?p=-1&size=101",
            "/api/user/topup?p=invalid&page_size=bad",
            "/api/user/topup/self?p=invalid&page_size=bad",
        ] {
            let request = Request::builder()
                .uri(uri)
                .header(header::AUTHORIZATION, "Bearer administrator")
                .body(Body::empty())
                .unwrap();
            assert_ne!(
                app(ROLE_ADMIN).oneshot(request).await.unwrap().status(),
                StatusCode::BAD_REQUEST,
                "{uri} must not receive Axum's query-extractor rejection"
            );
        }
    }

    #[test]
    fn manual_topup_quota_matches_the_frozen_provider_switch() {
        assert_eq!(
            manual_topup_quota(2, "epay", "2", DEFAULT_QUOTA_PER_UNIT),
            Some(1_000_000)
        );
        assert_eq!(
            manual_topup_quota(2, "", "2", DEFAULT_QUOTA_PER_UNIT),
            Some(1_000_000)
        );
        assert_eq!(
            manual_topup_quota(2, "stripe", "3.5", DEFAULT_QUOTA_PER_UNIT),
            Some(1_750_000)
        );
        assert_eq!(manual_topup_quota(2, "epay", "2", f64::NAN), None);
    }

    #[test]
    fn page_query_keeps_gin_fallbacks_and_bad_integer_tolerance() {
        let cases = [
            ("p=-1&size=-1", -1, -1),
            ("p=-1&size=0", -1, 10),
            ("p=-1&size=101", -1, 100),
            ("p=%2D1&size=101", -1, 100),
            ("p=invalid&page_size=bad&ps=0&size=0", 1, 10),
        ];
        for (raw, page, size) in cases {
            let query = PageQuery::from_raw(Some(raw));
            assert_eq!((query.page(), query.page_size()), (page, size), "{raw}");
        }
    }

    #[test]
    fn topup_search_pattern_matches_go_wildcard_contract() {
        assert_eq!(like_pattern("trade-42").unwrap(), "trade-42");
        assert_eq!(like_pattern("%trade-%").unwrap(), "%trade-%");
        assert_eq!(like_pattern("a_b").unwrap(), "a!_b");
        assert_eq!(like_pattern("%_").unwrap(), "%!_");
        assert_eq!(like_pattern("%!").unwrap(), "%!!");
        assert_eq!(
            like_pattern("a%%b").unwrap_err(),
            "搜索模式中不允许包含连续的 % 通配符"
        );
        assert_eq!(
            like_pattern("%a%b%").unwrap_err(),
            "搜索模式中最多允许包含 2 个 % 通配符"
        );
        assert_eq!(
            like_pattern("%a").unwrap_err(),
            "使用模糊搜索时，关键词长度至少为 2 个字符"
        );
    }

    #[tokio::test]
    async fn redemption_failure_uses_user_language_then_header_then_english_default() {
        let auth = StaticAuth { role: 1 };
        let mut user = auth
            .self_user(SecretString::from("test-session".to_owned()))
            .await
            .unwrap();
        let mut headers = HeaderMap::new();

        assert_eq!(
            redeem_failure_message(&user, &headers),
            "Redemption failed, please try again later"
        );

        headers.insert(header::ACCEPT_LANGUAGE, "zh-CN".parse().unwrap());
        assert_eq!(
            redeem_failure_message(&user, &headers),
            "兑换失败，请稍后重试"
        );

        user.setting = r#"{"language":"zh-TW"}"#.to_owned();
        headers.insert(header::ACCEPT_LANGUAGE, "en".parse().unwrap());
        assert_eq!(
            redeem_failure_message(&user, &headers),
            "兌換失敗，請稍後重試"
        );
    }

    #[test]
    fn topup_info_matches_payment_webhook_availability_matrix() {
        let mut options = HashMap::from([
            ("payment_setting.compliance_confirmed".into(), "true".into()),
            (
                "payment_setting.compliance_terms_version".into(),
                "v1".into(),
            ),
            ("PayMethods".into(), r#"[{"type":"alipay"}]"#.into()),
            ("PayAddress".into(), "https://epay.example".into()),
            ("EpayId".into(), "id".into()),
            ("EpayKey".into(), "key".into()),
            ("StripeApiSecret".into(), "sk".into()),
            ("StripeWebhookSecret".into(), "whsec".into()),
            ("StripePriceId".into(), "price".into()),
            ("CreemApiKey".into(), "creem".into()),
            ("CreemProducts".into(), r#"[{"productId":"prod"}]"#.into()),
            ("WaffoEnabled".into(), "true".into()),
            ("WaffoSandbox".into(), "true".into()),
            ("WaffoSandboxApiKey".into(), "api".into()),
            ("WaffoSandboxPrivateKey".into(), "private".into()),
            ("WaffoSandboxPublicCert".into(), "cert".into()),
            ("WaffoPancakeMerchantID".into(), "merchant".into()),
            ("WaffoPancakePrivateKey".into(), "private".into()),
            ("WaffoPancakeProductID".into(), "product".into()),
        ]);
        let enabled = topup_info_data(&options);
        assert_eq!(enabled["developer_access_granted"], true);
        assert!(enabled["enable_online_topup"].as_bool().unwrap());
        assert!(enabled["enable_stripe_topup"].as_bool().unwrap());
        assert!(enabled["enable_creem_topup"].as_bool().unwrap());
        assert!(enabled["enable_waffo_topup"].as_bool().unwrap());
        assert!(enabled["enable_waffo_pancake_topup"].as_bool().unwrap());
        assert_eq!(enabled["payment_compliance_terms_version"], "v1");
        assert_eq!(
            enabled["creem_products"],
            Value::String(r#"[{"productId":"prod"}]"#.into())
        );

        let neutral = topup_info_data_for_user(&options, false, "default");
        assert_eq!(neutral["developer_access_granted"], false);
        assert_eq!(neutral["activation_required"], true);
        assert_eq!(neutral["payment_available"], true);
        assert_eq!(neutral["min_payment"], 1.0);
        assert!(neutral.get("enable_stripe_topup").is_none());

        options.insert("TopupGroupRatio".into(), r#"{"vip":1.5}"#.into());
        assert_eq!(
            topup_info_data_for_user(&options, true, "vip")["topup_group_ratio"],
            1.5
        );

        options.insert(
            "payment_setting.compliance_confirmed".into(),
            "false".into(),
        );
        let compliance_off = topup_info_data(&options);
        for key in [
            "enable_online_topup",
            "enable_stripe_topup",
            "enable_creem_topup",
            "enable_waffo_topup",
            "enable_waffo_pancake_topup",
        ] {
            assert_eq!(compliance_off[key], false, "{key}");
        }

        options.insert("payment_setting.compliance_confirmed".into(), "true".into());
        options.remove("StripeWebhookSecret");
        options.remove("WaffoSandboxPublicCert");
        let missing_credentials = topup_info_data(&options);
        assert_eq!(missing_credentials["enable_stripe_topup"], false);
        assert_eq!(missing_credentials["enable_waffo_topup"], false);

        options.insert("WaffoSandbox".into(), "false".into());
        options.insert("WaffoApiKey".into(), "prod-api".into());
        options.insert("WaffoPrivateKey".into(), "prod-private".into());
        options.insert("WaffoPublicCert".into(), "prod-cert".into());
        let production_waffo = topup_info_data(&options);
        assert_eq!(production_waffo["enable_waffo_topup"], true);
        options.insert("WaffoEnabled".into(), "1".into());
        assert_eq!(topup_info_data(&options)["enable_waffo_topup"], false);
    }

    #[test]
    fn topup_info_defaults_match_go_option_map_wire_shape() {
        let options = HashMap::from([
            ("payment_setting.compliance_confirmed".into(), "true".into()),
            (
                "payment_setting.compliance_terms_version".into(),
                "v1".into(),
            ),
        ]);
        let value = topup_info_data(&options);
        assert_eq!(value["topup_group_ratio"], json!(1));
        assert_eq!(
            value["pay_methods"],
            json!([
                {"name": "支付宝", "icon": "SiAlipay", "type": "alipay"},
                {"name": "微信", "icon": "SiWechat", "type": "wxpay"},
                {"name": "自定义1", "icon": "LuCreditCard", "type": "custom1", "min_topup": "50"},
            ])
        );
    }

    #[test]
    fn quota_display_settings_prefer_go_dotted_options_and_keep_legacy_fallback() {
        let mut options = HashMap::from([(
            "general_setting".into(),
            r#"{"quota_display_type":"CNY","custom_currency_symbol":"X","custom_currency_exchange_rate":2}"#.into(),
        )]);
        assert_eq!(
            quota_display_settings(&options),
            ("CNY".into(), "X".into(), 2.0)
        );
        options.insert("general_setting.quota_display_type".into(), "CUSTOM".into());
        options.insert("general_setting.custom_currency_symbol".into(), "¤¤".into());
        options.insert(
            "general_setting.custom_currency_exchange_rate".into(),
            "3.5".into(),
        );
        assert_eq!(
            quota_display_settings(&options),
            ("CUSTOM".into(), "¤¤".into(), 3.5)
        );
    }

    #[test]
    fn waffo_pay_methods_keep_go_defaults_and_typed_fallbacks() {
        let mut options = HashMap::new();
        let defaults = json!([
            {
                "name": "Card",
                "icon": "/pay-card.png",
                "payMethodType": "CREDITCARD,DEBITCARD",
                "payMethodName": ""
            },
            {
                "name": "Apple Pay",
                "icon": "/pay-apple.png",
                "payMethodType": "APPLEPAY",
                "payMethodName": "APPLEPAY"
            },
            {
                "name": "Google Pay",
                "icon": "/pay-google.png",
                "payMethodType": "GOOGLEPAY",
                "payMethodName": "GOOGLEPAY"
            }
        ]);
        assert_eq!(waffo_pay_methods(&options), defaults);

        options.insert("WaffoPayMethods".into(), "not-json".into());
        assert_eq!(waffo_pay_methods(&options), defaults);
        options.insert("WaffoPayMethods".into(), "[]".into());
        assert_eq!(waffo_pay_methods(&options), json!([]));
        options.insert(
            "WaffoPayMethods".into(),
            r#"[{"name":"Custom","icon":"/custom.png","payMethodType":"CUSTOM","payMethodName":""}]"#.into(),
        );
        assert_eq!(
            waffo_pay_methods(&options),
            json!([{
                "name": "Custom",
                "icon": "/custom.png",
                "payMethodType": "CUSTOM",
                "payMethodName": ""
            }])
        );
        options.insert("WaffoPayMethods".into(), r#"[{"name":1}]"#.into());
        assert_eq!(waffo_pay_methods(&options), defaults);
    }

    #[test]
    fn self_topup_record_keeps_the_go_public_shape() {
        let value = serde_json::to_value(TopupSelfRecord::from(TopupRecord {
            id: 7,
            user_id: 11,
            amount: 20,
            credited_quota: 0,
            expected_amount_micros: 0,
            settled_amount_micros: 0,
            settlement_currency: String::new(),
            money: json!(20.0),
            trade_no: "trade-7".into(),
            payment_method: "stripe".into(),
            payment_provider: "stripe".into(),
            provider_product_id: String::new(),
            provider_store_id: String::new(),
            provider_event_id: None,
            provider_transaction_id: None,
            create_time: 100,
            complete_time: 200,
            status: "success".into(),
        }))
        .expect("serialize self top-up record");
        assert_eq!(value["id"], 7);
        assert_eq!(value["money"], 20.0);
        assert_eq!(value["payment_method"], "stripe");
        assert!(value.get("payment_provider").is_none());
    }

    #[test]
    fn money_value_matches_go_integral_float_wire_shape() {
        assert_eq!(serde_json::to_string(&money_value("2.000")).unwrap(), "2");
        assert_eq!(serde_json::to_string(&money_value("2.5")).unwrap(), "2.5");
        assert_eq!(
            serde_json::to_string(&money_value("not-a-number")).unwrap(),
            "\"not-a-number\""
        );
    }

    #[tokio::test]
    async fn complete_topup_uses_authoritative_quota_option_and_is_atomic_and_idempotent() {
        let Some((admin, pool, schema)) = integration_pools().await else {
            eprintln!("skipping PostgreSQL topup completion test: LMM_TEST_DATABASE_URL is unset");
            return;
        };
        for statement in [
            "CREATE TABLE options (key TEXT PRIMARY KEY, value TEXT)",
            "CREATE TABLE users (id BIGINT PRIMARY KEY, username TEXT, quota BIGINT, deleted_at TIMESTAMPTZ)",
            "CREATE TABLE top_ups (id BIGINT PRIMARY KEY, user_id BIGINT, amount BIGINT, money NUMERIC, trade_no TEXT UNIQUE, payment_method TEXT, payment_provider TEXT, create_time BIGINT, complete_time BIGINT, status TEXT)",
            "CREATE TABLE logs (user_id BIGINT, created_at BIGINT, type BIGINT, content TEXT, username TEXT, token_name TEXT, model_name TEXT, quota BIGINT, prompt_tokens BIGINT, completion_tokens BIGINT, use_time BIGINT, is_stream BOOLEAN, channel_id BIGINT, token_id BIGINT, \"group\" TEXT, ip TEXT, other TEXT)",
            "CREATE TABLE redemptions (id BIGINT PRIMARY KEY, \"key\" TEXT UNIQUE, quota BIGINT, status BIGINT, expired_time BIGINT, deleted_at TIMESTAMPTZ, redeemed_time BIGINT, used_user_id BIGINT)",
        ] {
            sqlx::query(statement)
                .execute(&pool)
                .await
                .expect("create topup fixture table");
        }
        sqlx::query("INSERT INTO options (key, value) VALUES ('QuotaPerUnit', '1234.5'), ('payment_setting.compliance_confirmed', 'true'), ('payment_setting.compliance_terms_version', 'v1')")
            .execute(&pool)
            .await
            .expect("seed non-default quota unit");
        sqlx::query("INSERT INTO users (id, username, quota) VALUES (7, 'ordinary', 0), (11, 'credited-user', 7)")
            .execute(&pool)
            .await
            .expect("seed credited user");
        sqlx::query("INSERT INTO top_ups (id, user_id, amount, money, trade_no, payment_method, payment_provider, create_time, complete_time, status) VALUES (1, 11, 2, 0, 'non-default', 'epay', 'epay', 0, 0, 'pending'), (2, 404, 2, 0, 'rollback', 'epay', 'epay', 0, 0, 'pending')")
            .execute(&pool)
            .await
            .expect("seed pending topups");
        sqlx::query("INSERT INTO top_ups (id, user_id, amount, money, trade_no, payment_method, payment_provider, create_time, complete_time, status) SELECT id, 11, 1, 0, 'bulk-' || id, 'epay', 'epay', 0, 0, 'pending' FROM generate_series(100, 10100) AS id")
            .execute(&pool)
            .await
            .expect("seed more than the search count cap");

        let ordinary_history =
            response_json(list_topups(&pool, &PageQuery::from_raw(None), None).await).await;
        assert_eq!(ordinary_history["data"]["total"], 10_003);
        let searched_history = response_json(
            list_topups(
                &pool,
                &PageQuery::from_raw(Some("keyword=%25bulk-%25")),
                None,
            )
            .await,
        )
        .await;
        assert_eq!(searched_history["data"]["total"], SEARCH_COUNT_HARD_LIMIT);

        let state =
            IdentityTopupState::new(pool.clone(), Arc::new(StaticAuth { role: ROLE_ADMIN }));
        let completed = complete_topup(
            State(state.clone()),
            None,
            admin_headers(),
            Ok(Json(CompleteRequest {
                trade_no: "non-default".into(),
            })),
        )
        .await;
        assert_eq!(response_json(completed).await["success"], true);
        let quota: i64 = sqlx::query_scalar("SELECT quota FROM users WHERE id = 11")
            .fetch_one(&pool)
            .await
            .expect("read credited quota");
        assert_eq!(quota, 2_476, "2 * configured 1234.5, truncated like Go");
        let audit: (i64, String, String) =
            sqlx::query_as("SELECT type, content, other FROM logs WHERE user_id = 11")
                .fetch_one(&pool)
                .await
                .expect("manual completion writes its best-effort audit record");
        assert_eq!(audit.0, 1);
        assert!(audit.1.contains("管理员补单成功"));
        assert!(audit.2.contains("callback_payment_method"));

        let duplicate = complete_topup(
            State(state.clone()),
            None,
            admin_headers(),
            Ok(Json(CompleteRequest {
                trade_no: "non-default".into(),
            })),
        )
        .await;
        assert_eq!(response_json(duplicate).await["success"], true);
        let quota_after_retry: i64 = sqlx::query_scalar("SELECT quota FROM users WHERE id = 11")
            .fetch_one(&pool)
            .await
            .expect("read idempotent quota");
        assert_eq!(
            quota_after_retry, quota,
            "successful orders are not credited twice"
        );

        sqlx::query("INSERT INTO top_ups (id, user_id, amount, money, trade_no, payment_method, payment_provider, create_time, complete_time, status) VALUES (3, 11, 1, 0, ' padded ', 'epay', 'epay', 0, 0, 'pending')")
            .execute(&pool)
            .await
            .expect("seed whitespace-sensitive trade number");
        let whitespace_trade = complete_topup(
            State(state.clone()),
            None,
            admin_headers(),
            Ok(Json(CompleteRequest {
                trade_no: " padded ".into(),
            })),
        )
        .await;
        assert_eq!(response_json(whitespace_trade).await["success"], true);
        let trimmed_trade = complete_topup(
            State(state.clone()),
            None,
            admin_headers(),
            Ok(Json(CompleteRequest {
                trade_no: "padded".into(),
            })),
        )
        .await;
        assert_eq!(response_json(trimmed_trade).await["success"], false);

        sqlx::query("INSERT INTO redemptions (id, \"key\", quota, status, expired_time) VALUES (1, ' zero-key ', 0, 1, 0)")
            .execute(&pool)
            .await
            .expect("seed zero-quota historical redemption");
        let zero_redemption = redeem(
            State(state.clone()),
            admin_headers(),
            Ok(Json(RedeemRequest {
                key: " zero-key ".into(),
            })),
        )
        .await;
        assert_eq!(response_json(zero_redemption).await["success"], true);
        let redemption_status: i64 =
            sqlx::query_scalar("SELECT status FROM redemptions WHERE id = 1")
                .fetch_one(&pool)
                .await
                .expect("read consumed zero-quota redemption");
        assert_eq!(redemption_status, REDEMPTION_USED);

        let rejected = complete_topup(
            State(state),
            None,
            admin_headers(),
            Ok(Json(CompleteRequest {
                trade_no: "rollback".into(),
            })),
        )
        .await;
        assert_eq!(response_json(rejected).await["success"], false);
        let status: String = sqlx::query_scalar("SELECT status FROM top_ups WHERE id = 2")
            .fetch_one(&pool)
            .await
            .expect("read rolled-back topup");
        assert_eq!(
            status, TOPUP_PENDING,
            "failed quota credit rolls back completion"
        );

        pool.close().await;
        sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
            .execute(&admin)
            .await
            .expect("drop isolated topup schema");
        admin.close().await;
    }
}
