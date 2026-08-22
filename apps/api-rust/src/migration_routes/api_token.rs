//! Frozen legacy `/api/token` routes.
//!
//! Authentication is deliberately supplied by the application boundary.  The
//! route module never accepts a token key as dashboard authentication, and only
//! exposes an unmasked key through the two explicit POST key-retrieval routes.

use std::{
    sync::{Arc, RwLock},
    time::Duration,
};

use axum::{
    Json, Router,
    body::Bytes,
    extract::{DefaultBodyLimit, Extension, Path, RawQuery, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use hmac::{Hmac, Mac};
use rand::{Rng, distr::Alphanumeric};
use redis::aio::MultiplexedConnection;
use serde::{
    Deserialize, Serialize,
    de::{DeserializeOwned, Error as DeError, MapAccess, Visitor},
};
use sha2::Sha256;
use sqlx::{PgPool, Row};

/// Legacy `SYNC_FREQUENCY` default used for token-cache writes.
pub const TOKEN_CACHE_TTL: Duration = Duration::from_secs(60);
const MAX_PAGE_SIZE: i64 = 100;
const SEARCH_HARD_LIMIT: i64 = 100;
const MAX_BATCH_KEYS: usize = 100;
const MAX_TOKEN_NAME_BYTES: usize = 50;
const DEFAULT_MAX_USER_TOKENS: i64 = 1_000;
const DEFAULT_QUOTA_PER_UNIT: f64 = 500_000.0;
const API_TOKEN_REQUEST_MAX_BYTES: usize = 512 * 1024;
type HmacSha256 = Hmac<Sha256>;

/// Set only by already-authenticated dashboard middleware.
///
/// The legacy route group uses `UserAuth`, not `AdminAuth`: every active
/// dashboard user may manage only their own API tokens. The shared dashboard
/// boundary validates status, username, and the complete user shape before it
/// attaches this value; this module repeats the role/id checks so an unmounted
/// route cannot accidentally treat a guest or malformed principal as trusted.
#[derive(Clone, Copy, Debug)]
pub struct ApiTokenPrincipal {
    pub user_id: i64,
    pub role: i64,
    /// Normalized language established by the dashboard UserAuth boundary.
    pub preferred_language: Option<&'static str>,
}

#[derive(Clone)]
pub struct ApiTokenHttpState {
    service: Arc<PgValkeyApiTokenService>,
    frozen_wire_errors: bool,
}

impl ApiTokenHttpState {
    #[must_use]
    pub fn new(service: Arc<PgValkeyApiTokenService>) -> Self {
        Self {
            service,
            // The isolated listener is the only runtime that must reproduce
            // the historical 5418ce6 `model.Token` binding errors.  The
            // normal listener follows the current Go `tokenRequest` wrapper.
            frozen_wire_errors: std::env::var_os("LMM_RS_TEST_INSTANCE").is_some(),
        }
    }

    /// Selects the historical Go top-level JSON binding error spelling for an
    /// in-process parity fixture. Production/current-Go callers keep the
    /// default `controller.tokenRequest` spelling.
    #[must_use]
    pub fn with_frozen_wire_errors(mut self, enabled: bool) -> Self {
        self.frozen_wire_errors = enabled;
        self
    }
}

pub fn api_token_router(state: ApiTokenHttpState) -> Router {
    let router = Router::new()
        .route("/api/token/", get(list).post(create).put(update))
        .route("/api/token/search", get(search))
        .route("/api/token/batch", post(batch_delete))
        .route("/api/token/batch/keys", post(batch_keys))
        .route("/api/token/{id}", get(detail).delete(remove))
        .route("/api/token/{id}/key", post(key))
        // Token mutation handlers intentionally extract raw `Bytes` to keep
        // their legacy JSON error envelope. Keep that compatibility shape,
        // but bound the allocation before extraction.
        .layer(DefaultBodyLimit::max(API_TOKEN_REQUEST_MAX_BYTES));
    let router = if state.frozen_wire_errors {
        // The frozen 5418 router has no static auto-groups route, so its
        // dynamic `/:id` handler receives this path and returns the legacy
        // strconv.Atoi error. Keep the optional extension on current mounts.
        router
    } else {
        router.route("/api/token/auto-groups", get(auto_groups))
    };
    router.with_state(state)
}

#[derive(Clone)]
pub struct PgValkeyApiTokenService {
    pg: PgPool,
    valkey: redis::Client,
    cache_ttl: Duration,
    dependency_timeout: Duration,
    crypto_secret: Arc<str>,
    max_user_tokens: i64,
    console_activation_on_create: bool,
    include_auto_groups_cache: bool,
    settings_snapshot: Arc<RwLock<TokenSettings>>,
}

impl PgValkeyApiTokenService {
    #[must_use]
    pub fn new(pg: PgPool, valkey: redis::Client) -> Self {
        Self {
            pg,
            valkey,
            cache_ttl: TOKEN_CACHE_TTL,
            dependency_timeout: Duration::from_secs(2),
            crypto_secret: Arc::from(""),
            max_user_tokens: DEFAULT_MAX_USER_TOKENS,
            console_activation_on_create: true,
            include_auto_groups_cache: false,
            settings_snapshot: Arc::new(RwLock::new(TokenSettings::defaults(
                DEFAULT_MAX_USER_TOKENS,
            ))),
        }
    }

    #[must_use]
    pub fn with_cache_ttl(mut self, cache_ttl: Duration) -> Self {
        self.cache_ttl = cache_ttl;
        self
    }

    /// Supplies the legacy `CRYPTO_SECRET` used to derive Valkey cache keys.
    #[must_use]
    pub fn with_crypto_secret(mut self, crypto_secret: impl Into<Arc<str>>) -> Self {
        self.crypto_secret = crypto_secret.into();
        self
    }

    /// Supplies the configured per-user token limit from `token_setting`.
    #[must_use]
    pub fn with_max_user_tokens(mut self, max_user_tokens: i64) -> Self {
        self.max_user_tokens = max_user_tokens.max(0);
        if let Ok(mut settings) = self.settings_snapshot.write() {
            settings.max_user_tokens = self.max_user_tokens;
        }
        self
    }

    /// Controls the optional dashboard activation side effect on token create.
    ///
    /// The normal listener keeps the current Rust policy enabled. The isolated
    /// frozen Go parity listener disables it because the 5418ce6 oracle's
    /// `AddToken` path only inserts the token and leaves the user row intact.
    #[must_use]
    pub fn with_console_activation_on_create(mut self, enabled: bool) -> Self {
        self.console_activation_on_create = enabled;
        self
    }

    /// Includes the current Go `AutoGroups` field in token cache hashes.
    ///
    /// The historical 5418ce6 listener predates this field, so its isolated
    /// parity mount must keep the old hash shape.  The normal listener opts in
    /// explicitly when it compares against the current Go model.
    #[must_use]
    pub fn with_auto_groups_cache(mut self, enabled: bool) -> Self {
        self.include_auto_groups_cache = enabled;
        self
    }

    async fn cache_connection(&self) -> Result<MultiplexedConnection, TokenError> {
        tokio::time::timeout(
            self.dependency_timeout,
            self.valkey.get_multiplexed_async_connection(),
        )
        .await
        .map_err(|_| TokenError::internal())?
        .map_err(|_| TokenError::internal())
    }

    async fn has_auto_groups_column(&self) -> Result<bool, TokenError> {
        sqlx::query_scalar(
            "SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'tokens' AND column_name = 'auto_groups')",
        )
        .fetch_one(&self.pg)
        .await
        .map_err(TokenError::db)
    }

    async fn invalidate(&self, keys: impl IntoIterator<Item = String>) -> Result<(), TokenError> {
        // Legacy deletion is asynchronous and best effort.  We still bound the
        // connection and command here, then deliberately discard the result at
        // the callsite so cache availability never changes a successful DB
        // mutation into a failed legacy response.
        let keys = keys
            .into_iter()
            .filter(|key| !key.is_empty())
            .collect::<Vec<_>>();
        if keys.is_empty() {
            return Ok(());
        }
        let mut connection = self.cache_connection().await?;
        let mut command = redis::cmd("DEL");
        for key in keys {
            command.arg(self.cache_key(&key)?);
        }
        command
            .query_async::<()>(&mut connection)
            .await
            .map_err(|_| TokenError::internal())
    }

    async fn store_cache(&self, token: &ApiToken) -> Result<(), TokenError> {
        let mut connection = self.cache_connection().await?;
        let ttl = self.cache_ttl.as_secs().max(1);
        // HSET and EXPIRE are one Lua operation, preserving the legacy hash
        // layout and ensuring a partial command cannot create an immortal key.
        const STORE_WITH_AUTO_GROUPS: &str = r#"
redis.call('HSET', KEYS[1],
  'Id', ARGV[1], 'UserId', ARGV[2], 'Key', '', 'Status', ARGV[3],
  'Name', ARGV[4], 'CreatedTime', ARGV[5], 'AccessedTime', ARGV[6],
  'ExpiredTime', ARGV[7], 'RemainQuota', ARGV[8], 'UnlimitedQuota', ARGV[9],
  'ModelLimitsEnabled', ARGV[10], 'ModelLimits', ARGV[11], 'AllowIps', ARGV[12],
  'UsedQuota', ARGV[13], 'Group', ARGV[14], 'CrossGroupRetry', ARGV[15],
  'AutoGroups', ARGV[16])
redis.call('EXPIRE', KEYS[1], ARGV[17])
return 1
"#;
        const STORE_WITHOUT_AUTO_GROUPS: &str = r#"
redis.call('HSET', KEYS[1],
  'Id', ARGV[1], 'UserId', ARGV[2], 'Key', '', 'Status', ARGV[3],
  'Name', ARGV[4], 'CreatedTime', ARGV[5], 'AccessedTime', ARGV[6],
  'ExpiredTime', ARGV[7], 'RemainQuota', ARGV[8], 'UnlimitedQuota', ARGV[9],
  'ModelLimitsEnabled', ARGV[10], 'ModelLimits', ARGV[11], 'AllowIps', ARGV[12],
  'UsedQuota', ARGV[13], 'Group', ARGV[14], 'CrossGroupRetry', ARGV[15])
redis.call('EXPIRE', KEYS[1], ARGV[16])
return 1
"#;
        let cache_key = self.cache_key(&token.key)?;
        let result = if self.include_auto_groups_cache {
            let script = redis::Script::new(STORE_WITH_AUTO_GROUPS);
            script
                .key(&cache_key)
                .arg(token.id)
                .arg(token.user_id)
                .arg(token.status)
                .arg(&token.name)
                .arg(token.created_time)
                .arg(token.accessed_time)
                .arg(token.expired_time)
                .arg(token.remain_quota)
                .arg(token.unlimited_quota)
                .arg(token.model_limits_enabled)
                .arg(&token.model_limits)
                .arg(token.allow_ips.as_deref().unwrap_or_default())
                .arg(token.used_quota)
                .arg(&token.group)
                .arg(token.cross_group_retry)
                .arg(&token.auto_groups_raw)
                .arg(ttl)
                .invoke_async::<i64>(&mut connection)
                .await
        } else {
            let script = redis::Script::new(STORE_WITHOUT_AUTO_GROUPS);
            script
                .key(&cache_key)
                .arg(token.id)
                .arg(token.user_id)
                .arg(token.status)
                .arg(&token.name)
                .arg(token.created_time)
                .arg(token.accessed_time)
                .arg(token.expired_time)
                .arg(token.remain_quota)
                .arg(token.unlimited_quota)
                .arg(token.model_limits_enabled)
                .arg(&token.model_limits)
                .arg(token.allow_ips.as_deref().unwrap_or_default())
                .arg(token.used_quota)
                .arg(&token.group)
                .arg(token.cross_group_retry)
                .arg(ttl)
                .invoke_async::<i64>(&mut connection)
                .await
        };
        result.map(|_| ()).map_err(|_| TokenError::internal())
    }

    fn cache_key(&self, key: &str) -> Result<String, TokenError> {
        let mut mac = HmacSha256::new_from_slice(self.crypto_secret.as_bytes())
            .map_err(|_| TokenError::internal())?;
        mac.update(key.as_bytes());
        Ok(format!(
            "token:{}",
            hex::encode(mac.finalize().into_bytes())
        ))
    }

    async fn token_settings(&self) -> TokenSettings {
        let fallback = self
            .settings_snapshot
            .read()
            .map(|settings| settings.clone())
            .unwrap_or_else(|_| TokenSettings::defaults(self.max_user_tokens));
        let refreshed = async {
            let rows = sqlx::query(
                "SELECT key, value FROM options WHERE key IN ('token_setting.max_user_tokens', 'QuotaPerUnit', 'AutoGroups', 'MaxTokenAutoGroups')",
            )
            .fetch_all(&self.pg)
            .await?;
            let mut settings = TokenSettings::defaults(self.max_user_tokens);
            let mut quota_per_unit_seen = false;
            for row in rows {
                let key: String = row.try_get("key")?;
                let value: String = row.try_get("value")?;
                if key == "token_setting.max_user_tokens" {
                    // The current option store flattens nested configuration
                    // keys. Go's integer option loader uses the parsed value's
                    // zero value when the stored value is not an integer.
                    settings.max_user_tokens =
                        parse_max_user_tokens_option(&value, settings.max_user_tokens);
                } else if key == "AutoGroups" {
                    if let Ok(groups) = serde_json::from_str::<Vec<String>>(&value) {
                        settings.auto_groups = groups;
                    }
                } else if key == "MaxTokenAutoGroups" {
                    if let Ok(max_count) = value.parse::<i64>() {
                        if max_count > 0 {
                            settings.max_token_auto_groups = max_count;
                        }
                    }
                } else {
                    // Go uses `strconv.ParseFloat(value, 64)` and deliberately
                    // assigns its zero value on failure.  Preserve that behavior
                    // when an option row exists, while a missing row retains the
                    // historical default.
                    quota_per_unit_seen = true;
                    settings.quota_per_unit = value.parse::<f64>().unwrap_or(0.0);
                }
            }
            if quota_per_unit_seen && !settings.quota_per_unit.is_finite() {
                settings.quota_per_unit = 0.0;
            }
            Ok::<_, sqlx::Error>(settings)
        }
        .await;
        if let Ok(settings) = refreshed {
            if let Ok(mut cached) = self.settings_snapshot.write() {
                *cached = settings.clone();
            }
            settings
        } else {
            // Options are startup/in-memory configuration in the frozen Go
            // service. A transient SELECT or row-decode fault must therefore
            // never become a new request failure boundary: keep using the
            // last successful snapshot (or the configured defaults).
            fallback
        }
    }

    async fn auto_groups(&self, user_id: i64) -> Result<serde_json::Value, TokenError> {
        let settings = self.token_settings().await;
        let user_group = sqlx::query_scalar::<_, String>(
            "SELECT COALESCE(\"group\", 'default') FROM users WHERE id = $1 AND deleted_at IS NULL",
        )
        .bind(user_id)
        .fetch_optional(&self.pg)
        .await
        .map_err(TokenError::db)?
        .unwrap_or_else(|| "default".to_owned());

        // `GetUserAutoGroup` preserves the configured order, removes
        // duplicates, and only exposes groups selectable by the authenticated
        // user's current group.  The option snapshot carries the same
        // global AutoGroups list; default/vip/svip are the legacy built-in
        // ratio groups when no custom snapshot has been persisted.
        let mut seen = std::collections::BTreeSet::new();
        let groups = settings
            .auto_groups
            .iter()
            .filter(|group| {
                let group = group.as_str();
                !group.is_empty()
                    && group != "auto"
                    && (matches!(group, "default" | "vip" | "svip") || group == user_group)
                    && seen.insert(group.to_owned())
            })
            .take(settings.max_token_auto_groups.max(0) as usize)
            .cloned()
            .collect::<Vec<_>>();
        Ok(serde_json::json!({
            "groups": groups,
            "max_count": settings.max_token_auto_groups,
        }))
    }

    async fn encode_auto_groups(
        &self,
        user_id: i64,
        groups: &[String],
    ) -> Result<String, TokenError> {
        if groups.is_empty() {
            return Ok(String::new());
        }
        let settings = self.token_settings().await;
        if groups.len() > settings.max_token_auto_groups.max(0) as usize {
            return Err(TokenError::invalid(format!(
                "令牌自动分组数量不能超过 {}",
                settings.max_token_auto_groups
            )));
        }
        let user_group = sqlx::query_scalar::<_, String>(
            "SELECT COALESCE(\"group\", 'default') FROM users WHERE id = $1 AND deleted_at IS NULL",
        )
        .bind(user_id)
        .fetch_optional(&self.pg)
        .await
        .map_err(TokenError::db)?
        .unwrap_or_else(|| "default".to_owned());
        let mut seen = std::collections::BTreeSet::new();
        for group in groups {
            if !seen.insert(group) {
                return Err(TokenError::invalid(format!("自动分组重复: {group}")));
            }
            if group.is_empty()
                || group == "auto"
                || !(matches!(group.as_str(), "default" | "vip" | "svip") || group == &user_group)
            {
                return Err(TokenError::invalid(format!("无效的自动分组: {group}")));
            }
        }
        serde_json::to_string(groups).map_err(|_| TokenError::internal())
    }

    async fn list(&self, user_id: i64, page: Page) -> Result<PageResult, TokenError> {
        let rows = if page.size < 0 {
            // GORM treats Limit(-1) as "cancel the LIMIT clause". PostgreSQL
            // rejects a literal negative LIMIT, so preserve the legacy result
            // by omitting the clause while retaining the raw response value.
            let sql = format!(
                "{TOKEN_SELECT} WHERE user_id = $1 AND deleted_at IS NULL ORDER BY id DESC OFFSET $2"
            );
            sqlx::query(&sql)
                .bind(user_id)
                .bind(page.offset().max(0))
                .fetch_all(&self.pg)
                .await
        } else {
            let sql = format!(
                "{TOKEN_SELECT} WHERE user_id = $1 AND deleted_at IS NULL ORDER BY id DESC LIMIT $2 OFFSET $3"
            );
            sqlx::query(&sql)
                .bind(user_id)
                .bind(page.size)
                .bind(page.offset().max(0))
                .fetch_all(&self.pg)
                .await
        }
        .map_err(TokenError::db)?;
        // The frozen controller fetches rows first, then calls CountUserTokens
        // and discards that error.  Keep the zero-value total when the second
        // query fails; changing this ordering changes both the response and
        // which database fault is observable.
        let total = sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*) FROM tokens WHERE user_id = $1 AND deleted_at IS NULL",
        )
        .bind(user_id)
        .fetch_one(&self.pg)
        .await
        .unwrap_or_default();
        Ok(PageResult {
            page: page.number,
            page_size: page.size,
            total,
            items: rows.iter().map(token_from_row).collect::<Result<_, _>>()?,
        })
    }

    async fn search(
        &self,
        user_id: i64,
        input: SearchQuery,
        page: Page,
    ) -> Result<PageResult, TokenError> {
        let settings = self.token_settings().await;
        let presented_key = input.token.strip_prefix("sk-").unwrap_or(&input.token);
        if input.keyword.contains('%') || presented_key.contains('%') {
            let count: i64 = sqlx::query_scalar(
                "SELECT COUNT(*) FROM tokens WHERE user_id = $1 AND deleted_at IS NULL",
            )
            .bind(user_id)
            .fetch_one(&self.pg)
            .await
            .map_err(TokenError::count_user_tokens_failed)?;
            if count > settings.max_user_tokens {
                return Err(TokenError::invalid(
                    "令牌数量超过上限，仅允许精确搜索，请勿使用 % 通配符",
                ));
            }
        }
        let keyword = like_pattern(&input.keyword)?;
        let token = like_pattern(presented_key)?;
        let total: i64 = sqlx::query_scalar(
            "SELECT COUNT(*) FROM tokens WHERE user_id = $1 AND deleted_at IS NULL AND ($2 = '' OR name LIKE $2 ESCAPE '!') AND ($3 = '' OR key LIKE $3 ESCAPE '!')",
        )
        .bind(user_id)
        .bind(&keyword)
        .bind(&token)
        .fetch_one(&self.pg)
        .await
        .map_err(TokenError::search_failed)?;
        let sql = format!(
            "{TOKEN_SELECT} WHERE user_id = $1 AND deleted_at IS NULL AND ($2 = '' OR name LIKE $2 ESCAPE '!') AND ($3 = '' OR key LIKE $3 ESCAPE '!') ORDER BY id DESC LIMIT $4 OFFSET $5"
        );
        let rows = sqlx::query(&sql)
            .bind(user_id)
            .bind(&keyword)
            .bind(&token)
            .bind(page.search_limit())
            .bind(page.search_offset())
            .fetch_all(&self.pg)
            .await
            .map_err(TokenError::search_failed)?;
        Ok(PageResult {
            page: page.number,
            page_size: page.size,
            total,
            items: rows
                .iter()
                .map(token_from_row)
                .collect::<Result<_, _>>()
                .map_err(|_| TokenError::search_failed_internal())?,
        })
    }

    async fn get(&self, user_id: i64, id: i64) -> Result<ApiToken, TokenError> {
        if id == 0 || user_id == 0 {
            return Err(TokenError::invalid("id 或 userId 为空！"));
        }
        let sql = format!("{TOKEN_SELECT} WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL");
        let row = sqlx::query(&sql)
            .bind(id)
            .bind(user_id)
            .fetch_optional(&self.pg)
            .await
            .map_err(TokenError::db)?
            .ok_or_else(TokenError::not_found)?;
        token_from_row(&row)
    }

    async fn keys(
        &self,
        user_id: i64,
        ids: &[i64],
    ) -> Result<serde_json::Map<String, serde_json::Value>, TokenError> {
        // `GetTokenKeysByIds` is one owner-scoped query. Missing or foreign
        // IDs are omitted, while a database failure fails the entire request.
        // Do not turn failures into an apparently successful partial map.
        let rows = sqlx::query(
            "SELECT id, key FROM tokens WHERE user_id = $1 AND id = ANY($2) AND deleted_at IS NULL",
        )
        .bind(user_id)
        .bind(ids)
        .fetch_all(&self.pg)
        .await
        .map_err(TokenError::db)?;
        rows.into_iter()
            .map(|row| {
                Ok((
                    row.try_get::<i64, _>("id").map_err(TokenError::db)?,
                    row.try_get::<String, _>("key").map_err(TokenError::db)?,
                ))
            })
            .collect::<Result<Vec<_>, _>>()
            .map(|rows| {
                rows.into_iter()
                    .fold(serde_json::Map::new(), |mut keys, (id, key)| {
                        keys.insert(id.to_string(), serde_json::Value::String(key));
                        keys
                    })
            })
    }

    async fn create(&self, user_id: i64, input: TokenInput) -> Result<(), TokenError> {
        let settings = self.token_settings().await;
        validate_input(&input, settings.max_quota())?;
        let auto_groups_raw = if input.group == "auto" {
            self.encode_auto_groups(user_id, &input.auto_groups).await?
        } else {
            String::new()
        };
        let has_auto_groups_column = self.has_auto_groups_column().await?;
        let key = generate_key();
        let now = unix_now();
        let mut tx = self.pg.begin().await.map_err(TokenError::db)?;
        let count: i64 = sqlx::query_scalar(
            "SELECT COUNT(*) FROM tokens WHERE user_id = $1 AND deleted_at IS NULL",
        )
        .bind(user_id)
        .fetch_one(&mut *tx)
        .await
        .map_err(TokenError::db)?;
        if count >= settings.max_user_tokens {
            return Err(TokenError::token_limit(settings.max_user_tokens));
        }
        // `AddToken` builds a fresh Go model without copying request status;
        // GORM applies its `status:1` and `expired_time:-1` defaults whenever
        // the incoming expiration is omitted or zero.
        let expired_time = legacy_create_expired_time(input.expired_time);
        if has_auto_groups_column {
            sqlx::query("INSERT INTO tokens (user_id, key, status, name, created_time, accessed_time, expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, allow_ips, used_quota, \"group\", cross_group_retry, auto_groups) VALUES ($1,$2,1,$3,$4,$4,$5,$6,$7,$8,$9,$10,0,$11,$12,$13)")
                .bind(user_id).bind(&key).bind(input.name).bind(now).bind(expired_time).bind(input.remain_quota).bind(input.unlimited_quota).bind(input.model_limits_enabled).bind(input.model_limits).bind(input.allow_ips.unwrap_or_default()).bind(input.group).bind(input.cross_group_retry).bind(auto_groups_raw)
                .execute(&mut *tx).await.map_err(TokenError::db)?;
        } else {
            sqlx::query("INSERT INTO tokens (user_id, key, status, name, created_time, accessed_time, expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, allow_ips, used_quota, \"group\", cross_group_retry) VALUES ($1,$2,1,$3,$4,$4,$5,$6,$7,$8,$9,$10,0,$11,$12)")
                .bind(user_id).bind(&key).bind(input.name).bind(now).bind(expired_time).bind(input.remain_quota).bind(input.unlimited_quota).bind(input.model_limits_enabled).bind(input.model_limits).bind(input.allow_ips.unwrap_or_default()).bind(input.group).bind(input.cross_group_retry)
                .execute(&mut *tx).await.map_err(TokenError::db)?;
        }
        if self.console_activation_on_create {
            sqlx::query(
                "UPDATE users SET console_activated_at = EXTRACT(EPOCH FROM NOW())::BIGINT WHERE id = $1 AND deleted_at IS NULL AND console_activated_at = 0",
            )
            .bind(user_id)
            .execute(&mut *tx)
            .await
            .map_err(TokenError::db)?;
        }
        tx.commit().await.map_err(TokenError::db)
    }

    async fn update(
        &self,
        user_id: i64,
        input: TokenUpdate,
        status_only: bool,
    ) -> Result<ApiToken, TokenError> {
        if input.id == 0 {
            return Err(TokenError::invalid("id 或 userId 为空！"));
        }
        let settings = self.token_settings().await;
        validate_input(&input.token, settings.max_quota())?;
        let current = self.get(user_id, input.id).await?;
        let has_auto_groups_column = self.has_auto_groups_column().await?;
        if input.status == 1
            && current.status == 3
            && current.expired_time != -1
            && current.expired_time <= unix_now()
        {
            return Err(TokenError::localized(TokenMessage::ExpiredCannotEnable));
        }
        if input.status == 1
            && current.status == 4
            && current.remain_quota <= 0
            && !current.unlimited_quota
        {
            return Err(TokenError::localized(TokenMessage::ExhaustedCannotEnable));
        }
        let mut updated = current;
        if status_only {
            updated.status = input.status;
            // Go's status-only controller branch changes only this in-memory
            // field, but Token.Update still selects and writes every mutable
            // column. Preserve that observable UPDATE OF column set while
            // retaining the loaded values (including NULL allow_ips).
            if has_auto_groups_column {
                sqlx::query("UPDATE tokens SET name=$1, status=$2, expired_time=$3, remain_quota=$4, unlimited_quota=$5, model_limits_enabled=$6, model_limits=$7, allow_ips=$8, \"group\"=$9, cross_group_retry=$10, auto_groups=$11 WHERE id=$12 AND user_id=$13 AND deleted_at IS NULL")
                    .bind(&updated.name).bind(updated.status).bind(updated.expired_time).bind(updated.remain_quota).bind(updated.unlimited_quota).bind(updated.model_limits_enabled).bind(&updated.model_limits).bind(&updated.allow_ips).bind(&updated.group).bind(updated.cross_group_retry).bind(&updated.auto_groups_raw).bind(input.id).bind(user_id).execute(&self.pg).await.map_err(TokenError::db)?;
            } else {
                sqlx::query("UPDATE tokens SET name=$1, status=$2, expired_time=$3, remain_quota=$4, unlimited_quota=$5, model_limits_enabled=$6, model_limits=$7, allow_ips=$8, \"group\"=$9, cross_group_retry=$10 WHERE id=$11 AND user_id=$12 AND deleted_at IS NULL")
                    .bind(&updated.name).bind(updated.status).bind(updated.expired_time).bind(updated.remain_quota).bind(updated.unlimited_quota).bind(updated.model_limits_enabled).bind(&updated.model_limits).bind(&updated.allow_ips).bind(&updated.group).bind(updated.cross_group_retry).bind(input.id).bind(user_id).execute(&self.pg).await.map_err(TokenError::db)?;
            }
        } else {
            let t = input.token;
            let auto_groups_set = t.auto_groups_set;
            let requested_auto_groups = t.auto_groups;
            updated.name = t.name;
            updated.expired_time = t.expired_time.unwrap_or_default();
            updated.remain_quota = t.remain_quota;
            updated.unlimited_quota = t.unlimited_quota;
            updated.model_limits_enabled = t.model_limits_enabled;
            updated.model_limits = t.model_limits;
            updated.allow_ips = t.allow_ips;
            updated.group = t.group;
            updated.cross_group_retry = t.cross_group_retry;
            if updated.group != "auto" {
                updated.cross_group_retry = false;
                updated.auto_groups_raw.clear();
                updated.auto_groups = None;
            } else if auto_groups_set {
                updated.auto_groups_raw = self
                    .encode_auto_groups(user_id, &requested_auto_groups)
                    .await?;
                updated.auto_groups = parse_auto_groups(&updated.auto_groups_raw);
            }
            if has_auto_groups_column {
                sqlx::query("UPDATE tokens SET name=$1, expired_time=$2, remain_quota=$3, unlimited_quota=$4, model_limits_enabled=$5, model_limits=$6, allow_ips=$7, \"group\"=$8, cross_group_retry=$9, auto_groups=$10 WHERE id=$11 AND user_id=$12 AND deleted_at IS NULL")
                    .bind(&updated.name).bind(updated.expired_time).bind(updated.remain_quota).bind(updated.unlimited_quota).bind(updated.model_limits_enabled).bind(&updated.model_limits).bind(&updated.allow_ips).bind(&updated.group).bind(updated.cross_group_retry).bind(&updated.auto_groups_raw).bind(input.id).bind(user_id).execute(&self.pg).await.map_err(TokenError::db)?;
            } else {
                sqlx::query("UPDATE tokens SET name=$1, expired_time=$2, remain_quota=$3, unlimited_quota=$4, model_limits_enabled=$5, model_limits=$6, allow_ips=$7, \"group\"=$8, cross_group_retry=$9 WHERE id=$10 AND user_id=$11 AND deleted_at IS NULL")
                    .bind(&updated.name).bind(updated.expired_time).bind(updated.remain_quota).bind(updated.unlimited_quota).bind(updated.model_limits_enabled).bind(&updated.model_limits).bind(&updated.allow_ips).bind(&updated.group).bind(updated.cross_group_retry).bind(input.id).bind(user_id).execute(&self.pg).await.map_err(TokenError::db)?;
            }
        }
        // This is intentionally best-effort, matching GORM's background
        // cache refresh. A database update is authoritative even if Valkey is
        // temporarily unavailable.
        let _ = self.store_cache(&updated).await;
        Ok(updated)
    }

    async fn delete(
        &self,
        user_id: i64,
        ids: &[i64],
        require_existing_token: bool,
    ) -> Result<usize, TokenError> {
        if ids.is_empty() {
            return Err(TokenError::invalid_params());
        }
        let mut tx = self.pg.begin().await.map_err(TokenError::db)?;
        let sql = format!(
            "{TOKEN_SELECT} WHERE user_id = $1 AND id = ANY($2) AND deleted_at IS NULL FOR UPDATE"
        );
        let rows = sqlx::query(&sql)
            .bind(user_id)
            .bind(ids)
            .fetch_all(&mut *tx)
            .await
            .map_err(TokenError::db)?;
        let keys = rows
            .iter()
            .map(token_from_row)
            .collect::<Result<Vec<_>, _>>()?
            .into_iter()
            .map(|token| token.key)
            .collect::<Vec<_>>();
        if rows.is_empty() && require_existing_token {
            return Err(TokenError::not_found());
        }
        sqlx::query("UPDATE tokens SET deleted_at = NOW() WHERE user_id = $1 AND id = ANY($2) AND deleted_at IS NULL").bind(user_id).bind(ids).execute(&mut *tx).await.map_err(TokenError::db)?;
        tx.commit().await.map_err(TokenError::db)?;
        let _ = self.invalidate(keys).await;
        Ok(rows.len())
    }
}

