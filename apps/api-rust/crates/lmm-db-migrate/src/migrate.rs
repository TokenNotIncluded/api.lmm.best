//! Transactional full-copy rehearsal and independent source/target verification.

use std::{
    ffi::OsString,
    fs,
    fs::File,
    io::{Read, Seek, SeekFrom, Write},
    path::{Path, PathBuf},
};

use fallible_iterator::FallibleIterator;
use postgres::{Client, NoTls, Transaction};
use rusqlite::{Connection, OpenFlags, types::ValueRef};
use serde::Serialize;
use sha2::{Digest, Sha256};

#[cfg(unix)]
use std::os::{fd::AsRawFd, unix::fs::MetadataExt};

use crate::{
    MigrationError,
    canonical::{
        CanonicalValue, TableHasher, canonical_bool, canonical_decimal, canonical_json,
        canonical_timestamp,
    },
    forward_schema::{
        BOUNTY_SCHEMA_CONTRACT_ID, CURRENT_DASHBOARD_SCHEMA_CONTRACT_ID,
        SUBSCRIPTION_RESET_SCHEMA_CONTRACT_ID, verify_current_dashboard_schema,
        verify_open_source_bounty_schema, verify_subscription_reset_schema,
    },
    inspect::inspect_sqlite,
    manifest::{Column, Converter, Manifest, Table},
    postgres_catalog::acquire_shared_migration_lock,
    release::ReleaseBinding,
};

#[derive(Debug, Serialize)]
pub struct MigrationReport {
    pub status: &'static str,
    pub schema: String,
    pub table_count: usize,
    pub sequence_count: usize,
    pub tables: Vec<TableEvidence>,
    pub financial_aggregates: Vec<AggregateEvidence>,
    pub release: ReleaseBinding,
}

#[derive(Debug, Serialize)]
pub struct TableEvidence {
    pub table: String,
    pub count: u64,
    pub blake3_table_v1: String,
    pub primary_key_bounds_present: bool,
}

#[derive(Debug, Serialize)]
pub struct AggregateEvidence {
    pub table: String,
    pub column: String,
    pub matched: bool,
}

pub struct RehearseOptions<'a> {
    pub sqlite: &'a Path,
    pub manifest: &'a Manifest,
    pub baseline: &'a Path,
    pub catalog_sql: &'a Path,
    pub contract_migration: &'a Path,
    pub release: &'a ReleaseBinding,
    pub schema: &'a str,
    pub database_url: &'a str,
    pub fault_after_table: Option<&'a str>,
    pub fault_before_verify: bool,
}

pub struct VerifyOptions<'a> {
    pub sqlite: &'a Path,
    pub manifest: &'a Manifest,
    pub schema: &'a str,
    pub database_url: &'a str,
    pub release: &'a ReleaseBinding,
}

pub fn rehearse(options: &RehearseOptions<'_>) -> Result<MigrationReport, MigrationError> {
    validate_schema(options.schema)?;
    let inspection = inspect_sqlite(options.sqlite, options.manifest)?;
    if !inspection.drift.is_empty() {
        return Err(MigrationError::Manifest(
            "source schema does not match manifest".into(),
        ));
    }
    let source_before = SourceSnapshot::capture(options.sqlite)?;
    let source = open_source(options.sqlite, &source_before)?;
    source.connection.execute_batch("BEGIN")?;
    let mut client = Client::connect(options.database_url, NoTls)?;
    let mut transaction = client.transaction()?;
    acquire_shared_migration_lock(&mut transaction)
        .map_err(|error| MigrationError::Manifest(error.to_string()))?;
    if schema_exists(&mut transaction, options.schema)? {
        return Err(MigrationError::Manifest(
            "target schema must not already exist".into(),
        ));
    }
    let baseline = qualify_sql(&fs::read_to_string(options.baseline)?, options.schema);
    transaction.batch_execute(&format!(
        "CREATE SCHEMA {}; SET LOCAL search_path = {}, pg_catalog;",
        quote_ident(options.schema),
        quote_ident(options.schema)
    ))?;
    transaction.batch_execute(&baseline)?;
    for table in &options.manifest.tables {
        copy_table(&source.connection, &mut transaction, options.schema, table)?;
        if options.fault_after_table == Some(table.name.as_str()) {
            return Err(MigrationError::Manifest(format!(
                "injected COPY failure after {}",
                table.name
            )));
        }
    }
    set_sequences(&mut transaction, options.schema, options.manifest)?;
    validate_catalog(
        &mut transaction,
        options.schema,
        options.catalog_sql,
        options.manifest,
    )?;
    if options.fault_before_verify {
        return Err(MigrationError::Manifest(
            "injected verification failure".into(),
        ));
    }
    let report = verify_connections(
        &source.connection,
        &mut transaction,
        options.schema,
        options.manifest,
        options.release,
    )?;
    ensure_source_still_offline(options.sqlite, options.manifest, &source_before)?;
    crate::contract::install_or_verify(
        &mut transaction,
        options.schema,
        options.contract_migration,
        options.release,
    )?;
    if options.release.contract_id().as_i64() >= BOUNTY_SCHEMA_CONTRACT_ID {
        verify_open_source_bounty_schema(&mut transaction, options.schema)?;
    }
    if options.release.contract_id().as_i64() >= CURRENT_DASHBOARD_SCHEMA_CONTRACT_ID {
        verify_current_dashboard_schema(&mut transaction, options.schema)?;
    }
    if options.release.contract_id().as_i64() >= SUBSCRIPTION_RESET_SCHEMA_CONTRACT_ID {
        verify_subscription_reset_schema(&mut transaction, options.schema)?;
    }
    transaction.commit()?;
    source.connection.execute_batch("COMMIT")?;
    Ok(report)
}

