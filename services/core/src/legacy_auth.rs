//! Existing Web authentication contract backed exclusively by Rust identity.
#[path = "identity_compat.rs"]
mod compatibility;
use crate::identity::{
    BrowserAuth, BrowserAuthError, BrowserBundle, BrowserLoginOutcome, BrowserRegistration,
    BrowserSessionMetadata, IdentityError, IdentityStore, MfaCompletion, MfaError,
};
use axum::{
    Json, Router,
    body::Bytes,
    extract::{
        ConnectInfo, DefaultBodyLimit, Query, Request, State,
        rejection::{JsonRejection, QueryRejection},
    },
    http::{HeaderMap, HeaderValue, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Redirect, Response},
    routing::{get, post},
};
use rand::RngCore;
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
    /// Only exact configured Web callbacks enable the built-in browser preset.
    /// The client ID is public; credentials and provider endpoints stay private.
    pub fn google_client_id<'a>(&self, store: &'a IdentityStore) -> Option<&'a str> {
        store.google_browser_client_id(&self.origin)
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
    let state = AuthState {
        browser,
        store,
        config,
    };
    Router::new()
        .merge(compatibility::routes())
        .route("/api/user/register", post(register))
        .route("/api/user/login", post(login))
        .route("/api/user/login/2fa", post(complete_mfa_login))
        .route("/api/user/2fa/status", get(mfa_status))
        .route("/api/user/2fa/setup", post(mfa_setup))
        .route("/api/user/2fa/enable", post(mfa_enable))
        .route("/api/user/2fa/disable", post(mfa_disable))
        .route("/api/user/2fa/backup_codes", post(mfa_regenerate))
        .route("/api/user/auth/refresh", post(refresh))
        .route("/api/user/auth/logout", post(logout))
        .route("/api/oauth/google", post(google_start))
        .route("/api/oauth/google/callback", get(google_callback))
        .route(
            "/api/user/self",
            get(current_user).put(compatibility::profile),
        )
        .layer(DefaultBodyLimit::max(16 * 1024))
        .layer(middleware::from_fn_with_state(state.clone(), security))
        .with_state(state)
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
    } else if unsafe_method || request.uri().path() == "/api/oauth/google/callback" {
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
    if !body.email.trim().is_empty()
        || !body.verification_code.is_empty()
        || !body.aff_code.is_empty()
        || !body.turnstile.is_empty()
    {
        return failure(
            StatusCode::SERVICE_UNAVAILABLE,
            "AUTH_REGISTRATION_FEATURE_UNAVAILABLE",
            "Email, verification, referral and challenge registration are not yet enabled",
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
    let mut random = [0u8; 32];
    if rand::rngs::OsRng.try_fill_bytes(&mut random).is_err() {
        return auth_failure(IdentityError::Storage.into());
    }
    let binding: String = random.iter().map(|byte| format!("{byte:02x}")).collect();
    match state
        .browser
        .login_with_mfa(&body.username, &body.password, &metadata, &binding)
        .await
    {
        Ok(BrowserLoginOutcome::Session(bundle)) => bundle_response(bundle, &state.config),
        Ok(BrowserLoginOutcome::Challenge(challenge)) => mfa_flow_cookie(
            success(Some(json!(challenge))),
            Some(&binding),
            &state.config,
        ),
        Err(error) => mfa_failure(error),
    }
}
fn mfa_cookie_name(config: &LegacyAuthConfig) -> &'static str {
    if config.secure {
        "__Host-lmm_mfa_flow"
    } else {
        "lmm_mfa_flow"
    }
}
fn mfa_binding_cookie<'a>(
    headers: &'a HeaderMap,
    config: &LegacyAuthConfig,
) -> Result<&'a str, MfaError> {
    let mut found = None;
    for raw in headers.get_all(header::COOKIE) {
        let raw = raw.to_str().map_err(|_| MfaError::FlowExpired)?;
        for part in raw.split(';') {
            if part.trim() == mfa_cookie_name(config) {
                return Err(MfaError::FlowExpired);
            }
            if let Some((name, value)) = part.trim().split_once('=')
                && name == mfa_cookie_name(config)
            {
                if found.is_some()
                    || value.len() != 64
                    || !value.bytes().all(|b| b.is_ascii_hexdigit())
                {
                    return Err(MfaError::FlowExpired);
                }
                found = Some(value);
            }
        }
    }
    found.ok_or(MfaError::FlowExpired)
}
fn mfa_flow_cookie(
    mut response: Response,
    binding: Option<&str>,
    config: &LegacyAuthConfig,
) -> Response {
    let secure = if config.secure { "Secure; " } else { "" };
    let value = format!(
        "{}={}; Path=/; {secure}HttpOnly; SameSite=Strict; Max-Age={}",
        mfa_cookie_name(config),
        binding.unwrap_or_default(),
        if binding.is_some() { 300 } else { 0 }
    );
    if let Ok(value) = HeaderValue::from_str(&value) {
        response.headers_mut().append(header::SET_COOKIE, value);
    }
    response
}
async fn complete_mfa_login(
    State(state): State<AuthState>,
    headers: HeaderMap,
    input: Result<Json<MfaCompletion>, JsonRejection>,
) -> Response {
    let Ok(Json(body)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    let result = async {
        let binding = mfa_binding_cookie(&headers, &state.config)?;
        state.browser.complete_mfa_login(&body, binding).await
    }
    .await;
    match result {
        Ok(bundle) => mfa_flow_cookie(bundle_response(bundle, &state.config), None, &state.config),
        // A failed older flow must not clear a newer tab's binding cookie.
        // The short-lived binding expires itself; successful completion clears it.
        Err(error) => mfa_failure(error),
    }
}
async fn mfa_status(State(state): State<AuthState>, headers: HeaderMap) -> Response {
    let result = async {
        state
            .browser
            .mfa_status(bearer(&headers)?.ok_or(IdentityError::Unauthorized)?)
            .await
    }
    .await;
    match result {
        Ok(status) => success(Some(json!(status))),
        Err(error) => mfa_failure(error),
    }
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct MfaSetupRequest {}
async fn mfa_setup(State(state): State<AuthState>, headers: HeaderMap, body: Bytes) -> Response {
    // The existing Web client sends no body; an explicit JSON object may only
    // be empty. Never silently ignore caller-supplied policy or factor fields.
    if !body.is_empty() && serde_json::from_slice::<MfaSetupRequest>(&body).is_err() {
        return auth_failure(IdentityError::Invalid.into());
    }
    let result = async {
        state
            .browser
            .mfa_setup(bearer(&headers)?.ok_or(IdentityError::Unauthorized)?)
            .await
    }
    .await;
    match result {
        Ok(setup) => success(Some(json!(setup))),
        Err(error) => mfa_failure(error),
    }
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct MfaCode {
    code: String,
}
async fn mfa_enable(
    State(state): State<AuthState>,
    headers: HeaderMap,
    input: Result<Json<MfaCode>, JsonRejection>,
) -> Response {
    let Ok(Json(body)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    let result = async {
        state
            .browser
            .mfa_enable(
                bearer(&headers)?.ok_or(IdentityError::Unauthorized)?,
                &body.code,
            )
            .await
    }
    .await;
    match result {
        Ok(bundle) => bundle_response(bundle, &state.config),
        Err(error) => mfa_failure(error),
    }
}
async fn mfa_disable(
    State(state): State<AuthState>,
    headers: HeaderMap,
    input: Result<Json<MfaCode>, JsonRejection>,
) -> Response {
    let Ok(Json(body)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    let result = async {
        state
            .browser
            .mfa_disable(
                bearer(&headers)?.ok_or(IdentityError::Unauthorized)?,
                &body.code,
            )
            .await
    }
    .await;
    match result {
        Ok(bundle) => bundle_response(bundle, &state.config),
        Err(error) => mfa_failure(error),
    }
}
async fn mfa_regenerate(
    State(state): State<AuthState>,
    headers: HeaderMap,
    input: Result<Json<MfaCode>, JsonRejection>,
) -> Response {
    let Ok(Json(body)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    let result = async {
        state
            .browser
            .mfa_regenerate(
                bearer(&headers)?.ok_or(IdentityError::Unauthorized)?,
                &body.code,
            )
            .await
    }
    .await;
    match result {
        Ok(bundle) => bundle_response(bundle, &state.config),
        Err(error) => mfa_failure(error),
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
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct GoogleStart {
    accepted_legal: Option<bool>,
}
async fn google_start(
    State(state): State<AuthState>,
    input: Result<Json<GoogleStart>, JsonRejection>,
) -> Response {
    if state.config.google_client_id(&state.store).is_none() {
        return auth_failure(IdentityError::Forbidden.into());
    }
    let Ok(Json(input)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    if state.config.require_legal_acceptance && input.accepted_legal != Some(true) {
        return failure(
            StatusCode::BAD_REQUEST,
            "AUTH_LEGAL_ACCEPTANCE_REQUIRED",
            "Accept the published legal terms before signing in",
        );
    }
    let mut random = [0u8; 32];
    if rand::rngs::OsRng.try_fill_bytes(&mut random).is_err() {
        return auth_failure(IdentityError::Storage.into());
    }
    let binding: String = random.iter().map(|byte| format!("{byte:02x}")).collect();
    match state.browser.begin_google_login(&binding).await {
        Ok(url) => google_flow_cookie(
            success(Some(json!({"authorization_url":url}))),
            Some(&binding),
            &state.config,
        ),
        Err(error) => auth_failure(error),
    }
}
fn google_cookie_name(config: &LegacyAuthConfig) -> &'static str {
    if config.secure {
        "__Host-lmm_web_google_flow"
    } else {
        "lmm_web_google_flow"
    }
}
fn google_binding_cookie<'a>(
    headers: &'a HeaderMap,
    config: &LegacyAuthConfig,
) -> Result<&'a str, IdentityError> {
    let mut found = None;
    for raw in headers.get_all(header::COOKIE) {
        let raw = raw.to_str().map_err(|_| IdentityError::Unauthorized)?;
        for part in raw.split(';') {
            if let Some((name, value)) = part.trim().split_once('=')
                && name == google_cookie_name(config)
            {
                if found.is_some()
                    || value.len() != 64
                    || !value.bytes().all(|b| b.is_ascii_hexdigit())
                {
                    return Err(IdentityError::Unauthorized);
                }
                found = Some(value);
            }
        }
    }
    found.ok_or(IdentityError::Unauthorized)
}
fn google_flow_cookie(
    mut response: Response,
    binding: Option<&str>,
    config: &LegacyAuthConfig,
) -> Response {
    let secure = if config.secure { "Secure; " } else { "" };
    let cookie = format!(
        "{}={}; Path=/; {secure}HttpOnly; SameSite=Lax; Max-Age={}",
        google_cookie_name(config),
        binding.unwrap_or_default(),
        if binding.is_some() { 600 } else { 0 }
    );
    if let Ok(cookie) = HeaderValue::from_str(&cookie) {
        response.headers_mut().append(header::SET_COOKIE, cookie);
    }
    response
}
#[derive(Deserialize)]
struct GoogleCallback {
    state: Option<String>,
    code: Option<String>,
    error: Option<String>,
}
async fn google_callback(
    State(state): State<AuthState>,
    headers: HeaderMap,
    peer: Result<ConnectInfo<SocketAddr>, axum::extract::rejection::ExtensionRejection>,
    query: Result<Query<GoogleCallback>, QueryRejection>,
) -> Response {
    let result = async {
        if state.config.google_client_id(&state.store).is_none() {
            return Err(IdentityError::Forbidden.into());
        }
        let Query(query) = query.map_err(|_| IdentityError::Invalid)?;
        if query.error.is_some() {
            return Err(IdentityError::Unauthorized.into());
        }
        let binding = google_binding_cookie(&headers, &state.config)?;
        let metadata = BrowserSessionMetadata {
            ip: peer.map(|peer| peer.0.ip().to_string()).unwrap_or_default(),
            user_agent: headers
                .get(header::USER_AGENT)
                .and_then(|v| v.to_str().ok())
                .unwrap_or_default()
                .chars()
                .take(512)
                .collect(),
        };
        state
            .browser
            .finish_google_login(
                query.state.as_deref().ok_or(IdentityError::Unauthorized)?,
                binding,
                query.code.as_deref().ok_or(IdentityError::Unauthorized)?,
                &metadata,
            )
            .await
    }
    .await;
    let response = match result {
        Ok(bundle) => set_cookie(
            Redirect::to("/console").into_response(),
            Some((&bundle.refresh_token, bundle.expires_at)),
            &state.config,
        ),
        Err(error) => auth_failure(error),
    };
    google_flow_cookie(response, None, &state.config)
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
fn mfa_failure(error: MfaError) -> Response {
    match error {
        MfaError::Identity(error) => auth_failure(error.into()),
        MfaError::CodeInvalid => {
            failure(StatusCode::BAD_REQUEST, "MFA_CODE_INVALID", "Invalid code")
        }
        MfaError::FlowExpired => failure(
            StatusCode::BAD_REQUEST,
            "MFA_FLOW_EXPIRED",
            "This login flow has expired or has already been used. Sign in again.",
        ),
        MfaError::Locked(seconds) => {
            let mut response = failure(
                StatusCode::TOO_MANY_REQUESTS,
                "MFA_LOCKED",
                "Too many verification attempts. Please retry later.",
            );
            if let Ok(value) = HeaderValue::from_str(&seconds.max(1).to_string()) {
                response.headers_mut().insert(header::RETRY_AFTER, value);
            }
            response
        }
        MfaError::Unavailable => failure(
            StatusCode::SERVICE_UNAVAILABLE,
            "MFA_UNAVAILABLE",
            "Two-factor authentication is temporarily unavailable. Try again later.",
        ),
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

#[cfg(test)]
mod mfa_http_tests {
    use super::*;

    fn config() -> LegacyAuthConfig {
        LegacyAuthConfig::new("https://app.example", &[42; 32]).unwrap()
    }

    #[test]
    fn mfa_binding_requires_one_bounded_hex_cookie() {
        let config = config();
        let name = mfa_cookie_name(&config);
        let valid = "a".repeat(64);
        let mut headers = HeaderMap::new();
        assert_eq!(
            mfa_binding_cookie(&headers, &config),
            Err(MfaError::FlowExpired)
        );
        headers.insert(
            header::COOKIE,
            HeaderValue::from_str(&format!("other=value; {name}={valid}")).unwrap(),
        );
        assert_eq!(mfa_binding_cookie(&headers, &config).unwrap(), valid);
        for invalid in [
            "a".repeat(63),
            "a".repeat(65),
            "g".repeat(64),
            String::new(),
        ] {
            headers.insert(
                header::COOKIE,
                HeaderValue::from_str(&format!("{name}={invalid}")).unwrap(),
            );
            assert_eq!(
                mfa_binding_cookie(&headers, &config),
                Err(MfaError::FlowExpired)
            );
        }
        headers.insert(
            header::COOKIE,
            HeaderValue::from_str(&format!("{name}={valid}; {name}={valid}")).unwrap(),
        );
        assert_eq!(
            mfa_binding_cookie(&headers, &config),
            Err(MfaError::FlowExpired)
        );
        headers.insert(
            header::COOKIE,
            HeaderValue::from_str(&format!("{name}={valid}")).unwrap(),
        );
        headers.append(
            header::COOKIE,
            HeaderValue::from_str(&format!("{name}={valid}")).unwrap(),
        );
        assert_eq!(
            mfa_binding_cookie(&headers, &config),
            Err(MfaError::FlowExpired)
        );
        headers.remove(header::COOKIE);
        headers.insert(
            header::COOKIE,
            HeaderValue::from_str(&format!("{name}={valid}; {name}")).unwrap(),
        );
        assert_eq!(
            mfa_binding_cookie(&headers, &config),
            Err(MfaError::FlowExpired)
        );
    }

    #[test]
    fn mfa_cookie_is_private_and_appended_beside_refresh_cookie() {
        let config = config();
        let mut response = success(None);
        response.headers_mut().append(
            header::SET_COOKIE,
            HeaderValue::from_static("new_api_refresh=retained"),
        );
        let response = mfa_flow_cookie(response, Some(&"b".repeat(64)), &config);
        let values: Vec<_> = response
            .headers()
            .get_all(header::SET_COOKIE)
            .iter()
            .map(|v| v.to_str().unwrap())
            .collect();
        assert_eq!(values.len(), 2);
        assert_eq!(values[0], "new_api_refresh=retained");
        assert!(values[1].starts_with("__Host-lmm_mfa_flow="));
        assert!(values[1].contains("Path=/; Secure; HttpOnly; SameSite=Strict; Max-Age=300"));
        let cleared = mfa_flow_cookie(success(None), None, &config);
        assert_eq!(
            cleared.headers()[header::SET_COOKIE],
            "__Host-lmm_mfa_flow=; Path=/; Secure; HttpOnly; SameSite=Strict; Max-Age=0"
        );
        let local = LegacyAuthConfig::new("http://127.0.0.1:8080", &[42; 32])
            .unwrap()
            .with_insecure_local_http()
            .unwrap();
        let cookie = mfa_flow_cookie(success(None), Some(&"b".repeat(64)), &local);
        let value = cookie.headers()[header::SET_COOKIE].to_str().unwrap();
        assert!(value.starts_with("lmm_mfa_flow="));
        assert!(!value.contains("Secure;"));
        let mut headers = HeaderMap::new();
        headers.insert(
            header::COOKIE,
            HeaderValue::from_str(&format!("__Host-lmm_mfa_flow={}", "b".repeat(64))).unwrap(),
        );
        assert_eq!(
            mfa_binding_cookie(&headers, &local),
            Err(MfaError::FlowExpired)
        );
    }

    #[test]
    fn mfa_errors_distinguish_verification_from_session_failure() {
        for error in [MfaError::CodeInvalid, MfaError::FlowExpired] {
            assert_eq!(mfa_failure(error).status(), StatusCode::BAD_REQUEST);
        }
        let locked = mfa_failure(MfaError::Locked(37));
        assert_eq!(locked.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(locked.headers()[header::RETRY_AFTER], "37");
        assert_eq!(
            mfa_failure(MfaError::Unavailable).status(),
            StatusCode::SERVICE_UNAVAILABLE
        );
        assert_eq!(
            mfa_failure(MfaError::Identity(IdentityError::Unauthorized)).status(),
            StatusCode::UNAUTHORIZED
        );
    }

    #[test]
    fn mfa_request_bodies_reject_caller_supplied_authority() {
        assert!(serde_json::from_value::<MfaSetupRequest>(json!({})).is_ok());
        assert!(serde_json::from_value::<MfaSetupRequest>(json!({"enabled":true})).is_err());
        assert!(serde_json::from_value::<MfaCode>(json!({"code":"123456","user_id":1})).is_err());
        assert!(
            serde_json::from_value::<MfaCompletion>(
                json!({"code":"123456","flow_token":"flow","mfa_verified":true})
            )
            .is_err()
        );
    }
}
