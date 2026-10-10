//! A fresh database contract, not a history of schema upgrades.
use super::*;

const SCHEMA: &str = include_str!("../../schema/identity.sql");
const VERSION: i32 = 1;
// Serialize concurrent installers. Ordinary requests do not take this lock.
const INSTALL_LOCK: i64 = 741_803_120;
const HAS_APPLICATION_OBJECTS: &str = r#"
SELECT EXISTS (
    SELECT 1 FROM pg_catalog.pg_namespace n
    WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
      AND left(n.nspname, 3) <> 'pg_'
      AND (n.nspname <> 'public'
        OR EXISTS (SELECT 1 FROM pg_catalog.pg_class c WHERE c.relnamespace = n.oid)
        OR EXISTS (SELECT 1 FROM pg_catalog.pg_proc p WHERE p.pronamespace = n.oid)
        OR EXISTS (SELECT 1 FROM pg_catalog.pg_type t WHERE t.typnamespace = n.oid))
)
"#;

impl IdentityStore {
    /// Explicit offline initialization. Never imports, upgrades, drops, repairs,
    /// or resets a database. Even a second installation on the same DB fails.
    pub async fn init_database(&self) -> Result<()> {
        let mut tx = self.pool.begin().await?;
        sqlx::query("SET LOCAL lock_timeout = '3s'")
            .execute(&mut *tx)
            .await?;
        sqlx::query("SELECT pg_advisory_xact_lock($1)")
            .bind(INSTALL_LOCK)
            .execute(&mut *tx)
            .await?;
        let occupied: bool = sqlx::query_scalar(HAS_APPLICATION_OBJECTS)
            .fetch_one(&mut *tx)
            .await?;
        if occupied {
            return Err(IdentityError::Conflict);
        }
        sqlx::raw_sql(SCHEMA).execute(&mut *tx).await?;
        sqlx::query("INSERT INTO core_meta.schema_contract(version,fingerprint) VALUES ($1,$2)")
            .bind(VERSION)
            .bind(Sha256::digest(SCHEMA.as_bytes()).to_vec())
            .execute(&mut *tx)
            .await?;
        tx.commit().await?;
        Ok(())
    }

    /// Startup checks the installed contract. It never executes DDL.
    pub async fn check_schema(&self) -> Result<()> {
        let row = sqlx::query(
            "SELECT version,fingerprint FROM core_meta.schema_contract WHERE singleton",
        )
        .fetch_one(&self.pool)
        .await?;
        if row.try_get::<i32, _>("version")? == VERSION
            && row.try_get::<Vec<u8>, _>("fingerprint")?
                == Sha256::digest(SCHEMA.as_bytes()).to_vec()
        {
            Ok(())
        } else {
            Err(IdentityError::Storage)
        }
    }
}
