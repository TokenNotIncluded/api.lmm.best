//! Legacy-compatible io.net deployment administration routes.
//!
//! The listener authenticates before mounting this router and supplies
//! [`DeploymentActor`] as an extension.  The provider is the sole owner of
//! provider I/O and persistence: PostgreSQL stores configuration and durable
//! idempotency records; Valkey supplies bounded request locks and read cache.
//! This boundary deliberately keeps a second HTTP client and its credentials
//! out of the migration route layer.

use std::{fmt, sync::Arc, time::Duration};

use crate::auth::{DashboardAuth, UserAuthPolicyError, enforce_user_auth_view, user_auth_message};
use async_trait::async_trait;
use axum::{
    Json, Router,
    body::Bytes,
    extract::{Extension, Path, RawQuery, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{get, post, put},
};
use secrecy::{ExposeSecret, SecretString};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Row};
use uuid::Uuid;

const ADMIN_ROLE: i64 = 10;
const IONET_HOST: &str = "api.io.solutions";
const IONET_PUBLIC_BASE: &str = "https://api.io.solutions/v1/io-cloud/caas/";
const IONET_ENTERPRISE_BASE: &str = "https://api.io.solutions/enterprise/v1/io-cloud/caas/";
const DEFAULT_IONET_TIMEOUT: Duration = Duration::from_secs(15);
const MAX_IONET_RESPONSE_BYTES: usize = 1_048_576;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";

/// Authenticated actor installed by the shared authentication listener.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct DeploymentActor {
    /// Database user id, used only for auditing and idempotency scope.
    pub user_id: i64,
    /// Legacy role value; 10 and above is an administrator.
    pub role: i64,
}

/// A deployment request after HTTP validation and legacy normalization.
#[derive(Clone, Debug, PartialEq)]
pub struct DeploymentCall {
    /// The selected legacy operation.
    pub operation: DeploymentOperation,
    /// Authenticated actor, never caller-provided JSON.
    pub actor: DeploymentActor,
    /// Validated path id where the route has one.
    pub deployment_id: Option<String>,
    /// Canonical JSON payload or query representation.
    pub input: Value,
    /// Optional client idempotency key for provider-side write serialization.
    pub idempotency_key: Option<String>,
}

/// All operations in the legacy `/api/deployments` route family.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DeploymentOperation {
    Settings,
    TestConnection,
    List,
    Search,
    HardwareTypes,
    Locations,
    AvailableReplicas,
    PriceEstimation,
    CheckName,
    Create,
    Get,
    Logs,
    ListContainers,
    GetContainer,
    Update,
    Rename,
    Extend,
    Delete,
}

/// Provider error translated to the exact legacy JSON envelope.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum DeploymentError {
    /// Feature disabled, no configured key, or no usable provider account.
    NotConfigured,
    /// Upstream rejected the request with a safe display message.
    Rejected(String),
    /// A duplicate write is still in progress; callers must not retry blindly.
    InProgress,
    /// Durable store, cache, or upstream transport failed without a safe detail.
    Unavailable,
}

impl DeploymentError {
    fn message(&self) -> String {
        match self {
            Self::NotConfigured => {
                "io.net model deployment is not enabled or api key missing".to_owned()
            }
            Self::Rejected(message) => message.clone(),
            Self::InProgress => "deployment request is already in progress".to_owned(),
            Self::Unavailable => "deployment operation failed".to_owned(),
        }
    }
}

/// Boundary for io.net plus its PostgreSQL/Valkey coordination.
///
/// Write implementations must store a result keyed by `(actor, operation,
/// idempotency_key)` in PostgreSQL before releasing the Valkey lock.  A second
/// identical request returns that persisted result; a different concurrent
/// key reports [`DeploymentError::InProgress`].  Read implementations may use
/// Valkey only as a cache and must tolerate a cache miss or outage.
#[async_trait]
pub trait DeploymentProvider: Send + Sync {
    /// Executes one normalized operation and returns the legacy `data` value.
    async fn execute(&self, call: DeploymentCall) -> Result<Value, DeploymentError>;
}

/// External boundary for io.net/container jobs.
///
/// Implementations must propagate `call.idempotency_key` to the upstream job
/// runner.  PostgreSQL is authoritative for recovery, while that key prevents
/// a recovered worker from creating a second external deployment if it lost a
/// Valkey lease after the upstream accepted the first request.
#[async_trait]
pub trait DeploymentJobRunner: Send + Sync {
    /// Runs the provider operation after the durable job record is committed.
    async fn run(&self, call: DeploymentCall) -> Result<Value, DeploymentError>;
}

/// An explicit fail-closed runner for test instances and deployments without
/// an io.net integration.  It performs no I/O and, crucially, never returns a
/// fabricated successful result after a PostgreSQL job has been journaled.
#[derive(Clone, Copy, Default)]
pub struct DisabledDeploymentJobRunner;

#[async_trait]
impl DeploymentJobRunner for DisabledDeploymentJobRunner {
    async fn run(&self, _call: DeploymentCall) -> Result<Value, DeploymentError> {
        Err(DeploymentError::NotConfigured)
    }
}

/// Server-owned configuration for the io.net runtime adapter.
///
/// The API key is deliberately [`SecretString`] rather than a request field:
/// browser input must never select credentials or make test instances perform
/// external calls.  The two base URLs are validated against the fixed io.net
/// HTTPS allowlist before any request can be issued.
pub struct IoNetDeploymentJobRunner {
    client: reqwest::Client,
    api_key: SecretString,
    public_base: reqwest::Url,
    enterprise_base: reqwest::Url,
    max_response_bytes: usize,
}

impl fmt::Debug for IoNetDeploymentJobRunner {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("IoNetDeploymentJobRunner")
            .field("public_base", &self.public_base)
            .field("enterprise_base", &self.enterprise_base)
            .field("max_response_bytes", &self.max_response_bytes)
            .finish_non_exhaustive()
    }
}

impl IoNetDeploymentJobRunner {
    /// Builds the production adapter with the shared HTTPS-only outbound
    /// client.  Callers load `api_key` only from server configuration.
    pub fn new(api_key: SecretString) -> Result<Self, DeploymentError> {
        let client = crate::outbound_http::client(DEFAULT_IONET_TIMEOUT)
            .map_err(|_| DeploymentError::Unavailable)?;
        Self::with_client(
            client,
            api_key,
            parse_ionet_base(IONET_PUBLIC_BASE)?,
            parse_ionet_base(IONET_ENTERPRISE_BASE)?,
        )
    }

    /// Dependency-injection constructor for the server composition root and
    /// deterministic adapter tests.  Both supplied URLs remain constrained to
    /// the io.net HTTPS origin; this is not a general-purpose proxy.
    pub fn with_client(
        client: reqwest::Client,
        api_key: SecretString,
        public_base: reqwest::Url,
        enterprise_base: reqwest::Url,
    ) -> Result<Self, DeploymentError> {
        if api_key.expose_secret().trim().is_empty()
            || !is_ionet_base(&public_base)
            || !is_ionet_base(&enterprise_base)
        {
            return Err(DeploymentError::NotConfigured);
        }
        Ok(Self {
            client,
            api_key,
            public_base,
            enterprise_base,
            max_response_bytes: MAX_IONET_RESPONSE_BYTES,
        })
    }

