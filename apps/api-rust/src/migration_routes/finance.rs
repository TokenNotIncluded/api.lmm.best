//! Administrator finance dashboard routes (overview, ledger, payment methods, users).
//!
//! Export remains in [`super::finance_export`]; this module covers the interactive
//! dashboard surface mounted under `/api/finance/*` except `/export`.

use std::{
    collections::{BTreeMap, HashMap},
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{Path, RawQuery, State},
    http::{HeaderMap, HeaderName, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get as route_get, post as route_post, put as route_put},
};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{PgPool, Row, postgres::PgRow};

use crate::auth::{DashboardAuth, DashboardUserView, UserAuthPolicyError, enforce_user_auth_view};

const ADMIN_ROLE: i64 = 10;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const MAX_BODY_BYTES: usize = 64 * 1024;
const DEFAULT_WINDOW_SECONDS: i64 = 30 * 24 * 60 * 60;
const MAX_WINDOW_SECONDS: i64 = 366 * 24 * 60 * 60;
const MAX_SOURCE_ROWS: i64 = 100_000;
const BATCH_SIZE: i64 = 1_000;
const MAX_ENTRIES: i64 = 100;
const MAX_USER_METRICS: usize = 100_000;
const MAX_METHOD_USER_PAIRS: i64 = 200_000;
const MAX_PAYMENT_METHODS: i64 = 256;
const FINANCE_CURRENCY_USD: &str = "USD";
const TOPUP_STATUS_SUCCESS: &str = "success";
const FINANCE_ENTRY_EXPENSE: &str = "expense";
const FINANCE_ENTRY_REVENUE: &str = "revenue";
const FINANCE_ENTRY_TOKEN_COST: &str = "token_cost";
const FINANCE_DIRECTION_DEBIT: i8 = -1;
const FINANCE_DIRECTION_CREDIT: i8 = 1;
const FINANCE_SOURCE_MANUAL: &str = "manual";
const LOG_TYPE_CONSUME: i64 = 2;

/// PostgreSQL and dashboard-auth dependencies for finance dashboard routes.
#[derive(Clone)]
pub struct FinanceState {
    backend: Arc<dyn FinanceBackend>,
    auth: Arc<dyn DashboardAuth>,
}

impl FinanceState {
    #[must_use]
    pub fn new(pg: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self::with_backend(Arc::new(PgFinanceBackend { pg }), auth)
    }

    #[must_use]
    pub fn with_backend(backend: Arc<dyn FinanceBackend>, auth: Arc<dyn DashboardAuth>) -> Self {
        Self { backend, auth }
    }
}

/// Builds finance dashboard routes (not including export).
pub fn router(state: FinanceState) -> Router {
    Router::new()
        .route("/api/finance/overview", route_get(get_overview))
        .route(
            "/api/finance/entries",
            route_get(list_entries).post(create_entry),
        )
        .route(
            "/api/finance/entries/{entry_id}/reverse",
            route_post(reverse_entry),
        )
        .route(
            "/api/finance/payment-methods",
            route_get(list_payment_methods),
        )
        .route(
            "/api/finance/payment-methods/{method}",
            route_put(update_payment_method),
        )
        .route("/api/finance/users", route_get(list_users))
        .route("/api/finance/users/{user_id}", route_get(get_user))
        .with_state(state)
}

#[derive(Clone, Debug, Serialize)]
struct FinanceRange {
    start: i64,
    end: i64,
}

#[derive(Clone, Debug, Default, Serialize)]
struct FinanceMethodMetric {
    method: String,
    provider: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    category: String,
    amount_micros: i64,
    orders: i64,
    users: i64,
    token_units: i64,
}

#[derive(Clone, Debug, Default, Eq, PartialEq, Serialize)]
struct FinanceCurrencyMetric {
    currency: String,
    amount_micros: i64,
    orders: i64,
}

#[derive(Clone, Debug, Serialize)]
struct FinanceDailyMetric {
    date: String,
    revenue_micros: i64,
    refund_micros: i64,
    expense_micros: i64,
    profit_micros: i64,
    token_units: i64,
    requests: i64,
}

#[derive(Clone, Debug, Default, Serialize)]
struct FinanceUserMetric {
    user_id: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    username: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    display_name: String,
    revenue_micros: i64,
    refund_micros: i64,
    expense_micros: i64,
    token_cost_micros: i64,
    token_units: i64,
    requests: i64,
}

#[derive(Clone, Debug, Default, Serialize)]
struct FinanceTokenMetric {
    prompt_tokens: i64,
    completion_tokens: i64,
    total_tokens: i64,
    requests: i64,
    estimated_cost_micros: i64,
    unpriced_requests: i64,
}

#[derive(Clone, Debug, Serialize)]
struct FinancePaymentMethod {
    id: i64,
    method: String,
    label: String,
    enabled: bool,
    include_revenue: bool,
    created_at: i64,
    updated_at: i64,
    created_by: i64,
}

#[derive(Clone, Debug, Serialize)]
struct FinanceOverview {
    range: FinanceRange,
    currency: String,
    revenue_micros: i64,
    refund_micros: i64,
    net_revenue_micros: i64,
    expense_micros: i64,
    profit_micros: i64,
    cost_attribution: String,
    revenue_by_method: Vec<FinanceMethodMetric>,
    refund_by_method: Vec<FinanceMethodMetric>,
    expense_by_method: Vec<FinanceMethodMetric>,
    tokens: FinanceTokenMetric,
    daily: Vec<FinanceDailyMetric>,
    users: Vec<FinanceUserMetric>,
    payment_methods: Vec<FinancePaymentMethod>,
    settlement_revenue_by_currency: Vec<FinanceCurrencyMetric>,
    unclassified_settlement_orders: i64,
    sources_bounded: bool,
    user_metrics_truncated: bool,
    user_metrics_complete: bool,
    user_metrics_limit: i64,
    method_user_metrics_complete: bool,
    method_user_metrics_limit: i64,
}

#[derive(Clone, Debug, Serialize)]
struct FinanceLedgerEntry {
    id: i64,
    entry_type: String,
    category: String,
    amount_micros: i64,
    currency: String,
    direction: i8,
    payment_method: String,
    payment_provider: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    user_id: Option<i64>,
    source_type: String,
    source_id: String,
    token_units: i64,
    note: String,
    occurred_at: i64,
    created_at: i64,
    created_by: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    reversal_of_id: Option<i64>,
}

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
#[error("{0}")]
pub struct FinanceError(pub String);

#[async_trait]
pub trait FinanceBackend: Send + Sync {
    async fn overview(
        &self,
        start: i64,
        end: i64,
        user_filter: i64,
        method_filter: &str,
    ) -> Result<FinanceOverview, FinanceError>;

    async fn list_entries(
        &self,
        start: i64,
        end: i64,
        user_id: i64,
        method: &str,
        entry_type: &str,
        before_occurred_at: i64,
        before_id: i64,
        limit: i64,
    ) -> Result<(Vec<FinanceLedgerEntry>, bool, Option<(i64, i64)>), FinanceError>;

    async fn create_entry(
        &self,
        actor_id: i64,
        input: CreateEntryInput,
    ) -> Result<FinanceLedgerEntry, FinanceError>;

    async fn reverse_entry(
        &self,
        entry_id: i64,
        actor_id: i64,
        now: i64,
    ) -> Result<FinanceLedgerEntry, FinanceError>;

    async fn list_payment_methods(&self) -> Result<Vec<FinancePaymentMethod>, FinanceError>;

    async fn update_payment_method(
        &self,
        method: &str,
        actor_id: i64,
        input: PaymentMethodInput,
    ) -> Result<FinancePaymentMethod, FinanceError>;
}

