//! Legacy-compatible L1 developer-access request routes.
//!
//! The self-service submission path accepts only a short-lived draft created
//! by the server-owned assistant. Administrator approval records the durable
//! console activation in the same PostgreSQL transaction as the review; the
//! derived Valkey user cache is evicted only after that transaction commits.

use std::sync::Arc;

use axum::{
    Json, Router,
    body::{Bytes, to_bytes},
    extract::{Path, RawQuery, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{FromRow, PgPool, Postgres, Row, Transaction, postgres::PgRow};

use super::assistant::redact_assistant_handoff_message;
use crate::{
    ClientIpKey,
    auth::{
        AssistantL1ConfirmationError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth,
        DashboardUserView, UserAuthPolicyError, enforce_user_auth_view, user_auth_message,
        user_auth_status,
    },
    legacy_empty_response,
};

const ADMIN_ROLE: i64 = 10;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const BODY_LIMIT_BYTES: usize = 64 * 1024 * 1024;
const REQUEST_LIMIT: i64 = 100;
const MIN_REASON_CHARS: usize = 5;
const MIN_RECOMMENDATION_CHARS: usize = 20;
const MIN_REVIEW_NOTE_CHARS: usize = 2;
const MAX_NOTE_CHARS: usize = 2_000;
const STATUS_PENDING: &str = "pending";
const STATUS_APPROVED: &str = "approved";
const STATUS_REJECTED: &str = "rejected";
const SOURCE_ASSISTANT: &str = "assistant_recommendation";

const REQUEST_COLUMNS: &str = "id, user_id, COALESCE(status, '') AS status, \
    COALESCE(source, 'legacy') AS source, COALESCE(reason, '') AS reason, \
    COALESCE(ai_recommendation, '') AS ai_recommendation, \
    COALESCE(admin_user_id, 0) AS admin_user_id, \
    COALESCE(admin_note, '') AS admin_note, COALESCE(created_at, 0) AS created_at, \
    COALESCE(reviewed_at, 0) AS reviewed_at";

/// PostgreSQL, Valkey, and dashboard-auth dependencies for this route slice.
#[derive(Clone)]
pub struct DeveloperAccessState {
    pg: PgPool,
    valkey: redis::Client,
    auth: Arc<dyn DashboardAuth>,
}

impl DeveloperAccessState {
    /// Creates production state backed by the listener's shared dependencies.
    #[must_use]
    pub fn new(pg: PgPool, valkey: redis::Client, auth: Arc<dyn DashboardAuth>) -> Self {
        Self { pg, valkey, auth }
    }
}

/// Builds the two self-service and three administrator request routes.
pub fn router(state: DeveloperAccessState) -> Router {
    Router::new()
        .route(
            "/api/user/developer-access/request",
            get(get_self_request).post(submit_self_request),
        )
        .route("/api/developer-access/requests", get(list_admin_requests))
        .route(
            "/api/developer-access/requests/{id}/approve",
            post(approve_request),
        )
        .route(
            "/api/developer-access/requests/{id}/reject",
            post(reject_request),
        )
        .with_state(state)
}

#[derive(Clone, Debug)]
struct Principal {
    user: DashboardUserView,
    credential: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct SubmissionInput {
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    reason: String,
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    ai_recommendation: String,
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    confirmation_token: String,
    #[serde(default, deserialize_with = "deserialize_nullable_bool")]
    confirmed: bool,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct ReviewInput {
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    note: String,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq)]
struct RecommendationDraft {
    user_statement: String,
    recommendation: String,
}

#[derive(Clone, Debug, Serialize)]
struct DeveloperAccessRequest {
    id: i64,
    user_id: i64,
    status: String,
    source: String,
    reason: String,
    ai_recommendation: String,
    admin_user_id: i64,
    admin_note: String,
    created_at: i64,
    reviewed_at: i64,
}

impl<'row> FromRow<'row, PgRow> for DeveloperAccessRequest {
    fn from_row(row: &'row PgRow) -> Result<Self, sqlx::Error> {
        Ok(Self {
            id: row.try_get("id")?,
            user_id: row.try_get("user_id")?,
            status: row.try_get("status")?,
            source: row.try_get("source")?,
            reason: row.try_get("reason")?,
            ai_recommendation: row.try_get("ai_recommendation")?,
            admin_user_id: row.try_get("admin_user_id")?,
            admin_note: row.try_get("admin_note")?,
            created_at: row.try_get("created_at")?,
            reviewed_at: row.try_get("reviewed_at")?,
        })
    }
}

#[derive(Clone, Debug, Serialize)]
struct SelfRequestView<'a> {
    id: i64,
    status: &'a str,
    source: &'a str,
    reason: &'a str,
    ai_recommendation: &'a str,
    admin_note: &'a str,
    created_at: i64,
    reviewed_at: i64,
}

impl DeveloperAccessRequest {
    fn self_view(&self) -> SelfRequestView<'_> {
        SelfRequestView {
            id: self.id,
            status: &self.status,
            source: &self.source,
            reason: &self.reason,
            ai_recommendation: &self.ai_recommendation,
            admin_note: &self.admin_note,
            created_at: self.created_at,
            reviewed_at: self.reviewed_at,
        }
    }
}

#[derive(Clone, Debug, Serialize)]
struct AdminRequestView {
    id: i64,
    user_id: i64,
    status: String,
    source: String,
    reason: String,
    ai_recommendation: String,
    admin_user_id: i64,
    admin_note: String,
    created_at: i64,
    reviewed_at: i64,
    username: String,
    email: String,
}

impl<'row> FromRow<'row, PgRow> for AdminRequestView {
    fn from_row(row: &'row PgRow) -> Result<Self, sqlx::Error> {
        Ok(Self {
            id: row.try_get("id")?,
            user_id: row.try_get("user_id")?,
            status: row.try_get("status")?,
            source: row.try_get("source")?,
            reason: row.try_get("reason")?,
            ai_recommendation: row.try_get("ai_recommendation")?,
            admin_user_id: row.try_get("admin_user_id")?,
            admin_note: row.try_get("admin_note")?,
            created_at: row.try_get("created_at")?,
            reviewed_at: row.try_get("reviewed_at")?,
            username: row.try_get("username")?,
            email: row.try_get("email")?,
        })
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum TextValidationError {
    ReasonTooShort,
    RecommendationTooShort,
    ReviewNoteTooShort,
    TooLong,
}

#[derive(Debug)]
enum ReviewStoreError {
    Database(sqlx::Error),
    NotFound,
    AlreadyReviewed,
}

async fn get_self_request(
    State(state): State<DeveloperAccessState>,
    headers: HeaderMap,
) -> Response {
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let response = match latest_request(&state.pg, principal.user.id).await {
        Ok(Some(request)) => api_success(json!(request.self_view())),
        Ok(None) => api_success(Value::Null),
        Err(error) => api_error(error.to_string()),
    };
    with_auth_version(response)
}

async fn submit_self_request(
    State(state): State<DeveloperAccessState>,
    request: Request,
) -> Response {
    let principal = match authenticated_user(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let client_ip = client_ip(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return with_auth_version(response);
    }
    if principal.user.developer_access_granted {
        return with_auth_version(developer_error(
            StatusCode::CONFLICT,
            "DEVELOPER_ACCESS_ALREADY_ACTIVE",
            "developer access is already active",
        ));
    }
    let input = match parse_submission(request).await {
        Ok(input) => input,
        Err(response) => return with_auth_version(response),
    };
    if !input.confirmed {
        return with_auth_version(developer_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "DEVELOPER_ACCESS_CONFIRMATION_REQUIRED",
            "explicit confirmation of the AI recommendation is required",
        ));
    }
    let session = match state
        .auth
        .current_session(SecretString::from(principal.credential.clone()))
        .await
    {
        Ok(session)
            if session.user.id == principal.user.id && !session.session_id.trim().is_empty() =>
        {
            session
        }
        _ => {
            return with_auth_version(developer_error(
                StatusCode::FORBIDDEN,
                "DEVELOPER_ACCESS_SESSION_REQUIRED",
                "a browser login session is required",
            ));
        }
    };
    let payload = match state
        .auth
        .consume_assistant_l1_confirmation(
            principal.user.id,
            session.session_id.trim(),
            SecretString::from(input.confirmation_token),
        )
        .await
    {
        Ok(payload) => payload,
        Err(AssistantL1ConfirmationError::Invalid) => {
            return with_auth_version(developer_error(
                StatusCode::UNPROCESSABLE_ENTITY,
                "DEVELOPER_ACCESS_AI_CONFIRMATION_INVALID",
                "AI recommendation confirmation is invalid or expired; continue the conversation to prepare a new one",
            ));
        }
        Err(AssistantL1ConfirmationError::Internal) => {
            return with_auth_version(api_error(
                "developer access confirmation could not be loaded".to_owned(),
            ));
        }
    };
    let draft = serde_json::from_str::<RecommendationDraft>(&payload);
    let reason = input.reason.trim();
    let recommendation = input.ai_recommendation.trim();
    let Ok(draft) = draft else {
        return with_auth_version(confirmation_mismatch());
    };
    if reason != draft.user_statement || recommendation != draft.recommendation {
        return with_auth_version(confirmation_mismatch());
    }
    let (reason, recommendation) = match normalize_submission(reason, recommendation) {
        Ok(values) => values,
        Err(error) => return with_auth_version(submission_validation_response(error)),
    };
    let response =
        match submit_request(&state.pg, principal.user.id, &reason, &recommendation).await {
            Ok(request) => api_success(json!(request.self_view())),
            Err(error) => api_error(error.to_string()),
        };
    with_auth_version(response)
}

async fn list_admin_requests(
    State(state): State<DeveloperAccessState>,
    RawQuery(raw_query): RawQuery,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let status = query_value(raw_query.as_deref(), "status")
        .trim()
        .to_owned();
    if !status.is_empty()
        && !matches!(
            status.as_str(),
            STATUS_PENDING | STATUS_APPROVED | STATUS_REJECTED
        )
    {
        return with_auth_version(developer_error(
            StatusCode::BAD_REQUEST,
            "DEVELOPER_ACCESS_INVALID_STATUS",
            "invalid request status",
        ));
    }
    let response = match list_requests(&state.pg, &status).await {
        Ok(requests) => api_success(json!(requests)),
        Err(error) => api_error(error.to_string()),
    };
    with_auth_version(response)
}

async fn approve_request(
    State(state): State<DeveloperAccessState>,
    Path(request_id): Path<String>,
    request: Request,
) -> Response {
    review_request(state, request_id, true, request).await
}

async fn reject_request(
    State(state): State<DeveloperAccessState>,
    Path(request_id): Path<String>,
    request: Request,
) -> Response {
    review_request(state, request_id, false, request).await
}

async fn review_request(
    state: DeveloperAccessState,
    request_id_raw: String,
    approve: bool,
    request: Request,
) -> Response {
    let principal = match authenticated_admin(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let client_ip = client_ip(&request);
    let request_path = request.uri().path().to_owned();
    let auth_method = if state
        .auth
        .current_session(SecretString::from(principal.credential.clone()))
        .await
        .is_ok()
    {
        "session"
    } else {
        "access_token"
    };
    let (response, success) = review_authenticated(
        &state,
        &principal,
        &request_id_raw,
        approve,
        &client_ip,
        request,
    )
    .await;
    record_admin_audit(
        &state.pg,
        AdminReviewAudit {
            actor_id: principal.user.id,
            actor_username: &principal.user.username,
            actor_role: principal.user.role,
            auth_method,
            client_ip: &client_ip,
            route: if approve {
                "/api/developer-access/requests/:id/approve"
            } else {
                "/api/developer-access/requests/:id/reject"
            },
            path: &request_path,
            request_id: &request_id_raw,
            status: response.status(),
            success,
        },
    )
    .await;
    response
}

async fn review_authenticated(
    state: &DeveloperAccessState,
    principal: &Principal,
    request_id_raw: &str,
    approve: bool,
    client_ip: &str,
    request: Request,
) -> (Response, bool) {
    if let Some(response) = critical_rate_limit(state, client_ip).await {
        return (with_auth_version(response), false);
    }
    let request_id = match request_id_raw.parse::<i64>() {
        Ok(request_id) if request_id > 0 => request_id,
        _ => {
            return (
                with_auth_version(developer_error(
                    StatusCode::BAD_REQUEST,
                    "DEVELOPER_ACCESS_INVALID_ID",
                    "invalid unlock request id",
                )),
                false,
            );
        }
    };
    let input = match parse_review(request).await {
        Ok(input) => input,
        Err(response) => return (with_auth_version(response), false),
    };
    let note = match normalize_review_note(&input.note) {
        Ok(note) => note,
        Err(TextValidationError::ReviewNoteTooShort) => {
            return (
                with_auth_version(developer_error(
                    StatusCode::UNPROCESSABLE_ENTITY,
                    "DEVELOPER_ACCESS_REVIEW_NOTE_TOO_SHORT",
                    "管理员意见至少需要 2 个字符",
                )),
                false,
            );
        }
        Err(_) => {
            return (
                with_auth_version(developer_error(
                    StatusCode::UNPROCESSABLE_ENTITY,
                    "DEVELOPER_ACCESS_REVIEW_NOTE_TOO_LONG",
                    "解锁申请说明不能超过 2000 个字符",
                )),
                false,
            );
        }
    };
    let reviewed =
        review_request_transaction(&state.pg, principal.user.id, request_id, approve, &note).await;
    let reviewed = match reviewed {
        Ok(reviewed) => reviewed,
        Err(ReviewStoreError::NotFound) => {
            return (
                with_auth_version(developer_error(
                    StatusCode::NOT_FOUND,
                    "DEVELOPER_ACCESS_REQUEST_NOT_FOUND",
                    "解锁申请不存在",
                )),
                false,
            );
        }
        Err(ReviewStoreError::AlreadyReviewed) => {
            return (
                with_auth_version(developer_error(
                    StatusCode::CONFLICT,
                    "DEVELOPER_ACCESS_REQUEST_ALREADY_REVIEWED",
                    "解锁申请已经处理",
                )),
                false,
            );
        }
        Err(ReviewStoreError::Database(error)) => {
            return (with_auth_version(api_error(error.to_string())), false);
        }
    };
    if approve {
        evict_user_cache(&state.valkey, reviewed.user_id).await;
    }
    record_system_log(
        &state.pg,
        principal.user.id,
        if approve {
            format!("approved developer access request {request_id}")
        } else {
            format!("rejected developer access request {request_id}")
        },
    )
    .await;
    (with_auth_version(api_success(json!(reviewed))), true)
}

async fn authenticated_user(
    state: &DeveloperAccessState,
    headers: &HeaderMap,
) -> Result<Principal, Response> {
    let credential = crate::migration_routes::legacy_http::dashboard_credential(headers)
        .ok_or_else(|| dashboard_auth_error(headers, None))?;
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(credential.clone()))
        .await
        .map_err(|error| dashboard_auth_error(headers, Some(error.kind)))?;
    enforce_user_auth_view(&user).map_err(|error| user_auth_error(headers, error))?;
    Ok(Principal { user, credential })
}

async fn authenticated_admin(
    state: &DeveloperAccessState,
    headers: &HeaderMap,
) -> Result<Principal, Response> {
    let credential = crate::migration_routes::legacy_http::dashboard_credential(headers)
        .ok_or_else(|| dashboard_auth_error(headers, None))?;
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(credential.clone()))
        .await
        .map_err(|error| dashboard_auth_error(headers, Some(error.kind)))?;
    if !user.developer_access_granted {
        return Err(console_not_found());
    }
    enforce_user_auth_view(&user).map_err(|error| user_auth_error(headers, error))?;
    if user.role < ADMIN_ROLE {
        return Err(user_auth_error(
            headers,
            UserAuthPolicyError::InsufficientPrivilege,
        ));
    }
    Ok(Principal { user, credential })
}

fn client_ip(request: &Request) -> String {
    request
        .extensions()
        .get::<ClientIpKey>()
        .map_or_else(|| "unknown".to_owned(), |key| key.0.clone())
}

async fn critical_rate_limit(state: &DeveloperAccessState, client_ip: &str) -> Option<Response> {
    match state.auth.check_critical_rate_limit(client_ip).await {
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
    }
}

async fn parse_submission(request: Request) -> Result<SubmissionInput, Response> {
    let bytes = request_bytes(request).await.map_err(|_| {
        developer_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "DEVELOPER_ACCESS_INVALID_REQUEST",
            "invalid unlock request",
        )
    })?;
    parse_nullable_json(&bytes).map_err(|_| {
        developer_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "DEVELOPER_ACCESS_INVALID_REQUEST",
            "invalid unlock request",
        )
    })
}

