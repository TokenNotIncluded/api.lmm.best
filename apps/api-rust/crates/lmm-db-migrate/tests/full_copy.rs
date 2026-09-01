#![cfg(unix)]

use std::{fs, path::Path, process::Command};

use lmm_db_migrate::{
    manifest::{Converter, Manifest},
    migrate::{RehearseOptions, VerifyOptions, rehearse, verify},
    release::{
        CompatibilityRange, MANDATORY_COMPONENT_NAMES, ReleaseBinding, Sha256Digest, Version,
    },
};
use postgres::{Client, NoTls};
use rusqlite::Connection;
use sha2::{Digest, Sha256};

#[test]
#[cfg(unix)]
#[ignore = "requires native PostgreSQL from rehearse-postgres.sh"]
fn full_copy_should_verify_all_tables_and_rollback_both_fault_phases() {
    let database_url = std::env::var("LMM_TEST_DATABASE_URL").expect("test database URL");
    let crate_dir = Path::new(env!("CARGO_MANIFEST_DIR"));
    let manifest_path = crate_dir.join("schema/table-map.json");
    let baseline = crate_dir.join("schema/postgresql-baseline.sql");
    let catalog_sql = crate_dir.join("schema/export-postgres-catalog.sql");
    let contract_migration = crate_dir.join("../../migrations/0001_schema_contract.sql");
    let release = release_binding(&contract_migration, "full-copy-release");
    let manifest = Manifest::load(&manifest_path).unwrap();
    let directory = tempfile::tempdir().unwrap();
    let sqlite = directory.path().join("all-tables.db");
    create_sqlite_fixture(&sqlite, &manifest);
    let fixtures = RehearseFixtures {
        sqlite: &sqlite,
        manifest: &manifest,
        baseline: &baseline,
        catalog_sql: &catalog_sql,
        contract_migration: &contract_migration,
        release: &release,
    };

    let report = rehearse(&options(&fixtures, "lmm_copy_success", &database_url)).unwrap();
    assert_eq!(report.table_count, 34);
    assert_eq!(report.sequence_count, 29);
    assert_eq!(report.financial_aggregates.len(), 15);
    assert!(
        verify(&VerifyOptions {
            sqlite: &sqlite,
            manifest: &manifest,
            schema: "lmm_copy_success",
            database_url: &database_url,
            release: &release,
        })
        .is_ok()
    );
    assert_cli_success_audit(
        &sqlite,
        directory.path(),
        &database_url,
        &contract_migration,
    );
    assert_independent_oracle(&sqlite, &database_url);
    assert!(rehearse(&options(&fixtures, "lmm_copy_success", &database_url)).is_err());

    let mut copy_fault = options(&fixtures, "lmm_copy_fault", &database_url);
    copy_fault.fault_after_table = Some("channels");
    assert!(rehearse(&copy_fault).is_err());
    assert!(!schema_exists(&database_url, "lmm_copy_fault"));

    let mut verify_fault = options(&fixtures, "lmm_verify_fault", &database_url);
    verify_fault.fault_before_verify = true;
    assert!(rehearse(&verify_fault).is_err());
    assert!(!schema_exists(&database_url, "lmm_verify_fault"));
    assert_sensitive_failures_are_redacted(
        &sqlite,
        directory.path(),
        crate_dir,
        &database_url,
        &contract_migration,
    );
}

#[cfg(unix)]
fn assert_sensitive_failures_are_redacted(
    source: &Path,
    directory: &Path,
    crate_dir: &Path,
    database_url: &str,
    contract_migration: &Path,
) {
    let cases = [
        (
            "timestamp",
            "UPDATE auth_flows SET created_at='SECRET_TIMESTAMP_NOT_VALID'",
            "lmm_secret_timestamp",
        ),
        (
            "decimal",
            "UPDATE subscription_plans SET price_amount='SECRET_DECIMAL_NOT_VALID'",
            "lmm_secret_decimal",
        ),
        (
            "copy",
            "UPDATE options SET value='SECRET_COPY' || char(0) WHERE key='control'",
            "lmm_secret_copy",
        ),
    ];
    for (name, mutation, schema) in cases {
        let sqlite = directory.join(format!("{name}.db"));
        fs::copy(source, &sqlite).unwrap();
        let connection = Connection::open(&sqlite).unwrap();
        connection.execute_batch(mutation).unwrap();
        connection.close().unwrap();
        let report = directory.join(format!("{name}.json"));
        let mut command = Command::new(env!("CARGO_BIN_EXE_lmm-db-migrate"));
        command
            .args(["rehearse", "--sqlite"])
            .arg(&sqlite)
            .args(["--manifest"])
            .arg(crate_dir.join("schema/table-map.json"))
            .args(["--baseline"])
            .arg(crate_dir.join("schema/postgresql-baseline.sql"))
            .args(["--catalog-sql"])
            .arg(crate_dir.join("schema/export-postgres-catalog.sql"))
            .args(["--contract-migration"])
            .arg(contract_migration)
            .args(["--schema", schema, "--report"])
            .arg(&report)
            .env("LMM_MIGRATE_DATABASE_URL", database_url);
        add_release_arguments(&mut command, contract_migration, "full-copy-release");
        let output = command.output().unwrap();
        assert!(!output.status.success());
        let stderr = String::from_utf8(output.stderr).unwrap();
        assert!(stderr.starts_with("migration failed: stage=rehearse error_category="));
        assert!(!stderr.contains("SECRET_"));
        assert!(!stderr.contains(database_url));
        let audit = fs::read_to_string(report).unwrap();
        assert!(!audit.contains("SECRET_"));
        assert!(!audit.contains(database_url));
    }
}

