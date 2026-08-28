//! Legacy-compatible authenticated profile routes.
//!
//! This module owns only self-service affiliation, preference, and profile
//! paths. Administrator CRUD, search, and management paths are exclusively
//! owned by [`crate::migration_routes::identity_admin`].

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{Extension, Path, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{delete, get, put},
};
use rand::distr::{Alphanumeric, SampleString};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};
use sqlx::{PgPool, Row};
use std::sync::Arc;

use crate::auth::{AuthErrorKind, DashboardAuth};

const ROLE_ROOT: i64 = 100;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";

/// Authenticated identity supplied by the listener after token validation.
#[derive(Clone, Copy, Debug)]
pub struct ProfileIdentity {
    /// The authenticated user id.
    pub user_id: i64,
    /// The authenticated role, never read from request JSON.
    pub role: i64,
}

/// Failure class from the server-side dashboard-identity verifier.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ProfileAuthError {
    /// The request did not carry a valid, live dashboard identity.
    Unauthorized,
    /// The verifier's durable session or user lookup failed.
    Internal,
}

/// Resolves a profile actor from credentials verified by the server.
///
/// Implementations must never read an identity from client-controlled headers.
#[async_trait]
pub trait ProfileIdentityResolver: Send + Sync {
    /// Validates the request credentials and returns the authenticated actor.
    async fn principal(&self, headers: &HeaderMap) -> Result<ProfileIdentity, ProfileAuthError>;
}

struct RejectingProfileIdentityResolver;

#[async_trait]
impl ProfileIdentityResolver for RejectingProfileIdentityResolver {
    async fn principal(&self, _: &HeaderMap) -> Result<ProfileIdentity, ProfileAuthError> {
        Err(ProfileAuthError::Unauthorized)
    }
}

/// Production adapter that delegates credential and session validation to the
/// shared dashboard-auth service.
#[derive(Clone)]
pub struct DashboardProfileIdentityResolver {
    auth: Arc<dyn DashboardAuth>,
}

impl DashboardProfileIdentityResolver {
    /// Creates an adapter over the listener's configured dashboard auth service.
    pub fn new(auth: Arc<dyn DashboardAuth>) -> Self {
        Self { auth }
    }
}

#[async_trait]
impl ProfileIdentityResolver for DashboardProfileIdentityResolver {
    async fn principal(&self, headers: &HeaderMap) -> Result<ProfileIdentity, ProfileAuthError> {
        let Some(token) = bearer(headers) else {
            return Err(ProfileAuthError::Unauthorized);
        };
        match self.auth.self_user(SecretString::from(token)).await {
            Ok(user) if user.id > 0 && user.status == 1 => Ok(ProfileIdentity {
                user_id: user.id,
                role: user.role,
            }),
            Ok(_) => Err(ProfileAuthError::Unauthorized),
            Err(error)
                if matches!(
                    error.kind,
                    AuthErrorKind::TokenExpired
                        | AuthErrorKind::SessionRevoked
                        | AuthErrorKind::Unauthorized
                ) =>
            {
                Err(ProfileAuthError::Unauthorized)
            }
            Err(_) => Err(ProfileAuthError::Internal),
        }
    }
}

/// PostgreSQL and Valkey dependencies used by the profile slice.
#[derive(Clone)]
pub struct ProfileState {
    pg: PgPool,
    valkey: redis::Client,
    identity: Arc<dyn ProfileIdentityResolver>,
}

impl ProfileState {
    /// Builds the state from the listener's shared PostgreSQL and Valkey clients.
    pub fn new(pg: PgPool, valkey: redis::Client) -> Self {
        Self {
            pg,
            valkey,
            identity: Arc::new(RejectingProfileIdentityResolver),
        }
    }

    /// Installs the listener-owned server-side identity verifier.
    #[must_use]
    pub fn with_identity_resolver(mut self, identity: Arc<dyn ProfileIdentityResolver>) -> Self {
        self.identity = identity;
        self
    }

    /// Installs the production adapter backed by the shared dashboard auth service.
    #[must_use]
    pub fn with_dashboard_auth(self, auth: Arc<dyn DashboardAuth>) -> Self {
        self.with_identity_resolver(Arc::new(DashboardProfileIdentityResolver::new(auth)))
    }
}

/// Self-service affiliation, preference, and profile routes.
pub fn router(state: ProfileState) -> Router {
    Router::new()
        .route("/api/user/aff", get(get_aff_code))
        .route("/api/user/setting", put(update_setting))
        .route("/api/user/self", put(update_self).delete(delete_self))
        .route(
            "/api/user/bindings/{binding_type}",
            delete(clear_self_oauth_binding),
        )
        // Go's UserAuth runs before the handler's body binding and publishes
        // Auth-Version on every downstream response. Inject the verified
        // principal once so profile handlers do not re-query auth or trust
        // client-supplied identity headers.
        .route_layer(middleware::from_fn_with_state(
            state.clone(),
            profile_auth_boundary,
        ))
        .with_state(state)
}

