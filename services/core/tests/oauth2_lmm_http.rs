//! Author-only HTTP regressions; execution and independent acceptance are UNRUN.
//! The configured issuer remains HTTPS. The listener is synthetic loopback HTTP
//! and Secure cookies are sent manually, so this is not browser/TLS acceptance.
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use lmm_core::{
    core_schema,
    identity::{
        BrowserAuth, BrowserBundle, BrowserRegistration, BrowserSessionMetadata, IdentityStore,
        grants::{LmmIssuer, LmmProfile},
    },
    legacy_auth::{self, LegacyAuthConfig},
    oauth2_http,
    runtime_database::{self, RequiredCapabilities},
};
use reqwest::{Client, Response, StatusCode, Url, header};
use serde_json::Value;
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use std::{future::Future, sync::Arc, time::Duration};
use tokio::{sync::oneshot, task::JoinHandle};

#[path = "common/runtime.rs"]
mod runtime;

const ISSUER: &str = "https://issuer.example.test";
const RESOURCE: &str = "https://issuer.example.test/api/oauth2";
const REDIRECT: &str = "http://127.0.0.1:38423/oauth/lmm/callback";
const PASSWORD: &str = "test passphrase";
const VERIFIER: &str = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-._~";

struct Fixture {
    session: BrowserBundle,
    user: i64,
}

fn profile() -> LmmProfile {
    LmmProfile::new(ISSUER, vec!["default".into(), "private".into()]).unwrap()
}

async fn fixture(pool: &PgPool) -> Fixture {
    core_schema::init(pool).await.unwrap();
    let store = IdentityStore::from_pool(pool.clone())
        .with_registration_enabled(true)
        .with_mfa_keys("test".into(), vec![("test".into(), vec![47; 32])])
        .unwrap();
    let browser = BrowserAuth::new(store.clone(), &[43; 32]).unwrap();
    browser
        .register(&BrowserRegistration {
            username: "alice",
            password: PASSWORD,
            email: "",
        })
        .await
        .unwrap();
    let user: i64 = sqlx::query_scalar(
        "SELECT user_id FROM core_identity.user_profiles WHERE username='alice'",
    )
    .fetch_one(pool)
    .await
    .unwrap();
    // Installation-owner fixture operations precede browser authentication.
    sqlx::query("INSERT INTO core_identity.developer_access(user_id,granted,source_reference) VALUES ($1,TRUE,'synthetic-oauth-test')")
        .bind(user).execute(pool).await.unwrap();
    sqlx::raw_sql(r#"
        INSERT INTO routing_price_versions(version,document) VALUES(1,'{"version":1}');
        INSERT INTO routing_config_versions(version,price_version,document) VALUES(1,1,'{"version":1}');
        INSERT INTO routing_groups(version,name,enabled) VALUES
            (1,'default',TRUE),(1,'private',TRUE),(1,'disabled',FALSE);
        UPDATE routing_price_versions SET sealed=TRUE WHERE version=1;
        UPDATE routing_config_versions SET sealed=TRUE WHERE version=1;
        UPDATE routing_active_release SET config_version=1;
    "#).execute(pool).await.unwrap();
    for group in ["default", "private", "disabled"] {
        sqlx::query("INSERT INTO core_identity.user_routing_groups(user_id,group_name,source_reference) VALUES ($1,$2,'synthetic-oauth-test')")
            .bind(user).bind(group).execute(pool).await.unwrap();
    }
    let session = browser
        .login("alice", PASSWORD, &BrowserSessionMetadata::default())
        .await
        .unwrap();
    Fixture { session, user }
}
async fn runtime_issuer(pool: &PgPool) -> (runtime::RuntimeRole, LmmIssuer) {
    let role = std::sync::Arc::new(runtime::RuntimeRole::create(pool).await);
    let setup_role = role.clone();
    let owner = pool.clone();
    let setup = tokio::spawn(async move {
        let role = setup_role;
        let (session_user, current_user): (String, String) =
            sqlx::query_as("SELECT session_user::text,current_user::text")
                .fetch_one(&role.pool)
                .await
                .unwrap();
        assert!(
            session_user == role.name,
            "runtime connection must authenticate its owned role"
        );
        assert!(
            current_user == role.name,
            "runtime SQL must execute as its owned role"
        );
        let role_url = Url::parse(&role.url).expect("synthetic runtime URL must parse");
        assert!(
            role_url.username() == role.name,
            "runtime URL must name its owned role"
        );
        role.optional("web_identity").await;
        // These are issuer-state privileges; policy tables remain SELECT-only.
        sqlx::raw_sql(&format!(
            r#"
        GRANT USAGE ON SCHEMA core_oauth TO {0};
        GRANT SELECT ON core_oauth.contract TO {0};
        GRANT SELECT,INSERT ON core_oauth.authorization_requests,core_oauth.authorization_codes,
            core_oauth.families,core_oauth.access_tokens,core_oauth.refresh_tokens TO {0};
        GRANT UPDATE(consumed_at) ON core_oauth.authorization_requests,
            core_oauth.authorization_codes,core_oauth.refresh_tokens TO {0};
        GRANT UPDATE(scopes,revoked_at) ON core_oauth.families TO {0};
        GRANT EXECUTE ON FUNCTION core_oauth.readonly_scope_set(text[]) TO {0};
        GRANT EXECUTE ON FUNCTION core_oauth.client_scope_set(text,text[],boolean) TO {0};
    "#,
            role.name
        ))
        .execute(&owner)
        .await
        .unwrap();
        runtime_database::check(
            &role.pool,
            RequiredCapabilities {
                web_identity: true,
                oauth2: true,
                ..RequiredCapabilities::default()
            },
        )
        .await
        .unwrap();
        LmmIssuer::open(
            IdentityStore::from_pool(role.pool.clone()),
            profile(),
            900,
            86400,
        )
        .await
        .unwrap()
    })
    .await;
    let role = match std::sync::Arc::try_unwrap(role) {
        Ok(role) => role,
        Err(_) => panic!("runtime setup retained an unexpected role reference"),
    };
    match setup {
        Ok(issuer) => (role, issuer),
        Err(error) => {
            role.cleanup().await;
            std::panic::resume_unwind(error.into_panic());
        }
    }
}

struct Server {
    stop: Option<oneshot::Sender<()>>,
    task: Option<JoinHandle<()>>,
}
impl Server {
    async fn close(mut self) -> Result<(), &'static str> {
        let stop_sent = self.stop.take().unwrap().send(()).is_ok();
        let mut task = self.task.take().unwrap();
        match tokio::time::timeout(Duration::from_secs(5), &mut task).await {
            Ok(Ok(())) if stop_sent => Ok(()),
            Ok(Ok(())) => Err("owned HTTP fixture stopped before shutdown request"),
            Ok(Err(_)) => Err("owned HTTP fixture task failed"),
            Err(_) => {
                task.abort();
                let _ = task.await;
                Err("owned HTTP fixture did not shut down in five seconds")
            }
        }
    }
}
impl Drop for Server {
    fn drop(&mut self) {
        if let Some(stop) = self.stop.take() {
            let _ = stop.send(());
        }
        if let Some(task) = self.task.take() {
            task.abort();
        }
    }
}