#[derive(Clone, Debug, Default, Deserialize)]
struct CreateEntryInput {
    #[serde(default)]
    entry_type: String,
    #[serde(default)]
    category: String,
    #[serde(default)]
    amount_micros: i64,
    #[serde(default)]
    currency: String,
    #[serde(default)]
    payment_method: String,
    #[serde(default)]
    payment_provider: String,
    user_id: Option<i64>,
    #[serde(default)]
    note: String,
    #[serde(default)]
    occurred_at: i64,
    #[serde(default)]
    idempotency_key: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct PaymentMethodInput {
    label: Option<String>,
    enabled: Option<bool>,
    include_revenue: Option<bool>,
}

#[derive(Clone)]
struct PgFinanceBackend {
    pg: PgPool,
}

// --- route handlers ---

async fn get_overview(
    State(state): State<FinanceState>,
    headers: HeaderMap,
    RawQuery(raw): RawQuery,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let query = parse_query(raw.as_deref());
    let response = match parse_dashboard_range(&query) {
        Ok((start, end)) => {
            let user_id = match parse_optional_user_id(&query) {
                Ok(id) => id,
                Err(message) => return with_auth_version(api_error(message)),
            };
            let method = trimmed(&query, "payment_method");
            match state.backend.overview(start, end, user_id, method).await {
                Ok(data) => api_success(json!(data)),
                Err(error) => api_error(&error.0),
            }
        }
        Err(message) => bad_request(message),
    };
    with_auth_version(response)
}

async fn list_users(
    State(state): State<FinanceState>,
    headers: HeaderMap,
    RawQuery(raw): RawQuery,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let query = parse_query(raw.as_deref());
    let response = match parse_dashboard_range(&query) {
        Ok((start, end)) => match state.backend.overview(start, end, 0, "").await {
            Ok(view) => api_success(json!({
                "range": view.range,
                "users": view.users,
                "user_metrics_complete": view.user_metrics_complete,
                "user_metrics_truncated": view.user_metrics_truncated,
                "user_metrics_limit": view.user_metrics_limit,
            })),
            Err(error) => api_error(&error.0),
        },
        Err(message) => bad_request(message),
    };
    with_auth_version(response)
}

async fn get_user(
    State(state): State<FinanceState>,
    headers: HeaderMap,
    Path(user_id): Path<String>,
    RawQuery(raw): RawQuery,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let user_id = match user_id.parse::<i64>() {
        Ok(id) if id > 0 => id,
        _ => return with_auth_version(bad_request("invalid user_id")),
    };
    let query = parse_query(raw.as_deref());
    let response = match parse_dashboard_range(&query) {
        Ok((start, end)) => {
            let method = trimmed(&query, "payment_method");
            match state.backend.overview(start, end, user_id, method).await {
                Ok(data) => api_success(json!(data)),
                Err(error) => api_error(&error.0),
            }
        }
        Err(message) => bad_request(message),
    };
    with_auth_version(response)
}

async fn list_entries(
    State(state): State<FinanceState>,
    headers: HeaderMap,
    RawQuery(raw): RawQuery,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let query = parse_query(raw.as_deref());
    let limit = query
        .get("limit")
        .and_then(|v| v.parse::<i64>().ok())
        .map_or(50, std::convert::identity)
        .clamp(1, MAX_ENTRIES);
    let (before_occurred_at, before_id) = match parse_entry_cursor(&query) {
        Ok(cursor) => cursor,
        Err(message) => return with_auth_version(bad_request(message)),
    };
    let (start, end) = match parse_dashboard_range(&query) {
        Ok(range) => range,
        Err(message) => return with_auth_version(bad_request(message)),
    };
    let user_id = match parse_optional_user_id(&query) {
        Ok(id) => id,
        Err(message) => return with_auth_version(bad_request(message)),
    };
    let method = trimmed(&query, "payment_method");
    if method.len() > 64 {
        return with_auth_version(bad_request("invalid payment_method"));
    }
    let entry_type = trimmed(&query, "entry_type");
    let response = match state
        .backend
        .list_entries(
            start,
            end,
            user_id,
            method,
            entry_type,
            before_occurred_at,
            before_id,
            limit,
        )
        .await
    {
        Ok((entries, has_more, next)) => {
            let mut page = json!({
                "scope": "append_only_ledger",
                "range": {"start": start, "end": end},
                "entries": entries,
                "has_more": has_more,
            });
            if let Some((occurred_at, id)) = next {
                page["next_before_occurred_at"] = json!(occurred_at);
                page["next_before_id"] = json!(id);
            }
            api_success(page)
        }
        Err(error) => api_error(&error.0),
    };
    with_auth_version(response)
}

async fn create_entry(
    State(state): State<FinanceState>,
    headers: HeaderMap,
    request: axum::extract::Request,
) -> Response {
    let actor = match authenticated_admin(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let body = match to_bytes(request.into_body(), MAX_BODY_BYTES).await {
        Ok(bytes) => bytes,
        Err(_) => {
            return with_auth_version(
                (
                    StatusCode::BAD_REQUEST,
                    Json(json!({"success": false, "message": "invalid finance entry"})),
                )
                    .into_response(),
            );
        }
    };
    let input: CreateEntryInput = match serde_json::from_slice(&body) {
        Ok(input) => input,
        Err(_) => {
            return with_auth_version(
                (
                    StatusCode::BAD_REQUEST,
                    Json(json!({"success": false, "message": "invalid finance entry"})),
                )
                    .into_response(),
            );
        }
    };
    if input.entry_type.trim() != FINANCE_ENTRY_EXPENSE {
        return with_auth_version(
            (
                StatusCode::UNPROCESSABLE_ENTITY,
                Json(json!({"success": false, "message": "manual entries must be expenses"})),
            )
                .into_response(),
        );
    }
    let response = match state.backend.create_entry(actor.id, input).await {
        Ok(entry) => api_success(json!({"entry": entry})),
        Err(error) => legacy_json(
            StatusCode::UNPROCESSABLE_ENTITY,
            json!({"success": false, "message": error.0}),
        ),
    };
    with_auth_version(response)
}

async fn reverse_entry(
    State(state): State<FinanceState>,
    headers: HeaderMap,
    Path(entry_id): Path<String>,
) -> Response {
    let actor = match authenticated_admin(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let entry_id = match entry_id.parse::<i64>() {
        Ok(id) if id > 0 => id,
        _ => {
            return with_auth_version(
                (
                    StatusCode::BAD_REQUEST,
                    Json(json!({"success": false, "message": "invalid entry_id"})),
                )
                    .into_response(),
            );
        }
    };
    let response = match state
        .backend
        .reverse_entry(entry_id, actor.id, unix_timestamp())
        .await
    {
        Ok(entry) => api_success(json!({"entry": entry})),
        Err(error) => {
            let status = if error.0.contains("not found") {
                StatusCode::NOT_FOUND
            } else {
                StatusCode::UNPROCESSABLE_ENTITY
            };
            legacy_json(status, json!({"success": false, "message": error.0}))
        }
    };
    with_auth_version(response)
}

async fn list_payment_methods(State(state): State<FinanceState>, headers: HeaderMap) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let response = match state.backend.list_payment_methods().await {
        Ok(methods) => api_success(json!({"methods": methods})),
        Err(error) => api_error(&error.0),
    };
    with_auth_version(response)
}

async fn update_payment_method(
    State(state): State<FinanceState>,
    headers: HeaderMap,
    Path(method): Path<String>,
    request: axum::extract::Request,
) -> Response {
    let actor = match authenticated_admin(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let method = method.trim();
    if method.is_empty() || method.len() > 64 {
        return with_auth_version(
            (
                StatusCode::BAD_REQUEST,
                Json(json!({"success": false, "message": "invalid payment method"})),
            )
                .into_response(),
        );
    }
    let body = match to_bytes(request.into_body(), MAX_BODY_BYTES).await {
        Ok(bytes) => bytes,
        Err(_) => {
            return with_auth_version(
                (
                    StatusCode::BAD_REQUEST,
                    Json(json!({"success": false, "message": "invalid payment method settings"})),
                )
                    .into_response(),
            );
        }
    };
    let input: PaymentMethodInput = match serde_json::from_slice(&body) {
        Ok(input) => input,
        Err(_) => {
            return with_auth_version(
                (
                    StatusCode::BAD_REQUEST,
                    Json(json!({"success": false, "message": "invalid payment method settings"})),
                )
                    .into_response(),
            );
        }
    };
    let response = match state
        .backend
        .update_payment_method(method, actor.id, input)
        .await
    {
        Ok(config) => api_success(json!(config)),
        Err(error) => api_error(&error.0),
    };
    with_auth_version(response)
}

// --- auth / HTTP helpers (mirror finance_export) ---

async fn authenticated_admin(
    state: &FinanceState,
    headers: &HeaderMap,
) -> Result<DashboardUserView, Response> {
    let Some(credential) = crate::migration_routes::legacy_http::dashboard_credential(headers)
    else {
        return Err(
            crate::migration_routes::legacy_http::localized_dashboard_auth_error(headers, None),
        );
    };
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(credential))
        .await
        .map_err(|error| {
            crate::migration_routes::legacy_http::localized_dashboard_auth_error(
                headers,
                Some(error.kind),
            )
        })?;
    if !user.developer_access_granted {
        return Err(console_not_found());
    }
    enforce_user_auth_view(&user).map_err(|error| {
        crate::migration_routes::legacy_http::localized_user_policy_error(headers, error)
    })?;
    if user.role < ADMIN_ROLE {
        return Err(
            crate::migration_routes::legacy_http::localized_user_policy_error(
                headers,
                UserAuthPolicyError::InsufficientPrivilege,
            ),
        );
    }
    Ok(user)
}

fn parse_dashboard_range(query: &HashMap<String, String>) -> Result<(i64, i64), &'static str> {
    let now = unix_timestamp();
    let mut start = now - DEFAULT_WINDOW_SECONDS;
    let mut end = now;
    for (key, target) in [("start_timestamp", &mut start), ("end_timestamp", &mut end)] {
        if let Some(value) = query.get(key) {
            let value = value.trim();
            if value.is_empty() {
                continue;
            }
            let parsed = value
                .parse::<i64>()
                .ok()
                .filter(|v| *v > 0)
                .ok_or(match key {
                    "start_timestamp" => "invalid start_timestamp",
                    _ => "invalid end_timestamp",
                })?;
            *target = parsed;
        }
    }
    if start >= end || end - start > MAX_WINDOW_SECONDS {
        return Err("invalid finance dashboard range");
    }
    Ok((start, end))
}

fn parse_optional_user_id(query: &HashMap<String, String>) -> Result<i64, &'static str> {
    let value = trimmed(query, "user_id");
    if value.is_empty() {
        return Ok(0);
    }
    value
        .parse::<i64>()
        .ok()
        .filter(|id| *id > 0)
        .ok_or("invalid user_id")
}

