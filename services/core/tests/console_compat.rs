//! Real PostgreSQL coverage for the persisted main announcement/setup contract.
use axum::{
    Router,
    body::{Body, to_bytes},
    http::{HeaderMap, Request, StatusCode},
};
use lmm_core::{
    accounts::{Account, AccountKind},
    console_compat::{self, ConsoleCompat, MandatoryAnnouncement, announcement_revision},
    core_schema,
    funding::FundingPolicy,
    identity::{IdentityStore, IssuedCredential},
};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use tower::ServiceExt;
#[path = "common/runtime.rs"]
mod runtime;

async fn setup(pool: &PgPool) -> (IdentityStore, ConsoleCompat, Router) {
    core_schema::init(pool).await.unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    let state = ConsoleCompat::new(pool.clone(), identity.clone());
    let app = console_compat::router(state.clone());
    (identity, state, app)
}
async fn option(pool: &PgPool, key: &str, value: &str) {
    sqlx::query("INSERT INTO core_console.options(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,revision=core_console.options.revision+1,updated_at=statement_timestamp()")
        .bind(key).bind(value).execute(pool).await.unwrap();
}
fn announcement(id: i64, generation: &str) -> Value {
    json!({"id":id,"content":format!("Synthetic announcement {id}"),"extra":"Test only","publishDate":"2025-01-01T00:00:00Z","mandatory":true,"ackRevision":generation})
}
async fn publish(pool: &PgPool, items: &[Value]) {
    option(
        pool,
        "console_setting.announcements",
        &json!(items).to_string(),
    )
    .await;
}
async fn call(
    app: &Router,
    method: &str,
    path: &str,
    secret: Option<&str>,
    body: Body,
    declared: Option<usize>,
) -> (StatusCode, HeaderMap, Value) {
    let mut request = Request::builder()
        .method(method)
        .uri(path)
        .header("content-type", "application/json");
    if let Some(secret) = secret {
        request = request.header("authorization", format!("Bearer {secret}"));
    }
    if let Some(length) = declared {
        request = request.header("content-length", length);
    }
    let response = app
        .clone()
        .oneshot(request.body(body).unwrap())
        .await
        .unwrap();
    let status = response.status();
    let headers = response.headers().clone();
    let bytes = to_bytes(response.into_body(), 1 << 20).await.unwrap();
    (
        status,
        headers,
        serde_json::from_slice(&bytes).unwrap_or(Value::Null),
    )
}
async fn get(app: &Router, path: &str, secret: Option<&str>) -> (StatusCode, HeaderMap, Value) {
    call(app, "GET", path, secret, Body::empty(), None).await
}
async fn ack(
    app: &Router,
    secret: &str,
    id: i64,
    revision: &str,
) -> (StatusCode, HeaderMap, Value) {
    call(
        app,
        "POST",
        "/api/user/self/announcements/read",
        Some(secret),
        Body::from(json!({"id":id,"revision":revision}).to_string()),
        None,
    )
    .await
}

