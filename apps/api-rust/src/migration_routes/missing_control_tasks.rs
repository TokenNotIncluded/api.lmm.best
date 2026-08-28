//! Legacy-compatible task listings and administrator status probe.
//!
//! This slice owns the remaining control-plane task reads without making the
//! relay task handlers a second authority for dashboard data.  Authentication
//! is delegated to the already-audited observability authorizer and every
//! external dependency is explicit: an unavailable store or status collector
//! returns an error rather than fabricating an empty successful response.

use std::{collections::BTreeMap, sync::Arc};

use async_trait::async_trait;
use axum::{
    Json, Router,
    extract::{RawQuery, Request, State},
    http::{HeaderMap, StatusCode},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::get,
};
use secrecy::SecretString;
use serde::Serialize;
use serde_json::{Value, json};
use sqlx::{PgPool, Row};
use thiserror::Error;

use crate::auth::DashboardAuth;

use super::observability::{ObservabilityAccess, ObservabilityAuthorizer, ObservabilityPrincipal};

const ADMIN_ROLE: i64 = 10;
const ROOT_ROLE: i64 = 100;
const USER_ROLE: i64 = 1;
const DEFAULT_PAGE_SIZE: i64 = 10;
const MAX_PAGE_SIZE: i64 = 100;

/// The three legacy task collections selected by the HTTP boundary.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ControlTaskOperation {
    /// Administrator view of every Midjourney task.
    MidjourneyAll,
    /// Current user's Midjourney task view.
    MidjourneySelf,
    /// Current user's generic asynchronous task view.
    TaskSelf,
}

/// Canonical, parsed paging and filter input passed to storage.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ControlTaskQuery {
    /// One-based page number, retaining the Go legacy parsing behavior.
    pub page: i64,
    /// Bounded page size, retaining the Go `page_size`/`ps`/`size` aliases.
    pub page_size: i64,
    /// Remaining decoded query filters.
    pub filters: BTreeMap<String, String>,
}

/// Fully authorized task-list request.
#[derive(Clone, Debug)]
pub struct ControlTaskCall {
    /// Selected collection.
    pub operation: ControlTaskOperation,
    /// Server-authenticated dashboard principal.
    pub principal: ObservabilityPrincipal,
    /// Frozen pagination and filtering input.
    pub query: ControlTaskQuery,
}

/// Persistence boundary for the three legacy task listings.
///
/// Implementations must scope `*_self` calls using the supplied server-side
/// user id. They must never trust an id or role passed by the client.
#[async_trait]
pub trait ControlTaskStore: Send + Sync {
    /// Returns the exact legacy `PageInfo` payload for an authorized list.
    async fn list(&self, call: ControlTaskCall) -> Result<Value, ControlTaskStoreError>;
}

/// Runtime status boundary for `GET /api/status/test`.
///
/// The Go handler checks database reachability and returns process HTTP
/// counters. Keeping both in this boundary prevents a successful health reply
/// from being emitted when either runtime owner is unavailable.
#[async_trait]
pub trait ControlTaskStatusProbe: Send + Sync {
    /// Checks dependencies and returns the direct legacy `http_stats` object.
    async fn test_status(&self) -> Result<Value, ControlTaskStatusError>;
}

/// PostgreSQL implementation of the task listing boundary.
#[derive(Clone)]
pub struct PgControlTaskStore {
    pg: PgPool,
}

impl PgControlTaskStore {
    /// Creates a PostgreSQL task-list adapter.
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self { pg }
    }
}

#[async_trait]
impl ControlTaskStore for PgControlTaskStore {
    async fn list(&self, call: ControlTaskCall) -> Result<Value, ControlTaskStoreError> {
        let offset = (call.query.page - 1).saturating_mul(call.query.page_size);
        match call.operation {
            ControlTaskOperation::MidjourneyAll | ControlTaskOperation::MidjourneySelf => {
                self.midjourneys(call, offset).await
            }
            ControlTaskOperation::TaskSelf => self.tasks_self(call, offset).await,
        }
    }
}

