//! Legacy-compatible channel management routes.
//!
//! PostgreSQL is authoritative.  A successful mutation commits before the
//! Valkey generation bump; readers can therefore never observe a cache entry
//! for an uncommitted channel row.

use async_trait::async_trait;
use axum::{
    Json, Router,
    extract::{Path, Query, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{delete, get, post},
};
use redis::AsyncCommands;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{PgPool, Row};
use std::{collections::BTreeMap, sync::Arc};

const CHANNEL_CACHE_GENERATION: &str = "lmm:channels:generation";
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";

/// Authorization is deliberately injected: the HTTP auth slice owns the
/// bearer/session verifier and this route slice only owns channel policy.
#[async_trait]
pub trait ChannelAdminAuthorizer: Send + Sync {
    async fn authorize(
        &self,
        headers: &HeaderMap,
        action: ChannelAction,
    ) -> Result<(), ChannelError>;
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ChannelAction {
    Read,
    Write,
    SensitiveWrite,
    Operate,
}

#[derive(Clone)]
pub struct ChannelCoreState {
    pub pg: PgPool,
    pub valkey: redis::Client,
    pub authorizer: Arc<dyn ChannelAdminAuthorizer>,
    pub retry_times: i64,
}

pub fn router(state: ChannelCoreState) -> Router {
    Router::new()
        .route("/api/channel/", get(list).post(add).put(update))
        .route("/api/channel/search", get(search))
        .route("/api/channel/{id}", get(get_one).delete(remove))
        .route("/api/channel/{id}/status", post(status))
        .route("/api/channel/status/batch", post(status_batch))
        .route("/api/channel/batch", post(remove_batch))
        .route("/api/channel/copy/{id}", post(copy))
        .route("/api/channel/disabled", delete(remove_disabled))
        .route("/api/channel/models", get(list_models))
        .route("/api/channel/models_enabled", get(list_enabled_models))
        .route("/api/channel/ops", get(ops))
        .route("/api/channel/fix", post(fix_abilities))
        .route("/api/channel/multi_key/manage", post(manage_multi_keys))
        // Go's authentication middleware runs before JSON binding.  Keep a
        // listener-owned preflight here so malformed/underspecified bodies
        // cannot turn an anonymous request into Axum's 422 before the shared
        // dashboard authorizer returns the legacy 401 envelope.
        .layer(middleware::from_fn_with_state(
            state.clone(),
            channel_auth_boundary,
        ))
        .with_state(state)
}

fn channel_action_for_request(request: &Request) -> ChannelAction {
    let path = request.uri().path();
    if request.method() == axum::http::Method::GET {
        return ChannelAction::Read;
    }
    if path.ends_with("/status") || path.ends_with("/status/batch") {
        return ChannelAction::Operate;
    }
    if path == "/api/channel/" && request.method() == axum::http::Method::PUT {
        return ChannelAction::Write;
    }
    if path == "/api/channel/tag" {
        return ChannelAction::Write;
    }
    if path == "/api/channel/batch/tag"
        || path == "/api/channel/tag/disabled"
        || path == "/api/channel/tag/enabled"
    {
        return ChannelAction::Operate;
    }
    ChannelAction::SensitiveWrite
}

async fn channel_auth_boundary(
    axum::extract::State(state): axum::extract::State<ChannelCoreState>,
    request: Request,
    next: Next,
) -> Response {
    let action = channel_action_for_request(&request);
    if let Err(error) = state.authorizer.authorize(request.headers(), action).await {
        return error.legacy();
    }
    let mut response = next.run(request).await;
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

#[derive(Debug, Serialize)]
struct Envelope<T: Serialize> {
    success: bool,
    message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<T>,
}
fn ok<T: Serialize>(data: T) -> Json<Envelope<T>> {
    Json(Envelope {
        success: true,
        message: String::new(),
        data: Some(data),
    })
}
fn empty() -> Json<Envelope<Value>> {
    Json(Envelope {
        success: true,
        message: String::new(),
        data: None,
    })
}

#[derive(Debug)]
pub enum ChannelError {
    /// The Go `ConsoleAccessGate` conceals channel discovery routes before
    /// AdminAuth sees anonymous, invalid, disabled, or unactivated users.
    ConsoleNotFound,
    Unauthorized,
    Forbidden,
    Invalid(&'static str),
    NotFound,
    Database,
    Cache,
}
impl ChannelError {
    pub(crate) fn legacy(self) -> Response {
        if matches!(&self, Self::ConsoleNotFound) {
            return (StatusCode::NOT_FOUND, Json(json!({"message":"Not Found"}))).into_response();
        }
        if matches!(&self, Self::Unauthorized) {
            return (
                StatusCode::UNAUTHORIZED,
                Json(json!({"success":false,"message":"Unauthorized, invalid access token","code":"AUTH_UNAUTHORIZED"})),
            )
                .into_response();
        }
        if matches!(&self, Self::Forbidden) {
            return (
                StatusCode::FORBIDDEN,
                Json(json!({"success":false,"message":"管理员权限不足"})),
            )
                .into_response();
        }
        let message = match self {
            Self::ConsoleNotFound | Self::Unauthorized | Self::Forbidden => unreachable!(),
            Self::Invalid(message) => message,
            Self::NotFound => "渠道不存在",
            Self::Database => "数据库错误",
            Self::Cache => "缓存失效失败",
        };
        // The legacy dashboard uses a 200 envelope for business failures.
        (
            StatusCode::OK,
            Json(Envelope::<Value> {
                success: false,
                message: message.to_owned(),
                data: None,
            }),
        )
            .into_response()
    }
}

async fn permit(
    state: &ChannelCoreState,
    headers: &HeaderMap,
    action: ChannelAction,
) -> Result<(), Response> {
    state
        .authorizer
        .authorize(headers, action)
        .await
        .map_err(ChannelError::legacy)
}

#[derive(Default, Deserialize)]
struct Page {
    p: Option<i64>,
    page_size: Option<i64>,
    group: Option<String>,
    status: Option<String>,
    #[serde(rename = "type")]
    channel_type: Option<i64>,
    id_sort: Option<bool>,
    sort_by: Option<String>,
    sort_order: Option<String>,
}
impl Page {
    fn page(&self) -> i64 {
        self.p.unwrap_or(1).max(1)
    }
    fn size(&self) -> i64 {
        match self.page_size {
            Some(0) | None => 20,
            Some(size) => size.max(1),
        }
    }
    fn status(&self) -> Option<i64> {
        match self.status.as_deref() {
            Some("enabled") | Some("1") => Some(1),
            Some("disabled") | Some("0") => Some(0),
            _ => None,
        }
    }
    fn order(&self) -> &'static str {
        match (self.sort_by.as_deref(), self.sort_order.as_deref()) {
            (Some("id"), Some("asc")) => "id ASC",
            (Some("id"), _) => "id DESC",
            (Some("name"), Some("asc")) => "name ASC, id ASC",
            (Some("name"), _) => "name DESC, id DESC",
            (Some("priority"), Some("asc")) => "priority ASC NULLS LAST, id ASC",
            (Some("priority"), _) => "priority DESC NULLS LAST, id ASC",
            (Some("balance"), Some("asc")) => "balance ASC NULLS LAST, id ASC",
            (Some("balance"), _) => "balance DESC NULLS LAST, id ASC",
            (Some("response_time"), Some("asc")) => "response_time ASC NULLS LAST, id ASC",
            (Some("response_time"), _) => "response_time DESC NULLS LAST, id ASC",
            (Some("test_time"), Some("asc")) => "test_time ASC NULLS LAST, id ASC",
            (Some("test_time"), _) => "test_time DESC NULLS LAST, id ASC",
            _ if self.id_sort.unwrap_or(false) => "id DESC",
            _ => "priority DESC NULLS LAST, id ASC",
        }
    }
}

async fn list(
    State(state): State<ChannelCoreState>,
    headers: HeaderMap,
    Query(page): Query<Page>,
) -> Result<Json<Envelope<Value>>, Response> {
    permit(&state, &headers, ChannelAction::Read).await?;
    let status = page.status();
    let group = group_filter_pattern(page.group.clone());
    let total: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM channels WHERE ($1::text IS NULL OR (',' || \"group\" || ',') LIKE $1 ESCAPE '!') AND ($2::bigint IS NULL OR ($2 = 1 AND status = 1) OR ($2 = 0 AND status <> 1)) AND ($3::bigint IS NULL OR type = $3)")
        .bind(group.as_deref()).bind(status).bind(page.channel_type).fetch_one(&state.pg).await.map_err(|_| ChannelError::Database.legacy())?;
    let rows = sqlx::query(&format!("SELECT to_jsonb(channels) - 'key' AS channel FROM channels WHERE ($1::text IS NULL OR (',' || \"group\" || ',') LIKE $1 ESCAPE '!') AND ($2::bigint IS NULL OR ($2 = 1 AND status = 1) OR ($2 = 0 AND status <> 1)) AND ($3::bigint IS NULL OR type = $3) ORDER BY {} LIMIT $4 OFFSET $5", page.order()))
        .bind(group.as_deref()).bind(status).bind(page.channel_type).bind(page.size()).bind((page.page() - 1) * page.size()).fetch_all(&state.pg).await.map_err(|_| ChannelError::Database.legacy())?;
    let items: Vec<Value> = rows
        .into_iter()
        .filter_map(|row| row.try_get("channel").ok())
        .map(redact_channel_info)
        .collect();
    let counts = type_counts(&state.pg, group.as_deref(), status)
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    Ok(ok(
        json!({"items": items, "total": total, "page": page.page(), "page_size": page.size(), "type_counts": counts}),
    ))
}

#[derive(Default, Deserialize)]
struct Search {
    #[serde(flatten)]
    page: Page,
    keyword: Option<String>,
    model: Option<String>,
}
async fn search(
    State(state): State<ChannelCoreState>,
    headers: HeaderMap,
    Query(query): Query<Search>,
) -> Result<Json<Envelope<Value>>, Response> {
    permit(&state, &headers, ChannelAction::Read).await?;
    let status = query.page.status();
    let group = group_filter_pattern(query.page.group.clone());
    let keyword = query.keyword.unwrap_or_default();
    let model = query.model.unwrap_or_default();
    let pattern = format!("%{keyword}%");
    let models = format!("%{model}%");
    let rows = sqlx::query(&format!("SELECT to_jsonb(channels) - 'key' AS channel FROM channels WHERE ($1::text IS NULL OR (',' || \"group\" || ',') LIKE $1 ESCAPE '!') AND ($2::bigint IS NULL OR ($2 = 1 AND status = 1) OR ($2 = 0 AND status <> 1)) AND ($3 = '' OR CAST(id AS text) = $3 OR name ILIKE $4 OR key = $3 OR base_url ILIKE $4) AND models ILIKE $5 ORDER BY {}", query.page.order()))
        .bind(group.as_deref()).bind(status).bind(&keyword).bind(pattern).bind(models).fetch_all(&state.pg).await.map_err(|_| ChannelError::Database.legacy())?;
    let all_unfiltered: Vec<Value> = rows
        .into_iter()
        .filter_map(|row| row.try_get("channel").ok())
        .map(redact_channel_info)
        .collect();
    let counts = all_unfiltered
        .iter()
        .filter_map(|value| value.get("type").and_then(Value::as_i64))
        .fold(BTreeMap::<i64, i64>::new(), |mut map, value| {
            *map.entry(value).or_default() += 1;
            map
        });
    let all: Vec<Value> = all_unfiltered
        .into_iter()
        .filter(|value| {
            query.page.channel_type.is_none()
                || value.get("type").and_then(Value::as_i64) == query.page.channel_type
        })
        .collect();
    let total = all.len();
    let start = usize::try_from((query.page.page() - 1) * query.page.size())
        .unwrap_or(usize::MAX)
        .min(total);
    let end = start
        .saturating_add(usize::try_from(query.page.size()).unwrap_or(100))
        .min(total);
    Ok(ok(
        json!({"items": &all[start..end], "total": total, "type_counts": counts}),
    ))
}

async fn get_one(
    State(state): State<ChannelCoreState>,
    headers: HeaderMap,
    Path(id): Path<i64>,
) -> Result<Json<Envelope<Value>>, Response> {
    permit(&state, &headers, ChannelAction::Read).await?;
    let row =
        sqlx::query("SELECT to_jsonb(channels) - 'key' AS channel FROM channels WHERE id = $1")
            .bind(id)
            .fetch_optional(&state.pg)
            .await
            .map_err(|_| ChannelError::Database.legacy())?
            .ok_or_else(|| ChannelError::NotFound.legacy())?;
    Ok(ok(redact_channel_info(
        row.try_get("channel")
            .map_err(|_| ChannelError::Database.legacy())?,
    )))
}

#[derive(Clone, Deserialize)]
struct ChannelInput {
    #[serde(default)]
    id: i64,
    #[serde(default)]
    r#type: i64,
    #[serde(default)]
    key: String,
    #[serde(default = "enabled")]
    status: i64,
    #[serde(default)]
    name: String,
    #[serde(default)]
    base_url: String,
    #[serde(default)]
    models: String,
    #[serde(default = "default_group", rename = "group")]
    group_name: String,
    #[serde(default)]
    priority: i64,
    #[serde(default)]
    weight: i64,
    #[serde(default)]
    tag: String,
    #[serde(default)]
    settings: String,
    #[serde(default)]
    setting: String,
    #[serde(default)]
    channel_info: Value,
    #[serde(default)]
    other: String,
    #[serde(default)]
    model_mapping: String,
    #[serde(default)]
    status_code_mapping: String,
    #[serde(default)]
    param_override: String,
    #[serde(default)]
    header_override: String,
    #[serde(default)]
    remark: String,
}
fn enabled() -> i64 {
    1
}
fn default_group() -> String {
    "default".to_owned()
}
impl ChannelInput {
    fn validate(&self, create: bool) -> Result<(), ChannelError> {
        if create && self.key.trim().is_empty() {
            return Err(ChannelError::Invalid("channel cannot be empty"));
        }
        if self.name.len() > 255 || self.remark.len() > 255 {
            return Err(ChannelError::Invalid("参数错误"));
        }
        if !matches!(self.status, 0..=3) {
            return Err(ChannelError::Invalid("参数错误"));
        }
        validate_json_object(&self.setting, "渠道额外设置[channel setting] 格式错误")?;
        validate_json_object(&self.settings, "渠道额外设置[channel setting] 格式错误")?;
        if self.r#type == 60 && self.base_url.trim().is_empty() {
            return Err(ChannelError::Invalid(
                "compatible relay channel base URL cannot be empty",
            ));
        }
        if self.r#type == 41 {
            if self.other.trim().is_empty() {
                return Err(ChannelError::Invalid("部署地区不能为空"));
            }
            let other = serde_json::from_str::<Value>(&self.other).map_err(|_| {
                ChannelError::Invalid(
                    "部署地区必须是标准的Json格式，例如{\"default\": \"us-central1\", \"region2\": \"us-east1\"}",
                )
            })?;
            if !other.is_object() {
                return Err(ChannelError::Invalid(
                    "部署地区必须是标准的Json格式，例如{\"default\": \"us-central1\", \"region2\": \"us-east1\"}",
                ));
            }
            if other.get("default").is_none() {
                return Err(ChannelError::Invalid("部署地区必须包含default字段"));
            }
        }
        if self.r#type == 57 && (create || !self.key.trim().is_empty()) {
            let key = serde_json::from_str::<Value>(self.key.trim())
                .ok()
                .filter(Value::is_object)
                .ok_or(ChannelError::Invalid(
                    "Codex key must be a valid JSON object",
                ))?;
            if !non_empty_json_field(&key, "access_token") {
                return Err(ChannelError::Invalid(
                    "Codex key JSON must include access_token",
                ));
            }
            if !non_empty_json_field(&key, "account_id") {
                return Err(ChannelError::Invalid(
                    "Codex key JSON must include account_id",
                ));
            }
        }
        if create && self.models.split(',').any(|model| model.len() > 255) {
            return Err(ChannelError::Invalid("模型名称过长"));
        }
        Ok(())
    }
}

fn validate_json_object(value: &str, message: &'static str) -> Result<(), ChannelError> {
    if value.trim().is_empty() {
        return Ok(());
    }
    serde_json::from_str::<Value>(value)
        .ok()
        .filter(Value::is_object)
        .map(|_| ())
        .ok_or(ChannelError::Invalid(message))
}

fn non_empty_json_field(value: &Value, field: &str) -> bool {
    value.get(field).is_some_and(|value| match value {
        Value::Null => false,
        Value::String(value) => !value.trim().is_empty(),
        value => !value.to_string().trim().is_empty(),
    })
}
#[derive(Deserialize)]
struct AddRequest {
    mode: String,
    channel: ChannelInput,
    #[serde(default)]
    multi_key_mode: String,
    #[serde(default)]
    batch_add_set_key_prefix_2_name: bool,
}
async fn add(
    State(state): State<ChannelCoreState>,
    headers: HeaderMap,
    Json(request): Json<AddRequest>,
) -> Result<Json<Envelope<Value>>, Response> {
    permit(&state, &headers, ChannelAction::SensitiveWrite).await?;
    request
        .channel
        .validate(true)
        .map_err(ChannelError::legacy)?;
    let keys: Vec<String> = match request.mode.as_str() {
        "single" => vec![request.channel.key.trim().to_owned()],
        "multi_to_single" => request
            .channel
            .key
            .lines()
            .map(str::trim)
            .filter(|key| !key.is_empty())
            .map(ToOwned::to_owned)
            .collect(),
        "batch" => request
            .channel
            .key
            .lines()
            .map(str::trim)
            .filter(|key| !key.is_empty())
            .map(ToOwned::to_owned)
            .collect(),
        _ => return Err(ChannelError::Invalid("不支持的添加模式").legacy()),
    };
    if keys.is_empty() {
        return Err(ChannelError::Invalid("channel cannot be empty").legacy());
    }
    let mut transaction = state
        .pg
        .begin()
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    for key in &keys {
        let mut channel = request.channel.clone();
        channel.key = key.clone();
        if request.batch_add_set_key_prefix_2_name && keys.len() > 1 {
            let prefix: String = key.chars().take(8).collect();
            channel.name = format!("{} {prefix}", channel.name);
        }
        if request.mode == "multi_to_single" {
            channel.channel_info = json!({
                "is_multi_key": true,
                "multi_key_size": keys.len(),
                "multi_key_mode": &request.multi_key_mode,
            });
            channel.key = keys.join("\n");
        }
        let channel_id = insert(&mut transaction, &channel)
            .await
            .map_err(|_| ChannelError::Database.legacy())?;
        recreate_abilities(&mut transaction, &[channel_id])
            .await
            .map_err(|_| ChannelError::Database.legacy())?;
        if request.mode == "multi_to_single" {
            break;
        }
    }
    transaction
        .commit()
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    invalidate(&state).await.map_err(ChannelError::legacy)?;
    Ok(empty())
}

async fn update(
    State(state): State<ChannelCoreState>,
    headers: HeaderMap,
    Json(raw): Json<Value>,
) -> Result<Json<Envelope<Value>>, Response> {
    permit(&state, &headers, ChannelAction::Write).await?;
    let fields = raw
        .as_object()
        .ok_or_else(|| ChannelError::Invalid("参数错误").legacy())?;
    if fields.contains_key("status") {
        return Err(ChannelError::Invalid(invalid_parameters_message(&headers)).legacy());
    }
    if update_requires_sensitive_write(fields) {
        permit(&state, &headers, ChannelAction::SensitiveWrite).await?;
    }
    let channel: ChannelInput =
        serde_json::from_value(raw).map_err(|_| ChannelError::Invalid("参数错误").legacy())?;
    if channel.id <= 0 {
        return Err(ChannelError::Invalid("参数错误").legacy());
    }
    channel.validate(false).map_err(ChannelError::legacy)?;
    let mut transaction = state
        .pg
        .begin()
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    // Legacy updates retain server-owned multi-key state even when an older
    // dashboard payload omits it.  Only dedicated multi-key operations mutate
    // `channel_info` after serializing on the row.
    let result = sqlx::query("UPDATE channels SET type=$2, key=CASE WHEN $3='' THEN key ELSE $3 END, name=$4, base_url=$5, models=$6, \"group\"=$7, priority=$8, weight=$9, tag=NULLIF($10,''), settings=$11, setting=$12, other=$13, model_mapping=$14, status_code_mapping=$15, param_override=$16, header_override=$17, remark=NULLIF($18,'') WHERE id=$1")
        .bind(channel.id).bind(channel.r#type).bind(&channel.key).bind(&channel.name).bind(&channel.base_url).bind(&channel.models).bind(&channel.group_name).bind(channel.priority).bind(channel.weight).bind(&channel.tag).bind(&channel.settings).bind(&channel.setting).bind(&channel.other).bind(&channel.model_mapping).bind(&channel.status_code_mapping).bind(&channel.param_override).bind(&channel.header_override).bind(&channel.remark).execute(&mut *transaction).await.map_err(|_| ChannelError::Database.legacy())?;
    if result.rows_affected() != 1 {
        return Err(ChannelError::NotFound.legacy());
    }
    recreate_abilities(&mut transaction, &[channel.id])
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    transaction
        .commit()
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    invalidate(&state).await.map_err(ChannelError::legacy)?;
    get_one(State(state), headers, Path(channel.id)).await
}

fn update_requires_sensitive_write(fields: &serde_json::Map<String, Value>) -> bool {
    const SENSITIVE: &[&str] = &[
        "type",
        "key",
        "base_url",
        "openai_organization",
        "header_override",
        "param_override",
        "setting",
        "other",
        "settings",
        "key_mode",
    ];
    const NON_SENSITIVE: &[&str] = &[
        "id",
        "test_model",
        "name",
        "weight",
        "models",
        "group",
        "model_mapping",
        "status_code_mapping",
        "priority",
        "auto_ban",
        "other_info",
        "tag",
        "remark",
        "channel_info",
        "multi_key_mode",
        "created_time",
        "test_time",
        "response_time",
        "balance",
        "balance_updated_time",
        "used_quota",
    ];
    fields.keys().any(|field| {
        SENSITIVE.contains(&field.as_str()) || !NON_SENSITIVE.contains(&field.as_str())
    })
}

async fn remove(
    State(state): State<ChannelCoreState>,
    headers: HeaderMap,
    Path(id): Path<i64>,
) -> Result<Json<Envelope<Value>>, Response> {
    permit(&state, &headers, ChannelAction::SensitiveWrite).await?;
    let mut transaction = state
        .pg
        .begin()
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    let exists: Option<i64> = sqlx::query_scalar("SELECT id FROM channels WHERE id=$1 FOR UPDATE")
        .bind(id)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    if exists.is_none() {
        return Err(ChannelError::NotFound.legacy());
    }
    sqlx::query("DELETE FROM abilities WHERE channel_id=$1")
        .bind(id)
        .execute(&mut *transaction)
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    sqlx::query("DELETE FROM channels WHERE id=$1")
        .bind(id)
        .execute(&mut *transaction)
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    transaction
        .commit()
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    invalidate(&state).await.map_err(ChannelError::legacy)?;
    Ok(empty())
}
#[derive(Deserialize)]
struct Ids {
    ids: Vec<i64>,
}
async fn remove_batch(
    State(state): State<ChannelCoreState>,
    headers: HeaderMap,
    Json(request): Json<Ids>,
) -> Result<Json<Envelope<Value>>, Response> {
    permit(&state, &headers, ChannelAction::SensitiveWrite).await?;
    if request.ids.is_empty() {
        return Err(ChannelError::Invalid("参数错误").legacy());
    }
    let mut tx = state
        .pg
        .begin()
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    let ids: Vec<i64> = sqlx::query_scalar("SELECT id FROM channels WHERE id = ANY($1) FOR UPDATE")
        .bind(&request.ids)
        .fetch_all(&mut *tx)
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    let deleted = ids.len();
    sqlx::query("DELETE FROM abilities WHERE channel_id = ANY($1)")
        .bind(&ids)
        .execute(&mut *tx)
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    sqlx::query("DELETE FROM channels WHERE id = ANY($1)")
        .bind(&ids)
        .execute(&mut *tx)
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    tx.commit()
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    if deleted > 0 {
        invalidate(&state).await.map_err(ChannelError::legacy)?;
    }
    Ok(ok(json!(deleted)))
}
#[derive(Deserialize)]
struct CopyQuery {
    suffix: Option<String>,
    reset_balance: Option<bool>,
}
async fn copy(
    State(state): State<ChannelCoreState>,
    headers: HeaderMap,
    Path(id): Path<i64>,
    Query(query): Query<CopyQuery>,
) -> Result<Json<Envelope<Value>>, Response> {
    permit(&state, &headers, ChannelAction::SensitiveWrite).await?;
    let suffix = query.suffix.unwrap_or_else(|| "_复制".to_owned());
    let reset = query.reset_balance.unwrap_or(true);
    let mut transaction = state
        .pg
        .begin()
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    let row = sqlx::query("INSERT INTO channels (type,key,open_ai_organization,test_model,status,name,weight,created_time,test_time,response_time,base_url,other,balance,balance_updated_time,models,\"group\",used_quota,model_mapping,status_code_mapping,priority,auto_ban,other_info,tag,setting,param_override,header_override,remark,channel_info,settings) SELECT type,key,open_ai_organization,test_model,status,name || $2,weight,EXTRACT(EPOCH FROM CURRENT_TIMESTAMP)::bigint,0,0,base_url,other,CASE WHEN $3 THEN 0 ELSE balance END,0,models,\"group\",CASE WHEN $3 THEN 0 ELSE used_quota END,model_mapping,status_code_mapping,priority,auto_ban,other_info,tag,setting,param_override,header_override,remark,channel_info,settings FROM channels WHERE id=$1 RETURNING id").bind(id).bind(suffix).bind(reset).fetch_optional(&mut *transaction).await.map_err(|_| ChannelError::Database.legacy())?.ok_or_else(|| ChannelError::NotFound.legacy())?;
    let new_id: i64 = row
        .try_get("id")
        .map_err(|_| ChannelError::Database.legacy())?;
    recreate_abilities(&mut transaction, &[new_id])
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    transaction
        .commit()
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    invalidate(&state).await.map_err(ChannelError::legacy)?;
    Ok(ok(json!({"id": new_id})))
}

async fn remove_disabled(
    State(state): State<ChannelCoreState>,
    headers: HeaderMap,
) -> Result<Json<Envelope<Value>>, Response> {
    permit(&state, &headers, ChannelAction::SensitiveWrite).await?;
    let mut transaction = state
        .pg
        .begin()
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    let ids: Vec<i64> =
        sqlx::query_scalar("SELECT id FROM channels WHERE status IN (2, 3) FOR UPDATE")
            .fetch_all(&mut *transaction)
            .await
            .map_err(|_| ChannelError::Database.legacy())?;
    if !ids.is_empty() {
        sqlx::query("DELETE FROM abilities WHERE channel_id = ANY($1)")
            .bind(&ids)
            .execute(&mut *transaction)
            .await
            .map_err(|_| ChannelError::Database.legacy())?;
        sqlx::query("DELETE FROM channels WHERE id = ANY($1)")
            .bind(&ids)
            .execute(&mut *transaction)
            .await
            .map_err(|_| ChannelError::Database.legacy())?;
    }
    transaction
        .commit()
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    if !ids.is_empty() {
        invalidate(&state).await.map_err(ChannelError::legacy)?;
    }
    Ok(ok(json!(ids.len())))
}

async fn list_models(
    State(state): State<ChannelCoreState>,
    headers: HeaderMap,
) -> Result<Json<Envelope<Vec<String>>>, Response> {
    permit(&state, &headers, ChannelAction::Read).await?;
    let models = all_models(&state.pg, false)
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    Ok(ok(models))
}

async fn list_enabled_models(
    State(state): State<ChannelCoreState>,
    headers: HeaderMap,
) -> Result<Json<Envelope<Vec<String>>>, Response> {
    permit(&state, &headers, ChannelAction::Read).await?;
    let models = all_models(&state.pg, true)
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    Ok(ok(models))
}

async fn all_models(pool: &PgPool, enabled_only: bool) -> Result<Vec<String>, sqlx::Error> {
    let rows = sqlx::query(
        "SELECT DISTINCT trim(model) AS model FROM channels CROSS JOIN LATERAL unnest(string_to_array(COALESCE(models, ''), ',')) AS split(model) WHERE trim(model) <> '' AND ($1 = FALSE OR status = 1) ORDER BY model ASC",
    )
    .bind(enabled_only)
    .fetch_all(pool)
    .await?;
    rows.into_iter().map(|row| row.try_get("model")).collect()
}

async fn ops(
    State(state): State<ChannelCoreState>,
    headers: HeaderMap,
) -> Result<Json<Envelope<Value>>, Response> {
    permit(&state, &headers, ChannelAction::Read).await?;
    Ok(ok(json!({"retry_times": state.retry_times})))
}

async fn fix_abilities(
    State(state): State<ChannelCoreState>,
    headers: HeaderMap,
) -> Result<Json<Envelope<Value>>, Response> {
    permit(&state, &headers, ChannelAction::Operate).await?;
    let mut transaction = state
        .pg
        .begin()
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    let ids: Vec<i64> = sqlx::query_scalar("SELECT id FROM channels ORDER BY id")
        .fetch_all(&mut *transaction)
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    recreate_abilities(&mut transaction, &ids)
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    transaction
        .commit()
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    invalidate(&state).await.map_err(ChannelError::legacy)?;
    Ok(ok(json!({"success": ids.len(), "fails": 0})))
}

#[derive(Deserialize)]
struct MultiKeyRequest {
    channel_id: i64,
    action: String,
    key_index: Option<usize>,
    page: Option<usize>,
    page_size: Option<usize>,
    status: Option<i64>,
}

async fn manage_multi_keys(
    State(state): State<ChannelCoreState>,
    headers: HeaderMap,
    Json(request): Json<MultiKeyRequest>,
) -> Result<Json<Envelope<Value>>, Response> {
    let action = match request.action.as_str() {
        "get_key_status"
        | "disable_key"
        | "enable_key"
        | "enable_all_keys"
        | "disable_all_keys"
        | "delete_key"
        | "delete_disabled_keys" => request.action.as_str(),
        _ => return Ok(failure_envelope("不支持的操作")),
    };
    permit(
        &state,
        &headers,
        if matches!(action, "delete_key" | "delete_disabled_keys") {
            ChannelAction::SensitiveWrite
        } else {
            ChannelAction::Operate
        },
    )
    .await?;
    let mut transaction = state
        .pg
        .begin()
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    // This transaction-scoped lock serializes writers across Rust instances;
    // it is stronger than the legacy process-local polling mutex.
    sqlx::query("SELECT pg_advisory_xact_lock($1)")
        .bind(request.channel_id)
        .execute(&mut *transaction)
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    let row = sqlx::query("SELECT key, channel_info FROM channels WHERE id=$1 FOR UPDATE")
        .bind(request.channel_id)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(|_| ChannelError::Database.legacy())?
        .ok_or_else(|| ChannelError::NotFound.legacy())?;
    let key: String = row
        .try_get("key")
        .map_err(|_| ChannelError::Database.legacy())?;
    let mut info: Value = row
        .try_get::<Option<Value>, _>("channel_info")
        .map_err(|_| ChannelError::Database.legacy())?
        .unwrap_or_else(|| json!({}));
    if !info
        .get("is_multi_key")
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        return Ok(failure_envelope("该渠道不是多密钥模式"));
    }
    let mut keys = channel_keys(&key);
    let object = info
        .as_object_mut()
        .ok_or_else(|| ChannelError::Invalid("参数错误").legacy())?;
    let mut statuses = number_map(object.remove("multi_key_status_list"));
    let mut times = number_map(object.remove("multi_key_disabled_time"));
    let mut reasons = string_map(object.remove("multi_key_disabled_reason"));
    let response = match action {
        "get_key_status" => multi_key_status_response(
            &keys,
            &statuses,
            &times,
            &reasons,
            request.page,
            request.page_size,
            request.status,
        ),
        "disable_key" => {
            let index = valid_key_index(request.key_index, keys.len(), "disable_key")
                .map_err(|message| failure_envelope(message).into_response())?;
            statuses.insert(index, 2);
            json!({"message":"密钥已禁用"})
        }
        "enable_key" => {
            let index = valid_key_index(request.key_index, keys.len(), "enable_key")
                .map_err(|message| failure_envelope(message).into_response())?;
            statuses.remove(&index);
            times.remove(&index);
            reasons.remove(&index);
            json!({"message":"密钥已启用"})
        }
        "enable_all_keys" => {
            let count = statuses.len();
            statuses.clear();
            times.clear();
            reasons.clear();
            json!({"message":format!("已启用 {count} 个密钥")})
        }
        "disable_all_keys" => {
            let mut count = 0;
            for index in 0..keys.len() {
                if statuses.get(&index).copied().unwrap_or(1) == 1 {
                    statuses.insert(index, 2);
                    count += 1;
                }
            }
            if count == 0 {
                return Ok(failure_envelope("没有可禁用的密钥"));
            }
            json!({"message":format!("已禁用 {count} 个密钥")})
        }
        "delete_key" => {
            let index = valid_key_index(request.key_index, keys.len(), "delete_key")
                .map_err(|message| failure_envelope(message).into_response())?;
            if keys.len() == 1 {
                return Ok(failure_envelope("不能删除最后一个密钥"));
            }
            keys.remove(index);
            remap_key_metadata(&mut statuses, &mut times, &mut reasons, index);
            json!({"message":"密钥已删除"})
        }
        "delete_disabled_keys" => {
            let deleted = keys
                .iter()
                .enumerate()
                .filter(|(index, _)| statuses.get(index).copied() == Some(3))
                .count();
            if deleted == 0 {
                return Ok(failure_envelope("没有需要删除的自动禁用密钥"));
            }
            let keep: Vec<usize> = keys
                .iter()
                .enumerate()
                .filter_map(|(index, _)| {
                    (statuses.get(&index).copied() != Some(3)).then_some(index)
                })
                .collect();
            keys = keep.iter().map(|index| keys[*index].clone()).collect();
            remap_to_kept(&mut statuses, &mut times, &mut reasons, &keep);
            json!({"message":format!("已删除 {deleted} 个自动禁用的密钥"),"data":deleted})
        }
        _ => unreachable!(),
    };
    if action != "get_key_status" {
        object.insert("multi_key_size".to_owned(), json!(keys.len()));
        object.insert("multi_key_status_list".to_owned(), json!(statuses));
        object.insert("multi_key_disabled_time".to_owned(), json!(times));
        object.insert("multi_key_disabled_reason".to_owned(), json!(reasons));
        sqlx::query("UPDATE channels SET key=$2, channel_info=$3 WHERE id=$1")
            .bind(request.channel_id)
            .bind(keys.join("\n"))
            .bind(&info)
            .execute(&mut *transaction)
            .await
            .map_err(|_| ChannelError::Database.legacy())?;
    }
    transaction
        .commit()
        .await
        .map_err(|_| ChannelError::Database.legacy())?;
    if action != "get_key_status" {
        invalidate(&state).await.map_err(ChannelError::legacy)?;
    }
    if action == "get_key_status" {
        return Ok(ok(response));
    }
    let message = response
        .get("message")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_owned();
    Ok(Json(Envelope {
        success: true,
        message,
        data: response.get("data").cloned(),
    }))
}

fn failure_envelope(message: &str) -> Json<Envelope<Value>> {
    Json(Envelope {
        success: false,
        message: message.to_owned(),
        data: None,
    })
}

fn channel_keys(value: &str) -> Vec<String> {
    serde_json::from_str::<Vec<Value>>(value)
        .ok()
        .map(|values| {
            values
                .into_iter()
                .map(|value| match value {
                    Value::String(value) => value,
                    other => other.to_string(),
                })
                .collect()
        })
        .unwrap_or_else(|| {
            value
                .trim_matches('\n')
                .lines()
                .map(str::to_owned)
                .collect()
        })
}

fn number_map(value: Option<Value>) -> BTreeMap<usize, i64> {
    value
        .and_then(|value| serde_json::from_value(value).ok())
        .unwrap_or_default()
}
fn string_map(value: Option<Value>) -> BTreeMap<usize, String> {
    value
        .and_then(|value| serde_json::from_value(value).ok())
        .unwrap_or_default()
}
fn valid_key_index(
    index: Option<usize>,
    count: usize,
    action: &str,
) -> Result<usize, &'static str> {
    let Some(index) = index else {
        return Err(match action {
            "enable_key" => "未指定要启用的密钥索引",
            "delete_key" => "未指定要删除的密钥索引",
            _ => "未指定要禁用的密钥索引",
        });
    };
    if index < count {
        Ok(index)
    } else {
        Err("密钥索引超出范围")
    }
}
fn remap_key_metadata(
    statuses: &mut BTreeMap<usize, i64>,
    times: &mut BTreeMap<usize, i64>,
    reasons: &mut BTreeMap<usize, String>,
    removed: usize,
) {
    let keep: Vec<_> = (0..statuses
        .keys()
        .chain(times.keys())
        .chain(reasons.keys())
        .max()
        .copied()
        .unwrap_or(0)
        + 1)
        .filter(|index| *index != removed)
        .collect();
    remap_to_kept(statuses, times, reasons, &keep);
}
fn remap_to_kept(
    statuses: &mut BTreeMap<usize, i64>,
    times: &mut BTreeMap<usize, i64>,
    reasons: &mut BTreeMap<usize, String>,
    keep: &[usize],
) {
    let old_statuses = std::mem::take(statuses);
    let old_times = std::mem::take(times);
    let old_reasons = std::mem::take(reasons);
    for (new_index, old_index) in keep.iter().enumerate() {
        if let Some(value) = old_statuses.get(old_index) {
            statuses.insert(new_index, *value);
        }
        if let Some(value) = old_times.get(old_index) {
            times.insert(new_index, *value);
        }
        if let Some(value) = old_reasons.get(old_index) {
            reasons.insert(new_index, value.clone());
        }
    }
}
fn multi_key_status_response(
    keys: &[String],
    statuses: &BTreeMap<usize, i64>,
    times: &BTreeMap<usize, i64>,
    reasons: &BTreeMap<usize, String>,
    page: Option<usize>,
    page_size: Option<usize>,
    filter: Option<i64>,
) -> Value {
    let page_size = page_size.unwrap_or(50).max(1);
    let mut entries: Vec<Value> = keys
        .iter()
        .enumerate()
        .map(|(index, key)| {
            let status = statuses.get(&index).copied().unwrap_or(1);
            let preview = if key.chars().count() > 10 {
                format!("{}...", key.chars().take(10).collect::<String>())
            } else {
                key.clone()
            };
            let mut value = json!({"index":index,"status":status,"key_preview":preview});
            if status != 1 {
                if let Some(time) = times.get(&index) {
                    value["disabled_time"] = json!(time);
                }
                if let Some(reason) = reasons.get(&index) {
                    value["reason"] = json!(reason);
                }
            }
            value
        })
        .collect();
    let enabled = entries.iter().filter(|entry| entry["status"] == 1).count();
    let manual = entries.iter().filter(|entry| entry["status"] == 2).count();
    let automatic = entries.iter().filter(|entry| entry["status"] == 3).count();
    if let Some(filter) = filter {
        entries.retain(|entry| entry["status"] == filter);
    }
    let total = entries.len();
    let total_pages = total.div_ceil(page_size).max(1);
    let page = page.unwrap_or(1).clamp(1, total_pages);
    let start = (page - 1) * page_size;
    json!({"keys":entries.into_iter().skip(start).take(page_size).collect::<Vec<_>>(),"total":total,"page":page,"page_size":page_size,"total_pages":total_pages,"enabled_count":enabled,"manual_disabled_count":manual,"auto_disabled_count":automatic})
}
#[derive(Deserialize)]
struct Status {
    status: i64,
}
async fn status(
    State(state): State<ChannelCoreState>,
    headers: HeaderMap,
    Path(id): Path<i64>,
    Json(request): Json<Status>,
) -> Result<Json<Envelope<Value>>, Response> {
    permit(&state, &headers, ChannelAction::Operate).await?;
    change_status(
        &state,
        &[id],
        request.status,
        invalid_parameters_message(&headers),
    )
    .await
    .map_err(ChannelError::legacy)
    .map(|changed| ok(json!(changed > 0)))
}
async fn status_batch(
    State(state): State<ChannelCoreState>,
    headers: HeaderMap,
    Json(request): Json<StatusBatch>,
) -> Result<Json<Envelope<Value>>, Response> {
    permit(&state, &headers, ChannelAction::Operate).await?;
    if request.ids.is_empty() {
        return Err(ChannelError::Invalid("参数错误").legacy());
    }
    change_status(
        &state,
        &request.ids,
        request.status,
        invalid_parameters_message(&headers),
    )
    .await
    .map_err(ChannelError::legacy)
    .map(|changed| ok(json!(changed)))
}
#[derive(Deserialize)]
struct StatusBatch {
    ids: Vec<i64>,
    status: i64,
}
async fn change_status(
    state: &ChannelCoreState,
    ids: &[i64],
    value: i64,
    invalid_parameters: &'static str,
) -> Result<u64, ChannelError> {
    if !matches!(value, 1 | 2) {
        return Err(ChannelError::Invalid(invalid_parameters));
    }
    let mut transaction = state.pg.begin().await.map_err(|_| ChannelError::Database)?;
    let changed = sqlx::query("UPDATE channels SET status=$2 WHERE id = ANY($1) AND status <> $2")
        .bind(ids)
        .bind(value)
        .execute(&mut *transaction)
        .await
        .map_err(|_| ChannelError::Database)?
        .rows_affected();
    if changed > 0 {
        sqlx::query("UPDATE abilities SET enabled=$2 WHERE channel_id = ANY($1)")
            .bind(ids)
            .bind(value == 1)
            .execute(&mut *transaction)
            .await
            .map_err(|_| ChannelError::Database)?;
    }
    transaction
        .commit()
        .await
        .map_err(|_| ChannelError::Database)?;
    if changed > 0 {
        invalidate(state).await?;
    }
    Ok(changed)
}

