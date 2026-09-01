//! Strict source and target catalog contracts.

use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    path::Path,
};

use serde::{Deserialize, Serialize};

use crate::MigrationError;

/// The only converter behaviors permitted by the migration contract.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Converter {
    Identity,
    Boolean01,
    Json,
    GormTimestampUtc,
    Decimal10_6,
}

/// A source column and the exact corresponding PostgreSQL column contract.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Column {
    pub name: String,
    pub sqlite_type: String,
    pub sqlite_not_null: bool,
    pub sqlite_default: Option<String>,
    pub pk_position: u32,
    pub postgres_type: String,
    pub postgres_not_null: bool,
    pub postgres_default: Option<String>,
    pub converter: Converter,
}

/// Exact SQLite index shape, including automatic constraint indexes.
#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct SqliteIndex {
    pub name: String,
    pub unique: bool,
    pub columns: Vec<String>,
    pub predicate: Option<String>,
}

/// Exact PostgreSQL index shape, including primary/unique constraint indexes.
#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PostgresIndex {
    pub name: String,
    pub unique: bool,
    pub primary: bool,
    pub columns: Vec<String>,
    pub predicate: Option<String>,
}

/// Exact PostgreSQL sequence ownership and column-default contract.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Sequence {
    pub name: String,
    pub owned_column: String,
    pub default: String,
}

/// One table's complete source and target contract.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Table {
    pub name: String,
    pub columns: Vec<Column>,
    pub sqlite_indexes: Vec<SqliteIndex>,
    pub postgres_indexes: Vec<PostgresIndex>,
    pub sequence: Option<Sequence>,
    pub verifier: String,
}

/// Versioned, exhaustive migration contract.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Manifest {
    pub version: u32,
    pub evidence: String,
    pub tables: Vec<Table>,
}

/// PostgreSQL catalog export consumed by the baseline rehearsal.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PostgresCatalogTable {
    pub name: String,
    pub columns: Vec<PostgresCatalogColumn>,
    pub indexes: Vec<PostgresIndex>,
    pub sequence: Option<Sequence>,
}

/// PostgreSQL column metadata used for exact baseline validation.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PostgresCatalogColumn {
    pub name: String,
    #[serde(rename = "type")]
    pub postgres_type: String,
    pub not_null: bool,
    pub default: Option<String>,
}

impl Manifest {
    /// Loads JSON and enforces the exact 34-table production contract.
    pub fn load(path: &Path) -> Result<Self, MigrationError> {
        let manifest: Self = serde_json::from_slice(&fs::read(path)?)?;
        manifest.validate()?;
        Ok(manifest)
    }

    /// Validates all internal source/target catalog invariants.
    pub fn validate(&self) -> Result<(), MigrationError> {
        if self.version != 2 {
            return Err(MigrationError::Manifest(format!(
                "unsupported manifest version {}",
                self.version
            )));
        }
        let expected: BTreeSet<_> = EXPECTED_TABLES.iter().copied().collect();
        let actual: BTreeSet<_> = self
            .tables
            .iter()
            .map(|table| table.name.as_str())
            .collect();
        if self.tables.len() != EXPECTED_TABLES.len() || actual != expected {
            return Err(MigrationError::Manifest(format!(
                "table set must be exactly the {} production tables",
                EXPECTED_TABLES.len()
            )));
        }
        for table in &self.tables {
            validate_table(table)?;
        }
        Ok(())
    }

    /// Compares a live catalog export against every target field in the manifest.
    pub fn validate_postgres_catalog(&self, path: &Path) -> Result<(), MigrationError> {
        let actual: Vec<PostgresCatalogTable> = serde_json::from_slice(&fs::read(path)?)?;
        if actual.len() != self.tables.len() {
            return Err(MigrationError::Manifest(format!(
                "PostgreSQL catalog has {} tables; expected {}",
                actual.len(),
                self.tables.len()
            )));
        }
        for expected in &self.tables {
            let table = actual
                .iter()
                .find(|table| table.name == expected.name)
                .ok_or_else(|| {
                    MigrationError::Manifest(format!(
                        "PostgreSQL catalog is missing table {}",
                        expected.name
                    ))
                })?;
            let expected_columns: BTreeMap<_, _> = expected
                .columns
                .iter()
                .map(|column| {
                    (
                        column.name.clone(),
                        PostgresCatalogColumn {
                            name: column.name.clone(),
                            postgres_type: column.postgres_type.clone(),
                            not_null: column.postgres_not_null,
                            default: column.postgres_default.clone(),
                        },
                    )
                })
                .collect();
            let actual_columns: BTreeMap<_, _> = table
                .columns
                .iter()
                .cloned()
                .map(|column| (column.name.clone(), column))
                .collect();
            if actual_columns.len() != table.columns.len() || actual_columns != expected_columns {
                return Err(MigrationError::Manifest(format!(
                    "PostgreSQL column contract drift: {}",
                    expected.name
                )));
            }
            let mut indexes = table.indexes.clone();
            indexes.sort();
            let mut expected_indexes = expected.postgres_indexes.clone();
            expected_indexes.sort();
            if indexes != expected_indexes {
                return Err(MigrationError::Manifest(format!(
                    "PostgreSQL index contract drift: {}",
                    expected.name
                )));
            }
            if table.sequence != expected.sequence {
                return Err(MigrationError::Manifest(format!(
                    "PostgreSQL sequence contract drift: {}",
                    expected.name
                )));
            }
        }
        Ok(())
    }
}

