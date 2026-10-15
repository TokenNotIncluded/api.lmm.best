use axum::{
    Router,
    body::{Body, to_bytes},
    http::{HeaderMap, Request, StatusCode},
};
use lmm_core::{
    accounts::{Account, AccountKind},
    funding::FundingPolicy,
    identity::{BrowserAuth, BrowserAuthError, IdentityError, IdentityStore, LoginRequest},
    legacy_auth::{self, LegacyAuthConfig},
};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::time::Duration;
use tower::ServiceExt;

const ORIGIN: &str = "http://api.lmm.local";
const OLD: &str = "old test password";
const NEW: &str = "new test password";

async fn setup(pool: &PgPool) -> (IdentityStore, BrowserAuth, Router) {
    let store = IdentityStore::from_pool(pool.clone())
        .with_registration_enabled(true)
        .with_password_slots(2)
        .unwrap();
    store.init_database().await.unwrap();
    // Explicit installer-owned fixture for the key-management/user projection dependency.
    sqlx::raw_sql(lmm_core::abuse::SCHEMA)
        .execute(pool)
        .await
        .unwrap();
    let browser = BrowserAuth::new(store.clone(), &[43; 32]).unwrap();
    let config = LegacyAuthConfig::new(ORIGIN, &[43; 32])
        .unwrap()
        .with_insecure_local_http()
        .unwrap();
    let app = legacy_auth::router(store.clone(), config);
    (store, browser, app)
}

async fn call(
    app: &Router,
    method: &str,
    path: &str,
    body: Value,
    headers: &[(&str, &str)],
) -> (StatusCode, HeaderMap, Value) {
    let mut request = Request::builder()
        .method(method)
        .uri(path)
        .header("origin", ORIGIN)
        .header("content-type", "application/json");
    for (name, value) in headers {
        request = request.header(*name, *value);
    }
    let response = app
        .clone()
        .oneshot(request.body(Body::from(body.to_string())).unwrap())
        .await
        .unwrap();
    let status = response.status();
    let headers = response.headers().clone();
    let bytes = to_bytes(response.into_body(), 65536).await.unwrap();
    (status, headers, serde_json::from_slice(&bytes).unwrap())
}

async fn register(app: &Router, name: &str) {
    assert_eq!(
        call(
            app,
            "POST",
            "/api/user/register",
            json!({"username":name,"password":OLD,"accepted_legal":true}),
            &[],
        )
        .await
        .0,
        StatusCode::OK
    );
}

async fn login(app: &Router, name: &str, password: &str) -> (HeaderMap, Value) {
    let (status, headers, body) = call(
        app,
        "POST",
        "/api/user/login",
        json!({"username":name,"password":password}),
        &[],
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    (headers, body["data"].clone())
}

fn cookie(headers: &HeaderMap) -> String {
    headers["set-cookie"]
        .to_str()
        .unwrap()
        .split(';')
        .next()
        .unwrap()
        .to_owned()
}

async fn state(pool: &PgPool) -> Value {
    // Only synthetic data. Rate-limit observations are intentionally excluded.
    // PostgreSQL sequence values are not transactional and are not compared.
    sqlx::query_scalar(
        "SELECT jsonb_build_object(
            'users',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM core_identity.users t),
            'passwords',(SELECT jsonb_agg(to_jsonb(t) ORDER BY user_id) FROM core_identity.password_logins t),
            'profiles',(SELECT jsonb_agg(to_jsonb(t) ORDER BY user_id) FROM core_identity.user_profiles t),
            'credentials',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM core_identity.credentials t),
            'sessions',(SELECT jsonb_agg(to_jsonb(t) ORDER BY sid) FROM core_identity.web_sessions t),
            'history',(SELECT jsonb_agg(to_jsonb(t) ORDER BY credential_id) FROM core_identity.web_session_credentials t),
            'audit',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM core_identity.audit t))",
    )
    .fetch_one(pool)
    .await
    .unwrap()
}

