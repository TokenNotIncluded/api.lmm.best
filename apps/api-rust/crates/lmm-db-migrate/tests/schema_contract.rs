use std::{fs, path::Path};

use lmm_db_migrate::{
    contract::{ContractError, ContractInstallOutcome, install_or_verify},
    forward_schema::{verify_company_billing_profile_schema, verify_subscription_reset_schema},
    release::{
        CompatibilityRange, MANDATORY_COMPONENT_NAMES, ReleaseBinding, Sha256Digest, Version,
    },
};
use postgres::{Client, NoTls, Transaction};
use sha2::{Digest, Sha256};

#[test]
#[ignore = "requires native PostgreSQL and LMM_TEST_DATABASE_URL"]
fn contract_ledger_should_be_transactional_idempotent_and_fail_closed() {
    let database_url = std::env::var("LMM_TEST_DATABASE_URL").expect("test database URL");
    let migration =
        Path::new(env!("CARGO_MANIFEST_DIR")).join("../../migrations/0001_schema_contract.sql");
    let contract_hash = file_hash(&migration);
    let schema = format!("lmm_contract_{}", std::process::id());
    let mut client = Client::connect(&database_url, NoTls).expect("connect to test PostgreSQL");

    let mut transaction = client.transaction().expect("start test transaction");
    create_schema(&mut transaction, &schema);
    let first = binding(1, &contract_hash, "release-1", 'b');
    assert_eq!(
        install_or_verify(&mut transaction, &schema, &migration, &first)
            .expect("install first contract"),
        ContractInstallOutcome::Installed
    );
    assert_eq!(
        install_or_verify(&mut transaction, &schema, &migration, &first)
            .expect("exact reapplication is valid"),
        ContractInstallOutcome::AlreadyApplied
    );
    assert_eq!(ledger_count(&mut transaction, &schema), 1);
    let search_path: String = transaction
        .query_one("SHOW search_path", &[])
        .expect("read hardened search_path")
        .get(0);
    assert_eq!(search_path, "pg_catalog");

    let changed_release = binding(1, &contract_hash, "release-1", 'c');
    assert!(matches!(
        install_or_verify(&mut transaction, &schema, &migration, &changed_release),
        Err(ContractError::ReleaseIdentityConflict)
    ));

    let directory = tempfile::tempdir().expect("create temporary directory");
    let changed_migration = directory.path().join("0001_changed.sql");
    let sql = fs::read_to_string(&migration).expect("read contract migration");
    fs::write(&changed_migration, format!("{sql}\n-- changed artifact\n"))
        .expect("write changed contract migration");
    let conflicting = binding(1, &file_hash(&changed_migration), "release-2", 'd');
    assert!(matches!(
        install_or_verify(&mut transaction, &schema, &changed_migration, &conflicting),
        Err(ContractError::ContractIdentityConflict)
    ));

    let second = binding(2, &contract_hash, "release-2", 'e');
    assert_eq!(
        install_or_verify(&mut transaction, &schema, &migration, &second)
            .expect("advance to next contract"),
        ContractInstallOutcome::Advanced
    );
    assert!(matches!(
        install_or_verify(&mut transaction, &schema, &migration, &first),
        Err(ContractError::Downgrade)
    ));
    transaction.rollback().expect("roll back contract test");

    let partial_schema = format!("lmm_contract_partial_{}", std::process::id());
    let mut transaction = client.transaction().expect("start partial-state test");
    create_schema(&mut transaction, &partial_schema);
    transaction
        .batch_execute(&format!(
            "CREATE TABLE {}.lmm_schema_contract (singleton boolean)",
            quote_ident(&partial_schema)
        ))
        .expect("create partial ledger state");
    assert!(matches!(
        install_or_verify(&mut transaction, &partial_schema, &migration, &first),
        Err(ContractError::UnknownState)
    ));
    transaction
        .rollback()
        .expect("roll back partial-state test");
}

