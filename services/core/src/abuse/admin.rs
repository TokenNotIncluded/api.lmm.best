//! Administrator-only preview, compare-and-swap and audit. No Go authority.
use super::*;
use axum::{
    Json, Router,
    extract::{DefaultBodyLimit, Path, Query, State},
    http::{HeaderMap, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use serde::{Deserialize, Serialize};
use serde_json::json;

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PolicyUpdate {
    pub operation_id: String,
    pub reason: String,
    pub expected_revision: i64,
    pub config: PolicyConfig,
}
#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Action {
    Mark,
    Unmark,
    ConvertDisabled,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Target {
    pub user_id: i64,
    pub expected_revision: i64,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PreviewRequest {
    pub operation_id: String,
    pub reason: String,
    pub action: Action,
    #[serde(default)]
    pub policy_id: Option<String>,
    pub expected_policy_revision: i64,
    pub targets: Vec<Target>,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct CommitRequest {
    pub operation_id: String,
    pub preview_id: String,
    pub preview_hash: String,
}
#[derive(Clone, Deserialize, Serialize)]
pub struct UserView {
    pub user_id: i64,
    pub username: String,
    pub email: String,
    pub active: bool,
    pub platform_role: String,
    pub revision: i64,
    pub marked: bool,
    pub policy_id: Option<String>,
}
#[derive(Deserialize, Serialize)]
struct SnapshotUser {
    view: UserView,
    auth_version: i64,
}
#[derive(Deserialize, Serialize)]
struct Snapshot {
    request: PreviewRequest,
    users: Vec<SnapshotUser>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct UserQuery {
    pub after: Option<i64>,
    pub limit: Option<i64>,
    pub query: Option<String>,
    pub state: Option<String>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Page {
    pub after: Option<i64>,
    pub limit: Option<i64>,
}

fn mutation_fields(operation: &str, reason: &str) -> Result<()> {
    if !(8..=128).contains(&operation.len())
        || !operation
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"_.:-".contains(&b))
        || reason.trim().is_empty()
        || reason.chars().count() > 1000
        || reason.contains('\0')
    {
        return Err(AbuseError::Invalid);
    }
    Ok(())
}
pub(super) async fn administrator(c: &mut PgConnection, secret: &str) -> Result<Principal> {
    let actor = crate::identity::authorize_in_transaction(c, secret).await?;
    if actor.credential_kind != CredentialKind::Session {
        return Err(AbuseError::Forbidden);
    }
    let role: String = sqlx::query_scalar(
        "SELECT platform_role FROM core_identity.users WHERE id=$1 AND active FOR SHARE",
    )
    .bind(actor.user_id)
    .fetch_one(c)
    .await?;
    if role != "superadmin" {
        return Err(AbuseError::Forbidden);
    }
    Ok(actor)
}
const USERS: &str = "SELECT u.id,u.active,u.platform_role,u.auth_version,COALESCE(p.username,'') AS username,COALESCE(p.email,'') AS email,COALESCE(m.revision,0) AS revision,COALESCE(m.marked,FALSE) AS marked,m.policy_id FROM core_identity.users u LEFT JOIN core_identity.user_profiles p ON p.user_id=u.id LEFT JOIN core_abuse.user_marks m ON m.user_id=u.id";
fn user_row(r: &sqlx::postgres::PgRow) -> Result<UserView> {
    Ok(UserView {
        user_id: r.try_get("id")?,
        username: r.try_get("username")?,
        email: r.try_get("email")?,
        active: r.try_get("active")?,
        platform_role: r.try_get("platform_role")?,
        revision: r.try_get("revision")?,
        marked: r.try_get("marked")?,
        policy_id: r.try_get("policy_id")?,
    })
}
async fn user(c: &mut PgConnection, id: i64, write: bool) -> Result<SnapshotUser> {
    let suffix = if write {
        " FOR UPDATE OF u"
    } else {
        " FOR SHARE OF u"
    };
    let r = sqlx::query(&format!("{USERS} WHERE u.id=$1{suffix}"))
        .bind(id)
        .fetch_optional(c)
        .await?
        .ok_or(AbuseError::NotFound)?;
    Ok(SnapshotUser {
        view: user_row(&r)?,
        auth_version: r.try_get("auth_version")?,
    })
}
async fn existing_operation(
    c: &mut PgConnection,
    actor: i64,
    operation: &str,
    digest: &[u8],
) -> Result<Option<Value>> {
    let r=sqlx::query("SELECT request_digest,result FROM core_abuse.operations WHERE actor_user_id=$1 AND operation_id=$2")
        .bind(actor).bind(operation).fetch_optional(c).await?;
    if let Some(r) = r {
        if r.try_get::<Vec<u8>, _>("request_digest")? != digest {
            return Err(AbuseError::Conflict);
        }
        return Ok(Some(r.try_get("result")?));
    }
    Ok(None)
}
struct AuditRecord<'a> {
    actor: i64,
    operation: &'a str,
    digest: &'a [u8],
    kind: &'a str,
    reason: &'a str,
    detail: Value,
    result: &'a Value,
}
async fn record(c: &mut PgConnection, entry: AuditRecord<'_>) -> Result<()> {
    let AuditRecord {
        actor,
        operation,
        digest,
        kind,
        reason,
        detail,
        result,
    } = entry;
    sqlx::query("INSERT INTO core_abuse.operations(actor_user_id,operation_id,request_digest,result) VALUES ($1,$2,$3,$4)")
        .bind(actor).bind(operation).bind(digest).bind(result).execute(&mut *c).await?;
    sqlx::query("INSERT INTO core_abuse.audit(actor_user_id,operation_id,kind,reason,detail) VALUES ($1,$2,$3,$4,$5)")
        .bind(actor).bind(operation).bind(kind).bind(reason).bind(detail).execute(c).await?;
    Ok(())
}
impl AbuseStore {
    pub async fn policies(&self, secret: &str) -> Result<Value> {
        let mut tx = self.pool.begin().await?;
        administrator(&mut tx, secret).await?;
        let (revision, config) = configuration(&mut tx).await?;
        tx.commit().await?;
        Ok(
            json!({"version":1,"revision":revision,"config":config,"capabilities":{
            "client_protocols":["openai_chat","openai_responses"],
            "upstream_protocols":["openai_chat","openai_responses","anthropic_messages","gemini"],
            "modes":["fixed_text","custom_upstream"],"numeric_loopback_only":self.network.is_none(),
            "external_https":self.network.is_some(),
            "local_http":self.network.as_ref().is_none_or(|policy|policy.local_http),
            "allowed_origins":self.network.as_ref().map(|policy|&policy.origins),
            "credential_references":self.aliases.as_ref()}}),
        )
    }
    pub async fn update_policies(&self, secret: &str, input: &PolicyUpdate) -> Result<Value> {
        mutation_fields(&input.operation_id, &input.reason)?;
        if input.expected_revision < 0 {
            return Err(AbuseError::Invalid);
        }
        let digest = hash(input)?;
        let mut tx = self.pool.begin().await?;
        resource_lock(&mut tx).await?;
        let actor = administrator(&mut tx, secret).await?;
        if let Some(result) =
            existing_operation(&mut tx, actor.user_id, &input.operation_id, &digest).await?
        {
            tx.commit().await?;
            return Ok(result);
        }
        self.validate_configuration(&input.config)?;
        let current: i64 = sqlx::query_scalar(
            "SELECT revision FROM core_abuse.settings WHERE singleton FOR UPDATE",
        )
        .fetch_one(&mut *tx)
        .await?;
        if current != input.expected_revision {
            return Err(AbuseError::Conflict);
        }
        let ids: Vec<&str> = input
            .config
            .policies
            .iter()
            .map(|p| p.id.as_str())
            .collect();
        let removed:bool=sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM core_abuse.user_marks WHERE marked AND NOT (policy_id=ANY($1::text[])))")
            .bind(&ids).fetch_one(&mut *tx).await?;
        if removed {
            return Err(AbuseError::Conflict);
        }
        let next = current.checked_add(1).ok_or(AbuseError::Unavailable)?;
        sqlx::query("UPDATE core_abuse.settings SET revision=$1,document=$2 WHERE singleton")
            .bind(next)
            .bind(serde_json::to_value(&input.config).map_err(|_| AbuseError::Invalid)?)
            .execute(&mut *tx)
            .await?;
        let applied_config_hash = config_hash(&input.config)?;
        let result = json!({"version":1,"status":"applied","operation_id":input.operation_id,"kind":"policies","policy_revision":next,"applied_config_hash":applied_config_hash,"users":[]});
        // Bind a policy digest, not endpoint credentials or arbitrary request data.
        record(&mut tx,AuditRecord{actor:actor.user_id,operation:&input.operation_id,digest:&digest,kind:"policies",reason:&input.reason,detail:json!({"before_revision":current,"after_revision":next,"policy_ids":ids,"config_digest":applied_config_hash}),result:&result}).await?;
        tx.commit().await?;
        Ok(result)
    }
    pub async fn users(&self, secret: &str, q: &UserQuery) -> Result<Value> {
        let after = q.after.unwrap_or(0);
        let limit = q.limit.unwrap_or(50);
        let query = q.query.as_deref().unwrap_or("");
        let state = q.state.as_deref().unwrap_or("all");
        if after < 0
            || !(1..=200).contains(&limit)
            || query.len() > 320
            || query.contains('\0')
            || !matches!(state, "all" | "marked" | "normal" | "disabled")
        {
            return Err(AbuseError::Invalid);
        }
        let pattern = format!(
            "%{}%",
            query
                .replace('\\', "\\\\")
                .replace('%', "\\%")
                .replace('_', "\\_")
        );
        let mut tx = self.pool.begin().await?;
        administrator(&mut tx, secret).await?;
        let rows=sqlx::query(&format!(r#"{USERS} WHERE u.id>$1 AND ($3='%%' OR p.username ILIKE $3 ESCAPE '\' OR p.email ILIKE $3 ESCAPE '\') AND ($4='all' OR ($4='marked' AND COALESCE(m.marked,FALSE)) OR ($4='normal' AND NOT COALESCE(m.marked,FALSE)) OR ($4='disabled' AND NOT u.active)) ORDER BY u.id LIMIT $2"#))
            .bind(after).bind(limit+1).bind(pattern).bind(state).fetch_all(&mut *tx).await?;
        let more = rows.len() > limit as usize;
        let users: Vec<UserView> = rows
            .iter()
            .take(limit as usize)
            .map(user_row)
            .collect::<Result<_>>()?;
        let next = if more {
            users.last().map(|u| u.user_id)
        } else {
            None
        };
        tx.commit().await?;
        Ok(json!({"version":1,"users":users,"next_after":next}))
    }
    pub async fn inspect_user(&self, secret: &str, id: i64) -> Result<UserView> {
        if id <= 0 {
            return Err(AbuseError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        administrator(&mut tx, secret).await?;
        let view = user(&mut tx, id, false).await?.view;
        tx.commit().await?;
        Ok(view)
    }
    pub async fn preview_users(&self, secret: &str, input: &PreviewRequest) -> Result<Value> {
        mutation_fields(&input.operation_id, &input.reason)?;
        if input.expected_policy_revision < 0
            || input.targets.is_empty()
            || input.targets.len() > 200
            || input
                .targets
                .iter()
                .any(|t| t.user_id <= 0 || t.expected_revision < 0)
            || input
                .targets
                .iter()
                .map(|t| t.user_id)
                .collect::<BTreeSet<_>>()
                .len()
                != input.targets.len()
            || (input.action == Action::Unmark && input.policy_id.is_some())
        {
            return Err(AbuseError::Invalid);
        }
        let digest = hash(input)?;
        let mut tx = self.pool.begin().await?;
        resource_lock(&mut tx).await?;
        let actor = administrator(&mut tx, secret).await?;
        // An operation already applied by another mutation kind cannot create a new preview.
        if sqlx::query_scalar::<_,bool>("SELECT EXISTS(SELECT 1 FROM core_abuse.operations WHERE actor_user_id=$1 AND operation_id=$2)").bind(actor.user_id).bind(&input.operation_id).fetch_one(&mut *tx).await? { return Err(AbuseError::Conflict); }
        if let Some(r)=sqlx::query("SELECT request_digest,response FROM core_abuse.previews WHERE actor_user_id=$1 AND operation_id=$2")
            .bind(actor.user_id).bind(&input.operation_id).fetch_optional(&mut *tx).await? {
            if r.try_get::<Vec<u8>,_>("request_digest")?!=digest { return Err(AbuseError::Conflict); }
            let response=r.try_get("response")?; tx.commit().await?; return Ok(response);
        }
        let (revision, cfg) = configuration(&mut tx).await?;
        if revision != input.expected_policy_revision {
            return Err(AbuseError::Conflict);
        }
        if input.action != Action::Unmark
            && !cfg
                .policies
                .iter()
                .any(|p| Some(&p.id) == input.policy_id.as_ref())
        {
            return Err(AbuseError::Invalid);
        }
        let mut targets = input.targets.clone();
        targets.sort_by_key(|t| t.user_id);
        let mut snapshots = Vec::new();
        let mut views = Vec::new();
        for target in &targets {
            let snapshot = user(&mut tx, target.user_id, false).await?;
            let v = &snapshot.view;
            if v.user_id == actor.user_id || v.platform_role != "user" {
                return Err(AbuseError::Forbidden);
            }
            if v.revision != target.expected_revision
                || (input.action == Action::Mark && !v.active)
                || (input.action == Action::ConvertDisabled && v.active)
            {
                return Err(AbuseError::Conflict);
            }
            views.push(json!({"user_id":v.user_id,"username":v.username,"active":v.active,"expected_revision":v.revision,"before":{"marked":v.marked,"policy_id":v.policy_id},"after":{"marked":input.action!=Action::Unmark,"policy_id":input.policy_id,"active":v.active || input.action==Action::ConvertDisabled}}));
            snapshots.push(snapshot);
        }
        let mut normalized = input.clone();
        normalized.targets = targets;
        let snapshot = Snapshot {
            request: normalized,
            users: snapshots,
        };
        let snapshot_json = serde_json::to_value(&snapshot).map_err(|_| AbuseError::Invalid)?;
        let preview_hash = hash(&snapshot)?;
        let id = random_id()?;
        let expires_at: i64 =
            sqlx::query_scalar("SELECT floor(extract(epoch FROM clock_timestamp()))::bigint+300")
                .fetch_one(&mut *tx)
                .await?;
        let response = json!({"version":1,"preview_id":id,"preview_hash":hex(&preview_hash),"expires_at":expires_at,"operation_id":input.operation_id,"action":input.action,"policy_id":input.policy_id,"policy_revision":revision,"targets":views});
        sqlx::query("INSERT INTO core_abuse.previews(id,actor_user_id,operation_id,request_digest,preview_hash,snapshot,response,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,to_timestamp($8::double precision))")
            .bind(&id).bind(actor.user_id).bind(&input.operation_id).bind(digest).bind(preview_hash).bind(snapshot_json).bind(&response).bind(expires_at).execute(&mut *tx).await?;
        tx.commit().await?;
        Ok(response)
    }
    pub async fn commit_users(&self, secret: &str, input: &CommitRequest) -> Result<Value> {
        if !policy::identifier(&input.preview_id, 64, 64)
            || !policy::identifier(&input.preview_hash, 64, 64)
        {
            return Err(AbuseError::Invalid);
        }
        mutation_fields(&input.operation_id, "commit")?;
        let digest = hash(input)?;
        let mut tx = self.pool.begin().await?;
        resource_lock(&mut tx).await?;
        let actor = administrator(&mut tx, secret).await?;
        if let Some(result) =
            existing_operation(&mut tx, actor.user_id, &input.operation_id, &digest).await?
        {
            tx.commit().await?;
            return Ok(result);
        }
        // Preview records are append-only. The operation resource lock already
        // serializes commits; a row lock would incorrectly require UPDATE
        // privilege on immutable evidence. Actor/hash/expiry and current
        // policy/user authority are still verified below in this transaction.
        let row=sqlx::query("SELECT preview_hash,snapshot,expires_at>clock_timestamp() AS live FROM core_abuse.previews WHERE id=$1 AND actor_user_id=$2 AND operation_id=$3")
            .bind(&input.preview_id).bind(actor.user_id).bind(&input.operation_id).fetch_optional(&mut *tx).await?.ok_or(AbuseError::NotFound)?;
        if hex(&row.try_get::<Vec<u8>, _>("preview_hash")?) != input.preview_hash {
            return Err(AbuseError::Conflict);
        }
        if !row.try_get::<bool, _>("live")? {
            return Err(AbuseError::PreviewExpired);
        }
        let snapshot: Snapshot = serde_json::from_value(row.try_get("snapshot")?)
            .map_err(|_| AbuseError::Unavailable)?;
        let (revision, cfg) = configuration(&mut tx).await?;
        if revision != snapshot.request.expected_policy_revision {
            return Err(AbuseError::Conflict);
        }
        if snapshot.request.action != Action::Unmark
            && !cfg
                .policies
                .iter()
                .any(|p| Some(&p.id) == snapshot.request.policy_id.as_ref())
        {
            return Err(AbuseError::Conflict);
        }
        let mut before = Vec::new();
        for old in &snapshot.users {
            let current = user(&mut tx, old.view.user_id, true).await?;
            if current.view.user_id == actor.user_id || current.view.platform_role != "user" {
                return Err(AbuseError::Forbidden);
            }
            if current.auth_version != old.auth_version
                || current.view.revision != old.view.revision
                || current.view.active != old.view.active
                || current.view.marked != old.view.marked
                || current.view.policy_id != old.view.policy_id
            {
                return Err(AbuseError::Conflict);
            }
            before.push(current);
        }
        let marked = snapshot.request.action != Action::Unmark;
        let mut after = Vec::new();
        let mut detail = Vec::new();
        for previous in before {
            let id = previous.view.user_id;
            let next = previous
                .view
                .revision
                .checked_add(1)
                .ok_or(AbuseError::Unavailable)?;
            // A row lock alone does not invalidate a pre-existing SERIALIZABLE
            // snapshot. Write an unchanged authority tuple so an older snapshot
            // waiting on this mark fails with 40001 rather than missing it.
            // The auth generation VALUE stays unchanged: sessions remain valid.
            sqlx::query("UPDATE core_identity.users SET auth_version=auth_version WHERE id=$1")
                .bind(id)
                .execute(&mut *tx)
                .await?;
            if snapshot.request.action == Action::ConvertDisabled {
                sqlx::query("UPDATE core_identity.users SET active=TRUE WHERE id=$1 AND NOT active AND platform_role='user'").bind(id).execute(&mut *tx).await?;
            }
            sqlx::query("INSERT INTO core_abuse.user_marks(user_id,marked,policy_id,revision) VALUES ($1,$2,$3,$4) ON CONFLICT(user_id) DO UPDATE SET marked=EXCLUDED.marked,policy_id=EXCLUDED.policy_id,revision=EXCLUDED.revision")
                .bind(id).bind(marked).bind(&snapshot.request.policy_id).bind(next).execute(&mut *tx).await?;
            detail.push(json!({"user_id":id,"before_revision":previous.view.revision,"after_revision":next,"before_marked":previous.view.marked,"after_marked":marked,"before_active":previous.view.active,"after_active":previous.view.active || snapshot.request.action==Action::ConvertDisabled,"policy_id":snapshot.request.policy_id}));
            after.push(user(&mut tx, id, false).await?.view);
        }
        let result = json!({"version":1,"status":"applied","operation_id":input.operation_id,"kind":"users","policy_revision":revision,"users":after});
        record(&mut tx,AuditRecord{actor:actor.user_id,operation:&input.operation_id,digest:&digest,kind:"users",reason:&snapshot.request.reason,detail:json!({"action":snapshot.request.action,"policy_revision":revision,"transitions":detail}),result:&result}).await?;
        tx.commit().await?;
        Ok(result)
    }
    pub async fn operation(&self, secret: &str, operation_id: &str) -> Result<Value> {
        mutation_fields(operation_id, "read")?;
        let mut tx = self.pool.begin().await?;
        let actor = administrator(&mut tx, secret).await?;
        let result = sqlx::query_scalar(
            "SELECT result FROM core_abuse.operations WHERE actor_user_id=$1 AND operation_id=$2",
        )
        .bind(actor.user_id)
        .bind(operation_id)
        .fetch_optional(&mut *tx)
        .await?
        .ok_or(AbuseError::OperationNotFound)?;
        tx.commit().await?;
        Ok(result)
    }
    pub async fn audit(&self, secret: &str, page: &Page) -> Result<Value> {
        let after = page.after.unwrap_or(0);
        let limit = page.limit.unwrap_or(50);
        if after < 0 || !(1..=200).contains(&limit) {
            return Err(AbuseError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        administrator(&mut tx, secret).await?;
        let rows=sqlx::query("SELECT id,actor_user_id,operation_id,kind,reason,detail,extract(epoch FROM created_at)::bigint AS created_at FROM core_abuse.audit WHERE id>$1 ORDER BY id LIMIT $2")
            .bind(after).bind(limit+1).fetch_all(&mut *tx).await?;
        let mut events = Vec::new();
        for r in rows.iter().take(limit as usize) {
            events.push(json!({"id":r.try_get::<i64,_>("id")?,"actor_user_id":r.try_get::<i64,_>("actor_user_id")?,"operation_id":r.try_get::<String,_>("operation_id")?,"kind":r.try_get::<String,_>("kind")?,"reason":r.try_get::<String,_>("reason")?,"detail":r.try_get::<Value,_>("detail")?,"created_at":r.try_get::<i64,_>("created_at")?}));
        }
        let next = if rows.len() > limit as usize {
            events.last().and_then(|e| e["id"].as_i64())
        } else {
            None
        };
        tx.commit().await?;
        Ok(json!({"version":1,"events":events,"next_after":next}))
    }
}

pub(super) fn bearer(headers: &HeaderMap) -> Result<&str> {
    let mut values = headers.get_all(header::AUTHORIZATION).iter();
    let raw = values
        .next()
        .ok_or(AbuseError::Unauthorized)?
        .to_str()
        .map_err(|_| AbuseError::Unauthorized)?;
    if values.next().is_some() {
        return Err(AbuseError::Unauthorized);
    }
    let (scheme, secret) = raw.split_once(' ').ok_or(AbuseError::Unauthorized)?;
    if !scheme.eq_ignore_ascii_case("Bearer") {
        return Err(AbuseError::Unauthorized);
    }
    Ok(secret)
}
impl IntoResponse for AbuseError {
    fn into_response(self) -> Response {
        let (status, code) = match self {
            Self::Unauthorized => (StatusCode::UNAUTHORIZED, "ADMIN_ABUSE_UNAUTHORIZED"),
            Self::Forbidden => (StatusCode::FORBIDDEN, "ADMIN_ABUSE_FORBIDDEN"),
            Self::Invalid => (StatusCode::BAD_REQUEST, "ADMIN_ABUSE_INVALID"),
            Self::NotFound => (StatusCode::NOT_FOUND, "ADMIN_ABUSE_NOT_FOUND"),
            Self::OperationNotFound => (StatusCode::NOT_FOUND, "ADMIN_ABUSE_OPERATION_NOT_FOUND"),
            Self::Conflict => (StatusCode::CONFLICT, "ADMIN_ABUSE_CONFLICT"),
            Self::PreviewExpired => (StatusCode::GONE, "ADMIN_ABUSE_PREVIEW_EXPIRED"),
            Self::RateLimited => (StatusCode::TOO_MANY_REQUESTS, "ADMIN_ABUSE_RATE_LIMITED"),
            Self::Unavailable => (StatusCode::SERVICE_UNAVAILABLE, "ADMIN_ABUSE_UNAVAILABLE"),
        };
        (
            status,
            [(header::CACHE_CONTROL, "no-store")],
            Json(json!({"error":{"code":code}})),
        )
            .into_response()
    }
}
pub fn router(store: AbuseStore) -> Router {
    Router::new()
        .route(
            "/core/v1/admin/abuse/policies",
            get(get_policies).put(put_policies),
        )
        .route("/core/v1/admin/abuse/users", get(get_users))
        .route("/core/v1/admin/abuse/users/{id}", get(get_user))
        .route(
            "/core/v1/admin/abuse/users/{id}/similarity",
            get(super::registration::get_similarity),
        )
        .route(
            "/core/v1/assistant/model-credential",
            get(super::registration::get_model_credential),
        )
        .route("/core/v1/admin/abuse/users/preview", post(preview))
        .route("/core/v1/admin/abuse/users/commit", post(commit))
        .route("/core/v1/admin/abuse/operations/{id}", get(operation))
        .route("/core/v1/admin/abuse/audit", get(audit))
        .layer(DefaultBodyLimit::max(96 * 1024))
        .layer(axum::middleware::from_fn(
            |request: axum::extract::Request, next: axum::middleware::Next| async move {
                let mut response = next.run(request).await;
                response.headers_mut().insert(
                    header::CACHE_CONTROL,
                    axum::http::HeaderValue::from_static("no-store"),
                );
                response
            },
        ))
        .with_state(store)
}
async fn get_policies(State(s): State<AbuseStore>, h: HeaderMap) -> Result<Json<Value>> {
    Ok(Json(s.policies(bearer(&h)?).await?))
}
async fn put_policies(
    State(s): State<AbuseStore>,
    h: HeaderMap,
    body: std::result::Result<Json<PolicyUpdate>, axum::extract::rejection::JsonRejection>,
) -> Result<Json<Value>> {
    let Json(v) = body.map_err(|_| AbuseError::Invalid)?;
    Ok(Json(s.update_policies(bearer(&h)?, &v).await?))
}
async fn get_users(
    State(s): State<AbuseStore>,
    h: HeaderMap,
    query: std::result::Result<Query<UserQuery>, axum::extract::rejection::QueryRejection>,
) -> Result<Json<Value>> {
    let Query(v) = query.map_err(|_| AbuseError::Invalid)?;
    Ok(Json(s.users(bearer(&h)?, &v).await?))
}
async fn get_user(
    State(s): State<AbuseStore>,
    h: HeaderMap,
    Path(id): Path<String>,
) -> Result<Json<UserView>> {
    let id = id.parse().map_err(|_| AbuseError::Invalid)?;
    Ok(Json(s.inspect_user(bearer(&h)?, id).await?))
}
async fn preview(
    State(s): State<AbuseStore>,
    h: HeaderMap,
    body: std::result::Result<Json<PreviewRequest>, axum::extract::rejection::JsonRejection>,
) -> Result<Json<Value>> {
    let Json(v) = body.map_err(|_| AbuseError::Invalid)?;
    Ok(Json(s.preview_users(bearer(&h)?, &v).await?))
}
async fn commit(
    State(s): State<AbuseStore>,
    h: HeaderMap,
    body: std::result::Result<Json<CommitRequest>, axum::extract::rejection::JsonRejection>,
) -> Result<Json<Value>> {
    let Json(v) = body.map_err(|_| AbuseError::Invalid)?;
    Ok(Json(s.commit_users(bearer(&h)?, &v).await?))
}
async fn operation(
    State(s): State<AbuseStore>,
    h: HeaderMap,
    Path(id): Path<String>,
) -> Result<Json<Value>> {
    Ok(Json(s.operation(bearer(&h)?, &id).await?))
}
async fn audit(
    State(s): State<AbuseStore>,
    h: HeaderMap,
    query: std::result::Result<Query<Page>, axum::extract::rejection::QueryRejection>,
) -> Result<Json<Value>> {
    let Query(v) = query.map_err(|_| AbuseError::Invalid)?;
    Ok(Json(s.audit(bearer(&h)?, &v).await?))
}
