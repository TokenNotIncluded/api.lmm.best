use std::{
    env,
    net::SocketAddr,
    sync::{Arc, Mutex},
    time::Duration,
};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{HeaderMap, Request, StatusCode},
};
use lmm_api_rs::{
    auth::{
        AuthBundle, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth,
        DashboardUser, LoginOutcome, LoginRequest, LogoutRequest, LogoutResult, RequestMetadata,
        TwoFactorLoginRequest,
    },
    migration_routes::system_config::{
        DashboardRootAuthorizer, ExchangeRateProvider, ProjectUpdateClient, SystemConfigAuthorizer,
        SystemConfigHttpState, SystemConfigIdentity, SystemConfigRuntimeWriter,
        WaffoPancakeGateway, system_config_router,
    },
};
use redis::AsyncCommands;
use secrecy::SecretString;
use serde_json::{Value, json};
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

struct Denied;

#[async_trait]
impl SystemConfigAuthorizer for Denied {
    async fn require_root_dashboard_session(
        &self,
        _: &HeaderMap,
    ) -> Result<SystemConfigIdentity, ()> {
        Err(())
    }
}

struct Root;

#[async_trait]
impl SystemConfigAuthorizer for Root {
    async fn require_root_dashboard_session(
        &self,
        _: &HeaderMap,
    ) -> Result<SystemConfigIdentity, ()> {
        Ok(SystemConfigIdentity { user_id: 7 })
    }
}

struct Update;

#[async_trait]
impl ProjectUpdateClient for Update {
    async fn latest_main_commit(&self) -> Result<Value, ()> {
        Ok(json!({
            "tag_name": "abcdef0",
            "name": "fix: control plane",
            "body": "fixture",
            "html_url": "https://github.com/LIghtJUNction/api.lmm.best/commit/abcdef0",
            "published_at": "2026-08-01T00:00:00Z"
        }))
    }
}

struct Pancake;

struct ExchangeRateProbe {
    calls: Mutex<Vec<String>>,
    result: Result<f64, ()>,
}

impl ExchangeRateProbe {
    fn succeeding(rate: f64) -> Self {
        Self {
            calls: Mutex::new(Vec::new()),
            result: Ok(rate),
        }
    }

    fn failing() -> Self {
        Self {
            calls: Mutex::new(Vec::new()),
            result: Err(()),
        }
    }

    fn calls(&self) -> Vec<String> {
        self.calls.lock().expect("exchange-rate calls").clone()
    }
}

#[async_trait]
impl ExchangeRateProvider for ExchangeRateProbe {
    async fn settlement_units_per_usd(&self, currency: &str) -> Result<f64, ()> {
        self.calls
            .lock()
            .expect("exchange-rate calls")
            .push(currency.to_owned());
        self.result
    }
}

struct CatalogProbe {
    catalog_calls: Mutex<Vec<(String, String)>>,
    fail_catalog: bool,
}

impl CatalogProbe {
    fn succeeding() -> Self {
        Self {
            catalog_calls: Mutex::new(Vec::new()),
            fail_catalog: false,
        }
    }

    fn failing() -> Self {
        Self {
            catalog_calls: Mutex::new(Vec::new()),
            fail_catalog: true,
        }
    }

    fn catalog_calls(&self) -> Vec<(String, String)> {
        self.catalog_calls.lock().expect("catalog calls").clone()
    }
}

struct OracleProbeRuntimeWriter;

#[async_trait]
impl SystemConfigRuntimeWriter for OracleProbeRuntimeWriter {
    async fn preflight(&self, changes: &[(String, String)]) -> Result<(), ()> {
        match changes {
            [(key, value)] if key == "SystemConfigOracleProbe" && value == "after" => Ok(()),
            _ => Err(()),
        }
    }

    async fn apply_committed(&self, changes: &[(String, String)]) -> Result<(), ()> {
        self.preflight(changes).await
    }
}

#[async_trait]
impl WaffoPancakeGateway for Pancake {
    async fn catalog(&self, _: &str, _: &str) -> Result<Value, ()> {
        Ok(json!({"stores": []}))
    }

