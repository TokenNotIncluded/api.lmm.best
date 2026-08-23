//! Legacy-compatible discount-code administration and user validation routes.

use std::{
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::{Bytes, to_bytes},
    extract::{Path, Query, RawQuery, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{delete, get, post},
};
use rand::TryRngCore;
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{PgPool, Row, postgres::PgRow};

use crate::{
    ClientIpKey,
    auth::{
        AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth, DashboardUserView,
        UserAuthPolicyError, dashboard_token_candidate, enforce_user_auth_view, user_auth_message,
        user_auth_status,
    },
    legacy_empty_response,
};

const ADMIN_ROLE: i64 = 10;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const BODY_LIMIT_BYTES: usize = 64 * 1024 * 1024;
const DISCOUNT_CODE_BATCH_MAX_COUNT: i64 = 100;
const LOG_TYPE_MANAGE: i64 = 3;
const STATUS_ENABLED: i64 = 1;
const STATUS_DISABLED: i64 = 2;

/// PostgreSQL and dashboard-auth dependencies for discount codes.
#[derive(Clone)]
pub struct DiscountCodeState {
    store: Arc<dyn DiscountCodeStore>,
    auth: Arc<dyn DashboardAuth>,
    audit_pool: PgPool,
}

impl DiscountCodeState {
    /// Creates production state backed by the listener's shared dependencies.
    #[must_use]
    pub fn new(pg: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self {
            store: Arc::new(PgDiscountCodeStore { pg: pg.clone() }),
            auth,
            audit_pool: pg,
        }
    }
}

/// Builds the administrator and user discount-code routes.
pub fn router(state: DiscountCodeState) -> Router {
    Router::new()
        .route(
            "/api/discount-code/",
            get(admin_list)
                .post(admin_create)
                .put(admin_update),
        )
        .route("/api/discount-code/search", get(admin_search))
        .route("/api/discount-code/batch", post(admin_batch_create))
        .route("/api/discount-code/exhausted", delete(admin_delete_exhausted))
        .route(
            "/api/discount-code/{id}",
            get(admin_get).delete(admin_delete),
        )
        .route(
            "/api/user/discount-code/validate",
            post(validate_for_user),
        )
        .with_state(state)
}

#[derive(Clone, Debug)]
struct Principal {
    user: DashboardUserView,
    credential: String,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
struct DiscountCode {
    id: i64,
    code: String,
    name: String,
    discount_percent: i64,
    min_amount: i64,
    status: i64,
    used_count: i64,
    max_uses: i64,
    created_by: i64,
    created_time: i64,
    updated_time: i64,
    starts_time: i64,
    expired_time: i64,
}

impl DiscountCode {
    fn from_row(row: PgRow) -> Result<Self, sqlx::Error> {
        Ok(Self {
            id: row.try_get("id")?,
            code: row.try_get("code")?,
            name: row.try_get("name")?,
            discount_percent: row.try_get("discount_percent")?,
            min_amount: row.try_get("min_amount")?,
            status: row.try_get("status")?,
            used_count: row.try_get("used_count")?,
            max_uses: row.try_get("max_uses")?,
            created_by: row.try_get("created_by")?,
            created_time: row.try_get("created_time")?,
            updated_time: row.try_get("updated_time")?,
            starts_time: row.try_get("starts_time")?,
            expired_time: row.try_get("expired_time")?,
        })
    }
}

#[derive(Clone, Debug, Default, Deserialize)]
struct DiscountCodeInput {
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    id: i64,
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    code: String,
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    name: String,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    discount_percent: i64,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    min_amount: i64,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    status: i64,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    max_uses: i64,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    starts_time: i64,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    expired_time: i64,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct DiscountCodeBatchRequest {
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    name: String,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    count: i64,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    discount_percent: i64,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    min_amount: i64,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    max_uses: i64,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    starts_time: i64,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    expired_time: i64,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct DiscountCodeValidationRequest {
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    code: String,
    #[serde(default, deserialize_with = "deserialize_nullable_i64")]
    amount: i64,
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    payment_method: String,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
struct PageResult {
    page: i64,
    page_size: i64,
    total: i64,
    items: Vec<DiscountCode>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, thiserror::Error)]
enum DiscountCodeValidationError {
    #[error("discount code must be 3-64 characters using A-Z, 0-9, _ or -")]
    InvalidDefinition,
    #[error("discount percent must be between 1 and 99")]
    InvalidPercent,
    #[error("minimum amount cannot be negative")]
    NegativeMinimum,
    #[error("invalid discount code validity window")]
    InvalidWindow,
    #[error("maximum discount code uses cannot be negative")]
    NegativeMaxUses,
    #[error("优惠码名称不能为空且不能超过 120 个字符")]
    InvalidName,
    #[error("优惠码数量必须在 1 到 100 之间")]
    InvalidBatchCount,
    #[error("无效的优惠码状态")]
    InvalidStatus,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, thiserror::Error)]
enum DiscountCodeEligibilityError {
    #[error("优惠码不存在")]
    NotFound,
    #[error("优惠码未启用或尚未生效")]
    Inactive,
    #[error("优惠码已过期")]
    Expired,
    #[error("当前充值金额未达到优惠码最低金额")]
    Minimum,
    #[error("优惠码使用次数已达上限")]
    Exhausted,
}

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
enum DiscountCodeStoreError {
    #[error("record not found")]
    NotFound,
    #[error("invalid discount code id")]
    InvalidId,
    #[error("优惠码已存在")]
    Duplicate,
    #[error("{0}")]
    Validation(String),
    #[error("{0}")]
    Eligibility(String),
    #[error("{0}")]
    Database(String),
}

#[async_trait]
trait DiscountCodeStore: Send + Sync {
    async fn list(&self, page: i64, page_size: i64, offset: i64) -> Result<PageResult, DiscountCodeStoreError>;

    async fn search(
        &self,
        keyword: &str,
        status: &str,
        page: i64,
        page_size: i64,
        offset: i64,
    ) -> Result<PageResult, DiscountCodeStoreError>;

    async fn get(&self, id: i64) -> Result<DiscountCode, DiscountCodeStoreError>;

    async fn create(
        &self,
        input: &DiscountCodeInput,
        created_by: i64,
        now: i64,
    ) -> Result<DiscountCode, DiscountCodeStoreError>;

    async fn batch_create(
        &self,
        request: &DiscountCodeBatchRequest,
        created_by: i64,
        now: i64,
    ) -> Result<Vec<DiscountCode>, DiscountCodeStoreError>;

    async fn update(
        &self,
        input: &DiscountCodeInput,
        status_only: bool,
    ) -> Result<DiscountCode, DiscountCodeStoreError>;

    async fn delete(&self, id: i64) -> Result<(), DiscountCodeStoreError>;

    async fn delete_exhausted(&self) -> Result<i64, DiscountCodeStoreError>;

    async fn validate_for_user(
        &self,
        code: &str,
        amount: i64,
        user_id: i64,
        now: i64,
    ) -> Result<DiscountCode, DiscountCodeStoreError>;
}

#[derive(Clone)]
struct PgDiscountCodeStore {
    pg: PgPool,
}

const DISCOUNT_CODE_COLUMNS: &str = "id::BIGINT AS id, COALESCE(code, '') AS code, \
    COALESCE(name, '') AS name, discount_percent::BIGINT AS discount_percent, \
    min_amount::BIGINT AS min_amount, status::BIGINT AS status, \
    used_count::BIGINT AS used_count, max_uses::BIGINT AS max_uses, \
    created_by::BIGINT AS created_by, created_time::BIGINT AS created_time, \
    updated_time::BIGINT AS updated_time, starts_time::BIGINT AS starts_time, \
    expired_time::BIGINT AS expired_time";

#[async_trait]
impl DiscountCodeStore for PgDiscountCodeStore {
    async fn list(
        &self,
        page: i64,
        page_size: i64,
        offset: i64,
    ) -> Result<PageResult, DiscountCodeStoreError> {
        let total = sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*)::BIGINT FROM discount_codes WHERE deleted_at IS NULL",
        )
        .fetch_one(&self.pg)
        .await
        .map_err(database_error)?;
        let rows = sqlx::query(&format!(
            "SELECT {DISCOUNT_CODE_COLUMNS} FROM discount_codes WHERE deleted_at IS NULL \
             ORDER BY id DESC OFFSET $1 LIMIT $2"
        ))
        .bind(offset)
        .bind(page_size)
        .fetch_all(&self.pg)
        .await
        .map_err(database_error)?;
        let items = rows
            .into_iter()
            .map(DiscountCode::from_row)
            .collect::<Result<Vec<_>, _>>()
            .map_err(database_error)?;
        Ok(PageResult {
            page,
            page_size,
            total,
            items,
        })
    }

    async fn search(
        &self,
        keyword: &str,
        status: &str,
        page: i64,
        page_size: i64,
        offset: i64,
    ) -> Result<PageResult, DiscountCodeStoreError> {
        let normalized = normalize_discount_code(keyword);
        let name_pattern = if keyword.trim().is_empty() {
            String::new()
        } else {
            format!("%{}%", keyword.trim())
        };
        let total = sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*)::BIGINT FROM discount_codes WHERE deleted_at IS NULL \
             AND ($1 = '' OR code LIKE $1 || '%' OR name LIKE $2) \
             AND ($3 = '' OR status::TEXT = $3)",
        )
        .bind(&normalized)
        .bind(&name_pattern)
        .bind(status)
        .fetch_one(&self.pg)
        .await
        .map_err(database_error)?;
        let rows = sqlx::query(&format!(
            "SELECT {DISCOUNT_CODE_COLUMNS} FROM discount_codes WHERE deleted_at IS NULL \
             AND ($1 = '' OR code LIKE $1 || '%' OR name LIKE $2) \
             AND ($3 = '' OR status::TEXT = $3) \
             ORDER BY id DESC OFFSET $4 LIMIT $5"
        ))
        .bind(&normalized)
        .bind(&name_pattern)
        .bind(status)
        .bind(offset)
        .bind(page_size)
        .fetch_all(&self.pg)
        .await
        .map_err(database_error)?;
        let items = rows
            .into_iter()
            .map(DiscountCode::from_row)
            .collect::<Result<Vec<_>, _>>()
            .map_err(database_error)?;
        Ok(PageResult {
            page,
            page_size,
            total,
            items,
        })
    }

    async fn get(&self, id: i64) -> Result<DiscountCode, DiscountCodeStoreError> {
        if id <= 0 {
            return Err(DiscountCodeStoreError::InvalidId);
        }
        sqlx::query(&format!(
            "SELECT {DISCOUNT_CODE_COLUMNS} FROM discount_codes \
             WHERE id = $1 AND deleted_at IS NULL"
        ))
        .bind(id)
        .fetch_optional(&self.pg)
        .await
        .map_err(database_error)?
        .map(DiscountCode::from_row)
        .transpose()
        .map_err(database_error)?
        .ok_or(DiscountCodeStoreError::NotFound)
    }

    async fn create(
        &self,
        input: &DiscountCodeInput,
        created_by: i64,
        now: i64,
    ) -> Result<DiscountCode, DiscountCodeStoreError> {
        validate_discount_code_input(input)?;
        let code = normalize_discount_code(&input.code);
        let row = sqlx::query(&format!(
            "INSERT INTO discount_codes (code, name, discount_percent, min_amount, status, \
             used_count, max_uses, created_by, created_time, updated_time, starts_time, \
             expired_time) VALUES ($1, $2, $3, $4, $5, 0, $6, $7, $8, $8, $9, $10) \
             RETURNING {DISCOUNT_CODE_COLUMNS}"
        ))
        .bind(&code)
        .bind(input.name.trim())
        .bind(input.discount_percent)
        .bind(input.min_amount)
        .bind(STATUS_ENABLED)
        .bind(input.max_uses)
        .bind(created_by)
        .bind(now)
        .bind(input.starts_time)
        .bind(input.expired_time)
        .fetch_one(&self.pg)
        .await
        .map_err(|error| map_unique_violation(error, DiscountCodeStoreError::Duplicate))?;
        DiscountCode::from_row(row).map_err(database_error)
    }

    async fn batch_create(
        &self,
        request: &DiscountCodeBatchRequest,
        created_by: i64,
        now: i64,
    ) -> Result<Vec<DiscountCode>, DiscountCodeStoreError> {
        validate_discount_code_batch_request(request)?;
        let starts_time = if request.starts_time <= 0 {
            now
        } else {
            request.starts_time
        };
        let template = DiscountCodeInput {
            name: request.name.trim().to_owned(),
            discount_percent: request.discount_percent,
            min_amount: request.min_amount,
            max_uses: request.max_uses,
            starts_time,
            expired_time: request.expired_time,
            ..DiscountCodeInput::default()
        };
        validate_discount_code_batch_input(&template)?;

        let mut transaction = self.pg.begin().await.map_err(database_error)?;
        let mut created = Vec::with_capacity(request.count as usize);
        for _ in 0..request.count {
            let mut inserted = None;
            let mut last_error = None;
            for _ in 0..5 {
                let code = generate_discount_code().map_err(|error| {
                    DiscountCodeStoreError::Database(error.to_string())
                })?;
                match sqlx::query(&format!(
                    "INSERT INTO discount_codes (code, name, discount_percent, min_amount, status, \
                     used_count, max_uses, created_by, created_time, updated_time, starts_time, \
                     expired_time) VALUES ($1, $2, $3, $4, $5, 0, $6, $7, $8, $8, $9, $10) \
                     RETURNING {DISCOUNT_CODE_COLUMNS}"
                ))
                .bind(&code)
                .bind(&template.name)
                .bind(template.discount_percent)
                .bind(template.min_amount)
                .bind(STATUS_ENABLED)
                .bind(template.max_uses)
                .bind(created_by)
                .bind(now)
                .bind(template.starts_time)
                .bind(template.expired_time)
                .fetch_one(&mut *transaction)
                .await
                {
                    Ok(row) => {
                        inserted = Some(DiscountCode::from_row(row).map_err(database_error)?);
                        last_error = None;
                        break;
                    }
                    Err(error) if is_unique_violation(&error) => {
                        last_error = Some(error);
                    }
                    Err(error) => return Err(database_error(error)),
                }
            }
            let Some(item) = inserted else {
                return Err(match last_error {
                    Some(error) => database_error(error),
                    None => DiscountCodeStoreError::Database(
                        "failed to generate unique discount code".to_owned(),
                    ),
                });
            };
            created.push(item);
        }
        transaction.commit().await.map_err(database_error)?;
        Ok(created)
    }

    async fn update(
        &self,
        input: &DiscountCodeInput,
        status_only: bool,
    ) -> Result<DiscountCode, DiscountCodeStoreError> {
        let mut current = self.get(input.id).await?;
        if status_only {
            if input.status != STATUS_ENABLED && input.status != STATUS_DISABLED {
                return Err(DiscountCodeStoreError::Validation(
                    DiscountCodeValidationError::InvalidStatus.to_string(),
                ));
            }
            current.status = input.status;
        } else {
            current.code = normalize_discount_code(&input.code);
            current.name = input.name.trim().to_owned();
            current.discount_percent = input.discount_percent;
            current.min_amount = input.min_amount;
            current.max_uses = input.max_uses;
            current.starts_time = input.starts_time;
            current.expired_time = input.expired_time;
            validate_discount_code_input(&DiscountCodeInput {
                id: current.id,
                code: current.code.clone(),
                name: current.name.clone(),
                discount_percent: current.discount_percent,
                min_amount: current.min_amount,
                max_uses: current.max_uses,
                starts_time: current.starts_time,
                expired_time: current.expired_time,
                ..DiscountCodeInput::default()
            })?;
        }
        let now = unix_now();
        let row = sqlx::query(&format!(
            "UPDATE discount_codes SET code = $1, name = $2, discount_percent = $3, \
             min_amount = $4, status = $5, max_uses = $6, starts_time = $7, expired_time = $8, \
             updated_time = $9 WHERE id = $10 AND deleted_at IS NULL \
             RETURNING {DISCOUNT_CODE_COLUMNS}"
        ))
        .bind(&current.code)
        .bind(&current.name)
        .bind(current.discount_percent)
        .bind(current.min_amount)
        .bind(current.status)
        .bind(current.max_uses)
        .bind(current.starts_time)
        .bind(current.expired_time)
        .bind(now)
        .bind(current.id)
        .fetch_one(&self.pg)
        .await
        .map_err(|error| map_unique_violation(error, DiscountCodeStoreError::Duplicate))?;
        DiscountCode::from_row(row).map_err(database_error)
    }

    async fn delete(&self, id: i64) -> Result<(), DiscountCodeStoreError> {
        if id <= 0 {
            return Err(DiscountCodeStoreError::InvalidId);
        }
        let result = sqlx::query(
            "UPDATE discount_codes SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL",
        )
        .bind(id)
        .execute(&self.pg)
        .await
        .map_err(database_error)?;
        if result.rows_affected() == 0 {
            return Err(DiscountCodeStoreError::NotFound);
        }
        Ok(())
    }

    async fn delete_exhausted(&self) -> Result<i64, DiscountCodeStoreError> {
        let result = sqlx::query(
            "UPDATE discount_codes SET deleted_at = NOW() \
             WHERE deleted_at IS NULL AND max_uses > 0 AND used_count >= max_uses",
        )
        .execute(&self.pg)
        .await
        .map_err(database_error)?;
        Ok(result.rows_affected() as i64)
    }

    async fn validate_for_user(
        &self,
        code: &str,
        amount: i64,
        user_id: i64,
        now: i64,
    ) -> Result<DiscountCode, DiscountCodeStoreError> {
        let normalized = normalize_discount_code(code);
        if normalized.is_empty() {
            return Err(DiscountCodeStoreError::Eligibility(
                DiscountCodeEligibilityError::NotFound.to_string(),
            ));
        }
        let row = sqlx::query(
            "SELECT id::BIGINT AS id, COALESCE(code, '') AS code, COALESCE(name, '') AS name, \
             discount_percent::BIGINT AS discount_percent, min_amount::BIGINT AS min_amount, \
             status::BIGINT AS status, used_count::BIGINT AS used_count, \
             max_uses::BIGINT AS max_uses, created_by::BIGINT AS created_by, \
             created_time::BIGINT AS created_time, updated_time::BIGINT AS updated_time, \
             starts_time::BIGINT AS starts_time, expired_time::BIGINT AS expired_time, \
             COALESCE(owner_user_id, 0)::BIGINT AS owner_user_id \
             FROM discount_codes WHERE code = $1 AND deleted_at IS NULL LIMIT 1",
        )
        .bind(&normalized)
        .fetch_optional(&self.pg)
        .await
        .map_err(database_error)?
        .ok_or_else(|| {
            DiscountCodeStoreError::Eligibility(DiscountCodeEligibilityError::NotFound.to_string())
        })?;
        let owner_user_id: i64 = row.try_get("owner_user_id").map_err(database_error)?;
        let row = DiscountCode::from_row(row).map_err(database_error)?;
        validate_discount_code_eligibility(&row, owner_user_id, amount, user_id, now)
            .map_err(|error| DiscountCodeStoreError::Eligibility(error.to_string()))?;
        Ok(row)
    }
}

