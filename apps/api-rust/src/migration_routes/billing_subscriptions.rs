//! Legacy-compatible subscription administration without payment-provider calls.
//!
//! This module is deliberately self-contained so it can be mounted during the
//! staged migration.  PostgreSQL is authoritative; Valkey only holds derived
//! plan views and is evicted after a successful commit.

use crate::auth::{AuthErrorKind, DashboardAuth, DashboardUser};
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{Path, Request, State},
    http::{HeaderMap, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{delete, get, post, put},
};
use chrono::Duration as ChronoDuration;
use chrono::{DateTime, Datelike, Local, LocalResult, NaiveDateTime, TimeZone};
use secrecy::SecretString;
use serde::{Deserialize, Serialize, Serializer};
use serde_json::{Value, json};
use sqlx::{PgPool, Postgres, Row, Transaction};
use std::{
    collections::{BTreeSet, HashMap},
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};
use tokio::{
    task::JoinHandle,
    time::{self, Duration as TokioDuration},
};

const PLAN_CACHE_PREFIX: &str = "new-api:subscription_plan:v1:";
const PLAN_INFO_CACHE_PREFIX: &str = "new-api:subscription_plan_info:v1:";
const ADMIN_ROLE: i64 = 10;
const USER_CACHE_PREFIX: &str = "user:";
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";

#[derive(Clone)]
pub struct BillingSubscriptionsState {
    pg: PgPool,
    valkey: Option<redis::Client>,
    auth: Arc<dyn DashboardAuth>,
    console_access_gate: bool,
}

impl BillingSubscriptionsState {
    #[must_use]
    pub fn new(pg: PgPool, valkey: Option<redis::Client>, auth: Arc<dyn DashboardAuth>) -> Self {
        Self {
            pg,
            valkey,
            auth,
            console_access_gate: false,
        }
    }

    /// Enables the API-wide ConsoleAccessGate used by the normal listener.
    /// Module-level contract tests leave this disabled so they can continue
    /// to exercise the route's direct UserAuth envelope in isolation.
    #[must_use]
    pub fn with_console_access_gate(mut self) -> Self {
        self.console_access_gate = true;
        self
    }
}

/// Start the Go-compatible subscription maintenance loop for the process
/// elected as the database master.  The loop is deliberately opt-out for
/// `NODE_TYPE=slave`, matching Go's `common.IsMasterNode` decision, and is
/// disabled for the isolated Rust test instance so fixtures remain explicit.
pub fn spawn_maintenance(pg: PgPool, valkey: Option<redis::Client>) -> Option<JoinHandle<()>> {
    if std::env::var("NODE_TYPE").ok().as_deref() == Some("slave")
        || std::env::var("LMM_RS_TEST_INSTANCE").ok().as_deref() == Some("1")
    {
        return None;
    }
    Some(tokio::spawn(async move {
        let mut ticker = time::interval(TokioDuration::from_secs(60));
        let mut last_cleanup = 0_i64;
        loop {
            ticker.tick().await;
            let current = match database_timestamp(&pg).await {
                Ok(current) => current,
                Err(error) => {
                    tracing::warn!(%error, "subscription maintenance clock query failed");
                    continue;
                }
            };
            let cleanup = current.saturating_sub(last_cleanup) >= 30 * 60;
            match maintenance_once_at(&pg, valkey.as_ref(), current, cleanup).await {
                Ok((expired, reset, cleaned)) => {
                    if cleanup {
                        last_cleanup = current;
                    }
                    if expired > 0 || reset > 0 || cleaned > 0 {
                        tracing::debug!(
                            expired,
                            reset,
                            cleaned,
                            "subscription maintenance completed"
                        );
                    }
                }
                Err(error) => {
                    tracing::warn!(%error, "subscription maintenance failed");
                }
            }
        }
    }))
}

/// Execute one maintenance pass.  This public seam is used by the isolated
/// PostgreSQL/Valkey integration proof; the production loop above calls the
/// same implementation with the Go-compatible 60-second cadence.
pub async fn maintenance_once(
    pg: &PgPool,
    valkey: Option<&redis::Client>,
) -> Result<(), sqlx::Error> {
    let current = database_timestamp(pg).await?;
    maintenance_once_at(pg, valkey, current, true)
        .await
        .map(|_| ())
}

async fn database_timestamp(pg: &PgPool) -> Result<i64, sqlx::Error> {
    sqlx::query_scalar("SELECT EXTRACT(EPOCH FROM NOW())::BIGINT")
        .fetch_one(pg)
        .await
}

async fn maintenance_once_at(
    pg: &PgPool,
    valkey: Option<&redis::Client>,
    current: i64,
    cleanup: bool,
) -> Result<(i64, i64, i64), sqlx::Error> {
    let mut expired = 0_i64;
    loop {
        let count = expire_due_subscriptions(pg, valkey, current, 300).await?;
        expired = expired.saturating_add(count);
        if count < 300 {
            break;
        }
    }

    let mut reset = 0_i64;
    loop {
        let count = reset_due_subscriptions(pg, current, 300).await?;
        reset = reset.saturating_add(count);
        if count < 300 {
            break;
        }
    }

    let cleaned = if cleanup {
        let cutoff = current.saturating_sub(7 * 24 * 60 * 60);
        sqlx::query("DELETE FROM subscription_pre_consume_records WHERE updated_at < $1")
            .bind(cutoff)
            .execute(pg)
            .await?
            .rows_affected() as i64
    } else {
        0
    };
    Ok((expired, reset, cleaned))
}

async fn expire_due_subscriptions(
    pg: &PgPool,
    valkey: Option<&redis::Client>,
    current: i64,
    limit: i64,
) -> Result<i64, sqlx::Error> {
    let candidate_users = sqlx::query(
        "SELECT user_id FROM user_subscriptions WHERE status='active' AND end_time > 0 AND end_time <= $1 ORDER BY end_time ASC, id ASC LIMIT $2",
    )
    .bind(current)
    .bind(limit)
    .fetch_all(pg)
    .await?
    .into_iter()
    .map(|row| row.try_get::<i64, _>("user_id"))
    .collect::<Result<BTreeSet<_>, _>>()?;

    let mut expired_count = 0_i64;
    for user_id in candidate_users {
        let mut tx = pg.begin().await?;
        let changed = sqlx::query(
            "UPDATE user_subscriptions SET status='expired',updated_at=$2 WHERE user_id=$1 AND status='active' AND end_time > 0 AND end_time <= $2",
        )
        .bind(user_id)
        .bind(current)
        .execute(&mut *tx)
        .await?
        .rows_affected() as i64;
        if changed == 0 {
            tx.commit().await?;
            continue;
        }
        expired_count = expired_count.saturating_add(changed);

        let active_upgrade: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM user_subscriptions WHERE user_id=$1 AND status='active' AND end_time>$2 AND upgrade_group<>'')",
        )
        .bind(user_id)
        .bind(current)
        .fetch_one(&mut *tx)
        .await?;
        let mut cache_group = None;
        if !active_upgrade
            && let Some(row) = sqlx::query(
                "SELECT COALESCE(downgrade_group,'') AS downgrade_group, COALESCE(upgrade_group,'') AS upgrade_group, COALESCE(prev_user_group,'') AS prev_user_group FROM user_subscriptions WHERE user_id=$1 AND status='expired' AND (downgrade_group<>'' OR upgrade_group<>'') ORDER BY end_time DESC,id DESC LIMIT 1",
            )
            .bind(user_id)
            .fetch_optional(&mut *tx)
            .await?
            {
                let current_group: String = sqlx::query_scalar(
                    "SELECT \"group\" FROM users WHERE id=$1 FOR UPDATE",
                )
                .bind(user_id)
                .fetch_one(&mut *tx)
                .await?;
                let explicit_downgrade: String = row.try_get("downgrade_group")?;
                let upgrade_group: String = row.try_get("upgrade_group")?;
                let previous_group: String = row.try_get("prev_user_group")?;
                let target = if !explicit_downgrade.trim().is_empty() {
                    explicit_downgrade.trim().to_owned()
                } else if !upgrade_group.trim().is_empty()
                    && !previous_group.trim().is_empty()
                    && current_group == upgrade_group.trim()
                {
                    previous_group.trim().to_owned()
                } else {
                    String::new()
                };
                if !target.is_empty() && target != current_group {
                    sqlx::query("UPDATE users SET \"group\"=$2 WHERE id=$1")
                        .bind(user_id)
                        .bind(&target)
                        .execute(&mut *tx)
                        .await?;
                    cache_group = Some(target);
                }
            }
        tx.commit().await?;
        if cache_group.is_some() {
            evict_user_cache(valkey, user_id).await;
        }
    }
    Ok(expired_count)
}

async fn reset_due_subscriptions(
    pg: &PgPool,
    current: i64,
    limit: i64,
) -> Result<i64, sqlx::Error> {
    let candidates = sqlx::query(
        "SELECT id, plan_id FROM user_subscriptions WHERE next_reset_time > 0 AND next_reset_time <= $1 AND status='active' ORDER BY next_reset_time ASC, id ASC LIMIT $2",
    )
    .bind(current)
    .bind(limit)
    .fetch_all(pg)
    .await?;
    let mut reset_count = 0_i64;
    for candidate in candidates {
        let id: i64 = candidate.try_get("id")?;
        let plan_id: i64 = candidate.try_get("plan_id")?;
        let plan = match plan(pg, plan_id).await {
            Ok(plan) => plan,
            Err(_) => continue,
        };
        let mut tx = pg.begin().await?;
        let Some(row) = sqlx::query(&format!(
            "{SUB_SELECT} WHERE id=$1 AND next_reset_time > 0 AND next_reset_time <= $2 FOR UPDATE"
        ))
        .bind(id)
        .bind(current)
        .fetch_optional(&mut *tx)
        .await?
        else {
            tx.commit().await?;
            continue;
        };
        let subscription = subscription_from_row(&row)?;
        maybe_reset_subscription(&mut tx, &subscription, &plan, current).await?;
        tx.commit().await?;
        reset_count = reset_count.saturating_add(1);
    }
    Ok(reset_count)
}

