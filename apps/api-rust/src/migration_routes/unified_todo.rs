//! Unified todo center and L1 onboarding routes.

use std::{collections::HashMap, sync::Arc, time::{SystemTime, UNIX_EPOCH}};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{RawQuery, State},
    http::{HeaderMap, HeaderName, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get as route_get, post as route_post},
};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{PgPool, Row, postgres::PgRow};

use crate::auth::{
    AuthErrorKind, DashboardAuth, DashboardUserView, UserAuthPolicyError, enforce_user_auth_view,
    user_auth_message, user_auth_status,
};

const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const MAX_BODY_BYTES: usize = 64 * 1024;
const DEFAULT_PAGE_SIZE: i64 = 20;
const MAX_PAGE_SIZE: i64 = 50;
const MAX_PAGE: i64 = 100;
const ADMIN_ROLE: i64 = 10;

#[derive(Clone)]
pub struct UnifiedTodoState {
    backend: Arc<dyn UnifiedTodoBackend>,
    auth: Arc<dyn DashboardAuth>,
}

impl UnifiedTodoState {
    #[must_use]
    pub fn new(pg: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self::with_backend(Arc::new(PgUnifiedTodoBackend { pg }), auth)
    }

    #[must_use]
    pub fn with_backend(backend: Arc<dyn UnifiedTodoBackend>, auth: Arc<dyn DashboardAuth>) -> Self {
        Self { backend, auth }
    }
}

pub fn router(state: UnifiedTodoState) -> Router {
    Router::new()
        .route("/api/todos", route_get(get_todos))
        .route("/api/todos/read", route_post(mark_read))
        .route(
            "/api/user/self/onboarding/todo",
            route_get(get_onboarding).patch(patch_onboarding),
        )
        .route("/api/onboarding/todo/proof", route_post(post_proof))
        .with_state(state)
}

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
#[error("{0}")]
pub struct UnifiedTodoError(pub String);

#[async_trait]
pub trait UnifiedTodoBackend: Send + Sync {
    async fn list_todos(
        &self,
        user_id: i64,
        role: i64,
        category: &str,
        page: i64,
        page_size: i64,
    ) -> Result<UnifiedTodoPage, UnifiedTodoError>;

    async fn mark_read(
        &self,
        user_id: i64,
        role: i64,
        category: &str,
        ids: &[i64],
        all: bool,
    ) -> Result<i64, UnifiedTodoError>;

    async fn get_onboarding(&self, user_id: i64) -> Result<L1OnboardingView, UnifiedTodoError>;

    async fn refresh_onboarding(&self, user_id: i64) -> Result<L1OnboardingView, UnifiedTodoError>;

    async fn apply_proof(
        &self,
        user_id: i64,
        token_id: i64,
        proof: L1OnboardingProof,
    ) -> Result<L1OnboardingView, UnifiedTodoError>;

    async fn authenticate_api_token(
        &self,
        raw: &str,
    ) -> Result<(i64, i64), UnifiedTodoError>;
}

#[derive(Clone, Debug, Serialize)]
struct UnifiedTodoItem {
    id: String,
    source_id: i64,
    category: String,
    r#type: String,
    title: String,
    summary: String,
    read: bool,
    created_at: i64,
    updated_at: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    details: Option<Value>,
}

#[derive(Clone, Debug, Serialize)]
struct UnifiedTodoCategorySummary {
    key: String,
    total: i64,
    unread: i64,
}

#[derive(Clone, Debug, Serialize)]
struct UnifiedTodoPage {
    items: Vec<UnifiedTodoItem>,
    page: i64,
    page_size: i64,
    total: i64,
    category: String,
    unread_count: i64,
    total_unread_count: i64,
    unread_by_category: HashMap<String, i64>,
    categories: Vec<UnifiedTodoCategorySummary>,
}

#[derive(Clone, Debug, Serialize)]
struct L1OnboardingEligibility {
    eligible: bool,
    developer_access_granted: bool,
    trust_level: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    reason: String,
}

