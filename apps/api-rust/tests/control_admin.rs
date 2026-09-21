use std::sync::Arc;

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{HeaderMap, HeaderName, Request, StatusCode, header},
};
use lmm_api_rs::{
    auth::{
        AuthBundle, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth,
        DashboardUser, LoginOutcome, LoginRequest, LogoutRequest, LogoutResult, RequestMetadata,
        TwoFactorLoginRequest,
    },
    routes::control_admin::{
        ControlAdminAuthorizer, ControlAdminIdentity, ControlAdminState,
        DashboardControlAdminAuthorizer, OAuthDiscoveryClient, control_admin_read_router,
        control_admin_router,
    },
};
use secrecy::{ExposeSecret, SecretString};
use serde_json::{Value, json};
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

struct ServerValidatedDashboard;

struct RejectingAuthorizer;

#[async_trait]
impl ControlAdminAuthorizer for RejectingAuthorizer {
    async fn authorize(&self, _: &HeaderMap) -> Result<ControlAdminIdentity, &'static str> {
        Err("Unauthorized, invalid access token")
    }
}

struct RoleAuthorizer(i64);

#[async_trait]
impl ControlAdminAuthorizer for RoleAuthorizer {
    async fn authorize(&self, _: &HeaderMap) -> Result<ControlAdminIdentity, &'static str> {
        Ok(ControlAdminIdentity {
            user_id: 7,
            role: self.0,
        })
    }
}

struct NoopDiscovery;

#[async_trait]
impl OAuthDiscoveryClient for NoopDiscovery {
    async fn discover(&self, _: &str) -> Result<Value, String> {
        panic!("discovery must not run before authorization")
    }
}

fn router(authorizer: Arc<dyn ControlAdminAuthorizer>) -> axum::Router {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgresql://lmm:lmm@127.0.0.1:1/lmm")
        .expect("lazy PostgreSQL pool");
    control_admin_router(ControlAdminState::new(
        pg,
        authorizer,
        Arc::new(NoopDiscovery),
    ))
}

fn read_router(authorizer: Arc<dyn ControlAdminAuthorizer>) -> axum::Router {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgresql://lmm:lmm@127.0.0.1:1/lmm")
        .expect("lazy PostgreSQL pool");
    control_admin_read_router(ControlAdminState::new(
        pg,
        authorizer,
        Arc::new(NoopDiscovery),
    ))
}

async fn response_json(response: axum::response::Response) -> Value {
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    serde_json::from_slice(&body).expect("JSON response")
}

#[async_trait]
impl DashboardAuth for ServerValidatedDashboard {
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

    async fn self_user(&self, token: SecretString) -> Result<DashboardUser, AuthError> {
        if token.expose_secret() != "server-validated-token" {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        Ok(DashboardUser {
            id: 7,
            username: "root".to_owned(),
            display_name: "Root".to_owned(),
            role: 100,
            status: 1,
            email: String::new(),
            github_id: String::new(),
            discord_id: String::new(),
            oidc_id: String::new(),
            wechat_id: String::new(),
            telegram_id: String::new(),
            group: "default".to_owned(),
            quota: 0,
            used_quota: 0,
            request_count: 0,
            aff_code: String::new(),
            aff_count: 0,
            aff_quota: 0,
            aff_history_quota: 0,
            inviter_id: 0,
            linux_do_id: String::new(),
            setting: "{}".to_owned(),
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

#[tokio::test]
async fn control_admin_authorizer_uses_the_server_validated_dashboard_identity() {
    let authorizer = DashboardControlAdminAuthorizer::new(Arc::new(ServerValidatedDashboard));
    let forged = HeaderMap::from_iter([(
        HeaderName::from_static("x-user-role"),
        "100".parse().expect("header"),
    )]);
    assert!(authorizer.authorize(&forged).await.is_err());

    let authenticated = HeaderMap::from_iter([(
        header::AUTHORIZATION,
        "Bearer server-validated-token".parse().expect("header"),
    )]);
    let identity = authorizer
        .authorize(&authenticated)
        .await
        .expect("server-validated root identity");
    assert_eq!((identity.user_id, identity.role), (7, 100));
}

#[tokio::test]
async fn authentication_precedes_malformed_json_and_query_parsing() {
    let app = router(Arc::new(RejectingAuthorizer));

    for request in [
        Request::post("/api/custom-oauth-provider/discovery")
            .body(Body::from("{"))
            .expect("request"),
        Request::post("/api/system-task/log-cleanup?target_timestamp=not-a-number")
            .body(Body::empty())
            .expect("request"),
    ] {
        let response = app.clone().oneshot(request).await.expect("response");
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        let body = response_json(response).await;
        assert_eq!(body["code"], "AUTH_UNAUTHORIZED");
    }
}

#[tokio::test]
async fn admin_identity_cannot_enter_root_control_routes() {
    let response = router(Arc::new(RoleAuthorizer(10)))
        .oneshot(
            Request::post("/api/custom-oauth-provider/discovery")
                .body(Body::from("{"))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    let body = response_json(response).await;
    assert_eq!(body["code"], "AUTH_INSUFFICIENT_PRIVILEGE");
}

#[tokio::test]
async fn read_only_control_mount_keeps_the_dashboard_auth_boundary() {
    let response = read_router(Arc::new(RejectingAuthorizer))
        .oneshot(
            Request::get("/api/system-task/list")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(response_json(response).await["code"], "AUTH_UNAUTHORIZED");
}