struct RehearseFixtures<'a> {
    sqlite: &'a Path,
    manifest: &'a Manifest,
    baseline: &'a Path,
    catalog_sql: &'a Path,
    contract_migration: &'a Path,
    release: &'a ReleaseBinding,
}

fn options<'a>(
    fixtures: &'a RehearseFixtures<'a>,
    schema: &'a str,
    database_url: &'a str,
) -> RehearseOptions<'a> {
    RehearseOptions {
        sqlite: fixtures.sqlite,
        manifest: fixtures.manifest,
        baseline: fixtures.baseline,
        catalog_sql: fixtures.catalog_sql,
        contract_migration: fixtures.contract_migration,
        release: fixtures.release,
        schema,
        database_url,
        fault_after_table: None,
        fault_before_verify: false,
    }
}

fn create_sqlite_fixture(path: &Path, manifest: &Manifest) {
    let connection = Connection::open(path).unwrap();
    for table in &manifest.tables {
        let mut definitions = table
            .columns
            .iter()
            .map(|column| {
                let mut sql = format!("{} {}", quote(&column.name), column.sqlite_type);
                if column.sqlite_not_null {
                    sql.push_str(" NOT NULL");
                }
                if let Some(default) = &column.sqlite_default {
                    sql.push_str(" DEFAULT ");
                    sql.push_str(default);
                }
                sql
            })
            .collect::<Vec<_>>();
        let mut primary = table
            .columns
            .iter()
            .filter(|column| column.pk_position > 0)
            .collect::<Vec<_>>();
        primary.sort_by_key(|column| column.pk_position);
        definitions.push(format!(
            "PRIMARY KEY ({})",
            primary
                .iter()
                .map(|column| quote(&column.name))
                .collect::<Vec<_>>()
                .join(", ")
        ));
        for index in table.sqlite_indexes.iter().filter(|index| {
            index.name.starts_with("sqlite_autoindex_")
                && index.columns
                    != primary
                        .iter()
                        .map(|column| column.name.clone())
                        .collect::<Vec<_>>()
        }) {
            definitions.push(format!(
                "UNIQUE ({})",
                index
                    .columns
                    .iter()
                    .map(|column| quote(column))
                    .collect::<Vec<_>>()
                    .join(", ")
            ));
        }
        connection
            .execute_batch(&format!(
                "CREATE TABLE {} ({})",
                quote(&table.name),
                definitions.join(", ")
            ))
            .unwrap();
        for index in table
            .sqlite_indexes
            .iter()
            .filter(|index| !index.name.starts_with("sqlite_autoindex_"))
        {
            let unique = if index.unique { "UNIQUE " } else { "" };
            let predicate = index
                .predicate
                .as_ref()
                .map(|value| format!(" WHERE {value}"))
                .unwrap_or_default();
            connection
                .execute_batch(&format!(
                    "CREATE {unique}INDEX {} ON {} ({}){predicate}",
                    quote(&index.name),
                    quote(&table.name),
                    index
                        .columns
                        .iter()
                        .map(|column| quote(column))
                        .collect::<Vec<_>>()
                        .join(", ")
                ))
                .unwrap();
        }
        if table.name == "vendors" {
            continue;
        }
        let columns = table
            .columns
            .iter()
            .map(|column| quote(&column.name))
            .collect::<Vec<_>>()
            .join(", ");
        let values = table
            .columns
            .iter()
            .map(fixture_value)
            .collect::<Vec<_>>()
            .join(", ");
        connection
            .execute_batch(&format!(
                "INSERT INTO {} ({columns}) VALUES ({values})",
                quote(&table.name)
            ))
            .unwrap();
    }
    connection.execute("INSERT INTO abilities (\"group\",model,channel_id,enabled,priority,weight,tag) VALUES (?1,?2,?3,?4,?5,?6,?7)", ("é", "model-z", 3_i64, 0_i64, -2_i64, 7_i64, Option::<String>::None)).unwrap();
    connection.execute("INSERT INTO abilities (\"group\",model,channel_id,enabled,priority,weight,tag) VALUES (?1,?2,?3,?4,?5,?6,?7)", ("A", "model-a", 2_i64, 1_i64, 9_i64, -1_i64, Some("late"))).unwrap();
    connection
        .execute(
            "INSERT INTO options (key,value) VALUES (?1,?2)",
            ("control", "tab\tline\nslash\\literal\\N"),
        )
        .unwrap();
    connection
        .execute(
            "INSERT INTO options (key,value) VALUES (?1,NULL)",
            ["null-value"],
        )
        .unwrap();
    connection
        .execute(
            "UPDATE channels SET channel_info=?1 WHERE id=1",
            [br#"{"z":2,"a":1}"#.as_slice()],
        )
        .unwrap();
    connection.execute_batch("INSERT INTO channels (id,key,balance) VALUES (2,'key-2',-0.1),(3,'key-3',NULL),(4,'key-4',0.0000001); INSERT INTO top_ups (id,trade_no,amount,money) VALUES (2,'trade-2',-5,-0.1),(3,'trade-3',NULL,NULL); INSERT INTO subscription_orders (id,trade_no,money) VALUES (2,'order-2',-0.1),(3,'order-3',NULL);").unwrap();
    // Exercise numeric primary-key ordering across a decimal-width boundary.
    // An unqualified PostgreSQL ORDER BY can otherwise bind to the text
    // projection and yield 1, 10, 2 instead of the source order 1, 2, 10.
    connection
        .execute_batch(
            "INSERT INTO checkins (id,user_id,checkin_date,quota_awarded,created_at) VALUES \
             (2,2,'2026-08-02',2,2),(10,10,'2026-08-10',10,10);",
        )
        .unwrap();
    connection.close().unwrap();
    assert!(!path.with_extension("db-wal").exists());
}

fn assert_independent_oracle(sqlite: &Path, database_url: &str) {
    let source =
        Connection::open_with_flags(sqlite, rusqlite::OpenFlags::SQLITE_OPEN_READ_ONLY).unwrap();
    let source_control: Option<String> = source
        .query_row("SELECT value FROM options WHERE key='control'", [], |row| {
            row.get(0)
        })
        .unwrap();
    assert_eq!(
        source_control.as_deref(),
        Some("tab\tline\nslash\\literal\\N")
    );
    let mut target = Client::connect(database_url, NoTls).unwrap();
    let target_control: Option<String> = target
        .query_one(
            "SELECT value FROM lmm_copy_success.options WHERE key='control'",
            &[],
        )
        .unwrap()
        .get(0);
    assert_eq!(target_control, source_control);
    let groups: Vec<String> = target.query("SELECT \"group\" FROM lmm_copy_success.abilities ORDER BY \"group\" COLLATE \"C\", model COLLATE \"C\", channel_id", &[]).unwrap().into_iter().map(|row| row.get(0)).collect();
    assert_eq!(groups, vec!["A", "x", "é"]);
    let next_vendor: i64 = target
        .query_one("SELECT nextval('lmm_copy_success.vendors_id_seq')", &[])
        .unwrap()
        .get(0);
    assert_eq!(next_vendor, 1);
    let next_channel: i64 = target
        .query_one("SELECT nextval('lmm_copy_success.channels_id_seq')", &[])
        .unwrap()
        .get(0);
    assert_eq!(next_channel, 5);
    let null_value: Option<String> = target
        .query_one(
            "SELECT value FROM lmm_copy_success.options WHERE key='null-value'",
            &[],
        )
        .unwrap()
        .get(0);
    assert_eq!(null_value, None);
}

#[cfg(unix)]
fn assert_cli_success_audit(
    sqlite: &Path,
    directory: &Path,
    database_url: &str,
    contract_migration: &Path,
) {
    use std::os::unix::fs::PermissionsExt;
    let report = directory.join("success.json");
    let crate_dir = Path::new(env!("CARGO_MANIFEST_DIR"));
    let mut command = Command::new(env!("CARGO_BIN_EXE_lmm-db-migrate"));
    command
        .args(["verify", "--sqlite"])
        .arg(sqlite)
        .args(["--manifest"])
        .arg(crate_dir.join("schema/table-map.json"))
        .args(["--schema", "lmm_copy_success", "--report"])
        .arg(&report)
        .env("LMM_MIGRATE_DATABASE_URL", database_url);
    add_release_arguments(&mut command, contract_migration, "full-copy-release");
    let output = command.output().unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let audit = fs::read_to_string(&report).unwrap();
    assert!(!audit.contains("tab\\tline"));
    assert!(!audit.contains("primary_key_min"));
    assert!(!audit.contains("value_sha256"));
    assert_eq!(
        fs::metadata(report).unwrap().permissions().mode() & 0o777,
        0o600
    );
}

fn fixture_value(column: &lmm_db_migrate::manifest::Column) -> String {
    match column.converter {
        Converter::Boolean01 => "1".into(),
        Converter::Json => "'{\"z\":2,\"a\":1}'".into(),
        Converter::GormTimestampUtc => "'2026-08-01 01:02:03.123456'".into(),
        Converter::Decimal10_6 => "'12.340000'".into(),
        Converter::Identity if matches!(column.sqlite_type.as_str(), "integer" | "bigint") => {
            "1".into()
        }
        Converter::Identity
            if matches!(
                column.sqlite_type.as_str(),
                "numeric" | "real" | "decimal(10,6)"
            ) =>
        {
            "1.25".into()
        }
        Converter::Identity => "'x'".into(),
    }
}

fn schema_exists(database_url: &str, schema: &str) -> bool {
    Client::connect(database_url, NoTls)
        .unwrap()
        .query_one(
            "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname=$1)",
            &[&schema],
        )
        .unwrap()
        .get(0)
}

fn quote(value: &str) -> String {
    format!("\"{}\"", value.replace('"', "\"\""))
}

fn release_binding(contract_migration: &Path, release_id: &str) -> ReleaseBinding {
    let contract_sha256 = contract_sha256(contract_migration);
    ReleaseBinding::new(
        Version::new(1, "contract_id").expect("valid contract version"),
        contract_sha256.parse().expect("valid contract digest"),
        CompatibilityRange::new(
            Version::new(1, "reader").expect("valid reader version"),
            Version::new(1, "reader").expect("valid reader version"),
            "reader",
        )
        .expect("valid reader range"),
        CompatibilityRange::new(
            Version::new(1, "writer").expect("valid writer version"),
            Version::new(1, "writer").expect("valid writer version"),
            "writer",
        )
        .expect("valid writer range"),
        release_id.parse().expect("valid release identifier"),
        Sha256Digest::parse(&"b".repeat(64), "release").expect("valid release digest"),
        MANDATORY_COMPONENT_NAMES.iter().map(|name| {
            format!("{name}={}", "c".repeat(64))
                .parse()
                .expect("valid component")
        }),
    )
    .expect("complete release binding")
}

fn contract_sha256(contract_migration: &Path) -> String {
    format!(
        "{:x}",
        Sha256::digest(fs::read(contract_migration).expect("contract migration is readable"))
    )
}

fn add_release_arguments(command: &mut Command, contract_migration: &Path, release_id: &str) {
    command.args([
        "--contract-id",
        "1",
        "--contract-sha256",
        &contract_sha256(contract_migration),
        "--min-reader-version",
        "1",
        "--max-reader-version",
        "1",
        "--min-writer-version",
        "1",
        "--max-writer-version",
        "1",
        "--release-id",
        release_id,
        "--release-sha256",
        &"b".repeat(64),
    ]);
    for name in MANDATORY_COMPONENT_NAMES {
        command
            .arg("--component-sha256")
            .arg(format!("{name}={}", "c".repeat(64)));
    }
}

#[cfg(unix)]
#[test]
fn cli_failure_should_publish_private_non_sensitive_audit() {
    use std::os::unix::fs::PermissionsExt;
    let directory = tempfile::tempdir().unwrap();
    let report = directory.path().join("failure.json");
    let contract_migration =
        Path::new(env!("CARGO_MANIFEST_DIR")).join("../../migrations/0001_schema_contract.sql");
    let mut command = Command::new(env!("CARGO_BIN_EXE_lmm-db-migrate"));
    command
        .args([
            "rehearse",
            "--sqlite",
            "/not/used",
            "--manifest",
            "/not/used",
            "--baseline",
            "/not/used",
            "--catalog-sql",
            "/not/used",
            "--contract-migration",
        ])
        .arg(&contract_migration)
        .args(["--schema", "safe", "--report"])
        .arg(&report)
        .env_remove("LMM_MIGRATE_DATABASE_URL");
    add_release_arguments(&mut command, &contract_migration, "failure-release");
    let output = command.output().unwrap();
    assert!(!output.status.success());
    let audit = fs::read_to_string(&report).unwrap();
    assert_eq!(
        audit,
        "{\n  \"status\": \"failed\",\n  \"stage\": \"rehearse\",\n  \"error_category\": \"contract\"\n}\n"
    );
    assert_eq!(
        fs::metadata(report).unwrap().permissions().mode() & 0o777,
        0o600
    );
}
