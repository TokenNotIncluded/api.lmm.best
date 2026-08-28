//! Legacy-compatible control-plane administration routes.
//!
//! Authentication is deliberately supplied by the host binary.  This keeps
//! these routes independent of a particular bearer-token implementation while
//! preserving the legacy distinction between root-only and administrator APIs.

use crate::auth::{DashboardAuth, UserAuthPolicyError, enforce_user_auth_view, user_auth_message};
use secrecy::SecretString;
use std::{
    net::{IpAddr, SocketAddr},
    sync::Arc,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{Path, RawQuery, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{delete, get, post},
};
use serde::{Deserialize, Serialize, de::DeserializeOwned};
use serde_json::{Value, json};
use sqlx::{PgPool, Row};
use uuid::Uuid;

const ROOT_ROLE: i64 = 100;
const ADMIN_ROLE: i64 = 10;
const CUSTOM_OAUTH_COLLECTION_PATH: &str = "/api/custom-oauth-provider/";
const STALE_INSTANCE_AFTER_SECONDS: i64 = 90;
const DISCOVERY_TIMEOUT: Duration = Duration::from_secs(20);
const DISCOVERY_MAX_RESPONSE_BYTES: usize = 1 << 20;
const DISCOVERY_MAX_REDIRECTS: usize = 3;
const MAX_CONTROL_BODY_BYTES: usize = 1 << 20;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const AUTH_USER_DISABLED: &str = "AUTH_USER_DISABLED";
const AUTH_USER_INVALID: &str = "AUTH_USER_INVALID";
const AUTH_INSUFFICIENT_PRIVILEGE: &str = "AUTH_INSUFFICIENT_PRIVILEGE";
const AUTH_TOKEN_EXPIRED: &str = "AUTH_TOKEN_EXPIRED";
const AUTH_SESSION_REVOKED: &str = "AUTH_SESSION_REVOKED";
const AUTH_INTERNAL_ERROR: &str = "AUTH_INTERNAL_ERROR";
const AUTH_UNAUTHORIZED: &str = "AUTH_UNAUTHORIZED";
const AUTH_CONSOLE_NOT_FOUND: &str = "AUTH_CONSOLE_NOT_FOUND";
const INVALID_IDENTITY_SENTINEL: i64 = -1;

#[derive(Clone, Debug)]
pub struct ControlAdminIdentity {
    pub user_id: i64,
    pub role: i64,
}

#[async_trait]
pub trait ControlAdminAuthorizer: Send + Sync {
    async fn authorize(&self, headers: &HeaderMap) -> Result<ControlAdminIdentity, &'static str>;
}

/// Production authorization adapter for the control-plane routes.
///
/// The dashboard-auth service validates the signed credential, PostgreSQL
/// session/user state, and Valkey revocation fences.  In particular, a caller
/// cannot promote itself with a role-like HTTP header.
#[derive(Clone)]
pub struct DashboardControlAdminAuthorizer {
    auth: Arc<dyn DashboardAuth>,
}

impl DashboardControlAdminAuthorizer {
    #[must_use]
    pub fn new(auth: Arc<dyn DashboardAuth>) -> Self {
        Self { auth }
    }
}

#[async_trait]
impl ControlAdminAuthorizer for DashboardControlAdminAuthorizer {
    async fn authorize(&self, headers: &HeaderMap) -> Result<ControlAdminIdentity, &'static str> {
        let token = dashboard_credential(headers).ok_or(AUTH_CONSOLE_NOT_FOUND)?;
        let user = self
            .auth
            .self_user_view_for_optional(SecretString::from(token.to_owned()))
            .await
            .map_err(|_| AUTH_CONSOLE_NOT_FOUND)?;
        if user.id <= 0 || user.status != 1 || !user.developer_access_granted {
            return Err(AUTH_CONSOLE_NOT_FOUND);
        }
        enforce_user_auth_view(&user).map_err(|error| match error {
            UserAuthPolicyError::UserDisabled => AUTH_USER_DISABLED,
            UserAuthPolicyError::InsufficientPrivilege => AUTH_INSUFFICIENT_PRIVILEGE,
            UserAuthPolicyError::InvalidUserInfo => AUTH_USER_INVALID,
        })?;
        // Root/AdminAuth compares the minimum role before validating the rest
        // of the user record. Preserve that observable order by carrying an
        // invalid-info marker to `root`/`admin` instead of rejecting here.
        let invalid_user_info =
            user.username.trim().is_empty() || !matches!(user.role, 0 | 1 | 10 | 100);
        Ok(ControlAdminIdentity {
            user_id: if invalid_user_info {
                INVALID_IDENTITY_SENTINEL
            } else {
                user.id
            },
            role: user.role,
        })
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

#[async_trait]
pub trait OAuthDiscoveryClient: Send + Sync {
    async fn discover(&self, url: &str) -> Result<Value, String>;
}

/// Proves that a durable system-task runner owns the lifecycle after the row
/// commits.  The HTTP route must not acknowledge a cleanup request when no
/// runner can consume it; merely placing a message in an unrelated cache would
/// leave an accepted task stranded forever.
#[async_trait]
pub trait SystemTaskDispatcher: Send + Sync {
    async fn ensure_log_cleanup_available(&self) -> Result<(), &'static str>;
}

/// Durable audit boundary for RootAuth/AdminAuth write routes.
///
/// The Go middleware records every administrator write, including rejected
/// handler attempts. Until the host listener supplies an equivalent durable
/// sink, write routes must fail closed rather than silently losing that audit.
#[async_trait]
pub trait ControlAdminAudit: Send + Sync {
    async fn record_write_attempt(
        &self,
        actor: &ControlAdminIdentity,
        action: &'static str,
    ) -> Result<(), &'static str>;
}

#[derive(Clone, Debug, Default)]
struct UnavailableControlAdminAudit;

#[async_trait]
impl ControlAdminAudit for UnavailableControlAdminAudit {
    async fn record_write_attempt(
        &self,
        _actor: &ControlAdminIdentity,
        _action: &'static str,
    ) -> Result<(), &'static str> {
        Err("管理员审计接口不可用")
    }
}

#[derive(Clone, Debug, Default)]
struct UnavailableSystemTaskDispatcher;

#[async_trait]
impl SystemTaskDispatcher for UnavailableSystemTaskDispatcher {
    async fn ensure_log_cleanup_available(&self) -> Result<(), &'static str> {
        Err("系统任务执行器不可用")
    }
}

/// Production OIDC discovery adapter with a DNS-pinned, HTTPS-only transport.
///
/// It deliberately does not forward dashboard headers or credentials.  Each
/// redirect is revalidated and re-resolved before a request is sent, preventing
/// an issuer-controlled document from pivoting the control plane to private
/// network addresses.
#[derive(Clone, Debug)]
pub struct HttpOAuthDiscoveryClient {
    timeout: Duration,
    allow_loopback_http: bool,
}

impl HttpOAuthDiscoveryClient {
    /// Creates the production discovery client using the shared outbound policy.
    pub fn production() -> Result<Self, String> {
        // Keep the shared policy as the canonical validation of common outbound
        // defaults. Per-request clients below additionally pin DNS answers,
        // which `reqwest::Client` cannot retrofit after construction.
        crate::outbound_http::client(DISCOVERY_TIMEOUT)
            .map_err(|_| "创建 Discovery 请求失败".to_owned())?;
        Ok(Self {
            timeout: DISCOVERY_TIMEOUT,
            allow_loopback_http: false,
        })
    }

    #[cfg(test)]
    fn test_loopback() -> Self {
        Self {
            timeout: Duration::from_secs(2),
            allow_loopback_http: true,
        }
    }

    async fn request(&self, url: reqwest::Url) -> Result<reqwest::Response, String> {
        let host = url
            .host_str()
            .ok_or_else(|| "Discovery URL 无效".to_owned())?;
        let port = url
            .port_or_known_default()
            .ok_or_else(|| "Discovery URL 无效".to_owned())?;
        let addresses = tokio::net::lookup_host((host, port))
            .await
            .map_err(|_| "Discovery 主机解析失败".to_owned())?
            .collect::<Vec<SocketAddr>>();
        if addresses.is_empty()
            || (url.scheme() == "http"
                && addresses.iter().any(|address| !address.ip().is_loopback()))
            || addresses
                .iter()
                .any(|address| !self.permitted_address(address.ip()))
        {
            return Err("Discovery URL 指向了不安全的地址".to_owned());
        }
        let client = reqwest::Client::builder()
            .use_rustls_tls()
            .no_proxy()
            .redirect(reqwest::redirect::Policy::none())
            .connect_timeout(self.timeout.min(Duration::from_secs(3)))
            .timeout(self.timeout)
            .resolve_to_addrs(host, &addresses)
            .build()
            .map_err(|_| "创建 Discovery 请求失败".to_owned())?;
        client
            .get(url)
            .header(header::ACCEPT, "application/json")
            .send()
            .await
            .map_err(|_| "Discovery 请求失败".to_owned())
    }

