//! Real PostgreSQL ledger + store escrow. No fake funds receipts or providers.
use lmm_core::{
    accounts::{Account, AccountKind},
    billing::{
        Credits,
        ledger::{Action, Ledger, Outcome, Request},
    },
    commerce::{CommerceStore, CreditPrice, Error, MoneyCommand, OrderSnapshot, Price, UsdMinor},
    core_schema,
    events::EventStore,
    funding::FundingPolicy,
    identity::IdentityStore,
};
use sha2::{Digest, Sha256};
use sqlx::PgPool;

fn hash(value: &serde_json::Value) -> String {
    Sha256::digest(serde_json::to_vec(value).unwrap())
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}
fn credit_price(amount: i64) -> Price {
    Price::Credits(CreditPrice {
        amount_credits: amount,
        unit: "credits".into(),
    })
}
fn command(n: i64, price: Price) -> MoneyCommand {
    let shop = hash(&serde_json::json!(["shop", 9]));
    let order = hash(&serde_json::json!(["order", n]));
    MoneyCommand {
        key: hash(&serde_json::json!(["store-money-v1", shop, order, "pay"])),
        kind: "pay".into(),
        shop_id: shop,
        order_id: order,
        buyer: Account {
            kind: AccountKind::Personal,
            id: 7,
        },
        seller: Account {
            kind: AccountKind::Personal,
            id: 9,
        },
        total: price.clone(),
        payment_ref: String::new(),
        settlement_ref: String::new(),
        order_snapshot: OrderSnapshot {
            variant_id: hash(&serde_json::json!(["variant", n])),
            quantity: 1,
            unit_price: price,
            created_by: 7,
        },
    }
}
fn bytes(cmd: &MoneyCommand) -> Vec<u8> {
    serde_json::to_vec(&[cmd]).unwrap()
}
fn action(cmd: &MoneyCommand, kind: &str, payment: &str, settlement: &str) -> MoneyCommand {
    let mut cmd = cmd.clone();
    cmd.kind = kind.into();
    cmd.payment_ref = payment.into();
    cmd.settlement_ref = settlement.into();
    cmd.key = hash(&serde_json::json!([
        "store-money-v1",
        cmd.shop_id,
        cmd.order_id,
        kind
    ]));
    cmd
}
struct Fixture {
    identity: IdentityStore,
    store: CommerceStore,
    ledger: Ledger,
    buyer: String,
    seller: String,
    outsider: String,
    buyer_id: i64,
    seller_id: i64,
}
async fn fixture(pool: &PgPool, amount: i64) -> Fixture {
    core_schema::init(pool).await.unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    let buyer = identity.bootstrap_user(7, 1).await.unwrap().secret;
    let seller = identity.bootstrap_user(9, 1).await.unwrap().secret;
    let outsider = identity.bootstrap_user(11, 6).await.unwrap().secret;
    let buyer_id = identity.authorize(&buyer).await.unwrap().owner_account_id;
    let seller_id = identity.authorize(&seller).await.unwrap().owner_account_id;
    let ledger = Ledger::from_pool(pool.clone());
    ledger.open_wallet(buyer_id).await.unwrap();
    ledger.open_wallet(seller_id).await.unwrap();
    fund(&ledger, buyer_id, amount, "buyer").await;
    Fixture {
        identity,
        store: CommerceStore::from_pool(pool.clone()),
        ledger,
        buyer,
        seller,
        outsider,
        buyer_id,
        seller_id,
    }
}
async fn fund(ledger: &Ledger, account: i64, amount: i64, key: &str) {
    assert!(matches!(
        ledger
            .execute(&Request {
                scope: "commerce-fixture".into(),
                operation_key: key.into(),
                actor_user_id: Some(7),
                reason: "Synthetic credits only in a disposable local PostgreSQL fixture".into(),
                action: Action::Credit {
                    account_id: account,
                    amount_units: Credits::try_from(amount).unwrap(),
                    payment_reference: format!("synthetic-commerce:{key}")
                }
            })
            .await
            .unwrap(),
        Outcome::Posted { .. }
    ));
}
async fn count(pool: &PgPool, table: &str) -> i64 {
    sqlx::query_scalar(&format!("SELECT count(*) FROM {table}"))
        .fetch_one(pool)
        .await
        .unwrap()
}
async fn balanced(pool: &PgPool) {
    let n:i64=sqlx::query_scalar("SELECT count(*) FROM (SELECT journal_id FROM core_billing.ledger_entries GROUP BY journal_id HAVING sum(delta_units)<>0) s").fetch_one(pool).await.unwrap();
    assert_eq!(n, 0);
}

