//! Unmounted advanced channel-operation route candidates.
//!
//! This slice deliberately contains no database, cache, or upstream client.
//! Its provider boundary owns those concerns, while the HTTP layer preserves
//! the dashboard authorization and legacy-envelope boundary for oracle tests.

use std::{
    net::IpAddr,
    sync::Arc,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::{Body, Bytes, to_bytes},
    extract::{Path, RawQuery, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{delete, get, post},
};
use chrono::{SecondsFormat, Utc};
use secrecy::{ExposeSecret, SecretString};
use serde::Serialize;
use serde_json::{Value, json};
use sqlx::{PgPool, Row};

const DEFAULT_ADVANCED_UPSTREAM_TIMEOUT: Duration = Duration::from_secs(15);
const DEFAULT_ADVANCED_RESPONSE_LIMIT: usize = 2 * 1024 * 1024;
const DEFAULT_ADVANCED_REQUEST_LIMIT: usize = 2 * 1024 * 1024;
const OLLAMA_PULL_TIMEOUT: Duration = Duration::from_secs(30 * 60);
const CODEX_OAUTH_CLIENT_ID: &str = "app_EMoamEEZ73f0CkXaXp7hrann";

use crate::auth::DashboardAuth;

const ROOT_ROLE: i64 = 100;
const STATUS_ENABLED: i64 = 1;
const CHANNEL_TYPE_OLLAMA: i64 = 4;
const CHANNEL_TYPE_CODEX: i64 = 57;

/// The frozen policy class for a candidate advanced channel operation.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ChannelAdvancedPermission {
    /// A dashboard administrator may inspect the operation result.
    Read,
    /// A dashboard administrator may ask the provider to mutate a channel.
    Write,
    /// A request can expose or rotate provider credentials.
    SensitiveWrite,
    /// A channel-level upstream operation is allowed for an administrator.
    Operate,
    /// A request can return a channel credential and therefore requires root.
    Root,
}

/// The normalized operation submitted to the provider boundary.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ChannelAdvancedOperation {
    CodexRefresh,
    CodexUsage,
    CodexUsageReset,
    CodexUsageResetCredits,
    ChannelKey,
    FetchModels,
    FetchUpstreamModels,
    OllamaDelete,
    OllamaPull,
    OllamaPullStream,
    OllamaVersion,
    TestAll,
    TestOne,
    UpdateAllBalances,
    UpdateBalance,
    ApplyUpstreamUpdates,
    ApplyAllUpstreamUpdates,
    DetectUpstreamUpdates,
    DetectAllUpstreamUpdates,
}

/// A request that has passed HTTP validation and authorization.
#[derive(Clone, Debug, PartialEq)]
pub struct ChannelAdvancedCall {
    /// The exact provider action requested by a legacy route.
    pub operation: ChannelAdvancedOperation,
    /// Route channel id, if the legacy route addresses one channel.
    pub channel_id: Option<i64>,
    /// Canonical JSON body or query object supplied to the provider.
    pub input: Value,
}

/// Errors translated by this route slice without leaking provider details.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ChannelAdvancedError {
    /// The Go `ConsoleAccessGate` conceals this dashboard discovery surface
    /// before `AdminAuth` sees anonymous, invalid, disabled, or unactivated
    /// credentials.
    ConsoleNotFound,
    Unauthorized,
    /// AdminAuth or RootAuth rejected the signed dashboard role.
    InsufficientPrivilege,
    /// RequirePermission rejected the signed user's channel capability.
    PermissionDenied,
    /// Compatibility denial for fakes that do not distinguish the middleware.
    Forbidden,
    /// A persisted channel required by the operation no longer exists.
    NotFound,
    /// The stored channel does not support the requested provider operation.
    UnsupportedChannel,
    /// A Codex-only operation targeted another persisted channel type.
    CodexChannelRequired,
    /// An Ollama-only operation targeted another persisted channel type.
    OllamaChannelRequired,
    /// A Codex usage operation cannot select a multi-key channel.
    MultiKeyUnsupported,
    /// The legacy balance operation cannot select a multi-key channel.
    MultiKeyBalanceUnsupported,
    Invalid,
    Provider,
}

impl ChannelAdvancedError {
    fn response(self) -> Response {
        match self {
            Self::ConsoleNotFound => {
                let mut response =
                    (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response();
                response.headers_mut().insert(
                    header::CONTENT_TYPE,
                    HeaderValue::from_static("application/json; charset=utf-8"),
                );
                response
            }
            Self::Unauthorized => (
                StatusCode::UNAUTHORIZED,
                Json(json!({
                    "success": false,
                    "message": "Unauthorized, invalid access token",
                    "code": "AUTH_UNAUTHORIZED"
                })),
            )
                .into_response(),
            Self::InsufficientPrivilege => (
                StatusCode::FORBIDDEN,
                Json(json!({
                    "success": false,
                    "message": "Unauthorized, insufficient privileges",
                    "code": "AUTH_INSUFFICIENT_PRIVILEGE"
                })),
            )
                .into_response(),
            Self::PermissionDenied | Self::Forbidden => (
                StatusCode::FORBIDDEN,
                Json(json!({
                    "success": false,
                    "message": "Unauthorized, insufficient privileges"
                })),
            )
                .into_response(),
            Self::NotFound => (
                StatusCode::NOT_FOUND,
                Json(json!({"success": false, "message": "Channel not found"})),
            )
                .into_response(),
            Self::UnsupportedChannel => (
                StatusCode::BAD_REQUEST,
                Json(json!({
                    "success": false,
                    "message": "This operation is not supported for this channel"
                })),
            )
                .into_response(),
            Self::CodexChannelRequired => Json(LegacyEnvelope::<Value>::failure(
                "channel type is not Codex",
            ))
            .into_response(),
            Self::OllamaChannelRequired => (
                StatusCode::BAD_REQUEST,
                Json(LegacyEnvelope::<Value>::failure(
                    "This operation is only supported for Ollama channels",
                )),
            )
                .into_response(),
            Self::MultiKeyUnsupported => Json(LegacyEnvelope::<Value>::failure(
                "multi-key channel is not supported",
            ))
            .into_response(),
            Self::MultiKeyBalanceUnsupported => {
                Json(LegacyEnvelope::<Value>::failure("多密钥渠道不支持余额查询")).into_response()
            }
            Self::Invalid => Json(LegacyEnvelope::<Value>::failure("参数错误")).into_response(),
            Self::Provider => (
                StatusCode::BAD_GATEWAY,
                Json(LegacyEnvelope::<Value>::failure("渠道上游操作失败")),
            )
                .into_response(),
        }
    }
}

/// Authentication and dashboard-permission boundary for this candidate slice.
#[async_trait]
pub trait ChannelAdvancedAuthorizer: Send + Sync {
    /// Verifies the signed dashboard credential and required frozen permission.
    async fn authorize(
        &self,
        headers: &HeaderMap,
        permission: ChannelAdvancedPermission,
    ) -> Result<(), ChannelAdvancedError>;
}

/// Production adapter that bases decisions only on the signed dashboard user.
#[derive(Clone)]
pub struct DashboardChannelAdvancedAuthorizer {
    auth: Arc<dyn DashboardAuth>,
}

impl DashboardChannelAdvancedAuthorizer {
    #[must_use]
    pub fn new(auth: Arc<dyn DashboardAuth>) -> Self {
        Self { auth }
    }
}

#[async_trait]
impl ChannelAdvancedAuthorizer for DashboardChannelAdvancedAuthorizer {
    async fn authorize(
        &self,
        headers: &HeaderMap,
        permission: ChannelAdvancedPermission,
    ) -> Result<(), ChannelAdvancedError> {
        // The normal Go listener applies ConsoleAccessGate to `/api/channel`
        // before the nested AdminAuth/RequirePermission chain.  Resolving the
        // optional dashboard view first preserves that generic 404 boundary;
        // only an activated principal reaches the legacy role/permission
        // decisions below.
        let token = dashboard_credential(headers).ok_or(ChannelAdvancedError::ConsoleNotFound)?;
        let user = self
            .auth
            .self_user_view_for_optional(SecretString::from(token.to_owned()))
            .await
            .map_err(|_| ChannelAdvancedError::ConsoleNotFound)?;
        if user.id <= 0 || user.status != STATUS_ENABLED || !user.developer_access_granted {
            return Err(ChannelAdvancedError::ConsoleNotFound);
        }
        // The frozen Go router applies AdminAuth to the entire route group.
        // The signed `DashboardAuth` record, not request headers, supplies
        // the additional channel permission for the eighteen non-root routes.
        if user.role < 10 {
            return Err(ChannelAdvancedError::InsufficientPrivilege);
        }
        if permission == ChannelAdvancedPermission::Root {
            return (user.role >= ROOT_ROLE)
                .then_some(())
                .ok_or(ChannelAdvancedError::InsufficientPrivilege);
        }
        if user.role == ROOT_ROLE || channel_permission(&user.permissions, permission) {
            return Ok(());
        }
        Err(ChannelAdvancedError::PermissionDenied)
    }
}

fn dashboard_credential(headers: &HeaderMap) -> Option<&str> {
    let value = headers
        .get(axum::http::header::AUTHORIZATION)?
        .to_str()
        .ok()?;
    let mut parts = value.split_whitespace();
    let first = parts.next()?;
    let second = parts.next();
    if parts.next().is_some() {
        return None;
    }
    match second {
        Some(token) if first.eq_ignore_ascii_case("bearer") && !token.is_empty() => Some(token),
        None if !first.is_empty() => Some(first),
        _ => None,
    }
}

fn channel_permission(permissions: &Value, permission: ChannelAdvancedPermission) -> bool {
    let permission = match permission {
        ChannelAdvancedPermission::Read => "read",
        ChannelAdvancedPermission::Write => "write",
        ChannelAdvancedPermission::SensitiveWrite => "sensitive_write",
        ChannelAdvancedPermission::Operate => "operate",
        ChannelAdvancedPermission::Root => return false,
    };
    permissions
        .get("admin_permissions")
        .and_then(|permissions| permissions.get("channel"))
        .and_then(|permissions| permissions.get(permission))
        .and_then(Value::as_bool)
        .unwrap_or(false)
}

/// Provider boundary for all upstream, persistence, and secret-bearing work.
#[async_trait]
pub trait ChannelAdvancedProvider: Send + Sync {
    /// Executes a normalized operation and returns the legacy `data` payload.
    async fn execute(&self, call: ChannelAdvancedCall) -> Result<Value, ChannelAdvancedError>;

