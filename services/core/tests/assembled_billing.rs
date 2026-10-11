//! Actual charging + actual PostgreSQL ledger, not a simulated funds adapter.
//! These tests do not by themselves enable the public model gateway.
use lmm_core::{
    accounts::{Account, AccountKind},
    billing::{
        Credits,
        charging::{
            Budget, Charging, Error, Outcome as ChargeOutcome, Price, Request as ChargeRequest,
            Source, Usage,
        },
        ledger::{Action, Ledger, Outcome, Request},
        transactional_ledger::TransactionalLedger,
    },
    core_schema,
    funding::FundingPolicy,
    identity::{IdentityError, IdentityStore},
};
use sqlx::{
    PgPool,
    postgres::{PgPoolOptions, PgSslMode},
    types::Json,
};
use std::{sync::Arc, time::Duration};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpListener, TcpStream},
};

const WORKER: [u8; 32] = [5; 32];

fn request(id: &str, reserve: i64) -> ChargeRequest {
    ChargeRequest {
        id: id.into(),
        fingerprint: [3; 32],
        reserve,
        lease_seconds: 300,
        maximum_seconds: 600,
        price: Price {
            version: "price-1".into(),
            model: "local-model".into(),
            group: "default".into(),
            input_per_million: 1_000_000,
            cached_per_million: 500_000,
            output_per_million: 2_000_000,
        },
    }
}
fn usage() -> Usage {
    Usage {
        input: 10,
        cached: 2,
        output: 20,
    }
}

async fn fixture(
    pool: &PgPool,
    amount: i64,
) -> (IdentityStore, Ledger, Charging, String, String, i64) {
    core_schema::init(pool).await.unwrap();
    core_schema::check(pool).await.unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    let session = identity.bootstrap_user(7, 1).await.unwrap();
    let person = identity.authorize(&session.secret).await.unwrap();
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
    let ledger = Ledger::from_pool(pool.clone());
    ledger.open_wallet(person.owner_account_id).await.unwrap();
    credit(&ledger, person.owner_account_id, amount, "fixture-credit").await;
    let charging = Charging::new(pool.clone(), Arc::new(TransactionalLedger));
    (
        identity,
        ledger,
        charging,
        session.secret,
        key.secret,
        person.owner_account_id,
    )
}

#[sqlx::test(migrations = false)]
async fn managed_key_model_ip_and_enable_policy_is_checked_inside_wallet_transaction(pool: PgPool) {
    let (identity, ledger, charging, _, key, account) = fixture(&pool, 1000).await;
    let principal = identity.authorize(&key).await.unwrap();
    sqlx::query("INSERT INTO core_identity.api_key_settings(credential_id,name,group_name,model_limits_enabled,model_limits,allow_ips) VALUES ($1,'Synthetic managed key','default',TRUE,ARRAY['local-model'],ARRAY['127.0.0.1/32']::inet[])")
        .bind(principal.credential_id).execute(&pool).await.unwrap();
    sqlx::query("INSERT INTO core_identity.user_routing_groups(user_id,group_name,enabled,source_reference) VALUES (7,'default',TRUE,'synthetic-finance-policy-test') ON CONFLICT(user_id,group_name) DO NOTHING").execute(&pool).await.unwrap();
    let spec = request("managed-key-reserve", 100);
    assert!(matches!(
        charging.reserve(&key, &spec, &WORKER).await,
        Err(Error::Forbidden)
    ));
    assert!(matches!(
        charging
            .reserve_with_context(&key, &spec, &WORKER, Some("192.0.2.1".parse().unwrap()))
            .await,
        Err(Error::Forbidden)
    ));
    let mut wrong_model = spec.clone();
    wrong_model.price.model = "other-model".into();
    assert!(matches!(
        charging
            .reserve_with_context(
                &key,
                &wrong_model,
                &WORKER,
                Some("127.0.0.1".parse().unwrap())
            )
            .await,
        Err(Error::Forbidden)
    ));
    assert_eq!(ledger.balance(account).await.unwrap().available_units, 1000);
    charging
        .reserve_with_context(&key, &spec, &WORKER, Some("127.0.0.1".parse().unwrap()))
        .await
        .unwrap();
    assert_eq!(ledger.balance(account).await.unwrap().available_units, 900);
    sqlx::query("UPDATE core_identity.api_key_settings SET enabled=FALSE WHERE credential_id=$1")
        .bind(principal.credential_id)
        .execute(&pool)
        .await
        .unwrap();
    let another = request("disabled-managed-key", 1);
    assert!(matches!(
        charging
            .reserve_with_context(&key, &another, &WORKER, Some("127.0.0.1".parse().unwrap()))
            .await,
        Err(Error::Unauthorized) | Err(Error::Forbidden)
    ));
    assert_eq!(ledger.balance(account).await.unwrap().available_units, 900);
    assert_balanced(&pool).await;
}