#[test]
fn exact_units_never_mix_credits_usd_or_float() {
    assert_eq!(credit_price(1).credits().unwrap(), 1);
    assert_eq!(
        Price::Usd(UsdMinor {
            amount_minor: 1,
            currency: "USD".into()
        })
        .credits()
        .unwrap(),
        5000
    );
    assert!(
        Price::Usd(UsdMinor {
            amount_minor: i64::MAX,
            currency: "USD".into()
        })
        .credits()
        .is_err()
    );
    for raw in [
        r#"{"amount_minor":1.1,"currency":"USD"}"#,
        r#"{"amount_credits":1,"unit":"credits","currency":"USD"}"#,
        r#"{"amount_minor":1,"currency":"USD","amount_credits":5000,"unit":"credits"}"#,
        r#"{"amount_minor":1,"amount_minor":2,"currency":"USD"}"#,
        r#"{"amount_credits":1,"unit":"credits","unit":"credits"}"#,
    ] {
        assert!(serde_json::from_str::<Price>(raw).is_err(), "{raw}");
    }
}
#[sqlx::test(migrations = false)]
async fn usd_pay_settle_full_refund_and_query_are_real_atomic_replays(pool: PgPool) {
    let f = fixture(&pool, 500_000).await;
    let cmd = command(
        1,
        Price::Usd(UsdMinor {
            amount_minor: 50,
            currency: "USD".into(),
        }),
    );
    let pay = f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap();
    assert_eq!(pay.outcome, "succeeded");
    assert_eq!(f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap(), pay);
    assert_eq!(
        f.store
            .query(&f.buyer, &cmd.shop_id, &cmd.order_id, &cmd.key)
            .await
            .unwrap(),
        Some(pay.clone())
    );
    assert_eq!(
        (
            f.ledger.balance(f.buyer_id).await.unwrap().available_units,
            f.ledger.balance(f.buyer_id).await.unwrap().reserved_units
        ),
        (250_000, 250_000)
    );
    let settle = action(&cmd, "settle", &pay.reference, "");
    let paid = f.store.execute(&f.seller, &bytes(&settle)).await.unwrap();
    assert_eq!(paid.outcome, "succeeded");
    assert_eq!(
        f.ledger.balance(f.seller_id).await.unwrap().available_units,
        250_000
    );
    let refund = action(&cmd, "refund", &pay.reference, &paid.reference);
    let refunded = f.store.execute(&f.seller, &bytes(&refund)).await.unwrap();
    assert_eq!(refunded.outcome, "succeeded");
    assert_eq!(
        f.store.execute(&f.seller, &bytes(&refund)).await.unwrap(),
        refunded
    );
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        500_000
    );
    assert_eq!(
        f.ledger.balance(f.seller_id).await.unwrap().available_units,
        0
    );
    assert_eq!(count(&pool, "core_commerce.operations").await, 3);
    let consumed: i64 = sqlx::query_scalar(
        "SELECT sum(amount_credits-refunded)::bigint FROM core_charging.external_consumption",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(consumed, 0);
    balanced(&pool).await;
}
#[sqlx::test(migrations = false)]
async fn one_credit_escrow_refund_keeps_integer_precision(pool: PgPool) {
    let f = fixture(&pool, 7).await;
    let cmd = command(2, credit_price(1));
    let pay = f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap();
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        6
    );
    let refund = action(&cmd, "refund", &pay.reference, "");
    assert_eq!(
        f.store
            .execute(&f.seller, &bytes(&refund))
            .await
            .unwrap()
            .outcome,
        "succeeded"
    );
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        7
    );
    balanced(&pool).await;
}
#[sqlx::test(migrations = false)]
async fn host_identity_outsider_admin_and_wrong_party_cannot_spend_or_read(pool: PgPool) {
    let f = fixture(&pool, 100).await;
    let cmd = command(3, credit_price(40));
    assert_eq!(
        f.store.execute(&f.outsider, &bytes(&cmd)).await,
        Err(Error::Forbidden)
    );
    assert_eq!(
        f.store.execute(&f.seller, &bytes(&cmd)).await,
        Err(Error::Forbidden)
    );
    assert_eq!(
        f.store
            .execute("host-service-token-is-not-user-credential", &bytes(&cmd))
            .await,
        Err(Error::Unauthorized)
    );
    let pay = f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap();
    assert_eq!(
        f.store
            .query(&f.outsider, &cmd.shop_id, &cmd.order_id, &cmd.key)
            .await,
        Err(Error::Forbidden)
    );
    let settle = action(&cmd, "settle", &pay.reference, "");
    assert_eq!(
        f.store.execute(&f.buyer, &bytes(&settle)).await,
        Err(Error::Forbidden)
    );
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        60
    );
    balanced(&pool).await;
}
#[sqlx::test(migrations = false)]
async fn frozen_total_seller_quantity_and_reference_cannot_change(pool: PgPool) {
    let f = fixture(&pool, 100).await;
    let cmd = command(4, credit_price(40));
    let pay = f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap();
    let mut bad = cmd.clone();
    bad.total = credit_price(1);
    bad.order_snapshot.unit_price = credit_price(1);
    assert_eq!(
        f.store.execute(&f.buyer, &bytes(&bad)).await,
        Err(Error::Conflict)
    );
    bad = cmd.clone();
    bad.order_snapshot.quantity = 2;
    assert_eq!(
        f.store.execute(&f.buyer, &bytes(&bad)).await,
        Err(Error::Invalid)
    );
    bad = action(&cmd, "refund", "commerce:journal:999", "");
    assert_eq!(
        f.store.execute(&f.seller, &bytes(&bad)).await,
        Err(Error::Conflict)
    );
    bad = action(&cmd, "settle", &pay.reference, "");
    bad.seller = Account {
        kind: AccountKind::Personal,
        id: 11,
    };
    assert_eq!(
        f.store.execute(&f.outsider, &bytes(&bad)).await,
        Err(Error::Conflict)
    );
    assert_eq!(count(&pool, "core_commerce.operations").await, 1);
    balanced(&pool).await;
}
#[sqlx::test(migrations = false)]
async fn insufficient_pay_has_permanent_receipt_without_budget_consumption(pool: PgPool) {
    let f = fixture(&pool, 10).await;
    let cmd = command(5, credit_price(40));
    let first = f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap();
    assert_eq!(
        (first.outcome.as_str(), first.rejection_code.as_str()),
        ("rejected", "insufficient_funds")
    );
    fund(&f.ledger, f.buyer_id, 100, "late-funds").await;
    assert_eq!(
        f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap(),
        first
    );
    assert_eq!(count(&pool, "core_charging.external_consumption").await, 0);
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        110
    );
    balanced(&pool).await;
}
#[sqlx::test(migrations = false)]
async fn api_key_requires_explicit_scope_and_revocation_denies_replay(pool: PgPool) {
    let f = fixture(&pool, 100).await;
    let cmd = command(6, credit_price(40));
    let key = f
        .identity
        .issue_key(&f.buyer, cmd.buyer, FundingPolicy::new_personal_key(), 3600)
        .await
        .unwrap();
    assert_eq!(
        f.store.execute(&key.secret, &bytes(&cmd)).await,
        Err(Error::Forbidden)
    );
    sqlx::query("INSERT INTO core_commerce.key_scopes(credential_id,can_spend) VALUES($1,true)")
        .bind(key.id)
        .execute(&pool)
        .await
        .unwrap();
    let receipt = f.store.execute(&key.secret, &bytes(&cmd)).await.unwrap();
    assert_eq!(receipt.outcome, "succeeded");
    sqlx::query("UPDATE core_commerce.key_scopes SET can_spend=false WHERE credential_id=$1")
        .bind(key.id)
        .execute(&pool)
        .await
        .unwrap();
    assert_eq!(
        f.store.execute(&key.secret, &bytes(&cmd)).await,
        Err(Error::Forbidden)
    );
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        60
    );
}
#[sqlx::test(migrations = false)]
async fn full_event_queue_rolls_back_new_money_and_receipt(pool: PgPool) {
    let f = fixture(&pool, 100).await;
    let mut tx = pool.begin().await.unwrap();
    EventStore::provision(
        &mut tx,
        "commerce-fixture",
        "store",
        &["commerce.operation.v1".into()],
        1,
    )
    .await
    .unwrap();
    tx.commit().await.unwrap();
    let first = command(7, credit_price(20));
    assert_eq!(
        f.store
            .execute(&f.buyer, &bytes(&first))
            .await
            .unwrap()
            .outcome,
        "succeeded"
    );
    let second = command(8, credit_price(20));
    assert_eq!(
        f.store.execute(&f.buyer, &bytes(&second)).await,
        Err(Error::Storage)
    );
    assert_eq!(count(&pool, "core_commerce.orders").await, 1);
    assert_eq!(count(&pool, "core_commerce.operations").await, 1);
    assert_eq!(count(&pool, "core_charging.external_consumption").await, 1);
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        80
    );
    balanced(&pool).await;
}
#[sqlx::test(migrations = false)]
async fn duplicate_concurrent_payment_only_reserves_once(pool: PgPool) {
    let f = fixture(&pool, 100).await;
    let cmd = command(9, credit_price(40));
    let wire = bytes(&cmd);
    let (a, b) = tokio::join!(
        f.store.execute(&f.buyer, &wire),
        f.store.execute(&f.buyer, &wire)
    );
    assert_eq!(a.unwrap(), b.unwrap());
    assert_eq!(count(&pool, "core_commerce.operations").await, 1);
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().reserved_units,
        40
    );
    balanced(&pool).await;
}
#[sqlx::test(migrations = false)]
async fn temporarily_empty_seller_refund_is_unknown_then_recovers_same_key(pool: PgPool) {
    let f = fixture(&pool, 100).await;
    let cmd = command(10, credit_price(40));
    let pay = f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap();
    let settled = f
        .store
        .execute(
            &f.seller,
            &bytes(&action(&cmd, "settle", &pay.reference, "")),
        )
        .await
        .unwrap();
    assert!(matches!(
        f.ledger
            .execute(&Request {
                scope: "commerce-fixture".into(),
                operation_key: "seller-spent".into(),
                actor_user_id: Some(9),
                reason: "Synthetic seller spent proceeds before refund".into(),
                action: Action::Charge {
                    account_id: f.seller_id,
                    amount_units: Credits::try_from(40).unwrap()
                }
            })
            .await
            .unwrap(),
        Outcome::Posted { .. }
    ));
    let refund = action(&cmd, "refund", &pay.reference, &settled.reference);
    assert_eq!(
        f.store.execute(&f.seller, &bytes(&refund)).await,
        Err(Error::Pending)
    );
    assert_eq!(
        f.store
            .query(&f.seller, &cmd.shop_id, &cmd.order_id, &refund.key)
            .await
            .unwrap(),
        None
    );
    assert_eq!(count(&pool, "core_commerce.operations").await, 2);
    let used: i64 = sqlx::query_scalar(
        "SELECT amount_credits-refunded FROM core_charging.external_consumption",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(used, 40);
    fund(&f.ledger, f.seller_id, 40, "seller-recovers").await;
    let result = f.store.execute(&f.seller, &bytes(&refund)).await.unwrap();
    assert_eq!(result.outcome, "succeeded");
    assert_eq!(
        f.store.execute(&f.seller, &bytes(&refund)).await.unwrap(),
        result
    );
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        100
    );
    balanced(&pool).await;
}
#[sqlx::test(migrations = false)]
async fn model_and_commerce_on_distinct_wallets_cannot_exceed_shared_user_cap(pool: PgPool) {
    use lmm_core::billing::{
        charging::{
            Budget, Charging, Error as ChargeError, Price as ModelPrice, Request as ModelRequest,
        },
        transactional_ledger::TransactionalLedger,
    };
    use std::{sync::Arc, time::Duration};
    let f = fixture(&pool, 100).await;
    let team = f.identity.create_team(&f.buyer).await.unwrap();
    let key = f
        .identity
        .issue_key(
            &f.buyer,
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
        .authorize(&key.secret)
        .await
        .unwrap()
        .owner_account_id;
    f.ledger.open_wallet(team_account).await.unwrap();
    fund(&f.ledger, team_account, 100, "model-team").await;
    let charging = Charging::new(pool.clone(), Arc::new(TransactionalLedger));
    charging
        .set_budget(
            &f.buyer,
            &Budget {
                scope: "self".into(),
                account_id: None,
                user_id: Some(7),
                key_id: None,
                period: "day".into(),
                anchor: 0,
                seconds: 0,
                limit: 50,
            },
        )
        .await
        .unwrap();
    let model = ModelRequest {
        id: "model-commerce-budget-race".into(),
        fingerprint: [3; 32],
        price: ModelPrice {
            upstream_cost_group_ppm: None,
            version: "price-1".into(),
            model: "local-model".into(),
            group: "default".into(),
            input_per_million: 1_000_000,
            cached_per_million: 500_000,
            output_per_million: 2_000_000,
        },
        reserve: 40,
        lease_seconds: 300,
        maximum_seconds: 600,
    };
    let cmd = command(11, credit_price(40));
    let wire = bytes(&cmd);
    let mut blocker = pool.begin().await.unwrap();
    sqlx::query("SELECT b.ledger_account_id FROM core_billing.balance_state b JOIN core_billing.ledger_accounts a ON a.id=b.ledger_account_id WHERE a.owner_account_id IN ($1,$2) FOR NO KEY UPDATE OF b")
        .bind(f.buyer_id).bind(team_account).fetch_all(&mut *blocker).await.unwrap();
    let store = f.store.clone();
    let buyer = f.buyer.clone();
    let commerce_task = tokio::spawn(async move { store.execute(&buyer, &wire).await });
    let model_task = tokio::spawn(async move {
        for _ in 0..12 {
            match charging.reserve(&key.secret, &model, &[5; 32]).await {
                Err(ChargeError::Retry) => tokio::task::yield_now().await,
                result => return result,
            }
        }
        panic!("bounded model retry exhausted")
    });
    let mut waiting = false;
    for _ in 0..40 {
        let n:i64=sqlx::query_scalar("SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT core_billing.post_ledger%'").fetch_one(&pool).await.unwrap();
        if n == 2 {
            waiting = true;
            break;
        }
        tokio::time::sleep(Duration::from_millis(2)).await;
    }
    blocker.commit().await.unwrap();
    assert!(
        waiting,
        "independent-wallet writers must both pass their shared budget predicate before wallet release"
    );
    let commerce = commerce_task.await.unwrap().unwrap();
    let model = model_task.await.unwrap();
    let accepted = usize::from(commerce.outcome == "succeeded") + usize::from(model.is_ok());
    assert_eq!(accepted, 1);
    if commerce.outcome == "rejected" {
        assert_eq!(commerce.rejection_code, "budget_exceeded");
    }
    if let Err(e) = model {
        assert_eq!(e, ChargeError::BudgetExceeded);
    }
    let used:i64=sqlx::query_scalar("SELECT ((SELECT coalesce(sum(core_charging.committed_credits(state,reserved,settled,refunded)),0) FROM core_charging.requests)+(SELECT coalesce(sum(amount_credits-refunded),0) FROM core_charging.external_consumption))::bigint").fetch_one(&pool).await.unwrap();
    assert_eq!(used, 40);
    balanced(&pool).await;
}
#[sqlx::test(migrations = false)]
async fn original_capture_grant_survives_buyer_logout_but_not_other_service_or_changed_delivery(
    pool: PgPool,
) {
    let f = fixture(&pool, 100).await;
    let cmd = command(12, credit_price(40));
    let pay = f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap();
    let settle = action(&cmd, "settle", &pay.reference, "");
    let wire = bytes(&settle);
    let wrong = CommerceStore::from_pool(pool.clone())
        .with_service_principal("different-store")
        .unwrap();
    assert_eq!(
        wrong
            .complete_delivery(&wire, &pay.command_hash, "delivery-12")
            .await,
        Err(Error::Forbidden)
    );
    assert_eq!(
        f.store
            .complete_delivery(&wire, &"0".repeat(64), "delivery-12")
            .await,
        Err(Error::Conflict)
    );
    sqlx::query(
        "UPDATE core_identity.credentials SET revoked_at=clock_timestamp() WHERE digest=$1",
    )
    .bind(Sha256::digest(f.buyer.as_bytes()).to_vec())
    .execute(&pool)
    .await
    .unwrap();
    assert_eq!(
        f.store.execute(&f.buyer, &bytes(&cmd)).await,
        Err(Error::Unauthorized)
    );
    let captured = f
        .store
        .complete_delivery(&wire, &pay.command_hash, "delivery-12")
        .await
        .unwrap();
    assert_eq!(captured.outcome, "succeeded");
    assert_eq!(
        f.store
            .complete_delivery(&wire, &pay.command_hash, "delivery-12")
            .await
            .unwrap(),
        captured
    );
    assert_eq!(
        f.store
            .complete_delivery(&wire, &pay.command_hash, "different-delivery")
            .await,
        Err(Error::Conflict)
    );
    assert_eq!(
        f.ledger.balance(f.seller_id).await.unwrap().available_units,
        40
    );
    assert_eq!(count(&pool, "core_commerce.operations").await, 2);
    balanced(&pool).await;
}
#[sqlx::test(migrations = false)]
async fn refunded_order_cannot_consume_old_capture_grant(pool: PgPool) {
    let f = fixture(&pool, 100).await;
    let cmd = command(13, credit_price(40));
    let pay = f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap();
    let refund = action(&cmd, "refund", &pay.reference, "");
    assert_eq!(
        f.store
            .execute(&f.seller, &bytes(&refund))
            .await
            .unwrap()
            .outcome,
        "succeeded"
    );
    let settle = action(&cmd, "settle", &pay.reference, "");
    assert_eq!(
        f.store
            .complete_delivery(&bytes(&settle), &pay.command_hash, "delivery-after-refund")
            .await,
        Err(Error::Conflict)
    );
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        100
    );
    assert_eq!(
        f.ledger.balance(f.seller_id).await.unwrap().available_units,
        0
    );
    balanced(&pool).await;
}
#[tokio::test]
async fn rpc_separate_service_permission_and_size_fail_before_database() {
    use lmm_core::{
        commerce::rpc::{CommerceRpc, Permissions},
        internal_rpc::pb::{self, core_commerce_server::CoreCommerce},
    };
    let pool = sqlx::postgres::PgPoolOptions::new()
        .connect_lazy("postgresql://unused@127.0.0.1:1/never_connect")
        .unwrap();
    let rpc = CommerceRpc::new(
        CommerceStore::from_pool(pool),
        b"synthetic-commerce-service-token-32-bytes",
        Permissions::default(),
    )
    .unwrap();
    let mut q = tonic::Request::new(pb::CompleteDeliveryRequest {
        protocol_major: 1,
        command_json: Vec::new(),
        payment_command_hash: String::new(),
        delivery_reference: String::new(),
    });
    q.metadata_mut().insert(
        "authorization",
        "Bearer synthetic-commerce-service-token-32-bytes"
            .parse()
            .unwrap(),
    );
    q.metadata_mut()
        .insert("x-lmm-protocol", "1".parse().unwrap());
    assert_eq!(
        rpc.complete_delivery(q).await.unwrap_err().code(),
        tonic::Code::PermissionDenied
    );
    let mut q = tonic::Request::new(pb::ExecuteCommerceRequest {
        protocol_major: 1,
        command_json: bytes(&command(14, credit_price(1))),
    });
    q.metadata_mut().insert(
        "authorization",
        "Bearer synthetic-commerce-service-token-32-bytes"
            .parse()
            .unwrap(),
    );
    q.metadata_mut()
        .insert("x-lmm-protocol", "1".parse().unwrap());
    assert_eq!(
        rpc.reconcile_commerce(q).await.unwrap_err().code(),
        tonic::Code::PermissionDenied
    );
    let q = tonic::Request::new(pb::ExecuteCommerceRequest {
        protocol_major: 1,
        command_json: vec![b' '; 8193],
    });
    assert_eq!(
        rpc.execute_commerce(q).await.unwrap_err().code(),
        tonic::Code::InvalidArgument
    );
    let q = tonic::Request::new(pb::ExecuteCommerceRequest {
        protocol_major: 1,
        command_json: bytes(&command(14, credit_price(1))),
    });
    assert_eq!(
        rpc.execute_commerce(q).await.unwrap_err().code(),
        tonic::Code::Unauthenticated
    );
}