fn parse_entry_cursor(query: &HashMap<String, String>) -> Result<(i64, i64), &'static str> {
    let raw_occurred = trimmed(query, "before_occurred_at");
    let raw_id = trimmed(query, "before_id");
    if raw_occurred.is_empty() && raw_id.is_empty() {
        return Ok((0, 0));
    }
    if raw_occurred.is_empty() || raw_id.is_empty() {
        return Err("before_occurred_at and before_id must be provided together");
    }
    let occurred_at = raw_occurred
        .parse::<i64>()
        .ok()
        .filter(|v| *v > 0)
        .ok_or("before_occurred_at must be a positive integer")?;
    let id = raw_id
        .parse::<i64>()
        .ok()
        .filter(|v| *v > 0)
        .ok_or("before_id must be a positive integer")?;
    Ok((occurred_at, id))
}

fn parse_query(raw: Option<&str>) -> HashMap<String, String> {
    let mut query = HashMap::new();
    let raw = raw.map_or("", std::convert::identity);
    for (key, value) in form_urlencoded::parse(raw.as_bytes()) {
        query
            .entry(key.into_owned())
            .or_insert_with(|| value.into_owned());
    }
    query
}

fn trimmed<'a>(query: &'a HashMap<String, String>, key: &str) -> &'a str {
    query.get(key).map_or("", |value| value.trim())
}

fn unix_timestamp() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| {
            i64::try_from(duration.as_secs()).map_or(i64::MAX, std::convert::identity)
        })
}

fn api_success(data: Value) -> Response {
    legacy_json(
        StatusCode::OK,
        json!({"success": true, "message": "", "data": data}),
    )
}

fn api_error(message: &str) -> Response {
    legacy_json(
        StatusCode::OK,
        json!({"success": false, "message": message}),
    )
}

fn bad_request(message: &'static str) -> Response {
    (
        StatusCode::BAD_REQUEST,
        Json(json!({"success": false, "message": message})),
    )
        .into_response()
}

fn console_not_found() -> Response {
    legacy_json(StatusCode::NOT_FOUND, json!({"message": "Not Found"}))
}

fn legacy_json(status: StatusCode, body: Value) -> Response {
    let mut response = (status, Json(body)).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

fn with_auth_version(mut response: Response) -> Response {
    response.headers_mut().insert(
        HeaderName::from_static("auth-version"),
        HeaderValue::from_static(AUTH_VERSION),
    );
    response
}

fn db_error(error: impl std::fmt::Display) -> FinanceError {
    FinanceError(error.to_string())
}

fn row_get<'r, T>(row: &'r PgRow, column: &str) -> Result<T, FinanceError>
where
    T: sqlx::Decode<'r, sqlx::Postgres> + sqlx::Type<sqlx::Postgres>,
{
    row.try_get(column).map_err(db_error)
}

fn finance_micros_from_float(value: f64) -> i64 {
    if value <= 0.0 || value.is_nan() || value.is_infinite() {
        0
    } else {
        (value * 1_000_000.0).round() as i64
    }
}

fn normalize_payment_source(method: &str, provider: &str) -> (String, String) {
    let mut method = method.trim().to_owned();
    let mut provider = provider.trim().to_owned();
    if method.is_empty() {
        method = provider.clone();
    }
    if provider.is_empty() {
        provider = method.clone();
    }
    (method, provider)
}

fn is_financial_payment_source(method: &str, provider: &str) -> bool {
    let method = method.trim().to_ascii_lowercase();
    let provider = provider.trim().to_ascii_lowercase();
    if method == "balance" || provider == "balance" {
        return false;
    }
    if provider == "epay" && matches!(method.as_str(), "linuxdo" | "linux_do" | "linuxdo_credit") {
        return false;
    }
    const INTERNAL: [&str; 10] = [
        "gift",
        "bonus",
        "checkin",
        "invite",
        "bounty",
        "linuxdo",
        "linux_do",
        "linuxdo_credit",
        "internal",
        "admin",
    ];
    !(INTERNAL.contains(&method.as_str()) || INTERNAL.contains(&provider.as_str()))
}

fn finance_payment_method_allowed(
    method: &str,
    provider: &str,
    configs: &HashMap<String, FinancePaymentMethod>,
) -> bool {
    if !is_financial_payment_source(method, provider) {
        return false;
    }
    let config = configs.get(method).or_else(|| configs.get(provider));
    config.is_none_or(|c| c.enabled && c.include_revenue)
}

fn finance_settlement_currency_allowed(currency: &str) -> bool {
    matches!(currency.trim().to_ascii_uppercase().as_str(), "USD" | "CNY")
}

fn finance_topup_amount(settled: i64, expected: i64, money: f64) -> i64 {
    if settled > 0 {
        settled
    } else if expected > 0 {
        expected
    } else {
        finance_micros_from_float(money)
    }
}

fn finance_usage_is_countable(other: &str) -> bool {
    let Ok(value) = serde_json::from_str::<Value>(other) else {
        return true;
    };
    let Some(source) = value.get("billing_source").and_then(Value::as_str) else {
        return true;
    };
    !matches!(
        source.trim().to_ascii_lowercase().as_str(),
        "gift"
            | "bonus"
            | "checkin"
            | "invite"
            | "bounty"
            | "linuxdo"
            | "linux_do"
            | "linuxdo_credit"
            | "internal"
    )
}

fn payment_method_from_row(row: &PgRow) -> Result<FinancePaymentMethod, FinanceError> {
    Ok(FinancePaymentMethod {
        id: row_get(row, "id")?,
        method: row_get(row, "method")?,
        label: row_get(row, "label")?,
        enabled: row_get(row, "enabled")?,
        include_revenue: row_get(row, "include_revenue")?,
        created_at: row_get(row, "created_at")?,
        updated_at: row_get(row, "updated_at")?,
        created_by: row_get(row, "created_by")?,
    })
}

fn ledger_entry_from_row(row: &PgRow) -> Result<FinanceLedgerEntry, FinanceError> {
    Ok(FinanceLedgerEntry {
        id: row_get(row, "id")?,
        entry_type: row_get(row, "entry_type")?,
        category: row_get(row, "category")?,
        amount_micros: row_get(row, "amount_micros")?,
        currency: row_get(row, "currency")?,
        direction: row_get(row, "direction")?,
        payment_method: row_get(row, "payment_method")?,
        payment_provider: row_get(row, "payment_provider")?,
        user_id: row_get(row, "user_id")?,
        source_type: row_get(row, "source_type")?,
        source_id: row_get(row, "source_id")?,
        token_units: row_get(row, "token_units")?,
        note: row_get(row, "note")?,
        occurred_at: row_get(row, "occurred_at")?,
        created_at: row_get(row, "created_at")?,
        created_by: row_get(row, "created_by")?,
        reversal_of_id: row_get(row, "reversal_of_id")?,
    })
}

struct FinanceAccumulator {
    overview: FinanceOverview,
    methods: HashMap<String, FinanceMethodMetric>,
    refunds: HashMap<String, FinanceMethodMetric>,
    expenses: HashMap<String, FinanceMethodMetric>,
    daily: BTreeMap<String, FinanceDailyMetric>,
    users: HashMap<i64, FinanceUserMetric>,
    method_users: HashMap<String, HashMap<i64, ()>>,
    settlement_revenue: BTreeMap<String, FinanceCurrencyMetric>,
    method_user_pairs: i64,
}

impl FinanceAccumulator {
    fn new(start: i64, end: i64, payment_methods: Vec<FinancePaymentMethod>) -> Self {
        Self {
            overview: FinanceOverview {
                range: FinanceRange { start, end },
                currency: FINANCE_CURRENCY_USD.to_owned(),
                cost_attribution: "complete".to_owned(),
                payment_methods,
                settlement_revenue_by_currency: Vec::new(),
                unclassified_settlement_orders: 0,
                sources_bounded: true,
                user_metrics_complete: true,
                user_metrics_limit: MAX_USER_METRICS as i64,
                method_user_metrics_complete: true,
                method_user_metrics_limit: MAX_METHOD_USER_PAIRS,
                revenue_by_method: Vec::new(),
                refund_by_method: Vec::new(),
                expense_by_method: Vec::new(),
                tokens: FinanceTokenMetric::default(),
                daily: Vec::new(),
                users: Vec::new(),
                revenue_micros: 0,
                refund_micros: 0,
                net_revenue_micros: 0,
                expense_micros: 0,
                profit_micros: 0,
                user_metrics_truncated: false,
            },
            methods: HashMap::new(),
            refunds: HashMap::new(),
            expenses: HashMap::new(),
            daily: BTreeMap::new(),
            users: HashMap::new(),
            method_users: HashMap::new(),
            settlement_revenue: BTreeMap::new(),
            method_user_pairs: 0,
        }
    }