    /// Executes an operation with an exact frozen response when it differs
    /// from the common `{success,message,data}` success envelope.
    async fn execute_reply(
        &self,
        call: ChannelAdvancedCall,
    ) -> Result<ChannelAdvancedReply, ChannelAdvancedError> {
        self.execute(call).await.map(ChannelAdvancedReply::success)
    }
}

/// A provider-produced legacy response that the route layer forwards intact.
pub enum ChannelAdvancedReply {
    /// A JSON response with the exact legacy HTTP status and body.
    Json { status: StatusCode, body: Value },
    /// A raw response, used by the Ollama pull-stream adapter for SSE.
    Raw(Response),
}

impl ChannelAdvancedReply {
    /// Wraps a normal successful provider payload in the common envelope.
    #[must_use]
    pub fn success(data: Value) -> Self {
        Self::Json {
            status: StatusCode::OK,
            body: json!({"success": true, "message": "", "data": data}),
        }
    }

    /// Preserves a route-specific legacy JSON body and HTTP status.
    #[must_use]
    pub fn new(status: StatusCode, body: Value) -> Self {
        Self::Json { status, body }
    }

    /// Forwards a streaming or otherwise non-JSON legacy upstream response.
    #[must_use]
    pub fn from_response(response: Response) -> Self {
        Self::Raw(response)
    }

    fn into_response(self) -> Response {
        match self {
            Self::Json { status, body } => (status, Json(body)).into_response(),
            Self::Raw(response) => response,
        }
    }
}

/// Persisted channel kinds that have special advanced-operation semantics.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ChannelAdvancedKind {
    /// A channel authenticated by a Codex OAuth credential.
    Codex,
    /// A channel served by an Ollama-compatible upstream.
    Ollama,
    /// Any other legacy channel type, retained as its database type code.
    Other(i64),
}

/// The secret-bearing channel configuration loaded only after authorization.
#[derive(Clone, Debug)]
pub struct ChannelAdvancedChannel {
    id: i64,
    kind: ChannelAdvancedKind,
    name: String,
    base_url: String,
    credential: SecretString,
    multi_key: bool,
}

impl ChannelAdvancedChannel {
    /// Builds a channel record at the persistence boundary.
    #[must_use]
    pub fn new(
        id: i64,
        kind: ChannelAdvancedKind,
        name: String,
        base_url: String,
        credential: SecretString,
        multi_key: bool,
    ) -> Self {
        Self {
            id,
            kind,
            name,
            base_url,
            credential,
            multi_key,
        }
    }

    /// Returns the stable database identifier.
    #[must_use]
    pub const fn id(&self) -> i64 {
        self.id
    }

    /// Returns the persisted legacy channel type.
    #[must_use]
    pub const fn kind(&self) -> ChannelAdvancedKind {
        self.kind
    }

    /// Returns the channel name for safe operation logging.
    #[must_use]
    pub fn name(&self) -> &str {
        &self.name
    }

    /// Returns the normalized upstream base URL.
    #[must_use]
    pub fn base_url(&self) -> &str {
        &self.base_url
    }

    /// Returns the provider credential only to a trusted upstream adapter.
    #[must_use]
    pub fn credential(&self) -> &SecretString {
        &self.credential
    }

    /// Reports whether this channel has multiple keys and lacks a stable key.
    #[must_use]
    pub const fn is_multi_key(&self) -> bool {
        self.multi_key
    }
}

/// Store boundary for the persisted channel configuration used by this slice.
///
/// A PostgreSQL implementation must return credentials only after this router
/// has authorized the request.  It must not substitute caller JSON for a
/// persisted channel when an operation names a channel id.
#[async_trait]
pub trait ChannelAdvancedStore: Send + Sync {
    /// Loads the secret-bearing configuration for an existing channel.
    async fn load_channel(
        &self,
        channel_id: i64,
    ) -> Result<ChannelAdvancedChannel, ChannelAdvancedError>;
}

/// PostgreSQL implementation of the persisted advanced-channel boundary.
///
/// It selects the credential-bearing row only for the post-authorization
/// provider layer.  Channel type codes are frozen from the archived legacy
/// constants (`Ollama=4`, `Codex=57`), which keeps protocol checks independent
/// from caller-supplied request fields.
#[derive(Clone)]
pub struct PgChannelAdvancedStore {
    pg: PgPool,
}

impl PgChannelAdvancedStore {
    /// Creates a store over the authoritative channel PostgreSQL pool.
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self { pg }
    }
}

#[async_trait]
impl ChannelAdvancedStore for PgChannelAdvancedStore {
    async fn load_channel(
        &self,
        channel_id: i64,
    ) -> Result<ChannelAdvancedChannel, ChannelAdvancedError> {
        let row = sqlx::query(
            "SELECT id, type, name, COALESCE(base_url, '') AS base_url, key, \
             COALESCE((channel_info ->> 'is_multi_key')::boolean, false) AS multi_key \
             FROM channels WHERE id = $1",
        )
        .bind(channel_id)
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| ChannelAdvancedError::Provider)?
        .ok_or(ChannelAdvancedError::NotFound)?;
        let id = row
            .try_get("id")
            .map_err(|_| ChannelAdvancedError::Provider)?;
        let channel_type: i64 = row
            .try_get("type")
            .map_err(|_| ChannelAdvancedError::Provider)?;
        let kind = match channel_type {
            CHANNEL_TYPE_CODEX => ChannelAdvancedKind::Codex,
            CHANNEL_TYPE_OLLAMA => ChannelAdvancedKind::Ollama,
            other => ChannelAdvancedKind::Other(other),
        };
        let name = row
            .try_get("name")
            .map_err(|_| ChannelAdvancedError::Provider)?;
        let base_url = row
            .try_get("base_url")
            .map_err(|_| ChannelAdvancedError::Provider)?;
        let credential = row
            .try_get::<String, _>("key")
            .map(SecretString::from)
            .map_err(|_| ChannelAdvancedError::Provider)?;
        let multi_key = row
            .try_get("multi_key")
            .map_err(|_| ChannelAdvancedError::Provider)?;
        Ok(ChannelAdvancedChannel::new(
            id, kind, name, base_url, credential, multi_key,
        ))
    }
}

