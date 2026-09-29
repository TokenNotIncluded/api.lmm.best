use lmm_db_migrate::forward_schema::{
    verify_mandatory_announcement_schema, verify_payment_runtime_schema,
};
use postgres::{Client, NoTls};

#[test]
#[ignore = "requires isolated PostgreSQL and LMM_TEST_DATABASE_URL"]
fn announcement_schema_preserves_history_and_rejects_weakened_unique_keys() {
    let database_url = std::env::var("LMM_TEST_DATABASE_URL").expect("test database URL");
    let mut client = Client::connect(&database_url, NoTls).expect("PostgreSQL");
    let mut transaction = client.transaction().expect("transaction");
    let schema = format!("lmm_announcements_contract_{}", std::process::id());
    transaction
        .batch_execute(&format!("CREATE SCHEMA {schema}"))
        .unwrap();
    let sql = include_str!("../../../migrations/0011_mandatory_announcements.sql")
        .replace("__LMM_APP_SCHEMA__", &schema);
    transaction.batch_execute(&sql).unwrap();
    transaction.batch_execute(&format!(
        "INSERT INTO {schema}.announcement_reads(user_id,announcement_id,revision,read_at) VALUES (1,2,'old-revision',3)"
    )).unwrap();
    transaction.batch_execute(&sql).unwrap();
    verify_mandatory_announcement_schema(&mut transaction, &schema).unwrap();
    let read: i64 = transaction.query_one(&format!(
        "SELECT read_at FROM {schema}.announcement_reads WHERE user_id=1 AND announcement_id=2"
    ), &[]).unwrap().get(0);
    assert_eq!(read, 3);
    for fault in [
        format!("DROP INDEX {schema}.idx_announcement_read"),
        format!(
            "DROP INDEX {schema}.idx_announcement_read; CREATE INDEX idx_announcement_read ON {schema}.announcement_reads(user_id,announcement_id,revision)"
        ),
        format!(
            "DROP INDEX {schema}.idx_announcement_read; CREATE UNIQUE INDEX idx_announcement_read ON {schema}.announcement_reads(announcement_id,revision)"
        ),
        format!("ALTER TABLE {schema}.announcement_reads ALTER COLUMN revision TYPE VARCHAR(63)"),
        format!("ALTER TABLE {schema}.announcement_reads ALTER COLUMN read_at DROP NOT NULL"),
        format!("ALTER TABLE {schema}.announcement_reads ALTER COLUMN id DROP DEFAULT"),
    ] {
        transaction.batch_execute("SAVEPOINT fault").unwrap();
        transaction.batch_execute(&fault).unwrap();
        assert!(
            verify_mandatory_announcement_schema(&mut transaction, &schema).is_err(),
            "accepted: {fault}"
        );
        transaction
            .batch_execute("ROLLBACK TO SAVEPOINT fault; RELEASE SAVEPOINT fault")
            .unwrap();
    }
    transaction.rollback().unwrap();
}

#[test]
#[ignore = "requires isolated PostgreSQL and LMM_TEST_DATABASE_URL"]
fn payment_runtime_schema_preserves_orders_and_rejects_weakened_replay_guards() {
    let database_url = std::env::var("LMM_TEST_DATABASE_URL").expect("test database URL");
    let mut client = Client::connect(&database_url, NoTls).expect("PostgreSQL");
    let mut transaction = client.transaction().expect("transaction");
    let schema = format!("lmm_payment_runtime_contract_{}", std::process::id());
    transaction.batch_execute(&format!(
        "CREATE SCHEMA {schema}; \
         CREATE TABLE {schema}.top_ups(id BIGSERIAL PRIMARY KEY,payment_provider VARCHAR(64),status VARCHAR(32)); \
         CREATE TABLE {schema}.users(id BIGINT PRIMARY KEY); \
         INSERT INTO {schema}.top_ups(payment_provider,status) VALUES ('epay','pending'); \
         INSERT INTO {schema}.users VALUES (1)"
    )).unwrap();
    let sql = include_str!("../../../migrations/0012_payment_runtime.sql")
        .replace("__LMM_APP_SCHEMA__", &schema);
    transaction.batch_execute(&sql).unwrap();
    verify_payment_runtime_schema(&mut transaction, &schema).unwrap();
    transaction
        .batch_execute(&format!(
            "UPDATE {schema}.top_ups SET credited_quota=500000,provider_event_id='event-1'; \
         UPDATE {schema}.users SET referral_first_top_up_id=1"
        ))
        .unwrap();
    transaction.batch_execute(&sql).unwrap();
    verify_payment_runtime_schema(&mut transaction, &schema).unwrap();
    let evidence = transaction
        .query_one(
            &format!(
                "SELECT credited_quota,provider_event_id,status FROM {schema}.top_ups WHERE id=1"
            ),
            &[],
        )
        .unwrap();
    assert_eq!(evidence.get::<_, i64>(0), 500000);
    assert_eq!(evidence.get::<_, String>(1), "event-1");
    assert_eq!(evidence.get::<_, String>(2), "pending");
    for fault in [
        format!("DROP INDEX {schema}.idx_topup_provider_event"),
        format!(
            "DROP INDEX {schema}.idx_topup_provider_transaction; CREATE INDEX idx_topup_provider_transaction ON {schema}.top_ups(payment_provider,provider_transaction_id)"
        ),
        format!(
            "DROP INDEX {schema}.idx_topup_provider_event; CREATE UNIQUE INDEX idx_topup_provider_event ON {schema}.top_ups(payment_provider,provider_event_id) WHERE status='success'"
        ),
        format!("ALTER TABLE {schema}.top_ups ALTER COLUMN credited_quota SET DEFAULT 1"),
        format!("ALTER TABLE {schema}.users ALTER COLUMN referral_first_top_up_id DROP NOT NULL"),
        format!("DROP INDEX {schema}.idx_discount_code_reservations_top_up_trade_no"),
        format!("DROP INDEX {schema}.idx_referral_rewards_invitee_id"),
        format!("DROP INDEX {schema}.idx_referral_ledger_entries_event_key"),
        format!("ALTER TABLE {schema}.referral_rewards ALTER COLUMN id DROP DEFAULT"),
    ] {
        transaction.batch_execute("SAVEPOINT fault").unwrap();
        transaction.batch_execute(&fault).unwrap();
        assert!(
            verify_payment_runtime_schema(&mut transaction, &schema).is_err(),
            "accepted: {fault}"
        );
        transaction
            .batch_execute("ROLLBACK TO SAVEPOINT fault; RELEASE SAVEPOINT fault")
            .unwrap();
    }
    transaction.rollback().unwrap();
}