async fn maybe_reset_subscription(
    tx: &mut Transaction<'_, Postgres>,
    subscription: &Subscription,
    plan: &Plan,
    current: i64,
) -> Result<(), sqlx::Error> {
    if subscription.next_reset_time > 0 && subscription.next_reset_time > current {
        return Ok(());
    }
    if !matches!(
        plan.quota_reset_period.trim(),
        "daily" | "weekly" | "monthly" | "custom"
    ) {
        return Ok(());
    }
    let mut base = if subscription.last_reset_time > 0 {
        subscription.last_reset_time
    } else {
        subscription.start_time
    };
    let mut next = next_reset(base, subscription.end_time, plan);
    let mut advanced = false;
    while next > 0 && next <= current {
        advanced = true;
        base = next;
        next = next_reset(base, subscription.end_time, plan);
    }
    if !advanced {
        if subscription.next_reset_time == 0 && next > 0 {
            sqlx::query(
                "UPDATE user_subscriptions SET last_reset_time=$2,next_reset_time=$3,updated_at=$4 WHERE id=$1",
            )
            .bind(subscription.id)
            .bind(base)
            .bind(next)
            .bind(current)
            .execute(&mut **tx)
            .await?;
        }
        return Ok(());
    }
    sqlx::query(
        "UPDATE user_subscriptions SET amount_used=0,last_reset_time=$2,next_reset_time=$3,updated_at=$4 WHERE id=$1",
    )
    .bind(subscription.id)
    .bind(base)
    .bind(next)
    .bind(current)
    .execute(&mut **tx)
    .await?;
    Ok(())
}

/// Routes that do not initiate payments or accept provider callbacks.
pub fn router(state: BillingSubscriptionsState) -> Router {
    let protected = Router::new()
        .route("/api/subscription/plans", get(list_enabled_plans))
        .route("/api/subscription/self", get(subscription_self))
        .route("/api/subscription/self/preference", put(update_preference))
        .route(
            "/api/subscription/admin/plans",
            get(admin_list_plans).post(admin_create_plan),
        )
        .route(
            "/api/subscription/admin/plans/{id}",
            put(admin_update_plan)
                .delete(admin_delete_plan)
                .patch(admin_update_plan_status),
        )
        .route(
            "/api/subscription/admin/plans/{id}/subscriptions/reset",
            post(admin_reset_plan),
        )
        .route(
            "/api/subscription/admin/bind",
            post(admin_bind_subscription),
        )
        .route(
            "/api/subscription/admin/users/{id}/subscriptions",
            get(admin_list_user_subscriptions).post(admin_create_user_subscription),
        )
        .route(
            "/api/subscription/admin/users/{id}/subscriptions/reset",
            post(admin_reset_user_subscriptions),
        )
        .route(
            "/api/subscription/admin/user_subscriptions/{id}",
            delete(admin_delete_subscription),
        )
        .route(
            "/api/subscription/admin/user_subscriptions/{id}/invalidate",
            post(admin_invalidate_subscription),
        )
        // Go's user/admin middleware runs before Gin binds JSON. Keep that
        // ordering at the listener boundary so malformed anonymous writes
        // cannot become Axum extractor errors before auth is evaluated.
        .route_layer(middleware::from_fn_with_state(
            state.clone(),
            subscription_auth_boundary,
        ));
    protected.with_state(state)
}

async fn subscription_auth_boundary(
    State(state): State<BillingSubscriptionsState>,
    request: Request,
    next: Next,
) -> Response {
    if !subscription_method_allowed(request.uri().path(), request.method()) {
        return next.run(request).await;
    }
    if state.console_access_gate
        && let Err(response) = console_access(&state, request.headers()).await
    {
        return response;
    }
    let is_admin = request.uri().path().starts_with("/api/subscription/admin/");
    let result = if is_admin {
        admin(&state, request.headers()).await.map(|_| ())
    } else {
        identity(&state, request.headers()).await.map(|_| ())
    };
    if let Err(response) = result {
        return response;
    }
    // Gin's UserAuth/AdminAuth middleware adds the current auth-version to
    // every response after authentication, including extractor and handler
    // failures. Apply it at the boundary so early returns cannot drift from
    // the Go contract.
    with_auth_version(next.run(request).await)
}

/// Mirrors the API-wide Go ConsoleAccessGate for the normal listener.  The
/// detailed UserAuth/AdminAuth response remains owned by `identity`/`admin`
/// after this discovery check has admitted the request.
async fn console_access(
    state: &BillingSubscriptionsState,
    headers: &HeaderMap,
) -> Result<(), Response> {
    let token = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| {
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
        })
        .ok_or_else(console_not_found)?;
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(token))
        .await
        .map_err(|_| console_not_found())?;
    user.developer_access_granted
        .then_some(())
        .ok_or_else(console_not_found)
}

fn subscription_method_allowed(path: &str, method: &axum::http::Method) -> bool {
    use axum::http::Method;

    match path {
        "/api/subscription/plans" | "/api/subscription/self" => *method == Method::GET,
        "/api/subscription/self/preference" => *method == Method::PUT,
        "/api/subscription/admin/plans" => *method == Method::GET || *method == Method::POST,
        "/api/subscription/admin/bind" => *method == Method::POST,
        _ if path.ends_with("/subscriptions/reset") => *method == Method::POST,
        _ if path.ends_with("/invalidate") => *method == Method::POST,
        _ if path.starts_with("/api/subscription/admin/plans/") => {
            *method == Method::PUT || *method == Method::DELETE || *method == Method::PATCH
        }
        _ if path.starts_with("/api/subscription/admin/users/") => {
            *method == Method::GET || *method == Method::POST
        }
        _ if path.starts_with("/api/subscription/admin/user_subscriptions/") => {
            *method == Method::DELETE
        }
        _ => false,
    }
}

#[derive(Serialize)]
struct Envelope<T: Serialize> {
    success: bool,
    message: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<T>,
}

fn ok<T: Serialize>(data: T) -> Response {
    Json(Envelope {
        success: true,
        message: "",
        data: Some(data),
    })
    .into_response()
}
fn empty_ok() -> Response {
    Json(Envelope::<()> {
        success: true,
        message: "",
        data: None,
    })
    .into_response()
}
fn failure(status: StatusCode, message: &'static str) -> Response {
    // Gin's ApiError and ApiErrorMsg deliberately use HTTP 200 for business
    // failures. Authentication middleware is the only caller that supplies a
    // non-200 status to this module.
    let status = match status {
        StatusCode::UNAUTHORIZED | StatusCode::FORBIDDEN => status,
        _ => StatusCode::OK,
    };
    (
        status,
        Json(Envelope::<()> {
            success: false,
            message,
            data: None,
        }),
    )
        .into_response()
}

fn with_auth_version(mut response: Response) -> Response {
    response.headers_mut().insert(
        "auth-version",
        axum::http::HeaderValue::from_static(AUTH_VERSION),
    );
    response
}

fn auth_failure(status: StatusCode, code: &'static str, message: &'static str) -> Response {
    (
        status,
        Json(json!({"success": false, "code": code, "message": message})),
    )
        .into_response()
}

fn console_not_found() -> Response {
    (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response()
}

async fn identity(
    state: &BillingSubscriptionsState,
    headers: &HeaderMap,
) -> Result<DashboardUser, Response> {
    let Some(value) = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.strip_prefix("Bearer "))
        .filter(|value| !value.is_empty())
    else {
        return Err(failure(
            StatusCode::UNAUTHORIZED,
            "Unauthorized, invalid access token",
        ));
    };
    let user = state
        .auth
        .self_user(SecretString::from(value.to_owned()))
        .await
        .map_err(|error| match error.kind {
            AuthErrorKind::Unauthorized
            | AuthErrorKind::TokenExpired
            | AuthErrorKind::SessionRevoked => auth_failure(
                StatusCode::UNAUTHORIZED,
                "AUTH_UNAUTHORIZED",
                "Unauthorized, invalid access token",
            ),
            _ => auth_failure(
                StatusCode::INTERNAL_SERVER_ERROR,
                "AUTH_INTERNAL_ERROR",
                "Database error, please contact the administrator",
            ),
        })?;
    if user.status != 1 {
        return Err(auth_failure(
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_DISABLED",
            "User has been banned",
        ));
    }
    if user.username.trim().is_empty() || !matches!(user.role, 0 | 1 | 10 | 100) {
        return Err(auth_failure(
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_INVALID",
            "Unauthorized, invalid user info",
        ));
    }
    if user.role < 1 {
        return Err(auth_failure(
            StatusCode::FORBIDDEN,
            "AUTH_INSUFFICIENT_PRIVILEGE",
            "Unauthorized, insufficient privileges",
        ));
    }
    Ok(user)
}

async fn admin(
    state: &BillingSubscriptionsState,
    headers: &HeaderMap,
) -> Result<DashboardUser, Response> {
    let user = identity(state, headers).await?;
    if user.role < ADMIN_ROLE {
        return Err(auth_failure(
            StatusCode::FORBIDDEN,
            "AUTH_INSUFFICIENT_PRIVILEGE",
            "Unauthorized, insufficient privileges",
        ));
    }
    Ok(user)
}

