//! Public control-plane routes whose legacy behavior is independent of
//! dashboard authentication.
//!
//! The surrounding application must merge [`control_public_router`] beneath
//! its API-wide rate-limit and request-boundary middleware.  In particular,
//! this module intentionally does not bypass a failed-closed global limiter.

use std::{sync::Arc, time::Duration};

use async_trait::async_trait;
use axum::{
    Json, Router,
    extract::State,
    http::{HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::get,
};
use serde::{Deserialize, Serialize, de::DeserializeOwned};
use sqlx::PgPool;

const UPTIME_GROUPS_OPTION: &str = "console_setting.uptime_kuma_groups";
const USER_AGREEMENT_OPTION: &str = "legal.user_agreement";
const PRIVACY_POLICY_OPTION: &str = "legal.privacy_policy";
const UPTIME_REQUEST_TIMEOUT: Duration = Duration::from_secs(30);
const UPTIME_HTTP_TIMEOUT: Duration = Duration::from_secs(10);

/// Failure returned by the authoritative option store.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ControlPublicError;

impl std::fmt::Display for ControlPublicError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str("public control-plane dependency unavailable")
    }
}

impl std::error::Error for ControlPublicError {}

/// Read-only source for the option-backed public configuration.
#[async_trait]
pub trait ControlPublicRepository: Send + Sync {
    /// Reads a single option, returning `None` for an absent legacy option.
    async fn option(&self, key: &str) -> Result<Option<String>, ControlPublicError>;
}

/// PostgreSQL-backed option reader for the anonymous control plane.
///
/// Public legal and uptime configuration is intentionally read from the
/// authoritative database instead of a process-local snapshot.  A caller can
/// therefore recover after a stale process or Valkey outage without exposing
/// cached legal text.
#[derive(Clone)]
pub struct PgControlPublicRepository {
    pg: PgPool,
}

impl PgControlPublicRepository {
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self { pg }
    }
}

#[async_trait]
impl ControlPublicRepository for PgControlPublicRepository {
    async fn option(&self, key: &str) -> Result<Option<String>, ControlPublicError> {
        sqlx::query_scalar("SELECT value FROM options WHERE key = $1")
            .bind(key)
            .fetch_optional(&self.pg)
            .await
            .map_err(|_| ControlPublicError)
            .map(|value: Option<Option<String>>| value.flatten())
    }
}

/// Uptime Kuma status-page payload after transport and JSON decoding.
#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
pub struct UptimeStatusPage {
    /// Public monitor groups in the order provided by Uptime Kuma.
    #[serde(rename = "publicGroupList", default)]
    pub public_group_list: Vec<UptimePublicGroup>,
}

/// A public monitor group from an Uptime Kuma status page.
#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
pub struct UptimePublicGroup {
    /// Display name of the group.
    #[serde(default)]
    pub name: String,
    /// Monitors within the group.
    #[serde(rename = "monitorList", default)]
    pub monitor_list: Vec<UptimePublicMonitor>,
}

/// A monitor from an Uptime Kuma status page.
#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
pub struct UptimePublicMonitor {
    /// Uptime Kuma monitor id.
    #[serde(default)]
    pub id: i64,
    /// Display name of the monitor.
    #[serde(default)]
    pub name: String,
}

/// Uptime Kuma heartbeat payload after transport and JSON decoding.
#[derive(Clone, Debug, Default, Deserialize, PartialEq)]
pub struct UptimeHeartbeatPage {
    /// Most-recent-first heartbeat records indexed by monitor id.
    #[serde(rename = "heartbeatList", default)]
    pub heartbeat_list: std::collections::BTreeMap<String, Vec<UptimeHeartbeat>>,
    /// Uptime figures indexed by `"{monitor_id}_24"`.
    #[serde(rename = "uptimeList", default)]
    pub uptime_list: std::collections::BTreeMap<String, f64>,
}

/// Minimal heartbeat data used by the legacy controller.
#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
pub struct UptimeHeartbeat {
    /// Legacy Uptime Kuma integer status.
    #[serde(default)]
    pub status: i64,
}

/// Transport/decoding adapter for a configured Uptime Kuma instance.
///
/// The adapter receives the fully assembled status-page and heartbeat URLs so
/// it can enforce the legacy ten-second HTTP client timeout without adding a
/// second HTTP stack to this route slice.
#[async_trait]
pub trait UptimeKumaClient: Send + Sync {
    /// Fetches and decodes `/api/status-page/{slug}`.
    async fn status_page(&self, url: &str) -> Result<UptimeStatusPage, ControlPublicError>;

