//! Legacy-compatible `GET /v1/models` vertical slice.

use std::{
    borrow::Cow,
    net::{IpAddr, Ipv4Addr},
    sync::Arc,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use crate::RequestContext;
use crate::auth::DashboardUser;
use async_trait::async_trait;
use axum::{
    Json, Router,
    extract::{Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::get,
};
use hmac::{Hmac, Mac};
use serde::Serialize;
use serde_json::Value;
use sha2::Sha256;
use sqlx::{PgPool, Row};
use thiserror::Error;

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct ModelView {
    pub id: String,
    pub object: &'static str,
    pub created: i64,
    pub owned_by: String,
    pub supported_endpoint_types: Vec<String>,
}

impl ModelView {
    #[must_use]
    pub fn new(id: impl Into<String>, owned_by: impl Into<String>) -> Self {
        Self {
            id: id.into(),
            object: "model",
            created: 1_626_777_600,
            owned_by: owned_by.into(),
            supported_endpoint_types: Vec::new(),
        }
    }
}

#[derive(Clone, Debug)]
pub struct ModelsRequest {
    pub authorization: Option<String>,
    pub api_key: Option<String>,
    pub gemini_key: Option<String>,
    pub mj_api_secret: Option<String>,
    pub client_ip: IpAddr,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ModelsErrorKind {
    MissingToken,
    InvalidToken,
    /// A valid credential whose discovery decision must remain concealed.
    ///
    /// This covers both an explicit trust denial and a failure to resolve the
    /// current trust/payment facts. Model inventory and billing failures use
    /// [`Self::Database`] and remain observable as internal errors.
    DiscoveryHidden,
    AccessDenied,
    UserBanned,
    Database,
}

#[derive(Debug, Error)]
#[error("models request failed: {kind:?}")]
pub struct ModelsError {
    pub kind: ModelsErrorKind,
    pub message: Cow<'static, str>,
}

impl ModelsError {
    #[must_use]
    pub fn new(kind: ModelsErrorKind, message: impl Into<Cow<'static, str>>) -> Self {
        Self {
            kind,
            message: message.into(),
        }
    }
}

#[async_trait]
pub trait ModelsService: Send + Sync {
    async fn list(&self, request: ModelsRequest) -> Result<Vec<ModelView>, ModelsError>;

    /// Apply the current model-list developer gate before returning the
    /// inventory. The frozen Go 5418ce6 listener deliberately uses the
    /// legacy [`Self::list`] path and remains pure TokenAuth.
    async fn list_with_discovery_policy(
        &self,
        request: ModelsRequest,
    ) -> Result<Vec<ModelView>, ModelsError> {
        self.list(request).await
    }

    /// Authorize dashboard API-token discovery independently of
    /// [`ModelsService::list`]. Legacy model-list TokenAuth does not apply the
    /// dashboard developer/trust gate.
    async fn dashboard_discovery_access(&self, user: &DashboardUser) -> Result<bool, ModelsError> {
        Ok(crate::models::dashboard_discovery_access(user))
    }
}

/// PostgreSQL is authoritative. This slice deliberately treats Valkey as an
/// optional accelerator: cache absence or failure never changes authorization.
pub struct PgModelsService {
    pg: PgPool,
    valkey: Option<redis::Client>,
    crypto_secret: Arc<str>,
    cache_ttl: Duration,
    local_acceptance: bool,
}

impl PgModelsService {
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self {
            pg,
            valkey: None,
            crypto_secret: Arc::from(""),
            cache_ttl: Duration::from_secs(60),
            local_acceptance: false,
        }
    }

    #[must_use]
    pub fn with_valkey(
        pg: PgPool,
        valkey: redis::Client,
        crypto_secret: impl Into<Arc<str>>,
        cache_ttl: Duration,
    ) -> Self {
        Self {
            pg,
            valkey: Some(valkey),
            crypto_secret: crypto_secret.into(),
            cache_ttl,
            local_acceptance: false,
        }
    }

    /// Enables the explicitly loopback-scoped local acceptance policy.
    ///
    /// The normal listener supplies the validated configuration value.  The
    /// isolated frozen listener deliberately leaves this disabled.
    #[must_use]
    pub fn with_local_acceptance(mut self, enabled: bool) -> Self {
        self.local_acceptance = enabled;
        self
    }

    /// Applies the legacy token and user checks without deriving model visibility.
    ///
    /// `GET /v1/models/{model}` authenticates through the same boundary as the
    /// list endpoint, but retrieves from Go's process-wide static map instead
    /// of applying token limits, groups, channel state, or billing filters.
    pub async fn authenticate_only(&self, request: ModelsRequest) -> Result<(), ModelsError> {
        self.authenticate_only_with_policy(request, false).await
    }

    /// Authenticate a static model lookup, optionally applying the current Go
    /// developer/trust gate after token and user checks.  The frozen listener
    /// keeps the historical TokenAuth-only behaviour; the normal listener
    /// opts into the current policy before exposing the static catalogue.
    pub async fn authenticate_only_with_policy(
        &self,
        request: ModelsRequest,
        enforce_discovery_policy: bool,
    ) -> Result<(), ModelsError> {
        let (key, has_channel_suffix) = legacy_token_parts(&request).ok_or_else(invalid_token)?;
        let token = authenticate(self, &key, request.client_ip).await?;
        if enforce_discovery_policy && !self.developer_access_allowed(&token.user).await? {
            return Err(discovery_hidden());
        }
        if has_channel_suffix && token.role < 10 {
            return Err(ModelsError::new(
                ModelsErrorKind::AccessDenied,
                "普通用户不支持指定渠道",
            ));
        }
        Ok(())
    }
}

#[derive(Debug)]
struct AuthenticatedToken {
    user: CachedUser,
    user_group: String,
    token_group: String,
    model_limits_enabled: bool,
    model_limits: String,
    accept_unset_ratio_model: bool,
    role: i64,
}

#[async_trait]
impl ModelsService for PgModelsService {
    async fn list(&self, request: ModelsRequest) -> Result<Vec<ModelView>, ModelsError> {
        self.list_with_policy(request, false).await
    }

    async fn list_with_discovery_policy(
        &self,
        request: ModelsRequest,
    ) -> Result<Vec<ModelView>, ModelsError> {
        self.list_with_policy(request, true).await
    }

    async fn dashboard_discovery_access(&self, user: &DashboardUser) -> Result<bool, ModelsError> {
        self.developer_access_allowed(&CachedUser::from_dashboard_user(user))
            .await
    }
}

impl PgModelsService {
    async fn list_with_policy(
        &self,
        request: ModelsRequest,
        enforce_discovery_policy: bool,
    ) -> Result<Vec<ModelView>, ModelsError> {
        let (key, has_channel_suffix) = legacy_token_parts(&request).ok_or_else(invalid_token)?;
        let token = authenticate(self, &key, request.client_ip).await?;
        if enforce_discovery_policy && !self.developer_access_allowed(&token.user).await? {
            return Err(ModelsError::new(
                ModelsErrorKind::DiscoveryHidden,
                "Not Found",
            ));
        }
        if has_channel_suffix && token.role < 10 {
            return Err(ModelsError::new(
                ModelsErrorKind::AccessDenied,
                "普通用户不支持指定渠道",
            ));
        }
        let groups = self.groups_for(&token).await?;
        let billing = self.billing_config().await?;

        let limits = token
            .model_limits
            .split(',')
            .filter(|model| !model.is_empty())
            .collect::<std::collections::HashSet<_>>();
        let names = if token.model_limits_enabled {
            let mut names = limits.into_iter().map(str::to_owned).collect::<Vec<_>>();
            names.sort_unstable();
            names
        } else {
            self.enabled_models(&groups).await?
        };
        let owners = self.model_owners(&names, &groups).await?;
        let supported_endpoint_types = self.supported_endpoint_types(&names).await?;
        Ok(names
            .into_iter()
            .filter(|model| token.accept_unset_ratio_model || billing.has_config(model))
            .map(|model| {
                let owner = owners.get(&model).map_or("custom", |channel| {
                    owner_for_channel(Some(channel.channel_type))
                });
                ModelView {
                    supported_endpoint_types: supported_endpoint_types
                        .get(&model)
                        .cloned()
                        .unwrap_or_default(),
                    ..ModelView::new(model, owner)
                }
            })
            .collect())
    }
}

async fn authenticate(
    service: &PgModelsService,
    key: &str,
    client_ip: IpAddr,
) -> Result<AuthenticatedToken, ModelsError> {
    let token = service.token_by_key(key).await?;
    if token.status != 1
        || (token.expired_time != -1 && token.expired_time < unix_now())
        || (!token.unlimited_quota && token.remain_quota <= 0)
    {
        return Err(invalid_token());
    }
    if !ip_is_allowed(client_ip, &token.allow_ips) {
        return Err(ModelsError::new(
            ModelsErrorKind::AccessDenied,
            "您的 IP 不在令牌允许访问的列表中",
        ));
    }
    let user = service.user_by_id(token.user_id).await?;
    if user.status != 1 {
        return Err(ModelsError::new(
            ModelsErrorKind::UserBanned,
            "User has been banned",
        ));
    }
    let accept_unset_ratio_model = serde_json::from_str::<Value>(&user.setting)
        .ok()
        .and_then(|value| value.get("accept_unset_model_ratio_model")?.as_bool())
        .unwrap_or(false);
    Ok(AuthenticatedToken {
        user: user.clone(),
        user_group: user.group,
        token_group: token.group,
        model_limits_enabled: token.model_limits_enabled,
        model_limits: token.model_limits,
        accept_unset_ratio_model: accept_unset_ratio_model || service.self_use_mode().await?,
        role: user.role,
    })
}

/// Apply the dashboard discovery predicate to a resolved model owner. Legacy
/// model-list authentication deliberately does not call this gate.
#[must_use]
pub fn discovery_access_granted(
    user_id: i64,
    username: &str,
    status: i64,
    role: i64,
    trust_granted: Option<bool>,
) -> bool {
    discovery_access_granted_with_local_acceptance(
        user_id,
        username,
        status,
        role,
        trust_granted,
        false,
    )
}

/// Applies the ordinary-user fallback after role and trust-override checks.
///
/// A present trust decision is decisive, including an explicit denial. Local
/// acceptance is considered only when no override exists; administrators are
/// always granted by role after the principal validity checks.
#[must_use]
pub fn discovery_access_granted_with_local_acceptance(
    user_id: i64,
    username: &str,
    status: i64,
    role: i64,
    trust_granted: Option<bool>,
    local_acceptance: bool,
) -> bool {
    // Only the canonical common-user role may use the ordinary trust path.
    // Unknown low roles must not inherit access from a forged trust decision;
    // administrator and custom administrator roles are handled explicitly.
    if user_id <= 0 || username.trim().is_empty() || status != 1 || (role < 10 && role != 1) {
        return false;
    }
    if role >= 10 {
        return true;
    }
    trust_granted.unwrap_or(local_acceptance)
}

/// Applies the dashboard-side representation of the shared developer gate.
///
/// The dashboard adapter does not carry the Go trust override or the
/// normalized external-payment aggregate.  Do not treat its
/// `permissions.console_activated_at` convenience field as trust evidence.
/// Administrators are an explicit role-based allow; ordinary users remain
/// fail-closed until the authoritative trust aggregate is available here.
#[must_use]
pub fn dashboard_discovery_access(user: &DashboardUser) -> bool {
    discovery_access_granted(user.id, &user.username, user.status, user.role, None)
}

#[derive(Debug)]
struct CachedToken {
    user_id: i64,
    status: i32,
    expired_time: i64,
    remain_quota: i64,
    unlimited_quota: bool,
    model_limits_enabled: bool,
    model_limits: String,
    allow_ips: String,
    group: String,
}

#[derive(Debug, Clone)]
struct CachedUser {
    id: i64,
    group: String,
    email: String,
    quota: i64,
    status: i32,
    role: i64,
    username: String,
    setting: String,
    auth_version: i64,
}

impl CachedUser {
    fn from_dashboard_user(user: &DashboardUser) -> Self {
        Self {
            id: user.id,
            group: user.group.clone(),
            email: user.email.clone(),
            quota: user.quota,
            status: i32::try_from(user.status).unwrap_or_default(),
            role: user.role,
            username: user.username.clone(),
            setting: user.setting.clone(),
            auth_version: 0,
        }
    }
}

#[derive(Clone)]
struct PreferredChannel {
    channel_type: i32,
}

#[derive(Default)]
struct BillingConfig {
    model_price: std::collections::HashMap<String, Value>,
    model_ratio: std::collections::HashMap<String, Value>,
    billing_mode: std::collections::HashMap<String, String>,
    billing_expr: std::collections::HashMap<String, String>,
}

impl BillingConfig {
    fn has_config(&self, model: &str) -> bool {
        let pricing_model = legacy_pricing_model_name(model);
        self.model_price.contains_key(pricing_model)
            || self.model_ratio.contains_key(pricing_model)
            || (model.ends_with("-openai-compact")
                && (self.model_price.contains_key("*-openai-compact")
                    || self.model_ratio.contains_key("*-openai-compact")))
            || (self
                .billing_mode
                .get(model)
                .is_some_and(|mode| mode == "tiered_expr")
                && self
                    .billing_expr
                    .get(model)
                    .is_some_and(|expr| !expr.trim().is_empty()))
    }
}

/// This is the legacy pricing normalizer, not a permissive model-name prefix
/// match.  It only coalesces the documented gizmo and Gemini thinking variants
/// before looking up explicit price/ratio entries.
fn legacy_pricing_model_name(model: &str) -> &str {
    if model.starts_with("gpt-4-gizmo") {
        "gpt-4-gizmo-*"
    } else if model.starts_with("gpt-4o-gizmo") {
        "gpt-4o-gizmo-*"
    } else if model.starts_with("gemini-2.5-flash-lite") && model.contains("-thinking-") {
        "gemini-2.5-flash-lite-thinking-*"
    } else if model.starts_with("gemini-2.5-flash") && model.contains("-thinking-") {
        "gemini-2.5-flash-thinking-*"
    } else if model.starts_with("gemini-2.5-pro") && model.contains("-thinking-") {
        "gemini-2.5-pro-thinking-*"
    } else {
        model
    }
}

/// Returns the Go-compatible decisive result for a persisted trust override.
/// A present override, including zero, an out-of-range value, or malformed
/// text at this compatibility boundary, suppresses paid fallback and denies.
#[must_use]
fn trust_override_decision(raw: Option<&str>) -> Option<bool> {
    raw.map(|value| {
        value
            .trim()
            .parse::<i64>()
            .ok()
            .is_some_and(|level| (1..=4).contains(&level))
    })
}

const BASELINE_PAID_TOPUP_SQL: &str = r#"
    SELECT EXISTS (
        SELECT 1
        FROM top_ups
        WHERE user_id = $1
          AND status = 'success'
          AND COALESCE(money, 0) > 0
          AND COALESCE(amount, 0) > 0
          AND COALESCE(payment_provider, '') <> 'balance'
          AND COALESCE(payment_method, '') <> 'balance'
          AND (
              COALESCE(payment_provider, '') IN
                  ('epay', 'stripe', 'creem', 'waffo', 'waffo_pancake')
              OR (
                  COALESCE(payment_provider, '') = ''
                  AND COALESCE(payment_method, '') IN
                      ('stripe', 'creem', 'waffo', 'waffo_pancake', 'alipay', 'wxpay')
              )
          )
    )
"#;

/// Conservative paid-activation predicate for the baseline `top_ups` schema.
///
/// The baseline schema has no normalized settlement/quota columns. Requiring
/// both positive legacy amount fields avoids treating a quota grant or an
/// incomplete payment row as proof of paid activation. This intentionally
/// produces false negatives for modern rows whose only authoritative positive
/// fact is a normalized settlement/credited-quota field; those rows remain
/// hidden until the schema-aware path is available.
#[must_use]
#[cfg(test)]
fn baseline_paid_topup_row_qualifies(
    status: &str,
    money: f64,
    amount: i64,
    payment_method: Option<&str>,
    payment_provider: Option<&str>,
) -> bool {
    if status != "success" || money <= 0.0 || amount <= 0 {
        return false;
    }
    let method = payment_method.unwrap_or_default();
    let provider = payment_provider.unwrap_or_default();
    if provider == "balance" || method == "balance" {
        return false;
    }
    matches!(
        provider,
        "epay" | "stripe" | "creem" | "waffo" | "waffo_pancake"
    ) || (provider.is_empty()
        && matches!(
            method,
            "stripe" | "creem" | "waffo" | "waffo_pancake" | "alipay" | "wxpay"
        ))
}

impl PgModelsService {
    async fn developer_access_allowed(&self, user: &CachedUser) -> Result<bool, ModelsError> {
        if user.role >= 10 {
            return Ok(discovery_access_granted(
                user.id,
                &user.username,
                i64::from(user.status),
                user.role,
                Some(true),
            ));
        }

        // Newer Go schemas may carry an explicit trust override while older
        // installations do not.  Reading through the row JSON keeps this
        // compatibility boundary schema-tolerant without treating a lookup
        // or malformed override as an allow.
        let override_value = sqlx::query_scalar::<_, Option<String>>(
            "SELECT to_jsonb(users) ->> 'trust_level_override' FROM users WHERE id = $1 AND deleted_at IS NULL",
        )
        .bind(user.id)
        .fetch_one(&self.pg)
        .await
        // Trust lookup is an authorization fact. A dependency failure must
        // remain fail-closed, while a NULL JSON value means no override.
        .map_err(|_| discovery_hidden())?;
        if let Some(trust_granted) = trust_override_decision(override_value.as_deref()) {
            return Ok(trust_granted);
        }

        // Use only columns present in the baseline schema. A query error (for
        // example, a missing table during a partial migration) is an
        // authorization denial, never a discovery grant or an observable 500.
        let paid = sqlx::query_scalar::<_, bool>(BASELINE_PAID_TOPUP_SQL)
            .bind(user.id)
            .fetch_one(&self.pg)
            .await
            .map_err(|_| discovery_hidden())?;
        // The loopback-only acceptance switch is an explicit test/development
        // grant.  It must be applied after a persisted override (so an
        // explicit denial still wins), but before the paid fallback: a local
        // acceptance user is intentionally not required to have a top-up.
        let trust_granted = if self.local_acceptance {
            None
        } else {
            Some(paid)
        };
        Ok(discovery_access_granted_with_local_acceptance(
            user.id,
            &user.username,
            i64::from(user.status),
            user.role,
            trust_granted,
            self.local_acceptance,
        ))
    }

    async fn option(&self, key: &str) -> Result<Option<String>, ModelsError> {
        sqlx::query_scalar("SELECT value FROM options WHERE key = $1")
            .bind(key)
            .fetch_optional(&self.pg)
            .await
            .map_err(|_| database_error())
    }

    async fn self_use_mode(&self) -> Result<bool, ModelsError> {
        Ok(self
            .option("SelfUseModeEnabled")
            .await?
            .is_some_and(|value| value.eq_ignore_ascii_case("true") || value == "1"))
    }

    async fn billing_config(&self) -> Result<BillingConfig, ModelsError> {
        let (model_price, model_ratio, billing_mode, billing_expr) = tokio::try_join!(
            self.option("ModelPrice"),
            self.option("ModelRatio"),
            self.option("billing_setting.billing_mode"),
            self.option("billing_setting.billing_expr"),
        )?;
        Ok(BillingConfig {
            model_price: json_map(model_price),
            model_ratio: json_map(model_ratio),
            billing_mode: json_string_map(billing_mode),
            billing_expr: json_string_map(billing_expr),
        })
    }

    async fn groups_for(&self, token: &AuthenticatedToken) -> Result<Vec<String>, ModelsError> {
        let (configured_usable, group_ratio, special_groups, legacy_special_groups) = tokio::try_join!(
            self.option("UserUsableGroups"),
            self.option("GroupRatio"),
            self.option("group_ratio_setting.group_special_usable_group"),
            self.option("group_ratio_setting"),
        )?;
        let mut usable = json_string_map(configured_usable);
        let special_groups = json_nested_string_map(special_groups)
            .or_else(|| group_special_groups_from_setting(legacy_special_groups));
        if let Some(special_groups) = special_groups
            && let Some(adjustments) = special_groups.get(&token.user_group)
        {
            for (group, description) in adjustments {
                if let Some(group) = group.strip_prefix("-:") {
                    usable.remove(group);
                } else if let Some(group) = group.strip_prefix("+:") {
                    usable.insert(group.to_owned(), description.clone());
                } else {
                    usable.insert(group.clone(), description.clone());
                }
            }
        }
        if !token.user_group.is_empty() {
            usable
                .entry(token.user_group.clone())
                .or_insert_with(|| "用户分组".to_owned());
        }
        let requested = token.token_group.as_str();
        if !requested.is_empty() && requested != "auto" {
            // TokenAuth performs these two checks before the models handler.
            // Preserve its order and un-coded error envelopes exactly.
            if !usable.contains_key(requested) {
                return Err(ModelsError::new(
                    ModelsErrorKind::AccessDenied,
                    format!("无权访问 {requested} 分组"),
                ));
            }
            if !json_map(group_ratio).contains_key(requested) {
                return Err(ModelsError::new(
                    ModelsErrorKind::AccessDenied,
                    format!("分组 {requested} 已被弃用"),
                ));
            }
            return Ok(vec![requested.to_owned()]);
        }
        if requested != "auto" {
            return Ok(vec![token.user_group.clone()]);
        }
        let configured = json_list(self.option("AutoGroups").await?);
        let groups = configured
            .into_iter()
            .filter(|group| usable.contains_key(group))
            .collect::<Vec<_>>();
        Ok(if groups.is_empty() {
            vec![token.user_group.clone()]
        } else {
            groups
        })
    }

    async fn enabled_models(&self, groups: &[String]) -> Result<Vec<String>, ModelsError> {
        let rows = sqlx::query(
            r#"SELECT a.model, selected.ordinal
                 FROM unnest($1::text[]) WITH ORDINALITY AS selected("group", ordinal)
                 JOIN abilities a ON a."group" = selected."group"
                 JOIN channels c ON c.id = a.channel_id
                WHERE a.enabled = TRUE AND c.status = 1
                ORDER BY selected.ordinal, a.model"#,
        )
        .bind(groups)
        .fetch_all(&self.pg)
        .await
        .map_err(|_| database_error())?;
        let mut seen = std::collections::HashSet::new();
        Ok(rows
            .into_iter()
            .filter_map(|row| row.try_get::<String, _>("model").ok())
            .filter(|model| seen.insert(model.clone()))
            .collect())
    }

    async fn model_owners(
        &self,
        names: &[String],
        groups: &[String],
    ) -> Result<std::collections::HashMap<String, PreferredChannel>, ModelsError> {
        if names.is_empty() || groups.is_empty() {
            return Ok(std::collections::HashMap::new());
        }
        let rows = sqlx::query(
            r#"SELECT DISTINCT ON (a.model) a.model, c.type
                 FROM abilities a JOIN channels c ON c.id = a.channel_id
                WHERE a.model = ANY($1) AND a."group" = ANY($2)
                  AND a.enabled = TRUE AND c.status = 1
                ORDER BY a.model, COALESCE(a.priority, 0) DESC, a.weight DESC, a.channel_id ASC"#,
        )
        .bind(names)
        .bind(groups)
        .fetch_all(&self.pg)
        .await
        .map_err(|_| database_error())?;
        Ok(rows
            .into_iter()
            .filter_map(|row| {
                let model = row.try_get::<String, _>("model").ok()?;
                let channel_type = row.try_get("type").ok()?;
                Some((model.clone(), PreferredChannel { channel_type }))
            })
            .collect())
    }

    /// Mirrors Go's process-wide pricing cache: endpoint capabilities are
    /// derived from every enabled ability for a model, rather than only the
    /// requester's selected group or the preferred owner channel.
    async fn supported_endpoint_types(
        &self,
        names: &[String],
    ) -> Result<std::collections::HashMap<String, Vec<String>>, ModelsError> {
        if names.is_empty() {
            return Ok(std::collections::HashMap::new());
        }
        let rows = sqlx::query(
            r#"SELECT a.model, COALESCE(c.type, 0) AS channel_type
                 FROM abilities a
                 LEFT JOIN channels c ON c.id = a.channel_id
                WHERE a.model = ANY($1) AND a.enabled = TRUE"#,
        )
        .bind(names)
        .fetch_all(&self.pg)
        .await
        .map_err(|_| database_error())?;

        let mut supported = std::collections::HashMap::<String, Vec<String>>::new();
        for row in rows {
            let model = row
                .try_get::<String, _>("model")
                .map_err(|_| database_error())?;
            let channel_type = row
                .try_get::<i32, _>("channel_type")
                .map_err(|_| database_error())?;
            let endpoints = supported.entry(model.clone()).or_default();
            for endpoint in endpoint_types_for_channel(channel_type, &model) {
                if !endpoints.iter().any(|existing| existing == endpoint) {
                    endpoints.push(endpoint.to_owned());
                }
            }
        }
        Ok(supported)
    }

    async fn token_by_key(&self, key: &str) -> Result<CachedToken, ModelsError> {
        if let Some(token) = self.cache_token(key).await {
            return Ok(token);
        }
        let row = sqlx::query(r#"SELECT id, user_id, name, created_time, accessed_time, status::INT4 AS status, expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, allow_ips, used_quota, "group", cross_group_retry FROM tokens WHERE key = $1 AND deleted_at IS NULL"#)
            .bind(key).fetch_optional(&self.pg).await.map_err(|_| database_error())?.ok_or_else(invalid_token)?;
        let token = token_from_row(&row)?;
        self.store_token(key, &row).await;
        Ok(token)
    }

    async fn user_by_id(&self, id: i64) -> Result<CachedUser, ModelsError> {
        if let Some(user) = self.cache_user(id).await {
            return Ok(user);
        }
        let row = sqlx::query(r#"SELECT id, username, role, status::INT4 AS status, email, quota, "group", setting, auth_version FROM users WHERE id = $1 AND deleted_at IS NULL"#)
            .bind(id).fetch_optional(&self.pg).await.map_err(|_| database_error())?.ok_or_else(invalid_token)?;
        let user = user_from_row(&row)?;
        if self
            .auth_floor(id)
            .await
            .is_some_and(|floor| floor > user.auth_version)
        {
            return Err(database_error());
        }
        self.store_user(&user).await;
        Ok(user)
    }

    async fn connection(&self) -> Option<redis::aio::MultiplexedConnection> {
        self.valkey
            .as_ref()?
            .get_multiplexed_async_connection()
            .await
            .ok()
    }

    async fn cache_token(&self, key: &str) -> Option<CachedToken> {
        let mut connection = self.connection().await?;
        let values: Vec<Option<String>> = redis::cmd("HMGET")
            .arg(self.token_key(key)?)
            .arg("UserId")
            .arg("Status")
            .arg("ExpiredTime")
            .arg("RemainQuota")
            .arg("UnlimitedQuota")
            .arg("ModelLimitsEnabled")
            .arg("ModelLimits")
            .arg("AllowIps")
            .arg("Group")
            .query_async(&mut connection)
            .await
            .ok()?;
        let value = complete(values, 9)?;
        Some(CachedToken {
            user_id: value[0].parse().ok()?,
            status: value[1].parse().ok()?,
            expired_time: value[2].parse().ok()?,
            remain_quota: value[3].parse().ok()?,
            unlimited_quota: value[4].parse().ok()?,
            model_limits_enabled: value[5].parse().ok()?,
            model_limits: value[6].clone(),
            allow_ips: value[7].clone(),
            group: value[8].clone(),
        })
    }

    async fn cache_user(&self, id: i64) -> Option<CachedUser> {
        let mut connection = self.connection().await?;
        let values: Vec<Option<String>> = redis::cmd("HMGET")
            .arg(format!("user:{id}"))
            .arg("Id")
            .arg("Group")
            .arg("Email")
            .arg("Quota")
            .arg("Status")
            .arg("Role")
            .arg("Username")
            .arg("Setting")
            .arg("AuthVersion")
            .arg("CacheSchema")
            .query_async(&mut connection)
            .await
            .ok()?;
        let value = complete(values, 10)?;
        if value[9] != "2" {
            return None;
        }
        let user = CachedUser {
            id: value[0].parse().ok()?,
            group: value[1].clone(),
            email: value[2].clone(),
            quota: value[3].parse().ok()?,
            status: value[4].parse().ok()?,
            role: value[5].parse().ok()?,
            username: value[6].clone(),
            setting: value[7].clone(),
            auth_version: value[8].parse().ok()?,
        };
        (user.id == id
            && user.auth_version > 0
            && self
                .auth_floor(id)
                .await
                .is_none_or(|floor| floor <= user.auth_version))
        .then_some(user)
    }

    async fn auth_floor(&self, id: i64) -> Option<i64> {
        let mut connection = self.connection().await?;
        let values: Vec<Option<String>> = redis::cmd("MGET")
            .arg(format!("auth:user:fence:{id}"))
            .arg(format!("auth:user:version:{id}"))
            .query_async(&mut connection)
            .await
            .ok()?;
        values
            .into_iter()
            .flatten()
            .filter_map(|value| value.parse().ok())
            .max()
    }

    async fn store_token(&self, token: &str, row: &sqlx::postgres::PgRow) {
        let Some(key) = self.token_key(token) else {
            return;
        };
        let Some(mut connection) = self.connection().await else {
            return;
        };
        let fields = [
            ("Id", row_value::<i64>(row, "id").to_string()),
            ("UserId", row_value::<i64>(row, "user_id").to_string()),
            ("Key", String::new()),
            ("Status", row_value::<i32>(row, "status").to_string()),
            (
                "Name",
                row_value::<Option<String>>(row, "name").unwrap_or_default(),
            ),
            (
                "CreatedTime",
                row_value::<i64>(row, "created_time").to_string(),
            ),
            (
                "AccessedTime",
                row_value::<i64>(row, "accessed_time").to_string(),
            ),
            (
                "ExpiredTime",
                row_value::<i64>(row, "expired_time").to_string(),
            ),
            (
                "RemainQuota",
                row_value::<i64>(row, "remain_quota").to_string(),
            ),
            (
                "UnlimitedQuota",
                row_value::<bool>(row, "unlimited_quota").to_string(),
            ),
            (
                "ModelLimitsEnabled",
                row_value::<bool>(row, "model_limits_enabled").to_string(),
            ),
            (
                "ModelLimits",
                row_value::<Option<String>>(row, "model_limits").unwrap_or_default(),
            ),
            (
                "AllowIps",
                row_value::<Option<String>>(row, "allow_ips").unwrap_or_default(),
            ),
            ("UsedQuota", row_value::<i64>(row, "used_quota").to_string()),
            (
                "Group",
                row_value::<Option<String>>(row, "group").unwrap_or_default(),
            ),
            (
                "CrossGroupRetry",
                row_value::<bool>(row, "cross_group_retry").to_string(),
            ),
        ];
        // A reader must observe either the previous complete hash or this one;
        // HSET followed by EXPIRE leaks partially-populated credentials.
        let script = redis::Script::new(
            "redis.call('HSET', KEYS[1], unpack(ARGV, 2)); redis.call('EXPIRE', KEYS[1], ARGV[1]); return 1",
        );
        let mut invocation = script.key(key);
        invocation.arg(self.cache_ttl.as_secs());
        for (field, value) in fields {
            invocation.arg(field).arg(value);
        }
        let _: Result<i64, _> = invocation.invoke_async(&mut connection).await;
    }

    async fn store_user(&self, user: &CachedUser) {
        let Some(mut connection) = self.connection().await else {
            return;
        };
        let script = r#"local incoming=tonumber(ARGV[1]); local pending=tonumber(redis.call('GET',KEYS[2]) or '0'); local committed=tonumber(redis.call('GET',KEYS[3]) or '0'); local current=tonumber(redis.call('HGET',KEYS[1],'AuthVersion') or '0'); if pending>incoming or committed>incoming or current>incoming then return 0 end; if committed<incoming then redis.call('SET',KEYS[3],ARGV[1]) end; if pending>0 and pending<=incoming then redis.call('DEL',KEYS[2]) end; redis.call('HSET',KEYS[1],'Id',ARGV[2],'Group',ARGV[3],'Email',ARGV[4],'Status',ARGV[5],'Role',ARGV[6],'Username',ARGV[7],'Setting',ARGV[8],'AuthVersion',ARGV[1],'CacheSchema','2','Quota',ARGV[9]); redis.call('EXPIRE',KEYS[1],ARGV[10]); return 1"#;
        let _: Result<i64, _> = redis::Script::new(script)
            .key(format!("user:{}", user.id))
            .key(format!("auth:user:fence:{}", user.id))
            .key(format!("auth:user:version:{}", user.id))
            .arg(user.auth_version)
            .arg(user.id)
            .arg(&user.group)
            .arg(&user.email)
            .arg(user.status)
            .arg(user.role)
            .arg(&user.username)
            .arg(&user.setting)
            .arg(user.quota)
            .arg(self.cache_ttl.as_secs())
            .invoke_async(&mut connection)
            .await;
    }

    fn token_key(&self, token: &str) -> Option<String> {
        self.valkey.as_ref()?;
        let mut mac = Hmac::<Sha256>::new_from_slice(self.crypto_secret.as_bytes()).ok()?;
        mac.update(token.as_bytes());
        let digest = mac.finalize().into_bytes();
        let hash = digest
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect::<String>();
        Some(format!("token:{hash}"))
    }
}

fn row_value<T>(row: &sqlx::postgres::PgRow, name: &str) -> T
where
    T: for<'r> sqlx::Decode<'r, sqlx::Postgres> + sqlx::Type<sqlx::Postgres> + Default,
{
    row.try_get(name).unwrap_or_default()
}
fn token_from_row(row: &sqlx::postgres::PgRow) -> Result<CachedToken, ModelsError> {
    Ok(CachedToken {
        user_id: row.try_get("user_id").map_err(|_| database_error())?,
        status: row.try_get("status").map_err(|_| database_error())?,
        expired_time: row.try_get("expired_time").map_err(|_| database_error())?,
        remain_quota: row.try_get("remain_quota").map_err(|_| database_error())?,
        unlimited_quota: row
            .try_get("unlimited_quota")
            .map_err(|_| database_error())?,
        model_limits_enabled: row
            .try_get("model_limits_enabled")
            .map_err(|_| database_error())?,
        model_limits: row
            .try_get::<Option<String>, _>("model_limits")
            .map_err(|_| database_error())?
            .unwrap_or_default(),
        allow_ips: row
            .try_get::<Option<String>, _>("allow_ips")
            .map_err(|_| database_error())?
            .unwrap_or_default(),
        group: row
            .try_get::<Option<String>, _>("group")
            .map_err(|_| database_error())?
            .unwrap_or_default(),
    })
}
fn user_from_row(row: &sqlx::postgres::PgRow) -> Result<CachedUser, ModelsError> {
    Ok(CachedUser {
        id: row.try_get("id").map_err(|_| database_error())?,
        group: row
            .try_get::<Option<String>, _>("group")
            .map_err(|_| database_error())?
            .unwrap_or_default(),
        email: row
            .try_get::<Option<String>, _>("email")
            .map_err(|_| database_error())?
            .unwrap_or_default(),
        quota: row.try_get("quota").map_err(|_| database_error())?,
        status: row.try_get("status").map_err(|_| database_error())?,
        role: row.try_get("role").map_err(|_| database_error())?,
        username: row.try_get("username").map_err(|_| database_error())?,
        setting: row
            .try_get::<Option<String>, _>("setting")
            .map_err(|_| database_error())?
            .unwrap_or_default(),
        auth_version: row.try_get("auth_version").map_err(|_| database_error())?,
    })
}
fn complete(values: Vec<Option<String>>, expected: usize) -> Option<Vec<String>> {
    (values.len() == expected).then_some(())?;
    values.into_iter().collect()
}
fn unix_now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| {
            i64::try_from(duration.as_secs()).unwrap_or(i64::MAX)
        })
}

fn legacy_token_parts(request: &ModelsRequest) -> Option<(String, bool)> {
    let raw = request
        .gemini_key
        .as_deref()
        .filter(|key| !key.is_empty())
        .or(request.api_key.as_deref().filter(|key| !key.is_empty()))
        .or_else(|| {
            request
                .authorization
                .as_deref()
                .filter(|key| !key.is_empty())
        })
        .or(request
            .mj_api_secret
            .as_deref()
            .filter(|key| !key.is_empty()))?;
    let raw = raw
        .strip_prefix("Bearer ")
        .or_else(|| raw.strip_prefix("bearer "))
        .unwrap_or(raw)
        .trim();
    let raw = raw.strip_prefix("sk-").unwrap_or(raw);
    let raw = if raw.is_empty() || raw == "midjourney-proxy" {
        request.mj_api_secret.as_deref()?.trim()
    } else {
        raw
    };
    let raw = raw
        .strip_prefix("Bearer ")
        .or_else(|| raw.strip_prefix("bearer "))
        .unwrap_or(raw);
    let raw = raw.trim().strip_prefix("sk-").unwrap_or(raw.trim());
    let mut parts = raw.split('-');
    let key = parts.next()?;
    (!key.is_empty() && key != "midjourney-proxy").then(|| (key.to_owned(), parts.next().is_some()))
}

#[cfg(test)]
fn legacy_token_key(request: &ModelsRequest) -> Option<String> {
    legacy_token_parts(request).map(|(key, _)| key)
}

fn json_map(value: Option<String>) -> std::collections::HashMap<String, Value> {
    value
        .and_then(|raw| serde_json::from_str(&raw).ok())
        .unwrap_or_default()
}

fn json_string_map(value: Option<String>) -> std::collections::HashMap<String, String> {
    value
        .and_then(|raw| serde_json::from_str(&raw).ok())
        .unwrap_or_default()
}

fn json_nested_string_map(
    value: Option<String>,
) -> Option<std::collections::HashMap<String, std::collections::HashMap<String, String>>> {
    value.and_then(|raw| serde_json::from_str(&raw).ok())
}

fn group_special_groups_from_setting(
    value: Option<String>,
) -> Option<std::collections::HashMap<String, std::collections::HashMap<String, String>>> {
    let setting = value.and_then(|raw| serde_json::from_str::<Value>(&raw).ok())?;
    setting
        .get("group_special_usable_group")
        .cloned()
        .and_then(|value| serde_json::from_value(value).ok())
}

fn json_list(value: Option<String>) -> Vec<String> {
    value
        .and_then(|raw| serde_json::from_str(&raw).ok())
        .unwrap_or_default()
}

fn ip_is_allowed(client_ip: IpAddr, raw_limits: &str) -> bool {
    let limits = raw_limits
        .lines()
        .map(|line| line.replace([' ', ','], ""))
        .filter(|line| !line.is_empty())
        .collect::<Vec<_>>();
    limits.is_empty()
        || limits.iter().any(|limit| {
            limit
                .parse::<ipnet::IpNet>()
                .is_ok_and(|network| network.contains(&client_ip))
                || limit.parse::<IpAddr>().is_ok_and(|ip| ip == client_ip)
        })
}

/// Exact equivalent of Go's `GetEndpointTypesByChannelType` for the channel
/// families represented by this vertical slice. Advanced-custom routes fall
/// through to Go's same default when they have no parsed route configuration.
fn endpoint_types_for_channel(channel_type: i32, model_name: &str) -> Vec<&'static str> {
    let mut endpoint_types = match channel_type {
        38 => vec!["jina-rerank"],
        14 | 33 => vec!["anthropic", "openai"],
        24 | 41 => vec!["gemini", "openai"],
        20 => vec!["openai"],
        48 => vec!["openai", "openai-response"],
        55 => vec!["openai-video"],
        59 | 60 => vec![
            "openai",
            "openai-response",
            "openai-response-compact",
            "anthropic",
            "gemini",
            "openai-alpha-search",
        ],
        57 => vec![
            "openai-response",
            "openai-response-compact",
            "openai-alpha-search",
        ],
        _ if is_openai_response_only_model(model_name) => vec!["openai-response"],
        _ => vec!["openai"],
    };
    if is_image_generation_model(model_name) {
        endpoint_types.insert(0, "image-generation");
    }
    endpoint_types
}

fn is_openai_response_only_model(model_name: &str) -> bool {
    ["o3-pro", "o3-deep-research", "o4-mini-deep-research"]
        .iter()
        .any(|needle| model_name.contains(needle))
}

fn is_image_generation_model(model_name: &str) -> bool {
    let model_name = model_name.to_ascii_lowercase();
    ["dall-e-3", "dall-e-2", "gpt-image-1", "flux-", "flux.1-"]
        .iter()
        .any(|needle| model_name.contains(needle))
        || model_name.starts_with("imagen-")
}

fn owner_for_channel(channel_type: Option<i32>) -> &'static str {
    match channel_type {
        Some(1) => "openai",
        // These are adaptor.GetChannelName values, not display names from the
        // channels table.  Several differ materially from their type names.
        Some(14) => "claude",
        Some(4) => "ollama",
        Some(11) => "google palm",
        Some(15) => "baidu",
        Some(16) => "zhipu",
        Some(17) => "ali",
        Some(18) => "xunfei",
        Some(20) => "openrouter",
        Some(23) => "tencent",
        Some(24) => "google gemini",
        Some(25) => "moonshot",
        Some(26) => "zhipu_4v",
        Some(27) => "perplexity",
        Some(33) => "aws",
        Some(34) => "cohere",
        Some(35) => "minimax",
        Some(37) => "dify",
        Some(38) => "jina",
        Some(39) => "cloudflare",
        Some(40) => "siliconflow",
        Some(41) => "vertex-ai",
        Some(42) => "mistral",
        Some(43) => "deepseek",
        Some(44) => "mokaai",
        Some(45 | 46) => "volcengine",
        Some(47) => "xinference",
        Some(48) => "xai",
        Some(49) => "coze",
        Some(51) => "jimeng",
        Some(53) => "submodel",
        Some(56) => "replicate",
        Some(57) => "codex",
        Some(58) => "advanced_custom",
        Some(59) => "sub2api",
        Some(60) => "newapi",
        Some(0) | None => "unknown",
        Some(2) => "midjourney",
        Some(3) => "azure",
        Some(5) => "midjourneyplus",
        Some(6) => "openaimax",
        Some(7) => "ohmygpt",
        Some(8) => "custom",
        Some(9) => "ails",
        Some(10) => "aiproxy",
        Some(12) => "api2gpt",
        Some(13) => "aigc2d",
        Some(19) => "360",
        Some(21) => "aiproxylibrary",
        Some(22) => "fastgpt",
        Some(31) => "lingyiwanwu",
        Some(36) => "sunoapi",
        Some(50) => "kling",
        Some(52) => "vidu",
        Some(54) => "doubaovideo",
        Some(55) => "sora",
        Some(_) => "unknown",
    }
}

