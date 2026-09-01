use std::{
    collections::HashSet,
    sync::{
        Arc, Mutex,
        atomic::{AtomicUsize, Ordering},
    },
};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{HeaderMap, Method, Request, StatusCode, header},
};
use lmm_api_rs::auth::{
    AuthBundle, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth, DashboardUser,
    LoginOutcome, LoginRequest, LogoutRequest, LogoutResult, RequestMetadata,
    TwoFactorLoginRequest,
};
use lmm_api_rs::routes::{
    channel_advanced::{
        ChannelAdvancedAuthorizer, ChannelAdvancedCall, ChannelAdvancedError,
        ChannelAdvancedHttpState, ChannelAdvancedPermission, ChannelAdvancedProvider,
        DashboardChannelAdvancedAuthorizer, channel_advanced_router,
    },
    identity_security::{
        IdentitySecurityState, MemorySecurityProvider, SecurityActor, SecurityAuthorizer,
        SecurityError, router as identity_security_router,
    },
};
use secrecy::{ExposeSecret, SecretString};
use serde_json::{Value, json};
use tower::ServiceExt;

#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
struct LegacyRoute {
    method: &'static str,
    path: &'static str,
    handler: &'static str,
    auth: AuthScope,
}

#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
enum AuthScope {
    Anonymous,
    User,
    Admin,
    Root,
    TokenAuth,
}

const CHANNEL_ADVANCED_ROUTES: &[LegacyRoute] = &[
    route(
        "POST",
        "/api/channel/:id/codex/refresh",
        "RefreshCodexChannelCredential",
        AuthScope::Admin,
    ),
    route(
        "GET",
        "/api/channel/:id/codex/usage",
        "GetCodexChannelUsage",
        AuthScope::Admin,
    ),
    route(
        "POST",
        "/api/channel/:id/codex/usage/reset",
        "ResetCodexChannelUsage",
        AuthScope::Admin,
    ),
    route(
        "GET",
        "/api/channel/:id/codex/usage/reset-credits",
        "GetCodexChannelRateLimitResetCredits",
        AuthScope::Admin,
    ),
    route(
        "POST",
        "/api/channel/:id/key",
        "GetChannelKey",
        AuthScope::Root,
    ),
    route(
        "POST",
        "/api/channel/fetch_models",
        "FetchModels",
        AuthScope::Admin,
    ),
    route(
        "GET",
        "/api/channel/fetch_models/:id",
        "FetchUpstreamModels",
        AuthScope::Admin,
    ),
    route(
        "DELETE",
        "/api/channel/ollama/delete",
        "OllamaDeleteModel",
        AuthScope::Admin,
    ),
    route(
        "POST",
        "/api/channel/ollama/pull",
        "OllamaPullModel",
        AuthScope::Admin,
    ),
    route(
        "POST",
        "/api/channel/ollama/pull/stream",
        "OllamaPullModelStream",
        AuthScope::Admin,
    ),
    route(
        "GET",
        "/api/channel/ollama/version/:id",
        "OllamaVersion",
        AuthScope::Admin,
    ),
    route(
        "GET",
        "/api/channel/test",
        "TestAllChannels",
        AuthScope::Admin,
    ),
    route(
        "GET",
        "/api/channel/test/:id",
        "TestChannel",
        AuthScope::Admin,
    ),
    route(
        "GET",
        "/api/channel/update_balance",
        "UpdateAllChannelsBalance",
        AuthScope::Admin,
    ),
    route(
        "GET",
        "/api/channel/update_balance/:id",
        "UpdateChannelBalance",
        AuthScope::Admin,
    ),
    route(
        "POST",
        "/api/channel/upstream_updates/apply",
        "ApplyChannelUpstreamModelUpdates",
        AuthScope::Admin,
    ),
    route(
        "POST",
        "/api/channel/upstream_updates/apply_all",
        "ApplyAllChannelUpstreamModelUpdates",
        AuthScope::Admin,
    ),
    route(
        "POST",
        "/api/channel/upstream_updates/detect",
        "DetectChannelUpstreamModelUpdates",
        AuthScope::Admin,
    ),
    route(
        "POST",
        "/api/channel/upstream_updates/detect_all",
        "DetectAllChannelUpstreamModelUpdates",
        AuthScope::Admin,
    ),
];

