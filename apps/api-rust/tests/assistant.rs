use std::sync::Arc;

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{Request, StatusCode, header},
};
use lmm_api_rs::{
    auth::{
        AuthBundle, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth,
        DashboardSessionContext, DashboardUser, LoginOutcome, LoginRequest, LogoutRequest,
        LogoutResult, RequestMetadata, TwoFactorLoginRequest,
    },
    routes::assistant::{AssistantRateLimitConfig, AssistantReadState, assistant_read_router},
};
use secrecy::{ExposeSecret, SecretString};
use serde_json::{Value, json};
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

struct PersonalTokenAuth;

fn dashboard_user() -> DashboardUser {
    DashboardUser {
        id: 7,
        username: "assistant-user".to_owned(),
        display_name: String::new(),
        role: 1,
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
    }
}

#[async_trait]
impl DashboardAuth for PersonalTokenAuth {
    async fn check_critical_rate_limit(
        &self,
        _: &str,
    ) -> Result<CriticalRateLimitOutcome, AuthError> {
        Ok(CriticalRateLimitOutcome::Allowed)
    }

    async fn login(&self, _: LoginRequest, _: RequestMetadata) -> Result<LoginOutcome, AuthError> {
        panic!("unused")
    }

    async fn login_2fa(
        &self,
        _: TwoFactorLoginRequest,
        _: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        panic!("unused")
    }

    async fn refresh(
        &self,
        _: SecretString,
        _: Option<String>,
        _: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        panic!("unused")
    }

    async fn self_user(&self, token: SecretString) -> Result<DashboardUser, AuthError> {
        if !matches!(token.expose_secret(), "personal-token" | "browser-session") {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        Ok(dashboard_user())
    }

    async fn current_session(
        &self,
        token: SecretString,
    ) -> Result<DashboardSessionContext, AuthError> {
        if token.expose_secret() != "browser-session" {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        Ok(DashboardSessionContext {
            user: dashboard_user(),
            session_id: "assistant-session".to_owned(),
            session_version: 1,
            user_auth_version: 1,
            client_ip: "127.0.0.1".to_owned(),
            user_agent: "assistant-test".to_owned(),
        })
    }

    async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, AuthError> {
        panic!("unused")
    }

    async fn generate_personal_access_token(&self, _: SecretString) -> Result<String, AuthError> {
        panic!("unused")
    }
}

fn smoke_router() -> axum::Router {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://postgres@127.0.0.1:1/assistant")
        .expect("valid lazy PostgreSQL URL");
    let valkey = redis::Client::open("redis://127.0.0.1/").expect("valid Valkey URL");
    assistant_read_router(AssistantReadState::new(
        pg,
        valkey,
        Arc::new(PersonalTokenAuth),
        secrecy::SecretString::from("assistant-test-session-secret"),
        AssistantRateLimitConfig {
            enabled: false,
            max_requests: 1,
            window: std::time::Duration::from_secs(1),
            dependency_timeout: std::time::Duration::from_secs(1),
        },
        lmm_api_rs::auth::DashboardDeveloperAccessPolicy::new(false),
    ))
}

#[tokio::test]
async fn offers_should_reject_personal_tokens_before_database_access() {
    let response = smoke_router()
        .oneshot(
            Request::get("/api/assistant/offers")
                .header(header::AUTHORIZATION, "Bearer personal-token")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    let status = response.status();
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    let body: Value = serde_json::from_slice(&body).expect("JSON response");

    assert_eq!(status, StatusCode::FORBIDDEN);
    assert_eq!(body["code"], "ASSISTANT_SESSION_REQUIRED");
}

#[tokio::test]
async fn offers_should_hide_plans_and_discounts_for_l0_before_database_access() {
    let response = smoke_router()
        .oneshot(
            Request::get("/api/assistant/offers")
                .header(header::AUTHORIZATION, "Bearer browser-session")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    let status = response.status();
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    let body: Value = serde_json::from_slice(&body).expect("JSON response");

    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["data"]["ok"], false);
    assert_eq!(body["data"]["developer_access_granted"], false);
    assert_eq!(body["data"]["read_only"], false);
    assert_eq!(body["data"]["payment_hidden"], true);
    assert_eq!(body["data"]["plans"], json!([]));
    assert_eq!(body["data"]["topup_discounts"], json!({}));
    assert!(
        body["data"]["error"]
            .as_str()
            .is_some_and(|error| error.contains("L1 access"))
    );
}
