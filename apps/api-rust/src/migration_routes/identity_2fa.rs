//! Legacy-compatible authenticated 2FA management routes.
//!
//! The owning migration router mounts [`router`] behind its authenticated-user
//! extractor and supplies a [`SecuritySessionRotator`].  Keeping identity
//! extraction and token issuance outside this slice prevents a second, subtly
//! different authentication implementation from being introduced here.

use crate::auth::{
    AuthErrorKind, DashboardAuth, PgValkeyDashboardAuth, RequestMetadata,
    SecuritySessionRotationRequest, UserAuthPolicyError, enforce_user_auth,
};
use async_trait::async_trait;
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{Extension, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    middleware,
    response::{IntoResponse, Response},
    routing::{delete, get, post},
};
use bcrypt::{DEFAULT_COST, hash, verify};
use rand::Rng;
use secrecy::{ExposeSecret, SecretString};
use serde::{Deserialize, Serialize};
use serde_json::json;
use sqlx::{PgPool, Row};
use std::{
    sync::Arc,
    time::{Duration, SystemTime, UNIX_EPOCH},
};
use totp_rs::{Algorithm, Secret, TOTP};

// `common.GenerateBackupCodes` in the pinned Go source emits four codes.
const BACKUP_CODE_COUNT: usize = 4;
const BACKUP_CODE_LENGTH: usize = 8;
const TOTP_SECRET_LENGTH: usize = 32;
const TOTP_ISSUER: &str = "LMM Forge";
const TOTP_MAX_ATTEMPTS: i64 = 5;
const TOTP_LOCK_SECONDS: i64 = 5 * 60;
const ROLE_ADMIN: i64 = 10;
const ROLE_ROOT: i64 = 100;
const TWO_FACTOR_STATUS_PATH: &str = "/api/user/2fa/status";

/// Identity established by the parent authenticated-user extractor.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Identity2FAActor {
    pub user_id: i64,
    pub role: i64,
}

/// Authenticated current-session data injected by the listener after it has
/// validated the access token.  It is intentionally not deserializable from a
/// request body, so a caller cannot select another user's session to rotate.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Identity2FASession {
    pub session_id: String,
    pub client_ip: String,
    pub user_agent: String,
    pub cookie_secure: bool,
}

impl Identity2FASession {
    fn valid_for(&self, actor: Identity2FAActor) -> bool {
        actor.user_id > 0 && !self.session_id.trim().is_empty()
    }
}

/// Result returned by the session owner after it has safely rebound the
/// listener-authenticated current session to the new auth version.
pub struct SecuritySessionRotation {
    data: serde_json::Value,
    refresh_cookie: String,
}

/// Refreshes the current session after an auth-version change.
///
/// This is deliberately an integration boundary: the existing auth slice owns
/// token encoding, cookies, and the session cache.
#[async_trait]
pub trait SecuritySessionRotator: Send + Sync {
    async fn rotate_after_security_change(
        &self,
        actor: Identity2FAActor,
        session: &Identity2FASession,
        reason: &'static str,
        auth_version: i64,
    ) -> Result<SecuritySessionRotation, String>;
}

/// Production adapter over the same PostgreSQL/Valkey dashboard-session
/// implementation used by login and refresh.  Its input comes only from the
/// authenticated listener extensions, never from 2FA request JSON.
#[async_trait]
impl SecuritySessionRotator for PgValkeyDashboardAuth {
    async fn rotate_after_security_change(
        &self,
        actor: Identity2FAActor,
        session: &Identity2FASession,
        _reason: &'static str,
        auth_version: i64,
    ) -> Result<SecuritySessionRotation, String> {
        if !session.valid_for(actor) {
            return Err("invalid authenticated session context".to_owned());
        }
        let bundle = self
            .rotate_after_security_change(SecuritySessionRotationRequest {
                user_id: actor.user_id,
                session_id: session.session_id.clone(),
                auth_version,
                metadata: RequestMetadata {
                    ip: session.client_ip.clone(),
                    user_agent: session.user_agent.clone(),
                },
            })
            .await
            .map_err(|_| "session rotation failed".to_owned())?;
        let expires_at = bundle.data.session.expires_at;
        Ok(SecuritySessionRotation {
            data: serde_json::to_value(bundle.data)
                .map_err(|_| "session response serialization failed".to_owned())?,
            refresh_cookie: refresh_cookie(
                bundle.refresh_token.expose_secret(),
                expires_at,
                session.cookie_secure,
            ),
        })
    }
}

/// Supplies entropy and wall-clock time at the TOTP system boundary.
///
/// Production uses [`SystemTwoFactorBoundary`]; a deterministic implementation
/// may be supplied to an isolated integration harness without weakening the
/// listener-facing API.
pub trait TwoFactorBoundary: Send + Sync {
    /// Produces the Base32 TOTP secret for a pending enrollment.
    fn totp_secret(&self) -> String;
    /// Produces the displayed one-time recovery codes.
    fn backup_codes(&self) -> Vec<String>;
    /// Returns the Unix timestamp used to validate a TOTP code.
    fn unix_seconds(&self) -> u64;
}

/// Production entropy and time source for 2FA enrollment and verification.
#[derive(Default)]
pub struct SystemTwoFactorBoundary;