#[sqlx::test(migrations = false)]
async fn concurrent_completion_consumes_original_capture_grant_once(pool: PgPool) {
    let f = fixture(&pool, 100).await;
    let cmd = command(15, credit_price(40));
    let pay = f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap();
    let settle = action(&cmd, "settle", &pay.reference, "");
    let wire = bytes(&settle);
    let (a, b) = tokio::join!(
        f.store
            .complete_delivery(&wire, &pay.command_hash, "delivery-15"),
        f.store
            .complete_delivery(&wire, &pay.command_hash, "delivery-15")
    );
    assert_eq!(a.unwrap(), b.unwrap());
    assert_eq!(count(&pool, "core_commerce.operations").await, 2);
    assert_eq!(
        f.ledger.balance(f.seller_id).await.unwrap().available_units,
        40
    );
    let captures: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_billing.ledger_journals WHERE kind='commerce_capture'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(captures, 1);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn concurrent_refund_and_completion_are_serialized_without_stranding_buyer_money(
    pool: PgPool,
) {
    let f = fixture(&pool, 100).await;
    let cmd = command(16, credit_price(40));
    let pay = f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap();
    let settle = action(&cmd, "settle", &pay.reference, "");
    let refund = action(&cmd, "refund", &pay.reference, "");
    let settle_wire = bytes(&settle);
    let refund_wire = bytes(&refund);
    let (completed, refunded) = tokio::join!(
        f.store
            .complete_delivery(&settle_wire, &pay.command_hash, "delivery-16"),
        f.store.execute(&f.seller, &refund_wire)
    );
    // If capture wins, the originally unpaid refund command must not silently
    // change its settlement binding. Reconcile then send the correct original
    // capture reference under the SAME stable refund operation key.
    match refunded {
        Ok(r) => assert_eq!(r.outcome, "succeeded"),
        Err(Error::Conflict) => {
            let captured = completed.as_ref().unwrap();
            let bound = action(&cmd, "refund", &pay.reference, &captured.reference);
            assert_eq!(
                f.store
                    .execute(&f.seller, &bytes(&bound))
                    .await
                    .unwrap()
                    .outcome,
                "succeeded"
            );
        }
        other => panic!("unexpected refund result {other:?}"),
    }
    assert!(completed.is_ok() || completed == Err(Error::Conflict));
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        100
    );
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().reserved_units,
        0
    );
    assert_eq!(
        f.ledger.balance(f.seller_id).await.unwrap().available_units,
        0
    );
    let used: i64 = sqlx::query_scalar(
        "SELECT amount_credits-refunded FROM core_charging.external_consumption",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(used, 0);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn authenticated_manual_settlement_is_reconciled_without_second_delivery_capture(
    pool: PgPool,
) {
    let f = fixture(&pool, 100).await;
    let cmd = command(17, credit_price(40));
    let pay = f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap();
    let settle = action(&cmd, "settle", &pay.reference, "");
    let wire = bytes(&settle);
    let manual = f.store.execute(&f.seller, &wire).await.unwrap();
    // Native seller settlement has no committed delivery evidence attached.
    // Do not retrofit an unrelated delivery ID onto its immutable receipt.
    assert_eq!(
        f.store
            .complete_delivery(&wire, &pay.command_hash, "later-delivery")
            .await,
        Err(Error::Conflict)
    );
    assert_eq!(
        f.store
            .query(&f.seller, &cmd.shop_id, &cmd.order_id, &settle.key)
            .await
            .unwrap(),
        Some(manual)
    );
    assert_eq!(count(&pool, "core_commerce.operations").await, 2);
    assert_eq!(
        f.ledger.balance(f.seller_id).await.unwrap().available_units,
        40
    );
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn inactive_original_buyer_can_receive_refund_without_selecting_another_payer(pool: PgPool) {
    let f = fixture(&pool, 100).await;
    let cmd = command(18, credit_price(40));
    let pay = f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap();
    sqlx::query("UPDATE core_identity.users SET active=false WHERE id=7")
        .execute(&pool)
        .await
        .unwrap();
    let mut changed = action(&cmd, "refund", &pay.reference, "");
    changed.buyer = Account {
        kind: AccountKind::Personal,
        id: 11,
    };
    assert_eq!(
        f.store.execute(&f.seller, &bytes(&changed)).await,
        Err(Error::Conflict)
    );
    let refund = action(&cmd, "refund", &pay.reference, "");
    assert_eq!(
        f.store
            .execute(&f.seller, &bytes(&refund))
            .await
            .unwrap()
            .outcome,
        "succeeded"
    );
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        100
    );
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().reserved_units,
        0
    );
    assert_eq!(
        f.ledger.balance(f.seller_id).await.unwrap().available_units,
        0
    );
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn lost_payment_acknowledgement_recovers_after_logout_without_replaying_user_spend(
    pool: PgPool,
) {
    let f = fixture(&pool, 100).await;
    let cmd = command(19, credit_price(40));
    let original = bytes(&cmd);
    // The Core transaction commits; pretend its transport response was lost.
    let committed = f.store.execute(&f.buyer, &original).await.unwrap();
    sqlx::query(
        "UPDATE core_identity.credentials SET revoked_at=clock_timestamp() WHERE digest=$1",
    )
    .bind(Sha256::digest(f.buyer.as_bytes()).to_vec())
    .execute(&pool)
    .await
    .unwrap();
    let restarted = CommerceStore::from_pool(pool.clone());
    assert_eq!(
        restarted
            .query(&f.buyer, &cmd.shop_id, &cmd.order_id, &cmd.key)
            .await,
        Err(Error::Unauthorized)
    );
    assert_eq!(
        restarted.reconcile(&original).await.unwrap(),
        Some(committed.clone())
    );
    let settle = action(&cmd, "settle", &committed.reference, "");
    assert_eq!(
        restarted
            .complete_delivery(
                &bytes(&settle),
                &committed.command_hash,
                "recovered-delivery-19"
            )
            .await
            .unwrap()
            .outcome,
        "succeeded"
    );
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        60
    );
    assert_eq!(
        f.ledger.balance(f.seller_id).await.unwrap().available_units,
        40
    );
    assert_eq!(count(&pool, "core_commerce.operations").await, 2);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn service_receipt_reconciliation_rejects_changed_hash_principal_key_and_never_mutates_money(
    pool: PgPool,
) {
    let f = fixture(&pool, 10).await;
    let cmd = command(20, credit_price(40));
    let original = bytes(&cmd);
    let before = count(&pool, "core_commerce.orders").await;
    assert_eq!(f.store.reconcile(&original).await.unwrap(), None);
    assert_eq!(count(&pool, "core_commerce.orders").await, before);
    let rejected = f.store.execute(&f.buyer, &original).await.unwrap();
    assert_eq!(rejected.outcome, "rejected");
    let journal_count = count(&pool, "core_billing.ledger_journals").await;
    let receipt_count = count(&pool, "core_commerce.operations").await;
    let outbox_count = count(&pool, "core_events.outbox").await;
    assert_eq!(f.store.reconcile(&original).await.unwrap(), Some(rejected));
    let wrong_service = CommerceStore::from_pool(pool.clone())
        .with_service_principal("another-store")
        .unwrap();
    assert_eq!(
        wrong_service.reconcile(&original).await,
        Err(Error::Forbidden)
    );
    let mut changed_hash = original.clone();
    changed_hash.push(b' ');
    assert_eq!(f.store.reconcile(&changed_hash).await, Err(Error::Conflict));
    let mut wrong_key = cmd.clone();
    wrong_key.key = "0".repeat(64);
    assert_eq!(
        f.store.reconcile(&bytes(&wrong_key)).await,
        Err(Error::Invalid)
    );
    let mut changed_amount = cmd.clone();
    changed_amount.total = credit_price(1);
    changed_amount.order_snapshot.unit_price = credit_price(1);
    assert_eq!(
        f.store.reconcile(&bytes(&changed_amount)).await,
        Err(Error::Conflict)
    );
    let absent = command(21, credit_price(40));
    assert_eq!(f.store.reconcile(&bytes(&absent)).await.unwrap(), None);
    assert_eq!(
        count(&pool, "core_billing.ledger_journals").await,
        journal_count
    );
    assert_eq!(
        count(&pool, "core_commerce.operations").await,
        receipt_count
    );
    assert_eq!(count(&pool, "core_events.outbox").await, outbox_count);
    assert_eq!(count(&pool, "core_charging.external_consumption").await, 0);
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        10
    );
    balanced(&pool).await;
}

