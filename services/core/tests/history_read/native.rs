// SPDX-License-Identifier: AGPL-3.0-or-later
//! Actual identity, charging and ledger fixtures, all in SQLx disposable PG.
//! These are test definitions, not evidence until the commands have executed.
use lmm_core::{
    accounts::{Account, AccountKind},
    billing::{
        Credits,
        charging::{Charging, Outcome, Price, Request, Usage},
        ledger::{Action, Ledger, Request as LedgerRequest},
        transactional_ledger::TransactionalLedger,
    },
    core_schema,
    funding::FundingPolicy,
    history_read::native::{NativeFilter, NativePageRequest, NativeUsageRead, ReadError, ReadMode},
    identity::{IdentityStore, IssuedCredential},
};
use sqlx::PgPool;
use std::sync::Arc;

struct Fixture {
    identity: IdentityStore,
    session: IssuedCredential,
    other: IssuedCredential,
    key: IssuedCredential,
    second_key: IssuedCredential,
    charging: Charging,
    filter: NativeFilter,
}

async fn fixture(pool: &PgPool) -> Fixture {
    core_schema::init(pool).await.unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    let session = identity.bootstrap_user(7, 1).await.unwrap();
    let other = identity.bootstrap_user(8, 1).await.unwrap();
    let owner = Account { kind: AccountKind::Personal, id: 7 };
    let key = identity.issue_key(&session.secret, owner, FundingPolicy::inherit_account_settings(), 3600).await.unwrap();
    let second_key = identity.issue_key(&session.secret, owner, FundingPolicy::inherit_account_settings(), 3600).await.unwrap();
    let payer = identity.authorize(&key.secret).await.unwrap().owner_account_id;
    let ledger = Ledger::from_pool(pool.clone());
    ledger.open_wallet(payer).await.unwrap();
    ledger.execute(&LedgerRequest {
        scope: "a04-native-tests".into(),
        operation_key: "synthetic-credit".into(),
        actor_user_id: Some(7),
        reason: "Synthetic fixture only".into(),
        action: Action::Credit {
            account_id: payer,
            amount_units: Credits::try_from(100_000).unwrap(),
            payment_reference: "synthetic-credit".into(),
        },
    }).await.unwrap();
    let now: i64 = sqlx::query_scalar("SELECT floor(extract(epoch from clock_timestamp()))::bigint")
        .fetch_one(pool).await.unwrap();
    Fixture {
        identity,
        session,
        other,
        key,
        second_key,
        charging: Charging::new(pool.clone(), Arc::new(TransactionalLedger)),
        filter: NativeFilter { start: now - 60, end: now + 600, user_id: None, key_id: None, model: None, group: None },
    }
}