impl PgControlTaskStore {
    async fn midjourneys(
        &self,
        call: ControlTaskCall,
        offset: i64,
    ) -> Result<Value, ControlTaskStoreError> {
        let self_user = match call.operation {
            ControlTaskOperation::MidjourneySelf => Some(principal_user_id(&call.principal)?),
            ControlTaskOperation::MidjourneyAll => None,
            ControlTaskOperation::TaskSelf => return Err(ControlTaskStoreError::Unavailable),
        };
        // `/api/mj/self` never accepted the administrator-only channel filter.
        let channel_id = matches!(call.operation, ControlTaskOperation::MidjourneyAll)
            .then(|| filter_i64(&call.query.filters, "channel_id"))
            .flatten();
        let mj_id = filter(&call.query.filters, "mj_id");
        let start = filter_i64(&call.query.filters, "start_timestamp");
        let end = filter_i64(&call.query.filters, "end_timestamp");
        let rows = sqlx::query(
            "SELECT jsonb_build_object(\
                'id', id, 'code', COALESCE(code, 0), 'user_id', COALESCE(user_id, 0),\
                'action', COALESCE(action, ''), 'mj_id', COALESCE(mj_id, ''),\
                'prompt', COALESCE(prompt, ''), 'prompt_en', COALESCE(prompt_en, ''),\
                'description', COALESCE(description, ''), 'state', COALESCE(state, ''),\
                'submit_time', COALESCE(submit_time, 0), 'start_time', COALESCE(start_time, 0),\
                'finish_time', COALESCE(finish_time, 0), 'image_url', COALESCE(image_url, ''),\
                'video_url', COALESCE(video_url, ''), 'video_urls', COALESCE(video_urls, ''),\
                'status', COALESCE(status, ''), 'progress', COALESCE(progress, ''),\
                'fail_reason', COALESCE(fail_reason, ''), 'channel_id', COALESCE(channel_id, 0),\
                'quota', COALESCE(quota, 0), 'buttons', COALESCE(buttons, ''),\
                'properties', COALESCE(properties, '')\
             ) FROM midjourneys \
             WHERE ($1::bigint IS NULL OR user_id = $1) \
               AND ($2::bigint IS NULL OR channel_id = $2) \
               AND ($3 = '' OR mj_id = $3) \
               AND ($4::bigint IS NULL OR submit_time >= $4) \
               AND ($5::bigint IS NULL OR submit_time <= $5) \
             ORDER BY id DESC LIMIT $6 OFFSET $7",
        )
        .bind(self_user)
        .bind(channel_id)
        .bind(mj_id)
        .bind(start)
        .bind(end)
        .bind(call.query.page_size)
        .bind(offset)
        .fetch_all(&self.pg)
        .await
        .map_err(|error| {
            tracing::warn!(%error, operation = "midjourney-list", "control task query failed");
            ControlTaskStoreError::Unavailable
        })?;
        let total = sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*) FROM midjourneys \
             WHERE ($1::bigint IS NULL OR user_id = $1) \
               AND ($2::bigint IS NULL OR channel_id = $2) \
               AND ($3 = '' OR mj_id = $3) \
               AND ($4::bigint IS NULL OR submit_time >= $4) \
               AND ($5::bigint IS NULL OR submit_time <= $5)",
        )
        .bind(self_user)
        .bind(channel_id)
        .bind(mj_id)
        .bind(start)
        .bind(end)
        .fetch_one(&self.pg)
        .await
        .map_err(|error| {
            tracing::warn!(%error, operation = "midjourney-count", "control task count failed");
            ControlTaskStoreError::Unavailable
        })?;
        Ok(page_payload(call.query, rows_to_values(rows)?, total))
    }

    async fn tasks_self(
        &self,
        call: ControlTaskCall,
        offset: i64,
    ) -> Result<Value, ControlTaskStoreError> {
        let user_id = principal_user_id(&call.principal)?;
        let platform = filter(&call.query.filters, "platform");
        let task_id = filter(&call.query.filters, "task_id");
        let status = filter(&call.query.filters, "status");
        let action = filter(&call.query.filters, "action");
        let start = filter_i64(&call.query.filters, "start_timestamp");
        let end = filter_i64(&call.query.filters, "end_timestamp");
        // Go deliberately omits `channel_id` from a user's task response.
        let rows = sqlx::query(
            "SELECT jsonb_build_object(\
                'id', id, 'created_at', COALESCE(created_at, 0),\
                'updated_at', COALESCE(updated_at, 0), 'task_id', COALESCE(task_id, ''),\
                'platform', COALESCE(platform, ''), 'user_id', COALESCE(user_id, 0),\
                'group', COALESCE(\"group\", ''), 'quota', COALESCE(quota, 0),\
                'action', COALESCE(action, ''), 'status', COALESCE(status, ''),\
                'fail_reason', COALESCE(fail_reason, ''), 'submit_time', COALESCE(submit_time, 0),\
                'start_time', COALESCE(start_time, 0), 'finish_time', COALESCE(finish_time, 0),\
                'progress', COALESCE(progress, ''), 'properties', properties, 'data', data\
             ) FROM tasks \
             WHERE user_id = $1 AND ($2 = '' OR platform = $2) \
               AND ($3 = '' OR task_id = $3) AND ($4 = '' OR status = $4) \
               AND ($5 = '' OR action = $5) \
               AND ($6::bigint IS NULL OR submit_time >= $6) \
               AND ($7::bigint IS NULL OR submit_time <= $7) \
             ORDER BY id DESC LIMIT $8 OFFSET $9",
        )
        .bind(user_id)
        .bind(platform)
        .bind(task_id)
        .bind(status)
        .bind(action)
        .bind(start)
        .bind(end)
        .bind(call.query.page_size)
        .bind(offset)
        .fetch_all(&self.pg)
        .await
        .map_err(|error| {
            tracing::warn!(%error, operation = "task-list", "control task query failed");
            ControlTaskStoreError::Unavailable
        })?;
        let total = sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*) FROM tasks WHERE user_id = $1 AND ($2 = '' OR platform = $2) \
             AND ($3 = '' OR task_id = $3) AND ($4 = '' OR status = $4) \
             AND ($5 = '' OR action = $5) \
             AND ($6::bigint IS NULL OR submit_time >= $6) \
             AND ($7::bigint IS NULL OR submit_time <= $7)",
        )
        .bind(user_id)
        .bind(platform)
        .bind(task_id)
        .bind(status)
        .bind(action)
        .bind(start)
        .bind(end)
        .fetch_one(&self.pg)
        .await
        .map_err(|error| {
            tracing::warn!(%error, operation = "task-count", "control task count failed");
            ControlTaskStoreError::Unavailable
        })?;
        Ok(page_payload(call.query, rows_to_values(rows)?, total))
    }
}