async fn seller_opening_debt(pool: &PgPool, account: i64, debt: i64) {
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::query("SET LOCAL synchronous_commit=on")
        .execute(&mut *tx)
        .await
        .unwrap();
    // Controlled OWNER-only synthetic opening, never direct balance updates.
    let request = serde_json::json!({"migration_id":"commerce-synthetic-opening","source_entity_key":"synthetic-seller","source_archive_sha256":"1".repeat(64),"mapping_sha256":"2".repeat(64),"source_row_sha256":"3".repeat(64),"account_id":account,"available_units":-debt,"reserved_units":0,"affiliate_units":0,"observed_at":1_700_000_000i64});
    let _: sqlx::types::Json<serde_json::Value> =
        sqlx::query_scalar("SELECT core_billing.import_opening_balance($1)")
            .bind(sqlx::types::Json(request))
            .fetch_one(&mut *tx)
            .await
            .unwrap();
    tx.commit().await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn settled_refund_restores_only_original_seller_debt_repayment(pool: PgPool) {
    let f = fixture(&pool, 500).await;
    seller_opening_debt(&pool, f.seller_id, 100).await;
    let cmd = command(22, credit_price(150));
    let pay = f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap();
    let captured = f
        .store
        .complete_delivery(
            &bytes(&action(&cmd, "settle", &pay.reference, "")),
            &pay.command_hash,
            "debt-delivery-22",
        )
        .await
        .unwrap();
    assert_eq!(
        f.ledger.balance(f.seller_id).await.unwrap().available_units,
        50
    );
    let refund = action(&cmd, "refund", &pay.reference, &captured.reference);
    let result = f.store.execute(&f.seller, &bytes(&refund)).await.unwrap();
    assert_eq!(result.outcome, "succeeded");
    assert_eq!(
        f.store.execute(&f.seller, &bytes(&refund)).await.unwrap(),
        result
    );
    let seller = f.ledger.balance(f.seller_id).await.unwrap();
    assert_eq!((seller.available_units, seller.spendable_units), (-100, 0));
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        500
    );
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn settled_refund_uses_current_seller_wallet_before_recreating_old_debt(pool: PgPool) {
    let f = fixture(&pool, 500).await;
    seller_opening_debt(&pool, f.seller_id, 100).await;
    let cmd = command(23, credit_price(150));
    let pay = f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap();
    let captured = f
        .store
        .complete_delivery(
            &bytes(&action(&cmd, "settle", &pay.reference, "")),
            &pay.command_hash,
            "debt-delivery-23",
        )
        .await
        .unwrap();
    fund(
        &f.ledger,
        f.seller_id,
        100,
        "seller-new-credit-after-capture",
    )
    .await;
    assert_eq!(
        f.ledger.balance(f.seller_id).await.unwrap().spendable_units,
        150
    );
    assert_eq!(
        f.store
            .execute(
                &f.seller,
                &bytes(&action(&cmd, "refund", &pay.reference, &captured.reference))
            )
            .await
            .unwrap()
            .outcome,
        "succeeded"
    );
    let seller = f.ledger.balance(f.seller_id).await.unwrap();
    assert_eq!((seller.available_units, seller.spendable_units), (0, 0));
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        500
    );
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn spent_seller_proceeds_cannot_create_more_debt_than_original_capture_repaid(pool: PgPool) {
    let f = fixture(&pool, 500).await;
    seller_opening_debt(&pool, f.seller_id, 100).await;
    let cmd = command(24, credit_price(150));
    let pay = f.store.execute(&f.buyer, &bytes(&cmd)).await.unwrap();
    let captured = f
        .store
        .complete_delivery(
            &bytes(&action(&cmd, "settle", &pay.reference, "")),
            &pay.command_hash,
            "debt-delivery-24",
        )
        .await
        .unwrap();
    assert!(matches!(
        f.ledger
            .execute(&Request {
                scope: "commerce-fixture".into(),
                operation_key: "seller-spent-nondebt-leg".into(),
                actor_user_id: Some(9),
                reason: "Synthetic seller spent only positive cash proceeds".into(),
                action: Action::Charge {
                    account_id: f.seller_id,
                    amount_units: Credits::try_from(50).unwrap()
                }
            })
            .await
            .unwrap(),
        Outcome::Posted { .. }
    ));
    let refund = action(&cmd, "refund", &pay.reference, &captured.reference);
    assert_eq!(
        f.store.execute(&f.seller, &bytes(&refund)).await,
        Err(Error::Pending)
    );
    assert_eq!(f.store.reconcile(&bytes(&refund)).await.unwrap(), None);
    assert_eq!(
        f.ledger.balance(f.seller_id).await.unwrap().available_units,
        0
    );
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        350
    );
    fund(
        &f.ledger,
        f.seller_id,
        50,
        "seller-restores-original-cash-leg",
    )
    .await;
    assert_eq!(
        f.store
            .execute(&f.seller, &bytes(&refund))
            .await
            .unwrap()
            .outcome,
        "succeeded"
    );
    assert_eq!(
        f.ledger.balance(f.seller_id).await.unwrap().available_units,
        -100
    );
    assert_eq!(
        f.ledger.balance(f.buyer_id).await.unwrap().available_units,
        500
    );
    balanced(&pool).await;
}

