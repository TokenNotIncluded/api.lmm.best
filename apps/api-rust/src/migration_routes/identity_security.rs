//! Frozen account-security and session route candidates.
//!
//! The module's only security authority is the listener-supplied
//! [`SecurityAuthorizer`], while mail, credential, and WebAuthn work stays
//! behind [`SecurityProvider`]. In particular, the in-memory provider is a
//! test fake and refuses security-sensitive operations by default rather than
//! manufacturing a successful proof or credential. Normal-listener mounting
//! is therefore a candidate-surface fact; it does not itself grant migration
//! gate ownership credit.

use std::{
    sync::{Arc, Mutex},
    time::{SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{DefaultBodyLimit, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, Uri, header},
    response::{IntoResponse, Response},
    routing::{delete, get, post},
};
use bcrypt::{DEFAULT_COST, hash};
use chrono::{DateTime, SecondsFormat, Utc};
use rand::{RngCore, rng};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{PgPool, Row};

use crate::migration_routes::{
    identity_2fa::{CriticalTwoFactorVerification, verify_critical_mutation_factor},
    verify_email::ValkeyVerificationCodeStore,
};
use crate::{
    ClientIpKey, RequestContext,
    auth::{
        AnonymousRequestSecurity, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth,
        SecurityProof, TurnstileCheckOutcome, dashboard_token_candidate,
        turnstile_failure_response, turnstile_missing_response,
    },
    legacy_empty_response,
};
use secrecy::SecretString;

const ADMIN_ROLE: i64 = 10;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const SESSIONS_PATH: &str = "/api/user/sessions";
const PASSKEY_PATH: &str = "/api/user/passkey";
const MAX_BODY_BYTES: usize = 1024 * 1024;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum LegacyLocale {
    En,
    ZhCn,
    ZhTw,
}

impl LegacyLocale {
    fn from_headers(headers: &HeaderMap) -> Self {
        let Some(language) = headers.get(header::ACCEPT_LANGUAGE) else {
            return Self::En;
        };
        let Ok(language) = language.to_str() else {
            return Self::En;
        };
        let Some(language) = language.split(',').next() else {
            return Self::En;
        };
        let Some(language) = language.split(';').next() else {
            return Self::En;
        };
        let language = language.trim().to_ascii_lowercase();
        if language.starts_with("zh-tw") {
            Self::ZhTw
        } else if language.starts_with("zh") {
            Self::ZhCn
        } else {
            Self::En
        }
    }

    const fn invalid_access_token(self) -> &'static str {
        match self {
            Self::En => "Unauthorized, invalid access token",
            Self::ZhCn => "无权进行此操作，access token 无效",
            Self::ZhTw => "無權進行此操作，access token 無效",
        }
    }

    const fn not_logged_in(self) -> &'static str {
        match self {
            Self::En => "Unauthorized, not logged in and no access token provided",
            Self::ZhCn => "无权进行此操作，未登录且未提供 access token",
            Self::ZhTw => "無權進行此操作，未登入且未提供 access token",
        }
    }

    const fn user_banned(self) -> &'static str {
        match self {
            Self::En => "User has been banned",
            Self::ZhCn => "用户已被封禁",
            Self::ZhTw => "使用者已被封禁",
        }
    }

    const fn invalid_user_info(self) -> &'static str {
        match self {
            Self::En => "Unauthorized, invalid user info",
            Self::ZhCn => "无权进行此操作，用户信息无效",
            Self::ZhTw => "無權進行此操作，使用者資訊無效",
        }
    }

    const fn invalid_params(self) -> &'static str {
        match self {
            Self::En => "Invalid parameters",
            Self::ZhCn => "无效的参数",
            Self::ZhTw => "無效的參數",
        }
    }

    const fn insufficient_privilege(self) -> &'static str {
        match self {
            Self::En => "Unauthorized, insufficient privileges",
            Self::ZhCn => "无权进行此操作，权限不足",
            Self::ZhTw => "無權進行此操作，權限不足",
        }
    }

    const fn database_error(self) -> &'static str {
        match self {
            Self::En => "Database error, please contact the administrator",
            Self::ZhCn => "数据库出错，请联系管理员",
            Self::ZhTw => "資料庫出錯，請聯繫管理員",
        }
    }

    const fn security_unavailable(self) -> &'static str {
        match self {
            Self::En => "Security service is temporarily unavailable",
            Self::ZhCn => "安全服务暂不可用",
            Self::ZhTw => "安全服務暫不可用",
        }
    }
}

/// Server-derived dashboard identity used by this route family.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct SecurityActor {
    /// Durable user id, never a client-controlled header value.
    pub user_id: i64,
    /// Legacy role used to enforce administrator-only routes.
    pub role: i64,
    /// Current durable session id when the operation is session scoped.
    pub session_id: Option<String>,
}

/// Identity boundary owned by the eventual listener integration.
#[async_trait]
pub trait SecurityAuthorizer: Send + Sync {
    /// Resolves an active regular dashboard identity from server-validated credentials.
    async fn user(&self, headers: &HeaderMap) -> Result<SecurityActor, SecurityError>;

    /// Resolves an administrator identity from server-validated credentials.
    async fn admin(&self, headers: &HeaderMap) -> Result<SecurityActor, SecurityError>;

    /// Runs the shared IP-keyed CriticalRateLimit after UserAuth and before a
    /// sensitive JSON body is parsed. Test authorizers default to Allowed;
    /// the production dashboard adapter delegates to the real auth service.
    async fn check_critical_rate_limit(
        &self,
        _client_ip: &str,
    ) -> Result<CriticalRateLimitOutcome, SecurityError> {
        Ok(CriticalRateLimitOutcome::Allowed)
    }

    /// Issues a session-bound proof only after the provider has consumed the
    /// purpose-scoped verification input. Authorizers without the shared auth
    /// authority fail closed.
    async fn issue_security_proof(
        &self,
        _actor: &SecurityActor,
        _method: &str,
        _scopes: &[String],
    ) -> Result<SecurityProof, SecurityError> {
        Err(SecurityError::Unavailable)
    }
}

/// Production authorization adapter backed by the shared dashboard-auth service.
///
/// It accepts only a syntactically valid bearer credential and derives the
/// actor exclusively from the server-side token/session lookup.
#[derive(Clone)]
pub struct DashboardSecurityAuthorizer {
    auth: Arc<dyn DashboardAuth>,
}

impl DashboardSecurityAuthorizer {
    /// Creates an authorizer over the listener's configured dashboard auth service.
    #[must_use]
    pub fn new(auth: Arc<dyn DashboardAuth>) -> Self {
        Self { auth }
    }

    async fn actor(&self, headers: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        let token = credential(headers).ok_or(SecurityError::Unauthorized)?;
        let session_candidate = dashboard_token_candidate(&token);
        let (user, session_id) = if session_candidate {
            // Browser-session routes need the server-owned SID as well as the
            // user projection. `self_user` deliberately omits that sensitive
            // identity, so resolve it through the authoritative session
            // adapter before constructing the actor passed to the provider.
            let session = self
                .auth
                .current_session(SecretString::from(token))
                .await
                .map_err(|error| match error.kind {
                    AuthErrorKind::TokenExpired => SecurityError::TokenExpired,
                    AuthErrorKind::SessionRevoked => SecurityError::SessionRevoked,
                    AuthErrorKind::Unauthorized | AuthErrorKind::InvalidCredentials => {
                        SecurityError::Unauthorized
                    }
                    AuthErrorKind::UserDisabled => SecurityError::UserDisabled,
                    _ => SecurityError::InternalAuth,
                })?;
            (session.user, Some(session.session_id))
        } else {
            let user = self
                .auth
                .self_user(SecretString::from(token))
                .await
                .map_err(|error| match error.kind {
                    AuthErrorKind::TokenExpired => SecurityError::TokenExpired,
                    AuthErrorKind::SessionRevoked => SecurityError::SessionRevoked,
                    AuthErrorKind::Unauthorized | AuthErrorKind::InvalidCredentials => {
                        SecurityError::Unauthorized
                    }
                    AuthErrorKind::UserDisabled => SecurityError::UserDisabled,
                    _ => SecurityError::InternalAuth,
                })?;
            (user, None)
        };
        if user.id <= 0 || user.username.trim().is_empty() || !matches!(user.role, 0 | 1 | 10 | 100)
        {
            return Err(SecurityError::InvalidUserInfo);
        }
        if user.status != 1 {
            return Err(SecurityError::UserDisabled);
        }
        Ok(SecurityActor {
            user_id: user.id,
            role: user.role,
            session_id,
        })
    }
}

