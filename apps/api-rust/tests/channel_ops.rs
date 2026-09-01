use std::{env, sync::Arc, time::Duration};

use async_trait::async_trait;
use axum::{
    body::Body,
    http::{Request, StatusCode},
    response::Response,
};
use lmm_api_rs::auth::{
    AuthBundle, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth,
    DashboardOnboardingView, DashboardUser, DashboardUserView, LoginOutcome, LoginRequest,
    LogoutRequest, LogoutResult, RequestMetadata, TrustLevelInfo, TrustLevelTier,
    TwoFactorLoginRequest,
};
use lmm_api_rs::routes::{
    channel_core::{ChannelAction, ChannelAdminAuthorizer, ChannelError},
    channel_ops::{ChannelOpsHttpState, DashboardChannelAuthorizer, channel_ops_router},
};
use secrecy::{ExposeSecret, SecretString};
use serde_json::Value;
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

// The route-level invalid-input contracts do not need a live database.  A lazy
// pool makes this test suite safe in ordinary local and CI runs.
struct Allow;

#[async_trait]
impl ChannelAdminAuthorizer for Allow {
    async fn authorize(
        &self,
        _: &axum::http::HeaderMap,
        _: ChannelAction,
    ) -> Result<(), ChannelError> {
        Ok(())
    }
}

struct DenyAll;

#[async_trait]
impl ChannelAdminAuthorizer for DenyAll {
    async fn authorize(
        &self,
        _: &axum::http::HeaderMap,
        _: ChannelAction,
    ) -> Result<(), ChannelError> {
        Err(ChannelError::Unauthorized)
    }
}

struct DenySensitive;

#[async_trait]
impl ChannelAdminAuthorizer for DenySensitive {
    async fn authorize(
        &self,
        _: &axum::http::HeaderMap,
        action: ChannelAction,
    ) -> Result<(), ChannelError> {
        match action {
            ChannelAction::SensitiveWrite => Err(ChannelError::Forbidden),
            ChannelAction::Read | ChannelAction::Write | ChannelAction::Operate => Ok(()),
        }
    }
}

struct SignedDashboardAuth {
    role: i64,
    permissions: Value,
}

impl SignedDashboardAuth {
    fn user_view(&self) -> DashboardUserView {
        DashboardUserView {
            id: 7,
            developer_access_granted: true,
            username: "writer".to_owned(),
            display_name: "Writer".to_owned(),
            role: self.role,
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
            setting: String::new(),
            stripe_customer: String::new(),
            trust_level_info: TrustLevelInfo {
                level: 1,
                automatic_level: 1,
                override_level: None,
                paid_amount: 0.0,
                discount_ratio: 1.0,
                discount_percent: 0.0,
                next_level: Some(2),
                next_level_paid_amount: Some(100.0),
                amount_to_next_level: Some(100.0),
                next_decay_at: None,
                inactivity_decay_steps: 0,
                decay_period_days: 90,
                overridden: false,
            },
            trust_level_tiers: [TrustLevelTier {
                level: 1,
                min_paid_amount: 0.0,
                requires_successful_top_up: false,
                discount_percent: 0.0,
            }; 5],
            onboarding: DashboardOnboardingView {
                activation_complete: true,
                paid_activation_complete: true,
                credential_complete: true,
                first_request_complete: true,
                stage: "active",
            },
            sidebar_modules: serde_json::json!({}),
            permissions: self.permissions.clone(),
        }
    }
}

#[async_trait]
impl DashboardAuth for SignedDashboardAuth {
    async fn check_critical_rate_limit(
        &self,
        _: &str,
    ) -> Result<CriticalRateLimitOutcome, AuthError> {
        Ok(CriticalRateLimitOutcome::Allowed)
    }

    async fn login(&self, _: LoginRequest, _: RequestMetadata) -> Result<LoginOutcome, AuthError> {
        Err(AuthError::new(AuthErrorKind::Internal))
    }

