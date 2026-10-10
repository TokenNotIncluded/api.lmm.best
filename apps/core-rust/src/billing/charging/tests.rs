//! PostgreSQL contract tests. RecordingLedger has NO wallet or real money.
//! Passing these tests is NOT task 02 ledger/charging joint acceptance.
use super::*;
use serde_json::Value;
use sha2::{Digest, Sha256};
use sqlx::{PgConnection, PgPool};
use std::sync::{
    Arc,
    atomic::{AtomicU8, Ordering},
};

const PERSONAL: &str = "lmmk_personal_11111111111111111111111111111111";
const SECOND: &str = "lmmk_second_22222222222222222222222222222222";
const TEAM: &str = "lmmk_team_33333333333333333333333333333333";
const ADMIN_KEY: &str = "lmmk_admin_44444444444444444444444444444444";
const OWNER_SESSION: &str = "lmms_owner_55555555555555555555555555555555";
const MEMBER_SESSION: &str = "lmms_member_66666666666666666666666666666666";
const ADMIN_SESSION: &str = "lmms_admin_77777777777777777777777777777777";
const WORKER: [u8; 32] = [9; 32];

#[derive(Default)]
struct RecordingLedger {
    // 1: no wallet funds; 2: fail AFTER recording settle; 3: fail AFTER reserve.
    mode: AtomicU8,
}
fn ledger_error(error: sqlx::Error) -> LedgerError {
    match Error::from(error) {
        Error::Retry => LedgerError::Retry,
        _ => LedgerError::Unavailable,
    }
}
impl Ledger for RecordingLedger {
    fn apply<'a>(&'a self, c: &'a mut PgConnection, command: LedgerCommand) -> LedgerFuture<'a> {
        Box::pin(async move {
            let detail = serde_json::to_value(&command).unwrap();
            sqlx::query(
                "INSERT INTO test_ledger_commands(id,detail) VALUES ($1,$2) ON CONFLICT DO NOTHING",
            )
            .bind(&command.operation_id)
            .bind(&detail)
            .execute(&mut *c)
            .await
            .map_err(ledger_error)?;
            let saved: Value =
                sqlx::query_scalar("SELECT detail FROM test_ledger_commands WHERE id=$1")
                    .bind(&command.operation_id)
                    .fetch_one(&mut *c)
                    .await
                    .map_err(ledger_error)?;
            if saved != detail {
                return Err(LedgerError::Conflict);
            }
            let mode = self.mode.load(Ordering::SeqCst);
            if mode == 1
                && command.source == Source::Wallet
                && matches!(command.change, Change::Reserve { .. })
            {
                return Err(LedgerError::Insufficient);
            }
            if (mode == 2 && matches!(command.change, Change::Settle { .. }))
                || (mode == 3 && matches!(command.change, Change::Reserve { .. }))
            {
                return Err(LedgerError::Unavailable);
            }
            Ok(())
        })
    }
}
async fn fixture(pool: &PgPool) -> (Charging, Arc<RecordingLedger>) {
    sqlx::raw_sql(include_str!("../../../schema/identity.sql"))
        .execute(pool)
        .await
        .unwrap();
    sqlx::raw_sql(SCHEMA).execute(pool).await.unwrap();
    sqlx::raw_sql(
        "CREATE TABLE test_ledger_commands(id TEXT PRIMARY KEY,detail JSONB NOT NULL); \
         INSERT INTO core_identity.accounts(id,kind) OVERRIDING SYSTEM VALUE VALUES \
         (101,'personal'),(102,'personal'),(103,'personal'),(501,'team'); \
         INSERT INTO core_identity.users(id,personal_account_id) VALUES (1,101),(2,102),(3,103); \
         INSERT INTO core_identity.teams(id,account_id,owner_user_id,created_by_user_id) OVERRIDING SYSTEM VALUE VALUES (77,501,1,1); \
         INSERT INTO core_identity.memberships(team_id,user_id,role,can_spend) VALUES (77,2,'member',true),(77,3,'admin',true); \
         INSERT INTO core_billing.account_policies(account_id) VALUES (101),(102),(103),(501);",
    ).execute(pool).await.unwrap();
    for (id, secret, kind, user, account) in [
        (11, PERSONAL, "api_key", 2, 102),
        (12, SECOND, "api_key", 2, 102),
        (13, TEAM, "api_key", 2, 501),
        (14, ADMIN_KEY, "api_key", 3, 501),
        (21, OWNER_SESSION, "session", 1, 101),
        (22, MEMBER_SESSION, "session", 2, 102),
        (23, ADMIN_SESSION, "session", 3, 103),
    ] {
        sqlx::query("INSERT INTO core_identity.credentials(id,digest,kind,user_id,user_version,owner_account_id,expires_at) OVERRIDING SYSTEM VALUE VALUES ($1,$2,$3,$4,1,$5,clock_timestamp()+interval '1 day')")
            .bind(i64::from(id)).bind(Sha256::digest(secret.as_bytes()).to_vec()).bind(kind)
            .bind(i64::from(user)).bind(i64::from(account)).execute(pool).await.unwrap();
        if kind == "api_key" {
            sqlx::query("INSERT INTO core_identity.key_funding_rules(credential_id,position,payer_account_id) VALUES ($1,0,$2)")
                .bind(i64::from(id)).bind(i64::from(account)).execute(pool).await.unwrap();
        }
    }
    sqlx::raw_sql("INSERT INTO core_identity.credential_grants(credential_id,team_id,team_version,membership_version) VALUES (13,77,1,1),(14,77,1,1)")
        .execute(pool).await.unwrap();
    let ledger = Arc::new(RecordingLedger::default());
    (Charging::new(pool.clone(), ledger.clone()), ledger)
}
fn request(id: &str, reserve: i64) -> Request {
    Request {
        id: id.into(),
        fingerprint: [1; 32],
        reserve,
        lease_seconds: 300,
        maximum_seconds: 3600,
        price: Price {
            version: "v1".into(),
            model: "test-model".into(),
            group: "default".into(),
            input_per_million: 1_000_000,
            cached_per_million: 500_000,
            output_per_million: 1_000_000,
        },
    }
}
fn usage(output: i64) -> Usage {
    Usage {
        output,
        ..Usage::default()
    }
}
fn cap(
    scope: &str,
    account: Option<i64>,
    user: Option<i64>,
    key: Option<i64>,
    limit: i64,
) -> Budget {
    Budget {
        scope: scope.into(),
        account_id: account,
        user_id: user,
        key_id: key,
        period: "month".into(),
        anchor: 0,
        seconds: 0,
        limit,
    }
}
async fn count(pool: &PgPool, operation: &str) -> i64 {
    sqlx::query_scalar(
        "SELECT count(*) FROM test_ledger_commands WHERE detail->'change'->>'operation'=$1",
    )
    .bind(operation)
    .fetch_one(pool)
    .await
    .unwrap()
}
async fn subscription(pool: &PgPool, account: i64, quota: i64) -> i64 {
    sqlx::query_scalar(
        "INSERT INTO core_charging.subscriptions(account_id,grant_reference,period,anchor,seconds,ends_at,limit_credits,models,groups) \
         VALUES ($1,'test-grant-'||$1,'custom',0,86400,253402300799,$2,ARRAY['test-model'],ARRAY['default']) RETURNING id",
    ).bind(account).bind(quota).fetch_one(pool).await.unwrap()
}
async fn expire(pool: &PgPool, id: &str) {
    sqlx::query("UPDATE core_charging.requests SET lease_until=floor(extract(epoch FROM clock_timestamp()))::bigint-1,revision=revision+1 WHERE id=$1")
        .bind(id).execute(pool).await.unwrap();
}

