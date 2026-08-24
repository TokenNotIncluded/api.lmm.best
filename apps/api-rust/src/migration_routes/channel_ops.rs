//! PostgreSQL-backed legacy channel tag operations.
//!
//! The slice authenticates every route through the injected dashboard policy and
//! commits PostgreSQL changes before invalidating the shared Valkey generation.

use std::sync::Arc;

use async_trait::async_trait;
use axum::{
    Json, Router,
    extract::{Query, Request, State, rejection::JsonRejection},
    http::{HeaderMap, StatusCode},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{get, post, put},
};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::json;
use sqlx::{PgPool, Postgres, Row, Transaction};

use super::channel_core::{ChannelAction, ChannelAdminAuthorizer, ChannelError};
use crate::auth::DashboardAuth;

const CHANNEL_CACHE_GENERATION: &str = "lmm:channels:generation";
const ADMIN_ROLE: i64 = 10;
const ROOT_ROLE: i64 = 100;
const STATUS_ENABLED: i64 = 1;

/// Production channel-policy adapter backed by the signed dashboard session.
///
/// Permission decisions only use the server-side user record returned by
/// `DashboardAuth`; role-like headers and request fields are never trusted.
#[derive(Clone)]
pub struct DashboardChannelAuthorizer {
    auth: Arc<dyn DashboardAuth>,
}

impl DashboardChannelAuthorizer {
    #[must_use]
    pub fn new(auth: Arc<dyn DashboardAuth>) -> Self {
        Self { auth }
    }
}

