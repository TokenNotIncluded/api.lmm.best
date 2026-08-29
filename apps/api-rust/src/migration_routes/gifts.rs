//! Legacy-compatible compensation-gift discovery, claiming, and administration.
//!
//! A new claim and its quota grant commit in one PostgreSQL transaction.  The
//! derived Valkey quota field and observability logs are updated only after the
//! durable transaction commits and never change the claim response on failure.

use std::{
    collections::HashMap,
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::{Bytes, to_bytes},
    extract::{Path, RawQuery, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post, put},
};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{PgPool, Postgres, Row, Transaction, postgres::PgRow};

use crate::{
    ClientIpKey,
    auth::{
        CriticalRateLimitOutcome, DashboardAuth, DashboardUserView, UserAuthPolicyError,
        dashboard_token_candidate, enforce_user_auth_view,
    },
    legacy_empty_response,
};

const ADMIN_ROLE: i64 = 10;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const BODY_LIMIT_BYTES: usize = 64 * 1024 * 1024;
const GIFT_EXPIRY_GRACE_SECONDS: i64 = 7 * 86_400;
const DEFAULT_PAGE: i64 = 1;
const DEFAULT_PAGE_SIZE: i64 = 20;
const MAX_PAGE_SIZE: i64 = 100;
const DEFAULT_QUOTA_PER_UNIT: f64 = 500_000.0;
const LOG_TYPE_TOPUP: i64 = 1;
const LOG_TYPE_MANAGE: i64 = 3;

/// PostgreSQL, Valkey, and dashboard-auth dependencies for compensation gifts.
#[derive(Clone)]
pub struct GiftState {
    store: Arc<dyn GiftStore>,
    effects: Arc<dyn GiftEffects>,
    auth: Arc<dyn DashboardAuth>,
    clock: Arc<dyn GiftClock>,
}

impl GiftState {
    /// Creates production state backed by the listener's shared dependencies.
    #[must_use]
    pub fn new(pg: PgPool, valkey: redis::Client, auth: Arc<dyn DashboardAuth>) -> Self {
        Self {
            store: Arc::new(PgGiftStore { pg: pg.clone() }),
            effects: Arc::new(PgValkeyGiftEffects { pg, valkey }),
            auth,
            clock: Arc::new(SystemGiftClock),
        }
    }
}

/// Builds the two user and four administrator compensation-gift routes.
pub fn router(state: GiftState) -> Router {
    Router::new()
        .route("/api/user/gift", get(available_gifts))
        .route("/api/user/gift/{id}/claim", post(claim_gift))
        .route("/api/gift/", get(admin_list_gifts).post(admin_create_gift))
        .route("/api/gift/{id}", put(admin_update_gift))
        .route("/api/gift/claims", get(admin_list_claims))
        .with_state(state)
}