#[test]
fn exact_cumulative_prices_cache_and_overflow() {
    let mut price = request("math", 0).price;
    assert_eq!(
        price.cost(Usage {
            input: 10,
            cached: 4,
            output: 3
        }),
        Ok(11)
    );
    assert_eq!(
        price.cost(Usage {
            input: 1,
            cached: 2,
            output: 0
        }),
        Err(Error::Invalid)
    );
    price.input_per_million = 1;
    price.output_per_million = 1;
    assert_eq!(price.cost(usage(1)), Ok(1));
    assert_eq!(price.cost(usage(999_999)), Ok(1));
    assert_eq!(price.cost(usage(1_000_001)), Ok(2));
    price.output_per_million = i64::MAX;
    assert_eq!(price.cost(usage(i64::MAX)), Err(Error::Invalid));
    price.input_per_million = -1;
    assert_eq!(price.cost(usage(0)), Err(Error::Invalid));
}
#[test]
fn invalid_specs_and_non_monotone_usage_are_rejected() {
    let mut r = request("x", 1);
    r.lease_seconds = 0;
    assert_eq!(r.validate(), Err(Error::Invalid));
    assert!(!usage(2).follows(usage(3)));
    assert!(!super::types::valid_id("x'; DELETE"));
    assert!(cap("member", Some(501), None, None, 1).validate().is_err());
    assert!(cap("self", None, Some(2), None, 1).validate().is_ok());
}

