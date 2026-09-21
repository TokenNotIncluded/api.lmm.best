use std::{env, sync::Arc};

use async_trait::async_trait;
use axum::{
    body::Body,
    http::{HeaderMap, Request, StatusCode},
};
use lmm_api_rs::auth::SecurityProof;
use lmm_api_rs::routes::identity_security::{
    IdentitySecurityState, MemorySecurityProvider, PgValkeySecurityProvider, SecurityActor,
    SecurityAuthorizer, SecurityCall, SecurityError, SecurityOperation, SecurityProvider,
    passkey_read_router, registration_router, router, sessions_read_router,
};
use serde_json::json;
use sqlx::{PgPool, Row, postgres::PgPoolOptions};
use tower::ServiceExt;

#[derive(Clone)]
struct Authorizer {
    user: Result<SecurityActor, SecurityError>,
    admin: Result<SecurityActor, SecurityError>,
}

#[derive(Clone)]
struct ProofAuthorizer {
    actor: SecurityActor,
    proof: Result<SecurityProof, SecurityError>,
}

#[async_trait]
impl SecurityAuthorizer for ProofAuthorizer {
    async fn user(&self, _: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        Ok(self.actor.clone())
    }

    async fn admin(&self, _: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        Ok(self.actor.clone())
    }

    async fn issue_security_proof(
        &self,
        _: &SecurityActor,
        _: &str,
        _: &[String],
    ) -> Result<SecurityProof, SecurityError> {
        self.proof.clone()
    }
}

#[async_trait]
impl SecurityAuthorizer for Authorizer {
    async fn user(&self, _: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        self.user.clone()
    }

    async fn admin(&self, _: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        self.admin.clone()
    }
}

fn user() -> SecurityActor {
    SecurityActor {
        user_id: 7,
        role: 1,
        session_id: Some("session-7".to_owned()),
    }
}

fn admin() -> SecurityActor {
    SecurityActor {
        user_id: 9,
        role: 10,
        session_id: Some("session-9".to_owned()),
    }
}

fn app(provider: Arc<MemorySecurityProvider>, authorizer: Authorizer) -> axum::Router {
    router(IdentitySecurityState::new(provider, Arc::new(authorizer)))
}

#[tokio::test]
async fn registration_router_exposes_only_the_completed_anonymous_registration_slice() {
    let provider = Arc::new(MemorySecurityProvider::new(Ok(serde_json::Value::Null)));
    let response = registration_router(IdentitySecurityState::new(
        provider.clone(),
        Arc::new(Authorizer {
            user: Err(SecurityError::Unauthorized),
            admin: Err(SecurityError::Unauthorized),
        }),
    ))
    .oneshot(
        Request::post("/api/user/register")
            .header("content-type", "application/json")
            .body(Body::from(
                r#"{"username":"alice","password":"password1","accepted_legal":true}"#,
            ))
            .expect("registration request"),
    )
    .await
    .expect("registration response");

    assert_eq!(
        response.status(),
        StatusCode::SERVICE_UNAVAILABLE,
        "registration must fail closed until its listener-owned anonymous security policy is configured"
    );
    assert!(provider.calls().expect("provider calls").is_empty());
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("registration body");
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(&body).expect("JSON"),
        json!({
            "success": false,
            "message": "Security service is temporarily unavailable"
        })
    );
}

#[tokio::test]
async fn read_only_security_mounts_keep_the_auth_boundary() {
    for (app, path) in [
        (
            sessions_read_router(IdentitySecurityState::with_rejecting_authorizer(Arc::new(
                MemorySecurityProvider::default(),
            ))),
            "/api/user/sessions",
        ),
        (
            passkey_read_router(IdentitySecurityState::with_rejecting_authorizer(Arc::new(
                MemorySecurityProvider::default(),
            ))),
            "/api/user/passkey",
        ),
    ] {
        let response = app
            .oneshot(Request::get(path).body(Body::empty()).expect("request"))
            .await
            .expect("response");
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    }
}