#[test]
#[ignore = "requires native PostgreSQL and LMM_TEST_DATABASE_URL"]
fn contract_ledger_should_reject_deleted_history_and_conflicting_ranges() {
    let database_url = std::env::var("LMM_TEST_DATABASE_URL").expect("test database URL");
    let migration =
        Path::new(env!("CARGO_MANIFEST_DIR")).join("../../migrations/0001_schema_contract.sql");
    let contract_hash = file_hash(&migration);
    let mut client = Client::connect(&database_url, NoTls).expect("connect to test PostgreSQL");

    let gap_schema = format!("lmm_contract_gap_{}", std::process::id());
    let mut transaction = client.transaction().expect("start history-gap test");
    create_schema(&mut transaction, &gap_schema);
    let first = binding(1, &contract_hash, "gap-release-1", 'a');
    let second = binding(2, &contract_hash, "gap-release-2", 'b');
    install_or_verify(&mut transaction, &gap_schema, &migration, &first)
        .expect("install first contract");
    install_or_verify(&mut transaction, &gap_schema, &migration, &second)
        .expect("install second contract");
    transaction
        .execute(
            &format!(
                "DELETE FROM {}.lmm_schema_release_ledger WHERE contract_id = 1",
                quote_ident(&gap_schema)
            ),
            &[],
        )
        .expect("delete historical contract rows");
    assert!(matches!(
        install_or_verify(&mut transaction, &gap_schema, &migration, &second),
        Err(ContractError::UnknownState)
    ));
    transaction.rollback().expect("roll back history-gap test");

    let range_schema = format!("lmm_contract_range_{}", std::process::id());
    let mut transaction = client
        .transaction()
        .expect("start compatibility-conflict test");
    create_schema(&mut transaction, &range_schema);
    let first = binding(1, &contract_hash, "range-release-1", 'c');
    install_or_verify(&mut transaction, &range_schema, &migration, &first)
        .expect("install first contract");
    transaction
        .execute(
            &format!(
                "INSERT INTO {}.lmm_schema_release_ledger \
                 (release_id, release_sha256, contract_id, contract_sha256, \
                  min_reader_version, max_reader_version, min_writer_version, \
                  max_writer_version, component_hashes) \
                 SELECT 'range-release-conflict', pg_catalog.repeat('d', 64), contract_id, \
                        contract_sha256, min_reader_version, max_reader_version + 1, \
                        min_writer_version, max_writer_version, component_hashes \
                 FROM {}.lmm_schema_release_ledger WHERE release_id = 'range-release-1'",
                quote_ident(&range_schema),
                quote_ident(&range_schema)
            ),
            &[],
        )
        .expect("insert conflicting compatibility row");
    assert!(matches!(
        install_or_verify(&mut transaction, &range_schema, &migration, &first),
        Err(ContractError::UnknownState)
    ));
    transaction
        .rollback()
        .expect("roll back compatibility-conflict test");
}

#[test]
#[ignore = "requires native PostgreSQL and LMM_TEST_DATABASE_URL"]
fn first_contract_should_start_at_one() {
    let database_url = std::env::var("LMM_TEST_DATABASE_URL").expect("test database URL");
    let migration =
        Path::new(env!("CARGO_MANIFEST_DIR")).join("../../migrations/0001_schema_contract.sql");
    let contract_hash = file_hash(&migration);
    let schema = format!("lmm_contract_initial_{}", std::process::id());
    let mut client = Client::connect(&database_url, NoTls).expect("connect to test PostgreSQL");
    let mut transaction = client.transaction().expect("start initial-contract test");
    create_schema(&mut transaction, &schema);
    let second = binding(2, &contract_hash, "release-2", 'e');
    assert!(matches!(
        install_or_verify(&mut transaction, &schema, &migration, &second),
        Err(ContractError::UnknownState)
    ));
    assert_eq!(ledger_count_if_present(&mut transaction, &schema), None);
    transaction
        .rollback()
        .expect("roll back initial-contract test");
}

