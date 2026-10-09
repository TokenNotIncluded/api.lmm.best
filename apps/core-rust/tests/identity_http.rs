use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Request, StatusCode},
    response::Response,
};
use lmm_core::{http, identity::IdentityStore, identity_http};
use serde_json::{Value, json};
use sqlx::PgPool;
use tower::ServiceExt;

async fn call(
    app: &Router,
    method: &str,
    path: &str,
    token: Option<&str>,
    body: Option<Value>,
) -> Response {
    let mut request = Request::builder().method(method).uri(path);
    if let Some(token) = token {
        request = request.header("authorization", format!("Bearer {token}"));
    }
    let body = match body {
        Some(value) => {
            request = request.header("content-type", "application/json");
            Body::from(value.to_string())
        }
        None => Body::empty(),
    };
    app.clone()
        .oneshot(request.body(body).unwrap())
        .await
        .unwrap()
}
async fn json_body(response: Response) -> Value {
    serde_json::from_slice(&to_bytes(response.into_body(), 65536).await.unwrap()).unwrap()
}

#[sqlx::test]
async fn native_http_flow_keeps_model_traffic_closed(pool: PgPool) {
    let store = IdentityStore::from_pool(pool);
    let session = store.bootstrap_user(11, 1).await.unwrap();
    let app = http::router().merge(identity_http::router(store));
    let denied = call(&app, "GET", "/core/v1/identity", None, None).await;
    assert_eq!(denied.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(denied.headers()["cache-control"], "no-store");
    assert_eq!(denied.headers()["www-authenticate"], "Bearer");
    let response = call(
        &app,
        "GET",
        "/core/v1/identity",
        Some(&session.secret),
        None,
    )
    .await;
    assert_eq!(response.status(), StatusCode::OK);
    let identity = json_body(response).await;
    assert_eq!(identity["user_id"], 11);
    assert!(identity.get("secret").is_none());
    assert!(identity.get("user_version").is_none());
    let response = call(
        &app,
        "POST",
        "/core/v1/keys",
        Some(&session.secret),
        Some(json!({"owner":{"kind":"personal","id":11},"ttl_seconds":3600})),
    )
    .await;
    assert_eq!(response.status(), StatusCode::CREATED);
    assert_eq!(response.headers()["cache-control"], "no-store");
    let key = json_body(response).await;
    let token = key["secret"].as_str().unwrap();
    assert_eq!(
        call(&app, "GET", "/core/v1/identity", Some(token), None)
            .await
            .status(),
        StatusCode::OK
    );
    assert_eq!(
        call(&app, "POST", "/core/v1/teams", Some(token), None)
            .await
            .status(),
        StatusCode::FORBIDDEN
    );
    assert_eq!(
        call(&app, "POST", "/core/v1/teams", Some(&session.secret), None)
            .await
            .status(),
        StatusCode::CREATED
    );
    let teams = call(&app, "GET", "/core/v1/teams", Some(&session.secret), None).await;
    assert_eq!(json_body(teams).await.as_array().unwrap().len(), 1);
    let forged = json!({"owner":{"kind":"personal","id":11},"ttl_seconds":3600,"platform_level":6});
    assert_eq!(
        call(
            &app,
            "POST",
            "/core/v1/keys",
            Some(&session.secret),
            Some(forged)
        )
        .await
        .status(),
        StatusCode::UNPROCESSABLE_ENTITY
    );
    let large = json!({"padding":"x".repeat(17000)});
    assert_eq!(
        call(
            &app,
            "POST",
            "/core/v1/keys",
            Some(&session.secret),
            Some(large)
        )
        .await
        .status(),
        StatusCode::PAYLOAD_TOO_LARGE
    );
    for (method, path) in [
        ("GET", "/health/ready"),
        ("GET", "/v1/models"),
        ("POST", "/v1/responses"),
    ] {
        assert_eq!(
            call(&app, method, path, Some(token), None).await.status(),
            StatusCode::SERVICE_UNAVAILABLE
        );
    }
    let path = format!("/core/v1/credentials/{}", key["id"].as_i64().unwrap());
    assert_eq!(
        call(&app, "DELETE", &path, Some(&session.secret), None)
            .await
            .status(),
        StatusCode::NO_CONTENT
    );
    assert_eq!(
        call(&app, "GET", "/core/v1/identity", Some(token), None)
            .await
            .status(),
        StatusCode::UNAUTHORIZED
    );
}

#[sqlx::test]
async fn database_failure_never_grants_access_or_calls_go(pool: PgPool) {
    let store = IdentityStore::from_pool(pool.clone());
    let session = store.bootstrap_user(1, 1).await.unwrap();
    let app = http::router().merge(identity_http::router(store));
    pool.close().await;
    let response = call(
        &app,
        "GET",
        "/core/v1/identity",
        Some(&session.secret),
        None,
    )
    .await;
    assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
    assert_eq!(
        json_body(response).await,
        json!({"error":{"code":"identity_unavailable"}})
    );
    assert_eq!(
        call(&app, "GET", "/health/live", None, None).await.status(),
        StatusCode::OK
    );
    assert_eq!(
        call(&app, "POST", "/v1/responses", Some(&session.secret), None)
            .await
            .status(),
        StatusCode::SERVICE_UNAVAILABLE
    );
}
