use lmm_db_migrate::forward_schema::verify_subscription_amount_snapshots_schema;
use postgres::{Client, NoTls};

const MIGRATION_SQL: &str =
    include_str!("../../../migrations/0018_subscription_amount_snapshots.sql");

type SubscriptionAmountRow = (i64, i64, i64, Option<i64>, Option<i64>);

#[test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
fn subscription_amount_snapshots_preserve_null_zero_and_existing_balances() {
    let mut client = Client::connect(
        &std::env::var("LMM_TEST_DATABASE_URL").expect("isolated PostgreSQL"),
        NoTls,
    )
    .unwrap();
    let mut transaction = client.transaction().unwrap();
    let schema = format!("subscription_amount_snapshots_{}", std::process::id());
    transaction
        .batch_execute(&format!(
            "CREATE SCHEMA {schema}; \
             CREATE TABLE {schema}.user_subscriptions \
                 (id BIGINT PRIMARY KEY,amount_total BIGINT NOT NULL,amount_used BIGINT NOT NULL); \
             INSERT INTO {schema}.user_subscriptions VALUES (1,0,3),(2,100,20)"
        ))
        .unwrap();
    assert!(verify_subscription_amount_snapshots_schema(&mut transaction, &schema).is_err());

    let sql = MIGRATION_SQL.replace("__LMM_APP_SCHEMA__", &schema);
    transaction.batch_execute(&sql).unwrap();
    verify_subscription_amount_snapshots_schema(&mut transaction, &schema).unwrap();
    let legacy = transaction
        .query_one(
            &format!(
                "SELECT amount_total,amount_used,reset_amount,renewal_amount \
                 FROM {schema}.user_subscriptions WHERE id=1"
            ),
            &[],
        )
        .unwrap();
    assert_eq!(legacy.get::<_, i64>(0), 0);
    assert_eq!(legacy.get::<_, i64>(1), 3);
    assert_eq!(legacy.get::<_, Option<i64>>(2), None);
    assert_eq!(legacy.get::<_, Option<i64>>(3), None);

    transaction
        .batch_execute(&format!(
            "UPDATE {schema}.user_subscriptions \
             SET reset_amount=5000000000,renewal_amount=4000000000 WHERE id=2; \
             INSERT INTO {schema}.user_subscriptions \
                 (id,amount_total,amount_used,reset_amount,renewal_amount) VALUES (3,0,0,0,0); \
             INSERT INTO {schema}.user_subscriptions (id,amount_total,amount_used) VALUES (4,0,0)"
        ))
        .unwrap();
    transaction.batch_execute(&sql).unwrap();
    verify_subscription_amount_snapshots_schema(&mut transaction, &schema).unwrap();
    let amounts: Vec<SubscriptionAmountRow> = transaction
        .query(
            &format!(
                "SELECT id,amount_total,amount_used,reset_amount,renewal_amount \
                 FROM {schema}.user_subscriptions ORDER BY id"
            ),
            &[],
        )
        .unwrap()
        .into_iter()
        .map(|row| (row.get(0), row.get(1), row.get(2), row.get(3), row.get(4)))
        .collect();
    assert_eq!(
        amounts,
        [
            (1, 0, 3, None, None),
            (2, 100, 20, Some(5_000_000_000), Some(4_000_000_000)),
            (3, 0, 0, Some(0), Some(0)),
            (4, 0, 0, None, None),
        ]
    );

    for column in ["reset_amount", "renewal_amount"] {
        for fault in [
            format!("ALTER TABLE {schema}.user_subscriptions DROP COLUMN {column}"),
            format!(
                "ALTER TABLE {schema}.user_subscriptions \
                 ALTER COLUMN {column} TYPE INTEGER USING 0"
            ),
            format!(
                "UPDATE {schema}.user_subscriptions SET {column}=COALESCE({column},0); \
                 ALTER TABLE {schema}.user_subscriptions ALTER COLUMN {column} SET NOT NULL"
            ),
            format!("ALTER TABLE {schema}.user_subscriptions ALTER COLUMN {column} SET DEFAULT 0"),
        ] {
            transaction.batch_execute("SAVEPOINT fault").unwrap();
            transaction.batch_execute(&fault).unwrap();
            assert!(
                verify_subscription_amount_snapshots_schema(&mut transaction, &schema).is_err(),
                "accepted {fault}"
            );
            transaction
                .batch_execute("ROLLBACK TO SAVEPOINT fault; RELEASE SAVEPOINT fault")
                .unwrap();
        }
    }
    transaction.rollback().unwrap();
}