#[test]
#[ignore = "requires native PostgreSQL and LMM_TEST_DATABASE_URL"]
fn contract_seven_verifier_rejects_catalog_spoofs_and_cascades_profile_deletion() {
    let database_url = std::env::var("LMM_TEST_DATABASE_URL").expect("test database URL");
    let schema = format!("lmm_contract_seven_{}", std::process::id());
    let quoted_schema = quote_ident(&schema);
    let migration = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../migrations/0007_company_billing_profile.sql");
    let sql = fs::read_to_string(migration)
        .expect("read contract-7 migration")
        .replace("__LMM_APP_SCHEMA__", &quoted_schema);
    let mut client = Client::connect(&database_url, NoTls).expect("connect to test PostgreSQL");
    let mut transaction = client.transaction().expect("start contract-7 test");
    create_schema(&mut transaction, &schema);
    transaction
        .batch_execute(&format!(
            "CREATE TABLE {quoted_schema}.users (id BIGINT PRIMARY KEY, legacy_id BIGINT UNIQUE); \
             CREATE TABLE {quoted_schema}.shadow_users (id BIGINT PRIMARY KEY); \
             CREATE TABLE {quoted_schema}.top_ups (id BIGINT PRIMARY KEY); \
             CREATE TABLE {quoted_schema}.subscription_orders (id BIGINT PRIMARY KEY)"
        ))
        .expect("create contract-7 prerequisites");
    transaction
        .batch_execute(&sql)
        .expect("apply contract-7 migration");
    transaction
        .batch_execute(&sql)
        .expect("reapply contract-7 migration");
    verify_company_billing_profile_schema(&mut transaction, &schema)
        .expect("valid contract-7 schema");

    let mutations = [
        (
            "company_billing_profiles_wrong_fkey",
            "user_id",
            "users",
            "id",
            "CASCADE",
        ),
        (
            "company_billing_profiles_user_id_fkey",
            "created_at",
            "users",
            "id",
            "CASCADE",
        ),
        (
            "company_billing_profiles_user_id_fkey",
            "user_id",
            "shadow_users",
            "id",
            "CASCADE",
        ),
        (
            "company_billing_profiles_user_id_fkey",
            "user_id",
            "users",
            "legacy_id",
            "CASCADE",
        ),
        (
            "company_billing_profiles_user_id_fkey",
            "user_id",
            "users",
            "id",
            "RESTRICT",
        ),
    ];
    for (name, source_column, target_table, target_column, delete_action) in mutations {
        transaction
            .batch_execute(&format!(
                "ALTER TABLE {quoted_schema}.company_billing_profiles \
                   DROP CONSTRAINT company_billing_profiles_user_id_fkey; \
                 ALTER TABLE {quoted_schema}.company_billing_profiles \
                   ADD CONSTRAINT {name} FOREIGN KEY ({source_column}) \
                   REFERENCES {quoted_schema}.{target_table}({target_column}) ON DELETE {delete_action} \
                   DEFERRABLE INITIALLY DEFERRED"
            ))
            .expect("install mutated company-profile foreign key");
        let error = verify_company_billing_profile_schema(&mut transaction, &schema)
            .expect_err("mutated foreign key must fail closed");
        assert!(error.to_string().contains("foreign key mismatch"));
        transaction
            .batch_execute(&format!(
                "ALTER TABLE {quoted_schema}.company_billing_profiles DROP CONSTRAINT {name}; \
                 ALTER TABLE {quoted_schema}.company_billing_profiles \
                   ADD CONSTRAINT company_billing_profiles_user_id_fkey FOREIGN KEY (user_id) \
                   REFERENCES {quoted_schema}.users(id) ON DELETE CASCADE \
                   DEFERRABLE INITIALLY DEFERRED"
            ))
            .expect("restore company-profile foreign key");
    }

    transaction
        .batch_execute(&format!(
            "ALTER TABLE {quoted_schema}.company_billing_profiles \
               DROP CONSTRAINT company_billing_profiles_user_id_fkey; \
             ALTER TABLE {quoted_schema}.company_billing_profiles \
               ADD CONSTRAINT company_billing_profiles_user_id_fkey FOREIGN KEY (user_id) \
               REFERENCES {quoted_schema}.users(id) ON DELETE CASCADE"
        ))
        .expect("install non-deferrable company-profile foreign key");
    let error = verify_company_billing_profile_schema(&mut transaction, &schema)
        .expect_err("non-deferrable foreign key must fail closed");
    assert!(error.to_string().contains("foreign key mismatch"));
    transaction
        .batch_execute(&format!(
            "ALTER TABLE {quoted_schema}.company_billing_profiles \
               DROP CONSTRAINT company_billing_profiles_user_id_fkey; \
             ALTER TABLE {quoted_schema}.company_billing_profiles \
               ADD CONSTRAINT company_billing_profiles_user_id_fkey FOREIGN KEY (user_id) \
               REFERENCES {quoted_schema}.users(id) ON DELETE CASCADE \
               DEFERRABLE INITIALLY DEFERRED"
        ))
        .expect("restore deferred company-profile foreign key");

    transaction
        .batch_execute(&format!(
            "ALTER TABLE {quoted_schema}.company_billing_profiles \
               DROP CONSTRAINT company_billing_profiles_pkey; \
             CREATE UNIQUE INDEX company_billing_profiles_pkey \
               ON {quoted_schema}.company_billing_profiles(user_id, (country || ''))"
        ))
        .expect("install expression-index primary-key spoof");
    let error = verify_company_billing_profile_schema(&mut transaction, &schema)
        .expect_err("expression index must not satisfy primary-key contract");
    assert!(error.to_string().contains("primary key mismatch"));
    transaction
        .batch_execute(&format!(
            "DROP INDEX {quoted_schema}.company_billing_profiles_pkey; \
             ALTER TABLE {quoted_schema}.company_billing_profiles \
               ADD CONSTRAINT company_billing_profiles_pkey PRIMARY KEY (user_id) INCLUDE (country)"
        ))
        .expect("install included-column primary key");
    let error = verify_company_billing_profile_schema(&mut transaction, &schema)
        .expect_err("included columns must not satisfy primary-key contract");
    assert!(error.to_string().contains("primary key mismatch"));

    transaction
        .batch_execute(&format!(
            "INSERT INTO {quoted_schema}.users (id, legacy_id) VALUES (7, 70); \
             INSERT INTO {quoted_schema}.company_billing_profiles \
                (user_id,country,is_business,postcode,state,business_name,tax_id,use_for_invoices,created_at,updated_at) \
             VALUES (7,'US',true,'','','fixture-business','fixture-tax',true,1,1); \
             DELETE FROM {quoted_schema}.users WHERE id=7"
        ))
        .expect("delete profile owner");
    let remaining: i64 = transaction
        .query_one(
            &format!("SELECT count(*) FROM {quoted_schema}.company_billing_profiles"),
            &[],
        )
        .expect("count profiles after owner deletion")
        .get(0);
    assert_eq!(
        remaining, 0,
        "owner deletion must physically remove invoice identity"
    );

    transaction.rollback().expect("roll back contract-7 test");
}