fn invalid_token() -> ModelsError {
    ModelsError::new(ModelsErrorKind::InvalidToken, "Invalid token")
}

fn discovery_hidden() -> ModelsError {
    ModelsError::new(ModelsErrorKind::DiscoveryHidden, "Not Found")
}

fn database_error() -> ModelsError {
    ModelsError::new(ModelsErrorKind::Database, "Database error")
}

#[derive(Clone)]
pub struct ModelsHttpState {
    service: Arc<dyn ModelsService>,
    version: Arc<str>,
    mode: ModelsListenerMode,
}

/// Selects the authorization contract for the model listener. The frozen
/// 5418ce6 mode is retained for its exact Go TokenAuth oracle; the normal
/// Rust listener opts into the current developer-trust policy explicitly.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ModelsListenerMode {
    CurrentTrustPolicy,
    FrozenGo5418ce6,
}

impl ModelsListenerMode {
    pub const FROZEN_GO_REVISION: &'static str = "5418ce6";

    const fn applies_discovery_policy(self) -> bool {
        matches!(self, Self::CurrentTrustPolicy)
    }
}

impl ModelsHttpState {
    #[must_use]
    pub fn new(service: Arc<dyn ModelsService>, version: impl Into<Arc<str>>) -> Self {
        Self {
            service,
            version: version.into(),
            // Unit and isolated oracle callers retain the historical pure
            // TokenAuth contract unless they opt into a named mode.
            mode: ModelsListenerMode::FrozenGo5418ce6,
        }
    }

