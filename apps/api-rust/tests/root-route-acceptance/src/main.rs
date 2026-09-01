#[cfg(any(feature = "runtime", test))]
mod inventory;

#[cfg(all(feature = "runtime", not(test)))]
mod config {
    use std::net::IpAddr;

    #[derive(Clone, Debug)]
    pub enum TrustedProxyPolicy {
        Disabled,
    }

    impl TrustedProxyPolicy {
        pub const fn trusts(&self, _address: IpAddr) -> bool {
            false
        }
    }
}

#[cfg(all(feature = "runtime", not(test)))]
// The production binary calls lifecycle and route helpers that
// this focused harness intentionally does not exercise. The source is linted
// in its owning crate; suppress duplicate-context diagnostics here.
#[allow(clippy::all, dead_code)]
#[path = "../../../src/http.rs"]
mod production_http;

#[cfg(all(feature = "runtime", not(test)))]
mod runtime {
    use super::{
        config::TrustedProxyPolicy,
        inventory::{AuthClass, RouteCase, axum_path, concrete_path, load_routes, wrong_method},
        production_http::{ApiTokenMount, AppState, RuntimeState, router_with_api_token},
    };
    use async_trait::async_trait;
    use axum::{
        Router,
        body::{Body, to_bytes},
        extract::{ConnectInfo, MatchedPath},
        http::{Method, Request, StatusCode, header},
        middleware::{self, Next},
        response::Response,
    };
    use lmm_api_rs::{
        auth::{
            AuthBundle, AuthError, AuthErrorKind, AuthHttpState, CriticalRateLimitOutcome,
            DashboardAuth, DashboardUser, LoginOutcome, LoginRequest, LogoutRequest, LogoutResult,
            RequestMetadata, TwoFactorLoginRequest,
        },
        routes::api_token::{ApiTokenHttpState, PgValkeyApiTokenService},
        models::{
            ModelView, ModelsError, ModelsErrorKind, ModelsHttpState, ModelsRequest, ModelsService,
        },
        status::{StatusHttpState, StatusRepository, StatusRepositoryError, StatusSnapshot},
    };
    use lmm_application::{
        GlobalApiRateLimiter, ProbeError, PublicContentCache, PublicContentCacheError,
        PublicContentError, PublicContentRepository, PublicContentService, RateLimitError,
        RateLimitOutcome, ReadinessProbe, ValkeyReadinessPolicy,
    };
    use lmm_domain::PublicContentKind;
    use secrecy::{ExposeSecret, SecretString};
    use sqlx::postgres::PgPoolOptions;
    use std::{collections::BTreeMap, net::SocketAddr, sync::Arc, time::Duration};
    use tower::ServiceExt;

    const SYNTHETIC_USER: &str = "synthetic-route-user";
    const SYNTHETIC_ADMIN: &str = "synthetic-route-admin";
    const SYNTHETIC_ROOT: &str = "synthetic-route-root";
    const SYNTHETIC_API_TOKEN: &str = "sk-synthetic-route-token";
    const REQUEST_TIMEOUT: Duration = Duration::from_millis(750);

    struct HealthyProbe;
    struct EmptyContentRepository;
    struct EmptyContentCache;
    struct AllowRateLimit;
    struct EmptyStatusRepository;
    struct SyntheticDashboardAuth;
    struct SyntheticModels;

    #[async_trait]
    impl ReadinessProbe for HealthyProbe {
        async fn postgres(&self) -> Result<(), ProbeError> {
            Ok(())
        }

        async fn valkey(&self) -> Result<(), ProbeError> {
            Ok(())
        }

        async fn schema_compatible(&self) -> Result<(), ProbeError> {
            Ok(())
        }
    }

    #[async_trait]
    impl PublicContentRepository for EmptyContentRepository {
        async fn get(
            &self,
            _kind: PublicContentKind,
        ) -> Result<Option<String>, PublicContentError> {
            Ok(None)
        }
    }

    #[async_trait]
    impl PublicContentCache for EmptyContentCache {
        async fn get(
            &self,
            _kind: PublicContentKind,
        ) -> Result<Option<String>, PublicContentCacheError> {
            Ok(None)
        }
    }