#[tokio::test]
async fn all_twenty_frozen_candidates_have_the_expected_method_and_shape() {
    let provider = Arc::new(MemorySecurityProvider::new(Ok(json!({"accepted": true}))));
    let application = app(
        Arc::clone(&provider),
        Authorizer {
            user: Ok(user()),
            admin: Ok(admin()),
        },
    );
    let routes = [
        ("DELETE", "/api/user/11/bindings/github", ""),
        ("DELETE", "/api/user/11/reset_passkey", ""),
        ("DELETE", "/api/user/passkey", ""),
        ("DELETE", "/api/user/sessions/other-session", ""),
        ("GET", "/api/authz/catalog", ""),
        ("GET", "/api/reset_password?email=ada@example.test", ""),
        ("GET", "/api/user/passkey", ""),
        ("GET", "/api/user/sessions", ""),
        ("GET", "/api/verification?email=ada@example.test", ""),
        ("POST", "/api/user/login/2fa", r#"{"code":"123456"}"#),
        ("POST", "/api/user/passkey/login/begin", "{}"),
        ("POST", "/api/user/passkey/login/finish", "{}"),
        ("POST", "/api/user/passkey/register/begin", "{}"),
        ("POST", "/api/user/passkey/register/finish", "{}"),
        ("POST", "/api/user/passkey/verify/begin", "{}"),
        ("POST", "/api/user/passkey/verify/finish", "{}"),
        ("POST", "/api/user/register", "{}"),
        (
            "POST",
            "/api/user/reset",
            r#"{"email":"ada@example.test","token":"reset-token"}"#,
        ),
        ("POST", "/api/user/sessions/revoke-others", "{}"),
        ("POST", "/api/verify", "{}"),
    ];

    for (method, uri, body) in routes {
        let response = application
            .clone()
            .oneshot(
                Request::builder()
                    .method(method)
                    .uri(uri)
                    .header("content-type", "application/json")
                    .body(Body::from(body))
                    .expect("valid route request"),
            )
            .await
            .expect("candidate responds");
        let expected_status = if matches!(uri, "/api/user/register" | "/api/verify") {
            StatusCode::SERVICE_UNAVAILABLE
        } else {
            StatusCode::OK
        };
        assert_eq!(response.status(), expected_status, "{method} {uri}");
    }
    assert_eq!(provider.calls().expect("fake calls").len(), 19);
}

#[tokio::test]
async fn user_routes_authenticate_before_body_validation_or_provider_execution() {
    let provider = Arc::new(MemorySecurityProvider::default());
    let response = app(
        Arc::clone(&provider),
        Authorizer {
            user: Err(SecurityError::Unauthorized),
            admin: Ok(admin()),
        },
    )
    .oneshot(
        Request::post("/api/user/passkey/register/finish")
            .header("x-user-id", "7")
            .body(Body::from("{"))
            .expect("request"),
    )
    .await
    .expect("response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert!(provider.calls().expect("fake calls").is_empty());
}

#[tokio::test]
async fn administrator_route_checks_role_before_path_validation_or_provider_execution() {
    let provider = Arc::new(MemorySecurityProvider::default());
    let response = app(
        Arc::clone(&provider),
        Authorizer {
            user: Ok(user()),
            admin: Ok(user()),
        },
    )
    .oneshot(
        Request::delete("/api/user/not-a-number/reset_passkey")
            .body(Body::empty())
            .expect("request"),
    )
    .await
    .expect("response");

    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert!(provider.calls().expect("fake calls").is_empty());
}

#[tokio::test]
async fn client_identity_headers_are_not_an_authentication_mechanism() {
    let provider = Arc::new(MemorySecurityProvider::default());
    let response = router(IdentitySecurityState::with_rejecting_authorizer(provider))
        .oneshot(
            Request::get("/api/user/sessions")
                .header("x-user-id", "7")
                .header("x-role", "100")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
}

#[tokio::test]
async fn role_zero_user_is_rejected_as_insufficient_privilege_before_provider_execution() {
    let provider = Arc::new(MemorySecurityProvider::default());
    let response = app(
        Arc::clone(&provider),
        Authorizer {
            user: Ok(SecurityActor {
                user_id: 7,
                role: 0,
                session_id: None,
            }),
            admin: Ok(admin()),
        },
    )
    .oneshot(
        Request::get("/api/user/sessions")
            .body(Body::empty())
            .expect("request"),
    )
    .await
    .expect("response");

    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert!(provider.calls().expect("fake calls").is_empty());
}

#[tokio::test]
async fn unsupported_method_preserves_axums_method_not_allowed_response() {
    let provider = Arc::new(MemorySecurityProvider::default());
    let response = app(
        provider,
        Authorizer {
            user: Ok(user()),
            admin: Ok(admin()),
        },
    )
    .oneshot(
        Request::patch("/api/user/passkey")
            .body(Body::empty())
            .expect("request"),
    )
    .await
    .expect("response");

    assert_eq!(response.status(), StatusCode::METHOD_NOT_ALLOWED);
}

#[tokio::test]
async fn unavailable_webauthn_boundary_never_reports_a_fabricated_success() {
    let provider = Arc::new(MemorySecurityProvider::default());
    let response = app(
        provider,
        Authorizer {
            user: Ok(user()),
            admin: Ok(admin()),
        },
    )
    .oneshot(
        Request::post("/api/user/passkey/login/finish")
            .header("content-type", "application/json")
            .body(Body::from("{}"))
            .expect("request"),
    )
    .await
    .expect("response");

    assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
}

#[tokio::test]
async fn universal_verify_returns_a_proof_only_after_provider_verification() {
    let provider = Arc::new(MemorySecurityProvider::new(Ok(json!({
        "method": "email",
        "scope": "channel.key.read"
    }))));
    let application = router(IdentitySecurityState::new(
        provider,
        Arc::new(ProofAuthorizer {
            actor: user(),
            proof: Ok(SecurityProof {
                token: "proof-token".to_owned(),
                expires_at: 1_900_000_000,
            }),
        }),
    ));
    let response = application
        .oneshot(
            Request::post("/api/verify")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"method":"email","code":"123456","scope":"channel.key.read"}"#,
                ))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::OK);
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(&body).expect("JSON"),
        json!({
            "success": true,
            "message": "验证成功",
            "data": {
                "proof_token": "proof-token",
                "expires_at": 1_900_000_000_i64,
                "method": "email",
                "scope": "channel.key.read"
            }
        })
    );
}

