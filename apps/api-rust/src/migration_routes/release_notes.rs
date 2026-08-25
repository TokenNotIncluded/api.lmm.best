//! Legacy-compatible release-note discovery, acknowledgement, and publication routes.
//!
//! User acknowledgement is durable across sessions and devices. Publications
//! are immutable: publishing an existing version creates its next revision.

use std::sync::Arc;

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::{Bytes, to_bytes},
    extract::{Path, RawQuery, Request, State},
    http::{HeaderMap, HeaderName, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{PgPool, Row, postgres::PgRow};

use crate::{
    ClientIpKey,
    auth::{
        AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth, DashboardUserView,
        UserAuthPolicyError, dashboard_token_candidate, enforce_user_auth_view, user_auth_message,
        user_auth_status,
    },
    legacy_empty_response,
};

const ADMIN_ROLE: i64 = 10;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const BODY_LIMIT_BYTES: usize = 128 * 1024 * 1024;
const DEFAULT_LIST_LIMIT: i64 = 50;
const MAX_LIST_LIMIT: i64 = 100;
const MAX_VERSION_CHARS: usize = 128;
const MAX_CONTENT_CHARS: usize = 20_000;

/// PostgreSQL and dashboard-auth dependencies for the release-note routes.
#[derive(Clone)]
pub struct ReleaseNoteState {
    store: Arc<dyn ReleaseNoteStore>,
    auth: Arc<dyn DashboardAuth>,
    audit_pool: Option<PgPool>,
}

impl ReleaseNoteState {
    /// Creates production state backed by the listener's shared PostgreSQL pool.
    #[must_use]
    pub fn new(pool: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self {
            store: Arc::new(PgReleaseNoteStore { pool: pool.clone() }),
            auth,
            audit_pool: Some(pool),
        }
    }

    #[cfg(test)]
    fn with_store(store: Arc<dyn ReleaseNoteStore>, auth: Arc<dyn DashboardAuth>) -> Self {
        Self {
            store,
            auth,
            audit_pool: None,
        }
    }
}

/// Builds the two user and two administrator release-note routes.
pub fn router(state: ReleaseNoteState) -> Router {
    Router::new()
        .route("/api/release-notes/latest", get(latest_unread))
        .route("/api/release-notes/{id}/read", post(mark_read))
        .route(
            "/api/release-notes/admin",
            get(list_admin).post(publish_admin),
        )
        .with_state(state)
}

#[derive(Clone, Debug)]
struct Principal {
    user: DashboardUserView,
    credential: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct PublishInput {
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    version: String,
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    content: String,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
struct ReleaseNote {
    id: i64,
    version: String,
    revision: i64,
    content: String,
    published_at: i64,
    published_by: i64,
}

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
enum ReleaseNoteStoreError {
    #[error("release note not found")]
    NotFound,
    #[error("invalid release publisher")]
    InvalidPublisher,
    #[error("{0}")]
    Database(String),
}

#[async_trait]
trait ReleaseNoteStore: Send + Sync {
    async fn session_created_at(&self, session_id: &str) -> Result<i64, ReleaseNoteStoreError>;

    async fn latest_unread(
        &self,
        user_id: i64,
        session_created_at: i64,
    ) -> Result<Option<ReleaseNote>, ReleaseNoteStoreError>;

    async fn mark_read(&self, user_id: i64, note_id: i64) -> Result<(), ReleaseNoteStoreError>;

    async fn list(&self, limit: i64) -> Result<Vec<ReleaseNote>, ReleaseNoteStoreError>;

    async fn publish(
        &self,
        publisher_id: i64,
        version: &str,
        content: &str,
    ) -> Result<ReleaseNote, ReleaseNoteStoreError>;
}

#[derive(Clone)]
struct PgReleaseNoteStore {
    pool: PgPool,
}

#[async_trait]
impl ReleaseNoteStore for PgReleaseNoteStore {
    async fn session_created_at(&self, session_id: &str) -> Result<i64, ReleaseNoteStoreError> {
        sqlx::query_scalar::<_, i64>("SELECT created_at FROM user_sessions WHERE sid = $1")
            .bind(session_id)
            .fetch_optional(&self.pool)
            .await
            .map_err(database_error)?
            .ok_or_else(|| ReleaseNoteStoreError::Database("record not found".to_owned()))
    }

    async fn latest_unread(
        &self,
        user_id: i64,
        session_created_at: i64,
    ) -> Result<Option<ReleaseNote>, ReleaseNoteStoreError> {
        let note = sqlx::query(
            "SELECT id::BIGINT AS id, version, revision::BIGINT AS revision, content, \
             published_at::BIGINT AS published_at, published_by::BIGINT AS published_by \
             FROM release_notes ORDER BY published_at DESC, id DESC LIMIT 1",
        )
        .fetch_optional(&self.pool)
        .await
        .map_err(database_error)?
        .map(release_note_from_row)
        .transpose()
        .map_err(database_error)?;
        let Some(note) = note else {
            return Ok(None);
        };
        if session_created_at > 0 && note.published_at > session_created_at {
            return Ok(None);
        }
        let read_count = sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*)::BIGINT FROM release_note_reads \
             WHERE release_note_id = $1 AND user_id = $2",
        )
        .bind(note.id)
        .bind(user_id)
        .fetch_one(&self.pool)
        .await
        .map_err(database_error)?;
        Ok((read_count == 0).then_some(note))
    }

    async fn mark_read(&self, user_id: i64, note_id: i64) -> Result<(), ReleaseNoteStoreError> {
        let note_count = sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*)::BIGINT FROM release_notes WHERE id = $1",
        )
        .bind(note_id)
        .fetch_one(&self.pool)
        .await
        .map_err(database_error)?;
        if note_count == 0 {
            return Err(ReleaseNoteStoreError::NotFound);
        }
        sqlx::query(
            "INSERT INTO release_note_reads (release_note_id, user_id, read_at) \
             VALUES ($1, $2, EXTRACT(EPOCH FROM NOW())::BIGINT) \
             ON CONFLICT (release_note_id, user_id) DO NOTHING",
        )
        .bind(note_id)
        .bind(user_id)
        .execute(&self.pool)
        .await
        .map_err(database_error)?;
        Ok(())
    }

    async fn list(&self, limit: i64) -> Result<Vec<ReleaseNote>, ReleaseNoteStoreError> {
        let rows = sqlx::query(
            "SELECT id::BIGINT AS id, version, revision::BIGINT AS revision, content, \
             published_at::BIGINT AS published_at, published_by::BIGINT AS published_by \
             FROM release_notes ORDER BY published_at DESC, id DESC LIMIT $1",
        )
        .bind(limit)
        .fetch_all(&self.pool)
        .await
        .map_err(database_error)?;
        rows.into_iter()
            .map(release_note_from_row)
            .collect::<Result<Vec<_>, _>>()
            .map_err(database_error)
    }

    async fn publish(
        &self,
        publisher_id: i64,
        version: &str,
        content: &str,
    ) -> Result<ReleaseNote, ReleaseNoteStoreError> {
        if publisher_id <= 0 {
            return Err(ReleaseNoteStoreError::InvalidPublisher);
        }
        let mut transaction = self.pool.begin().await.map_err(database_error)?;
        let latest_revision = sqlx::query_scalar::<_, i64>(
            "SELECT revision::BIGINT FROM release_notes WHERE version = $1 \
             ORDER BY revision DESC LIMIT 1 FOR UPDATE",
        )
        .bind(version)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(database_error)?;
        let revision = latest_revision.map_or(1, |revision| revision + 1);
        let row = sqlx::query(
            "INSERT INTO release_notes \
             (version, revision, content, published_at, published_by) \
             VALUES ($1, $2, $3, EXTRACT(EPOCH FROM NOW())::BIGINT, $4) \
             RETURNING id::BIGINT AS id, version, revision::BIGINT AS revision, content, \
             published_at::BIGINT AS published_at, published_by::BIGINT AS published_by",
        )
        .bind(version)
        .bind(revision)
        .bind(content)
        .bind(publisher_id)
        .fetch_one(&mut *transaction)
        .await
        .map_err(database_error)?;
        let note = release_note_from_row(row).map_err(database_error)?;
        transaction.commit().await.map_err(database_error)?;
        Ok(note)
    }
}

