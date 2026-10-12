//! Real PostgreSQL wallet/profile reads and actual charging/refund accounting.
use lmm_core::{
    accounts::{Account, AccountKind},
    billing::{
        Credits,
        charging::{Charging, Outcome, Price, Request, Usage},
        ledger::{Action, Ledger, Request as LedgerRequest},
        transactional_ledger::TransactionalLedger,
    },
    core_schema,
    financial_profile::{self, FinancialProfile, ProfileError},
    funding::FundingPolicy,
    identity::IdentityStore,
};
use serde_json::{Value, json};
use sqlx::{PgPool, types::Json};
use std::sync::Arc;
#[path = "common/runtime.rs"]
mod runtime;

async fn profile(pool: &PgPool, user: i64) -> Result<FinancialProfile, ProfileError> {
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
        .execute(&mut *tx)
        .await
        .unwrap();
    let result = financial_profile::read(&mut tx, user).await;
    tx.commit().await.unwrap();
    result
}

async fn imported_person(pool: &PgPool, user: i64) -> i64 {
    let account: i64 = sqlx::query_scalar(
        "INSERT INTO core_identity.accounts(kind) VALUES ('personal') RETURNING id",
    )
    .fetch_one(pool)
    .await
    .unwrap();
    sqlx::query("INSERT INTO core_identity.users(id,personal_account_id) VALUES ($1,$2)")
        .bind(user)
        .bind(account)
        .execute(pool)
        .await
        .unwrap();
    account
}

