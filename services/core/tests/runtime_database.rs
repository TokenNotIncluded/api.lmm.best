use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Request, StatusCode},
};
use lmm_core::{
    core_schema,
    identity::IdentityStore,
    legacy_auth::{self, LegacyAuthConfig},
    runtime_database::{self, RequiredCapabilities, RuntimeDatabaseError as Error},
};
use serde_json::{Value, json};
use sqlx::PgPool;
use tower::ServiceExt;

#[path = "common/runtime.rs"]
mod runtime;

#[sqlx::test(migrations = false)]
async fn restricted_role_checks_without_disabled_module_grants(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    // Match the official isolated installer: identity-only runtime has no
    // public USAGE. ACL provisioning must not change deparsed catalog truth.
    sqlx::query("REVOKE USAGE ON SCHEMA public FROM PUBLIC")
        .execute(&pool)
        .await
        .unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    let public_visible: bool = sqlx::query_scalar("SELECT has_schema_privilege('public','USAGE')")
        .fetch_one(&role.pool)
        .await
        .unwrap();
    assert!(!public_visible);
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
            oauth2: false,
        },
    )
    .await
    .unwrap();
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn web_catalogue_fallback_requires_only_three_readonly_projections(pool: PgPool) {
    const ORIGIN: &str = "http://api.lmm.local";
    async fn request(
        app: &Router,
        method: &str,
        path: &str,
        secret: &str,
        body: Option<Value>,
    ) -> (StatusCode, Value) {
        let mut request = Request::builder()
            .method(method)
            .uri(path)
            .header("authorization", format!("Bearer {secret}"))
            .header("origin", ORIGIN);
        let body = match body {
            Some(body) => {
                request = request.header("content-type", "application/json");
                Body::from(body.to_string())
            }
            None => Body::empty(),
        };
        let response = app
            .clone()
            .oneshot(request.body(body).unwrap())
            .await
            .unwrap();
        let status = response.status();
        let bytes = to_bytes(response.into_body(), 65536).await.unwrap();
        (status, serde_json::from_slice(&bytes).unwrap())
    }

    core_schema::init(&pool).await.unwrap();
    // SQLx owns this isolated database. Remove the default inherited permission
    // so the schema-USAGE negative case exercises actual effective authority.
    sqlx::query("REVOKE USAGE ON SCHEMA public FROM PUBLIC")
        .execute(&pool)
        .await
        .unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    role.optional("web_identity").await;
    let requirements = RequiredCapabilities {
        web_identity: true,
        ..RequiredCapabilities::default()
    };
    let session = IdentityStore::from_pool(pool.clone())
        .bootstrap_user(1, 0)
        .await
        .unwrap();
    sqlx::raw_sql(r#"
        INSERT INTO routing_price_versions(version,document) VALUES(1,'{"version":1}');
        INSERT INTO routing_config_versions(version,price_version,document) VALUES(1,1,'{"version":1}');
        INSERT INTO routing_groups(version,name,enabled) VALUES(1,'default',TRUE),(1,'private',TRUE),(1,'disabled',FALSE);
        INSERT INTO routing_routes(version,id,group_name,model,enabled,speeds,max_attempts,retry_on,allow_replay_after_send)
          VALUES(1,1,'default','local-model',TRUE,ARRAY['standard'],1,'{}',FALSE),
                (1,2,'private','private-model',TRUE,ARRAY['standard'],1,'{}',FALSE),
                (1,3,'disabled','disabled-model',TRUE,ARRAY['standard'],1,'{}',FALSE);
        UPDATE routing_price_versions SET sealed=TRUE WHERE version=1;
        UPDATE routing_config_versions SET sealed=TRUE WHERE version=1;
        UPDATE routing_active_release SET config_version=1;
        INSERT INTO core_identity.user_routing_groups(user_id,group_name,source_reference)
          VALUES(1,'default','synthetic-operator'),(1,'disabled','synthetic-operator');
    "#).execute(&pool).await.unwrap();
    // No with_dashboard_routing: this is the real models-disabled SQL fallback,
    // and every HTTP request uses the restricted role rather than the owner.
    let app = legacy_auth::router(
        IdentityStore::from_pool(role.pool.clone()),
        LegacyAuthConfig::new(ORIGIN, &[71; 32])
            .unwrap()
            .with_insecure_local_http()
            .unwrap(),
    );
    let key_input = json!({"name":"Synthetic fallback key","remain_quota":100,"expired_time":-1,"unlimited_quota":false,"group":"default","one_time_reveal":true});
    let paths = [
        "/api/user/self/groups",
        "/api/token/auto-groups",
        "/api/user/models",
    ];
    runtime_database::check(&role.pool, requirements)
        .await
        .unwrap();
    for table in ["routing_active_release", "routing_groups", "routing_routes"] {
        sqlx::raw_sql(&format!(
            "REVOKE SELECT ON public.{table} FROM {}",
            role.name
        ))
        .execute(&pool)
        .await
        .unwrap();
        // Identity-only startup does not require the optional Web catalogue.
        runtime_database::check(&role.pool, RequiredCapabilities::default())
            .await
            .unwrap();
        assert_eq!(
            runtime_database::check(&role.pool, requirements).await,
            Err(Error::MissingPrivileges),
            "missing projection accepted: {table}"
        );
        for path in paths {
            let expected = if table == "routing_routes" && path != "/api/user/models" {
                StatusCode::OK
            } else {
                StatusCode::SERVICE_UNAVAILABLE
            };
            assert_eq!(
                request(&app, "GET", path, &session.secret, None).await.0,
                expected
            );
        }
        if table != "routing_routes" {
            assert_eq!(
                request(
                    &app,
                    "POST",
                    "/api/token",
                    &session.secret,
                    Some(key_input.clone())
                )
                .await
                .0,
                StatusCode::SERVICE_UNAVAILABLE
            );
            let keys: i64 =
                sqlx::query_scalar("SELECT count(*) FROM core_identity.api_key_settings")
                    .fetch_one(&pool)
                    .await
                    .unwrap();
            assert_eq!(keys, 0, "failed validation must not create a key");
        }
        sqlx::raw_sql(&format!("GRANT SELECT ON public.{table} TO {}", role.name))
            .execute(&pool)
            .await
            .unwrap();
        runtime_database::check(&role.pool, requirements)
            .await
            .unwrap();
    }
    sqlx::raw_sql(&format!("REVOKE USAGE ON SCHEMA public FROM {}", role.name))
        .execute(&pool)
        .await
        .unwrap();
    assert_eq!(
        runtime_database::check(&role.pool, requirements).await,
        Err(Error::MissingPrivileges)
    );
    sqlx::raw_sql(&format!("GRANT USAGE ON SCHEMA public TO {}", role.name))
        .execute(&pool)
        .await
        .unwrap();
    runtime_database::check(&role.pool, requirements)
        .await
        .unwrap();

    for command in [
        "INSERT INTO public.routing_active_release(singleton) VALUES(FALSE)",
        "UPDATE public.routing_groups SET enabled=enabled WHERE FALSE",
        "DELETE FROM public.routing_routes WHERE FALSE",
    ] {
        let error = sqlx::query(command).execute(&role.pool).await.unwrap_err();
        assert_eq!(
            error.as_database_error().and_then(|e| e.code()).as_deref(),
            Some("42501")
        );
    }
    for privilege in [
        "UPDATE ON public.routing_active_release",
        "UPDATE(config_version) ON public.routing_active_release",
        "UPDATE ON public.routing_groups",
        "UPDATE(enabled) ON public.routing_groups",
        "UPDATE ON public.routing_routes",
        "UPDATE(enabled) ON public.routing_routes",
    ] {
        sqlx::raw_sql(&format!("GRANT {privilege} TO {}", role.name))
            .execute(&pool)
            .await
            .unwrap();
        assert_eq!(
            runtime_database::check(&role.pool, requirements).await,
            Err(Error::UnsafePrivileges)
        );
        sqlx::raw_sql(&format!("REVOKE {privilege} FROM {}", role.name))
            .execute(&pool)
            .await
            .unwrap();
        runtime_database::check(&role.pool, requirements)
            .await
            .unwrap();
    }
    let other_reads: bool = sqlx::query_scalar(r#"
        SELECT EXISTS(SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
          WHERE n.nspname='public' AND c.relname IN ('routing_credential_refs','routing_price_versions',
            'routing_config_versions','routing_price_rules','routing_upstreams','routing_targets','routing_aliases')
          AND (has_table_privilege(c.oid,'SELECT') OR has_any_column_privilege(c.oid,'SELECT')))
    "#).fetch_one(&role.pool).await.unwrap();
    assert!(
        !other_reads,
        "fallback does not require configuration documents or upstreams"
    );
    let groups = request(&app, "GET", paths[0], &session.secret, None).await;
    assert_eq!(groups.0, StatusCode::OK);
    assert_eq!(
        groups.1["data"],
        json!({"default":{"desc":"default","ratio":"model-specific"}})
    );
    let auto = request(&app, "GET", paths[1], &session.secret, None).await;
    assert_eq!(auto.0, StatusCode::OK);
    assert_eq!(auto.1["data"]["groups"], json!(["default"]));
    let models = request(&app, "GET", paths[2], &session.secret, None).await;
    assert_eq!(models.0, StatusCode::OK);
    assert_eq!(models.1["data"], json!(["local-model"]));
    let created = request(
        &app,
        "POST",
        "/api/token",
        &session.secret,
        Some(key_input.clone()),
    )
    .await;
    assert_eq!(created.0, StatusCode::OK);
    let mut updated = key_input;
    updated["id"] = created.1["data"]["id"].clone();
    updated.as_object_mut().unwrap().remove("one_time_reveal");
    assert_eq!(
        request(
            &app,
            "PUT",
            "/api/token",
            &session.secret,
            Some(updated.clone())
        )
        .await
        .0,
        StatusCode::OK
    );
    updated["group"] = json!("private");
    assert_eq!(
        request(&app, "PUT", "/api/token", &session.secret, Some(updated))
            .await
            .0,
        StatusCode::FORBIDDEN
    );
    core_schema::check(&role.pool).await.unwrap();
    sqlx::query("GRANT USAGE ON SCHEMA public TO PUBLIC")
        .execute(&pool)
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