// Dashboard identity middleware may supply a higher-priority user setting;
// this route-local boundary preserves Gin's Accept-Language fallback.
fn invalid_parameters_message(headers: &HeaderMap) -> &'static str {
    let language = headers
        .get("accept-language")
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.split(',').next())
        .map(str::trim)
        .unwrap_or_default()
        .to_ascii_lowercase();
    if language.starts_with("zh-tw") {
        "無效的參數"
    } else if language.starts_with("zh") {
        "无效的参数"
    } else {
        "Invalid parameters"
    }
}
async fn insert(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    channel: &ChannelInput,
) -> Result<i64, sqlx::Error> {
    sqlx::query_scalar("INSERT INTO channels (type,key,status,name,base_url,models,\"group\",priority,weight,tag,settings,setting,channel_info,other,model_mapping,status_code_mapping,param_override,header_override,remark,created_time) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11,$12,$13,$14,$15,$16,$17,$18,NULLIF($19,''),EXTRACT(EPOCH FROM CURRENT_TIMESTAMP)::bigint) RETURNING id").bind(channel.r#type).bind(&channel.key).bind(channel.status).bind(&channel.name).bind(&channel.base_url).bind(&channel.models).bind(&channel.group_name).bind(channel.priority).bind(channel.weight).bind(&channel.tag).bind(&channel.settings).bind(&channel.setting).bind(&channel.channel_info).bind(&channel.other).bind(&channel.model_mapping).bind(&channel.status_code_mapping).bind(&channel.param_override).bind(&channel.header_override).bind(&channel.remark).fetch_one(&mut **transaction).await
}

async fn recreate_abilities(
    transaction: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    ids: &[i64],
) -> Result<(), sqlx::Error> {
    if ids.is_empty() {
        return Ok(());
    }
    sqlx::query("DELETE FROM abilities WHERE channel_id = ANY($1)")
        .bind(ids)
        .execute(&mut **transaction)
        .await?;
    sqlx::query(
        r#"INSERT INTO abilities ("group", model, channel_id, enabled, priority, weight, tag)
           SELECT DISTINCT trim(groups.value), trim(models.value), c.id, c.status = 1,
                  COALESCE(c.priority, 0), COALESCE(c.weight, 0), c.tag
             FROM channels c
             CROSS JOIN LATERAL unnest(string_to_array(COALESCE(c.models, ''), ',')) AS models(value)
             CROSS JOIN LATERAL unnest(string_to_array(COALESCE(c."group", ''), ',')) AS groups(value)
            WHERE c.id = ANY($1) AND trim(models.value) <> '' AND trim(groups.value) <> ''
           ON CONFLICT ("group", model, channel_id) DO UPDATE
             SET enabled = EXCLUDED.enabled, priority = EXCLUDED.priority,
                 weight = EXCLUDED.weight, tag = EXCLUDED.tag"#,
    )
    .bind(ids)
    .execute(&mut **transaction)
    .await?;
    Ok(())
}
async fn invalidate(state: &ChannelCoreState) -> Result<(), ChannelError> {
    let mut connection = state
        .valkey
        .get_multiplexed_async_connection()
        .await
        .map_err(|_| ChannelError::Cache)?;
    let _: i64 = connection
        .incr(CHANNEL_CACHE_GENERATION, 1)
        .await
        .map_err(|_| ChannelError::Cache)?;
    Ok(())
}
async fn type_counts(
    pool: &PgPool,
    group: Option<&str>,
    status: Option<i64>,
) -> Result<BTreeMap<i64, i64>, sqlx::Error> {
    let rows = sqlx::query("SELECT type, COUNT(*) AS count FROM channels WHERE ($1::text IS NULL OR (',' || \"group\" || ',') LIKE $1 ESCAPE '!') AND ($2::bigint IS NULL OR ($2 = 1 AND status = 1) OR ($2 = 0 AND status <> 1)) GROUP BY type").bind(group).bind(status).fetch_all(pool).await?;
    Ok(rows
        .into_iter()
        .filter_map(|row| Some((row.try_get("type").ok()?, row.try_get("count").ok()?)))
        .collect())
}
fn normalized_group(value: Option<String>) -> Option<String> {
    value.and_then(|group| {
        let group = group.trim();
        (!group.is_empty()
            && !group.eq_ignore_ascii_case("all")
            && !group.eq_ignore_ascii_case("null"))
        .then(|| group.to_owned())
    })
}
fn group_filter_pattern(value: Option<String>) -> Option<String> {
    normalized_group(value).map(|group| {
        let escaped = group
            .replace('!', "!!")
            .replace('%', "!%")
            .replace('_', "!_");
        format!("%,{escaped},%")
    })
}
fn redact_channel_info(mut value: Value) -> Value {
    // GORM's `Omit("key")` leaves the model's JSON field at its zero value;
    // the frozen dashboard contract therefore exposes `"key":""`, not an
    // absent property.  Keep that shape without ever selecting the secret.
    if let Some(object) = value.as_object_mut() {
        object.insert("key".to_owned(), Value::String(String::new()));
    }
    if value
        .get("channel_info")
        .and_then(|info| info.get("is_multi_key"))
        .and_then(Value::as_bool)
        .unwrap_or(false)
    {
        for key in ["multi_key_disabled_reason", "multi_key_disabled_time"] {
            if let Some(object) = value.get_mut("channel_info").and_then(Value::as_object_mut) {
                object.remove(key);
            }
        }
    }
    value
}

#[cfg(test)]
mod tests {
    use serde::de::DeserializeOwned;

    use super::*;

    type TestResult<T = ()> = Result<T, Box<dyn std::error::Error>>;

    fn test_error(message: impl Into<String>) -> Box<dyn std::error::Error> {
        Box::new(std::io::Error::other(message.into()))
    }

    fn json_from_value<T: DeserializeOwned>(
        value: Value,
        context: &'static str,
    ) -> TestResult<T> {
        serde_json::from_value(value)
            .map_err(|error| test_error(format!("{context}: invalid JSON value: {error}")))
    }

    fn required<T>(value: Option<T>, context: &'static str) -> TestResult<T> {
        value.ok_or_else(|| test_error(format!("missing required value: {context}")))
    }

    #[test]
    fn group_normalization_rejects_legacy_all_values() {
        assert_eq!(normalized_group(Some(" all ".into())), None);
    }

    #[test]
    fn status_filter_accepts_legacy_enabled_and_disabled_forms() -> TestResult {
        let status = required(
            Page {
                status: Some("enabled".into()),
                ..Default::default()
            }
            .status(),
            "enabled status filter result",
        )?;
        assert_eq!(status, 1);
        Ok(())
    }

    #[test]
    fn unknown_status_matches_the_legacy_all_filter() {
        assert_eq!(
            Page {
                status: Some("future-status".into()),
                ..Default::default()
            }
            .status(),
            None
        );
    }

    #[test]
    fn group_filter_escapes_like_metacharacters() {
        assert_eq!(
            group_filter_pattern(Some(" team_%! ".into())).as_deref(),
            Some("%,team!_!%!!,%")
        );
    }

    #[test]
    fn redact_channel_info_never_returns_multikey_failure_details() {
        let channel = redact_channel_info(
            json!({"channel_info":{"is_multi_key":true,"multi_key_disabled_reason":{"0":"secret"},"multi_key_disabled_time":{"0":1}}}),
        );
        assert!(
            channel["channel_info"]
                .get("multi_key_disabled_reason")
                .is_none()
        );
    }

    #[test]
    fn ordinary_channel_info_is_not_redacted_as_multikey_state() {
        let channel = redact_channel_info(json!({"channel_info":{
            "multi_key_disabled_reason":{"0":"ordinary metadata"}
        }}));
        assert_eq!(
            channel["channel_info"]["multi_key_disabled_reason"]["0"],
            "ordinary metadata"
        );
    }

    #[test]
    fn provider_configuration_validation_matches_frozen_go_boundaries() -> TestResult {
        let new_api = ChannelInput {
            r#type: 60,
            key: "key".into(),
            ..json_from_value(json!({}), "default channel input")?
        };
        assert!(matches!(
            new_api.validate(true),
            Err(ChannelError::Invalid(
                "compatible relay channel base URL cannot be empty"
            ))
        ));

        let codex: ChannelInput = json_from_value(
            json!({
                "type":57,
                "key":"{\"access_token\":\"token\"}"
            }),
            "Codex channel input",
        )?;
        assert!(matches!(
            codex.validate(true),
            Err(ChannelError::Invalid(
                "Codex key JSON must include account_id"
            ))
        ));

        let vertex: ChannelInput = json_from_value(
            json!({
                "type":41,
                "key":"key",
                "other":"{}"
            }),
            "Vertex channel input",
        )?;
        assert!(matches!(
            vertex.validate(true),
            Err(ChannelError::Invalid("部署地区必须包含default字段"))
        ));
        Ok(())
    }

    #[test]
    fn update_fields_fail_closed_for_sensitive_and_unknown_names() -> TestResult {
        let harmless = json_from_value::<Value>(
            json!({"id":1,"name":"new"}),
            "non-sensitive update payload",
        )?;
        assert!(!update_requires_sensitive_write(required(
            harmless.as_object(),
            "non-sensitive update JSON object",
        )?));
        for value in [
            json!({"id":1,"base_url":"https://upstream"}),
            json!({"id":1,"future_secret":"x"}),
        ] {
            assert!(update_requires_sensitive_write(required(
                value.as_object(),
                "sensitive update JSON object",
            )?));
        }
        Ok(())
    }

    #[test]
    fn missing_multikey_indexes_keep_action_specific_legacy_messages() {
        assert_eq!(
            valid_key_index(None, 2, "disable_key"),
            Err("未指定要禁用的密钥索引")
        );
        assert_eq!(
            valid_key_index(None, 2, "enable_key"),
            Err("未指定要启用的密钥索引")
        );
        assert_eq!(
            valid_key_index(None, 2, "delete_key"),
            Err("未指定要删除的密钥索引")
        );
    }
}
