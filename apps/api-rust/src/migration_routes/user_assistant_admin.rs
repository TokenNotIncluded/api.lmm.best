//! Administrator assistant profile, memory, and developer-access archive routes.

use std::sync::Arc;

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{Path, RawQuery, State},
    http::{HeaderMap, HeaderName, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get as route_get, put as route_put},
};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{PgPool, Row, postgres::PgRow};

use crate::auth::{
    AuthErrorKind, DashboardAuth, DashboardUserView, UserAuthPolicyError, enforce_user_auth_view,
    user_auth_message, user_auth_status,
};

const ADMIN_ROLE: i64 = 10;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const MAX_BODY_BYTES: usize = 64 * 1024;
const MAX_MEMORIES: i64 = 64;

#[derive(Clone)]
pub struct UserAssistantAdminState {
    backend: Arc<dyn UserAssistantAdminBackend>,
    auth: Arc<dyn DashboardAuth>,
}

impl UserAssistantAdminState {
    #[must_use]
    pub fn new(pg: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self::with_backend(Arc::new(PgUserAssistantAdminBackend { pg }), auth)
    }

    #[must_use]
    pub fn with_backend(
        backend: Arc<dyn UserAssistantAdminBackend>,
        auth: Arc<dyn DashboardAuth>,
    ) -> Self {
        Self { backend, auth }
    }
}

pub fn router(state: UserAssistantAdminState) -> Router {
    Router::new()
        .route(
            "/api/user/{id}/assistant-profile",
            route_get(get_profile).put(update_profile),
        )
        .route(
            "/api/user/{id}/assistant-memories",
            route_get(list_memories).post(create_memory),
        )
        .route(
            "/api/user/{id}/assistant-memories/{memoryId}",
            route_put(update_memory).delete(delete_memory),
        )
        .route(
            "/api/user/{id}/developer-access/archives",
            route_get(list_archives),
        )
        .with_state(state)
}

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
#[error("{0}")]
pub struct UserAssistantAdminError(pub String);

#[async_trait]
pub trait UserAssistantAdminBackend: Send + Sync {
    async fn get_profile(
        &self,
        target_id: i64,
    ) -> Result<Option<AssistantProfileView>, UserAssistantAdminError>;

    async fn set_profile(
        &self,
        target_id: i64,
        actor_id: i64,
        input: ProfileInput,
    ) -> Result<AssistantProfileView, UserAssistantAdminError>;

    async fn list_memories(
        &self,
        target_id: i64,
    ) -> Result<Vec<AssistantMemoryView>, UserAssistantAdminError>;

    async fn save_memory(
        &self,
        target_id: i64,
        actor_id: i64,
        memory_id: i64,
        input: MemoryInput,
    ) -> Result<AssistantMemoryView, UserAssistantAdminError>;

    async fn delete_memory(
        &self,
        target_id: i64,
        memory_id: i64,
    ) -> Result<(), UserAssistantAdminError>;

    async fn list_archives(
        &self,
        target_id: i64,
        limit: i64,
    ) -> Result<Vec<DeveloperAccessArchive>, UserAssistantAdminError>;

    async fn target_role(&self, target_id: i64) -> Result<Option<i64>, UserAssistantAdminError>;
}

#[derive(Clone, Debug, Serialize)]
struct AssistantProfileView {
    profile_key: String,
    tags: Vec<String>,
    strategy: String,
    enabled: bool,
    source: String,
    updated_at: i64,
}

#[derive(Clone, Debug, Serialize)]
struct AssistantMemoryView {
    id: i64,
    title: String,
    content: String,
    tags: Vec<String>,
    source: String,
    enabled: bool,
    created_at: i64,
    updated_at: i64,
}