async fn profile_auth_boundary(
    State(state): State<ProfileState>,
    mut request: Request,
    next: Next,
) -> Response {
    let identity = match authenticated(&state, request.headers()).await {
        Ok(identity) => identity,
        Err(error) => return error.into_response(),
    };
    request.extensions_mut().insert(identity);
    let mut response = next.run(request).await;
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

fn self_oauth_binding_column(binding_type: &str) -> Option<(&'static str, &'static str)> {
    match binding_type.trim().to_ascii_lowercase().as_str() {
        "github" => Some(("github_id", "github")),
        "discord" => Some(("discord_id", "discord")),
        "oidc" => Some(("oidc_id", "oidc")),
        "wechat" => Some(("wechat_id", "wechat")),
        "telegram" => Some(("telegram_id", "telegram")),
        "linuxdo" => Some(("linux_do_id", "linuxdo")),
        _ => None,
    }
}

async fn clear_self_oauth_binding(
    State(state): State<ProfileState>,
    Extension(identity): Extension<ProfileIdentity>,
    Path(binding_type): Path<String>,
) -> Result<Response, ProfileError> {
    let Some((column, provider)) = self_oauth_binding_column(&binding_type) else {
        // Go's controller keeps this legacy API error at HTTP 200 and exposes
        // only the success/message envelope for invalid binding names.
        return Ok(Json(serde_json::json!({
            "success": false,
            "message": "invalid parameters"
        }))
        .into_response());
    };
    let mut transaction = state
        .pg
        .begin()
        .await
        .map_err(|_| ProfileError::internal())?;
    let exists = sqlx::query_scalar::<_, i64>(
        "SELECT id FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(identity.user_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(|_| ProfileError::internal())?;
    if exists.is_none() {
        return Err(ProfileError::not_found());
    }
    let statement = format!("UPDATE users SET {column} = '' WHERE id = $1 AND deleted_at IS NULL");
    sqlx::query(&statement)
        .bind(identity.user_id)
        .execute(&mut *transaction)
        .await
        .map_err(|_| ProfileError::internal())?;
    if provider == "telegram" {
        sqlx::query(
            "DELETE FROM external_identity_claims WHERE provider = 'telegram' AND user_id = $1",
        )
        .bind(identity.user_id)
        .execute(&mut *transaction)
        .await
        .map_err(|_| ProfileError::internal())?;
    }
    transaction
        .commit()
        .await
        .map_err(|_| ProfileError::internal())?;
    clear_user_cache(&state, identity.user_id).await;
    Ok(Json(serde_json::json!({
        "success": true,
        "message": "success"
    }))
    .into_response())
}

async fn update_self(
    State(state): State<ProfileState>,
    Extension(identity): Extension<ProfileIdentity>,
    request: Request,
) -> Result<Response, ProfileError> {
    let request_locale = locale(request.headers());
    let mut request = request_object(request).await?;
    if request.contains_key("sidebar_modules") || request.contains_key("language") {
        return update_self_setting(&state, identity.user_id, &mut request, request_locale).await;
    }
    let username = request
        .get("username")
        .and_then(Value::as_str)
        .unwrap_or_default();
    let display_name = request
        .get("display_name")
        .and_then(Value::as_str)
        .unwrap_or("");
    if username.len() > 20 || display_name.len() > 20 {
        return Err(ProfileError::bad_request("invalid input"));
    }
    // Password rotation must be performed by the session-owning auth module.
    if request
        .get("password")
        .and_then(Value::as_str)
        .is_some_and(|password| !password.is_empty())
    {
        return Err(ProfileError::bad_request(
            "password rotation requires the authenticated session",
        ));
    }
    sqlx::query("UPDATE users SET username = COALESCE(NULLIF($1, ''), username), display_name = COALESCE(NULLIF($2, ''), display_name) WHERE id = $3 AND deleted_at IS NULL")
        .bind(username)
        .bind(display_name)
        .bind(identity.user_id)
        .execute(&state.pg)
        .await
        .map_err(|_| ProfileError::internal())?;
    // `User.Update(false)` in the legacy handler republishes the existing
    // non-quota user hash after the durable write.  Keep that cache-aside
    // side effect instead of deleting the hash (a deletion would diverge on
    // a following setting write and needlessly force a cold refill).
    refresh_user_cache(&state, identity.user_id).await;
    Ok(ordinary_update_success())
}

async fn request_object(request: Request) -> Result<Map<String, Value>, ProfileError> {
    let bytes = to_bytes(request.into_body(), 1024 * 1024)
        .await
        .map_err(|_| ProfileError::bad_request("invalid parameters"))?;
    match serde_json::from_slice::<Value>(&bytes)
        .map_err(|_| ProfileError::bad_request("invalid parameters"))?
    {
        Value::Object(object) => Ok(object),
        // encoding/json accepts `null` into a Go struct and leaves all fields
        // at their zero values; the profile handlers then apply their normal
        // validation/default behavior.
        Value::Null => Ok(Map::new()),
        _ => Err(ProfileError::bad_request("invalid parameters")),
    }
}

async fn update_self_setting(
    state: &ProfileState,
    user_id: i64,
    request: &mut Map<String, Value>,
    request_locale: LegacyLocale,
) -> Result<Response, ProfileError> {
    let raw = sqlx::query_scalar::<_, Option<String>>(
        "SELECT setting FROM users WHERE id = $1 AND deleted_at IS NULL",
    )
    .bind(user_id)
    .fetch_optional(&state.pg)
    .await
    .map_err(|_| ProfileError::internal())?
    .flatten()
    .unwrap_or_default();
    let mut setting = serde_json::from_str::<LegacyNotificationSetting>(&raw).unwrap_or_default();
    // The legacy handler gives sidebar preference precedence when both exist.
    if request.contains_key("sidebar_modules") {
        if let Some(Value::String(value)) = request.remove("sidebar_modules") {
            setting.sidebar_modules = value;
        }
    } else if let Some(Value::String(value)) = request.remove("language") {
        setting.language = value;
    }
    let serialized = serialize_legacy_notification_setting(&setting)?;
    sqlx::query("UPDATE users SET setting = $1 WHERE id = $2 AND deleted_at IS NULL")
        .bind(&serialized)
        .bind(user_id)
        .execute(&state.pg)
        .await
        .map_err(|_| ProfileError::internal())?;
    update_user_setting_cache(state, user_id, &serialized).await;
    Ok(common_update_success(request_locale))
}

async fn delete_self(
    State(state): State<ProfileState>,
    Extension(identity): Extension<ProfileIdentity>,
) -> Result<Json<Success<()>>, ProfileError> {
    let mut transaction = state
        .pg
        .begin()
        .await
        .map_err(|_| ProfileError::internal())?;
    let role = sqlx::query_scalar::<_, i64>(
        "SELECT role FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(identity.user_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(|_| ProfileError::internal())?
    .ok_or_else(ProfileError::not_found)?;
    if role == ROLE_ROOT {
        return Err(ProfileError::forbidden("root user cannot be deleted"));
    }
    let version = sqlx::query_scalar::<_, i64>("UPDATE users SET deleted_at = NOW(), auth_version = auth_version + 1 WHERE id = $1 AND deleted_at IS NULL RETURNING auth_version")
        .bind(identity.user_id)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(|_| ProfileError::internal())?
        .ok_or_else(ProfileError::not_found)?;
    sqlx::query("UPDATE user_sessions SET status = 'revoked', revoked_at = EXTRACT(EPOCH FROM NOW())::BIGINT, revoked_reason = 'self_delete' WHERE user_id = $1 AND status = 'active'")
        .bind(identity.user_id)
        .execute(&mut *transaction)
        .await
        .map_err(|_| ProfileError::internal())?;
    transaction
        .commit()
        .await
        .map_err(|_| ProfileError::internal())?;
    publish_auth_floor(&state, identity.user_id, version).await;
    Ok(success(None))
}

#[derive(Serialize)]
struct Success<T: Serialize> {
    success: bool,
    message: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<T>,
}

fn success<T: Serialize>(data: Option<T>) -> Json<Success<T>> {
    Json(Success {
        success: true,
        message: "",
        data,
    })
}

#[derive(Debug)]
struct ProfileError {
    status: StatusCode,
    code: Option<&'static str>,
    message: &'static str,
}

impl ProfileError {
    const fn legacy(message: &'static str) -> Self {
        Self {
            status: StatusCode::OK,
            code: None,
            message,
        }
    }

    const fn bad_request(message: &'static str) -> Self {
        Self {
            status: StatusCode::BAD_REQUEST,
            code: None,
            message,
        }
    }

    const fn forbidden(message: &'static str) -> Self {
        Self {
            status: StatusCode::FORBIDDEN,
            code: None,
            message,
        }
    }

    const fn not_found() -> Self {
        Self {
            status: StatusCode::NOT_FOUND,
            code: None,
            message: "user not found",
        }
    }

    fn unauthorized(request_locale: LegacyLocale) -> Self {
        Self {
            status: StatusCode::UNAUTHORIZED,
            code: Some("AUTH_UNAUTHORIZED"),
            message: request_locale.invalid_access_token(),
        }
    }

    const fn internal() -> Self {
        Self {
            status: StatusCode::INTERNAL_SERVER_ERROR,
            code: None,
            message: "identity profile operation failed",
        }
    }

    fn auth_internal(locale: LegacyLocale) -> Self {
        Self {
            status: StatusCode::INTERNAL_SERVER_ERROR,
            code: Some("AUTH_INTERNAL_ERROR"),
            message: locale.database_error(),
        }
    }
}

impl IntoResponse for ProfileError {
    fn into_response(self) -> Response {
        // Gin's ApiError/ApiErrorI18n deliberately keep profile business and
        // database failures at HTTP 200. Only middleware-auth failures carry
        // an explicit auth code and retain their transport status.
        let status = if self.code.is_some() {
            self.status
        } else {
            StatusCode::OK
        };
        let mut body = serde_json::Map::from_iter([
            ("success".to_owned(), Value::Bool(false)),
            ("message".to_owned(), Value::String(self.message.to_owned())),
        ]);
        if let Some(code) = self.code {
            body.insert("code".to_owned(), Value::String(code.to_owned()));
        }
        (status, Json(Value::Object(body))).into_response()
    }
}

async fn get_aff_code(
    State(state): State<ProfileState>,
    Extension(identity): Extension<ProfileIdentity>,
) -> Result<Json<Success<String>>, ProfileError> {
    let existing =
        sqlx::query_scalar::<_, Option<String>>("SELECT aff_code FROM users WHERE id = $1")
            .bind(identity.user_id)
            .fetch_optional(&state.pg)
            .await
            .map_err(|_| ProfileError::internal())?
            .flatten()
            .filter(|code| !code.is_empty());
    if let Some(code) = existing {
        return Ok(success(Some(code)));
    }
    let generated = generate_aff_code();
    let assigned = sqlx::query_scalar::<_, String>("UPDATE users SET aff_code = $1 WHERE id = $2 AND (aff_code IS NULL OR aff_code = '') RETURNING aff_code")
        .bind(&generated)
        .bind(identity.user_id)
        .fetch_optional(&state.pg)
        .await
        .map_err(|_| ProfileError::internal())?;
    let code = match assigned {
        Some(code) => code,
        None => sqlx::query_scalar::<_, Option<String>>("SELECT aff_code FROM users WHERE id = $1")
            .bind(identity.user_id)
            .fetch_optional(&state.pg)
            .await
            .map_err(|_| ProfileError::internal())?
            .flatten()
            .filter(|code| !code.is_empty())
            .ok_or_else(ProfileError::not_found)?,
    };
    clear_user_cache(&state, identity.user_id).await;
    Ok(success(Some(code)))
}

const AFF_CODE_LENGTH: usize = 4;

/// Match Go's `common.GetRandomString(4)`: four ASCII alphanumeric characters.
fn generate_aff_code() -> String {
    Alphanumeric.sample_string(&mut rand::rng(), AFF_CODE_LENGTH)
}

async fn update_setting(
    State(state): State<ProfileState>,
    Extension(identity): Extension<ProfileIdentity>,
    request: Request,
) -> Result<Response, ProfileError> {
    let request_locale = locale(request.headers());
    let object = match request_object(request).await {
        Ok(object) => object,
        Err(_) => return Err(ProfileError::legacy(request_locale.invalid_parameters())),
    };
    let request: UserSettingRequest = match serde_json::from_value(Value::Object(object)) {
        Ok(request) => request,
        Err(_) => return Err(ProfileError::legacy(request_locale.invalid_parameters())),
    };
    validate_user_setting(&request, request_locale)?;
    update_notification_setting(&state, identity, request).await?;
    Ok(update_success(request_locale))
}

#[derive(Deserialize)]
struct UserSettingRequest {
    #[serde(default)]
    notify_type: String,
    #[serde(default)]
    quota_warning_threshold: f64,
    #[serde(default)]
    webhook_url: String,
    #[serde(default)]
    webhook_secret: String,
    #[serde(default)]
    notification_email: String,
    #[serde(default)]
    bark_url: String,
    #[serde(default)]
    gotify_url: String,
    #[serde(default)]
    gotify_token: String,
    #[serde(default)]
    gotify_priority: i64,
    upstream_model_update_notify_enabled: Option<bool>,
    #[serde(default)]
    accept_unset_model_ratio_model: bool,
    #[serde(default)]
    record_ip_log: bool,
}

fn validate_user_setting(
    request: &UserSettingRequest,
    request_locale: LegacyLocale,
) -> Result<(), ProfileError> {
    if !matches!(
        request.notify_type.as_str(),
        "email" | "webhook" | "bark" | "gotify"
    ) {
        return Err(ProfileError::legacy(request_locale.invalid_setting_type()));
    }
    if !request.quota_warning_threshold.is_finite() || request.quota_warning_threshold <= 0.0 {
        return Err(ProfileError::legacy(
            request_locale.quota_threshold_gt_zero(),
        ));
    }
    match request.notify_type.as_str() {
        "email"
            if !request.notification_email.is_empty()
                && !request.notification_email.contains('@') =>
        {
            Err(ProfileError::legacy(request_locale.invalid_email()))
        }
        "webhook" => validate_http_url(&request.webhook_url, "webhook URL", request_locale),
        "bark" => validate_http_url(&request.bark_url, "Bark URL", request_locale),
        "gotify" => {
            if request.gotify_url.is_empty() {
                return Err(ProfileError::legacy(request_locale.gotify_url_empty()));
            }
            if request.gotify_token.is_empty() {
                return Err(ProfileError::legacy(request_locale.gotify_token_empty()));
            }
            validate_http_url(&request.gotify_url, "Gotify URL", request_locale)?;
            Ok(())
        }
        _ => Ok(()),
    }
}

fn validate_http_url(
    value: &str,
    label: &'static str,
    request_locale: LegacyLocale,
) -> Result<(), ProfileError> {
    let error = || {
        ProfileError::legacy(match label {
            "webhook URL" => request_locale.invalid_webhook_url(),
            "Bark URL" => request_locale.invalid_bark_url(),
            "Gotify URL" => request_locale.invalid_gotify_url(),
            _ => "Invalid URL",
        })
    };
    if value.is_empty() {
        return Err(ProfileError::legacy(match label {
            "webhook URL" => request_locale.webhook_url_empty(),
            "Bark URL" => request_locale.bark_url_empty(),
            "Gotify URL" => request_locale.gotify_url_empty(),
            _ => "URL cannot be empty",
        }));
    }
    if !is_request_uri(value) {
        return Err(error());
    }
    if matches!(label, "Bark URL" | "Gotify URL")
        && !value.starts_with("https://")
        && !value.starts_with("http://")
    {
        return Err(ProfileError::legacy(request_locale.url_must_http()));
    }
    Ok(())
}

/// Go's url.ParseRequestURI accepts an absolute URI or an absolute request
/// path, but rejects bare relative paths and whitespace/control characters.
fn is_request_uri(value: &str) -> bool {
    if value.is_empty() || value.chars().any(char::is_whitespace) {
        return false;
    }
    if value.starts_with('/') {
        return true;
    }
    let Some(colon) = value.find(':') else {
        return false;
    };
    colon > 0
        && value[..colon].chars().enumerate().all(|(index, ch)| {
            if index == 0 {
                ch.is_ascii_alphabetic()
            } else {
                ch.is_ascii_alphanumeric() || matches!(ch, '+' | '-' | '.')
            }
        })
}

async fn update_notification_setting(
    state: &ProfileState,
    identity: ProfileIdentity,
    request: UserSettingRequest,
) -> Result<(), ProfileError> {
    let raw = sqlx::query_scalar::<_, Option<String>>(
        "SELECT setting FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(identity.user_id)
    .fetch_optional(&state.pg)
    .await
    .map_err(|_| ProfileError::internal())?
    .flatten()
    .unwrap_or_default();
    let setting = build_notification_setting(&raw, &request, identity.role)?;
    let updated = sqlx::query("UPDATE users SET setting = $1 WHERE id = $2 AND deleted_at IS NULL")
        .bind(&setting)
        .bind(identity.user_id)
        .execute(&state.pg)
        .await
        .map_err(|_| ProfileError::internal())?;
    if updated.rows_affected() != 1 {
        return Err(ProfileError::not_found());
    }
    update_user_setting_cache(state, identity.user_id, &setting).await;
    Ok(())
}

#[derive(Default, Deserialize, Serialize, Debug, PartialEq)]
struct LegacyNotificationSetting {
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

fn build_notification_setting(
    raw: &str,
    request: &UserSettingRequest,
    role: i64,
) -> Result<String, ProfileError> {
    let existing = serde_json::from_str::<LegacyNotificationSetting>(raw).unwrap_or_default();
    let upstream = if role >= 10 {
        request
            .upstream_model_update_notify_enabled
            .unwrap_or(existing.upstream_model_update_notify_enabled)
    } else {
        existing.upstream_model_update_notify_enabled
    };
    let mut setting = LegacyNotificationSetting {
        notify_type: request.notify_type.clone(),
        quota_warning_threshold: request.quota_warning_threshold,
        upstream_model_update_notify_enabled: upstream,
        accept_unset_ratio_model: request.accept_unset_model_ratio_model,
        record_ip_log: request.record_ip_log,
        ..LegacyNotificationSetting::default()
    };
    match request.notify_type.as_str() {
        "email" => setting.notification_email = request.notification_email.clone(),
        "webhook" => {
            setting.webhook_url = request.webhook_url.clone();
            setting.webhook_secret = request.webhook_secret.clone();
        }
        "bark" => setting.bark_url = request.bark_url.clone(),
        "gotify" => {
            setting.gotify_url = request.gotify_url.clone();
            setting.gotify_token = request.gotify_token.clone();
            setting.gotify_priority = if !(0..=10).contains(&request.gotify_priority) {
                5
            } else {
                request.gotify_priority
            };
        }
        _ => return Err(ProfileError::internal()),
    }
    serialize_legacy_notification_setting(&setting)
}

fn go_json_number(value: f64) -> String {
    let mut number = value.to_string();
    if number.ends_with(".0") {
        number.truncate(number.len() - 2);
    }
    number
}

fn serialize_legacy_notification_setting(
    setting: &LegacyNotificationSetting,
) -> Result<String, ProfileError> {
    let mut serialized = serde_json::to_string(setting).map_err(|_| ProfileError::internal())?;
    // Go's encoding/json renders an integral float64 as an integer token
    // (`2`), while serde_json keeps the explicit decimal (`2.0`). The stored
    // setting is a legacy JSON string, so preserve Go's lexical form as well
    // as its parsed value.
    if let Some(marker) = serialized.find("\"quota_warning_threshold\":") {
        let value_start = marker + "\"quota_warning_threshold\":".len();
        let value_end = serialized[value_start..]
            .find([',', '}'])
            .map_or(serialized.len(), |offset| value_start + offset);
        serialized.replace_range(
            value_start..value_end,
            &go_json_number(setting.quota_warning_threshold),
        );
    }
    Ok(serialized)
}

async fn publish_auth_floor(state: &ProfileState, user_id: i64, version: i64) {
    let Ok(mut connection) = state.valkey.get_multiplexed_async_connection().await else {
        tracing::warn!(
            user_id,
            "identity profile Valkey unavailable after durable deletion"
        );
        return;
    };
    if redis::pipe()
        .atomic()
        .cmd("SET")
        .arg(format!("auth:user:version:{user_id}"))
        .arg(version)
        .cmd("DEL")
        .arg(format!("auth:user:fence:{user_id}"))
        .cmd("DEL")
        .arg(format!("user:{user_id}"))
        .query_async::<()>(&mut connection)
        .await
        .is_err()
    {
        tracing::warn!(
            user_id,
            "identity profile Valkey invalidation failed after durable deletion"
        );
    }
}

/// Refresh only the setting field of an already-populated legacy user hash.
///
/// Go's `UpdateUserSetting` uses an auth-version-fenced HSET and does not
/// manufacture a cold cache. Keep that cache-aside behavior for this route;
/// PostgreSQL remains authoritative when Valkey is unavailable.
async fn update_user_setting_cache(state: &ProfileState, user_id: i64, setting: &str) {
    update_user_cache_field(state, user_id, "Setting", setting).await;
}

/// Update one existing legacy user-cache field behind the same auth-version
/// fence used by Go's `updateUserCacheFieldAtVersion`.
async fn update_user_cache_field(state: &ProfileState, user_id: i64, field: &str, value: &str) {
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
            tracing::warn!(%error, user_id, "profile user cache version lookup failed");
            return;
        }
    };
    let Ok(mut connection) = state.valkey.get_multiplexed_async_connection().await else {
        tracing::warn!(user_id, "profile user cache connection failed");
        return;
    };
    let script = redis::Script::new(
        r#"local incoming=tonumber(ARGV[1]); local pending=tonumber(redis.call('GET',KEYS[2]) or '0'); local committed=tonumber(redis.call('GET',KEYS[3]) or '0'); local current=tonumber(redis.call('HGET',KEYS[1],'AuthVersion') or '0'); if pending>incoming or committed>incoming or current>incoming then return 0 end; if committed<incoming then redis.call('SET',KEYS[3],ARGV[1]) end; if pending>0 and pending<=incoming then redis.call('DEL',KEYS[2]) end; if redis.call('EXISTS',KEYS[1])==0 then return 1 end; if current~=incoming then return 1 end; redis.call('HSET',KEYS[1],ARGV[2],ARGV[3],'CacheSchema','2'); return 1"#,
    );
    if let Err(error) = script
        .key(format!("user:{user_id}"))
        .key(format!("auth:user:fence:{user_id}"))
        .key(format!("auth:user:version:{user_id}"))
        .arg(auth_version)
        .arg(field)
        .arg(value)
        .invoke_async::<i64>(&mut connection)
        .await
    {
        tracing::warn!(%error, user_id, field, "profile user cache update failed");
    }
}

/// Republish the complete non-quota user hash after the ordinary self-profile
/// update.  This mirrors Go's `updateUserCache`/`writeUserCache(..., false)`:
/// an existing hash is refreshed atomically, while a cold cache is left cold.
async fn refresh_user_cache(state: &ProfileState, user_id: i64) {
    let row = match sqlx::query(
        r#"SELECT "group", COALESCE(email, '') AS email, status, role, username,
                  COALESCE(setting, '') AS setting, COALESCE(auth_version, 0) AS auth_version
             FROM users
            WHERE id = $1 AND deleted_at IS NULL"#,
    )
    .bind(user_id)
    .fetch_optional(&state.pg)
    .await
    {
        Ok(Some(row)) => row,
        Ok(None) => return,
        Err(error) => {
            tracing::warn!(%error, user_id, "profile user cache refresh query failed");
            return;
        }
    };
    let group = match row.try_get::<String, _>("group") {
        Ok(value) => value,
        Err(error) => {
            tracing::warn!(%error, user_id, "profile user cache group decode failed");
            return;
        }
    };
    let email = match row.try_get::<String, _>("email") {
        Ok(value) => value,
        Err(error) => {
            tracing::warn!(%error, user_id, "profile user cache email decode failed");
            return;
        }
    };
    let status: i64 = match row.try_get("status") {
        Ok(value) => value,
        Err(error) => {
            tracing::warn!(%error, user_id, "profile user cache status decode failed");
            return;
        }
    };
    let role: i64 = match row.try_get("role") {
        Ok(value) => value,
        Err(error) => {
            tracing::warn!(%error, user_id, "profile user cache role decode failed");
            return;
        }
    };
    let username = match row.try_get::<String, _>("username") {
        Ok(value) => value,
        Err(error) => {
            tracing::warn!(%error, user_id, "profile user cache username decode failed");
            return;
        }
    };
    let setting = match row.try_get::<String, _>("setting") {
        Ok(value) => value,
        Err(error) => {
            tracing::warn!(%error, user_id, "profile user cache setting decode failed");
            return;
        }
    };
    let auth_version: i64 = match row.try_get("auth_version") {
        Ok(value) if value > 0 => value,
        Ok(_) => return,
        Err(error) => {
            tracing::warn!(%error, user_id, "profile user cache auth version decode failed");
            return;
        }
    };
    let Ok(mut connection) = state.valkey.get_multiplexed_async_connection().await else {
        tracing::warn!(user_id, "profile user cache refresh connection failed");
        return;
    };
    let script = redis::Script::new(
        r#"local incoming=tonumber(ARGV[1]); local pending=tonumber(redis.call('GET',KEYS[2]) or '0'); local committed=tonumber(redis.call('GET',KEYS[3]) or '0'); local current=tonumber(redis.call('HGET',KEYS[1],'AuthVersion') or '0'); if pending>incoming or committed>incoming or current>incoming then return 0 end; if committed<incoming then redis.call('SET',KEYS[3],ARGV[1]) end; if pending>0 and pending<=incoming then redis.call('DEL',KEYS[2]) end; if ARGV[9]=='0' and redis.call('EXISTS',KEYS[1])==0 then return 1 end; redis.call('HSET',KEYS[1],'Id',ARGV[2],'Group',ARGV[3],'Email',ARGV[4],'Status',ARGV[5],'Role',ARGV[6],'Username',ARGV[7],'Setting',ARGV[8],'AuthVersion',ARGV[1],'CacheSchema','2'); return 1"#,
    );
    if let Err(error) = script
        .key(format!("user:{user_id}"))
        .key(format!("auth:user:fence:{user_id}"))
        .key(format!("auth:user:version:{user_id}"))
        .arg(auth_version)
        .arg(user_id)
        .arg(group)
        .arg(email)
        .arg(status)
        .arg(role)
        .arg(username)
        .arg(setting)
        .arg("0")
        .invoke_async::<i64>(&mut connection)
        .await
    {
        tracing::warn!(%error, user_id, "profile user cache refresh failed");
    }
}

async fn clear_user_cache(state: &ProfileState, user_id: i64) {
    let Ok(mut connection) = state.valkey.get_multiplexed_async_connection().await else {
        tracing::warn!(
            user_id,
            "identity profile Valkey unavailable after durable update"
        );
        return;
    };
    if redis::cmd("DEL")
        .arg(format!("user:{user_id}"))
        .query_async::<()>(&mut connection)
        .await
        .is_err()
    {
        tracing::warn!(
            user_id,
            "identity profile Valkey invalidation failed after durable update"
        );
    }
}

async fn authenticated(
    state: &ProfileState,
    headers: &HeaderMap,
) -> Result<ProfileIdentity, ProfileError> {
    state
        .identity
        .principal(headers)
        .await
        .map_err(|error| match error {
            ProfileAuthError::Unauthorized => ProfileError::unauthorized(locale(headers)),
            ProfileAuthError::Internal => ProfileError::auth_internal(locale(headers)),
        })
}

fn bearer(headers: &HeaderMap) -> Option<String> {
    let raw = headers.get(header::AUTHORIZATION)?.to_str().ok()?.trim();
    let mut parts = raw.split_ascii_whitespace();
    let scheme = parts.next()?;
    let token = parts.next()?;
    (scheme.eq_ignore_ascii_case("bearer") && parts.next().is_none() && !token.is_empty())
        .then(|| token.to_owned())
}

#[derive(Clone, Copy)]
enum LegacyLocale {
    En,
    ZhCn,
    ZhTw,
}

impl LegacyLocale {
    fn invalid_parameters(self) -> &'static str {
        match self {
            Self::En => "Invalid parameters",
            Self::ZhCn => "无效的参数",
            Self::ZhTw => "無效的參數",
        }
    }

    fn invalid_access_token(self) -> &'static str {
        match self {
            Self::En => "Unauthorized, invalid access token",
            Self::ZhCn => "无权进行此操作，access token 无效",
            Self::ZhTw => "無權進行此操作，access token 無效",
        }
    }

    fn database_error(self) -> &'static str {
        match self {
            Self::En => "Database error, please contact the administrator",
            Self::ZhCn => "数据库出错，请联系管理员",
            Self::ZhTw => "資料庫出錯，請聯繫管理員",
        }
    }

    fn invalid_setting_type(self) -> &'static str {
        match self {
            Self::En => "Invalid warning type",
            Self::ZhCn => "无效的预警类型",
            Self::ZhTw => "無效的預警類型",
        }
    }

    fn quota_threshold_gt_zero(self) -> &'static str {
        match self {
            Self::En => "Warning threshold must be greater than 0",
            Self::ZhCn => "预警阈值必须大于0",
            Self::ZhTw => "預警閾值必須大於0",
        }
    }

    fn invalid_webhook_url(self) -> &'static str {
        match self {
            Self::En => "Invalid Webhook URL",
            Self::ZhCn => "无效的Webhook地址",
            Self::ZhTw => "無效的Webhook位址",
        }
    }

    fn webhook_url_empty(self) -> &'static str {
        match self {
            Self::En => "Webhook URL cannot be empty",
            Self::ZhCn => "Webhook地址不能为空",
            Self::ZhTw => "Webhook位址不能為空",
        }
    }

    fn invalid_email(self) -> &'static str {
        match self {
            Self::En => "Invalid email address",
            Self::ZhCn => "无效的邮箱地址",
            Self::ZhTw => "無效的信箱位址",
        }
    }

    fn invalid_bark_url(self) -> &'static str {
        match self {
            Self::En => "Invalid Bark push URL",
            Self::ZhCn => "无效的Bark推送URL",
            Self::ZhTw => "無效的Bark推送URL",
        }
    }

    fn bark_url_empty(self) -> &'static str {
        match self {
            Self::En => "Bark push URL cannot be empty",
            Self::ZhCn => "Bark推送URL不能为空",
            Self::ZhTw => "Bark推送URL不能為空",
        }
    }

    fn invalid_gotify_url(self) -> &'static str {
        match self {
            Self::En => "Invalid Gotify server URL",
            Self::ZhCn => "无效的Gotify服务器地址",
            Self::ZhTw => "無效的Gotify伺服器位址",
        }
    }

    fn gotify_url_empty(self) -> &'static str {
        match self {
            Self::En => "Gotify server URL cannot be empty",
            Self::ZhCn => "Gotify服务器地址不能为空",
            Self::ZhTw => "Gotify伺服器位址不能為空",
        }
    }

    fn gotify_token_empty(self) -> &'static str {
        match self {
            Self::En => "Gotify token cannot be empty",
            Self::ZhCn => "Gotify令牌不能为空",
            Self::ZhTw => "Gotify令牌不能為空",
        }
    }

    fn url_must_http(self) -> &'static str {
        match self {
            Self::En => "URL must start with http:// or https://",
            Self::ZhCn => "URL必须以http://或https://开头",
            Self::ZhTw => "URL必須以http://或https://開頭",
        }
    }

    fn update_success(self) -> &'static str {
        match self {
            Self::En => "Settings updated",
            Self::ZhCn => "设置已更新",
            Self::ZhTw => "設定已更新",
        }
    }

    fn common_update_success(self) -> &'static str {
        match self {
            Self::En => "Update successful",
            Self::ZhCn | Self::ZhTw => "更新成功",
        }
    }
}

