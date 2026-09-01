use axum::{
    body::{Body, to_bytes},
    http::{Request, StatusCode, header},
};
use bcrypt::{DEFAULT_COST, hash};
use lmm_api_rs::auth::{
    AssistantL1ConfirmationError, AuthConfig, AuthErrorKind, AuthHttpState, DashboardAuth,
    PgValkeyDashboardAuth, UserAuthPolicyError, auth_router, enforce_user_auth,
};
use lmm_api_rs::routes::identity_catalog::{IdentityCatalogState, token_router};
use lmm_api_rs::{ClientIpKey, RequestContext};
use secrecy::SecretString;
use serde_json::Value;
use sqlx::{PgPool, Row, postgres::PgPoolOptions};
use std::{env, net::IpAddr, sync::Arc, time::Duration};
use totp_rs::{Algorithm, Secret, TOTP};
use tower::ServiceExt;

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use auth-listener-differential.sh"]
async fn auth_routes_preserve_postgres_and_valkey_control_plane() {
    let (database_url, valkey_url) = integration_urls();
    let pool = PgPoolOptions::new()
        .max_connections(4)
        .connect(&database_url)
        .await
        .expect("test PostgreSQL must be reachable");
    reset_schema(&pool).await;
    let password_hash = hash("correct horse", DEFAULT_COST).expect("bcrypt fixture");
    sqlx::query(
        "INSERT INTO users (id, username, password, display_name, role, status, email, \"group\", setting, auth_version) VALUES (7, 'alice', $1, 'Alice', 1, 1, 'alice@example.test', 'default', '{}', 1)",
    )
    .bind(password_hash)
    .execute(&pool)
    .await
    .expect("user fixture");

    let valkey = redis::Client::open(valkey_url.as_str()).expect("Valkey URL");
    let mut connection = valkey
        .get_multiplexed_async_connection()
        .await
        .expect("test Valkey must be reachable");
    redis::cmd("FLUSHDB")
        .query_async::<()>(&mut connection)
        .await
        .expect("isolated Valkey reset");

    let auth: Arc<dyn DashboardAuth> = Arc::new(
        PgValkeyDashboardAuth::new(pool.clone(), valkey.clone(), integration_config())
            .expect("auth adapter"),
    );
    let router =
        auth_router(AuthHttpState::new(Arc::clone(&auth), false).with_password_login_enabled(true))
            .merge(token_router(IdentityCatalogState::new(
                pool.clone(),
                Arc::clone(&auth),
            )));

    for (method, uri) in [("POST", "/api/user/login/2fa")] {
        let response = router
            .clone()
            .oneshot(
                Request::builder()
                    .method(method)
                    .uri(uri)
                    .body(Body::empty())
                    .expect("unverified route request"),
            )
            .await
            .expect("unverified route response");
        assert_eq!(response.status(), StatusCode::NOT_FOUND, "{method} {uri}");
    }

    let login = router
        .clone()
        .oneshot(json_request(
            "POST",
            "/api/user/login",
            r#"{"username":"alice","password":"correct horse"}"#,
        ))
        .await
        .expect("login response");
    assert_eq!(login.status(), StatusCode::OK);
    assert_eq!(login.headers()[header::CACHE_CONTROL], "no-store");
    let refresh_cookie = login.headers()[header::SET_COOKIE]
        .to_str()
        .expect("refresh cookie")
        .split(';')
        .next()
        .expect("cookie pair")
        .to_owned();
    let login_json = json_body(login).await;
    let access_token = login_json["data"]["access_token"]
        .as_str()
        .expect("access token")
        .to_owned();
    let sid = login_json["data"]["session"]["sid"]
        .as_str()
        .expect("session id")
        .to_owned();

    let personal_token = router
        .clone()
        .oneshot(
            Request::builder()
                .uri("/api/user/token")
                .header(header::AUTHORIZATION, format!("Bearer {access_token}"))
                .body(Body::empty())
                .expect("personal access token request"),
        )
        .await
        .expect("personal access token response");
    assert_eq!(personal_token.status(), StatusCode::OK);
    assert_eq!(
        personal_token.headers()[header::CACHE_CONTROL],
        "no-store, no-cache, must-revalidate, private, max-age=0"
    );
    let personal_token_body = json_body(personal_token).await;
    assert_eq!(personal_token_body["success"], true);
    assert!(personal_token_body["data"].as_str().is_some());

    let row = sqlx::query(
        "SELECT status, refresh_hash, user_auth_version FROM user_sessions WHERE sid = $1",
    )
    .bind(&sid)
    .fetch_one(&pool)
    .await
    .expect("authoritative session row");
    assert_eq!(row.get::<String, _>("status"), "active");
    assert_eq!(row.get::<String, _>("refresh_hash").trim().len(), 64);
    assert_eq!(row.get::<i64, _>("user_auth_version"), 1);
    let keys: Vec<String> = redis::cmd("KEYS")
        .arg("auth:session:*")
        .query_async(&mut connection)
        .await
        .expect("session cache key");
    assert_eq!(keys.len(), 1);

    let self_response = router
        .clone()
        .oneshot(
            Request::builder()
                .uri("/api/user/self")
                .header(header::AUTHORIZATION, format!("Bearer {access_token}"))
                .body(Body::empty())
                .expect("self request"),
        )
        .await
        .expect("self response");
    assert_eq!(self_response.status(), StatusCode::OK);
    assert_eq!(
        self_response.headers()["auth-version"],
        "864b7076dbcd0a3c01b5520316720ebf"
    );
    assert_eq!(json_body(self_response).await["data"]["username"], "alice");

    let mut refreshes = tokio::task::JoinSet::new();
    for user_agent in ["race-a", "race-b"] {
        let router = router.clone();
        let refresh_cookie = refresh_cookie.clone();
        let sid = sid.clone();
        let user_agent = user_agent.to_owned();
        refreshes.spawn(async move {
            let response = router
                .oneshot(with_client_ip(
                    Request::builder()
                        .method("POST")
                        .uri("/api/user/auth/refresh")
                        .header(header::COOKIE, refresh_cookie)
                        .header("x-auth-session", sid)
                        .header("x-real-ip", "127.0.0.2")
                        .header(header::USER_AGENT, &user_agent)
                        .body(Body::empty())
                        .expect("refresh request"),
                    "127.0.0.2",
                ))
                .await
                .expect("refresh response");
            (user_agent, response)
        });
    }
    let mut refresh_responses = Vec::new();
    while let Some(result) = refreshes.join_next().await {
        let (user_agent, refresh) = result.expect("concurrent refresh task");
        assert_eq!(refresh.status(), StatusCode::OK);
        refresh_responses.push((user_agent, refresh));
    }
    assert_eq!(refresh_responses.len(), 2);
    let rotated_cookie = refresh_responses[0].1.headers()[header::SET_COOKIE]
        .to_str()
        .expect("rotated cookie")
        .split(';')
        .next()
        .expect("cookie pair")
        .to_owned();
    let other_rotated_cookie = refresh_responses[1].1.headers()[header::SET_COOKIE]
        .to_str()
        .expect("concurrent rotated cookie")
        .split(';')
        .next()
        .expect("cookie pair")
        .to_owned();
    assert_eq!(rotated_cookie, other_rotated_cookie);
    let persisted_metadata: (String, String) =
        sqlx::query_as("SELECT ip, user_agent FROM user_sessions WHERE sid = $1")
            .bind(&sid)
            .fetch_one(&pool)
            .await
            .expect("login metadata remains authoritative after refresh");
    assert_eq!(
        persisted_metadata,
        ("127.0.0.1".to_owned(), "integration-login".to_owned())
    );
    let cache_metadata: Vec<Option<String>> = redis::cmd("HMGET")
        .arg(&keys[0])
        .arg("IP")
        .arg("UserAgent")
        .arg("PreviousIP")
        .arg("PreviousUserAgent")
        .query_async(&mut connection)
        .await
        .expect("Valkey preserves login metadata after refresh");
    assert_eq!(
        cache_metadata,
        vec![
            Some("127.0.0.1".to_owned()),
            Some("integration-login".to_owned()),
            Some("127.0.0.1".to_owned()),
            Some("integration-login".to_owned()),
        ]
    );
    let mut winner_response = None;
    let mut loser_response_count = 0;
    for (request_user_agent, response) in refresh_responses {
        let body = json_body(response).await;
        let response_user_agent = body["data"]["session"]["user_agent"]
            .as_str()
            .expect("response user agent");
        if request_user_agent == response_user_agent {
            assert_eq!(body["data"]["session"]["ip"], "127.0.0.2");
            winner_response = Some(body);
        } else {
            // The legacy CAS loser responds with the snapshot read before
            // either rotation, not with its own request metadata.
            assert_eq!(response_user_agent, "integration-login");
            loser_response_count += 1;
        }
    }
    assert_eq!(loser_response_count, 1);
    let refresh_json = winner_response.expect("winner response");
    let rotated_access = refresh_json["data"]["access_token"]
        .as_str()
        .expect("rotated access")
        .to_owned();
    let previous_valid_until: i64 =
        sqlx::query_scalar("SELECT previous_valid_until FROM user_sessions WHERE sid = $1")
            .bind(&sid)
            .fetch_one(&pool)
            .await
            .expect("rotation row");
    assert!(previous_valid_until > 0);

    let replay = router
        .clone()
        .oneshot(with_client_ip(
            Request::builder()
                .method("POST")
                .uri("/api/user/auth/refresh")
                .header(header::COOKIE, &refresh_cookie)
                .header("x-auth-session", &sid)
                .header("x-real-ip", "127.0.0.3")
                .header(header::USER_AGENT, "replay-request")
                .body(Body::empty())
                .expect("refresh replay request"),
            "127.0.0.3",
        ))
        .await
        .expect("refresh replay response");
    assert_eq!(replay.status(), StatusCode::OK);
    let replay_body = json_body(replay).await;
    assert_eq!(replay_body["data"]["session"]["ip"], "127.0.0.1");
    assert_eq!(
        replay_body["data"]["session"]["user_agent"],
        "integration-login"
    );
    let replay_persisted_metadata: (String, String) =
        sqlx::query_as("SELECT ip, user_agent FROM user_sessions WHERE sid = $1")
            .bind(&sid)
            .fetch_one(&pool)
            .await
            .expect("replay leaves login metadata untouched");
    assert_eq!(replay_persisted_metadata, persisted_metadata);
    let replay_cache_metadata: Vec<Option<String>> = redis::cmd("HMGET")
        .arg(&keys[0])
        .arg("IP")
        .arg("UserAgent")
        .query_async(&mut connection)
        .await
        .expect("replay leaves Valkey metadata untouched");
    assert_eq!(
        replay_cache_metadata,
        vec![
            Some("127.0.0.1".to_owned()),
            Some("integration-login".to_owned()),
        ]
    );

    let logout = router
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/user/auth/logout")
                .header(header::AUTHORIZATION, format!("Bearer {rotated_access}"))
                .header(header::COOKIE, rotated_cookie)
                .header("x-auth-session", &sid)
                .body(Body::empty())
                .expect("logout request"),
        )
        .await
        .expect("logout response");
    assert_eq!(logout.status(), StatusCode::OK);
    assert!(
        logout.headers()[header::SET_COOKIE]
            .to_str()
            .expect("clear cookie")
            .contains("Max-Age=0")
    );
    let status: String = sqlx::query_scalar("SELECT status FROM user_sessions WHERE sid = $1")
        .bind(&sid)
        .fetch_one(&pool)
        .await
        .expect("revoked row");
    assert_eq!(status, "revoked");
    let cached_status: String = redis::cmd("HGET")
        .arg(&keys[0])
        .arg("Status")
        .query_async(&mut connection)
        .await
        .expect("revoked tombstone");
    assert_eq!(cached_status, "revoked");
}