struct HttpFixture {
    owner: PgPool,
    data: Fixture,
    role: runtime::RuntimeRole,
    client: Client,
    base: String,
    server: Server,
}
impl HttpFixture {
    async fn new(pool: &PgPool) -> Self {
        let data = fixture(pool).await;
        let native_registered: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM core_identity.audit
             WHERE actor_user_id=$1 AND action='user.register'
             AND target->>'user_id'=$1::bigint::text)",
        )
        .bind(data.user)
        .fetch_one(pool)
        .await
        .unwrap();
        assert!(
            native_registered,
            "HTTP subject must have its real native registration audit"
        );
        // Do fallible transport setup before acquiring the cluster-wide role.
        let config = LegacyAuthConfig::new(ISSUER, &[43; 32]).unwrap();
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let client = Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            .timeout(Duration::from_secs(5))
            .build()
            .unwrap();
        let (role, issuer) = runtime_issuer(pool).await;
        let store = IdentityStore::from_pool(role.pool.clone());
        // Router/config assembly can panic as product code changes. Capture
        // that failure while retaining ownership of the role for cleanup.
        let assembled = tokio::spawn(async move {
            let config = config.with_oauth2_issuer(issuer.clone()).unwrap();
            let app = legacy_auth::router(store, config).merge(oauth2_http::router(issuer));
            let (stop, stopped) = oneshot::channel();
            let task = tokio::spawn(async move {
                axum::serve(
                    listener,
                    app.into_make_service_with_connect_info::<std::net::SocketAddr>(),
                )
                .with_graceful_shutdown(async move {
                    let _ = stopped.await;
                })
                .await
                .unwrap();
            });
            Server {
                stop: Some(stop),
                task: Some(task),
            }
        })
        .await;
        let server = match assembled {
            Ok(server) => server,
            Err(error) => {
                role.cleanup().await;
                if error.is_panic() {
                    std::panic::resume_unwind(error.into_panic());
                }
                panic!("HTTP fixture setup was cancelled");
            }
        };
        Self {
            owner: pool.clone(),
            data,
            role,
            client,
            base: format!("http://{address}"),
            server,
        }
    }
    fn url(&self, path: &str) -> String {
        format!("{}{path}", self.base)
    }
    fn browser_cookie(&self) -> String {
        format!("new_api_refresh={}", self.data.session.refresh_token)
    }
    async fn close(self) -> Result<(), &'static str> {
        let server = self.server.close().await;
        self.role.cleanup().await;
        server
    }
}

