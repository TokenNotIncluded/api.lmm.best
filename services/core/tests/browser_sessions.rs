//! Real isolated PostgreSQL acceptance for native browser-family management.
use axum::{
    Router,
    body::{Body, to_bytes},
    http::{HeaderMap, Request, StatusCode},
};
use lmm_core::{
    identity::{
        BrowserAuth, BrowserAuthError, BrowserBundle, BrowserRegistration, BrowserSessionMetadata,
        IdentityError, IdentityStore,
    },
    legacy_auth::{self, LegacyAuthConfig},
};
use serde_json::{Value, json};
use sqlx::PgPool;
use totp_rs::{Algorithm, Builder, Secret};
use tower::ServiceExt;
const ORIGIN: &str = "https://sessions.example.invalid";
const PASSWORD: &str = "sessions-password";
async fn setup(pool: PgPool) -> (BrowserAuth, Router) {
    let store = IdentityStore::from_pool(pool.clone())
        .with_registration_enabled(true)
        .with_mfa_keys("test".into(), vec![("test".into(), vec![47; 32])])
        .unwrap();
    store.init_database().await.unwrap();
    sqlx::raw_sql(lmm_core::abuse::SCHEMA)
        .execute(&pool)
        .await
        .unwrap();
    let browser = BrowserAuth::new(store.clone(), &[43; 32]).unwrap();
    let app = legacy_auth::router(store, LegacyAuthConfig::new(ORIGIN, &[43; 32]).unwrap());
    (browser, app)
}
async fn register(browser: &BrowserAuth, name: &str) {
    browser
        .register(&BrowserRegistration {
            username: name,
            password: PASSWORD,
            email: "",
        })
        .await
        .unwrap();
}
async fn login(browser: &BrowserAuth, name: &str) -> BrowserBundle {
    browser
        .login(
            name,
            PASSWORD,
            &BrowserSessionMetadata {
                ip: "192.0.2.2".into(),
                user_agent: "synthetic session acceptance".into(),
            },
        )
        .await
        .unwrap()
}
fn access(bundle: &BrowserBundle) -> &str {
    bundle.data["access_token"].as_str().unwrap()
}
fn sid(bundle: &BrowserBundle) -> &str {
    bundle.data["session"]["sid"].as_str().unwrap()
}
async fn call(
    app: &Router,
    method: &str,
    path: &str,
    token: Option<&str>,
    origin: bool,
    body: Value,
    expected: Option<&str>,
) -> (StatusCode, HeaderMap, Value) {
    let mut builder = Request::builder()
        .method(method)
        .uri(path)
        .header("content-type", "application/json");
    if let Some(token) = token {
        builder = builder.header("authorization", format!("Bearer {token}"));
    }
    if origin {
        builder = builder.header("origin", ORIGIN);
    }
    if let Some(sid) = expected {
        builder = builder.header("x-auth-session", sid);
    }
    let response = app
        .clone()
        .oneshot(builder.body(Body::from(body.to_string())).unwrap())
        .await
        .unwrap();
    let status = response.status();
    let headers = response.headers().clone();
    let value =
        serde_json::from_slice(&to_bytes(response.into_body(), 65536).await.unwrap()).unwrap();
    assert_eq!(headers["cache-control"], "no-store");
    (status, headers, value)
}
#[sqlx::test(migrations = false)]
async fn list_projects_only_owned_live_families_without_secrets(pool: PgPool) {
    let (browser, app) = setup(pool.clone()).await;
    register(&browser, "alice").await;
    register(&browser, "bob").await;
    let first = login(&browser, "alice").await;
    let second = login(&browser, "alice").await;
    let other = login(&browser, "bob").await;
    let (status, _, value) = call(
        &app,
        "GET",
        "/api/user/sessions",
        Some(access(&first)),
        false,
        Value::Null,
        None,
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(value["success"], true);
    assert_eq!(value["session_auto_logout"], true);
    let rows = value["data"].as_array().unwrap();
    assert_eq!(rows.len(), 2);
    assert!(
        rows.iter()
            .any(|r| r["sid"] == sid(&first) && r["current"] == true)
    );
    assert!(
        rows.iter()
            .any(|r| r["sid"] == sid(&second) && r["current"] == false)
    );
    assert!(!value.to_string().contains(sid(&other)));
    assert!(!value.to_string().contains(access(&first)));
    for row in rows {
        assert_eq!(row["ip"], "192.0.2.2");
        assert_eq!(row["login_method"], "password");
        assert!(row.get("refresh_digest").is_none());
    }
    assert_eq!(
        call(
            &app,
            "GET",
            "/api/user/sessions",
            None,
            false,
            Value::Null,
            None
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
}
#[sqlx::test(migrations = false)]
async fn cross_user_origin_and_stale_generation_fail_without_writes(pool: PgPool) {
    let (browser, app) = setup(pool.clone()).await;
    register(&browser, "alice").await;
    register(&browser, "bob").await;
    let first = login(&browser, "alice").await;
    let other = login(&browser, "bob").await;
    let path = format!("/api/user/sessions/{}", sid(&other));
    assert_eq!(
        call(
            &app,
            "DELETE",
            &path,
            Some(access(&first)),
            true,
            Value::Null,
            None
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/user/sessions/revoke-others",
            Some(access(&first)),
            false,
            Value::Null,
            None
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/user/sessions/revoke-others",
            Some(access(&first)),
            true,
            Value::Null,
            Some(sid(&other))
        )
        .await
        .0,
        StatusCode::CONFLICT
    );
    browser.current_user(access(&other)).await.unwrap();
    let refreshed = browser
        .refresh(&first.refresh_token, Some(sid(&first)))
        .await
        .unwrap();
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/user/sessions/revoke-others",
            Some(access(&first)),
            true,
            Value::Null,
            None
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    browser.current_user(access(&refreshed)).await.unwrap();
    browser.current_user(access(&other)).await.unwrap();
}
#[sqlx::test(migrations = false)]
async fn revoke_others_invalidates_access_refresh_but_preserves_self_and_other_users(pool: PgPool) {
    let (browser, app) = setup(pool.clone()).await;
    register(&browser, "alice").await;
    register(&browser, "bob").await;
    let first = login(&browser, "alice").await;
    let second = login(&browser, "alice").await;
    let other = login(&browser, "bob").await;
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/user/sessions/revoke-others",
            Some(access(&first)),
            true,
            Value::Null,
            None
        )
        .await
        .0,
        StatusCode::OK
    );
    browser.current_user(access(&first)).await.unwrap();
    browser.current_user(access(&other)).await.unwrap();
    assert!(browser.current_user(access(&second)).await.is_err());
    assert!(browser.refresh(&second.refresh_token, None).await.is_err());
    assert_eq!(
        browser.login_sessions(access(&first)).await.unwrap()["data"]
            .as_array()
            .unwrap()
            .len(),
        1
    );
    let count: i64=sqlx::query_scalar("SELECT count(*) FROM core_identity.session_authentication a JOIN core_identity.credentials c ON c.id=a.credential_id JOIN core_identity.web_session_credentials m ON m.credential_id=c.id WHERE m.sid=$1 AND c.revoked_at IS NOT NULL").bind(sid(&second)).fetch_one(&pool).await.unwrap();
    assert_eq!(count, 1);
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/user/sessions/revoke-others",
            Some(access(&first)),
            true,
            Value::Null,
            None
        )
        .await
        .0,
        StatusCode::OK
    );
}
#[sqlx::test(migrations = false)]
async fn self_revocation_clears_cookie_without_invalidating_other_family(pool: PgPool) {
    let (browser, app) = setup(pool.clone()).await;
    register(&browser, "alice").await;
    let first = login(&browser, "alice").await;
    let second = login(&browser, "alice").await;
    let path = format!("/api/user/sessions/{}", sid(&first));
    let (status, headers, _) = call(
        &app,
        "DELETE",
        &path,
        Some(access(&first)),
        true,
        Value::Null,
        Some(sid(&first)),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert!(
        headers["set-cookie"]
            .to_str()
            .unwrap()
            .contains("Max-Age=0")
    );
    assert!(browser.current_user(access(&first)).await.is_err());
    assert!(browser.refresh(&first.refresh_token, None).await.is_err());
    browser.current_user(access(&second)).await.unwrap();
}
#[sqlx::test(migrations = false)]
async fn weekly_policy_persists_for_existing_and_new_logins_and_busy_rows_conflict(pool: PgPool) {
    let (browser, app) = setup(pool.clone()).await;
    register(&browser, "alice").await;
    let first = login(&browser, "alice").await;
    let second = login(&browser, "alice").await;
    assert_eq!(
        call(
            &app,
            "PUT",
            "/api/user/sessions/settings",
            Some(access(&first)),
            true,
            json!({"session_auto_logout":false}),
            None
        )
        .await
        .0,
        StatusCode::OK
    );
    assert_eq!(
        browser.login_sessions(access(&first)).await.unwrap()["session_auto_logout"],
        false
    );
    let third = login(&browser, "alice").await;
    let weekly: Vec<bool> =
        sqlx::query_scalar("SELECT weekly_logout FROM core_identity.web_sessions ORDER BY sid")
            .fetch_all(&pool)
            .await
            .unwrap();
    assert_eq!(weekly, vec![false; 3]);
    let mut blocker = pool.begin().await.unwrap();
    sqlx::query("SELECT sid FROM core_identity.web_sessions WHERE sid=$1 FOR UPDATE")
        .bind(sid(&second))
        .execute(&mut *blocker)
        .await
        .unwrap();
    assert!(matches!(
        browser
            .revoke_login_sessions(access(&first), Some(sid(&second)), None)
            .await,
        Err(BrowserAuthError::Identity(IdentityError::Conflict))
    ));
    blocker.rollback().await.unwrap();
    browser.current_user(access(&second)).await.unwrap();
    assert_eq!(
        call(
            &app,
            "PUT",
            "/api/user/sessions/settings",
            Some(access(&third)),
            true,
            json!({"session_auto_logout":true,"user_id":99}),
            None
        )
        .await
        .0,
        StatusCode::BAD_REQUEST
    );
    assert_eq!(
        call(
            &app,
            "PUT",
            "/api/user/sessions/settings",
            Some(access(&third)),
            true,
            json!({"session_auto_logout":true}),
            None
        )
        .await
        .0,
        StatusCode::OK
    );
    let weekly: Vec<bool> =
        sqlx::query_scalar("SELECT weekly_logout FROM core_identity.web_sessions")
            .fetch_all(&pool)
            .await
            .unwrap();
    assert_eq!(weekly, vec![true; 3]);
}
#[sqlx::test(migrations = false)]
async fn current_session_assurance_is_rechecked_on_every_operation(pool: PgPool) {
    let (browser, _app) = setup(pool.clone()).await;
    register(&browser, "alice").await;
    let first = login(&browser, "alice").await;
    let setup = browser.mfa_setup(access(&first)).await.unwrap();
    let now: i64 =
        sqlx::query_scalar("SELECT floor(extract(epoch FROM clock_timestamp()))::bigint")
            .fetch_one(&pool)
            .await
            .unwrap();
    let secret = Secret::try_from_base32(&setup.secret).unwrap();
    let otp = Builder::new()
        .with_algorithm(Algorithm::SHA1)
        .with_digits(6)
        .with_step_duration(30)
        .with_skew(1)
        .with_secret(secret.as_bytes())
        .build()
        .unwrap()
        .generate(now as u64)
        .to_string();
    let live = browser.mfa_enable(access(&first), &otp).await.unwrap();
    browser.login_sessions(access(&live)).await.unwrap();
    // Change only the durable factor revision in this isolated owner fixture.
    // The live bearer keeps its auth generation but its proof is now stale.
    sqlx::query("UPDATE core_identity.mfa_factors SET revision=revision+1 WHERE user_id=$1")
        .bind(live.data["user"]["id"].as_i64().unwrap())
        .execute(&pool)
        .await
        .unwrap();
    assert!(browser.login_sessions(access(&live)).await.is_err());
    assert!(
        browser
            .revoke_login_sessions(access(&live), None, None)
            .await
            .is_err()
    );
    assert!(
        browser
            .update_login_session_settings(access(&live), false, None)
            .await
            .is_err()
    );
    assert!(browser.login_sessions(access(&first)).await.is_err());
    assert!(
        browser
            .revoke_login_sessions(access(&first), None, None)
            .await
            .is_err()
    );
    assert!(
        browser
            .update_login_session_settings(access(&first), false, None)
            .await
            .is_err()
    );
    let weekly: bool =
        sqlx::query_scalar("SELECT weekly_logout FROM core_identity.web_sessions WHERE sid=$1")
            .bind(sid(&first))
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(weekly);
}

#[sqlx::test(migrations = false)]
async fn api_keys_cannot_manage_browser_families(pool: PgPool) {
    use lmm_core::{
        accounts::{Account, AccountKind},
        funding::FundingPolicy,
    };
    let (browser, app) = setup(pool.clone()).await;
    register(&browser, "alice").await;
    let first = login(&browser, "alice").await;
    let store = IdentityStore::from_pool(pool.clone());
    let actor = store.authorize(access(&first)).await.unwrap();
    sqlx::query("INSERT INTO core_identity.developer_access(user_id,granted,source_reference) VALUES ($1,TRUE,'synthetic-session-management-test')").bind(actor.user_id).execute(&pool).await.unwrap();
    let key = store
        .issue_key(
            access(&first),
            Account {
                kind: AccountKind::Personal,
                id: actor.user_id,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    for (method, path, body) in [
        ("GET", "/api/user/sessions", Value::Null),
        ("POST", "/api/user/sessions/revoke-others", Value::Null),
        (
            "PUT",
            "/api/user/sessions/settings",
            json!({"session_auto_logout":false}),
        ),
    ] {
        assert_eq!(
            call(&app, method, path, Some(&key.secret), true, body, None)
                .await
                .0,
            StatusCode::FORBIDDEN
        );
    }
    browser.current_user(access(&first)).await.unwrap();
}
#[sqlx::test(migrations = false)]
async fn toggling_weekly_policy_never_revives_elapsed_families(pool: PgPool) {
    let (browser, _app) = setup(pool.clone()).await;
    register(&browser, "alice").await;
    let old = login(&browser, "alice").await;
    let current = login(&browser, "alice").await;
    browser
        .update_login_session_settings(access(&current), false, None)
        .await
        .unwrap();
    sqlx::query("UPDATE core_identity.web_sessions SET created_at=clock_timestamp()-interval '8 days',expires_at=clock_timestamp()+interval '21 days' WHERE sid=$1").bind(sid(&old)).execute(&pool).await.unwrap();
    browser.current_user(access(&old)).await.unwrap();
    browser
        .update_login_session_settings(access(&current), true, None)
        .await
        .unwrap();
    assert!(browser.current_user(access(&old)).await.is_err());
    assert!(browser.refresh(&old.refresh_token, None).await.is_err());
    browser
        .update_login_session_settings(access(&current), false, None)
        .await
        .unwrap();
    assert!(browser.current_user(access(&old)).await.is_err());
    assert!(browser.refresh(&old.refresh_token, None).await.is_err());
    let revoked: bool = sqlx::query_scalar(
        "SELECT revoked_at IS NOT NULL FROM core_identity.web_sessions WHERE sid=$1",
    )
    .bind(sid(&old))
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(revoked);
}
#[sqlx::test(migrations = false)]
async fn concurrent_revoke_others_has_one_authority_winner(pool: PgPool) {
    let (browser, _app) = setup(pool.clone()).await;
    register(&browser, "alice").await;
    let first = login(&browser, "alice").await;
    let second = login(&browser, "alice").await;
    let (a, b) = tokio::join!(
        browser.revoke_login_sessions(access(&first), None, None),
        browser.revoke_login_sessions(access(&second), None, None)
    );
    assert_ne!(a.is_ok(), b.is_ok());
    let count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_identity.web_sessions WHERE revoked_at IS NULL",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(count, 1);
    let first_live = browser.current_user(access(&first)).await.is_ok();
    let second_live = browser.current_user(access(&second)).await.is_ok();
    assert_ne!(first_live, second_live);
}
