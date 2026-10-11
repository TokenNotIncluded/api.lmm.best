//! Existing Web authentication contract backed exclusively by Rust identity.
#[path = "identity_compat.rs"]
mod compatibility;
use crate::identity::{
    BrowserAuth, BrowserAuthError, BrowserBundle, BrowserRegistration, BrowserSessionMetadata,
    IdentityError, IdentityStore,
};
use axum::{
    Json, Router,
    extract::{ConnectInfo, DefaultBodyLimit, Request, State, rejection::JsonRejection},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use serde::Deserialize;
use serde_json::{Value, json};
use std::{
    net::SocketAddr,
    sync::Arc,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

const COOKIE: &str = "new_api_refresh";
#[derive(Clone)]
pub struct LegacyAuthConfig {
    origin: String,
    refresh_key: Arc<[u8]>,
    secure: bool,
    require_legal_acceptance: bool,
}
impl LegacyAuthConfig {
    /// Use a newly generated private key, independent from old sessions. The
    /// canonical external origin is configured, never inferred from headers.
    pub fn new(origin: &str, refresh_key: &[u8]) -> Result<Self, IdentityError> {
        if !(32..=128).contains(&refresh_key.len()) {
            return Err(IdentityError::Invalid);
        }
        let url = reqwest::Url::parse(origin).map_err(|_| IdentityError::Invalid)?;
        if !matches!(url.scheme(), "https" | "http")
            || url.host_str().is_none()
            || !url.username().is_empty()
            || url.password().is_some()
            || url.path() != "/"
            || url.query().is_some()
            || url.fragment().is_some()
        {
            return Err(IdentityError::Invalid);
        }
        Ok(Self {
            origin: url.origin().ascii_serialization(),
            refresh_key: refresh_key.into(),
            secure: true,
            require_legal_acceptance: true,
        })
    }
    /// Explicitly match the operator's published registration policy.
    pub fn with_legal_acceptance_required(mut self, required: bool) -> Self {
        self.require_legal_acceptance = required;
        self
    }
    /// Deliberate local-only exception. Origin checking remains enabled even
    /// though the local browser cannot use a Secure cookie over plain HTTP.
    pub fn with_insecure_local_http(mut self) -> Result<Self, IdentityError> {
        let url = reqwest::Url::parse(&self.origin).map_err(|_| IdentityError::Invalid)?;
        if url.scheme() != "http"
            || !matches!(
                url.host_str(),
                Some("localhost" | "127.0.0.1" | "[::1]" | "api.lmm.local")
            )
        {
            return Err(IdentityError::Invalid);
        }
        self.secure = false;
        Ok(self)
    }
}
#[derive(Clone)]
struct AuthState {
    browser: BrowserAuth,
    store: IdentityStore,
    config: LegacyAuthConfig,
}
pub fn router(store: IdentityStore, config: LegacyAuthConfig) -> Router {
    let browser = BrowserAuth::new(store.clone(), &config.refresh_key)
        .expect("validated private refresh key");
    let hg = crate::humangrant::router(store.pool(), store.clone(), browser.clone(), config.origin.clone());
    let state = AuthState {
        browser,
        store,
        config,
    };
    Router::new()
        .merge(compatibility::routes())
        .route("/api/user/register", post(register))
        .route("/api/user/login", post(login))
        .route("/api/user/auth/refresh", post(refresh))
        .route("/api/user/auth/logout", post(logout))
        .route(
            "/api/user/self",
            get(current_user).put(compatibility::profile),
        )
        .layer(DefaultBodyLimit::max(16 * 1024))
        .layer(middleware::from_fn_with_state(state.clone(), security))
        .with_state(state)
        .merge(hg)
}
async fn security(State(state): State<AuthState>, request: Request, next: Next) -> Response {
    let unsafe_method = !matches!(
        *request.method(),
        axum::http::Method::GET | axum::http::Method::HEAD | axum::http::Method::OPTIONS
    );
    let mut response = if unsafe_method && !valid_origin(request.headers(), &state.config.origin) {
        failure(
            StatusCode::FORBIDDEN,
            "AUTH_ORIGIN_FORBIDDEN",
            "Request origin is not allowed",
        )
    } else if unsafe_method {
        let peer = request
            .extensions()
            .get::<ConnectInfo<SocketAddr>>()
            .map(|peer| peer.0.ip().to_string())
            .unwrap_or_else(|| "unknown-peer".into());
        match state.store.auth_rate_limit("browser_peer", &peer, 60).await {
            Ok(true) => next.run(request).await,
            Ok(false) => {
                let mut response = failure(
                    StatusCode::TOO_MANY_REQUESTS,
                    "AUTH_RATE_LIMITED",
                    "Please retry later",
                );
                response
                    .headers_mut()
                    .insert(header::RETRY_AFTER, HeaderValue::from_static("60"));
                response
            }
            Err(error) => auth_failure(error.into()),
        }
    } else {
        next.run(request).await
    };
    for (name, value) in [
        (header::CACHE_CONTROL, "no-store"),
        (header::PRAGMA, "no-cache"),
        (header::EXPIRES, "0"),
        (header::X_CONTENT_TYPE_OPTIONS, "nosniff"),
        (header::REFERRER_POLICY, "no-referrer"),
    ] {
        response
            .headers_mut()
            .insert(name, HeaderValue::from_static(value));
    }
    response
}
fn one_header(
    headers: &HeaderMap,
    name: header::HeaderName,
) -> Result<Option<&str>, IdentityError> {
    let mut values = headers.get_all(name).iter();
    let Some(value) = values.next() else {
        return Ok(None);
    };
    if values.next().is_some() {
        return Err(IdentityError::Invalid);
    }
    let value = value.to_str().map_err(|_| IdentityError::Invalid)?.trim();
    if value.is_empty() || value.contains(',') {
        return Err(IdentityError::Invalid);
    }
    Ok(Some(value))
}
fn valid_origin(headers: &HeaderMap, trusted: &str) -> bool {
    let candidate = match one_header(headers, header::ORIGIN) {
        Ok(Some(origin)) => {
            let Ok(url) = reqwest::Url::parse(origin) else {
                return false;
            };
            if url.path() != "/" || url.query().is_some() || url.fragment().is_some() {
                return false;
            }
            url
        }
        Ok(None) => {
            let Ok(Some(referer)) = one_header(headers, header::REFERER) else {
                return false;
            };
            let Ok(url) = reqwest::Url::parse(referer) else {
                return false;
            };
            url
        }
        Err(_) => return false,
    };
    candidate.username().is_empty()
        && candidate.password().is_none()
        && candidate.origin().ascii_serialization() == trusted
}
fn refresh_cookie(headers: &HeaderMap) -> Result<Option<&str>, IdentityError> {
    let mut found = None;
    for raw in headers.get_all(header::COOKIE) {
        let raw = raw.to_str().map_err(|_| IdentityError::Unauthorized)?;
        for part in raw.split(';') {
            if let Some((name, value)) = part.trim().split_once('=')
                && name == COOKIE
            {
                if found.is_some() || value.is_empty() || value.len() > 256 {
                    return Err(IdentityError::Unauthorized);
                }
                found = Some(value);
            }
        }
    }
    Ok(found)
}
fn expected_sid(headers: &HeaderMap) -> Result<Option<&str>, IdentityError> {
    let sid = one_header(headers, header::HeaderName::from_static("x-auth-session"))?;
    if sid.is_some_and(|value| value.len() > 128) {
        return Err(IdentityError::Invalid);
    }
    Ok(sid)
}
fn bearer(headers: &HeaderMap) -> Result<Option<&str>, IdentityError> {
    let Some(raw) = one_header(headers, header::AUTHORIZATION)? else {
        return Ok(None);
    };
    let mut fields = raw.split_whitespace();
    let scheme = fields.next().ok_or(IdentityError::Unauthorized)?;
    let secret = fields.next().ok_or(IdentityError::Unauthorized)?;
    if !scheme.eq_ignore_ascii_case("Bearer") || fields.next().is_some() {
        return Err(IdentityError::Unauthorized);
    }
    Ok(Some(secret))
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Login {
    username: String,
    password: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Register {
    username: String,
    password: String,
    #[serde(default)]
    email: String,
    #[serde(default)]
    verification_code: String,
    #[serde(default)]
    aff_code: String,
    #[serde(default)]
    accepted_legal: Option<bool>,
    #[serde(default)]
    turnstile: String,
}
async fn register(
    State(state): State<AuthState>,
    input: Result<Json<Register>, JsonRejection>,
) -> Response {
    let Ok(Json(body)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    if state.config.require_legal_acceptance && body.accepted_legal != Some(true) {
        return failure(
            StatusCode::BAD_REQUEST,
            "AUTH_LEGAL_ACCEPTANCE_REQUIRED",
            "Accept the published legal terms before registering",
        );
    }
    if !body.verification_code.is_empty() || !body.aff_code.is_empty() || !body.turnstile.is_empty()
    {
        return failure(
            StatusCode::SERVICE_UNAVAILABLE,
            "AUTH_REGISTRATION_FEATURE_UNAVAILABLE",
            "Email verification and referral registration are not yet enabled",
        );
    }
    match state
        .browser
        .register(&BrowserRegistration {
            username: &body.username,
            password: &body.password,
            email: &body.email,
        })
        .await
    {
        Ok(()) => success(None),
        Err(error) => auth_failure(error),
    }
}
async fn login(
    State(state): State<AuthState>,
    headers: HeaderMap,
    peer: Result<ConnectInfo<SocketAddr>, axum::extract::rejection::ExtensionRejection>,
    input: Result<Json<Login>, JsonRejection>,
) -> Response {
    let Ok(Json(body)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    let metadata = BrowserSessionMetadata {
        ip: peer.map(|peer| peer.0.ip().to_string()).unwrap_or_default(),
        user_agent: headers
            .get(header::USER_AGENT)
            .and_then(|h| h.to_str().ok())
            .unwrap_or_default()
            .chars()
            .take(512)
            .collect(),
    };
    match state
        .browser
        .login(&body.username, &body.password, &metadata)
        .await
    {
        Ok(bundle) => bundle_response(bundle, &state.config),
        Err(error) => auth_failure(error),
    }
}
async fn refresh(State(state): State<AuthState>, headers: HeaderMap) -> Response {
    let result = async {
        let cookie = refresh_cookie(&headers)?.ok_or(IdentityError::Unauthorized)?;
        state.browser.refresh(cookie, expected_sid(&headers)?).await
    }
    .await;
    match result {
        Ok(bundle) => bundle_response(bundle, &state.config),
        Err(error) => {
            let clear = matches!(
                error,
                BrowserAuthError::SessionRevoked
                    | BrowserAuthError::Identity(IdentityError::Unauthorized)
            );
            let response = auth_failure(error);
            if clear {
                set_cookie(response, None, &state.config)
            } else {
                response
            }
        }
    }
}
async fn logout(State(state): State<AuthState>, headers: HeaderMap) -> Response {
    let result = async {
        let expected = expected_sid(&headers)?;
        let access = bearer(&headers)?;
        let cookie = refresh_cookie(&headers);
        let initial = match cookie {
            Ok(cookie) => state.browser.logout(cookie, access, expected).await,
            Err(error) => Err(error.into()),
        };
        match initial {
            Err(BrowserAuthError::Identity(IdentityError::Unauthorized)) => {
                if access.is_some() {
                    match state.browser.logout(None, access, expected).await {
                        Err(BrowserAuthError::Identity(IdentityError::Unauthorized)) => Ok(()),
                        result => result,
                    }
                } else {
                    Ok(())
                }
            }
            result => result,
        }
    }
    .await;
    match result {
        Ok(()) => set_cookie(success(None), None, &state.config),
        Err(error) => auth_failure(error),
    }
}
async fn current_user(State(state): State<AuthState>, headers: HeaderMap) -> Response {
    let result = async {
        state
            .browser
            .current_user(bearer(&headers)?.ok_or(IdentityError::Unauthorized)?)
            .await
    }
    .await;
    match result {
        Ok(user) => success(Some(user)),
        Err(error) => auth_failure(error),
    }
}
fn success(data: Option<Value>) -> Response {
    let mut body = json!({"success":true,"message":""});
    if let Some(data) = data {
        body["data"] = data
    }
    Json(body).into_response()
}
fn failure(status: StatusCode, code: &str, message: &str) -> Response {
    (
        status,
        Json(json!({"success":false,"message":message,"code":code})),
    )
        .into_response()
}
fn auth_failure(error: BrowserAuthError) -> Response {
    match error {
        BrowserAuthError::SessionMismatch => failure(
            StatusCode::CONFLICT,
            "AUTH_SESSION_MISMATCH",
            "Browser session changed",
        ),
        BrowserAuthError::RefreshRace => failure(
            StatusCode::CONFLICT,
            "AUTH_REFRESH_RACE",
            "Session refresh is already in progress",
        ),
        BrowserAuthError::SessionRevoked => failure(
            StatusCode::UNAUTHORIZED,
            "AUTH_SESSION_REVOKED",
            "Session is no longer active",
        ),
        BrowserAuthError::Identity(error) => match error {
            IdentityError::Unauthorized => failure(
                StatusCode::UNAUTHORIZED,
                "AUTH_UNAUTHORIZED",
                "Invalid credentials or expired session",
            ),
            IdentityError::Forbidden => failure(
                StatusCode::FORBIDDEN,
                "AUTH_FORBIDDEN",
                "This operation is not allowed",
            ),
            IdentityError::Invalid => failure(
                StatusCode::BAD_REQUEST,
                "AUTH_INVALID_REQUEST",
                "Invalid request parameters",
            ),
            IdentityError::Conflict => failure(
                StatusCode::CONFLICT,
                "AUTH_CONFLICT",
                "The request conflicts with existing data",
            ),
            IdentityError::Storage => failure(
                StatusCode::SERVICE_UNAVAILABLE,
                "AUTH_UNAVAILABLE",
                "Authentication is temporarily unavailable",
            ),
        },
    }
}
fn bundle_response(bundle: BrowserBundle, config: &LegacyAuthConfig) -> Response {
    set_cookie(
        success(Some(bundle.data)),
        Some((&bundle.refresh_token, bundle.expires_at)),
        config,
    )
}
fn set_cookie(
    mut response: Response,
    cookie: Option<(&str, i64)>,
    config: &LegacyAuthConfig,
) -> Response {
    let secure = if config.secure { "Secure; " } else { "" };
    let value = match cookie {
        Some((token, expires)) => {
            let now = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap_or(Duration::ZERO)
                .as_secs() as i64;
            format!(
                "{COOKIE}={token}; Path=/api/user/auth; Max-Age={}; HttpOnly; {secure}SameSite=Strict",
                (expires - now).max(1)
            )
        }
        None => format!(
            "{COOKIE}=; Path=/api/user/auth; Expires=Thu, 01 Jan 1970 00:00:01 GMT; Max-Age=0; HttpOnly; {secure}SameSite=Strict"
        ),
    };
    if let Ok(value) = HeaderValue::from_str(&value) {
        response.headers_mut().append(header::SET_COOKIE, value);
    }
    response
}