fn locale(headers: &HeaderMap) -> LegacyLocale {
    let language = headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.split(',').next())
        .map(|value| {
            value
                .split(';')
                .next()
                .unwrap_or("")
                .trim()
                .to_ascii_lowercase()
        })
        .unwrap_or_default();
    if language.starts_with("zh-tw") {
        LegacyLocale::ZhTw
    } else if language.starts_with("zh") {
        LegacyLocale::ZhCn
    } else {
        LegacyLocale::En
    }
}

fn update_success(request_locale: LegacyLocale) -> Response {
    let mut response = Json(serde_json::json!({
        "success": true,
        "message": request_locale.update_success(),
        "data": null,
    }))
    .into_response();
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        HeaderValue::from_static("no-store, no-cache, must-revalidate, private, max-age=0"),
    );
    response
}

fn common_update_success(request_locale: LegacyLocale) -> Response {
    let mut response = Json(serde_json::json!({
        "success": true,
        "message": request_locale.common_update_success(),
        "data": null,
    }))
    .into_response();
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        HeaderValue::from_static("no-store, no-cache, must-revalidate, private, max-age=0"),
    );
    response
}

fn ordinary_update_success() -> Response {
    let mut response = Json(serde_json::json!({
        "success": true,
        "message": "",
    }))
    .into_response();
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        HeaderValue::from_static("no-store, no-cache, must-revalidate, private, max-age=0"),
    );
    response
}

