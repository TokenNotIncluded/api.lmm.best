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
    store: IngressStore,
    ledger: Ledger,
    identity: IdentityStore,
    secret: String,
    account: i64,
}
#[derive(Clone)]
struct IngressStore(PaymentsStore);
impl std::ops::Deref for IngressStore {
    type Target = PaymentsStore;
    fn deref(&self) -> &Self::Target {
        &self.0
    }
}
impl IngressStore {
    async fn receive(
        &self,
        intent_id: i64,
        evidence: &mut Option<pb::ProviderEvidence>,
    ) -> Result<(), Error> {
        let evidence = evidence.as_mut().ok_or(Error::Invalid)?;
        if evidence.accepted_proof_id.is_empty() {
            let receipt = self
                .0
                .accept_proof(pb::AcceptProviderProofRequest {
                    intent_id,
                    evidence: Some(evidence.clone()),
                })
                .await?;
            evidence.accepted_proof_id = receipt.proof_id;
        }
        Ok(())
    }
    async fn credit(
        &self,
        mut request: pb::CreditTopupRequest,
    ) -> Result<pb::PaymentReceipt, Error> {
        self.receive(request.intent_id, &mut request.evidence)
            .await?;
        self.0.credit(request).await
    }
    async fn resolve(
        &self,
        mut request: pb::ResolveRefundRequest,
        commit: bool,
    ) -> Result<pb::PaymentReceipt, Error> {
        if commit {
            self.receive(request.intent_id, &mut request.evidence)
                .await?;
        }
        self.0.resolve(request, commit).await
    }
    async fn external_refund(
        &self,
        mut request: pb::ExternalRefundRequest,
    ) -> Result<pb::PaymentReceipt, Error> {
        self.receive(request.intent_id, &mut request.evidence)
            .await?;
        self.0.external_refund(request).await
    }
}
async fn fresh(pool: &PgPool) -> Fixture {
    let identity = IdentityStore::from_pool(pool.clone());
    identity.init_database().await.unwrap();
    // Payment-intent admission reads the required Core abuse policy. Install it
    // explicitly with this test's synthetic owner before exercising that path.
    sqlx::raw_sql(lmm_core::abuse::SCHEMA)
        .execute(pool)
        .await
        .unwrap();
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
        store: IngressStore(
            PaymentsStore::new(
                pool.clone(),
                vec![Channel::stripe_test(
                    1,
                    MERCHANT.into(),
                    SECRET.into(),
                    100_000,
                )],
            )
            .unwrap(),
        ),
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
        accepted_proof_id: String::new(),
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

#[sqlx::test]
async fn ingress_is_immutable_nonfinancial_and_requires_scoped_reconciliation(pool: PgPool) {
    let f = fresh(&pool).await;
    let intent = prepare(&f, "prepare_durable_ingress", 100).await;
    let mut evidence = proof(event(&intent, "evt_durable_ingress", None));
    let receipt = f
        .store
        .accept_proof(pb::AcceptProviderProofRequest {
            intent_id: intent.intent_id,
            evidence: Some(evidence.clone()),
        })
        .await
        .unwrap();
    assert_eq!(receipt.intent_id, intent.intent_id);
    assert_eq!(
        receipt.payload_sha256,
        <Sha256 as sha2::Digest>::digest(&evidence.signed_payload).to_vec()
    );
    assert_eq!(journals(&pool).await, 0);
    assert_eq!(
        f.store
            .0
            .credit(credit_request(
                &intent,
                "credit_without_proof",
                evidence.clone()
            ))
            .await
            .unwrap_err(),
        Error::Denied
    );
    let lookup = pb::GetProviderProofRequest {
        intent_id: intent.intent_id,
        provider_event_id: evidence.provider_event_id.clone(),
        payload_sha256: receipt.payload_sha256.clone(),
    };
    assert_eq!(f.store.get_proof(lookup.clone()).await.unwrap(), receipt);
    let mut wrong = lookup.clone();
    wrong.payload_sha256[0] ^= 1;
    assert_eq!(f.store.get_proof(wrong).await.unwrap_err(), Error::Conflict);
    let other = prepare(&f, "prepare_other_durable", 100).await;
    let mut wrong = lookup;
    wrong.intent_id = other.intent_id;
    assert_eq!(f.store.get_proof(wrong).await.unwrap_err(), Error::NotFound);
    assert!(
        sqlx::query("UPDATE core_payments.accepted_proofs SET event_id='evt_changed'")
            .execute(&pool)
            .await
            .is_err()
    );
    assert!(
        sqlx::query("DELETE FROM core_payments.accepted_proofs")
            .execute(&pool)
            .await
            .is_err()
    );
    evidence.accepted_proof_id = receipt.proof_id;
    // Accepted facts cannot be changed through the reference or raw bytes.
    let mut wrong = evidence.clone();
    wrong.signed_payload.push(b' ');
    assert_eq!(
        f.store
            .0
            .credit(credit_request(&intent, "credit_changed_bytes", wrong))
            .await
            .unwrap_err(),
        Error::Conflict
    );
    let mut wrong = evidence.clone();
    wrong.payment.as_mut().unwrap().merchant = "acct_other".into();
    assert_eq!(
        f.store
            .0
            .credit(credit_request(&intent, "credit_changed_merchant", wrong))
            .await
            .unwrap_err(),
        Error::Conflict
    );
    let request = credit_request(&intent, "credit_durable_ingress", evidence);
    assert_eq!(f.store.0.credit(request.clone()).await.unwrap().state, 1);
    let mut forged = request;
    forged.evidence.as_mut().unwrap().accepted_proof_id = format!("proof_{}", "0".repeat(64));
    assert_eq!(f.store.0.credit(forged).await.unwrap_err(), Error::Denied);
    assert_eq!(journals(&pool).await, 1);
    balance(&f, 500_000, 0).await;
}

#[sqlx::test]
async fn accepted_proof_survives_secret_rotation_but_unknown_stale_input_is_denied(pool: PgPool) {
    let f = fresh(&pool).await;
    let intent = prepare(&f, "prepare_rotation_durable", 100).await;
    let mut evidence = proof(event(&intent, "evt_rotation_durable", None));
    let receipt = f
        .store
        .accept_proof(pb::AcceptProviderProofRequest {
            intent_id: intent.intent_id,
            evidence: Some(evidence.clone()),
        })
        .await
        .unwrap();
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_secs();
    let stale = now - 301;
    let mut mac = Hmac::<Sha256>::new_from_slice(SECRET.as_bytes()).unwrap();
    mac.update(format!("{stale}.").as_bytes());
    mac.update(&evidence.signed_payload);
    use std::fmt::Write;
    let mut header = format!("t={stale},v1=");
    for byte in mac.finalize().into_bytes() {
        write!(&mut header, "{byte:02x}").unwrap();
    }
    evidence.signature = header.into_bytes();
    assert_eq!(
        f.store
            .accept_proof(pb::AcceptProviderProofRequest {
                intent_id: intent.intent_id,
                evidence: Some(evidence.clone()),
            })
            .await
            .unwrap_err(),
        Error::Denied
    );
    // A genuinely unknown event, signed correctly by the currently configured
    // endpoint key but outside freshness, cannot obtain a durable receipt.
    let mut unknown = proof(event(&intent, "evt_unknown_stale_current", None));
    let mut mac = Hmac::<Sha256>::new_from_slice(SECRET.as_bytes()).unwrap();
    mac.update(format!("{stale}.").as_bytes());
    mac.update(&unknown.signed_payload);
    let mut header = format!("t={stale},v1=");
    for byte in mac.finalize().into_bytes() {
        write!(&mut header, "{byte:02x}").unwrap();
    }
    unknown.signature = header.into_bytes();
    assert_eq!(
        f.store
            .accept_proof(pb::AcceptProviderProofRequest {
                intent_id: intent.intent_id,
                evidence: Some(unknown.clone()),
            })
            .await
            .unwrap_err(),
        Error::Denied
    );
    assert_eq!(
        f.store
            .get_proof(pb::GetProviderProofRequest {
                intent_id: intent.intent_id,
                provider_event_id: unknown.provider_event_id,
                payload_sha256: <Sha256 as sha2::Digest>::digest(&unknown.signed_payload).to_vec(),
            })
            .await
            .unwrap_err(),
        Error::NotFound
    );
    // A new store instance has no old secret. The immutable accepted facts,
    // rather than the stale signature or in-memory state, authorize settlement.
    let restarted = PaymentsStore::new(
        pool.clone(),
        vec![Channel::stripe_test(
            1,
            MERCHANT.into(),
            "whsec_local_rotated_synthetic_only".into(),
            100_000,
        )],
    )
    .unwrap();
    evidence.accepted_proof_id = receipt.proof_id;
    let request = credit_request(&intent, "credit_rotation_durable", evidence);
    let applied = restarted.credit(request.clone()).await.unwrap();
    assert_eq!(restarted.credit(request).await.unwrap(), applied);
    assert_eq!(journals(&pool).await, 1);
    balance(&f, 500_000, 0).await;
}

#[sqlx::test]
async fn same_event_cannot_change_ignored_bytes_and_ingress_failure_rolls_back(pool: PgPool) {
    let f = fresh(&pool).await;
    let intent = prepare(&f, "prepare_proof_conflict", 100).await;
    let value = event(&intent, "evt_proof_conflict", None);
    let original = proof(value.clone());
    let receipt = f
        .store
        .accept_proof(pb::AcceptProviderProofRequest {
            intent_id: intent.intent_id,
            evidence: Some(original),
        })
        .await
        .unwrap();
    let mut changed = value;
    changed["ignored_provider_field"] = json!(true);
    assert_eq!(
        f.store
            .accept_proof(pb::AcceptProviderProofRequest {
                intent_id: intent.intent_id,
                evidence: Some(proof(changed)),
            })
            .await
            .unwrap_err(),
        Error::Conflict
    );
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_payments.accepted_proofs")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 1);
    assert!(!receipt.proof_id.is_empty());
    sqlx::raw_sql("CREATE FUNCTION core_payments.test_proof_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic proof persistence failure'; END $$; CREATE TRIGGER test_proof_failure BEFORE INSERT ON core_payments.accepted_proofs FOR EACH ROW EXECUTE FUNCTION core_payments.test_proof_failure();").execute(&pool).await.unwrap();
    let failed = proof(event(&intent, "evt_proof_insert_failure", None));
    assert_eq!(
        f.store
            .accept_proof(pb::AcceptProviderProofRequest {
                intent_id: intent.intent_id,
                evidence: Some(failed.clone()),
            })
            .await
            .unwrap_err(),
        Error::Storage
    );
    assert_eq!(
        f.store
            .get_proof(pb::GetProviderProofRequest {
                intent_id: intent.intent_id,
                provider_event_id: failed.provider_event_id,
                payload_sha256: <Sha256 as sha2::Digest>::digest(&failed.signed_payload).to_vec(),
            })
            .await
            .unwrap_err(),
        Error::NotFound
    );
    assert_eq!(journals(&pool).await, 0);
}

/// Explicit separate command: requires isolated Go payment DB owner/runtime
/// files. The 302-second wait uses real wall time, with no production clock hook.
#[sqlx::test]
#[ignore = "requires dedicated Go DB files and 302 seconds of real signature expiry"]
async fn real_go_http_uds_proof_survives_expiry_and_service_restart(pool: PgPool) {
    use lmm_core::{
        internal_rpc::RpcServer,
        payments::rpc::{PaymentsRpc, Permissions},
    };
    use std::{
        fs,
        os::unix::fs::PermissionsExt,
        path::PathBuf,
        process::{Command, Stdio},
        time::Duration,
    };
    use tokio::sync::watch;
    let owner_file = std::env::var("LMM_PAYMENT_JOINT_GO_OWNER_URL_FILE")
        .expect("isolated Go owner file required");
    let runtime_file = std::env::var("LMM_PAYMENT_JOINT_GO_RUNTIME_URL_FILE")
        .expect("isolated Go runtime file required");
    let go_owner = fs::read_to_string(owner_file).unwrap();
    let go_pool = sqlx::postgres::PgPoolOptions::new()
        .max_connections(1)
        .connect(go_owner.trim())
        .await
        .unwrap();
    let go_name: String = sqlx::query_scalar("SELECT current_database()")
        .fetch_one(&go_pool)
        .await
        .unwrap();
    assert!(
        go_name.starts_with("lmm_payments_proof_"),
        "refusing non-owned Go DB"
    );
    let tables: i64 =
        sqlx::query_scalar("SELECT count(*) FROM pg_tables WHERE schemaname='public'")
            .fetch_one(&go_pool)
            .await
            .unwrap();
    assert_eq!(
        tables, 0,
        "Go fixture must start empty; never clear an existing DB"
    );
    sqlx::raw_sql(include_str!(
        "../../extensions/internal/modules/payments/schema.sql"
    ))
    .execute(&go_pool)
    .await
    .unwrap();
    // Default table privileges are narrowed before any Go service connects:
    // only SELECT on the storage guard, SIU on module business tables.
    let grants_file = std::env::var("LMM_PAYMENT_JOINT_GO_POST_INIT_SQL_FILE")
        .expect("isolated post-init grants file required");
    let grants_metadata = fs::metadata(&grants_file).unwrap();
    assert!(grants_metadata.is_file() && grants_metadata.permissions().mode() & 0o077 == 0);
    let grants = fs::read_to_string(grants_file).unwrap();
    assert!(grants.len() < 16384);
    sqlx::raw_sql(&grants).execute(&go_pool).await.unwrap();
    go_pool.close().await;
    let f = fresh(&pool).await;
    let key = "prepare_real_go_durable";
    let intent = prepare(&f, key, 100).await;
    let mut value = event(&intent, "evt_real_go_durable", None);
    value["data"]["object"]["metadata"]["lmm_order_id"] =
        json!(format!("top_{}", intent.intent_id));
    let evidence = proof(value.clone());
    value["id"] = json!("evt_real_go_ack_lost");
    let lost_evidence = proof(value);
    let accepted_timestamp = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_secs();
    let temp = PathBuf::from(format!(
        "/tmp/lmm-payproof-joint-{}-{}",
        std::process::id(),
        accepted_timestamp
    ));
    fs::create_dir(&temp).unwrap();
    fs::set_permissions(&temp, fs::Permissions::from_mode(0o700)).unwrap();
    let socket = temp.join("core.sock");
    let fixture = temp.join("fixture.private.json");
    let token = "payment_fixture_service_token_0123456789abcdef";
    let config = json!({
        "socket":socket,"token":token,"user":f.secret,"account":f.account,
        "channel":{"id":1,"provider":"stripe","merchant":MERCHANT,"environment":"test","currency":"USD","enabled":true},
        "intent_id":intent.intent_id,"prepare_key":key,"currency":"USD","amount_minor":100,
        "go_database_url_file":runtime_file,"go_database_name":go_name,
        "events":[
            {"payload":String::from_utf8(evidence.signed_payload.clone()).unwrap(),"signature":String::from_utf8(evidence.signature.clone()).unwrap()},
            {"payload":String::from_utf8(lost_evidence.signed_payload.clone()).unwrap(),"signature":String::from_utf8(lost_evidence.signature.clone()).unwrap()}
        ],
    });
    fs::write(&fixture, serde_json::to_vec(&config).unwrap()).unwrap();
    fs::set_permissions(&fixture, fs::Permissions::from_mode(0o600)).unwrap();
    let permissions = Permissions {
        proof_ingress: true,
        proof_read: true,
        prepare: true,
        credit_evidence: true,
        refund_user: true,
        refund_evidence: true,
        read_user: true,
        reconcile: true,
    };
    let (shutdown, receiver) = watch::channel(false);
    let rpc = PaymentsRpc::new(f.store.0.clone(), token.as_bytes(), permissions).unwrap();
    let server = RpcServer::bind(
        &socket,
        b"identity_fixture_separate_token_0123456789abcdef",
        Some(f.identity.clone()),
    )
    .unwrap()
    .with_payments(rpc);
    let handle = tokio::spawn(server.serve(receiver));
    let root = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../extensions");
    fn run_go(root: PathBuf, temp: PathBuf, fixture: PathBuf, phase: &str) {
        let output = Command::new("mise")
            .args([
                "exec",
                "--",
                "go",
                "test",
                "-race",
                "-mod=readonly",
                "./internal/modules/payments",
                "-run",
                "^TestRustDurableProofAcrossGoRestart$",
                "-count=1",
                "-timeout=90s",
            ])
            .current_dir(&root)
            .env("MISE_IGNORED_TOOLS", "rust")
            .env("MISE_AUTO_INSTALL", "false")
            .env("PAYMENT_CORE_PROOF_FIXTURE", &fixture)
            .env("PAYMENT_CORE_PROOF_PHASE", phase)
            .stdin(Stdio::null())
            .output()
            .unwrap();
        // Private logs remain in the owned temp directory; assertion output does
        // not dump fixture credentials, raw callbacks or connection strings.
        fs::write(
            temp.join(format!("{phase}.private.log")),
            [&output.stdout[..], &output.stderr[..]].concat(),
        )
        .unwrap();
        fs::set_permissions(
            temp.join(format!("{phase}.private.log")),
            fs::Permissions::from_mode(0o600),
        )
        .unwrap();
        assert!(
            output.status.success(),
            "Go joint phase failed; inspect private owned evidence"
        );
    }
    let (r, t, fpath) = (root.clone(), temp.clone(), fixture.clone());
    tokio::task::spawn_blocking(move || run_go(r, t, fpath, "recv"))
        .await
        .unwrap();
    assert_eq!(journals(&pool).await, 0);
    let ingress: i64 = sqlx::query_scalar("SELECT count(*) FROM core_payments.accepted_proofs")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(ingress, 1);
    // Actual Go PostgreSQL insertion failure occurs after Rust has committed
    // acceptance. The provider sees 503; redelivery must recover that proof.
    let go_pool = sqlx::postgres::PgPoolOptions::new()
        .max_connections(1)
        .connect(go_owner.trim())
        .await
        .unwrap();
    // Reproduce the persisted pre-upgrade queue shape: inbox already drained,
    // credit quarantined without a proof reference, and no money receipt.
    // This is synthetic legacy-state construction, not a production migration.
    sqlx::raw_sql("UPDATE public.payment_events SET snapshot=jsonb_set(snapshot,'{evidence,accepted_proof_id}',to_jsonb(''::text)),status='done'; UPDATE public.payment_orders SET snapshot=jsonb_set(snapshot,'{jobs}',(SELECT jsonb_object_agg(key,CASE WHEN value->>'kind'='credit' THEN jsonb_set(jsonb_set(value,'{evidence,accepted_proof_id}',to_jsonb(''::text)),'{state}',to_jsonb('review'::text)) ELSE value END) FROM jsonb_each(snapshot->'jobs')));").execute(&go_pool).await.unwrap();
    sqlx::raw_sql("CREATE FUNCTION public.test_inbox_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic inbox persistence failure'; END $$; CREATE TRIGGER test_inbox_failure BEFORE INSERT ON public.payment_events FOR EACH ROW EXECUTE FUNCTION public.test_inbox_failure();").execute(&go_pool).await.unwrap();
    let (r, t, fpath) = (root.clone(), temp.clone(), fixture.clone());
    tokio::task::spawn_blocking(move || run_go(r, t, fpath, "recv-lost"))
        .await
        .unwrap();
    let ingress: i64 = sqlx::query_scalar("SELECT count(*) FROM core_payments.accepted_proofs")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(ingress, 2);
    let inbox: i64 = sqlx::query_scalar("SELECT count(*) FROM public.payment_events")
        .fetch_one(&go_pool)
        .await
        .unwrap();
    assert_eq!(inbox, 1);
    sqlx::raw_sql("DROP TRIGGER test_inbox_failure ON public.payment_events; DROP FUNCTION public.test_inbox_failure();").execute(&go_pool).await.unwrap();
    go_pool.close().await;
    shutdown.send(true).unwrap();
    handle.await.unwrap().unwrap();
    let (r, t, fpath) = (root.clone(), temp.clone(), fixture.clone());
    tokio::task::spawn_blocking(move || run_go(r, t, fpath, "offline"))
        .await
        .unwrap();
    // A new Rust service/store instance reads only durable PostgreSQL proof.
    let restarted = PaymentsStore::new(
        pool.clone(),
        vec![Channel::stripe_test(
            1,
            MERCHANT.into(),
            "whsec_rotated_joint_endpoint_only".into(),
            100_000,
        )],
    )
    .unwrap();
    let rpc = PaymentsRpc::new(restarted, token.as_bytes(), permissions).unwrap();
    let (shutdown2, receiver2) = watch::channel(false);
    let server2 = RpcServer::bind(
        &socket,
        b"identity_fixture_separate_token_0123456789abcdef",
        Some(f.identity.clone()),
    )
    .unwrap()
    .with_payments(rpc);
    let handle2 = tokio::spawn(server2.serve(receiver2));
    tokio::time::sleep(Duration::from_secs(302)).await;
    assert!(
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs()
            - accepted_timestamp
            > 301
    );
    let evidence_temp = temp.clone();
    tokio::task::spawn_blocking(move || run_go(root, evidence_temp, fixture, "settle"))
        .await
        .unwrap();
    shutdown2.send(true).unwrap();
    handle2.await.unwrap().unwrap();
    balance(&f, 500_000, 0).await;
    assert_eq!(journals(&pool).await, 1);
    let net: i64 =
        sqlx::query_scalar("SELECT sum(delta_units)::bigint FROM core_billing.ledger_entries")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(net, 0);
    let counts:(i64,i64,i64)=sqlx::query_as("SELECT (SELECT count(*) FROM core_payments.accepted_proofs),(SELECT count(*) FROM core_payments.receipts WHERE method='credit'),(SELECT count(*) FROM core_payments.provider_events)").fetch_one(&pool).await.unwrap();
    assert_eq!(counts, (2, 1, 1));
    fs::write(temp.join("result.json"), serde_json::to_vec(&json!({"status":"pass","wall_expiry_seconds":302,"go_processes":4,"rust_service_instances":2,"rust_os_process_restart":false,"accepted_proofs":2,"synthetic_legacy_done_inbox_repair":true,"core_offline_ack":503,"go_inbox_failure_ack":503,"credit_receipts":1,"journals":1,"ledger_net":net})).unwrap()).unwrap();
    fs::set_permissions(temp.join("result.json"), fs::Permissions::from_mode(0o600)).unwrap();
}

#[path = "common/abuse_resources.rs"]
mod abuse_resources;

#[sqlx::test(migrations = false)]
async fn marked_payment_intent_admission_denied_but_original_credit_and_refund_are_real(
    pool: PgPool,
) {
    let f = fresh(&pool).await;
    let intent = prepare(&f, "resource-old-prepare", 100).await;
    let team = f.identity.create_team(&f.secret).await.unwrap();
    let team_key = f
        .identity
        .issue_key(
            &f.secret,
            lmm_core::accounts::Account {
                kind: lmm_core::accounts::AccountKind::Team,
                id: team,
            },
            lmm_core::funding::FundingPolicy::inherit_account_settings(),
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
    let marker = abuse_resources::marker(&pool, &f.identity).await;
    marker.mark(101).await;
    let before = abuse_resources::financial_snapshot(&pool).await;
    for (key, account) in [
        ("resource-new-personal", f.account),
        ("resource-new-team", team_account),
    ] {
        let mut request = prepare_request(&f, key, 100);
        request.account_id = account;
        assert_eq!(
            f.store.prepare(&f.secret, request).await.unwrap_err(),
            Error::Denied
        );
        assert_eq!(
            abuse_resources::financial_snapshot(&pool).await,
            before,
            "denied payment created intent, receipt, journal or sequence change"
        );
    }
    assert_eq!(prepare(&f, "resource-old-prepare", 100).await, intent);
    let credited = credit(&f, &intent, "resource-credit-after-mark").await;
    assert_eq!(credited.state, 1);
    assert!(credited.journal_id > 0);
    balance(&f, 500_000, 0).await;
    let held = f
        .store
        .hold(
            &f.secret,
            pb::HoldRefundRequest {
                mutation: mutation("resource-old-refund-hold"),
                intent_id: intent.intent_id,
                refund_amount_minor: 20,
            },
        )
        .await
        .unwrap();
    assert_eq!(held.state, 2);
    let refund = f
        .store
        .resolve(
            pb::ResolveRefundRequest {
                mutation: mutation("resource-real-refund"),
                intent_id: intent.intent_id,
                hold_receipt_id: held.receipt_id.clone(),
                evidence: Some(proof(event(
                    &intent,
                    "evt_resource_refund",
                    Some(("re_resource", 20, "succeeded", Some(&held.receipt_id))),
                ))),
            },
            true,
        )
        .await
        .unwrap();
    assert_eq!(refund.state, 3);
    assert!(refund.journal_id > 0);
    balance(&f, 400_000, 0).await;
    let net: i64 =
        sqlx::query_scalar("SELECT sum(delta_units)::bigint FROM core_billing.ledger_entries")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(net, 0);
    assert_eq!(
        f.store
            .receipt(
                Some(&f.secret),
                pb::GetPaymentReceiptRequest {
                    intent_id: intent.intent_id,
                    idempotency_key: "resource-credit-after-mark".into()
                }
            )
            .await
            .unwrap(),
        credited
    );
}