    /// Fetches and decodes `/api/status-page/heartbeat/{slug}`.
    async fn heartbeat_page(&self, url: &str) -> Result<UptimeHeartbeatPage, ControlPublicError>;
}

/// Concrete Uptime Kuma HTTP adapter with the legacy ten-second client deadline.
///
/// Uptime Kuma endpoints are operator-configured and legacy deployments may use
/// HTTP as well as HTTPS, so this intentionally mirrors Go's ordinary
/// `http.Client` rather than the HTTPS-only outbound policy used for pinned
/// control-plane providers.
#[derive(Clone)]
pub struct ReqwestUptimeKumaClient {
    client: reqwest::Client,
}

impl ReqwestUptimeKumaClient {
    /// Builds the same bounded, redirect-following client shape as the legacy route.
    pub fn new() -> Result<Self, ControlPublicError> {
        reqwest::Client::builder()
            .timeout(UPTIME_HTTP_TIMEOUT)
            .build()
            .map(|client| Self { client })
            .map_err(|_| ControlPublicError)
    }

    async fn get_json<T: DeserializeOwned>(&self, url: &str) -> Result<T, ControlPublicError> {
        let response = self
            .client
            .get(url)
            .send()
            .await
            .map_err(|_| ControlPublicError)?;
        if response.status() != reqwest::StatusCode::OK {
            return Err(ControlPublicError);
        }
        response.json().await.map_err(|_| ControlPublicError)
    }
}

#[async_trait]
impl UptimeKumaClient for ReqwestUptimeKumaClient {
    async fn status_page(&self, url: &str) -> Result<UptimeStatusPage, ControlPublicError> {
        self.get_json(url).await
    }

    async fn heartbeat_page(&self, url: &str) -> Result<UptimeHeartbeatPage, ControlPublicError> {
        self.get_json(url).await
    }
}

/// Dependencies for the public control-plane slice.
#[derive(Clone)]
pub struct ControlPublicHttpState {
    repository: Arc<dyn ControlPublicRepository>,
    uptime_kuma: Arc<dyn UptimeKumaClient>,
}

impl ControlPublicHttpState {
    /// Creates public control-plane state from authoritative adapters.
    #[must_use]
    pub fn new(
        repository: Arc<dyn ControlPublicRepository>,
        uptime_kuma: Arc<dyn UptimeKumaClient>,
    ) -> Self {
        Self {
            repository,
            uptime_kuma,
        }
    }
}

/// Public routes to merge under the API-wide request middleware.
pub fn control_public_router(state: ControlPublicHttpState) -> Router {
    Router::new()
        .route("/api/uptime/status", get(uptime_status))
        .route("/api/user-agreement", get(user_agreement))
        .route("/api/privacy-policy", get(privacy_policy))
        .with_state(state)
}

async fn user_agreement(State(state): State<ControlPublicHttpState>) -> Response {
    legal_document(state, USER_AGREEMENT_OPTION).await
}

async fn privacy_policy(State(state): State<ControlPublicHttpState>) -> Response {
    legal_document(state, PRIVACY_POLICY_OPTION).await
}

async fn legal_document(state: ControlPublicHttpState, key: &str) -> Response {
    match state.repository.option(key).await {
        Ok(value) => {
            let mut response = legacy_success(value.unwrap_or_default()).into_response();
            response.headers_mut().insert(
                header::CONTENT_TYPE,
                HeaderValue::from_static("application/json; charset=utf-8"),
            );
            response
        }
        Err(error) => {
            tracing::error!(%error, option = key, "public legal document read failed");
            legacy_dependency_error()
        }
    }
}

async fn uptime_status(State(state): State<ControlPublicHttpState>) -> Response {
    let groups = match state.repository.option(UPTIME_GROUPS_OPTION).await {
        Ok(value) => uptime_groups(value.as_deref()),
        Err(error) => {
            tracing::error!(%error, "uptime kuma group configuration read failed");
            return legacy_dependency_error();
        }
    };

    // This exact empty Vec (rather than `null`) is observable in the legacy
    // response when no group configuration exists or it is malformed.
    if groups.is_empty() {
        return legacy_success(Vec::<UptimeGroupResult>::new());
    }

    let mut tasks = tokio::task::JoinSet::new();
    for (index, group) in groups.into_iter().enumerate() {
        let client = Arc::clone(&state.uptime_kuma);
        tasks.spawn(async move { (index, fetch_group(client, group).await) });
    }

    let mut results = vec![UptimeGroupResult::default(); tasks.len()];
    // The Go controller has a 30-second request context.  Individual group
    // requests are already bounded at ten seconds; aborting any remaining
    // groups at the outer deadline preserves a successful aggregate envelope.
    let collect = async {
        while let Some(joined) = tasks.join_next().await {
            if let Ok((index, result)) = joined {
                results[index] = result;
            }
        }
    };
    if tokio::time::timeout(UPTIME_REQUEST_TIMEOUT, collect)
        .await
        .is_err()
    {
        tasks.abort_all();
    }
    legacy_success(results)
}