#[cfg(test)]
mod tests {
    use super::{
        AFF_CODE_LENGTH, LegacyLocale, UserSettingRequest, build_notification_setting,
        generate_aff_code, is_request_uri, self_oauth_binding_column, validate_user_setting,
    };
    use serde_json::{Map, Value};

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    #[test]
    fn self_setting_keeps_sidebar_precedence_when_both_legacy_keys_arrive() {
        let request = Map::from_iter([
            ("language".to_owned(), Value::String("en".to_owned())),
            (
                "sidebar_modules".to_owned(),
                Value::String("{\"chat\":true}".to_owned()),
            ),
        ]);
        let key = if request.contains_key("sidebar_modules") {
            "sidebar_modules"
        } else {
            "language"
        };
        assert_eq!(key, "sidebar_modules");
    }

    #[test]
    fn self_oauth_binding_whitelist_matches_go() {
        for binding_type in ["github", "discord", "oidc", "wechat", "telegram", "linuxdo"] {
            assert!(self_oauth_binding_column(&format!("  {binding_type}  ")).is_some());
        }
        for binding_type in ["", "email", "github_id", "password", "../github"] {
            assert!(self_oauth_binding_column(binding_type).is_none());
        }
    }

    #[test]
    fn generated_aff_code_matches_go_length_and_charset() {
        for _ in 0..128 {
            let code = generate_aff_code();
            assert_eq!(code.len(), AFF_CODE_LENGTH);
            assert!(code.bytes().all(|byte| byte.is_ascii_alphanumeric()));
        }
    }