impl TwoFactorBoundary for SystemTwoFactorBoundary {
    fn totp_secret(&self) -> String {
        random_alphanumeric(TOTP_SECRET_LENGTH)
    }

    fn backup_codes(&self) -> Vec<String> {
        generate_backup_codes()
    }

    fn unix_seconds(&self) -> u64 {
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_or(0, |duration| duration.as_secs())
    }
}

#[derive(Clone)]
pub struct Identity2FAState {
    pool: PgPool,
    valkey: redis::Client,
    rotator: Arc<dyn SecuritySessionRotator>,
    boundary: Arc<dyn TwoFactorBoundary>,
    dashboard_auth: Option<Arc<dyn DashboardAuth>>,
    cookie_secure: bool,
}

impl Identity2FAState {
    #[must_use]
    pub fn new(
        pool: PgPool,
        valkey: redis::Client,
        rotator: Arc<dyn SecuritySessionRotator>,
    ) -> Self {
        Self {
            pool,
            valkey,
            rotator,
            boundary: Arc::new(SystemTwoFactorBoundary),
            dashboard_auth: None,
            cookie_secure: false,
        }
    }

    /// Replaces only the entropy/time system boundary for an isolated harness.
    #[must_use]
    pub fn with_boundary(mut self, boundary: Arc<dyn TwoFactorBoundary>) -> Self {
        self.boundary = boundary;
        self
    }

    #[must_use]
    pub fn with_dashboard_auth(mut self, auth: Arc<dyn DashboardAuth>) -> Self {
        self.dashboard_auth = Some(auth);
        self
    }

    #[must_use]
    pub fn with_cookie_secure(mut self, secure: bool) -> Self {
        self.cookie_secure = secure;
        self
    }
}

/// Dependencies for the normal-listener read-only 2FA status route.
#[derive(Clone)]
pub struct Identity2FAReadState {
    pool: PgPool,
    auth: Arc<dyn DashboardAuth>,
}

impl Identity2FAReadState {
    #[must_use]
    pub fn new(pool: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self { pool, auth }
    }
}

/// Returns the legacy `/api/user/2fa/*` management surface.
pub fn router(state: Identity2FAState) -> Router {
    let writes = Router::<Identity2FAState>::new()
        .route("/api/user/2fa/setup", post(setup))
        .route("/api/user/2fa/enable", post(enable))
        .route("/api/user/2fa/disable", post(disable))
        .route("/api/user/2fa/backup_codes", post(regenerate_backup_codes))
        .layer(middleware::map_response(disable_cache));
    Router::<Identity2FAState>::new()
        .route("/api/user/2fa/status", get(status))
        .route("/api/user/2fa/stats", get(stats))
        .route("/api/user/{id}/2fa", delete(admin_disable))
        .merge(writes)
        .with_state(state.clone())
        .layer(middleware::from_fn_with_state(state, identity_2fa_guard))
        .layer(middleware::map_response(legacy_json_content_type))
}

/// Builds only the authenticated `GET /api/user/2fa/status` read. Mutation
/// routes remain on the full candidate router until their listener ownership
/// is independently promoted.
pub fn status_read_router(state: Identity2FAReadState) -> Router {
    Router::new()
        .route(TWO_FACTOR_STATUS_PATH, get(status_read))
        .with_state(state)
        .layer(middleware::map_response(legacy_json_content_type))
}

async fn identity_2fa_guard(
    State(state): State<Identity2FAState>,
    mut request: Request,
    next: middleware::Next,
) -> Response {
    let Some(auth) = state.dashboard_auth.as_ref() else {
        return next.run(request).await;
    };
    let Some(token) = dashboard_token(request.headers()) else {
        return auth_error(StatusCode::UNAUTHORIZED);
    };
    let session = match auth
        .current_session(secrecy::SecretString::from(token))
        .await
    {
        Ok(session) => session,
        Err(error) => {
            return match error.kind {
                AuthErrorKind::UserDisabled => {
                    user_auth_error(request.headers(), UserAuthPolicyError::UserDisabled)
                }
                AuthErrorKind::Internal => auth_error(StatusCode::INTERNAL_SERVER_ERROR),
                _ => auth_error(StatusCode::UNAUTHORIZED),
            };
        }
    };
    if let Err(error) = enforce_user_auth(&session.user) {
        return user_auth_error(request.headers(), error);
    }
    request.extensions_mut().insert(Identity2FAActor {
        user_id: session.user.id,
        role: session.user.role,
    });
    request.extensions_mut().insert(Identity2FASession {
        session_id: session.session_id,
        client_ip: session.client_ip,
        user_agent: session.user_agent,
        cookie_secure: state.cookie_secure,
    });
    next.run(request).await
}

fn dashboard_token(headers: &HeaderMap) -> Option<String> {
    let value = headers.get(header::AUTHORIZATION)?.to_str().ok()?;
    let mut words = value.split_whitespace();
    let first = words.next()?;
    let second = words.next();
    if words.next().is_some() {
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

fn auth_error(status: StatusCode) -> Response {
    (
        status,
        Json(json!({
            "success": false,
            "code": "AUTH_UNAUTHORIZED",
            "message": "Unauthorized, invalid access token"
        })),
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
            "message": crate::auth::user_auth_message(
                error,
                headers
                    .get(header::ACCEPT_LANGUAGE)
                    .and_then(|value| value.to_str().ok()),
            ),
        })),
    )
        .into_response()
}