/// Exercises the shared dashboard resolver against real PostgreSQL and
/// Valkey.  In particular, a credential that identifies itself as our JWT is
/// never allowed to escape into the legacy opaque-PAT lookup.
#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use auth-listener-differential.sh"]
async fn dashboard_resolver_preserves_session_pat_and_userauth_boundaries() {
    let (database_url, valkey_url) = integration_urls();
    let pool = PgPoolOptions::new()
        .max_connections(4)
        .connect(&database_url)
        .await
        .expect("test PostgreSQL must be reachable");
    reset_schema(&pool).await;
    sqlx::query("ALTER TABLE users ALTER COLUMN access_token TYPE TEXT")
        .execute(&pool)
        .await
        .expect("fixture may retain JWT-shaped collision credential");
    let password_hash = hash("correct horse", DEFAULT_COST).expect("bcrypt fixture");
    sqlx::query(
        "INSERT INTO users (id, username, password, role, status, access_token, \"group\", setting, auth_version) VALUES (7, 'alice', $1, 1, 1, 'pat-alice', 'default', '{}', 1)",
    )
    .bind(password_hash)
    .execute(&pool)
    .await
    .expect("user fixture");
    let valkey = redis::Client::open(valkey_url.as_str()).expect("Valkey URL");
    let mut connection = valkey
        .get_multiplexed_async_connection()
        .await
        .expect("test Valkey must be reachable");
    redis::cmd("FLUSHDB")
        .query_async::<()>(&mut connection)
        .await
        .expect("isolated Valkey reset");
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(pool.clone(), valkey, integration_config())
            .expect("dashboard resolver"),
    );
    let router =
        auth_router(AuthHttpState::new(auth.clone(), false).with_password_login_enabled(true));
    let login = router
        .clone()
        .oneshot(json_request(
            "POST",
            "/api/user/login",
            r#"{"username":"alice","password":"correct horse"}"#,
        ))
        .await
        .expect("session login");
    assert_eq!(login.status(), StatusCode::OK);
    let session = json_body(login).await["data"]["access_token"]
        .as_str()
        .expect("access token")
        .to_owned();

    for authorization in [session.clone(), format!("Bearer {session}")] {
        let response = router
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/api/user/self")
                    .header(header::AUTHORIZATION, authorization)
                    .body(Body::empty())
                    .expect("session self request"),
            )
            .await
            .expect("session self response");
        assert_eq!(response.status(), StatusCode::OK, "session form must work");
    }
    for authorization in ["pat-alice".to_owned(), "Bearer pat-alice".to_owned()] {
        let response = router
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/api/user/self")
                    .header(header::AUTHORIZATION, authorization)
                    .body(Body::empty())
                    .expect("PAT self request"),
            )
            .await
            .expect("PAT self response");
        assert_eq!(response.status(), StatusCode::OK, "PAT form must work");
    }

    // The resolver intentionally returns a role-zero user so the HTTP handler
    // must apply Go's required-user policy before it can serialize `data`.
    // Exercise both credential families against the real PostgreSQL lookup.
    sqlx::query("UPDATE users SET role = 0 WHERE id = 7")
        .execute(&pool)
        .await
        .expect("guest fixture");
    for (credential_kind, authorization) in [
        ("session", format!("Bearer {session}")),
        ("PAT", "Bearer pat-alice".to_owned()),
    ] {
        let response = router
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/api/user/self")
                    .header(header::AUTHORIZATION, authorization)
                    .body(Body::empty())
                    .expect("guest self request"),
            )
            .await
            .expect("guest self response");
        assert_eq!(
            response.status(),
            StatusCode::FORBIDDEN,
            "{credential_kind}"
        );
        let body = json_body(response).await;
        assert_eq!(
            body["code"], "AUTH_INSUFFICIENT_PRIVILEGE",
            "{credential_kind}"
        );
        assert_eq!(body["message"], "Unauthorized, insufficient privileges");
        assert!(body.get("data").is_none(), "{credential_kind}");
    }

    sqlx::query("UPDATE users SET role = 2 WHERE id = 7")
        .execute(&pool)
        .await
        .expect("malformed role fixture");
    let response = router
        .clone()
        .oneshot(
            Request::builder()
                .uri("/api/user/self")
                .header(header::AUTHORIZATION, "Bearer pat-alice")
                .body(Body::empty())
                .expect("malformed PAT self request"),
        )
        .await
        .expect("malformed PAT self response");
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    let body = json_body(response).await;
    assert_eq!(body["code"], "AUTH_USER_INVALID");
    assert_eq!(body["message"], "Unauthorized, invalid user info");
    assert!(body.get("data").is_none());

    sqlx::query("UPDATE users SET role = 1, status = 2 WHERE id = 7")
        .execute(&pool)
        .await
        .expect("disabled PAT fixture");
    let response = router
        .clone()
        .oneshot(
            Request::builder()
                .uri("/api/user/self")
                .header(header::AUTHORIZATION, "Bearer pat-alice")
                .body(Body::empty())
                .expect("disabled PAT self request"),
        )
        .await
        .expect("disabled PAT self response");
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    let body = json_body(response).await;
    assert_eq!(body["code"], "AUTH_USER_DISABLED");
    assert_eq!(body["message"], "User has been banned");
    assert!(body.get("data").is_none());

    sqlx::query("UPDATE users SET role = 1, status = 1 WHERE id = 7")
        .execute(&pool)
        .await
        .expect("restore valid user");

    // Go validates internal sessions (including user status) before its
    // post-resolution UserAuth policy, so a disabled session is revoked at
    // the resolver boundary. Opaque PATs above retain AUTH_USER_DISABLED.
    sqlx::query("UPDATE users SET status = 2 WHERE id = 7")
        .execute(&pool)
        .await
        .expect("disable session user for self handler");
    let response = router
        .clone()
        .oneshot(
            Request::builder()
                .uri("/api/user/self")
                .header(header::AUTHORIZATION, format!("Bearer {session}"))
                .body(Body::empty())
                .expect("disabled session self request"),
        )
        .await
        .expect("disabled session self response");
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    let body = json_body(response).await;
    assert_eq!(
        body["code"], "AUTH_SESSION_REVOKED",
        "disabled internal session follows Go's resolver order"
    );
    assert_eq!(
        body["message"],
        "Unauthorized, not logged in and no access token provided"
    );
    assert!(body.get("data").is_none());
    assert_eq!(
        auth.self_user(SecretString::from(session.clone()))
            .await
            .expect_err("general required resolver remains strict for disabled sessions")
            .kind,
        AuthErrorKind::SessionRevoked
    );
    sqlx::query("UPDATE users SET status = 1 WHERE id = 7")
        .execute(&pool)
        .await
        .expect("restore session user after handler assertion");

    let mut invalid_jwt = session.clone();
    invalid_jwt.push('x');
    sqlx::query("UPDATE users SET access_token = $2 WHERE id = $1")
        .bind(7_i64)
        .bind(&invalid_jwt)
        .execute(&pool)
        .await
        .expect("collision fixture");
    assert_eq!(
        auth.self_user(SecretString::from(invalid_jwt))
            .await
            .expect_err("invalid dashboard JWT must not fall through to PAT")
            .kind,
        AuthErrorKind::Unauthorized
    );
    sqlx::query("UPDATE users SET status = 2 WHERE id = 7")
        .execute(&pool)
        .await
        .expect("disable session user");
    assert_eq!(
        auth.self_user(SecretString::from(session.clone()))
            .await
            .expect_err("disabled session user")
            .kind,
        AuthErrorKind::SessionRevoked
    );
    sqlx::query("UPDATE users SET status = 1 WHERE id = 7")
        .execute(&pool)
        .await
        .expect("restore session user");
    sqlx::query("UPDATE users SET access_token = 'pat-alice' WHERE id = 7")
        .execute(&pool)
        .await
        .expect("restore PAT fixture");

    sqlx::query("UPDATE user_sessions SET expires_at = 0 WHERE user_id = 7")
        .execute(&pool)
        .await
        .expect("expire session");
    assert_eq!(
        auth.self_user(SecretString::from(session.clone()))
            .await
            .expect_err("expired session")
            .kind,
        AuthErrorKind::SessionRevoked
    );
    sqlx::query("UPDATE user_sessions SET expires_at = $2, status = 'revoked', revoked_at = $2 WHERE user_id = $1")
        .bind(7_i64)
        .bind(i64::MAX / 4)
        .execute(&pool)
        .await
        .expect("revoke session");
    assert_eq!(
        auth.self_user(SecretString::from(session.clone()))
            .await
            .expect_err("revoked session")
            .kind,
        AuthErrorKind::SessionRevoked
    );
    let response = router
        .clone()
        .oneshot(
            Request::builder()
                .uri("/api/user/self")
                .header(header::AUTHORIZATION, format!("Bearer {session}"))
                .body(Body::empty())
                .expect("revoked session self request"),
        )
        .await
        .expect("revoked session self response");
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    let body = json_body(response).await;
    assert_eq!(body["code"], "AUTH_SESSION_REVOKED");
    assert_eq!(
        body["message"],
        "Unauthorized, not logged in and no access token provided"
    );
    assert!(body.get("data").is_none());

    sqlx::query("UPDATE users SET status = 2 WHERE id = 7")
        .execute(&pool)
        .await
        .expect("disable PAT user");
    assert_eq!(
        auth.self_user(SecretString::from("pat-alice".to_owned()))
            .await
            .expect_err("disabled PAT owner")
            .kind,
        AuthErrorKind::UserDisabled
    );
    sqlx::query("UPDATE users SET status = 1, role = 0 WHERE id = 7")
        .execute(&pool)
        .await
        .expect("guest PAT fixture");
    let guest = auth
        .self_user(SecretString::from("pat-alice".to_owned()))
        .await
        .expect("resolver returns authoritative user for policy");
    assert_eq!(
        enforce_user_auth(&guest),
        Err(UserAuthPolicyError::InsufficientPrivilege)
    );
}

