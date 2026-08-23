//! Legacy-compatible identity federation routes.
//!
//! Provider token exchange remains behind [`FederationProviders`]: this route
//! slice does not make outbound calls until the listener installs configured
//! production adapters.

use crate::migration_routes::verify_email::ValkeyVerificationCodeStore;
use crate::{
    ClientIpKey, RequestContext,
    auth::{AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth},
    legacy_empty_response,
};
use async_trait::async_trait;
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{DefaultBodyLimit, Path, Query, Request, State},
    http::{HeaderMap, HeaderName, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{delete, get, post},
};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use hmac::{Hmac, Mac};
use jsonwebtoken::{Algorithm, DecodingKey, Validation, decode};
use rand::RngCore;
use reqwest::{Client, Url, redirect::Policy};
use secrecy::{ExposeSecret, SecretString};
use serde::{Deserialize, Serialize};
use serde_json::json;
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Row};
use std::{
    collections::BTreeMap,
    net::IpAddr,
    sync::Arc,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

type HmacSha256 = Hmac<Sha256>;
const OAUTH_FLOW_TTL: Duration = Duration::from_secs(10 * 60);
const MAX_IDENTITY_BODY_BYTES: usize = 1024 * 1024;
const OAUTH_STATE_PATH: &str = "/api/oauth/state";
const OAUTH_EMAIL_BIND_PATH: &str = "/api/oauth/email/bind";
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const EMAIL_VERIFICATION_PURPOSE: &str = "v";

/// Identity returned by a configured OAuth/OIDC adapter after it has verified
/// the provider's token response and user-info (or ID-token) claims.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FederatedUser {
    pub provider_user_id: String,
    pub legacy_provider_user_id: Option<String>,
    pub username: String,
    pub display_name: String,
    pub email: String,
}

/// The one-time server-side state available to an OAuth adapter.
///
/// `pkce_verifier` and `nonce` are intentionally never returned by the
/// callback. An adapter uses them only when its authorization request actually
/// advertised the corresponding challenge/nonce.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct OAuthFlowContext {
    pub provider: String,
    pub intent: String,
    pub user_id: i64,
    pub session_id: String,
    pub affiliate_code: String,
    pub pkce_verifier: String,
    pub nonce: String,
}

/// Output from the shared dashboard-auth implementation.  The auth component
/// owns JWT creation, the refresh cookie, PostgreSQL session rows, and Valkey
/// invalidation; this route must not mint a second kind of session.
#[derive(Debug)]
pub struct FederatedLogin {
    pub data: serde_json::Value,
    pub refresh_cookie: Option<HeaderValue>,
}

#[derive(Debug, thiserror::Error)]
pub enum FederationProviderError {
    #[error("provider disabled")]
    Disabled,
    #[error("invalid authorization code")]
    InvalidCode,
    #[error("provider rejected the authorization")]
    Denied,
    #[error("provider communication failed")]
    Unavailable,
    #[error("provider returned an invalid identity")]
    InvalidIdentity,
}

/// Production boundary for standards-compliant OAuth/OIDC implementations.
///
/// The listener must supply adapters backed by `oauth2` + `reqwest`, and by
/// `openidconnect` for OIDC ID-token/JWKS validation. Keeping that networking
/// boundary here makes callback state consumption and account binding testable
/// without ever placing client secrets in browser code.
#[async_trait]
pub trait FederationProviders: Send + Sync {
    /// Whether a built-in provider has a complete, enabled configuration.
    fn built_in_enabled(&self, provider: &str) -> bool;

    /// Exchanges `code`, validates the upstream response, and returns a stable
    /// provider subject. Implementations must use `flow.pkce_verifier` only
    /// when the matching authorization request used PKCE and must validate the
    /// OIDC nonce when one was sent.
    async fn exchange(
        &self,
        provider: &str,
        code: &str,
        flow: &OAuthFlowContext,
    ) -> Result<FederatedUser, FederationProviderError>;

    /// Establishes the regular dashboard session for a successful OAuth login.
    /// This is where the shared auth service writes `user_sessions`, updates
    /// Valkey session/auth-version keys, and creates the refresh cookie.
    async fn login(
        &self,
        provider: &str,
        user: FederatedUser,
        affiliate_code: &str,
        headers: &HeaderMap,
    ) -> Result<FederatedLogin, FederationError>;

    /// Confirms that this adapter consumes every non-empty verifier/nonce in
    /// [`OAuthFlowContext`]. A row containing such secrets fails closed unless
    /// the provider explicitly opts in; merely storing random values is not
    /// equivalent to advertising and validating PKCE or OIDC nonce binding.
    fn consumes_flow_secrets(&self, _: &str) -> bool {
        false
    }

    /// Resolves the existing Telegram user before the assertion is consumed.
    /// Frozen Go performs this lookup before its replay claim and never creates
    /// a user from a Telegram widget callback.
    async fn validate_existing_login(&self, _: &str, _: &str) -> Result<(), FederationError> {
        Err(FederationError::Internal)
    }

    fn wechat_enabled(&self) -> bool {
        false
    }

    /// Resolves the one-time code issued by the configured WeChat bridge.
    async fn exchange_wechat(&self, _: &str) -> Result<String, FederationProviderError> {
        Err(FederationProviderError::Disabled)
    }

    /// The Telegram bot token used only to authenticate widget assertions.
    /// Implementations must source it from secret configuration, never a
    /// request header or query value.
    fn telegram_bot_token(&self) -> Option<String> {
        None
    }

    fn telegram_enabled(&self) -> bool {
        self.telegram_bot_token().is_some()
    }
}

#[derive(Default)]
struct DisabledProviders;

#[async_trait]
impl FederationProviders for DisabledProviders {
    fn built_in_enabled(&self, _: &str) -> bool {
        false
    }

    async fn exchange(
        &self,
        _: &str,
        _: &str,
        _: &OAuthFlowContext,
    ) -> Result<FederatedUser, FederationProviderError> {
        Err(FederationProviderError::Disabled)
    }

    async fn login(
        &self,
        _: &str,
        _: FederatedUser,
        _: &str,
        _: &HeaderMap,
    ) -> Result<FederatedLogin, FederationError> {
        Err(FederationError::Internal)
    }
}

/// Server-side issuer used after a provider has proved an external identity.
///
/// It deliberately remains separate from token exchange: the OAuth adapter
/// never manufactures a dashboard session.  The listener must install an
/// issuer that uses the same PostgreSQL/Valkey session implementation as
/// password login, otherwise every successful exchange fails closed.
#[async_trait]
pub trait FederatedLoginIssuer: Send + Sync {
    async fn issue_login(
        &self,
        provider: &str,
        user: FederatedUser,
        affiliate_code: &str,
        headers: &HeaderMap,
    ) -> Result<FederatedLogin, FederationError>;
}

/// Verifies email codes issued by the configured mail-verification service.
///
/// This boundary avoids treating a browser-supplied code as proof.  A missing
/// implementation is intentionally equivalent to an invalid code.
#[async_trait]
pub trait EmailCodeVerifier: Send + Sync {
    async fn verify(&self, email: &str, code: &str) -> Result<bool, FederationError>;
}

#[derive(Default)]
pub struct DisabledEmailCodeVerifier;

#[async_trait]
impl EmailCodeVerifier for DisabledEmailCodeVerifier {
    async fn verify(&self, _: &str, _: &str) -> Result<bool, FederationError> {
        Ok(false)
    }
}

/// Valkey-backed verifier for Go's reusable email-binding code (`purpose=v`).
/// It intentionally performs a read-only comparison; the security-email
/// route uses [`ValkeyVerificationCodeStore::verify_and_consume`] separately.
#[derive(Clone)]
pub struct ValkeyEmailCodeVerifier {
    codes: ValkeyVerificationCodeStore,
}

impl ValkeyEmailCodeVerifier {
    #[must_use]
    pub fn new(valkey: redis::Client) -> Self {
        Self {
            codes: ValkeyVerificationCodeStore::new(valkey),
        }
    }

    #[must_use]
    pub fn with_dependency_timeout(mut self, dependency_timeout: Duration) -> Self {
        self.codes = self.codes.with_dependency_timeout(dependency_timeout);
        self
    }
}

#[async_trait]
impl EmailCodeVerifier for ValkeyEmailCodeVerifier {
    async fn verify(&self, email: &str, code: &str) -> Result<bool, FederationError> {
        self.codes
            .verify_without_consuming(email, code, EMAIL_VERIFICATION_PURPOSE)
            .await
            .map_err(|_| FederationError::Internal)
    }
}

/// Configuration for the built-in GitHub OAuth provider.  Credentials are
/// never serialized or copied to an authorization URL.
#[derive(Clone, Debug)]
pub struct GitHubOAuthConfig {
    client_id: String,
    client_secret: SecretString,
    redirect_uri: Url,
    token_endpoint: Url,
    user_endpoint: Url,
    timeout: Duration,
    fetch_policy: ProviderFetchPolicy,
}

#[derive(Clone, Copy, Debug)]
enum ProviderFetchPolicy {
    PublicHttps,
    #[cfg(test)]
    LoopbackFixture,
}

impl GitHubOAuthConfig {
    /// Reads the complete provider configuration from environment variables.
    /// An absent or partial configuration returns `None`, leaving GitHub
    /// explicitly disabled rather than accepting a callback that cannot be
    /// verified.
    pub fn from_env() -> Result<Option<Self>, FederationProviderError> {
        let client_id = std::env::var("GITHUB_CLIENT_ID").unwrap_or_default();
        let client_secret = std::env::var("GITHUB_CLIENT_SECRET").unwrap_or_default();
        let redirect_uri = std::env::var("GITHUB_REDIRECT_URI").unwrap_or_default();
        if client_id.trim().is_empty()
            && client_secret.trim().is_empty()
            && redirect_uri.trim().is_empty()
        {
            return Ok(None);
        }
        if client_id.trim().is_empty()
            || client_secret.trim().is_empty()
            || redirect_uri.trim().is_empty()
        {
            return Err(FederationProviderError::Disabled);
        }
        Self::new(client_id, SecretString::from(client_secret), &redirect_uri).map(Some)
    }

    pub fn new(
        client_id: impl Into<String>,
        client_secret: SecretString,
        redirect_uri: &str,
    ) -> Result<Self, FederationProviderError> {
        let client_id = client_id.into();
        if client_id.trim().is_empty() || client_secret.expose_secret().trim().is_empty() {
            return Err(FederationProviderError::Disabled);
        }
        let redirect_uri = public_https_url(redirect_uri)?;
        Ok(Self {
            client_id,
            client_secret,
            redirect_uri,
            token_endpoint: public_https_url("https://github.com/login/oauth/access_token")?,
            user_endpoint: public_https_url("https://api.github.com/user")?,
            timeout: Duration::from_secs(20),
            fetch_policy: ProviderFetchPolicy::PublicHttps,
        })
    }
}

/// A concrete GitHub exchange implementation matching the frozen Go oracle:
/// JSON token exchange followed by the authenticated `/user` request.
pub struct ConfiguredFederationProviders {
    github: Option<GitHubOAuthConfig>,
    client: Client,
    issuer: Arc<dyn FederatedLoginIssuer>,
}

impl ConfiguredFederationProviders {
    pub fn new(
        github: Option<GitHubOAuthConfig>,
        issuer: Arc<dyn FederatedLoginIssuer>,
    ) -> Result<Self, FederationProviderError> {
        let client = Client::builder()
            .redirect(Policy::none())
            .build()
            .map_err(|_| FederationProviderError::Unavailable)?;
        Ok(Self {
            github,
            client,
            issuer,
        })
    }

    async fn github_exchange(
        &self,
        config: &GitHubOAuthConfig,
        code: &str,
    ) -> Result<FederatedUser, FederationProviderError> {
        if code.is_empty() || code.len() > 4096 {
            return Err(FederationProviderError::InvalidCode);
        }
        validate_fetch_url(&config.token_endpoint, config.fetch_policy).await?;
        validate_fetch_url(&config.user_endpoint, config.fetch_policy).await?;
        let token = self
            .client
            .post(config.token_endpoint.clone())
            .timeout(config.timeout)
            .header("accept", "application/json")
            .json(&json!({
                "client_id": config.client_id,
                "client_secret": config.client_secret.expose_secret(),
                "code": code,
                "redirect_uri": config.redirect_uri.as_str(),
            }))
            .send()
            .await
            .map_err(|_| FederationProviderError::Unavailable)?;
        let token_status = token.status();
        if !token_status.is_success() {
            return Err(
                if token_status.is_server_error() || token_status.as_u16() == 429 {
                    FederationProviderError::Unavailable
                } else {
                    FederationProviderError::InvalidCode
                },
            );
        }
        let token: GitHubTokenResponse = token
            .json()
            .await
            .map_err(|_| FederationProviderError::InvalidCode)?;
        if !token.error.trim().is_empty() || token.access_token.trim().is_empty() {
            return Err(FederationProviderError::InvalidCode);
        }
        let response = self
            .client
            .get(config.user_endpoint.clone())
            .timeout(config.timeout)
            .bearer_auth(&token.access_token)
            .header("accept", "application/json")
            .send()
            .await
            .map_err(|_| FederationProviderError::Unavailable)?;
        let status = response.status();
        if !status.is_success() {
            return Err(if status.is_server_error() || status.as_u16() == 429 {
                FederationProviderError::Unavailable
            } else {
                FederationProviderError::InvalidIdentity
            });
        }
        let user: GitHubUserResponse = response
            .json()
            .await
            .map_err(|_| FederationProviderError::InvalidIdentity)?;
        if user.id <= 0 || user.login.trim().is_empty() {
            return Err(FederationProviderError::InvalidIdentity);
        }
        Ok(FederatedUser {
            provider_user_id: user.id.to_string(),
            legacy_provider_user_id: Some(user.login.clone()),
            username: user.login,
            display_name: user.name,
            email: user.email,
        })
    }
}