    fn daily_metric(&mut self, timestamp: i64) -> &mut FinanceDailyMetric {
        let key = chrono::DateTime::<chrono::Utc>::from_timestamp(timestamp, 0).map_or_else(
            || "1970-01-01".to_owned(),
            |dt| dt.format("%Y-%m-%d").to_string(),
        );
        self.daily
            .entry(key.clone())
            .or_insert_with(|| FinanceDailyMetric {
                date: key,
                revenue_micros: 0,
                refund_micros: 0,
                expense_micros: 0,
                profit_micros: 0,
                token_units: 0,
                requests: 0,
            })
    }

    fn user_metric(&mut self, user_id: i64) -> Option<&mut FinanceUserMetric> {
        if user_id <= 0 {
            return None;
        }
        if !self.users.contains_key(&user_id) && self.users.len() >= MAX_USER_METRICS {
            self.overview.user_metrics_complete = false;
            return None;
        }
        Some(
            self.users
                .entry(user_id)
                .or_insert_with(|| FinanceUserMetric {
                    user_id,
                    ..FinanceUserMetric::default()
                }),
        )
    }

    fn add_method_user(&mut self, key: &str, user_id: i64) {
        if user_id <= 0 {
            return;
        }
        if self
            .method_users
            .get(key)
            .is_some_and(|users| users.contains_key(&user_id))
        {
            return;
        }
        if self.method_user_pairs >= MAX_METHOD_USER_PAIRS {
            self.overview.method_user_metrics_complete = false;
            return;
        }
        self.method_users
            .entry(key.to_owned())
            .or_default()
            .insert(user_id, ());
        self.method_user_pairs += 1;
    }

    fn add_revenue(
        &mut self,
        method: &str,
        provider: &str,
        amount: i64,
        timestamp: i64,
        user_id: i64,
    ) {
        if amount <= 0 {
            return;
        }
        let key = format!("{method}\x00{provider}");
        let metric = self
            .methods
            .entry(key.clone())
            .or_insert_with(|| FinanceMethodMetric {
                method: method.to_owned(),
                provider: provider.to_owned(),
                ..FinanceMethodMetric::default()
            });
        metric.amount_micros += amount;
        metric.orders += 1;
        if user_id > 0 {
            self.add_method_user(&key, user_id);
            if let Some(user) = self.user_metric(user_id) {
                user.revenue_micros += amount;
            }
        }
        self.overview.revenue_micros += amount;
        self.daily_metric(timestamp).revenue_micros += amount;
    }

    fn add_settlement_revenue(
        &mut self,
        currency: &str,
        method: &str,
        provider: &str,
        amount: i64,
        timestamp: i64,
        user_id: i64,
    ) {
        let currency = currency.trim().to_ascii_uppercase();
        if amount <= 0 || !finance_settlement_currency_allowed(&currency) {
            self.overview.unclassified_settlement_orders += 1;
            return;
        }
        let metric = self
            .settlement_revenue
            .entry(currency.clone())
            .or_insert_with(|| FinanceCurrencyMetric {
                currency: currency.clone(),
                ..FinanceCurrencyMetric::default()
            });
        metric.amount_micros += amount;
        metric.orders += 1;
        if currency == FINANCE_CURRENCY_USD {
            self.add_revenue(method, provider, amount, timestamp, user_id);
        }
    }

    fn add_expense_delta(
        &mut self,
        category: &str,
        method: &str,
        provider: &str,
        amount: i64,
        timestamp: i64,
        user_id: i64,
    ) {
        if amount == 0 {
            return;
        }
        let key = format!("{category}\x00{method}\x00{provider}");
        let metric = self
            .expenses
            .entry(key)
            .or_insert_with(|| FinanceMethodMetric {
                method: method.to_owned(),
                provider: provider.to_owned(),
                ..FinanceMethodMetric::default()
            });
        metric.category = category.to_owned();
        metric.amount_micros += amount;
        if user_id > 0
            && let Some(user) = self.user_metric(user_id)
        {
            user.expense_micros += amount;
        }
        self.overview.expense_micros += amount;
        self.daily_metric(timestamp).expense_micros += amount;
    }

    fn add_refund(
        &mut self,
        method: &str,
        provider: &str,
        amount: i64,
        timestamp: i64,
        user_id: i64,
    ) {
        if amount <= 0 {
            return;
        }
        let key = format!("{method}\x00{provider}");
        let metric = self
            .refunds
            .entry(key)
            .or_insert_with(|| FinanceMethodMetric {
                method: method.to_owned(),
                provider: provider.to_owned(),
                category: "refund".to_owned(),
                ..FinanceMethodMetric::default()
            });
        metric.amount_micros += amount;
        metric.orders += 1;
        self.overview.refund_micros += amount;
        self.daily_metric(timestamp).refund_micros += amount;
        if user_id > 0
            && let Some(user) = self.user_metric(user_id)
        {
            user.refund_micros += amount;
        }
    }

    fn add_usage(
        &mut self,
        user_id: i64,
        timestamp: i64,
        prompt: i64,
        completion: i64,
        estimated_cost: i64,
        priced: bool,
    ) {
        let prompt = prompt.max(0);
        let completion = completion.max(0);
        let total = prompt + completion;
        self.overview.tokens.prompt_tokens += prompt;
        self.overview.tokens.completion_tokens += completion;
        self.overview.tokens.total_tokens += total;
        self.overview.tokens.requests += 1;
        self.overview.tokens.estimated_cost_micros += estimated_cost;
        if !priced {
            self.overview.tokens.unpriced_requests += 1;
        }
        let daily = self.daily_metric(timestamp);
        daily.token_units += total;
        daily.requests += 1;
        if let Some(user) = self.user_metric(user_id) {
            user.token_units += total;
            user.requests += 1;
            user.token_cost_micros += estimated_cost;
        }
        if estimated_cost > 0 {
            self.add_expense_delta("token_cost", "", "", estimated_cost, timestamp, user_id);
        }
    }

    fn finish(mut self, start: i64, end: i64) -> FinanceOverview {
        self.overview.settlement_revenue_by_currency =
            self.settlement_revenue.values().cloned().collect();
        self.overview
            .settlement_revenue_by_currency
            .sort_by(|left, right| left.currency.cmp(&right.currency));
        for (key, metric) in &self.methods {
            let mut copy = metric.clone();
            copy.users = self.method_users.get(key).map_or(0, |m| m.len() as i64);
            self.overview.revenue_by_method.push(copy);
        }
        for metric in self.expenses.values() {
            self.overview.expense_by_method.push(metric.clone());
        }
        for metric in self.refunds.values() {
            self.overview.refund_by_method.push(metric.clone());
        }
        let mut day = chrono::DateTime::<chrono::Utc>::from_timestamp(start, 0)
            .map_or_else(chrono::Utc::now, |dt| dt)
            .date_naive()
            .and_hms_opt(0, 0, 0)
            .map(|dt| dt.and_utc().timestamp())
            .map_or(start, std::convert::identity);
        while day < end {
            self.daily_metric(day);
            day += 24 * 60 * 60;
        }
        for metric in self.daily.values_mut() {
            metric.profit_micros =
                metric.revenue_micros - metric.refund_micros - metric.expense_micros;
            self.overview.daily.push(metric.clone());
        }
        for metric in self.users.values() {
            self.overview.users.push(metric.clone());
        }
        self.overview
            .revenue_by_method
            .sort_by_key(|m| std::cmp::Reverse(m.amount_micros));
        self.overview
            .expense_by_method
            .sort_by_key(|m| std::cmp::Reverse(m.amount_micros));
        self.overview
            .refund_by_method
            .sort_by_key(|m| std::cmp::Reverse(m.amount_micros));
        self.overview.daily.sort_by(|a, b| a.date.cmp(&b.date));
        self.overview.users.sort_by(|left, right| {
            let left_activity = left.expense_micros + left.revenue_micros + left.refund_micros;
            let right_activity = right.expense_micros + right.revenue_micros + right.refund_micros;
            right_activity
                .cmp(&left_activity)
                .then_with(|| left.user_id.cmp(&right.user_id))
        });
        if self.overview.users.len() > MAX_ENTRIES as usize {
            self.overview.user_metrics_truncated = true;
            self.overview.users.truncate(MAX_ENTRIES as usize);
        }
        self.overview.net_revenue_micros =
            self.overview.revenue_micros - self.overview.refund_micros;
        self.overview.profit_micros =
            self.overview.net_revenue_micros - self.overview.expense_micros;
        self.overview
    }
}

