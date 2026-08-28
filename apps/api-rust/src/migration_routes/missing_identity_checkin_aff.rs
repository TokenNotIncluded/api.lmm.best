//! Legacy-compatible check-in, affiliate-quota, and top-up quote routes.
//!
//! The database is the source of truth: all balance-changing operations use a
//! PostgreSQL transaction and lock the user row.  The small [`Clock`] and
//! [`Awarder`] seams keep the calendar and random reward boundary testable.

use std::{
    collections::BTreeMap,
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{Query, Request, State},
    http::{HeaderMap, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use chrono::{FixedOffset, Local, TimeZone};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{PgPool, Row};

use crate::auth::{
    AuthErrorKind, DashboardAuth, DashboardUser, UserAuthPolicyError, enforce_user_auth,
    user_auth_message,
};

const DEFAULT_QUOTA_PER_UNIT: i64 = 500_000;
const DEFAULT_CHECKIN_MIN_QUOTA: i64 = 1_000;
const DEFAULT_CHECKIN_MAX_QUOTA: i64 = 10_000;
const LOG_TYPE_SYSTEM: i64 = 4;

#[async_trait]
pub trait Clock: Send + Sync {
    fn now(&self) -> i64;
}
pub trait Calendar: Send + Sync {
    fn date(&self, epoch: i64) -> String;
    fn month(&self, epoch: i64) -> String;
}
pub struct ProcessLocalCalendar;
impl Calendar for ProcessLocalCalendar {
    fn date(&self, epoch: i64) -> String {
        Local.timestamp_opt(epoch, 0).single().map_or_else(
            || "1970-01-01".to_owned(),
            |value| value.format("%Y-%m-%d").to_string(),
        )
    }
    fn month(&self, epoch: i64) -> String {
        Local.timestamp_opt(epoch, 0).single().map_or_else(
            || "1970-01".to_owned(),
            |value| value.format("%Y-%m").to_string(),
        )
    }
}
struct FixedCalendar(FixedOffset);
impl Calendar for FixedCalendar {
    fn date(&self, epoch: i64) -> String {
        date(epoch, self.0)
    }
    fn month(&self, epoch: i64) -> String {
        month(epoch, self.0)
    }
}
pub struct SystemClock;
#[async_trait]
impl Clock for SystemClock {
    fn now(&self) -> i64 {
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_or(0, |d| d.as_secs() as i64)
    }
}

#[async_trait]
pub trait Awarder: Send + Sync {
    fn award(&self, min: i64, max: i64) -> i64;
}
pub struct RandomAwarder;
#[async_trait]
impl Awarder for RandomAwarder {
    fn award(&self, min: i64, max: i64) -> i64 {
        if max <= min {
            min
        } else {
            min + (rand::random::<u64>() % ((max - min + 1) as u64)) as i64
        }
    }
}

/// Effects which are deliberately outside the durable check-in transaction.
/// Go commits the check-in first, then best-effort updates the user hash and
/// writes its system log; neither failure may revoke a completed check-in.
#[async_trait]
pub trait CheckinEffects: Send + Sync {
    async fn committed(&self, user_id: i64, username: &str, award: i64, now: i64);
}

struct NoopCheckinEffects;
#[async_trait]
impl CheckinEffects for NoopCheckinEffects {
    async fn committed(&self, _: i64, _: &str, _: i64, _: i64) {}
}

/// Real post-commit adapter for the isolated Rust surface.
pub struct PgValkeyCheckinEffects {
    pg: PgPool,
    valkey: redis::Client,
}
impl PgValkeyCheckinEffects {
    #[must_use]
    pub fn new(pg: PgPool, valkey: redis::Client) -> Self {
        Self { pg, valkey }
    }
}
#[async_trait]
impl CheckinEffects for PgValkeyCheckinEffects {
    async fn committed(&self, user_id: i64, username: &str, award: i64, now: i64) {
        let cache = async {
            let mut connection = self.valkey.get_multiplexed_async_connection().await?;
            redis::cmd("HINCRBY")
                .arg(format!("user:{user_id}"))
                .arg("Quota")
                .arg(award)
                .query_async::<i64>(&mut connection)
                .await
        }
        .await;
        if let Err(error) = cache {
            tracing::warn!(%error, user_id, "checkin quota cache update failed after commit");
        }
        // Frozen Go uses `logger.LogQuota` before `model.RecordLog`.  The
        // default display is USD and `QuotaPerUnit` is a direct option key.
        let quota_per_unit =
            option_f64(&self.pg, "QuotaPerUnit", DEFAULT_QUOTA_PER_UNIT as f64).await;
        let content = format!(
            "用户签到，获得额度 {}",
            checkin_usd_log_quota(award, quota_per_unit)
        );
        // GORM writes the zero values of `Log` explicitly.  The PostgreSQL
        // baseline leaves several of these nullable, so set them here rather
        // than relying on its column defaults.
        let log = sqlx::query("INSERT INTO logs (user_id, created_at, type, content, username, token_name, model_name, quota, prompt_tokens, completion_tokens, use_time, is_stream, channel_id, token_id, \"group\", ip, other) VALUES ($1, $2, $3, $4, $5, '', '', 0, 0, 0, 0, false, 0, 0, '', '', '')")
            .bind(user_id).bind(now).bind(LOG_TYPE_SYSTEM)
            .bind(content).bind(username).execute(&self.pg).await;
        if let Err(error) = log {
            tracing::warn!(%error, user_id, "checkin system log write failed after commit");
        }
    }
}

#[derive(Clone)]
pub struct IdentityCheckinAffState {
    pg: PgPool,
    auth: Arc<dyn DashboardAuth>,
    clock: Arc<dyn Clock>,
    awarder: Arc<dyn Awarder>,
    effects: Arc<dyn CheckinEffects>,
    /// Calendar boundaries are injected by the runtime, so tests need not
    /// mutate global process timezone state.
    calendar: Arc<dyn Calendar>,
}
impl IdentityCheckinAffState {
    #[must_use]
    pub fn new(pg: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self {
            pg,
            auth,
            clock: Arc::new(SystemClock),
            awarder: Arc::new(RandomAwarder),
            effects: Arc::new(NoopCheckinEffects),
            calendar: Arc::new(ProcessLocalCalendar),
        }
    }
    #[must_use]
    pub fn with_clock(mut self, clock: Arc<dyn Clock>) -> Self {
        self.clock = clock;
        self
    }
    #[must_use]
    pub fn with_awarder(mut self, awarder: Arc<dyn Awarder>) -> Self {
        self.awarder = awarder;
        self
    }
    #[must_use]
    pub fn with_effects(mut self, effects: Arc<dyn CheckinEffects>) -> Self {
        self.effects = effects;
        self
    }
    #[must_use]
    pub fn with_timezone(mut self, timezone: FixedOffset) -> Self {
        self.calendar = Arc::new(FixedCalendar(timezone));
        self
    }
}

pub fn router(state: IdentityCheckinAffState) -> Router {
    checkin_read_routes()
        .route("/api/user/checkin", post(checkin))
        .route("/api/user/aff_transfer", post(aff_transfer))
        .route("/api/user/amount", post(amount))
        .with_state(state)
}

/// Read-only normal-listener slice for the Go `GET /api/user/checkin` route.
///
/// Keep the quota-changing check-in and affiliate routes on the isolated
/// candidate surface until their side effects have independent differential
/// evidence.  Sharing this route builder with [`router`] prevents the two
/// mounts from drifting while ensuring the normal listener cannot accidentally
/// expose a write method.
pub fn read_router(state: IdentityCheckinAffState) -> Router {
    checkin_read_routes().with_state(state)
}

fn checkin_read_routes() -> Router<IdentityCheckinAffState> {
    Router::new().route("/api/user/checkin", get(checkin_status))
}

#[derive(Serialize)]
struct Envelope<T: Serialize> {
    success: bool,
    message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<T>,
}
fn fail(message: impl Into<String>) -> Response {
    Json(Envelope::<()> {
        success: false,
        message: message.into(),
        data: None,
    })
    .into_response()
}
fn legacy(message: &'static str, data: impl Serialize) -> Response {
    Json(json!({"message":message,"data":data})).into_response()
}
fn checkin_status_ok(data: Value) -> Response {
    // Frozen Go writes this route's success map directly, unlike the shared
    // `ApiSuccess` helper, so the empty `message` field is absent.
    Json(json!({"success":true,"data":data})).into_response()
}

async fn user(
    state: &IdentityCheckinAffState,
    headers: &HeaderMap,
) -> Result<DashboardUser, Response> {
    let token = dashboard_token(headers).ok_or_else(|| auth_error(StatusCode::UNAUTHORIZED))?;
    let user = match state
        .auth
        .self_user(SecretString::from(token.to_owned()))
        .await
    {
        Ok(user) => user,
        Err(e) if e.kind == AuthErrorKind::UserDisabled => {
            return Err(user_auth_error(headers, UserAuthPolicyError::UserDisabled));
        }
        Err(e)
            if matches!(
                e.kind,
                AuthErrorKind::Unauthorized
                    | AuthErrorKind::TokenExpired
                    | AuthErrorKind::SessionRevoked
            ) =>
        {
            return Err(auth_error(StatusCode::UNAUTHORIZED));
        }
        Err(_) => return Err(auth_error(StatusCode::INTERNAL_SERVER_ERROR)),
    };
    enforce_user_auth(&user)
        .map(|()| user)
        .map_err(|error| user_auth_error(headers, error))
}

/// Mirrors the frozen Go `authorizationToken` parser: accept a bare token or
/// a case-insensitive two-word `Bearer <token>` value, while rejecting extra
/// words and empty credentials.
fn dashboard_token(headers: &HeaderMap) -> Option<String> {
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

fn auth_error(status: StatusCode) -> Response {
    (
        status,
        Json(json!({"success":false,"code":"AUTH_UNAUTHORIZED","message":"Unauthorized, invalid access token"})),
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

#[derive(Default, Deserialize)]
struct Month {
    month: Option<String>,
}

/// Mirrors `strconv.ParseBool`, which is what the frozen Go configuration
/// loader applies to the flattened `checkin_setting.enabled` option.
fn parse_go_bool(value: &str) -> Option<bool> {
    match value {
        "1" | "t" | "T" | "TRUE" | "true" | "True" => Some(true),
        "0" | "f" | "F" | "FALSE" | "false" | "False" => Some(false),
        _ => None,
    }
}

/// Mirrors the Go loader's `ParseInt` first, then `ParseFloat` compatibility
/// path for integer config fields such as `100.000000`.
fn parse_go_i64(value: &str) -> Option<i64> {
    value.parse::<i64>().ok().or_else(|| {
        value
            .parse::<f64>()
            .ok()
            .filter(|number| number.is_finite())
            .map(|number| number as i64)
    })
}

#[derive(Clone, Debug, PartialEq)]
struct CheckinConfig {
    enabled: bool,
    min_quota: i64,
    max_quota: i64,
}

fn checkin_config_from_options(options: &BTreeMap<String, String>) -> CheckinConfig {
    CheckinConfig {
        enabled: options
            .get("checkin_setting.enabled")
            .and_then(|value| parse_go_bool(value))
            .unwrap_or(false),
        min_quota: options
            .get("checkin_setting.min_quota")
            .and_then(|value| parse_go_i64(value))
            .unwrap_or(DEFAULT_CHECKIN_MIN_QUOTA),
        max_quota: options
            .get("checkin_setting.max_quota")
            .and_then(|value| parse_go_i64(value))
            .unwrap_or(DEFAULT_CHECKIN_MAX_QUOTA),
    }
}

fn checkin_usd_log_quota(award: i64, quota_per_unit: f64) -> String {
    let quota_per_unit = if quota_per_unit > 0.0 {
        quota_per_unit
    } else {
        DEFAULT_QUOTA_PER_UNIT as f64
    };
    format!("＄{:.6} 额度", (award as f64) / quota_per_unit)
}

async fn checkin_config(pg: &PgPool) -> Result<CheckinConfig, ()> {
    // `ConfigManager.LoadFromDB` in frozen Go only collects option keys with
    // the case-sensitive `checkin_setting.` prefix.  Its historical JSON
    // aggregate key (`checkin_setting`) is deliberately ignored.
    let rows: Vec<(String, String)> =
        sqlx::query_as("SELECT key, value FROM options WHERE key = ANY($1)")
            .bind([
                "checkin_setting.enabled",
                "checkin_setting.min_quota",
                "checkin_setting.max_quota",
            ])
            .fetch_all(pg)
            .await
            .map_err(|_| ())?;
    let options = rows.into_iter().collect();
    Ok(checkin_config_from_options(&options))
}

fn date(now: i64, timezone: FixedOffset) -> String {
    chrono::DateTime::from_timestamp(now, 0)
        .unwrap_or(chrono::DateTime::UNIX_EPOCH)
        .with_timezone(&timezone)
        .format("%Y-%m-%d")
        .to_string()
}
fn month(now: i64, timezone: FixedOffset) -> String {
    chrono::DateTime::from_timestamp(now, 0)
        .unwrap_or(chrono::DateTime::UNIX_EPOCH)
        .with_timezone(&timezone)
        .format("%Y-%m")
        .to_string()
}

async fn checkin_status(
    State(state): State<IdentityCheckinAffState>,
    headers: HeaderMap,
    Query(query): Query<Month>,
) -> Response {
    // The frozen Go route only requires UserAuth; ordinary active users can
    // read their check-in history even when they do not have developer access.
    let actor = match user(&state, &headers).await {
        Ok(v) => v,
        Err(v) => return v,
    };
    let config = match checkin_config(&state.pg).await {
        Ok(v) => v,
        Err(_) => return fail("系统错误"),
    };
    if !config.enabled {
        return fail("签到功能未启用");
    }
    // Go's `DefaultQuery` only supplies the current month when the parameter
    // is absent; malformed or empty values are passed through verbatim.
    let requested = query
        .month
        .unwrap_or_else(|| state.calendar.month(state.clock.now()));
    let rows = match sqlx::query("SELECT checkin_date, quota_awarded FROM checkins WHERE user_id=$1 AND checkin_date >= $2 AND checkin_date <= $3 ORDER BY checkin_date DESC").bind(actor.id).bind(format!("{requested}-01")).bind(format!("{requested}-31")).fetch_all(&state.pg).await { Ok(v)=>v, Err(_)=>return fail("系统错误") };
    let records: Vec<Value> = rows.iter().map(|r| json!({"checkin_date":r.get::<String,_>("checkin_date"),"quota_awarded":r.get::<i64,_>("quota_awarded")})).collect();
    let total: i64 =
        sqlx::query_scalar("SELECT COALESCE(SUM(quota_awarded),0) FROM checkins WHERE user_id=$1")
            .bind(actor.id)
            .fetch_one(&state.pg)
            .await
            .unwrap_or(0);
    let count: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM checkins WHERE user_id=$1")
        .bind(actor.id)
        .fetch_one(&state.pg)
        .await
        .unwrap_or(0);
    let today = state.calendar.date(state.clock.now());
    let checked: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM checkins WHERE user_id=$1 AND checkin_date=$2)",
    )
    .bind(actor.id)
    .bind(today)
    .fetch_one(&state.pg)
    .await
    .unwrap_or(false);
    checkin_status_ok(json!({
        "enabled": config.enabled,
        "min_quota": config.min_quota,
        "max_quota": config.max_quota,
        "stats":{"total_quota":total,"total_checkins":count,"checkin_count":records.len(),"checked_in_today":checked,"records":records}
    }))
}

async fn checkin(State(state): State<IdentityCheckinAffState>, headers: HeaderMap) -> Response {
    let actor = match user(&state, &headers).await {
        Ok(v) => v,
        Err(v) => return v,
    };
    let config = match checkin_config(&state.pg).await {
        Ok(v) => v,
        Err(_) => return fail("系统错误"),
    };
    if !config.enabled {
        return fail("签到功能未启用");
    }
    // The pinned Go route uses the configured operation-setting range here.
    // It does not apply the separate trust-level pricing projection used by
    // newer Go revisions, so keep the write path on the same raw values as
    // the legacy endpoint and its response contract.
    if config.min_quota < 0 || config.max_quota < config.min_quota {
        return fail("签到失败，请稍后重试");
    }
    let today = state.calendar.date(state.clock.now());
    let award = state.awarder.award(config.min_quota, config.max_quota);
    let mut tx = match state.pg.begin().await {
        Ok(v) => v,
        Err(_) => return fail("签到失败，请稍后重试"),
    };
    let inserted=sqlx::query("INSERT INTO checkins(user_id,checkin_date,quota_awarded,created_at) VALUES($1,$2,$3,$4) ON CONFLICT (user_id,checkin_date) DO NOTHING").bind(actor.id).bind(&today).bind(award).bind(state.clock.now()).execute(&mut *tx).await;
    match inserted {
        Ok(v) if v.rows_affected() == 0 => return fail("今日已签到"),
        Ok(_) => {}
        Err(_) => return fail("签到失败，请稍后重试"),
    }
    if sqlx::query("UPDATE users SET quota=quota+$1 WHERE id=$2 AND deleted_at IS NULL")
        .bind(award)
        .bind(actor.id)
        .execute(&mut *tx)
        .await
        .map_or(true, |v| v.rows_affected() != 1)
    {
        return fail("签到失败：更新额度出错");
    }
    if tx.commit().await.is_err() {
        return fail("签到失败，请稍后重试");
    }
    state
        .effects
        .committed(actor.id, &actor.username, award, state.clock.now())
        .await;
    Json(json!({"success":true,"message":"签到成功","data":{"quota_awarded":award,"checkin_date":today}})).into_response()
}

async fn compliance(pg: &PgPool) -> Result<bool, ()> {
    let rows = sqlx::query(
        "SELECT key, value FROM options WHERE key IN ('payment_setting.compliance_confirmed', 'payment_setting.compliance_terms_version')",
    )
    .fetch_all(pg)
    .await
    .map_err(|_| ())?;
    let mut options = BTreeMap::new();
    for row in rows {
        options.insert(
            row.try_get::<String, _>("key").map_err(|_| ())?,
            row.try_get::<String, _>("value").map_err(|_| ())?,
        );
    }
    Ok(payment_compliance(&options))
}

fn payment_compliance(options: &BTreeMap<String, String>) -> bool {
    options
        .get("payment_setting.compliance_confirmed")
        .is_some_and(|value| value.eq_ignore_ascii_case("true") || value == "1")
        && options
            .get("payment_setting.compliance_terms_version")
            .is_some_and(|value| value == "v1")
}
fn compliance_message(headers: &HeaderMap) -> &'static str {
    if headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|v| v.to_str().ok())
        .is_some_and(|v| v.to_ascii_lowercase().starts_with("zh"))
    {
        "支付、兑换码、订阅计划和邀请返利功能已禁用。管理员需先确认合规声明后方可启用。"
    } else {
        "Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them."
    }
}
fn transfer_success_message(headers: &HeaderMap) -> &'static str {
    let language = headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .map(str::to_ascii_lowercase)
        .unwrap_or_default();
    if language.starts_with("zh-tw") {
        "劃轉成功"
    } else if language.starts_with("zh") {
        "划转成功"
    } else {
        "Transfer successful"
    }
}
fn transfer_failure_message(headers: &HeaderMap, error: impl std::fmt::Display) -> String {
    let language = headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .map(str::to_ascii_lowercase)
        .unwrap_or_default();
    let prefix = if language.starts_with("zh-tw") {
        "劃轉失敗"
    } else if language.starts_with("zh") {
        "划转失败"
    } else {
        "Transfer failed"
    };
    format!("{prefix} {error}")
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
#[derive(Deserialize)]
struct Quota {
    quota: i64,
}
async fn aff_transfer(State(state): State<IdentityCheckinAffState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let actor = match user(&state, &headers).await {
        Ok(v) => v,
        Err(v) => return v,
    };
    match compliance(&state.pg).await {
        Ok(true) => {}
        Ok(false) => return fail(compliance_message(&headers)),
        Err(_) => return fail("系统错误"),
    };
    // Gin's authentication middleware has already established the actor
    // before the handler checks compliance. Reading the body only after the
    // gate preserves the handler's no-mutation rejection path.
    let body: Quota = match to_bytes(request.into_body(), 1024 * 1024)
        .await
        .ok()
        .and_then(|bytes| serde_json::from_slice(&bytes).ok())
    {
        Some(v) => v,
        None => return fail("invalid parameters"),
    };
    let qpu = option_f64(&state.pg, "QuotaPerUnit", DEFAULT_QUOTA_PER_UNIT as f64).await;
    if (body.quota as f64) < qpu {
        return fail(transfer_failure_message(
            &headers,
            format!("转移额度最小为{qpu}！"),
        ));
    }
    let mut tx = match state.pg.begin().await {
        Ok(v) => v,
        Err(error) => {
            return fail(transfer_failure_message(
                &headers,
                legacy_database_error(&error),
            ));
        }
    };
    // Frozen Go loads the complete `User` value and saves it through GORM.
    // PostgreSQL NULLs scanned into non-nullable Go scalar fields therefore
    // become their zero values in the same durable write as the balance move.
    let changed = sqlx::query(
        r#"UPDATE users
           SET aff_quota = aff_quota - $1,
               quota = quota + $1,
               aff_code = COALESCE(aff_code, ''),
               discord_id = COALESCE(discord_id, ''),
               github_id = COALESCE(github_id, ''),
               inviter_id = COALESCE(inviter_id, 0),
               linux_do_id = COALESCE(linux_do_id, ''),
               oidc_id = COALESCE(oidc_id, ''),
               remark = COALESCE(remark, ''),
               stripe_customer = COALESCE(stripe_customer, ''),
               telegram_id = COALESCE(telegram_id, ''),
               wechat_id = COALESCE(wechat_id, '')
           WHERE id = $2 AND deleted_at IS NULL AND aff_quota >= $1"#,
    )
    .bind(body.quota)
    .bind(actor.id)
    .execute(&mut *tx)
    .await;
    match changed {
        Ok(result) if result.rows_affected() == 1 => {}
        Ok(_) => return fail(transfer_failure_message(&headers, "邀请额度不足！")),
        Err(error) => {
            return fail(transfer_failure_message(
                &headers,
                legacy_database_error(&error),
            ));
        }
    }
    if let Err(error) = tx.commit().await {
        return fail(transfer_failure_message(
            &headers,
            legacy_database_error(&error),
        ));
    }
    Json(json!({
        "success": true,
        "message": transfer_success_message(&headers),
        "data": Value::Null,
    }))
    .into_response()
}
async fn option_i64(pg: &PgPool, key: &str, default: i64) -> i64 {
    sqlx::query_scalar::<_, Option<String>>("SELECT value FROM options WHERE key=$1")
        .bind(key)
        .fetch_optional(pg)
        .await
        .ok()
        .flatten()
        .flatten()
        .and_then(|v| v.parse().ok())
        .filter(|v: &i64| *v > 0)
        .unwrap_or(default)
}
#[derive(Deserialize)]
struct Amount {
    amount: i64,
}
async fn amount(State(state): State<IdentityCheckinAffState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let actor = match user(&state, &headers).await {
        Ok(v) => v,
        Err(v) => return v,
    };
    let request: Amount = match to_bytes(request.into_body(), 1024 * 1024)
        .await
        .ok()
        .and_then(|bytes| serde_json::from_slice(&bytes).ok())
    {
        Some(v) => v,
        None => return legacy("error", "参数错误"),
    };
    let min = option_i64(&state.pg, "MinTopUp", 1).await;
    let display_type = match option_string(&state.pg, "general_setting.quota_display_type").await {
        Some(value) => value,
        None => option_string(&state.pg, "general_setting")
            .await
            .as_deref()
            .map(|value| quota_display_type_from_options(None, Some(value)))
            .unwrap_or_else(|| "USD".to_owned()),
    };
    let quota_per_unit = option_f64(&state.pg, "QuotaPerUnit", DEFAULT_QUOTA_PER_UNIT as f64).await;
    let minimum = topup_minimum(min, &display_type, quota_per_unit);
    if request.amount < minimum {
        return legacy("error", format!("充值数量不能小于 {minimum}"));
    }
    let group: Option<String> =
        match sqlx::query_scalar("SELECT \"group\" FROM users WHERE id=$1 AND deleted_at IS NULL")
            .bind(actor.id)
            .fetch_optional(&state.pg)
            .await
        {
            Ok(v) => v.flatten(),
            Err(_) => return legacy("error", "获取用户分组失败"),
        };
    let Some(group) = group else {
        return legacy("error", "获取用户分组失败");
    };
    let price = option_f64(&state.pg, "Price", 1.0).await;
    let ratios: Value = sqlx::query_scalar::<_, Option<String>>(
        "SELECT value FROM options WHERE key='TopupGroupRatio'",
    )
    .fetch_optional(&state.pg)
    .await
    .ok()
    .flatten()
    .flatten()
    .and_then(|v| serde_json::from_str(&v).ok())
    .unwrap_or(Value::Null);
    let ratio = ratios
        .get(&group)
        .and_then(Value::as_f64)
        .filter(|v| *v > 0.0)
        .unwrap_or(1.0);
    let amount_discount = option_string(&state.pg, "payment_setting.amount_discount").await;
    let legacy_payment_setting = if amount_discount.is_none() {
        option_string(&state.pg, "payment_setting").await
    } else {
        None
    };
    // Go uses the positive amount-specific discount, with the dotted
    // registered option taking precedence over the legacy aggregate shape.
    let discount = amount_discount_from_options(
        amount_discount.as_deref(),
        legacy_payment_setting.as_deref(),
        request.amount,
    );
    let amount_in_currency = if display_type == "TOKENS" {
        (request.amount as f64) / quota_per_unit
    } else {
        request.amount as f64
    };
    let money = amount_in_currency * price * ratio * discount;
    if money <= 0.01 {
        return legacy("error", "充值金额过低");
    };
    legacy("success", format!("{money:.2}"))
}

fn topup_minimum(min_topup: i64, display_type: &str, quota_per_unit: f64) -> i64 {
    if display_type == "TOKENS" {
        ((min_topup as f64) * quota_per_unit)
            .trunc()
            .clamp(i64::MIN as f64, i64::MAX as f64) as i64
    } else {
        min_topup
    }
}

fn quota_display_type_from_options(dotted: Option<&str>, aggregate: Option<&str>) -> String {
    dotted
        .and_then(|value| (!value.trim().is_empty()).then(|| value.to_owned()))
        .or_else(|| {
            aggregate
                .and_then(|raw| serde_json::from_str::<Value>(raw).ok())
                .and_then(|value| {
                    value
                        .get("quota_display_type")
                        .and_then(Value::as_str)
                        .map(str::to_owned)
                })
                .filter(|value| !value.trim().is_empty())
        })
        .unwrap_or_else(|| "USD".to_owned())
}

fn amount_discount_from_options(dotted: Option<&str>, aggregate: Option<&str>, amount: i64) -> f64 {
    dotted
        .and_then(|raw| serde_json::from_str::<Value>(raw).ok())
        .and_then(|value| value.get(amount.to_string()).and_then(Value::as_f64))
        .or_else(|| {
            aggregate
                .and_then(|raw| serde_json::from_str::<Value>(raw).ok())
                .and_then(|value| value.get("amount_discount").cloned())
                .and_then(|value| value.get(amount.to_string()).and_then(Value::as_f64))
        })
        .filter(|value| value.is_finite() && *value > 0.0)
        .unwrap_or(1.0)
}

async fn option_string(pg: &PgPool, key: &str) -> Option<String> {
    sqlx::query_scalar::<_, Option<String>>("SELECT value FROM options WHERE key=$1")
        .bind(key)
        .fetch_optional(pg)
        .await
        .ok()
        .flatten()
        .flatten()
}
async fn option_f64(pg: &PgPool, key: &str, default: f64) -> f64 {
    sqlx::query_scalar::<_, Option<String>>("SELECT value FROM options WHERE key=$1")
        .bind(key)
        .fetch_optional(pg)
        .await
        .ok()
        .flatten()
        .flatten()
        .and_then(|v| v.parse().ok())
        .filter(|v: &f64| v.is_finite())
        .unwrap_or(default)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::migration_routes::test_support::RejectingDashboardAuth;
    use axum::{
        body::{Body, to_bytes},
        http::Request,
    };
    use tower::ServiceExt;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    fn test_pool() -> Result<PgPool, sqlx::Error> {
        PgPool::connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
    }

    fn app() -> Result<Router, sqlx::Error> {
        Ok(router(IdentityCheckinAffState::new(
            test_pool()?,
            Arc::new(RejectingDashboardAuth),
        )))
    }

    fn read_app() -> Result<Router, sqlx::Error> {
        Ok(read_router(IdentityCheckinAffState::new(
            test_pool()?,
            Arc::new(RejectingDashboardAuth),
        )))
    }

    #[tokio::test]
    async fn every_checkin_and_affiliate_public_seam_requires_a_dashboard_credential() -> TestResult
    {
        for (method, uri) in [
            ("GET", "/api/user/checkin"),
            ("POST", "/api/user/checkin"),
            ("POST", "/api/user/aff_transfer"),
            ("POST", "/api/user/amount"),
        ] {
            let request = Request::builder()
                .method(method)
                .uri(uri)
                .body(Body::empty())?;
            let response = app()?.oneshot(request).await?;
            assert_eq!(
                response.status(),
                StatusCode::UNAUTHORIZED,
                "{method} {uri}"
            );
            let body = to_bytes(response.into_body(), 1024).await?;
            assert_eq!(
                serde_json::from_slice::<Value>(&body)?,
                json!({"success":false,"code":"AUTH_UNAUTHORIZED","message":"Unauthorized, invalid access token"}),
                "{method} {uri}"
            );
        }
        Ok(())
    }

    #[tokio::test]
    async fn read_router_does_not_expose_quota_changing_methods() -> TestResult {
        for (method, uri, expected_status) in [
            ("POST", "/api/user/checkin", StatusCode::METHOD_NOT_ALLOWED),
            ("POST", "/api/user/aff_transfer", StatusCode::NOT_FOUND),
            ("POST", "/api/user/amount", StatusCode::NOT_FOUND),
        ] {
            let request = Request::builder()
                .method(method)
                .uri(uri)
                .body(Body::empty())?;
            let response = read_app()?.oneshot(request).await?;
            assert_eq!(response.status(), expected_status, "{method} {uri}");
        }
        Ok(())
    }

    #[test]
    fn checkin_calendar_uses_an_injected_process_local_timezone_across_midnight() -> TestResult {
        // 2026-08-01T16:30:00Z is already 2026-08-02 in the Go process's
        // configured UTC+8 local timezone.
        let now = 1_785_601_800;
        let utc_plus_eight = FixedOffset::east_opt(8 * 60 * 60)
            .ok_or_else(|| std::io::Error::other("invalid UTC+8 test offset"))?;
        assert_eq!(date(now, utc_plus_eight), "2026-08-02");
        assert_eq!(month(now, utc_plus_eight), "2026-08");
        Ok(())
    }

    #[test]
    fn checkin_config_defaults_match_the_frozen_go_setting() {
        let setting = BTreeMap::new();
        assert_eq!(
            checkin_config_from_options(&setting),
            CheckinConfig {
                enabled: false,
                min_quota: 1_000,
                max_quota: 10_000,
            }
        );
    }

    #[test]
    fn checkin_config_reads_lowercase_flattened_go_keys() {
        let setting = BTreeMap::from([
            ("checkin_setting.enabled".to_owned(), "TRUE".to_owned()),
            (
                "checkin_setting.min_quota".to_owned(),
                "100.000000".to_owned(),
            ),
            ("checkin_setting.max_quota".to_owned(), "250".to_owned()),
            (
                "checkin_setting.level_multipliers".to_owned(),
                "[0.25,0.5,0.75,0.9,1]".to_owned(),
            ),
        ]);
        assert_eq!(
            checkin_config_from_options(&setting),
            CheckinConfig {
                enabled: true,
                min_quota: 100,
                max_quota: 250,
            }
        );
    }

    #[test]
    fn dashboard_token_matches_go_bare_and_bearer_forms() -> TestResult {
        for value in ["token", "Bearer token", "bearer token"] {
            let mut headers = HeaderMap::new();
            headers.insert(header::AUTHORIZATION, value.parse()?);
            assert_eq!(
                dashboard_token(&headers).as_deref(),
                Some("token"),
                "{value}"
            );
        }
        let mut headers = HeaderMap::new();
        headers.insert(header::AUTHORIZATION, "Bearer token extra".parse()?);
        assert_eq!(dashboard_token(&headers), None);
        Ok(())
    }

    #[test]
    fn checkin_config_ignores_legacy_aggregate_and_case_mismatched_keys() {
        let setting = BTreeMap::from([
            (
                "checkin_setting".to_owned(),
                r#"{"enabled":true,"min_quota":100,"max_quota":250}"#.to_owned(),
            ),
            ("checkin_setting.Enabled".to_owned(), "true".to_owned()),
            ("checkin_setting.MinQuota".to_owned(), "100".to_owned()),
            ("checkin_setting.MaxQuota".to_owned(), "250".to_owned()),
        ]);
        assert_eq!(
            checkin_config_from_options(&setting),
            CheckinConfig {
                enabled: false,
                min_quota: DEFAULT_CHECKIN_MIN_QUOTA,
                max_quota: DEFAULT_CHECKIN_MAX_QUOTA,
            }
        );
    }

    #[tokio::test]
    async fn checkin_status_success_omits_message_like_frozen_go() -> TestResult {
        let response = checkin_status_ok(json!({"enabled": true}));
        assert_eq!(response.status(), StatusCode::OK);
        let body = to_bytes(response.into_body(), 1024).await?;
        assert_eq!(
            serde_json::from_slice::<Value>(&body)?,
            json!({"success": true, "data": {"enabled": true}})
        );
        Ok(())
    }

    #[test]
    fn checkin_system_log_uses_the_frozen_go_usd_quota_format() {
        assert_eq!(checkin_usd_log_quota(100, 500_000.0), "＄0.000200 额度");
    }

    #[test]
    fn affiliate_transfer_fixture_is_disabled_without_flattened_compliance_options() {
        let options = BTreeMap::from([(
            "payment_setting".to_owned(),
            r#"{"compliance_confirmed":true,"compliance_terms_version":"v1"}"#.to_owned(),
        )]);
        assert!(!payment_compliance(&options));
    }

    #[test]
    fn affiliate_transfer_accepts_only_confirmed_current_flattened_compliance_options() {
        let options = BTreeMap::from([
            (
                "payment_setting.compliance_confirmed".to_owned(),
                "true".to_owned(),
            ),
            (
                "payment_setting.compliance_terms_version".to_owned(),
                "v1".to_owned(),
            ),
        ]);
        assert!(payment_compliance(&options));
    }

    #[tokio::test]
    async fn affiliate_transfer_compliance_rejection_matches_go_and_has_no_side_effects()
    -> TestResult {
        let options = BTreeMap::new();
        assert!(!payment_compliance(&options));
        // The gate runs before body binding or the transfer transaction, so a
        // rejected request has no durable affiliate or log side effect.
        let response = fail(compliance_message(&HeaderMap::new()));
        assert_eq!(response.status(), StatusCode::OK);
        let body = to_bytes(response.into_body(), 1024).await?;
        assert_eq!(
            serde_json::from_slice::<Value>(&body)?,
            json!({
                "success": false,
                "message": "Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them."
            })
        );
        Ok(())
    }

    #[test]
    fn affiliate_transfer_success_message_matches_go_locales() -> TestResult {
        let mut headers = HeaderMap::new();
        assert_eq!(transfer_success_message(&headers), "Transfer successful");
        assert_eq!(
            transfer_failure_message(&headers, "邀请额度不足！"),
            "Transfer failed 邀请额度不足！"
        );
        headers.insert(header::ACCEPT_LANGUAGE, "zh-CN".parse()?);
        assert_eq!(transfer_success_message(&headers), "划转成功");
        assert_eq!(
            transfer_failure_message(&headers, "邀请额度不足！"),
            "划转失败 邀请额度不足！"
        );
        headers.insert(header::ACCEPT_LANGUAGE, "zh-TW".parse()?);
        assert_eq!(transfer_success_message(&headers), "劃轉成功");
        assert_eq!(
            transfer_failure_message(&headers, "邀请额度不足！"),
            "劃轉失敗 邀请额度不足！"
        );
        Ok(())
    }

    #[test]
    fn token_display_topup_minimum_uses_the_current_quota_per_unit() {
        assert_eq!(topup_minimum(2, "TOKENS", 1_234.5), 2_469);
        assert_eq!(topup_minimum(2, "USD", 1_234.5), 2);
    }

    #[test]
    fn amount_quote_prefers_go_dotted_settings_and_keeps_aggregate_fallback() {
        assert_eq!(
            quota_display_type_from_options(
                Some("TOKENS"),
                Some(r#"{"quota_display_type":"CNY"}"#),
            ),
            "TOKENS"
        );
        assert_eq!(
            quota_display_type_from_options(None, Some(r#"{"quota_display_type":"CNY"}"#)),
            "CNY"
        );
        assert_eq!(
            amount_discount_from_options(
                Some(r#"{"100":0.8}"#),
                Some(r#"{"amount_discount":{"100":0.7}}"#),
                100,
            ),
            0.8
        );
        assert_eq!(
            amount_discount_from_options(None, Some(r#"{"amount_discount":{"100":0.7}}"#), 100),
            0.7
        );
        assert_eq!(
            amount_discount_from_options(Some("invalid"), None, 100),
            1.0
        );
    }
}
