//! Transactional application of a reviewed forward-only schema contract.

use std::path::Path;

use postgres::{Client, NoTls};
use serde::Serialize;

use crate::{
    MigrationError,
    contract::{ContractInstallOutcome, install_or_verify},
    forward_schema::{
        BOUNTY_SCHEMA_CONTRACT_ID, COMPANY_BILLING_PROFILE_SCHEMA_CONTRACT_ID,
        CURRENT_DASHBOARD_SCHEMA_CONTRACT_ID, SUBSCRIPTION_RESET_SCHEMA_CONTRACT_ID,
        verify_company_billing_profile_schema, verify_current_dashboard_schema,
        verify_open_source_bounty_schema, verify_subscription_reset_schema,
    },
    postgres_catalog::acquire_shared_migration_lock,
    release::ReleaseBinding,
};

/// Inputs for one forward-only schema expansion.
pub struct ForwardOptions<'a> {
    /// PostgreSQL connection URL. It is never included in the report.
    pub database_url: &'a str,
    /// Existing, non-public application schema to expand.
    pub schema: &'a str,
    /// Exact schema-contract SQL artifact bound by `release`.
    pub contract_migration: &'a Path,
    /// Immutable contract and release identity.
    pub release: &'a ReleaseBinding,
}

/// Non-sensitive result of a forward-only expansion.
#[derive(Debug, Serialize)]
pub struct ForwardReport {
    pub status: &'static str,
    pub schema: String,
    pub contract_id: i64,
    pub outcome: ContractInstallOutcome,
    pub bounty_schema_verified: bool,
}

/// Applies one reviewed contract step to an existing schema and verifies its expanded shape.
///
/// The operation is intentionally separate from the contract-1 SQLite rehearsal: it requires an
/// existing schema-contract ledger, takes the shared migration lock, executes the bound artifact
/// in one transaction, and rolls back if the mounted bounty schema is incomplete.
pub fn forward(options: &ForwardOptions<'_>) -> Result<ForwardReport, MigrationError> {
    validate_schema(options.schema)?;
    if options.release.contract_id().as_i64() < BOUNTY_SCHEMA_CONTRACT_ID {
        return Err(MigrationError::Manifest(
            "forward migration requires contract id 2 or newer".into(),
        ));
    }
    let mut client = Client::connect(options.database_url, NoTls)?;
    let mut transaction = client.transaction()?;
    transaction.batch_execute("SET TRANSACTION ISOLATION LEVEL READ COMMITTED")?;
    acquire_shared_migration_lock(&mut transaction)
        .map_err(|error| MigrationError::Manifest(error.to_string()))?;
    if !schema_exists(&mut transaction, options.schema)? {
        return Err(MigrationError::Manifest(
            "forward migration target schema does not exist".into(),
        ));
    }
    let outcome = install_or_verify(
        &mut transaction,
        options.schema,
        options.contract_migration,
        options.release,
    )?;
    verify_open_source_bounty_schema(&mut transaction, options.schema)?;
    if options.release.contract_id().as_i64() >= CURRENT_DASHBOARD_SCHEMA_CONTRACT_ID {
        verify_current_dashboard_schema(&mut transaction, options.schema)?;
    }
    if options.release.contract_id().as_i64() >= SUBSCRIPTION_RESET_SCHEMA_CONTRACT_ID {
        verify_subscription_reset_schema(&mut transaction, options.schema)?;
    }
    if options.release.contract_id().as_i64() >= COMPANY_BILLING_PROFILE_SCHEMA_CONTRACT_ID {
        verify_company_billing_profile_schema(&mut transaction, options.schema)?;
    }
    transaction.commit()?;
    Ok(ForwardReport {
        status: "verified",
        schema: options.schema.to_owned(),
        contract_id: options.release.contract_id().as_i64(),
        outcome,
        bounty_schema_verified: true,
    })
}

fn schema_exists(
    transaction: &mut postgres::Transaction<'_>,
    schema: &str,
) -> Result<bool, MigrationError> {
    Ok(transaction
        .query_one(
            "SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname = $1)",
            &[&schema],
        )?
        .get(0))
}

fn validate_schema(schema: &str) -> Result<(), MigrationError> {
    let valid = schema != "public"
        && !schema.is_empty()
        && schema.len() <= 63
        && schema.bytes().enumerate().all(|(index, byte)| {
            byte == b'_' || byte.is_ascii_lowercase() || (index > 0 && byte.is_ascii_digit())
        });
    if !valid {
        return Err(MigrationError::Manifest(
            "schema must be a non-public name matching [a-z_][a-z0-9_]{0,62}".into(),
        ));
    }
    Ok(())
}
