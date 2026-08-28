use std::sync::{
    Arc,
    atomic::{AtomicBool, AtomicUsize, Ordering},
};

use axum::{
    Json, Router,
    extract::{ConnectInfo, Extension, Request, State},
    http::{HeaderName, HeaderValue, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::get,
};
use lmm_api_rs::auth::{
    AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth, UserAuthPolicyError,
    enforce_user_auth,
};
use lmm_api_rs::{
    ClientIpKey, PreserveLegacyEmptyError, RequestContext,
    auth::{AuthHttpState, auth_router},
    legacy_empty_response,
    migration_routes::api_token::{ApiTokenHttpState, ApiTokenPrincipal, api_token_router},
    models::{ModelsHttpState, ModelsService, models_router},
    status::StatusHttpState,
};
use lmm_application::{
    GlobalApiRateLimiter, PublicContentService, RateLimitOutcome, ReadinessProbe,
    ValkeyReadinessPolicy, check_readiness,
};
use lmm_contracts::{
    BuildResponse, ErrorBody, ErrorEnvelope, HealthResponse, LegacySuccessEnvelope,
};
use lmm_domain::PublicContentKind;
use secrecy::SecretString;
use serde_json::Value;
use std::net::{IpAddr, SocketAddr};
use uuid::Uuid;

use crate::config::TrustedProxyPolicy;

const REQUEST_ID_HEADER: HeaderName = HeaderName::from_static("x-request-id");
const REAL_IP_HEADER: HeaderName = HeaderName::from_static("x-real-ip");
const LEGACY_REQUEST_ID_HEADER: HeaderName = HeaderName::from_static("x-oneapi-request-id");
const LEGACY_VERSION_HEADER: HeaderName = HeaderName::from_static("x-new-api-version");

#[derive(Clone)]
pub struct AppState {
    pub readiness: Arc<dyn ReadinessProbe>,
    pub valkey_readiness_policy: ValkeyReadinessPolicy,
    pub global_api_rate_limiter: Arc<dyn GlobalApiRateLimiter>,
    pub public_content: Arc<PublicContentService>,
    pub status: StatusHttpState,
    pub slot: String,
    pub runtime: RuntimeState,
    pub trusted_proxies: TrustedProxyPolicy,
}

#[derive(Clone)]
struct AuthGlobalRateLimiter(Arc<dyn GlobalApiRateLimiter>);

#[derive(Clone)]
struct AuthLegacyHeaderState {
    version: String,
}

/// Isolated dependencies for the nine API-token routes. The normal C1
/// listener uses current-Go policy; the explicit `test_instance` candidate
/// listener selects historical frozen-ledger parity.
#[derive(Clone)]
enum ApiTokenDiscoveryPolicy {
    /// Current-Go-compatible policy used by the explicitly wired C1 listener.
    CurrentGo(Arc<dyn ModelsService>),
    /// Historical frozen-ledger parity: UserAuth plus token ownership only.
    HistoricalFrozenGoParity,
}

#[derive(Clone)]
pub struct ApiTokenMount {
    state: ApiTokenHttpState,
    auth: Arc<dyn DashboardAuth>,
    discovery_policy: ApiTokenDiscoveryPolicy,
    valkey: redis::Client,
    dependency_timeout: std::time::Duration,
    search_rate_limit_enabled: bool,
    search_rate_limit: u64,
    search_rate_limit_window: std::time::Duration,
}

impl ApiTokenMount {
    #[must_use]
    pub fn new(
        state: ApiTokenHttpState,
        auth: Arc<dyn DashboardAuth>,
        valkey: redis::Client,
        dependency_timeout: std::time::Duration,
        search_rate_limit_enabled: bool,
        search_rate_limit: u64,
        search_rate_limit_window: std::time::Duration,
    ) -> Self {
        Self {
            state,
            auth,
            discovery_policy: ApiTokenDiscoveryPolicy::HistoricalFrozenGoParity,
            valkey,
            dependency_timeout,
            search_rate_limit_enabled,
            search_rate_limit,
            search_rate_limit_window,
        }
    }

    /// Opts a non-parity API-token mount into the model service's current
    /// dashboard discovery policy. Frozen Go parity mounts omit this builder
    /// and therefore use only `UserAuth` plus token ownership.
    #[must_use]
    pub fn with_current_dashboard_discovery_policy(
        mut self,
        service: Arc<dyn ModelsService>,
    ) -> Self {
        self.discovery_policy = ApiTokenDiscoveryPolicy::CurrentGo(service);
        self
    }

    /// Names the historical frozen-ledger behavior explicitly for candidate
    /// and differential test mounts.
    #[must_use]
    pub fn with_historical_frozen_go_parity(mut self) -> Self {
        self.discovery_policy = ApiTokenDiscoveryPolicy::HistoricalFrozenGoParity;
        self
    }
}

/// Shared process lifecycle state.  The deployer marks a slot draining before
/// closing its listener so stale keep-alive connections cannot keep accepting
/// new work after the edge has moved to the replacement slot.
#[derive(Clone, Default)]
pub struct RuntimeState {
    draining: Arc<AtomicBool>,
    inflight: Arc<AtomicUsize>,
}

impl RuntimeState {
    pub fn begin_drain(&self) {
        self.draining.store(true, Ordering::Release);
    }

    pub fn is_draining(&self) -> bool {
        self.draining.load(Ordering::Acquire)
    }

    pub fn inflight(&self) -> usize {
        self.inflight.load(Ordering::Acquire)
    }
}

struct InflightGuard(RuntimeState);

#[derive(Clone)]
struct RequestBoundaryState {
    runtime: RuntimeState,
    trusted_proxies: TrustedProxyPolicy,
    version: String,
}

impl Drop for InflightGuard {
    fn drop(&mut self) {
        self.0.inflight.fetch_sub(1, Ordering::AcqRel);
    }
}

#[derive(Clone)]
struct ServerRequestId(String);

#[cfg_attr(not(test), allow(dead_code))]
pub fn router(state: AppState, auth: AuthHttpState, models: ModelsHttpState) -> Router {
    router_with_api_token(state, auth, models, None)
}

/// Builds a listener route surface with an optional explicitly policy-bound
/// API-token mount. The normal C1 listener selects current-Go policy; the
/// `test_instance` candidate selects historical frozen-ledger parity.
pub fn router_with_api_token(
    state: AppState,
    auth: AuthHttpState,
    models: ModelsHttpState,
    api_token: Option<ApiTokenMount>,
) -> Router {
    router_with_api_token_and_extra(state, auth, models, api_token, None)
}

/// Builds the listener route surface and, when supplied, an API-token mount
/// plus caller-owned extra surface under one listener boundary. Mounting the
/// C1 routes does not transfer production ownership from Go.
///
/// `extra_surface` must already contain every authorization, rate-limit, and
/// provider boundary it requires. This function deliberately only merges it
/// before applying the listener-wide fallback, 405, CORS, and request-boundary
/// middleware once. The caller remains responsible for choosing the
/// historical parity or current-Go API-token policy before mounting it.
pub fn router_with_api_token_and_extra(
    state: AppState,
    auth: AuthHttpState,
    models: ModelsHttpState,
    api_token: Option<ApiTokenMount>,
    extra_surface: Option<Router>,
) -> Router {
    let router = production_surface(&state, auth, models, api_token);
    let router = match extra_surface {
        Some(extra_surface) => router.merge(extra_surface),
        None => router,
    };
    finalize_listener(router, state)
}

fn production_surface(
    state: &AppState,
    auth: AuthHttpState,
    models: ModelsHttpState,
    api_token: Option<ApiTokenMount>,
) -> Router {
    let router = Router::new()
        .route("/livez", get(livez))
        .route("/api/livez", get(api_livez))
        .route("/readyz", get(readyz))
        .route("/_internal/build", get(build))
        .route("/api/status", get(status))
        .route("/api/notice", get(notice))
        .route("/api/about", get(about))
        .route("/api/home_page_content", get(home_page_content));
    #[cfg(test)]
    let router = router
        .route("/_test/json", axum::routing::post(test_json_extractor))
        .route("/_test/hold", get(test_hold_request));
    let auth_legacy_headers = AuthLegacyHeaderState {
        version: state.status.version().to_owned(),
    };
    let api_token_legacy_headers = auth_legacy_headers.clone();
    let api_token_global_rate_limiter = Arc::clone(&state.global_api_rate_limiter);
    let auth = auth_router(auth)
        .layer(middleware::from_fn(enforce_auth_global_rate_limit))
        // This must wrap the global limiter so its fail-closed 500 and empty
        // 429 preserve the Go Auth response headers. Account-security and
        // personal-token routes are now explicit normal-listener mounts; they
        // must therefore pass through the same auth boundary as the core
        // routes.
        .layer(middleware::from_fn_with_state(
            auth_legacy_headers,
            attach_auth_legacy_headers,
        ))
        .layer(Extension(AuthGlobalRateLimiter(Arc::clone(
            &state.global_api_rate_limiter,
        ))));
    let router = router
        .with_state(state.clone())
        .merge(auth)
        .merge(models_router(models));
    match api_token {
        Some(api_token) => router.merge(mounted_api_token_router(
            api_token,
            api_token_legacy_headers,
            api_token_global_rate_limiter,
        )),
        None => router,
    }
}

fn finalize_listener(router: Router, state: AppState) -> Router {
    let boundary = RequestBoundaryState {
        runtime: state.runtime,
        trusted_proxies: state.trusted_proxies,
        version: state.status.version().to_owned(),
    };
    router
        .fallback(root_not_found)
        .method_not_allowed_fallback(root_not_found)
        .layer(middleware::from_fn(legacy_models_cors))
        .layer(middleware::from_fn_with_state(boundary, request_boundary))
}

#[allow(
    dead_code,
    reason = "the test-only wrapper is also compiled into the non-test binary target"
)]
/// Prepares the migration-candidate surface for
/// [`router_with_api_token_and_extra`].
///
/// This keeps candidate-specific compatibility headers and global limiting
/// caller-owned while leaving listener-wide behavior to the final root.
pub fn migration_candidate_test_surface(state: &AppState, candidates: Router) -> Router {
    let legacy_headers = AuthLegacyHeaderState {
        version: state.status.version().to_owned(),
    };
    let limiter = Arc::clone(&state.global_api_rate_limiter);

    candidates
        .layer(middleware::from_fn(enforce_auth_global_rate_limit))
        .layer(middleware::from_fn_with_state(
            legacy_headers,
            attach_auth_legacy_headers,
        ))
        .layer(Extension(AuthGlobalRateLimiter(limiter)))
}

/// Adds the Go `GlobalAPIRateLimit` boundary to an anonymous `/api` surface.
///
/// This is deliberately a separate, caller-owned mount instead of a
/// listener-wide layer: health and non-API routes must not consume the API
/// limiter, while authenticated and API-token surfaces already apply this
/// boundary themselves.  The final listener boundary supplies the canonical
/// trusted-proxy client key used by [`enforce_auth_global_rate_limit`].
pub fn api_global_rate_limited_surface(state: &AppState, surface: Router) -> Router {
    surface
        .layer(middleware::from_fn(enforce_auth_global_rate_limit))
        .layer(Extension(AuthGlobalRateLimiter(Arc::clone(
            &state.global_api_rate_limiter,
        ))))
}

async fn enforce_auth_global_rate_limit(
    Extension(limiter): Extension<AuthGlobalRateLimiter>,
    request: Request,
    next: Next,
) -> Response {
    if request.method() == axum::http::Method::OPTIONS
        && request.uri().path() == "/api/usage/token/"
    {
        return next.run(request).await;
    }
    let Some(client_ip) = request_client_ip_key(&request) else {
        tracing::error!("request peer address is unavailable for global API rate limiting");
        return legacy_empty_response(StatusCode::INTERNAL_SERVER_ERROR, None);
    };
    match enforce_global_api_rate_limit_with(limiter.0.as_ref(), &client_ip).await {
        Some(response) => response,
        None => next.run(request).await,
    }
}

async fn attach_auth_legacy_headers(
    State(state): State<AuthLegacyHeaderState>,
    request: Request,
    next: Next,
) -> Response {
    let request_id = request
        .extensions()
        .get::<ServerRequestId>()
        .map(|value| value.0.clone());
    let mut response = next.run(request).await;
    if let Ok(version) = HeaderValue::from_str(&state.version) {
        response
            .headers_mut()
            .insert(LEGACY_VERSION_HEADER, version);
    }
    if let Some(request_id) = request_id
        && let Ok(request_id) = HeaderValue::from_str(&request_id)
    {
        response
            .headers_mut()
            .insert(LEGACY_REQUEST_ID_HEADER, request_id);
    }
    if response
        .headers()
        .get(header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.starts_with("application/json"))
    {
        response.headers_mut().insert(
            header::CONTENT_TYPE,
            HeaderValue::from_static("application/json; charset=utf-8"),
        );
    }
    response
}

