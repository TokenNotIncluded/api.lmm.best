//! Legacy-compatible ratio synchronisation routes.
//!
//! This slice keeps outbound fetching behind an injected boundary.  The test
//! instance must use [`TestInstanceDisabledRatioSyncUpstream`], so exposing the
//! route while it is being differentially tested can never make a real network
//! request.

use std::{
    collections::{BTreeMap, BTreeSet},
    net::{IpAddr, SocketAddr},
    sync::Arc,
    time::Duration,
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    extract::{Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value, json};
use sqlx::{PgPool, Row};

use crate::auth::DashboardAuth;

const ROOT_ROLE: i64 = 100;
const DEFAULT_TIMEOUT_SECONDS: u64 = 10;
const DEFAULT_ENDPOINT: &str = "/api/pricing";
const OFFICIAL_PRESET_ID: i64 = -100;
const MODELS_DEV_PRESET_ID: i64 = -101;
const MAX_RATIO_RESPONSE_BYTES: usize = 10 << 20;
const MAX_CONCURRENT_FETCHES: usize = 8;
const CONSOLE_NOT_FOUND: &str = "__console_not_found__";

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RatioSyncIdentity {
    pub role: i64,
}

#[async_trait]
pub trait RatioSyncAuthorizer: Send + Sync {
    async fn authorize(&self, headers: &HeaderMap) -> Result<RatioSyncIdentity, &'static str>;
}

/// Production root-auth adapter.  Roles are obtained only from the validated
/// dashboard session, never from an HTTP header.
#[derive(Clone)]
pub struct DashboardRatioSyncAuthorizer {
    auth: Arc<dyn DashboardAuth>,
}

impl DashboardRatioSyncAuthorizer {
    #[must_use]
    pub fn new(auth: Arc<dyn DashboardAuth>) -> Self {
        Self { auth }
    }
}

#[async_trait]
impl RatioSyncAuthorizer for DashboardRatioSyncAuthorizer {
    async fn authorize(&self, headers: &HeaderMap) -> Result<RatioSyncIdentity, &'static str> {
        let credential = dashboard_credential(headers).ok_or(CONSOLE_NOT_FOUND)?;
        let user = self
            .auth
            .self_user_view_for_optional(SecretString::from(credential.to_owned()))
            .await
            .map_err(|_| CONSOLE_NOT_FOUND)?;
        if !user.developer_access_granted {
            return Err(CONSOLE_NOT_FOUND);
        }
        Ok(RatioSyncIdentity { role: user.role })
    }
}

fn dashboard_credential(headers: &HeaderMap) -> Option<&str> {
    let value = headers.get(header::AUTHORIZATION)?.to_str().ok()?;
    let mut pieces = value.split_whitespace();
    let first = pieces.next()?;
    let second = pieces.next();
    if pieces.next().is_some() {
        return None;
    }
    match second {
        Some(token) if first.eq_ignore_ascii_case("bearer") && !token.is_empty() => Some(token),
        None if !first.is_empty() => Some(first),
        _ => None,
    }
}

#[derive(Clone, Debug, Serialize, PartialEq)]
pub struct SyncableChannel {
    pub id: i64,
    pub name: String,
    pub base_url: String,
    pub status: i64,
    #[serde(rename = "type")]
    pub channel_type: i64,
}

#[derive(Clone, Debug)]
pub struct UpstreamTarget {
    pub id: i64,
    pub name: String,
    pub base_url: String,
    pub endpoint: String,
    pub api_key: Option<String>,
}

/// Local pricing plus the quota conversion base read from one repository
/// snapshot.  OpenRouter and models.dev quote USD prices, while the dashboard
/// stores its currently configured quota-per-USD value in `QuotaPerUnit`.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct RatioSyncPricingSnapshot {
    pub local_pricing: BTreeMap<String, Value>,
    pub usd_ratio: f64,
}

#[async_trait]
pub trait RatioSyncRepository: Send + Sync {
    async fn syncable_channels(&self) -> Result<Vec<SyncableChannel>, String>;
    async fn channels_by_ids(&self, ids: &[i64]) -> Result<Vec<UpstreamTarget>, String>;
    /// Reads all local pricing fields and its USD conversion base together.
    async fn pricing_snapshot(&self) -> Result<RatioSyncPricingSnapshot, String>;
}

/// PostgreSQL implementation of the legacy channel and local-ratio reads.
#[derive(Clone)]
pub struct PgRatioSyncRepository {
    pg: PgPool,
}

impl PgRatioSyncRepository {
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self { pg }
    }
}

#[async_trait]
impl RatioSyncRepository for PgRatioSyncRepository {
    async fn syncable_channels(&self) -> Result<Vec<SyncableChannel>, String> {
        let mut transaction = self.pg.begin().await.map_err(|error| error.to_string())?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
            .execute(&mut *transaction)
            .await
            .map_err(|error| error.to_string())?;
        let rows = sqlx::query("SELECT id, COALESCE(name, ''), COALESCE(base_url, ''), COALESCE(status, 1), COALESCE(type, 0) FROM channels")
            .fetch_all(&mut *transaction).await.map_err(|error| error.to_string())?;
        transaction
            .commit()
            .await
            .map_err(|error| error.to_string())?;
        Ok(rows
            .into_iter()
            .filter_map(|row| {
                let base_url: String = row.try_get(2).ok()?;
                (!base_url.is_empty()).then(|| SyncableChannel {
                    id: row.try_get(0).unwrap_or_default(),
                    name: row.try_get(1).unwrap_or_default(),
                    base_url,
                    status: row.try_get(3).unwrap_or(1),
                    channel_type: row.try_get(4).unwrap_or_default(),
                })
            })
            .collect())
    }