#[async_trait]
impl SecurityAuthorizer for DashboardSecurityAuthorizer {
    async fn user(&self, headers: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        self.actor(headers).await
    }

    async fn admin(&self, headers: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        self.actor(headers).await
    }

    async fn check_critical_rate_limit(
        &self,
        client_ip: &str,
    ) -> Result<CriticalRateLimitOutcome, SecurityError> {
        self.auth
            .check_critical_rate_limit(client_ip)
            .await
            .map_err(|_| SecurityError::Unavailable)
    }

    async fn issue_security_proof(
        &self,
        actor: &SecurityActor,
        method: &str,
        scopes: &[String],
    ) -> Result<SecurityProof, SecurityError> {
        let session_id = actor
            .session_id
            .as_deref()
            .filter(|session_id| !session_id.trim().is_empty())
            .ok_or(SecurityError::SessionRequired)?;
        self.auth
            .issue_security_proof(actor.user_id, session_id, method, scopes)
            .await
            .map_err(|error| match error.kind {
                AuthErrorKind::TokenExpired => SecurityError::TokenExpired,
                AuthErrorKind::SessionRevoked => SecurityError::SessionRevoked,
                AuthErrorKind::Unauthorized | AuthErrorKind::InvalidCredentials => {
                    SecurityError::Unauthorized
                }
                AuthErrorKind::UserDisabled => SecurityError::UserDisabled,
                _ => SecurityError::Unavailable,
            })
    }
}

struct RejectingSecurityAuthorizer;

#[async_trait]
impl SecurityAuthorizer for RejectingSecurityAuthorizer {
    async fn user(&self, _: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        Err(SecurityError::Unauthorized)
    }

    async fn admin(&self, _: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        Err(SecurityError::Unauthorized)
    }
}

/// Normalized operation selected only after its required authorization succeeds.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum SecurityOperation {
    AdminClearBinding,
    AdminResetPasskey,
    AuthzCatalog,
    SendPasswordReset,
    PasskeyStatus,
    ListSessions,
    SendEmailVerification,
    VerifyTwoFactorLogin,
    PasskeyLoginBegin,
    PasskeyLoginFinish,
    PasskeyRegisterBegin,
    PasskeyRegisterFinish,
    PasskeyVerifyBegin,
    PasskeyVerifyFinish,
    Register,
    ResetPassword,
    RevokeOtherSessions,
    UniversalVerify,
    DeletePasskey,
    DeleteSession,
}

/// Input delivered to the durable/mail/WebAuthn boundary after validation.
#[derive(Clone, Debug, PartialEq)]
pub struct SecurityCall {
    /// Selected route operation.
    pub operation: SecurityOperation,
    /// Authenticated actor for user/admin routes; anonymous routes have no actor.
    pub actor: Option<SecurityActor>,
    /// Legacy path, query, or JSON data.
    pub input: Value,
}

/// Errors exposed through the frozen legacy JSON envelope.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum SecurityError {
    /// No valid listener-authenticated identity exists.
    Unauthorized,
    /// A recognized dashboard access token has expired.
    TokenExpired,
    /// A recognized dashboard session is no longer valid.
    SessionRevoked,
    /// The authenticated dashboard user is disabled.
    UserDisabled,
    /// The resolved dashboard user has an invalid username or role.
    InvalidUserInfo,
    /// Authentication storage or validation failed internally.
    InternalAuth,
    /// Authentication succeeded, but the shared interface omitted the session id.
    AuthenticatedSessionUnavailable,
    /// The authenticated principal lacks the requested administrator role.
    Forbidden,
    /// The operation requires a listener-verified browser-session identity.
    SessionRequired,
    /// The legacy request shape is invalid.
    Invalid(&'static str),
    /// An external or durable dependency could not safely complete the operation.
    Unavailable,
    /// A known legacy business-rule rejection.
    Rejected(String),
    /// Public registration is disabled by the operator.
    RegistrationDisabled,
    /// Password registration is disabled by the operator.
    PasswordRegistrationDisabled,
    /// Registration cannot proceed until both legal documents are published.
    RegistrationLegalUnavailable,
    /// Registration requires explicit acceptance of both legal documents.
    LegalConsentRequired,
    /// Registration input does not satisfy the legacy validation contract.
    InvalidRegistration,
    /// The requested username or email already belongs to an account.
    RegistrationConflict,
}

impl SecurityError {
    fn response(self, locale: LegacyLocale) -> Response {
        match self {
            Self::Unauthorized => failure(
                StatusCode::UNAUTHORIZED,
                locale.invalid_access_token(),
                Some("AUTH_UNAUTHORIZED"),
            ),
            Self::TokenExpired => failure(
                StatusCode::UNAUTHORIZED,
                locale.not_logged_in(),
                Some("AUTH_TOKEN_EXPIRED"),
            ),
            Self::SessionRevoked => failure(
                StatusCode::UNAUTHORIZED,
                locale.not_logged_in(),
                Some("AUTH_SESSION_REVOKED"),
            ),
            Self::UserDisabled => failure(
                StatusCode::UNAUTHORIZED,
                locale.user_banned(),
                Some("AUTH_USER_DISABLED"),
            ),
            Self::InvalidUserInfo => failure(
                StatusCode::UNAUTHORIZED,
                locale.invalid_user_info(),
                Some("AUTH_USER_INVALID"),
            ),
            Self::InternalAuth => failure(
                StatusCode::INTERNAL_SERVER_ERROR,
                locale.database_error(),
                Some("AUTH_INTERNAL_ERROR"),
            ),
            Self::AuthenticatedSessionUnavailable => failure(
                StatusCode::SERVICE_UNAVAILABLE,
                locale.security_unavailable(),
                None,
            ),
            Self::Forbidden => failure(
                StatusCode::FORBIDDEN,
                locale.insufficient_privilege(),
                Some("AUTH_INSUFFICIENT_PRIVILEGE"),
            ),
            Self::SessionRequired => failure(
                StatusCode::FORBIDDEN,
                "a dashboard login session is required",
                Some("AUTH_SESSION_REQUIRED"),
            ),
            Self::Invalid(message) => failure(StatusCode::OK, message, None),
            Self::Unavailable => failure(
                StatusCode::SERVICE_UNAVAILABLE,
                locale.security_unavailable(),
                None,
            ),
            Self::Rejected(message) => failure(StatusCode::OK, &message, None),
            Self::RegistrationDisabled => {
                failure(StatusCode::OK, "注册功能已关闭", Some("REGISTER_DISABLED"))
            }
            Self::PasswordRegistrationDisabled => failure(
                StatusCode::OK,
                "管理员关闭了密码注册",
                Some("PASSWORD_REGISTER_DISABLED"),
            ),
            Self::RegistrationLegalUnavailable => failure(
                StatusCode::SERVICE_UNAVAILABLE,
                "Registration is unavailable until the user agreement and privacy policy are published.",
                Some("REGISTRATION_LEGAL_UNAVAILABLE"),
            ),
            Self::LegalConsentRequired => failure(
                StatusCode::UNPROCESSABLE_ENTITY,
                "You must accept the user agreement and privacy policy to register.",
                Some("LEGAL_CONSENT_REQUIRED"),
            ),
            Self::InvalidRegistration => failure(StatusCode::OK, locale.invalid_params(), None),
            Self::RegistrationConflict => {
                failure(StatusCode::OK, "用户名或邮箱已存在", Some("USER_EXISTS"))
            }
        }
    }
}