    fn endpoint(
        &self,
        enterprise: bool,
        path: &[&str],
        input: &Value,
    ) -> Result<reqwest::Url, DeploymentError> {
        let mut url = if enterprise {
            self.enterprise_base.clone()
        } else {
            self.public_base.clone()
        };
        {
            let mut segments = url
                .path_segments_mut()
                .map_err(|_| DeploymentError::Unavailable)?;
            for segment in path {
                if segment.trim().is_empty() || segment.contains(['/', '\\']) {
                    return Err(DeploymentError::Rejected(
                        "invalid deployment identifier".to_owned(),
                    ));
                }
                segments.push(segment);
            }
        }
        if let Some(query) = input.as_object() {
            let mut pairs = url.query_pairs_mut();
            for (name, value) in query {
                match value {
                    Value::String(value) if !value.is_empty() => {
                        pairs.append_pair(name, value);
                    }
                    Value::Number(value) => {
                        pairs.append_pair(name, &value.to_string());
                    }
                    Value::Bool(value) => {
                        pairs.append_pair(name, if *value { "true" } else { "false" });
                    }
                    Value::Array(values) if !values.is_empty() => {
                        let encoded = serde_json::to_string(values)
                            .map_err(|_| DeploymentError::Unavailable)?;
                        pairs.append_pair(name, &encoded);
                    }
                    _ => {}
                }
            }
        }
        Ok(url)
    }

    async fn request(
        &self,
        method: reqwest::Method,
        enterprise: bool,
        path: &[&str],
        query: &Value,
        body: Option<&Value>,
        idempotency_key: Option<&str>,
    ) -> Result<Vec<u8>, DeploymentError> {
        let url = self.endpoint(enterprise, path, query)?;
        let mut request = self
            .client
            .request(method, url)
            .header(reqwest::header::ACCEPT, "application/json")
            .header("x-api-key", self.api_key.expose_secret());
        if let Some(body) = body {
            request = request.json(body);
        }
        if let Some(key) = idempotency_key {
            request = request.header("idempotency-key", key);
        }
        let response = request
            .send()
            .await
            .map_err(|_| DeploymentError::Unavailable)?;
        let status = response.status();
        let bytes = read_limited(response, self.max_response_bytes).await?;
        if !status.is_success() {
            return Err(DeploymentError::Rejected(upstream_error_message(
                status, &bytes,
            )));
        }
        Ok(bytes)
    }

    async fn json_request(
        &self,
        method: reqwest::Method,
        enterprise: bool,
        path: &[&str],
        query: &Value,
        body: Option<&Value>,
        idempotency_key: Option<&str>,
    ) -> Result<Value, DeploymentError> {
        let bytes = self
            .request(method, enterprise, path, query, body, idempotency_key)
            .await?;
        serde_json::from_slice(&bytes).map_err(|_| DeploymentError::Unavailable)
    }

    async fn mutation(
        &self,
        operation: DeploymentOperation,
        call: &DeploymentCall,
        path: &[&str],
        body: &Value,
    ) -> Result<Value, DeploymentError> {
        let method = match operation {
            DeploymentOperation::Create | DeploymentOperation::Extend => reqwest::Method::POST,
            DeploymentOperation::Update => reqwest::Method::PATCH,
            DeploymentOperation::Rename => reqwest::Method::PUT,
            DeploymentOperation::Delete => reqwest::Method::DELETE,
            _ => return Err(DeploymentError::Unavailable),
        };
        let response = self
            .json_request(
                method,
                true,
                path,
                &json!({}),
                (!matches!(operation, DeploymentOperation::Delete)).then_some(body),
                call.idempotency_key.as_deref(),
            )
            .await?;
        exact_mutation_response(&response)
    }
}