/// Error emitted by the storage dependency.
#[derive(Clone, Copy, Debug, Error, Eq, PartialEq)]
pub enum ControlTaskStoreError {
    /// The backing database or row decoding is unavailable.
    #[error("database error")]
    Unavailable,
}

/// Error emitted by the process status dependency.
#[derive(Clone, Copy, Debug, Error, Eq, PartialEq)]
pub enum ControlTaskStatusError {
    /// The database health check failed.
    #[error("数据库连接失败")]
    DatabaseUnavailable,
    /// Process counters cannot be safely read.
    #[error("http statistics unavailable")]
    HttpStatsUnavailable,
}

/// State for the independently mountable control-task slice.
#[derive(Clone)]
pub struct MissingControlTasksState {
    store: Arc<dyn ControlTaskStore>,
    authorizer: Arc<dyn ObservabilityAuthorizer>,
    status: Arc<dyn ControlTaskStatusProbe>,
    console_access_auth: Option<Arc<dyn DashboardAuth>>,
}

impl MissingControlTasksState {
    /// Builds route state from application-owned persistence, authorization,
    /// and process-health adapters.
    #[must_use]
    pub fn new(
        store: Arc<dyn ControlTaskStore>,
        authorizer: Arc<dyn ObservabilityAuthorizer>,
        status: Arc<dyn ControlTaskStatusProbe>,
    ) -> Self {
        Self {
            store,
            authorizer,
            status,
            console_access_auth: None,
        }
    }

    /// Enables the Go `ConsoleAccessGate` for the normal listener. Candidate
    /// tests leave this disabled so they can exercise direct route auth.
    #[must_use]
    pub fn with_console_access_gate(mut self, auth: Arc<dyn DashboardAuth>) -> Self {
        self.console_access_auth = Some(auth);
        self
    }
}