async fn parse_review(request: Request) -> Result<ReviewInput, Response> {
    if request
        .headers()
        .get(header::CONTENT_LENGTH)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.trim().parse::<u64>().ok())
        .is_some_and(|length| length == 0)
    {
        return Ok(ReviewInput::default());
    }
    let bytes = request_bytes(request).await.map_err(|_| {
        developer_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "DEVELOPER_ACCESS_INVALID_REQUEST",
            "invalid review request",
        )
    })?;
    if bytes.is_empty() {
        return Ok(ReviewInput::default());
    }
    parse_nullable_json(&bytes).map_err(|_| {
        developer_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "DEVELOPER_ACCESS_INVALID_REQUEST",
            "invalid review request",
        )
    })
}

async fn request_bytes(request: Request) -> Result<Bytes, axum::Error> {
    to_bytes(request.into_body(), BODY_LIMIT_BYTES).await
}

fn parse_nullable_json<T>(bytes: &[u8]) -> Result<T, serde_json::Error>
where
    T: for<'de> Deserialize<'de> + Default,
{
    let value: Value = serde_json::from_slice(bytes)?;
    if value.is_null() {
        return Ok(T::default());
    }
    serde_json::from_value(value)
}

fn deserialize_nullable_string<'de, D>(deserializer: D) -> Result<String, D::Error>
where
    D: serde::Deserializer<'de>,
{
    Option::<String>::deserialize(deserializer).map(Option::unwrap_or_default)
}