#[async_trait]
impl DeploymentJobRunner for IoNetDeploymentJobRunner {
    async fn run(&self, call: DeploymentCall) -> Result<Value, DeploymentError> {
        let id = call.deployment_id.as_deref();
        match call.operation {
            DeploymentOperation::Settings => Ok(json!({
                "provider": "io.net", "enabled": true, "configured": true, "can_connect": true,
            })),
            DeploymentOperation::TestConnection => {
                let response = self
                    .json_request(
                        reqwest::Method::GET,
                        true,
                        &["hardware", "max-gpus-per-container"],
                        &json!({}),
                        None,
                        None,
                    )
                    .await?;
                let data = response_data(response);
                let hardware = data
                    .get("hardware")
                    .and_then(Value::as_array)
                    .ok_or(DeploymentError::Unavailable)?;
                let total = data
                    .get("total")
                    .and_then(Value::as_i64)
                    .unwrap_or_else(|| {
                        hardware
                            .iter()
                            .filter_map(|item| item.get("available")?.as_i64())
                            .sum()
                    });
                Ok(json!({"hardware_count": hardware.len(), "total_available": total}))
            }
            DeploymentOperation::List | DeploymentOperation::Search => {
                let mut query = call.input.clone();
                if let Some(query) = query.as_object_mut() {
                    // Search is legacy local filtering over the normal list;
                    // forwarding it would alter the provider request contract.
                    query.remove("keyword");
                }
                let response = self
                    .json_request(
                        reqwest::Method::GET,
                        true,
                        &["deployments"],
                        &query,
                        None,
                        None,
                    )
                    .await?;
                let mut data = response_data(response);
                if call.operation == DeploymentOperation::Search {
                    filter_deployments(
                        &mut data,
                        call.input.get("keyword").and_then(Value::as_str),
                    );
                }
                Ok(data)
            }
            DeploymentOperation::HardwareTypes => {
                let response = self
                    .json_request(
                        reqwest::Method::GET,
                        true,
                        &["hardware", "max-gpus-per-container"],
                        &json!({}),
                        None,
                        None,
                    )
                    .await?;
                Ok(response_data(response))
            }
            DeploymentOperation::Locations => Ok(response_data(
                self.json_request(
                    reqwest::Method::GET,
                    false,
                    &["locations"],
                    &json!({}),
                    None,
                    None,
                )
                .await?,
            )),
            DeploymentOperation::AvailableReplicas => {
                let mut query = call.input.clone();
                if let Some(query) = query.as_object_mut()
                    && let Some(gpu_count) = query.remove("gpu_count") {
                        query.insert("hardware_qty".to_owned(), gpu_count);
                    }
                Ok(response_data(
                    self.json_request(
                        reqwest::Method::GET,
                        true,
                        &["available-replicas"],
                        &query,
                        None,
                        None,
                    )
                    .await?,
                ))
            }
            DeploymentOperation::PriceEstimation => Ok(response_data(
                self.json_request(
                    reqwest::Method::GET,
                    true,
                    &["price"],
                    &call.input,
                    None,
                    None,
                )
                .await?,
            )),
            DeploymentOperation::CheckName => {
                let name = call
                    .input
                    .get("name")
                    .and_then(Value::as_str)
                    .ok_or_else(|| {
                        DeploymentError::Rejected("name parameter is required".to_owned())
                    })?;
                let response = self
                    .json_request(
                        reqwest::Method::GET,
                        true,
                        &["clusters", "check_cluster_name_availability"],
                        &json!({"cluster_name": name}),
                        None,
                        None,
                    )
                    .await?;
                let available = response
                    .as_bool()
                    .or_else(|| response.get("data").and_then(Value::as_bool))
                    .ok_or(DeploymentError::Unavailable)?;
                Ok(json!({"available": available, "name": name}))
            }
            DeploymentOperation::Create => self.create(&call).await,
            DeploymentOperation::Get => Ok(response_data(
                self.json_request(
                    reqwest::Method::GET,
                    true,
                    &["deployment", required_id(id)?],
                    &json!({}),
                    None,
                    None,
                )
                .await?,
            )),
            DeploymentOperation::Logs => {
                let container = call
                    .input
                    .get("container_id")
                    .and_then(Value::as_str)
                    .ok_or_else(|| {
                        DeploymentError::Rejected("container_id parameter is required".to_owned())
                    })?;
                let mut query = call.input.clone();
                if let Some(query) = query.as_object_mut() {
                    query.remove("container_id");
                }
                let bytes = self
                    .request(
                        reqwest::Method::GET,
                        false,
                        &["deployment", required_id(id)?, "log", container],
                        &query,
                        None,
                        None,
                    )
                    .await?;
                String::from_utf8(bytes)
                    .map(Value::String)
                    .map_err(|_| DeploymentError::Unavailable)
            }
            DeploymentOperation::ListContainers => Ok(response_data(
                self.json_request(
                    reqwest::Method::GET,
                    true,
                    &["deployment", required_id(id)?, "containers"],
                    &json!({}),
                    None,
                    None,
                )
                .await?,
            )),
            DeploymentOperation::GetContainer => {
                let container = call
                    .input
                    .get("container_id")
                    .and_then(Value::as_str)
                    .ok_or_else(|| {
                        DeploymentError::Rejected("container ID is required".to_owned())
                    })?;
                Ok(response_data(
                    self.json_request(
                        reqwest::Method::GET,
                        true,
                        &["deployment", required_id(id)?, "container", container],
                        &json!({}),
                        None,
                        None,
                    )
                    .await?,
                ))
            }
            DeploymentOperation::Update => {
                self.mutation(
                    DeploymentOperation::Update,
                    &call,
                    &["deployment", required_id(id)?],
                    &call.input,
                )
                .await
            }
            DeploymentOperation::Rename => {
                let name = call
                    .input
                    .get("name")
                    .and_then(Value::as_str)
                    .ok_or_else(|| {
                        DeploymentError::Rejected("deployment name cannot be empty".to_owned())
                    })?;
                self.rename(&call, required_id(id)?, name).await
            }
            DeploymentOperation::Extend => self.extend(&call, required_id(id)?).await,
            DeploymentOperation::Delete => {
                self.mutation(
                    DeploymentOperation::Delete,
                    &call,
                    &["deployment", required_id(id)?],
                    &json!({}),
                )
                .await
            }
        }
    }
}

impl IoNetDeploymentJobRunner {
    async fn create(&self, call: &DeploymentCall) -> Result<Value, DeploymentError> {
        let response = self
            .mutation(DeploymentOperation::Create, call, &["deploy"], &call.input)
            .await?;
        Ok(json!({
            "deployment_id": response["deployment_id"],
            "status": response["status"],
            "message": "Deployment created successfully",
        }))
    }

    async fn rename(
        &self,
        call: &DeploymentCall,
        deployment_id: &str,
        name: &str,
    ) -> Result<Value, DeploymentError> {
        let response = self
            .json_request(
                reqwest::Method::PUT,
                true,
                &["clusters", deployment_id, "update-name"],
                &json!({}),
                Some(&json!({"cluster_name": name})),
                call.idempotency_key.as_deref(),
            )
            .await?;
        let object = response.as_object().ok_or(DeploymentError::Unavailable)?;
        let status = object
            .get("status")
            .and_then(Value::as_str)
            .ok_or(DeploymentError::Unavailable)?;
        let message = object
            .get("message")
            .and_then(Value::as_str)
            .unwrap_or_default();
        Ok(json!({"status": status, "message": message, "id": deployment_id, "name": name}))
    }

    async fn extend(
        &self,
        call: &DeploymentCall,
        deployment_id: &str,
    ) -> Result<Value, DeploymentError> {
        let response = self
            .json_request(
                reqwest::Method::POST,
                true,
                &["deployment", deployment_id, "extend"],
                &json!({}),
                Some(&call.input),
                call.idempotency_key.as_deref(),
            )
            .await?;
        let detail = response_data(response);
        let object = detail.as_object().ok_or(DeploymentError::Unavailable)?;
        if object.get("id").and_then(Value::as_str).is_none()
            || object.get("status").and_then(Value::as_str).is_none()
        {
            return Err(DeploymentError::Unavailable);
        }
        Ok(detail)
    }
}

fn parse_ionet_base(value: &str) -> Result<reqwest::Url, DeploymentError> {
    let url = reqwest::Url::parse(value).map_err(|_| DeploymentError::NotConfigured)?;
    is_ionet_base(&url)
        .then_some(url)
        .ok_or(DeploymentError::NotConfigured)
}

fn is_ionet_base(url: &reqwest::Url) -> bool {
    url.scheme() == "https"
        && url.host_str() == Some(IONET_HOST)
        && url.port_or_known_default() == Some(443)
        && url.username().is_empty()
        && url.password().is_none()
        && url.path().ends_with('/')
}

async fn read_limited(
    mut response: reqwest::Response,
    limit: usize,
) -> Result<Vec<u8>, DeploymentError> {
    if response
        .content_length()
        .is_some_and(|length| length > limit as u64)
    {
        return Err(DeploymentError::Unavailable);
    }
    let mut body = Vec::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| DeploymentError::Unavailable)?
    {
        if body.len().saturating_add(chunk.len()) > limit {
            return Err(DeploymentError::Unavailable);
        }
        body.extend_from_slice(&chunk);
    }
    Ok(body)
}

fn response_data(value: Value) -> Value {
    value.get("data").cloned().unwrap_or(value)
}

fn exact_mutation_response(value: &Value) -> Result<Value, DeploymentError> {
    let object = value.as_object().ok_or(DeploymentError::Unavailable)?;
    let deployment_id = object
        .get("deployment_id")
        .and_then(Value::as_str)
        .filter(|id| !id.trim().is_empty())
        .ok_or(DeploymentError::Unavailable)?;
    let status = object
        .get("status")
        .and_then(Value::as_str)
        .ok_or(DeploymentError::Unavailable)?;
    let mut output = serde_json::Map::new();
    output.insert(
        "deployment_id".to_owned(),
        Value::String(deployment_id.to_owned()),
    );
    output.insert("status".to_owned(), Value::String(status.to_owned()));
    if let Some(message) = object.get("message").and_then(Value::as_str) {
        output.insert("message".to_owned(), Value::String(message.to_owned()));
    }
    Ok(Value::Object(output))
}

