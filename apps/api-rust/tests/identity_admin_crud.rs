//! Route-contract tests for the seven legacy administrator user CRUD endpoints.
//!
//! These tests intentionally use a lazy PostgreSQL pool: every assertion covers
//! a request rejected before persistence, proving that unauthenticated or
//! invalid input cannot reach the database or expose a stored secret.

use async_trait::async_trait;
use axum::{
    body::Body,
    http::{Method, Request, StatusCode},
};
use lmm_api_rs::{
    auth::{
        AuthBundle, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth,
        DashboardUser, LoginOutcome, LoginRequest, LogoutRequest, LogoutResult, RequestMetadata,
        TwoFactorLoginRequest,
    },
    routes::identity_admin::{IdentityAdminState, router},
};
use secrecy::SecretString;
use serde_json::{Value, json};
use sqlx::postgres::PgPoolOptions;
use std::sync::Arc;
use tower::ServiceExt;

#[derive(Clone)]
struct StaticAuth {
    role: i64,
}

#[async_trait]
impl DashboardAuth for StaticAuth {
    async fn check_critical_rate_limit(
        &self,
        _: &str,
    ) -> Result<CriticalRateLimitOutcome, AuthError> {
        Ok(CriticalRateLimitOutcome::Allowed)
    }

    async fn login(&self, _: LoginRequest, _: RequestMetadata) -> Result<LoginOutcome, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn login_2fa(
        &self,
        _: TwoFactorLoginRequest,
        _: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn refresh(
        &self,
        _: SecretString,
        _: Option<String>,
        _: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn self_user(&self, _: SecretString) -> Result<DashboardUser, AuthError> {
        Ok(DashboardUser {
            id: 1,
            username: "operator".into(),
            display_name: "Operator".into(),
            role: self.role,
            status: 1,
            email: String::new(),
            github_id: String::new(),
            discord_id: String::new(),
            oidc_id: String::new(),
            wechat_id: String::new(),
            telegram_id: String::new(),
            group: "default".into(),
            quota: 0,
            used_quota: 0,
            request_count: 0,
            aff_code: String::new(),
            aff_count: 0,
            aff_quota: 0,
            aff_history_quota: 0,
            inviter_id: 0,
            linux_do_id: String::new(),
            setting: "{}".into(),
            stripe_customer: String::new(),
            sidebar_modules: json!({}),
            permissions: json!({}),
        })
    }

    async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn generate_personal_access_token(&self, _: SecretString) -> Result<String, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }
}

fn app(role: i64) -> axum::Router {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("valid lazy PostgreSQL URL");
    let valkey = redis::Client::open("redis://127.0.0.1/").expect("valid Valkey URL");
    router(IdentityAdminState::new(
        pool,
        valkey,
        Arc::new(StaticAuth { role }),
    ))
}

async fn request(
    role: i64,
    method: Method,
    path: &str,
    authorization: Option<&str>,
    body: Value,
) -> (StatusCode, Value) {
    let mut builder = Request::builder().method(method).uri(path);
    if let Some(value) = authorization {
        builder = builder.header("authorization", value);
    }
    let response = app(role)
        .oneshot(
            builder
                .header("content-type", "application/json")
                .body(Body::from(body.to_string()))
                .expect("request"),
        )
        .await
        .expect("response");
    let status = response.status();
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    (
        status,
        serde_json::from_slice(&body).expect("JSON envelope"),
    )
}

struct RouteCase {
    method: Method,
    path: &'static str,
    body: Value,
}

fn routes() -> Vec<RouteCase> {
    vec![
        RouteCase {
            method: Method::GET,
            path: "/api/user/?p=1",
            body: json!({}),
        },
        RouteCase {
            method: Method::GET,
            path: "/api/user/search?keyword=target",
            body: json!({}),
        },
        RouteCase {
            method: Method::GET,
            path: "/api/user/9",
            body: json!({}),
        },
        RouteCase {
            method: Method::POST,
            path: "/api/user/",
            body: json!({"username":"target","password":"password"}),
        },
        RouteCase {
            method: Method::PUT,
            path: "/api/user/",
            body: json!({"id":9,"username":"target"}),
        },
        RouteCase {
            method: Method::DELETE,
            path: "/api/user/9",
            body: json!({}),
        },
        RouteCase {
            method: Method::POST,
            path: "/api/user/manage",
            body: json!({"id":9,"action":"disable"}),
        },
    ]
}

#[tokio::test]
async fn identity_admin_crud_rejects_unauthenticated_requests_before_postgres() {
    for route in routes() {
        let (status, response) = request(100, route.method, route.path, None, route.body).await;
        assert_eq!(status, StatusCode::UNAUTHORIZED, "{}", route.path);
        assert_eq!(response["success"], false, "{}", route.path);
        assert_eq!(response["code"], "AUTH_UNAUTHORIZED", "{}", route.path);
    }
}

#[tokio::test]
async fn identity_admin_crud_rejects_non_administrators_before_postgres() {
    for route in routes() {
        let (status, response) = request(
            1,
            route.method,
            route.path,
            Some("Bearer dashboard-token"),
            route.body,
        )
        .await;
        assert_eq!(status, StatusCode::FORBIDDEN, "{}", route.path);
        assert_eq!(response["success"], false, "{}", route.path);
        assert_eq!(
            response["code"], "AUTH_INSUFFICIENT_PRIVILEGE",
            "{}",
            route.path
        );
    }
}

#[tokio::test]
async fn identity_admin_crud_rejects_invalid_create_input_without_leaking_password_or_token() {
    let cases = [
        json!({"username":"","password":"password"}),
        json!({"username":"a".repeat(21),"password":"password"}),
        json!({"username":"target","password":"short"}),
        json!({"username":"target","password":"password","display_name":"a".repeat(21)}),
        json!({"username":"target","password":"password","email":"a".repeat(51)}),
        json!({"username":"target","password":"password","remark":"a".repeat(256)}),
    ];
    for body in cases {
        let (status, response) = request(
            100,
            Method::POST,
            "/api/user/",
            Some("Bearer dashboard-token"),
            body,
        )
        .await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(response["success"], false);
        let serialized = response.to_string();
        assert!(!serialized.contains("password"));
        assert!(!serialized.contains("access_token"));
    }
}

#[tokio::test]
async fn identity_admin_crud_rejects_invalid_update_input_without_touching_postgres() {
    let cases = [
        json!({"username":"target"}),
        json!({"id":0,"username":"target"}),
        json!({"id":9,"username":""}),
        json!({"id":9,"username":"target","password":"short"}),
        json!({"id":9,"username":"target","display_name":"a".repeat(21)}),
        json!({"id":9,"username":"target","email":"a".repeat(51)}),
        json!({"id":9,"username":"target","remark":"a".repeat(256)}),
    ];
    for body in cases {
        let (status, response) = request(
            100,
            Method::PUT,
            "/api/user/",
            Some("Bearer dashboard-token"),
            body,
        )
        .await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(response["success"], false);
        assert!(!response.to_string().contains("password"));
    }
}

#[tokio::test]
async fn identity_admin_authenticated_handler_errors_include_auth_version() {
    let response = app(100)
        .oneshot(
            Request::post("/api/user/")
                .header("authorization", "Bearer dashboard-token")
                .header("content-type", "application/json")
                .body(Body::from(json!({"username":"target"}).to_string()))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        response.headers()["auth-version"],
        "864b7076dbcd0a3c01b5520316720ebf"
    );

    let unauthenticated = app(100)
        .oneshot(
            Request::post("/api/user/")
                .header("content-type", "application/json")
                .body(Body::from(json!({"username":"target"}).to_string()))
                .expect("request"),
        )
        .await
        .expect("response");
    assert!(!unauthenticated.headers().contains_key("auth-version"));
}

#[tokio::test]
async fn identity_admin_authenticates_before_malformed_json_binding() {
    let unauthenticated = app(100)
        .oneshot(
            Request::post("/api/user/")
                .header("content-type", "application/json")
                .body(Body::from("{"))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(unauthenticated.status(), StatusCode::UNAUTHORIZED);
    assert!(!unauthenticated.headers().contains_key("auth-version"));

    let non_administrator = app(1)
        .oneshot(
            Request::post("/api/user/")
                .header("authorization", "Bearer dashboard-token")
                .header("content-type", "application/json")
                .body(Body::from("{"))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(non_administrator.status(), StatusCode::FORBIDDEN);
    assert!(!non_administrator.headers().contains_key("auth-version"));

    let authenticated = app(100)
        .oneshot(
            Request::post("/api/user/")
                .header("authorization", "Bearer dashboard-token")
                .header("content-type", "application/json")
                .body(Body::from("{"))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(authenticated.status(), StatusCode::OK);
    assert_eq!(
        authenticated.headers()["auth-version"],
        "864b7076dbcd0a3c01b5520316720ebf"
    );
    let body = axum::body::to_bytes(authenticated.into_body(), usize::MAX)
        .await
        .expect("response body");
    let body: Value = serde_json::from_slice(&body).expect("JSON envelope");
    assert_eq!(body["success"], false);
}

#[tokio::test]
async fn identity_admin_crud_retains_legacy_method_contract() {
    let response = app(100)
        .oneshot(
            Request::get("/api/user/manage")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::METHOD_NOT_ALLOWED);
}