/// Upstream boundary for protocol-specific advanced channel work.
///
/// Implementations own outbound HTTP, timeouts, Codex token refresh, and any
/// durable mutation required by the operation.  The route layer supplies a
/// stored channel where legacy behavior requires one; it never forwards a
/// credential from the request body.
#[async_trait]
pub trait ChannelAdvancedUpstream: Send + Sync {
    /// Executes the operation using an optional, persisted channel config.
    async fn execute(
        &self,
        call: ChannelAdvancedCall,
        channel: Option<ChannelAdvancedChannel>,
    ) -> Result<Value, ChannelAdvancedError>;

    /// Returns a raw legacy response for protocol-specific cases such as SSE.
    async fn execute_reply(
        &self,
        call: ChannelAdvancedCall,
        channel: Option<ChannelAdvancedChannel>,
    ) -> Result<ChannelAdvancedReply, ChannelAdvancedError> {
        self.execute(call, channel)
            .await
            .map(ChannelAdvancedReply::success)
    }
}

/// Real, bounded HTTP implementation for channel-management upstream calls.
///
/// A target is always derived from a loaded [`ChannelAdvancedChannel`].  In
/// particular, this adapter deliberately never reads a URL, credential, or
/// proxy setting from `ChannelAdvancedCall::input`; that keeps preview bodies
/// from becoming a server-side request forgery primitive.  Supply a client
/// with the deployment's DNS/proxy policy and a finite timeout.
#[derive(Clone)]
pub struct ReqwestChannelAdvancedUpstream {
    client: reqwest::Client,
    timeout: Duration,
    max_response_bytes: usize,
    // Candidate listeners never accept an Internet OAuth target.  Isolated
    // parity tests inject a literal-loopback fixture through the builder.
    codex_oauth_token_url: Option<reqwest::Url>,
    /// Present only in the production composition.  Keeping this optional
    /// permits deterministic HTTP-only contract tests without a database.
    pg: Option<PgPool>,
}

impl ReqwestChannelAdvancedUpstream {
    /// Builds a production adapter with the legacy 15-second management
    /// deadline, bounded transport timeouts, and redirect following disabled.
    pub fn new() -> Result<Self, ChannelAdvancedError> {
        let client = reqwest::Client::builder()
            .use_rustls_tls()
            .connect_timeout(Duration::from_secs(3))
            .timeout(DEFAULT_ADVANCED_UPSTREAM_TIMEOUT)
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .map_err(|_| ChannelAdvancedError::Provider)?;
        Ok(Self {
            client,
            timeout: DEFAULT_ADVANCED_UPSTREAM_TIMEOUT,
            max_response_bytes: DEFAULT_ADVANCED_RESPONSE_LIMIT,
            codex_oauth_token_url: None,
            pg: None,
        })
    }

    /// Injects an isolated client for a controlled local integration test.
    /// Production callers must use [`Self::new`] so redirects cannot switch a
    /// stored target to a different origin after validation.
    #[cfg(test)]
    fn with_test_client(client: reqwest::Client) -> Result<Self, ChannelAdvancedError> {
        Ok(Self {
            client,
            timeout: DEFAULT_ADVANCED_UPSTREAM_TIMEOUT,
            max_response_bytes: DEFAULT_ADVANCED_RESPONSE_LIMIT,
            codex_oauth_token_url: None,
            pg: None,
        })
    }

    /// Attaches the authoritative PostgreSQL pool for credential refresh and
    /// test timing persistence.  No request-derived endpoint is stored.
    #[must_use]
    pub fn with_pg_pool(mut self, pg: PgPool) -> Self {
        self.pg = Some(pg);
        self
    }

    /// Narrows the response limit; zero is rejected instead of disabling it.
    #[must_use]
    pub const fn with_max_response_bytes(mut self, bytes: usize) -> Self {
        self.max_response_bytes = bytes;
        self
    }

    /// Overrides the ordinary management deadline for an explicitly bounded
    /// deployment or an isolated local HTTP test.
    #[must_use]
    pub const fn with_timeout(mut self, timeout: Duration) -> Self {
        self.timeout = timeout;
        self
    }

    /// Injects an isolated, literal-loopback OAuth fixture for parity tests.
    ///
    /// Candidate listeners deliberately have no Internet OAuth egress.  A
    /// deployment that needs a real OAuth refresh boundary must provide a
    /// separately reviewed adapter instead of changing this route slice.
    pub fn with_codex_oauth_token_url(
        mut self,
        url: reqwest::Url,
    ) -> Result<Self, ChannelAdvancedError> {
        validate_loopback_target(&url)?;
        self.codex_oauth_token_url = Some(url);
        Ok(self)
    }

    fn target(
        channel: &ChannelAdvancedChannel,
        path: &str,
    ) -> Result<reqwest::Url, ChannelAdvancedError> {
        let mut base =
            reqwest::Url::parse(channel.base_url()).map_err(|_| ChannelAdvancedError::Provider)?;
        validate_loopback_target(&base)?;
        if !base.path().ends_with('/') {
            let path = format!("{}/", base.path());
            base.set_path(&path);
        }
        base.join(path.trim_start_matches('/'))
            .map_err(|_| ChannelAdvancedError::Provider)
    }

    fn authorization(
        request: reqwest::RequestBuilder,
        channel: &ChannelAdvancedChannel,
    ) -> reqwest::RequestBuilder {
        let key = first_channel_key(channel.credential().expose_secret());
        if key.is_empty() {
            request
        } else {
            request.bearer_auth(key)
        }
    }

    async fn send_bounded(
        &self,
        request: reqwest::RequestBuilder,
        timeout: Duration,
    ) -> Result<(reqwest::StatusCode, Bytes), ChannelAdvancedError> {
        if self.max_response_bytes == 0 || timeout.is_zero() {
            return Err(ChannelAdvancedError::Provider);
        }
        let mut response = tokio::time::timeout(timeout, request.send())
            .await
            .map_err(|_| ChannelAdvancedError::Provider)?
            .map_err(|_| ChannelAdvancedError::Provider)?;
        let status = response.status();
        if response
            .content_length()
            .is_some_and(|length| length > self.max_response_bytes as u64)
        {
            return Err(ChannelAdvancedError::Provider);
        }
        let read = async {
            let mut output = Vec::new();
            while let Some(chunk) = response
                .chunk()
                .await
                .map_err(|_| ChannelAdvancedError::Provider)?
            {
                if output.len().saturating_add(chunk.len()) > self.max_response_bytes {
                    return Err(ChannelAdvancedError::Provider);
                }
                output.extend_from_slice(&chunk);
            }
            Ok::<_, ChannelAdvancedError>(Bytes::from(output))
        };
        tokio::time::timeout(timeout, read)
            .await
            .map_err(|_| ChannelAdvancedError::Provider)?
            .map(|bytes| (status, bytes))
    }

    async fn ollama_mutation(
        &self,
        channel: &ChannelAdvancedChannel,
        path: &str,
        method: reqwest::Method,
        model_name: &str,
        stream: bool,
    ) -> Result<(), ChannelAdvancedError> {
        if model_name.trim().is_empty() {
            return Err(ChannelAdvancedError::Invalid);
        }
        let url = Self::target(channel, path)?;
        let body = if path == "api/pull" {
            json!({"name": model_name, "stream": stream})
        } else {
            json!({"name": model_name})
        };
        let request = Self::authorization(self.client.request(method, url).json(&body), channel);
        let (status, _) = self.send_bounded(request, OLLAMA_PULL_TIMEOUT).await?;
        (status == reqwest::StatusCode::OK)
            .then_some(())
            .ok_or(ChannelAdvancedError::Provider)
    }

    async fn codex_wham(
        &self,
        channel: &ChannelAdvancedChannel,
        path: &str,
        method: reqwest::Method,
    ) -> Result<Value, ChannelAdvancedError> {
        let mut oauth = parse_codex_credential(channel.credential().expose_secret())?;
        let (mut status, mut body) = self
            .codex_wham_request(&oauth, channel, path, method.clone())
            .await?;
        // The frozen Go handler refreshes once after an authentication failure,
        // persists the replacement credential, then retries the same WHAM
        // request.  A failed refresh intentionally leaves the original WHAM
        // response intact rather than treating it as a successful operation.
        if matches!(
            status,
            reqwest::StatusCode::UNAUTHORIZED | reqwest::StatusCode::FORBIDDEN
        ) && !oauth.refresh_token.trim().is_empty()
            && let Ok(refreshed) = self.refresh_codex_credential(channel).await
        {
            oauth = refreshed;
            if let Ok(retried) = self.codex_wham_request(&oauth, channel, path, method).await {
                (status, body) = retried;
            }
        }
        let payload = serde_json::from_slice(&body)
            .unwrap_or_else(|_| Value::String(String::from_utf8_lossy(&body).into_owned()));
        Ok(json!({
            "success": status.is_success(),
            "message": if status.is_success() {
                String::new()
            } else {
                format!("upstream status: {}", status.as_u16())
            },
            "upstream_status": status.as_u16(),
            "data": payload,
        }))
    }