#[async_trait]
impl FinanceBackend for PgFinanceBackend {
    async fn overview(
        &self,
        start: i64,
        end: i64,
        user_filter: i64,
        method_filter: &str,
    ) -> Result<FinanceOverview, FinanceError> {
        let (methods, config_map) = load_payment_methods(&self.pg).await?;
        let mut acc = FinanceAccumulator::new(start, end, methods);
        if !method_filter.is_empty() {
            acc.overview.cost_attribution = "unavailable_for_payment_method".to_owned();
        }
        load_topups(
            &self.pg,
            start,
            end,
            user_filter,
            method_filter,
            &config_map,
            &mut acc,
        )
        .await?;
        load_subscription_orders(
            &self.pg,
            start,
            end,
            user_filter,
            method_filter,
            &config_map,
            &mut acc,
        )
        .await?;
        load_subscription_payment_events(
            &self.pg,
            start,
            end,
            user_filter,
            method_filter,
            &config_map,
            &mut acc,
        )
        .await?;
        if method_filter.is_empty() {
            load_usage_logs(&self.pg, start, end, user_filter, &mut acc).await?;
        }
        load_ledger_entries(
            &self.pg,
            start,
            end,
            user_filter,
            method_filter,
            &config_map,
            &mut acc,
        )
        .await?;
        let mut overview = acc.finish(start, end);
        attach_user_labels(&self.pg, &mut overview).await?;
        Ok(overview)
    }

    async fn list_entries(
        &self,
        start: i64,
        end: i64,
        user_id: i64,
        method: &str,
        entry_type: &str,
        before_occurred_at: i64,
        before_id: i64,
        limit: i64,
    ) -> Result<(Vec<FinanceLedgerEntry>, bool, Option<(i64, i64)>), FinanceError> {
        let mut sql = String::from(
            "SELECT id, entry_type, category, amount_micros, currency, direction, \
             payment_method, payment_provider, user_id, source_type, source_id, token_units, \
             note, occurred_at, created_at, created_by, reversal_of_id \
             FROM finance_ledger_entries WHERE occurred_at >= $1 AND occurred_at < $2",
        );
        let mut bind_idx = 3;
        let mut binds: Vec<String> = Vec::new();
        if before_occurred_at > 0 {
            sql.push_str(&format!(
                " AND (occurred_at < ${bind_idx} OR (occurred_at = ${bind_idx} AND id < ${}))",
                bind_idx + 1
            ));
            bind_idx += 2;
        }
        if user_id > 0 {
            sql.push_str(&format!(" AND user_id = ${bind_idx}"));
            bind_idx += 1;
        }
        if !method.is_empty() {
            sql.push_str(&format!(
                " AND COALESCE(NULLIF(TRIM(payment_method), ''), NULLIF(TRIM(payment_provider), '')) = ${bind_idx}"
            ));
            binds.push(method.to_owned());
            bind_idx += 1;
        }
        if !entry_type.is_empty() {
            sql.push_str(&format!(" AND entry_type = ${bind_idx}"));
            bind_idx += 1;
        }
        sql.push_str(&format!(
            " ORDER BY occurred_at DESC, id DESC LIMIT ${bind_idx}"
        ));

        let mut query = sqlx::query(&sql).bind(start).bind(end);
        if before_occurred_at > 0 {
            query = query.bind(before_occurred_at).bind(before_id);
        }
        if user_id > 0 {
            query = query.bind(user_id);
        }
        for value in &binds {
            query = query.bind(value);
        }
        if !entry_type.is_empty() {
            query = query.bind(entry_type);
        }
        query = query.bind(limit + 1);

        let rows = query.fetch_all(&self.pg).await.map_err(db_error)?;
        let has_more = rows.len() as i64 > limit;
        let rows = if has_more {
            &rows[..limit as usize]
        } else {
            &rows[..]
        };
        let entries = rows
            .iter()
            .map(ledger_entry_from_row)
            .collect::<Result<Vec<_>, _>>()?;
        let next = if has_more {
            let last = entries.last().ok_or_else(|| {
                FinanceError("finance ledger pagination produced no cursor row".to_owned())
            })?;
            Some((last.occurred_at, last.id))
        } else {
            None
        };
        Ok((entries, has_more, next))
    }

    async fn create_entry(
        &self,
        actor_id: i64,
        input: CreateEntryInput,
    ) -> Result<FinanceLedgerEntry, FinanceError> {
        let occurred_at = if input.occurred_at == 0 {
            unix_timestamp()
        } else {
            input.occurred_at
        };
        let idempotency_key = if input.idempotency_key.trim().is_empty() {
            format!("finance:auto:{}", uuid::Uuid::new_v4())
        } else {
            input.idempotency_key.trim().to_owned()
        };
        if let Some(existing) = sqlx::query(
            "SELECT id, entry_type, category, amount_micros, currency, direction, \
             payment_method, payment_provider, user_id, source_type, source_id, token_units, \
             note, occurred_at, created_at, created_by, reversal_of_id \
             FROM finance_ledger_entries WHERE idempotency_key = $1",
        )
        .bind(&idempotency_key)
        .fetch_optional(&self.pg)
        .await
        .map_err(db_error)?
        {
            return ledger_entry_from_row(&existing);
        }
        let now = unix_timestamp();
        let row = sqlx::query(
            "INSERT INTO finance_ledger_entries \
             (entry_type, category, amount_micros, currency, direction, payment_method, \
              payment_provider, user_id, source_type, source_id, note, occurred_at, created_at, \
              created_by, idempotency_key) \
             VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, '', $10, $11, $12, $13, $14) \
             RETURNING id, entry_type, category, amount_micros, currency, direction, \
             payment_method, payment_provider, user_id, source_type, source_id, token_units, \
             note, occurred_at, created_at, created_by, reversal_of_id",
        )
        .bind(FINANCE_ENTRY_EXPENSE)
        .bind(input.category.trim())
        .bind(input.amount_micros)
        .bind(if input.currency.trim().is_empty() {
            FINANCE_CURRENCY_USD
        } else {
            input.currency.trim()
        })
        .bind(FINANCE_DIRECTION_DEBIT)
        .bind(input.payment_method.trim())
        .bind(input.payment_provider.trim())
        .bind(input.user_id)
        .bind(FINANCE_SOURCE_MANUAL)
        .bind(input.note.trim())
        .bind(occurred_at)
        .bind(now)
        .bind(actor_id)
        .bind(&idempotency_key)
        .fetch_one(&self.pg)
        .await
        .map_err(|error| FinanceError(error.to_string()))?;
        ledger_entry_from_row(&row)
    }

    async fn reverse_entry(
        &self,
        entry_id: i64,
        actor_id: i64,
        now: i64,
    ) -> Result<FinanceLedgerEntry, FinanceError> {
        let mut tx = self.pg.begin().await.map_err(db_error)?;
        let original = sqlx::query(
            "SELECT id, entry_type, category, amount_micros, currency, direction, \
             payment_method, payment_provider, user_id, source_type, source_id, token_units, \
             note, occurred_at, created_at, created_by, reversal_of_id \
             FROM finance_ledger_entries WHERE id = $1 FOR UPDATE",
        )
        .bind(entry_id)
        .fetch_optional(&mut *tx)
        .await
        .map_err(db_error)?
        .ok_or_else(|| FinanceError("finance ledger entry not found".to_owned()))?;
        let existing: i64 = sqlx::query_scalar(
            "SELECT COUNT(*)::BIGINT FROM finance_ledger_entries WHERE reversal_of_id = $1",
        )
        .bind(entry_id)
        .fetch_one(&mut *tx)
        .await
        .map_err(db_error)?;
        if existing > 0 {
            return Err(FinanceError(
                "finance ledger entry has already been reversed".to_owned(),
            ));
        }
        let entry_type: String = row_get(&original, "entry_type")?;
        let category: String = row_get(&original, "category")?;
        let amount_micros: i64 = row_get(&original, "amount_micros")?;
        let currency: String = row_get(&original, "currency")?;
        let direction: i8 = row_get(&original, "direction")?;
        let payment_method: String = row_get(&original, "payment_method")?;
        let payment_provider: String = row_get(&original, "payment_provider")?;
        let user_id: Option<i64> = row_get(&original, "user_id")?;
        let token_units: i64 = row_get(&original, "token_units")?;
        let idempotency_key = format!("finance:reversal:{entry_id}");
        let row = sqlx::query(
            "INSERT INTO finance_ledger_entries \
             (entry_type, category, amount_micros, currency, direction, payment_method, \
              payment_provider, user_id, source_type, source_id, token_units, note, occurred_at, \
              created_at, created_by, reversal_of_id, idempotency_key) \
             VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17) \
             RETURNING id, entry_type, category, amount_micros, currency, direction, \
             payment_method, payment_provider, user_id, source_type, source_id, token_units, \
             note, occurred_at, created_at, created_by, reversal_of_id",
        )
        .bind(entry_type)
        .bind(category)
        .bind(amount_micros)
        .bind(currency)
        .bind(-direction)
        .bind(payment_method)
        .bind(payment_provider)
        .bind(user_id)
        .bind(FINANCE_SOURCE_MANUAL)
        .bind(format!("reversal:{entry_id}"))
        .bind(token_units)
        .bind(format!("Reversal of ledger entry {entry_id}"))
        .bind(now)
        .bind(now)
        .bind(actor_id)
        .bind(entry_id)
        .bind(idempotency_key)
        .fetch_one(&mut *tx)
        .await
        .map_err(db_error)?;
        tx.commit().await.map_err(db_error)?;
        ledger_entry_from_row(&row)
    }

