use super::{
    AuthBundle, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth, LoginOutcome,
    LoginRequest, LogoutRequest, REFRESH_COOKIE_NAME, RequestMetadata, TwoFactorLoginRequest,
    UserAuthPolicyError, enforce_user_auth_view,
};
use crate::{ClientIpKey, RequestContext, legacy_empty_response};
use async_trait::async_trait;
use axum::{
    Json, Router,
    body::{Body, Bytes},
    extract::{DefaultBodyLimit, FromRequest, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, Uri, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use secrecy::{ExposeSecret, SecretString};
use serde::Serialize;
use std::{
    sync::Arc,
    time::{Duration, UNIX_EPOCH},
};
use subtle::ConstantTimeEq;

const TURNSTILE_SITEVERIFY_ENDPOINT: &str =
    "https://challenges.cloudflare.com/turnstile/v0/siteverify";
const TURNSTILE_TIMEOUT: Duration = Duration::from_secs(3);

#[async_trait]
pub trait TurnstileVerifier: Send + Sync {
    async fn verify(&self, token: &str, remote_ip: &str) -> bool;
}

/// Security policy shared by anonymous, credential-creating endpoints.
///
/// The policy keeps the rate-limit, request-size, and Turnstile configuration
/// bound to the same listener-owned [`DashboardAuth`] instance.  Consumers
/// must opt in explicitly; an endpoint without this policy must fail closed.
#[derive(Clone)]
pub struct AnonymousRequestSecurity {
    auth: Arc<dyn DashboardAuth>,
    body_limit_bytes: usize,
    turnstile_check_enabled: bool,
    turnstile_verifier: Option<Arc<dyn TurnstileVerifier>>,
}

/// Result of applying the configured Turnstile policy to one request.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum TurnstileCheckOutcome {
    /// The operator has not enabled Turnstile checks.
    Disabled,
    /// Turnstile is enabled but the client did not supply a challenge token.
    MissingToken,
    /// The verifier rejected the token or was unavailable.
    Rejected,
    /// The verifier accepted the token.
    Allowed,
}

impl AnonymousRequestSecurity {
    /// Returns the maximum accepted anonymous request size.
    #[must_use]
    pub const fn body_limit_bytes(&self) -> usize {
        self.body_limit_bytes
    }

    /// Applies the listener's critical rate-limit policy.
    pub async fn check_critical_rate_limit(
        &self,
        client_ip: &str,
    ) -> Result<CriticalRateLimitOutcome, AuthError> {
        self.auth.check_critical_rate_limit(client_ip).await
    }

    /// Applies the listener's configured Turnstile policy and fails closed on
    /// a missing verifier, an upstream error, or an invalid token.
    pub async fn check_turnstile(&self, uri: &Uri, client_ip: &str) -> TurnstileCheckOutcome {
        if !self.turnstile_check_enabled {
            return TurnstileCheckOutcome::Disabled;
        }
        let Some(token) = turnstile_token(uri) else {
            return TurnstileCheckOutcome::MissingToken;
        };
        let Some(verifier) = self.turnstile_verifier.as_ref() else {
            return TurnstileCheckOutcome::Rejected;
        };
        if verifier.verify(&token, client_ip).await {
            TurnstileCheckOutcome::Allowed
        } else {
            TurnstileCheckOutcome::Rejected
        }
    }
}

struct CloudflareTurnstileVerifier {
    client: Option<reqwest::Client>,
    secret: Option<SecretString>,
}

impl CloudflareTurnstileVerifier {
    fn new(secret_key: Option<String>) -> Self {
        let secret = secret_key
            .filter(|secret| !secret.trim().is_empty())
            .map(SecretString::from);
        let client = crate::outbound_http::client(TURNSTILE_TIMEOUT).ok();
        Self { client, secret }
    }
}

#[derive(serde::Deserialize)]
struct TurnstileCheckResponse {
    success: bool,
}

#[async_trait]
impl TurnstileVerifier for CloudflareTurnstileVerifier {
    async fn verify(&self, token: &str, remote_ip: &str) -> bool {
        if token.is_empty() || remote_ip.is_empty() {
            return false;
        }
        let (Some(client), Some(secret)) = (&self.client, &self.secret) else {
            return false;
        };
        let response = client
            .post(TURNSTILE_SITEVERIFY_ENDPOINT)
            .form(&[
                ("secret", secret.expose_secret()),
                ("response", token),
                ("remoteip", remote_ip),
            ])
            .send()
            .await;
        let Ok(response) = response else {
            return false;
        };
        if !response.status().is_success() {
            return false;
        }
        let Ok(body) = response.bytes().await else {
            return false;
        };
        serde_json::from_slice::<TurnstileCheckResponse>(&body)
            .map(|result| result.success)
            .unwrap_or(false)
    }
}

#[derive(Clone)]
pub struct AuthHttpState {
    auth: Arc<dyn DashboardAuth>,
    cookie_secure: bool,
    password_login_enabled: bool,
    trusted_origins: Arc<[String]>,
    anonymous_body_limit_bytes: usize,
    turnstile_check_enabled: bool,
    turnstile_verifier: Option<Arc<dyn TurnstileVerifier>>,
    version: Arc<str>,
}

impl AuthHttpState {
    pub fn new(auth: Arc<dyn DashboardAuth>, cookie_secure: bool) -> Self {
        Self {
            auth,
            cookie_secure,
            password_login_enabled: false,
            trusted_origins: Arc::from([]),
            anonymous_body_limit_bytes: 512 * 1024,
            turnstile_check_enabled: false,
            turnstile_verifier: None,
            version: Arc::from(concat!("v", env!("CARGO_PKG_VERSION"))),
        }
    }

    #[must_use]
    pub const fn with_password_login_enabled(mut self, enabled: bool) -> Self {
        self.password_login_enabled = enabled;
        self
    }

    #[must_use]
    pub fn with_trusted_origins<I, S>(mut self, origins: I) -> Self
    where
        I: IntoIterator<Item = S>,
        S: AsRef<str>,
    {
        self.trusted_origins = origins
            .into_iter()
            .filter_map(|origin| normalize_origin(origin.as_ref()))
            .collect::<Vec<_>>()
            .into();
        self
    }

    #[must_use]
    pub const fn with_anonymous_body_limit_bytes(mut self, bytes: usize) -> Self {
        self.anonymous_body_limit_bytes = bytes;
        self
    }

    #[must_use]
    pub fn with_turnstile_check(mut self, enabled: bool, secret_key: Option<String>) -> Self {
        self.turnstile_check_enabled = enabled;
        self.turnstile_verifier = enabled.then(|| {
            Arc::new(CloudflareTurnstileVerifier::new(secret_key)) as Arc<dyn TurnstileVerifier>
        });
        self
    }

    #[must_use]
    pub fn with_turnstile_verifier(mut self, verifier: Arc<dyn TurnstileVerifier>) -> Self {
        self.turnstile_check_enabled = true;
        self.turnstile_verifier = Some(verifier);
        self
    }

    #[must_use]
    pub fn with_version(mut self, version: impl Into<Arc<str>>) -> Self {
        self.version = version.into();
        self
    }

    #[must_use]
    pub fn dashboard_auth(&self) -> Arc<dyn DashboardAuth> {
        Arc::clone(&self.auth)
    }

    /// Derives the exact anonymous-request security policy used by login.
    ///
    /// This lets separately mounted anonymous routes share the configured
    /// limit, critical limiter, and Turnstile verifier rather than silently
    /// re-implementing a divergent policy.
    #[must_use]
    pub fn anonymous_request_security(&self) -> AnonymousRequestSecurity {
        AnonymousRequestSecurity {
            auth: Arc::clone(&self.auth),
            body_limit_bytes: self.anonymous_body_limit_bytes,
            turnstile_check_enabled: self.turnstile_check_enabled,
            turnstile_verifier: self.turnstile_verifier.clone(),
        }
    }
}

#[derive(Clone, Copy)]
enum LegacyLocale {
    En,
    ZhCn,
    ZhTw,
}

#[derive(Clone, Copy)]
enum LegacyAuthMessage {
    InvalidParameters,
    PasswordLoginDisabled,
    UsernameOrPasswordError,
    RequireTwoFactor,
    InvalidAccessToken,
    NotLoggedIn,
    DatabaseError,
    UserBanned,
}

impl LegacyLocale {
    fn from_request(request: &Request) -> Self {
        let language = request
            .headers()
            .get(header::ACCEPT_LANGUAGE)
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.split(',').next())
            .map(|value| {
                value
                    .split(';')
                    .next()
                    .unwrap_or_default()
                    .trim()
                    .to_ascii_lowercase()
            })
            .unwrap_or_default();
        if language.starts_with("zh-tw") {
            Self::ZhTw
        } else if language.starts_with("zh") {
            Self::ZhCn
        } else {
            Self::En
        }
    }

    const fn message(self, message: LegacyAuthMessage) -> &'static str {
        match (self, message) {
            (Self::En, LegacyAuthMessage::InvalidParameters) => "Invalid parameters",
            (Self::ZhCn, LegacyAuthMessage::InvalidParameters) => "无效的参数",
            (Self::ZhTw, LegacyAuthMessage::InvalidParameters) => "無效的參數",
            (Self::En, LegacyAuthMessage::PasswordLoginDisabled) => {
                "Password login has been disabled by administrator"
            }
            (Self::ZhCn, LegacyAuthMessage::PasswordLoginDisabled) => "管理员关闭了密码登录",
            (Self::ZhTw, LegacyAuthMessage::PasswordLoginDisabled) => "管理員關閉了密碼登錄",
            (Self::En, LegacyAuthMessage::UsernameOrPasswordError) => {
                "Username or password is incorrect, or user has been banned"
            }
            (Self::ZhCn, LegacyAuthMessage::UsernameOrPasswordError) => {
                "用户名或密码错误，或用户已被封禁"
            }
            (Self::ZhTw, LegacyAuthMessage::UsernameOrPasswordError) => {
                "使用者名或密碼錯誤，或使用者已被封禁"
            }
            (Self::En, LegacyAuthMessage::RequireTwoFactor) => {
                "Please enter two-factor authentication code"
            }
            (Self::ZhCn, LegacyAuthMessage::RequireTwoFactor) => "请输入两步验证码",
            (Self::ZhTw, LegacyAuthMessage::RequireTwoFactor) => "請輸入雙重驗證碼",
            (Self::En, LegacyAuthMessage::InvalidAccessToken) => {
                "Unauthorized, invalid access token"
            }
            (Self::ZhCn, LegacyAuthMessage::InvalidAccessToken) => {
                "无权进行此操作，access token 无效"
            }
            (Self::ZhTw, LegacyAuthMessage::InvalidAccessToken) => {
                "無權進行此操作，access token 無效"
            }
            (Self::En, LegacyAuthMessage::NotLoggedIn) => {
                "Unauthorized, not logged in and no access token provided"
            }
            (Self::ZhCn, LegacyAuthMessage::NotLoggedIn) => {
                "无权进行此操作，未登录且未提供 access token"
            }
            (Self::ZhTw, LegacyAuthMessage::NotLoggedIn) => {
                "無權進行此操作，未登入且未提供 access token"
            }
            (Self::En, LegacyAuthMessage::DatabaseError) => {
                "Database error, please contact the administrator"
            }
            (Self::ZhCn, LegacyAuthMessage::DatabaseError) => "数据库出错，请联系管理员",
            (Self::ZhTw, LegacyAuthMessage::DatabaseError) => "資料庫出錯，請聯繫管理員",
            (Self::En, LegacyAuthMessage::UserBanned) => "User has been banned",
            (Self::ZhCn, LegacyAuthMessage::UserBanned) => "用户已被封禁",
            (Self::ZhTw, LegacyAuthMessage::UserBanned) => "使用者已被封禁",
        }
    }
}

