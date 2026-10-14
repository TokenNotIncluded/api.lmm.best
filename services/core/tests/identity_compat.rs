//! Synthetic PostgreSQL + actual HTTP adapter. No production credential imports.
use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Request, StatusCode},
};
use lmm_core::{
    accounts::{Account, AccountKind},
    core_schema,
    funding::FundingPolicy,
    identity::{DashboardKeyInput, IdentityError, IdentityStore, LoginRequest},
    identity_http,
    legacy_auth::{self, LegacyAuthConfig},
};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Row};
use tower::ServiceExt;
const ORIGIN: &str = "http://api.lmm.local";
async fn fixture(pool: &PgPool) -> (IdentityStore, Router, String, String) {
    core_schema::init(pool).await.unwrap();
    let store = IdentityStore::from_pool(pool.clone())
        .with_registration_enabled(true)
        .with_commerce_scope_enabled(true)
        .with_api_key_encryption_key(&[45; 32])
        .unwrap();
    let alice = store.bootstrap_user(1, 0).await.unwrap();
    let bob = store.bootstrap_user(2, 0).await.unwrap();
    sqlx::raw_sql("UPDATE core_identity.user_profiles SET username=CASE user_id WHEN 1 THEN 'alice' ELSE 'bob' END,display_name=CASE user_id WHEN 1 THEN 'Alice' ELSE 'Bob' END WHERE user_id IN (1,2); INSERT INTO routing_price_versions(version,document) VALUES(1,'{\"version\":1}'); INSERT INTO routing_config_versions(version,price_version,document) VALUES(1,1,'{\"version\":1}'); INSERT INTO routing_groups(version,name,enabled) VALUES(1,'default',TRUE),(1,'private',TRUE),(1,'disabled',FALSE); INSERT INTO routing_routes(version,id,group_name,model,enabled,speeds,max_attempts,retry_on,allow_replay_after_send) VALUES(1,1,'default','local-model',TRUE,ARRAY['standard'],1,'{}',FALSE),(1,2,'private','private-model',TRUE,ARRAY['standard'],1,'{}',FALSE); UPDATE routing_price_versions SET sealed=TRUE WHERE version=1; UPDATE routing_config_versions SET sealed=TRUE WHERE version=1; UPDATE routing_active_release SET config_version=1; INSERT INTO core_identity.user_routing_groups(user_id,group_name,source_reference) VALUES(1,'default','synthetic-operator'),(2,'private','synthetic-operator');").execute(pool).await.unwrap();
    let config = LegacyAuthConfig::new(ORIGIN, &[43; 32])
        .unwrap()
        .with_insecure_local_http()
        .unwrap();
    let app = legacy_auth::router(store.clone(), config);
    (store, app, alice.secret, bob.secret)
}
async fn call(
    app: &Router,
    method: &str,
    path: &str,
    secret: Option<&str>,
    body: Option<Value>,
    origin: Option<&str>,
) -> (StatusCode, Value, axum::http::HeaderMap) {
    let mut request = Request::builder().method(method).uri(path);
    if let Some(secret) = secret {
        request = request.header("authorization", format!("Bearer {secret}"));
    }
    if let Some(origin) = origin {
        request = request.header("origin", origin);
    }
    let body = if let Some(body) = body {
        request = request.header("content-type", "application/json");
        Body::from(body.to_string())
    } else {
        Body::empty()
    };
    let response = app
        .clone()
        .oneshot(request.body(body).unwrap())
        .await
        .unwrap();
    let status = response.status();
    let headers = response.headers().clone();
    let bytes = to_bytes(response.into_body(), 65536).await.unwrap();
    (
        status,
        serde_json::from_slice(&bytes).unwrap_or(Value::Null),
        headers,
    )
}
fn input() -> Value {
    json!({"name":"Synthetic key","remain_quota":100,"expired_time":-1,"unlimited_quota":false,"model_limits_enabled":true,"model_limits":"local-model","allow_ips":"127.0.0.0/8,::1/128","group":"default","auto_groups":[],"cross_group_retry":false,"one_time_reveal":true})
}
async fn create(app: &Router, secret: &str, body: Value) -> Value {
    let response = call(
        app,
        "POST",
        "/api/token/",
        Some(secret),
        Some(body),
        Some(ORIGIN),
    )
    .await;
    assert_eq!(response.0, StatusCode::OK, "{:?}", response.1);
    assert_eq!(response.2["cache-control"], "no-store");
    response.1["data"].clone()
}
#[sqlx::test(migrations = false)]
async fn real_key_hash_lifecycle_and_idor(pool: PgPool) {
    let (store, app, alice, bob) = fixture(&pool).await;
    let key = create(&app, &alice, input()).await;
    let id = key["id"].as_i64().unwrap();
    let secret = key["key"].as_str().unwrap();
    let row=sqlx::query("SELECT c.digest,s.encrypted_secret FROM core_identity.credentials c JOIN core_identity.api_key_settings s ON s.credential_id=c.id WHERE c.id=$1").bind(id).fetch_one(&pool).await.unwrap();
    assert_eq!(
        row.get::<Vec<u8>, _>("digest"),
        Sha256::digest(secret.as_bytes()).to_vec()
    );
    assert!(row.get::<Option<Vec<u8>>, _>("encrypted_secret").is_none());
    assert!(store.authorize(secret).await.is_ok());
    for path in [format!("/api/token/{id}"), format!("/api/token/{id}/key")] {
        let method = if path.ends_with("key") { "POST" } else { "GET" };
        assert_eq!(
            call(&app, method, &path, Some(&bob), None, Some(ORIGIN))
                .await
                .0,
            StatusCode::FORBIDDEN
        );
    }
    assert_eq!(
        call(
            &app,
            "GET",
            "/api/token/?p=1&size=10",
            Some(secret),
            None,
            None
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    let listed = call(
        &app,
        "GET",
        "/api/token/?p=1&size=10&creation_mode=manual",
        Some(&alice),
        None,
        None,
    )
    .await;
    assert_eq!(listed.1["data"]["items"][0]["key"], "********");
    assert!(!listed.1.to_string().contains(secret));
    assert_eq!(
        call(
            &app,
            "PUT",
            "/api/token/?status_only=true",
            Some(&alice),
            Some(json!({"id":id,"status":2})),
            Some(ORIGIN)
        )
        .await
        .0,
        StatusCode::OK
    );
    assert!(matches!(
        store.authorize(secret).await,
        Err(IdentityError::Unauthorized)
    ));
    assert_eq!(
        call(
            &app,
            "PUT",
            "/api/token/?status_only=true",
            Some(&alice),
            Some(json!({"id":id,"status":1})),
            Some(ORIGIN)
        )
        .await
        .0,
        StatusCode::OK
    );
    assert!(store.authorize(secret).await.is_ok());
    assert_eq!(
        call(
            &app,
            "DELETE",
            &format!("/api/token/{id}/"),
            Some(&alice),
            None,
            Some(ORIGIN)
        )
        .await
        .0,
        StatusCode::OK
    );
    assert!(store.authorize(secret).await.is_err());
    assert_eq!(
        call(
            &app,
            "PUT",
            "/api/token/?status_only=true",
            Some(&alice),
            Some(json!({"id":id,"status":1})),
            Some(ORIGIN)
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
}
#[sqlx::test(migrations = false)]
async fn one_time_and_recoverable_ciphertext_policy(pool: PgPool) {
    let (store, app, alice, _) = fixture(&pool).await;
    let one = create(&app, &alice, input()).await;
    assert_eq!(
        call(
            &app,
            "POST",
            &format!("/api/token/{}/key", one["id"]),
            Some(&alice),
            None,
            Some(ORIGIN)
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    let mut data = input();
    data["one_time_reveal"] = json!(false);
    let key = create(&app, &alice, data).await;
    let id = key["id"].as_i64().unwrap();
    let revealed = call(
        &app,
        "POST",
        &format!("/api/token/{id}/key"),
        Some(&alice),
        None,
        Some(ORIGIN),
    )
    .await;
    assert_eq!(revealed.1["data"]["key"], key["key"]);
    assert_eq!(revealed.2["cache-control"], "no-store");
    assert!(
        store
            .clone()
            .with_api_key_encryption_key(&[44; 32])
            .unwrap()
            .dashboard_reveal_keys(&alice, &[id])
            .await
            .is_err()
    );
    assert!(
        store
            .clone()
            .with_api_key_encryption_key(&[44; 31])
            .is_err()
    );
    // Rotating the unrelated browser-refresh master cannot break API key
    // recovery; only the Core's dedicated key file controls this ciphertext.
    let rotated = legacy_auth::router(
        store.clone(),
        LegacyAuthConfig::new(ORIGIN, &[46; 32])
            .unwrap()
            .with_insecure_local_http()
            .unwrap(),
    );
    let readback = call(
        &rotated,
        "POST",
        &format!("/api/token/{id}/key"),
        Some(&alice),
        None,
        Some(ORIGIN),
    )
    .await;
    assert_eq!(readback.0, StatusCode::OK);
    assert_eq!(readback.1["data"]["key"], key["key"]);
    // A missing private key never silently falls back to the refresh key or
    // persists a recoverable credential whose secret cannot be recovered.
    let missing = IdentityStore::from_pool(pool.clone());
    let missing_app = legacy_auth::router(
        missing,
        LegacyAuthConfig::new(ORIGIN, &[43; 32])
            .unwrap()
            .with_insecure_local_http()
            .unwrap(),
    );
    assert_eq!(
        call(
            &missing_app,
            "POST",
            &format!("/api/token/{id}/key"),
            Some(&alice),
            None,
            Some(ORIGIN)
        )
        .await
        .0,
        StatusCode::SERVICE_UNAVAILABLE
    );
    let before: i64 = sqlx::query_scalar("SELECT count(*) FROM core_identity.credentials")
        .fetch_one(&pool)
        .await
        .unwrap();
    let mut recoverable = input();
    recoverable["one_time_reveal"] = json!(false);
    assert_eq!(
        call(
            &missing_app,
            "POST",
            "/api/token/",
            Some(&alice),
            Some(recoverable),
            Some(ORIGIN)
        )
        .await
        .0,
        StatusCode::SERVICE_UNAVAILABLE
    );
    let after: i64 = sqlx::query_scalar("SELECT count(*) FROM core_identity.credentials")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(before, after);
    create(&missing_app, &alice, input()).await;
    let encrypted: Vec<u8> = sqlx::query_scalar(
        "SELECT encrypted_secret FROM core_identity.api_key_settings WHERE credential_id=$1",
    )
    .bind(id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(
        !encrypted
            .windows(key["key"].as_str().unwrap().len())
            .any(|b| b == key["key"].as_str().unwrap().as_bytes())
    );
    assert!(sqlx::query("UPDATE core_identity.api_key_settings SET one_time_reveal=TRUE,encrypted_secret=NULL WHERE credential_id=$1").bind(id).execute(&pool).await.is_err());
    let mut data = input();
    data["one_time_reveal"] = json!(false);
    let other = create(&app, &alice, data).await;
    assert!(
        sqlx::query(
            "UPDATE core_identity.api_key_settings SET encrypted_secret=$2 WHERE credential_id=$1"
        )
        .bind(other["id"].as_i64().unwrap())
        .bind(encrypted)
        .execute(&pool)
        .await
        .is_err()
    );
}
#[sqlx::test(migrations = false)]
async fn model_ip_group_and_operator_revocation_are_enforced(pool: PgPool) {
    let (store, app, alice, _) = fixture(&pool).await;
    let key = create(&app, &alice, input()).await;
    let secret = key["key"].as_str().unwrap();
    assert!(
        store
            .authorize_model_request(
                secret,
                "local-model",
                Some("127.0.0.1".parse().unwrap()),
                "default"
            )
            .await
            .is_ok()
    );
    for (model, peer, group) in [
        ("private-model", Some("127.0.0.1"), "default"),
        ("local-model", None, "default"),
        ("local-model", Some("192.0.2.1"), "default"),
        ("local-model", Some("::1"), "private"),
    ] {
        assert!(matches!(
            store
                .authorize_model_request(secret, model, peer.map(|p| p.parse().unwrap()), group)
                .await,
            Err(IdentityError::Forbidden)
        ));
    }
    assert_eq!(
        store.api_key_routing_groups(secret).await.unwrap(),
        vec!["default"]
    );
    sqlx::query("UPDATE core_identity.user_routing_groups SET enabled=FALSE WHERE user_id=1")
        .execute(&pool)
        .await
        .unwrap();
    assert!(store.authorize(secret).await.is_err());
    assert!(store.authorize(&alice).await.is_err());
}
#[sqlx::test(migrations = false)]
async fn grants_never_follow_service_level_or_client_fields(pool: PgPool) {
    let (store, app, _, _) = fixture(&pool).await;
    let session = store
        .register(&LoginRequest {
            login_name: "newuser".into(),
            password: "a real test password".into(),
        })
        .await
        .unwrap();
    let user = store.authorize(&session.secret).await.unwrap().user_id;
    sqlx::query("UPDATE core_identity.accounts SET service_level=4 WHERE id=(SELECT personal_account_id FROM core_identity.users WHERE id=$1)").bind(user).execute(&pool).await.unwrap();
    assert!(matches!(
        store
            .issue_key(
                &session.secret,
                Account {
                    kind: AccountKind::Personal,
                    id: user
                },
                FundingPolicy::new_personal_key(),
                3600
            )
            .await,
        Err(IdentityError::Forbidden)
    ));
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/token/",
            Some(&session.secret),
            Some(input()),
            Some(ORIGIN)
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    assert_eq!(
        call(
            &app,
            "PUT",
            "/api/user/self",
            Some(&session.secret),
            Some(json!({"developer_access_granted":true})),
            Some(ORIGIN)
        )
        .await
        .0,
        StatusCode::BAD_REQUEST
    );
}
#[sqlx::test(migrations = false)]
async fn unsafe_methods_require_canonical_origin(pool: PgPool) {
    let (_, app, alice, _) = fixture(&pool).await;
    let key = create(&app, &alice, input()).await;
    for origin in [None, Some("https://evil.invalid")] {
        assert_eq!(
            call(
                &app,
                "PUT",
                "/api/token/?status_only=true",
                Some(&alice),
                Some(json!({"id":key["id"],"status":2})),
                origin
            )
            .await
            .0,
            StatusCode::FORBIDDEN
        );
        assert_eq!(
            call(
                &app,
                "DELETE",
                &format!("/api/token/{}/", key["id"]),
                Some(&alice),
                None,
                origin
            )
            .await
            .0,
            StatusCode::FORBIDDEN
        );
    }
}
#[sqlx::test(migrations = false)]
async fn lifetime_quota_edit_preserves_historical_consumption(pool: PgPool) {
    let (_, app, alice, _) = fixture(&pool).await;
    let key = create(&app, &alice, input()).await;
    let id = key["id"].as_i64().unwrap();
    let mut seed = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *seed)
        .await
        .unwrap();
    sqlx::query("INSERT INTO core_charging.budget_opening_consumption(scope,key_id,period,start_at,end_at,consumed_credits,import_reference) VALUES('key',$1,'lifetime',-62135596800,253402300800,70,'synthetic-history')")
        .bind(id).execute(&mut *seed).await.unwrap();
    seed.commit().await.unwrap();
    let before = call(
        &app,
        "GET",
        &format!("/api/token/{id}"),
        Some(&alice),
        None,
        None,
    )
    .await;
    assert_eq!(before.1["data"]["remain_quota"], 30);
    assert_eq!(before.1["data"]["used_quota"], 70);
    let mut edit = input();
    edit.as_object_mut().unwrap().remove("one_time_reveal");
    edit["id"] = json!(id);
    edit["remain_quota"] = json!(20);
    assert_eq!(
        call(
            &app,
            "PUT",
            "/api/token/",
            Some(&alice),
            Some(edit.clone()),
            Some(ORIGIN)
        )
        .await
        .0,
        StatusCode::OK
    );
    let after = call(
        &app,
        "GET",
        &format!("/api/token/{id}"),
        Some(&alice),
        None,
        None,
    )
    .await;
    assert_eq!(after.1["data"]["remain_quota"], 20);
    assert_eq!(after.1["data"]["used_quota"], 70);
    edit["unlimited_quota"] = json!(true);
    assert_eq!(
        call(
            &app,
            "PUT",
            "/api/token/",
            Some(&alice),
            Some(edit.clone()),
            Some(ORIGIN)
        )
        .await
        .0,
        StatusCode::OK
    );
    edit["unlimited_quota"] = json!(false);
    edit["remain_quota"] = json!(0);
    assert_eq!(
        call(
            &app,
            "PUT",
            "/api/token/",
            Some(&alice),
            Some(edit),
            Some(ORIGIN)
        )
        .await
        .0,
        StatusCode::OK
    );
    let end = call(
        &app,
        "GET",
        &format!("/api/token/{id}"),
        Some(&alice),
        None,
        None,
    )
    .await;
    assert_eq!(end.1["data"]["used_quota"], 70);
    assert_eq!(end.1["data"]["status"], 4);
}
#[sqlx::test(migrations = false)]
async fn batch_delete_is_atomic_and_search_is_owner_scoped(pool: PgPool) {
    let (store, app, alice, bob) = fixture(&pool).await;
    let key = create(&app, &alice, input()).await;
    let mut data = input();
    data["group"] = json!("private");
    let other = create(&app, &bob, data).await;
    assert_eq!(
        call(
            &app,
            "POST",
            "/api/token/batch",
            Some(&alice),
            Some(json!({"ids":[key["id"],other["id"]]})),
            Some(ORIGIN)
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    assert!(store.authorize(key["key"].as_str().unwrap()).await.is_ok());
    let search = call(
        &app,
        "GET",
        "/api/token/search?keyword=Synthetic&p=1&size=1",
        Some(&alice),
        None,
        None,
    )
    .await;
    assert_eq!(search.1["data"]["total"], 1);
    assert_eq!(search.1["data"]["items"][0]["id"], key["id"]);
    let groups = call(
        &app,
        "GET",
        "/api/user/self/groups",
        Some(&alice),
        None,
        None,
    )
    .await;
    assert!(groups.1["data"].get("private").is_none());
    assert!(groups.1["data"].get("default").is_some());
}
#[sqlx::test(migrations = false)]
async fn company_and_profile_data_are_private_durable_and_not_authority(pool: PgPool) {
    let (_, app, alice, bob) = fixture(&pool).await;
    assert_eq!(
        call(
            &app,
            "GET",
            "/api/user/company-billing-profile",
            Some(&alice),
            None,
            None
        )
        .await
        .1["data"],
        Value::Null
    );
    let profile = json!({"country":"tw","isBusiness":true,"postcode":" 100 ","state":"","businessName":"Synthetic company","taxId":"SYNTHETIC-NOT-REAL","useForInvoices":true});
    let saved = call(
        &app,
        "PUT",
        "/api/user/company-billing-profile",
        Some(&alice),
        Some(profile.clone()),
        Some(ORIGIN),
    )
    .await;
    assert_eq!(saved.0, StatusCode::OK);
    assert_eq!(saved.1["data"]["country"], "TW");
    assert_eq!(
        call(
            &app,
            "GET",
            "/api/user/company-billing-profile",
            Some(&bob),
            None,
            None
        )
        .await
        .1["data"],
        Value::Null
    );
    let mut invalid = profile;
    invalid["country"] = json!("XX");
    let rejected = call(
        &app,
        "PUT",
        "/api/user/company-billing-profile",
        Some(&alice),
        Some(invalid),
        Some(ORIGIN),
    )
    .await;
    assert_eq!(rejected.0, StatusCode::UNPROCESSABLE_ENTITY);
    // The existing company's Web consumer discovers field names under the
    // main errors object; a custom fields array would lose inline feedback.
    assert_eq!(rejected.1["errors"]["country"], "invalid");
    assert_eq!(
        call(
            &app,
            "PUT",
            "/api/user/self",
            Some(&alice),
            Some(
                json!({"display_name":"Changed","language":"zh-TW","wallet_display_currency":"USD"})
            ),
            Some(ORIGIN)
        )
        .await
        .0,
        StatusCode::OK
    );
    let raw: String =
        sqlx::query_scalar("SELECT setting FROM core_identity.user_profiles WHERE user_id=1")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(
        serde_json::from_str::<Value>(&raw).unwrap()["wallet_display_currency"],
        "USD"
    );
    let audits: Vec<String> = sqlx::query_scalar("SELECT target::text FROM core_identity.audit")
        .fetch_all(&pool)
        .await
        .unwrap();
    assert!(
        audits
            .iter()
            .all(|a| !a.contains("SYNTHETIC-NOT-REAL") && !a.contains("Synthetic company"))
    );
}
#[sqlx::test(migrations = false)]
async fn commerce_scope_is_explicit_revocable_and_personal_owner_only(pool: PgPool) {
    let (store, app, alice, bob) = fixture(&pool).await;
    let key = create(&app, &alice, input()).await;
    let id = key["id"].as_i64().unwrap();
    let native = identity_http::router(store.clone());
    assert_eq!(
        call(
            &native,
            "PUT",
            &format!("/core/v1/keys/{id}/commerce-scope"),
            Some(&bob),
            Some(json!({"enabled":true})),
            None
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    assert_eq!(
        call(
            &native,
            "PUT",
            &format!("/core/v1/keys/{id}/commerce-scope"),
            Some(&alice),
            Some(json!({"enabled":true})),
            None
        )
        .await
        .0,
        StatusCode::NO_CONTENT
    );
    let spend: bool =
        sqlx::query_scalar("SELECT can_spend FROM core_commerce.key_scopes WHERE credential_id=$1")
            .bind(id)
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(spend);
    store
        .set_commerce_key_scope(&alice, id, false)
        .await
        .unwrap();
    assert!(
        !sqlx::query_scalar::<_, bool>(
            "SELECT can_spend FROM core_commerce.key_scopes WHERE credential_id=$1"
        )
        .bind(id)
        .fetch_one(&pool)
        .await
        .unwrap()
    );
    assert!(
        IdentityStore::from_pool(pool.clone())
            .set_commerce_key_scope(&alice, id, true)
            .await
            .is_err()
    );
}
#[sqlx::test(migrations = false)]
async fn key_request_rejects_invalid_limits_and_ungranted_groups(pool: PgPool) {
    let (_, app, alice, _) = fixture(&pool).await;
    for (field, value) in [
        ("group", json!("private")),
        ("allow_ips", json!("127.0.0.1/33")),
        ("model_limits", json!("local-model,local-model")),
        ("remain_quota", json!(-1)),
        ("expired_time", json!(0)),
    ] {
        let mut body = input();
        body[field] = value;
        let response = call(
            &app,
            "POST",
            "/api/token/",
            Some(&alice),
            Some(body),
            Some(ORIGIN),
        )
        .await;
        assert!(matches!(
            response.0,
            StatusCode::BAD_REQUEST | StatusCode::FORBIDDEN
        ));
    }
    let count: i64 =
        sqlx::query_scalar("SELECT count(*) FROM core_identity.credentials WHERE kind='api_key'")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(count, 0);
    let _: DashboardKeyInput = serde_json::from_value(input()).unwrap();
}

#[sqlx::test(migrations = false)]
async fn unlimited_remaining_keeps_signed_history_and_own_policy(pool: PgPool) {
    let (_, app, alice, _) = fixture(&pool).await;
    let mut body = input();
    body["unlimited_quota"] = json!(true);
    body["remain_quota"] = json!(10);
    let key = create(&app, &alice, body.clone()).await;
    let id = key["id"].as_i64().unwrap();
    let mut seed = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *seed)
        .await
        .unwrap();
    sqlx::query("INSERT INTO core_charging.budget_opening_consumption(scope,key_id,period,start_at,end_at,consumed_credits,import_reference) VALUES('key',$1,'lifetime',-62135596800,253402300800,70,'synthetic-unlimited-history')")
        .bind(id).execute(&mut *seed).await.unwrap();
    // An independent issuer's cap still restricts admission but must not
    // silently rewrite the owner's displayed and editable quota policy.
    sqlx::query("INSERT INTO core_charging.budgets(scope,key_id,issuer_user_id,period,limit_credits) VALUES('key',$1,2,'lifetime',5)").bind(id).execute(&mut *seed).await.unwrap();
    seed.commit().await.unwrap();
    let before = call(
        &app,
        "GET",
        &format!("/api/token/{id}"),
        Some(&alice),
        None,
        None,
    )
    .await;
    assert_eq!(before.1["data"]["remain_quota"], -60);
    assert_eq!(before.1["data"]["used_quota"], 70);
    assert_eq!(before.1["data"]["unlimited_quota"], true);
    body.as_object_mut().unwrap().remove("one_time_reveal");
    body["id"] = json!(id);
    body["remain_quota"] = json!(-60);
    let updated = call(
        &app,
        "PUT",
        "/api/token/",
        Some(&alice),
        Some(body),
        Some(ORIGIN),
    )
    .await;
    assert_eq!(updated.0, StatusCode::OK);
    assert_eq!(updated.1["data"]["remain_quota"], -60);
    let other_cap: i64 = sqlx::query_scalar(
        "SELECT limit_credits FROM core_charging.budgets WHERE key_id=$1 AND issuer_user_id=2",
    )
    .bind(id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(other_cap, 5);
}

#[sqlx::test(migrations = false)]
async fn commerce_scope_team_hierarchy_and_api_key_self_grant_denial(pool: PgPool) {
    use lmm_core::{accounts::TeamRole, identity::InviteRequest};
    let (store, _, owner, _) = fixture(&pool).await;
    let admin = store.bootstrap_user(3, 0).await.unwrap();
    let peer_admin = store.bootstrap_user(4, 0).await.unwrap();
    let member = store.bootstrap_user(5, 0).await.unwrap();
    let team = store.create_team(&owner).await.unwrap();
    for (user, secret, role) in [
        (3, &admin.secret, TeamRole::Admin),
        (4, &peer_admin.secret, TeamRole::Admin),
        (5, &member.secret, TeamRole::Member),
    ] {
        let invite = store
            .invite(
                &owner,
                team,
                &InviteRequest {
                    recipient_user_id: user,
                    role,
                    can_spend: true,
                    ttl_seconds: 3600,
                },
            )
            .await
            .unwrap();
        store.accept_invite(secret, &invite.secret).await.unwrap();
    }
    let account = Account {
        kind: AccountKind::Team,
        id: team,
    };
    let owner_key = store
        .issue_key(
            &owner,
            account,
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let peer_key = store
        .issue_key(
            &peer_admin.secret,
            account,
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let member_key = store
        .issue_key(
            &member.secret,
            account,
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    for target in [owner_key.id, peer_key.id] {
        assert!(matches!(
            store
                .set_commerce_key_scope(&admin.secret, target, true)
                .await,
            Err(IdentityError::Forbidden)
        ));
        let count: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM core_commerce.key_scopes WHERE credential_id=$1",
        )
        .bind(target)
        .fetch_one(&pool)
        .await
        .unwrap();
        assert_eq!(count, 0);
    }
    assert!(matches!(
        store
            .set_commerce_key_scope(&member_key.secret, member_key.id, true)
            .await,
        Err(IdentityError::Forbidden)
    ));
    store
        .set_commerce_key_scope(&admin.secret, member_key.id, true)
        .await
        .unwrap();
    let audit_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_identity.audit WHERE action='credential.commerce_scope'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(audit_count, 1);
    store
        .set_commerce_key_scope(&owner, peer_key.id, true)
        .await
        .unwrap();
}

// Additional PostgreSQL contracts. The twelve existing cases above are preserved.
#[path = "common/runtime.rs"]
mod marked_runtime;
mod marked_key_cases {
    use super::*;
    use lmm_core::abuse::{
        AbuseStore,
        admin::{CommitRequest, PolicyUpdate, PreviewRequest},
    };
    use std::collections::BTreeSet;

    async fn policy(pool: &PgPool, store: &IdentityStore) -> (AbuseStore, String) {
        let installed: bool =
            sqlx::query_scalar("SELECT to_regclass('core_abuse.user_marks') IS NOT NULL")
                .fetch_one(pool)
                .await
                .unwrap();
        if !installed {
            // Explicit installation-owner fixture setup, never runtime startup.
            sqlx::raw_sql(lmm_core::abuse::SCHEMA)
                .execute(pool)
                .await
                .unwrap();
        }
        let admin = store.bootstrap_user(99, 6).await.unwrap();
        let abuse = AbuseStore::new(pool.clone(), BTreeSet::new());
        let update: PolicyUpdate = serde_json::from_value(json!({
            "operation_id":"identity-policy-1","reason":"synthetic identity fixture","expected_revision":0,
            "config":{"version":1,"policies":[{"id":"synthetic","name":"Synthetic policy","mode":"fixed_text",
                "fixed_text":"synthetic response","public_models":["public-demo"],"clients":{},
                "limits":{"requests_per_minute_per_user":10,"requests_per_minute_global":100,
                    "max_concurrent_per_user":2,"max_concurrent_global":10,"max_input_bytes":1024,
                    "max_output_bytes":1024,"max_output_tokens":128,"output_bytes_per_second":0,"total_timeout_seconds":5}}]}
        })).unwrap();
        abuse.update_policies(&admin.secret, &update).await.unwrap();
        (abuse, admin.secret)
    }
    async fn mark(abuse: &AbuseStore, admin: &str, user: i64, revision: i64, enabled: bool) {
        let operation = format!(
            "identity-{}-{user}-{revision}",
            if enabled { "mark" } else { "unmark" }
        );
        let mut body = json!({"operation_id":operation,"reason":"synthetic identity transition",
            "action":if enabled {"mark"} else {"unmark"},"expected_policy_revision":1,
            "targets":[{"user_id":user,"expected_revision":revision}]});
        if enabled {
            body["policy_id"] = json!("synthetic");
        }
        let preview: PreviewRequest = serde_json::from_value(body).unwrap();
        let flow = abuse.preview_users(admin, &preview).await.unwrap();
        let commit: CommitRequest = serde_json::from_value(json!({"operation_id":operation,
            "preview_id":flow["preview_id"],"preview_hash":flow["preview_hash"]}))
        .unwrap();
        abuse.commit_users(admin, &commit).await.unwrap();
    }
    async fn ordinary(store: &IdentityStore) -> (i64, String) {
        let login = store
            .register(&LoginRequest {
                login_name: "marked_candidate".into(),
                password: "synthetic password only".into(),
            })
            .await
            .unwrap();
        let user = store.authorize(&login.secret).await.unwrap().user_id;
        (user, login.secret)
    }
    // /api/user/self accepts current browser families, not native Sessions.
    // Use the real password path so Core persists the family and primary proof.
    async fn browser_account(store: &IdentityStore, username: &str) -> (i64, String) {
        let browser = lmm_core::identity::BrowserAuth::new(store.clone(), &[43; 32]).unwrap();
        let password = "synthetic password";
        browser
            .register(&lmm_core::identity::BrowserRegistration {
                username,
                password,
                email: "",
            })
            .await
            .unwrap();
        let bundle = browser
            .login(
                username,
                password,
                &lmm_core::identity::BrowserSessionMetadata::default(),
            )
            .await
            .unwrap();
        let access = bundle.data["access_token"].as_str().unwrap().to_owned();
        let user = store.authorize(&access).await.unwrap().user_id;
        (user, access)
    }
    async fn restricted(pool: &PgPool) -> marked_runtime::RuntimeRole {
        let role = marked_runtime::RuntimeRole::create(pool).await;
        role.optional("web_identity").await;
        // Full-runtime fixtures also cover the admin host. This metadata-only
        // connection must remove both table and column grants from that setup.
        sqlx::raw_sql(&format!(
            r#"
            REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA core_abuse FROM {0};
            REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA core_abuse FROM {0};
            REVOKE UPDATE(revision,document) ON core_abuse.settings FROM {0};
            REVOKE UPDATE(marked,policy_id,revision) ON core_abuse.user_marks FROM {0};
            REVOKE UPDATE(attempts) ON core_abuse.model_counters FROM {0};
            REVOKE UPDATE(finished_at) ON core_abuse.model_leases FROM {0};
            GRANT USAGE ON SCHEMA core_abuse TO {0};
            GRANT SELECT ON core_abuse.user_marks,core_abuse.settings TO {0};
        "#,
            role.name
        ))
        .execute(pool)
        .await
        .unwrap();
        let (session_user, current_user): (String, String) =
            sqlx::query_as("SELECT session_user::text,current_user::text")
                .fetch_one(&role.pool)
                .await
                .unwrap();
        let url = reqwest::Url::parse(&role.url).expect("invalid synthetic runtime URL");
        assert!(
            session_user == role.name && current_user == role.name && url.username() == role.name,
            "identity tests must use the fresh restricted role"
        );
        let privileges: bool = sqlx::query_scalar("SELECT NOT rolsuper AND NOT rolbypassrls AND NOT rolcreatedb AND NOT rolcreaterole FROM pg_roles WHERE rolname=current_user").fetch_one(&role.pool).await.unwrap();
        assert!(privileges, "runtime must remain unprivileged");
        let mut c = role.pool.acquire().await.unwrap();
        let developer_write: bool = sqlx::query_scalar("SELECT has_table_privilege(current_user,'core_identity.developer_access','INSERT,UPDATE,DELETE') OR has_table_privilege(current_user,'core_identity.user_routing_groups','INSERT,UPDATE,DELETE') OR has_table_privilege(current_user,'core_abuse.user_marks','INSERT,UPDATE,DELETE') OR has_table_privilege(current_user,'core_abuse.settings','INSERT,UPDATE,DELETE')").fetch_one(&mut *c).await.unwrap();
        assert!(
            !developer_write,
            "key management cannot mutate grants or abuse policy"
        );
        drop(c);
        role
    }
    fn runtime_store(pool: &PgPool) -> IdentityStore {
        IdentityStore::from_pool(pool.clone())
            .with_commerce_scope_enabled(true)
            .with_api_key_encryption_key(&[45; 32])
            .unwrap()
    }
    fn app(store: IdentityStore) -> Router {
        legacy_auth::router(
            store,
            LegacyAuthConfig::new(ORIGIN, &[43; 32])
                .unwrap()
                .with_insecure_local_http()
                .unwrap(),
        )
    }
    fn marked_input() -> Value {
        let mut body = input();
        body["group"] = json!("auto");
        body["model_limits"] = json!("public-demo");
        body["one_time_reveal"] = json!(false);
        body
    }
    async fn self_view(app: &Router, secret: &str) -> Value {
        let (status, body, _) = call(app, "GET", "/api/user/self", Some(secret), None, None).await;
        assert!(
            status == StatusCode::OK,
            "self must accept the authenticated session"
        );
        body["data"].clone()
    }
    async fn unchanged_authority(pool: &PgPool, user: i64) -> (i64, i16, bool, i64) {
        sqlx::query_as("SELECT u.auth_version,a.service_level,COALESCE(d.granted,FALSE),(SELECT count(*) FROM core_identity.user_routing_groups g WHERE g.user_id=u.id) FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id LEFT JOIN core_identity.developer_access d ON d.user_id=u.id WHERE u.id=$1").bind(user).fetch_one(pool).await.unwrap()
    }
    async fn mutation_counts(pool: &PgPool) -> (i64, i64, i64, i64, i64) {
        sqlx::query_as("SELECT (SELECT count(*) FROM core_identity.credentials WHERE kind='api_key'),(SELECT count(*) FROM core_identity.api_key_settings),(SELECT count(*) FROM core_identity.audit),(SELECT count(*) FROM core_charging.budgets),(SELECT count(*) FROM core_charging.policy_events)").fetch_one(pool).await.unwrap()
    }
    async fn funding(pool: &PgPool, id: i64, user: i64) {
        let personal: bool = sqlx::query_scalar("SELECT c.user_id=$2 AND c.owner_account_id=u.personal_account_id AND EXISTS(SELECT 1 FROM core_identity.key_funding_rules f WHERE f.credential_id=c.id) AND NOT EXISTS(SELECT 1 FROM core_identity.key_funding_rules f WHERE f.credential_id=c.id AND f.payer_account_id<>u.personal_account_id) AND NOT EXISTS(SELECT 1 FROM core_identity.credential_grants g WHERE g.credential_id=c.id) FROM core_identity.credentials c JOIN core_identity.users u ON u.id=c.user_id WHERE c.id=$1").bind(id).bind(user).fetch_one(pool).await.unwrap();
        assert!(
            personal,
            "marked issuance must use only the owner's personal account and funding"
        );
    }

    #[sqlx::test(migrations = false)]
    async fn marked_l0_runtime_http_key_lifecycle_keeps_real_authority(pool: PgPool) {
        let (owner, _, _, bob) = fixture(&pool).await;
        let (user, secret) = browser_account(&owner, "marked_candidate").await;
        let (abuse, admin) = policy(&pool, &owner).await;
        let before = unchanged_authority(&pool, user).await;
        assert!(
            before.1 == 0 && !before.2 && before.3 == 0,
            "fixture must be an ungranted L0 user"
        );
        let role = restricted(&pool).await;
        let store = runtime_store(&role.pool);
        let app = app(store.clone());
        assert_eq!(
            self_view(&app, &secret).await["permissions"]["api_key_management"],
            false
        );
        mark(&abuse, &admin, user, 0, true).await;
        let view = self_view(&app, &secret).await;
        assert_eq!(view["permissions"]["api_key_management"], true);
        assert_eq!(view["developer_access_granted"], false);
        assert!(
            view.get("marked").is_none() && view.get("honeypot").is_none(),
            "self must expose only the generic permission"
        );
        let groups = call(
            &app,
            "GET",
            "/api/user/self/groups",
            Some(&secret),
            None,
            None,
        )
        .await;
        assert_eq!(groups.0, StatusCode::OK);
        assert_eq!(
            groups.1["data"],
            json!({"auto":{"desc":"auto","ratio":"model-specific"}})
        );
        let automatic = call(
            &app,
            "GET",
            "/api/token/auto-groups",
            Some(&secret),
            None,
            None,
        )
        .await;
        assert_eq!(automatic.0, StatusCode::OK);
        assert_eq!(
            automatic.1["data"],
            json!({"groups":["auto"],"max_count":16})
        );
        let models = call(&app, "GET", "/api/user/models", Some(&secret), None, None).await;
        assert_eq!(models.0, StatusCode::OK);
        assert_eq!(models.1["data"], json!(["public-demo"]));
        let key = create(&app, &secret, marked_input()).await;
        let id = key["id"].as_i64().unwrap();
        let key_secret = key["key"].as_str().unwrap();
        funding(&pool, id, user).await;
        assert_eq!(key["group"], "auto");
        assert_eq!(key["auto_groups"], json!(["auto"]));
        assert_eq!(key["account_balance_read"], false);
        let list = call(&app, "GET", "/api/token/", Some(&secret), None, None).await;
        assert_eq!(list.0, StatusCode::OK);
        assert_eq!(list.1["data"]["total"], 1);
        assert!(
            !list.1.to_string().contains(key_secret),
            "list must mask the key secret"
        );
        assert_eq!(
            call(
                &app,
                "GET",
                &format!("/api/token/{id}"),
                Some(&bob),
                None,
                None
            )
            .await
            .0,
            StatusCode::FORBIDDEN
        );
        let revealed = call(
            &app,
            "POST",
            &format!("/api/token/{id}/key"),
            Some(&secret),
            None,
            Some(ORIGIN),
        )
        .await;
        assert_eq!(revealed.0, StatusCode::OK);
        assert!(
            revealed.1["data"]["key"].as_str() == Some(key_secret),
            "recoverable key reveal must match without logging the secret"
        );
        let mut edit = marked_input();
        edit.as_object_mut().unwrap().remove("one_time_reveal");
        edit["id"] = json!(id);
        edit["name"] = json!("Edited synthetic key");
        edit["remain_quota"] = json!(50);
        assert_eq!(
            call(
                &app,
                "PUT",
                "/api/token/",
                Some(&secret),
                Some(edit),
                Some(ORIGIN)
            )
            .await
            .0,
            StatusCode::OK
        );
        for status in [2, 1] {
            assert_eq!(
                call(
                    &app,
                    "PUT",
                    "/api/token/?status_only=true",
                    Some(&secret),
                    Some(json!({"id":id,"status":status})),
                    Some(ORIGIN)
                )
                .await
                .0,
                StatusCode::OK
            );
            assert!(
                store.authorize(key_secret).await.is_ok() == (status == 1),
                "key status must control canonical authentication"
            );
        }
        assert_eq!(
            call(
                &app,
                "DELETE",
                &format!("/api/token/{id}"),
                Some(&secret),
                None,
                Some(ORIGIN)
            )
            .await
            .0,
            StatusCode::OK
        );
        assert!(matches!(
            store.authorize(key_secret).await,
            Err(IdentityError::Unauthorized)
        ));
        assert_eq!(unchanged_authority(&pool, user).await, before);
        role.cleanup().await;
    }

    #[sqlx::test(migrations = false)]
    async fn marked_runtime_rejects_private_groups_models_and_key_self_management_atomically(
        pool: PgPool,
    ) {
        let (owner, _, _, _) = fixture(&pool).await;
        let (user, secret) = ordinary(&owner).await;
        let (abuse, admin) = policy(&pool, &owner).await;
        mark(&abuse, &admin, user, 0, true).await;
        let role = restricted(&pool).await;
        let store = runtime_store(&role.pool);
        let app = app(store.clone());
        let before = mutation_counts(&pool).await;
        for (field, value) in [
            ("group", json!("default")),
            ("group", json!("private")),
            ("auto_groups", json!(["private"])),
            ("cross_group_retry", json!(true)),
            ("model_limits", json!("private-model")),
            ("model_limits", json!("public-demo,local-model")),
        ] {
            let mut body = marked_input();
            body[field] = value;
            assert_eq!(
                call(
                    &app,
                    "POST",
                    "/api/token/",
                    Some(&secret),
                    Some(body),
                    Some(ORIGIN)
                )
                .await
                .0,
                StatusCode::FORBIDDEN
            );
            assert_eq!(mutation_counts(&pool).await, before);
        }
        let mut forged = marked_input();
        forged["permissions"] = json!({"api_key_management":true});
        assert_eq!(
            call(
                &app,
                "POST",
                "/api/token/",
                Some(&secret),
                Some(forged),
                Some(ORIGIN)
            )
            .await
            .0,
            StatusCode::BAD_REQUEST
        );
        assert_eq!(mutation_counts(&pool).await, before);
        assert_eq!(
            call(
                &app,
                "POST",
                "/api/token/",
                Some(&secret),
                Some(marked_input()),
                None
            )
            .await
            .0,
            StatusCode::FORBIDDEN
        );
        assert_eq!(mutation_counts(&pool).await, before);
        let mut body = marked_input();
        body["one_time_reveal"] = json!(true);
        let key = create(&app, &secret, body).await;
        let id = key["id"].as_i64().unwrap();
        let key_secret = key["key"].as_str().unwrap();
        let after = mutation_counts(&pool).await;
        assert_eq!(
            call(
                &app,
                "POST",
                &format!("/api/token/{id}/key"),
                Some(&secret),
                None,
                Some(ORIGIN)
            )
            .await
            .0,
            StatusCode::FORBIDDEN
        );
        for path in [
            "/api/token/",
            "/api/token/auto-groups",
            "/api/user/self/groups",
            "/api/user/models",
        ] {
            assert_eq!(
                call(&app, "GET", path, Some(key_secret), None, None)
                    .await
                    .0,
                StatusCode::FORBIDDEN
            );
        }
        assert!(matches!(
            store.dashboard_balance_access(&secret, id, true).await,
            Err(IdentityError::Forbidden)
        ));
        assert!(matches!(
            store.set_commerce_key_scope(&secret, id, true).await,
            Err(IdentityError::Forbidden)
        ));
        assert!(matches!(
            store.api_key_routing_groups(key_secret).await,
            Err(IdentityError::Forbidden)
        ));
        assert_eq!(mutation_counts(&pool).await, after);
        role.cleanup().await;
    }

    #[sqlx::test(migrations = false)]
    async fn marked_native_issuance_restricts_real_developer_and_l0_funding(pool: PgPool) {
        use lmm_core::{accounts::TeamRole, identity::InviteRequest};
        let (owner, _, alice, _) = fixture(&pool).await;
        let (user, secret) = ordinary(&owner).await;
        let team = owner.create_team(&alice).await.unwrap();
        let invite = owner
            .invite(
                &alice,
                team,
                &InviteRequest {
                    recipient_user_id: user,
                    role: TeamRole::Member,
                    can_spend: true,
                    ttl_seconds: 3600,
                },
            )
            .await
            .unwrap();
        owner.accept_invite(&secret, &invite.secret).await.unwrap();
        let team_account = Account {
            kind: AccountKind::Team,
            id: team,
        };
        let old_team = owner
            .issue_key(
                &alice,
                team_account,
                FundingPolicy::inherit_account_settings(),
                3600,
            )
            .await
            .unwrap();
        let (abuse, admin) = policy(&pool, &owner).await;
        mark(&abuse, &admin, user, 0, true).await;
        mark(&abuse, &admin, 1, 0, true).await;
        let role = restricted(&pool).await;
        let store = runtime_store(&role.pool);
        for (issuer, issuer_user) in [(&secret, user), (&alice, 1)] {
            let personal = Account {
                kind: AccountKind::Personal,
                id: issuer_user,
            };
            let before = mutation_counts(&pool).await;
            for (account, policy) in [
                (team_account, FundingPolicy::inherit_account_settings()),
                (
                    Account {
                        kind: AccountKind::Personal,
                        id: 2,
                    },
                    FundingPolicy::new_personal_key(),
                ),
                (
                    personal,
                    FundingPolicy {
                        version: 1,
                        account_order: Some(vec![personal, team_account]),
                        personal_billing_preference: None,
                    },
                ),
            ] {
                assert!(
                    matches!(
                        store.issue_key(issuer, account, policy, 3600).await,
                        Err(IdentityError::Forbidden)
                    ),
                    "marked issuance must reject foreign or team funding even for a real developer"
                );
                assert_eq!(mutation_counts(&pool).await, before);
            }
            let key = store
                .issue_key(issuer, personal, FundingPolicy::new_personal_key(), 3600)
                .await
                .unwrap();
            funding(&pool, key.id, issuer_user).await;
        }
        // Reducing existing authority remains possible; no funding is rewritten.
        store.revoke(&alice, old_team.id).await.unwrap();
        assert!(matches!(
            store.authorize(&old_team.secret).await,
            Err(IdentityError::Unauthorized)
        ));
        role.cleanup().await;
    }

    #[sqlx::test(migrations = false)]
    async fn marked_existing_mixed_key_can_be_inspected_and_revoked_but_not_revealed_or_edited(
        pool: PgPool,
    ) {
        let (owner, owner_app, alice, _) = fixture(&pool).await;
        let mut body = input();
        body["one_time_reveal"] = json!(false);
        let key = create(&owner_app, &alice, body).await;
        let id = key["id"].as_i64().unwrap();
        let team = owner.create_team(&alice).await.unwrap();
        // Explicit synthetic owner setup for a previously granted mixed-funding key.
        sqlx::query("INSERT INTO core_identity.key_funding_rules(credential_id,position,payer_account_id) SELECT $1,1,account_id FROM core_identity.teams WHERE id=$2").bind(id).bind(team).execute(&pool).await.unwrap();
        sqlx::query("INSERT INTO core_identity.credential_grants(credential_id,team_id,team_version,membership_version) SELECT $1,id,version,0 FROM core_identity.teams WHERE id=$2").bind(id).bind(team).execute(&pool).await.unwrap();
        let rules:Vec<(i16,i64)>=sqlx::query_as("SELECT position,payer_account_id FROM core_identity.key_funding_rules WHERE credential_id=$1 ORDER BY position").bind(id).fetch_all(&pool).await.unwrap();
        let (abuse, admin) = policy(&pool, &owner).await;
        mark(&abuse, &admin, 1, 0, true).await;
        let role = restricted(&pool).await;
        let app = app(runtime_store(&role.pool));
        assert_eq!(
            call(
                &app,
                "GET",
                &format!("/api/token/{id}"),
                Some(&alice),
                None,
                None
            )
            .await
            .0,
            StatusCode::OK
        );
        let list = call(&app, "GET", "/api/token/", Some(&alice), None, None).await;
        assert_eq!(list.0, StatusCode::OK);
        assert_eq!(list.1["data"]["total"], 1);
        let before = mutation_counts(&pool).await;
        assert_eq!(
            call(
                &app,
                "POST",
                &format!("/api/token/{id}/key"),
                Some(&alice),
                None,
                Some(ORIGIN)
            )
            .await
            .0,
            StatusCode::FORBIDDEN
        );
        let mut edit = marked_input();
        edit.as_object_mut().unwrap().remove("one_time_reveal");
        edit["id"] = json!(id);
        assert_eq!(
            call(
                &app,
                "PUT",
                "/api/token/",
                Some(&alice),
                Some(edit),
                Some(ORIGIN)
            )
            .await
            .0,
            StatusCode::FORBIDDEN
        );
        assert_eq!(mutation_counts(&pool).await, before);
        assert_eq!(
            call(
                &app,
                "DELETE",
                &format!("/api/token/{id}"),
                Some(&alice),
                None,
                Some(ORIGIN)
            )
            .await
            .0,
            StatusCode::OK
        );
        let after:Vec<(i16,i64)>=sqlx::query_as("SELECT position,payer_account_id FROM core_identity.key_funding_rules WHERE credential_id=$1 ORDER BY position").bind(id).fetch_all(&pool).await.unwrap();
        assert_eq!(after, rules);
        role.cleanup().await;
    }

    #[sqlx::test(migrations = false)]
    async fn unmark_removes_only_the_narrow_capability_and_disabled_user_stays_disabled(
        pool: PgPool,
    ) {
        let (owner, _, _, _) = fixture(&pool).await;
        let (developer, alice) = browser_account(&owner, "browser_developer").await;
        // The synthetic installation owner grants the real policy row. Service
        // level, browser fields and client-supplied flags do not grant access.
        sqlx::query("INSERT INTO core_identity.developer_access(user_id,granted,source_reference) VALUES ($1,TRUE,'synthetic-browser-developer-control')")
            .bind(developer)
            .execute(&pool)
            .await
            .unwrap();
        let (user, secret) = browser_account(&owner, "marked_candidate").await;
        let (abuse, admin) = policy(&pool, &owner).await;
        let role = restricted(&pool).await;
        let store = runtime_store(&role.pool);
        let app = app(store.clone());
        assert_eq!(
            self_view(&app, &alice).await["permissions"]["api_key_management"],
            true
        );
        let before = unchanged_authority(&pool, user).await;
        mark(&abuse, &admin, user, 0, true).await;
        let key = create(&app, &secret, marked_input()).await;
        mark(&abuse, &admin, user, 1, false).await;
        assert_eq!(unchanged_authority(&pool, user).await, before);
        let view = self_view(&app, &secret).await;
        assert_eq!(view["permissions"]["api_key_management"], false);
        assert_eq!(view["developer_access_granted"], false);
        assert_eq!(
            call(&app, "GET", "/api/token/", Some(&secret), None, None)
                .await
                .0,
            StatusCode::FORBIDDEN
        );
        assert!(matches!(
            store
                .api_key_routing_groups(key["key"].as_str().unwrap())
                .await,
            Err(IdentityError::Forbidden)
        ));
        mark(&abuse, &admin, user, 2, true).await;
        sqlx::query("UPDATE core_identity.users SET active=FALSE WHERE id=$1")
            .bind(user)
            .execute(&pool)
            .await
            .unwrap();
        let active: bool = sqlx::query_scalar("SELECT active FROM core_identity.users WHERE id=$1")
            .bind(user)
            .fetch_one(&pool)
            .await
            .unwrap();
        assert!(!active);
        let before = mutation_counts(&pool).await;
        assert_eq!(
            call(
                &app,
                "POST",
                "/api/token/",
                Some(&secret),
                Some(marked_input()),
                Some(ORIGIN)
            )
            .await
            .0,
            StatusCode::UNAUTHORIZED
        );
        assert_eq!(
            call(&app, "GET", "/api/user/self", Some(&secret), None, None)
                .await
                .0,
            StatusCode::UNAUTHORIZED
        );
        assert!(matches!(
            store
                .issue_key(
                    &secret,
                    Account {
                        kind: AccountKind::Personal,
                        id: user
                    },
                    FundingPolicy::new_personal_key(),
                    3600
                )
                .await,
            Err(IdentityError::Unauthorized)
        ));
        assert_eq!(mutation_counts(&pool).await, before);
        assert!(
            !sqlx::query_scalar::<_, bool>("SELECT active FROM core_identity.users WHERE id=$1")
                .bind(user)
                .fetch_one(&pool)
                .await
                .unwrap()
        );
        role.cleanup().await;
    }

    #[sqlx::test(migrations = false)]
    async fn marked_management_does_not_replace_persisted_session_assurance(pool: PgPool) {
        let (owner, _, _, _) = fixture(&pool).await;
        let (user, _) = ordinary(&owner).await;
        let (abuse, admin) = policy(&pool, &owner).await;
        mark(&abuse, &admin, user, 0, true).await;
        let secret = format!("lmms_{:064x}", rand::random::<u128>());
        sqlx::query("INSERT INTO core_identity.credentials(digest,kind,user_id,user_version,owner_account_id,expires_at) SELECT $1,'session',id,auth_version,personal_account_id,clock_timestamp()+interval '10 minutes' FROM core_identity.users WHERE id=$2").bind(Sha256::digest(secret.as_bytes()).to_vec()).bind(user).execute(&pool).await.unwrap();
        let role = restricted(&pool).await;
        let store = runtime_store(&role.pool);
        let app = app(store.clone());
        let before = mutation_counts(&pool).await;
        for path in [
            "/api/user/self",
            "/api/token/",
            "/api/token/auto-groups",
            "/api/user/self/groups",
            "/api/user/models",
        ] {
            assert_eq!(
                call(&app, "GET", path, Some(&secret), None, None).await.0,
                StatusCode::UNAUTHORIZED
            );
        }
        assert_eq!(
            call(
                &app,
                "POST",
                "/api/token/",
                Some(&secret),
                Some(marked_input()),
                Some(ORIGIN)
            )
            .await
            .0,
            StatusCode::UNAUTHORIZED
        );
        assert!(matches!(
            store
                .issue_key(
                    &secret,
                    Account {
                        kind: AccountKind::Personal,
                        id: user
                    },
                    FundingPolicy::new_personal_key(),
                    3600
                )
                .await,
            Err(IdentityError::Unauthorized)
        ));
        assert_eq!(mutation_counts(&pool).await, before);
        role.cleanup().await;
    }
}
