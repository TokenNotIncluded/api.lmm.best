use super::{
    BillingSubscriptionsState, ROOT_ROLE, admin, database_timestamp, evict_user_cache, failure, ok,
    with_auth_version,
};
use crate::{
    ClientIpKey,
    auth::{CriticalRateLimitOutcome, DashboardUser},
};
use axum::{
    Json, Router,
    body::{Body, to_bytes},
    extract::{Path, Query, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use chrono::{Datelike, TimeZone, Timelike, Utc};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Postgres, Row, Transaction};
use std::collections::{BTreeSet, HashMap, HashSet};
use uuid::Uuid;

const MAX_TARGETS: usize = 5_000;
const MAX_ACTIVE_SUBSCRIPTIONS: usize = 20_000;
const MAX_FILTER_IDS: usize = 100;
const MAX_QUERY_CHARACTERS: usize = 200;
const PREVIEW_LIFETIME_SECONDS: i64 = 10 * 60;
const MAX_BODY_BYTES: usize = 512 * 1024;

pub(super) fn router() -> Router<BillingSubscriptionsState> {
    Router::new()
        .route("/api/subscription/admin/records", get(admin_records))
        .route("/api/subscription/root/reset-targets", get(root_targets))
        .route("/api/subscription/root/reset/preview", post(root_preview))
        .route("/api/subscription/root/reset", post(root_execute))
        .route("/api/subscription/self/reset-vouchers", get(self_vouchers))
        .route(
            "/api/subscription/self/reset-vouchers/{id}/redeem",
            post(redeem_voucher),
        )
}

fn no_cache(mut response: Response) -> Response {
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        HeaderValue::from_static("no-store, no-cache, must-revalidate, proxy-revalidate"),
    );
    response
        .headers_mut()
        .insert(header::PRAGMA, HeaderValue::from_static("no-cache"));
    response
        .headers_mut()
        .insert(header::EXPIRES, HeaderValue::from_static("0"));
    response
}

async fn root_user(
    state: &BillingSubscriptionsState,
    headers: &HeaderMap,
) -> Result<DashboardUser, Response> {
    let user = admin(state, headers).await?;
    if user.role < ROOT_ROLE {
        return Err(with_auth_version(failure(
            StatusCode::FORBIDDEN,
            "Unauthorized, insufficient privileges",
        )));
    }
    Ok(user)
}

async fn root_write(
    state: &BillingSubscriptionsState,
    headers: &HeaderMap,
) -> Result<DashboardUser, Response> {
    let user = root_user(state, headers).await?;
    super::require_payment_compliance(state, headers)
        .await
        .map_err(with_auth_version)?;
    Ok(user)
}

fn request_ip(request: &Request) -> String {
    request
        .extensions()
        .get::<ClientIpKey>()
        .map_or_else(|| "unknown".to_owned(), |key| key.0.clone())
}

async fn critical_limit(state: &BillingSubscriptionsState, client_ip: &str) -> Option<Response> {
    match state.auth.check_critical_rate_limit(client_ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => None,
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => {
            let mut response = Response::new(Body::empty());
            *response.status_mut() = StatusCode::TOO_MANY_REQUESTS;
            if let Ok(value) = HeaderValue::from_str(&retry_after_seconds.to_string()) {
                response.headers_mut().insert(header::RETRY_AFTER, value);
            }
            Some(with_auth_version(no_cache(response)))
        }
        Err(_) => Some(with_auth_version(no_cache(failure(
            StatusCode::INTERNAL_SERVER_ERROR,
            "系统错误",
        )))),
    }
}