async fn family_bounds(pool: &PgPool, sid: &str) -> Value {
    sqlx::query_scalar(
        "SELECT jsonb_build_array(created_at,expires_at,weekly_logout,ip,user_agent)
         FROM core_identity.web_sessions WHERE sid=$1",
    )
    .bind(sid)
    .fetch_one(pool)
    .await
    .unwrap()
}

#[sqlx::test(migrations = false)]
async fn change_rotates_only_current_family_and_invalidates_old_user_generation(pool: PgPool) {
    let (store, _, app) = setup(&pool).await;
    register(&app, "alice").await;
    register(&app, "other").await;
    let (headers, first) = login(&app, "alice", OLD).await;
    let (second_headers, second) = login(&app, "alice", OLD).await;
    let (_, other) = login(&app, "other", OLD).await;
    let access = first["access_token"].as_str().unwrap();
    let sid = first["session"]["sid"].as_str().unwrap();
    let user = first["user"]["id"].as_i64().unwrap();
    let bounds = family_bounds(&pool, sid).await;
    sqlx::query(
        "INSERT INTO core_identity.developer_access(user_id,granted,source_reference)
         VALUES ($1,TRUE,'synthetic-password-test')",
    )
    .bind(user)
    .execute(&pool)
    .await
    .unwrap();
    let team = store.create_team(access).await.unwrap();
    let mut keys = Vec::new();
    for owner in [
        Account {
            kind: AccountKind::Personal,
            id: user,
        },
        Account {
            kind: AccountKind::Team,
            id: team,
        },
    ] {
        let policy = FundingPolicy::inherit_account_settings();
        let key = store.issue_key(access, owner, policy, 3600).await.unwrap();
        assert!(store.authorize(&key.secret).await.is_ok());
        keys.push(key);
    }
    let canonical: String =
        sqlx::query_scalar("SELECT login_name FROM core_identity.password_logins WHERE user_id=$1")
            .bind(user)
            .fetch_one(&pool)
            .await
            .unwrap();
    let native = store
        .login(&LoginRequest {
            login_name: canonical,
            password: OLD.into(),
        })
        .await
        .unwrap();
    let version: i64 =
        sqlx::query_scalar("SELECT auth_version FROM core_identity.users WHERE id=$1")
            .bind(user)
            .fetch_one(&pool)
            .await
            .unwrap();
    let bearer = format!("Bearer {access}");
    let (status, changed_headers, response) = call(
        &app,
        "PUT",
        "/api/user/self",
        json!({"original_password":OLD,"password":NEW,"display_name":"Changed"}),
        &[("authorization", &bearer), ("x-auth-session", sid)],
    )
    .await;
    assert_eq!(status, StatusCode::OK, "{response}");
    assert_eq!(response["success"], true);
    assert_eq!(response["data"]["session"]["sid"], sid);
    assert_eq!(response["data"]["user"]["display_name"], "Changed");
    assert_eq!(changed_headers["cache-control"], "no-store");
    assert_eq!(changed_headers["referrer-policy"], "no-referrer");
    assert_ne!(cookie(&changed_headers), cookie(&headers));
    assert_eq!(family_bounds(&pool, sid).await, bounds);
    let next = response["data"]["access_token"].as_str().unwrap();
    assert_ne!(next, access);
    assert!(store.authorize(next).await.is_ok());
    for old in [
        access,
        second["access_token"].as_str().unwrap(),
        &native.secret,
    ] {
        assert!(store.authorize(old).await.is_err());
    }
    for key in keys {
        assert!(store.authorize(&key.secret).await.is_err());
    }
    assert!(
        store
            .authorize(other["access_token"].as_str().unwrap())
            .await
            .is_ok()
    );
    for old_cookie in [cookie(&headers), cookie(&second_headers)] {
        assert_eq!(
            call(
                &app,
                "POST",
                "/api/user/auth/refresh",
                Value::Null,
                &[("cookie", &old_cookie)]
            )
            .await
            .0,
            StatusCode::UNAUTHORIZED
        );
    }
    // Historical bearers can revoke a family after an ordinary refresh, but
    // never across a password-generation change.
    let logout = call(
        &app,
        "POST",
        "/api/user/auth/logout",
        Value::Null,
        &[("authorization", &bearer), ("x-auth-session", sid)],
    )
    .await;
    assert_eq!(logout.0, StatusCode::OK);
    assert!(store.authorize(next).await.is_ok());
    let after_version: i64 =
        sqlx::query_scalar("SELECT auth_version FROM core_identity.users WHERE id=$1")
            .bind(user)
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(after_version, version + 1);
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/user/login",
            json!({"username":"alice","password":OLD}),
            &[]
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    login(&app, "alice", NEW).await;
    // Reconstruct the store, not a second in-memory identity store. This is
    // database-persistence coverage, not a separate-process restart claim.
    let restarted = BrowserAuth::new(IdentityStore::from_pool(pool.clone()), &[43; 32]).unwrap();
    let current_cookie = cookie(&changed_headers);
    let refreshed = restarted
        .refresh(current_cookie.split_once('=').unwrap().1, Some(sid))
        .await
        .unwrap();
    assert_eq!(refreshed.data["user"]["id"], user);
    let audits: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_identity.audit WHERE action='password.change'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(audits, 1);
}