fn validate_table(table: &Table) -> Result<(), MigrationError> {
    let columns: BTreeSet<_> = table
        .columns
        .iter()
        .map(|column| column.name.as_str())
        .collect();
    if columns.len() != table.columns.len() || columns.is_empty() {
        return Err(MigrationError::Manifest(format!(
            "{} has empty or duplicate columns",
            table.name
        )));
    }
    let mut primary_key: Vec<_> = table
        .columns
        .iter()
        .filter(|column| column.pk_position > 0)
        .collect();
    primary_key.sort_unstable_by_key(|column| column.pk_position);
    if primary_key.is_empty()
        || primary_key
            .iter()
            .enumerate()
            .any(|(index, column)| column.pk_position as usize != index + 1)
    {
        return Err(MigrationError::Manifest(format!(
            "{} has a missing or non-contiguous primary key",
            table.name
        )));
    }
    validate_unique_names(
        &table.name,
        "SQLite index",
        table.sqlite_indexes.iter().map(|index| index.name.as_str()),
    )?;
    validate_unique_names(
        &table.name,
        "PostgreSQL index",
        table
            .postgres_indexes
            .iter()
            .map(|index| index.name.as_str()),
    )?;
    if table
        .sqlite_indexes
        .iter()
        .any(|index| index.columns.is_empty())
        || table
            .postgres_indexes
            .iter()
            .any(|index| index.columns.is_empty())
    {
        return Err(MigrationError::Manifest(format!(
            "{} has an index without key columns",
            table.name
        )));
    }
    let id_primary_key = primary_key.len() == 1 && primary_key[0].name == "id";
    if id_primary_key != table.sequence.is_some() {
        return Err(MigrationError::Manifest(format!(
            "{} sequence declaration disagrees with its primary key",
            table.name
        )));
    }
    if let Some(sequence) = &table.sequence
        && (sequence.name != format!("{}_id_seq", table.name)
            || sequence.owned_column != "id"
            || sequence.default != format!("nextval('{}_id_seq'::regclass)", table.name))
    {
        return Err(MigrationError::Manifest(format!(
            "{} has an invalid sequence ownership/default contract",
            table.name
        )));
    }
    let primary_indexes: Vec<_> = table
        .postgres_indexes
        .iter()
        .filter(|index| index.primary)
        .collect();
    if primary_indexes.len() != 1 || !primary_indexes[0].unique {
        return Err(MigrationError::Manifest(format!(
            "{} must have one unique PostgreSQL primary index",
            table.name
        )));
    }
    if table.verifier != "count+blake3_table_v1" {
        return Err(MigrationError::Manifest(format!(
            "{} has an unsupported verifier",
            table.name
        )));
    }
    Ok(())
}

fn validate_unique_names<'a>(
    table: &str,
    kind: &str,
    names: impl Iterator<Item = &'a str>,
) -> Result<(), MigrationError> {
    let names: Vec<_> = names.collect();
    let unique: BTreeSet<_> = names.iter().copied().collect();
    if unique.len() != names.len() {
        return Err(MigrationError::Manifest(format!(
            "{table} has duplicate {kind} names"
        )));
    }
    Ok(())
}

const EXPECTED_TABLES: [&str; 34] = [
    "abilities",
    "auth_flows",
    "authz_roles",
    "casbin_rule",
    "channels",
    "checkins",
    "custom_oauth_providers",
    "external_identity_claims",
    "logs",
    "midjourneys",
    "models",
    "options",
    "passkey_credentials",
    "perf_metrics",
    "prefill_groups",
    "quota_data",
    "redemptions",
    "setups",
    "subscription_orders",
    "subscription_plans",
    "subscription_pre_consume_records",
    "system_instances",
    "system_task_locks",
    "system_tasks",
    "tasks",
    "tokens",
    "top_ups",
    "two_fa_backup_codes",
    "two_fas",
    "user_oauth_bindings",
    "user_sessions",
    "user_subscriptions",
    "users",
    "vendors",
];