    #[async_trait]
    impl GlobalApiRateLimiter for AllowRateLimit {
        async fn check(&self, _client_ip: &str) -> Result<RateLimitOutcome, RateLimitError> {
            Ok(RateLimitOutcome::Allowed)
        }
    }

    #[async_trait]
    impl StatusRepository for EmptyStatusRepository {
        async fn snapshot(&self) -> Result<StatusSnapshot, StatusRepositoryError> {
            Ok(StatusSnapshot {
                options: BTreeMap::new(),
                custom_oauth_providers: Vec::new(),
                setup: false,
            })
        }
    }

    #[async_trait]
    impl DashboardAuth for SyntheticDashboardAuth {
        async fn check_critical_rate_limit(
            &self,
            _client_ip: &str,
        ) -> Result<CriticalRateLimitOutcome, AuthError> {
            Ok(CriticalRateLimitOutcome::Allowed)
        }

        async fn login(
            &self,
            _request: LoginRequest,
            _metadata: RequestMetadata,
        ) -> Result<LoginOutcome, AuthError> {
            Err(AuthError::new(AuthErrorKind::InvalidCredentials))
        }

        async fn login_2fa(
            &self,
            _request: TwoFactorLoginRequest,
            _metadata: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn refresh(
            &self,
            _refresh_token: SecretString,
            _expected_sid: Option<String>,
            _metadata: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn self_user(&self, access_token: SecretString) -> Result<DashboardUser, AuthError> {
            match access_token.expose_secret() {
                SYNTHETIC_USER => Ok(dashboard_user(7, 1)),
                SYNTHETIC_ADMIN => Ok(dashboard_user(8, 10)),
                SYNTHETIC_ROOT => Ok(dashboard_user(9, 100)),
                _ => Err(AuthError::new(AuthErrorKind::Unauthorized)),
            }
        }

        async fn logout(&self, _request: LogoutRequest) -> Result<LogoutResult, AuthError> {
            Ok(LogoutResult {
                revoked_sid: None,
                cookie_cleared: None,
            })
        }

        async fn generate_personal_access_token(
            &self,
            _access_token: SecretString,
        ) -> Result<String, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }
    }

    #[async_trait]
    impl ModelsService for SyntheticModels {
        async fn list(&self, request: ModelsRequest) -> Result<Vec<ModelView>, ModelsError> {
            let bearer = request
                .authorization
                .as_deref()
                .and_then(|value| value.strip_prefix("Bearer "));
            let credential = bearer
                .or(request.api_key.as_deref())
                .or(request.gemini_key.as_deref())
                .or(request.mj_api_secret.as_deref());
            match credential {
                Some(SYNTHETIC_API_TOKEN) => {
                    Ok(vec![ModelView::new("synthetic-model", "acceptance")])
                }
                None => Err(ModelsError::new(
                    ModelsErrorKind::MissingToken,
                    "synthetic token is required",
                )),
                Some(_) => Err(ModelsError::new(
                    ModelsErrorKind::InvalidToken,
                    "synthetic token is invalid",
                )),
            }
        }
    }

    fn dashboard_user(id: i64, role: i64) -> DashboardUser {
        DashboardUser {
            id,
            username: format!("acceptance-{id}"),
            display_name: "Route acceptance".to_owned(),
            role,
            status: 1,
            email: String::new(),
            github_id: String::new(),
            discord_id: String::new(),
            oidc_id: String::new(),
            wechat_id: String::new(),
            telegram_id: String::new(),
            group: "default".to_owned(),
            quota: 1_000_000,
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
            sidebar_modules: serde_json::Value::Null,
            permissions: serde_json::Value::Null,
        }
    }

