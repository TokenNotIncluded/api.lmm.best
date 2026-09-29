use lmm_db_migrate::forward_schema::verify_token_management_schema;
use postgres::{Client, NoTls};

#[test]
#[ignore = "requires isolated PostgreSQL and LMM_TEST_DATABASE_URL"]
fn token_management_schema_preserves_rows_and_rejects_weakened_guards() {
    let database_url = std::env::var("LMM_TEST_DATABASE_URL").expect("test database URL");
    let mut client = Client::connect(&database_url, NoTls).expect("PostgreSQL");
    let mut transaction = client.transaction().expect("transaction");
    let schema = format!("lmm_token_management_contract_{}", std::process::id());
    transaction
        .batch_execute(&format!(
            "CREATE SCHEMA {schema}; \
             CREATE TABLE {schema}.tokens ( \
               id BIGSERIAL PRIMARY KEY, name VARCHAR(255) NOT NULL, \
               deleted_at BIGINT, unlimited_quota BOOLEAN NOT NULL DEFAULT FALSE, \
               expired_time BIGINT NOT NULL DEFAULT 0, \
               \"group\" VARCHAR(64) NOT NULL DEFAULT 'default' \
             ); \
             INSERT INTO {schema}.tokens(name) VALUES ('用户的初始令牌'); \
             INSERT INTO {schema}.tokens(name,unlimited_quota,expired_time,\"group\") \
               VALUES ('drawing-image-2',TRUE,-1,'image-2'); \
             INSERT INTO {schema}.tokens(name) VALUES ('managed token'); \
             INSERT INTO {schema}.tokens(name,deleted_at) VALUES ('已删除的初始令牌',1)"
        ))
        .unwrap();

    let sql = include_str!("../../../migrations/0016_token_management.sql")
        .replace("__LMM_APP_SCHEMA__", &schema);
    transaction.batch_execute(&sql).unwrap();
    verify_token_management_schema(&mut transaction, &schema).unwrap();

    let sources: Vec<String> = transaction
        .query(
            &format!("SELECT creation_source FROM {schema}.tokens ORDER BY id"),
            &[],
        )
        .unwrap()
        .into_iter()
        .map(|row| row.get(0))
        .collect();
    assert_eq!(
        sources,
        ["system", "drawing_mcp", "manual", "manual"].map(str::to_owned)
    );
    transaction
        .batch_execute(&format!(
            "UPDATE {schema}.tokens SET oauth_managed=TRUE,one_time_reveal=TRUE, \
             creation_source='assistant_runtime' WHERE id=3"
        ))
        .unwrap();
    transaction.batch_execute(&sql).unwrap();
    verify_token_management_schema(&mut transaction, &schema).unwrap();
    let managed = transaction
        .query_one(
            &format!(
                "SELECT oauth_managed,one_time_reveal,creation_source \
                 FROM {schema}.tokens WHERE id=3"
            ),
            &[],
        )
        .unwrap();
    assert!(managed.get::<_, bool>(0));
    assert!(managed.get::<_, bool>(1));
    assert_eq!(managed.get::<_, String>(2), "assistant_runtime");

    for fault in [
        format!("ALTER TABLE {schema}.tokens ALTER COLUMN one_time_reveal DROP NOT NULL"),
        format!("ALTER TABLE {schema}.tokens ALTER COLUMN oauth_managed SET DEFAULT TRUE"),
        format!("ALTER TABLE {schema}.tokens ALTER COLUMN creation_source TYPE VARCHAR(64)"),
        format!("DROP INDEX {schema}.idx_tokens_creation_source"),
        format!("DROP INDEX {schema}.idx_tokens_oauth_managed"),
    ] {
        transaction.batch_execute("SAVEPOINT fault").unwrap();
        transaction.batch_execute(&fault).unwrap();
        assert!(
            verify_token_management_schema(&mut transaction, &schema).is_err(),
            "accepted: {fault}"
        );
        transaction
            .batch_execute("ROLLBACK TO SAVEPOINT fault; RELEASE SAVEPOINT fault")
            .unwrap();
    }
    transaction.rollback().unwrap();
}
