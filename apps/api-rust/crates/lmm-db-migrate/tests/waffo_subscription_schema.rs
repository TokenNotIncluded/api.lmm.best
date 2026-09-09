use lmm_db_migrate::forward_schema::verify_waffo_subscription_schema;
use postgres::{Client, NoTls};

#[test]
#[ignore = "requires native PostgreSQL and LMM_TEST_DATABASE_URL"]
fn contract_eight_preserves_pending_evidence_and_rejects_broken_replay_guards() {
    let database_url = std::env::var("LMM_TEST_DATABASE_URL").expect("test database URL");
    let schema = format!("lmm_contract_eight_{}", std::process::id());
    let sql = include_str!("../../../migrations/0008_waffo_subscription_webhooks.sql")
        .replace("__LMM_APP_SCHEMA__", &format!("\"{schema}\""));
    let mut client = Client::connect(&database_url, NoTls).expect("connect to test PostgreSQL");
    let mut transaction = client.transaction().expect("start test transaction");
    transaction
        .batch_execute(&format!("CREATE SCHEMA {schema}; {sql}"))
        .expect("apply contract-8 migration");
    transaction
        .batch_execute(&sql)
        .expect("contract-8 migration is idempotent");
    verify_waffo_subscription_schema(&mut transaction, &schema).expect("valid contract-8 schema");

    let payment = format!(
        "INSERT INTO {schema}.waffo_pancake_subscription_payments \
         (subscription_order_id,event_id,provider_order_id,payment_id,currency,amount_micros,payment_date,payload,received_at) \
         VALUES (1,$1,'ORD_1',$2,'USD',3990000,1788825600,'fixture',1788825601) \
         ON CONFLICT DO NOTHING"
    );
    assert_eq!(
        transaction
            .execute(&payment, &[&"event-1", &"payment-1"])
            .unwrap(),
        1
    );
    assert_eq!(
        transaction
            .execute(&payment, &[&"event-1", &"payment-2"])
            .unwrap(),
        0,
        "event replay must not create another payment"
    );
    assert_eq!(
        transaction
            .execute(&payment, &[&"event-2", &"payment-1"])
            .unwrap(),
        0,
        "one payment must not be claimed by a different event ID"
    );
    let boundaries = transaction
        .query_one(
            &format!(
                "SELECT period_start,period_end FROM {schema}.waffo_pancake_subscription_payments"
            ),
            &[],
        )
        .expect("read durable pending payment");
    assert_eq!(boundaries.get::<_, i64>(0), 0);
    assert_eq!(boundaries.get::<_, i64>(1), 0);

    let period = format!(
        "INSERT INTO {schema}.waffo_pancake_subscription_periods \
         (subscription_order_id,event_id,event_type,provider_order_id,billing_period,currency,amount_micros,period_start,period_end,payload,received_at) \
         VALUES (1,$1,'subscription.renewed','ORD_1','monthly','USD',3990000,1788825600,1791417600,'fixture',1788825601) \
         ON CONFLICT DO NOTHING"
    );
    for event_id in ["period-1", "period-2"] {
        assert_eq!(transaction.execute(&period, &[&event_id]).unwrap(), 1);
    }
    assert_eq!(transaction.execute(&period, &[&"period-1"]).unwrap(), 0);

    for (mutation, expected_error) in [
        (
            format!(
                "ALTER TABLE {schema}.waffo_pancake_subscription_payments ALTER COLUMN period_start SET DEFAULT 10"
            ),
            "column/default contract mismatch",
        ),
        (
            format!(
                "ALTER TABLE {schema}.waffo_pancake_subscription_periods ALTER COLUMN id DROP DEFAULT"
            ),
            "primary key/sequence mismatch",
        ),
        (
            format!(
                "DROP INDEX {schema}.idx_waffo_pancake_subscription_payments_payment_id; \
                     CREATE INDEX idx_waffo_pancake_subscription_payments_payment_id \
                     ON {schema}.waffo_pancake_subscription_payments(payment_id)"
            ),
            "idx_waffo_pancake_subscription_payments_payment_id",
        ),
        (
            format!(
                "DROP INDEX {schema}.idx_waffo_pancake_subscription_periods_event_id; \
                     CREATE UNIQUE INDEX idx_waffo_pancake_subscription_periods_event_id \
                     ON {schema}.waffo_pancake_subscription_periods(event_id, (payload || ''))"
            ),
            "idx_waffo_pancake_subscription_periods_event_id",
        ),
        (
            format!(
                "DROP INDEX {schema}.idx_waffo_pancake_subscription_payments_provider_order_id; \
                     CREATE INDEX idx_waffo_pancake_subscription_payments_provider_order_id \
                     ON {schema}.waffo_pancake_subscription_payments(provider_order_id) WHERE period_end=0"
            ),
            "idx_waffo_pancake_subscription_payments_provider_order_id",
        ),
    ] {
        transaction.batch_execute("SAVEPOINT mutation").unwrap();
        transaction.batch_execute(&mutation).expect("mutate schema");
        let error = verify_waffo_subscription_schema(&mut transaction, &schema)
            .expect_err("unsafe catalog drift must fail verification");
        assert!(error.to_string().contains(expected_error), "{error}");
        transaction
            .batch_execute("ROLLBACK TO SAVEPOINT mutation")
            .unwrap();
    }
    transaction.rollback().expect("roll back contract-8 test");
}