fn validate_discount_code_eligibility(
    row: &DiscountCode,
    owner_user_id: i64,
    amount: i64,
    user_id: i64,
    now: i64,
) -> Result<(), DiscountCodeEligibilityError> {
    if row.status != STATUS_ENABLED {
        return Err(DiscountCodeEligibilityError::Inactive);
    }
    if row.starts_time > 0 && row.starts_time > now {
        return Err(DiscountCodeEligibilityError::Inactive);
    }
    if row.expired_time > 0 && row.expired_time < now {
        return Err(DiscountCodeEligibilityError::Expired);
    }
    if amount < row.min_amount {
        return Err(DiscountCodeEligibilityError::Minimum);
    }
    if owner_user_id != 0 && owner_user_id != user_id {
        return Err(DiscountCodeEligibilityError::NotFound);
    }
    if row.max_uses > 0 && row.used_count >= row.max_uses {
        return Err(DiscountCodeEligibilityError::Exhausted);
    }
    Ok(())
}

fn validate_discount_code_input(input: &DiscountCodeInput) -> Result<(), DiscountCodeStoreError> {
    validate_discount_code_batch_input(input)?;
    validate_discount_code_definition(&input.code, input.discount_percent, input.min_amount, input.starts_time, input.expired_time)
}

fn validate_discount_code_batch_input(input: &DiscountCodeInput) -> Result<(), DiscountCodeStoreError> {
    let name = input.name.trim();
    if name.is_empty() || name.chars().count() > 120 {
        return Err(DiscountCodeStoreError::Validation(
            DiscountCodeValidationError::InvalidName.to_string(),
        ));
    }
    if input.max_uses < 0 {
        return Err(DiscountCodeStoreError::Validation(
            DiscountCodeValidationError::NegativeMaxUses.to_string(),
        ));
    }
    validate_discount_code_terms(input.discount_percent, input.min_amount, input.starts_time, input.expired_time)
}