#[derive(Clone, Debug, Serialize)]
struct L1OnboardingStepState {
    id: String,
    status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    completed_at: Option<i64>,
}

#[derive(Clone, Debug, Serialize)]
struct L1OnboardingView {
    eligibility: L1OnboardingEligibility,
    status: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    current_step: String,
    steps: Vec<L1OnboardingStepState>,
    #[serde(skip_serializing_if = "Option::is_none")]
    completed_at: Option<i64>,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct TodoReadRequest {
    #[serde(default)]
    category: String,
    #[serde(default)]
    ids: Vec<i64>,
    #[serde(default)]
    all: bool,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct L1OnboardingProof {
    #[serde(default)]
    step: String,
    #[serde(default)]
    client: String,
    #[serde(default)]
    base_url: String,
    #[serde(default)]
    group: String,
}

#[derive(Clone)]
struct PgUnifiedTodoBackend {
    pg: PgPool,
}

async fn get_todos(
    State(state): State<UnifiedTodoState>,
    headers: HeaderMap,
    RawQuery(raw): RawQuery,
) -> Response {
    let user = match authenticated_user(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return response,
    };
    let query = parse_query(raw.as_deref());
    let page = query.get("p").and_then(|v| v.parse().ok()).unwrap_or(1).clamp(1, MAX_PAGE);
    let page_size = query
        .get("page_size")
        .or_else(|| query.get("size"))
        .and_then(|v| v.parse().ok())
        .unwrap_or(DEFAULT_PAGE_SIZE)
        .clamp(1, MAX_PAGE_SIZE);
    let category = query.get("category").map_or("all", |v| v.trim());
    let response = match state
        .backend
        .list_todos(user.id, user.role, category, page, page_size)
        .await
    {
        Ok(page) => api_success(json!(page)),
        Err(error) => api_error(&error.0),
    };
    with_auth_version(response)
}

async fn mark_read(
    State(state): State<UnifiedTodoState>,
    headers: HeaderMap,
    request: axum::extract::Request,
) -> Response {
    let user = match authenticated_user(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return response,
    };
    let body = match to_bytes(request.into_body(), MAX_BODY_BYTES).await {
        Ok(bytes) => bytes,
        Err(_) => return with_auth_version(api_error("待办已读请求无效")),
    };
    let input: TodoReadRequest = match serde_json::from_slice(&body) {
        Ok(input) => input,
        Err(_) => return with_auth_version(api_error("待办已读请求无效")),
    };
    let response = match state
        .backend
        .mark_read(
            user.id,
            user.role,
            input.category.trim(),
            &input.ids,
            input.all,
        )
        .await
    {
        Ok(marked) => api_success(json!({
            "category": input.category,
            "all": input.all,
            "marked": marked,
        })),
        Err(error) => api_error(&error.0),
    };
    with_auth_version(response)
}

async fn get_onboarding(
    State(state): State<UnifiedTodoState>,
    headers: HeaderMap,
) -> Response {
    let user = match authenticated_user(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return response,
    };
    let response = match state.backend.get_onboarding(user.id).await {
        Ok(view) => api_success(json!(view)),
        Err(error) => onboarding_error(&error.0),
    };
    with_auth_version(response)
}

async fn patch_onboarding(
    State(state): State<UnifiedTodoState>,
    headers: HeaderMap,
) -> Response {
    let user = match authenticated_user(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return response,
    };
    let response = match state.backend.refresh_onboarding(user.id).await {
        Ok(view) => api_success(json!(view)),
        Err(error) => onboarding_error(&error.0),
    };
    with_auth_version(response)
}

async fn post_proof(
    State(state): State<UnifiedTodoState>,
    headers: HeaderMap,
    request: axum::extract::Request,
) -> Response {
    let raw = dashboard_or_token_credential(&headers);
    let Some(raw) = raw else {
        return token_auth_failure();
    };
    let (user_id, token_id) = match state.backend.authenticate_api_token(&raw).await {
        Ok(ids) => ids,
        Err(_) => return token_auth_failure(),
    };
    let body = match to_bytes(request.into_body(), MAX_BODY_BYTES).await {
        Ok(bytes) => bytes,
        Err(_) => return onboarding_error("invalid onboarding proof"),
    };
    let proof: L1OnboardingProof = match serde_json::from_slice(&body) {
        Ok(proof) => proof,
        Err(_) => return onboarding_error("invalid onboarding proof"),
    };
    let response = match state.backend.apply_proof(user_id, token_id, proof).await {
        Ok(view) => api_success(json!(view)),
        Err(error) => onboarding_error(&error.0),
    };
    with_auth_version(response)
}

#[async_trait]
impl UnifiedTodoBackend for PgUnifiedTodoBackend {
    async fn list_todos(
        &self,
        user_id: i64,
        role: i64,
        category: &str,
        page: i64,
        page_size: i64,
    ) -> Result<UnifiedTodoPage, UnifiedTodoError> {
        let category = normalize_category(category)?;
        let offset = (page - 1) * page_size;
        let refs = load_todo_refs(&self.pg, user_id, role, category, offset, page_size).await?;
        let items = hydrate_todo_items(&self.pg, user_id, role, &refs).await?;
        let total = count_todo_refs(&self.pg, user_id, role, category).await?;
        let unread_by_category = count_unread_by_category(&self.pg, user_id, role).await?;
        let total_unread: i64 = unread_by_category.values().sum();
        let categories = build_category_summaries(&self.pg, user_id, role).await?;
        Ok(UnifiedTodoPage {
            items,
            page,
            page_size,
            total,
            category: category.to_owned(),
            unread_count: unread_by_category
                .get(category)
                .copied()
                .unwrap_or(total_unread),
            total_unread_count: total_unread,
            unread_by_category,
            categories,
        })
    }

    async fn mark_read(
        &self,
        user_id: i64,
        role: i64,
        category: &str,
        ids: &[i64],
        all: bool,
    ) -> Result<i64, UnifiedTodoError> {
        let category = normalize_category(category)?;
        if category == "all" && !all {
            return Err(UnifiedTodoError("待办已读请求无效".to_owned()));
        }
        if !all && ids.is_empty() {
            return Err(UnifiedTodoError("待办已读请求无效".to_owned()));
        }
        let now = unix_timestamp();
        let mut marked = 0i64;
        let categories: Vec<&str> = if category == "all" {
            vec![
                "security_incident",
                "security_review",
                "open_source_bounty_review",
                "open_source_bounty",
                "developer_access",
                "account_action",
            ]
        } else {
            vec![category]
        };
        for cat in categories {
            let target_ids = if all {
                load_all_source_ids(&self.pg, user_id, role, cat).await?
            } else if category == "all" {
                continue;
            } else {
                ids.to_vec()
            };
            for item_id in target_ids {
                let result = sqlx::query(
                    "INSERT INTO unified_todo_reads (user_id, category, item_id, read_at) \
                     VALUES ($1, $2, $3, $4) ON CONFLICT (user_id, category, item_id) DO NOTHING",
                )
                .bind(user_id)
                .bind(cat)
                .bind(item_id)
                .bind(now)
                .execute(&self.pg)
                .await
                .map_err(db_error)?;
                marked += result.rows_affected() as i64;
            }
        }
        if category != "all" && !all {
            for item_id in ids {
                let result = sqlx::query(
                    "INSERT INTO unified_todo_reads (user_id, category, item_id, read_at) \
                     VALUES ($1, $2, $3, $4) ON CONFLICT (user_id, category, item_id) DO NOTHING",
                )
                .bind(user_id)
                .bind(category)
                .bind(item_id)
                .bind(now)
                .execute(&self.pg)
                .await
                .map_err(db_error)?;
                marked += result.rows_affected() as i64;
            }
        }
        Ok(marked)
    }

    async fn get_onboarding(&self, user_id: i64) -> Result<L1OnboardingView, UnifiedTodoError> {
        build_onboarding_view(&self.pg, user_id).await
    }

    async fn refresh_onboarding(&self, user_id: i64) -> Result<L1OnboardingView, UnifiedTodoError> {
        build_onboarding_view(&self.pg, user_id).await
    }

    async fn apply_proof(
        &self,
        user_id: i64,
        token_id: i64,
        proof: L1OnboardingProof,
    ) -> Result<L1OnboardingView, UnifiedTodoError> {
        let step = proof.step.trim();
        if step != "install_client" && step != "configure_client" {
            return Err(UnifiedTodoError("invalid L1 onboarding step".to_owned()));
        }
        let client = proof.client.trim();
        if client.is_empty() || client.chars().count() > 64 {
            return Err(UnifiedTodoError("invalid onboarding proof".to_owned()));
        }
        if step == "configure_client"
            && (proof.base_url.trim().is_empty()
                || proof.group.trim().is_empty()
                || proof.base_url.chars().count() > 256)
            {
                return Err(UnifiedTodoError("invalid onboarding proof".to_owned()));
            }
        let view = build_onboarding_view(&self.pg, user_id).await?;
        if !view.eligibility.eligible {
            return Err(UnifiedTodoError(
                "L1 onboarding is only available to users with approved developer access"
                    .to_owned(),
            ));
        }
        let token = sqlx::query(
            "SELECT id, status, \"group\", auto_groups FROM tokens \
             WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL",
        )
        .bind(token_id)
        .bind(user_id)
        .fetch_optional(&self.pg)
        .await
        .map_err(db_error)?
        .ok_or_else(|| UnifiedTodoError("a server-verified onboarding proof is required".to_owned()))?;
        let token_status: i64 = row_get(&token, "status")?;
        if token_status != 1 {
            return Err(UnifiedTodoError(
                "a server-verified onboarding proof is required".to_owned(),
            ));
        }
        let now = unix_timestamp();
        ensure_onboarding_row(&self.pg, user_id, now).await?;
        if step == "install_client" {
            if view.current_step != "install_client" && !view.current_step.is_empty() {
                let installed: i64 = sqlx::query_scalar(
                    "SELECT client_installed_at FROM l1_onboarding_todos WHERE user_id = $1",
                )
                .bind(user_id)
                .fetch_one(&self.pg)
                .await
                .map_err(db_error)?;
                if installed == 0 && view.current_step != "install_client" {
                    return Err(UnifiedTodoError(
                        "L1 onboarding steps must be completed in order".to_owned(),
                    ));
                }
            }
            sqlx::query(
                "UPDATE l1_onboarding_todos SET client_installed_at = CASE WHEN client_installed_at = 0 THEN $1 ELSE client_installed_at END, updated_at = $1 WHERE user_id = $2",
            )
            .bind(now)
            .bind(user_id)
            .execute(&self.pg)
            .await
            .map_err(db_error)?;
        } else {
            let token_group: String = row_get(&token, "group")?;
            let auto_groups: String = row_get(&token, "auto_groups")?;
            if token_group.is_empty() && auto_groups.is_empty() {
                return Err(UnifiedTodoError("invalid onboarding proof".to_owned()));
            }
            let group = proof.group.trim();
            let mut group_matches = token_group == group;
            if token_group == "auto" {
                group_matches = auto_groups.split(',').any(|g| g.trim() == group);
            }
            if !group_matches {
                return Err(UnifiedTodoError("invalid onboarding proof".to_owned()));
            }
            sqlx::query(
                "UPDATE l1_onboarding_todos SET client_configured_at = CASE WHEN client_configured_at = 0 THEN $1 ELSE client_configured_at END, updated_at = $1 WHERE user_id = $2",
            )
            .bind(now)
            .bind(user_id)
            .execute(&self.pg)
            .await
            .map_err(db_error)?;
        }
        build_onboarding_view(&self.pg, user_id).await
    }

    async fn authenticate_api_token(&self, raw: &str) -> Result<(i64, i64), UnifiedTodoError> {
        let key = token_middleware_key(raw);
        let row = sqlx::query(
            "SELECT t.id, t.user_id, COALESCE(t.status, 1) AS status, COALESCE(u.status, 1) AS user_status \
             FROM tokens t JOIN users u ON u.id = t.user_id AND u.deleted_at IS NULL \
             WHERE t.deleted_at IS NULL AND (t.key = $1 OR t.key LIKE $1 || '-%') \
             ORDER BY CASE WHEN t.key = $1 THEN 0 ELSE 1 END, t.id LIMIT 1",
        )
        .bind(&key)
        .fetch_optional(&self.pg)
        .await
        .map_err(db_error)?
        .ok_or_else(|| UnifiedTodoError("invalid token".to_owned()))?;
        let status: i64 = row_get(&row, "status")?;
        let user_status: i64 = row_get(&row, "user_status")?;
        if status == 2 {
            return Err(UnifiedTodoError("invalid token".to_owned()));
        }
        if user_status != 1 {
            return Err(UnifiedTodoError("user banned".to_owned()));
        }
        Ok((row_get(&row, "user_id")?, row_get(&row, "id")?))
    }
}

struct TodoRef {
    source_id: i64,
    category: String,
    updated_at: i64,
}

async fn load_todo_refs(
    pg: &PgPool,
    user_id: i64,
    role: i64,
    category: &str,
    offset: i64,
    limit: i64,
) -> Result<Vec<TodoRef>, UnifiedTodoError> {
    let is_admin = role >= ADMIN_ROLE;
    let mut parts = Vec::new();
    if (category == "all" || category == "developer_access")
        && (is_admin || category != "all" || true)
        && (is_admin || category == "developer_access") {
            let clause = if is_admin {
                "SELECT request.id AS source_id, 'developer_access' AS category, request.created_at AS updated_at \
                 FROM developer_access_requests AS request \
                 JOIN users ON users.id = request.user_id AND users.deleted_at IS NULL \
                 WHERE request.status = 'pending' AND request.source <> 'legacy'"
            } else {
                "SELECT request.id AS source_id, 'developer_access' AS category, request.created_at AS updated_at \
                 FROM developer_access_requests AS request \
                 JOIN users ON users.id = request.user_id AND users.deleted_at IS NULL \
                 WHERE request.status = 'pending' AND request.source <> 'legacy' AND request.user_id = $1"
            };
            parts.push(clause.to_owned());
        }
    if category == "all" || category == "open_source_bounty" {
        parts.push(
            "SELECT notification.id AS source_id, 'open_source_bounty' AS category, notification.created_at AS updated_at \
             FROM open_source_bounty_ledgers AS notification \
             WHERE notification.counterparty_user_id = $1 AND notification.kind IN ('tip_transfer', 'reward_transfer', 'dispute_reward_transfer')"
                .to_owned(),
        );
    }
    if parts.is_empty() {
        return Ok(Vec::new());
    }
    let sql = format!(
        "SELECT source_id, category, updated_at FROM ({}) AS todo ORDER BY updated_at DESC, category ASC, source_id DESC LIMIT $2 OFFSET $3",
        parts.join(" UNION ALL ")
    );
    let rows = sqlx::query(&sql)
        .bind(user_id)
        .bind(limit)
        .bind(offset)
        .fetch_all(pg)
        .await
        .map_err(db_error)?;
    Ok(rows
        .into_iter()
        .map(|row| TodoRef {
            source_id: row.try_get("source_id").unwrap_or(0),
            category: row.try_get("category").unwrap_or_default(),
            updated_at: row.try_get("updated_at").unwrap_or(0),
        })
        .collect())
}

async fn hydrate_todo_items(
    pg: &PgPool,
    user_id: i64,
    role: i64,
    refs: &[TodoRef],
) -> Result<Vec<UnifiedTodoItem>, UnifiedTodoError> {
    let mut items = Vec::with_capacity(refs.len());
    for reference in refs {
        let read = sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*)::BIGINT FROM unified_todo_reads \
             WHERE user_id = $1 AND category = $2 AND item_id = $3",
        )
        .bind(user_id)
        .bind(&reference.category)
        .bind(reference.source_id)
        .fetch_one(pg)
        .await
        .map_err(db_error)?
            > 0;
        let (title, summary, details) = match reference.category.as_str() {
            "developer_access" => (
                "developer_access.request",
                "pending developer access request",
                json!({"request_id": reference.source_id}),
            ),
            "open_source_bounty" => (
                "open_source_bounty.notification",
                "open source bounty notification",
                json!({"notification_id": reference.source_id}),
            ),
            _ => ("todo.item", "pending item", json!({})),
        };
        items.push(UnifiedTodoItem {
            id: format!("{}:{}", reference.category, reference.source_id),
            source_id: reference.source_id,
            category: reference.category.clone(),
            r#type: reference.category.clone(),
            title: title.to_owned(),
            summary: summary.to_owned(),
            read,
            created_at: reference.updated_at,
            updated_at: reference.updated_at,
            details: Some(details),
        });
    }
    let _ = role;
    Ok(items)
}