/// Builds the four remaining control-plane route candidates.
///
/// The caller should merge this only into the migration test root until its
/// concrete status counter adapter is accepted for production ownership.
pub fn missing_control_tasks_router(state: MissingControlTasksState) -> Router {
    let routes = Router::new()
        .route("/api/mj/", get(all_midjourney))
        .route("/api/mj/self", get(self_midjourney))
        .route("/api/task/self", get(self_tasks))
        .route("/api/status/test", get(test_status));
    let routes = if state.console_access_auth.is_some() {
        routes.layer(middleware::from_fn_with_state(
            state.clone(),
            console_access_boundary,
        ))
    } else {
        routes
    };
    routes.with_state(state)
}

async fn console_access_boundary(
    State(state): State<MissingControlTasksState>,
    request: Request,
    next: Next,
) -> Response {
    if !control_task_discovery_route(request.uri().path()) {
        return next.run(request).await;
    }
    let Some(auth) = state.console_access_auth.as_ref() else {
        return next.run(request).await;
    };
    let Some(token) = crate::migration_routes::legacy_http::dashboard_credential(request.headers())
    else {
        return console_not_found();
    };
    let user = match auth
        .self_user_view_for_optional(SecretString::from(token))
        .await
    {
        Ok(user) => user,
        Err(_) => return console_not_found(),
    };
    if !user.developer_access_granted {
        return console_not_found();
    }
    next.run(request).await
}

fn control_task_discovery_route(path: &str) -> bool {
    ["/api/mj", "/api/task", "/api/status/test"]
        .iter()
        .any(|prefix| {
            path == *prefix
                || path
                    .strip_prefix(prefix)
                    .is_some_and(|suffix| suffix.starts_with('/'))
        })
}