#[cfg(test)]
mod tests {
    use super::*;

    fn manifest_and_catalog() -> (Manifest, Vec<PostgresCatalogTable>) {
        let path = Path::new(env!("CARGO_MANIFEST_DIR")).join("schema/table-map.json");
        let manifest = Manifest::load(&path).unwrap();
        let catalog = manifest
            .tables
            .iter()
            .map(|table| PostgresCatalogTable {
                name: table.name.clone(),
                columns: table
                    .columns
                    .iter()
                    .map(|column| PostgresCatalogColumn {
                        name: column.name.clone(),
                        postgres_type: column.postgres_type.clone(),
                        not_null: column.postgres_not_null,
                        default: column.postgres_default.clone(),
                    })
                    .collect(),
                indexes: table.postgres_indexes.clone(),
                sequence: table.sequence.clone(),
            })
            .collect();
        (manifest, catalog)
    }

    fn validate_catalog(
        manifest: &Manifest,
        catalog: &[PostgresCatalogTable],
    ) -> Result<(), MigrationError> {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("catalog.json");
        fs::write(&path, serde_json::to_vec(catalog).unwrap()).unwrap();
        manifest.validate_postgres_catalog(&path)
    }

    #[test]
    fn checked_in_manifest_should_be_strictly_valid() {
        let path = Path::new(env!("CARGO_MANIFEST_DIR")).join("schema/table-map.json");
        Manifest::load(&path).unwrap();
    }

    #[test]
    fn postgres_catalog_validator_should_accept_exact_catalog() {
        let (manifest, catalog) = manifest_and_catalog();
        validate_catalog(&manifest, &catalog).unwrap();
    }

    #[test]
    fn postgres_catalog_validator_should_reject_column_contract_mutations() {
        let (manifest, catalog) = manifest_and_catalog();

        let mut changed_type = catalog.clone();
        changed_type[0].columns[0].postgres_type = "text".into();
        assert!(validate_catalog(&manifest, &changed_type).is_err());

        let mut changed_nullability = catalog.clone();
        changed_nullability[0].columns[0].not_null = !changed_nullability[0].columns[0].not_null;
        assert!(validate_catalog(&manifest, &changed_nullability).is_err());

        let mut changed_default = catalog.clone();
        let column = changed_default
            .iter_mut()
            .flat_map(|table| &mut table.columns)
            .find(|column| column.default.is_some())
            .unwrap();
        column.default = Some("definitely_wrong".into());
        assert!(validate_catalog(&manifest, &changed_default).is_err());
    }

    #[test]
    fn postgres_catalog_validator_should_reject_index_contract_mutations() {
        let (manifest, catalog) = manifest_and_catalog();

        let mut deleted_index = catalog.clone();
        deleted_index[0].indexes.pop();
        assert!(validate_catalog(&manifest, &deleted_index).is_err());

        let mut changed_columns = catalog.clone();
        changed_columns[0].indexes[0].columns[0] = "not_a_column".into();
        assert!(validate_catalog(&manifest, &changed_columns).is_err());

        let mut changed_unique = catalog.clone();
        changed_unique[0].indexes[0].unique = !changed_unique[0].indexes[0].unique;
        assert!(validate_catalog(&manifest, &changed_unique).is_err());

        let mut changed_predicate = catalog.clone();
        let index = changed_predicate
            .iter_mut()
            .flat_map(|table| &mut table.indexes)
            .find(|index| index.predicate.is_some())
            .unwrap();
        index.predicate = Some("(deleted_at IS NOT NULL)".into());
        assert!(validate_catalog(&manifest, &changed_predicate).is_err());
    }

    #[test]
    fn postgres_catalog_validator_should_reject_sequence_contract_mutations() {
        let (manifest, catalog) = manifest_and_catalog();

        let mut changed_name = catalog.clone();
        changed_name
            .iter_mut()
            .find_map(|table| table.sequence.as_mut())
            .unwrap()
            .name = "wrong_sequence".into();
        assert!(validate_catalog(&manifest, &changed_name).is_err());

        let mut changed_owner = catalog.clone();
        changed_owner
            .iter_mut()
            .find_map(|table| table.sequence.as_mut())
            .unwrap()
            .owned_column = "wrong_column".into();
        assert!(validate_catalog(&manifest, &changed_owner).is_err());

        let mut changed_default = catalog.clone();
        changed_default
            .iter_mut()
            .find_map(|table| table.sequence.as_mut())
            .unwrap()
            .default = "nextval('wrong_sequence'::regclass)".into();
        assert!(validate_catalog(&manifest, &changed_default).is_err());
    }
}