pub(crate) async fn payment_compliance_confirmed(pg: &PgPool) -> Result<bool, sqlx::Error> {
    let rows = sqlx::query(
        "SELECT key, value FROM options WHERE key IN ('payment_setting.compliance_confirmed', 'payment_setting.compliance_terms_version', 'payment_setting')",
    )
    .fetch_all(pg)
    .await?;
    let values = rows
        .iter()
        .map(|row| {
            Ok((
                row.try_get::<String, _>("key")?,
                row.try_get::<String, _>("value")?,
            ))
        })
        .collect::<Result<HashMap<_, _>, sqlx::Error>>()?;
    let split_options = values
        .get("payment_setting.compliance_confirmed")
        .is_some_and(|value| value.eq_ignore_ascii_case("true") || value == "1")
        && values
            .get("payment_setting.compliance_terms_version")
            .is_some_and(|value| value == "v1");
    Ok(split_options
        || values
            .get("payment_setting")
            .and_then(|value| serde_json::from_str::<Value>(value).ok())
            .is_some_and(|value| {
                value
                    .get("compliance_confirmed")
                    .and_then(Value::as_bool)
                    .unwrap_or(false)
                    && value
                        .get("compliance_terms_version")
                        .and_then(Value::as_str)
                        == Some("v1")
            }))
}

fn compliance_message(headers: &HeaderMap) -> &'static str {
    let locale = headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default()
        .to_ascii_lowercase();
    if locale.starts_with("zh-tw") {
        "支付、兌換碼、訂閱方案和邀請返利功能已停用。管理員需先確認合規聲明後方可啟用。"
    } else if locale.starts_with("zh") {
        "支付、兑换码、订阅计划和邀请返利功能已禁用。管理员需先确认合规声明后方可启用。"
    } else {
        "Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them."
    }
}

async fn require_payment_compliance(
    state: &BillingSubscriptionsState,
    headers: &HeaderMap,
) -> Result<(), Response> {
    match payment_compliance_confirmed(&state.pg).await {
        Ok(true) => Ok(()),
        Ok(false) => Err(failure(StatusCode::OK, compliance_message(headers))),
        Err(_) => Err(failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误")),
    }
}

/// Preserve the mutation gate ordering: console access runs in middleware,
/// followed by administrator authentication and then payment compliance.
async fn authorize_admin_write(
    state: &BillingSubscriptionsState,
    headers: &HeaderMap,
) -> Result<DashboardUser, Response> {
    let administrator = admin(state, headers).await?;
    require_payment_compliance(state, headers)
        .await
        .map_err(with_auth_version)?;
    Ok(administrator)
}

async fn authorize_admin_id(
    state: &BillingSubscriptionsState,
    headers: &HeaderMap,
    id: i64,
    invalid_message: &'static str,
) -> Result<(), Response> {
    admin(state, headers).await?;
    if id <= 0 {
        return Err(with_auth_version(failure(
            StatusCode::BAD_REQUEST,
            invalid_message,
        )));
    }
    Ok(())
}

#[derive(Clone, Debug, Serialize)]
struct Plan {
    id: i64,
    title: String,
    subtitle: String,
    #[serde(serialize_with = "serialize_legacy_number")]
    price_amount: f64,
    currency: String,
    duration_unit: String,
    duration_value: i64,
    custom_seconds: i64,
    enabled: bool,
    sort_order: i64,
    allow_balance_pay: bool,
    allow_wallet_overflow: bool,
    stripe_price_id: String,
    creem_product_id: String,
    waffo_pancake_product_id: String,
    max_purchase_per_user: i64,
    total_amount: i64,
    upgrade_group: String,
    downgrade_group: String,
    quota_reset_period: String,
    quota_reset_custom_seconds: i64,
    created_at: i64,
    updated_at: i64,
}

/// Match Go's `encoding/json` spelling for integral `float64` values while
/// preserving fractional subscription prices exactly.
fn serialize_legacy_number<S>(value: &f64, serializer: S) -> Result<S::Ok, S::Error>
where
    S: Serializer,
{
    if value.is_finite()
        && value.fract() == 0.0
        && *value >= i64::MIN as f64
        && *value <= i64::MAX as f64
    {
        serializer.serialize_i64(*value as i64)
    } else {
        serializer.serialize_f64(*value)
    }
}

#[derive(Serialize)]
pub(crate) struct PlanView {
    plan: Plan,
}
#[derive(Deserialize)]
struct PlanRequest {
    plan: PlanInput,
}
#[derive(Deserialize)]
struct PlanInput {
    title: String,
    #[serde(default)]
    subtitle: String,
    price_amount: f64,
    #[serde(default)]
    currency: String,
    #[serde(default)]
    duration_unit: String,
    #[serde(default)]
    duration_value: i64,
    #[serde(default)]
    custom_seconds: i64,
    #[serde(default = "true_value")]
    enabled: bool,
    #[serde(default)]
    sort_order: i64,
    allow_balance_pay: Option<bool>,
    allow_wallet_overflow: Option<bool>,
    #[serde(default)]
    stripe_price_id: String,
    #[serde(default)]
    creem_product_id: String,
    #[serde(default)]
    waffo_pancake_product_id: String,
    #[serde(default)]
    max_purchase_per_user: i64,
    #[serde(default)]
    total_amount: i64,
    #[serde(default)]
    upgrade_group: String,
    #[serde(default)]
    downgrade_group: String,
    #[serde(default)]
    quota_reset_period: String,
    #[serde(default)]
    quota_reset_custom_seconds: i64,
}
fn true_value() -> bool {
    true
}

impl PlanInput {
    fn normalize(mut self) -> Result<Self, &'static str> {
        self.title = self.title.trim().to_owned();
        self.upgrade_group = self.upgrade_group.trim().to_owned();
        self.downgrade_group = self.downgrade_group.trim().to_owned();
        if self.title.is_empty() {
            return Err("套餐标题不能为空");
        }
        if !(0.0..=9999.0).contains(&self.price_amount) {
            return Err(if self.price_amount < 0.0 {
                "价格不能为负数"
            } else {
                "价格不能超过9999"
            });
        }
        if self.max_purchase_per_user < 0 {
            return Err("购买上限不能为负数");
        }
        if self.total_amount < 0 {
            return Err("总额度不能为负数");
        }
        self.currency = "USD".to_owned();
        if self.duration_unit.is_empty() {
            self.duration_unit = "month".to_owned();
        }
        if !matches!(
            self.duration_unit.as_str(),
            "year" | "month" | "day" | "hour" | "custom"
        ) {
            return Err("无效的订阅周期");
        }
        if self.duration_unit == "custom" {
            if self.custom_seconds <= 0 {
                return Err("自定义时长需大于0秒");
            }
        } else if self.duration_value <= 0 {
            self.duration_value = 1;
        }
        self.quota_reset_period = match self.quota_reset_period.trim() {
            "daily" => "daily",
            "weekly" => "weekly",
            "monthly" => "monthly",
            "custom" => "custom",
            _ => "never",
        }
        .to_owned();
        if self.quota_reset_period == "custom" && self.quota_reset_custom_seconds <= 0 {
            return Err("自定义重置周期需大于0秒");
        }
        Ok(self)
    }
}

async fn validated_plan_input(pg: &PgPool, request: PlanRequest) -> Result<PlanInput, Response> {
    let input = request
        .plan
        .normalize()
        .map_err(|message| failure(StatusCode::BAD_REQUEST, message))?;
    validate_plan_groups(pg, &input)
        .await
        .map_err(|message| with_auth_version(failure(StatusCode::BAD_REQUEST, message)))?;
    Ok(input)
}

async fn validate_plan_groups(pg: &PgPool, input: &PlanInput) -> Result<(), &'static str> {
    if input.upgrade_group.is_empty() && input.downgrade_group.is_empty() {
        return Ok(());
    }
    let groups: Option<String> =
        sqlx::query_scalar("SELECT value FROM options WHERE key='GroupRatio'")
            .fetch_optional(pg)
            .await
            .map_err(|_| "系统错误")?;
    let groups = groups
        .and_then(|value| serde_json::from_str::<HashMap<String, Value>>(&value).ok())
        .ok_or("升级分组不存在")?;
    if !input.upgrade_group.is_empty() && !groups.contains_key(&input.upgrade_group) {
        return Err("升级分组不存在");
    }
    if !input.downgrade_group.is_empty() && !groups.contains_key(&input.downgrade_group) {
        return Err("降级分组不存在");
    }
    Ok(())
}