const IDENTITY_SECURITY_ROUTES: &[LegacyRoute] = &[
    route(
        "GET",
        "/api/authz/catalog",
        "GetPermissionCatalog",
        AuthScope::Admin,
    ),
    route(
        "GET",
        "/api/verification",
        "SendEmailVerification",
        AuthScope::Anonymous,
    ),
    route(
        "GET",
        "/api/reset_password",
        "SendPasswordResetEmail",
        AuthScope::Anonymous,
    ),
    route("POST", "/api/verify", "UniversalVerify", AuthScope::User),
    route(
        "DELETE",
        "/api/user/:id/bindings/:binding_type",
        "AdminClearUserBinding",
        AuthScope::Admin,
    ),
    route(
        "DELETE",
        "/api/user/:id/reset_passkey",
        "AdminResetPasskey",
        AuthScope::Admin,
    ),
    route(
        "POST",
        "/api/user/register",
        "Register",
        AuthScope::Anonymous,
    ),
    route(
        "POST",
        "/api/user/reset",
        "ResetPassword",
        AuthScope::Anonymous,
    ),
    route(
        "POST",
        "/api/user/login/2fa",
        "Verify2FALogin",
        AuthScope::Anonymous,
    ),
    route(
        "POST",
        "/api/user/passkey/login/begin",
        "PasskeyLoginBegin",
        AuthScope::Anonymous,
    ),
    route(
        "POST",
        "/api/user/passkey/login/finish",
        "PasskeyLoginFinish",
        AuthScope::Anonymous,
    ),
    route("GET", "/api/user/passkey", "PasskeyStatus", AuthScope::User),
    route(
        "POST",
        "/api/user/passkey/register/begin",
        "PasskeyRegisterBegin",
        AuthScope::User,
    ),
    route(
        "POST",
        "/api/user/passkey/register/finish",
        "PasskeyRegisterFinish",
        AuthScope::User,
    ),
    route(
        "POST",
        "/api/user/passkey/verify/begin",
        "PasskeyVerifyBegin",
        AuthScope::User,
    ),
    route(
        "POST",
        "/api/user/passkey/verify/finish",
        "PasskeyVerifyFinish",
        AuthScope::User,
    ),
    route(
        "DELETE",
        "/api/user/passkey",
        "PasskeyDelete",
        AuthScope::User,
    ),
    route(
        "GET",
        "/api/user/sessions",
        "GetLoginSessions",
        AuthScope::User,
    ),
    route(
        "DELETE",
        "/api/user/sessions/:sid",
        "DeleteLoginSession",
        AuthScope::User,
    ),
    route(
        "POST",
        "/api/user/sessions/revoke-others",
        "RevokeOtherLoginSessions",
        AuthScope::User,
    ),
];

const fn route(
    method: &'static str,
    path: &'static str,
    handler: &'static str,
    auth: AuthScope,
) -> LegacyRoute {
    LegacyRoute {
        method,
        path,
        handler,
        auth,
    }
}

fn frozen_routes() -> HashSet<(&'static str, &'static str, &'static str)> {
    include_str!("fixtures/routes/legacy-go-routes.tsv")
        .lines()
        .map(|line| {
            let mut columns = line.split('\t');
            let method = columns.next().expect("legacy route method");
            let path = columns.next().expect("legacy route path");
            let handler = columns
                .next()
                .expect("legacy route handler")
                .rsplit('.')
                .next()
                .expect("legacy handler symbol");
            assert!(columns.next().is_none(), "unexpected legacy route column");
            (method, path, handler)
        })
        .collect()
}

fn concrete_path(path: &str) -> String {
    path.replace(":binding_type", "email")
        .replace(":sid", "session-42")
        .replace(":id", "42")
}

fn request_for(route: LegacyRoute) -> Request<Body> {
    let uri = match route.path {
        "/api/verification" | "/api/reset_password" => {
            format!("{}?email=ada%40example.test", route.path)
        }
        _ => concrete_path(route.path),
    };
    let mut builder = Request::builder()
        .method(route.method)
        .uri(uri)
        .header("x-user-id", "7")
        .header("x-user-role", "100")
        .header(header::AUTHORIZATION, "Bearer client-forged-token");
    let body = if route.method == "POST" {
        builder = builder.header(header::CONTENT_TYPE, "application/json");
        Body::from("{")
    } else {
        Body::empty()
    };
    builder.body(body).expect("Wave 3 request")
}