#[sqlx::test(migrations = false)]
async fn utc_calendar_custom_and_anniversary_boundaries(pool: PgPool) {
    fixture(&pool).await;
    let cases = [
        ("day", 0, 0, 1709164800, 1709164800, 1709251200), // 2024-02-29 UTC
        ("week", 0, 0, 1709164800, 1708905600, 1709510400),
        ("month", 0, 0, 1709164800, 1706745600, 1709251200),
        ("custom", 100, 10, 99, 90, 100),
        ("custom", 100, 10, 100, 100, 110),
        (
            "anniversary_month",
            1706659200,
            0,
            1709251200,
            1709164800,
            1711843200,
        ),
    ];
    let mut c = pool.acquire().await.unwrap();
    sqlx::query("SET TIME ZONE 'Pacific/Auckland'")
        .execute(&mut *c)
        .await
        .unwrap();
    for (period, anchor, seconds, at, start, end) in cases {
        let actual: (i64, i64) =
            sqlx::query_as("SELECT * FROM core_charging.period_window($1,$2,$3,$4)")
                .bind(period)
                .bind(i64::from(anchor))
                .bind(i64::from(seconds))
                .bind(i64::from(at))
                .fetch_one(&mut *c)
                .await
                .unwrap();
        assert_eq!(actual, (i64::from(start), i64::from(end)), "{period}");
    }
}

#[sqlx::test(migrations = false)]
async fn budget_exact_boundary_and_parallel_consumption(pool: PgPool) {
    let (engine, _) = fixture(&pool).await;
    engine
        .set_budget(MEMBER_SESSION, &cap("self", None, Some(2), None, 50))
        .await
        .unwrap();
    let mut jobs = Vec::new();
    for n in 0..16 {
        let engine = engine.clone();
        jobs.push(tokio::spawn(async move {
            let spec = request(&format!("parallel-{n}"), 10);
            for _ in 0..100 {
                match engine.reserve(PERSONAL, &spec, &WORKER).await {
                    Err(Error::Retry | Error::Conflict) => tokio::task::yield_now().await,
                    result => return result,
                }
            }
            panic!("serialization retry budget exhausted");
        }));
    }
    let mut accepted = 0;
    for job in jobs {
        match job.await.unwrap() {
            Ok(_) => accepted += 1,
            Err(Error::BudgetExceeded) => {}
            other => panic!("unexpected result: {other:?}"),
        }
    }
    assert_eq!(accepted, 5);
    assert_eq!(count(&pool, "reserve").await, 5);
    assert_eq!(
        engine
            .reserve(SECOND, &request("plus-one", 1), &WORKER)
            .await
            .unwrap_err(),
        Error::BudgetExceeded
    );
}

#[sqlx::test(migrations = false)]
async fn caps_added_late_changed_or_new_keys_do_not_reset_spend(pool: PgPool) {
    let (engine, _) = fixture(&pool).await;
    engine
        .reserve(PERSONAL, &request("before-cap", 80), &WORKER)
        .await
        .unwrap();
    let mut budget = cap("self", None, Some(2), None, 100);
    let first = engine.set_budget(MEMBER_SESSION, &budget).await.unwrap();
    assert_eq!(
        engine
            .reserve(SECOND, &request("new-key", 21), &WORKER)
            .await
            .unwrap_err(),
        Error::BudgetExceeded
    );
    budget.limit = 90;
    assert_eq!(
        engine.set_budget(MEMBER_SESSION, &budget).await.unwrap(),
        first
    );
    assert_eq!(
        engine
            .reserve(PERSONAL, &request("changed-cap", 11), &WORKER)
            .await
            .unwrap_err(),
        Error::BudgetExceeded
    );
    engine
        .reserve(SECOND, &request("exact", 10), &WORKER)
        .await
        .unwrap();
    assert!(
        sqlx::query("DELETE FROM core_charging.budgets WHERE id=$1")
            .bind(first)
            .execute(&pool)
            .await
            .is_err()
    );
}

#[sqlx::test(migrations = false)]
async fn per_key_and_team_aggregate_limits_are_independent(pool: PgPool) {
    let (engine, _) = fixture(&pool).await;
    engine
        .set_budget(MEMBER_SESSION, &cap("key", None, None, Some(11), 10))
        .await
        .unwrap();
    assert_eq!(
        engine
            .reserve(PERSONAL, &request("key-limit", 11), &WORKER)
            .await
            .unwrap_err(),
        Error::BudgetExceeded
    );
    engine
        .reserve(SECOND, &request("other-key", 11), &WORKER)
        .await
        .unwrap();
    engine
        .set_budget(OWNER_SESSION, &cap("account", Some(501), None, None, 100))
        .await
        .unwrap();
    engine
        .reserve(TEAM, &request("member", 60), &WORKER)
        .await
        .unwrap();
    assert_eq!(
        engine
            .reserve(ADMIN_KEY, &request("admin", 41), &WORKER)
            .await
            .unwrap_err(),
        Error::BudgetExceeded
    );
    engine
        .reserve(ADMIN_KEY, &request("team-exact", 40), &WORKER)
        .await
        .unwrap();
}

