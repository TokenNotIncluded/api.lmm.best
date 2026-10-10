use std::{sync::Arc, time::Duration};

use lmm_core::{
    billing::{
        Credits,
        ledger::{Action, Error, Ledger, Outcome, Rejection, Request, SCHEMA},
    },
    identity::IdentityStore,
};
use sqlx::{PgPool, postgres::PgPoolOptions, postgres::PgSslMode, types::Json};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpListener, TcpStream},
    sync::Barrier,
    task::JoinSet,
};

fn units(value: i64) -> Credits {
    Credits::try_from(value).unwrap()
}

fn request(key: &str, action: Action) -> Request {
    Request {
        scope: "ledger-tests".into(),
        operation_key: key.into(),
        actor_user_id: Some(7),
        reason: "PostgreSQL acceptance test".into(),
        action,
    }
}

async fn fresh(pool: &PgPool) -> Ledger {
    IdentityStore::from_pool(pool.clone())
        .init_database()
        .await
        .unwrap();
    let mut tx = pool.begin().await.unwrap();
    sqlx::raw_sql(SCHEMA).execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
    sqlx::query(
        "INSERT INTO core_identity.accounts(kind) VALUES ('personal'), ('team'), ('personal')",
    )
    .execute(pool)
    .await
    .unwrap();
    let ledger = Ledger::from_pool(pool.clone());
    for account in 1..=3 {
        let first = ledger.open_wallet(account).await.unwrap();
        assert_eq!(first, ledger.open_wallet(account).await.unwrap());
    }
    ledger
}

async fn run(ledger: &Ledger, key: &str, action: Action) -> Outcome {
    ledger.execute(&request(key, action)).await.unwrap()
}

fn posted(outcome: &Outcome) -> i64 {
    match outcome {
        Outcome::Posted {
            journal_id,
            entries,
        } => {
            assert!(entries.len() >= 2);
            assert_eq!(
                entries
                    .iter()
                    .map(|e| i128::from(e.delta_units))
                    .sum::<i128>(),
                0
            );
            *journal_id
        }
        other => panic!("expected posting, got {other:?}"),
    }
}

fn rejected(outcome: Outcome, code: Rejection) {
    assert_eq!(outcome, Outcome::Rejected { code });
}

async fn fund(ledger: &Ledger, key: &str, account_id: i64, amount: i64) -> i64 {
    posted(
        &run(
            ledger,
            key,
            Action::Credit {
                account_id,
                amount_units: units(amount),
                payment_reference: format!("test-provider:{key}"),
            },
        )
        .await,
    )
}

async fn balanced(pool: &PgPool) {
    let bad_journals: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM (SELECT j.id FROM core_billing.ledger_journals j \
         LEFT JOIN core_billing.ledger_entries e ON e.journal_id = j.id GROUP BY j.id \
         HAVING count(e.journal_id) < 2 OR sum(e.delta_units) <> 0) bad",
    )
    .fetch_one(pool)
    .await
    .unwrap();
    assert_eq!(bad_journals, 0);
    let stale: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_billing.balance_state b LEFT JOIN \
         (SELECT ledger_account_id, sum(delta_units) AS units, count(*) AS entries \
          FROM core_billing.ledger_entries GROUP BY ledger_account_id) e \
         ON e.ledger_account_id = b.ledger_account_id \
         WHERE b.balance_units::numeric <> COALESCE(e.units, 0) OR b.revision <> COALESCE(e.entries, 0)",
    ).fetch_one(pool).await.unwrap();
    assert_eq!(
        stale, 0,
        "materialized balance differs from immutable entries"
    );
    let negative: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_billing.balance_state b JOIN core_billing.ledger_accounts a \
         ON a.id = b.ledger_account_id WHERE a.bucket <> 'clearing' AND b.balance_units < 0",
    )
    .fetch_one(pool)
    .await
    .unwrap();
    assert_eq!(negative, 0);
    let conserved: bool = sqlx::query_scalar(
        "SELECT COALESCE(sum(balance_units), 0) = 0 FROM core_billing.balance_state",
    )
    .fetch_one(pool)
    .await
    .unwrap();
    assert!(conserved);
}