    async fn channels_by_ids(&self, ids: &[i64]) -> Result<Vec<UpstreamTarget>, String> {
        let mut transaction = self.pg.begin().await.map_err(|error| error.to_string())?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
            .execute(&mut *transaction)
            .await
            .map_err(|error| error.to_string())?;
        let rows = sqlx::query("SELECT id, COALESCE(name, ''), COALESCE(base_url, ''), key FROM channels WHERE id = ANY($1)")
            .bind(ids).fetch_all(&mut *transaction).await.map_err(|error| error.to_string())?;
        transaction
            .commit()
            .await
            .map_err(|error| error.to_string())?;
        Ok(rows
            .into_iter()
            .filter_map(|row| {
                let base_url: String = row.try_get(2).ok()?;
                (!base_url.is_empty()).then(|| UpstreamTarget {
                    id: row.try_get(0).unwrap_or_default(),
                    name: row.try_get(1).unwrap_or_default(),
                    base_url,
                    endpoint: String::new(),
                    api_key: row.try_get::<String, _>(3).ok(),
                })
            })
            .collect())
    }

    async fn pricing_snapshot(&self) -> Result<RatioSyncPricingSnapshot, String> {
        // These option keys are the PostgreSQL representation of the legacy
        // ratio_setting and billing_setting exposed maps.
        const OPTIONS: [(&str, &str); 10] = [
            ("ModelRatio", "model_ratio"),
            ("CompletionRatio", "completion_ratio"),
            ("CacheRatio", "cache_ratio"),
            ("CreateCacheRatio", "create_cache_ratio"),
            ("ImageRatio", "image_ratio"),
            ("AudioRatio", "audio_ratio"),
            ("AudioCompletionRatio", "audio_completion_ratio"),
            ("ModelPrice", "model_price"),
            ("billing_setting.billing_mode", "billing_mode"),
            ("billing_setting.billing_expr", "billing_expr"),
        ];
        let mut transaction = self.pg.begin().await.map_err(|error| error.to_string())?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
            .execute(&mut *transaction)
            .await
            .map_err(|error| error.to_string())?;
        let keys = OPTIONS
            .iter()
            .map(|(key, _)| *key)
            .chain(std::iter::once("QuotaPerUnit"))
            .collect::<Vec<_>>();
        let rows = sqlx::query("SELECT key, value FROM options WHERE key = ANY($1)")
            .bind(&keys)
            .fetch_all(&mut *transaction)
            .await
            .map_err(|error| error.to_string())?;
        transaction
            .commit()
            .await
            .map_err(|error| error.to_string())?;

        let values = rows
            .into_iter()
            .filter_map(|row| {
                Some((
                    row.try_get::<String, _>("key").ok()?,
                    row.try_get::<String, _>("value").ok()?,
                ))
            })
            .collect::<BTreeMap<_, _>>();
        let local_pricing = OPTIONS
            .into_iter()
            .filter_map(|(key, field)| {
                values
                    .get(key)
                    .and_then(|value| serde_json::from_str(value).ok())
                    .map(|value| (field.to_owned(), value))
            })
            .collect();
        // A quota unit is expressed per thousand legacy USD-ratio points.
        // Treat an absent or malformed dynamic setting as an unavailable local
        // pricing snapshot; do not silently resurrect the former fixed 500.
        let usd_ratio = values
            .get("QuotaPerUnit")
            .ok_or_else(|| "QuotaPerUnit 配置无效".to_owned())
            .and_then(|value| usd_ratio_from_quota_per_unit(value))?;
        Ok(RatioSyncPricingSnapshot {
            local_pricing,
            usd_ratio,
        })
    }
}

#[async_trait]
pub trait RatioSyncUpstream: Send + Sync {
    async fn fetch(
        &self,
        target: &UpstreamTarget,
        timeout: Duration,
        usd_ratio: f64,
    ) -> Result<BTreeMap<String, Value>, String>;
}

/// The production transport accepts only pinned public HTTPS destinations.
///
/// The frozen Go handler accepts arbitrary HTTP URLs, but a root-only helper
/// must not become an SSRF escape hatch. DNS is resolved
/// before each request and those addresses are supplied back to reqwest, so a
/// second resolver lookup cannot rebind the connection to a private address.
#[derive(Clone, Default)]
pub struct HttpRatioSyncUpstream;

#[async_trait]
impl RatioSyncUpstream for HttpRatioSyncUpstream {
    async fn fetch(
        &self,
        target: &UpstreamTarget,
        timeout: Duration,
        usd_ratio: f64,
    ) -> Result<BTreeMap<String, Value>, String> {
        let (url, openrouter) = upstream_url(target)?;
        let mut request = pinned_client(&url, timeout).await?.get(url.clone());
        if openrouter {
            let key = target
                .api_key
                .as_deref()
                .filter(|key| !key.trim().is_empty())
                .ok_or_else(|| {
                    if target.id == 0 {
                        "OpenRouter requires a valid channel with API key".to_owned()
                    } else {
                        "no API key configured for this channel".to_owned()
                    }
                })?;
            request = request.bearer_auth(key.trim());
        }
        let response = request.send().await.map_err(|error| error.to_string())?;
        if response.status() != StatusCode::OK {
            return Err(response.status().to_string());
        }
        let bytes = bounded_response_bytes(response).await?;
        if openrouter {
            return openrouter_ratios(&bytes, usd_ratio);
        }
        if is_models_dev(url.as_str()) {
            return models_dev_ratios(&bytes, usd_ratio);
        }
        pricing_ratios(&bytes)
    }
}