#[sqlx::test(migrations = false)]
async fn authorized_fallback_and_team_wallet_isolation(pool: PgPool) {
    let (engine, _) = fixture(&pool).await;
    sqlx::raw_sql(
        "DELETE FROM core_identity.key_funding_rules WHERE credential_id=11; \
        INSERT INTO core_identity.key_funding_rules VALUES (11,0,501),(11,1,102); \
        INSERT INTO core_identity.credential_grants VALUES (11,77,1,1)",
    )
    .execute(&pool)
    .await
    .unwrap();
    engine
        .set_budget(OWNER_SESSION, &cap("member", Some(501), Some(2), None, 50))
        .await
        .unwrap();
    let chosen = engine
        .reserve(PERSONAL, &request("fallback", 60), &WORKER)
        .await
        .unwrap();
    assert_eq!(chosen.payer_account_id, 102);
    assert_eq!(count(&pool, "reserve").await, 1);
    assert_eq!(
        engine
            .reserve(TEAM, &request("no-personal-fallback", 60), &WORKER)
            .await
            .unwrap_err(),
        Error::BudgetExceeded
    );
    assert!(
        sqlx::query("INSERT INTO core_identity.key_funding_rules VALUES (13,1,102)")
            .execute(&pool)
            .await
            .is_err()
    );
    sqlx::query(
        "UPDATE core_identity.memberships SET can_spend=false,version=version+1 WHERE team_id=77 AND user_id=2",
    )
    .execute(&pool)
    .await
    .unwrap();
    // An unauthorized configured candidate rejects the WHOLE order.
    assert_eq!(
        engine
            .reserve(PERSONAL, &request("revoked", 1), &WORKER)
            .await
            .unwrap_err(),
        Error::Forbidden
    );
}

#[sqlx::test(migrations = false)]
async fn subscription_priority_fallback_and_eligibility(pool: PgPool) {
    let (engine, ledger) = fixture(&pool).await;
    let sub = subscription(&pool, 102, 50).await;
    sqlx::query("UPDATE core_billing.account_policies SET preference='subscription_first' WHERE account_id=102").execute(&pool).await.unwrap();
    let first = engine
        .reserve(PERSONAL, &request("sub", 40), &WORKER)
        .await
        .unwrap();
    assert!(matches!(first.source, Source::Subscription { id, .. } if id == sub));
    assert_eq!(
        engine
            .reserve(PERSONAL, &request("wallet-overflow", 11), &WORKER)
            .await
            .unwrap()
            .source,
        Source::Wallet
    );
    sqlx::query(
        "UPDATE core_billing.account_policies SET preference='wallet_first' WHERE account_id=102",
    )
    .execute(&pool)
    .await
    .unwrap();
    assert_eq!(
        engine
            .reserve(PERSONAL, &request("wallet-priority", 1), &WORKER)
            .await
            .unwrap()
            .source,
        Source::Wallet
    );
    ledger.mode.store(1, Ordering::SeqCst);
    assert!(matches!(
        engine
            .reserve(PERSONAL, &request("wallet-empty", 5), &WORKER)
            .await
            .unwrap()
            .source,
        Source::Subscription { .. }
    ));
    sqlx::query("UPDATE core_billing.account_policies SET preference='subscription_only' WHERE account_id=102").execute(&pool).await.unwrap();
    assert_eq!(
        engine
            .reserve(PERSONAL, &request("quota-empty", 6), &WORKER)
            .await
            .unwrap_err(),
        Error::NoFunds
    );
    let mut wrong_model = request("wrong-model", 1);
    wrong_model.price.model = "not-entitled".into();
    assert_eq!(
        engine
            .reserve(PERSONAL, &wrong_model, &WORKER)
            .await
            .unwrap_err(),
        Error::NoFunds
    );
    assert_eq!(count(&pool, "reserve").await, 4); // rejected wallet attempt rolled back
}