#[derive(Serialize)]
struct Envelope<T: Serialize> {
    success: bool,
    message: &'static str,
    data: T,
}

#[derive(Serialize)]
struct Failure {
    success: bool,
    message: &'static str,
}

#[derive(Deserialize)]
struct CodeRequest {
    code: String,
}

#[derive(Serialize)]
struct Status {
    enabled: bool,
    locked: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    backup_codes_remaining: Option<i64>,
}

#[derive(Serialize)]
struct Setup {
    secret: String,
    qr_code_data: String,
    backup_codes: Vec<String>,
}

fn failure(message: &'static str) -> Response {
    Json(Failure {
        success: false,
        message,
    })
    .into_response()
}

fn internal() -> Response {
    failure("服务器内部错误")
}

fn rotated_success(message: &'static str, rotation: SecuritySessionRotation) -> Response {
    let mut response = Json(Envelope {
        success: true,
        message,
        data: rotation.data,
    })
    .into_response();
    let Ok(cookie) = HeaderValue::from_str(&rotation.refresh_cookie) else {
        return internal();
    };
    response.headers_mut().append(header::SET_COOKIE, cookie);
    response
}

async fn disable_cache(mut response: Response) -> Response {
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        HeaderValue::from_static("no-store, no-cache, must-revalidate, private, max-age=0"),
    );
    response
        .headers_mut()
        .insert(header::PRAGMA, HeaderValue::from_static("no-cache"));
    response
        .headers_mut()
        .insert(header::EXPIRES, HeaderValue::from_static("0"));
    response
}

async fn legacy_json_content_type(mut response: Response) -> Response {
    if response
        .headers()
        .get(header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.starts_with("application/json"))
    {
        response.headers_mut().insert(
            header::CONTENT_TYPE,
            HeaderValue::from_static("application/json; charset=utf-8"),
        );
    }
    response
}

async fn code_request(request: Request) -> Result<CodeRequest, Response> {
    let body = to_bytes(request.into_body(), 1024 * 1024)
        .await
        .map_err(|_| failure("参数错误"))?;
    serde_json::from_slice(&body).map_err(|_| failure("参数错误"))
}

fn require_session(
    actor: Identity2FAActor,
    session: Option<Extension<Identity2FASession>>,
) -> Option<Identity2FASession> {
    session
        .map(|Extension(session)| session)
        .filter(|session| session.valid_for(actor))
}

fn require_actor(actor: Option<Extension<Identity2FAActor>>) -> Option<Identity2FAActor> {
    actor
        .map(|Extension(actor)| actor)
        .filter(|actor| actor.user_id > 0)
}

async fn status(
    State(state): State<Identity2FAState>,
    actor: Option<Extension<Identity2FAActor>>,
) -> Response {
    let Some(actor) = require_actor(actor) else {
        return failure("未登录或用户已被封禁");
    };
    let row = match sqlx::query(
        "SELECT is_enabled, locked_until IS NOT NULL AND locked_until > NOW() AS locked FROM two_fas WHERE user_id = $1 AND deleted_at IS NULL LIMIT 1",
    ).bind(actor.user_id).fetch_optional(&state.pool).await { Ok(row) => row, Err(_) => return internal() };
    let Some(row) = row else {
        return Json(Envelope {
            success: true,
            message: "",
            data: Status {
                enabled: false,
                locked: false,
                backup_codes_remaining: None,
            },
        })
        .into_response();
    };
    let enabled: bool = match row.try_get("is_enabled") {
        Ok(value) => value,
        Err(_) => return internal(),
    };
    let locked: bool = match row.try_get("locked") {
        Ok(value) => value,
        Err(_) => return internal(),
    };
    let remaining = if enabled {
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM two_fa_backup_codes WHERE user_id = $1 AND is_used = FALSE AND deleted_at IS NULL")
            .bind(actor.user_id).fetch_one(&state.pool).await.ok()
    } else {
        None
    };
    Json(Envelope {
        success: true,
        message: "",
        data: Status {
            enabled,
            locked,
            backup_codes_remaining: remaining,
        },
    })
    .into_response()
}

async fn status_read(State(state): State<Identity2FAReadState>, headers: HeaderMap) -> Response {
    let Some(token) = dashboard_token(&headers) else {
        return auth_error(StatusCode::UNAUTHORIZED);
    };
    let user = match state.auth.self_user(SecretString::from(token)).await {
        Ok(user) => user,
        Err(error) => {
            return match error.kind {
                AuthErrorKind::UserDisabled => {
                    user_auth_error(&headers, UserAuthPolicyError::UserDisabled)
                }
                AuthErrorKind::Internal => auth_error(StatusCode::INTERNAL_SERVER_ERROR),
                _ => auth_error(StatusCode::UNAUTHORIZED),
            };
        }
    };
    if let Err(error) = enforce_user_auth(&user) {
        return user_auth_error(&headers, error);
    }

    let mut response = status_for_user(&state.pool, user.id).await;
    response.headers_mut().insert(
        "auth-version",
        HeaderValue::from_static("864b7076dbcd0a3c01b5520316720ebf"),
    );
    response
}