/// Boundary for password-reset mail, session storage, and WebAuthn operations.
///
/// Production adapters must perform their protocol-specific verification here;
/// a handler never converts a browser assertion into a success response itself.
#[async_trait]
pub trait SecurityProvider: Send + Sync {
    /// Executes a normalized operation after route-level authorization and validation.
    async fn execute(&self, call: SecurityCall) -> Result<Value, SecurityError>;
}

/// Deterministic in-memory fake for route tests.
///
/// Its default result is [`SecurityError::Unavailable`], which ensures that a
/// test harness cannot accidentally claim a mail or WebAuthn security success.
pub struct MemorySecurityProvider {
    result: Mutex<Result<Value, SecurityError>>,
    calls: Mutex<Vec<SecurityCall>>,
}

impl Default for MemorySecurityProvider {
    fn default() -> Self {
        Self::new(Err(SecurityError::Unavailable))
    }
}

impl MemorySecurityProvider {
    /// Creates a fake with an explicit result suitable for one isolated test.
    #[must_use]
    pub fn new(result: Result<Value, SecurityError>) -> Self {
        Self {
            result: Mutex::new(result),
            calls: Mutex::new(Vec::new()),
        }
    }

    /// Returns calls recorded by the fake, or an error if a test poisoned its lock.
    pub fn calls(&self) -> Result<Vec<SecurityCall>, SecurityError> {
        self.calls
            .lock()
            .map(|calls| calls.clone())
            .map_err(|_| SecurityError::Unavailable)
    }
}

#[async_trait]
impl SecurityProvider for MemorySecurityProvider {
    async fn execute(&self, call: SecurityCall) -> Result<Value, SecurityError> {
        self.calls
            .lock()
            .map_err(|_| SecurityError::Unavailable)?
            .push(call);
        self.result
            .lock()
            .map_err(|_| SecurityError::Unavailable)?
            .clone()
    }
}

/// PostgreSQL/Valkey implementation for the durable security operations.
///
/// WebAuthn ceremony validation and outbound mail deliberately remain outside
/// this adapter: accepting a browser assertion or claiming a message was sent
/// without a configured standards-compliant boundary would be a security bug.
/// Universal email verification is the exception: it consumes the shared
/// purpose-scoped Valkey code here and delegates proof signing to the shared
/// session authority.
#[derive(Clone)]
pub struct PgValkeySecurityProvider {
    pool: PgPool,
    _valkey: redis::Client,
    verification_codes: ValkeyVerificationCodeStore,
}

fn preferred_security_method_for(
    normalized_email: &str,
    two_factor_enabled: bool,
) -> (String, Option<String>) {
    if !normalized_email.is_empty() {
        return ("email".to_owned(), Some(normalized_email.to_owned()));
    }
    if two_factor_enabled {
        return ("2fa".to_owned(), None);
    }
    ("passkey".to_owned(), None)
}

impl PgValkeySecurityProvider {
    #[must_use]
    pub fn new(pool: PgPool, valkey: redis::Client) -> Self {
        Self {
            pool,
            verification_codes: ValkeyVerificationCodeStore::new(valkey.clone()),
            _valkey: valkey,
        }
    }

    fn actor(call: &SecurityCall) -> Result<&SecurityActor, SecurityError> {
        call.actor.as_ref().ok_or(SecurityError::Unauthorized)
    }

    fn admin_actor(call: &SecurityCall) -> Result<&SecurityActor, SecurityError> {
        let actor = Self::actor(call)?;
        (actor.role >= ADMIN_ROLE)
            .then_some(actor)
            .ok_or(SecurityError::Forbidden)
    }

    async fn passkey_status(&self, actor: &SecurityActor) -> Result<Value, SecurityError> {
        let row = sqlx::query(
            "SELECT to_char( \
                 last_used_at AT TIME ZONE 'UTC', \
                 'YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"' \
             ) AS last_used_at FROM passkey_credentials \
             WHERE user_id = $1 AND deleted_at IS NULL LIMIT 1",
        )
        .bind(actor.user_id)
        .fetch_optional(&self.pool)
        .await
        .map_err(|_| SecurityError::Unavailable)?;
        let Some(row) = row else {
            return Ok(json!({"enabled": false}));
        };
        let last_used_at = row
            .try_get::<Option<String>, _>("last_used_at")
            .map_err(|_| SecurityError::Unavailable)?
            .map(|value| {
                DateTime::parse_from_rfc3339(&value)
                    .map(|timestamp| {
                        timestamp
                            .with_timezone(&Utc)
                            .to_rfc3339_opts(SecondsFormat::AutoSi, true)
                    })
                    .map_err(|_| SecurityError::Unavailable)
            })
            .transpose()?;
        Ok(json!({
            "enabled": true,
            "last_used_at": last_used_at,
        }))
    }

    async fn list_sessions(&self, actor: &SecurityActor) -> Result<Value, SecurityError> {
        let now = unix_seconds()?;
        let rows = sqlx::query(
            "SELECT sid, login_method, ip, user_agent, created_at, last_active_at, expires_at \
             FROM user_sessions WHERE user_id = $1 AND status = 'active' AND revoked_at = 0 \
             AND expires_at > $2 ORDER BY created_at DESC LIMIT 100",
        )
        .bind(actor.user_id)
        .bind(now)
        .fetch_all(&self.pool)
        .await
        .map_err(|_| SecurityError::Unavailable)?;
        let sessions = rows
            .into_iter()
            .map(|row| {
                let sid = row
                    .try_get::<String, _>("sid")
                    .map_err(|_| SecurityError::Unavailable)?;
                let current = actor.session_id.as_deref() == Some(sid.as_str());
                Ok(json!({
                    "sid": sid,
                    "current": current,
                    "login_method": row.try_get::<String, _>("login_method").map_err(|_| SecurityError::Unavailable)?,
                    "ip": row.try_get::<Option<String>, _>("ip").map_err(|_| SecurityError::Unavailable)?,
                    "user_agent": row.try_get::<Option<String>, _>("user_agent").map_err(|_| SecurityError::Unavailable)?,
                    "created_at": row.try_get::<i64, _>("created_at").map_err(|_| SecurityError::Unavailable)?,
                    "last_active_at": row.try_get::<i64, _>("last_active_at").map_err(|_| SecurityError::Unavailable)?,
                    "expires_at": row.try_get::<i64, _>("expires_at").map_err(|_| SecurityError::Unavailable)?,
                }))
            })
            .collect::<Result<Vec<_>, SecurityError>>()?;
        Ok(Value::Array(sessions))
    }