#[sqlx::test(migrations = false)]
async fn request_price_usage_settlement_and_refund_are_idempotent(pool: PgPool) {
    let (engine, _) = fixture(&pool).await;
    let spec = request("idempotent", 100);
    let original = engine.reserve(PERSONAL, &spec, &WORKER).await.unwrap();
    assert_eq!(
        engine.reserve(PERSONAL, &spec, &WORKER).await.unwrap(),
        original
    );
    let mut changed = spec.clone();
    changed.price.version = "v2".into();
    assert_eq!(
        engine
            .reserve(PERSONAL, &changed, &WORKER)
            .await
            .unwrap_err(),
        Error::Conflict
    );
    assert_eq!(
        engine.reserve(SECOND, &spec, &WORKER).await.unwrap_err(),
        Error::Conflict
    );
    assert!(engine.start(&spec.id, &WORKER).await.unwrap());
    assert!(!engine.start(&spec.id, &WORKER).await.unwrap());
    engine
        .checkpoint(&spec.id, &WORKER, "chunk-1", usage(30))
        .await
        .unwrap();
    engine
        .checkpoint(&spec.id, &WORKER, "chunk-1", usage(30))
        .await
        .unwrap();
    assert_eq!(
        engine
            .checkpoint(&spec.id, &WORKER, "chunk-1", usage(31))
            .await
            .unwrap_err(),
        Error::Conflict
    );
    let settled = engine
        .settle(&spec.id, &WORKER, usage(30), Outcome::Cancelled)
        .await
        .unwrap();
    assert_eq!(settled.settled, 30);
    assert_eq!(
        engine
            .settle(&spec.id, &WORKER, usage(30), Outcome::Cancelled)
            .await
            .unwrap(),
        settled
    );
    assert_eq!(count(&pool, "settle").await, 1);
    let refund = engine
        .refund(&spec.id, "r1", 10, "support-case-1")
        .await
        .unwrap();
    assert_eq!(
        engine
            .refund(&spec.id, "r1", 10, "support-case-1")
            .await
            .unwrap(),
        refund
    );
    assert_eq!(refund.refunded, 10);
    assert_eq!(refund.payer_account_id, original.payer_account_id);
    assert_eq!(
        engine
            .refund(&spec.id, "r2", 21, "too-large")
            .await
            .unwrap_err(),
        Error::Invalid
    );
    assert_eq!(count(&pool, "refund").await, 1);
    assert!(
        sqlx::query("UPDATE core_charging.requests SET price='{}',revision=revision+1 WHERE id=$1")
            .bind(&spec.id)
            .execute(&pool)
            .await
            .is_err()
    );
}

#[sqlx::test(migrations = false)]
async fn failed_ledger_writes_roll_back_budget_and_keep_holds(pool: PgPool) {
    let (engine, ledger) = fixture(&pool).await;
    ledger.mode.store(3, Ordering::SeqCst);
    assert_eq!(
        engine
            .reserve(PERSONAL, &request("reserve-failed", 50), &WORKER)
            .await
            .unwrap_err(),
        Error::Ledger
    );
    assert!(engine.resolve("reserve-failed").await.unwrap().is_none());
    assert_eq!(count(&pool, "reserve").await, 0);
    ledger.mode.store(0, Ordering::SeqCst);
    engine
        .set_budget(MEMBER_SESSION, &cap("self", None, Some(2), None, 50))
        .await
        .unwrap();
    engine
        .reserve(PERSONAL, &request("settle-failed", 50), &WORKER)
        .await
        .unwrap();
    engine.start("settle-failed", &WORKER).await.unwrap();
    ledger.mode.store(2, Ordering::SeqCst);
    assert_eq!(
        engine
            .settle("settle-failed", &WORKER, usage(20), Outcome::Failed)
            .await
            .unwrap_err(),
        Error::Ledger
    );
    assert_eq!(
        engine
            .resolve("settle-failed")
            .await
            .unwrap()
            .unwrap()
            .state,
        "streaming"
    );
    assert_eq!(count(&pool, "settle").await, 0);
    assert_eq!(
        engine
            .reserve(SECOND, &request("hold-retained", 1), &WORKER)
            .await
            .unwrap_err(),
        Error::BudgetExceeded
    );
    ledger.mode.store(0, Ordering::SeqCst);
    engine
        .settle("settle-failed", &WORKER, usage(20), Outcome::Failed)
        .await
        .unwrap();
    assert_eq!(count(&pool, "settle").await, 1);
    engine
        .reserve(SECOND, &request("unused-restored", 30), &WORKER)
        .await
        .unwrap();
}

#[sqlx::test(migrations = false)]
async fn cancellation_before_dispatch_differs_from_partial_output(pool: PgPool) {
    let (engine, _) = fixture(&pool).await;
    engine
        .reserve(PERSONAL, &request("not-started", 100), &WORKER)
        .await
        .unwrap();
    let release = engine.release("not-started", &WORKER).await.unwrap();
    assert_eq!(release.state, "released");
    assert_eq!(
        engine.release("not-started", &WORKER).await.unwrap(),
        release
    );
    assert_eq!(count(&pool, "release").await, 1);
    engine
        .reserve(PERSONAL, &request("partial", 100), &WORKER)
        .await
        .unwrap();
    engine.start("partial", &WORKER).await.unwrap();
    engine
        .checkpoint("partial", &WORKER, "one", usage(10))
        .await
        .unwrap();
    assert_eq!(
        engine.release("partial", &WORKER).await.unwrap_err(),
        Error::WrongState
    );
    assert_eq!(
        engine
            .settle("partial", &WORKER, usage(0), Outcome::Cancelled)
            .await
            .unwrap_err(),
        Error::Invalid
    );
    assert_eq!(
        engine
            .settle("partial", &WORKER, usage(10), Outcome::Cancelled)
            .await
            .unwrap()
            .settled,
        10
    );
}

