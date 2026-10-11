use axum::{
    Router,
    body::{Body, to_bytes},
    http::{HeaderMap, Request, StatusCode},
};
use lmm_core::{
    accounts::{Account, AccountKind},
    funding::FundingPolicy,
    identity::{IdentityError, IdentityStore, LoginRequest},
    legacy_auth::{self, LegacyAuthConfig},
};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use tower::ServiceExt;

const ORIGIN: &str = "http://api.lmm.local";
fn config() -> LegacyAuthConfig {
    LegacyAuthConfig::new(ORIGIN, &[43; 32])
        .unwrap()
        .with_insecure_local_http()
        .unwrap()
}
async fn setup(pool: PgPool) -> (IdentityStore, Router) {
    let store = IdentityStore::from_pool(pool).with_registration_enabled(true);
    store.init_database().await.unwrap();
    store.check_schema().await.unwrap();
    let app = legacy_auth::router(store.clone(), config());
    (store, app)
}
async fn call(
    app: &Router,
    method: &str,
    path: &str,
    body: Option<Value>,
    headers: &[(&str, &str)],
) -> (StatusCode, HeaderMap, Value) {
    let mut builder = Request::builder().method(method).uri(path);
    if method == "POST" {
        builder = builder.header("origin", ORIGIN)
    }
    for (name, value) in headers {
        builder = builder.header(*name, *value)
    }
    let body = if let Some(body) = body {
        builder = builder.header("content-type", "application/json");
        Body::from(body.to_string())
    } else {
        Body::empty()
    };
    let response = app
        .clone()
        .oneshot(builder.body(body).unwrap())
        .await
        .unwrap();
    let status = response.status();
    let headers = response.headers().clone();
    let bytes = to_bytes(response.into_body(), 65536).await.unwrap();
    let value = serde_json::from_slice(&bytes).unwrap_or(Value::Null);
    (status, headers, value)
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
async fn register(app: &Router, name: &str) -> Value {
    let response = call(
        app,
        "POST",
        "/api/user/register",
        Some(json!({"username":name,"password":"abc12345","accepted_legal":true})),
        &[],
    )
    .await;
    assert_eq!(response.0, StatusCode::OK, "{:?}", response.2);
    assert!(response.1.get("set-cookie").is_none());
    assert!(response.2.get("data").is_none());
    response.2
}
async fn login(app: &Router, name: &str) -> (HeaderMap, Value) {
    let (status, headers, body) = call(
        app,
        "POST",
        "/api/user/login",
        Some(json!({"username":name,"password":"abc12345"})),
        &[],
    )
    .await;
    assert_eq!(status, StatusCode::OK, "{body:?}");
    (headers, body["data"].clone())
}

#[sqlx::test(migrations = false)]
async fn web_session_defaults_share_one_clock_and_enforce_thirty_day_limit(pool: PgPool) {
    let (store, _) = setup(pool.clone()).await;
    let credential = store.bootstrap_user(1, 0).await.unwrap();
    sqlx::query("UPDATE core_identity.credentials SET expires_at=statement_timestamp()+interval '15 minutes' WHERE id=$1")
        .bind(credential.id)
        .execute(&pool)
        .await
        .unwrap();
    let sid = "e".repeat(64);
    // Delay row creation inside this statement: a wall-clock default cannot
    // accidentally pass the equality check just because evaluation was fast.
    let (created_at_statement_start, same_activity_clock, exact_lifetime): (bool, bool, bool) =
        sqlx::query_as(
            "WITH delay AS MATERIALIZED (SELECT pg_sleep(0.01))
             INSERT INTO core_identity.web_sessions
                 (sid,user_id,user_version,current_credential_id,refresh_digest,ip,user_agent)
             SELECT $1,c.user_id,c.user_version,c.id,$2,'',''
             FROM core_identity.credentials c CROSS JOIN delay WHERE c.id=$3
             RETURNING created_at=statement_timestamp(),last_active_at=created_at,
                       expires_at=created_at+interval '30 days'",
        )
        .bind(&sid)
        .bind(vec![7_u8; 32])
        .bind(credential.id)
        .fetch_one(&pool)
        .await
        .unwrap();
    assert!(created_at_statement_start);
    assert!(same_activity_clock);
    assert!(exact_lifetime);

    let error = sqlx::query(
        "UPDATE core_identity.web_sessions
         SET expires_at=created_at+interval '30 days 1 microsecond' WHERE sid=$1",
    )
    .bind(&sid)
    .execute(&pool)
    .await
    .unwrap_err();
    assert_eq!(
        error.as_database_error().unwrap().code().as_deref(),
        Some("23514")
    );
    let still_exact: bool = sqlx::query_scalar(
        "SELECT expires_at=created_at+interval '30 days'
         FROM core_identity.web_sessions WHERE sid=$1",
    )
    .bind(&sid)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(still_exact);
}

#[sqlx::test(migrations = false)]
async fn browser_password_cookie_refresh_and_logout_flow(pool: PgPool) {
    let (store, app) = setup(pool.clone()).await;
    register(&app, "小明").await;
    let wrong = call(
        &app,
        "POST",
        "/api/user/login",
        Some(json!({"username":"小明","password":"wrong"})),
        &[],
    )
    .await;
    assert_eq!(wrong.0, StatusCode::UNAUTHORIZED);
    let (headers, bundle) = login(&app, "小明").await;
    assert_eq!(headers["cache-control"], "no-store");
    let raw = headers["set-cookie"].to_str().unwrap();
    assert!(raw.contains("Path=/api/user/auth"));
    assert!(raw.contains("HttpOnly"));
    assert!(raw.contains("SameSite=Strict"));
    assert!(!raw.contains("Secure"));
    let original_cookie = cookie(&headers);
    let sid = bundle["session"]["sid"].as_str().unwrap();
    let access = bundle["access_token"].as_str().unwrap();
    assert_eq!(sid.len(), 64);
    assert_ne!(access, original_cookie.split_once('=').unwrap().1);
    assert_eq!(bundle["token_type"], "Bearer");
    assert_eq!(bundle["user"]["username"], "小明");
    assert_eq!(bundle["user"]["role"], 1);
    assert!(bundle["user"].get("quota").is_none());
    for field in [
        "current",
        "login_method",
        "ip",
        "user_agent",
        "created_at",
        "last_active_at",
        "expires_at",
    ] {
        assert!(bundle["session"].get(field).is_some(), "{field}")
    }
    let ttl = bundle["access_expires_at"].as_i64().unwrap()
        - bundle["session"]["created_at"].as_i64().unwrap();
    assert!((898..=901).contains(&ttl));
    let bearer = format!("Bearer {access}");
    let current = call(
        &app,
        "GET",
        "/api/user/self",
        None,
        &[("authorization", &bearer)],
    )
    .await;
    assert_eq!(current.0, StatusCode::OK);
    assert_eq!(current.2["data"]["id"], bundle["user"]["id"]);
    let refresh_headers = [
        ("cookie", original_cookie.as_str()),
        ("x-auth-session", sid),
    ];
    let (a, b) = tokio::join!(
        call(
            &app,
            "POST",
            "/api/user/auth/refresh",
            None,
            &refresh_headers
        ),
        call(
            &app,
            "POST",
            "/api/user/auth/refresh",
            None,
            &refresh_headers
        )
    );
    assert_eq!(a.0, StatusCode::OK, "{:?}", a.2);
    assert_eq!(b.0, StatusCode::OK, "{:?}", b.2);
    assert_eq!(cookie(&a.1), cookie(&b.1));
    assert_eq!(a.2["data"]["access_token"], b.2["data"]["access_token"]);
    assert_eq!(a.2["data"]["session"]["sid"], sid);
    assert!(store.authorize(access).await.is_err());
    let updated_cookie = cookie(&a.1);
    let next_access = a.2["data"]["access_token"].as_str().unwrap();
    assert!(store.authorize(next_access).await.is_ok());
    let logout = call(
        &app,
        "POST",
        "/api/user/auth/logout",
        None,
        &[("cookie", &updated_cookie), ("x-auth-session", sid)],
    )
    .await;
    assert_eq!(logout.0, StatusCode::OK);
    assert!(
        logout.1["set-cookie"]
            .to_str()
            .unwrap()
            .contains("Max-Age=0")
    );
    assert!(store.authorize(next_access).await.is_err());
    let expired = call(
        &app,
        "POST",
        "/api/user/auth/refresh",
        None,
        &[("cookie", &updated_cookie)],
    )
    .await;
    assert_eq!(expired.0, StatusCode::UNAUTHORIZED);
    assert_eq!(expired.2["code"], "AUTH_SESSION_REVOKED");
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/user/auth/logout",
            None,
            &[("cookie", &updated_cookie)]
        )
        .await
        .0,
        StatusCode::OK
    );
    assert_eq!(
        call(&app, "POST", "/api/user/auth/logout", None, &[])
            .await
            .0,
        StatusCode::OK
    );
}
#[sqlx::test(migrations = false)]
async fn refresh_mismatch_unknown_and_old_replay_have_separate_effects(pool: PgPool) {
    let (store, app) = setup(pool.clone()).await;
    register(&app, "alice").await;
    let (headers, bundle) = login(&app, "alice").await;
    let original = cookie(&headers);
    let sid = bundle["session"]["sid"].as_str().unwrap();
    let access = bundle["access_token"].as_str().unwrap();
    let mismatch = call(
        &app,
        "POST",
        "/api/user/auth/refresh",
        None,
        &[("cookie", &original), ("x-auth-session", "another-login")],
    )
    .await;
    assert_eq!(mismatch.0, StatusCode::CONFLICT);
    assert_eq!(mismatch.2["code"], "AUTH_SESSION_MISMATCH");
    assert!(mismatch.1.get("set-cookie").is_none());
    assert!(store.authorize(access).await.is_ok());
    let forged = format!("new_api_refresh={sid}.lmmr_{}", "f".repeat(64));
    let invalid = call(
        &app,
        "POST",
        "/api/user/auth/refresh",
        None,
        &[("cookie", &forged)],
    )
    .await;
    assert_eq!(invalid.0, StatusCode::UNAUTHORIZED);
    assert!(store.authorize(access).await.is_ok());
    let renewed = call(
        &app,
        "POST",
        "/api/user/auth/refresh",
        None,
        &[("cookie", &original)],
    )
    .await;
    assert_eq!(renewed.0, StatusCode::OK);
    let current = renewed.2["data"]["access_token"].as_str().unwrap();
    sqlx::query("UPDATE core_identity.web_sessions SET previous_valid_until=clock_timestamp()-interval '1 second' WHERE sid=$1").bind(sid).execute(&pool).await.unwrap();
    let reuse = call(
        &app,
        "POST",
        "/api/user/auth/refresh",
        None,
        &[("cookie", &original)],
    )
    .await;
    assert_eq!(reuse.0, StatusCode::UNAUTHORIZED);
    assert_eq!(reuse.2["code"], "AUTH_SESSION_REVOKED");
    assert!(store.authorize(current).await.is_err());
}
#[sqlx::test(migrations = false)]
async fn browser_credentials_cannot_escalate_via_native_refresh_or_api_keys(pool: PgPool) {
    let (store, app) = setup(pool.clone()).await;
    register(&app, "bob").await;
    let (headers, bundle) = login(&app, "bob").await;
    let access = bundle["access_token"].as_str().unwrap();
    let principal = store.authorize(access).await.unwrap();
    sqlx::query("INSERT INTO core_identity.developer_access(user_id,granted,source_reference) VALUES ($1,TRUE,'synthetic-test-grant')")
        .bind(principal.user_id).execute(&pool).await.unwrap();
    assert!(matches!(
        store.refresh_session(access).await,
        Err(IdentityError::Forbidden)
    ));
    let key = store
        .issue_key(
            access,
            Account {
                kind: AccountKind::Personal,
                id: principal.user_id,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    let bearer = format!("Bearer {}", key.secret);
    let self_response = call(
        &app,
        "GET",
        "/api/user/self",
        None,
        &[("authorization", &bearer)],
    )
    .await;
    assert_eq!(self_response.0, StatusCode::FORBIDDEN);
    assert_eq!(
        call(
            &app,
            "GET",
            "/api/user/self",
            None,
            &[("cookie", &cookie(&headers))]
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    store.logout(access).await.unwrap();
    assert!(store.authorize(access).await.is_err());
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/user/auth/refresh",
            None,
            &[("cookie", &cookie(&headers))]
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    assert!(store.authorize(&key.secret).await.is_ok());
}
#[sqlx::test(migrations = false)]
async fn imported_bcrypt_username_email_and_policy_are_preserved(pool: PgPool) {
    let (store, app) = setup(pool.clone()).await;
    let bootstrap = store.bootstrap_user(773, 5).await.unwrap();
    store.logout(&bootstrap.secret).await.unwrap();
    let hash = bcrypt::hash("abc12345", 4).unwrap();
    sqlx::query("INSERT INTO core_identity.password_logins(login_name,user_id,password_hash) VALUES ('legacy-773',773,$1)").bind(&hash).execute(&pool).await.unwrap();
    sqlx::query("INSERT INTO core_identity.user_profiles(user_id,username,display_name,email,email_login_allowed,legacy_bcrypt_allowed) VALUES (773,'旧用户','真实名字','old@example.test',TRUE,TRUE)").execute(&pool).await.unwrap();
    let (_, bundle) = login(&app, "old@example.test").await;
    assert_eq!(bundle["user"]["id"], 773);
    assert_eq!(bundle["user"]["username"], "旧用户");
    assert_eq!(bundle["user"]["display_name"], "真实名字");
    assert_eq!(bundle["user"]["role"], 10);
    let upgraded: String = sqlx::query_scalar(
        "SELECT password_hash FROM core_identity.password_logins WHERE user_id=773",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(upgraded.starts_with("$argon2id$"));
    assert_ne!(hash, upgraded);
    let allowed: bool = sqlx::query_scalar(
        "SELECT legacy_bcrypt_allowed FROM core_identity.user_profiles WHERE user_id=773",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(!allowed);
    sqlx::query(
        "UPDATE core_identity.user_profiles SET second_factor_required=TRUE WHERE user_id=773",
    )
    .execute(&pool)
    .await
    .unwrap();
    let denied = call(
        &app,
        "POST",
        "/api/user/login",
        Some(json!({"username":"旧用户","password":"abc12345"})),
        &[],
    )
    .await;
    assert_eq!(denied.0, StatusCode::UNAUTHORIZED);
    assert!(
        store
            .login(&LoginRequest {
                login_name: "legacy-773".into(),
                password: "abc12345".into()
            })
            .await
            .is_err()
    );
    sqlx::query("UPDATE core_identity.user_profiles SET second_factor_required=FALSE,password_login_allowed=FALSE WHERE user_id=773").execute(&pool).await.unwrap();
    assert!(
        store
            .login(&LoginRequest {
                login_name: "legacy-773".into(),
                password: "abc12345".into()
            })
            .await
            .is_err()
    );
}
#[sqlx::test(migrations = false)]
async fn origin_ambiguity_and_registration_are_fail_closed(pool: PgPool) {
    let (_, app) = setup(pool.clone()).await;
    let response = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/user/register")
                .header("content-type", "application/json")
                .body(Body::from(
                    json!({"username":"a","password":"abc12345","accepted_legal":true}).to_string(),
                ))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    let duplicate_origin = call(
        &app,
        "POST",
        "/api/user/register",
        Some(json!({"username":"a","password":"abc12345","accepted_legal":true})),
        &[("origin", "http://evil.test")],
    )
    .await;
    assert_eq!(duplicate_origin.0, StatusCode::FORBIDDEN);
    register(&app, "a").await;
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_identity.accounts")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 1);
    let duplicate = call(
        &app,
        "POST",
        "/api/user/register",
        Some(json!({"username":"a","password":"abc12345","accepted_legal":true})),
        &[],
    )
    .await;
    assert_eq!(duplicate.0, StatusCode::CONFLICT);
    let after: i64 = sqlx::query_scalar("SELECT count(*) FROM core_identity.accounts")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(after, 1);
    let (headers, bundle) = login(&app, "a").await;
    let cookie = cookie(&headers);
    let duplicate_cookie = format!("{cookie}; {cookie}");
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/user/auth/refresh",
            None,
            &[("cookie", &duplicate_cookie)]
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    let access = bundle["access_token"].as_str().unwrap();
    let duplicate_bearer = format!("Bearer {access}");
    assert_eq!(
        call(
            &app,
            "GET",
            "/api/user/self",
            None,
            &[
                ("authorization", &duplicate_bearer),
                ("authorization", &duplicate_bearer)
            ]
        )
        .await
        .0,
        StatusCode::BAD_REQUEST
    );
    assert!(
        LegacyAuthConfig::new("http://evil.test", &[1; 32])
            .unwrap()
            .with_insecure_local_http()
            .is_err()
    );
    assert!(LegacyAuthConfig::new(ORIGIN, &[1; 8]).is_err());
}
#[sqlx::test(migrations = false)]
async fn family_expiry_and_disable_apply_to_native_authority_too(pool: PgPool) {
    let (store, app) = setup(pool.clone()).await;
    register(&app, "carol").await;
    let (headers, bundle) = login(&app, "carol").await;
    let access = bundle["access_token"].as_str().unwrap();
    let user = bundle["user"]["id"].as_i64().unwrap();
    sqlx::query("UPDATE core_identity.users SET active=FALSE WHERE id=$1")
        .bind(user)
        .execute(&pool)
        .await
        .unwrap();
    assert!(store.authorize(access).await.is_err());
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/user/auth/refresh",
            None,
            &[("cookie", &cookie(&headers))]
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    sqlx::query("UPDATE core_identity.users SET active=TRUE WHERE id=$1")
        .bind(user)
        .execute(&pool)
        .await
        .unwrap();
    let (headers, bundle) = login(&app, "carol").await;
    let access = bundle["access_token"].as_str().unwrap();
    let sid = bundle["session"]["sid"].as_str().unwrap();
    sqlx::query("UPDATE core_identity.web_sessions SET created_at=clock_timestamp()-interval '8 days',expires_at=clock_timestamp()+interval '10 days' WHERE sid=$1").bind(sid).execute(&pool).await.unwrap();
    assert!(store.authorize(access).await.is_err());
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/user/auth/refresh",
            None,
            &[("cookie", &cookie(&headers))]
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    let digests: Vec<Vec<u8>> =
        sqlx::query_scalar("SELECT refresh_digest FROM core_identity.web_sessions")
            .fetch_all(&pool)
            .await
            .unwrap();
    assert!(digests.iter().all(|hash| hash.len() == 32));
    assert!(
        !digests
            .iter()
            .any(|hash| hash == &Sha256::digest(access.as_bytes()).to_vec())
    );
}

#[sqlx::test(migrations = false)]
async fn consent_email_alias_and_policy_changes_are_enforced(pool: PgPool) {
    let (store, app) = setup(pool.clone()).await;
    for consent in [None, Some(false)] {
        let mut body = json!({"username":"consent","password":"abc12345"});
        if let Some(consent) = consent {
            body["accepted_legal"] = json!(consent);
        }
        let denied = call(&app, "POST", "/api/user/register", Some(body), &[]).await;
        assert_eq!(denied.0, StatusCode::BAD_REQUEST);
        assert_eq!(denied.2["code"], "AUTH_LEGAL_ACCEPTANCE_REQUIRED");
    }
    let registered=call(&app,"POST","/api/user/register",Some(json!({"username":"owner","password":"abc12345","accepted_legal":true,"email":"unverified@test.tld"})),&[]).await;
    assert_eq!(registered.0, StatusCode::OK);
    let (_, bundle) = login(&app, "owner").await;
    assert_eq!(bundle["user"]["email"], "");
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/user/login",
            Some(json!({"username":"unverified@test.tld","password":"abc12345"})),
            &[]
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    let id = bundle["user"]["id"].as_i64().unwrap();
    sqlx::query("UPDATE core_identity.user_profiles SET email='a@b.test',email_login_allowed=TRUE WHERE user_id=$1").bind(id).execute(&pool).await.unwrap();
    let collision = call(
        &app,
        "POST",
        "/api/user/register",
        Some(json!({"username":"a@b.test","password":"abc12345","accepted_legal":true})),
        &[],
    )
    .await;
    assert_eq!(collision.0, StatusCode::CONFLICT);
    let (_, alias) = login(&app, "a@b.test").await;
    assert_eq!(alias["user"]["id"], id);
    let access = alias["access_token"].as_str().unwrap();
    assert!(store.authorize(access).await.is_ok());
    sqlx::query(
        "UPDATE core_identity.user_profiles SET second_factor_required=TRUE WHERE user_id=$1",
    )
    .bind(id)
    .execute(&pool)
    .await
    .unwrap();
    assert!(store.authorize(access).await.is_err());
}

#[sqlx::test(migrations = false)]
async fn invalid_cookie_recovers_valid_bearer_logout_and_mutation_races_do_not_deadlock(
    pool: PgPool,
) {
    let (store, app) = setup(pool.clone()).await;
    register(&app, "racer").await;
    let (_, bundle) = login(&app, "racer").await;
    let access = bundle["access_token"].as_str().unwrap();
    let bearer = format!("Bearer {access}");
    let sid = bundle["session"]["sid"].as_str().unwrap();
    let result = call(
        &app,
        "POST",
        "/api/user/auth/logout",
        None,
        &[
            ("cookie", "new_api_refresh=invalid"),
            ("authorization", &bearer),
            ("x-auth-session", sid),
        ],
    )
    .await;
    assert_eq!(result.0, StatusCode::OK);
    assert!(
        result.1["set-cookie"]
            .to_str()
            .unwrap()
            .contains("Max-Age=0")
    );
    assert!(store.authorize(access).await.is_err());
    for _ in 0..3 {
        let (headers, bundle) = login(&app, "racer").await;
        let cookie = cookie(&headers);
        let access = bundle["access_token"].as_str().unwrap().to_owned();
        let bearer = format!("Bearer {access}");
        let sid = bundle["session"]["sid"].as_str().unwrap();
        let logout_headers = [("authorization", bearer.as_str()), ("x-auth-session", sid)];
        let refresh_headers = [("cookie", cookie.as_str()), ("x-auth-session", sid)];
        let (logout, refresh) = tokio::join!(
            call(&app, "POST", "/api/user/auth/logout", None, &logout_headers),
            call(
                &app,
                "POST",
                "/api/user/auth/refresh",
                None,
                &refresh_headers
            )
        );
        assert_eq!(logout.0, StatusCode::OK, "{:?}", logout.2);
        assert!(
            matches!(refresh.0, StatusCode::OK | StatusCode::UNAUTHORIZED),
            "{:?}",
            refresh.2
        );
        let final_cookie = if refresh.0 == StatusCode::OK {
            cookie_from(&refresh.1)
        } else {
            cookie.clone()
        };
        assert_eq!(
            call(
                &app,
                "POST",
                "/api/user/auth/refresh",
                None,
                &[("cookie", &final_cookie)]
            )
            .await
            .0,
            StatusCode::UNAUTHORIZED,
            "Logout must revoke the family regardless of the refresh winner"
        );
    }
}
fn cookie_from(headers: &HeaderMap) -> String {
    cookie(headers)
}

#[sqlx::test(migrations = false)]
async fn historical_bearer_logout_revokes_stable_family_after_refresh(pool: PgPool) {
    let (store, app) = setup(pool.clone()).await;
    register(&app, "history").await;
    let (original_headers, original_bundle) = login(&app, "history").await;
    let original_cookie = cookie(&original_headers);
    let old_access = original_bundle["access_token"].as_str().unwrap();
    let sid = original_bundle["session"]["sid"].as_str().unwrap();
    let renewed = call(
        &app,
        "POST",
        "/api/user/auth/refresh",
        None,
        &[("cookie", &original_cookie)],
    )
    .await;
    assert_eq!(renewed.0, StatusCode::OK);
    let renewed_cookie = cookie(&renewed.1);
    let new_access = renewed.2["data"]["access_token"].as_str().unwrap();
    assert!(store.authorize(old_access).await.is_err());
    assert!(store.refresh_session(old_access).await.is_err());
    assert!(store.authorize(new_access).await.is_ok());
    let old_bearer = format!("Bearer {old_access}");
    let logout = call(
        &app,
        "POST",
        "/api/user/auth/logout",
        None,
        &[("authorization", &old_bearer), ("x-auth-session", sid)],
    )
    .await;
    assert_eq!(logout.0, StatusCode::OK);
    assert!(store.authorize(new_access).await.is_err());
    assert!(store.authorize(old_access).await.is_err());
    let revoked = call(
        &app,
        "POST",
        "/api/user/auth/refresh",
        None,
        &[("cookie", &renewed_cookie)],
    )
    .await;
    assert_eq!(revoked.0, StatusCode::UNAUTHORIZED);
    assert_eq!(revoked.2["code"], "AUTH_SESSION_REVOKED");
    let count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_identity.web_session_credentials WHERE sid=$1",
    )
    .bind(sid)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(count, 2);
}

#[sqlx::test(migrations = false)]
async fn logout_history_never_accepts_unknown_expired_key_or_other_sid(pool: PgPool) {
    let (store, app) = setup(pool.clone()).await;
    register(&app, "limits").await;
    let (headers, bundle) = login(&app, "limits").await;
    let original_cookie = cookie(&headers);
    let original_access = bundle["access_token"].as_str().unwrap();
    let sid = bundle["session"]["sid"].as_str().unwrap();
    let user = bundle["user"]["id"].as_i64().unwrap();
    let renewed = call(
        &app,
        "POST",
        "/api/user/auth/refresh",
        None,
        &[("cookie", &original_cookie)],
    )
    .await;
    assert_eq!(renewed.0, StatusCode::OK);
    let mut current_cookie = cookie(&renewed.1);
    let current_access = renewed.2["data"]["access_token"].as_str().unwrap();
    let wrong_sid = call(
        &app,
        "POST",
        "/api/user/auth/logout",
        None,
        &[
            ("authorization", &format!("Bearer {original_access}")),
            ("x-auth-session", "caller-selected-family"),
        ],
    )
    .await;
    assert_eq!(wrong_sid.0, StatusCode::CONFLICT);
    assert_eq!(wrong_sid.2["code"], "AUTH_SESSION_MISMATCH");
    assert!(wrong_sid.1.get("set-cookie").is_none());
    assert!(store.authorize(current_access).await.is_ok());
    sqlx::query("INSERT INTO core_identity.developer_access(user_id,granted,source_reference) VALUES ($1,TRUE,'synthetic-test-grant')")
        .bind(user).execute(&pool).await.unwrap();
    let key = store
        .issue_key(
            current_access,
            Account {
                kind: AccountKind::Personal,
                id: user,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    assert!(
        sqlx::query(
            "INSERT INTO core_identity.web_session_credentials(credential_id,sid) VALUES ($1,$2)"
        )
        .bind(key.id)
        .bind(sid)
        .execute(&pool)
        .await
        .is_err()
    );
    assert!(
        sqlx::query("DELETE FROM core_identity.web_session_credentials WHERE sid=$1")
            .bind(sid)
            .execute(&pool)
            .await
            .is_err()
    );
    assert!(
        sqlx::query("TRUNCATE core_identity.web_session_credentials")
            .execute(&pool)
            .await
            .is_err()
    );
    let (other_headers, other_bundle) = login(&app, "limits").await;
    let other_sid = other_bundle["session"]["sid"].as_str().unwrap();
    let other_access = other_bundle["access_token"].as_str().unwrap();
    assert!(sqlx::query("UPDATE core_identity.web_session_credentials SET sid=$2 WHERE credential_id=(SELECT id FROM core_identity.credentials WHERE digest=$1)").bind(Sha256::digest(original_access.as_bytes()).to_vec()).bind(other_sid).execute(&pool).await.is_err());
    for token in [format!("lmms_{}", "d".repeat(64)), key.secret.clone()] {
        let response = call(
            &app,
            "POST",
            "/api/user/auth/logout",
            None,
            &[
                ("authorization", &format!("Bearer {token}")),
                ("x-auth-session", sid),
            ],
        )
        .await;
        assert_eq!(response.0, StatusCode::OK);
        let probe = call(
            &app,
            "POST",
            "/api/user/auth/refresh",
            None,
            &[("cookie", &current_cookie)],
        )
        .await;
        assert_eq!(probe.0, StatusCode::OK);
        current_cookie = cookie(&probe.1);
        assert!(store.authorize(other_access).await.is_ok());
    }
    sqlx::query("UPDATE core_identity.credentials SET expires_at=clock_timestamp()-interval '1 second' WHERE digest=$1").bind(Sha256::digest(original_access.as_bytes()).to_vec()).execute(&pool).await.unwrap();
    let expired = call(
        &app,
        "POST",
        "/api/user/auth/logout",
        None,
        &[
            ("authorization", &format!("Bearer {original_access}")),
            ("x-auth-session", sid),
        ],
    )
    .await;
    assert_eq!(expired.0, StatusCode::OK);
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/user/auth/refresh",
            None,
            &[("cookie", &current_cookie)]
        )
        .await
        .0,
        StatusCode::OK
    );
    assert!(store.authorize(other_access).await.is_ok());
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/user/auth/logout",
            None,
            &[("cookie", &cookie(&other_headers))]
        )
        .await
        .0,
        StatusCode::OK
    );
}