fn database_error(error: sqlx::Error) -> ReleaseNoteStoreError {
    ReleaseNoteStoreError::Database(error.to_string())
}

fn release_note_from_row(row: PgRow) -> Result<ReleaseNote, sqlx::Error> {
    Ok(ReleaseNote {
        id: row.try_get("id")?,
        version: row.try_get("version")?,
        revision: row.try_get("revision")?,
        content: row.try_get("content")?,
        published_at: row.try_get("published_at")?,
        published_by: row.try_get("published_by")?,
    })
}

async fn latest_unread(State(state): State<ReleaseNoteState>, headers: HeaderMap) -> Response {
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let session_created_at = match state
        .auth
        .current_session(SecretString::from(principal.credential.clone()))
        .await
    {
        Ok(session) if session.user.id == principal.user.id => {
            match state.store.session_created_at(&session.session_id).await {
                Ok(created_at) => created_at,
                Err(error) => {
                    return authenticated_handler_response(api_error(error.to_string()));
                }
            }
        }
        Ok(_) => {
            return authenticated_handler_response(api_error(
                "dashboard authentication failed: Unauthorized".to_owned(),
            ));
        }
        Err(error) if error.kind == AuthErrorKind::Unauthorized => 0,
        Err(error) => {
            return authenticated_handler_response(api_error(error.to_string()));
        }
    };
    let response = match state
        .store
        .latest_unread(principal.user.id, session_created_at)
        .await
    {
        Ok(Some(note)) => api_success(json!(note)),
        Ok(None) => api_success(Value::Null),
        Err(error) => api_error(error.to_string()),
    };
    authenticated_handler_response(response)
}