#[sqlx::test(migrations = false)]
async fn long_stream_reservation_and_stale_worker_fencing(pool: PgPool) {
    let (engine, ledger) = fixture(&pool).await;
    engine
        .reserve(PERSONAL, &request("stream", 10), &WORKER)
        .await
        .unwrap();
    engine.start("stream", &WORKER).await.unwrap();
    assert_eq!(
        engine
            .checkpoint("stream", &WORKER, "too-much", usage(11))
            .await
            .unwrap_err(),
        Error::NeedReservation
    );
    engine
        .increase(PERSONAL, "stream", &WORKER, 20)
        .await
        .unwrap();
    engine
        .checkpoint("stream", &WORKER, "covered", usage(11))
        .await
        .unwrap();
    let renewed = engine.heartbeat("stream", &WORKER, 300).await.unwrap();
    assert!(renewed.lease_until <= renewed.hard_deadline);
    expire(&pool, "stream").await;
    // Reconstruct the service to ensure nothing depends on in-memory state.
    let restarted = Charging::new(pool.clone(), ledger);
    let recovered = restarted.recover_expired(10).await.unwrap();
    assert_eq!(recovered[0].state, "reconcile");
    assert_eq!(recovered[0].reserved, 20);
    assert_eq!(
        restarted.heartbeat("stream", &WORKER, 1).await.unwrap_err(),
        Error::StaleWorker
    );
    assert_eq!(
        restarted
            .settle("stream", &WORKER, usage(11), Outcome::Completed)
            .await
            .unwrap_err(),
        Error::StaleWorker
    );
    let final_charge = restarted
        .reconcile(
            "stream",
            recovered[0].revision,
            usage(15),
            Outcome::Failed,
            "provider-status-42",
        )
        .await
        .unwrap();
    assert_eq!(final_charge.settled, 15);
    assert_eq!(count(&pool, "release").await, 0);
}

#[sqlx::test(migrations = false)]
async fn expired_unstarted_request_releases_but_unknown_stream_does_not(pool: PgPool) {
    let (engine, _) = fixture(&pool).await;
    for id in ["unstarted", "unknown"] {
        engine
            .reserve(PERSONAL, &request(id, 10), &WORKER)
            .await
            .unwrap();
    }
    engine.start("unknown", &WORKER).await.unwrap();
    expire(&pool, "unstarted").await;
    expire(&pool, "unknown").await;
    engine.recover_expired(10).await.unwrap();
    assert_eq!(
        engine.resolve("unstarted").await.unwrap().unwrap().state,
        "released"
    );
    let unknown = engine.resolve("unknown").await.unwrap().unwrap();
    assert_eq!(unknown.state, "reconcile");
    assert_eq!(unknown.usage, Usage::default());
    assert_eq!(unknown.reserved, 10);
    assert_eq!(count(&pool, "release").await, 1);
}

#[sqlx::test(migrations = false)]
async fn leave_rejoin_and_changed_key_do_not_reset_member_budget(pool: PgPool) {
    let (engine, _) = fixture(&pool).await;
    engine
        .set_budget(OWNER_SESSION, &cap("member", Some(501), Some(2), None, 100))
        .await
        .unwrap();
    engine
        .reserve(TEAM, &request("before-leave", 60), &WORKER)
        .await
        .unwrap();
    engine.start("before-leave", &WORKER).await.unwrap();
    sqlx::query("UPDATE core_identity.memberships SET active=false,version=2 WHERE team_id=77 AND user_id=2").execute(&pool).await.unwrap();
    assert_eq!(
        engine
            .increase(TEAM, "before-leave", &WORKER, 61)
            .await
            .unwrap_err(),
        Error::Forbidden
    );
    // Settlement must still debit the original team after revocation.
    engine
        .settle("before-leave", &WORKER, usage(60), Outcome::Completed)
        .await
        .unwrap();
    sqlx::raw_sql("UPDATE core_identity.memberships SET active=true,version=3 WHERE team_id=77 AND user_id=2; \
        DELETE FROM core_identity.key_funding_rules WHERE credential_id=12; \
        INSERT INTO core_identity.key_funding_rules VALUES (12,0,501); \
        INSERT INTO core_identity.credential_grants VALUES (12,77,1,3)").execute(&pool).await.unwrap();
    assert_eq!(
        engine
            .reserve(TEAM, &request("old-grant", 1), &WORKER)
            .await
            .unwrap_err(),
        Error::Forbidden
    );
    assert_eq!(
        engine
            .reserve(SECOND, &request("rejoined", 41), &WORKER)
            .await
            .unwrap_err(),
        Error::BudgetExceeded
    );
    engine
        .reserve(SECOND, &request("rejoined-exact", 40), &WORKER)
        .await
        .unwrap();
}

