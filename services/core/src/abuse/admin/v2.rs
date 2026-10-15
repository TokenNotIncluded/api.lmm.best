//! Explicit public ID projection. Existing v1 storage and hash inputs stay integer.
use super::{
    AbuseError, AbuseStore, Action, CommitRequest, Page, PreviewRequest, Result, Target, UserQuery,
    bearer, mutation_fields,
};
use axum::{
    Json, Router,
    extract::{Path, Query, State},
    http::HeaderMap,
    routing::{get, post},
};
use serde::Deserialize;
use serde_json::{Map, Value};

fn positive_id(input: &str) -> Result<i64> {
    if input.is_empty()
        || input.len() > 19
        || !matches!(input.as_bytes()[0], b'1'..=b'9')
        || !input.bytes().all(|b| b.is_ascii_digit())
    {
        return Err(AbuseError::Invalid);
    }
    input.parse::<i64>().map_err(|_| AbuseError::Invalid)
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct UserQueryV2 {
    after: Option<String>,
    limit: Option<i64>,
    query: Option<String>,
    state: Option<String>,
}
impl UserQueryV2 {
    fn domain(self) -> Result<UserQuery> {
        Ok(UserQuery {
            after: self.after.as_deref().map(positive_id).transpose()?,
            limit: self.limit,
            query: self.query,
            state: self.state,
        })
    }
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PageV2 {
    after: Option<String>,
    limit: Option<i64>,
}
impl PageV2 {
    fn domain(self) -> Result<Page> {
        Ok(Page {
            after: self.after.as_deref().map(positive_id).transpose()?,
            limit: self.limit,
        })
    }
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct TargetV2 {
    user_id: String,
    expected_revision: i64,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PreviewRequestV2 {
    operation_id: String,
    reason: String,
    action: Action,
    #[serde(default)]
    policy_id: Option<String>,
    expected_policy_revision: i64,
    targets: Vec<TargetV2>,
}
impl PreviewRequestV2 {
    fn domain(self) -> Result<PreviewRequest> {
        // request_digest is calculated before the existing domain sorts targets.
        // Preserve incoming order and Option values when normalizing the wire.
        Ok(PreviewRequest {
            operation_id: self.operation_id,
            reason: self.reason,
            action: self.action,
            policy_id: self.policy_id,
            expected_policy_revision: self.expected_policy_revision,
            targets: self
                .targets
                .into_iter()
                .map(|v| {
                    Ok(Target {
                        user_id: positive_id(&v.user_id)?,
                        expected_revision: v.expected_revision,
                    })
                })
                .collect::<Result<_>>()?,
        })
    }
}

// Validate the closed original result before changing any public fields. In
// particular, nullable fields are required keys, unlike Serde Option defaults.
fn object<'a>(value: &'a mut Value, fields: &[&str]) -> Result<&'a mut Map<String, Value>> {
    let map = value.as_object_mut().ok_or(AbuseError::Unavailable)?;
    if map.len() != fields.len() || fields.iter().any(|key| !map.contains_key(*key)) {
        return Err(AbuseError::Unavailable);
    }
    Ok(map)
}
fn value<'a>(map: &'a Map<String, Value>, key: &str) -> Result<&'a Value> {
    map.get(key).ok_or(AbuseError::Unavailable)
}
fn value_mut<'a>(map: &'a mut Map<String, Value>, key: &str) -> Result<&'a mut Value> {
    map.get_mut(key).ok_or(AbuseError::Unavailable)
}
fn text<'a>(map: &'a Map<String, Value>, key: &str) -> Result<&'a str> {
    value(map, key)?.as_str().ok_or(AbuseError::Unavailable)
}
fn integer(map: &Map<String, Value>, key: &str) -> Result<i64> {
    value(map, key)?.as_i64().ok_or(AbuseError::Unavailable)
}
fn nonnegative(map: &Map<String, Value>, key: &str) -> Result<i64> {
    let n = integer(map, key)?;
    if n < 0 {
        return Err(AbuseError::Unavailable);
    }
    Ok(n)
}
fn boolean(map: &Map<String, Value>, key: &str) -> Result<bool> {
    value(map, key)?.as_bool().ok_or(AbuseError::Unavailable)
}
fn original_version(map: &Map<String, Value>) -> Result<()> {
    if value(map, "version")?.as_i64() != Some(1) {
        return Err(AbuseError::Unavailable);
    }
    Ok(())
}
fn opaque_hash(map: &Map<String, Value>, key: &str) -> Result<()> {
    let s = text(map, key)?;
    if s.len() != 64
        || !s
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
    {
        return Err(AbuseError::Unavailable);
    }
    Ok(())
}
fn operation_id(map: &Map<String, Value>) -> Result<()> {
    mutation_fields(text(map, "operation_id")?, "read").map_err(|_| AbuseError::Unavailable)
}
fn policy_id(map: &Map<String, Value>, key: &str) -> Result<bool> {
    match value(map, key)? {
        Value::Null => Ok(false),
        Value::String(s) if super::policy::identifier(s, 1, 64) => Ok(true),
        _ => Err(AbuseError::Unavailable),
    }
}
fn marked_policy(map: &Map<String, Value>) -> Result<()> {
    if boolean(map, "marked")? != policy_id(map, "policy_id")? {
        return Err(AbuseError::Unavailable);
    }
    Ok(())
}
fn string_id(map: &mut Map<String, Value>, key: &str) -> Result<i64> {
    let id = value(map, key)?
        .as_i64()
        .filter(|id| *id > 0)
        .ok_or(AbuseError::Unavailable)?;
    map.insert(key.into(), Value::String(id.to_string()));
    Ok(id)
}
fn nullable_cursor(map: &mut Map<String, Value>, key: &str, last: Option<i64>) -> Result<()> {
    if value(map, key)?.is_null() {
        return Ok(());
    }
    let id = string_id(map, key)?;
    if Some(id) != last {
        return Err(AbuseError::Unavailable);
    }
    Ok(())
}
fn user_view(v: &mut Value) -> Result<i64> {
    let m = object(
        v,
        &[
            "user_id",
            "username",
            "email",
            "active",
            "platform_role",
            "revision",
            "marked",
            "policy_id",
        ],
    )?;
    if text(m, "username")?.chars().count() > 128
        || text(m, "email")?.chars().count() > 320
        || text(m, "username")?.contains('\0')
        || text(m, "email")?.contains('\0')
        || !matches!(text(m, "platform_role")?, "user" | "admin" | "superadmin")
    {
        return Err(AbuseError::Unavailable);
    }
    boolean(m, "active")?;
    if nonnegative(m, "revision")? == 0 && boolean(m, "marked")? {
        return Err(AbuseError::Unavailable);
    }
    marked_policy(m)?;
    string_id(m, "user_id")
}
fn array_mut<'a>(map: &'a mut Map<String, Value>, key: &str) -> Result<&'a mut Vec<Value>> {
    value_mut(map, key)?
        .as_array_mut()
        .ok_or(AbuseError::Unavailable)
}
fn bounded_array(values: &[Value], minimum: usize) -> Result<()> {
    if !(minimum..=200).contains(&values.len()) {
        return Err(AbuseError::Unavailable);
    }
    Ok(())
}
fn action(map: &Map<String, Value>) -> Result<&str> {
    let a = text(map, "action")?;
    if !matches!(a, "mark" | "unmark" | "convert_disabled") {
        return Err(AbuseError::Unavailable);
    }
    Ok(a)
}
fn user_list(mut v: Value) -> Result<Value> {
    let m = object(&mut v, &["version", "users", "next_after"])?;
    original_version(m)?;
    let users = array_mut(m, "users")?;
    bounded_array(users, 0)?;
    let mut last = None;
    for user in users {
        let id = user_view(user)?;
        if last.is_some_and(|previous| previous >= id) {
            return Err(AbuseError::Unavailable);
        }
        last = Some(id);
    }
    nullable_cursor(m, "next_after", last)?;
    m.insert("version".into(), Value::from(2));
    Ok(v)
}
fn detail(mut v: Value) -> Result<Value> {
    user_view(&mut v)?;
    v.as_object_mut()
        .ok_or(AbuseError::Unavailable)?
        .insert("version".into(), Value::from(2));
    Ok(v)
}
fn preview(mut v: Value) -> Result<Value> {
    let m = object(
        &mut v,
        &[
            "version",
            "preview_id",
            "preview_hash",
            "expires_at",
            "operation_id",
            "action",
            "policy_id",
            "policy_revision",
            "targets",
        ],
    )?;
    original_version(m)?;
    opaque_hash(m, "preview_id")?;
    opaque_hash(m, "preview_hash")?;
    integer(m, "expires_at")?;
    nonnegative(m, "policy_revision")?;
    operation_id(m)?;
    let unmark = action(m)? == "unmark";
    if unmark == policy_id(m, "policy_id")? {
        return Err(AbuseError::Unavailable);
    }
    let targets = array_mut(m, "targets")?;
    bounded_array(targets, 1)?;
    let mut last = None;
    for target in targets {
        let t = object(
            target,
            &[
                "user_id",
                "username",
                "active",
                "expected_revision",
                "before",
                "after",
            ],
        )?;
        if text(t, "username")?.chars().count() > 128 || text(t, "username")?.contains('\0') {
            return Err(AbuseError::Unavailable);
        }
        boolean(t, "active")?;
        nonnegative(t, "expected_revision")?;
        let before = object(value_mut(t, "before")?, &["marked", "policy_id"])?;
        marked_policy(before)?;
        let after = object(value_mut(t, "after")?, &["marked", "policy_id", "active"])?;
        marked_policy(after)?;
        boolean(after, "active")?;
        let id = string_id(t, "user_id")?;
        if last.is_some_and(|previous| previous >= id) {
            return Err(AbuseError::Unavailable);
        }
        last = Some(id);
    }
    m.insert("version".into(), Value::from(2));
    Ok(v)
}
fn operation(mut v: Value) -> Result<Value> {
    let kind = v
        .get("kind")
        .and_then(Value::as_str)
        .ok_or(AbuseError::Unavailable)?;
    let policies = match kind {
        "users" => false,
        "policies" => true,
        _ => return Err(AbuseError::Unavailable),
    };
    let fields = if policies {
        &[
            "version",
            "status",
            "operation_id",
            "kind",
            "policy_revision",
            "applied_config_hash",
            "users",
        ][..]
    } else {
        &[
            "version",
            "status",
            "operation_id",
            "kind",
            "policy_revision",
            "users",
        ][..]
    };
    let m = object(&mut v, fields)?;
    original_version(m)?;
    if text(m, "status")? != "applied" {
        return Err(AbuseError::Unavailable);
    }
    operation_id(m)?;
    nonnegative(m, "policy_revision")?;
    if policies {
        opaque_hash(m, "applied_config_hash")?;
        if !array_mut(m, "users")?.is_empty() {
            return Err(AbuseError::Unavailable);
        }
        return Ok(v);
    }
    let users = array_mut(m, "users")?;
    bounded_array(users, 1)?;
    let mut last = None;
    for user in users {
        let id = user_view(user)?;
        if last.is_some_and(|previous| previous >= id) {
            return Err(AbuseError::Unavailable);
        }
        last = Some(id);
    }
    m.insert("version".into(), Value::from(2));
    Ok(v)
}
fn audit_detail(v: &mut Value, users: bool) -> Result<()> {
    if users {
        let d = object(v, &["action", "policy_revision", "transitions"])?;
        action(d)?;
        nonnegative(d, "policy_revision")?;
        let transitions = array_mut(d, "transitions")?;
        bounded_array(transitions, 1)?;
        let mut last = None;
        for transition in transitions {
            let t = object(
                transition,
                &[
                    "user_id",
                    "before_revision",
                    "after_revision",
                    "before_marked",
                    "after_marked",
                    "before_active",
                    "after_active",
                    "policy_id",
                ],
            )?;
            let before = nonnegative(t, "before_revision")?;
            if before.checked_add(1) != Some(nonnegative(t, "after_revision")?) {
                return Err(AbuseError::Unavailable);
            }
            boolean(t, "before_marked")?;
            if boolean(t, "after_marked")? != policy_id(t, "policy_id")? {
                return Err(AbuseError::Unavailable);
            }
            boolean(t, "before_active")?;
            boolean(t, "after_active")?;
            let id = string_id(t, "user_id")?;
            if last.is_some_and(|previous| previous >= id) {
                return Err(AbuseError::Unavailable);
            }
            last = Some(id);
        }
    } else {
        let d = object(
            v,
            &[
                "before_revision",
                "after_revision",
                "policy_ids",
                "config_digest",
            ],
        )?;
        let before = nonnegative(d, "before_revision")?;
        if before.checked_add(1) != Some(nonnegative(d, "after_revision")?) {
            return Err(AbuseError::Unavailable);
        }
        opaque_hash(d, "config_digest")?;
        let ids = array_mut(d, "policy_ids")?;
        if ids.len() > 32 {
            return Err(AbuseError::Unavailable);
        }
        let mut seen = std::collections::BTreeSet::new();
        for id in ids {
            let id = id.as_str().ok_or(AbuseError::Unavailable)?;
            if !super::policy::identifier(id, 1, 64) || !seen.insert(id) {
                return Err(AbuseError::Unavailable);
            }
        }
    }
    Ok(())
}
fn audit(mut v: Value) -> Result<Value> {
    let m = object(&mut v, &["version", "events", "next_after"])?;
    original_version(m)?;
    let events = array_mut(m, "events")?;
    bounded_array(events, 0)?;
    let mut last = None;
    for event in events {
        let e = object(
            event,
            &[
                "id",
                "actor_user_id",
                "operation_id",
                "kind",
                "reason",
                "detail",
                "created_at",
            ],
        )?;
        let users = match text(e, "kind")? {
            "users" => true,
            "policies" => false,
            _ => return Err(AbuseError::Unavailable),
        };
        operation_id(e)?;
        mutation_fields(text(e, "operation_id")?, text(e, "reason")?)
            .map_err(|_| AbuseError::Unavailable)?;
        integer(e, "created_at")?;
        audit_detail(value_mut(e, "detail")?, users)?;
        string_id(e, "actor_user_id")?;
        let id = string_id(e, "id")?;
        if last.is_some_and(|previous| previous >= id) {
            return Err(AbuseError::Unavailable);
        }
        last = Some(id);
    }
    nullable_cursor(m, "next_after", last)?;
    m.insert("version".into(), Value::from(2));
    Ok(v)
}

