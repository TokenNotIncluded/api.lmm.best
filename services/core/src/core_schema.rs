//! One explicit, atomic fresh installation for the assembled Rust core.
//! Startup checks definitions and their installed catalog; it never executes DDL.
use crate::identity::{IdentityError, IdentityStore};
use sha2::{Digest, Sha256};
use sqlx::{PgConnection, PgPool, Row};

const COMPONENTS: &[(&str, &str)] = &[
    ("identity", include_str!("../schema/identity.sql")),
    ("ledger", include_str!("../schema/ledger.sql")),
    ("payments", include_str!("../schema/payments.sql")),
    ("charging", include_str!("../schema/charging.sql")),
    ("routing", include_str!("../schema/routing.sql")),
    ("events", include_str!("../schema/events.sql")),
    (
        "charging-ledger",
        include_str!("../schema/charging_ledger.sql"),
    ),
    ("migration", include_str!("../schema/migration.sql")),
    ("console", include_str!("../schema/console.sql")),
    ("model-gateway", include_str!("../schema/model_gateway.sql")),
    ("commerce", include_str!("../schema/commerce.sql")),
];

/// The compiled fresh-install contract, including explicitly disabled modules.
/// Operators cannot change its size with runtime configuration.
pub fn component_count() -> usize {
    COMPONENTS.len()
}

const CONTRACT: &str = "CREATE TABLE core_meta.component_contract (name TEXT PRIMARY KEY, fingerprint BYTEA NOT NULL CHECK (octet_length(fingerprint)=32)); CREATE TABLE core_meta.runtime_contract (singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton), catalog_fingerprint BYTEA NOT NULL CHECK (octet_length(catalog_fingerprint)=32));";

// Include application relations, columns/defaults, constraints, indexes,
// functions, and triggers. Rows, owner names and grants are deliberately absent:
// normal application writes and provisioning a restricted role cannot alter it.
const CATALOG: &str = r#"
WITH ns AS (
 SELECT oid,nspname FROM pg_namespace
 WHERE nspname NOT IN ('pg_catalog','information_schema') AND left(nspname,3)<>'pg_'
), definitions AS (
 SELECT json_build_array('relation',n.nspname,c.relname,c.relkind)::text AS definition
 FROM pg_class c JOIN ns n ON n.oid=c.relnamespace
 UNION ALL
 SELECT json_build_array('column',n.nspname,c.relname,a.attnum,a.attname,
   format_type(a.atttypid,a.atttypmod),a.attnotnull,a.attidentity,a.attgenerated,
   pg_get_expr(d.adbin,d.adrelid))::text
 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN ns n ON n.oid=c.relnamespace
 LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
 WHERE a.attnum>0 AND NOT a.attisdropped
 UNION ALL
 SELECT json_build_array('constraint',n.nspname,c.conname,pg_get_constraintdef(c.oid,true))::text
 FROM pg_constraint c JOIN ns n ON n.oid=c.connamespace
 UNION ALL
 SELECT json_build_array('index',n.nspname,c.relname,pg_get_indexdef(i.indexrelid))::text
 FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN ns n ON n.oid=c.relnamespace
 UNION ALL
 SELECT json_build_array('function',n.nspname,p.proname,pg_get_functiondef(p.oid))::text
 FROM pg_proc p JOIN ns n ON n.oid=p.pronamespace WHERE p.prokind IN ('f','p')
 UNION ALL
 SELECT json_build_array('trigger',n.nspname,c.relname,t.tgname,t.tgenabled,
   pg_get_triggerdef(t.oid,true))::text
 FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN ns n ON n.oid=c.relnamespace
 WHERE NOT t.tgisinternal
 UNION ALL
 -- RI trigger names embed installation OIDs, which change on pg_dump/restore.
 -- Hash their stable constraint/function/trigger properties instead.
 SELECT json_build_array('internal-trigger',n.nspname,c.relname,k.conname,
   pn.nspname,p.proname,t.tgtype,t.tgenabled,t.tgdeferrable,t.tginitdeferred,
   t.tgattr::text,encode(t.tgargs,'hex'),pg_get_expr(t.tgqual,t.tgrelid))::text
 FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN ns n ON n.oid=c.relnamespace
 JOIN pg_proc p ON p.oid=t.tgfoid JOIN pg_namespace pn ON pn.oid=p.pronamespace
 LEFT JOIN pg_constraint k ON k.oid=t.tgconstraint WHERE t.tgisinternal
) SELECT definition FROM definitions ORDER BY definition COLLATE "C"
"#;

