use super::*;
use crate::identity::LoginRequest;
use axum::{extract::ConnectInfo, response::Redirect};
use rand::RngCore;
use std::net::SocketAddr;

const FLOW_COOKIE: &str = "__Host-lmm_google_flow";

pub(super) fn routes(store: IdentityStore) -> Router<IdentityStore> {
    Router::new()
        .route("/core/v1/auth/register", post(register))
        .route("/core/v1/auth/login", post(login))
        .route("/core/v1/auth/session/refresh", post(refresh))
        .route("/core/v1/auth/logout", post(logout))
        .route("/core/v1/auth/logout-all", post(logout_all))
        .route("/core/v1/auth/oauth/google", get(google_start))
        .route("/core/v1/auth/oauth/google/callback", get(google_callback))
        .route_layer(middleware::from_fn_with_state(store, rate_limit))
}
async fn rate_limit(State(store): State<IdentityStore>, request: Request, next: Next) -> Response {
    let peer = request
        .extensions()
        .get::<ConnectInfo<SocketAddr>>()
        .map(|p| p.0.ip().to_string())
        .unwrap_or_else(|| "unknown-peer".to_owned());
    match store.auth_rate_limit("peer", &peer, 60).await {
        Ok(true) => next.run(request).await,
        Ok(false) => (
            StatusCode::TOO_MANY_REQUESTS,
            [(header::RETRY_AFTER, "60")],
            Json(json!({"error":{"code":"identity_rate_limited"}})),
        )
            .into_response(),
        Err(e) => e.into_response(),
    }
}
async fn register(
    State(store): State<IdentityStore>,
    Json(body): Json<LoginRequest>,
) -> Result<impl IntoResponse> {
    Ok((StatusCode::CREATED, Json(store.register(&body).await?)))
}
async fn login(
    State(store): State<IdentityStore>,
    Json(body): Json<LoginRequest>,
) -> Result<impl IntoResponse> {
    Ok(Json(store.login(&body).await?))
}
async fn refresh(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
) -> Result<impl IntoResponse> {
    Ok(Json(store.refresh_session(bearer(&headers)?).await?))
}
async fn logout(State(store): State<IdentityStore>, headers: HeaderMap) -> Result<StatusCode> {
    store.logout(bearer(&headers)?).await?;
    Ok(StatusCode::NO_CONTENT)
}
async fn logout_all(State(store): State<IdentityStore>, headers: HeaderMap) -> Result<StatusCode> {
    store.logout_all(bearer(&headers)?).await?;
    Ok(StatusCode::NO_CONTENT)
}
pub(super) async fn list_credentials(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Query(query): Query<TeamQuery>,
) -> Result<impl IntoResponse> {
    Ok(Json(
        store
            .list_credentials(bearer(&headers)?, query.after, None)
            .await?,
    ))
}
async fn google_start(State(store): State<IdentityStore>) -> Result<Response> {
    let mut random = [0u8; 32];
    rand::rngs::OsRng
        .try_fill_bytes(&mut random)
        .map_err(|_| IdentityError::Storage)?;
    let binding: String = random.iter().map(|b| format!("{b:02x}")).collect();
    let url = store.begin_google_login(&binding).await?;
    let mut response = Redirect::to(&url).into_response();
    let cookie =
        format!("{FLOW_COOKIE}={binding}; Path=/; Secure; HttpOnly; SameSite=Lax; Max-Age=600");
    response.headers_mut().insert(
        header::SET_COOKIE,
        HeaderValue::from_str(&cookie).map_err(|_| IdentityError::Storage)?,
    );
    Ok(response)
}
fn flow_cookie(headers: &HeaderMap) -> Result<&str> {
    let mut found = None;
    for header in headers.get_all(header::COOKIE) {
        let header = header.to_str().map_err(|_| IdentityError::Unauthorized)?;
        for part in header.split(';') {
            if let Some((name, value)) = part.trim().split_once('=')
                && name == FLOW_COOKIE
            {
                if found.is_some()
                    || value.len() != 64
                    || !value.bytes().all(|b| b.is_ascii_hexdigit())
                {
                    return Err(IdentityError::Unauthorized);
                }
                found = Some(value);
            }
        }
    }
    found.ok_or(IdentityError::Unauthorized)
}
#[derive(Deserialize)]
struct Callback {
    state: Option<String>,
    code: Option<String>,
    error: Option<String>,
}
async fn google_callback(
    State(store): State<IdentityStore>,
    headers: HeaderMap,
    Query(query): Query<Callback>,
) -> Response {
    let result = async {
        if query.error.is_some() {
            return Err(IdentityError::Unauthorized);
        }
        let binding = flow_cookie(&headers)?;
        let state = query.state.as_deref().ok_or(IdentityError::Unauthorized)?;
        let code = query.code.as_deref().ok_or(IdentityError::Unauthorized)?;
        store.finish_google_login(state, binding, code).await
    }
    .await;
    let mut response = match result {
        Ok(issued) => Json(issued).into_response(),
        Err(error) => error.into_response(),
    };
    response.headers_mut().insert(
        header::SET_COOKIE,
        HeaderValue::from_static(
            "__Host-lmm_google_flow=; Path=/; Secure; HttpOnly; SameSite=Lax; Max-Age=0",
        ),
    );
    response
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn binding_cookie_rejects_missing_duplicates_and_unbounded_values() {
        let mut headers = HeaderMap::new();
        assert!(flow_cookie(&headers).is_err());
        headers.insert(
            header::COOKIE,
            HeaderValue::from_str(&format!("{FLOW_COOKIE}={}", "a".repeat(64))).unwrap(),
        );
        assert_eq!(flow_cookie(&headers).unwrap(), "a".repeat(64));
        headers.append(
            header::COOKIE,
            HeaderValue::from_str(&format!("{FLOW_COOKIE}={}", "b".repeat(64))).unwrap(),
        );
        assert!(flow_cookie(&headers).is_err());
    }
}