async fn list(
    State(state): State<ApiTokenHttpState>,
    principal: Option<Extension<ApiTokenPrincipal>>,
    query: RawQuery,
    headers: HeaderMap,
) -> Response {
    let principal = match require_principal(principal, &headers) {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    respond_token(
        state
            .service
            .list(
                principal.user_id,
                PageQuery::from_raw(query.0.as_deref()).page(),
            )
            .await,
        true,
        request_locale(&principal, &headers),
        state.frozen_wire_errors,
    )
}
async fn search(
    State(state): State<ApiTokenHttpState>,
    principal: Option<Extension<ApiTokenPrincipal>>,
    query: RawQuery,
    headers: HeaderMap,
) -> Response {
    let principal = match require_principal(principal, &headers) {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let query = SearchQuery::from_raw(query.0.as_deref());
    let page = query.page.page();
    respond_token(
        state.service.search(principal.user_id, query, page).await,
        true,
        request_locale(&principal, &headers),
        state.frozen_wire_errors,
    )
}

async fn auto_groups(
    State(state): State<ApiTokenHttpState>,
    principal: Option<Extension<ApiTokenPrincipal>>,
    headers: HeaderMap,
) -> Response {
    let principal = match require_principal(principal, &headers) {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    respond(
        state.service.auto_groups(principal.user_id).await,
        false,
        request_locale(&principal, &headers),
    )
}
async fn detail(
    State(state): State<ApiTokenHttpState>,
    principal: Option<Extension<ApiTokenPrincipal>>,
    Path(id): Path<String>,
    headers: HeaderMap,
) -> Response {
    let principal = match require_principal(principal, &headers) {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    match legacy_id(&id) {
        Ok(id) => respond_token(
            state.service.get(principal.user_id, id).await,
            true,
            request_locale(&principal, &headers),
            state.frozen_wire_errors,
        ),
        Err(error) => error.response_for(request_locale(&principal, &headers)),
    }
}
async fn key(
    State(state): State<ApiTokenHttpState>,
    principal: Option<Extension<ApiTokenPrincipal>>,
    Path(id): Path<String>,
    headers: HeaderMap,
) -> Response {
    let principal = match require_principal(principal, &headers) {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    no_store(match legacy_id(&id) {
        Ok(id) => match state.service.get(principal.user_id, id).await {
            Ok(token) => success(serde_json::json!({"key": token.key})),
            Err(error) => error.response_for(request_locale(&principal, &headers)),
        },
        Err(error) => error.response_for(request_locale(&principal, &headers)),
    })
}
async fn create(
    State(state): State<ApiTokenHttpState>,
    principal: Option<Extension<ApiTokenPrincipal>>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    let principal = match require_principal(principal, &headers) {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let input = match decode_legacy_json_for_route::<TokenInput>(&body, state.frozen_wire_errors) {
        Ok(input) => input,
        Err(error) => {
            return error.response_for(request_locale(&principal, &headers));
        }
    };
    match state.service.create(principal.user_id, input).await {
        Ok(()) => success_no_data(),
        Err(error) => error.response_for(request_locale(&principal, &headers)),
    }
}
async fn update(
    State(state): State<ApiTokenHttpState>,
    principal: Option<Extension<ApiTokenPrincipal>>,
    query: RawQuery,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    let principal = match require_principal(principal, &headers) {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let input = match decode_legacy_json_for_route::<TokenUpdate>(&body, state.frozen_wire_errors) {
        Ok(input) => input,
        Err(error) => return error.response_for(request_locale(&principal, &headers)),
    };
    let query = StatusOnlyQuery::from_raw(query.0.as_deref());
    respond_token(
        state
            .service
            .update(principal.user_id, input, query.enabled())
            .await,
        true,
        request_locale(&principal, &headers),
        state.frozen_wire_errors,
    )
}
async fn remove(
    State(state): State<ApiTokenHttpState>,
    principal: Option<Extension<ApiTokenPrincipal>>,
    Path(id): Path<String>,
    headers: HeaderMap,
) -> Response {
    let principal = match require_principal(principal, &headers) {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    match legacy_delete_id(&id) {
        Ok(id) => match state.service.delete(principal.user_id, &[id], true).await {
            Ok(_) => success_no_data(),
            Err(error) => error.response_for(request_locale(&principal, &headers)),
        },
        Err(error) => error.response_for(request_locale(&principal, &headers)),
    }
}
async fn batch_delete(
    State(state): State<ApiTokenHttpState>,
    principal: Option<Extension<ApiTokenPrincipal>>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    let principal = match require_principal(principal, &headers) {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let batch: TokenBatch = match decode_legacy_json(&body) {
        Ok(batch) => batch,
        Err(_) => {
            return TokenError::invalid_params().response_for(request_locale(&principal, &headers));
        }
    };
    respond(
        state
            .service
            .delete(principal.user_id, &batch.ids, false)
            .await,
        true,
        request_locale(&principal, &headers),
    )
}
async fn batch_keys(
    State(state): State<ApiTokenHttpState>,
    principal: Option<Extension<ApiTokenPrincipal>>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    let principal = match require_principal(principal, &headers) {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let batch: TokenBatch = match decode_legacy_json(&body) {
        Ok(batch) => batch,
        Err(_) => {
            return no_store(
                TokenError::invalid_params().response_for(request_locale(&principal, &headers)),
            );
        }
    };
    if batch.ids.is_empty() {
        return no_store(
            TokenError::invalid_params().response_for(request_locale(&principal, &headers)),
        );
    }
    if batch.ids.len() > MAX_BATCH_KEYS {
        return no_store(
            TokenError::batch_too_many().response_for(request_locale(&principal, &headers)),
        );
    }
    match state.service.keys(principal.user_id, &batch.ids).await {
        Ok(keys) => no_store(success(serde_json::json!({"keys": keys}))),
        Err(error) => no_store(error.response_for(request_locale(&principal, &headers))),
    }
}

fn respond<T: Serialize>(
    result: Result<T, TokenError>,
    masked: bool,
    locale: TokenLocale,
) -> Response {
    respond_with_options(result, masked, locale, false)
}

fn respond_token<T: Serialize>(
    result: Result<T, TokenError>,
    masked: bool,
    locale: TokenLocale,
    omit_auto_groups: bool,
) -> Response {
    respond_with_options(result, masked, locale, omit_auto_groups)
}

fn respond_with_options<T: Serialize>(
    result: Result<T, TokenError>,
    masked: bool,
    locale: TokenLocale,
    omit_auto_groups: bool,
) -> Response {
    match result {
        Ok(value) => {
            let mut value = if masked {
                mask_json(value)
            } else {
                serde_json::to_value(value).map_err(|_| TokenError::internal())
            };
            if omit_auto_groups {
                if let Ok(value) = &mut value {
                    omit_auto_groups_field(value);
                }
            }
            match value {
                Ok(value) => success(value),
                Err(error) => error.response_for(locale),
            }
        }
        Err(error) => error.response_for(locale),
    }
}

fn omit_auto_groups_field(value: &mut serde_json::Value) {
    match value {
        serde_json::Value::Object(map) => {
            map.remove("auto_groups");
            for child in map.values_mut() {
                omit_auto_groups_field(child);
            }
        }
        serde_json::Value::Array(values) => {
            for child in values {
                omit_auto_groups_field(child);
            }
        }
        _ => {}
    }
}
fn mask_json<T: Serialize>(value: T) -> Result<serde_json::Value, TokenError> {
    let mut value = serde_json::to_value(value).map_err(|_| TokenError::internal())?;
    mask_value(&mut value);
    Ok(value)
}
fn mask_value(value: &mut serde_json::Value) {
    match value {
        serde_json::Value::Object(map) => {
            if let Some(serde_json::Value::String(key)) = map.get_mut("key") {
                *key = mask_key(key);
            }
            for child in map.values_mut() {
                mask_value(child);
            }
        }
        serde_json::Value::Array(values) => {
            for child in values {
                mask_value(child);
            }
        }
        _ => {}
    }
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum PrincipalAuthError {
    InsufficientPrivilege,
    InvalidUserInfo,
}

fn principal_auth_error(principal: ApiTokenPrincipal) -> Option<PrincipalAuthError> {
    // A role of zero is the legacy guest role and is a valid user shape, but
    // UserAuth requires at least the common-user role for this route family.
    if principal.role == 0 {
        return Some(PrincipalAuthError::InsufficientPrivilege);
    }
    if principal.role < 0 || principal.user_id <= 0 || !matches!(principal.role, 1 | 10 | 100) {
        return Some(PrincipalAuthError::InvalidUserInfo);
    }
    None
}

// This HTTP boundary returns a fully formed Axum response so callers can
// preserve the legacy status, headers, and localized JSON envelope.
#[allow(clippy::result_large_err)]
fn require_principal(
    principal: Option<Extension<ApiTokenPrincipal>>,
    headers: &HeaderMap,
) -> Result<ApiTokenPrincipal, Response> {
    let Some(Extension(principal)) = principal else {
        return Err(unauthorized());
    };
    let Some(error) = principal_auth_error(principal) else {
        return Ok(principal);
    };
    Err(principal_auth_error_response(
        error,
        request_locale(&principal, headers),
    ))
}

fn principal_auth_error_response(error: PrincipalAuthError, locale: TokenLocale) -> Response {
    let (status, code, message) = match error {
        PrincipalAuthError::InsufficientPrivilege => (
            StatusCode::FORBIDDEN,
            "AUTH_INSUFFICIENT_PRIVILEGE",
            match locale {
                TokenLocale::En => "Unauthorized, insufficient privileges",
                TokenLocale::ZhCn => "无权进行此操作，权限不足",
                TokenLocale::ZhTw => "無權進行此操作，權限不足",
            },
        ),
        PrincipalAuthError::InvalidUserInfo => (
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_INVALID",
            match locale {
                TokenLocale::En => "Unauthorized, invalid user info",
                TokenLocale::ZhCn => "无权进行此操作，用户信息无效",
                TokenLocale::ZhTw => "無權進行此操作，使用者資訊無效",
            },
        ),
    };
    (
        status,
        Json(serde_json::json!({
            "success": false,
            "code": code,
            "message": message,
        })),
    )
        .into_response()
}

fn request_locale(principal: &ApiTokenPrincipal, headers: &HeaderMap) -> TokenLocale {
    TokenLocale::from_language(principal.preferred_language)
        .unwrap_or_else(|| TokenLocale::from_headers(headers))
}

#[derive(Clone, Copy)]
enum TokenLocale {
    En,
    ZhCn,
    ZhTw,
}

impl TokenLocale {
    fn from_language(language: Option<&str>) -> Option<Self> {
        let language = language?.trim().to_ascii_lowercase();
        Some(if language.starts_with("zh-tw") {
            Self::ZhTw
        } else if language.starts_with("zh") {
            Self::ZhCn
        } else if !language.is_empty() {
            Self::En
        } else {
            return None;
        })
    }

    fn from_headers(headers: &HeaderMap) -> Self {
        let language = headers
            .get(header::ACCEPT_LANGUAGE)
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.split(',').next())
            .and_then(|value| value.split(';').next())
            .unwrap_or_default()
            .trim()
            .to_ascii_lowercase();
        Self::from_language(Some(&language)).unwrap_or(Self::En)
    }
}

#[derive(Clone, Copy, Debug)]
enum TokenMessage {
    InvalidParams,
    BatchTooMany,
    NameTooLong,
    QuotaNegative,
    QuotaExceeds(i64),
    ExpiredCannotEnable,
    ExhaustedCannotEnable,
}

impl TokenMessage {
    fn render(self, locale: TokenLocale) -> String {
        match (self, locale) {
            (Self::InvalidParams, TokenLocale::En) => "Invalid parameters".to_owned(),
            (Self::InvalidParams, TokenLocale::ZhCn) => "无效的参数".to_owned(),
            (Self::InvalidParams, TokenLocale::ZhTw) => "無效的參數".to_owned(),
            (Self::BatchTooMany, TokenLocale::En) => {
                "Too many items in batch request, maximum is 100".to_owned()
            }
            (Self::BatchTooMany, TokenLocale::ZhCn) => "批量请求数量过多，最多 100 条".to_owned(),
            (Self::BatchTooMany, TokenLocale::ZhTw) => "批次請求數量過多，最多 100 條".to_owned(),
            (Self::NameTooLong, TokenLocale::En) => "Token name is too long".to_owned(),
            (Self::NameTooLong, TokenLocale::ZhCn) => "令牌名称过长".to_owned(),
            (Self::NameTooLong, TokenLocale::ZhTw) => "令牌名稱過長".to_owned(),
            (Self::QuotaNegative, TokenLocale::En) => "Quota value cannot be negative".to_owned(),
            (Self::QuotaNegative, TokenLocale::ZhCn) => "额度值不能为负数".to_owned(),
            (Self::QuotaNegative, TokenLocale::ZhTw) => "額度值不能為負數".to_owned(),
            (Self::QuotaExceeds(max), TokenLocale::En) => {
                format!("Quota value exceeds valid range, maximum is {max}")
            }
            (Self::QuotaExceeds(max), TokenLocale::ZhCn) => {
                format!("额度值超出有效范围，最大值为 {max}")
            }
            (Self::QuotaExceeds(max), TokenLocale::ZhTw) => {
                format!("額度值超出有效範圍，最大值為 {max}")
            }
            (Self::ExpiredCannotEnable, TokenLocale::En) => "Token has expired and cannot be enabled. Please modify the expiration time or set it to never expire".to_owned(),
            (Self::ExpiredCannotEnable, TokenLocale::ZhCn) => "令牌已过期，无法启用，请先修改令牌过期时间，或者设置为永不过期".to_owned(),
            (Self::ExpiredCannotEnable, TokenLocale::ZhTw) => "令牌已過期，無法啟用，請先修改令牌過期時間，或者設定為永不過期".to_owned(),
            (Self::ExhaustedCannotEnable, TokenLocale::En) => "Token quota is exhausted and cannot be enabled. Please modify the remaining quota or set it to unlimited".to_owned(),
            (Self::ExhaustedCannotEnable, TokenLocale::ZhCn) => "令牌可用额度已用尽，无法启用，请先修改令牌剩余额度，或者设置为无限额度".to_owned(),
            (Self::ExhaustedCannotEnable, TokenLocale::ZhTw) => "令牌可用額度已用盡，無法啟用，請先修改令牌剩餘額度，或者設定為無限額度".to_owned(),
        }
    }
}
fn legacy_id(id: &str) -> Result<i64, TokenError> {
    id.parse::<i64>().map_err(|error| {
        let reason = match error.kind() {
            std::num::IntErrorKind::PosOverflow | std::num::IntErrorKind::NegOverflow => {
                "value out of range"
            }
            _ => "invalid syntax",
        };
        TokenError::invalid(format!("strconv.Atoi: parsing {id:?}: {reason}"))
    })
}
fn legacy_delete_id(id: &str) -> Result<i64, TokenError> {
    // The frozen DeleteToken handler intentionally discards strconv.Atoi's
    // error before DeleteTokenById runs. strconv.ParseInt returns the nearest
    // bound on range errors, while syntax errors leave the zero value.
    let id = match id.parse::<i64>() {
        Ok(id) => id,
        Err(error) => match error.kind() {
            std::num::IntErrorKind::PosOverflow => i64::MAX,
            std::num::IntErrorKind::NegOverflow => i64::MIN,
            _ => 0,
        },
    };
    if id == 0 {
        return Err(TokenError::invalid("id 或 userId 为空！"));
    }
    Ok(id)
}
fn unauthorized() -> Response {
    (
        StatusCode::UNAUTHORIZED,
        Json(Envelope::<()> {
            success: false,
            message: "Unauthorized",
            data: None,
        }),
    )
        .into_response()
}
fn no_store(mut response: Response) -> Response {
    let headers = response.headers_mut();
    headers.insert(
        header::CACHE_CONTROL,
        HeaderValue::from_static("no-store, no-cache, must-revalidate, private, max-age=0"),
    );
    headers.insert(header::PRAGMA, HeaderValue::from_static("no-cache"));
    headers.insert(header::EXPIRES, HeaderValue::from_static("0"));
    response
}
fn decode_legacy_json<T: DeserializeOwned>(body: &[u8]) -> Result<T, TokenError> {
    // ShouldBindJSON calls Decoder.Decode exactly once.  In particular, it
    // consumes the first value and does not ask the decoder to prove that the
    // input has no second JSON value.
    let mut decoder = serde_json::Deserializer::from_slice(body);
    T::deserialize(&mut decoder).map_err(|error| {
        // `gin.Context.ShouldBindJSON` delegates to Go's encoding/json. Its
        // incomplete-document error is `unexpected EOF`, while empty input is
        // the distinct decoder error `EOF`.
        if error.is_eof() {
            if body.iter().any(|byte| !byte.is_ascii_whitespace()) {
                TokenError::invalid("unexpected EOF")
            } else {
                TokenError::invalid("EOF")
            }
        } else {
            let message = error.to_string();
            let message = message
                .strip_prefix("json: cannot unmarshal ")
                .and_then(|_| message.find(" at line ").map(|index| &message[..index]))
                .unwrap_or(&message);
            let message = if message.starts_with("parsing time ")
                || message.starts_with("Time.UnmarshalJSON: input is not a JSON string")
            {
                message
                    .find(" at line ")
                    .map_or(message, |index| &message[..index])
            } else {
                message
            };
            TokenError::invalid(message)
        }
    })
}

fn decode_legacy_json_for_route<T: DeserializeOwned>(
    body: &[u8],
    frozen_wire_errors: bool,
) -> Result<T, TokenError> {
    if frozen_wire_errors {
        // The frozen 5418 listener bound create/update bodies directly to
        // `model.Token`, while the current Go listener binds them through the
        // `controller.tokenRequest` wrapper. Probe only a complete top-level
        // JSON value here so malformed input keeps the normal decoder's exact
        // EOF and syntax errors.
        let mut decoder = serde_json::Deserializer::from_slice(body);
        if let Ok(value) = serde_json::Value::deserialize(&mut decoder) {
            let kind = match value {
                serde_json::Value::Array(_) => Some("array"),
                serde_json::Value::Bool(_) => Some("bool"),
                serde_json::Value::String(_) => Some("string"),
                serde_json::Value::Number(_) => Some("number"),
                serde_json::Value::Null | serde_json::Value::Object(_) => None,
            };
            if let Some(kind) = kind {
                return Err(TokenError::invalid(format!(
                    "json: cannot unmarshal {kind} into Go value of type model.Token"
                )));
            }
        }
    }
    decode_legacy_json(body)
}

#[derive(Default)]
struct TokenWire {
    id: i64,
    user_id: i64,
    key: String,
    status: i64,
    name: String,
    created_time: i64,
    accessed_time: i64,
    expired_time: Option<i64>,
    remain_quota: i64,
    unlimited_quota: bool,
    model_limits_enabled: bool,
    model_limits: String,
    allow_ips: Option<String>,
    used_quota: i64,
    group: String,
    cross_group_retry: bool,
    auto_groups_set: bool,
    auto_groups: Vec<String>,
}

impl<'de> Deserialize<'de> for TokenWire {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        deserializer.deserialize_any(TokenWireVisitor)
    }
}

struct TokenWireVisitor;

impl<'de> Visitor<'de> for TokenWireVisitor {
    type Value = TokenWire;

    fn expecting(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str("a JSON object or null")
    }

    fn visit_unit<E>(self) -> Result<Self::Value, E>
    where
        E: DeError,
    {
        Ok(TokenWire::default())
    }

    fn visit_bool<E>(self, _value: bool) -> Result<Self::Value, E>
    where
        E: DeError,
    {
        Err(E::custom(top_level_type_error("bool")))
    }

    fn visit_i64<E>(self, _value: i64) -> Result<Self::Value, E>
    where
        E: DeError,
    {
        Err(E::custom(top_level_type_error("number")))
    }

    fn visit_u64<E>(self, _value: u64) -> Result<Self::Value, E>
    where
        E: DeError,
    {
        Err(E::custom(top_level_type_error("number")))
    }

    fn visit_f64<E>(self, _value: f64) -> Result<Self::Value, E>
    where
        E: DeError,
    {
        Err(E::custom(top_level_type_error("number")))
    }

    fn visit_str<E>(self, _value: &str) -> Result<Self::Value, E>
    where
        E: DeError,
    {
        Err(E::custom(top_level_type_error("string")))
    }

    fn visit_string<E>(self, _value: String) -> Result<Self::Value, E>
    where
        E: DeError,
    {
        Err(E::custom(top_level_type_error("string")))
    }

    fn visit_bytes<E>(self, _value: &[u8]) -> Result<Self::Value, E>
    where
        E: DeError,
    {
        Err(E::custom(top_level_type_error("string")))
    }

    fn visit_seq<A>(self, _seq: A) -> Result<Self::Value, A::Error>
    where
        A: serde::de::SeqAccess<'de>,
    {
        Err(A::Error::custom(top_level_type_error("array")))
    }

    fn visit_map<A>(self, mut map: A) -> Result<Self::Value, A::Error>
    where
        A: MapAccess<'de>,
    {
        let mut wire = TokenWire::default();
        let mut first_error = None;
        while let Some(field) = map.next_key::<String>()? {
            let value: Box<serde_json::value::RawValue> = map.next_value()?;
            let result = if field.eq_ignore_ascii_case("id") {
                set_i64(&mut wire.id, &value, "id", "int")
            } else if field.eq_ignore_ascii_case("user_id") {
                set_i64(&mut wire.user_id, &value, "user_id", "int")
            } else if field.eq_ignore_ascii_case("key") {
                set_string(&mut wire.key, &value, "key")
            } else if field.eq_ignore_ascii_case("status") {
                set_i64(&mut wire.status, &value, "status", "int")
            } else if field.eq_ignore_ascii_case("name") {
                set_string(&mut wire.name, &value, "name")
            } else if field.eq_ignore_ascii_case("created_time") {
                set_i64(&mut wire.created_time, &value, "created_time", "int64")
            } else if field.eq_ignore_ascii_case("accessed_time") {
                set_i64(&mut wire.accessed_time, &value, "accessed_time", "int64")
            } else if field.eq_ignore_ascii_case("expired_time") {
                set_optional_i64(&mut wire.expired_time, &value, "expired_time", "int64")
            } else if field.eq_ignore_ascii_case("remain_quota") {
                set_i64(&mut wire.remain_quota, &value, "remain_quota", "int")
            } else if field.eq_ignore_ascii_case("unlimited_quota") {
                set_bool(&mut wire.unlimited_quota, &value, "unlimited_quota")
            } else if field.eq_ignore_ascii_case("model_limits_enabled") {
                set_bool(
                    &mut wire.model_limits_enabled,
                    &value,
                    "model_limits_enabled",
                )
            } else if field.eq_ignore_ascii_case("model_limits") {
                set_string(&mut wire.model_limits, &value, "model_limits")
            } else if field.eq_ignore_ascii_case("allow_ips") {
                set_optional_string(&mut wire.allow_ips, &value, "allow_ips")
            } else if field.eq_ignore_ascii_case("used_quota") {
                set_i64(&mut wire.used_quota, &value, "used_quota", "int")
            } else if field.eq_ignore_ascii_case("group") {
                set_string(&mut wire.group, &value, "group")
            } else if field.eq_ignore_ascii_case("cross_group_retry") {
                set_bool(&mut wire.cross_group_retry, &value, "cross_group_retry")
            } else if field.eq_ignore_ascii_case("auto_groups") {
                set_auto_groups(&mut wire.auto_groups_set, &mut wire.auto_groups, &value)
            } else if field.eq_ignore_ascii_case("DeletedAt") {
                set_deleted_at(&value)
            } else {
                Ok(())
            };
            if let Err(error) = result {
                if first_error.is_none() {
                    first_error = Some(error);
                }
            }
        }
        first_error.map_or(Ok(wire), |error| Err(A::Error::custom(error)))
    }
}

#[derive(Default)]
struct TokenBatchWire {
    ids: Vec<i64>,
}

impl<'de> Deserialize<'de> for TokenBatchWire {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        deserializer.deserialize_any(TokenBatchWireVisitor)
    }
}

struct TokenBatchWireVisitor;

impl<'de> Visitor<'de> for TokenBatchWireVisitor {
    type Value = TokenBatchWire;

    fn expecting(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str("a JSON object or null")
    }

    fn visit_unit<E>(self) -> Result<Self::Value, E>
    where
        E: DeError,
    {
        Ok(TokenBatchWire::default())
    }

    fn visit_map<A>(self, mut map: A) -> Result<Self::Value, A::Error>
    where
        A: MapAccess<'de>,
    {
        let mut wire = TokenBatchWire::default();
        let mut first_error = None;
        while let Some(field) = map.next_key::<String>()? {
            let value: Box<serde_json::value::RawValue> = map.next_value()?;
            if field.eq_ignore_ascii_case("ids") {
                if let Err(error) = set_batch_ids(&mut wire.ids, &value) {
                    if first_error.is_none() {
                        first_error = Some(error);
                    }
                }
            }
        }
        first_error.map_or(Ok(wire), |error| Err(A::Error::custom(error)))
    }
}

fn raw_json_kind(value: &serde_json::value::RawValue) -> Option<&'static str> {
    match value.get().as_bytes().first().copied() {
        Some(b'n') => None,
        Some(b't' | b'f') => Some("bool"),
        Some(b'"') => Some("string"),
        Some(b'[') => Some("array"),
        Some(b'{') => Some("object"),
        Some(_) => Some("number"),
        None => None,
    }
}

fn type_error(field: &str, actual: &str, expected: &str) -> String {
    format!(
        "json: cannot unmarshal {actual} into Go struct field {}.{field} of type {expected}",
        token_request_field_prefix()
    )
}

fn token_request_field_prefix() -> &'static str {
    // The frozen 5418ce6 model used a direct Token decoder. Current Go embeds
    // model.Token inside controller.tokenRequest, and encoding/json exposes
    // that containing type in its type errors. The isolated test listener
    // selects the historical spelling explicitly.
    if std::env::var_os("LMM_RS_TEST_INSTANCE").is_some() {
        "Token"
    } else {
        "tokenRequest.Token"
    }
}

fn top_level_type_error(actual: &str) -> String {
    let target = if std::env::var_os("LMM_RS_TEST_INSTANCE").is_some() {
        "model.Token"
    } else {
        "controller.tokenRequest"
    };
    format!("json: cannot unmarshal {actual} into Go value of type {target}")
}

fn batch_type_error(actual: &str) -> String {
    format!("json: cannot unmarshal {actual} into Go struct field TokenBatch.ids of type []int")
}

fn set_string(
    destination: &mut String,
    value: &serde_json::value::RawValue,
    field: &str,
) -> Result<(), String> {
    match raw_json_kind(value) {
        None => Ok(()),
        Some("string") => {
            *destination = serde_json::from_str(value.get())
                .map_err(|_| type_error(field, "string", "string"))?;
            Ok(())
        }
        Some(actual) => Err(type_error(field, actual, "string")),
    }
}

fn set_optional_string(
    destination: &mut Option<String>,
    value: &serde_json::value::RawValue,
    field: &str,
) -> Result<(), String> {
    match raw_json_kind(value) {
        None => {
            *destination = None;
            Ok(())
        }
        Some("string") => {
            *destination = Some(
                serde_json::from_str(value.get())
                    .map_err(|_| type_error(field, "string", "string"))?,
            );
            Ok(())
        }
        Some(actual) => Err(type_error(field, actual, "string")),
    }
}

fn set_bool(
    destination: &mut bool,
    value: &serde_json::value::RawValue,
    field: &str,
) -> Result<(), String> {
    match raw_json_kind(value) {
        None => Ok(()),
        Some("bool") => {
            *destination = value.get() == "true";
            Ok(())
        }
        Some(actual) => Err(type_error(field, actual, "bool")),
    }
}

fn set_i64(
    destination: &mut i64,
    value: &serde_json::value::RawValue,
    field: &str,
    expected: &str,
) -> Result<(), String> {
    match raw_json_kind(value) {
        None => Ok(()),
        Some("number") => {
            // encoding/json parses an integer field with strconv.ParseInt:
            // decimal fractions and exponent notation are not integers, even
            // where their mathematical value happens to be integral.
            *destination = value.get().parse::<i64>().map_err(|_| {
                format!(
                    "json: cannot unmarshal number {} into Go struct field {}.{} of type {}",
                    value.get(),
                    token_request_field_prefix(),
                    field,
                    expected
                )
            })?;
            Ok(())
        }
        Some(actual) => Err(type_error(field, actual, expected)),
    }
}

fn set_optional_i64(
    destination: &mut Option<i64>,
    value: &serde_json::value::RawValue,
    field: &str,
    expected: &str,
) -> Result<(), String> {
    match raw_json_kind(value) {
        None => Ok(()),
        Some("number") => {
            *destination = Some(value.get().parse::<i64>().map_err(|_| {
                format!(
                    "json: cannot unmarshal number {} into Go struct field {}.{} of type {}",
                    value.get(),
                    token_request_field_prefix(),
                    field,
                    expected
                )
            })?);
            Ok(())
        }
        Some(actual) => Err(type_error(field, actual, expected)),
    }
}

fn set_deleted_at(value: &serde_json::value::RawValue) -> Result<(), String> {
    match raw_json_kind(value) {
        None => Ok(()),
        Some("string") => {
            let timestamp: String = serde_json::from_str(value.get())
                .map_err(|_| "Time.UnmarshalJSON: input is not a JSON string".to_owned())?;
            chrono::DateTime::parse_from_rfc3339(&timestamp).map_err(|_| {
                format!(
                    "parsing time {timestamp:?} as \"2006-01-02T15:04:05Z07:00\": cannot parse {timestamp:?} as \"2006\""
                )
            })?;
            Ok(())
        }
        Some(_) => Err("Time.UnmarshalJSON: input is not a JSON string".to_owned()),
    }
}

fn set_auto_groups(
    set: &mut bool,
    destination: &mut Vec<String>,
    value: &serde_json::value::RawValue,
) -> Result<(), String> {
    *set = true;
    match raw_json_kind(value) {
        None => {
            destination.clear();
            Ok(())
        }
        Some("array") => {
            *destination = serde_json::from_str(value.get()).map_err(|_| {
                format!(
                    "json: cannot unmarshal array into Go struct field {}.auto_groups of type []string",
                    token_request_field_prefix()
                )
            })?;
            Ok(())
        }
        Some(actual) => Err(format!(
            "json: cannot unmarshal {actual} into Go struct field {}.auto_groups of type []string",
            token_request_field_prefix()
        )),
    }
}

fn set_batch_ids(
    destination: &mut Vec<i64>,
    value: &serde_json::value::RawValue,
) -> Result<(), String> {
    match raw_json_kind(value) {
        None => {
            destination.clear();
            Ok(())
        }
        Some("array") => {
            let values: Vec<Box<serde_json::value::RawValue>> =
                serde_json::from_str(value.get()).map_err(|_| batch_type_error("array"))?;
            let mut decoded = Vec::with_capacity(values.len());
            let mut first_error = None;
            for value in values {
                match raw_json_kind(&value) {
                    None => decoded.push(0),
                    Some("number") => match value.get().parse::<i64>() {
                        Ok(id) => decoded.push(id),
                        Err(_) if first_error.is_none() => {
                            first_error = Some(format!(
                                "json: cannot unmarshal number {} into Go struct field TokenBatch.ids of type []int",
                                value.get()
                            ));
                        }
                        Err(_) => {}
                    },
                    Some(actual) if first_error.is_none() => {
                        first_error = Some(batch_type_error(actual));
                    }
                    Some(_) => {}
                }
            }
            *destination = decoded;
            first_error.map_or(Ok(()), Err)
        }
        Some(actual) => Err(batch_type_error(actual)),
    }
}
fn success<T: Serialize>(data: T) -> Response {
    Json(Envelope {
        success: true,
        message: "",
        data: Some(data),
    })
    .into_response()
}
fn success_no_data() -> Response {
    Json(Envelope::<()> {
        success: true,
        message: "",
        data: None,
    })
    .into_response()
}

#[derive(Serialize)]
struct Envelope<T: Serialize> {
    success: bool,
    message: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<T>,
}
#[derive(Serialize)]
struct FailureEnvelope {
    success: bool,
    message: String,
}
#[derive(Debug, thiserror::Error)]
#[error("{message}")]
struct TokenError {
    message: String,
    localized: Option<TokenMessage>,
}
impl TokenError {
    fn invalid(message: impl Into<String>) -> Self {
        Self {
            message: message.into(),
            localized: None,
        }
    }
    fn localized(message: TokenMessage) -> Self {
        Self {
            // Preserve the frozen Chinese default for callers that have no
            // HTTP locale context (service and unit-test seams).
            message: message.render(TokenLocale::ZhCn),
            localized: Some(message),
        }
    }
    fn invalid_params() -> Self {
        Self::localized(TokenMessage::InvalidParams)
    }
    fn batch_too_many() -> Self {
        Self::localized(TokenMessage::BatchTooMany)
    }
    fn not_found() -> Self {
        Self::invalid("record not found")
    }
    fn token_limit(limit: i64) -> Self {
        // The response string is part of the dashboard contract.
        Self::invalid(format!("已达到最大令牌数量限制 ({limit})"))
    }
    fn db(error: sqlx::Error) -> Self {
        Self::invalid(database_error_message(&error))
    }
    fn count_user_tokens_failed(_: sqlx::Error) -> Self {
        Self::invalid("获取令牌数量失败")
    }
    fn search_failed(_: sqlx::Error) -> Self {
        Self::search_failed_internal()
    }
    fn search_failed_internal() -> Self {
        Self::invalid("搜索令牌失败")
    }
    fn internal() -> Self {
        Self::invalid("Internal server error")
    }
    fn response_for(self, locale: TokenLocale) -> Response {
        let message = self
            .localized
            .map_or(self.message, |message| message.render(locale));
        (
            StatusCode::OK,
            Json(FailureEnvelope {
                success: false,
                message,
            }),
        )
            .into_response()
    }
}

fn database_error_message(error: &sqlx::Error) -> String {
    match error {
        sqlx::Error::Database(database) => {
            let mut message = format!("ERROR: {}", database.message());
            if let Some(code) = database.code() {
                message.push_str(&format!(" (SQLSTATE {code})"));
            }
            message
        }
        _ => error.to_string(),
    }
}

#[derive(Clone, Debug, Serialize)]
struct ApiToken {
    id: i64,
    user_id: i64,
    key: String,
    status: i64,
    name: String,
    created_time: i64,
    accessed_time: i64,
    expired_time: i64,
    remain_quota: i64,
    unlimited_quota: bool,
    model_limits_enabled: bool,
    model_limits: String,
    allow_ips: Option<String>,
    used_quota: i64,
    group: String,
    cross_group_retry: bool,
    auto_groups: Option<Vec<String>>,
    #[serde(skip)]
    auto_groups_raw: String,
    #[serde(rename = "DeletedAt")]
    deleted_at: Option<()>,
}
const TOKEN_SELECT: &str = "SELECT id, user_id, COALESCE(key,''), COALESCE(status,0)::BIGINT, COALESCE(name,''), COALESCE(created_time,0), COALESCE(accessed_time,0), COALESCE(expired_time,0), COALESCE(remain_quota,0), COALESCE(unlimited_quota,FALSE), COALESCE(model_limits_enabled,FALSE), COALESCE(model_limits,''), allow_ips, COALESCE(used_quota,0), COALESCE(\"group\",''), COALESCE(cross_group_retry,FALSE), COALESCE(to_jsonb(tokens)->>'auto_groups','') AS auto_groups FROM tokens";
fn token_from_row(row: &sqlx::postgres::PgRow) -> Result<ApiToken, TokenError> {
    let auto_groups_raw: String = row.try_get(16).map_err(TokenError::db)?;
    Ok(ApiToken {
        id: row.try_get(0).map_err(TokenError::db)?,
        user_id: row.try_get(1).map_err(TokenError::db)?,
        key: row.try_get(2).map_err(TokenError::db)?,
        status: row.try_get(3).map_err(TokenError::db)?,
        name: row.try_get(4).map_err(TokenError::db)?,
        created_time: row.try_get(5).map_err(TokenError::db)?,
        accessed_time: row.try_get(6).map_err(TokenError::db)?,
        expired_time: row.try_get(7).map_err(TokenError::db)?,
        remain_quota: row.try_get(8).map_err(TokenError::db)?,
        unlimited_quota: row.try_get(9).map_err(TokenError::db)?,
        model_limits_enabled: row.try_get(10).map_err(TokenError::db)?,
        model_limits: row.try_get(11).map_err(TokenError::db)?,
        allow_ips: row.try_get(12).map_err(TokenError::db)?,
        used_quota: row.try_get(13).map_err(TokenError::db)?,
        group: row.try_get(14).map_err(TokenError::db)?,
        cross_group_retry: row.try_get(15).map_err(TokenError::db)?,
        auto_groups: parse_auto_groups(&auto_groups_raw),
        auto_groups_raw,
        deleted_at: None,
    })
}

fn parse_auto_groups(raw: &str) -> Option<Vec<String>> {
    if raw.trim().is_empty() {
        return None;
    }
    serde_json::from_str::<Vec<String>>(raw)
        .ok()
        .filter(|groups| !groups.is_empty())
}
#[derive(Debug)]
struct TokenInput {
    name: String,
    expired_time: Option<i64>,
    remain_quota: i64,
    unlimited_quota: bool,
    model_limits_enabled: bool,
    model_limits: String,
    allow_ips: Option<String>,
    group: String,
    cross_group_retry: bool,
    auto_groups_set: bool,
    auto_groups: Vec<String>,
}
impl From<TokenWire> for TokenInput {
    fn from(wire: TokenWire) -> Self {
        Self {
            name: wire.name,
            expired_time: wire.expired_time,
            remain_quota: wire.remain_quota,
            unlimited_quota: wire.unlimited_quota,
            model_limits_enabled: wire.model_limits_enabled,
            model_limits: wire.model_limits,
            allow_ips: wire.allow_ips,
            group: wire.group,
            cross_group_retry: wire.cross_group_retry,
            auto_groups_set: wire.auto_groups_set,
            auto_groups: wire.auto_groups,
        }
    }
}
impl<'de> Deserialize<'de> for TokenInput {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        TokenWire::deserialize(deserializer).map(Into::into)
    }
}

#[derive(Debug)]
struct TokenUpdate {
    id: i64,
    status: i64,
    token: TokenInput,
}
impl<'de> Deserialize<'de> for TokenUpdate {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        TokenWire::deserialize(deserializer).map(|wire| Self {
            id: wire.id,
            status: wire.status,
            token: wire.into(),
        })
    }
}

#[derive(Debug)]
struct TokenBatch {
    ids: Vec<i64>,
}
impl<'de> Deserialize<'de> for TokenBatch {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        TokenBatchWire::deserialize(deserializer).map(|wire| Self { ids: wire.ids })
    }
}
#[derive(Deserialize)]
struct PageQuery {
    p: Option<i64>,
    page_size: Option<i64>,
    ps: Option<i64>,
    size: Option<i64>,
}
#[derive(Deserialize)]
struct SearchQuery {
    #[serde(default)]
    keyword: String,
    #[serde(default)]
    token: String,
    #[serde(flatten)]
    page: PageQuery,
}
#[derive(Deserialize)]
struct StatusOnlyQuery {
    status_only: Option<String>,
}
#[derive(Clone, Copy)]
struct Page {
    number: i64,
    size: i64,
}
impl Page {
    fn from_query(p: Option<i64>, size: Option<i64>) -> Self {
        let parsed_p = p.unwrap_or(0);
        let number = if parsed_p < 1 {
            if parsed_p != 0 { parsed_p } else { 1 }
        } else {
            parsed_p
        };
        let mut size = size.unwrap_or(10);
        if size == 0 {
            size = 10;
        }
        if size > MAX_PAGE_SIZE {
            size = MAX_PAGE_SIZE;
        }
        Self { number, size }
    }
    fn offset(self) -> i64 {
        // Go's `int` arithmetic in `PageInfo.GetStartIdx` wraps at the
        // platform word size.  Keep that unusual boundary behavior instead
        // of saturating or rejecting an otherwise parseable legacy query.
        self.number.wrapping_sub(1).wrapping_mul(self.size)
    }

    // SearchUserTokens normalizes its model-layer query independently from
    // the PageInfo returned to the caller. List routes retain the raw legacy
    // page values, including negative compatibility inputs.
    fn search_limit(self) -> i64 {
        if self.size <= 0 || self.size > SEARCH_HARD_LIMIT {
            SEARCH_HARD_LIMIT
        } else {
            self.size
        }
    }

    fn search_offset(self) -> i64 {
        self.offset().max(0)
    }
}
impl PageQuery {
    fn page(&self) -> Page {
        let mut size = self.page_size.unwrap_or(0);
        if size == 0 {
            size = self.ps.unwrap_or(0);
        }
        if size == 0 {
            size = self.size.unwrap_or(0);
        }
        Page::from_query(self.p, Some(size))
    }

    fn from_raw(raw: Option<&str>) -> Self {
        Self {
            p: raw_query_i64(raw, "p"),
            // Go assigns page_size only when this first Atoi succeeds.  An
            // invalid value therefore stays zero and reaches the ps/size
            // fallbacks instead of preserving an overflow saturation.
            page_size: raw_query_i64_if_valid(raw, "page_size"),
            ps: raw_query_i64(raw, "ps"),
            size: raw_query_i64(raw, "size"),
        }
    }
}
impl SearchQuery {
    fn from_raw(raw: Option<&str>) -> Self {
        Self {
            keyword: raw_query_string(raw, "keyword").unwrap_or_default(),
            token: raw_query_string(raw, "token").unwrap_or_default(),
            page: PageQuery::from_raw(raw),
        }
    }
}
impl StatusOnlyQuery {
    fn from_raw(raw: Option<&str>) -> Self {
        Self {
            status_only: raw_query_string(raw, "status_only"),
        }
    }

    fn enabled(&self) -> bool {
        self.status_only
            .as_deref()
            .is_some_and(|value| !value.is_empty())
    }
}

// Gin's `Query` ignores failed numeric conversions.  Raw parsing avoids an
// Axum extractor rejection before the legacy handler can apply its fallbacks.
fn raw_query_string(raw: Option<&str>, wanted: &str) -> Option<String> {
    raw?.split('&').find_map(|part| {
        let (key, value) = part.split_once('=').unwrap_or((part, ""));
        (percent_decode_query(key) == wanted).then(|| percent_decode_query(value))
    })
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
fn raw_query_i64(raw: Option<&str>, wanted: &str) -> Option<i64> {
    // `strconv.Atoi` returns a clamped integer alongside ErrRange. The Go
    // compatibility branches for p, ps and size discard that error but keep
    // the returned value; malformed values still retain their zero value.
    raw_query_string(raw, wanted).and_then(|value| match value.parse::<i64>() {
        Ok(value) => Some(value),
        Err(error) => match error.kind() {
            std::num::IntErrorKind::PosOverflow => Some(i64::MAX),
            std::num::IntErrorKind::NegOverflow => Some(i64::MIN),
            _ => None,
        },
    })
}

fn raw_query_i64_if_valid(raw: Option<&str>, wanted: &str) -> Option<i64> {
    raw_query_string(raw, wanted).and_then(|value| value.parse::<i64>().ok())
}
#[derive(Serialize)]
struct PageResult {
    page: i64,
    page_size: i64,
    total: i64,
    items: Vec<ApiToken>,
}
#[derive(Clone)]
struct TokenSettings {
    max_user_tokens: i64,
    quota_per_unit: f64,
    auto_groups: Vec<String>,
    max_token_auto_groups: i64,
}
impl TokenSettings {
    fn defaults(max_user_tokens: i64) -> Self {
        Self {
            max_user_tokens,
            quota_per_unit: DEFAULT_QUOTA_PER_UNIT,
            auto_groups: vec!["default".to_owned()],
            max_token_auto_groups: 5,
        }
    }
}
fn parse_max_user_tokens_option(value: &str, fallback: i64) -> i64 {
    value.parse::<i64>().unwrap_or_else(|_| {
        value
            .parse::<f64>()
            .ok()
            .filter(|number| number.is_finite())
            .map_or(fallback, |number| number as i64)
    })
}

impl TokenSettings {
    fn max_quota(&self) -> i64 {
        (1_000_000_000_f64 * self.quota_per_unit)
            .trunc()
            .min(i64::MAX as f64) as i64
    }
}
fn legacy_create_expired_time(expired_time: Option<i64>) -> i64 {
    expired_time.filter(|value| *value != 0).unwrap_or(-1)
}
fn validate_input(input: &TokenInput, max_quota: i64) -> Result<(), TokenError> {
    if input.name.len() > MAX_TOKEN_NAME_BYTES {
        return Err(TokenError::localized(TokenMessage::NameTooLong));
    }
    if !input.unlimited_quota && input.remain_quota < 0 {
        return Err(TokenError::localized(TokenMessage::QuotaNegative));
    }
    if !input.unlimited_quota && input.remain_quota > max_quota {
        return Err(TokenError::localized(TokenMessage::QuotaExceeds(max_quota)));
    }
    Ok(())
}
fn like_pattern(input: &str) -> Result<String, TokenError> {
    let escaped = input.replace('!', "!!").replace('_', "!_");
    if escaped.contains("%%") {
        return Err(TokenError::invalid("搜索模式中不允许包含连续的 % 通配符"));
    }
    let wildcard_count = escaped.matches('%').count();
    if wildcard_count > 2 {
        return Err(TokenError::invalid("搜索模式中最多允许包含 2 个 % 通配符"));
    }
    if wildcard_count > 0 && escaped.replace('%', "").len() < 2 {
        return Err(TokenError::invalid(
            "使用模糊搜索时，关键词长度至少为 2 个字符",
        ));
    }
    Ok(escaped)
}
fn generate_key() -> String {
    rand::rng()
        .sample_iter(&Alphanumeric)
        .take(48)
        .map(char::from)
        .collect()
}
fn mask_key(key: &str) -> String {
    let n = key.len();
    if n <= 4 {
        "*".repeat(n)
    } else if n <= 8 {
        let start = key.get(..2).unwrap_or_default();
        let end = key.get(n - 2..).unwrap_or_default();
        format!("{start}****{end}")
    } else {
        let start = key.get(..4).unwrap_or_default();
        let end = key.get(n - 4..).unwrap_or_default();
        format!("{start}**********{end}")
    }
}
fn unix_now() -> i64 {
    std::time::UNIX_EPOCH
        .elapsed()
        .map_or(0, |duration| duration.as_secs() as i64)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn masks_only_presentation_key() {
        assert_eq!(mask_key("abcdefghij"), "abcd**********ghij");
    }
    #[test]
    fn search_pattern_escapes_like_metacharacters() {
        assert_eq!(like_pattern("a_b").unwrap(), "a!_b");
        assert_eq!(like_pattern("%_").unwrap(), "%!_");
        assert_eq!(like_pattern("%!").unwrap(), "%!!");
        assert!(like_pattern("%").is_err());
    }

    #[test]
    fn search_pattern_preserves_frozen_go_error_messages_and_byte_length() {
        assert_eq!(
            like_pattern("a%%b").unwrap_err().message,
            "搜索模式中不允许包含连续的 % 通配符"
        );
        assert_eq!(
            like_pattern("%a%b%").unwrap_err().message,
            "搜索模式中最多允许包含 2 个 % 通配符"
        );
        assert_eq!(
            like_pattern("%a").unwrap_err().message,
            "使用模糊搜索时，关键词长度至少为 2 个字符"
        );
        // Go's len(string) counts UTF-8 bytes.  A single `é` is therefore a
        // valid fuzzy-search literal after `%` is removed, unlike a one-byte
        // ASCII character.
        assert_eq!(like_pattern("%é").unwrap(), "%é");
        assert_eq!(like_pattern("%中").unwrap(), "%中");
    }

    #[test]
    fn empty_status_only_query_keeps_the_full_update_path() {
        assert!(!StatusOnlyQuery::from_raw(None).enabled());
        assert!(!StatusOnlyQuery::from_raw(Some("status_only=")).enabled());
        assert!(!StatusOnlyQuery::from_raw(Some("status_only")).enabled());
        assert!(StatusOnlyQuery::from_raw(Some("status_only=1")).enabled());
    }

    #[test]
    fn route_principal_only_requires_the_verified_userauth_identity() {
        assert!(
            principal_auth_error(ApiTokenPrincipal {
                user_id: 7,
                role: 1,
                preferred_language: None,
            })
            .is_none()
        );
        assert!(
            principal_auth_error(ApiTokenPrincipal {
                user_id: 7,
                role: 10,
                preferred_language: None,
            })
            .is_none()
        );
        assert!(
            principal_auth_error(ApiTokenPrincipal {
                user_id: 7,
                role: 0,
                preferred_language: None,
            })
            .is_some()
        );
        assert!(
            principal_auth_error(ApiTokenPrincipal {
                user_id: 0,
                role: 100,
                preferred_language: None,
            })
            .is_some()
        );
        assert_eq!(
            principal_auth_error(ApiTokenPrincipal {
                user_id: 7,
                role: 0,
                preferred_language: None,
            }),
            Some(PrincipalAuthError::InsufficientPrivilege)
        );
        assert_eq!(
            principal_auth_error(ApiTokenPrincipal {
                user_id: 7,
                role: 2,
                preferred_language: None,
            }),
            Some(PrincipalAuthError::InvalidUserInfo)
        );
        assert_eq!(
            principal_auth_error(ApiTokenPrincipal {
                user_id: 7,
                role: -1,
                preferred_language: None,
            }),
            Some(PrincipalAuthError::InvalidUserInfo)
        );
    }

    #[test]
    fn validation_messages_follow_accept_language() {
        assert_eq!(
            TokenMessage::QuotaExceeds(42).render(TokenLocale::En),
            "Quota value exceeds valid range, maximum is 42"
        );
        assert_eq!(
            TokenMessage::BatchTooMany.render(TokenLocale::ZhCn),
            "批量请求数量过多，最多 100 条"
        );
        assert_eq!(
            TokenMessage::ExpiredCannotEnable.render(TokenLocale::ZhTw),
            "令牌已過期，無法啟用，請先修改令牌過期時間，或者設定為永不過期"
        );
    }

    #[test]
    fn omitted_token_fields_keep_the_go_zero_defaults() {
        let created: TokenInput = serde_json::from_str(r#"{"name":"zero"}"#)
            .expect("minimal Go-compatible token body deserializes");
        assert_eq!(created.expired_time, None);
        let updated: TokenUpdate = serde_json::from_str(r#"{"name":"zero"}"#)
            .expect("minimal Go-compatible update body deserializes");
        assert_eq!(updated.id, 0);
        assert_eq!(updated.status, 0);
        assert_eq!(updated.token.expired_time, None);
        assert_eq!(updated.token.remain_quota, 0);
        assert!(!updated.token.unlimited_quota);
        assert!(!updated.token.model_limits_enabled);
        assert_eq!(updated.token.model_limits, "");
        assert!(updated.token.allow_ips.is_none());
        assert_eq!(updated.token.group, "");
        assert!(!updated.token.cross_group_retry);
    }

    #[test]
    fn create_uses_gorm_defaults_for_missing_and_zero_expiration() {
        assert_eq!(legacy_create_expired_time(None), -1);
        assert_eq!(legacy_create_expired_time(Some(0)), -1);
        assert_eq!(legacy_create_expired_time(Some(42)), 42);
    }

    #[test]
    fn token_wire_preserves_go_zero_for_an_omitted_expiration() {
        let created: TokenInput =
            serde_json::from_str(r#"{"name":"zero"}"#).expect("minimal token body deserializes");
        assert_eq!(created.expired_time.unwrap_or_default(), 0);
    }

    #[test]
    fn token_wire_reports_go_field_type_errors_and_accepts_null_scalars() {
        let error = decode_legacy_json::<TokenInput>(br#"{"remain_quota":"100"}"#)
            .expect_err("wrong quota type must fail");
        assert_eq!(
            error.message,
            "json: cannot unmarshal string into Go struct field tokenRequest.Token.remain_quota of type int"
        );
        let input = decode_legacy_json::<TokenInput>(br#"{"name":null,"expired_time":null}"#)
            .expect("Go accepts null scalar fields");
        assert_eq!(input.name, "");
        assert_eq!(input.expired_time, None);
    }

    #[test]
    fn token_wire_reports_go_top_level_shape_errors_for_create_and_update() {
        for (shape, body) in [
            ("array", br#"[]"#.as_slice()),
            ("bool", br#"true"#.as_slice()),
            ("string", br#""token""#.as_slice()),
            ("number", br#"1"#.as_slice()),
        ] {
            let expected = format!(
                "json: cannot unmarshal {shape} into Go value of type controller.tokenRequest"
            );
            assert_eq!(
                decode_legacy_json::<TokenInput>(body)
                    .expect_err("create shape error")
                    .message,
                expected,
                "create top-level {shape}"
            );
            assert_eq!(
                decode_legacy_json::<TokenUpdate>(body)
                    .expect_err("update shape error")
                    .message,
                expected,
                "update top-level {shape}"
            );
        }
    }

    #[test]
    fn token_batch_wire_keeps_empty_batch_as_a_handler_level_invalid_params() {
        let batch =
            decode_legacy_json::<TokenBatch>(br#"{}"#).expect("missing ids decodes as empty");
        assert!(batch.ids.is_empty());
        let batch = decode_legacy_json::<TokenBatch>(br#"{"ids":null}"#)
            .expect("null ids decodes as empty");
        assert!(batch.ids.is_empty());
    }

    #[test]
    fn token_wire_checks_unused_fields_and_keeps_last_valid_duplicate() {
        let error = decode_legacy_json::<TokenInput>(
            br#"{"created_time":"bad","name":"ignored-after-error"}"#,
        )
        .expect_err("known unused fields still bind");
        assert_eq!(
            error.message,
            "json: cannot unmarshal string into Go struct field tokenRequest.Token.created_time of type int64"
        );

        let input = decode_legacy_json::<TokenInput>(
            br#"{"Name":"first","name":"last"} {"name":"trailing"}"#,
        )
        .expect("the one-shot decoder accepts a trailing JSON value");
        assert_eq!(input.name, "last");
    }

    #[test]
    fn token_wire_retains_the_first_type_error_after_later_duplicates() {
        let error = decode_legacy_json::<TokenInput>(br#"{"remain_quota":1.5,"Remain_Quota":7}"#)
            .expect_err("the first type error is retained");
        assert_eq!(
            error.message,
            "json: cannot unmarshal number 1.5 into Go struct field tokenRequest.Token.remain_quota of type int"
        );

        for number in ["1e-1", "9223372036854775808"] {
            let error = decode_legacy_json::<TokenInput>(
                format!(r#"{{"remain_quota":{number}}}"#).as_bytes(),
            )
            .expect_err("non-integer and overflowing numbers must fail");
            assert!(error.message.contains(&format!("number {number}")));
        }
    }

    #[test]
    fn batch_wire_only_validates_ids_and_accepts_null_elements_as_zero() {
        let batch = decode_legacy_json::<TokenBatch>(br#"{"unknown":"ignored","ids":[null,2]}"#)
            .expect("unknown fields and null scalar elements are ignored/zero");
        assert_eq!(batch.ids, vec![0, 2]);

        let error = decode_legacy_json::<TokenBatch>(br#"{"ids":[1.5]}"#)
            .expect_err("fractional batch ids must fail");
        assert_eq!(
            error.message,
            "json: cannot unmarshal number 1.5 into Go struct field TokenBatch.ids of type []int"
        );
    }

    #[test]
    fn token_limit_reads_the_flattened_option_value() {
        assert_eq!(parse_max_user_tokens_option("37", 1000), 37);
        assert_eq!(parse_max_user_tokens_option("2.000000", 1000), 2);
        assert_eq!(parse_max_user_tokens_option("not-an-integer", 1000), 1000);
    }

    #[test]
    fn raw_query_ignores_bad_integers_and_uses_legacy_size_fallbacks() {
        let query = PageQuery::from_raw(Some("p=nope&page_size=0&ps=7&size=9"));
        assert_eq!((query.page().number, query.page().size), (1, 7));
        let query = PageQuery::from_raw(Some("p=-1&page_size=101"));
        assert_eq!((query.page().number, query.page().size), (-1, 100));
        assert_eq!(
            SearchQuery::from_raw(Some("keyword=%25%E4%B8%AD&token=sk-%25abc")).keyword,
            "%中"
        );
        assert!(StatusOnlyQuery::from_raw(Some("%73tatus_only=true")).enabled());
    }

    #[test]
    fn raw_query_uses_field_specific_atoi_overflow_and_first_repeated_values() {
        let positive =
            "p=9223372036854775808&p=2&ps=9223372036854775808&ps=3&size=9223372036854775808&size=4";
        let positive_page = PageQuery::from_raw(Some(positive)).page();
        assert_eq!((positive_page.number, positive_page.size), (i64::MAX, 100));

        let negative = "p=-9223372036854775809&p=2&ps=-9223372036854775809&ps=3&size=-9223372036854775809&size=4";
        let negative_page = PageQuery::from_raw(Some(negative)).page();
        assert_eq!(
            (negative_page.number, negative_page.size),
            (i64::MIN, i64::MIN)
        );

        assert_eq!(raw_query_i64(Some(positive), "p"), Some(i64::MAX));
        assert_eq!(raw_query_i64(Some(negative), "ps"), Some(i64::MIN));
        assert_eq!(
            raw_query_i64_if_valid(Some("page_size=9223372036854775808"), "page_size"),
            None
        );
        assert_eq!(
            raw_query_i64_if_valid(Some("page_size=-9223372036854775809"), "page_size"),
            None
        );

        for overflow in [
            "page_size=9223372036854775808&page_size=4&ps=7",
            "page_size=-9223372036854775809&page_size=4&ps=7",
        ] {
            assert_eq!(PageQuery::from_raw(Some(overflow)).page().size, 7);
        }
        for overflow in [
            "page_size=9223372036854775808&page_size=4&size=8",
            "page_size=-9223372036854775809&page_size=4&size=8",
        ] {
            assert_eq!(PageQuery::from_raw(Some(overflow)).page().size, 8);
        }
        for overflow in [
            "page_size=9223372036854775808&page_size=4",
            "page_size=-9223372036854775809&page_size=4",
        ] {
            assert_eq!(PageQuery::from_raw(Some(overflow)).page().size, 10);
        }
        assert_eq!(
            PageQuery::from_raw(Some("page_size=4&page_size=8&ps=7"))
                .page()
                .size,
            4
        );
    }
    #[test]
    fn page_preserves_legacy_negative_list_values_but_search_normalizes_them() {
        let page = Page::from_query(Some(-1), Some(1_000));
        assert_eq!((page.number, page.size), (-1, 100));
        assert_eq!(page.search_limit(), 100);
        assert_eq!(page.search_offset(), 0);

        let negative_size = Page::from_query(Some(1), Some(-1));
        assert_eq!((negative_size.number, negative_size.size), (1, -1));
        assert_eq!(negative_size.search_limit(), 100);
        assert_eq!(negative_size.search_offset(), 0);

        let wrapping = Page::from_query(Some(i64::MIN), Some(10));
        assert_eq!(wrapping.offset(), -10);
        assert_eq!(wrapping.search_offset(), 0);
    }

    #[tokio::test]
    async fn unmounted_http_router_preserves_malformed_json_errors_after_userauth() {
        use axum::{
            body::{Body, to_bytes},
            http::{Request, StatusCode},
        };
        use tower::ServiceExt;

        let service = Arc::new(PgValkeyApiTokenService::new(
            PgPool::connect_lazy("postgres://127.0.0.1:1/unused").expect("lazy pool"),
            redis::Client::open("redis://127.0.0.1/").expect("Valkey URL"),
        ));
        let router = api_token_router(ApiTokenHttpState::new(service));
        for (language, message) in [
            (None, "unexpected EOF"),
            (Some("zh-CN"), "unexpected EOF"),
            (Some("zh-TW"), "unexpected EOF"),
        ] {
            let mut request = Request::builder()
                .method("POST")
                .uri("/api/token/")
                // A principal only reaches this router after the mounted
                // UserAuth boundary has checked its status and role.
                .extension(ApiTokenPrincipal {
                    user_id: 7,
                    role: 1,
                    preferred_language: None,
                })
                .header(header::CONTENT_TYPE, "application/json");
            if let Some(language) = language {
                request = request.header(header::ACCEPT_LANGUAGE, language);
            }
            let response = router
                .clone()
                .oneshot(
                    request
                        .body(Body::from("{"))
                        .expect("valid malformed-body request"),
                )
                .await
                .expect("router is infallible");

            assert_eq!(response.status(), StatusCode::OK, "{language:?}");
            let body: serde_json::Value = serde_json::from_slice(
                &to_bytes(response.into_body(), usize::MAX)
                    .await
                    .expect("response body"),
            )
            .expect("legacy JSON envelope");
            assert_eq!(
                body,
                serde_json::json!({"success": false, "message": message}),
                "{language:?}"
            );
        }
    }

    #[tokio::test]
    async fn unmounted_http_router_rejects_oversized_bytes_before_decode() {
        use axum::{
            body::Body,
            http::{Request, StatusCode},
        };
        use tower::ServiceExt;

        let service = Arc::new(PgValkeyApiTokenService::new(
            PgPool::connect_lazy("postgres://127.0.0.1:1/unused").expect("lazy pool"),
            redis::Client::open("redis://127.0.0.1/").expect("Valkey URL"),
        ));
        let router = api_token_router(ApiTokenHttpState::new(service));
        let response = router
            .oneshot(
                Request::post("/api/token/")
                    .extension(ApiTokenPrincipal {
                        user_id: 7,
                        role: 1,
                        preferred_language: None,
                    })
                    .header("content-type", "application/json")
                    .body(Body::from(vec![b'x'; API_TOKEN_REQUEST_MAX_BYTES + 1]))
                    .expect("oversized request"),
            )
            .await
            .expect("router is infallible");

        assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
    }

    #[tokio::test]
    async fn unmounted_http_router_localizes_parameter_validation_after_userauth() {
        use axum::{
            body::{Body, to_bytes},
            http::{Request, StatusCode},
        };
        use tower::ServiceExt;

        let service = Arc::new(PgValkeyApiTokenService::new(
            PgPool::connect_lazy("postgres://127.0.0.1:1/unused").expect("lazy pool"),
            redis::Client::open("redis://127.0.0.1/").expect("Valkey URL"),
        ));
        let router = api_token_router(ApiTokenHttpState::new(service));
        for (language, message) in [
            (None, "Invalid parameters"),
            (Some("zh-CN"), "无效的参数"),
            (Some("zh-TW"), "無效的參數"),
        ] {
            let mut request = Request::builder()
                .method("POST")
                .uri("/api/token/batch")
                .extension(ApiTokenPrincipal {
                    user_id: 7,
                    role: 1,
                    preferred_language: None,
                })
                .header(header::CONTENT_TYPE, "application/json");
            if let Some(language) = language {
                request = request.header(header::ACCEPT_LANGUAGE, language);
            }
            let response = router
                .clone()
                .oneshot(
                    request
                        .body(Body::from(r#"{"ids":[]}"#))
                        .expect("valid empty-batch request"),
                )
                .await
                .expect("router is infallible");

            assert_eq!(response.status(), StatusCode::OK, "{language:?}");
            let body: serde_json::Value = serde_json::from_slice(
                &to_bytes(response.into_body(), usize::MAX)
                    .await
                    .expect("response body"),
            )
            .expect("legacy JSON envelope");
            assert_eq!(
                body,
                serde_json::json!({"success": false, "message": message}),
                "{language:?}"
            );
        }
    }

    #[tokio::test]
    async fn cache_key_uses_the_legacy_crypto_secret_hmac_namespace() {
        let service = PgValkeyApiTokenService::new(
            PgPool::connect_lazy("postgres://127.0.0.1:1/unused").expect("lazy pool"),
            redis::Client::open("redis://127.0.0.1/").expect("Valkey URL"),
        )
        .with_crypto_secret("secret");
        assert_eq!(
            service.cache_key("key").expect("HMAC cache key"),
            "token:96de09a0f8699191b28587118ac57df88bbf6c2d0c131d196dcd90f7efd68c93" // gitleaks:allow -- deterministic HMAC fixture
        );
    }
}