    fn validate_url(&self, value: &str) -> Result<reqwest::Url, String> {
        let url = reqwest::Url::parse(value.trim())
            .map_err(|_| "Discovery URL 无效，仅支持 https".to_owned())?;
        let loopback_http = self.allow_loopback_http && url.scheme() == "http";
        if !(url.scheme() == "https" || loopback_http)
            || url.host_str().is_none()
            || !url.username().is_empty()
            || url.password().is_some()
            || url.fragment().is_some()
        {
            return Err("Discovery URL 无效，仅支持 https".to_owned());
        }
        if let Some(host) = url.host_str()
            && let Ok(address) = host.trim_matches(['[', ']']).parse::<IpAddr>()
            && !self.permitted_address(address)
        {
            return Err("Discovery URL 指向了不安全的地址".to_owned());
        }
        Ok(url)
    }

    fn permitted_address(&self, address: IpAddr) -> bool {
        (self.allow_loopback_http && address.is_loopback()) || globally_routable(address)
    }

    async fn discovery_document(&self, initial: &str) -> Result<Value, String> {
        let mut url = self.validate_url(initial)?;
        let maximum_content_length = match u64::try_from(DISCOVERY_MAX_RESPONSE_BYTES) {
            Ok(maximum_content_length) => maximum_content_length,
            Err(_) => return Err("Discovery 配置过大".to_owned()),
        };
        for redirect_count in 0..=DISCOVERY_MAX_REDIRECTS {
            let mut response = self.request(url.clone()).await?;
            if response.status() == StatusCode::OK {
                if response
                    .content_length()
                    .is_some_and(|length| length > maximum_content_length)
                {
                    return Err("Discovery 配置过大".to_owned());
                }
                let mut body = Vec::new();
                while let Some(chunk) = response
                    .chunk()
                    .await
                    .map_err(|_| "读取 Discovery 响应失败".to_owned())?
                {
                    if body.len().saturating_add(chunk.len()) > DISCOVERY_MAX_RESPONSE_BYTES {
                        return Err("Discovery 配置过大".to_owned());
                    }
                    body.extend_from_slice(&chunk);
                }
                return serde_json::from_slice(&body)
                    .map_err(|_| "解析 Discovery 配置失败".to_owned())
                    .and_then(|document| self.validate_document(document));
            }
            if !response.status().is_redirection() || redirect_count == DISCOVERY_MAX_REDIRECTS {
                let status = response.status();
                let mut message = Vec::new();
                while message.len() < 512 {
                    let Some(chunk) = response
                        .chunk()
                        .await
                        .map_err(|_| "读取 Discovery 响应失败".to_owned())?
                    else {
                        break;
                    };
                    let remaining = 512 - message.len();
                    message.extend_from_slice(&chunk[..chunk.len().min(remaining)]);
                }
                let message = String::from_utf8_lossy(&message).trim().to_owned();
                return Err(if message.is_empty() {
                    status.to_string()
                } else {
                    message
                });
            }
            let location = response
                .headers()
                .get(header::LOCATION)
                .and_then(|value| value.to_str().ok())
                .ok_or_else(|| "Discovery 重定向无效".to_owned())?;
            url = url
                .join(location)
                .map_err(|_| "Discovery 重定向无效".to_owned())?;
            self.validate_url(url.as_str())?;
        }
        Err("Discovery 重定向无效".to_owned())
    }

    fn validate_document(&self, document: Value) -> Result<Value, String> {
        let object = document
            .as_object()
            .ok_or_else(|| "解析 Discovery 配置失败".to_owned())?;
        for field in ["issuer", "authorization_endpoint", "token_endpoint"] {
            let endpoint = object
                .get(field)
                .and_then(Value::as_str)
                .ok_or_else(|| "Discovery 配置缺少必要字段".to_owned())?;
            self.validate_url(endpoint)?;
        }
        if let Some(endpoint) = object.get("userinfo_endpoint").and_then(Value::as_str) {
            self.validate_url(endpoint)?;
        }
        Ok(document)
    }
}

#[async_trait]
impl OAuthDiscoveryClient for HttpOAuthDiscoveryClient {
    async fn discover(&self, url: &str) -> Result<Value, String> {
        self.discovery_document(url).await
    }
}

fn globally_routable(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(ip) => {
            let octets = ip.octets();
            !(ip.is_unspecified()
                || ip.is_loopback()
                || ip.is_private()
                || ip.is_link_local()
                || ip.is_multicast()
                || ip.is_broadcast()
                || ip.is_documentation()
                || octets[0] == 0
                || (octets[0] == 100 && (64..=127).contains(&octets[1]))
                || (octets[0] == 192 && octets[1] == 0 && octets[2] == 0)
                || (octets[0] == 198 && matches!(octets[1], 18 | 19))
                || octets[0] >= 240)
        }
        IpAddr::V6(ip) => {
            if let Some(mapped) = ip.to_ipv4_mapped() {
                return globally_routable(IpAddr::V4(mapped));
            }
            let segments = ip.segments();
            !(ip.is_unspecified()
                || ip.is_loopback()
                || ip.is_multicast()
                || ip.is_unicast_link_local()
                || ip.is_unique_local()
                // Deprecated site-local space.
                || (segments[0] & 0xffc0) == 0xfec0
                // IPv4/IPv6 translation, discard-only and local-use prefixes.
                || (segments[0] == 0x0064
                    && segments[1] == 0xff9b
                    && segments[2] == 0x0001)
                || (segments[0] == 0x0100 && segments[1..4] == [0, 0, 0])
                // IETF protocol assignments (Teredo/ORCHID/etc.) and docs.
                || (segments[0] == 0x2001
                    && (segments[1] <= 0x01ff || segments[1] == 0x0db8))
                // 6to4 and documentation prefix 3fff::/20.
                || segments[0] == 0x2002
                || (segments[0] & 0xfff0) == 0x3ff0)
        }
    }
}

#[cfg(test)]
mod discovery_tests {
    use super::{HttpOAuthDiscoveryClient, OAuthDiscoveryClient};
    use axum::{
        Router,
        http::{HeaderMap, StatusCode, header},
        response::Redirect,
        routing::get,
    };
    use serde_json::{Value, json};
    use std::{error::Error, io};

    type TestResult<T = ()> = Result<T, Box<dyn Error + Send + Sync>>;

    fn test_error(message: impl Into<String>) -> Box<dyn Error + Send + Sync> {
        Box::new(io::Error::other(message.into()))
    }

    fn discovery_error(result: Result<Value, String>, context: &'static str) -> TestResult<String> {
        match result {
            Ok(_) => Err(test_error(format!(
                "{context}: discovery succeeded when an error was required"
            ))),
            Err(error) => Ok(error),
        }
    }

    fn required_json_string<'a>(document: &'a Value, field: &'static str) -> TestResult<&'a str> {
        document
            .get(field)
            .and_then(Value::as_str)
            .ok_or_else(|| test_error(format!("discovery JSON field `{field}` is missing")))
    }

    struct TestServer {
        base: reqwest::Url,
        task: tokio::task::JoinHandle<Result<(), io::Error>>,
    }

    impl TestServer {
        fn url(&self, path: &str) -> TestResult<reqwest::Url> {
            self.base
                .join(path)
                .map_err(|error| test_error(format!("build discovery fixture URL: {error}")))
        }
    }

    impl Drop for TestServer {
        fn drop(&mut self) {
            self.task.abort();
        }
    }

    async fn server(app: Router) -> TestResult<TestServer> {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .map_err(|error| test_error(format!("bind discovery fixture listener: {error}")))?;
        let address = listener
            .local_addr()
            .map_err(|error| test_error(format!("read discovery fixture address: {error}")))?;
        let base = reqwest::Url::parse(&format!("http://{address}/"))
            .map_err(|error| test_error(format!("parse discovery fixture base URL: {error}")))?;
        let task = tokio::spawn(async move { axum::serve(listener, app).await });
        Ok(TestServer { base, task })
    }

    async fn valid_document(headers: HeaderMap) -> Result<axum::Json<Value>, StatusCode> {
        let host = headers
            .get(header::HOST)
            .ok_or(StatusCode::BAD_REQUEST)?
            .to_str()
            .map_err(|_| StatusCode::BAD_REQUEST)?;
        Ok(axum::Json(json!({
            "issuer": format!("http://{host}"),
            "authorization_endpoint": format!("http://{host}/authorize"),
            "token_endpoint": format!("http://{host}/token"),
            "userinfo_endpoint": format!("http://{host}/userinfo"),
        })))
    }

    #[tokio::test]
    async fn discovery_production_client_rejects_loopback_before_connecting() -> TestResult {
        let client = HttpOAuthDiscoveryClient::production()
            .map_err(|error| test_error(format!("build production discovery client: {error}")))?;
        let error = discovery_error(
            client
                .discover("http://127.0.0.1/.well-known/openid-configuration")
                .await,
            "reject plaintext loopback production issuer",
        )?;
        assert_eq!(error, "Discovery URL 无效，仅支持 https");
        Ok(())
    }