fn validate_discount_code_batch_request(
    request: &DiscountCodeBatchRequest,
) -> Result<(), DiscountCodeStoreError> {
    if request.count < 1 || request.count > DISCOUNT_CODE_BATCH_MAX_COUNT {
        return Err(DiscountCodeStoreError::Validation(
            DiscountCodeValidationError::InvalidBatchCount.to_string(),
        ));
    }
    Ok(())
}

fn validate_discount_code_definition(
    code: &str,
    percent: i64,
    min_amount: i64,
    starts_time: i64,
    expired_time: i64,
) -> Result<(), DiscountCodeStoreError> {
    if !discount_code_pattern_matches(&normalize_discount_code(code)) {
        return Err(DiscountCodeStoreError::Validation(
            DiscountCodeValidationError::InvalidDefinition.to_string(),
        ));
    }
    validate_discount_code_terms(percent, min_amount, starts_time, expired_time)
}

fn validate_discount_code_terms(
    percent: i64,
    min_amount: i64,
    starts_time: i64,
    expired_time: i64,
) -> Result<(), DiscountCodeStoreError> {
    if percent <= 0 || percent >= 100 {
        return Err(DiscountCodeStoreError::Validation(
            DiscountCodeValidationError::InvalidPercent.to_string(),
        ));
    }
    if min_amount < 0 {
        return Err(DiscountCodeStoreError::Validation(
            DiscountCodeValidationError::NegativeMinimum.to_string(),
        ));
    }
    if starts_time < 0
        || expired_time < 0
        || (starts_time > 0 && expired_time > 0 && expired_time <= starts_time)
    {
        return Err(DiscountCodeStoreError::Validation(
            DiscountCodeValidationError::InvalidWindow.to_string(),
        ));
    }
    Ok(())
}