async fn list_enabled_plans(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = identity(&state, &headers).await {
        return response;
    }
    match payment_compliance_confirmed(&state.pg).await {
        Ok(false) => return with_auth_version(ok(Vec::<PlanView>::new())),
        Err(_) => {
            return with_auth_version(failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"));
        }
        Ok(true) => {}
    }
    plan_list_response(plans(&state.pg, true).await)
}
async fn admin_list_plans(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = admin(&state, &headers).await {
        return response;
    }
    plan_list_response(plans(&state.pg, false).await)
}

fn plan_list_response(result: Result<Vec<Plan>, sqlx::Error>) -> Response {
    with_auth_version(
        result
            .map(|plans| {
                ok(plans
                    .into_iter()
                    .map(|plan| PlanView { plan })
                    .collect::<Vec<_>>())
            })
            .unwrap_or_else(|_| failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误")),
    )
}

async fn plans(pg: &PgPool, enabled_only: bool) -> Result<Vec<Plan>, sqlx::Error> {
    let sql = if enabled_only {
        format!("{PLAN_SELECT} WHERE enabled = TRUE ORDER BY sort_order DESC, id DESC")
    } else {
        format!("{PLAN_SELECT} ORDER BY sort_order DESC, id DESC")
    };
    sqlx::query(&sql)
        .fetch_all(pg)
        .await?
        .iter()
        .map(plan_from_row)
        .collect()
}

pub(crate) async fn enabled_plan_views(pg: &PgPool) -> Result<Vec<PlanView>, sqlx::Error> {
    plans(pg, true)
        .await
        .map(|plans| plans.into_iter().map(|plan| PlanView { plan }).collect())
}
const PLAN_SELECT: &str = "SELECT id, title, COALESCE(subtitle, '') subtitle, price_amount::FLOAT8 price_amount, COALESCE(currency, 'USD') currency, COALESCE(duration_unit, 'month') duration_unit, COALESCE(duration_value, 1) duration_value, COALESCE(custom_seconds, 0) custom_seconds, enabled, COALESCE(sort_order, 0) sort_order, COALESCE(allow_balance_pay, TRUE) allow_balance_pay, COALESCE(allow_wallet_overflow, TRUE) allow_wallet_overflow, COALESCE(stripe_price_id, '') stripe_price_id, COALESCE(creem_product_id, '') creem_product_id, COALESCE(waffo_pancake_product_id, '') waffo_pancake_product_id, COALESCE(max_purchase_per_user, 0) max_purchase_per_user, COALESCE(total_amount, 0) total_amount, COALESCE(upgrade_group, '') upgrade_group, COALESCE(downgrade_group, '') downgrade_group, COALESCE(quota_reset_period, 'never') quota_reset_period, COALESCE(quota_reset_custom_seconds, 0) quota_reset_custom_seconds, COALESCE(created_at, 0) created_at, COALESCE(updated_at, 0) updated_at FROM subscription_plans";

fn bind_plan_input<'q>(
    query: sqlx::query::Query<'q, Postgres, sqlx::postgres::PgArguments>,
    input: &'q PlanInput,
    allow_balance_pay: Option<bool>,
    allow_wallet_overflow: Option<bool>,
) -> sqlx::query::Query<'q, Postgres, sqlx::postgres::PgArguments> {
    query
        .bind(&input.title)
        .bind(&input.subtitle)
        .bind(input.price_amount)
        .bind(&input.currency)
        .bind(&input.duration_unit)
        .bind(input.duration_value)
        .bind(input.custom_seconds)
        .bind(input.enabled)
        .bind(input.sort_order)
        .bind(allow_balance_pay)
        .bind(allow_wallet_overflow)
        .bind(&input.stripe_price_id)
        .bind(&input.creem_product_id)
        .bind(&input.waffo_pancake_product_id)
        .bind(input.max_purchase_per_user)
        .bind(input.total_amount)
        .bind(&input.upgrade_group)
        .bind(&input.downgrade_group)
        .bind(&input.quota_reset_period)
        .bind(input.quota_reset_custom_seconds)
}

fn plan_from_row(row: &sqlx::postgres::PgRow) -> Result<Plan, sqlx::Error> {
    Ok(Plan {
        id: row.try_get("id")?,
        title: row.try_get("title")?,
        subtitle: row.try_get("subtitle")?,
        price_amount: row.try_get("price_amount")?,
        currency: row.try_get("currency")?,
        duration_unit: row.try_get("duration_unit")?,
        duration_value: row.try_get("duration_value")?,
        custom_seconds: row.try_get("custom_seconds")?,
        enabled: row.try_get("enabled")?,
        sort_order: row.try_get("sort_order")?,
        allow_balance_pay: row.try_get("allow_balance_pay")?,
        allow_wallet_overflow: row.try_get("allow_wallet_overflow")?,
        stripe_price_id: row.try_get("stripe_price_id")?,
        creem_product_id: row.try_get("creem_product_id")?,
        waffo_pancake_product_id: row.try_get("waffo_pancake_product_id")?,
        max_purchase_per_user: row.try_get("max_purchase_per_user")?,
        total_amount: row.try_get("total_amount")?,
        upgrade_group: row.try_get("upgrade_group")?,
        downgrade_group: row.try_get("downgrade_group")?,
        quota_reset_period: row.try_get("quota_reset_period")?,
        quota_reset_custom_seconds: row.try_get("quota_reset_custom_seconds")?,
        created_at: row.try_get("created_at")?,
        updated_at: row.try_get("updated_at")?,
    })
}

async fn admin_create_plan(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
    Json(input): Json<PlanRequest>,
) -> Response {
    if let Err(response) = authorize_admin_write(&state, &headers).await {
        return response;
    }
    let input = match validated_plan_input(&state.pg, input).await {
        Ok(input) => input,
        Err(response) => return response,
    };
    let now = now();
    let row = bind_plan_input(
        sqlx::query("INSERT INTO subscription_plans (title, subtitle, price_amount, currency, duration_unit, duration_value, custom_seconds, enabled, sort_order, allow_balance_pay, allow_wallet_overflow, stripe_price_id, creem_product_id, waffo_pancake_product_id, max_purchase_per_user, total_amount, upgrade_group, downgrade_group, quota_reset_period, quota_reset_custom_seconds, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$21) RETURNING id"),
        &input,
        Some(input.allow_balance_pay.unwrap_or(true)),
        Some(input.allow_wallet_overflow.unwrap_or(true)),
    )
    .bind(now)
    .fetch_one(&state.pg)
    .await;
    match row {
        Ok(row) => {
            let id: i64 = match row.try_get("id") {
                Ok(id) => id,
                Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
            };
            evict_plan(&state, id).await;
            with_auth_version(match plan(&state.pg, id).await {
                Ok(plan) => ok(plan),
                Err(_) => failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
            })
        }
        Err(_) => failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
    }
}

async fn admin_update_plan(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
    Path(id): Path<i64>,
    Json(input): Json<PlanRequest>,
) -> Response {
    if let Err(response) = authorize_admin_write(&state, &headers).await {
        return response;
    }
    if id <= 0 {
        return failure(StatusCode::BAD_REQUEST, "无效的ID");
    }
    let input = match validated_plan_input(&state.pg, input).await {
        Ok(input) => input,
        Err(response) => return response,
    };
    let updated = bind_plan_input(
        sqlx::query("UPDATE subscription_plans SET title=$2, subtitle=$3, price_amount=$4, currency=$5, duration_unit=$6, duration_value=$7, custom_seconds=$8, enabled=$9, sort_order=$10, allow_balance_pay=COALESCE($11, allow_balance_pay), allow_wallet_overflow=COALESCE($12, allow_wallet_overflow), stripe_price_id=$13, creem_product_id=$14, waffo_pancake_product_id=$15, max_purchase_per_user=$16, total_amount=$17, upgrade_group=$18, downgrade_group=$19, quota_reset_period=$20, quota_reset_custom_seconds=$21, updated_at=$22 WHERE id=$1")
            .bind(id),
        &input,
        input.allow_balance_pay,
        input.allow_wallet_overflow,
    )
    .bind(now())
    .execute(&state.pg)
    .await;
    match updated {
        Ok(_) => {
            evict_plan(&state, id).await;
            with_auth_version(empty_ok())
        }
        Err(_) => failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
    }
}

#[derive(Deserialize)]
struct StatusRequest {
    enabled: Option<bool>,
}
async fn admin_update_plan_status(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
    Path(id): Path<i64>,
    Json(input): Json<StatusRequest>,
) -> Response {
    if let Err(response) = authorize_admin_write(&state, &headers).await {
        return response;
    }
    if id <= 0 {
        return with_auth_version(failure(StatusCode::BAD_REQUEST, "无效的ID"));
    }
    let Some(enabled) = input.enabled else {
        return failure(StatusCode::BAD_REQUEST, "参数错误");
    };
    match sqlx::query("UPDATE subscription_plans SET enabled=$2, updated_at=$3 WHERE id=$1")
        .bind(id)
        .bind(enabled)
        .bind(now())
        .execute(&state.pg)
        .await
    {
        Ok(_) => {
            evict_plan(&state, id).await;
            with_auth_version(empty_ok())
        }
        Err(_) => failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
    }
}