async fn json_body<T: for<'de> Deserialize<'de>>(request: Request) -> Result<T, Response> {
    let bytes = to_bytes(request.into_body(), MAX_BODY_BYTES)
        .await
        .map_err(|_| failure(StatusCode::BAD_REQUEST, "参数错误"))?;
    serde_json::from_slice(&bytes).map_err(|_| failure(StatusCode::BAD_REQUEST, "参数错误"))
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PageQuery {
    query: Option<String>,
    plan_id: Option<i64>,
    plan_ids: Option<String>,
    user_ids: Option<String>,
    status: Option<String>,
    page: Option<i64>,
    page_size: Option<i64>,
}

fn normalized_page(value: Option<i64>, default: i64, maximum: i64) -> i64 {
    value.unwrap_or(default).clamp(1, maximum)
}

fn parse_query_ids(value: Option<&str>) -> Result<Vec<i64>, ResetError> {
    let value = value.unwrap_or_default().trim();
    if value.is_empty() {
        return Ok(Vec::new());
    }
    let mut values = BTreeSet::new();
    for part in value.split(',') {
        let id = part
            .trim()
            .parse::<i64>()
            .map_err(|_| ResetError::Business("invalid subscription reset id filter"))?;
        if id <= 0 {
            return Err(ResetError::Business("invalid subscription reset id filter"));
        }
        values.insert(id);
        if values.len() > MAX_FILTER_IDS {
            return Err(ResetError::Business(
                "too many subscription reset id filters",
            ));
        }
    }
    Ok(values.into_iter().collect())
}

#[derive(Serialize, sqlx::FromRow)]
struct AdminRecord {
    id: i64,
    user_id: i64,
    username: String,
    email: String,
    plan_id: i64,
    plan_title: String,
    plan_archived_at: i64,
    amount_total: i64,
    amount_used: i64,
    start_time: i64,
    end_time: i64,
    status: String,
    source: String,
    last_reset_time: i64,
    next_reset_time: i64,
    allow_wallet_overflow: bool,
    created_at: i64,
    updated_at: i64,
}

#[derive(Serialize)]
struct Page<T: Serialize> {
    items: Vec<T>,
    total: i64,
    page: i64,
    page_size: i64,
}

async fn admin_records(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
    Query(query): Query<PageQuery>,
) -> Response {
    if let Err(response) = admin(&state, &headers).await {
        return no_cache(response);
    }
    let page = normalized_page(query.page, 1, 1_000_000);
    let page_size = normalized_page(query.page_size, 20, 100);
    let search = query.query.unwrap_or_default().trim().to_ascii_lowercase();
    if search.chars().count() > MAX_QUERY_CHARACTERS {
        return no_cache(with_auth_version(failure(
            StatusCode::BAD_REQUEST,
            "subscription record search filter is too long",
        )));
    }
    let like = format!("%{search}%");
    if query.plan_id.is_some_and(|id| id <= 0) {
        return no_cache(with_auth_version(failure(
            StatusCode::BAD_REQUEST,
            "invalid subscription record plan filter",
        )));
    }
    let plan_id = query.plan_id.unwrap_or_default();
    let status = query.status.unwrap_or_else(|| "all".to_owned());
    if !matches!(status.as_str(), "all" | "active" | "expired" | "cancelled") {
        return no_cache(with_auth_version(failure(
            StatusCode::BAD_REQUEST,
            "invalid subscription record status filter",
        )));
    }
    let base = " FROM user_subscriptions us JOIN users ON users.id=us.user_id AND users.deleted_at IS NULL JOIN subscription_plans plans ON plans.id=us.plan_id WHERE ($1='' OR CAST(us.user_id AS TEXT) LIKE $2 OR LOWER(users.username) LIKE $2 OR LOWER(COALESCE(users.email,'')) LIKE $2) AND ($3::BIGINT<=0 OR us.plan_id=$3) AND ($4='all' OR $4='' OR us.status=$4)";
    let total = sqlx::query_scalar::<_, i64>(&format!("SELECT COUNT(*){base}"))
        .bind(&search)
        .bind(&like)
        .bind(plan_id)
        .bind(&status)
        .fetch_one(&state.pg)
        .await;
    let rows = sqlx::query_as::<_, AdminRecord>(&format!(
        "SELECT us.id,us.user_id,users.username,COALESCE(users.email,'') email,us.plan_id,plans.title plan_title,COALESCE(plans.archived_at,0) plan_archived_at,us.amount_total,us.amount_used,us.start_time,us.end_time,COALESCE(us.status,'') status,COALESCE(us.source,'') source,COALESCE(us.last_reset_time,0) last_reset_time,COALESCE(us.next_reset_time,0) next_reset_time,COALESCE(us.allow_wallet_overflow,TRUE) allow_wallet_overflow,COALESCE(us.created_at,0) created_at,COALESCE(us.updated_at,0) updated_at{base} ORDER BY us.id DESC OFFSET $5 LIMIT $6"
    ))
    .bind(&search)
    .bind(&like)
    .bind(plan_id)
    .bind(&status)
    .bind((page - 1).saturating_mul(page_size))
    .bind(page_size)
    .fetch_all(&state.pg)
    .await;
    no_cache(with_auth_version(match (total, rows) {
        (Ok(total), Ok(items)) => ok(Page {
            items,
            total,
            page,
            page_size,
        }),
        _ => failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
    }))
}

#[derive(Clone, Debug, Serialize, sqlx::FromRow)]
struct Eligible {
    user_id: i64,
    username: String,
    email: String,
    plan_id: i64,
    plan_title: String,
    plan_archived_at: i64,
    active_subscription_count: i64,
    amount_total: i64,
    amount_used: i64,
    next_reset_time: i64,
    banked_voucher_count: i64,
}

async fn eligible_rows(
    pg: &PgPool,
    query: &str,
    plan_id: i64,
    plan_ids: &[i64],
    user_ids: &[i64],
    now: i64,
    offset: i64,
    limit: i64,
) -> Result<(i64, Vec<Eligible>), sqlx::Error> {
    let like = format!("%{}%", query.trim().to_ascii_lowercase());
    let base = " FROM user_subscriptions us JOIN users ON users.id=us.user_id AND users.deleted_at IS NULL JOIN subscription_plans plans ON plans.id=us.plan_id WHERE us.status='active' AND us.end_time>$1 AND ($2='' OR CAST(us.user_id AS TEXT) LIKE $3 OR LOWER(users.username) LIKE $3 OR LOWER(COALESCE(users.email,'')) LIKE $3) AND ($4::BIGINT<=0 OR us.plan_id=$4) AND (CARDINALITY($5::BIGINT[])=0 OR us.plan_id=ANY($5)) AND (CARDINALITY($6::BIGINT[])=0 OR us.user_id=ANY($6))";
    let total = sqlx::query_scalar::<_, i64>(&format!(
        "SELECT COUNT(*) FROM (SELECT us.user_id,us.plan_id{base} GROUP BY us.user_id,us.plan_id) eligible"
    ))
    .bind(now)
    .bind(query.trim())
    .bind(&like)
    .bind(plan_id)
    .bind(plan_ids)
    .bind(user_ids)
    .fetch_one(pg)
    .await?;
    let rows = sqlx::query_as::<_, Eligible>(&format!(
        "SELECT us.user_id,users.username,COALESCE(users.email,'') email,us.plan_id,plans.title plan_title,COALESCE(plans.archived_at,0) plan_archived_at,COUNT(*)::BIGINT active_subscription_count,COALESCE(SUM(us.amount_total),0)::BIGINT amount_total,COALESCE(SUM(us.amount_used),0)::BIGINT amount_used,COALESCE(MIN(NULLIF(us.next_reset_time,0)),0)::BIGINT next_reset_time,(SELECT COUNT(*) FROM subscription_reset_vouchers v WHERE v.user_id=us.user_id AND v.plan_id=us.plan_id AND v.status='available' AND v.expires_at>$1)::BIGINT banked_voucher_count{base} GROUP BY us.user_id,users.username,users.email,us.plan_id,plans.title,plans.archived_at ORDER BY us.user_id DESC,us.plan_id DESC OFFSET $7 LIMIT $8"
    ))
    .bind(now)
    .bind(query.trim())
    .bind(&like)
    .bind(plan_id)
    .bind(plan_ids)
    .bind(user_ids)
    .bind(offset)
    .bind(limit)
    .fetch_all(pg)
    .await?;
    Ok((total, rows))
}

async fn root_targets(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
    Query(query): Query<PageQuery>,
) -> Response {
    if let Err(response) = root_user(&state, &headers).await {
        return no_cache(response);
    }
    let page = normalized_page(query.page, 1, 1_000_000);
    let page_size = normalized_page(query.page_size, 20, 100);
    let search = query.query.as_deref().unwrap_or_default().trim();
    if search.chars().count() > MAX_QUERY_CHARACTERS {
        return no_cache(with_auth_version(failure(
            StatusCode::BAD_REQUEST,
            "subscription reset search filter is too long",
        )));
    }
    if query.plan_id.is_some_and(|id| id <= 0) {
        return no_cache(with_auth_version(failure(
            StatusCode::BAD_REQUEST,
            "invalid subscription reset plan filter",
        )));
    }
    let now = match database_timestamp(&state.pg).await {
        Ok(value) => value,
        Err(_) => {
            return no_cache(with_auth_version(failure(
                StatusCode::INTERNAL_SERVER_ERROR,
                "系统错误",
            )));
        }
    };
    let plan_ids = match parse_query_ids(query.plan_ids.as_deref()) {
        Ok(values) => values,
        Err(error) => return no_cache(with_auth_version(error_response(error))),
    };
    let user_ids = match parse_query_ids(query.user_ids.as_deref()) {
        Ok(values) => values,
        Err(error) => return no_cache(with_auth_version(error_response(error))),
    };
    if query.plan_id.is_some() && !plan_ids.is_empty() {
        return no_cache(with_auth_version(failure(
            StatusCode::BAD_REQUEST,
            "plan_id cannot be combined with plan_ids",
        )));
    }
    let result = eligible_rows(
        &state.pg,
        search,
        query.plan_id.unwrap_or_default(),
        &plan_ids,
        &user_ids,
        now,
        (page - 1).saturating_mul(page_size),
        page_size,
    )
    .await;
    no_cache(with_auth_version(match result {
        Ok((total, items)) => ok(Page {
            items,
            total,
            page,
            page_size,
        }),
        Err(_) => failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
    }))
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, Hash, PartialEq, Serialize, sqlx::FromRow)]
struct Target {
    user_id: i64,
    plan_id: i64,
}

