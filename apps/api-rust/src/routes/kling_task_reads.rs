//! Read-only compatibility routes for stored Kling video tasks.
//!
//! The legacy Kling aliases rewrite a normal GET to the generic video-task
//! fetch path, then run relay-token authentication and distribution.  A
//! successful read is nevertheless entirely local: it selects an owned row
//! from `tasks` and returns the provider-neutral task DTO.  This module keeps
//! that ordering without adding a provider client or any write side effects.

use std::{
    collections::{HashMap, HashSet},
    net::{IpAddr, Ipv4Addr},
    sync::Arc,
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{Path, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::get,
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{PgPool, Row, postgres::PgRow};

use crate::{
    RequestContext,
    models::{ModelsError, ModelsErrorKind, ModelsRequest, PgModelsService},
};

const MAX_REQUEST_BODY_BYTES: usize = 128 * 1024 * 1024;
const SPECIFIC_CHANNEL_VERSION: &str = "701e3ae1dc3f7975556d354e0675168d004891c8";

/// Relay-token facts needed after current-Go authentication has completed.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct KlingTaskAccess {
    user_id: i64,
    model_limits: Option<Vec<String>>,
    specific_channel_id: Option<i64>,
}

impl KlingTaskAccess {
    /// Creates access for a token without model or channel restrictions.
    #[must_use]
    pub const fn new(user_id: i64) -> Self {
        Self {
            user_id,
            model_limits: None,
            specific_channel_id: None,
        }
    }

    /// Enables the same comma-split model allow-list stored on a Go token.
    #[must_use]
    pub fn with_model_limits(mut self, limits: Vec<String>) -> Self {
        self.model_limits = Some(limits);
        self
    }

    /// Pins distribution to an administrator-selected channel.
    #[must_use]
    pub const fn with_specific_channel(mut self, channel_id: i64) -> Self {
        self.specific_channel_id = Some(channel_id);
        self
    }
}

/// Sendable request facts established before the async service boundary.
#[derive(Clone, Debug)]
pub struct KlingTaskReadRequest {
    headers: HeaderMap,
    request_id: String,
    client_ip: Option<IpAddr>,
}

impl KlingTaskReadRequest {
    fn from_http(request: &Request) -> Self {
        let boundary = request.extensions().get::<RequestContext>();
        Self {
            headers: request.headers().clone(),
            request_id: boundary.map_or_else(
                || {
                    request
                        .headers()
                        .get("x-oneapi-request-id")
                        .and_then(|value| value.to_str().ok())
                        .unwrap_or_default()
                        .to_owned()
                },
                |context| context.request_id.clone(),
            ),
            client_ip: boundary.and_then(|context| context.client_ip),
        }
    }

    /// Returns the presented Authorization header for deterministic adapters.
    #[must_use]
    pub fn authorization(&self) -> Option<&str> {
        self.headers
            .get(header::AUTHORIZATION)
            .and_then(|value| value.to_str().ok())
    }
}

/// Public, non-secret properties emitted by Go's task DTO.
#[derive(Clone, Debug, Default, Eq, PartialEq, Deserialize, Serialize)]
pub struct KlingTaskProperties {
    /// Original provider input retained with the task.
    #[serde(default)]
    pub input: String,
    /// Model name selected for the provider, when recorded.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub upstream_model_name: String,
    /// Client-facing model name used by token model-limit distribution.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub origin_model_name: String,
}

/// Exact provider-neutral task payload returned by the legacy fetch builder.
#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct KlingTask {
    pub id: i64,
    pub created_at: i64,
    pub updated_at: i64,
    pub task_id: String,
    pub platform: String,
    pub user_id: i64,
    pub group: String,
    pub channel_id: i64,
    pub quota: i64,
    pub action: String,
    pub status: String,
    pub fail_reason: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub result_url: Option<String>,
    pub submit_time: i64,
    pub start_time: i64,
    pub finish_time: i64,
    pub progress: String,
    pub properties: KlingTaskProperties,
    pub data: Value,
}