#[tokio::test]
async fn universal_verify_rejects_provider_success_without_proof_fields() {
    let provider = Arc::new(MemorySecurityProvider::new(Ok(json!({"accepted": true}))));
    let application = router(IdentitySecurityState::new(
        provider,
        Arc::new(ProofAuthorizer {
            actor: user(),
            proof: Ok(SecurityProof {
                token: "must-not-be-used".to_owned(),
                expires_at: 1_900_000_000,
            }),
        }),
    ));
    let response = application
        .oneshot(
            Request::post("/api/verify")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"method":"email","code":"123456","scope":"channel.key.read"}"#,
                ))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
}

async fn reset_provider_schema(pool: &PgPool) {
    for statement in [
        "DROP TABLE IF EXISTS user_sessions, passkey_credentials, users CASCADE",
        "CREATE TABLE users (id BIGINT PRIMARY KEY, deleted_at TIMESTAMPTZ, auth_version BIGINT NOT NULL DEFAULT 1)",
        "CREATE TABLE passkey_credentials (id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL, credential_id TEXT, deleted_at TIMESTAMPTZ, last_used_at TIMESTAMPTZ, backup_eligible BOOLEAN, backup_state BOOLEAN)",
        "CREATE TABLE user_sessions (sid TEXT PRIMARY KEY, user_id BIGINT NOT NULL, status TEXT NOT NULL, revoked_at BIGINT NOT NULL DEFAULT 0, revoked_reason TEXT)",
    ] {
        sqlx::query(statement)
            .execute(pool)
            .await
            .expect("reset isolated identity-security schema");
    }
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL/Valkey; set LMM_IDENTITY_TEST_DATABASE_URL, LMM_IDENTITY_TEST_VALKEY_URL, and LMM_IDENTITY_TEST_ALLOW_SCHEMA_RESET=1"]
async fn passkey_delete_rolls_back_when_valkey_fence_cannot_be_armed() {
    assert_eq!(
        env::var("LMM_IDENTITY_TEST_ALLOW_SCHEMA_RESET").as_deref(),
        Ok("1")
    );
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&env::var("LMM_IDENTITY_TEST_DATABASE_URL").expect("isolated database URL"))
        .await
        .expect("connect isolated PostgreSQL");
    reset_provider_schema(&pool).await;
    sqlx::query("INSERT INTO users (id, auth_version) VALUES (7, 1)")
        .execute(&pool)
        .await
        .expect("seed user");
    sqlx::query("INSERT INTO passkey_credentials (user_id, credential_id) VALUES (7, 'cred-7')")
        .execute(&pool)
        .await
        .expect("seed credential");
    sqlx::query(
        "INSERT INTO user_sessions (sid, user_id, status) VALUES ('session-7', 7, 'active')",
    )
    .execute(&pool)
    .await
    .expect("seed session");
    let unavailable_valkey = redis::Client::open("redis://127.0.0.1:1/").expect("valid URL");
    let outcome = PgValkeySecurityProvider::new(pool.clone(), unavailable_valkey)
        .execute(SecurityCall {
            operation: SecurityOperation::DeletePasskey,
            actor: Some(user()),
            input: json!({}),
        })
        .await;
    assert_eq!(outcome, Err(SecurityError::Unavailable));
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM passkey_credentials WHERE user_id = 7")
            .fetch_one(&pool)
            .await
            .expect("credential count"),
        1
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT auth_version FROM users WHERE id = 7")
            .fetch_one(&pool)
            .await
            .expect("auth version"),
        1
    );
    assert_eq!(
        sqlx::query("SELECT status FROM user_sessions WHERE sid = 'session-7'")
            .fetch_one(&pool)
            .await
            .expect("session row")
            .get::<String, _>("status"),
        "active"
    );
}