async fn fetch_group(
    client: Arc<dyn UptimeKumaClient>,
    group: UptimeGroupConfig,
) -> UptimeGroupResult {
    let empty = UptimeGroupResult {
        category_name: group.category_name,
        monitors: Vec::new(),
    };
    if group.url.is_empty() || group.slug.is_empty() {
        return empty;
    }

    let base_url = group.url.trim_end_matches('/');
    let status_url = format!("{base_url}/api/status-page/{}", group.slug);
    let heartbeat_url = format!("{base_url}/api/status-page/heartbeat/{}", group.slug);
    let (status, heartbeat) = tokio::join!(
        tokio::time::timeout(UPTIME_HTTP_TIMEOUT, client.status_page(&status_url)),
        tokio::time::timeout(UPTIME_HTTP_TIMEOUT, client.heartbeat_page(&heartbeat_url)),
    );
    let (Ok(Ok(status)), Ok(Ok(heartbeat))) = (status, heartbeat) else {
        return empty;
    };

    let mut monitors = Vec::new();
    for public_group in status.public_group_list {
        let group_name = &public_group.name;
        for monitor in public_group.monitor_list {
            let id = monitor.id.to_string();
            monitors.push(UptimeMonitor {
                name: monitor.name,
                uptime: heartbeat
                    .uptime_list
                    .get(&format!("{id}_24"))
                    .copied()
                    .unwrap_or_default(),
                status: heartbeat
                    .heartbeat_list
                    .get(&id)
                    .and_then(|heartbeats| heartbeats.first())
                    .map_or(0, |entry| entry.status),
                group: group_name.clone(),
            });
        }
    }
    UptimeGroupResult {
        category_name: empty.category_name,
        monitors,
    }
}

fn uptime_groups(raw: Option<&str>) -> Vec<UptimeGroupConfig> {
    // Go's json.Unmarshal into []map[string]interface{} ignores malformed
    // JSON and yields no usable groups.  Preserve that compatibility instead
    // of surfacing a configuration parse error to public callers.
    serde_json::from_str::<Vec<serde_json::Value>>(raw.unwrap_or_default())
        .unwrap_or_default()
        .into_iter()
        .map(|value| UptimeGroupConfig {
            url: value
                .get("url")
                .and_then(serde_json::Value::as_str)
                .unwrap_or_default()
                .to_owned(),
            slug: value
                .get("slug")
                .and_then(serde_json::Value::as_str)
                .unwrap_or_default()
                .to_owned(),
            category_name: value
                .get("categoryName")
                .and_then(serde_json::Value::as_str)
                .unwrap_or_default()
                .to_owned(),
        })
        .collect()
}

#[derive(Debug)]
struct UptimeGroupConfig {
    url: String,
    slug: String,
    category_name: String,
}

#[derive(Clone, Debug, Default, Serialize, PartialEq)]
struct UptimeGroupResult {
    #[serde(rename = "categoryName")]
    category_name: String,
    monitors: Vec<UptimeMonitor>,
}

#[derive(Clone, Debug, Serialize, PartialEq)]
struct UptimeMonitor {
    name: String,
    uptime: f64,
    status: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    group: String,
}

#[derive(Serialize)]
struct LegacySuccess<T> {
    success: bool,
    message: &'static str,
    data: T,
}

fn legacy_success<T: Serialize>(data: T) -> Response {
    let mut response = Json(LegacySuccess {
        success: true,
        message: "",
        data,
    })
    .into_response();
    // Gin's JSON writer includes the charset parameter on these public API
    // envelopes; preserve that observable legacy wire contract.
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

fn legacy_dependency_error() -> Response {
    let mut response = (
        StatusCode::INTERNAL_SERVER_ERROR,
        Json(LegacyFailure {
            success: false,
            message: "public control-plane data is temporarily unavailable",
        }),
    )
        .into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

#[derive(Serialize)]
struct LegacyFailure {
    success: bool,
    message: &'static str,
}
