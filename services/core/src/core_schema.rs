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

async fn definitions(connection: &mut PgConnection) -> Result<Vec<String>, IdentityError> {
    Ok(sqlx::query_scalar(CATALOG).fetch_all(connection).await?)
}

fn fingerprint(definitions: &[String]) -> Vec<u8> {
    let mut hash = Sha256::new();
    for definition in definitions {
        hash.update((definition.len() as u64).to_be_bytes());
        hash.update(definition.as_bytes());
    }
    hash.finalize().to_vec()
}

async fn catalog(connection: &mut PgConnection) -> Result<Vec<u8>, IdentityError> {
    Ok(fingerprint(&definitions(connection).await?))
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

async fn check_components(connection: &mut PgConnection, prior: bool) -> Result<(), IdentityError> {
    let rows = sqlx::query("SELECT name,fingerprint FROM core_meta.component_contract")
        .fetch_all(&mut *connection)
        .await?;
    if rows.len() != COMPONENTS.len() {
        return Err(IdentityError::Storage);
    }
    for (name, schema) in COMPONENTS {
        let expected = if prior && *name == "charging-ledger" {
            PRIOR_CHARGING_LEDGER.to_vec()
        } else {
            Sha256::digest(schema.as_bytes()).to_vec()
        };
        let valid = rows.iter().any(|row| {
            row.try_get::<String, _>("name").ok().as_deref() == Some(name)
                && row.try_get::<Vec<u8>, _>("fingerprint").ok() == Some(expected.clone())
        });
        if !valid {
            return Err(IdentityError::Storage);
        }
    }
    Ok(())
}

async fn check_connection(connection: &mut PgConnection) -> Result<(), IdentityError> {
    IdentityStore::check_schema_connection(connection).await?;
    check_components(connection, false).await?;
    check_usage_index(connection).await?;
    let saved: Vec<u8> = sqlx::query_scalar(
        "SELECT catalog_fingerprint FROM core_meta.runtime_contract WHERE singleton",
    )
    .fetch_one(&mut *connection)
    .await?;
    if saved != catalog(connection).await? {
        return Err(IdentityError::Storage);
    }
    Ok(())
}

pub async fn check(pool: &PgPool) -> Result<(), IdentityError> {
    // One repeatable catalog snapshot, including native and component receipts.
    let mut tx = pool.begin().await?;
    sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
        .execute(&mut *tx)
        .await?;
    check_connection(&mut tx).await?;
    tx.commit().await?;
    Ok(())
}

// The only prior source revision authorized by this one-purpose procedure.
const PRIOR_CHARGING_LEDGER: [u8; 32] = [
    0x7f, 0x84, 0x65, 0x9a, 0xd2, 0xbd, 0x60, 0x26, 0x82, 0x56, 0xa9, 0xf7, 0x69, 0x59, 0x29, 0x9a,
    0x33, 0x91, 0x1a, 0xaa, 0xea, 0xf7, 0x19, 0x41, 0x56, 0xa6, 0xf1, 0x95, 0xab, 0x54, 0xa7, 0x34,
];
const USAGE_INDEX_DDL: &str = "CREATE INDEX ledger_commands_request_operation ON core_charging.ledger_commands USING btree (request_id, operation_id)";

async fn check_usage_index(connection: &mut PgConnection) -> Result<(), IdentityError> {
    let valid: bool = sqlx::query_scalar(r#"
        SELECT coalesce((SELECT
            c.relkind='i' AND c.reloptions IS NULL AND c.reltablespace=0
            AND am.amname='btree' AND tc.relname='ledger_commands' AND tc.relkind='r'
            AND tc.relnamespace=n.oid
            AND NOT i.indisunique AND NOT i.indisprimary AND NOT i.indisexclusion
            AND i.indisvalid AND i.indisready AND i.indislive
            AND i.indnkeyatts=2 AND i.indnatts=2
            AND i.indexprs IS NULL AND i.indpred IS NULL
            AND pg_get_indexdef(i.indexrelid)=
                'CREATE INDEX ledger_commands_request_operation ON core_charging.ledger_commands USING btree (request_id, operation_id)'
        FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
        JOIN pg_index i ON i.indexrelid=c.oid JOIN pg_am am ON am.oid=c.relam
        JOIN pg_class tc ON tc.oid=i.indrelid
        WHERE n.nspname='core_charging' AND c.relname='ledger_commands_request_operation'),false)
    "#).fetch_one(connection).await?;
    if !valid {
        return Err(IdentityError::Storage);
    }
    Ok(())
}

// Only these catalog definitions are introduced by the verified index. Filtering
// a prior snapshot would hide an already-present index, so filter only AFTER DDL.
fn without_usage_index(definitions: Vec<String>) -> Result<Vec<String>, IdentityError> {
    let mut previous = Vec::new();
    for definition in definitions {
        let value: serde_json::Value =
            serde_json::from_str(&definition).map_err(|_| IdentityError::Storage)?;
        if value[1] == "core_charging"
            && value[2] == "ledger_commands_request_operation"
            && matches!(value[0].as_str(), Some("relation" | "column" | "index"))
        {
            continue;
        }
        previous.push(definition);
    }
    Ok(previous)
}

async fn permissions(connection: &mut PgConnection) -> Result<Vec<String>, IdentityError> {
    // Catalog hashing intentionally omits grants/owners. The upgrade separately
    // proves these do not change; financial rows are never update targets.
    Ok(sqlx::query_scalar(r#"
        WITH ns AS (SELECT oid,nspname,nspowner,nspacl FROM pg_namespace
          WHERE nspname NOT IN ('pg_catalog','information_schema') AND left(nspname,3)<>'pg_')
        SELECT definition FROM (
          SELECT json_build_array('schema',nspname,nspowner,nspacl)::text AS definition FROM ns
          UNION ALL SELECT json_build_array('relation',n.nspname,c.relname,c.relowner,c.relacl)::text
          FROM pg_class c JOIN ns n ON n.oid=c.relnamespace
          WHERE NOT (n.nspname='core_charging' AND c.relname='ledger_commands_request_operation')
          UNION ALL SELECT json_build_array('function',n.nspname,p.proname,p.proowner,p.proacl)::text
          FROM pg_proc p JOIN ns n ON n.oid=p.pronamespace
        ) d ORDER BY definition COLLATE "C"
    "#).fetch_all(connection).await?)
}

/// Explicit offline installer-owner operation, never called by startup/requests.
/// The caller supplies the independently approved prior catalog SHA256 (not a
/// detected/default value), using separate owner configuration. One transaction,
/// 15-second total bound, 3-second lock and 5-second statement bounds; no retries.
/// Existing-site execution requires frozen-source independent review first.
pub async fn install_usage_receipt_index(
    pool: &PgPool,
    approved_prior: [u8; 32],
) -> Result<(), IdentityError> {
    tokio::time::timeout(
        std::time::Duration::from_secs(15),
        install_usage_receipt_index_transaction(pool, approved_prior),
    )
    .await
    .map_err(|_| IdentityError::Storage)?
}

async fn install_usage_receipt_index_transaction(
    pool: &PgPool,
    approved_prior: [u8; 32],
) -> Result<(), IdentityError> {
    let mut tx = pool.begin().await?;
    sqlx::raw_sql("SET LOCAL synchronous_commit=on; SET LOCAL lock_timeout='3s'; SET LOCAL statement_timeout='5s'; SET LOCAL idle_in_transaction_session_timeout='10s'")
        .execute(&mut *tx).await?;
    sqlx::query("SELECT pg_advisory_xact_lock(741803120)")
        .execute(&mut *tx)
        .await?;
    sqlx::raw_sql("LOCK TABLE core_charging.ledger_commands IN SHARE MODE; LOCK TABLE core_meta.schema_contract IN SHARE MODE; LOCK TABLE core_meta.component_contract,core_meta.runtime_contract IN ACCESS EXCLUSIVE MODE")
        .execute(&mut *tx).await?;
    // Superuser status is not authority: the login and effective role must both
    // be the actual table/receipt owner, without SET ROLE indirection.
    let owner: bool = sqlx::query_scalar(
        r#"
        SELECT current_user=session_user AND count(*)=4
            AND bool_and(pg_get_userbyid(c.relowner)=current_user)
        FROM pg_class c WHERE c.oid IN (
          'core_charging.ledger_commands'::regclass,'core_meta.schema_contract'::regclass,
          'core_meta.component_contract'::regclass,'core_meta.runtime_contract'::regclass)
    "#,
    )
    .fetch_one(&mut *tx)
    .await?;
    if !owner {
        return Err(IdentityError::Forbidden);
    }
    IdentityStore::check_schema_connection(&mut tx).await?;
    check_components(&mut tx, true).await?;
    let exists: bool = sqlx::query_scalar(
        "SELECT to_regclass('core_charging.ledger_commands_request_operation') IS NOT NULL",
    )
    .fetch_one(&mut *tx)
    .await?;
    if exists {
        return Err(IdentityError::Conflict);
    }
    let prior = definitions(&mut tx).await?;
    let saved: Vec<u8> = sqlx::query_scalar(
        "SELECT catalog_fingerprint FROM core_meta.runtime_contract WHERE singleton",
    )
    .fetch_one(&mut *tx)
    .await?;
    if saved != approved_prior || saved != fingerprint(&prior) {
        return Err(IdentityError::Storage);
    }
    let prior_permissions = permissions(&mut tx).await?;
    sqlx::query(USAGE_INDEX_DDL).execute(&mut *tx).await?;
    check_usage_index(&mut tx).await?;
    let after = definitions(&mut tx).await?;
    if without_usage_index(after.clone())? != prior
        || permissions(&mut tx).await? != prior_permissions
    {
        return Err(IdentityError::Storage);
    }
    let source = COMPONENTS
        .iter()
        .find(|(name, _)| *name == "charging-ledger")
        .ok_or(IdentityError::Storage)?
        .1;
    let changed = sqlx::query("UPDATE core_meta.component_contract SET fingerprint=$1 WHERE name='charging-ledger' AND fingerprint=$2")
        .bind(Sha256::digest(source.as_bytes()).to_vec()).bind(PRIOR_CHARGING_LEDGER.to_vec())
        .execute(&mut *tx).await?;
    if changed.rows_affected() != 1 {
        return Err(IdentityError::Storage);
    }
    let changed = sqlx::query("UPDATE core_meta.runtime_contract SET catalog_fingerprint=$1 WHERE singleton AND catalog_fingerprint=$2")
        .bind(fingerprint(&after)).bind(approved_prior.to_vec()).execute(&mut *tx).await?;
    if changed.rows_affected() != 1 {
        return Err(IdentityError::Storage);
    }
    check_connection(&mut tx).await?;
    tx.commit().await?;
    Ok(())
}

#[cfg(test)]
mod index_tests;