    async fn codex_wham_request(
        &self,
        oauth: &CodexCredential,
        channel: &ChannelAdvancedChannel,
        path: &str,
        method: reqwest::Method,
    ) -> Result<(reqwest::StatusCode, Bytes), ChannelAdvancedError> {
        let url = Self::target(channel, path)?;
        let mut request = self
            .client
            .request(method.clone(), url)
            .bearer_auth(&oauth.access_token)
            .header("chatgpt-account-id", &oauth.account_id)
            .header("originator", "codex_cli_rs")
            .header(reqwest::header::ACCEPT, "application/json");
        if method == reqwest::Method::POST {
            request = request.json(&json!({"redeem_request_id": uuid::Uuid::new_v4().to_string()}));
        }
        self.send_bounded(request, self.timeout).await
    }

    async fn persist_channel_key(
        &self,
        channel_id: i64,
        key: &str,
    ) -> Result<(), ChannelAdvancedError> {
        let Some(pg) = &self.pg else {
            return Err(ChannelAdvancedError::Provider);
        };
        let result = sqlx::query("UPDATE channels SET key = $1 WHERE id = $2 AND type = $3")
            .bind(key)
            .bind(channel_id)
            .bind(CHANNEL_TYPE_CODEX)
            .execute(pg)
            .await
            .map_err(|_| ChannelAdvancedError::Provider)?;
        if result.rows_affected() != 1 {
            return Err(ChannelAdvancedError::NotFound);
        }
        Ok(())
    }

    async fn refresh_codex_credential(
        &self,
        channel: &ChannelAdvancedChannel,
    ) -> Result<CodexCredential, ChannelAdvancedError> {
        let mut oauth = parse_codex_refresh_credential(channel.credential().expose_secret())?;
        let oauth_url = self
            .codex_oauth_token_url
            .clone()
            .ok_or(ChannelAdvancedError::Provider)?;
        let request = self.client.post(oauth_url).form(&[
            ("grant_type", "refresh_token"),
            ("refresh_token", oauth.refresh_token.as_str()),
            ("client_id", CODEX_OAUTH_CLIENT_ID),
        ]);
        let (status, body) = self.send_bounded(request, Duration::from_secs(10)).await?;
        if !status.is_success() {
            return Err(ChannelAdvancedError::Provider);
        }
        let refreshed: CodexRefreshResponse =
            serde_json::from_slice(&body).map_err(|_| ChannelAdvancedError::Provider)?;
        if refreshed.access_token.trim().is_empty()
            || refreshed.refresh_token.trim().is_empty()
            || refreshed.expires_in == 0
        {
            return Err(ChannelAdvancedError::Provider);
        }
        oauth.access_token = refreshed.access_token;
        oauth.refresh_token = refreshed.refresh_token;
        if oauth.kind.trim().is_empty() {
            oauth.kind = "codex".to_owned();
        }
        populate_missing_codex_identity(&mut oauth);
        let now = unix_seconds()?;
        oauth.last_refresh = Some(rfc3339_seconds(now)?);
        oauth.expired = Some(rfc3339_seconds(
            now.checked_add(
                i64::try_from(refreshed.expires_in).map_err(|_| ChannelAdvancedError::Provider)?,
            )
            .ok_or(ChannelAdvancedError::Provider)?,
        )?);
        let encoded = serde_json::to_string(&oauth).map_err(|_| ChannelAdvancedError::Provider)?;
        self.persist_channel_key(channel.id(), &encoded).await?;
        Ok(oauth)
    }

    async fn refresh_codex(
        &self,
        channel: &ChannelAdvancedChannel,
    ) -> Result<Value, ChannelAdvancedError> {
        let oauth = self.refresh_codex_credential(channel).await?;
        Ok(json!({
            "expires_at": oauth.expired,
            "last_refresh": oauth.last_refresh,
            "account_id": oauth.account_id,
            "email": oauth.email,
            "channel_id": channel.id(),
            "channel_type": CHANNEL_TYPE_CODEX,
            "channel_name": channel.name(),
        }))
    }
}

#[derive(serde::Deserialize, serde::Serialize)]
struct CodexCredential {
    #[serde(default)]
    access_token: String,
    #[serde(default)]
    refresh_token: String,
    #[serde(default)]
    account_id: String,
    #[serde(default)]
    email: String,
    #[serde(default)]
    last_refresh: Option<String>,
    #[serde(default)]
    expired: Option<String>,
    #[serde(default)]
    #[serde(rename = "type")]
    kind: String,
}

#[derive(serde::Deserialize)]
struct CodexRefreshResponse {
    access_token: String,
    refresh_token: String,
    expires_in: u64,
}

fn parse_codex_credential(value: &str) -> Result<CodexCredential, ChannelAdvancedError> {
    let credential: CodexCredential =
        serde_json::from_str(value).map_err(|_| ChannelAdvancedError::Provider)?;
    if credential.access_token.trim().is_empty() || credential.account_id.trim().is_empty() {
        return Err(ChannelAdvancedError::Provider);
    }
    Ok(credential)
}

fn parse_codex_refresh_credential(value: &str) -> Result<CodexCredential, ChannelAdvancedError> {
    let credential: CodexCredential =
        serde_json::from_str(value).map_err(|_| ChannelAdvancedError::Provider)?;
    (!credential.refresh_token.trim().is_empty())
        .then_some(credential)
        .ok_or(ChannelAdvancedError::Provider)
}

fn populate_missing_codex_identity(credential: &mut CodexCredential) {
    let Some(claims) = decode_unverified_jwt_claims(&credential.access_token) else {
        return;
    };
    if credential.account_id.trim().is_empty() {
        credential.account_id = claims
            .get("https://api.openai.com/auth")
            .and_then(Value::as_object)
            .and_then(|auth| auth.get("chatgpt_account_id"))
            .and_then(Value::as_str)
            .map(str::trim)
            .filter(|account_id| !account_id.is_empty())
            .unwrap_or_default()
            .to_owned();
    }
    if credential.email.trim().is_empty() {
        credential.email = claims
            .get("email")
            .and_then(Value::as_str)
            .map(str::trim)
            .filter(|email| !email.is_empty())
            .unwrap_or_default()
            .to_owned();
    }
}

fn decode_unverified_jwt_claims(token: &str) -> Option<serde_json::Map<String, Value>> {
    use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};

    let mut parts = token.split('.');
    let _header = parts.next()?;
    let claims = parts.next()?;
    let _signature = parts.next()?;
    if parts.next().is_some() {
        return None;
    }
    let decoded = URL_SAFE_NO_PAD.decode(claims).ok()?;
    serde_json::from_slice::<Value>(&decoded)
        .ok()?
        .as_object()
        .cloned()
}

fn first_channel_key(value: &str) -> &str {
    value.lines().next().unwrap_or_default().trim()
}

fn validate_loopback_target(url: &reqwest::Url) -> Result<(), ChannelAdvancedError> {
    if !matches!(url.scheme(), "http" | "https")
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err(ChannelAdvancedError::Provider);
    }
    // Never resolve a hostname here: a DNS answer can change between a check
    // and connect.  Candidate provider routes accept only literal loopback
    // addresses, which makes SSRF and DNS rebinding fail closed.
    let Some(host) = url.host_str() else {
        return Err(ChannelAdvancedError::Provider);
    };
    host.parse::<IpAddr>()
        .ok()
        .filter(|address| address.is_loopback())
        .map(|_| ())
        .ok_or(ChannelAdvancedError::Provider)
}

fn unix_seconds() -> Result<i64, ChannelAdvancedError> {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| ChannelAdvancedError::Provider)
        .and_then(|duration| {
            i64::try_from(duration.as_secs()).map_err(|_| ChannelAdvancedError::Provider)
        })
}

fn rfc3339_seconds(seconds: i64) -> Result<String, ChannelAdvancedError> {
    chrono::DateTime::<Utc>::from_timestamp(seconds, 0)
        .map(|value| value.to_rfc3339_opts(SecondsFormat::Secs, true))
        .ok_or(ChannelAdvancedError::Provider)
}