async fn mark_read(
    State(state): State<ReleaseNoteState>,
    Path(note_id): Path<String>,
    headers: HeaderMap,
) -> Response {
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let note_id = match note_id.parse::<i64>() {
        Ok(note_id) if note_id > 0 => note_id,
        _ => {
            return authenticated_handler_response(release_note_error(
                StatusCode::BAD_REQUEST,
                "RELEASE_NOTE_INVALID_ID",
                "invalid release note id",
            ));
        }
    };
    let response = match state.store.mark_read(principal.user.id, note_id).await {
        Ok(()) => api_success(Value::Null),
        Err(ReleaseNoteStoreError::NotFound) => release_note_error(
            StatusCode::NOT_FOUND,
            "RELEASE_NOTE_NOT_FOUND",
            "release note not found",
        ),
        Err(error) => api_error(error.to_string()),
    };
    authenticated_handler_response(response)
}

async fn list_admin(
    State(state): State<ReleaseNoteState>,
    RawQuery(raw_query): RawQuery,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let limit = release_note_limit(raw_query.as_deref());
    let response = match state.store.list(limit).await {
        Ok(notes) => api_success(json!(notes)),
        Err(error) => api_error(error.to_string()),
    };
    authenticated_handler_response(response)
}

async fn publish_admin(State(state): State<ReleaseNoteState>, request: Request) -> Response {
    let principal = match authenticated_admin(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let client_ip = request
        .extensions()
        .get::<ClientIpKey>()
        .map_or_else(String::new, |value| value.0.clone());
    let auth_method = if dashboard_token_candidate(&principal.credential) {
        "session"
    } else {
        "access_token"
    };
    let rate_response = match state.auth.check_critical_rate_limit(&client_ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => None,
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => Some(legacy_empty_response(
            StatusCode::TOO_MANY_REQUESTS,
            Some(retry_after_seconds),
        )),
        Err(_) => Some(legacy_empty_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            None,
        )),
    };
    if let Some(response) = rate_response {
        return audited_publish_response(
            &state,
            &principal,
            auth_method,
            &client_ip,
            with_auth_version(response),
            false,
        )
        .await;
    }
    let input = match parse_publish_input(request).await {
        Ok(input) => input,
        Err(response) => {
            return audited_publish_response(
                &state,
                &principal,
                auth_method,
                &client_ip,
                authenticated_handler_response(response),
                false,
            )
            .await;
        }
    };
    let (version, content) = match normalize_release_note(&input.version, &input.content) {
        Ok(normalized) => normalized,
        Err(message) => {
            let response = release_note_error(
                StatusCode::UNPROCESSABLE_ENTITY,
                "RELEASE_NOTE_VALIDATION_FAILED",
                message,
            );
            return audited_publish_response(
                &state,
                &principal,
                auth_method,
                &client_ip,
                authenticated_handler_response(response),
                false,
            )
            .await;
        }
    };
    let (response, success) = match state
        .store
        .publish(principal.user.id, &version, &content)
        .await
    {
        Ok(note) => (api_success(json!(note)), true),
        Err(ReleaseNoteStoreError::InvalidPublisher) => (
            release_note_error(
                StatusCode::BAD_REQUEST,
                "RELEASE_NOTE_INVALID_PUBLISHER",
                "invalid release publisher",
            ),
            false,
        ),
        Err(error) => (api_error(error.to_string()), false),
    };
    audited_publish_response(
        &state,
        &principal,
        auth_method,
        &client_ip,
        authenticated_handler_response(response),
        success,
    )
    .await
}