    fn root_router() -> Router {
        let dashboard_auth: Arc<dyn DashboardAuth> = Arc::new(SyntheticDashboardAuth);
        let pool = PgPoolOptions::new()
            .acquire_timeout(Duration::from_millis(25))
            .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
            .expect("synthetic lazy PostgreSQL URL is valid");
        let valkey =
            redis::Client::open("redis://127.0.0.1:1").expect("synthetic lazy Valkey URL is valid");
        let api_token_state =
            ApiTokenHttpState::new(Arc::new(PgValkeyApiTokenService::new(pool, valkey.clone())));
        let state = AppState {
            readiness: Arc::new(HealthyProbe),
            valkey_readiness_policy: ValkeyReadinessPolicy::RequiredForRateLimiting,
            global_api_rate_limiter: Arc::new(AllowRateLimit),
            public_content: Arc::new(PublicContentService::new(
                Arc::new(EmptyContentRepository),
                Arc::new(EmptyContentCache),
                Duration::from_millis(25),
            )),
            status: StatusHttpState::new(Arc::new(EmptyStatusRepository), "v0.0.0", 0),
            slot: "blue".to_owned(),
            runtime: RuntimeState::default(),
            trusted_proxies: TrustedProxyPolicy::Disabled,
        };
        router_with_api_token(
            state,
            AuthHttpState::new(Arc::clone(&dashboard_auth), false)
                .with_password_login_enabled(true)
                .with_version("v0.0.0"),
            ModelsHttpState::new(Arc::new(SyntheticModels), "v0.0.0"),
            Some(
                ApiTokenMount::new(
                    api_token_state,
                    dashboard_auth,
                    valkey,
                    Duration::from_millis(25),
                    false,
                    10,
                    Duration::from_secs(60),
                )
                .with_historical_frozen_go_parity(),
            ),
        )
        .layer(middleware::from_fn(expose_matched_path))
    }

    #[derive(Clone, Debug)]
    struct AcceptanceMatchedPath(String);

    async fn expose_matched_path(request: Request<Body>, next: Next) -> Response {
        let matched_path = request
            .extensions()
            .get::<MatchedPath>()
            .map(|path| path.as_str().to_owned());
        let mut response = next.run(request).await;
        if let Some(path) = matched_path {
            response
                .extensions_mut()
                .insert(AcceptanceMatchedPath(path));
        }
        response
    }

    #[derive(Clone, Copy)]
    enum Identity {
        Anonymous,
        User,
        Admin,
        Root,
        ApiToken,
    }

    impl Identity {
        const fn label(self) -> &'static str {
            match self {
                Self::Anonymous => "anonymous",
                Self::User => "synthetic-user",
                Self::Admin => "synthetic-admin",
                Self::Root => "synthetic-root",
                Self::ApiToken => "synthetic-api-token",
            }
        }

