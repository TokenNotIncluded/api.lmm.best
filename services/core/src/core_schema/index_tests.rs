//! Newly constructed SQLx-owned current-schema fixtures with only the charging
//! index removed. These do not establish historical identity-schema upgrades.
use super::*;

async fn prior(pool: &PgPool) -> [u8; 32] {
    let mut tx = pool.begin().await.unwrap();
    for (name, source) in COMPONENTS {
        let source = if *name == "charging-ledger" {
            let old = source.replace("CREATE INDEX ledger_commands_request_operation\n    ON core_charging.ledger_commands USING btree (request_id, operation_id);\n", "");
            assert_eq!(
                Sha256::digest(old.as_bytes()).as_slice(),
                PRIOR_CHARGING_LEDGER
            );
            old
        } else {
            source.to_string()
        };
        sqlx::raw_sql(&source).execute(&mut *tx).await.unwrap();
        // Receipt tables are created below, after all component sources.
    }
    sqlx::raw_sql(CONTRACT).execute(&mut *tx).await.unwrap();
    IdentityStore::record_schema_contract(&mut tx)
        .await
        .unwrap();
    for (name, source) in COMPONENTS {
        let hash = if *name == "charging-ledger" {
            PRIOR_CHARGING_LEDGER.to_vec()
        } else {
            Sha256::digest(source.as_bytes()).to_vec()
        };
        sqlx::query("INSERT INTO core_meta.component_contract VALUES ($1,$2)")
            .bind(name)
            .bind(hash)
            .execute(&mut *tx)
            .await
            .unwrap();
    }
    let hash = catalog(&mut tx).await.unwrap();
    sqlx::query("INSERT INTO core_meta.runtime_contract(catalog_fingerprint) VALUES ($1)")
        .bind(&hash)
        .execute(&mut *tx)
        .await
        .unwrap();
    tx.commit().await.unwrap();
    hash.try_into().unwrap()
}