#[derive(Clone, Debug, Serialize)]
struct DeveloperAccessArchive {
    id: i64,
    user_id: i64,
    request_id: i64,
    source: String,
    reason: String,
    recommendation: String,
    admin_user_id: i64,
    admin_note: String,
    approved_at: i64,
    created_at: i64,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct ProfileInput {
    #[serde(default, rename = "profile_key")]
    profile_key: String,
    #[serde(default)]
    tags: Vec<String>,
    #[serde(default)]
    strategy: String,
    #[serde(default)]
    enabled: bool,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct MemoryInput {
    #[serde(default)]
    title: String,
    #[serde(default)]
    content: String,
    #[serde(default)]
    tags: Vec<String>,
    #[serde(default)]
    enabled: bool,
}

#[derive(Clone)]
struct PgUserAssistantAdminBackend {
    pg: PgPool,
}

async fn get_profile(
    State(state): State<UserAssistantAdminState>,
    headers: HeaderMap,
    Path(id): Path<String>,
) -> Response {
    let (actor, target_id) = match admin_scope(&state, &headers, &id).await {
        Ok(scope) => scope,
        Err(response) => return response,
    };
    let _ = actor;
    let response = match state.backend.get_profile(target_id).await {
        Ok(Some(profile)) => api_success(json!(profile)),
        Ok(None) => assistant_error(
            StatusCode::NOT_FOUND,
            "ASSISTANT_PROFILE_USER_NOT_FOUND",
            "assistant conversation not found",
        ),
        Err(error) => api_error(&error.0),
    };
    with_auth_version(response)
}

async fn update_profile(
    State(state): State<UserAssistantAdminState>,
    headers: HeaderMap,
    Path(id): Path<String>,
    request: axum::extract::Request,
) -> Response {
    let (actor, target_id) = match admin_scope(&state, &headers, &id).await {
        Ok(scope) => scope,
        Err(response) => return response,
    };
    let body = match to_bytes(request.into_body(), MAX_BODY_BYTES).await {
        Ok(bytes) => bytes,
        Err(_) => return with_auth_version(api_error("invalid assistant profile payload")),
    };
    let input: ProfileInput = match serde_json::from_slice(&body) {
        Ok(input) => input,
        Err(_) => return with_auth_version(api_error("invalid assistant profile payload")),
    };
    let response = match state.backend.set_profile(target_id, actor.id, input).await {
        Ok(profile) => api_success(json!(profile)),
        Err(error) => assistant_error(
            StatusCode::BAD_REQUEST,
            "ASSISTANT_PROFILE_INVALID",
            &error.0,
        ),
    };
    with_auth_version(response)
}

async fn list_memories(
    State(state): State<UserAssistantAdminState>,
    headers: HeaderMap,
    Path(id): Path<String>,
) -> Response {
    let (_, target_id) = match admin_scope(&state, &headers, &id).await {
        Ok(scope) => scope,
        Err(response) => return response,
    };
    let response = match state.backend.list_memories(target_id).await {
        Ok(memories) => api_success(json!(memories)),
        Err(error) => assistant_error(
            StatusCode::FORBIDDEN,
            "ASSISTANT_MEMORY_FORBIDDEN",
            &error.0,
        ),
    };
    with_auth_version(response)
}

async fn create_memory(
    State(state): State<UserAssistantAdminState>,
    headers: HeaderMap,
    Path(id): Path<String>,
    request: axum::extract::Request,
) -> Response {
    save_memory_handler(state, headers, id, 0, request).await
}

async fn update_memory(
    State(state): State<UserAssistantAdminState>,
    headers: HeaderMap,
    Path((id, memory_id)): Path<(String, String)>,
    request: axum::extract::Request,
) -> Response {
    let memory_id = match memory_id.parse::<i64>() {
        Ok(id) if id > 0 => id,
        _ => {
            return with_auth_version(assistant_error(
                StatusCode::BAD_REQUEST,
                "ASSISTANT_MEMORY_INVALID",
                "invalid assistant memory id",
            ));
        }
    };
    save_memory_handler(state, headers, id, memory_id, request).await
}

async fn save_memory_handler(
    state: UserAssistantAdminState,
    headers: HeaderMap,
    id: String,
    memory_id: i64,
    request: axum::extract::Request,
) -> Response {
    let (actor, target_id) = match admin_scope(&state, &headers, &id).await {
        Ok(scope) => scope,
        Err(response) => return response,
    };
    let body = match to_bytes(request.into_body(), MAX_BODY_BYTES).await {
        Ok(bytes) => bytes,
        Err(_) => {
            return with_auth_version(assistant_error(
                StatusCode::BAD_REQUEST,
                "ASSISTANT_MEMORY_INVALID",
                "invalid assistant memory payload",
            ));
        }
    };
    let input: MemoryInput = match serde_json::from_slice(&body) {
        Ok(input) => input,
        Err(_) => {
            return with_auth_version(assistant_error(
                StatusCode::BAD_REQUEST,
                "ASSISTANT_MEMORY_INVALID",
                "invalid assistant memory payload",
            ));
        }
    };
    let response = match state
        .backend
        .save_memory(target_id, actor.id, memory_id, input)
        .await
    {
        Ok(memory) => api_success(json!(memory)),
        Err(error) => {
            let status = if error.0.contains("not found") {
                StatusCode::NOT_FOUND
            } else {
                StatusCode::BAD_REQUEST
            };
            assistant_error(status, "ASSISTANT_MEMORY_INVALID", &error.0)
        }
    };
    with_auth_version(response)
}

async fn delete_memory(
    State(state): State<UserAssistantAdminState>,
    headers: HeaderMap,
    Path((id, memory_id)): Path<(String, String)>,
) -> Response {
    let (_, target_id) = match admin_scope(&state, &headers, &id).await {
        Ok(scope) => scope,
        Err(response) => return response,
    };
    let memory_id = match memory_id.parse::<i64>() {
        Ok(id) if id > 0 => id,
        _ => {
            return with_auth_version(assistant_error(
                StatusCode::BAD_REQUEST,
                "ASSISTANT_MEMORY_INVALID",
                "invalid assistant memory id",
            ));
        }
    };
    let response = match state.backend.delete_memory(target_id, memory_id).await {
        Ok(()) => api_success(json!({"deleted": true, "memory_id": memory_id})),
        Err(error) => {
            let status = if error.0.contains("not found") {
                StatusCode::NOT_FOUND
            } else {
                StatusCode::INTERNAL_SERVER_ERROR
            };
            assistant_error(status, "ASSISTANT_MEMORY_DELETE_FAILED", &error.0)
        }
    };
    with_auth_version(response)
}

async fn list_archives(
    State(state): State<UserAssistantAdminState>,
    headers: HeaderMap,
    Path(id): Path<String>,
    RawQuery(raw): RawQuery,
) -> Response {
    let (_, target_id) = match admin_scope(&state, &headers, &id).await {
        Ok(scope) => scope,
        Err(response) => return response,
    };
    let limit = raw
        .as_deref()
        .and_then(parse_query)
        .and_then(|query| query.get("limit").and_then(|v| v.parse().ok()))
        .unwrap_or(50)
        .clamp(1, 100);
    let response = match state.backend.list_archives(target_id, limit).await {
        Ok(archives) => api_success(json!(archives)),
        Err(error) => archive_error(
            StatusCode::FORBIDDEN,
            "DEVELOPER_ACCESS_ARCHIVE_FORBIDDEN",
            &error.0,
        ),
    };
    with_auth_version(response)
}

#[async_trait]
impl UserAssistantAdminBackend for PgUserAssistantAdminBackend {
    async fn get_profile(
        &self,
        target_id: i64,
    ) -> Result<Option<AssistantProfileView>, UserAssistantAdminError> {
        let row = sqlx::query(
            "SELECT profile_key, tags_json, strategy, enabled, source, updated_at \
             FROM assistant_user_profiles WHERE user_id = $1",
        )
        .bind(target_id)
        .fetch_optional(&self.pg)
        .await
        .map_err(db_error)?;
        row.as_ref().map(profile_from_row).transpose()
    }

    async fn set_profile(
        &self,
        target_id: i64,
        actor_id: i64,
        input: ProfileInput,
    ) -> Result<AssistantProfileView, UserAssistantAdminError> {
        let tags = serde_json::to_string(&input.tags).unwrap_or_else(|_| "[]".to_owned());
        let now = unix_timestamp();
        let row = sqlx::query(
            "INSERT INTO assistant_user_profiles (user_id, profile_key, tags_json, strategy, source, enabled, updated_by, created_at, updated_at) \
             VALUES ($1, $2, $3, $4, 'administrator', $5, $6, $7, $7) \
             ON CONFLICT (user_id) DO UPDATE SET profile_key = EXCLUDED.profile_key, tags_json = EXCLUDED.tags_json, \
             strategy = EXCLUDED.strategy, source = 'administrator', enabled = EXCLUDED.enabled, updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at \
             RETURNING profile_key, tags_json, strategy, enabled, source, updated_at",
        )
        .bind(target_id)
        .bind(input.profile_key.trim())
        .bind(tags)
        .bind(input.strategy.trim())
        .bind(input.enabled)
        .bind(actor_id)
        .bind(now)
        .fetch_one(&self.pg)
        .await
        .map_err(db_error)?;
        profile_from_row(&row)
    }

    async fn list_memories(
        &self,
        target_id: i64,
    ) -> Result<Vec<AssistantMemoryView>, UserAssistantAdminError> {
        let rows = sqlx::query(
            "SELECT id, title, content, tags_json, source, enabled, created_at, updated_at \
             FROM assistant_memories WHERE user_id = $1 ORDER BY updated_at DESC, id DESC LIMIT $2",
        )
        .bind(target_id)
        .bind(MAX_MEMORIES)
        .fetch_all(&self.pg)
        .await
        .map_err(db_error)?;
        rows.iter().map(memory_from_row).collect()
    }

    async fn save_memory(
        &self,
        target_id: i64,
        actor_id: i64,
        memory_id: i64,
        input: MemoryInput,
    ) -> Result<AssistantMemoryView, UserAssistantAdminError> {
        let title = input.title.trim();
        let content = input.content.trim();
        if title.is_empty() || content.is_empty() {
            return Err(UserAssistantAdminError(
                "assistant memory is invalid".to_owned(),
            ));
        }
        let tags = serde_json::to_string(&input.tags).unwrap_or_else(|_| "[]".to_owned());
        let now = unix_timestamp();
        let row = if memory_id > 0 {
            sqlx::query(
                "UPDATE assistant_memories SET title = $1, content = $2, tags_json = $3, source = 'administrator', \
                 enabled = $4, updated_by = $5, updated_at = $6 WHERE id = $7 AND user_id = $8 \
                 RETURNING id, title, content, tags_json, source, enabled, created_at, updated_at",
            )
            .bind(title)
            .bind(content)
            .bind(tags)
            .bind(input.enabled)
            .bind(actor_id)
            .bind(now)
            .bind(memory_id)
            .bind(target_id)
            .fetch_optional(&self.pg)
            .await
            .map_err(db_error)?
            .ok_or_else(|| UserAssistantAdminError("assistant memory not found".to_owned()))?
        } else {
            let count: i64 = sqlx::query_scalar(
                "SELECT COUNT(*)::BIGINT FROM assistant_memories WHERE user_id = $1",
            )
            .bind(target_id)
            .fetch_one(&self.pg)
            .await
            .map_err(db_error)?;
            if count >= MAX_MEMORIES {
                return Err(UserAssistantAdminError(
                    "assistant memory limit reached".to_owned(),
                ));
            }
            sqlx::query(
                "INSERT INTO assistant_memories (user_id, title, content, tags_json, source, enabled, updated_by, created_at, updated_at) \
                 VALUES ($1, $2, $3, $4, 'administrator', $5, $6, $7, $7) \
                 RETURNING id, title, content, tags_json, source, enabled, created_at, updated_at",
            )
            .bind(target_id)
            .bind(title)
            .bind(content)
            .bind(tags)
            .bind(input.enabled)
            .bind(actor_id)
            .bind(now)
            .fetch_one(&self.pg)
            .await
            .map_err(db_error)?
        };
        memory_from_row(&row)
    }

    async fn delete_memory(
        &self,
        target_id: i64,
        memory_id: i64,
    ) -> Result<(), UserAssistantAdminError> {
        let result = sqlx::query("DELETE FROM assistant_memories WHERE id = $1 AND user_id = $2")
            .bind(memory_id)
            .bind(target_id)
            .execute(&self.pg)
            .await
            .map_err(db_error)?;
        if result.rows_affected() == 0 {
            return Err(UserAssistantAdminError(
                "assistant memory not found".to_owned(),
            ));
        }
        Ok(())
    }

    async fn list_archives(
        &self,
        target_id: i64,
        limit: i64,
    ) -> Result<Vec<DeveloperAccessArchive>, UserAssistantAdminError> {
        let rows = sqlx::query(
            "SELECT id, user_id, request_id, source, reason, recommendation, admin_user_id, admin_note, approved_at, created_at \
             FROM developer_access_recommendation_archives WHERE user_id = $1 \
             ORDER BY approved_at DESC, id DESC LIMIT $2",
        )
        .bind(target_id)
        .bind(limit)
        .fetch_all(&self.pg)
        .await
        .map_err(db_error)?;
        rows.iter().map(archive_from_row).collect()
    }

    async fn target_role(&self, target_id: i64) -> Result<Option<i64>, UserAssistantAdminError> {
        sqlx::query_scalar("SELECT role FROM users WHERE id = $1 AND deleted_at IS NULL")
            .bind(target_id)
            .fetch_optional(&self.pg)
            .await
            .map_err(db_error)
    }
}

async fn admin_scope(
    state: &UserAssistantAdminState,
    headers: &HeaderMap,
    raw_id: &str,
) -> Result<(DashboardUserView, i64), Response> {
    let actor = authenticated_admin(state, headers).await?;
    let target_id = raw_id
        .parse::<i64>()
        .ok()
        .filter(|id| *id > 0)
        .ok_or_else(|| {
            assistant_error(
                StatusCode::BAD_REQUEST,
                "ASSISTANT_PROFILE_INVALID_USER",
                "invalid user id",
            )
        })?;
    let target_role = state
        .backend
        .target_role(target_id)
        .await
        .map_err(|error| api_error(&error.0))?;
    let Some(target_role) = target_role else {
        return Err(archive_error(
            StatusCode::NOT_FOUND,
            "DEVELOPER_ACCESS_USER_NOT_FOUND",
            "user was not found",
        ));
    };
    if actor.role <= target_role {
        return Err(assistant_error(
            StatusCode::FORBIDDEN,
            "ASSISTANT_PROFILE_FORBIDDEN",
            "you cannot manage this user",
        ));
    }
    Ok((actor, target_id))
}

async fn authenticated_admin(
    state: &UserAssistantAdminState,
    headers: &HeaderMap,
) -> Result<DashboardUserView, Response> {
    let Some(credential) = crate::migration_routes::legacy_http::dashboard_credential(headers)
    else {
        return Err(dashboard_auth_error(headers, None));
    };
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(credential))
        .await
        .map_err(|error| dashboard_auth_error(headers, Some(error.kind)))?;
    if !user.developer_access_granted {
        return Err(console_not_found());
    }
    enforce_user_auth_view(&user).map_err(|error| user_policy_error(headers, error))?;
    if user.role < ADMIN_ROLE {
        return Err(user_policy_error(
            headers,
            UserAuthPolicyError::InsufficientPrivilege,
        ));
    }
    Ok(user)
}

fn profile_from_row(row: &PgRow) -> Result<AssistantProfileView, UserAssistantAdminError> {
    let tags_raw: String = row_get(row, "tags_json")?;
    Ok(AssistantProfileView {
        profile_key: row_get(row, "profile_key")?,
        tags: serde_json::from_str(&tags_raw).unwrap_or_default(),
        strategy: row_get(row, "strategy")?,
        enabled: row_get(row, "enabled")?,
        source: row_get(row, "source")?,
        updated_at: row_get(row, "updated_at")?,
    })
}

fn memory_from_row(row: &PgRow) -> Result<AssistantMemoryView, UserAssistantAdminError> {
    let tags_raw: String = row_get(row, "tags_json")?;
    Ok(AssistantMemoryView {
        id: row_get(row, "id")?,
        title: row_get(row, "title")?,
        content: row_get(row, "content")?,
        tags: serde_json::from_str(&tags_raw).unwrap_or_default(),
        source: row_get(row, "source")?,
        enabled: row_get(row, "enabled")?,
        created_at: row_get(row, "created_at")?,
        updated_at: row_get(row, "updated_at")?,
    })
}

fn archive_from_row(row: &PgRow) -> Result<DeveloperAccessArchive, UserAssistantAdminError> {
    Ok(DeveloperAccessArchive {
        id: row_get(row, "id")?,
        user_id: row_get(row, "user_id")?,
        request_id: row_get(row, "request_id")?,
        source: row_get(row, "source")?,
        reason: row_get(row, "reason")?,
        recommendation: row_get(row, "recommendation")?,
        admin_user_id: row_get(row, "admin_user_id")?,
        admin_note: row_get(row, "admin_note")?,
        approved_at: row_get(row, "approved_at")?,
        created_at: row_get(row, "created_at")?,
    })
}

fn row_get<'r, T>(row: &'r PgRow, column: &str) -> Result<T, UserAssistantAdminError>
where
    T: sqlx::Decode<'r, sqlx::Postgres> + sqlx::Type<sqlx::Postgres>,
{
    row.try_get(column).map_err(db_error)
}