/// Stable failures at the read-only compatibility boundary.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum KlingTaskReadFailure {
    /// Invalid or insufficiently trusted relay credentials are deliberately hidden.
    ConcealedNotFound,
    /// A current OpenAI-shaped middleware rejection.
    OpenAi {
        status: StatusCode,
        message: String,
        code: String,
        specific_channel_version: bool,
    },
    /// A task-fetch error returned by `RelayTaskFetch` after middleware succeeds.
    Task {
        status: StatusCode,
        code: String,
        message: String,
    },
}

impl KlingTaskReadFailure {
    fn openai(status: StatusCode, message: impl Into<String>, code: impl Into<String>) -> Self {
        Self::OpenAi {
            status,
            message: message.into(),
            code: code.into(),
            specific_channel_version: false,
        }
    }

    fn specific_channel_denied(message: String) -> Self {
        Self::OpenAi {
            status: StatusCode::FORBIDDEN,
            message,
            code: String::new(),
            specific_channel_version: true,
        }
    }

    fn task(status: StatusCode, code: &str, message: impl Into<String>) -> Self {
        Self::Task {
            status,
            code: code.to_owned(),
            message: message.into(),
        }
    }
}

/// Storage/authentication port used by the HTTP ordering layer.
#[async_trait]
pub trait KlingTaskReadService: Send + Sync {
    /// Runs current-Go relay `TokenAuth`, including the developer/trust gate.
    async fn authenticate(
        &self,
        request: &KlingTaskReadRequest,
    ) -> Result<KlingTaskAccess, KlingTaskReadFailure>;

    /// Validates a token-selected channel before model-limit distribution.
    async fn validate_specific_channel(
        &self,
        channel_id: i64,
        request: &KlingTaskReadRequest,
    ) -> Result<(), KlingTaskReadFailure>;

    /// Performs the distributor's first, best-effort owned task lookup.
    ///
    /// Go intentionally suppresses storage errors here and treats them as an
    /// empty origin model.  The handler subsequently performs its authoritative
    /// lookup, unless model access has already rejected the request.
    async fn origin_model_for_owned_task(&self, user_id: i64, task_id: &str) -> Option<String>;

    /// Loads the authoritative owned row used to build the response.
    async fn owned_task(
        &self,
        user_id: i64,
        task_id: &str,
    ) -> Result<Option<KlingTask>, KlingTaskReadFailure>;
}

/// Independently mountable state for the two Kling task reads.
#[derive(Clone)]
pub struct KlingTaskReadState {
    service: Arc<dyn KlingTaskReadService>,
}

impl KlingTaskReadState {
    /// Creates state from an application-owned, fail-closed service.
    #[must_use]
    pub fn new(service: Arc<dyn KlingTaskReadService>) -> Self {
        Self { service }
    }
}

/// PostgreSQL-backed service using the normal listener's model/token authority.
#[derive(Clone)]
pub struct PgKlingTaskReadService {
    pg: PgPool,
    models: Arc<PgModelsService>,
}

impl PgKlingTaskReadService {
    /// Builds the read service. `models` should be the same configured instance
    /// used by the normal relay listener so Valkey remains only its cache.
    #[must_use]
    pub fn new(pg: PgPool, models: Arc<PgModelsService>) -> Self {
        Self { pg, models }
    }
}

/// Builds only the two owned GET routes; no Kling submission route is included.
pub fn router(state: KlingTaskReadState) -> Router {
    Router::new()
        .route("/kling/v1/videos/image2video/{task_id}", get(read_task))
        .route("/kling/v1/videos/text2video/{task_id}", get(read_task))
        .with_state(state)
}