#[sqlx::test(migrations = false)]
async fn budget_authority_and_issuer_limits_cannot_override_owner(pool: PgPool) {
    let (engine, _) = fixture(&pool).await;
    let owner_rule = cap("member", Some(501), Some(3), None, 50);
    let a = engine.set_budget(OWNER_SESSION, &owner_rule).await.unwrap();
    let mut admin_rule = owner_rule.clone();
    admin_rule.limit = 100;
    let b = engine.set_budget(ADMIN_SESSION, &admin_rule).await.unwrap();
    assert_ne!(a, b);
    assert_eq!(
        engine
            .reserve(ADMIN_KEY, &request("owner-cap", 51), &WORKER)
            .await
            .unwrap_err(),
        Error::BudgetExceeded
    );
    assert_eq!(
        engine
            .set_budget(MEMBER_SESSION, &owner_rule)
            .await
            .unwrap_err(),
        Error::Forbidden
    );
    assert_eq!(
        engine
            .set_budget(ADMIN_SESSION, &cap("account", Some(501), None, None, 100))
            .await
            .unwrap_err(),
        Error::Forbidden
    );
    engine
        .set_budget(MEMBER_SESSION, &cap("self", None, Some(2), None, 10))
        .await
        .unwrap();
    sqlx::query("UPDATE core_identity.users SET platform_role='superadmin' WHERE id=2")
        .execute(&pool)
        .await
        .unwrap();
    // Role changes invalidate old credentials. Issue a fresh key at the new
    // user generation before testing team spending revocation independently.
    assert_eq!(
        engine
            .reserve(TEAM, &request("old-role-key", 1), &WORKER)
            .await
            .unwrap_err(),
        Error::Unauthorized
    );
    let l6_key = "lmmk_superadmin_88888888888888888888888888888888";
    let new_id: i64 = sqlx::query_scalar("INSERT INTO core_identity.credentials(digest,kind,user_id,user_version,owner_account_id,expires_at) SELECT $1,c.kind,c.user_id,u.auth_version,c.owner_account_id,c.expires_at FROM core_identity.credentials c JOIN core_identity.users u ON u.id=c.user_id WHERE c.id=13 RETURNING id")
        .bind(Sha256::digest(l6_key.as_bytes()).to_vec()).fetch_one(&pool).await.unwrap();
    sqlx::query("INSERT INTO core_identity.key_funding_rules SELECT $1,position,payer_account_id FROM core_identity.key_funding_rules WHERE credential_id=13")
        .bind(new_id).execute(&pool).await.unwrap();
    sqlx::query("INSERT INTO core_identity.credential_grants SELECT $1,team_id,team_version,membership_version FROM core_identity.credential_grants WHERE credential_id=13")
        .bind(new_id).execute(&pool).await.unwrap();
    engine
        .reserve(l6_key, &request("fresh-l6-key", 1), &WORKER)
        .await
        .unwrap();
    sqlx::query(
        "UPDATE core_identity.memberships SET can_spend=false,version=version+1 WHERE user_id=2",
    )
    .execute(&pool)
    .await
    .unwrap();
    assert_eq!(
        engine
            .reserve(l6_key, &request("no-l6-bypass", 1), &WORKER)
            .await
            .unwrap_err(),
        Error::Forbidden
    );
}

#[sqlx::test(migrations = false)]
async fn status_resolution_waits_for_inflight_commit_and_replay_is_safe(pool: PgPool) {
    let (engine, _) = fixture(&pool).await;
    let spec = request("unknown-ack", 10);
    engine.reserve(PERSONAL, &spec, &WORKER).await.unwrap();
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended('core_charging:unknown-ack',0))")
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::query("UPDATE core_charging.requests SET lease_until=lease_until-1,revision=revision+1 WHERE id='unknown-ack'")
        .execute(&mut *tx).await.unwrap();
    let copy = engine.clone();
    let mut read = tokio::spawn(async move { copy.resolve("unknown-ack").await });
    assert!(
        tokio::time::timeout(std::time::Duration::from_millis(40), &mut read)
            .await
            .is_err()
    );
    tx.commit().await.unwrap();
    assert_eq!(read.await.unwrap().unwrap().unwrap().revision, 2);
    engine.reserve(PERSONAL, &spec, &WORKER).await.unwrap();
    assert_eq!(count(&pool, "reserve").await, 1);
}

