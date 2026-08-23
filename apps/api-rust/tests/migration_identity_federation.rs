use async_trait::async_trait;
use axum::{
    Router,
    body::Body,
    http::{HeaderMap, HeaderValue, Request, StatusCode},
};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use hmac::{Hmac, Mac};
use lmm_api_rs::auth::DashboardAuth;
use lmm_api_rs::migration_routes::identity_federation::{
    FederatedLogin, FederatedUser, FederationError, FederationIdentity,
    FederationMutationPublisher, FederationPrincipal, FederationProviderError, FederationProviders,
    FederationState, OAuthFlowContext, bindings_router, oauth_email_bind_router,
    oauth_external_provider_router, oauth_login_start_router, oauth_state_router, provider_router,
    router, verify_telegram_authorization,
};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::postgres::PgPoolOptions;
use std::{
    collections::BTreeMap,
    env,
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};
use tower::ServiceExt;

struct NoIdentity;

#[test]
fn oauth_login_start_router_constructor_is_exposed_for_listener_composition() {
    let _constructor: fn(FederationState) -> Router = oauth_login_start_router;
}

#[test]
fn oauth_state_router_constructor_is_exposed_for_listener_composition() {
    let _constructor: fn(FederationState, Arc<dyn DashboardAuth>, usize) -> Router =
        oauth_state_router;
}

#[test]
fn oauth_email_bind_router_constructor_is_exposed_for_listener_composition() {
    let _constructor: fn(FederationState, Arc<dyn DashboardAuth>) -> Router =
        oauth_email_bind_router;
}

#[test]
fn oauth_external_provider_router_constructor_is_exposed_for_listener_composition() {
    let _constructor: fn(FederationState) -> Router = oauth_external_provider_router;
}

#[async_trait]
impl FederationIdentity for NoIdentity {
    async fn principal(&self, _: &HeaderMap) -> Result<FederationPrincipal, FederationError> {
        Err(FederationError::Unauthorized)
    }

    async fn verify_email_code(&self, _: &str, _: &str) -> Result<bool, FederationError> {
        Ok(false)
    }
}

struct BoundIdentity;

#[async_trait]
impl FederationIdentity for BoundIdentity {
    async fn principal(&self, _: &HeaderMap) -> Result<FederationPrincipal, FederationError> {
        Ok(FederationPrincipal {
            user_id: 7,
            role: 1,
            session_id: "session-7".to_owned(),
        })
    }

    async fn verify_email_code(&self, _: &str, _: &str) -> Result<bool, FederationError> {
        Ok(false)
    }

    async fn validate_session_reference(
        &self,
        user_id: i64,
        session_id: &str,
    ) -> Result<(), FederationError> {
        (user_id == 7 && session_id == "session-7")
            .then_some(())
            .ok_or(FederationError::Unauthorized)
    }
}

struct ConfiguredMutationPublisher;

#[async_trait]
impl FederationMutationPublisher for ConfiguredMutationPublisher {
    fn configured(&self) -> bool {
        true
    }

    async fn publish_user(&self, user_id: i64) -> Result<(), FederationError> {
        (user_id == 7)
            .then_some(())
            .ok_or(FederationError::Internal)
    }
}

struct RoleZeroIdentity;

#[async_trait]
impl FederationIdentity for RoleZeroIdentity {
    async fn principal(&self, _: &HeaderMap) -> Result<FederationPrincipal, FederationError> {
        Ok(FederationPrincipal {
            user_id: 7,
            role: 0,
            session_id: "session-7".to_owned(),
        })
    }

    async fn verify_email_code(&self, _: &str, _: &str) -> Result<bool, FederationError> {
        Ok(false)
    }
}

struct InvalidIdentity;

#[async_trait]
impl FederationIdentity for InvalidIdentity {
    async fn principal(&self, _: &HeaderMap) -> Result<FederationPrincipal, FederationError> {
        Ok(FederationPrincipal {
            user_id: 0,
            role: 1,
            session_id: "session-0".to_owned(),
        })
    }

    async fn verify_email_code(&self, _: &str, _: &str) -> Result<bool, FederationError> {
        Ok(false)
    }
}

struct GithubBoundary;

#[async_trait]
impl FederationProviders for GithubBoundary {
    fn built_in_enabled(&self, provider: &str) -> bool {
        provider == "github"
    }

