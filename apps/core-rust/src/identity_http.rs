mod auth;
mod lifecycle;
use crate::{
    accounts::{Account, AccountKind},
    funding::FundingPolicy,
    identity::{IdentityError, IdentityStore, InviteRequest},
};
use axum::{
    Json, Router,
    extract::{DefaultBodyLimit, Path, Query, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{delete, get, post},
};
use serde::Deserialize;
use serde_json::{Value, json};

type Result<T> = std::result::Result<T, IdentityError>;

pub fn router(store: IdentityStore) -> Router {
    Router::new()
        .merge(auth::routes(store.clone()))
        .merge(lifecycle::routes())
        .route("/core/v1/identity", get(identity))
        .route("/core/v1/keys", post(issue_key))
        .route("/core/v1/credentials/{id}", delete(revoke))
        .route("/core/v1/teams", get(list_teams).post(create_team))
        .route(
            "/core/v1/teams/{id}/invites",
            post(invite).get(lifecycle::team_invites),
        )
        .route("/core/v1/invites/accept", post(accept_invite))
        .route(
            "/core/v1/teams/{team_id}/members/{user_id}",
            delete(remove_member).patch(lifecycle::update_member),
        )
        .layer(DefaultBodyLimit::max(16 * 1024))
        .layer(middleware::from_fn(no_store))
        .with_state(store)
}
async fn no_store(request: Request, next: Next) -> Response {
    let mut response = next.run(request).await;
    response
        .headers_mut()
        .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    response.headers_mut().insert(
        header::REFERRER_POLICY,
        HeaderValue::from_static("no-referrer"),
    );
    response.headers_mut().insert(
        header::X_CONTENT_TYPE_OPTIONS,
        HeaderValue::from_static("nosniff"),
    );
    response.headers_mut().insert(
        header::CONTENT_SECURITY_POLICY,
        HeaderValue::from_static("default-src 'none'; frame-ancestors 'none'"),
    );
    response
}
fn bearer(headers: &HeaderMap) -> Result<&str> {
    if headers.get_all(header::AUTHORIZATION).iter().count() != 1 {
        return Err(IdentityError::Unauthorized);
    }
    let raw = headers
        .get(header::AUTHORIZATION)
        .and_then(|h| h.to_str().ok())
        .ok_or(IdentityError::Unauthorized)?;
    let (scheme, secret) = raw.split_once(' ').ok_or(IdentityError::Unauthorized)?;
    if !scheme.eq_ignore_ascii_case("Bearer") {
        return Err(IdentityError::Unauthorized);
    }
    Ok(secret)
}
impl IntoResponse for IdentityError {
    fn into_response(self) -> Response {
        let (status, code) = match self {
            Self::Unauthorized => (StatusCode::UNAUTHORIZED, "identity_unauthorized"),
            Self::Forbidden => (StatusCode::FORBIDDEN, "identity_forbidden"),
            Self::Invalid => (StatusCode::BAD_REQUEST, "identity_invalid"),
            Self::Conflict => (StatusCode::CONFLICT, "identity_conflict"),
            Self::Storage => (StatusCode::SERVICE_UNAVAILABLE, "identity_unavailable"),
        };
        let mut response = (status, Json(json!({"error":{"code":code}}))).into_response();
        if status == StatusCode::UNAUTHORIZED {
            response
                .headers_mut()
                .insert(header::WWW_AUTHENTICATE, HeaderValue::from_static("Bearer"));
        }
        response
    }
}
async fn identity(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
) -> Result<impl IntoResponse> {
    Ok(Json(store.authorize(bearer(&headers)?).await?))
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct KeyRequest {
    owner: Account,
    funding_policy: Option<FundingPolicy>,
    ttl_seconds: i64,
}
async fn issue_key(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Json(body): Json<KeyRequest>,
) -> Result<impl IntoResponse> {
    let policy = body.funding_policy.unwrap_or_else(|| {
        if body.owner.kind == AccountKind::Personal {
            FundingPolicy::new_personal_key()
        } else {
            FundingPolicy::inherit_account_settings()
        }
    });
    Ok((
        StatusCode::CREATED,
        Json(
            store
                .issue_key(bearer(&headers)?, body.owner, policy, body.ttl_seconds)
                .await?,
        ),
    ))
}
async fn revoke(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Path(id): Path<i64>,
) -> Result<StatusCode> {
    store.revoke(bearer(&headers)?, id).await?;
    Ok(StatusCode::NO_CONTENT)
}
#[derive(Default, Deserialize)]
#[serde(deny_unknown_fields)]
struct TeamQuery {
    #[serde(default)]
    after: i64,
}
async fn list_teams(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Query(query): Query<TeamQuery>,
) -> Result<impl IntoResponse> {
    Ok(Json(
        store.list_teams(bearer(&headers)?, query.after).await?,
    ))
}
async fn create_team(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
) -> Result<(StatusCode, Json<Value>)> {
    let id = store.create_team(bearer(&headers)?).await?;
    Ok((StatusCode::CREATED, Json(json!({"id":id}))))
}
async fn invite(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Path(id): Path<i64>,
    Json(body): Json<InviteRequest>,
) -> Result<impl IntoResponse> {
    Ok((
        StatusCode::CREATED,
        Json(store.invite(bearer(&headers)?, id, &body).await?),
    ))
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AcceptRequest {
    token: String,
}
async fn accept_invite(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Json(body): Json<AcceptRequest>,
) -> Result<Json<Value>> {
    let id = store.accept_invite(bearer(&headers)?, &body.token).await?;
    Ok(Json(json!({"team_id":id})))
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RemoveQuery {
    version: i64,
}
async fn remove_member(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Path((team, user)): Path<(i64, i64)>,
    Query(query): Query<RemoveQuery>,
) -> Result<StatusCode> {
    store
        .remove_member(bearer(&headers)?, team, user, query.version)
        .await?;
    Ok(StatusCode::NO_CONTENT)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn duplicate_and_non_bearer_headers_are_rejected() {
        let mut headers = HeaderMap::new();
        assert_eq!(bearer(&headers), Err(IdentityError::Unauthorized));
        headers.insert(
            header::AUTHORIZATION,
            HeaderValue::from_static("Basic test"),
        );
        assert_eq!(bearer(&headers), Err(IdentityError::Unauthorized));
        headers.insert(
            header::AUTHORIZATION,
            HeaderValue::from_static("bEaReR a-token"),
        );
        assert_eq!(bearer(&headers), Ok("a-token"));
        headers.append(
            header::AUTHORIZATION,
            HeaderValue::from_static("Bearer other"),
        );
        assert_eq!(bearer(&headers), Err(IdentityError::Unauthorized));
    }
}
