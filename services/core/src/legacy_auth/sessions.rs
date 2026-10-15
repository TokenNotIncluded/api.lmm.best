//! Browser session routes share the parent's origin and no-store middleware.
use super::*;
use axum::extract::Path;

pub(super) fn routes() -> Router<AuthState> {
    Router::new()
        .route("/api/user/sessions", get(list))
        .route("/api/user/sessions/settings", axum::routing::put(settings))
        .route("/api/user/sessions/revoke-others", post(revoke_others))
        .route("/api/user/sessions/{sid}", axum::routing::delete(revoke))
}
async fn list(State(state): State<AuthState>, headers: HeaderMap) -> Response {
    let result = async {
        state
            .browser
            .login_sessions(bearer(&headers)?.ok_or(IdentityError::Unauthorized)?)
            .await
    }
    .await;
    match result {
        Ok(data) => Json(data).into_response(),
        Err(error) => auth_failure(error),
    }
}
async fn revoke(
    State(state): State<AuthState>,
    headers: HeaderMap,
    Path(sid): Path<String>,
) -> Response {
    let result = async {
        state
            .browser
            .revoke_login_sessions(
                bearer(&headers)?.ok_or(IdentityError::Unauthorized)?,
                Some(&sid),
                expected_sid(&headers)?,
            )
            .await
    }
    .await;
    match result {
        Ok((current, _)) => {
            let response = success(Some(json!({"revoked_sid":sid,"current":current})));
            if current {
                set_cookie(response, None, &state.config)
            } else {
                response
            }
        }
        Err(error) => auth_failure(error),
    }
}
async fn revoke_others(State(state): State<AuthState>, headers: HeaderMap) -> Response {
    let result = async {
        state
            .browser
            .revoke_login_sessions(
                bearer(&headers)?.ok_or(IdentityError::Unauthorized)?,
                None,
                expected_sid(&headers)?,
            )
            .await
    }
    .await;
    match result {
        Ok((_, count)) => success(Some(json!({"revoked_count":count}))),
        Err(error) => auth_failure(error),
    }
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Settings {
    session_auto_logout: bool,
}
async fn settings(
    State(state): State<AuthState>,
    headers: HeaderMap,
    body: std::result::Result<Json<Settings>, JsonRejection>,
) -> Response {
    let result = async {
        let Json(body) = body.map_err(|_| IdentityError::Invalid)?;
        state
            .browser
            .update_login_session_settings(
                bearer(&headers)?.ok_or(IdentityError::Unauthorized)?,
                body.session_auto_logout,
                expected_sid(&headers)?,
            )
            .await
    }
    .await;
    match result {
        Ok(()) => success(None),
        Err(error) => auth_failure(error),
    }
}