async fn settle(fixture: &Fixture, secret: &str, id: &str) {
    let request = Request {
        id: id.into(),
        fingerprint: [3; 32],
        reserve: 100,
        lease_seconds: 300,
        maximum_seconds: 600,
        price: Price {
            version: "a04-synthetic-v1".into(),
            model: "local-model".into(),
            group: "default".into(),
            input_per_million: 1_000_000,
            cached_per_million: 500_000,
            output_per_million: 2_000_000,
        },
    };
    fixture.charging.reserve(secret, &request, &[5; 32]).await.unwrap();
    fixture.charging.start(id, &[5; 32]).await.unwrap();
    fixture.charging.settle(id, &[5; 32], Usage { input: 10, cached: 2, output: 20 }, Outcome::Completed).await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn native_queries_share_authority_count_page_flow_and_refund_units(pool: PgPool) {
    let f = fixture(&pool).await;
    settle(&f, &f.key.secret, "native-charge").await;
    f.charging.refund("native-charge", "refund-once", 9, "Synthetic correction").await.unwrap();
    let mut read = NativeUsageRead::begin(&pool, &f.session.secret, ReadMode::SelfSession, f.filter.clone()).await.unwrap();
    let page = read.page(NativePageRequest { page: 1, size: 20 }).await.unwrap();
    assert_eq!(page.totals.request_count, "1");
    assert_eq!(page.totals.settled_credits, "49");
    assert_eq!(page.totals.refunded_credits, "9");
    assert_eq!(page.totals.net_credits, "40");
    assert_eq!(page.totals.net_usd, "0.00008");
    assert!(!page.totals.includes_legacy);
    assert_eq!(page.items.len(), 1);
    assert_eq!(page.items[0].net_credits, "40");
    assert_eq!(read.self_profile().await.unwrap().normalized_used_quota, Some(40));
    let flow = read.flow(3600).await.unwrap();
    assert_eq!(flow.len(), 1);
    assert_eq!(flow[0].totals, page.totals);
    read.finish().await.unwrap();
    let mut other = NativeUsageRead::begin(&pool, &f.other.secret, ReadMode::SelfSession, f.filter.clone()).await.unwrap();
    assert_eq!(other.totals().await.unwrap().request_count, "0");
    other.finish().await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn session_and_key_cannot_change_their_authority_with_filters(pool: PgPool) {
    let f = fixture(&pool).await;
    let mut filter = f.filter.clone();
    filter.user_id = Some(8);
    assert!(matches!(NativeUsageRead::begin(&pool, &f.session.secret, ReadMode::SelfSession, filter.clone()).await, Err(ReadError::Forbidden)));
    assert!(matches!(NativeUsageRead::begin(&pool, &f.key.secret, ReadMode::ApiKey, filter).await, Err(ReadError::Forbidden)));
    let mut filter = f.filter.clone();
    filter.key_id = Some(f.second_key.id);
    assert!(matches!(NativeUsageRead::begin(&pool, &f.key.secret, ReadMode::ApiKey, filter).await, Err(ReadError::Forbidden)));
    assert!(matches!(NativeUsageRead::begin(&pool, &f.key.secret, ReadMode::SelfSession, f.filter.clone()).await, Err(ReadError::Forbidden)));
    assert!(matches!(NativeUsageRead::begin(&pool, &f.session.secret, ReadMode::ApiKey, f.filter.clone()).await, Err(ReadError::Forbidden)));
    let mut read = NativeUsageRead::begin(&pool, &f.key.secret, ReadMode::ApiKey, f.filter.clone()).await.unwrap();
    assert!(matches!(read.self_profile().await, Err(ReadError::Forbidden)));
    read.finish().await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn platform_role_not_service_level_controls_administrator_reads(pool: PgPool) {
    let f = fixture(&pool).await;
    sqlx::query("UPDATE core_identity.accounts SET service_level=4 WHERE id=(SELECT personal_account_id FROM core_identity.users WHERE id=7)")
        .execute(&pool).await.unwrap();
    assert!(matches!(NativeUsageRead::begin(&pool, &f.session.secret, ReadMode::AdministratorSession, f.filter.clone()).await, Err(ReadError::Forbidden)));
    let admin = f.identity.bootstrap_user(9, 5).await.unwrap();
    let mut read = NativeUsageRead::begin(&pool, &admin.secret, ReadMode::AdministratorSession, f.filter.clone()).await.unwrap();
    assert_eq!(read.totals().await.unwrap().request_count, "0");
    read.finish().await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn key_reads_never_include_another_key_or_team_key(pool: PgPool) {
    let f = fixture(&pool).await;
    settle(&f, &f.key.secret, "first-key-charge").await;
    settle(&f, &f.second_key.secret, "second-key-charge").await;
    let team = f.identity.create_team(&f.session.secret).await.unwrap();
    let team_key = f.identity.issue_key(&f.session.secret, Account { kind: AccountKind::Team, id: team }, FundingPolicy::inherit_account_settings(), 3600).await.unwrap();
    // The team key owns a different account and has no charge, even though its
    // human owner has two personal-key charges in exactly the same interval.
    let mut team_read = NativeUsageRead::begin(&pool, &team_key.secret, ReadMode::ApiKey, f.filter.clone()).await.unwrap();
    assert_eq!(team_read.totals().await.unwrap().request_count, "0");
    team_read.finish().await.unwrap();
    let mut read = NativeUsageRead::begin(&pool, &f.key.secret, ReadMode::ApiKey, f.filter.clone()).await.unwrap();
    let page = read.page(NativePageRequest { page: 1, size: 20 }).await.unwrap();
    assert_eq!(page.totals.request_count, "1");
    assert_eq!(page.items[0].id, "first-key-charge");
    read.finish().await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn concurrent_settlement_and_refund_cannot_change_an_existing_snapshot(pool: PgPool) {
    let f = fixture(&pool).await;
    settle(&f, &f.key.secret, "before-snapshot").await;
    let mut read = NativeUsageRead::begin(&pool, &f.session.secret, ReadMode::SelfSession, f.filter.clone()).await.unwrap();
    let before = read.totals().await.unwrap();
    settle(&f, &f.key.secret, "after-snapshot").await;
    f.charging.refund("before-snapshot", "concurrent-refund", 9, "Synthetic correction").await.unwrap();
    let page = read.page(NativePageRequest { page: 1, size: 20 }).await.unwrap();
    assert_eq!(page.totals, before);
    assert_eq!(page.items.len(), 1);
    assert_eq!(page.items[0].refunded_credits, "0");
    read.finish().await.unwrap();
    let mut next = NativeUsageRead::begin(&pool, &f.session.secret, ReadMode::SelfSession, f.filter.clone()).await.unwrap();
    let totals = next.totals().await.unwrap();
    assert_eq!(totals.request_count, "2");
    assert_eq!(totals.net_credits, "89");
    next.finish().await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn filters_are_bound_and_have_identical_statistics_and_page_scope(pool: PgPool) {
    let f = fixture(&pool).await;
    settle(&f, &f.key.secret, "filter-charge").await;
    let mut filter = f.filter.clone();
    filter.model = Some("local-model' OR true --".into());
    let mut read = NativeUsageRead::begin(&pool, &f.session.secret, ReadMode::SelfSession, filter).await.unwrap();
    let page = read.page(NativePageRequest { page: 1, size: 20 }).await.unwrap();
    assert_eq!(page.totals.request_count, "0");
    assert!(page.items.is_empty());
    assert!(read.flow(900).await.unwrap().is_empty());
    read.finish().await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn revoked_or_expired_key_cannot_start_a_new_snapshot(pool: PgPool) {
    let f = fixture(&pool).await;
    sqlx::query("UPDATE core_identity.credentials SET revoked_at=clock_timestamp() WHERE id=$1")
        .bind(f.key.id).execute(&pool).await.unwrap();
    assert!(matches!(NativeUsageRead::begin(&pool, &f.key.secret, ReadMode::ApiKey, f.filter.clone()).await, Err(ReadError::Unauthorized)));
    sqlx::query("UPDATE core_identity.credentials SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1")
        .bind(f.second_key.id).execute(&pool).await.unwrap();
    assert!(matches!(NativeUsageRead::begin(&pool, &f.second_key.secret, ReadMode::ApiKey, f.filter.clone()).await, Err(ReadError::Unauthorized)));
}

#[sqlx::test(migrations = false)]
async fn native_response_has_no_raw_secrets_and_reads_do_not_write_state(pool: PgPool) {
    let f = fixture(&pool).await;
    settle(&f, &f.key.secret, "allowlisted-charge").await;
    let sql = "SELECT (SELECT count(*) FROM core_identity.audit),(SELECT count(*) FROM core_billing.ledger_journals), \
        (SELECT sum(balance_units::numeric)::text FROM core_billing.balance_state), \
        (SELECT sum(revision)::text FROM core_charging.requests)";
    let before: (i64, i64, String, String) = sqlx::query_as(sql).fetch_one(&pool).await.unwrap();
    let mut read = NativeUsageRead::begin(&pool, &f.session.secret, ReadMode::SelfSession, f.filter.clone()).await.unwrap();
    let page = read.page(NativePageRequest { page: 1, size: 20 }).await.unwrap();
    let json = serde_json::to_value(page).unwrap();
    let fields = json["items"][0].as_object().unwrap();
    let mut names: Vec<_> = fields.keys().map(String::as_str).collect();
    names.sort_unstable();
    let mut expected = vec!["id", "authorized_at", "model", "group", "price_version", "settled_credits", "refunded_credits", "net_credits", "net_usd", "input_tokens", "cached_tokens", "output_tokens"];
    expected.sort_unstable();
    assert_eq!(names, expected);
    assert!(!json.to_string().contains(&f.key.secret));
    read.finish().await.unwrap();
    let after: (i64, i64, String, String) = sqlx::query_as(sql).fetch_one(&pool).await.unwrap();
    assert_eq!(before, after);
}
