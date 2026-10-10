use lmm_db_migrate::forward_schema::{
    verify_current_catalog_schema, verify_payment_extensions_schema, verify_relay_settlement_schema,
};
use postgres::{Client, NoTls};

#[test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
fn current_catalog_schema_preserves_ads_and_rejects_weak_replay_or_money_columns() {
    let mut client = Client::connect(
        &std::env::var("LMM_TEST_DATABASE_URL").expect("isolated PostgreSQL"),
        NoTls,
    )
    .unwrap();
    let mut tx = client.transaction().unwrap();
    let schema = format!("catalog_schema_{}", std::process::id());
    tx.batch_execute(&format!("CREATE SCHEMA {schema}"))
        .unwrap();
    let sql = include_str!("../../../migrations/0015_current_catalog.sql")
        .replace("__LMM_APP_SCHEMA__", &schema);
    tx.batch_execute(&sql).unwrap();
    tx.batch_execute(&format!("INSERT INTO {schema}.ai_directory_ads(owner_user_id,name,url,bid_cents,charged_quota,request_id,status,paid_at,expires_at) VALUES(7,'Ad','https://example.com',100,1000,'immutable-request-id','active',1,2592001)")).unwrap();
    tx.batch_execute(&sql).unwrap();
    verify_current_catalog_schema(&mut tx, &schema).unwrap();
    assert_eq!(tx.query_one(&format!("SELECT charged_quota FROM {schema}.ai_directory_ads WHERE request_id='immutable-request-id'"),&[]).unwrap().get::<_,i64>(0),1000);
    for fault in [
        format!("DROP INDEX {schema}.idx_ai_directory_ads_request_id"),
        format!(
            "DROP INDEX {schema}.idx_ai_directory_ads_request_id;CREATE INDEX idx_ai_directory_ads_request_id ON {schema}.ai_directory_ads(request_id)"
        ),
        format!("ALTER TABLE {schema}.ai_directory_ads ALTER COLUMN charged_quota TYPE INTEGER"),
        format!("ALTER TABLE {schema}.ai_directory_ads ALTER COLUMN owner_user_id DROP NOT NULL"),
        format!("ALTER TABLE {schema}.ai_directory_ads ALTER COLUMN hidden_at SET DEFAULT 1"),
    ] {
        tx.batch_execute("SAVEPOINT fault").unwrap();
        tx.batch_execute(&fault).unwrap();
        assert!(
            verify_current_catalog_schema(&mut tx, &schema).is_err(),
            "accepted {fault}"
        );
        tx.batch_execute("ROLLBACK TO SAVEPOINT fault;RELEASE SAVEPOINT fault")
            .unwrap();
    }
    tx.rollback().unwrap();
}

#[test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
fn payment_extensions_preserve_legacy_subscription_currency_width_and_optional_periods() {
    let mut client = Client::connect(
        &std::env::var("LMM_TEST_DATABASE_URL").expect("isolated PostgreSQL"),
        NoTls,
    )
    .unwrap();
    let mut tx = client.transaction().unwrap();
    let schema = format!("payment_extensions_legacy_{}", std::process::id());
    tx.batch_execute(&format!("CREATE SCHEMA {schema};SET LOCAL search_path TO {schema};CREATE TABLE {schema}.subscription_orders(id BIGSERIAL PRIMARY KEY)")).unwrap();
    let legacy = include_str!("../../../migrations/0004_payment_money_contract.sql")
        .replace("public.", &format!("{schema}."));
    tx.batch_execute(&legacy).unwrap();
    tx.batch_execute(&format!("INSERT INTO {schema}.subscription_orders(plan_currency,settlement_currency) VALUES('LEGACY-CURRENCY','LEGACY-CURRENCY')")).unwrap();
    let sql = include_str!("../../../migrations/0013_payment_extensions.sql")
        .replace("__LMM_APP_SCHEMA__", &schema);
    tx.batch_execute(&sql).unwrap();
    tx.batch_execute(&sql).unwrap();
    verify_payment_extensions_schema(&mut tx, &schema).unwrap();
    assert_eq!(
        tx.query_one(
            &format!("SELECT plan_currency FROM {schema}.subscription_orders WHERE id=1"),
            &[]
        )
        .unwrap()
        .get::<_, String>(0),
        "LEGACY-CURRENCY"
    );
    tx.batch_execute(&format!("INSERT INTO {schema}.subscription_payment_events(subscription_order_id,payment_provider,provider_event_id,provider_transaction_id,settlement_currency,settlement_amount_micros) VALUES(1,'stripe','event-1','transaction-1','USD',1000000),(1,'stripe','event-2','transaction-2','USD',1000000)")).unwrap();
    assert_eq!(
        tx.query_one(
            &format!(
                "SELECT COUNT(*) FROM {schema}.subscription_payment_events WHERE period_end IS NULL"
            ),
            &[]
        )
        .unwrap()
        .get::<_, i64>(0),
        2
    );
    for fault in [
        format!(
            "ALTER TABLE {schema}.subscription_orders ALTER COLUMN provider_event_time_millis DROP NOT NULL"
        ),
        format!("DROP INDEX {schema}.idx_subscription_provider_transaction"),
        format!(
            "DROP INDEX {schema}.idx_subscription_order_period;CREATE UNIQUE INDEX idx_subscription_order_period ON {schema}.subscription_payment_events(subscription_order_id,period_start)"
        ),
        format!(
            "ALTER TABLE {schema}.subscription_payment_events ALTER COLUMN period_end SET DEFAULT 0"
        ),
    ] {
        tx.batch_execute("SAVEPOINT fault").unwrap();
        tx.batch_execute(&fault).unwrap();
        assert!(
            verify_payment_extensions_schema(&mut tx, &schema).is_err(),
            "accepted {fault}"
        );
        tx.batch_execute("ROLLBACK TO SAVEPOINT fault;RELEASE SAVEPOINT fault")
            .unwrap();
    }
    tx.rollback().unwrap();
}

