use super::*;
use axum::{
    Json, Router,
    extract::{Query, State, rejection::QueryRejection},
    http::{HeaderMap, StatusCode, header},
    response::{IntoResponse, Response},
    routing::get,
};
fn bearer(h: &HeaderMap) -> ReadResult<&str> {
    let mut values = h.get_all(header::AUTHORIZATION).iter();
    let value = values
        .next()
        .ok_or(AdminUsersError::Unauthorized)?
        .to_str()
        .map_err(|_| AdminUsersError::Unauthorized)?;
    if values.next().is_some() {
        return Err(AdminUsersError::Unauthorized);
    }
    let (scheme, secret) = value.split_once(' ').ok_or(AdminUsersError::Unauthorized)?;
    if !scheme.eq_ignore_ascii_case("Bearer") {
        return Err(AdminUsersError::Unauthorized);
    }
    Ok(secret)
}
impl IntoResponse for AdminUsersError {
    fn into_response(self) -> Response {
        let (status, code) = match self {
            Self::Unauthorized => (StatusCode::UNAUTHORIZED, "ADMIN_USERS_UNAUTHORIZED"),
            Self::Forbidden => (StatusCode::FORBIDDEN, "ADMIN_USERS_FORBIDDEN"),
            Self::Invalid => (StatusCode::BAD_REQUEST, "ADMIN_USERS_INVALID"),
            Self::UnsupportedFilter => (
                StatusCode::UNPROCESSABLE_ENTITY,
                "ADMIN_USERS_FILTER_UNAVAILABLE",
            ),
            Self::UnsupportedSort => (
                StatusCode::UNPROCESSABLE_ENTITY,
                "ADMIN_USERS_SORT_UNAVAILABLE",
            ),
            Self::Unavailable => (StatusCode::SERVICE_UNAVAILABLE, "ADMIN_USERS_UNAVAILABLE"),
        };
        (status, Json(json!({"error":{"code":code}}))).into_response()
    }
}
pub fn router(store: AdminUsersStore) -> Router {
    Router::new()
        .route("/core/v1/admin/users", get(users))
        .route("/core/v1/admin/users/groups", get(groups))
        .layer(axum::middleware::from_fn(
            |request: axum::extract::Request, next: axum::middleware::Next| async move {
                let mut response = next.run(request).await;
                response.headers_mut().insert(
                    header::CACHE_CONTROL,
                    "private, no-store".parse().expect("static header"),
                );
                response.headers_mut().insert(
                    header::VARY,
                    "Authorization".parse().expect("static header"),
                );
                response
            },
        ))
        .with_state(store)
}
async fn users(
    State(store): State<AdminUsersStore>,
    headers: HeaderMap,
    input: std::result::Result<Query<UsersQuery>, QueryRejection>,
) -> ReadResult<Json<UsersPage>> {
    let secret = bearer(&headers)?;
    let Query(input) = input.map_err(|_| AdminUsersError::Invalid)?;
    Ok(Json(store.users(secret, &input).await?))
}
async fn groups(
    State(store): State<AdminUsersStore>,
    headers: HeaderMap,
    input: std::result::Result<Query<GroupsQuery>, QueryRejection>,
) -> ReadResult<Json<GroupsPage>> {
    let secret = bearer(&headers)?;
    let Query(input) = input.map_err(|_| AdminUsersError::Invalid)?;
    Ok(Json(store.groups(secret, &input).await?))
}