async fn admin_delete_plan(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
    Path(id): Path<i64>,
) -> Response {
    let administrator = match authorize_admin_write(&state, &headers).await {
        Ok(administrator) => administrator,
        Err(response) => return response,
    };
    if id <= 0 {
        return with_auth_version(failure(StatusCode::BAD_REQUEST, "无效的ID"));
    }

    let mut tx = match state.pg.begin().await {
        Ok(tx) => tx,
        Err(_) => {
            return with_auth_version(failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"));
        }
    };
    match sqlx::query_scalar::<_, i64>("SELECT id FROM subscription_plans WHERE id=$1 FOR UPDATE")
        .bind(id)
        .fetch_optional(&mut *tx)
        .await
    {
        Ok(Some(_)) => {}
        Ok(None) => {
            return with_auth_version(failure(
                StatusCode::BAD_REQUEST,
                "Subscription plan not found",
            ));
        }
        Err(_) => {
            return with_auth_version(failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"));
        }
    }

    match sqlx::query_scalar::<_, bool>(
        "SELECT EXISTS(SELECT 1 FROM user_subscriptions WHERE plan_id=$1) OR EXISTS(SELECT 1 FROM subscription_orders WHERE plan_id=$1)",
    )
    .bind(id)
    .fetch_one(&mut *tx)
    .await
    {
        Ok(true) => {
            return with_auth_version(failure(
                StatusCode::BAD_REQUEST,
                "Subscription plan has subscription or order history and cannot be deleted. Disable it instead.",
            ));
        }
        Ok(false) => {}
        Err(_) => {
            return with_auth_version(failure(
                StatusCode::INTERNAL_SERVER_ERROR,
                "系统错误",
            ));
        }
    }

    let audit = json!({
        "admin_info": {
            "admin_id": administrator.id,
            "admin_username": administrator.username,
            "admin_role": administrator.role,
            "auth_method": "session",
        },
        "audit_info": {
            "method": "DELETE",
            "route": "/api/subscription/admin/plans/:id",
            "path": format!("/api/subscription/admin/plans/{id}"),
            "status": 200,
            "success": true,
            "params": { "id": id.to_string() },
        },
        "op": {
            "action": "subscription.plan_delete",
            "params": { "plan_id": id },
        },
    })
    .to_string();
    let deleted = sqlx::query("DELETE FROM subscription_plans WHERE id=$1")
        .bind(id)
        .execute(&mut *tx)
        .await;
    let audited = sqlx::query(
        "INSERT INTO logs (user_id, created_at, type, content, username, ip, other) \
         VALUES ($1, EXTRACT(EPOCH FROM NOW())::BIGINT, 3, $2, $3, '', $4)",
    )
    .bind(administrator.id)
    .bind(format!("DELETE /api/subscription/admin/plans/{id}"))
    .bind(&administrator.username)
    .bind(audit)
    .execute(&mut *tx)
    .await;
    if deleted.is_err() || audited.is_err() || tx.commit().await.is_err() {
        return with_auth_version(failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"));
    }
    evict_plan(&state, id).await;
    with_auth_version(empty_ok())
}

#[derive(Deserialize)]
struct PreferenceRequest {
    billing_preference: String,
}

#[derive(Default, Deserialize, Serialize)]
struct LegacyUserSetting {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    notify_type: String,
    #[serde(default, skip_serializing_if = "is_zero_f64")]
    quota_warning_threshold: f64,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    webhook_url: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    webhook_secret: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    notification_email: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    bark_url: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    gotify_url: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    gotify_token: String,
    #[serde(default)]
    gotify_priority: i64,
    #[serde(default, skip_serializing_if = "is_false")]
    upstream_model_update_notify_enabled: bool,
    #[serde(
        default,
        rename = "accept_unset_model_ratio_model",
        skip_serializing_if = "is_false"
    )]
    accept_unset_ratio_model: bool,
    #[serde(default, skip_serializing_if = "is_false")]
    record_ip_log: bool,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    sidebar_modules: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    billing_preference: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    language: String,
}

fn is_zero_f64(value: &f64) -> bool {
    *value == 0.0
}

fn is_false(value: &bool) -> bool {
    !*value
}

/// Go's encoding/json writes an integral float64 without a trailing `.0`.
/// Preserve that lexical form because the legacy user setting is stored as a
/// JSON string and can be updated by more than one route family.
fn serialize_legacy_user_setting(setting: &LegacyUserSetting) -> Result<String, serde_json::Error> {
    let mut serialized = serde_json::to_string(setting)?;
    if let Some(marker) = serialized.find("\"quota_warning_threshold\":") {
        let value_start = marker + "\"quota_warning_threshold\":".len();
        let value_end = serialized[value_start..]
            .find([',', '}'])
            .map_or(serialized.len(), |offset| value_start + offset);
        let mut number = setting.quota_warning_threshold.to_string();
        if number.ends_with(".0") {
            number.truncate(number.len() - 2);
        }
        serialized.replace_range(value_start..value_end, &number);
    }
    Ok(serialized)
}

async fn update_preference(
    State(state): State<BillingSubscriptionsState>,
    request: Request,
) -> Response {
    let headers = request.headers().clone();
    let user = match identity(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return response,
    };
    let body = match to_bytes(request.into_body(), 1024 * 1024).await {
        Ok(body) => body,
        Err(_) => return with_auth_version(failure(StatusCode::OK, "参数错误")),
    };
    let input = match parse_preference_request(&body) {
        Ok(input) => input,
        Err(()) => return with_auth_version(failure(StatusCode::OK, "参数错误")),
    };
    let preference = normalize_preference(&input.billing_preference);
    let mut setting = serde_json::from_str::<LegacyUserSetting>(&user.setting).unwrap_or_default();
    setting.billing_preference = preference.to_owned();
    let setting = match serialize_legacy_user_setting(&setting) {
        Ok(setting) => setting,
        Err(_) => {
            return with_auth_version(failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"));
        }
    };
    let updated = sqlx::query("UPDATE users SET setting = $2 WHERE id = $1 AND deleted_at IS NULL")
        .bind(user.id)
        .bind(&setting)
        .execute(&state.pg)
        .await;
    match updated {
        Ok(result) if result.rows_affected() == 0 => {
            with_auth_version(failure(StatusCode::NOT_FOUND, "用户不存在"))
        }
        Ok(_) => {
            // Go updates an existing user hash in place after the durable
            // setting write. Keep the same cache-aside side effect while
            // preserving whichever legacy cache schema populated the hash
            // (the Rust model cache currently uses schema 2, while older Go
            // deployments may still have schema 4).
            update_user_setting_cache(&state, user.id, &setting).await;
            with_auth_version(ok(json!({"billing_preference": preference})))
        }
        Err(_) => with_auth_version(failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误")),
    }
}

/// Gin's `ShouldBindJSON` binds into a struct with a zero-value string field:
/// an omitted or null `billing_preference` is accepted and normalized, unknown
/// fields are ignored, while a non-string field or a non-object JSON value is
/// rejected with the legacy HTTP-200 error envelope. Axum's `Json<T>` extractor
/// has different status/error semantics, so perform that small compatibility
/// decode after the dashboard auth boundary has run.
fn parse_preference_request(body: &[u8]) -> Result<PreferenceRequest, ()> {
    let value: Value = serde_json::from_slice(body).map_err(|_| ())?;
    match value {
        Value::Null => Ok(PreferenceRequest {
            billing_preference: String::new(),
        }),
        Value::Object(mut object) => match object.remove("billing_preference") {
            None | Some(Value::Null) => Ok(PreferenceRequest {
                billing_preference: String::new(),
            }),
            Some(Value::String(billing_preference)) => Ok(PreferenceRequest { billing_preference }),
            Some(_) => Err(()),
        },
        _ => Err(()),
    }
}
fn normalize_preference(value: &str) -> &'static str {
    match value.trim() {
        "subscription_first" => "subscription_first",
        "wallet_first" => "wallet_first",
        "subscription_only" => "subscription_only",
        "wallet_only" => "wallet_only",
        _ => "subscription_first",
    }
}

async fn subscription_self(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
) -> Response {
    let user = match identity(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return response,
    };
    let preference = serde_json::from_str::<Value>(&user.setting)
        .ok()
        .and_then(|setting| {
            setting
                .get("billing_preference")
                .and_then(Value::as_str)
                .map(normalize_preference)
        })
        .unwrap_or("subscription_first");
    // Go's GetSubscriptionSelf deliberately degrades each read failure to an
    // empty list after UserAuth has succeeded. Preserve that wire contract so
    // a transient subscription-table failure does not turn a dashboard read
    // into a different HTTP/error envelope on Rust.
    let all = subscriptions(&state.pg, user.id, false)
        .await
        .unwrap_or_default();
    let active = subscriptions(&state.pg, user.id, true)
        .await
        .unwrap_or_default();
    with_auth_version(ok(
        json!({"billing_preference": preference, "subscriptions": active, "all_subscriptions": all}),
    ))
}

#[derive(Serialize)]
struct Subscription {
    id: i64,
    user_id: i64,
    plan_id: i64,
    amount_total: i64,
    amount_used: i64,
    start_time: i64,
    end_time: i64,
    status: String,
    source: String,
    last_reset_time: i64,
    next_reset_time: i64,
    upgrade_group: String,
    prev_user_group: String,
    downgrade_group: String,
    allow_wallet_overflow: bool,
    created_at: i64,
    updated_at: i64,
}
#[derive(Serialize)]
struct SubscriptionView {
    subscription: Subscription,
}
const SUB_SELECT: &str = "SELECT id,user_id,plan_id,COALESCE(amount_total,0) amount_total,COALESCE(amount_used,0) amount_used,COALESCE(start_time,0) start_time,COALESCE(end_time,0) end_time,status,COALESCE(source,'') source,COALESCE(last_reset_time,0) last_reset_time,COALESCE(next_reset_time,0) next_reset_time,COALESCE(upgrade_group,'') upgrade_group,COALESCE(prev_user_group,'') prev_user_group,COALESCE(downgrade_group,'') downgrade_group,COALESCE(allow_wallet_overflow,TRUE) allow_wallet_overflow,COALESCE(created_at,0) created_at,COALESCE(updated_at,0) updated_at FROM user_subscriptions";
fn subscription_from_row(row: &sqlx::postgres::PgRow) -> Result<Subscription, sqlx::Error> {
    Ok(Subscription {
        id: row.try_get("id")?,
        user_id: row.try_get("user_id")?,
        plan_id: row.try_get("plan_id")?,
        amount_total: row.try_get("amount_total")?,
        amount_used: row.try_get("amount_used")?,
        start_time: row.try_get("start_time")?,
        end_time: row.try_get("end_time")?,
        status: row.try_get("status")?,
        source: row.try_get("source")?,
        last_reset_time: row.try_get("last_reset_time")?,
        next_reset_time: row.try_get("next_reset_time")?,
        upgrade_group: row.try_get("upgrade_group")?,
        prev_user_group: row.try_get("prev_user_group")?,
        downgrade_group: row.try_get("downgrade_group")?,
        allow_wallet_overflow: row.try_get("allow_wallet_overflow")?,
        created_at: row.try_get("created_at")?,
        updated_at: row.try_get("updated_at")?,
    })
}
async fn subscriptions(
    pg: &PgPool,
    user_id: i64,
    active_only: bool,
) -> Result<Vec<SubscriptionView>, sqlx::Error> {
    let sql = if active_only {
        format!(
            "{SUB_SELECT} WHERE user_id=$1 AND status='active' AND end_time > $2 ORDER BY end_time DESC,id DESC"
        )
    } else {
        format!("{SUB_SELECT} WHERE user_id=$1 ORDER BY end_time DESC,id DESC")
    };
    let mut query = sqlx::query(&sql).bind(user_id);
    if active_only {
        query = query.bind(now());
    }
    Ok(query
        .fetch_all(pg)
        .await?
        .iter()
        .map(subscription_from_row)
        .collect::<Result<Vec<_>, _>>()?
        .into_iter()
        .map(|subscription| SubscriptionView { subscription })
        .collect())
}

#[derive(Deserialize)]
struct BindRequest {
    user_id: i64,
    plan_id: i64,
}
#[derive(Deserialize)]
struct CreateSubscriptionRequest {
    plan_id: i64,
}
async fn admin_bind_subscription(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
    Json(input): Json<BindRequest>,
) -> Response {
    if let Err(response) = authorize_admin_write(&state, &headers).await {
        return response;
    }
    with_auth_version(bind(&state, input.user_id, input.plan_id).await)
}
async fn admin_create_user_subscription(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
    Path(user_id): Path<i64>,
    Json(input): Json<CreateSubscriptionRequest>,
) -> Response {
    if let Err(response) = authorize_admin_write(&state, &headers).await {
        return response;
    }
    if user_id <= 0 {
        return with_auth_version(failure(StatusCode::BAD_REQUEST, "无效的用户ID"));
    }
    with_auth_version(bind(&state, user_id, input.plan_id).await)
}
async fn bind(state: &BillingSubscriptionsState, user_id: i64, plan_id: i64) -> Response {
    if user_id <= 0 || plan_id <= 0 {
        return failure(StatusCode::BAD_REQUEST, "参数错误");
    }
    let (mut tx, plan) = match transaction_with_locked_plan(&state.pg, plan_id).await {
        Ok(transaction) => transaction,
        Err(response) => return response,
    };
    match create_subscription(&mut tx, user_id, &plan, "admin").await {
        Ok(message) => match tx.commit().await {
            Ok(()) => {
                evict_plan(state, plan_id).await;
                evict_user(state, user_id).await;
                if let Some(message) = message {
                    ok(json!({"message": message}))
                } else {
                    empty_ok()
                }
            }
            Err(_) => failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
        },
        Err(message) => failure(StatusCode::BAD_REQUEST, message),
    }
}

async fn plan(pg: &PgPool, id: i64) -> Result<Plan, sqlx::Error> {
    sqlx::query(&format!("{PLAN_SELECT} WHERE id=$1"))
        .bind(id)
        .fetch_one(pg)
        .await
        .and_then(|row| plan_from_row(&row))
}
async fn locked_plan(
    tx: &mut Transaction<'_, Postgres>,
    id: i64,
) -> Result<Option<Plan>, sqlx::Error> {
    sqlx::query(&format!("{PLAN_SELECT} WHERE id=$1 FOR UPDATE"))
        .bind(id)
        .fetch_optional(&mut **tx)
        .await?
        .map(|row| plan_from_row(&row))
        .transpose()
}

async fn transaction_with_locked_plan<'a>(
    pg: &'a PgPool,
    plan_id: i64,
) -> Result<(Transaction<'a, Postgres>, Plan), Response> {
    let mut tx = pg
        .begin()
        .await
        .map_err(|_| failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"))?;
    let plan = match locked_plan(&mut tx, plan_id).await {
        Ok(Some(plan)) => plan,
        Ok(None) => return Err(failure(StatusCode::NOT_FOUND, "套餐不存在")),
        Err(_) => return Err(failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误")),
    };
    Ok((tx, plan))
}
async fn create_subscription(
    tx: &mut Transaction<'_, Postgres>,
    user_id: i64,
    plan: &Plan,
    source: &str,
) -> Result<Option<String>, &'static str> {
    let user =
        sqlx::query("SELECT \"group\" FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE")
            .bind(user_id)
            .fetch_optional(&mut **tx)
            .await
            .map_err(|_| "系统错误")?
            .ok_or("用户不存在")?;
    if plan.max_purchase_per_user > 0 {
        let count: i64 = sqlx::query_scalar(
            "SELECT COUNT(*) FROM user_subscriptions WHERE user_id=$1 AND plan_id=$2",
        )
        .bind(user_id)
        .bind(plan.id)
        .fetch_one(&mut **tx)
        .await
        .map_err(|_| "系统错误")?;
        if count >= plan.max_purchase_per_user {
            return Err("已达到该套餐购买上限");
        }
    }
    let current_group: String = user.try_get("group").map_err(|_| "系统错误")?;
    let start = now();
    let end = end_time(start, plan)?;
    let next = next_reset(start, end, plan);
    let changed = !plan.upgrade_group.is_empty() && plan.upgrade_group != current_group;
    if changed {
        sqlx::query("UPDATE users SET \"group\"=$2 WHERE id=$1")
            .bind(user_id)
            .bind(&plan.upgrade_group)
            .execute(&mut **tx)
            .await
            .map_err(|_| "系统错误")?;
    }
    sqlx::query("INSERT INTO user_subscriptions (user_id,plan_id,amount_total,amount_used,start_time,end_time,status,source,last_reset_time,next_reset_time,upgrade_group,prev_user_group,downgrade_group,allow_wallet_overflow,created_at,updated_at) VALUES ($1,$2,$3,0,$4,$5,'active',$6,$7,$8,$9,$10,$11,$12,$4,$4)").bind(user_id).bind(plan.id).bind(plan.total_amount).bind(start).bind(end).bind(source).bind(if next > 0 { start } else { 0 }).bind(next).bind(&plan.upgrade_group).bind(if changed { &current_group } else { "" }).bind(&plan.downgrade_group).bind(plan.allow_wallet_overflow).execute(&mut **tx).await.map_err(|_| "系统错误")?;
    Ok(changed.then(|| format!("用户分组将升级到 {}", plan.upgrade_group)))
}
fn end_time(start: i64, plan: &Plan) -> Result<i64, &'static str> {
    let start = local_timestamp(start).ok_or("无效的订阅周期")?;
    let end = match plan.duration_unit.as_str() {
        // Go's time.Time.AddDate is calendar based (and deliberately carries
        // an end-of-month overflow into the following month), rather than a
        // fixed 365/30-day approximation.
        "year" => {
            let months = plan
                .duration_value
                .checked_mul(12)
                .ok_or("无效的订阅周期")?;
            add_calendar_months(start, months)
        }
        "month" => add_calendar_months(start, plan.duration_value),
        "day" => start.checked_add_signed(ChronoDuration::days(plan.duration_value)),
        "hour" => start.checked_add_signed(ChronoDuration::hours(plan.duration_value)),
        "custom" => start.checked_add_signed(ChronoDuration::seconds(plan.custom_seconds)),
        _ => None,
    }
    .ok_or("无效的订阅周期")?;
    Ok(end.timestamp())
}
fn next_reset(start: i64, end: i64, plan: &Plan) -> i64 {
    let Some(base) = local_timestamp(start) else {
        return 0;
    };
    let next = match plan.quota_reset_period.as_str() {
        "daily" => local_midnight(base)
            .and_then(|midnight| midnight.checked_add_signed(ChronoDuration::days(1))),
        "weekly" => {
            // Go aligns weekly resets to the next Monday at local midnight.
            let days_until_monday = 8 - i64::from(base.weekday().number_from_monday());
            local_midnight(base).and_then(|midnight| {
                midnight.checked_add_signed(ChronoDuration::days(days_until_monday))
            })
        }
        "monthly" => first_of_month(base).and_then(|first| add_calendar_months(first, 1)),
        "custom" if plan.quota_reset_custom_seconds > 0 => {
            base.checked_add_signed(ChronoDuration::seconds(plan.quota_reset_custom_seconds))
        }
        "custom" => None,
        _ => None,
    };
    let Some(next) = next else {
        return 0;
    };
    let next = next.timestamp();
    if next > 0 && next <= end { next } else { 0 }
}

/// Convert a Unix timestamp using the process-local timezone, matching Go's
/// `time.Unix`/`time.Date` behaviour used by subscription calculations.
fn local_timestamp(timestamp: i64) -> Option<DateTime<Local>> {
    match Local.timestamp_opt(timestamp, 0) {
        LocalResult::Single(value) | LocalResult::Ambiguous(value, _) => Some(value),
        LocalResult::None => None,
    }
}

fn local_from_naive(value: NaiveDateTime) -> Option<DateTime<Local>> {
    match Local.from_local_datetime(&value) {
        LocalResult::Single(value) | LocalResult::Ambiguous(value, _) => Some(value),
        LocalResult::None => None,
    }
}

fn local_midnight(value: DateTime<Local>) -> Option<DateTime<Local>> {
    local_from_naive(value.date_naive().and_hms_opt(0, 0, 0)?)
}

fn first_of_month(value: DateTime<Local>) -> Option<DateTime<Local>> {
    local_from_naive(value.date_naive().with_day(1)?.and_hms_opt(0, 0, 0)?)
}

/// Match Go's AddDate for year/month arithmetic. Go normalizes an invalid
/// target day forward (for example 2025-01-31 + 1 month = 2025-03-03), while
/// chrono's month helper clamps to the last day of the target month.
fn add_calendar_months(value: DateTime<Local>, months: i64) -> Option<DateTime<Local>> {
    let total_months = i64::from(value.year())
        .checked_mul(12)?
        .checked_add(i64::from(value.month0()))?
        .checked_add(months)?;
    let year = total_months.div_euclid(12);
    let month0 = total_months.rem_euclid(12);
    let date = chrono::NaiveDate::from_ymd_opt(year.try_into().ok()?, month0 as u32 + 1, 1)?
        .checked_add_signed(ChronoDuration::days(i64::from(value.day()) - 1))?;
    local_from_naive(date.and_time(value.time()))
}

#[derive(Deserialize)]
struct ResetRequest {
    plan_id: Option<i64>,
    advance_reset_time: Option<bool>,
}
#[derive(Serialize)]
struct ResetResult {
    plan_id: i64,
    matched_count: i64,
    reset_count: i64,
    user_count: i64,
    advance_reset_time: bool,
}
async fn admin_reset_user_subscriptions(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
    Path(user_id): Path<i64>,
    Json(input): Json<ResetRequest>,
) -> Response {
    if let Err(response) = authorize_admin_id(&state, &headers, user_id, "无效的用户ID").await
    {
        return response;
    }
    let Some(plan_id) = input.plan_id else {
        return failure(StatusCode::BAD_REQUEST, "参数错误");
    };
    with_auth_version(
        reset(
            &state,
            Some(user_id),
            plan_id,
            input.advance_reset_time.unwrap_or(true),
        )
        .await,
    )
}
async fn admin_reset_plan(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
    Path(plan_id): Path<i64>,
    Json(input): Json<ResetRequest>,
) -> Response {
    if let Err(response) = authorize_admin_id(&state, &headers, plan_id, "无效的ID").await {
        return response;
    }
    with_auth_version(
        reset(
            &state,
            None,
            plan_id,
            input.advance_reset_time.unwrap_or(true),
        )
        .await,
    )
}
async fn reset(
    state: &BillingSubscriptionsState,
    user_id: Option<i64>,
    plan_id: i64,
    advance: bool,
) -> Response {
    let (mut tx, plan) = match transaction_with_locked_plan(&state.pg, plan_id).await {
        Ok(transaction) => transaction,
        Err(response) => return response,
    };
    let current = now();
    let rows = match user_id { Some(user) => sqlx::query(&format!("{SUB_SELECT} WHERE user_id=$1 AND plan_id=$2 AND status='active' AND end_time>$3 ORDER BY end_time,id FOR UPDATE")).bind(user).bind(plan_id).bind(current).fetch_all(&mut *tx).await, None => sqlx::query(&format!("{SUB_SELECT} WHERE plan_id=$1 AND status='active' AND end_time>$2 ORDER BY user_id,end_time,id FOR UPDATE")).bind(plan_id).bind(current).fetch_all(&mut *tx).await };
    let rows = match rows {
        Ok(rows) => rows,
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
    };
    if user_id.is_some() && rows.is_empty() {
        return failure(StatusCode::BAD_REQUEST, "该用户没有有效的此套餐订阅");
    }
    let users = match rows
        .iter()
        .map(|row| row.try_get::<i64, _>("user_id"))
        .collect::<Result<BTreeSet<_>, _>>()
    {
        Ok(users) => users,
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
    };
    for row in &rows {
        let id: i64 = match row.try_get("id") {
            Ok(id) => id,
            Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
        };
        let next = if advance {
            let end: i64 = match row.try_get("end_time") {
                Ok(end) => end,
                Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
            };
            next_reset(current, end, &plan)
        } else {
            match row.try_get("next_reset_time") {
                Ok(next) => next,
                Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
            }
        };
        let last = if advance {
            if next > 0 { current } else { 0 }
        } else {
            match row.try_get("last_reset_time") {
                Ok(last) => last,
                Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
            }
        };
        if sqlx::query("UPDATE user_subscriptions SET amount_used=0,last_reset_time=$2,next_reset_time=$3,updated_at=$4 WHERE id=$1").bind(id).bind(last).bind(next).bind(current).execute(&mut *tx).await.is_err() { return failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"); }
    }
    if tx.commit().await.is_err() {
        return failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误");
    };
    evict_plan(state, plan_id).await;
    ok(ResetResult {
        plan_id,
        matched_count: rows.len() as i64,
        reset_count: rows.len() as i64,
        user_count: users.len() as i64,
        advance_reset_time: advance,
    })
}

async fn admin_list_user_subscriptions(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
    Path(user_id): Path<i64>,
) -> Response {
    if let Err(response) = authorize_admin_id(&state, &headers, user_id, "无效的用户ID").await
    {
        return response;
    }
    with_auth_version(
        subscriptions(&state.pg, user_id, false)
            .await
            .map(ok)
            .unwrap_or_else(|_| failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误")),
    )
}
async fn admin_invalidate_subscription(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
    Path(id): Path<i64>,
) -> Response {
    admin_change_subscription(&state, &headers, id, true).await
}
async fn admin_delete_subscription(
    State(state): State<BillingSubscriptionsState>,
    headers: HeaderMap,
    Path(id): Path<i64>,
) -> Response {
    admin_change_subscription(&state, &headers, id, false).await
}

async fn admin_change_subscription(
    state: &BillingSubscriptionsState,
    headers: &HeaderMap,
    id: i64,
    invalidate: bool,
) -> Response {
    if let Err(response) = admin(state, headers).await {
        return response;
    }
    with_auth_version(change_subscription_status(state, id, invalidate).await)
}
async fn change_subscription_status(
    state: &BillingSubscriptionsState,
    id: i64,
    invalidate: bool,
) -> Response {
    if id <= 0 {
        return failure(StatusCode::BAD_REQUEST, "无效的订阅ID");
    };
    let mut tx = match state.pg.begin().await {
        Ok(tx) => tx,
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
    };
    let subscription = match locked_subscription(&mut tx, id).await {
        Ok(Some(subscription)) => subscription,
        Ok(None) => return failure(StatusCode::NOT_FOUND, "订阅不存在"),
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
    };
    let current = now();
    let downgrade = match downgrade_user_group(&mut tx, &subscription, current).await {
        Ok(group) => group,
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误"),
    };
    let mutation = if invalidate {
        sqlx::query(
            "UPDATE user_subscriptions SET status='cancelled',end_time=$2,updated_at=$2 WHERE id=$1",
        )
        .bind(id)
        .bind(current)
        .execute(&mut *tx)
        .await
    } else {
        sqlx::query("DELETE FROM user_subscriptions WHERE id=$1")
            .bind(id)
            .execute(&mut *tx)
            .await
    };
    if mutation.is_err() || tx.commit().await.is_err() {
        return failure(StatusCode::INTERNAL_SERVER_ERROR, "系统错误");
    }
    evict_user(state, subscription.user_id).await;
    match downgrade {
        Some(group) => ok(json!({"message": format!("用户分组将回退到 {group}")})),
        None => empty_ok(),
    }
}

async fn locked_subscription(
    tx: &mut Transaction<'_, Postgres>,
    id: i64,
) -> Result<Option<Subscription>, sqlx::Error> {
    sqlx::query(&format!("{SUB_SELECT} WHERE id=$1 FOR UPDATE"))
        .bind(id)
        .fetch_optional(&mut **tx)
        .await?
        .map(|row| subscription_from_row(&row))
        .transpose()
}

/// Preserve the legacy cancellation/delete ordering: lock the subscription and
/// user, keep a later active upgraded subscription, then apply an explicit
/// downgrade group or the pre-upgrade group snapshot.
async fn downgrade_user_group(
    tx: &mut Transaction<'_, Postgres>,
    subscription: &Subscription,
    current: i64,
) -> Result<Option<String>, sqlx::Error> {
    if subscription.downgrade_group.is_empty() && subscription.upgrade_group.is_empty() {
        return Ok(None);
    }
    let user = sqlx::query("SELECT \"group\" FROM users WHERE id=$1 FOR UPDATE")
        .bind(subscription.user_id)
        .fetch_one(&mut **tx)
        .await?;
    let current_group: String = user.try_get("group")?;
    let other_active: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM user_subscriptions WHERE user_id=$1 AND status='active' AND end_time>$2 AND id<>$3 AND upgrade_group<>'')",
    )
    .bind(subscription.user_id)
    .bind(current)
    .bind(subscription.id)
    .fetch_one(&mut **tx)
    .await?;
    if other_active {
        return Ok(None);
    }
    let target = if !subscription.downgrade_group.is_empty() {
        subscription.downgrade_group.as_str()
    } else if current_group == subscription.upgrade_group {
        subscription.prev_user_group.as_str()
    } else {
        ""
    };
    if target.is_empty() || target == current_group {
        return Ok(None);
    }
    sqlx::query("UPDATE users SET \"group\"=$2 WHERE id=$1")
        .bind(subscription.user_id)
        .bind(target)
        .execute(&mut **tx)
        .await?;
    Ok(Some(target.to_owned()))
}

async fn valkey_connection(
    client: Option<&redis::Client>,
) -> Option<redis::aio::MultiplexedConnection> {
    client?.get_multiplexed_async_connection().await.ok()
}

async fn evict_plan(state: &BillingSubscriptionsState, plan_id: i64) {
    let Some(mut connection) = valkey_connection(state.valkey.as_ref()).await else {
        return;
    };
    let _: Result<(), _> = redis::cmd("DEL")
        .arg(format!("{PLAN_CACHE_PREFIX}{plan_id}"))
        .query_async(&mut connection)
        .await;
    let mut cursor = 0_u64;
    loop {
        let scan: Result<(u64, Vec<String>), _> = redis::cmd("SCAN")
            .arg(cursor)
            .arg("MATCH")
            .arg(format!("{PLAN_INFO_CACHE_PREFIX}*"))
            .arg("COUNT")
            .arg(200)
            .query_async(&mut connection)
            .await;
        let Ok((next, keys)) = scan else { return };
        if !keys.is_empty() {
            let _: Result<(), _> = redis::cmd("DEL")
                .arg(keys)
                .query_async(&mut connection)
                .await;
        }
        cursor = next;
        if cursor == 0 {
            return;
        }
    }
}

/// A failed Valkey eviction never rolls back a committed PostgreSQL change.
/// The user cache is reconstructible from PostgreSQL and its TTL bounds the
/// recovery window, matching the legacy cache-aside contract.
async fn evict_user(state: &BillingSubscriptionsState, user_id: i64) {
    evict_user_cache(state.valkey.as_ref(), user_id).await;
}

async fn evict_user_cache(valkey: Option<&redis::Client>, user_id: i64) {
    let Some(mut connection) = valkey_connection(valkey).await else {
        return;
    };
    let _: Result<(), _> = redis::cmd("DEL")
        .arg(format!("{USER_CACHE_PREFIX}{user_id}"))
        .query_async(&mut connection)
        .await;
}

/// Refresh only the setting field of an already-populated legacy user hash.
///
/// Go's `UpdateUserSetting` performs this as an auth-version-fenced HSET and
/// deliberately leaves a cold/missing hash alone. The fence prevents a stale
/// asynchronous cache fill from overwriting a newer snapshot; preserving the
/// existing CacheSchema field keeps Rust's schema-2 model cache compatible
/// with a hash that was populated by either runtime.
async fn update_user_setting_cache(state: &BillingSubscriptionsState, user_id: i64, setting: &str) {
    let Some(client) = state.valkey.as_ref() else {
        return;
    };
    let auth_version = match sqlx::query_scalar::<_, i64>(
        "SELECT COALESCE(auth_version, 0) FROM users WHERE id = $1 AND deleted_at IS NULL",
    )
    .bind(user_id)
    .fetch_optional(&state.pg)
    .await
    {
        Ok(Some(version)) if version > 0 => version,
        Ok(_) => return,
        Err(error) => {
            tracing::warn!(%error, user_id, "subscription preference cache version lookup failed");
            return;
        }
    };
    let Ok(mut connection) = client.get_multiplexed_async_connection().await else {
        tracing::warn!(user_id, "subscription preference cache connection failed");
        return;
    };
    // Keep this script in lockstep with Go's updateUserCacheFieldAtVersion:
    // security fences win, committed floors are monotonic, and a cold cache is
    // not manufactured by a setting-only update.
    let script = redis::Script::new(
        r#"local incoming=tonumber(ARGV[1]); local pending=tonumber(redis.call('GET',KEYS[2]) or '0'); local committed=tonumber(redis.call('GET',KEYS[3]) or '0'); local current=tonumber(redis.call('HGET',KEYS[1],'AuthVersion') or '0'); if pending>incoming or committed>incoming or current>incoming then return 0 end; if committed<incoming then redis.call('SET',KEYS[3],ARGV[1]) end; if pending>0 and pending<=incoming then redis.call('DEL',KEYS[2]) end; if redis.call('EXISTS',KEYS[1])==0 then return 1 end; if current~=incoming then return 1 end; redis.call('HSET',KEYS[1],'Setting',ARGV[2]); return 1"#,
    );
    if let Err(error) = script
        .key(format!("{USER_CACHE_PREFIX}{user_id}"))
        .key(format!("auth:user:fence:{user_id}"))
        .key(format!("auth:user:version:{user_id}"))
        .arg(auth_version)
        .arg(setting)
        .invoke_async::<i64>(&mut connection)
        .await
    {
        tracing::warn!(%error, user_id, "subscription preference cache update failed");
    }
}

fn now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs() as i64)
}