fn discount_code_pattern_matches(code: &str) -> bool {
    let chars: Vec<char> = code.chars().collect();
    if chars.len() < 3 || chars.len() > 64 {
        return false;
    }
    let valid = |ch: char| ch.is_ascii_uppercase() || ch.is_ascii_digit() || matches!(ch, '_' | '-');
    valid(chars[0]) && chars[1..].iter().copied().all(valid)
}

fn normalize_discount_code(value: &str) -> String {
    value.trim().to_ascii_uppercase()
}

fn generate_discount_code() -> Result<String, &'static str> {
    let mut bytes = [0_u8; 10];
    rand::rng().try_fill_bytes(&mut bytes).map_err(|_| "randomness unavailable")?;
    Ok(format!("LMM-{}", encode_base32_no_padding(&bytes)))
}

fn encode_base32_no_padding(data: &[u8]) -> String {
    const ALPHABET: &[u8; 32] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
    let mut output = String::new();
    let mut buffer = 0_u32;
    let mut bits_left = 0;
    for byte in data {
        buffer = (buffer << 8) | u32::from(*byte);
        bits_left += 8;
        while bits_left >= 5 {
            bits_left -= 5;
            let index = ((buffer >> bits_left) & 0x1F) as usize;
            output.push(char::from(ALPHABET[index]));
        }
    }
    if bits_left > 0 {
        let index = ((buffer << (5 - bits_left)) & 0x1F) as usize;
        output.push(char::from(ALPHABET[index]));
    }
    output
}