fn console_not_found() -> Response {
    (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response()
}

async fn all_midjourney(
    State(state): State<MissingControlTasksState>,
    headers: HeaderMap,
    raw: RawQuery,
) -> Response {
    list(
        &state,
        &headers,
        raw,
        ObservabilityAccess::Admin,
        ControlTaskOperation::MidjourneyAll,
    )
    .await
}

async fn self_midjourney(
    State(state): State<MissingControlTasksState>,
    headers: HeaderMap,
    raw: RawQuery,
) -> Response {
    list(
        &state,
        &headers,
        raw,
        ObservabilityAccess::User,
        ControlTaskOperation::MidjourneySelf,
    )
    .await
}

async fn self_tasks(
    State(state): State<MissingControlTasksState>,
    headers: HeaderMap,
    raw: RawQuery,
) -> Response {
    list(
        &state,
        &headers,
        raw,
        ObservabilityAccess::User,
        ControlTaskOperation::TaskSelf,
    )
    .await
}

async fn list(
    state: &MissingControlTasksState,
    headers: &HeaderMap,
    raw: RawQuery,
    access: ObservabilityAccess,
    operation: ControlTaskOperation,
) -> Response {
    let principal = match authorize(&state.authorizer, headers, access).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    match state
        .store
        .list(ControlTaskCall {
            operation,
            principal,
            query: parse_query(raw),
        })
        .await
    {
        Ok(data) => success(data),
        Err(error) => failure(StatusCode::INTERNAL_SERVER_ERROR, error.to_string()),
    }
}

async fn test_status(
    State(state): State<MissingControlTasksState>,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = authorize(&state.authorizer, &headers, ObservabilityAccess::Admin).await
    {
        return response;
    }
    match state.status.test_status().await {
        Ok(http_stats) => Json(json!({
            "success": true,
            "message": "Server is running",
            "http_stats": http_stats,
        }))
        .into_response(),
        Err(ControlTaskStatusError::DatabaseUnavailable) => (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(json!({
                "success": false,
                "message": "数据库连接失败",
            })),
        )
            .into_response(),
        // A test instance without an observable process statistics collector
        // must fail closed rather than claim a successful status with invented
        // zero counters.
        Err(ControlTaskStatusError::HttpStatsUnavailable) => (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(json!({
                "success": false,
                "message": "HTTP统计信息不可用",
            })),
        )
            .into_response(),
    }
}

async fn authorize(
    authorizer: &Arc<dyn ObservabilityAuthorizer>,
    headers: &HeaderMap,
    access: ObservabilityAccess,
) -> Result<ObservabilityPrincipal, Response> {
    let principal = authorizer
        .authorize(headers, access)
        .await
        .map_err(|_| unauthorized())?;
    let allowed = match (&principal, access) {
        (ObservabilityPrincipal::User { role, .. }, ObservabilityAccess::Admin) => {
            *role >= ADMIN_ROLE
        }
        (ObservabilityPrincipal::User { role, .. }, ObservabilityAccess::Root) => {
            *role >= ROOT_ROLE
        }
        (ObservabilityPrincipal::User { user_id, role, .. }, ObservabilityAccess::User) => {
            *user_id > 0 && *role >= USER_ROLE
        }
        _ => false,
    };
    allowed.then_some(principal).ok_or_else(forbidden)
}

fn principal_user_id(principal: &ObservabilityPrincipal) -> Result<i64, ControlTaskStoreError> {
    match principal {
        ObservabilityPrincipal::User { user_id, .. } if *user_id > 0 => Ok(*user_id),
        _ => Err(ControlTaskStoreError::Unavailable),
    }
}

fn parse_query(raw: RawQuery) -> ControlTaskQuery {
    let filters = raw.0.map_or_else(BTreeMap::new, |raw| {
        raw.split('&')
            .filter_map(|part| {
                let (key, value) = part.split_once('=').unwrap_or((part, ""));
                Some((decode_query(key)?, decode_query(value)?))
            })
            .collect::<BTreeMap<_, _>>()
    });
    // This deliberately preserves `common.GetPageQuery`: a negative `p` is
    // retained rather than silently normalized, while zero/missing means 1.
    let supplied_page = filter_i64(&filters, "p").unwrap_or(0);
    let page = if supplied_page < 1 {
        if supplied_page != 0 { supplied_page } else { 1 }
    } else {
        supplied_page
    };
    let page_size = filter_i64(&filters, "page_size")
        .or_else(|| filter_i64(&filters, "ps"))
        .or_else(|| filter_i64(&filters, "size"))
        .filter(|size| *size != 0)
        .unwrap_or(DEFAULT_PAGE_SIZE)
        .min(MAX_PAGE_SIZE);
    ControlTaskQuery {
        page,
        page_size,
        filters,
    }
}

fn decode_query(value: &str) -> Option<String> {
    let mut bytes = Vec::with_capacity(value.len());
    let source = value.as_bytes();
    let mut index = 0;
    while index < source.len() {
        match source[index] {
            b'+' => bytes.push(b' '),
            b'%' if index + 2 < source.len() => {
                let high = (source[index + 1] as char).to_digit(16)?;
                let low = (source[index + 2] as char).to_digit(16)?;
                bytes.push((high * 16 + low) as u8);
                index += 2;
            }
            b'%' => return None,
            byte => bytes.push(byte),
        }
        index += 1;
    }
    String::from_utf8(bytes).ok()
}

fn filter<'a>(filters: &'a BTreeMap<String, String>, key: &str) -> &'a str {
    filters.get(key).map_or("", String::as_str)
}

fn filter_i64(filters: &BTreeMap<String, String>, key: &str) -> Option<i64> {
    filters.get(key)?.parse().ok()
}

fn rows_to_values(rows: Vec<sqlx::postgres::PgRow>) -> Result<Value, ControlTaskStoreError> {
    rows.into_iter()
        .map(|row| {
            row.try_get::<Value, _>(0)
                .map_err(|_| ControlTaskStoreError::Unavailable)
        })
        .collect::<Result<Vec<_>, _>>()
        .map(Value::Array)
}

fn page_payload(query: ControlTaskQuery, items: Value, total: i64) -> Value {
    json!({
        "page": query.page,
        "page_size": query.page_size,
        "total": total,
        "items": items,
    })
}

#[derive(Serialize)]
struct LegacyEnvelope<T: Serialize> {
    success: bool,
    message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<T>,
}

