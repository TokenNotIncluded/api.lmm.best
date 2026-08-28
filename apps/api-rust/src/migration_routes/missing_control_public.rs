//! Legacy-compatible public/control-plane reads that were not covered by the
//! first migration slices.
//!
//! This module deliberately contains no outbound client.  Every value is read
//! through an injected durable store, and all authentication is server-derived.
//! It is therefore safe to mount on the isolated Rust test instance without
//! accidentally reaching an upstream channel or provider.

use std::{
    collections::BTreeMap,
    sync::{Arc, RwLock},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    extract::{RawQuery, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::get,
};
use secrecy::SecretString;
use serde_json::{Map, Number, Value, json};
use sqlx::PgPool;

use crate::auth::{
    AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth, UserAuthPolicyError,
    dashboard_token_candidate, enforce_user_auth, user_auth_message,
};
use crate::{ClientIpKey, RequestContext, legacy_empty_response};

const ADMIN_ROLE: i64 = 10;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const GROUP_PATH: &str = "/api/group/";
const RATIO_CONFIG_PATH: &str = "/api/ratio_config";

/// Identity derived from a verified dashboard session.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct MissingControlPrincipal {
    pub user_id: i64,
    pub role: i64,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum MissingControlAuthError {
    UnmatchedOpaque,
    Unauthorized,
    TokenExpired,
    SessionRevoked,
    Unavailable,
    UserAuth(UserAuthPolicyError),
    /// A recognized dashboard user that `TryUserAuth` may expose to optional
    /// routes while required `UserAuth` routes still reject the policy error.
    PolicyInvalid {
        principal: MissingControlPrincipal,
        error: UserAuthPolicyError,
    },
}

/// Session boundary for dashboard-authenticated reads.
#[async_trait]
pub trait MissingControlAuthorizer: Send + Sync {
    async fn principal(
        &self,
        headers: &HeaderMap,
    ) -> Result<MissingControlPrincipal, MissingControlAuthError>;

    async fn optional_principal(
        &self,
        headers: &HeaderMap,
    ) -> Result<MissingControlPrincipal, MissingControlAuthError> {
        self.principal(headers).await
    }

    /// Resolves a principal backed by a live browser session. Implementations
    /// without session authority fail closed after validating the credential.
    async fn browser_session_principal(
        &self,
        headers: &HeaderMap,
    ) -> Result<Option<MissingControlPrincipal>, MissingControlAuthError> {
        self.principal(headers).await.map(|_| None)
    }
}

/// Production adapter over the listener's shared dashboard authentication.
#[derive(Clone)]
pub struct DashboardMissingControlAuthorizer {
    auth: Arc<dyn DashboardAuth>,
}

impl DashboardMissingControlAuthorizer {
    #[must_use]
    pub fn new(auth: Arc<dyn DashboardAuth>) -> Self {
        Self { auth }
    }
}

#[async_trait]
impl MissingControlAuthorizer for DashboardMissingControlAuthorizer {
    async fn principal(
        &self,
        headers: &HeaderMap,
    ) -> Result<MissingControlPrincipal, MissingControlAuthError> {
        let token = dashboard_credential(headers).ok_or(MissingControlAuthError::Unauthorized)?;
        let internal = dashboard_token_candidate(&token);
        match self.auth.self_user(SecretString::from(token)).await {
            Ok(user) => {
                let principal = MissingControlPrincipal {
                    user_id: user.id,
                    role: user.role,
                };
                match enforce_user_auth(&user) {
                    Ok(()) => Ok(principal),
                    Err(error) => Err(MissingControlAuthError::UserAuth(error)),
                }
            }
            Err(error) if error.kind == AuthErrorKind::TokenExpired => {
                Err(MissingControlAuthError::TokenExpired)
            }
            Err(error) if error.kind == AuthErrorKind::SessionRevoked => {
                Err(MissingControlAuthError::SessionRevoked)
            }
            Err(error)
                if matches!(
                    error.kind,
                    AuthErrorKind::Unauthorized | AuthErrorKind::InvalidCredentials
                ) =>
            {
                Err(if internal {
                    MissingControlAuthError::Unauthorized
                } else {
                    MissingControlAuthError::UnmatchedOpaque
                })
            }
            Err(error) if error.kind == AuthErrorKind::UserDisabled => Err(
                MissingControlAuthError::UserAuth(UserAuthPolicyError::UserDisabled),
            ),
            Err(_) => Err(MissingControlAuthError::Unavailable),
        }
    }

    async fn optional_principal(
        &self,
        headers: &HeaderMap,
    ) -> Result<MissingControlPrincipal, MissingControlAuthError> {
        let token = dashboard_credential(headers).ok_or(MissingControlAuthError::Unauthorized)?;
        let internal = dashboard_token_candidate(&token);
        match self
            .auth
            .self_user_for_optional(SecretString::from(token))
            .await
        {
            Ok(user) => {
                let principal = MissingControlPrincipal {
                    user_id: user.id,
                    role: user.role,
                };
                match enforce_user_auth(&user) {
                    Ok(()) => Ok(principal),
                    Err(error) => Err(MissingControlAuthError::PolicyInvalid { principal, error }),
                }
            }
            Err(error) if error.kind == AuthErrorKind::TokenExpired => {
                Err(MissingControlAuthError::TokenExpired)
            }
            Err(error) if error.kind == AuthErrorKind::SessionRevoked => {
                Err(MissingControlAuthError::SessionRevoked)
            }
            Err(error)
                if matches!(
                    error.kind,
                    AuthErrorKind::Unauthorized | AuthErrorKind::InvalidCredentials
                ) =>
            {
                Err(if internal {
                    MissingControlAuthError::Unauthorized
                } else {
                    MissingControlAuthError::UnmatchedOpaque
                })
            }
            Err(error) if error.kind == AuthErrorKind::UserDisabled => Err(
                MissingControlAuthError::UserAuth(UserAuthPolicyError::UserDisabled),
            ),
            Err(_) => Err(MissingControlAuthError::Unavailable),
        }
    }

    async fn browser_session_principal(
        &self,
        headers: &HeaderMap,
    ) -> Result<Option<MissingControlPrincipal>, MissingControlAuthError> {
        let token = dashboard_credential(headers).ok_or(MissingControlAuthError::Unauthorized)?;
        let principal = self.principal(headers).await?;
        match self.auth.current_session(SecretString::from(token)).await {
            Ok(_) => Ok(Some(principal)),
            Err(error)
                if matches!(
                    error.kind,
                    AuthErrorKind::Unauthorized | AuthErrorKind::InvalidCredentials
                ) =>
            {
                Ok(None)
            }
            Err(error) if error.kind == AuthErrorKind::TokenExpired => {
                Err(MissingControlAuthError::TokenExpired)
            }
            Err(error) if error.kind == AuthErrorKind::SessionRevoked => {
                Err(MissingControlAuthError::SessionRevoked)
            }
            Err(_) => Err(MissingControlAuthError::Unavailable),
        }
    }
}

/// Normalized access configuration of a legacy `HeaderNavModules` entry.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct HeaderNavAccess {
    pub enabled: bool,
    pub require_auth: bool,
}

/// Parses the JSON value stored by Go's `HeaderNavModules` option.
///
/// The option predates Rust and has intentionally remained permissive: module
/// values can be booleans, numeric/string flags, or objects using the camelCase
/// `requireAuth` field. Unknown/malformed values use Go's public fallback.
#[must_use]
pub fn parse_header_nav_access(raw: Option<&Value>) -> HeaderNavAccess {
    let fallback = HeaderNavAccess::default();
    match raw {
        Some(Value::Bool(enabled)) => HeaderNavAccess {
            enabled: *enabled,
            ..fallback
        },
        Some(Value::String(enabled)) => HeaderNavAccess {
            enabled: parse_header_nav_bool(enabled, fallback.enabled),
            ..fallback
        },
        Some(Value::Number(enabled)) => HeaderNavAccess {
            enabled: enabled.as_f64().map_or(fallback.enabled, |value| {
                parse_header_nav_number(value, fallback.enabled)
            }),
            ..fallback
        },
        Some(Value::Object(value)) => HeaderNavAccess {
            enabled: value.get("enabled").map_or(fallback.enabled, |value| {
                parse_header_nav_value(value, fallback.enabled)
            }),
            require_auth: value
                .get("requireAuth")
                .map_or(fallback.require_auth, |value| {
                    parse_header_nav_value(value, fallback.require_auth)
                }),
        },
        _ => fallback,
    }
}