fn database_error(error: sqlx::Error) -> DiscountCodeStoreError {
    DiscountCodeStoreError::Database(error.to_string())
}

fn is_unique_violation(error: &sqlx::Error) -> bool {
    error.to_string().to_ascii_lowercase().contains("unique")
}

fn map_unique_violation(error: sqlx::Error, mapped: DiscountCodeStoreError) -> DiscountCodeStoreError {
    if is_unique_violation(&error) {
        mapped
    } else {
        database_error(error)
    }
}

fn normalize_page(raw_query: Option<&str>) -> (i64, i64, i64) {
    let value = |key: &str| {
        form_urlencoded::parse(raw_query.unwrap_or_default().as_bytes())
            .find_map(|(candidate, value)| (candidate == key).then(|| value.into_owned()))
    };
    let raw_page = value("p").and_then(|value| value.parse::<i64>().ok()).unwrap_or(0);
    let page = if raw_page < 1 {
        if raw_page == 0 { 1 } else { raw_page }
    } else {
        raw_page
    };
    let mut page_size = value("page_size").and_then(|value| value.parse::<i64>().ok()).unwrap_or(0);
    if page_size <= 0 {
        page_size = value("ps").and_then(|value| value.parse::<i64>().ok()).unwrap_or(0);
    }
    if page_size <= 0 {
        page_size = value("size").and_then(|value| value.parse::<i64>().ok()).unwrap_or(0);
    }
    if page_size <= 0 {
        page_size = 10;
    }
    if page_size > 100 {
        page_size = 100;
    }
    let offset = page.saturating_sub(1).saturating_mul(page_size);
    (page, page_size, offset)
}

