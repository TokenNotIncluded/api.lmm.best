//! Real PostgreSQL payment acceptance with synthetic endpoint signatures only.
//! SQLx creates a new owned database per test; never point DATABASE_URL at live data.
#![cfg(unix)]
use hmac::{Hmac, Mac};
use lmm_core::{
    billing::{
        Credits,
        ledger::{self, Action, Ledger, Outcome},
    },
    identity::IdentityStore,
    internal_rpc::pb,
    payments::{Channel, Error, PaymentsStore},
};
use serde_json::{Value, json};
use sha2::Sha256;
use sqlx::PgPool;
use std::time::{SystemTime, UNIX_EPOCH};

const SECRET: &str = "whsec_local_synthetic_endpoint_only";
const MERCHANT: &str = "acct_local_synthetic";
struct Fixture {
    store: PaymentsStore,
    ledger: Ledger,
    identity: IdentityStore,
    secret: String,
    account: i64,
}
async fn fresh(pool: &PgPool) -> Fixture {
    let identity = IdentityStore::from_pool(pool.clone());
    identity.init_database().await.unwrap();
    let mut tx = pool.begin().await.unwrap();
    sqlx::raw_sql(ledger::SCHEMA)
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::raw_sql(lmm_core::payments::SCHEMA)
        .execute(&mut *tx)
        .await
        .unwrap();
    tx.commit().await.unwrap();
    let issued = identity.bootstrap_user(101, 0).await.unwrap();
    let account = identity
        .authorize(&issued.secret)
        .await
        .unwrap()
        .owner_account_id;
    Fixture {
        store: PaymentsStore::new(
            pool.clone(),
            vec![Channel::stripe_test(
                1,
                MERCHANT.into(),
                SECRET.into(),
                100_000,
            )],
        )
        .unwrap(),
        ledger: Ledger::from_pool(pool.clone()),
        identity,
        secret: issued.secret,
        account,
    }
}
fn mutation(key: &str) -> Option<pb::MutationContext> {
    Some(pb::MutationContext {
        idempotency_key: key.into(),
        expected_version: None,
    })
}
fn prepare_request(f: &Fixture, key: &str, amount: i64) -> pb::PrepareTopupRequest {
    pb::PrepareTopupRequest {
        mutation: mutation(key),
        account_id: f.account,
        payment_channel_id: 1,
        currency: "USD".into(),
        amount_minor: amount,
    }
}
async fn prepare(f: &Fixture, key: &str, amount: i64) -> pb::PaymentIntent {
    f.store
        .prepare(&f.secret, prepare_request(f, key, amount))
        .await
        .unwrap()
}
fn event(
    intent: &pb::PaymentIntent,
    id: &str,
    refund: Option<(&str, i64, &str, Option<&str>)>,
) -> Value {
    let mut metadata =
        json!({"lmm_core_intent_id":intent.intent_id.to_string(),"lmm_merchant":MERCHANT});
    let object = if let Some((refund_id, amount, status, hold)) = refund {
        if let Some(hold) = hold {
            metadata["lmm_hold_receipt_id"] = json!(hold);
        }
        json!({"id":refund_id,"object":"refund","status":status,"amount":amount,"currency":"usd","payment_intent":format!("pi_{}",intent.intent_id),"metadata":metadata})
    } else {
        json!({"id":format!("pi_{}",intent.intent_id),"object":"payment_intent","status":"succeeded","amount":intent.amount_minor,"amount_received":intent.amount_minor,"currency":"usd","livemode":false,"metadata":metadata})
    };
    json!({"id":id,"type":if refund.is_some(){"refund.updated"}else{"payment_intent.succeeded"},"livemode":false,"data":{"object":object}})
}
fn proof(value: Value) -> pb::ProviderEvidence {
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_secs();
    let payload = serde_json::to_vec(&value).unwrap();
    let mut mac = Hmac::<Sha256>::new_from_slice(SECRET.as_bytes()).unwrap();
    mac.update(format!("{now}.").as_bytes());
    mac.update(&payload);
    let mut signature = format!("t={now},v1=");
    use std::fmt::Write;
    for byte in mac.finalize().into_bytes() {
        write!(&mut signature, "{byte:02x}").unwrap();
    }
    let object = &value["data"]["object"];
    let transaction = if object["object"] == "refund" {
        object["payment_intent"].as_str().unwrap()
    } else {
        object["id"].as_str().unwrap()
    };
    pb::ProviderEvidence {
        payment: Some(pb::ExternalPaymentReference {
            provider: "stripe".into(),
            merchant: MERCHANT.into(),
            environment: "test".into(),
            transaction: transaction.into(),
        }),
        signed_payload: payload,
        signature: signature.into_bytes(),
        provider_event_id: value["id"].as_str().unwrap().into(),
    }
}
fn credit_request(
    intent: &pb::PaymentIntent,
    key: &str,
    evidence: pb::ProviderEvidence,
) -> pb::CreditTopupRequest {
    pb::CreditTopupRequest {
        mutation: mutation(key),
        intent_id: intent.intent_id,
        evidence: Some(evidence),
    }
}
async fn credit(f: &Fixture, intent: &pb::PaymentIntent, key: &str) -> pb::PaymentReceipt {
    f.store
        .credit(credit_request(
            intent,
            key,
            proof(event(
                intent,
                &format!("evt_credit_{}", intent.intent_id),
                None,
            )),
        ))
        .await
        .unwrap()
}
async fn balance(f: &Fixture, available: i64, reserved: i64) {
    let actual = f.ledger.balance(f.account).await.unwrap();
    assert_eq!(
        (actual.available_units, actual.reserved_units),
        (available, reserved)
    );
}
async fn journals(pool: &PgPool) -> i64 {
    sqlx::query_scalar("SELECT count(*) FROM core_billing.ledger_journals")
        .fetch_one(pool)
        .await
        .unwrap()
}