    async fn exchange(
        &self,
        provider: &str,
        code: &str,
        flow: &OAuthFlowContext,
    ) -> Result<FederatedUser, FederationProviderError> {
        if provider != "github" || code != "upstream-code" || flow.intent != "bind" {
            return Err(FederationProviderError::InvalidCode);
        }
        Ok(FederatedUser {
            provider_user_id: "github-subject".to_owned(),
            legacy_provider_user_id: None,
            username: "octocat".to_owned(),
            display_name: "Octocat".to_owned(),
            email: "octocat@example.test".to_owned(),
        })
    }

    async fn login(
        &self,
        _: &str,
        _: FederatedUser,
        _: &str,
        _: &HeaderMap,
    ) -> Result<FederatedLogin, FederationError> {
        Ok(FederatedLogin {
            data: json!({"access_token": "boundary-token"}),
            refresh_cookie: Some(HeaderValue::from_static("refresh=boundary; HttpOnly")),
        })
    }

    async fn validate_existing_login(
        &self,
        provider: &str,
        subject: &str,
    ) -> Result<(), FederationError> {
        (provider == "telegram" && subject == "12345")
            .then_some(())
            .ok_or(FederationError::Unauthorized)
    }

    fn telegram_bot_token(&self) -> Option<String> {
        Some("test-bot-token".to_owned())
    }
}

async fn isolated_postgres() -> sqlx::PgPool {
    assert_eq!(
        env::var("LMM_IDENTITY_TEST_ALLOW_SCHEMA_RESET").as_deref(),
        Ok("1"),
        "integration test requires LMM_IDENTITY_TEST_ALLOW_SCHEMA_RESET=1"
    );
    let database_url =
        env::var("LMM_IDENTITY_TEST_DATABASE_URL").expect("isolated PostgreSQL 18 URL");
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database_url)
        .await
        .expect("connect isolated PostgreSQL");
    sqlx::query("DROP TABLE IF EXISTS external_identity_claims, auth_flows, users CASCADE")
        .execute(&pool)
        .await
        .expect("reset isolated identity tables");
    sqlx::query(
        "CREATE TABLE users (id BIGINT PRIMARY KEY, github_id TEXT, deleted_at TIMESTAMPTZ, status BIGINT)",
    )
    .execute(&pool)
    .await
    .expect("create users");
    sqlx::query(
        "CREATE TABLE auth_flows (id BIGSERIAL PRIMARY KEY, token_hash TEXT NOT NULL UNIQUE, purpose TEXT NOT NULL, provider TEXT, intent TEXT, user_id BIGINT, session_id TEXT, payload TEXT, created_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL, consumed_at TIMESTAMPTZ)",
    )
    .execute(&pool)
    .await
    .expect("create auth_flows");
    sqlx::query("CREATE TABLE external_identity_claims (id BIGSERIAL PRIMARY KEY, provider TEXT NOT NULL, subject TEXT NOT NULL, user_id BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL, UNIQUE (provider, subject), UNIQUE (provider, user_id))")
        .execute(&pool)
        .await
        .expect("create external identity claims");
    sqlx::query("INSERT INTO users (id, github_id, status) VALUES (7, '', 1)")
        .execute(&pool)
        .await
        .expect("seed bind target");
    pool
}

#[tokio::test]
async fn callback_rejects_missing_state_before_any_provider_exchange() {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("a lazy test pool is valid");
    let app = provider_router(FederationState::new(
        pool,
        Arc::new(NoIdentity),
        "test-secret",
    ));

    let response = app
        .oneshot(
            Request::get("/api/oauth/github?code=upstream-code")
                .body(Body::empty())
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), StatusCode::FORBIDDEN);
}

#[tokio::test]
async fn email_binding_requires_a_verified_listener_identity_before_postgres() {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("a lazy test pool is valid");
    let app = router(FederationState::new(
        pool,
        Arc::new(NoIdentity),
        "test-secret",
    ));

    let response = app
        .oneshot(
            Request::post("/api/oauth/email/bind")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"email":"ada@example.test","code":"123456"}"#,
                ))
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
}