fn deserialize_nullable_bool<'de, D>(deserializer: D) -> Result<bool, D::Error>
where
    D: serde::Deserializer<'de>,
{
    Option::<bool>::deserialize(deserializer).map(Option::unwrap_or_default)
}

fn normalize_submission(
    reason: &str,
    recommendation: &str,
) -> Result<(String, String), TextValidationError> {
    let reason = normalize_text(reason)?;
    if reason.chars().count() < MIN_REASON_CHARS {
        return Err(TextValidationError::ReasonTooShort);
    }
    let recommendation = normalize_text(recommendation)?;
    if recommendation.chars().count() < MIN_RECOMMENDATION_CHARS {
        return Err(TextValidationError::RecommendationTooShort);
    }
    Ok((
        redact_assistant_handoff_message(&reason),
        redact_assistant_handoff_message(&recommendation),
    ))
}

fn normalize_review_note(note: &str) -> Result<String, TextValidationError> {
    let note = normalize_text(note)?;
    if note.chars().count() < MIN_REVIEW_NOTE_CHARS {
        return Err(TextValidationError::ReviewNoteTooShort);
    }
    Ok(note)
}

fn normalize_text(value: &str) -> Result<String, TextValidationError> {
    let value = value.trim();
    if value.chars().count() > MAX_NOTE_CHARS {
        return Err(TextValidationError::TooLong);
    }
    Ok(value.to_owned())
}

