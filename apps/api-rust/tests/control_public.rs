use std::{
    env,
    sync::{
        Arc, Mutex,
        atomic::{AtomicUsize, Ordering},
    },
};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{HeaderMap, Request, StatusCode, header},
};
use lmm_api_rs::RequestContext;
use lmm_api_rs::auth::{
    AuthBundle, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth, DashboardUser,
    LoginOutcome, LoginRequest, LogoutRequest, RequestMetadata, TwoFactorLoginRequest,
    UserAuthPolicyError,
};
use lmm_api_rs::routes::control_public::{
    ControlPublicError, ControlPublicHttpState, ControlPublicRepository, PgControlPublicRepository,
    ReqwestUptimeKumaClient, UptimeHeartbeatPage, UptimeKumaClient, UptimeStatusPage,
    control_public_router,
};
use lmm_api_rs::routes::public_catalog::{
    DashboardPublicCatalogAuthorizer, HeaderNavAccess, MemoryPublicCatalogStore,
    PublicCatalogAuthError, PublicCatalogAuthorizer, PublicCatalogPrincipal,
    PublicCatalogRateLimiter, PublicCatalogState, PublicCatalogStore, PublicCatalogStoreError,
    PublicCatalogToken, parse_header_nav_access, public_catalog_router,
};
use secrecy::{ExposeSecret, SecretString};
use sqlx::postgres::PgPoolOptions;
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::TcpListener,
};
use tower::ServiceExt;

struct MissingOptions;

#[async_trait]
impl ControlPublicRepository for MissingOptions {
    async fn option(&self, _: &str) -> Result<Option<String>, ControlPublicError> {
        Ok(None)
    }
}

struct FailedOptions;

#[async_trait]
impl ControlPublicRepository for FailedOptions {
    async fn option(&self, _: &str) -> Result<Option<String>, ControlPublicError> {
        Err(ControlPublicError)
    }
}

struct PublicCatalogAuth(Option<PublicCatalogPrincipal>);

#[async_trait]
impl PublicCatalogAuthorizer for PublicCatalogAuth {
    async fn principal(
        &self,
        _: &axum::http::HeaderMap,
    ) -> Result<PublicCatalogPrincipal, PublicCatalogAuthError> {
        self.0.ok_or(PublicCatalogAuthError::UnmatchedOpaque)
    }

    async fn browser_session_principal(
        &self,
        headers: &HeaderMap,
    ) -> Result<Option<PublicCatalogPrincipal>, PublicCatalogAuthError> {
        self.principal(headers).await.map(Some)
    }
}

struct FixedPublicCatalogAuth(PublicCatalogAuthError);

#[async_trait]
impl PublicCatalogAuthorizer for FixedPublicCatalogAuth {
    async fn principal(
        &self,
        _: &HeaderMap,
    ) -> Result<PublicCatalogPrincipal, PublicCatalogAuthError> {
        Err(self.0)
    }
}

struct RecordingDashboardAuth {
    credentials: Arc<Mutex<Vec<String>>>,
}

#[async_trait]
impl DashboardAuth for RecordingDashboardAuth {
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

    async fn self_user(&self, access_token: SecretString) -> Result<DashboardUser, AuthError> {
        let credential = access_token.expose_secret().to_owned();
        self.credentials
            .lock()
            .expect("credential recorder")
            .push(credential.clone());
        if credential != "dashboard" {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        Ok(DashboardUser {
            id: 7,
            username: "dashboard".to_owned(),
            display_name: String::new(),
            role: 10,
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
            sidebar_modules: serde_json::Value::Null,
            permissions: serde_json::Value::Null,
        })
    }

    async fn logout(&self, _: LogoutRequest) -> Result<lmm_api_rs::auth::LogoutResult, AuthError> {
        panic!("unused")
    }

    async fn generate_personal_access_token(&self, _: SecretString) -> Result<String, AuthError> {
        panic!("unused")
    }
}

struct PolicyMatrixAuth {
    user: DashboardUser,
}

#[async_trait]
impl DashboardAuth for PolicyMatrixAuth {
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

    async fn self_user(&self, access_token: SecretString) -> Result<DashboardUser, AuthError> {
        if access_token.expose_secret() == "recognized" {
            Ok(self.user.clone())
        } else {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }
    }

    async fn logout(&self, _: LogoutRequest) -> Result<lmm_api_rs::auth::LogoutResult, AuthError> {
        panic!("unused")
    }

    async fn generate_personal_access_token(&self, _: SecretString) -> Result<String, AuthError> {
        panic!("unused")
    }
}

fn matrix_user(status: i64, role: i64) -> DashboardUser {
    DashboardUser {
        id: 77,
        username: "matrix-user".to_owned(),
        display_name: String::new(),
        role,
        status,
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
        sidebar_modules: serde_json::Value::Null,
        permissions: serde_json::Value::Null,
    }
}

fn public_catalog_store() -> MemoryPublicCatalogStore {
    let mut store = MemoryPublicCatalogStore {
        group_names: vec!["default".to_owned(), "vip".to_owned()],
        models: serde_json::json!({"46": ["ernie-4.0-8k-latest", "deepseek-r1"]}),
        pricing_data: serde_json::json!({
            "success": true,
            "data": [],
            "vendors": [],
            "group_ratio": {},
            "usable_group": {},
            "supported_endpoint": {},
            "auto_groups": [],
            "pricing_version": "a42d372ccf0b5dd13ecf71203521f9d2"
        }),
        ratio: Some(serde_json::json!({"model_ratio": {"gpt-4o": 1}})),
        ..Default::default()
    };
    for period in ["today", "week", "month", "year"] {
        store
            .ranking_data
            .insert(period.to_owned(), serde_json::json!({"period": period}));
    }
    store.usages.insert(
        "abc".to_owned(),
        serde_json::json!({
            "object": "token_usage",
            "name": "fixture",
            "total_granted": 13,
            "total_used": 5,
            "total_available": 8,
            "unlimited_quota": false,
            "model_limits": {"gpt-4o": true},
            "model_limits_enabled": true,
            "expires_at": 0
        }),
    );
    store
}

fn public_catalog_app(store: impl PublicCatalogStore + 'static, role: Option<i64>) -> axum::Router {
    public_catalog_router(PublicCatalogState::new(
        Arc::new(store),
        Arc::new(PublicCatalogAuth(
            role.map(|role| PublicCatalogPrincipal { user_id: 7, role }),
        )),
    ))
}

#[derive(Clone)]
struct CountingStore {
    inner: MemoryPublicCatalogStore,
    ratio_calls: Arc<AtomicUsize>,
    token_auth_calls: Arc<AtomicUsize>,
    token_usage_owners: Arc<Mutex<Vec<i64>>>,
}

#[async_trait]
impl PublicCatalogStore for CountingStore {
    async fn header_nav(&self, module: &str) -> Result<HeaderNavAccess, PublicCatalogStoreError> {
        self.inner.header_nav(module).await
    }

    async fn groups(&self) -> Result<Vec<String>, PublicCatalogStoreError> {
        self.inner.groups().await
    }

    async fn dashboard_models(&self) -> serde_json::Value {
        self.inner.dashboard_models().await
    }

    async fn pricing(
        &self,
        actor: Option<PublicCatalogPrincipal>,
    ) -> Result<serde_json::Value, PublicCatalogStoreError> {
        self.inner.pricing(actor).await
    }

    async fn rankings(&self, period: &str) -> Result<serde_json::Value, PublicCatalogStoreError> {
        self.inner.rankings(period).await
    }

    async fn exposed_ratio(&self) -> Result<Option<serde_json::Value>, PublicCatalogStoreError> {
        self.ratio_calls.fetch_add(1, Ordering::SeqCst);
        self.inner.exposed_ratio().await
    }

    async fn token_usage(
        &self,
        key: &str,
    ) -> Result<Option<serde_json::Value>, PublicCatalogStoreError> {
        self.inner.token_usage(key).await
    }

    async fn token_usage_for_owner(
        &self,
        key: &str,
        owner_id: i64,
    ) -> Result<Option<serde_json::Value>, PublicCatalogStoreError> {
        self.token_usage_owners
            .lock()
            .expect("owner recorder")
            .push(owner_id);
        self.inner.token_usage(key).await
    }

