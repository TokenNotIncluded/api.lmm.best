//! Native public-client OAuth transport; browser consent is separately scoped.
use crate::identity::{
    IdentityError,
    grants::{LmmIssuer, ReadError, RevokeForm, TokenForm},
};
use axum::{
    Form, Json, Router,
    body::Body,
    extract::Request,
    extract::{State, rejection::FormRejection},
    http::{HeaderMap, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use serde_json::{Value, json};

pub fn router(issuer: LmmIssuer) -> Router {
    Router::new()
        .route("/.well-known/oauth-authorization-server", get(metadata))
        .route(
            "/.well-known/oauth-protected-resource/api/oauth2",
            get(resource_metadata),
        )
        .route("/api/oauth2/token", post(token))
        .route("/api/oauth2/revoke", post(revoke))
        .route("/api/oauth2/catalog", get(catalog))
        .route("/api/oauth2/balance", get(balance))
        .route("/api/oauth2/usage", get(usage))
        .layer(axum::extract::DefaultBodyLimit::max(16 * 1024))
        .layer(middleware::from_fn_with_state(issuer.clone(), security))
        .with_state(issuer)
}
async fn security(State(issuer): State<LmmIssuer>, request: Request, next: Next) -> Response {
    let peer = request
        .extensions()
        .get::<axum::extract::ConnectInfo<std::net::SocketAddr>>()
        .map(|peer| peer.0.ip().to_string())
        .unwrap_or_else(|| "unknown-peer".into());
    let mut response = match issuer.rate_limit(&peer).await {
        Ok(true) => next.run(request).await,
        Ok(false) => {
            let mut response = (
                StatusCode::TOO_MANY_REQUESTS,
                Json(json!({"error":"temporarily_unavailable"})),
            )
                .into_response();
            response.headers_mut().insert(
                header::RETRY_AFTER,
                axum::http::HeaderValue::from_static("60"),
            );
            response
        }
        Err(error) => protocol_error(error),
    };
    // Originless native requests are expected. There is no cookie credential
    // fallback and no permissive CORS layer on this router. Every response,
    // including the limiter and storage errors, receives these headers.
    for (name, value) in [
        (header::CACHE_CONTROL, "no-store"),
        (header::PRAGMA, "no-cache"),
        (header::REFERRER_POLICY, "no-referrer"),
        (header::X_CONTENT_TYPE_OPTIONS, "nosniff"),
    ] {
        response
            .headers_mut()
            .insert(name, axum::http::HeaderValue::from_static(value));
    }
    response
}
async fn metadata(State(issuer): State<LmmIssuer>) -> Json<Value> {
    let p = issuer.profile();
    let resource = p.resource();
    let mut document = json!({"issuer":p.issuer(),"authorization_endpoint":format!("{resource}/authorize"),"token_endpoint":format!("{resource}/token"),"revocation_endpoint":format!("{resource}/revoke"),"response_types_supported":["code"],"grant_types_supported":["authorization_code","refresh_token"],"code_challenge_methods_supported":["S256"],"token_endpoint_auth_methods_supported":["none"],"authorization_response_iss_parameter_supported":true,"scopes_supported":["catalog:read","balance:read"],"lmm_client_ids_supported":["lmm"]});
    if p.pi_budget_limit().is_some() {
        document["lmm_client_ids_supported"] = json!(["lmm", "lmm-pi"]);
        document["scopes_supported"] = json!([
            "catalog:read",
            "balance:read",
            "usage:read",
            "models:invoke"
        ]);
    }
    Json(document)
}
async fn resource_metadata(State(issuer): State<LmmIssuer>) -> Json<Value> {
    Json(
        json!({"resource":issuer.profile().resource(),"authorization_servers":[issuer.profile().issuer()]}),
    )
}
fn protocol_error(error: IdentityError) -> Response {
    let (status, code) = match error {
        IdentityError::Invalid => (StatusCode::BAD_REQUEST, "invalid_request"),
        IdentityError::Unauthorized => (StatusCode::BAD_REQUEST, "invalid_grant"),
        IdentityError::Forbidden => (StatusCode::BAD_REQUEST, "invalid_scope"),
        IdentityError::Conflict => (StatusCode::CONFLICT, "temporarily_unavailable"),
        IdentityError::Storage => (StatusCode::SERVICE_UNAVAILABLE, "temporarily_unavailable"),
    };
    (status, Json(json!({"error":code}))).into_response()
}
fn no_native_credentials(headers: &HeaderMap) -> bool {
    ![
        "authorization",
        "x-api-key",
        "x-goog-api-key",
        "x-auth-session",
    ]
    .iter()
    .any(|name| headers.contains_key(*name))
}
async fn token(
    State(issuer): State<LmmIssuer>,
    headers: HeaderMap,
    uri: axum::http::Uri,
    input: Result<Form<TokenForm>, FormRejection>,
) -> Response {
    if uri.query().is_some() || !no_native_credentials(&headers) {
        return protocol_error(IdentityError::Invalid);
    }
    let Ok(Form(form)) = input else {
        return protocol_error(IdentityError::Invalid);
    };
    let request = match form.validate(issuer.profile()) {
        Ok(v) => v,
        Err(e) => return protocol_error(e),
    };
    match issuer.token(request).await {
        Ok(token) => Json(token).into_response(),
        Err(e) => protocol_error(e),
    }
}
async fn revoke(
    State(issuer): State<LmmIssuer>,
    headers: HeaderMap,
    uri: axum::http::Uri,
    input: Result<Form<RevokeForm>, FormRejection>,
) -> Response {
    if uri.query().is_some() || !no_native_credentials(&headers) {
        return protocol_error(IdentityError::Invalid);
    }
    let Ok(Form(form)) = input else {
        return protocol_error(IdentityError::Invalid);
    };
    if let Err(e) = form.validate() {
        return protocol_error(e);
    }
    match issuer.revoke_for_client(&form.client_id, &form.token).await {
        Ok(()) => (StatusCode::OK, Body::empty()).into_response(),
        Err(e) => protocol_error(e),
    }
}
fn bearer(headers: &HeaderMap) -> Result<&str, IdentityError> {
    if ["x-api-key", "x-goog-api-key", "x-auth-session"]
        .iter()
        .any(|name| headers.contains_key(*name))
    {
        return Err(IdentityError::Unauthorized);
    }
    let mut values = headers.get_all(header::AUTHORIZATION).iter();
    let value = values
        .next()
        .ok_or(IdentityError::Unauthorized)?
        .to_str()
        .map_err(|_| IdentityError::Unauthorized)?;
    if values.next().is_some() {
        return Err(IdentityError::Unauthorized);
    }
    let token = value
        .strip_prefix("Bearer ")
        .ok_or(IdentityError::Unauthorized)?;
    if token.is_empty() || token.bytes().any(|v| v.is_ascii_whitespace()) {
        return Err(IdentityError::Unauthorized);
    }
    Ok(token)
}
fn read_error(error: ReadError) -> Response {
    match error {
        ReadError::Unavailable => (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(json!({"error":"temporarily_unavailable"})),
        )
            .into_response(),
        ReadError::Identity(IdentityError::Unauthorized) => {
            let mut response = (
                StatusCode::UNAUTHORIZED,
                Json(json!({"error":"invalid_token"})),
            )
                .into_response();
            response.headers_mut().insert(
                header::WWW_AUTHENTICATE,
                axum::http::HeaderValue::from_static("Bearer error=\"invalid_token\""),
            );
            response
        }
        ReadError::Identity(IdentityError::Forbidden) => (
            StatusCode::FORBIDDEN,
            Json(json!({"error":"insufficient_scope"})),
        )
            .into_response(),
        ReadError::Identity(e) => protocol_error(e),
    }
}
async fn catalog(
    State(issuer): State<LmmIssuer>,
    headers: HeaderMap,
    uri: axum::http::Uri,
) -> Response {
    if uri.query().is_some() {
        return read_error(ReadError::Identity(IdentityError::Unauthorized));
    }
    let access = match bearer(&headers) {
        Ok(v) => v,
        Err(e) => return read_error(ReadError::Identity(e)),
    };
    match issuer.catalog(access).await {
        Ok(v) => Json(v).into_response(),
        Err(e) => read_error(e),
    }
}
async fn balance(
    State(issuer): State<LmmIssuer>,
    headers: HeaderMap,
    uri: axum::http::Uri,
) -> Response {
    if uri.query().is_some() {
        return read_error(ReadError::Identity(IdentityError::Unauthorized));
    }
    let access = match bearer(&headers) {
        Ok(v) => v,
        Err(e) => return read_error(ReadError::Identity(e)),
    };
    match issuer.balance(access).await {
        Ok(v) => Json(v).into_response(),
        Err(e) => read_error(e),
    }
}

async fn usage(
    State(issuer): State<LmmIssuer>,
    headers: HeaderMap,
    uri: axum::http::Uri,
) -> Response {
    if uri.query().is_some() {
        return read_error(ReadError::Identity(IdentityError::Unauthorized));
    }
    let access = match bearer(&headers) {
        Ok(v) => v,
        Err(e) => return read_error(ReadError::Identity(e)),
    };
    match issuer.usage(access).await {
        Ok(value) => Json(value).into_response(),
        Err(e) => read_error(e),
    }
}