    async fn create_pair(&self, _: &str, _: &str, _: &str) -> Result<Value, Value> {
        Ok(json!({}))
    }

    async fn create_product(
        &self,
        _: &str,
        _: &str,
        _: &str,
        _: &str,
        _: &str,
        _: &str,
    ) -> Result<Value, ()> {
        Ok(json!("product-fixture"))
    }
}

#[async_trait]
impl WaffoPancakeGateway for CatalogProbe {
    async fn catalog(&self, merchant_id: &str, private_key: &str) -> Result<Value, ()> {
        self.catalog_calls
            .lock()
            .expect("catalog calls")
            .push((merchant_id.to_owned(), private_key.to_owned()));
        if self.fail_catalog {
            Err(())
        } else {
            Ok(json!({"stores": []}))
        }
    }

    async fn create_pair(&self, _: &str, _: &str, _: &str) -> Result<Value, Value> {
        Err(json!({}))
    }

    async fn create_product(
        &self,
        _: &str,
        _: &str,
        _: &str,
        _: &str,
        _: &str,
        _: &str,
    ) -> Result<Value, ()> {
        Err(())
    }
}

struct Dashboard {
    role: i64,
}

async fn spawn_tcp_router(app: axum::Router) -> (String, tokio::task::JoinHandle<()>) {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("test listener");
    let address: SocketAddr = listener.local_addr().expect("listener address");
    let server = tokio::spawn(async move {
        axum::serve(listener, app.into_make_service())
            .await
            .expect("test server");
    });
    (format!("http://{address}"), server)
}

fn exchange_rate_app(
    authorizer: Arc<dyn SystemConfigAuthorizer>,
    provider: Arc<dyn ExchangeRateProvider>,
) -> axum::Router {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
        .expect("valid deferred PostgreSQL URL");
    let valkey = redis::Client::open("redis://127.0.0.1:1/").expect("valid deferred Valkey URL");
    system_config_router(
        SystemConfigHttpState::new(
            pool,
            valkey,
            authorizer,
            Arc::new(Update),
            Arc::new(Pancake),
        )
        .with_exchange_rate_provider(provider),
    )
}

async fn response_json(response: axum::response::Response) -> Value {
    serde_json::from_slice(
        &to_bytes(response.into_body(), 16 * 1024)
            .await
            .expect("bounded response body"),
    )
    .expect("JSON response")
}