#[derive(Deserialize)]
struct GitHubTokenResponse {
    #[serde(default)]
    access_token: String,
    #[serde(default)]
    error: String,
}

#[derive(Deserialize)]
struct GitHubUserResponse {
    #[serde(default)]
    id: i64,
    #[serde(default)]
    login: String,
    #[serde(default)]
    name: String,
    #[serde(default)]
    email: String,
}

#[async_trait]
impl FederationProviders for ConfiguredFederationProviders {
    fn built_in_enabled(&self, provider: &str) -> bool {
        provider == "github" && self.github.is_some()
    }

    async fn exchange(
        &self,
        provider: &str,
        code: &str,
        _: &OAuthFlowContext,
    ) -> Result<FederatedUser, FederationProviderError> {
        match (provider, self.github.as_ref()) {
            ("github", Some(config)) => self.github_exchange(config, code).await,
            _ => Err(FederationProviderError::Disabled),
        }
    }

    async fn login(
        &self,
        provider: &str,
        user: FederatedUser,
        affiliate_code: &str,
        headers: &HeaderMap,
    ) -> Result<FederatedLogin, FederationError> {
        self.issuer
            .issue_login(provider, user, affiliate_code, headers)
            .await
    }
}

fn public_https_url(raw: &str) -> Result<Url, FederationProviderError> {
    let url = Url::parse(raw).map_err(|_| FederationProviderError::InvalidIdentity)?;
    if url.scheme() != "https"
        || url.username() != ""
        || url.password().is_some()
        || url.host_str().is_none()
        || url.port_or_known_default() != Some(443)
    {
        return Err(FederationProviderError::InvalidIdentity);
    }
    if let Some(host) = url.host_str()
        && let Ok(ip) = host.parse::<IpAddr>()
        && !public_ip(ip)
    {
        return Err(FederationProviderError::InvalidIdentity);
    }
    Ok(url)
}

async fn validate_fetch_url(
    url: &Url,
    policy: ProviderFetchPolicy,
) -> Result<(), FederationProviderError> {
    #[cfg(test)]
    if matches!(policy, ProviderFetchPolicy::LoopbackFixture) {
        let host = url.host_str().ok_or(FederationProviderError::Unavailable)?;
        let ip = host
            .parse::<IpAddr>()
            .map_err(|_| FederationProviderError::Unavailable)?;
        return (url.scheme() == "http"
            && ip.is_loopback()
            && url.username().is_empty()
            && url.password().is_none()
            && url.port().is_some())
        .then_some(())
        .ok_or(FederationProviderError::Unavailable);
    }
    let _ = policy;
    if url.scheme() != "https" || url.port_or_known_default() != Some(443) {
        return Err(FederationProviderError::Unavailable);
    }
    let host = url.host_str().ok_or(FederationProviderError::Unavailable)?;
    if let Ok(ip) = host.parse::<IpAddr>() {
        return public_ip(ip)
            .then_some(())
            .ok_or(FederationProviderError::Unavailable);
    }
    let addresses = tokio::net::lookup_host((host, 443))
        .await
        .map_err(|_| FederationProviderError::Unavailable)?;
    let mut found = false;
    for address in addresses {
        found = true;
        if !public_ip(address.ip()) {
            return Err(FederationProviderError::Unavailable);
        }
    }
    found
        .then_some(())
        .ok_or(FederationProviderError::Unavailable)
}