    #[tokio::test]
    async fn discovery_rejects_credentials_and_private_literal_endpoints() -> TestResult {
        let client = HttpOAuthDiscoveryClient::test_loopback();
        let credential_error = discovery_error(
            client
                .discover("http://user:never-log-me@127.0.0.1/document")
                .await,
            "reject credential-bearing discovery URL",
        )?;
        assert_eq!(credential_error, "Discovery URL 无效，仅支持 https");

        let production = HttpOAuthDiscoveryClient::production()
            .map_err(|error| test_error(format!("build production discovery client: {error}")))?;
        let private_error = discovery_error(
            production.discover("https://192.168.1.2/document").await,
            "reject private literal discovery URL",
        )?;
        assert_eq!(private_error, "Discovery URL 指向了不安全的地址");
        Ok(())
    }

    #[tokio::test]
    async fn discovery_loopback_contract_follows_only_revalidated_redirects() -> TestResult {
        let server = server(
            Router::new()
                .route(
                    "/loopback-redirect-start",
                    get(|| async { Redirect::temporary("/loopback-redirect-document") }),
                )
                .route("/loopback-redirect-document", get(valid_document)),
        )
        .await?;
        let request_url = server.url("loopback-redirect-start")?;
        let token_url = server.url("token")?;
        let document = HttpOAuthDiscoveryClient::test_loopback()
            .discover(request_url.as_str())
            .await
            .map_err(|error| {
                test_error(format!("follow revalidated loopback redirect: {error}"))
            })?;
        assert_eq!(
            required_json_string(&document, "token_endpoint")?,
            token_url.as_str()
        );
        Ok(())
    }

    #[tokio::test]
    async fn discovery_rejects_a_redirect_to_private_network() -> TestResult {
        let server = server(Router::new().route(
            "/private-redirect-start",
            get(|| async {
                (
                    StatusCode::FOUND,
                    [(header::LOCATION, "http://192.168.1.2/document")],
                )
            }),
        ))
        .await?;
        let request_url = server.url("private-redirect-start")?;
        let error = discovery_error(
            HttpOAuthDiscoveryClient::test_loopback()
                .discover(request_url.as_str())
                .await,
            "reject redirect to private network",
        )?;
        assert_eq!(error, "Discovery URL 指向了不安全的地址");
        Ok(())
    }

    #[tokio::test]
    async fn discovery_rejects_oversized_response_without_parsing_it() -> TestResult {
        let server = server(Router::new().route(
            "/oversized-document",
            get(|| async { "x".repeat((1 << 20) + 1) }),
        ))
        .await?;
        let request_url = server.url("oversized-document")?;
        let error = discovery_error(
            HttpOAuthDiscoveryClient::test_loopback()
                .discover(request_url.as_str())
                .await,
            "reject oversized discovery response",
        )?;
        assert_eq!(error, "Discovery 配置过大");
        Ok(())
    }

    #[tokio::test]
    async fn discovery_rejects_invalid_json_and_incomplete_endpoints() -> TestResult {
        let invalid_json =
            server(Router::new().route("/invalid-json-document", get(|| async { "{" }))).await?;
        let invalid_url = invalid_json.url("invalid-json-document")?;
        let invalid_error = discovery_error(
            HttpOAuthDiscoveryClient::test_loopback()
                .discover(invalid_url.as_str())
                .await,
            "reject invalid discovery JSON",
        )?;
        assert_eq!(invalid_error, "解析 Discovery 配置失败");

        let incomplete = server(Router::new().route(
            "/incomplete-document",
            get(|| async { axum::Json(json!({"issuer": "http://127.0.0.1"})) }),
        ))
        .await?;
        let incomplete_url = incomplete.url("incomplete-document")?;
        let incomplete_error = discovery_error(
            HttpOAuthDiscoveryClient::test_loopback()
                .discover(incomplete_url.as_str())
                .await,
            "reject incomplete discovery JSON",
        )?;
        assert_eq!(incomplete_error, "Discovery 配置缺少必要字段");
        Ok(())
    }
}

#[derive(Clone)]
pub struct ControlAdminState {
    pub pg: PgPool,
    pub valkey: Option<redis::Client>,
    pub authorizer: Arc<dyn ControlAdminAuthorizer>,
    pub discovery: Arc<dyn OAuthDiscoveryClient>,
    pub system_task_dispatcher: Arc<dyn SystemTaskDispatcher>,
    pub audit: Arc<dyn ControlAdminAudit>,
}

impl ControlAdminState {
    #[must_use]
    pub fn new(
        pg: PgPool,
        authorizer: Arc<dyn ControlAdminAuthorizer>,
        discovery: Arc<dyn OAuthDiscoveryClient>,
    ) -> Self {
        Self {
            pg,
            valkey: None,
            authorizer,
            discovery,
            system_task_dispatcher: Arc::new(UnavailableSystemTaskDispatcher),
            audit: Arc::new(UnavailableControlAdminAudit),
        }
    }

    #[must_use]
    pub fn with_valkey(mut self, valkey: redis::Client) -> Self {
        self.valkey = Some(valkey);
        self
    }

    #[must_use]
    pub fn with_system_task_dispatcher(
        mut self,
        system_task_dispatcher: Arc<dyn SystemTaskDispatcher>,
    ) -> Self {
        self.system_task_dispatcher = system_task_dispatcher;
        self
    }

    #[must_use]
    pub fn with_audit(mut self, audit: Arc<dyn ControlAdminAudit>) -> Self {
        self.audit = audit;
        self
    }
}

/// Routes migrated from `/api/custom-oauth-provider`, `/api/system-task`,
/// `/api/system-info`, and the administrator half of `/api/task`.
pub fn control_admin_router(state: ControlAdminState) -> Router {
    control_admin_read_routes()
        .route(
            "/api/custom-oauth-provider/",
            get(list_oauth).post(create_oauth),
        )
        .route(
            "/api/custom-oauth-provider/discovery",
            post(oauth_discovery),
        )
        .route(
            "/api/custom-oauth-provider/{id}",
            get(get_oauth).put(update_oauth).delete(delete_oauth),
        )
        .route(
            "/api/system-task/log-cleanup",
            post(create_log_cleanup_task),
        )
        .route(
            "/api/system-info/stale-instances",
            delete(delete_stale_instances),
        )
        .route(
            "/api/system-info/instances/{node_name}",
            delete(delete_stale_instance),
        )
        .route("/api/task", get(redirect_task_trailing_slash))
        .route("/api/task/", get(list_tasks))
        .with_state(state)
}

fn control_admin_read_routes() -> Router<ControlAdminState> {
    Router::new()
        .route("/api/system-task/list", get(list_system_tasks))
        .route("/api/system-task/current", get(current_system_task))
        .route("/api/system-task/{task_id}", get(get_system_task))
        .route("/api/system-info/instances", get(list_instances))
}

/// Mounts only the read-only control-admin views for the normal listener.
///
/// The broader control-admin router also contains OAuth discovery, task
/// creation, and instance-management operations. Keeping this route separate
/// makes the production candidate's PostgreSQL read boundary explicit and
/// prevents an accidental write or outbound-discovery mount.
pub fn control_admin_read_router(state: ControlAdminState) -> Router {
    control_admin_read_routes()
        .route(CUSTOM_OAUTH_COLLECTION_PATH, get(list_oauth))
        .with_state(state)
}

async fn redirect_task_trailing_slash() -> impl IntoResponse {
    (
        StatusCode::MOVED_PERMANENTLY,
        [(header::LOCATION, "/api/task/")],
    )
}

#[derive(Serialize)]
struct Envelope<T: Serialize> {
    success: bool,
    message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    code: Option<&'static str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<T>,
}

fn ok<T: Serialize>(data: T) -> Response {
    authenticated_response(
        Json(Envelope {
            success: true,
            message: String::new(),
            code: None,
            data: Some(data),
        })
        .into_response(),
    )
}
fn ok_message<T: Serialize>(message: &str, data: T) -> Response {
    authenticated_response(
        Json(Envelope {
            success: true,
            message: message.to_owned(),
            code: None,
            data: Some(data),
        })
        .into_response(),
    )
}
fn ok_message_only(message: &str) -> Response {
    authenticated_response(
        Json(Envelope::<Value> {
            success: true,
            message: message.to_owned(),
            code: None,
            data: None,
        })
        .into_response(),
    )
}
fn failure(status: StatusCode, message: impl Into<String>) -> Response {
    authenticated_response(
        (
            status,
            Json(Envelope::<Value> {
                success: false,
                message: message.into(),
                code: None,
                data: None,
            }),
        )
            .into_response(),
    )
}

fn failure_code(status: StatusCode, code: &'static str, message: impl Into<String>) -> Response {
    (
        status,
        Json(Envelope::<Value> {
            success: false,
            message: message.into(),
            code: Some(code),
            data: None,
        }),
    )
        .into_response()
}

fn authenticated_response(mut response: Response) -> Response {
    response.headers_mut().insert(
        axum::http::HeaderName::from_static("auth-version"),
        HeaderValue::from_static(AUTH_VERSION),
    );
    response
}