#[sqlx::test]
async fn prepare_freezes_policy_credit_receipt_and_replay_are_atomic(pool: PgPool) {
    let f = fresh(&pool).await;
    let intent = prepare(&f, "prepare_atomic_0001", 100).await;
    assert_eq!(
        (intent.credit_units, intent.credit_unit.as_str()),
        (500_000, "credit_500k_usd")
    );
    assert_eq!(prepare(&f, "prepare_atomic_0001", 100).await, intent);
    let prepared = f
        .store
        .receipt(
            Some(&f.secret),
            pb::GetPaymentReceiptRequest {
                intent_id: intent.intent_id,
                idempotency_key: intent.idempotency_key.clone(),
            },
        )
        .await
        .unwrap();
    assert_eq!((prepared.state, prepared.journal_id), (7, 0));
    let evidence = proof(event(&intent, "evt_atomic_1", None));
    let request = credit_request(&intent, "credit_atomic_0001", evidence);
    let receipt = f.store.credit(request.clone()).await.unwrap();
    assert_eq!(receipt.state, 1);
    assert!(receipt.journal_id > 0);
    assert_eq!(f.store.credit(request).await.unwrap(), receipt);
    balance(&f, 500_000, 0).await;
    assert_eq!(journals(&pool).await, 1);
    let net: i64 =
        sqlx::query_scalar("SELECT sum(delta_units)::bigint FROM core_billing.ledger_entries")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(net, 0);
}

#[sqlx::test]
async fn current_authority_is_checked_even_on_prepare_and_hold_replay(pool: PgPool) {
    let f = fresh(&pool).await;
    let intent = prepare(&f, "prepare_authority_01", 100).await;
    let stranger = f.identity.bootstrap_user(202, 0).await.unwrap();
    assert_eq!(
        f.store
            .prepare(
                &stranger.secret,
                prepare_request(&f, "prepare_stranger_01", 100)
            )
            .await
            .unwrap_err(),
        Error::Denied
    );
    credit(&f, &intent, "credit_authority_01").await;
    let request = pb::HoldRefundRequest {
        mutation: mutation("hold_authority_0001"),
        intent_id: intent.intent_id,
        refund_amount_minor: 20,
    };
    f.store.hold(&f.secret, request.clone()).await.unwrap();
    sqlx::query(
        "UPDATE core_identity.credentials SET revoked_at=clock_timestamp() WHERE user_id=101",
    )
    .execute(&pool)
    .await
    .unwrap();
    assert_eq!(
        f.store
            .prepare(&f.secret, prepare_request(&f, "prepare_authority_01", 100))
            .await
            .unwrap_err(),
        Error::Denied
    );
    assert_eq!(
        f.store.hold(&f.secret, request).await.unwrap_err(),
        Error::Denied
    );
    balance(&f, 400_000, 100_000).await;
}

