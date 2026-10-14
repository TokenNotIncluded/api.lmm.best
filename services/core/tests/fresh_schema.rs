use lmm_core::{
    accounts::{Account, AccountKind},
    funding::FundingPolicy,
    identity::{IdentityError as E, IdentityStore},
};
use sqlx::PgPool;

#[sqlx::test(migrations = false)]
async fn full_core_contract_includes_new_components_and_does_not_repair_missing_ones(pool: PgPool) {
    lmm_core::core_schema::init(&pool).await.unwrap();
    lmm_core::core_schema::check(&pool).await.unwrap();
    let components: Vec<String> =
        sqlx::query_scalar("SELECT name FROM core_meta.component_contract ORDER BY name")
            .fetch_all(&pool)
            .await
            .unwrap();
    assert_eq!(components.len(), lmm_core::core_schema::component_count());
    for name in ["console", "model-gateway", "commerce"] {
        assert!(components.iter().any(|entry| entry == name));
    }
    sqlx::raw_sql("DROP SCHEMA core_model CASCADE; DELETE FROM core_meta.component_contract WHERE name='model-gateway'")
        .execute(&pool).await.unwrap();
    assert!(lmm_core::core_schema::check(&pool).await.is_err());
    assert!(lmm_core::core_schema::init(&pool).await.is_err());
    let repaired: bool =
        sqlx::query_scalar("SELECT EXISTS(SELECT FROM pg_namespace WHERE nspname='core_model')")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(
        !repaired,
        "checking or reinstalling an occupied database must not patch a missing new component"
    );
}

#[sqlx::test(migrations = false)]
async fn nonempty_database_is_rejected_without_touching_its_data(pool: PgPool) {
    sqlx::raw_sql(
        "CREATE TABLE public.sentinel(value TEXT); INSERT INTO public.sentinel VALUES ('keep');",
    )
    .execute(&pool)
    .await
    .unwrap();
    let store = IdentityStore::from_pool(pool.clone());
    assert!(matches!(store.init_database().await, Err(E::Conflict)));
    let value: String = sqlx::query_scalar("SELECT value FROM public.sentinel")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(value, "keep");
    let exists: bool =
        sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='core_meta')")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(!exists);
}

#[sqlx::test(migrations = false)]
async fn concurrent_initialization_has_one_winner_and_startup_does_not_repair(pool: PgPool) {
    let store = IdentityStore::from_pool(pool.clone());
    let (a, b) = tokio::join!(store.init_database(), store.init_database());
    assert_eq!(usize::from(a.is_ok()) + usize::from(b.is_ok()), 1);
    assert!(matches!(a, Err(E::Conflict)) || matches!(b, Err(E::Conflict)));
    store.check_schema().await.unwrap();
    assert!(matches!(store.init_database().await, Err(E::Conflict)));
    let installed_version: i32 =
        sqlx::query_scalar("SELECT version FROM core_meta.schema_contract")
            .fetch_one(&pool)
            .await
            .unwrap();
    sqlx::query("UPDATE core_meta.schema_contract SET version=version+1")
        .execute(&pool)
        .await
        .unwrap();
    assert!(matches!(store.check_schema().await, Err(E::Storage)));
    let version: i32 = sqlx::query_scalar("SELECT version FROM core_meta.schema_contract")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(version, installed_version + 1);
}