fn public_ip(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(ip) => {
            !ip.is_private()
                && !ip.is_loopback()
                && !ip.is_link_local()
                && !ip.is_unspecified()
                && !ip.is_broadcast()
                && !ip.is_documentation()
                && !ip.is_multicast()
        }
        IpAddr::V6(ip) => {
            !ip.is_loopback()
                && !ip.is_unspecified()
                && !ip.is_unique_local()
                && !ip.is_unicast_link_local()
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FederationPrincipal {
    pub user_id: i64,
    pub role: i64,
    pub session_id: String,
}

#[async_trait]
pub trait FederationIdentity: Send + Sync {
    async fn principal(&self, headers: &HeaderMap) -> Result<FederationPrincipal, FederationError>;
    async fn verify_email_code(&self, email: &str, code: &str) -> Result<bool, FederationError>;

    /// Applies Go's `TryUserAuth` classification. Missing or syntactically
    /// unmatched Authorization is anonymous; a recognized invalid credential
    /// remains an error and must stop the request before body/query parsing.
    async fn optional_principal(
        &self,
        headers: &HeaderMap,
    ) -> Result<Option<FederationPrincipal>, FederationError> {
        if dashboard_credential(headers).is_none() {
            return Ok(None);
        }
        self.principal(headers).await.map(Some)
    }

    /// Checks a persisted dashboard session when a bind callback returns
    /// without a browser Authorization header.  This prevents a captured state
    /// token from being used after logout, token rotation, or account disable.
    async fn validate_session_reference(&self, _: i64, _: &str) -> Result<(), FederationError> {
        Err(FederationError::Unauthorized)
    }
}

#[derive(Clone)]
pub struct FederationState {
    pool: PgPool,
    identity: Arc<dyn FederationIdentity>,
    providers: Arc<dyn FederationProviders>,
    mutation_publisher: Arc<dyn FederationMutationPublisher>,
    auth_flow_key: Arc<[u8]>,
}

/// Cache/session publication boundary for durable identity mutations.
///
/// A listener that cannot invalidate or republish the changed dashboard user
/// must leave the mutation routes unavailable. This prevents a successful
/// response while Valkey still serves the pre-bind identity.
#[async_trait]
pub trait FederationMutationPublisher: Send + Sync {
    fn configured(&self) -> bool;
    async fn publish_user(&self, user_id: i64) -> Result<(), FederationError>;
}

#[derive(Default)]
struct DisabledMutationPublisher;

#[async_trait]
impl FederationMutationPublisher for DisabledMutationPublisher {
    fn configured(&self) -> bool {
        false
    }

    async fn publish_user(&self, _: i64) -> Result<(), FederationError> {
        Err(FederationError::Internal)
    }
}

/// Invalidates the shared Go-compatible `user:{id}` hash after a durable
/// identity mutation.  The PostgreSQL row remains authoritative; deleting
/// the hash makes the next auth lookup refill it from that row instead of
/// serving a stale email.
#[derive(Clone)]
pub struct ValkeyFederationMutationPublisher {
    valkey: redis::Client,
    dependency_timeout: Duration,
}

impl ValkeyFederationMutationPublisher {
    #[must_use]
    pub fn new(valkey: redis::Client) -> Self {
        Self {
            valkey,
            dependency_timeout: Duration::from_secs(2),
        }
    }

    #[must_use]
    pub fn with_dependency_timeout(mut self, dependency_timeout: Duration) -> Self {
        self.dependency_timeout = dependency_timeout;
        self
    }
}

#[async_trait]
impl FederationMutationPublisher for ValkeyFederationMutationPublisher {
    fn configured(&self) -> bool {
        true
    }

    async fn publish_user(&self, user_id: i64) -> Result<(), FederationError> {
        if user_id <= 0 {
            return Err(FederationError::Internal);
        }
        let result = tokio::time::timeout(self.dependency_timeout, async {
            let mut connection = self
                .valkey
                .get_multiplexed_async_connection()
                .await
                .map_err(|_| FederationError::Internal)?;
            redis::cmd("DEL")
                .arg(format!("user:{user_id}"))
                .query_async::<i64>(&mut connection)
                .await
                .map_err(|_| FederationError::Internal)
        })
        .await;
        match result {
            Ok(result) => result.map(|_| ()),
            Err(_) => Err(FederationError::Internal),
        }
    }
}

impl FederationState {
    #[must_use]
    pub fn new(
        pool: PgPool,
        identity: Arc<dyn FederationIdentity>,
        session_secret: impl AsRef<[u8]>,
    ) -> Self {
        Self {
            pool,
            identity,
            providers: Arc::new(DisabledProviders),
            mutation_publisher: Arc::new(DisabledMutationPublisher),
            auth_flow_key: Arc::from(session_secret.as_ref()),
        }
    }

    /// Installs the listener's configured provider adapters.  `new` is safe
    /// for tests and pre-wiring deployments; it deliberately enables none.
    #[must_use]
    pub fn with_providers(mut self, providers: Arc<dyn FederationProviders>) -> Self {
        self.providers = providers;
        self
    }

    /// Installs the listener's authoritative user-cache/session publisher.
    #[must_use]
    pub fn with_mutation_publisher(
        mut self,
        mutation_publisher: Arc<dyn FederationMutationPublisher>,
    ) -> Self {
        self.mutation_publisher = mutation_publisher;
        self
    }

    fn flow_hash(&self, token: &str) -> Result<String, FederationError> {
        let mut mac = HmacSha256::new_from_slice(&self.auth_flow_key)
            .map_err(|_| FederationError::Internal)?;
        mac.update(b"auth-flow-v1:");
        mac.update(token.as_bytes());
        Ok(mac
            .finalize()
            .into_bytes()
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect())
    }
}

/// Routes for OAuth/OIDC federation and custom OAuth binding management.
pub fn router(state: FederationState) -> Router {
    provider_router(state.clone()).merge(bindings_router(state))
}

/// Builds only the provider/login-exchange routes.  The durable binding
/// management routes are intentionally exposed separately so a listener can
/// take ownership of PostgreSQL binding reads/deletes without also enabling a
/// remote OAuth provider or login issuer.
pub fn provider_router(state: FederationState) -> Router {
    Router::new()
        .route("/api/oauth/state", post(create_oauth_state))
        .route("/api/oauth/email/bind", post(bind_email))
        .with_state(state.clone())
        .merge(oauth_external_provider_router(state))
}

/// Mounts browser OAuth login-start routes for WeChat and Telegram.
pub fn oauth_login_start_router(state: FederationState) -> Router {
    Router::new()
        .route("/api/oauth/wechat/start", post(wechat_auth_start))
        .route("/api/oauth/telegram/login/start", post(telegram_login_start))
        .layer(DefaultBodyLimit::max(MAX_IDENTITY_BODY_BYTES))
        .with_state(state)
}

#[derive(Default, Deserialize)]
struct WeChatAuthStartRequest {
    accepted_legal: bool,
}

async fn wechat_auth_start(State(state): State<FederationState>, request: Request) -> Response {
    if !state.providers.wechat_enabled() {
        return failure(
            StatusCode::OK,
            "管理员未开启通过微信登录以及注册",
        );
    }
    let body = match axum::body::to_bytes(request.into_body(), MAX_IDENTITY_BODY_BYTES).await {
        Ok(body) => body,
        Err(_) => return failure(StatusCode::OK, "Invalid parameters"),
    };
    let input = match serde_json::from_slice::<WeChatAuthStartRequest>(&body) {
        Ok(input) => input,
        Err(_) => return failure(StatusCode::OK, "Invalid parameters"),
    };
    create_provider_login_flow(
        &state,
        "wechat_login",
        "wechat",
        "login",
        0,
        "",
        json!({"accepted_legal": input.accepted_legal}).to_string(),
        "wechat",
    )
    .await
}

async fn telegram_login_start(State(state): State<FederationState>, _: Request) -> Response {
    if !state.providers.telegram_enabled() {
        return failure(
            StatusCode::OK,
            "管理员未开启通过 Telegram 登录以及注册",
        );
    }
    create_provider_login_flow(
        &state,
        "telegram_login",
        "telegram",
        "login",
        0,
        "",
        "{}".to_owned(),
        "telegram",
    )
    .await
}

async fn create_provider_login_flow(
    state: &FederationState,
    purpose: &str,
    provider: &str,
    intent: &str,
    user_id: i64,
    session_id: &str,
    payload: String,
    cookie_provider: &str,
) -> Response {
    let token = random_urlsafe(32);
    let hash = match state.flow_hash(&token) {
        Ok(hash) => hash,
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    };
    let created = sqlx::query(
        "INSERT INTO auth_flows (token_hash, purpose, provider, intent, user_id, session_id, payload, created_at, expires_at) \
         VALUES ($1, $2, $3, $4, NULLIF($5, 0), NULLIF($6, ''), $7, NOW(), NOW() + make_interval(secs => $8)) \
         RETURNING EXTRACT(EPOCH FROM expires_at)::BIGINT",
    )
    .bind(hash)
    .bind(purpose)
    .bind(provider)
    .bind(intent)
    .bind(user_id)
    .bind(session_id)
    .bind(payload)
    .bind(OAUTH_FLOW_TTL.as_secs() as f64)
    .fetch_one(&state.pool)
    .await;
    let expires_at = match created.and_then(|row| row.try_get::<i64, _>(0)) {
        Ok(value) => value,
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    };
    let mut response = Json(Envelope {
        success: true,
        message: "",
        data: Some(json!({"flow_token": token, "expires_at": expires_at})),
    })
    .into_response();
    if let Ok(cookie) = axum::http::HeaderValue::from_str(&format!(
        "oauth_state_{cookie_provider}={token}; Path=/; HttpOnly; SameSite=Lax; Max-Age={}",
        OAUTH_FLOW_TTL.as_secs()
    )) {
        response.headers_mut().append(header::SET_COOKIE, cookie);
    }
    response
}

/// Mounts the external-provider login and bind routes.
///
/// A listener that has not installed a live [`FederationProviders`] adapter
/// still exposes these paths so they cannot 404 through to Go.  The default
/// disabled-provider boundary fails closed before any remote exchange.
pub fn oauth_external_provider_router(state: FederationState) -> Router {
    Router::new()
        .route("/api/oauth/{provider}", get(oauth_callback))
        .route("/api/oauth/wechat", get(wechat_login))
        .route("/api/oauth/wechat/bind", post(wechat_bind))
        .route("/api/oauth/telegram/login", get(telegram_login))
        .route("/api/oauth/telegram/bind/start", post(telegram_bind_start))
        .route("/api/oauth/telegram/bind/{flow_token}", get(telegram_bind))
        .with_state(state)
}

#[derive(Clone)]
struct OAuthStateRouteState {
    federation: FederationState,
    auth: Arc<dyn DashboardAuth>,
    body_limit_bytes: usize,
}

#[derive(Clone)]
struct OAuthEmailBindRouteState {
    federation: FederationState,
    auth: Arc<dyn DashboardAuth>,
}

/// Mounts only `POST /api/oauth/state` with the same route-local policy as
/// the Go router. Critical rate limiting runs before optional authentication
/// and body parsing; the cache headers are applied only after that gate has
/// allowed the request. External-provider callbacks live on
/// [`oauth_external_provider_router`] so the normal listener can mount them
/// independently of this state-only policy wrapper.
pub fn oauth_state_router(
    state: FederationState,
    auth: Arc<dyn DashboardAuth>,
    body_limit_bytes: usize,
) -> Router {
    let body_limit_bytes = body_limit_bytes.max(1);
    Router::new()
        .route(OAUTH_STATE_PATH, post(create_oauth_state_with_policy))
        .layer(DefaultBodyLimit::max(body_limit_bytes))
        .with_state(OAuthStateRouteState {
            federation: state,
            auth,
            body_limit_bytes,
        })
}

/// Mounts only `POST /api/oauth/email/bind` with Go's route-local order:
/// UserAuth, then CriticalRateLimit, then controller body parsing.  The
/// authenticated response always carries the legacy Auth-Version header,
/// including the empty 429/500 responses emitted by the critical limiter.
pub fn oauth_email_bind_router(state: FederationState, auth: Arc<dyn DashboardAuth>) -> Router {
    Router::new()
        .route(OAUTH_EMAIL_BIND_PATH, post(bind_email_with_policy))
        .layer(DefaultBodyLimit::max(MAX_IDENTITY_BODY_BYTES))
        .with_state(OAuthEmailBindRouteState {
            federation: state,
            auth,
        })
}

/// Builds only the durable custom-OAuth binding management routes.
///
/// The OAuth callback and provider-exchange routes remain on the candidate
/// router until their external-provider and login-issuer evidence is complete.
/// These four routes are PostgreSQL/session-authority operations and can be
/// mounted independently without exposing a half-configured provider flow.
pub fn bindings_router(state: FederationState) -> Router {
    bindings_routes().with_state(state)
}

fn bindings_routes() -> Router<FederationState> {
    Router::new()
        .route("/api/user/oauth/bindings", get(list_self_bindings))
        .route(
            "/api/user/oauth/bindings/{provider_id}",
            delete(unbind_self),
        )
        .route("/api/user/{id}/oauth/bindings", get(list_admin_bindings))
        .route(
            "/api/user/{id}/oauth/bindings/{provider_id}",
            delete(unbind_admin),
        )
}

#[derive(Debug, thiserror::Error)]
pub enum FederationError {
    #[error("unauthorized")]
    Unauthorized,
    #[error("token expired")]
    TokenExpired,
    #[error("session revoked")]
    SessionRevoked,
    #[error("user disabled")]
    UserDisabled,
    #[error("forbidden")]
    Forbidden,
    #[error("dependency failure")]
    Internal,
}

/// Concrete adapter for the shared `PgValkeyDashboardAuth` authentication
/// service.  It validates the signed access token's session binding before
/// returning a federation principal, so OAuth bind flows cannot outlive a
/// logout, account disable, or auth-version rotation.
pub struct DashboardFederationIdentity {
    auth: Arc<dyn DashboardAuth>,
    pool: PgPool,
    access_key: Arc<[u8]>,
    email_codes: Arc<dyn EmailCodeVerifier>,
}

impl DashboardFederationIdentity {
    pub fn new(
        auth: Arc<dyn DashboardAuth>,
        pool: PgPool,
        session_secret: &SecretString,
        email_codes: Arc<dyn EmailCodeVerifier>,
    ) -> Result<Self, FederationError> {
        if session_secret.expose_secret().trim().is_empty() {
            return Err(FederationError::Internal);
        }
        let mut mac = HmacSha256::new_from_slice(session_secret.expose_secret().as_bytes())
            .map_err(|_| FederationError::Internal)?;
        mac.update(b"new-api/auth/access/v1");
        Ok(Self {
            auth,
            pool,
            access_key: Arc::from(mac.finalize().into_bytes().to_vec()),
            email_codes,
        })
    }

    fn parse_access_token(&self, raw: &str) -> Result<FederationAccessClaims, FederationError> {
        if raw.is_empty() || raw.len() > 16 * 1024 {
            return Err(FederationError::Unauthorized);
        }
        let mut validation = Validation::new(Algorithm::HS256);
        validation.leeway = 5;
        validation.validate_nbf = true;
        validation.set_audience(&["new-api-dashboard"]);
        validation.set_issuer(&["new-api"]);
        validation.set_required_spec_claims(&["exp", "nbf", "iss", "aud", "sub"]);
        let claims = decode::<FederationAccessClaims>(
            raw,
            &DecodingKey::from_secret(&self.access_key),
            &validation,
        )
        .map_err(|_| FederationError::Unauthorized)?
        .claims;
        if claims.token_use != "access"
            || claims
                .sub
                .parse::<i64>()
                .ok()
                .filter(|id| *id > 0)
                .is_none()
            || claims.sid.trim().is_empty()
            || claims.uv <= 0
            || claims.sv <= 0
        {
            return Err(FederationError::Unauthorized);
        }
        Ok(claims)
    }
}

#[derive(Clone, Debug, Deserialize)]
struct FederationAccessClaims {
    token_use: String,
    sid: String,
    uv: i64,
    sv: i64,
    sub: String,
}

#[async_trait]
impl FederationIdentity for DashboardFederationIdentity {
    async fn principal(&self, headers: &HeaderMap) -> Result<FederationPrincipal, FederationError> {
        let token = dashboard_credential(headers).ok_or(FederationError::Unauthorized)?;
        let user = self
            .auth
            .self_user(SecretString::from(token.clone()))
            .await
            .map_err(map_auth_error)?;
        let session_id = match self.parse_access_token(&token) {
            Ok(claims) => {
                let user_id = claims
                    .sub
                    .parse::<i64>()
                    .map_err(|_| FederationError::Unauthorized)?;
                if user.id != user_id {
                    return Err(FederationError::Unauthorized);
                }
                // `self_user` already performed the shared session and
                // auth-version validation.
                claims.sid
            }
            // `DashboardAuth::self_user` has already resolved this exact
            // opaque value as a personal access token. PAT-authenticated Go
            // UserAuth requests do not carry a dashboard session id.
            Err(_) => String::new(),
        };
        if user.status != 1 {
            return Err(FederationError::UserDisabled);
        }
        if user.id <= 0 || user.role < 1 || user.username.trim().is_empty() {
            return Err(FederationError::Forbidden);
        }
        Ok(FederationPrincipal {
            user_id: user.id,
            role: user.role,
            session_id,
        })
    }

    async fn optional_principal(
        &self,
        headers: &HeaderMap,
    ) -> Result<Option<FederationPrincipal>, FederationError> {
        let Some(token) = dashboard_credential(headers) else {
            return Ok(None);
        };
        let user = self
            .auth
            .self_user_for_optional(SecretString::from(token.clone()))
            .await
            .map_err(map_auth_error)?;
        let session_id = match self.parse_access_token(&token) {
            Ok(claims) => {
                let user_id = claims
                    .sub
                    .parse::<i64>()
                    .map_err(|_| FederationError::Unauthorized)?;
                if user.id != user_id {
                    return Err(FederationError::Unauthorized);
                }
                // `self_user_for_optional` already performed the shared
                // session validation without applying UserAuth's enabled-role
                // policy, matching Go TryUserAuth.
                claims.sid
            }
            Err(_) => String::new(),
        };
        Ok(Some(FederationPrincipal {
            user_id: user.id,
            role: user.role,
            session_id,
        }))
    }

    async fn verify_email_code(&self, email: &str, code: &str) -> Result<bool, FederationError> {
        if email.is_empty() || code.is_empty() || code.len() > 256 {
            return Ok(false);
        }
        self.email_codes.verify(email, code).await
    }

    async fn validate_session_reference(
        &self,
        user_id: i64,
        session_id: &str,
    ) -> Result<(), FederationError> {
        if user_id <= 0 || session_id.trim().is_empty() {
            return Err(FederationError::Unauthorized);
        }
        let valid = sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS(SELECT 1 FROM users u JOIN user_sessions s ON s.user_id = u.id \
             WHERE u.id = $1 AND s.sid = $2 AND u.deleted_at IS NULL AND u.status = 1 \
             AND s.status = 'active' AND s.revoked_at = 0 \
             AND s.expires_at > EXTRACT(EPOCH FROM NOW())::BIGINT \
             AND s.user_auth_version = u.auth_version)",
        )
        .bind(user_id)
        .bind(session_id)
        .fetch_one(&self.pool)
        .await
        .map_err(|_| FederationError::Internal)?;
        valid.then_some(()).ok_or(FederationError::Unauthorized)
    }
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

fn map_auth_error(error: crate::auth::AuthError) -> FederationError {
    match error.kind {
        AuthErrorKind::TokenExpired => FederationError::TokenExpired,
        AuthErrorKind::SessionRevoked | AuthErrorKind::SessionMismatch => {
            FederationError::SessionRevoked
        }
        AuthErrorKind::UserDisabled => FederationError::UserDisabled,
        AuthErrorKind::Unauthorized | AuthErrorKind::InvalidCredentials => {
            FederationError::Unauthorized
        }
        _ => FederationError::Internal,
    }
}

#[derive(Serialize)]
struct Envelope<T: Serialize> {
    success: bool,
    message: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<T>,
}

fn failure(status: StatusCode, message: &'static str) -> Response {
    (
        status,
        Json(Envelope::<()> {
            success: false,
            message,
            data: None,
        }),
    )
        .into_response()
}

fn auth_failure(status: StatusCode, code: &'static str, message: &'static str) -> Response {
    (
        status,
        Json(json!({"success": false, "code": code, "message": message})),
    )
        .into_response()
}

fn federation_error_response(error: FederationError) -> Response {
    match error {
        FederationError::Unauthorized => auth_failure(
            StatusCode::UNAUTHORIZED,
            "AUTH_UNAUTHORIZED",
            "Unauthorized, invalid access token",
        ),
        FederationError::TokenExpired => auth_failure(
            StatusCode::UNAUTHORIZED,
            "AUTH_TOKEN_EXPIRED",
            "Token expired",
        ),
        FederationError::SessionRevoked => auth_failure(
            StatusCode::UNAUTHORIZED,
            "AUTH_SESSION_REVOKED",
            "Session revoked",
        ),
        FederationError::UserDisabled => auth_failure(
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_DISABLED",
            "User has been banned",
        ),
        FederationError::Forbidden => auth_failure(
            StatusCode::FORBIDDEN,
            "AUTH_INSUFFICIENT_PRIVILEGE",
            "Unauthorized, insufficient privileges",
        ),
        FederationError::Internal => {
            failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error")
        }
    }
}

async fn optional_principal(
    state: &FederationState,
    headers: &HeaderMap,
) -> Result<Option<FederationPrincipal>, Response> {
    state
        .identity
        .optional_principal(headers)
        .await
        .map_err(federation_error_response)
}

fn with_auth_version(mut response: Response, authenticated: bool) -> Response {
    if authenticated {
        response.headers_mut().insert(
            HeaderName::from_static("auth-version"),
            HeaderValue::from_static(AUTH_VERSION),
        );
    }
    response
}

fn with_disable_cache(mut response: Response) -> Response {
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

async fn principal(
    state: &FederationState,
    headers: &HeaderMap,
) -> Result<FederationPrincipal, Response> {
    let actor = state
        .identity
        .principal(headers)
        .await
        .map_err(federation_error_response)?;
    if actor.user_id <= 0 {
        return Err(auth_failure(
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_INVALID",
            "Unauthorized, invalid user information",
        ));
    }
    if actor.role < 1 {
        return Err(auth_failure(
            StatusCode::FORBIDDEN,
            "AUTH_INSUFFICIENT_PRIVILEGE",
            "Unauthorized, insufficient privileges",
        ));
    }
    Ok(actor)
}

#[derive(Deserialize)]
struct OAuthStateRequest {
    provider: String,
    intent: String,
    #[serde(default)]
    aff: String,
    #[serde(default)]
    accepted_legal: bool,
}

#[derive(Serialize, Deserialize, Default)]
struct OAuthFlowPayload {
    #[serde(default)]
    affiliate_code: String,
    #[serde(default, skip_serializing_if = "is_false")]
    accepted_legal: bool,
    #[serde(default)]
    pkce_verifier: String,
    #[serde(default)]
    nonce: String,
}

fn is_false(value: &bool) -> bool {
    !*value
}

fn is_built_in_provider(provider: &str) -> bool {
    matches!(provider, "github" | "discord" | "oidc" | "linuxdo")
}

fn random_urlsafe(bytes: usize) -> String {
    let mut random = vec![0_u8; bytes];
    rand::rng().fill_bytes(&mut random);
    URL_SAFE_NO_PAD.encode(random)
}

async fn create_oauth_state(State(state): State<FederationState>, request: Request) -> Response {
    create_oauth_state_request(&state, request, MAX_IDENTITY_BODY_BYTES).await
}

async fn create_oauth_state_with_policy(
    State(route): State<OAuthStateRouteState>,
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
    match route.auth.check_critical_rate_limit(&client_ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => {}
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => {
            return legacy_empty_response(StatusCode::TOO_MANY_REQUESTS, Some(retry_after_seconds));
        }
        Err(_) => return legacy_empty_response(StatusCode::INTERNAL_SERVER_ERROR, None),
    }
    with_disable_cache(
        create_oauth_state_request(&route.federation, request, route.body_limit_bytes).await,
    )
}

async fn create_oauth_state_request(
    state: &FederationState,
    request: Request,
    body_limit_bytes: usize,
) -> Response {
    let headers = request.headers().clone();
    let actor = match optional_principal(state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let authenticated = actor.is_some();
    let body = match to_bytes(request.into_body(), body_limit_bytes).await {
        Ok(body) => body,
        Err(_) => {
            return with_auth_version(failure(StatusCode::OK, "Invalid parameters"), authenticated);
        }
    };
    let request = match serde_json::from_slice::<OAuthStateRequest>(&body) {
        Ok(request) => request,
        Err(_) => {
            return with_auth_version(failure(StatusCode::OK, "Invalid parameters"), authenticated);
        }
    };
    let response = create_oauth_state_inner(state, actor, request).await;
    with_auth_version(response, authenticated)
}

async fn create_oauth_state_inner(
    state: &FederationState,
    actor: Option<FederationPrincipal>,
    request: OAuthStateRequest,
) -> Response {
    let provider = request.provider.trim();
    let intent = request.intent.trim();
    let affiliate = request.aff.trim();
    if provider.is_empty()
        || provider.len() > 64
        || !matches!(intent, "login" | "bind")
        || affiliate.len() > 32
        || (intent == "bind" && !affiliate.is_empty())
    {
        return failure(StatusCode::OK, "Invalid parameters");
    }
    let exists =
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM custom_oauth_providers WHERE slug = $1")
            .bind(provider)
            .fetch_one(&state.pool)
            .await;
    let provider_known = is_built_in_provider(provider) || matches!(exists, Ok(count) if count > 0);
    if !provider_known {
        return failure(StatusCode::OK, "Invalid parameters");
    }
    let (user_id, session_id) = if intent == "bind" {
        let Some(actor) = actor else {
            return auth_failure(
                StatusCode::UNAUTHORIZED,
                "AUTH_UNAUTHORIZED",
                "Unauthorized, invalid access token",
            );
        };
        if actor.session_id.is_empty() {
            return auth_failure(
                StatusCode::UNAUTHORIZED,
                "AUTH_UNAUTHORIZED",
                "Binding requires a dashboard session",
            );
        }
        (actor.user_id, actor.session_id)
    } else {
        (0, String::new())
    };
    let token = random_urlsafe(32);
    let hash = match state.flow_hash(&token) {
        Ok(hash) => hash,
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    };
    let payload = match serde_json::to_string(&OAuthFlowPayload {
        affiliate_code: affiliate.to_owned(),
        accepted_legal: intent == "login" && request.accepted_legal,
        // The frozen browser authorization builders do not advertise PKCE or
        // an OIDC nonce. Generating unused secrets would create a false
        // security contract, so those fields stay empty until both sides are
        // wired by a provider adapter.
        pkce_verifier: String::new(),
        nonce: String::new(),
    }) {
        Ok(payload) => payload,
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    };
    let result = sqlx::query(
        "INSERT INTO auth_flows (token_hash, purpose, provider, intent, user_id, session_id, payload, created_at, expires_at) VALUES ($1, 'oauth', $2, $3, NULLIF($4, 0), NULLIF($5, ''), $6, NOW(), NOW() + make_interval(secs => $7)) RETURNING EXTRACT(EPOCH FROM expires_at)::BIGINT",
    )
    .bind(hash)
    .bind(provider)
    .bind(intent)
    .bind(user_id)
    .bind(session_id)
    .bind(payload)
    .bind(OAUTH_FLOW_TTL.as_secs() as f64)
    .fetch_one(&state.pool)
    .await;
    let expires_at: i64 = match result.and_then(|row| row.try_get(0)) {
        Ok(expires_at) => expires_at,
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    };
    Json(Envelope {
        success: true,
        message: "",
        data: Some(json!({"flow_token": token, "expires_at": expires_at})),
    })
    .into_response()
}

#[derive(Default, Deserialize)]
#[serde(default)]
struct OAuthCallbackQuery {
    code: String,
    state: String,
    error: String,
}

struct PendingOAuthFlow {
    intent: String,
    user_id: i64,
    session_id: String,
    payload: OAuthFlowPayload,
}

async fn oauth_callback(
    State(state): State<FederationState>,
    Path(provider): Path<String>,
    request: Request,
) -> Response {
    let headers = request.headers().clone();
    let actor = match optional_principal(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let authenticated = actor.is_some();
    let query = oauth_callback_query(request.uri().query());
    let response = oauth_callback_inner(&state, &headers, actor, provider, query).await;
    with_auth_version(response, authenticated)
}

fn oauth_callback_query(raw_query: Option<&str>) -> OAuthCallbackQuery {
    let mut parsed = OAuthCallbackQuery::default();
    let Some(raw_query) = raw_query else {
        return parsed;
    };
    let mut url = Url::parse("http://127.0.0.1/").expect("static callback parser URL");
    url.set_query(Some(raw_query));
    for (key, value) in url.query_pairs() {
        let target = match key.as_ref() {
            "code" if parsed.code.is_empty() => &mut parsed.code,
            "state" if parsed.state.is_empty() => &mut parsed.state,
            "error" if parsed.error.is_empty() => &mut parsed.error,
            _ => continue,
        };
        *target = value.into_owned();
    }
    parsed
}

async fn oauth_callback_inner(
    state: &FederationState,
    headers: &HeaderMap,
    actor: Option<FederationPrincipal>,
    provider: String,
    query: OAuthCallbackQuery,
) -> Response {
    if provider.is_empty() || !provider_known(state, &provider).await {
        return failure(StatusCode::BAD_REQUEST, "unknown OAuth provider");
    }
    let state_token = query.state.trim();
    let pending = match load_flow(state, state_token, &provider).await {
        Ok(Some(flow)) => flow,
        Ok(None) => return failure(StatusCode::FORBIDDEN, "OAuth state is invalid"),
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    };
    if !matches!(pending.intent.as_str(), "login" | "bind") {
        return failure(StatusCode::OK, "Invalid parameters");
    }
    if pending.intent == "bind" && !bind_session_is_current(state, actor.as_ref(), &pending).await {
        return failure(StatusCode::FORBIDDEN, "OAuth state is invalid");
    }

    if pending.intent == "bind" && !state.mutation_publisher.configured() {
        return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error");
    }

    if !is_provider_enabled(state, &provider).await {
        return failure(StatusCode::OK, "OAuth provider is not enabled");
    }
    if !query.error.trim().is_empty() {
        if consume_flow(state, state_token, &provider, &pending)
            .await
            .is_err()
        {
            return failure(StatusCode::FORBIDDEN, "OAuth state is invalid");
        }
        return failure(StatusCode::OK, "OAuth authorization was denied");
    }
    if query.code.trim().is_empty() {
        return failure(StatusCode::OK, "invalid OAuth code");
    }
    let context = OAuthFlowContext {
        provider: provider.clone(),
        intent: pending.intent.clone(),
        user_id: pending.user_id,
        session_id: pending.session_id.clone(),
        affiliate_code: pending.payload.affiliate_code.clone(),
        pkce_verifier: pending.payload.pkce_verifier.clone(),
        nonce: pending.payload.nonce.clone(),
    };
    if (!context.pkce_verifier.is_empty() || !context.nonce.is_empty())
        && !state.providers.consumes_flow_secrets(&provider)
    {
        return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error");
    }
    let upstream_user = match state
        .providers
        .exchange(&provider, query.code.trim(), &context)
        .await
    {
        Ok(user) if !user.provider_user_id.trim().is_empty() => user,
        Ok(_) | Err(FederationProviderError::InvalidIdentity) => {
            return failure(StatusCode::OK, "OAuth user information is invalid");
        }
        Err(FederationProviderError::Disabled) => {
            return failure(StatusCode::OK, "OAuth provider is not enabled");
        }
        Err(FederationProviderError::InvalidCode) => {
            return failure(StatusCode::OK, "invalid OAuth code");
        }
        Err(FederationProviderError::Denied) => {
            return failure(StatusCode::OK, "OAuth authorization was denied");
        }
        Err(FederationProviderError::Unavailable) => {
            return failure(StatusCode::OK, "OAuth provider connection failed");
        }
    };
    if pending.intent == "bind" {
        return bind_oauth_identity(state, state_token, &provider, &pending, upstream_user).await;
    }
    if consume_flow(state, state_token, &provider, &pending)
        .await
        .is_err()
    {
        return failure(StatusCode::FORBIDDEN, "OAuth state is invalid");
    }
    match state
        .providers
        .login(&provider, upstream_user, &context.affiliate_code, headers)
        .await
    {
        Ok(login) => login_response(login),
        Err(FederationError::Unauthorized) => failure(StatusCode::OK, "OAuth user is banned"),
        Err(_) => failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    }
}

async fn provider_known(state: &FederationState, provider: &str) -> bool {
    if is_built_in_provider(provider) {
        return true;
    }
    matches!(
        sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*) FROM custom_oauth_providers WHERE slug = $1",
        )
        .bind(provider)
        .fetch_one(&state.pool)
        .await,
        Ok(count) if count == 1
    )
}

async fn is_provider_enabled(state: &FederationState, provider: &str) -> bool {
    if is_built_in_provider(provider) {
        return state.providers.built_in_enabled(provider);
    }
    matches!(
        sqlx::query_scalar::<_, bool>(
            "SELECT enabled FROM custom_oauth_providers WHERE slug = $1",
        )
        .bind(provider)
        .fetch_optional(&state.pool)
        .await,
        Ok(Some(true))
    )
}

async fn load_flow(
    state: &FederationState,
    token: &str,
    provider: &str,
) -> Result<Option<PendingOAuthFlow>, FederationError> {
    if token.is_empty() {
        return Ok(None);
    }
    let hash = state.flow_hash(token)?;
    let row = sqlx::query(
        "SELECT intent, COALESCE(user_id, 0), COALESCE(session_id, ''), COALESCE(payload, '') \
         FROM auth_flows WHERE token_hash = $1 AND purpose = 'oauth' AND provider = $2 \
         AND consumed_at IS NULL AND expires_at > NOW()",
    )
    .bind(hash)
    .bind(provider)
    .fetch_optional(&state.pool)
    .await
    .map_err(|_| FederationError::Internal)?;
    row.map(|row| {
        let payload = row
            .try_get::<String, _>(3)
            .map_err(|_| FederationError::Internal)?;
        let payload = serde_json::from_str(&payload).map_err(|_| FederationError::Internal)?;
        Ok(PendingOAuthFlow {
            intent: row.try_get(0).map_err(|_| FederationError::Internal)?,
            user_id: row.try_get(1).map_err(|_| FederationError::Internal)?,
            session_id: row.try_get(2).map_err(|_| FederationError::Internal)?,
            payload,
        })
    })
    .transpose()
}

async fn bind_session_is_current(
    state: &FederationState,
    actor: Option<&FederationPrincipal>,
    flow: &PendingOAuthFlow,
) -> bool {
    match actor {
        Some(identity) => {
            !identity.session_id.is_empty()
                && identity.user_id == flow.user_id
                && identity.session_id == flow.session_id
        }
        None => state
            .identity
            .validate_session_reference(flow.user_id, &flow.session_id)
            .await
            .is_ok(),
    }
}

async fn consume_flow(
    state: &FederationState,
    token: &str,
    provider: &str,
    flow: &PendingOAuthFlow,
) -> Result<(), FederationError> {
    let hash = state.flow_hash(token)?;
    let affected = sqlx::query(
        "UPDATE auth_flows SET consumed_at = NOW() WHERE token_hash = $1 AND purpose = 'oauth' \
         AND provider = $2 AND intent = $3 AND COALESCE(user_id, 0) = $4 \
         AND COALESCE(session_id, '') = $5 AND consumed_at IS NULL AND expires_at > NOW()",
    )
    .bind(hash)
    .bind(provider)
    .bind(&flow.intent)
    .bind(flow.user_id)
    .bind(&flow.session_id)
    .execute(&state.pool)
    .await
    .map_err(|_| FederationError::Internal)?
    .rows_affected();
    (affected == 1)
        .then_some(())
        .ok_or(FederationError::Unauthorized)
}

fn login_response(login: FederatedLogin) -> Response {
    let mut response = Json(Envelope {
        success: true,
        message: "",
        data: Some(login.data),
    })
    .into_response();
    if let Some(cookie) = login.refresh_cookie {
        response.headers_mut().append(header::SET_COOKIE, cookie);
    }
    response
}

async fn bind_oauth_identity(
    state: &FederationState,
    token: &str,
    provider: &str,
    flow: &PendingOAuthFlow,
    user: FederatedUser,
) -> Response {
    let mut tx = match state.pool.begin().await {
        Ok(tx) => tx,
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    };
    let hash = match state.flow_hash(token) {
        Ok(hash) => hash,
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    };
    let consumed = sqlx::query(
        "UPDATE auth_flows SET consumed_at = NOW() WHERE token_hash = $1 AND purpose = 'oauth' \
         AND provider = $2 AND intent = 'bind' AND user_id = $3 AND session_id = $4 \
         AND consumed_at IS NULL AND expires_at > NOW()",
    )
    .bind(hash)
    .bind(provider)
    .bind(flow.user_id)
    .bind(&flow.session_id)
    .execute(&mut *tx)
    .await;
    if !matches!(consumed, Ok(result) if result.rows_affected() == 1) {
        return failure(StatusCode::FORBIDDEN, "OAuth state is invalid");
    }
    let result = if let Some(column) = built_in_binding_column(provider) {
        match claim_external_identity(&mut tx, provider, &user.provider_user_id, flow.user_id).await
        {
            Ok(()) => {
                bind_builtin(
                    &mut tx,
                    flow.user_id,
                    column,
                    &user.provider_user_id,
                    user.legacy_provider_user_id.as_deref(),
                )
                .await
            }
            Err(error) => Err(error),
        }
    } else {
        bind_custom(&mut tx, flow.user_id, provider, &user.provider_user_id).await
    };
    match result {
        Ok(()) => {}
        Err(BindError::Taken) => {
            return failure(StatusCode::OK, "OAuth account is already bound");
        }
        Err(BindError::Internal) => {
            return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error");
        }
    };
    if tx.commit().await.is_err()
        || state
            .mutation_publisher
            .publish_user(flow.user_id)
            .await
            .is_err()
    {
        return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error");
    }
    Json(Envelope {
        success: true,
        message: "",
        data: Some(json!({"action": "bind"})),
    })
    .into_response()
}

fn built_in_binding_column(provider: &str) -> Option<&'static str> {
    match provider {
        "github" => Some("github_id"),
        "discord" => Some("discord_id"),
        "oidc" => Some("oidc_id"),
        "linuxdo" => Some("linux_do_id"),
        _ => None,
    }
}

#[derive(Debug)]
enum BindError {
    Taken,
    Internal,
}

fn map_bind_db_error(error: sqlx::Error) -> BindError {
    match error {
        sqlx::Error::Database(database) if database.code().as_deref() == Some("23505") => {
            BindError::Taken
        }
        _ => BindError::Internal,
    }
}

/// Claims a normalized external subject and a provider slot for one user.
///
/// The two transaction-scoped advisory locks make concurrent rebinds use a
/// deterministic order. The unique `(provider, subject)` and
/// `(provider, user_id)` constraints remain the final cross-process guard.
async fn claim_external_identity(
    tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    provider: &str,
    subject: &str,
    user_id: i64,
) -> Result<(), BindError> {
    let provider = provider.trim();
    let subject = subject.trim();
    if provider.is_empty()
        || provider.len() > 32
        || subject.is_empty()
        || subject.len() > 128
        || user_id <= 0
    {
        return Err(BindError::Taken);
    }
    let mut lock_keys = [
        format!("identity:{provider}:subject:{subject}"),
        format!("identity:{provider}:user:{user_id}"),
    ];
    lock_keys.sort();
    for key in lock_keys {
        sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended($1, 0))")
            .bind(key)
            .execute(&mut **tx)
            .await
            .map_err(map_bind_db_error)?;
    }

    let owner = sqlx::query_scalar::<_, i64>(
        "SELECT user_id FROM external_identity_claims \
         WHERE provider = $1 AND subject = $2 FOR UPDATE",
    )
    .bind(provider)
    .bind(subject)
    .fetch_optional(&mut **tx)
    .await
    .map_err(map_bind_db_error)?;
    if matches!(owner, Some(owner) if owner != user_id) {
        return Err(BindError::Taken);
    }

    let current = sqlx::query_scalar::<_, String>(
        "SELECT subject FROM external_identity_claims \
         WHERE provider = $1 AND user_id = $2 FOR UPDATE",
    )
    .bind(provider)
    .bind(user_id)
    .fetch_optional(&mut **tx)
    .await
    .map_err(map_bind_db_error)?;
    match current {
        Some(current) if current == subject => Ok(()),
        Some(_) => sqlx::query(
            "UPDATE external_identity_claims SET subject = $3 \
             WHERE provider = $1 AND user_id = $2",
        )
        .bind(provider)
        .bind(user_id)
        .bind(subject)
        .execute(&mut **tx)
        .await
        .map_err(map_bind_db_error)
        .map(|_| ()),
        None => sqlx::query(
            "INSERT INTO external_identity_claims (provider, subject, user_id, created_at) \
             VALUES ($1, $2, $3, NOW())",
        )
        .bind(provider)
        .bind(subject)
        .bind(user_id)
        .execute(&mut **tx)
        .await
        .map_err(map_bind_db_error)
        .map(|_| ()),
    }
}

async fn bind_builtin(
    tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    user_id: i64,
    column: &str,
    subject: &str,
    legacy_subject: Option<&str>,
) -> Result<(), BindError> {
    if subject.trim().is_empty() {
        return Err(BindError::Taken);
    }
    if let Some(legacy_subject) = legacy_subject.filter(|value| !value.trim().is_empty()) {
        let legacy_taken = sqlx::query_scalar::<_, bool>(&format!(
            "SELECT EXISTS (SELECT 1 FROM users WHERE {column} = $1 AND id <> $2)"
        ))
        .bind(legacy_subject)
        .bind(user_id)
        .fetch_one(&mut **tx)
        .await
        .map_err(map_bind_db_error)?;
        if legacy_taken {
            return Err(BindError::Taken);
        }
    }
    let sql = format!(
        "UPDATE users SET {column} = $1 WHERE id = $2 AND deleted_at IS NULL \
         AND NOT EXISTS (SELECT 1 FROM users WHERE {column} = $1 AND id <> $2)"
    );
    let affected = sqlx::query(&sql)
        .bind(subject)
        .bind(user_id)
        .execute(&mut **tx)
        .await
        .map_err(map_bind_db_error)?
        .rows_affected();
    (affected == 1).then_some(()).ok_or(BindError::Taken)
}

async fn bind_custom(
    tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    user_id: i64,
    provider: &str,
    subject: &str,
) -> Result<(), BindError> {
    let provider_id = sqlx::query_scalar::<_, i64>(
        "SELECT id FROM custom_oauth_providers WHERE slug = $1 AND enabled = TRUE FOR UPDATE",
    )
    .bind(provider)
    .fetch_optional(&mut **tx)
    .await
    .map_err(map_bind_db_error)?
    .ok_or(BindError::Taken)?;
    if subject.trim().is_empty() {
        return Err(BindError::Taken);
    }
    // `FOR UPDATE` above serializes rebinds for this provider.  The frozen Go
    // handler updates an existing binding for the same user rather than
    // rejecting it, while still rejecting an identity owned by another user.
    let owner = sqlx::query_scalar::<_, i64>(
        "SELECT user_id FROM user_oauth_bindings WHERE provider_id = $1 AND provider_user_id = $2 FOR UPDATE",
    )
    .bind(provider_id)
    .bind(subject)
    .fetch_optional(&mut **tx)
    .await
    .map_err(map_bind_db_error)?;
    if matches!(owner, Some(owner) if owner != user_id) {
        return Err(BindError::Taken);
    }
    let updated = sqlx::query(
        "UPDATE user_oauth_bindings SET provider_user_id = $3 \
         WHERE user_id = $1 AND provider_id = $2",
    )
    .bind(user_id)
    .bind(provider_id)
    .bind(subject)
    .execute(&mut **tx)
    .await
    .map_err(map_bind_db_error)?;
    if updated.rows_affected() == 0 {
        sqlx::query(
            "INSERT INTO user_oauth_bindings (user_id, provider_id, provider_user_id, created_at) \
             VALUES ($1, $2, $3, NOW())",
        )
        .bind(user_id)
        .bind(provider_id)
        .bind(subject)
        .execute(&mut **tx)
        .await
        .map_err(map_bind_db_error)?;
    }
    Ok(())
}

#[derive(Deserialize)]
struct WeChatBindRequest {
    code: String,
}

async fn wechat_login(
    State(state): State<FederationState>,
    headers: HeaderMap,
    Query(query): Query<OAuthCallbackQuery>,
) -> Response {
    if !state.providers.wechat_enabled() {
        return failure(StatusCode::OK, "WeChat login is not enabled");
    }
    let subject = match state.providers.exchange_wechat(query.code.trim()).await {
        Ok(subject) if !subject.trim().is_empty() => subject,
        Ok(_)
        | Err(FederationProviderError::InvalidIdentity | FederationProviderError::InvalidCode) => {
            return failure(StatusCode::OK, "verification code error");
        }
        Err(FederationProviderError::Disabled) => {
            return failure(StatusCode::OK, "WeChat login is not enabled");
        }
        Err(_) => return failure(StatusCode::OK, "WeChat provider connection failed"),
    };
    match state
        .providers
        .login(
            "wechat",
            FederatedUser {
                provider_user_id: subject,
                legacy_provider_user_id: None,
                username: String::new(),
                display_name: "WeChat User".to_owned(),
                email: String::new(),
            },
            "",
            &headers,
        )
        .await
    {
        Ok(login) => login_response(login),
        Err(FederationError::Unauthorized) => failure(StatusCode::OK, "OAuth user is banned"),
        Err(_) => failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    }
}

async fn wechat_bind(State(state): State<FederationState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let actor = match principal(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    if !state.mutation_publisher.configured() {
        return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error");
    }
    let body = match to_bytes(request.into_body(), MAX_IDENTITY_BODY_BYTES).await {
        Ok(body) => body,
        Err(_) => return failure(StatusCode::OK, "invalid request"),
    };
    let request = match serde_json::from_slice::<WeChatBindRequest>(&body) {
        Ok(request) => request,
        Err(_) => return failure(StatusCode::OK, "invalid request"),
    };
    if !state.providers.wechat_enabled() {
        return failure(StatusCode::OK, "WeChat login is not enabled");
    }
    let subject = match state.providers.exchange_wechat(request.code.trim()).await {
        Ok(subject) if !subject.trim().is_empty() => subject,
        Ok(_)
        | Err(FederationProviderError::InvalidIdentity | FederationProviderError::InvalidCode) => {
            return failure(StatusCode::OK, "verification code error");
        }
        Err(FederationProviderError::Disabled) => {
            return failure(StatusCode::OK, "WeChat login is not enabled");
        }
        Err(_) => return failure(StatusCode::OK, "WeChat provider connection failed"),
    };
    let mut tx = match state.pool.begin().await {
        Ok(tx) => tx,
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    };
    let result = match claim_external_identity(&mut tx, "wechat", &subject, actor.user_id).await {
        Ok(()) => bind_builtin(&mut tx, actor.user_id, "wechat_id", &subject, None).await,
        Err(error) => Err(error),
    };
    match result {
        Ok(()) => {}
        Err(BindError::Taken) => {
            return failure(StatusCode::OK, "WeChat account is already bound");
        }
        Err(BindError::Internal) => {
            return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error");
        }
    };
    if tx.commit().await.is_err()
        || state
            .mutation_publisher
            .publish_user(actor.user_id)
            .await
            .is_err()
    {
        return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error");
    }
    Json(Envelope::<()> {
        success: true,
        message: "",
        data: None,
    })
    .into_response()
}

const TELEGRAM_ASSERTION_TTL: Duration = Duration::from_secs(5 * 60);
const TELEGRAM_FUTURE_SKEW: Duration = Duration::from_secs(2 * 60);
const TELEGRAM_BIND_TTL: Duration = Duration::from_secs(5 * 60);

async fn telegram_login(
    State(state): State<FederationState>,
    headers: HeaderMap,
    Query(pairs): Query<Vec<(String, String)>>,
) -> Response {
    let Some(token) = state.providers.telegram_bot_token() else {
        return failure(StatusCode::OK, "Telegram login is not enabled");
    };
    let now = unix_now();
    let params = match unique_telegram_params(pairs) {
        Ok(params) => params,
        Err(_) => return failure(StatusCode::OK, "invalid request"),
    };
    let subject = match verify_telegram_authorization(&params, &token, now) {
        Ok(subject) => subject,
        Err(_) => return failure(StatusCode::OK, "invalid request"),
    };
    if state
        .providers
        .validate_existing_login("telegram", &subject)
        .await
        .is_err()
    {
        return failure(StatusCode::OK, "Telegram user does not exist");
    }
    let Some(expires_at) = telegram_assertion_expires_at(&params, now) else {
        return failure(StatusCode::OK, "invalid request");
    };
    if claim_telegram_assertion(
        &state,
        params.get("hash").map_or("", String::as_str),
        expires_at,
    )
    .await
    .is_err()
    {
        return failure(
            StatusCode::FORBIDDEN,
            "Telegram authorization has already been used",
        );
    }
    match state
        .providers
        .login(
            "telegram",
            FederatedUser {
                provider_user_id: subject,
                legacy_provider_user_id: None,
                username: String::new(),
                display_name: "Telegram User".to_owned(),
                email: String::new(),
            },
            "",
            &headers,
        )
        .await
    {
        Ok(login) => login_response(login),
        Err(FederationError::Unauthorized) => failure(StatusCode::OK, "OAuth user is banned"),
        Err(_) => failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    }
}

async fn telegram_bind_start(State(state): State<FederationState>, headers: HeaderMap) -> Response {
    let actor = match principal(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    if !state.providers.telegram_enabled() {
        return failure(StatusCode::OK, "Telegram login is not enabled");
    }
    let token = random_urlsafe(32);
    let hash = match state.flow_hash(&token) {
        Ok(hash) => hash,
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    };
    let created = sqlx::query(
        "INSERT INTO auth_flows (token_hash, purpose, user_id, session_id, created_at, expires_at) \
         VALUES ($1, 'telegram_bind', $2, $3, NOW(), NOW() + make_interval(secs => $4)) \
         RETURNING EXTRACT(EPOCH FROM expires_at)::BIGINT",
    )
    .bind(hash)
    .bind(actor.user_id)
    .bind(&actor.session_id)
    .bind(TELEGRAM_BIND_TTL.as_secs() as f64)
    .fetch_one(&state.pool)
    .await;
    let expires_at = match created.and_then(|row| row.try_get::<i64, _>(0)) {
        Ok(value) => value,
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    };
    Json(Envelope {
        success: true,
        message: "",
        data: Some(json!({
            "flow_token": token,
            "callback_url": format!("/api/oauth/telegram/bind/{token}"),
            "expires_at": expires_at,
        })),
    })
    .into_response()
}

async fn telegram_bind(
    State(state): State<FederationState>,
    Path(flow_token): Path<String>,
    Query(pairs): Query<Vec<(String, String)>>,
) -> Response {
    let failure_redirect = |code: &'static str| telegram_bind_redirect(&flow_token, false, code);
    let Some(bot_token) = state.providers.telegram_bot_token() else {
        return failure_redirect("TELEGRAM_BIND_DISABLED");
    };
    if !state.mutation_publisher.configured() {
        return failure_redirect("TELEGRAM_BIND_INTERNAL_ERROR");
    }
    let now = unix_now();
    let params = match unique_telegram_params(pairs) {
        Ok(params) => params,
        Err(_) => return failure_redirect("TELEGRAM_BIND_INVALID_REQUEST"),
    };
    let subject = match verify_telegram_authorization(&params, &bot_token, now) {
        Ok(subject) => subject,
        Err(_) => return failure_redirect("TELEGRAM_BIND_INVALID_REQUEST"),
    };
    let Some(assertion_expires_at) = telegram_assertion_expires_at(&params, now) else {
        return failure_redirect("TELEGRAM_BIND_INVALID_REQUEST");
    };
    let hash = match state.flow_hash(&flow_token) {
        Ok(hash) => hash,
        Err(_) => return failure_redirect("TELEGRAM_BIND_INTERNAL_ERROR"),
    };
    let flow = sqlx::query(
        "SELECT user_id, COALESCE(session_id, '') FROM auth_flows WHERE token_hash = $1 \
         AND purpose = 'telegram_bind' AND consumed_at IS NULL AND expires_at > NOW()",
    )
    .bind(&hash)
    .fetch_optional(&state.pool)
    .await;
    let Some(flow) = (match flow {
        Ok(flow) => flow,
        Err(_) => return failure_redirect("TELEGRAM_BIND_INTERNAL_ERROR"),
    }) else {
        return failure_redirect("TELEGRAM_BIND_FLOW_INVALID");
    };
    let user_id: i64 = match flow.try_get(0) {
        Ok(value) => value,
        Err(_) => return failure_redirect("TELEGRAM_BIND_INTERNAL_ERROR"),
    };
    let session_id: String = match flow.try_get(1) {
        Ok(value) => value,
        Err(_) => return failure_redirect("TELEGRAM_BIND_INTERNAL_ERROR"),
    };
    let assertion = match params.get("hash") {
        Some(assertion) => assertion,
        None => return failure_redirect("TELEGRAM_BIND_INVALID_REQUEST"),
    };
    match commit_telegram_bind(
        &state,
        TelegramBindCommit {
            flow_hash: &hash,
            user_id,
            session_id: &session_id,
            assertion,
            subject: &subject,
            now,
            assertion_expires_at,
        },
    )
    .await
    {
        Ok(()) => {
            if state
                .mutation_publisher
                .publish_user(user_id)
                .await
                .is_err()
            {
                failure_redirect("TELEGRAM_BIND_INTERNAL_ERROR")
            } else {
                telegram_bind_redirect(&flow_token, true, "")
            }
        }
        Err(TelegramBindError::Replay) => failure_redirect("TELEGRAM_BIND_INVALID_REQUEST"),
        Err(TelegramBindError::Taken) => failure_redirect("TELEGRAM_BIND_ALREADY_BOUND"),
        Err(TelegramBindError::UserDeleted) => failure_redirect("TELEGRAM_BIND_USER_DELETED"),
        Err(TelegramBindError::UserDisabled) => failure_redirect("TELEGRAM_BIND_USER_DISABLED"),
        Err(TelegramBindError::SessionInvalid) => failure_redirect("TELEGRAM_BIND_SESSION_INVALID"),
        Err(TelegramBindError::InvalidFlow) => failure_redirect("TELEGRAM_BIND_FLOW_INVALID"),
        Err(TelegramBindError::Internal) => failure_redirect("TELEGRAM_BIND_INTERNAL_ERROR"),
    }
}

#[derive(Debug, Eq, PartialEq)]
enum TelegramBindError {
    Replay,
    Taken,
    UserDeleted,
    UserDisabled,
    SessionInvalid,
    InvalidFlow,
    Internal,
}

/// Validates the Telegram Login Widget's HMAC-SHA256 authorization assertion.
#[derive(Clone, Copy, Debug, Eq, PartialEq, thiserror::Error)]
pub enum TelegramAuthorizationError {
    #[error("telegram authorization is invalid")]
    Invalid,
}

pub fn verify_telegram_authorization(
    params: &BTreeMap<String, String>,
    bot_token: &str,
    now: i64,
) -> Result<String, TelegramAuthorizationError> {
    if bot_token.is_empty() {
        return Err(TelegramAuthorizationError::Invalid);
    }
    let subject = params.get("id").map(String::as_str).unwrap_or_default();
    let hash = params.get("hash").map(String::as_str).unwrap_or_default();
    let auth_date = params
        .get("auth_date")
        .and_then(|value| value.parse::<i64>().ok())
        .ok_or(TelegramAuthorizationError::Invalid)?;
    if subject.is_empty()
        || hash.is_empty()
        || auth_date < now - TELEGRAM_ASSERTION_TTL.as_secs() as i64
        || auth_date > now + TELEGRAM_FUTURE_SKEW.as_secs() as i64
    {
        return Err(TelegramAuthorizationError::Invalid);
    }
    let payload = params
        .iter()
        .filter(|(key, _)| key.as_str() != "hash")
        .map(|(key, value)| format!("{key}={value}"))
        .collect::<Vec<_>>()
        .join("\n");
    let secret = sha2::Sha256::digest(bot_token.as_bytes());
    let mut mac =
        HmacSha256::new_from_slice(&secret).map_err(|_| TelegramAuthorizationError::Invalid)?;
    mac.update(payload.as_bytes());
    let supplied = hex::decode(hash).map_err(|_| TelegramAuthorizationError::Invalid)?;
    mac.verify_slice(&supplied)
        .map_err(|_| TelegramAuthorizationError::Invalid)?;
    Ok(subject.to_owned())
}

fn unique_telegram_params(
    pairs: Vec<(String, String)>,
) -> Result<BTreeMap<String, String>, TelegramAuthorizationError> {
    let mut params = BTreeMap::new();
    for (key, value) in pairs {
        if key.is_empty() || params.insert(key, value).is_some() {
            return Err(TelegramAuthorizationError::Invalid);
        }
    }
    Ok(params)
}

fn telegram_assertion_expires_at(params: &BTreeMap<String, String>, now: i64) -> Option<i64> {
    let auth_date = params.get("auth_date")?.parse::<i64>().ok()?;
    let expires_at = auth_date.checked_add(TELEGRAM_ASSERTION_TTL.as_secs() as i64)?;
    (expires_at > now).then_some(expires_at)
}

async fn claim_telegram_assertion(
    state: &FederationState,
    assertion: &str,
    expires_at: i64,
) -> Result<(), FederationError> {
    let hash = state.flow_hash(assertion)?;
    let affected = sqlx::query(
        "INSERT INTO auth_flows (token_hash, purpose, created_at, expires_at) \
         VALUES ($1, 'telegram_assertion', NOW(), to_timestamp($2)) \
         ON CONFLICT (token_hash) DO NOTHING",
    )
    .bind(hash)
    .bind(expires_at)
    .execute(&state.pool)
    .await
    .map_err(|_| FederationError::Internal)?
    .rows_affected();
    (affected == 1)
        .then_some(())
        .ok_or(FederationError::Unauthorized)
}

struct TelegramBindCommit<'a> {
    flow_hash: &'a str,
    user_id: i64,
    session_id: &'a str,
    assertion: &'a str,
    subject: &'a str,
    now: i64,
    assertion_expires_at: i64,
}

async fn commit_telegram_bind(
    state: &FederationState,
    commit: TelegramBindCommit<'_>,
) -> Result<(), TelegramBindError> {
    let TelegramBindCommit {
        flow_hash,
        user_id,
        session_id,
        assertion,
        subject,
        now,
        assertion_expires_at,
    } = commit;
    let mut tx = state
        .pool
        .begin()
        .await
        .map_err(|_| TelegramBindError::Internal)?;
    let user = sqlx::query(
        "SELECT deleted_at IS NOT NULL, status, auth_version, COALESCE(telegram_id, '') \
         FROM users WHERE id = $1 FOR UPDATE",
    )
    .bind(user_id)
    .fetch_optional(&mut *tx)
    .await
    .map_err(|_| TelegramBindError::Internal)?;
    let Some(user) = user else {
        return Err(TelegramBindError::UserDeleted);
    };
    let deleted: bool = user.try_get(0).map_err(|_| TelegramBindError::Internal)?;
    if deleted {
        return Err(TelegramBindError::UserDeleted);
    }
    let user_status: i64 = user.try_get(1).map_err(|_| TelegramBindError::Internal)?;
    let user_version: i64 = user.try_get(2).map_err(|_| TelegramBindError::Internal)?;
    let telegram_id: String = user.try_get(3).map_err(|_| TelegramBindError::Internal)?;
    if user_status != 1 {
        return Err(TelegramBindError::UserDisabled);
    }
    if !telegram_id.is_empty() {
        return Err(TelegramBindError::Taken);
    }

    let session = sqlx::query(
        "SELECT status, user_auth_version, expires_at, revoked_at \
         FROM user_sessions WHERE user_id = $1 AND sid = $2 FOR UPDATE",
    )
    .bind(user_id)
    .bind(session_id)
    .fetch_optional(&mut *tx)
    .await
    .map_err(|_| TelegramBindError::Internal)?;
    let Some(session) = session else {
        return Err(TelegramBindError::SessionInvalid);
    };
    let session_status: String = session
        .try_get(0)
        .map_err(|_| TelegramBindError::Internal)?;
    let session_version: i64 = session
        .try_get(1)
        .map_err(|_| TelegramBindError::Internal)?;
    let expires_at: i64 = session
        .try_get(2)
        .map_err(|_| TelegramBindError::Internal)?;
    let revoked_at: i64 = session
        .try_get(3)
        .map_err(|_| TelegramBindError::Internal)?;
    if session_status != "active"
        || revoked_at != 0
        || expires_at <= now
        || session_version != user_version
    {
        return Err(TelegramBindError::SessionInvalid);
    }
    let consumed = sqlx::query(
        "UPDATE auth_flows SET consumed_at = NOW() WHERE token_hash = $1 \
         AND purpose = 'telegram_bind' AND user_id = $2 AND session_id = $3 \
         AND consumed_at IS NULL AND expires_at > NOW()",
    )
    .bind(flow_hash)
    .bind(user_id)
    .bind(session_id)
    .execute(&mut *tx)
    .await
    .map_err(|_| TelegramBindError::Internal)?;
    if consumed.rows_affected() != 1 {
        return Err(TelegramBindError::InvalidFlow);
    }
    let assertion_hash = state
        .flow_hash(assertion)
        .map_err(|_| TelegramBindError::Internal)?;
    let claimed = sqlx::query(
        "INSERT INTO auth_flows (token_hash, purpose, created_at, expires_at) \
         VALUES ($1, 'telegram_assertion', NOW(), to_timestamp($2)) \
         ON CONFLICT (token_hash) DO NOTHING",
    )
    .bind(assertion_hash)
    .bind(assertion_expires_at)
    .execute(&mut *tx)
    .await
    .map_err(|_| TelegramBindError::Internal)?;
    if claimed.rows_affected() != 1 {
        return Err(TelegramBindError::Replay);
    }
    let external_claim = sqlx::query(
        "INSERT INTO external_identity_claims (provider, subject, user_id, created_at) \
         VALUES ('telegram', $1, $2, NOW()) \
         ON CONFLICT DO NOTHING",
    )
    .bind(subject)
    .bind(user_id)
    .execute(&mut *tx)
    .await
    .map_err(|_| TelegramBindError::Internal)?;
    if external_claim.rows_affected() != 1 {
        return Err(TelegramBindError::Taken);
    }
    let updated = sqlx::query(
        "UPDATE users SET telegram_id = $1 WHERE id = $2 AND deleted_at IS NULL \
         AND status = 1 AND COALESCE(telegram_id, '') = ''",
    )
    .bind(subject)
    .bind(user_id)
    .execute(&mut *tx)
    .await
    .map_err(|_| TelegramBindError::Internal)?;
    if updated.rows_affected() != 1 {
        return Err(TelegramBindError::Taken);
    }
    tx.commit().await.map_err(|_| TelegramBindError::Internal)
}

fn telegram_bind_redirect(flow_token: &str, success: bool, code: &str) -> Response {
    let flow_token = percent_encode_query_component(flow_token);
    let location = if success {
        format!("/oauth/telegram?telegram_bind=success&flow_token={flow_token}")
    } else {
        format!("/oauth/telegram?telegram_bind=error&flow_token={flow_token}&error_code={code}")
    };
    let mut response = StatusCode::FOUND.into_response();
    match HeaderValue::from_str(&location) {
        Ok(value) => {
            response.headers_mut().insert(header::LOCATION, value);
            response
        }
        Err(_) => failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    }
}

fn percent_encode_query_component(value: &str) -> String {
    const HEX: &[u8; 16] = b"0123456789ABCDEF";

    let mut encoded = String::with_capacity(value.len());
    for byte in value.bytes() {
        if byte == b' ' {
            encoded.push('+');
        } else if byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'.' | b'_' | b'~') {
            encoded.push(char::from(byte));
        } else {
            encoded.push('%');
            encoded.push(char::from(HEX[usize::from(byte >> 4)]));
            encoded.push(char::from(HEX[usize::from(byte & 0x0F)]));
        }
    }
    encoded
}

fn unix_now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs() as i64)
}