    #[must_use]
    pub fn with_listener_mode(mut self, mode: ModelsListenerMode) -> Self {
        self.mode = mode;
        self
    }

    #[must_use]
    pub fn listener_mode(&self) -> ModelsListenerMode {
        self.mode
    }
}

pub fn models_router(state: ModelsHttpState) -> Router {
    Router::new()
        .route("/v1/models", get(list_models))
        .route("/v1beta/models", get(list_gemini_models))
        .route("/v1beta/openai/models", get(list_gemini_openai_models))
        .with_state(state)
}

async fn list_gemini_models(
    State(state): State<ModelsHttpState>,
    headers: HeaderMap,
    request: Request,
) -> Response {
    list_models_with_format(state, headers, request, ModelsFormat::Gemini).await
}

async fn list_gemini_openai_models(
    State(state): State<ModelsHttpState>,
    headers: HeaderMap,
    request: Request,
) -> Response {
    list_models_with_format(state, headers, request, ModelsFormat::GeminiOpenAi).await
}

async fn list_models(
    State(state): State<ModelsHttpState>,
    headers: HeaderMap,
    request: Request,
) -> Response {
    let format = if headers
        .get("x-goog-api-key")
        .is_some_and(|value| !value.is_empty())
        || request.uri().query().and_then(query_key).is_some()
    {
        // Gin dispatches these aliases through RetrieveModel on the bare
        // `/v1/models` path, where the model parameter is intentionally empty.
        ModelsFormat::GeminiRetrieveMissing
    } else if headers
        .get("x-api-key")
        .is_some_and(|value| !value.is_empty())
        && headers
            .get("anthropic-version")
            .is_some_and(|value| !value.is_empty())
    {
        ModelsFormat::Anthropic
    } else {
        ModelsFormat::OpenAi
    };
    list_models_with_format(state, headers, request, format).await
}