    async fn login_2fa(
        &self,
        _: TwoFactorLoginRequest,
        _: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        Err(AuthError::new(AuthErrorKind::Internal))
    }

    async fn refresh(
        &self,
        _: SecretString,
        _: Option<String>,
        _: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        Err(AuthError::new(AuthErrorKind::Internal))
    }

    async fn self_user(&self, token: SecretString) -> Result<DashboardUser, AuthError> {
        if token.expose_secret() != "signed-dashboard-session" {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        Ok(DashboardUser {
            id: 7,
            username: "writer".to_owned(),
            display_name: "Writer".to_owned(),
            role: self.role,
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
            setting: String::new(),
            stripe_customer: String::new(),
            sidebar_modules: serde_json::json!({}),
            permissions: self.permissions.clone(),
        })
    }

    async fn self_user_view_for_optional(
        &self,
        token: SecretString,
    ) -> Result<DashboardUserView, AuthError> {
        if token.expose_secret() != "signed-dashboard-session" {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        Ok(self.user_view())
    }

    async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, AuthError> {
        Ok(LogoutResult {
            revoked_sid: None,
            cookie_cleared: None,
        })
    }

    async fn generate_personal_access_token(&self, _: SecretString) -> Result<String, AuthError> {
        Err(AuthError::new(AuthErrorKind::Internal))
    }
}

fn router() -> axum::Router {
    channel_ops_router(ChannelOpsHttpState::new(
        PgPoolOptions::new()
            .connect_lazy("postgres://unused")
            .expect("lazy pool"),
        redis::Client::open("redis://127.0.0.1/").expect("Valkey client"),
        Arc::new(Allow),
    ))
}

async fn call(request: Request<Body>) -> Response {
    router().oneshot(request).await.expect("response")
}

async fn json_body(response: Response) -> Value {
    serde_json::from_slice(
        &axum::body::to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("body"),
    )
    .expect("json")
}

#[tokio::test]
async fn tag_operation_invalid_payloads_keep_legacy_messages() {
    let response = call(
        Request::builder()
            .method("POST")
            .uri("/api/channel/tag/disabled")
            .header("content-type", "application/json")
            .body(Body::from(r#"{"tag":""}"#))
            .expect("request"),
    )
    .await;
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        json_body(response).await,
        serde_json::json!({"success":false,"message":"参数错误"})
    );

    let response = call(
        Request::builder()
            .method("PUT")
            .uri("/api/channel/tag")
            .header("content-type", "application/json")
            .body(Body::from(r#"{"tag":""}"#))
            .expect("request"),
    )
    .await;
    assert_eq!(
        json_body(response).await,
        serde_json::json!({"success":false,"message":"tag不能为空"})
    );

    let response = call(
        Request::builder()
            .method("POST")
            .uri("/api/channel/batch/tag")
            .header("content-type", "application/json")
            .body(Body::from(r#"{"ids":[]}"#))
            .expect("request"),
    )
    .await;
    assert_eq!(
        json_body(response).await,
        serde_json::json!({"success":false,"message":"参数错误"})
    );
}

#[tokio::test]
async fn malformed_tag_operation_json_keeps_the_legacy_success_envelope() {
    let response = call(
        Request::builder()
            .method("POST")
            .uri("/api/channel/tag/disabled")
            .header("content-type", "application/json")
            .body(Body::from("{"))
            .expect("request"),
    )
    .await;

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        json_body(response).await,
        serde_json::json!({"success":false,"message":"参数错误"})
    );
}

#[tokio::test]
async fn malformed_tag_operation_authentication_precedes_json_binding() {
    let app = channel_ops_router(ChannelOpsHttpState::new(
        PgPoolOptions::new()
            .connect_lazy("postgres://unused")
            .expect("lazy pool"),
        redis::Client::open("redis://127.0.0.1/").expect("Valkey client"),
        Arc::new(DenyAll),
    ));
    let response = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/channel/tag/disabled")
                .header("content-type", "application/json")
                .body(Body::from("{"))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(
        json_body(response).await,
        serde_json::json!({
            "success": false,
            "message": "Unauthorized, invalid access token",
            "code": "AUTH_UNAUTHORIZED"
        })
    );
}

#[tokio::test]
async fn tag_models_requires_tag_with_legacy_bad_request_status() {
    let response = call(
        Request::builder()
            .uri("/api/channel/tag/models")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(response.status(), StatusCode::BAD_REQUEST);
    assert_eq!(
        json_body(response).await,
        serde_json::json!({"success":false,"message":"tag不能为空"})
    );
}

#[tokio::test]
async fn tag_edit_requires_sensitive_write_for_override_fields_before_storage_access() {
    for body in [
        r#"{"tag":"oracle","param_override":"{}"}"#,
        r#"{"tag":"oracle","header_override":"{}"}"#,
    ] {
        let app = channel_ops_router(ChannelOpsHttpState::new(
            PgPoolOptions::new()
                .connect_lazy("postgres://unused")
                .expect("lazy pool"),
            redis::Client::open("redis://127.0.0.1/").expect("Valkey client"),
            Arc::new(DenySensitive),
        ));
        let response = app
            .oneshot(
                Request::builder()
                    .method("PUT")
                    .uri("/api/channel/tag")
                    .header("content-type", "application/json")
                    .body(Body::from(body))
                    .expect("request"),
            )
            .await
            .expect("response");

        assert_eq!(response.status(), StatusCode::FORBIDDEN);
        assert_eq!(
            json_body(response).await,
            serde_json::json!({"success":false,"message":"管理员权限不足"})
        );
    }
}

#[tokio::test]
async fn signed_dashboard_permission_rejects_forged_role_header_and_sensitive_override() {
    let app = channel_ops_router(ChannelOpsHttpState::new(
        PgPoolOptions::new()
            .connect_lazy("postgres://unused")
            .expect("lazy pool"),
        redis::Client::open("redis://127.0.0.1/").expect("Valkey client"),
        Arc::new(DashboardChannelAuthorizer::new(Arc::new(
            SignedDashboardAuth {
                role: 10,
                permissions: serde_json::json!({
                    "admin_permissions": {
                        "channel": {"write": true, "sensitive_write": false}
                    }
                }),
            },
        ))),
    ));
    let response = app
        .oneshot(
            Request::builder()
                .method("PUT")
                .uri("/api/channel/tag")
                .header("authorization", "Bearer signed-dashboard-session")
                .header("x-user-role", "100")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"tag":"oracle","param_override":"{}"}"#))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert_eq!(
        json_body(response).await,
        serde_json::json!({"success":false,"message":"管理员权限不足"})
    );
}

#[tokio::test]
async fn signed_dashboard_permissions_never_promote_a_non_admin_user() {
    let app = channel_ops_router(ChannelOpsHttpState::new(
        PgPoolOptions::new()
            .connect_lazy("postgres://unused")
            .expect("lazy pool"),
        redis::Client::open("redis://127.0.0.1/").expect("Valkey client"),
        Arc::new(DashboardChannelAuthorizer::new(Arc::new(
            SignedDashboardAuth {
                role: 1,
                permissions: serde_json::json!({
                    "admin_permissions": {"channel": {"write": true}}
                }),
            },
        ))),
    ));
    let response = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/channel/batch/tag")
                .header("authorization", "Bearer signed-dashboard-session")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"ids":[1],"tag":"forged"}"#))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert_eq!(
        json_body(response).await,
        serde_json::json!({"success":false,"message":"管理员权限不足"})
    );
}

#[tokio::test]
#[ignore = "requires PostgreSQL 18 and Valkey; set LMM_CHANNEL_TEST_DATABASE_URL and LMM_CHANNEL_TEST_VALKEY_URL"]
async fn tag_mutations_update_real_postgres_abilities_and_valkey_generation() {
    let database_url = env::var("LMM_CHANNEL_TEST_DATABASE_URL")
        .expect("LMM_CHANNEL_TEST_DATABASE_URL is required for the real channel test");
    let valkey_url = env::var("LMM_CHANNEL_TEST_VALKEY_URL")
        .expect("LMM_CHANNEL_TEST_VALKEY_URL is required for the real channel test");
    let pool = PgPoolOptions::new()
        .acquire_timeout(Duration::from_secs(3))
        .connect(&database_url)
        .await
        .expect("connect real PostgreSQL");
    reset_schema(&pool).await;
    sqlx::query("INSERT INTO channels (id, status, models, \"group\", tag, priority, weight) VALUES (1,1,'gpt-a,gpt-b','default','oracle',3,2), (2,1,'gpt-a','default','oracle',2,1)")
        .execute(&pool)
        .await
        .expect("channel fixtures");
    sqlx::query("INSERT INTO abilities (\"group\",model,channel_id,enabled,priority,weight,tag) VALUES ('default','gpt-a',1,TRUE,3,2,'oracle'),('default','gpt-b',1,TRUE,3,2,'oracle'),('default','gpt-a',2,TRUE,2,1,'oracle')")
        .execute(&pool)
        .await
        .expect("ability fixtures");
    let valkey = redis::Client::open(valkey_url.as_str()).expect("real Valkey URL");
    let mut cache = valkey
        .get_multiplexed_async_connection()
        .await
        .expect("connect Valkey");
    redis::cmd("DEL")
        .arg("lmm:channels:generation")
        .query_async::<()>(&mut cache)
        .await
        .expect("clear generation");
    let app = channel_ops_router(ChannelOpsHttpState::new(
        pool.clone(),
        valkey,
        Arc::new(Allow),
    ));
    let response = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/channel/tag/disabled")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"tag":"oracle"}"#))
                .expect("disable request"),
        )
        .await
        .expect("disable response");
    assert_eq!(response.status(), StatusCode::OK);
    let disabled: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM abilities WHERE tag='oracle' AND enabled=FALSE")
            .fetch_one(&pool)
            .await
            .expect("disabled ability count");
    assert_eq!(disabled, 3);
    let tagged = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/channel/batch/tag")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"ids":[1,2],"tag":"migrated"}"#))
                .expect("tag request"),
        )
        .await
        .expect("tag response");
    let tagged: Value = serde_json::from_slice(
        &axum::body::to_bytes(tagged.into_body(), usize::MAX)
            .await
            .expect("tag body"),
    )
    .expect("tag JSON");
    assert_eq!(
        tagged,
        serde_json::json!({"success":true,"message":"","data":2})
    );
    let generation: i64 = redis::cmd("GET")
        .arg("lmm:channels:generation")
        .query_async(&mut cache)
        .await
        .expect("generation");
    assert_eq!(generation, 2);
}

async fn reset_schema(pool: &sqlx::PgPool) {
    for statement in [
        "DROP TABLE IF EXISTS abilities",
        "DROP TABLE IF EXISTS channels",
        "CREATE TABLE channels (id BIGINT PRIMARY KEY,status BIGINT NOT NULL DEFAULT 1,models TEXT NOT NULL DEFAULT '',\"group\" TEXT NOT NULL DEFAULT 'default',tag TEXT,priority BIGINT DEFAULT 0,weight BIGINT DEFAULT 0,model_mapping TEXT,param_override TEXT,header_override TEXT)",
        "CREATE TABLE abilities (\"group\" TEXT NOT NULL,model TEXT NOT NULL,channel_id BIGINT NOT NULL,enabled BOOLEAN NOT NULL,priority BIGINT NOT NULL DEFAULT 0,weight BIGINT NOT NULL DEFAULT 0,tag TEXT,PRIMARY KEY (\"group\",model,channel_id))",
    ] {
        sqlx::query(statement)
            .execute(pool)
            .await
            .expect("isolated channel ops schema statement");
    }
}