fn upstream_error_message(status: reqwest::StatusCode, bytes: &[u8]) -> String {
    let detail = serde_json::from_slice::<Value>(bytes)
        .ok()
        .and_then(|value| {
            value
                .get("detail")
                .or_else(|| value.get("message"))
                .and_then(Value::as_str)
                .map(str::trim)
                .filter(|message| !message.is_empty())
                .map(ToOwned::to_owned)
        });
    detail
        .map(|message| message.chars().take(512).collect())
        .unwrap_or_else(|| format!("io.net request failed with status {}", status.as_u16()))
}

fn required_id(id: Option<&str>) -> Result<&str, DeploymentError> {
    id.filter(|id| !id.trim().is_empty())
        .ok_or_else(|| DeploymentError::Rejected("deployment ID is required".to_owned()))
}

fn filter_deployments(data: &mut Value, keyword: Option<&str>) {
    let Some(keyword) = keyword.map(str::trim).filter(|value| !value.is_empty()) else {
        return;
    };
    let Some(object) = data.as_object_mut() else {
        return;
    };
    let key = if object.contains_key("deployments") {
        "deployments"
    } else {
        "items"
    };
    let Some(items) = object.get_mut(key).and_then(Value::as_array_mut) else {
        return;
    };
    let keyword = keyword.to_ascii_lowercase();
    items.retain(|item| {
        item.get("name")
            .or_else(|| item.get("deployment_name"))
            .and_then(Value::as_str)
            .is_some_and(|name| name.to_ascii_lowercase().contains(&keyword))
    });
    let total = items.len();
    let _ = items;
    object.insert("total".to_owned(), json!(total));
}

const DEPLOYMENT_LEASE_PREFIX: &str = "lmm:deployment:v1:lease";
const DEFAULT_LEASE_TTL: Duration = Duration::from_secs(30);

/// PostgreSQL/Valkey adapter for deployment writes.
///
/// The migration runner creates `deployment_request_journal` and
/// `deployment_jobs`; this adapter never creates or mutates schema at request
/// time.  The journal makes completed results and retry state durable, and a
/// token-bound Valkey lease serializes a live writer for one actor/resource.
#[derive(Clone)]
pub struct PgValkeyDeploymentProvider {
    pg: PgPool,
    valkey: redis::Client,
    runner: Arc<dyn DeploymentJobRunner>,
    lease_ttl: Duration,
}

impl PgValkeyDeploymentProvider {
    #[must_use]
    pub fn new(pg: PgPool, valkey: redis::Client, runner: Arc<dyn DeploymentJobRunner>) -> Self {
        Self {
            pg,
            valkey,
            runner,
            lease_ttl: DEFAULT_LEASE_TTL,
        }
    }

    #[must_use]
    pub fn with_lease_ttl(mut self, lease_ttl: Duration) -> Self {
        self.lease_ttl = lease_ttl.max(Duration::from_secs(1));
        self
    }

    async fn execute_write(&self, call: DeploymentCall) -> Result<Value, DeploymentError> {
        let Some(idempotency_key) = call.idempotency_key.clone() else {
            // Legacy clients that do not send a key retain their original
            // provider semantics.  New callers must send Idempotency-Key for
            // crash-safe replay; no synthetic key can distinguish a retry
            // from a deliberate identical deployment.
            return self.runner.run(call).await;
        };
        let request_hash = request_hash(&call)?;
        let lease =
            DeploymentLease::acquire(&self.valkey, lease_key(&call), self.lease_ttl).await?;
        let result = self
            .execute_durable_write(call, &idempotency_key, &request_hash)
            .await;
        lease.release().await;
        result
    }

    async fn execute_durable_write(
        &self,
        call: DeploymentCall,
        idempotency_key: &str,
        request_hash: &str,
    ) -> Result<Value, DeploymentError> {
        let operation = operation_name(call.operation);
        let mut transaction = self
            .pg
            .begin()
            .await
            .map_err(|_| DeploymentError::Unavailable)?;
        let existing = sqlx::query(
            "SELECT request_hash, state, result FROM deployment_request_journal \
             WHERE actor_id = $1 AND operation = $2 AND idempotency_key = $3 FOR UPDATE",
        )
        .bind(call.actor.user_id)
        .bind(operation)
        .bind(idempotency_key)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(|_| DeploymentError::Unavailable)?;
        if let Some(existing) = existing {
            let stored_hash: String = existing
                .try_get("request_hash")
                .map_err(|_| DeploymentError::Unavailable)?;
            if stored_hash != request_hash {
                return Err(DeploymentError::Rejected(
                    "idempotency key was already used with a different deployment request"
                        .to_owned(),
                ));
            }
            let state: String = existing
                .try_get("state")
                .map_err(|_| DeploymentError::Unavailable)?;
            if state == "completed" {
                return existing
                    .try_get("result")
                    .map_err(|_| DeploymentError::Unavailable);
            }
            // Acquiring the token-bound lease proves no live writer owns this
            // resource.  A `running` journal record therefore belongs to a
            // crashed/expired worker and is safe to resume with the same
            // upstream idempotency key.  Concurrent callers never reach this
            // branch because their SET NX lease acquisition fails.
            sqlx::query(
                "UPDATE deployment_request_journal \
                 SET state = 'running', attempts = attempts + 1, updated_at = CURRENT_TIMESTAMP \
                 WHERE actor_id = $1 AND operation = $2 AND idempotency_key = $3",
            )
            .bind(call.actor.user_id)
            .bind(operation)
            .bind(idempotency_key)
            .execute(&mut *transaction)
            .await
            .map_err(|_| DeploymentError::Unavailable)?;
        } else {
            sqlx::query(
                "INSERT INTO deployment_request_journal \
                 (actor_id, operation, idempotency_key, request_hash, state, attempts, updated_at) \
                 VALUES ($1, $2, $3, $4, 'running', 1, CURRENT_TIMESTAMP)",
            )
            .bind(call.actor.user_id)
            .bind(operation)
            .bind(idempotency_key)
            .bind(request_hash)
            .execute(&mut *transaction)
            .await
            .map_err(|_| DeploymentError::Unavailable)?;
        }
        sqlx::query(
            "INSERT INTO deployment_jobs (actor_id, operation, idempotency_key, state, attempts, updated_at) \
             VALUES ($1, $2, $3, 'running', 1, CURRENT_TIMESTAMP) \
             ON CONFLICT (actor_id, operation, idempotency_key) DO UPDATE \
             SET state = 'running', attempts = deployment_jobs.attempts + 1, updated_at = CURRENT_TIMESTAMP",
        )
        .bind(call.actor.user_id)
        .bind(operation)
        .bind(idempotency_key)
        .execute(&mut *transaction)
        .await
        .map_err(|_| DeploymentError::Unavailable)?;
        transaction
            .commit()
            .await
            .map_err(|_| DeploymentError::Unavailable)?;

        match self.runner.run(call.clone()).await {
            Ok(result) => {
                self.finish_job(&call, idempotency_key, "completed", Some(&result))
                    .await?;
                Ok(result)
            }
            Err(error) => {
                self.finish_job(&call, idempotency_key, "failed", None)
                    .await?;
                Err(error)
            }
        }
    }