#[derive(Clone, Copy)]
enum ModelsFormat {
    OpenAi,
    Anthropic,
    Gemini,
    GeminiOpenAi,
    GeminiRetrieveMissing,
}

async fn list_models_with_format(
    state: ModelsHttpState,
    headers: HeaderMap,
    request: Request,
    format: ModelsFormat,
) -> Response {
    let request_context = request.extensions().get::<RequestContext>();
    let request_id = request_context.map_or_else(
        || uuid::Uuid::new_v4().to_string(),
        |context| context.request_id.clone(),
    );
    let authorization = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .map(str::to_owned);
    let raw_api_key = headers
        .get("x-api-key")
        .and_then(|value| value.to_str().ok())
        .map(str::to_owned);
    // TokenAuth only maps x-api-key onto Authorization for the OpenAI and
    // Anthropic aliases. Gemini aliases deliberately accept only their own
    // header/query key (or Authorization).
    let api_key = matches!(
        format,
        ModelsFormat::OpenAi | ModelsFormat::Anthropic | ModelsFormat::GeminiRetrieveMissing
    )
    .then_some(raw_api_key)
    .flatten();
    let requested_gemini_key = headers
        .get("x-goog-api-key")
        .and_then(|value| value.to_str().ok())
        .filter(|value| !value.is_empty())
        .map(str::to_owned)
        .or_else(|| request.uri().query().and_then(query_key));
    let gemini_key = matches!(format, ModelsFormat::Gemini | ModelsFormat::GeminiOpenAi)
        .then_some(requested_gemini_key)
        .flatten();
    let mj_api_secret = headers
        .get("mj-api-secret")
        .and_then(|value| value.to_str().ok())
        .map(str::to_owned);
    let client_ip = request_context
        .and_then(|context| context.client_ip)
        .or_else(|| {
            headers
                .get("x-real-ip")
                .and_then(|value| value.to_str().ok())
                .and_then(|value| value.parse().ok())
        })
        .unwrap_or(IpAddr::V4(Ipv4Addr::LOCALHOST));

    let request = ModelsRequest {
        authorization,
        api_key,
        gemini_key,
        mj_api_secret,
        client_ip,
    };
    let result = if state.mode.applies_discovery_policy() {
        state.service.list_with_discovery_policy(request).await
    } else {
        state.service.list(request).await
    };
    let mut response = match result {
        Ok(models) => success_response(models, format),
        Err(error) if error.kind == ModelsErrorKind::DiscoveryHidden => {
            discovery_not_found_response()
        }
        Err(error) => {
            let status = match error.kind {
                ModelsErrorKind::MissingToken | ModelsErrorKind::InvalidToken
                    if state.mode.applies_discovery_policy() =>
                {
                    StatusCode::NOT_FOUND
                }
                ModelsErrorKind::MissingToken | ModelsErrorKind::InvalidToken => {
                    StatusCode::UNAUTHORIZED
                }
                ModelsErrorKind::DiscoveryHidden => unreachable!("handled above"),
                ModelsErrorKind::AccessDenied | ModelsErrorKind::UserBanned => {
                    StatusCode::FORBIDDEN
                }
                ModelsErrorKind::Database => StatusCode::INTERNAL_SERVER_ERROR,
            };
            (
                status,
                Json(ErrorEnvelope {
                    error: ErrorBody {
                        message: format!(
                            "{} (request id: {request_id})",
                            if error.kind == ModelsErrorKind::Database {
                                "Database error, please contact the administrator"
                            } else {
                                error.message.as_ref()
                            }
                        ),
                        kind: "new_api_error",
                        code: if error.message == "您的 IP 不在令牌允许访问的列表中" {
                            "access_denied"
                        } else {
                            ""
                        },
                    },
                }),
            )
                .into_response()
        }
    };
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    if let Ok(version) = HeaderValue::from_str(&state.version) {
        response.headers_mut().insert("x-new-api-version", version);
    }
    if let Ok(request_id) = HeaderValue::from_str(&request_id) {
        response
            .headers_mut()
            .insert("x-oneapi-request-id", request_id);
    }
    response
}