#[async_trait]
impl ChannelAdvancedUpstream for ReqwestChannelAdvancedUpstream {
    async fn execute(
        &self,
        call: ChannelAdvancedCall,
        channel: Option<ChannelAdvancedChannel>,
    ) -> Result<Value, ChannelAdvancedError> {
        let channel = channel.ok_or(ChannelAdvancedError::Invalid)?;
        match call.operation {
            ChannelAdvancedOperation::FetchModels
            | ChannelAdvancedOperation::FetchUpstreamModels => {
                let path = if channel.kind() == ChannelAdvancedKind::Ollama {
                    "api/tags"
                } else {
                    "v1/models"
                };
                let url = Self::target(&channel, path)?;
                let request = Self::authorization(self.client.get(url), &channel);
                let (status, body) = self.send_bounded(request, self.timeout).await?;
                if !status.is_success() {
                    return Err(ChannelAdvancedError::Provider);
                }
                let payload: Value =
                    serde_json::from_slice(&body).map_err(|_| ChannelAdvancedError::Provider)?;
                let models = if channel.kind() == ChannelAdvancedKind::Ollama {
                    payload
                        .get("models")
                        .and_then(Value::as_array)
                        .map(|models| {
                            models
                                .iter()
                                .filter_map(|m| m.get("name").and_then(Value::as_str))
                                .map(str::to_owned)
                                .collect::<Vec<_>>()
                        })
                        .unwrap_or_default()
                } else {
                    payload
                        .get("data")
                        .and_then(Value::as_array)
                        .map(|models| {
                            models
                                .iter()
                                .filter_map(|m| m.get("id").and_then(Value::as_str))
                                .map(str::to_owned)
                                .collect::<Vec<_>>()
                        })
                        .unwrap_or_default()
                };
                Ok(json!(models))
            }
            ChannelAdvancedOperation::OllamaVersion => {
                let url = Self::target(&channel, "api/version")?;
                let request = Self::authorization(self.client.get(url), &channel);
                let (status, body) = self.send_bounded(request, self.timeout).await?;
                if !status.is_success() {
                    return Err(ChannelAdvancedError::Provider);
                }
                let version = serde_json::from_slice::<Value>(&body)
                    .ok()
                    .and_then(|body| {
                        body.get("version")
                            .and_then(Value::as_str)
                            .map(str::to_owned)
                    })
                    .filter(|version| !version.trim().is_empty())
                    .ok_or(ChannelAdvancedError::Provider)?;
                Ok(json!({"version": version}))
            }
            ChannelAdvancedOperation::CodexUsage => {
                self.codex_wham(&channel, "backend-api/wham/usage", reqwest::Method::GET)
                    .await
            }
            ChannelAdvancedOperation::CodexUsageResetCredits => {
                self.codex_wham(
                    &channel,
                    "backend-api/wham/rate-limit-reset-credits",
                    reqwest::Method::GET,
                )
                .await
            }
            ChannelAdvancedOperation::CodexUsageReset => {
                self.codex_wham(
                    &channel,
                    "backend-api/wham/rate-limit-reset-credits/consume",
                    reqwest::Method::POST,
                )
                .await
            }
            ChannelAdvancedOperation::CodexRefresh => self.refresh_codex(&channel).await,
            ChannelAdvancedOperation::OllamaPull => {
                let model = call
                    .input
                    .get("model_name")
                    .and_then(Value::as_str)
                    .ok_or(ChannelAdvancedError::Invalid)?;
                self.ollama_mutation(&channel, "api/pull", reqwest::Method::POST, model, false)
                    .await?;
                Ok(json!({"message": format!("Model {model} pulled successfully")}))
            }
            ChannelAdvancedOperation::OllamaDelete => {
                let model = call
                    .input
                    .get("model_name")
                    .and_then(Value::as_str)
                    .ok_or(ChannelAdvancedError::Invalid)?;
                self.ollama_mutation(
                    &channel,
                    "api/delete",
                    reqwest::Method::DELETE,
                    model,
                    false,
                )
                .await?;
                Ok(json!({"message": format!("Model {model} deleted successfully")}))
            }
            ChannelAdvancedOperation::TestOne => {
                let started = tokio::time::Instant::now();
                let path = if channel.kind() == ChannelAdvancedKind::Ollama {
                    "api/tags"
                } else {
                    "v1/models"
                };
                let url = Self::target(&channel, path)?;
                let request = Self::authorization(self.client.get(url), &channel);
                let (status, _) = self.send_bounded(request, self.timeout).await?;
                Ok(json!({"ok": status.is_success(), "time": started.elapsed().as_secs_f64()}))
            }
            _ => Err(ChannelAdvancedError::Provider),
        }
    }

    async fn execute_reply(
        &self,
        call: ChannelAdvancedCall,
        channel: Option<ChannelAdvancedChannel>,
    ) -> Result<ChannelAdvancedReply, ChannelAdvancedError> {
        if call.operation == ChannelAdvancedOperation::OllamaPullStream {
            let channel = channel.ok_or(ChannelAdvancedError::Invalid)?;
            let model = call
                .input
                .get("model_name")
                .and_then(Value::as_str)
                .filter(|model| !model.trim().is_empty())
                .ok_or(ChannelAdvancedError::Invalid)?;
            let url = Self::target(&channel, "api/pull")?;
            let request = Self::authorization(
                self.client
                    .post(url)
                    .json(&json!({"name": model, "stream": true})),
                &channel,
            );
            let response = tokio::time::timeout(OLLAMA_PULL_TIMEOUT, request.send())
                .await
                .map_err(|_| ChannelAdvancedError::Provider)?
                .map_err(|_| ChannelAdvancedError::Provider)?;
            if !response.status().is_success() {
                return Err(ChannelAdvancedError::Provider);
            }
            let status = response.status();
            let headers = response.headers().clone();
            let mut output = Response::new(Body::from_stream(response.bytes_stream()));
            *output.status_mut() = status;
            output.headers_mut().insert(
                axum::http::header::CONTENT_TYPE,
                axum::http::HeaderValue::from_static("text/event-stream"),
            );
            output.headers_mut().insert(
                axum::http::header::CACHE_CONTROL,
                axum::http::HeaderValue::from_static("no-cache"),
            );
            output.headers_mut().insert(
                axum::http::header::CONNECTION,
                axum::http::HeaderValue::from_static("keep-alive"),
            );
            output.headers_mut().insert(
                axum::http::header::ACCESS_CONTROL_ALLOW_ORIGIN,
                axum::http::HeaderValue::from_static("*"),
            );
            output.headers_mut().insert(
                axum::http::HeaderName::from_static("x-accel-buffering"),
                axum::http::HeaderValue::from_static("no"),
            );
            for (name, value) in &headers {
                if !is_hop_by_hop_header(name) && name != axum::http::header::CONTENT_TYPE {
                    output.headers_mut().append(name, value.clone());
                }
            }
            return Ok(ChannelAdvancedReply::from_response(output));
        }
        let operation = call.operation;
        let payload = self.execute(call, channel).await?;
        match operation {
            ChannelAdvancedOperation::CodexUsage
            | ChannelAdvancedOperation::CodexUsageReset
            | ChannelAdvancedOperation::CodexUsageResetCredits => {
                Ok(ChannelAdvancedReply::new(StatusCode::OK, payload))
            }
            ChannelAdvancedOperation::CodexRefresh => Ok(ChannelAdvancedReply::new(
                StatusCode::OK,
                json!({"success": true, "message": "refreshed", "data": payload}),
            )),
            ChannelAdvancedOperation::OllamaPull | ChannelAdvancedOperation::OllamaDelete => {
                let message = payload
                    .get("message")
                    .and_then(Value::as_str)
                    .unwrap_or_default();
                Ok(ChannelAdvancedReply::new(
                    StatusCode::OK,
                    json!({
                        "success": true,
                        "message": message,
                    }),
                ))
            }
            ChannelAdvancedOperation::TestOne => Ok(ChannelAdvancedReply::new(
                StatusCode::OK,
                json!({
                    "success": payload.get("ok").and_then(Value::as_bool).unwrap_or(false),
                    "message": "",
                    "time": payload.get("time").cloned().unwrap_or(Value::from(0.0)),
                }),
            )),
            _ => Ok(ChannelAdvancedReply::success(payload)),
        }
    }
}