    async fn list_payment_methods(&self) -> Result<Vec<FinancePaymentMethod>, FinanceError> {
        let (methods, _) = load_payment_methods(&self.pg).await?;
        Ok(methods)
    }

    async fn update_payment_method(
        &self,
        method: &str,
        actor_id: i64,
        input: PaymentMethodInput,
    ) -> Result<FinancePaymentMethod, FinanceError> {
        let now = unix_timestamp();
        let existing = sqlx::query(
            "SELECT id, method, label, enabled, include_revenue, created_at, updated_at, created_by \
             FROM finance_payment_methods WHERE method = $1",
        )
        .bind(method)
        .fetch_optional(&self.pg)
        .await
        .map_err(db_error)?;
        let (id, label, enabled, include_revenue, _created_at) = if let Some(row) = existing {
            (
                row_get::<i64>(&row, "id")?,
                row_get::<String>(&row, "label")?,
                row_get::<bool>(&row, "enabled")?,
                row_get::<bool>(&row, "include_revenue")?,
                row_get::<i64>(&row, "created_at")?,
            )
        } else {
            (0, method.to_owned(), true, true, now)
        };
        let label = input
            .label
            .as_ref()
            .map(|v| v.trim().to_owned())
            .filter(|v| !v.is_empty())
            .map_or_else(|| label.clone(), std::convert::identity);
        let label = if label.is_empty() {
            method.to_owned()
        } else {
            label
        };
        let enabled = input.enabled.map_or(enabled, std::convert::identity);
        let include_revenue = input
            .include_revenue
            .map_or(include_revenue, std::convert::identity);
        let row = if id > 0 {
            sqlx::query(
                "UPDATE finance_payment_methods SET label = $1, enabled = $2, include_revenue = $3, \
                 updated_at = $4, created_by = $5 WHERE id = $6 \
                 RETURNING id, method, label, enabled, include_revenue, created_at, updated_at, created_by",
            )
            .bind(&label)
            .bind(enabled)
            .bind(include_revenue)
            .bind(now)
            .bind(actor_id)
            .bind(id)
            .fetch_one(&self.pg)
            .await
            .map_err(db_error)?
        } else {
            sqlx::query(
                "INSERT INTO finance_payment_methods (method, label, enabled, include_revenue, created_at, updated_at, created_by) \
                 VALUES ($1, $2, $3, $4, $5, $6, $7) \
                 RETURNING id, method, label, enabled, include_revenue, created_at, updated_at, created_by",
            )
            .bind(method)
            .bind(&label)
            .bind(enabled)
            .bind(include_revenue)
            .bind(now)
            .bind(now)
            .bind(actor_id)
            .fetch_one(&self.pg)
            .await
            .map_err(db_error)?
        };
        payment_method_from_row(&row)
    }
}

async fn load_payment_methods(
    pg: &PgPool,
) -> Result<
    (
        Vec<FinancePaymentMethod>,
        HashMap<String, FinancePaymentMethod>,
    ),
    FinanceError,
> {
    let rows = sqlx::query(
        "SELECT id, method, label, enabled, include_revenue, created_at, updated_at, created_by \
         FROM finance_payment_methods ORDER BY method ASC",
    )
    .fetch_all(pg)
    .await
    .map_err(db_error)?;
    let mut methods = Vec::new();
    let mut by_method = HashMap::new();
    let mut seen = HashMap::new();
    for row in rows {
        let config = payment_method_from_row(&row)?;
        seen.insert(config.method.clone(), true);
        by_method.insert(config.method.clone(), config.clone());
        methods.push(config);
    }
    let known = [
        "stripe",
        "creem",
        "epay",
        "waffo",
        "waffo_pancake",
        "balance",
    ];
    for source in ["top_ups", "subscription_orders", "finance_ledger_entries"] {
        let table = match source {
            "top_ups" => "top_ups",
            "subscription_orders" => "subscription_orders",
            _ => "finance_ledger_entries",
        };
        let observed: Vec<String> = sqlx::query_scalar(&format!(
            "SELECT DISTINCT COALESCE(NULLIF(TRIM(payment_method), ''), NULLIF(TRIM(payment_provider), '')) \
             FROM {table} WHERE payment_method <> '' OR payment_provider <> '' \
             ORDER BY 1 ASC LIMIT {}",
            MAX_PAYMENT_METHODS + 1
        ))
        .fetch_all(pg)
        .await
        .map_err(db_error)?;
        if observed.len() as i64 > MAX_PAYMENT_METHODS {
            return Err(FinanceError(format!(
                "finance payment method discovery limit exceeded: source={source}"
            )));
        }
        for method in observed {
            let method = method.trim().to_owned();
            if method.is_empty() || method == "balance" || seen.contains_key(&method) {
                continue;
            }
            seen.insert(method.clone(), true);
            if !known.contains(&method.as_str()) {
                let config = FinancePaymentMethod {
                    id: 0,
                    method: method.clone(),
                    label: method.clone(),
                    enabled: true,
                    include_revenue: true,
                    created_at: 0,
                    updated_at: 0,
                    created_by: 0,
                };
                methods.push(config.clone());
                by_method.insert(method, config);
            }
        }
    }
    methods.sort_by(|a, b| a.method.cmp(&b.method));
    Ok((methods, by_method))
}

async fn attach_user_labels(
    pg: &PgPool,
    overview: &mut FinanceOverview,
) -> Result<(), FinanceError> {
    if overview.users.is_empty() {
        return Ok(());
    }
    let ids: Vec<i64> = overview.users.iter().map(|u| u.user_id).collect();
    let rows = sqlx::query(
        "SELECT id, COALESCE(username, '') AS username, COALESCE(display_name, '') AS display_name \
         FROM users WHERE id = ANY($1)",
    )
    .bind(&ids)
    .fetch_all(pg)
    .await
    .map_err(db_error)?;
    let mut labels = HashMap::new();
    for row in rows {
        labels.insert(
            row_get::<i64>(&row, "id")?,
            (
                row_get::<String>(&row, "username")?,
                row_get::<String>(&row, "display_name")?,
            ),
        );
    }
    for user in &mut overview.users {
        if let Some((username, display_name)) = labels.get(&user.user_id) {
            user.username.clone_from(username);
            user.display_name.clone_from(display_name);
        }
    }
    Ok(())
}

async fn load_topups(
    pg: &PgPool,
    start: i64,
    end: i64,
    user_filter: i64,
    method_filter: &str,
    configs: &HashMap<String, FinancePaymentMethod>,
    acc: &mut FinanceAccumulator,
) -> Result<(), FinanceError> {
    let mut last_ts = 0i64;
    let mut last_id = 0i64;
    let mut processed = 0i64;
    loop {
        let limit = batch_limit(processed);
        if limit == 0 {
            break;
        }
        let mut query = sqlx::query(
            "SELECT id, user_id, expected_amount_micros, settled_amount_micros, settlement_currency, \
             money, payment_method, payment_provider, create_time, complete_time \
             FROM top_ups WHERE status = $1 \
             AND COALESCE(NULLIF(complete_time, 0), create_time) >= $2 \
             AND COALESCE(NULLIF(complete_time, 0), create_time) < $3 \
             AND NOT EXISTS (SELECT 1 FROM subscription_orders so WHERE so.trade_no = top_ups.trade_no AND so.status = $1) \
             AND (COALESCE(NULLIF(complete_time, 0), create_time) > $4 OR (COALESCE(NULLIF(complete_time, 0), create_time) = $4 AND id > $5)) \
             ORDER BY COALESCE(NULLIF(complete_time, 0), create_time) ASC, id ASC LIMIT $6",
        )
        .bind(TOPUP_STATUS_SUCCESS)
        .bind(start)
        .bind(end)
        .bind(last_ts)
        .bind(last_id)
        .bind(limit);
        if user_filter > 0 {
            query = sqlx::query(
                "SELECT id, user_id, expected_amount_micros, settled_amount_micros, settlement_currency, \
                 money, payment_method, payment_provider, create_time, complete_time \
                 FROM top_ups WHERE status = $1 AND user_id = $7 \
                 AND COALESCE(NULLIF(complete_time, 0), create_time) >= $2 \
                 AND COALESCE(NULLIF(complete_time, 0), create_time) < $3 \
                 AND NOT EXISTS (SELECT 1 FROM subscription_orders so WHERE so.trade_no = top_ups.trade_no AND so.status = $1) \
                 AND (COALESCE(NULLIF(complete_time, 0), create_time) > $4 OR (COALESCE(NULLIF(complete_time, 0), create_time) = $4 AND id > $5)) \
                 ORDER BY COALESCE(NULLIF(complete_time, 0), create_time) ASC, id ASC LIMIT $6",
            )
            .bind(TOPUP_STATUS_SUCCESS)
            .bind(start)
            .bind(end)
            .bind(last_ts)
            .bind(last_id)
            .bind(limit)
            .bind(user_filter);
        }
        let rows = query.fetch_all(pg).await.map_err(db_error)?;
        if rows.is_empty() {
            break;
        }
        for row in &rows {
            let (method, provider) = normalize_payment_source(
                &row_get::<String>(row, "payment_method")?,
                &row_get::<String>(row, "payment_provider")?,
            );
            let currency: String = row_get(row, "settlement_currency")?;
            if !finance_settlement_currency_allowed(&currency) {
                continue;
            }
            if !method_filter.is_empty() && method != method_filter {
                continue;
            }
            if !finance_payment_method_allowed(&method, &provider, configs) {
                continue;
            }
            let mut timestamp: i64 = row_get(row, "complete_time")?;
            if timestamp <= 0 {
                timestamp = row_get(row, "create_time")?;
            }
            let user_id: i64 = row_get(row, "user_id")?;
            let amount = finance_topup_amount(
                row_get(row, "settled_amount_micros")?,
                row_get(row, "expected_amount_micros")?,
                row_get(row, "money")?,
            );
            acc.add_settlement_revenue(&currency, &method, &provider, amount, timestamp, user_id);
        }
        processed += rows.len() as i64;
        let last = rows
            .last()
            .ok_or_else(|| FinanceError("top-up pagination returned an empty batch".to_owned()))?;
        last_ts = row_get(last, "complete_time")?;
        if last_ts <= 0 {
            last_ts = row_get(last, "create_time")?;
        }
        last_id = row_get(last, "id")?;
        if (rows.len() as i64) < limit {
            break;
        }
    }
    Ok(())
}