fn upstream_url(target: &UpstreamTarget) -> Result<(reqwest::Url, bool), String> {
    let endpoint = target.endpoint.trim();
    let openrouter = endpoint == "openrouter";
    let raw = if openrouter {
        format!("{}/v1/models", target.base_url.trim_end_matches('/'))
    } else if endpoint.starts_with("https://") {
        endpoint.to_owned()
    } else if endpoint.starts_with("http://") {
        return Err("only HTTPS upstream URLs are allowed".to_owned());
    } else {
        format!(
            "{}{}",
            target.base_url.trim_end_matches('/'),
            if endpoint.is_empty() {
                DEFAULT_ENDPOINT.to_owned()
            } else if endpoint.starts_with('/') {
                endpoint.to_owned()
            } else {
                format!("/{endpoint}")
            }
        )
    };
    let url = reqwest::Url::parse(&raw).map_err(|_| "invalid upstream URL".to_owned())?;
    validate_ratio_url(&url)?;
    Ok((url, openrouter))
}

fn validate_ratio_url(url: &reqwest::Url) -> Result<(), String> {
    if url.scheme() != "https" || !url.username().is_empty() || url.password().is_some() {
        return Err("only absolute HTTPS upstream URLs are allowed".to_owned());
    }
    let host = url
        .host_str()
        .ok_or_else(|| "upstream URL has no host".to_owned())?;
    if host.eq_ignore_ascii_case("localhost") || host.ends_with(".localhost") {
        return Err("unsafe upstream host".to_owned());
    }
    let port = url
        .port_or_known_default()
        .ok_or_else(|| "upstream URL has no port".to_owned())?;
    if !matches!(port, 443 | 8443) {
        return Err("upstream HTTPS port is not allowed".to_owned());
    }
    if let Ok(ip) = host.parse::<IpAddr>()
        && !globally_routable(ip)
    {
        return Err("unsafe upstream IP".to_owned());
    }
    Ok(())
}

async fn pinned_client(url: &reqwest::Url, timeout: Duration) -> Result<reqwest::Client, String> {
    if timeout.is_zero() {
        return Err("upstream timeout must be positive".to_owned());
    }
    let host = url
        .host_str()
        .ok_or_else(|| "upstream URL has no host".to_owned())?;
    let port = url
        .port_or_known_default()
        .ok_or_else(|| "upstream URL has no port".to_owned())?;
    let addresses = tokio::net::lookup_host((host, port))
        .await
        .map_err(|_| "upstream DNS resolution failed".to_owned())?
        .collect::<Vec<SocketAddr>>();
    if addresses.is_empty()
        || addresses
            .iter()
            .any(|address| !globally_routable(address.ip()))
    {
        return Err("unsafe upstream DNS result".to_owned());
    }
    reqwest::Client::builder()
        .use_rustls_tls()
        .https_only(true)
        .no_proxy()
        .redirect(reqwest::redirect::Policy::none())
        .connect_timeout(timeout.min(Duration::from_secs(3)))
        .timeout(timeout)
        .resolve_to_addrs(host, &addresses)
        .build()
        .map_err(|_| "failed to create upstream client".to_owned())
}

async fn bounded_response_bytes(mut response: reqwest::Response) -> Result<Vec<u8>, String> {
    if response.content_length().is_some_and(|length| {
        usize::try_from(length).map_or(true, |length| length > MAX_RATIO_RESPONSE_BYTES)
    }) {
        return Err("response body too large".to_owned());
    }
    let mut body = Vec::new();
    while let Some(chunk) = response.chunk().await.map_err(|error| error.to_string())? {
        if body.len().saturating_add(chunk.len()) > MAX_RATIO_RESPONSE_BYTES {
            return Err("response body too large".to_owned());
        }
        body.extend_from_slice(&chunk);
    }
    Ok(body)
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
                || (segments[0] == 0x2001 && segments[1] == 0x0db8))
        }
    }
}

/// Test-instance provider.  It fails closed without opening a socket.
#[derive(Clone, Default)]
pub struct TestInstanceDisabledRatioSyncUpstream;
#[async_trait]
impl RatioSyncUpstream for TestInstanceDisabledRatioSyncUpstream {
    async fn fetch(
        &self,
        _: &UpstreamTarget,
        _: Duration,
        _: f64,
    ) -> Result<BTreeMap<String, Value>, String> {
        Err("测试实例已禁用上游倍率同步".to_owned())
    }
}

#[derive(Clone)]
pub struct RatioSyncHttpState {
    repository: Arc<dyn RatioSyncRepository>,
    upstream: Arc<dyn RatioSyncUpstream>,
    authorizer: Arc<dyn RatioSyncAuthorizer>,
}

impl RatioSyncHttpState {
    #[must_use]
    pub fn new(
        repository: Arc<dyn RatioSyncRepository>,
        upstream: Arc<dyn RatioSyncUpstream>,
        authorizer: Arc<dyn RatioSyncAuthorizer>,
    ) -> Self {
        Self {
            repository,
            upstream,
            authorizer,
        }
    }
}

pub fn ratio_sync_router(state: RatioSyncHttpState) -> Router {
    let protected = Router::new()
        .route("/api/ratio_sync/channels", get(syncable_channels))
        .route("/api/ratio_sync/fetch", post(fetch_upstream_ratios))
        // RootAuth runs before JSON extraction: an unauthenticated malformed
        // body is still an auth failure, as it is in Gin's route group.
        .route_layer(middleware::from_fn_with_state(
            state.clone(),
            ratio_sync_root_guard,
        ));
    Router::new().merge(protected).with_state(state)
}

#[derive(Serialize)]
struct Envelope<T: Serialize> {
    success: bool,
    message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<T>,
}
fn legacy_ok<T: Serialize>(data: T) -> Response {
    Json(Envelope {
        success: true,
        message: String::new(),
        data: Some(data),
    })
    .into_response()
}
fn legacy_error(message: impl Into<String>) -> Response {
    Json(Envelope::<Value> {
        success: false,
        message: message.into(),
        data: None,
    })
    .into_response()
}