async fn json_body(response: axum::response::Response) -> Value {
    serde_json::from_slice(
        &to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body"),
    )
    .expect("JSON response")
}

struct DenyChannel;

#[async_trait]
impl ChannelAdvancedAuthorizer for DenyChannel {
    async fn authorize(
        &self,
        _: &HeaderMap,
        _: ChannelAdvancedPermission,
    ) -> Result<(), ChannelAdvancedError> {
        Err(ChannelAdvancedError::Unauthorized)
    }
}

#[derive(Default)]
struct RecordingChannelAuthorizer {
    permissions: Mutex<Vec<ChannelAdvancedPermission>>,
}

#[async_trait]
impl ChannelAdvancedAuthorizer for RecordingChannelAuthorizer {
    async fn authorize(
        &self,
        _: &HeaderMap,
        permission: ChannelAdvancedPermission,
    ) -> Result<(), ChannelAdvancedError> {
        self.permissions
            .lock()
            .expect("permission recorder")
            .push(permission);
        Err(if permission == ChannelAdvancedPermission::Root {
            ChannelAdvancedError::InsufficientPrivilege
        } else {
            ChannelAdvancedError::PermissionDenied
        })
    }
}

fn expected_channel_permission(route: LegacyRoute) -> ChannelAdvancedPermission {
    match (route.method, route.path) {
        ("GET", "/api/channel/:id/codex/usage")
        | ("GET", "/api/channel/:id/codex/usage/reset-credits") => ChannelAdvancedPermission::Read,
        ("POST", "/api/channel/:id/codex/usage/reset")
        | ("GET", "/api/channel/fetch_models/:id")
        | ("GET", "/api/channel/test")
        | ("GET", "/api/channel/test/:id")
        | ("GET", "/api/channel/update_balance")
        | ("GET", "/api/channel/update_balance/:id")
        | ("POST", "/api/channel/upstream_updates/detect")
        | ("POST", "/api/channel/upstream_updates/detect_all") => {
            ChannelAdvancedPermission::Operate
        }
        ("POST", "/api/channel/:id/codex/refresh")
        | ("POST", "/api/channel/fetch_models")
        | ("DELETE", "/api/channel/ollama/delete")
        | ("POST", "/api/channel/ollama/pull")
        | ("POST", "/api/channel/ollama/pull/stream")
        | ("GET", "/api/channel/ollama/version/:id") => ChannelAdvancedPermission::SensitiveWrite,
        ("POST", "/api/channel/upstream_updates/apply")
        | ("POST", "/api/channel/upstream_updates/apply_all") => ChannelAdvancedPermission::Write,
        ("POST", "/api/channel/:id/key") => ChannelAdvancedPermission::Root,
        _ => panic!(
            "missing frozen permission for {} {}",
            route.method, route.path
        ),
    }
}

#[derive(Default)]
struct CountingChannelProvider {
    calls: AtomicUsize,
}

#[async_trait]
impl ChannelAdvancedProvider for CountingChannelProvider {
    async fn execute(&self, _: ChannelAdvancedCall) -> Result<Value, ChannelAdvancedError> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        Ok(json!({}))
    }
}

struct StaticDashboardAuth {
    role: i64,
    status: i64,
    permissions: Value,
}

#[async_trait]
impl DashboardAuth for StaticDashboardAuth {
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
            username: "operator".to_owned(),
            display_name: "Operator".to_owned(),
            role: self.role,
            status: self.status,
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
            sidebar_modules: json!({}),
            permissions: self.permissions.clone(),
        })
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

struct DenyIdentity;

#[async_trait]
impl SecurityAuthorizer for DenyIdentity {
    async fn user(&self, _: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        Err(SecurityError::Unauthorized)
    }

    async fn admin(&self, _: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        Err(SecurityError::Unauthorized)
    }
}

struct NonAdminIdentity;

#[async_trait]
impl SecurityAuthorizer for NonAdminIdentity {
    async fn user(&self, _: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        Ok(SecurityActor {
            user_id: 7,
            role: 1,
            session_id: Some("session-7".to_owned()),
        })
    }

    async fn admin(&self, _: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        self.user(&HeaderMap::new()).await
    }
}

struct InvalidUserIdentity;

#[async_trait]
impl SecurityAuthorizer for InvalidUserIdentity {
    async fn user(&self, _: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        Ok(SecurityActor {
            user_id: 0,
            role: 100,
            session_id: Some("forged-session".to_owned()),
        })
    }