pub fn verify(options: &VerifyOptions<'_>) -> Result<MigrationReport, MigrationError> {
    validate_schema(options.schema)?;
    let inspection = inspect_sqlite(options.sqlite, options.manifest)?;
    if !inspection.drift.is_empty() {
        return Err(MigrationError::Manifest(
            "source schema does not match manifest".into(),
        ));
    }
    let source_before = SourceSnapshot::capture(options.sqlite)?;
    let source = open_source(options.sqlite, &source_before)?;
    source.connection.execute_batch("BEGIN")?;
    let mut client = Client::connect(options.database_url, NoTls)?;
    let mut transaction = client.transaction()?;
    transaction.batch_execute("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY")?;
    acquire_shared_migration_lock(&mut transaction)
        .map_err(|error| MigrationError::Manifest(error.to_string()))?;
    let report = verify_connections(
        &source.connection,
        &mut transaction,
        options.schema,
        options.manifest,
        options.release,
    )?;
    crate::contract::verify_release(&mut transaction, options.schema, options.release)?;
    if options.release.contract_id().as_i64() >= BOUNTY_SCHEMA_CONTRACT_ID {
        verify_open_source_bounty_schema(&mut transaction, options.schema)?;
    }
    if options.release.contract_id().as_i64() >= CURRENT_DASHBOARD_SCHEMA_CONTRACT_ID {
        verify_current_dashboard_schema(&mut transaction, options.schema)?;
    }
    if options.release.contract_id().as_i64() >= SUBSCRIPTION_RESET_SCHEMA_CONTRACT_ID {
        verify_subscription_reset_schema(&mut transaction, options.schema)?;
    }
    ensure_source_still_offline(options.sqlite, options.manifest, &source_before)?;
    transaction.commit()?;
    source.connection.execute_batch("COMMIT")?;
    Ok(report)
}

fn ensure_source_still_offline(
    sqlite: &Path,
    manifest: &Manifest,
    before: &SourceSnapshot,
) -> Result<(), MigrationError> {
    let inspection = inspect_sqlite(sqlite, manifest)?;
    let after = SourceSnapshot::capture(sqlite)?;
    if inspection.drift.is_empty() && &after == before {
        Ok(())
    } else {
        Err(MigrationError::Manifest(
            "source changed or acquired drift during migration".into(),
        ))
    }
}

#[derive(Debug, PartialEq, Eq)]
struct SourceSnapshot {
    canonical: PathBuf,
    device: u64,
    inode: u64,
    size: u64,
    modified_seconds: i64,
    modified_nanoseconds: i64,
    sha256: [u8; 32],
}

impl SourceSnapshot {
    fn capture(path: &Path) -> Result<Self, MigrationError> {
        let link = fs::symlink_metadata(path)?;
        if link.file_type().is_symlink() || !link.file_type().is_file() {
            return Err(MigrationError::Manifest(
                "SQLite source must be a regular file".into(),
            ));
        }
        reject_sidecars(path)?;
        let canonical = fs::canonicalize(path)?;
        reject_sidecars(&canonical)?;
        let mut file = File::open(&canonical)?;
        Self::from_file(canonical, &mut file)
    }

    fn from_file(canonical: PathBuf, file: &mut File) -> Result<Self, MigrationError> {
        let metadata = file.metadata()?;
        file.seek(SeekFrom::Start(0))?;
        let mut digest = Sha256::new();
        let mut buffer = [0_u8; 1024 * 1024];
        loop {
            let read = file.read(&mut buffer)?;
            if read == 0 {
                break;
            }
            digest.update(&buffer[..read]);
        }
        let sha256: [u8; 32] = digest.finalize().into();
        file.seek(SeekFrom::Start(0))?;
        Ok(Self {
            canonical,
            device: metadata.dev(),
            inode: metadata.ino(),
            size: metadata.len(),
            modified_seconds: metadata.mtime(),
            modified_nanoseconds: metadata.mtime_nsec(),
            sha256,
        })
    }
}