async fn credit(ledger: &Ledger, account: i64, amount: i64, id: &str) {
    let result = ledger
        .execute(&Request {
            scope: "assembled-billing-test".into(),
            operation_key: id.into(),
            actor_user_id: Some(7),
            reason: "Synthetic funds in a disposable local test database".into(),
            action: Action::Credit {
                account_id: account,
                amount_units: Credits::try_from(amount).unwrap(),
                payment_reference: format!("disposable-fixture:{id}"),
            },
        })
        .await
        .unwrap();
    assert!(matches!(result, Outcome::Posted { .. }));
}

async fn assert_balanced(pool: &PgPool) {
    let unbalanced: i64 = sqlx::query_scalar("SELECT count(*) FROM (SELECT journal_id FROM core_billing.ledger_entries GROUP BY journal_id HAVING sum(delta_units)<>0) s")
        .fetch_one(pool).await.unwrap();
    let negative: i64 = sqlx::query_scalar("SELECT count(*) FROM core_billing.balance_state s JOIN core_billing.ledger_accounts a ON a.id=s.ledger_account_id WHERE a.bucket IN ('wallet','reserved') AND s.balance_units<0")
        .fetch_one(pool).await.unwrap();
    assert_eq!((unbalanced, negative), (0, 0));
}

#[sqlx::test(migrations = false)]
async fn complete_installation_is_atomic_and_drift_is_rejected(pool: PgPool) {
    let (a, b) = tokio::join!(core_schema::init(&pool), core_schema::init(&pool));
    assert_eq!(usize::from(a.is_ok()) + usize::from(b.is_ok()), 1);
    assert!(matches!(a, Err(IdentityError::Conflict)) || matches!(b, Err(IdentityError::Conflict)));
    core_schema::check(&pool).await.unwrap();
    let components: i64 = sqlx::query_scalar("SELECT count(*) FROM core_meta.component_contract")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(components as usize, core_schema::component_count());
    assert!(matches!(
        core_schema::init(&pool).await,
        Err(IdentityError::Conflict)
    ));
    sqlx::query("ALTER TABLE core_charging.requests ADD COLUMN unexpected TEXT")
        .execute(&pool)
        .await
        .unwrap();
    assert!(matches!(
        core_schema::check(&pool).await,
        Err(IdentityError::Storage)
    ));
    // Startup did not silently repair or remove the unexpected column.
    let exists: bool = sqlx::query_scalar("SELECT EXISTS(SELECT FROM information_schema.columns WHERE table_schema='core_charging' AND table_name='requests' AND column_name='unexpected')")
        .fetch_one(&pool).await.unwrap();
    assert!(exists);
}

#[sqlx::test(migrations = false)]
async fn occupied_database_is_preserved(pool: PgPool) {
    sqlx::raw_sql("CREATE TABLE sentinel(value TEXT); INSERT INTO sentinel VALUES ('keep')")
        .execute(&pool)
        .await
        .unwrap();
    assert!(matches!(
        core_schema::init(&pool).await,
        Err(IdentityError::Conflict)
    ));
    let value: String = sqlx::query_scalar("SELECT value FROM sentinel")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(value, "keep");
    let exists: bool =
        sqlx::query_scalar("SELECT EXISTS(SELECT FROM pg_namespace WHERE nspname='core_identity')")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(!exists);
}