    async fn admin(&self, _: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        self.user(&HeaderMap::new()).await
    }
}

struct GuestIdentity;

#[async_trait]
impl SecurityAuthorizer for GuestIdentity {
    async fn user(&self, _: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        Ok(SecurityActor {
            user_id: 8,
            role: 0,
            session_id: Some("guest-session".to_owned()),
        })
    }

    async fn admin(&self, _: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        self.user(&HeaderMap::new()).await
    }
}

struct AllowIdentity;

#[async_trait]
impl SecurityAuthorizer for AllowIdentity {
    async fn user(&self, _: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        Ok(SecurityActor {
            user_id: 7,
            role: 1,
            session_id: Some("session-7".to_owned()),
        })
    }

    async fn admin(&self, _: &HeaderMap) -> Result<SecurityActor, SecurityError> {
        Ok(SecurityActor {
            user_id: 9,
            role: 100,
            session_id: Some("root-session".to_owned()),
        })
    }
}

#[test]
fn wave3_route_fixture_should_match_the_frozen_go_manifest_without_duplicates() {
    let expected = CHANNEL_ADVANCED_ROUTES
        .iter()
        .chain(IDENTITY_SECURITY_ROUTES)
        .copied()
        .collect::<HashSet<_>>();
    assert_eq!(expected.len(), 39, "Wave 3 contains a duplicate route");
    assert_eq!(CHANNEL_ADVANCED_ROUTES.len(), 19);
    assert_eq!(IDENTITY_SECURITY_ROUTES.len(), 20);

    let frozen = frozen_routes();
    let missing = expected
        .iter()
        .filter(|route| !frozen.contains(&(route.method, route.path, route.handler)))
        .copied()
        .collect::<Vec<_>>();
    assert!(
        missing.is_empty(),
        "Wave 3 fixture drifted from legacy-go-routes.tsv: {missing:?}"
    );
}

#[test]
fn wave3_auth_fixture_should_preserve_the_frozen_router_tiers() {
    assert_eq!(
        CHANNEL_ADVANCED_ROUTES
            .iter()
            .filter(|route| route.auth == AuthScope::Root)
            .map(|route| (route.method, route.path))
            .collect::<Vec<_>>(),
        [("POST", "/api/channel/:id/key")]
    );
    assert!(
        CHANNEL_ADVANCED_ROUTES
            .iter()
            .filter(|route| route.auth != AuthScope::Root)
            .all(|route| route.auth == AuthScope::Admin)
    );
    assert_eq!(
        IDENTITY_SECURITY_ROUTES
            .iter()
            .filter(|route| route.auth == AuthScope::Anonymous)
            .count(),
        7
    );
    assert_eq!(
        IDENTITY_SECURITY_ROUTES
            .iter()
            .filter(|route| route.auth == AuthScope::User)
            .count(),
        10
    );
    assert_eq!(
        IDENTITY_SECURITY_ROUTES
            .iter()
            .filter(|route| route.auth == AuthScope::Admin)
            .count(),
        3
    );
    assert!(
        CHANNEL_ADVANCED_ROUTES
            .iter()
            .chain(IDENTITY_SECURITY_ROUTES)
            .all(|route| route.auth != AuthScope::TokenAuth),
        "Wave 3 has no legacy TokenAuth route"
    );
}

#[tokio::test]
async fn channel_advanced_should_authorize_before_path_body_or_upstream_access() {
    let provider = Arc::new(CountingChannelProvider::default());
    let app = channel_advanced_router(ChannelAdvancedHttpState::new(
        Arc::new(DenyChannel),
        provider.clone(),
    ));

    for route in CHANNEL_ADVANCED_ROUTES {
        let response = app
            .clone()
            .oneshot(request_for(*route))
            .await
            .expect("advanced channel response");
        assert_eq!(
            response.status(),
            StatusCode::UNAUTHORIZED,
            "{} {} did not authorize before parsing or upstream access",
            route.method,
            route.path
        );
        assert_eq!(
            json_body(response).await,
            json!({
                "success": false,
                "message": "Unauthorized, invalid access token",
                "code": "AUTH_UNAUTHORIZED"
            }),
            "{} {} changed the frozen AdminAuth envelope",
            route.method,
            route.path
        );
    }

    assert_eq!(
        provider.calls.load(Ordering::SeqCst),
        0,
        "an unauthorized stream or upstream operation reached its provider"
    );
}