        const fn credential(self) -> Option<&'static str> {
            match self {
                Self::Anonymous => None,
                Self::User => Some(SYNTHETIC_USER),
                Self::Admin => Some(SYNTHETIC_ADMIN),
                Self::Root => Some(SYNTHETIC_ROOT),
                Self::ApiToken => Some(SYNTHETIC_API_TOKEN),
            }
        }
    }

    #[derive(Debug)]
    struct Observed {
        status: StatusCode,
        root_error_code: Option<String>,
        matched_path: Option<String>,
    }

    impl Observed {
        fn is_root_error(&self, code: &str) -> bool {
            self.root_error_code.as_deref() == Some(code)
        }

        fn is_identity_rejection(&self) -> bool {
            matches!(
                self.status,
                StatusCode::UNAUTHORIZED | StatusCode::FORBIDDEN
            )
        }
    }

    async fn probe(
        app: &Router,
        method: &str,
        path: &str,
        identity: Identity,
    ) -> Result<Observed, String> {
        let method = Method::from_bytes(method.as_bytes())
            .map_err(|error| format!("invalid method {method}: {error}"))?;
        let mut builder = Request::builder()
            .method(method.clone())
            .uri(path)
            .header(header::ACCEPT, "application/json");
        if let Some(credential) = identity.credential() {
            builder = builder.header(header::AUTHORIZATION, format!("Bearer {credential}"));
        }
        let body = if matches!(method, Method::POST | Method::PUT | Method::PATCH) {
            builder = builder.header(header::CONTENT_TYPE, "application/json");
            Body::from("{}")
        } else {
            Body::empty()
        };
        let mut request = builder
            .body(body)
            .map_err(|error| format!("cannot build {method} {path}: {error}"))?;
        request.extensions_mut().insert(ConnectInfo(
            "127.0.0.1:43117"
                .parse::<SocketAddr>()
                .expect("synthetic peer address is valid"),
        ));

        let response = tokio::time::timeout(REQUEST_TIMEOUT, app.clone().oneshot(request))
            .await
            .map_err(|_| {
                format!(
                    "{} {path} timed out for {}",
                    method.as_str(),
                    identity.label()
                )
            })?
            .map_err(|error| format!("{} {path} failed: {error}", method.as_str()))?;
        let status = response.status();
        let matched_path = response
            .extensions()
            .get::<AcceptanceMatchedPath>()
            .map(|path| path.0.clone());
        let root_error_code = if matches!(
            status,
            StatusCode::NOT_FOUND | StatusCode::METHOD_NOT_ALLOWED
        ) {
            let bytes =
                tokio::time::timeout(REQUEST_TIMEOUT, to_bytes(response.into_body(), 16 * 1024))
                    .await
                    .map_err(|_| format!("{} {path} error body timed out", method.as_str()))?
                    .map_err(|error| {
                        format!("{} {path} error body failed: {error}", method.as_str())
                    })?;
            serde_json::from_slice::<serde_json::Value>(&bytes)
                .ok()
                .and_then(|value| {
                    if value["message"] == "Not Found" {
                        Some("not_found".to_owned())
                    } else {
                        value["error"]["code"].as_str().map(ToOwned::to_owned)
                    }
                })
        } else {
            None
        };
        Ok(Observed {
            status,
            root_error_code,
            matched_path,
        })
    }

    fn accepted_identity(observed: &Observed) -> bool {
        !observed.is_identity_rejection()
            && observed.status != StatusCode::METHOD_NOT_ALLOWED
            && observed.matched_path.is_some()
    }

    async fn require_identity(
        app: &Router,
        route: &RouteCase,
        concrete: &str,
        identity: Identity,
        failures: &mut Vec<String>,
    ) -> Option<Observed> {
        match probe(app, &route.method, concrete, identity).await {
            Ok(observed) if accepted_identity(&observed) => Some(observed),
            Ok(observed) => {
                failures.push(format!(
                    "auth {} {} {} class={} status={} root_code={}",
                    identity.label(),
                    route.method,
                    route.path,
                    route.auth.as_str(),
                    observed.status,
                    observed.root_error_code.as_deref().unwrap_or("none")
                ));
                Some(observed)
            }
            Err(error) => {
                failures.push(format!(
                    "auth {} {} {} class={}: {error}",
                    identity.label(),
                    route.method,
                    route.path,
                    route.auth.as_str()
                ));
                None
            }
        }
    }

    async fn require_identity_invariant(
        app: &Router,
        route: &RouteCase,
        concrete: &str,
        anonymous: &Observed,
        identity: Identity,
        failures: &mut Vec<String>,
    ) {
        match probe(app, &route.method, concrete, identity).await {
            Ok(observed)
                if observed.status == anonymous.status
                    && observed.matched_path == anonymous.matched_path => {}
            Ok(observed) => failures.push(format!(
                "auth {} {} {} class={} status={} anonymous-status={} expected=identity-invariant",
                identity.label(),
                route.method,
                route.path,
                route.auth.as_str(),
                observed.status,
                anonymous.status
            )),
            Err(error) => failures.push(format!(
                "auth {} {} {} class={}: {error}",
                identity.label(),
                route.method,
                route.path,
                route.auth.as_str()
            )),
        }
    }

    async fn verify_auth_class(
        app: &Router,
        route: &RouteCase,
        concrete: &str,
        anonymous: &Observed,
        failures: &mut Vec<String>,
    ) {
        match route.auth {
            // Public and webhook handlers may legitimately reject malformed
            // handler input (for example, a missing refresh cookie or webhook
            // signature). Dashboard/API identities must not change that
            // admission result; otherwise an auth layer was mounted by mistake.
            AuthClass::Public | AuthClass::Webhook => {
                for identity in [Identity::User, Identity::Admin, Identity::ApiToken] {
                    require_identity_invariant(app, route, concrete, anonymous, identity, failures)
                        .await;
                }
            }
            AuthClass::PublicOrUser => {
                if anonymous.is_identity_rejection() {
                    failures.push(format!(
                        "auth anonymous {} {} class=public-or-user status={} expected=anonymous-admission",
                        route.method, route.path, anonymous.status
                    ));
                }
                require_identity(app, route, concrete, Identity::User, failures).await;
            }
            AuthClass::User => {
                if anonymous.status != StatusCode::UNAUTHORIZED {
                    failures.push(format!(
                        "auth anonymous {} {} class=user status={} expected=401",
                        route.method, route.path, anonymous.status
                    ));
                }
                require_identity(app, route, concrete, Identity::User, failures).await;
            }
            AuthClass::UserOrToken => {
                if anonymous.status != StatusCode::UNAUTHORIZED {
                    failures.push(format!(
                        "auth anonymous {} {} class=user-or-token status={} expected=401",
                        route.method, route.path, anonymous.status
                    ));
                }
                require_identity(app, route, concrete, Identity::User, failures).await;
                require_identity(app, route, concrete, Identity::ApiToken, failures).await;
            }
            AuthClass::Token => {
                if anonymous.status != StatusCode::UNAUTHORIZED {
                    failures.push(format!(
                        "auth anonymous {} {} class=token status={} expected=401",
                        route.method, route.path, anonymous.status
                    ));
                }
                require_identity(app, route, concrete, Identity::ApiToken, failures).await;
            }
            AuthClass::Admin => {
                if anonymous.status != StatusCode::UNAUTHORIZED {
                    failures.push(format!(
                        "auth anonymous {} {} class=admin status={} expected=401",
                        route.method, route.path, anonymous.status
                    ));
                }
                match probe(app, &route.method, concrete, Identity::User).await {
                    Ok(observed) if observed.status == StatusCode::FORBIDDEN => {}
                    Ok(observed) => failures.push(format!(
                        "auth synthetic-user {} {} class=admin status={} expected=403",
                        route.method, route.path, observed.status
                    )),
                    Err(error) => failures.push(format!(
                        "auth synthetic-user {} {} class=admin: {error}",
                        route.method, route.path
                    )),
                }
                let admin = probe(app, &route.method, concrete, Identity::Admin).await;
                let root = probe(app, &route.method, concrete, Identity::Root).await;
                let admin_accepted = admin.as_ref().is_ok_and(accepted_identity);
                let root_accepted = root.as_ref().is_ok_and(accepted_identity);
                if !admin_accepted && !root_accepted {
                    failures.push(format!(
                        "auth privileged {} {} class=admin admin={} root={}",
                        route.method,
                        route.path,
                        describe_probe(&admin),
                        describe_probe(&root)
                    ));
                }
            }
            AuthClass::Root => {
                if anonymous.status != StatusCode::UNAUTHORIZED {
                    failures.push(format!(
                        "auth anonymous {} {} class=root status={} expected=401",
                        route.method, route.path, anonymous.status
                    ));
                }
                for identity in [Identity::User, Identity::Admin] {
                    match probe(app, &route.method, concrete, identity).await {
                        Ok(observed) if observed.status == StatusCode::FORBIDDEN => {}
                        Ok(observed) => failures.push(format!(
                            "auth {} {} {} class=root status={} expected=403",
                            identity.label(),
                            route.method,
                            route.path,
                            observed.status
                        )),
                        Err(error) => failures.push(format!(
                            "auth {} {} {} class=root: {error}",
                            identity.label(),
                            route.method,
                            route.path
                        )),
                    }
                }
                require_identity(app, route, concrete, Identity::Root, failures).await;
            }
        }
    }

    fn describe_probe(result: &Result<Observed, String>) -> String {
        match result {
            Ok(observed) => format!(
                "{}:{}:matched={}",
                observed.status,
                observed.root_error_code.as_deref().unwrap_or("none"),
                observed.matched_path.as_deref().unwrap_or("none")
            ),
            Err(error) => format!("error:{error}"),
        }
    }

    pub async fn run() -> Result<(), String> {
        let routes = load_routes()?;
        let app = root_router();
        let mut failures = Vec::new();
        let mut mounted = 0usize;
        let mut auth_verified = 0usize;
        let mut wrong_method_verified = 0usize;

        for route in &routes {
            let concrete = concrete_path(&route.path);
            let expected_matched_path = axum_path(&route.path);
            let anonymous = match probe(&app, &route.method, &concrete, Identity::Anonymous).await {
                Ok(observed) => observed,
                Err(error) => {
                    failures.push(format!("shape {} {}: {error}", route.method, route.path));
                    continue;
                }
            };
            if anonymous.matched_path.as_deref() != Some(expected_matched_path.as_str()) {
                failures.push(format!(
                    "shape missing {} {} concrete={concrete} matched={} auth={} handler={}",
                    route.method,
                    route.path,
                    anonymous.matched_path.as_deref().unwrap_or("none"),
                    route.auth.as_str(),
                    route.handler
                ));
                continue;
            }
            if anonymous.status == StatusCode::METHOD_NOT_ALLOWED
                || anonymous.is_root_error("method_not_allowed")
            {
                failures.push(format!(
                    "shape method-mismatch {} {} concrete={concrete}",
                    route.method, route.path
                ));
                continue;
            }
            mounted += 1;

            let failures_before_auth = failures.len();
            verify_auth_class(&app, route, &concrete, &anonymous, &mut failures).await;
            if failures.len() == failures_before_auth {
                auth_verified += 1;
            }

            let invalid_method = wrong_method(&routes, &concrete)?;
            match probe(&app, invalid_method, &concrete, Identity::Anonymous).await {
                Ok(observed)
                    if observed.status == StatusCode::NOT_FOUND
                        && observed.is_root_error("not_found")
                        && observed.matched_path.as_deref()
                            == Some(expected_matched_path.as_str()) =>
                {
                    wrong_method_verified += 1;
                }
                Ok(observed) => failures.push(format!(
                    "wrong-method {invalid_method} {} shape={} status={} root_code={} matched={}",
                    route.path,
                    route.method,
                    observed.status,
                    observed.root_error_code.as_deref().unwrap_or("none"),
                    observed.matched_path.as_deref().unwrap_or("none")
                )),
                Err(error) => failures.push(format!(
                    "wrong-method {invalid_method} {} shape={}: {error}",
                    route.path, route.method
                )),
            }
        }

        let mut unknown_verified = 0usize;
        for unknown_path in [
            "/__lmm_root_route_acceptance_unknown__",
            "/api/not-a-route",
            "/v1/not-a-route",
        ] {
            let unknown = probe(&app, "GET", unknown_path, Identity::Anonymous).await?;
            if unknown.status == StatusCode::NOT_FOUND
                && unknown.is_root_error("not_found")
                && unknown.matched_path.is_none()
            {
                unknown_verified += 1;
            } else {
                failures.push(format!(
                    "unknown-path {unknown_path} status={} root_code={} matched={} expected=404:not_found",
                    unknown.status,
                    unknown.root_error_code.as_deref().unwrap_or("none"),
                    unknown.matched_path.as_deref().unwrap_or("none")
                ));
            }
        }

        if failures.is_empty()
            && mounted == routes.len()
            && auth_verified == routes.len()
            && wrong_method_verified == routes.len()
            && unknown_verified == 3
        {
            println!(
                "root route acceptance passed: routes={} mounted={} auth={}/{} auth-classes=8 wrong-method={} unknown={}/3",
                routes.len(),
                mounted,
                auth_verified,
                routes.len(),
                wrong_method_verified,
                unknown_verified
            );
            return Ok(());
        }

        let mut report = format!(
            "root route acceptance failed: frozen={} mounted={} auth={}/{} wrong-method={} failures={}",
            routes.len(),
            mounted,
            auth_verified,
            routes.len(),
            wrong_method_verified,
            failures.len()
        );
        for failure in failures.iter().take(80) {
            report.push_str("\n  - ");
            report.push_str(failure);
        }
        if failures.len() > 80 {
            report.push_str(&format!(
                "\n  - ... {} more failure(s)",
                failures.len() - 80
            ));
        }
        Err(report)
    }
}

#[cfg(all(feature = "runtime", not(test)))]
#[tokio::main]
async fn main() {
    if let Err(error) = runtime::run().await {
        eprintln!("{error}");
        std::process::exit(1);
    }
}

#[cfg(not(feature = "runtime"))]
fn main() {
    eprintln!(
        "enable the runtime feature or use apps/api-rust/tests/scripts/run-root-route-acceptance.sh"
    );
    std::process::exit(2);
}