async fn load_subscription_orders(
    pg: &PgPool,
    start: i64,
    end: i64,
    user_filter: i64,
    method_filter: &str,
    configs: &HashMap<String, FinancePaymentMethod>,
    acc: &mut FinanceAccumulator,
) -> Result<(), FinanceError> {
    let mut last_ts = 0i64;
    let mut last_id = 0i64;
    let mut processed = 0i64;
    loop {
        let limit = batch_limit(processed);
        if limit == 0 {
            break;
        }
        let sql = if user_filter > 0 {
            "SELECT id, user_id, expected_amount_micros, settlement_currency, payment_method, payment_provider, create_time, complete_time \
             FROM subscription_orders WHERE status = $1 AND user_id = $7 \
             AND COALESCE(NULLIF(complete_time, 0), create_time) >= $2 \
             AND COALESCE(NULLIF(complete_time, 0), create_time) < $3 \
             AND NOT EXISTS (SELECT 1 FROM subscription_payment_events spe WHERE spe.subscription_order_id = subscription_orders.id) \
             AND (COALESCE(NULLIF(complete_time, 0), create_time) > $4 OR (COALESCE(NULLIF(complete_time, 0), create_time) = $4 AND id > $5)) \
             ORDER BY COALESCE(NULLIF(complete_time, 0), create_time) ASC, id ASC LIMIT $6"
        } else {
            "SELECT id, user_id, expected_amount_micros, settlement_currency, payment_method, payment_provider, create_time, complete_time \
             FROM subscription_orders WHERE status = $1 \
             AND COALESCE(NULLIF(complete_time, 0), create_time) >= $2 \
             AND COALESCE(NULLIF(complete_time, 0), create_time) < $3 \
             AND NOT EXISTS (SELECT 1 FROM subscription_payment_events spe WHERE spe.subscription_order_id = subscription_orders.id) \
             AND (COALESCE(NULLIF(complete_time, 0), create_time) > $4 OR (COALESCE(NULLIF(complete_time, 0), create_time) = $4 AND id > $5)) \
             ORDER BY COALESCE(NULLIF(complete_time, 0), create_time) ASC, id ASC LIMIT $6"
        };
        let mut query = sqlx::query(sql)
            .bind(TOPUP_STATUS_SUCCESS)
            .bind(start)
            .bind(end)
            .bind(last_ts)
            .bind(last_id)
            .bind(limit);
        if user_filter > 0 {
            query = query.bind(user_filter);
        }
        let rows = query.fetch_all(pg).await.map_err(db_error)?;
        if rows.is_empty() {
            break;
        }
        for row in &rows {
            let (method, provider) = normalize_payment_source(
                &row_get::<String>(row, "payment_method")?,
                &row_get::<String>(row, "payment_provider")?,
            );
            if !method_filter.is_empty() && method != method_filter {
                continue;
            }
            if !finance_payment_method_allowed(&method, &provider, configs) {
                continue;
            }
            let mut timestamp: i64 = row_get(row, "complete_time")?;
            if timestamp <= 0 {
                timestamp = row_get(row, "create_time")?;
            }
            let user_id: i64 = row_get(row, "user_id")?;
            let currency: String = row_get(row, "settlement_currency")?;
            let amount: i64 = row_get(row, "expected_amount_micros")?;
            acc.add_settlement_revenue(&currency, &method, &provider, amount, timestamp, user_id);
        }
        processed += rows.len() as i64;
        let last = rows.last().ok_or_else(|| {
            FinanceError("subscription pagination returned an empty batch".to_owned())
        })?;
        last_ts = row_get(last, "complete_time")?;
        if last_ts <= 0 {
            last_ts = row_get(last, "create_time")?;
        }
        last_id = row_get(last, "id")?;
        if (rows.len() as i64) < limit {
            break;
        }
    }
    Ok(())
}

async fn load_subscription_payment_events(
    pg: &PgPool,
    start: i64,
    end: i64,
    user_filter: i64,
    method_filter: &str,
    configs: &HashMap<String, FinancePaymentMethod>,
    acc: &mut FinanceAccumulator,
) -> Result<(), FinanceError> {
    let mut last_ts = 0i64;
    let mut last_id = 0i64;
    let mut processed = 0i64;
    loop {
        let limit = batch_limit(processed);
        if limit == 0 {
            break;
        }
        let sql = if user_filter > 0 {
            "SELECT spe.id, so.user_id, spe.settlement_amount_micros, spe.settlement_currency, \
             so.payment_method, spe.payment_provider, spe.created_time \
             FROM subscription_payment_events spe \
             JOIN subscription_orders so ON so.id = spe.subscription_order_id \
             WHERE so.status = $1 AND so.user_id = $7 \
             AND spe.created_time >= $2 AND spe.created_time < $3 \
             AND (spe.created_time > $4 OR (spe.created_time = $4 AND spe.id > $5)) \
             ORDER BY spe.created_time ASC, spe.id ASC LIMIT $6"
        } else {
            "SELECT spe.id, so.user_id, spe.settlement_amount_micros, spe.settlement_currency, \
             so.payment_method, spe.payment_provider, spe.created_time \
             FROM subscription_payment_events spe \
             JOIN subscription_orders so ON so.id = spe.subscription_order_id \
             WHERE so.status = $1 \
             AND spe.created_time >= $2 AND spe.created_time < $3 \
             AND (spe.created_time > $4 OR (spe.created_time = $4 AND spe.id > $5)) \
             ORDER BY spe.created_time ASC, spe.id ASC LIMIT $6"
        };
        let mut query = sqlx::query(sql)
            .bind(TOPUP_STATUS_SUCCESS)
            .bind(start)
            .bind(end)
            .bind(last_ts)
            .bind(last_id)
            .bind(limit);
        if user_filter > 0 {
            query = query.bind(user_filter);
        }
        let rows = query.fetch_all(pg).await.map_err(db_error)?;
        if rows.is_empty() {
            break;
        }
        for row in &rows {
            let (method, provider) = normalize_payment_source(
                &row_get::<String>(row, "payment_method")?,
                &row_get::<String>(row, "payment_provider")?,
            );
            if !method_filter.is_empty() && method != method_filter {
                continue;
            }
            if !finance_payment_method_allowed(&method, &provider, configs) {
                continue;
            }
            acc.add_settlement_revenue(
                &row_get::<String>(row, "settlement_currency")?,
                &method,
                &provider,
                row_get(row, "settlement_amount_micros")?,
                row_get(row, "created_time")?,
                row_get(row, "user_id")?,
            );
        }
        processed += rows.len() as i64;
        let last = rows.last().ok_or_else(|| {
            FinanceError("subscription payment pagination returned an empty batch".to_owned())
        })?;
        last_ts = row_get(last, "created_time")?;
        last_id = row_get(last, "id")?;
        if (rows.len() as i64) < limit {
            break;
        }
    }
    Ok(())
}