fn submission_validation_response(error: TextValidationError) -> Response {
    match error {
        TextValidationError::ReasonTooShort => developer_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "DEVELOPER_ACCESS_REASON_TOO_SHORT",
            "解锁申请说明至少需要 5 个字符",
        ),
        TextValidationError::RecommendationTooShort => developer_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "DEVELOPER_ACCESS_RECOMMENDATION_TOO_SHORT",
            "AI 推荐信至少需要 20 个字符",
        ),
        TextValidationError::TooLong | TextValidationError::ReviewNoteTooShort => developer_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "DEVELOPER_ACCESS_REASON_TOO_LONG",
            "解锁申请说明不能超过 2000 个字符",
        ),
    }
}

fn confirmation_mismatch() -> Response {
    developer_error(
        StatusCode::UNPROCESSABLE_ENTITY,
        "DEVELOPER_ACCESS_AI_CONFIRMATION_MISMATCH",
        "AI recommendation does not match the confirmed draft",
    )
}

async fn latest_request(
    pg: &PgPool,
    user_id: i64,
) -> Result<Option<DeveloperAccessRequest>, sqlx::Error> {
    sqlx::query_as::<_, DeveloperAccessRequest>(&format!(
        "SELECT {REQUEST_COLUMNS} FROM developer_access_requests \
         WHERE user_id = $1 ORDER BY id DESC LIMIT 1"
    ))
    .bind(user_id)
    .fetch_optional(pg)
    .await
}