    async fn finish_job(
        &self,
        call: &DeploymentCall,
        idempotency_key: &str,
        state: &str,
        result: Option<&Value>,
    ) -> Result<(), DeploymentError> {
        let operation = operation_name(call.operation);
        let mut transaction = self
            .pg
            .begin()
            .await
            .map_err(|_| DeploymentError::Unavailable)?;
        sqlx::query(
            "UPDATE deployment_request_journal \
             SET state = $1, result = $2, updated_at = CURRENT_TIMESTAMP \
             WHERE actor_id = $3 AND operation = $4 AND idempotency_key = $5",
        )
        .bind(state)
        .bind(result)
        .bind(call.actor.user_id)
        .bind(operation)
        .bind(idempotency_key)
        .execute(&mut *transaction)
        .await
        .map_err(|_| DeploymentError::Unavailable)?;
        sqlx::query(
            "UPDATE deployment_jobs SET state = $1, updated_at = CURRENT_TIMESTAMP \
             WHERE actor_id = $2 AND operation = $3 AND idempotency_key = $4",
        )
        .bind(state)
        .bind(call.actor.user_id)
        .bind(operation)
        .bind(idempotency_key)
        .execute(&mut *transaction)
        .await
        .map_err(|_| DeploymentError::Unavailable)?;
        transaction
            .commit()
            .await
            .map_err(|_| DeploymentError::Unavailable)
    }
}

#[async_trait]
impl DeploymentProvider for PgValkeyDeploymentProvider {
    async fn execute(&self, call: DeploymentCall) -> Result<Value, DeploymentError> {
        if is_write(call.operation) {
            self.execute_write(call).await
        } else {
            self.runner.run(call).await
        }
    }
}

struct DeploymentLease {
    client: redis::Client,
    key: String,
    token: String,
}

impl DeploymentLease {
    async fn acquire(
        client: &redis::Client,
        key: String,
        ttl: Duration,
    ) -> Result<Self, DeploymentError> {
        let token = Uuid::new_v4().to_string();
        let milliseconds = i64::try_from(ttl.as_millis()).unwrap_or(i64::MAX);
        let mut connection = client
            .get_multiplexed_async_connection()
            .await
            .map_err(|_| DeploymentError::Unavailable)?;
        let acquired: Option<String> = redis::cmd("SET")
            .arg(&key)
            .arg(&token)
            .arg("NX")
            .arg("PX")
            .arg(milliseconds)
            .query_async(&mut connection)
            .await
            .map_err(|_| DeploymentError::Unavailable)?;
        acquired
            .is_some()
            .then_some(Self {
                client: client.clone(),
                key,
                token,
            })
            .ok_or(DeploymentError::InProgress)
    }

    async fn release(self) {
        let Ok(mut connection) = self.client.get_multiplexed_async_connection().await else {
            return;
        };
        let _: Result<i64, _> = redis::Script::new(
            "if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end return 0",
        )
        .key(self.key)
        .arg(self.token)
        .invoke_async(&mut connection)
        .await;
    }
}

fn is_write(operation: DeploymentOperation) -> bool {
    matches!(
        operation,
        DeploymentOperation::Create
            | DeploymentOperation::Update
            | DeploymentOperation::Rename
            | DeploymentOperation::Extend
            | DeploymentOperation::Delete
    )
}

fn operation_name(operation: DeploymentOperation) -> &'static str {
    match operation {
        DeploymentOperation::Settings => "settings",
        DeploymentOperation::TestConnection => "test_connection",
        DeploymentOperation::List => "list",
        DeploymentOperation::Search => "search",
        DeploymentOperation::HardwareTypes => "hardware_types",
        DeploymentOperation::Locations => "locations",
        DeploymentOperation::AvailableReplicas => "available_replicas",
        DeploymentOperation::PriceEstimation => "price_estimation",
        DeploymentOperation::CheckName => "check_name",
        DeploymentOperation::Create => "create",
        DeploymentOperation::Get => "get",
        DeploymentOperation::Logs => "logs",
        DeploymentOperation::ListContainers => "list_containers",
        DeploymentOperation::GetContainer => "get_container",
        DeploymentOperation::Update => "update",
        DeploymentOperation::Rename => "rename",
        DeploymentOperation::Extend => "extend",
        DeploymentOperation::Delete => "delete",
    }
}

fn lease_key(call: &DeploymentCall) -> String {
    let resource = call.deployment_id.as_deref().unwrap_or("new-deployment");
    format!(
        "{DEPLOYMENT_LEASE_PREFIX}:{}:{resource}",
        call.actor.user_id
    )
}

fn request_hash(call: &DeploymentCall) -> Result<String, DeploymentError> {
    let input = serde_json::to_vec(&call.input).map_err(|_| DeploymentError::Unavailable)?;
    let mut hasher = Sha256::new();
    hasher.update(operation_name(call.operation));
    hasher.update([0]);
    hasher.update(call.actor.user_id.to_be_bytes());
    hasher.update([0]);
    if let Some(deployment_id) = &call.deployment_id {
        hasher.update(deployment_id.as_bytes());
    }
    hasher.update([0]);
    hasher.update(input);
    Ok(hex::encode(hasher.finalize()))
}

#[derive(Clone)]
pub struct DeploymentState {
    provider: Arc<dyn DeploymentProvider>,
    dashboard_auth: Option<Arc<dyn DashboardAuth>>,
}

impl DeploymentState {
    #[must_use]
    pub fn new(provider: Arc<dyn DeploymentProvider>) -> Self {
        Self {
            provider,
            dashboard_auth: None,
        }
    }

    /// Installs the shared dashboard-auth authority used by the normal
    /// listener.  Candidate/test routers may omit it and continue to rely on
    /// an explicitly injected [`DeploymentActor`], but a production mount must
    /// never depend on a caller-provided extension.
    #[must_use]
    pub fn with_dashboard_auth(mut self, auth: Arc<dyn DashboardAuth>) -> Self {
        self.dashboard_auth = Some(auth);
        self
    }
}

