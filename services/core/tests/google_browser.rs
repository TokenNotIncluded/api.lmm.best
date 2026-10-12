//! Public browser preset refusal/installation-contract tests on fresh synthetic PG.
use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Request, StatusCode},
};
use lmm_core::{
    identity::{GoogleOAuth, IdentityStore},
    legacy_auth::{self, LegacyAuthConfig},
};
use serde_json::{Value, json};
use sqlx::PgPool;
use tower::ServiceExt;

const ORIGIN: &str = "https://example.com";
fn config() -> LegacyAuthConfig {
    LegacyAuthConfig::new(ORIGIN, &[43; 32]).unwrap()
}
fn google(uri: &str) -> GoogleOAuth {
    GoogleOAuth::new(
        "public-test-client".into(),
        "synthetic-secret-never-networked".into(),
        uri.into(),
    )
    .unwrap()
}
async fn request(
    app: &Router,
    method: &str,
    uri: &str,
    origin: Option<&str>,
    body: Value,
) -> (StatusCode, Value) {
    let mut req = Request::builder()
        .method(method)
        .uri(uri)
        .header("content-type", "application/json");
    if let Some(origin) = origin {
        req = req.header("origin", origin);
    }
    let response = app
        .clone()
        .oneshot(req.body(Body::from(body.to_string())).unwrap())
        .await
        .unwrap();
    let status = response.status();
    assert_eq!(response.headers()["cache-control"], "no-store");
    let value = serde_json::from_slice(&to_bytes(response.into_body(), 65536).await.unwrap())
        .unwrap_or(Value::Null);
    (status, value)
}
#[sqlx::test(migrations = false)]
async fn preset_status_and_start_fail_closed_without_exact_browser_installation(pool: PgPool) {
    let store = IdentityStore::from_pool(pool.clone());
    store.init_database().await.unwrap();
    for configured in [
        None,
        Some("https://example.com/core/v1/auth/oauth/google/callback"),
        Some("https://other.example/api/oauth/google/callback"),
    ] {
        let store = if let Some(uri) = configured {
            store.clone().with_google_oauth(google(uri))
        } else {
            store.clone()
        };
        assert!(config().google_client_id(&store).is_none());
        let app = legacy_auth::router(store, config());
        let (status, value) = request(
            &app,
            "POST",
            "/api/oauth/google",
            Some(ORIGIN),
            json!({"accepted_legal":true}),
        )
        .await;
        assert_eq!(status, StatusCode::FORBIDDEN);
        assert_eq!(value["success"], false);
        assert!(!value.to_string().contains("synthetic-secret"));
    }
    let ready = store.with_google_oauth(google("https://example.com/api/oauth/google/callback"));
    assert_eq!(
        config().google_client_id(&ready),
        Some("public-test-client")
    );
    let app = legacy_auth::router(ready, config());
    for origin in [
        None,
        Some("https://evil.example"),
        Some("null"),
        Some("https://example.com, https://evil.example"),
    ] {
        assert_eq!(
            request(
                &app,
                "POST",
                "/api/oauth/google",
                origin,
                json!({"accepted_legal":true})
            )
            .await
            .0,
            StatusCode::FORBIDDEN
        );
    }
    for body in [json!({}), json!({"accepted_legal":false})] {
        let (status, body) = request(&app, "POST", "/api/oauth/google", Some(ORIGIN), body).await;
        assert_eq!(status, StatusCode::BAD_REQUEST);
        assert_eq!(body["code"], "AUTH_LEGAL_ACCEPTANCE_REQUIRED");
    }
    assert_eq!(
        request(
            &app,
            "POST",
            "/api/oauth/google",
            Some(ORIGIN),
            json!({"accepted_legal":true,"redirect_uri":"https://evil.example"})
        )
        .await
        .0,
        StatusCode::BAD_REQUEST
    );
    assert_eq!(
        request(&app, "GET", "/api/oauth/google", None, Value::Null)
            .await
            .0,
        StatusCode::METHOD_NOT_ALLOWED
    );
    let counts: (i64,i64,i64) = sqlx::query_as("SELECT (SELECT count(*) FROM core_identity.users),(SELECT count(*) FROM core_identity.credentials),(SELECT count(*) FROM core_identity.oauth_flows)").fetch_one(&pool).await.unwrap();
    assert_eq!(counts, (0, 0, 0));
}