#[sqlx::test(migrations = false)]
async fn credit_transfer_refund_reverse_and_original_payer(pool: PgPool) {
    let ledger = fresh(&pool).await;
    fund(&ledger, "deposit", 1, 100).await;
    let transfer = run(
        &ledger,
        "move",
        Action::Transfer {
            from_account_id: 1,
            to_account_id: 2,
            amount_units: units(40),
        },
    )
    .await;
    let original = posted(&transfer);
    let refund = run(
        &ledger,
        "refund",
        Action::Refund {
            journal_id: original,
            amount_units: units(15),
        },
    )
    .await;
    posted(&refund);
    assert_eq!(ledger.balance(1).await.unwrap().available_units, 75);
    assert_eq!(ledger.balance(2).await.unwrap().available_units, 25);
    assert_eq!(ledger.balance(3).await.unwrap().available_units, 0);
    rejected(
        run(
            &ledger,
            "too-much",
            Action::Refund {
                journal_id: original,
                amount_units: units(26),
            },
        )
        .await,
        Rejection::ExceedsRemaining,
    );
    rejected(
        run(
            &ledger,
            "reverse-partial",
            Action::Reverse {
                journal_id: original,
            },
        )
        .await,
        Rejection::AlreadyCompensated,
    );
    let charge = posted(
        &run(
            &ledger,
            "charge",
            Action::Charge {
                account_id: 1,
                amount_units: units(10),
            },
        )
        .await,
    );
    let reversal = run(&ledger, "reverse", Action::Reverse { journal_id: charge }).await;
    posted(&reversal);
    assert_eq!(
        reversal,
        run(&ledger, "reverse", Action::Reverse { journal_id: charge }).await
    );
    rejected(
        run(
            &ledger,
            "reverse-again",
            Action::Reverse { journal_id: charge },
        )
        .await,
        Rejection::AlreadyCompensated,
    );
    rejected(
        run(
            &ledger,
            "refund-reversed",
            Action::Refund {
                journal_id: charge,
                amount_units: units(1),
            },
        )
        .await,
        Rejection::ExceedsRemaining,
    );
    assert_eq!(ledger.balance(1).await.unwrap().available_units, 75);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn concurrent_spending_never_exceeds_available_balance(pool: PgPool) {
    let ledger = fresh(&pool).await;
    fund(&ledger, "deposit", 1, 100).await;
    let barrier = Arc::new(Barrier::new(40));
    let mut tasks = JoinSet::new();
    for index in 0..40 {
        let ledger = ledger.clone();
        let barrier = barrier.clone();
        tasks.spawn(async move {
            barrier.wait().await;
            run(
                &ledger,
                &format!("spend-{index}"),
                Action::Charge {
                    account_id: 1,
                    amount_units: units(7),
                },
            )
            .await
        });
    }
    let mut successes = 0;
    let mut failures = 0;
    while let Some(result) = tasks.join_next().await {
        match result.unwrap() {
            Outcome::Posted { .. } => successes += 1,
            outcome => {
                rejected(outcome, Rejection::InsufficientFunds);
                failures += 1;
            }
        }
    }
    assert_eq!((successes, failures), (14, 26));
    assert_eq!(ledger.balance(1).await.unwrap().available_units, 2);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn duplicate_requests_replay_exact_results_and_reject_changed_parameters(pool: PgPool) {
    let ledger = fresh(&pool).await;
    fund(&ledger, "deposit", 1, 100).await;
    let req = request(
        "duplicate",
        Action::Charge {
            account_id: 1,
            amount_units: units(10),
        },
    );
    let mut tasks = JoinSet::new();
    let barrier = Arc::new(Barrier::new(24));
    for _ in 0..24 {
        let ledger = ledger.clone();
        let req = req.clone();
        let barrier = barrier.clone();
        tasks.spawn(async move {
            barrier.wait().await;
            ledger.execute(&req).await.unwrap()
        });
    }
    let first = tasks.join_next().await.unwrap().unwrap();
    posted(&first);
    while let Some(result) = tasks.join_next().await {
        assert_eq!(result.unwrap(), first);
    }
    let mut changed = req.clone();
    changed.action = Action::Charge {
        account_id: 1,
        amount_units: units(11),
    };
    assert!(matches!(
        ledger.execute(&changed).await,
        Err(Error::IdempotencyConflict)
    ));
    changed = req.clone();
    changed.actor_user_id = Some(8);
    assert!(matches!(
        ledger.execute(&changed).await,
        Err(Error::IdempotencyConflict)
    ));
    changed = req.clone();
    changed.reason = "changed audit evidence".into();
    assert!(matches!(
        ledger.execute(&changed).await,
        Err(Error::IdempotencyConflict)
    ));
    fund(&ledger, "later-deposit", 1, 10).await;
    assert_eq!(
        ledger.execute(&req).await.unwrap(),
        first,
        "receipt retains original snapshots"
    );
    assert_eq!(ledger.balance(1).await.unwrap().available_units, 100);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn rejected_requests_remain_rejected_after_funding_and_payment_ids_are_unique(pool: PgPool) {
    let ledger = fresh(&pool).await;
    let req = request(
        "too-early",
        Action::Charge {
            account_id: 1,
            amount_units: units(5),
        },
    );
    let original = ledger.execute(&req).await.unwrap();
    rejected(original.clone(), Rejection::InsufficientFunds);
    fund(&ledger, "deposit", 1, 10).await;
    assert_eq!(ledger.execute(&req).await.unwrap(), original);
    rejected(
        run(
            &ledger,
            "another-operation-key",
            Action::Credit {
                account_id: 1,
                amount_units: units(10),
                payment_reference: "test-provider:deposit".into(),
            },
        )
        .await,
        Rejection::DuplicatePayment,
    );
    assert_eq!(ledger.balance(1).await.unwrap().available_units, 10);
    let wrong = request(
        "missing",
        Action::Charge {
            account_id: 999,
            amount_units: units(1),
        },
    );
    rejected(ledger.execute(&wrong).await.unwrap(), Rejection::NotFound);
    assert!(matches!(ledger.balance(999).await, Err(Error::NotFound)));
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn opposite_transfers_follow_one_lock_order(pool: PgPool) {
    let ledger = fresh(&pool).await;
    fund(&ledger, "one", 1, 100).await;
    fund(&ledger, "two", 2, 100).await;
    let barrier = Arc::new(Barrier::new(40));
    let mut tasks = JoinSet::new();
    for index in 0..40 {
        let ledger = ledger.clone();
        let barrier = barrier.clone();
        tasks.spawn(async move {
            barrier.wait().await;
            let (from_account_id, to_account_id) = if index % 2 == 0 { (1, 2) } else { (2, 1) };
            posted(
                &run(
                    &ledger,
                    &format!("transfer-{index}"),
                    Action::Transfer {
                        from_account_id,
                        to_account_id,
                        amount_units: units(1),
                    },
                )
                .await,
            )
        });
    }
    tokio::time::timeout(Duration::from_secs(20), async {
        while let Some(result) = tasks.join_next().await {
            result.unwrap();
        }
    })
    .await
    .unwrap();
    assert_eq!(ledger.balance(1).await.unwrap().available_units, 100);
    assert_eq!(ledger.balance(2).await.unwrap().available_units, 100);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn partial_refunds_are_serialized_and_duplicates_do_not_refund_twice(pool: PgPool) {
    let ledger = fresh(&pool).await;
    fund(&ledger, "deposit", 1, 100).await;
    let original = posted(
        &run(
            &ledger,
            "charge",
            Action::Charge {
                account_id: 1,
                amount_units: units(20),
            },
        )
        .await,
    );
    let barrier = Arc::new(Barrier::new(40));
    let mut tasks = JoinSet::new();
    for index in 0..40 {
        let ledger = ledger.clone();
        let barrier = barrier.clone();
        tasks.spawn(async move {
            barrier.wait().await;
            let req = request(
                &format!("refund-{index}"),
                Action::Refund {
                    journal_id: original,
                    amount_units: units(1),
                },
            );
            let outcome = ledger.execute(&req).await.unwrap();
            assert_eq!(ledger.execute(&req).await.unwrap(), outcome);
            outcome
        });
    }
    let mut successes = 0;
    while let Some(result) = tasks.join_next().await {
        match result.unwrap() {
            Outcome::Posted { .. } => successes += 1,
            result => rejected(result, Rejection::ExceedsRemaining),
        }
    }
    assert_eq!(successes, 20);
    assert_eq!(ledger.balance(1).await.unwrap().available_units, 100);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn reservation_capture_release_and_refund_use_original_wallet(pool: PgPool) {
    let ledger = fresh(&pool).await;
    fund(&ledger, "deposit", 2, 100).await;
    let reservation = posted(
        &run(
            &ledger,
            "reserve",
            Action::Reserve {
                account_id: 2,
                amount_units: units(80),
            },
        )
        .await,
    );
    let balance = ledger.balance(2).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (20, 80));
    rejected(
        run(
            &ledger,
            "overspend",
            Action::Charge {
                account_id: 2,
                amount_units: units(21),
            },
        )
        .await,
        Rejection::InsufficientFunds,
    );
    rejected(
        run(
            &ledger,
            "overcapture",
            Action::Capture {
                reservation_journal_id: reservation,
                amount_units: units(81),
            },
        )
        .await,
        Rejection::ExceedsRemaining,
    );
    let capture = posted(
        &run(
            &ledger,
            "capture",
            Action::Capture {
                reservation_journal_id: reservation,
                amount_units: units(30),
            },
        )
        .await,
    );
    let balance = ledger.balance(2).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (70, 0));
    rejected(
        run(
            &ledger,
            "release-after-capture",
            Action::Release {
                reservation_journal_id: reservation,
            },
        )
        .await,
        Rejection::AlreadyFinalized,
    );
    posted(
        &run(
            &ledger,
            "refund-capture",
            Action::Refund {
                journal_id: capture,
                amount_units: units(30),
            },
        )
        .await,
    );
    assert_eq!(ledger.balance(2).await.unwrap().available_units, 100);
    assert_eq!(ledger.balance(1).await.unwrap().available_units, 0);
    let second = posted(
        &run(
            &ledger,
            "reserve-again",
            Action::Reserve {
                account_id: 2,
                amount_units: units(40),
            },
        )
        .await,
    );
    let release = run(
        &ledger,
        "release",
        Action::Release {
            reservation_journal_id: second,
        },
    )
    .await;
    posted(&release);
    assert_eq!(
        release,
        run(
            &ledger,
            "release",
            Action::Release {
                reservation_journal_id: second
            }
        )
        .await
    );
    let third = posted(
        &run(
            &ledger,
            "reserve-zero",
            Action::Reserve {
                account_id: 2,
                amount_units: units(40),
            },
        )
        .await,
    );
    posted(
        &run(
            &ledger,
            "zero-capture",
            Action::Capture {
                reservation_journal_id: third,
                amount_units: units(0),
            },
        )
        .await,
    );
    let balance = ledger.balance(2).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (100, 0));
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn competing_capture_and_release_can_finalize_only_once(pool: PgPool) {
    let ledger = fresh(&pool).await;
    fund(&ledger, "deposit", 1, 100).await;
    let reservation = posted(
        &run(
            &ledger,
            "reserve",
            Action::Reserve {
                account_id: 1,
                amount_units: units(80),
            },
        )
        .await,
    );
    let (capture, release) = tokio::join!(
        run(
            &ledger,
            "capture",
            Action::Capture {
                reservation_journal_id: reservation,
                amount_units: units(30)
            }
        ),
        run(
            &ledger,
            "release",
            Action::Release {
                reservation_journal_id: reservation
            }
        )
    );
    assert_eq!(
        [&capture, &release]
            .iter()
            .filter(|o| matches!(o, Outcome::Posted { .. }))
            .count(),
        1
    );
    for outcome in [capture, release] {
        if matches!(outcome, Outcome::Rejected { .. }) {
            rejected(outcome, Rejection::AlreadyFinalized);
        }
    }
    let balance = ledger.balance(1).await.unwrap();
    assert_eq!(balance.reserved_units, 0);
    assert!([70, 100].contains(&balance.available_units));
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn overflow_and_mid_posting_failure_roll_back_all_legs(pool: PgPool) {
    let ledger = fresh(&pool).await;
    fund(&ledger, "maximum", 1, i64::MAX).await;
    rejected(
        run(
            &ledger,
            "overflow",
            Action::Credit {
                account_id: 1,
                amount_units: units(1),
                payment_reference: "provider:overflow".into(),
            },
        )
        .await,
        Rejection::AmountOverflow,
    );
    assert_eq!(ledger.balance(1).await.unwrap().available_units, i64::MAX);
    // Fail AFTER the balance/entries/journal have been written, before the receipt is sealed.
    sqlx::raw_sql(
        "CREATE FUNCTION core_billing.test_crash() RETURNS trigger LANGUAGE plpgsql AS $$ \
         BEGIN IF NEW.operation_key = 'crash' THEN RAISE EXCEPTION 'injected fault' USING ERRCODE = 'XX000'; \
         END IF; RETURN NEW; END $$; \
         CREATE TRIGGER test_crash BEFORE INSERT ON core_billing.ledger_operations \
         FOR EACH ROW EXECUTE FUNCTION core_billing.test_crash();",
    ).execute(&pool).await.unwrap();
    let req = request(
        "crash",
        Action::Charge {
            account_id: 1,
            amount_units: units(9),
        },
    );
    assert!(matches!(
        ledger.execute(&req).await,
        Err(Error::Database(_))
    ));
    assert_eq!(ledger.balance(1).await.unwrap().available_units, i64::MAX);
    let leaked: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_billing.ledger_journals WHERE operation_key = 'crash'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(leaked, 0);
    sqlx::query("DROP TRIGGER test_crash ON core_billing.ledger_operations")
        .execute(&pool)
        .await
        .unwrap();
    posted(&ledger.execute(&req).await.unwrap());
    assert_eq!(
        ledger.balance(1).await.unwrap().available_units,
        i64::MAX - 9
    );
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn history_cannot_be_edited_truncated_or_extended_after_sealing(pool: PgPool) {
    let ledger = fresh(&pool).await;
    let journal = fund(&ledger, "deposit", 1, 50).await;
    for statement in [
        "UPDATE core_billing.ledger_journals SET reason = 'rewrite'",
        "DELETE FROM core_billing.ledger_journals",
        "UPDATE core_billing.ledger_entries SET delta_units = 999",
        "DELETE FROM core_billing.ledger_entries",
        "UPDATE core_billing.ledger_operations SET result = '{}'::jsonb",
        "DELETE FROM core_billing.ledger_operations",
        "UPDATE core_billing.ledger_accounts SET owner_account_id = 3 WHERE owner_account_id = 1",
        "TRUNCATE core_billing.ledger_entries",
        "TRUNCATE core_billing.ledger_operations CASCADE",
        "TRUNCATE core_billing.balance_state",
    ] {
        let error = sqlx::query(statement).execute(&pool).await.unwrap_err();
        assert_eq!(
            error.as_database_error().unwrap().code().as_deref(),
            Some("55000"),
            "{statement}: {error}"
        );
    }
    let wallet = ledger.open_wallet(3).await.unwrap();
    let error = sqlx::query(
        "INSERT INTO core_billing.ledger_entries(journal_id, ledger_account_id, delta_units) VALUES ($1, $2, 1)",
    ).bind(journal).bind(wallet.wallet_id).execute(&pool).await.unwrap_err();
    assert_eq!(
        error.as_database_error().unwrap().code().as_deref(),
        Some("55000")
    );
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn unbalanced_journal_is_rejected_at_commit(pool: PgPool) {
    let ledger = fresh(&pool).await;
    fund(&ledger, "deposit", 1, 50).await;
    let wallet = ledger.open_wallet(1).await.unwrap();
    let mut tx = pool.begin().await.unwrap();
    let id: i64 = sqlx::query_scalar(
        "INSERT INTO core_billing.ledger_journals(scope, operation_key, kind, payer_id, payee_id, amount_units, reason) \
         VALUES ('test', 'unbalanced', 'charge', $1, (SELECT id FROM core_billing.ledger_accounts WHERE bucket = 'revenue'), 1, 'test') RETURNING id",
    ).bind(wallet.wallet_id).fetch_one(&mut *tx).await.unwrap();
    sqlx::query("INSERT INTO core_billing.ledger_entries(journal_id, ledger_account_id, delta_units) VALUES ($1, $2, -1)")
        .bind(id).bind(wallet.wallet_id).execute(&mut *tx).await.unwrap();
    sqlx::query("INSERT INTO core_billing.ledger_operations(scope, operation_key, request, result) VALUES ('test', 'unbalanced', '{}', '{\"status\":\"posted\"}')")
        .execute(&mut *tx).await.unwrap();
    let error = tx.commit().await.unwrap_err();
    assert_eq!(
        error.as_database_error().unwrap().code().as_deref(),
        Some("23514")
    );
    assert_eq!(ledger.balance(1).await.unwrap().available_units, 50);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn runtime_role_can_only_read_and_call_controlled_functions(pool: PgPool) {
    let ledger = fresh(&pool).await;
    let mut tx = pool.begin().await.unwrap();
    let pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
        .fetch_one(&mut *tx)
        .await
        .unwrap();
    let role = format!("ledger_acceptance_{pid}");
    sqlx::query(&format!("CREATE ROLE {role} NOLOGIN"))
        .execute(&mut *tx)
        .await
        .unwrap();
    let public_execute: bool = sqlx::query_scalar(
        "SELECT has_function_privilege($1, 'core_billing.post_ledger(jsonb)', 'EXECUTE')",
    )
    .bind(&role)
    .fetch_one(&mut *tx)
    .await
    .unwrap();
    assert!(
        !public_execute,
        "PUBLIC must not inherit posting privileges"
    );
    sqlx::raw_sql(&format!(
        "GRANT USAGE ON SCHEMA core_billing TO {role}; \
         GRANT SELECT ON core_billing.ledger_accounts, core_billing.balance_state, core_billing.ledger_operations, \
         core_billing.ledger_journals, core_billing.ledger_entries TO {role}; \
         GRANT EXECUTE ON FUNCTION core_billing.open_ledger_wallet(bigint), core_billing.post_ledger(jsonb) TO {role}; \
         SET LOCAL ROLE {role};",
    )).execute(&mut *tx).await.unwrap();
    for statement in [
        "UPDATE core_billing.balance_state SET balance_units = 999",
        "INSERT INTO core_billing.ledger_accounts(bucket) VALUES ('revenue')",
        "DELETE FROM core_billing.ledger_entries",
        "TRUNCATE core_billing.ledger_operations CASCADE",
        "ALTER TABLE core_billing.ledger_entries DISABLE TRIGGER ALL",
    ] {
        sqlx::query("SAVEPOINT denied_write")
            .execute(&mut *tx)
            .await
            .unwrap();
        let error = sqlx::query(statement).execute(&mut *tx).await.unwrap_err();
        assert_eq!(
            error.as_database_error().unwrap().code().as_deref(),
            Some("42501"),
            "{statement}: {error}"
        );
        sqlx::raw_sql("ROLLBACK TO SAVEPOINT denied_write; RELEASE SAVEPOINT denied_write;")
            .execute(&mut *tx)
            .await
            .unwrap();
    }
    let req = request(
        "runtime-credit",
        Action::Credit {
            account_id: 1,
            amount_units: units(1),
            payment_reference: "provider:runtime".into(),
        },
    );
    let Json(outcome): Json<Outcome> = sqlx::query_scalar("SELECT core_billing.post_ledger($1)")
        .bind(Json(&req))
        .fetch_one(&mut *tx)
        .await
        .unwrap();
    posted(&outcome);
    // Transactional role creation/grants are rolled back too; no global test-role leak.
    tx.rollback().await.unwrap();
    assert_eq!(ledger.balance(1).await.unwrap().available_units, 0);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn lost_connection_before_commit_rolls_back_and_same_key_can_retry(pool: PgPool) {
    let ledger = fresh(&pool).await;
    fund(&ledger, "deposit", 1, 100).await;
    let req = request(
        "before-commit",
        Action::Charge {
            account_id: 1,
            amount_units: units(30),
        },
    );
    let mut tx = pool.begin().await.unwrap();
    let pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
        .fetch_one(&mut *tx)
        .await
        .unwrap();
    let Json(outcome): Json<Outcome> = sqlx::query_scalar("SELECT core_billing.post_ledger($1)")
        .bind(Json(&req))
        .fetch_one(&mut *tx)
        .await
        .unwrap();
    posted(&outcome);
    let killed: bool = sqlx::query_scalar("SELECT pg_terminate_backend($1)")
        .bind(pid)
        .fetch_one(&pool)
        .await
        .unwrap();
    assert!(killed);
    assert!(tx.commit().await.is_err());
    posted(&ledger.execute(&req).await.unwrap());
    assert_eq!(ledger.balance(1).await.unwrap().available_units, 70);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn lost_commit_response_replays_the_committed_result_without_double_charge(pool: PgPool) {
    let ledger = fresh(&pool).await;
    fund(&ledger, "deposit", 1, 100).await;
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
            // PostgreSQL backend frames: tag byte, network-order length, payload.
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
                    // The server finished COMMIT. Drop TCP without forwarding its acknowledgement.
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
    let req = request(
        "lost-reply",
        Action::Charge {
            account_id: 1,
            amount_units: units(30),
        },
    );
    let fault_ledger = Ledger::from_pool(fault_pool.clone());
    let result = tokio::time::timeout(Duration::from_secs(20), fault_ledger.execute(&req))
        .await
        .unwrap();
    assert!(
        matches!(result, Err(Error::Database(_))),
        "caller must not receive success: {result:?}"
    );
    tokio::time::timeout(Duration::from_secs(20), proxy)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(
        ledger.balance(1).await.unwrap().available_units,
        70,
        "COMMIT really happened"
    );
    let replay = ledger.execute(&req).await.unwrap();
    posted(&replay);
    let Json(stored): Json<Outcome> = sqlx::query_scalar(
        "SELECT result FROM core_billing.ledger_operations WHERE scope = $1 AND operation_key = $2",
    )
    .bind(&req.scope)
    .bind(&req.operation_key)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(replay, stored);
    assert_eq!(ledger.balance(1).await.unwrap().available_units, 70);
    fault_pool.close().await;
    balanced(&pool).await;
}
