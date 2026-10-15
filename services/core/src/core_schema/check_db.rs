//! One actual read-only schema/authority observation. Never a maintenance fence.
use super::{ComponentManifest, schema_manifest};
use crate::runtime_database::{self, RequiredCapabilities, RuntimeDatabaseError};
use serde::Serialize;
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Row};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum DatabaseCheckError {
    SchemaUnverified,
    AuthorityUnverified,
    RuntimeUnsafe,
    RuntimeIncomplete,
    ObservationUnavailable,
}
impl From<sqlx::Error> for DatabaseCheckError {
    fn from(_: sqlx::Error) -> Self {
        Self::ObservationUnavailable
    }
}
#[derive(Serialize)]
pub struct DatabaseCheckReport {
    version: u32,
    protocol: &'static str,
    status: &'static str,
    scope: &'static str,
    compiled_schema_manifest_sha256: String,
    compiled_schema_manifest_hash_domain: &'static str,
    components: Vec<ComponentManifest>,
    catalog: Catalog,
    runtime_authority: Authority,
    database: Database,
    observation: Observation,
    system_identifier: SystemIdentifier,
    not_checked: [&'static str; 6],
}
#[derive(Serialize)]
struct Catalog {
    status: &'static str,
    fingerprint_sha256: String,
    fingerprint_hash_domain: &'static str,
    matches_installed_receipt: bool,
}
#[derive(Serialize)]
struct Authority {
    status: &'static str,
    required_capabilities: Capabilities,
}
#[derive(Serialize)]
struct Capabilities {
    models: bool,
    events: bool,
    payments: bool,
    console: bool,
    commerce: bool,
    web_identity: bool,
    oauth2: bool,
}
impl From<RequiredCapabilities> for Capabilities {
    fn from(v: RequiredCapabilities) -> Self {
        Self {
            models: v.models,
            events: v.events,
            payments: v.payments,
            console: v.console,
            commerce: v.commerce,
            web_identity: v.web_identity,
            oauth2: v.oauth2,
        }
    }
}
#[derive(Serialize)]
struct Database {
    name: String,
    oid: String,
    server_address: Option<String>,
    server_port: Option<u16>,
    postmaster_started_at_unix_micros: String,
    observed_at_unix_micros: String,
    session_user: String,
    current_user: String,
    server_version_num: u32,
}
#[derive(Serialize)]
struct Observation {
    transaction_read_only: bool,
    transaction_isolation: &'static str,
    snapshot: &'static str,
}
#[derive(Serialize)]
struct SystemIdentifier {
    status: &'static str,
    value: Option<String>,
}
fn identifier(value: &str) -> bool {
    !value.is_empty() && value.len() <= 63 && !value.contains('\0')
}
fn authority_error(error: RuntimeDatabaseError) -> DatabaseCheckError {
    match error {
        RuntimeDatabaseError::UnsafePrivileges => DatabaseCheckError::RuntimeUnsafe,
        RuntimeDatabaseError::MissingPrivileges => DatabaseCheckError::RuntimeIncomplete,
        RuntimeDatabaseError::Unavailable => DatabaseCheckError::AuthorityUnverified,
    }
}
/// Use an independently protected, max-one-connection pool. All official checks,
/// actual catalog and public DB incarnation are observed in this same RR RO TX.
pub async fn check_database(
    pool: &PgPool,
    required: RequiredCapabilities,
) -> Result<DatabaseCheckReport, DatabaseCheckError> {
    let mut tx = pool.begin().await?;
    sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
        .execute(&mut *tx)
        .await?;
    sqlx::query("SET LOCAL statement_timeout='5s'")
        .execute(&mut *tx)
        .await?;
    sqlx::query("SET LOCAL lock_timeout='500ms'")
        .execute(&mut *tx)
        .await?;
    super::check_connection(&mut tx)
        .await
        .map_err(|_| DatabaseCheckError::SchemaUnverified)?;
    sqlx::query("SET LOCAL statement_timeout='1500ms'")
        .execute(&mut *tx)
        .await?;
    runtime_database::check_connection(&mut tx, required)
        .await
        .map_err(authority_error)?;
    sqlx::query("SET LOCAL statement_timeout='5s'")
        .execute(&mut *tx)
        .await?;
    let catalog = super::catalog(&mut tx)
        .await
        .map_err(|_| DatabaseCheckError::SchemaUnverified)?;
    if catalog.len() != 32 {
        return Err(DatabaseCheckError::ObservationUnavailable);
    }
    let row = sqlx::query(
        r#"
        SELECT current_database()::text AS name,d.oid::text AS oid,
          inet_server_addr()::text AS server_address,inet_server_port() AS server_port,
          floor(extract(epoch FROM pg_postmaster_start_time())*1000000)::bigint AS started,
          floor(extract(epoch FROM clock_timestamp())*1000000)::bigint AS observed,
          session_user::text AS session_role,current_user::text AS current_role,
          current_setting('server_version_num')::integer AS server_version,
          current_setting('transaction_read_only')='on' AS read_only,
          current_setting('transaction_isolation') AS isolation
        FROM pg_catalog.pg_database d WHERE d.datname=current_database()
    "#,
    )
    .fetch_one(&mut *tx)
    .await?;
    let name: String = row.try_get("name")?;
    let oid: String = row.try_get("oid")?;
    let oid = oid
        .parse::<u32>()
        .ok()
        .filter(|v| *v > 0)
        .ok_or(DatabaseCheckError::ObservationUnavailable)?;
    let session_user: String = row.try_get("session_role")?;
    let current_user: String = row.try_get("current_role")?;
    let version: i32 = row.try_get("server_version")?;
    let read_only: bool = row.try_get("read_only")?;
    let isolation: String = row.try_get("isolation")?;
    let address: Option<String> = row.try_get("server_address")?;
    let port: Option<i32> = row.try_get("server_port")?;
    if !identifier(&name)
        || !identifier(&session_user)
        || !identifier(&current_user)
        || session_user != current_user
        || !read_only
        || isolation != "repeatable read"
        || version <= 0
        || address.is_some() != port.is_some()
        || address
            .as_ref()
            .is_some_and(|v| v.is_empty() || v.len() > 128)
    {
        return Err(DatabaseCheckError::ObservationUnavailable);
    }
    let port = port
        .map(|v| {
            u16::try_from(v)
                .ok()
                .filter(|v| *v > 0)
                .ok_or(DatabaseCheckError::ObservationUnavailable)
        })
        .transpose()?;
    let started: i64 = row.try_get("started")?;
    let observed: i64 = row.try_get("observed")?;
    let manifest = schema_manifest();
    // Identical producer bytes to the existing schema-manifest CLI, including LF.
    let mut bytes =
        serde_json::to_vec(&manifest).map_err(|_| DatabaseCheckError::ObservationUnavailable)?;
    bytes.push(b'\n');
    let digest = format!("{:x}", Sha256::digest(&bytes));
    let report = DatabaseCheckReport {
        version: 1,
        protocol: "core.check-db.v1",
        status: "validated",
        scope: "runtime_configured_capabilities",
        compiled_schema_manifest_sha256: digest,
        compiled_schema_manifest_hash_domain: "sha256-exact-schema-manifest-stdout-v1",
        components: manifest.components,
        catalog: Catalog {
            status: "validated",
            fingerprint_sha256: catalog.iter().map(|v| format!("{v:02x}")).collect(),
            fingerprint_hash_domain: "sha256-u64be-byte-length-prefixed-utf8-v1",
            matches_installed_receipt: true,
        },
        runtime_authority: Authority {
            status: "validated",
            required_capabilities: required.into(),
        },
        database: Database {
            name,
            oid: oid.to_string(),
            server_address: address,
            server_port: port,
            postmaster_started_at_unix_micros: started.to_string(),
            observed_at_unix_micros: observed.to_string(),
            session_user,
            current_user,
            server_version_num: version as u32,
        },
        observation: Observation {
            transaction_read_only: read_only,
            transaction_isolation: "repeatable_read",
            snapshot: "single_repeatable_read_transaction",
        },
        system_identifier: SystemIdentifier {
            status: "not_observed",
            value: None,
        },
        not_checked: [
            "maintenance_fence",
            "restore_readiness",
            "backup_integrity",
            "physical_system_identifier",
            "external_services",
            "historical_migration",
        ],
    };
    tx.commit().await?;
    Ok(report)
}