async fn root(state: &RatioSyncHttpState, headers: &HeaderMap) -> Result<(), Response> {
    match state.authorizer.authorize(headers).await {
        Err(CONSOLE_NOT_FOUND) => {
            let mut response =
                (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response();
            response.headers_mut().insert(
                header::CONTENT_TYPE,
                HeaderValue::from_static("application/json; charset=utf-8"),
            );
            Err(response)
        }
        Err(message) => Err((
            StatusCode::UNAUTHORIZED,
            Json(json!({"success":false,"message":message,"code":"AUTH_UNAUTHORIZED"})),
        )
            .into_response()),
        Ok(identity) if identity.role == ROOT_ROLE => Ok(()),
        Ok(_) => Err((
            StatusCode::FORBIDDEN,
            Json(json!({"success":false,"message":"管理员权限不足"})),
        )
            .into_response()),
    }
}

async fn ratio_sync_root_guard(
    State(state): State<RatioSyncHttpState>,
    request: Request,
    next: Next,
) -> Response {
    if let Err(response) = root(&state, request.headers()).await {
        return response;
    }
    next.run(request).await
}

async fn syncable_channels(State(state): State<RatioSyncHttpState>) -> Response {
    match state.repository.syncable_channels().await {
        Ok(mut channels) => {
            channels.push(SyncableChannel {
                id: OFFICIAL_PRESET_ID,
                name: "官方倍率预设".to_owned(),
                base_url: "https://basellm.github.io".to_owned(),
                status: 1,
                channel_type: 0,
            });
            channels.push(SyncableChannel {
                id: MODELS_DEV_PRESET_ID,
                name: "models.dev 价格预设".to_owned(),
                base_url: "https://models.dev".to_owned(),
                status: 1,
                channel_type: 0,
            });
            legacy_ok(channels)
        }
        Err(message) => legacy_error(message),
    }
}

#[derive(Debug, Deserialize)]
struct FetchRequest {
    #[serde(default)]
    channel_ids: Vec<i64>,
    #[serde(default)]
    upstreams: Vec<IncomingUpstream>,
    #[serde(default)]
    timeout: i64,
}
#[derive(Debug, Deserialize)]
struct IncomingUpstream {
    #[serde(default)]
    id: i64,
    name: String,
    base_url: String,
    #[serde(default)]
    endpoint: String,
}
#[derive(Serialize)]
struct TestResult {
    name: String,
    status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    error: Option<String>,
}

async fn fetch_upstream_ratios(
    State(state): State<RatioSyncHttpState>,
    body: Result<Json<FetchRequest>, axum::extract::rejection::JsonRejection>,
) -> Response {
    let Json(request) = match body {
        Ok(body) => body,
        Err(_) => {
            return (
                StatusCode::BAD_REQUEST,
                Json(json!({"success":false,"message":"请求参数格式错误"})),
            )
                .into_response();
        }
    };
    let timeout = Duration::from_secs(
        u64::try_from(request.timeout)
            .ok()
            .filter(|value| *value > 0)
            .unwrap_or(DEFAULT_TIMEOUT_SECONDS),
    );
    let targets = if !request.upstreams.is_empty() {
        request
            .upstreams
            .into_iter()
            .filter(|target| target.base_url.starts_with("http"))
            .map(|target| UpstreamTarget {
                id: target.id,
                name: target.name,
                base_url: target.base_url.trim_end_matches('/').to_owned(),
                endpoint: if target.endpoint.is_empty() {
                    DEFAULT_ENDPOINT.to_owned()
                } else {
                    target.endpoint
                },
                api_key: None,
            })
            .collect()
    } else if !request.channel_ids.is_empty() {
        match state.repository.channels_by_ids(&request.channel_ids).await {
            Ok(targets) => targets
                .into_iter()
                .filter(|target| target.base_url.starts_with("http"))
                .collect(),
            Err(_) => {
                return (
                    StatusCode::INTERNAL_SERVER_ERROR,
                    Json(json!({"success":false,"message":"查询渠道失败"})),
                )
                    .into_response();
            }
        }
    } else {
        Vec::new()
    };
    if targets.is_empty() {
        return legacy_error("无有效上游渠道");
    }
    let snapshot = match state.repository.pricing_snapshot().await {
        Ok(data) => data,
        Err(_) => return legacy_error("查询本地倍率失败"),
    };
    // The legacy implementation caps provider fan-out at eight.  Preserve
    // that bound while retaining request order in the emitted test results;
    // deterministic ordering makes equal snapshots comparable across runs.
    let mut completed = Vec::with_capacity(targets.len());
    let usd_ratio = snapshot.usd_ratio;
    for (chunk_index, chunk) in targets.chunks(MAX_CONCURRENT_FETCHES).enumerate() {
        let mut set = tokio::task::JoinSet::new();
        for (offset, target) in chunk.iter().cloned().enumerate() {
            let upstream = Arc::clone(&state.upstream);
            let index = chunk_index * MAX_CONCURRENT_FETCHES + offset;
            set.spawn(async move {
                let name = target_name(&target);
                let result = upstream.fetch(&target, timeout, usd_ratio).await;
                (index, name, result)
            });
        }
        while let Some(joined) = set.join_next().await {
            match joined {
                Ok(result) => completed.push(result),
                Err(_) => completed.push((
                    usize::MAX,
                    "upstream task".to_owned(),
                    Err("上游请求任务失败".to_owned()),
                )),
            }
        }
    }
    completed.sort_by_key(|(offset, _, _)| *offset);
    let mut results = Vec::with_capacity(completed.len());
    let mut successful = Vec::new();
    for (_, name, result) in completed {
        match result {
            Ok(data) => {
                results.push(TestResult {
                    name: name.clone(),
                    status: "success".to_owned(),
                    error: None,
                });
                successful.push((name, data));
            }
            Err(error) => results.push(TestResult {
                name,
                status: "error".to_owned(),
                error: Some(error),
            }),
        }
    }
    legacy_ok(
        json!({"differences": build_differences(&snapshot.local_pricing, &successful), "test_results": results}),
    )
}

fn target_name(target: &UpstreamTarget) -> String {
    if target.id == 0 {
        target.name.clone()
    } else {
        format!("{}({})", target.name, target.id)
    }
}

const FIELDS: [&str; 10] = [
    "model_ratio",
    "completion_ratio",
    "cache_ratio",
    "create_cache_ratio",
    "image_ratio",
    "audio_ratio",
    "audio_completion_ratio",
    "model_price",
    "billing_mode",
    "billing_expr",
];
fn map(value: Option<&Value>) -> BTreeMap<String, Value> {
    value
        .and_then(Value::as_object)
        .map(|values| {
            values
                .iter()
                .map(|(key, value)| (key.clone(), value.clone()))
                .collect()
        })
        .unwrap_or_default()
}
fn equal(left: &Value, right: &Value) -> bool {
    match (left.as_f64(), right.as_f64()) {
        (Some(left), Some(right)) => (left - right).abs() < 1e-9,
        _ => left == right,
    }
}
fn normalized(field: &str, value: &Value) -> Value {
    if [
        "model_ratio",
        "completion_ratio",
        "cache_ratio",
        "create_cache_ratio",
        "image_ratio",
        "audio_ratio",
        "audio_completion_ratio",
        "model_price",
    ]
    .contains(&field)
    {
        value
            .as_f64()
            .map_or_else(|| value.clone(), |value| json!(value))
    } else {
        value.clone()
    }
}

fn build_differences(
    local: &BTreeMap<String, Value>,
    upstreams: &[(String, BTreeMap<String, Value>)],
) -> BTreeMap<String, BTreeMap<String, Value>> {
    let mut models = BTreeSet::new();
    for field in FIELDS {
        models.extend(map(local.get(field)).into_keys());
        for (_, upstream) in upstreams {
            models.extend(map(upstream.get(field)).into_keys());
        }
    }
    let mut differences = BTreeMap::new();
    for model in models {
        let mut fields = BTreeMap::new();
        for field in FIELDS {
            let local_value = map(local.get(field))
                .get(&model)
                .cloned()
                .map(|value| normalized(field, &value));
            let mut upstream_values = Map::new();
            let mut confidence = Map::new();
            let mut has_difference = false;
            let mut has_upstream_value = false;
            for (name, upstream) in upstreams {
                let upstream_value = map(upstream.get(field))
                    .get(&model)
                    .cloned()
                    .map(|value| normalized(field, &value));
                let shown = match (&local_value, &upstream_value) {
                    (Some(left), Some(right)) if equal(left, right) => json!("same"),
                    (_, Some(value)) => {
                        has_upstream_value = true;
                        if local_value
                            .as_ref()
                            .is_none_or(|local| !equal(local, value))
                        {
                            has_difference = true;
                        }
                        value.clone()
                    }
                    (None, None) => json!("same"),
                    (Some(_), None) => Value::Null,
                };
                upstream_values.insert(name.clone(), shown);
                confidence.insert(
                    name.clone(),
                    Value::Bool(upstream_confidence(upstream, &model)),
                );
            }
            if (local_value.is_some() && has_difference)
                || (local_value.is_none() && has_upstream_value)
            {
                fields.insert(field.to_owned(), json!({"current":local_value,"upstreams":upstream_values,"confidence":confidence}));
            }
        }
        if !fields.is_empty() {
            differences.insert(model, fields);
        }
    }

    // Frozen Go removes channels that are globally identical from every
    // remaining difference.  Keep that presentation-level merge rule rather
    // than leaking a no-op provider alongside a real change.
    let active_channels = differences
        .values()
        .flat_map(BTreeMap::values)
        .filter_map(|value| value.get("upstreams").and_then(Value::as_object))
        .flat_map(|values| values.iter())
        .filter(|(_, value)| !value.is_null() && value.as_str() != Some("same"))
        .map(|(name, _)| name.clone())
        .collect::<BTreeSet<_>>();
    differences.retain(|_, fields| {
        fields.retain(|_, item| {
            for key in ["upstreams", "confidence"] {
                if let Some(values) = item.get_mut(key).and_then(Value::as_object_mut) {
                    values.retain(|name, _| active_channels.contains(name));
                }
            }
            item.get("upstreams")
                .and_then(Value::as_object)
                .is_some_and(|values| {
                    !values.is_empty()
                        && values.values().any(|value| value.as_str() != Some("same"))
                })
        });
        !fields.is_empty()
    });
    differences
}

fn upstream_confidence(upstream: &BTreeMap<String, Value>, model: &str) -> bool {
    let ratios = map(upstream.get("model_ratio"));
    let completions = map(upstream.get("completion_ratio"));
    match (ratios.get(model), completions.get(model)) {
        (Some(ratio), Some(completion)) => {
            !(ratio
                .as_f64()
                .zip(completion.as_f64())
                .is_some_and(|(ratio, completion)| {
                    (ratio - 37.5).abs() < 1e-9 && (completion - 1.0).abs() < 1e-9
                }))
        }
        _ => true,
    }
}

fn pricing_ratios(bytes: &[u8]) -> Result<BTreeMap<String, Value>, String> {
    let document: Value = serde_json::from_slice(bytes).map_err(|error| error.to_string())?;
    if !document
        .get("success")
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        return Err(document
            .get("message")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_owned());
    }
    let data = document.get("data").cloned().unwrap_or(Value::Null);
    if let Some(config) = data
        .as_object()
        .filter(|config| FIELDS.iter().any(|field| config.contains_key(*field)))
    {
        return validated_ratio_config(config);
    }
    let prices = data
        .as_array()
        .ok_or_else(|| "无法解析上游返回数据".to_owned())?;
    let mut result: BTreeMap<String, Map<String, Value>> = BTreeMap::new();
    for item in prices {
        let Some(model) = item
            .get("model_name")
            .and_then(Value::as_str)
            .filter(|value| !value.is_empty())
        else {
            continue;
        };
        let quota_type = optional_i64(item, "quota_type")?.unwrap_or_default();
        let put = |field: &str,
                   value: Option<Value>,
                   result: &mut BTreeMap<String, Map<String, Value>>| {
            if let Some(value) = value {
                result
                    .entry(field.to_owned())
                    .or_default()
                    .insert(model.to_owned(), value);
            }
        };
        if quota_type == 1 {
            put(
                "model_price",
                Some(json!(number_or_default(item, "model_price")?)),
                &mut result,
            );
        } else {
            put(
                "model_ratio",
                Some(json!(number_or_default(item, "model_ratio")?)),
                &mut result,
            );
            put(
                "completion_ratio",
                Some(json!(number_or_default(item, "completion_ratio")?)),
                &mut result,
            );
        }
        for field in [
            "cache_ratio",
            "create_cache_ratio",
            "image_ratio",
            "audio_ratio",
            "audio_completion_ratio",
        ] {
            put(
                field,
                optional_number(item, field)?.map(|value| json!(value)),
                &mut result,
            );
        }
        if optional_string(item, "billing_mode")?.as_deref() == Some("tiered_expr")
            && optional_string(item, "billing_expr")?
                .as_deref()
                .is_some_and(|value| !value.trim().is_empty())
        {
            put("billing_mode", Some(json!("tiered_expr")), &mut result);
            put(
                "billing_expr",
                optional_string(item, "billing_expr")?.map(Value::String),
                &mut result,
            );
        }
    }
    Ok(result
        .into_iter()
        .map(|(key, value)| (key, Value::Object(value)))
        .collect())
}