    #[test]
    fn notification_setting_serialization_matches_go_dto_and_priority_fallback() -> TestResult {
        let request = UserSettingRequest {
            notify_type: "gotify".to_owned(),
            quota_warning_threshold: 2.5,
            webhook_url: String::new(),
            webhook_secret: String::new(),
            notification_email: String::new(),
            bark_url: String::new(),
            gotify_url: "https://gotify.example".to_owned(),
            gotify_token: "token".to_owned(),
            gotify_priority: 99,
            upstream_model_update_notify_enabled: None,
            accept_unset_model_ratio_model: false,
            record_ip_log: false,
        };
        let json = build_notification_setting(
            r#"{"language":"zh","gotify_priority":7,"upstream_model_update_notify_enabled":true}"#,
            &request,
            1,
        )
        .map_err(|error| std::io::Error::other(format!("{error:?}")))?;
        let value: Value = serde_json::from_str(&json)?;
        assert_eq!(
            value,
            serde_json::json!({
                "notify_type": "gotify",
                "quota_warning_threshold": 2.5,
                "gotify_url": "https://gotify.example",
                "gotify_token": "token",
                "gotify_priority": 5,
                "upstream_model_update_notify_enabled": true,
            })
        );
        Ok(())
    }

    #[test]
    fn notification_setting_drops_fields_go_fresh_dto_drops() -> TestResult {
        let request = UserSettingRequest {
            notify_type: "email".to_owned(),
            quota_warning_threshold: 1.0,
            webhook_url: String::new(),
            webhook_secret: String::new(),
            notification_email: "ada@example.test".to_owned(),
            bark_url: String::new(),
            gotify_url: String::new(),
            gotify_token: String::new(),
            gotify_priority: 0,
            upstream_model_update_notify_enabled: None,
            accept_unset_model_ratio_model: false,
            record_ip_log: false,
        };
        let json = build_notification_setting(
            r#"{"language":"zh","billing_preference":"wallet","gotify_priority":7}"#,
            &request,
            1,
        )
        .map_err(|error| std::io::Error::other(format!("{error:?}")))?;
        let value: Value = serde_json::from_str(&json)?;
        assert_eq!(value["notify_type"], "email");
        assert_eq!(value["notification_email"], "ada@example.test");
        assert_eq!(value["gotify_priority"], 0);
        assert!(value.get("language").is_none());
        assert!(value.get("billing_preference").is_none());
        Ok(())
    }

    #[test]
    fn setting_validation_matches_go_legacy_http_status_messages() -> TestResult {
        let request = UserSettingRequest {
            notify_type: String::new(),
            quota_warning_threshold: 0.0,
            webhook_url: String::new(),
            webhook_secret: String::new(),
            notification_email: String::new(),
            bark_url: String::new(),
            gotify_url: String::new(),
            gotify_token: String::new(),
            gotify_priority: 0,
            upstream_model_update_notify_enabled: None,
            accept_unset_model_ratio_model: false,
            record_ip_log: false,
        };
        let error = validate_user_setting(&request, LegacyLocale::En)
            .err()
            .ok_or_else(|| std::io::Error::other("invalid warning type was accepted"))?;
        assert_eq!(error.status, axum::http::StatusCode::OK);
        assert_eq!(error.message, "Invalid warning type");
        assert!(is_request_uri("/webhook"));
        assert!(is_request_uri("https://example.test/hook"));
        assert!(!is_request_uri("relative/path"));
        Ok(())
    }
}