async fn submit_request(
    pg: &PgPool,
    user_id: i64,
    reason: &str,
    recommendation: &str,
) -> Result<DeveloperAccessRequest, sqlx::Error> {
    let mut transaction = pg.begin().await?;
    sqlx::query_scalar::<_, i64>(
        "SELECT id FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(user_id)
    .fetch_one(&mut *transaction)
    .await?;
    let pending = sqlx::query_as::<_, DeveloperAccessRequest>(&format!(
        "SELECT {REQUEST_COLUMNS} FROM developer_access_requests \
         WHERE user_id = $1 AND status = $2 ORDER BY id DESC LIMIT 1"
    ))
    .bind(user_id)
    .bind(STATUS_PENDING)
    .fetch_optional(&mut *transaction)
    .await?;
    let request = if let Some(pending) = pending {
        pending
    } else {
        sqlx::query_as::<_, DeveloperAccessRequest>(&format!(
            "INSERT INTO developer_access_requests \
             (user_id, status, source, reason, ai_recommendation, admin_user_id, \
              admin_note, created_at, reviewed_at) \
             VALUES ($1, $2, $3, $4, $5, 0, '', EXTRACT(EPOCH FROM NOW())::BIGINT, 0) \
             RETURNING {REQUEST_COLUMNS}"
        ))
        .bind(user_id)
        .bind(STATUS_PENDING)
        .bind(SOURCE_ASSISTANT)
        .bind(reason)
        .bind(recommendation)
        .fetch_one(&mut *transaction)
        .await?
    };
    transaction.commit().await?;
    Ok(request)
}

async fn list_requests(pg: &PgPool, status: &str) -> Result<Vec<AdminRequestView>, sqlx::Error> {
    let columns = "request.id, request.user_id, COALESCE(request.status, '') AS status, \
        COALESCE(request.source, 'legacy') AS source, COALESCE(request.reason, '') AS reason, \
        COALESCE(request.ai_recommendation, '') AS ai_recommendation, \
        COALESCE(request.admin_user_id, 0) AS admin_user_id, \
        COALESCE(request.admin_note, '') AS admin_note, \
        COALESCE(request.created_at, 0) AS created_at, \
        COALESCE(request.reviewed_at, 0) AS reviewed_at, \
        COALESCE(users.username, '') AS username, COALESCE(users.email, '') AS email";
    if status.is_empty() {
        sqlx::query_as::<_, AdminRequestView>(&format!(
            "SELECT {columns} FROM developer_access_requests AS request \
             JOIN users ON users.id = request.user_id ORDER BY request.id DESC LIMIT $1"
        ))
        .bind(REQUEST_LIMIT)
        .fetch_all(pg)
        .await
    } else {
        sqlx::query_as::<_, AdminRequestView>(&format!(
            "SELECT {columns} FROM developer_access_requests AS request \
             JOIN users ON users.id = request.user_id WHERE request.status = $1 \
             ORDER BY request.id DESC LIMIT $2"
        ))
        .bind(status)
        .bind(REQUEST_LIMIT)
        .fetch_all(pg)
        .await
    }
}

async fn review_request_transaction(
    pg: &PgPool,
    admin_user_id: i64,
    request_id: i64,
    approve: bool,
    note: &str,
) -> Result<DeveloperAccessRequest, ReviewStoreError> {
    let mut transaction = pg.begin().await.map_err(ReviewStoreError::Database)?;
    let mut request = locked_request(&mut transaction, request_id)
        .await
        .map_err(ReviewStoreError::Database)?
        .ok_or(ReviewStoreError::NotFound)?;
    if request.status != STATUS_PENDING {
        return Err(ReviewStoreError::AlreadyReviewed);
    }
    if approve {
        let affected = sqlx::query(
            "UPDATE users SET console_activated_at = EXTRACT(EPOCH FROM NOW())::BIGINT, \
             trust_level_override = NULL WHERE id = $1 AND deleted_at IS NULL",
        )
        .bind(request.user_id)
        .execute(&mut *transaction)
        .await
        .map_err(ReviewStoreError::Database)?
        .rows_affected();
        if affected == 0 {
            return Err(ReviewStoreError::NotFound);
        }
    }
    let status = if approve {
        STATUS_APPROVED
    } else {
        STATUS_REJECTED
    };
    let reviewed_at = sqlx::query_scalar::<_, i64>(
        "UPDATE developer_access_requests SET status = $2, admin_user_id = $3, \
         admin_note = $4, reviewed_at = EXTRACT(EPOCH FROM NOW())::BIGINT WHERE id = $1 \
         RETURNING reviewed_at",
    )
    .bind(request_id)
    .bind(status)
    .bind(admin_user_id)
    .bind(note)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ReviewStoreError::Database)?;
    if approve {
        let recommendation = request.ai_recommendation.trim();
        if !recommendation.is_empty() {
            // Keep the immutable recommendation snapshot atomic with approval,
            // so the user-management archive cannot lag behind the review.
            sqlx::query(
                "INSERT INTO developer_access_recommendation_archives \
                 (user_id, request_id, source, reason, recommendation, admin_user_id, \
                  admin_note, approved_at, created_at) \
                 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)",
            )
            .bind(request.user_id)
            .bind(request.id)
            .bind(&request.source)
            .bind(&request.reason)
            .bind(recommendation)
            .bind(admin_user_id)
            .bind(note)
            .bind(reviewed_at)
            .execute(&mut *transaction)
            .await
            .map_err(ReviewStoreError::Database)?;
        }
    }
    transaction
        .commit()
        .await
        .map_err(ReviewStoreError::Database)?;
    request.status = status.to_owned();
    request.admin_user_id = admin_user_id;
    request.admin_note = note.to_owned();
    request.reviewed_at = reviewed_at;
    Ok(request)
}