async fn count_todo_refs(
    pg: &PgPool,
    user_id: i64,
    role: i64,
    category: &str,
) -> Result<i64, UnifiedTodoError> {
    let refs = load_todo_refs(pg, user_id, role, category, 0, i64::MAX).await?;
    Ok(refs.len() as i64)
}

async fn count_unread_by_category(
    pg: &PgPool,
    user_id: i64,
    role: i64,
) -> Result<HashMap<String, i64>, UnifiedTodoError> {
    let mut map = HashMap::new();
    for category in [
        "developer_access",
        "open_source_bounty",
        "account_action",
        "security_incident",
        "security_review",
        "open_source_bounty_review",
    ] {
        let refs = load_todo_refs(pg, user_id, role, category, 0, i64::MAX).await?;
        let unread = hydrate_todo_items(pg, user_id, role, &refs)
            .await?
            .into_iter()
            .filter(|item| !item.read)
            .count() as i64;
        if unread > 0 {
            map.insert(category.to_owned(), unread);
        }
    }
    Ok(map)
}

async fn build_category_summaries(
    pg: &PgPool,
    user_id: i64,
    role: i64,
) -> Result<Vec<UnifiedTodoCategorySummary>, UnifiedTodoError> {
    let mut summaries = Vec::new();
    for category in [
        "security_incident",
        "security_review",
        "open_source_bounty_review",
        "open_source_bounty",
        "developer_access",
        "account_action",
    ] {
        let total = count_todo_refs(pg, user_id, role, category).await?;
        let unread_map = count_unread_by_category(pg, user_id, role).await?;
        summaries.push(UnifiedTodoCategorySummary {
            key: category.to_owned(),
            total,
            unread: unread_map.get(category).copied().unwrap_or(0),
        });
    }
    Ok(summaries)
}