async fn catalog(connection: &mut PgConnection) -> Result<Vec<u8>, IdentityError> {
    let definitions: Vec<String> = sqlx::query_scalar(CATALOG).fetch_all(connection).await?;
    let mut hash = Sha256::new();
    for definition in definitions {
        hash.update((definition.len() as u64).to_be_bytes());
        hash.update(definition.as_bytes());
    }
    Ok(hash.finalize().to_vec())
}

pub async fn init(pool: &PgPool) -> Result<(), IdentityError> {
    let mut tx = pool.begin().await?;
    sqlx::query("SET LOCAL synchronous_commit=on")
        .execute(&mut *tx)
        .await?;
    sqlx::query("SET LOCAL lock_timeout='3s'")
        .execute(&mut *tx)
        .await?;
    // Same installation lock as the isolated identity component initializer.
    sqlx::query("SELECT pg_advisory_xact_lock(741803120)")
        .execute(&mut *tx)
        .await?;
    let occupied: bool = sqlx::query_scalar(
        r#"SELECT EXISTS (
        SELECT 1 FROM pg_namespace n WHERE n.nspname NOT IN ('pg_catalog','information_schema')
        AND left(n.nspname,3)<>'pg_' AND (n.nspname<>'public'
        OR EXISTS(SELECT 1 FROM pg_class c WHERE c.relnamespace=n.oid)
        OR EXISTS(SELECT 1 FROM pg_proc p WHERE p.pronamespace=n.oid)
        OR EXISTS(SELECT 1 FROM pg_type t WHERE t.typnamespace=n.oid)))"#,
    )
    .fetch_one(&mut *tx)
    .await?;
    if occupied {
        return Err(IdentityError::Conflict);
    }
    for (_, schema) in COMPONENTS {
        sqlx::raw_sql(schema).execute(&mut *tx).await?;
    }
    sqlx::raw_sql(CONTRACT).execute(&mut *tx).await?;
    // Preserve the native identity contract used by the read-only RPC service.
    IdentityStore::record_schema_contract(&mut tx).await?;
    for (name, schema) in COMPONENTS {
        sqlx::query("INSERT INTO core_meta.component_contract(name,fingerprint) VALUES ($1,$2)")
            .bind(name)
            .bind(Sha256::digest(schema.as_bytes()).to_vec())
            .execute(&mut *tx)
            .await?;
    }
    let fingerprint = catalog(&mut tx).await?;
    sqlx::query("INSERT INTO core_meta.runtime_contract(catalog_fingerprint) VALUES ($1)")
        .bind(fingerprint)
        .execute(&mut *tx)
        .await?;
    tx.commit().await?;
    Ok(())
}

pub async fn check(pool: &PgPool) -> Result<(), IdentityError> {
    IdentityStore::from_pool(pool.clone())
        .check_schema()
        .await?;
    // One repeatable catalog snapshot, including all component receipts.
    let mut tx = pool.begin().await?;
    sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
        .execute(&mut *tx)
        .await?;
    let rows = sqlx::query("SELECT name,fingerprint FROM core_meta.component_contract")
        .fetch_all(&mut *tx)
        .await?;
    if rows.len() != COMPONENTS.len() {
        return Err(IdentityError::Storage);
    }
    for (name, schema) in COMPONENTS {
        let valid = rows.iter().any(|row| {
            row.try_get::<String, _>("name").ok().as_deref() == Some(name)
                && row.try_get::<Vec<u8>, _>("fingerprint").ok()
                    == Some(Sha256::digest(schema.as_bytes()).to_vec())
        });
        if !valid {
            return Err(IdentityError::Storage);
        }
    }
    let saved: Vec<u8> = sqlx::query_scalar(
        "SELECT catalog_fingerprint FROM core_meta.runtime_contract WHERE singleton",
    )
    .fetch_one(&mut *tx)
    .await?;
    if saved != catalog(&mut tx).await? {
        return Err(IdentityError::Storage);
    }
    tx.commit().await?;
    Ok(())
}