pub(super) fn routes() -> Router<AbuseStore> {
    Router::new()
        .route("/core/v2/admin/abuse/users", get(get_users))
        .route("/core/v2/admin/abuse/users/{id}", get(get_user))
        .route("/core/v2/admin/abuse/users/preview", post(post_preview))
        .route("/core/v2/admin/abuse/users/commit", post(commit))
        .route("/core/v2/admin/abuse/operations/{id}", get(get_operation))
        .route("/core/v2/admin/abuse/audit", get(get_audit))
}
async fn get_users(
    State(s): State<AbuseStore>,
    h: HeaderMap,
    query: std::result::Result<Query<UserQueryV2>, axum::extract::rejection::QueryRejection>,
) -> Result<Json<Value>> {
    let Query(v) = query.map_err(|_| AbuseError::Invalid)?;
    Ok(Json(user_list(s.users(bearer(&h)?, &v.domain()?).await?)?))
}
async fn get_user(
    State(s): State<AbuseStore>,
    h: HeaderMap,
    Path(id): Path<String>,
) -> Result<Json<Value>> {
    let view = s.inspect_user(bearer(&h)?, positive_id(&id)?).await?;
    Ok(Json(detail(
        serde_json::to_value(view).map_err(|_| AbuseError::Unavailable)?,
    )?))
}
async fn post_preview(
    State(s): State<AbuseStore>,
    h: HeaderMap,
    body: std::result::Result<Json<PreviewRequestV2>, axum::extract::rejection::JsonRejection>,
) -> Result<Json<Value>> {
    let Json(v) = body.map_err(|_| AbuseError::Invalid)?;
    Ok(Json(preview(
        s.preview_users(bearer(&h)?, &v.domain()?).await?,
    )?))
}
async fn commit(
    State(s): State<AbuseStore>,
    h: HeaderMap,
    body: std::result::Result<Json<CommitRequest>, axum::extract::rejection::JsonRejection>,
) -> Result<Json<Value>> {
    let Json(v) = body.map_err(|_| AbuseError::Invalid)?;
    let result = operation(s.commit_users(bearer(&h)?, &v).await?)?;
    if result["kind"] != "users" || result["version"] != 2 {
        return Err(AbuseError::Unavailable);
    }
    Ok(Json(result))
}
async fn get_operation(
    State(s): State<AbuseStore>,
    h: HeaderMap,
    Path(id): Path<String>,
) -> Result<Json<Value>> {
    Ok(Json(operation(s.operation(bearer(&h)?, &id).await?)?))
}
async fn get_audit(
    State(s): State<AbuseStore>,
    h: HeaderMap,
    query: std::result::Result<Query<PageV2>, axum::extract::rejection::QueryRejection>,
) -> Result<Json<Value>> {
    let Query(v) = query.map_err(|_| AbuseError::Invalid)?;
    Ok(Json(audit(s.audit(bearer(&h)?, &v.domain()?).await?)?))
}

#[cfg(test)]
mod tests;
