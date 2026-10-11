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
    ] {
        assert!(
            runtime_database::check(&role.pool, requirements)
                .await
                .is_err()
        );
    }
    for schema in ["core_charging", "core_events", "core_payments"] {
        role.optional(schema).await;
    }
    runtime_database::check(
        &role.pool,
        RequiredCapabilities {
            models: true,
            events: true,
            payments: true,
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