fn is_hop_by_hop_header(name: &axum::http::HeaderName) -> bool {
    matches!(
        name.as_str(),
        "connection"
            | "keep-alive"
            | "proxy-authenticate"
            | "proxy-authorization"
            | "te"
            | "trailer"
            | "transfer-encoding"
            | "upgrade"
    )
}

#[cfg(test)]
mod upstream_tests {
    use std::{
        io,
        sync::{Arc, Mutex, MutexGuard},
    };

    use axum::{
        Router,
        body::to_bytes,
        extract::{Request, State},
        http::{HeaderMap, StatusCode},
        response::{IntoResponse, Response},
        routing::any,
    };
    use secrecy::SecretString;
    use serde_json::json;

    use super::{
        ChannelAdvancedCall, ChannelAdvancedChannel, ChannelAdvancedKind, ChannelAdvancedOperation,
        ChannelAdvancedUpstream, ReqwestChannelAdvancedUpstream,
    };

    type TestResult<T = ()> = Result<T, Box<dyn std::error::Error + Send + Sync>>;
    type MockServer = (
        String,
        Arc<Mutex<Vec<CapturedRequest>>>,
        tokio::task::JoinHandle<io::Result<()>>,
    );

    fn test_error(message: impl Into<String>) -> io::Error {
        io::Error::other(message.into())
    }

    fn lock_unpoisoned<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
        match mutex.lock() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        }
    }

    fn optional_header(
        headers: &HeaderMap,
        name: &'static str,
    ) -> Result<Option<String>, Response> {
        headers
            .get(name)
            .map(|value| {
                value.to_str().map(ToOwned::to_owned).map_err(|error| {
                    (
                        StatusCode::BAD_REQUEST,
                        format!("invalid {name} header: {error}"),
                    )
                        .into_response()
                })
            })
            .transpose()
    }

    #[derive(Clone, Debug, Eq, PartialEq)]
    struct CapturedRequest {
        method: String,
        path: String,
        authorization: Option<String>,
        account_id: Option<String>,
        body: Vec<u8>,
    }

    async fn capture(
        State(captured): State<Arc<Mutex<Vec<CapturedRequest>>>>,
        request: Request,
    ) -> Response {
        let (parts, body) = request.into_parts();
        let body = match to_bytes(body, 64 * 1024).await {
            Ok(body) => body.to_vec(),
            Err(error) => {
                return (
                    StatusCode::INTERNAL_SERVER_ERROR,
                    format!("mock request body error: {error}"),
                )
                    .into_response();
            }
        };
        let authorization = match optional_header(&parts.headers, "authorization") {
            Ok(value) => value,
            Err(response) => return response,
        };
        let account_id = match optional_header(&parts.headers, "chatgpt-account-id") {
            Ok(value) => value,
            Err(response) => return response,
        };
        let path = parts.uri.path().to_owned();
        lock_unpoisoned(&captured).push(CapturedRequest {
            method: parts.method.to_string(),
            path: path.clone(),
            authorization,
            account_id,
            body,
        });
        match path.as_str() {
            "/api/version" => {
                (StatusCode::OK, axum::Json(json!({"version":"0.9.1"}))).into_response()
            }
            "/api/tags" => (
                StatusCode::OK,
                axum::Json(json!({"models":[{"name":"phi4"}]})),
            )
                .into_response(),
            "/backend-api/wham/usage" => {
                (StatusCode::OK, axum::Json(json!({"used":3}))).into_response()
            }
            "/api/pull" => {
                (StatusCode::OK, axum::Json(json!({"status":"success"}))).into_response()
            }
            _ => StatusCode::NOT_FOUND.into_response(),
        }
    }

    async fn local_mock() -> TestResult<MockServer> {
        let captured = Arc::new(Mutex::new(Vec::new()));
        let app = Router::new()
            .fallback(any(capture))
            .with_state(captured.clone());
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .map_err(|error| test_error(format!("mock listener bind error: {error}")))?;
        let address = listener
            .local_addr()
            .map_err(|error| test_error(format!("mock listener address error: {error}")))?;
        let base = format!("http://{address}");
        reqwest::Url::parse(&base)
            .map_err(|error| test_error(format!("mock server URI error: {error}")))?;
        let task = tokio::spawn(async move { axum::serve(listener, app).await });
        Ok((base, captured, task))
    }

    fn channel(
        id: i64,
        kind: ChannelAdvancedKind,
        base_url: String,
        credential: &str,
    ) -> ChannelAdvancedChannel {
        ChannelAdvancedChannel::new(
            id,
            kind,
            "test".to_owned(),
            base_url,
            SecretString::from(credential.to_owned()),
            false,
        )
    }

    #[tokio::test]
    async fn ollama_operations_use_the_stored_target_and_never_a_request_url() -> TestResult {
        let (base, captured, task) = local_mock().await?;
        let upstream = ReqwestChannelAdvancedUpstream::with_test_client(reqwest::Client::new())
            .map_err(|error| test_error(format!("test upstream URI error: {error:?}")))?;
        let result = upstream
            .execute(
                ChannelAdvancedCall {
                    operation: ChannelAdvancedOperation::OllamaVersion,
                    channel_id: Some(7),
                    input: json!({"base_url":"http://attacker.invalid"}),
                },
                Some(channel(7, ChannelAdvancedKind::Ollama, base, "one\ntwo")),
            )
            .await
            .map_err(|error| test_error(format!("Ollama version request failed: {error:?}")))?;
        assert_eq!(result, json!({"version":"0.9.1"}));
        let recorded = lock_unpoisoned(&captured).clone();
        assert_eq!(recorded.len(), 1);
        let request = recorded
            .first()
            .ok_or_else(|| test_error("Ollama version request was not captured"))?;
        assert_eq!(request.path, "/api/version");
        let authorization = request
            .authorization
            .as_deref()
            .ok_or_else(|| test_error("Ollama version authorization header was not captured"))?;
        assert_eq!(authorization, "Bearer one");
        task.abort();
        Ok(())
    }

    #[tokio::test]
    async fn codex_usage_preserves_the_wham_method_headers_and_payload() -> TestResult {
        let (base, captured, task) = local_mock().await?;
        let upstream = ReqwestChannelAdvancedUpstream::with_test_client(reqwest::Client::new())
            .map_err(|error| test_error(format!("test upstream URI error: {error:?}")))?;
        let credential =
            r#"{"access_token":"access","refresh_token":"refresh","account_id":"account"}"#;
        let result = upstream
            .execute(
                ChannelAdvancedCall {
                    operation: ChannelAdvancedOperation::CodexUsage,
                    channel_id: Some(9),
                    input: json!({}),
                },
                Some(channel(9, ChannelAdvancedKind::Codex, base, credential)),
            )
            .await
            .map_err(|error| test_error(format!("Codex usage request failed: {error:?}")))?;
        assert_eq!(result["success"], true);
        assert_eq!(result["upstream_status"], 200);
        assert_eq!(result["data"], json!({"used":3}));
        let recorded = lock_unpoisoned(&captured).clone();
        let request = recorded
            .first()
            .ok_or_else(|| test_error("Codex usage request was not captured"))?;
        assert_eq!(request.method, "GET");
        assert_eq!(request.path, "/backend-api/wham/usage");
        let authorization = request
            .authorization
            .as_deref()
            .ok_or_else(|| test_error("Codex usage authorization header was not captured"))?;
        assert_eq!(authorization, "Bearer access");
        let account_id = request
            .account_id
            .as_deref()
            .ok_or_else(|| test_error("Codex account header was not captured"))?;
        assert_eq!(account_id, "account");
        task.abort();
        Ok(())
    }

    #[tokio::test]
    async fn ollama_pull_uses_legacy_json_shape_and_does_not_forward_hop_headers() -> TestResult {
        let (base, captured, task) = local_mock().await?;
        let upstream = ReqwestChannelAdvancedUpstream::with_test_client(reqwest::Client::new())
            .map_err(|error| test_error(format!("test upstream URI error: {error:?}")))?;
        upstream
            .execute(
                ChannelAdvancedCall {
                    operation: ChannelAdvancedOperation::OllamaPull,
                    channel_id: Some(7),
                    input: json!({"model_name":"phi4"}),
                },
                Some(channel(7, ChannelAdvancedKind::Ollama, base, "secret")),
            )
            .await
            .map_err(|error| test_error(format!("Ollama pull request failed: {error:?}")))?;
        let recorded = lock_unpoisoned(&captured).clone();
        let request = recorded
            .first()
            .ok_or_else(|| test_error("Ollama pull request was not captured"))?;
        assert_eq!(request.method, "POST");
        assert_eq!(request.path, "/api/pull");
        let body = serde_json::from_slice::<serde_json::Value>(&request.body)
            .map_err(|error| test_error(format!("Ollama pull JSON error: {error}")))?;
        assert_eq!(body, json!({"name":"phi4","stream":false}));
        task.abort();
        Ok(())
    }
}