async fn parse_json_request<T: DeserializeOwned>(request: Request) -> Result<T, Box<Response>> {
    let body = to_bytes(request.into_body(), MAX_CONTROL_BODY_BYTES)
        .await
        .map_err(|_| {
            Box::new(failure(
                StatusCode::OK,
                "无效的请求参数: request body exceeds 1048576 bytes",
            ))
        })?;
    serde_json::from_slice(&body)
        .map_err(|error| Box::new(failure(StatusCode::OK, format!("无效的请求参数: {error}"))))
}

fn raw_query_string(raw_query: Option<&str>, wanted: &str) -> Option<String> {
    raw_query?.split('&').find_map(|part| {
        let (key, value) = match part.split_once('=') {
            Some(parts) => parts,
            None => (part, ""),
        };
        if decode_query_component(key).as_deref() == Some(wanted) {
            decode_query_component(value)
        } else {
            None
        }
    })
}

fn decode_query_component(value: &str) -> Option<String> {
    let bytes = value.as_bytes();
    let mut decoded = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        match bytes[index] {
            b'+' => {
                decoded.push(b' ');
                index += 1;
            }
            b'%' if index + 2 < bytes.len() => {
                decoded.push((hex_value(bytes[index + 1])? << 4) | hex_value(bytes[index + 2])?);
                index += 3;
            }
            b'%' => return None,
            byte => {
                decoded.push(byte);
                index += 1;
            }
        }
    }
    String::from_utf8(decoded).ok()
}

fn hex_value(byte: u8) -> Option<u8> {
    match byte {
        b'0'..=b'9' => Some(byte - b'0'),
        b'a'..=b'f' => Some(byte - b'a' + 10),
        b'A'..=b'F' => Some(byte - b'A' + 10),
        _ => None,
    }
}

fn raw_query_i64(raw_query: Option<&str>, wanted: &str) -> Option<i64> {
    raw_query_string(raw_query, wanted)?.parse().ok()
}

async fn root(
    state: &ControlAdminState,
    headers: &HeaderMap,
) -> Result<ControlAdminIdentity, Response> {
    let identity = state
        .authorizer
        .authorize(headers)
        .await
        .map_err(|message| authorization_failure(headers, message))?;
    if identity.role < ROOT_ROLE {
        return Err(failure_code(
            StatusCode::FORBIDDEN,
            AUTH_INSUFFICIENT_PRIVILEGE,
            localized_user_auth_message(headers, UserAuthPolicyError::InsufficientPrivilege),
        ));
    }
    if identity.user_id == INVALID_IDENTITY_SENTINEL || !matches!(identity.role, 0 | 1 | 10 | 100) {
        return Err(failure_code(
            StatusCode::UNAUTHORIZED,
            AUTH_USER_INVALID,
            localized_user_auth_message(headers, UserAuthPolicyError::InvalidUserInfo),
        ));
    }
    Ok(identity)
}

async fn admin(
    state: &ControlAdminState,
    headers: &HeaderMap,
) -> Result<ControlAdminIdentity, Response> {
    let identity = state
        .authorizer
        .authorize(headers)
        .await
        .map_err(|message| authorization_failure(headers, message))?;
    if identity.role < ADMIN_ROLE {
        return Err(failure_code(
            StatusCode::FORBIDDEN,
            AUTH_INSUFFICIENT_PRIVILEGE,
            localized_user_auth_message(headers, UserAuthPolicyError::InsufficientPrivilege),
        ));
    }
    if identity.user_id == INVALID_IDENTITY_SENTINEL || !matches!(identity.role, 0 | 1 | 10 | 100) {
        return Err(failure_code(
            StatusCode::UNAUTHORIZED,
            AUTH_USER_INVALID,
            localized_user_auth_message(headers, UserAuthPolicyError::InvalidUserInfo),
        ));
    }
    Ok(identity)
}

fn localized_user_auth_message(headers: &HeaderMap, error: UserAuthPolicyError) -> &'static str {
    user_auth_message(
        error,
        headers
            .get(header::ACCEPT_LANGUAGE)
            .and_then(|value| value.to_str().ok()),
    )
}

fn authorization_failure(headers: &HeaderMap, error: &'static str) -> Response {
    match error {
        AUTH_CONSOLE_NOT_FOUND => console_not_found(),
        AUTH_USER_DISABLED => failure_code(
            StatusCode::UNAUTHORIZED,
            AUTH_USER_DISABLED,
            localized_user_auth_message(headers, UserAuthPolicyError::UserDisabled),
        ),
        AUTH_INSUFFICIENT_PRIVILEGE => failure_code(
            StatusCode::FORBIDDEN,
            AUTH_INSUFFICIENT_PRIVILEGE,
            localized_user_auth_message(headers, UserAuthPolicyError::InsufficientPrivilege),
        ),
        AUTH_USER_INVALID => failure_code(
            StatusCode::UNAUTHORIZED,
            AUTH_USER_INVALID,
            localized_user_auth_message(headers, UserAuthPolicyError::InvalidUserInfo),
        ),
        AUTH_TOKEN_EXPIRED => failure_code(
            StatusCode::UNAUTHORIZED,
            AUTH_TOKEN_EXPIRED,
            "Unauthorized, not logged in and no access token provided",
        ),
        AUTH_SESSION_REVOKED => failure_code(
            StatusCode::UNAUTHORIZED,
            AUTH_SESSION_REVOKED,
            "Unauthorized, not logged in and no access token provided",
        ),
        AUTH_INTERNAL_ERROR => failure_code(
            StatusCode::INTERNAL_SERVER_ERROR,
            AUTH_INTERNAL_ERROR,
            "Database error, please contact the administrator",
        ),
        AUTH_UNAUTHORIZED => failure_code(
            StatusCode::UNAUTHORIZED,
            AUTH_UNAUTHORIZED,
            "Unauthorized, invalid access token",
        ),
        message => failure_code(StatusCode::UNAUTHORIZED, AUTH_UNAUTHORIZED, message),
    }
}