#[allow(dead_code)] // Covers an unmounted handler retained for future verification.
async fn exercise_two_factor_permissions_and_concurrency(pool: &PgPool, valkey: &redis::Client) {
    const TOTP_SECRET: &str = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP";
    sqlx::query("UPDATE users SET role = 10 WHERE id = 7")
        .execute(pool)
        .await
        .expect("admin fixture");
    sqlx::query("INSERT INTO casbin_rule (ptype, v0, v1, v2, v3) VALUES ('p', 'role:admin', 'channel', 'read', 'allow'), ('p', 'role:admin', 'channel', 'operate', 'allow'), ('p', 'role:admin', 'channel', 'write', 'allow'), ('p', 'user:7', 'channel', 'write', 'deny'), ('p', 'user:7', 'channel', 'sensitive_write', 'allow')")
        .execute(pool)
        .await
        .expect("permission overrides");
    sqlx::query("INSERT INTO two_fas (user_id, secret, is_enabled, created_at, updated_at) VALUES (7, $1, TRUE, NOW(), NOW())")
        .bind(TOTP_SECRET)
        .execute(pool)
        .await
        .expect("2FA fixture");
    let auth = PgValkeyDashboardAuth::new(pool.clone(), valkey.clone(), integration_config())
        .expect("2FA adapter");
    let router =
        auth_router(AuthHttpState::new(Arc::new(auth), false).with_password_login_enabled(true));
    let begin = router
        .clone()
        .oneshot(json_request(
            "POST",
            "/api/user/login",
            r#"{"username":" alice@example.test ","password":"correct horse"}"#,
        ))
        .await
        .expect("2FA begin");
    assert_eq!(begin.status(), StatusCode::OK);
    let begin_json = json_body(begin).await;
    assert_eq!(begin_json["data"]["require_2fa"], true);
    let flow_token = begin_json["data"]["flow_token"]
        .as_str()
        .expect("flow token")
        .to_owned();
    let totp = TOTP::new(
        Algorithm::SHA1,
        6,
        1,
        30,
        Secret::Encoded(TOTP_SECRET.to_owned())
            .to_bytes()
            .expect("TOTP secret"),
    )
    .expect("TOTP");
    let body = serde_json::json!({
        "flow_token": flow_token,
        "code": totp.generate_current().expect("current TOTP")
    })
    .to_string();
    let verified = router
        .clone()
        .oneshot(dynamic_json_request("/api/user/login/2fa", body.clone()))
        .await
        .expect("2FA verify");
    assert_eq!(verified.status(), StatusCode::OK);
    let verified_json = json_body(verified).await;
    assert_eq!(verified_json["data"]["session"]["login_method"], "2fa");
    assert_eq!(
        verified_json["data"]["user"]["permissions"]["admin_permissions"]["channel"]["read"],
        true
    );
    assert_eq!(
        verified_json["data"]["user"]["permissions"]["admin_permissions"]["channel"]["write"],
        false
    );
    assert_eq!(
        verified_json["data"]["user"]["permissions"]["admin_permissions"]["channel"]["sensitive_write"],
        true
    );
    let replay = router
        .clone()
        .oneshot(dynamic_json_request("/api/user/login/2fa", body))
        .await
        .expect("2FA replay");
    assert_eq!(replay.status(), StatusCode::OK);
    assert_eq!(json_body(replay).await["success"], false);

    let backup_hash = hash("backup-code-1", DEFAULT_COST).expect("backup hash");
    sqlx::query("INSERT INTO two_fa_backup_codes (user_id, code_hash, is_used, created_at) VALUES (7, $1, FALSE, NOW())")
        .bind(backup_hash)
        .execute(pool)
        .await
        .expect("backup fixture");
    let backup_begin = router
        .clone()
        .oneshot(json_request(
            "POST",
            "/api/user/login",
            r#"{"username":"alice","password":"correct horse"}"#,
        ))
        .await
        .expect("backup begin");
    let backup_flow = json_body(backup_begin).await["data"]["flow_token"]
        .as_str()
        .expect("backup flow")
        .to_owned();
    let backup_verified = router
        .clone()
        .oneshot(dynamic_json_request(
            "/api/user/login/2fa",
            serde_json::json!({"flow_token": backup_flow, "code": "backup-code-1"}).to_string(),
        ))
        .await
        .expect("backup verify");
    assert_eq!(backup_verified.status(), StatusCode::OK);
    assert!(
        sqlx::query_scalar::<_, bool>("SELECT is_used FROM two_fa_backup_codes WHERE user_id = 7",)
            .fetch_one(pool)
            .await
            .expect("backup consumed")
    );

    sqlx::query("UPDATE two_fas SET failed_attempts = 0, locked_until = NULL WHERE user_id = 7")
        .execute(pool)
        .await
        .expect("2FA failure fixture reset");
    let failure_begin = router
        .clone()
        .oneshot(json_request(
            "POST",
            "/api/user/login",
            r#"{"username":"alice","password":"correct horse"}"#,
        ))
        .await
        .expect("failure flow begin");
    let failure_flow = json_body(failure_begin).await["data"]["flow_token"]
        .as_str()
        .expect("failure flow")
        .to_owned();
    let mut failures = tokio::task::JoinSet::new();
    for _ in 0..8 {
        let router = router.clone();
        let flow_token = failure_flow.clone();
        failures.spawn(async move {
            router
                .oneshot(dynamic_json_request(
                    "/api/user/login/2fa",
                    serde_json::json!({"flow_token": flow_token, "code": "invalid-code"})
                        .to_string(),
                ))
                .await
                .expect("invalid 2FA response")
        });
    }
    while let Some(result) = failures.join_next().await {
        assert_eq!(result.expect("invalid 2FA task").status(), StatusCode::OK);
    }
    let lock_state = sqlx::query(
        "SELECT failed_attempts, locked_until IS NOT NULL AND locked_until > NOW() AS is_locked FROM two_fas WHERE user_id = 7",
    )
    .fetch_one(pool)
    .await
    .expect("2FA lock state");
    assert_eq!(lock_state.get::<i64, _>("failed_attempts"), 5);
    assert!(lock_state.get::<bool, _>("is_locked"));
    let locked = router
        .clone()
        .oneshot(dynamic_json_request(
            "/api/user/login/2fa",
            serde_json::json!({"flow_token": failure_flow, "code": "invalid-code"}).to_string(),
        ))
        .await
        .expect("locked 2FA response");
    assert_eq!(
        json_body(locked).await["message"],
        "账户已被锁定，请稍后重试"
    );

    for statement in [
        "DELETE FROM user_sessions",
        "DELETE FROM two_fas",
        "UPDATE users SET role = 1 WHERE id = 7",
    ] {
        sqlx::query(statement)
            .execute(pool)
            .await
            .expect("concurrency fixture reset");
    }
    assert_concurrent_limit(pool, valkey, 1, 100, "active session limit").await;
    sqlx::query("DELETE FROM user_sessions")
        .execute(pool)
        .await
        .expect("issuance fixture reset");
    assert_concurrent_limit(pool, valkey, 100, 1, "issuance limit").await;
}