// Capture assertion panics so our runtime role and TCP server still get their
// explicit cleanup. SQLx owns and disposes of each test's separate database.
async fn with_http<F, Fut>(pool: PgPool, case: F)
where
    F: FnOnce(Arc<HttpFixture>) -> Fut + Send + 'static,
    Fut: Future<Output = ()> + Send + 'static,
{
    let fixture = Arc::new(HttpFixture::new(&pool).await);
    let copy = fixture.clone();
    let result = tokio::spawn(async move { case(copy).await }).await;
    let fixture = match Arc::try_unwrap(fixture) {
        Ok(fixture) => fixture,
        Err(_) => panic!("HTTP case retained an unexpected fixture reference"),
    };
    let cleanup = fixture.close().await;
    if let Err(error) = result {
        if let Err(cleanup) = cleanup {
            eprintln!("HTTP cleanup also failed: {cleanup}");
        }
        if error.is_panic() {
            std::panic::resume_unwind(error.into_panic());
        }
        panic!("HTTP fixture case was cancelled");
    }
    cleanup.unwrap();
}

fn security(response: &Response, expected: StatusCode) {
    assert_eq!(response.status(), expected);
    assert_eq!(response.headers()[header::CACHE_CONTROL], "no-store");
    assert_eq!(response.headers()[header::PRAGMA], "no-cache");
    assert!(
        !response
            .headers()
            .contains_key(header::ACCESS_CONTROL_ALLOW_ORIGIN)
    );
}
fn form(pairs: &[(&str, &str)]) -> String {
    let mut url = Url::parse("https://encoding.invalid/").unwrap();
    url.query_pairs_mut().extend_pairs(pairs.iter().copied());
    url.query().unwrap().to_owned()
}
fn authorize_url(f: &HttpFixture) -> String {
    let challenge = URL_SAFE_NO_PAD.encode(Sha256::digest(VERIFIER.as_bytes()));
    format!(
        "{}?{}",
        f.url("/api/oauth2/authorize"),
        form(&[
            ("response_type", "code"),
            ("client_id", "lmm"),
            ("redirect_uri", REDIRECT),
            ("scope", "catalog:read balance:read"),
            ("resource", RESOURCE),
            ("code_challenge", &challenge),
            ("code_challenge_method", "S256"),
            ("state", "synthetic-state"),
        ])
    )
}
fn csrf(html: &str) -> String {
    let start = "name=csrf value=\"";
    let (_, after) = html.split_once(start).unwrap();
    let (csrf, _) = after.split_once('"').unwrap();
    assert_eq!(html.matches(start).count(), 1);
    assert_eq!(csrf.len(), 43);
    csrf.to_owned()
}
fn binding_cookie(response: &Response) -> String {
    let raw: Vec<_> = response
        .headers()
        .get_all(header::SET_COOKIE)
        .iter()
        .filter(|value| value.to_str().unwrap().contains("Max-Age=300"))
        .collect();
    assert_eq!(raw.len(), 1);
    let raw = raw[0].to_str().unwrap();
    assert!(raw.starts_with("__Secure-lmm_oauth2_"));
    let (name, value) = raw.split_once('=').unwrap();
    assert_eq!(name.len(), "__Secure-lmm_oauth2_".len() + 64);
    assert!(
        name["__Secure-lmm_oauth2_".len()..]
            .bytes()
            .all(|b| b.is_ascii_hexdigit() && !b.is_ascii_uppercase())
    );
    assert_eq!(value.split(';').next().unwrap().len(), 43);
    assert!(raw.contains("; Path=/api/user/auth/oauth2;"));
    for attribute in ["Secure;", "HttpOnly;", "SameSite=Strict;", "Max-Age=300"] {
        assert!(raw.contains(attribute));
    }
    assert!(!raw.contains("Domain="));
    raw.split(';').next().unwrap().to_owned()
}
fn cleared_binding(response: &Response) {
    let cleared: Vec<_> = response
        .headers()
        .get_all(header::SET_COOKIE)
        .iter()
        .filter_map(|value| value.to_str().ok())
        .filter(|value| value.starts_with("__Secure-lmm_oauth2_") && value.contains("=;"))
        .collect();
    assert_eq!(cleared.len(), 1);
    assert!(cleared[0].contains("Max-Age=0"));
}
async fn counts(pool: &PgPool) -> (i64, i64, i64, i64, i64) {
    sqlx::query_as(
        "SELECT (SELECT count(*) FROM core_oauth.families),
         (SELECT count(*) FROM core_oauth.authorization_codes),
         (SELECT count(*) FROM core_oauth.access_tokens),
         (SELECT count(*) FROM core_oauth.refresh_tokens),
         (SELECT count(*) FROM core_oauth.authorization_requests WHERE consumed_at IS NOT NULL)",
    )
    .fetch_one(pool)
    .await
    .unwrap()
}
async fn preflight(f: &HttpFixture) -> (String, String) {
    let response = f
        .client
        .get(authorize_url(f))
        .send()
        .await
        .unwrap_or_else(|_| panic!("owned HTTP transport failed"));
    security(&response, StatusCode::OK);
    let cookie = binding_cookie(&response);
    let html = response.text().await.unwrap();
    assert!(html.contains("/api/user/auth/oauth2/continue"));
    assert!(!html.contains("name=decision"));
    (cookie, csrf(&html))
}
async fn continue_page(f: &HttpFixture, browser_cookie: &str) -> (String, String) {
    let (binding, first_csrf) = preflight(f).await;
    let response = f
        .client
        .post(f.url("/api/user/auth/oauth2/continue"))
        .header(header::ORIGIN, ISSUER)
        .header(header::COOKIE, format!("{binding}; {browser_cookie}"))
        .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
        .body(form(&[("csrf", &first_csrf)]))
        .send()
        .await
        .unwrap_or_else(|_| panic!("owned HTTP transport failed"));
    security(&response, StatusCode::OK);
    let cookie = binding_cookie(&response);
    cleared_binding(&response);
    assert!(
        cookie != binding,
        "Continue must replace the one-use binding"
    );
    let html = response.text().await.unwrap();
    assert!(html.contains("Account: alice"));
    assert!(html.contains("<li>default</li><li>private</li>"));
    assert!(!html.contains("<li>disabled</li>"));
    assert!(html.contains("/api/user/auth/oauth2/consent"));
    (cookie, csrf(&html))
}
async fn consent_code(f: &HttpFixture) -> String {
    let browser_cookie = f.browser_cookie();
    let (binding, csrf) = continue_page(f, &browser_cookie).await;
    let response = f
        .client
        .post(f.url("/api/user/auth/oauth2/consent"))
        .header(header::ORIGIN, ISSUER)
        .header(header::COOKIE, format!("{binding}; {browser_cookie}"))
        .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
        .body(form(&[("csrf", &csrf), ("decision", "allow")]))
        .send()
        .await
        .unwrap_or_else(|_| panic!("owned HTTP transport failed"));
    callback_code(response)
}
fn callback_code(response: Response) -> String {
    security(&response, StatusCode::SEE_OTHER);
    cleared_binding(&response);
    let url = Url::parse(response.headers()[header::LOCATION].to_str().unwrap()).unwrap();
    assert_eq!(url.origin().ascii_serialization(), "http://127.0.0.1:38423");
    assert_eq!(url.path(), "/oauth/lmm/callback");
    assert!(url.fragment().is_none());
    let pairs: Vec<_> = url.query_pairs().collect();
    assert_eq!(pairs.len(), 3);
    for expected in ["code", "state", "iss"] {
        assert_eq!(pairs.iter().filter(|(name, _)| name == expected).count(), 1);
    }
    assert_eq!(
        pairs.iter().find(|(name, _)| name == "state").unwrap().1,
        "synthetic-state"
    );
    assert_eq!(
        pairs.iter().find(|(name, _)| name == "iss").unwrap().1,
        ISSUER
    );
    pairs
        .iter()
        .find(|(name, _)| name == "code")
        .unwrap()
        .1
        .to_string()
}
fn code_body(code: &str, verifier: &str) -> String {
    form(&[
        ("grant_type", "authorization_code"),
        ("client_id", "lmm"),
        ("code", code),
        ("redirect_uri", REDIRECT),
        ("code_verifier", verifier),
        ("resource", RESOURCE),
    ])
}
fn refresh_body(token: &str) -> String {
    form(&[
        ("grant_type", "refresh_token"),
        ("client_id", "lmm"),
        ("refresh_token", token),
        ("resource", RESOURCE),
    ])
}
async fn token_request(f: &HttpFixture, body: String) -> Response {
    f.client
        .post(f.url("/api/oauth2/token"))
        .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
        .body(body)
        .send()
        .await
        .unwrap_or_else(|_| panic!("owned HTTP transport failed"))
}
async fn tokens(f: &HttpFixture) -> Value {
    let code = consent_code(f).await;
    let response = token_request(f, code_body(&code, VERIFIER)).await;
    security(&response, StatusCode::OK);
    let token: Value = response.json().await.unwrap();
    assert_eq!(token["token_type"], "Bearer");
    assert!((1..=900).contains(&token["expires_in"].as_i64().unwrap()));
    assert!(
        token["access_token"]
            .as_str()
            .unwrap()
            .starts_with("lmm_at_")
    );
    assert!(
        token["refresh_token"]
            .as_str()
            .unwrap()
            .starts_with("lmm_rt_")
    );
    token
}