fn discovery_not_found_response() -> Response {
    (
        StatusCode::NOT_FOUND,
        Json(serde_json::json!({"message": "Not Found"})),
    )
        .into_response()
}

fn query_key(query: &str) -> Option<String> {
    form_urlencoded::parse(query.as_bytes())
        .find_map(|(key, value)| (key == "key").then(|| value.into_owned()))
}

fn success_response(models: Vec<ModelView>, format: ModelsFormat) -> Response {
    match format {
        ModelsFormat::OpenAi => Json(ModelsEnvelope {
            data: models,
            object: "list",
            success: true,
        })
        .into_response(),
        ModelsFormat::GeminiOpenAi => Json(ModelsEnvelope {
            data: models,
            object: "list",
            success: true,
        })
        .into_response(),
        ModelsFormat::Anthropic => {
            if models.is_empty() {
                // Deliberately preserves the legacy handler's unguarded
                // `useranthropicModels[0]` failure as its public recovery
                // envelope.  Existing Anthropic clients depend on this exact
                // compatibility behaviour for an empty token model limit.
                return (
                    StatusCode::INTERNAL_SERVER_ERROR,
                    Json(LegacyPanicEnvelope {
                        error: LegacyPanicError {
                            message: "Panic detected, error: runtime error: index out of range [0] with length 0. Please submit a issue here: https://github.com/Calcium-Ion/new-api",
                            kind: "new_api_panic",
                        },
                    }),
                )
                    .into_response();
            }
            let data = models
                .into_iter()
                .map(|model| AnthropicModel {
                    id: model.id.clone(),
                    created_at: "2021-07-20T10:40:00Z",
                    display_name: model.id,
                    kind: "model",
                })
                .collect::<Vec<_>>();
            let first_id = data.first().map(|model| model.id.clone());
            let last_id = data.last().map(|model| model.id.clone());
            Json(AnthropicModelsEnvelope {
                data,
                first_id,
                has_more: false,
                last_id,
            })
            .into_response()
        }
        ModelsFormat::Gemini => Json(GeminiModelsEnvelope {
            models: models
                .into_iter()
                .map(|model| GeminiModel {
                    name: model.id.clone(),
                    display_name: model.id,
                    base_model_id: None,
                    description: None,
                    input_token_limit: None,
                    max_temperature: None,
                    output_token_limit: None,
                    supported_generation_methods: None,
                    temperature: None,
                    thinking: None,
                    top_k: None,
                    top_p: None,
                    version: None,
                })
                .collect(),
            next_page_token: None,
        })
        .into_response(),
        ModelsFormat::GeminiRetrieveMissing => Json(GeminiRetrieveMissingEnvelope {
            error: GeminiRetrieveError {
                message: "The model '' does not exist",
                kind: "invalid_request_error",
                param: "model",
                code: "model_not_found",
            },
        })
        .into_response(),
    }
}