#[derive(Deserialize)]
struct EmailBindRequest {
    email: String,
    code: String,
}

async fn bind_email(State(state): State<FederationState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let actor = match principal(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    bind_email_after_auth(&state, actor, request).await
}

async fn bind_email_with_policy(
    State(route): State<OAuthEmailBindRouteState>,
    request: Request,
) -> Response {
    let headers = request.headers().clone();
    let actor = match principal(&route.federation, &headers).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
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
    match route.auth.check_critical_rate_limit(&client_ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => {}
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => {
            return with_auth_version(
                legacy_empty_response(StatusCode::TOO_MANY_REQUESTS, Some(retry_after_seconds)),
                true,
            );
        }
        Err(_) => {
            return with_auth_version(
                legacy_empty_response(StatusCode::INTERNAL_SERVER_ERROR, None),
                true,
            );
        }
    }
    with_auth_version(
        bind_email_after_auth(&route.federation, actor, request).await,
        true,
    )
}

async fn bind_email_after_auth(
    state: &FederationState,
    actor: FederationPrincipal,
    request: Request,
) -> Response {
    if !state.mutation_publisher.configured() {
        return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error");
    }
    let body = match to_bytes(request.into_body(), MAX_IDENTITY_BODY_BYTES).await {
        Ok(body) => body,
        Err(_) => return failure(StatusCode::OK, "invalid request body"),
    };
    let request = match serde_json::from_slice::<EmailBindRequest>(&body) {
        Ok(request) => request,
        Err(_) => return failure(StatusCode::OK, "invalid request body"),
    };
    let email = request.email.trim().to_ascii_lowercase();
    if email.is_empty() || request.code.trim().is_empty() {
        return failure(StatusCode::OK, "invalid request body");
    }
    match state
        .identity
        .verify_email_code(&email, request.code.trim())
        .await
    {
        Ok(true) => {}
        Ok(false) => return failure(StatusCode::OK, "verification code error"),
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    }
    let mut tx = match state.pool.begin().await {
        Ok(tx) => tx,
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    };
    if let Err(error) = claim_external_identity(&mut tx, "email", &email, actor.user_id).await {
        return match error {
            BindError::Taken => failure(StatusCode::OK, "email already taken"),
            BindError::Internal => {
                failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error")
            }
        };
    }
    let result = sqlx::query(
        "UPDATE users SET email = $1 WHERE id = $2 AND deleted_at IS NULL AND status = 1 \
         AND NOT EXISTS (SELECT 1 FROM users WHERE LOWER(email) = $1 AND id <> $2)",
    )
    .bind(&email)
    .bind(actor.user_id)
    .execute(&mut *tx)
    .await;
    match result {
        Ok(done) if done.rows_affected() == 1 => {}
        Ok(_) => return failure(StatusCode::OK, "email already taken"),
        Err(error) => {
            return match map_bind_db_error(error) {
                BindError::Taken => failure(StatusCode::OK, "email already taken"),
                BindError::Internal => {
                    failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error")
                }
            };
        }
    }
    if tx.commit().await.is_err()
        || state
            .mutation_publisher
            .publish_user(actor.user_id)
            .await
            .is_err()
    {
        return failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error");
    }
    Json(Envelope::<()> {
        success: true,
        message: "",
        data: None,
    })
    .into_response()
}

#[derive(Serialize)]
struct BindingView {
    provider_id: i64,
    provider_name: String,
    provider_slug: String,
    provider_icon: String,
    provider_user_id: String,
}

async fn binding_views(pool: &PgPool, user_id: i64) -> Result<Vec<BindingView>, FederationError> {
    let rows = sqlx::query(
        "SELECT b.provider_id, p.name, p.slug, COALESCE(p.icon, ''), b.provider_user_id FROM user_oauth_bindings b JOIN custom_oauth_providers p ON p.id = b.provider_id WHERE b.user_id = $1 ORDER BY b.id",
    )
    .bind(user_id)
    .fetch_all(pool)
    .await
    .map_err(|_| FederationError::Internal)?;
    rows.into_iter()
        .map(|row| {
            Ok(BindingView {
                provider_id: row.try_get(0).map_err(|_| FederationError::Internal)?,
                provider_name: row.try_get(1).map_err(|_| FederationError::Internal)?,
                provider_slug: row.try_get(2).map_err(|_| FederationError::Internal)?,
                provider_icon: row.try_get(3).map_err(|_| FederationError::Internal)?,
                provider_user_id: row.try_get(4).map_err(|_| FederationError::Internal)?,
            })
        })
        .collect()
}

async fn list_self_bindings(State(state): State<FederationState>, headers: HeaderMap) -> Response {
    let actor = match principal(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    match binding_views(&state.pool, actor.user_id).await {
        Ok(data) => Json(Envelope {
            success: true,
            message: "",
            data: Some(data),
        })
        .into_response(),
        Err(_) => failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    }
}

async fn unbind_self(
    State(state): State<FederationState>,
    headers: HeaderMap,
    Path(provider_id): Path<String>,
) -> Response {
    let actor = match principal(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let provider_id = match provider_id.parse::<i64>() {
        Ok(provider_id) => provider_id,
        Err(_) => return failure(StatusCode::OK, "无效的提供商 ID"),
    };
    delete_binding(&state.pool, actor.user_id, provider_id, "解绑成功")
        .await
        .0
}

async fn list_admin_bindings(
    State(state): State<FederationState>,
    headers: HeaderMap,
    Path(user_id): Path<String>,
) -> Response {
    let actor = match principal(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    if actor.role < 10 {
        return auth_failure(
            StatusCode::FORBIDDEN,
            "AUTH_INSUFFICIENT_PRIVILEGE",
            "Unauthorized, insufficient privileges",
        );
    }
    let user_id = match user_id.parse::<i64>() {
        Ok(user_id) => user_id,
        Err(_) => return failure(StatusCode::OK, "invalid user id"),
    };
    if !can_manage_binding_target(&state.pool, actor.role, user_id).await {
        return failure(StatusCode::OK, "no permission");
    }
    match binding_views(&state.pool, user_id).await {
        Ok(data) => Json(Envelope {
            success: true,
            message: "",
            data: Some(data),
        })
        .into_response(),
        Err(_) => failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
    }
}

async fn unbind_admin(
    State(state): State<FederationState>,
    headers: HeaderMap,
    Path((user_id, provider_id)): Path<(String, String)>,
) -> Response {
    let actor = match principal(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    if actor.role < 10 {
        return auth_failure(
            StatusCode::FORBIDDEN,
            "AUTH_INSUFFICIENT_PRIVILEGE",
            "Unauthorized, insufficient privileges",
        );
    }
    let user_id = match user_id.parse::<i64>() {
        Ok(user_id) => user_id,
        Err(_) => {
            let response = failure(StatusCode::OK, "invalid user id");
            record_oauth_unbind_audit(
                &state.pool,
                &actor,
                &user_id.to_string(),
                &provider_id,
                response.status(),
                false,
            )
            .await;
            return response;
        }
    };
    let provider_id = match provider_id.parse::<i64>() {
        Ok(provider_id) => provider_id,
        Err(_) => {
            let response = failure(StatusCode::OK, "invalid provider id");
            record_oauth_unbind_audit(
                &state.pool,
                &actor,
                &user_id.to_string(),
                &provider_id,
                response.status(),
                false,
            )
            .await;
            return response;
        }
    };
    if !can_manage_binding_target(&state.pool, actor.role, user_id).await {
        let response = failure(StatusCode::OK, "no permission");
        record_oauth_unbind_audit(
            &state.pool,
            &actor,
            &user_id.to_string(),
            &provider_id.to_string(),
            response.status(),
            false,
        )
        .await;
        return response;
    }
    let (response, success) = delete_binding(&state.pool, user_id, provider_id, "success").await;
    record_oauth_unbind_audit(
        &state.pool,
        &actor,
        &user_id.to_string(),
        &provider_id.to_string(),
        response.status(),
        success,
    )
    .await;
    response
}

async fn can_manage_binding_target(pool: &PgPool, actor_role: i64, user_id: i64) -> bool {
    matches!(
        sqlx::query_scalar::<_, i64>(
            "SELECT role FROM users WHERE id = $1 AND deleted_at IS NULL",
        )
        .bind(user_id)
        .fetch_optional(pool)
        .await,
        Ok(Some(target_role)) if actor_role == 100 || actor_role > target_role
    )
}

async fn delete_binding(
    pool: &PgPool,
    user_id: i64,
    provider_id: i64,
    message: &'static str,
) -> (Response, bool) {
    match sqlx::query("DELETE FROM user_oauth_bindings WHERE user_id = $1 AND provider_id = $2")
        .bind(user_id)
        .bind(provider_id)
        .execute(pool)
        .await
    {
        Ok(_) => (
            Json(Envelope::<()> {
                success: true,
                message,
                data: None,
            })
            .into_response(),
            true,
        ),
        Err(_) => (
            failure(StatusCode::INTERNAL_SERVER_ERROR, "internal server error"),
            false,
        ),
    }
}

/// Mirrors Go's AdminAuth middleware audit for the administrator binding
/// delete route. The audit is best-effort and must never change the response
/// returned by the binding handler.
async fn record_oauth_unbind_audit(
    pool: &PgPool,
    actor: &FederationPrincipal,
    user_id: &str,
    provider_id: &str,
    status: StatusCode,
    success: bool,
) {
    let username = sqlx::query_scalar::<_, String>("SELECT username FROM users WHERE id = $1")
        .bind(actor.user_id)
        .fetch_optional(pool)
        .await
        .ok()
        .flatten()
        .unwrap_or_default();
    let path = format!("/api/user/{user_id}/oauth/bindings/{provider_id}");
    let other = json!({
        "op": {"action": "user.oauth_unbind"},
        "admin_info": {
            "admin_id": actor.user_id,
            "admin_username": username,
            "admin_role": actor.role,
            "auth_method": "session",
        },
        "audit_info": {
            "method": "DELETE",
            "route": "/api/user/:id/oauth/bindings/:provider_id",
            "path": path,
            "status": status.as_u16(),
            "success": success,
            "params": {"id": user_id, "provider_id": provider_id},
        },
    });
    let _ = sqlx::query(
        "INSERT INTO logs (user_id, created_at, type, content, username, ip, other) VALUES ($1, EXTRACT(EPOCH FROM NOW())::BIGINT, 3, $2, $3, '', $4)",
    )
    .bind(actor.user_id)
    .bind("DELETE /api/user/:id/oauth/bindings/:provider_id")
    .bind(username)
    .bind(other.to_string())
    .execute(pool)
    .await;
}

#[cfg(test)]
mod adapter_tests {
    use super::*;
    use crate::auth::{
        AuthBundle, DashboardUser, LoginOutcome, LogoutRequest, LogoutResult, RequestMetadata,
        TwoFactorLoginRequest,
    };
    use axum::body::Body;
    use sqlx::postgres::PgPoolOptions;
    use tower::ServiceExt;

    struct NoIssuer;

    struct AnonymousIdentity;

    #[async_trait]
    impl FederationIdentity for AnonymousIdentity {
        async fn principal(&self, _: &HeaderMap) -> Result<FederationPrincipal, FederationError> {
            Err(FederationError::Unauthorized)
        }

        async fn verify_email_code(&self, _: &str, _: &str) -> Result<bool, FederationError> {
            Ok(false)
        }
    }

    struct AuthenticatedIdentity;

    #[async_trait]
    impl FederationIdentity for AuthenticatedIdentity {
        async fn principal(&self, _: &HeaderMap) -> Result<FederationPrincipal, FederationError> {
            Ok(FederationPrincipal {
                user_id: 7,
                role: 1,
                session_id: "session-7".to_owned(),
            })
        }

        async fn verify_email_code(&self, _: &str, _: &str) -> Result<bool, FederationError> {
            Ok(false)
        }
    }

    struct CriticalAuth {
        outcome: CriticalRateLimitOutcome,
    }

    #[async_trait]
    impl DashboardAuth for CriticalAuth {
        async fn check_critical_rate_limit(
            &self,
            _: &str,
        ) -> Result<CriticalRateLimitOutcome, crate::auth::AuthError> {
            Ok(self.outcome)
        }

        async fn login(
            &self,
            _: crate::auth::LoginRequest,
            _: RequestMetadata,
        ) -> Result<LoginOutcome, crate::auth::AuthError> {
            Err(crate::auth::AuthError::new(AuthErrorKind::Internal))
        }

        async fn login_2fa(
            &self,
            _: TwoFactorLoginRequest,
            _: RequestMetadata,
        ) -> Result<AuthBundle, crate::auth::AuthError> {
            Err(crate::auth::AuthError::new(AuthErrorKind::Internal))
        }

        async fn refresh(
            &self,
            _: SecretString,
            _: Option<String>,
            _: RequestMetadata,
        ) -> Result<AuthBundle, crate::auth::AuthError> {
            Err(crate::auth::AuthError::new(AuthErrorKind::Internal))
        }

        async fn self_user(
            &self,
            _: SecretString,
        ) -> Result<DashboardUser, crate::auth::AuthError> {
            Err(crate::auth::AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, crate::auth::AuthError> {
            Err(crate::auth::AuthError::new(AuthErrorKind::Internal))
        }

        async fn generate_personal_access_token(
            &self,
            _: SecretString,
        ) -> Result<String, crate::auth::AuthError> {
            Err(crate::auth::AuthError::new(AuthErrorKind::Internal))
        }
    }

    async fn fixture_token() -> Json<serde_json::Value> {
        Json(json!({"access_token": "fixture-token", "token_type": "Bearer"}))
    }

    async fn fixture_user(headers: HeaderMap) -> Response {
        if headers
            .get(header::AUTHORIZATION)
            .and_then(|value| value.to_str().ok())
            != Some("Bearer fixture-token")
        {
            return StatusCode::UNAUTHORIZED.into_response();
        }
        Json(json!({
            "id": 4242,
            "login": "fixture-user",
            "name": "Fixture User",
            "email": "fixture@example.test"
        }))
        .into_response()
    }

    #[async_trait]
    impl FederatedLoginIssuer for NoIssuer {
        async fn issue_login(
            &self,
            _: &str,
            _: FederatedUser,
            _: &str,
            _: &HeaderMap,
        ) -> Result<FederatedLogin, FederationError> {
            Err(FederationError::Internal)
        }
    }

    #[test]
    fn github_configuration_rejects_an_insecure_or_credential_bearing_redirect() {
        let secret = SecretString::from("client-secret".to_owned());
        assert!(
            GitHubOAuthConfig::new("client", secret.clone(), "http://example.test/callback")
                .is_err()
        );
        assert!(
            GitHubOAuthConfig::new(
                "client",
                secret,
                "https://client:secret@example.test/callback"
            )
            .is_err()
        );
    }

    #[test]
    fn oauth_login_flow_payload_preserves_accepted_legal_only_for_true() {
        let accepted = serde_json::to_value(OAuthFlowPayload {
            affiliate_code: "invite".to_owned(),
            accepted_legal: true,
            pkce_verifier: String::new(),
            nonce: String::new(),
        })
        .expect("serialize accepted legal payload");
        assert_eq!(accepted["accepted_legal"], true);
        assert_eq!(accepted["affiliate_code"], "invite");

        let omitted = serde_json::to_value(OAuthFlowPayload {
            affiliate_code: String::new(),
            accepted_legal: false,
            pkce_verifier: String::new(),
            nonce: String::new(),
        })
        .expect("serialize omitted legal payload");
        assert!(omitted.get("accepted_legal").is_none());
    }

    #[tokio::test]
    async fn oauth_state_critical_limit_runs_before_body_and_cache_policy() {
        let pool = PgPoolOptions::new()
            .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
            .expect("a lazy test pool is valid");
        let app = oauth_state_router(
            FederationState::new(pool, Arc::new(AnonymousIdentity), "test-secret"),
            Arc::new(CriticalAuth {
                outcome: CriticalRateLimitOutcome::Rejected {
                    retry_after_seconds: 7,
                },
            }),
            8,
        );
        let response = app
            .oneshot(
                axum::http::Request::post(OAUTH_STATE_PATH)
                    .header("content-type", "application/json")
                    .body(Body::from("not-json-but-the-limit-must-win"))
                    .expect("request builds"),
            )
            .await
            .expect("router response");
        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(response.headers()[header::RETRY_AFTER], "7");
        assert!(response.headers().get(header::CACHE_CONTROL).is_none());
        assert!(
            axum::body::to_bytes(response.into_body(), 128)
                .await
                .expect("body reads")
                .is_empty()
        );
    }

    #[tokio::test]
    async fn oauth_state_allowed_response_has_go_disable_cache_headers() {
        let pool = PgPoolOptions::new()
            .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
            .expect("a lazy test pool is valid");
        let app = oauth_state_router(
            FederationState::new(pool, Arc::new(AnonymousIdentity), "test-secret"),
            Arc::new(CriticalAuth {
                outcome: CriticalRateLimitOutcome::Allowed,
            }),
            1024,
        );
        let response = app
            .oneshot(
                axum::http::Request::post(OAUTH_STATE_PATH)
                    .header("content-type", "application/json")
                    .body(Body::from("not-json"))
                    .expect("request builds"),
            )
            .await
            .expect("router response");
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            response.headers()[header::CACHE_CONTROL],
            "no-store, no-cache, must-revalidate, private, max-age=0"
        );
        assert_eq!(response.headers()[header::PRAGMA], "no-cache");
        assert_eq!(response.headers()[header::EXPIRES], "0");
    }

    #[tokio::test]
    async fn oauth_email_bind_authenticates_before_critical_limit_and_preserves_auth_version() {
        let pool = PgPoolOptions::new()
            .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
            .expect("a lazy test pool is valid");
        let app = oauth_email_bind_router(
            FederationState::new(pool, Arc::new(AuthenticatedIdentity), "test-secret"),
            Arc::new(CriticalAuth {
                outcome: CriticalRateLimitOutcome::Rejected {
                    retry_after_seconds: 11,
                },
            }),
        );
        let response = app
            .oneshot(
                axum::http::Request::post(OAUTH_EMAIL_BIND_PATH)
                    .header("content-type", "application/json")
                    .body(Body::from("not-json-but-the-limit-must-win"))
                    .expect("request builds"),
            )
            .await
            .expect("router response");
        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(response.headers()[header::RETRY_AFTER], "11");
        assert_eq!(response.headers()["auth-version"], AUTH_VERSION);
        assert!(response.headers().get(header::CACHE_CONTROL).is_none());
        assert!(
            axum::body::to_bytes(response.into_body(), 128)
                .await
                .expect("body reads")
                .is_empty()
        );
    }

    #[tokio::test]
    async fn oauth_email_bind_missing_auth_wins_before_critical_limit() {
        let pool = PgPoolOptions::new()
            .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
            .expect("a lazy test pool is valid");
        let app = oauth_email_bind_router(
            FederationState::new(pool, Arc::new(AnonymousIdentity), "test-secret"),
            Arc::new(CriticalAuth {
                outcome: CriticalRateLimitOutcome::Rejected {
                    retry_after_seconds: 11,
                },
            }),
        );
        let response = app
            .oneshot(
                axum::http::Request::post(OAUTH_EMAIL_BIND_PATH)
                    .header("content-type", "application/json")
                    .body(Body::from("not-json"))
                    .expect("request builds"),
            )
            .await
            .expect("router response");
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert!(response.headers().get("auth-version").is_none());
    }

    #[test]
    fn ssrf_guard_rejects_private_and_documentation_addresses() {
        assert!(!public_ip("127.0.0.1".parse().expect("IP")));
        assert!(!public_ip("10.0.0.1".parse().expect("IP")));
        assert!(!public_ip("192.0.2.1".parse().expect("IP")));
        assert!(!public_ip("::1".parse().expect("IP")));
        assert!(public_ip("8.8.8.8".parse().expect("IP")));
    }

    #[tokio::test]
    async fn unconfigured_provider_is_explicitly_disabled_and_cannot_fake_a_login() {
        let providers = ConfiguredFederationProviders::new(None, Arc::new(NoIssuer))
            .expect("client construction");
        assert!(!providers.built_in_enabled("github"));
        assert!(matches!(
            providers
                .exchange(
                    "github",
                    "code",
                    &OAuthFlowContext {
                        provider: "github".to_owned(),
                        intent: "login".to_owned(),
                        user_id: 0,
                        session_id: String::new(),
                        affiliate_code: String::new(),
                        pkce_verifier: String::new(),
                        nonce: String::new(),
                    },
                )
                .await,
            Err(FederationProviderError::Disabled)
        ));
    }

    #[tokio::test]
    async fn configured_provider_rejects_an_oversized_code_without_network_access() {
        let config = GitHubOAuthConfig::new(
            "client",
            SecretString::from("client-secret".to_owned()),
            "https://app.example.test/api/oauth/github",
        )
        .expect("valid config");
        let providers = ConfiguredFederationProviders::new(Some(config), Arc::new(NoIssuer))
            .expect("client construction");
        assert!(matches!(
            providers
                .exchange(
                    "github",
                    &"x".repeat(4097),
                    &OAuthFlowContext {
                        provider: "github".to_owned(),
                        intent: "login".to_owned(),
                        user_id: 0,
                        session_id: String::new(),
                        affiliate_code: String::new(),
                        pkce_verifier: String::new(),
                        nonce: String::new(),
                    },
                )
                .await,
            Err(FederationProviderError::InvalidCode)
        ));
    }

    #[tokio::test]
    async fn configured_github_provider_uses_compile_time_loopback_fixture() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind loopback fixture");
        let address = listener.local_addr().expect("fixture address");
        let fixture = Router::new()
            .route("/token", post(fixture_token))
            .route("/user", get(fixture_user));
        let task = tokio::spawn(async move {
            axum::serve(listener, fixture)
                .await
                .expect("serve loopback fixture");
        });
        let base = Url::parse(&format!("http://{address}/")).expect("fixture base URL");
        let config = GitHubOAuthConfig {
            client_id: "fixture-client".to_owned(),
            client_secret: SecretString::from("fixture-secret".to_owned()),
            redirect_uri: Url::parse("https://app.example.test/api/oauth/github")
                .expect("redirect URL"),
            token_endpoint: base.join("token").expect("token URL"),
            user_endpoint: base.join("user").expect("user URL"),
            timeout: Duration::from_secs(2),
            fetch_policy: ProviderFetchPolicy::LoopbackFixture,
        };
        let providers = ConfiguredFederationProviders::new(Some(config), Arc::new(NoIssuer))
            .expect("client construction");
        let user = providers
            .exchange(
                "github",
                "fixture-code",
                &OAuthFlowContext {
                    provider: "github".to_owned(),
                    intent: "login".to_owned(),
                    user_id: 0,
                    session_id: String::new(),
                    affiliate_code: String::new(),
                    pkce_verifier: String::new(),
                    nonce: String::new(),
                },
            )
            .await
            .expect("loopback exchange");
        assert_eq!(user.provider_user_id, "4242");
        assert_eq!(user.username, "fixture-user");
        task.abort();
    }
}