fn reject_sidecars(path: &Path) -> Result<(), MigrationError> {
    for suffix in ["-wal", "-journal", "-shm"] {
        let mut candidate: OsString = path.as_os_str().to_owned();
        candidate.push(suffix);
        if fs::symlink_metadata(Path::new(&candidate)).is_ok() {
            return Err(MigrationError::Manifest(format!(
                "SQLite sidecar exists: {suffix}"
            )));
        }
    }
    Ok(())
}

pub(crate) fn validate_schema(schema: &str) -> Result<(), MigrationError> {
    let valid = schema != "public"
        && !schema.is_empty()
        && schema.len() <= 63
        && schema
            .bytes()
            .enumerate()
            .all(|(i, b)| b == b'_' || b.is_ascii_lowercase() || (i > 0 && b.is_ascii_digit()));
    if !valid {
        return Err(MigrationError::Manifest(
            "schema must be a non-public name matching [a-z_][a-z0-9_]{0,62}".into(),
        ));
    }
    Ok(())
}

struct SourceHandle {
    connection: Connection,
    _identity_file: File,
}

fn open_source(path: &Path, before: &SourceSnapshot) -> Result<SourceHandle, MigrationError> {
    let link = fs::symlink_metadata(path)?;
    if link.file_type().is_symlink() || !link.file_type().is_file() {
        return Err(MigrationError::Manifest(
            "SQLite source must remain a regular file".into(),
        ));
    }
    reject_sidecars(path)?;
    let canonical = fs::canonicalize(path)?;
    let mut identity_file = File::open(path)?;
    let opened = SourceSnapshot::from_file(canonical, &mut identity_file)?;
    if &opened != before {
        return Err(MigrationError::Manifest(
            "SQLite source changed between capture and open".into(),
        ));
    }
    let flags = OpenFlags::SQLITE_OPEN_READ_ONLY
        | OpenFlags::SQLITE_OPEN_URI
        | OpenFlags::SQLITE_OPEN_NO_MUTEX;
    let connection = Connection::open_with_flags(
        format!("file:/proc/self/fd/{}?mode=ro", identity_file.as_raw_fd()),
        flags,
    )?;
    Ok(SourceHandle {
        connection,
        _identity_file: identity_file,
    })
}

fn schema_exists(client: &mut Transaction<'_>, schema: &str) -> Result<bool, MigrationError> {
    Ok(client
        .query_one(
            "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)",
            &[&schema],
        )?
        .get(0))
}

fn qualify_sql(sql: &str, schema: &str) -> String {
    sql.replace("public.", &format!("{}.", quote_ident(schema)))
}

fn copy_table(
    source: &Connection,
    target: &mut Transaction<'_>,
    schema: &str,
    table: &Table,
) -> Result<(), MigrationError> {
    let columns = table
        .columns
        .iter()
        .map(|c| quote_ident(&c.name))
        .collect::<Vec<_>>()
        .join(", ");
    let order = primary_key(table)
        .iter()
        .map(|c| sqlite_order_expression(c))
        .collect::<Vec<_>>()
        .join(", ");
    let sql = format!(
        "SELECT {columns} FROM {} ORDER BY {order}",
        quote_ident(&table.name)
    );
    let mut statement = source.prepare(&sql)?;
    let mut rows = statement.query([])?;
    let copy = format!(
        "COPY {}.{} ({columns}) FROM STDIN WITH (FORMAT text)",
        quote_ident(schema),
        quote_ident(&table.name)
    );
    let mut writer = target.copy_in(&copy)?;
    while let Some(row) = rows.next()? {
        for (index, column) in table.columns.iter().enumerate() {
            if index > 0 {
                writer.write_all(b"\t")?;
            }
            let value = sqlite_canonical(row.get_ref(index)?, column)?;
            writer.write_all(copy_text(&value).as_bytes())?;
        }
        writer.write_all(b"\n")?;
    }
    writer.finish()?;
    Ok(())
}

fn sqlite_canonical(
    value: ValueRef<'_>,
    column: &Column,
) -> Result<CanonicalValue, MigrationError> {
    if matches!(value, ValueRef::Null) {
        return Ok(CanonicalValue::Null);
    }
    match column.converter {
        Converter::Boolean01 => canonical_bool(Some(sqlite_i64(value, column)?)),
        Converter::Json => canonical_json(Some(sqlite_json_str(value, column)?)),
        Converter::GormTimestampUtc => canonical_timestamp(Some(sqlite_str(value, column)?)),
        Converter::Decimal10_6 => {
            let value = sqlite_number(value, column)?;
            canonical_decimal(Some(&value), 10, 6)
        }
        Converter::Identity if column.postgres_type.starts_with("numeric") => {
            let number = match value {
                ValueRef::Integer(value) => value.to_string(),
                ValueRef::Real(value) => value.to_string(),
                ValueRef::Text(value) => String::from_utf8(value.to_vec()).map_err(|_| {
                    MigrationError::Canonical(format!("{} is not UTF-8", column.name))
                })?,
                _ => {
                    return Err(MigrationError::Canonical(format!(
                        "{} is not numeric",
                        column.name
                    )));
                }
            };
            normalize_number(&number).map(CanonicalValue::Decimal)
        }
        Converter::Identity => match value {
            ValueRef::Integer(v) => Ok(CanonicalValue::Integer(v)),
            ValueRef::Real(v) => Ok(CanonicalValue::Decimal(v.to_string())),
            ValueRef::Text(v) => Ok(CanonicalValue::Text(
                String::from_utf8(v.to_vec()).map_err(|_| {
                    MigrationError::Canonical(format!("{} is not UTF-8", column.name))
                })?,
            )),
            ValueRef::Blob(v) => Ok(CanonicalValue::Bytes(v.to_vec())),
            ValueRef::Null => Ok(CanonicalValue::Null),
        },
    }
}