fn db_error(error: impl std::fmt::Display) -> UserAssistantAdminError {
    UserAssistantAdminError(error.to_string())
}

fn parse_query(raw: &str) -> Option<std::collections::HashMap<String, String>> {
    let mut values = std::collections::HashMap::new();
    for (key, value) in form_urlencoded::parse(raw.as_bytes()) {
        values.insert(key.into_owned(), value.into_owned());
    }
    Some(values)
}

fn unix_timestamp() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(0, |duration| {
            i64::try_from(duration.as_secs()).unwrap_or(i64::MAX)
        })
}

fn api_success(data: Value) -> Response {
    legacy_json(
        StatusCode::OK,
        json!({"success": true, "message": "", "data": data}),
    )
}

fn api_error(message: &str) -> Response {
    legacy_json(
        StatusCode::OK,
        json!({"success": false, "message": message}),
    )
}

fn assistant_error(status: StatusCode, code: &str, message: &str) -> Response {
    legacy_json(
        status,
        json!({"success": false, "code": code, "message": message}),
    )
}

fn archive_error(status: StatusCode, code: &str, message: &str) -> Response {
    assistant_error(status, code, message)
}

fn console_not_found() -> Response {
    legacy_json(StatusCode::NOT_FOUND, json!({"message": "Not Found"}))
}