#[path = "common/abuse_resources.rs"]
mod abuse_resources;

#[sqlx::test(migrations = false)]
async fn marked_new_personal_team_and_key_commerce_denied_but_existing_money_finishes(
    pool: PgPool,
) {
    let f = fixture(&pool, 500_000).await;
    let personal_key = f
        .identity
        .issue_key(
            &f.buyer,
            Account {
                kind: AccountKind::Personal,
                id: 7,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    let team = f.identity.create_team(&f.buyer).await.unwrap();
    let team_key = f
        .identity
        .issue_key(
            &f.buyer,
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
    f.ledger.open_wallet(team_account).await.unwrap();
    fund(&f.ledger, team_account, 500_000, "marked-team-fixture").await;
    sqlx::query(
        "INSERT INTO core_commerce.key_scopes(credential_id,can_spend) VALUES($1,TRUE),($2,TRUE)",
    )
    .bind(personal_key.id)
    .bind(team_key.id)
    .execute(&pool)
    .await
    .unwrap();
    let first = command(91_001, credit_price(100));
    let second = command(91_002, credit_price(100));
    let first_receipt = f.store.execute(&f.buyer, &bytes(&first)).await.unwrap();
    let second_receipt = f.store.execute(&f.buyer, &bytes(&second)).await.unwrap();
    assert_eq!(first_receipt.outcome, "succeeded");
    assert_eq!(second_receipt.outcome, "succeeded");
    let marker = abuse_resources::marker(&pool, &f.identity).await;
    marker.mark(7).await;
    marker.mark(9).await;
    let before = abuse_resources::financial_snapshot(&pool).await;
    for (offset, credential, buyer) in [
        (
            0,
            &f.buyer,
            Account {
                kind: AccountKind::Personal,
                id: 7,
            },
        ),
        (
            1,
            &personal_key.secret,
            Account {
                kind: AccountKind::Personal,
                id: 7,
            },
        ),
        (
            2,
            &f.buyer,
            Account {
                kind: AccountKind::Team,
                id: team,
            },
        ),
        (
            3,
            &team_key.secret,
            Account {
                kind: AccountKind::Team,
                id: team,
            },
        ),
    ] {
        let mut new = command(92_000 + offset, credit_price(100));
        new.buyer = buyer;
        assert_eq!(
            f.store.execute(credential, &bytes(&new)).await.unwrap_err(),
            Error::Forbidden
        );
        assert_eq!(
            abuse_resources::financial_snapshot(&pool).await,
            before,
            "denied new commerce changed funds, metadata or sequences"
        );
    }
    assert_eq!(
        f.store.execute(&f.buyer, &bytes(&first)).await.unwrap(),
        first_receipt
    );
    let settle = action(&first, "settle", &first_receipt.reference, "");
    let settled = f.store.execute(&f.seller, &bytes(&settle)).await.unwrap();
    assert_eq!(settled.outcome, "succeeded");
    let refund_settled = action(
        &first,
        "refund",
        &first_receipt.reference,
        &settled.reference,
    );
    assert_eq!(
        f.store
            .execute(&f.seller, &bytes(&refund_settled))
            .await
            .unwrap()
            .outcome,
        "succeeded"
    );
    let refund_held = action(&second, "refund", &second_receipt.reference, "");
    assert_eq!(
        f.store
            .execute(&f.seller, &bytes(&refund_held))
            .await
            .unwrap()
            .outcome,
        "succeeded"
    );
    let wallet = f.ledger.balance(f.buyer_id).await.unwrap();
    assert_eq!(
        (wallet.available_units, wallet.reserved_units),
        (500_000, 0)
    );
    balanced(&pool).await;
    // Authentication and own key management continue; no fake ledger entries.
    assert_eq!(f.identity.authorize(&f.buyer).await.unwrap().user_id, 7);
    assert!(
        f.identity
            .issue_key(
                &f.buyer,
                Account {
                    kind: AccountKind::Personal,
                    id: 7
                },
                FundingPolicy::new_personal_key(),
                3600
            )
            .await
            .is_ok()
    );
}