async fn locked_request(
    transaction: &mut Transaction<'_, Postgres>,
    request_id: i64,
) -> Result<Option<DeveloperAccessRequest>, sqlx::Error> {
    sqlx::query_as::<_, DeveloperAccessRequest>(&format!(
        "SELECT {REQUEST_COLUMNS} FROM developer_access_requests WHERE id = $1 FOR UPDATE"
    ))
    .bind(request_id)
    .fetch_optional(&mut **transaction)
    .await
}

async fn evict_user_cache(valkey: &redis::Client, user_id: i64) {
    let Ok(mut connection) = valkey.get_multiplexed_async_connection().await else {
        return;
    };
    let _: Result<(), _> = redis::cmd("DEL")
        .arg(format!("user:{user_id}"))
        .query_async(&mut connection)
        .await;
}

async fn record_system_log(pg: &PgPool, admin_user_id: i64, content: String) {
    let username = sqlx::query_scalar::<_, String>(
        "SELECT COALESCE(username, '') FROM users WHERE id = $1 AND deleted_at IS NULL",
    )
    .bind(admin_user_id)
    .fetch_optional(pg)
    .await
    .ok()
    .flatten()
    .unwrap_or_default();
    let _ = sqlx::query(
        "INSERT INTO logs (user_id, created_at, type, content, username, request_id) \
         VALUES ($1, EXTRACT(EPOCH FROM NOW())::BIGINT, 4, $2, $3, $4)",
    )
    .bind(admin_user_id)
    .bind(content)
    .bind(username)
    .bind(uuid::Uuid::new_v4().to_string())
    .execute(pg)
    .await;
}