fn sqlite_i64(value: ValueRef<'_>, column: &Column) -> Result<i64, MigrationError> {
    value
        .as_i64()
        .map_err(|error| MigrationError::Canonical(format!("{}: {error}", column.name)))
}

fn sqlite_str<'a>(value: ValueRef<'a>, column: &Column) -> Result<&'a str, MigrationError> {
    value
        .as_str()
        .map_err(|error| MigrationError::Canonical(format!("{}: {error}", column.name)))
}

fn sqlite_json_str<'a>(value: ValueRef<'a>, column: &Column) -> Result<&'a str, MigrationError> {
    let bytes = match value {
        ValueRef::Text(bytes) | ValueRef::Blob(bytes) => bytes,
        _ => {
            return Err(MigrationError::Canonical(format!(
                "{} is not UTF-8 JSON text or blob",
                column.name
            )));
        }
    };
    std::str::from_utf8(bytes)
        .map_err(|_| MigrationError::Canonical(format!("{} is not UTF-8", column.name)))
}

fn sqlite_number(value: ValueRef<'_>, column: &Column) -> Result<String, MigrationError> {
    match value {
        ValueRef::Integer(value) => Ok(value.to_string()),
        ValueRef::Real(value) if value.is_finite() => Ok(expand_scientific(&value.to_string())?),
        ValueRef::Real(_) => Err(MigrationError::Canonical(format!(
            "{} is non-finite",
            column.name
        ))),
        ValueRef::Text(value) => String::from_utf8(value.to_vec())
            .map_err(|_| MigrationError::Canonical(format!("{} is not UTF-8", column.name))),
        _ => Err(MigrationError::Canonical(format!(
            "{} is not numeric",
            column.name
        ))),
    }
}

fn copy_text(value: &CanonicalValue) -> String {
    match value {
        CanonicalValue::Null => "\\N".into(),
        CanonicalValue::Bool(value) => if *value { "t" } else { "f" }.into(),
        CanonicalValue::Integer(value) => value.to_string(),
        CanonicalValue::Decimal(value)
        | CanonicalValue::Text(value)
        | CanonicalValue::Json(value)
        | CanonicalValue::Timestamp(value) => escape_copy(value),
        CanonicalValue::Bytes(value) => format!(
            "\\\\x{}",
            value
                .iter()
                .map(|byte| format!("{byte:02x}"))
                .collect::<String>()
        ),
    }
}

fn escape_copy(value: &str) -> String {
    value
        .replace('\\', "\\\\")
        .replace('\t', "\\t")
        .replace('\n', "\\n")
        .replace('\r', "\\r")
}

fn set_sequences(
    target: &mut Transaction<'_>,
    schema: &str,
    manifest: &Manifest,
) -> Result<(), MigrationError> {
    for table in &manifest.tables {
        if let Some(sequence) = &table.sequence {
            let sql = format!(
                "SELECT setval(to_regclass($1), COALESCE(MAX(id), 1), MAX(id) IS NOT NULL) FROM {}.{}",
                quote_ident(schema),
                quote_ident(&table.name)
            );
            let qualified = format!("{}.{}", quote_ident(schema), quote_ident(&sequence.name));
            target.execute(&sql, &[&qualified])?;
        }
    }
    Ok(())
}

fn validate_catalog(
    target: &mut Transaction<'_>,
    schema: &str,
    catalog_sql: &Path,
    manifest: &Manifest,
) -> Result<(), MigrationError> {
    let query = fs::read_to_string(catalog_sql)?.replace("'public'", &format!("'{schema}'"));
    let value: serde_json::Value = target.query_one(&query, &[])?.get(0);
    let temporary = tempfile::NamedTempFile::new()?;
    // PostgreSQL renders a regclass default as `nextval('sequence'::regclass)` when
    // the sequence is on the search path, but qualifies it in a versioned schema.
    // The manifest deliberately stores the schema-agnostic form so the same contract
    // can validate each isolated rehearsal namespace.
    let canonical_catalog = serde_json::to_string(&value)?.replace(&format!("'{schema}."), "'");
    temporary
        .as_file()
        .write_all(canonical_catalog.as_bytes())?;
    manifest.validate_postgres_catalog(temporary.path())
}