#[tokio::test]
async fn channel_advanced_should_preserve_permissions_root_tier_and_forbidden_envelope() {
    for route in CHANNEL_ADVANCED_ROUTES {
        let authorizer = Arc::new(RecordingChannelAuthorizer::default());
        let provider = Arc::new(CountingChannelProvider::default());
        let app = channel_advanced_router(ChannelAdvancedHttpState::new(
            authorizer.clone(),
            provider.clone(),
        ));
        let response = app
            .oneshot(request_for(*route))
            .await
            .expect("permission response");

        assert_eq!(
            response.status(),
            StatusCode::FORBIDDEN,
            "{} {} bypassed its frozen permission",
            route.method,
            route.path
        );
        let expected_body = if route.auth == AuthScope::Root {
            json!({
                "success": false,
                "message": "Unauthorized, insufficient privileges",
                "code": "AUTH_INSUFFICIENT_PRIVILEGE"
            })
        } else {
            json!({
                "success": false,
                "message": "Unauthorized, insufficient privileges"
            })
        };
        assert_eq!(json_body(response).await, expected_body);
        assert_eq!(
            *authorizer.permissions.lock().expect("permission recorder"),
            [expected_channel_permission(*route)],
            "{} {} used the wrong frozen channel permission",
            route.method,
            route.path
        );
        assert_eq!(
            provider.calls.load(Ordering::SeqCst),
            0,
            "{} {} reached the provider after permission denial",
            route.method,
            route.path
        );
    }
}

#[tokio::test]
async fn channel_dashboard_adapter_should_use_only_server_role_and_permissions() {
    let mut forged_headers = HeaderMap::new();
    forged_headers.insert(
        header::AUTHORIZATION,
        "Bearer signed-dashboard-session"
            .parse()
            .expect("authorization header"),
    );
    forged_headers.insert("x-user-role", "100".parse().expect("forged role header"));
    forged_headers.insert(
        "x-channel-permission",
        "sensitive_write".parse().expect("forged permission header"),
    );

    let forged_user = DashboardChannelAdvancedAuthorizer::new(Arc::new(StaticDashboardAuth {
        role: 1,
        status: 1,
        permissions: json!({
            "admin_permissions": {
                "channel": {
                    "read": true,
                    "write": true,
                    "sensitive_write": true,
                    "operate": true
                }
            }
        }),
    }));
    assert_eq!(
        forged_user
            .authorize(&forged_headers, ChannelAdvancedPermission::Read)
            .await,
        Err(ChannelAdvancedError::ConsoleNotFound),
        "an unactivated principal must be concealed before the frozen AdminAuth tier"
    );

    let unprivileged_admin =
        DashboardChannelAdvancedAuthorizer::new(Arc::new(StaticDashboardAuth {
            role: 10,
            status: 1,
            permissions: json!({}),
        }));
    assert_eq!(
        unprivileged_admin
            .authorize(&forged_headers, ChannelAdvancedPermission::Operate)
            .await,
        Err(ChannelAdvancedError::PermissionDenied),
        "AdminAuth must not bypass the frozen RequirePermission middleware"
    );
    assert_eq!(
        unprivileged_admin
            .authorize(&forged_headers, ChannelAdvancedPermission::Root)
            .await,
        Err(ChannelAdvancedError::InsufficientPrivilege),
        "an ordinary administrator reached the root-only channel key operation"
    );

    let read_only_admin = DashboardChannelAdvancedAuthorizer::new(Arc::new(StaticDashboardAuth {
        role: 10,
        status: 1,
        permissions: json!({"admin_permissions":{"channel":{"read":true}}}),
    }));
    assert_eq!(
        read_only_admin
            .authorize(&forged_headers, ChannelAdvancedPermission::Read)
            .await,
        Ok(())
    );
    assert_eq!(
        read_only_admin
            .authorize(&forged_headers, ChannelAdvancedPermission::SensitiveWrite)
            .await,
        Err(ChannelAdvancedError::PermissionDenied)
    );

    let root = DashboardChannelAdvancedAuthorizer::new(Arc::new(StaticDashboardAuth {
        role: 100,
        status: 1,
        permissions: json!({}),
    }));
    for permission in [
        ChannelAdvancedPermission::Read,
        ChannelAdvancedPermission::Write,
        ChannelAdvancedPermission::SensitiveWrite,
        ChannelAdvancedPermission::Operate,
        ChannelAdvancedPermission::Root,
    ] {
        assert_eq!(root.authorize(&forged_headers, permission).await, Ok(()));
    }
}