fn parse_header_nav_value(value: &Value, fallback: bool) -> bool {
    match value {
        Value::Bool(value) => *value,
        Value::String(value) => parse_header_nav_bool(value, fallback),
        Value::Number(value) => value
            .as_f64()
            .map_or(fallback, |value| parse_header_nav_number(value, fallback)),
        _ => fallback,
    }
}

fn parse_header_nav_bool(value: &str, fallback: bool) -> bool {
    match value.trim().to_ascii_lowercase().as_str() {
        "true" | "1" => true,
        "false" | "0" => false,
        _ => fallback,
    }
}

fn parse_header_nav_number(value: f64, fallback: bool) -> bool {
    if value == 1.0 {
        true
    } else if value == 0.0 {
        false
    } else {
        fallback
    }
}

impl Default for HeaderNavAccess {
    fn default() -> Self {
        Self {
            enabled: true,
            require_auth: false,
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct MissingControlStoreError(pub String);

impl MissingControlStoreError {
    #[must_use]
    pub fn new(message: impl Into<String>) -> Self {
        Self(message.into())
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct MissingControlToken {
    pub user_id: i64,
    pub status: i64,
    pub user_status: i64,
    pub saved_language: Option<String>,
}

impl Default for MissingControlToken {
    fn default() -> Self {
        Self {
            user_id: 0,
            status: 1,
            user_status: 1,
            saved_language: None,
        }
    }
}

/// Durable source for the seven legacy endpoints in this slice.
///
/// Values are JSON on purpose: the Go endpoints expose cache-backed dynamic
/// shapes (not stable structs), and preserving them avoids silently dropping
/// fields during the staged migration.
#[async_trait]
pub trait MissingControlStore: Send + Sync {
    async fn header_nav(&self, module: &str) -> Result<HeaderNavAccess, MissingControlStoreError>;
    async fn groups(&self) -> Result<Vec<String>, MissingControlStoreError>;
    async fn dashboard_models(&self) -> Value;
    async fn pricing(
        &self,
        actor: Option<MissingControlPrincipal>,
    ) -> Result<Value, MissingControlStoreError>;
    async fn rankings(&self, period: &str) -> Result<Value, MissingControlStoreError>;
    async fn exposed_ratio(&self) -> Result<Option<Value>, MissingControlStoreError>;
    async fn token_usage(&self, key: &str) -> Result<Option<Value>, MissingControlStoreError>;

    async fn token_usage_for_owner(
        &self,
        key: &str,
        _: i64,
    ) -> Result<Option<Value>, MissingControlStoreError> {
        self.token_usage(key).await
    }

    /// The first lookup performed by Go's `TokenAuthReadOnly` middleware.
    ///
    /// The default keeps existing durable adapters source-compatible while
    /// allowing stores with token metadata to reproduce disabled/banned
    /// checks exactly.
    async fn token_auth_read_only(
        &self,
        key: &str,
    ) -> Result<Option<MissingControlToken>, MissingControlStoreError> {
        Ok(self
            .token_usage(key)
            .await?
            .map(|_| MissingControlToken::default()))
    }
}

#[async_trait]
pub trait MissingControlRateLimiter: Send + Sync {
    async fn check(
        &self,
        client_ip: &str,
    ) -> Result<CriticalRateLimitOutcome, MissingControlStoreError>;
}

#[derive(Clone)]
pub struct DashboardMissingControlRateLimiter {
    auth: Arc<dyn DashboardAuth>,
}

impl DashboardMissingControlRateLimiter {
    #[must_use]
    pub fn new(auth: Arc<dyn DashboardAuth>) -> Self {
        Self { auth }
    }
}

#[async_trait]
impl MissingControlRateLimiter for DashboardMissingControlRateLimiter {
    async fn check(
        &self,
        client_ip: &str,
    ) -> Result<CriticalRateLimitOutcome, MissingControlStoreError> {
        self.auth
            .check_critical_rate_limit(client_ip)
            .await
            .map_err(|error| MissingControlStoreError::new(error.to_string()))
    }
}

struct AllowMissingControlRateLimiter;

#[async_trait]
impl MissingControlRateLimiter for AllowMissingControlRateLimiter {
    async fn check(&self, _: &str) -> Result<CriticalRateLimitOutcome, MissingControlStoreError> {
        Ok(CriticalRateLimitOutcome::Allowed)
    }
}

#[derive(Clone, Default)]
struct MissingControlLastGood {
    nav: BTreeMap<String, HeaderNavAccess>,
    groups: Vec<String>,
    pricing: BTreeMap<Option<i64>, Value>,
    ratio: Option<Value>,
}

/// State for [`missing_control_public_router`].
#[derive(Clone)]
pub struct MissingControlPublicState {
    store: Arc<dyn MissingControlStore>,
    authorizer: Arc<dyn MissingControlAuthorizer>,
    limiter: Arc<dyn MissingControlRateLimiter>,
    console_access_auth: Option<Arc<dyn DashboardAuth>>,
    last_good: Arc<RwLock<MissingControlLastGood>>,
}

impl MissingControlPublicState {
    #[must_use]
    pub fn new(
        store: Arc<dyn MissingControlStore>,
        authorizer: Arc<dyn MissingControlAuthorizer>,
    ) -> Self {
        Self {
            store,
            authorizer,
            limiter: Arc::new(AllowMissingControlRateLimiter),
            console_access_auth: None,
            last_good: Arc::new(RwLock::new(MissingControlLastGood::default())),
        }
    }

    #[must_use]
    pub fn with_critical_rate_limiter(
        mut self,
        limiter: Arc<dyn MissingControlRateLimiter>,
    ) -> Self {
        self.limiter = limiter;
        self
    }

    /// Enables the API-wide Go ConsoleAccessGate for the normal listener.
    /// Candidate/module tests leave it disabled so they can exercise the
    /// route-local optional-auth behavior without a second boundary.
    #[must_use]
    pub fn with_console_access_gate(mut self, auth: Arc<dyn DashboardAuth>) -> Self {
        self.console_access_auth = Some(auth);
        self
    }

    async fn header_nav(&self, module: &str) -> HeaderNavAccess {
        match self.store.header_nav(module).await {
            Ok(access) => {
                self.write_last_good().nav.insert(module.to_owned(), access);
                access
            }
            Err(_) => self
                .read_last_good()
                .nav
                .get(module)
                .copied()
                .unwrap_or_default(),
        }
    }

    async fn groups(&self) -> Vec<String> {
        match self.store.groups().await {
            Ok(groups) => {
                self.write_last_good().groups = groups.clone();
                groups
            }
            Err(_) => self.read_last_good().groups.clone(),
        }
    }

    async fn pricing(&self, actor: Option<MissingControlPrincipal>) -> Value {
        let cache_key = actor.map(|principal| principal.user_id);
        match self.store.pricing(actor).await {
            Ok(pricing) => {
                self.write_last_good()
                    .pricing
                    .insert(cache_key, pricing.clone());
                pricing
            }
            Err(_) => self
                .read_last_good()
                .pricing
                .get(&cache_key)
                .cloned()
                .unwrap_or_else(empty_pricing_snapshot),
        }
    }

    async fn ratio(&self) -> Option<Value> {
        match self.store.exposed_ratio().await {
            Ok(ratio) => {
                self.write_last_good().ratio = ratio.clone();
                ratio
            }
            Err(_) => self.read_last_good().ratio.clone(),
        }
    }

    fn read_last_good(&self) -> std::sync::RwLockReadGuard<'_, MissingControlLastGood> {
        self.last_good
            .read()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
    }

    fn write_last_good(&self) -> std::sync::RwLockWriteGuard<'_, MissingControlLastGood> {
        self.last_good
            .write()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
    }
}

/// Minimal state for the public ratio configuration endpoint.
///
/// The broader [`MissingControlPublicState`] remains isolated on the
/// candidate surface because its other routes carry additional control-plane
/// semantics.  The ratio read itself is a bounded PostgreSQL option lookup,
/// so the normal listener can mount this one route without exposing those
/// unrelated candidate paths.
#[derive(Clone)]
pub struct RatioConfigState {
    pg: PgPool,
    limiter: Arc<dyn MissingControlRateLimiter>,
}

impl RatioConfigState {
    #[must_use]
    pub fn new(pg: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self {
            pg,
            limiter: Arc::new(DashboardMissingControlRateLimiter::new(auth)),
        }
    }
}

/// Minimal state for the administrator-only group catalogue endpoint.
///
/// The legacy handler enumerates the keys of the process-wide `GroupRatio`
/// map.  Reading that option directly keeps this normal-listener mount narrow:
/// it does not expose the broader pricing or model-control candidate surface.
#[derive(Clone)]
pub struct GroupState {
    pg: PgPool,
    authorizer: Arc<dyn MissingControlAuthorizer>,
}

impl GroupState {
    #[must_use]
    pub fn new(pg: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self {
            pg,
            authorizer: Arc::new(DashboardMissingControlAuthorizer::new(auth)),
        }
    }
}

/// Mounts only `GET /api/group/` for the normal listener.
pub fn group_router(state: GroupState) -> Router {
    Router::new()
        .route(GROUP_PATH, get(groups_direct))
        .with_state(state)
}

/// Mounts only `GET /api/ratio_config` for the normal listener.
pub fn ratio_config_router(state: RatioConfigState) -> Router {
    Router::new()
        .route(RATIO_CONFIG_PATH, get(ratio_config_direct))
        .route_layer(middleware::from_fn_with_state(
            state.clone(),
            ratio_config_critical_rate_limit,
        ))
        .with_state(state)
}

/// Mount point used by the migration root.
pub fn missing_control_public_router(state: MissingControlPublicState) -> Router {
    let router = Router::new()
        .route("/api/assistant/pricing", get(assistant_pricing))
        .route("/api/group/", get(groups))
        .route("/api/models", get(models))
        .route("/api/pricing", get(pricing))
        .route("/api/rankings", get(rankings))
        .route(
            "/api/ratio_config",
            get(ratio_config).route_layer(middleware::from_fn_with_state(
                state.clone(),
                critical_rate_limit,
            )),
        )
        .route(
            "/api/usage/token/",
            get(token_usage_get).fallback(token_usage_method_fallback),
        )
        .with_state(state.clone());
    if state.console_access_auth.is_some() {
        router.layer(middleware::from_fn_with_state(
            state,
            console_access_boundary,
        ))
    } else {
        router
    }
}

async fn console_access_boundary(
    State(state): State<MissingControlPublicState>,
    request: Request,
    next: Next,
) -> Response {
    if !console_discovery_route(request.uri().path()) {
        return next.run(request).await;
    }
    let Some(auth) = state.console_access_auth.as_ref() else {
        return next.run(request).await;
    };
    let Some(token) = dashboard_credential(request.headers()) else {
        return console_not_found();
    };
    let user = match auth
        .self_user_view_for_optional(SecretString::from(token))
        .await
    {
        Ok(user) => user,
        Err(_) => return console_not_found(),
    };
    if !user.developer_access_granted {
        return console_not_found();
    }
    next.run(request).await
}

fn console_discovery_route(path: &str) -> bool {
    [
        "/api/assistant/pricing",
        "/api/group",
        "/api/models",
        "/api/pricing",
        "/api/rankings",
        "/api/ratio_config",
        "/api/usage",
    ]
    .iter()
    .any(|prefix| {
        path == *prefix
            || path
                .strip_prefix(prefix)
                .is_some_and(|suffix| suffix.starts_with('/'))
    })
}

fn console_not_found() -> Response {
    (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response()
}

async fn assistant_pricing(
    State(state): State<MissingControlPublicState>,
    headers: HeaderMap,
) -> Response {
    let principal = match require_assistant_dashboard(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    with_auth_version(legacy_json(state.pricing(Some(principal)).await))
}

async fn groups(State(state): State<MissingControlPublicState>, headers: HeaderMap) -> Response {
    let principal = match require_public_dashboard(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    if principal.role < ADMIN_ROLE {
        return public_user_auth_error(&headers, UserAuthPolicyError::InsufficientPrivilege);
    }
    with_auth_version(public_success(json!(state.groups().await)))
}

async fn groups_direct(State(state): State<GroupState>, headers: HeaderMap) -> Response {
    let principal =
        match require_public_dashboard_authorizer(state.authorizer.as_ref(), &headers).await {
            Ok(principal) => principal,
            Err(response) => return response,
        };
    if principal.role < ADMIN_ROLE {
        return public_user_auth_error(&headers, UserAuthPolicyError::InsufficientPrivilege);
    }

    // Go's `GetGroups` returns the keys of GroupRatio. Invalid or absent
    // persisted JSON leaves the legacy cache at its empty initial value in the
    // isolated fixture; preserve that fail-closed read shape here.
    let groups =
        sqlx::query_scalar::<_, String>("SELECT value FROM options WHERE key = 'GroupRatio'")
            .fetch_optional(&state.pg)
            .await
            .ok()
            .flatten()
            .and_then(|value| serde_json::from_str::<Map<String, Value>>(&value).ok())
            .map(|values| {
                values
                    .into_iter()
                    .map(|(group, _)| group)
                    .collect::<Vec<_>>()
            })
            .unwrap_or_default();
    with_auth_version(public_success(json!(groups)))
}

async fn models(State(state): State<MissingControlPublicState>, headers: HeaderMap) -> Response {
    if let Err(response) = require_dashboard(&state, &headers).await {
        return response;
    }
    with_auth_version(legacy_json(json!({
        "success": true,
        "data": state.store.dashboard_models().await,
    })))
}

async fn pricing(State(state): State<MissingControlPublicState>, headers: HeaderMap) -> Response {
    let access = state.header_nav("pricing").await;
    let actor = match nav_actor(&state, &headers, access, "pricing").await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let response = legacy_json(state.pricing(actor).await);
    if actor.is_some() {
        with_auth_version(response)
    } else {
        response
    }
}

async fn rankings(
    State(state): State<MissingControlPublicState>,
    headers: HeaderMap,
    RawQuery(raw_query): RawQuery,
) -> Response {
    let access = state.header_nav("rankings").await;
    let actor = match nav_actor(&state, &headers, access, "rankings").await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let period = match parse_ranking_period(raw_query.as_deref()) {
        Ok(period) => period,
        Err(()) => {
            let response = public_failure(StatusCode::BAD_REQUEST, "invalid ranking period");
            return if actor.is_some() {
                with_auth_version(response)
            } else {
                response
            };
        }
    };
    if !matches!(period.as_str(), "today" | "week" | "month" | "year") {
        let response = public_failure(
            StatusCode::BAD_REQUEST,
            &format!("invalid ranking period: {period}"),
        );
        return if actor.is_some() {
            with_auth_version(response)
        } else {
            response
        };
    }
    let response = match state.store.rankings(&period).await {
        Ok(data) => legacy_json(json!({"success": true, "data": data})),
        Err(MissingControlStoreError(message)) => public_failure(StatusCode::BAD_REQUEST, &message),
    };
    if actor.is_some() {
        with_auth_version(response)
    } else {
        response
    }
}

async fn ratio_config(State(state): State<MissingControlPublicState>) -> Response {
    match state.ratio().await {
        Some(data) => legacy_json(json!({"success": true, "message": "", "data": data})),
        None => public_failure(StatusCode::FORBIDDEN, "倍率配置接口未启用"),
    }
}

async fn ratio_config_direct(State(state): State<RatioConfigState>) -> Response {
    let enabled = sqlx::query_scalar::<_, String>(
        "SELECT value FROM options WHERE key = 'ExposeRatioEnabled'",
    )
    .fetch_optional(&state.pg)
    .await
    .ok()
    .flatten()
    .is_some_and(|value| value == "true");
    if !enabled {
        return public_failure(StatusCode::FORBIDDEN, "倍率配置接口未启用");
    }

    let rows = match sqlx::query_as::<_, (String, String)>(
        "SELECT key, value FROM options WHERE key = ANY($1)",
    )
    .bind(vec![
        "ModelRatio",
        "CompletionRatio",
        "CacheRatio",
        "CreateCacheRatio",
        "ModelPrice",
    ])
    .fetch_all(&state.pg)
    .await
    {
        Ok(rows) => rows,
        Err(_) => return public_failure(StatusCode::FORBIDDEN, "倍率配置接口未启用"),
    };
    let values = rows
        .into_iter()
        .filter_map(|(key, value)| {
            serde_json::from_str::<BTreeMap<String, f64>>(&value)
                .ok()
                .map(|value| (key, go_ratio_map_json(value)))
        })
        .collect::<BTreeMap<String, Value>>();
    let data = json!({
        "model_ratio": values.get("ModelRatio").cloned().unwrap_or_else(|| json!({})),
        "completion_ratio": values.get("CompletionRatio").cloned().unwrap_or_else(|| json!({})),
        "cache_ratio": values.get("CacheRatio").cloned().unwrap_or_else(|| json!({})),
        "create_cache_ratio": values.get("CreateCacheRatio").cloned().unwrap_or_else(|| json!({})),
        "model_price": values.get("ModelPrice").cloned().unwrap_or_else(|| json!({})),
    });
    legacy_json(json!({"success": true, "message": "", "data": data}))
}

fn go_ratio_map_json(values: BTreeMap<String, f64>) -> Value {
    let values = values
        .into_iter()
        .filter_map(|(key, value)| {
            let number = if value.is_finite()
                && value.fract() == 0.0
                && value >= i64::MIN as f64
                && value <= i64::MAX as f64
            {
                Number::from(value as i64)
            } else {
                Number::from_f64(value)?
            };
            Some((key, Value::Number(number)))
        })
        .collect::<Map<String, Value>>();
    Value::Object(values)
}

async fn token_usage_get(
    State(state): State<MissingControlPublicState>,
    request: Request,
) -> Response {
    let client_ip = request
        .extensions()
        .get::<ClientIpKey>()
        .map(|key| key.0.clone())
        .or_else(|| {
            request
                .extensions()
                .get::<RequestContext>()
                .and_then(|context| context.client_ip)
                .map(|ip| ip.to_string())
        })
        .unwrap_or_else(|| "unknown".to_owned());
    let mut response = match critical_rate_limit_response(&state, &client_ip).await {
        Some(response) => response,
        None => token_usage(&state, request.headers()).await,
    };
    if is_cross_origin(request.headers()) {
        response = token_usage_cors_response(response, false);
    }
    response
}

async fn token_usage_method_fallback(request: Request) -> Response {
    if request.method() == axum::http::Method::OPTIONS && is_cross_origin(request.headers()) {
        token_usage_cors_response(StatusCode::NO_CONTENT.into_response(), true)
    } else {
        relay_not_found_response(&request)
    }
}

fn relay_not_found_response(request: &Request) -> Response {
    legacy_json_with_status(
        StatusCode::NOT_FOUND,
        json!({
            "error": {
                "message": format!(
                    "Invalid URL ({} {})",
                    request.method(),
                    request.uri().path()
                ),
                "type": "invalid_request_error",
                "param": "",
                "code": "",
            }
        }),
    )
}

fn token_usage_cors_response(mut response: Response, is_preflight: bool) -> Response {
    {
        response.headers_mut().insert(
            header::HeaderName::from_static("access-control-allow-origin"),
            HeaderValue::from_static("*"),
        );
        response.headers_mut().insert(
            header::HeaderName::from_static("access-control-allow-credentials"),
            HeaderValue::from_static("true"),
        );
        if is_preflight {
            response.headers_mut().insert(
                header::HeaderName::from_static("access-control-allow-methods"),
                HeaderValue::from_static("GET,POST,PUT,DELETE,OPTIONS"),
            );
            response.headers_mut().insert(
                header::HeaderName::from_static("access-control-allow-headers"),
                HeaderValue::from_static("*"),
            );
            response.headers_mut().insert(
                header::HeaderName::from_static("access-control-max-age"),
                HeaderValue::from_static("43200"),
            );
        }
    }
    response
}

async fn critical_rate_limit_response(
    state: &MissingControlPublicState,
    client_ip: &str,
) -> Option<Response> {
    match state.limiter.check(client_ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => None,
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => Some(empty_limiter_response(
            StatusCode::TOO_MANY_REQUESTS,
            Some(retry_after_seconds),
        )),
        Err(_) => Some(empty_limiter_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            None,
        )),
    }
}

async fn critical_rate_limit(
    State(state): State<MissingControlPublicState>,
    request: Request,
    next: Next,
) -> Response {
    let client_ip = request
        .extensions()
        .get::<ClientIpKey>()
        .map(|key| key.0.clone())
        .or_else(|| {
            request
                .extensions()
                .get::<RequestContext>()
                .and_then(|context| context.client_ip)
                .map(|ip| ip.to_string())
        })
        .unwrap_or_else(|| "unknown".to_owned());
    match critical_rate_limit_response(&state, &client_ip).await {
        Some(response) => response,
        None => next.run(request).await,
    }
}

async fn ratio_config_critical_rate_limit(
    State(state): State<RatioConfigState>,
    request: Request,
    next: Next,
) -> Response {
    let client_ip = request
        .extensions()
        .get::<ClientIpKey>()
        .map(|key| key.0.clone())
        .or_else(|| {
            request
                .extensions()
                .get::<RequestContext>()
                .and_then(|context| context.client_ip)
                .map(|ip| ip.to_string())
        })
        .unwrap_or_else(|| "unknown".to_owned());
    match state.limiter.check(&client_ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => next.run(request).await,
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => empty_limiter_response(StatusCode::TOO_MANY_REQUESTS, Some(retry_after_seconds)),
        Err(_) => empty_limiter_response(StatusCode::INTERNAL_SERVER_ERROR, None),
    }
}

fn parse_ranking_period(raw_query: Option<&str>) -> Result<String, ()> {
    let Some(raw_query) = raw_query else {
        return Ok("week".to_owned());
    };
    let mut first_period = None;
    for pair in raw_query.split('&') {
        if pair.contains(';') {
            continue;
        }
        let (raw_key, raw_value) = pair.split_once('=').unwrap_or((pair, ""));
        let (Ok(key), Ok(value)) = (
            percent_decode_query(raw_key),
            percent_decode_query(raw_value),
        ) else {
            continue;
        };
        if key == b"period" && first_period.is_none() {
            first_period = Some(value);
        }
    }
    let Some(period) = first_period else {
        return Ok("week".to_owned());
    };
    if period.is_empty() {
        return Ok("week".to_owned());
    }
    String::from_utf8(period).map_err(|_| ())
}

fn is_cross_origin(headers: &HeaderMap) -> bool {
    let Some(origin) = headers
        .get(header::ORIGIN)
        .and_then(|value| value.to_str().ok())
    else {
        return false;
    };
    let same_origin = headers
        .get(header::HOST)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|host| {
            origin == format!("http://{host}") || origin == format!("https://{host}")
        });
    !same_origin
}

fn percent_decode_query(raw: &str) -> Result<Vec<u8>, String> {
    let mut bytes = Vec::with_capacity(raw.len());
    let raw = raw.as_bytes();
    let mut index = 0;
    while index < raw.len() {
        match raw[index] {
            b'+' => bytes.push(b' '),
            b'%' if index + 2 < raw.len() => {
                let Some(high) = hex_nibble(raw[index + 1]) else {
                    return Err(format!("invalid URL escape at byte {index}"));
                };
                let Some(low) = hex_nibble(raw[index + 2]) else {
                    return Err(format!("invalid URL escape at byte {index}"));
                };
                bytes.push(high << 4 | low);
                index += 2;
            }
            b'%' => return Err(format!("invalid URL escape at byte {index}")),
            byte => bytes.push(byte),
        }
        index += 1;
    }
    Ok(bytes)
}

fn hex_nibble(byte: u8) -> Option<u8> {
    match byte {
        b'0'..=b'9' => Some(byte - b'0'),
        b'a'..=b'f' => Some(byte - b'a' + 10),
        b'A'..=b'F' => Some(byte - b'A' + 10),
        _ => None,
    }
}

fn empty_limiter_response(status: StatusCode, retry_after_seconds: Option<u64>) -> Response {
    legacy_empty_response(status, retry_after_seconds)
}

async fn token_usage(state: &MissingControlPublicState, headers: &HeaderMap) -> Response {
    let Some(raw) = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
    else {
        return token_auth_failure(
            headers,
            StatusCode::UNAUTHORIZED,
            TokenAuthMessage::NotProvided,
        );
    };

    if raw.is_empty() {
        return token_auth_failure(
            headers,
            StatusCode::UNAUTHORIZED,
            TokenAuthMessage::NotProvided,
        );
    }

    let middleware_key = token_auth_middleware_key(raw);
    let token = match state.store.token_auth_read_only(&middleware_key).await {
        Ok(Some(token)) => token,
        Ok(None) => {
            return token_auth_failure(
                headers,
                StatusCode::UNAUTHORIZED,
                TokenAuthMessage::Invalid,
            );
        }
        Err(_) => {
            return token_auth_failure(
                headers,
                StatusCode::INTERNAL_SERVER_ERROR,
                TokenAuthMessage::Database,
            );
        }
    };
    if token.status == 2 {
        return token_auth_failure(
            headers,
            StatusCode::UNAUTHORIZED,
            TokenAuthMessage::StatusUnavailable,
        );
    }
    if token.user_status != 1 {
        return token_auth_failure(headers, StatusCode::FORBIDDEN, TokenAuthMessage::UserBanned);
    }

    let mut parts = raw.split(' ');
    let (Some(scheme), Some(key), None) = (parts.next(), parts.next(), parts.next()) else {
        return token_auth_failure(
            headers,
            StatusCode::UNAUTHORIZED,
            TokenAuthMessage::InvalidBearer,
        );
    };
    if !scheme.eq_ignore_ascii_case("bearer") {
        return token_auth_failure(
            headers,
            StatusCode::UNAUTHORIZED,
            TokenAuthMessage::InvalidBearer,
        );
    }
    let key = key.strip_prefix("sk-").unwrap_or(key);
    match state.store.token_usage_for_owner(key, token.user_id).await {
        Ok(Some(data)) => legacy_json(json!({"code": true, "message": "ok", "data": data})),
        Ok(None) | Err(_) => token_usage_failure(headers, token.saved_language.as_deref()),
    }
}

fn token_auth_middleware_key(raw: &str) -> String {
    let key = if raw.starts_with("Bearer ") || raw.starts_with("bearer ") {
        raw[7..].trim()
    } else {
        raw
    };
    let key = key.strip_prefix("sk-").unwrap_or(key);
    key.split('-').next().unwrap_or_default().to_owned()
}

#[derive(Clone, Copy)]
enum TokenAuthMessage {
    NotProvided,
    Invalid,
    StatusUnavailable,
    UserBanned,
    Database,
    InvalidBearer,
    GetInfoFailed,
}

fn token_auth_failure(
    headers: &HeaderMap,
    status: StatusCode,
    message: TokenAuthMessage,
) -> Response {
    public_failure(status, token_auth_message(message, token_locale(headers)))
}

fn token_usage_failure(headers: &HeaderMap, saved_language: Option<&str>) -> Response {
    public_failure(
        StatusCode::OK,
        token_auth_message(
            TokenAuthMessage::GetInfoFailed,
            saved_language.map_or_else(|| token_locale(headers), token_locale_value),
        ),
    )
}

fn token_locale(headers: &HeaderMap) -> TokenLocale {
    headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.split(',').next())
        .and_then(|value| value.split(';').next())
        .map(str::trim)
        .map_or(TokenLocale::En, token_locale_value)
}

fn token_locale_value(language: &str) -> TokenLocale {
    let language = language.trim().to_ascii_lowercase();
    if language.starts_with("zh-tw") {
        TokenLocale::ZhTw
    } else if language.starts_with("zh") {
        TokenLocale::ZhCn
    } else {
        TokenLocale::En
    }
}

fn empty_pricing_snapshot() -> Value {
    json!({
        "success": true,
        "data": [],
        "vendors": [],
        "group_ratio": {},
        "usable_group": {},
        "supported_endpoint": {},
        "auto_groups": ["default"],
        "pricing_version": "a42d372ccf0b5dd13ecf71203521f9d2",
    })
}

#[derive(Clone, Copy)]
enum TokenLocale {
    En,
    ZhCn,
    ZhTw,
}

fn token_auth_message(message: TokenAuthMessage, locale: TokenLocale) -> &'static str {
    match (locale, message) {
        (TokenLocale::En, TokenAuthMessage::NotProvided) => "Token not provided",
        (TokenLocale::ZhCn, TokenAuthMessage::NotProvided)
        | (TokenLocale::ZhTw, TokenAuthMessage::NotProvided) => "未提供令牌",
        (TokenLocale::En, TokenAuthMessage::Invalid) => "Invalid token",
        (TokenLocale::ZhCn, TokenAuthMessage::Invalid) => "无效的令牌",
        (TokenLocale::ZhTw, TokenAuthMessage::Invalid) => "無效的令牌",
        (TokenLocale::En, TokenAuthMessage::StatusUnavailable) => {
            "This token status is unavailable"
        }
        (TokenLocale::ZhCn, TokenAuthMessage::StatusUnavailable) => "该令牌状态不可用",
        (TokenLocale::ZhTw, TokenAuthMessage::StatusUnavailable) => "該令牌狀態不可用",
        (TokenLocale::En, TokenAuthMessage::UserBanned) => "User has been banned",
        (TokenLocale::ZhCn, TokenAuthMessage::UserBanned) => "用户已被封禁",
        (TokenLocale::ZhTw, TokenAuthMessage::UserBanned) => "使用者已被封禁",
        (TokenLocale::En, TokenAuthMessage::Database) => {
            "Database error, please contact the administrator"
        }
        (TokenLocale::ZhCn, TokenAuthMessage::Database) => "数据库出错，请联系管理员",
        (TokenLocale::ZhTw, TokenAuthMessage::Database) => "資料庫出錯，請聯繫管理員",
        (_, TokenAuthMessage::InvalidBearer) => "Invalid Bearer token",
        (TokenLocale::En, TokenAuthMessage::GetInfoFailed) => {
            "Failed to get token info, please try again later"
        }
        (TokenLocale::ZhCn, TokenAuthMessage::GetInfoFailed) => "获取令牌信息失败，请稍后重试",
        (TokenLocale::ZhTw, TokenAuthMessage::GetInfoFailed) => "獲取令牌資訊失敗，請稍後重試",
    }
}

async fn nav_actor(
    state: &MissingControlPublicState,
    headers: &HeaderMap,
    access: HeaderNavAccess,
    module: &str,
) -> Result<Option<MissingControlPrincipal>, Response> {
    if !access.enabled {
        return Err(public_failure(
            StatusCode::FORBIDDEN,
            &format!("{module} is disabled"),
        ));
    }
    if !dashboard_credential_present(headers) {
        return if access.require_auth {
            Err(dashboard_unauthorized(headers))
        } else {
            Ok(None)
        };
    }
    let authorization = if access.require_auth {
        state.authorizer.principal(headers).await
    } else {
        state.authorizer.optional_principal(headers).await
    };
    match authorization {
        Ok(principal) => Ok(Some(principal)),
        Err(MissingControlAuthError::UnmatchedOpaque) if !access.require_auth => Ok(None),
        Err(MissingControlAuthError::UnmatchedOpaque | MissingControlAuthError::Unauthorized) => {
            Err(public_dashboard_unauthorized(headers))
        }
        Err(MissingControlAuthError::TokenExpired) => {
            Err(dashboard_auth_failure(headers, AuthErrorKind::TokenExpired))
        }
        Err(MissingControlAuthError::SessionRevoked) => Err(dashboard_auth_failure(
            headers,
            AuthErrorKind::SessionRevoked,
        )),
        Err(MissingControlAuthError::Unavailable) => Err(dashboard_internal_error(headers)),
        Err(MissingControlAuthError::UserAuth(error)) => {
            Err(public_user_auth_error(headers, error))
        }
        Err(MissingControlAuthError::PolicyInvalid { principal, .. }) if !access.require_auth => {
            Ok(Some(principal))
        }
        Err(MissingControlAuthError::PolicyInvalid { error, .. }) => {
            Err(public_user_auth_error(headers, error))
        }
    }
}

async fn require_public_dashboard(
    state: &MissingControlPublicState,
    headers: &HeaderMap,
) -> Result<MissingControlPrincipal, Response> {
    require_public_dashboard_authorizer(state.authorizer.as_ref(), headers).await
}

async fn require_public_dashboard_authorizer(
    authorizer: &dyn MissingControlAuthorizer,
    headers: &HeaderMap,
) -> Result<MissingControlPrincipal, Response> {
    match authorizer.principal(headers).await {
        Ok(principal) => Ok(principal),
        Err(MissingControlAuthError::UnmatchedOpaque | MissingControlAuthError::Unauthorized) => {
            Err(public_dashboard_unauthorized(headers))
        }
        Err(MissingControlAuthError::TokenExpired) => {
            Err(dashboard_auth_failure(headers, AuthErrorKind::TokenExpired))
        }
        Err(MissingControlAuthError::SessionRevoked) => Err(dashboard_auth_failure(
            headers,
            AuthErrorKind::SessionRevoked,
        )),
        Err(MissingControlAuthError::Unavailable) => Err(dashboard_internal_error(headers)),
        Err(MissingControlAuthError::UserAuth(error)) => {
            Err(public_user_auth_error(headers, error))
        }
        Err(MissingControlAuthError::PolicyInvalid { .. }) => {
            unreachable!("required dashboard auth must enforce policy")
        }
    }
}

async fn require_dashboard(
    state: &MissingControlPublicState,
    headers: &HeaderMap,
) -> Result<MissingControlPrincipal, Response> {
    match state.authorizer.principal(headers).await {
        Ok(principal) => Ok(principal),
        Err(MissingControlAuthError::UnmatchedOpaque | MissingControlAuthError::Unauthorized) => {
            Err(dashboard_unauthorized(headers))
        }
        Err(MissingControlAuthError::TokenExpired) => {
            Err(dashboard_auth_failure(headers, AuthErrorKind::TokenExpired))
        }
        Err(MissingControlAuthError::SessionRevoked) => Err(dashboard_auth_failure(
            headers,
            AuthErrorKind::SessionRevoked,
        )),
        Err(MissingControlAuthError::Unavailable) => Err(dashboard_internal_error(headers)),
        Err(MissingControlAuthError::UserAuth(error)) => Err(user_auth_error(headers, error)),
        Err(MissingControlAuthError::PolicyInvalid { error, .. }) => {
            Err(user_auth_error(headers, error))
        }
    }
}

async fn require_assistant_dashboard(
    state: &MissingControlPublicState,
    headers: &HeaderMap,
) -> Result<MissingControlPrincipal, Response> {
    match state.authorizer.browser_session_principal(headers).await {
        Ok(Some(principal)) => Ok(principal),
        Ok(None) => Err(assistant_session_required()),
        Err(MissingControlAuthError::UnmatchedOpaque | MissingControlAuthError::Unauthorized) => {
            Err(with_auth_version(dashboard_unauthorized(headers)))
        }
        Err(MissingControlAuthError::TokenExpired) => Err(with_auth_version(
            dashboard_auth_failure(headers, AuthErrorKind::TokenExpired),
        )),
        Err(MissingControlAuthError::SessionRevoked) => Err(with_auth_version(
            dashboard_auth_failure(headers, AuthErrorKind::SessionRevoked),
        )),
        Err(MissingControlAuthError::Unavailable) => {
            Err(with_auth_version(dashboard_internal_error(headers)))
        }
        Err(MissingControlAuthError::UserAuth(error)) => {
            Err(with_auth_version(user_auth_error(headers, error)))
        }
        Err(MissingControlAuthError::PolicyInvalid { error, .. }) => {
            Err(with_auth_version(user_auth_error(headers, error)))
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
    legacy_json_with_status(
        status,
        json!({
            "success": false,
            "code": code,
            "message": user_auth_message(
                error,
                headers.get(axum::http::header::ACCEPT_LANGUAGE).and_then(|value| value.to_str().ok()),
            ),
        }),
    )
}

fn public_user_auth_error(headers: &HeaderMap, error: UserAuthPolicyError) -> Response {
    legacy_json_with_status(user_auth_status(error), user_auth_body(headers, error))
}

fn user_auth_status(error: UserAuthPolicyError) -> StatusCode {
    match error {
        UserAuthPolicyError::InsufficientPrivilege => StatusCode::FORBIDDEN,
        UserAuthPolicyError::UserDisabled | UserAuthPolicyError::InvalidUserInfo => {
            StatusCode::UNAUTHORIZED
        }
    }
}

fn user_auth_body(headers: &HeaderMap, error: UserAuthPolicyError) -> Value {
    let code = match error {
        UserAuthPolicyError::UserDisabled => "AUTH_USER_DISABLED",
        UserAuthPolicyError::InsufficientPrivilege => "AUTH_INSUFFICIENT_PRIVILEGE",
        UserAuthPolicyError::InvalidUserInfo => "AUTH_USER_INVALID",
    };
    json!({
        "success": false,
        "code": code,
        "message": user_auth_message(
            error,
            headers.get(header::ACCEPT_LANGUAGE).and_then(|value| value.to_str().ok()),
        ),
    })
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

fn dashboard_credential_present(headers: &HeaderMap) -> bool {
    dashboard_credential(headers).is_some()
}

fn dashboard_unauthorized(headers: &HeaderMap) -> Response {
    legacy_json_with_status(
        StatusCode::UNAUTHORIZED,
        json!({
            "success": false,
            "code": "AUTH_UNAUTHORIZED",
            "message": auth_invalid_access_token(headers),
        }),
    )
}

fn assistant_session_required() -> Response {
    with_auth_version(legacy_json_with_status(
        StatusCode::FORBIDDEN,
        json!({
            "success": false,
            "code": "ASSISTANT_SESSION_REQUIRED",
            "message": "assistant tools require a browser login session",
        }),
    ))
}

fn dashboard_auth_failure(headers: &HeaderMap, kind: AuthErrorKind) -> Response {
    let (code, message) = match kind {
        AuthErrorKind::TokenExpired => ("AUTH_TOKEN_EXPIRED", auth_not_logged_in(headers)),
        AuthErrorKind::SessionRevoked => ("AUTH_SESSION_REVOKED", auth_not_logged_in(headers)),
        _ => ("AUTH_UNAUTHORIZED", auth_invalid_access_token(headers)),
    };
    legacy_json_with_status(
        StatusCode::UNAUTHORIZED,
        json!({"success": false, "code": code, "message": message}),
    )
}

fn auth_not_logged_in(headers: &HeaderMap) -> &'static str {
    match token_locale(headers) {
        TokenLocale::En => "Unauthorized, not logged in and no access token provided",
        TokenLocale::ZhCn => "无权进行此操作，未登录且未提供 access token",
        TokenLocale::ZhTw => "無權進行此操作，未登入且未提供 access token",
    }
}

fn auth_invalid_access_token(headers: &HeaderMap) -> &'static str {
    match token_locale(headers) {
        TokenLocale::En => "Unauthorized, invalid access token",
        TokenLocale::ZhCn => "无权进行此操作，access token 无效",
        TokenLocale::ZhTw => "無權進行此操作，access token 無效",
    }
}

fn with_auth_version(mut response: Response) -> Response {
    response.headers_mut().insert(
        header::HeaderName::from_static("auth-version"),
        HeaderValue::from_static(AUTH_VERSION),
    );
    response
}

fn public_success(data: Value) -> Response {
    legacy_json(json!({"success": true, "message": "", "data": data}))
}

fn public_failure(status: StatusCode, message: &str) -> Response {
    legacy_json_with_status(status, json!({"success": false, "message": message}))
}

fn public_dashboard_unauthorized(headers: &HeaderMap) -> Response {
    legacy_json_with_status(
        StatusCode::UNAUTHORIZED,
        json!({
            "success": false,
            "code": "AUTH_UNAUTHORIZED",
            "message": auth_invalid_access_token(headers),
        }),
    )
}

fn dashboard_internal_error(headers: &HeaderMap) -> Response {
    let message = headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .map(str::to_ascii_lowercase)
        .filter(|value| value.starts_with("zh-tw"))
        .map_or_else(
            || {
                if headers
                    .get(header::ACCEPT_LANGUAGE)
                    .and_then(|value| value.to_str().ok())
                    .is_some_and(|value| value.to_ascii_lowercase().starts_with("zh"))
                {
                    "数据库出错，请联系管理员"
                } else {
                    "Database error, please contact the administrator"
                }
            },
            |_| "資料庫出錯，請聯繫管理員",
        );
    legacy_json_with_status(
        StatusCode::INTERNAL_SERVER_ERROR,
        json!({
            "success": false,
            "code": "AUTH_INTERNAL_ERROR",
            "message": message,
        }),
    )
}

fn legacy_json(value: Value) -> Response {
    legacy_json_with_status(StatusCode::OK, value)
}

fn legacy_json_with_status(status: StatusCode, value: Value) -> Response {
    let mut response = (status, Json(value)).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

/// Deterministic store for HTTP contract tests. Production must provide a
/// durable adapter; this type cannot call external services.
#[derive(Clone, Default)]
pub struct MemoryMissingControlStore {
    pub nav: BTreeMap<String, HeaderNavAccess>,
    pub group_names: Vec<String>,
    pub models: Value,
    pub pricing_data: Value,
    pub ranking_data: BTreeMap<String, Value>,
    pub ranking_error: Option<MissingControlStoreError>,
    pub ratio: Option<Value>,
    pub usages: BTreeMap<String, Value>,
    pub token_records: BTreeMap<String, MissingControlToken>,
    pub token_auth_error: Option<MissingControlStoreError>,
    pub token_usage_error: Option<MissingControlStoreError>,
}

#[async_trait]
impl MissingControlStore for MemoryMissingControlStore {
    async fn header_nav(&self, module: &str) -> Result<HeaderNavAccess, MissingControlStoreError> {
        Ok(self.nav.get(module).copied().unwrap_or_default())
    }
    async fn groups(&self) -> Result<Vec<String>, MissingControlStoreError> {
        Ok(self.group_names.clone())
    }
    async fn dashboard_models(&self) -> Value {
        self.models.clone()
    }
    async fn pricing(
        &self,
        _: Option<MissingControlPrincipal>,
    ) -> Result<Value, MissingControlStoreError> {
        Ok(self.pricing_data.clone())
    }
    async fn rankings(&self, period: &str) -> Result<Value, MissingControlStoreError> {
        if let Some(error) = &self.ranking_error {
            return Err(error.clone());
        }
        self.ranking_data
            .get(period)
            .cloned()
            .ok_or_else(|| MissingControlStoreError::new(format!("missing ranking data: {period}")))
    }
    async fn exposed_ratio(&self) -> Result<Option<Value>, MissingControlStoreError> {
        Ok(self.ratio.clone())
    }
    async fn token_usage(&self, key: &str) -> Result<Option<Value>, MissingControlStoreError> {
        if let Some(error) = &self.token_usage_error {
            return Err(error.clone());
        }
        Ok(self.usages.get(key).cloned())
    }

    async fn token_auth_read_only(
        &self,
        key: &str,
    ) -> Result<Option<MissingControlToken>, MissingControlStoreError> {
        if let Some(error) = &self.token_auth_error {
            return Err(error.clone());
        }
        if let Some(token) = self.token_records.get(key) {
            return Ok(Some(token.clone()));
        }
        Ok(self.usages.get(key).map(|_| MissingControlToken::default()))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::auth::{
        AuthBundle, AuthError, CriticalRateLimitOutcome, DashboardUser, LoginOutcome, LoginRequest,
        LogoutRequest, RequestMetadata, TwoFactorLoginRequest,
    };
    use axum::{body::Body, http::Request};
    use secrecy::ExposeSecret;
    use tower::ServiceExt;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    struct Auth(Option<MissingControlPrincipal>);
    #[async_trait]
    impl MissingControlAuthorizer for Auth {
        async fn principal(
            &self,
            _: &HeaderMap,
        ) -> Result<MissingControlPrincipal, MissingControlAuthError> {
            self.0.ok_or(MissingControlAuthError::Unauthorized)
        }

        async fn browser_session_principal(
            &self,
            headers: &HeaderMap,
        ) -> Result<Option<MissingControlPrincipal>, MissingControlAuthError> {
            self.principal(headers).await.map(Some)
        }
    }

    struct DashboardAuthForParser;

    #[async_trait]
    impl DashboardAuth for DashboardAuthForParser {
        async fn check_critical_rate_limit(
            &self,
            _: &str,
        ) -> Result<CriticalRateLimitOutcome, AuthError> {
            panic!("not used by public route tests")
        }

        async fn login(
            &self,
            _: LoginRequest,
            _: RequestMetadata,
        ) -> Result<LoginOutcome, AuthError> {
            panic!("not used by public route tests")
        }

        async fn login_2fa(
            &self,
            _: TwoFactorLoginRequest,
            _: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            panic!("not used by public route tests")
        }

        async fn refresh(
            &self,
            _: SecretString,
            _: Option<String>,
            _: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            panic!("not used by public route tests")
        }

        async fn self_user(&self, access_token: SecretString) -> Result<DashboardUser, AuthError> {
            if access_token.expose_secret() != "dashboard" {
                return Err(AuthError::new(AuthErrorKind::Unauthorized));
            }
            Ok(DashboardUser {
                id: 7,
                username: "dashboard".to_owned(),
                display_name: String::new(),
                role: 10,
                status: 1,
                email: String::new(),
                github_id: String::new(),
                discord_id: String::new(),
                oidc_id: String::new(),
                wechat_id: String::new(),
                telegram_id: String::new(),
                group: String::new(),
                quota: 0,
                used_quota: 0,
                request_count: 0,
                aff_code: String::new(),
                aff_count: 0,
                aff_quota: 0,
                aff_history_quota: 0,
                inviter_id: 0,
                linux_do_id: String::new(),
                setting: String::new(),
                stripe_customer: String::new(),
                sidebar_modules: Value::Null,
                permissions: Value::Null,
            })
        }

        async fn logout(&self, _: LogoutRequest) -> Result<crate::auth::LogoutResult, AuthError> {
            panic!("not used by public route tests")
        }

        async fn generate_personal_access_token(
            &self,
            _: SecretString,
        ) -> Result<String, AuthError> {
            panic!("not used by public route tests")
        }
    }

    fn app(role: Option<i64>) -> Router {
        let mut store = MemoryMissingControlStore {
            group_names: vec!["default".into()],
            models: json!({"1": ["gpt"]}),
            pricing_data: json!({"success": true, "data": []}),
            ratio: Some(json!({"model_ratio": {"gpt": 1}})),
            ..Default::default()
        };
        store
            .ranking_data
            .insert("week".into(), json!({"models": []}));
        store.usages.insert(
            "abc".into(),
            json!({
                "object": "token_usage",
                "name": "k",
                "total_granted": 13,
                "total_used": 5,
                "total_available": 8,
                "unlimited_quota": false,
                "model_limits": {"gpt": true},
                "model_limits_enabled": true,
                "expires_at": 0,
            }),
        );
        missing_control_public_router(MissingControlPublicState::new(
            Arc::new(store),
            Arc::new(Auth(
                role.map(|role| MissingControlPrincipal { user_id: 1, role }),
            )),
        ))
    }

    #[tokio::test]
    async fn all_dashboard_routes_use_the_shared_raw_or_bearer_parser() -> TestResult {
        let mut store = MemoryMissingControlStore {
            group_names: vec!["default".into()],
            models: json!({"1": ["gpt"]}),
            pricing_data: json!({"success": true, "data": []}),
            ..Default::default()
        };
        store
            .ranking_data
            .insert("week".into(), json!({"models": []}));
        for module in ["pricing", "rankings"] {
            store.nav.insert(
                module.to_owned(),
                HeaderNavAccess {
                    enabled: true,
                    require_auth: true,
                },
            );
        }
        let app = missing_control_public_router(MissingControlPublicState::new(
            Arc::new(store),
            Arc::new(DashboardMissingControlAuthorizer::new(Arc::new(
                DashboardAuthForParser,
            ))),
        ));
        for (uri, authorization) in [
            ("/api/group/", "dashboard"),
            ("/api/models", "dashboard"),
            ("/api/pricing", "dashboard"),
            ("/api/rankings?period=week", "dashboard"),
            ("/api/group/", "Bearer dashboard"),
            ("/api/models", "Bearer dashboard"),
            ("/api/pricing", "Bearer dashboard"),
            ("/api/rankings?period=week", "Bearer dashboard"),
        ] {
            let response = app
                .clone()
                .oneshot(
                    Request::get(uri)
                        .header("authorization", authorization)
                        .body(Body::empty())?,
                )
                .await?;
            assert_eq!(
                response.status(),
                StatusCode::OK,
                "{uri} with {authorization}"
            );
        }
        Ok(())
    }

    #[test]
    fn dashboard_credential_parser_matches_shared_http_semantics() {
        for (raw, expected) in [
            ("dashboard", Some("dashboard")),
            ("Bearer dashboard", Some("dashboard")),
            ("bEaReR dashboard", Some("dashboard")),
            ("Bearer  dashboard", Some("dashboard")),
            ("Bearer dashboard extra", None),
            ("", None),
        ] {
            let mut headers = HeaderMap::new();
            headers.insert(header::AUTHORIZATION, HeaderValue::from_static(raw));
            assert_eq!(
                dashboard_credential(&headers).as_deref(),
                expected,
                "{raw:?}"
            );
        }
    }

    #[tokio::test]
    async fn group_is_admin_only_and_preserves_legacy_envelope() -> TestResult {
        let unauthorized = app(None)
            .oneshot(Request::get("/api/group/").body(Body::empty())?)
            .await?;
        assert_eq!(unauthorized.status(), StatusCode::UNAUTHORIZED);
        let unauthorized_body = axum::body::to_bytes(unauthorized.into_body(), usize::MAX).await?;
        assert_eq!(
            serde_json::from_slice::<Value>(&unauthorized_body)?,
            json!({
                "success": false,
                "code": "AUTH_UNAUTHORIZED",
                "message": "Unauthorized, invalid access token",
            })
        );
        let denied = app(Some(1))
            .oneshot(
                Request::get("/api/group/")
                    .header("authorization", "Bearer x")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(denied.status(), StatusCode::FORBIDDEN);
        let ok = app(Some(10))
            .oneshot(
                Request::get("/api/group/")
                    .header("authorization", "Bearer x")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(ok.status(), StatusCode::OK);
        let body = axum::body::to_bytes(ok.into_body(), usize::MAX).await?;
        assert_eq!(
            serde_json::from_slice::<Value>(&body)?,
            json!({"success": true, "message": "", "data": ["default"]})
        );
        Ok(())
    }

    #[tokio::test]
    async fn models_requires_a_server_verified_user() -> TestResult {
        let response = app(None)
            .oneshot(Request::get("/api/models").body(Body::empty())?)
            .await?;
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        let body = axum::body::to_bytes(response.into_body(), usize::MAX).await?;
        assert_eq!(
            serde_json::from_slice::<Value>(&body)?,
            json!({
                "success": false,
                "code": "AUTH_UNAUTHORIZED",
                "message": "Unauthorized, invalid access token",
            })
        );
        let response = app(Some(1))
            .oneshot(
                Request::get("/api/models")
                    .header("authorization", "Bearer x")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::OK);
        let body = axum::body::to_bytes(response.into_body(), usize::MAX).await?;
        assert_eq!(
            serde_json::from_slice::<Value>(&body)?,
            json!({"success": true, "data": {"1": ["gpt"]}})
        );
        Ok(())
    }

    #[tokio::test]
    async fn public_routes_keep_methods_paths_and_bearer_failures() -> TestResult {
        let pricing = app(None)
            .oneshot(Request::get("/api/pricing").body(Body::empty())?)
            .await?;
        assert_eq!(pricing.status(), StatusCode::OK);
        let ranking = app(None)
            .oneshot(Request::get("/api/rankings?period=week").body(Body::empty())?)
            .await?;
        assert_eq!(ranking.status(), StatusCode::OK);
        let ratio = app(None)
            .oneshot(Request::get("/api/ratio_config").body(Body::empty())?)
            .await?;
        assert_eq!(ratio.status(), StatusCode::OK);
        let no_bearer = app(None)
            .oneshot(Request::get("/api/usage/token/").body(Body::empty())?)
            .await?;
        assert_eq!(no_bearer.status(), StatusCode::UNAUTHORIZED);
        let no_bearer_body = axum::body::to_bytes(no_bearer.into_body(), usize::MAX).await?;
        assert_eq!(
            serde_json::from_slice::<Value>(&no_bearer_body)?,
            json!({"success": false, "message": "Token not provided"})
        );
        let usage = app(None)
            .oneshot(
                Request::get("/api/usage/token/")
                    .header("authorization", "Bearer sk-abc")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(usage.status(), StatusCode::OK);
        let body = axum::body::to_bytes(usage.into_body(), usize::MAX).await?;
        assert_eq!(
            serde_json::from_slice::<Value>(&body)?,
            json!({
                "code": true,
                "message": "ok",
                "data": {
                    "object": "token_usage",
                    "name": "k",
                    "total_granted": 13,
                    "total_used": 5,
                    "total_available": 8,
                    "unlimited_quota": false,
                    "model_limits": {"gpt": true},
                    "model_limits_enabled": true,
                    "expires_at": 0,
                },
            })
        );
        Ok(())
    }

    #[tokio::test]
    async fn normal_ratio_config_read_fails_closed_when_options_are_unavailable() -> TestResult {
        let pool = sqlx::postgres::PgPoolOptions::new()
            .acquire_timeout(std::time::Duration::from_millis(1))
            .connect_lazy("postgres://unused:unused@localhost/unused")?;
        let response = ratio_config_direct(axum::extract::State(RatioConfigState {
            pg: pool,
            limiter: std::sync::Arc::new(AllowMissingControlRateLimiter),
        }))
        .await;
        assert_eq!(response.status(), StatusCode::FORBIDDEN);
        let body = axum::body::to_bytes(response.into_body(), usize::MAX).await?;
        assert_eq!(
            serde_json::from_slice::<Value>(&body)?,
            json!({"success": false, "message": "倍率配置接口未启用"})
        );
        Ok(())
    }

    #[test]
    fn ratio_map_json_matches_go_integer_number_encoding() {
        let values = BTreeMap::from([
            ("fractional".to_owned(), 0.25),
            ("integral".to_owned(), 2.0),
        ]);
        assert_eq!(
            go_ratio_map_json(values),
            json!({
                "fractional": 0.25,
                "integral": 2,
            })
        );
    }

    #[tokio::test]
    async fn rankings_keeps_the_go_envelope_for_empty_data_and_invalid_periods() -> TestResult {
        let success = app(None)
            .oneshot(Request::get("/api/rankings?period=week").body(Body::empty())?)
            .await?;
        assert_eq!(success.status(), StatusCode::OK);
        let success_body = axum::body::to_bytes(success.into_body(), usize::MAX).await?;
        assert_eq!(
            serde_json::from_slice::<Value>(&success_body)?,
            json!({"success": true, "data": {"models": []}})
        );

        let invalid = app(None)
            .oneshot(Request::get("/api/rankings?period=quarter").body(Body::empty())?)
            .await?;
        assert_eq!(invalid.status(), StatusCode::BAD_REQUEST);
        let invalid_body = axum::body::to_bytes(invalid.into_body(), usize::MAX).await?;
        assert_eq!(
            serde_json::from_slice::<Value>(&invalid_body)?,
            json!({"success": false, "message": "invalid ranking period: quarter"})
        );
        Ok(())
    }
}
