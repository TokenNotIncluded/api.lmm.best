use async_trait::async_trait;
use axum::{
    body::Body,
    http::{Request, StatusCode},
};
use lmm_api_rs::{
    auth::{
        AuthBundle, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth,
        DashboardUser, LoginOutcome, LoginRequest, LogoutRequest, LogoutResult, RequestMetadata,
        TwoFactorLoginRequest,
    },
    routes::{
        identity_admin::{IdentityAdminState, router},
        identity_profile::{ProfileState, router as profile_router},
    },
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
            username: "actor".into(),
            display_name: "Actor".into(),
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
        .expect("valid lazy test URL");
    let valkey = redis::Client::open("redis://127.0.0.1/").expect("valid test URL");
    router(IdentityAdminState::new(
        pool,
        valkey,
        Arc::new(StaticAuth { role }),
    ))
}

fn composite_app(role: i64) -> axum::Router {
    let admin_pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("valid lazy test URL");
    let profile_pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("valid lazy test URL");
    let admin_valkey = redis::Client::open("redis://127.0.0.1/").expect("valid test URL");
    let profile_valkey = redis::Client::open("redis://127.0.0.1/").expect("valid test URL");
    router(IdentityAdminState::new(
        admin_pool,
        admin_valkey,
        Arc::new(StaticAuth { role }),
    ))
    .merge(profile_router(ProfileState::new(
        profile_pool,
        profile_valkey,
    )))
}

async fn body(response: axum::response::Response) -> Value {
    serde_json::from_slice(
        &axum::body::to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("body"),
    )
    .expect("JSON envelope")
}

#[tokio::test]
async fn list_users_should_reject_missing_bearer_before_touching_postgres() {
    let response = app(100)
        .oneshot(
            Request::get("/api/user/")
                .header("x-user-id", "999")
                .header("x-role", "100")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(
        body(response).await,
        json!({
            "success": false,
            "code": "AUTH_UNAUTHORIZED",
            "message": "Unauthorized, invalid access token",
        })
    );
}

#[tokio::test]
async fn search_users_should_reject_non_administrator_with_legacy_failure_envelope() {
    let response = app(1)
        .oneshot(
            Request::get("/api/user/search?keyword=alice")
                .header("authorization", "Bearer test-token")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert_eq!(
        body(response).await,
        json!({
            "success": false,
            "code": "AUTH_INSUFFICIENT_PRIVILEGE",
            "message": "Unauthorized, insufficient privileges",
        })
    );
}

#[tokio::test]
async fn administrator_routes_reject_role_zero_before_touching_postgres() {
    let response = app(0)
        .oneshot(
            Request::get("/api/user/")
                .header("authorization", "Bearer test-token")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert_eq!(body(response).await["code"], "AUTH_INSUFFICIENT_PRIVILEGE");
}

#[tokio::test]
async fn composite_identity_router_keeps_admin_put_user_path_exclusive() {
    let response = composite_app(100)
        .oneshot(
            Request::put("/api/user/")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"username":"target","password":"password"}"#))
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(body(response).await["success"], false);
}