    async fn preferred_security_method(
        &self,
        actor: &SecurityActor,
    ) -> Result<(String, Option<String>), SecurityError> {
        let email = sqlx::query_scalar::<_, String>(
            "SELECT COALESCE(email, '') FROM users WHERE id = $1 AND deleted_at IS NULL",
        )
        .bind(actor.user_id)
        .fetch_optional(&self.pool)
        .await
        .map_err(|_| SecurityError::Unavailable)?
        .ok_or(SecurityError::Unauthorized)?;
        let email = email.trim().to_ascii_lowercase();
        if !email.is_empty() {
            return Ok(preferred_security_method_for(&email, false));
        }
        let two_factor_enabled = sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS(SELECT 1 FROM two_fas \
             WHERE user_id = $1 AND is_enabled = TRUE AND deleted_at IS NULL)",
        )
        .bind(actor.user_id)
        .fetch_one(&self.pool)
        .await
        .map_err(|_| SecurityError::Unavailable)?;
        Ok(preferred_security_method_for("", two_factor_enabled))
    }

    async fn universal_verify(&self, call: SecurityCall) -> Result<Value, SecurityError> {
        let actor = Self::actor(&call)?;
        let method = call
            .input
            .get("method")
            .and_then(Value::as_str)
            .map(str::trim)
            .filter(|method| !method.is_empty() && method.len() <= 32)
            .ok_or(SecurityError::Invalid("参数错误"))?;
        let scope = call
            .input
            .get("scope")
            .and_then(Value::as_str)
            .map(str::trim)
            .filter(|scope| !scope.is_empty() && scope.len() <= 128)
            .ok_or(SecurityError::Invalid("不支持的安全验证范围"))?;
        if !matches!(
            scope,
            "channel.key.read"
                | "passkey.register"
                | "passkey.delete"
                | "security.review_runs.delete"
        ) {
            return Err(SecurityError::Invalid("不支持的安全验证范围"));
        }

        let (preferred_method, email) = self.preferred_security_method(actor).await?;
        if method != preferred_method {
            return Err(SecurityError::Rejected(
                "请优先使用邮箱验证；未绑定邮箱时请使用已启用的 2FA，否则使用 Passkey 验证"
                    .to_owned(),
            ));
        }
        match method {
            "email" => {
                let code = call
                    .input
                    .get("code")
                    .and_then(Value::as_str)
                    .map(str::trim)
                    .filter(|code| !code.is_empty() && code.len() <= 64)
                    .ok_or(SecurityError::Invalid("验证码不能为空"))?;
                let email = email.ok_or(SecurityError::Unavailable)?;
                let valid = self
                    .verification_codes
                    .verify_and_consume(&email, code, "s")
                    .await
                    .map_err(|_| SecurityError::Unavailable)?;
                if !valid {
                    return Err(SecurityError::Rejected(
                        "验证失败，请检查邮箱验证码".to_owned(),
                    ));
                }
            }
            "2fa" => {
                let code = call
                    .input
                    .get("code")
                    .and_then(Value::as_str)
                    .map(str::trim)
                    .filter(|code| !code.is_empty() && code.len() <= 64)
                    .ok_or(SecurityError::Invalid("验证码不能为空"))?;
                let mut transaction = self
                    .pool
                    .begin()
                    .await
                    .map_err(|_| SecurityError::Unavailable)?;
                match verify_critical_mutation_factor(&mut transaction, actor.user_id, code)
                    .await
                    .map_err(|_| SecurityError::Unavailable)?
                {
                    CriticalTwoFactorVerification::Verified => transaction
                        .commit()
                        .await
                        .map_err(|_| SecurityError::Unavailable)?,
                    CriticalTwoFactorVerification::Rejected => {
                        transaction
                            .commit()
                            .await
                            .map_err(|_| SecurityError::Unavailable)?;
                        return Err(SecurityError::Rejected(
                            "验证失败，请检查两步验证码或备用码".to_owned(),
                        ));
                    }
                    CriticalTwoFactorVerification::NotRequired => {
                        return Err(SecurityError::Rejected(
                            "两步验证未启用，请重新选择安全验证方式".to_owned(),
                        ));
                    }
                }
            }
            "passkey" => {
                return Err(SecurityError::Rejected(
                    "Passkey 验证必须使用 Passkey verify 流程".to_owned(),
                ));
            }
            _ => {
                return Err(SecurityError::Rejected("不支持的安全验证方式".to_owned()));
            }
        }
        Ok(json!({"method": method, "scope": scope}))
    }

    async fn option(&self, key: &str) -> Result<Option<String>, SecurityError> {
        sqlx::query_scalar::<_, Option<String>>("SELECT value FROM options WHERE key = $1")
            .bind(key)
            .fetch_optional(&self.pool)
            .await
            .map(|value| value.flatten())
            .map_err(|_| SecurityError::Unavailable)
    }

    async fn option_bool(&self, key: &str, default: bool) -> Result<bool, SecurityError> {
        let value = self.option(key).await?;
        Ok(match value.as_deref() {
            Some(value) => value.trim().eq_ignore_ascii_case("true"),
            None => default,
        })
    }

    async fn register(&self, input: Value) -> Result<Value, SecurityError> {
        if !self.option_bool("RegisterEnabled", true).await? {
            return Err(SecurityError::RegistrationDisabled);
        }
        if !self.option_bool("PasswordRegisterEnabled", true).await? {
            return Err(SecurityError::PasswordRegistrationDisabled);
        }

        // Go decodes into an embedded zero-valued User before applying the
        // public legal gate. Keep this order so malformed-but-decodable input
        // cannot bypass the operator's registration stop.
        let request: RegistrationInput =
            serde_json::from_value(input).map_err(|_| SecurityError::InvalidRegistration)?;
        let agreement = self.option("legal.user_agreement").await?;
        let privacy = self.option("legal.privacy_policy").await?;
        if agreement
            .as_deref()
            .is_none_or(|value| value.trim().is_empty())
            || privacy
                .as_deref()
                .is_none_or(|value| value.trim().is_empty())
        {
            return Err(SecurityError::RegistrationLegalUnavailable);
        }
        if !request.accepted_legal {
            return Err(SecurityError::LegalConsentRequired);
        }
        let username = request.username.trim().to_owned();
        let password = request.password;
        let email = match request.email {
            Some(email) => email.trim().to_ascii_lowercase(),
            None => String::new(),
        };
        if username.is_empty()
            || username.chars().count() > 20
            || username.chars().any(char::is_control)
            || password.len() < 8
            || password.len() > 20
            || (!email.is_empty() && email.chars().count() > 50)
        {
            return Err(SecurityError::InvalidRegistration);
        }
        if self.option_bool("EmailVerificationEnabled", false).await? {
            // Verification-code delivery/confirmation is not part of this mounted
            // slice yet.  Refuse the write when the legacy flag is enabled rather
            // than creating an account that bypasses the configured gate.
            return Err(SecurityError::Unavailable);
        }

        let password_hash = tokio::task::spawn_blocking(move || hash(password, DEFAULT_COST))
            .await
            .map_err(|_| SecurityError::Unavailable)?
            .map_err(|_| SecurityError::Unavailable)?;
        let now = unix_seconds()?;
        let mut transaction = self
            .pool
            .begin()
            .await
            .map_err(|_| SecurityError::Unavailable)?;
        let existing = sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS(SELECT 1 FROM users WHERE deleted_at IS NULL AND (username = $1 OR ($2 <> '' AND lower(COALESCE(email, '')) = $2)))",
        )
        .bind(&username)
        .bind(&email)
        .fetch_one(&mut *transaction)
        .await
        .map_err(|_| SecurityError::Unavailable)?;
        if existing {
            return Err(SecurityError::RegistrationConflict);
        }

        let inviter_id = if request.aff_code.trim().is_empty() {
            0_i64
        } else {
            let inviter = sqlx::query_scalar::<_, Option<i64>>(
                "SELECT id FROM users WHERE deleted_at IS NULL AND aff_code = $1 LIMIT 1",
            )
            .bind(request.aff_code.trim())
            .fetch_optional(&mut *transaction)
            .await
            .map_err(|_| SecurityError::Unavailable)?
            .flatten();
            inviter.unwrap_or_default()
        };

        let aff_code = new_aff_code();
        let inserted = sqlx::query(
            "INSERT INTO users (username, password, display_name, role, status, email, \"group\", aff_code, inviter_id, quota, used_quota, request_count, created_at, auth_version) VALUES ($1, $2, $1, 1, 1, $3, 'default', $4, $5, 0, 0, 0, $6, 1)",
        )
        .bind(&username)
        .bind(&password_hash)
        .bind(&email)
        .bind(&aff_code)
        .bind(inviter_id)
        .bind(now)
        .execute(&mut *transaction)
        .await;
        match inserted {
            Ok(_) => transaction
                .commit()
                .await
                .map_err(|_| SecurityError::Unavailable)?,
            Err(error)
                if error
                    .as_database_error()
                    .is_some_and(|db| db.code().as_deref() == Some("23505")) =>
            {
                return Err(SecurityError::RegistrationConflict);
            }
            Err(_) => return Err(SecurityError::Unavailable),
        }
        Ok(Value::Null)
    }
}