    async fn token_auth_read_only(
        &self,
        key: &str,
    ) -> Result<Option<PublicCatalogToken>, PublicCatalogStoreError> {
        self.token_auth_calls.fetch_add(1, Ordering::SeqCst);
        self.inner.token_auth_read_only(key).await
    }
}

struct FixedLimiter {
    outcome: Result<CriticalRateLimitOutcome, PublicCatalogStoreError>,
    calls: Arc<AtomicUsize>,
}

struct RecordingLimiter {
    ips: Arc<Mutex<Vec<String>>>,
    outcome: CriticalRateLimitOutcome,
}

#[async_trait]
impl PublicCatalogRateLimiter for RecordingLimiter {
    async fn check(
        &self,
        client_ip: &str,
    ) -> Result<CriticalRateLimitOutcome, PublicCatalogStoreError> {
        self.ips
            .lock()
            .expect("client ip recorder")
            .push(client_ip.to_owned());
        Ok(self.outcome)
    }
}

#[derive(Clone)]
struct RefreshStore {
    fail: Arc<AtomicUsize>,
    nav: HeaderNavAccess,
}

struct OwnerAuth;

#[async_trait]
impl PublicCatalogAuthorizer for OwnerAuth {
    async fn principal(
        &self,
        headers: &HeaderMap,
    ) -> Result<PublicCatalogPrincipal, PublicCatalogAuthError> {
        match headers
            .get(header::AUTHORIZATION)
            .and_then(|value| value.to_str().ok())
        {
            Some("Bearer owner-a") => Ok(PublicCatalogPrincipal {
                user_id: 101,
                role: 1,
            }),
            Some("Bearer owner-b") => Ok(PublicCatalogPrincipal {
                user_id: 202,
                role: 1,
            }),
            _ => Err(PublicCatalogAuthError::UnmatchedOpaque),
        }
    }
}

#[derive(Clone)]
struct OwnerPricingStore {
    inner: MemoryPublicCatalogStore,
    fail: Arc<AtomicUsize>,
}

#[async_trait]
impl PublicCatalogStore for OwnerPricingStore {
    async fn header_nav(&self, _: &str) -> Result<HeaderNavAccess, PublicCatalogStoreError> {
        Ok(HeaderNavAccess {
            enabled: true,
            require_auth: true,
        })
    }

    async fn groups(&self) -> Result<Vec<String>, PublicCatalogStoreError> {
        self.inner.groups().await
    }

    async fn dashboard_models(&self) -> serde_json::Value {
        self.inner.dashboard_models().await
    }

    async fn pricing(
        &self,
        actor: Option<PublicCatalogPrincipal>,
    ) -> Result<serde_json::Value, PublicCatalogStoreError> {
        if self.fail.load(Ordering::SeqCst) != 0 {
            return Err(PublicCatalogStoreError::new("pricing read failed"));
        }
        let owner = actor.expect("authenticated owner").user_id;
        Ok(serde_json::json!({
            "success": true,
            "data": [{"owner": owner}],
            "vendors": [],
            "group_ratio": {},
            "usable_group": {},
            "supported_endpoint": {},
            "auto_groups": ["default"],
            "pricing_version": "a42d372ccf0b5dd13ecf71203521f9d2"
        }))
    }

    async fn rankings(&self, period: &str) -> Result<serde_json::Value, PublicCatalogStoreError> {
        self.inner.rankings(period).await
    }

    async fn exposed_ratio(&self) -> Result<Option<serde_json::Value>, PublicCatalogStoreError> {
        self.inner.exposed_ratio().await
    }

    async fn token_usage(
        &self,
        key: &str,
    ) -> Result<Option<serde_json::Value>, PublicCatalogStoreError> {
        self.inner.token_usage(key).await
    }
}

impl RefreshStore {
    fn result<T>(&self, value: T) -> Result<T, PublicCatalogStoreError> {
        if self.fail.load(Ordering::SeqCst) == 0 {
            Ok(value)
        } else {
            Err(PublicCatalogStoreError::new("refresh failed"))
        }
    }
}

#[async_trait]
impl PublicCatalogStore for RefreshStore {
    async fn header_nav(&self, _: &str) -> Result<HeaderNavAccess, PublicCatalogStoreError> {
        self.result(self.nav)
    }

    async fn groups(&self) -> Result<Vec<String>, PublicCatalogStoreError> {
        self.result(vec!["refreshed".to_owned()])
    }

    async fn dashboard_models(&self) -> serde_json::Value {
        serde_json::json!({"46": ["ernie-4.0-8k-latest"]})
    }

    async fn pricing(
        &self,
        actor: Option<PublicCatalogPrincipal>,
    ) -> Result<serde_json::Value, PublicCatalogStoreError> {
        let model_name = actor.map_or("refreshed", |_| "refreshed-authenticated");
        self.result(serde_json::json!({
            "success": true,
            "data": [{"model_name": model_name}],
            "vendors": [],
            "group_ratio": {},
            "usable_group": {},
            "supported_endpoint": {},
            "auto_groups": ["default"],
            "pricing_version": "a42d372ccf0b5dd13ecf71203521f9d2"
        }))
    }

    async fn rankings(&self, _: &str) -> Result<serde_json::Value, PublicCatalogStoreError> {
        Err(PublicCatalogStoreError::new("ranking failure"))
    }

    async fn exposed_ratio(&self) -> Result<Option<serde_json::Value>, PublicCatalogStoreError> {
        self.result(Some(serde_json::json!({"model_ratio": {"refreshed": 2}})))
    }

    async fn token_usage(
        &self,
        _: &str,
    ) -> Result<Option<serde_json::Value>, PublicCatalogStoreError> {
        Ok(None)
    }
}

#[async_trait]
impl PublicCatalogRateLimiter for FixedLimiter {
    async fn check(&self, _: &str) -> Result<CriticalRateLimitOutcome, PublicCatalogStoreError> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        self.outcome.clone()
    }
}

fn limited_app(
    store: impl PublicCatalogStore + 'static,
    limiter: impl PublicCatalogRateLimiter + 'static,
) -> axum::Router {
    public_catalog_router(
        PublicCatalogState::new(Arc::new(store), Arc::new(PublicCatalogAuth(None)))
            .with_critical_rate_limiter(Arc::new(limiter)),
    )
}

async fn missing_json(
    app: axum::Router,
    request: Request<Body>,
) -> (StatusCode, String, serde_json::Value) {
    let response = app.oneshot(request).await.expect("response");
    let status = response.status();
    let content_type = response
        .headers()
        .get("content-type")
        .expect("content type")
        .to_str()
        .expect("content type text")
        .to_owned();
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    (
        status,
        content_type,
        serde_json::from_slice(&body).expect("JSON response"),
    )
}

#[tokio::test]
async fn group_route_rejects_anonymous_requests_with_legacy_auth_body() {
    let (status, content_type, body) = missing_json(
        public_catalog_app(public_catalog_store(), None),
        Request::get("/api/group/")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::UNAUTHORIZED);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(
        body,
        serde_json::json!({
            "success": false,
            "code": "AUTH_UNAUTHORIZED",
            "message": "Unauthorized, invalid access token"
        })
    );
}

#[tokio::test]
async fn group_route_rejects_non_admin_users_with_legacy_privilege_body() {
    let (status, content_type, body) = missing_json(
        public_catalog_app(public_catalog_store(), Some(1)),
        Request::get("/api/group/")
            .header("authorization", "Bearer dashboard")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::FORBIDDEN);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(
        body,
        serde_json::json!({
            "success": false,
            "code": "AUTH_INSUFFICIENT_PRIVILEGE",
            "message": "Unauthorized, insufficient privileges"
        })
    );
}

