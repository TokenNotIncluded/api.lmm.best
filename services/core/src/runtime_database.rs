//! Read-only startup verification of the runtime role's database authority.
//! Install/import owners must never be used by the network-facing process.
use sqlx::{PgPool, Row};
use std::fmt;

#[derive(Clone, Copy, Default)]
pub struct RequiredCapabilities {
    pub models: bool,
    pub events: bool,
    pub payments: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RuntimeDatabaseError {
    UnsafePrivileges,
    MissingPrivileges,
    Unavailable,
}
impl fmt::Display for RuntimeDatabaseError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            Self::UnsafePrivileges => "core runtime database authority is unsafe",
            Self::MissingPrivileges => "core runtime database authority is incomplete",
            Self::Unavailable => "core runtime database authority could not be verified",
        })
    }
}
impl std::error::Error for RuntimeDatabaseError {}
impl From<sqlx::Error> for RuntimeDatabaseError {
    fn from(_: sqlx::Error) -> Self {
        Self::Unavailable
    }
}

/// No DDL or probe writes: inspect effective privileges, including inherited and
/// SET ROLE paths. PostgreSQL 18 pg_has_role(...,'SET') accounts for membership
/// SET options rather than assuming every membership allows impersonation.
pub async fn check(
    pool: &PgPool,
    required: RequiredCapabilities,
) -> Result<(), RuntimeDatabaseError> {
    let mut transaction = pool.begin().await?;
    sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
        .execute(&mut *transaction)
        .await?;
    sqlx::query("SET LOCAL statement_timeout='1500ms'")
        .execute(&mut *transaction)
        .await?;
    let unsafe_role: bool = sqlx::query_scalar(r#"
        SELECT current_user<>session_user
        OR EXISTS(SELECT FROM pg_roles r WHERE r.rolname<>current_user AND (
          pg_has_role(current_user,r.oid,'SET') OR pg_has_role(session_user,r.oid,'SET')
          OR pg_has_role(current_user,r.oid,'MEMBER WITH ADMIN OPTION') OR pg_has_role(session_user,r.oid,'MEMBER WITH ADMIN OPTION')))
        OR EXISTS(SELECT FROM pg_roles r
          WHERE (r.rolsuper OR r.rolbypassrls OR r.rolcreaterole OR r.rolcreatedb OR r.rolreplication
            OR r.rolname IN ('pg_read_server_files','pg_write_server_files','pg_execute_server_program','pg_read_all_data','pg_write_all_data'))
          AND (r.rolname IN (current_user,session_user) OR pg_has_role(current_user,r.oid,'USAGE') OR pg_has_role(session_user,r.oid,'SET')
            OR pg_has_role(current_user,r.oid,'MEMBER WITH ADMIN OPTION') OR pg_has_role(session_user,r.oid,'MEMBER WITH ADMIN OPTION')))
        OR has_database_privilege(current_database(),'CREATE,TEMP')
        OR EXISTS(SELECT FROM pg_namespace n WHERE n.nspname NOT IN ('pg_catalog','information_schema')
          AND left(n.nspname,3)<>'pg_' AND has_schema_privilege(n.oid,'CREATE'))
        OR EXISTS(SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
          WHERE c.relkind IN ('r','p','v','f') AND n.nspname NOT IN ('pg_catalog','information_schema')
            AND left(n.nspname,3)<>'pg_' AND has_table_privilege(c.oid,'TRIGGER'))
        OR EXISTS(WITH owners AS (
          SELECT datdba AS owner FROM pg_database WHERE datname=current_database()
          UNION SELECT nspowner FROM pg_namespace WHERE nspname NOT IN ('pg_catalog','information_schema') AND left(nspname,3)<>'pg_'
          UNION SELECT c.relowner FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
            WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'
          UNION SELECT p.proowner FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
            WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'
          UNION SELECT t.typowner FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace
            WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'
        ) SELECT FROM owners o WHERE pg_has_role(current_user,o.owner,'USAGE') OR pg_has_role(session_user,o.owner,'SET')
          OR pg_has_role(current_user,o.owner,'MEMBER WITH ADMIN OPTION') OR pg_has_role(session_user,o.owner,'MEMBER WITH ADMIN OPTION'))
    "#).fetch_one(&mut *transaction).await?;
    if unsafe_role {
        return Err(RuntimeDatabaseError::UnsafePrivileges);
    }

    let forbidden_write: bool = sqlx::query_scalar(r#"
        SELECT EXISTS(SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
          WHERE c.relkind IN ('r','p','v','m','f') AND (
            n.nspname IN ('core_meta','core_migration') OR
            (n.nspname='core_billing' AND c.relname IN ('ledger_accounts','balance_state','ledger_operations','ledger_journals','ledger_entries')))
          AND (has_table_privilege(c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER,REFERENCES,MAINTAIN')
            OR has_any_column_privilege(c.oid,'INSERT,UPDATE,REFERENCES')))
    "#).fetch_one(&mut *transaction).await?;
    if forbidden_write {
        return Err(RuntimeDatabaseError::UnsafePrivileges);
    }

    // Identity is always mounted when the runtime has a database. Normal identity
    // operations must work; unsupported optional modules need no extra grants.
    let identity_complete: bool = sqlx::query_scalar(r#"
        SELECT has_schema_privilege('core_identity','USAGE')
          AND has_schema_privilege('core_meta','USAGE')
          AND has_schema_privilege('core_billing','USAGE')
          AND NOT EXISTS(SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
            CROSS JOIN (VALUES ('SELECT'),('INSERT'),('UPDATE'),('DELETE')) AS p(privilege)
            WHERE n.nspname='core_identity' AND c.relkind IN ('r','p') AND NOT has_table_privilege(c.oid,p.privilege))
          AND NOT EXISTS(SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
            WHERE n.nspname='core_identity' AND CASE WHEN c.relkind='S' THEN NOT has_sequence_privilege(c.oid,'USAGE') ELSE FALSE END)
          AND NOT EXISTS(SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
            WHERE (n.nspname='core_meta' OR (n.nspname='core_billing' AND c.relkind IN ('r','p')))
              AND c.relkind IN ('r','p') AND NOT has_table_privilege(c.oid,'SELECT'))
          AND has_table_privilege('core_billing.account_policies','SELECT')
          AND has_table_privilege('core_billing.account_policies','INSERT')
          AND has_table_privilege('core_billing.account_policies','UPDATE')
          AND has_table_privilege('core_billing.account_policies','DELETE')
          AND has_function_privilege('core_billing.open_ledger_wallet(bigint)','EXECUTE')
          AND has_function_privilege('core_billing.post_ledger(jsonb)','EXECUTE')
    "#).fetch_one(&mut *transaction).await?;
    if !identity_complete {
        return Err(RuntimeDatabaseError::MissingPrivileges);
    }

    for (enabled, schema) in [
        (required.models, "core_charging"),
        (required.events, "core_events"),
        (required.payments, "core_payments"),
    ] {
        if !enabled {
            continue;
        }
        let row = sqlx::query(r#"SELECT has_schema_privilege($1,'USAGE') AS usable,
          EXISTS(SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind IN ('r','p')) AS present,
          NOT EXISTS(SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
            CROSS JOIN (VALUES ('SELECT'),('INSERT'),('UPDATE')) AS p(privilege)
            WHERE n.nspname=$1 AND c.relkind IN ('r','p') AND NOT has_table_privilege(c.oid,p.privilege))
          AND NOT EXISTS(SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
            WHERE n.nspname=$1 AND CASE WHEN c.relkind='S' THEN NOT has_sequence_privilege(c.oid,'USAGE') ELSE FALSE END) AS complete"#)
            .bind(schema).fetch_one(&mut *transaction).await?;
        if !row.try_get::<bool, _>("usable")?
            || !row.try_get::<bool, _>("present")?
            || !row.try_get::<bool, _>("complete")?
        {
            return Err(RuntimeDatabaseError::MissingPrivileges);
        }
    }
    transaction.commit().await?;
    Ok(())
}