async fn read_task(
    State(state): State<KlingTaskReadState>,
    Path(task_id): Path<String>,
    request: Request,
) -> Response {
    // `KlingRequestConvert` reads first but lets conversion errors continue to
    // TokenAuth. Preserve that observable order while retaining no request
    // bytes after the local-only decision is known.
    let relay_request = KlingTaskReadRequest::from_http(&request);
    let (parts, body) = request.into_parts();
    let conversion = match to_bytes(body, MAX_REQUEST_BODY_BYTES).await {
        Ok(body) => kling_get_conversion(&parts.headers, &body),
        Err(_) => KlingGetConversion::InvalidJson,
    };
    let access = match state.service.authenticate(&relay_request).await {
        Ok(access) => access,
        Err(error) => return failure_response(error),
    };

    if conversion == KlingGetConversion::InvalidJson {
        return failure_response(invalid_json_failure(&relay_request));
    }

    if let Some(channel_id) = access.specific_channel_id
        && let Err(error) = state
            .service
            .validate_specific_channel(channel_id, &relay_request)
            .await
    {
        return failure_response(error);
    }

    if conversion == KlingGetConversion::UnconvertedJson {
        if let Some(limits) = access.model_limits.as_deref()
            && !model_is_allowed("", limits)
        {
            return failure_response(model_forbidden_failure("", &relay_request));
        }
        if access.specific_channel_id.is_none() {
            return failure_response(model_required_failure(&relay_request));
        }
        // This is the unreachable-safe form of Go's invalid relay-mode branch
        // after a token-pinned channel. It fails closed instead of invoking a
        // nil response builder.
        return failure_response(KlingTaskReadFailure::task(
            StatusCode::BAD_REQUEST,
            "invalid_relay_mode",
            "invalid_relay_mode",
        ));
    }

    if access.specific_channel_id.is_none()
        && let Some(limits) = access.model_limits.as_deref()
    {
        let origin_model = state
            .service
            .origin_model_for_owned_task(access.user_id, &task_id)
            .await
            .unwrap_or_default();
        if !model_is_allowed(&origin_model, limits) {
            return failure_response(model_forbidden_failure(&origin_model, &relay_request));
        }
    }

    match state.service.owned_task(access.user_id, &task_id).await {
        Ok(Some(task)) => success_response(&task),
        Ok(None) => failure_response(KlingTaskReadFailure::task(
            StatusCode::BAD_REQUEST,
            "task_not_exist",
            "task_not_exist",
        )),
        Err(error) => failure_response(error),
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum KlingGetConversion {
    Converted,
    UnconvertedJson,
    InvalidJson,
}

fn kling_get_conversion(headers: &HeaderMap, body: &[u8]) -> KlingGetConversion {
    let is_json = headers
        .get(header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.starts_with("application/json"));
    if !is_json {
        return KlingGetConversion::Converted;
    }
    match serde_json::from_slice::<Value>(body) {
        Ok(Value::Object(_) | Value::Null) => KlingGetConversion::Converted,
        Ok(_) => KlingGetConversion::UnconvertedJson,
        Err(_) => KlingGetConversion::InvalidJson,
    }
}

#[async_trait]
impl KlingTaskReadService for PgKlingTaskReadService {
    async fn authenticate(
        &self,
        request: &KlingTaskReadRequest,
    ) -> Result<KlingTaskAccess, KlingTaskReadFailure> {
        let request_id = request.request_id.clone();
        let credential =
            relay_credential(&request.headers).ok_or(KlingTaskReadFailure::ConcealedNotFound)?;
        let locale = request_locale(&request.headers);
        let client_ip = request
            .client_ip
            // The listener always supplies RequestContext. An isolated mount
            // must not turn a missing canonical IP into an allow-list bypass.
            .unwrap_or(IpAddr::V4(Ipv4Addr::UNSPECIFIED));
        self.models
            .authenticate_only_with_policy(
                ModelsRequest {
                    // Supplying only the already normalized key prevents the
                    // shared model authority from applying its suffix check
                    // before Go's token-group check.
                    authorization: Some(format!("Bearer {}", credential.key)),
                    api_key: None,
                    gemini_key: None,
                    mj_api_secret: None,
                    client_ip,
                },
                true,
            )
            .await
            .map_err(|error| models_failure(error, locale, &request_id))?;

        let row = sqlx::query(
            r#"SELECT t.user_id,
                      COALESCE(t.model_limits_enabled,FALSE) AS model_limits_enabled,
                      COALESCE(t.model_limits,'') AS model_limits,
                      COALESCE(t."group",'') AS token_group,
                      COALESCE(u."group",'default') AS user_group,
                      COALESCE(u.role,1) AS role
                 FROM tokens t
                 JOIN users u ON u.id=t.user_id
                WHERE t.key=$1 AND t.deleted_at IS NULL AND u.deleted_at IS NULL"#,
        )
        .bind(&credential.key)
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| auth_storage_failure(locale, &request_id))?
        .ok_or(KlingTaskReadFailure::ConcealedNotFound)?;
        let user_id = row
            .try_get::<i64, _>("user_id")
            .map_err(|_| auth_storage_failure(locale, &request_id))?;
        let token_group = row
            .try_get::<String, _>("token_group")
            .map_err(|_| auth_storage_failure(locale, &request_id))?;
        let user_group = row
            .try_get::<String, _>("user_group")
            .map_err(|_| auth_storage_failure(locale, &request_id))?;
        validate_group(&self.pg, &user_group, &token_group, locale, &request_id).await?;

        let role = row
            .try_get::<i64, _>("role")
            .map_err(|_| auth_storage_failure(locale, &request_id))?;
        let specific_channel_id = match credential.channel_suffix.as_deref() {
            Some(_) if role < 10 => {
                return Err(KlingTaskReadFailure::specific_channel_denied(
                    with_request_id("普通用户不支持指定渠道", &request_id),
                ));
            }
            Some("") | None => None,
            Some(raw) => Some(raw.parse::<i64>().map_err(|_| {
                distributor_failure(
                    StatusCode::BAD_REQUEST,
                    localized(locale, Message::InvalidChannel),
                    "",
                    &request_id,
                )
            })?),
        };
        let model_limits_enabled = row
            .try_get::<bool, _>("model_limits_enabled")
            .map_err(|_| auth_storage_failure(locale, &request_id))?;
        let model_limits = if model_limits_enabled {
            let raw = row
                .try_get::<String, _>("model_limits")
                .map_err(|_| auth_storage_failure(locale, &request_id))?;
            Some(if raw.is_empty() {
                Vec::new()
            } else {
                raw.split(',').map(str::to_owned).collect()
            })
        } else {
            None
        };
        Ok(KlingTaskAccess {
            user_id,
            model_limits,
            specific_channel_id,
        })
    }

    async fn validate_specific_channel(
        &self,
        channel_id: i64,
        request: &KlingTaskReadRequest,
    ) -> Result<(), KlingTaskReadFailure> {
        let locale = request_locale(&request.headers);
        let request_id = request.request_id.clone();
        let row = sqlx::query("SELECT COALESCE(status,1) AS status FROM channels WHERE id=$1")
            .bind(channel_id)
            .fetch_optional(&self.pg)
            .await
            .map_err(|_| {
                distributor_failure(
                    StatusCode::BAD_REQUEST,
                    localized(locale, Message::InvalidChannel),
                    "",
                    &request_id,
                )
            })?
            .ok_or_else(|| {
                distributor_failure(
                    StatusCode::BAD_REQUEST,
                    localized(locale, Message::InvalidChannel),
                    "",
                    &request_id,
                )
            })?;
        let status = row.try_get::<i64, _>("status").map_err(|_| {
            distributor_failure(
                StatusCode::BAD_REQUEST,
                localized(locale, Message::InvalidChannel),
                "",
                &request_id,
            )
        })?;
        if status != 1 {
            return Err(distributor_failure(
                StatusCode::FORBIDDEN,
                localized(locale, Message::ChannelDisabled),
                "",
                &request_id,
            ));
        }
        Ok(())
    }

    async fn origin_model_for_owned_task(&self, user_id: i64, task_id: &str) -> Option<String> {
        let properties = sqlx::query_scalar::<_, Option<Value>>(
            "SELECT properties FROM tasks WHERE user_id=$1 AND task_id=$2 ORDER BY id LIMIT 1",
        )
        .bind(user_id)
        .bind(task_id)
        .fetch_optional(&self.pg)
        .await
        .ok()
        .flatten()
        .flatten()?;
        serde_json::from_value::<KlingTaskProperties>(properties)
            .ok()
            .map(|properties| properties.origin_model_name)
    }

    async fn owned_task(
        &self,
        user_id: i64,
        task_id: &str,
    ) -> Result<Option<KlingTask>, KlingTaskReadFailure> {
        let row = sqlx::query(
            r#"SELECT id, COALESCE(created_at,0) AS created_at,
                      COALESCE(updated_at,0) AS updated_at,
                      COALESCE(task_id,'') AS task_id,
                      COALESCE(platform,'') AS platform,
                      COALESCE(user_id,0) AS user_id,
                      COALESCE("group",'') AS "group",
                      COALESCE(channel_id,0) AS channel_id,
                      COALESCE(quota,0) AS quota,
                      COALESCE(action,'') AS action,
                      COALESCE(status,'') AS status,
                      COALESCE(fail_reason,'') AS fail_reason,
                      COALESCE(submit_time,0) AS submit_time,
                      COALESCE(start_time,0) AS start_time,
                      COALESCE(finish_time,0) AS finish_time,
                      COALESCE(progress,'') AS progress,
                      properties, private_data, data
                 FROM tasks
                WHERE user_id=$1 AND task_id=$2
                ORDER BY id LIMIT 1"#,
        )
        .bind(user_id)
        .bind(task_id)
        .fetch_optional(&self.pg)
        .await
        .map_err(task_storage_failure)?;
        row.map(task_from_row).transpose()
    }
}

fn task_from_row(row: PgRow) -> Result<KlingTask, KlingTaskReadFailure> {
    let fail_reason = row
        .try_get::<String, _>("fail_reason")
        .map_err(task_storage_failure)?;
    let private_data = row
        .try_get::<Option<Value>, _>("private_data")
        .map_err(task_storage_failure)?
        .unwrap_or(Value::Null);
    let private_result_url = match &private_data {
        Value::Null | Value::Object(_) => private_data
            .get("result_url")
            .and_then(Value::as_str)
            .unwrap_or_default(),
        _ => {
            return Err(KlingTaskReadFailure::task(
                StatusCode::INTERNAL_SERVER_ERROR,
                "get_task_failed",
                "invalid task private_data",
            ));
        }
    };
    let result_url = if private_result_url.is_empty() {
        (!fail_reason.is_empty()).then(|| fail_reason.clone())
    } else {
        Some(private_result_url.to_owned())
    };
    let properties = row
        .try_get::<Option<Value>, _>("properties")
        .map_err(task_storage_failure)?
        .map_or_else(
            || Ok(KlingTaskProperties::default()),
            |value| {
                serde_json::from_value(value).map_err(|error| {
                    KlingTaskReadFailure::task(
                        StatusCode::INTERNAL_SERVER_ERROR,
                        "get_task_failed",
                        error.to_string(),
                    )
                })
            },
        )?;
    Ok(KlingTask {
        id: task_column(&row, "id")?,
        created_at: task_column(&row, "created_at")?,
        updated_at: task_column(&row, "updated_at")?,
        task_id: task_column(&row, "task_id")?,
        platform: task_column(&row, "platform")?,
        user_id: task_column(&row, "user_id")?,
        group: task_column(&row, "group")?,
        channel_id: task_column(&row, "channel_id")?,
        quota: task_column(&row, "quota")?,
        action: task_column(&row, "action")?,
        status: task_column(&row, "status")?,
        fail_reason,
        result_url,
        submit_time: task_column(&row, "submit_time")?,
        start_time: task_column(&row, "start_time")?,
        finish_time: task_column(&row, "finish_time")?,
        progress: task_column(&row, "progress")?,
        properties,
        data: row
            .try_get::<Option<Value>, _>("data")
            .map_err(task_storage_failure)?
            .unwrap_or(Value::Null),
    })
}

fn task_column<T>(row: &PgRow, column: &str) -> Result<T, KlingTaskReadFailure>
where
    T: for<'row> sqlx::Decode<'row, sqlx::Postgres> + sqlx::Type<sqlx::Postgres>,
{
    row.try_get(column).map_err(task_storage_failure)
}

fn task_storage_failure(error: sqlx::Error) -> KlingTaskReadFailure {
    KlingTaskReadFailure::task(
        StatusCode::INTERNAL_SERVER_ERROR,
        "get_task_failed",
        error.to_string(),
    )
}

#[derive(Debug)]
struct RelayCredential {
    key: String,
    channel_suffix: Option<String>,
}

fn relay_credential(headers: &HeaderMap) -> Option<RelayCredential> {
    let websocket = headers
        .get("sec-websocket-protocol")
        .and_then(|value| value.to_str().ok())
        .and_then(websocket_api_key);
    let authorization = websocket.map_or_else(
        || {
            headers
                .get(header::AUTHORIZATION)
                .and_then(|value| value.to_str().ok())
                .unwrap_or_default()
                .to_owned()
        },
        |key| format!("Bearer {key}"),
    );
    let mut raw = strip_bearer(&authorization);
    let fallback;
    if raw.is_empty() || raw == "midjourney-proxy" {
        fallback = headers
            .get("mj-api-secret")
            .and_then(|value| value.to_str().ok())
            .unwrap_or_default()
            .to_owned();
        raw = strip_bearer(&fallback);
    }
    let raw = raw.strip_prefix("sk-").unwrap_or(raw);
    let mut parts = raw.split('-');
    let key = parts.next().unwrap_or_default();
    (!key.is_empty()).then(|| RelayCredential {
        key: key.to_owned(),
        channel_suffix: parts.next().map(str::to_owned),
    })
}

fn strip_bearer(raw: &str) -> &str {
    raw.strip_prefix("Bearer ")
        .or_else(|| raw.strip_prefix("bearer "))
        .map_or(raw, str::trim)
}

fn websocket_api_key(protocols: &str) -> Option<String> {
    protocols.split(',').find_map(|part| {
        part.trim()
            .strip_prefix("openai-insecure-api-key.")
            .filter(|key| !key.is_empty())
            .map(str::to_owned)
    })
}

async fn validate_group(
    pg: &PgPool,
    user_group: &str,
    token_group: &str,
    locale: Locale,
    request_id: &str,
) -> Result<(), KlingTaskReadFailure> {
    if token_group.is_empty() {
        return Ok(());
    }
    let keys = vec!["UserUsableGroups".to_owned(), "GroupRatio".to_owned()];
    let rows = sqlx::query("SELECT key,value FROM options WHERE key=ANY($1)")
        .bind(keys)
        .fetch_all(pg)
        .await
        .map_err(|_| auth_storage_failure(locale, request_id))?;
    let options = rows
        .into_iter()
        .map(|row| {
            Ok((
                row.try_get::<String, _>("key")?,
                row.try_get::<Option<String>, _>("value")?
                    .unwrap_or_default(),
            ))
        })
        .collect::<Result<HashMap<_, _>, sqlx::Error>>()
        .map_err(|_| auth_storage_failure(locale, request_id))?;
    let usable = json_object_keys(options.get("UserUsableGroups"));
    if token_group != user_group && !usable.contains(token_group) {
        return Err(KlingTaskReadFailure::openai(
            StatusCode::FORBIDDEN,
            with_request_id(&format!("无权访问 {token_group} 分组"), request_id),
            "",
        ));
    }
    let ratios = json_object_keys(options.get("GroupRatio"));
    if token_group != "default" && token_group != "auto" && !ratios.contains(token_group) {
        return Err(KlingTaskReadFailure::openai(
            StatusCode::FORBIDDEN,
            with_request_id(&format!("分组 {token_group} 已被弃用"), request_id),
            "",
        ));
    }
    Ok(())
}

fn json_object_keys(raw: Option<&String>) -> HashSet<String> {
    raw.and_then(|value| serde_json::from_str::<Value>(value).ok())
        .and_then(|value| value.as_object().cloned())
        .map(|object| object.into_iter().map(|(key, _)| key).collect())
        .unwrap_or_default()
}

fn model_is_allowed(model: &str, limits: &[String]) -> bool {
    let matching = matching_model_name(model);
    limits.iter().any(|allowed| allowed == matching)
}

fn matching_model_name(model: &str) -> &str {
    if model.starts_with("gemini-2.5-flash-lite") && model.contains("-thinking-") {
        "gemini-2.5-flash-lite-thinking-*"
    } else if model.starts_with("gemini-2.5-flash") && model.contains("-thinking-") {
        "gemini-2.5-flash-thinking-*"
    } else if model.starts_with("gemini-2.5-pro") && model.contains("-thinking-") {
        "gemini-2.5-pro-thinking-*"
    } else if model.starts_with("gpt-4-gizmo") {
        "gpt-4-gizmo-*"
    } else if model.starts_with("gpt-4o-gizmo") {
        "gpt-4o-gizmo-*"
    } else {
        model
    }
}

fn models_failure(error: ModelsError, locale: Locale, request_id: &str) -> KlingTaskReadFailure {
    match error.kind {
        ModelsErrorKind::MissingToken
        | ModelsErrorKind::InvalidToken
        | ModelsErrorKind::DiscoveryHidden => KlingTaskReadFailure::ConcealedNotFound,
        ModelsErrorKind::AccessDenied => KlingTaskReadFailure::openai(
            StatusCode::FORBIDDEN,
            with_request_id(error.message.as_ref(), request_id),
            if error.message == "您的 IP 不在令牌允许访问的列表中" {
                "access_denied"
            } else {
                ""
            },
        ),
        ModelsErrorKind::UserBanned => KlingTaskReadFailure::openai(
            StatusCode::FORBIDDEN,
            with_request_id(localized(locale, Message::UserBanned), request_id),
            "",
        ),
        ModelsErrorKind::Database => auth_storage_failure(locale, request_id),
    }
}

fn auth_storage_failure(locale: Locale, request_id: &str) -> KlingTaskReadFailure {
    KlingTaskReadFailure::openai(
        StatusCode::INTERNAL_SERVER_ERROR,
        with_request_id(localized(locale, Message::Database), request_id),
        "",
    )
}

fn distributor_failure(
    status: StatusCode,
    message: &str,
    code: &str,
    request_id: &str,
) -> KlingTaskReadFailure {
    KlingTaskReadFailure::openai(status, with_request_id(message, request_id), code)
}

fn invalid_json_failure(request: &KlingTaskReadRequest) -> KlingTaskReadFailure {
    let locale = request_locale(&request.headers);
    let inner = localized(locale, Message::InvalidJson);
    let once = localized_invalid_request(locale, inner);
    distributor_failure(
        StatusCode::BAD_REQUEST,
        &localized_invalid_request(locale, &once),
        "",
        &request.request_id,
    )
}

fn model_required_failure(request: &KlingTaskReadRequest) -> KlingTaskReadFailure {
    let locale = request_locale(&request.headers);
    distributor_failure(
        StatusCode::BAD_REQUEST,
        localized(locale, Message::ModelRequired),
        "",
        &request.request_id,
    )
}

fn model_forbidden_failure(model: &str, request: &KlingTaskReadRequest) -> KlingTaskReadFailure {
    let locale = request_locale(&request.headers);
    distributor_failure(
        StatusCode::FORBIDDEN,
        &localized_model_forbidden(locale, model),
        "",
        &request.request_id,
    )
}

fn with_request_id(message: &str, request_id: &str) -> String {
    format!("{message} (request id: {request_id})")
}

#[derive(Clone, Copy)]
enum Locale {
    En,
    ZhCn,
    ZhTw,
}

fn request_locale(headers: &HeaderMap) -> Locale {
    let language = headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default()
        .to_ascii_lowercase();
    if language.contains("zh-tw") || language.contains("zh-hk") {
        Locale::ZhTw
    } else if language.contains("zh") {
        Locale::ZhCn
    } else {
        Locale::En
    }
}

#[derive(Clone, Copy)]
enum Message {
    Database,
    UserBanned,
    InvalidChannel,
    ChannelDisabled,
    InvalidJson,
    ModelRequired,
}

fn localized(locale: Locale, message: Message) -> &'static str {
    match (locale, message) {
        (Locale::En, Message::Database) => "Database error, please contact the administrator",
        (Locale::ZhCn, Message::Database) => "数据库出错，请联系管理员",
        (Locale::ZhTw, Message::Database) => "資料庫出錯，請聯繫管理員",
        (Locale::En, Message::UserBanned) => "User has been banned",
        (Locale::ZhCn, Message::UserBanned) => "用户已被封禁",
        (Locale::ZhTw, Message::UserBanned) => "使用者已被封禁",
        (Locale::En, Message::InvalidChannel) => "Invalid channel ID",
        (Locale::ZhCn, Message::InvalidChannel) => "无效的渠道 Id",
        (Locale::ZhTw, Message::InvalidChannel) => "無效的管道 Id",
        (Locale::En, Message::ChannelDisabled) => "This channel has been disabled",
        (Locale::ZhCn, Message::ChannelDisabled) => "该渠道已被禁用",
        (Locale::ZhTw, Message::ChannelDisabled) => "該管道已被禁用",
        (Locale::En, Message::InvalidJson)
        | (Locale::ZhCn, Message::InvalidJson)
        | (Locale::ZhTw, Message::InvalidJson) => "invalid JSON request body",
        (Locale::En, Message::ModelRequired) => {
            "Model name not specified, model name cannot be empty"
        }
        (Locale::ZhCn, Message::ModelRequired) => "未指定模型名称，模型名称不能为空",
        (Locale::ZhTw, Message::ModelRequired) => "未指定模型名稱，模型名稱不能為空",
    }
}

