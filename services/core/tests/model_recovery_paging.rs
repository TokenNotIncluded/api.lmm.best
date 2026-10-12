//! A07 isolated PostgreSQL tests for the S11 recovery-page contract candidate.
//! No supplier or Go process is used. Rebuilding a gateway is not an OS restart.
use lmm_core::{
    accounts::{Account, AccountKind},
    billing::{
        Credits,
        charging::{Charging, Price, Request as ChargeRequest},
        ledger::{Action, Ledger, Request as LedgerRequest},
        transactional_ledger::TransactionalLedger,
    },
    core_schema,
    funding::FundingPolicy,
    identity::IdentityStore,
    model_http::{ChargePolicy, ModelGateway},
    relay::{Limits, RelayError},
    routing::{PriceConfig, PriceSnapshot, Router, RoutingConfig},
};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::{collections::BTreeMap, sync::Arc, time::Duration};

const PAGE_CONTRACT: &str = include_str!("fixtures/model_recovery_page.sql");

struct Fixture {
    gateway: ModelGateway,
    charging: Charging,
    ledger: Ledger,
    key: String,
    account: i64,
}

async fn fixture(pool: &PgPool, install_page: bool) -> Fixture {
    core_schema::init(pool).await.unwrap();
    core_schema::check(pool).await.unwrap();
    if install_page {
        sqlx::raw_sql(PAGE_CONTRACT).execute(pool).await.unwrap();
    }
    let identity = IdentityStore::from_pool(pool.clone());
    let session = identity.bootstrap_user(7, 1).await.unwrap();
    let principal = identity.authorize(&session.secret).await.unwrap();
    let key = identity
        .issue_key(
            &session.secret,
            Account { kind: AccountKind::Personal, id: 7 },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    let ledger = Ledger::from_pool(pool.clone());
    ledger.open_wallet(principal.owner_account_id).await.unwrap();
    ledger.execute(&LedgerRequest {
        scope: "a07-recovery-page-test".into(),
        operation_key: "synthetic-funds".into(),
        actor_user_id: Some(7),
        reason: "Synthetic funds in a disposable SQLx test database".into(),
        action: Action::Credit {
            account_id: principal.owner_account_id,
            amount_units: Credits::try_from(1000_i64).unwrap(),
            payment_reference: "synthetic:a07-recovery-page".into(),
        },
    }).await.unwrap();
    // Recovery needs no configured supplier, credential or outgoing connection.
    let router = Router::new(
        RoutingConfig {
            version: 1,
            groups: vec![],
            upstreams: vec![],
            routes: vec![],
            aliases: vec![],
        },
        Arc::new(PriceSnapshot::compile(PriceConfig { version: 1, rules: vec![] }).unwrap()),
    ).unwrap();
    let gateway = ModelGateway::new(
        pool.clone(), Arc::new(router), vec![], BTreeMap::new(),
        Limits {
            total_timeout: Duration::from_secs(3),
            hook_timeout: Duration::from_secs(2),
            ..Limits::default()
        },
        ChargePolicy { input_token_ceiling: 100, lease_seconds: 30 },
    ).unwrap();
    Fixture {
        gateway,
        charging: Charging::new(pool.clone(), Arc::new(TransactionalLedger)),
        ledger,
        key: key.secret,
        account: principal.owner_account_id,
    }
}

async fn seed(pool: &PgPool, fixture: &Fixture, id: &str, usage: Value, terminal: bool) {
    let worker = [42_u8; 32];
    let request = ChargeRequest {
        id: id.into(),
        fingerprint: [17; 32],
        price: Price {
            version: "synthetic-recovery-v1".into(),
            model: "local-model".into(),
            group: "allowed".into(),
            input_per_million: 1_000_000,
            cached_per_million: 250_000,
            output_per_million: 2_000_000,
        },
        reserve: 140,
        lease_seconds: 300,
        maximum_seconds: 300,
    };
    fixture.charging.reserve(&fixture.key, &request, &worker).await.unwrap();
    assert!(fixture.charging.start(id, &worker).await.unwrap());
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx).await.unwrap();
    sqlx::query("SELECT core_model.checkpoint_output($1,$2,1,$3,25,$4,$5)")
        .bind(id).bind(worker.as_slice()).bind([9_u8; 32].as_slice())
        .bind(usage).bind(terminal).execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
}

async fn expire(pool: &PgPool, fixture: &Fixture) {
    // Owner-only test clock control in this disposable database; no live lease
    // or public runtime path is changed. The immutable request/price is retained.
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx).await.unwrap();
    sqlx::query("UPDATE core_charging.requests SET lease_until=0,revision=revision+1 WHERE state='streaming'")
        .execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
    fixture.gateway.recover_expired(1000).await.unwrap();
}

async fn balanced(pool: &PgPool) {
    let unbalanced: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM (SELECT journal_id FROM core_billing.ledger_entries \
         GROUP BY journal_id HAVING sum(delta_units)<>0) entries",
    ).fetch_one(pool).await.unwrap();
    assert_eq!(unbalanced, 0);
}

fn valid_usage() -> Value {
    json!({"input_tokens":10,"cached_input_tokens":2,"output_tokens":20})
}