#[test]
#[ignore = "requires native PostgreSQL and LMM_TEST_DATABASE_URL"]
fn contract_seven_defers_copy_order_until_commit_and_cascades_owner_deletion() {
    let database_url = std::env::var("LMM_TEST_DATABASE_URL").expect("test database URL");
    let schema = format!("lmm_contract_seven_deferred_{}", std::process::id());
    let quoted_schema = quote_ident(&schema);
    let migration = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../migrations/0007_company_billing_profile.sql");
    let sql = fs::read_to_string(migration)
        .expect("read contract-7 migration")
        .replace("__LMM_APP_SCHEMA__", &quoted_schema);
    let mut client = Client::connect(&database_url, NoTls).expect("connect to test PostgreSQL");
    client
        .batch_execute(&format!(
            "CREATE SCHEMA {quoted_schema}; \
             CREATE TABLE {quoted_schema}.users (id BIGINT PRIMARY KEY); \
             CREATE TABLE {quoted_schema}.top_ups (id BIGINT PRIMARY KEY); \
             CREATE TABLE {quoted_schema}.subscription_orders (id BIGINT PRIMARY KEY); \
             {sql}"
        ))
        .expect("install contract-7 schema");

    let mut missing_owner = client.transaction().expect("start missing-owner copy");
    missing_owner
        .batch_execute(&format!(
            "INSERT INTO {quoted_schema}.company_billing_profiles \
               (user_id,country,is_business,use_for_invoices,created_at,updated_at) \
             VALUES (8,'US',false,false,1,1)"
        ))
        .expect("initially deferred foreign key must allow child-first copy");
    assert!(
        missing_owner.commit().is_err(),
        "deferred foreign key must reject an unresolved owner at commit"
    );

    let mut dependency_order = client.transaction().expect("start dependency-order copy");
    dependency_order
        .batch_execute(&format!(
            "INSERT INTO {quoted_schema}.company_billing_profiles \
               (user_id,country,is_business,business_name,tax_id,use_for_invoices,created_at,updated_at) \
             VALUES (9,'US',true,'fixture-business','fixture-tax',true,1,1); \
             INSERT INTO {quoted_schema}.users (id) VALUES (9)"
        ))
        .expect("copy child before owner in one transaction");
    dependency_order
        .commit()
        .expect("owner inserted later in the transaction must satisfy the deferred foreign key");

    client
        .execute(
            &format!("DELETE FROM {quoted_schema}.users WHERE id=9"),
            &[],
        )
        .expect("delete copied owner");
    let remaining: i64 = client
        .query_one(
            &format!("SELECT count(*) FROM {quoted_schema}.company_billing_profiles"),
            &[],
        )
        .expect("count profiles after cascade")
        .get(0);
    assert_eq!(
        remaining, 0,
        "owner deletion must physically remove billing PII"
    );
    client
        .batch_execute(&format!("DROP SCHEMA {quoted_schema} CASCADE"))
        .expect("drop contract-7 test schema");
}

