use lmm_db_migrate::forward_schema::verify_account_balance_access_schema;
use postgres::{Client, NoTls};

const MIGRATION_SQL: &str = include_str!("../../../migrations/0010_account_balance_access.sql");

#[test]
#[ignore = "requires native PostgreSQL and LMM_TEST_DATABASE_URL"]
fn account_balance_access_migration_is_additive_idempotent_and_default_denied() {
    let database_url = std::env::var("LMM_TEST_DATABASE_URL").expect("test database URL");
    let schema = format!("lmm_account_balance_access_{}", std::process::id());
    let mut client = Client::connect(&database_url, NoTls).expect("connect to test PostgreSQL");
    let mut transaction = client.transaction().expect("start test transaction");
    let sql = MIGRATION_SQL.replace("__LMM_APP_SCHEMA__", &format!("\"{schema}\""));
    transaction
        .batch_execute(&format!(
            "CREATE SCHEMA {schema}; CREATE TABLE {schema}.tokens (id BIGINT PRIMARY KEY); {sql}"
        ))
        .expect("apply account-balance migration");
    verify_account_balance_access_schema(&mut transaction, &schema)
        .expect("account-balance schema contract");
    transaction
        .batch_execute(&sql)
        .expect("account-balance migration is idempotent");
    verify_account_balance_access_schema(&mut transaction, &schema)
        .expect("account-balance schema contract after replay");
    let column = transaction
        .query_one(
            &format!(
                "SELECT column_default, is_nullable FROM information_schema.columns \
                 WHERE table_schema = '{schema}' AND table_name = 'tokens' \
                   AND column_name = 'account_balance_read'"
            ),
            &[],
        )
        .expect("account-balance column");
    assert_eq!(column.get::<_, Option<String>>(0).as_deref(), Some("false"));
    assert_eq!(column.get::<_, String>(1), "NO");
    transaction
        .execute(&format!("INSERT INTO {schema}.tokens (id) VALUES (1)"), &[])
        .expect("insert default token");
    assert!(
        !transaction
            .query_one(
                &format!("SELECT account_balance_read FROM {schema}.tokens"),
                &[],
            )
            .expect("read default token")
            .get::<_, bool>(0)
    );
    transaction.rollback().expect("roll back migration test");
}