#[derive(Default, Deserialize)]
#[serde(deny_unknown_fields)]
struct ResetFilter {
    #[serde(default)]
    query: String,
    #[serde(default)]
    plan_id: i64,
    #[serde(default)]
    plan_ids: Vec<i64>,
    #[serde(default)]
    user_ids: Vec<i64>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ResetPreviewRequest {
    mode: String,
    #[serde(default)]
    targets: Vec<Target>,
    #[serde(default)]
    all_matching: bool,
    filter: Option<ResetFilter>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ResetExecuteRequest {
    operation_id: String,
    preview_token: String,
}

#[derive(Clone, Debug, Deserialize, Serialize, sqlx::FromRow)]
struct FrozenSubscription {
    id: i64,
    user_id: i64,
    plan_id: i64,
    amount_used: i64,
    status: String,
    end_time: i64,
    updated_at: i64,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
struct FrozenTarget {
    user_id: i64,
    plan_id: i64,
    subscriptions: Vec<FrozenSubscription>,
}

#[derive(sqlx::FromRow)]
struct ActiveRow {
    id: i64,
    user_id: i64,
    username: String,
    email: String,
    plan_id: i64,
    plan_title: String,
    plan_archived_at: i64,
    amount_total: i64,
    amount_used: i64,
    next_reset_time: i64,
    status: String,
    end_time: i64,
    updated_at: i64,
    banked_voucher_count: i64,
}

fn checked_reset_add(current: i64, value: i64) -> Result<i64, ResetError> {
    current.checked_add(value).ok_or(ResetError::Business(
        "subscription reset quota total exceeds the supported range",
    ))
}

fn normalize_mode(value: &str) -> Result<&'static str, ResetError> {
    match value.trim().to_ascii_lowercase().as_str() {
        "hard" => Ok("hard"),
        "soft" => Ok("soft"),
        _ => Err(ResetError::Business("invalid subscription reset mode")),
    }
}

fn normalize_ids(values: &[i64], label: &'static str) -> Result<Vec<i64>, ResetError> {
    if values.len() > MAX_FILTER_IDS {
        return Err(ResetError::Owned(format!(
            "too many subscription reset {label} filters"
        )));
    }
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    for value in values {
        if *value <= 0 {
            return Err(ResetError::Owned(format!(
                "invalid subscription reset {label} filter"
            )));
        }
        if seen.insert(*value) {
            result.push(*value);
        }
    }
    Ok(result)
}

async fn resolve_targets(
    pg: &PgPool,
    request: &ResetPreviewRequest,
    now: i64,
) -> Result<Vec<Target>, ResetError> {
    if !request.all_matching {
        if request.targets.len() > MAX_TARGETS {
            return Err(ResetError::Owned(format!(
                "too many subscription reset targets: {}",
                request.targets.len()
            )));
        }
        let mut seen = HashSet::new();
        let mut targets = Vec::new();
        for target in &request.targets {
            if target.user_id <= 0 || target.plan_id <= 0 {
                return Err(ResetError::Business("invalid subscription reset target"));
            }
            if seen.insert(*target) {
                targets.push(*target);
            }
        }
        return Ok(targets);
    }
    if !request.targets.is_empty() {
        return Err(ResetError::Business(
            "explicit reset targets cannot be combined with all_matching",
        ));
    }
    let filter = request.filter.as_ref().ok_or(ResetError::Business(
        "all_matching subscription resets require an explicit filter object",
    ))?;
    if filter.plan_id < 0 {
        return Err(ResetError::Business(
            "invalid subscription reset plan filter",
        ));
    }
    if filter.plan_id > 0 && !filter.plan_ids.is_empty() {
        return Err(ResetError::Business(
            "plan_id cannot be combined with plan_ids",
        ));
    }
    let search = filter.query.trim();
    if search.chars().count() > MAX_QUERY_CHARACTERS {
        return Err(ResetError::Business(
            "subscription reset search filter is too long",
        ));
    }
    let plan_ids = normalize_ids(&filter.plan_ids, "plan")?;
    let user_ids = normalize_ids(&filter.user_ids, "user")?;
    let like = format!("%{}%", search.to_ascii_lowercase());
    let rows = sqlx::query_as::<_, Target>(
        "SELECT us.user_id,us.plan_id FROM user_subscriptions us JOIN users ON users.id=us.user_id AND users.deleted_at IS NULL WHERE us.status='active' AND us.end_time>$1 AND ($2='' OR CAST(us.user_id AS TEXT) LIKE $3 OR LOWER(users.username) LIKE $3 OR LOWER(COALESCE(users.email,'')) LIKE $3) AND ($4::BIGINT<=0 OR us.plan_id=$4) AND (CARDINALITY($5::BIGINT[])=0 OR us.plan_id=ANY($5)) AND (CARDINALITY($6::BIGINT[])=0 OR us.user_id=ANY($6)) GROUP BY us.user_id,us.plan_id ORDER BY us.user_id,us.plan_id LIMIT 5001",
    )
    .bind(now)
    .bind(search)
    .bind(like)
    .bind(filter.plan_id)
    .bind(&plan_ids)
    .bind(&user_ids)
    .fetch_all(pg)
    .await?;
    if rows.len() > MAX_TARGETS {
        return Err(ResetError::Business(
            "subscription reset selection exceeds 5000 targets",
        ));
    }
    Ok(rows)
}

async fn load_frozen(
    pg: &PgPool,
    targets: &[Target],
    now: i64,
) -> Result<(Vec<Eligible>, Vec<FrozenTarget>), ResetError> {
    if targets.is_empty() {
        return Ok((Vec::new(), Vec::new()));
    }
    let users = targets
        .iter()
        .map(|target| target.user_id)
        .collect::<Vec<_>>();
    let plans = targets
        .iter()
        .map(|target| target.plan_id)
        .collect::<Vec<_>>();
    let selected = targets.iter().copied().collect::<HashSet<_>>();
    let rows = sqlx::query_as::<_, ActiveRow>(
        "SELECT us.id,us.user_id,users.username,COALESCE(users.email,'') email,us.plan_id,plans.title plan_title,COALESCE(plans.archived_at,0) plan_archived_at,us.amount_total,us.amount_used,COALESCE(us.next_reset_time,0) next_reset_time,us.status,us.end_time,COALESCE(us.updated_at,0) updated_at,(SELECT COUNT(*) FROM subscription_reset_vouchers v WHERE v.user_id=us.user_id AND v.plan_id=us.plan_id AND v.status='available' AND v.expires_at>$3)::BIGINT banked_voucher_count FROM unnest($1::BIGINT[],$2::BIGINT[]) selected(user_id,plan_id) JOIN user_subscriptions us ON us.user_id=selected.user_id AND us.plan_id=selected.plan_id JOIN users ON users.id=us.user_id AND users.deleted_at IS NULL JOIN subscription_plans plans ON plans.id=us.plan_id WHERE us.status='active' AND us.end_time>$3 ORDER BY us.user_id,us.plan_id,us.id LIMIT 20001",
    )
    .bind(&users)
    .bind(&plans)
    .bind(now)
    .fetch_all(pg)
    .await?;
    if rows.len() > MAX_ACTIVE_SUBSCRIPTIONS {
        return Err(ResetError::Business(
            "subscription reset selection exceeds 20000 active subscriptions",
        ));
    }
    let mut summaries = HashMap::<Target, Eligible>::new();
    let mut frozen = HashMap::<Target, FrozenTarget>::new();
    for row in rows {
        if row.amount_used < 0 {
            return Err(ResetError::Business(
                "subscription reset encountered a negative used quota",
            ));
        }
        let target = Target {
            user_id: row.user_id,
            plan_id: row.plan_id,
        };
        if !selected.contains(&target) {
            continue;
        }
        let summary = summaries.entry(target).or_insert_with(|| Eligible {
            user_id: row.user_id,
            username: row.username.clone(),
            email: row.email.clone(),
            plan_id: row.plan_id,
            plan_title: row.plan_title.clone(),
            plan_archived_at: row.plan_archived_at,
            active_subscription_count: 0,
            amount_total: 0,
            amount_used: 0,
            next_reset_time: 0,
            banked_voucher_count: row.banked_voucher_count,
        });
        summary.active_subscription_count = summary
            .active_subscription_count
            .checked_add(1)
            .ok_or(ResetError::Business(
                "subscription reset count exceeds the supported range",
            ))?;
        summary.amount_total = checked_reset_add(summary.amount_total, row.amount_total)?;
        summary.amount_used = checked_reset_add(summary.amount_used, row.amount_used)?;
        if row.next_reset_time > 0
            && (summary.next_reset_time == 0 || row.next_reset_time < summary.next_reset_time)
        {
            summary.next_reset_time = row.next_reset_time;
        }
        frozen
            .entry(target)
            .or_insert_with(|| FrozenTarget {
                user_id: row.user_id,
                plan_id: row.plan_id,
                subscriptions: Vec::new(),
            })
            .subscriptions
            .push(FrozenSubscription {
                id: row.id,
                user_id: row.user_id,
                plan_id: row.plan_id,
                amount_used: row.amount_used,
                status: row.status,
                end_time: row.end_time,
                updated_at: row.updated_at,
            });
    }
    let mut ordered_summaries = Vec::new();
    let mut ordered_frozen = Vec::new();
    for target in targets {
        if let (Some(summary), Some(target_frozen)) =
            (summaries.remove(target), frozen.remove(target))
        {
            ordered_summaries.push(summary);
            ordered_frozen.push(target_frozen);
        }
    }
    Ok((ordered_summaries, ordered_frozen))
}

fn payload_hash(mode: &str, targets: &[FrozenTarget]) -> Result<String, ResetError> {
    let payload = serde_json::to_vec(&json!({"mode": mode, "targets": targets}))?;
    Ok(format!("{:x}", Sha256::digest(payload)))
}

fn one_calendar_month(timestamp: i64) -> Result<i64, ResetError> {
    let current = Utc
        .timestamp_opt(timestamp, 0)
        .single()
        .ok_or(ResetError::Business("invalid subscription reset timestamp"))?;
    let (year, month) = if current.month() == 12 {
        (current.year() + 1, 1)
    } else {
        (current.year(), current.month() + 1)
    };
    let following = if month == 12 {
        Utc.with_ymd_and_hms(year + 1, 1, 1, 0, 0, 0).single()
    } else {
        Utc.with_ymd_and_hms(year, month + 1, 1, 0, 0, 0).single()
    }
    .ok_or(ResetError::Business("invalid subscription reset timestamp"))?;
    let last_day = (following - chrono::Duration::days(1)).day();
    Utc.with_ymd_and_hms(
        year,
        month,
        current.day().min(last_day),
        current.hour(),
        current.minute(),
        current.second(),
    )
    .single()
    .map(|value| value.timestamp())
    .ok_or(ResetError::Business("invalid subscription reset timestamp"))
}

#[derive(Serialize)]
struct PreviewResult {
    token: String,
    mode: String,
    target_count: i64,
    user_count: usize,
    plan_count: usize,
    active_subscriptions: i64,
    quota_to_restore: i64,
    #[serde(skip_serializing_if = "is_zero")]
    voucher_expires_at: i64,
    expires_at: i64,
    targets: Vec<Eligible>,
}

fn is_zero(value: &i64) -> bool {
    *value == 0
}

async fn root_preview(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
    request: Request,
) -> Response {
    let actor = match root_write(&state, &headers).await {
        Ok(value) => value,
        Err(response) => return no_cache(response),
    };
    let client_ip = request_ip(&request);
    if let Some(response) = critical_limit(&state, &client_ip).await {
        return response;
    }
    let request: ResetPreviewRequest = match json_body(request).await {
        Ok(value) => value,
        Err(response) => return no_cache(with_auth_version(response)),
    };
    let result = preview(&state.pg, actor.id, request).await;
    no_cache(with_auth_version(match result {
        Ok(value) => ok(value),
        Err(error) => error_response(error),
    }))
}

async fn preview(
    pg: &PgPool,
    actor_user_id: i64,
    request: ResetPreviewRequest,
) -> Result<PreviewResult, ResetError> {
    let mode = normalize_mode(&request.mode)?;
    let now = database_timestamp(pg).await?;
    let targets = resolve_targets(pg, &request, now).await?;
    let (summaries, frozen) = load_frozen(pg, &targets, now).await?;
    if summaries.is_empty() {
        return Err(ResetError::Business(
            "no active subscription users matched the reset request",
        ));
    }
    let target_count = i64::try_from(summaries.len())
        .map_err(|_| ResetError::Business("subscription reset selection exceeds 5000 targets"))?;
    let users = summaries
        .iter()
        .map(|item| item.user_id)
        .collect::<HashSet<_>>();
    let plans = summaries
        .iter()
        .map(|item| item.plan_id)
        .collect::<HashSet<_>>();
    let active_subscriptions = summaries.iter().try_fold(0_i64, |total, item| {
        total
            .checked_add(item.active_subscription_count)
            .ok_or(ResetError::Business(
                "subscription reset count exceeds the supported range",
            ))
    })?;
    let quota_to_restore = summaries.iter().try_fold(0_i64, |total, item| {
        checked_reset_add(total, item.amount_used)
    })?;
    let voucher_expires_at = if mode == "soft" {
        one_calendar_month(now)?
    } else {
        0
    };
    let token = Uuid::new_v4().to_string();
    let expires_at = now + PREVIEW_LIFETIME_SECONDS;
    let targets_json = serde_json::to_string(&frozen)?;
    let hash = payload_hash(mode, &frozen)?;
    sqlx::query(
        "INSERT INTO subscription_reset_previews (token,actor_user_id,mode,targets_json,payload_hash,target_count,active_subscriptions,quota_to_restore,voucher_expires_at,expires_at,consumed_at,operation_id,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,0,'',$11)",
    )
    .bind(&token)
    .bind(actor_user_id)
    .bind(mode)
    .bind(targets_json)
    .bind(hash)
    .bind(target_count)
    .bind(active_subscriptions)
    .bind(quota_to_restore)
    .bind(voucher_expires_at)
    .bind(expires_at)
    .bind(now)
    .execute(pg)
    .await?;
    Ok(PreviewResult {
        token,
        mode: mode.to_owned(),
        target_count,
        user_count: users.len(),
        plan_count: plans.len(),
        active_subscriptions,
        quota_to_restore,
        voucher_expires_at,
        expires_at,
        targets: summaries,
    })
}

#[derive(Clone, Deserialize, Serialize)]
struct BatchResult {
    operation_id: String,
    mode: String,
    requested_targets: i64,
    processed_targets: i64,
    skipped_targets: i64,
    reset_subscriptions: i64,
    restored_quota: i64,
    vouchers_issued: i64,
    #[serde(default, skip_serializing_if = "is_zero")]
    voucher_expires_at: i64,
}

#[derive(sqlx::FromRow)]
struct OperationRow {
    preview_token: String,
    actor_user_id: i64,
    result_json: String,
}

#[derive(sqlx::FromRow)]
struct PreviewRow {
    mode: String,
    targets_json: String,
    payload_hash: String,
    target_count: i64,
    voucher_expires_at: i64,
    expires_at: i64,
    consumed_at: i64,
}

fn operation_result(
    operation: &OperationRow,
    actor: i64,
    preview_token: &str,
) -> Result<BatchResult, ResetError> {
    if operation.actor_user_id != actor || operation.preview_token != preview_token {
        return Err(ResetError::Conflict);
    }
    serde_json::from_str(&operation.result_json)
        .map_err(|_| ResetError::Business("subscription reset operation result is malformed"))
}

async fn find_operation<'e, E>(
    executor: E,
    operation_id: &str,
) -> Result<Option<OperationRow>, sqlx::Error>
where
    E: sqlx::Executor<'e, Database = Postgres>,
{
    sqlx::query_as::<_, OperationRow>(
        "SELECT preview_token,actor_user_id,result_json FROM subscription_reset_operations WHERE operation_id=$1",
    )
    .bind(operation_id)
    .fetch_optional(executor)
    .await
}

async fn verify_frozen(
    tx: &mut Transaction<'_, Postgres>,
    targets: &[FrozenTarget],
    now: i64,
) -> Result<(), ResetError> {
    if targets.is_empty() || targets.len() > MAX_TARGETS {
        return Err(ResetError::Stale);
    }
    let mut expected = HashMap::new();
    let mut expected_by_pair = HashMap::<Target, Vec<i64>>::new();
    let mut users = Vec::with_capacity(targets.len());
    let mut plans = Vec::with_capacity(targets.len());
    for target in targets {
        let pair = Target {
            user_id: target.user_id,
            plan_id: target.plan_id,
        };
        if pair.user_id <= 0
            || pair.plan_id <= 0
            || target.subscriptions.is_empty()
            || expected_by_pair.contains_key(&pair)
        {
            return Err(ResetError::Stale);
        }
        let mut ids = Vec::with_capacity(target.subscriptions.len());
        for item in &target.subscriptions {
            if item.id <= 0
                || item.user_id != pair.user_id
                || item.plan_id != pair.plan_id
                || expected.insert(item.id, item).is_some()
            {
                return Err(ResetError::Stale);
            }
            ids.push(item.id);
        }
        expected_by_pair.insert(pair, ids);
        users.push(pair.user_id);
        plans.push(pair.plan_id);
    }
    if expected.len() > MAX_ACTIVE_SUBSCRIPTIONS {
        return Err(ResetError::Stale);
    }
    let rows = sqlx::query_as::<_, FrozenSubscription>(
        "SELECT us.id,us.user_id,us.plan_id,us.amount_used,us.status,us.end_time,COALESCE(us.updated_at,0) updated_at FROM unnest($1::BIGINT[],$2::BIGINT[]) selected(user_id,plan_id) JOIN user_subscriptions us ON us.user_id=selected.user_id AND us.plan_id=selected.plan_id WHERE us.status='active' AND us.end_time>$3 ORDER BY us.user_id,us.plan_id,us.id FOR UPDATE OF us",
    )
    .bind(&users)
    .bind(&plans)
    .bind(now)
    .fetch_all(&mut **tx)
    .await?;
    if rows.len() != expected.len() {
        return Err(ResetError::Stale);
    }
    let mut current_by_pair = HashMap::<Target, Vec<i64>>::new();
    for row in &rows {
        let Some(frozen) = expected.get(&row.id) else {
            return Err(ResetError::Stale);
        };
        if row.user_id != frozen.user_id
            || row.plan_id != frozen.plan_id
            || row.amount_used != frozen.amount_used
            || row.status != frozen.status
            || row.end_time != frozen.end_time
            || row.updated_at != frozen.updated_at
        {
            return Err(ResetError::Stale);
        }
        current_by_pair
            .entry(Target {
                user_id: row.user_id,
                plan_id: row.plan_id,
            })
            .or_default()
            .push(row.id);
    }
    if current_by_pair != expected_by_pair {
        return Err(ResetError::Stale);
    }
    Ok(())
}

async fn root_execute(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
    request: Request,
) -> Response {
    let actor = match root_write(&state, &headers).await {
        Ok(value) => value,
        Err(response) => return no_cache(response),
    };
    let client_ip = request_ip(&request);
    if let Some(response) = critical_limit(&state, &client_ip).await {
        return response;
    }
    let request: ResetExecuteRequest = match json_body(request).await {
        Ok(value) => value,
        Err(response) => return no_cache(with_auth_version(response)),
    };
    let result = execute(&state, &actor, request).await;
    no_cache(with_auth_version(match result {
        Ok(value) => ok(value),
        Err(error) => error_response(error),
    }))
}

async fn execute(
    state: &BillingSubscriptionsState,
    actor: &DashboardUser,
    request: ResetExecuteRequest,
) -> Result<BatchResult, ResetError> {
    let operation_id = request.operation_id.trim();
    let preview_token = request.preview_token.trim();
    if preview_token.is_empty() {
        return Err(ResetError::Business(
            "subscription reset preview is required",
        ));
    }
    if operation_id.is_empty() {
        return Err(ResetError::Business(
            "subscription reset operation id is required",
        ));
    }
    if operation_id.len() > 64 {
        return Err(ResetError::Business(
            "subscription reset operation id is too long",
        ));
    }
    let now = database_timestamp(&state.pg).await?;
    let mut tx = state.pg.begin().await?;
    if let Some(operation) = find_operation(&mut *tx, operation_id).await? {
        return operation_result(&operation, actor.id, preview_token);
    }
    let preview = sqlx::query_as::<_, PreviewRow>(
        "SELECT mode,targets_json,payload_hash,target_count,voucher_expires_at,expires_at,consumed_at FROM subscription_reset_previews WHERE token=$1 AND actor_user_id=$2 FOR UPDATE",
    )
    .bind(preview_token)
    .bind(actor.id)
    .fetch_optional(&mut *tx)
    .await?
    .ok_or(ResetError::NotFound)?;
    if preview.consumed_at > 0 {
        if let Some(operation) = find_operation(&mut *tx, operation_id).await? {
            return operation_result(&operation, actor.id, preview_token);
        }
        return Err(ResetError::Business(
            "subscription reset preview has already been consumed",
        ));
    }
    if preview.expires_at <= now {
        return Err(ResetError::Business(
            "subscription reset preview has expired",
        ));
    }
    let targets: Vec<FrozenTarget> = serde_json::from_str(&preview.targets_json)
        .map_err(|_| ResetError::Business("subscription reset preview targets are malformed"))?;
    if i64::try_from(targets.len()).ok() != Some(preview.target_count)
        || payload_hash(&preview.mode, &targets)? != preview.payload_hash
    {
        return Err(ResetError::Business(
            "subscription reset preview payload is invalid",
        ));
    }
    verify_frozen(&mut tx, &targets, now).await?;
    let claimed = sqlx::query(
        "UPDATE subscription_reset_previews SET consumed_at=$3,operation_id=$4 WHERE token=$1 AND actor_user_id=$2 AND consumed_at=0 AND expires_at>$3",
    )
    .bind(preview_token)
    .bind(actor.id)
    .bind(now)
    .bind(operation_id)
    .execute(&mut *tx)
    .await?;
    if claimed.rows_affected() != 1 {
        return Err(ResetError::Business(
            "subscription reset preview has already been consumed",
        ));
    }
    let requested_targets = i64::try_from(targets.len()).map_err(|_| ResetError::Stale)?;
    let mut result = BatchResult {
        operation_id: operation_id.to_owned(),
        mode: preview.mode.clone(),
        requested_targets,
        processed_targets: requested_targets,
        skipped_targets: 0,
        reset_subscriptions: 0,
        restored_quota: 0,
        vouchers_issued: 0,
        voucher_expires_at: preview.voucher_expires_at,
    };
    let mut affected_users = BTreeSet::new();
    for target in &targets {
        let mut voucher_id = 0_i64;
        let mut reset_count = 0_i64;
        let mut restored_quota = 0_i64;
        if preview.mode == "hard" {
            for frozen in &target.subscriptions {
                let changed = sqlx::query(
                    "UPDATE user_subscriptions SET amount_used=0 WHERE id=$1 AND user_id=$2 AND plan_id=$3 AND status=$4 AND end_time=$5 AND end_time>$6 AND amount_used=$7 AND COALESCE(updated_at,0)=$8",
                )
                .bind(frozen.id)
                .bind(frozen.user_id)
                .bind(frozen.plan_id)
                .bind(&frozen.status)
                .bind(frozen.end_time)
                .bind(now)
                .bind(frozen.amount_used)
                .bind(frozen.updated_at)
                .execute(&mut *tx)
                .await?;
                if changed.rows_affected() != 1 {
                    return Err(ResetError::Stale);
                }
                reset_count = reset_count.checked_add(1).ok_or(ResetError::Business(
                    "subscription reset count exceeds the supported range",
                ))?;
                restored_quota = checked_reset_add(restored_quota, frozen.amount_used)?;
            }
            result.reset_subscriptions = result
                .reset_subscriptions
                .checked_add(reset_count)
                .ok_or(ResetError::Business(
                    "subscription reset count exceeds the supported range",
                ))?;
            result.restored_quota = checked_reset_add(result.restored_quota, restored_quota)?;
            affected_users.insert(target.user_id);
        } else {
            voucher_id = sqlx::query_scalar::<_, i64>(
                "INSERT INTO subscription_reset_vouchers (user_id,plan_id,operation_id,status,expires_at,redeemed_at,created_by,created_at,updated_at) VALUES ($1,$2,$3,'available',$4,0,$5,$6,$6) RETURNING id",
            )
            .bind(target.user_id)
            .bind(target.plan_id)
            .bind(operation_id)
            .bind(preview.voucher_expires_at)
            .bind(actor.id)
            .bind(now)
            .fetch_one(&mut *tx)
            .await?;
            result.vouchers_issued =
                result
                    .vouchers_issued
                    .checked_add(1)
                    .ok_or(ResetError::Business(
                        "subscription reset count exceeds the supported range",
                    ))?;
        }
        sqlx::query(
            "INSERT INTO subscription_reset_events (operation_id,user_id,plan_id,mode,actor_user_id,voucher_id,reset_count,restored_quota,voucher_expiry,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)",
        )
        .bind(operation_id)
        .bind(target.user_id)
        .bind(target.plan_id)
        .bind(&preview.mode)
        .bind(actor.id)
        .bind(voucher_id)
        .bind(reset_count)
        .bind(restored_quota)
        .bind(if preview.mode == "soft" { preview.voucher_expires_at } else { 0 })
        .bind(now)
        .execute(&mut *tx)
        .await?;
    }
    let result_json = serde_json::to_string(&result)?;
    sqlx::query(
        "INSERT INTO subscription_reset_operations (operation_id,preview_token,actor_user_id,mode,payload_hash,result_json,created_at,completed_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$7)",
    )
    .bind(operation_id)
    .bind(preview_token)
    .bind(actor.id)
    .bind(&preview.mode)
    .bind(&preview.payload_hash)
    .bind(result_json)
    .bind(now)
    .execute(&mut *tx)
    .await?;
    insert_audit(
        &mut tx,
        actor,
        now,
        "POST /api/subscription/root/reset",
        "subscription.reset.execute",
        json!({
            "operation_id":operation_id,"mode":preview.mode,
            "requested_targets":result.requested_targets,
            "processed_targets":result.processed_targets,
            "reset_subscriptions":result.reset_subscriptions,
            "restored_quota":result.restored_quota,"vouchers_issued":result.vouchers_issued
        }),
    )
    .await?;
    match tx.commit().await {
        Ok(()) => {}
        Err(error) => {
            if let Some(operation) = find_operation(&state.pg, operation_id).await? {
                return operation_result(&operation, actor.id, preview_token);
            }
            return Err(ResetError::Database(error));
        }
    }
    for user_id in affected_users {
        evict_user_cache(state.valkey.as_ref(), user_id).await;
    }
    Ok(result)
}

#[derive(Serialize, sqlx::FromRow)]
struct Voucher {
    id: i64,
    user_id: i64,
    plan_id: i64,
    operation_id: String,
    status: String,
    expires_at: i64,
    redeemed_at: i64,
    created_by: i64,
    created_at: i64,
    updated_at: i64,
    plan_title: String,
    expired: bool,
}

async fn self_vouchers(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
) -> Response {
    let user = match super::identity(&state, &headers).await {
        Ok(value) => value,
        Err(response) => return no_cache(response),
    };
    let now = match database_timestamp(&state.pg).await {
        Ok(value) => value,
        Err(_) => {
            return no_cache(with_auth_version(failure(
                StatusCode::INTERNAL_SERVER_ERROR,
                "系统错误",
            )));
        }
    };
    let rows = sqlx::query_as::<_, Voucher>(
        "SELECT v.id,v.user_id,v.plan_id,v.operation_id,v.status,v.expires_at,v.redeemed_at,v.created_by,v.created_at,v.updated_at,p.title plan_title,(v.status='available' AND v.expires_at<=$2) expired FROM subscription_reset_vouchers v JOIN subscription_plans p ON p.id=v.plan_id WHERE v.user_id=$1 ORDER BY CASE WHEN v.status='available' AND v.expires_at>$2 THEN 0 ELSE 1 END,v.id DESC LIMIT 100",
    )
    .bind(user.id)
    .bind(now)
    .fetch_all(&state.pg)
    .await;
    no_cache(with_auth_version(match rows {
        Ok(value) => ok(value),
        Err(_) => failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
    }))
}

#[derive(Serialize)]
struct VoucherResetResult {
    plan_id: i64,
    plan_title: String,
    matched_count: i64,
    reset_count: i64,
    user_count: i64,
    advance_reset_time: bool,
    restored_quota: i64,
    affected_user_ids: Vec<i64>,
}

#[derive(sqlx::FromRow)]
struct VoucherRow {
    id: i64,
    plan_id: i64,
    plan_title: String,
    status: String,
    expires_at: i64,
}

async fn redeem_voucher(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
    Path(voucher_id): Path<i64>,
    request: Request,
) -> Response {
    let user = match super::identity(&state, &headers).await {
        Ok(value) => value,
        Err(response) => return no_cache(response),
    };
    let client_ip = request_ip(&request);
    if let Some(response) = critical_limit(&state, &client_ip).await {
        return response;
    }
    if voucher_id <= 0 {
        return no_cache(with_auth_version(failure(
            StatusCode::BAD_REQUEST,
            "无效的重置券",
        )));
    }
    let result = redeem(&state, &user, voucher_id).await;
    no_cache(with_auth_version(match result {
        Ok(value) => ok(value),
        Err(error) => error_response(error),
    }))
}

async fn redeem(
    state: &BillingSubscriptionsState,
    user: &DashboardUser,
    voucher_id: i64,
) -> Result<VoucherResetResult, ResetError> {
    let now = database_timestamp(&state.pg).await?;
    let operation_id = format!("voucher:{voucher_id}");
    let mut tx = state.pg.begin().await?;
    let voucher = sqlx::query_as::<_, VoucherRow>(
        "SELECT v.id,v.plan_id,p.title plan_title,v.status,v.expires_at FROM subscription_reset_vouchers v JOIN subscription_plans p ON p.id=v.plan_id WHERE v.id=$1 AND v.user_id=$2 FOR UPDATE OF v",
    )
    .bind(voucher_id)
    .bind(user.id)
    .fetch_optional(&mut *tx)
    .await?
    .ok_or(ResetError::NotFound)?;
    if voucher.status == "redeemed" {
        let row = sqlx::query(
            "SELECT reset_count,restored_quota FROM subscription_reset_events WHERE operation_id=$1 AND voucher_id=$2 AND mode='voucher_redeem'",
        )
        .bind(&operation_id)
        .bind(voucher_id)
        .fetch_optional(&mut *tx)
        .await?
        .ok_or(ResetError::VoucherUnavailable)?;
        return Ok(VoucherResetResult {
            plan_id: voucher.plan_id,
            plan_title: voucher.plan_title,
            matched_count: row.try_get("reset_count")?,
            reset_count: row.try_get("reset_count")?,
            user_count: 1,
            advance_reset_time: false,
            restored_quota: row.try_get("restored_quota")?,
            affected_user_ids: vec![user.id],
        });
    }
    if voucher.status != "available" {
        return Err(ResetError::VoucherUnavailable);
    }
    if voucher.expires_at <= now {
        return Err(ResetError::VoucherExpired);
    }
    let rows = sqlx::query(
        "SELECT id,amount_used FROM user_subscriptions WHERE user_id=$1 AND plan_id=$2 AND status='active' AND end_time>$3 ORDER BY end_time,id FOR UPDATE",
    )
    .bind(user.id)
    .bind(voucher.plan_id)
    .bind(now)
    .fetch_all(&mut *tx)
    .await?;
    let reset_count = locked_voucher_reset_count(rows.len())?;
    let claimed = sqlx::query(
        "UPDATE subscription_reset_vouchers SET status='redeemed',redeemed_at=$3,updated_at=$3 WHERE id=$1 AND user_id=$2 AND status='available' AND expires_at>$3",
    )
    .bind(voucher.id)
    .bind(user.id)
    .bind(now)
    .execute(&mut *tx)
    .await?;
    if claimed.rows_affected() != 1 {
        return Err(ResetError::VoucherUnavailable);
    }
    let mut restored_quota = 0_i64;
    for row in &rows {
        let amount_used = row.try_get::<i64, _>("amount_used")?;
        if amount_used < 0 {
            return Err(ResetError::Business(
                "subscription reset encountered a negative used quota",
            ));
        }
        restored_quota = checked_reset_add(restored_quota, amount_used)?;
        sqlx::query("UPDATE user_subscriptions SET amount_used=0 WHERE id=$1")
            .bind(row.try_get::<i64, _>("id")?)
            .execute(&mut *tx)
            .await?;
    }
    sqlx::query(
        "INSERT INTO subscription_reset_events (operation_id,user_id,plan_id,mode,actor_user_id,voucher_id,reset_count,restored_quota,voucher_expiry,created_at) VALUES ($1,$2,$3,'voucher_redeem',$2,$4,$5,$6,0,$7)",
    )
    .bind(&operation_id)
    .bind(user.id)
    .bind(voucher.plan_id)
    .bind(voucher.id)
    .bind(reset_count)
    .bind(restored_quota)
    .bind(now)
    .execute(&mut *tx)
    .await?;
    insert_audit(
        &mut tx,
        user,
        now,
        &format!("POST /api/subscription/self/reset-vouchers/{voucher_id}/redeem"),
        "subscription.reset.voucher_redeem",
        json!({"voucher_id":voucher_id,"reset_subscriptions":reset_count,"restored_quota":restored_quota}),
    )
    .await?;
    tx.commit().await?;
    evict_user_cache(state.valkey.as_ref(), user.id).await;
    Ok(VoucherResetResult {
        plan_id: voucher.plan_id,
        plan_title: voucher.plan_title,
        matched_count: reset_count,
        reset_count,
        user_count: 1,
        advance_reset_time: false,
        restored_quota,
        affected_user_ids: vec![user.id],
    })
}

fn locked_voucher_reset_count(locked_subscription_count: usize) -> Result<i64, ResetError> {
    let reset_count = i64::try_from(locked_subscription_count).map_err(|_| ResetError::Stale)?;
    if reset_count == 0 {
        return Err(ResetError::RequiresActive);
    }
    Ok(reset_count)
}

async fn insert_audit(
    tx: &mut Transaction<'_, Postgres>,
    actor: &DashboardUser,
    now: i64,
    content: &str,
    action: &str,
    params: Value,
) -> Result<(), ResetError> {
    sqlx::query(
        "INSERT INTO logs (user_id,created_at,type,content,username,ip,other) VALUES ($1,$2,3,$3,$4,'',$5)",
    )
    .bind(actor.id)
    .bind(now)
    .bind(content)
    .bind(&actor.username)
    .bind(json!({"op":{"action":action,"params":params}}).to_string())
    .execute(&mut **tx)
    .await?;
    Ok(())
}

#[derive(Debug)]
enum ResetError {
    Database(sqlx::Error),
    Json(serde_json::Error),
    Business(&'static str),
    Owned(String),
    NotFound,
    Stale,
    Conflict,
    VoucherUnavailable,
    VoucherExpired,
    RequiresActive,
}

impl From<sqlx::Error> for ResetError {
    fn from(value: sqlx::Error) -> Self {
        Self::Database(value)
    }
}

impl From<serde_json::Error> for ResetError {
    fn from(value: serde_json::Error) -> Self {
        Self::Json(value)
    }
}

fn error_response(error: ResetError) -> Response {
    match error {
        ResetError::Business(message) => failure(StatusCode::BAD_REQUEST, message),
        ResetError::Owned(message) => {
            Json(json!({"success":false,"message":message})).into_response()
        }
        ResetError::NotFound => failure(StatusCode::BAD_REQUEST, "重置券不存在"),
        ResetError::Stale => failure(
            StatusCode::BAD_REQUEST,
            "subscription reset preview is stale",
        ),
        ResetError::Conflict => failure(
            StatusCode::BAD_REQUEST,
            "subscription reset operation id is already bound to another preview",
        ),
        ResetError::VoucherUnavailable => failure(StatusCode::BAD_REQUEST, "重置券已使用或不可用"),
        ResetError::VoucherExpired => failure(StatusCode::BAD_REQUEST, "重置券已过期"),
        ResetError::RequiresActive => {
            failure(StatusCode::BAD_REQUEST, "仅有效订阅用户可以使用重置券")
        }
        ResetError::Database(error) => {
            tracing::warn!(%error, "subscription reset database operation failed");
            failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误")
        }
        ResetError::Json(error) => {
            tracing::warn!(%error, "subscription reset JSON operation failed");
            failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误")
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn voucher_redemption_rejects_an_empty_locked_subscription_set() {
        assert!(matches!(
            locked_voucher_reset_count(0),
            Err(ResetError::RequiresActive)
        ));
        assert_eq!(locked_voucher_reset_count(2).expect("locked rows"), 2);
    }

    #[test]
    fn hard_reset_replay_defaults_the_omitted_voucher_expiry() -> Result<(), ResetError> {
        let result_json = serde_json::to_string(&BatchResult {
            operation_id: "hard-reset".to_owned(),
            mode: "hard".to_owned(),
            requested_targets: 1,
            processed_targets: 1,
            skipped_targets: 0,
            reset_subscriptions: 1,
            restored_quota: 10,
            vouchers_issued: 0,
            voucher_expires_at: 0,
        })?;
        assert!(!result_json.contains("voucher_expires_at"));
        let operation = OperationRow {
            preview_token: "preview".to_owned(),
            actor_user_id: 1,
            result_json,
        };
        let replay = operation_result(&operation, 1, "preview")?;
        assert_eq!(replay.voucher_expires_at, 0);
        Ok(())
    }

    #[test]
    fn preview_filter_uses_go_snake_case_contract() -> Result<(), Box<dyn std::error::Error>> {
        let request: ResetPreviewRequest = serde_json::from_str(
            r#"{"mode":"hard","all_matching":true,"filter":{"query":"alice","plan_id":7,"plan_ids":[7,8],"user_ids":[9,10]}}"#,
        )?;
        let filter = request.filter.as_ref().ok_or("filter was not decoded")?;
        assert_eq!(filter.query, "alice");
        assert_eq!(filter.plan_id, 7);
        assert_eq!(filter.plan_ids, vec![7, 8]);
        assert_eq!(filter.user_ids, vec![9, 10]);
        assert!(
            serde_json::from_str::<ResetPreviewRequest>(
                r#"{"mode":"hard","all_matching":true,"filter":{"plan_ids_typo":[7]}}"#
            )
            .is_err()
        );
        assert!(
            serde_json::from_str::<ResetExecuteRequest>(
                r#"{"preview_token":"p","operation_id":"o","mode":"hard"}"#
            )
            .is_err()
        );
        Ok(())
    }

    #[test]
    fn filter_id_limits_are_bounded() {
        assert!(normalize_ids(&vec![1; MAX_FILTER_IDS], "user").is_ok());
        assert!(normalize_ids(&vec![1; MAX_FILTER_IDS + 1], "user").is_err());
        assert!(normalize_ids(&[0], "plan").is_err());
        assert!(parse_query_ids(Some("1,2,2,3")).is_ok());
        assert!(parse_query_ids(Some("1,invalid")).is_err());
    }

    #[test]
    fn reset_totals_reject_overflow() {
        assert!(checked_reset_add(i64::MAX, 1).is_err());
    }

    #[test]
    fn soft_voucher_expiry_is_one_calendar_month() {
        let january_31 = Utc
            .with_ymd_and_hms(2024, 1, 31, 12, 0, 0)
            .single()
            .map(|value| value.timestamp());
        let february_29 = Utc
            .with_ymd_and_hms(2024, 2, 29, 12, 0, 0)
            .single()
            .map(|value| value.timestamp());
        assert_eq!(
            january_31.and_then(|value| one_calendar_month(value).ok()),
            february_29
        );
    }
}