pub fn auth_router(state: AuthHttpState) -> Router {
    let anonymous = Router::new()
        .route("/api/user/login", post(login))
        .layer(DefaultBodyLimit::max(state.anonymous_body_limit_bytes));
    let session = Router::new()
        .route("/api/user/auth/refresh", post(refresh))
        .route("/api/user/auth/logout", post(logout))
        .route("/api/user/self", get(self_user));
    anonymous
        .merge(session)
        .with_state(state.clone())
        .layer(middleware::from_fn_with_state(
            state,
            legacy_response_headers,
        ))
}

/// Apply the anonymous registration middleware that Go mounts around
/// `controller.Register`: critical rate limiting, the configured request-body
/// ceiling, and Turnstile verification all run before the handler can parse
/// or write registration data.
pub fn anonymous_registration_surface(state: AuthHttpState, surface: Router) -> Router {
    let body_limit = state.anonymous_body_limit_bytes;
    surface
        .layer(middleware::from_fn_with_state(
            state,
            protect_anonymous_registration,
        ))
        .layer(DefaultBodyLimit::max(body_limit))
}

async fn protect_anonymous_registration(
    State(state): State<AuthHttpState>,
    request: Request,
    next: Next,
) -> Response {
    let metadata = request_metadata(&request);
    if let Some(response) = critical_rate_limit(&state, &metadata.ip).await {
        return response;
    }
    let registration_uri = request.uri().clone();
    let (mut parts, body) = request.into_parts();
    let bounded_body =
        match Bytes::from_request(Request::from_parts(parts.clone(), body), &state).await {
            Ok(body) => body,
            Err(error) if error.status() == StatusCode::PAYLOAD_TOO_LARGE => {
                let mut response = Response::new(Body::empty());
                *response.status_mut() = StatusCode::PAYLOAD_TOO_LARGE;
                return response;
            }
            Err(_) => {
                let mut response = Response::new(Body::empty());
                *response.status_mut() = StatusCode::BAD_REQUEST;
                return response;
            }
        };
    if state.turnstile_check_enabled {
        let Some(token) = turnstile_token(&registration_uri) else {
            return turnstile_missing_response();
        };
        let verified = match state.turnstile_verifier.as_ref() {
            Some(verifier) => verifier.verify(&token, &metadata.ip).await,
            None => false,
        };
        if !verified {
            return turnstile_failure_response();
        }
    }
    if let Ok(content_length) = HeaderValue::from_str(&bounded_body.len().to_string()) {
        parts.headers.insert(header::CONTENT_LENGTH, content_length);
    }
    next.run(Request::from_parts(parts, Body::from(bounded_body)))
        .await
}

#[derive(Serialize)]
struct SuccessEnvelope<T: Serialize> {
    success: bool,
    message: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<T>,
}

#[derive(Serialize)]
struct ErrorEnvelope {
    success: bool,
    code: &'static str,
    message: &'static str,
}

#[derive(Serialize)]
struct FailureEnvelope {
    success: bool,
    message: &'static str,
}

async fn login(State(state): State<AuthHttpState>, request: Request) -> Response {
    let locale = LegacyLocale::from_request(&request);
    let metadata = request_metadata(&request);
    let turnstile_uri = request.uri().clone();
    if let Some(response) = critical_rate_limit(&state, &metadata.ip).await {
        return response;
    }

    // Buffer the bounded body before Turnstile, matching the legacy middleware
    // order without parsing credentials until the challenge has passed. The
    // route's `DefaultBodyLimit` is carried in the request extensions and is
    // enforced by the `Bytes` extractor here.
    let (parts, body) = request.into_parts();
    let bounded_body =
        match Bytes::from_request(Request::from_parts(parts.clone(), body), &state).await {
            Ok(body) => body,
            Err(error) if error.status() == StatusCode::PAYLOAD_TOO_LARGE => {
                let mut response = Response::new(Body::empty());
                *response.status_mut() = StatusCode::PAYLOAD_TOO_LARGE;
                return response;
            }
            Err(_) => return legacy_login_error(locale, LegacyAuthMessage::InvalidParameters),
        };
    if state.turnstile_check_enabled {
        let Some(token) = turnstile_token(&turnstile_uri) else {
            return turnstile_missing_response();
        };
        let verified = match state.turnstile_verifier.as_ref() {
            Some(verifier) => verifier.verify(&token, &metadata.ip).await,
            None => false,
        };
        if !verified {
            return turnstile_failure_response();
        }
    }
    if !state.password_login_enabled {
        return legacy_login_error(locale, LegacyAuthMessage::PasswordLoginDisabled);
    }
    let payload = Json::<LoginRequest>::from_request(
        Request::from_parts(parts, Body::from(bounded_body)),
        &state,
    )
    .await;
    let Json(payload) = match payload {
        Ok(payload) => payload,
        Err(error) if error.status() == StatusCode::PAYLOAD_TOO_LARGE => {
            let mut response = Response::new(Body::empty());
            *response.status_mut() = StatusCode::PAYLOAD_TOO_LARGE;
            return response;
        }
        Err(_) => return legacy_login_error(locale, LegacyAuthMessage::InvalidParameters),
    };
    let username = payload.username.trim().to_owned();
    if username.is_empty() || payload.password.expose_secret().is_empty() {
        return legacy_login_error(locale, LegacyAuthMessage::InvalidParameters);
    }
    let payload = LoginRequest {
        username,
        password: payload.password,
    };
    match state.auth.login(payload, metadata).await {
        Ok(LoginOutcome::Authenticated(bundle)) => bundle_response(*bundle, state.cookie_secure),
        Ok(LoginOutcome::TwoFactorRequired(challenge)) => {
            let mut response = Json(SuccessEnvelope {
                success: true,
                message: locale.message(LegacyAuthMessage::RequireTwoFactor),
                data: Some(challenge),
            })
            .into_response();
            disable_cache(&mut response, true);
            response
        }
        Err(error) if error.kind == AuthErrorKind::InvalidCredentials => {
            invalid_login_error(locale)
        }
        Err(error) => dashboard_auth_error(locale, error),
    }
}

#[allow(dead_code)] // Kept compiled for future verification, but deliberately not mounted.
async fn login_2fa(State(state): State<AuthHttpState>, request: Request) -> Response {
    let metadata = request_metadata(&request);
    if let Some(response) = critical_rate_limit(&state, &metadata.ip).await {
        return response;
    }
    let payload = Json::<TwoFactorLoginRequest>::from_request(request, &state).await;
    let Json(payload) = match payload {
        Ok(payload) => payload,
        Err(error) if error.status() == StatusCode::PAYLOAD_TOO_LARGE => {
            let mut response = Response::new(Body::empty());
            *response.status_mut() = StatusCode::PAYLOAD_TOO_LARGE;
            return response;
        }
        Err(_) => return legacy_two_factor_error("参数错误"),
    };
    if payload.code.expose_secret().trim().is_empty()
        || payload.flow_token.expose_secret().trim().is_empty()
    {
        return legacy_two_factor_error("参数错误");
    }
    match state.auth.login_2fa(payload, metadata).await {
        Ok(bundle) => bundle_response(bundle, state.cookie_secure),
        Err(error) => match error.kind {
            AuthErrorKind::TwoFactorFlowExpired => {
                legacy_two_factor_error("会话已过期，请重新登录")
            }
            AuthErrorKind::InvalidTwoFactorCode => {
                legacy_two_factor_error("验证码或备用码错误，请重试")
            }
            AuthErrorKind::TwoFactorLocked => legacy_two_factor_error("账户已被锁定，请稍后重试"),
            AuthErrorKind::TwoFactorUnavailable => legacy_two_factor_error("用户未启用2FA"),
            _ => auth_error(error),
        },
    }
}