#[sqlx::test(migrations = false)]
async fn malformed_wrong_password_and_wrong_authority_leave_identity_unchanged(pool: PgPool) {
    let (store, browser, app) = setup(&pool).await;
    register(&app, "alice").await;
    let (_, bundle) = login(&app, "alice", OLD).await;
    let access = bundle["access_token"].as_str().unwrap();
    let bearer = format!("Bearer {access}");
    let before = state(&pool).await;
    for body in [
        json!({"password":NEW}),
        json!({"password":NEW,"original_password":"wrong"}),
        json!({"password":"short","original_password":OLD}),
        json!({"password":"a".repeat(21),"original_password":OLD}),
        json!({"password":NEW,"original_password":OLD,"user_id":2}),
        json!({"password":NEW,"original_password":OLD,"language":"en"}),
        json!({"password":NEW,"original_password":OLD,"display_name":"bad\nname"}),
    ] {
        let result = call(
            &app,
            "PUT",
            "/api/user/self",
            body,
            &[("authorization", &bearer)],
        )
        .await;
        assert!(matches!(
            result.0,
            StatusCode::BAD_REQUEST | StatusCode::FORBIDDEN
        ));
        assert!(result.1.get("set-cookie").is_none());
        assert_eq!(result.2["success"], false);
    }
    for extra in [
        ("origin", "https://evil.invalid"),
        ("x-auth-session", "other-family"),
    ] {
        let result = call(
            &app,
            "PUT",
            "/api/user/self",
            json!({"password":NEW,"original_password":OLD}),
            &[("authorization", &bearer), extra],
        )
        .await;
        assert!(matches!(
            result.0,
            StatusCode::FORBIDDEN | StatusCode::CONFLICT
        ));
        assert!(result.1.get("set-cookie").is_none());
    }
    assert!(store.authorize(access).await.is_ok());
    let no_bearer = call(
        &app,
        "PUT",
        "/api/user/self",
        json!({"password":NEW,"original_password":OLD}),
        &[],
    )
    .await;
    assert_eq!(no_bearer.0, StatusCode::UNAUTHORIZED);
    assert!(no_bearer.1.get("set-cookie").is_none());
    assert_eq!(state(&pool).await, before);
    let native = store
        .register(&LoginRequest {
            login_name: "native".into(),
            password: OLD.into(),
        })
        .await
        .unwrap();
    let actor = store.authorize(access).await.unwrap();
    sqlx::query(
        "INSERT INTO core_identity.developer_access(user_id,granted,source_reference)
         VALUES ($1,TRUE,'synthetic-password-denial-test')",
    )
    .bind(actor.user_id)
    .execute(&pool)
    .await
    .unwrap();
    let key = store
        .issue_key(access, actor.owner, FundingPolicy::new_personal_key(), 3600)
        .await
        .unwrap();
    let snapshot = state(&pool).await;
    for secret in [&native.secret, &key.secret] {
        assert!(matches!(
            browser.change_password(secret, OLD, NEW, None, None).await,
            Err(BrowserAuthError::Identity(IdentityError::Forbidden))
        ));
    }
    assert_eq!(state(&pool).await, snapshot);
}