#[derive(Debug, Deserialize)]
struct RegistrationInput {
    #[serde(default)]
    username: String,
    #[serde(default)]
    password: String,
    #[serde(default)]
    email: Option<String>,
    #[serde(default)]
    aff_code: String,
    #[serde(default)]
    accepted_legal: bool,
}

fn new_aff_code() -> String {
    let mut bytes = [0_u8; 8];
    rng().fill_bytes(&mut bytes);
    const ALPHABET: &[u8] = b"23456789ABCDEFGHJKLMNPQRSTUVWXYZ";
    bytes
        .into_iter()
        .map(|byte| ALPHABET[(byte as usize) % ALPHABET.len()] as char)
        .collect()
}

#[async_trait]
impl SecurityProvider for PgValkeySecurityProvider {
    async fn execute(&self, call: SecurityCall) -> Result<Value, SecurityError> {
        match call.operation {
            SecurityOperation::PasskeyStatus => self.passkey_status(Self::actor(&call)?).await,
            SecurityOperation::ListSessions => self.list_sessions(Self::actor(&call)?).await,
            SecurityOperation::DeleteSession => {
                // Go publishes a session-specific deny fence and clears the
                // matching refresh cookie. The candidate has neither boundary.
                Err(SecurityError::Unavailable)
            }
            SecurityOperation::RevokeOtherSessions => {
                // Go publishes per-session deny fences before this mutation.
                // This candidate has no shared session-cache boundary, so a
                // database-only update would leave a revoked session usable.
                Err(SecurityError::Unavailable)
            }
            // Deleting a passkey must consume a scoped 2FA/passkey proof.
            // The listener does not yet supply such a verified proof.
            SecurityOperation::DeletePasskey => Err(SecurityError::Unavailable),
            SecurityOperation::AdminResetPasskey => {
                Self::admin_actor(&call)?;
                // Go writes an explicit admin audit and publishes a deny
                // fence for every session before committing revocation. The
                // candidate has neither boundary, so no partial reset is safe.
                Err(SecurityError::Unavailable)
            }
            SecurityOperation::AdminClearBinding => {
                Self::admin_actor(&call)?;
                Err(SecurityError::Unavailable)
            }
            SecurityOperation::AuthzCatalog => {
                Self::admin_actor(&call)?;
                // The legacy payload is the static authorization registry,
                // not a database table. Do not invent a partial catalog.
                Err(SecurityError::Unavailable)
            }
            // No handler may synthesize success for an external mail/WebAuthn
            // protocol or for a password flow whose proof boundary is absent.
            SecurityOperation::SendPasswordReset
            | SecurityOperation::SendEmailVerification
            | SecurityOperation::VerifyTwoFactorLogin
            | SecurityOperation::PasskeyLoginBegin
            | SecurityOperation::PasskeyLoginFinish
            | SecurityOperation::PasskeyRegisterBegin
            | SecurityOperation::PasskeyRegisterFinish
            | SecurityOperation::PasskeyVerifyBegin
            | SecurityOperation::PasskeyVerifyFinish
            | SecurityOperation::ResetPassword => Err(SecurityError::Unavailable),
            SecurityOperation::UniversalVerify => self.universal_verify(call).await,
            SecurityOperation::Register => self.register(call.input).await,
        }
    }
}

fn unix_seconds() -> Result<i64, SecurityError> {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_secs() as i64)
        .map_err(|_| SecurityError::Unavailable)
}

#[cfg(test)]
mod provider_tests {
    use super::*;

    type TestResult<T = ()> = Result<T, Box<dyn std::error::Error + Send + Sync>>;

    fn test_error(message: impl Into<String>) -> Box<dyn std::error::Error + Send + Sync> {
        Box::new(std::io::Error::other(message.into()))
    }

    fn provider() -> TestResult<PgValkeySecurityProvider> {
        let pool = sqlx::postgres::PgPoolOptions::new()
            .connect_lazy("postgres://unused:unused@127.0.0.1/unused")?;
        let valkey = redis::Client::open("redis://127.0.0.1/")?;
        Ok(PgValkeySecurityProvider::new(pool, valkey))
    }

    #[test]
    fn registration_input_defaults_missing_credentials_like_go() -> TestResult {
        let request: RegistrationInput = serde_json::from_value(json!({})).map_err(|error| {
            test_error(format!(
                "failed to decode zero-valued registration JSON: {error}"
            ))
        })?;
        assert!(request.username.is_empty());
        assert!(request.password.is_empty());
        Ok(())
    }

    #[tokio::test]
    async fn unconfigured_mail_boundary_fails_closed_without_database_or_valkey_io() -> TestResult {
        let outcome = provider()?
            .execute(SecurityCall {
                operation: SecurityOperation::SendEmailVerification,
                actor: None,
                input: json!({"email": "ada@example.test"}),
            })
            .await;
        assert_eq!(outcome, Err(SecurityError::Unavailable));
        Ok(())
    }

    #[tokio::test]
    async fn production_provider_checks_admin_role_before_catalog_storage_access() -> TestResult {
        let outcome = provider()?
            .execute(SecurityCall {
                operation: SecurityOperation::AuthzCatalog,
                actor: Some(SecurityActor {
                    user_id: 7,
                    role: 1,
                    session_id: Some("server-session".to_owned()),
                }),
                input: json!({}),
            })
            .await;
        assert_eq!(outcome, Err(SecurityError::Forbidden));
        Ok(())
    }
}

/// Dependencies for the unmounted identity-security candidate router.
#[derive(Clone)]
pub struct IdentitySecurityState {
    provider: Arc<dyn SecurityProvider>,
    authorizer: Arc<dyn SecurityAuthorizer>,
    registration_security: Option<AnonymousRequestSecurity>,
    /// Legacy `passkey.enabled` setting, when supplied by the listener.
    /// `None` keeps the candidate router's provider-driven test behavior.
    passkey_enabled: Option<bool>,
}

impl IdentitySecurityState {
    /// Creates a candidate router state with an explicit security provider and authorizer.
    #[must_use]
    pub fn new(
        provider: Arc<dyn SecurityProvider>,
        authorizer: Arc<dyn SecurityAuthorizer>,
    ) -> Self {
        Self {
            provider,
            authorizer,
            registration_security: None,
            passkey_enabled: None,
        }
    }

    /// Supplies the listener-owned legacy Passkey feature flag.
    #[must_use]
    pub fn with_passkey_enabled(mut self, enabled: bool) -> Self {
        self.passkey_enabled = Some(enabled);
        self
    }

    /// Creates a state which rejects every authenticated request until listener wiring exists.
    #[must_use]
    pub fn with_rejecting_authorizer(provider: Arc<dyn SecurityProvider>) -> Self {
        Self::new(provider, Arc::new(RejectingSecurityAuthorizer))
    }

    /// Installs the production authorizer backed by the shared dashboard auth service.
    #[must_use]
    pub fn with_dashboard_auth(mut self, auth: Arc<dyn DashboardAuth>) -> Self {
        self.authorizer = Arc::new(DashboardSecurityAuthorizer::new(auth));
        self
    }

