//! Independent restricted-role, real TCP and refresh-race session acceptance.
use axum::{Router, http::StatusCode};
use lmm_core::{
    identity::{
        BrowserAuth, BrowserAuthError, BrowserBundle, BrowserRegistration, BrowserSessionMetadata,
        IdentityError, IdentityStore,
    },
    legacy_auth::{self, LegacyAuthConfig},
};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::{net::SocketAddr, time::Duration};
// Exercise exactly the official grant script; no hand-copied partial grant matrix.
struct RuntimeRole {
    pool: PgPool,
    name: String,
    owner: PgPool,
}
impl RuntimeRole {
    async fn create(owner: &PgPool) -> Self {
        let name = format!("sessions_runtime_{:016x}", rand::random::<u64>());
        let password = format!(
            "{:032x}{:032x}",
            rand::random::<u128>(),
            rand::random::<u128>()
        );
        sqlx::raw_sql(&format!("CREATE ROLE {name} LOGIN PASSWORD '{password}' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS")).execute(owner).await.unwrap();
        let database = owner.connect_options().get_database().unwrap().to_owned();
        let mut admin = reqwest::Url::parse(&std::env::var("DATABASE_URL").unwrap()).unwrap();
        assert_eq!(admin.host_str(), Some("127.0.0.1"));
        let grants = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../../deployment/postgres/grant-core-runtime.sql");
        let output = std::process::Command::new("/usr/bin/psql")
            .args([
                "-Xq",
                "-v",
                "ON_ERROR_STOP=1",
                "-v",
                &format!("runtime_role={name}"),
                "-v",
                "enable_oauth2=true",
                "-v",
                "enable_web_identity=true",
                "--file",
            ])
            .arg(grants)
            .env_clear()
            .env("PGHOST", "127.0.0.1")
            .env("PGPORT", admin.port().unwrap().to_string())
            .env("PGUSER", admin.username())
            .env("PGPASSWORD", admin.password().unwrap())
            .env("PGDATABASE", &database)
            .env("LC_ALL", "C")
            .output()
            .unwrap();
        assert!(
            output.status.success(),
            "official runtime grants failed before session behavior"
        );
        admin.set_path(&database);
        admin.set_username(&name).unwrap();
        admin.set_password(Some(&password)).unwrap();
        let pool = sqlx::postgres::PgPoolOptions::new()
            .max_connections(2)
            .connect(admin.as_str())
            .await
            .unwrap();
        lmm_core::runtime_database::check(
            &pool,
            lmm_core::runtime_database::RequiredCapabilities {
                web_identity: true,
                oauth2: true,
                ..Default::default()
            },
        )
        .await
        .expect("official complete runtime authority must pass before testing browser sessions");
        lmm_core::core_schema::check(&pool)
            .await
            .expect("official complete schema contract must match");
        Self {
            pool,
            name,
            owner: owner.clone(),
        }
    }
    async fn cleanup(self) {
        self.pool.close().await;
        sqlx::raw_sql(&format!(
            "DROP OWNED BY {}; DROP ROLE {}",
            self.name, self.name
        ))
        .execute(&self.owner)
        .await
        .unwrap();
    }
}