#[sqlx::test(migrations = false)]
async fn invalid_first_pages_do_not_starve_later_settlement(pool: PgPool) {
    let f = fixture(&pool, true).await;
    seed(&pool, &f, "a-bad-cache", json!({"input_tokens":10,"cached_input_tokens":11,"output_tokens":20}), true).await;
    seed(&pool, &f, "b-bad-type", json!({"input_tokens":10,"cached_input_tokens":"bad","output_tokens":20}), true).await;
    seed(&pool, &f, "c-valid", valid_usage(), true).await;
    seed(&pool, &f, "d-partial", valid_usage(), false).await;
    expire(&pool, &f).await;

    // Demonstrate the legacy first-page failure before exercising the new API.
    assert_eq!(f.gateway.reconcile_completed_streams(1).await.unwrap(), 0);
    assert_eq!(f.gateway.reconcile_completed_streams(1).await.unwrap(), 0);
    let mut after = None;
    for (expected_count, expected_cursor) in [
        (0, Some("a-bad-cache")), (0, Some("b-bad-type")),
        (1, Some("c-valid")), (0, None),
    ] {
        let (recovered, next) = f.gateway
            .reconcile_completed_streams_page(1, after.as_deref()).await.unwrap();
        assert_eq!(recovered, expected_count);
        assert_eq!(next.as_deref(), expected_cursor);
        after = next;
    }
    for id in ["a-bad-cache", "b-bad-type", "d-partial"] {
        let charge = f.charging.resolve(id).await.unwrap().unwrap();
        assert_eq!(charge.state, "reconcile");
        assert_eq!(charge.settled, 0);
        assert_eq!(charge.reserved, 140);
    }
    let charge = f.charging.resolve("c-valid").await.unwrap().unwrap();
    assert_eq!(charge.settled, 49);
    let balance = f.ledger.balance(f.account).await.unwrap();
    assert_eq!(balance.reserved_units, 420);
    assert_eq!(balance.available_units + balance.reserved_units, 951);

    // A later record before the previous cursor is found in the next sweep.
    seed(&pool, &f, "a-later", valid_usage(), true).await;
    expire(&pool, &f).await;
    let (recovered, next) = f.gateway.reconcile_completed_streams_page(1000, None).await.unwrap();
    assert_eq!((recovered, next), (1, None));
    assert_eq!(f.charging.resolve("a-later").await.unwrap().unwrap().settled, 49);
    assert_eq!(f.gateway.reconcile_completed_streams_page(1000, None).await.unwrap(), (0, None));
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn concurrent_pages_confirm_the_same_charge_without_double_debit(pool: PgPool) {
    let f = fixture(&pool, true).await;
    seed(&pool, &f, "single-valid", valid_usage(), true).await;
    expire(&pool, &f).await;
    let (one, two) = tokio::join!(
        f.gateway.reconcile_completed_streams_page(1, None),
        f.gateway.reconcile_completed_streams_page(1, None),
    );
    // Counts are confirmations, not a claim that each worker made a new debit.
    assert!(one.unwrap().0 + two.unwrap().0 >= 1);
    assert_eq!(f.gateway.reconcile_completed_streams_page(1, None).await.unwrap(), (0, None));
    let charge = f.charging.resolve("single-valid").await.unwrap().unwrap();
    assert_eq!(charge.state, "settled");
    assert_eq!(charge.settled, 49);
    let balance = f.ledger.balance(f.account).await.unwrap();
    assert_eq!(balance.available_units, 951);
    assert_eq!(balance.reserved_units, 0);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn missing_contract_and_invalid_pages_never_fall_back_or_expand_access(pool: PgPool) {
    let f = fixture(&pool, false).await;
    assert!(matches!(
        f.gateway.reconcile_completed_streams_page(1, None).await,
        Err(RelayError::Unsupported(_))
    ));
    for (limit, cursor) in [(0, None), (1001, None), (1, Some("")), (1, Some("bad cursor"))] {
        assert!(matches!(
            f.gateway.reconcile_completed_streams_page(limit, cursor).await,
            Err(RelayError::Invalid(_))
        ));
    }
    sqlx::raw_sql(PAGE_CONTRACT).execute(&pool).await.unwrap();
    assert_eq!(f.gateway.reconcile_completed_streams_page(1, None).await.unwrap(), (0, None));
    for (limit, cursor) in [(0, None), (1001, None), (1, Some("")), (1, Some("bad cursor"))] {
        let error = sqlx::query("SELECT * FROM core_model.completed_streams_after($1,$2)")
            .bind(limit).bind(cursor).execute(&pool).await.unwrap_err();
        assert_eq!(error.as_database_error().unwrap().code().as_deref(), Some("23514"));
    }
    let public_execute: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM pg_proc p, \
         LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a \
         WHERE p.oid='core_model.completed_streams_after(integer,text)'::regprocedure \
         AND a.grantee=0 AND a.privilege_type='EXECUTE')",
    ).fetch_one(&pool).await.unwrap();
    assert!(!public_execute);
    let hardened: bool = sqlx::query_scalar(
        "SELECT prosecdef AND proconfig @> ARRAY['search_path=pg_catalog'] FROM pg_proc \
         WHERE oid='core_model.completed_streams_after(integer,text)'::regprocedure",
    ).fetch_one(&pool).await.unwrap();
    assert!(hardened);
    let balance = f.ledger.balance(f.account).await.unwrap();
    assert_eq!(balance.available_units, 1000);
    assert_eq!(balance.reserved_units, 0);
}