async fn status_for_user(pool: &PgPool, user_id: i64) -> Response {
    let row = match sqlx::query(
        "SELECT is_enabled, locked_until IS NOT NULL AND locked_until > NOW() AS locked FROM two_fas WHERE user_id = $1 AND deleted_at IS NULL LIMIT 1",
    )
    .bind(user_id)
    .fetch_optional(pool)
    .await
    {
        Ok(row) => row,
        Err(_) => return internal(),
    };
    let Some(row) = row else {
        return Json(Envelope {
            success: true,
            message: "",
            data: Status {
                enabled: false,
                locked: false,
                backup_codes_remaining: None,
            },
        })
        .into_response();
    };
    let enabled: bool = match row.try_get("is_enabled") {
        Ok(value) => value,
        Err(_) => return internal(),
    };
    let locked: bool = match row.try_get("locked") {
        Ok(value) => value,
        Err(_) => return internal(),
    };
    let remaining = if enabled {
        sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*) FROM two_fa_backup_codes WHERE user_id = $1 AND is_used = FALSE AND deleted_at IS NULL",
        )
        .bind(user_id)
        .fetch_one(pool)
        .await
        .ok()
    } else {
        None
    };
    Json(Envelope {
        success: true,
        message: "",
        data: Status {
            enabled,
            locked,
            backup_codes_remaining: remaining,
        },
    })
    .into_response()
}