    /// Supplies the listener-owned protection required by the anonymous
    /// password-registration endpoint. Without this explicit policy the
    /// registration route fails closed instead of accepting an unprotected
    /// account-creation request.
    #[must_use]
    pub fn with_registration_security(mut self, security: AnonymousRequestSecurity) -> Self {
        self.registration_security = Some(security);
        self
    }
}

/// All frozen account-security route candidates.
pub fn router(state: IdentitySecurityState) -> Router {
    Router::new()
        .route(
            "/api/user/{id}/bindings/{binding_type}",
            delete(admin_clear_binding),
        )
        .route("/api/user/{id}/reset_passkey", delete(admin_reset_passkey))
        .route(
            "/api/user/passkey",
            get(passkey_status).delete(delete_passkey),
        )
        .route("/api/user/sessions/{sid}", delete(delete_session))
        .route("/api/authz/catalog", get(authz_catalog))
        .route("/api/reset_password", get(send_password_reset))
        .route("/api/user/sessions", get(list_sessions))
        .route("/api/verification", get(send_email_verification))
        .route("/api/user/login/2fa", post(verify_two_factor_login))
        .route("/api/user/passkey/login/begin", post(passkey_login_begin))
        .route("/api/user/passkey/login/finish", post(passkey_login_finish))
        .route(
            "/api/user/passkey/register/begin",
            post(passkey_register_begin),
        )
        .route(
            "/api/user/passkey/register/finish",
            post(passkey_register_finish),
        )
        .route("/api/user/passkey/verify/begin", post(passkey_verify_begin))
        .route(
            "/api/user/passkey/verify/finish",
            post(passkey_verify_finish),
        )
        .merge(registration_route())
        .route("/api/user/reset", post(reset_password))
        .route(
            "/api/user/sessions/revoke-others",
            post(revoke_other_sessions),
        )
        .route("/api/verify", post(universal_verify))
        .with_state(state)
}

/// The password-registration route is the one anonymous identity surface that
/// has a complete PostgreSQL implementation. Keep it separate from the
/// remaining account-security candidates. The listener must supply the
/// registration security policy explicitly before accepting account creation.
pub fn registration_router(state: IdentitySecurityState) -> Router {
    let body_limit_bytes = state
        .registration_security
        .as_ref()
        .map_or(MAX_BODY_BYTES, AnonymousRequestSecurity::body_limit_bytes);
    registration_route()
        .layer(DefaultBodyLimit::max(body_limit_bytes))
        .with_state(state)
}

/// Builds only the read-only dashboard session inventory route for the
/// normal-listener compatibility tests. Session mutations remain isolated in
/// the full candidate router.
pub fn sessions_read_router(state: IdentitySecurityState) -> Router {
    Router::new()
        .route(SESSIONS_PATH, get(list_sessions))
        .with_state(state)
}

/// Builds only the authenticated passkey status read. Registration,
/// verification, and deletion remain isolated in the full candidate router.
pub fn passkey_read_router(state: IdentitySecurityState) -> Router {
    Router::new()
        .route(PASSKEY_PATH, get(passkey_status))
        .with_state(state)
}

fn registration_route() -> Router<IdentitySecurityState> {
    Router::new().route("/api/user/register", post(register))
}

#[derive(Serialize)]
struct Envelope<T: Serialize> {
    success: bool,
    message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<T>,
    #[serde(skip_serializing_if = "Option::is_none")]
    code: Option<&'static str>,
}

fn failure(status: StatusCode, message: &str, code: Option<&'static str>) -> Response {
    legacy_json_content_type(
        (
            status,
            Json(Envelope::<()> {
                success: false,
                message: message.to_owned(),
                data: None,
                code,
            }),
        )
            .into_response(),
    )
}

fn success(data: Value) -> Response {
    legacy_json_content_type(
        Json(Envelope {
            success: true,
            message: String::new(),
            data: Some(data),
            code: None,
        })
        .into_response(),
    )
}

fn operation_success(operation: SecurityOperation, data: Value) -> Response {
    if operation == SecurityOperation::Register {
        return legacy_json_content_type(
            Json(json!({"success": true, "message": ""})).into_response(),
        );
    }
    if operation == SecurityOperation::AdminResetPasskey {
        return legacy_json_content_type(
            Json(json!({
                "success": true,
                "message": "Passkey 已重置",
            }))
            .into_response(),
        );
    }
    success(data)
}

