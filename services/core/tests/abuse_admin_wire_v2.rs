//! Real SQLx-owned PostgreSQL regressions for the explicit public v2 adapter.
use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Method, Request, StatusCode},
};
use lmm_core::{
    abuse::{
        AbuseStore, PolicyConfig,
        admin::{self, PolicyUpdate},
    },
    accounts::{Account, AccountKind},
    billing::{
        Credits,
        charging::{Charging, Price, Request as ChargeRequest},
        ledger::{Action as LedgerAction, Ledger, Request as LedgerRequest},
        transactional_ledger::TransactionalLedger,
    },
    core_schema,
    funding::FundingPolicy,
    identity::{IdentityStore, IssuedCredential},
};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use std::{collections::BTreeSet, sync::Arc};
use tower::ServiceExt;
#[path = "common/runtime.rs"]
mod runtime;
const BIG: i64 = 9_007_199_254_740_993;
struct Fixture {
    identity: IdentityStore,
    app: Router,
    root: IssuedCredential,
    lower: IssuedCredential,
    key: IssuedCredential,
    root_key: IssuedCredential,
    role: runtime::RuntimeRole,
}
async fn fresh(pool: &PgPool) -> Fixture {
    core_schema::init(pool).await.unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    let root = identity.bootstrap_user(1000, 6).await.unwrap();
    let lower = identity.bootstrap_user(1001, 5).await.unwrap();
    let target = identity.bootstrap_user(BIG, 0).await.unwrap();
    identity.bootstrap_user(BIG + 1, 0).await.unwrap();
    identity.bootstrap_user(i64::MAX, 0).await.unwrap();
    let key = identity
        .issue_key(
            &target.secret,
            Account {
                kind: AccountKind::Personal,
                id: BIG,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    let root_key = identity
        .issue_key(
            &root.secret,
            Account {
                kind: AccountKind::Personal,
                id: 1000,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    let cfg:PolicyConfig=serde_json::from_value(json!({"version":1,"policies":[{"id":"trap","name":"Synthetic isolation","mode":"fixed_text","fixed_text":"configured response","public_models":["public-model"],"clients":{},"limits":{"requests_per_minute_per_user":10,"requests_per_minute_global":20,"max_concurrent_per_user":1,"max_concurrent_global":2,"max_input_bytes":65536,"max_output_bytes":65536,"max_output_tokens":1000,"output_bytes_per_second":0,"total_timeout_seconds":10}}]})).unwrap();
    AbuseStore::new(pool.clone(), BTreeSet::new())
        .update_policies(
            &root.secret,
            &PolicyUpdate {
                operation_id: "wire-initial-policy".into(),
                reason: "Synthetic adapter regression".into(),
                expected_revision: 0,
                config: cfg,
            },
        )
        .await
        .unwrap();
    let role = runtime::RuntimeRole::create(pool).await;
    role.optional("core_charging").await;
    let username = reqwest::Url::parse(&role.url)
        .unwrap()
        .username()
        .to_owned();
    let (session, current): (String, String) =
        sqlx::query_as("SELECT session_user::text,current_user::text")
            .fetch_one(&role.pool)
            .await
            .unwrap();
    assert!(
        username == role.name && session == role.name && current == role.name,
        "real restricted connection identity mismatch"
    );
    let app = admin::router(AbuseStore::new(role.pool.clone(), BTreeSet::new()));
    Fixture {
        identity,
        app,
        root,
        lower,
        key,
        root_key,
        role,
    }
}
async fn call(
    app: &Router,
    secret: &str,
    method: Method,
    path: &str,
    input: Option<Value>,
) -> (StatusCode, Value) {
    let raw = input.map(|v| serde_json::to_vec(&v).unwrap());
    call_raw(app, secret, method, path, raw).await
}
async fn call_raw(
    app: &Router,
    secret: &str,
    method: Method,
    path: &str,
    input: Option<Vec<u8>>,
) -> (StatusCode, Value) {
    let mut req = Request::builder()
        .method(method)
        .uri(path)
        .header("authorization", format!("Bearer {secret}"));
    let body = if let Some(bytes) = input {
        req = req.header("content-type", "application/json");
        Body::from(bytes)
    } else {
        Body::empty()
    };
    let res = app.clone().oneshot(req.body(body).unwrap()).await.unwrap();
    assert!(
        res.headers()
            .get("cache-control")
            .is_some_and(|v| v == "no-store"),
        "private response cache header missing"
    );
    let status = res.status();
    let bytes = to_bytes(res.into_body(), 131072).await.unwrap();
    (status, serde_json::from_slice(&bytes).unwrap())
}
fn request(op: &str, v2: bool, ids: &[(i64, i64)]) -> Value {
    json!({"operation_id":op,"reason":"Synthetic adapter regression","action":"mark","policy_id":"trap","expected_policy_revision":1,"targets":ids.iter().map(|(id,revision)|json!({"user_id":if v2{json!(id.to_string())}else{json!(id)},"expected_revision":revision})).collect::<Vec<_>>()})
}
fn commit(preview: &Value) -> Value {
    json!({"operation_id":preview["operation_id"],"preview_id":preview["preview_id"],"preview_hash":preview["preview_hash"]})
}
async fn evidence(pool: &PgPool, op: &str) -> String {
    sqlx::query_scalar("SELECT jsonb_build_object('preview',(SELECT to_jsonb(p) FROM core_abuse.previews p WHERE operation_id=$1),'operation',(SELECT to_jsonb(o) FROM core_abuse.operations o WHERE operation_id=$1),'audit',(SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY id),'[]'::jsonb) FROM core_abuse.audit a WHERE operation_id=$1))::text").bind(op).fetch_one(pool).await.unwrap()
}
async fn financial(pool: &PgPool) -> Value {
    let mut out = serde_json::Map::new();
    for table in [
        "core_billing.balance_state",
        "core_billing.ledger_operations",
        "core_billing.ledger_journals",
        "core_billing.ledger_entries",
        "core_charging.requests",
        "core_charging.ledger_holds",
        "core_charging.usage_events",
        "core_charging.refunds",
        "core_events.capacity",
        "core_events.subscriptions",
        "core_events.outbox",
        "core_events.deliveries",
        "core_events.inbox",
    ] {
        let v:Value=sqlx::query_scalar(&format!("SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb) FROM {table} t")).fetch_one(pool).await.unwrap();
        out.insert(table.into(), v);
    }
    Value::Object(out)
}
async fn seed_nonempty_hold(f: &Fixture, pool: &PgPool) {
    let account: i64 =
        sqlx::query_scalar("SELECT personal_account_id FROM core_identity.users WHERE id=$1")
            .bind(BIG)
            .fetch_one(pool)
            .await
            .unwrap();
    let ledger = Ledger::from_pool(pool.clone());
    ledger.open_wallet(account).await.unwrap();
    ledger
        .execute(&LedgerRequest {
            scope: "wire-v2-financial".into(),
            operation_key: "synthetic-funding".into(),
            actor_user_id: None,
            reason: "Isolated synthetic funds".into(),
            action: LedgerAction::Credit {
                account_id: account,
                amount_units: Credits::try_from(1000).unwrap(),
                payment_reference: "synthetic-wire-v2".into(),
            },
        })
        .await
        .unwrap();
    let charging = Charging::new(f.role.pool.clone(), Arc::new(TransactionalLedger));
    let charge = ChargeRequest {
        id: "before-wire-v2-mark".into(),
        fingerprint: [39; 32],
        reserve: 50,
        lease_seconds: 60,
        maximum_seconds: 120,
        price: Price {
            upstream_cost_group_ppm: None,
            version: "synthetic-v1".into(),
            model: "public-model".into(),
            group: "synthetic-group".into(),
            input_per_million: 1_000_000,
            cached_per_million: 0,
            output_per_million: 1_000_000,
        },
    };
    charging
        .reserve(&f.key.secret, &charge, &[37; 32])
        .await
        .unwrap();
    charging.start(&charge.id, &[37; 32]).await.unwrap();
    let held: i64 = sqlx::query_scalar("SELECT count(*) FROM core_charging.ledger_holds")
        .fetch_one(pool)
        .await
        .unwrap();
    assert!(
        held > 0,
        "financial conservation fixture must contain a real hold"
    );
}
#[sqlx::test(migrations = false)]
async fn actual_v2_list_detail_and_numeric_cursor_preserve_every_bigint(pool: PgPool) {
    let f = fresh(&pool).await;
    let (s, v1) = call(
        &f.app,
        &f.root.secret,
        Method::GET,
        "/core/v1/admin/abuse/users?query=native_900719925474099&limit=1",
        None,
    )
    .await;
    assert_eq!(s, StatusCode::OK);
    assert_eq!(v1["users"][0]["user_id"].as_i64(), Some(BIG));
    let (s, v2) = call(
        &f.app,
        &f.root.secret,
        Method::GET,
        "/core/v2/admin/abuse/users?query=native_900719925474099&limit=1",
        None,
    )
    .await;
    assert_eq!(s, StatusCode::OK);
    assert_eq!(v2["version"], 2);
    assert_eq!(v2["users"][0]["user_id"], BIG.to_string());
    assert_eq!(v2["next_after"], BIG.to_string());
    let path =
        format!("/core/v2/admin/abuse/users?query=native_900719925474099&limit=1&after={BIG}");
    let (s, next) = call(&f.app, &f.root.secret, Method::GET, &path, None).await;
    assert_eq!(s, StatusCode::OK);
    assert_eq!(next["users"][0]["user_id"], (BIG + 1).to_string());
    assert!(next["next_after"].is_null());
    for id in [1000, BIG, BIG + 1, i64::MAX] {
        let (s, v) = call(
            &f.app,
            &f.root.secret,
            Method::GET,
            &format!("/core/v2/admin/abuse/users/{id}"),
            None,
        )
        .await;
        assert_eq!(s, StatusCode::OK);
        assert_eq!(v["version"], 2);
        assert_eq!(v["user_id"], id.to_string());
    }
    let (s, policy) = call(
        &f.app,
        &f.root.secret,
        Method::GET,
        "/core/v1/admin/abuse/operations/wire-initial-policy",
        None,
    )
    .await;
    assert_eq!(s, StatusCode::OK);
    let before = evidence(&pool, "wire-initial-policy").await;
    let (s, mixed) = call(
        &f.app,
        &f.root.secret,
        Method::GET,
        "/core/v2/admin/abuse/operations/wire-initial-policy",
        None,
    )
    .await;
    assert_eq!(s, StatusCode::OK);
    assert_eq!(mixed, policy);
    assert_eq!(mixed["version"], 1);
    assert_eq!(before, evidence(&pool, "wire-initial-policy").await);
    f.role.cleanup().await;
}
#[sqlx::test(migrations = false)]
async fn rejected_v2_syntax_and_authority_do_not_create_mutation_evidence(pool: PgPool) {
    let f = fresh(&pool).await;
    let before = evidence(&pool, "wire-invalid-request").await;
    for after in ["0", "01", "-1", "%2B1", "1e3", "9223372036854775808"] {
        let (s, _) = call(
            &f.app,
            &f.root.secret,
            Method::GET,
            &format!("/core/v2/admin/abuse/users?after={after}"),
            None,
        )
        .await;
        assert_eq!(s, StatusCode::BAD_REQUEST);
    }
    for id in [
        json!(BIG),
        json!("0"),
        json!("01"),
        json!("9223372036854775808"),
        Value::Null,
    ] {
        let mut v = request("wire-invalid-request", true, &[(BIG, 0)]);
        v["targets"][0]["user_id"] = id;
        let (s, _) = call(
            &f.app,
            &f.root.secret,
            Method::POST,
            "/core/v2/admin/abuse/users/preview",
            Some(v),
        )
        .await;
        assert_eq!(s, StatusCode::BAD_REQUEST);
    }
    let mut v = request("wire-invalid-request", true, &[(BIG, 0)]);
    v["targets"][0]["extra"] = json!(true);
    let (s, _) = call(
        &f.app,
        &f.root.secret,
        Method::POST,
        "/core/v2/admin/abuse/users/preview",
        Some(v),
    )
    .await;
    assert_eq!(s, StatusCode::BAD_REQUEST);
    let raw=br#"{"operation_id":"wire-invalid-request","reason":"Synthetic","action":"mark","policy_id":"trap","expected_policy_revision":1,"targets":[{"user_id":"7","user_id":"8","expected_revision":0}]}"#.to_vec();
    let (s, _) = call_raw(
        &f.app,
        &f.root.secret,
        Method::POST,
        "/core/v2/admin/abuse/users/preview",
        Some(raw),
    )
    .await;
    assert_eq!(s, StatusCode::BAD_REQUEST);
    for secret in [&f.lower.secret, &f.root_key.secret, &f.key.secret] {
        for (method, path, input) in [
            (Method::GET, "/core/v2/admin/abuse/users", None),
            (Method::GET, "/core/v2/admin/abuse/users/1000", None),
            (
                Method::POST,
                "/core/v2/admin/abuse/users/preview",
                Some(request("wire-invalid-request", true, &[(BIG, 0)])),
            ),
            (
                Method::POST,
                "/core/v2/admin/abuse/users/commit",
                Some(
                    json!({"operation_id":"wire-invalid-request","preview_id":"a".repeat(64),"preview_hash":"b".repeat(64)}),
                ),
            ),
            (
                Method::GET,
                "/core/v2/admin/abuse/operations/wire-initial-policy",
                None,
            ),
            (Method::GET, "/core/v2/admin/abuse/audit", None),
        ] {
            let (s, _) = call(&f.app, secret, method, path, input).await;
            assert_eq!(s, StatusCode::FORBIDDEN);
        }
    }
    assert_eq!(before, evidence(&pool, "wire-invalid-request").await);
    f.role.cleanup().await;
}
#[sqlx::test(migrations = false)]
async fn cached_v1_preview_v2_commit_reconcile_and_audit_leave_hashes_and_money_exact(
    pool: PgPool,
) {
    let f = fresh(&pool).await;
    seed_nonempty_hold(&f, &pool).await;
    let money = financial(&pool).await;
    let op = "wire-cross-version";
    // Intentionally descending request order: request_digest precedes domain sort.
    let (s, p1) = call(
        &f.app,
        &f.root.secret,
        Method::POST,
        "/core/v1/admin/abuse/users/preview",
        Some(request(op, false, &[(BIG + 1, 0), (BIG, 0)])),
    )
    .await;
    assert_eq!(s, StatusCode::OK);
    let preview_before = evidence(&pool, op).await;
    let (s, p2) = call(
        &f.app,
        &f.root.secret,
        Method::POST,
        "/core/v2/admin/abuse/users/preview",
        Some(request(op, true, &[(BIG + 1, 0), (BIG, 0)])),
    )
    .await;
    assert_eq!(s, StatusCode::OK);
    assert_eq!(p2["version"], 2);
    assert_eq!(p1["preview_id"], p2["preview_id"]);
    assert_eq!(p1["preview_hash"], p2["preview_hash"]);
    assert_eq!(p2["targets"][0]["user_id"], BIG.to_string());
    assert_eq!(preview_before, evidence(&pool, op).await);
    let (s, _) = call(
        &f.app,
        &f.root.secret,
        Method::POST,
        "/core/v2/admin/abuse/users/preview",
        Some(request(op, true, &[(BIG, 0), (BIG + 1, 0)])),
    )
    .await;
    assert_eq!(s, StatusCode::CONFLICT);
    assert_eq!(preview_before, evidence(&pool, op).await);
    let input = commit(&p1);
    let (s, applied) = call(
        &f.app,
        &f.root.secret,
        Method::POST,
        "/core/v2/admin/abuse/users/commit",
        Some(input.clone()),
    )
    .await;
    assert_eq!(s, StatusCode::OK);
    assert_eq!(applied["version"], 2);
    assert_eq!(applied["users"][0]["user_id"], BIG.to_string());
    assert_eq!(
        money,
        financial(&pool).await,
        "v2 marking changed nonempty funds or original hold"
    );
    let durable = evidence(&pool, op).await;
    for (method, path, body) in [
        (
            Method::POST,
            "/core/v2/admin/abuse/users/commit",
            Some(input.clone()),
        ),
        (
            Method::GET,
            "/core/v2/admin/abuse/operations/wire-cross-version",
            None,
        ),
    ] {
        let (s, v) = call(&f.app, &f.root.secret, method, path, body).await;
        assert_eq!(s, StatusCode::OK);
        assert_eq!(v, applied);
        assert_eq!(durable, evidence(&pool, op).await);
    }
    let (s, v1) = call(
        &f.app,
        &f.root.secret,
        Method::POST,
        "/core/v1/admin/abuse/users/commit",
        Some(input),
    )
    .await;
    assert_eq!(s, StatusCode::OK);
    assert_eq!(v1["version"], 1);
    assert_eq!(v1["users"][0]["user_id"].as_i64(), Some(BIG));
    assert_eq!(durable, evidence(&pool, op).await);
    let (s, audit) = call(
        &f.app,
        &f.root.secret,
        Method::GET,
        "/core/v2/admin/abuse/audit",
        None,
    )
    .await;
    assert_eq!(s, StatusCode::OK);
    let event = audit["events"]
        .as_array()
        .unwrap()
        .iter()
        .find(|v| v["operation_id"] == op)
        .unwrap();
    assert!(event["id"].is_string());
    assert_eq!(event["actor_user_id"], "1000");
    assert_eq!(
        event["detail"]["transitions"][0]["user_id"],
        BIG.to_string()
    );
    assert_eq!(durable, evidence(&pool, op).await);
    assert_eq!(money, financial(&pool).await);
    let original: Value =
        sqlx::query_scalar("SELECT response FROM core_abuse.previews WHERE operation_id=$1")
            .bind(op)
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(original["version"], 1);
    assert_eq!(original["targets"][0]["user_id"].as_i64(), Some(BIG));
    f.role.cleanup().await;
}
#[sqlx::test(migrations = false)]
async fn v2_preview_v1_commit_and_missing_assurance_or_downgrade_fail_closed(pool: PgPool) {
    let f = fresh(&pool).await;
    let (s, p) = call(
        &f.app,
        &f.root.secret,
        Method::POST,
        "/core/v2/admin/abuse/users/preview",
        Some(request("wire-reverse-version", true, &[(BIG, 0)])),
    )
    .await;
    assert_eq!(s, StatusCode::OK);
    let (s, v1) = call(
        &f.app,
        &f.root.secret,
        Method::POST,
        "/core/v1/admin/abuse/users/commit",
        Some(commit(&p)),
    )
    .await;
    assert_eq!(s, StatusCode::OK);
    assert_eq!(v1["users"][0]["user_id"].as_i64(), Some(BIG));
    let durable = evidence(&pool, "wire-reverse-version").await;
    let (s, v2) = call(
        &f.app,
        &f.root.secret,
        Method::GET,
        "/core/v2/admin/abuse/operations/wire-reverse-version",
        None,
    )
    .await;
    assert_eq!(s, StatusCode::OK);
    assert_eq!(v2["version"], 2);
    assert_eq!(v2["users"][0]["user_id"], BIG.to_string());
    assert_eq!(durable, evidence(&pool, "wire-reverse-version").await);
    // Deliberate installer-created corrupt credential: no persistent assurance.
    // Do not forge a successful proof or delete append-only existing evidence.
    let missing = format!(
        "lmms_{:032x}{:032x}",
        rand::random::<u128>(),
        rand::random::<u128>()
    );
    sqlx::query("INSERT INTO core_identity.credentials(digest,kind,user_id,user_version,owner_account_id,expires_at) SELECT $1,'session',id,auth_version,personal_account_id,clock_timestamp()+interval '1 hour' FROM core_identity.users WHERE id=1000").bind(Sha256::digest(missing.as_bytes()).to_vec()).execute(&pool).await.unwrap();
    let (s, _) = call(
        &f.app,
        &missing,
        Method::GET,
        "/core/v2/admin/abuse/users",
        None,
    )
    .await;
    assert!(
        matches!(s, StatusCode::UNAUTHORIZED | StatusCode::FORBIDDEN),
        "missing assurance authorized admin read"
    );
    // Real generation revocation on the originally issued Session, not a UI role.
    sqlx::query("UPDATE core_identity.users SET platform_role='admin' WHERE id=1000")
        .execute(&pool)
        .await
        .unwrap();
    assert!(
        f.identity.authorize(&f.root.secret).await.is_err(),
        "downgraded original session stayed authorized"
    );
    let (s, _) = call(
        &f.app,
        &f.root.secret,
        Method::GET,
        "/core/v2/admin/abuse/users",
        None,
    )
    .await;
    assert!(
        matches!(s, StatusCode::UNAUTHORIZED | StatusCode::FORBIDDEN),
        "downgraded session authorized v2 read"
    );
    assert_eq!(durable, evidence(&pool, "wire-reverse-version").await);
    f.role.cleanup().await;
}