async fn load_all_source_ids(
    pg: &PgPool,
    user_id: i64,
    role: i64,
    category: &str,
) -> Result<Vec<i64>, UnifiedTodoError> {
    Ok(load_todo_refs(pg, user_id, role, category, 0, i64::MAX)
        .await?
        .into_iter()
        .map(|reference| reference.source_id)
        .collect())
}

async fn build_onboarding_view(pg: &PgPool, user_id: i64) -> Result<L1OnboardingView, UnifiedTodoError> {
    let user = sqlx::query(
        "SELECT id, COALESCE(console_activated_at, 0) AS console_activated_at, \
         COALESCE(trust_level_override, -1) AS trust_level_override, \
         COALESCE(last_api_activity_at, 0) AS last_api_activity_at \
         FROM users WHERE id = $1 AND deleted_at IS NULL",
    )
    .bind(user_id)
    .fetch_optional(pg)
    .await
    .map_err(db_error)?
    .ok_or_else(|| UnifiedTodoError("user not found".to_owned()))?;
    let developer_access_granted: bool = sqlx::query_scalar(
        "SELECT EXISTS (SELECT 1 FROM developer_access_requests WHERE user_id = $1 AND status = 'approved')",
    )
    .bind(user_id)
    .fetch_one(pg)
    .await
    .map_err(db_error)?;
    let trust_level: i64 = row_get(&user, "trust_level_override")?;
    let eligible = developer_access_granted || trust_level >= 1;
    let eligibility = L1OnboardingEligibility {
        eligible,
        developer_access_granted,
        trust_level,
        reason: if eligible {
            String::new()
        } else {
            "L1_REQUIRED".to_owned()
        },
    };
    if !eligible {
        return Ok(L1OnboardingView {
            eligibility,
            status: "unavailable".to_owned(),
            current_step: String::new(),
            steps: Vec::new(),
            completed_at: None,
        });
    }
    let now = unix_timestamp();
    ensure_onboarding_row(pg, user_id, now).await?;
    let todo = sqlx::query(
        "SELECT client_installed_at, client_configured_at, completed_at FROM l1_onboarding_todos WHERE user_id = $1",
    )
    .bind(user_id)
    .fetch_one(pg)
    .await
    .map_err(db_error)?;
    let key_exists: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM tokens WHERE user_id = $1 AND status = 1 AND deleted_at IS NULL",
    )
    .bind(user_id)
    .fetch_one(pg)
    .await
    .map_err(db_error)?;
    let key_complete = key_exists > 0;
    let installed: i64 = row_get(&todo, "client_installed_at")?;
    let configured: i64 = row_get(&todo, "client_configured_at")?;
    let last_api: i64 = row_get(&user, "last_api_activity_at")?;
    let install_complete = key_complete && installed > 0;
    let configure_complete = install_complete && configured > 0;
    let first_response_complete =
        configure_complete && last_api >= configured && last_api > 0;
    let mut completed_at: i64 = row_get(&todo, "completed_at")?;
    if first_response_complete && completed_at == 0 {
        completed_at = now;
        sqlx::query("UPDATE l1_onboarding_todos SET completed_at = $1, updated_at = $1 WHERE user_id = $2")
            .bind(now)
            .bind(user_id)
            .execute(pg)
            .await
            .map_err(db_error)?;
    }
    if completed_at > 0 && !first_response_complete {
        completed_at = 0;
        sqlx::query("UPDATE l1_onboarding_todos SET completed_at = 0, updated_at = $1 WHERE user_id = $2")
            .bind(now)
            .bind(user_id)
            .execute(pg)
            .await
            .map_err(db_error)?;
    }
    let steps = vec![
        step_state("create_api_key", key_complete, now),
        step_state("install_client", install_complete, installed),
        step_state("configure_client", configure_complete, configured),
        step_state(
            "first_successful_response",
            first_response_complete,
            last_api,
        ),
    ];
    let current = steps
        .iter()
        .find(|step| step.status != "completed")
        .map(|step| step.id.clone())
        .unwrap_or_default();
    Ok(L1OnboardingView {
        eligibility,
        status: if completed_at > 0 {
            "completed".to_owned()
        } else {
            "in_progress".to_owned()
        },
        current_step: current,
        steps,
        completed_at: if completed_at > 0 {
            Some(completed_at)
        } else {
            None
        },
    })
}