fn mounted_api_token_router(
    mount: ApiTokenMount,
    headers: AuthLegacyHeaderState,
    global_rate_limiter: Arc<dyn GlobalApiRateLimiter>,
) -> Router {
    api_token_router(mount.state.clone())
        // Layer order is intentional: Go applies GA, then UserAuth, then the
        // route-specific CT/SR guards. `layer` wraps earlier layers in Axum.
        .layer(middleware::from_fn_with_state(
            mount.clone(),
            enforce_api_token_search_rate_limit,
        ))
        .layer(middleware::from_fn_with_state(
            mount.clone(),
            enforce_api_token_critical_rate_limit,
        ))
        .layer(middleware::from_fn_with_state(
            mount,
            authenticate_api_token_dashboard_user,
        ))
        .layer(middleware::from_fn(enforce_auth_global_rate_limit))
        .layer(middleware::from_fn_with_state(
            headers,
            attach_auth_legacy_headers,
        ))
        .layer(Extension(AuthGlobalRateLimiter(global_rate_limiter)))
}

async fn authenticate_api_token_dashboard_user(
    State(mount): State<ApiTokenMount>,
    mut request: Request,
    next: Next,
) -> Response {
    let current_go_policy = matches!(
        &mount.discovery_policy,
        ApiTokenDiscoveryPolicy::CurrentGo(_)
    );
    let Some(credential) = dashboard_credential(request.headers()) else {
        return if current_go_policy {
            discovery_not_found()
        } else {
            discovery_unauthorized()
        };
    };
    let user = match mount.auth.self_user(SecretString::from(credential)).await {
        Ok(user) => user,
        Err(error) => return discovery_auth_error(error, current_go_policy),
    };
    if current_go_policy {
        let discovery_allowed = match &mount.discovery_policy {
            ApiTokenDiscoveryPolicy::CurrentGo(service) => {
                service.dashboard_discovery_access(&user).await
            }
            ApiTokenDiscoveryPolicy::HistoricalFrozenGoParity => {
                unreachable!("current-Go policy flag must match the API-token discovery policy")
            }
        };
        match discovery_allowed {
            Ok(true) => {}
            // ConsoleAccessGate conceals trust failures and its own lookup
            // errors before UserAuth gets a chance to expose a credential
            // classification.
            Ok(false) | Err(_) => return discovery_not_found(),
        }
    }
    if let Err(error) = enforce_user_auth(&user) {
        return match error {
            UserAuthPolicyError::UserDisabled => {
                discovery_unauthorized_with_code("AUTH_USER_DISABLED", "User has been banned")
            }
            UserAuthPolicyError::InsufficientPrivilege => discovery_forbidden_with_code(
                "AUTH_INSUFFICIENT_PRIVILEGE",
                "Unauthorized, insufficient privileges",
            ),
            UserAuthPolicyError::InvalidUserInfo => discovery_unauthorized_with_code(
                "AUTH_USER_INVALID",
                "Unauthorized, invalid user info",
            ),
        };
    }
    request.extensions_mut().insert(ApiTokenPrincipal {
        user_id: user.id,
        role: user.role,
        preferred_language: api_token_preferred_language(&user.setting),
    });
    let mut response = next.run(request).await;
    response.headers_mut().insert(
        HeaderName::from_static("auth-version"),
        HeaderValue::from_static("864b7076dbcd0a3c01b5520316720ebf"),
    );
    response
}

fn api_token_preferred_language(setting: &str) -> Option<&'static str> {
    let setting: Value = serde_json::from_str(setting).ok()?;
    let language = setting
        .get("language")?
        .as_str()?
        .trim()
        .to_ascii_lowercase();
    if language.starts_with("zh-tw") {
        Some("zh-tw")
    } else if language.starts_with("zh") {
        Some("zh")
    } else if language.is_empty() {
        None
    } else {
        Some("en")
    }
}

async fn enforce_api_token_critical_rate_limit(
    State(mount): State<ApiTokenMount>,
    request: Request,
    next: Next,
) -> Response {
    if !matches!(
        request.uri().path(),
        path if path.ends_with("/key") || path == "/api/token/batch/keys"
    ) {
        return next.run(request).await;
    }
    let Some(client_ip) = request_client_ip_key(&request) else {
        return legacy_empty_response(StatusCode::INTERNAL_SERVER_ERROR, None);
    };
    match mount.auth.check_critical_rate_limit(&client_ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => next.run(request).await,
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => legacy_empty_response(StatusCode::TOO_MANY_REQUESTS, Some(retry_after_seconds)),
        Err(_) => legacy_empty_response(StatusCode::INTERNAL_SERVER_ERROR, None),
    }
}

async fn enforce_api_token_search_rate_limit(
    State(mount): State<ApiTokenMount>,
    request: Request,
    next: Next,
) -> Response {
    if request.uri().path() != "/api/token/search" || !mount.search_rate_limit_enabled {
        return next.run(request).await;
    }
    let Some(principal) = request.extensions().get::<ApiTokenPrincipal>() else {
        return discovery_unauthorized();
    };
    const SCRIPT: &str = r#"
local count = redis.call('INCR', KEYS[1])
if count == 1 then redis.call('EXPIRE', KEYS[1], ARGV[2]) end
local ttl = redis.call('TTL', KEYS[1])
if count <= tonumber(ARGV[1]) then return {1, ttl} end
return {0, ttl}
"#;
    let result = tokio::time::timeout(mount.dependency_timeout, async {
        let mut connection = mount.valkey.get_multiplexed_async_connection().await?;
        redis::Script::new(SCRIPT)
            .key(format!("rateLimit:v2:user:SR:{}", principal.user_id))
            .arg(mount.search_rate_limit)
            .arg(mount.search_rate_limit_window.as_secs())
            .invoke_async::<(i64, i64)>(&mut connection)
            .await
    })
    .await;
    match result {
        Ok(Ok((1, _))) => next.run(request).await,
        Ok(Ok((_, ttl))) => legacy_empty_response(
            StatusCode::TOO_MANY_REQUESTS,
            u64::try_from(ttl).ok().filter(|ttl| *ttl > 0),
        ),
        Ok(Err(_)) | Err(_) => legacy_empty_response(StatusCode::INTERNAL_SERVER_ERROR, None),
    }
}

fn discovery_not_found() -> Response {
    (
        StatusCode::NOT_FOUND,
        Json(serde_json::json!({"message": "Not Found"})),
    )
        .into_response()
}

fn discovery_unauthorized() -> Response {
    discovery_unauthorized_with_code("AUTH_UNAUTHORIZED", "Unauthorized, invalid access token")
}