fn validated_ratio_config(data: &Map<String, Value>) -> Result<BTreeMap<String, Value>, String> {
    let mut result = BTreeMap::new();
    for field in FIELDS {
        let Some(entries) = data.get(field) else {
            continue;
        };
        let entries = entries
            .as_object()
            .ok_or_else(|| format!("invalid upstream {field}"))?;
        let mut validated = Map::new();
        for (model, value) in entries {
            if model.is_empty() {
                continue;
            }
            if numeric_sync_field(field) {
                let number = value
                    .as_f64()
                    .filter(|value| value.is_finite())
                    .ok_or_else(|| format!("invalid numeric upstream {field}"))?;
                validated.insert(model.clone(), json!(number));
            } else {
                let value = value
                    .as_str()
                    .ok_or_else(|| format!("invalid string upstream {field}"))?;
                validated.insert(model.clone(), Value::String(value.to_owned()));
            }
        }
        if !validated.is_empty() {
            result.insert(field.to_owned(), Value::Object(validated));
        }
    }
    Ok(result)
}

fn numeric_sync_field(field: &str) -> bool {
    !matches!(field, "billing_mode" | "billing_expr")
}

fn optional_i64(item: &Value, field: &str) -> Result<Option<i64>, String> {
    match item.get(field) {
        None | Some(Value::Null) => Ok(None),
        Some(value) => value
            .as_i64()
            .map(Some)
            .ok_or_else(|| format!("invalid upstream {field}")),
    }
}