#[test]
#[ignore = "requires native PostgreSQL and LMM_TEST_DATABASE_URL"]
fn contract_six_verifier_rejects_wrong_default_and_index_columns() {
    let database_url = std::env::var("LMM_TEST_DATABASE_URL").expect("test database URL");
    let schema = format!("lmm_contract_six_{}", std::process::id());
    let quoted_schema = quote_ident(&schema);
    let migration = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../migrations/0006_subscription_reset_system.sql");
    let sql = fs::read_to_string(migration)
        .expect("read contract-6 migration")
        .replace("__LMM_APP_SCHEMA__", &quoted_schema);
    let mut client = Client::connect(&database_url, NoTls).expect("connect to test PostgreSQL");
    let mut transaction = client.transaction().expect("start test transaction");
    transaction
        .batch_execute(&format!(
            "CREATE SCHEMA {quoted_schema}; CREATE TABLE {quoted_schema}.subscription_plans (id BIGINT PRIMARY KEY)"
        ))
        .expect("create contract-6 prerequisite");
    transaction
        .batch_execute(&sql)
        .expect("apply contract-6 migration");
    verify_subscription_reset_schema(&mut transaction, &schema).expect("valid contract-6 schema");

    transaction
        .batch_execute(&format!(
            "ALTER TABLE {quoted_schema}.subscription_plans ALTER COLUMN archived_at SET DEFAULT 10"
        ))
        .expect("replace archived default");
    let error = verify_subscription_reset_schema(&mut transaction, &schema)
        .expect_err("nonzero archived default must fail");
    assert!(error.to_string().contains("subscription_plans.archived_at"));
    transaction
        .batch_execute(&format!(
            "ALTER TABLE {quoted_schema}.subscription_plans ALTER COLUMN archived_at SET DEFAULT 0; \
             DROP INDEX {quoted_schema}.idx_subscription_reset_operations_preview_token; \
             CREATE UNIQUE INDEX idx_subscription_reset_operations_preview_token \
             ON {quoted_schema}.subscription_reset_operations(actor_user_id)"
        ))
        .expect("replace reset operation index");
    let error = verify_subscription_reset_schema(&mut transaction, &schema)
        .expect_err("wrong index columns must fail");
    assert!(
        error
            .to_string()
            .contains("idx_subscription_reset_operations_preview_token")
    );
    transaction
        .batch_execute(&format!(
            "DROP INDEX {quoted_schema}.idx_subscription_reset_operations_preview_token; \
             CREATE UNIQUE INDEX idx_subscription_reset_operations_preview_token \
             ON {quoted_schema}.subscription_reset_operations(preview_token, (operation_id || ''))"
        ))
        .expect("replace reset operation index with an expression key");
    let error = verify_subscription_reset_schema(&mut transaction, &schema)
        .expect_err("expression key must not be ignored by index verification");
    assert!(
        error
            .to_string()
            .contains("idx_subscription_reset_operations_preview_token")
    );
    transaction
        .batch_execute(&format!(
            "DROP INDEX {quoted_schema}.idx_subscription_reset_operations_preview_token; \
             CREATE UNIQUE INDEX idx_subscription_reset_operations_preview_token \
             ON {quoted_schema}.subscription_reset_operations(preview_token) INCLUDE (operation_id)"
        ))
        .expect("replace reset operation index with an included column");
    let error = verify_subscription_reset_schema(&mut transaction, &schema)
        .expect_err("included columns must not satisfy the exact index contract");
    assert!(
        error
            .to_string()
            .contains("idx_subscription_reset_operations_preview_token")
    );
    transaction
        .batch_execute(&format!(
            "DROP INDEX {quoted_schema}.idx_subscription_reset_operations_preview_token; \
             CREATE UNIQUE INDEX idx_subscription_reset_operations_preview_token \
             ON {quoted_schema}.subscription_reset_operations(preview_token); \
             ALTER TABLE {quoted_schema}.subscription_reset_previews \
             DROP CONSTRAINT subscription_reset_previews_pkey"
        ))
        .expect("replace preview primary key");
    let error = verify_subscription_reset_schema(&mut transaction, &schema)
        .expect_err("missing preview primary key must fail");
    assert!(error.to_string().contains("subscription_reset_previews"));
    transaction
        .batch_execute(&format!(
            "ALTER TABLE {quoted_schema}.subscription_reset_previews ADD PRIMARY KEY (token); \
             ALTER TABLE {quoted_schema}.subscription_reset_vouchers ALTER COLUMN id DROP DEFAULT"
        ))
        .expect("remove voucher sequence default");
    let error = verify_subscription_reset_schema(&mut transaction, &schema)
        .expect_err("missing voucher sequence default must fail");
    assert!(error.to_string().contains("subscription_reset_vouchers.id"));
    transaction
        .batch_execute(&format!(
            "CREATE SEQUENCE {quoted_schema}.wrong_voucher_id_seq; \
             ALTER TABLE {quoted_schema}.subscription_reset_vouchers ALTER COLUMN id \
             SET DEFAULT nextval('{quoted_schema}.wrong_voucher_id_seq'::regclass) + \
                         nextval('{quoted_schema}.subscription_reset_vouchers_id_seq'::regclass) * 0"
        ))
        .expect("replace voucher sequence expression");
    let error = verify_subscription_reset_schema(&mut transaction, &schema)
        .expect_err("compound voucher sequence default must fail");
    assert!(error.to_string().contains("subscription_reset_vouchers.id"));
    transaction
        .batch_execute(&format!(
            "ALTER TABLE {quoted_schema}.subscription_reset_vouchers ALTER COLUMN id \
             SET DEFAULT nextval('{quoted_schema}.subscription_reset_vouchers_id_seq'::regclass); \
             ALTER TABLE {quoted_schema}.subscription_reset_vouchers ALTER COLUMN status SET DEFAULT 'invalid'"
        ))
        .expect("replace voucher status default");
    let error = verify_subscription_reset_schema(&mut transaction, &schema)
        .expect_err("wrong voucher status default must fail");
    assert!(
        error
            .to_string()
            .contains("subscription_reset_vouchers.status")
    );
    transaction
        .batch_execute(&format!(
            "ALTER TABLE {quoted_schema}.subscription_reset_vouchers ALTER COLUMN status SET DEFAULT 'available'; \
             DROP INDEX {quoted_schema}.idx_subscription_reset_vouchers_status; \
             CREATE INDEX idx_subscription_reset_vouchers_status \
             ON {quoted_schema}.subscription_reset_vouchers(plan_id)"
        ))
        .expect("replace voucher status index");
    let error = verify_subscription_reset_schema(&mut transaction, &schema)
        .expect_err("wrong secondary index columns must fail");
    assert!(
        error
            .to_string()
            .contains("idx_subscription_reset_vouchers_status")
    );
    transaction
        .batch_execute(&format!(
            "DROP INDEX {quoted_schema}.idx_subscription_reset_vouchers_status; \
             ALTER TABLE {quoted_schema}.subscription_reset_vouchers \
             ADD CONSTRAINT idx_subscription_reset_vouchers_status EXCLUDE USING btree (status WITH =)"
        ))
        .expect("replace voucher status index with exclusion constraint");
    let error = verify_subscription_reset_schema(&mut transaction, &schema)
        .expect_err("exclusion constraint must not satisfy a normal index contract");
    assert!(
        error
            .to_string()
            .contains("idx_subscription_reset_vouchers_status")
    );
    transaction.rollback().expect("roll back contract-6 test");
}