async fn parse_publish_input(request: Request) -> Result<PublishInput, Response> {
    let body = to_bytes(request.into_body(), BODY_LIMIT_BYTES)
        .await
        .map_err(|_| invalid_publish_request())?;
    deserialize_one_nullable::<PublishInput>(&body)
        .map(Option::unwrap_or_default)
        .map_err(|_| invalid_publish_request())
}

fn deserialize_one_nullable<T>(body: &Bytes) -> Result<Option<T>, serde_json::Error>
where
    T: for<'de> Deserialize<'de>,
{
    let mut deserializer = serde_json::Deserializer::from_slice(body);
    Option::<T>::deserialize(&mut deserializer)
}

fn deserialize_nullable_string<'de, D>(deserializer: D) -> Result<String, D::Error>
where
    D: serde::Deserializer<'de>,
{
    Option::<String>::deserialize(deserializer).map(Option::unwrap_or_default)
}

fn normalize_release_note(version: &str, content: &str) -> Result<(String, String), &'static str> {
    let version = version.trim();
    let content = content.trim();
    if version.is_empty() {
        return Err("release version is required");
    }
    if version.chars().count() > MAX_VERSION_CHARS {
        return Err("release version must be at most 128 characters");
    }
    if !valid_release_version(version) {
        return Err("release version contains unsupported characters");
    }
    if content.is_empty() {
        return Err("release changelog is required");
    }
    if content.chars().count() > MAX_CONTENT_CHARS {
        return Err("release changelog must be at most 20000 characters");
    }
    Ok((version.to_owned(), content.to_owned()))
}

fn valid_release_version(version: &str) -> bool {
    let mut bytes = version.bytes();
    let Some(first) = bytes.next() else {
        return false;
    };
    first.is_ascii_alphanumeric()
        && bytes
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'+' | b'-'))
}

fn release_note_limit(raw_query: Option<&str>) -> i64 {
    let parsed = raw_query
        .and_then(|query| {
            form_urlencoded::parse(query.as_bytes())
                .find(|(key, _)| key == "limit")
                .and_then(|(_, value)| value.parse::<i64>().ok())
        })
        .unwrap_or_default();
    if parsed <= 0 || parsed > MAX_LIST_LIMIT {
        DEFAULT_LIST_LIMIT
    } else {
        parsed
    }
}

async fn authenticated_user(
    state: &ReleaseNoteState,
    headers: &HeaderMap,
) -> Result<Principal, Response> {
    let Some(credential) = dashboard_credential(headers) else {
        return Err(dashboard_auth_error(headers, None));
    };
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(credential.clone()))
        .await
        .map_err(|error| dashboard_auth_error(headers, Some(error.kind)))?;
    enforce_user_auth_view(&user).map_err(|error| user_policy_error(headers, error))?;
    Ok(Principal { user, credential })
}

async fn authenticated_admin(
    state: &ReleaseNoteState,
    headers: &HeaderMap,
) -> Result<Principal, Response> {
    let Some(credential) = dashboard_credential(headers) else {
        return Err(dashboard_auth_error(headers, None));
    };
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(credential.clone()))
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
    Ok(Principal { user, credential })
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