fn success(data: Value) -> Response {
    Json(LegacyEnvelope {
        success: true,
        message: String::new(),
        data: Some(data),
    })
    .into_response()
}

fn failure(status: StatusCode, message: impl Into<String>) -> Response {
    (
        status,
        Json(LegacyEnvelope::<Value> {
            success: false,
            message: message.into(),
            data: None,
        }),
    )
        .into_response()
}

fn unauthorized() -> Response {
    (
        StatusCode::UNAUTHORIZED,
        Json(json!({
            "success": false,
            "code": "AUTH_UNAUTHORIZED",
            "message": "Unauthorized, invalid access token",
        })),
    )
        .into_response()
}

fn forbidden() -> Response {
    (
        StatusCode::FORBIDDEN,
        Json(json!({
            "success": false,
            "code": "AUTH_INSUFFICIENT_PRIVILEGE",
            "message": "管理员权限不足",
        })),
    )
        .into_response()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::auth::{
        AuthBundle, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardUser,
        LoginOutcome, LoginRequest, LogoutRequest, LogoutResult, RequestMetadata,
        TwoFactorLoginRequest,
    };
    use crate::migration_routes::observability::ObservabilityAuthError;
    use axum::{
        body::{Body, to_bytes},
        http::Request,
    };
    use std::sync::atomic::{AtomicUsize, Ordering};
    use tower::ServiceExt;

    struct StaticAuthorizer(ObservabilityPrincipal);

    #[async_trait]
    impl ObservabilityAuthorizer for StaticAuthorizer {
        async fn authorize(
            &self,
            _: &HeaderMap,
            _: ObservabilityAccess,
        ) -> Result<ObservabilityPrincipal, ObservabilityAuthError> {
            Ok(self.0.clone())
        }
    }

    struct RejectingAuthorizer;

    #[async_trait]
    impl ObservabilityAuthorizer for RejectingAuthorizer {
        async fn authorize(
            &self,
            _: &HeaderMap,
            _: ObservabilityAccess,
        ) -> Result<ObservabilityPrincipal, ObservabilityAuthError> {
            Err(ObservabilityAuthError::Unauthorized)
        }
    }

    struct GateAuth {
        user: DashboardUser,
    }

    #[async_trait]
    impl DashboardAuth for GateAuth {
        async fn check_critical_rate_limit(
            &self,
            _: &str,
        ) -> Result<CriticalRateLimitOutcome, AuthError> {
            Ok(CriticalRateLimitOutcome::Allowed)
        }

        async fn login(
            &self,
            _: LoginRequest,
            _: RequestMetadata,
        ) -> Result<LoginOutcome, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn login_2fa(
            &self,
            _: TwoFactorLoginRequest,
            _: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn refresh(
            &self,
            _: SecretString,
            _: Option<String>,
            _: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn self_user(&self, _: SecretString) -> Result<DashboardUser, AuthError> {
            Ok(self.user.clone())
        }

        async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn generate_personal_access_token(
            &self,
            _: SecretString,
        ) -> Result<String, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }
    }

    fn dashboard_user(role: i64) -> DashboardUser {
        DashboardUser {
            id: 7,
            username: "member".to_owned(),
            display_name: String::new(),
            role,
            status: 1,
            email: String::new(),
            github_id: String::new(),
            discord_id: String::new(),
            oidc_id: String::new(),
            wechat_id: String::new(),
            telegram_id: String::new(),
            group: String::new(),
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
            sidebar_modules: Value::Null,
            permissions: Value::Null,
        }
    }

    struct CountingStore(AtomicUsize);

    #[async_trait]
    impl ControlTaskStore for CountingStore {
        async fn list(&self, call: ControlTaskCall) -> Result<Value, ControlTaskStoreError> {
            self.0.fetch_add(1, Ordering::Relaxed);
            Ok(json!({
                "page": call.query.page,
                "page_size": call.query.page_size,
                "total": 0,
                "items": [],
            }))
        }
    }

    struct StaticStatus(Result<Value, ControlTaskStatusError>);

    #[async_trait]
    impl ControlTaskStatusProbe for StaticStatus {
        async fn test_status(&self) -> Result<Value, ControlTaskStatusError> {
            self.0.clone()
        }
    }

    struct CountingStatus(AtomicUsize);

    #[async_trait]
    impl ControlTaskStatusProbe for CountingStatus {
        async fn test_status(&self) -> Result<Value, ControlTaskStatusError> {
            self.0.fetch_add(1, Ordering::Relaxed);
            Ok(json!({}))
        }
    }

    fn member() -> ObservabilityPrincipal {
        ObservabilityPrincipal::User {
            user_id: 7,
            username: "member".to_owned(),
            role: 1,
        }
    }

    fn router_for(
        principal: ObservabilityPrincipal,
        store: Arc<CountingStore>,
        status: Result<Value, ControlTaskStatusError>,
    ) -> Router {
        missing_control_tasks_router(MissingControlTasksState::new(
            store,
            Arc::new(StaticAuthorizer(principal)),
            Arc::new(StaticStatus(status)),
        ))
    }

    type TestResult<T = ()> = Result<T, Box<dyn std::error::Error>>;

    async fn body(response: Response) -> TestResult<Value> {
        let bytes = to_bytes(response.into_body(), usize::MAX).await?;
        Ok(serde_json::from_slice(&bytes)?)
    }

    #[tokio::test]
    async fn self_task_lists_are_user_scoped_and_keep_legacy_page_aliases() -> TestResult {
        let store = Arc::new(CountingStore(AtomicUsize::new(0)));
        let app = router_for(member(), store.clone(), Ok(json!({"requests": 3})));
        let response = app
            .oneshot(Request::get("/api/task/self?p=2&ps=101").body(Body::empty())?)
            .await?;
        assert_eq!(response.status(), StatusCode::OK);
        let payload = body(response).await?;
        assert_eq!(payload["success"], true);
        assert_eq!(payload["data"]["page"], 2);
        assert_eq!(payload["data"]["page_size"], 100);
        assert_eq!(store.0.load(Ordering::Relaxed), 1);
        Ok(())
    }

    #[tokio::test]
    async fn member_cannot_read_administrator_midjourney_list_or_status() -> TestResult {
        let store = Arc::new(CountingStore(AtomicUsize::new(0)));
        let app = router_for(member(), store.clone(), Ok(json!({})));
        let mj = app
            .clone()
            .oneshot(Request::get("/api/mj/").body(Body::empty())?)
            .await?;
        assert_eq!(mj.status(), StatusCode::FORBIDDEN);
        let status = app
            .oneshot(Request::get("/api/status/test").body(Body::empty())?)
            .await?;
        assert_eq!(status.status(), StatusCode::FORBIDDEN);
        assert_eq!(store.0.load(Ordering::Relaxed), 0);
        Ok(())
    }

    #[tokio::test]
    async fn unauthenticated_control_routes_preserve_go_auth_envelope_before_dependencies()
    -> TestResult {
        let store = Arc::new(CountingStore(AtomicUsize::new(0)));
        let status = Arc::new(CountingStatus(AtomicUsize::new(0)));
        let app = missing_control_tasks_router(MissingControlTasksState::new(
            store.clone(),
            Arc::new(RejectingAuthorizer),
            status.clone(),
        ));

        for path in [
            "/api/mj/",
            "/api/mj/self",
            "/api/task/self",
            "/api/status/test",
        ] {
            let response = app
                .clone()
                .oneshot(Request::get(path).body(Body::empty())?)
                .await?;
            assert_eq!(response.status(), StatusCode::UNAUTHORIZED, "{path}");
            assert_eq!(
                response
                    .headers()
                    .get("content-type")
                    .and_then(|value| value.to_str().ok()),
                Some("application/json"),
                "{path}"
            );
            assert_eq!(
                body(response).await?,
                json!({
                    "success": false,
                    "code": "AUTH_UNAUTHORIZED",
                    "message": "Unauthorized, invalid access token",
                }),
                "{path}"
            );
        }
        assert_eq!(store.0.load(Ordering::Relaxed), 0);
        assert_eq!(status.0.load(Ordering::Relaxed), 0);
        Ok(())
    }

    #[tokio::test]
    async fn console_gate_hides_task_discovery_from_anonymous_and_l0() -> TestResult {
        let store = Arc::new(CountingStore(AtomicUsize::new(0)));
        let status = Arc::new(CountingStatus(AtomicUsize::new(0)));
        let l0_auth: Arc<dyn DashboardAuth> = Arc::new(GateAuth {
            user: dashboard_user(USER_ROLE),
        });
        let app = missing_control_tasks_router(
            MissingControlTasksState::new(
                store.clone(),
                Arc::new(StaticAuthorizer(member())),
                status.clone(),
            )
            .with_console_access_gate(l0_auth),
        );

        for request in [
            Request::get("/api/mj/").body(Body::empty())?,
            Request::get("/api/task/self")
                .header("authorization", "Bearer dashboard")
                .body(Body::empty())?,
            Request::get("/api/status/test")
                .header("authorization", "Bearer dashboard")
                .body(Body::empty())?,
        ] {
            let response = app.clone().oneshot(request).await?;
            assert_eq!(response.status(), StatusCode::NOT_FOUND);
            assert_eq!(body(response).await?["message"], "Not Found");
        }
        assert_eq!(store.0.load(Ordering::Relaxed), 0);
        assert_eq!(status.0.load(Ordering::Relaxed), 0);

        let root_auth: Arc<dyn DashboardAuth> = Arc::new(GateAuth {
            user: dashboard_user(ROOT_ROLE),
        });
        let root = missing_control_tasks_router(
            MissingControlTasksState::new(
                store.clone(),
                Arc::new(StaticAuthorizer(ObservabilityPrincipal::User {
                    user_id: 1,
                    username: "root".to_owned(),
                    role: ROOT_ROLE,
                })),
                status.clone(),
            )
            .with_console_access_gate(root_auth),
        );
        let response = root
            .oneshot(
                Request::get("/api/status/test")
                    .header("authorization", "Bearer dashboard")
                    .body(Body::empty())?,
            )
            .await?;
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(status.0.load(Ordering::Relaxed), 1);
        Ok(())
    }

    #[tokio::test]
    async fn administrator_status_preserves_go_database_failure_payload() -> TestResult {
        let store = Arc::new(CountingStore(AtomicUsize::new(0)));
        let admin = ObservabilityPrincipal::User {
            user_id: 1,
            username: "admin".to_owned(),
            role: ADMIN_ROLE,
        };
        let app = router_for(
            admin,
            store,
            Err(ControlTaskStatusError::DatabaseUnavailable),
        );
        let response = app
            .oneshot(Request::get("/api/status/test").body(Body::empty())?)
            .await?;
        assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
        let payload = body(response).await?;
        assert_eq!(
            payload,
            json!({"success": false, "message": "数据库连接失败"})
        );
        Ok(())
    }

    #[tokio::test]
    async fn administrator_status_keeps_http_stats_at_the_legacy_single_level() -> TestResult {
        let store = Arc::new(CountingStore(AtomicUsize::new(0)));
        let admin = ObservabilityPrincipal::User {
            user_id: 1,
            username: "admin".to_owned(),
            role: ADMIN_ROLE,
        };
        let app = router_for(
            admin,
            store,
            Ok(json!({"request_count": 3, "error_count": 1})),
        );
        let response = app
            .oneshot(Request::get("/api/status/test").body(Body::empty())?)
            .await?;
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            body(response).await?,
            json!({
                "success": true,
                "message": "Server is running",
                "http_stats": {"request_count": 3, "error_count": 1},
            })
        );
        Ok(())
    }

    #[tokio::test]
    async fn administrator_status_fails_closed_when_stats_are_unobservable() -> TestResult {
        let store = Arc::new(CountingStore(AtomicUsize::new(0)));
        let admin = ObservabilityPrincipal::User {
            user_id: 1,
            username: "admin".to_owned(),
            role: ADMIN_ROLE,
        };
        let app = router_for(
            admin,
            store,
            Err(ControlTaskStatusError::HttpStatsUnavailable),
        );
        let response = app
            .oneshot(Request::get("/api/status/test").body(Body::empty())?)
            .await?;
        assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(
            body(response).await?,
            json!({"success": false, "message": "HTTP统计信息不可用"})
        );
        Ok(())
    }
}