async fn import(
    pool: &PgPool,
    user: i64,
    account: i64,
    available: i64,
    affiliate: i64,
    counters: Option<(i64, i64, i64)>,
) {
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::query("SET LOCAL synchronous_commit=on")
        .execute(&mut *tx)
        .await
        .unwrap();
    let mut source = json!({"migration_id":"synthetic-profile-import","source_entity_key":format!("legacy.users/{user}"),
        "source_archive_sha256":"a".repeat(64),"mapping_sha256":"b".repeat(64),"source_row_sha256":"c".repeat(64),
        "observed_at":1_700_000_000,"account_id":account,"available_units":available,"reserved_units":0,"affiliate_units":affiliate});
    sqlx::query_scalar::<_, Json<Value>>("SELECT core_billing.import_opening_balance($1)")
        .bind(Json(source.clone()))
        .fetch_one(&mut *tx)
        .await
        .unwrap();
    if let Some((used, requests, history)) = counters {
        let object = source.as_object_mut().unwrap();
        for key in ["available_units", "reserved_units", "affiliate_units"] {
            object.remove(key);
        }
        object.extend([
            ("user_id".into(), json!(user)),
            ("used_quota".into(), json!(used)),
            ("request_count".into(), json!(requests)),
            ("aff_history".into(), json!(history)),
        ]);
        sqlx::query_scalar::<_, Json<Value>>(
            "SELECT core_billing.import_account_opening_counters($1)",
        )
        .bind(Json(source))
        .fetch_one(&mut *tx)
        .await
        .unwrap();
    }
    tx.commit().await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn audited_native_user_has_true_zero_without_creating_ledger_rows(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    IdentityStore::from_pool(pool.clone())
        .bootstrap_user(7, 1)
        .await
        .unwrap();
    let before: i64 = sqlx::query_scalar("SELECT count(*) FROM core_billing.ledger_accounts")
        .fetch_one(&pool)
        .await
        .unwrap();
    let result = profile(&pool, 7).await.unwrap();
    assert!(result.financial_projection_available && result.usage_projection_available);
    assert_eq!(
        (
            result.quota,
            result.used_quota,
            result.request_count,
            result.aff_quota,
            result.aff_debt
        ),
        (0, 0, 0, 0, 0)
    );
    assert_eq!(result.normalized_used_quota, Some(0));
    let after: i64 = sqlx::query_scalar("SELECT count(*) FROM core_billing.ledger_accounts")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(before, after);
    assert_eq!(profile(&pool, 0).await, Err(ProfileError::Invalid));
    assert_eq!(profile(&pool, 999).await, Err(ProfileError::Unavailable));
}

#[sqlx::test(migrations = false)]
async fn imported_identity_requires_both_economic_opening_and_historical_counters(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let account = imported_person(&pool, 7).await;
    assert_eq!(profile(&pool, 7).await, Err(ProfileError::Unavailable));
    import(&pool, 7, account, 120, 0, None).await;
    assert_eq!(profile(&pool, 7).await, Err(ProfileError::Unavailable));
}

#[sqlx::test(migrations = false)]
async fn imported_signed_wallet_affiliate_and_raw_counters_preserve_their_basis(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let account = imported_person(&pool, 7).await;
    import(&pool, 7, account, -70, -11, Some((300, 9, 42))).await;
    let result = profile(&pool, 7).await.unwrap();
    assert_eq!(
        (
            result.quota,
            result.wallet_spendable_quota,
            result.wallet_debt
        ),
        (-70, 0, 70)
    );
    assert_eq!(
        (result.aff_quota, result.aff_debt, result.aff_history_quota),
        (0, 11, 42)
    );
    assert_eq!((result.used_quota, result.request_count), (300, 9));
    assert!(result.financial_projection_available);
    assert!(!result.usage_projection_available);
    assert_eq!(result.normalized_used_quota, None);
    assert_eq!(profile(&pool, 7).await.unwrap(), result);
    let row: (i64,i64) = sqlx::query_as("SELECT (SELECT count(*) FROM core_billing.ledger_journals),(SELECT count(*) FROM core_billing.account_opening_counters)")
        .fetch_one(&pool).await.unwrap();
    assert_eq!(row, (1, 1));
}

#[sqlx::test(migrations = false)]
async fn zero_historical_usage_has_an_exact_current_credit_basis(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let account = imported_person(&pool, 7).await;
    import(&pool, 7, account, 123, 17, Some((0, 12, -3))).await;
    let result = profile(&pool, 7).await.unwrap();
    assert_eq!(
        (
            result.quota,
            result.aff_quota,
            result.aff_debt,
            result.aff_history_quota
        ),
        (123, 17, 0, -3)
    );
    assert_eq!(
        (result.normalized_used_quota, result.request_count),
        (Some(0), 12)
    );
}

#[sqlx::test(migrations = false)]
async fn actual_team_settlement_and_refund_count_the_actor_without_spending_personal_wallet(
    pool: PgPool,
) {
    core_schema::init(&pool).await.unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    let session = identity.bootstrap_user(7, 1).await.unwrap();
    identity.bootstrap_user(8, 1).await.unwrap();
    let team = identity.create_team(&session.secret).await.unwrap();
    let key = identity
        .issue_key(
            &session.secret,
            Account {
                kind: AccountKind::Team,
                id: team,
            },
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let payer = identity
        .authorize(&key.secret)
        .await
        .unwrap()
        .owner_account_id;
    let ledger = Ledger::from_pool(pool.clone());
    ledger.open_wallet(payer).await.unwrap();
    ledger
        .execute(&LedgerRequest {
            scope: "synthetic-profile".into(),
            operation_key: "team-credit".into(),
            actor_user_id: Some(7),
            reason: "Synthetic fixture only".into(),
            action: Action::Credit {
                account_id: payer,
                amount_units: Credits::try_from(1000).unwrap(),
                payment_reference: "synthetic-team-credit".into(),
            },
        })
        .await
        .unwrap();
    let charging = Charging::new(pool.clone(), Arc::new(TransactionalLedger));
    let request = Request {
        id: "profile-team-charge".into(),
        fingerprint: [3; 32],
        reserve: 100,
        lease_seconds: 300,
        maximum_seconds: 600,
        price: Price {
            upstream_cost_group_ppm: None,
            version: "synthetic-v1".into(),
            model: "local-model".into(),
            group: "default".into(),
            input_per_million: 1_000_000,
            cached_per_million: 500_000,
            output_per_million: 2_000_000,
        },
    };
    charging
        .reserve(&key.secret, &request, &[5; 32])
        .await
        .unwrap();
    charging.start(&request.id, &[5; 32]).await.unwrap();
    charging
        .settle(
            &request.id,
            &[5; 32],
            Usage {
                upstream_cost_atto_usd: None,
                input: 10,
                cached: 2,
                output: 20,
            },
            Outcome::Completed,
        )
        .await
        .unwrap();
    charging
        .refund(
            &request.id,
            "synthetic-refund",
            9,
            "Synthetic provider correction",
        )
        .await
        .unwrap();
    let result = profile(&pool, 7).await.unwrap();
    assert_eq!(
        (result.quota, result.used_quota, result.request_count),
        (0, 40, 1)
    );
    assert_eq!(result.normalized_used_quota, Some(40));
    assert_eq!(ledger.balance(payer).await.unwrap().available_units, 960);
    let other = profile(&pool, 8).await.unwrap();
    assert_eq!((other.used_quota, other.request_count), (0, 0));
    assert_eq!(profile(&pool, 7).await.unwrap(), result);
}

#[sqlx::test(migrations = false)]
async fn restricted_runtime_can_read_without_opening_or_counter_write_authority(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let account = imported_person(&pool, 7).await;
    import(&pool, 7, account, 321, 9, Some((17, 2, 8))).await;
    let role = runtime::RuntimeRole::create(&pool).await;
    role.optional("web_identity").await;
    let runtime_pool = sqlx::postgres::PgPoolOptions::new()
        .max_connections(1)
        .connect(&role.url)
        .await
        .unwrap();
    assert_eq!(profile(&runtime_pool, 7).await.unwrap().quota, 321);
    let allowed:bool = sqlx::query_scalar("SELECT has_table_privilege(current_user,'core_billing.account_opening_counters','INSERT,UPDATE,DELETE,TRUNCATE')")
        .fetch_one(&runtime_pool).await.unwrap();
    assert!(!allowed);
    runtime_pool.close().await;
    role.cleanup().await;
}
