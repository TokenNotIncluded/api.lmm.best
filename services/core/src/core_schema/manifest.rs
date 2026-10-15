//! The compiled installation contract. This projection performs no I/O.
use super::{CATALOG, COMPONENTS, CONTRACT};
use crate::identity::IdentityStore;
use serde::Serialize;
use sha2::{Digest, Sha256};

#[derive(Serialize)]
pub struct SchemaManifest {
    pub format_version: u32,
    pub scope: &'static str,
    /// Fresh installation order, including configured-off components.
    pub components: Vec<ComponentManifest>,
    pub identity: IdentityManifest,
    pub installer_contract_sql_sha256: String,
    pub catalog: CatalogManifest,
}

#[derive(Serialize)]
pub struct ComponentManifest {
    pub name: &'static str,
    pub raw_schema_sha256: String,
}

#[derive(Serialize)]
pub struct IdentityManifest {
    pub version: i32,
    pub fingerprint_sha256: String,
}

#[derive(Serialize)]
pub struct CatalogManifest {
    /// The exact compiled SQL, including its leading and trailing newlines.
    pub query_utf8: &'static str,
    pub query_sha256: String,
    pub query_hash_domain: &'static str,
    /// This describes the existing algorithm; no domain prefix is hashed.
    pub fingerprint_hash_domain: &'static str,
    pub definition_order: &'static str,
}

/// Describe compiled bytes, without reading configuration, files or a database.
/// The caller must separately bind this output to its verified binary artifact;
/// this is not an observation of an installed database or source revision.
pub fn schema_manifest() -> SchemaManifest {
    let (version, identity_fingerprint) = IdentityStore::compiled_schema_contract();
    SchemaManifest {
        format_version: 1,
        scope: "compiled-core-schema-contract",
        components: COMPONENTS
            .iter()
            .map(|(name, sql)| ComponentManifest {
                name,
                raw_schema_sha256: format!("{:x}", Sha256::digest(sql.as_bytes())),
            })
            .collect(),
        identity: IdentityManifest {
            version,
            fingerprint_sha256: identity_fingerprint
                .iter()
                .map(|byte| format!("{byte:02x}"))
                .collect(),
        },
        installer_contract_sql_sha256: format!("{:x}", Sha256::digest(CONTRACT.as_bytes())),
        catalog: CatalogManifest {
            query_utf8: CATALOG,
            query_sha256: format!("{:x}", Sha256::digest(CATALOG.as_bytes())),
            query_hash_domain: "sha256-raw-utf8-v1",
            fingerprint_hash_domain: "sha256-u64be-byte-length-prefixed-utf8-v1",
            definition_order: "postgresql-c-collation",
        },
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::BTreeSet;

    #[test]
    fn components_and_identity_use_actual_compiled_installation_bytes() {
        let manifest = schema_manifest();
        assert_eq!(manifest.format_version, 1);
        assert_eq!(manifest.scope, "compiled-core-schema-contract");
        assert_eq!(manifest.components.len(), COMPONENTS.len());
        let mut names = BTreeSet::new();
        for (item, (name, sql)) in manifest.components.iter().zip(COMPONENTS) {
            assert_eq!(item.name, *name);
            assert!(names.insert(item.name));
            assert_eq!(
                item.raw_schema_sha256,
                format!("{:x}", Sha256::digest(sql.as_bytes()))
            );
        }
        let (version, fingerprint) = IdentityStore::compiled_schema_contract();
        assert_eq!(manifest.identity.version, version);
        assert_eq!(
            fingerprint.to_vec(),
            Sha256::digest(include_str!("../../schema/identity.sql").as_bytes()).to_vec()
        );
        assert_eq!(
            manifest.identity.fingerprint_sha256,
            manifest
                .components
                .iter()
                .find(|item| item.name == "identity")
                .unwrap()
                .raw_schema_sha256
        );
        assert_eq!(
            manifest.installer_contract_sql_sha256,
            format!("{:x}", Sha256::digest(CONTRACT.as_bytes()))
        );
    }

    #[test]
    fn catalog_query_preserves_exact_literal_bytes_and_hash_domains() {
        let manifest = schema_manifest();
        assert_eq!(manifest.catalog.query_utf8.as_bytes(), CATALOG.as_bytes());
        assert!(manifest.catalog.query_utf8.starts_with('\n'));
        assert!(manifest.catalog.query_utf8.ends_with('\n'));
        assert_eq!(
            manifest.catalog.query_sha256,
            format!("{:x}", Sha256::digest(CATALOG.as_bytes()))
        );
        assert_eq!(manifest.catalog.query_hash_domain, "sha256-raw-utf8-v1");
        assert_eq!(
            manifest.catalog.fingerprint_hash_domain,
            "sha256-u64be-byte-length-prefixed-utf8-v1"
        );
        assert_eq!(manifest.catalog.definition_order, "postgresql-c-collation");
    }

    #[test]
    fn advertised_catalog_framing_matches_the_real_non_ascii_byte_algorithm() {
        let definitions = vec!["é".to_owned(), "x".to_owned()];
        let mut bytes = Vec::new();
        bytes.extend_from_slice(&2_u64.to_be_bytes());
        bytes.extend_from_slice("é".as_bytes());
        bytes.extend_from_slice(&1_u64.to_be_bytes());
        bytes.extend_from_slice(b"x");
        assert_eq!(
            super::super::fingerprint(&definitions),
            Sha256::digest(bytes).to_vec()
        );
        assert_eq!(super::super::fingerprint(&[]), Sha256::digest(b"").to_vec());
        assert_ne!(
            super::super::fingerprint(&["ab".into(), "c".into()]),
            super::super::fingerprint(&["a".into(), "bc".into()])
        );
    }
}