#[derive(Serialize)]
struct ModelsEnvelope {
    data: Vec<ModelView>,
    object: &'static str,
    success: bool,
}

#[derive(Serialize)]
struct AnthropicModel {
    id: String,
    created_at: &'static str,
    display_name: String,
    #[serde(rename = "type")]
    kind: &'static str,
}

#[derive(Serialize)]
struct AnthropicModelsEnvelope {
    data: Vec<AnthropicModel>,
    first_id: Option<String>,
    has_more: bool,
    last_id: Option<String>,
}

#[derive(Serialize)]
struct GeminiModel {
    name: String,
    #[serde(rename = "displayName")]
    display_name: String,
    #[serde(rename = "baseModelId")]
    base_model_id: Option<Value>,
    description: Option<Value>,
    #[serde(rename = "inputTokenLimit")]
    input_token_limit: Option<Value>,
    #[serde(rename = "maxTemperature")]
    max_temperature: Option<Value>,
    #[serde(rename = "outputTokenLimit")]
    output_token_limit: Option<Value>,
    #[serde(rename = "supportedGenerationMethods")]
    supported_generation_methods: Option<Value>,
    temperature: Option<Value>,
    thinking: Option<Value>,
    #[serde(rename = "topK")]
    top_k: Option<Value>,
    #[serde(rename = "topP")]
    top_p: Option<Value>,
    version: Option<Value>,
}

#[derive(Serialize)]
struct GeminiModelsEnvelope {
    models: Vec<GeminiModel>,
    #[serde(rename = "nextPageToken")]
    next_page_token: Option<String>,
}

#[derive(Serialize)]
struct GeminiRetrieveMissingEnvelope {
    error: GeminiRetrieveError,
}

#[derive(Serialize)]
struct GeminiRetrieveError {
    message: &'static str,
    #[serde(rename = "type")]
    kind: &'static str,
    param: &'static str,
    code: &'static str,
}

#[derive(Serialize)]
struct ErrorEnvelope {
    error: ErrorBody,
}

#[derive(Serialize)]
struct ErrorBody {
    message: String,
    #[serde(rename = "type")]
    kind: &'static str,
    code: &'static str,
}

#[derive(Serialize)]
struct LegacyPanicEnvelope {
    error: LegacyPanicError,
}

#[derive(Serialize)]
struct LegacyPanicError {
    message: &'static str,
    #[serde(rename = "type")]
    kind: &'static str,
}

#[cfg(test)]
mod tests {
    use std::{
        net::{IpAddr, Ipv4Addr},
        sync::Mutex,
    };

    use axum::{
        body::{Body, to_bytes},
        http::{Request, header},
    };
    use serde_json::{Value, json};
    use tower::ServiceExt;