fn localized_invalid_request(locale: Locale, error: &str) -> String {
    match locale {
        Locale::En => format!("Invalid request: {error}"),
        Locale::ZhCn => format!("无效的请求，{error}"),
        Locale::ZhTw => format!("無效的請求，{error}"),
    }
}

fn localized_model_forbidden(locale: Locale, model: &str) -> String {
    match locale {
        Locale::En => format!("This token has no access to model {model}"),
        Locale::ZhCn => format!("该令牌无权访问模型 {model}"),
        Locale::ZhTw => format!("該令牌無權存取模型 {model}"),
    }
}

#[derive(Serialize)]
struct TaskResponse<'task> {
    code: &'static str,
    message: &'static str,
    data: &'task KlingTask,
}

#[derive(Serialize)]
struct TaskErrorResponse<'message> {
    code: &'message str,
    message: &'message str,
    data: Value,
}

#[derive(Serialize)]
struct OpenAiErrorEnvelope<'error> {
    error: OpenAiError<'error>,
}

#[derive(Serialize)]
struct OpenAiError<'error> {
    code: &'error str,
    message: &'error str,
    #[serde(rename = "type")]
    kind: &'static str,
}

fn success_response(task: &KlingTask) -> Response {
    json_response(
        StatusCode::OK,
        Json(TaskResponse {
            code: "success",
            message: "",
            data: task,
        }),
    )
}

fn failure_response(error: KlingTaskReadFailure) -> Response {
    match error {
        KlingTaskReadFailure::ConcealedNotFound => {
            json_response(StatusCode::NOT_FOUND, Json(json!({"message":"Not Found"})))
        }
        KlingTaskReadFailure::OpenAi {
            status,
            message,
            code,
            specific_channel_version,
        } => {
            let mut response = json_response(
                status,
                Json(OpenAiErrorEnvelope {
                    error: OpenAiError {
                        code: &code,
                        message: &message,
                        kind: "new_api_error",
                    },
                }),
            );
            if specific_channel_version {
                response.headers_mut().insert(
                    "specific_channel_version",
                    HeaderValue::from_static(SPECIFIC_CHANNEL_VERSION),
                );
            }
            response
        }
        KlingTaskReadFailure::Task {
            status,
            code,
            message,
        } => json_response(
            status,
            Json(TaskErrorResponse {
                code: &code,
                message: &message,
                data: Value::Null,
            }),
        ),
    }
}

fn json_response<T>(status: StatusCode, body: Json<T>) -> Response
where
    T: Serialize,
{
    let mut response = (status, body).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}
