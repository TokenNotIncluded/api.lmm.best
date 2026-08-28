//! Advanced-security administrator extensions and violation-fee appeal routes.

use std::{
    collections::HashMap,
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{Path, RawQuery, Request, State},
    http::{HeaderMap, HeaderName, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get as route_get, post as route_post, put as route_put},
};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{PgPool, Row, postgres::PgRow};

use crate::{
    ClientIpKey,
    auth::{
        AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth, DashboardUserView,
        UserAuthPolicyError, enforce_user_auth_view, user_auth_message, user_auth_status,
    },
    legacy_empty_response,
};

const ADMIN_ROLE: i64 = 10;
const ROOT_ROLE: i64 = 100;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const MAX_BODY_BYTES: usize = 64 * 1024;
const DEFAULT_PAGE_SIZE: i64 = 10;
const MAX_PAGE_SIZE: i64 = 100;
const DEFAULT_REVIEW_HISTORY_KEEP: i64 = 30;
const MAX_REVIEW_HISTORY_KEEP: i64 = 100;
const MAX_REVIEW_HISTORY_EXPECTED_COUNT: i64 = 100_000;
const REVIEW_RUN_CLEANUP_SCOPE: &str = "security.review_runs.delete";
const STALE_REVIEW_CLEANUP_MESSAGE: &str = "cleanup preview is stale; refresh and confirm again";
const SECURITY_PROOF_HEADER: &str = "x-security-proof";

#[derive(Clone)]
pub struct SecurityAdminState {
    backend: Arc<dyn SecurityAdminBackend>,
    auth: Arc<dyn DashboardAuth>,
}

impl SecurityAdminState {
    #[must_use]
    pub fn new(pg: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self::with_backend(Arc::new(PgSecurityAdminBackend { pg }), auth)
    }

    #[must_use]
    pub fn with_backend(
        backend: Arc<dyn SecurityAdminBackend>,
        auth: Arc<dyn DashboardAuth>,
    ) -> Self {
        Self { backend, auth }
    }
}

pub fn router(state: SecurityAdminState) -> Router {
    Router::new()
        .route("/api/security/admin/settings", route_put(update_settings))
        .route("/api/security/admin/ai-reviews", route_get(list_ai_reviews))
        .route(
            "/api/security/admin/review-runs/cleanup-preview",
            route_get(preview_review_run_cleanup),
        )
        .route(
            "/api/security/admin/review-runs",
            route_get(list_review_runs).delete(delete_review_runs),
        )
        .route(
            "/api/security/admin/review-runs/{task_id}",
            route_get(get_review_run),
        )
        .route(
            "/api/security/admin/violation-fee-appeals",
            route_get(list_admin_appeals),
        )
        .route(
            "/api/security/admin/violation-fee-appeals/{id}/{action}",
            route_post(review_admin_appeal),
        )
        .route("/api/user/violation-fee-appeals", route_post(submit_appeal))
        .route(
            "/api/user/violation-fees",
            route_get(list_self_violation_fees),
        )
        .with_state(state)
}

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
#[error("{0}")]
pub struct SecurityAdminError(pub String);

#[async_trait]
pub trait SecurityAdminBackend: Send + Sync {
    async fn update_settings(
        &self,
        enabled: bool,
        on_prompt: bool,
        action: &str,
        rules: &str,
    ) -> Result<(), SecurityAdminError>;

    async fn list_ai_reviews(
        &self,
        filter: &SecurityReviewFilter,
        limit: i64,
        offset: i64,
        actor_role: i64,
        actor_id: i64,
    ) -> Result<(Vec<AiReviewRow>, i64, bool), SecurityAdminError>;

    async fn list_review_runs(
        &self,
        limit: i64,
    ) -> Result<Vec<SystemTaskSummary>, SecurityAdminError>;

    async fn get_review_run(
        &self,
        task_id: &str,
    ) -> Result<Option<SystemTaskDetail>, SecurityAdminError>;

    async fn preview_review_run_cleanup(&self, keep: i64) -> Result<i64, SecurityAdminError>;

    async fn delete_review_runs(
        &self,
        keep: i64,
        expected_count: i64,
        admin_user_id: i64,
    ) -> Result<i64, SecurityAdminError>;

    async fn list_admin_appeals(
        &self,
        status: &str,
        limit: i64,
    ) -> Result<Vec<ViolationFeeAppeal>, SecurityAdminError>;

    async fn review_appeal(
        &self,
        admin_id: i64,
        appeal_id: i64,
        approve: bool,
        note: &str,
    ) -> Result<ViolationFeeAppeal, SecurityAdminError>;

    async fn submit_appeal(
        &self,
        user_id: i64,
        record_id: i64,
        reason: &str,
    ) -> Result<ViolationFeeAppeal, SecurityAdminError>;

    async fn list_user_violation_fees(
        &self,
        user_id: i64,
        limit: i64,
    ) -> Result<Vec<ViolationFeeRecord>, SecurityAdminError>;
}

#[derive(Clone, Debug, Default)]
struct SecurityReviewFilter {
    start_timestamp: i64,
    end_timestamp: i64,
    user_id: i64,
    category: String,
    group: String,
    decision: String,
    violations_only: bool,
    clear_only: bool,
}

#[derive(Clone, Debug, Serialize)]
struct AiReviewRow {
    id: i64,
    created_at: i64,
    request_id: String,
    user_id: i64,
    group: String,
    review_model: String,
    intensity: String,
    status: String,
    violation: bool,
    abuse: bool,
    rules: Value,
    #[serde(skip_serializing_if = "String::is_empty")]
    explanation: String,
}

#[derive(Clone, Debug, Serialize)]
struct SystemTaskSummary {
    id: i64,
    task_id: String,
    r#type: String,
    status: String,
    active_key: Option<String>,
    state: Value,
    error: String,
    locked_by: String,
    created_at: i64,
    updated_at: i64,
}