fn console_not_found() -> Response {
    let mut response =
        (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

async fn audit_write(
    state: &ControlAdminState,
    actor: &ControlAdminIdentity,
    action: &'static str,
) -> Result<(), Response> {
    state
        .audit
        .record_write_attempt(actor, action)
        .await
        .map_err(|message| failure(StatusCode::OK, message))
}

#[derive(Serialize)]
struct OAuthProvider {
    id: i64,
    name: String,
    slug: String,
    icon: String,
    enabled: bool,
    client_id: String,
    authorization_endpoint: String,
    token_endpoint: String,
    user_info_endpoint: String,
    scopes: String,
    user_id_field: String,
    username_field: String,
    display_name_field: String,
    email_field: String,
    well_known: String,
    auth_style: i64,
    access_policy: String,
    access_denied_message: String,
}

#[derive(Deserialize)]
struct OAuthRequest {
    name: Option<String>,
    slug: Option<String>,
    icon: Option<String>,
    enabled: Option<bool>,
    client_id: Option<String>,
    client_secret: Option<String>,
    authorization_endpoint: Option<String>,
    token_endpoint: Option<String>,
    user_info_endpoint: Option<String>,
    scopes: Option<String>,
    user_id_field: Option<String>,
    username_field: Option<String>,
    display_name_field: Option<String>,
    email_field: Option<String>,
    well_known: Option<String>,
    auth_style: Option<i64>,
    access_policy: Option<String>,
    access_denied_message: Option<String>,
}

fn required(value: Option<String>, field: &str) -> Result<String, String> {
    value
        .filter(|value| !value.is_empty())
        .ok_or_else(|| format!("无效的请求参数: {field} is required"))
}

fn normalize_slug(slug: String) -> Result<String, String> {
    let slug = slug.to_ascii_lowercase();
    if slug
        .bytes()
        .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
    {
        Ok(slug)
    } else {
        Err("provider slug must contain only lowercase letters, numbers, and hyphens".to_owned())
    }
}

fn is_builtin_oauth_slug(slug: &str) -> bool {
    matches!(slug, "github" | "discord" | "oidc" | "linuxdo")
}

fn legacy_oauth_default(value: Option<String>, default: &'static str) -> String {
    value
        .filter(|value| !value.is_empty())
        .map_or_else(|| default.to_owned(), std::convert::identity)
}

#[derive(Deserialize)]
struct AccessPolicy {
    #[serde(default)]
    logic: String,
    #[serde(default)]
    conditions: Vec<AccessCondition>,
    #[serde(default)]
    groups: Vec<AccessPolicy>,
}

#[derive(Deserialize)]
struct AccessCondition {
    field: String,
    op: String,
    value: Value,
}

fn validate_access_policy(raw: &str) -> Result<(), String> {
    if raw.trim().is_empty() {
        return Ok(());
    }
    let policy: AccessPolicy =
        serde_json::from_str(raw).map_err(|_| "access_policy must be valid JSON".to_owned())?;
    validate_access_policy_group(&policy)
        .map_err(|message| format!("access_policy is invalid: {message}"))
}

fn validate_access_policy_group(policy: &AccessPolicy) -> Result<(), String> {
    let logic = policy.logic.trim().to_ascii_lowercase();
    if !logic.is_empty() && logic != "and" && logic != "or" {
        return Err(format!("unsupported logic: {logic}"));
    }
    if policy.conditions.is_empty() && policy.groups.is_empty() {
        return Err("policy requires at least one condition or group".to_owned());
    }
    for (index, condition) in policy.conditions.iter().enumerate() {
        if condition.field.trim().is_empty() {
            return Err(format!("condition[{index}].field is required"));
        }
        let operation = condition.op.trim().to_ascii_lowercase();
        if !matches!(
            operation.as_str(),
            "eq" | "ne"
                | "gt"
                | "gte"
                | "lt"
                | "lte"
                | "in"
                | "not_in"
                | "contains"
                | "not_contains"
                | "exists"
                | "not_exists"
        ) {
            return Err(format!("condition[{index}].op is unsupported: {operation}"));
        }
        if matches!(operation.as_str(), "in" | "not_in") && !condition.value.is_array() {
            return Err(format!(
                "condition[{index}].value must be an array for op {operation}"
            ));
        }
    }
    for group in &policy.groups {
        validate_access_policy_group(group)?;
    }
    Ok(())
}

async fn slug_taken(pg: &PgPool, slug: &str, exclude_id: i64) -> bool {
    let mut query = String::from("SELECT COUNT(*) FROM custom_oauth_providers WHERE slug = $1");
    if exclude_id > 0 {
        query.push_str(" AND id != $2");
    }
    let query = sqlx::query_scalar::<_, i64>(&query).bind(slug);
    let result = if exclude_id > 0 {
        query.bind(exclude_id).fetch_one(pg).await
    } else {
        query.fetch_one(pg).await
    };
    // Frozen Go deliberately treats lookup failure as occupied.
    result.map_or(true, |count| count > 0)
}

async fn ensure_oauth_cache(state: &ControlAdminState) -> Result<(), Response> {
    let Some(client) = &state.valkey else {
        return Err(failure(StatusCode::OK, "OAuth 提供商缓存不可用"));
    };
    let mut connection = client
        .get_multiplexed_async_connection()
        .await
        .map_err(|_| failure(StatusCode::OK, "OAuth 提供商缓存不可用"))?;
    redis::cmd("PING")
        .query_async::<String>(&mut connection)
        .await
        .map(|_| ())
        .map_err(|_| failure(StatusCode::OK, "OAuth 提供商缓存不可用"))
}

fn database_failure(error: impl std::fmt::Display) -> Response {
    failure(StatusCode::OK, error.to_string())
}

async fn list_oauth(State(state): State<ControlAdminState>, headers: HeaderMap) -> Response {
    if let Err(response) = root(&state, &headers).await {
        return response;
    }
    let rows = match sqlx::query("SELECT id, name, slug, COALESCE(icon,''), COALESCE(enabled,FALSE), COALESCE(client_id,''), COALESCE(authorization_endpoint,''), COALESCE(token_endpoint,''), COALESCE(user_info_endpoint,''), COALESCE(scopes,''), COALESCE(user_id_field,''), COALESCE(username_field,''), COALESCE(display_name_field,''), COALESCE(email_field,''), COALESCE(well_known,''), COALESCE(auth_style,0), COALESCE(access_policy,''), COALESCE(access_denied_message,'') FROM custom_oauth_providers ORDER BY id ASC").fetch_all(&state.pg).await { Ok(rows) => rows, Err(error) => return database_failure(error) };
    match rows
        .into_iter()
        .map(oauth_from_row)
        .collect::<Result<Vec<_>, _>>()
    {
        Ok(providers) => ok(providers),
        Err(error) => database_failure(error),
    }
}

async fn get_oauth(
    State(state): State<ControlAdminState>,
    headers: HeaderMap,
    Path(id): Path<String>,
) -> Response {
    if let Err(response) = root(&state, &headers).await {
        return response;
    }
    let Ok(id) = id.parse::<i64>() else {
        return failure(StatusCode::OK, "无效的 ID");
    };
    match oauth_by_id(&state.pg, id).await {
        Ok(Some(provider)) => ok(provider),
        Ok(None) => failure(StatusCode::OK, "未找到该 OAuth 提供商"),
        Err(error) => database_failure(error),
    }
}

async fn create_oauth(State(state): State<ControlAdminState>, request: Request) -> Response {
    let actor = match root(&state, request.headers()).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    if let Err(response) = audit_write(&state, &actor, "custom_oauth_provider.create").await {
        return response;
    }
    let request = match parse_json_request::<OAuthRequest>(request).await {
        Ok(request) => request,
        Err(response) => return *response,
    };
    let name = match required(request.name, "name") {
        Ok(value) => value,
        Err(message) => return failure(StatusCode::OK, message),
    };
    let slug = match required(request.slug, "slug").and_then(normalize_slug) {
        Ok(value) => value,
        Err(message) => return failure(StatusCode::OK, message),
    };
    let client_id = match required(request.client_id, "client_id") {
        Ok(value) => value,
        Err(message) => return failure(StatusCode::OK, message),
    };
    let client_secret = match required(request.client_secret, "client_secret") {
        Ok(value) => value,
        Err(message) => return failure(StatusCode::OK, message),
    };
    let authorization_endpoint =
        match required(request.authorization_endpoint, "authorization_endpoint") {
            Ok(value) => value,
            Err(message) => return failure(StatusCode::OK, message),
        };
    let token_endpoint = match required(request.token_endpoint, "token_endpoint") {
        Ok(value) => value,
        Err(message) => return failure(StatusCode::OK, message),
    };
    let user_info_endpoint = match required(request.user_info_endpoint, "user_info_endpoint") {
        Ok(value) => value,
        Err(message) => return failure(StatusCode::OK, message),
    };
    if slug_taken(&state.pg, &slug, 0).await {
        return failure(StatusCode::OK, "该 Slug 已被使用");
    }
    if is_builtin_oauth_slug(&slug) {
        return failure(StatusCode::OK, "该 Slug 与内置 OAuth 提供商冲突");
    }
    let access_policy = request
        .access_policy
        .map_or_else(String::new, std::convert::identity);
    if let Err(message) = validate_access_policy(&access_policy) {
        return failure(StatusCode::OK, message);
    }
    if let Err(response) = ensure_oauth_cache(&state).await {
        return response;
    }
    // These are the defaults applied by the frozen Go model validator. They
    // are not frontend/API-field inventions.
    let scopes = legacy_oauth_default(request.scopes, "openid profile email");
    let user_id_field = legacy_oauth_default(request.user_id_field, "sub");
    let username_field = legacy_oauth_default(request.username_field, "preferred_username");
    let display_name_field = legacy_oauth_default(request.display_name_field, "name");
    let email_field = legacy_oauth_default(request.email_field, "email");
    let result = sqlx::query("INSERT INTO custom_oauth_providers (name,slug,icon,enabled,client_id,client_secret,authorization_endpoint,token_endpoint,user_info_endpoint,scopes,user_id_field,username_field,display_name_field,email_field,well_known,auth_style,access_policy,access_denied_message,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,NOW(),NOW()) RETURNING id")
        .bind(name).bind(&slug).bind(request.icon.map_or_else(String::new, std::convert::identity)).bind(request.enabled.map_or(false, std::convert::identity)).bind(client_id).bind(client_secret).bind(authorization_endpoint).bind(token_endpoint).bind(user_info_endpoint).bind(scopes).bind(user_id_field).bind(username_field).bind(display_name_field).bind(email_field).bind(request.well_known.map_or_else(String::new, std::convert::identity)).bind(request.auth_style.map_or(0, std::convert::identity)).bind(access_policy).bind(request.access_denied_message.map_or_else(String::new, std::convert::identity)).fetch_one(&state.pg).await;
    let id = match result {
        Ok(row) => match row.try_get::<i64, _>("id") {
            Ok(id) => id,
            Err(error) => return database_failure(error),
        },
        Err(error) if is_unique(&error) => return failure(StatusCode::OK, "该 Slug 已被使用"),
        Err(error) => return database_failure(error),
    };
    if let Err(response) = invalidate_oauth(&state).await {
        return response;
    }
    match oauth_by_id(&state.pg, id).await {
        Ok(Some(provider)) => ok_message("创建成功", provider),
        Ok(None) => failure(StatusCode::OK, "未找到该 OAuth 提供商"),
        Err(error) => database_failure(error),
    }
}

async fn update_oauth(
    State(state): State<ControlAdminState>,
    Path(id): Path<String>,
    request: Request,
) -> Response {
    let actor = match root(&state, request.headers()).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    if let Err(response) = audit_write(&state, &actor, "custom_oauth_provider.update").await {
        return response;
    }
    let Ok(id) = id.parse::<i64>() else {
        return failure(StatusCode::OK, "无效的 ID");
    };
    let request = match parse_json_request::<OAuthRequest>(request).await {
        Ok(request) => request,
        Err(response) => return *response,
    };
    let Some(current) = (match oauth_by_id(&state.pg, id).await {
        Ok(value) => value,
        Err(error) => return database_failure(error),
    }) else {
        return failure(StatusCode::OK, "未找到该 OAuth 提供商");
    };
    let slug = match request.slug.filter(|slug| !slug.is_empty()) {
        Some(slug) => match normalize_slug(slug) {
            Ok(slug) => slug,
            Err(message) => return failure(StatusCode::OK, message),
        },
        None => current.slug.clone(),
    };
    if slug != current.slug {
        if slug_taken(&state.pg, &slug, id).await {
            return failure(StatusCode::OK, "该 Slug 已被使用");
        }
        if is_builtin_oauth_slug(&slug) {
            return failure(StatusCode::OK, "该 Slug 与内置 OAuth 提供商冲突");
        }
    }
    let access_policy = request
        .access_policy
        .map_or_else(|| current.access_policy.clone(), std::convert::identity);
    if let Err(message) = validate_access_policy(&access_policy) {
        return failure(StatusCode::OK, message);
    }
    if let Err(response) = ensure_oauth_cache(&state).await {
        return response;
    }
    let result = sqlx::query("UPDATE custom_oauth_providers SET name=$1,slug=$2,icon=$3,enabled=$4,client_id=$5,client_secret=CASE WHEN $6 = '' THEN client_secret ELSE $6 END,authorization_endpoint=$7,token_endpoint=$8,user_info_endpoint=$9,scopes=$10,user_id_field=$11,username_field=$12,display_name_field=$13,email_field=$14,well_known=$15,auth_style=$16,access_policy=$17,access_denied_message=$18,updated_at=NOW() WHERE id=$19")
        .bind(request.name.filter(|value| !value.is_empty()).map_or(current.name, std::convert::identity)).bind(slug).bind(request.icon.map_or(current.icon, std::convert::identity)).bind(request.enabled.map_or(current.enabled, std::convert::identity)).bind(request.client_id.filter(|value| !value.is_empty()).map_or(current.client_id, std::convert::identity)).bind(request.client_secret.map_or_else(String::new, std::convert::identity)).bind(request.authorization_endpoint.filter(|value| !value.is_empty()).map_or(current.authorization_endpoint, std::convert::identity)).bind(request.token_endpoint.filter(|value| !value.is_empty()).map_or(current.token_endpoint, std::convert::identity)).bind(request.user_info_endpoint.filter(|value| !value.is_empty()).map_or(current.user_info_endpoint, std::convert::identity)).bind(request.scopes.filter(|value| !value.is_empty()).map_or(current.scopes, std::convert::identity)).bind(request.user_id_field.filter(|value| !value.is_empty()).map_or(current.user_id_field, std::convert::identity)).bind(request.username_field.filter(|value| !value.is_empty()).map_or(current.username_field, std::convert::identity)).bind(request.display_name_field.filter(|value| !value.is_empty()).map_or(current.display_name_field, std::convert::identity)).bind(request.email_field.filter(|value| !value.is_empty()).map_or(current.email_field, std::convert::identity)).bind(request.well_known.map_or(current.well_known, std::convert::identity)).bind(request.auth_style.map_or(current.auth_style, std::convert::identity)).bind(access_policy).bind(request.access_denied_message.map_or(current.access_denied_message, std::convert::identity)).bind(id).execute(&state.pg).await;
    match result {
        Ok(_) => {}
        Err(error) if is_unique(&error) => return failure(StatusCode::OK, "该 Slug 已被使用"),
        Err(error) => return database_failure(error),
    };
    if let Err(response) = invalidate_oauth(&state).await {
        return response;
    }
    match oauth_by_id(&state.pg, id).await {
        Ok(Some(provider)) => ok_message("更新成功", provider),
        Ok(None) => failure(StatusCode::OK, "未找到该 OAuth 提供商"),
        Err(error) => database_failure(error),
    }
}

async fn delete_oauth(
    State(state): State<ControlAdminState>,
    headers: HeaderMap,
    Path(id): Path<String>,
) -> Response {
    let actor = match root(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    if let Err(response) = audit_write(&state, &actor, "custom_oauth_provider.delete").await {
        return response;
    }
    let Ok(id) = id.parse::<i64>() else {
        return failure(StatusCode::OK, "无效的 ID");
    };
    let provider = match oauth_by_id(&state.pg, id).await {
        Ok(Some(provider)) => provider,
        Ok(None) => return failure(StatusCode::OK, "未找到该 OAuth 提供商"),
        Err(error) => return database_failure(error),
    };
    let bindings = match sqlx::query_scalar::<_, i64>(
        "SELECT COUNT(*) FROM user_oauth_bindings WHERE provider_id = $1",
    )
    .bind(id)
    .fetch_one(&state.pg)
    .await
    {
        Ok(value) => value,
        Err(_) => return failure(StatusCode::OK, "检查用户绑定时发生错误，请稍后重试"),
    };
    if bindings > 0 {
        return failure(
            StatusCode::OK,
            "该 OAuth 提供商还有用户绑定，无法删除。请先解除所有用户绑定。",
        );
    }
    if let Err(response) = ensure_oauth_cache(&state).await {
        return response;
    }
    match sqlx::query("DELETE FROM custom_oauth_providers WHERE id = $1")
        .bind(id)
        .execute(&state.pg)
        .await
    {
        Ok(result) if result.rows_affected() > 0 => {
            if let Err(response) = invalidate_oauth(&state).await {
                return response;
            }
            let _deleted_slug = provider.slug;
            ok_message_only("删除成功")
        }
        Ok(_) => failure(StatusCode::OK, "未找到该 OAuth 提供商"),
        Err(error) => database_failure(error),
    }
}

#[derive(Deserialize)]
struct DiscoveryRequest {
    well_known_url: Option<String>,
    issuer_url: Option<String>,
}
async fn oauth_discovery(State(state): State<ControlAdminState>, request: Request) -> Response {
    if let Err(response) = root(&state, request.headers()).await {
        return response;
    }
    let request = match parse_json_request::<DiscoveryRequest>(request).await {
        Ok(request) => request,
        Err(response) => return *response,
    };
    let well_known_url = request
        .well_known_url
        .map_or_else(String::new, std::convert::identity)
        .trim()
        .to_owned();
    let issuer_url = request
        .issuer_url
        .map_or_else(String::new, std::convert::identity)
        .trim()
        .to_owned();
    if well_known_url.is_empty() && issuer_url.is_empty() {
        return failure(StatusCode::OK, "请先填写 Discovery URL 或 Issuer URL");
    }
    let url = if well_known_url.is_empty() {
        format!(
            "{}/.well-known/openid-configuration",
            issuer_url.trim_end_matches('/')
        )
    } else {
        well_known_url
    };
    match reqwest::Url::parse(url.trim()) {
        Ok(parsed)
            if parsed.host_str().is_some() && matches!(parsed.scheme(), "http" | "https") => {}
        Ok(_) | Err(_) => {
            return failure(StatusCode::OK, "Discovery URL 无效，仅支持 http/https");
        }
    }
    match state.discovery.discover(&url).await {
        Ok(discovery) => ok(json!({"well_known_url": url, "discovery": discovery})),
        Err(message) => failure(StatusCode::OK, discovery_error_message(message)),
    }
}

fn discovery_error_message(message: String) -> String {
    if [
        "获取 Discovery 配置失败:",
        "创建 Discovery 请求失败",
        "解析 Discovery 配置失败",
    ]
    .iter()
    .any(|prefix| message.starts_with(prefix))
    {
        message
    } else {
        format!("获取 Discovery 配置失败: {message}")
    }
}

async fn list_system_tasks(
    State(state): State<ControlAdminState>,
    headers: HeaderMap,
    query: RawQuery,
) -> Response {
    if let Err(response) = root(&state, &headers).await {
        return response;
    }
    let requested_limit =
        raw_query_i64(query.0.as_deref(), "limit").map_or(0, std::convert::identity);
    let limit = if requested_limit <= 0 {
        20
    } else {
        requested_limit.min(100)
    };
    match sqlx::query("SELECT id,task_id,type,status,active_key,payload,state,result,error,locked_by,created_at,updated_at FROM system_tasks ORDER BY id DESC LIMIT $1").bind(limit).fetch_all(&state.pg).await {
        Ok(rows) => match rows.into_iter().map(system_task_from_row).collect::<Result<Vec<_>, _>>() {
            Ok(tasks) => ok(tasks),
            Err(error) => database_failure(error),
        },
        Err(error) => database_failure(error),
    }
}

async fn current_system_task(
    State(state): State<ControlAdminState>,
    headers: HeaderMap,
    query: RawQuery,
) -> Response {
    if let Err(response) = root(&state, &headers).await {
        return response;
    }
    let Some(task_type) =
        raw_query_string(query.0.as_deref(), "type").filter(|value| !value.is_empty())
    else {
        return failure(StatusCode::OK, "type is required");
    };
    match sqlx::query("SELECT id,task_id,type,status,active_key,payload,state,result,error,locked_by,created_at,updated_at FROM system_tasks WHERE type=$1 AND status IN ('pending','running') ORDER BY id DESC LIMIT 1").bind(task_type).fetch_optional(&state.pg).await {
        Ok(Some(row)) => system_task_from_row(row).map_or_else(
            database_failure,
            ok,
        ),
        Ok(None) => ok(Value::Null),
        Err(error) => database_failure(error),
    }
}

async fn get_system_task(
    State(state): State<ControlAdminState>,
    headers: HeaderMap,
    Path(task_id): Path<String>,
) -> Response {
    if let Err(response) = root(&state, &headers).await {
        return response;
    }
    if task_id.is_empty() {
        return failure(StatusCode::OK, "task id is required");
    }
    match sqlx::query("SELECT id,task_id,type,status,active_key,payload,state,result,error,locked_by,created_at,updated_at FROM system_tasks WHERE task_id=$1").bind(task_id).fetch_optional(&state.pg).await {
        Ok(Some(row)) => system_task_from_row(row).map_or_else(
            database_failure,
            ok,
        ),
        Ok(None) => failure(StatusCode::NOT_FOUND, "task not found"),
        Err(error) => database_failure(error),
    }
}

async fn create_log_cleanup_task(
    State(state): State<ControlAdminState>,
    headers: HeaderMap,
    query: RawQuery,
) -> Response {
    let actor = match root(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    if let Err(response) = audit_write(&state, &actor, "system_task.log_cleanup").await {
        return response;
    }
    let Some(target_timestamp) =
        raw_query_i64(query.0.as_deref(), "target_timestamp").filter(|value| *value != 0)
    else {
        return failure(StatusCode::OK, "target timestamp is required");
    };
    if let Err(message) = state
        .system_task_dispatcher
        .ensure_log_cleanup_available()
        .await
    {
        return failure(StatusCode::OK, message);
    }

    let task_id = format!("systask_{}", Uuid::new_v4().simple());
    let now = unix_now();
    let task = sqlx::query("INSERT INTO system_tasks (task_id,type,status,active_key,payload,state,result,error,locked_by,created_at,updated_at) VALUES ($1,'log_cleanup','pending','log_cleanup',$2,$3,'','','',$4,$4) RETURNING id,task_id,type,status,active_key,payload,state,result,error,locked_by,created_at,updated_at")
        .bind(&task_id)
        .bind(json!({"target_timestamp": target_timestamp, "batch_size": 100}).to_string())
        .bind(json!({}).to_string())
        .bind(now)
        .fetch_one(&state.pg)
        .await;
    match task {
        Ok(row) => system_task_from_row(row).map_or_else(database_failure, ok),
        Err(error) if is_unique(&error) => {
            match active_system_task(&state.pg, "log_cleanup").await {
                Ok(Some(row)) => system_task_from_row(row).map_or_else(database_failure, ok),
                Ok(None) => failure(StatusCode::OK, "系统任务状态冲突"),
                Err(error) => database_failure(error),
            }
        }
        Err(error) => database_failure(error),
    }
}

async fn active_system_task(
    pg: &PgPool,
    task_type: &str,
) -> Result<Option<sqlx::postgres::PgRow>, sqlx::Error> {
    sqlx::query("SELECT id,task_id,type,status,active_key,payload,state,result,error,locked_by,created_at,updated_at FROM system_tasks WHERE type=$1 AND status IN ('pending','running') ORDER BY id DESC LIMIT 1")
        .bind(task_type)
        .fetch_optional(pg)
        .await
}

async fn list_instances(State(state): State<ControlAdminState>, headers: HeaderMap) -> Response {
    if let Err(response) = root(&state, &headers).await {
        return response;
    }
    let now = unix_now();
    match sqlx::query("SELECT node_name,COALESCE(info,''),COALESCE(started_at,0),COALESCE(last_seen_at,0) FROM system_instances ORDER BY last_seen_at DESC").fetch_all(&state.pg).await {
        Ok(rows) => match rows.into_iter().map(|row| instance_from_row(row, now)).collect::<Result<Vec<_>, _>>() {
            Ok(instances) => ok(instances),
            Err(error) => database_failure(error),
        },
        Err(error) => database_failure(error),
    }
}

async fn delete_stale_instances(
    State(state): State<ControlAdminState>,
    headers: HeaderMap,
) -> Response {
    let actor = match root(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    if let Err(response) = audit_write(&state, &actor, "system_info.delete_stale").await {
        return response;
    }
    match sqlx::query("DELETE FROM system_instances WHERE last_seen_at < $1")
        .bind(unix_now() - STALE_INSTANCE_AFTER_SECONDS)
        .execute(&state.pg)
        .await
    {
        Ok(result) => ok(json!({"deleted_count": result.rows_affected()})),
        Err(error) => database_failure(error),
    }
}

async fn delete_stale_instance(
    State(state): State<ControlAdminState>,
    headers: HeaderMap,
    Path(node_name): Path<String>,
) -> Response {
    let actor = match root(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    if let Err(response) = audit_write(&state, &actor, "system_info.delete_instance").await {
        return response;
    }
    if node_name.trim().is_empty() {
        return failure(StatusCode::OK, "node name is required");
    }
    match sqlx::query("DELETE FROM system_instances WHERE node_name=$1 AND last_seen_at < $2")
        .bind(node_name)
        .bind(unix_now() - STALE_INSTANCE_AFTER_SECONDS)
        .execute(&state.pg)
        .await
    {
        Ok(result) if result.rows_affected() > 0 => ok(json!({"deleted_count": 1})),
        Ok(_) => failure(StatusCode::OK, "instance is not stale or no longer exists"),
        Err(error) => database_failure(error),
    }
}

async fn list_tasks(
    State(state): State<ControlAdminState>,
    headers: HeaderMap,
    query: RawQuery,
) -> Response {
    if let Err(response) = admin(&state, &headers).await {
        return response;
    }
    let raw = query.0.as_deref();
    let page = raw_query_i64(raw, "p")
        .filter(|page| *page != 0)
        .map_or(1, std::convert::identity);
    let mut page_size = raw_query_i64(raw, "page_size").map_or(0, std::convert::identity);
    if page_size == 0 {
        page_size = raw_query_i64(raw, "ps").map_or(0, std::convert::identity);
    }
    if page_size == 0 {
        page_size = raw_query_i64(raw, "size").map_or(0, std::convert::identity);
    }
    if page_size == 0 {
        page_size = 10;
    }
    page_size = page_size.min(100);
    // GORM omits negative LIMIT/OFFSET clauses. Preserve the response values
    // while representing an omitted LIMIT as SQL NULL and an omitted OFFSET
    // as zero.
    let query_limit = (page_size >= 0).then_some(page_size);
    let query_offset = (page - 1).saturating_mul(page_size).max(0);
    let platform =
        raw_query_string(raw, "platform").map_or_else(String::new, std::convert::identity);
    let task_id = raw_query_string(raw, "task_id").map_or_else(String::new, std::convert::identity);
    let status = raw_query_string(raw, "status").map_or_else(String::new, std::convert::identity);
    let action = raw_query_string(raw, "action").map_or_else(String::new, std::convert::identity);
    let start_timestamp = raw_query_i64(raw, "start_timestamp").map_or(0, std::convert::identity);
    let end_timestamp = raw_query_i64(raw, "end_timestamp").map_or(0, std::convert::identity);
    let channel_id_text =
        raw_query_string(raw, "channel_id").map_or_else(String::new, std::convert::identity);
    let channel_id = if channel_id_text.is_empty() {
        None
    } else {
        match channel_id_text.parse::<i64>() {
            Ok(channel_id) => Some(channel_id),
            Err(_) => {
                return ok(json!({
                    "page": page,
                    "page_size": page_size,
                    "total": 0,
                    "items": Vec::<Value>::new(),
                }));
            }
        }
    };
    let rows = sqlx::query("SELECT t.id,t.created_at,t.updated_at,COALESCE(t.task_id,''),COALESCE(t.platform,''),COALESCE(t.user_id,0),COALESCE(t.\"group\",''),COALESCE(t.channel_id,0),COALESCE(t.quota,0),COALESCE(t.action,''),COALESCE(t.status,''),COALESCE(t.fail_reason,''),COALESCE(t.submit_time,0),COALESCE(t.start_time,0),COALESCE(t.finish_time,0),COALESCE(t.progress,''),t.properties,t.private_data,t.data,COALESCE(u.username,'') AS username FROM tasks t LEFT JOIN users u ON u.id=t.user_id WHERE ($1='' OR t.platform=$1) AND ($2='' OR t.task_id=$2) AND ($3='' OR t.status=$3) AND ($4='' OR t.action=$4) AND ($5=0 OR t.submit_time >= $5) AND ($6=0 OR t.submit_time <= $6) AND ($7::BIGINT IS NULL OR t.channel_id=$7) ORDER BY t.id DESC LIMIT $8 OFFSET $9").bind(&platform).bind(&task_id).bind(&status).bind(&action).bind(start_timestamp).bind(end_timestamp).bind(channel_id).bind(query_limit).bind(query_offset).fetch_all(&state.pg).await;
    let rows = match rows {
        Ok(rows) => rows,
        Err(error) => return database_failure(error),
    };
    let items = match rows
        .into_iter()
        .map(task_from_row)
        .collect::<Result<Vec<_>, _>>()
    {
        Ok(items) => items,
        Err(error) => return database_failure(error),
    };
    let total = match sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM tasks WHERE ($1='' OR platform=$1) AND ($2='' OR task_id=$2) AND ($3='' OR status=$3) AND ($4='' OR action=$4) AND ($5=0 OR submit_time >= $5) AND ($6=0 OR submit_time <= $6) AND ($7::BIGINT IS NULL OR channel_id=$7)").bind(&platform).bind(&task_id).bind(&status).bind(&action).bind(start_timestamp).bind(end_timestamp).bind(channel_id).fetch_one(&state.pg).await { Ok(total) => total, Err(error) => return database_failure(error) };
    ok(json!({"page": page, "page_size": page_size, "total": total, "items": items}))
}

async fn oauth_by_id(pg: &PgPool, id: i64) -> Result<Option<OAuthProvider>, sqlx::Error> {
    sqlx::query("SELECT id, name, slug, COALESCE(icon,''), COALESCE(enabled,FALSE), COALESCE(client_id,''), COALESCE(authorization_endpoint,''), COALESCE(token_endpoint,''), COALESCE(user_info_endpoint,''), COALESCE(scopes,''), COALESCE(user_id_field,''), COALESCE(username_field,''), COALESCE(display_name_field,''), COALESCE(email_field,''), COALESCE(well_known,''), COALESCE(auth_style,0), COALESCE(access_policy,''), COALESCE(access_denied_message,'') FROM custom_oauth_providers WHERE id=$1")
        .bind(id)
        .fetch_optional(pg)
        .await?
        .map(oauth_from_row)
        .transpose()
}
fn oauth_from_row(row: sqlx::postgres::PgRow) -> Result<OAuthProvider, sqlx::Error> {
    Ok(OAuthProvider {
        id: row.try_get("id")?,
        name: row.try_get("name")?,
        slug: row.try_get("slug")?,
        icon: row.try_get(3)?,
        enabled: row.try_get(4)?,
        client_id: row.try_get(5)?,
        authorization_endpoint: row.try_get(6)?,
        token_endpoint: row.try_get(7)?,
        user_info_endpoint: row.try_get(8)?,
        scopes: row.try_get(9)?,
        user_id_field: row.try_get(10)?,
        username_field: row.try_get(11)?,
        display_name_field: row.try_get(12)?,
        email_field: row.try_get(13)?,
        well_known: row.try_get(14)?,
        auth_style: row.try_get(15)?,
        access_policy: row.try_get(16)?,
        access_denied_message: row.try_get(17)?,
    })
}
fn system_task_from_row(row: sqlx::postgres::PgRow) -> Result<Value, sqlx::Error> {
    let active_key = row.try_get::<Option<String>, _>("active_key")?;
    let mut response = serde_json::Map::from_iter([
        ("id".to_owned(), json!(row.try_get::<i64, _>("id")?)),
        (
            "task_id".to_owned(),
            json!(row.try_get::<String, _>("task_id")?),
        ),
        ("type".to_owned(), json!(row.try_get::<String, _>("type")?)),
        (
            "status".to_owned(),
            json!(row.try_get::<String, _>("status")?),
        ),
        (
            "payload".to_owned(),
            json_value(row.try_get::<Option<String>, _>("payload")?),
        ),
        (
            "state".to_owned(),
            json_value(row.try_get::<Option<String>, _>("state")?),
        ),
        (
            "result".to_owned(),
            json_value(row.try_get::<Option<String>, _>("result")?),
        ),
        (
            "error".to_owned(),
            json!(
                row.try_get::<Option<String>, _>("error")?
                    .map_or_else(String::new, std::convert::identity)
            ),
        ),
        (
            "locked_by".to_owned(),
            json!(
                row.try_get::<Option<String>, _>("locked_by")?
                    .map_or_else(String::new, std::convert::identity)
            ),
        ),
        (
            "created_at".to_owned(),
            json!(
                row.try_get::<Option<i64>, _>("created_at")?
                    .map_or(0, std::convert::identity)
            ),
        ),
        (
            "updated_at".to_owned(),
            json!(
                row.try_get::<Option<i64>, _>("updated_at")?
                    .map_or(0, std::convert::identity)
            ),
        ),
    ]);
    if let Some(active_key) = active_key {
        response.insert("active_key".to_owned(), Value::String(active_key));
    }
    Ok(Value::Object(response))
}
fn instance_from_row(row: sqlx::postgres::PgRow, now: i64) -> Result<Value, sqlx::Error> {
    let last_seen_at: i64 = row.try_get(3)?;
    let info: String = row.try_get(1)?;
    Ok(
        json!({"node_name": row.try_get::<String,_>(0)?, "status": if now-last_seen_at > STALE_INSTANCE_AFTER_SECONDS {"stale"} else {"online"}, "stale_after_seconds": STALE_INSTANCE_AFTER_SECONDS, "started_at": row.try_get::<i64,_>(2)?, "last_seen_at": last_seen_at, "info": json_value(Some(info))}),
    )
}
fn task_from_row(row: sqlx::postgres::PgRow) -> Result<Value, sqlx::Error> {
    let fail_reason = row.try_get::<String, _>(11)?;
    let private_data = row
        .try_get::<Option<Value>, _>(17)?
        .map_or(Value::Null, std::convert::identity);
    let result_url = private_data
        .get("result_url")
        .and_then(Value::as_str)
        .filter(|value| !value.is_empty())
        .map_or(fail_reason.as_str(), std::convert::identity)
        .to_owned();
    let properties = row
        .try_get::<Option<Value>, _>(16)?
        .map_or_else(|| json!({"input": ""}), std::convert::identity);
    let username = row.try_get::<String, _>("username")?;
    let mut response = serde_json::Map::from_iter([
        ("id".to_owned(), json!(row.try_get::<i64, _>("id")?)),
        (
            "created_at".to_owned(),
            json!(row.try_get::<i64, _>("created_at")?),
        ),
        (
            "updated_at".to_owned(),
            json!(row.try_get::<i64, _>("updated_at")?),
        ),
        (
            "task_id".to_owned(),
            json!(row.try_get::<String, _>("task_id")?),
        ),
        ("platform".to_owned(), json!(row.try_get::<String, _>(4)?)),
        ("user_id".to_owned(), json!(row.try_get::<i64, _>(5)?)),
        ("group".to_owned(), json!(row.try_get::<String, _>(6)?)),
        ("channel_id".to_owned(), json!(row.try_get::<i64, _>(7)?)),
        ("quota".to_owned(), json!(row.try_get::<i64, _>(8)?)),
        ("action".to_owned(), json!(row.try_get::<String, _>(9)?)),
        ("status".to_owned(), json!(row.try_get::<String, _>(10)?)),
        ("fail_reason".to_owned(), json!(fail_reason)),
        ("submit_time".to_owned(), json!(row.try_get::<i64, _>(12)?)),
        ("start_time".to_owned(), json!(row.try_get::<i64, _>(13)?)),
        ("finish_time".to_owned(), json!(row.try_get::<i64, _>(14)?)),
        ("progress".to_owned(), json!(row.try_get::<String, _>(15)?)),
        ("properties".to_owned(), properties),
        (
            "data".to_owned(),
            row.try_get::<Option<Value>, _>(18)?
                .map_or(Value::Null, std::convert::identity),
        ),
    ]);
    if !result_url.is_empty() {
        response.insert("result_url".to_owned(), Value::String(result_url));
    }
    if !username.is_empty() {
        response.insert("username".to_owned(), Value::String(username));
    }
    Ok(Value::Object(response))
}
fn json_value(value: Option<String>) -> Value {
    match value {
        Some(value) if value.is_empty() => Value::Null,
        Some(value) => match serde_json::from_str(&value) {
            Ok(json) => json,
            Err(_) => Value::String(value),
        },
        None => Value::Null,
    }
}
fn unix_now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs() as i64)
}
fn is_unique(error: &sqlx::Error) -> bool {
    error
        .as_database_error()
        .and_then(|error| error.code())
        .is_some_and(|code| code == "23505")
}
async fn invalidate_oauth(state: &ControlAdminState) -> Result<(), Response> {
    let Some(client) = &state.valkey else {
        return Err(failure(StatusCode::OK, "OAuth 提供商缓存刷新失败"));
    };
    let mut connection = client
        .get_multiplexed_async_connection()
        .await
        .map_err(|_| failure(StatusCode::OK, "OAuth 提供商缓存刷新失败"))?;
    redis::cmd("DEL")
        .arg("oauth:custom:providers")
        .query_async::<i64>(&mut connection)
        .await
        .map_err(|_| failure(StatusCode::OK, "OAuth 提供商缓存刷新失败"))?;
    redis::cmd("PUBLISH")
        .arg("lmm:oauth:providers:changed")
        .arg("1")
        .query_async::<i64>(&mut connection)
        .await
        .map(|_| ())
        .map_err(|_| failure(StatusCode::OK, "OAuth 提供商缓存刷新失败"))
}