#[test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
fn relay_settlement_schema_pins_phase_money_json_and_replay_constraints() {
    let mut client = Client::connect(
        &std::env::var("LMM_TEST_DATABASE_URL").expect("isolated PostgreSQL"),
        NoTls,
    )
    .unwrap();
    let mut tx = client.transaction().unwrap();
    let schema = format!("relay_settlement_schema_{}", std::process::id());
    tx.batch_execute(&format!("CREATE SCHEMA {schema};CREATE TABLE {schema}.user_subscriptions(id BIGINT PRIMARY KEY);CREATE TABLE {schema}.subscription_pre_consume_records(id BIGINT PRIMARY KEY,request_id VARCHAR(64));")).unwrap();
    let sql = include_str!("../../../migrations/0014_relay_settlement.sql")
        .replace("__LMM_APP_SCHEMA__", &schema);
    tx.batch_execute(&sql).unwrap();
    tx.batch_execute(&format!("INSERT INTO {schema}.relay_settlement_records(reservation_id,request_id,user_id,token_id,channel_id,model_name,using_group,is_stream,funding_source,expected_quota,price_snapshot,status,created_at,updated_at) VALUES('reservation','request',1,2,3,'model','default',TRUE,'wallet',100,'{{}}','reserved',1,1)")).unwrap();
    tx.batch_execute(&sql).unwrap();
    verify_relay_settlement_schema(&mut tx, &schema).unwrap();
    assert_eq!(tx.query_one(&format!("SELECT expected_quota FROM {schema}.relay_settlement_records WHERE reservation_id='reservation'"),&[]).unwrap().get::<_,i64>(0),100);
    for fault in [
        format!(
            "ALTER TABLE {schema}.relay_settlement_records DROP CONSTRAINT relay_settlement_records_expected_quota_check"
        ),
        format!(
            "ALTER TABLE {schema}.relay_settlement_records DROP CONSTRAINT relay_settlement_records_check"
        ),
        format!(
            "ALTER TABLE {schema}.relay_settlement_records DROP CONSTRAINT relay_settlement_records_check1"
        ),
        format!(
            "ALTER TABLE {schema}.relay_settlement_records DROP CONSTRAINT relay_settlement_records_price_snapshot_check"
        ),
        format!(
            "ALTER TABLE {schema}.relay_settlement_records DROP CONSTRAINT relay_settlement_records_status_check;ALTER TABLE {schema}.relay_settlement_records ADD CONSTRAINT relay_settlement_records_status_check CHECK (status='reserved' OR TRUE)"
        ),
        format!(
            "DROP INDEX {schema}.idx_relay_settlement_records_active_request;CREATE UNIQUE INDEX idx_relay_settlement_records_active_request ON {schema}.relay_settlement_records(user_id,request_id) WHERE status<>'settled'"
        ),
        format!(
            "ALTER TABLE {schema}.relay_settlement_records DROP CONSTRAINT relay_settlement_records_pkey"
        ),
        format!(
            "ALTER TABLE {schema}.subscription_pre_consume_records ALTER COLUMN wallet_consumed SET DEFAULT 1"
        ),
        format!("ALTER TABLE {schema}.user_subscriptions ALTER COLUMN quota_version DROP NOT NULL"),
    ] {
        tx.batch_execute("SAVEPOINT fault").unwrap();
        tx.batch_execute(&fault).unwrap();
        assert!(
            verify_relay_settlement_schema(&mut tx, &schema).is_err(),
            "accepted {fault}"
        );
        tx.batch_execute("ROLLBACK TO SAVEPOINT fault;RELEASE SAVEPOINT fault")
            .unwrap();
    }
    for invalid in [
        "expected_quota=-1",
        "status='settled'",
        "funding_source='subscription'",
        "price_snapshot='[]'::jsonb",
        "usage_snapshot='[]'::jsonb",
    ] {
        tx.batch_execute("SAVEPOINT invalid_row").unwrap();
        assert!(
            tx.execute(
                &format!("UPDATE {schema}.relay_settlement_records SET {invalid}"),
                &[]
            )
            .is_err(),
            "accepted invalid row {invalid}"
        );
        tx.batch_execute("ROLLBACK TO SAVEPOINT invalid_row;RELEASE SAVEPOINT invalid_row")
            .unwrap();
    }
    tx.rollback().unwrap();
}