trait PgQuery {
    fn for_each_row(
        &mut self,
        query: &str,
        visitor: &mut dyn FnMut(postgres::Row) -> Result<(), MigrationError>,
    ) -> Result<(), MigrationError>;
    fn scalar_string(&mut self, query: &str) -> Result<String, MigrationError>;
}
impl PgQuery for Client {
    fn for_each_row(
        &mut self,
        query: &str,
        visitor: &mut dyn FnMut(postgres::Row) -> Result<(), MigrationError>,
    ) -> Result<(), MigrationError> {
        let mut rows = self.query_raw(
            query,
            std::iter::empty::<&(dyn postgres::types::ToSql + Sync)>(),
        )?;
        while let Some(row) = rows.next()? {
            visitor(row)?;
        }
        Ok(())
    }
    fn scalar_string(&mut self, query: &str) -> Result<String, MigrationError> {
        Ok(self.query_one(query, &[])?.get(0))
    }
}
impl PgQuery for Transaction<'_> {
    fn for_each_row(
        &mut self,
        query: &str,
        visitor: &mut dyn FnMut(postgres::Row) -> Result<(), MigrationError>,
    ) -> Result<(), MigrationError> {
        let mut rows = self.query_raw(
            query,
            std::iter::empty::<&(dyn postgres::types::ToSql + Sync)>(),
        )?;
        while let Some(row) = rows.next()? {
            visitor(row)?;
        }
        Ok(())
    }
    fn scalar_string(&mut self, query: &str) -> Result<String, MigrationError> {
        Ok(self.query_one(query, &[])?.get(0))
    }
}

fn verify_connections(
    target_source: &Connection,
    target: &mut impl PgQuery,
    schema: &str,
    manifest: &Manifest,
    release: &ReleaseBinding,
) -> Result<MigrationReport, MigrationError> {
    let mut tables = Vec::with_capacity(manifest.tables.len());
    for table in &manifest.tables {
        let source = sqlite_evidence(target_source, table)?;
        let destination = postgres_evidence(target, schema, table)?;
        if source.0 != destination.0 || source.1 != destination.1 {
            return Err(MigrationError::Manifest(format!(
                "verification mismatch for {}",
                table.name
            )));
        }
        tables.push(TableEvidence {
            table: table.name.clone(),
            count: source.0,
            blake3_table_v1: source.1.to_hex().to_string(),
            primary_key_bounds_present: source.2,
        });
    }
    let financial_aggregates = financial_evidence(target_source, target, schema)?;
    Ok(MigrationReport {
        status: "verified",
        schema: schema.into(),
        table_count: tables.len(),
        sequence_count: manifest
            .tables
            .iter()
            .filter(|t| t.sequence.is_some())
            .count(),
        tables,
        financial_aggregates,
        release: release.clone(),
    })
}

type Evidence = (u64, blake3::Hash, bool);

fn sqlite_evidence(source: &Connection, table: &Table) -> Result<Evidence, MigrationError> {
    let count: i64 = source.query_row(
        &format!("SELECT count(*) FROM {}", quote_ident(&table.name)),
        [],
        |r| r.get(0),
    )?;
    let count =
        u64::try_from(count).map_err(|_| MigrationError::Canonical("negative count".into()))?;
    let columns = table
        .columns
        .iter()
        .map(|c| quote_ident(&c.name))
        .collect::<Vec<_>>()
        .join(", ");
    let keys = primary_key(table);
    let order = keys
        .iter()
        .map(|c| sqlite_order_expression(c))
        .collect::<Vec<_>>()
        .join(", ");
    let mut statement = source.prepare(&format!(
        "SELECT {columns} FROM {} ORDER BY {order}",
        quote_ident(&table.name)
    ))?;
    let mut rows = statement.query([])?;
    let mut hasher = TableHasher::new(&table.name, count);
    let mut bounds_present = false;
    while let Some(row) = rows.next()? {
        let values = table
            .columns
            .iter()
            .enumerate()
            .map(|(i, c)| sqlite_canonical(row.get_ref(i)?, c))
            .collect::<Result<Vec<_>, _>>()?;
        bounds_present = true;
        hasher.update(&values);
    }
    let (count, hash) = hasher.finish();
    Ok((count, hash, bounds_present))
}