fn dashboard_auth_error(headers: &HeaderMap, kind: Option<AuthErrorKind>) -> Response {
    if kind == Some(AuthErrorKind::UserDisabled) {
        return user_policy_error(headers, UserAuthPolicyError::UserDisabled);
    }
    let (status, code, message) = match kind {
        Some(AuthErrorKind::TokenExpired) => (
            StatusCode::UNAUTHORIZED,
            "AUTH_TOKEN_EXPIRED",
            auth_not_logged_in(headers),
        ),
        Some(AuthErrorKind::SessionRevoked) => (
            StatusCode::UNAUTHORIZED,
            "AUTH_SESSION_REVOKED",
            auth_not_logged_in(headers),
        ),
        Some(AuthErrorKind::Internal) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            "AUTH_INTERNAL_ERROR",
            auth_database_error(headers),
        ),
        _ => (
            StatusCode::UNAUTHORIZED,
            "AUTH_UNAUTHORIZED",
            auth_invalid_access_token(headers),
        ),
    };
    legacy_json(
        status,
        json!({"success": false, "code": code, "message": message}),
    )
}

fn user_policy_error(headers: &HeaderMap, error: UserAuthPolicyError) -> Response {
    let code = match error {
        UserAuthPolicyError::UserDisabled => "AUTH_USER_DISABLED",
        UserAuthPolicyError::InsufficientPrivilege => "AUTH_INSUFFICIENT_PRIVILEGE",
        UserAuthPolicyError::InvalidUserInfo => "AUTH_USER_INVALID",
    };
    legacy_json(
        StatusCode::from_u16(user_auth_status(error)).unwrap_or(StatusCode::UNAUTHORIZED),
        json!({
            "success": false,
            "code": code,
            "message": user_auth_message(
                error,
                headers.get(header::ACCEPT_LANGUAGE).and_then(|value| value.to_str().ok()),
            ),
        }),
    )
}

fn token_locale(headers: &HeaderMap) -> TokenLocale {
    let language = headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.split(',').next())
        .and_then(|value| value.split(';').next())
        .unwrap_or_default()
        .trim()
        .to_ascii_lowercase();
    if language.starts_with("zh-tw") {
        TokenLocale::ZhTw
    } else if language.starts_with("zh") {
        TokenLocale::ZhCn
    } else {
        TokenLocale::En
    }
}

#[derive(Clone, Copy)]
enum TokenLocale {
    En,
    ZhCn,
    ZhTw,
}

fn auth_not_logged_in(headers: &HeaderMap) -> &'static str {
    match token_locale(headers) {
        TokenLocale::En => "Unauthorized, not logged in and no access token provided",
        TokenLocale::ZhCn => "无权进行此操作，未登录且未提供 access token",
        TokenLocale::ZhTw => "無權進行此操作，未登入且未提供 access token",
    }
}

fn auth_invalid_access_token(headers: &HeaderMap) -> &'static str {
    match token_locale(headers) {
        TokenLocale::En => "Unauthorized, invalid access token",
        TokenLocale::ZhCn => "无权进行此操作，access token 无效",
        TokenLocale::ZhTw => "無權進行此操作，access token 無效",
    }
}

fn auth_database_error(headers: &HeaderMap) -> &'static str {
    match token_locale(headers) {
        TokenLocale::En => "Database error, please contact the administrator",
        TokenLocale::ZhCn => "数据库出错，请联系管理员",
        TokenLocale::ZhTw => "資料庫出錯，請聯繫管理員",
    }
}

fn api_success(data: Value) -> Response {
    legacy_json(
        StatusCode::OK,
        json!({"success": true, "message": "", "data": data}),
    )
}

fn api_error(message: String) -> Response {
    legacy_json(
        StatusCode::OK,
        json!({"success": false, "message": message}),
    )
}

fn release_note_error(status: StatusCode, code: &'static str, message: &'static str) -> Response {
    legacy_json(
        status,
        json!({"success": false, "code": code, "message": message}),
    )
}