#[sqlx::test]
async fn independently_rejects_fake_signature_and_signed_wrong_bindings(pool: PgPool) {
    let f = fresh(&pool).await;
    let intent = prepare(&f, "prepare_proofs_0001", 100).await;
    for (index, field) in [
        "amount",
        "currency",
        "merchant",
        "environment",
        "intent",
        "reference",
        "signature",
    ]
    .iter()
    .enumerate()
    {
        let mut value = event(&intent, &format!("evt_reject_{index}"), None);
        match *field {
            "amount" => value["data"]["object"]["amount"] = json!(101),
            "currency" => value["data"]["object"]["currency"] = json!("eur"),
            "merchant" => value["data"]["object"]["metadata"]["lmm_merchant"] = json!("acct_wrong"),
            "environment" => value["livemode"] = json!(true),
            "intent" => value["data"]["object"]["metadata"]["lmm_core_intent_id"] = json!("9999"),
            _ => {}
        }
        let mut evidence = proof(value);
        if *field == "reference" {
            evidence.payment.as_mut().unwrap().merchant = "acct_wrong".into();
        }
        if *field == "signature" {
            evidence.signature[20] ^= 1;
        }
        assert!(
            f.store
                .credit(credit_request(
                    &intent,
                    &format!("credit_reject_{index:04}"),
                    evidence
                ))
                .await
                .is_err(),
            "accepted {field}"
        );
        assert_eq!(journals(&pool).await, 0);
    }
    credit(&f, &intent, "credit_proof_good_01").await;
    balance(&f, 500_000, 0).await;
}

#[sqlx::test]
async fn immutable_keys_events_and_provider_transaction_prevent_double_credit(pool: PgPool) {
    let f = fresh(&pool).await;
    let intent = prepare(&f, "prepare_idem_000001", 100).await;
    assert_eq!(
        f.store
            .prepare(&f.secret, prepare_request(&f, "prepare_idem_000001", 101))
            .await
            .unwrap_err(),
        Error::Conflict
    );
    let value = event(&intent, "evt_idem_1", None);
    let original = credit_request(&intent, "credit_idem_000001", proof(value.clone()));
    let receipt = f.store.credit(original).await.unwrap();
    let mut changed = value.clone();
    changed["id"] = json!("evt_idem_other");
    assert_eq!(
        f.store
            .credit(credit_request(
                &intent,
                "credit_idem_000001",
                proof(changed)
            ))
            .await
            .unwrap_err(),
        Error::Conflict
    );
    assert_eq!(
        f.store
            .credit(credit_request(&intent, "credit_idem_other01", proof(value)))
            .await
            .unwrap_err(),
        Error::Conflict
    );
    assert_eq!(receipt.state, 1);
    balance(&f, 500_000, 0).await;
    assert_eq!(journals(&pool).await, 1);
}

#[sqlx::test]
async fn concurrent_credit_replays_post_once_with_serializable_retries(pool: PgPool) {
    let f = fresh(&pool).await;
    let intent = prepare(&f, "prepare_concurrent01", 100).await;
    let request = credit_request(
        &intent,
        "credit_concurrent_01",
        proof(event(&intent, "evt_concurrent1", None)),
    );
    let mut tasks = tokio::task::JoinSet::new();
    for _ in 0..8 {
        let store = f.store.clone();
        let request = request.clone();
        tasks.spawn(async move {
            for _ in 0..20 {
                match store.credit(request.clone()).await {
                    Ok(receipt) => return receipt,
                    Err(Error::Conflict | Error::Unavailable) => tokio::task::yield_now().await,
                    Err(error) => panic!("unexpected {error:?}"),
                }
            }
            panic!("serializable retry budget exhausted");
        });
    }
    let mut first = None;
    while let Some(result) = tasks.join_next().await {
        let receipt = result.unwrap();
        if let Some(old) = &first {
            assert_eq!(old, &receipt);
        } else {
            first = Some(receipt);
        }
    }
    balance(&f, 500_000, 0).await;
    assert_eq!(journals(&pool).await, 1);
}