/// Production composition of the persistent channel store and upstream adapter.
///
/// This implementation removes the former opaque route-to-provider shortcut:
/// legacy channel ids are loaded from the store, Codex/Ollama type invariants
/// are checked before outbound work, and root key reads terminate at the store.
#[derive(Clone)]
pub struct StoreBackedChannelAdvancedProvider {
    store: Arc<dyn ChannelAdvancedStore>,
    upstream: Arc<dyn ChannelAdvancedUpstream>,
}

impl StoreBackedChannelAdvancedProvider {
    /// Creates a provider that keeps persistence and outbound work separate.
    #[must_use]
    pub fn new(
        store: Arc<dyn ChannelAdvancedStore>,
        upstream: Arc<dyn ChannelAdvancedUpstream>,
    ) -> Self {
        Self { store, upstream }
    }

    async fn stored_channel(
        &self,
        call: &ChannelAdvancedCall,
    ) -> Result<Option<ChannelAdvancedChannel>, ChannelAdvancedError> {
        let channel_id = required_stored_channel_id(call)?;
        let channel = match channel_id {
            Some(channel_id) => Some(self.store.load_channel(channel_id).await?),
            None => None,
        };
        validate_channel_kind(call.operation, channel.as_ref())?;
        Ok(channel)
    }
}

#[async_trait]
impl ChannelAdvancedProvider for StoreBackedChannelAdvancedProvider {
    async fn execute(&self, call: ChannelAdvancedCall) -> Result<Value, ChannelAdvancedError> {
        let channel = self.stored_channel(&call).await?;
        if call.operation == ChannelAdvancedOperation::ChannelKey {
            // Go applies SecureVerificationRequired after RootAuth.  This
            // slice has no equivalent second-factor assertion in its state,
            // so returning a credential here would weaken the legacy route.
            return Err(ChannelAdvancedError::Forbidden);
        }
        self.upstream.execute(call, channel).await
    }

    async fn execute_reply(
        &self,
        call: ChannelAdvancedCall,
    ) -> Result<ChannelAdvancedReply, ChannelAdvancedError> {
        let channel = self.stored_channel(&call).await?;
        if call.operation == ChannelAdvancedOperation::ChannelKey {
            return Err(ChannelAdvancedError::Forbidden);
        }
        self.upstream.execute_reply(call, channel).await
    }
}

fn required_stored_channel_id(
    call: &ChannelAdvancedCall,
) -> Result<Option<i64>, ChannelAdvancedError> {
    if call.channel_id.is_some() {
        return Ok(call.channel_id);
    }
    match call.operation {
        ChannelAdvancedOperation::OllamaDelete
        | ChannelAdvancedOperation::OllamaPull
        | ChannelAdvancedOperation::OllamaPullStream => {
            required_input_channel_id(&call.input, "channel_id").map(Some)
        }
        ChannelAdvancedOperation::ApplyUpstreamUpdates
        | ChannelAdvancedOperation::DetectUpstreamUpdates => {
            required_input_channel_id(&call.input, "id").map(Some)
        }
        // Legacy previews use `channel_id > 0` to select an existing channel;
        // zero or an omitted id means a caller-supplied preview configuration.
        ChannelAdvancedOperation::FetchModels => {
            optional_input_channel_id(&call.input, "channel_id")
        }
        ChannelAdvancedOperation::CodexRefresh
        | ChannelAdvancedOperation::CodexUsage
        | ChannelAdvancedOperation::CodexUsageReset
        | ChannelAdvancedOperation::CodexUsageResetCredits
        | ChannelAdvancedOperation::ChannelKey
        | ChannelAdvancedOperation::FetchUpstreamModels
        | ChannelAdvancedOperation::OllamaVersion
        | ChannelAdvancedOperation::TestOne
        | ChannelAdvancedOperation::UpdateBalance => Err(ChannelAdvancedError::Invalid),
        ChannelAdvancedOperation::TestAll
        | ChannelAdvancedOperation::UpdateAllBalances
        | ChannelAdvancedOperation::ApplyAllUpstreamUpdates
        | ChannelAdvancedOperation::DetectAllUpstreamUpdates => Ok(None),
    }
}

fn required_input_channel_id(input: &Value, field: &str) -> Result<i64, ChannelAdvancedError> {
    optional_input_channel_id(input, field)?.ok_or(ChannelAdvancedError::Invalid)
}

fn optional_input_channel_id(
    input: &Value,
    field: &str,
) -> Result<Option<i64>, ChannelAdvancedError> {
    match input.get(field) {
        None | Some(Value::Null) => Ok(None),
        Some(value) => value
            .as_i64()
            .map(|id| (id > 0).then_some(id))
            .ok_or(ChannelAdvancedError::Invalid),
    }
}

fn validate_channel_kind(
    operation: ChannelAdvancedOperation,
    channel: Option<&ChannelAdvancedChannel>,
) -> Result<(), ChannelAdvancedError> {
    let required = match operation {
        ChannelAdvancedOperation::CodexRefresh
        | ChannelAdvancedOperation::CodexUsage
        | ChannelAdvancedOperation::CodexUsageReset
        | ChannelAdvancedOperation::CodexUsageResetCredits => Some(ChannelAdvancedKind::Codex),
        ChannelAdvancedOperation::OllamaDelete
        | ChannelAdvancedOperation::OllamaPull
        | ChannelAdvancedOperation::OllamaPullStream
        | ChannelAdvancedOperation::OllamaVersion => Some(ChannelAdvancedKind::Ollama),
        _ => None,
    };
    if let (Some(required), Some(channel)) = (required, channel)
        && channel.kind() != required
    {
        return Err(match required {
            ChannelAdvancedKind::Codex => ChannelAdvancedError::CodexChannelRequired,
            ChannelAdvancedKind::Ollama => ChannelAdvancedError::OllamaChannelRequired,
            ChannelAdvancedKind::Other(_) => ChannelAdvancedError::UnsupportedChannel,
        });
    }
    if matches!(
        operation,
        ChannelAdvancedOperation::CodexUsage
            | ChannelAdvancedOperation::CodexUsageReset
            | ChannelAdvancedOperation::CodexUsageResetCredits
    ) && channel.is_some_and(ChannelAdvancedChannel::is_multi_key)
    {
        return Err(ChannelAdvancedError::MultiKeyUnsupported);
    }
    if operation == ChannelAdvancedOperation::UpdateBalance
        && channel.is_some_and(ChannelAdvancedChannel::is_multi_key)
    {
        return Err(ChannelAdvancedError::MultiKeyBalanceUnsupported);
    }
    Ok(())
}

/// Dependencies for advanced channel operations.
#[derive(Clone)]
pub struct ChannelAdvancedHttpState {
    authorizer: Arc<dyn ChannelAdvancedAuthorizer>,
    provider: Arc<dyn ChannelAdvancedProvider>,
}

impl ChannelAdvancedHttpState {
    #[must_use]
    pub fn new(
        authorizer: Arc<dyn ChannelAdvancedAuthorizer>,
        provider: Arc<dyn ChannelAdvancedProvider>,
    ) -> Self {
        Self {
            authorizer,
            provider,
        }
    }
}

/// Builds the advanced channel routes.
pub fn channel_advanced_router(state: ChannelAdvancedHttpState) -> Router {
    Router::new()
        .route("/api/channel/ollama/delete", delete(ollama_delete))
        .route("/api/channel/{id}/codex/usage", get(codex_usage))
        .route(
            "/api/channel/{id}/codex/usage/reset-credits",
            get(codex_usage_reset_credits),
        )
        .route("/api/channel/fetch_models/{id}", get(fetch_upstream_models))
        .route("/api/channel/ollama/version/{id}", get(ollama_version))
        .route("/api/channel/test", get(test_all))
        .route("/api/channel/test/{id}", get(test_one))
        .route("/api/channel/update_balance", get(update_all_balances))
        .route("/api/channel/update_balance/{id}", get(update_balance))
        .route("/api/channel/{id}/codex/refresh", post(codex_refresh))
        .route(
            "/api/channel/{id}/codex/usage/reset",
            post(codex_usage_reset),
        )
        .route("/api/channel/{id}/key", post(channel_key))
        .route("/api/channel/fetch_models", post(fetch_models))
        .route("/api/channel/ollama/pull", post(ollama_pull))
        .route("/api/channel/ollama/pull/stream", post(ollama_pull_stream))
        .route(
            "/api/channel/upstream_updates/apply",
            post(apply_upstream_updates),
        )
        .route(
            "/api/channel/upstream_updates/apply_all",
            post(apply_all_upstream_updates),
        )
        .route(
            "/api/channel/upstream_updates/detect",
            post(detect_upstream_updates),
        )
        .route(
            "/api/channel/upstream_updates/detect_all",
            post(detect_all_upstream_updates),
        )
        .with_state(state)
}