    use super::*;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    fn lock_unpoisoned<T>(mutex: &Mutex<T>) -> std::sync::MutexGuard<'_, T> {
        match mutex.lock() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        }
    }

    struct StubService;

    #[async_trait]
    impl ModelsService for StubService {
        async fn list(&self, request: ModelsRequest) -> Result<Vec<ModelView>, ModelsError> {
            assert_eq!(
                request.authorization.as_deref(),
                Some("Bearer sk-oraclemodelstoken")
            );
            assert_eq!(request.api_key, None);
            assert_eq!(request.client_ip, IpAddr::V4(Ipv4Addr::LOCALHOST));
            Ok(vec![
                ModelView::new("gpt-4o", "openai"),
                ModelView::new("text-embedding-3-small", "openai"),
            ])
        }
    }

    struct ErrorService(ModelsErrorKind);

    #[async_trait]
    impl ModelsService for ErrorService {
        async fn list(&self, _request: ModelsRequest) -> Result<Vec<ModelView>, ModelsError> {
            let message = match self.0 {
                ModelsErrorKind::Database => "Database error",
                ModelsErrorKind::AccessDenied => "denied",
                ModelsErrorKind::UserBanned => "User has been banned",
                ModelsErrorKind::DiscoveryHidden => "Not Found",
                _ => "Invalid token",
            };
            Err(ModelsError::new(self.0, message))
        }
    }

    struct ListenerModeService;

    #[async_trait]
    impl ModelsService for ListenerModeService {
        async fn list(&self, _request: ModelsRequest) -> Result<Vec<ModelView>, ModelsError> {
            Err(ModelsError::new(
                ModelsErrorKind::InvalidToken,
                "Invalid token",
            ))
        }

        async fn list_with_discovery_policy(
            &self,
            _request: ModelsRequest,
        ) -> Result<Vec<ModelView>, ModelsError> {
            Err(discovery_hidden())
        }
    }

    struct EnvelopeService;

    #[async_trait]
    impl ModelsService for EnvelopeService {
        async fn list(&self, _request: ModelsRequest) -> Result<Vec<ModelView>, ModelsError> {
            Ok(vec![ModelView::new("gpt-4o", "openai")])
        }
    }

    struct CaptureService(Arc<Mutex<Vec<ModelsRequest>>>);

    #[async_trait]
    impl ModelsService for CaptureService {
        async fn list(&self, request: ModelsRequest) -> Result<Vec<ModelView>, ModelsError> {
            lock_unpoisoned(&self.0).push(request);
            Ok(vec![ModelView::new("gpt-4o", "openai")])
        }
    }

    #[tokio::test]
    async fn matches_frozen_legacy_success_contract() -> TestResult {
        let response = models_router(ModelsHttpState::new(Arc::new(StubService), "v0.0.0"))
            .oneshot(
                Request::builder()
                    .uri("/v1/models")
                    .header(header::AUTHORIZATION, "Bearer sk-oraclemodelstoken")
                    .body(Body::empty())?,
            )
            .await?;

        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "application/json; charset=utf-8"
        );
        assert_eq!(response.headers()["x-new-api-version"], "v0.0.0");
        assert!(response.headers().contains_key("x-oneapi-request-id"));
        let body: Value =
            serde_json::from_slice(&to_bytes(response.into_body(), usize::MAX).await?)?;
        assert_eq!(
            body,
            json!({
                "data": [
                    {"id":"gpt-4o","object":"model","created":1626777600,"owned_by":"openai","supported_endpoint_types":[]},
                    {"id":"text-embedding-3-small","object":"model","created":1626777600,"owned_by":"openai","supported_endpoint_types":[]}
                ],
                "object":"list",
                "success":true
            })
        );
        Ok(())
    }

    #[test]
    fn token_parser_matches_legacy_prefix_and_suffix_rules() {
        let request = |authorization: Option<&str>, api_key: Option<&str>| ModelsRequest {
            authorization: authorization.map(str::to_owned),
            api_key: api_key.map(str::to_owned),
            gemini_key: None,
            mj_api_secret: None,
            client_ip: IpAddr::V4(Ipv4Addr::LOCALHOST),
        };
        assert_eq!(
            legacy_token_key(&request(Some("Bearer sk-alpha-channel"), None)).as_deref(),
            Some("alpha")
        );
        assert_eq!(
            legacy_token_key(&request(Some("bearer raw"), None)).as_deref(),
            Some("raw")
        );
        assert_eq!(
            legacy_token_key(&request(Some("Bearer ignored"), Some("sk-anthropic-extra")))
                .as_deref(),
            Some("anthropic")
        );
        assert_eq!(
            legacy_token_parts(&request(Some("Bearer sk-alpha-channel"), None)),
            Some(("alpha".to_owned(), true))
        );
        assert_eq!(legacy_token_key(&request(None, None)), None);
    }

    #[test]
    fn query_key_matches_form_urlencoded_and_first_value_semantics() {
        assert_eq!(
            query_key("key=slash%2Fplus%2Bsign"),
            Some("slash/plus+sign".to_owned())
        );
        assert_eq!(
            query_key("key=ordinary+plus"), // gitleaks:allow -- URL-decoding fixture
            Some("ordinary plus".to_owned())
        );
        assert_eq!(query_key("key=&key=second"), Some(String::new()));
    }

    #[tokio::test]
    async fn gemini_credentials_prefer_header_query_then_bearer() -> TestResult {
        let (router, captured) = capture_router();

        for request in [
            Request::builder()
                .uri("/v1beta/models?key=query-token")
                .header("x-goog-api-key", "header-token")
                .header(header::AUTHORIZATION, "Bearer plainbearer")
                .body(Body::empty())?,
            Request::builder()
                .uri("/v1beta/models?key=query-token")
                .header(header::AUTHORIZATION, "Bearer plainbearer")
                .body(Body::empty())?,
            Request::builder()
                .uri("/v1beta/models?key=query-token")
                .header("x-goog-api-key", "")
                .header(header::AUTHORIZATION, "Bearer plainbearer")
                .body(Body::empty())?,
            Request::builder()
                .uri("/v1beta/models?key=&key=second-token")
                .header(header::AUTHORIZATION, "Bearer plainbearer")
                .body(Body::empty())?,
        ] {
            router.clone().oneshot(request).await?;
        }

        let requests = lock_unpoisoned(&captured);
        assert_eq!(requests[0].gemini_key.as_deref(), Some("header-token"));
        assert_eq!(requests[1].gemini_key.as_deref(), Some("query-token"));
        assert_eq!(requests[2].gemini_key.as_deref(), Some("query-token"));
        assert_eq!(requests[3].gemini_key.as_deref(), Some(""));
        assert_eq!(
            legacy_token_key(&requests[3]).as_deref(),
            Some("plainbearer")
        );

        let anthropic = ModelsRequest {
            authorization: Some("Bearer bearer-token".to_owned()),
            api_key: Some("anthropictoken".to_owned()),
            gemini_key: None,
            mj_api_secret: None,
            client_ip: IpAddr::V4(Ipv4Addr::LOCALHOST),
        };
        assert_eq!(
            legacy_token_key(&anthropic).as_deref(),
            Some("anthropictoken")
        );
        Ok(())
    }

    #[tokio::test]
    async fn dispatches_anthropic_and_gemini_list_envelopes_from_legacy_credentials() -> TestResult
    {
        let router = models_router(ModelsHttpState::new(Arc::new(EnvelopeService), "v0.0.0"));
        let anthropic = router
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/v1/models")
                    .header("x-api-key", "sk-oraclemodelstoken")
                    .header("anthropic-version", "2023-06-01")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(
            json_body(anthropic).await?,
            json!({
                "data":[{"id":"gpt-4o","created_at":"2021-07-20T10:40:00Z","display_name":"gpt-4o","type":"model"}],
                "first_id":"gpt-4o", "has_more":false, "last_id":"gpt-4o"
            })
        );
        let gemini = router
            .oneshot(
                Request::builder()
                    .uri("/v1beta/models?key=sk-oraclemodelstoken")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(
            json_body(gemini).await?,
            json!({"models":[{"name":"gpt-4o","displayName":"gpt-4o","baseModelId":null,"description":null,"inputTokenLimit":null,"maxTemperature":null,"outputTokenLimit":null,"supportedGenerationMethods":null,"temperature":null,"thinking":null,"topK":null,"topP":null,"version":null}],"nextPageToken":null})
        );
        Ok(())
    }

    #[tokio::test]
    async fn credential_aliases_match_legacy_tokenauth_routing() -> TestResult {
        let (router, captured) = capture_router();

        let bare_google = router
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/v1/models?key=sk-google-is-not-tokenauth-here")
                    .header(header::AUTHORIZATION, "Bearer sk-oraclemodelstoken")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(
            json_body(bare_google).await?,
            json!({"error":{"message":"The model '' does not exist","type":"invalid_request_error","param":"model","code":"model_not_found"}})
        );

        let gemini = router
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/v1beta/models?key=sk-gemini-token")
                    .header("x-api-key", "sk-must-be-ignored")
                    .header(header::AUTHORIZATION, "Bearer sk-also-ignored")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(gemini.status(), StatusCode::OK);

        let gemini_openai = router
            .oneshot(
                Request::builder()
                    .uri("/v1beta/openai/models")
                    .header("x-goog-api-key", "sk-google-openai-token")
                    .header("x-api-key", "sk-must-be-ignored")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(
            json_body(gemini_openai).await?,
            json!({"data":[{"id":"gpt-4o","object":"model","created":1626777600,"owned_by":"openai","supported_endpoint_types":[]}],"object":"list","success":true})
        );

        let requests = lock_unpoisoned(&captured);
        assert_eq!(requests.len(), 3);
        assert_eq!(
            requests[0].authorization.as_deref(),
            Some("Bearer sk-oraclemodelstoken")
        );
        assert_eq!(requests[0].gemini_key, None);
        assert_eq!(
            requests[1].authorization,
            Some("Bearer sk-also-ignored".to_owned())
        );
        assert_eq!(requests[1].api_key, None);
        assert_eq!(requests[1].gemini_key, Some("sk-gemini-token".to_owned()));
        assert_eq!(requests[2].api_key, None);
        assert_eq!(
            requests[2].gemini_key,
            Some("sk-google-openai-token".to_owned())
        );
        Ok(())
    }

    #[tokio::test]
    async fn preserves_legacy_anthropic_empty_models_panic_envelope() -> TestResult {
        let response = success_response(Vec::new(), ModelsFormat::Anthropic);
        assert_eq!(response.status(), StatusCode::INTERNAL_SERVER_ERROR);
        assert_eq!(
            json_body(response).await?,
            json!({"error":{"message":"Panic detected, error: runtime error: index out of range [0] with length 0. Please submit a issue here: https://github.com/Calcium-Ion/new-api","type":"new_api_panic"}})
        );
        Ok(())
    }

    #[test]
    fn billing_and_owner_rules_match_legacy_dynamic_settings() {
        let billing = BillingConfig {
            billing_mode: [("dynamic-tiered".to_owned(), "tiered_expr".to_owned())]
                .into_iter()
                .collect(),
            billing_expr: [("dynamic-tiered".to_owned(), "tier(\"base\", p)".to_owned())]
                .into_iter()
                .collect(),
            ..BillingConfig::default()
        };
        assert!(billing.has_config("dynamic-tiered"));
        assert!(!billing.has_config("gpt-hardcoded-prefix-only"));
        assert_eq!(
            legacy_pricing_model_name("gemini-2.5-pro-preview-thinking-8192"),
            "gemini-2.5-pro-thinking-*"
        );
        assert_eq!(owner_for_channel(Some(14)), "claude");
        assert_eq!(owner_for_channel(Some(24)), "google gemini");
        assert_eq!(owner_for_channel(Some(41)), "vertex-ai");
        assert_eq!(owner_for_channel(Some(3)), "azure");
    }

    #[test]
    fn trust_override_is_the_only_ordinary_discovery_grant() {
        for (raw, expected) in [
            (None, None),
            (Some(""), Some(false)),
            (Some("0"), Some(false)),
            (Some("5"), Some(false)),
            (Some("unknown"), Some(false)),
            (Some(" 2 "), Some(true)),
            (Some("4"), Some(true)),
        ] {
            assert_eq!(trust_override_decision(raw), expected, "{raw:?}");
        }
    }

    #[test]
    fn local_acceptance_grants_only_without_an_explicit_override() {
        assert!(discovery_access_granted_with_local_acceptance(
            7,
            "local-user",
            1,
            1,
            None,
            true
        ));
        assert!(!discovery_access_granted_with_local_acceptance(
            7,
            "local-user",
            1,
            1,
            Some(false),
            true,
        ));
        assert!(discovery_access_granted_with_local_acceptance(
            7,
            "local-user",
            1,
            1,
            Some(true),
            false,
        ));
    }

    #[test]
    fn baseline_paid_topup_requires_external_success_and_both_legacy_amounts() {
        assert!(baseline_paid_topup_row_qualifies(
            "success",
            10.0,
            1,
            Some("alipay"),
            None,
        ));
        assert!(baseline_paid_topup_row_qualifies(
            "success",
            10.0,
            1,
            None,
            Some("stripe"),
        ));

        for (status, money, amount, method, provider) in [
            ("pending", 10.0, 1, Some("alipay"), None),
            ("success", 0.0, 1, Some("alipay"), None),
            ("success", 10.0, 0, Some("alipay"), None),
            ("success", 10.0, 1, Some("manual"), None),
            ("success", 10.0, 1, Some("balance"), Some("balance")),
            ("success", 10.0, 1, Some("stripe"), Some("unknown")),
        ] {
            assert!(!baseline_paid_topup_row_qualifies(
                status, money, amount, method, provider
            ));
        }

        // Modern rows whose only positive fact is normalized settlement or
        // credited quota are intentionally false negatives in this baseline
        // adapter: those columns are absent here, so discovery stays hidden.
    }

    #[test]
    fn parses_legacy_group_special_usable_group_setting() -> TestResult {
        let special = group_special_groups_from_setting(Some(
            r#"{"group_special_usable_group":{"default":{"+:vip":"VIP","-:unavailable":""}}}"#
                .to_owned(),
        ))
        .ok_or_else(|| std::io::Error::other("group special setting is unavailable"))?;
        assert_eq!(special["default"]["+:vip"], "VIP");
        assert_eq!(special["default"]["-:unavailable"], "");
        Ok(())
    }

    #[test]
    fn cidr_and_exact_ip_limits_are_enforced() -> TestResult {
        assert!(ip_is_allowed("10.2.3.4".parse()?, "10.0.0.0/8"));
        assert!(ip_is_allowed("127.0.0.1".parse()?, "127.0.0.1"));
        assert!(!ip_is_allowed("192.0.2.1".parse()?, "10.0.0.0/8"));
        assert!(ip_is_allowed("192.0.2.1".parse()?, ""));
        Ok(())
    }

    #[tokio::test]
    async fn emits_legacy_error_envelope_and_statuses() -> TestResult {
        for (kind, expected) in [
            (ModelsErrorKind::MissingToken, StatusCode::UNAUTHORIZED),
            (ModelsErrorKind::InvalidToken, StatusCode::UNAUTHORIZED),
            (ModelsErrorKind::DiscoveryHidden, StatusCode::NOT_FOUND),
            (ModelsErrorKind::AccessDenied, StatusCode::FORBIDDEN),
            (ModelsErrorKind::UserBanned, StatusCode::FORBIDDEN),
            (ModelsErrorKind::Database, StatusCode::INTERNAL_SERVER_ERROR),
        ] {
            let response =
                models_router(ModelsHttpState::new(Arc::new(ErrorService(kind)), "v0.0.0"))
                    .oneshot(Request::builder().uri("/v1/models").body(Body::empty())?)
                    .await?;
            assert_eq!(response.status(), expected);
            let body = json_body(response).await?;
            if kind == ModelsErrorKind::DiscoveryHidden {
                assert_eq!(body, json!({"message":"Not Found"}));
            } else {
                assert_eq!(body["error"]["type"], "new_api_error");
                assert!(
                    body["error"]["message"]
                        .as_str()
                        .is_some_and(|message| message.contains("request id:"))
                );
            }
            if matches!(
                kind,
                ModelsErrorKind::MissingToken | ModelsErrorKind::InvalidToken
            ) {
                assert_eq!(body["error"]["code"], "");
            }
            if kind == ModelsErrorKind::Database {
                assert!(body["error"]["message"].as_str().is_some_and(|message| {
                    message.starts_with("Database error, please contact the administrator")
                }));
            }
        }
        Ok(())
    }

    #[tokio::test]
    async fn frozen_5418ce6_mode_keeps_tokenauth_separate_from_current_policy() -> TestResult {
        let frozen = models_router(ModelsHttpState::new(
            Arc::new(ListenerModeService),
            "v0.0.0",
        ));
        let frozen_response = frozen
            .oneshot(Request::builder().uri("/v1/models").body(Body::empty())?)
            .await?;
        assert_eq!(frozen_response.status(), StatusCode::UNAUTHORIZED);

        let current = models_router(
            ModelsHttpState::new(Arc::new(ListenerModeService), "v0.0.0")
                .with_listener_mode(ModelsListenerMode::CurrentTrustPolicy),
        );
        let current_response = current
            .oneshot(Request::builder().uri("/v1/models").body(Body::empty())?)
            .await?;
        assert_eq!(current_response.status(), StatusCode::NOT_FOUND);
        Ok(())
    }

    #[tokio::test]
    async fn current_policy_keeps_model_backend_database_failure_visible() -> TestResult {
        let response = models_router(
            ModelsHttpState::new(Arc::new(ErrorService(ModelsErrorKind::Database)), "v0.0.0")
                .with_listener_mode(ModelsListenerMode::CurrentTrustPolicy),
        )
        .oneshot(Request::builder().uri("/v1/models").body(Body::empty())?)
        .await?;

        assert_eq!(response.status(), StatusCode::INTERNAL_SERVER_ERROR);
        Ok(())
    }

    async fn json_body(response: Response) -> Result<Value, Box<dyn std::error::Error>> {
        let body = to_bytes(response.into_body(), usize::MAX).await?;
        Ok(serde_json::from_slice(&body)?)
    }

    fn capture_router() -> (axum::Router, Arc<Mutex<Vec<ModelsRequest>>>) {
        let captured = Arc::new(Mutex::new(Vec::new()));
        let router = models_router(ModelsHttpState::new(
            Arc::new(CaptureService(captured.clone())),
            "v0.0.0",
        ));
        (router, captured)
    }
}
