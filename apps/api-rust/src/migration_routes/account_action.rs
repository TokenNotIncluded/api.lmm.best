//! Legacy-compatible account-action request routes.
//!
//! Assistant-originated disable proposals and disabled-account appeals share
//! the same durable approval queue. Only administrator review may change a
//! user's status; submission paths create or reuse pending rows only.

use std::sync::Arc;

use axum::{
    Json, Router,
    body::{Bytes, to_bytes},
    extract::{Path, RawQuery, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use bcrypt::verify;
use hmac::{Hmac, Mac};
use secrecy::{ExposeSecret, SecretString};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sha2::Sha256;
use sqlx::{FromRow, PgPool, Postgres, Row, Transaction, postgres::PgRow};

use super::assistant::redact_assistant_handoff_message;
use crate::{
    ClientIpKey,
    auth::{
        AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth, DashboardUserView,
        UserAuthPolicyError, dashboard_token_candidate, enforce_user_auth_view, user_auth_message,
        user_auth_status,
    },
    legacy_empty_response,
};

type HmacSha256 = Hmac<Sha256>;

const ADMIN_ROLE: i64 = 10;
const ROOT_ROLE: i64 = 100;
const STATUS_ENABLED: i64 = 1;
const STATUS_DISABLED: i64 = 2;
const TOKEN_STATUS_ENABLED: i64 = 1;
const TOKEN_STATUS_DISABLED: i64 = 2;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const BODY_LIMIT_BYTES: usize = 16 * 1024;
const REQUEST_LIMIT: i64 = 100;
const MIN_REASON_CHARS: usize = 5;
const MIN_REVIEW_NOTE_CHARS: usize = 2;
const MAX_TEXT_CHARS: usize = 2_000;
const STATUS_PENDING: &str = "pending";
const STATUS_APPROVED: &str = "approved";
const STATUS_REJECTED: &str = "rejected";
const KIND_DISABLE: &str = "disable";
const KIND_APPEAL: &str = "appeal";
const AUTH_FLOW_PURPOSE_DISABLE: &str = "assistant_account_disable";
const AUTH_FENCE_TTL_SECONDS: u64 = 120;

const REQUEST_COLUMNS: &str = "id, target_user_id, requested_by_user_id, \
    COALESCE(kind, '') AS kind, COALESCE(status, '') AS status, \
    COALESCE(reason, '') AS reason, COALESCE(admin_user_id, 0) AS admin_user_id, \
    COALESCE(admin_note, '') AS admin_note, COALESCE(created_at, 0) AS created_at, \
    COALESCE(reviewed_at, 0) AS reviewed_at";

/// PostgreSQL, Valkey, dashboard-auth, and session-secret dependencies.
#[derive(Clone)]
pub struct AccountActionState {
    pg: PgPool,
    valkey: redis::Client,
    auth: Arc<dyn DashboardAuth>,
    session_secret: SecretString,
}

impl AccountActionState {
    #[must_use]
    pub fn new(
        pg: PgPool,
        valkey: redis::Client,
        auth: Arc<dyn DashboardAuth>,
        session_secret: SecretString,
    ) -> Self {
        Self {
            pg,
            valkey,
            auth,
            session_secret,
        }
    }
}

pub fn router(state: AccountActionState) -> Router {
    Router::new()
        .route(
            "/api/user/account-action-requests/appeal",
            get(get_appeal).post(submit_appeal),
        )
        .route(
            "/api/user/account-action-requests",
            post(submit_disable_request),
        )
        .route("/api/account-action-requests", get(list_admin_requests))
        .route(
            "/api/account-action-requests/{id}/approve",
            post(approve_request),
        )
        .route(
            "/api/account-action-requests/{id}/reject",
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
struct DisableSubmissionInput {
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    reason: String,
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    confirmation_token: String,
    #[serde(default, deserialize_with = "deserialize_nullable_bool")]
    confirmed: bool,
    #[serde(default)]
    target_user_id: i64,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct AppealSubmissionInput {
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    username: String,
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    password: String,
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    reason: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct ReviewInput {
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    note: String,
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq)]
struct DisableDraft {
    target_user_id: i64,
    reason: String,
}

#[derive(Clone, Debug, Serialize)]
struct AccountActionRequest {
    id: i64,
    target_user_id: i64,
    requested_by_user_id: i64,
    kind: String,
    status: String,
    reason: String,
    admin_user_id: i64,
    admin_note: String,
    created_at: i64,
    reviewed_at: i64,
}

impl<'row> FromRow<'row, PgRow> for AccountActionRequest {
    fn from_row(row: &'row PgRow) -> Result<Self, sqlx::Error> {
        Ok(Self {
            id: row.try_get("id")?,
            target_user_id: row.try_get("target_user_id")?,
            requested_by_user_id: row.try_get("requested_by_user_id")?,
            kind: row.try_get("kind")?,
            status: row.try_get("status")?,
            reason: row.try_get("reason")?,
            admin_user_id: row.try_get("admin_user_id")?,
            admin_note: row.try_get("admin_note")?,
            created_at: row.try_get("created_at")?,
            reviewed_at: row.try_get("reviewed_at")?,
        })
    }
}

#[derive(Clone, Debug, Serialize)]
struct AdminRequestView {
    id: i64,
    target_user_id: i64,
    requested_by_user_id: i64,
    kind: String,
    status: String,
    reason: String,
    admin_user_id: i64,
    admin_note: String,
    created_at: i64,
    reviewed_at: i64,
    target_username: String,
    target_email: String,
    requested_by_username: String,
    requested_by_email: String,
}

impl<'row> FromRow<'row, PgRow> for AdminRequestView {
    fn from_row(row: &'row PgRow) -> Result<Self, sqlx::Error> {
        Ok(Self {
            id: row.try_get("id")?,
            target_user_id: row.try_get("target_user_id")?,
            requested_by_user_id: row.try_get("requested_by_user_id")?,
            kind: row.try_get("kind")?,
            status: row.try_get("status")?,
            reason: row.try_get("reason")?,
            admin_user_id: row.try_get("admin_user_id")?,
            admin_note: row.try_get("admin_note")?,
            created_at: row.try_get("created_at")?,
            reviewed_at: row.try_get("reviewed_at")?,
            target_username: row.try_get("target_username")?,
            target_email: row.try_get("target_email")?,
            requested_by_username: row.try_get("requested_by_username")?,
            requested_by_email: row.try_get("requested_by_email")?,
        })
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum TextValidationError {
    ReasonTooShort,
    ReviewNoteTooShort,
    TooLong,
}

#[derive(Debug)]
enum ReviewStoreError {
    Database(sqlx::Error),
    NotFound,
    AlreadyReviewed,
    RootProtected,
    TargetForbidden,
    StateConflict,
    InvalidKind,
}

async fn submit_disable_request(
    State(state): State<AccountActionState>,
    request: Request,
) -> Response {
    let headers = request.headers().clone();
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let client_ip = client_ip(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return with_auth_version(response);
    }
    if !dashboard_token_candidate(&principal.credential) {
        return with_auth_version(account_error(
            StatusCode::FORBIDDEN,
            "ACCOUNT_ACTION_SESSION_REQUIRED",
            "账号操作申请必须使用浏览器登录会话",
        ));
    }
    let input = match parse_json::<DisableSubmissionInput>(request).await {
        Ok(input) => input,
        Err(response) => return with_auth_version(response),
    };
    if !input.confirmed {
        return with_auth_version(account_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ACCOUNT_ACTION_CONFIRMATION_REQUIRED",
            "请先在页面中明确确认此账号操作申请",
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
            return with_auth_version(account_error(
                StatusCode::FORBIDDEN,
                "ACCOUNT_ACTION_SESSION_REQUIRED",
                "账号操作申请需要有效的浏览器登录会话",
            ));
        }
    };
    let payload = match consume_disable_confirmation(
        &state,
        principal.user.id,
        session.session_id.trim(),
        input.confirmation_token.trim(),
    )
    .await
    {
        Ok(payload) => payload,
        Err(ConfirmationError::Invalid) => {
            return with_auth_version(account_error(
                StatusCode::UNPROCESSABLE_ENTITY,
                "ACCOUNT_ACTION_CONFIRMATION_INVALID",
                "账号操作确认已失效，请重新与助手确认",
            ));
        }
        Err(ConfirmationError::Internal) => {
            return with_auth_version(api_error("账号操作确认无法加载".to_owned()));
        }
    };
    let draft = match serde_json::from_str::<DisableDraft>(&payload) {
        Ok(draft) => draft,
        Err(_) => {
            return with_auth_version(account_error(
                StatusCode::UNPROCESSABLE_ENTITY,
                "ACCOUNT_ACTION_CONFIRMATION_MISMATCH",
                "确认内容与助手生成的申请不一致",
            ));
        }
    };
    if input.reason.trim() != draft.reason
        || (input.target_user_id > 0 && input.target_user_id != draft.target_user_id)
    {
        return with_auth_version(account_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ACCOUNT_ACTION_CONFIRMATION_MISMATCH",
            "确认内容与助手生成的申请不一致",
        ));
    }
    let reason = match normalize_reason(&input.reason) {
        Ok(reason) => reason,
        Err(error) => return with_auth_version(text_validation_response(error)),
    };
    let target_user_id = if draft.target_user_id > 0 {
        draft.target_user_id
    } else {
        principal.user.id
    };
    let response = match submit_disable(&state.pg, principal.user.id, target_user_id, &reason).await
    {
        Ok(request) => api_success(json!(request)),
        Err(SubmitError::RootProtected) => with_auth_version(account_error(
            StatusCode::FORBIDDEN,
            "ACCOUNT_ACTION_ROOT_PROTECTED",
            "超级管理员账号不可被禁用",
        )),
        Err(SubmitError::TargetForbidden) => with_auth_version(account_error(
            StatusCode::FORBIDDEN,
            "ACCOUNT_ACTION_TARGET_FORBIDDEN",
            "无权操作该账号",
        )),
        Err(SubmitError::Validation(error)) => with_auth_version(text_validation_response(error)),
        Err(SubmitError::StateConflict) => with_auth_version(account_error(
            StatusCode::CONFLICT,
            "ACCOUNT_ACTION_STATE_CONFLICT",
            "当前账号状态不允许此申请",
        )),
        Err(SubmitError::Database(error)) => with_auth_version(api_error(error.to_string())),
    };
    with_auth_version(response)
}

async fn get_appeal(
    State(state): State<AccountActionState>,
    headers: HeaderMap,
) -> Response {
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let response = match latest_request(&state.pg, principal.user.id, KIND_APPEAL).await {
        Ok(request) => api_success(json!(request)),
        Err(error) => api_error(error.to_string()),
    };
    with_auth_version(response)
}

async fn submit_appeal(
    State(state): State<AccountActionState>,
    request: Request,
) -> Response {
    let headers = request.headers().clone();
    let client_ip = client_ip(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return with_auth_version(response);
    }
    let input = match parse_json::<AppealSubmissionInput>(request).await {
        Ok(input) => input,
        Err(response) => return with_auth_version(response),
    };
    let user_id = match resolve_appeal_user(&state, &headers, &input).await {
        Ok(user_id) => user_id,
        Err(AppealIdentityError::Invalid) => {
            return with_auth_version(account_error(
                StatusCode::UNAUTHORIZED,
                "ACCOUNT_APPEAL_IDENTITY_INVALID",
                "账号身份校验失败或暂不允许提交解封申请",
            ));
        }
        Err(AppealIdentityError::NotNeeded) => {
            return with_auth_version(account_error(
                StatusCode::CONFLICT,
                "ACCOUNT_APPEAL_NOT_NEEDED",
                "当前账号状态不允许此申请",
            ));
        }
        Err(AppealIdentityError::Database(error)) => {
            return with_auth_version(api_error(error.to_string()));
        }
    };
    let reason = match normalize_reason(&input.reason) {
        Ok(reason) => reason,
        Err(error) => return with_auth_version(text_validation_response(error)),
    };
    let response = match submit_appeal_request(&state.pg, user_id, &reason).await {
        Ok(request) => api_success(json!(request)),
        Err(SubmitError::Validation(error)) => with_auth_version(text_validation_response(error)),
        Err(SubmitError::StateConflict) => with_auth_version(account_error(
            StatusCode::CONFLICT,
            "ACCOUNT_APPEAL_NOT_NEEDED",
            "当前账号状态不允许此申请",
        )),
        Err(SubmitError::Database(error)) => with_auth_version(api_error(error.to_string())),
        Err(_) => with_auth_version(api_error("解封申请提交失败".to_owned())),
    };
    with_auth_version(response)
}

async fn list_admin_requests(
    State(state): State<AccountActionState>,
    RawQuery(raw_query): RawQuery,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let status = query_value(raw_query.as_deref(), "status");
    let kind = query_value(raw_query.as_deref(), "kind");
    if !status.is_empty()
        && !matches!(
            status.as_str(),
            STATUS_PENDING | STATUS_APPROVED | STATUS_REJECTED
        )
    {
        return with_auth_version(account_error(
            StatusCode::BAD_REQUEST,
            "ACCOUNT_ACTION_INVALID_STATUS",
            "账号操作申请状态无效",
        ));
    }
    if !kind.is_empty() && kind != KIND_DISABLE && kind != KIND_APPEAL {
        return with_auth_version(account_error(
            StatusCode::BAD_REQUEST,
            "ACCOUNT_ACTION_INVALID_KIND",
            "账号操作申请类型无效",
        ));
    }
    let response = match list_requests(&state.pg, &status, &kind).await {
        Ok(requests) => api_success(json!(requests)),
        Err(error) => api_error(error.to_string()),
    };
    with_auth_version(disable_cache(response))
}

async fn approve_request(
    State(state): State<AccountActionState>,
    Path(request_id): Path<String>,
    request: Request,
) -> Response {
    review_request(state, request_id, true, request).await
}

async fn reject_request(
    State(state): State<AccountActionState>,
    Path(request_id): Path<String>,
    request: Request,
) -> Response {
    review_request(state, request_id, false, request).await
}

async fn review_request(
    state: AccountActionState,
    request_id_raw: String,
    approve: bool,
    request: Request,
) -> Response {
    let principal = match authenticated_admin(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let client_ip = client_ip(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return with_auth_version(disable_cache(response));
    }
    let request_id = match request_id_raw.parse::<i64>() {
        Ok(request_id) if request_id > 0 => request_id,
        _ => {
            return with_auth_version(disable_cache(account_error(
                StatusCode::BAD_REQUEST,
                "ACCOUNT_ACTION_INVALID_ID",
                "账号操作申请编号无效",
            )));
        }
    };
    let input = match parse_review(request).await {
        Ok(input) => input,
        Err(response) => return with_auth_version(disable_cache(response)),
    };
    let note = if approve {
        input.note.trim().to_owned()
    } else {
        match normalize_review_note(&input.note) {
            Ok(note) => note,
            Err(TextValidationError::ReviewNoteTooShort) => {
                return with_auth_version(disable_cache(account_error(
                    StatusCode::UNPROCESSABLE_ENTITY,
                    "ACCOUNT_ACTION_REVIEW_NOTE_REQUIRED",
                    "拒绝申请时必须填写至少 2 个字符的管理员意见",
                )));
            }
            Err(TextValidationError::TooLong) => {
                return with_auth_version(disable_cache(account_error(
                    StatusCode::UNPROCESSABLE_ENTITY,
                    "ACCOUNT_ACTION_REVIEW_NOTE_TOO_LONG",
                    "管理员意见不能超过 2000 个字符",
                )));
            }
            Err(TextValidationError::ReasonTooShort) => unreachable!(),
        }
    };
    let response = match review_request_transaction(
        &state,
        principal.user.id,
        principal.user.role,
        request_id,
        approve,
        &note,
    )
    .await
    {
        Ok(reviewed) => api_success(json!(reviewed)),
        Err(ReviewStoreError::NotFound) => account_error(
            StatusCode::NOT_FOUND,
            "ACCOUNT_ACTION_REQUEST_NOT_FOUND",
            "账号操作申请不存在",
        ),
        Err(ReviewStoreError::AlreadyReviewed) => account_error(
            StatusCode::CONFLICT,
            "ACCOUNT_ACTION_REQUEST_ALREADY_REVIEWED",
            "账号操作申请已经处理",
        ),
        Err(ReviewStoreError::RootProtected) => account_error(
            StatusCode::FORBIDDEN,
            "ACCOUNT_ACTION_ROOT_PROTECTED",
            "超级管理员账号不可被禁用",
        ),
        Err(ReviewStoreError::TargetForbidden) => account_error(
            StatusCode::FORBIDDEN,
            "ACCOUNT_ACTION_TARGET_FORBIDDEN",
            "无权操作该账号",
        ),
        Err(ReviewStoreError::StateConflict) => account_error(
            StatusCode::CONFLICT,
            "ACCOUNT_ACTION_STATE_CONFLICT",
            "当前账号状态已发生变化，无法审核",
        ),
        Err(ReviewStoreError::InvalidKind) => account_error(
            StatusCode::BAD_REQUEST,
            "ACCOUNT_ACTION_INVALID_KIND",
            "账号操作申请类型无效",
        ),
        Err(ReviewStoreError::Database(error)) => api_error(error.to_string()),
    };
    with_auth_version(disable_cache(response))
}

#[derive(Debug)]
enum ConfirmationError {
    Invalid,
    Internal,
}

#[derive(Debug)]
enum AppealIdentityError {
    Invalid,
    NotNeeded,
    Database(sqlx::Error),
}

#[derive(Debug)]
enum SubmitError {
    RootProtected,
    TargetForbidden,
    StateConflict,
    Validation(TextValidationError),
    Database(sqlx::Error),
}

async fn consume_disable_confirmation(
    state: &AccountActionState,
    user_id: i64,
    session_id: &str,
    token: &str,
) -> Result<String, ConfirmationError> {
    if token.is_empty() {
        return Err(ConfirmationError::Invalid);
    }
    let token_hash = hash_auth_flow(&state.session_secret, token).map_err(|_| ConfirmationError::Internal)?;
    let payload = sqlx::query_scalar::<_, String>(
        "UPDATE auth_flows SET consumed_at = NOW() \
         WHERE token_hash = $1 AND purpose = $2 AND user_id = $3 AND session_id = $4 \
         AND consumed_at IS NULL AND expires_at > NOW() RETURNING COALESCE(payload, '')",
    )
    .bind(&token_hash)
    .bind(AUTH_FLOW_PURPOSE_DISABLE)
    .bind(user_id)
    .bind(session_id)
    .fetch_optional(&state.pg)
    .await
    .map_err(|_| ConfirmationError::Internal)?;
    payload.ok_or(ConfirmationError::Invalid)
}

fn hash_auth_flow(session_secret: &SecretString, token: &str) -> Result<String, ()> {
    let key = format!("auth-flow-v1:{}", session_secret.expose_secret());
    let mut mac = HmacSha256::new_from_slice(key.as_bytes()).map_err(|_| ())?;
    mac.update(token.as_bytes());
    Ok(hex::encode(mac.finalize().into_bytes()))
}

async fn resolve_appeal_user(
    state: &AccountActionState,
    headers: &HeaderMap,
    input: &AppealSubmissionInput,
) -> Result<i64, AppealIdentityError> {
    if let Ok(principal) = authenticated_user(state, headers).await {
        if dashboard_token_candidate(&principal.credential) {
            let session = state
                .auth
                .current_session(SecretString::from(principal.credential.clone()))
                .await
                .map_err(|_| AppealIdentityError::Invalid)?;
            if session.session_id.trim().is_empty() {
                return Err(AppealIdentityError::Invalid);
            }
            if principal.user.status != STATUS_DISABLED {
                return Err(AppealIdentityError::NotNeeded);
            }
            return Ok(principal.user.id);
        }
    }
    let username = input.username.trim();
    if username.is_empty() || input.password.is_empty() {
        return Err(AppealIdentityError::Invalid);
    }
    let row = sqlx::query("SELECT id, password, status FROM users WHERE deleted_at IS NULL AND (username = $1 OR email = $1) LIMIT 1")
        .bind(username)
        .fetch_optional(&state.pg)
        .await
        .map_err(AppealIdentityError::Database)?;
    let Some(row) = row else {
        return Err(AppealIdentityError::Invalid);
    };
    let user_id: i64 = row.try_get("id").map_err(AppealIdentityError::Database)?;
    let password_hash: String = row.try_get("password").map_err(AppealIdentityError::Database)?;
    let status: i64 = row.try_get("status").map_err(AppealIdentityError::Database)?;
    if status != STATUS_DISABLED {
        return Err(AppealIdentityError::Invalid);
    }
    let password = input.password.clone();
    let valid = match tokio::task::spawn_blocking(move || verify(&password, &password_hash)).await {
        Ok(value) => value.unwrap_or(false),
        Err(_) => return Err(AppealIdentityError::Invalid),
    };
    if !valid {
        return Err(AppealIdentityError::Invalid);
    }
    Ok(user_id)
}

async fn submit_disable(
    pg: &PgPool,
    requested_by: i64,
    target_user_id: i64,
    reason: &str,
) -> Result<AccountActionRequest, SubmitError> {
    if requested_by <= 0 || target_user_id <= 0 {
        return Err(SubmitError::TargetForbidden);
    }
    let mut transaction = pg.begin().await.map_err(SubmitError::Database)?;
    let requester_role: i64 = sqlx::query_scalar(
        "SELECT role FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(requested_by)
    .fetch_one(&mut *transaction)
    .await
    .map_err(SubmitError::Database)?;
    let target = sqlx::query_as::<_, TargetUser>(
        "SELECT id, role, status FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(target_user_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(SubmitError::Database)?
    .ok_or(SubmitError::TargetForbidden)?;
    if target.role == ROOT_ROLE {
        return Err(SubmitError::RootProtected);
    }
    if requester_role < ADMIN_ROLE && target_user_id != requested_by {
        return Err(SubmitError::TargetForbidden);
    }
    if requester_role < ROOT_ROLE && target.role >= ADMIN_ROLE {
        return Err(SubmitError::TargetForbidden);
    }
    if let Some(pending) = sqlx::query_as::<_, AccountActionRequest>(&format!(
        "SELECT {REQUEST_COLUMNS} FROM account_action_requests \
         WHERE target_user_id = $1 AND kind = $2 AND status = $3 \
         ORDER BY id DESC LIMIT 1 FOR UPDATE"
    ))
    .bind(target_user_id)
    .bind(KIND_DISABLE)
    .bind(STATUS_PENDING)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(SubmitError::Database)?
    {
        transaction.commit().await.map_err(SubmitError::Database)?;
        return Ok(pending);
    }
    let request = sqlx::query_as::<_, AccountActionRequest>(&format!(
        "INSERT INTO account_action_requests \
         (target_user_id, requested_by_user_id, kind, status, reason, admin_user_id, admin_note, created_at, reviewed_at) \
         VALUES ($1, $2, $3, $4, $5, 0, '', EXTRACT(EPOCH FROM NOW())::BIGINT, 0) \
         RETURNING {REQUEST_COLUMNS}"
    ))
    .bind(target_user_id)
    .bind(requested_by)
    .bind(KIND_DISABLE)
    .bind(STATUS_PENDING)
    .bind(reason)
    .fetch_one(&mut *transaction)
    .await
    .map_err(SubmitError::Database)?;
    transaction.commit().await.map_err(SubmitError::Database)?;
    Ok(request)
}

async fn submit_appeal_request(
    pg: &PgPool,
    user_id: i64,
    reason: &str,
) -> Result<AccountActionRequest, SubmitError> {
    let mut transaction = pg.begin().await.map_err(SubmitError::Database)?;
    let status: i64 = sqlx::query_scalar(
        "SELECT status FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(user_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(SubmitError::Database)?;
    if status != STATUS_DISABLED {
        return Err(SubmitError::StateConflict);
    }
    if let Some(pending) = sqlx::query_as::<_, AccountActionRequest>(&format!(
        "SELECT {REQUEST_COLUMNS} FROM account_action_requests \
         WHERE target_user_id = $1 AND kind = $2 AND status = $3 \
         ORDER BY id DESC LIMIT 1 FOR UPDATE"
    ))
    .bind(user_id)
    .bind(KIND_APPEAL)
    .bind(STATUS_PENDING)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(SubmitError::Database)?
    {
        transaction.commit().await.map_err(SubmitError::Database)?;
        return Ok(pending);
    }
    let request = sqlx::query_as::<_, AccountActionRequest>(&format!(
        "INSERT INTO account_action_requests \
         (target_user_id, requested_by_user_id, kind, status, reason, admin_user_id, admin_note, created_at, reviewed_at) \
         VALUES ($1, $1, $2, $3, $4, 0, '', EXTRACT(EPOCH FROM NOW())::BIGINT, 0) \
         RETURNING {REQUEST_COLUMNS}"
    ))
    .bind(user_id)
    .bind(KIND_APPEAL)
    .bind(STATUS_PENDING)
    .bind(reason)
    .fetch_one(&mut *transaction)
    .await
    .map_err(SubmitError::Database)?;
    transaction.commit().await.map_err(SubmitError::Database)?;
    Ok(request)
}

async fn latest_request(
    pg: &PgPool,
    user_id: i64,
    kind: &str,
) -> Result<Option<AccountActionRequest>, sqlx::Error> {
    sqlx::query_as::<_, AccountActionRequest>(&format!(
        "SELECT {REQUEST_COLUMNS} FROM account_action_requests \
         WHERE target_user_id = $1 AND kind = $2 ORDER BY id DESC LIMIT 1"
    ))
    .bind(user_id)
    .bind(kind)
    .fetch_optional(pg)
    .await
}

async fn list_requests(
    pg: &PgPool,
    status: &str,
    kind: &str,
) -> Result<Vec<AdminRequestView>, sqlx::Error> {
    let columns = "request.id, request.target_user_id, request.requested_by_user_id, \
        request.kind, request.status, request.reason, request.admin_user_id, request.admin_note, \
        request.created_at, request.reviewed_at, \
        COALESCE(target.username, '') AS target_username, COALESCE(target.email, '') AS target_email, \
        COALESCE(requester.username, '') AS requested_by_username, COALESCE(requester.email, '') AS requested_by_email";
    let mut query = format!(
        "SELECT {columns} FROM account_action_requests AS request \
         JOIN users AS target ON target.id = request.target_user_id AND target.deleted_at IS NULL \
         LEFT JOIN users AS requester ON requester.id = request.requested_by_user_id AND requester.deleted_at IS NULL \
         WHERE 1=1"
    );
    if !status.is_empty() {
        query.push_str(" AND request.status = $1");
    }
    if !kind.is_empty() {
        query.push_str(if status.is_empty() {
            " AND request.kind = $1"
        } else {
            " AND request.kind = $2"
        });
    }
    query.push_str(" ORDER BY request.id DESC LIMIT ");
    query.push_str(&REQUEST_LIMIT.to_string());
    match (status.is_empty(), kind.is_empty()) {
        (true, true) => {
            sqlx::query_as::<_, AdminRequestView>(&query).fetch_all(pg).await
        }
        (false, true) => {
            sqlx::query_as::<_, AdminRequestView>(&query)
                .bind(status)
                .fetch_all(pg)
                .await
        }
        (true, false) => {
            sqlx::query_as::<_, AdminRequestView>(&query)
                .bind(kind)
                .fetch_all(pg)
                .await
        }
        (false, false) => {
            sqlx::query_as::<_, AdminRequestView>(&query)
                .bind(status)
                .bind(kind)
                .fetch_all(pg)
                .await
        }
    }
}

#[derive(Clone, Debug)]
struct TargetUser {
    id: i64,
    role: i64,
    status: i64,
}

impl<'row> FromRow<'row, PgRow> for TargetUser {
    fn from_row(row: &'row PgRow) -> Result<Self, sqlx::Error> {
        Ok(Self {
            id: row.try_get("id")?,
            role: row.try_get("role")?,
            status: row.try_get("status")?,
        })
    }
}

async fn review_request_transaction(
    state: &AccountActionState,
    admin_user_id: i64,
    admin_role: i64,
    request_id: i64,
    approve: bool,
    note: &str,
) -> Result<AccountActionRequest, ReviewStoreError> {
    if admin_user_id <= 0 || request_id <= 0 || admin_role < ADMIN_ROLE {
        return Err(ReviewStoreError::TargetForbidden);
    }
    let mut transaction = state.pg.begin().await.map_err(ReviewStoreError::Database)?;
    let mut request = sqlx::query_as::<_, AccountActionRequest>(&format!(
        "SELECT {REQUEST_COLUMNS} FROM account_action_requests WHERE id = $1 FOR UPDATE"
    ))
    .bind(request_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ReviewStoreError::Database)?
    .ok_or(ReviewStoreError::NotFound)?;
    if request.status != STATUS_PENDING {
        return Err(ReviewStoreError::AlreadyReviewed);
    }
    if request.kind != KIND_DISABLE && request.kind != KIND_APPEAL {
        return Err(ReviewStoreError::InvalidKind);
    }
    let target = sqlx::query_as::<_, TargetUser>(
        "SELECT id, role, status FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(request.target_user_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(ReviewStoreError::Database)?
    .ok_or(ReviewStoreError::NotFound)?;
    if target.role == ROOT_ROLE {
        return Err(ReviewStoreError::RootProtected);
    }
    if admin_role < ROOT_ROLE && target.role >= ADMIN_ROLE {
        return Err(ReviewStoreError::TargetForbidden);
    }
    if !approve {
        update_review(&mut transaction, &mut request, admin_user_id, STATUS_REJECTED, note)
            .await?;
        transaction
            .commit()
            .await
            .map_err(ReviewStoreError::Database)?;
        return Ok(request);
    }
    let now = sqlx::query_scalar::<_, i64>("SELECT EXTRACT(EPOCH FROM NOW())::BIGINT")
        .fetch_one(&mut *transaction)
        .await
        .map_err(ReviewStoreError::Database)?;
    let reason = format!("account_action_{}", request.kind);
    revoke_active_sessions(&mut transaction, request.target_user_id, &reason, now)
        .await
        .map_err(ReviewStoreError::Database)?;
    sqlx::query(
        "UPDATE tokens SET status = $2 WHERE user_id = $1 AND status = $3",
    )
    .bind(request.target_user_id)
    .bind(TOKEN_STATUS_DISABLED)
    .bind(TOKEN_STATUS_ENABLED)
    .execute(&mut *transaction)
    .await
    .map_err(ReviewStoreError::Database)?;
    let next_auth_version: i64 = sqlx::query_scalar(
        "UPDATE users SET auth_version = GREATEST(COALESCE(auth_version, 0), 0) + 1 \
         WHERE id = $1 RETURNING auth_version",
    )
    .bind(request.target_user_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(ReviewStoreError::Database)?;
    let new_status = if request.kind == KIND_APPEAL {
        if target.status != STATUS_DISABLED {
            return Err(ReviewStoreError::StateConflict);
        }
        STATUS_ENABLED
    } else {
        STATUS_DISABLED
    };
    sqlx::query("UPDATE users SET status = $2 WHERE id = $1")
        .bind(request.target_user_id)
        .bind(new_status)
        .execute(&mut *transaction)
        .await
        .map_err(ReviewStoreError::Database)?;
    update_review(&mut transaction, &mut request, admin_user_id, STATUS_APPROVED, note).await?;
    transaction
        .commit()
        .await
        .map_err(ReviewStoreError::Database)?;
    if publish_auth_version(&state.valkey, request.target_user_id, next_auth_version)
        .await
        .is_err()
    {
        tracing::warn!(
            user_id = request.target_user_id,
            auth_version = next_auth_version,
            "account action review committed; auth cache publish failed"
        );
    }
    Ok(request)
}

async fn update_review(
    transaction: &mut Transaction<'_, Postgres>,
    request: &mut AccountActionRequest,
    admin_user_id: i64,
    status: &str,
    note: &str,
) -> Result<(), ReviewStoreError> {
    let reviewed_at = sqlx::query_scalar::<_, i64>(
        "UPDATE account_action_requests SET status = $2, admin_user_id = $3, admin_note = $4, \
         reviewed_at = EXTRACT(EPOCH FROM NOW())::BIGINT WHERE id = $1 RETURNING reviewed_at",
    )
    .bind(request.id)
    .bind(status)
    .bind(admin_user_id)
    .bind(note)
    .fetch_one(&mut **transaction)
    .await
    .map_err(ReviewStoreError::Database)?;
    request.status = status.to_owned();
    request.admin_user_id = admin_user_id;
    request.admin_note = note.to_owned();
    request.reviewed_at = reviewed_at;
    Ok(())
}

async fn revoke_active_sessions(
    transaction: &mut Transaction<'_, Postgres>,
    user_id: i64,
    reason: &str,
    now: i64,
) -> Result<(), sqlx::Error> {
    sqlx::query(
        "UPDATE user_sessions SET status = 'revoked', revoked_at = $3, revoked_reason = $2 \
         WHERE user_id = $1 AND status = 'active' AND expires_at > $3",
    )
    .bind(user_id)
    .bind(reason)
    .bind(now)
    .execute(&mut **transaction)
    .await
    .map(|_| ())
}

async fn publish_auth_version(
    valkey: &redis::Client,
    user_id: i64,
    version: i64,
) -> Result<(), redis::RedisError> {
    let mut connection = valkey.get_multiplexed_async_connection().await?;
    let _: () = redis::pipe()
        .atomic()
        .cmd("SET")
        .arg(format!("auth:user:version:{user_id}"))
        .arg(version)
        .cmd("DEL")
        .arg(format!("auth:user:fence:{user_id}"))
        .cmd("DEL")
        .arg(format!("user:{user_id}"))
        .query_async(&mut connection)
        .await?;
    Ok(())
}

async fn authenticated_user(
    state: &AccountActionState,
    headers: &HeaderMap,
) -> Result<Principal, Response> {
    let credential =
        dashboard_credential(headers).ok_or_else(|| dashboard_auth_error(headers, None))?;
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(credential.clone()))
        .await
        .map_err(|error| dashboard_auth_error(headers, Some(error.kind)))?;
    enforce_user_auth_view(&user).map_err(|error| user_auth_error(headers, error))?;
    Ok(Principal { user, credential })
}

async fn authenticated_admin(
    state: &AccountActionState,
    headers: &HeaderMap,
) -> Result<Principal, Response> {
    let principal = authenticated_user(state, headers).await?;
    if principal.user.role < ADMIN_ROLE {
        return Err(user_auth_error(
            headers,
            UserAuthPolicyError::InsufficientPrivilege,
        ));
    }
    Ok(principal)
}

fn dashboard_credential(headers: &HeaderMap) -> Option<String> {
    headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.strip_prefix("Bearer "))
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(str::to_owned)
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
        None => return console_not_found(),
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

fn user_auth_error(headers: &HeaderMap, error: UserAuthPolicyError) -> Response {
    let code = match error {
        UserAuthPolicyError::UserDisabled => "AUTH_USER_DISABLED",
        UserAuthPolicyError::InsufficientPrivilege => "AUTH_INSUFFICIENT_PRIVILEGE",
        UserAuthPolicyError::InvalidUserInfo => "AUTH_USER_INVALID",
    };
    let status = StatusCode::from_u16(user_auth_status(error)).unwrap_or(StatusCode::UNAUTHORIZED);
    (
        status,
        Json(json!({
            "success": false,
            "code": code,
            "message": user_auth_message(
                error,
                headers
                    .get(header::ACCEPT_LANGUAGE)
                    .and_then(|value| value.to_str().ok()),
            ),
        })),
    )
        .into_response()
}

fn client_ip(request: &Request) -> String {
    request
        .extensions()
        .get::<ClientIpKey>()
        .map_or_else(|| "unknown".to_owned(), |key| key.0.clone())
}

async fn critical_rate_limit(state: &AccountActionState, client_ip: &str) -> Option<Response> {
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

async fn parse_json<T>(request: Request) -> Result<T, Response>
where
    T: for<'de> Deserialize<'de> + Default,
{
    let bytes = request_bytes(request).await.map_err(|_| {
        account_error(
            StatusCode::BAD_REQUEST,
            "ACCOUNT_ACTION_INVALID_REQUEST",
            "账号操作申请格式无效",
        )
    })?;
    parse_nullable_json(&bytes).map_err(|_| {
        account_error(
            StatusCode::BAD_REQUEST,
            "ACCOUNT_ACTION_INVALID_REQUEST",
            "账号操作申请格式无效",
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
        account_error(
            StatusCode::BAD_REQUEST,
            "ACCOUNT_ACTION_INVALID_REVIEW",
            "管理员审核意见格式无效",
        )
    })?;
    if bytes.is_empty() {
        return Ok(ReviewInput::default());
    }
    parse_nullable_json(&bytes).map_err(|_| {
        account_error(
            StatusCode::BAD_REQUEST,
            "ACCOUNT_ACTION_INVALID_REVIEW",
            "管理员审核意见格式无效",
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

fn normalize_reason(value: &str) -> Result<String, TextValidationError> {
    let value = value.trim();
    if value.chars().count() > MAX_TEXT_CHARS {
        return Err(TextValidationError::TooLong);
    }
    if value.chars().count() < MIN_REASON_CHARS {
        return Err(TextValidationError::ReasonTooShort);
    }
    Ok(redact_assistant_handoff_message(value))
}

fn normalize_review_note(value: &str) -> Result<String, TextValidationError> {
    let value = value.trim();
    if value.chars().count() > MAX_TEXT_CHARS {
        return Err(TextValidationError::TooLong);
    }
    if value.chars().count() < MIN_REVIEW_NOTE_CHARS {
        return Err(TextValidationError::ReviewNoteTooShort);
    }
    Ok(value.to_owned())
}

fn text_validation_response(error: TextValidationError) -> Response {
    match error {
        TextValidationError::ReasonTooShort => account_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ACCOUNT_ACTION_REASON_TOO_SHORT",
            "申请说明至少需要 5 个字符",
        ),
        TextValidationError::TooLong => account_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ACCOUNT_ACTION_REASON_TOO_LONG",
            "申请说明不能超过 2000 个字符",
        ),
        TextValidationError::ReviewNoteTooShort => account_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ACCOUNT_ACTION_REVIEW_NOTE_REQUIRED",
            "拒绝申请时必须填写至少 2 个字符的管理员意见",
        ),
    }
}

fn query_value(raw_query: Option<&str>, key: &str) -> String {
    raw_query
        .and_then(|query| {
            form_urlencoded::parse(query.as_bytes())
                .find(|(candidate, _)| candidate == key)
                .map(|(_, value)| value.into_owned())
        })
        .unwrap_or_default()
        .trim()
        .to_owned()
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

fn account_error(status: StatusCode, code: &'static str, message: &'static str) -> Response {
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

fn disable_cache(mut response: Response) -> Response {
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        HeaderValue::from_static("no-store"),
    );
    response
}
