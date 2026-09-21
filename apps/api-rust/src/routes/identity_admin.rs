//! Legacy-compatible administrator user-management routes.

use crate::auth::{DashboardAuth, DashboardUser};
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{Extension, Path, Query, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use bcrypt::{DEFAULT_COST, hash};
use secrecy::SecretString;
use serde::{Deserialize, Serialize, de::DeserializeOwned};
use serde_json::json;
use sqlx::{FromRow, PgPool, Postgres, Row, Transaction, postgres::PgRow};
use std::sync::Arc;

const ROLE_ADMIN: i64 = 10;
const ROLE_ROOT: i64 = 100;
const STATUS_ENABLED: i64 = 1;
const STATUS_DISABLED: i64 = 2;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const MAX_BODY_BYTES: usize = 1 << 20;
// A pending fence must outlive the normal user-cache TTL.  If Valkey becomes
// unavailable after a database commit, retaining this fence fails closed until
// the cache can recover from PostgreSQL rather than re-authorizing an old token.
const AUTH_FENCE_TTL_SECONDS: u64 = 120;

/// Dependencies for user-management routes.
#[derive(Clone)]
pub struct IdentityAdminState {
    pool: PgPool,
    valkey: redis::Client,
    auth: Arc<dyn DashboardAuth>,
}

impl IdentityAdminState {
    pub fn new(pool: PgPool, valkey: redis::Client, auth: Arc<dyn DashboardAuth>) -> Self {
        Self { pool, valkey, auth }
    }
}

/// Routes migrated from `controller/user.go`: list/search/detail/create/update/delete/manage.
pub fn router(state: IdentityAdminState) -> Router {
    Router::new()
        .route(
            "/api/user/",
            get(list_users)
                .post(create_user)
                .put(update_user)
                .route_layer(middleware::from_fn_with_state(
                    state.clone(),
                    admin_auth_boundary,
                )),
        )
        .route(
            "/api/user/search",
            get(search_users).route_layer(middleware::from_fn_with_state(
                state.clone(),
                admin_auth_boundary,
            )),
        )
        .route(
            "/api/user/{id}",
            get(get_user)
                .delete(delete_user)
                .route_layer(middleware::from_fn_with_state(
                    state.clone(),
                    admin_auth_boundary,
                )),
        )
        .route(
            "/api/user/manage",
            post(manage_user).route_layer(middleware::from_fn_with_state(
                state.clone(),
                admin_auth_boundary,
            )),
        )
        .with_state(state)
}

async fn admin_auth_boundary(
    State(state): State<IdentityAdminState>,
    mut request: Request,
    next: Next,
) -> Response {
    let actor = match administrator(&state, request.headers()).await {
        Ok(actor) => actor,
        Err(response) => return response,
    };
    request.extensions_mut().insert(actor);
    let mut response = next.run(request).await;
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

#[derive(Serialize)]
struct Envelope<T: Serialize> {
    success: bool,
    message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<T>,
}

fn ok<T: Serialize>(data: Option<T>) -> Response {
    authenticated_response(
        Json(Envelope {
            success: true,
            message: String::new(),
            data,
        })
        .into_response(),
    )
}
fn fail(message: impl Into<String>) -> Response {
    authenticated_response(
        Json(Envelope::<()> {
            success: false,
            message: message.into(),
            data: None,
        })
        .into_response(),
    )
}
fn authenticated_response(mut response: Response) -> Response {
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

async fn parse_json_body<T: DeserializeOwned>(request: Request) -> Result<T, Response> {
    let body = to_bytes(request.into_body(), MAX_BODY_BYTES)
        .await
        .map_err(|_| fail("参数错误"))?;
    let mut decoder = serde_json::Deserializer::from_slice(&body);
    T::deserialize(&mut decoder).map_err(|_| fail("参数错误"))
}

fn unauthorized() -> Response {
    (
        StatusCode::UNAUTHORIZED,
        Json(serde_json::json!({
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
        Json(serde_json::json!({
            "success": false,
            "code": "AUTH_INSUFFICIENT_PRIVILEGE",
            "message": "Unauthorized, insufficient privileges",
        })),
    )
        .into_response()
}
fn internal() -> Response {
    fail("internal server error")
}

async fn administrator(
    state: &IdentityAdminState,
    headers: &HeaderMap,
) -> Result<DashboardUser, Response> {
    let Some(token) = bearer(headers) else {
        return Err(unauthorized());
    };
    let user = state
        .auth
        .self_user(SecretString::from(token))
        .await
        .map_err(|_| unauthorized())?;
    if user.status != STATUS_ENABLED {
        return Err(unauthorized());
    }
    if user.role < ROLE_ADMIN {
        return Err(forbidden());
    }
    if user.id <= 0
        || user.username.trim().is_empty()
        || !matches!(user.role, 0 | 1 | ROLE_ADMIN | ROLE_ROOT)
    {
        return Err(unauthorized());
    }
    Ok(user)
}

fn bearer(headers: &HeaderMap) -> Option<String> {
    let value = headers.get(header::AUTHORIZATION)?.to_str().ok()?;
    let mut words = value.split_whitespace();
    let scheme = words.next()?;
    let token = words.next()?;
    (scheme.eq_ignore_ascii_case("bearer") && !token.is_empty() && words.next().is_none())
        .then(|| token.to_owned())
}

#[derive(Debug, Deserialize)]
struct PageQuery {
    p: Option<i64>,
    page_size: Option<i64>,
    ps: Option<i64>,
    size: Option<i64>,
    sort_by: Option<String>,
    sort_order: Option<String>,
}
impl PageQuery {
    fn page(&self) -> i64 {
        self.p.unwrap_or(1).max(1)
    }
    fn page_size(&self) -> i64 {
        self.page_size
            .or(self.ps)
            .or(self.size)
            .unwrap_or(10)
            .clamp(1, 100)
    }
    fn order(&self) -> &'static str {
        match self
            .sort_by
            .as_deref()
            .map(str::trim)
            .map(str::to_ascii_lowercase)
            .as_deref()
        {
            Some("username") => "username",
            Some("quota") => "quota",
            Some("group") => "\"group\"",
            Some("created_at") => "created_at",
            Some("last_login_at") => "last_login_at",
            _ => "id",
        }
    }
    fn direction(&self) -> &'static str {
        if self
            .sort_order
            .as_deref()
            .is_some_and(|v| v.eq_ignore_ascii_case("asc"))
        {
            "ASC"
        } else {
            "DESC"
        }
    }
}

#[derive(Debug, Deserialize)]
struct SearchQuery {
    #[serde(flatten)]
    page: PageQuery,
    keyword: Option<String>,
    group: Option<String>,
    role: Option<i64>,
    status: Option<i64>,
}

#[derive(Debug, Serialize)]
struct UserView {
    id: i64,
    username: String,
    display_name: String,
    role: i64,
    status: i64,
    email: String,
    github_id: String,
    discord_id: String,
    oidc_id: String,
    wechat_id: String,
    telegram_id: String,
    quota: i64,
    used_quota: i64,
    request_count: i64,
    group: String,
    aff_code: String,
    aff_count: i64,
    aff_quota: i64,
    aff_history_quota: i64,
    inviter_id: i64,
    linux_do_id: String,
    remark: String,
    created_at: i64,
    last_login_at: i64,
    #[serde(rename = "DeletedAt")]
    deleted_at: Option<String>,
}

impl<'r> FromRow<'r, PgRow> for UserView {
    fn from_row(row: &'r PgRow) -> Result<Self, sqlx::Error> {
        Ok(Self {
            id: row.try_get("id")?,
            username: row.try_get("username")?,
            display_name: row.try_get("display_name")?,
            role: row.try_get("role")?,
            status: row.try_get("status")?,
            email: row.try_get("email")?,
            github_id: row.try_get("github_id")?,
            discord_id: row.try_get("discord_id")?,
            oidc_id: row.try_get("oidc_id")?,
            wechat_id: row.try_get("wechat_id")?,
            telegram_id: row.try_get("telegram_id")?,
            quota: row.try_get("quota")?,
            used_quota: row.try_get("used_quota")?,
            request_count: row.try_get("request_count")?,
            group: row.try_get("group")?,
            aff_code: row.try_get("aff_code")?,
            aff_count: row.try_get("aff_count")?,
            aff_quota: row.try_get("aff_quota")?,
            aff_history_quota: row.try_get("aff_history_quota")?,
            inviter_id: row.try_get("inviter_id")?,
            linux_do_id: row.try_get("linux_do_id")?,
            remark: row.try_get("remark")?,
            created_at: row.try_get("created_at")?,
            last_login_at: row.try_get("last_login_at")?,
            deleted_at: row.try_get("deleted_at")?,
        })
    }
}

const USER_COLUMNS: &str = r#"id, COALESCE(username, '') AS username, COALESCE(display_name, '') AS display_name, COALESCE(role, 1) AS role, COALESCE(status, 1) AS status, COALESCE(email, '') AS email, COALESCE(github_id, '') AS github_id, COALESCE(discord_id, '') AS discord_id, COALESCE(oidc_id, '') AS oidc_id, COALESCE(wechat_id, '') AS wechat_id, COALESCE(telegram_id, '') AS telegram_id, COALESCE(quota, 0) AS quota, COALESCE(used_quota, 0) AS used_quota, COALESCE(request_count, 0) AS request_count, COALESCE("group", 'default') AS "group", COALESCE(aff_code, '') AS aff_code, COALESCE(aff_count, 0) AS aff_count, COALESCE(aff_quota, 0) AS aff_quota, COALESCE(aff_history, 0) AS aff_history_quota, COALESCE(inviter_id, 0) AS inviter_id, COALESCE(linux_do_id, '') AS linux_do_id, COALESCE(remark, '') AS remark, COALESCE(created_at, 0) AS created_at, COALESCE(last_login_at, 0) AS last_login_at, deleted_at::text AS deleted_at"#;

#[derive(Serialize)]
struct Page<T> {
    page: i64,
    page_size: i64,
    total: i64,
    items: Vec<T>,
}

async fn list_users(
    State(state): State<IdentityAdminState>,
    Extension(_actor): Extension<DashboardUser>,
    Query(query): Query<PageQuery>,
) -> Response {
    let page = query.page();
    let page_size = query.page_size();
    let offset = (page - 1) * page_size;
    let count = sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM users")
        .fetch_one(&state.pool)
        .await;
    let sql = format!(
        "SELECT {USER_COLUMNS} FROM users ORDER BY {} {}, id DESC LIMIT $1 OFFSET $2",
        query.order(),
        query.direction()
    );
    let users = sqlx::query_as::<_, UserView>(&sql)
        .bind(page_size)
        .bind(offset)
        .fetch_all(&state.pool)
        .await;
    match (count, users) {
        (Ok(total), Ok(items)) => ok(Some(Page {
            page,
            page_size,
            total,
            items,
        })),
        _ => internal(),
    }
}

async fn search_users(
    State(state): State<IdentityAdminState>,
    Extension(_actor): Extension<DashboardUser>,
    Query(query): Query<SearchQuery>,
) -> Response {
    let page = query.page.page();
    let page_size = query.page.page_size();
    let offset = (page - 1) * page_size;
    let keyword = query.keyword.unwrap_or_default();
    let keyword_id = keyword.parse::<i64>().ok();
    let pattern = format!("%{keyword}%");
    let where_sql = "(username ILIKE $1 OR email ILIKE $1 OR display_name ILIKE $1 OR id = COALESCE($2, -1)) AND ($3::text IS NULL OR \"group\" = $3) AND ($4::bigint IS NULL OR role = $4) AND ($5::bigint IS NULL OR CASE WHEN $5 = -1 THEN deleted_at IS NOT NULL ELSE deleted_at IS NULL AND status = $5 END)";
    let count_sql = format!("SELECT COUNT(*) FROM users WHERE {where_sql}");
    let list_sql = format!(
        "SELECT {USER_COLUMNS} FROM users WHERE {where_sql} ORDER BY {} {}, id DESC LIMIT $6 OFFSET $7",
        query.page.order(),
        query.page.direction()
    );
    let count = sqlx::query_scalar::<_, i64>(&count_sql)
        .bind(&pattern)
        .bind(keyword_id)
        .bind(query.group.as_deref())
        .bind(query.role)
        .bind(query.status)
        .fetch_one(&state.pool)
        .await;
    let users = sqlx::query_as::<_, UserView>(&list_sql)
        .bind(&pattern)
        .bind(keyword_id)
        .bind(query.group.as_deref())
        .bind(query.role)
        .bind(query.status)
        .bind(page_size)
        .bind(offset)
        .fetch_all(&state.pool)
        .await;
    match (count, users) {
        (Ok(total), Ok(items)) => ok(Some(Page {
            page,
            page_size,
            total,
            items,
        })),
        _ => internal(),
    }
}

async fn get_user(
    State(state): State<IdentityAdminState>,
    Extension(actor): Extension<DashboardUser>,
    Path(id): Path<i64>,
) -> Response {
    let sql = format!("SELECT {USER_COLUMNS} FROM users WHERE id = $1 AND deleted_at IS NULL");
    match sqlx::query_as::<_, UserView>(&sql)
        .bind(id)
        .fetch_optional(&state.pool)
        .await
    {
        Ok(Some(user)) if can_manage(actor.role, user.role) => ok(Some(user)),
        Ok(Some(_)) => fail("无权操作同等级或更高等级用户"),
        Ok(None) => fail("record not found"),
        Err(_) => internal(),
    }
}

#[derive(Deserialize)]
struct UserInput {
    id: Option<i64>,
    #[serde(default)]
    username: String,
    password: Option<String>,
    display_name: Option<String>,
    role: Option<i64>,
    email: Option<String>,
    group: Option<String>,
    remark: Option<String>,
}

fn char_len(value: &str) -> usize {
    value.chars().count()
}

fn has_invalid_user_text(input: &UserInput) -> bool {
    input
        .display_name
        .as_deref()
        .is_some_and(|value| char_len(value) > 20)
        || input
            .email
            .as_deref()
            .is_some_and(|value| char_len(value) > 50)
        || input
            .remark
            .as_deref()
            .is_some_and(|value| char_len(value) > 255)
}

async fn create_user(
    State(state): State<IdentityAdminState>,
    Extension(actor): Extension<DashboardUser>,
    request: Request,
) -> Response {
    let mut input: UserInput = match parse_json_body(request).await {
        Ok(input) => input,
        Err(response) => return response,
    };
    input.username = input.username.trim().to_owned();
    input.password = input.password.filter(|password| !password.is_empty());
    let Some(password) = input.password.as_deref() else {
        return fail("参数错误");
    };
    if input.username.is_empty()
        || char_len(&input.username) > 20
        || char_len(password) < 8
        || char_len(password) > 20
        || has_invalid_user_text(&input)
    {
        return fail("用户输入不合法");
    }
    let role = input.role.unwrap_or(0);
    if role >= actor.role {
        return fail("无法创建权限大于等于自己的用户");
    }
    let password = match hash(password, DEFAULT_COST) {
        Ok(value) => value,
        Err(_) => return internal(),
    };
    let display_name = input
        .display_name
        .filter(|value| !value.is_empty())
        .unwrap_or_else(|| input.username.clone());
    let mut transaction = match state.pool.begin().await {
        Ok(transaction) => transaction,
        Err(_) => return internal(),
    };
    // The legacy admin-create path intentionally persists only this clean
    // subset; contact/group/remark fields in its request are validated but
    // never granted write authority by this endpoint.
    let id = match sqlx::query_scalar::<_, i64>("INSERT INTO users (username, password, display_name, role, status, \"group\", quota, aff_code, setting, created_at, auth_version) VALUES ($1, $2, $3, $4, 1, 'default', 0, substr(md5(random()::text), 1, 4), '{}', EXTRACT(EPOCH FROM NOW())::BIGINT, 1) RETURNING id")
        .bind(&input.username).bind(password).bind(&display_name).bind(role).fetch_one(&mut *transaction).await {
        Ok(id) => id,
        Err(_) => return fail("duplicate key value violates unique constraint"),
    };
    if record_manage_audit(
        &mut transaction,
        &actor,
        "user.create",
        format!("Created user {display_name} (role {role})"),
        json!({"username": display_name, "role": role, "target_user_id": id}),
    )
    .await
    .is_err()
        || transaction.commit().await.is_err()
    {
        return internal();
    }
    ok::<()>(None)
}

async fn update_user(
    State(state): State<IdentityAdminState>,
    Extension(actor): Extension<DashboardUser>,
    request: Request,
) -> Response {
    let mut input: UserInput = match parse_json_body(request).await {
        Ok(input) => input,
        Err(response) => return response,
    };
    let Some(id) = input.id.filter(|id| *id > 0) else {
        return fail("参数错误");
    };
    input.username = input.username.trim().to_owned();
    // Legacy UpdateUser replaces an empty password with a validation sentinel
    // and then restores it before persistence, meaning an explicit empty
    // password is a no-password-change request rather than an invalid hash.
    input.password = input.password.filter(|password| !password.is_empty());
    if input.username.is_empty()
        || char_len(&input.username) > 20
        || input
            .password
            .as_deref()
            .is_some_and(|password| char_len(password) < 8 || char_len(password) > 20)
        || has_invalid_user_text(&input)
    {
        return fail("用户输入不合法");
    }
    let mut transaction = match state.pool.begin().await {
        Ok(transaction) => transaction,
        Err(_) => return internal(),
    };
    let existing = sqlx::query_as::<_, UserView>(&format!(
        "SELECT {USER_COLUMNS} FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE"
    ))
    .bind(id)
    .fetch_optional(&mut *transaction)
    .await;
    let Some(existing) = (match existing {
        Ok(value) => value,
        Err(_) => return internal(),
    }) else {
        return fail("record not found");
    };
    if input
        .role
        .is_some_and(|role| role != 0 && role != existing.role)
    {
        return fail("参数错误");
    }
    if !can_manage(actor.role, existing.role) {
        return fail("无权操作权限更高的用户");
    }
    let password = match input.password {
        Some(value) => match hash(value, DEFAULT_COST) {
            Ok(hash) => Some(hash),
            Err(_) => return internal(),
        },
        None => None,
    };
    // GORM's map update receives zero values for omitted JSON strings here;
    // preserve that legacy full-replacement behavior rather than treating an
    // absent field as a partial PATCH.  Email is deliberately not updated:
    // legacy EditWithTx validates it but only writes the clean admin subset.
    let display_name = input.display_name.unwrap_or_default();
    let group = input.group.unwrap_or_default();
    let remark = input.remark.unwrap_or_default();
    let group_changed = group != existing.group;
    let changed_auth = password.is_some() || group_changed;
    let version = match sqlx::query_scalar::<_, i64>("UPDATE users SET username = $2, display_name = $3, password = COALESCE($4, password), \"group\" = $5, remark = $6, auth_version = auth_version + CASE WHEN $7 THEN 1 ELSE 0 END WHERE id = $1 RETURNING auth_version")
        .bind(id).bind(&input.username).bind(display_name).bind(password).bind(group).bind(remark).bind(changed_auth).fetch_one(&mut *transaction).await {
        Ok(version) => version,
        Err(_) => return internal(),
    };
    if changed_auth
        && (revoke_active_sessions(&mut transaction, id, "admin_user_update")
            .await
            .is_err()
            || arm_auth_fence(&state.valkey, id, version).await.is_err())
    {
        return internal();
    }
    if record_manage_audit(
        &mut transaction,
        &actor,
        "user.update",
        format!("Updated user {} (ID: {id})", existing.username),
        json!({"username": existing.username, "id": id}),
    )
    .await
    .is_err()
        || transaction.commit().await.is_err()
    {
        return internal();
    }
    if changed_auth
        && publish_auth_version(&state.valkey, id, version)
            .await
            .is_err()
    {
        tracing::warn!(
            user_id = id,
            auth_version = version,
            "user update committed; pending auth fence will fail closed until Valkey recovers"
        );
    }
    ok::<()>(None)
}

async fn delete_user(
    State(state): State<IdentityAdminState>,
    Extension(actor): Extension<DashboardUser>,
    Path(id): Path<i64>,
) -> Response {
    let mut transaction = match state.pool.begin().await {
        Ok(transaction) => transaction,
        Err(_) => return internal(),
    };
    let target = match sqlx::query_as::<_, UserView>(&format!(
        "SELECT {USER_COLUMNS} FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE"
    ))
    .bind(id)
    .fetch_optional(&mut *transaction)
    .await
    {
        Ok(Some(target)) => target,
        Ok(None) => return fail("record not found"),
        Err(_) => return internal(),
    };
    // Legacy hard-delete is stricter than detail/update: even root cannot
    // hard-delete an equal-role root account.
    if actor.role <= target.role {
        return fail("无权操作权限更高的用户");
    }
    let version = match sqlx::query_scalar::<_, i64>(
        "UPDATE users SET auth_version = auth_version + 1 WHERE id = $1 RETURNING auth_version",
    )
    .bind(id)
    .fetch_one(&mut *transaction)
    .await
    {
        Ok(version) => version,
        Err(_) => return internal(),
    };
    if arm_auth_fence(&state.valkey, id, version).await.is_err() {
        return internal();
    }
    for table in [
        "two_fa_backup_codes",
        "two_fas",
        "user_sessions",
        "auth_flows",
        "passkey_credentials",
        "tokens",
        "user_oauth_bindings",
        "external_identity_claims",
    ] {
        let sql = format!("DELETE FROM {table} WHERE user_id = $1");
        if sqlx::query(&sql)
            .bind(id)
            .execute(&mut *transaction)
            .await
            .is_err()
        {
            return internal();
        }
    }
    if record_manage_audit(
        &mut transaction,
        &actor,
        "user.delete",
        format!("Deleted user {} (ID: {id})", target.username),
        json!({"username": target.username, "id": id}),
    )
    .await
    .is_err()
        || sqlx::query("DELETE FROM users WHERE id = $1")
            .bind(id)
            .execute(&mut *transaction)
            .await
            .is_err()
        || transaction.commit().await.is_err()
    {
        return internal();
    }
    if publish_auth_version(&state.valkey, id, version)
        .await
        .is_err()
    {
        tracing::warn!(
            user_id = id,
            auth_version = version,
            "user deletion committed; pending auth fence will fail closed until Valkey recovers"
        );
    }
    ok::<()>(None)
}

#[derive(Deserialize)]
struct ManageRequest {
    #[serde(default)]
    id: i64,
    #[serde(default)]
    action: String,
    value: Option<i64>,
    mode: Option<String>,
}
#[derive(Serialize)]
struct Managed {
    role: i64,
    status: i64,
}

async fn manage_user(
    State(state): State<IdentityAdminState>,
    Extension(actor): Extension<DashboardUser>,
    request: Request,
) -> Response {
    let request: ManageRequest = match parse_json_body(request).await {
        Ok(request) => request,
        Err(response) => return response,
    };
    let mut transaction = match state.pool.begin().await {
        Ok(transaction) => transaction,
        Err(_) => return internal(),
    };
    let target = match locked_user(&mut transaction, request.id).await {
        Ok(Some(target)) => target,
        Ok(None) => return fail("用户不存在"),
        Err(_) => return internal(),
    };
    if target.deleted_at.is_some() {
        return fail("用户不存在");
    }
    if !can_manage(actor.role, target.role) {
        return fail("无权操作权限更高的用户");
    }
    match request.action.as_str() {
        "delete" => {
            if target.role == ROLE_ROOT {
                return fail("无法删除根用户");
            }
            let version = match sqlx::query_scalar::<_, i64>("UPDATE users SET deleted_at = NOW(), auth_version = auth_version + 1 WHERE id = $1 AND deleted_at IS NULL RETURNING auth_version").bind(target.id).fetch_optional(&mut *transaction).await {
                Ok(Some(version)) => version,
                Ok(None) => return fail("用户不存在"),
                Err(_) => return internal(),
            };
            if revoke_active_sessions(&mut transaction, target.id, "admin_delete")
                .await
                .is_err()
                || arm_auth_fence(&state.valkey, target.id, version)
                    .await
                    .is_err()
                || record_manage_audit(
                    &mut transaction,
                    &actor,
                    "user.manage",
                    format!(
                        "Performed delete on user {} (ID: {})",
                        target.username, target.id
                    ),
                    json!({"action":"delete", "username":target.username, "id":target.id}),
                )
                .await
                .is_err()
                || transaction.commit().await.is_err()
            {
                return internal();
            }
            if publish_auth_version(&state.valkey, target.id, version)
                .await
                .is_err()
            {
                tracing::warn!(
                    user_id = target.id,
                    auth_version = version,
                    "user soft deletion committed; pending auth fence will fail closed until Valkey recovers"
                );
            }
            ok::<()>(None)
        }
        "disable" | "enable" | "promote" | "demote" => {
            let (role, status, reason) = match request.action.as_str() {
                "disable" if target.role == ROLE_ROOT => return fail("无法禁用根用户"),
                "disable" => (target.role, STATUS_DISABLED, "admin_disable"),
                "enable" => (target.role, STATUS_ENABLED, "admin_enable"),
                "promote" if actor.role != ROLE_ROOT => return fail("管理员不能提升用户为管理员"),
                "promote" if target.role >= ROLE_ADMIN => return fail("用户已经是管理员"),
                "promote" => (ROLE_ADMIN, target.status, "admin_promote"),
                "demote" if target.role == ROLE_ROOT => return fail("无法降级根用户"),
                "demote" if target.role == 1 => return fail("用户已经是普通用户"),
                "demote" => (1, target.status, "admin_demote"),
                _ => return fail("参数错误"),
            };
            let version = match sqlx::query_scalar::<_, i64>("UPDATE users SET role = $2, status = $3, auth_version = auth_version + 1 WHERE id = $1 RETURNING auth_version").bind(target.id).bind(role).bind(status).fetch_one(&mut *transaction).await {
                Ok(version) => version,
                Err(_) => return internal(),
            };
            if revoke_active_sessions(&mut transaction, target.id, reason)
                .await
                .is_err()
                || arm_auth_fence(&state.valkey, target.id, version)
                    .await
                    .is_err()
                || record_manage_audit(
                    &mut transaction,
                    &actor,
                    "user.manage",
                    format!(
                        "Performed {} on user {} (ID: {})",
                        request.action, target.username, target.id
                    ),
                    json!({"action":request.action, "username":target.username, "id":target.id}),
                )
                .await
                .is_err()
                || transaction.commit().await.is_err()
            {
                return internal();
            }
            if publish_auth_version(&state.valkey, target.id, version)
                .await
                .is_err()
            {
                tracing::warn!(
                    user_id = target.id,
                    auth_version = version,
                    "user management committed; pending auth fence will fail closed until Valkey recovers"
                );
            }
            ok(Some(Managed { role, status }))
        }
        "add_quota" => {
            if let Err(response) = manage_quota(
                &mut transaction,
                &actor,
                &target,
                request.mode.as_deref(),
                request.value,
            )
            .await
            {
                return response;
            }
            if transaction.commit().await.is_err() {
                return internal();
            }
            ok::<()>(None)
        }
        _ => fail("参数错误"),
    }
}

async fn manage_quota(
    transaction: &mut Transaction<'_, Postgres>,
    actor: &DashboardUser,
    target: &UserView,
    mode: Option<&str>,
    value: Option<i64>,
) -> Result<(), Response> {
    let Some(value) = value else {
        return Err(fail("参数错误"));
    };
    let sql = match mode {
        Some("add") if value > 0 => "UPDATE users SET quota = quota + $2 WHERE id = $1",
        Some("subtract") if value > 0 => "UPDATE users SET quota = quota - $2 WHERE id = $1",
        Some("override") => "UPDATE users SET quota = $2 WHERE id = $1",
        Some("add") | Some("subtract") => return Err(fail("修改额度不能为零")),
        _ => return Err(fail("参数错误")),
    };
    if sqlx::query(sql)
        .bind(target.id)
        .bind(value)
        .execute(&mut **transaction)
        .await
        .is_err()
    {
        return Err(internal());
    }
    let action = match mode {
        Some("add") => "user.quota_add",
        Some("subtract") => "user.quota_subtract",
        _ => "user.quota_override",
    };
    let content = match mode {
        Some("add") => format!("Increased user quota by {value}"),
        Some("subtract") => format!("Decreased user quota by {value}"),
        _ => format!("Overrode user quota from {} to {value}", target.quota),
    };
    let params = match mode {
        Some("add") | Some("subtract") => json!({"quota": value, "target_user_id": target.id}),
        _ => json!({"from": target.quota, "to": value, "target_user_id": target.id}),
    };
    if record_manage_audit(transaction, actor, action, content, params)
        .await
        .is_err()
    {
        return Err(internal());
    }
    Ok(())
}

fn can_manage(actor_role: i64, target_role: i64) -> bool {
    actor_role == ROLE_ROOT || actor_role > target_role
}

async fn locked_user(
    transaction: &mut Transaction<'_, Postgres>,
    user_id: i64,
) -> Result<Option<UserView>, sqlx::Error> {
    sqlx::query_as::<_, UserView>(&format!(
        "SELECT {USER_COLUMNS} FROM users WHERE id = $1 FOR UPDATE"
    ))
    .bind(user_id)
    .fetch_optional(&mut **transaction)
    .await
}

async fn revoke_active_sessions(
    transaction: &mut Transaction<'_, Postgres>,
    user_id: i64,
    reason: &str,
) -> Result<(), sqlx::Error> {
    sqlx::query("UPDATE user_sessions SET status = 'revoked', revoked_at = EXTRACT(EPOCH FROM NOW())::BIGINT, revoked_reason = $2 WHERE user_id = $1 AND status = 'active'")
        .bind(user_id)
        .bind(reason)
        .execute(&mut **transaction)
        .await
        .map(|_| ())
}

async fn arm_auth_fence(
    valkey: &redis::Client,
    user_id: i64,
    version: i64,
) -> Result<(), redis::RedisError> {
    let mut connection = valkey.get_multiplexed_async_connection().await?;
    redis::cmd("SET")
        .arg(format!("auth:user:fence:{user_id}"))
        .arg(version)
        .arg("EX")
        .arg(AUTH_FENCE_TTL_SECONDS)
        .query_async::<()>(&mut connection)
        .await
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

async fn record_manage_audit(
    transaction: &mut Transaction<'_, Postgres>,
    actor: &DashboardUser,
    action: &str,
    content: String,
    params: serde_json::Value,
) -> Result<(), sqlx::Error> {
    let other = json!({
        "op": { "action": action, "params": params },
        "admin_info": {
            "admin_id": actor.id,
            "admin_username": actor.username,
            "admin_role": actor.role,
        },
    });
    sqlx::query("INSERT INTO logs (user_id, created_at, type, content, username, ip, other) VALUES ($1, EXTRACT(EPOCH FROM NOW())::BIGINT, 3, $2, $3, '', $4)")
        .bind(actor.id)
        .bind(content)
        .bind(&actor.username)
        .bind(other.to_string())
        .execute(&mut **transaction)
        .await
        .map(|_| ())
}