#[cfg(test)]
mod tests {
    use chrono::{Local, TimeZone};
    use std::{error::Error, io};

    use super::{
        LegacyUserSetting, Plan, end_time, next_reset, normalize_preference,
        parse_preference_request,
    };

    type TestResult<T = ()> = Result<T, Box<dyn Error>>;

    fn test_error(message: impl Into<String>) -> Box<dyn Error> {
        Box::new(io::Error::other(message.into()))
    }

    fn local_timestamp(year: i32, month: u32, day: u32, hour: u32, minute: u32) -> TestResult<i64> {
        Local
            .with_ymd_and_hms(year, month, day, hour, minute, 0)
            .single()
            .map(|timestamp| timestamp.timestamp())
            .ok_or_else(|| test_error("test timestamp should be a valid, unambiguous local time"))
    }

    fn plan() -> Plan {
        Plan {
            id: 1,
            title: "p".into(),
            subtitle: String::new(),
            price_amount: 1.0,
            currency: "USD".into(),
            duration_unit: "day".into(),
            duration_value: 1,
            custom_seconds: 0,
            enabled: true,
            sort_order: 0,
            allow_balance_pay: true,
            allow_wallet_overflow: true,
            stripe_price_id: String::new(),
            creem_product_id: String::new(),
            waffo_pancake_product_id: String::new(),
            max_purchase_per_user: 0,
            total_amount: 1,
            upgrade_group: String::new(),
            downgrade_group: String::new(),
            quota_reset_period: "daily".into(),
            quota_reset_custom_seconds: 0,
            created_at: 0,
            updated_at: 0,
        }
    }
    #[test]
    fn preference_matches_go_enum_and_default() -> TestResult {
        for preference in [
            "subscription_first",
            "wallet_first",
            "subscription_only",
            "wallet_only",
        ] {
            assert_eq!(normalize_preference(preference), preference);
        }
        assert_eq!(normalize_preference("unexpected"), "subscription_first");
        assert_eq!(normalize_preference("quota"), "subscription_first");
        Ok(())
    }