fn invalid_publish_request() -> Response {
    release_note_error(
        StatusCode::UNPROCESSABLE_ENTITY,
        "RELEASE_NOTE_INVALID_REQUEST",
        "invalid release note request",
    )
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

fn authenticated_handler_response(response: Response) -> Response {
    with_auth_version(with_disabled_cache(response))
}

fn with_auth_version(mut response: Response) -> Response {
    response.headers_mut().insert(
        HeaderName::from_static("auth-version"),
        HeaderValue::from_static(AUTH_VERSION),
    );
    response
}

fn with_disabled_cache(mut response: Response) -> Response {
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        HeaderValue::from_static("no-store, no-cache, must-revalidate, private, max-age=0"),
    );
    response
        .headers_mut()
        .insert(header::PRAGMA, HeaderValue::from_static("no-cache"));
    response
        .headers_mut()
        .insert(header::EXPIRES, HeaderValue::from_static("0"));
    response
}

async fn audited_publish_response(
    state: &ReleaseNoteState,
    principal: &Principal,
    auth_method: &str,
    client_ip: &str,
    response: Response,
    success: bool,
) -> Response {
    record_publish_audit(
        state,
        principal,
        auth_method,
        client_ip,
        response.status(),
        success,
    )
    .await;
    response
}

async fn record_publish_audit(
    state: &ReleaseNoteState,
    principal: &Principal,
    auth_method: &str,
    client_ip: &str,
    status: StatusCode,
    success: bool,
) {
    let Some(pool) = &state.audit_pool else {
        return;
    };
    let route = "/api/release-notes/admin";
    let other = json!({
        "op": {
            "action": "generic",
            "params": {"method": "POST", "route": route},
        },
        "admin_info": {
            "admin_id": principal.user.id,
            "admin_username": principal.user.username,
            "admin_role": principal.user.role,
            "auth_method": auth_method,
        },
        "audit_info": {
            "method": "POST",
            "route": route,
            "path": route,
            "status": status.as_u16(),
            "success": success,
        },
    });
    let _ = sqlx::query(
        "INSERT INTO logs (user_id, created_at, type, content, username, ip, other) \
         VALUES ($1, EXTRACT(EPOCH FROM NOW())::BIGINT, 3, $2, $3, $4, $5)",
    )
    .bind(principal.user.id)
    .bind(format!("POST {route}"))
    .bind(&principal.user.username)
    .bind(client_ip)
    .bind(other.to_string())
    .execute(pool)
    .await;
}

#[cfg(test)]
mod tests {
    use std::sync::{Arc, Mutex};

    use axum::{
        body::{Body, to_bytes},
        http::{Request as HttpRequest, header},
    };
    use tower::ServiceExt;

    use super::*;
    use crate::auth::{
        AuthBundle, AuthError, DashboardSessionContext, DashboardUser, LoginOutcome, LoginRequest,
        LogoutRequest, LogoutResult, RequestMetadata, TwoFactorLoginRequest,
    };

    #[derive(Clone)]
    struct FixtureStore {
        latest: Option<ReleaseNote>,
        mark_result: Result<(), ReleaseNoteStoreError>,
        listed: Vec<ReleaseNote>,
        publish_result: Result<ReleaseNote, ReleaseNoteStoreError>,
        observed_session_times: Arc<Mutex<Vec<i64>>>,
    }

    impl Default for FixtureStore {
        fn default() -> Self {
            Self {
                latest: None,
                mark_result: Ok(()),
                listed: Vec::new(),
                publish_result: Ok(note(2, 1)),
                observed_session_times: Arc::new(Mutex::new(Vec::new())),
            }
        }
    }

    #[async_trait]
    impl ReleaseNoteStore for FixtureStore {
        async fn session_created_at(&self, _: &str) -> Result<i64, ReleaseNoteStoreError> {
            Ok(80)
        }

        async fn latest_unread(
            &self,
            _: i64,
            session_created_at: i64,
        ) -> Result<Option<ReleaseNote>, ReleaseNoteStoreError> {
            self.observed_session_times
                .lock()
                .expect("session observations")
                .push(session_created_at);
            Ok(self.latest.clone())
        }