fn legacy_json(status: StatusCode, body: Value) -> Response {
    let mut response = (status, Json(body)).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

fn with_auth_version(mut response: Response) -> Response {
    response.headers_mut().insert(
        HeaderName::from_static("auth-version"),
        HeaderValue::from_static(AUTH_VERSION),
    );
    response
}

fn dashboard_auth_error(headers: &HeaderMap, kind: Option<AuthErrorKind>) -> Response {
    if kind == Some(AuthErrorKind::UserDisabled) {
        return user_policy_error(headers, UserAuthPolicyError::UserDisabled);
    }
    legacy_json(
        StatusCode::UNAUTHORIZED,
        json!({"success": false, "code": "AUTH_UNAUTHORIZED", "message": "Unauthorized, invalid access token"}),
    )
}

fn user_policy_error(headers: &HeaderMap, error: UserAuthPolicyError) -> Response {
    legacy_json(
        StatusCode::from_u16(user_auth_status(error)).unwrap_or(StatusCode::UNAUTHORIZED),
        json!({
            "success": false,
            "code": "AUTH_INSUFFICIENT_PRIVILEGE",
            "message": user_auth_message(
                error,
                headers.get(header::ACCEPT_LANGUAGE).and_then(|value| value.to_str().ok()),
            ),
        }),
    )
}