fn optional_number(item: &Value, field: &str) -> Result<Option<f64>, String> {
    match item.get(field) {
        None | Some(Value::Null) => Ok(None),
        Some(value) => value
            .as_f64()
            .filter(|value| value.is_finite())
            .map(Some)
            .ok_or_else(|| format!("invalid numeric upstream {field}")),
    }
}

fn number_or_default(item: &Value, field: &str) -> Result<f64, String> {
    Ok(optional_number(item, field)?.unwrap_or_default())
}

fn optional_string(item: &Value, field: &str) -> Result<Option<String>, String> {
    match item.get(field) {
        None | Some(Value::Null) => Ok(None),
        Some(value) => value
            .as_str()
            .map(|value| Some(value.to_owned()))
            .ok_or_else(|| format!("invalid string upstream {field}")),
    }
}

fn is_models_dev(url: &str) -> bool {
    reqwest::Url::parse(url).ok().is_some_and(|url| {
        url.host_str()
            .is_some_and(|host| host.eq_ignore_ascii_case("models.dev"))
            && url.path().trim_end_matches('/') == "/api.json"
    })
}
fn round(value: f64) -> f64 {
    (value * 1_000_000.0).round() / 1_000_000.0
}

fn usd_ratio_from_quota_per_unit(value: &str) -> Result<f64, String> {
    value
        .parse::<f64>()
        .ok()
        .map(|quota_per_unit| quota_per_unit / 1_000.0)
        .filter(|ratio| ratio.is_finite() && *ratio > 0.0)
        .ok_or_else(|| "QuotaPerUnit 配置无效".to_owned())
}