#[tokio::test]
async fn federation_user_routes_reject_role_zero_before_postgres() {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("a lazy test pool is valid");
    let response = router(FederationState::new(
        pool,
        Arc::new(RoleZeroIdentity),
        "test-secret",
    ))
    .oneshot(
        Request::get("/api/user/oauth/bindings")
            .body(Body::empty())
            .expect("request is valid"),
    )
    .await
    .expect("router responds");

    assert_eq!(response.status(), StatusCode::FORBIDDEN);
}

#[tokio::test]
async fn federation_user_routes_reject_invalid_identity_before_postgres() {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("a lazy test pool is valid");
    let response = router(FederationState::new(
        pool,
        Arc::new(InvalidIdentity),
        "test-secret",
    ))
    .oneshot(
        Request::get("/api/user/oauth/bindings")
            .body(Body::empty())
            .expect("request is valid"),
    )
    .await
    .expect("router responds");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
}

#[tokio::test]
async fn bindings_router_rejects_missing_identity_before_postgres() {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("a lazy test pool is valid");
    let response = bindings_router(FederationState::new(
        pool,
        Arc::new(NoIdentity),
        "test-secret",
    ))
    .oneshot(
        Request::get("/api/user/oauth/bindings")
            .body(Body::empty())
            .expect("request is valid"),
    )
    .await
    .expect("router responds");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
}

#[tokio::test]
async fn telegram_bind_failure_redirect_escapes_untrusted_flow_token() {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("a lazy test pool is valid");
    let app = router(FederationState::new(
        pool,
        Arc::new(NoIdentity),
        "test-secret",
    ));

    let response = app
        .oneshot(
            Request::get("/api/oauth/telegram/bind/flow%26forged%3D1")
                .body(Body::empty())
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), StatusCode::FOUND);
    assert_eq!(
        response
            .headers()
            .get("location")
            .and_then(|value| value.to_str().ok()),
        Some(
            "/oauth/telegram?telegram_bind=error&flow_token=flow%26forged%3D1&error_code=TELEGRAM_BIND_DISABLED"
        )
    );
}

#[tokio::test]
async fn telegram_bind_failure_redirect_preserves_legacy_query_space_encoding() {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("a lazy test pool is valid");
    let app = router(FederationState::new(
        pool,
        Arc::new(NoIdentity),
        "test-secret",
    ));

    let response = app
        .oneshot(
            Request::get("/api/oauth/telegram/bind/flow%20token")
                .body(Body::empty())
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), StatusCode::FOUND);
    assert_eq!(
        response
            .headers()
            .get("location")
            .and_then(|value| value.to_str().ok()),
        Some(
            "/oauth/telegram?telegram_bind=error&flow_token=flow+token&error_code=TELEGRAM_BIND_DISABLED"
        )
    );
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18; set LMM_IDENTITY_TEST_DATABASE_URL and LMM_IDENTITY_TEST_ALLOW_SCHEMA_RESET=1"]
async fn oauth_bind_callback_consumes_postgres_state_and_claims_the_account_once() {
    let pool = isolated_postgres().await;
    let app = router(
        FederationState::new(pool.clone(), Arc::new(BoundIdentity), "test-secret")
            .with_providers(Arc::new(GithubBoundary))
            .with_mutation_publisher(Arc::new(ConfiguredMutationPublisher)),
    );

    let state_response = app
        .clone()
        .oneshot(
            Request::post("/api/oauth/state")
                .header("authorization", "Bearer fixture-access")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"provider":"github","intent":"bind"}"#))
                .expect("state request is valid"),
        )
        .await
        .expect("state response");
    assert_eq!(state_response.status(), StatusCode::OK);
    let state_body = axum::body::to_bytes(state_response.into_body(), usize::MAX)
        .await
        .expect("state body");
    let state: Value = serde_json::from_slice(&state_body).expect("state JSON");
    let flow_token = state["data"]["flow_token"]
        .as_str()
        .expect("flow token")
        .to_owned();

    let callback = format!("/api/oauth/github?state={flow_token}&code=upstream-code");
    let callback_response = app
        .oneshot(
            Request::get(callback)
                .body(Body::empty())
                .expect("callback request is valid"),
        )
        .await
        .expect("callback response");

    assert_eq!(callback_response.status(), StatusCode::OK);
    let callback_body = axum::body::to_bytes(callback_response.into_body(), usize::MAX)
        .await
        .expect("callback body");
    assert_eq!(
        serde_json::from_slice::<Value>(&callback_body).expect("callback JSON"),
        json!({"success": true, "message": "", "data": {"action": "bind"}})
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT github_id FROM users WHERE id = 7")
            .fetch_one(&pool)
            .await
            .expect("bound subject"),
        "github-subject"
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*) FROM auth_flows WHERE purpose = 'oauth' AND consumed_at IS NOT NULL",
        )
        .fetch_one(&pool)
        .await
        .expect("consumed flow"),
        1
    );
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18; set LMM_IDENTITY_TEST_DATABASE_URL and LMM_IDENTITY_TEST_ALLOW_SCHEMA_RESET=1"]
async fn telegram_login_rejects_a_replayed_widget_assertion_after_the_first_cookie_response() {
    let pool = isolated_postgres().await;
    let app = router(
        FederationState::new(pool, Arc::new(BoundIdentity), "test-secret")
            .with_providers(Arc::new(GithubBoundary)),
    );
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("system clock after epoch")
        .as_secs() as i64;
    let request_path = telegram_login_path("test-bot-token", now);

    let first = app
        .clone()
        .oneshot(
            Request::get(&request_path)
                .body(Body::empty())
                .expect("Telegram request is valid"),
        )
        .await
        .expect("first Telegram response");

    assert_eq!(first.status(), StatusCode::OK);
    assert_eq!(
        first
            .headers()
            .get("set-cookie")
            .and_then(|value| value.to_str().ok()),
        Some("refresh=boundary; HttpOnly")
    );
    let first_body = axum::body::to_bytes(first.into_body(), usize::MAX)
        .await
        .expect("first Telegram body");
    assert_eq!(
        serde_json::from_slice::<Value>(&first_body).expect("first Telegram JSON"),
        json!({"success": true, "message": "", "data": {"access_token": "boundary-token"}})
    );

    let replay = app
        .oneshot(
            Request::get(request_path)
                .body(Body::empty())
                .expect("replay request is valid"),
        )
        .await
        .expect("replay response");

    assert_eq!(replay.status(), StatusCode::FORBIDDEN);
}