fn binding(
    contract_id: u64,
    contract_hash: &str,
    release_id: &str,
    release_hash_character: char,
) -> ReleaseBinding {
    let version = Version::new(contract_id, "contract_id").expect("valid contract version");
    let compatibility = CompatibilityRange::new(version, version, "compatibility")
        .expect("valid compatibility range");
    ReleaseBinding::new(
        version,
        Sha256Digest::parse(contract_hash, "contract").expect("valid contract hash"),
        compatibility,
        compatibility,
        release_id.parse().expect("valid release identifier"),
        repeated_hash(release_hash_character),
        MANDATORY_COMPONENT_NAMES.iter().map(|name| {
            format!("{name}={}", "f".repeat(64))
                .parse()
                .expect("valid component hash")
        }),
    )
    .expect("valid release binding")
}

fn repeated_hash(character: char) -> Sha256Digest {
    Sha256Digest::parse(&character.to_string().repeat(64), "test").expect("valid test hash")
}

fn file_hash(path: &Path) -> String {
    format!(
        "{:x}",
        Sha256::digest(fs::read(path).expect("read hashed file"))
    )
}

fn create_schema(transaction: &mut Transaction<'_>, schema: &str) {
    transaction
        .batch_execute(&format!("CREATE SCHEMA {}", quote_ident(schema)))
        .expect("create test schema");
}

fn ledger_count(transaction: &mut Transaction<'_>, schema: &str) -> i64 {
    transaction
        .query_one(
            &format!(
                "SELECT count(*)::bigint FROM {}.lmm_schema_release_ledger",
                quote_ident(schema)
            ),
            &[],
        )
        .expect("query ledger count")
        .get(0)
}

fn ledger_count_if_present(transaction: &mut Transaction<'_>, schema: &str) -> Option<i64> {
    transaction
        .query_one(
            r#"
            SELECT CASE WHEN pg_catalog.to_regclass($1) IS NULL THEN NULL ELSE 0::bigint END
            "#,
            &[&format!(
                "{}.lmm_schema_release_ledger",
                quote_ident(schema)
            )],
        )
        .expect("query optional ledger")
        .get(0)
}

fn quote_ident(identifier: &str) -> String {
    format!("\"{}\"", identifier.replace('"', "\"\""))
}