        async fn mark_read(&self, _: i64, _: i64) -> Result<(), ReleaseNoteStoreError> {
            self.mark_result.clone()
        }

        async fn list(&self, _: i64) -> Result<Vec<ReleaseNote>, ReleaseNoteStoreError> {
            Ok(self.listed.clone())
        }

        async fn publish(
            &self,
            _: i64,
            _: &str,
            _: &str,
        ) -> Result<ReleaseNote, ReleaseNoteStoreError> {
            self.publish_result.clone()
        }
    }

    #[derive(Clone)]
    struct FixtureAuth {
        user: DashboardUser,
        auth_error: Option<AuthErrorKind>,
        critical: CriticalRateLimitOutcome,
        browser_session: bool,
    }

    #[async_trait]
    impl DashboardAuth for FixtureAuth {
        async fn check_critical_rate_limit(
            &self,
            _: &str,
        ) -> Result<CriticalRateLimitOutcome, AuthError> {
            Ok(self.critical)
        }

        async fn login(
            &self,
            _: LoginRequest,
            _: RequestMetadata,
        ) -> Result<LoginOutcome, AuthError> {
            Err(AuthError::new(AuthErrorKind::Internal))
        }

        async fn login_2fa(
            &self,
            _: TwoFactorLoginRequest,
            _: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            Err(AuthError::new(AuthErrorKind::Internal))
        }

        async fn refresh(
            &self,
            _: SecretString,
            _: Option<String>,
            _: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            Err(AuthError::new(AuthErrorKind::Internal))
        }

        async fn self_user(&self, _: SecretString) -> Result<DashboardUser, AuthError> {
            self.auth_error
                .map_or_else(|| Ok(self.user.clone()), |kind| Err(AuthError::new(kind)))
        }

        async fn current_session(
            &self,
            _: SecretString,
        ) -> Result<DashboardSessionContext, AuthError> {
            if self.browser_session {
                Ok(DashboardSessionContext {
                    user: self.user.clone(),
                    session_id: "sid-1".to_owned(),
                    session_version: 1,
                    user_auth_version: 1,
                    client_ip: String::new(),
                    user_agent: String::new(),
                })
            } else {
                Err(AuthError::new(AuthErrorKind::Unauthorized))
            }
        }

        async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, AuthError> {
            Err(AuthError::new(AuthErrorKind::Internal))
        }

        async fn generate_personal_access_token(
            &self,
            _: SecretString,
        ) -> Result<String, AuthError> {
            Err(AuthError::new(AuthErrorKind::Internal))
        }
    }

    fn dashboard_user(role: i64) -> DashboardUser {
        DashboardUser {
            id: 7,
            username: "release-user".to_owned(),
            display_name: String::new(),
            role,
            status: 1,
            email: String::new(),
            github_id: String::new(),
            discord_id: String::new(),
            oidc_id: String::new(),
            wechat_id: String::new(),
            telegram_id: String::new(),
            group: "default".to_owned(),
            quota: 0,
            used_quota: 0,
            request_count: 0,
            aff_code: String::new(),
            aff_count: 0,
            aff_quota: 0,
            aff_history_quota: 0,
            inviter_id: 0,
            linux_do_id: String::new(),
            setting: String::new(),
            stripe_customer: String::new(),
            sidebar_modules: json!({}),
            permissions: json!({}),
        }
    }

    fn fixture_auth(role: i64) -> FixtureAuth {
        FixtureAuth {
            user: dashboard_user(role),
            auth_error: None,
            critical: CriticalRateLimitOutcome::Allowed,
            browser_session: false,
        }
    }

    fn note(id: i64, revision: i64) -> ReleaseNote {
        ReleaseNote {
            id,
            version: "v1.2.3".to_owned(),
            revision,
            content: "- changes".to_owned(),
            published_at: 75,
            published_by: 10,
        }
    }

    fn test_router(store: FixtureStore, auth: FixtureAuth) -> Router {
        router(ReleaseNoteState::with_store(
            Arc::new(store),
            Arc::new(auth),
        ))
    }