#[sqlx::test(migrations = false)]
async fn tcp_browser_requires_explicit_bound_ceremony_after_get(pool: PgPool) {
    with_http(pool, |f| async move {
        for (path, expected) in [
            ("/.well-known/oauth-authorization-server", "issuer"),
            (
                "/.well-known/oauth-protected-resource/api/oauth2",
                "resource",
            ),
        ] {
            let response = f
                .client
                .get(f.url(path))
                .send()
                .await
                .unwrap_or_else(|_| panic!("owned HTTP transport failed"));
            security(&response, StatusCode::OK);
            let json: Value = response.json().await.unwrap();
            assert_eq!(
                json[expected],
                if expected == "issuer" {
                    ISSUER
                } else {
                    RESOURCE
                }
            );
            if expected == "issuer" {
                assert_eq!(json["lmm_client_ids_supported"], serde_json::json!(["lmm"]));
                assert_eq!(
                    json["code_challenge_methods_supported"],
                    serde_json::json!(["S256"])
                );
                assert_eq!(
                    json["scopes_supported"],
                    serde_json::json!(["catalog:read", "balance:read"])
                );
                assert_eq!(json["authorization_response_iss_parameter_supported"], true);
            }
        }
        for suffix in ["&state=again", "&unknown=1"] {
            let response = f
                .client
                .get(format!("{}{suffix}", authorize_url(&f)))
                .send()
                .await
                .unwrap_or_else(|_| panic!("owned HTTP transport failed"));
            security(&response, StatusCode::BAD_REQUEST);
            assert!(!response.headers().contains_key(header::LOCATION));
        }
        let browser_cookie = f.browser_cookie();
        // Even manually sending the refresh cookie outside its normal Path
        // cannot make the GET identify the user or approve this request.
        let response = f
            .client
            .get(authorize_url(&f))
            .header(header::COOKIE, &browser_cookie)
            .send()
            .await
            .unwrap_or_else(|_| panic!("owned HTTP transport failed"));
        security(&response, StatusCode::OK);
        let binding = binding_cookie(&response);
        let html = response.text().await.unwrap();
        let first_csrf = csrf(&html);
        assert!(html.contains("/api/user/auth/oauth2/continue"));
        assert!(!html.contains("Account: alice"));
        assert!(!html.contains("name=decision"));
        assert_eq!(counts(&f.owner).await, (0, 0, 0, 0, 0));
        let trusted_identity_rows: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM core_oauth.authorization_requests WHERE user_id IS NOT NULL",
        )
        .fetch_one(&f.owner)
        .await
        .unwrap();
        assert_eq!(trusted_identity_rows, 0);
        let valid_body = form(&[("csrf", &first_csrf)]);
        let cookie = format!("{binding}; {browser_cookie}");
        for (origin, body, expected) in [
            (
                "https://other.example.test",
                valid_body.clone(),
                StatusCode::FORBIDDEN,
            ),
            (
                ISSUER,
                format!("{valid_body}&csrf={first_csrf}"),
                StatusCode::BAD_REQUEST,
            ),
            (
                ISSUER,
                format!("{valid_body}&user_id=1"),
                StatusCode::BAD_REQUEST,
            ),
            (
                ISSUER,
                form(&[("csrf", &"A".repeat(43))]),
                StatusCode::UNAUTHORIZED,
            ),
        ] {
            let response = f
                .client
                .post(f.url("/api/user/auth/oauth2/continue"))
                .header(header::ORIGIN, origin)
                .header(header::COOKIE, &cookie)
                .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
                .body(body)
                .send()
                .await
                .unwrap_or_else(|_| panic!("owned HTTP transport failed"));
            security(&response, expected);
            assert_eq!(counts(&f.owner).await, (0, 0, 0, 0, 0));
        }
        for invalid_cookie in [
            browser_cookie.clone(),
            format!("{binding}; {binding}; {browser_cookie}"),
        ] {
            let response = f
                .client
                .post(f.url("/api/user/auth/oauth2/continue"))
                .header(header::ORIGIN, ISSUER)
                .header(header::COOKIE, invalid_cookie)
                .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
                .body(valid_body.clone())
                .send()
                .await
                .unwrap_or_else(|_| panic!("owned HTTP transport failed"));
            security(&response, StatusCode::UNAUTHORIZED);
            assert_eq!(counts(&f.owner).await, (0, 0, 0, 0, 0));
        }
        // Bearer/session headers do not replace the path-scoped refresh
        // cookie. A valid anonymous Continue consumes only its old flow.
        let response = f
            .client
            .post(f.url("/api/user/auth/oauth2/continue"))
            .header(header::ORIGIN, ISSUER)
            .header(header::COOKIE, &binding)
            .bearer_auth(f.data.session.data["access_token"].as_str().unwrap())
            .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
            .body(valid_body.clone())
            .send()
            .await
            .unwrap_or_else(|_| panic!("owned HTTP transport failed"));
        security(&response, StatusCode::OK);
        let new_binding = binding_cookie(&response);
        assert!(
            new_binding != binding,
            "anonymous Continue must replace its binding"
        );
        let anonymous = response.text().await.unwrap();
        assert!(anonymous.contains("/api/user/auth/oauth2/continue"));
        assert!(!anonymous.contains("name=decision"));
        assert_eq!(counts(&f.owner).await, (0, 0, 0, 0, 1));
        let response = f
            .client
            .post(f.url("/api/user/auth/oauth2/continue"))
            .header(header::ORIGIN, ISSUER)
            .header(header::COOKIE, &cookie)
            .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
            .body(valid_body)
            .send()
            .await
            .unwrap_or_else(|_| panic!("owned HTTP transport failed"));
        security(&response, StatusCode::UNAUTHORIZED);
        assert_eq!(counts(&f.owner).await, (0, 0, 0, 0, 1));
    })
    .await;
}

