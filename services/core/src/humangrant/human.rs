use super::{Error, HgState, Permission, Result, digest, exact_header, permissions, token};
use axum::{
    Json,
    extract::{Path, State},
    http::{HeaderMap, StatusCode, header},
    response::{IntoResponse, Response},
};
use serde::Deserialize;
use serde_json::{Value, json};
use sqlx::Row;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Empty {}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Approval {
    permissions: Vec<Permission>,
    expires_at: i64,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct Setting {
    enabled: bool,
}

fn html(page: &'static str) -> Response {
    let body = format!(
        r#"<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>HumanGrant | LMM API</title><link rel="stylesheet" href="/human-grant/review.css"></head><body><main id="human-grant" data-page="{page}"><header><h1>HumanGrant</h1><p>Authorize an AI to use only selected account permissions.</p></header><section id="status" role="status">Loading…</section><section id="request" hidden><p id="identity"></p><dl id="details"></dl><p id="purpose"></p><fieldset id="permissions"><legend>Permissions</legend></fieldset><p id="expires"></p><div class="actions"><button id="begin">Verify my account</button><button id="approve" disabled>Approve selected permissions</button><button id="reject" disabled>Reject</button></div></section><section id="grant-list" hidden></section><footer>Never share your password, cookies, or private keys with an AI.</footer></main><script defer src="/human-grant/review.js"></script></body></html>"#
    );
    let mut response = ([(header::CONTENT_TYPE, "text/html; charset=utf-8")], body).into_response();
    response.headers_mut().insert(header::CONTENT_SECURITY_POLICY,
        "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
            .parse().expect("static CSP"));
    response
}

pub(super) async fn review_page(
    State(state): State<HgState>,
    Path(link): Path<String>,
) -> Result<Response> {
    state.require_enabled().await?;
    state.request_by_link(&link).await?;
    Ok(html("review"))
}
pub(super) async fn landing_page() -> Response {
    html("intro")
}
pub(super) async fn grants_page() -> Response {
    html("grants")
}

pub(super) async fn script() -> impl IntoResponse {
    (
        [(
            header::CONTENT_TYPE,
            "application/javascript; charset=utf-8",
        )],
        include_str!("review.js"),
    )
}
pub(super) async fn css() -> impl IntoResponse {
    (
        [(header::CONTENT_TYPE, "text/css; charset=utf-8")],
        include_str!("review.css"),
    )
}
pub(super) async fn details(
    State(state): State<HgState>,
    Path(link): Path<String>,
) -> Result<Json<Value>> {
    state.require_enabled().await?;
    let row = state.request_by_link(&link).await?;
    Ok(Json(json!({
        "actor_id":row.try_get::<String,_>("actor_id")?,
        "client_id":row.try_get::<String,_>("client_id")?,
        "target_service":row.try_get::<String,_>("target_service")?,
        "purpose":row.try_get::<String,_>("purpose")?,
        "permissions":row.try_get::<Value,_>("requested")?,
        "requested_ttl_seconds":row.try_get::<i32,_>("requested_ttl_seconds")?,
        "phase":row.try_get::<String,_>("phase")?,
        "expires_at":sqlx::query_scalar::<_,i64>("SELECT EXTRACT(EPOCH FROM expires_at)::bigint FROM core_identity.hg_requests WHERE id=$1")
            .bind(row.try_get::<String,_>("id")?).fetch_one(&state.pool).await?,
    })))
}
pub(super) async fn begin(
    State(state): State<HgState>,
    Path(link): Path<String>,
    headers: HeaderMap,
    Json(_body): Json<Empty>,
) -> Result<Json<Value>> {
    state.require_enabled().await?;
    let (user, bearer, _role) = state.browser_user(&headers, true).await?;
    let mut tx = state.pool.begin().await?;
    let row=sqlx::query("SELECT id,phase,expected_human_id,expires_at FROM core_identity.hg_requests WHERE link_digest=$1 FOR UPDATE")
        .bind(digest(link.as_bytes())).fetch_optional(&mut *tx).await?.ok_or_else(Error::not_found)?;
    let id: String = row.try_get("id")?;
    let expected: Option<i64> = row.try_get("expected_human_id")?;
    if expected.is_some_and(|v| v != user) {
        return Err(Error::forbidden());
    }
    if row.try_get::<String, _>("phase")? != "waiting_human" {
        return Err(Error::state());
    }
    let live: bool = sqlx::query_scalar(
        "SELECT expires_at>clock_timestamp() FROM core_identity.hg_requests WHERE id=$1",
    )
    .bind(&id)
    .fetch_one(&mut *tx)
    .await?;
    if !live {
        return Err(Error::expired());
    }
    let csrf = token(32);
    sqlx::query("INSERT INTO core_identity.hg_reviews(csrf_digest,access_digest,request_id,user_id,expires_at) SELECT $1,$2,$3,$4,LEAST(expires_at,clock_timestamp()+interval '5 minutes') FROM core_identity.hg_requests WHERE id=$3")
        .bind(digest(csrf.as_bytes())).bind(digest(bearer.as_bytes())).bind(&id).bind(user)
        .execute(&mut *tx).await?;
    sqlx::query("UPDATE core_identity.hg_requests SET phase='awaiting_consent',status_updated_at=clock_timestamp() WHERE id=$1")
        .bind(&id).execute(&mut *tx).await?;
    sqlx::query("INSERT INTO core_identity.hg_audit(request_id,user_id,action) VALUES ($1,$2,'review.verified')")
        .bind(&id).bind(user).execute(&mut *tx).await?;
    let display: String =
        sqlx::query_scalar("SELECT username FROM core_identity.user_profiles WHERE user_id=$1")
            .bind(user)
            .fetch_one(&mut *tx)
            .await?;
    tx.commit().await?;
    Ok(Json(
        json!({"csrf_token":csrf,"grantor_id":user.to_string(),"username":display,"status":"awaiting_consent"}),
    ))
}
fn csrf(headers: &HeaderMap) -> Result<String> {
    let raw = exact_header(headers, "x-humangrant-csrf")?;
    if raw.len() != 43
        || !raw
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
    {
        return Err(Error::forbidden());
    }
    Ok(raw.to_owned())
}
async fn verified_review(
    tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    csrf_token: &str,
    bearer: &str,
    user: i64,
    id: &str,
) -> Result<()> {
    let found=sqlx::query("SELECT csrf_digest FROM core_identity.hg_reviews WHERE csrf_digest=$1 AND access_digest=$2 AND request_id=$3 AND user_id=$4 AND expires_at>clock_timestamp() AND consumed_at IS NULL FOR UPDATE")
        .bind(digest(csrf_token.as_bytes()))
        .bind(digest(bearer.as_bytes()))
        .bind(id).bind(user)
        .fetch_optional(&mut **tx).await?;
    if found.is_none() {
        return Err(Error::forbidden());
    }
    Ok(())
}
pub(super) async fn approve(
    State(state): State<HgState>,
    Path(link): Path<String>,
    headers: HeaderMap,
    Json(body): Json<Approval>,
) -> Result<Json<Value>> {
    state.require_enabled().await?;
    permissions(&body.permissions)?;
    let (user, bearer, _role) = state.browser_user(&headers, true).await?;
    let csrf = csrf(&headers)?;
    let request = state.request_by_link(&link).await?;
    let id: String = request.try_get("id")?;
    let mut tx = state.pool.begin().await?;
    verified_review(&mut tx, &csrf, &bearer, user, &id).await?;
    let row=sqlx::query("SELECT phase,expected_human_id,requested,requested_ttl_seconds FROM core_identity.hg_requests WHERE id=$1 FOR UPDATE")
        .bind(&id).fetch_one(&mut *tx).await?;
    if row.try_get::<String, _>("phase")? != "awaiting_consent" {
        return Err(Error::state());
    }
    if row
        .try_get::<Option<i64>, _>("expected_human_id")?
        .is_some_and(|v| v != user)
    {
        return Err(Error::forbidden());
    }
    let live: bool = sqlx::query_scalar(
        "SELECT expires_at>clock_timestamp() FROM core_identity.hg_requests WHERE id=$1",
    )
    .bind(&id)
    .fetch_one(&mut *tx)
    .await?;
    if !live {
        return Err(Error::expired());
    }
    let permitted: Vec<Permission> =
        serde_json::from_value(row.try_get("requested")?).map_err(|_| Error::internal())?;
    if !body.permissions.iter().all(|p| permitted.contains(p)) {
        return Err(Error::forbidden());
    }
    let ttl: i32 = row.try_get("requested_ttl_seconds")?;
    let now = super::crypto::now();
    if body.expires_at <= now || body.expires_at > now + i64::from(ttl) {
        return Err(Error::invalid());
    }
    // Current account is checked under a shared lock. The only current
    // grantable action is read-only access to the grantor's own profile.
    let valid=sqlx::query("SELECT u.id FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id WHERE u.id=$1 AND u.active AND a.active FOR SHARE OF u,a")
        .bind(user).fetch_optional(&mut *tx).await?;
    if valid.is_none() {
        return Err(Error::forbidden());
    }
    let gid = token(16);
    sqlx::query("INSERT INTO core_identity.hg_grants(id,request_id,user_id,actor_id,permissions,expires_at) VALUES ($1,$2,$3,$4,$5,to_timestamp($6))")
        .bind(&gid).bind(&id).bind(user).bind(request.try_get::<String,_>("actor_id")?)
        .bind(json!(body.permissions)).bind(body.expires_at)
        .execute(&mut *tx).await?;
    sqlx::query("UPDATE core_identity.hg_requests SET phase='approved',status_updated_at=clock_timestamp() WHERE id=$1")
        .bind(&id).execute(&mut *tx).await?;
    sqlx::query(
        "UPDATE core_identity.hg_reviews SET consumed_at=clock_timestamp() WHERE csrf_digest=$1",
    )
    .bind(digest(csrf.as_bytes()))
    .execute(&mut *tx)
    .await?;
    sqlx::query("INSERT INTO core_identity.hg_audit(request_id,user_id,action) VALUES ($1,$2,'grant.approved')")
        .bind(&id).bind(user).execute(&mut *tx).await?;
    tx.commit().await?;
    Ok(Json(
        json!({"status":"approved","grant_id":gid,"expires_at":body.expires_at}),
    ))
}
pub(super) async fn reject(
    State(state): State<HgState>,
    Path(link): Path<String>,
    headers: HeaderMap,
    Json(_body): Json<Empty>,
) -> Result<Json<Value>> {
    state.require_enabled().await?;
    let (user, bearer, _role) = state.browser_user(&headers, true).await?;
    let csrf = csrf(&headers)?;
    let req = state.request_by_link(&link).await?;
    let id: String = req.try_get("id")?;
    let mut tx = state.pool.begin().await?;
    verified_review(&mut tx, &csrf, &bearer, user, &id).await?;
    let phase: String =
        sqlx::query_scalar("SELECT phase FROM core_identity.hg_requests WHERE id=$1 FOR UPDATE")
            .bind(&id)
            .fetch_one(&mut *tx)
            .await?;
    if phase != "awaiting_consent" {
        return Err(Error::state());
    }
    sqlx::query("UPDATE core_identity.hg_requests SET phase='rejected',status_updated_at=clock_timestamp() WHERE id=$1")
        .bind(&id).execute(&mut *tx).await?;
    sqlx::query(
        "UPDATE core_identity.hg_reviews SET consumed_at=clock_timestamp() WHERE csrf_digest=$1",
    )
    .bind(digest(csrf.as_bytes()))
    .execute(&mut *tx)
    .await?;
    sqlx::query("INSERT INTO core_identity.hg_audit(request_id,user_id,action) VALUES ($1,$2,'grant.rejected')")
        .bind(&id).bind(user).execute(&mut *tx).await?;
    tx.commit().await?;
    Ok(Json(json!({"status":"rejected"})))
}
pub(super) async fn settings(State(state): State<HgState>) -> Result<Json<Value>> {
    Ok(Json(json!({"enabled":state.enabled().await?})))
}
pub(super) async fn set_settings(
    State(state): State<HgState>,
    headers: HeaderMap,
    Json(body): Json<Setting>,
) -> Result<Json<Value>> {
    let (user, _bearer, role) = state.browser_user(&headers, true).await?;
    if role < 10 {
        return Err(Error::forbidden());
    }
    let mut tx = state.pool.begin().await?;
    sqlx::query("UPDATE core_identity.hg_config SET enabled=$1 WHERE singleton=TRUE")
        .bind(body.enabled)
        .execute(&mut *tx)
        .await?;
    sqlx::query("INSERT INTO core_identity.hg_audit(user_id,action) VALUES ($1,$2)")
        .bind(user)
        .bind(if body.enabled {
            "settings.enabled"
        } else {
            "settings.disabled"
        })
        .execute(&mut *tx)
        .await?;
    tx.commit().await?;
    Ok(Json(json!({"enabled":body.enabled})))
}
pub(super) async fn grants(
    State(state): State<HgState>,
    headers: HeaderMap,
) -> Result<Json<Value>> {
    let (user, bearer, _role) = state.browser_user(&headers, false).await?;
    let limited = state
        .store
        .auth_rate_limit("hg_grants", &user.to_string(), 60)
        .await
        .map_err(|_| Error::internal())?;
    if !limited {
        return Err(Error {
            status: StatusCode::TOO_MANY_REQUESTS,
            code: "rate_limited",
        });
    }
    let rows=sqlx::query("SELECT id,request_id,actor_id,permissions,revoked_at IS NOT NULL AS revoked,EXTRACT(EPOCH FROM expires_at)::bigint AS expiry FROM core_identity.hg_grants WHERE user_id=$1 ORDER BY created_at DESC LIMIT 30")
        .bind(user).fetch_all(&state.pool).await?;
    let mut result = Vec::new();
    for row in rows {
        let id: String = row.try_get("id")?;
        let reqid: String = row.try_get("request_id")?;
        let revoked: bool = row.try_get("revoked")?;
        let expiry: i64 = row.try_get("expiry")?;
        let csrf_token = if !revoked && expiry > super::crypto::now() {
            let token = token(32);
            sqlx::query("INSERT INTO core_identity.hg_reviews(csrf_digest,access_digest,request_id,user_id,expires_at) VALUES ($1,$2,$3,$4,clock_timestamp()+interval '5 minutes')")
                .bind(digest(token.as_bytes())).bind(digest(bearer.as_bytes())).bind(&reqid).bind(user)
                .execute(&state.pool).await?;
            Some(token)
        } else {
            None
        };
        result.push(json!({
            "grant_id":id,
            "actor_id":row.try_get::<String,_>("actor_id")?,
            "permissions":row.try_get::<Value,_>("permissions")?,
            "expires_at":expiry,
            "revoked":revoked,
            "csrf_token":csrf_token
        }));
    }
    Ok(Json(json!({"grants":result})))
}
pub(super) async fn revoke(
    State(state): State<HgState>,
    Path(id): Path<String>,
    headers: HeaderMap,
    Json(_body): Json<Empty>,
) -> Result<Json<Value>> {
    let (user, bearer, _role) = state.browser_user(&headers, true).await?;
    let csrf = csrf(&headers)?;
    let mut tx = state.pool.begin().await?;
    let request_id: Option<String> = sqlx::query_scalar(
        "SELECT request_id FROM core_identity.hg_grants WHERE id=$1 AND user_id=$2",
    )
    .bind(&id)
    .bind(user)
    .fetch_optional(&mut *tx)
    .await?;
    let request_id = request_id.ok_or_else(Error::not_found)?;
    verified_review(&mut tx, &csrf, &bearer, user, &request_id).await?;
    let row=sqlx::query("SELECT revoked_at IS NOT NULL AS revoked FROM core_identity.hg_grants WHERE id=$1 AND user_id=$2 FOR UPDATE")
        .bind(&id).bind(user).fetch_optional(&mut *tx).await?.ok_or_else(Error::not_found)?;
    if row.try_get::<bool, _>("revoked")? {
        return Err(Error::state());
    }
    sqlx::query("UPDATE core_identity.hg_grants SET revoked_at=clock_timestamp() WHERE id=$1")
        .bind(&id)
        .execute(&mut *tx)
        .await?;
    sqlx::query(
        "UPDATE core_identity.hg_reviews SET consumed_at=clock_timestamp() WHERE csrf_digest=$1",
    )
    .bind(digest(csrf.as_bytes()))
    .execute(&mut *tx)
    .await?;
    sqlx::query("INSERT INTO core_identity.hg_audit(request_id,user_id,action) VALUES ($1,$2,'grant.revoked')")
        .bind(&request_id).bind(user).execute(&mut *tx).await?;
    tx.commit().await?;
    Ok(Json(json!({"status":"revoked","grant_id":id})))
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn consent_requires_strictly_positive_grant() {
        let permission = Permission {
            action: "lmm.profile.read".into(),
            resource: "self".into(),
        };
        assert!(permissions(&[permission]).is_ok());
        assert!(permissions(&[]).is_err());
    }
}