#[sqlx::test(migrations = false)]
async fn actual_wallet_absolute_holds_capture_refund_and_replays(pool: PgPool) {
    let (_, ledger, charging, _, key, account) = fixture(&pool, 1000).await;
    let spec = request("request-a", 100);
    let first = charging.reserve(&key, &spec, &WORKER).await.unwrap();
    assert_eq!(
        charging
            .reserve(&key, &spec, &WORKER)
            .await
            .unwrap()
            .revision,
        first.revision
    );
    assert!(charging.start(&spec.id, &WORKER).await.unwrap());
    assert!(!charging.start(&spec.id, &WORKER).await.unwrap());
    charging
        .increase(&key, &spec.id, &WORKER, 180)
        .await
        .unwrap();
    charging
        .increase(&key, &spec.id, &WORKER, 180)
        .await
        .unwrap();
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!(
        (balance.available_units, balance.reserved_units),
        (820, 180)
    );
    let settled = charging
        .settle(&spec.id, &WORKER, usage(), ChargeOutcome::Completed)
        .await
        .unwrap();
    assert_eq!(settled.settled, 49);
    charging
        .settle(&spec.id, &WORKER, usage(), ChargeOutcome::Completed)
        .await
        .unwrap();
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (951, 0));
    charging
        .refund(&spec.id, "refund-1", 20, "synthetic refund evidence 1")
        .await
        .unwrap();
    charging
        .refund(&spec.id, "refund-1", 20, "synthetic refund evidence 1")
        .await
        .unwrap();
    assert_eq!(
        charging
            .refund(&spec.id, "refund-1", 21, "synthetic refund evidence 1")
            .await
            .unwrap_err(),
        Error::Conflict
    );
    charging
        .refund(&spec.id, "refund-2", 29, "synthetic refund evidence 2")
        .await
        .unwrap();
    assert_eq!(
        charging
            .refund(&spec.id, "over-refund", 1, "synthetic evidence")
            .await
            .unwrap_err(),
        Error::Invalid
    );
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (1000, 0));
    assert_balanced(&pool).await;
    core_schema::check(&pool).await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn failure_after_money_writes_rolls_back_capture_and_charging_together(pool: PgPool) {
    let (_, ledger, charging, _, key, account) = fixture(&pool, 1000).await;
    let spec = request("rollback", 100);
    charging.reserve(&key, &spec, &WORKER).await.unwrap();
    charging.start(&spec.id, &WORKER).await.unwrap();
    sqlx::raw_sql("CREATE FUNCTION public.reject_final_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_id='final' THEN RAISE EXCEPTION 'injected transaction failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_final_event BEFORE INSERT ON core_charging.usage_events FOR EACH ROW EXECUTE FUNCTION public.reject_final_event()")
        .execute(&pool).await.unwrap();
    assert_eq!(
        charging
            .settle(&spec.id, &WORKER, usage(), ChargeOutcome::Cancelled)
            .await
            .unwrap_err(),
        Error::Storage
    );
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!(
        (balance.available_units, balance.reserved_units),
        (900, 100)
    );
    assert_eq!(
        charging.resolve(&spec.id).await.unwrap().unwrap().state,
        "streaming"
    );
    let captures: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_billing.ledger_journals WHERE kind='capture'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(captures, 0);
    sqlx::raw_sql("DROP TRIGGER reject_final_event ON core_charging.usage_events; DROP FUNCTION public.reject_final_event()")
        .execute(&pool).await.unwrap();
    charging
        .settle(&spec.id, &WORKER, usage(), ChargeOutcome::Cancelled)
        .await
        .unwrap();
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (951, 0));
    assert_balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn competing_real_holds_cannot_overdraw_and_release_restores_money(pool: PgPool) {
    let (_, ledger, charging, _, key, account) = fixture(&pool, 100).await;
    let mut tasks = tokio::task::JoinSet::new();
    for index in 0..4 {
        let charging = charging.clone();
        let key = key.clone();
        tasks.spawn(async move {
            let spec = request(&format!("concurrent-{index}"), 40);
            for _ in 0..12 {
                match charging.reserve(&key, &spec, &WORKER).await {
                    Err(Error::Retry) => tokio::task::yield_now().await,
                    outcome => return (spec.id, outcome),
                }
            }
            panic!("bounded serializable retries exhausted");
        });
    }
    let mut accepted = Vec::new();
    let mut rejected = 0;
    while let Some(result) = tasks.join_next().await {
        let (id, result) = result.unwrap();
        match result {
            Ok(_) => accepted.push(id),
            Err(Error::NoFunds) => rejected += 1,
            Err(error) => panic!("unexpected charging failure {error:?}"),
        }
    }
    assert_eq!((accepted.len(), rejected), (2, 2));
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (20, 80));
    for id in accepted {
        charging.release(&id, &WORKER).await.unwrap();
        charging.release(&id, &WORKER).await.unwrap();
    }
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (100, 0));
    assert_balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn a_team_charge_keeps_the_original_wallet_after_key_revocation(pool: PgPool) {
    let (identity, ledger, charging, session, _, personal) = fixture(&pool, 1000).await;
    let team = identity.create_team(&session).await.unwrap();
    let key = identity
        .issue_key(
            &session,
            Account {
                kind: AccountKind::Team,
                id: team,
            },
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let account = identity
        .authorize(&key.secret)
        .await
        .unwrap()
        .owner_account_id;
    ledger.open_wallet(account).await.unwrap();
    credit(&ledger, account, 200, "team-credit").await;
    let spec = request("team-request", 100);
    charging.reserve(&key.secret, &spec, &WORKER).await.unwrap();
    charging.start(&spec.id, &WORKER).await.unwrap();
    identity.revoke(&session, key.id).await.unwrap();
    assert!(matches!(
        charging.increase(&key.secret, &spec.id, &WORKER, 150).await,
        Err(Error::Unauthorized)
    ));
    charging
        .settle(&spec.id, &WORKER, usage(), ChargeOutcome::Completed)
        .await
        .unwrap();
    charging
        .refund(&spec.id, "team-refund", 49, "original team request")
        .await
        .unwrap();
    let team_balance = ledger.balance(account).await.unwrap();
    let personal_balance = ledger.balance(personal).await.unwrap();
    assert_eq!(
        (team_balance.available_units, team_balance.reserved_units),
        (200, 0)
    );
    assert_eq!(
        (
            personal_balance.available_units,
            personal_balance.reserved_units
        ),
        (1000, 0)
    );
    assert_balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn ledger_joins_serializable_but_rejects_repeatable_read(pool: PgPool) {
    let (_, ledger, _, _, _, account) = fixture(&pool, 1000).await;
    let debit = Request {
        scope: "assembled-billing-test".into(),
        operation_key: "serializable-debit".into(),
        actor_user_id: Some(7),
        reason: "Controlled serializable composition".into(),
        action: Action::Charge {
            account_id: account,
            amount_units: Credits::try_from(7).unwrap(),
        },
    };
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx)
        .await
        .unwrap();
    let Json(result): Json<Outcome> = sqlx::query_scalar("SELECT core_billing.post_ledger($1)")
        .bind(Json(&debit))
        .fetch_one(&mut *tx)
        .await
        .unwrap();
    assert!(matches!(result, Outcome::Posted { .. }));
    tx.commit().await.unwrap();
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ")
        .execute(&mut *tx)
        .await
        .unwrap();
    let error = sqlx::query_scalar::<_, Json<Outcome>>("SELECT core_billing.post_ledger($1)")
        .bind(Json(&debit))
        .fetch_one(&mut *tx)
        .await
        .unwrap_err();
    assert_eq!(
        error.as_database_error().unwrap().code().as_deref(),
        Some("P1000")
    );
    tx.rollback().await.unwrap();
    assert_eq!(ledger.balance(account).await.unwrap().available_units, 993);
    assert_balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn same_request_waiters_retry_the_whole_transaction_and_reserve_once(pool: PgPool) {
    let (_, ledger, charging, _, key, account) = fixture(&pool, 1000).await;
    let id = "identical-concurrent-request";
    let mut blocker = pool.begin().await.unwrap();
    sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended('core_charging:' || $1,0))")
        .bind(id)
        .execute(&mut *blocker)
        .await
        .unwrap();
    let mut tasks = tokio::task::JoinSet::new();
    for _ in 0..2 {
        let charging = charging.clone();
        let key = key.clone();
        tasks.spawn(async move { charging.reserve(&key, &request(id, 40), &WORKER).await });
    }
    // Both SERIALIZABLE transactions take their snapshot before this lock is
    // released. The loser must restart rather than permanently conflict.
    let mut both_waiting = false;
    for _ in 0..200 {
        let waiting: i64 = sqlx::query_scalar("SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND NOT granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database())")
            .fetch_one(&pool).await.unwrap();
        if waiting == 2 {
            both_waiting = true;
            break;
        }
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
    assert!(
        both_waiting,
        "both reserve calls must reach the controlled lock wait"
    );
    blocker.commit().await.unwrap();
    let mut retries = 0;
    while let Some(result) = tasks.join_next().await {
        match result.unwrap() {
            Ok(charge) => assert_eq!(charge.reserved, 40),
            Err(Error::Retry) => {
                retries += 1;
                assert_eq!(
                    charging
                        .reserve(&key, &request(id, 40), &WORKER)
                        .await
                        .unwrap()
                        .reserved,
                    40
                );
            }
            Err(error) => panic!("same-payload race must be replayable: {error:?}"),
        }
    }
    assert_eq!(retries, 1);
    assert_eq!(
        charging
            .reserve(&key, &request(id, 41), &WORKER)
            .await
            .unwrap_err(),
        Error::Conflict
    );
    let counts: (i64, i64) = sqlx::query_as("SELECT (SELECT count(*) FROM core_charging.requests),(SELECT count(*) FROM core_charging.ledger_holds)")
        .fetch_one(&pool).await.unwrap();
    assert_eq!(counts, (1, 1));
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (960, 40));
    assert_balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn actual_wallet_budget_race_cannot_overspend_the_cap(pool: PgPool) {
    let (identity, ledger, charging, session, key, account) = fixture(&pool, 1000).await;
    let team = identity.create_team(&session).await.unwrap();
    let team_key = identity
        .issue_key(
            &session,
            Account {
                kind: AccountKind::Team,
                id: team,
            },
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let team_account = identity
        .authorize(&team_key.secret)
        .await
        .unwrap()
        .owner_account_id;
    ledger.open_wallet(team_account).await.unwrap();
    credit(&ledger, team_account, 1000, "separate-budget-wallet").await;
    charging
        .set_budget(
            &session,
            &Budget {
                scope: "self".into(),
                account_id: None,
                user_id: Some(7),
                key_id: None,
                period: "day".into(),
                anchor: 0,
                seconds: 0,
                limit: 100,
            },
        )
        .await
        .unwrap();
    // Distinct wallets have no common balance row. Hold both until both
    // transactions have read the shared self-budget predicate, proving SSI
    // rather than an incidental shared-wallet lock prevents write skew.
    let mut blocker = pool.begin().await.unwrap();
    sqlx::query("SELECT b.ledger_account_id FROM core_billing.balance_state b JOIN core_billing.ledger_accounts a ON a.id=b.ledger_account_id WHERE a.owner_account_id IN ($1,$2) FOR NO KEY UPDATE OF b")
        .bind(account).bind(team_account).fetch_all(&mut *blocker).await.unwrap();
    let mut tasks = tokio::task::JoinSet::new();
    for (index, key) in [key, team_key.secret].into_iter().enumerate() {
        let charging = charging.clone();
        tasks.spawn(async move {
            let spec = request(&format!("budget-contender-{index}"), 60);
            for _ in 0..12 {
                match charging.reserve(&key, &spec, &WORKER).await {
                    Err(Error::Retry) => tokio::time::sleep(Duration::from_millis(5)).await,
                    result => return result,
                }
            }
            panic!("bounded serializable retries exhausted");
        });
    }
    let mut both_waiting = false;
    for _ in 0..200 {
        let waiting: i64 = sqlx::query_scalar("SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT core_billing.post_ledger%'")
            .fetch_one(&pool).await.unwrap();
        if waiting == 2 {
            both_waiting = true;
            break;
        }
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
    assert!(
        both_waiting,
        "both distinct wallets must reach their independent lock waits"
    );
    blocker.commit().await.unwrap();
    let (mut accepted, mut rejected) = (0, 0);
    while let Some(result) = tasks.join_next().await {
        match result.unwrap() {
            Ok(_) => accepted += 1,
            Err(Error::BudgetExceeded) => rejected += 1,
            Err(error) => panic!("unexpected budget race failure {error:?}"),
        }
    }
    assert_eq!((accepted, rejected), (1, 1));
    let balance = ledger.balance(account).await.unwrap();
    let team_balance = ledger.balance(team_account).await.unwrap();
    assert_eq!(
        (
            balance.available_units + team_balance.available_units,
            balance.reserved_units + team_balance.reserved_units
        ),
        (1940, 60)
    );
    assert_balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn subscription_receipts_never_debit_wallet_cash(pool: PgPool) {
    let (identity, ledger, charging, session, _, account) = fixture(&pool, 1000).await;
    // New personal keys intentionally override the account to wallet_only.
    // This key explicitly inherits the account's subscription_only policy.
    let key = identity
        .issue_key(
            &session,
            Account {
                kind: AccountKind::Personal,
                id: 7,
            },
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap()
        .secret;
    let mut seed = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *seed)
        .await
        .unwrap();
    sqlx::query("UPDATE core_billing.account_policies SET preference='subscription_only' WHERE account_id=$1")
        .bind(account).execute(&mut *seed).await.unwrap();
    let subscription: i64 = sqlx::query_scalar("INSERT INTO core_charging.subscriptions(account_id,grant_reference,period,anchor,seconds,ends_at,limit_credits,models,groups) VALUES ($1,'synthetic-purchased-grant','custom',0,86400,253402300799,100,ARRAY['local-model'],ARRAY['default']) RETURNING id")
        .bind(account).fetch_one(&mut *seed).await.unwrap();
    seed.commit().await.unwrap();
    let spec = request("subscription-funded", 100);
    let charge = charging.reserve(&key, &spec, &WORKER).await.unwrap();
    assert!(matches!(charge.source, Source::Subscription { id, .. } if id == subscription));
    charging.start(&spec.id, &WORKER).await.unwrap();
    charging
        .settle(&spec.id, &WORKER, usage(), ChargeOutcome::Completed)
        .await
        .unwrap();
    charging
        .refund(
            &spec.id,
            "subscription-refund",
            49,
            "authoritative synthetic refund",
        )
        .await
        .unwrap();
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (1000, 0));
    let counts: (i64, i64, i64) = sqlx::query_as("SELECT (SELECT count(*) FROM core_charging.ledger_commands),(SELECT count(*) FROM core_charging.ledger_holds),(SELECT count(*) FROM core_billing.ledger_journals)")
        .fetch_one(&pool).await.unwrap();
    assert_eq!(counts, (3, 0, 1)); // Only the synthetic initial credit moved cash.
    assert_eq!(
        charging.resolve(&spec.id).await.unwrap().unwrap().refunded,
        49
    );
    assert_balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn restarted_recovery_retains_dispatched_money_until_evidence(pool: PgPool) {
    let (_, ledger, charging, _, key, account) = fixture(&pool, 1000).await;
    charging
        .reserve(&key, &request("dispatched-before-crash", 100), &WORKER)
        .await
        .unwrap();
    charging
        .start("dispatched-before-crash", &WORKER)
        .await
        .unwrap();
    charging
        .reserve(&key, &request("unstarted-before-crash", 40), &WORKER)
        .await
        .unwrap();
    let mut expire = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *expire)
        .await
        .unwrap();
    sqlx::query("UPDATE core_charging.requests SET lease_until=floor(extract(epoch FROM clock_timestamp()))::bigint-1,revision=revision+1")
        .execute(&mut *expire).await.unwrap();
    expire.commit().await.unwrap();
    drop(charging);
    let restarted = Charging::new(pool.clone(), Arc::new(TransactionalLedger));
    assert_eq!(restarted.recover_expired(10).await.unwrap().len(), 2);
    let unknown = restarted
        .resolve("dispatched-before-crash")
        .await
        .unwrap()
        .unwrap();
    assert_eq!(unknown.state, "reconcile");
    assert_eq!(
        restarted
            .resolve("unstarted-before-crash")
            .await
            .unwrap()
            .unwrap()
            .state,
        "released"
    );
    assert_eq!(
        restarted.release(&unknown.id, &WORKER).await.unwrap_err(),
        Error::WrongState
    );
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!(
        (balance.available_units, balance.reserved_units),
        (900, 100)
    );
    restarted
        .reconcile(
            &unknown.id,
            unknown.revision,
            usage(),
            ChargeOutcome::Failed,
            "synthetic provider's final usage record after restart",
        )
        .await
        .unwrap();
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (951, 0));
    assert_balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn charging_tables_reject_nonserializable_writers(pool: PgPool) {
    let (_, _, charging, _, key, _) = fixture(&pool, 1000).await;
    charging
        .reserve(&key, &request("guarded", 10), &WORKER)
        .await
        .unwrap();
    for table in [
        "requests",
        "budgets",
        "subscriptions",
        "usage_events",
        "budget_checks",
        "ledger_commands",
        "ledger_holds",
    ] {
        // A statement trigger checks isolation even when the statement matches
        // no rows, before constraints or money writes can run.
        let statement = format!("DELETE FROM core_charging.{table} WHERE false");
        let error = sqlx::query(&statement).execute(&pool).await.unwrap_err();
        assert_eq!(
            error.as_database_error().unwrap().code().as_deref(),
            Some("25001"),
            "{table}"
        );
        let mut tx = pool.begin().await.unwrap();
        sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
            .execute(&mut *tx)
            .await
            .unwrap();
        sqlx::query(&statement).execute(&mut *tx).await.unwrap();
        tx.commit().await.unwrap();
    }
    charging.start("guarded", &WORKER).await.unwrap();
    charging.heartbeat("guarded", &WORKER, 300).await.unwrap();
    charging
        .checkpoint("guarded", &WORKER, "observed", Usage::default())
        .await
        .unwrap();
}

#[sqlx::test(migrations = false)]
async fn a_mid_installation_failure_rolls_back_every_component(pool: PgPool) {
    // This event trigger belongs only to this disposable SQLx test database.
    // Its pg_catalog function leaves the fresh-app occupancy check unchanged.
    sqlx::raw_sql("CREATE FUNCTION pg_catalog.reject_charging_install() RETURNS event_trigger LANGUAGE plpgsql AS $$ BEGIN IF EXISTS(SELECT FROM pg_namespace WHERE nspname='core_charging') THEN RAISE EXCEPTION 'injected installer failure'; END IF; END $$; CREATE EVENT TRIGGER reject_charging_install ON ddl_command_end WHEN TAG IN ('CREATE SCHEMA') EXECUTE FUNCTION pg_catalog.reject_charging_install()")
        .execute(&pool).await.unwrap();
    assert!(matches!(
        core_schema::init(&pool).await,
        Err(IdentityError::Storage)
    ));
    let schemas: i64 =
        sqlx::query_scalar("SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'core_%'")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(
        schemas, 0,
        "identity, ledger and payment DDL must roll back together"
    );
    sqlx::raw_sql("DROP EVENT TRIGGER reject_charging_install; DROP FUNCTION pg_catalog.reject_charging_install()")
        .execute(&pool).await.unwrap();
    core_schema::init(&pool).await.unwrap();
    core_schema::check(&pool).await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn migration_receipts_preserve_the_catalog_and_runtime_cannot_write_them(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    sqlx::query("INSERT INTO core_migration.import_runs(migration_id,source_archive_sha256,mapping_sha256,manifest) VALUES ('synthetic-import',repeat('a',64),repeat('b',64),'{\"source\":\"synthetic\"}')")
        .execute(&pool).await.unwrap();
    core_schema::check(&pool).await.unwrap(); // Receipt rows do not change the sealed catalog.
    let oid: i64 =
        sqlx::query_scalar("SELECT oid::bigint FROM pg_database WHERE datname=current_database()")
            .fetch_one(&pool)
            .await
            .unwrap();
    let role = format!("migration_reader_{oid}");
    sqlx::raw_sql(&format!("CREATE ROLE {role} NOLOGIN; GRANT USAGE ON SCHEMA core_migration TO {role}; GRANT SELECT ON core_migration.import_runs TO {role}"))
        .execute(&pool).await.unwrap();
    let mut tx = pool.begin().await.unwrap();
    sqlx::query(&format!("SET LOCAL ROLE {role}"))
        .execute(&mut *tx)
        .await
        .unwrap();
    let rows: i64 = sqlx::query_scalar("SELECT count(*) FROM core_migration.import_runs")
        .fetch_one(&mut *tx)
        .await
        .unwrap();
    assert_eq!(rows, 1);
    tx.commit().await.unwrap();
    for statement in [
        "INSERT INTO core_migration.import_runs(migration_id,source_archive_sha256,mapping_sha256,manifest) VALUES ('forbidden',repeat('a',64),repeat('b',64),'{}')",
        "UPDATE core_migration.import_runs SET manifest='{}'",
        "DELETE FROM core_migration.import_runs",
        "TRUNCATE core_migration.import_runs",
    ] {
        let mut tx = pool.begin().await.unwrap();
        sqlx::query(&format!("SET LOCAL ROLE {role}"))
            .execute(&mut *tx)
            .await
            .unwrap();
        let error = sqlx::query(statement).execute(&mut *tx).await.unwrap_err();
        assert_eq!(
            error.as_database_error().unwrap().code().as_deref(),
            Some("42501")
        );
        tx.rollback().await.unwrap();
    }
    sqlx::raw_sql(&format!("DROP OWNED BY {role}; DROP ROLE {role}"))
        .execute(&pool)
        .await
        .unwrap();
    core_schema::check(&pool).await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn disabled_financial_triggers_are_catalog_drift_and_never_repaired(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    for (table, trigger) in [
        ("core_charging.requests", "charging_serializable"),
        ("core_billing.ledger_entries", "ledger_entry_balance"),
        // Includes internal foreign-key triggers as well as application triggers.
        ("core_charging.requests", "ALL"),
    ] {
        sqlx::query(&format!("ALTER TABLE {table} DISABLE TRIGGER {trigger}"))
            .execute(&pool)
            .await
            .unwrap();
        assert!(matches!(
            core_schema::check(&pool).await,
            Err(IdentityError::Storage)
        ));
        let disabled: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT FROM pg_trigger WHERE tgrelid=$1::regclass AND tgenabled='D')",
        )
        .bind(table)
        .fetch_one(&pool)
        .await
        .unwrap();
        assert!(
            disabled,
            "startup check must not silently enable altered triggers"
        );
        sqlx::query(&format!("ALTER TABLE {table} ENABLE TRIGGER {trigger}"))
            .execute(&pool)
            .await
            .unwrap();
        core_schema::check(&pool).await.unwrap();
    }
}

#[sqlx::test(migrations = false)]
async fn rejected_ledger_operation_snapshot_race_is_retryable(pool: PgPool) {
    let (_, ledger, _, _, _, account) = fixture(&pool, 1000).await;
    let spec = Request {
        scope: "assembled-billing-test".into(),
        operation_key: "rejected-race".into(),
        actor_user_id: Some(7),
        reason: "Insufficient-funds receipt snapshot race".into(),
        action: Action::Charge {
            account_id: account,
            amount_units: Credits::try_from(1001).unwrap(),
        },
    };
    let mut blocker = pool.begin().await.unwrap();
    sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended(jsonb_build_array('lmm-ledger-v1',$1::text,$2::text)::text,0))")
        .bind(&spec.scope).bind(&spec.operation_key).execute(&mut *blocker).await.unwrap();
    let mut tasks = tokio::task::JoinSet::new();
    for _ in 0..2 {
        let pool = pool.clone();
        let spec = spec.clone();
        tasks.spawn(async move {
            let mut tx = pool.begin().await.unwrap();
            sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
                .execute(&mut *tx)
                .await
                .unwrap();
            let result =
                sqlx::query_scalar::<_, Json<Outcome>>("SELECT core_billing.post_ledger($1)")
                    .bind(Json(&spec))
                    .fetch_one(&mut *tx)
                    .await;
            match result {
                Ok(Json(outcome)) => {
                    tx.commit().await.unwrap();
                    Ok(outcome)
                }
                Err(error) => {
                    tx.rollback().await.unwrap();
                    Err(error)
                }
            }
        });
    }
    let mut both_waiting = false;
    for _ in 0..200 {
        let waiting: i64 = sqlx::query_scalar("SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND NOT granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database())")
            .fetch_one(&pool).await.unwrap();
        if waiting == 2 {
            both_waiting = true;
            break;
        }
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
    assert!(both_waiting);
    blocker.commit().await.unwrap();
    let mut retries = 0;
    while let Some(result) = tasks.join_next().await {
        let outcome = match result.unwrap() {
            Ok(outcome) => outcome,
            Err(error) => {
                assert_eq!(
                    error.as_database_error().unwrap().code().as_deref(),
                    Some("40001")
                );
                retries += 1;
                ledger.execute(&spec).await.unwrap()
            }
        };
        assert!(matches!(
            outcome,
            Outcome::Rejected {
                code: lmm_core::billing::ledger::Rejection::InsufficientFunds
            }
        ));
    }
    assert_eq!(retries, 1);
    let receipts: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_billing.ledger_operations WHERE scope=$1 AND operation_key=$2",
    )
    .bind(&spec.scope)
    .bind(&spec.operation_key)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(receipts, 1);
    assert_eq!(ledger.balance(account).await.unwrap().available_units, 1000);
    assert_balanced(&pool).await;
}

// Same PostgreSQL frame fault used by the standalone ledger acceptance test:
// wait for the server's COMMIT+ReadyForQuery, then drop its acknowledgement.
async fn lost_ack_pool(pool: &PgPool) -> (PgPool, tokio::task::JoinHandle<()>) {
    let options = pool.connect_options();
    let upstream = (options.get_host().to_owned(), options.get_port());
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    let proxy = tokio::spawn(async move {
        let (client, _) = listener.accept().await.unwrap();
        let server = TcpStream::connect(upstream).await.unwrap();
        let (mut client_read, mut client_write) = client.into_split();
        let (mut server_read, mut server_write) = server.into_split();
        let upload =
            tokio::spawn(async move { tokio::io::copy(&mut client_read, &mut server_write).await });
        let mut suppress_commit = false;
        loop {
            let mut header = [0_u8; 5];
            server_read.read_exact(&mut header).await.unwrap();
            let length = u32::from_be_bytes(header[1..5].try_into().unwrap()) as usize;
            assert!((4..=1_048_576).contains(&length));
            let mut payload = vec![0; length - 4];
            server_read.read_exact(&mut payload).await.unwrap();
            if header[0] == b'C' && payload == b"COMMIT\0" {
                suppress_commit = true;
            }
            if suppress_commit {
                if header[0] == b'Z' {
                    upload.abort();
                    let _ = upload.await;
                    break;
                }
            } else {
                client_write.write_all(&header).await.unwrap();
                client_write.write_all(&payload).await.unwrap();
            }
        }
    });
    let fault_pool = PgPoolOptions::new()
        .max_connections(1)
        .connect_with(
            options
                .as_ref()
                .clone()
                .host("127.0.0.1")
                .port(port)
                .ssl_mode(PgSslMode::Disable),
        )
        .await
        .unwrap();
    (fault_pool, proxy)
}

#[sqlx::test(migrations = false)]
async fn lost_commit_acknowledgements_resolve_without_double_money_or_dispatch(pool: PgPool) {
    let (_, ledger, charging, _, key, account) = fixture(&pool, 1000).await;
    let spec = request("ambiguous-commits", 100);
    for step in 0..4 {
        let (fault_pool, proxy) = lost_ack_pool(&pool).await;
        let fault = Charging::new(fault_pool.clone(), Arc::new(TransactionalLedger));
        let result = tokio::time::timeout(Duration::from_secs(20), async {
            match step {
                0 => fault.reserve(&key, &spec, &WORKER).await.map(|_| ()),
                1 => fault.start(&spec.id, &WORKER).await.map(|_| ()),
                2 => fault
                    .settle(&spec.id, &WORKER, usage(), ChargeOutcome::Completed)
                    .await
                    .map(|_| ()),
                _ => fault
                    .refund(
                        &spec.id,
                        "ambiguous-refund",
                        20,
                        "synthetic provider refund proof",
                    )
                    .await
                    .map(|_| ()),
            }
        })
        .await
        .unwrap();
        assert_eq!(result.unwrap_err(), Error::CommitUnknown);
        tokio::time::timeout(Duration::from_secs(20), proxy)
            .await
            .unwrap()
            .unwrap();
        fault_pool.close().await;
        let durable = charging.resolve(&spec.id).await.unwrap().unwrap();
        match step {
            0 => {
                assert_eq!(durable.state, "reserved");
                charging.reserve(&key, &spec, &WORKER).await.unwrap();
            }
            1 => {
                assert_eq!(durable.state, "streaming");
                assert!(
                    !charging.start(&spec.id, &WORKER).await.unwrap(),
                    "ambiguous start must never dispatch again"
                );
            }
            2 => {
                assert_eq!(durable.settled, 49);
                charging
                    .settle(&spec.id, &WORKER, usage(), ChargeOutcome::Completed)
                    .await
                    .unwrap();
            }
            _ => {
                assert_eq!(durable.refunded, 20);
                charging
                    .refund(
                        &spec.id,
                        "ambiguous-refund",
                        20,
                        "synthetic provider refund proof",
                    )
                    .await
                    .unwrap();
            }
        }
    }
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (971, 0));
    let counts: (i64, i64) = sqlx::query_as("SELECT (SELECT count(*) FROM core_billing.ledger_journals),(SELECT count(*) FROM core_charging.ledger_commands)")
        .fetch_one(&pool).await.unwrap();
    assert_eq!(counts, (4, 3)); // One credit, reserve, capture and refund.
    assert_balanced(&pool).await;
}
