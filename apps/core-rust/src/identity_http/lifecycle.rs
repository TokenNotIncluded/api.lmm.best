use super::*;
use crate::identity::MemberUpdate;
use axum::routing::patch;

pub(super) fn routes() -> Router<IdentityStore> {
    Router::new()
        .route("/core/v1/credentials", get(super::auth::list_credentials))
        .route("/core/v1/teams/{id}", patch(rename).delete(close))
        .route("/core/v1/teams/{id}/owner", post(transfer))
        .route("/core/v1/teams/{id}/members", get(members))
        .route("/core/v1/teams/{id}/keys", get(keys))
        .route("/core/v1/invites", get(inbox))
        .route("/core/v1/invites/{id}/accept", post(accept_by_id))
        .route("/core/v1/invites/{id}/reject", post(reject))
        .route("/core/v1/invites/{id}", delete(withdraw))
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct TransferRequest {
    new_owner_user_id: i64,
    expected_version: i64,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RenameRequest {
    name: String,
    expected_version: i64,
}
async fn members(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Path(id): Path<i64>,
    Query(q): Query<TeamQuery>,
) -> Result<impl IntoResponse> {
    Ok(Json(
        store.list_members(bearer(&headers)?, id, q.after).await?,
    ))
}
async fn keys(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Path(id): Path<i64>,
    Query(q): Query<TeamQuery>,
) -> Result<impl IntoResponse> {
    Ok(Json(
        store
            .list_credentials(bearer(&headers)?, q.after, Some(id))
            .await?,
    ))
}
pub(super) async fn update_member(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Path((team, user)): Path<(i64, i64)>,
    Json(body): Json<MemberUpdate>,
) -> Result<StatusCode> {
    store
        .update_member(bearer(&headers)?, team, user, &body)
        .await?;
    Ok(StatusCode::NO_CONTENT)
}
async fn transfer(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Path(id): Path<i64>,
    Json(body): Json<TransferRequest>,
) -> Result<StatusCode> {
    store
        .transfer_team(
            bearer(&headers)?,
            id,
            body.new_owner_user_id,
            body.expected_version,
        )
        .await?;
    Ok(StatusCode::NO_CONTENT)
}
async fn rename(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Path(id): Path<i64>,
    Json(body): Json<RenameRequest>,
) -> Result<StatusCode> {
    store
        .rename_team(bearer(&headers)?, id, &body.name, body.expected_version)
        .await?;
    Ok(StatusCode::NO_CONTENT)
}
async fn close(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Path(id): Path<i64>,
    Query(q): Query<RemoveQuery>,
) -> Result<StatusCode> {
    store.close_team(bearer(&headers)?, id, q.version).await?;
    Ok(StatusCode::NO_CONTENT)
}
async fn inbox(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Query(q): Query<TeamQuery>,
) -> Result<impl IntoResponse> {
    Ok(Json(
        store.list_invites(bearer(&headers)?, None, q.after).await?,
    ))
}
pub(super) async fn team_invites(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Path(id): Path<i64>,
    Query(q): Query<TeamQuery>,
) -> Result<impl IntoResponse> {
    Ok(Json(
        store
            .list_invites(bearer(&headers)?, Some(id), q.after)
            .await?,
    ))
}
async fn accept_by_id(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Path(id): Path<i64>,
) -> Result<impl IntoResponse> {
    Ok(Json(
        json!({"team_id":store.accept_invite_by_id(bearer(&headers)?, id).await?}),
    ))
}
async fn reject(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Path(id): Path<i64>,
) -> Result<StatusCode> {
    store.reject_invite(bearer(&headers)?, id).await?;
    Ok(StatusCode::NO_CONTENT)
}
async fn withdraw(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Path(id): Path<i64>,
) -> Result<StatusCode> {
    store.withdraw_invite(bearer(&headers)?, id).await?;
    Ok(StatusCode::NO_CONTENT)
}