#[test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
fn payment_extensions_schema_keeps_nullable_history_and_rejects_weak_idempotency() {
    let mut client = Client::connect(
        &std::env::var("LMM_TEST_DATABASE_URL").expect("isolated PostgreSQL"),
        NoTls,
    )
    .unwrap();
    let mut tx = client.transaction().unwrap();
    let schema = format!("payment_extensions_schema_{}", std::process::id());
    tx.batch_execute(&format!(
        "CREATE SCHEMA {schema}; CREATE TABLE {schema}.subscription_orders(id BIGINT PRIMARY KEY)"
    ))
    .unwrap();
    let sql = include_str!("../../../migrations/0013_payment_extensions.sql")
        .replace("__LMM_APP_SCHEMA__", &schema);
    tx.batch_execute(&sql).unwrap();
    tx.batch_execute(&format!("INSERT INTO {schema}.finance_ledger_entries(entry_type,amount_micros,direction,source_type,occurred_at,created_at,created_by) VALUES('refund',1000,-1,'top_up',1,1,0),('refund',2000,-1,'top_up',2,2,0)")).unwrap();
    tx.batch_execute(&sql).unwrap();
    verify_payment_extensions_schema(&mut tx, &schema).unwrap();
    assert_eq!(
        tx.query_one(
            &format!(
                "SELECT COUNT(*) FROM {schema}.finance_ledger_entries WHERE idempotency_key IS NULL"
            ),
            &[]
        )
        .unwrap()
        .get::<_, i64>(0),
        2
    );
    for fault in [
        format!("DROP INDEX {schema}.idx_finance_ledger_entries_idempotency_key"),
        format!(
            "DROP INDEX {schema}.idx_finance_ledger_entries_idempotency_key;CREATE INDEX idx_finance_ledger_entries_idempotency_key ON {schema}.finance_ledger_entries(idempotency_key)"
        ),
        format!(
            "DROP INDEX {schema}.idx_finance_ledger_entries_idempotency_key;CREATE UNIQUE INDEX idx_finance_ledger_entries_idempotency_key ON {schema}.finance_ledger_entries(idempotency_key) WHERE user_id IS NOT NULL"
        ),
        format!("ALTER TABLE {schema}.finance_ledger_entries ALTER COLUMN direction TYPE BIGINT"),
        format!(
            "ALTER TABLE {schema}.finance_ledger_entries ALTER COLUMN amount_micros DROP NOT NULL"
        ),
        format!(
            "ALTER TABLE {schema}.finance_ledger_entries ALTER COLUMN currency SET DEFAULT 'EUR'"
        ),
    ] {
        tx.batch_execute("SAVEPOINT fault").unwrap();
        tx.batch_execute(&fault).unwrap();
        assert!(
            verify_payment_extensions_schema(&mut tx, &schema).is_err(),
            "accepted {fault}"
        );
        tx.batch_execute("ROLLBACK TO SAVEPOINT fault;RELEASE SAVEPOINT fault")
            .unwrap();
    }
    tx.rollback().unwrap();
}