#[derive(Clone, Debug)]
struct Principal {
    user: DashboardUserView,
    credential: String,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
struct Gift {
    id: i64,
    title: String,
    description: String,
    quota: i64,
    start_at: i64,
    end_at: i64,
    min_used_quota: i64,
    min_account_age_days: i64,
    enabled: bool,
    created_at: i64,
}

impl Gift {
    fn from_row(row: PgRow) -> Result<Self, sqlx::Error> {
        Ok(Self {
            id: row.try_get("id")?,
            title: row.try_get("title")?,
            description: row.try_get("description")?,
            quota: row.try_get("quota")?,
            start_at: row.try_get("start_at")?,
            end_at: row.try_get("end_at")?,
            min_used_quota: row.try_get("min_used_quota")?,
            min_account_age_days: row.try_get("min_account_age_days")?,
            enabled: row.try_get("enabled")?,
            created_at: row.try_get("created_at")?,
        })
    }
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
struct GiftClaim {
    id: i64,
    gift_id: i64,
    user_id: i64,
    username: String,
    quota: i64,
    created_at: i64,
}

impl GiftClaim {
    fn from_row(row: PgRow) -> Result<Self, sqlx::Error> {
        Ok(Self {
            id: row.try_get("id")?,
            gift_id: row.try_get("gift_id")?,
            user_id: row.try_get("user_id")?,
            username: row.try_get("username")?,
            quota: row.try_get("quota")?,
            created_at: row.try_get("created_at")?,
        })
    }
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
struct GiftWithClaimStatus {
    #[serde(flatten)]
    gift: Gift,
    claimed: bool,
    #[serde(skip_serializing_if = "is_zero")]
    claimed_at: i64,
    eligible: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    reason: String,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
struct GiftUser {
    created_at: i64,
    used_quota: i64,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct GiftInput {
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    title: String,
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    description: String,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    quota: i64,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    start_at: i64,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    end_at: i64,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    min_used_quota: i64,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    min_account_age_days: i64,
    #[serde(default)]
    enabled: Option<bool>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
struct ClaimsPage {
    gift_id: i64,
    page: i64,
    page_size: i64,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
struct ClaimsResult {
    items: Vec<GiftClaim>,
    total: i64,
    page: i64,
    page_size: i64,
}

#[derive(Clone, Debug, Eq, PartialEq)]
enum ClaimOutcome {
    Granted(GiftClaim),
    AlreadyClaimed(GiftClaim),
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, thiserror::Error)]
enum GiftEligibilityError {
    #[error("礼包不存在或未启用")]
    NotFound,
    #[error("礼包尚未开始")]
    NotStarted,
    #[error("礼包已过期")]
    Expired,
    #[error("不满足领取条件")]
    NotEligible,
}

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
enum GiftStoreError {
    #[error("礼包不存在")]
    NotFound,
    #[error("{0}")]
    Eligibility(#[from] GiftEligibilityError),
    #[error("{0}")]
    Database(String),
}

#[async_trait]
trait GiftStore: Send + Sync {
    async fn available(
        &self,
        user_id: i64,
        now: i64,
    ) -> Result<Vec<GiftWithClaimStatus>, GiftStoreError>;

    async fn claim(
        &self,
        user_id: i64,
        gift_id: i64,
        now: i64,
    ) -> Result<ClaimOutcome, GiftStoreError>;

    async fn list(&self) -> Result<Vec<Gift>, GiftStoreError>;

    async fn create(&self, input: &GiftInput, now: i64) -> Result<Gift, GiftStoreError>;

    async fn update(&self, gift_id: i64, input: &GiftInput) -> Result<Gift, GiftStoreError>;

    async fn claims(&self, page: ClaimsPage) -> Result<ClaimsResult, GiftStoreError>;
}

#[derive(Clone)]
struct PgGiftStore {
    pg: PgPool,
}

#[async_trait]
impl GiftStore for PgGiftStore {
    async fn available(
        &self,
        user_id: i64,
        now: i64,
    ) -> Result<Vec<GiftWithClaimStatus>, GiftStoreError> {
        let user = load_user(&self.pg, user_id).await?;
        let rows = sqlx::query(
            "SELECT id::BIGINT AS id, COALESCE(title, '') AS title, \
             COALESCE(description, '') AS description, quota::BIGINT AS quota, \
             start_at::BIGINT AS start_at, end_at::BIGINT AS end_at, \
             min_used_quota::BIGINT AS min_used_quota, \
             min_account_age_days::BIGINT AS min_account_age_days, enabled, \
             COALESCE(created_at, 0)::BIGINT AS created_at \
             FROM gifts WHERE enabled = TRUE AND end_at > $1 \
             ORDER BY id DESC",
        )
        .bind(now.saturating_sub(GIFT_EXPIRY_GRACE_SECONDS))
        .fetch_all(&self.pg)
        .await
        .map_err(database_error)?;
        let gifts = rows
            .into_iter()
            .map(Gift::from_row)
            .collect::<Result<Vec<_>, _>>()
            .map_err(database_error)?;
        let gift_ids = gifts.iter().map(|gift| gift.id).collect::<Vec<_>>();
        let claimed = if gift_ids.is_empty() {
            HashMap::new()
        } else {
            let rows = sqlx::query(
                "SELECT gift_id::BIGINT AS gift_id, COALESCE(created_at, 0)::BIGINT AS created_at \
                 FROM gift_claims WHERE user_id = $1 AND gift_id = ANY($2)",
            )
            .bind(user_id)
            .bind(&gift_ids)
            .fetch_all(&self.pg)
            .await
            .map_err(database_error)?;
            rows.into_iter()
                .map(|row| {
                    Ok((
                        row.try_get::<i64, _>("gift_id")?,
                        row.try_get::<i64, _>("created_at")?,
                    ))
                })
                .collect::<Result<HashMap<_, _>, sqlx::Error>>()
                .map_err(database_error)?
        };
        Ok(gifts
            .into_iter()
            .map(|gift| {
                let claim = claimed.get(&gift.id).copied();
                let claimed_at = claim.unwrap_or_default();
                let eligibility = gift_eligibility(&gift, user, now);
                GiftWithClaimStatus {
                    gift,
                    claimed: claim.is_some(),
                    claimed_at,
                    eligible: eligibility.is_ok(),
                    reason: eligibility
                        .err()
                        .map_or_else(String::new, |error| error.to_string()),
                }
            })
            .collect())
    }

    async fn claim(
        &self,
        user_id: i64,
        gift_id: i64,
        now: i64,
    ) -> Result<ClaimOutcome, GiftStoreError> {
        let mut transaction = self.pg.begin().await.map_err(database_error)?;
        let user = load_user_for_update(&mut transaction, user_id).await?;
        let gift = load_gift_for_update(&mut transaction, gift_id)
            .await?
            .ok_or(GiftEligibilityError::NotFound)?;
        gift_eligibility(&gift, GiftUser::from(&user), now)?;
        let row = sqlx::query(
            "INSERT INTO gift_claims (gift_id, user_id, username, quota, created_at) \
             VALUES ($1, $2, $3, $4, $5) \
             ON CONFLICT (gift_id, user_id) DO NOTHING \
             RETURNING id::BIGINT AS id, gift_id::BIGINT AS gift_id, \
             user_id::BIGINT AS user_id, COALESCE(username, '') AS username, \
             quota::BIGINT AS quota, COALESCE(created_at, 0)::BIGINT AS created_at",
        )
        .bind(gift_id)
        .bind(user_id)
        .bind(&user.username)
        .bind(gift.quota)
        .bind(now)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(database_error)?;
        let Some(row) = row else {
            let existing = load_existing_claim(&mut transaction, user_id, gift_id)
                .await?
                .ok_or_else(|| {
                    GiftStoreError::Database("gift claim conflict could not be loaded".to_owned())
                })?;
            transaction.commit().await.map_err(database_error)?;
            return Ok(ClaimOutcome::AlreadyClaimed(existing));
        };
        let claim = GiftClaim::from_row(row).map_err(database_error)?;
        let updated =
            sqlx::query("UPDATE users SET quota = quota + $1 WHERE id = $2 AND deleted_at IS NULL")
                .bind(gift.quota)
                .bind(user_id)
                .execute(&mut *transaction)
                .await
                .map_err(database_error)?;
        if updated.rows_affected() != 1 {
            return Err(GiftStoreError::Database(
                "领取失败：更新额度出错".to_owned(),
            ));
        }
        transaction.commit().await.map_err(database_error)?;
        Ok(ClaimOutcome::Granted(claim))
    }

    async fn list(&self) -> Result<Vec<Gift>, GiftStoreError> {
        let rows = sqlx::query(&format!(
            "SELECT {GIFT_COLUMNS} FROM gifts ORDER BY id DESC"
        ))
        .fetch_all(&self.pg)
        .await
        .map_err(database_error)?;
        rows.into_iter()
            .map(Gift::from_row)
            .collect::<Result<Vec<_>, _>>()
            .map_err(database_error)
    }

    async fn create(&self, input: &GiftInput, now: i64) -> Result<Gift, GiftStoreError> {
        let row = sqlx::query(
            "INSERT INTO gifts (title, description, quota, start_at, end_at, \
             min_used_quota, min_account_age_days, enabled, created_at) \
             VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) \
             RETURNING id::BIGINT AS id, COALESCE(title, '') AS title, \
             COALESCE(description, '') AS description, quota::BIGINT AS quota, \
             start_at::BIGINT AS start_at, end_at::BIGINT AS end_at, \
             min_used_quota::BIGINT AS min_used_quota, \
             min_account_age_days::BIGINT AS min_account_age_days, enabled, \
             COALESCE(created_at, 0)::BIGINT AS created_at",
        )
        .bind(&input.title)
        .bind(&input.description)
        .bind(input.quota)
        .bind(input.start_at)
        .bind(input.end_at)
        .bind(input.min_used_quota)
        .bind(input.min_account_age_days)
        .bind(input.enabled.unwrap_or(true))
        .bind(now)
        .fetch_one(&self.pg)
        .await
        .map_err(database_error)?;
        Gift::from_row(row).map_err(database_error)
    }

    async fn update(&self, gift_id: i64, input: &GiftInput) -> Result<Gift, GiftStoreError> {
        let mut transaction = self.pg.begin().await.map_err(database_error)?;
        let existing = load_gift_for_update(&mut transaction, gift_id)
            .await?
            .ok_or(GiftStoreError::NotFound)?;
        let enabled = input.enabled.unwrap_or(existing.enabled);
        let row = sqlx::query(
            "UPDATE gifts SET title = $1, description = $2, quota = $3, \
             start_at = $4, end_at = $5, min_used_quota = $6, \
             min_account_age_days = $7, enabled = $8 WHERE id = $9 \
             RETURNING id::BIGINT AS id, COALESCE(title, '') AS title, \
             COALESCE(description, '') AS description, quota::BIGINT AS quota, \
             start_at::BIGINT AS start_at, end_at::BIGINT AS end_at, \
             min_used_quota::BIGINT AS min_used_quota, \
             min_account_age_days::BIGINT AS min_account_age_days, enabled, \
             COALESCE(created_at, 0)::BIGINT AS created_at",
        )
        .bind(&input.title)
        .bind(&input.description)
        .bind(input.quota)
        .bind(input.start_at)
        .bind(input.end_at)
        .bind(input.min_used_quota)
        .bind(input.min_account_age_days)
        .bind(enabled)
        .bind(gift_id)
        .fetch_one(&mut *transaction)
        .await
        .map_err(database_error)?;
        let gift = Gift::from_row(row).map_err(database_error)?;
        transaction.commit().await.map_err(database_error)?;
        Ok(gift)
    }

    async fn claims(&self, page: ClaimsPage) -> Result<ClaimsResult, GiftStoreError> {
        let total = if page.gift_id > 0 {
            sqlx::query_scalar::<_, i64>(
                "SELECT COUNT(*)::BIGINT FROM gift_claims WHERE gift_id = $1",
            )
            .bind(page.gift_id)
            .fetch_one(&self.pg)
            .await
            .map_err(database_error)?
        } else {
            sqlx::query_scalar::<_, i64>("SELECT COUNT(*)::BIGINT FROM gift_claims")
                .fetch_one(&self.pg)
                .await
                .map_err(database_error)?
        };
        let offset = page.page.saturating_sub(1).saturating_mul(page.page_size);
        let rows = if page.gift_id > 0 {
            sqlx::query(&format!(
                "SELECT {CLAIM_COLUMNS} FROM gift_claims WHERE gift_id = $1 \
                 ORDER BY id DESC OFFSET $2 LIMIT $3"
            ))
            .bind(page.gift_id)
            .bind(offset)
            .bind(page.page_size)
            .fetch_all(&self.pg)
            .await
            .map_err(database_error)?
        } else {
            sqlx::query(&format!(
                "SELECT {CLAIM_COLUMNS} FROM gift_claims ORDER BY id DESC OFFSET $1 LIMIT $2"
            ))
            .bind(offset)
            .bind(page.page_size)
            .fetch_all(&self.pg)
            .await
            .map_err(database_error)?
        };
        let items = rows
            .into_iter()
            .map(GiftClaim::from_row)
            .collect::<Result<Vec<_>, _>>()
            .map_err(database_error)?;
        Ok(ClaimsResult {
            items,
            total,
            page: page.page,
            page_size: page.page_size,
        })
    }
}

const GIFT_COLUMNS: &str = "id::BIGINT AS id, COALESCE(title, '') AS title, \
    COALESCE(description, '') AS description, quota::BIGINT AS quota, \
    start_at::BIGINT AS start_at, end_at::BIGINT AS end_at, \
    min_used_quota::BIGINT AS min_used_quota, \
    min_account_age_days::BIGINT AS min_account_age_days, enabled, \
    COALESCE(created_at, 0)::BIGINT AS created_at";

const CLAIM_COLUMNS: &str = "id::BIGINT AS id, gift_id::BIGINT AS gift_id, \
    user_id::BIGINT AS user_id, COALESCE(username, '') AS username, \
    quota::BIGINT AS quota, COALESCE(created_at, 0)::BIGINT AS created_at";

#[derive(Clone, Debug)]
struct LockedGiftUser {
    username: String,
    created_at: i64,
    used_quota: i64,
}

impl From<&LockedGiftUser> for GiftUser {
    fn from(user: &LockedGiftUser) -> Self {
        Self {
            created_at: user.created_at,
            used_quota: user.used_quota,
        }
    }
}

async fn load_user(pg: &PgPool, user_id: i64) -> Result<GiftUser, GiftStoreError> {
    let row = sqlx::query(
        "SELECT id::BIGINT AS id, COALESCE(created_at, 0)::BIGINT AS created_at, \
         COALESCE(used_quota, 0)::BIGINT AS used_quota \
         FROM users WHERE id = $1 AND deleted_at IS NULL LIMIT 1",
    )
    .bind(user_id)
    .fetch_optional(pg)
    .await
    .map_err(database_error)?
    .ok_or_else(|| GiftStoreError::Database("record not found".to_owned()))?;
    Ok(GiftUser {
        created_at: row.try_get("created_at").map_err(database_error)?,
        used_quota: row.try_get("used_quota").map_err(database_error)?,
    })
}

async fn load_user_for_update(
    transaction: &mut Transaction<'_, Postgres>,
    user_id: i64,
) -> Result<LockedGiftUser, GiftStoreError> {
    let row = sqlx::query(
        "SELECT id::BIGINT AS id, COALESCE(username, '') AS username, \
         COALESCE(created_at, 0)::BIGINT AS created_at, \
         COALESCE(used_quota, 0)::BIGINT AS used_quota \
         FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(user_id)
    .fetch_optional(&mut **transaction)
    .await
    .map_err(database_error)?
    .ok_or_else(|| GiftStoreError::Database("record not found".to_owned()))?;
    Ok(LockedGiftUser {
        username: row.try_get("username").map_err(database_error)?,
        created_at: row.try_get("created_at").map_err(database_error)?,
        used_quota: row.try_get("used_quota").map_err(database_error)?,
    })
}

async fn load_gift_for_update(
    transaction: &mut Transaction<'_, Postgres>,
    gift_id: i64,
) -> Result<Option<Gift>, GiftStoreError> {
    sqlx::query(&format!(
        "SELECT {GIFT_COLUMNS} FROM gifts WHERE id = $1 FOR UPDATE"
    ))
    .bind(gift_id)
    .fetch_optional(&mut **transaction)
    .await
    .map_err(database_error)?
    .map(Gift::from_row)
    .transpose()
    .map_err(database_error)
}

async fn load_existing_claim(
    transaction: &mut Transaction<'_, Postgres>,
    user_id: i64,
    gift_id: i64,
) -> Result<Option<GiftClaim>, GiftStoreError> {
    sqlx::query(&format!(
        "SELECT {CLAIM_COLUMNS} FROM gift_claims WHERE gift_id = $1 AND user_id = $2 LIMIT 1"
    ))
    .bind(gift_id)
    .bind(user_id)
    .fetch_optional(&mut **transaction)
    .await
    .map_err(database_error)?
    .map(GiftClaim::from_row)
    .transpose()
    .map_err(database_error)
}

fn database_error(error: sqlx::Error) -> GiftStoreError {
    GiftStoreError::Database(error.to_string())
}

fn gift_eligibility<U>(gift: &Gift, user: U, now: i64) -> Result<(), GiftEligibilityError>
where
    U: Into<GiftUser>,
{
    let user = user.into();
    if !gift.enabled {
        return Err(GiftEligibilityError::NotFound);
    }
    if now < gift.start_at {
        return Err(GiftEligibilityError::NotStarted);
    }
    if now >= gift.end_at {
        return Err(GiftEligibilityError::Expired);
    }
    if gift.min_account_age_days > 0 {
        let minimum_created_at = now.wrapping_sub(gift.min_account_age_days.wrapping_mul(86_400));
        if user.created_at > minimum_created_at {
            return Err(GiftEligibilityError::NotEligible);
        }
    }
    if gift.min_used_quota > 0 && user.used_quota < gift.min_used_quota {
        return Err(GiftEligibilityError::NotEligible);
    }
    Ok(())
}

trait GiftClock: Send + Sync {
    fn now(&self) -> i64;
}

struct SystemGiftClock;

impl GiftClock for SystemGiftClock {
    fn now(&self) -> i64 {
        unix_now()
    }
}

#[derive(Clone, Debug)]
struct GiftClaimEffect {
    user_id: i64,
    username: String,
    quota: i64,
}

#[derive(Clone, Debug)]
struct GiftAuditEffect {
    actor_id: i64,
    actor_username: String,
    actor_role: i64,
    auth_method: &'static str,
    client_ip: String,
    method: &'static str,
    route: &'static str,
    path: String,
    route_id: Option<String>,
    status: StatusCode,
    success: bool,
}

#[async_trait]
trait GiftEffects: Send + Sync {
    async fn claim_committed(&self, effect: GiftClaimEffect);
    async fn admin_write_finished(&self, effect: GiftAuditEffect);
}

struct PgValkeyGiftEffects {
    pg: PgPool,
    valkey: redis::Client,
}

#[async_trait]
impl GiftEffects for PgValkeyGiftEffects {
    async fn claim_committed(&self, effect: GiftClaimEffect) {
        let cache_result = async {
            let mut connection = self.valkey.get_multiplexed_async_connection().await?;
            redis::cmd("HINCRBY")
                .arg(format!("user:{}", effect.user_id))
                .arg("Quota")
                .arg(effect.quota)
                .query_async::<i64>(&mut connection)
                .await
        }
        .await;
        if let Err(error) = cache_result {
            tracing::warn!(%error, user_id = effect.user_id, "gift quota cache update failed after commit");
        }
        let content = format!(
            "领取补偿礼包，获得额度 {}",
            format_log_quota(&self.pg, effect.quota).await
        );
        let username = current_username(&self.pg, effect.user_id)
            .await
            .unwrap_or(effect.username);
        let log = sqlx::query(
            "INSERT INTO logs (user_id, created_at, type, content, username, \
             token_name, model_name, quota, prompt_tokens, completion_tokens, \
             use_time, is_stream, channel_id, token_id, \"group\", ip, other) \
             VALUES ($1, $2, $3, $4, $5, '', '', 0, 0, 0, 0, FALSE, 0, 0, '', '', '')",
        )
        .bind(effect.user_id)
        .bind(unix_now())
        .bind(LOG_TYPE_TOPUP)
        .bind(content)
        .bind(username)
        .execute(&self.pg)
        .await;
        if let Err(error) = log {
            tracing::warn!(%error, user_id = effect.user_id, "gift claim log write failed after commit");
        }
    }

    async fn admin_write_finished(&self, effect: GiftAuditEffect) {
        let mut audit_info = json!({
            "method": effect.method,
            "route": effect.route,
            "path": effect.path,
            "status": effect.status.as_u16(),
            "success": effect.success,
        });
        if let Some(route_id) = &effect.route_id {
            audit_info["params"] = json!({"id": route_id});
        }
        let other = json!({
            "op": {
                "action": "generic",
                "params": {"method": effect.method, "route": effect.route},
            },
            "admin_info": {
                "admin_id": effect.actor_id,
                "admin_username": effect.actor_username,
                "admin_role": effect.actor_role,
                "auth_method": effect.auth_method,
            },
            "audit_info": audit_info,
        });
        let log_username = current_username(&self.pg, effect.actor_id)
            .await
            .unwrap_or_else(|| effect.actor_username.clone());
        let log = sqlx::query(
            "INSERT INTO logs (user_id, created_at, type, content, username, ip, other) \
             VALUES ($1, $2, $3, $4, $5, $6, $7)",
        )
        .bind(effect.actor_id)
        .bind(unix_now())
        .bind(LOG_TYPE_MANAGE)
        .bind(format!("{} {}", effect.method, effect.route))
        .bind(log_username)
        .bind(effect.client_ip)
        .bind(other.to_string())
        .execute(&self.pg)
        .await;
        if let Err(error) = log {
            tracing::warn!(%error, "gift administrator audit write failed");
        }
    }
}

async fn current_username(pg: &PgPool, user_id: i64) -> Option<String> {
    sqlx::query_scalar::<_, String>(
        "SELECT COALESCE(username, '') FROM users WHERE id = $1 AND deleted_at IS NULL",
    )
    .bind(user_id)
    .fetch_optional(pg)
    .await
    .ok()
    .flatten()
}

async fn available_gifts(State(state): State<GiftState>, headers: HeaderMap) -> Response {
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let response = match state
        .store
        .available(principal.user.id, state.clock.now())
        .await
    {
        Ok(gifts) => success_without_message(json!(gifts)),
        Err(error) => api_error(error.to_string()),
    };
    with_auth_version(response)
}

async fn claim_gift(
    State(state): State<GiftState>,
    Path(gift_id): Path<String>,
    request: Request,
) -> Response {
    let principal = match authenticated_user(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let client_ip = client_ip(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return with_auth_version(response);
    }
    let gift_id = match gift_id.parse::<i64>() {
        Ok(gift_id) if gift_id > 0 => gift_id,
        _ => return with_auth_version(invalid_parameters(request.headers())),
    };
    let response = match state
        .store
        .claim(principal.user.id, gift_id, state.clock.now())
        .await
    {
        Ok(ClaimOutcome::AlreadyClaimed(claim)) => Json(json!({
            "success": true,
            "message": "已领取过该礼包",
            "data": {"claim": claim, "already_claimed": true},
        }))
        .into_response(),
        Ok(ClaimOutcome::Granted(claim)) => {
            state
                .effects
                .claim_committed(GiftClaimEffect {
                    user_id: principal.user.id,
                    username: claim.username.clone(),
                    quota: claim.quota,
                })
                .await;
            Json(json!({
                "success": true,
                "message": "领取成功",
                "data": {"claim": claim, "already_claimed": false},
            }))
            .into_response()
        }
        Err(error) => api_error(error.to_string()),
    };
    with_auth_version(response)
}

async fn admin_list_gifts(State(state): State<GiftState>, headers: HeaderMap) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let response = match state.store.list().await {
        Ok(gifts) => success_without_message(json!(gifts)),
        Err(error) => api_error(error.to_string()),
    };
    with_auth_version(response)
}

async fn admin_create_gift(State(state): State<GiftState>, request: Request) -> Response {
    let principal = match authenticated_admin(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let audit = gift_audit_effect(&principal, &request, "POST", "/api/gift/", None);
    let client_ip = client_ip(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return finish_admin_write(&state, audit, with_auth_version(response), false).await;
    }
    let accept_language = request.headers().get(header::ACCEPT_LANGUAGE).cloned();
    let input = match parse_gift_input(request).await {
        Ok(input) => input,
        Err(()) => {
            let response = invalid_parameters_value(accept_language.as_ref());
            return finish_admin_write(&state, audit, with_auth_version(response), false).await;
        }
    };
    if let Some(message) = validate_gift_input(&input) {
        let response = api_error(message.to_owned());
        return finish_admin_write(&state, audit, with_auth_version(response), false).await;
    }
    let (response, success) = match state.store.create(&input, state.clock.now()).await {
        Ok(gift) => (
            Json(json!({"success": true, "message": "创建成功", "data": gift})).into_response(),
            true,
        ),
        Err(error) => (api_error(error.to_string()), false),
    };
    finish_admin_write(&state, audit, with_auth_version(response), success).await
}

async fn admin_update_gift(
    State(state): State<GiftState>,
    Path(gift_id_raw): Path<String>,
    request: Request,
) -> Response {
    let principal = match authenticated_admin(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let audit = gift_audit_effect(
        &principal,
        &request,
        "PUT",
        "/api/gift/:id",
        Some(gift_id_raw.clone()),
    );
    let client_ip = client_ip(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return finish_admin_write(&state, audit, with_auth_version(response), false).await;
    }
    let gift_id = match gift_id_raw.parse::<i64>() {
        Ok(gift_id) if gift_id > 0 => gift_id,
        _ => {
            let response = invalid_parameters(request.headers());
            return finish_admin_write(&state, audit, with_auth_version(response), false).await;
        }
    };
    let accept_language = request.headers().get(header::ACCEPT_LANGUAGE).cloned();
    let input = match parse_gift_input(request).await {
        Ok(input) => input,
        Err(()) => {
            let response = invalid_parameters_value(accept_language.as_ref());
            return finish_admin_write(&state, audit, with_auth_version(response), false).await;
        }
    };
    if let Some(message) = validate_gift_input(&input) {
        let response = api_error(message.to_owned());
        return finish_admin_write(&state, audit, with_auth_version(response), false).await;
    }
    let (response, success) = match state.store.update(gift_id, &input).await {
        Ok(gift) => (
            Json(json!({"success": true, "message": "更新成功", "data": gift})).into_response(),
            true,
        ),
        Err(GiftStoreError::NotFound) => (api_error("礼包不存在".to_owned()), false),
        Err(error) => (api_error(error.to_string()), false),
    };
    finish_admin_write(&state, audit, with_auth_version(response), success).await
}

async fn admin_list_claims(
    State(state): State<GiftState>,
    RawQuery(raw_query): RawQuery,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return with_auth_version(response);
    }
    let page = normalize_claims_page(raw_query.as_deref());
    let response = match state.store.claims(page).await {
        Ok(result) => success_without_message(json!(result)),
        Err(error) => api_error(error.to_string()),
    };
    with_auth_version(response)
}

async fn finish_admin_write(
    state: &GiftState,
    mut audit: GiftAuditEffect,
    response: Response,
    success: bool,
) -> Response {
    audit.status = response.status();
    audit.success = success;
    state.effects.admin_write_finished(audit).await;
    response
}

fn gift_audit_effect(
    principal: &Principal,
    request: &Request,
    method: &'static str,
    route: &'static str,
    route_id: Option<String>,
) -> GiftAuditEffect {
    GiftAuditEffect {
        actor_id: principal.user.id,
        actor_username: principal.user.username.clone(),
        actor_role: principal.user.role,
        auth_method: if dashboard_token_candidate(&principal.credential) {
            "session"
        } else {
            "access_token"
        },
        client_ip: client_ip(request),
        method,
        route,
        path: request.uri().path().to_owned(),
        route_id,
        status: StatusCode::OK,
        success: false,
    }
}

async fn authenticated_user(state: &GiftState, headers: &HeaderMap) -> Result<Principal, Response> {
    let credential = crate::migration_routes::legacy_http::dashboard_credential(headers)
        .ok_or_else(|| {
            crate::migration_routes::legacy_http::simple_dashboard_auth_error(headers, None)
        })?;
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(credential.clone()))
        .await
        .map_err(|error| {
            crate::migration_routes::legacy_http::simple_dashboard_auth_error(
                headers,
                Some(error.kind),
            )
        })?;
    if !user.developer_access_granted {
        return Err(console_not_found());
    }
    enforce_user_auth_view(&user).map_err(|error| {
        crate::migration_routes::legacy_http::simple_user_auth_error(headers, error)
    })?;
    Ok(Principal { user, credential })
}

async fn authenticated_admin(
    state: &GiftState,
    headers: &HeaderMap,
) -> Result<Principal, Response> {
    let principal = authenticated_user(state, headers).await?;
    if principal.user.role < ADMIN_ROLE {
        return Err(
            crate::migration_routes::legacy_http::simple_user_auth_error(
                headers,
                UserAuthPolicyError::InsufficientPrivilege,
            ),
        );
    }
    Ok(principal)
}

async fn critical_rate_limit(state: &GiftState, client_ip: &str) -> Option<Response> {
    match state.auth.check_critical_rate_limit(client_ip).await {
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

fn client_ip(request: &Request) -> String {
    request
        .extensions()
        .get::<ClientIpKey>()
        .map_or_else(|| "unknown".to_owned(), |key| key.0.clone())
}

async fn parse_gift_input(request: Request) -> Result<GiftInput, ()> {
    let bytes = request_bytes(request).await.map_err(|_| ())?;
    parse_nullable_json(&bytes).map_err(|_| ())
}

async fn request_bytes(request: Request) -> Result<Bytes, axum::Error> {
    to_bytes(request.into_body(), BODY_LIMIT_BYTES).await
}

fn parse_nullable_json<T>(bytes: &[u8]) -> Result<T, serde_json::Error>
where
    T: for<'de> Deserialize<'de> + Default,
{
    let mut deserializer = serde_json::Deserializer::from_slice(bytes);
    let value = Value::deserialize(&mut deserializer)?;
    if value.is_null() {
        return Ok(T::default());
    }
    serde_json::from_value(value)
}

fn deserialize_nullable_string<'de, D>(deserializer: D) -> Result<String, D::Error>
where
    D: serde::Deserializer<'de>,
{
    Option::<String>::deserialize(deserializer).map(Option::unwrap_or_default)
}

fn deserialize_nullable_i64<'de, D>(deserializer: D) -> Result<i64, D::Error>
where
    D: serde::Deserializer<'de>,
{
    Option::<i64>::deserialize(deserializer).map(Option::unwrap_or_default)
}

fn validate_gift_input(input: &GiftInput) -> Option<&'static str> {
    if input.title.is_empty() {
        return Some("礼包标题不能为空");
    }
    if input.quota <= 0 {
        return Some("礼包额度必须为正数");
    }
    if input.end_at <= input.start_at {
        return Some("结束时间必须晚于开始时间");
    }
    if input.min_used_quota < 0 || input.min_account_age_days < 0 {
        return Some("门槛参数不能为负数");
    }
    None
}

fn normalize_claims_page(raw_query: Option<&str>) -> ClaimsPage {
    let value = |key: &str| {
        form_urlencoded::parse(raw_query.unwrap_or_default().as_bytes())
            .find_map(|(candidate, value)| (candidate == key).then(|| value.into_owned()))
    };
    let gift_id = value("gift_id")
        .and_then(|value| value.parse::<i64>().ok())
        .unwrap_or_default();
    let page = value("p")
        .and_then(|value| value.parse::<i64>().ok())
        .filter(|page| *page >= 1)
        .unwrap_or(DEFAULT_PAGE);
    let page_size = value("page_size")
        .and_then(|value| value.parse::<i64>().ok())
        .filter(|page_size| (1..=MAX_PAGE_SIZE).contains(page_size))
        .unwrap_or(DEFAULT_PAGE_SIZE);
    ClaimsPage {
        gift_id,
        page,
        page_size,
    }
}

async fn format_log_quota(pg: &PgPool, quota: i64) -> String {
    let rows = sqlx::query("SELECT key, value FROM options WHERE key = ANY($1)")
        .bind([
            "QuotaPerUnit",
            "USDExchangeRate",
            "general_setting.quota_display_type",
            "general_setting.custom_currency_symbol",
            "general_setting.custom_currency_exchange_rate",
            "general_setting",
        ])
        .fetch_all(pg)
        .await;
    let options = rows.map_or_else(
        |_| HashMap::new(),
        |rows| {
            rows.into_iter()
                .filter_map(|row| {
                    Some((
                        row.try_get::<String, _>("key").ok()?,
                        row.try_get::<String, _>("value").ok()?,
                    ))
                })
                .collect::<HashMap<_, _>>()
        },
    );
    format_log_quota_from_options(&options, quota)
}

fn format_log_quota_from_options(options: &HashMap<String, String>, quota: i64) -> String {
    let quota_per_unit = options
        .get("QuotaPerUnit")
        .and_then(|value| value.parse::<f64>().ok())
        .filter(|value| value.is_finite() && *value > 0.0)
        .unwrap_or(DEFAULT_QUOTA_PER_UNIT);
    let aggregate = options
        .get("general_setting")
        .and_then(|value| serde_json::from_str::<Value>(value).ok())
        .unwrap_or_else(|| json!({}));
    let display_type = options
        .get("general_setting.quota_display_type")
        .filter(|value| !value.trim().is_empty())
        .map(String::as_str)
        .or_else(|| aggregate.get("quota_display_type").and_then(Value::as_str))
        .unwrap_or("USD");
    let usd = quota as f64 / quota_per_unit;
    match display_type {
        "CNY" => {
            let rate = options
                .get("USDExchangeRate")
                .and_then(|value| value.parse::<f64>().ok())
                .filter(|value| value.is_finite())
                .unwrap_or(7.3);
            format!("¥{:.6} 额度", usd * rate)
        }
        "CUSTOM" => {
            let symbol = options
                .get("general_setting.custom_currency_symbol")
                .filter(|value| !value.is_empty())
                .map(String::as_str)
                .or_else(|| {
                    aggregate
                        .get("custom_currency_symbol")
                        .and_then(Value::as_str)
                })
                .filter(|value| !value.is_empty())
                .unwrap_or("¤");
            let rate = options
                .get("general_setting.custom_currency_exchange_rate")
                .and_then(|value| value.parse::<f64>().ok())
                .filter(|value| value.is_finite() && *value > 0.0)
                .or_else(|| {
                    aggregate
                        .get("custom_currency_exchange_rate")
                        .and_then(Value::as_f64)
                        .filter(|value| value.is_finite() && *value > 0.0)
                })
                .unwrap_or(1.0);
            format!("{symbol}{:.6} 额度", usd * rate)
        }
        "TOKENS" => format!("{quota} 点额度"),
        _ => format!("＄{usd:.6} 额度"),
    }
}

fn invalid_parameters(headers: &HeaderMap) -> Response {
    invalid_parameters_value(headers.get(header::ACCEPT_LANGUAGE))
}

fn invalid_parameters_value(accept_language: Option<&HeaderValue>) -> Response {
    let language = accept_language
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default()
        .to_ascii_lowercase();
    let message = if language.starts_with("zh-tw") {
        "無效的參數"
    } else if language.starts_with("zh") {
        "无效的参数"
    } else {
        "Invalid parameters"
    };
    api_error(message.to_owned())
}

fn accepts_chinese(headers: &HeaderMap) -> bool {
    headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.to_ascii_lowercase().starts_with("zh"))
}

fn console_not_found() -> Response {
    (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response()
}

fn success_without_message(data: Value) -> Response {
    Json(json!({"success": true, "data": data})).into_response()
}

fn api_error(message: String) -> Response {
    Json(json!({"success": false, "message": message})).into_response()
}

fn coded_error(status: StatusCode, code: &'static str, message: &'static str) -> Response {
    (
        status,
        Json(json!({"success": false, "code": code, "message": message})),
    )
        .into_response()
}

fn with_auth_version(mut response: Response) -> Response {
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

fn is_zero(value: &i64) -> bool {
    *value == 0
}

fn unix_now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs() as i64)
}

#[cfg(test)]
mod tests {
    use axum::{body::Body, http::Request};
    use sqlx::postgres::PgPoolOptions;
    use tower::ServiceExt;

    use super::*;
    use crate::auth::{
        AuthBundle, AuthError, AuthErrorKind, DashboardUser, LoginOutcome, LoginRequest,
        LogoutRequest, LogoutResult, RequestMetadata, TwoFactorLoginRequest,
    };

    type TestResult<T = ()> = Result<T, Box<dyn std::error::Error + Send + Sync>>;

    fn test_error(
        context: &'static str,
        error: impl std::fmt::Display,
    ) -> Box<dyn std::error::Error + Send + Sync> {
        Box::new(std::io::Error::other(format!("{context}: {error}")))
    }

    fn required<T>(value: Option<T>, context: &'static str) -> TestResult<T> {
        value.ok_or_else(|| test_error(context, "missing value"))
    }

    fn required_header<'a>(
        headers: &'a HeaderMap,
        name: &'static str,
    ) -> TestResult<&'a HeaderValue> {
        headers
            .get(name)
            .ok_or_else(|| test_error("required response header is absent", name))
    }

    struct UnauthorizedAuth;

    #[async_trait]
    impl DashboardAuth for UnauthorizedAuth {
        async fn check_critical_rate_limit(
            &self,
            _: &str,
        ) -> Result<CriticalRateLimitOutcome, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn login(
            &self,
            _: LoginRequest,
            _: RequestMetadata,
        ) -> Result<LoginOutcome, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn login_2fa(
            &self,
            _: TwoFactorLoginRequest,
            _: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn refresh(
            &self,
            _: SecretString,
            _: Option<String>,
            _: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn self_user(&self, _: SecretString) -> Result<DashboardUser, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn generate_personal_access_token(
            &self,
            _: SecretString,
        ) -> Result<String, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }
    }

    fn fixture_gift() -> Gift {
        Gift {
            id: 8,
            title: "compensation".to_owned(),
            description: String::new(),
            quota: 1_000,
            start_at: 900_000,
            end_at: 2_000_000,
            min_used_quota: 100,
            min_account_age_days: 7,
            enabled: true,
            created_at: 800,
        }
    }

    fn fixture_user() -> GiftUser {
        GiftUser {
            created_at: 0,
            used_quota: 500,
        }
    }

    #[test]
    fn eligibility_should_accept_user_meeting_window_and_gates() {
        let result = gift_eligibility(&fixture_gift(), fixture_user(), 1_000_000);

        assert_eq!(result, Ok(()));
    }

    #[test]
    fn eligibility_should_reject_start_boundary_before_window() {
        let mut gift = fixture_gift();
        gift.start_at = 1_000_001;

        let result = gift_eligibility(&gift, fixture_user(), 1_000_000);

        assert_eq!(result, Err(GiftEligibilityError::NotStarted));
    }

    #[test]
    fn eligibility_should_reject_end_boundary_as_expired() {
        let gift = fixture_gift();

        let result = gift_eligibility(&gift, fixture_user(), gift.end_at);

        assert_eq!(result, Err(GiftEligibilityError::Expired));
    }

    #[test]
    fn eligibility_should_reject_new_account() {
        let gift = fixture_gift();
        let mut user = fixture_user();
        user.created_at = 999_999;

        let result = gift_eligibility(&gift, user, 1_000_000);

        assert_eq!(result, Err(GiftEligibilityError::NotEligible));
    }

    #[test]
    fn eligibility_should_reject_insufficient_historical_usage() {
        let gift = fixture_gift();
        let mut user = fixture_user();
        user.used_quota = 99;

        let result = gift_eligibility(&gift, user, 1_000_000);

        assert_eq!(result, Err(GiftEligibilityError::NotEligible));
    }

    #[test]
    fn validation_should_preserve_go_order_for_empty_title() {
        let input = GiftInput::default();

        assert_eq!(validate_gift_input(&input), Some("礼包标题不能为空"));
    }

    #[test]
    fn validation_should_accept_whitespace_title_like_go() {
        let input = GiftInput {
            title: " ".to_owned(),
            quota: 1,
            start_at: 1,
            end_at: 2,
            ..GiftInput::default()
        };

        assert_eq!(validate_gift_input(&input), None);
    }

    #[test]
    fn claims_page_should_default_invalid_values_like_strconv() {
        assert_eq!(
            normalize_claims_page(Some("gift_id=bad&p=0&page_size=101")),
            ClaimsPage {
                gift_id: 0,
                page: 1,
                page_size: 20,
            }
        );
    }

    #[test]
    fn gift_status_should_omit_zero_claim_time_and_empty_reason() -> TestResult {
        let value = serde_json::to_value(GiftWithClaimStatus {
            gift: fixture_gift(),
            claimed: false,
            claimed_at: 0,
            eligible: true,
            reason: String::new(),
        })
        .map_err(|error| test_error("serialize gift status JSON", error))?;

        assert_eq!(value.get("claimed_at"), None);
        assert_eq!(
            required(value.get("id"), "gift status JSON must contain an id")?,
            8
        );
        Ok(())
    }

    #[test]
    fn quota_log_should_follow_tokens_display() {
        let options = HashMap::from([(
            "general_setting.quota_display_type".to_owned(),
            "TOKENS".to_owned(),
        )]);

        assert_eq!(
            format_log_quota_from_options(&options, 1_234),
            "1234 点额度"
        );
    }

    #[test]
    fn quota_log_should_follow_custom_display() {
        let options = HashMap::from([
            ("QuotaPerUnit".to_owned(), "100".to_owned()),
            (
                "general_setting.quota_display_type".to_owned(),
                "CUSTOM".to_owned(),
            ),
            (
                "general_setting.custom_currency_symbol".to_owned(),
                "¤".to_owned(),
            ),
            (
                "general_setting.custom_currency_exchange_rate".to_owned(),
                "2".to_owned(),
            ),
        ]);

        assert_eq!(
            format_log_quota_from_options(&options, 50),
            "¤1.000000 额度"
        );
    }

    #[tokio::test]
    async fn admin_claims_should_authenticate_before_parsing_malformed_query() -> TestResult {
        let pg = PgPoolOptions::new()
            .connect_lazy("postgres://postgres:postgres@127.0.0.1/gifts")
            .map_err(|error| test_error("create lazy PostgreSQL gift fixture", error))?;
        let valkey = redis::Client::open("redis://127.0.0.1/")
            .map_err(|error| test_error("create Valkey gift fixture", error))?;
        let app = router(GiftState::new(pg, valkey, Arc::new(UnauthorizedAuth)));
        let uri = "/api/gift/claims?p=%ZZ&page_size=invalid"
            .parse::<axum::http::Uri>()
            .map_err(|error| test_error("parse malformed gift claims fixture URI", error))?;
        let accept_language = HeaderValue::from_str("zh-TW")
            .map_err(|error| test_error("parse gift claims locale header", error))?;
        let request = Request::builder()
            .uri(uri)
            .header(header::ACCEPT_LANGUAGE, accept_language)
            .body(Body::empty())
            .map_err(|error| test_error("build malformed gift claims request", error))?;

        let response = app
            .oneshot(request)
            .await
            .map_err(|error| test_error("serve malformed gift claims request", error))?;

        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(
            required_header(response.headers(), "auth-version")?
                .to_str()
                .map_err(|error| test_error("decode gift auth-version header", error))?,
            AUTH_VERSION
        );
        let body = to_bytes(response.into_body(), usize::MAX)
            .await
            .map_err(|error| test_error("read gift authentication error body", error))?;
        let body: Value = serde_json::from_slice(&body)
            .map_err(|error| test_error("decode gift authentication error JSON", error))?;
        assert_eq!(
            required(
                body.get("code").and_then(Value::as_str),
                "gift authentication error JSON must contain a string code",
            )?,
            "AUTH_UNAUTHORIZED"
        );
        assert_eq!(
            required(
                body.get("message").and_then(Value::as_str),
                "gift authentication error JSON must contain a string message",
            )?,
            "无权进行此操作，access token 无效"
        );
        Ok(())
    }
}