#[tokio::test]
async fn identity_security_should_authorize_before_path_body_or_provider_access() {
    let provider = Arc::new(MemorySecurityProvider::default());
    let app = identity_security_router(IdentitySecurityState::new(
        provider.clone(),
        Arc::new(DenyIdentity),
    ));

    for route in IDENTITY_SECURITY_ROUTES
        .iter()
        .filter(|route| route.auth != AuthScope::Anonymous)
    {
        let response = app
            .clone()
            .oneshot(request_for(*route))
            .await
            .expect("identity-security response");
        assert_eq!(
            response.status(),
            StatusCode::UNAUTHORIZED,
            "{} {} parsed input or invoked its provider before authentication",
            route.method,
            route.path
        );
        assert_eq!(
            json_body(response).await,
            json!({
                "success": false,
                "message": "Unauthorized, invalid access token",
                "code": "AUTH_UNAUTHORIZED"
            }),
            "{} {} changed the frozen dashboard auth envelope",
            route.method,
            route.path
        );
    }

    assert!(
        provider.calls().expect("provider calls").is_empty(),
        "an unauthorized identity request reached the security provider"
    );
}

#[tokio::test]
async fn identity_security_should_reject_invalid_or_non_admin_principals_before_provider_access() {
    let invalid_provider = Arc::new(MemorySecurityProvider::default());
    let invalid_user_app = identity_security_router(IdentitySecurityState::new(
        invalid_provider.clone(),
        Arc::new(InvalidUserIdentity),
    ));
    let invalid_response = invalid_user_app
        .oneshot(
            Request::get("/api/user/passkey")
                .header("x-user-id", "7")
                .header("x-user-role", "100")
                .body(Body::empty())
                .expect("invalid identity request"),
        )
        .await
        .expect("invalid identity response");
    assert_eq!(invalid_response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(
        json_body(invalid_response).await["code"],
        "AUTH_UNAUTHORIZED"
    );
    assert!(invalid_provider.calls().expect("provider calls").is_empty());

    let guest_provider = Arc::new(MemorySecurityProvider::default());
    let guest_app = identity_security_router(IdentitySecurityState::new(
        guest_provider.clone(),
        Arc::new(GuestIdentity),
    ));
    let guest_response = guest_app
        .oneshot(
            Request::get("/api/user/passkey")
                .body(Body::empty())
                .expect("guest request"),
        )
        .await
        .expect("guest response");
    assert_eq!(guest_response.status(), StatusCode::FORBIDDEN);
    assert_eq!(
        json_body(guest_response).await,
        json!({
            "success": false,
            "message": "Unauthorized, insufficient privileges",
            "code": "AUTH_INSUFFICIENT_PRIVILEGE"
        })
    );
    assert!(guest_provider.calls().expect("provider calls").is_empty());

    let non_admin_provider = Arc::new(MemorySecurityProvider::default());
    let non_admin_app = identity_security_router(IdentitySecurityState::new(
        non_admin_provider.clone(),
        Arc::new(NonAdminIdentity),
    ));
    for route in IDENTITY_SECURITY_ROUTES
        .iter()
        .filter(|route| route.auth == AuthScope::Admin)
    {
        let response = non_admin_app
            .clone()
            .oneshot(request_for(*route))
            .await
            .expect("non-admin response");
        assert_eq!(response.status(), StatusCode::FORBIDDEN);
        assert_eq!(
            json_body(response).await,
            json!({
                "success": false,
                "message": "Unauthorized, insufficient privileges",
                "code": "AUTH_INSUFFICIENT_PRIVILEGE"
            })
        );
    }
    assert!(
        non_admin_provider
            .calls()
            .expect("provider calls")
            .is_empty()
    );
}

#[tokio::test]
async fn identity_security_default_provider_should_never_fabricate_mail_or_webauthn_success() {
    let provider = Arc::new(MemorySecurityProvider::default());
    let app = identity_security_router(IdentitySecurityState::with_rejecting_authorizer(
        provider.clone(),
    ));

    for route in IDENTITY_SECURITY_ROUTES
        .iter()
        .filter(|route| route.auth == AuthScope::Anonymous)
    {
        let uri = match route.path {
            "/api/verification" | "/api/reset_password" => {
                format!("{}?email=ada%40example.test", route.path)
            }
            _ => concrete_path(route.path),
        };
        let mut builder = Request::builder().method(route.method).uri(uri);
        let body = if route.method == "POST" {
            builder = builder.header(header::CONTENT_TYPE, "application/json");
            match route.path {
                "/api/user/login/2fa" => Body::from(r#"{"code":"123456"}"#),
                "/api/user/reset" => {
                    Body::from(r#"{"email":"ada@example.test","token":"reset-token"}"#)
                }
                _ => Body::from("{}"),
            }
        } else {
            Body::empty()
        };
        let response = app
            .clone()
            .oneshot(builder.body(body).expect("anonymous security request"))
            .await
            .expect("anonymous security response");
        assert_eq!(
            response.status(),
            StatusCode::SERVICE_UNAVAILABLE,
            "{} {} fabricated a security success without a real provider",
            route.method,
            route.path
        );
        assert_eq!(json_body(response).await["success"], false);
    }

    assert_eq!(
        provider.calls().expect("provider calls").len(),
        6,
        "every configured anonymous mail/WebAuthn operation must cross the provider boundary; registration is fail-closed until its anonymous security policy is wired"
    );
}

#[tokio::test]
async fn identity_security_default_provider_should_not_fabricate_authenticated_security_success() {
    let provider = Arc::new(MemorySecurityProvider::default());
    let app = identity_security_router(IdentitySecurityState::new(
        provider.clone(),
        Arc::new(AllowIdentity),
    ));

    for route in IDENTITY_SECURITY_ROUTES
        .iter()
        .filter(|route| route.auth == AuthScope::User)
    {
        let mut builder = Request::builder()
            .method(route.method)
            .uri(concrete_path(route.path))
            .header(header::AUTHORIZATION, "Bearer listener-verified");
        let body = if route.method == "POST" {
            builder = builder.header(header::CONTENT_TYPE, "application/json");
            Body::from("{}")
        } else {
            Body::empty()
        };
        let response = app
            .clone()
            .oneshot(builder.body(body).expect("authenticated security request"))
            .await
            .expect("authenticated security response");
        assert_eq!(
            response.status(),
            StatusCode::SERVICE_UNAVAILABLE,
            "{} {} fabricated a session/WebAuthn success without a real provider",
            route.method,
            route.path
        );
        assert_eq!(json_body(response).await["success"], false);
    }

    assert_eq!(
        provider.calls().expect("provider calls").len(),
        10,
        "every authenticated security operation must cross the provider boundary"
    );
}

#[tokio::test]
async fn wave3_routers_should_expose_only_the_frozen_methods_for_each_path() {
    let channel_app = channel_advanced_router(ChannelAdvancedHttpState::new(
        Arc::new(DenyChannel),
        Arc::new(CountingChannelProvider::default()),
    ));
    let identity_app = identity_security_router(IdentitySecurityState::with_rejecting_authorizer(
        Arc::new(MemorySecurityProvider::default()),
    ));

    for (routes, app) in [
        (CHANNEL_ADVANCED_ROUTES, channel_app),
        (IDENTITY_SECURITY_ROUTES, identity_app),
    ] {
        let paths = routes
            .iter()
            .map(|route| route.path)
            .collect::<HashSet<_>>();
        for path in paths {
            let response = app
                .clone()
                .oneshot(
                    Request::builder()
                        .method(Method::PATCH)
                        .uri(concrete_path(path))
                        .body(Body::empty())
                        .expect("method probe"),
                )
                .await
                .expect("method response");
            assert_eq!(
                response.status(),
                StatusCode::METHOD_NOT_ALLOWED,
                "PATCH unexpectedly matched {path}"
            );

            let actual = response
                .headers()
                .get(header::ALLOW)
                .expect("405 Allow header")
                .to_str()
                .expect("ASCII Allow header")
                .split(',')
                .map(str::trim)
                .filter(|method| *method != "HEAD")
                .collect::<HashSet<_>>();
            let expected = routes
                .iter()
                .filter(|route| route.path == path)
                .map(|route| route.method)
                .collect::<HashSet<_>>();
            assert_eq!(actual, expected, "wrong method set for {path}");
        }
    }
}