#[tokio::test]
async fn group_route_returns_configured_groups_for_admin_users() {
    let (status, content_type, body) = missing_json(
        public_catalog_app(public_catalog_store(), Some(10)),
        Request::get("/api/group/")
            .header("authorization", "Bearer dashboard")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(
        body,
        serde_json::json!({
            "success": true,
            "message": "",
            "data": ["default", "vip"]
        })
    );
}

#[tokio::test]
async fn models_route_has_only_the_frozen_legacy_envelope_fields() {
    let (status, _, body) = missing_json(
        public_catalog_app(public_catalog_store(), Some(1)),
        Request::get("/api/models")
            .header(header::AUTHORIZATION, "Bearer dashboard")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(
        body,
        serde_json::json!({
            "success": true,
            "data": {"46": ["ernie-4.0-8k-latest", "deepseek-r1"]}
        })
    );
    assert!(body.get("message").is_none());
}

#[tokio::test]
async fn dashboard_adapter_distinguishes_raw_bearer_missing_opaque_and_internal_jwt() {
    let credentials = Arc::new(Mutex::new(Vec::new()));
    let auth = Arc::new(RecordingDashboardAuth {
        credentials: Arc::clone(&credentials),
    });
    let mut store = public_catalog_store();
    store.nav.insert(
        "pricing".to_owned(),
        HeaderNavAccess {
            enabled: true,
            require_auth: false,
        },
    );
    let app = public_catalog_router(PublicCatalogState::new(
        Arc::new(store),
        Arc::new(DashboardPublicCatalogAuthorizer::new(auth)),
    ));

    for authorization in ["dashboard", "Bearer dashboard"] {
        let response = app
            .clone()
            .oneshot(
                Request::get("/api/pricing")
                    .header(header::AUTHORIZATION, authorization)
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(response.status(), StatusCode::OK, "{authorization}");
        assert_eq!(
            response.headers()["auth-version"],
            "864b7076dbcd0a3c01b5520316720ebf"
        );
    }

    let missing = app
        .clone()
        .oneshot(
            Request::get("/api/pricing")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(missing.status(), StatusCode::OK);
    assert!(!missing.headers().contains_key("auth-version"));

    let opaque = app
        .clone()
        .oneshot(
            Request::get("/api/pricing")
                .header(header::AUTHORIZATION, "Bearer opaque-relay-key")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(opaque.status(), StatusCode::OK);
    assert!(!opaque.headers().contains_key("auth-version"));

    const INVALID_INTERNAL_JWT: &str = "eyJhbGciOiJIUzI1NiJ9.eyJ0b2tlbl91c2UiOiJhY2Nlc3MiLCJpc3MiOiJuZXctYXBpIiwiYXVkIjpbIm5ldy1hcGktZGFzaGJvYXJkIl19.tampered";
    let internal = app
        .clone()
        .oneshot(
            Request::get("/api/pricing")
                .header(header::AUTHORIZATION, INVALID_INTERNAL_JWT)
                .header(header::ACCEPT_LANGUAGE, "zh-CN")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(internal.status(), StatusCode::UNAUTHORIZED);
    let body = to_bytes(internal.into_body(), usize::MAX)
        .await
        .expect("body");
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(&body).expect("JSON"),
        serde_json::json!({
            "success": false,
            "code": "AUTH_UNAUTHORIZED",
            "message": "无权进行此操作，access token 无效"
        })
    );
    assert_eq!(
        *credentials.lock().expect("credential recorder"),
        vec![
            "dashboard".to_owned(),
            "dashboard".to_owned(),
            "opaque-relay-key".to_owned(),
            INVALID_INTERNAL_JWT.to_owned(),
        ]
    );

    let malformed_header = app
        .oneshot(
            Request::get("/api/pricing")
                .header(
                    header::AUTHORIZATION,
                    "e30.eyJ0b2tlbl91c2UiOiJhY2Nlc3MiLCJpc3MiOiJuZXctYXBpIiwiYXVkIjpbIm5ldy1hcGktZGFzaGJvYXJkIl19.tampered",
                )
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(malformed_header.status(), StatusCode::OK);
    assert!(!malformed_header.headers().contains_key("auth-version"));
}

#[tokio::test]
async fn dashboard_adapter_policy_matrix_keeps_optional_and_required_headers_distinct() {
    let mut store = public_catalog_store();
    store.nav.insert(
        "pricing".to_owned(),
        HeaderNavAccess {
            enabled: true,
            require_auth: false,
        },
    );

    let optional = public_catalog_router(PublicCatalogState::new(
        Arc::new(store.clone()),
        Arc::new(DashboardPublicCatalogAuthorizer::new(Arc::new(
            PolicyMatrixAuth {
                user: matrix_user(2, 10),
            },
        ))),
    ));
    let disabled = optional
        .oneshot(
            Request::get("/api/pricing")
                .header(header::AUTHORIZATION, "Bearer recognized")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(disabled.status(), StatusCode::OK);
    assert_eq!(
        disabled.headers()["auth-version"],
        "864b7076dbcd0a3c01b5520316720ebf"
    );

    let required = public_catalog_router(PublicCatalogState::new(
        Arc::new(store),
        Arc::new(DashboardPublicCatalogAuthorizer::new(Arc::new(
            PolicyMatrixAuth {
                user: matrix_user(2, 10),
            },
        ))),
    ));
    let disabled_required = required
        .oneshot(
            Request::get("/api/models")
                .header(header::AUTHORIZATION, "Bearer recognized")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(disabled_required.status(), StatusCode::UNAUTHORIZED);
    assert!(!disabled_required.headers().contains_key("auth-version"));

    let role_denied = public_catalog_router(PublicCatalogState::new(
        Arc::new(public_catalog_store()),
        Arc::new(DashboardPublicCatalogAuthorizer::new(Arc::new(
            PolicyMatrixAuth {
                user: matrix_user(1, 1),
            },
        ))),
    ));
    let role_denied = role_denied
        .oneshot(
            Request::get("/api/group/")
                .header(header::AUTHORIZATION, "Bearer recognized")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(role_denied.status(), StatusCode::FORBIDDEN);
    assert!(!role_denied.headers().contains_key("auth-version"));

    let opaque = public_catalog_router(PublicCatalogState::new(
        Arc::new(public_catalog_store()),
        Arc::new(DashboardPublicCatalogAuthorizer::new(Arc::new(
            RecordingDashboardAuth {
                credentials: Arc::new(Mutex::new(Vec::new())),
            },
        ))),
    ));
    let opaque = opaque
        .oneshot(
            Request::get("/api/pricing")
                .header(header::AUTHORIZATION, "Bearer opaque-relay-key")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(opaque.status(), StatusCode::OK);
    assert!(!opaque.headers().contains_key("auth-version"));
}

#[tokio::test]
async fn console_gate_hides_l0_pricing_before_optional_header_nav_or_store_reads() {
    for path in ["/api/pricing", "/api/assistant/pricing"] {
        let auth: Arc<dyn DashboardAuth> = Arc::new(PolicyMatrixAuth {
            user: matrix_user(1, 1),
        });
        let app = public_catalog_router(
            PublicCatalogState::new(
                Arc::new(public_catalog_store()),
                Arc::new(DashboardPublicCatalogAuthorizer::new(Arc::clone(&auth))),
            )
            .with_console_access_gate(auth),
        );
        let response = app
            .oneshot(
                Request::get(path)
                    .header(header::AUTHORIZATION, "Bearer recognized")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(response.status(), StatusCode::NOT_FOUND, "{path}");
        assert_eq!(
            serde_json::from_slice::<serde_json::Value>(
                &to_bytes(response.into_body(), usize::MAX)
                    .await
                    .expect("body"),
            )
            .expect("JSON"),
            serde_json::json!({"message": "Not Found"}),
            "{path}"
        );
    }
}

#[tokio::test]
async fn required_header_nav_enforces_user_auth_policy_statuses_without_auth_version() {
    for (error, expected_status) in [
        (UserAuthPolicyError::UserDisabled, StatusCode::UNAUTHORIZED),
        (
            UserAuthPolicyError::InsufficientPrivilege,
            StatusCode::FORBIDDEN,
        ),
        (
            UserAuthPolicyError::InvalidUserInfo,
            StatusCode::UNAUTHORIZED,
        ),
    ] {
        let mut store = public_catalog_store();
        store.nav.insert(
            "pricing".to_owned(),
            HeaderNavAccess {
                enabled: true,
                require_auth: true,
            },
        );
        let response = public_catalog_router(PublicCatalogState::new(
            Arc::new(store),
            Arc::new(FixedPublicCatalogAuth(PublicCatalogAuthError::UserAuth(
                error,
            ))),
        ))
        .oneshot(
            Request::get("/api/pricing")
                .header(header::AUTHORIZATION, "Bearer recognized")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
        assert_eq!(response.status(), expected_status);
        assert!(!response.headers().contains_key("auth-version"));
    }
}

#[tokio::test]
async fn dashboard_successes_include_the_frozen_auth_version_header() {
    for (uri, module, role) in [
        ("/api/group/", "", 10),
        ("/api/models", "", 1),
        ("/api/pricing", "pricing", 1),
        ("/api/rankings?period=week", "rankings", 1),
    ] {
        let mut store = public_catalog_store();
        if !module.is_empty() {
            store.nav.insert(
                module.to_owned(),
                HeaderNavAccess {
                    enabled: true,
                    require_auth: true,
                },
            );
        }
        let response = public_catalog_app(store, Some(role))
            .oneshot(
                Request::get(uri)
                    .header(header::AUTHORIZATION, "Bearer dashboard")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(response.status(), StatusCode::OK, "{uri}");
        assert_eq!(
            response
                .headers()
                .get("auth-version")
                .and_then(|value| value.to_str().ok()),
            Some("864b7076dbcd0a3c01b5520316720ebf"),
            "{uri}"
        );
    }
}

#[tokio::test]
async fn successful_authentication_versions_validation_store_and_handler_errors() {
    let denied = public_catalog_app(public_catalog_store(), Some(1))
        .oneshot(
            Request::get("/api/group/")
                .header(header::AUTHORIZATION, "Bearer dashboard")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(denied.status(), StatusCode::FORBIDDEN);
    assert!(!denied.headers().contains_key("auth-version"));

    let invalid = public_catalog_app(public_catalog_store(), Some(1))
        .oneshot(
            Request::get("/api/rankings?period=quarter")
                .header(header::AUTHORIZATION, "Bearer dashboard")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(invalid.status(), StatusCode::BAD_REQUEST);
    assert_eq!(
        invalid.headers()["auth-version"],
        "864b7076dbcd0a3c01b5520316720ebf"
    );

    let mut store = public_catalog_store();
    store.ranking_error = Some(PublicCatalogStoreError::new("ranking read failed"));
    let failed = public_catalog_app(store, Some(1))
        .oneshot(
            Request::get("/api/rankings?period=week")
                .header(header::AUTHORIZATION, "Bearer dashboard")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(failed.status(), StatusCode::BAD_REQUEST);
    assert_eq!(
        failed.headers()["auth-version"],
        "864b7076dbcd0a3c01b5520316720ebf"
    );

    let mut token_store = public_catalog_store();
    token_store.token_usage_error = Some(PublicCatalogStoreError::new("usage read failed"));
    let token_failed = public_catalog_app(token_store, None)
        .oneshot(
            Request::get("/api/usage/token/")
                .header(header::AUTHORIZATION, "Bearer abc")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(token_failed.status(), StatusCode::OK);
    assert!(!token_failed.headers().contains_key("auth-version"));
}

#[tokio::test]
async fn dashboard_auth_preserves_expired_revoked_and_unauthorized_errors() {
    for (error, code, message) in [
        (
            PublicCatalogAuthError::TokenExpired,
            "AUTH_TOKEN_EXPIRED",
            "Unauthorized, not logged in and no access token provided",
        ),
        (
            PublicCatalogAuthError::SessionRevoked,
            "AUTH_SESSION_REVOKED",
            "Unauthorized, not logged in and no access token provided",
        ),
        (
            PublicCatalogAuthError::Unauthorized,
            "AUTH_UNAUTHORIZED",
            "Unauthorized, invalid access token",
        ),
    ] {
        let response = public_catalog_router(PublicCatalogState::new(
            Arc::new(public_catalog_store()),
            Arc::new(FixedPublicCatalogAuth(error)),
        ))
        .oneshot(
            Request::get("/api/models")
                .header(header::AUTHORIZATION, "Bearer fixture")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(
            response
                .headers()
                .get(header::CONTENT_TYPE)
                .and_then(|value| value.to_str().ok()),
            Some("application/json; charset=utf-8")
        );
        let body = to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("body");
        assert_eq!(
            serde_json::from_slice::<serde_json::Value>(&body).expect("JSON"),
            serde_json::json!({"success": false, "code": code, "message": message})
        );
    }
}

#[tokio::test]
async fn optional_header_nav_unknown_opaque_credentials_remain_anonymous() {
    let mut store = public_catalog_store();
    store.nav.insert(
        "pricing".to_owned(),
        HeaderNavAccess {
            enabled: true,
            require_auth: false,
        },
    );
    let (status, content_type, body) = missing_json(
        public_catalog_app(store, None),
        Request::get("/api/pricing")
            .header(header::AUTHORIZATION, "Bearer opaque-relay-key")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(body["success"], true);
}

#[tokio::test]
async fn required_header_nav_invalid_credentials_are_json_with_charset() {
    let mut store = public_catalog_store();
    store.nav.insert(
        "pricing".to_owned(),
        HeaderNavAccess {
            enabled: true,
            require_auth: true,
        },
    );
    let (status, content_type, body) = missing_json(
        public_catalog_app(store, None),
        Request::get("/api/pricing")
            .header(header::AUTHORIZATION, "Bearer opaque-relay-key")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(status, StatusCode::UNAUTHORIZED);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(body["code"], "AUTH_UNAUTHORIZED");
}

#[test]
fn header_nav_parser_matches_the_frozen_option_shapes() {
    for (raw, expected) in [
        (
            serde_json::json!({"enabled": true, "requireAuth": true}),
            HeaderNavAccess {
                enabled: true,
                require_auth: true,
            },
        ),
        (
            serde_json::json!({"enabled": "0", "requireAuth": "1"}),
            HeaderNavAccess {
                enabled: false,
                require_auth: true,
            },
        ),
        (
            serde_json::json!(false),
            HeaderNavAccess {
                enabled: false,
                require_auth: false,
            },
        ),
        (
            serde_json::json!("true"),
            HeaderNavAccess {
                enabled: true,
                require_auth: false,
            },
        ),
        (
            serde_json::json!(1),
            HeaderNavAccess {
                enabled: true,
                require_auth: false,
            },
        ),
        (
            serde_json::json!({"enabled": 0, "require_auth": true}),
            HeaderNavAccess {
                enabled: false,
                require_auth: false,
            },
        ),
    ] {
        assert_eq!(parse_header_nav_access(Some(&raw)), expected, "{raw}");
    }
    assert_eq!(parse_header_nav_access(None), HeaderNavAccess::default());
}

#[tokio::test]
async fn token_usage_preflight_matches_legacy_cors_boundary() {
    let response = public_catalog_app(public_catalog_store(), None)
        .oneshot(
            Request::builder()
                .method("OPTIONS")
                .uri("/api/usage/token/")
                .header(header::ORIGIN, "https://browser.example")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::NO_CONTENT);
    assert_eq!(response.headers()["access-control-allow-origin"], "*");
    assert_eq!(
        response.headers()["access-control-allow-credentials"],
        "true"
    );
    assert_eq!(
        response.headers()["access-control-allow-methods"],
        "GET,POST,PUT,DELETE,OPTIONS"
    );
    assert_eq!(response.headers()["access-control-allow-headers"], "*");
    assert_eq!(response.headers()["access-control-max-age"], "43200");
}

#[tokio::test]
async fn token_usage_origin_preflight_skips_limiter_and_store() {
    let ratio_calls = Arc::new(AtomicUsize::new(0));
    let token_auth_calls = Arc::new(AtomicUsize::new(0));
    let limiter_calls = Arc::new(AtomicUsize::new(0));
    let store = CountingStore {
        inner: public_catalog_store(),
        ratio_calls,
        token_auth_calls: Arc::clone(&token_auth_calls),
        token_usage_owners: Arc::new(Mutex::new(Vec::new())),
    };
    let response = limited_app(
        store,
        FixedLimiter {
            outcome: Ok(CriticalRateLimitOutcome::Rejected {
                retry_after_seconds: 7,
            }),
            calls: Arc::clone(&limiter_calls),
        },
    )
    .oneshot(
        Request::builder()
            .method("OPTIONS")
            .uri("/api/usage/token/")
            .header(header::ORIGIN, "https://browser.example")
            .body(Body::empty())
            .expect("request"),
    )
    .await
    .expect("response");
    assert_eq!(response.status(), StatusCode::NO_CONTENT);
    assert_eq!(limiter_calls.load(Ordering::SeqCst), 0);
    assert_eq!(token_auth_calls.load(Ordering::SeqCst), 0);
}

#[tokio::test]
async fn token_usage_origin_get_and_rate_limit_keep_legacy_cors_headers() {
    let allowed = public_catalog_app(public_catalog_store(), None)
        .oneshot(
            Request::get("/api/usage/token/")
                .header(header::ORIGIN, "https://browser.example")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(allowed.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(allowed.headers()["access-control-allow-origin"], "*");
    assert_eq!(
        allowed.headers()["access-control-allow-credentials"],
        "true"
    );
    assert!(
        !allowed
            .headers()
            .contains_key("access-control-allow-methods")
    );

    let limited = limited_app(
        public_catalog_store(),
        FixedLimiter {
            outcome: Ok(CriticalRateLimitOutcome::Rejected {
                retry_after_seconds: 7,
            }),
            calls: Arc::new(AtomicUsize::new(0)),
        },
    )
    .oneshot(
        Request::get("/api/usage/token/")
            .header(header::ORIGIN, "https://browser.example")
            .body(Body::empty())
            .expect("request"),
    )
    .await
    .expect("response");
    assert_eq!(limited.status(), StatusCode::TOO_MANY_REQUESTS);
    assert_eq!(limited.headers()["retry-after"], "7");
    assert_eq!(limited.headers()["access-control-allow-origin"], "*");
    assert_eq!(
        limited.headers()["access-control-allow-credentials"],
        "true"
    );

    let same_origin = public_catalog_app(public_catalog_store(), None)
        .oneshot(
            Request::get("/api/usage/token/")
                .header(header::HOST, "panel.example")
                .header(header::ORIGIN, "https://panel.example")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(same_origin.status(), StatusCode::UNAUTHORIZED);
    assert!(
        !same_origin
            .headers()
            .contains_key("access-control-allow-origin")
    );
}

#[tokio::test]
async fn originless_token_usage_options_falls_through_method_handling() {
    let response = public_catalog_app(public_catalog_store(), None)
        .oneshot(
            Request::builder()
                .method("OPTIONS")
                .uri("/api/usage/token/")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::NOT_FOUND);
    assert_eq!(
        response.headers()[header::CONTENT_TYPE],
        "application/json; charset=utf-8"
    );
    assert!(
        !response
            .headers()
            .contains_key("access-control-allow-origin")
    );
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(&body).expect("json"),
        serde_json::json!({
            "error": {
                "message": "Invalid URL (OPTIONS /api/usage/token/)",
                "type": "invalid_request_error",
                "param": "",
                "code": ""
            }
        })
    );
}

#[tokio::test]
async fn critical_limiter_uses_only_listener_request_context_ip() {
    let ips = Arc::new(Mutex::new(Vec::new()));
    let app = limited_app(
        public_catalog_store(),
        RecordingLimiter {
            ips: Arc::clone(&ips),
            outcome: CriticalRateLimitOutcome::Rejected {
                retry_after_seconds: 1,
            },
        },
    );

    let mut trusted = Request::get("/api/usage/token/")
        .header(header::ORIGIN, "https://browser.example")
        .header("x-real-ip", "198.51.100.8")
        .body(Body::empty())
        .expect("request");
    trusted.extensions_mut().insert(RequestContext {
        request_id: "test".to_owned(),
        client_ip: Some("203.0.113.9".parse().expect("ip")),
    });
    let response = app.clone().oneshot(trusted).await.expect("response");
    assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);

    let spoofed = Request::get("/api/usage/token/")
        .header("x-real-ip", "198.51.100.8")
        .body(Body::empty())
        .expect("request");
    let response = app.clone().oneshot(spoofed).await.expect("response");
    assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);

    let direct = Request::get("/api/usage/token/")
        .body(Body::empty())
        .expect("request");
    let response = app.oneshot(direct).await.expect("response");
    assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);

    assert_eq!(
        *ips.lock().expect("client ip recorder"),
        vec![
            "203.0.113.9".to_owned(),
            "unknown".to_owned(),
            "unknown".to_owned(),
        ]
    );
}

#[tokio::test]
async fn pricing_route_preserves_go_top_level_shape_and_version() {
    let (status, content_type, body) = missing_json(
        public_catalog_app(public_catalog_store(), None),
        Request::get("/api/pricing")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(body["success"], true);
    assert_eq!(body["data"], serde_json::json!([]));
    assert_eq!(body["pricing_version"], "a42d372ccf0b5dd13ecf71203521f9d2");
}

#[tokio::test]
async fn assistant_pricing_should_require_a_dashboard_user_and_reuse_pricing_shape() {
    let (status, content_type, body) = missing_json(
        public_catalog_app(public_catalog_store(), Some(1)),
        Request::get("/api/assistant/pricing")
            .header(header::AUTHORIZATION, "Bearer dashboard")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(body["success"], true);
    assert_eq!(body["data"], serde_json::json!([]));
    assert_eq!(body["pricing_version"], "a42d372ccf0b5dd13ecf71203521f9d2");
}

#[tokio::test]
async fn assistant_pricing_should_reject_a_personal_token_without_a_browser_session() {
    let app = public_catalog_router(PublicCatalogState::new(
        Arc::new(public_catalog_store()),
        Arc::new(DashboardPublicCatalogAuthorizer::new(Arc::new(
            RecordingDashboardAuth {
                credentials: Arc::new(Mutex::new(Vec::new())),
            },
        ))),
    ));
    let (status, _, body) = missing_json(
        app,
        Request::get("/api/assistant/pricing")
            .header(header::AUTHORIZATION, "Bearer dashboard")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::FORBIDDEN);
    assert_eq!(body["code"], "ASSISTANT_SESSION_REQUIRED");
}

#[tokio::test]
async fn cache_backed_routes_use_cold_defaults_refresh_and_retain_last_good() {
    let fail = Arc::new(AtomicUsize::new(1));
    let app = public_catalog_app(
        RefreshStore {
            fail: Arc::clone(&fail),
            nav: HeaderNavAccess::default(),
        },
        Some(10),
    );

    let (_, _, cold_groups) = missing_json(
        app.clone(),
        Request::get("/api/group/")
            .header(header::AUTHORIZATION, "dashboard")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    let (_, _, cold_pricing) = missing_json(
        app.clone(),
        Request::get("/api/pricing")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    let (cold_ratio_status, _, _) = missing_json(
        app.clone(),
        Request::get("/api/ratio_config")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(cold_groups["data"], serde_json::json!([]));
    assert_eq!(cold_pricing["data"], serde_json::json!([]));
    assert_eq!(cold_pricing["auto_groups"], serde_json::json!(["default"]));
    assert_eq!(cold_ratio_status, StatusCode::FORBIDDEN);

    fail.store(0, Ordering::SeqCst);
    let (_, _, refreshed_groups) = missing_json(
        app.clone(),
        Request::get("/api/group/")
            .header(header::AUTHORIZATION, "dashboard")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    let (_, _, refreshed_pricing) = missing_json(
        app.clone(),
        Request::get("/api/pricing")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    let (_, _, refreshed_ratio) = missing_json(
        app.clone(),
        Request::get("/api/ratio_config")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(refreshed_groups["data"], serde_json::json!(["refreshed"]));
    assert_eq!(refreshed_pricing["data"][0]["model_name"], "refreshed");
    assert_eq!(refreshed_ratio["data"]["model_ratio"]["refreshed"], 2);

    fail.store(1, Ordering::SeqCst);
    let (_, _, retained_groups) = missing_json(
        app.clone(),
        Request::get("/api/group/")
            .header(header::AUTHORIZATION, "dashboard")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    let (_, _, retained_pricing) = missing_json(
        app.clone(),
        Request::get("/api/pricing")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    let (_, _, retained_ratio) = missing_json(
        app,
        Request::get("/api/ratio_config")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(retained_groups, refreshed_groups);
    assert_eq!(retained_pricing, refreshed_pricing);
    assert_eq!(retained_ratio, refreshed_ratio);
}

#[tokio::test]
async fn pricing_last_good_cache_isolated_for_two_authenticated_owners() {
    let fail = Arc::new(AtomicUsize::new(0));
    let app = public_catalog_router(PublicCatalogState::new(
        Arc::new(OwnerPricingStore {
            inner: public_catalog_store(),
            fail: Arc::clone(&fail),
        }),
        Arc::new(OwnerAuth),
    ));

    for (credential, owner) in [("Bearer owner-a", 101), ("Bearer owner-b", 202)] {
        let response = app
            .clone()
            .oneshot(
                Request::get("/api/pricing")
                    .header(header::AUTHORIZATION, credential)
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        let body = to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("body");
        assert_eq!(
            serde_json::from_slice::<serde_json::Value>(&body).expect("json")["data"][0]["owner"],
            owner
        );
    }
    fail.store(1, Ordering::SeqCst);

    for (credential, owner) in [("Bearer owner-a", 101), ("Bearer owner-b", 202)] {
        let response = app
            .clone()
            .oneshot(
                Request::get("/api/pricing")
                    .header(header::AUTHORIZATION, credential)
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        let body = to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("body");
        assert_eq!(
            serde_json::from_slice::<serde_json::Value>(&body).expect("json")["data"][0]["owner"],
            owner
        );
    }
}

#[tokio::test]
async fn header_nav_refresh_failure_retains_the_last_good_access_policy() {
    let fail = Arc::new(AtomicUsize::new(0));
    let app = public_catalog_app(
        RefreshStore {
            fail: Arc::clone(&fail),
            nav: HeaderNavAccess {
                enabled: false,
                require_auth: false,
            },
        },
        None,
    );
    let first = app
        .clone()
        .oneshot(
            Request::get("/api/pricing")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(first.status(), StatusCode::FORBIDDEN);

    fail.store(1, Ordering::SeqCst);
    let retained = app
        .oneshot(
            Request::get("/api/pricing")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(retained.status(), StatusCode::FORBIDDEN);
}

#[tokio::test]
async fn pricing_last_good_is_isolated_between_public_and_authenticated_owners() {
    let fail = Arc::new(AtomicUsize::new(0));
    let app = public_catalog_app(
        RefreshStore {
            fail: Arc::clone(&fail),
            nav: HeaderNavAccess::default(),
        },
        Some(10),
    );
    for authorization in [Some("dashboard"), None] {
        let mut request = Request::get("/api/pricing");
        if let Some(authorization) = authorization {
            request = request.header(header::AUTHORIZATION, authorization);
        }
        let response = app
            .clone()
            .oneshot(request.body(Body::empty()).expect("request"))
            .await
            .expect("response");
        assert_eq!(response.status(), StatusCode::OK);
    }

    fail.store(1, Ordering::SeqCst);
    let (_, _, authenticated) = missing_json(
        app.clone(),
        Request::get("/api/pricing")
            .header(header::AUTHORIZATION, "dashboard")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    let (_, _, public) = missing_json(
        app,
        Request::get("/api/pricing")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(
        authenticated["data"][0]["model_name"],
        "refreshed-authenticated"
    );
    assert_eq!(public["data"][0]["model_name"], "refreshed");
}

#[tokio::test]
async fn rankings_route_uses_week_for_missing_and_empty_periods() {
    for uri in ["/api/rankings", "/api/rankings?period="] {
        let (status, content_type, body) = missing_json(
            public_catalog_app(public_catalog_store(), None),
            Request::get(uri).body(Body::empty()).expect("request"),
        )
        .await;

        assert_eq!(status, StatusCode::OK);
        assert_eq!(content_type, "application/json; charset=utf-8");
        assert_eq!(
            body,
            serde_json::json!({"success": true, "data": {"period": "week"}})
        );
    }
}

#[tokio::test]
async fn rankings_route_accepts_each_go_period() {
    for period in ["today", "week", "month", "year"] {
        let (status, content_type, body) = missing_json(
            public_catalog_app(public_catalog_store(), None),
            Request::get(format!("/api/rankings?period={period}"))
                .body(Body::empty())
                .expect("request"),
        )
        .await;

        assert_eq!(status, StatusCode::OK);
        assert_eq!(content_type, "application/json; charset=utf-8");
        assert_eq!(body["data"]["period"], period);
    }
}

#[tokio::test]
async fn rankings_route_rejects_unknown_periods_with_go_error_text() {
    let (status, content_type, body) = missing_json(
        public_catalog_app(public_catalog_store(), None),
        Request::get("/api/rankings?period=quarter")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::BAD_REQUEST);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(
        body,
        serde_json::json!({"success": false, "message": "invalid ranking period: quarter"})
    );
}

#[tokio::test]
async fn rankings_route_preserves_every_dependency_error_body_and_status() {
    let mut store = public_catalog_store();
    store.ranking_error = Some(PublicCatalogStoreError::new("redis: connection refused"));
    let (status, content_type, body) = missing_json(
        public_catalog_app(store, None),
        Request::get("/api/rankings?period=week")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::BAD_REQUEST);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(
        body,
        serde_json::json!({"success": false, "message": "redis: connection refused"})
    );
}

#[tokio::test]
async fn rankings_parses_raw_query_after_optional_auth_with_go_first_value_rules() {
    let mut store = public_catalog_store();
    store.nav.insert(
        "rankings".to_owned(),
        HeaderNavAccess {
            enabled: true,
            require_auth: false,
        },
    );
    let app = public_catalog_app(store, Some(1));

    for uri in [
        "/api/rankings?period=month&period=quarter",
        "/api/rankings?period=%6Donth",
    ] {
        let response = app
            .clone()
            .oneshot(
                Request::get(uri)
                    .header(header::AUTHORIZATION, "Bearer dashboard")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(response.status(), StatusCode::OK, "{uri}");
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "application/json; charset=utf-8"
        );
        assert_eq!(
            response.headers()["auth-version"],
            "864b7076dbcd0a3c01b5520316720ebf"
        );
        let body = to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("body");
        assert_eq!(
            serde_json::from_slice::<serde_json::Value>(&body).expect("json")["data"]["period"],
            "month"
        );
    }
}

#[tokio::test]
async fn rankings_discards_malformed_query_pairs_after_auth() {
    let mut store = public_catalog_store();
    store.nav.insert(
        "rankings".to_owned(),
        HeaderNavAccess {
            enabled: true,
            require_auth: false,
        },
    );
    let app = public_catalog_app(store, Some(1));
    for uri in [
        "/api/rankings?period=%",
        "/api/rankings?period=month;period=quarter",
        "/api/rankings?period=%&period=month",
    ] {
        let response = app
            .clone()
            .oneshot(
                Request::get(uri)
                    .header(header::AUTHORIZATION, "Bearer dashboard")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(response.status(), StatusCode::OK, "{uri}");
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "application/json; charset=utf-8"
        );
        assert_eq!(
            response.headers()["auth-version"],
            "864b7076dbcd0a3c01b5520316720ebf"
        );
        let body = to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("body");
        let expected_period = if uri.ends_with("period=%&period=month") {
            "month"
        } else {
            "week"
        };
        assert_eq!(
            serde_json::from_slice::<serde_json::Value>(&body).expect("json")["data"]["period"],
            expected_period
        );
    }

    for uri in [
        "/api/rankings?period=%FF",
        "/api/rankings?period=%FF&period=month",
    ] {
        let response = app
            .clone()
            .oneshot(
                Request::get(uri)
                    .header(header::AUTHORIZATION, "Bearer dashboard")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(response.status(), StatusCode::BAD_REQUEST, "{uri}");
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "application/json; charset=utf-8"
        );
    }
}

#[tokio::test]
async fn ratio_config_route_returns_enabled_legacy_envelope() {
    let (status, content_type, body) = missing_json(
        public_catalog_app(public_catalog_store(), None),
        Request::get("/api/ratio_config")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(
        body,
        serde_json::json!({
            "success": true,
            "message": "",
            "data": {"model_ratio": {"gpt-4o": 1}}
        })
    );
}

#[tokio::test]
async fn ratio_config_route_reports_when_exposure_is_disabled() {
    let mut store = public_catalog_store();
    store.ratio = None;
    let (status, content_type, body) = missing_json(
        public_catalog_app(store, None),
        Request::get("/api/ratio_config")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::FORBIDDEN);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(
        body,
        serde_json::json!({"success": false, "message": "倍率配置接口未启用"})
    );
}

#[tokio::test]
async fn critical_limiter_rejects_both_routes_without_calling_the_store() {
    for uri in ["/api/ratio_config", "/api/usage/token/"] {
        let ratio_calls = Arc::new(AtomicUsize::new(0));
        let token_auth_calls = Arc::new(AtomicUsize::new(0));
        let limiter_calls = Arc::new(AtomicUsize::new(0));
        let store = CountingStore {
            inner: public_catalog_store(),
            ratio_calls: Arc::clone(&ratio_calls),
            token_auth_calls: Arc::clone(&token_auth_calls),
            token_usage_owners: Arc::new(Mutex::new(Vec::new())),
        };
        let response = limited_app(
            store,
            FixedLimiter {
                outcome: Ok(CriticalRateLimitOutcome::Rejected {
                    retry_after_seconds: 7,
                }),
                calls: Arc::clone(&limiter_calls),
            },
        )
        .oneshot(
            Request::get(uri)
                .header(header::AUTHORIZATION, "Bearer abc")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS, "{uri}");
        assert_eq!(response.headers()[header::RETRY_AFTER], "7", "{uri}");
        assert!(
            to_bytes(response.into_body(), usize::MAX)
                .await
                .expect("body")
                .is_empty(),
            "{uri}"
        );
        assert_eq!(limiter_calls.load(Ordering::SeqCst), 1, "{uri}");
        assert_eq!(ratio_calls.load(Ordering::SeqCst), 0, "{uri}");
        assert_eq!(token_auth_calls.load(Ordering::SeqCst), 0, "{uri}");
    }
}

#[tokio::test]
async fn critical_limiter_failures_are_empty_500_without_downstream_calls() {
    for uri in ["/api/ratio_config", "/api/usage/token/"] {
        let ratio_calls = Arc::new(AtomicUsize::new(0));
        let token_auth_calls = Arc::new(AtomicUsize::new(0));
        let store = CountingStore {
            inner: public_catalog_store(),
            ratio_calls: Arc::clone(&ratio_calls),
            token_auth_calls: Arc::clone(&token_auth_calls),
            token_usage_owners: Arc::new(Mutex::new(Vec::new())),
        };
        let response = limited_app(
            store,
            FixedLimiter {
                outcome: Err(PublicCatalogStoreError::new("limiter unavailable")),
                calls: Arc::new(AtomicUsize::new(0)),
            },
        )
        .oneshot(
            Request::get(uri)
                .header(header::AUTHORIZATION, "Bearer abc")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
        assert_eq!(
            response.status(),
            StatusCode::INTERNAL_SERVER_ERROR,
            "{uri}"
        );
        assert!(
            !response.headers().contains_key(header::RETRY_AFTER),
            "{uri}"
        );
        assert!(
            to_bytes(response.into_body(), usize::MAX)
                .await
                .expect("body")
                .is_empty(),
            "{uri}"
        );
        assert_eq!(ratio_calls.load(Ordering::SeqCst), 0, "{uri}");
        assert_eq!(token_auth_calls.load(Ordering::SeqCst), 0, "{uri}");
    }
}

#[tokio::test]
async fn critical_limiter_allows_both_routes_to_reach_their_handlers() {
    for uri in ["/api/ratio_config", "/api/usage/token/"] {
        let ratio_calls = Arc::new(AtomicUsize::new(0));
        let token_auth_calls = Arc::new(AtomicUsize::new(0));
        let token_usage_owners = Arc::new(Mutex::new(Vec::new()));
        let mut inner = public_catalog_store();
        inner.token_records.insert(
            "abc".to_owned(),
            PublicCatalogToken {
                user_id: 42,
                ..PublicCatalogToken::default()
            },
        );
        let store = CountingStore {
            inner,
            ratio_calls: Arc::clone(&ratio_calls),
            token_auth_calls: Arc::clone(&token_auth_calls),
            token_usage_owners: Arc::clone(&token_usage_owners),
        };
        let response = limited_app(
            store,
            FixedLimiter {
                outcome: Ok(CriticalRateLimitOutcome::Allowed),
                calls: Arc::new(AtomicUsize::new(0)),
            },
        )
        .oneshot(
            Request::get(uri)
                .header(header::AUTHORIZATION, "Bearer abc")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
        assert_eq!(response.status(), StatusCode::OK, "{uri}");
        assert_eq!(
            ratio_calls.load(Ordering::SeqCst),
            usize::from(uri == "/api/ratio_config"),
            "{uri}"
        );
        assert_eq!(
            token_auth_calls.load(Ordering::SeqCst),
            usize::from(uri == "/api/usage/token/"),
            "{uri}"
        );
        assert_eq!(
            *token_usage_owners.lock().expect("owner recorder"),
            if uri == "/api/usage/token/" {
                vec![42]
            } else {
                Vec::new()
            },
            "{uri}"
        );
    }
}

#[tokio::test]
async fn token_usage_route_uses_go_missing_authorization_wording() {
    let (status, content_type, body) = missing_json(
        public_catalog_app(public_catalog_store(), None),
        Request::get("/api/usage/token/")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::UNAUTHORIZED);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(
        body,
        serde_json::json!({"success": false, "message": "Token not provided"})
    );
}

#[tokio::test]
async fn token_usage_route_rejects_malformed_bearer_headers() {
    let (status, content_type, body) = missing_json(
        public_catalog_app(public_catalog_store(), None),
        Request::get("/api/usage/token/")
            .header("authorization", "Basic abc")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::UNAUTHORIZED);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(
        body,
        serde_json::json!({"success": false, "message": "Invalid token"})
    );
}

#[tokio::test]
async fn token_usage_route_accepts_the_sk_prefix() {
    let (status, content_type, body) = missing_json(
        public_catalog_app(public_catalog_store(), None),
        Request::get("/api/usage/token/")
            .header("authorization", "Bearer sk-abc")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(body["code"], true);
    assert_eq!(body["data"]["name"], "fixture");
}

#[tokio::test]
async fn token_usage_route_accepts_lowercase_bearer_and_hyphenated_keys() {
    let mut store = public_catalog_store();
    store
        .token_records
        .insert("hyphen".to_owned(), PublicCatalogToken::default());
    store.usages.insert(
        "hyphen-tail".to_owned(),
        store.usages.get("abc").expect("fixture token").clone(),
    );
    let (status, content_type, body) = missing_json(
        public_catalog_app(store, None),
        Request::get("/api/usage/token/")
            .header("authorization", "bearer hyphen-tail")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(body["code"], true);
}

#[tokio::test]
async fn token_usage_route_keeps_expired_and_exhausted_enabled_tokens_readable() {
    for status_code in [3, 4] {
        let mut store = public_catalog_store();
        let key = format!("state-{status_code}");
        store.token_records.insert(
            "state".to_owned(),
            PublicCatalogToken {
                status: status_code,
                user_status: 1,
                ..PublicCatalogToken::default()
            },
        );
        store.usages.insert(
            key.clone(),
            store.usages.get("abc").expect("fixture token").clone(),
        );
        let (response_status, _, body) = missing_json(
            public_catalog_app(store, None),
            Request::get("/api/usage/token/")
                .header("authorization", format!("Bearer {key}"))
                .body(Body::empty())
                .expect("request"),
        )
        .await;
        assert_eq!(response_status, StatusCode::OK);
        assert_eq!(body["code"], true);
    }
}

#[tokio::test]
async fn token_usage_route_rejects_disabled_and_banned_tokens_with_legacy_errors() {
    let mut disabled = public_catalog_store();
    disabled.token_records.insert(
        "disabled".to_owned(),
        PublicCatalogToken {
            status: 2,
            user_status: 1,
            ..PublicCatalogToken::default()
        },
    );
    let (status, content_type, body) = missing_json(
        public_catalog_app(disabled, None),
        Request::get("/api/usage/token/")
            .header("authorization", "Bearer disabled")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(status, StatusCode::UNAUTHORIZED);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(body["message"], "This token status is unavailable");

    let mut banned = public_catalog_store();
    banned.token_records.insert(
        "banned".to_owned(),
        PublicCatalogToken {
            status: 1,
            user_status: 2,
            ..PublicCatalogToken::default()
        },
    );
    let (status, _, body) = missing_json(
        public_catalog_app(banned, None),
        Request::get("/api/usage/token/")
            .header("authorization", "Bearer banned")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(status, StatusCode::FORBIDDEN);
    assert_eq!(body["message"], "User has been banned");
}

#[tokio::test]
async fn token_usage_route_distinguishes_middleware_and_handler_database_errors() {
    let mut middleware_error = public_catalog_store();
    middleware_error.token_auth_error =
        Some(PublicCatalogStoreError::new("middleware lookup failed"));
    let (status, content_type, body) = missing_json(
        public_catalog_app(middleware_error, None),
        Request::get("/api/usage/token/")
            .header("authorization", "Bearer abc")
            .header("accept-language", "zh-TW")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(status, StatusCode::INTERNAL_SERVER_ERROR);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(body["message"], "資料庫出錯，請聯繫管理員");

    let mut handler_error = public_catalog_store();
    handler_error.token_usage_error = Some(PublicCatalogStoreError::new("handler lookup failed"));
    handler_error.token_records.insert(
        "abc".to_owned(),
        PublicCatalogToken {
            user_id: 42,
            saved_language: Some("zh-TW".to_owned()),
            ..PublicCatalogToken::default()
        },
    );
    let (status, content_type, body) = missing_json(
        public_catalog_app(handler_error, None),
        Request::get("/api/usage/token/")
            .header("authorization", "Bearer abc")
            .header("accept-language", "en-US")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(body["message"], "獲取令牌資訊失敗，請稍後重試");
}

#[tokio::test]
async fn token_usage_route_matches_go_whitespace_and_empty_header_parsing() {
    for authorization in ["", " ", "Bearer   abc"] {
        let (status, content_type, body) = missing_json(
            public_catalog_app(public_catalog_store(), None),
            Request::get("/api/usage/token/")
                .header("authorization", authorization)
                .body(Body::empty())
                .expect("request"),
        )
        .await;
        assert_eq!(content_type, "application/json; charset=utf-8");
        if authorization.is_empty() {
            assert_eq!(status, StatusCode::UNAUTHORIZED);
            assert_eq!(body["message"], "Token not provided");
        } else if authorization == "Bearer   abc" {
            assert_eq!(status, StatusCode::UNAUTHORIZED);
            assert_eq!(body["message"], "Invalid Bearer token");
        } else {
            assert_eq!(status, StatusCode::UNAUTHORIZED);
            assert_eq!(body["message"], "Invalid token");
        }
    }
}

#[tokio::test]
async fn token_usage_route_returns_go_failure_shape_for_unknown_tokens() {
    let (status, content_type, body) = missing_json(
        public_catalog_app(public_catalog_store(), None),
        Request::get("/api/usage/token/")
            .header("authorization", "Bearer missing")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::UNAUTHORIZED);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(
        body,
        serde_json::json!({"success": false, "message": "Invalid token"})
    );
}

#[tokio::test]
async fn token_usage_route_preserves_quota_expiry_and_model_fields() {
    let (status, content_type, body) = missing_json(
        public_catalog_app(public_catalog_store(), None),
        Request::get("/api/usage/token/")
            .header("authorization", "Bearer abc")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(content_type, "application/json; charset=utf-8");
    assert_eq!(
        body["data"],
        serde_json::json!({
            "object": "token_usage",
            "name": "fixture",
            "total_granted": 13,
            "total_used": 5,
            "total_available": 8,
            "unlimited_quota": false,
            "model_limits": {"gpt-4o": true},
            "model_limits_enabled": true,
            "expires_at": 0
        })
    );
}

struct NoUptimeCall;

#[async_trait]
impl UptimeKumaClient for NoUptimeCall {
    async fn status_page(&self, _: &str) -> Result<UptimeStatusPage, ControlPublicError> {
        panic!("missing uptime configuration must not call upstream")
    }

    async fn heartbeat_page(&self, _: &str) -> Result<UptimeHeartbeatPage, ControlPublicError> {
        panic!("missing uptime configuration must not call upstream")
    }
}

async fn route_json(
    repository: Arc<dyn ControlPublicRepository>,
    uri: &str,
) -> (StatusCode, serde_json::Value) {
    let response = control_public_router(ControlPublicHttpState::new(
        repository,
        Arc::new(NoUptimeCall),
    ))
    .oneshot(
        Request::builder()
            .uri(uri)
            .body(Body::empty())
            .expect("request"),
    )
    .await
    .expect("response");
    let status = response.status();
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    (
        status,
        serde_json::from_slice(&body).expect("JSON response"),
    )
}

#[tokio::test]
async fn missing_public_options_preserve_empty_legacy_success_values() {
    assert_eq!(
        route_json(Arc::new(MissingOptions), "/api/user-agreement").await,
        (
            StatusCode::OK,
            serde_json::json!({"success":true,"message":"","data":""})
        )
    );
    assert_eq!(
        route_json(Arc::new(MissingOptions), "/api/uptime/status").await,
        (
            StatusCode::OK,
            serde_json::json!({"success":true,"message":"","data":[]})
        )
    );
}

#[tokio::test]
async fn authoritative_option_failure_never_fabricates_public_success() {
    assert_eq!(
        route_json(Arc::new(FailedOptions), "/api/privacy-policy").await,
        (
            StatusCode::INTERNAL_SERVER_ERROR,
            serde_json::json!({
                "success":false,
                "message":"public control-plane data is temporarily unavailable"
            })
        )
    );
}

fn serve_raw_http(
    listener: TcpListener,
    responses: Vec<(&'static str, &'static [u8])>,
) -> tokio::task::JoinHandle<Vec<String>> {
    tokio::spawn(async move {
        let mut requests = Vec::with_capacity(responses.len());
        for _ in 0..responses.len() {
            let (mut socket, _) = listener.accept().await.expect("TCP connection");
            let mut request = vec![0; 4096];
            let size = socket.read(&mut request).await.expect("HTTP request");
            let request = String::from_utf8_lossy(&request[..size]).into_owned();
            let (_, response) = responses
                .iter()
                .find(|(path, _)| request.starts_with(&format!("GET {path} HTTP/1.1")))
                .expect("expected Uptime Kuma path");
            socket.write_all(response).await.expect("HTTP response");
            requests.push(request);
        }
        requests
    })
}

#[tokio::test]
async fn uptime_http_adapter_should_decode_the_legacy_status_page_over_tcp() {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("TCP listener");
    let address = listener.local_addr().expect("listener address");
    let upstream = serve_raw_http(
        listener,
        vec![
            ("/api/status-page/primary", b"HTTP/1.1 200 OK\r\ncontent-type: application/json\r\nconnection: close\r\ncontent-length: 75\r\n\r\n{\"publicGroupList\":[{\"name\":\"Core\",\"monitorList\":[{\"id\":7,\"name\":\"API\"}]}]}"),
        ],
    );
    let adapter = ReqwestUptimeKumaClient::new().expect("bounded Uptime Kuma adapter");

    let page = adapter
        .status_page(&format!("http://{address}/api/status-page/primary"))
        .await
        .expect("200 status page");

    assert_eq!(page.public_group_list[0].monitor_list[0].name, "API");
    assert!(
        upstream.await.expect("upstream task")[0]
            .starts_with("GET /api/status-page/primary HTTP/1.1")
    );
}

#[tokio::test]
async fn uptime_http_adapter_should_reject_a_non_ok_upstream_response() {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("TCP listener");
    let address = listener.local_addr().expect("listener address");
    let upstream = serve_raw_http(
        listener,
        vec![(
            "/api/status-page/heartbeat/primary",
            b"HTTP/1.1 503 Service Unavailable\r\nconnection: close\r\ncontent-length: 0\r\n\r\n",
        )],
    );
    let adapter = ReqwestUptimeKumaClient::new().expect("bounded Uptime Kuma adapter");

    assert!(
        adapter
            .heartbeat_page(&format!(
                "http://{address}/api/status-page/heartbeat/primary"
            ))
            .await
            .is_err()
    );
    assert!(
        upstream.await.expect("upstream task")[0]
            .starts_with("GET /api/status-page/heartbeat/primary HTTP/1.1")
    );
}

#[tokio::test]
#[ignore = "requires an isolated PostgreSQL 18 database; set LMM_CONTROL_PUBLIC_TEST_DATABASE_URL"]
async fn control_public_routes_should_read_pg_and_call_uptime_over_real_tcp() {
    let database_url = env::var("LMM_CONTROL_PUBLIC_TEST_DATABASE_URL").expect(
        "LMM_CONTROL_PUBLIC_TEST_DATABASE_URL is required for the isolated PostgreSQL 18 harness",
    );
    let pool = PgPoolOptions::new()
        .max_connections(3)
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL must be reachable");
    sqlx::query("CREATE TABLE IF NOT EXISTS options (key TEXT PRIMARY KEY, value TEXT)")
        .execute(&pool)
        .await
        .expect("options fixture table");

    let upstream_listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("Uptime Kuma fixture listener");
    let upstream_address = upstream_listener.local_addr().expect("upstream address");
    let upstream = serve_raw_http(
        upstream_listener,
        vec![
            ("/api/status-page/primary", b"HTTP/1.1 200 OK\r\ncontent-type: application/json\r\nconnection: close\r\ncontent-length: 75\r\n\r\n{\"publicGroupList\":[{\"name\":\"Core\",\"monitorList\":[{\"id\":7,\"name\":\"API\"}]}]}"),
            ("/api/status-page/heartbeat/primary", b"HTTP/1.1 200 OK\r\ncontent-type: application/json\r\nconnection: close\r\ncontent-length: 65\r\n\r\n{\"heartbeatList\":{\"7\":[{\"status\":1}]},\"uptimeList\":{\"7_24\":99.9}}"),
        ],
    );
    let groups = format!(
        r#"[{{"url":"http://{upstream_address}","slug":"primary","categoryName":"Primary"}}]"#
    );
    for (key, value) in [
        ("legal.user_agreement", "agreement-fixture"),
        ("legal.privacy_policy", "privacy-fixture"),
        ("console_setting.uptime_kuma_groups", groups.as_str()),
    ] {
        sqlx::query("INSERT INTO options (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value")
            .bind(key)
            .bind(value)
            .execute(&pool)
            .await
            .expect("PostgreSQL option fixture");
    }

    let app = control_public_router(ControlPublicHttpState::new(
        Arc::new(PgControlPublicRepository::new(pool)),
        Arc::new(ReqwestUptimeKumaClient::new().expect("Uptime Kuma adapter")),
    ));
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("Rust route listener");
    let address = listener.local_addr().expect("Rust route address");
    let server = tokio::spawn(async move {
        axum::serve(listener, app).await.expect("Rust route server");
    });
    let client = reqwest::Client::new();

    let agreement = client
        .get(format!("http://{address}/api/user-agreement"))
        .send()
        .await
        .expect("agreement response");
    assert_eq!(agreement.status(), StatusCode::OK);
    assert_eq!(
        agreement
            .json::<serde_json::Value>()
            .await
            .expect("agreement JSON"),
        serde_json::json!({"success": true, "message": "", "data": "agreement-fixture"})
    );

    let privacy = client
        .get(format!("http://{address}/api/privacy-policy"))
        .send()
        .await
        .expect("privacy response");
    assert_eq!(privacy.status(), StatusCode::OK);
    assert_eq!(
        privacy
            .json::<serde_json::Value>()
            .await
            .expect("privacy JSON"),
        serde_json::json!({"success": true, "message": "", "data": "privacy-fixture"})
    );

    let uptime = client
        .get(format!("http://{address}/api/uptime/status"))
        .send()
        .await
        .expect("uptime response");
    assert_eq!(uptime.status(), StatusCode::OK);
    assert_eq!(
        uptime
            .json::<serde_json::Value>()
            .await
            .expect("uptime JSON"),
        serde_json::json!({
            "success": true,
            "message": "",
            "data": [{"categoryName": "Primary", "monitors": [{"name": "API", "uptime": 99.9, "status": 1, "group": "Core"}]}]
        })
    );
    assert_eq!(upstream.await.expect("Uptime Kuma task").len(), 2);
    server.abort();
}