/// The complete legacy administrator-only deployment API surface.
pub fn router(state: DeploymentState) -> Router {
    Router::new()
        .route("/api/deployments/", get(list).post(create))
        .route("/api/deployments/settings", get(settings))
        .route(
            "/api/deployments/settings/test-connection",
            post(test_connection),
        )
        .route("/api/deployments/test-connection", post(test_connection))
        .route("/api/deployments/search", get(search))
        .route("/api/deployments/hardware-types", get(hardware_types))
        .route("/api/deployments/locations", get(locations))
        .route(
            "/api/deployments/available-replicas",
            get(available_replicas),
        )
        .route("/api/deployments/price-estimation", post(price_estimation))
        .route("/api/deployments/check-name", get(check_name))
        .route(
            "/api/deployments/{id}",
            get(get_deployment).put(update).delete(remove),
        )
        .route("/api/deployments/{id}/logs", get(logs))
        .route("/api/deployments/{id}/containers", get(containers))
        .route(
            "/api/deployments/{id}/containers/{container_id}",
            get(container),
        )
        .route("/api/deployments/{id}/name", put(rename))
        .route("/api/deployments/{id}/extend", post(extend))
        // The legacy admin group authenticates before binding JSON or query
        // parameters. Keep that ordering here so malformed input cannot turn
        // an unauthorized request into an extractor-generated 400/422.
        .route_layer(middleware::from_fn_with_state(
            state.clone(),
            deployment_admin_guard,
        ))
        .with_state(state)
}

#[derive(Serialize)]
struct Success {
    success: bool,
    message: &'static str,
    data: Value,
}

#[derive(Serialize)]
struct Failure {
    success: bool,
    message: String,
}

fn failure(message: impl Into<String>) -> Response {
    Json(Failure {
        success: false,
        message: message.into(),
    })
    .into_response()
}

fn require_admin(actor: Option<Extension<DeploymentActor>>) -> Option<DeploymentActor> {
    actor
        .map(|Extension(actor)| actor)
        .filter(|actor| actor.user_id > 0 && actor.role >= ADMIN_ROLE)
}

#[allow(dead_code)]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum DeploymentAuthRejection {
    /// The Go `ConsoleAccessGate` conceals deployment discovery routes from
    /// anonymous, invalid, disabled, and unactivated credentials.
    ConsoleNotFound,
    Unauthorized {
        supplied: bool,
    },
    TokenExpired,
    SessionRevoked,
    UserDisabled,
    InvalidUser,
    InsufficientPrivilege,
    Internal,
}

async fn deployment_admin_guard(
    State(state): State<DeploymentState>,
    mut request: Request,
    next: Next,
) -> Response {
    let Some(auth) = state.dashboard_auth.as_ref() else {
        match request.extensions().get::<DeploymentActor>().copied() {
            Some(actor) if actor.user_id > 0 && actor.role >= ADMIN_ROLE => {
                return next.run(request).await;
            }
            Some(actor) if actor.user_id > 0 => {
                return deployment_auth_rejection(
                    request.headers(),
                    DeploymentAuthRejection::InsufficientPrivilege,
                );
            }
            _ => {
                return deployment_auth_rejection(
                    request.headers(),
                    DeploymentAuthRejection::Unauthorized { supplied: false },
                );
            }
        }
    };

    let Some(token) = dashboard_credential(request.headers()) else {
        return deployment_auth_rejection(
            request.headers(),
            DeploymentAuthRejection::ConsoleNotFound,
        );
    };
    // The normal Go listener runs ConsoleAccessGate before AdminAuth. Resolve
    // the optional view first so relay-console inventory remains a generic
    // 404 until the account has crossed the live activation/trust boundary.
    let user = match auth
        .self_user_view_for_optional(SecretString::from(token))
        .await
    {
        Ok(user) => user,
        Err(_) => {
            return deployment_auth_rejection(
                request.headers(),
                DeploymentAuthRejection::ConsoleNotFound,
            );
        }
    };
    if user.id <= 0 || user.status != 1 || !user.developer_access_granted {
        return deployment_auth_rejection(
            request.headers(),
            DeploymentAuthRejection::ConsoleNotFound,
        );
    }
    if let Err(policy) = enforce_user_auth_view(&user) {
        let rejection = match policy {
            UserAuthPolicyError::UserDisabled => DeploymentAuthRejection::UserDisabled,
            UserAuthPolicyError::InsufficientPrivilege => {
                DeploymentAuthRejection::InsufficientPrivilege
            }
            UserAuthPolicyError::InvalidUserInfo => DeploymentAuthRejection::InvalidUser,
        };
        return deployment_auth_rejection(request.headers(), rejection);
    }
    if user.role < ADMIN_ROLE {
        return deployment_auth_rejection(
            request.headers(),
            DeploymentAuthRejection::InsufficientPrivilege,
        );
    }

    request.extensions_mut().insert(DeploymentActor {
        user_id: user.id,
        role: user.role,
    });
    next.run(request).await
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

fn token_locale(headers: &HeaderMap) -> (bool, bool) {
    let language = headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default()
        .to_ascii_lowercase();
    (language.starts_with("zh-tw"), language.starts_with("zh"))
}

fn invalid_access_token(headers: &HeaderMap) -> &'static str {
    match token_locale(headers) {
        (true, _) => "無權進行此操作，access token 無效",
        (_, true) => "无权进行此操作，access token 无效",
        _ => "Unauthorized, invalid access token",
    }
}

fn deployment_policy_message(
    headers: &HeaderMap,
    rejection: DeploymentAuthRejection,
) -> &'static str {
    let policy = match rejection {
        DeploymentAuthRejection::UserDisabled => UserAuthPolicyError::UserDisabled,
        DeploymentAuthRejection::InvalidUser => UserAuthPolicyError::InvalidUserInfo,
        DeploymentAuthRejection::InsufficientPrivilege => {
            UserAuthPolicyError::InsufficientPrivilege
        }
        _ => return invalid_access_token(headers),
    };
    user_auth_message(
        policy,
        headers
            .get(header::ACCEPT_LANGUAGE)
            .and_then(|value| value.to_str().ok()),
    )
}