#[sqlx::test(migrations = false)]
async fn audit_failure_rolls_back_password_generation_family_and_display_name(pool: PgPool) {
    let (store, browser, app) = setup(&pool).await;
    register(&app, "alice").await;
    let (_, bundle) = login(&app, "alice", OLD).await;
    let access = bundle["access_token"].as_str().unwrap();
    sqlx::raw_sql(
        "CREATE FUNCTION core_identity.reject_password_audit() RETURNS trigger LANGUAGE plpgsql AS $$
         BEGIN IF NEW.action='password.change' THEN RAISE EXCEPTION 'synthetic password audit failure'; END IF;
         RETURN NEW; END $$;
         CREATE TRIGGER password_audit_fault BEFORE INSERT ON core_identity.audit
         FOR EACH ROW EXECUTE FUNCTION core_identity.reject_password_audit();",
    ).execute(&pool).await.unwrap();
    let before = state(&pool).await;
    assert!(
        browser
            .change_password(access, OLD, NEW, Some("Do not save"), None)
            .await
            .is_err()
    );
    assert_eq!(state(&pool).await, before);
    assert!(store.authorize(access).await.is_ok());
    login(&app, "alice", OLD).await;
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/user/login",
            json!({"username":"alice","password":NEW}),
            &[]
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
}

#[sqlx::test(migrations = false)]
async fn parallel_changes_have_one_winner_and_no_revived_credentials(pool: PgPool) {
    let (store, browser, app) = setup(&pool).await;
    register(&app, "alice").await;
    let (_, bundle) = login(&app, "alice", OLD).await;
    let access = bundle["access_token"].as_str().unwrap();
    let (one, two) = tokio::time::timeout(Duration::from_secs(5), async {
        tokio::join!(
            browser.change_password(access, OLD, NEW, None, None),
            browser.change_password(access, OLD, "second new password", None, None),
        )
    })
    .await
    .expect("password change must not deadlock");
    assert_eq!(usize::from(one.is_ok()) + usize::from(two.is_ok()), 1);
    let winner = one.or(two).unwrap();
    assert!(
        store
            .authorize(winner.data["access_token"].as_str().unwrap())
            .await
            .is_ok()
    );
    assert!(store.authorize(access).await.is_err());
    let count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_identity.audit WHERE action='password.change'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(count, 1);
}

#[sqlx::test(migrations = false)]
async fn native_password_endpoint_cannot_rotate_a_browser_credential(pool: PgPool) {
    let (store, _, app) = setup(&pool).await;
    register(&app, "alice").await;
    let (headers, bundle) = login(&app, "alice", OLD).await;
    let access = bundle["access_token"].as_str().unwrap();
    let before = state(&pool).await;
    assert!(matches!(
        store.change_password(access, OLD, NEW).await,
        Err(IdentityError::Forbidden)
    ));
    let native_app = lmm_core::identity_http::router(store.clone());
    let (status, response_headers, _) = call(
        &native_app,
        "POST",
        "/core/v1/auth/password",
        json!({"original_password":OLD,"password":NEW}),
        &[("authorization", &format!("Bearer {access}"))],
    )
    .await;
    assert_eq!(status, StatusCode::FORBIDDEN);
    assert_eq!(response_headers["cache-control"], "no-store");
    assert!(response_headers.get("set-cookie").is_none());
    assert_eq!(state(&pool).await, before);
    assert!(store.authorize(access).await.is_ok());
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/user/auth/refresh",
            Value::Null,
            &[("cookie", &cookie(&headers))],
        )
        .await
        .0,
        StatusCode::OK
    );
}