#[derive(Clone, Debug, Serialize)]
struct SystemTaskDetail {
    id: i64,
    task_id: String,
    r#type: String,
    status: String,
    active_key: Option<String>,
    payload: Value,
    state: Value,
    result: Value,
    error: String,
    locked_by: String,
    created_at: i64,
    updated_at: i64,
}

#[derive(Clone, Debug, Serialize)]
struct ViolationFeeAppeal {
    id: i64,
    record_id: i64,
    user_id: i64,
    reason: String,
    status: String,
    admin_user_id: i64,
    admin_note: String,
    created_at: i64,
    reviewed_at: i64,
}

#[derive(Clone, Debug, Serialize)]
struct ViolationFeeRecord {
    id: i64,
    user_id: i64,
    policy_key: String,
    error_code: String,
    charged_quota: i64,
    status: String,
    created_at: i64,
    reversed_at: i64,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct SettingsUpdate {
    enabled: Option<bool>,
    on_prompt: Option<bool>,
    #[serde(default)]
    action: String,
    rules: Value,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct AppealInput {
    record_id: i64,
    #[serde(default)]
    reason: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct AppealReviewInput {
    #[serde(default)]
    note: String,
}

#[derive(Clone)]
struct PgSecurityAdminBackend {
    pg: PgPool,
}

async fn update_settings(
    State(state): State<SecurityAdminState>,
    headers: HeaderMap,
    request: axum::extract::Request,
) -> Response {
    if let Err(response) = authenticated_root(&state, &headers).await {
        return response;
    }
    let body = match to_bytes(request.into_body(), MAX_BODY_BYTES).await {
        Ok(bytes) => bytes,
        Err(_) => {
            return with_auth_version(api_error("invalid advanced security settings payload"));
        }
    };
    let input: SettingsUpdate = match serde_json::from_slice(&body) {
        Ok(input) => input,
        Err(_) => {
            return with_auth_version(api_error("invalid advanced security settings payload"));
        }
    };
    let Some(enabled) = input.enabled else {
        return with_auth_version(api_error(
            "enabled, on_prompt, action, and rules are required",
        ));
    };
    let Some(on_prompt) = input.on_prompt else {
        return with_auth_version(api_error(
            "enabled, on_prompt, action, and rules are required",
        ));
    };
    let action = input.action.trim().to_ascii_lowercase();
    if action.is_empty() || !matches!(action.as_str(), "block" | "audit") {
        return with_auth_version(api_error(
            "enabled, on_prompt, action, and rules are required",
        ));
    }
    let rules = serde_json::to_string(&input.rules).unwrap_or_default();
    let trimmed = rules.trim();
    if trimmed.is_empty() || (!trimmed.starts_with('{') && !trimmed.starts_with('[')) {
        return with_auth_version(api_error(
            "enabled, on_prompt, action, and rules are required",
        ));
    }
    let response = match state
        .backend
        .update_settings(enabled, on_prompt, &action, trimmed)
        .await
    {
        Ok(()) => api_success(Value::Null),
        Err(error) => api_error(&error.0),
    };
    with_auth_version(response)
}

async fn list_ai_reviews(
    State(state): State<SecurityAdminState>,
    headers: HeaderMap,
    RawQuery(raw): RawQuery,
) -> Response {
    let actor = match authenticated_admin(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let query = parse_query(raw.as_deref());
    let page = page_query(&query);
    let filter = parse_review_filter(&query);
    let response = match state
        .backend
        .list_ai_reviews(&filter, page.page_size, page.offset(), actor.role, actor.id)
        .await
    {
        Ok((items, total, available)) => api_success(json!({
            "items": items,
            "total": total,
            "page": page.page,
            "page_size": page.page_size,
            "available": available,
        })),
        Err(error) => api_error(&error.0),
    };
    with_auth_version(response)
}

async fn list_review_runs(
    State(state): State<SecurityAdminState>,
    headers: HeaderMap,
    RawQuery(raw): RawQuery,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let query = parse_query(raw.as_deref());
    let limit = query
        .get("limit")
        .and_then(|v| v.parse::<i64>().ok())
        .unwrap_or(20)
        .clamp(1, 100);
    let response = match state.backend.list_review_runs(limit).await {
        Ok(items) => api_success(json!(items)),
        Err(error) => api_error(&error.0),
    };
    with_auth_version(response)
}

#[derive(Clone, Debug, Serialize)]
struct ReviewRunCleanupResponse {
    task_type: &'static str,
    keep: i64,
    eligible_count: i64,
    deleted_count: i64,
}

async fn preview_review_run_cleanup(
    State(state): State<SecurityAdminState>,
    headers: HeaderMap,
    RawQuery(raw): RawQuery,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let keep = match parse_review_history_keep(raw.as_deref()) {
        Ok(keep) => keep,
        Err(response) => return with_auth_version(no_store(response)),
    };
    let response = match state.backend.preview_review_run_cleanup(keep).await {
        Ok(eligible_count) => api_success(json!(ReviewRunCleanupResponse {
            task_type: "assistant_review",
            keep,
            eligible_count,
            deleted_count: 0,
        })),
        Err(error) => api_error(&error.0),
    };
    with_auth_version(no_store(response))
}

async fn delete_review_runs(State(state): State<SecurityAdminState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let admin = match authenticated_admin(&state, &headers).await {
        Ok(admin) => admin,
        Err(response) => return response,
    };
    if let Some(response) = critical_rate_limit(&state, client_ip(&request)).await {
        return with_auth_version(no_store(response));
    }
    if let Err(response) = require_cleanup_security_proof(&state, &headers, admin.id).await {
        return with_auth_version(no_store(response));
    }
    let keep = match parse_review_history_keep(request.uri().query()) {
        Ok(keep) => keep,
        Err(response) => return with_auth_version(no_store(response)),
    };
    let expected_count = match parse_review_history_expected_count(request.uri().query()) {
        Ok(expected_count) => expected_count,
        Err(response) => return with_auth_version(no_store(response)),
    };
    let response = match state
        .backend
        .delete_review_runs(keep, expected_count, admin.id)
        .await
    {
        Ok(deleted_count) => api_success(json!(ReviewRunCleanupResponse {
            task_type: "assistant_review",
            keep,
            eligible_count: deleted_count,
            deleted_count,
        })),
        Err(error) if error.0 == STALE_REVIEW_CLEANUP_MESSAGE => stale_cleanup(),
        Err(error) => api_error(&error.0),
    };
    with_auth_version(no_store(response))
}

async fn get_review_run(
    State(state): State<SecurityAdminState>,
    headers: HeaderMap,
    Path(task_id): Path<String>,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    if task_id.trim().is_empty() {
        return with_auth_version(api_error("task id is required"));
    }
    let response = match state.backend.get_review_run(task_id.trim()).await {
        Ok(Some(task)) => api_success(json!(task)),
        Ok(None) => legacy_json(
            StatusCode::NOT_FOUND,
            json!({"success": false, "message": "task not found"}),
        ),
        Err(error) => api_error(&error.0),
    };
    with_auth_version(response)
}

async fn list_admin_appeals(
    State(state): State<SecurityAdminState>,
    headers: HeaderMap,
    RawQuery(raw): RawQuery,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let query = parse_query(raw.as_deref());
    let status = query.get("status").map_or("", |v| v.trim());
    let response = match state.backend.list_admin_appeals(status, 200).await {
        Ok(items) => api_success(json!(items)),
        Err(error) => api_error(&error.0),
    };
    with_auth_version(response)
}

async fn review_admin_appeal(
    State(state): State<SecurityAdminState>,
    headers: HeaderMap,
    Path((id, action)): Path<(String, String)>,
    request: axum::extract::Request,
) -> Response {
    let actor = match authenticated_admin(&state, &headers).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    let appeal_id = match id.parse::<i64>() {
        Ok(id) if id > 0 => id,
        _ => {
            return with_auth_version(legacy_json(
                StatusCode::BAD_REQUEST,
                json!({"success": false, "code": "VIOLATION_FEE_APPEAL_INVALID_ID", "message": "申诉编号无效"}),
            ));
        }
    };
    let action = action.trim().to_ascii_lowercase();
    if action != "approve" && action != "reject" {
        return with_auth_version(legacy_json(
            StatusCode::BAD_REQUEST,
            json!({"success": false, "code": "VIOLATION_FEE_APPEAL_INVALID_ACTION", "message": "审核动作无效"}),
        ));
    }
    let body = to_bytes(request.into_body(), MAX_BODY_BYTES)
        .await
        .unwrap_or_default();
    let note = if body.is_empty() {
        String::new()
    } else {
        serde_json::from_slice::<AppealReviewInput>(&body)
            .map(|input| input.note)
            .unwrap_or_default()
    };
    let response = match state
        .backend
        .review_appeal(actor.id, appeal_id, action == "approve", note.trim())
        .await
    {
        Ok(appeal) => api_success(json!(appeal)),
        Err(error) => api_error(&error.0),
    };
    with_auth_version(response)
}

async fn submit_appeal(
    State(state): State<SecurityAdminState>,
    headers: HeaderMap,
    request: axum::extract::Request,
) -> Response {
    let user = match authenticated_user(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return response,
    };
    let body = match to_bytes(request.into_body(), MAX_BODY_BYTES).await {
        Ok(bytes) => bytes,
        Err(_) => {
            return with_auth_version(legacy_json(
                StatusCode::BAD_REQUEST,
                json!({"success": false, "code": "VIOLATION_FEE_APPEAL_INVALID", "message": "申诉格式无效"}),
            ));
        }
    };
    let input: AppealInput = match serde_json::from_slice(&body) {
        Ok(input) => input,
        Err(_) => {
            return with_auth_version(legacy_json(
                StatusCode::BAD_REQUEST,
                json!({"success": false, "code": "VIOLATION_FEE_APPEAL_INVALID", "message": "申诉格式无效"}),
            ));
        }
    };
    let response = match state
        .backend
        .submit_appeal(user.id, input.record_id, input.reason.trim())
        .await
    {
        Ok(appeal) => api_success(json!(appeal)),
        Err(error) => {
            let status = if error.0.contains("不存在") || error.0.contains("pending") {
                StatusCode::CONFLICT
            } else {
                StatusCode::BAD_REQUEST
            };
            legacy_json(
                status,
                json!({"success": false, "code": "VIOLATION_FEE_APPEAL_REJECTED", "message": error.0}),
            )
        }
    };
    with_auth_version(response)
}

async fn list_self_violation_fees(
    State(state): State<SecurityAdminState>,
    headers: HeaderMap,
) -> Response {
    let user = match authenticated_user(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return response,
    };
    let response = match state.backend.list_user_violation_fees(user.id, 100).await {
        Ok(records) => api_success(json!(records)),
        Err(error) => api_error(&error.0),
    };
    with_auth_version(response)
}

#[async_trait]
impl SecurityAdminBackend for PgSecurityAdminBackend {
    async fn update_settings(
        &self,
        enabled: bool,
        on_prompt: bool,
        action: &str,
        rules: &str,
    ) -> Result<(), SecurityAdminError> {
        let mut tx = self.pg.begin().await.map_err(db_error)?;
        let pairs = [
            ("AdvancedSecurityEnabled", enabled.to_string()),
            ("AdvancedSecurityOnPromptEnabled", on_prompt.to_string()),
            ("AdvancedSecurityAction", action.to_owned()),
            ("AdvancedSecurityRules", rules.to_owned()),
        ];
        for (key, value) in pairs {
            sqlx::query(
                "INSERT INTO options (key, value) VALUES ($1, $2) \
                 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value",
            )
            .bind(key)
            .bind(value)
            .execute(&mut *tx)
            .await
            .map_err(db_error)?;
        }
        tx.commit().await.map_err(db_error)?;
        Ok(())
    }

    async fn list_ai_reviews(
        &self,
        filter: &SecurityReviewFilter,
        limit: i64,
        offset: i64,
        actor_role: i64,
        actor_id: i64,
    ) -> Result<(Vec<AiReviewRow>, i64, bool), SecurityAdminError> {
        let available = table_exists(&self.pg, "assistant_request_reviews").await?;
        if !available {
            return Ok((Vec::new(), 0, false));
        }
        let mut sql = String::from(
            "SELECT id, created_at, request_id, user_id, \"group\", review_model, intensity, status, \
             violation, abuse, rules_json, explanation FROM assistant_request_reviews WHERE TRUE",
        );
        let mut bind = 1;
        if filter.start_timestamp > 0 {
            sql.push_str(&format!(" AND created_at >= ${bind}"));
            bind += 1;
        }
        if filter.end_timestamp > 0 {
            sql.push_str(&format!(" AND created_at <= ${bind}"));
            bind += 1;
        }
        if filter.user_id > 0 {
            sql.push_str(&format!(" AND user_id = ${bind}"));
            bind += 1;
        }
        if !filter.category.is_empty() && filter.category != "assistant_review" {
            sql.push_str(" AND FALSE");
        }
        if !filter.group.is_empty() {
            sql.push_str(&format!(" AND \"group\" = ${bind}"));
            bind += 1;
        }
        if !filter.decision.is_empty()
            && filter.decision != "violation"
            && filter.decision != "clear"
        {
            sql.push_str(" AND FALSE");
        }
        if filter.violations_only || filter.decision == "violation" {
            sql.push_str(" AND violation = TRUE");
        }
        if filter.clear_only || filter.decision == "clear" {
            sql.push_str(" AND status = 'completed' AND violation = FALSE");
        }
        let count_sql = sql.replace(
            "SELECT id, created_at, request_id, user_id, \"group\", review_model, intensity, status, \
             violation, abuse, rules_json, explanation",
            "SELECT COUNT(*)::BIGINT",
        );
        let mut count_query = sqlx::query_scalar::<_, i64>(&count_sql);
        if filter.start_timestamp > 0 {
            count_query = count_query.bind(filter.start_timestamp);
        }
        if filter.end_timestamp > 0 {
            count_query = count_query.bind(filter.end_timestamp);
        }
        if filter.user_id > 0 {
            count_query = count_query.bind(filter.user_id);
        }
        if !filter.group.is_empty() {
            count_query = count_query.bind(&filter.group);
        }
        let total = count_query.fetch_one(&self.pg).await.map_err(db_error)?;
        sql.push_str(&format!(
            " ORDER BY created_at DESC, id DESC LIMIT ${bind} OFFSET ${}",
            bind + 1
        ));
        let mut query = sqlx::query(&sql);
        if filter.start_timestamp > 0 {
            query = query.bind(filter.start_timestamp);
        }
        if filter.end_timestamp > 0 {
            query = query.bind(filter.end_timestamp);
        }
        if filter.user_id > 0 {
            query = query.bind(filter.user_id);
        }
        if !filter.group.is_empty() {
            query = query.bind(&filter.group);
        }
        query = query.bind(limit).bind(offset);
        let rows = query.fetch_all(&self.pg).await.map_err(db_error)?;
        let mut items = Vec::with_capacity(rows.len());
        for row in rows {
            let target_user: i64 = row_get(&row, "user_id")?;
            let mut item = AiReviewRow {
                id: row_get(&row, "id")?,
                created_at: row_get(&row, "created_at")?,
                request_id: row_get(&row, "request_id")?,
                user_id: target_user,
                group: row_get(&row, "group")?,
                review_model: row_get(&row, "review_model")?,
                intensity: row_get(&row, "intensity")?,
                status: row_get(&row, "status")?,
                violation: row_get(&row, "violation")?,
                abuse: row_get(&row, "abuse")?,
                rules: parse_json_column(&row, "rules_json")?,
                explanation: row_get(&row, "explanation")?,
            };
            if !can_manage_target(actor_role, actor_id, target_user, &self.pg).await? {
                item.request_id.clear();
                item.user_id = 0;
                item.explanation.clear();
                item.rules = Value::Null;
            }
            items.push(item);
        }
        Ok((items, total, true))
    }

    async fn list_review_runs(
        &self,
        limit: i64,
    ) -> Result<Vec<SystemTaskSummary>, SecurityAdminError> {
        let rows = sqlx::query(
            "SELECT id, task_id, type, status, active_key, \
             CASE WHEN LENGTH(state) <= 4096 THEN state ELSE '' END AS state, \
             error, locked_by, created_at, updated_at \
             FROM system_tasks WHERE type = 'assistant_review' \
             ORDER BY id DESC LIMIT $1",
        )
        .bind(limit)
        .fetch_all(&self.pg)
        .await
        .map_err(db_error)?;
        rows.iter().map(task_summary_from_row).collect()
    }

    async fn get_review_run(
        &self,
        task_id: &str,
    ) -> Result<Option<SystemTaskDetail>, SecurityAdminError> {
        let row = sqlx::query(
            "SELECT id, task_id, type, status, active_key, payload, state, result, error, locked_by, created_at, updated_at \
             FROM system_tasks WHERE task_id = $1 AND type = 'assistant_review'",
        )
        .bind(task_id)
        .fetch_optional(&self.pg)
        .await
        .map_err(db_error)?;
        row.as_ref().map(task_detail_from_row).transpose()
    }

    async fn preview_review_run_cleanup(&self, keep: i64) -> Result<i64, SecurityAdminError> {
        sqlx::query_scalar::<_, i64>(
            "SELECT GREATEST(COUNT(*) - $1::BIGINT, 0)::BIGINT \
             FROM system_tasks \
             WHERE type = 'assistant_review' AND status IN ('succeeded', 'failed')",
        )
        .bind(keep)
        .fetch_one(&self.pg)
        .await
        .map_err(db_error)
    }

    async fn delete_review_runs(
        &self,
        keep: i64,
        expected_count: i64,
        admin_user_id: i64,
    ) -> Result<i64, SecurityAdminError> {
        let mut transaction = self.pg.begin().await.map_err(db_error)?;
        let ids = sqlx::query_scalar::<_, i64>(
            "SELECT id FROM system_tasks \
             WHERE type = 'assistant_review' AND status IN ('succeeded', 'failed') \
             ORDER BY id DESC OFFSET $1 FOR UPDATE",
        )
        .bind(keep)
        .fetch_all(&mut *transaction)
        .await
        .map_err(db_error)?;
        let candidate_count = i64::try_from(ids.len())
            .map_err(|_| SecurityAdminError("cleanup row count overflow".to_owned()))?;
        if candidate_count != expected_count {
            return Err(SecurityAdminError(STALE_REVIEW_CLEANUP_MESSAGE.to_owned()));
        }
        let deleted_count = if ids.is_empty() {
            0
        } else {
            let result = sqlx::query(
                "DELETE FROM system_tasks \
                 WHERE type = 'assistant_review' \
                 AND status IN ('succeeded', 'failed') AND id = ANY($1)",
            )
            .bind(&ids)
            .execute(&mut *transaction)
            .await
            .map_err(db_error)?;
            i64::try_from(result.rows_affected())
                .map_err(|_| SecurityAdminError("cleanup row count overflow".to_owned()))?
        };
        let content =
            format!("deleted {deleted_count} assistant review run history records (keep={keep})");
        let audit_result = sqlx::query(
            "INSERT INTO logs (user_id, created_at, type, content, username) \
             SELECT id, EXTRACT(EPOCH FROM NOW())::BIGINT, 4, $2, username \
             FROM users WHERE id = $1",
        )
        .bind(admin_user_id)
        .bind(content)
        .execute(&mut *transaction)
        .await
        .map_err(db_error)?;
        if audit_result.rows_affected() != 1 {
            return Err(SecurityAdminError(
                "cleanup audit record could not be written".to_owned(),
            ));
        }
        transaction.commit().await.map_err(db_error)?;
        Ok(deleted_count)
    }

    async fn list_admin_appeals(
        &self,
        status: &str,
        limit: i64,
    ) -> Result<Vec<ViolationFeeAppeal>, SecurityAdminError> {
        let rows = if status.is_empty() {
            sqlx::query(
                "SELECT id, record_id, user_id, reason, status, admin_user_id, admin_note, created_at, reviewed_at \
                 FROM violation_fee_appeals ORDER BY id DESC LIMIT $1",
            )
            .bind(limit)
            .fetch_all(&self.pg)
            .await
        } else {
            sqlx::query(
                "SELECT id, record_id, user_id, reason, status, admin_user_id, admin_note, created_at, reviewed_at \
                 FROM violation_fee_appeals WHERE status = $1 ORDER BY id DESC LIMIT $2",
            )
            .bind(status)
            .bind(limit)
            .fetch_all(&self.pg)
            .await
        }
        .map_err(db_error)?;
        rows.iter().map(appeal_from_row).collect()
    }

    async fn review_appeal(
        &self,
        admin_id: i64,
        appeal_id: i64,
        approve: bool,
        note: &str,
    ) -> Result<ViolationFeeAppeal, SecurityAdminError> {
        if !approve && note.chars().count() < 2 {
            return Err(SecurityAdminError(
                "拒绝申诉时必须填写至少 2 个字符的管理员意见".to_owned(),
            ));
        }
        let mut tx = self.pg.begin().await.map_err(db_error)?;
        let appeal = sqlx::query(
            "SELECT id, record_id, user_id, reason, status, admin_user_id, admin_note, created_at, reviewed_at \
             FROM violation_fee_appeals WHERE id = $1 FOR UPDATE",
        )
        .bind(appeal_id)
        .fetch_optional(&mut *tx)
        .await
        .map_err(db_error)?
        .ok_or_else(|| SecurityAdminError("违规扣费记录不存在".to_owned()))?;
        let status: String = row_get(&appeal, "status")?;
        if status != "pending" {
            return Err(SecurityAdminError("违规扣费记录已经处理".to_owned()));
        }
        let record_id: i64 = row_get(&appeal, "record_id")?;
        let user_id: i64 = row_get(&appeal, "user_id")?;
        let record = sqlx::query(
            "SELECT id, charged_quota, status FROM violation_fee_records \
             WHERE id = $1 AND user_id = $2 FOR UPDATE",
        )
        .bind(record_id)
        .bind(user_id)
        .fetch_optional(&mut *tx)
        .await
        .map_err(db_error)?
        .ok_or_else(|| SecurityAdminError("违规扣费记录不存在".to_owned()))?;
        let now = unix_timestamp();
        let new_status = if approve { "approved" } else { "rejected" };
        if approve {
            let charged: i64 = row_get(&record, "charged_quota")?;
            let record_status: String = row_get(&record, "status")?;
            if record_status == "charged" && charged > 0 {
                sqlx::query("UPDATE users SET quota = quota + $1 WHERE id = $2")
                    .bind(charged)
                    .bind(user_id)
                    .execute(&mut *tx)
                    .await
                    .map_err(db_error)?;
                sqlx::query(
                    "UPDATE violation_fee_records SET status = 'reversed', reversed_at = $1, reversed_by = $2 \
                     WHERE id = $3 AND status = 'charged'",
                )
                .bind(now)
                .bind(admin_id)
                .bind(record_id)
                .execute(&mut *tx)
                .await
                .map_err(db_error)?;
            }
        }
        let row = sqlx::query(
            "UPDATE violation_fee_appeals SET status = $1, admin_user_id = $2, admin_note = $3, reviewed_at = $4 \
             WHERE id = $5 \
             RETURNING id, record_id, user_id, reason, status, admin_user_id, admin_note, created_at, reviewed_at",
        )
        .bind(new_status)
        .bind(admin_id)
        .bind(note)
        .bind(now)
        .bind(appeal_id)
        .fetch_one(&mut *tx)
        .await
        .map_err(db_error)?;
        tx.commit().await.map_err(db_error)?;
        appeal_from_row(&row)
    }

    async fn submit_appeal(
        &self,
        user_id: i64,
        record_id: i64,
        reason: &str,
    ) -> Result<ViolationFeeAppeal, SecurityAdminError> {
        let chars = reason.chars().count();
        if record_id <= 0 || !(5..=2000).contains(&chars) {
            return Err(SecurityAdminError(
                "申诉说明需要 5 至 2000 个字符".to_owned(),
            ));
        }
        let mut tx = self.pg.begin().await.map_err(db_error)?;
        let record = sqlx::query(
            "SELECT id, status, charged_quota FROM violation_fee_records \
             WHERE id = $1 AND user_id = $2 FOR UPDATE",
        )
        .bind(record_id)
        .bind(user_id)
        .fetch_optional(&mut *tx)
        .await
        .map_err(db_error)?
        .ok_or_else(|| SecurityAdminError("违规扣费记录不存在".to_owned()))?;
        let status: String = row_get(&record, "status")?;
        let charged: i64 = row_get(&record, "charged_quota")?;
        if status != "charged" || charged <= 0 {
            return Err(SecurityAdminError("该违规扣费记录当前不可申诉".to_owned()));
        }
        let pending: i64 = sqlx::query_scalar(
            "SELECT COUNT(*)::BIGINT FROM violation_fee_appeals \
             WHERE record_id = $1 AND user_id = $2 AND status = 'pending'",
        )
        .bind(record_id)
        .bind(user_id)
        .fetch_one(&mut *tx)
        .await
        .map_err(db_error)?;
        if pending > 0 {
            return Err(SecurityAdminError("该违规扣费已有待处理申诉".to_owned()));
        }
        let row = sqlx::query(
            "INSERT INTO violation_fee_appeals (record_id, user_id, reason, status, created_at) \
             VALUES ($1, $2, $3, 'pending', $4) \
             RETURNING id, record_id, user_id, reason, status, admin_user_id, admin_note, created_at, reviewed_at",
        )
        .bind(record_id)
        .bind(user_id)
        .bind(reason)
        .bind(unix_timestamp())
        .fetch_one(&mut *tx)
        .await
        .map_err(db_error)?;
        tx.commit().await.map_err(db_error)?;
        appeal_from_row(&row)
    }

    async fn list_user_violation_fees(
        &self,
        user_id: i64,
        limit: i64,
    ) -> Result<Vec<ViolationFeeRecord>, SecurityAdminError> {
        let rows = sqlx::query(
            "SELECT id, user_id, policy_key, error_code, charged_quota, status, created_at, reversed_at \
             FROM violation_fee_records WHERE user_id = $1 ORDER BY id DESC LIMIT $2",
        )
        .bind(user_id)
        .bind(limit)
        .fetch_all(&self.pg)
        .await
        .map_err(db_error)?;
        rows.iter().map(record_from_row).collect()
    }
}

async fn authenticated_admin(
    state: &SecurityAdminState,
    headers: &HeaderMap,
) -> Result<DashboardUserView, Response> {
    let user = authenticate_dashboard(state, headers).await?;
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

async fn authenticated_root(
    state: &SecurityAdminState,
    headers: &HeaderMap,
) -> Result<DashboardUserView, Response> {
    let user = authenticate_dashboard(state, headers).await?;
    if user.role < ROOT_ROLE {
        return Err(user_policy_error(
            headers,
            UserAuthPolicyError::InsufficientPrivilege,
        ));
    }
    Ok(user)
}

async fn authenticated_user(
    state: &SecurityAdminState,
    headers: &HeaderMap,
) -> Result<DashboardUserView, Response> {
    let user = authenticate_dashboard(state, headers).await?;
    enforce_user_auth_view(&user).map_err(|error| user_policy_error(headers, error))?;
    Ok(user)
}

async fn authenticate_dashboard(
    state: &SecurityAdminState,
    headers: &HeaderMap,
) -> Result<DashboardUserView, Response> {
    let Some(credential) = crate::migration_routes::legacy_http::dashboard_credential(headers)
    else {
        return Err(dashboard_auth_error(headers, None));
    };
    state
        .auth
        .self_user_view_for_optional(SecretString::from(credential))
        .await
        .map_err(|error| dashboard_auth_error(headers, Some(error.kind)))
}

async fn can_manage_target(
    actor_role: i64,
    actor_id: i64,
    target_user: i64,
    pg: &PgPool,
) -> Result<bool, SecurityAdminError> {
    if actor_role >= ROOT_ROLE || target_user == actor_id {
        return Ok(true);
    }
    let target_role: Option<i64> = sqlx::query_scalar("SELECT role FROM users WHERE id = $1")
        .bind(target_user)
        .fetch_optional(pg)
        .await
        .map_err(db_error)?;
    Ok(target_role.is_some_and(|role| actor_role > role))
}

async fn table_exists(pg: &PgPool, table: &str) -> Result<bool, SecurityAdminError> {
    let exists: bool = sqlx::query_scalar(
        "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)",
    )
    .bind(table)
    .fetch_one(pg)
    .await
    .map_err(db_error)?;
    Ok(exists)
}

fn parse_review_history_keep(raw: Option<&str>) -> Result<i64, Response> {
    let query = parse_query(raw);
    let keep = match query.get("keep") {
        Some(value) => value.parse::<i64>().map_err(|_| cleanup_invalid_keep())?,
        None => DEFAULT_REVIEW_HISTORY_KEEP,
    };
    if !(1..=MAX_REVIEW_HISTORY_KEEP).contains(&keep) {
        return Err(cleanup_invalid_keep());
    }
    Ok(keep)
}

fn cleanup_invalid_keep() -> Response {
    legacy_json(
        StatusCode::BAD_REQUEST,
        json!({
            "success": false,
            "code": "INVALID_PARAMS",
            "message": "keep must be between 1 and 100",
        }),
    )
}

fn parse_review_history_expected_count(raw: Option<&str>) -> Result<i64, Response> {
    let query = parse_query(raw);
    let expected_count = query
        .get("expected_count")
        .ok_or_else(cleanup_invalid_expected_count)?
        .parse::<i64>()
        .map_err(|_| cleanup_invalid_expected_count())?;
    if !(0..=MAX_REVIEW_HISTORY_EXPECTED_COUNT).contains(&expected_count) {
        return Err(cleanup_invalid_expected_count());
    }
    Ok(expected_count)
}

fn cleanup_invalid_expected_count() -> Response {
    legacy_json(
        StatusCode::BAD_REQUEST,
        json!({
            "success": false,
            "code": "INVALID_PARAMS",
            "message": "expected_count must be between 0 and 100000",
        }),
    )
}

fn stale_cleanup() -> Response {
    legacy_json(
        StatusCode::CONFLICT,
        json!({
            "success": false,
            "code": "STALE_PREVIEW",
            "message": STALE_REVIEW_CLEANUP_MESSAGE,
        }),
    )
}

fn client_ip(request: &Request) -> &str {
    request
        .extensions()
        .get::<ClientIpKey>()
        .map_or("unknown", |key| key.0.as_str())
}

async fn critical_rate_limit(state: &SecurityAdminState, client_ip: &str) -> Option<Response> {
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

async fn require_cleanup_security_proof(
    state: &SecurityAdminState,
    headers: &HeaderMap,
    admin_id: i64,
) -> Result<(), Response> {
    let raw = headers
        .get(SECURITY_PROOF_HEADER)
        .and_then(|value| value.to_str().ok())
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .ok_or_else(|| {
            security_proof_error("SECURITY_PROOF_REQUIRED", "Secure verification is required")
        })?;
    let credential = crate::migration_routes::legacy_http::dashboard_credential(headers)
        .ok_or_else(|| {
            security_proof_error("SECURITY_PROOF_INVALID", "Security proof is invalid")
        })?;
    let session = state
        .auth
        .current_session(SecretString::from(credential))
        .await
        .map_err(|error| security_proof_auth_error(error.kind))?;
    if session.user.id != admin_id {
        return Err(security_proof_error(
            "SECURITY_PROOF_INVALID",
            "Security proof is invalid",
        ));
    }
    let allowed_methods = vec!["email".to_owned(), "2fa".to_owned(), "passkey".to_owned()];
    state
        .auth
        .verify_security_proof(
            SecretString::from(raw.to_owned()),
            admin_id,
            &session.session_id,
            REVIEW_RUN_CLEANUP_SCOPE,
            &allowed_methods,
        )
        .await
        .map_err(|error| security_proof_auth_error(error.kind))?;
    Ok(())
}

fn security_proof_auth_error(kind: AuthErrorKind) -> Response {
    if kind == AuthErrorKind::TokenExpired {
        security_proof_error("SECURITY_PROOF_EXPIRED", "Security proof has expired")
    } else {
        security_proof_error("SECURITY_PROOF_INVALID", "Security proof is invalid")
    }
}

fn security_proof_error(code: &str, message: &str) -> Response {
    legacy_json(
        StatusCode::FORBIDDEN,
        json!({"success": false, "code": code, "message": message}),
    )
}

fn no_store(mut response: Response) -> Response {
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        HeaderValue::from_static("no-store, no-cache, must-revalidate, private, max-age=0"),
    );
    response
}

fn parse_review_filter(query: &HashMap<String, String>) -> SecurityReviewFilter {
    let start_timestamp = query
        .get("start_timestamp")
        .and_then(|v| v.parse().ok())
        .unwrap_or(0);
    let end_timestamp = query
        .get("end_timestamp")
        .and_then(|v| v.parse().ok())
        .unwrap_or(0);
    let user_id = query
        .get("user_id")
        .and_then(|v| v.parse().ok())
        .unwrap_or(0);
    let decision = query
        .get("decision")
        .map_or("", |v| v.trim())
        .to_ascii_lowercase();
    SecurityReviewFilter {
        start_timestamp,
        end_timestamp,
        user_id,
        category: query.get("category").cloned().unwrap_or_default(),
        group: query.get("group").cloned().unwrap_or_default(),
        decision: decision.clone(),
        violations_only: query
            .get("violations_only")
            .is_some_and(|v| v.eq_ignore_ascii_case("true"))
            || decision == "violation",
        clear_only: decision == "clear",
    }
}

struct PageQuery {
    page: i64,
    page_size: i64,
}

impl PageQuery {
    fn offset(&self) -> i64 {
        (self.page.max(1) - 1) * self.page_size
    }
}

fn page_query(query: &HashMap<String, String>) -> PageQuery {
    let page = query
        .get("p")
        .and_then(|v| v.parse().ok())
        .filter(|v| *v != 0)
        .unwrap_or(1);
    let page_size = query
        .get("page_size")
        .or_else(|| query.get("ps"))
        .or_else(|| query.get("size"))
        .and_then(|v| v.parse().ok())
        .filter(|v| *v != 0)
        .unwrap_or(DEFAULT_PAGE_SIZE)
        .min(MAX_PAGE_SIZE);
    PageQuery { page, page_size }
}

fn task_summary_from_row(row: &PgRow) -> Result<SystemTaskSummary, SecurityAdminError> {
    Ok(SystemTaskSummary {
        id: row_get(row, "id")?,
        task_id: row_get(row, "task_id")?,
        r#type: row_get(row, "type")?,
        status: row_get(row, "status")?,
        active_key: row_get(row, "active_key")?,
        state: parse_json_column(row, "state")?,
        error: row_get(row, "error")?,
        locked_by: row_get(row, "locked_by")?,
        created_at: row_get(row, "created_at")?,
        updated_at: row_get(row, "updated_at")?,
    })
}

fn task_detail_from_row(row: &PgRow) -> Result<SystemTaskDetail, SecurityAdminError> {
    Ok(SystemTaskDetail {
        id: row_get(row, "id")?,
        task_id: row_get(row, "task_id")?,
        r#type: row_get(row, "type")?,
        status: row_get(row, "status")?,
        active_key: row_get(row, "active_key")?,
        payload: parse_json_column(row, "payload")?,
        state: parse_json_column(row, "state")?,
        result: parse_json_column(row, "result")?,
        error: row_get(row, "error")?,
        locked_by: row_get(row, "locked_by")?,
        created_at: row_get(row, "created_at")?,
        updated_at: row_get(row, "updated_at")?,
    })
}

fn appeal_from_row(row: &PgRow) -> Result<ViolationFeeAppeal, SecurityAdminError> {
    Ok(ViolationFeeAppeal {
        id: row_get(row, "id")?,
        record_id: row_get(row, "record_id")?,
        user_id: row_get(row, "user_id")?,
        reason: row_get(row, "reason")?,
        status: row_get(row, "status")?,
        admin_user_id: row_get(row, "admin_user_id")?,
        admin_note: row_get(row, "admin_note")?,
        created_at: row_get(row, "created_at")?,
        reviewed_at: row_get(row, "reviewed_at")?,
    })
}

fn record_from_row(row: &PgRow) -> Result<ViolationFeeRecord, SecurityAdminError> {
    Ok(ViolationFeeRecord {
        id: row_get(row, "id")?,
        user_id: row_get(row, "user_id")?,
        policy_key: row_get(row, "policy_key")?,
        error_code: row_get(row, "error_code")?,
        charged_quota: row_get(row, "charged_quota")?,
        status: row_get(row, "status")?,
        created_at: row_get(row, "created_at")?,
        reversed_at: row_get(row, "reversed_at")?,
    })
}

fn parse_json_column(row: &PgRow, column: &str) -> Result<Value, SecurityAdminError> {
    let raw: String = row_get(row, column)?;
    if raw.trim().is_empty() {
        return Ok(Value::Null);
    }
    Ok(serde_json::from_str(&raw).unwrap_or(Value::String(raw)))
}

fn row_get<'r, T>(row: &'r PgRow, column: &str) -> Result<T, SecurityAdminError>
where
    T: sqlx::Decode<'r, sqlx::Postgres> + sqlx::Type<sqlx::Postgres>,
{
    row.try_get(column).map_err(db_error)
}

fn db_error(error: impl std::fmt::Display) -> SecurityAdminError {
    SecurityAdminError(error.to_string())
}

fn parse_query(raw: Option<&str>) -> HashMap<String, String> {
    let mut values = HashMap::new();
    for (key, value) in form_urlencoded::parse(raw.unwrap_or_default().as_bytes()) {
        values
            .entry(key.into_owned())
            .or_insert_with(|| value.into_owned());
    }
    values
}

fn unix_timestamp() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
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
    let (status, code, message) = match kind {
        Some(AuthErrorKind::TokenExpired) | Some(AuthErrorKind::SessionRevoked) => (
            StatusCode::UNAUTHORIZED,
            "AUTH_TOKEN_EXPIRED",
            "Unauthorized, not logged in and no access token provided",
        ),
        Some(AuthErrorKind::Internal) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            "AUTH_INTERNAL_ERROR",
            "Database error, please contact the administrator",
        ),
        _ => (
            StatusCode::UNAUTHORIZED,
            "AUTH_UNAUTHORIZED",
            "Unauthorized, invalid access token",
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

#[cfg(test)]
mod cleanup_tests {
    use super::*;

    #[test]
    fn cleanup_keep_defaults_and_enforces_bounds() {
        assert_eq!(
            parse_review_history_keep(None).ok(),
            Some(DEFAULT_REVIEW_HISTORY_KEEP)
        );
        assert_eq!(parse_review_history_keep(Some("keep=1")).ok(), Some(1));
        assert_eq!(parse_review_history_keep(Some("keep=100")).ok(), Some(100));
        for query in ["keep=0", "keep=101", "keep=invalid"] {
            assert_eq!(
                parse_review_history_keep(Some(query))
                    .err()
                    .map(|response| response.status()),
                Some(StatusCode::BAD_REQUEST)
            );
        }
    }

    #[test]
    fn cleanup_expected_count_requires_a_safe_non_negative_value() {
        assert_eq!(
            parse_review_history_expected_count(Some("keep=30&expected_count=0")).ok(),
            Some(0)
        );
        assert_eq!(
            parse_review_history_expected_count(Some("expected_count=100000")).ok(),
            Some(MAX_REVIEW_HISTORY_EXPECTED_COUNT)
        );
        for query in [
            "keep=30",
            "expected_count=-1",
            "expected_count=100001",
            "expected_count=invalid",
        ] {
            assert_eq!(
                parse_review_history_expected_count(Some(query))
                    .err()
                    .map(|response| response.status()),
                Some(StatusCode::BAD_REQUEST)
            );
        }
    }

    #[test]
    fn stale_cleanup_uses_conflict_status() {
        assert_eq!(stale_cleanup().status(), StatusCode::CONFLICT);
    }

    #[test]
    fn cleanup_responses_disable_caching() {
        let response = no_store(api_success(json!({"ok": true})));
        assert_eq!(
            response.headers().get(header::CACHE_CONTROL),
            Some(&HeaderValue::from_static(
                "no-store, no-cache, must-revalidate, private, max-age=0"
            ))
        );
    }
}