fn discovery_unauthorized_with_code(code: &'static str, message: &'static str) -> Response {
    let mut response = (
        StatusCode::UNAUTHORIZED,
        Json(serde_json::json!({
            "success": false,
            "code": code,
            "message": message,
        })),
    )
        .into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

fn discovery_forbidden_with_code(code: &'static str, message: &'static str) -> Response {
    let mut response = (
        StatusCode::FORBIDDEN,
        Json(serde_json::json!({
            "success": false,
            "code": code,
            "message": message,
        })),
    )
        .into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

fn discovery_internal_error() -> Response {
    (
        StatusCode::INTERNAL_SERVER_ERROR,
        Json(serde_json::json!({"message": "Internal Server Error"})),
    )
        .into_response()
}

fn discovery_auth_error(error: AuthError, current_go_policy: bool) -> Response {
    if current_go_policy {
        // ConsoleAccessGate hides every failed credential classification on a
        // discovery route, including expired/revoked credentials and lookup
        // errors. Only a user that passes this gate reaches UserAuth below.
        return discovery_not_found();
    }
    match error.kind {
        AuthErrorKind::UserDisabled => {
            discovery_unauthorized_with_code("AUTH_USER_DISABLED", "User has been banned")
        }
        AuthErrorKind::Internal => discovery_internal_error(),
        AuthErrorKind::TokenExpired => discovery_unauthorized_with_code(
            "AUTH_TOKEN_EXPIRED",
            "Unauthorized, not logged in and no access token provided",
        ),
        AuthErrorKind::SessionRevoked => discovery_unauthorized_with_code(
            "AUTH_SESSION_REVOKED",
            "Unauthorized, not logged in and no access token provided",
        ),
        AuthErrorKind::InvalidCredentials
        | AuthErrorKind::InvalidRequest
        | AuthErrorKind::Unauthorized => discovery_unauthorized(),
        _ => discovery_unauthorized_with_code(
            "AUTH_UNAUTHORIZED",
            "Unauthorized, invalid access token",
        ),
    }
}

fn dashboard_credential(headers: &axum::http::HeaderMap) -> Option<String> {
    let value = headers.get(header::AUTHORIZATION)?.to_str().ok()?.trim();
    let mut fields = value.split_whitespace();
    let first = fields.next()?;
    let second = fields.next();
    if fields.next().is_some() {
        return None;
    }
    match second {
        Some(token) if first.eq_ignore_ascii_case("bearer") && !token.is_empty() => {
            Some(token.to_owned())
        }
        None if !first.is_empty() => Some(first.to_owned()),
        _ => None,
    }
}

#[cfg(test)]
async fn test_json_extractor(Json(_value): Json<serde_json::Value>) -> StatusCode {
    StatusCode::NO_CONTENT
}

#[cfg(test)]
async fn test_hold_request() -> StatusCode {
    tokio::time::sleep(std::time::Duration::from_millis(75)).await;
    StatusCode::NO_CONTENT
}

async fn request_boundary(
    State(boundary): State<RequestBoundaryState>,
    mut request: Request,
    next: Next,
) -> Response {
    if boundary.runtime.is_draining() {
        return error_response(
            StatusCode::SERVICE_UNAVAILABLE,
            "draining",
            "server is draining; retry the replacement slot",
            "unknown",
        );
    }
    boundary.runtime.inflight.fetch_add(1, Ordering::AcqRel);
    let _guard = InflightGuard(boundary.runtime);
    request.headers_mut().remove(&REQUEST_ID_HEADER);
    let request_id = Uuid::new_v4().to_string();
    request
        .extensions_mut()
        .insert(ServerRequestId(request_id.clone()));
    let client = canonical_client_ip_with_key(&request, &boundary.trusted_proxies);
    request.extensions_mut().insert(RequestContext {
        client_ip: client.as_ref().map(|(ip, _)| *ip),
        request_id: request_id.clone(),
    });
    if let Some((_, key)) = client {
        request.extensions_mut().insert(ClientIpKey(key));
    }
    let preserve_legacy_non_json_error = preserves_legacy_non_json_error(request.uri().path());
    let mut response = next.run(request).await;
    if response.status().is_client_error() || response.status().is_server_error() {
        let is_json = response
            .headers()
            .get(header::CONTENT_TYPE)
            .and_then(|value| value.to_str().ok())
            .is_some_and(|value| value.starts_with("application/json"));
        let preserve_empty = response
            .extensions()
            .get::<PreserveLegacyEmptyError>()
            .is_some();
        if !is_json && !preserve_empty && !preserve_legacy_non_json_error {
            response = error_response(
                response.status(),
                status_code(response.status()),
                "request rejected",
                &request_id,
            );
        }
    }
    if let Ok(value) = HeaderValue::from_str(&request_id) {
        response
            .headers_mut()
            .insert(REQUEST_ID_HEADER, value.clone());
        response
            .headers_mut()
            .insert(LEGACY_REQUEST_ID_HEADER, value);
    }
    if !response.headers().contains_key(&LEGACY_VERSION_HEADER)
        && let Ok(value) = HeaderValue::from_str(&boundary.version)
    {
        response.headers_mut().insert(LEGACY_VERSION_HEADER, value);
    }
    response
}

fn preserves_legacy_non_json_error(path: &str) -> bool {
    path == "/api/waffo/webhook"
        || path
            .strip_prefix("/api/waffo-pancake/webhook/")
            .is_some_and(|environment| !environment.is_empty() && !environment.contains('/'))
}

async fn legacy_models_cors(request: Request, next: Next) -> Response {
    if !is_models_path(request.uri().path()) || !request.headers().contains_key(header::ORIGIN) {
        return next.run(request).await;
    }
    let preflight = request.method() == axum::http::Method::OPTIONS
        && request
            .headers()
            .contains_key("access-control-request-method");
    let mut response = if preflight {
        StatusCode::NO_CONTENT.into_response()
    } else {
        next.run(request).await
    };
    response.headers_mut().insert(
        HeaderName::from_static("access-control-allow-origin"),
        HeaderValue::from_static("*"),
    );
    response.headers_mut().insert(
        HeaderName::from_static("access-control-allow-credentials"),
        HeaderValue::from_static("true"),
    );
    if preflight {
        response.headers_mut().insert(
            HeaderName::from_static("access-control-allow-methods"),
            HeaderValue::from_static("GET,POST,PUT,DELETE,OPTIONS"),
        );
        response.headers_mut().insert(
            HeaderName::from_static("access-control-allow-headers"),
            HeaderValue::from_static("*"),
        );
        response.headers_mut().insert(
            HeaderName::from_static("access-control-max-age"),
            HeaderValue::from_static("43200"),
        );
    }
    response
}

fn is_models_path(path: &str) -> bool {
    matches!(
        path,
        "/v1/models" | "/v1beta/models" | "/v1beta/openai/models"
    )
}

async fn livez() -> Json<HealthResponse> {
    Json(HealthResponse { status: "ok" })
}

/// Legacy API liveness envelope.  The internal `/livez` probe intentionally
/// remains the compact `{status:"ok"}` contract; the public `/api/livez`
/// route is a Go-compatible API read and therefore carries the legacy
/// success/data fields.
async fn api_livez() -> Response {
    let mut response = Json(serde_json::json!({
        "success": true,
        "live": true,
        "message": "",
    }))
    .into_response();
    // Gin's JSON writer includes the charset parameter.  Keep the public
    // legacy route's wire contract distinct from the compact internal probe.
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

async fn readyz(State(state): State<AppState>, request: Request) -> Response {
    if state.runtime.is_draining() {
        tracing::info!(slot = %state.slot, inflight = state.runtime.inflight(), "readiness rejected while slot is draining");
        return error_from_request(
            StatusCode::SERVICE_UNAVAILABLE,
            "draining",
            "server is draining; retry the replacement slot",
            &request,
        );
    }
    let report = check_readiness(state.readiness.as_ref(), state.valkey_readiness_policy).await;
    for failure in &report.required_failures {
        tracing::warn!(
            dependency = failure.dependency,
            "required readiness check failed"
        );
    }
    for failure in &report.degraded {
        tracing::warn!(
            dependency = failure.dependency,
            "optional dependency is degraded"
        );
    }
    if !report.required_failures.is_empty() {
        error_from_request(
            StatusCode::SERVICE_UNAVAILABLE,
            "not_ready",
            "required service dependencies are unavailable",
            &request,
        )
    } else if report.degraded.is_empty() {
        Json(HealthResponse { status: "ok" }).into_response()
    } else {
        Json(HealthResponse { status: "degraded" }).into_response()
    }
}

async fn build(State(state): State<AppState>) -> Response {
    let mut response = Json(BuildResponse {
        version: env!("CARGO_PKG_VERSION"),
        revision: option_env!("LMM_BUILD_REVISION").unwrap_or("unknown"),
        slot: state.slot,
    })
    .into_response();
    response.headers_mut().insert(
        HeaderName::from_static("x-lmm-draining"),
        HeaderValue::from_static(if state.runtime.is_draining() {
            "true"
        } else {
            "false"
        }),
    );
    if let Ok(inflight) = HeaderValue::from_str(&state.runtime.inflight().to_string()) {
        response
            .headers_mut()
            .insert(HeaderName::from_static("x-lmm-inflight"), inflight);
    }
    response
}

async fn notice(State(state): State<AppState>, request: Request) -> Response {
    public_content(state, request, PublicContentKind::Notice).await
}

async fn about(State(state): State<AppState>, request: Request) -> Response {
    public_content(state, request, PublicContentKind::About).await
}

async fn home_page_content(State(state): State<AppState>, request: Request) -> Response {
    public_content(state, request, PublicContentKind::HomePage).await
}

async fn status(State(state): State<AppState>, request: Request) -> Response {
    let credential = dashboard_credential(request.headers());
    let mut response = match request_client_ip_key(&request) {
        Some(client_ip) => match enforce_global_api_rate_limit(&state, &client_ip).await {
            Some(response) => response,
            None => {
                state
                    .status
                    .response_with_authorization(credential.as_deref())
                    .await
            }
        },
        None => {
            tracing::error!("request peer address is unavailable for global API rate limiting");
            legacy_empty_response(StatusCode::INTERNAL_SERVER_ERROR, None)
        }
    };
    attach_legacy_api_headers(&state, &request, &mut response);
    response
}

/// The legacy API middleware applies these headers to every public `/api/*`
/// response, including rate-limit and dependency failures.  Keep the public
/// content handlers on the same contract as `/api/status`, rather than only
/// decorating their success JSON body.
fn attach_legacy_api_headers(state: &AppState, request: &Request, response: &mut Response) {
    if let Ok(version) = HeaderValue::from_str(state.status.version()) {
        response
            .headers_mut()
            .insert(LEGACY_VERSION_HEADER, version);
    }
    if let Some(request_id) = request.extensions().get::<ServerRequestId>()
        && let Ok(request_id) = HeaderValue::from_str(&request_id.0)
    {
        response
            .headers_mut()
            .insert(LEGACY_REQUEST_ID_HEADER, request_id);
    }
    if response
        .headers()
        .get(header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.starts_with("application/json"))
    {
        response.headers_mut().insert(
            header::CONTENT_TYPE,
            HeaderValue::from_static("application/json; charset=utf-8"),
        );
    }
}

async fn public_content(state: AppState, request: Request, kind: PublicContentKind) -> Response {
    let mut response = match request_client_ip_key(&request) {
        None => {
            tracing::error!("request peer address is unavailable for global API rate limiting");
            legacy_empty_response(StatusCode::INTERNAL_SERVER_ERROR, None)
        }
        Some(client_ip) => {
            if let Some(response) = enforce_global_api_rate_limit(&state, &client_ip).await {
                response
            } else {
                match state.public_content.read(kind).await {
                    Ok(data) => Json(LegacySuccessEnvelope {
                        success: true,
                        message: "",
                        data,
                    })
                    .into_response(),
                    Err(error) => {
                        tracing::error!(%error, "authoritative public content read failed");
                        error_from_request(
                            StatusCode::INTERNAL_SERVER_ERROR,
                            "content_unavailable",
                            "public content is temporarily unavailable",
                            &request,
                        )
                    }
                }
            }
        }
    };
    attach_legacy_api_headers(&state, &request, &mut response);
    response
}

async fn enforce_global_api_rate_limit(state: &AppState, client_ip: &str) -> Option<Response> {
    enforce_global_api_rate_limit_with(state.global_api_rate_limiter.as_ref(), client_ip).await
}

async fn enforce_global_api_rate_limit_with(
    limiter: &dyn GlobalApiRateLimiter,
    client_ip: &str,
) -> Option<Response> {
    match limiter.check(client_ip).await {
        Ok(RateLimitOutcome::Allowed) => None,
        Ok(RateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => Some(legacy_empty_response(
            StatusCode::TOO_MANY_REQUESTS,
            retry_after_seconds,
        )),
        Err(error) => {
            tracing::error!(%error, client_ip, "global API rate limit check failed closed");
            Some(legacy_empty_response(
                StatusCode::INTERNAL_SERVER_ERROR,
                None,
            ))
        }
    }
}

fn request_client_ip(request: &Request) -> Option<IpAddr> {
    request
        .extensions()
        .get::<RequestContext>()
        .and_then(|context| context.client_ip)
}

fn request_client_ip_key(request: &Request) -> Option<String> {
    request
        .extensions()
        .get::<ClientIpKey>()
        .map(|key| key.0.clone())
        .or_else(|| request_client_ip(request).map(|ip| ip.to_string()))
}

#[cfg_attr(not(test), allow(dead_code))]
fn canonical_client_ip(request: &Request, trusted_proxies: &TrustedProxyPolicy) -> Option<IpAddr> {
    canonical_client_ip_with_key(request, trusted_proxies).map(|(ip, _)| ip)
}

fn canonical_client_ip_with_key(
    request: &Request,
    trusted_proxies: &TrustedProxyPolicy,
) -> Option<(IpAddr, String)> {
    let peer_ip = request
        .extensions()
        .get::<ConnectInfo<SocketAddr>>()?
        .0
        .ip();
    if !trusted_proxies.trusts(peer_ip) {
        return Some((peer_ip, peer_ip.to_string()));
    }
    if let Some(forwarded_for) = request
        .headers()
        .get("x-forwarded-for")
        .and_then(|value| value.to_str().ok())
        && let Some(client) = trusted_forwarded_header(forwarded_for, trusted_proxies)
    {
        return Some(client);
    }
    if let Some((forwarded_ip, key)) = request
        .headers()
        .get(&REAL_IP_HEADER)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| trusted_forwarded_header(value, trusted_proxies))
    {
        return Some((forwarded_ip, key));
    }
    Some((peer_ip, peer_ip.to_string()))
}

/// Gin's `validateHeader`: inspect a comma-separated chain from right to left,
/// stop on the first malformed segment, and return the first untrusted address
/// (or the leftmost address when every segment is trusted).  The trimmed raw
/// text is retained for legacy rate-limit keys, including IPv6 spellings.
fn trusted_forwarded_header(
    header_value: &str,
    trusted_proxies: &TrustedProxyPolicy,
) -> Option<(IpAddr, String)> {
    if header_value.is_empty() {
        return None;
    }
    let items = header_value.split(',').collect::<Vec<_>>();
    for (index, item) in items.iter().enumerate().rev() {
        let raw = item.trim();
        let address = raw.parse::<IpAddr>().ok()?;
        if index == 0 || !trusted_proxies.trusts(address) {
            return Some((address, raw.to_owned()));
        }
    }
    None
}

/// Gin's root `NoRoute`/method-mismatch response is deliberately smaller than
/// the OpenAI-compatible relay error envelope.
async fn root_not_found(_request: Request) -> Response {
    (
        StatusCode::NOT_FOUND,
        Json(serde_json::json!({"message": "Not Found"})),
    )
        .into_response()
}

fn error_from_request(
    status: StatusCode,
    code: &'static str,
    message: &str,
    request: &Request,
) -> Response {
    let request_id = request
        .extensions()
        .get::<ServerRequestId>()
        .map_or("unknown", |value| value.0.as_str());
    error_response(status, code, message, request_id)
}

fn error_response(
    status: StatusCode,
    code: &'static str,
    message: &str,
    request_id: &str,
) -> Response {
    (
        status,
        Json(ErrorEnvelope {
            error: ErrorBody {
                code,
                message: message.to_owned(),
            },
            request_id: request_id.to_owned(),
        }),
    )
        .into_response()
}

fn status_code(status: StatusCode) -> &'static str {
    match status {
        StatusCode::BAD_REQUEST | StatusCode::UNPROCESSABLE_ENTITY => "invalid_request",
        StatusCode::METHOD_NOT_ALLOWED => "method_not_allowed",
        StatusCode::NOT_FOUND => "not_found",
        StatusCode::SERVICE_UNAVAILABLE => "not_ready",
        _ if status.is_server_error() => "internal_error",
        _ => "request_rejected",
    }
}

#[cfg(test)]
mod tests {
    use super::{
        ApiTokenMount, AppState, LEGACY_REQUEST_ID_HEADER, LEGACY_VERSION_HEADER,
        REQUEST_ID_HEADER, RuntimeState, TrustedProxyPolicy, api_global_rate_limited_surface,
        canonical_client_ip, discovery_auth_error, discovery_unauthorized, finalize_listener,
        migration_candidate_test_surface, preserves_legacy_non_json_error,
        router as production_router, router_with_api_token, router_with_api_token_and_extra,
    };

    #[test]
    fn only_legacy_webhook_routes_keep_non_json_error_bodies() {
        assert!(preserves_legacy_non_json_error("/api/waffo/webhook"));
        assert!(preserves_legacy_non_json_error(
            "/api/waffo-pancake/webhook/test"
        ));
        assert!(!preserves_legacy_non_json_error(
            "/api/waffo-pancake/webhook/"
        ));
        assert!(!preserves_legacy_non_json_error(
            "/api/waffo-pancake/webhook/test/extra"
        ));
        assert!(!preserves_legacy_non_json_error("/api/other"));
    }
    use async_trait::async_trait;
    use axum::{
        Router,
        body::Body,
        extract::ConnectInfo,
        http::{Request, StatusCode, header},
        response::IntoResponse,
        routing::get,
    };
    use lmm_api_rs::{
        auth::{
            AuthBundle, AuthError, AuthErrorKind, AuthHttpState, CriticalRateLimitOutcome,
            DashboardAuth, DashboardUser, LoginOutcome, LoginRequest, LogoutRequest, LogoutResult,
            RequestMetadata, TwoFactorLoginRequest,
        },
        migration_routes::{
            api_token::{ApiTokenHttpState, ApiTokenPrincipal, PgValkeyApiTokenService},
            open_source_bounties::{OpenSourceBountyState, router as open_source_bounty_router},
            relay_anthropic_gemini::{
                RelayBackend, RelayChannel, RelayFailure, RelayHttpState, RelayIdentity,
                RelayOutcome, RelayProtocol, UpstreamReply, UpstreamRequest,
                router as relay_router,
            },
        },
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
    use secrecy::SecretString;
    use serde_json::Value;
    use sqlx::postgres::PgPoolOptions;
    use std::{
        collections::BTreeMap,
        net::SocketAddr,
        sync::{
            Arc, Mutex,
            atomic::{AtomicUsize, Ordering},
        },
    };
    use tower::ServiceExt;
    use uuid::Uuid;

    type TestResult<T = ()> = Result<T, Box<dyn std::error::Error + Send + Sync>>;

    fn test_error(message: &'static str) -> std::io::Error {
        std::io::Error::other(message)
    }

    #[tokio::test]
    async fn anonymous_api_token_auth_uses_the_go_unauthorized_envelope() -> TestResult {
        let response = discovery_unauthorized();
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "application/json; charset=utf-8"
        );
        let body: Value =
            serde_json::from_slice(&axum::body::to_bytes(response.into_body(), usize::MAX).await?)?;
        assert_eq!(
            body,
            serde_json::json!({
                "code": "AUTH_UNAUTHORIZED",
                "message": "Unauthorized, invalid access token",
                "success": false
            })
        );
        Ok(())
    }

    #[tokio::test]
    async fn dashboard_auth_error_codes_match_the_go_envelopes() -> TestResult {
        for (kind, code, message) in [
            (
                AuthErrorKind::Unauthorized,
                "AUTH_UNAUTHORIZED",
                "Unauthorized, invalid access token",
            ),
            (
                AuthErrorKind::TokenExpired,
                "AUTH_TOKEN_EXPIRED",
                "Unauthorized, not logged in and no access token provided",
            ),
            (
                AuthErrorKind::SessionRevoked,
                "AUTH_SESSION_REVOKED",
                "Unauthorized, not logged in and no access token provided",
            ),
            (
                AuthErrorKind::UserDisabled,
                "AUTH_USER_DISABLED",
                "User has been banned",
            ),
        ] {
            let response = discovery_auth_error(AuthError::new(kind), false);
            assert_eq!(response.status(), StatusCode::UNAUTHORIZED, "{code}");
            assert_eq!(
                response.headers()[header::CONTENT_TYPE],
                "application/json; charset=utf-8",
                "{code}"
            );
            let body: Value = serde_json::from_slice(
                &axum::body::to_bytes(response.into_body(), usize::MAX).await?,
            )?;
            assert_eq!(
                body,
                serde_json::json!({"code": code, "message": message, "success": false}),
                "{code}"
            );
        }
        Ok(())
    }

    #[tokio::test]
    async fn current_go_discovery_auth_classification_failures_are_hidden() -> TestResult {
        for kind in [
            AuthErrorKind::InvalidCredentials,
            AuthErrorKind::Unauthorized,
            AuthErrorKind::TokenExpired,
            AuthErrorKind::SessionRevoked,
            AuthErrorKind::UserDisabled,
            AuthErrorKind::Internal,
        ] {
            let response = discovery_auth_error(AuthError::new(kind), true);
            assert_eq!(response.status(), StatusCode::NOT_FOUND, "{kind:?}");
            let body: Value = serde_json::from_slice(
                &axum::body::to_bytes(response.into_body(), usize::MAX).await?,
            )?;
            assert_eq!(
                body,
                serde_json::json!({"message": "Not Found"}),
                "{kind:?}"
            );
        }
        Ok(())
    }

    struct RootRelayBackend {
        request_ids: Arc<Mutex<Vec<String>>>,
    }

    #[async_trait]
    impl RelayBackend for RootRelayBackend {
        async fn authenticate(&self, token: &str) -> Result<RelayIdentity, RelayFailure> {
            (token == "root-relay-token")
                .then(|| RelayIdentity {
                    token_id: "root-relay-identity".to_owned(),
                })
                .ok_or(RelayFailure::Unauthorized)
        }

        async fn select_channel(
            &self,
            _: &RelayIdentity,
            _: RelayProtocol,
            model: &str,
        ) -> Result<RelayChannel, RelayFailure> {
            Ok(RelayChannel {
                id: 11,
                upstream_model: model.to_owned(),
            })
        }

        async fn invoke(
            &self,
            _: &RelayChannel,
            request: UpstreamRequest,
        ) -> Result<UpstreamReply, RelayFailure> {
            self.request_ids
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner())
                .push(request.request_id.clone());
            Err(RelayFailure::Provider {
                status: StatusCode::TOO_MANY_REQUESTS,
                body: serde_json::json!({
                    "error": {
                        "message": "provider rejected request",
                        "request_id": request.request_id,
                    }
                }),
            })
        }

        async fn record_outcome(
            &self,
            _: Option<&RelayIdentity>,
            _: Option<&RelayChannel>,
            _: RelayOutcome,
        ) {
        }
    }

    struct MockProbe(Option<&'static str>);

    struct MockContentRepository(Option<String>);

    struct MissingCache;

    struct AllowAllRateLimiter;

    struct MockStatusRepository;

    struct UnavailableAuth;

    struct UnavailableModels;

    struct DiscoveryGateModels {
        allow: bool,
        calls: Arc<AtomicUsize>,
    }

    struct FailingDiscoveryModels;

    #[derive(Clone)]
    struct TokenRouteAuth {
        user: DashboardUser,
    }

    #[async_trait]
    impl DashboardAuth for UnavailableAuth {
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
            Err(AuthError::new(AuthErrorKind::Internal))
        }

        async fn login_2fa(
            &self,
            _request: TwoFactorLoginRequest,
            _metadata: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            Err(AuthError::new(AuthErrorKind::Internal))
        }

        async fn refresh(
            &self,
            _refresh_token: SecretString,
            _expected_sid: Option<String>,
            _metadata: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            Err(AuthError::new(AuthErrorKind::Internal))
        }

        async fn self_user(&self, _access_token: SecretString) -> Result<DashboardUser, AuthError> {
            Err(AuthError::new(AuthErrorKind::Internal))
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
            Err(AuthError::new(AuthErrorKind::Internal))
        }
    }

    #[async_trait]
    impl DashboardAuth for TokenRouteAuth {
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
            Err(AuthError::new(AuthErrorKind::Internal))
        }

        async fn login_2fa(
            &self,
            _request: TwoFactorLoginRequest,
            _metadata: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            Err(AuthError::new(AuthErrorKind::Internal))
        }

        async fn refresh(
            &self,
            _refresh_token: SecretString,
            _expected_sid: Option<String>,
            _metadata: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            Err(AuthError::new(AuthErrorKind::Internal))
        }

        async fn self_user(&self, _access_token: SecretString) -> Result<DashboardUser, AuthError> {
            Ok(self.user.clone())
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
            Err(AuthError::new(AuthErrorKind::Internal))
        }
    }

    #[async_trait]
    impl ModelsService for UnavailableModels {
        async fn list(&self, _request: ModelsRequest) -> Result<Vec<ModelView>, ModelsError> {
            Err(ModelsError::new(
                ModelsErrorKind::Database,
                "models test stub is unavailable",
            ))
        }
    }

    #[async_trait]
    impl ModelsService for DiscoveryGateModels {
        async fn list(&self, _request: ModelsRequest) -> Result<Vec<ModelView>, ModelsError> {
            Ok(Vec::new())
        }

        async fn dashboard_discovery_access(
            &self,
            _user: &DashboardUser,
        ) -> Result<bool, ModelsError> {
            self.calls.fetch_add(1, Ordering::Relaxed);
            Ok(self.allow)
        }
    }

    #[async_trait]
    impl ModelsService for FailingDiscoveryModels {
        async fn list(&self, _request: ModelsRequest) -> Result<Vec<ModelView>, ModelsError> {
            Ok(Vec::new())
        }

        async fn dashboard_discovery_access(
            &self,
            _user: &DashboardUser,
        ) -> Result<bool, ModelsError> {
            Err(ModelsError::new(
                ModelsErrorKind::Database,
                "discovery lookup failed",
            ))
        }
    }

    #[async_trait]
    impl StatusRepository for MockStatusRepository {
        async fn snapshot(&self) -> Result<StatusSnapshot, StatusRepositoryError> {
            Ok(StatusSnapshot {
                options: BTreeMap::new(),
                custom_oauth_providers: Vec::new(),
                setup: false,
            })
        }
    }

    fn router(state: AppState) -> axum::Router {
        production_router(state, auth_state(), models_state())
    }

    fn auth_state() -> AuthHttpState {
        AuthHttpState::new(Arc::new(UnavailableAuth), false).with_version("v0.0.0")
    }

    fn models_state() -> ModelsHttpState {
        ModelsHttpState::new(Arc::new(UnavailableModels), "v0.0.0")
    }

    fn open_source_bounty_test_surface() -> TestResult<Router> {
        let pg = PgPoolOptions::new()
            .acquire_timeout(std::time::Duration::from_millis(10))
            .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")?;
        Ok(open_source_bounty_router(OpenSourceBountyState::new(
            pg,
            Arc::new(UnavailableAuth),
        )))
    }

    #[tokio::test]
    async fn final_candidate_listener_keeps_both_topup_reads_reachable() -> TestResult {
        let pg = PgPoolOptions::new()
            .acquire_timeout(std::time::Duration::from_millis(10))
            .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")?;
        let valkey = redis::Client::open("redis://127.0.0.1:1")?;
        let auth: Arc<dyn DashboardAuth> = Arc::new(UnavailableAuth);
        let app_state = state(None)?;
        let candidates = migration_candidate_test_surface(
            &app_state,
            crate::test_instance::safe_candidate_surface(pg, valkey, Arc::clone(&auth)),
        );
        let app = router_with_api_token_and_extra(
            app_state,
            AuthHttpState::new(auth, false).with_version("v0.0.0"),
            models_state(),
            None,
            Some(candidates),
        );
        for path in [
            "/api/user/topup/info",
            "/api/user/topup/self",
            "/api/user/checkin?month=2026-08",
        ] {
            let mut request = Request::get(path).body(Body::empty())?;
            request
                .extensions_mut()
                .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));
            let response = app.clone().oneshot(request).await?;
            assert_eq!(response.status(), StatusCode::UNAUTHORIZED, "{path}");
        }
        Ok(())
    }

    fn dashboard_user(id: i64, role: i64, status: i64) -> DashboardUser {
        DashboardUser {
            id,
            username: "token-user".to_owned(),
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
            sidebar_modules: Value::Null,
            permissions: Value::Null,
        }
    }

    fn mounted_api_token_router_for(user: DashboardUser) -> TestResult<axum::Router> {
        mounted_api_token_router_for_with_discovery(user, None)
    }

    fn mounted_api_token_router_for_with_discovery(
        user: DashboardUser,
        discovery_service: Option<Arc<dyn ModelsService>>,
    ) -> TestResult<axum::Router> {
        mounted_api_token_router_for_with_services(
            user,
            discovery_service,
            Arc::new(UnavailableModels),
        )
    }

    fn mounted_api_token_router_for_with_services(
        user: DashboardUser,
        discovery_service: Option<Arc<dyn ModelsService>>,
        models_service: Arc<dyn ModelsService>,
    ) -> TestResult<axum::Router> {
        let auth: Arc<dyn DashboardAuth> = Arc::new(TokenRouteAuth { user });
        let pool = PgPoolOptions::new()
            .acquire_timeout(std::time::Duration::from_millis(10))
            .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")?;
        let valkey = redis::Client::open("redis://127.0.0.1:1")?;
        let token_state =
            ApiTokenHttpState::new(Arc::new(PgValkeyApiTokenService::new(pool, valkey.clone())));
        let mount = ApiTokenMount::new(
            token_state,
            Arc::clone(&auth),
            valkey,
            std::time::Duration::from_millis(10),
            false,
            10,
            std::time::Duration::from_secs(60),
        )
        .with_historical_frozen_go_parity();
        let mount = match discovery_service {
            Some(service) => mount.with_current_dashboard_discovery_policy(service),
            None => mount,
        };
        Ok(router_with_api_token(
            state(None)?,
            AuthHttpState::new(Arc::clone(&auth), false),
            ModelsHttpState::new(models_service, "v0.0.0"),
            Some(mount),
        ))
    }

    async fn mounted_api_token_call(
        router: TestResult<axum::Router>,
        method: &str,
        uri: &str,
        body: Option<&str>,
        forged_principal: bool,
    ) -> TestResult<axum::response::Response> {
        mounted_api_token_call_with_locale(router, method, uri, body, forged_principal, None).await
    }

    async fn mounted_api_token_call_with_locale(
        router: TestResult<axum::Router>,
        method: &str,
        uri: &str,
        body: Option<&str>,
        forged_principal: bool,
        accept_language: Option<&str>,
    ) -> TestResult<axum::response::Response> {
        let router = router?;
        let mut builder = Request::builder()
            .method(method)
            .uri(uri)
            .header(header::AUTHORIZATION, "Bearer dashboard-session-token");
        if let Some(accept_language) = accept_language {
            builder = builder.header(header::ACCEPT_LANGUAGE, accept_language);
        }
        if body.is_some() {
            builder = builder.header(header::CONTENT_TYPE, "application/json");
        }
        let mut request =
            builder.body(body.map_or_else(Body::empty, |value| Body::from(value.to_owned())))?;
        request
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));
        if forged_principal {
            request.extensions_mut().insert(ApiTokenPrincipal {
                user_id: 999,
                role: 10,
                preferred_language: None,
            });
        }
        Ok(router.oneshot(request).await?)
    }

    async fn mounted_api_token_call_without_credential(
        router: TestResult<axum::Router>,
    ) -> TestResult<axum::response::Response> {
        let router = router?;
        let mut request = Request::builder()
            .method("GET")
            .uri("/api/token/")
            .body(Body::empty())?;
        request
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));
        Ok(router.oneshot(request).await?)
    }

    #[tokio::test]
    async fn mounted_api_token_routes_reject_disabled_dashboard_users_before_token_access()
    -> TestResult {
        let response = mounted_api_token_call(
            mounted_api_token_router_for(dashboard_user(7, 1, 2)),
            "GET",
            "/api/token/",
            None,
            false,
        )
        .await?;
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        let body: Value =
            serde_json::from_slice(&axum::body::to_bytes(response.into_body(), usize::MAX).await?)?;
        assert_eq!(body["code"], "AUTH_USER_DISABLED");
        assert_eq!(body["success"], false);
        Ok(())
    }

    #[tokio::test]
    async fn mounted_api_token_routes_share_the_models_discovery_gate() -> TestResult {
        for (allow, expected_status) in [(true, StatusCode::OK), (false, StatusCode::NOT_FOUND)] {
            let calls = Arc::new(AtomicUsize::new(0));
            let service: Arc<dyn ModelsService> = Arc::new(DiscoveryGateModels {
                allow,
                calls: Arc::clone(&calls),
            });
            let response = mounted_api_token_call(
                mounted_api_token_router_for_with_discovery(dashboard_user(7, 1, 1), Some(service)),
                "POST",
                "/api/token/",
                Some("{"),
                false,
            )
            .await?;
            assert_eq!(response.status(), expected_status, "allow={allow}");
            assert_eq!(calls.load(Ordering::Relaxed), 1, "allow={allow}");
        }
        Ok(())
    }

    #[tokio::test]
    async fn current_go_api_token_policy_hides_anonymous_and_unactivated_users() -> TestResult {
        let calls = Arc::new(AtomicUsize::new(0));
        let service: Arc<dyn ModelsService> = Arc::new(DiscoveryGateModels {
            allow: false,
            calls: Arc::clone(&calls),
        });
        let anonymous =
            mounted_api_token_call_without_credential(mounted_api_token_router_for_with_discovery(
                dashboard_user(7, 1, 1),
                Some(Arc::clone(&service)),
            ))
            .await?;
        assert_eq!(anonymous.status(), StatusCode::NOT_FOUND);
        assert_eq!(calls.load(Ordering::Relaxed), 0);

        let unactivated = mounted_api_token_call(
            mounted_api_token_router_for_with_discovery(dashboard_user(7, 1, 1), Some(service)),
            "GET",
            "/api/token/",
            None,
            false,
        )
        .await?;
        assert_eq!(unactivated.status(), StatusCode::NOT_FOUND);
        assert_eq!(calls.load(Ordering::Relaxed), 1);
        Ok(())
    }

    #[tokio::test]
    async fn current_go_api_token_policy_hides_discovery_lookup_failure() -> TestResult {
        let response = mounted_api_token_call(
            mounted_api_token_router_for_with_discovery(
                dashboard_user(7, 1, 1),
                Some(Arc::new(FailingDiscoveryModels)),
            ),
            "GET",
            "/api/token/",
            None,
            false,
        )
        .await?;
        assert_eq!(response.status(), StatusCode::NOT_FOUND);
        let body: Value =
            serde_json::from_slice(&axum::body::to_bytes(response.into_body(), usize::MAX).await?)?;
        assert_eq!(body, serde_json::json!({"message": "Not Found"}));
        Ok(())
    }

    #[tokio::test]
    async fn current_go_api_token_policy_preserves_disabled_credential_401() -> TestResult {
        let calls = Arc::new(AtomicUsize::new(0));
        let response = mounted_api_token_call(
            mounted_api_token_router_for_with_discovery(
                dashboard_user(7, 1, 2),
                Some(Arc::new(DiscoveryGateModels { allow: true, calls })),
            ),
            "GET",
            "/api/token/",
            None,
            false,
        )
        .await?;
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        let body: Value =
            serde_json::from_slice(&axum::body::to_bytes(response.into_body(), usize::MAX).await?)?;
        assert_eq!(body["code"], "AUTH_USER_DISABLED");
        Ok(())
    }

    #[tokio::test]
    async fn current_go_api_token_policy_exposes_user_auth_only_after_discovery() -> TestResult {
        for (user, expected_status, expected_code) in [
            (
                dashboard_user(7, 0, 1),
                StatusCode::FORBIDDEN,
                "AUTH_INSUFFICIENT_PRIVILEGE",
            ),
            (
                dashboard_user(7, 9, 1),
                StatusCode::UNAUTHORIZED,
                "AUTH_USER_INVALID",
            ),
            (
                dashboard_user(0, 1, 1),
                StatusCode::UNAUTHORIZED,
                "AUTH_USER_INVALID",
            ),
        ] {
            let response = mounted_api_token_call(
                mounted_api_token_router_for_with_discovery(
                    user,
                    Some(Arc::new(DiscoveryGateModels {
                        allow: true,
                        calls: Arc::new(AtomicUsize::new(0)),
                    })),
                ),
                "GET",
                "/api/token/",
                None,
                false,
            )
            .await?;
            assert_eq!(response.status(), expected_status, "{expected_code}");
            let body: Value = serde_json::from_slice(
                &axum::body::to_bytes(response.into_body(), usize::MAX).await?,
            )?;
            assert_eq!(body["code"], expected_code);
            assert_eq!(body["success"], false);
        }
        Ok(())
    }

    #[tokio::test]
    async fn frozen_api_token_parity_path_does_not_query_model_discovery_policy() -> TestResult {
        let calls = Arc::new(AtomicUsize::new(0));
        let service: Arc<dyn ModelsService> = Arc::new(DiscoveryGateModels {
            allow: false,
            calls: Arc::clone(&calls),
        });
        let response = mounted_api_token_call(
            mounted_api_token_router_for_with_services(dashboard_user(7, 1, 1), None, service),
            "POST",
            "/api/token/",
            Some("{"),
            false,
        )
        .await?;
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(calls.load(Ordering::Relaxed), 0);
        Ok(())
    }

    #[tokio::test]
    async fn mounted_api_token_routes_reject_low_role_and_invalid_dashboard_users() -> TestResult {
        let mut blank_username = dashboard_user(7, 1, 1);
        blank_username.username.clear();
        for (user, expected_status, expected_code) in [
            (
                dashboard_user(7, 0, 1),
                StatusCode::FORBIDDEN,
                "AUTH_INSUFFICIENT_PRIVILEGE",
            ),
            (
                dashboard_user(0, 1, 1),
                StatusCode::UNAUTHORIZED,
                "AUTH_USER_INVALID",
            ),
            (
                blank_username,
                StatusCode::UNAUTHORIZED,
                "AUTH_USER_INVALID",
            ),
        ] {
            let response = mounted_api_token_call(
                mounted_api_token_router_for(user),
                "GET",
                "/api/token/",
                None,
                false,
            )
            .await?;
            assert_eq!(response.status(), expected_status);
            let body: Value = serde_json::from_slice(
                &axum::body::to_bytes(response.into_body(), usize::MAX).await?,
            )?;
            assert_eq!(body["code"], expected_code);
            assert_eq!(body["success"], false);
        }
        Ok(())
    }

    #[tokio::test]
    async fn mounted_api_token_routes_validate_dashboard_users_in_go_order() -> TestResult {
        for (user, expected_status, expected_code) in [
            (
                dashboard_user(7, 2, 2),
                StatusCode::UNAUTHORIZED,
                "AUTH_USER_DISABLED",
            ),
            (
                dashboard_user(7, 0, 1),
                StatusCode::FORBIDDEN,
                "AUTH_INSUFFICIENT_PRIVILEGE",
            ),
            (
                dashboard_user(7, 2, 1),
                StatusCode::UNAUTHORIZED,
                "AUTH_USER_INVALID",
            ),
        ] {
            let response = mounted_api_token_call(
                mounted_api_token_router_for(user),
                "GET",
                "/api/token/",
                None,
                false,
            )
            .await?;
            assert_eq!(response.status(), expected_status);
            let body: Value = serde_json::from_slice(
                &axum::body::to_bytes(response.into_body(), usize::MAX).await?,
            )?;
            assert_eq!(body["code"], expected_code);
            assert_eq!(body["success"], false);
        }
        Ok(())
    }

    #[tokio::test]
    async fn mounted_api_token_routes_reject_each_noncanonical_dashboard_role() -> TestResult {
        for role in [2, 9, 11] {
            let response = mounted_api_token_call(
                mounted_api_token_router_for(dashboard_user(7, role, 1)),
                "GET",
                "/api/token/",
                None,
                false,
            )
            .await?;
            assert_eq!(response.status(), StatusCode::UNAUTHORIZED, "role {role}");
            let body: Value = serde_json::from_slice(
                &axum::body::to_bytes(response.into_body(), usize::MAX).await?,
            )?;
            assert_eq!(body["code"], "AUTH_USER_INVALID", "role {role}");
            assert_eq!(body["success"], false, "role {role}");
        }
        Ok(())
    }

    #[tokio::test]
    async fn mounted_api_token_routes_keep_root_dashboard_role_valid() -> TestResult {
        let response = mounted_api_token_call(
            mounted_api_token_router_for(dashboard_user(7, 100, 1)),
            "POST",
            "/api/token/",
            Some("{"),
            false,
        )
        .await?;
        assert_eq!(response.status(), StatusCode::OK);
        let body: Value =
            serde_json::from_slice(&axum::body::to_bytes(response.into_body(), usize::MAX).await?)?;
        assert_eq!(body["success"], false);
        assert!(body.get("code").is_none());
        Ok(())
    }

    #[tokio::test]
    async fn mounted_api_token_routes_preserve_invalid_dashboard_role_errors() -> TestResult {
        for accept_language in ["en-US", "zh-CN", "zh-TW"] {
            let response = mounted_api_token_call_with_locale(
                mounted_api_token_router_for(dashboard_user(7, 9, 1)),
                "GET",
                "/api/token/",
                None,
                false,
                Some(accept_language),
            )
            .await?;
            assert_eq!(
                response.status(),
                StatusCode::UNAUTHORIZED,
                "{accept_language}"
            );
            let body: Value = serde_json::from_slice(
                &axum::body::to_bytes(response.into_body(), usize::MAX).await?,
            )?;
            assert_eq!(body["code"], "AUTH_USER_INVALID", "{accept_language}");
            assert_eq!(body["success"], false, "{accept_language}");
        }
        Ok(())
    }

    #[tokio::test]
    async fn mounted_api_token_routes_do_not_trust_a_forged_principal_extension() -> TestResult {
        let router = mounted_api_token_router_for(dashboard_user(7, 100, 1))?;
        let mut request = Request::builder()
            .method("GET")
            .uri("/api/token/")
            .body(Body::empty())?;
        request
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));
        request.extensions_mut().insert(ApiTokenPrincipal {
            user_id: 999,
            role: 10,
            preferred_language: None,
        });
        let response = router.oneshot(request).await?;
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        let body: Value =
            serde_json::from_slice(&axum::body::to_bytes(response.into_body(), usize::MAX).await?)?;
        assert_eq!(body["code"], "AUTH_UNAUTHORIZED");
        assert_eq!(body["success"], false);
        Ok(())
    }

    #[tokio::test]
    async fn mounted_api_token_malformed_json_and_query_use_legacy_success_envelopes() -> TestResult
    {
        for (method, uri, body) in [
            ("POST", "/api/token/", Some("{")),
            ("GET", "/api/token/?p=not-an-integer", None),
        ] {
            let response = mounted_api_token_call(
                mounted_api_token_router_for(dashboard_user(7, 1, 1)),
                method,
                uri,
                body,
                false,
            )
            .await?;
            assert_eq!(response.status(), StatusCode::OK, "{method} {uri}");
            let body: Value = serde_json::from_slice(
                &axum::body::to_bytes(response.into_body(), usize::MAX).await?,
            )?;
            assert_eq!(body["success"], false, "{method} {uri}");
        }
        Ok(())
    }

    #[tokio::test]
    async fn mounted_api_token_validation_prefers_user_setting_language() -> TestResult {
        let mut user = dashboard_user(7, 1, 1);
        user.setting = r#"{"language":"zh-TW"}"#.to_owned();
        let response = mounted_api_token_call_with_locale(
            mounted_api_token_router_for(user),
            "POST",
            "/api/token/batch",
            Some(r#"{"ids":"1"}"#),
            false,
            Some("en-US"),
        )
        .await?;
        assert_eq!(response.status(), StatusCode::OK);
        let body: Value =
            serde_json::from_slice(&axum::body::to_bytes(response.into_body(), usize::MAX).await?)?;
        assert_eq!(body["message"], "無效的參數");
        Ok(())
    }

    #[derive(Clone, Copy)]
    enum MockLimitMode {
        Reject(u64),
        Fail,
    }

    struct MockRateLimiter {
        mode: MockLimitMode,
        client_ips: Mutex<Vec<String>>,
    }

    #[async_trait]
    impl GlobalApiRateLimiter for AllowAllRateLimiter {
        async fn check(&self, _client_ip: &str) -> Result<RateLimitOutcome, RateLimitError> {
            Ok(RateLimitOutcome::Allowed)
        }
    }

    #[async_trait]
    impl GlobalApiRateLimiter for MockRateLimiter {
        async fn check(&self, client_ip: &str) -> Result<RateLimitOutcome, RateLimitError> {
            self.client_ips
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner())
                .push(client_ip.to_owned());
            match self.mode {
                MockLimitMode::Reject(retry_after_seconds) => Ok(RateLimitOutcome::Rejected {
                    retry_after_seconds: Some(retry_after_seconds),
                }),
                MockLimitMode::Fail => Err(RateLimitError),
            }
        }
    }

    #[async_trait]
    impl PublicContentCache for MissingCache {
        async fn get(
            &self,
            _kind: PublicContentKind,
        ) -> Result<Option<String>, PublicContentCacheError> {
            Ok(None)
        }
    }

    #[async_trait]
    impl PublicContentRepository for MockContentRepository {
        async fn get(
            &self,
            _kind: PublicContentKind,
        ) -> Result<Option<String>, PublicContentError> {
            Ok(self.0.clone())
        }
    }

    fn state(failing: Option<&'static str>) -> TestResult<AppState> {
        state_with_rate_limiter(failing, Arc::new(AllowAllRateLimiter))
    }

    fn state_with_rate_limiter(
        failing: Option<&'static str>,
        global_api_rate_limiter: Arc<dyn GlobalApiRateLimiter>,
    ) -> TestResult<AppState> {
        state_with_rate_limiter_and_policy(
            failing,
            global_api_rate_limiter,
            ValkeyReadinessPolicy::RequiredForRateLimiting,
        )
    }

    fn state_with_rate_limiter_and_policy(
        failing: Option<&'static str>,
        global_api_rate_limiter: Arc<dyn GlobalApiRateLimiter>,
        valkey_readiness_policy: ValkeyReadinessPolicy,
    ) -> TestResult<AppState> {
        Ok(AppState {
            readiness: Arc::new(MockProbe(failing)),
            valkey_readiness_policy,
            global_api_rate_limiter,
            public_content: Arc::new(PublicContentService::new(
                Arc::new(MockContentRepository(Some("configured content".to_owned()))),
                Arc::new(MissingCache),
                std::time::Duration::from_secs(1),
            )),
            status: StatusHttpState::new(Arc::new(MockStatusRepository), "v0.0.0", 1_700_000_000),
            slot: "blue".to_owned(),
            runtime: RuntimeState::default(),
            trusted_proxies: TrustedProxyPolicy::Explicit(vec!["127.0.0.0/8".parse()?]),
        })
    }

    #[async_trait]
    impl ReadinessProbe for MockProbe {
        async fn postgres(&self) -> Result<(), ProbeError> {
            self.result("postgres")
        }
        async fn valkey(&self) -> Result<(), ProbeError> {
            self.result("valkey")
        }
        async fn schema_compatible(&self) -> Result<(), ProbeError> {
            self.result("schema")
        }
    }

    impl MockProbe {
        fn result(&self, dependency: &'static str) -> Result<(), ProbeError> {
            if self.0 == Some(dependency) {
                Err(ProbeError { dependency })
            } else {
                Ok(())
            }
        }
    }

    async fn call(
        method: &str,
        uri: &str,
        client_id: Option<&str>,
        failing: Option<&'static str>,
    ) -> TestResult<(StatusCode, String, Value)> {
        call_with_policy(
            method,
            uri,
            client_id,
            failing,
            ValkeyReadinessPolicy::RequiredForRateLimiting,
        )
        .await
    }

    async fn call_with_policy(
        method: &str,
        uri: &str,
        client_id: Option<&str>,
        failing: Option<&'static str>,
        valkey_readiness_policy: ValkeyReadinessPolicy,
    ) -> TestResult<(StatusCode, String, Value)> {
        let mut builder = Request::builder().method(method).uri(uri);
        if let Some(value) = client_id {
            builder = builder.header("x-request-id", value);
        }
        let mut request = builder.body(Body::empty())?;
        request
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));
        let response = router(state_with_rate_limiter_and_policy(
            failing,
            Arc::new(AllowAllRateLimiter),
            valkey_readiness_policy,
        )?)
        .oneshot(request)
        .await?;
        let status = response.status();
        let id = response
            .headers()
            .get("x-request-id")
            .ok_or_else(|| test_error("server id exists"))?
            .to_str()?
            .to_owned();
        let bytes = axum::body::to_bytes(response.into_body(), usize::MAX).await?;
        let body = serde_json::from_slice(&bytes)?;
        Ok((status, id, body))
    }

    async fn auth_call(
        method: &str,
        uri: &str,
        body: Option<&str>,
    ) -> TestResult<axum::response::Response> {
        auth_call_with_limiter(method, uri, body, Arc::new(AllowAllRateLimiter)).await
    }

    async fn auth_call_with_limiter(
        method: &str,
        uri: &str,
        body: Option<&str>,
        limiter: Arc<dyn GlobalApiRateLimiter>,
    ) -> TestResult<axum::response::Response> {
        let mut builder = Request::builder().method(method).uri(uri);
        if body.is_some() {
            builder = builder.header(header::CONTENT_TYPE, "application/json");
        }
        let mut request =
            builder.body(body.map_or_else(Body::empty, |value| Body::from(value.to_owned())))?;
        request
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));
        Ok(router(state_with_rate_limiter(None, limiter)?)
            .oneshot(request)
            .await?)
    }

    #[tokio::test]
    async fn global_rate_limit_should_fail_closed_before_auth_login() -> TestResult {
        let response = auth_call_with_limiter(
            "POST",
            "/api/user/login",
            Some(r#"{"username":"alice","password":"wrong"}"#),
            Arc::new(MockRateLimiter {
                mode: MockLimitMode::Reject(37),
                client_ips: Mutex::new(Vec::new()),
            }),
        )
        .await?;
        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(response.headers()[header::RETRY_AFTER], "37");
        assert!(response.headers().get(header::CONTENT_TYPE).is_none());
        assert_eq!(response.headers()["x-new-api-version"], "v0.0.0");
        let request_id = response.headers()["x-request-id"].to_str()?;
        assert_eq!(response.headers()["x-oneapi-request-id"], request_id);
        Uuid::parse_str(request_id)?;
        assert!(
            axum::body::to_bytes(response.into_body(), usize::MAX)
                .await?
                .is_empty()
        );
        Ok(())
    }

    #[tokio::test]
    async fn migration_candidate_root_should_rate_limit_before_candidate_execution() -> TestResult {
        let calls = Arc::new(AtomicUsize::new(0));
        let handler_calls = Arc::clone(&calls);
        let candidates = axum::Router::new().route_service(
            "/api/candidate-probe",
            tower::service_fn(move |_request: Request<Body>| {
                let handler_calls = Arc::clone(&handler_calls);
                async move {
                    handler_calls.fetch_add(1, Ordering::SeqCst);
                    Ok::<_, std::convert::Infallible>(axum::response::Response::new(Body::empty()))
                }
            }),
        );
        let limiter = Arc::new(MockRateLimiter {
            mode: MockLimitMode::Reject(19),
            client_ips: Mutex::new(Vec::new()),
        });
        let state = state_with_rate_limiter(None, limiter.clone())?;
        let app = finalize_listener(migration_candidate_test_surface(&state, candidates), state);
        let mut request = Request::get("/api/candidate-probe").body(Body::empty())?;
        request
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));

        let response = app.oneshot(request).await?;

        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(response.headers()[header::RETRY_AFTER], "19");
        assert_eq!(response.headers()["x-new-api-version"], "v0.0.0");
        assert_eq!(calls.load(Ordering::SeqCst), 0);
        assert_eq!(
            limiter
                .client_ips
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner())
                .as_slice(),
            ["127.0.0.1"]
        );
        Ok(())
    }

    #[tokio::test]
    async fn public_extra_api_routes_use_one_global_limit_with_the_trusted_client_ip() -> TestResult
    {
        let limiter = Arc::new(MockRateLimiter {
            mode: MockLimitMode::Reject(19),
            client_ips: Mutex::new(Vec::new()),
        });
        let state = state_with_rate_limiter(None, limiter.clone())?;
        let public_routes = Router::new().route(
            "/api/test-public-rate-limit/{name}",
            get(|| async { StatusCode::NO_CONTENT }),
        );
        let app = finalize_listener(
            api_global_rate_limited_surface(&state, public_routes),
            state,
        );

        for uri in [
            "/api/test-public-rate-limit/uptime",
            "/api/test-public-rate-limit/agreement",
            "/api/test-public-rate-limit/privacy",
        ] {
            let mut request = Request::get(uri)
                .header("x-forwarded-for", "203.0.113.10, 127.0.0.2")
                .body(Body::empty())?;
            request
                .extensions_mut()
                .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));

            let response = app.clone().oneshot(request).await?;
            assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS, "{uri}");
            assert_eq!(response.headers()[header::RETRY_AFTER], "19", "{uri}");
        }

        assert_eq!(
            limiter
                .client_ips
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner())
                .as_slice(),
            ["203.0.113.10", "203.0.113.10", "203.0.113.10"]
        );
        Ok(())
    }

    #[tokio::test]
    async fn production_open_source_bounty_surface_is_rate_limited_and_public_get_remains_reachable()
    -> TestResult {
        let limiter = Arc::new(MockRateLimiter {
            mode: MockLimitMode::Reject(23),
            client_ips: Mutex::new(Vec::new()),
        });
        let state = state_with_rate_limiter(None, limiter.clone())?;
        let extra = api_global_rate_limited_surface(&state, open_source_bounty_test_surface()?);
        let app =
            router_with_api_token_and_extra(state, auth_state(), models_state(), None, Some(extra));

        let mut request = Request::get("/api/open-source-bounties/projects/not-an-id")
            .header("x-forwarded-for", "203.0.113.11, 127.0.0.2")
            .body(Body::empty())?;
        request
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));

        let response = app.oneshot(request).await?;
        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(response.headers()[header::RETRY_AFTER], "23");
        assert_eq!(
            limiter
                .client_ips
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner())
                .as_slice(),
            ["203.0.113.11"]
        );

        let state = state_with_rate_limiter(None, Arc::new(AllowAllRateLimiter))?;
        let extra = api_global_rate_limited_surface(&state, open_source_bounty_test_surface()?);
        let app =
            router_with_api_token_and_extra(state, auth_state(), models_state(), None, Some(extra));
        let mut request =
            Request::get("/api/open-source-bounties/projects/not-an-id").body(Body::empty())?;
        request
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));

        let response = app.oneshot(request).await?;
        assert_eq!(response.status(), StatusCode::OK);
        let body: Value =
            serde_json::from_slice(&axum::body::to_bytes(response.into_body(), usize::MAX).await?)?;
        assert_eq!(body["code"], "OPEN_SOURCE_BOUNTY_INVALID_ID");
        Ok(())
    }

    #[tokio::test]
    async fn migration_candidate_token_preflight_bypasses_rejecting_global_limiter() -> TestResult {
        let limiter = Arc::new(MockRateLimiter {
            mode: MockLimitMode::Reject(19),
            client_ips: Mutex::new(Vec::new()),
        });
        let candidates = Router::new().route(
            "/api/usage/token/",
            axum::routing::options(|| async { StatusCode::NO_CONTENT }),
        );
        let state = state_with_rate_limiter(None, limiter.clone())?;
        let app = finalize_listener(migration_candidate_test_surface(&state, candidates), state);
        let mut request = Request::builder()
            .method("OPTIONS")
            .uri("/api/usage/token/")
            .header(header::ORIGIN, "https://browser.example")
            .body(Body::empty())?;
        request
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));

        let response = app.oneshot(request).await?;

        assert_eq!(response.status(), StatusCode::NO_CONTENT);
        assert!(
            limiter
                .client_ips
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner())
                .is_empty()
        );
        Ok(())
    }

    async fn extra_probe(request: Request<Body>) -> axum::response::Response {
        let Some(request_id) = request
            .extensions()
            .get::<super::ServerRequestId>()
            .map(|request_id| request_id.0.clone())
        else {
            return StatusCode::INTERNAL_SERVER_ERROR.into_response();
        };
        let Ok(request_id_header) = request_id.parse() else {
            return StatusCode::INTERNAL_SERVER_ERROR.into_response();
        };
        let mut response = StatusCode::NO_CONTENT.into_response();
        response
            .headers_mut()
            .insert("x-extra-seen-request-id", request_id_header);
        response
    }

    #[tokio::test]
    async fn extra_surface_shares_one_listener_boundary_and_fallback() -> TestResult {
        let extra = Router::new().route("/api/extra-probe", get(extra_probe));
        let app = router_with_api_token_and_extra(
            state(None)?,
            auth_state(),
            models_state(),
            None,
            Some(extra),
        );

        let mut request = Request::get("/api/extra-probe")
            .header(REQUEST_ID_HEADER, "attacker-controlled")
            .body(Body::empty())?;
        request
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));
        let response = app.clone().oneshot(request).await?;
        assert_eq!(response.status(), StatusCode::NO_CONTENT);
        assert_eq!(
            response.headers()["x-extra-seen-request-id"],
            response.headers()[REQUEST_ID_HEADER],
            "the extra handler and response observe the same single boundary ID"
        );

        let wrong_method = app
            .clone()
            .oneshot(Request::post("/api/extra-probe").body(Body::empty())?)
            .await?;
        assert_eq!(wrong_method.status(), StatusCode::NOT_FOUND);

        let backend_miss = app
            .oneshot(Request::get("/api/extra-missing").body(Body::empty())?)
            .await?;
        assert_eq!(backend_miss.status(), StatusCode::NOT_FOUND);
        let backend_miss_body = axum::body::to_bytes(backend_miss.into_body(), usize::MAX).await?;
        let backend_miss: Value = serde_json::from_slice(&backend_miss_body)?;
        assert_eq!(backend_miss, serde_json::json!({"message": "Not Found"}));
        Ok(())
    }

    #[tokio::test]
    async fn extra_surface_can_supply_a_complementary_method_without_route_conflict() -> TestResult
    {
        let extra = Router::new().route("/livez", axum::routing::post(StatusCode::NO_CONTENT));
        let app = router_with_api_token_and_extra(
            state(None)?,
            auth_state(),
            models_state(),
            None,
            Some(extra),
        );
        for (method, expected) in [("GET", StatusCode::OK), ("POST", StatusCode::NO_CONTENT)] {
            let response = app
                .clone()
                .oneshot(
                    Request::builder()
                        .method(method)
                        .uri("/livez")
                        .body(Body::empty())?,
                )
                .await?;
            assert_eq!(response.status(), expected, "{method} /livez");
        }
        Ok(())
    }

    #[tokio::test]
    async fn hidden_auth_surfaces_remain_not_found_before_global_rate_limit() -> TestResult {
        let limiter = Arc::new(MockRateLimiter {
            mode: MockLimitMode::Reject(37),
            client_ips: Mutex::new(Vec::new()),
        });
        for (method, path) in [("POST", "/api/user/login/2fa"), ("GET", "/api/user/token")] {
            let response = auth_call_with_limiter(method, path, None, limiter.clone()).await?;
            assert_eq!(response.status(), StatusCode::NOT_FOUND, "{method} {path}");
        }
        assert!(
            limiter
                .client_ips
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner())
                .is_empty()
        );

        let (status, _, body) = call("GET", "/api/user/token", None, None).await?;
        assert_eq!(status, StatusCode::NOT_FOUND);
        assert_eq!(
            body,
            serde_json::json!({"message": "Not Found"}),
            "listener-wide concealment keeps the current Go root 404 envelope"
        );
        Ok(())
    }

    #[tokio::test]
    async fn boundary_should_replace_untrusted_request_id_and_echo_server_id() -> TestResult {
        let mut request = Request::get("/missing")
            .header(REQUEST_ID_HEADER, "attacker-controlled")
            .body(Body::empty())?;
        request
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));
        let response = router(state(None)?).oneshot(request).await?;
        let id = response.headers()[REQUEST_ID_HEADER].to_str()?.to_owned();
        assert_ne!(id, "attacker-controlled");
        assert_eq!(response.headers()[LEGACY_REQUEST_ID_HEADER], id);
        assert_eq!(response.headers()[LEGACY_VERSION_HEADER], "v0.0.0");
        let body: Value =
            serde_json::from_slice(&axum::body::to_bytes(response.into_body(), usize::MAX).await?)?;
        assert_eq!(body, serde_json::json!({"message": "Not Found"}));
        assert!(body.get("request_id").is_none());
        Ok(())
    }

    #[tokio::test]
    async fn root_boundary_should_share_one_request_id_with_relay_and_provider_error_body()
    -> TestResult {
        let request_ids = Arc::new(Mutex::new(Vec::new()));
        let app = router_with_api_token_and_extra(
            state(None)?,
            auth_state(),
            models_state(),
            None,
            Some(relay_router(RelayHttpState::new(Arc::new(
                RootRelayBackend {
                    request_ids: Arc::clone(&request_ids),
                },
            )))),
        );
        let mut request = Request::post("/v1/messages")
            .header("x-request-id", "attacker-controlled")
            .header("x-api-key", "root-relay-token")
            .header(header::CONTENT_TYPE, "application/json")
            .body(Body::from(r#"{"model":"claude-test"}"#))?;
        request
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));

        let response = app.oneshot(request).await?;
        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "application/json; charset=utf-8"
        );
        let request_id = response.headers()[REQUEST_ID_HEADER].to_str()?.to_owned();
        Uuid::parse_str(&request_id)?;
        assert_ne!(request_id, "attacker-controlled");
        assert_eq!(response.headers()[LEGACY_REQUEST_ID_HEADER], request_id);
        let body: Value =
            serde_json::from_slice(&axum::body::to_bytes(response.into_body(), usize::MAX).await?)?;
        assert_eq!(
            body,
            serde_json::json!({
                "error": {
                    "message": "provider rejected request",
                    "request_id": request_id,
                }
            })
        );
        assert_eq!(
            request_ids
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner())
                .as_slice(),
            [request_id.as_str()]
        );
        Ok(())
    }

    #[tokio::test]
    async fn fallback_and_wrong_method_share_the_go_not_found_contract() -> TestResult {
        for (method, uri) in [
            ("GET", "/missing"),
            ("POST", "/livez"),
            ("GET", "/api/user/login"),
        ] {
            let (status, _, body) = call(method, uri, None, None).await?;
            assert_eq!(status, StatusCode::NOT_FOUND, "{method} {uri}");
            assert_eq!(
                body,
                serde_json::json!({"message": "Not Found"}),
                "{method} {uri}"
            );
        }
        Ok(())
    }

    #[tokio::test]
    async fn non_fallback_errors_keep_the_shared_json_envelope() -> TestResult {
        for (method, uri, failing, expected) in [("GET", "/readyz", Some("postgres"), 503)] {
            let (status, id, body) = call(method, uri, None, failing).await?;
            assert_eq!(status.as_u16(), expected);
            assert_eq!(body["request_id"], id);
            assert!(body["error"]["code"].is_string());
        }
        Ok(())
    }

    #[tokio::test]
    async fn wrong_method_on_a_mounted_auth_route_uses_the_go_404_envelope() -> TestResult {
        let response = auth_call("GET", "/api/user/login", None).await?;
        assert_eq!(response.status(), StatusCode::NOT_FOUND);
        let body = axum::body::to_bytes(response.into_body(), usize::MAX).await?;
        let body: Value = serde_json::from_slice(&body)?;
        assert_eq!(body, serde_json::json!({"message": "Not Found"}));
        Ok(())
    }

    #[tokio::test]
    async fn wrong_method_on_a_mounted_models_route_uses_the_go_404_envelope() -> TestResult {
        let (status, _, body) = call("POST", "/v1/models", None, None).await?;
        assert_eq!(status, StatusCode::NOT_FOUND);
        assert_eq!(body, serde_json::json!({"message": "Not Found"}));
        Ok(())
    }

    #[tokio::test]
    async fn models_cors_matches_legacy_preflight_and_actual_response_headers() -> TestResult {
        for path in ["/v1/models", "/v1beta/models", "/v1beta/openai/models"] {
            let mut preflight = Request::builder()
                .method("OPTIONS")
                .uri(path)
                .header(header::ORIGIN, "https://browser.example")
                .header("access-control-request-method", "GET")
                .body(Body::empty())?;
            preflight
                .extensions_mut()
                .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));
            let preflight = router(state(None)?).oneshot(preflight).await?;
            assert_eq!(preflight.status(), StatusCode::NO_CONTENT, "{path}");
            assert_eq!(
                preflight.headers()["access-control-allow-origin"],
                "*",
                "{path}"
            );
            assert_eq!(
                preflight.headers()["access-control-allow-credentials"],
                "true",
                "{path}"
            );
            assert_eq!(
                preflight.headers()["access-control-allow-methods"],
                "GET,POST,PUT,DELETE,OPTIONS",
                "{path}"
            );
            assert_eq!(
                preflight.headers()["access-control-allow-headers"],
                "*",
                "{path}"
            );
            assert_eq!(
                preflight.headers()["access-control-max-age"],
                "43200",
                "{path}"
            );
        }

        let mut actual = Request::builder()
            .method("GET")
            .uri("/v1/models")
            .header(header::ORIGIN, "https://browser.example")
            .body(Body::empty())?;
        actual
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));
        let actual = router(state(None)?).oneshot(actual).await?;
        assert_eq!(actual.headers()["access-control-allow-origin"], "*");
        assert_eq!(actual.headers()["access-control-allow-credentials"], "true");
        assert!(actual.headers().get("access-control-max-age").is_none());

        for path in ["/api/user/login", "/api/status", "/v1/models/not-an-alias"] {
            let mut non_models = Request::builder()
                .method("OPTIONS")
                .uri(path)
                .header(header::ORIGIN, "https://browser.example")
                .header("access-control-request-method", "GET")
                .body(Body::empty())?;
            non_models
                .extensions_mut()
                .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));
            let response = router(state(None)?).oneshot(non_models).await?;
            assert!(
                response
                    .headers()
                    .get("access-control-allow-origin")
                    .is_none(),
                "CORS must not leak to {path}"
            );
            assert!(
                response.headers().get("access-control-max-age").is_none(),
                "preflight cache policy must not leak to {path}"
            );
        }
        Ok(())
    }

    #[test]
    fn client_ip_ignores_spoofed_forwarding_from_an_untrusted_peer() -> TestResult {
        let mut request = Request::builder()
            .uri("/v1/models")
            .header("x-forwarded-for", "203.0.113.10")
            .body(Body::empty())?;
        request
            .extensions_mut()
            .insert(ConnectInfo("198.51.100.10:12345".parse::<SocketAddr>()?));
        let policy = TrustedProxyPolicy::Default(vec!["127.0.0.0/8".parse()?]);
        assert_eq!(
            canonical_client_ip(&request, &policy),
            Some("198.51.100.10".parse()?)
        );
        Ok(())
    }

    #[test]
    fn client_ip_uses_the_rightmost_untrusted_hop_from_a_trusted_cidr() -> TestResult {
        let mut request = Request::builder()
            .uri("/v1/models")
            .header("x-forwarded-for", "192.0.2.99, 203.0.113.10")
            .body(Body::empty())?;
        request
            .extensions_mut()
            .insert(ConnectInfo("172.20.0.2:12345".parse::<SocketAddr>()?));
        let policy = TrustedProxyPolicy::Explicit(vec!["172.16.0.0/12".parse()?]);
        assert_eq!(
            canonical_client_ip(&request, &policy),
            Some("203.0.113.10".parse()?)
        );
        Ok(())
    }

    #[test]
    fn client_ip_uses_a_valid_rightmost_hop_before_a_bad_left_segment() -> TestResult {
        let mut request = Request::builder()
            .uri("/v1/models")
            .header("x-forwarded-for", "bad, 203.0.113.10")
            .body(Body::empty())?;
        request
            .extensions_mut()
            .insert(ConnectInfo("172.20.0.2:12345".parse::<SocketAddr>()?));
        let policy = TrustedProxyPolicy::Explicit(vec!["172.16.0.0/12".parse()?]);
        assert_eq!(
            canonical_client_ip(&request, &policy),
            Some("203.0.113.10".parse()?)
        );
        Ok(())
    }

    #[test]
    fn client_ip_falls_back_after_a_bad_hop_before_a_usable_hop() -> TestResult {
        let mut request = Request::builder()
            .uri("/v1/models")
            .header("x-forwarded-for", "203.0.113.10, bad, 172.20.0.3")
            .body(Body::empty())?;
        request
            .extensions_mut()
            .insert(ConnectInfo("172.20.0.2:12345".parse::<SocketAddr>()?));
        let policy = TrustedProxyPolicy::Explicit(vec!["172.16.0.0/12".parse()?]);
        assert_eq!(
            canonical_client_ip(&request, &policy),
            Some("172.20.0.2".parse()?)
        );
        Ok(())
    }

    #[tokio::test]
    async fn listener_rate_limit_key_preserves_trimmed_raw_ipv6_text() -> TestResult {
        let limiter = Arc::new(MockRateLimiter {
            mode: MockLimitMode::Reject(1),
            client_ips: Mutex::new(Vec::new()),
        });
        let mut request = Request::get("/api/status")
            .header("x-forwarded-for", " 2001:0db8:0:0:0:0:0:1 , 172.20.0.3")
            .body(Body::empty())?;
        request
            .extensions_mut()
            .insert(ConnectInfo("172.20.0.2:12345".parse::<SocketAddr>()?));
        let mut state = state_with_rate_limiter(None, limiter.clone())?;
        state.trusted_proxies = TrustedProxyPolicy::Explicit(vec!["172.16.0.0/12".parse()?]);
        let response = router(state).oneshot(request).await?;

        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(
            limiter
                .client_ips
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner())
                .as_slice(),
            ["2001:0db8:0:0:0:0:0:1"]
        );
        Ok(())
    }

    #[tokio::test]
    async fn extractor_rejection_should_use_the_json_envelope() -> TestResult {
        let mut request = Request::builder()
            .method("POST")
            .uri("/_test/json")
            .header("content-type", "application/json")
            .body(Body::from("{"))?;
        request
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));
        let response = router(state(None)?).oneshot(request).await?;
        assert_eq!(response.status(), StatusCode::BAD_REQUEST);
        assert_eq!(response.headers()["content-type"], "application/json");
        Ok(())
    }

    #[tokio::test]
    async fn build_should_report_runtime_slot_identity() -> TestResult {
        let (_, _, body) = call("GET", "/_internal/build", None, None).await?;
        assert_eq!(body["slot"], "blue");
        Ok(())
    }

    #[tokio::test]
    async fn valkey_failure_should_reject_traffic_when_rate_limiting_is_enabled() -> TestResult {
        let (status, _, body) = call("GET", "/readyz", None, Some("valkey")).await?;
        assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(body["error"]["code"], "not_ready");
        Ok(())
    }

    #[tokio::test]
    async fn valkey_failure_should_degrade_when_rate_limiting_is_disabled() -> TestResult {
        let (status, _, body) = call_with_policy(
            "GET",
            "/readyz",
            None,
            Some("valkey"),
            ValkeyReadinessPolicy::OptionalCacheOnly,
        )
        .await?;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["status"], "degraded");
        Ok(())
    }

    #[tokio::test]
    async fn drain_should_finish_an_inflight_request_then_reject_new_work() -> TestResult {
        let runtime = RuntimeState::default();
        let mut draining_state = state(None)?;
        draining_state.runtime = runtime.clone();
        let service = router(draining_state);
        let mut held_request = Request::builder()
            .method("GET")
            .uri("/_test/hold")
            .body(Body::empty())?;
        held_request
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));

        let held = tokio::spawn(async move {
            match service.oneshot(held_request).await {
                Ok(response) => response,
                Err(error) => match error {},
            }
        });
        for _ in 0..10 {
            if runtime.inflight() == 1 {
                break;
            }
            tokio::task::yield_now().await;
        }
        assert_eq!(runtime.inflight(), 1, "the original request is draining");

        runtime.begin_drain();
        let response = router({
            let mut state = state(None)?;
            state.runtime = runtime.clone();
            state
        })
        .oneshot(
            Request::builder()
                .method("GET")
                .uri("/readyz")
                .body(Body::empty())?,
        )
        .await?;
        assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);

        let completed = tokio::time::timeout(std::time::Duration::from_secs(1), held).await??;
        assert_eq!(completed.status(), StatusCode::NO_CONTENT);
        assert_eq!(runtime.inflight(), 0, "drain has no remaining requests");
        Ok(())
    }

    #[tokio::test]
    async fn public_content_should_match_the_go_success_envelope() -> TestResult {
        for uri in ["/api/notice", "/api/about", "/api/home_page_content"] {
            let mut request = Request::builder()
                .method("GET")
                .uri(uri)
                .body(Body::empty())?;
            request
                .extensions_mut()
                .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));
            let response = router(state(None)?).oneshot(request).await?;
            let status = response.status();
            assert_eq!(status, StatusCode::OK);
            assert_eq!(
                response.headers()[header::CONTENT_TYPE],
                "application/json; charset=utf-8"
            );
            assert_eq!(response.headers()["x-new-api-version"], "v0.0.0");
            assert_eq!(
                response.headers()["x-oneapi-request-id"],
                response.headers()["x-request-id"]
            );
            let bytes = axum::body::to_bytes(response.into_body(), usize::MAX).await?;
            let body: Value = serde_json::from_slice(&bytes)?;
            assert_eq!(body["success"], true);
            assert_eq!(body["message"], "");
            assert_eq!(body["data"], "configured content");
        }
        Ok(())
    }

    #[tokio::test]
    async fn root_router_should_expose_only_the_four_p0_auth_routes() -> TestResult {
        for (method, path, body, expected) in [
            (
                "POST",
                "/api/user/login",
                Some(r#"{"username":"alice","password":"wrong"}"#),
                StatusCode::OK,
            ),
            (
                "POST",
                "/api/user/auth/refresh",
                Some("{}"),
                StatusCode::UNAUTHORIZED,
            ),
            ("GET", "/api/user/self", None, StatusCode::UNAUTHORIZED),
            ("POST", "/api/user/auth/logout", Some("{}"), StatusCode::OK),
        ] {
            let response = auth_call(method, path, body).await?;
            assert_eq!(response.status(), expected, "{method} {path}");
            assert_eq!(
                response.headers()[LEGACY_REQUEST_ID_HEADER],
                response.headers()[REQUEST_ID_HEADER],
                "{method} {path} must expose one server request id"
            );
        }

        for (method, path) in [("POST", "/api/user/login/2fa"), ("GET", "/api/user/token")] {
            let response = auth_call(method, path, None).await?;
            assert_eq!(
                response.status(),
                StatusCode::NOT_FOUND,
                "{method} {path} must remain outside production ownership"
            );
        }
        Ok(())
    }

    #[tokio::test]
    async fn status_should_match_the_current_go_shape_through_the_real_router() -> TestResult {
        let mut request = Request::builder()
            .method("GET")
            .uri("/api/status")
            .body(Body::empty())?;
        request
            .extensions_mut()
            .insert(ConnectInfo("127.0.0.1:12345".parse::<SocketAddr>()?));
        let response = router(state(None)?).oneshot(request).await?;
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "application/json; charset=utf-8"
        );
        assert_eq!(response.headers()["x-new-api-version"], "v0.0.0");
        assert_eq!(
            response.headers()["x-oneapi-request-id"],
            response.headers()["x-request-id"]
        );
        let body = axum::body::to_bytes(response.into_body(), usize::MAX).await?;
        let actual: Value = serde_json::from_slice(&body)?;
        assert_eq!(actual["success"], true);
        assert_eq!(actual["message"], "");
        assert_eq!(actual["data"]["version"], "v0.0.0");
        assert_eq!(actual["data"]["start_time"], 1_700_000_000_i64);
        assert_eq!(
            actual["data"]["docs_link"],
            "https://docs.newapi.pro/en/docs"
        );
        assert_eq!(actual["data"]["api_info_enabled"], true);
        assert_eq!(actual["data"]["api_info"], serde_json::json!([]));
        assert_eq!(actual["data"]["server_address"], "http://localhost:3000");
        assert_eq!(actual["data"]["passkey_origins"], "http://localhost:3000");
        assert_eq!(actual["data"]["passkey_rp_id"], "localhost:3000");
        assert!(actual["data"].get("turnstile_secret_key").is_none());
        assert!(actual["data"].get("client_secret").is_none());
        Ok(())
    }

    async fn limited_response(
        limiter: Arc<dyn GlobalApiRateLimiter>,
        peer: &str,
        real_ip: &str,
    ) -> TestResult<axum::response::Response> {
        let mut request = Request::builder()
            .method("GET")
            .uri("/api/notice")
            .header("x-real-ip", real_ip)
            .body(Body::empty())?;
        request
            .extensions_mut()
            .insert(ConnectInfo(peer.parse::<SocketAddr>()?));
        Ok(router(state_with_rate_limiter(None, limiter)?)
            .oneshot(request)
            .await?)
    }

    #[tokio::test]
    async fn rate_limit_should_match_go_empty_429_and_trust_only_loopback_proxy() -> TestResult {
        let limiter = Arc::new(MockRateLimiter {
            mode: MockLimitMode::Reject(37),
            client_ips: Mutex::new(Vec::new()),
        });
        let response = limited_response(limiter.clone(), "127.0.0.1:12345", "192.0.2.10").await?;
        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(response.headers()["retry-after"], "37");
        assert!(response.headers().get("content-type").is_none());
        assert_eq!(response.headers()["x-new-api-version"], "v0.0.0");
        assert_eq!(
            response.headers()["x-oneapi-request-id"],
            response.headers()["x-request-id"]
        );
        let body = axum::body::to_bytes(response.into_body(), usize::MAX).await?;
        assert!(body.is_empty());
        assert_eq!(
            limiter
                .client_ips
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner())
                .first()
                .ok_or_else(|| test_error("trusted limiter records one client IP"))?,
            "192.0.2.10"
        );

        let untrusted = Arc::new(MockRateLimiter {
            mode: MockLimitMode::Reject(37),
            client_ips: Mutex::new(Vec::new()),
        });
        let _ = limited_response(untrusted.clone(), "198.51.100.20:12345", "192.0.2.99").await?;
        assert_eq!(
            untrusted
                .client_ips
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner())
                .first()
                .ok_or_else(|| test_error("untrusted limiter records one client IP"))?,
            "198.51.100.20"
        );
        Ok(())
    }

    #[tokio::test]
    async fn rate_limit_backend_failure_should_match_go_empty_500() -> TestResult {
        let limiter = Arc::new(MockRateLimiter {
            mode: MockLimitMode::Fail,
            client_ips: Mutex::new(Vec::new()),
        });
        let response = auth_call_with_limiter(
            "POST",
            "/api/user/login",
            Some(r#"{"username":"alice","password":"wrong"}"#),
            limiter,
        )
        .await?;
        assert_eq!(response.status(), StatusCode::INTERNAL_SERVER_ERROR);
        assert!(response.headers().get("retry-after").is_none());
        assert!(response.headers().get("content-type").is_none());
        assert_eq!(response.headers()["x-new-api-version"], "v0.0.0");
        let request_id = response.headers()["x-request-id"].to_str()?;
        assert_eq!(response.headers()["x-oneapi-request-id"], request_id);
        Uuid::parse_str(request_id)?;
        let body = axum::body::to_bytes(response.into_body(), usize::MAX).await?;
        assert!(body.is_empty());
        Ok(())
    }
}