fn legacy_json_content_type(mut response: Response) -> Response {
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

fn with_auth_version(mut response: Response) -> Response {
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

fn with_no_store(mut response: Response) -> Response {
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

async fn authenticated_user(
    state: &IdentitySecurityState,
    headers: &HeaderMap,
) -> Result<SecurityActor, Response> {
    let locale = LegacyLocale::from_headers(headers);
    let actor = match state.authorizer.user(headers).await {
        Ok(actor) => actor,
        Err(error) => {
            let credential_was_validated =
                matches!(error, SecurityError::AuthenticatedSessionUnavailable);
            let response = error.response(locale);
            return Err(if credential_was_validated {
                with_auth_version(response)
            } else {
                response
            });
        }
    };
    if actor.user_id <= 0 {
        return Err(SecurityError::Unauthorized.response(locale));
    }
    if actor.role < 1 {
        return Err(SecurityError::Forbidden.response(locale));
    }
    Ok(actor)
}

async fn authenticated_browser_session(
    state: &IdentitySecurityState,
    headers: &HeaderMap,
) -> Result<SecurityActor, Response> {
    let actor = authenticated_user(state, headers).await?;
    match actor.session_id.as_deref() {
        Some(session_id) if !session_id.trim().is_empty() => Ok(actor),
        _ => Err(with_auth_version(
            SecurityError::SessionRequired.response(LegacyLocale::from_headers(headers)),
        )),
    }
}

fn credential(headers: &HeaderMap) -> Option<String> {
    let raw = headers.get(header::AUTHORIZATION)?.to_str().ok()?.trim();
    let mut fields = raw.split_ascii_whitespace();
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

async fn authenticated_admin(
    state: &IdentitySecurityState,
    headers: &HeaderMap,
) -> Result<SecurityActor, Response> {
    let locale = LegacyLocale::from_headers(headers);
    let actor = match state.authorizer.admin(headers).await {
        Ok(actor) => actor,
        Err(error) => {
            let credential_was_validated =
                matches!(error, SecurityError::AuthenticatedSessionUnavailable);
            let response = error.response(locale);
            return Err(if credential_was_validated {
                with_auth_version(response)
            } else {
                response
            });
        }
    };
    if actor.user_id <= 0 {
        return Err(SecurityError::Unauthorized.response(locale));
    }
    if actor.role < ADMIN_ROLE {
        return Err(SecurityError::Forbidden.response(locale));
    }
    Ok(actor)
}

async fn json_after_auth(request: Request, locale: LegacyLocale) -> Result<Value, Box<Response>> {
    let body = to_bytes(request.into_body(), MAX_BODY_BYTES)
        .await
        .map_err(|_| Box::new(SecurityError::Invalid("参数错误").response(locale)))?;
    json_from_body(body, locale)
}

async fn json_after_auth_with_limit(
    request: Request,
    locale: LegacyLocale,
    max_body_bytes: usize,
) -> Result<Value, Box<Response>> {
    let body = to_bytes(request.into_body(), max_body_bytes)
        .await
        .map_err(|_| Box::new(legacy_empty_response(StatusCode::PAYLOAD_TOO_LARGE, None)))?;
    json_from_body(body, locale)
}

fn json_from_body(body: axum::body::Bytes, locale: LegacyLocale) -> Result<Value, Box<Response>> {
    let value: Value = serde_json::from_slice(&body)
        .map_err(|_| Box::new(SecurityError::Invalid("参数错误").response(locale)))?;
    if value.is_object() {
        Ok(value)
    } else {
        Err(Box::new(
            SecurityError::Invalid("参数错误").response(locale),
        ))
    }
}

fn query_after_auth(uri: &Uri, key: &str, locale: LegacyLocale) -> Result<Value, SecurityError> {
    let value = uri
        .query()
        .and_then(|query| {
            query.split('&').find_map(|part| {
                part.split_once('=')
                    .filter(|(name, _)| *name == key)
                    .map(|(_, value)| value)
            })
        })
        .filter(|value| !value.is_empty() && value.len() <= 320);
    value
        .map(|value| json!({key: value}))
        .ok_or(SecurityError::Invalid(locale.invalid_params()))
}

fn single_path_after_auth(uri: &Uri, prefix: &str, name: &str) -> Result<Value, SecurityError> {
    let value = uri
        .path()
        .strip_prefix(prefix)
        .filter(|value| !value.is_empty() && !value.contains('/') && value.len() <= 128);
    value
        .map(|value| json!({name: value}))
        .ok_or(SecurityError::Invalid("参数错误"))
}

fn binding_path_after_auth(uri: &Uri) -> Result<Value, SecurityError> {
    let value = uri
        .path()
        .strip_prefix("/api/user/")
        .and_then(|tail| tail.split_once("/bindings/"));
    let Some((id, binding_type)) = value else {
        return Err(SecurityError::Invalid("参数错误"));
    };
    let id = id.parse::<i64>().ok().filter(|id| *id > 0);
    if binding_type.is_empty() || binding_type.contains('/') || binding_type.len() > 64 {
        return Err(SecurityError::Invalid("参数错误"));
    }
    id.map(|id| json!({"id": id, "binding_type": binding_type}))
        .ok_or(SecurityError::Invalid("参数错误"))
}

async fn execute(
    state: &IdentitySecurityState,
    operation: SecurityOperation,
    actor: Option<SecurityActor>,
    input: Value,
    locale: LegacyLocale,
) -> Response {
    let authenticated = actor.is_some();
    // Gin binds `Verify2FARequest` before entering the legacy handler.  Its
    // required `code` field therefore turns an empty object into the legacy
    // HTTP-200 parameter-error envelope, even though the durable 2FA
    // provider is unavailable in this candidate.  Preserve that observable
    // validation boundary instead of leaking a provider 503 for malformed
    // requests.
    if operation == SecurityOperation::VerifyTwoFactorLogin
        && input
            .get("code")
            .and_then(Value::as_str)
            .is_none_or(|code| code.is_empty())
    {
        let response = SecurityError::Invalid("参数错误").response(locale);
        return if authenticated {
            with_auth_version(response)
        } else {
            response
        };
    }
    // `ResetPassword` decodes the legacy request before checking the
    // verification code.  Missing email/token fields therefore produce the
    // HTTP-200 locale-specific invalid-parameters envelope rather than
    // reaching the unavailable mail/credential boundary.
    if operation == SecurityOperation::ResetPassword
        && !(input
            .get("email")
            .and_then(Value::as_str)
            .is_some_and(|email| !email.is_empty())
            && input
                .get("token")
                .and_then(Value::as_str)
                .is_some_and(|token| !token.is_empty()))
    {
        let response = SecurityError::Invalid(locale.invalid_params()).response(locale);
        return if authenticated {
            with_auth_version(response)
        } else {
            response
        };
    }
    let response = match state
        .provider
        .execute(SecurityCall {
            operation,
            actor,
            input,
        })
        .await
    {
        Ok(data) => operation_success(operation, data),
        Err(error) => error.response(locale),
    };
    if authenticated {
        with_auth_version(response)
    } else {
        response
    }
}

async fn admin_clear_binding(
    State(state): State<IdentitySecurityState>,
    request: Request,
) -> Response {
    let locale = LegacyLocale::from_headers(request.headers());
    let actor = match authenticated_admin(&state, request.headers()).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let input = match binding_path_after_auth(request.uri()) {
        Ok(input) => input,
        Err(error) => return with_auth_version(error.response(locale)),
    };
    execute(
        &state,
        SecurityOperation::AdminClearBinding,
        Some(actor),
        input,
        locale,
    )
    .await
}

async fn admin_reset_passkey(
    State(state): State<IdentitySecurityState>,
    request: Request,
) -> Response {
    let locale = LegacyLocale::from_headers(request.headers());
    let actor = match authenticated_admin(&state, request.headers()).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let Some(id) = request
        .uri()
        .path()
        .strip_prefix("/api/user/")
        .and_then(|tail| tail.strip_suffix("/reset_passkey"))
        .filter(|id| !id.contains('/'))
        .and_then(|id| id.parse::<i64>().ok())
        .filter(|id| *id > 0)
    else {
        return with_auth_version(SecurityError::Invalid("无效的用户 ID").response(locale));
    };
    execute(
        &state,
        SecurityOperation::AdminResetPasskey,
        Some(actor),
        json!({"id": id}),
        locale,
    )
    .await
}

async fn authz_catalog(State(state): State<IdentitySecurityState>, request: Request) -> Response {
    let locale = LegacyLocale::from_headers(request.headers());
    let actor = match authenticated_admin(&state, request.headers()).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    execute(
        &state,
        SecurityOperation::AuthzCatalog,
        Some(actor),
        json!({}),
        locale,
    )
    .await
}
async fn passkey_status(State(state): State<IdentitySecurityState>, request: Request) -> Response {
    let locale = LegacyLocale::from_headers(request.headers());
    let actor = match authenticated_user(&state, request.headers()).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    execute(
        &state,
        SecurityOperation::PasskeyStatus,
        Some(actor),
        json!({}),
        locale,
    )
    .await
}
async fn list_sessions(State(state): State<IdentitySecurityState>, request: Request) -> Response {
    let locale = LegacyLocale::from_headers(request.headers());
    let actor = match authenticated_browser_session(&state, request.headers()).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    with_no_store(
        execute(
            &state,
            SecurityOperation::ListSessions,
            Some(actor),
            json!({}),
            locale,
        )
        .await,
    )
}
async fn delete_passkey(State(state): State<IdentitySecurityState>, request: Request) -> Response {
    let locale = LegacyLocale::from_headers(request.headers());
    let actor = match authenticated_browser_session(&state, request.headers()).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    with_no_store(
        execute(
            &state,
            SecurityOperation::DeletePasskey,
            Some(actor),
            json!({}),
            locale,
        )
        .await,
    )
}
async fn delete_session(State(state): State<IdentitySecurityState>, request: Request) -> Response {
    let locale = LegacyLocale::from_headers(request.headers());
    let actor = match authenticated_browser_session(&state, request.headers()).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let input = match single_path_after_auth(request.uri(), "/api/user/sessions/", "sid") {
        Ok(input) => input,
        Err(error) => return with_no_store(with_auth_version(error.response(locale))),
    };
    with_no_store(
        execute(
            &state,
            SecurityOperation::DeleteSession,
            Some(actor),
            input,
            locale,
        )
        .await,
    )
}
async fn send_password_reset(
    State(state): State<IdentitySecurityState>,
    request: Request,
) -> Response {
    let locale = LegacyLocale::from_headers(request.headers());
    let input = match query_after_auth(request.uri(), "email", locale) {
        Ok(input) => input,
        Err(error) => return error.response(locale),
    };
    execute(
        &state,
        SecurityOperation::SendPasswordReset,
        None,
        input,
        locale,
    )
    .await
}
async fn send_email_verification(
    State(state): State<IdentitySecurityState>,
    request: Request,
) -> Response {
    let locale = LegacyLocale::from_headers(request.headers());
    let input = match query_after_auth(request.uri(), "email", locale) {
        Ok(input) => input,
        Err(error) => return error.response(locale),
    };
    execute(
        &state,
        SecurityOperation::SendEmailVerification,
        None,
        input,
        locale,
    )
    .await
}

async fn anonymous_json(
    State(state): State<IdentitySecurityState>,
    request: Request,
    operation: SecurityOperation,
) -> Response {
    let locale = LegacyLocale::from_headers(request.headers());
    let input = match json_after_auth(request, locale).await {
        Ok(input) => input,
        Err(response) => return *response,
    };
    execute(&state, operation, None, input, locale).await
}
async fn user_json(
    State(state): State<IdentitySecurityState>,
    request: Request,
    operation: SecurityOperation,
) -> Response {
    let locale = LegacyLocale::from_headers(request.headers());
    let actor = match authenticated_browser_session(&state, request.headers()).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let input = match json_after_auth(request, locale).await {
        Ok(input) => input,
        Err(response) => return with_no_store(with_auth_version(*response)),
    };
    with_no_store(execute(&state, operation, Some(actor), input, locale).await)
}

async fn verify_two_factor_login(
    state: State<IdentitySecurityState>,
    request: Request,
) -> Response {
    with_no_store(anonymous_json(state, request, SecurityOperation::VerifyTwoFactorLogin).await)
}
async fn passkey_login_begin(state: State<IdentitySecurityState>, request: Request) -> Response {
    if state.passkey_enabled == Some(false) {
        let response = failure(StatusCode::OK, "管理员未启用 Passkey 登录", None);
        return with_no_store(response);
    }
    with_no_store(anonymous_json(state, request, SecurityOperation::PasskeyLoginBegin).await)
}
async fn passkey_login_finish(state: State<IdentitySecurityState>, request: Request) -> Response {
    if state.passkey_enabled == Some(false) {
        let response = failure(StatusCode::OK, "管理员未启用 Passkey 登录", None);
        return with_no_store(response);
    }
    with_no_store(anonymous_json(state, request, SecurityOperation::PasskeyLoginFinish).await)
}
fn request_client_ip(request: &Request) -> String {
    if let Some(key) = request.extensions().get::<ClientIpKey>() {
        return key.0.clone();
    }
    if let Some(ip) = request
        .extensions()
        .get::<RequestContext>()
        .and_then(|context| context.client_ip)
    {
        return ip.to_string();
    }
    "unknown".to_owned()
}

async fn register(state: State<IdentitySecurityState>, request: Request) -> Response {
    let locale = LegacyLocale::from_headers(request.headers());
    let Some(security) = state.registration_security.as_ref() else {
        return SecurityError::Unavailable.response(locale);
    };
    let client_ip = request_client_ip(&request);
    match security.check_critical_rate_limit(&client_ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => {}
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => {
            return legacy_empty_response(StatusCode::TOO_MANY_REQUESTS, Some(retry_after_seconds));
        }
        Err(_) => return legacy_empty_response(StatusCode::INTERNAL_SERVER_ERROR, None),
    }
    match security.check_turnstile(request.uri(), &client_ip).await {
        TurnstileCheckOutcome::Disabled | TurnstileCheckOutcome::Allowed => {}
        TurnstileCheckOutcome::MissingToken => return turnstile_missing_response(),
        TurnstileCheckOutcome::Rejected => return turnstile_failure_response(),
    }
    let input = match json_after_auth_with_limit(request, locale, security.body_limit_bytes()).await
    {
        Ok(input) => input,
        Err(response) => return *response,
    };
    execute(&state, SecurityOperation::Register, None, input, locale).await
}
async fn reset_password(state: State<IdentitySecurityState>, request: Request) -> Response {
    anonymous_json(state, request, SecurityOperation::ResetPassword).await
}
async fn passkey_register_begin(state: State<IdentitySecurityState>, request: Request) -> Response {
    user_json(state, request, SecurityOperation::PasskeyRegisterBegin).await
}
async fn passkey_register_finish(
    state: State<IdentitySecurityState>,
    request: Request,
) -> Response {
    user_json(state, request, SecurityOperation::PasskeyRegisterFinish).await
}
async fn passkey_verify_begin(state: State<IdentitySecurityState>, request: Request) -> Response {
    user_json(state, request, SecurityOperation::PasskeyVerifyBegin).await
}
async fn passkey_verify_finish(state: State<IdentitySecurityState>, request: Request) -> Response {
    user_json(state, request, SecurityOperation::PasskeyVerifyFinish).await
}
async fn revoke_other_sessions(state: State<IdentitySecurityState>, request: Request) -> Response {
    let locale = LegacyLocale::from_headers(request.headers());
    let actor = match authenticated_browser_session(&state, request.headers()).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    with_no_store(
        execute(
            &state,
            SecurityOperation::RevokeOtherSessions,
            Some(actor),
            json!({}),
            locale,
        )
        .await,
    )
}
async fn universal_verify(state: State<IdentitySecurityState>, request: Request) -> Response {
    let locale = LegacyLocale::from_headers(request.headers());
    let actor = match authenticated_browser_session(&state, request.headers()).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let client_ip = request_client_ip(&request);
    match state.authorizer.check_critical_rate_limit(&client_ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => {}
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => {
            return with_auth_version(legacy_empty_response(
                StatusCode::TOO_MANY_REQUESTS,
                Some(retry_after_seconds),
            ));
        }
        Err(_) => {
            return with_auth_version(legacy_empty_response(
                StatusCode::INTERNAL_SERVER_ERROR,
                None,
            ));
        }
    }
    let input = match json_after_auth(request, locale).await {
        Ok(input) => input,
        Err(response) => return with_no_store(with_auth_version(*response)),
    };
    let method_and_scope = match state
        .provider
        .execute(SecurityCall {
            operation: SecurityOperation::UniversalVerify,
            actor: Some(actor.clone()),
            input,
        })
        .await
    {
        Ok(data) => data,
        Err(error) => return with_no_store(with_auth_version(error.response(locale))),
    };
    let Some(method) = method_and_scope.get("method").and_then(Value::as_str) else {
        return with_no_store(with_auth_version(
            SecurityError::Unavailable.response(locale),
        ));
    };
    let Some(scope) = method_and_scope.get("scope").and_then(Value::as_str) else {
        return with_no_store(with_auth_version(
            SecurityError::Unavailable.response(locale),
        ));
    };
    let scopes = vec![scope.to_owned()];
    let proof = match state
        .authorizer
        .issue_security_proof(&actor, method, &scopes)
        .await
    {
        Ok(proof) => proof,
        Err(error) => return with_no_store(with_auth_version(error.response(locale))),
    };
    let response = legacy_json_content_type(
        Json(json!({
            "success": true,
            "message": "验证成功",
            "data": {
                "proof_token": proof.token,
                "expires_at": proof.expires_at,
                "method": method,
                "scope": scope,
            },
        }))
        .into_response(),
    );
    with_no_store(with_auth_version(response))
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::body::Body;

    type TestResult<T = ()> = Result<T, Box<dyn std::error::Error + Send + Sync>>;

    fn test_error(message: impl Into<String>) -> Box<dyn std::error::Error + Send + Sync> {
        Box::new(std::io::Error::other(message.into()))
    }

    #[test]
    fn preferred_security_method_matches_email_two_factor_passkey_order() -> TestResult {
        assert_eq!(
            preferred_security_method_for("admin@example.com", true),
            ("email".to_owned(), Some("admin@example.com".to_owned()))
        );
        assert_eq!(
            preferred_security_method_for("", true),
            ("2fa".to_owned(), None)
        );
        assert_eq!(
            preferred_security_method_for("", false),
            ("passkey".to_owned(), None)
        );
        Ok(())
    }

    #[tokio::test]
    async fn registration_json_limit_returns_413_before_json_parsing() -> TestResult {
        let request = Request::builder().body(Body::from("x".repeat(17)))?;
        let response = match json_after_auth_with_limit(request, LegacyLocale::En, 16).await {
            Ok(_) => return Err(test_error("oversized request was accepted")),
            Err(response) => response,
        };

        assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
        Ok(())
    }
}