#[sqlx::test(migrations = false)]
async fn tcp_same_sid_consent_and_pkce_exchange_preserve_immutable_authority(pool: PgPool) {
    with_http(pool, |f| async move {
        let (binding, consent_csrf) = continue_page(&f, &f.browser_cookie()).await;
        let response = f.client.post(f.url("/api/user/auth/refresh"))
            .header(header::ORIGIN, ISSUER).header(header::COOKIE, f.browser_cookie())
            .send().await.unwrap_or_else(|_| panic!("owned HTTP transport failed"));
        security(&response, StatusCode::OK);
        let cookie = response.headers()[header::SET_COOKIE].to_str().unwrap().split(';').next().unwrap().to_owned();
        let refreshed: Value = response.json().await.unwrap();
        assert!(refreshed["data"]["session"]["sid"] == f.data.session.data["session"]["sid"], "browser refresh must preserve the stable SID");
        let body = form(&[("csrf", &consent_csrf), ("decision", "allow")]);
        let cookies = format!("{binding}; {cookie}");
        let response = f.client.post(f.url("/api/user/auth/oauth2/consent"))
            .header(header::ORIGIN, ISSUER).header(header::COOKIE, &cookies)
            .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
            .body(body.clone()).send().await.unwrap_or_else(|_| panic!("owned HTTP transport failed"));
        let code = callback_code(response);
        assert_eq!(counts(&f.owner).await, (1, 1, 0, 0, 2));
        let response = f.client.post(f.url("/api/user/auth/oauth2/consent"))
            .header(header::ORIGIN, ISSUER).header(header::COOKIE, &cookies)
            .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
            .body(body).send().await.unwrap_or_else(|_| panic!("owned HTTP transport failed"));
        security(&response, StatusCode::UNAUTHORIZED);
        assert_eq!(counts(&f.owner).await, (1, 1, 0, 0, 2));
        let valid = code_body(&code, VERIFIER);
        for body in [
            format!("{valid}&client_id=lmm"),
            format!("{valid}&client_secret=forbidden"),
            code_body(&code, &"x".repeat(64)),
            valid.replace("issuer.example.test", "other.example.test"),
        ] {
            let response = token_request(&f, body).await;
            security(&response, StatusCode::BAD_REQUEST);
            assert_eq!(counts(&f.owner).await, (1, 1, 0, 0, 2));
            let consumed: bool = sqlx::query_scalar("SELECT consumed_at IS NOT NULL FROM core_oauth.authorization_codes WHERE digest=$1").bind(Sha256::digest(code.as_bytes()).to_vec()).fetch_one(&f.owner).await.unwrap();
            assert!(!consumed);
        }
        for name in ["authorization", "x-api-key"] {
            let response = f.client.post(f.url("/api/oauth2/token"))
                .header(name, "synthetic-native-credential")
                .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
                .body(valid.clone()).send().await.unwrap_or_else(|_| panic!("owned HTTP transport failed"));
            security(&response, StatusCode::BAD_REQUEST);
        }
        let response = f.client.post(format!("{}?access_token=forbidden", f.url("/api/oauth2/token")))
            .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
            .body(valid.clone()).send().await.unwrap_or_else(|_| panic!("owned HTTP transport failed"));
        security(&response, StatusCode::BAD_REQUEST);
        assert_eq!(counts(&f.owner).await, (1, 1, 0, 0, 2));
        let response = token_request(&f, valid.clone()).await;
        security(&response, StatusCode::OK);
        let issued: Value = response.json().await.unwrap();
        assert_eq!(issued["token_type"], "Bearer");
        assert_eq!(counts(&f.owner).await, (1, 1, 1, 1, 2));
        let response = token_request(&f, valid).await;
        security(&response, StatusCode::BAD_REQUEST);
        assert_eq!(counts(&f.owner).await, (1, 1, 1, 1, 2));
        let scope = issued["scope"].as_str().unwrap();
        let scopes: std::collections::BTreeSet<_> = scope.split(' ').collect();
        let group_default = format!("group:{}", URL_SAFE_NO_PAD.encode("default"));
        let group_private = format!("group:{}", URL_SAFE_NO_PAD.encode("private"));
        assert_eq!(scopes, ["catalog:read", "balance:read", group_default.as_str(), group_private.as_str()].into_iter().collect());
    }).await;
}