async fn snapshot(pool: &PgPool) -> Vec<String> {
    sqlx::query_scalar(r#"
      SELECT value FROM (
        SELECT json_build_array('component',name,encode(fingerprint,'hex'))::text AS value FROM core_meta.component_contract
        UNION ALL SELECT json_build_array('runtime',singleton,encode(catalog_fingerprint,'hex'))::text FROM core_meta.runtime_contract
        UNION ALL SELECT json_build_array('identity',singleton,version,encode(fingerprint,'hex'))::text FROM core_meta.schema_contract
        UNION ALL SELECT json_build_array('journal',to_jsonb(j))::text FROM core_billing.ledger_journals j
        UNION ALL SELECT json_build_array('entry',to_jsonb(e))::text FROM core_billing.ledger_entries e
        UNION ALL SELECT json_build_array('wallet',to_jsonb(w))::text FROM core_billing.ledger_accounts w
        UNION ALL SELECT json_build_array('balance',to_jsonb(b))::text FROM core_billing.balance_state b
        UNION ALL SELECT json_build_array('operation',to_jsonb(o))::text FROM core_billing.ledger_operations o
        UNION ALL SELECT json_build_array('request',to_jsonb(r))::text FROM core_charging.requests r
        UNION ALL SELECT json_build_array('command',to_jsonb(c))::text FROM core_charging.ledger_commands c
      ) rows ORDER BY value COLLATE "C"
    "#).fetch_all(pool).await.unwrap()
}

async fn unchanged(pool: &PgPool, before: &[String]) {
    assert_eq!(snapshot(pool).await, before);
    let index: bool = sqlx::query_scalar(
        "SELECT to_regclass('core_charging.ledger_commands_request_operation') IS NOT NULL",
    )
    .fetch_one(pool)
    .await
    .unwrap();
    assert!(!index);
}

#[sqlx::test(migrations = false)]
async fn current_schema_index_predecessor_upgrade_changes_only_verified_index_and_two_receipts(
    pool: PgPool,
) {
    let approved = prior(&pool).await;
    let prior_hex = approved
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect::<String>();
    assert_eq!(
        prior_hex, "9ce6c5120645792608d4466ef9fe4b6f91add7ec082670d276bffbd94f48c14a",
        "current-schema index-predecessor fixture must match its reviewed canonical catalog"
    );
    println!("prior catalog SHA256={prior_hex}");
    let store = IdentityStore::from_pool(pool.clone());
    store.check_schema().await.unwrap();
    let credential = store.bootstrap_user(7, 0).await.unwrap();
    let owner = store
        .authorize(&credential.secret)
        .await
        .unwrap()
        .owner_account_id;
    let ledger = crate::billing::ledger::Ledger::from_pool(pool.clone());
    ledger.open_wallet(owner).await.unwrap();
    // Nonempty financial data makes preservation assertions meaningful.
    let request = crate::billing::ledger::Request {
        scope: "index-fixture".into(),
        operation_key: "credit".into(),
        actor_user_id: Some(7),
        reason: "Synthetic prior-schema fixture".into(),
        action: crate::billing::ledger::Action::Credit {
            account_id: owner,
            amount_units: crate::billing::Credits::try_from(1000).unwrap(),
            payment_reference: "index-fixture-credit".into(),
        },
    };
    ledger.execute(&request).await.unwrap();
    let before = snapshot(&pool).await;
    let grants = permissions(&mut pool.acquire().await.unwrap())
        .await
        .unwrap();
    assert!(
        check(&pool).await.is_err(),
        "startup must not repair old schema"
    );
    unchanged(&pool, &before).await;
    // The retired eleven-component and earlier catalogs are not this installed
    // twelve-component catalog, even when approval matches the saved receipt.
    for retired_hex in [
        "e8fa053daca7741952caa4c60e27071652f600b4fa48f4b7631ea54ee801df7b",
        "5510e0bee0e5659ff940b798c9d6d3c69aea220734d9f73577c847b6841254b7",
    ] {
        let retired: [u8; 32] = std::array::from_fn(|i| {
            u8::from_str_radix(&retired_hex[i * 2..i * 2 + 2], 16).unwrap()
        });
        for (saved, approval) in [(approved, retired), (retired, retired), (retired, approved)] {
            sqlx::query(
                "UPDATE core_meta.runtime_contract SET catalog_fingerprint=$1 WHERE singleton",
            )
            .bind(saved.to_vec())
            .execute(&pool)
            .await
            .unwrap();
            let before_refusal = snapshot(&pool).await;
            let before_catalog = catalog(&mut pool.acquire().await.unwrap()).await.unwrap();
            assert!(
                install_usage_receipt_index(&pool, approval).await.is_err(),
                "retired catalog receipt or approval must be rejected"
            );
            unchanged(&pool, &before_refusal).await;
            assert_eq!(
                catalog(&mut pool.acquire().await.unwrap()).await.unwrap(),
                before_catalog,
                "retired catalog refusal changed definitions"
            );
            assert_eq!(
                permissions(&mut pool.acquire().await.unwrap())
                    .await
                    .unwrap(),
                grants,
                "retired catalog refusal changed permissions"
            );
        }
    }
    sqlx::query("UPDATE core_meta.runtime_contract SET catalog_fingerprint=$1 WHERE singleton")
        .bind(approved.to_vec())
        .execute(&pool)
        .await
        .unwrap();
    unchanged(&pool, &before).await;
    install_usage_receipt_index(&pool, approved).await.unwrap();
    check(&pool).await.unwrap();
    store.check_schema().await.unwrap();
    let financial = |rows: Vec<String>| {
        rows.into_iter()
            .filter(|s| !s.starts_with("[\"component\"") && !s.starts_with("[\"runtime\""))
            .collect::<Vec<_>>()
    };
    assert_eq!(financial(before), financial(snapshot(&pool).await));
    assert_eq!(
        permissions(&mut pool.acquire().await.unwrap())
            .await
            .unwrap(),
        grants
    );
    assert!(
        install_usage_receipt_index(&pool, approved).await.is_err(),
        "no retry/reinstallation"
    );
}

#[sqlx::test(migrations = false)]
async fn upgrade_rejects_wrong_approval_component_catalog_and_native_receipts(pool: PgPool) {
    let approved = prior(&pool).await;
    let before = snapshot(&pool).await;
    assert!(install_usage_receipt_index(&pool, [0; 32]).await.is_err());
    unchanged(&pool, &before).await;
    for sql in [
        "UPDATE core_meta.component_contract SET fingerprint=decode(repeat('00',32),'hex') WHERE name='ledger'",
        "UPDATE core_meta.component_contract SET fingerprint=decode(repeat('00',32),'hex') WHERE name='charging-ledger'",
        "UPDATE core_meta.schema_contract SET version=version+1",
        "UPDATE core_meta.runtime_contract SET catalog_fingerprint=decode(repeat('00',32),'hex')",
        "ALTER TABLE core_charging.ledger_commands ADD COLUMN unexpected TEXT",
        "INSERT INTO core_meta.component_contract VALUES ('unexpected',decode(repeat('00',32),'hex'))",
    ] {
        let mut connection = pool.acquire().await.unwrap();
        sqlx::query("BEGIN")
            .execute(&mut *connection)
            .await
            .unwrap();
        sqlx::query(sql).execute(&mut *connection).await.unwrap();
        sqlx::query("COMMIT")
            .execute(&mut *connection)
            .await
            .unwrap();
        drop(connection);
        let drift = snapshot(&pool).await;
        assert!(
            install_usage_receipt_index(&pool, approved).await.is_err(),
            "{sql}"
        );
        unchanged(&pool, &drift).await;
        let undo = match sql {
            s if s.contains("name='ledger'") => Some((
                "ledger",
                COMPONENTS.iter().find(|(n, _)| *n == "ledger").unwrap().1,
            )),
            s if s.contains("name='charging-ledger'") => None,
            _ => None,
        };
        if let Some((name, source)) = undo {
            sqlx::query("UPDATE core_meta.component_contract SET fingerprint=$1 WHERE name=$2")
                .bind(Sha256::digest(source.as_bytes()).to_vec())
                .bind(name)
                .execute(&pool)
                .await
                .unwrap();
        } else if sql.contains("name='charging-ledger'") {
            sqlx::query("UPDATE core_meta.component_contract SET fingerprint=$1 WHERE name='charging-ledger'")
                .bind(PRIOR_CHARGING_LEDGER.to_vec()).execute(&pool).await.unwrap();
        } else if sql.contains("version=version+1") {
            sqlx::query("UPDATE core_meta.schema_contract SET version=version-1")
                .execute(&pool)
                .await
                .unwrap();
        } else if sql.contains("runtime_contract") {
            sqlx::query("UPDATE core_meta.runtime_contract SET catalog_fingerprint=$1")
                .bind(approved.to_vec())
                .execute(&pool)
                .await
                .unwrap();
        } else if sql.contains("ADD COLUMN") {
            sqlx::query("ALTER TABLE core_charging.ledger_commands DROP COLUMN unexpected")
                .execute(&pool)
                .await
                .unwrap();
        } else {
            sqlx::query("DELETE FROM core_meta.component_contract WHERE name='unexpected'")
                .execute(&pool)
                .await
                .unwrap();
        }
        unchanged(&pool, &before).await;
    }
}

#[sqlx::test(migrations = false)]
async fn startup_required_shape_cannot_be_bypassed_by_re_receipting(pool: PgPool) {
    init(&pool).await.unwrap();
    for ddl in [
        "", // missing
        "CREATE INDEX ledger_commands_request_operation ON core_charging.ledger_commands(operation_id,request_id)",
        "CREATE UNIQUE INDEX ledger_commands_request_operation ON core_charging.ledger_commands(request_id,operation_id)",
        "CREATE INDEX ledger_commands_request_operation ON core_charging.ledger_commands(request_id,operation_id) WHERE request_id<>''",
        "CREATE INDEX ledger_commands_request_operation ON core_charging.ledger_commands(request_id,operation_id) INCLUDE(created_at)",
        "CREATE INDEX ledger_commands_request_operation ON core_charging.ledger_commands(request_id,operation_id) WITH(fillfactor=90)",
        "CREATE INDEX ledger_commands_request_operation ON core_charging.ledger_commands(request_id DESC,operation_id)",
    ] {
        sqlx::query("DROP INDEX IF EXISTS core_charging.ledger_commands_request_operation")
            .execute(&pool)
            .await
            .unwrap();
        if !ddl.is_empty() {
            sqlx::query(ddl).execute(&pool).await.unwrap();
        }
        let hash = catalog(&mut pool.acquire().await.unwrap()).await.unwrap();
        sqlx::query("UPDATE core_meta.runtime_contract SET catalog_fingerprint=$1")
            .bind(hash)
            .execute(&pool)
            .await
            .unwrap();
        assert!(check(&pool).await.is_err(), "{ddl}");
    }
    sqlx::query("DROP INDEX core_charging.ledger_commands_request_operation")
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query(USAGE_INDEX_DDL).execute(&pool).await.unwrap();
    let hash = catalog(&mut pool.acquire().await.unwrap()).await.unwrap();
    sqlx::query("UPDATE core_meta.runtime_contract SET catalog_fingerprint=$1")
        .bind(hash)
        .execute(&pool)
        .await
        .unwrap();
    check(&pool).await.unwrap();
    // Disposable superuser fixture only: exercise readiness/validity omitted by
    // pg_get_indexdef. Restore catalog flags before fixture teardown.
    for flag in ["indisvalid", "indisready"] {
        sqlx::query(&format!("UPDATE pg_index SET {flag}=false WHERE indexrelid='core_charging.ledger_commands_request_operation'::regclass"))
            .execute(&pool).await.unwrap();
        assert!(check(&pool).await.is_err());
        sqlx::query(&format!("UPDATE pg_index SET {flag}=true WHERE indexrelid='core_charging.ledger_commands_request_operation'::regclass"))
            .execute(&pool).await.unwrap();
    }
    check(&pool).await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn upgrade_rejects_existing_index_even_with_approved_catalog(pool: PgPool) {
    let _ = prior(&pool).await;
    for ddl in [
        USAGE_INDEX_DDL,
        "CREATE INDEX ledger_commands_request_operation ON core_charging.ledger_commands(operation_id,request_id)",
    ] {
        sqlx::query(ddl).execute(&pool).await.unwrap();
        let hash = catalog(&mut pool.acquire().await.unwrap()).await.unwrap();
        sqlx::query("UPDATE core_meta.runtime_contract SET catalog_fingerprint=$1")
            .bind(&hash)
            .execute(&pool)
            .await
            .unwrap();
        let before = snapshot(&pool).await;
        assert!(
            install_usage_receipt_index(&pool, hash.try_into().unwrap())
                .await
                .is_err()
        );
        assert_eq!(snapshot(&pool).await, before);
        sqlx::query("DROP INDEX core_charging.ledger_commands_request_operation")
            .execute(&pool)
            .await
            .unwrap();
    }
}

#[sqlx::test(migrations = false)]
async fn upgrade_lock_timeout_and_cancellation_leave_no_partial_index_or_receipt(pool: PgPool) {
    let approved = prior(&pool).await;
    let before = snapshot(&pool).await;
    let mut blocker = pool.begin().await.unwrap();
    sqlx::query("LOCK TABLE core_charging.ledger_commands IN ROW EXCLUSIVE MODE")
        .execute(&mut *blocker)
        .await
        .unwrap();
    let started = std::time::Instant::now();
    assert!(install_usage_receipt_index(&pool, approved).await.is_err());
    assert!(started.elapsed() < std::time::Duration::from_secs(5));
    blocker.rollback().await.unwrap();
    unchanged(&pool, &before).await;
    let mut blocker = pool.begin().await.unwrap();
    sqlx::query("SELECT pg_advisory_xact_lock(741803120)")
        .execute(&mut *blocker)
        .await
        .unwrap();
    assert!(
        tokio::time::timeout(
            std::time::Duration::from_millis(150),
            install_usage_receipt_index(&pool, approved)
        )
        .await
        .is_err()
    );
    blocker.rollback().await.unwrap();
    unchanged(&pool, &before).await;
    install_usage_receipt_index(&pool, approved).await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn upgrade_rejects_runtime_role_and_unrelated_superuser(pool: PgPool) {
    let approved = prior(&pool).await;
    let before = snapshot(&pool).await;
    let name = format!(
        "index_other_{}_{:x}",
        std::process::id(),
        rand::random::<u64>()
    );
    let password = format!(
        "{:032x}{:032x}",
        rand::random::<u128>(),
        rand::random::<u128>()
    );
    sqlx::query(&format!(
        "CREATE ROLE {name} LOGIN SUPERUSER PASSWORD '{password}'"
    ))
    .execute(&pool)
    .await
    .unwrap();
    let options = pool
        .connect_options()
        .as_ref()
        .clone()
        .username(&name)
        .password(&password);
    let unrelated = sqlx::postgres::PgPoolOptions::new()
        .max_connections(1)
        .connect_with(options)
        .await
        .unwrap();
    assert!(
        install_usage_receipt_index(&unrelated, approved)
            .await
            .is_err()
    );
    unrelated.close().await;
    sqlx::query(&format!("DROP ROLE {name}"))
        .execute(&pool)
        .await
        .unwrap();
    unchanged(&pool, &before).await;
    let role = runtime::RuntimeRole::create(&pool).await;
    role.optional("core_charging").await;
    assert!(
        role.url
            .contains(pool.connect_options().get_database().unwrap())
    );
    assert!(
        install_usage_receipt_index(&role.pool, approved)
            .await
            .is_err()
    );
    unchanged(&pool, &before).await;
    role.cleanup().await;
}

#[path = "../../tests/common/runtime.rs"]
mod runtime;