#[derive(Clone, Debug, Default, Deserialize)]
struct SearchQuery {
    keyword: Option<String>,
    status: Option<String>,
}

async fn admin_list(
    State(state): State<DiscountCodeState>,
    RawQuery(raw_query): RawQuery,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let (page, page_size, offset) = normalize_page(raw_query.as_deref());
    let response = match state.store.list(page, page_size, offset).await {
        Ok(result) => api_success(json!(result)),
        Err(error) => api_error(error.to_string()),
    };
    with_auth_version(response)
}

async fn admin_search(
    State(state): State<DiscountCodeState>,
    Query(query): Query<SearchQuery>,
    RawQuery(raw_query): RawQuery,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let (page, page_size, offset) = normalize_page(raw_query.as_deref());
    let keyword = query.keyword.unwrap_or_default();
    let status = query.status.unwrap_or_default();
    let response = match state
        .store
        .search(&keyword, &status, page, page_size, offset)
        .await
    {
        Ok(result) => api_success(json!(result)),
        Err(error) => api_error(error.to_string()),
    };
    with_auth_version(response)
}

async fn admin_get(
    State(state): State<DiscountCodeState>,
    Path(id_raw): Path<String>,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let id = match id_raw.parse::<i64>() {
        Ok(id) if id > 0 => id,
        _ => return with_auth_version(api_error("invalid discount code id".to_owned())),
    };
    let response = match state.store.get(id).await {
        Ok(code) => api_success(json!(code)),
        Err(DiscountCodeStoreError::NotFound) => api_error("record not found".to_owned()),
        Err(DiscountCodeStoreError::InvalidId) => api_error("invalid discount code id".to_owned()),
        Err(error) => api_error(error.to_string()),
    };
    with_auth_version(response)
}

