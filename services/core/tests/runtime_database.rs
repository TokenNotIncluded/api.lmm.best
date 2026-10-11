use lmm_core::{
    core_schema,
    runtime_database::{self, RequiredCapabilities, RuntimeDatabaseError as Error},
};
use sqlx::PgPool;

#[path = "common/runtime.rs"]
mod runtime;

#[sqlx::test(migrations = false)]
async fn restricted_role_checks_without_disabled_module_grants(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    assert_eq!(
        reqwest::Url::parse(&role.url).unwrap().username(),
        role.name
    );
    runtime_database::check(&role.pool, RequiredCapabilities::default())
        .await
        .unwrap();
    core_schema::check(&role.pool).await.unwrap();
    for requirements in [
        RequiredCapabilities {
            models: true,
            ..RequiredCapabilities::default()
        },
        RequiredCapabilities {
            events: true,
            ..RequiredCapabilities::default()
        },
        RequiredCapabilities {
            payments: true,
            ..RequiredCapabilities::default()
        },
        RequiredCapabilities {
            console: true,
            ..RequiredCapabilities::default()
        },
        RequiredCapabilities {
            commerce: true,
            ..RequiredCapabilities::default()
        },
        RequiredCapabilities {
            web_identity: true,
            ..RequiredCapabilities::default()
        },
    ] {
        assert!(
            runtime_database::check(&role.pool, requirements)
                .await
                .is_err()
        );
    }
    role.optional("web_identity").await;
    runtime_database::check(
        &role.pool,
        RequiredCapabilities {
            web_identity: true,
            ..RequiredCapabilities::default()
        },
    )
    .await
    .unwrap();
    let model_writes: bool =
        sqlx::query_scalar("SELECT has_table_privilege('core_charging.requests','INSERT,UPDATE')")
            .fetch_one(&role.pool)
            .await
            .unwrap();
    assert!(
        !model_writes,
        "Web key management must not require model request write authority"
    );
    for schema in [
        "core_charging",
        "core_events",
        "core_payments",
        "core_console",
        "core_model",
        "core_commerce",
    ] {
        role.optional(schema).await;
    }
    runtime_database::check(
        &role.pool,
        RequiredCapabilities {
            models: true,
            events: true,
            payments: true,
            console: true,
            commerce: true,
            web_identity: true,
        },
    )
    .await
    .unwrap();
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn runtime_rejects_superuser_owner_and_direct_or_column_ledger_writes(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    assert_eq!(
        runtime_database::check(&pool, RequiredCapabilities::default()).await,
        Err(Error::UnsafePrivileges)
    );
    let role = runtime::RuntimeRole::create(&pool).await;
    for (grant, revoke) in [
        (
            "GRANT UPDATE ON core_billing.balance_state TO",
            "REVOKE UPDATE ON core_billing.balance_state FROM",
        ),
        (
            "GRANT UPDATE(balance_units) ON core_billing.balance_state TO",
            "REVOKE UPDATE(balance_units) ON core_billing.balance_state FROM",
        ),
        (
            "GRANT INSERT ON core_meta.component_contract TO",
            "REVOKE INSERT ON core_meta.component_contract FROM",
        ),
        (
            "GRANT UPDATE ON core_migration.import_runs TO",
            "REVOKE UPDATE ON core_migration.import_runs FROM",
        ),
        (
            "GRANT CREATE ON SCHEMA public TO",
            "REVOKE CREATE ON SCHEMA public FROM",
        ),
    ] {
        sqlx::raw_sql(&format!("{grant} {}", role.name))
            .execute(&pool)
            .await
            .unwrap();
        assert_eq!(
            runtime_database::check(&role.pool, RequiredCapabilities::default()).await,
            Err(Error::UnsafePrivileges)
        );
        sqlx::raw_sql(&format!("{revoke} {}", role.name))
            .execute(&pool)
            .await
            .unwrap();
        runtime_database::check(&role.pool, RequiredCapabilities::default())
            .await
            .unwrap();
    }
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn runtime_rejects_set_role_inherited_owner_and_admin_paths(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    let owner = format!("lmm_test_owner_{:016x}", rand::random::<u64>());
    let schema = format!("lmm_test_owned_{:016x}", rand::random::<u64>());
    sqlx::raw_sql(&format!(
        "CREATE ROLE {owner} NOLOGIN; CREATE SCHEMA {schema} AUTHORIZATION {owner}"
    ))
    .execute(&pool)
    .await
    .unwrap();
    runtime_database::check(&role.pool, RequiredCapabilities::default())
        .await
        .unwrap();
    for options in [
        "WITH INHERIT FALSE, SET TRUE",
        "WITH INHERIT TRUE, SET FALSE",
        "WITH INHERIT FALSE, SET FALSE, ADMIN TRUE",
    ] {
        sqlx::raw_sql(&format!("GRANT {owner} TO {} {options}", role.name))
            .execute(&pool)
            .await
            .unwrap();
        assert_eq!(
            runtime_database::check(&role.pool, RequiredCapabilities::default()).await,
            Err(Error::UnsafePrivileges)
        );
        sqlx::raw_sql(&format!("REVOKE {owner} FROM {}", role.name))
            .execute(&pool)
            .await
            .unwrap();
    }
    sqlx::raw_sql(&format!("DROP SCHEMA {schema}; DROP ROLE {owner}"))
        .execute(&pool)
        .await
        .unwrap();
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn missing_ledger_execute_is_rejected_and_check_never_changes_grants(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    sqlx::raw_sql(&format!(
        "REVOKE EXECUTE ON FUNCTION core_billing.post_ledger(jsonb) FROM {}",
        role.name
    ))
    .execute(&pool)
    .await
    .unwrap();
    assert_eq!(
        runtime_database::check(&role.pool, RequiredCapabilities::default()).await,
        Err(Error::MissingPrivileges)
    );
    let allowed: bool = sqlx::query_scalar(
        "SELECT has_function_privilege('core_billing.post_ledger(jsonb)','EXECUTE')",
    )
    .fetch_one(&role.pool)
    .await
    .unwrap();
    assert!(!allowed, "verification must not repair or grant authority");
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn noinherit_set_role_cannot_hide_a_nonowner_groups_ledger_authority(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    let group = format!("lmm_test_writer_{:016x}", rand::random::<u64>());
    sqlx::raw_sql(&format!("CREATE ROLE {group} NOLOGIN; GRANT UPDATE ON core_billing.balance_state TO {group}; GRANT {group} TO {} WITH INHERIT FALSE, SET TRUE",role.name))
        .execute(&pool).await.unwrap();
    let directly_allowed: bool =
        sqlx::query_scalar("SELECT has_table_privilege('core_billing.balance_state','UPDATE')")
            .fetch_one(&role.pool)
            .await
            .unwrap();
    assert!(
        !directly_allowed,
        "the test must exercise hidden SET ROLE authority"
    );
    assert_eq!(
        runtime_database::check(&role.pool, RequiredCapabilities::default()).await,
        Err(Error::UnsafePrivileges)
    );
    sqlx::raw_sql(&format!(
        "REVOKE {group} FROM {}; DROP OWNED BY {group}; DROP ROLE {group}",
        role.name
    ))
    .execute(&pool)
    .await
    .unwrap();
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn new_evidence_and_import_tables_reject_table_and_column_write_grants(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    for (grant, revoke) in [
        (
            "UPDATE ON core_console.initialization",
            "UPDATE ON core_console.initialization",
        ),
        (
            "UPDATE(revision) ON core_console.announcement_reads",
            "UPDATE(revision) ON core_console.announcement_reads",
        ),
        (
            "INSERT ON core_model.output_checkpoints",
            "INSERT ON core_model.output_checkpoints",
        ),
        (
            "UPDATE ON core_payments.accepted_proofs",
            "UPDATE ON core_payments.accepted_proofs",
        ),
        (
            "DELETE ON core_payments.refund_holds",
            "DELETE ON core_payments.refund_holds",
        ),
        (
            "TRUNCATE ON core_payments.intents",
            "TRUNCATE ON core_payments.intents",
        ),
        (
            "INSERT ON core_billing.opening_imports",
            "INSERT ON core_billing.opening_imports",
        ),
        (
            "UPDATE ON core_billing.account_opening_counters",
            "UPDATE ON core_billing.account_opening_counters",
        ),
        (
            "INSERT ON core_charging.budget_opening_consumption",
            "INSERT ON core_charging.budget_opening_consumption",
        ),
        (
            "UPDATE ON core_commerce.orders",
            "UPDATE ON core_commerce.orders",
        ),
        (
            "UPDATE ON core_charging.external_consumption",
            "UPDATE ON core_charging.external_consumption",
        ),
        (
            "INSERT ON core_identity.developer_access",
            "INSERT ON core_identity.developer_access",
        ),
        (
            "EXECUTE ON FUNCTION core_billing.import_opening_balance(jsonb)",
            "EXECUTE ON FUNCTION core_billing.import_opening_balance(jsonb)",
        ),
        (
            "EXECUTE ON FUNCTION core_billing.import_account_opening_counters(jsonb)",
            "EXECUTE ON FUNCTION core_billing.import_account_opening_counters(jsonb)",
        ),
    ] {
        sqlx::raw_sql(&format!("GRANT {grant} TO {}", role.name))
            .execute(&pool)
            .await
            .unwrap();
        assert_eq!(
            runtime_database::check(&role.pool, RequiredCapabilities::default()).await,
            Err(Error::UnsafePrivileges),
            "unexpected grant accepted: {grant}"
        );
        sqlx::raw_sql(&format!("REVOKE {revoke} FROM {}", role.name))
            .execute(&pool)
            .await
            .unwrap();
        runtime_database::check(&role.pool, RequiredCapabilities::default())
            .await
            .unwrap();
    }
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn security_definer_public_execute_and_search_path_are_rejected(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    sqlx::query("GRANT EXECUTE ON FUNCTION core_billing.post_ledger(jsonb) TO PUBLIC")
        .execute(&pool)
        .await
        .unwrap();
    assert_eq!(
        runtime_database::check(&role.pool, RequiredCapabilities::default()).await,
        Err(Error::UnsafePrivileges)
    );
    sqlx::query("REVOKE EXECUTE ON FUNCTION core_billing.post_ledger(jsonb) FROM PUBLIC")
        .execute(&pool)
        .await
        .unwrap();
    runtime_database::check(&role.pool, RequiredCapabilities::default())
        .await
        .unwrap();
    sqlx::query("ALTER FUNCTION core_billing.post_ledger(jsonb) SET search_path=public,pg_catalog")
        .execute(&pool)
        .await
        .unwrap();
    assert_eq!(
        runtime_database::check(&role.pool, RequiredCapabilities::default()).await,
        Err(Error::UnsafePrivileges)
    );
    role.cleanup().await;
}