#[sqlx::test]
async fn refunds_hold_original_wallet_commit_partial_and_never_replay(pool: PgPool) {
    let f = fresh(&pool).await;
    let intent = prepare(&f, "prepare_refund_0001", 100).await;
    credit(&f, &intent, "credit_refund_0001").await;
    let hold = f
        .store
        .hold(
            &f.secret,
            pb::HoldRefundRequest {
                mutation: mutation("hold_refund_000001"),
                intent_id: intent.intent_id,
                refund_amount_minor: 30,
            },
        )
        .await
        .unwrap();
    balance(&f, 350_000, 150_000).await;
    let evidence = proof(event(
        &intent,
        "evt_refund_commit1",
        Some(("re_partial1", 30, "succeeded", Some(&hold.receipt_id))),
    ));
    let request = pb::ResolveRefundRequest {
        mutation: mutation("commit_refund_0001"),
        intent_id: intent.intent_id,
        hold_receipt_id: hold.receipt_id.clone(),
        evidence: Some(evidence),
    };
    let receipt = f.store.resolve(request.clone(), true).await.unwrap();
    assert_eq!(receipt.state, 3);
    assert_eq!(f.store.resolve(request, true).await.unwrap(), receipt);
    balance(&f, 350_000, 0).await;
    let changed = pb::ExternalRefundRequest {
        mutation: mutation("external_same_refund"),
        intent_id: intent.intent_id,
        evidence: Some(proof(event(
            &intent,
            "evt_refund_duplicate",
            Some(("re_partial1", 30, "succeeded", None)),
        ))),
    };
    assert_eq!(
        f.store.external_refund(changed).await.unwrap_err(),
        Error::Conflict
    );
    balance(&f, 350_000, 0).await;
    assert_eq!(journals(&pool).await, 4);
}

#[sqlx::test]
async fn unknown_or_other_failed_attempt_never_releases_hold(pool: PgPool) {
    let f = fresh(&pool).await;
    let intent = prepare(&f, "prepare_unknown_001", 100).await;
    credit(&f, &intent, "credit_unknown_001").await;
    let hold = f
        .store
        .hold(
            &f.secret,
            pb::HoldRefundRequest {
                mutation: mutation("hold_unknown_0001"),
                intent_id: intent.intent_id,
                refund_amount_minor: 40,
            },
        )
        .await
        .unwrap();
    let no_proof = pb::ResolveRefundRequest {
        mutation: mutation("release_unknown_001"),
        intent_id: intent.intent_id,
        hold_receipt_id: hold.receipt_id.clone(),
        evidence: None,
    };
    assert_eq!(
        f.store.resolve(no_proof, false).await.unwrap_err(),
        Error::Unavailable
    );
    let attempt_a = pb::ResolveRefundRequest {
        mutation: mutation("release_attempt_a01"),
        intent_id: intent.intent_id,
        hold_receipt_id: hold.receipt_id.clone(),
        evidence: Some(proof(event(
            &intent,
            "evt_failed_a",
            Some(("re_attempt_a", 40, "failed", Some(&hold.receipt_id))),
        ))),
    };
    assert_eq!(
        f.store.resolve(attempt_a, false).await.unwrap_err(),
        Error::Unavailable
    );
    balance(&f, 300_000, 200_000).await;
    let attempt_b = pb::ResolveRefundRequest {
        mutation: mutation("commit_attempt_b001"),
        intent_id: intent.intent_id,
        hold_receipt_id: hold.receipt_id.clone(),
        evidence: Some(proof(event(
            &intent,
            "evt_success_b",
            Some(("re_attempt_b", 40, "succeeded", Some(&hold.receipt_id))),
        ))),
    };
    assert_eq!(f.store.resolve(attempt_b, true).await.unwrap().state, 3);
    balance(&f, 300_000, 0).await;
}

#[sqlx::test]
async fn precredit_refund_is_fenced_without_fake_journal(pool: PgPool) {
    let f = fresh(&pool).await;
    let intent = prepare(&f, "prepare_precredit01", 100).await;
    let request = pb::ExternalRefundRequest {
        mutation: mutation("external_precredit01"),
        intent_id: intent.intent_id,
        evidence: Some(proof(event(
            &intent,
            "evt_precredit_refund",
            Some(("re_precredit", 100, "succeeded", None)),
        ))),
    };
    let receipt = f.store.external_refund(request.clone()).await.unwrap();
    assert_eq!((receipt.state, receipt.journal_id), (5, 0));
    assert_eq!(f.store.external_refund(request).await.unwrap(), receipt);
    let later = credit(&f, &intent, "credit_after_fence01").await;
    assert_eq!(
        (later.state, later.journal_id, later.rejection_code.as_str()),
        (6, 0, "refund_fence")
    );
    assert_eq!(journals(&pool).await, 0);
}