#[derive(Serialize)]
struct LegacyEnvelope<T: Serialize> {
    success: bool,
    message: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<T>,
}

impl LegacyEnvelope<Value> {
    fn failure(message: &'static str) -> Self {
        Self {
            success: false,
            message,
            data: None,
        }
    }
}

async fn permit(
    state: &ChannelAdvancedHttpState,
    headers: &HeaderMap,
    permission: ChannelAdvancedPermission,
) -> Result<(), Response> {
    state
        .authorizer
        .authorize(headers, permission)
        .await
        .map_err(ChannelAdvancedError::response)
}

fn channel_id(raw: String) -> Result<i64, ChannelAdvancedError> {
    raw.parse::<i64>()
        .ok()
        .filter(|id| *id > 0)
        .ok_or(ChannelAdvancedError::Invalid)
}

fn query_input(raw_query: Option<String>) -> Result<Value, ChannelAdvancedError> {
    let query = raw_query.unwrap_or_default();
    if query.is_empty() {
        return Ok(json!({}));
    }
    // Query decoding belongs to the concrete provider adapter because legacy
    // operations do not share one stable query schema yet.  Keeping the raw
    // representation avoids a new direct URL-parser dependency in this slice.
    Ok(json!({"query": query}))
}

fn json_input(body: Bytes) -> Result<Value, ChannelAdvancedError> {
    if body.is_empty() {
        return Ok(json!({}));
    }
    serde_json::from_slice(&body).map_err(|_| ChannelAdvancedError::Invalid)
}

async fn execute(
    state: &ChannelAdvancedHttpState,
    operation: ChannelAdvancedOperation,
    channel_id: Option<i64>,
    input: Value,
) -> Response {
    match state
        .provider
        .execute_reply(ChannelAdvancedCall {
            operation,
            channel_id,
            input,
        })
        .await
    {
        Ok(reply) => reply.into_response(),
        Err(error) => error.response(),
    }
}

macro_rules! get_query_route {
    ($name:ident, $permission:expr, $operation:expr) => {
        async fn $name(
            State(state): State<ChannelAdvancedHttpState>,
            headers: HeaderMap,
            RawQuery(query): RawQuery,
        ) -> Response {
            if let Err(response) = permit(&state, &headers, $permission).await {
                return response;
            }
            let input = match query_input(query) {
                Ok(input) => input,
                Err(error) => return error.response(),
            };
            execute(&state, $operation, None, input).await
        }
    };
}

macro_rules! get_id_query_route {
    ($name:ident, $permission:expr, $operation:expr) => {
        async fn $name(
            State(state): State<ChannelAdvancedHttpState>,
            headers: HeaderMap,
            Path(raw_id): Path<String>,
            RawQuery(query): RawQuery,
        ) -> Response {
            if let Err(response) = permit(&state, &headers, $permission).await {
                return response;
            }
            let channel_id = match channel_id(raw_id) {
                Ok(channel_id) => channel_id,
                Err(error) => return error.response(),
            };
            let input = match query_input(query) {
                Ok(input) => input,
                Err(error) => return error.response(),
            };
            execute(&state, $operation, Some(channel_id), input).await
        }
    };
}

macro_rules! post_body_route {
    ($name:ident, $permission:expr, $operation:expr) => {
        async fn $name(
            State(state): State<ChannelAdvancedHttpState>,
            request: Request,
        ) -> Response {
            let (parts, body) = request.into_parts();
            if let Err(response) = permit(&state, &parts.headers, $permission).await {
                return response;
            }
            let body = match to_bytes(body, DEFAULT_ADVANCED_REQUEST_LIMIT).await {
                Ok(body) => body,
                Err(_) => return ChannelAdvancedError::Invalid.response(),
            };
            let input = match json_input(body) {
                Ok(input) => input,
                Err(error) => return error.response(),
            };
            execute(&state, $operation, None, input).await
        }
    };
}

macro_rules! post_id_body_route {
    ($name:ident, $permission:expr, $operation:expr) => {
        async fn $name(
            State(state): State<ChannelAdvancedHttpState>,
            Path(raw_id): Path<String>,
            request: Request,
        ) -> Response {
            let (parts, body) = request.into_parts();
            if let Err(response) = permit(&state, &parts.headers, $permission).await {
                return response;
            }
            let channel_id = match channel_id(raw_id) {
                Ok(channel_id) => channel_id,
                Err(error) => return error.response(),
            };
            let body = match to_bytes(body, DEFAULT_ADVANCED_REQUEST_LIMIT).await {
                Ok(body) => body,
                Err(_) => return ChannelAdvancedError::Invalid.response(),
            };
            let input = match json_input(body) {
                Ok(input) => input,
                Err(error) => return error.response(),
            };
            execute(&state, $operation, Some(channel_id), input).await
        }
    };
}

get_id_query_route!(
    codex_usage,
    ChannelAdvancedPermission::Read,
    ChannelAdvancedOperation::CodexUsage
);
get_id_query_route!(
    codex_usage_reset_credits,
    ChannelAdvancedPermission::Read,
    ChannelAdvancedOperation::CodexUsageResetCredits
);
get_id_query_route!(
    fetch_upstream_models,
    ChannelAdvancedPermission::Operate,
    ChannelAdvancedOperation::FetchUpstreamModels
);
get_id_query_route!(
    ollama_version,
    ChannelAdvancedPermission::SensitiveWrite,
    ChannelAdvancedOperation::OllamaVersion
);
get_query_route!(
    test_all,
    ChannelAdvancedPermission::Operate,
    ChannelAdvancedOperation::TestAll
);
get_id_query_route!(
    test_one,
    ChannelAdvancedPermission::Operate,
    ChannelAdvancedOperation::TestOne
);
get_query_route!(
    update_all_balances,
    ChannelAdvancedPermission::Operate,
    ChannelAdvancedOperation::UpdateAllBalances
);
get_id_query_route!(
    update_balance,
    ChannelAdvancedPermission::Operate,
    ChannelAdvancedOperation::UpdateBalance
);
post_id_body_route!(
    codex_refresh,
    ChannelAdvancedPermission::SensitiveWrite,
    ChannelAdvancedOperation::CodexRefresh
);
post_id_body_route!(
    codex_usage_reset,
    ChannelAdvancedPermission::Operate,
    ChannelAdvancedOperation::CodexUsageReset
);
post_id_body_route!(
    channel_key,
    ChannelAdvancedPermission::Root,
    ChannelAdvancedOperation::ChannelKey
);
post_body_route!(
    fetch_models,
    ChannelAdvancedPermission::SensitiveWrite,
    ChannelAdvancedOperation::FetchModels
);
post_body_route!(
    ollama_pull,
    ChannelAdvancedPermission::SensitiveWrite,
    ChannelAdvancedOperation::OllamaPull
);
post_body_route!(
    ollama_pull_stream,
    ChannelAdvancedPermission::SensitiveWrite,
    ChannelAdvancedOperation::OllamaPullStream
);
post_body_route!(
    apply_upstream_updates,
    ChannelAdvancedPermission::Write,
    ChannelAdvancedOperation::ApplyUpstreamUpdates
);
post_body_route!(
    apply_all_upstream_updates,
    ChannelAdvancedPermission::Write,
    ChannelAdvancedOperation::ApplyAllUpstreamUpdates
);
post_body_route!(
    detect_upstream_updates,
    ChannelAdvancedPermission::Operate,
    ChannelAdvancedOperation::DetectUpstreamUpdates
);
post_body_route!(
    detect_all_upstream_updates,
    ChannelAdvancedPermission::Operate,
    ChannelAdvancedOperation::DetectAllUpstreamUpdates
);
post_body_route!(
    ollama_delete,
    ChannelAdvancedPermission::SensitiveWrite,
    ChannelAdvancedOperation::OllamaDelete
);
