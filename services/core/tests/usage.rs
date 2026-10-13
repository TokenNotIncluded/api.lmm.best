//! Real disposable PostgreSQL + actual Charging/TransactionalLedger receipts.
//! No production accounts, providers, schema migrations or success-only adapters.
use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Request, StatusCode},
};
use lmm_core::{
    accounts::{Account, AccountKind},
    billing::{
        Credits,
        charging::{Charging, Outcome, Price, Request as ChargeRequest, Usage},
        ledger::{Action, Ledger, Outcome as LedgerOutcome, Request as LedgerRequest},
        transactional_ledger::TransactionalLedger,
    },
    core_schema,
    funding::FundingPolicy,
    http::{self, Capabilities},
    identity::{IdentityStore, IssuedCredential},
    usage::{self, UsageStore},
};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::sync::Arc;
use tower::ServiceExt;
#[path = "usage/receipts.rs"]
mod receipts;
#[path = "common/runtime.rs"]
mod runtime;
const WORKER: [u8; 32] = [9; 32];
const PATHS: [&str; 3] = ["/api/log/self", "/api/log/self/stat", "/api/data/self"];
struct Fixture {
    identity: IdentityStore,
    charging: Charging,
    ledger: Ledger,
    session: IssuedCredential,
    key: IssuedCredential,
    account: i64,
    app: Router,
}
async fn credit(ledger: &Ledger, account: i64, id: &str) {
    ledger.open_wallet(account).await.unwrap();
    assert!(matches!(
        ledger
            .execute(&LedgerRequest {
                scope: "usage-test".into(),
                operation_key: id.into(),
                actor_user_id: Some(7),
                reason: "Synthetic private fixture".into(),
                action: Action::Credit {
                    account_id: account,
                    amount_units: Credits::try_from(1_000_000).unwrap(),
                    payment_reference: format!("usage-fixture:{id}")
                }
            })
            .await
            .unwrap(),
        LedgerOutcome::Posted { .. }
    ));
}
async fn fixture(pool: &PgPool) -> Fixture {
    core_schema::init(pool).await.unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    let session = identity.bootstrap_user(7, 0).await.unwrap();
    let account = identity
        .authorize(&session.secret)
        .await
        .unwrap()
        .owner_account_id;
    let key = identity
        .issue_key(
            &session.secret,
            Account {
                kind: AccountKind::Personal,
                id: 7,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    sqlx::query("INSERT INTO core_identity.api_key_settings(credential_id,name,group_name) VALUES($1,'Test key','default')").bind(key.id).execute(pool).await.unwrap();
    sqlx::query("INSERT INTO core_identity.user_routing_groups(user_id,group_name,source_reference) VALUES(7,'default','usage-fixture')").execute(pool).await.unwrap();
    let ledger = Ledger::from_pool(pool.clone());
    ledger.open_wallet(account).await.unwrap();
    credit(&ledger, account, "personal-credit").await;
    let charging = Charging::new(pool.clone(), Arc::new(TransactionalLedger));
    // Exercise the production mount, not only the standalone module router.
    let app = http::configured_router(
        pool.clone(),
        Capabilities {
            identity: true,
            ..Default::default()
        },
    );
    Fixture {
        identity,
        charging,
        ledger,
        session,
        key,
        account,
        app,
    }
}
fn spec(id: &str) -> ChargeRequest {
    ChargeRequest {
        id: id.into(),
        fingerprint: [3; 32],
        reserve: 100,
        lease_seconds: 300,
        maximum_seconds: 600,
        price: Price {
            upstream_cost_group_ppm: None,
            version: "v1".into(),
            model: "test-model".into(),
            group: "default".into(),
            input_per_million: 1_000_000,
            cached_per_million: 500_000,
            output_per_million: 2_000_000,
        },
    }
}
fn counts() -> Usage {
    Usage {
        input: 10,
        cached: 2,
        output: 20,
        ..Default::default()
    }
}
async fn settled(f: &Fixture, key: &str, id: &str, outcome: Outcome) {
    f.charging.reserve(key, &spec(id), &WORKER).await.unwrap();
    f.charging.start(id, &WORKER).await.unwrap();
    f.charging
        .checkpoint(id, &WORKER, "provider-final", counts())
        .await
        .unwrap();
    f.charging
        .settle(id, &WORKER, counts(), outcome)
        .await
        .unwrap();
}
async fn get(app: &Router, path: &str, secret: Option<&str>) -> (StatusCode, Value) {
    let mut request = Request::builder().uri(path);
    if let Some(secret) = secret {
        request = request.header("authorization", format!("Bearer {secret}"));
    }
    let response = app
        .clone()
        .oneshot(request.body(Body::empty()).unwrap())
        .await
        .unwrap();
    let status = response.status();
    assert_eq!(response.headers()["cache-control"], "no-store");
    let data: Value =
        serde_json::from_slice(&to_bytes(response.into_body(), 1 << 20).await.unwrap()).unwrap();
    (status, data)
}
async fn ok(f: &Fixture, path: &str) -> Value {
    let (status, body) = get(&f.app, path, Some(&f.session.secret)).await;
    assert_eq!(status, StatusCode::OK, "{path}: {body}");
    assert_eq!(body["success"], true);
    body["data"].clone()
}

#[sqlx::test(migrations = false)]
async fn settled_refunded_failed_cancelled_reserved_streaming_reconcile_and_released(pool: PgPool) {
    let f = fixture(&pool).await;
    settled(&f, &f.key.secret, "completed", Outcome::Completed).await;
    f.charging
        .refund("completed", "refund", 20, "private fixture refund")
        .await
        .unwrap();
    settled(&f, &f.key.secret, "failed", Outcome::Failed).await;
    settled(&f, &f.key.secret, "cancelled", Outcome::Cancelled).await;
    // A real zero-usage cancellation is valid only with explicit final receipts.
    f.charging
        .reserve(&f.key.secret, &spec("zero-cancelled"), &WORKER)
        .await
        .unwrap();
    f.charging.start("zero-cancelled", &WORKER).await.unwrap();
    f.charging
        .settle(
            "zero-cancelled",
            &WORKER,
            Usage::default(),
            Outcome::Cancelled,
        )
        .await
        .unwrap();
    for id in ["reserved", "streaming", "reconcile", "released"] {
        f.charging
            .reserve(&f.key.secret, &spec(id), &WORKER)
            .await
            .unwrap();
    }
    for id in ["streaming", "reconcile"] {
        f.charging.start(id, &WORKER).await.unwrap();
    }
    f.charging.release("released", &WORKER).await.unwrap();
    // Simulate restart recovery using the real recovery transition after expiry.
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::query(
        "UPDATE core_charging.requests SET lease_until=0,revision=revision+1 WHERE id='reconcile'",
    )
    .execute(&mut *tx)
    .await
    .unwrap();
    tx.commit().await.unwrap();
    f.charging.recover_expired(100).await.unwrap();
    let logs = ok(&f, "/api/log/self").await;
    assert_eq!(logs["total"], 8);
    assert_eq!(logs["page"], 1);
    assert_eq!(logs["page_size"], 20);
    let items = logs["items"].as_array().unwrap();
    for id in ["reserved", "streaming", "reconcile", "released"] {
        let row = items.iter().find(|r| r["request_id"] == id).unwrap();
        assert_eq!(row["type"], 5);
        assert_eq!(row["quota_money_available"], false);
        assert!(row.get("prompt_tokens").is_none());
        assert!(row.get("completion_tokens").is_none());
        let other: Value = serde_json::from_str(row["other"].as_str().unwrap()).unwrap();
        assert_eq!(other["usage_status"], "incomplete");
        assert_eq!(
            other["reserved_credits"],
            if id == "released" { 0 } else { 100 }
        );
    }
    let completed = items
        .iter()
        .find(|r| r["request_id"] == "completed")
        .unwrap();
    assert_eq!(completed["quota"], 29);
    assert_eq!(completed["quota_usd"], "0.000058");
    assert_eq!(completed["quota_money_basis"], "captured_credits_per_usd");
    assert_eq!(completed["type"], 2);
    assert_eq!(completed["prompt_tokens"], 10);
    assert_eq!(completed["completion_tokens"], 20);
    for row in items {
        assert!(row["id"].as_i64().unwrap() > 0);
        assert!(row["id"].as_i64().unwrap() < (1 << 52));
        assert!(row.get("channel").is_none());
        assert!(row.get("upstream_request_id").is_none());
    }
    let stats = ok(&f, "/api/log/self/stat").await;
    assert_eq!(stats["quota"], 127);
    assert_eq!(stats["quota_usd"], "0.000254");
    assert_eq!(stats["reserved_credits"], 300);
    assert_eq!(stats["incomplete_count"], 3);
    assert_eq!(stats["rpm"], 4);
    assert_eq!(stats["tpm"], 90);
    let data = ok(&f, "/api/data/self?default_time=day").await;
    let rows = data.as_array().unwrap();
    assert_eq!(
        rows.iter()
            .map(|r| r["quota"].as_i64().unwrap())
            .sum::<i64>(),
        127
    );
    assert_eq!(
        rows.iter()
            .map(|r| r["count"].as_i64().unwrap())
            .sum::<i64>(),
        4
    );
    assert_eq!(
        rows.iter()
            .map(|r| r["token_used"].as_i64().unwrap())
            .sum::<i64>(),
        90
    );
    assert!(
        rows.iter()
            .all(|r| r["created_at"].as_i64().unwrap() % 3600 == 0)
    );
    assert_eq!(ok(&f, "/api/log/self?type=2").await["total"], 1);
    assert_eq!(ok(&f, "/api/log/self?type=5").await["total"], 7);
    assert_eq!(ok(&f,"/api/log/self?model_name=test-model&token_name=Test%20key&group=default&request_id=completed").await["total"],1);
    assert_eq!(ok(&f, "/api/log/self?model_name=absent").await["total"], 0);
    // Repeated/concurrent reads never mutate funds or financial history.
    let before = f.ledger.balance(f.account).await.unwrap();
    let (a, b) = tokio::join!(ok(&f, "/api/log/self"), ok(&f, "/api/log/self"));
    assert_eq!(a, b);
    let after = f.ledger.balance(f.account).await.unwrap();
    assert_eq!(before.available_units, after.available_units);
    assert_eq!(before.reserved_units, after.reserved_units);
}

#[sqlx::test(migrations = false)]
async fn actor_and_personal_owner_isolation_including_team_payer(pool: PgPool) {
    let f = fixture(&pool).await;
    let other = f.identity.bootstrap_user(8, 0).await.unwrap();
    let other_key = f
        .identity
        .issue_key(
            &other.secret,
            Account {
                kind: AccountKind::Personal,
                id: 8,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    let other_account = f
        .identity
        .authorize(&other.secret)
        .await
        .unwrap()
        .owner_account_id;
    credit(&f.ledger, other_account, "other-credit").await;
    settled(&f, &f.key.secret, "personal", Outcome::Completed).await;
    settled(&f, &other_key.secret, "other-actor", Outcome::Completed).await;
    let team = f.identity.create_team(&f.session.secret).await.unwrap();
    let team_key = f
        .identity
        .issue_key(
            &f.session.secret,
            Account {
                kind: AccountKind::Team,
                id: team,
            },
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let team_account = f
        .identity
        .authorize(&team_key.secret)
        .await
        .unwrap()
        .owner_account_id;
    credit(&f.ledger, team_account, "team-credit").await;
    settled(&f, &team_key.secret, "team-owned", Outcome::Completed).await;
    let team_paid_key = f
        .identity
        .issue_key(
            &f.session.secret,
            Account {
                kind: AccountKind::Personal,
                id: 7,
            },
            FundingPolicy {
                version: 1,
                account_order: Some(vec![Account {
                    kind: AccountKind::Team,
                    id: team,
                }]),
                personal_billing_preference: None,
            },
            3600,
        )
        .await
        .unwrap();
    settled(
        &f,
        &team_paid_key.secret,
        "personal-team-paid",
        Outcome::Completed,
    )
    .await;
    let invite = f
        .identity
        .invite(
            &f.session.secret,
            team,
            &lmm_core::identity::InviteRequest {
                recipient_user_id: 8,
                role: lmm_core::accounts::TeamRole::Member,
                can_spend: true,
                ttl_seconds: 3600,
            },
        )
        .await
        .unwrap();
    f.identity
        .accept_invite(&other.secret, &invite.secret)
        .await
        .unwrap();
    let other_team_paid = f
        .identity
        .issue_key(
            &other.secret,
            Account {
                kind: AccountKind::Personal,
                id: 8,
            },
            FundingPolicy {
                version: 1,
                account_order: Some(vec![Account {
                    kind: AccountKind::Team,
                    id: team,
                }]),
                personal_billing_preference: None,
            },
            3600,
        )
        .await
        .unwrap();
    settled(
        &f,
        &other_team_paid.secret,
        "other-actor-same-payer",
        Outcome::Completed,
    )
    .await;
    let logs = ok(&f, "/api/log/self").await;
    assert_eq!(logs["total"], 2);
    let team_row = logs["items"]
        .as_array()
        .unwrap()
        .iter()
        .find(|r| r["request_id"] == "personal-team-paid")
        .unwrap();
    assert_eq!(team_row["team_funded"], true);
    assert_eq!(team_row["payer_kind"], "team");
    assert!(team_row.get("payer_account_id").is_none());
    for id in ["team-owned", "other-actor", "other-actor-same-payer"] {
        assert_eq!(
            ok(&f, &format!("/api/log/self?request_id={id}")).await["total"],
            0
        );
    }
    let stats = ok(&f, "/api/log/self/stat").await;
    assert_eq!(stats["quota"], 98);
    assert_eq!(stats["team_funded_quota"], 49);
    assert_eq!(stats["personal_wallet_quota"], 49);
    let data = ok(&f, "/api/data/self").await;
    assert_eq!(data.as_array().unwrap().len(), 2);
    assert!(
        data.as_array()
            .unwrap()
            .iter()
            .any(|r| r["payer_kind"] == "team")
    );
    for path in PATHS {
        let (status, body) = get(&f.app, path, Some(&other.secret)).await;
        assert_eq!(status, StatusCode::OK);
        assert!(!body.to_string().contains("personal-team-paid"));
    }
    // No admin counterparts are opened by this self-only batch, even for admins.
    let admin = f.identity.bootstrap_user(9, 5).await.unwrap();
    assert_eq!(
        get(&f.app, "/api/log", Some(&admin.secret)).await.0,
        StatusCode::SERVICE_UNAVAILABLE
    );
    assert_eq!(
        get(&f.app, "/api/log/self", Some(&admin.secret)).await.1["data"]["total"],
        0
    );
}

#[sqlx::test(migrations = false)]
async fn auth_denial_key_rejection_revocation_and_storage_failure(pool: PgPool) {
    let f = fixture(&pool).await;
    for path in PATHS {
        for secret in [None, Some("invalid")] {
            let (s, b) = get(&f.app, path, secret).await;
            assert_eq!(s, StatusCode::UNAUTHORIZED);
            assert_eq!(b["success"], false);
        }
        assert_eq!(
            get(&f.app, path, Some(&f.key.secret)).await.0,
            StatusCode::FORBIDDEN
        );
    }
    for path in PATHS {
        for duplicate in [false, true] {
            let mut request = Request::builder().uri(path).header(
                "authorization",
                if duplicate {
                    format!("Bearer {}", f.session.secret)
                } else {
                    format!("Basic {}", f.session.secret)
                },
            );
            if duplicate {
                request = request.header("authorization", format!("Bearer {}", f.key.secret));
            }
            let response = f
                .app
                .clone()
                .oneshot(request.body(Body::empty()).unwrap())
                .await
                .unwrap();
            assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
            assert_eq!(response.headers()["cache-control"], "no-store");
        }
    }
    sqlx::query("UPDATE core_identity.credentials SET revoked_at=clock_timestamp() WHERE id=$1")
        .bind(f.session.id)
        .execute(&pool)
        .await
        .unwrap();
    for path in PATHS {
        assert_eq!(
            get(&f.app, path, Some(&f.session.secret)).await.0,
            StatusCode::UNAUTHORIZED
        );
    }
    let fresh = f.identity.bootstrap_user(10, 0).await.unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    assert_eq!(
        reqwest::Url::parse(&role.url).unwrap().username(),
        role.name
    );
    role.optional("core_charging").await;
    let app = usage::router(UsageStore::new(role.pool.clone()));
    assert_eq!(
        get(&app, "/api/log/self", Some(&fresh.secret)).await.0,
        StatusCode::OK
    );
    // Loss of a required receipt table's SELECT must not become empty success.
    sqlx::raw_sql(&format!(
        "REVOKE SELECT ON core_charging.usage_events FROM {}",
        role.name
    ))
    .execute(&pool)
    .await
    .unwrap();
    for path in PATHS {
        let (s, b) = get(&app, path, Some(&fresh.secret)).await;
        assert_eq!(s, StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(b["success"], false);
        assert!(b.get("data").is_none());
        assert!(!b.to_string().contains("SELECT"));
    }
    role.cleanup().await;
    let unavailable = sqlx::postgres::PgPoolOptions::new()
        .acquire_timeout(std::time::Duration::from_millis(100))
        .connect_lazy("postgres://unused@127.0.0.1:1/unused")
        .unwrap();
    let app = usage::router(UsageStore::new(unavailable));
    for path in PATHS {
        assert_eq!(
            get(&app, path, Some(&fresh.secret)).await.0,
            StatusCode::SERVICE_UNAVAILABLE
        );
    }
}

#[sqlx::test(migrations = false)]
async fn inclusive_time_boundaries_bounded_pagination_and_invalid_queries(pool: PgPool) {
    let f = fixture(&pool).await;
    for id in ["a", "b", "c"] {
        settled(&f, &f.key.secret, id, Outcome::Completed).await;
    }
    let at: i64 =
        sqlx::query_scalar("SELECT authorized_at FROM core_charging.requests WHERE id='a'")
            .fetch_one(&pool)
            .await
            .unwrap();
    for path in PATHS {
        let point = format!("{path}?start_timestamp={at}&end_timestamp={at}&request_id=a");
        let (s, b) = get(&f.app, &point, Some(&f.session.secret)).await;
        assert_eq!(s, StatusCode::OK);
        assert!(b["success"].as_bool().unwrap());
        if path.ends_with("stat") {
            assert_eq!(b["data"]["quota"], 49);
        } else if path == "/api/data/self" {
            assert_eq!(
                b["data"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .map(|r| r["count"].as_i64().unwrap())
                    .sum::<i64>(),
                1
            );
        } else {
            assert_eq!(b["data"]["total"], 1);
        }
        let empty = format!("{path}?start_timestamp={}&end_timestamp={}", at - 1, at - 1);
        let (s, b) = get(&f.app, &empty, Some(&f.session.secret)).await;
        assert_eq!(s, StatusCode::OK);
        if path.ends_with("stat") {
            assert_eq!(b["data"]["quota"], 0);
            assert_eq!(b["data"]["rpm"], 0);
        } else if path == "/api/data/self" {
            assert_eq!(b["data"], json!([]));
        } else {
            assert_eq!(b["data"]["total"], 0);
        }
        let after = format!(
            "{path}?start_timestamp={}&end_timestamp={}&request_id=a",
            at + 1,
            at + 1
        );
        let (status, body) = get(&f.app, &after, Some(&f.session.secret)).await;
        assert_eq!(status, StatusCode::OK);
        if path.ends_with("stat") {
            assert_eq!(body["data"]["quota"], 0);
        } else if path == "/api/data/self" {
            assert_eq!(body["data"], json!([]));
        } else {
            assert_eq!(body["data"]["total"], 0);
        }
        assert_eq!(
            get(
                &f.app,
                &format!(
                    "{path}?start_timestamp={}&end_timestamp={at}",
                    at - 31 * 86400
                ),
                Some(&f.session.secret)
            )
            .await
            .0,
            StatusCode::OK
        );
        for query in [
            "p=0",
            "p=-1",
            "page_size=0",
            "page_size=101",
            "p=9223372036854775807",
            "p=100002&page_size=1",
            "start_timestamp=-1",
            "start_timestamp=2&end_timestamp=1",
            "start_timestamp=0&end_timestamp=2678401",
            "end_timestamp=253402300800",
            "start_timestamp=nan",
            "type=8",
            "default_time=unknown",
            "channel=1",
            "username=other",
            "upstream_request_id=secret",
            "unexpected=1",
            "p=1&p=2",
        ] {
            let (s, b) = get(&f.app, &format!("{path}?{query}"), Some(&f.session.secret)).await;
            assert_eq!(s, StatusCode::BAD_REQUEST, "{path}?{query}: {b}");
            assert_eq!(b["success"], false);
        }
    }
    let all = ok(&f, "/api/log/self?page_size=100").await;
    let expected = all["items"].as_array().unwrap();
    let mut paged = Vec::new();
    for p in 1..=4 {
        let page = ok(&f, &format!("/api/log/self?p={p}&page_size=1")).await;
        assert_eq!(page["total"], 3);
        assert_eq!(page["page"], p);
        paged.extend(page["items"].as_array().unwrap().clone());
    }
    assert_eq!(&paged, expected);
    assert_eq!(
        ok(&f, "/api/log/self?p=100001&page_size=1").await["items"],
        json!([])
    );
}

#[sqlx::test(migrations = false)]
async fn missing_final_receipt_never_becomes_known_zero_usage(pool: PgPool) {
    let f = fixture(&pool).await;
    // A storage-level incomplete imported record is not a valid successful
    // receipt merely because column defaults say zero. Real PG constraints stay.
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::query("INSERT INTO core_charging.requests(id,actor_user_id,key_id,owner_account_id,payer_account_id,fingerprint,worker_digest,spec_digest,price,authorized_at,lease_until,hard_deadline,state,reserved,outcome) VALUES('missing-receipt',7,$1,$2,$2,$3,$3,$3,$4,floor(extract(epoch FROM clock_timestamp()))::bigint,253402300798,253402300799,'settled',0,'completed')")
        .bind(f.key.id).bind(f.account).bind(vec![3u8;32]).bind(serde_json::to_value(spec("unused").price).unwrap()).execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
    for path in PATHS {
        let (s, b) = get(&f.app, path, Some(&f.session.secret)).await;
        assert_eq!(s, StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(b["success"], false);
        assert!(b.get("data").is_none());
    }
    // Corruption belonging to another actor is neither returned nor able to
    // contaminate this actor's otherwise empty successful projection.
    let other = f.identity.bootstrap_user(8, 0).await.unwrap();
    assert_eq!(
        get(&f.app, "/api/data/self", Some(&other.secret)).await.1["data"],
        json!([])
    );
}

#[sqlx::test(migrations = false)]
async fn subscription_consumption_is_not_personal_wallet_spending(pool: PgPool) {
    let f = fixture(&pool).await;
    let key = f
        .identity
        .issue_key(
            &f.session.secret,
            Account {
                kind: AccountKind::Personal,
                id: 7,
            },
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::query("UPDATE core_billing.account_policies SET preference='subscription_only' WHERE account_id=$1").bind(f.account).execute(&mut *tx).await.unwrap();
    sqlx::query("INSERT INTO core_charging.subscriptions(account_id,grant_reference,period,anchor,seconds,ends_at,limit_credits,models,groups) VALUES($1,'usage-fixture-grant','custom',0,86400,253402300799,100,ARRAY['test-model'],ARRAY['default'])").bind(f.account).execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
    settled(&f, &key.secret, "subscription", Outcome::Completed).await;
    f.charging
        .refund(
            "subscription",
            "refund",
            10,
            "synthetic subscription refund",
        )
        .await
        .unwrap();
    sqlx::query("UPDATE core_identity.credentials SET revoked_at=clock_timestamp() WHERE id=$1")
        .bind(key.id)
        .execute(&pool)
        .await
        .unwrap();
    let logs = ok(&f, "/api/log/self?request_id=subscription").await;
    let row = &logs["items"][0];
    assert_eq!(row["quota"], 39);
    assert_eq!(row["subscription_funded"], true);
    let other: Value = serde_json::from_str(row["other"].as_str().unwrap()).unwrap();
    assert_eq!(other["billing_source"], "subscription");
    let stats = ok(&f, "/api/log/self/stat").await;
    assert_eq!(stats["quota"], 39);
    assert_eq!(stats["personal_wallet_quota"], 0);
    assert_eq!(stats["team_funded_quota"], 0);
    let data = ok(&f, "/api/data/self").await;
    assert_eq!(data[0]["subscription_funded"], true);
    assert_eq!(data[0]["quota"], 39);
    let balance = f.ledger.balance(f.account).await.unwrap();
    assert_eq!(balance.available_units, 1_000_000);
    assert_eq!(balance.reserved_units, 0);
}