#[sqlx::test(migrations = false)]
async fn password_change_and_refresh_have_one_winner_without_deadlock(pool: PgPool) {
    let (store, browser, app) = setup(&pool).await;
    register(&app, "alice").await;
    let (headers, bundle) = login(&app, "alice", OLD).await;
    let access = bundle["access_token"].as_str().unwrap();
    let sid = bundle["session"]["sid"].as_str().unwrap();
    let old_cookie = cookie(&headers);
    let token = old_cookie.split_once('=').unwrap().1;
    let bounds = family_bounds(&pool, sid).await;
    let (changed, refreshed) = tokio::time::timeout(Duration::from_secs(5), async {
        tokio::join!(
            browser.change_password(access, OLD, NEW, None, Some(sid)),
            browser.refresh(token, Some(sid)),
        )
    })
    .await
    .expect("password change and refresh must not deadlock");
    assert_eq!(
        usize::from(changed.is_ok()) + usize::from(refreshed.is_ok()),
        1
    );
    let changed_won = changed.is_ok();
    let winner = changed.or(refreshed).unwrap();
    assert!(store.authorize(access).await.is_err());
    assert!(
        store
            .authorize(winner.data["access_token"].as_str().unwrap())
            .await
            .is_ok()
    );
    assert_eq!(family_bounds(&pool, sid).await, bounds);
    let audits: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_identity.audit WHERE action='password.change'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(audits, i64::from(changed_won));
    if changed_won {
        assert!(browser.refresh(token, Some(sid)).await.is_err());
        login(&app, "alice", NEW).await;
    } else {
        login(&app, "alice", OLD).await;
    }
}

#[sqlx::test(migrations = false)]
async fn conflicting_family_or_policy_locks_reject_without_waiting_or_writes(pool: PgPool) {
    let (_, browser, app) = setup(&pool).await;
    register(&app, "alice").await;
    let (_, bundle) = login(&app, "alice", OLD).await;
    let access = bundle["access_token"].as_str().unwrap();
    let user = bundle["user"]["id"].as_i64().unwrap();
    for lock in [
        "SELECT sid FROM core_identity.web_sessions WHERE user_id=$1 FOR UPDATE",
        "SELECT user_id FROM core_identity.user_profiles WHERE user_id=$1 FOR UPDATE",
        "SELECT user_id FROM core_identity.password_logins WHERE user_id=$1 FOR UPDATE",
    ] {
        let mut held = pool.begin().await.unwrap();
        sqlx::query(lock)
            .bind(user)
            .fetch_all(&mut *held)
            .await
            .unwrap();
        let before = state(&pool).await;
        let result = tokio::time::timeout(
            Duration::from_secs(5),
            browser.change_password(access, OLD, NEW, None, None),
        )
        .await
        .expect("row-lock conflicts must return without waiting");
        assert!(matches!(
            result,
            Err(BrowserAuthError::Identity(IdentityError::Conflict))
        ));
        assert_eq!(state(&pool).await, before);
        held.rollback().await.unwrap();
    }
    sqlx::query(
        "UPDATE core_identity.user_profiles SET second_factor_required=TRUE WHERE user_id=$1",
    )
    .bind(user)
    .execute(&pool)
    .await
    .unwrap();
    let before = state(&pool).await;
    assert!(
        browser
            .change_password(access, OLD, NEW, None, None)
            .await
            .is_err()
    );
    assert_eq!(state(&pool).await, before);
}