fn postgres_evidence(
    target: &mut impl PgQuery,
    schema: &str,
    table: &Table,
) -> Result<Evidence, MigrationError> {
    let expressions = table
        .columns
        .iter()
        .map(pg_text_expression)
        .collect::<Vec<_>>()
        .join(", ");
    let keys = primary_key(table)
        .iter()
        .map(|c| pg_order_expression(c))
        .collect::<Vec<_>>()
        .join(", ");
    let query = format!(
        "SELECT {expressions} FROM {}.{} AS source ORDER BY {keys}",
        quote_ident(schema),
        quote_ident(&table.name)
    );
    let count: u64 = target
        .scalar_string(&format!(
            "SELECT count(*)::text FROM {}.{}",
            quote_ident(schema),
            quote_ident(&table.name)
        ))?
        .parse()
        .map_err(|_| {
            MigrationError::Canonical(format!("invalid PostgreSQL count for {}", table.name))
        })?;
    let mut hasher = TableHasher::new(&table.name, count);
    let mut bounds_present = false;
    target.for_each_row(&query, &mut |row| {
        let values = table
            .columns
            .iter()
            .enumerate()
            .map(|(i, c)| pg_canonical(row.get::<_, Option<String>>(i), c))
            .collect::<Result<Vec<_>, _>>()?;
        bounds_present = true;
        hasher.update(&values);
        Ok(())
    })?;
    let (count, hash) = hasher.finish();
    Ok((count, hash, bounds_present))
}

fn sqlite_order_expression(column: &Column) -> String {
    let name = quote_ident(&column.name);
    if sqlite_is_text(column) {
        format!("CAST({name} AS BLOB)")
    } else {
        name
    }
}

fn pg_order_expression(column: &Column) -> String {
    // Qualify the base column so PostgreSQL cannot resolve this ORDER BY item
    // to the text-valued SELECT expression with the same derived name. That
    // ambiguity otherwise orders bigint identifiers lexicographically (for
    // example 1, 10, 2) and produces a false canonical-hash mismatch.
    let name = format!("source.{}", quote_ident(&column.name));
    if postgres_is_text(column) {
        format!("{name} COLLATE \"C\"")
    } else {
        name
    }
}

fn sqlite_is_text(column: &Column) -> bool {
    matches!(column.sqlite_type.as_str(), "text" | "datetime" | "json")
        || column.sqlite_type.starts_with("varchar")
        || column.sqlite_type.starts_with("char")
}

fn postgres_is_text(column: &Column) -> bool {
    column.postgres_type == "text" || column.postgres_type.starts_with("character")
}