struct AdminReviewAudit<'a> {
    actor_id: i64,
    actor_username: &'a str,
    actor_role: i64,
    auth_method: &'static str,
    client_ip: &'a str,
    route: &'static str,
    path: &'a str,
    request_id: &'a str,
    status: StatusCode,
    success: bool,
}

async fn record_admin_audit(pg: &PgPool, audit: AdminReviewAudit<'_>) {
    let other = json!({
        "op": {
            "action": "generic",
            "params": {
                "method": "POST",
                "route": audit.route,
            },
        },
        "admin_info": {
            "admin_id": audit.actor_id,
            "admin_username": audit.actor_username,
            "admin_role": audit.actor_role,
            "auth_method": audit.auth_method,
        },
        "audit_info": {
            "method": "POST",
            "route": audit.route,
            "path": audit.path,
            "status": audit.status.as_u16(),
            "success": audit.success,
            "params": {"id": audit.request_id},
        },
    });
    let _ = sqlx::query(
        "INSERT INTO logs (user_id, created_at, type, content, username, ip, other, request_id) \
         VALUES ($1, EXTRACT(EPOCH FROM NOW())::BIGINT, 3, $2, $3, $4, $5, $6)",
    )
    .bind(audit.actor_id)
    .bind(format!("POST {}", audit.route))
    .bind(audit.actor_username)
    .bind(audit.client_ip)
    .bind(other.to_string())
    .bind(uuid::Uuid::new_v4().to_string())
    .execute(pg)
    .await;
}

fn query_value(query: Option<&str>, key: &str) -> String {
    form_urlencoded::parse(query.unwrap_or_default().as_bytes())
        .find_map(|(candidate, value)| (candidate == key).then(|| value.into_owned()))
        .unwrap_or_default()
}

fn dashboard_auth_error(headers: &HeaderMap, kind: Option<AuthErrorKind>) -> Response {
    let (status, code, english) = match kind {
        Some(AuthErrorKind::TokenExpired) => (
            StatusCode::UNAUTHORIZED,
            "AUTH_TOKEN_EXPIRED",
            "Unauthorized, not logged in and no access token provided",
        ),
        Some(AuthErrorKind::SessionRevoked) => (
            StatusCode::UNAUTHORIZED,
            "AUTH_SESSION_REVOKED",
            "Unauthorized, not logged in and no access token provided",
        ),
        Some(AuthErrorKind::Internal) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            "AUTH_INTERNAL_ERROR",
            "Database error, please contact the administrator",
        ),
        Some(AuthErrorKind::UserDisabled) => (
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_DISABLED",
            "User has been banned",
        ),
        _ => (
            StatusCode::UNAUTHORIZED,
            "AUTH_UNAUTHORIZED",
            "Unauthorized, invalid access token",
        ),
    };
    let message = if accepts_chinese(headers) {
        match code {
            "AUTH_INTERNAL_ERROR" => "数据库出错，请联系管理员",
            "AUTH_TOKEN_EXPIRED" | "AUTH_SESSION_REVOKED" => {
                "无权进行此操作，未登录且未提供 access token"
            }
            "AUTH_USER_DISABLED" => "用户已被封禁",
            _ => "无权进行此操作，access token 无效",
        }
    } else {
        english
    };
    developer_error(status, code, message)
}

fn user_auth_error(headers: &HeaderMap, error: UserAuthPolicyError) -> Response {
    let code = match error {
        UserAuthPolicyError::UserDisabled => "AUTH_USER_DISABLED",
        UserAuthPolicyError::InsufficientPrivilege => "AUTH_INSUFFICIENT_PRIVILEGE",
        UserAuthPolicyError::InvalidUserInfo => "AUTH_USER_INVALID",
    };
    let status = StatusCode::from_u16(user_auth_status(error)).unwrap_or(StatusCode::UNAUTHORIZED);
    developer_error(
        status,
        code,
        user_auth_message(
            error,
            headers
                .get(header::ACCEPT_LANGUAGE)
                .and_then(|value| value.to_str().ok()),
        ),
    )
}