// Fixture-only historical rows: these test attribution, not real ledger money.
async fn historical(pool: &PgPool, source_id: &str, old_id: &str, subscription_cycle: bool) {
    let sql = if subscription_cycle {
        "INSERT INTO core_charging.requests(id,actor_user_id,key_id,owner_account_id,payer_account_id, \
         fingerprint,worker_digest,spec_digest,price,source_subscription_id,source_start,source_end, \
         authorized_at,lease_until,hard_deadline,state,reserved,output_tokens,settled,outcome) \
         SELECT $2,actor_user_id,key_id,owner_account_id,payer_account_id,fingerprint,worker_digest,spec_digest,price, \
         source_subscription_id,source_start-86400,source_end-86400,authorized_at-86400,lease_until-86400, \
         hard_deadline-86400,'settled',reserved,reserved,reserved,'completed' FROM core_charging.requests WHERE id=$1"
    } else {
        "INSERT INTO core_charging.requests(id,actor_user_id,key_id,owner_account_id,payer_account_id, \
         fingerprint,worker_digest,spec_digest,price,authorized_at,lease_until,hard_deadline, \
         state,reserved,output_tokens,settled,outcome) \
         SELECT $2,r.actor_user_id,r.key_id,r.owner_account_id,r.payer_account_id,r.fingerprint,r.worker_digest,r.spec_digest,r.price, \
         w.start_at-1,w.start_at+299,w.start_at+3599,'settled',r.reserved,r.reserved,r.reserved,'completed' \
         FROM core_charging.requests r CROSS JOIN LATERAL core_charging.period_window('month',0,0,r.authorized_at) w WHERE r.id=$1"
    };
    sqlx::query(sql)
        .bind(source_id)
        .bind(old_id)
        .execute(pool)
        .await
        .unwrap();
}

#[sqlx::test(migrations = false)]
async fn monthly_rollover_and_old_refund_do_not_restore_new_window(pool: PgPool) {
    let (engine, _) = fixture(&pool).await;
    engine
        .reserve(PERSONAL, &request("template", 100), &WORKER)
        .await
        .unwrap();
    engine.release("template", &WORKER).await.unwrap();
    historical(&pool, "template", "last-month", false).await;
    engine
        .set_budget(MEMBER_SESSION, &cap("self", None, Some(2), None, 100))
        .await
        .unwrap();
    engine
        .reserve(PERSONAL, &request("this-month", 100), &WORKER)
        .await
        .unwrap();
    engine
        .refund(
            "last-month",
            "old-refund",
            20,
            "historical-attribution-test",
        )
        .await
        .unwrap();
    assert_eq!(
        engine
            .reserve(SECOND, &request("not-restored", 1), &WORKER)
            .await
            .unwrap_err(),
        Error::BudgetExceeded
    );
}

#[sqlx::test(migrations = false)]
async fn subscription_rollover_refunds_stay_in_original_cycle(pool: PgPool) {
    let (engine, _) = fixture(&pool).await;
    subscription(&pool, 102, 50).await;
    sqlx::query("UPDATE core_billing.account_policies SET preference='subscription_only' WHERE account_id=102").execute(&pool).await.unwrap();
    engine
        .reserve(PERSONAL, &request("sub-template", 50), &WORKER)
        .await
        .unwrap();
    engine.release("sub-template", &WORKER).await.unwrap();
    historical(&pool, "sub-template", "last-cycle", true).await;
    let current = engine
        .reserve(PERSONAL, &request("this-cycle", 50), &WORKER)
        .await
        .unwrap();
    let old_refund = engine
        .refund(
            "last-cycle",
            "sub-refund",
            20,
            "historical-subscription-test",
        )
        .await
        .unwrap();
    assert_ne!(old_refund.source, current.source);
    assert_eq!(
        engine
            .reserve(PERSONAL, &request("no-new-quota", 1), &WORKER)
            .await
            .unwrap_err(),
        Error::NoFunds
    );
}

#[sqlx::test(migrations = false)]
async fn running_stream_cannot_top_up_from_new_subscription_cycle(pool: PgPool) {
    let (engine, _) = fixture(&pool).await;
    sqlx::raw_sql("INSERT INTO core_charging.subscriptions(account_id,grant_reference,period,anchor,seconds,ends_at,limit_credits,models,groups) \
        VALUES (102,'short-cycle','custom',0,1,253402300799,100,ARRAY['test-model'],ARRAY['default']); \
        UPDATE core_billing.account_policies SET preference='subscription_only' WHERE account_id=102").execute(&pool).await.unwrap();
    let original = engine
        .reserve(PERSONAL, &request("cycle-crossing", 10), &WORKER)
        .await
        .unwrap();
    engine.start("cycle-crossing", &WORKER).await.unwrap();
    tokio::time::sleep(std::time::Duration::from_millis(1100)).await;
    assert_eq!(
        engine
            .increase(PERSONAL, "cycle-crossing", &WORKER, 11)
            .await
            .unwrap_err(),
        Error::NoFunds
    );
    let final_charge = engine
        .settle("cycle-crossing", &WORKER, usage(5), Outcome::Completed)
        .await
        .unwrap();
    assert_eq!(final_charge.source, original.source);
    assert_eq!(final_charge.settled, 5);
}