fn pg_text_expression(column: &Column) -> String {
    let name = quote_ident(&column.name);
    let body = match column.converter {
        Converter::Boolean01 => format!("CASE WHEN {name} THEN '1' ELSE '0' END"),
        Converter::GormTimestampUtc => {
            format!("to_char({name} AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"')")
        }
        Converter::Json => format!("{name}::text"),
        _ if column.postgres_type == "bytea" => format!("encode({name}, 'hex')"),
        _ => format!("{name}::text"),
    };
    format!("CASE WHEN {name} IS NULL THEN NULL ELSE {body} END")
}

fn pg_canonical(value: Option<String>, column: &Column) -> Result<CanonicalValue, MigrationError> {
    let Some(value) = value else {
        return Ok(CanonicalValue::Null);
    };
    match column.converter {
        Converter::Boolean01 => {
            canonical_bool(Some(value.parse().map_err(|_| {
                MigrationError::Canonical("invalid PostgreSQL boolean".into())
            })?))
        }
        Converter::Json => canonical_json(Some(&value)),
        Converter::GormTimestampUtc => canonical_timestamp(Some(&value)),
        Converter::Decimal10_6 => canonical_decimal(Some(&value), 10, 6),
        Converter::Identity
            if column.postgres_type == "bigint" || column.postgres_type == "integer" =>
        {
            Ok(CanonicalValue::Integer(value.parse().map_err(|_| {
                MigrationError::Canonical(format!("invalid integer in {}", column.name))
            })?))
        }
        Converter::Identity if column.postgres_type == "bytea" => {
            Ok(CanonicalValue::Bytes(decode_hex(&value)?))
        }
        Converter::Identity if column.postgres_type.starts_with("numeric") => {
            normalize_number(&value).map(CanonicalValue::Decimal)
        }
        Converter::Identity => Ok(CanonicalValue::Text(value)),
    }
}

fn decode_hex(value: &str) -> Result<Vec<u8>, MigrationError> {
    if !value.len().is_multiple_of(2) {
        return Err(MigrationError::Canonical("invalid bytea hex".into()));
    }
    (0..value.len())
        .step_by(2)
        .map(|i| {
            u8::from_str_radix(&value[i..i + 2], 16)
                .map_err(|_| MigrationError::Canonical("invalid bytea hex".into()))
        })
        .collect()
}
fn primary_key(table: &Table) -> Vec<&Column> {
    let mut keys = table
        .columns
        .iter()
        .filter(|c| c.pk_position > 0)
        .collect::<Vec<_>>();
    keys.sort_by_key(|c| c.pk_position);
    keys
}
const FINANCIAL: [(&str, &str); 15] = [
    ("users", "quota"),
    ("users", "used_quota"),
    ("users", "request_count"),
    ("tokens", "remain_quota"),
    ("tokens", "used_quota"),
    ("logs", "quota"),
    ("logs", "prompt_tokens"),
    ("logs", "completion_tokens"),
    ("quota_data", "token_used"),
    ("quota_data", "quota"),
    ("top_ups", "amount"),
    ("top_ups", "money"),
    ("subscription_orders", "money"),
    ("channels", "balance"),
    ("channels", "used_quota"),
];
fn financial_evidence(
    source: &Connection,
    target: &mut impl PgQuery,
    schema: &str,
) -> Result<Vec<AggregateEvidence>, MigrationError> {
    let mut out = Vec::new();
    for (table, column) in FINANCIAL {
        let source_value: String = source.query_row(
            &format!(
                "SELECT COALESCE(CAST(SUM({}) AS TEXT),'0') FROM {}",
                quote_ident(column),
                quote_ident(table)
            ),
            [],
            |r| r.get(0),
        )?;
        let query = format!(
            "SELECT COALESCE(SUM({})::text,'0') FROM {}.{}",
            quote_ident(column),
            quote_ident(schema),
            quote_ident(table)
        );
        let target_value = target.scalar_string(&query)?;
        let source_normalized = normalize_number(&source_value)?;
        let target_normalized = normalize_number(&target_value)?;
        let real_aggregate = matches!(
            (table, column),
            ("top_ups", "money") | ("subscription_orders", "money") | ("channels", "balance")
        );
        if if real_aggregate {
            !numbers_within_tolerance(&source_normalized, &target_normalized)?
        } else {
            source_normalized != target_normalized
        } {
            return Err(MigrationError::Manifest(format!(
                "financial aggregate mismatch for {table}.{column}"
            )));
        }
        out.push(AggregateEvidence {
            table: table.into(),
            column: column.into(),
            matched: true,
        });
    }
    Ok(out)
}
fn normalize_number(value: &str) -> Result<String, MigrationError> {
    let expanded = expand_scientific(value)?;
    let value = expanded.as_str();
    let negative = value.starts_with('-');
    let raw = value.trim_start_matches('-');
    let (whole, fraction) = raw.split_once('.').unwrap_or((raw, ""));
    let whole = whole.trim_start_matches('0');
    let fraction = fraction.trim_end_matches('0');
    let mut result: String = if whole.is_empty() {
        "0".into()
    } else {
        whole.into()
    };
    if !fraction.is_empty() {
        result.push('.');
        result.push_str(fraction);
    }
    if negative && result != "0" {
        result.insert(0, '-');
    }
    Ok(result)
}

fn expand_scientific(value: &str) -> Result<String, MigrationError> {
    let lower = value.to_ascii_lowercase();
    let Some((mantissa, exponent)) = lower.split_once('e') else {
        return Ok(value.to_owned());
    };
    let exponent: i32 = exponent
        .parse()
        .map_err(|_| MigrationError::Canonical("invalid numeric exponent".into()))?;
    let negative = mantissa.starts_with('-');
    let unsigned = mantissa.trim_start_matches(['-', '+']);
    let (whole, fraction) = unsigned.split_once('.').unwrap_or((unsigned, ""));
    if whole.is_empty()
        || !whole
            .bytes()
            .chain(fraction.bytes())
            .all(|byte| byte.is_ascii_digit())
    {
        return Err(MigrationError::Canonical("invalid numeric mantissa".into()));
    }
    let digits = format!("{whole}{fraction}");
    let decimal = i32::try_from(whole.len())
        .map_err(|_| MigrationError::Canonical("numeric too long".into()))?
        + exponent;
    let mut result = if decimal <= 0 {
        format!("0.{}{}", "0".repeat((-decimal) as usize), digits)
    } else if decimal as usize >= digits.len() {
        format!("{}{}", digits, "0".repeat(decimal as usize - digits.len()))
    } else {
        let split = decimal as usize;
        format!("{}.{}", &digits[..split], &digits[split..])
    };
    if negative {
        result.insert(0, '-');
    }
    Ok(result)
}

fn numbers_within_tolerance(left: &str, right: &str) -> Result<bool, MigrationError> {
    let left: f64 = left
        .parse()
        .map_err(|_| MigrationError::Canonical("invalid source aggregate".into()))?;
    let right: f64 = right
        .parse()
        .map_err(|_| MigrationError::Canonical("invalid target aggregate".into()))?;
    if !left.is_finite() || !right.is_finite() {
        return Ok(false);
    }
    let tolerance = 1e-9_f64.max(left.abs().max(right.abs()) * 1e-12);
    Ok((left - right).abs() <= tolerance)
}
fn quote_ident(value: &str) -> String {
    format!("\"{}\"", value.replace('"', "\"\""))
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn schema_validation_should_reject_injection_and_uppercase() {
        assert!(validate_schema("safe_01").is_ok());
        assert!(validate_schema("public;drop schema public").is_err());
        assert!(validate_schema("Upper").is_err());
    }
    #[test]
    fn copy_text_should_escape_control_characters() {
        assert_eq!(
            copy_text(&CanonicalValue::Text("a\\b\tc\nd".into())),
            "a\\\\b\\tc\\nd"
        );
    }
    #[test]
    fn source_snapshot_should_detect_content_and_sidecar_changes() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("source.db");
        let connection = Connection::open(&path).unwrap();
        connection
            .execute_batch(
                "CREATE TABLE fixture(id INTEGER PRIMARY KEY); INSERT INTO fixture VALUES (1)",
            )
            .unwrap();
        connection.close().unwrap();
        let before = SourceSnapshot::capture(&path).unwrap();
        let connection = Connection::open(&path).unwrap();
        connection
            .execute("INSERT INTO fixture VALUES (2)", [])
            .unwrap();
        connection.close().unwrap();
        assert_ne!(SourceSnapshot::capture(&path).unwrap(), before);
        File::create(format!("{}-wal", path.display())).unwrap();
        assert!(SourceSnapshot::capture(&path).is_err());
    }
    #[test]
    fn source_open_should_reject_path_replacement_after_capture() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("source.db");
        Connection::open(&path)
            .unwrap()
            .execute_batch("CREATE TABLE original(id INTEGER PRIMARY KEY)")
            .unwrap();
        let before = SourceSnapshot::capture(&path).unwrap();
        fs::rename(&path, directory.path().join("original.db")).unwrap();
        Connection::open(&path)
            .unwrap()
            .execute_batch("CREATE TABLE replacement(id INTEGER PRIMARY KEY)")
            .unwrap();
        assert!(open_source(&path, &before).is_err());
    }
    #[test]
    fn source_read_transaction_should_hold_lock_until_explicit_commit() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("source.db");
        let setup = Connection::open(&path).unwrap();
        setup
            .execute_batch(
                "CREATE TABLE fixture(id INTEGER PRIMARY KEY); INSERT INTO fixture VALUES(1)",
            )
            .unwrap();
        setup.close().unwrap();
        let before = SourceSnapshot::capture(&path).unwrap();
        let source = open_source(&path, &before).unwrap();
        source.connection.execute_batch("BEGIN").unwrap();
        let _: i64 = source
            .connection
            .query_row("SELECT id FROM fixture", [], |row| row.get(0))
            .unwrap();
        let writer = Connection::open(&path).unwrap();
        writer.busy_timeout(std::time::Duration::ZERO).unwrap();
        assert!(writer.execute_batch("BEGIN EXCLUSIVE").is_err());
        source.connection.execute_batch("COMMIT").unwrap();
        assert!(writer.execute_batch("BEGIN EXCLUSIVE; ROLLBACK").is_ok());
    }
    #[test]
    fn canonical_errors_should_not_echo_rejected_values() {
        let timestamp = "SECRET_TIMESTAMP_NOT_VALID";
        let decimal = "SECRET_DECIMAL_NOT_VALID";
        assert!(
            !canonical_timestamp(Some(timestamp))
                .unwrap_err()
                .to_string()
                .contains(timestamp)
        );
        assert!(
            !canonical_decimal(Some(decimal), 10, 6)
                .unwrap_err()
                .to_string()
                .contains(decimal)
        );
        assert!(
            !canonical_bool(Some(987_654_321))
                .unwrap_err()
                .to_string()
                .contains("987654321")
        );
    }
    #[test]
    fn scientific_numbers_should_expand_before_numeric_copy() {
        assert_eq!(expand_scientific("1.25e-7").unwrap(), "0.000000125");
        assert_eq!(expand_scientific("-2e3").unwrap(), "-2000");
        assert!(
            sqlite_number(
                ValueRef::Real(f64::INFINITY),
                &Column {
                    name: "amount".into(),
                    sqlite_type: "real".into(),
                    sqlite_not_null: false,
                    sqlite_default: None,
                    pk_position: 0,
                    postgres_type: "numeric".into(),
                    postgres_not_null: false,
                    postgres_default: None,
                    converter: Converter::Identity
                }
            )
            .is_err()
        );
    }

    fn json_column() -> Column {
        Column {
            name: "channel_info".into(),
            sqlite_type: "json".into(),
            sqlite_not_null: false,
            sqlite_default: None,
            pk_position: 0,
            postgres_type: "json".into(),
            postgres_not_null: false,
            postgres_default: None,
            converter: Converter::Json,
        }
    }

    #[test]
    fn sqlite_json_should_accept_utf8_blob_storage_used_by_gorm() {
        assert_eq!(
            sqlite_canonical(ValueRef::Blob(br#"{"z":2,"a":1}"#), &json_column()).unwrap(),
            CanonicalValue::Json(r#"{"a":1,"z":2}"#.into())
        );
    }

    #[test]
    fn sqlite_json_should_reject_non_utf8_blob_storage() {
        assert!(sqlite_canonical(ValueRef::Blob(&[0xff, 0xfe]), &json_column()).is_err());
    }
}