#[tokio::test]
async fn exchange_rate_requires_root_auth_before_query_validation() {
    let provider = Arc::new(ExchangeRateProbe::succeeding(6.8));
    let response = exchange_rate_app(Arc::new(Denied), provider.clone())
        .oneshot(
            Request::builder()
                .uri("/api/option/exchange-rate?currency=CNY")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert!(provider.calls().is_empty());
}

#[tokio::test]
async fn exchange_rate_validates_iso_code_normalizes_case_and_bypasses_provider_for_usd() {
    let provider = Arc::new(ExchangeRateProbe::succeeding(6.8));
    let app = exchange_rate_app(Arc::new(Root), provider.clone());

    for uri in [
        "/api/option/exchange-rate",
        "/api/option/exchange-rate?currency=CN",
        "/api/option/exchange-rate?currency=C%24Y",
        "/api/option/exchange-rate?currency=CNY1",
    ] {
        let response = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(uri)
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(response.status(), StatusCode::BAD_REQUEST, "{uri}");
        assert_eq!(
            response_json(response).await,
            json!({"success": false, "message": "currency must be a three-letter ISO code"}),
            "{uri}"
        );
    }

    let usd = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/api/option/exchange-rate?currency=usd")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(usd.status(), StatusCode::OK);
    let usd_body = response_json(usd).await;
    assert_eq!(usd_body["success"], true);
    assert_eq!(usd_body["message"], "");
    assert_eq!(usd_body["data"]["base_currency"], "USD");
    assert_eq!(usd_body["data"]["quote_currency"], "USD");
    assert_eq!(usd_body["data"]["rate"], 1.0);
    assert_eq!(usd_body["data"]["provider"], "base");
    assert!(usd_body["data"]["fetched_at"].is_string());
    assert!(provider.calls().is_empty());

    let cny = app
        .oneshot(
            Request::builder()
                .uri("/api/option/exchange-rate?currency=cny")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(cny.status(), StatusCode::OK);
    let cny_body = response_json(cny).await;
    assert_eq!(cny_body["success"], true);
    assert_eq!(cny_body["data"]["base_currency"], "USD");
    assert_eq!(cny_body["data"]["quote_currency"], "CNY");
    assert_eq!(cny_body["data"]["rate"], 6.8);
    assert_eq!(cny_body["data"]["provider"], "pinned-providers");
    assert!(cny_body["data"]["fetched_at"].is_string());
    assert_eq!(provider.calls(), vec!["CNY"]);
}

#[tokio::test]
async fn exchange_rate_fails_closed_when_both_pinned_providers_are_unavailable() {
    let provider = Arc::new(ExchangeRateProbe::failing());
    let response = exchange_rate_app(Arc::new(Root), provider.clone())
        .oneshot(
            Request::builder()
                .uri("/api/option/exchange-rate?currency=CNY")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::BAD_GATEWAY);
    assert_eq!(
        response_json(response).await,
        json!({"success": false, "message": "failed to fetch the latest USD exchange rate"})
    );
    assert_eq!(provider.calls(), vec!["CNY"]);
}

#[tokio::test]
async fn option_route_anonymous_contract_is_frozen_over_real_tcp_with_locale() {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
        .expect("valid deferred PostgreSQL URL");
    let valkey = redis::Client::open("redis://127.0.0.1:1/").expect("valid deferred Valkey URL");
    let app = system_config_router(SystemConfigHttpState::new(
        pool,
        valkey,
        Arc::new(Denied),
        Arc::new(Update),
        Arc::new(Pancake),
    ));
    let (base_url, server) = spawn_tcp_router(app).await;

    let response = reqwest::Client::new()
        .get(format!("{base_url}/api/option/"))
        .header("accept-language", "zh-CN")
        .send()
        .await
        .expect("TCP response");

    assert_eq!(response.status(), reqwest::StatusCode::UNAUTHORIZED);
    assert_eq!(
        response.headers()[reqwest::header::CONTENT_TYPE],
        "application/json; charset=utf-8"
    );
    assert_eq!(
        response.json::<Value>().await.expect("JSON body"),
        json!({
            "success": false,
            "code": "AUTH_UNAUTHORIZED",
            "message": "无权进行此操作，未登录且未提供 access token"
        })
    );
    let malformed = reqwest::Client::new()
        .put(format!("{base_url}/api/option/"))
        .header("content-type", "application/json")
        .body("{")
        .send()
        .await
        .expect("TCP malformed response");
    assert_eq!(malformed.status(), reqwest::StatusCode::UNAUTHORIZED);
    assert_eq!(
        malformed.json::<Value>().await.expect("JSON body"),
        json!({
            "success": false,
            "code": "AUTH_UNAUTHORIZED",
            "message": "Unauthorized, not logged in and no access token provided"
        })
    );
    server.abort();
}

#[tokio::test]
async fn waffo_catalog_get_is_unavailable_and_never_forwards_query_credentials() {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
        .expect("valid deferred PostgreSQL URL");
    let valkey = redis::Client::open("redis://127.0.0.1:1/").expect("valid deferred Valkey URL");
    let pancake = Arc::new(CatalogProbe::succeeding());
    let app = system_config_router(SystemConfigHttpState::new(
        pool,
        valkey,
        Arc::new(Root),
        Arc::new(Update),
        pancake.clone(),
    ));
    let private_key = "url-private-key-must-not-be-read";

    let response = app
        .oneshot(
            Request::get(format!(
                "/api/option/waffo-pancake/catalog?merchant_id=url-merchant&private_key={private_key}&return_url=https%3A%2F%2Fexample.invalid"
            ))
            .body(Body::empty())
            .expect("GET request"),
        )
        .await
        .expect("GET response");

    assert_eq!(response.status(), StatusCode::METHOD_NOT_ALLOWED);
    assert!(pancake.catalog_calls().is_empty());
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert!(!String::from_utf8_lossy(&body).contains(private_key));
}

#[tokio::test]
async fn waffo_catalog_post_requires_root_before_parsing_credentials() {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
        .expect("valid deferred PostgreSQL URL");
    let valkey = redis::Client::open("redis://127.0.0.1:1/").expect("valid deferred Valkey URL");
    let pancake = Arc::new(CatalogProbe::succeeding());
    let app = system_config_router(SystemConfigHttpState::new(
        pool,
        valkey,
        Arc::new(Denied),
        Arc::new(Update),
        pancake.clone(),
    ));
    let private_key = "body-private-key-must-not-leak";

    let response = app
        .oneshot(
            Request::post("/api/option/waffo-pancake/catalog")
                .header("content-type", "application/json")
                .body(Body::from(format!(
                    r#"{{"merchant_id":"merchant","private_key":"{private_key}"}}"#
                )))
                .expect("POST request"),
        )
        .await
        .expect("POST response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert!(pancake.catalog_calls().is_empty());
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert!(!String::from_utf8_lossy(&body).contains(private_key));
}

#[tokio::test]
async fn waffo_catalog_post_uses_only_json_credentials_and_keeps_errors_redacted() {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
        .expect("valid deferred PostgreSQL URL");
    let valkey = redis::Client::open("redis://127.0.0.1:1/").expect("valid deferred Valkey URL");
    let pancake = Arc::new(CatalogProbe::failing());
    let app = system_config_router(SystemConfigHttpState::new(
        pool,
        valkey,
        Arc::new(Root),
        Arc::new(Update),
        pancake.clone(),
    ));
    let body_private_key = "body-private-key-must-not-leak";
    let query_private_key = "query-private-key-must-be-ignored";

    let response = app
        .oneshot(
            Request::post(format!(
                "/api/option/waffo-pancake/catalog?merchant_id=query-merchant&private_key={query_private_key}"
            ))
            .header("content-type", "application/json")
            .body(Body::from(format!(
                r#"{{"merchant_id":" body-merchant ","private_key":" {body_private_key} "}}"#
            )))
            .expect("POST request"),
        )
        .await
        .expect("POST response");

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        pancake.catalog_calls(),
        vec![("body-merchant".to_owned(), body_private_key.to_owned())]
    );
    let body = serde_json::from_slice::<Value>(
        &to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body"),
    )
    .expect("JSON response");
    assert_eq!(body, json!({"message":"error","data":"拉取目录失败"}));
    let serialized = body.to_string();
    assert!(!serialized.contains(body_private_key));
    assert!(!serialized.contains(query_private_key));
}

#[tokio::test]
async fn waffo_catalog_post_enforces_the_go_body_limit_after_root_auth() {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
        .expect("valid deferred PostgreSQL URL");
    let valkey = redis::Client::open("redis://127.0.0.1:1/").expect("valid deferred Valkey URL");
    let app = system_config_router(SystemConfigHttpState::new(
        pool,
        valkey,
        Arc::new(Root),
        Arc::new(Update),
        Arc::new(Pancake),
    ));

    let response = app
        .oneshot(
            Request::post("/api/option/waffo-pancake/catalog")
                .body(Body::from(vec![b'x'; (16 << 10) + 1]))
                .expect("oversized POST request"),
        )
        .await
        .expect("oversized POST response");

    assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
}

#[async_trait]
impl DashboardAuth for Dashboard {
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
            id: 7,
            username: "root".to_owned(),
            display_name: "Root".to_owned(),
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
async fn project_update_uses_the_injected_bounded_upstream_client() {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
        .expect("valid deferred PostgreSQL URL");
    let valkey = redis::Client::open("redis://127.0.0.1:1/").expect("valid deferred Valkey URL");
    let app = system_config_router(SystemConfigHttpState::new(
        pool,
        valkey,
        Arc::new(Root),
        Arc::new(Update),
        Arc::new(Pancake),
    ));

    let response = app
        .oneshot(
            Request::builder()
                .uri("/api/option/project-update")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    let body = serde_json::from_slice::<Value>(
        &to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body"),
    )
    .expect("JSON response");
    assert_eq!(body["success"], true);
    assert_eq!(body["data"]["tag_name"], "abcdef0");
}

#[tokio::test]
async fn malformed_protected_payloads_keep_the_frozen_legacy_envelopes() {
    let pool = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
        .expect("valid deferred PostgreSQL URL");
    let valkey = redis::Client::open("redis://127.0.0.1:1/").expect("valid deferred Valkey URL");
    let app = system_config_router(SystemConfigHttpState::new(
        pool,
        valkey,
        Arc::new(Root),
        Arc::new(Update),
        Arc::new(Pancake),
    ));

    for (uri, expected_status, expected_body) in [
        (
            "/api/option/",
            StatusCode::BAD_REQUEST,
            json!({"success":false,"message":"无效的参数"}),
        ),
        (
            "/api/option/payment_compliance",
            StatusCode::OK,
            json!({"success":false,"message":"参数错误"}),
        ),
        (
            "/api/option/waffo-pancake/catalog",
            StatusCode::OK,
            json!({"message":"error","data":"参数错误"}),
        ),
        (
            "/api/option/waffo-pancake/pair",
            StatusCode::OK,
            json!({"message":"error","data":"参数错误"}),
        ),
        (
            "/api/option/waffo-pancake/save",
            StatusCode::OK,
            json!({"message":"error","data":"参数错误"}),
        ),
        (
            "/api/option/waffo-pancake/subscription-product",
            StatusCode::OK,
            json!({"message":"error","data":"参数错误"}),
        ),
    ] {
        let response = app
            .clone()
            .oneshot(
                Request::builder()
                    .method(if uri == "/api/option/" { "PUT" } else { "POST" })
                    .uri(uri)
                    .header("content-type", "application/json")
                    .body(Body::from("{"))
                    .expect("malformed request"),
            )
            .await
            .expect("response");
        assert_eq!(response.status(), expected_status, "{uri}");
        let body = serde_json::from_slice::<Value>(
            &to_bytes(response.into_body(), usize::MAX)
                .await
                .expect("response body"),
        )
        .expect("JSON response");
        assert_eq!(body, expected_body, "{uri}");
    }
}

#[tokio::test]
async fn setup_route_rejects_body_above_configured_limit_before_database_access() {
    let pool = PgPoolOptions::new()
        .acquire_timeout(Duration::from_millis(10))
        .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
        .expect("valid deferred PostgreSQL URL");
    let valkey = redis::Client::open("redis://127.0.0.1:1/").expect("valid deferred Valkey URL");
    let app = system_config_router(
        SystemConfigHttpState::new(
            pool,
            valkey,
            Arc::new(Denied),
            Arc::new(Update),
            Arc::new(Pancake),
        )
        .with_anonymous_body_limit_bytes(16),
    );

    let response = app
        .oneshot(
            Request::post("/api/setup")
                .body(Body::from(vec![b'x'; 17]))
                .expect("oversized setup request"),
        )
        .await
        .expect("setup response");

    assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
}

#[tokio::test]
async fn dashboard_root_authorizer_accepts_only_a_valid_root_bearer_identity() {
    let headers = HeaderMap::from_iter([(
        axum::http::header::AUTHORIZATION,
        "Bearer server-validated-token".parse().expect("header"),
    )]);
    let identity = DashboardRootAuthorizer::new(Arc::new(Dashboard { role: 100 }))
        .require_root_dashboard_session(&headers)
        .await
        .expect("root dashboard identity");
    assert_eq!(identity.user_id, 7);
    assert!(
        DashboardRootAuthorizer::new(Arc::new(Dashboard { role: 10 }))
            .require_root_dashboard_session(&headers)
            .await
            .is_err()
    );
    assert!(
        DashboardRootAuthorizer::new(Arc::new(Dashboard { role: 100 }))
            .require_root_dashboard_session(&HeaderMap::new())
            .await
            .is_err()
    );
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; run with LMM_SYSTEM_CONFIG_TEST_DATABASE_URL and LMM_SYSTEM_CONFIG_TEST_VALKEY_URL"]
async fn option_write_invalidates_valkey_then_recovers_from_authoritative_postgres() {
    let database_url = env::var("LMM_SYSTEM_CONFIG_TEST_DATABASE_URL").expect(
        "LMM_SYSTEM_CONFIG_TEST_DATABASE_URL is required for the isolated PostgreSQL 18 harness",
    );
    let valkey_url = env::var("LMM_SYSTEM_CONFIG_TEST_VALKEY_URL")
        .expect("LMM_SYSTEM_CONFIG_TEST_VALKEY_URL is required for the isolated Valkey harness");
    let pool = PgPoolOptions::new()
        .max_connections(3)
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL must be reachable");
    let version: String = sqlx::query_scalar("SHOW server_version")
        .fetch_one(&pool)
        .await
        .expect("PostgreSQL version");
    assert!(
        version.starts_with("18."),
        "requires PostgreSQL 18, got {version}"
    );
    sqlx::query("CREATE TABLE IF NOT EXISTS options (key TEXT PRIMARY KEY, value TEXT)")
        .execute(&pool)
        .await
        .expect("options fixture table");
    sqlx::query("DELETE FROM options WHERE key = 'SystemConfigOracleProbe'")
        .execute(&pool)
        .await
        .expect("remove old PostgreSQL fixture");
    sqlx::query("INSERT INTO options (key, value) VALUES ('SystemConfigOracleProbe', 'before')")
        .execute(&pool)
        .await
        .expect("PostgreSQL fixture");
    sqlx::query("INSERT INTO options (key, value) VALUES ('SMTPToken', 'never-return-me'), ('GitHubOAuthClientSecret', 'never-return-me'), ('payment_setting.provider_private_key', 'never-return-me') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value")
        .execute(&pool)
        .await
        .expect("sensitive PostgreSQL fixtures");

    let valkey = redis::Client::open(valkey_url).expect("isolated Valkey URL");
    let mut connection = valkey
        .get_multiplexed_async_connection()
        .await
        .expect("isolated Valkey must be reachable");
    connection
        .set::<_, _, ()>(
            "lmm:system-config:options",
            r#"{"SystemConfigOracleProbe":"stale"}"#,
        )
        .await
        .expect("stale cache fixture");

    let app = system_config_router(
        SystemConfigHttpState::new(
            pool.clone(),
            valkey.clone(),
            Arc::new(Root),
            Arc::new(Update),
            Arc::new(Pancake),
        )
        .with_runtime_writer(Arc::new(OracleProbeRuntimeWriter)),
    );
    let update = app
        .clone()
        .oneshot(
            Request::builder()
                .method("PUT")
                .uri("/api/option/")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"key":"SystemConfigOracleProbe","value":"after"}"#,
                ))
                .expect("update request"),
        )
        .await
        .expect("update response");
    assert_eq!(update.status(), StatusCode::OK);
    assert_eq!(
        sqlx::query_scalar::<_, String>(
            "SELECT value FROM options WHERE key = 'SystemConfigOracleProbe'",
        )
        .fetch_one(&pool)
        .await
        .expect("updated PostgreSQL value"),
        "after"
    );
    assert_eq!(
        connection
            .get::<_, Option<String>>("lmm:system-config:options")
            .await
            .expect("Valkey cache read"),
        None
    );

    let get = app
        .oneshot(
            Request::builder()
                .uri("/api/option/")
                .body(Body::empty())
                .expect("GET request"),
        )
        .await
        .expect("GET response");
    let body = serde_json::from_slice::<Value>(
        &to_bytes(get.into_body(), usize::MAX)
            .await
            .expect("GET body"),
    )
    .expect("GET JSON");
    assert!(
        body["data"]
            .as_array()
            .expect("option array")
            .iter()
            .any(|option| option == &json!({"key":"SystemConfigOracleProbe","value":"after"}))
    );
    assert!(
        connection
            .get::<_, Option<String>>("lmm:system-config:options")
            .await
            .expect("repopulated Valkey cache")
            .is_some()
    );
    assert!(
        body["data"]
            .as_array()
            .expect("option array")
            .iter()
            .all(|option| {
                !matches!(
                    option["key"].as_str(),
                    Some(
                        "SMTPToken"
                            | "GitHubOAuthClientSecret"
                            | "payment_setting.provider_private_key"
                    )
                )
            }),
        "root reads must use the same secret-redaction policy for SMTP, OAuth, and payment settings"
    );
}
