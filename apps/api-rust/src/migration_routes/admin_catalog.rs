//! Frozen administrator catalog route candidates.
//!
//! This module is deliberately unmounted. It gives the model, vendor, prefill
//! group, and redemption route families one HTTP boundary while the final
//! PostgreSQL, Valkey, and upstream-sync contracts are captured separately.

use std::{
    collections::{BTreeMap, HashMap, VecDeque},
    sync::{Arc, Mutex},
    time::{SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::Bytes,
    extract::{Path, Query, State},
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
    routing::{delete, get, post},
};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{PgPool, Row};

use crate::auth::{DashboardAuth, UserAuthPolicyError, enforce_user_auth_view};

const ADMIN_ROLE: i64 = 10;

/// Server-validated administrator identity passed to catalog operations.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct AdminCatalogActor {
    /// Durable user identifier from the dashboard authentication service.
    pub user_id: i64,
    /// Legacy role value; administrators have a value of at least ten.
    pub role: i64,
}

/// Authentication boundary owned by the listener integration.
#[async_trait]
pub trait AdminCatalogAuthorizer: Send + Sync {
    /// Verifies the request credential and returns only a server-derived actor.
    async fn authorize(&self, headers: &HeaderMap) -> Result<AdminCatalogActor, CatalogError>;
}

/// Production authorization adapter using signed dashboard credentials.
#[derive(Clone)]
pub struct DashboardAdminCatalogAuthorizer {
    auth: Arc<dyn DashboardAuth>,
}

impl DashboardAdminCatalogAuthorizer {
    #[must_use]
    pub fn new(auth: Arc<dyn DashboardAuth>) -> Self {
        Self { auth }
    }
}