#[test]
fn revision_matches_go_html_unicode_and_pinned_generation_bytes() {
    let mut item = MandatoryAnnouncement {
        id: 7,
        content: "<公告>&\u{2028}\u{2029}".into(),
        extra: "extra".into(),
        publish_date: "2026-01-01T00:00:00Z".into(),
        ..Default::default()
    };
    let expected =
        "[7,\"\\u003c公告\\u003e\\u0026\\u2028\\u2029\",\"extra\",\"2026-01-01T00:00:00Z\"]";
    assert_eq!(
        announcement_revision(&item).unwrap(),
        format!("{:x}", Sha256::digest(expected.as_bytes()))
    );
    item.ack_revision = " \u{2003}generation1\n".into();
    let pinned = announcement_revision(&item).unwrap();
    assert_eq!(
        pinned,
        format!("{:x}", Sha256::digest(br#"[7,"generation1"]"#))
    );
    item.content.push('!');
    assert_eq!(announcement_revision(&item).unwrap(), pinned);
    item.ack_revision = "generation2".into();
    assert_ne!(announcement_revision(&item).unwrap(), pinned);
    item.ack_revision.clear();
    let old = announcement_revision(&item).unwrap();
    item.content.push('!');
    assert_ne!(announcement_revision(&item).unwrap(), old);
}

#[sqlx::test(migrations = false)]
async fn persisted_options_precedence_notice_and_invalid_configuration(pool: PgPool) {
    let (identity, state, app) = setup(&pool).await;
    let user = identity.bootstrap_user(1, 0).await.unwrap();
    option(
        &pool,
        "Announcements",
        &json!([announcement(1, "")]).to_string(),
    )
    .await;
    assert_eq!(
        state.announcements(&user.secret, None).await.unwrap().len(),
        1
    );
    option(&pool, "console_setting.announcements", "").await;
    assert!(
        state
            .announcements(&user.secret, None)
            .await
            .unwrap()
            .is_empty()
    );
    option(&pool, "console_setting.announcements", "null").await;
    assert_eq!(
        get(&app, "/api/user/self/announcements", Some(&user.secret))
            .await
            .2["data"],
        json!([])
    );
    option(&pool, "console_setting.announcements", "{broken").await;
    assert_eq!(
        get(&app, "/api/user/self/announcements", Some(&user.secret))
            .await
            .0,
        StatusCode::SERVICE_UNAVAILABLE
    );
    publish(&pool, &[announcement(2, "g1")]).await;
    assert_eq!(
        state.announcements(&user.secret, None).await.unwrap()[0].id,
        2
    );
    option(&pool, "Notice", "Synthetic persisted notice <b>example</b>").await;
    let notice = get(&app, "/api/notice", None).await;
    assert_eq!(notice.0, StatusCode::OK);
    assert_eq!(
        notice.2["data"],
        "Synthetic persisted notice <b>example</b>"
    );
    option(&pool, "Notice", "changed").await;
    assert_eq!(get(&app, "/api/notice", None).await.2["data"], "changed");
    assert!(
        notice.1["cache-control"]
            .to_str()
            .unwrap()
            .contains("no-store")
    );
    assert_eq!(notice.1["auth-version"], "864b7076dbcd0a3c01b5520316720ebf");
}

#[sqlx::test(migrations = false)]
async fn ordering_uses_instants_then_ids_and_rejects_bad_mandatory_dates(pool: PgPool) {
    let (identity, state, _) = setup(&pool).await;
    let user = identity.bootstrap_user(1, 0).await.unwrap();
    publish(
        &pool,
        &[
            json!({"id":2,"mandatory":true,"publishDate":"2025-01-01T01:00:00+01:00"}),
            json!({"id":1,"mandatory":true,"publishDate":"2025-01-01T00:00:00Z","read_at":123}),
            json!({"id":3,"mandatory":true,"publishDate":"2099-01-01T00:00:00Z"}),
            json!({"id":4,"mandatory":false,"publishDate":"invalid"}),
            Value::Null,
        ],
    )
    .await;
    let items = state.announcements(&user.secret, None).await.unwrap();
    assert_eq!(items.iter().map(|v| v.id).collect::<Vec<_>>(), [1, 2]);
    assert!(items.iter().all(|v| v.read_at == 0));
    for date in ["invalid", "2025-02-30T00:00:00Z", "2025-01-01T00:00:60Z"] {
        publish(
            &pool,
            &[json!({"id":1,"mandatory":true,"publishDate":date})],
        )
        .await;
        assert!(
            state.announcements(&user.secret, None).await.is_err(),
            "{date}"
        );
    }
}

#[sqlx::test(migrations = false)]
async fn acknowledgements_are_ordered_persistent_and_isolated_between_accounts(pool: PgPool) {
    let (identity, state, app) = setup(&pool).await;
    let alice = identity.bootstrap_user(1, 0).await.unwrap();
    let bob = identity.bootstrap_user(2, 0).await.unwrap();
    publish(&pool, &[announcement(2, "g1"), announcement(1, "g1")]).await;
    let items = state.announcements(&alice.secret, None).await.unwrap();
    assert_eq!(
        ack(&app, &alice.secret, 2, &items[1].revision).await.0,
        StatusCode::CONFLICT
    );
    assert_eq!(
        ack(&app, &alice.secret, 1, &"0".repeat(64)).await.0,
        StatusCode::CONFLICT
    );
    let read = ack(&app, &alice.secret, 1, &items[0].revision).await;
    assert_eq!(read.0, StatusCode::OK);
    assert!(read.2["data"][0]["read_at"].as_i64().unwrap() > 0);
    assert_eq!(read.2["data"][1]["read_at"], 0);
    let replay = ack(&app, &alice.secret, 1, &items[0].revision).await;
    assert_eq!(read.2, replay.2);
    assert!(
        state
            .announcements(&bob.secret, None)
            .await
            .unwrap()
            .iter()
            .all(|v| v.read_at == 0)
    );
    let another_instance = ConsoleCompat::new(pool.clone(), IdentityStore::from_pool(pool.clone()));
    assert!(
        another_instance
            .announcements(&alice.secret, None)
            .await
            .unwrap()[0]
            .read_at
            > 0
    );
    let response = call(
        &app,
        "POST",
        "/api/user/self/announcements/read",
        Some(&bob.secret),
        Body::from(json!({"id":1,"revision":items[0].revision,"user_id":1}).to_string()),
        None,
    )
    .await;
    assert_eq!(response.0, StatusCode::OK);
    let rows: i64 = sqlx::query_scalar("SELECT count(*) FROM core_console.announcement_reads")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(rows, 2);
}

#[sqlx::test(migrations = false)]
async fn pinned_edits_preserve_receipts_bumps_and_legacy_edits_require_confirmation(pool: PgPool) {
    let (identity, state, _) = setup(&pool).await;
    let user = identity.bootstrap_user(1, 0).await.unwrap();
    publish(&pool, &[announcement(1, "g1")]).await;
    let first = state
        .announcements(&user.secret, None)
        .await
        .unwrap()
        .remove(0);
    state
        .acknowledge(&user.secret, 1, &first.revision)
        .await
        .unwrap();
    let mut edited = announcement(1, "g1");
    edited["content"] = json!("Corrected text");
    publish(&pool, &[edited.clone()]).await;
    assert!(state.announcements(&user.secret, None).await.unwrap()[0].read_at > 0);
    edited["ackRevision"] = json!("g2");
    publish(&pool, &[edited.clone()]).await;
    assert_eq!(
        state.announcements(&user.secret, None).await.unwrap()[0].read_at,
        0
    );
    assert!(
        state
            .acknowledge(&user.secret, 1, &first.revision)
            .await
            .is_err()
    );
    edited["ackRevision"] = json!("");
    publish(&pool, &[edited.clone()]).await;
    let old = state
        .announcements(&user.secret, None)
        .await
        .unwrap()
        .remove(0);
    state
        .acknowledge(&user.secret, 1, &old.revision)
        .await
        .unwrap();
    edited["content"] = json!("New legacy text");
    publish(&pool, &[edited]).await;
    assert_eq!(
        state.announcements(&user.secret, None).await.unwrap()[0].read_at,
        0
    );
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_console.announcement_reads")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 2);
}

#[sqlx::test(migrations = false)]
async fn concurrent_duplicate_confirmation_has_one_receipt_and_equal_snapshots(pool: PgPool) {
    let (identity, state, _) = setup(&pool).await;
    let user = identity.bootstrap_user(1, 0).await.unwrap();
    publish(&pool, &[announcement(1, "g1")]).await;
    let item = state
        .announcements(&user.secret, None)
        .await
        .unwrap()
        .remove(0);
    let (a, b) = tokio::join!(
        state.acknowledge(&user.secret, 1, &item.revision),
        state.acknowledge(&user.secret, 1, &item.revision)
    );
    assert_eq!(a.unwrap(), b.unwrap());
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_console.announcement_reads")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 1);
}

#[sqlx::test(migrations = false)]
async fn platform_roles_follow_native_authority_and_admin_target_boundary(pool: PgPool) {
    let (identity, _, app) = setup(&pool).await;
    let user = identity.bootstrap_user(1, 0).await.unwrap();
    let service4 = identity.bootstrap_user(2, 4).await.unwrap();
    let admin = identity.bootstrap_user(3, 5).await.unwrap();
    let other_admin = identity.bootstrap_user(4, 5).await.unwrap();
    let root = identity.bootstrap_user(5, 6).await.unwrap();
    let other_root = identity.bootstrap_user(6, 6).await.unwrap();
    assert_eq!(
        identity
            .authorize(&admin.secret)
            .await
            .unwrap()
            .platform_level,
        5
    );
    assert_eq!(
        identity
            .authorize(&root.secret)
            .await
            .unwrap()
            .platform_level,
        6
    );
    for secret in [&user.secret, &service4.secret] {
        assert_eq!(
            get(&app, "/api/user/1/announcements", Some(secret)).await.0,
            StatusCode::FORBIDDEN
        );
        assert_eq!(
            get(&app, "/api/user/self/announcements", Some(secret))
                .await
                .0,
            StatusCode::OK
        );
    }
    assert_eq!(
        get(&app, "/api/user/1/announcements", Some(&admin.secret))
            .await
            .0,
        StatusCode::OK
    );
    for id in [3, 4, 5] {
        assert_eq!(
            get(
                &app,
                &format!("/api/user/{id}/announcements"),
                Some(&admin.secret)
            )
            .await
            .0,
            StatusCode::FORBIDDEN
        );
    }
    assert_eq!(
        get(&app, "/api/user/6/announcements", Some(&root.secret))
            .await
            .0,
        StatusCode::OK
    );
    assert_eq!(
        get(&app, "/api/user/5/announcements", Some(&other_root.secret))
            .await
            .0,
        StatusCode::OK
    );
    assert_eq!(
        get(&app, "/api/user/1/announcements", Some(&other_admin.secret))
            .await
            .0,
        StatusCode::OK
    );
}

#[sqlx::test(migrations = false)]
async fn authentication_precedes_body_and_storage_access_and_rejects_api_keys(pool: PgPool) {
    let (identity, _, app) = setup(&pool).await;
    let user = identity.bootstrap_user(1, 0).await.unwrap();
    let key = identity
        .issue_key(
            &user.secret,
            Account {
                kind: AccountKind::Personal,
                id: 1,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    for secret in [None, Some("unknown"), Some(key.secret.as_str())] {
        let response = call(
            &app,
            "POST",
            "/api/user/self/announcements/read",
            secret,
            Body::from("{"),
            Some(99999),
        )
        .await;
        assert!(matches!(
            response.0,
            StatusCode::UNAUTHORIZED | StatusCode::FORBIDDEN
        ));
    }
    for declared in [Some(1025), None] {
        let chunks = tokio_stream::iter(vec![
            Ok::<_, std::convert::Infallible>(vec![b' '; 600]),
            Ok(vec![b' '; 425]),
        ]);
        assert_eq!(
            call(
                &app,
                "POST",
                "/api/user/self/announcements/read",
                Some(&user.secret),
                Body::from_stream(chunks),
                declared
            )
            .await
            .0,
            StatusCode::PAYLOAD_TOO_LARGE
        );
    }
    sqlx::query("DROP TABLE core_console.options")
        .execute(&pool)
        .await
        .unwrap();
    assert_eq!(
        get(&app, "/api/user/self/announcements", Some("unknown"))
            .await
            .0,
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(
        get(&app, "/api/user/self/announcements", Some(&user.secret))
            .await
            .0,
        StatusCode::SERVICE_UNAVAILABLE
    );
    assert_eq!(
        get(&app, "/api/notice", None).await.0,
        StatusCode::SERVICE_UNAVAILABLE
    );
}

#[sqlx::test(migrations = false)]
async fn expired_revoked_and_disabled_sessions_cannot_read_or_confirm(pool: PgPool) {
    let (identity, _, app) = setup(&pool).await;
    publish(&pool, &[announcement(1, "g1")]).await;
    let expired = identity.bootstrap_user(1, 0).await.unwrap();
    let revoked = identity.bootstrap_user(2, 0).await.unwrap();
    let disabled = identity.bootstrap_user(3, 0).await.unwrap();
    sqlx::query("UPDATE core_identity.credentials SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1").bind(expired.id).execute(&pool).await.unwrap();
    sqlx::query("UPDATE core_identity.credentials SET revoked_at=clock_timestamp() WHERE id=$1")
        .bind(revoked.id)
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query("UPDATE core_identity.users SET active=false WHERE id=3")
        .execute(&pool)
        .await
        .unwrap();
    for IssuedCredential { secret, .. } in [expired, revoked, disabled] {
        assert_eq!(
            get(&app, "/api/user/self/announcements", Some(&secret))
                .await
                .0,
            StatusCode::UNAUTHORIZED
        );
        assert_eq!(
            ack(&app, &secret, 1, &"0".repeat(64)).await.0,
            StatusCode::UNAUTHORIZED
        );
    }
}

#[sqlx::test(migrations = false)]
async fn setup_requires_marker_active_root_and_real_schema_and_post_never_writes(pool: PgPool) {
    let (identity, state, app) = setup(&pool).await;
    let fresh = state.setup_status().await.unwrap();
    assert!(!fresh.status && !fresh.root_init);
    assert!(
        console_compat::record_initialization(&pool, "synthetic-v1")
            .await
            .is_err()
    );
    identity.bootstrap_user(1, 0).await.unwrap();
    assert!(!state.setup_status().await.unwrap().root_init);
    identity.bootstrap_user(2, 6).await.unwrap();
    let legacy = state.setup_status().await.unwrap();
    assert!(!legacy.status && legacy.root_init);
    let denied = call(
        &app,
        "POST",
        "/api/setup",
        None,
        Body::from(r#"{"username":"intruder","password":"do-not-consume"}"#),
        None,
    )
    .await;
    assert_eq!(denied.0, StatusCode::FORBIDDEN);
    assert_eq!(denied.2["code"], "SETUP_CLI_ONLY");
    let records: i64 = sqlx::query_scalar("SELECT count(*) FROM core_console.initialization")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(records, 0);
    console_compat::record_initialization(&pool, "synthetic-v1")
        .await
        .unwrap();
    assert!(state.setup_status().await.unwrap().status);
    let before: (String, String) =
        sqlx::query_as("SELECT version,initialized_at::text FROM core_console.initialization")
            .fetch_one(&pool)
            .await
            .unwrap();
    console_compat::record_initialization(&pool, "synthetic-v1")
        .await
        .unwrap();
    assert!(
        console_compat::record_initialization(&pool, "changed")
            .await
            .is_err()
    );
    let after: (String, String) =
        sqlx::query_as("SELECT version,initialized_at::text FROM core_console.initialization")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(before, after);
    sqlx::query("UPDATE core_identity.users SET active=false WHERE id=2")
        .execute(&pool)
        .await
        .unwrap();
    assert_eq!(
        get(&app, "/api/setup", None).await.0,
        StatusCode::SERVICE_UNAVAILABLE
    );
}

#[sqlx::test(migrations = false)]
async fn concurrent_offline_setup_completion_is_singleton_and_does_not_create_roots(pool: PgPool) {
    let (identity, state, _) = setup(&pool).await;
    identity.bootstrap_user(1, 6).await.unwrap();
    let (a, b) = tokio::join!(
        console_compat::record_initialization(&pool, "synthetic-v1"),
        console_compat::record_initialization(&pool, "synthetic-v1")
    );
    a.unwrap();
    b.unwrap();
    assert!(state.setup_status().await.unwrap().status);
    let counts:(i64,i64)=sqlx::query_as("SELECT (SELECT count(*) FROM core_console.initialization),(SELECT count(*) FROM core_identity.users WHERE platform_role='superadmin')").fetch_one(&pool).await.unwrap();
    assert_eq!(counts, (1, 1));
    sqlx::query("ALTER TABLE core_console.initialization ADD COLUMN unexpected BOOLEAN")
        .execute(&pool)
        .await
        .unwrap();
    assert!(state.setup_status().await.is_err());
}

#[sqlx::test(migrations = false)]
async fn failed_receipt_write_rolls_back_and_does_not_return_read_snapshot(pool: PgPool) {
    let (identity, state, app) = setup(&pool).await;
    let user = identity.bootstrap_user(1, 0).await.unwrap();
    publish(&pool, &[announcement(1, "g1")]).await;
    let item = state
        .announcements(&user.secret, None)
        .await
        .unwrap()
        .remove(0);
    sqlx::raw_sql("CREATE FUNCTION core_console.fail_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic receipt failure'; END $$; CREATE TRIGGER fail_write BEFORE INSERT ON core_console.announcement_reads FOR EACH ROW EXECUTE FUNCTION core_console.fail_write();").execute(&pool).await.unwrap();
    assert_eq!(
        ack(&app, &user.secret, 1, &item.revision).await.0,
        StatusCode::SERVICE_UNAVAILABLE
    );
    assert_eq!(
        state.announcements(&user.secret, None).await.unwrap()[0].read_at,
        0
    );
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_console.announcement_reads")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 0);
}

#[sqlx::test(migrations = false)]
async fn acknowledgement_returns_committed_snapshot_without_a_second_status_query(pool: PgPool) {
    let (identity, state, app) = setup(&pool).await;
    let user = identity.bootstrap_user(1, 0).await.unwrap();
    publish(&pool, &[announcement(1, "g1")]).await;
    let item = state
        .announcements(&user.secret, None)
        .await
        .unwrap()
        .remove(0);
    // Owner-only failure injection in this synthetic DB: a later status read
    // cannot work, but the already captured successful response must survive.
    sqlx::raw_sql("CREATE FUNCTION core_console.break_later_read() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN DROP TABLE core_console.options; RETURN NEW; END $$; CREATE TRIGGER break_later_read AFTER INSERT ON core_console.announcement_reads FOR EACH ROW EXECUTE FUNCTION core_console.break_later_read();").execute(&pool).await.unwrap();
    let response = ack(&app, &user.secret, 1, &item.revision).await;
    assert_eq!(response.0, StatusCode::OK);
    assert!(response.2["data"][0]["read_at"].as_i64().unwrap() > 0);
    assert_eq!(
        get(&app, "/api/user/self/announcements", Some(&user.secret))
            .await
            .0,
        StatusCode::SERVICE_UNAVAILABLE
    );
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_console.announcement_reads")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 1);
}

#[sqlx::test(migrations = false)]
async fn restricted_runtime_reads_setup_and_notice_and_writes_only_receipts(pool: PgPool) {
    let (identity, _, _) = setup(&pool).await;
    let user = identity.bootstrap_user(1, 0).await.unwrap();
    identity.bootstrap_user(2, 6).await.unwrap();
    console_compat::record_initialization(&pool, "synthetic-v1")
        .await
        .unwrap();
    publish(&pool, &[announcement(1, "g1")]).await;
    option(&pool, "Notice", "Synthetic runtime notice").await;
    let role = runtime::RuntimeRole::create(&pool).await;
    role.optional("core_console").await;
    assert!(matches!(
        reqwest::Url::parse(&role.url).unwrap().host_str(),
        Some("127.0.0.1" | "localhost")
    ));
    let current_role: String = sqlx::query_scalar("SELECT current_user::text")
        .fetch_one(&role.pool)
        .await
        .unwrap();
    assert_eq!(current_role, role.name);
    let state = ConsoleCompat::new(
        role.pool.clone(),
        IdentityStore::from_pool(role.pool.clone()),
    );
    assert!(state.setup_status().await.unwrap().status);
    assert_eq!(state.notice().await.unwrap(), "Synthetic runtime notice");
    let item = state
        .announcements(&user.secret, None)
        .await
        .unwrap()
        .remove(0);
    assert!(
        state
            .acknowledge(&user.secret, 1, &item.revision)
            .await
            .unwrap()[0]
            .read_at
            > 0
    );
    for sql in [
        "INSERT INTO core_console.initialization(version) VALUES('forbidden')",
        "UPDATE core_console.initialization SET version='forbidden'",
        "UPDATE core_console.options SET value='forbidden'",
        "DELETE FROM core_console.announcement_reads",
        "CREATE TABLE core_console.forbidden(id BIGINT)",
    ] {
        assert!(sqlx::query(sql).execute(&role.pool).await.is_err(), "{sql}");
    }
    assert!(
        console_compat::record_initialization(&role.pool, "synthetic-v1")
            .await
            .is_err()
    );
    role.cleanup().await;
}