fn openrouter_ratios(bytes: &[u8], usd_ratio: f64) -> Result<BTreeMap<String, Value>, String> {
    let document: Value = serde_json::from_slice(bytes)
        .map_err(|error| format!("failed to decode OpenRouter response: {error}"))?;
    let models = document
        .get("data")
        .and_then(Value::as_array)
        .ok_or_else(|| "failed to decode OpenRouter response: missing data".to_owned())?;
    let mut input = Map::new();
    let mut completion = Map::new();
    let mut cache = Map::new();
    for model in models {
        let Some(id) = model
            .get("id")
            .and_then(Value::as_str)
            .filter(|id| !id.is_empty())
        else {
            continue;
        };
        let pricing = model.get("pricing").and_then(Value::as_object);
        let parse = |key: &str| {
            pricing
                .and_then(|pricing| pricing.get(key))
                .and_then(|value| match value {
                    Value::String(value) => value.parse::<f64>().ok(),
                    Value::Number(value) => value.as_f64(),
                    _ => None,
                })
        };
        let (Some(prompt), Some(output)) = (parse("prompt"), parse("completion")) else {
            continue;
        };
        if prompt < 0.0 || output < 0.0 {
            continue;
        }
        if prompt == 0.0 && output == 0.0 {
            input.insert(id.to_owned(), json!(0.0));
            continue;
        }
        if prompt <= 0.0 {
            continue;
        }
        input.insert(id.to_owned(), json!(round(prompt * 1000.0 * usd_ratio)));
        completion.insert(id.to_owned(), json!(round(output / prompt)));
        if let Some(read) = parse("input_cache_read").filter(|read| *read >= 0.0) {
            cache.insert(id.to_owned(), json!(round(read / prompt)));
        }
    }
    let mut result = BTreeMap::new();
    if !input.is_empty() {
        result.insert("model_ratio".to_owned(), Value::Object(input));
    }
    if !completion.is_empty() {
        result.insert("completion_ratio".to_owned(), Value::Object(completion));
    }
    if !cache.is_empty() {
        result.insert("cache_ratio".to_owned(), Value::Object(cache));
    }
    Ok(result)
}