async fn assert_concurrent_limit(
    pool: &PgPool,
    valkey: &redis::Client,
    active_session_limit: i64,
    issuance_limit: i64,
    label: &str,
) {
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(
            pool.clone(),
            valkey.clone(),
            AuthConfig {
                active_session_limit,
                issuance_limit,
                ..integration_config()
            },
        )
        .expect("limit adapter"),
    );
    let router = auth_router(AuthHttpState::new(auth, false).with_password_login_enabled(true));
    let mut tasks = tokio::task::JoinSet::new();
    for _ in 0..8 {
        let router = router.clone();
        tasks.spawn(async move {
            router
                .oneshot(json_request(
                    "POST",
                    "/api/user/login",
                    r#"{"username":"alice","password":"correct horse"}"#,
                ))
                .await
                .expect("concurrent login")
                .status()
        });
    }
    let mut successes = 0;
    while let Some(result) = tasks.join_next().await {
        successes += usize::from(result.expect("login task") == StatusCode::OK);
    }
    assert_eq!(successes, 1, "advisory lock must make {label} hard");
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM user_sessions WHERE status = 'active'")
            .fetch_one(pool)
            .await
            .expect("active count"),
        1
    );
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use auth-listener-differential.sh"]
async fn assistant_l1_confirmation_should_be_session_bound_and_single_use() {
    let (database_url, valkey_url) = integration_urls();
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database_url)
        .await
        .expect("test PostgreSQL must be reachable");
    reset_schema(&pool).await;
    let valkey = redis::Client::open(valkey_url).expect("Valkey URL");
    let auth =
        PgValkeyDashboardAuth::new(pool, valkey, integration_config()).expect("auth adapter");
    let token = auth
        .create_assistant_l1_confirmation(
            7,
            "browser-session",
            r#"{"user_statement":"concrete use case","recommendation":"concrete recommendation for administrator review"}"#,
            Duration::from_secs(60),
        )
        .await
        .expect("confirmation token");

    let wrong_session = auth
        .consume_assistant_l1_confirmation(
            7,
            "different-session",
            SecretString::from(token.clone()),
        )
        .await;
    assert_eq!(wrong_session, Err(AssistantL1ConfirmationError::Invalid));

    let payload = auth
        .consume_assistant_l1_confirmation(7, "browser-session", SecretString::from(token.clone()))
        .await
        .expect("matching confirmation consumes once");
    assert!(payload.contains("concrete use case"));

    let replay = auth
        .consume_assistant_l1_confirmation(7, "browser-session", SecretString::from(token))
        .await;
    assert_eq!(replay, Err(AssistantL1ConfirmationError::Invalid));
}