fn deployment_auth_rejection(headers: &HeaderMap, rejection: DeploymentAuthRejection) -> Response {
    if matches!(rejection, DeploymentAuthRejection::ConsoleNotFound) {
        let mut response =
            (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response();
        response.headers_mut().insert(
            header::CONTENT_TYPE,
            HeaderValue::from_static("application/json; charset=utf-8"),
        );
        return response;
    }
    let (status, code, message) = match rejection {
        DeploymentAuthRejection::ConsoleNotFound => {
            unreachable!("console-not-found is returned before legacy auth mapping")
        }
        DeploymentAuthRejection::Unauthorized { .. } => (
            StatusCode::UNAUTHORIZED,
            "AUTH_UNAUTHORIZED",
            invalid_access_token(headers),
        ),
        DeploymentAuthRejection::TokenExpired => (
            StatusCode::UNAUTHORIZED,
            "AUTH_TOKEN_EXPIRED",
            invalid_access_token(headers),
        ),
        DeploymentAuthRejection::SessionRevoked => (
            StatusCode::UNAUTHORIZED,
            "AUTH_SESSION_REVOKED",
            invalid_access_token(headers),
        ),
        DeploymentAuthRejection::UserDisabled => (
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_DISABLED",
            deployment_policy_message(headers, rejection),
        ),
        DeploymentAuthRejection::InvalidUser => (
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_INVALID",
            deployment_policy_message(headers, rejection),
        ),
        DeploymentAuthRejection::InsufficientPrivilege => (
            StatusCode::FORBIDDEN,
            "AUTH_INSUFFICIENT_PRIVILEGE",
            deployment_policy_message(headers, rejection),
        ),
        DeploymentAuthRejection::Internal => (
            StatusCode::INTERNAL_SERVER_ERROR,
            "AUTH_INTERNAL_ERROR",
            "Database error, please contact the administrator",
        ),
    };
    let mut response = (
        status,
        Json(json!({"success": false, "code": code, "message": message})),
    )
        .into_response();
    if status.is_success() {
        response.headers_mut().insert(
            header::HeaderName::from_static("auth-version"),
            axum::http::HeaderValue::from_static(AUTH_VERSION),
        );
    }
    response
}

async fn run(
    state: DeploymentState,
    actor: Option<Extension<DeploymentActor>>,
    operation: DeploymentOperation,
    deployment_id: Option<String>,
    input: Value,
    idempotency_key: Option<String>,
) -> Response {
    let Some(actor) = require_admin(actor) else {
        return failure("无权进行此操作");
    };
    match state
        .provider
        .execute(DeploymentCall {
            operation,
            actor,
            deployment_id,
            input,
            idempotency_key,
        })
        .await
    {
        Ok(data) => Json(Success {
            success: true,
            message: "",
            data,
        })
        .into_response(),
        Err(error) => failure(error.message()),
    }
}

fn id(value: String) -> Option<String> {
    let value = value.trim().to_owned();
    (!value.is_empty()).then_some(value)
}

fn key(value: Option<String>) -> Option<String> {
    value.and_then(|value| (!value.trim().is_empty()).then(|| value.trim().to_owned()))
}

fn header_key(headers: &HeaderMap) -> Option<String> {
    headers
        .get("idempotency-key")
        .and_then(|value| value.to_str().ok())
        .map(str::to_owned)
        .and_then(|value| key(Some(value)))
}

#[derive(Deserialize)]
struct AnyQuery {
    #[serde(flatten)]
    values: serde_json::Map<String, Value>,
}

impl AnyQuery {
    fn value(self) -> Value {
        Value::Object(self.values)
    }
}

fn query(raw: Option<&str>) -> Result<AnyQuery, ()> {
    let mut values = serde_json::Map::new();
    for part in raw
        .unwrap_or_default()
        .split('&')
        .filter(|part| !part.is_empty())
    {
        let (key, value) = part.split_once('=').unwrap_or((part, ""));
        values.insert(
            decode_query_component(key).ok_or(())?,
            Value::String(decode_query_component(value).ok_or(())?),
        );
    }
    Ok(AnyQuery { values })
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

fn json_body<T: serde::de::DeserializeOwned>(body: &[u8]) -> Result<T, ()> {
    serde_json::from_slice(body).map_err(|_| ())
}

async fn settings(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
) -> Response {
    run(s, a, DeploymentOperation::Settings, None, json!({}), None).await
}
async fn list(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
    RawQuery(raw): RawQuery,
) -> Response {
    let Ok(q) = query(raw.as_deref()) else {
        return failure("invalid query parameters");
    };
    run(s, a, DeploymentOperation::List, None, q.value(), None).await
}
async fn search(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
    RawQuery(raw): RawQuery,
) -> Response {
    let Ok(q) = query(raw.as_deref()) else {
        return failure("invalid query parameters");
    };
    run(s, a, DeploymentOperation::Search, None, q.value(), None).await
}
async fn hardware_types(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
) -> Response {
    run(
        s,
        a,
        DeploymentOperation::HardwareTypes,
        None,
        json!({}),
        None,
    )
    .await
}
async fn locations(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
) -> Response {
    run(s, a, DeploymentOperation::Locations, None, json!({}), None).await
}

async fn available_replicas(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
    RawQuery(raw): RawQuery,
) -> Response {
    let Ok(q) = query(raw.as_deref()) else {
        return failure("invalid query parameters");
    };
    let Some(hardware) = q.values.get("hardware_id").and_then(Value::as_str) else {
        return failure("hardware_id parameter is required");
    };
    if hardware
        .parse::<i64>()
        .ok()
        .filter(|value| *value > 0)
        .is_none()
    {
        return failure("invalid hardware_id parameter");
    }
    run(
        s,
        a,
        DeploymentOperation::AvailableReplicas,
        None,
        AnyQuery { values: q.values }.value(),
        None,
    )
    .await
}

async fn check_name(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
    RawQuery(raw): RawQuery,
) -> Response {
    let Ok(mut q) = query(raw.as_deref()) else {
        return failure("invalid query parameters");
    };
    let Some(name) = q.values.get("name").and_then(Value::as_str) else {
        return failure("name parameter is required");
    };
    let name = name.trim();
    if name.is_empty() {
        return failure("name parameter is required");
    }
    q.values
        .insert("name".to_owned(), Value::String(name.to_owned()));
    run(s, a, DeploymentOperation::CheckName, None, q.value(), None).await
}

async fn get_deployment(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
    Path(raw): Path<String>,
) -> Response {
    match id(raw) {
        Some(id) => run(s, a, DeploymentOperation::Get, Some(id), json!({}), None).await,
        None => failure("deployment ID is required"),
    }
}
async fn logs(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
    Path(raw): Path<String>,
    RawQuery(query): RawQuery,
) -> Response {
    match id(raw) {
        Some(id) => {
            let Ok(q) = self::query(query.as_deref()) else {
                return failure("invalid query parameters");
            };
            if q.values
                .get("container_id")
                .and_then(Value::as_str)
                .is_none_or(str::is_empty)
            {
                return failure("container_id parameter is required");
            }
            run(s, a, DeploymentOperation::Logs, Some(id), q.value(), None).await
        }
        None => failure("deployment ID is required"),
    }
}
async fn containers(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
    Path(raw): Path<String>,
) -> Response {
    match id(raw) {
        Some(id) => {
            run(
                s,
                a,
                DeploymentOperation::ListContainers,
                Some(id),
                json!({}),
                None,
            )
            .await
        }
        None => failure("deployment ID is required"),
    }
}
async fn container(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
    Path((raw, container_id)): Path<(String, String)>,
) -> Response {
    match (
        id(raw),
        (!container_id.trim().is_empty()).then_some(container_id.trim().to_owned()),
    ) {
        (Some(id), Some(container_id)) => {
            run(
                s,
                a,
                DeploymentOperation::GetContainer,
                Some(id),
                json!({"container_id": container_id}),
                None,
            )
            .await
        }
        (None, _) => failure("deployment ID is required"),
        (_, None) => failure("container ID is required"),
    }
}

async fn create(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    let input = match json_body(&body) {
        Ok(input) => input,
        Err(()) => return failure("invalid request payload"),
    };
    run(
        s,
        a,
        DeploymentOperation::Create,
        None,
        input,
        header_key(&headers),
    )
    .await
}
async fn price_estimation(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
    body: Bytes,
) -> Response {
    let input = match json_body(&body) {
        Ok(input) => input,
        Err(()) => return failure("invalid request payload"),
    };
    run(
        s,
        a,
        DeploymentOperation::PriceEstimation,
        None,
        input,
        None,
    )
    .await
}
async fn test_connection(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
    body: Bytes,
) -> Response {
    let input = if body.iter().all(u8::is_ascii_whitespace) {
        json!({})
    } else {
        match serde_json::from_slice(&body) {
            Ok(input) => input,
            Err(_) => return failure("invalid request payload"),
        }
    };
    run(s, a, DeploymentOperation::TestConnection, None, input, None).await
}
async fn update(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
    Path(raw): Path<String>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    let Some(id) = id(raw) else {
        return failure("deployment ID is required");
    };
    let input = match json_body(&body) {
        Ok(input) => input,
        Err(()) => return failure("invalid request payload"),
    };
    run(
        s,
        a,
        DeploymentOperation::Update,
        Some(id),
        input,
        header_key(&headers),
    )
    .await
}

#[derive(Deserialize)]
struct NameRequest {
    name: String,
    #[serde(default)]
    idempotency_key: Option<String>,
}
async fn rename(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
    Path(raw): Path<String>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    let Some(id) = id(raw) else {
        return failure("deployment ID is required");
    };
    let input = match json_body::<NameRequest>(&body) {
        Ok(input) => input,
        Err(()) => return failure("invalid request payload"),
    };
    let name = input.name.trim();
    if name.is_empty() {
        return failure("deployment name cannot be empty");
    }
    run(
        s,
        a,
        DeploymentOperation::Rename,
        Some(id),
        json!({"name": name}),
        header_key(&headers).or_else(|| key(input.idempotency_key)),
    )
    .await
}
async fn extend(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
    Path(raw): Path<String>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    let Some(id) = id(raw) else {
        return failure("deployment ID is required");
    };
    let input = match json_body(&body) {
        Ok(input) => input,
        Err(()) => return failure("invalid request payload"),
    };
    run(
        s,
        a,
        DeploymentOperation::Extend,
        Some(id),
        input,
        header_key(&headers),
    )
    .await
}
async fn remove(
    State(s): State<DeploymentState>,
    a: Option<Extension<DeploymentActor>>,
    Path(raw): Path<String>,
    headers: HeaderMap,
) -> Response {
    match id(raw) {
        Some(id) => {
            run(
                s,
                a,
                DeploymentOperation::Delete,
                Some(id),
                json!({}),
                header_key(&headers),
            )
            .await
        }
        None => failure("deployment ID is required"),
    }
}

#[cfg(test)]
mod runtime_tests {
    use super::*;

    fn call(operation: DeploymentOperation) -> DeploymentCall {
        DeploymentCall {
            operation,
            actor: DeploymentActor {
                user_id: 7,
                role: ADMIN_ROLE,
            },
            deployment_id: Some("dep-1".to_owned()),
            input: json!({}),
            idempotency_key: Some("retry-1".to_owned()),
        }
    }

    #[tokio::test]
    async fn disabled_runner_is_fail_closed_and_never_fabricates_a_job_result() {
        let result = DisabledDeploymentJobRunner
            .run(call(DeploymentOperation::Create))
            .await;
        assert_eq!(result, Err(DeploymentError::NotConfigured));
    }

    #[test]
    fn runtime_requires_a_nonempty_server_secret_and_fixed_https_origin() {
        let client = crate::outbound_http::client(Duration::from_secs(1)).expect("client");
        let public = reqwest::Url::parse(IONET_PUBLIC_BASE).expect("public URL");
        let enterprise = reqwest::Url::parse(IONET_ENTERPRISE_BASE).expect("enterprise URL");
        assert!(
            IoNetDeploymentJobRunner::with_client(
                client.clone(),
                SecretString::from("server-secret"),
                public.clone(),
                enterprise.clone(),
            )
            .is_ok()
        );
        assert_eq!(
            IoNetDeploymentJobRunner::with_client(
                client.clone(),
                SecretString::from(""),
                public.clone(),
                enterprise.clone(),
            )
            .err(),
            Some(DeploymentError::NotConfigured)
        );
        let untrusted = reqwest::Url::parse("https://example.test/v1/").expect("URL");
        assert_eq!(
            IoNetDeploymentJobRunner::with_client(
                client,
                SecretString::from("server-secret"),
                untrusted,
                enterprise,
            )
            .err(),
            Some(DeploymentError::NotConfigured)
        );
    }

    #[test]
    fn runtime_uses_encoded_pinned_endpoints_and_rejects_path_injection() {
        let runner = IoNetDeploymentJobRunner::new(SecretString::from("server-secret"))
            .expect("configured runner");
        let url = runner
            .endpoint(
                true,
                &["deployment", "dep id", "log", "container#1"],
                &json!({"limit": 100, "follow": true}),
            )
            .expect("pinned endpoint");
        assert_eq!(url.host_str(), Some(IONET_HOST));
        assert!(url.as_str().contains("dep%20id"));
        assert!(url.as_str().contains("container%231"));
        assert!(url.as_str().contains("limit=100"));
        assert_eq!(
            runner.endpoint(true, &["deployment", "../admin"], &json!({})),
            Err(DeploymentError::Rejected(
                "invalid deployment identifier".to_owned()
            ))
        );
    }

    #[test]
    fn mutations_require_the_direct_io_net_response_shape() {
        assert_eq!(
            exact_mutation_response(&json!({"deployment_id": "d-1", "status": "requested"})),
            Ok(json!({"deployment_id": "d-1", "status": "requested"}))
        );
        for response in [
            json!({"status": "requested"}),
            json!({"deployment_id": "d-1"}),
            json!({"deployment_id": 1, "status": "requested"}),
            json!({"deployment_id": "d-1", "status": 1}),
        ] {
            assert_eq!(
                exact_mutation_response(&response),
                Err(DeploymentError::Unavailable)
            );
        }
    }

    #[test]
    fn legacy_search_filters_locally_and_recomputes_total() {
        let mut response = json!({
            "deployments": [
                {"name": "primary-gpu"},
                {"name": "batch-cpu"}
            ],
            "total": 999
        });
        filter_deployments(&mut response, Some(" GPU "));
        assert_eq!(
            response,
            json!({"deployments": [{"name": "primary-gpu"}], "total": 1})
        );
    }

    #[test]
    fn upstream_error_detail_is_bounded_and_never_reflects_raw_html() {
        assert_eq!(
            upstream_error_message(
                reqwest::StatusCode::UNAUTHORIZED,
                br#"{"detail":"bad key"}"#
            ),
            "bad key"
        );
        assert_eq!(
            upstream_error_message(reqwest::StatusCode::BAD_GATEWAY, b"<html>upstream</html>"),
            "io.net request failed with status 502"
        );
    }
}