async fn refresh(State(state): State<AuthHttpState>, request: Request) -> Response {
    if let Err(kind) = require_cookie_origin(&state, &request) {
        return origin_forbidden_response(kind);
    }
    let expected_sid = header_text(request.headers(), "x-auth-session");
    let metadata = request_metadata(&request);
    if let Some(response) = critical_rate_limit(&state, &metadata.ip).await {
        return response;
    }
    let refresh_token = cookie(request.headers(), REFRESH_COOKIE_NAME);
    let Some(refresh_token) = refresh_token else {
        return with_clear_cookie(
            auth_error(AuthError::new(AuthErrorKind::Unauthorized)),
            state.cookie_secure,
        );
    };
    match state
        .auth
        .refresh(SecretString::from(refresh_token), expected_sid, metadata)
        .await
    {
        Ok(bundle) => bundle_response(bundle, state.cookie_secure),
        Err(error) => {
            let clear = matches!(
                error.kind,
                AuthErrorKind::Unauthorized | AuthErrorKind::SessionRevoked
            );
            let response = auth_error(error);
            if clear {
                with_clear_cookie(response, state.cookie_secure)
            } else {
                response
            }
        }
    }
}

async fn self_user(State(state): State<AuthHttpState>, request: Request) -> Response {
    let locale = LegacyLocale::from_request(&request);
    let Some(token) = authorization_token(request.headers()) else {
        return self_unauthorized(locale);
    };
    // `/api/user/self` must resolve the authoritative principal first so its
    // required-user policy can preserve Go's disabled/guest/malformed
    // response contract. This resolver still rejects expired, revoked, and
    // auth-version-mismatched sessions before a user reaches this handler.
    match state
        .auth
        .self_user_view_for_optional(SecretString::from(token))
        .await
    {
        Ok(user) => {
            let user_legacy = user.to_legacy_go_shape();
            if let Err(error) = enforce_user_auth_view(&user) {
                return self_user_auth_error(locale, error);
            }
            let mut response = Json(SuccessEnvelope {
                success: true,
                message: "",
                data: Some(user_legacy),
            })
            .into_response();
            response.headers_mut().insert(
                "auth-version",
                HeaderValue::from_static("864b7076dbcd0a3c01b5520316720ebf"),
            );
            response
        }
        Err(error) => dashboard_auth_error(locale, error),
    }
}

/// Translates the post-resolution legacy `UserAuth` policy without exposing a
/// resolved-but-unusable principal in the `/api/user/self` response.
fn self_user_auth_error(locale: LegacyLocale, error: UserAuthPolicyError) -> Response {
    let (status, code, message) = match error {
        UserAuthPolicyError::UserDisabled => (
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_DISABLED",
            locale.message(LegacyAuthMessage::UserBanned),
        ),
        UserAuthPolicyError::InsufficientPrivilege => (
            StatusCode::FORBIDDEN,
            "AUTH_INSUFFICIENT_PRIVILEGE",
            match locale {
                LegacyLocale::En => "Unauthorized, insufficient privileges",
                LegacyLocale::ZhCn => "无权进行此操作，权限不足",
                LegacyLocale::ZhTw => "無權進行此操作，權限不足",
            },
        ),
        UserAuthPolicyError::InvalidUserInfo => (
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_INVALID",
            match locale {
                LegacyLocale::En => "Unauthorized, invalid user info",
                LegacyLocale::ZhCn => "无权进行此操作，用户信息无效",
                LegacyLocale::ZhTw => "無權進行此操作，使用者資訊無效",
            },
        ),
    };
    (
        status,
        Json(ErrorEnvelope {
            success: false,
            code,
            message,
        }),
    )
        .into_response()
}

fn self_unauthorized(locale: LegacyLocale) -> Response {
    (
        StatusCode::UNAUTHORIZED,
        Json(ErrorEnvelope {
            success: false,
            code: "AUTH_UNAUTHORIZED",
            message: locale.message(LegacyAuthMessage::InvalidAccessToken),
        }),
    )
        .into_response()
}

async fn logout(State(state): State<AuthHttpState>, request: Request) -> Response {
    if let Err(kind) = require_cookie_origin(&state, &request) {
        return origin_forbidden_response(kind);
    }
    let metadata = request_metadata(&request);
    if let Some(response) = critical_rate_limit(&state, &metadata.ip).await {
        return response;
    }
    let refresh_token = cookie(request.headers(), REFRESH_COOKIE_NAME).map(SecretString::from);
    let logout = LogoutRequest {
        access_token: dashboard_bearer(request.headers()).map(SecretString::from),
        refresh_token,
        expected_sid: header_text(request.headers(), "x-auth-session"),
    };
    let access_only = logout.access_token.is_some();
    match state.auth.logout(logout).await {
        Ok(result) => {
            let data = result
                .revoked_sid
                .map(|sid| {
                    serde_json::json!({"revoked_sid": sid, "cookie_cleared": result.cookie_cleared.unwrap_or(false)})
                });
            let response = Json(SuccessEnvelope {
                success: true,
                message: "",
                data,
            })
            .into_response();
            if !access_only || result.cookie_cleared == Some(true) {
                with_clear_cookie(response, state.cookie_secure)
            } else {
                response
            }
        }
        Err(error) => auth_error(error),
    }
}

#[allow(dead_code)] // Kept compiled for future verification, but deliberately not mounted.
async fn generate_personal_access_token(
    State(state): State<AuthHttpState>,
    request: Request,
) -> Response {
    let locale = LegacyLocale::from_request(&request);
    let Some(token) = authorization_token(request.headers()) else {
        return unauthorized(locale.message(LegacyAuthMessage::InvalidAccessToken));
    };
    match state
        .auth
        .generate_personal_access_token(SecretString::from(token))
        .await
    {
        Ok(token) => Json(SuccessEnvelope {
            success: true,
            message: "",
            data: Some(token),
        })
        .into_response(),
        Err(error) => auth_error(error),
    }
}

fn request_metadata(request: &Request) -> RequestMetadata {
    RequestMetadata {
        ip: request
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
            .unwrap_or_else(|| "unknown".to_owned()),
        user_agent: header_text(request.headers(), "user-agent").unwrap_or_default(),
    }
}

fn turnstile_token(uri: &Uri) -> Option<String> {
    form_urlencoded::parse(uri.query()?.as_bytes()).find_map(|(key, value)| {
        (key == "turnstile" && !value.is_empty()).then(|| value.into_owned())
    })
}

/// Produces the legacy-compatible response for a missing Turnstile token.
pub fn turnstile_missing_response() -> Response {
    (
        StatusCode::OK,
        Json(serde_json::json!({"success": false, "message": "Turnstile token 为空"})),
    )
        .into_response()
}

/// Produces the legacy-compatible response for a rejected Turnstile token.
pub fn turnstile_failure_response() -> Response {
    (
        StatusCode::OK,
        Json(serde_json::json!({
            "success": false,
            "message": "Turnstile 校验失败，请刷新重试！"
        })),
    )
        .into_response()
}