async fn setup(
    State(state): State<Identity2FAState>,
    actor: Option<Extension<Identity2FAActor>>,
) -> Response {
    let Some(actor) = require_actor(actor) else {
        return failure("未登录或用户已被封禁");
    };
    let mut tx = match state.pool.begin().await {
        Ok(tx) => tx,
        Err(_) => return internal(),
    };
    let row = match sqlx::query(
        "SELECT id, is_enabled FROM two_fas WHERE user_id = $1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(actor.user_id)
    .fetch_optional(&mut *tx)
    .await
    {
        Ok(row) => row,
        Err(_) => return internal(),
    };
    if let Some(row) = row.as_ref() {
        let enabled = match row.try_get::<bool, _>("is_enabled") {
            Ok(enabled) => enabled,
            Err(_) => return internal(),
        };
        if enabled {
            return failure("用户已启用2FA，请先禁用后重新设置");
        }
    }
    if row.is_some()
        && (sqlx::query("DELETE FROM two_fa_backup_codes WHERE user_id = $1")
            .bind(actor.user_id)
            .execute(&mut *tx)
            .await
            .is_err()
            || sqlx::query("DELETE FROM two_fas WHERE user_id = $1 AND is_enabled = FALSE")
                .bind(actor.user_id)
                .execute(&mut *tx)
                .await
                .is_err())
    {
        return internal();
    }
    let username: String =
        match sqlx::query_scalar("SELECT username FROM users WHERE id = $1 AND deleted_at IS NULL")
            .bind(actor.user_id)
            .fetch_optional(&mut *tx)
            .await
        {
            Ok(Some(name)) => name,
            Ok(None) => return failure("用户不存在"),
            Err(_) => return internal(),
        };
    let secret = state.boundary.totp_secret();
    let codes = state.boundary.backup_codes();
    if sqlx::query("INSERT INTO two_fas (user_id, secret, is_enabled, failed_attempts, created_at, updated_at) VALUES ($1, $2, FALSE, 0, NOW(), NOW())")
        .bind(actor.user_id).bind(&secret).execute(&mut *tx).await.is_err() { return internal(); }
    for code in &codes {
        let Ok(code_hash) = hash(canonical_backup_code(code), DEFAULT_COST) else {
            return internal();
        };
        if sqlx::query("INSERT INTO two_fa_backup_codes (user_id, code_hash, is_used, created_at) VALUES ($1, $2, FALSE, NOW())")
            .bind(actor.user_id).bind(code_hash).execute(&mut *tx).await.is_err() { return internal(); }
    }
    if tx.commit().await.is_err() {
        return internal();
    }
    Json(Envelope {
        success: true,
        message: "2FA设置初始化成功，请使用认证器扫描二维码并输入验证码完成设置",
        data: Setup {
            qr_code_data: totp_uri(&username, &secret),
            secret,
            backup_codes: codes,
        },
    })
    .into_response()
}

async fn enable(
    State(state): State<Identity2FAState>,
    actor: Option<Extension<Identity2FAActor>>,
    session: Option<Extension<Identity2FASession>>,
    request: Request,
) -> Response {
    let Some(actor) = require_actor(actor) else {
        return failure("未登录或用户已被封禁");
    };
    let request = match code_request(request).await {
        Ok(request) => request,
        Err(response) => return response,
    };
    let Some(session) = require_session(actor, session) else {
        return internal();
    };
    let Some(code) = numeric_code(&request.code) else {
        return failure(numeric_code_error(&request.code));
    };
    let mut tx = match state.pool.begin().await {
        Ok(tx) => tx,
        Err(_) => return internal(),
    };
    if lock_user_for_security_mutation(&mut tx, actor.user_id)
        .await
        .is_err()
    {
        return internal();
    }
    let row = match factor_for_update(&mut tx, actor.user_id, None).await {
        Ok(Some(row)) => row,
        Ok(None) => return failure("请先完成2FA初始化设置"),
        Err(_) => return internal(),
    };
    let enabled = match row.try_get::<bool, _>("is_enabled") {
        Ok(enabled) => enabled,
        Err(_) => return internal(),
    };
    if enabled {
        return failure("2FA已经启用");
    }
    let secret: String = match row.try_get("secret") {
        Ok(value) => value,
        Err(_) => return internal(),
    };
    if !valid_totp(&secret, &code, state.boundary.unix_seconds()) {
        return failure("验证码或备用码错误，请重试");
    }
    let version = match enable_and_bump(&mut tx, actor.user_id).await {
        Ok(version) => version,
        Err(_) => return internal(),
    };
    if tx.commit().await.is_err()
        || publish_version(&state.valkey, actor.user_id, version)
            .await
            .is_err()
    {
        return internal();
    }
    let rotation = match state
        .rotator
        .rotate_after_security_change(actor, &session, "twofa_enabled", version)
        .await
    {
        Ok(data) => data,
        Err(_) => return internal(),
    };
    rotated_success("两步验证启用成功", rotation)
}

async fn disable(
    State(state): State<Identity2FAState>,
    actor: Option<Extension<Identity2FAActor>>,
    session: Option<Extension<Identity2FASession>>,
    request: Request,
) -> Response {
    let Some(actor) = require_actor(actor) else {
        return failure("未登录或用户已被封禁");
    };
    let request = match code_request(request).await {
        Ok(request) => request,
        Err(response) => return response,
    };
    let Some(session) = require_session(actor, session) else {
        return internal();
    };
    let mut tx = match state.pool.begin().await {
        Ok(tx) => tx,
        Err(_) => return internal(),
    };
    if lock_user_for_security_mutation(&mut tx, actor.user_id)
        .await
        .is_err()
    {
        return internal();
    }
    let row = match factor_for_update(&mut tx, actor.user_id, Some(true)).await {
        Ok(Some(row)) => row,
        Ok(None) => return failure("用户未启用2FA"),
        Err(_) => return internal(),
    };
    match verify_factor(
        &mut tx,
        actor.user_id,
        &row,
        &request.code,
        state.boundary.unix_seconds(),
    )
    .await
    {
        Ok(true) => {}
        Ok(false) => {
            if tx.commit().await.is_err() {
                return internal();
            }
            return failure("验证码或备用码错误，请重试");
        }
        Err(_) => return internal(),
    }
    let version = match bump_auth_version(&mut tx, actor.user_id).await {
        Ok(version) => version,
        Err(_) => return internal(),
    };
    if sqlx::query("DELETE FROM two_fa_backup_codes WHERE user_id = $1")
        .bind(actor.user_id)
        .execute(&mut *tx)
        .await
        .is_err()
        || sqlx::query("DELETE FROM two_fas WHERE user_id = $1")
            .bind(actor.user_id)
            .execute(&mut *tx)
            .await
            .is_err()
        || tx.commit().await.is_err()
        || publish_version(&state.valkey, actor.user_id, version)
            .await
            .is_err()
    {
        return internal();
    }
    let rotation = match state
        .rotator
        .rotate_after_security_change(actor, &session, "twofa_disabled", version)
        .await
    {
        Ok(data) => data,
        Err(_) => return internal(),
    };
    rotated_success("两步验证已禁用", rotation)
}

async fn regenerate_backup_codes(
    State(state): State<Identity2FAState>,
    actor: Option<Extension<Identity2FAActor>>,
    session: Option<Extension<Identity2FASession>>,
    request: Request,
) -> Response {
    let Some(actor) = require_actor(actor) else {
        return failure("未登录或用户已被封禁");
    };
    let request = match code_request(request).await {
        Ok(request) => request,
        Err(response) => return response,
    };
    let Some(session) = require_session(actor, session) else {
        return internal();
    };
    let mut tx = match state.pool.begin().await {
        Ok(tx) => tx,
        Err(_) => return internal(),
    };
    if lock_user_for_security_mutation(&mut tx, actor.user_id)
        .await
        .is_err()
    {
        return internal();
    }
    let row = match factor_for_update(&mut tx, actor.user_id, Some(true)).await {
        Ok(Some(row)) => row,
        Ok(None) => return failure("用户未启用2FA"),
        Err(_) => return internal(),
    };
    let Some(code) = numeric_code(&request.code) else {
        return failure(numeric_code_error(&request.code));
    };
    if !verify_totp_factor(&mut tx, &row, &code, state.boundary.unix_seconds()).await {
        return failure("验证码或备用码错误，请重试");
    }
    let codes = state.boundary.backup_codes();
    let version = match bump_auth_version(&mut tx, actor.user_id).await {
        Ok(version) => version,
        Err(_) => return internal(),
    };
    if sqlx::query("DELETE FROM two_fa_backup_codes WHERE user_id = $1")
        .bind(actor.user_id)
        .execute(&mut *tx)
        .await
        .is_err()
    {
        return internal();
    }
    for code in &codes {
        let Ok(code_hash) = hash(canonical_backup_code(code), DEFAULT_COST) else {
            return internal();
        };
        if sqlx::query("INSERT INTO two_fa_backup_codes (user_id, code_hash, is_used, created_at) VALUES ($1, $2, FALSE, NOW())").bind(actor.user_id).bind(code_hash).execute(&mut *tx).await.is_err() { return internal(); }
    }
    if tx.commit().await.is_err()
        || publish_version(&state.valkey, actor.user_id, version)
            .await
            .is_err()
    {
        return internal();
    }
    let mut rotation = match state
        .rotator
        .rotate_after_security_change(actor, &session, "twofa_backup_codes_regenerated", version)
        .await
    {
        Ok(data) => data,
        Err(_) => return internal(),
    };
    rotation.data["backup_codes"] = serde_json::json!(codes);
    rotated_success("备用码重新生成成功", rotation)
}

async fn stats(
    State(state): State<Identity2FAState>,
    actor: Option<Extension<Identity2FAActor>>,
) -> Response {
    let Some(actor) = require_actor(actor) else {
        return failure("未登录或用户已被封禁");
    };
    if actor.role < ROLE_ADMIN {
        return failure("无权进行此操作");
    }
    let total =
        match sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM users WHERE deleted_at IS NULL")
            .fetch_one(&state.pool)
            .await
        {
            Ok(value) => value,
            Err(_) => return internal(),
        };
    let enabled = match sqlx::query_scalar::<_, i64>(
        "SELECT COUNT(*) FROM two_fas WHERE is_enabled = TRUE AND deleted_at IS NULL",
    )
    .fetch_one(&state.pool)
    .await
    {
        Ok(value) => value,
        Err(_) => return internal(),
    };
    Json(Envelope { success: true, message: "", data: serde_json::json!({"total_users": total, "enabled_users": enabled, "enabled_rate": format!("{:.1}%", if total == 0 { 0.0 } else { enabled as f64 / total as f64 * 100.0 })}) }).into_response()
}

async fn admin_disable(
    State(state): State<Identity2FAState>,
    actor: Option<Extension<Identity2FAActor>>,
    axum::extract::Path(user_id): axum::extract::Path<i64>,
) -> Response {
    let Some(actor) = require_actor(actor) else {
        return failure("未登录或用户已被封禁");
    };
    if actor.role < ROLE_ADMIN || user_id <= 0 {
        return failure("无权进行此操作");
    }
    let mut tx = match state.pool.begin().await {
        Ok(tx) => tx,
        Err(_) => return internal(),
    };
    let target_role = match sqlx::query_scalar::<_, i64>(
        "SELECT role FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(user_id)
    .fetch_optional(&mut *tx)
    .await
    {
        Ok(Some(role)) => role,
        Ok(None) => return failure("用户不存在"),
        Err(_) => return internal(),
    };
    if actor.role != ROLE_ROOT && actor.role <= target_role {
        return failure("无权操作同级或更高级用户的2FA设置");
    }
    let enabled = match sqlx::query_scalar::<_, i64>(
        "SELECT id FROM two_fas WHERE user_id = $1 AND is_enabled = TRUE AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(user_id)
    .fetch_optional(&mut *tx)
    .await
    {
        Ok(Some(id)) => id,
        Ok(None) => return failure("用户未启用2FA"),
        Err(_) => return internal(),
    };
    let version = match bump_auth_version(&mut tx, user_id).await {
        Ok(version) => version,
        Err(_) => return internal(),
    };
    if sqlx::query("DELETE FROM two_fa_backup_codes WHERE user_id = $1")
        .bind(user_id)
        .execute(&mut *tx)
        .await
        .is_err()
        || sqlx::query("DELETE FROM two_fas WHERE id = $1")
            .bind(enabled)
            .execute(&mut *tx)
            .await
            .is_err()
        || sqlx::query("UPDATE user_sessions SET status = 'revoked', revoked_at = EXTRACT(EPOCH FROM NOW())::BIGINT, revoked_reason = 'admin_twofa_disabled' WHERE user_id = $1 AND status = 'active'")
            .bind(user_id)
            .execute(&mut *tx)
            .await
            .is_err()
        || tx.commit().await.is_err()
        || publish_version(&state.valkey, user_id, version).await.is_err()
    {
        return internal();
    }
    Json(Envelope {
        success: true,
        message: "用户2FA已被强制禁用",
        data: serde_json::json!({}),
    })
    .into_response()
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::migration_routes) enum CriticalTwoFactorVerification {
    NotRequired,
    Verified,
    Rejected,
}

pub(in crate::migration_routes) async fn verify_critical_mutation_factor(
    tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    user_id: i64,
    code: &str,
) -> Result<CriticalTwoFactorVerification, sqlx::Error> {
    let Some(row) = factor_for_update(tx, user_id, Some(true)).await? else {
        return Ok(CriticalTwoFactorVerification::NotRequired);
    };
    let unix_seconds = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs();
    if verify_factor(tx, user_id, &row, code, unix_seconds).await? {
        Ok(CriticalTwoFactorVerification::Verified)
    } else {
        Ok(CriticalTwoFactorVerification::Rejected)
    }
}

async fn factor_for_update(
    tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    user_id: i64,
    enabled: Option<bool>,
) -> Result<Option<sqlx::postgres::PgRow>, sqlx::Error> {
    sqlx::query("SELECT id, secret, is_enabled, locked_until IS NOT NULL AND locked_until > NOW() AS locked FROM two_fas WHERE user_id = $1 AND ($2::BOOLEAN IS NULL OR is_enabled = $2) AND deleted_at IS NULL FOR UPDATE")
        .bind(user_id)
        .bind(enabled)
        .fetch_optional(&mut **tx)
        .await
}

async fn verify_factor(
    tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    user_id: i64,
    row: &sqlx::postgres::PgRow,
    code: &str,
    unix_seconds: u64,
) -> Result<bool, sqlx::Error> {
    let factor_id = row.try_get::<i64, _>("id")?;
    let secret = row.try_get::<String, _>("secret")?;
    let locked = row.try_get::<bool, _>("locked")?;
    if locked {
        return Ok(false);
    }
    let valid = numeric_code(code).is_some_and(|code| valid_totp(&secret, &code, unix_seconds));
    if valid {
        let updated = sqlx::query("UPDATE two_fas SET failed_attempts = 0, locked_until = NULL, last_used_at = NOW(), updated_at = NOW() WHERE id = $1")
            .bind(factor_id)
            .execute(&mut **tx)
            .await?;
        return Ok(updated.rows_affected() == 1);
    }
    let backup_rows = sqlx::query("SELECT id, code_hash FROM two_fa_backup_codes WHERE user_id = $1 AND is_used = FALSE AND deleted_at IS NULL FOR UPDATE")
        .bind(user_id)
        .fetch_all(&mut **tx)
        .await?;
    let canonical_backup = valid_backup_code(code).then(|| canonical_backup_code(code));
    for backup in backup_rows {
        let Ok(hash_value) = backup.try_get::<String, _>("code_hash") else {
            continue;
        };
        let verified = canonical_backup
            .as_ref()
            .is_some_and(|candidate| matches!(verify(candidate, &hash_value), Ok(true)));
        if verified {
            let backup_id = backup.try_get::<i64, _>("id")?;
            let claimed = sqlx::query("UPDATE two_fa_backup_codes SET is_used = TRUE, used_at = NOW() WHERE id = $1 AND is_used = FALSE")
                .bind(backup_id)
                .execute(&mut **tx)
                .await?;
            if claimed.rows_affected() != 1 {
                return Ok(false);
            }
            let updated = sqlx::query("UPDATE two_fas SET failed_attempts = 0, locked_until = NULL, last_used_at = NOW(), updated_at = NOW() WHERE id = $1")
                .bind(factor_id)
                .execute(&mut **tx)
                .await?;
            return Ok(updated.rows_affected() == 1);
        }
    }
    sqlx::query("UPDATE two_fas SET failed_attempts = LEAST(COALESCE(failed_attempts, 0) + 1, $2), locked_until = CASE WHEN COALESCE(failed_attempts, 0) + 1 >= $2 THEN NOW() + make_interval(secs => $3) ELSE NULL END, updated_at = NOW() WHERE id = $1")
        .bind(factor_id)
        .bind(TOTP_MAX_ATTEMPTS)
        .bind(TOTP_LOCK_SECONDS as f64)
        .execute(&mut **tx)
        .await?;
    Ok(false)
}

async fn verify_totp_factor(
    tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    row: &sqlx::postgres::PgRow,
    code: &str,
    unix_seconds: u64,
) -> bool {
    let Ok(factor_id) = row.try_get::<i64, _>("id") else {
        return false;
    };
    let Ok(secret) = row.try_get::<String, _>("secret") else {
        return false;
    };
    let Ok(locked) = row.try_get::<bool, _>("locked") else {
        return false;
    };
    if locked {
        return false;
    }
    if valid_totp(&secret, code, unix_seconds) {
        return sqlx::query("UPDATE two_fas SET failed_attempts = 0, locked_until = NULL, last_used_at = NOW(), updated_at = NOW() WHERE id = $1")
            .bind(factor_id)
            .execute(&mut **tx)
            .await
            .is_ok();
    }
    let _ = sqlx::query("UPDATE two_fas SET failed_attempts = LEAST(COALESCE(failed_attempts, 0) + 1, $2), locked_until = CASE WHEN COALESCE(failed_attempts, 0) + 1 >= $2 THEN NOW() + make_interval(secs => $3) ELSE NULL END, updated_at = NOW() WHERE id = $1")
        .bind(factor_id)
        .bind(TOTP_MAX_ATTEMPTS)
        .bind(TOTP_LOCK_SECONDS as f64)
        .execute(&mut **tx)
        .await;
    false
}

async fn lock_user_for_security_mutation(
    tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    user_id: i64,
) -> Result<(), sqlx::Error> {
    sqlx::query_scalar::<_, i64>(
        "SELECT id FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(user_id)
    .fetch_one(&mut **tx)
    .await
    .map(|_| ())
}

async fn bump_auth_version(
    tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    user_id: i64,
) -> Result<i64, sqlx::Error> {
    sqlx::query_scalar("UPDATE users SET auth_version = GREATEST(COALESCE(auth_version, 0), 0) + 1 WHERE id = $1 AND deleted_at IS NULL RETURNING auth_version").bind(user_id).fetch_one(&mut **tx).await
}

async fn enable_and_bump(
    tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    user_id: i64,
) -> Result<i64, sqlx::Error> {
    sqlx::query("UPDATE two_fas SET is_enabled = TRUE, failed_attempts = 0, locked_until = NULL, updated_at = NOW() WHERE user_id = $1 AND is_enabled = FALSE AND deleted_at IS NULL").bind(user_id).execute(&mut **tx).await?;
    bump_auth_version(tx, user_id).await
}

async fn publish_version(
    valkey: &redis::Client,
    user_id: i64,
    version: i64,
) -> Result<(), redis::RedisError> {
    let mut connection = valkey.get_multiplexed_async_connection().await?;
    let _: () = redis::pipe()
        .atomic()
        .cmd("SET")
        .arg(format!("auth:user:version:{user_id}"))
        .arg(version)
        .cmd("DEL")
        .arg(format!("auth:user:fence:{user_id}"))
        .cmd("DEL")
        .arg(format!("user:{user_id}"))
        .query_async(&mut connection)
        .await?;
    Ok(())
}

fn refresh_cookie(token: &str, expires_at: i64, secure: bool) -> String {
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs() as i64);
    let max_age = (expires_at - now).max(1);
    let expires =
        httpdate::fmt_http_date(UNIX_EPOCH + Duration::from_secs(expires_at.max(1) as u64));
    format!(
        "new_api_refresh={token}; Path=/api/user/auth; Expires={expires}; Max-Age={max_age}; HttpOnly; {}SameSite=Strict",
        if secure { "Secure; " } else { "" }
    )
}

fn numeric_code(code: &str) -> Option<String> {
    let code = code.replace(' ', "");
    (code.len() == 6 && code.bytes().all(|byte| byte.is_ascii_digit())).then_some(code)
}

fn numeric_code_error(code: &str) -> &'static str {
    if code.replace(' ', "").len() != 6 {
        "验证码必须是6位数字"
    } else {
        "验证码只能包含数字"
    }
}

fn valid_totp(secret: &str, code: &str, unix_seconds: u64) -> bool {
    let Ok(secret) = Secret::Encoded(secret.trim().to_ascii_uppercase()).to_bytes() else {
        return false;
    };
    TOTP::new(Algorithm::SHA1, 6, 1, 30, secret)
        .ok()
        .is_some_and(|totp| totp.check(code, unix_seconds))
}
fn generate_backup_codes() -> Vec<String> {
    (0..BACKUP_CODE_COUNT)
        .map(|_| {
            let code = random_backup_code(BACKUP_CODE_LENGTH);
            format!("{}-{}", &code[..4], &code[4..])
        })
        .collect()
}
fn random_alphanumeric(length: usize) -> String {
    const ALPHABET: &[u8] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
    let mut rng = rand::rng();
    (0..length)
        .map(|_| ALPHABET[rng.random_range(0..ALPHABET.len())] as char)
        .collect()
}
fn random_backup_code(length: usize) -> String {
    const ALPHABET: &[u8] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
    let mut rng = rand::rng();
    (0..length)
        .map(|_| ALPHABET[rng.random_range(0..ALPHABET.len())] as char)
        .collect()
}
fn normalize_backup_code(code: &str) -> String {
    code.replace('-', "").to_ascii_uppercase()
}
fn valid_backup_code(code: &str) -> bool {
    let normalized = normalize_backup_code(code);
    normalized.len() == BACKUP_CODE_LENGTH
        && normalized
            .bytes()
            .all(|byte| byte.is_ascii_uppercase() || byte.is_ascii_digit())
}
fn canonical_backup_code(code: &str) -> String {
    let normalized = normalize_backup_code(code);
    format!("{}-{}", &normalized[..4], &normalized[4..])
}
fn totp_uri(username: &str, secret: &str) -> String {
    format!(
        "otpauth://totp/{}:{}?secret={secret}&issuer={}",
        percent_encode(TOTP_ISSUER),
        percent_encode(username),
        percent_encode(TOTP_ISSUER)
    )
}
fn percent_encode(value: &str) -> String {
    value
        .bytes()
        .flat_map(|byte| match byte {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                vec![byte as char]
            }
            _ => format!("%{byte:02X}").chars().collect(),
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn backup_codes_are_normalized_without_changing_their_shape() {
        let code = generate_backup_codes().pop().expect("a recovery code");
        assert!(
            code.len() == 9
                && code.as_bytes()[4] == b'-'
                && normalize_backup_code(&code).len() == 8
        );
    }
    #[test]
    fn numeric_totp_requires_exactly_six_ascii_digits() {
        assert!(
            numeric_code("012 345").as_deref() == Some("012345")
                && numeric_code("１２３４５６").is_none()
                && numeric_code("12345").is_none()
        );
    }
    #[test]
    fn backup_code_hash_input_matches_legacy_hyphenated_normalization() {
        assert_eq!(canonical_backup_code("a1b2c3d4"), "A1B2-C3D4");
    }

    #[test]
    fn totp_validation_uses_the_supplied_system_boundary_time() {
        let secret = concat!("JBSWY3DP", "EHPK3PXP", "JBSWY3DP", "EHPK3PXP");
        let unix_seconds = 1_700_000_000;
        let totp = TOTP::new(
            Algorithm::SHA1,
            6,
            1,
            30,
            Secret::Encoded(secret.to_owned())
                .to_bytes()
                .expect("fixed Base32 secret"),
        )
        .expect("fixed TOTP configuration");

        assert!(valid_totp(
            secret,
            &totp.generate(unix_seconds),
            unix_seconds
        ));
    }

    #[test]
    fn totp_uri_escapes_an_identity_with_reserved_characters() {
        assert!(totp_uri("a+b@example.test", "JBSWY3DPEHPK3PXPJ").contains("a%2Bb%40example.test"));
    }
}