fn integration_config() -> AuthConfig {
    AuthConfig {
        session_secret: SecretString::from("integration-SESSION-secret-2026!".to_owned()),
        dependency_timeout: Duration::from_secs(3),
        critical_rate_limit: 1_000,
        ..AuthConfig::default()
    }
}

fn integration_urls() -> (String, String) {
    assert_eq!(
        env::var("LMM_AUTH_TEST_ALLOW_SCHEMA_RESET").as_deref(),
        Ok("1"),
        "integration test requires LMM_AUTH_TEST_ALLOW_SCHEMA_RESET=1"
    );
    (
        env::var("LMM_AUTH_TEST_DATABASE_URL")
            .expect("integration test requires LMM_AUTH_TEST_DATABASE_URL"),
        env::var("LMM_AUTH_TEST_VALKEY_URL")
            .expect("integration test requires LMM_AUTH_TEST_VALKEY_URL"),
    )
}

fn json_request(method: &str, uri: &str, body: &'static str) -> Request<Body> {
    with_test_context(
        Request::builder()
            .method(method)
            .uri(uri)
            .header(header::CONTENT_TYPE, "application/json")
            .header("x-real-ip", "127.0.0.1")
            .header(header::USER_AGENT, "integration-login")
            .body(Body::from(body))
            .expect("JSON request"),
    )
}