fn step_state(id: &str, complete: bool, timestamp: i64) -> L1OnboardingStepState {
    L1OnboardingStepState {
        id: id.to_owned(),
        status: if complete {
            "completed".to_owned()
        } else {
            "pending".to_owned()
        },
        completed_at: if complete { Some(timestamp) } else { None },
    }
}

async fn ensure_onboarding_row(pg: &PgPool, user_id: i64, now: i64) -> Result<(), UnifiedTodoError> {
    sqlx::query(
        "INSERT INTO l1_onboarding_todos (user_id, created_at, updated_at) VALUES ($1, $2, $2) \
         ON CONFLICT (user_id) DO NOTHING",
    )
    .bind(user_id)
    .bind(now)
    .execute(pg)
    .await
    .map_err(db_error)?;
    Ok(())
}

fn normalize_category(category: &str) -> Result<&str, UnifiedTodoError> {
    let category = category.trim();
    if category.is_empty() || category == "all" {
        return Ok("all");
    }
    match category {
        "open_source_bounty" | "open_source_bounty_review" | "developer_access"
        | "account_action" | "security_incident" | "security_review" => Ok(category),
        _ => Err(UnifiedTodoError("待办分类无效".to_owned())),
    }
}

fn token_middleware_key(raw: &str) -> String {
    let key = if raw.starts_with("Bearer ") || raw.starts_with("bearer ") {
        raw[7..].trim()
    } else {
        raw.trim()
    };
    let key = key.strip_prefix("sk-").unwrap_or(key);
    key.split('-').next().unwrap_or_default().to_owned()
}