    #[test]
    fn preference_request_binding_matches_gin_zero_value_contract() -> TestResult {
        let explicit = parse_preference_request(br#"{"billing_preference":" wallet_only "}"#)
            .map_err(|()| test_error("valid billing preference JSON should deserialize"))?;
        assert_eq!(explicit.billing_preference, " wallet_only ");

        let omitted = parse_preference_request(br#"{}"#)
            .map_err(|()| test_error("omitted billing preference JSON should deserialize"))?;
        assert_eq!(omitted.billing_preference, "");

        let null = parse_preference_request(br#"{"billing_preference":null}"#)
            .map_err(|()| test_error("null billing preference JSON should deserialize"))?;
        assert_eq!(null.billing_preference, "");

        assert!(parse_preference_request(br#"{"billing_preference":7}"#).is_err());
        assert!(parse_preference_request(br#"[]"#).is_err());
        Ok(())
    }

    #[test]
    fn preference_persistence_matches_go_user_setting_json() -> TestResult {
        let mut setting = serde_json::from_str::<LegacyUserSetting>("{}").map_err(|error| {
            test_error(format!(
                "empty legacy user setting JSON should deserialize: {error}"
            ))
        })?;
        setting.billing_preference = "subscription_first".to_owned();
        let serialized = serde_json::to_string(&setting).map_err(|error| {
            test_error(format!(
                "legacy user setting should serialize as JSON: {error}"
            ))
        })?;
        assert_eq!(
            serialized,
            r#"{"gotify_priority":0,"billing_preference":"subscription_first"}"#
        );
        Ok(())
    }

    #[test]
    fn subscription_end_time_uses_plan_duration() -> TestResult {
        assert_eq!(end_time(100, &plan()), Ok(86_500));
        Ok(())
    }

    #[test]
    fn plan_price_wire_number_matches_go_encoding() -> TestResult {
        let mut integral = plan();
        integral.price_amount = 10.0;
        let integral_json = serde_json::to_value(&integral).map_err(|error| {
            test_error(format!(
                "integral-price plan should serialize as JSON: {error}"
            ))
        })?;
        assert_eq!(integral_json["price_amount"], serde_json::json!(10));

        let mut fractional = plan();
        fractional.price_amount = 0.97;
        let fractional_json = serde_json::to_value(&fractional).map_err(|error| {
            test_error(format!(
                "fractional-price plan should serialize as JSON: {error}"
            ))
        })?;
        assert_eq!(fractional_json["price_amount"], serde_json::json!(0.97));
        Ok(())
    }

    #[test]
    fn subscription_month_and_year_use_go_calendar_overflow() -> TestResult {
        let month_start = local_timestamp(2025, 1, 31, 12, 0)?;
        let mut month_plan = plan();
        month_plan.duration_unit = "month".into();
        assert_eq!(
            end_time(month_start, &month_plan),
            Ok(local_timestamp(2025, 3, 3, 12, 0)?)
        );

        let year_start = local_timestamp(2024, 2, 29, 12, 0)?;
        let mut year_plan = plan();
        year_plan.duration_unit = "year".into();
        assert_eq!(
            end_time(year_start, &year_plan),
            Ok(local_timestamp(2025, 3, 1, 12, 0)?)
        );
        Ok(())
    }

    #[test]
    fn subscription_resets_use_local_calendar_boundaries() -> TestResult {
        let start = local_timestamp(2025, 1, 31, 12, 0)?;
        let end = local_timestamp(2025, 3, 4, 0, 0)?;
        let mut reset_plan = plan();

        reset_plan.quota_reset_period = "daily".into();
        assert_eq!(
            next_reset(start, end, &reset_plan),
            local_timestamp(2025, 2, 1, 0, 0)?
        );

        reset_plan.quota_reset_period = "weekly".into();
        assert_eq!(
            next_reset(start, end, &reset_plan),
            local_timestamp(2025, 2, 3, 0, 0)?
        );

        reset_plan.quota_reset_period = "monthly".into();
        assert_eq!(
            next_reset(start, end, &reset_plan),
            local_timestamp(2025, 2, 1, 0, 0)?
        );
        Ok(())
    }

    #[test]
    fn reset_never_exceeds_subscription_end() -> TestResult {
        let start = local_timestamp(2025, 1, 31, 12, 0)?;
        let before_midnight = local_timestamp(2025, 1, 31, 23, 59)?;
        assert_eq!(next_reset(start, before_midnight, &plan()), 0);
        Ok(())
    }
}