async fn critical_rate_limit(state: &AuthHttpState, client_ip: &str) -> Option<Response> {
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

/// Legacy `UserAuth` middleware accepts either a raw token or a
/// case-insensitive `Bearer <token>` value. It rejects ambiguous values.
fn authorization_token(headers: &HeaderMap) -> Option<String> {
    let value = headers.get(header::AUTHORIZATION)?.to_str().ok()?;
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

/// The logout controller uses the narrower legacy dashboard parser and only
/// accepts an explicit `Bearer <token>` authorization scheme.
fn dashboard_bearer(headers: &HeaderMap) -> Option<String> {
    let value = headers.get(header::AUTHORIZATION)?.to_str().ok()?;
    let mut fields = value.split_whitespace();
    let scheme = fields.next()?;
    let token = fields.next()?;
    if fields.next().is_some() || !scheme.eq_ignore_ascii_case("bearer") || token.is_empty() {
        return None;
    }
    Some(token.to_owned())
}

fn cookie(headers: &HeaderMap, name: &str) -> Option<String> {
    headers
        .get_all(header::COOKIE)
        .iter()
        .filter_map(|value| value.to_str().ok())
        .flat_map(|value| value.split(';'))
        .filter_map(|part| part.trim().split_once('='))
        .find_map(|(candidate, value)| {
            (candidate == name && !value.is_empty()).then(|| value.to_owned())
        })
}

fn header_text(headers: &HeaderMap, name: &'static str) -> Option<String> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(ToOwned::to_owned)
}

fn bundle_response(bundle: AuthBundle, cookie_secure: bool) -> Response {
    let Ok(mut data) = serde_json::to_value(&bundle.data) else {
        return auth_error(AuthError::new(AuthErrorKind::Internal));
    };
    if let Some(body) = data.as_object_mut()
        && let Some(user) = body.get_mut("user")
    {
        *user = bundle.data.user.to_legacy_go_shape();
    }
    let cookie = refresh_cookie(
        bundle.refresh_token.expose_secret(),
        bundle.data.session.expires_at,
        cookie_secure,
    );
    let mut response = Json(SuccessEnvelope {
        success: true,
        message: "",
        data: Some(data),
    })
    .into_response();
    // The legacy login and refresh handlers use `setAuthNoStore`, whose
    // observable value is exactly `no-store`.
    disable_cache(&mut response, false);
    if let Ok(value) = HeaderValue::from_str(&cookie) {
        response.headers_mut().append(header::SET_COOKIE, value);
    }
    response
}

fn with_clear_cookie(mut response: Response, secure: bool) -> Response {
    disable_cache(&mut response, false);
    let cookie = clear_cookie(secure);
    if let Ok(value) = HeaderValue::from_str(&cookie) {
        response.headers_mut().append(header::SET_COOKIE, value);
    }
    response
}

fn refresh_cookie(token: &str, expires_at: i64, secure: bool) -> String {
    let now = unix_now();
    let max_age = (expires_at - now).max(1);
    let expires =
        httpdate::fmt_http_date(UNIX_EPOCH + Duration::from_secs(expires_at.max(1) as u64));
    format!(
        "{REFRESH_COOKIE_NAME}={token}; Path=/api/user/auth; Expires={expires}; Max-Age={max_age}; HttpOnly; {}SameSite=Strict",
        if secure { "Secure; " } else { "" }
    )
}

fn clear_cookie(secure: bool) -> String {
    format!(
        "{REFRESH_COOKIE_NAME}=; Path=/api/user/auth; Expires=Thu, 01 Jan 1970 00:00:01 GMT; Max-Age=0; HttpOnly; {}SameSite=Strict",
        if secure { "Secure; " } else { "" }
    )
}

fn disable_cache(response: &mut Response, strict: bool) {
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        HeaderValue::from_static(if strict {
            "no-store, no-cache, must-revalidate, private, max-age=0"
        } else {
            "no-store"
        }),
    );
    response
        .headers_mut()
        .insert(header::EXPIRES, HeaderValue::from_static("0"));
    response
        .headers_mut()
        .insert(header::PRAGMA, HeaderValue::from_static("no-cache"));
}

async fn legacy_response_headers(
    State(state): State<AuthHttpState>,
    request: Request,
    next: Next,
) -> Response {
    let request_id = request
        .extensions()
        .get::<RequestContext>()
        .map(|context| context.request_id.clone())
        .unwrap_or_else(|| uuid::Uuid::new_v4().to_string());
    let mut response = next.run(request).await;
    if let Ok(value) = HeaderValue::from_str(&state.version) {
        response.headers_mut().insert("x-new-api-version", value);
    }
    if let Ok(value) = HeaderValue::from_str(&request_id) {
        response.headers_mut().insert("x-oneapi-request-id", value);
    }
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

fn require_cookie_origin(state: &AuthHttpState, request: &Request) -> Result<(), AuthErrorKind> {
    if !state.cookie_secure {
        return Ok(());
    }
    let headers = request.headers();
    let candidate = match single_header(headers, header::ORIGIN) {
        SingleHeader::Value(origin) => secure_origin_from_uri(origin, false),
        SingleHeader::Missing => match single_header(headers, header::REFERER) {
            SingleHeader::Value(referer) => secure_origin_from_uri(referer, true),
            SingleHeader::Missing | SingleHeader::Invalid => None,
        },
        SingleHeader::Invalid => None,
    };
    let Some(candidate) = candidate else {
        return Err(AuthErrorKind::OriginForbidden);
    };
    let allowed = state
        .trusted_origins
        .iter()
        .any(|trusted| trusted.as_bytes().ct_eq(candidate.as_bytes()).into());
    if allowed {
        Ok(())
    } else {
        Err(AuthErrorKind::OriginForbidden)
    }
}

enum SingleHeader<'a> {
    Missing,
    Value(&'a str),
    Invalid,
}

fn single_header(headers: &HeaderMap, name: header::HeaderName) -> SingleHeader<'_> {
    let mut values = headers.get_all(name).iter();
    let Some(value) = values.next() else {
        return SingleHeader::Missing;
    };
    if values.next().is_some() {
        return SingleHeader::Invalid;
    }
    let Ok(value) = value.to_str() else {
        return SingleHeader::Invalid;
    };
    let value = value.trim();
    if value.is_empty() || value.contains(',') {
        SingleHeader::Invalid
    } else {
        SingleHeader::Value(value)
    }
}

fn secure_origin_from_uri(value: &str, allow_path: bool) -> Option<String> {
    let uri = value.trim().parse::<Uri>().ok()?;
    if uri.query().is_some()
        || uri.authority()?.as_str().contains('@')
        || (!allow_path && uri.path() != "/" && !uri.path().is_empty())
    {
        return None;
    }
    let origin = normalize_origin(value)?;
    origin.starts_with("https://").then_some(origin)
}

fn normalize_origin(value: &str) -> Option<String> {
    let uri = value.trim().parse::<Uri>().ok()?;
    let scheme = uri.scheme_str()?.to_ascii_lowercase();
    if scheme != "http" && scheme != "https" {
        return None;
    }
    let authority = uri.authority()?.as_str().to_ascii_lowercase();
    if authority.contains('@') {
        return None;
    }
    Some(format!("{scheme}://{authority}"))
}

fn unix_now() -> i64 {
    UNIX_EPOCH
        .elapsed()
        .map_or(0, |elapsed| elapsed.as_secs() as i64)
}

fn auth_error(error: AuthError) -> Response {
    let (status, code) = match error.kind {
        AuthErrorKind::InvalidCredentials | AuthErrorKind::Unauthorized => {
            (StatusCode::UNAUTHORIZED, "AUTH_UNAUTHORIZED")
        }
        AuthErrorKind::InvalidRequest => (StatusCode::BAD_REQUEST, "INVALID_PARAMS"),
        AuthErrorKind::TwoFactorRequired => (StatusCode::FORBIDDEN, "AUTH_TWO_FACTOR_REQUIRED"),
        AuthErrorKind::TwoFactorFlowExpired => {
            (StatusCode::UNAUTHORIZED, "AUTH_TWO_FACTOR_FLOW_EXPIRED")
        }
        AuthErrorKind::InvalidTwoFactorCode => {
            (StatusCode::UNAUTHORIZED, "AUTH_TWO_FACTOR_INVALID")
        }
        AuthErrorKind::TwoFactorLocked => (StatusCode::LOCKED, "AUTH_TWO_FACTOR_LOCKED"),
        AuthErrorKind::TwoFactorUnavailable => {
            (StatusCode::CONFLICT, "AUTH_TWO_FACTOR_UNAVAILABLE")
        }
        AuthErrorKind::PasswordLoginDisabled => (StatusCode::FORBIDDEN, "PASSWORD_LOGIN_DISABLED"),
        AuthErrorKind::OriginForbidden => (StatusCode::FORBIDDEN, "AUTH_ORIGIN_FORBIDDEN"),
        AuthErrorKind::SessionLimit => (StatusCode::CONFLICT, "AUTH_SESSION_LIMIT"),
        AuthErrorKind::SessionIssuanceLimit => {
            (StatusCode::TOO_MANY_REQUESTS, "AUTH_SESSION_ISSUANCE_LIMIT")
        }
        AuthErrorKind::SessionMismatch => (StatusCode::CONFLICT, "AUTH_SESSION_MISMATCH"),
        AuthErrorKind::RefreshRace => (StatusCode::CONFLICT, "AUTH_REFRESH_RACE"),
        AuthErrorKind::TokenExpired => (StatusCode::UNAUTHORIZED, "AUTH_TOKEN_EXPIRED"),
        AuthErrorKind::SessionRevoked => (StatusCode::UNAUTHORIZED, "AUTH_SESSION_REVOKED"),
        AuthErrorKind::UserDisabled => (StatusCode::UNAUTHORIZED, "AUTH_USER_DISABLED"),
        AuthErrorKind::Internal => (StatusCode::INTERNAL_SERVER_ERROR, "AUTH_INTERNAL_ERROR"),
    };
    let message = match error.kind {
        AuthErrorKind::OriginForbidden => "request origin is not allowed",
        AuthErrorKind::PasswordLoginDisabled => "Password login is disabled",
        _ => status.canonical_reason().unwrap_or("Internal Server Error"),
    };
    let mut response = (
        status,
        Json(ErrorEnvelope {
            success: false,
            code,
            message,
        }),
    )
        .into_response();
    disable_cache(&mut response, false);
    response
}

fn origin_forbidden_response(kind: AuthErrorKind) -> Response {
    debug_assert_eq!(kind, AuthErrorKind::OriginForbidden);
    (
        StatusCode::FORBIDDEN,
        Json(ErrorEnvelope {
            success: false,
            code: "AUTH_ORIGIN_FORBIDDEN",
            message: "request origin is not allowed",
        }),
    )
        .into_response()
}

fn dashboard_auth_error(locale: LegacyLocale, error: AuthError) -> Response {
    let (status, code, message) = match error.kind {
        AuthErrorKind::Unauthorized => (
            StatusCode::UNAUTHORIZED,
            "AUTH_UNAUTHORIZED",
            locale.message(LegacyAuthMessage::InvalidAccessToken),
        ),
        AuthErrorKind::TokenExpired => (
            StatusCode::UNAUTHORIZED,
            "AUTH_TOKEN_EXPIRED",
            locale.message(LegacyAuthMessage::NotLoggedIn),
        ),
        AuthErrorKind::SessionRevoked => (
            StatusCode::UNAUTHORIZED,
            "AUTH_SESSION_REVOKED",
            locale.message(LegacyAuthMessage::NotLoggedIn),
        ),
        AuthErrorKind::UserDisabled => (
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_DISABLED",
            locale.message(LegacyAuthMessage::UserBanned),
        ),
        AuthErrorKind::Internal => (
            StatusCode::INTERNAL_SERVER_ERROR,
            "AUTH_INTERNAL_ERROR",
            locale.message(LegacyAuthMessage::DatabaseError),
        ),
        _ => return auth_error(error),
    };
    (
        status,
        Json(ErrorEnvelope {
            success: false,
            code,
            message,
        }),
    )
        .into_response()
}

fn unauthorized(message: &'static str) -> Response {
    let mut response = (
        StatusCode::UNAUTHORIZED,
        Json(ErrorEnvelope {
            success: false,
            code: "AUTH_UNAUTHORIZED",
            message,
        }),
    )
        .into_response();
    disable_cache(&mut response, false);
    response
}

fn invalid_login_error(locale: LegacyLocale) -> Response {
    let mut response = Json(FailureEnvelope {
        success: false,
        message: locale.message(LegacyAuthMessage::UsernameOrPasswordError),
    })
    .into_response();
    disable_cache(&mut response, true);
    response
}

#[allow(dead_code)] // Used only by the unmounted 2FA completion handler above.
fn legacy_two_factor_error(message: &'static str) -> Response {
    let mut response = Json(FailureEnvelope {
        success: false,
        message,
    })
    .into_response();
    disable_cache(&mut response, true);
    response
}

fn legacy_login_error(locale: LegacyLocale, message: LegacyAuthMessage) -> Response {
    let mut response = Json(FailureEnvelope {
        success: false,
        message: locale.message(message),
    })
    .into_response();
    disable_cache(&mut response, true);
    response
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::auth::{
        AuthResponseData, DashboardSelfUserFacts, DashboardUser, DashboardUserView, LoginOutcome,
        LoginSessionView, LogoutResult, TwoFactorChallenge, TwoFactorLoginRequest,
    };
    use async_trait::async_trait;
    use axum::body::{Body, to_bytes};
    use axum::extract::ConnectInfo;
    use serde_json::Value;
    use std::{
        error::Error,
        net::SocketAddr,
        sync::{
            Mutex, MutexGuard,
            atomic::{AtomicUsize, Ordering},
        },
    };
    use tower::ServiceExt;

    type TestResult<T = ()> = Result<T, Box<dyn Error>>;

    fn test_error(message: &'static str) -> Box<dyn Error> {
        Box::new(std::io::Error::other(message))
    }

    fn required<T>(value: Option<T>, message: &'static str) -> TestResult<T> {
        value.ok_or_else(|| test_error(message))
    }

    fn lock_unpoisoned<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
        match mutex.lock() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        }
    }

    struct MockAuth {
        next: Mutex<Option<Result<LoginOutcome, AuthErrorKind>>>,
        rate_limit: Mutex<Result<CriticalRateLimitOutcome, AuthErrorKind>>,
        rate_limit_checks: AtomicUsize,
        events: Mutex<Vec<&'static str>>,
        metadata: Mutex<Vec<RequestMetadata>>,
        self_user: Mutex<Result<DashboardUser, AuthErrorKind>>,
        self_user_credentials: Mutex<Vec<String>>,
        logout_result: Mutex<LogoutResult>,
        logout_requests: Mutex<Vec<LogoutRequest>>,
    }

    struct MockTurnstile {
        allowed: bool,
        requests: Mutex<Vec<(String, String)>>,
    }

    impl MockTurnstile {
        fn allowing() -> Self {
            Self {
                allowed: true,
                requests: Mutex::new(Vec::new()),
            }
        }

        fn rejecting() -> Self {
            Self {
                allowed: false,
                requests: Mutex::new(Vec::new()),
            }
        }
    }

    #[async_trait]
    impl TurnstileVerifier for MockTurnstile {
        async fn verify(&self, token: &str, remote_ip: &str) -> bool {
            lock_unpoisoned(&self.requests)
                .push((token.to_owned(), remote_ip.to_owned()));
            self.allowed
        }
    }

    async fn registration_probe(request: Request) -> Response {
        let content_length = request
            .headers()
            .get(header::CONTENT_LENGTH)
            .and_then(|value| value.to_str().ok())
            .unwrap_or_default();
        (StatusCode::CREATED, content_length.to_owned()).into_response()
    }

    fn registration_probe_router() -> Router {
        Router::new().fallback(registration_probe)
    }

    impl MockAuth {
        fn success() -> Self {
            Self {
                next: Mutex::new(Some(Ok(LoginOutcome::Authenticated(Box::new(
                    sample_bundle(),
                ))))),
                rate_limit: Mutex::new(Ok(CriticalRateLimitOutcome::Allowed)),
                rate_limit_checks: AtomicUsize::new(0),
                events: Mutex::new(Vec::new()),
                metadata: Mutex::new(Vec::new()),
                self_user: Mutex::new(Ok(sample_user())),
                self_user_credentials: Mutex::new(Vec::new()),
                logout_result: Mutex::new(LogoutResult {
                    revoked_sid: Some("sid-1".to_owned()),
                    cookie_cleared: Some(true),
                }),
                logout_requests: Mutex::new(Vec::new()),
            }
        }

        fn two_factor_required() -> Self {
            Self {
                next: Mutex::new(Some(Ok(LoginOutcome::TwoFactorRequired(
                    TwoFactorChallenge {
                        require_2fa: true,
                        flow_token: "flow-token".to_owned(),
                        expires_at: unix_now() + 300,
                    },
                )))),
                rate_limit: Mutex::new(Ok(CriticalRateLimitOutcome::Allowed)),
                rate_limit_checks: AtomicUsize::new(0),
                events: Mutex::new(Vec::new()),
                metadata: Mutex::new(Vec::new()),
                self_user: Mutex::new(Ok(sample_user())),
                self_user_credentials: Mutex::new(Vec::new()),
                logout_result: Mutex::new(LogoutResult {
                    revoked_sid: Some("sid-1".to_owned()),
                    cookie_cleared: Some(true),
                }),
                logout_requests: Mutex::new(Vec::new()),
            }
        }

        fn with_self_user(user: DashboardUser) -> Self {
            let auth = Self::success();
            *lock_unpoisoned(&auth.self_user) = Ok(user);
            auth
        }

        fn with_logout_result(result: LogoutResult) -> Self {
            let auth = Self::success();
            *lock_unpoisoned(&auth.logout_result) = result;
            auth
        }
    }

    #[async_trait]
    impl DashboardAuth for MockAuth {
        async fn check_critical_rate_limit(
            &self,
            _: &str,
        ) -> Result<CriticalRateLimitOutcome, AuthError> {
            self.rate_limit_checks.fetch_add(1, Ordering::Relaxed);
            lock_unpoisoned(&self.events)
                .push("critical_rate_limit");
            (*lock_unpoisoned(&self.rate_limit)).map_err(AuthError::new)
        }

        async fn login(
            &self,
            _: LoginRequest,
            metadata: RequestMetadata,
        ) -> Result<LoginOutcome, AuthError> {
            lock_unpoisoned(&self.metadata).push(metadata);
            take(self)
        }

        async fn login_2fa(
            &self,
            _: TwoFactorLoginRequest,
            _: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            Ok(sample_bundle())
        }

        async fn refresh(
            &self,
            _: SecretString,
            _: Option<String>,
            _: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            lock_unpoisoned(&self.events).push("refresh");
            match take(self)? {
                LoginOutcome::Authenticated(bundle) => Ok(*bundle),
                LoginOutcome::TwoFactorRequired(_) => Err(AuthError::new(AuthErrorKind::Internal)),
            }
        }

        async fn self_user(&self, credential: SecretString) -> Result<DashboardUser, AuthError> {
            lock_unpoisoned(&self.self_user_credentials)
                .push(credential.expose_secret().to_owned());
            lock_unpoisoned(&self.self_user)
                .clone()
                .map_err(AuthError::new)
        }

        async fn logout(&self, request: LogoutRequest) -> Result<LogoutResult, AuthError> {
            lock_unpoisoned(&self.logout_requests)
                .push(request);
            Ok(lock_unpoisoned(&self.logout_result)
                .clone())
        }

        async fn generate_personal_access_token(
            &self,
            _: SecretString,
        ) -> Result<String, AuthError> {
            Ok("management-token".to_owned())
        }
    }

    fn take(mock: &MockAuth) -> Result<LoginOutcome, AuthError> {
        lock_unpoisoned(&mock.next)
            .take()
            .ok_or_else(|| AuthError::new(AuthErrorKind::Internal))?
            .map_err(AuthError::new)
    }

    fn sample_user() -> DashboardUser {
        DashboardUser {
            id: 7,
            username: "alice".to_owned(),
            display_name: "Alice".to_owned(),
            role: 1,
            status: 1,
            email: String::new(),
            github_id: String::new(),
            discord_id: String::new(),
            oidc_id: String::new(),
            wechat_id: String::new(),
            telegram_id: String::new(),
            group: "default".to_owned(),
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
            sidebar_modules: serde_json::json!({}),
            permissions: serde_json::json!({}),
        }
    }

    fn sample_bundle() -> AuthBundle {
        AuthBundle {
            data: AuthResponseData {
                access_token: "access".to_owned(),
                token_type: "Bearer",
                access_expires_at: unix_now() + 900,
                session: LoginSessionView {
                    sid: "sid-1".to_owned(),
                    current: true,
                    login_method: "password".to_owned(),
                    ip: "127.0.0.1".to_owned(),
                    user_agent: "test".to_owned(),
                    created_at: unix_now(),
                    last_active_at: unix_now(),
                    expires_at: unix_now() + 3600,
                },
                user: DashboardUserView::build(sample_user(), DashboardSelfUserFacts::default()),
            },
            refresh_token: SecretString::from("sid-1.secret".to_owned()),
        }
    }

    #[tokio::test]
    async fn login_preserves_legacy_envelope_and_cookie_controls() -> TestResult {
        let router = auth_router(
            AuthHttpState::new(Arc::new(MockAuth::success()), true)
                .with_password_login_enabled(true)
                .with_trusted_origins(["https://dashboard.example"]),
        );
        let response = router
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/user/login")
                    .header(header::CONTENT_TYPE, "application/json")
                    .header(header::ORIGIN, "https://dashboard.example")
                    .body(Body::from(r#"{"username":"alice","password":"pw"}"#))?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::OK);
        let cookie = response.headers()[header::SET_COOKIE]
            .to_str()?;
        assert!(cookie.contains("HttpOnly"));
        assert!(cookie.contains("Secure"));
        assert!(cookie.contains("SameSite=Strict"));
        let body: Value = serde_json::from_slice(
            &to_bytes(response.into_body(), usize::MAX)
                .await?,
        )?;
        assert_eq!(body["success"], true);
        assert_eq!(body["data"]["user"]["username"], "alice");
        Ok(())
    }

    #[tokio::test]
    async fn login_uses_legacy_accept_language_catalog_for_two_factor_challenges() -> TestResult {
        for (language, message) in [
            (
                "en-US,en;q=0.9",
                "Please enter two-factor authentication code",
            ),
            ("zh-CN", "请输入两步验证码"),
            ("zh-TW", "請輸入雙重驗證碼"),
            (
                "fr-FR,zh-CN;q=0.9",
                "Please enter two-factor authentication code",
            ),
        ] {
            let router = auth_router(
                AuthHttpState::new(Arc::new(MockAuth::two_factor_required()), false)
                    .with_password_login_enabled(true),
            );
            let response = router
                .oneshot(
                    Request::builder()
                        .method("POST")
                        .uri("/api/user/login")
                        .header(header::ACCEPT_LANGUAGE, language)
                        .header(header::CONTENT_TYPE, "application/json")
                        .body(Body::from(r#"{"username":"alice","password":"pw"}"#))?,
                )
                .await?;
            assert_eq!(response.status(), StatusCode::OK, "{language}");
            assert_eq!(
                response_json(response).await?["message"],
                message,
                "{language}"
            );
        }
        Ok(())
    }

    #[tokio::test]
    async fn login_rejects_missing_turnstile_token_when_enabled() -> TestResult {
        let auth = Arc::new(MockAuth::success());
        let response = auth_router(
            AuthHttpState::new(auth.clone(), false)
                .with_password_login_enabled(true)
                .with_turnstile_check(true, None),
        )
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/user/login")
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(r#"{"username":"alice","password":"pw"}"#))?,
        )
        .await?;
        assert_eq!(response.status(), StatusCode::OK);
        let body = response_json(response).await?;
        assert_eq!(body["success"], false);
        assert_eq!(body["message"], "Turnstile token 为空");
        assert!(lock_unpoisoned(&auth.next).is_some());
        Ok(())
    }

    #[tokio::test]
    async fn login_allows_turnstile_token_when_enabled() -> TestResult {
        let auth = Arc::new(MockAuth::success());
        let response = auth_router(
            AuthHttpState::new(auth.clone(), false)
                .with_password_login_enabled(true)
                .with_turnstile_verifier(Arc::new(MockTurnstile::allowing())),
        )
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/user/login?turnstile=demo%2Dtoken")
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(r#"{"username":"alice","password":"pw"}"#))?,
        )
        .await?;
        assert_eq!(response.status(), StatusCode::OK);
        let body = response_json(response).await?;
        assert_eq!(body["success"], true);
        assert_eq!(body["data"]["user"]["username"], "alice");
        assert!(lock_unpoisoned(&auth.next).is_none());
        Ok(())
    }

    #[tokio::test]
    async fn login_rejects_turnstile_when_verifier_fails_closed() -> TestResult {
        let auth = Arc::new(MockAuth::success());
        let verifier = Arc::new(MockTurnstile::rejecting());
        let response = auth_router(
            AuthHttpState::new(auth.clone(), false)
                .with_password_login_enabled(true)
                .with_turnstile_verifier(verifier.clone()),
        )
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/user/login?turnstile=demo-token")
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(r#"{"username":"alice","password":"pw"}"#))?,
        )
        .await?;
        assert_eq!(response.status(), StatusCode::OK);
        let body = response_json(response).await?;
        assert_eq!(body["success"], false);
        assert_eq!(body["message"], "Turnstile 校验失败，请刷新重试！");
        assert!(lock_unpoisoned(&auth.next).is_some());
        assert_eq!(
            lock_unpoisoned(&verifier.requests)
                .as_slice(),
            &[("demo-token".to_owned(), "unknown".to_owned())]
        );
        Ok(())
    }

    #[tokio::test]
    async fn enabled_turnstile_without_secret_rejects_even_with_token() -> TestResult {
        let auth = Arc::new(MockAuth::success());
        let response = auth_router(
            AuthHttpState::new(auth.clone(), false)
                .with_password_login_enabled(true)
                .with_turnstile_check(true, None),
        )
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/user/login?turnstile=demo-token")
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(r#"{"username":"alice","password":"pw"}"#))?,
        )
        .await?;
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(response_json(response).await?["success"], false);
        assert!(lock_unpoisoned(&auth.next).is_some());
        Ok(())
    }

    #[tokio::test]
    async fn unverified_auth_routes_are_not_mounted() -> TestResult {
        let router = auth_router(AuthHttpState::new(Arc::new(MockAuth::success()), false));
        for (method, path) in [("POST", "/api/user/login/2fa"), ("GET", "/api/user/token")] {
            let response = router
                .clone()
                .oneshot(
                    Request::builder()
                        .method(method)
                        .uri(path)
                        .body(Body::empty())?,
                )
                .await?;
            assert_eq!(response.status(), StatusCode::NOT_FOUND, "{method} {path}");
        }
        Ok(())
    }

    #[tokio::test]
    async fn self_accepts_legacy_raw_tokens_and_rejects_ambiguous_authorization() -> TestResult {
        let auth = Arc::new(MockAuth::success());
        let router = auth_router(AuthHttpState::new(auth.clone(), false));
        let response = router
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/api/user/self")
                    .header(header::AUTHORIZATION, "token-only")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            *lock_unpoisoned(&auth.self_user_credentials),
            ["token-only"]
        );
        let response = router
            .oneshot(
                Request::builder()
                    .uri("/api/user/self")
                    .header(header::AUTHORIZATION, "Bearer token extra")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        Ok(())
    }

    #[tokio::test]
    async fn logout_rejects_a_raw_authorization_token() -> TestResult {
        let auth = Arc::new(MockAuth::with_logout_result(LogoutResult {
            revoked_sid: None,
            cookie_cleared: None,
        }));
        let response = auth_router(AuthHttpState::new(auth.clone(), false))
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/user/auth/logout")
                    .header(header::AUTHORIZATION, "raw-token")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::OK);
        let requests = lock_unpoisoned(&auth.logout_requests);
        assert_eq!(requests.len(), 1);
        assert!(requests[0].access_token.is_none());
        Ok(())
    }

    #[tokio::test]
    async fn logout_clears_cookie_only_when_the_legacy_contract_requires_it() -> TestResult {
        let cases = [
            (
                "access only",
                Some("Bearer access"),
                None,
                LogoutResult {
                    revoked_sid: Some("sid-1".to_owned()),
                    cookie_cleared: Some(false),
                },
                false,
            ),
            (
                "access plus foreign cookie",
                Some("Bearer access"),
                Some("new_api_refresh=other.secret"),
                LogoutResult {
                    revoked_sid: Some("sid-1".to_owned()),
                    cookie_cleared: Some(false),
                },
                false,
            ),
            (
                "access with confirmed cookie clear",
                Some("Bearer access"),
                Some("new_api_refresh=sid-1.secret"),
                LogoutResult {
                    revoked_sid: Some("sid-1".to_owned()),
                    cookie_cleared: Some(true),
                },
                true,
            ),
            (
                "cookie only",
                None,
                Some("new_api_refresh=sid-1.secret"),
                LogoutResult {
                    revoked_sid: None,
                    cookie_cleared: None,
                },
                true,
            ),
            (
                "anonymous",
                None,
                None,
                LogoutResult {
                    revoked_sid: None,
                    cookie_cleared: None,
                },
                true,
            ),
        ];
        for (label, authorization, cookie, result, should_clear) in cases {
            let auth = Arc::new(MockAuth::with_logout_result(result));
            let mut request = Request::builder()
                .method("POST")
                .uri("/api/user/auth/logout");
            if let Some(authorization) = authorization {
                request = request.header(header::AUTHORIZATION, authorization);
            }
            if let Some(cookie) = cookie {
                request = request.header(header::COOKIE, cookie);
            }
            let response = auth_router(AuthHttpState::new(auth.clone(), false))
                .oneshot(request.body(Body::empty())?)
                .await?;
            assert_eq!(response.status(), StatusCode::OK, "{label}");
            assert_eq!(
                response.headers().contains_key(header::SET_COOKIE),
                should_clear,
                "{label}"
            );
            assert_eq!(
                lock_unpoisoned(&auth.logout_requests)
                    .len(),
                1
            );
        }
        Ok(())
    }

    #[tokio::test]
    async fn successful_self_emits_legacy_auth_version() -> TestResult {
        let router = auth_router(AuthHttpState::new(Arc::new(MockAuth::success()), false));
        let response = router
            .oneshot(
                Request::builder()
                    .uri("/api/user/self")
                    .header(header::AUTHORIZATION, "Bearer access")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            response.headers()["auth-version"],
            "864b7076dbcd0a3c01b5520316720ebf"
        );
        Ok(())
    }

    #[tokio::test]
    async fn self_rejects_resolved_required_user_policy_failures_without_data() -> TestResult {
        type SelfPolicyCase = (
            &'static str,
            fn(&mut DashboardUser),
            StatusCode,
            &'static str,
            &'static str,
        );
        let cases: [SelfPolicyCase; 3] = [
            (
                "disabled",
                |user: &mut DashboardUser| user.status = 2,
                StatusCode::UNAUTHORIZED,
                "AUTH_USER_DISABLED",
                "User has been banned",
            ),
            (
                "guest",
                |user: &mut DashboardUser| user.role = 0,
                StatusCode::FORBIDDEN,
                "AUTH_INSUFFICIENT_PRIVILEGE",
                "Unauthorized, insufficient privileges",
            ),
            (
                "malformed",
                |user: &mut DashboardUser| user.role = 2,
                StatusCode::UNAUTHORIZED,
                "AUTH_USER_INVALID",
                "Unauthorized, invalid user info",
            ),
        ];
        for (label, mutate, status, code, message) in cases {
            let mut user = sample_user();
            mutate(&mut user);
            let auth = Arc::new(MockAuth::with_self_user(user));
            let response = auth_router(AuthHttpState::new(auth.clone(), false))
                .oneshot(
                    Request::builder()
                        .uri("/api/user/self")
                        .header(header::AUTHORIZATION, "Bearer resolved-credential")
                        .body(Body::empty())?,
                )
                .await?;
            assert_eq!(response.status(), status, "{label}");
            let body = response_json(response).await?;
            assert_eq!(body["code"], code, "{label}");
            assert_eq!(body["message"], message, "{label}");
            assert!(body.get("data").is_none(), "{label}");
            assert_eq!(
                *lock_unpoisoned(&auth.self_user_credentials),
                ["resolved-credential"],
                "{label} must resolve the credential before policy rejection"
            );
        }
        Ok(())
    }

    #[tokio::test]
    async fn refresh_checks_origin_then_rate_limit_then_cookie() -> TestResult {
        let auth = Arc::new(MockAuth::success());
        *lock_unpoisoned(&auth.rate_limit) =
            Ok(CriticalRateLimitOutcome::Rejected {
                retry_after_seconds: 37,
            });
        let router = auth_router(
            AuthHttpState::new(auth.clone(), true)
                .with_trusted_origins(["https://dashboard.example"]),
        );

        let response = router
            .clone()
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/user/auth/refresh")
                    .header(header::ORIGIN, "https://evil.example")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::FORBIDDEN);
        assert_eq!(auth.rate_limit_checks.load(Ordering::Relaxed), 0);
        assert!(lock_unpoisoned(&auth.events).is_empty());

        let response = router
            .clone()
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/user/auth/refresh")
                    .header(header::ORIGIN, "https://dashboard.example")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(auth.rate_limit_checks.load(Ordering::Relaxed), 1);
        assert_eq!(
            *lock_unpoisoned(&auth.events),
            ["critical_rate_limit"]
        );
        assert!(!response.headers().contains_key(header::SET_COOKIE));

        let response = router
            .clone()
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/user/auth/refresh")
                    .header(header::ORIGIN, "https://dashboard.example")
                    .header(header::COOKIE, "new_api_refresh=attacker.secret")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(auth.rate_limit_checks.load(Ordering::Relaxed), 2);
        assert_eq!(
            *lock_unpoisoned(&auth.events),
            ["critical_rate_limit", "critical_rate_limit"]
        );
        assert!(!response.headers().contains_key(header::SET_COOKIE));

        *lock_unpoisoned(&auth.rate_limit) = Ok(CriticalRateLimitOutcome::Allowed);
        *lock_unpoisoned(&auth.next) = Some(Err(AuthErrorKind::TokenExpired));
        lock_unpoisoned(&auth.events).clear();
        let response = router
            .clone()
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/user/auth/refresh")
                    .header(header::ORIGIN, "https://dashboard.example")
                    .header(header::COOKIE, "new_api_refresh=expired.secret")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(auth.rate_limit_checks.load(Ordering::Relaxed), 3);
        assert_eq!(
            *lock_unpoisoned(&auth.events),
            ["critical_rate_limit", "refresh"]
        );
        assert_eq!(response_json(response).await?["code"], "AUTH_TOKEN_EXPIRED");

        *lock_unpoisoned(&auth.next) =
            Some(Ok(LoginOutcome::Authenticated(Box::new(sample_bundle()))));
        lock_unpoisoned(&auth.events).clear();
        let response = router
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/user/auth/refresh")
                    .header(header::ORIGIN, "https://dashboard.example")
                    .header(header::COOKIE, "new_api_refresh=valid.secret")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(auth.rate_limit_checks.load(Ordering::Relaxed), 4);
        assert_eq!(
            *lock_unpoisoned(&auth.events),
            ["critical_rate_limit", "refresh"]
        );

        assert_eq!(
            response.headers()[header::SET_COOKIE]
                .to_str()?
                .split(';')
                .next(),
            Some("new_api_refresh=sid-1.secret")
        );
        Ok(())
    }

    #[tokio::test]
    async fn refresh_missing_cookie_still_passes_rate_limit_before_unauthorized() -> TestResult {
        let auth = Arc::new(MockAuth::success());
        let router = auth_router(
            AuthHttpState::new(auth.clone(), true)
                .with_trusted_origins(["https://dashboard.example"]),
        );
        let response = router
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/user/auth/refresh")
                    .header(header::ORIGIN, "https://dashboard.example")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(auth.rate_limit_checks.load(Ordering::Relaxed), 1);
        assert_eq!(
            *lock_unpoisoned(&auth.events),
            ["critical_rate_limit"]
        );
        assert_eq!(
            response.headers()[header::SET_COOKIE],
            "new_api_refresh=; Path=/api/user/auth; Expires=Thu, 01 Jan 1970 00:00:01 GMT; Max-Age=0; HttpOnly; Secure; SameSite=Strict"
        );
        assert_eq!(
            response_json(response).await?,
            serde_json::json!({
                "success": false,
                "code": "AUTH_UNAUTHORIZED",
                "message": "Unauthorized"
            })
        );
        Ok(())
    }

    #[tokio::test]
    async fn password_login_is_fail_closed_until_explicitly_enabled() -> TestResult {
        let router = auth_router(AuthHttpState::new(Arc::new(MockAuth::success()), false));
        let response = router
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/user/login")
                    .header(header::CONTENT_TYPE, "application/json")
                    .body(Body::from(r#"{"username":"alice","password":"pw"}"#))?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::OK);
        let body = response_json(response).await?;
        assert_eq!(
            body["message"],
            "Password login has been disabled by administrator"
        );
        Ok(())
    }

    #[tokio::test]
    async fn secure_cookie_routes_require_an_allowed_origin_or_referer() -> TestResult {
        let state = AuthHttpState::new(Arc::new(MockAuth::success()), true)
            .with_password_login_enabled(true)
            .with_trusted_origins(["https://dashboard.example"]);
        for forbidden_headers in [
            vec![],
            vec![(header::ORIGIN, "https://evil.example")],
            vec![
                (header::ORIGIN, "https://evil.example"),
                (header::HOST, "evil.example"),
            ],
            vec![(header::ORIGIN, "http://dashboard.example")],
            vec![
                (
                    header::ORIGIN,
                    "https://dashboard.example, https://evil.example",
                ),
                (header::REFERER, "https://dashboard.example/settings"),
            ],
            vec![
                (header::ORIGIN, "https://dashboard.example"),
                (header::ORIGIN, "https://dashboard.example"),
            ],
        ] {
            let mut builder = Request::builder()
                .method("POST")
                .uri("/api/user/auth/refresh")
                .header(header::COOKIE, "new_api_refresh=sid.secret");
            for (name, value) in forbidden_headers {
                builder = builder.header(name, value);
            }
            let response = auth_router(state.clone())
                .oneshot(builder.body(Body::empty())?)
                .await?;
            assert_eq!(response.status(), StatusCode::FORBIDDEN);
            for header_name in [header::CACHE_CONTROL, header::EXPIRES, header::PRAGMA] {
                assert!(
                    response.headers().get(&header_name).is_none(),
                    "origin guard must run before cache controls: {header_name}"
                );
            }
            assert_eq!(
                response_json(response).await?["code"],
                "AUTH_ORIGIN_FORBIDDEN"
            );
        }

        let response = auth_router(state)
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/user/auth/refresh")
                    .header(header::COOKIE, "new_api_refresh=sid.secret")
                    .header(header::REFERER, "https://dashboard.example/settings")
                    .body(Body::empty())?,
            )
            .await?;
        assert_ne!(response.status(), StatusCode::FORBIDDEN);
        Ok(())
    }

    #[tokio::test]
    async fn oracle_auth_fixtures_match_status_headers_and_body() -> TestResult {
        let state = AuthHttpState::new(
            Arc::new(MockAuth {
                next: Mutex::new(Some(Err(AuthErrorKind::InvalidCredentials))),
                rate_limit: Mutex::new(Ok(CriticalRateLimitOutcome::Allowed)),
                rate_limit_checks: AtomicUsize::new(0),
                events: Mutex::new(Vec::new()),
                metadata: Mutex::new(Vec::new()),
                self_user: Mutex::new(Ok(sample_user())),
                self_user_credentials: Mutex::new(Vec::new()),
                logout_result: Mutex::new(LogoutResult {
                    revoked_sid: None,
                    cookie_cleared: None,
                }),
                logout_requests: Mutex::new(Vec::new()),
            }),
            false,
        )
        .with_password_login_enabled(true)
        .with_version("v0.0.0");
        let cases = [
            (
                "auth-login.json",
                "POST",
                "/api/user/login",
                Some(r#"{"username":"missing","password":"bad"}"#),
            ),
            (
                "auth-logout.json",
                "POST",
                "/api/user/auth/logout",
                Some("{}"),
            ),
            (
                "auth-refresh.json",
                "POST",
                "/api/user/auth/refresh",
                Some("{}"),
            ),
            ("auth-self.json", "GET", "/api/user/self", None),
        ];
        for (fixture_name, method, uri, body) in cases {
            let fixture_path = format!(
                "{}/tests/behavior-oracle/fixtures/{fixture_name}",
                env!("CARGO_MANIFEST_DIR")
            );
            let fixture: Value = serde_json::from_str(
                &std::fs::read_to_string(fixture_path)?,
            )?;
            let mut builder = Request::builder().method(method).uri(uri);
            if body.is_some() {
                builder = builder.header(header::CONTENT_TYPE, "application/json");
            }
            let response = auth_router(state.clone())
                .oneshot(
                    builder
                        .body(body.map_or_else(Body::empty, Body::from))?,
                )
                .await?;
            assert_eq!(
                response.status().as_u16(),
                required(fixture["response"]["status"].as_u64(), "missing fixture status")?
                    as u16,
                "{fixture_name}"
            );
            for name in ["cache-control", "expires", "pragma", "set-cookie"] {
                if let Some(expected) = fixture["response"]["selected_headers"][name].as_str() {
                    assert_eq!(
                        response
                            .headers()
                            .get(name)
                            .and_then(|value| value.to_str().ok()),
                        Some(expected),
                        "{fixture_name}: {name}"
                    );
                }
            }
            assert!(response.headers().contains_key("x-oneapi-request-id"));
            assert_eq!(response.headers()["x-new-api-version"], "v0.0.0");
            assert_eq!(response_json(response).await?, fixture["response"]["body"]);
        }
        Ok(())
    }

    #[tokio::test]
    async fn response_identity_is_server_generated_and_version_is_build_derived() -> TestResult {
        let router = auth_router(
            AuthHttpState::new(Arc::new(MockAuth::success()), false)
                .with_password_login_enabled(true),
        );
        let response = router
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/user/login")
                    .header(header::CONTENT_TYPE, "application/json")
                    .header("x-oneapi-request-id", "attacker-controlled")
                    .body(Body::from(r#"{"username":"alice","password":"pw"}"#))?,
            )
            .await?;

        let request_id = response.headers()["x-oneapi-request-id"]
            .to_str()?;
        assert_ne!(request_id, "attacker-controlled");
        uuid::Uuid::parse_str(request_id)?;
        assert_eq!(
            response.headers()["x-new-api-version"],
            concat!("v", env!("CARGO_PKG_VERSION"))
        );
        Ok(())
    }

    #[tokio::test]
    async fn anonymous_login_body_limit_rejects_before_authentication() -> TestResult {
        let auth = Arc::new(MockAuth::success());
        let router = auth_router(
            AuthHttpState::new(auth.clone(), false)
                .with_password_login_enabled(true)
                .with_anonymous_body_limit_bytes(16),
        );
        let response = router
            .oneshot(login_request(
                "alice",
                "a password larger than the limit",
                None,
            )?)
            .await?;
        assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
        assert!(lock_unpoisoned(&auth.next).is_some());
        Ok(())
    }

    #[tokio::test]
    async fn anonymous_login_body_limit_rejects_before_turnstile() -> TestResult {
        let auth = Arc::new(MockAuth::success());
        let verifier = Arc::new(MockTurnstile::allowing());
        let router = auth_router(
            AuthHttpState::new(auth.clone(), false)
                .with_password_login_enabled(true)
                .with_anonymous_body_limit_bytes(16)
                .with_turnstile_verifier(verifier.clone()),
        );
        let response = router
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/user/login?turnstile=demo-token")
                    .header(header::CONTENT_TYPE, "application/json")
                    .body(Body::from(
                        r#"{"username":"alice","password":"a password larger than the limit"}"#,
                    ))?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
        assert!(lock_unpoisoned(&auth.next).is_some());
        assert!(
            lock_unpoisoned(&verifier.requests)
                .is_empty()
        );
        Ok(())
    }

    #[tokio::test]
    async fn anonymous_registration_middleware_matches_go_guard_order() -> TestResult {
        let auth = Arc::new(MockAuth::success());
        let verifier = Arc::new(MockTurnstile::allowing());
        let router = anonymous_registration_surface(
            AuthHttpState::new(auth.clone(), false)
                .with_anonymous_body_limit_bytes(16)
                .with_turnstile_verifier(verifier.clone()),
            registration_probe_router(),
        );

        let response = router
            .clone()
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/user/register?turnstile=demo-token")
                    .body(Body::from("012345678901234567"))?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
        assert!(
            lock_unpoisoned(&verifier.requests)
                .is_empty()
        );

        let response = router
            .clone()
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/user/register")
                    .body(Body::from("{}"))?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            response_json(response).await?["message"],
            "Turnstile token 为空"
        );

        let response = router
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/user/register?turnstile=demo-token")
                    .body(Body::from("{}"))?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::CREATED);
        assert_eq!(
            String::from_utf8(
                to_bytes(response.into_body(), usize::MAX)
                    .await?
                    .to_vec(),
            )?,
            "2"
        );
        assert_eq!(
            lock_unpoisoned(&verifier.requests)
                .as_slice(),
            &[("demo-token".to_owned(), "unknown".to_owned())]
        );
        assert_eq!(
            auth.rate_limit_checks.load(Ordering::Relaxed),
            3,
            "every registration attempt is rate limited before body or Turnstile"
        );
        Ok(())
    }

    #[tokio::test]
    async fn anonymous_registration_rate_limit_rejects_before_body_and_turnstile() -> TestResult {
        let auth = Arc::new(MockAuth::success());
        *lock_unpoisoned(&auth.rate_limit) =
            Ok(CriticalRateLimitOutcome::Rejected {
                retry_after_seconds: 37,
            });
        let verifier = Arc::new(MockTurnstile::allowing());
        let response = anonymous_registration_surface(
            AuthHttpState::new(auth.clone(), false).with_turnstile_verifier(verifier.clone()),
            registration_probe_router(),
        )
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/user/register?turnstile=demo-token")
                .body(Body::from("012345678901234567"))?,
        )
        .await?;
        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(response.headers()[header::RETRY_AFTER], "37");
        assert!(
            to_bytes(response.into_body(), usize::MAX)
                .await?
                .is_empty()
        );
        assert!(
            lock_unpoisoned(&verifier.requests)
                .is_empty()
        );
        assert_eq!(auth.rate_limit_checks.load(Ordering::Relaxed), 1);
        Ok(())
    }

    #[tokio::test]
    async fn critical_rate_limit_returns_empty_429_without_calling_login() -> TestResult {
        let auth = Arc::new(MockAuth::success());
        *lock_unpoisoned(&auth.rate_limit) =
            Ok(CriticalRateLimitOutcome::Rejected {
                retry_after_seconds: 37,
            });
        let response =
            auth_router(AuthHttpState::new(auth.clone(), false).with_password_login_enabled(true))
                .oneshot(login_request("alice", "pw", None)?)
                .await?;
        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(response.headers()[header::RETRY_AFTER], "37");
        assert!(!response.headers().contains_key(header::CONTENT_TYPE));
        assert!(
            to_bytes(response.into_body(), usize::MAX)
                .await?
                .is_empty()
        );
        assert!(lock_unpoisoned(&auth.next).is_some());
        Ok(())
    }

    #[tokio::test]
    async fn audit_ip_uses_only_trusted_loopback_proxy_headers() -> TestResult {
        let untrusted = Arc::new(MockAuth::success());
        auth_router(AuthHttpState::new(untrusted.clone(), false).with_password_login_enabled(true))
            .oneshot(login_request(
                "alice",
                "pw",
                Some(("198.51.100.20:443", "203.0.113.99")),
            )?)
            .await?;
        assert_eq!(
            lock_unpoisoned(&untrusted.metadata)[0].ip,
            "198.51.100.20"
        );

        let trusted = Arc::new(MockAuth::success());
        auth_router(AuthHttpState::new(trusted.clone(), false).with_password_login_enabled(true))
            .oneshot(login_request(
                "alice",
                "pw",
                Some(("127.0.0.1:443", "203.0.113.99")),
            )?)
            .await?;
        assert_eq!(
            lock_unpoisoned(&trusted.metadata)[0].ip,
            "203.0.113.99"
        );
        Ok(())
    }

    fn login_request(
        username: &str,
        password: &str,
        peer_and_real_ip: Option<(&str, &str)>,
    ) -> TestResult<Request> {
        let mut builder = Request::builder()
            .method("POST")
            .uri("/api/user/login")
            .header(header::CONTENT_TYPE, "application/json");
        if let Some((_, real_ip)) = peer_and_real_ip {
            builder = builder.header("x-real-ip", real_ip);
        }
        let mut request = builder.body(Body::from(
            serde_json::json!({"username": username, "password": password}).to_string(),
        ))?;
        if let Some((peer, real_ip)) = peer_and_real_ip {
            let peer = peer.parse::<SocketAddr>()?;
            request.extensions_mut().insert(ConnectInfo(peer));
            // Mirror the listener's default trusted-proxy policy: only a
            // loopback peer may supply the effective client IP via x-real-ip.
            // Untrusted peers retain their socket IP even when that header is
            // present, keeping both audit-IP assertions meaningful.
            let ip = if peer.ip().is_loopback() {
                real_ip.parse()?
            } else {
                peer.ip()
            };
            request.extensions_mut().insert(RequestContext {
                request_id: "test-request".to_owned(),
                client_ip: Some(ip),
            });
            request.extensions_mut().insert(ClientIpKey(ip.to_string()));
        }
        Ok(request)
    }

    async fn response_json(response: Response) -> TestResult<Value> {
        let body = to_bytes(response.into_body(), usize::MAX).await?;
        Ok(serde_json::from_slice(&body)?)
    }

    #[tokio::test]
    async fn anonymous_request_security_reuses_limit_rate_limit_and_turnstile_policy() -> TestResult {
        let auth = Arc::new(MockAuth::success());
        let verifier = Arc::new(MockTurnstile::allowing());
        let policy = AuthHttpState::new(auth.clone(), false)
            .with_anonymous_body_limit_bytes(123)
            .with_turnstile_verifier(verifier.clone())
            .anonymous_request_security();

        assert_eq!(policy.body_limit_bytes(), 123);
        assert!(matches!(
            policy.check_critical_rate_limit("203.0.113.9").await,
            Ok(CriticalRateLimitOutcome::Allowed)
        ));
        assert_eq!(
            policy
                .check_turnstile(
                    &"/api/user/register?turnstile=token".parse()?,
                    "203.0.113.9"
                )
                .await,
            TurnstileCheckOutcome::Allowed
        );
        assert_eq!(
            lock_unpoisoned(&verifier.requests)
                .as_slice(),
            &[("token".to_owned(), "203.0.113.9".to_owned())]
        );
        assert_eq!(auth.rate_limit_checks.load(Ordering::Relaxed), 1);
        Ok(())
    }
}