async fn authenticated_user(
    state: &UnifiedTodoState,
    headers: &HeaderMap,
) -> Result<DashboardUserView, Response> {
    let Some(credential) = dashboard_credential(headers) else {
        return Err(dashboard_auth_error(headers, None));
    };
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(credential))
        .await
        .map_err(|error| dashboard_auth_error(headers, Some(error.kind)))?;
    enforce_user_auth_view(&user).map_err(|error| user_policy_error(headers, error))?;
    Ok(user)
}

fn dashboard_or_token_credential(headers: &HeaderMap) -> Option<String> {
    headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(str::to_owned)
        .or_else(|| {
            headers
                .get("x-api-key")
                .and_then(|value| value.to_str().ok())
                .map(str::trim)
                .filter(|value| !value.is_empty())
                .map(str::to_owned)
        })
}

fn onboarding_error(message: &str) -> Response {
    let (status, code) = match message {
        m if m.contains("only available") => (StatusCode::FORBIDDEN, "L1_ONBOARDING_NOT_ELIGIBLE"),
        m if m.contains("invalid L1 onboarding step") => {
            (StatusCode::UNPROCESSABLE_ENTITY, "L1_ONBOARDING_INVALID_STEP")
        }
        m if m.contains("in order") => (StatusCode::CONFLICT, "L1_ONBOARDING_OUT_OF_ORDER"),
        m if m.contains("proof is required") => (StatusCode::FORBIDDEN, "L1_ONBOARDING_PROOF_REQUIRED"),
        _ => (StatusCode::UNPROCESSABLE_ENTITY, "L1_ONBOARDING_INVALID_PROOF"),
    };
    legacy_json(
        status,
        json!({"success": false, "code": code, "message": message}),
    )
}

fn token_auth_failure() -> Response {
    legacy_json(
        StatusCode::UNAUTHORIZED,
        json!({"success": false, "message": "Invalid token"}),
    )
}

fn row_get<'r, T>(row: &'r PgRow, column: &str) -> Result<T, UnifiedTodoError>
where
    T: sqlx::Decode<'r, sqlx::Postgres> + sqlx::Type<sqlx::Postgres>,
{
    row.try_get(column).map_err(db_error)
}

fn db_error(error: impl std::fmt::Display) -> UnifiedTodoError {
    UnifiedTodoError(error.to_string())
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
        .map_or(0, |duration| i64::try_from(duration.as_secs()).unwrap_or(i64::MAX))
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
            "code": "AUTH_USER_DISABLED",
            "message": user_auth_message(
                error,
                headers.get(header::ACCEPT_LANGUAGE).and_then(|value| value.to_str().ok()),
            ),
        }),
    )
}