#[sqlx::test(migrations = false)]
async fn funding_uses_native_account_references_and_cannot_fall_back_implicitly(pool: PgPool) {
    let store = IdentityStore::from_pool(pool.clone());
    store.init_database().await.unwrap();
    // Explicit installer-owned fixture for the key-management/user projection dependency.
    sqlx::raw_sql(lmm_core::abuse::SCHEMA)
        .execute(&pool)
        .await
        .unwrap();
    let session = store.bootstrap_user(9001, 6).await.unwrap();
    let person = store.authorize(&session.secret).await.unwrap();
    assert_ne!(person.owner_account_id, person.user_id);
    let level: i16 =
        sqlx::query_scalar("SELECT service_level FROM core_identity.accounts WHERE id=$1")
            .bind(person.owner_account_id)
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(level, 0); // Platform administration is not an account entitlement.
    let id = store.create_team(&session.secret).await.unwrap();
    let owner = Account {
        kind: AccountKind::Team,
        id,
    };
    let key = store
        .issue_key(
            &session.secret,
            owner,
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let actor = store.authorize(&key.secret).await.unwrap();
    assert_ne!(actor.owner_account_id, person.owner_account_id);
    assert_eq!(actor.funding_account_ids, vec![actor.owner_account_id]);
    assert!(sqlx::query("INSERT INTO core_identity.key_funding_rules(credential_id,position,payer_account_id) VALUES ($1,1,$2)")
        .bind(key.id).bind(person.owner_account_id).execute(&pool).await.is_err());
    assert!(
        sqlx::query("UPDATE core_identity.credentials SET owner_account_id=$1 WHERE id=$2")
            .bind(person.owner_account_id)
            .bind(key.id)
            .execute(&pool)
            .await
            .is_err()
    );
    sqlx::query("DELETE FROM core_identity.key_funding_rules WHERE credential_id=$1")
        .bind(key.id)
        .execute(&pool)
        .await
        .unwrap();
    assert!(matches!(
        store.authorize(&key.secret).await,
        Err(E::Storage)
    ));
}

#[sqlx::test(migrations = false)]
async fn duplicate_user_creation_does_not_leave_orphan_accounts_or_policies(pool: PgPool) {
    let store = IdentityStore::from_pool(pool.clone());
    store.init_database().await.unwrap();
    let session = store.bootstrap_user(7, 1).await.unwrap();
    assert!(matches!(store.bootstrap_user(7, 6).await, Err(E::Conflict)));
    let accounts: i64 = sqlx::query_scalar("SELECT count(*) FROM core_identity.accounts")
        .fetch_one(&pool)
        .await
        .unwrap();
    let policies: i64 = sqlx::query_scalar("SELECT count(*) FROM core_billing.account_policies")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!((accounts, policies), (1, 1));
    sqlx::query("UPDATE core_identity.accounts SET active=FALSE")
        .execute(&pool)
        .await
        .unwrap();
    assert!(matches!(
        store.authorize(&session.secret).await,
        Err(E::Unauthorized)
    ));
}

#[sqlx::test(migrations = false)]
async fn browser_session_defaults_share_statement_time_and_enforce_maximum_lifetime(pool: PgPool) {
    let store = IdentityStore::from_pool(pool.clone());
    store.init_database().await.unwrap();
    let credential = store.bootstrap_user(9001, 1).await.unwrap();
    let sid = "a".repeat(64);
    // Delay within the INSERT to distinguish its stable statement time from
    // independently sampled wall-clock defaults, even on a fast database.
    let coherent: (bool, bool, bool) = sqlx::query_as(
        "INSERT INTO core_identity.web_sessions
         (sid,user_id,user_version,current_credential_id,refresh_digest,ip,user_agent)
         SELECT $1,id,auth_version,$2,$3,'127.0.0.1','schema-regression'
         FROM core_identity.users, pg_sleep(0.01) WHERE id=9001
         RETURNING created_at=statement_timestamp(),last_active_at=created_at,
                   expires_at=created_at+interval '30 days'",
    )
    .bind(&sid)
    .bind(credential.id)
    .bind(vec![7_u8; 32])
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(coherent, (true, true, true));

    let error = sqlx::query(
        "UPDATE core_identity.web_sessions
         SET expires_at=created_at+interval '30 days 1 microsecond' WHERE sid=$1",
    )
    .bind(&sid)
    .execute(&pool)
    .await
    .unwrap_err();
    assert_eq!(
        error.as_database_error().unwrap().code().as_deref(),
        Some("23514")
    );
    let unchanged: bool = sqlx::query_scalar(
        "SELECT expires_at=created_at+interval '30 days'
         FROM core_identity.web_sessions WHERE sid=$1",
    )
    .bind(&sid)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(unchanged);
}