fn telegram_login_path(bot_token: &str, now: i64) -> String {
    let mut params = BTreeMap::from([
        ("auth_date".to_owned(), now.to_string()),
        ("first_name".to_owned(), "Ada".to_owned()),
        ("id".to_owned(), "12345".to_owned()),
    ]);
    let payload = params
        .iter()
        .map(|(key, value)| format!("{key}={value}"))
        .collect::<Vec<_>>()
        .join("\n");
    let secret = Sha256::digest(bot_token.as_bytes());
    let mut mac = Hmac::<Sha256>::new_from_slice(&secret).expect("valid HMAC key");
    mac.update(payload.as_bytes());
    params.insert("hash".to_owned(), hex::encode(mac.finalize().into_bytes()));
    format!(
        "/api/oauth/telegram/login?{}",
        params
            .iter()
            .map(|(key, value)| format!("{key}={value}"))
            .collect::<Vec<_>>()
            .join("&")
    )
}

#[test]
fn telegram_widget_signature_is_verified_with_short_lived_assertion() {
    let bot_token = "test-bot-token";
    let now = 1_700_000_000_i64;
    let mut params = BTreeMap::from([
        ("auth_date".to_owned(), now.to_string()),
        ("first_name".to_owned(), "Ada".to_owned()),
        ("id".to_owned(), "12345".to_owned()),
    ]);
    let payload = params
        .iter()
        .map(|(key, value)| format!("{key}={value}"))
        .collect::<Vec<_>>()
        .join("\n");
    let secret = Sha256::digest(bot_token.as_bytes());
    let mut mac = Hmac::<Sha256>::new_from_slice(&secret).expect("valid HMAC key");
    mac.update(payload.as_bytes());
    params.insert("hash".to_owned(), hex::encode(mac.finalize().into_bytes()));

    assert_eq!(
        verify_telegram_authorization(&params, bot_token, now),
        Ok("12345".to_owned())
    );

    params.insert("id".to_owned(), "other-user".to_owned());
    assert!(verify_telegram_authorization(&params, bot_token, now).is_err());
}

#[test]
fn generated_state_token_shape_is_safe_for_callback_paths() {
    let token = URL_SAFE_NO_PAD.encode([42_u8; 32]);
    assert!(
        token
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-' || byte == b'_')
    );
}