const ORIGIN: &str = "http://api.lmm.local";
const PASSWORD: &str = "session-test-pass";
struct Host {
    endpoint: String,
    task: tokio::task::JoinHandle<()>,
}
impl Drop for Host {
    fn drop(&mut self) {
        self.task.abort();
    }
}
async fn setup(owner: &PgPool) -> (BrowserAuth, RuntimeRole, Host) {
    let store = IdentityStore::from_pool(owner.clone()).with_registration_enabled(true);
    // The shared restricted runtime role is provisioned against the complete
    // official fresh schema, including the ledger functions in its grants.
    lmm_core::core_schema::init(owner).await.unwrap();
    let seed = BrowserAuth::new(store, &[69; 32]).unwrap();
    for name in ["independent_alice", "independent_bob"] {
        seed.register(&BrowserRegistration {
            username: name,
            password: PASSWORD,
            email: "",
        })
        .await
        .unwrap();
    }
    let role = RuntimeRole::create(owner).await;
    let store = IdentityStore::from_pool(role.pool.clone());
    let browser = BrowserAuth::new(store.clone(), &[69; 32]).unwrap();
    let app: Router = legacy_auth::router(
        store,
        LegacyAuthConfig::new(ORIGIN, &[69; 32])
            .unwrap()
            .with_insecure_local_http()
            .unwrap(),
    );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let endpoint = format!("http://{}", listener.local_addr().unwrap());
    let task = tokio::spawn(async move {
        axum::serve(
            listener,
            app.into_make_service_with_connect_info::<SocketAddr>(),
        )
        .await
        .unwrap();
    });
    (browser, role, Host { endpoint, task })
}
async fn login(browser: &BrowserAuth, name: &str) -> BrowserBundle {
    browser
        .login(
            name,
            PASSWORD,
            &BrowserSessionMetadata {
                ip: "".into(),
                user_agent: "".into(),
            },
        )
        .await
        .unwrap()
}
fn access(b: &BrowserBundle) -> &str {
    b.data["access_token"].as_str().unwrap()
}
fn sid(b: &BrowserBundle) -> &str {
    b.data["session"]["sid"].as_str().unwrap()
}
async fn http(
    host: &Host,
    method: reqwest::Method,
    path: &str,
    b: Option<&BrowserBundle>,
    body: Option<Value>,
) -> reqwest::Response {
    let client = reqwest::Client::builder()
        .no_proxy()
        .timeout(Duration::from_secs(5))
        .build()
        .unwrap();
    let mut r = client
        .request(method, format!("{}{path}", host.endpoint))
        .header("origin", ORIGIN);
    if let Some(b) = b {
        r = r.bearer_auth(access(b)).header("x-auth-session", sid(b));
    }
    if let Some(body) = body {
        r = r.json(&body);
    }
    r.send().await.unwrap()
}
async fn assert_refresh_dead(browser: &BrowserAuth, b: &BrowserBundle) {
    assert!(browser.refresh(&b.refresh_token, None).await.is_err());
}
#[sqlx::test(migrations = false)]
async fn restricted_runtime_tcp_contract_has_no_secrets_and_revokes_exact_families(pool: PgPool) {
    let (browser, role, host) = setup(&pool).await;
    let first = login(&browser, "independent_alice").await;
    let other = login(&browser, "independent_alice").await;
    let bob = login(&browser, "independent_bob").await;
    let r = http(
        &host,
        reqwest::Method::GET,
        "/api/user/sessions",
        Some(&first),
        None,
    )
    .await;
    assert_eq!(r.status(), StatusCode::OK);
    assert_eq!(r.headers()["cache-control"], "no-store");
    let value: Value = r.json().await.unwrap();
    let rows = value["data"].as_array().unwrap();
    assert_eq!(rows.len(), 2);
    for row in rows {
        let mut keys = row
            .as_object()
            .unwrap()
            .keys()
            .map(String::as_str)
            .collect::<Vec<_>>();
        keys.sort();
        assert_eq!(
            keys,
            vec![
                "created_at",
                "current",
                "expires_at",
                "ip",
                "last_active_at",
                "login_method",
                "sid",
                "user_agent"
            ]
        );
        assert!(row["ip"].is_null());
        assert!(row["user_agent"].is_null());
        assert_eq!(row["login_method"], "password");
    }
    let raw = value.to_string();
    for secret in [
        access(&first),
        access(&other),
        access(&bob),
        first.refresh_token.as_str(),
        other.refresh_token.as_str(),
        sid(&bob),
    ] {
        assert!(!raw.contains(secret));
    }
    let forbidden = http(
        &host,
        reqwest::Method::DELETE,
        &format!("/api/user/sessions/{}", sid(&bob)),
        Some(&first),
        None,
    )
    .await;
    assert_eq!(forbidden.status(), StatusCode::FORBIDDEN);
    let r = http(
        &host,
        reqwest::Method::POST,
        "/api/user/sessions/revoke-others",
        Some(&first),
        None,
    )
    .await;
    assert_eq!(r.status(), StatusCode::OK);
    assert!(r.headers().get("set-cookie").is_none());
    let v: Value = r.json().await.unwrap();
    assert_eq!(v["data"]["revoked_count"], 1);
    assert_refresh_dead(&browser, &other).await;
    browser.current_user(access(&first)).await.unwrap();
    browser.current_user(access(&bob)).await.unwrap();
    let r = http(
        &host,
        reqwest::Method::DELETE,
        &format!("/api/user/sessions/{}", sid(&first)),
        Some(&first),
        None,
    )
    .await;
    assert_eq!(r.status(), StatusCode::OK);
    let cookie = r.headers()["set-cookie"].to_str().unwrap();
    assert!(cookie.contains("Max-Age=0"));
    assert!(cookie.contains("Path=/api/user/auth"));
    assert!(cookie.contains("HttpOnly"));
    let v: Value = r.json().await.unwrap();
    assert_eq!(v["data"]["current"], true);
    assert_eq!(v["data"]["revoked_sid"], sid(&first));
    assert_refresh_dead(&browser, &first).await;
    drop(host);
    drop(browser);
    role.cleanup().await;
}
#[sqlx::test(migrations = false)]
async fn blocked_real_refresh_cannot_resurrect_revoked_family_and_nowait_rolls_back(pool: PgPool) {
    let (browser, role, host) = setup(&pool).await;
    let first = login(&browser, "independent_alice").await;
    let other = login(&browser, "independent_alice").await;
    let mut blocker = pool.begin().await.unwrap();
    sqlx::query("SELECT sid FROM core_identity.web_sessions WHERE sid=$1 FOR UPDATE")
        .bind(sid(&other))
        .execute(&mut *blocker)
        .await
        .unwrap();
    let cloned = browser.clone();
    let refresh = other.refresh_token.clone();
    let handle = tokio::spawn(async move { cloned.refresh(&refresh, None).await });
    tokio::time::timeout(Duration::from_secs(2),async {loop {let waiting:i64=sqlx::query_scalar("SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock' AND query LIKE 'SELECT sid FROM core_identity.web_sessions WHERE sid=% FOR UPDATE'").bind(&role.name).fetch_one(&pool).await.unwrap();if waiting==1 {break;}tokio::time::sleep(Duration::from_millis(10)).await;}}).await.expect("real refresher must be observed waiting on the owned family lock");
    assert!(!handle.is_finished());
    let result = tokio::time::timeout(
        Duration::from_secs(2),
        browser.revoke_login_sessions(access(&first), None, Some(sid(&first))),
    )
    .await
    .expect("NOWAIT revocation must return promptly");
    assert!(matches!(
        result,
        Err(BrowserAuthError::Identity(IdentityError::Conflict))
    ));
    let revoked: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_identity.web_sessions WHERE revoked_at IS NOT NULL",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(revoked, 0);
    blocker.rollback().await.unwrap();
    let rotated = tokio::time::timeout(Duration::from_secs(3), handle)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert_eq!(sid(&rotated), sid(&other));
    assert!(browser.current_user(access(&other)).await.is_err());
    browser.current_user(access(&rotated)).await.unwrap();
    let r = http(
        &host,
        reqwest::Method::POST,
        "/api/user/sessions/revoke-others",
        Some(&first),
        None,
    )
    .await;
    assert_eq!(r.status(), StatusCode::OK);
    assert_refresh_dead(&browser, &other).await;
    assert_refresh_dead(&browser, &rotated).await;
    drop(host);
    drop(browser);
    role.cleanup().await;
}
#[sqlx::test(migrations = false)]
async fn weekly_transition_preserves_settings_history_and_terminal_self_revocation(pool: PgPool) {
    let (browser, role, host) = setup(&pool).await;
    let current = login(&browser, "independent_alice").await;
    browser
        .update_login_session_settings(access(&current), false, None)
        .await
        .unwrap();
    sqlx::query("UPDATE core_identity.user_profiles SET setting=(setting::jsonb || '{\"independent_unknown\":{\"preserve\":true}}'::jsonb)::text WHERE username='independent_alice'").execute(&pool).await.unwrap();
    sqlx::query("UPDATE core_identity.web_sessions SET created_at=clock_timestamp()-interval '8 days',expires_at=clock_timestamp()+interval '20 days' WHERE sid=$1").bind(sid(&current)).execute(&pool).await.unwrap();
    let list = browser.login_sessions(access(&current)).await.unwrap();
    assert_eq!(list["session_auto_logout"], false);
    assert_eq!(list["data"].as_array().unwrap().len(), 1);
    let r = http(
        &host,
        reqwest::Method::PUT,
        "/api/user/sessions/settings",
        Some(&current),
        Some(json!({"session_auto_logout":true})),
    )
    .await;
    assert_eq!(r.status(), StatusCode::OK);
    assert!(browser.current_user(access(&current)).await.is_err());
    assert_refresh_dead(&browser, &current).await;
    let raw: String = sqlx::query_scalar(
        "SELECT setting FROM core_identity.user_profiles WHERE username='independent_alice'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    let value: Value = serde_json::from_str(&raw).unwrap();
    assert_eq!(value["independent_unknown"]["preserve"], true);
    assert_eq!(value["session_auto_logout"], true);
    let fresh = login(&browser, "independent_alice").await;
    browser
        .update_login_session_settings(access(&fresh), false, None)
        .await
        .unwrap();
    assert_refresh_dead(&browser, &current).await;
    let revoked: bool = sqlx::query_scalar(
        "SELECT revoked_at IS NOT NULL FROM core_identity.web_sessions WHERE sid=$1",
    )
    .bind(sid(&current))
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(revoked);
    drop(host);
    drop(browser);
    role.cleanup().await;
}