async fn admin_create(State(state): State<DiscountCodeState>, request: Request) -> Response {
    let principal = match authenticated_admin(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let audit = discount_code_audit_effect(&principal, &request, "POST", "/api/discount-code/", None);
    let client_ip = client_ip(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return finish_admin_write(&state, audit, with_auth_version(response), false).await;
    }
    let headers = request.headers().clone();
    let input = match parse_discount_code_input(request).await {
        Ok(input) => input,
        Err(()) => {
            let response = invalid_parameters(&headers);
            return finish_admin_write(&state, audit, with_auth_version(response), false).await;
        }
    };
    let (response, success) = match state
        .store
        .create(&input, principal.user.id, unix_now())
        .await
    {
        Ok(code) => (api_success(json!(code)), true),
        Err(DiscountCodeStoreError::Duplicate) => (api_error("优惠码已存在".to_owned()), false),
        Err(DiscountCodeStoreError::Validation(message)) => (api_error(message), false),
        Err(error) => (api_error(error.to_string()), false),
    };
    finish_admin_write(&state, audit, with_auth_version(response), success).await
}

async fn admin_batch_create(State(state): State<DiscountCodeState>, request: Request) -> Response {
    let principal = match authenticated_admin(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let audit = discount_code_audit_effect(
        &principal,
        &request,
        "POST",
        "/api/discount-code/batch",
        None,
    );
    let client_ip = client_ip(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return finish_admin_write(&state, audit, with_auth_version(response), false).await;
    }
    let headers = request.headers().clone();
    let request_body = match parse_batch_request(request).await {
        Ok(request_body) => request_body,
        Err(()) => {
            let response = invalid_parameters(&headers);
            return finish_admin_write(&state, audit, with_auth_version(response), false).await;
        }
    };
    let (response, success) = match state
        .store
        .batch_create(&request_body, principal.user.id, unix_now())
        .await
    {
        Ok(codes) => (api_success(json!(codes)), true),
        Err(DiscountCodeStoreError::Validation(message)) => (api_error(message), false),
        Err(error) => (api_error(error.to_string()), false),
    };
    finish_admin_write(&state, audit, with_auth_version(response), success).await
}

async fn admin_update(State(state): State<DiscountCodeState>, request: Request) -> Response {
    let principal = match authenticated_admin(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let audit = discount_code_audit_effect(&principal, &request, "PUT", "/api/discount-code/", None);
    let client_ip = client_ip(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return finish_admin_write(&state, audit, with_auth_version(response), false).await;
    }
    let status_only = request.uri().query().is_some_and(|query| {
        query
            .split('&')
            .any(|part| part == "status_only" || part.starts_with("status_only="))
    });
    let headers = request.headers().clone();
    let input = match parse_discount_code_input(request).await {
        Ok(input) => input,
        Err(()) => {
            let response = invalid_parameters(&headers);
            return finish_admin_write(&state, audit, with_auth_version(response), false).await;
        }
    };
    let (response, success) = match state.store.update(&input, status_only).await {
        Ok(code) => (api_success(json!(code)), true),
        Err(DiscountCodeStoreError::NotFound) => (api_error("record not found".to_owned()), false),
        Err(DiscountCodeStoreError::Duplicate) => (api_error("优惠码已存在".to_owned()), false),
        Err(DiscountCodeStoreError::Validation(message)) => (api_error(message), false),
        Err(error) => (api_error(error.to_string()), false),
    };
    finish_admin_write(&state, audit, with_auth_version(response), success).await
}

async fn admin_delete(
    State(state): State<DiscountCodeState>,
    Path(id_raw): Path<String>,
    request: Request,
) -> Response {
    let principal = match authenticated_admin(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let audit = discount_code_audit_effect(
        &principal,
        &request,
        "DELETE",
        "/api/discount-code/:id",
        Some(id_raw.clone()),
    );
    let client_ip = client_ip(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return finish_admin_write(&state, audit, with_auth_version(response), false).await;
    }
    let id = match id_raw.parse::<i64>() {
        Ok(id) if id > 0 => id,
        _ => {
            let response = invalid_parameters(request.headers());
            return finish_admin_write(&state, audit, with_auth_version(response), false).await;
        }
    };
    let (response, success) = match state.store.delete(id).await {
        Ok(()) => (api_success(Value::Null), true),
        Err(DiscountCodeStoreError::NotFound) => (api_error("record not found".to_owned()), false),
        Err(DiscountCodeStoreError::InvalidId) => (api_error("invalid discount code id".to_owned()), false),
        Err(error) => (api_error(error.to_string()), false),
    };
    finish_admin_write(&state, audit, with_auth_version(response), success).await
}

async fn admin_delete_exhausted(State(state): State<DiscountCodeState>, request: Request) -> Response {
    let principal = match authenticated_admin(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let audit = discount_code_audit_effect(
        &principal,
        &request,
        "DELETE",
        "/api/discount-code/exhausted",
        None,
    );
    let client_ip = client_ip(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return finish_admin_write(&state, audit, with_auth_version(response), false).await;
    }
    let (response, success) = match state.store.delete_exhausted().await {
        Ok(count) => (api_success(json!({"count": count})), true),
        Err(error) => (api_error(error.to_string()), false),
    };
    finish_admin_write(&state, audit, with_auth_version(response), success).await
}

async fn validate_for_user(State(state): State<DiscountCodeState>, request: Request) -> Response {
    let principal = match authenticated_user(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let client_ip = client_ip(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return with_auth_version(response);
    }
    let headers = request.headers().clone();
    let body = match parse_validation_request(request).await {
        Ok(body) => body,
        Err(()) => return with_auth_version(invalid_parameters(&headers)),
    };
    let response = match state
        .store
        .validate_for_user(&body.code, body.amount, principal.user.id, unix_now())
        .await
    {
        Ok(row) => api_success(json!({
            "code": row.code,
            "discount_percent": row.discount_percent,
            "min_amount": row.min_amount,
        })),
        Err(DiscountCodeStoreError::Eligibility(message)) => api_error(message),
        Err(error) => api_error(error.to_string()),
    };
    with_auth_version(response)
}

#[derive(Clone, Debug)]
struct DiscountCodeAuditEffect {
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

fn discount_code_audit_effect(
    principal: &Principal,
    request: &Request,
    method: &'static str,
    route: &'static str,
    route_id: Option<String>,
) -> DiscountCodeAuditEffect {
    DiscountCodeAuditEffect {
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

async fn finish_admin_write(
    state: &DiscountCodeState,
    mut audit: DiscountCodeAuditEffect,
    response: Response,
    success: bool,
) -> Response {
    audit.status = response.status();
    audit.success = success;
    record_discount_code_audit(state, audit).await;
    response
}

async fn record_discount_code_audit(state: &DiscountCodeState, effect: DiscountCodeAuditEffect) {
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
    let username = sqlx::query_scalar::<_, String>(
        "SELECT COALESCE(username, '') FROM users WHERE id = $1 AND deleted_at IS NULL",
    )
    .bind(effect.actor_id)
    .fetch_optional(&state.audit_pool)
    .await
    .ok()
    .flatten()
    .unwrap_or(effect.actor_username);
    let log = sqlx::query(
        "INSERT INTO logs (user_id, created_at, type, content, username, ip, other) \
         VALUES ($1, $2, $3, $4, $5, $6, $7)",
    )
    .bind(effect.actor_id)
    .bind(unix_now())
    .bind(LOG_TYPE_MANAGE)
    .bind(format!("{} {}", effect.method, effect.route))
    .bind(username)
    .bind(effect.client_ip)
    .bind(other.to_string())
    .execute(&state.audit_pool)
    .await;
    if let Err(error) = log {
        tracing::warn!(%error, "discount code administrator audit write failed");
    }
}

async fn authenticated_user(state: &DiscountCodeState, headers: &HeaderMap) -> Result<Principal, Response> {
    let credential =
        dashboard_credential(headers).ok_or_else(|| dashboard_auth_error(headers, None))?;
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(credential.clone()))
        .await
        .map_err(|error| dashboard_auth_error(headers, Some(error.kind)))?;
    if !user.developer_access_granted {
        return Err(console_not_found());
    }
    enforce_user_auth_view(&user).map_err(|error| user_auth_error(headers, error))?;
    Ok(Principal { user, credential })
}

async fn authenticated_admin(
    state: &DiscountCodeState,
    headers: &HeaderMap,
) -> Result<Principal, Response> {
    let principal = authenticated_user(state, headers).await?;
    if principal.user.role < ADMIN_ROLE {
        return Err(user_auth_error(
            headers,
            UserAuthPolicyError::InsufficientPrivilege,
        ));
    }
    Ok(principal)
}

async fn critical_rate_limit(state: &DiscountCodeState, client_ip: &str) -> Option<Response> {
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

async fn parse_discount_code_input(request: Request) -> Result<DiscountCodeInput, ()> {
    let bytes = request_bytes(request).await.map_err(|_| ())?;
    parse_nullable_json(&bytes).map_err(|_| ())
}

async fn parse_batch_request(request: Request) -> Result<DiscountCodeBatchRequest, ()> {
    let bytes = request_bytes(request).await.map_err(|_| ())?;
    parse_nullable_json(&bytes).map_err(|_| ())
}

async fn parse_validation_request(request: Request) -> Result<DiscountCodeValidationRequest, ()> {
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

fn dashboard_credential(headers: &HeaderMap) -> Option<String> {
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

fn dashboard_auth_error(headers: &HeaderMap, kind: Option<AuthErrorKind>) -> Response {
    let (status, code, english) = match kind {
        Some(AuthErrorKind::TokenExpired) => (
            StatusCode::UNAUTHORIZED,
            "AUTH_TOKEN_EXPIRED",
            "Unauthorized, not logged in and no access token provided",
        ),
        Some(AuthErrorKind::SessionRevoked) => (
            StatusCode::UNAUTHORIZED,
            "AUTH_SESSION_REVOKED",
            "Unauthorized, not logged in and no access token provided",
        ),
        Some(AuthErrorKind::Internal) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            "AUTH_INTERNAL_ERROR",
            "Database error, please contact the administrator",
        ),
        Some(AuthErrorKind::UserDisabled) => (
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_DISABLED",
            "User has been banned",
        ),
        _ => (
            StatusCode::UNAUTHORIZED,
            "AUTH_UNAUTHORIZED",
            "Unauthorized, invalid access token",
        ),
    };
    let message = if accepts_chinese(headers) {
        match code {
            "AUTH_INTERNAL_ERROR" => "数据库出错，请联系管理员",
            "AUTH_TOKEN_EXPIRED" | "AUTH_SESSION_REVOKED" => {
                "无权进行此操作，未登录且未提供 access token"
            }
            "AUTH_USER_DISABLED" => "用户已被封禁",
            _ => "无权进行此操作，access token 无效",
        }
    } else {
        english
    };
    coded_error(status, code, message)
}

fn user_auth_error(headers: &HeaderMap, error: UserAuthPolicyError) -> Response {
    let code = match error {
        UserAuthPolicyError::UserDisabled => "AUTH_USER_DISABLED",
        UserAuthPolicyError::InsufficientPrivilege => "AUTH_INSUFFICIENT_PRIVILEGE",
        UserAuthPolicyError::InvalidUserInfo => "AUTH_USER_INVALID",
    };
    let status = StatusCode::from_u16(user_auth_status(error)).unwrap_or(StatusCode::UNAUTHORIZED);
    coded_error(
        status,
        code,
        user_auth_message(
            error,
            headers
                .get(header::ACCEPT_LANGUAGE)
                .and_then(|value| value.to_str().ok()),
        ),
    )
}

fn invalid_parameters(headers: &HeaderMap) -> Response {
    let language = headers
        .get(header::ACCEPT_LANGUAGE)
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

fn api_success(data: Value) -> Response {
    Json(json!({"success": true, "message": "", "data": data})).into_response()
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

fn unix_now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs() as i64)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalize_discount_code_should_uppercase_and_trim() {
        assert_eq!(normalize_discount_code(" save-10 "), "SAVE-10");
    }

    #[test]
    fn pattern_should_accept_valid_codes() {
        assert!(discount_code_pattern_matches("ABC"));
        assert!(discount_code_pattern_matches("LMM-ABCDEFGHIJKLMNOP"));
    }

    #[test]
    fn pattern_should_reject_short_or_invalid_codes() {
        assert!(!discount_code_pattern_matches("AB"));
        assert!(!discount_code_pattern_matches("-SAVE10"));
    }

    #[test]
    fn batch_count_should_match_go_bounds() {
        let request = DiscountCodeBatchRequest {
            count: 101,
            ..DiscountCodeBatchRequest::default()
        };
        assert!(validate_discount_code_batch_request(&request).is_err());
    }
}