#[sqlx::test]
async fn partial_precredit_refund_is_explicitly_blocked_for_net_reconciliation(pool: PgPool) {
    let f = fresh(&pool).await;
    let intent = prepare(&f, "prepare_partial_fence", 100).await;
    let request = pb::ExternalRefundRequest {
        mutation: mutation("external_partial_fence"),
        intent_id: intent.intent_id,
        evidence: Some(proof(event(
            &intent,
            "evt_partial_before_credit",
            Some(("re_partial_before_credit", 30, "succeeded", None)),
        ))),
    };
    assert_eq!(
        f.store.external_refund(request).await.unwrap_err(),
        Error::Conflict
    );
    let fact:(String,i64,bool)=sqlx::query_as("SELECT b.reason,b.amount_minor,i.refund_fenced FROM core_payments.blocked_refunds b JOIN core_payments.intents i ON i.id=b.intent_id").fetch_one(&pool).await.unwrap();
    assert_eq!(
        fact,
        ("net_credit_reconciliation_required".into(), 30, true)
    );
    let receipts: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_payments.receipts WHERE method='external_refund'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(receipts, 0);
    let later = credit(&f, &intent, "credit_after_partial_fence").await;
    assert_eq!(later.rejection_code, "refund_fence");
    assert_eq!(journals(&pool).await, 0);
}

#[sqlx::test]
async fn external_refund_debt_is_preserved_but_not_reported_as_success(pool: PgPool) {
    let f = fresh(&pool).await;
    let intent = prepare(&f, "prepare_debt_000001", 100).await;
    credit(&f, &intent, "credit_debt_000001").await;
    let outcome = f
        .ledger
        .execute(&ledger::Request {
            scope: "synthetic-usage".into(),
            operation_key: "consume_all".into(),
            actor_user_id: Some(101),
            reason: "synthetic model usage".into(),
            action: Action::Charge {
                account_id: f.account,
                amount_units: Credits::try_from(500_000).unwrap(),
            },
        })
        .await
        .unwrap();
    assert!(matches!(outcome, Outcome::Posted { .. }));
    let request = pb::ExternalRefundRequest {
        mutation: mutation("external_debt_00001"),
        intent_id: intent.intent_id,
        evidence: Some(proof(event(
            &intent,
            "evt_debt_refund",
            Some(("re_debt", 100, "succeeded", None)),
        ))),
    };
    assert_eq!(
        f.store.external_refund(request.clone()).await.unwrap_err(),
        Error::Conflict
    );
    let fact:(String,i64,bool)=sqlx::query_as("SELECT b.reason,b.amount_minor,i.refund_fenced FROM core_payments.blocked_refunds b JOIN core_payments.intents i ON i.id=b.intent_id").fetch_one(&pool).await.unwrap();
    assert_eq!(fact, ("refund_debt_recovery_required".into(), 100, true));
    assert_eq!(
        f.store.external_refund(request).await.unwrap_err(),
        Error::Conflict
    );
    let receipts: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_payments.receipts WHERE method='external_refund'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(receipts, 0);
    balance(&f, 0, 0).await;
    assert_eq!(journals(&pool).await, 2);
}

#[sqlx::test]
async fn metadata_failure_rolls_back_credit_and_money_history(pool: PgPool) {
    let f = fresh(&pool).await;
    let intent = prepare(&f, "prepare_rollback_001", 100).await;
    sqlx::raw_sql("CREATE FUNCTION core_payments.test_receipt_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.method='credit' THEN RAISE EXCEPTION 'synthetic metadata failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER test_receipt_failure BEFORE INSERT ON core_payments.receipts FOR EACH ROW EXECUTE FUNCTION core_payments.test_receipt_failure();").execute(&pool).await.unwrap();
    let request = credit_request(
        &intent,
        "credit_rollback_001",
        proof(event(&intent, "evt_rollback", None)),
    );
    assert_eq!(
        f.store.credit(request.clone()).await.unwrap_err(),
        Error::Storage
    );
    assert_eq!(journals(&pool).await, 0);
    let events: i64 = sqlx::query_scalar("SELECT count(*) FROM core_payments.provider_events")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(events, 0);
    sqlx::raw_sql("DROP TRIGGER test_receipt_failure ON core_payments.receipts; DROP FUNCTION core_payments.test_receipt_failure();").execute(&pool).await.unwrap();
    assert_eq!(f.store.credit(request).await.unwrap().state, 1);
    balance(&f, 500_000, 0).await;
}