#[sqlx::test(migrations = false)]
async fn tcp_readonly_resources_old_refresh_revocation_and_rate_limit_fail_closed(pool: PgPool) {
    with_http(pool, |f| async move {
        let issued = tokens(&f).await;
        let access = issued["access_token"].as_str().unwrap();
        let old_refresh = issued["refresh_token"].as_str().unwrap();
        let response = f.client.get(f.url("/api/oauth2/balance")).bearer_auth(access).send().await.unwrap_or_else(|_| panic!("owned HTTP transport failed"));
        security(&response, StatusCode::OK);
        let balance: Value = response.json().await.unwrap();
        assert_eq!(balance["schema_version"], 2);
        assert_eq!(balance["currency"], "USD");
        assert_eq!(balance["quota"], 0);
        assert_eq!(balance["public_credit_balance"], "0");
        assert_eq!(balance["credits_per_usd"], "500000");
        let response = f.client.get(f.url("/api/oauth2/catalog")).bearer_auth(access).send().await.unwrap_or_else(|_| panic!("owned HTTP transport failed"));
        security(&response, StatusCode::SERVICE_UNAVAILABLE);
        let unavailable: Value = response.json().await.unwrap();
        assert_eq!(unavailable["error"], "temporarily_unavailable");
        // No actual ModelGateway was loaded: a successful catalog is never
        // fabricated from the synthetic SQL routing rows used for consent.
        let native_access = f.data.session.data["access_token"].as_str().unwrap();
        let invalid = [
            f.client.get(f.url("/api/oauth2/balance")).header(header::COOKIE, f.browser_cookie()),
            f.client.get(f.url("/api/oauth2/balance")).bearer_auth(native_access),
            f.client.get(f.url("/api/oauth2/balance")).bearer_auth(access).header("x-api-key", native_access),
            f.client.get(format!("{}?access_token={access}", f.url("/api/oauth2/balance"))).bearer_auth(access),
            f.client.get(f.url("/api/oauth2/balance")).header(header::AUTHORIZATION, format!("Bearer {access}")).header(header::AUTHORIZATION, format!("Bearer {access}")),
        ];
        for request in invalid {
            let response = request.send().await.unwrap_or_else(|_| panic!("owned HTTP transport failed"));
            security(&response, StatusCode::UNAUTHORIZED);
            assert_eq!(response.headers()[header::WWW_AUTHENTICATE], "Bearer error=\"invalid_token\"");
        }
        let response = f.client.get(f.url("/api/user/self")).bearer_auth(access).send().await.unwrap_or_else(|_| panic!("owned HTTP transport failed"));
        security(&response, StatusCode::UNAUTHORIZED);
        let response = token_request(&f, refresh_body(old_refresh)).await;
        security(&response, StatusCode::OK);
        let rotated: Value = response.json().await.unwrap();
        assert!(issued["access_token"] != rotated["access_token"], "OAuth refresh must replace its access token");
        assert!(issued["refresh_token"] != rotated["refresh_token"], "OAuth refresh must replace its refresh token");
        assert_eq!(counts(&f.owner).await, (1, 1, 2, 2, 2));
        // An UNKNOWN response can be recovered by revoking the consumed old
        // refresh. A wrong client must leave the live family unchanged first.
        for client in ["other", "lmm", "lmm"] {
            let response = f.client.post(f.url("/api/oauth2/revoke"))
                .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
                .body(form(&[("client_id", client), ("token", old_refresh), ("token_type_hint", "refresh_token")]))
                .send().await.unwrap_or_else(|_| panic!("owned HTTP transport failed"));
            security(&response, if client == "lmm" { StatusCode::OK } else { StatusCode::BAD_REQUEST });
            if client == "lmm" {
                assert!(response.bytes().await.unwrap().is_empty());
            }
            let revoked: bool = sqlx::query_scalar("SELECT revoked_at IS NOT NULL FROM core_oauth.families").fetch_one(&f.owner).await.unwrap();
            assert_eq!(revoked, client == "lmm");
        }
        for token in [&issued, &rotated] {
            let response = f.client.get(f.url("/api/oauth2/balance")).bearer_auth(token["access_token"].as_str().unwrap()).send().await.unwrap_or_else(|_| panic!("owned HTTP transport failed"));
            security(&response, StatusCode::UNAUTHORIZED);
        }
        let response = token_request(&f, refresh_body(old_refresh)).await;
        security(&response, StatusCode::BAD_REQUEST);
        // A separate family proves that replay itself causes revocation;
        // the explicit revoke above cannot serve as evidence for this path.
        let replay_first = tokens(&f).await;
        let replay_refresh = replay_first["refresh_token"].as_str().unwrap();
        let response = token_request(&f, refresh_body(replay_refresh)).await;
        security(&response, StatusCode::OK);
        let replay_next: Value = response.json().await.unwrap();
        let response = f.client.get(f.url("/api/oauth2/balance"))
            .bearer_auth(replay_next["access_token"].as_str().unwrap()).send().await.unwrap_or_else(|_| panic!("owned HTTP transport failed"));
        security(&response, StatusCode::OK);
        let response = token_request(&f, refresh_body(replay_refresh)).await;
        security(&response, StatusCode::BAD_REQUEST);
        let replay_revoked: bool = sqlx::query_scalar("SELECT f.revoked_at IS NOT NULL FROM core_oauth.families f JOIN core_oauth.refresh_tokens r ON r.family_id=f.id WHERE r.digest=$1")
            .bind(Sha256::digest(replay_refresh.as_bytes()).to_vec()).fetch_one(&f.owner).await.unwrap();
        assert!(replay_revoked);
        for token in [&replay_first, &replay_next] {
            let response = f.client.get(f.url("/api/oauth2/balance"))
                .bearer_auth(token["access_token"].as_str().unwrap()).send().await.unwrap_or_else(|_| panic!("owned HTTP transport failed"));
            security(&response, StatusCode::UNAUTHORIZED);
        }
        // Deterministically exercise the actual persisted peer limiter without
        // sixty transport requests or sleeps. Only this SQLx-owned DB changes.
        let bucket = Sha256::digest(b"oauth2_native_peer\0".iter().chain(b"127.0.0.1").copied().collect::<Vec<_>>());
        let seeded = sqlx::query("UPDATE core_identity.auth_attempts SET attempts=60,started_at=clock_timestamp() WHERE bucket=$1").bind(bucket.to_vec()).execute(&f.owner).await.unwrap();
        assert_eq!(seeded.rows_affected(), 1);
        let response = f.client.get(f.url("/.well-known/oauth-authorization-server")).send().await.unwrap_or_else(|_| panic!("owned HTTP transport failed"));
        security(&response, StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(response.headers()[header::RETRY_AFTER], "60");
        let error: Value = response.json().await.unwrap();
        assert_eq!(error["error"], "temporarily_unavailable");
    }).await;
}

#[sqlx::test(migrations = false)]
async fn tcp_parallel_flow_responses_do_not_replace_or_clear_another_tabs_cookie(pool: PgPool) {
    with_http(pool, |f| async move {
        let (a, a_csrf) = preflight(&f).await;
        let (b, b_csrf) = preflight(&f).await;
        let a_name = a.split_once('=').unwrap().0;
        let b_name = b.split_once('=').unwrap().0;
        assert_ne!(
            a_name, b_name,
            "parallel starts must have disjoint cookie names"
        );
        let cookies = format!("{a}; {b}; {}", f.browser_cookie());
        let request = |csrf: &str| {
            f.client
                .post(f.url("/api/user/auth/oauth2/continue"))
                .header(header::ORIGIN, ISSUER)
                .header(header::COOKIE, &cookies)
                .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
                .body(form(&[("csrf", csrf)]))
        };
        let (a_response, b_response) =
            tokio::join!(request(&a_csrf).send(), request(&b_csrf).send());
        let a_response = a_response.unwrap_or_else(|_| panic!("owned HTTP transport failed"));
        let b_response = b_response.unwrap_or_else(|_| panic!("owned HTTP transport failed"));
        security(&a_response, StatusCode::OK);
        security(&b_response, StatusCode::OK);
        for (response, own, other) in [(&a_response, a_name, b_name), (&b_response, b_name, a_name)]
        {
            cleared_binding(response);
            let values: Vec<_> = response
                .headers()
                .get_all(header::SET_COOKIE)
                .iter()
                .map(|v| v.to_str().unwrap())
                .collect();
            assert_eq!(
                values.len(),
                2,
                "Continue must clear old and set exactly one new stage"
            );
            assert!(values.iter().any(|v| v.starts_with(&format!("{own}=;"))));
            assert!(!values.iter().any(|v| v.starts_with(&format!("{other}="))));
        }
        let a_next = binding_cookie(&a_response);
        let b_next = binding_cookie(&b_response);
        assert_ne!(
            a_next.split_once('=').unwrap().0,
            b_next.split_once('=').unwrap().0
        );
        let a_next_csrf = csrf(&a_response.text().await.unwrap());
        let b_next_csrf = csrf(&b_response.text().await.unwrap());
        // Browsers may process the two response header sets in either order:
        // both contain only disjoint names, so neither can overwrite the other.
        let cookies = format!("{b_next}; {a_next}; {}", f.browser_cookie());
        for (binding, csrf, decision) in [
            (&b_next, &b_next_csrf, "deny"),
            (&a_next, &a_next_csrf, "allow"),
        ] {
            let response = f
                .client
                .post(f.url("/api/user/auth/oauth2/consent"))
                .header(header::ORIGIN, ISSUER)
                .header(header::COOKIE, &cookies)
                .header(header::CONTENT_TYPE, "application/x-www-form-urlencoded")
                .body(form(&[("csrf", csrf), ("decision", decision)]))
                .send()
                .await
                .unwrap_or_else(|_| panic!("owned HTTP transport failed"));
            security(&response, StatusCode::SEE_OTHER);
            cleared_binding(&response);
            let expected_name = binding.split_once('=').unwrap().0;
            assert!(
                response.headers()[header::SET_COOKIE]
                    .to_str()
                    .unwrap()
                    .starts_with(&format!("{expected_name}=;"))
            );
            let callback =
                Url::parse(response.headers()[header::LOCATION].to_str().unwrap()).unwrap();
            let pairs: std::collections::BTreeMap<_, _> =
                callback.query_pairs().into_owned().collect();
            assert_eq!(pairs["iss"], ISSUER);
            assert_eq!(pairs["state"], "synthetic-state");
            if decision == "deny" {
                assert_eq!(pairs["error"], "access_denied");
                assert!(!pairs.contains_key("code"));
            } else {
                assert!(pairs.contains_key("code"));
                assert!(!pairs.contains_key("error"));
            }
        }
        assert_eq!(counts(&f.owner).await, (1, 1, 0, 0, 4));
    })
    .await;
}