#[async_trait]
impl AdminCatalogAuthorizer for DashboardAdminCatalogAuthorizer {
    async fn authorize(&self, headers: &HeaderMap) -> Result<AdminCatalogActor, CatalogError> {
        // Go mounts this administrator catalogue below the API-wide
        // ConsoleAccessGate.  Anonymous, malformed, expired, revoked, and
        // pre-activation credentials are therefore concealed as a generic
        // route miss before AdminAuth can emit a 401/403 envelope.
        let token = dashboard_credential(headers).ok_or(CatalogError::ConsoleNotFound)?;
        let user = self
            .auth
            .self_user_view_for_optional(SecretString::from(token.to_owned()))
            .await
            .map_err(|_| CatalogError::ConsoleNotFound)?;
        if !user.developer_access_granted {
            return Err(CatalogError::ConsoleNotFound);
        }
        if let Err(error) = enforce_user_auth_view(&user) {
            return Err(match error {
                UserAuthPolicyError::UserDisabled | UserAuthPolicyError::InvalidUserInfo => {
                    CatalogError::Unauthorized
                }
                UserAuthPolicyError::InsufficientPrivilege => CatalogError::Forbidden,
            });
        }
        Ok(AdminCatalogActor {
            user_id: user.id,
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

/// A normalized operation from the frozen catalog route family.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum CatalogOperation {
    ListModels,
    CreateModel,
    UpdateModel,
    GetModel,
    DeleteModel,
    MissingModels,
    SearchModels,
    SyncUpstream,
    PreviewUpstreamSync,
    ListVendors,
    CreateVendor,
    UpdateVendor,
    GetVendor,
    DeleteVendor,
    SearchVendors,
    ListPrefillGroups,
    CreatePrefillGroup,
    UpdatePrefillGroup,
    DeletePrefillGroup,
    ListRedemptions,
    CreateRedemption,
    UpdateRedemption,
    GetRedemption,
    DeleteRedemption,
    DeleteInvalidRedemptions,
    SearchRedemptions,
}

/// Request data after authorization and basic legacy route validation.
#[derive(Clone, Debug, PartialEq)]
pub struct CatalogCall {
    /// The selected route operation.
    pub operation: CatalogOperation,
    /// Identity validated by the authorizer, never request JSON.
    pub actor: AdminCatalogActor,
    /// Numeric resource id for item routes.
    pub resource_id: Option<i64>,
    /// Query values or JSON body, preserving legacy names as strings/JSON.
    pub input: Value,
}

/// Errors translated to the legacy JSON envelope.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum CatalogError {
    /// The API-wide ConsoleAccessGate intentionally hides the discovery
    /// surface until a dashboard account has active developer access.
    ConsoleNotFound,
    /// No valid dashboard credential was supplied.
    Unauthorized,
    /// A valid identity lacks the administrator role.
    Forbidden,
    /// The request cannot be represented by the legacy handler.
    Invalid(&'static str),
    /// The requested entity does not exist.
    NotFound,
    /// A durable dependency could not complete the operation.
    Unavailable,
    /// A captured legacy business error safe to show to the dashboard.
    Rejected(String),
}

impl CatalogError {
    fn response(self) -> Response {
        match self {
            Self::ConsoleNotFound => {
                let mut response =
                    (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response();
                response.headers_mut().insert(
                    axum::http::header::CONTENT_TYPE,
                    axum::http::HeaderValue::from_static("application/json; charset=utf-8"),
                );
                response
            }
            Self::Unauthorized => failure(
                StatusCode::UNAUTHORIZED,
                "Unauthorized, invalid access token",
                Some("AUTH_UNAUTHORIZED"),
            ),
            Self::Forbidden => failure(
                StatusCode::FORBIDDEN,
                "管理员权限不足",
                Some("AUTH_INSUFFICIENT_PRIVILEGE"),
            ),
            Self::Invalid(message) => failure(StatusCode::OK, message, None),
            Self::NotFound => failure(StatusCode::OK, "资源不存在", None),
            Self::Unavailable => failure(StatusCode::OK, "目录操作失败", None),
            Self::Rejected(message) => failure(StatusCode::OK, message, None),
        }
    }
}

/// Boundary for durable catalog storage and upstream synchronization.
#[async_trait]
pub trait AdminCatalogProvider: Send + Sync {
    /// Executes an authorized, normalized catalog operation.
    async fn execute(&self, call: CatalogCall) -> Result<Value, CatalogError>;
}

/// Small in-memory provider for route tests and local contract probes.
///
/// It records calls in order and returns queued responses before its default
/// response. Production wiring must use a durable provider instead.
#[derive(Clone)]
pub struct MemoryCatalogProvider {
    calls: Arc<Mutex<Vec<CatalogCall>>>,
    responses: Arc<Mutex<VecDeque<Result<Value, CatalogError>>>>,
    default_response: Value,
}

impl Default for MemoryCatalogProvider {
    fn default() -> Self {
        Self::new(json!({}))
    }
}

impl MemoryCatalogProvider {
    #[must_use]
    pub fn new(default_response: Value) -> Self {
        Self {
            calls: Arc::new(Mutex::new(Vec::new())),
            responses: Arc::new(Mutex::new(VecDeque::new())),
            default_response,
        }
    }

    /// Queues the next provider result for deterministic route tests.
    pub fn push_response(&self, response: Result<Value, CatalogError>) -> Result<(), CatalogError> {
        self.responses
            .lock()
            .map_err(|_| CatalogError::Unavailable)?
            .push_back(response);
        Ok(())
    }

    /// Returns a snapshot of normalized calls recorded so far.
    pub fn calls(&self) -> Result<Vec<CatalogCall>, CatalogError> {
        self.calls
            .lock()
            .map(|calls| calls.clone())
            .map_err(|_| CatalogError::Unavailable)
    }
}

#[async_trait]
impl AdminCatalogProvider for MemoryCatalogProvider {
    async fn execute(&self, call: CatalogCall) -> Result<Value, CatalogError> {
        self.calls
            .lock()
            .map_err(|_| CatalogError::Unavailable)?
            .push(call);
        let queued = self
            .responses
            .lock()
            .map_err(|_| CatalogError::Unavailable)?
            .pop_front();
        match queued {
            Some(response) => response,
            None => Ok(self.default_response.clone()),
        }
    }
}

/// Upstream metadata needed by the two frozen synchronization routes.
#[derive(Clone, Debug, Deserialize)]
pub struct UpstreamCatalog {
    #[serde(default)]
    pub models: Vec<UpstreamModel>,
    #[serde(default)]
    pub vendors: Vec<UpstreamVendor>,
    pub models_url: String,
    pub vendors_url: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct UpstreamModel {
    #[serde(default)]
    pub model_name: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub icon: String,
    #[serde(default)]
    pub tags: String,
    #[serde(default)]
    pub vendor_name: String,
    #[serde(default)]
    pub status: i64,
    #[serde(default)]
    pub name_rule: i64,
}

#[derive(Clone, Debug, Deserialize)]
pub struct UpstreamVendor {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub icon: String,
    #[serde(default)]
    pub status: i64,
}

/// Network boundary for the official metadata feed.
#[async_trait]
pub trait CatalogUpstream: Send + Sync {
    /// Fetches the models feed; a vendor-feed failure is represented by an
    /// empty vendor list to preserve the frozen Go route's best-effort policy.
    async fn fetch(&self, locale: &str) -> Result<UpstreamCatalog, CatalogError>;
}

/// HTTPS implementation of the official metadata-feed boundary.
#[derive(Clone)]
pub struct HttpCatalogUpstream {
    client: reqwest::Client,
    base_url: reqwest::Url,
}

impl HttpCatalogUpstream {
    pub fn new(client: reqwest::Client, base_url: reqwest::Url) -> Self {
        Self { client, base_url }
    }

    pub fn official(client: reqwest::Client) -> Result<Self, CatalogError> {
        let base_url = reqwest::Url::parse("https://basellm.github.io/llm-metadata/")
            .map_err(|_| CatalogError::Unavailable)?;
        Ok(Self::new(client, base_url))
    }

    fn urls(&self, locale: &str) -> Result<(reqwest::Url, reqwest::Url), CatalogError> {
        let locale = locale.trim().to_ascii_lowercase();
        let prefix = match locale.as_str() {
            "" => "api/newapi/".to_owned(),
            "en" | "ja" => format!("api/i18n/{locale}/newapi/"),
            // The upstream feed publishes one Chinese dataset under /i18n/zh/.
            "zh" | "zh-cn" | "zh-tw" => "api/i18n/zh/newapi/".to_owned(),
            _ => return Err(CatalogError::Invalid("locale 参数错误")),
        };
        let models = self
            .base_url
            .join(&format!("{prefix}models.json"))
            .map_err(|_| CatalogError::Unavailable)?;
        let vendors = self
            .base_url
            .join(&format!("{prefix}vendors.json"))
            .map_err(|_| CatalogError::Unavailable)?;
        Ok((models, vendors))
    }
}

#[async_trait]
impl CatalogUpstream for HttpCatalogUpstream {
    async fn fetch(&self, locale: &str) -> Result<UpstreamCatalog, CatalogError> {
        let (models_url, vendors_url) = self.urls(locale)?;
        let models_response = self
            .client
            .get(models_url.clone())
            .send()
            .await
            .map_err(|_| CatalogError::Rejected("获取上游模型失败".to_owned()))?;
        if !models_response.status().is_success() {
            return Err(CatalogError::Rejected("获取上游模型失败".to_owned()));
        }
        let models_body = models_response
            .json::<Value>()
            .await
            .map_err(|_| CatalogError::Rejected("获取上游模型失败".to_owned()))?;
        let models = decode_upstream(models_body)
            .map_err(|_| CatalogError::Rejected("获取上游模型失败".to_owned()))?;
        let vendors = match self.client.get(vendors_url.clone()).send().await {
            Ok(response) if response.status().is_success() => {
                match response.json::<Value>().await {
                    Ok(value) => optional_upstream_entries(decode_upstream(value)),
                    Err(_) => Vec::new(),
                }
            }
            _ => Vec::new(),
        };
        Ok(UpstreamCatalog {
            models,
            vendors,
            models_url: models_url.to_string(),
            vendors_url: vendors_url.to_string(),
        })
    }
}

fn decode_upstream<T: for<'de> Deserialize<'de>>(
    value: Value,
) -> Result<Vec<T>, serde_json::Error> {
    match value {
        Value::Array(_) => serde_json::from_value(value),
        Value::Object(mut object) => match object.remove("data") {
            Some(data) => serde_json::from_value(data),
            None => serde_json::from_value(Value::Array(Vec::new())),
        },
        _ => serde_json::from_value(Value::Array(Vec::new())),
    }
}

/// A malformed vendor feed is optional for the frozen upstream-sync contract.
fn optional_upstream_entries<T>(result: Result<Vec<T>, serde_json::Error>) -> Vec<T> {
    result.ok().into_iter().flatten().collect()
}

/// PostgreSQL-backed implementation for the frozen catalog family.
///
/// Catalog rows are authoritative in PostgreSQL. The upstream is only used by
/// the explicit sync/preview routes and is never contacted by ordinary CRUD.
#[derive(Clone)]
pub struct PgCatalogProvider {
    pg: PgPool,
    upstream: Arc<dyn CatalogUpstream>,
}

impl PgCatalogProvider {
    pub fn new(pg: PgPool, upstream: Arc<dyn CatalogUpstream>) -> Self {
        Self { pg, upstream }
    }

    pub fn with_official_upstream(
        pg: PgPool,
        client: reqwest::Client,
    ) -> Result<Self, CatalogError> {
        Ok(Self::new(
            pg,
            Arc::new(HttpCatalogUpstream::official(client)?),
        ))
    }
}

#[async_trait]
impl AdminCatalogProvider for PgCatalogProvider {
    async fn execute(&self, call: CatalogCall) -> Result<Value, CatalogError> {
        let result = match call.operation {
            CatalogOperation::ListModels => list_models_pg(&self.pg, &call.input, false).await,
            CatalogOperation::SearchModels => list_models_pg(&self.pg, &call.input, true).await,
            CatalogOperation::CreateModel => create_model_pg(&self.pg, &call.input).await,
            CatalogOperation::UpdateModel => update_model_pg(&self.pg, &call.input).await,
            CatalogOperation::GetModel => {
                get_model_pg(&self.pg, required_resource_id(&call)?).await
            }
            CatalogOperation::DeleteModel => {
                delete_by_id(&self.pg, "models", required_resource_id(&call)?).await
            }
            CatalogOperation::MissingModels => missing_models_pg(&self.pg).await,
            CatalogOperation::SyncUpstream => {
                sync_upstream_pg(&self.pg, self.upstream.as_ref(), &call.input).await
            }
            CatalogOperation::PreviewUpstreamSync => {
                preview_upstream_pg(&self.pg, self.upstream.as_ref(), &call.input).await
            }
            CatalogOperation::ListVendors => list_vendors_pg(&self.pg, &call.input, false).await,
            CatalogOperation::SearchVendors => list_vendors_pg(&self.pg, &call.input, true).await,
            CatalogOperation::CreateVendor => create_vendor_pg(&self.pg, &call.input).await,
            CatalogOperation::UpdateVendor => update_vendor_pg(&self.pg, &call.input).await,
            CatalogOperation::GetVendor => {
                get_vendor_pg(&self.pg, required_resource_id(&call)?).await
            }
            CatalogOperation::DeleteVendor => {
                delete_by_id(&self.pg, "vendors", required_resource_id(&call)?).await
            }
            CatalogOperation::ListPrefillGroups => {
                list_prefill_groups_pg(&self.pg, &call.input).await
            }
            CatalogOperation::CreatePrefillGroup => {
                create_prefill_group_pg(&self.pg, &call.input).await
            }
            CatalogOperation::UpdatePrefillGroup => {
                update_prefill_group_pg(&self.pg, &call.input).await
            }
            CatalogOperation::DeletePrefillGroup => {
                delete_by_id(&self.pg, "prefill_groups", required_resource_id(&call)?).await
            }
            CatalogOperation::ListRedemptions => {
                list_redemptions_pg(&self.pg, &call.input, false).await
            }
            CatalogOperation::SearchRedemptions => {
                list_redemptions_pg(&self.pg, &call.input, true).await
            }
            CatalogOperation::CreateRedemption => {
                create_redemptions_pg(&self.pg, call.actor, &call.input).await
            }
            CatalogOperation::UpdateRedemption => update_redemption_pg(&self.pg, &call.input).await,
            CatalogOperation::GetRedemption => {
                get_redemption_pg(&self.pg, required_resource_id(&call)?).await
            }
            CatalogOperation::DeleteRedemption => {
                delete_by_id(&self.pg, "redemptions", required_resource_id(&call)?).await
            }
            CatalogOperation::DeleteInvalidRedemptions => {
                delete_invalid_redemptions_pg(&self.pg).await
            }
        };
        // Go's AdminAuth middleware records a type=3 operation audit for every
        // authorized catalog write (including business failures).  The
        // redemption create handler has one special success-only audit shape;
        // all other writes use the middleware route/status envelope.  Keep this
        // best-effort, just like Go's asynchronous audit writer: an observability
        // failure must never change the catalog response.
        self.record_audit(&call, &result).await;
        result
    }
}

fn catalog_audit_route(
    operation: CatalogOperation,
) -> Option<(&'static str, &'static str, &'static str)> {
    Some(match operation {
        CatalogOperation::CreateModel => ("POST", "/api/models/", "model.create"),
        CatalogOperation::UpdateModel => ("PUT", "/api/models/", "model.update"),
        CatalogOperation::DeleteModel => ("DELETE", "/api/models/:id", "model.delete"),
        CatalogOperation::SyncUpstream => {
            ("POST", "/api/models/sync_upstream", "model.sync_upstream")
        }
        CatalogOperation::CreateVendor => ("POST", "/api/vendors/", "vendor.create"),
        CatalogOperation::UpdateVendor => ("PUT", "/api/vendors/", "vendor.update"),
        CatalogOperation::DeleteVendor => ("DELETE", "/api/vendors/:id", "vendor.delete"),
        CatalogOperation::CreatePrefillGroup => {
            ("POST", "/api/prefill_group/", "prefill_group.create")
        }
        CatalogOperation::UpdatePrefillGroup => {
            ("PUT", "/api/prefill_group/", "prefill_group.update")
        }
        CatalogOperation::DeletePrefillGroup => {
            ("DELETE", "/api/prefill_group/:id", "prefill_group.delete")
        }
        CatalogOperation::CreateRedemption => ("POST", "/api/redemption/", "redemption.create"),
        CatalogOperation::UpdateRedemption => ("PUT", "/api/redemption/", "redemption.update"),
        CatalogOperation::DeleteRedemption => {
            ("DELETE", "/api/redemption/:id", "redemption.delete")
        }
        CatalogOperation::DeleteInvalidRedemptions => (
            "DELETE",
            "/api/redemption/invalid",
            "redemption.delete_invalid",
        ),
        _ => return None,
    })
}

async fn catalog_log_quota(pg: &PgPool, quota: i64) -> String {
    let quota_per_unit =
        sqlx::query_scalar::<_, String>("SELECT value FROM options WHERE key = 'QuotaPerUnit'")
            .fetch_optional(pg)
            .await
            .ok()
            .flatten()
            .and_then(|value| value.parse::<f64>().ok())
            .filter(|value| value.is_finite() && *value > 0.0)
            .unwrap_or(500_000.0);
    format!("＄{:.6} 额度", quota as f64 / quota_per_unit)
}

impl PgCatalogProvider {
    async fn record_audit(&self, call: &CatalogCall, result: &Result<Value, CatalogError>) {
        let Some((method, route, action)) = catalog_audit_route(call.operation) else {
            return;
        };
        let success = result.is_ok();
        let resource_params = call
            .resource_id
            .map(|id| json!({"id": id.to_string()}))
            .unwrap_or_else(|| json!({}));
        let path = call.resource_id.map_or_else(
            || route.to_owned(),
            |id| {
                format!(
                    "{route_base}{id}",
                    route_base = route.trim_end_matches(":id")
                )
            },
        );

        let (content, params, audit_info) =
            if call.operation == CatalogOperation::CreateRedemption && success {
                let name = text(&call.input, "name");
                let count = integer(&call.input, "count", 0);
                let quota = integer(&call.input, "quota", 100);
                let quota_display = catalog_log_quota(&self.pg, quota).await;
                (
                    format!("Created {count} redemption codes named {name} ({quota_display} each)"),
                    json!({"name": name, "count": count, "quota": quota_display}),
                    None,
                )
            } else {
                let mut audit_info = json!({
                    "method": method,
                    "route": route,
                    "path": path,
                    "status": 200,
                    "success": success,
                });
                if let Some(params) = resource_params.as_object()
                    && !params.is_empty() {
                        audit_info["params"] = Value::Object(params.clone());
                    }
                (
                    format!("{method} {route}"),
                    resource_params,
                    Some(audit_info),
                )
            };

        let username = sqlx::query_scalar::<_, String>("SELECT username FROM users WHERE id = $1")
            .bind(call.actor.user_id)
            .fetch_optional(&self.pg)
            .await
            .ok()
            .flatten()
            .unwrap_or_default();
        let mut other = json!({
            "op": {"action": action, "params": params},
            "admin_info": {
                "admin_id": call.actor.user_id,
                "admin_username": username,
                "admin_role": call.actor.role,
                "auth_method": "session",
            },
        });
        if let Some(audit_info) = audit_info {
            other["audit_info"] = audit_info;
        }
        let _ = sqlx::query(
            "INSERT INTO logs (user_id, created_at, type, content, username, ip, other) VALUES ($1, EXTRACT(EPOCH FROM NOW())::BIGINT, 3, $2, $3, '', $4)",
        )
        .bind(call.actor.user_id)
        .bind(content)
        .bind(username)
        .bind(other.to_string())
        .execute(&self.pg)
        .await;
    }
}

fn is_zero_i64(value: &i64) -> bool {
    *value == 0
}

fn is_empty_vec<T>(value: &[T]) -> bool {
    value.is_empty()
}

#[derive(Clone, Debug, Serialize)]
struct CatalogBoundChannel {
    name: String,
    #[serde(rename = "type")]
    channel_type: i64,
}

#[derive(Serialize)]
struct CatalogModel {
    id: i64,
    model_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    description: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    icon: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    tags: String,
    #[serde(skip_serializing_if = "is_zero_i64")]
    vendor_id: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    endpoints: String,
    status: i64,
    sync_official: i64,
    created_time: i64,
    updated_time: i64,
    name_rule: i64,
    #[serde(skip_serializing_if = "is_empty_vec")]
    bound_channels: Vec<CatalogBoundChannel>,
    #[serde(skip_serializing_if = "is_empty_vec")]
    enable_groups: Vec<String>,
    #[serde(skip_serializing_if = "is_empty_vec")]
    quota_types: Vec<i64>,
    #[serde(skip_serializing_if = "is_empty_vec")]
    matched_models: Vec<String>,
    #[serde(skip_serializing_if = "is_zero_i64")]
    matched_count: i64,
}

#[derive(Serialize)]
struct CatalogVendor {
    id: i64,
    name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    description: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    icon: String,
    status: i64,
    created_time: i64,
    updated_time: i64,
}

#[derive(Serialize)]
struct CatalogPrefillGroup {
    id: i64,
    name: String,
    #[serde(rename = "type")]
    group_type: String,
    items: Value,
    #[serde(skip_serializing_if = "String::is_empty")]
    description: String,
    created_time: i64,
    updated_time: i64,
}

#[derive(Serialize)]
struct CatalogRedemption {
    id: i64,
    user_id: i64,
    key: String,
    status: i64,
    name: String,
    quota: i64,
    count: i64,
    created_time: i64,
    redeemed_time: i64,
    used_user_id: i64,
    expired_time: i64,
    #[serde(rename = "DeletedAt")]
    deleted_at: Value,
}

fn model_from_row(row: &sqlx::postgres::PgRow) -> Result<CatalogModel, CatalogError> {
    Ok(CatalogModel {
        id: row.try_get("id").map_err(database_error)?,
        model_name: row.try_get("model_name").map_err(database_error)?,
        description: row.try_get("description").map_err(database_error)?,
        icon: row.try_get("icon").map_err(database_error)?,
        tags: row.try_get("tags").map_err(database_error)?,
        vendor_id: row.try_get("vendor_id").map_err(database_error)?,
        endpoints: row.try_get("endpoints").map_err(database_error)?,
        status: row.try_get("status").map_err(database_error)?,
        sync_official: row.try_get("sync_official").map_err(database_error)?,
        created_time: row.try_get("created_time").map_err(database_error)?,
        updated_time: row.try_get("updated_time").map_err(database_error)?,
        name_rule: row.try_get("name_rule").map_err(database_error)?,
        bound_channels: Vec::new(),
        enable_groups: Vec::new(),
        quota_types: Vec::new(),
        matched_models: Vec::new(),
        matched_count: 0,
    })
}

fn vendor_from_row(row: &sqlx::postgres::PgRow) -> Result<CatalogVendor, CatalogError> {
    Ok(CatalogVendor {
        id: row.try_get("id").map_err(database_error)?,
        name: row.try_get("name").map_err(database_error)?,
        description: row.try_get("description").map_err(database_error)?,
        icon: row.try_get("icon").map_err(database_error)?,
        status: row.try_get("status").map_err(database_error)?,
        created_time: row.try_get("created_time").map_err(database_error)?,
        updated_time: row.try_get("updated_time").map_err(database_error)?,
    })
}

fn prefill_group_from_row(
    row: &sqlx::postgres::PgRow,
) -> Result<CatalogPrefillGroup, CatalogError> {
    Ok(CatalogPrefillGroup {
        id: row.try_get("id").map_err(database_error)?,
        name: row.try_get("name").map_err(database_error)?,
        group_type: row.try_get("type").map_err(database_error)?,
        items: row.try_get("items").map_err(database_error)?,
        description: row.try_get("description").map_err(database_error)?,
        created_time: row.try_get("created_time").map_err(database_error)?,
        updated_time: row.try_get("updated_time").map_err(database_error)?,
    })
}

fn redemption_from_row(row: &sqlx::postgres::PgRow) -> Result<CatalogRedemption, CatalogError> {
    Ok(CatalogRedemption {
        id: row.try_get("id").map_err(database_error)?,
        user_id: row.try_get("user_id").map_err(database_error)?,
        key: row.try_get("key").map_err(database_error)?,
        status: row.try_get("status").map_err(database_error)?,
        name: row.try_get("name").map_err(database_error)?,
        quota: row.try_get("quota").map_err(database_error)?,
        count: 0,
        created_time: row.try_get("created_time").map_err(database_error)?,
        redeemed_time: row.try_get("redeemed_time").map_err(database_error)?,
        used_user_id: row.try_get("used_user_id").map_err(database_error)?,
        expired_time: row.try_get("expired_time").map_err(database_error)?,
        // GORM's gorm.DeletedAt has no json tag in the frozen Go model. A
        // non-deleted row therefore serializes this zero value as null.
        deleted_at: Value::Null,
    })
}

const MODEL_COLUMNS: &str = "id, model_name, COALESCE(description, '') AS description, COALESCE(icon, '') AS icon, COALESCE(tags, '') AS tags, COALESCE(vendor_id, 0) AS vendor_id, COALESCE(endpoints, '') AS endpoints, COALESCE(status, 1) AS status, COALESCE(sync_official, 1) AS sync_official, COALESCE(created_time, 0) AS created_time, COALESCE(updated_time, 0) AS updated_time, COALESCE(name_rule, 0) AS name_rule";
const VENDOR_COLUMNS: &str = "id, name, COALESCE(description, '') AS description, COALESCE(icon, '') AS icon, COALESCE(status, 1) AS status, COALESCE(created_time, 0) AS created_time, COALESCE(updated_time, 0) AS updated_time";
const PREFILL_COLUMNS: &str = "id, name, type, COALESCE(items, 'null'::json) AS items, COALESCE(description, '') AS description, COALESCE(created_time, 0) AS created_time, COALESCE(updated_time, 0) AS updated_time";
const REDEMPTION_COLUMNS: &str = "id, COALESCE(user_id, 0) AS user_id, COALESCE(BTRIM(key), '') AS key, COALESCE(status, 1) AS status, COALESCE(name, '') AS name, COALESCE(quota, 100) AS quota, COALESCE(created_time, 0) AS created_time, COALESCE(redeemed_time, 0) AS redeemed_time, COALESCE(used_user_id, 0) AS used_user_id, COALESCE(expired_time, 0) AS expired_time";

fn required_resource_id(call: &CatalogCall) -> Result<i64, CatalogError> {
    call.resource_id.ok_or(CatalogError::Invalid("id 参数错误"))
}

fn database_error(_: sqlx::Error) -> CatalogError {
    CatalogError::Unavailable
}

fn unix_now() -> Result<i64, CatalogError> {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_secs() as i64)
        .map_err(|_| CatalogError::Unavailable)
}

fn text(input: &Value, name: &str) -> String {
    input
        .get(name)
        .and_then(Value::as_str)
        .map_or_else(String::new, |value| value.trim().to_owned())
}

fn integer(input: &Value, name: &str, default: i64) -> i64 {
    input
        .get(name)
        .and_then(Value::as_i64)
        .map_or(default, |value| value)
}

fn page(input: &Value) -> (i64, i64, i64) {
    // Match common.GetPageQuery: a zero/invalid page becomes one, while a
    // non-zero negative page is preserved for the response (GORM simply
    // omits its negative OFFSET clause).
    let raw_page = text(input, "p").parse::<i64>().unwrap_or(0);
    let number = if raw_page < 1 {
        if raw_page == 0 { 1 } else { raw_page }
    } else {
        raw_page
    };

    // The legacy dashboard accepts page_size first, then the historical ps
    // and token-size aliases. A negative size is intentional: GORM treats it
    // as an unlimited query rather than emitting LIMIT -1.
    let mut size = text(input, "page_size").parse::<i64>().unwrap_or(0);
    if size == 0 {
        size = text(input, "ps").parse::<i64>().unwrap_or(0);
    }
    if size == 0 {
        size = text(input, "size").parse::<i64>().unwrap_or(0);
    }
    if size == 0 {
        size = 10;
    }
    if size > 100 {
        size = 100;
    }
    let offset = number.wrapping_sub(1).wrapping_mul(size);
    (number, size, offset)
}

fn normalize_status_filter(value: &str) -> String {
    let normalized = value.trim().to_ascii_lowercase();
    match normalized.as_str() {
        "" | "all" => String::new(),
        "enabled" | "1" => "1".to_owned(),
        "disabled" | "0" => "0".to_owned(),
        _ if normalized.parse::<i64>().is_ok() => normalized,
        // Go ignores unknown status values instead of passing them to a
        // numeric SQL predicate (notably, yes/no are sync-only aliases).
        _ => String::new(),
    }
}

fn normalize_sync_filter(value: &str) -> String {
    let normalized = value.trim().to_ascii_lowercase();
    match normalized.as_str() {
        "" | "all" => String::new(),
        "yes" | "1" => "1".to_owned(),
        "no" | "0" => "0".to_owned(),
        _ if normalized.parse::<i64>().is_ok() => normalized,
        _ => String::new(),
    }
}

fn catalog_endpoint_types_for_channel(channel_type: i64, model_name: &str) -> Vec<&'static str> {
    let mut endpoint_types = match channel_type {
        38 => vec!["jina-rerank"],
        14 | 33 => vec!["anthropic", "openai"],
        24 | 41 => vec!["gemini", "openai"],
        20 => vec!["openai"],
        48 => vec!["openai", "openai-response"],
        55 => vec!["openai-video"],
        57 => vec![
            "openai-response",
            "openai-response-compact",
            "openai-alpha-search",
        ],
        59 | 60 => vec![
            "openai",
            "openai-response",
            "openai-response-compact",
            "anthropic",
            "gemini",
            "openai-alpha-search",
        ],
        _ if ["o3-pro", "o3-deep-research", "o4-mini-deep-research"]
            .iter()
            .any(|known| model_name.contains(known)) =>
        {
            vec!["openai-response"]
        }
        _ => vec!["openai"],
    };
    let lower_name = model_name.to_ascii_lowercase();
    if ["dall-e-3", "dall-e-2", "gpt-image-1", "flux-", "flux.1-"]
        .iter()
        .any(|needle| lower_name.contains(needle))
        || lower_name.starts_with("imagen-")
    {
        endpoint_types.insert(0, "image-generation");
    }
    endpoint_types
}

async fn enrich_exact_models(pg: &PgPool, items: &mut [CatalogModel]) -> Result<(), CatalogError> {
    let names = items
        .iter()
        .filter(|item| item.name_rule == 0)
        .map(|item| item.model_name.clone())
        .collect::<Vec<_>>();
    if names.is_empty() {
        return Ok(());
    }

    let mut groups = HashMap::<String, Vec<String>>::new();
    let mut channels = HashMap::<String, Vec<CatalogBoundChannel>>::new();
    let mut endpoints = HashMap::<String, Vec<String>>::new();
    let rows = sqlx::query(
        r#"SELECT a.model, a."group", c.id AS channel_id,
                   COALESCE(c.name, '') AS channel_name, COALESCE(c.type, 0) AS channel_type
              FROM abilities a
              LEFT JOIN channels c ON c.id = a.channel_id
             WHERE a.model = ANY($1) AND a.enabled = TRUE
             ORDER BY a.model, a.channel_id, a."group""#,
    )
    .bind(&names)
    .fetch_all(pg)
    .await
    .map_err(database_error)?;
    for row in rows {
        let model: String = row.try_get("model").map_err(database_error)?;
        let group: String = row.try_get("group").map_err(database_error)?;
        let model_groups = groups.entry(model.clone()).or_default();
        if !model_groups.iter().any(|known| known == &group) {
            model_groups.push(group);
        }

        let channel_id: Option<i64> = row.try_get("channel_id").map_err(database_error)?;
        if channel_id.is_none() {
            continue;
        }
        let channel_name: String = row.try_get("channel_name").map_err(database_error)?;
        let channel_type: i64 = row.try_get("channel_type").map_err(database_error)?;
        let bound = channels.entry(model.clone()).or_default();
        if !bound
            .iter()
            .any(|known| known.name == channel_name && known.channel_type == channel_type)
        {
            bound.push(CatalogBoundChannel {
                name: channel_name,
                channel_type,
            });
        }
        let supported = endpoints.entry(model.clone()).or_default();
        for endpoint in catalog_endpoint_types_for_channel(channel_type, &model) {
            if !supported.iter().any(|known| known == endpoint) {
                supported.push(endpoint.to_owned());
            }
        }
    }

    let model_price =
        sqlx::query_scalar::<_, String>("SELECT value FROM options WHERE key = 'ModelPrice'")
            .fetch_optional(pg)
            .await
            .map_err(database_error)?
            .and_then(|value| serde_json::from_str::<Value>(&value).ok())
            .and_then(|value| value.as_object().cloned())
            .unwrap_or_default();

    for item in items.iter_mut().filter(|item| item.name_rule == 0) {
        // Go fills an empty endpoints column from the process-wide pricing
        // snapshot, including an explicit [] when no ability exists.
        if item.endpoints.is_empty() {
            let values = endpoints.get(&item.model_name).cloned().unwrap_or_default();
            item.endpoints =
                serde_json::to_string(&values).map_err(|_| CatalogError::Unavailable)?;
        }
        if let Some(values) = channels.remove(&item.model_name) {
            item.bound_channels = values;
        }
        if let Some(values) = groups.remove(&item.model_name) {
            item.enable_groups = values;
            if item.status == 1 {
                item.quota_types = vec![if model_price.contains_key(&item.model_name) {
                    1
                } else {
                    0
                }];
            }
        }
    }
    Ok(())
}

fn unique_error(error: sqlx::Error) -> CatalogError {
    match &error {
        sqlx::Error::Database(database) if database.code().as_deref() == Some("23505") => {
            CatalogError::Rejected("名称已存在".to_owned())
        }
        _ => database_error(error),
    }
}

async fn list_models_pg(pg: &PgPool, input: &Value, search: bool) -> Result<Value, CatalogError> {
    let (page_number, page_size, offset) = page(input);
    let keyword = if search {
        text(input, "keyword")
    } else {
        String::new()
    };
    let vendor = if search {
        text(input, "vendor")
    } else {
        String::new()
    };
    let vendor_id = vendor.parse::<i64>().ok();
    let vendor_name = if vendor_id.is_some() {
        String::new()
    } else {
        vendor
    };
    let status = normalize_status_filter(&text(input, "status"));
    let sync = normalize_sync_filter(&text(input, "sync_official"));
    let pagination = if page_size < 0 {
        if offset > 0 {
            format!(" OFFSET {offset}")
        } else {
            String::new()
        }
    } else if offset > 0 {
        " OFFSET $6 LIMIT $7".to_owned()
    } else {
        " LIMIT $6".to_owned()
    };
    let query = format!(
        "SELECT {MODEL_COLUMNS} FROM models WHERE deleted_at IS NULL AND ($1 = '' OR model_name LIKE '%' || $1 || '%' OR COALESCE(description, '') LIKE '%' || $1 || '%' OR COALESCE(tags, '') LIKE '%' || $1 || '%') AND ($2::bigint IS NULL OR vendor_id = $2) AND ($3 = '' OR EXISTS (SELECT 1 FROM vendors v WHERE v.id = models.vendor_id AND v.deleted_at IS NULL AND v.name LIKE '%' || $3 || '%')) AND ($4 = '' OR status = $4::bigint) AND ($5 = '' OR sync_official = $5::bigint) ORDER BY id DESC{pagination}"
    );
    let mut request = sqlx::query(&query)
        .bind(&keyword)
        .bind(vendor_id)
        .bind(&vendor_name)
        .bind(&status)
        .bind(&sync);
    if page_size >= 0 {
        if offset > 0 {
            request = request.bind(offset).bind(page_size);
        } else {
            request = request.bind(page_size);
        }
    }
    let rows = request.fetch_all(pg).await.map_err(database_error)?;
    let mut items = rows
        .iter()
        .map(model_from_row)
        .collect::<Result<Vec<_>, _>>()?;
    enrich_exact_models(pg, &mut items).await?;
    let total = sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM models WHERE deleted_at IS NULL AND ($1 = '' OR model_name LIKE '%' || $1 || '%' OR COALESCE(description, '') LIKE '%' || $1 || '%' OR COALESCE(tags, '') LIKE '%' || $1 || '%') AND ($2::bigint IS NULL OR vendor_id = $2) AND ($3 = '' OR EXISTS (SELECT 1 FROM vendors v WHERE v.id = models.vendor_id AND v.deleted_at IS NULL AND v.name LIKE '%' || $3 || '%')) AND ($4 = '' OR status = $4::bigint) AND ($5 = '' OR sync_official = $5::bigint)").bind(keyword).bind(vendor_id).bind(vendor_name).bind(status).bind(sync).fetch_one(pg).await.map_err(database_error)?;
    let count_rows = sqlx::query("SELECT COALESCE(vendor_id, 0) AS vendor_id, COUNT(*) AS count FROM models WHERE deleted_at IS NULL GROUP BY vendor_id").fetch_all(pg).await.map_err(database_error)?;
    let vendor_counts = count_rows
        .into_iter()
        .try_fold(BTreeMap::new(), |mut counts, row| {
            let vendor_id: i64 = row.try_get("vendor_id").map_err(database_error)?;
            let count: i64 = row.try_get("count").map_err(database_error)?;
            counts.insert(vendor_id.to_string(), count);
            Ok::<_, CatalogError>(counts)
        })?;
    Ok(
        json!({"items": items, "total": total, "page": page_number, "page_size": page_size, "vendor_counts": vendor_counts}),
    )
}

async fn get_model_pg(pg: &PgPool, id: i64) -> Result<Value, CatalogError> {
    let row = sqlx::query(&format!(
        "SELECT {MODEL_COLUMNS} FROM models WHERE id = $1 AND deleted_at IS NULL"
    ))
    .bind(id)
    .fetch_optional(pg)
    .await
    .map_err(database_error)?
    .ok_or(CatalogError::NotFound)?;
    let mut item = model_from_row(&row)?;
    enrich_exact_models(pg, std::slice::from_mut(&mut item)).await?;
    serde_json::to_value(item).map_err(|_| CatalogError::Unavailable)
}

async fn create_model_pg(pg: &PgPool, input: &Value) -> Result<Value, CatalogError> {
    let name = text(input, "model_name");
    if name.is_empty() {
        return Err(CatalogError::Rejected("模型名称不能为空".to_owned()));
    }
    let duplicate = sqlx::query_scalar::<_, i64>(
        "SELECT 1::bigint FROM models WHERE model_name=$1 AND deleted_at IS NULL LIMIT 1",
    )
    .bind(&name)
    .fetch_optional(pg)
    .await
    .map_err(database_error)?
    .is_some();
    if duplicate {
        return Err(CatalogError::Rejected("模型名称已存在".to_owned()));
    }
    let now = unix_now()?;
    let row = sqlx::query(&format!("INSERT INTO models (model_name, description, icon, tags, vendor_id, endpoints, status, sync_official, created_time, updated_time, name_rule) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,$10) RETURNING {MODEL_COLUMNS}")).bind(name).bind(text(input,"description")).bind(text(input,"icon")).bind(text(input,"tags")).bind(integer(input,"vendor_id",0)).bind(text(input,"endpoints")).bind(integer(input,"status",1)).bind(integer(input,"sync_official",1)).bind(now).bind(integer(input,"name_rule",0)).fetch_one(pg).await.map_err(unique_error)?;
    serde_json::to_value(model_from_row(&row)?).map_err(|_| CatalogError::Unavailable)
}

async fn update_model_pg(pg: &PgPool, input: &Value) -> Result<Value, CatalogError> {
    let id = integer(input, "id", 0);
    if id <= 0 {
        return Err(CatalogError::Rejected("缺少模型 ID".to_owned()));
    }
    let status_only = text(input, "status_only") == "true";
    if !status_only {
        let name = text(input, "model_name");
        if !name.is_empty()
            && sqlx::query_scalar::<_, i64>(
                "SELECT 1::bigint FROM models WHERE model_name=$1 AND id<>$2 AND deleted_at IS NULL LIMIT 1",
            )
            .bind(&name)
            .bind(id)
            .fetch_optional(pg)
            .await
            .map_err(database_error)?
            .is_some()
        {
            return Err(CatalogError::Rejected("模型名称已存在".to_owned()));
        }
    }
    let now = unix_now()?;
    let row = if status_only {
        sqlx::query(&format!("UPDATE models SET status=$2, updated_time=$3 WHERE id=$1 AND deleted_at IS NULL RETURNING {MODEL_COLUMNS}")).bind(id).bind(integer(input,"status",0)).bind(now).fetch_optional(pg).await.map_err(unique_error)?
    } else {
        sqlx::query(&format!("UPDATE models SET model_name=$2, description=$3, icon=$4, tags=$5, vendor_id=$6, endpoints=$7, status=$8, sync_official=$9, name_rule=$10, updated_time=$11 WHERE id=$1 AND deleted_at IS NULL RETURNING {MODEL_COLUMNS}")).bind(id).bind(text(input,"model_name")).bind(text(input,"description")).bind(text(input,"icon")).bind(text(input,"tags")).bind(integer(input,"vendor_id",0)).bind(text(input,"endpoints")).bind(integer(input,"status",0)).bind(integer(input,"sync_official",0)).bind(integer(input,"name_rule",0)).bind(now).fetch_optional(pg).await.map_err(unique_error)?
    };
    let row = row.ok_or(CatalogError::NotFound)?;
    serde_json::to_value(model_from_row(&row)?).map_err(|_| CatalogError::Unavailable)
}

async fn delete_by_id(pg: &PgPool, table: &'static str, id: i64) -> Result<Value, CatalogError> {
    let statement = match table {
        "models" => "UPDATE models SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL",
        "vendors" => "UPDATE vendors SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL",
        "prefill_groups" => {
            "UPDATE prefill_groups SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL"
        }
        "redemptions" => {
            "UPDATE redemptions SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL"
        }
        _ => return Err(CatalogError::Unavailable),
    };
    sqlx::query(statement)
        .bind(id)
        .execute(pg)
        .await
        .map_err(database_error)?;
    Ok(Value::Null)
}

async fn list_vendors_pg(pg: &PgPool, input: &Value, search: bool) -> Result<Value, CatalogError> {
    let (page_number, page_size, offset) = page(input);
    let keyword = if search {
        text(input, "keyword")
    } else {
        String::new()
    };
    // `GetAllVendors` uses GORM's default primary-key order, whereas
    // `SearchVendors` explicitly uses `id DESC`.
    let order = if search { "DESC" } else { "ASC" };
    let pagination = if page_size < 0 {
        if offset > 0 {
            format!(" OFFSET {offset}")
        } else {
            String::new()
        }
    } else if offset > 0 {
        " OFFSET $2 LIMIT $3".to_owned()
    } else {
        " LIMIT $2".to_owned()
    };
    let query = format!(
        "SELECT {VENDOR_COLUMNS} FROM vendors WHERE deleted_at IS NULL AND ($1 = '' OR name LIKE '%' || $1 || '%' OR COALESCE(description, '') LIKE '%' || $1 || '%') ORDER BY id {order}{pagination}"
    );
    let mut request = sqlx::query(&query).bind(&keyword);
    if page_size >= 0 {
        if offset > 0 {
            request = request.bind(offset).bind(page_size);
        } else {
            request = request.bind(page_size);
        }
    }
    let rows = request.fetch_all(pg).await.map_err(database_error)?;
    let items = rows
        .iter()
        .map(vendor_from_row)
        .collect::<Result<Vec<_>, _>>()?;
    let total = sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM vendors WHERE deleted_at IS NULL AND ($1 = '' OR name LIKE '%' || $1 || '%' OR COALESCE(description, '') LIKE '%' || $1 || '%')").bind(keyword).fetch_one(pg).await.map_err(database_error)?;
    Ok(json!({"items":items,"total":total,"page":page_number,"page_size":page_size}))
}

async fn get_vendor_pg(pg: &PgPool, id: i64) -> Result<Value, CatalogError> {
    let row = sqlx::query(&format!(
        "SELECT {VENDOR_COLUMNS} FROM vendors WHERE id=$1 AND deleted_at IS NULL"
    ))
    .bind(id)
    .fetch_optional(pg)
    .await
    .map_err(database_error)?
    .ok_or(CatalogError::NotFound)?;
    serde_json::to_value(vendor_from_row(&row)?).map_err(|_| CatalogError::Unavailable)
}

async fn create_vendor_pg(pg: &PgPool, input: &Value) -> Result<Value, CatalogError> {
    let name = text(input, "name");
    if name.is_empty() {
        return Err(CatalogError::Rejected("供应商名称不能为空".to_owned()));
    }
    if sqlx::query_scalar::<_, i64>(
        "SELECT 1::bigint FROM vendors WHERE name=$1 AND deleted_at IS NULL LIMIT 1",
    )
    .bind(&name)
    .fetch_optional(pg)
    .await
    .map_err(database_error)?
    .is_some()
    {
        return Err(CatalogError::Rejected("供应商名称已存在".to_owned()));
    }
    let now = unix_now()?;
    // GORM applies Vendor.Status's default tag when the JSON value is zero on
    // create. Preserve that legacy behaviour; updates still persist zero.
    let requested_status = integer(input, "status", 1);
    let status = if requested_status == 0 {
        1
    } else {
        requested_status
    };
    let row = sqlx::query(&format!("INSERT INTO vendors (name,description,icon,status,created_time,updated_time) VALUES ($1,$2,$3,$4,$5,$5) RETURNING {VENDOR_COLUMNS}")).bind(name).bind(text(input,"description")).bind(text(input,"icon")).bind(status).bind(now).fetch_one(pg).await.map_err(unique_error)?;
    serde_json::to_value(vendor_from_row(&row)?).map_err(|_| CatalogError::Unavailable)
}

async fn update_vendor_pg(pg: &PgPool, input: &Value) -> Result<Value, CatalogError> {
    let id = integer(input, "id", 0);
    if id <= 0 {
        return Err(CatalogError::Rejected("缺少供应商 ID".to_owned()));
    }
    let name = text(input, "name");
    if !name.is_empty()
        && sqlx::query_scalar::<_, i64>(
            "SELECT 1::bigint FROM vendors WHERE name=$1 AND id<>$2 AND deleted_at IS NULL LIMIT 1",
        )
        .bind(&name)
        .bind(id)
        .fetch_optional(pg)
        .await
        .map_err(database_error)?
        .is_some()
    {
        return Err(CatalogError::Rejected("供应商名称已存在".to_owned()));
    }
    let now = unix_now()?;
    let row=sqlx::query(&format!("UPDATE vendors SET name=$2,description=$3,icon=$4,status=$5,updated_time=$6 WHERE id=$1 AND deleted_at IS NULL RETURNING {VENDOR_COLUMNS}")).bind(id).bind(text(input,"name")).bind(text(input,"description")).bind(text(input,"icon")).bind(integer(input,"status",0)).bind(now).fetch_optional(pg).await.map_err(unique_error)?.ok_or(CatalogError::NotFound)?;
    serde_json::to_value(vendor_from_row(&row)?).map_err(|_| CatalogError::Unavailable)
}

async fn list_prefill_groups_pg(pg: &PgPool, input: &Value) -> Result<Value, CatalogError> {
    let kind = text(input, "type");
    let rows=sqlx::query(&format!("SELECT {PREFILL_COLUMNS} FROM prefill_groups WHERE deleted_at IS NULL AND ($1='' OR type=$1) ORDER BY updated_time DESC")).bind(kind).fetch_all(pg).await.map_err(database_error)?;
    let items = rows
        .iter()
        .map(prefill_group_from_row)
        .collect::<Result<Vec<_>, _>>()?;
    serde_json::to_value(items).map_err(|_| CatalogError::Unavailable)
}

async fn create_prefill_group_pg(pg: &PgPool, input: &Value) -> Result<Value, CatalogError> {
    let name = text(input, "name");
    let kind = text(input, "type");
    if name.is_empty() || kind.is_empty() {
        return Err(CatalogError::Rejected("组名称和类型不能为空".to_owned()));
    }
    if sqlx::query_scalar::<_, i64>(
        "SELECT 1::bigint FROM prefill_groups WHERE name=$1 AND deleted_at IS NULL LIMIT 1",
    )
    .bind(&name)
    .fetch_optional(pg)
    .await
    .map_err(database_error)?
    .is_some()
    {
        return Err(CatalogError::Rejected("组名称已存在".to_owned()));
    }
    let now = unix_now()?;
    let items = input
        .get("items")
        .cloned()
        .map_or(Value::Null, |value| value);
    let row=sqlx::query(&format!("INSERT INTO prefill_groups (name,type,items,description,created_time,updated_time) VALUES ($1,$2,$3,$4,$5,$5) RETURNING {PREFILL_COLUMNS}")).bind(name).bind(kind).bind(items).bind(text(input,"description")).bind(now).fetch_one(pg).await.map_err(unique_error)?;
    serde_json::to_value(prefill_group_from_row(&row)?).map_err(|_| CatalogError::Unavailable)
}

async fn update_prefill_group_pg(pg: &PgPool, input: &Value) -> Result<Value, CatalogError> {
    let id = integer(input, "id", 0);
    if id <= 0 {
        return Err(CatalogError::Rejected("缺少组 ID".to_owned()));
    }
    let name = text(input, "name");
    if !name.is_empty()
        && sqlx::query_scalar::<_, i64>(
            "SELECT 1::bigint FROM prefill_groups WHERE name=$1 AND id<>$2 AND deleted_at IS NULL LIMIT 1",
        )
        .bind(&name)
        .bind(id)
        .fetch_optional(pg)
        .await
        .map_err(database_error)?
        .is_some()
    {
        return Err(CatalogError::Rejected("组名称已存在".to_owned()));
    }
    let now = unix_now()?;
    let items = input
        .get("items")
        .cloned()
        .map_or(Value::Null, |value| value);
    let row=sqlx::query(&format!("UPDATE prefill_groups SET name=$2,type=$3,items=$4,description=$5,updated_time=$6 WHERE id=$1 AND deleted_at IS NULL RETURNING {PREFILL_COLUMNS}")).bind(id).bind(text(input,"name")).bind(text(input,"type")).bind(items).bind(text(input,"description")).bind(now).fetch_optional(pg).await.map_err(unique_error)?.ok_or(CatalogError::NotFound)?;
    serde_json::to_value(prefill_group_from_row(&row)?).map_err(|_| CatalogError::Unavailable)
}

async fn list_redemptions_pg(
    pg: &PgPool,
    input: &Value,
    search: bool,
) -> Result<Value, CatalogError> {
    let (page_number, page_size, offset) = page(input);
    let keyword = if search {
        text(input, "keyword")
    } else {
        String::new()
    };
    let status = if search {
        text(input, "status")
    } else {
        String::new()
    };
    let now = unix_now()?;
    let pagination = if page_size < 0 {
        if offset > 0 {
            format!(" OFFSET {offset}")
        } else {
            String::new()
        }
    } else if offset > 0 {
        " OFFSET $4 LIMIT $5".to_owned()
    } else {
        " LIMIT $4".to_owned()
    };
    let query = format!(
        "SELECT {REDEMPTION_COLUMNS} FROM redemptions WHERE deleted_at IS NULL AND ($1='' OR name LIKE $1 || '%' OR CAST(id AS TEXT)=$1) AND ($2='' OR ($2='expired' AND status=1 AND expired_time<>0 AND expired_time<$3) OR ($2='1' AND status=1 AND (expired_time=0 OR expired_time>=$3)) OR ($2='0' AND status=0) OR ($2='2' AND status=2)) ORDER BY id DESC{pagination}"
    );
    let mut request = sqlx::query(&query)
        .bind(keyword.clone())
        .bind(status.clone())
        .bind(now);
    if page_size >= 0 {
        if offset > 0 {
            request = request.bind(offset).bind(page_size);
        } else {
            request = request.bind(page_size);
        }
    }
    let rows = request.fetch_all(pg).await.map_err(database_error)?;
    let items = rows
        .iter()
        .map(redemption_from_row)
        .collect::<Result<Vec<_>, _>>()?;
    let total=sqlx::query_scalar::<_,i64>("SELECT COUNT(*) FROM redemptions WHERE deleted_at IS NULL AND ($1='' OR name LIKE $1 || '%' OR CAST(id AS TEXT)=$1) AND ($2='' OR ($2='expired' AND status=1 AND expired_time<>0 AND expired_time<$3) OR ($2='1' AND status=1 AND (expired_time=0 OR expired_time>=$3)) OR ($2='0' AND status=0) OR ($2='2' AND status=2))").bind(keyword).bind(status).bind(now).fetch_one(pg).await.map_err(database_error)?;
    Ok(json!({"items":items,"total":total,"page":page_number,"page_size":page_size}))
}

async fn get_redemption_pg(pg: &PgPool, id: i64) -> Result<Value, CatalogError> {
    let row = sqlx::query(&format!(
        "SELECT {REDEMPTION_COLUMNS} FROM redemptions WHERE id=$1 AND deleted_at IS NULL"
    ))
    .bind(id)
    .fetch_optional(pg)
    .await
    .map_err(database_error)?
    .ok_or(CatalogError::NotFound)?;
    serde_json::to_value(redemption_from_row(&row)?).map_err(|_| CatalogError::Unavailable)
}

async fn payment_compliance_confirmed(pg: &PgPool) -> Result<bool, CatalogError> {
    let rows=sqlx::query("SELECT key,value FROM options WHERE key IN ('payment_setting.compliance_confirmed','payment_setting.compliance_terms_version')").fetch_all(pg).await.map_err(database_error)?;
    let mut values = HashMap::new();
    for row in rows {
        let key: String = row.try_get("key").map_err(database_error)?;
        let value: String = row.try_get("value").map_err(database_error)?;
        values.insert(key, value);
    }
    Ok(values
        .get("payment_setting.compliance_confirmed")
        .is_some_and(|value| value.eq_ignore_ascii_case("true") || value == "1")
        && values
            .get("payment_setting.compliance_terms_version")
            .is_some_and(|value| value == "v1"))
}

async fn create_redemptions_pg(
    pg: &PgPool,
    actor: AdminCatalogActor,
    input: &Value,
) -> Result<Value, CatalogError> {
    if !payment_compliance_confirmed(pg).await? {
        return Err(CatalogError::Rejected("Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them.".to_owned()));
    }
    let name = text(input, "name");
    let count = integer(input, "count", 0);
    let quota = integer(input, "quota", 100);
    let expired = integer(input, "expired_time", 0);
    if name.chars().count() == 0 || name.chars().count() > 20 {
        return Err(CatalogError::Rejected(
            "Redemption code name length must be between 1-20".to_owned(),
        ));
    }
    if count <= 0 {
        return Err(CatalogError::Rejected(
            "Redemption code count must be greater than 0".to_owned(),
        ));
    }
    if count > 100 {
        return Err(CatalogError::Rejected(
            "Maximum 100 redemption codes can be generated at once".to_owned(),
        ));
    }
    if expired != 0 && expired < unix_now()? {
        return Err(CatalogError::Rejected(
            "Expiration time cannot be earlier than current time".to_owned(),
        ));
    }
    let now = unix_now()?;
    let mut transaction = pg.begin().await.map_err(database_error)?;
    let mut keys = Vec::with_capacity(count as usize);
    for _ in 0..count {
        let key = uuid::Uuid::new_v4().simple().to_string();
        sqlx::query("INSERT INTO redemptions (user_id,key,status,name,quota,created_time,redeemed_time,used_user_id,expired_time) VALUES ($1,$2,1,$3,$4,$5,0,0,$6)").bind(actor.user_id).bind(&key).bind(&name).bind(quota).bind(now).bind(expired).execute(&mut *transaction).await.map_err(database_error)?;
        keys.push(key);
    }
    transaction.commit().await.map_err(database_error)?;
    serde_json::to_value(keys).map_err(|_| CatalogError::Unavailable)
}

async fn update_redemption_pg(pg: &PgPool, input: &Value) -> Result<Value, CatalogError> {
    let id = integer(input, "id", 0);
    if id <= 0 {
        return Err(CatalogError::Invalid("id 参数错误"));
    }
    // Go treats every non-empty `status_only` query value as status-only for
    // redemption updates (unlike the models route, which requires `true`).
    let status_only = !text(input, "status_only").is_empty();
    if !status_only {
        let expired = integer(input, "expired_time", 0);
        if expired != 0 && expired < unix_now()? {
            return Err(CatalogError::Rejected(
                "Expiration time cannot be earlier than current time".to_owned(),
            ));
        }
    }
    let row = if status_only {
        sqlx::query(&format!("UPDATE redemptions SET status=$2 WHERE id=$1 AND deleted_at IS NULL RETURNING {REDEMPTION_COLUMNS}")).bind(id).bind(integer(input,"status",0)).fetch_optional(pg).await.map_err(database_error)?
    } else {
        sqlx::query(&format!("UPDATE redemptions SET name=$2,quota=$3,expired_time=$4 WHERE id=$1 AND deleted_at IS NULL RETURNING {REDEMPTION_COLUMNS}")).bind(id).bind(text(input,"name")).bind(integer(input,"quota",0)).bind(integer(input,"expired_time",0)).fetch_optional(pg).await.map_err(database_error)?
    };
    let row = row.ok_or(CatalogError::NotFound)?;
    serde_json::to_value(redemption_from_row(&row)?).map_err(|_| CatalogError::Unavailable)
}

async fn delete_invalid_redemptions_pg(pg: &PgPool) -> Result<Value, CatalogError> {
    let now = unix_now()?;
    let result=sqlx::query("UPDATE redemptions SET deleted_at=NOW() WHERE deleted_at IS NULL AND (status IN (0,2) OR (status=1 AND expired_time<>0 AND expired_time<$1))").bind(now).execute(pg).await.map_err(database_error)?;
    Ok(json!(result.rows_affected()))
}

async fn missing_models_pg(pg: &PgPool) -> Result<Value, CatalogError> {
    let rows=sqlx::query("SELECT DISTINCT abilities.model FROM abilities JOIN channels ON channels.id=abilities.channel_id WHERE abilities.enabled=TRUE AND channels.status=1 AND abilities.model<>'' AND NOT EXISTS (SELECT 1 FROM models WHERE models.model_name=abilities.model AND models.deleted_at IS NULL) ORDER BY abilities.model").fetch_all(pg).await.map_err(database_error)?;
    let names = rows
        .into_iter()
        .map(|row| row.try_get::<String, _>("model").map_err(database_error))
        .collect::<Result<Vec<_>, _>>()?;
    serde_json::to_value(names).map_err(|_| CatalogError::Unavailable)
}

async fn sync_upstream_pg(
    pg: &PgPool,
    upstream: &dyn CatalogUpstream,
    input: &Value,
) -> Result<Value, CatalogError> {
    let locale = text(input, "locale");
    let feed = upstream.fetch(&locale).await?;
    let missing = serde_json::from_value::<Vec<String>>(missing_models_pg(pg).await?)
        .map_err(|_| CatalogError::Unavailable)?;
    let requested = input
        .get("overwrite")
        .and_then(Value::as_array)
        .map_or_else(Vec::new, Clone::clone);
    if missing.is_empty() && requested.is_empty() {
        return Ok(
            json!({"created_models":0,"created_vendors":0,"updated_models":0,"skipped_models":[],"created_list":[],"updated_list":[],"source":{"locale":locale,"models_url":feed.models_url,"vendors_url":feed.vendors_url}}),
        );
    }
    let model_by_name = feed
        .models
        .iter()
        .filter(|model| !model.model_name.trim().is_empty())
        .map(|model| (model.model_name.as_str(), model))
        .collect::<HashMap<_, _>>();
    let vendor_by_name = feed
        .vendors
        .iter()
        .filter(|vendor| !vendor.name.trim().is_empty())
        .map(|vendor| (vendor.name.as_str(), vendor))
        .collect::<HashMap<_, _>>();
    let now = unix_now()?;
    let mut transaction = pg.begin().await.map_err(database_error)?;
    let mut created_models = 0_u64;
    let mut created_vendors = 0_u64;
    let mut created_list = Vec::new();
    let mut skipped_models = Vec::new();
    for name in missing {
        let Some(model) = model_by_name.get(name.as_str()) else {
            skipped_models.push(name);
            continue;
        };
        let vendor = vendor_by_name.get(model.vendor_name.as_str()).copied();
        let existing = sqlx::query("SELECT id FROM vendors WHERE name=$1 AND deleted_at IS NULL")
            .bind(&model.vendor_name)
            .fetch_optional(&mut *transaction)
            .await
            .map_err(database_error)?;
        let vendor_id = match existing {
            Some(row) => row.try_get("id").map_err(database_error)?,
            None if model.vendor_name.trim().is_empty() => 0,
            None => {
                let row=sqlx::query("INSERT INTO vendors (name,description,icon,status,created_time,updated_time) VALUES ($1,$2,$3,$4,$5,$5) RETURNING id").bind(&model.vendor_name).bind(vendor.map_or("",|vendor|vendor.description.as_str())).bind(vendor.map_or("",|vendor|vendor.icon.as_str())).bind(vendor.map_or(1,|vendor|if vendor.status==0{1}else{vendor.status})).bind(now).fetch_one(&mut *transaction).await.map_err(unique_error)?;
                created_vendors += 1;
                row.try_get("id").map_err(database_error)?
            }
        };
        let result = sqlx::query("INSERT INTO models (model_name,description,icon,tags,vendor_id,status,sync_official,created_time,updated_time,name_rule) VALUES ($1,$2,$3,$4,$5,$6,1,$7,$7,$8) ON CONFLICT DO NOTHING").bind(&model.model_name).bind(&model.description).bind(&model.icon).bind(&model.tags).bind(vendor_id).bind(if model.status==0{1}else{model.status}).bind(now).bind(model.name_rule).execute(&mut *transaction).await.map_err(database_error)?;
        if result.rows_affected() == 1 {
            created_models += 1;
            created_list.push(model.model_name.clone());
        } else {
            skipped_models.push(model.model_name.clone());
        }
    }
    let mut updated_models = 0_u64;
    let mut updated_list = Vec::new();
    for request in requested {
        let name = text(&request, "model_name");
        let Some(model) = model_by_name.get(name.as_str()) else {
            continue;
        };
        let fields =
            request
                .get("fields")
                .and_then(Value::as_array)
                .map_or_else(Vec::new, |values| {
                    values
                        .iter()
                        .filter_map(Value::as_str)
                        .map(str::trim)
                        .collect::<Vec<_>>()
                });
        if fields.is_empty() {
            continue;
        }
        let local = sqlx::query("SELECT id, sync_official, status, vendor_id FROM models WHERE model_name=$1 AND deleted_at IS NULL FOR UPDATE")
            .bind(&name)
            .fetch_optional(&mut *transaction)
            .await
            .map_err(database_error)?;
        let Some(local) = local else {
            continue;
        };
        let sync_official: i64 = local.try_get("sync_official").map_err(database_error)?;
        if sync_official == 0 {
            continue;
        }
        let local_status: i64 = local.try_get("status").map_err(database_error)?;
        let local_vendor_id: i64 = local.try_get("vendor_id").map_err(database_error)?;
        let remote_vendor_id =
            sqlx::query("SELECT id FROM vendors WHERE name=$1 AND deleted_at IS NULL")
                .bind(&model.vendor_name)
                .fetch_optional(&mut *transaction)
                .await
                .map_err(database_error)?
                .map(|row| row.try_get("id").map_err(database_error))
                .transpose()?
                .map_or(local_vendor_id, |id| id);
        let id: i64 = local.try_get("id").map_err(database_error)?;
        let updated = sqlx::query("UPDATE models SET description=CASE WHEN $2 THEN $3 ELSE description END,icon=CASE WHEN $4 THEN $5 ELSE icon END,tags=CASE WHEN $6 THEN $7 ELSE tags END,vendor_id=CASE WHEN $8 THEN $9 ELSE vendor_id END,name_rule=CASE WHEN $10 THEN $11 ELSE name_rule END,status=CASE WHEN $12 THEN $13 ELSE status END,updated_time=$14 WHERE id=$1")
            .bind(id)
            .bind(fields.contains(&"description"))
            .bind(&model.description)
            .bind(fields.contains(&"icon"))
            .bind(&model.icon)
            .bind(fields.contains(&"tags"))
            .bind(&model.tags)
            .bind(fields.contains(&"vendor"))
            .bind(remote_vendor_id)
            .bind(fields.contains(&"name_rule"))
            .bind(model.name_rule)
            .bind(fields.contains(&"status"))
            .bind(if model.status == 0 { local_status } else { model.status })
            .bind(now)
            .execute(&mut *transaction)
            .await
            .map_err(database_error)?;
        if updated.rows_affected() == 1 {
            updated_models += 1;
            updated_list.push(name);
        }
    }
    transaction.commit().await.map_err(database_error)?;
    Ok(
        json!({"created_models":created_models,"created_vendors":created_vendors,"updated_models":updated_models,"skipped_models":skipped_models,"created_list":created_list,"updated_list":updated_list,"source":{"locale":locale,"models_url":feed.models_url,"vendors_url":feed.vendors_url}}),
    )
}

async fn preview_upstream_pg(
    pg: &PgPool,
    upstream: &dyn CatalogUpstream,
    input: &Value,
) -> Result<Value, CatalogError> {
    let locale = text(input, "locale");
    let feed = upstream.fetch(&locale).await?;
    let missing = serde_json::from_value::<Vec<String>>(missing_models_pg(pg).await?)
        .map_err(|_| CatalogError::Unavailable)?;
    let upstream_models = feed
        .models
        .iter()
        .filter(|model| !model.model_name.trim().is_empty())
        .map(|model| (model.model_name.as_str(), model))
        .collect::<HashMap<_, _>>();
    let rows = sqlx::query("SELECT models.model_name,COALESCE(models.description,'') AS description,COALESCE(models.icon,'') AS icon,COALESCE(models.tags,'') AS tags,COALESCE(models.name_rule,0) AS name_rule,COALESCE(models.status,1) AS status,COALESCE(vendors.name,'') AS vendor_name FROM models LEFT JOIN vendors ON vendors.id=models.vendor_id AND vendors.deleted_at IS NULL WHERE models.deleted_at IS NULL AND models.sync_official<>0").fetch_all(pg).await.map_err(database_error)?;
    let mut conflicts = Vec::new();
    for row in rows {
        let name: String = row.try_get("model_name").map_err(database_error)?;
        let Some(remote) = upstream_models.get(name.as_str()) else {
            continue;
        };
        let mut fields = Vec::new();
        let local_description: String = row.try_get("description").map_err(database_error)?;
        if local_description.trim() != remote.description.trim() {
            fields.push(json!({"field":"description","local":local_description,"upstream":remote.description}));
        }
        let local_icon: String = row.try_get("icon").map_err(database_error)?;
        if local_icon.trim() != remote.icon.trim() {
            fields.push(json!({"field":"icon","local":local_icon,"upstream":remote.icon}));
        }
        let local_tags: String = row.try_get("tags").map_err(database_error)?;
        if local_tags.trim() != remote.tags.trim() {
            fields.push(json!({"field":"tags","local":local_tags,"upstream":remote.tags}));
        }
        let local_vendor: String = row.try_get("vendor_name").map_err(database_error)?;
        if local_vendor.trim() != remote.vendor_name.trim() {
            fields
                .push(json!({"field":"vendor","local":local_vendor,"upstream":remote.vendor_name}));
        }
        let local_rule: i64 = row.try_get("name_rule").map_err(database_error)?;
        if local_rule != remote.name_rule {
            fields
                .push(json!({"field":"name_rule","local":local_rule,"upstream":remote.name_rule}));
        }
        if !fields.is_empty() {
            conflicts.push(json!({"model_name":name,"fields":fields}));
        }
    }
    let missing = missing
        .into_iter()
        .filter(|name| upstream_models.contains_key(name.as_str()))
        .collect::<Vec<_>>();
    Ok(
        json!({"missing":missing,"conflicts":conflicts,"source":{"locale":locale,"models_url":feed.models_url,"vendors_url":feed.vendors_url}}),
    )
}

/// Dependencies for the unmounted catalog route group.
#[derive(Clone)]
pub struct AdminCatalogState {
    pub provider: Arc<dyn AdminCatalogProvider>,
    pub authorizer: Arc<dyn AdminCatalogAuthorizer>,
}

impl AdminCatalogState {
    #[must_use]
    pub fn new(
        provider: Arc<dyn AdminCatalogProvider>,
        authorizer: Arc<dyn AdminCatalogAuthorizer>,
    ) -> Self {
        Self {
            provider,
            authorizer,
        }
    }
}

/// All frozen, administrator-only catalog method/path candidates.
pub fn router(state: AdminCatalogState) -> Router {
    Router::new()
        .route(
            "/api/models/",
            get(list_models).post(create_model).put(update_model),
        )
        .route("/api/models/missing", get(missing_models))
        .route("/api/models/search", get(search_models))
        .route("/api/models/sync_upstream", post(sync_upstream))
        .route(
            "/api/models/sync_upstream/preview",
            get(preview_upstream_sync),
        )
        .route("/api/models/{id}", get(get_model).delete(delete_model))
        .route(
            "/api/vendors/",
            get(list_vendors).post(create_vendor).put(update_vendor),
        )
        .route("/api/vendors/search", get(search_vendors))
        .route("/api/vendors/{id}", get(get_vendor).delete(delete_vendor))
        .route(
            "/api/prefill_group/",
            get(list_prefill_groups)
                .post(create_prefill_group)
                .put(update_prefill_group),
        )
        .route("/api/prefill_group/{id}", delete(delete_prefill_group))
        .route(
            "/api/redemption/",
            get(list_redemptions)
                .post(create_redemption)
                .put(update_redemption),
        )
        .route(
            "/api/redemption/invalid",
            delete(delete_invalid_redemptions),
        )
        .route("/api/redemption/search", get(search_redemptions))
        .route(
            "/api/redemption/{id}",
            get(get_redemption).delete(delete_redemption),
        )
        .with_state(state)
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

fn success(data: Value) -> Response {
    Json(Envelope {
        success: true,
        message: String::new(),
        data: Some(data),
        code: None,
    })
    .into_response()
}

fn success_without_data() -> Response {
    Json(Envelope::<Value> {
        success: true,
        message: String::new(),
        data: None,
        code: None,
    })
    .into_response()
}

fn success_data_only(data: Value) -> Response {
    Json(json!({"success": true, "data": data})).into_response()
}

fn failure(status: StatusCode, message: impl Into<String>, code: Option<&'static str>) -> Response {
    (
        status,
        Json(Envelope::<Value> {
            success: false,
            message: message.into(),
            data: None,
            code,
        }),
    )
        .into_response()
}

async fn admin(
    state: &AdminCatalogState,
    headers: &HeaderMap,
) -> Result<AdminCatalogActor, Response> {
    let actor = state
        .authorizer
        .authorize(headers)
        .await
        .map_err(CatalogError::response)?;
    if actor.user_id <= 0 || actor.role < ADMIN_ROLE {
        return Err(CatalogError::Forbidden.response());
    }
    Ok(actor)
}

async fn execute(
    state: AdminCatalogState,
    headers: HeaderMap,
    operation: CatalogOperation,
    resource_id: Option<i64>,
    input: Value,
) -> Response {
    let actor = match admin(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    execute_authorized(&state, actor, operation, resource_id, input).await
}

async fn execute_authorized(
    state: &AdminCatalogState,
    actor: AdminCatalogActor,
    operation: CatalogOperation,
    resource_id: Option<i64>,
    input: Value,
) -> Response {
    match state
        .provider
        .execute(CatalogCall {
            operation,
            actor,
            resource_id,
            input,
        })
        .await
    {
        Ok(_) if operation == CatalogOperation::DeleteRedemption => success_without_data(),
        Ok(data) if operation == CatalogOperation::MissingModels => {
            // GetMissingModels uses a hand-written Go envelope: it omits the
            // message key, and a nil slice serializes as JSON null when there
            // are no enabled channel models missing metadata.
            let data = match &data {
                Value::Array(values) if values.is_empty() => Value::Null,
                _ => data,
            };
            success_data_only(data)
        }
        Ok(data) => success(data),
        Err(error) => error.response(),
    }
}

async fn execute_id(
    state: AdminCatalogState,
    headers: HeaderMap,
    operation: CatalogOperation,
    raw: String,
) -> Response {
    let actor = match admin(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let id = match resource_id(raw) {
        Ok(id) => id,
        Err(error) => return error.response(),
    };
    execute_authorized(&state, actor, operation, Some(id), json!({})).await
}

async fn execute_json(
    state: AdminCatalogState,
    headers: HeaderMap,
    operation: CatalogOperation,
    body: Bytes,
    requires_id: bool,
    query: Option<Value>,
) -> Response {
    // Go's AdminAuth middleware runs before ShouldBindJSON. Keep that order so
    // an anonymous malformed request cannot reveal body-validation behavior.
    let actor = match admin(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let body = match serde_json::from_slice::<Value>(&body) {
        Ok(body) => body,
        Err(_) => return CatalogError::Invalid("无效的参数").response(),
    };
    let mut body = match require_object(body) {
        Ok(body) => body,
        Err(error) => return error.response(),
    };
    if let (Some(Value::Object(query)), Some(body)) = (query, body.as_object_mut()) {
        // Gin binds the JSON body independently. Only the two update handlers'
        // `status_only` query flag is read by Go; unrelated query keys must not
        // override body fields such as `id`.
        if let Some(status_only) = query.get("status_only") {
            body.insert("status_only".to_owned(), status_only.clone());
        }
    }
    if requires_id
        && let Err(error) = require_positive_body_id(&body) {
            return error.response();
        }
    execute_authorized(&state, actor, operation, None, body).await
}

fn resource_id(raw: String) -> Result<i64, CatalogError> {
    raw.parse::<i64>()
        .ok()
        .filter(|id| *id > 0)
        .ok_or(CatalogError::Invalid("id 参数错误"))
}

fn require_object(value: Value) -> Result<Value, CatalogError> {
    value
        .is_object()
        .then_some(value)
        .ok_or(CatalogError::Invalid("无效的参数"))
}

fn require_positive_body_id(value: &Value) -> Result<(), CatalogError> {
    value
        .get("id")
        .and_then(Value::as_i64)
        .filter(|id| *id > 0)
        .map(|_| ())
        .ok_or(CatalogError::Invalid("id 参数错误"))
}

#[derive(Default, Deserialize)]
struct LegacyQuery {
    #[serde(flatten)]
    values: serde_json::Map<String, Value>,
}

impl LegacyQuery {
    fn into_value(self) -> Value {
        Value::Object(self.values)
    }
}

async fn list_models(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Query(query): Query<LegacyQuery>,
) -> Response {
    execute(
        state,
        headers,
        CatalogOperation::ListModels,
        None,
        query.into_value(),
    )
    .await
}

async fn create_model(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    execute_json(
        state,
        headers,
        CatalogOperation::CreateModel,
        body,
        false,
        None,
    )
    .await
}

async fn update_model(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Query(query): Query<LegacyQuery>,
    body: Bytes,
) -> Response {
    execute_json(
        state,
        headers,
        CatalogOperation::UpdateModel,
        body,
        true,
        Some(query.into_value()),
    )
    .await
}

async fn get_model(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Path(raw): Path<String>,
) -> Response {
    execute_id(state, headers, CatalogOperation::GetModel, raw).await
}

async fn delete_model(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Path(raw): Path<String>,
) -> Response {
    execute_id(state, headers, CatalogOperation::DeleteModel, raw).await
}

async fn missing_models(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Query(query): Query<LegacyQuery>,
) -> Response {
    execute(
        state,
        headers,
        CatalogOperation::MissingModels,
        None,
        query.into_value(),
    )
    .await
}

async fn search_models(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Query(query): Query<LegacyQuery>,
) -> Response {
    execute(
        state,
        headers,
        CatalogOperation::SearchModels,
        None,
        query.into_value(),
    )
    .await
}

async fn sync_upstream(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    execute_json(
        state,
        headers,
        CatalogOperation::SyncUpstream,
        body,
        false,
        None,
    )
    .await
}

async fn preview_upstream_sync(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Query(query): Query<LegacyQuery>,
) -> Response {
    execute(
        state,
        headers,
        CatalogOperation::PreviewUpstreamSync,
        None,
        query.into_value(),
    )
    .await
}

async fn list_vendors(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Query(query): Query<LegacyQuery>,
) -> Response {
    execute(
        state,
        headers,
        CatalogOperation::ListVendors,
        None,
        query.into_value(),
    )
    .await
}

async fn create_vendor(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    execute_json(
        state,
        headers,
        CatalogOperation::CreateVendor,
        body,
        false,
        None,
    )
    .await
}

async fn update_vendor(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    execute_json(
        state,
        headers,
        CatalogOperation::UpdateVendor,
        body,
        true,
        None,
    )
    .await
}

async fn get_vendor(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Path(raw): Path<String>,
) -> Response {
    execute_id(state, headers, CatalogOperation::GetVendor, raw).await
}

async fn delete_vendor(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Path(raw): Path<String>,
) -> Response {
    execute_id(state, headers, CatalogOperation::DeleteVendor, raw).await
}

async fn search_vendors(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Query(query): Query<LegacyQuery>,
) -> Response {
    execute(
        state,
        headers,
        CatalogOperation::SearchVendors,
        None,
        query.into_value(),
    )
    .await
}

async fn list_prefill_groups(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Query(query): Query<LegacyQuery>,
) -> Response {
    execute(
        state,
        headers,
        CatalogOperation::ListPrefillGroups,
        None,
        query.into_value(),
    )
    .await
}

async fn create_prefill_group(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    execute_json(
        state,
        headers,
        CatalogOperation::CreatePrefillGroup,
        body,
        false,
        None,
    )
    .await
}

async fn update_prefill_group(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    execute_json(
        state,
        headers,
        CatalogOperation::UpdatePrefillGroup,
        body,
        true,
        None,
    )
    .await
}

async fn delete_prefill_group(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Path(raw): Path<String>,
) -> Response {
    execute_id(state, headers, CatalogOperation::DeletePrefillGroup, raw).await
}

async fn list_redemptions(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Query(query): Query<LegacyQuery>,
) -> Response {
    execute(
        state,
        headers,
        CatalogOperation::ListRedemptions,
        None,
        query.into_value(),
    )
    .await
}

async fn create_redemption(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    execute_json(
        state,
        headers,
        CatalogOperation::CreateRedemption,
        body,
        false,
        None,
    )
    .await
}

async fn update_redemption(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Query(query): Query<LegacyQuery>,
    body: Bytes,
) -> Response {
    execute_json(
        state,
        headers,
        CatalogOperation::UpdateRedemption,
        body,
        true,
        Some(query.into_value()),
    )
    .await
}

async fn get_redemption(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Path(raw): Path<String>,
) -> Response {
    execute_id(state, headers, CatalogOperation::GetRedemption, raw).await
}

async fn delete_redemption(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Path(raw): Path<String>,
) -> Response {
    execute_id(state, headers, CatalogOperation::DeleteRedemption, raw).await
}

async fn delete_invalid_redemptions(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
) -> Response {
    execute(
        state,
        headers,
        CatalogOperation::DeleteInvalidRedemptions,
        None,
        json!({}),
    )
    .await
}

async fn search_redemptions(
    State(state): State<AdminCatalogState>,
    headers: HeaderMap,
    Query(query): Query<LegacyQuery>,
) -> Response {
    execute(
        state,
        headers,
        CatalogOperation::SearchRedemptions,
        None,
        query.into_value(),
    )
    .await
}

#[cfg(test)]
mod tests {
    use super::{
        CatalogModel, CatalogRedemption, CatalogVendor, HttpCatalogUpstream,
        normalize_status_filter, normalize_sync_filter, page,
    };
    use serde_json::{Value, json};

    #[test]
    fn upstream_urls_map_chinese_aliases_to_published_feed() {
        let upstream = HttpCatalogUpstream::new(
            reqwest::Client::new(),
            reqwest::Url::parse("https://catalog.example/root/").expect("valid base URL"),
        );

        for locale in ["zh", "zh-CN", "zh-TW"] {
            let (models, vendors) = upstream.urls(locale).expect("supported locale");
            assert_eq!(
                models.as_str(),
                "https://catalog.example/root/api/i18n/zh/newapi/models.json"
            );
            assert_eq!(
                vendors.as_str(),
                "https://catalog.example/root/api/i18n/zh/newapi/vendors.json"
            );
        }
    }

    #[test]
    fn page_query_preserves_legacy_aliases_and_negative_gorm_controls() {
        assert_eq!(page(&json!({})), (1, 10, 0));
        assert_eq!(page(&json!({"p": "2", "ps": "7"})), (2, 7, 7));
        assert_eq!(page(&json!({"p": "2", "size": "8"})), (2, 8, 8));
        assert_eq!(page(&json!({"p": "wat", "page_size": "101"})), (1, 100, 0));
        assert_eq!(page(&json!({"p": "-1", "page_size": "-1"})), (-1, -1, 2));
    }

    #[test]
    fn model_filters_match_go_status_and_sync_vocabularies() {
        assert_eq!(normalize_status_filter("enabled"), "1");
        assert_eq!(normalize_status_filter("disabled"), "0");
        assert_eq!(normalize_status_filter("2"), "2");
        assert_eq!(normalize_status_filter("yes"), "");
        assert_eq!(normalize_status_filter("unknown"), "");

        assert_eq!(normalize_sync_filter("yes"), "1");
        assert_eq!(normalize_sync_filter("no"), "0");
        assert_eq!(normalize_sync_filter("2"), "2");
        assert_eq!(normalize_sync_filter("unknown"), "");
    }

    #[test]
    fn catalog_wire_omits_go_omitempty_fields_and_keeps_redemption_count() {
        let model = serde_json::to_value(CatalogModel {
            id: 1,
            model_name: "fixture".to_owned(),
            description: String::new(),
            icon: String::new(),
            tags: String::new(),
            vendor_id: 0,
            endpoints: String::new(),
            status: 1,
            sync_official: 1,
            created_time: 10,
            updated_time: 11,
            name_rule: 0,
            bound_channels: Vec::new(),
            enable_groups: Vec::new(),
            quota_types: Vec::new(),
            matched_models: Vec::new(),
            matched_count: 0,
        })
        .expect("model JSON");
        assert_eq!(
            model,
            json!({
                "id": 1,
                "model_name": "fixture",
                "status": 1,
                "sync_official": 1,
                "created_time": 10,
                "updated_time": 11,
                "name_rule": 0
            })
        );

        let vendor = serde_json::to_value(CatalogVendor {
            id: 2,
            name: "vendor".to_owned(),
            description: String::new(),
            icon: String::new(),
            status: 1,
            created_time: 10,
            updated_time: 11,
        })
        .expect("vendor JSON");
        assert_eq!(
            vendor,
            json!({
                "id": 2,
                "name": "vendor",
                "status": 1,
                "created_time": 10,
                "updated_time": 11
            })
        );

        let redemption = serde_json::to_value(CatalogRedemption {
            id: 3,
            user_id: 4,
            key: "key".to_owned(),
            status: 1,
            name: "trial".to_owned(),
            quota: 100,
            count: 0,
            created_time: 10,
            redeemed_time: 0,
            used_user_id: 0,
            expired_time: 0,
            deleted_at: Value::Null,
        })
        .expect("redemption JSON");
        assert_eq!(redemption["count"], 0);
        assert_eq!(redemption["DeletedAt"], Value::Null);
    }
}