fn models_dev_ratios(bytes: &[u8], usd_ratio: f64) -> Result<BTreeMap<String, Value>, String> {
    let document: Value = serde_json::from_slice(bytes)
        .map_err(|error| format!("failed to decode models.dev response: {error}"))?;
    let providers = document
        .as_object()
        .filter(|providers| !providers.is_empty())
        .ok_or_else(|| "empty models.dev response".to_owned())?;
    #[derive(Clone)]
    struct Candidate {
        provider: String,
        input: f64,
        output: Option<f64>,
        cache: Option<f64>,
    }
    let mut selected: BTreeMap<String, Candidate> = BTreeMap::new();
    for (provider, data) in providers {
        let Some(models) = data.get("models").and_then(Value::as_object) else {
            continue;
        };
        for (name, model) in models {
            let cost = model.get("cost").and_then(Value::as_object);
            let input = cost
                .and_then(|cost| cost.get("input"))
                .and_then(Value::as_f64);
            let output = cost
                .and_then(|cost| cost.get("output"))
                .and_then(Value::as_f64);
            let cache = cost
                .and_then(|cost| cost.get("cache_read"))
                .and_then(Value::as_f64);
            let Some(input) = input.filter(|value| value.is_finite() && *value >= 0.0) else {
                continue;
            };
            if output.is_some_and(|value| !value.is_finite() || value < 0.0)
                || cache.is_some_and(|value| !value.is_finite() || value < 0.0)
                || (input == 0.0 && output.is_some_and(|value| value > 0.0))
            {
                continue;
            }
            let next = Candidate {
                provider: provider.clone(),
                input,
                output,
                cache,
            };
            let replace = match selected.get(name) {
                None => true,
                Some(current) => {
                    let current_non_zero = current.input > 0.0;
                    let next_non_zero = next.input > 0.0;
                    if current_non_zero != next_non_zero {
                        next_non_zero
                    } else if next_non_zero && (next.input - current.input).abs() >= 1e-9 {
                        next.input < current.input
                    } else {
                        next.provider < current.provider
                    }
                }
            };
            if replace {
                selected.insert(name.clone(), next);
            }
        }
    }
    if selected.is_empty() {
        return Err("no valid models.dev pricing entries found".to_owned());
    }
    let mut input = Map::new();
    let mut completion = Map::new();
    let mut cache = Map::new();
    for (name, candidate) in selected {
        if candidate.input == 0.0 {
            input.insert(name, json!(0.0));
            continue;
        }
        input.insert(
            name.clone(),
            json!(round(candidate.input * usd_ratio / 1000.0)),
        );
        if let Some(output) = candidate.output {
            completion.insert(name.clone(), json!(round(output / candidate.input)));
        }
        if let Some(read) = candidate.cache {
            cache.insert(name, json!(round(read / candidate.input)));
        }
    }
    let mut result = BTreeMap::new();
    if !input.is_empty() {
        result.insert("model_ratio".to_owned(), Value::Object(input));
    }
    if !completion.is_empty() {
        result.insert("completion_ratio".to_owned(), Value::Object(completion));
    }
    if !cache.is_empty() {
        result.insert("cache_ratio".to_owned(), Value::Object(cache));
    }
    Ok(result)
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{body::Body, http::Request};
    use tower::ServiceExt;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    #[derive(Default)]
    struct AllowRoot;
    #[async_trait]
    impl RatioSyncAuthorizer for AllowRoot {
        async fn authorize(&self, _: &HeaderMap) -> Result<RatioSyncIdentity, &'static str> {
            Ok(RatioSyncIdentity { role: ROOT_ROLE })
        }
    }
    #[derive(Default)]
    struct MemoryRepository;
    #[async_trait]
    impl RatioSyncRepository for MemoryRepository {
        async fn syncable_channels(&self) -> Result<Vec<SyncableChannel>, String> {
            Ok(vec![SyncableChannel {
                id: 7,
                name: "one".to_owned(),
                base_url: "https://one.example".to_owned(),
                status: 1,
                channel_type: 1,
            }])
        }
        async fn channels_by_ids(&self, _: &[i64]) -> Result<Vec<UpstreamTarget>, String> {
            Ok(vec![])
        }
        async fn pricing_snapshot(&self) -> Result<RatioSyncPricingSnapshot, String> {
            Ok(RatioSyncPricingSnapshot {
                local_pricing: BTreeMap::new(),
                usd_ratio: 750.0,
            })
        }
    }
    #[tokio::test]
    async fn channels_are_root_gated_and_keep_legacy_presets() -> TestResult {
        let app = ratio_sync_router(RatioSyncHttpState::new(
            Arc::new(MemoryRepository),
            Arc::new(TestInstanceDisabledRatioSyncUpstream),
            Arc::new(AllowRoot),
        ));
        let request = Request::builder()
            .uri("/api/ratio_sync/channels")
            .header("authorization", "Bearer test")
            .body(Body::empty())?;
        let response = app.oneshot(request).await?;
        assert_eq!(response.status(), StatusCode::OK);
        let body = axum::body::to_bytes(response.into_body(), 4096).await?;
        let body: Value = serde_json::from_slice(&body)?;
        assert_eq!(body["data"].as_array().map(Vec::len), Some(3));
        Ok(())
    }
    #[tokio::test]
    async fn disabled_test_instance_never_performs_outbound_fetch() -> TestResult {
        let app = ratio_sync_router(RatioSyncHttpState::new(
            Arc::new(MemoryRepository),
            Arc::new(TestInstanceDisabledRatioSyncUpstream),
            Arc::new(AllowRoot),
        ));
        let request = Request::builder()
            .method("POST")
            .uri("/api/ratio_sync/fetch")
            .header("authorization", "Bearer test")
            .header("content-type", "application/json")
            .body(Body::from(
                r#"{"upstreams":[{"name":"unsafe","base_url":"https://example.invalid"}]}"#,
            ))?;
        let response = app.oneshot(request).await?;
        let body = axum::body::to_bytes(response.into_body(), 4096).await?;
        let body: Value = serde_json::from_slice(&body)?;
        assert_eq!(body["success"], true);
        assert_eq!(body["data"]["test_results"][0]["status"], "error");
        Ok(())
    }

    #[derive(Default)]
    struct DenyRoot;
    #[async_trait]
    impl RatioSyncAuthorizer for DenyRoot {
        async fn authorize(&self, _: &HeaderMap) -> Result<RatioSyncIdentity, &'static str> {
            Err("Unauthorized, invalid access token")
        }
    }

    #[tokio::test]
    async fn root_auth_precedes_json_rejection() -> TestResult {
        let app = ratio_sync_router(RatioSyncHttpState::new(
            Arc::new(MemoryRepository),
            Arc::new(TestInstanceDisabledRatioSyncUpstream),
            Arc::new(DenyRoot),
        ));
        let request = Request::builder()
            .method("POST")
            .uri("/api/ratio_sync/fetch")
            .header("content-type", "application/json")
            .body(Body::from("{not json"))?;
        let response = app.oneshot(request).await?;
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        Ok(())
    }

    #[test]
    fn provider_urls_require_public_https_and_never_follow_redirects() {
        let target = |base_url: &str, endpoint: &str| UpstreamTarget {
            id: 0,
            name: "fixture".to_owned(),
            base_url: base_url.to_owned(),
            endpoint: endpoint.to_owned(),
            api_key: None,
        };
        assert!(upstream_url(&target("http://example.com", "")).is_err());
        assert!(upstream_url(&target("https://127.0.0.1", "")).is_err());
        assert!(upstream_url(&target("https://example.com", "http://example.org")).is_err());
        assert!(upstream_url(&target("https://example.com", "")).is_ok());
    }

    #[test]
    fn pricing_payload_validation_rejects_non_numeric_ratio_values() {
        assert!(
            pricing_ratios(br#"{"success":true,"data":{"model_ratio":{"model":"not-a-number"}}}"#)
                .is_err()
        );
        assert!(pricing_ratios(
            br#"{"success":true,"data":[{"model_name":"model","quota_type":0,"model_ratio":"not-a-number"}]}"#
        )
        .is_err());
    }

    #[test]
    fn difference_merge_omits_globally_identical_provider_and_marks_default_pricing_untrusted() {
        let local = BTreeMap::from([(
            "model_ratio".to_owned(),
            json!({"model": 1.0, "other": 1.0}),
        )]);
        let unchanged = BTreeMap::from([(
            "model_ratio".to_owned(),
            json!({"model": 1.0, "other": 1.0}),
        )]);
        let changed = BTreeMap::from([
            (
                "model_ratio".to_owned(),
                json!({"model": 37.5, "other": 2.0}),
            ),
            (
                "completion_ratio".to_owned(),
                json!({"model": 1.0, "other": 2.0}),
            ),
        ]);
        let differences = build_differences(
            &local,
            &[
                ("same".to_owned(), unchanged),
                ("changed".to_owned(), changed),
            ],
        );
        let item = &differences["model"]["model_ratio"];
        assert_eq!(item["upstreams"], json!({"changed": 37.5}));
        assert_eq!(item["confidence"], json!({"changed": false}));
    }

    #[test]
    fn upstream_conversions_use_the_snapshot_ratio_instead_of_a_fixed_constant() -> TestResult {
        assert_eq!(
            usd_ratio_from_quota_per_unit("750000"),
            Ok(750.0),
            "the configured non-default quota base must flow into both conversions"
        );
        assert!(usd_ratio_from_quota_per_unit("0").is_err());

        let openrouter = openrouter_ratios(
            br#"{"data":[{"id":"or-model","pricing":{"prompt":"0.000002","completion":"0.000004"}}]}"#,
            750.0,
        )
        .map_err(std::io::Error::other)?;
        assert_eq!(openrouter["model_ratio"]["or-model"], json!(1.5));
        assert_eq!(openrouter["completion_ratio"]["or-model"], json!(2.0));

        let models_dev = models_dev_ratios(
            br#"{"provider":{"models":{"dev-model":{"cost":{"input":2.0,"output":4.0}}}}}"#,
            750.0,
        )
        .map_err(std::io::Error::other)?;
        assert_eq!(models_dev["model_ratio"]["dev-model"], json!(1.5));
        assert_eq!(models_dev["completion_ratio"]["dev-model"], json!(2.0));
        Ok(())
    }
}