fn dynamic_json_request(uri: &str, body: String) -> Request<Body> {
    with_test_context(
        Request::builder()
            .method("POST")
            .uri(uri)
            .header(header::CONTENT_TYPE, "application/json")
            .header("x-real-ip", "127.0.0.1")
            .header(header::USER_AGENT, "integration-2fa")
            .body(Body::from(body))
            .expect("dynamic JSON request"),
    )
}

fn with_test_context(request: Request<Body>) -> Request<Body> {
    with_client_ip(request, "127.0.0.1")
}

fn with_client_ip(mut request: Request<Body>, ip: &str) -> Request<Body> {
    let client_ip = ip.parse::<IpAddr>().expect("test client IP");
    request.extensions_mut().insert(RequestContext {
        request_id: "integration-test-request".to_owned(),
        client_ip: Some(client_ip),
    });
    request
        .extensions_mut()
        .insert(ClientIpKey(client_ip.to_string()));
    request
}

async fn json_body(response: axum::response::Response) -> Value {
    serde_json::from_slice(
        &to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body"),
    )
    .expect("JSON response")
}

async fn reset_schema(pool: &PgPool) {
    for statement in [
        "DROP TABLE IF EXISTS user_sessions",
        "DROP TABLE IF EXISTS auth_flows",
        "DROP TABLE IF EXISTS two_fa_backup_codes",
        "DROP TABLE IF EXISTS two_fas",
        "DROP TABLE IF EXISTS casbin_rule",
        "DROP TABLE IF EXISTS users",
        r#"CREATE TABLE users (
            id BIGINT PRIMARY KEY, username TEXT UNIQUE, password TEXT NOT NULL,
            display_name TEXT, role BIGINT DEFAULT 1, status BIGINT DEFAULT 1,
            email TEXT, github_id TEXT, discord_id TEXT, oidc_id TEXT, wechat_id TEXT,
            telegram_id TEXT, access_token VARCHAR(32), quota BIGINT DEFAULT 0, used_quota BIGINT DEFAULT 0,
            request_count BIGINT DEFAULT 0, "group" VARCHAR(64) DEFAULT 'default',
            aff_code VARCHAR(32), aff_count BIGINT DEFAULT 0, aff_quota BIGINT DEFAULT 0,
            aff_history BIGINT DEFAULT 0, inviter_id BIGINT, deleted_at TIMESTAMPTZ,
            linux_do_id TEXT, setting TEXT, stripe_customer VARCHAR(64),
            last_login_at BIGINT DEFAULT 0, console_activated_at BIGINT NOT NULL DEFAULT 0,
            auth_version BIGINT NOT NULL DEFAULT 1
        )"#,
        r#"CREATE TABLE two_fas (
            id BIGINT PRIMARY KEY GENERATED ALWAYS AS IDENTITY, user_id BIGINT NOT NULL,
            secret VARCHAR(255) NOT NULL, is_enabled BOOLEAN NOT NULL DEFAULT FALSE,
            failed_attempts BIGINT DEFAULT 0, locked_until TIMESTAMPTZ,
            last_used_at TIMESTAMPTZ, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ,
            deleted_at TIMESTAMPTZ
        )"#,
        r#"CREATE TABLE two_fa_backup_codes (
            id BIGINT PRIMARY KEY GENERATED ALWAYS AS IDENTITY, user_id BIGINT NOT NULL,
            code_hash VARCHAR(255) NOT NULL, is_used BOOLEAN DEFAULT FALSE,
            used_at TIMESTAMPTZ, created_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ
        )"#,
        r#"CREATE TABLE auth_flows (
            id BIGINT PRIMARY KEY GENERATED ALWAYS AS IDENTITY, token_hash CHAR(64) NOT NULL UNIQUE,
            purpose VARCHAR(32) NOT NULL, provider VARCHAR(64), intent VARCHAR(16), user_id BIGINT,
            session_id VARCHAR(64), payload TEXT, created_at TIMESTAMPTZ,
            expires_at TIMESTAMPTZ NOT NULL, consumed_at TIMESTAMPTZ
        )"#,
        r#"CREATE TABLE casbin_rule (
            id BIGINT PRIMARY KEY GENERATED ALWAYS AS IDENTITY, ptype VARCHAR(100),
            v0 VARCHAR(100), v1 VARCHAR(100), v2 VARCHAR(100), v3 VARCHAR(100),
            v4 VARCHAR(100), v5 VARCHAR(100)
        )"#,
        "CREATE UNIQUE INDEX idx_users_access_token ON users (access_token)",
        r#"CREATE TABLE user_sessions (
            sid VARCHAR(64) PRIMARY KEY, user_id BIGINT NOT NULL, version BIGINT NOT NULL,
            user_auth_version BIGINT NOT NULL, status VARCHAR(16) NOT NULL,
            refresh_hash CHAR(64) NOT NULL, previous_refresh_hash VARCHAR(64),
            previous_valid_until BIGINT NOT NULL DEFAULT 0, login_method VARCHAR(32) NOT NULL,
            ip VARCHAR(64), user_agent TEXT, created_at BIGINT NOT NULL,
            last_active_at BIGINT NOT NULL, expires_at BIGINT NOT NULL,
            revoked_at BIGINT NOT NULL DEFAULT 0, revoked_reason VARCHAR(64)
        )"#,
    ] {
        sqlx::query(statement)
            .execute(pool)
            .await
            .expect("isolated auth schema statement");
    }
}