    async fn response_json(response: Response) -> Value {
        let bytes = to_bytes(response.into_body(), 1024 * 1024)
            .await
            .expect("response body");
        serde_json::from_slice(&bytes).expect("response json")
    }

    #[test]
    fn normalize_release_note_should_trim_valid_fields() {
        assert_eq!(
            normalize_release_note("  v1.2.3  ", "  - changes  "),
            Ok(("v1.2.3".to_owned(), "- changes".to_owned()))
        );
    }

    #[test]
    fn normalize_release_note_should_reject_non_ascii_version() {
        assert_eq!(
            normalize_release_note("版本1", "changes"),
            Err("release version contains unsupported characters")
        );
    }

    #[test]
    fn release_note_limit_should_fall_back_when_over_maximum() {
        assert_eq!(release_note_limit(Some("limit=101")), 50);
    }

    #[tokio::test]
    async fn latest_should_use_browser_session_creation_time() {
        let store = FixtureStore {
            latest: Some(note(1, 1)),
            ..FixtureStore::default()
        };
        let observations = Arc::clone(&store.observed_session_times);
        let mut auth = fixture_auth(1);
        auth.browser_session = true;
        let response = test_router(store, auth)
            .oneshot(
                HttpRequest::get("/api/release-notes/latest")
                    .header(header::AUTHORIZATION, "Bearer token")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        let body = response_json(response).await;
        assert_eq!(
            (observations.lock().expect("observations").clone(), body),
            (
                vec![80],
                json!({
                    "success": true,
                    "message": "",
                    "data": {
                        "id": 1,
                        "version": "v1.2.3",
                        "revision": 1,
                        "content": "- changes",
                        "published_at": 75,
                        "published_by": 10,
                    }
                })
            )
        );
    }

    #[tokio::test]
    async fn admin_list_should_hide_from_authenticated_l0_user() {
        let response = test_router(FixtureStore::default(), fixture_auth(1))
            .oneshot(
                HttpRequest::get("/api/release-notes/admin")
                    .header(header::AUTHORIZATION, "token")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(
            (response.status(), response_json(response).await),
            (StatusCode::NOT_FOUND, json!({"message": "Not Found"}))
        );
    }

    #[tokio::test]
    async fn publish_rate_limit_should_precede_disable_cache() {
        let mut auth = fixture_auth(10);
        auth.critical = CriticalRateLimitOutcome::Rejected {
            retry_after_seconds: 17,
        };
        let response = test_router(FixtureStore::default(), auth)
            .oneshot(
                HttpRequest::post("/api/release-notes/admin")
                    .header(header::AUTHORIZATION, "token")
                    .body(Body::from("not-json"))
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(
            (
                response.status(),
                response.headers().get(header::RETRY_AFTER),
                response.headers().get(header::CACHE_CONTROL),
            ),
            (
                StatusCode::TOO_MANY_REQUESTS,
                Some(&HeaderValue::from_static("17")),
                None,
            )
        );
    }

    #[tokio::test]
    async fn publish_null_body_should_reach_validation() {
        let response = test_router(FixtureStore::default(), fixture_auth(10))
            .oneshot(
                HttpRequest::post("/api/release-notes/admin")
                    .header(header::AUTHORIZATION, "token")
                    .body(Body::from("null"))
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(
            (response.status(), response_json(response).await),
            (
                StatusCode::UNPROCESSABLE_ENTITY,
                json!({
                    "success": false,
                    "code": "RELEASE_NOTE_VALIDATION_FAILED",
                    "message": "release version is required",
                })
            )
        );
    }

    #[tokio::test]
    async fn mark_read_should_return_named_not_found_error() {
        let store = FixtureStore {
            mark_result: Err(ReleaseNoteStoreError::NotFound),
            ..FixtureStore::default()
        };
        let response = test_router(store, fixture_auth(1))
            .oneshot(
                HttpRequest::post("/api/release-notes/42/read")
                    .header(header::AUTHORIZATION, "token")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(
            (response.status(), response_json(response).await),
            (
                StatusCode::NOT_FOUND,
                json!({
                    "success": false,
                    "code": "RELEASE_NOTE_NOT_FOUND",
                    "message": "release note not found",
                })
            )
        );
    }
}