async fn load_usage_logs(
    pg: &PgPool,
    start: i64,
    end: i64,
    user_filter: i64,
    acc: &mut FinanceAccumulator,
) -> Result<(), FinanceError> {
    let mut last_ts = 0i64;
    let mut last_request_id = String::new();
    let mut processed = 0i64;
    loop {
        let limit = batch_limit(processed);
        if limit == 0 {
            break;
        }
        let sql = if user_filter > 0 {
            "SELECT user_id, created_at, request_id, prompt_tokens, completion_tokens, quota, other \
             FROM logs WHERE type = $1 AND user_id = $7 AND created_at >= $2 AND created_at < $3 \
             AND (created_at > $4 OR (created_at = $4 AND request_id > $5)) \
             ORDER BY created_at ASC, request_id ASC LIMIT $6"
        } else {
            "SELECT user_id, created_at, request_id, prompt_tokens, completion_tokens, quota, other \
             FROM logs WHERE type = $1 AND created_at >= $2 AND created_at < $3 \
             AND (created_at > $4 OR (created_at = $4 AND request_id > $5)) \
             ORDER BY created_at ASC, request_id ASC LIMIT $6"
        };
        let mut query = sqlx::query(sql)
            .bind(LOG_TYPE_CONSUME)
            .bind(start)
            .bind(end)
            .bind(last_ts)
            .bind(&last_request_id)
            .bind(limit);
        if user_filter > 0 {
            query = query.bind(user_filter);
        }
        let rows = query.fetch_all(pg).await.map_err(db_error)?;
        if rows.is_empty() {
            break;
        }
        for row in &rows {
            let other: String = row_get(row, "other")?;
            if !finance_usage_is_countable(&other) {
                continue;
            }
            let mut priced = false;
            let mut price = 0.0f64;
            if let Ok(value) = serde_json::from_str::<Value>(&other)
                && let Some(raw) = value.get("model_price").and_then(Value::as_f64)
                && raw > 0.0
            {
                price = raw;
                priced = true;
            }
            let prompt: i64 = row_get(row, "prompt_tokens")?;
            let completion: i64 = row_get(row, "completion_tokens")?;
            let total = (prompt + completion).max(0);
            let mut cost = (price * total as f64).round() as i64;
            let quota: i64 = row_get(row, "quota")?;
            if priced && total == 0 && quota > 0 {
                cost = finance_micros_from_float(price);
            }
            let user_id: i64 = row_get(row, "user_id")?;
            let created_at: i64 = row_get(row, "created_at")?;
            acc.add_usage(user_id, created_at, prompt, completion, cost, priced);
        }
        processed += rows.len() as i64;
        let last = rows
            .last()
            .ok_or_else(|| FinanceError("usage pagination returned an empty batch".to_owned()))?;
        last_ts = row_get(last, "created_at")?;
        last_request_id = row_get(last, "request_id")?;
        if (rows.len() as i64) < limit {
            break;
        }
    }
    Ok(())
}

async fn load_ledger_entries(
    pg: &PgPool,
    start: i64,
    end: i64,
    user_filter: i64,
    method_filter: &str,
    configs: &HashMap<String, FinancePaymentMethod>,
    acc: &mut FinanceAccumulator,
) -> Result<(), FinanceError> {
    let mut last_ts = 0i64;
    let mut last_id = 0i64;
    let mut processed = 0i64;
    loop {
        let limit = batch_limit(processed);
        if limit == 0 {
            break;
        }
        let sql = if user_filter > 0 && !method_filter.is_empty() {
            "SELECT id, entry_type, category, amount_micros, direction, payment_method, payment_provider, user_id, occurred_at \
             FROM finance_ledger_entries WHERE occurred_at >= $1 AND occurred_at < $2 AND user_id = $7 \
             AND UPPER(TRIM(currency)) = 'USD' \
             AND COALESCE(NULLIF(TRIM(payment_method), ''), NULLIF(TRIM(payment_provider), '')) = $8 \
             AND (occurred_at > $3 OR (occurred_at = $3 AND id > $4)) \
             ORDER BY occurred_at ASC, id ASC LIMIT $5"
        } else if user_filter > 0 {
            "SELECT id, entry_type, category, amount_micros, direction, payment_method, payment_provider, user_id, occurred_at \
             FROM finance_ledger_entries WHERE occurred_at >= $1 AND occurred_at < $2 AND user_id = $7 \
             AND UPPER(TRIM(currency)) = 'USD' \
             AND (occurred_at > $3 OR (occurred_at = $3 AND id > $4)) \
             ORDER BY occurred_at ASC, id ASC LIMIT $5"
        } else if !method_filter.is_empty() {
            "SELECT id, entry_type, category, amount_micros, direction, payment_method, payment_provider, user_id, occurred_at \
             FROM finance_ledger_entries WHERE occurred_at >= $1 AND occurred_at < $2 \
             AND UPPER(TRIM(currency)) = 'USD' \
             AND COALESCE(NULLIF(TRIM(payment_method), ''), NULLIF(TRIM(payment_provider), '')) = $6 \
             AND (occurred_at > $3 OR (occurred_at = $3 AND id > $4)) \
             ORDER BY occurred_at ASC, id ASC LIMIT $5"
        } else {
            "SELECT id, entry_type, category, amount_micros, direction, payment_method, payment_provider, user_id, occurred_at \
             FROM finance_ledger_entries WHERE occurred_at >= $1 AND occurred_at < $2 \
             AND UPPER(TRIM(currency)) = 'USD' \
             AND (occurred_at > $3 OR (occurred_at = $3 AND id > $4)) \
             ORDER BY occurred_at ASC, id ASC LIMIT $5"
        };
        let mut query = sqlx::query(sql)
            .bind(start)
            .bind(end)
            .bind(last_ts)
            .bind(last_id)
            .bind(limit);
        if !method_filter.is_empty() {
            query = query.bind(method_filter);
        }
        if user_filter > 0 {
            query = query.bind(user_filter);
        }
        let rows = query.fetch_all(pg).await.map_err(db_error)?;
        if rows.is_empty() {
            break;
        }
        for row in &rows {
            let entry_type: String = row_get(row, "entry_type")?;
            let amount: i64 = row_get(row, "amount_micros")?;
            let direction: i8 = row_get(row, "direction")?;
            let (method, provider) = normalize_payment_source(
                &row_get::<String>(row, "payment_method")?,
                &row_get::<String>(row, "payment_provider")?,
            );
            let user_id: Option<i64> = row_get(row, "user_id")?;
            let user_id = user_id.map_or(0, std::convert::identity);
            let occurred_at: i64 = row_get(row, "occurred_at")?;
            let category: String = row_get(row, "category")?;
            match entry_type.as_str() {
                FINANCE_ENTRY_REVENUE => {
                    if method.is_empty() {
                        continue;
                    }
                    if direction == FINANCE_DIRECTION_CREDIT {
                        if finance_payment_method_allowed(&method, &provider, configs) {
                            acc.add_revenue(&method, &provider, amount, occurred_at, user_id);
                        }
                    } else if finance_payment_method_allowed(&method, &provider, configs) {
                        acc.add_refund(&method, &provider, amount, occurred_at, user_id);
                    }
                }
                FINANCE_ENTRY_EXPENSE | FINANCE_ENTRY_TOKEN_COST => {
                    let signed = if direction == FINANCE_DIRECTION_CREDIT {
                        -amount
                    } else {
                        amount
                    };
                    acc.add_expense_delta(
                        &category,
                        &method,
                        &provider,
                        signed,
                        occurred_at,
                        user_id,
                    );
                }
                _ => {}
            }
        }
        processed += rows.len() as i64;
        let last = rows
            .last()
            .ok_or_else(|| FinanceError("ledger pagination returned an empty batch".to_owned()))?;
        last_ts = row_get(last, "occurred_at")?;
        last_id = row_get(last, "id")?;
        if (rows.len() as i64) < limit {
            break;
        }
    }
    Ok(())
}

fn batch_limit(processed: i64) -> i64 {
    let remaining = MAX_SOURCE_ROWS - processed;
    if remaining <= 0 {
        0
    } else {
        remaining.min(BATCH_SIZE)
    }
}

#[cfg(test)]
mod unit_tests {
    use super::{FinanceAccumulator, FinanceCurrencyMetric, FinanceOverview};

    #[test]
    fn settlement_totals_keep_real_currencies_separate() {
        let mut accumulator = FinanceAccumulator::new(0, 86_400, Vec::new());
        accumulator.add_settlement_revenue("USD", "stripe", "stripe", 1_000_000, 1, 1);
        accumulator.add_settlement_revenue("CNY", "alipay", "epay", 6_800_000, 1, 2);
        accumulator.add_settlement_revenue("", "legacy", "", 9_000_000, 1, 3);

        let FinanceOverview {
            revenue_micros,
            settlement_revenue_by_currency,
            unclassified_settlement_orders,
            ..
        } = accumulator.finish(0, 86_400);
        assert_eq!(revenue_micros, 1_000_000);
        assert_eq!(unclassified_settlement_orders, 1);
        assert_eq!(
            settlement_revenue_by_currency,
            vec![
                FinanceCurrencyMetric {
                    currency: "CNY".to_owned(),
                    amount_micros: 6_800_000,
                    orders: 1,
                },
                FinanceCurrencyMetric {
                    currency: "USD".to_owned(),
                    amount_micros: 1_000_000,
                    orders: 1,
                },
            ]
        );
    }
}