#[async_trait]
impl ChannelAdminAuthorizer for DashboardChannelAuthorizer {
    async fn authorize(
        &self,
        headers: &HeaderMap,
        action: ChannelAction,
    ) -> Result<(), ChannelError> {
        let token = dashboard_credential(headers).ok_or(ChannelError::ConsoleNotFound)?;
        let user = self
            .auth
            .self_user_view_for_optional(SecretString::from(token.to_owned()))
            .await
            .map_err(|_| ChannelError::ConsoleNotFound)?;
        if user.id <= 0 || user.status != STATUS_ENABLED || !user.developer_access_granted {
            return Err(ChannelError::ConsoleNotFound);
        }
        if user.role < ADMIN_ROLE {
            return Err(ChannelError::Forbidden);
        }
        if user.role == ROOT_ROLE || channel_permission(&user.permissions, action) {
            return Ok(());
        }
        Err(ChannelError::Forbidden)
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

fn channel_permission(permissions: &serde_json::Value, action: ChannelAction) -> bool {
    let action = match action {
        ChannelAction::Read => "read",
        ChannelAction::Write => "write",
        ChannelAction::SensitiveWrite => "sensitive_write",
        ChannelAction::Operate => "operate",
    };
    permissions
        .get("admin_permissions")
        .and_then(|permissions| permissions.get("channel"))
        .and_then(|permissions| permissions.get(action))
        .and_then(serde_json::Value::as_bool)
        .unwrap_or(false)
}

#[derive(Clone)]
pub struct ChannelOpsHttpState {
    pg: PgPool,
    valkey: redis::Client,
    authorizer: Arc<dyn ChannelAdminAuthorizer>,
}

impl ChannelOpsHttpState {
    #[must_use]
    pub fn new(
        pg: PgPool,
        valkey: redis::Client,
        authorizer: Arc<dyn ChannelAdminAuthorizer>,
    ) -> Self {
        Self {
            pg,
            valkey,
            authorizer,
        }
    }
}

/// Routes owned by this migration slice.  The parent router must apply the
/// legacy admin authentication and per-route permissions before merging it.
pub fn channel_ops_router(state: ChannelOpsHttpState) -> Router {
    Router::new()
        .route("/api/channel/tag/disabled", post(disable_tag_channels))
        .route("/api/channel/tag/enabled", post(enable_tag_channels))
        .route("/api/channel/tag", put(edit_tag_channels))
        .route("/api/channel/batch/tag", post(batch_set_channel_tag))
        .route("/api/channel/tag/models", get(get_tag_models))
        // Go's AdminAuth/RequirePermission middleware runs before Gin binds
        // JSON. Keep the same listener-owned preflight here so anonymous or
        // malformed requests receive the legacy auth envelope instead of an
        // Axum 422 response from a JSON extractor.
        .layer(middleware::from_fn_with_state(
            state.clone(),
            channel_ops_auth_boundary,
        ))
        .with_state(state)
}

fn channel_ops_action_for_request(request: &Request) -> ChannelAction {
    let path = request.uri().path();
    if request.method() == axum::http::Method::GET {
        return ChannelAction::Read;
    }
    if path == "/api/channel/tag" && request.method() == axum::http::Method::PUT {
        return ChannelAction::Write;
    }
    if path == "/api/channel/batch/tag" {
        return ChannelAction::Write;
    }
    if path == "/api/channel/tag/disabled" || path == "/api/channel/tag/enabled" {
        return ChannelAction::Operate;
    }
    ChannelAction::SensitiveWrite
}

async fn channel_ops_auth_boundary(
    axum::extract::State(state): axum::extract::State<ChannelOpsHttpState>,
    request: Request,
    next: Next,
) -> Response {
    let action = channel_ops_action_for_request(&request);
    if let Err(error) = state.authorizer.authorize(request.headers(), action).await {
        return error.legacy();
    }
    next.run(request).await
}

#[derive(Debug, Deserialize)]
struct ChannelTagRequest {
    tag: String,
    new_tag: Option<String>,
    priority: Option<i64>,
    weight: Option<i64>,
    model_mapping: Option<String>,
    models: Option<String>,
    groups: Option<String>,
    param_override: Option<String>,
    header_override: Option<String>,
}

#[derive(Debug, Deserialize)]
struct ChannelBatchRequest {
    ids: Vec<i64>,
    tag: Option<String>,
}

#[derive(Debug, Deserialize)]
struct TagModelsQuery {
    tag: Option<String>,
}

#[derive(Serialize)]
struct LegacyEnvelope<T: Serialize> {
    success: bool,
    message: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<T>,
}

fn success() -> Json<LegacyEnvelope<()>> {
    Json(LegacyEnvelope {
        success: true,
        message: "",
        data: None,
    })
}

fn failure(message: &'static str) -> Json<LegacyEnvelope<()>> {
    Json(LegacyEnvelope {
        success: false,
        message,
        data: None,
    })
}

async fn disable_tag_channels(
    State(state): State<ChannelOpsHttpState>,
    headers: HeaderMap,
    request: Result<Json<ChannelTagRequest>, JsonRejection>,
) -> Response {
    if let Err(response) = permit(&state, &headers, ChannelAction::Operate).await {
        return response;
    }
    let Json(request) = match request {
        Ok(request) => request,
        Err(_) => return failure("参数错误").into_response(),
    };
    if request.tag.is_empty() {
        return failure("参数错误").into_response();
    }
    match set_tag_status(&state.pg, &request.tag, 2, false).await {
        Ok(()) => match invalidate(&state).await {
            Ok(()) => success().into_response(),
            Err(response) => response,
        },
        Err(_) => database_failure(),
    }
}

async fn enable_tag_channels(
    State(state): State<ChannelOpsHttpState>,
    headers: HeaderMap,
    request: Result<Json<ChannelTagRequest>, JsonRejection>,
) -> Response {
    if let Err(response) = permit(&state, &headers, ChannelAction::Operate).await {
        return response;
    }
    let Json(request) = match request {
        Ok(request) => request,
        Err(_) => return failure("参数错误").into_response(),
    };
    if request.tag.is_empty() {
        return failure("参数错误").into_response();
    }
    match set_tag_status(&state.pg, &request.tag, 1, true).await {
        Ok(()) => match invalidate(&state).await {
            Ok(()) => success().into_response(),
            Err(response) => response,
        },
        Err(_) => database_failure(),
    }
}

async fn set_tag_status(
    pool: &PgPool,
    tag: &str,
    status: i64,
    enabled: bool,
) -> Result<(), sqlx::Error> {
    let mut tx = pool.begin().await?;
    lock_tag(&mut tx, tag).await?;
    let ids: Vec<i64> =
        sqlx::query_scalar("SELECT id FROM channels WHERE tag = $1 ORDER BY id FOR UPDATE")
            .bind(tag)
            .fetch_all(&mut *tx)
            .await?;
    sqlx::query("UPDATE channels SET status = $1 WHERE tag = $2")
        .bind(status)
        .bind(tag)
        .execute(&mut *tx)
        .await?;
    sqlx::query("UPDATE abilities SET enabled = $1 WHERE channel_id = ANY($2)")
        .bind(enabled)
        .bind(&ids)
        .execute(&mut *tx)
        .await?;
    tx.commit().await
}

async fn edit_tag_channels(
    State(state): State<ChannelOpsHttpState>,
    headers: HeaderMap,
    request: Result<Json<ChannelTagRequest>, JsonRejection>,
) -> Response {
    if let Err(response) = permit(&state, &headers, ChannelAction::Write).await {
        return response;
    }
    let Json(mut request) = match request {
        Ok(request) => request,
        Err(_) => return failure("参数错误").into_response(),
    };
    if request.tag.is_empty() {
        return failure("tag不能为空").into_response();
    }
    // Legacy treats the presence of either override as a sensitive mutation,
    // even when its value is later rejected as invalid JSON.
    if (request.param_override.is_some() || request.header_override.is_some())
        && let Err(response) = permit(&state, &headers, ChannelAction::SensitiveWrite).await
    {
        return response;
    }
    let param_override = match validated_json_override(
        request.param_override.take(),
        "参数覆盖必须是合法的 JSON 格式",
    ) {
        Ok(value) => value,
        Err(message) => return failure(message).into_response(),
    };
    let header_override = match validated_json_override(
        request.header_override.take(),
        "请求头覆盖必须是合法的 JSON 格式",
    ) {
        Ok(value) => value,
        Err(message) => return failure(message).into_response(),
    };
    match edit_tag(&state.pg, request, param_override, header_override).await {
        Ok(()) => match invalidate(&state).await {
            Ok(()) => success().into_response(),
            Err(response) => response,
        },
        Err(_) => database_failure(),
    }
}

fn validated_json_override(
    value: Option<String>,
    message: &'static str,
) -> Result<Option<String>, &'static str> {
    let Some(value) = value else { return Ok(None) };
    let value = value.trim().to_owned();
    if !value.is_empty() && serde_json::from_str::<serde_json::Value>(&value).is_err() {
        return Err(message);
    }
    Ok(Some(value))
}

async fn edit_tag(
    pool: &PgPool,
    request: ChannelTagRequest,
    param_override: Option<String>,
    header_override: Option<String>,
) -> Result<(), sqlx::Error> {
    let mut tx = pool.begin().await?;
    let updated_tag = request.new_tag.as_deref().unwrap_or(&request.tag);
    let mut tags = vec![&request.tag[..], updated_tag];
    tags.sort_unstable();
    tags.dedup();
    for tag in tags {
        lock_tag(&mut tx, tag).await?;
    }
    sqlx::query(
        r#"UPDATE channels
              SET tag = COALESCE($1, tag), model_mapping = COALESCE($2, model_mapping),
                  models = CASE WHEN $3 <> '' THEN $3 ELSE models END,
                  "group" = CASE WHEN $4 <> '' THEN $4 ELSE "group" END,
                  priority = COALESCE($5, priority), weight = COALESCE($6, weight),
                  param_override = COALESCE($7, param_override),
                  header_override = COALESCE($8, header_override)
            WHERE tag = $9"#,
    )
    .bind(request.new_tag.as_deref())
    .bind(request.model_mapping.as_deref())
    .bind(request.models.as_deref().unwrap_or(""))
    .bind(request.groups.as_deref().unwrap_or(""))
    .bind(request.priority)
    .bind(request.weight)
    .bind(param_override.as_deref())
    .bind(header_override.as_deref())
    .bind(&request.tag)
    .execute(&mut *tx)
    .await?;

    let recreate = request
        .models
        .as_deref()
        .is_some_and(|value| !value.is_empty())
        || request
            .groups
            .as_deref()
            .is_some_and(|value| !value.is_empty());
    if recreate {
        recreate_tag_abilities(&mut tx, updated_tag).await?;
    } else {
        sqlx::query(
            "UPDATE abilities SET tag = COALESCE($1, tag), priority = COALESCE($2, priority), weight = COALESCE($3, weight) WHERE tag = $4",
        )
        .bind(request.new_tag.as_deref())
        .bind(request.priority)
        .bind(request.weight)
        .bind(&request.tag)
        .execute(&mut *tx)
        .await?;
    }
    tx.commit().await
}

async fn batch_set_channel_tag(
    State(state): State<ChannelOpsHttpState>,
    headers: HeaderMap,
    request: Result<Json<ChannelBatchRequest>, JsonRejection>,
) -> Response {
    if let Err(response) = permit(&state, &headers, ChannelAction::Write).await {
        return response;
    }
    let Json(request) = match request {
        Ok(request) => request,
        Err(_) => return failure("参数错误").into_response(),
    };
    if request.ids.is_empty() {
        return failure("参数错误").into_response();
    }
    match batch_set_tag(&state.pg, &request.ids, request.tag.as_deref()).await {
        Ok(()) => match invalidate(&state).await {
            Ok(()) => Json(LegacyEnvelope {
                success: true,
                message: "",
                data: Some(request.ids.len()),
            })
            .into_response(),
            Err(response) => response,
        },
        Err(_) => database_failure(),
    }
}

async fn batch_set_tag(pool: &PgPool, ids: &[i64], tag: Option<&str>) -> Result<(), sqlx::Error> {
    let mut tx = pool.begin().await?;
    sqlx::query("SELECT id FROM channels WHERE id = ANY($1) ORDER BY id FOR UPDATE")
        .bind(ids)
        .fetch_all(&mut *tx)
        .await?;
    sqlx::query("UPDATE channels SET tag = $1 WHERE id = ANY($2)")
        .bind(tag)
        .bind(ids)
        .execute(&mut *tx)
        .await?;
    recreate_abilities_for_ids(&mut tx, ids).await?;
    tx.commit().await
}

async fn get_tag_models(
    State(state): State<ChannelOpsHttpState>,
    headers: HeaderMap,
    Query(query): Query<TagModelsQuery>,
) -> Response {
    if let Err(response) = permit(&state, &headers, ChannelAction::Read).await {
        return response;
    }
    let Some(tag) = query.tag.filter(|tag| !tag.is_empty()) else {
        return (StatusCode::BAD_REQUEST, failure("tag不能为空")).into_response();
    };
    let result = sqlx::query(
        "SELECT COALESCE(models, '') AS models FROM channels WHERE tag = $1 ORDER BY priority DESC NULLS LAST, weight DESC NULLS LAST, id ASC",
    )
    .bind(tag)
    .fetch_all(&state.pg)
        .await;
    match result {
        Ok(rows) => {
            let mut longest = String::new();
            let mut max_length = 0;
            for row in rows {
                let models = row.try_get::<String, _>("models").unwrap_or_default();
                if !models.is_empty() {
                    let length = models.split(',').count();
                    if length > max_length {
                        max_length = length;
                        longest = models;
                    }
                }
            }
            Json(LegacyEnvelope {
                success: true,
                message: "",
                data: Some(longest),
            })
            .into_response()
        }
        Err(_) => database_failure(),
    }
}

async fn permit(
    state: &ChannelOpsHttpState,
    headers: &HeaderMap,
    action: ChannelAction,
) -> Result<(), Response> {
    state
        .authorizer
        .authorize(headers, action)
        .await
        .map_err(ChannelError::legacy)
}

async fn invalidate(state: &ChannelOpsHttpState) -> Result<(), Response> {
    let mut connection = state
        .valkey
        .get_multiplexed_async_connection()
        .await
        .map_err(|_| ChannelError::Cache.legacy())?;
    redis::cmd("INCR")
        .arg(CHANNEL_CACHE_GENERATION)
        .query_async::<i64>(&mut connection)
        .await
        .map_err(|_| ChannelError::Cache.legacy())?;
    Ok(())
}

async fn recreate_tag_abilities(
    tx: &mut Transaction<'_, Postgres>,
    tag: &str,
) -> Result<(), sqlx::Error> {
    let ids: Vec<i64> = sqlx::query("SELECT id FROM channels WHERE tag = $1")
        .bind(tag)
        .fetch_all(&mut **tx)
        .await?
        .into_iter()
        .filter_map(|row| row.try_get("id").ok())
        .collect();
    recreate_abilities_for_ids(tx, &ids).await
}

async fn recreate_abilities_for_ids(
    tx: &mut Transaction<'_, Postgres>,
    ids: &[i64],
) -> Result<(), sqlx::Error> {
    if ids.is_empty() {
        return Ok(());
    }
    sqlx::query("DELETE FROM abilities WHERE channel_id = ANY($1)")
        .bind(ids)
        .execute(&mut **tx)
        .await?;
    // `DISTINCT` mirrors the legacy per-channel duplicate suppression.
    sqlx::query(
        r#"INSERT INTO abilities ("group", model, channel_id, enabled, priority, weight, tag)
           SELECT DISTINCT trim(groups.value), trim(models.value), c.id, c.status = 1,
                  COALESCE(c.priority, 0), COALESCE(c.weight, 0), c.tag
             FROM channels c
             CROSS JOIN LATERAL unnest(string_to_array(COALESCE(c.models, ''), ',')) AS models(value)
             CROSS JOIN LATERAL unnest(string_to_array(COALESCE(c."group", ''), ',')) AS groups(value)
            WHERE c.id = ANY($1) AND trim(models.value) <> '' AND trim(groups.value) <> ''
           ON CONFLICT ("group", model, channel_id) DO NOTHING"#,
    )
    .bind(ids)
    .execute(&mut **tx)
    .await?;
    Ok(())
}

async fn lock_tag(tx: &mut Transaction<'_, Postgres>, tag: &str) -> Result<(), sqlx::Error> {
    sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended($1, 0))")
        .bind(tag)
        .execute(&mut **tx)
        .await?;
    Ok(())
}

fn database_failure() -> Response {
    // The root middleware turns storage errors into the deployment's legacy API
    // error envelope.  Keep this slice deterministic when mounted standalone.
    (
        StatusCode::INTERNAL_SERVER_ERROR,
        Json(json!({"success": false, "message": "database error"})),
    )
        .into_response()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn json_override_validation_matches_legacy_empty_and_invalid_rules() {
        assert_eq!(
            validated_json_override(Some("  ".into()), "x").unwrap(),
            Some(String::new())
        );
        assert!(validated_json_override(Some("{oops}".into()), "x").is_err());
        assert_eq!(
            validated_json_override(Some(" {\"x\":1} ".into()), "x").unwrap(),
            Some("{\"x\":1}".into())
        );
    }
}