fn accepts_chinese(headers: &HeaderMap) -> bool {
    headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.to_ascii_lowercase().starts_with("zh"))
}

fn console_not_found() -> Response {
    (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response()
}

fn api_success(data: Value) -> Response {
    Json(json!({
        "success": true,
        "message": "",
        "data": data,
    }))
    .into_response()
}

fn api_error(message: String) -> Response {
    Json(json!({
        "success": false,
        "message": message,
    }))
    .into_response()
}

fn developer_error(status: StatusCode, code: &'static str, message: &'static str) -> Response {
    (
        status,
        Json(json!({
            "success": false,
            "code": code,
            "message": message,
        })),
    )
        .into_response()
}

fn with_auth_version(mut response: Response) -> Response {
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

#[cfg(test)]
mod tests {
    use axum::body::{Body, to_bytes};
    use serde_json::Value;

    use super::*;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    fn request_fixture() -> DeveloperAccessRequest {
        DeveloperAccessRequest {
            id: 9,
            user_id: 7,
            status: STATUS_PENDING.to_owned(),
            source: SOURCE_ASSISTANT.to_owned(),
            reason: "connect a coding client".to_owned(),
            ai_recommendation: "The described integration is specific and appropriate.".to_owned(),
            admin_user_id: 0,
            admin_note: String::new(),
            created_at: 1_725_000_000,
            reviewed_at: 0,
        }
    }

    #[test]
    fn submission_should_use_unicode_character_limits_and_redact_handoff_secrets() -> TestResult {
        let reason = "  测试申请说，password: hunter2  ";
        let recommendation =
            "AI recommends approval because key=sk-secret-token-123 is configured.";

        let (reason, recommendation) = normalize_submission(reason, recommendation)
            .map_err(|error| std::io::Error::other(format!("{error:?}")))?;

        assert_eq!(reason, "测试申请说，password: [REDACTED]");
        assert_eq!(
            recommendation,
            "AI recommends approval because key=[REDACTED_API_KEY] is configured."
        );
        Ok(())
    }

    #[test]
    fn submission_should_reject_short_reason_and_recommendation_independently() {
        assert_eq!(
            normalize_submission("四个字", &"推".repeat(MIN_RECOMMENDATION_CHARS)),
            Err(TextValidationError::ReasonTooShort)
        );
        assert_eq!(
            normalize_submission("valid reason", &"推".repeat(MIN_RECOMMENDATION_CHARS - 1)),
            Err(TextValidationError::RecommendationTooShort)
        );
    }

    #[test]
    fn pending_request_self_projection_should_hide_admin_and_user_ids() -> TestResult {
        let value = serde_json::to_value(request_fixture().self_view())?;

        assert_eq!(value["id"], 9);
        assert!(value.get("user_id").is_none());
        assert!(value.get("admin_user_id").is_none());
        assert_eq!(
            value["ai_recommendation"],
            "The described integration is specific and appropriate."
        );
        Ok(())
    }

    #[test]
    fn status_query_should_decode_and_preserve_the_first_value() {
        assert_eq!(
            query_value(Some("status=pending&status=rejected"), "status"),
            STATUS_PENDING
        );
        assert_eq!(
            query_value(Some("status=approved%20"), "status"),
            "approved "
        );
    }

    #[test]
    fn nullable_json_should_match_gin_zero_value_binding() -> TestResult {
        let input = parse_nullable_json::<SubmissionInput>(b"null")?;

        assert!(!input.confirmed);
        assert!(input.reason.is_empty());
        assert!(input.ai_recommendation.is_empty());
        Ok(())
    }

    #[tokio::test]
    async fn zero_content_length_review_should_ignore_the_body_like_gin() -> TestResult {
        let request = Request::builder()
            .header(header::CONTENT_LENGTH, "0")
            .body(Body::from("not-json"))?;

        let input = parse_review(request).await.map_err(|response| {
            std::io::Error::other(format!("unexpected status: {}", response.status()))
        })?;

        assert!(input.note.is_empty());
        Ok(())
    }

    #[tokio::test]
    async fn coded_error_should_preserve_status_and_legacy_json_contract() -> TestResult {
        let response = developer_error(
            StatusCode::CONFLICT,
            "DEVELOPER_ACCESS_ALREADY_ACTIVE",
            "developer access is already active",
        );
        assert_eq!(response.status(), StatusCode::CONFLICT);
        let bytes = to_bytes(response.into_body(), 1_024).await?;
        let body: Value = serde_json::from_slice(&bytes)?;

        assert_eq!(body["success"], false);
        assert_eq!(body["code"], "DEVELOPER_ACCESS_ALREADY_ACTIVE");
        assert_eq!(body["message"], "developer access is already active");
        Ok(())
    }
}
