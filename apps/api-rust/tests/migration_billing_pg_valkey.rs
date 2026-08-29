use lmm_api_rs::migration_routes::billing_payments::{
    BillingCache, BillingRepository, Completion, CreateOrder, PgBillingRepository,
    ValkeyBillingCache,
};
use sqlx::{PgPool, Row, postgres::PgPoolOptions};
use std::{env, sync::Arc, time::Duration};

/// Runs only against the disposable PostgreSQL 18 and Valkey instances created
/// by the billing TCP differential harness. Missing environment is an error
/// when explicitly invoked; it never converts infrastructure absence into a
/// passing test.
#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use billing-listener-differential.sh"]
async fn balance_ledger_is_atomic_and_valkey_is_not_an_idempotency_store() {
    let database_url = env::var("LMM_BILLING_TEST_DATABASE_URL")
        .expect("set LMM_BILLING_TEST_DATABASE_URL to isolated PostgreSQL 18");
    let valkey_url = env::var("LMM_BILLING_TEST_VALKEY_URL")
        .expect("set LMM_BILLING_TEST_VALKEY_URL to isolated Valkey");
    let pool = PgPoolOptions::new()
        .max_connections(4)
        .acquire_timeout(Duration::from_secs(3))
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL must be reachable");
    let version: String = sqlx::query_scalar("SHOW server_version")
        .fetch_one(&pool)
        .await
        .expect("PostgreSQL version");
    assert!(
        version.starts_with("18."),
        "requires PostgreSQL 18, got {version}"
    );
    reset_schema(&pool).await;

    let valkey = redis::Client::open(valkey_url).expect("isolated Valkey URL");
    let mut connection = valkey
        .get_multiplexed_async_connection()
        .await
        .expect("isolated Valkey must be reachable");
    redis::cmd("FLUSHDB")
        .query_async::<()>(&mut connection)
        .await
        .expect("reset isolated Valkey");

    sqlx::query(
        "INSERT INTO users (id, quota, \"group\", deleted_at) VALUES (7, 1000001, 'default', NULL)",
    )
    .execute(&pool)
    .await
    .expect("user fixture");
    sqlx::query("INSERT INTO subscription_plans (id, price_amount, enabled, allow_balance_pay, max_purchase_per_user, total_amount, duration_unit, duration_value, custom_seconds, upgrade_group, downgrade_group, allow_wallet_overflow) VALUES (3, 1.000001, TRUE, TRUE, 0, 44, 'day', 1, 0, 'pro', '', TRUE)")
        .execute(&pool)
        .await
        .expect("plan fixture");

    let repo = Arc::new(PgBillingRepository::new(pool.clone()));
    let (first, second) = tokio::join!(
        repo.purchase_with_balance(7, 3, 1_000_000),
        repo.purchase_with_balance(7, 3, 1_000_000)
    );
    assert!(matches!(first, Ok(Completion::Completed { .. })));
    assert!(
        second.is_err(),
        "the locked wallet must reject the concurrent overdraft: {second:?}"
    );

    let user = sqlx::query("SELECT quota, \"group\" FROM users WHERE id = 7")
        .fetch_one(&pool)
        .await
        .expect("user after atomic ledger transaction");
    assert_eq!(user.get::<i64, _>("quota"), 0);
    assert_eq!(user.get::<String, _>("group"), "pro");
    let subscription =
        sqlx::query("SELECT amount_total, status, source, prev_user_group FROM user_subscriptions")
            .fetch_one(&pool)
            .await
            .expect("one created subscription");
    assert_eq!(subscription.get::<i64, _>("amount_total"), 44);
    assert_eq!(subscription.get::<String, _>("status"), "active");
    assert_eq!(subscription.get::<String, _>("source"), "balance");
    assert_eq!(subscription.get::<String, _>("prev_user_group"), "default");
    let order = sqlx::query("SELECT payment_method, payment_provider, status, provider_payload FROM subscription_orders")
        .fetch_one(&pool)
        .await
        .expect("completed balance order");
    assert_eq!(order.get::<String, _>("payment_method"), "balance");
    assert_eq!(order.get::<String, _>("payment_provider"), "balance");
    assert_eq!(order.get::<String, _>("status"), "success");
    assert_eq!(
        order.get::<String, _>("provider_payload"),
        "charged_quota=1000001"
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM top_ups")
            .fetch_one(&pool)
            .await
            .expect("top up count"),
        0,
        "balance subscription purchase does not manufacture a top-up record"
    );

    let subscription_id: i64 = sqlx::query_scalar("SELECT id FROM user_subscriptions")
        .fetch_one(&pool)
        .await
        .expect("subscription id");
    let cache_key = format!("new-api:subscription_plan_info:v1:sub:{subscription_id}");
    redis::cmd("SET")
        .arg(&cache_key)
        .arg("stale")
        .query_async::<()>(&mut connection)
        .await
        .expect("stale cache fixture");
    redis::cmd("HSET")
        .arg("user:7")
        .arg("Id")
        .arg(7)
        .arg("Quota")
        .arg(1_000_001)
        .query_async::<i64>(&mut connection)
        .await
        .expect("cached user fixture");
    let cache = ValkeyBillingCache::new(valkey);
    cache
        .invalidate_completed_payment(subscription_id, 7, 1_000_001, true)
        .await;
    assert_eq!(
        redis::cmd("EXISTS")
            .arg(&cache_key)
            .query_async::<i64>(&mut connection)
            .await
            .expect("cache key check"),
        0
    );
    assert_eq!(
        redis::cmd("EXISTS")
            .arg("user:7")
            .query_async::<i64>(&mut connection)
            .await
            .expect("group cache invalidation"),
        0
    );

    redis::cmd("HSET")
        .arg("user:7")
        .arg("Id")
        .arg(7)
        .arg("Quota")
        .arg(100)
        .query_async::<i64>(&mut connection)
        .await
        .expect("quota cache fixture");
    redis::cmd("SET")
        .arg(&cache_key)
        .arg("stale")
        .query_async::<()>(&mut connection)
        .await
        .expect("quota subscription cache fixture");
    cache
        .invalidate_completed_payment(subscription_id, 7, 30, false)
        .await;
    assert_eq!(
        redis::cmd("HGET")
            .arg("user:7")
            .arg("Quota")
            .query_async::<i64>(&mut connection)
            .await
            .expect("quota cache delta"),
        70
    );
    redis::cmd("DEL")
        .arg("user:7")
        .query_async::<i64>(&mut connection)
        .await
        .expect("expire user cache fixture");
    redis::cmd("SET")
        .arg(&cache_key)
        .arg("stale")
        .query_async::<()>(&mut connection)
        .await
        .expect("second stale cache fixture");
    cache
        .invalidate_completed_payment(subscription_id, 7, 1_000_001, false)
        .await;
    assert_eq!(
        redis::cmd("EXISTS")
            .arg("user:7")
            .query_async::<i64>(&mut connection)
            .await
            .expect("quota invalidation must not create a user hash"),
        0,
        "a post-commit quota delta must not recreate an expired user cache"
    );
    let keys: Vec<String> = redis::cmd("KEYS")
        .arg("*")
        .query_async(&mut connection)
        .await
        .expect("Valkey key scan");
    assert!(
        keys.is_empty(),
        "no Valkey lock or idempotency key is used: {keys:?}"
    );
}

/// A provider callback is allowed to race with its duplicate, but it must not
/// create a second subscription or partially apply the plan snapshot.
#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use billing-listener-differential.sh"]
async fn provider_completion_is_idempotent_and_applies_the_full_plan_snapshot() {
    let database_url = env::var("LMM_BILLING_TEST_DATABASE_URL")
        .expect("set LMM_BILLING_TEST_DATABASE_URL to isolated PostgreSQL 18");
    let pool = PgPoolOptions::new()
        .max_connections(4)
        .acquire_timeout(Duration::from_secs(3))
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL must be reachable");
    let version: String = sqlx::query_scalar("SHOW server_version")
        .fetch_one(&pool)
        .await
        .expect("PostgreSQL version");
    assert!(
        version.starts_with("18."),
        "requires PostgreSQL 18, got {version}"
    );
    reset_schema(&pool).await;
    sqlx::query(
        "INSERT INTO users (id, quota, \"group\", deleted_at) VALUES (7, 0, 'default', NULL)",
    )
    .execute(&pool)
    .await
    .expect("user fixture");
    sqlx::query("INSERT INTO subscription_plans (id, price_amount, currency, enabled, allow_balance_pay, max_purchase_per_user, total_amount, duration_unit, duration_value, custom_seconds, upgrade_group, downgrade_group, allow_wallet_overflow, quota_reset_period, quota_reset_custom_seconds) VALUES (3, 1.000000, 'USD', TRUE, TRUE, 1, 44, 'day', 1, 0, 'pro', '', TRUE, 'custom', 60)")
        .execute(&pool)
        .await
        .expect("disabled plan fixture");

    let repo = Arc::new(PgBillingRepository::new(pool.clone()));
    sqlx::query("UPDATE users SET deleted_at = NOW() WHERE id = 7")
        .execute(&pool)
        .await
        .expect("soft delete fixture user");
    assert!(matches!(
        repo.create_pending(CreateOrder {
            user_id: 7,
            plan_id: 3,
            payment_method: "card".into(),
            provider: "stripe",
        })
        .await,
        Err(lmm_api_rs::migration_routes::billing_payments::BillingError::Rejected)
    ));
    sqlx::query("UPDATE users SET deleted_at = NULL WHERE id = 7")
        .execute(&pool)
        .await
        .expect("restore fixture user");
    let order = repo
        .create_pending(CreateOrder {
            user_id: 7,
            plan_id: 3,
            payment_method: "card".into(),
            provider: "stripe",
        })
        .await
        .expect("pending order fixture");
    sqlx::query("UPDATE users SET deleted_at = NOW() WHERE id = 7")
        .execute(&pool)
        .await
        .expect("soft delete callback user");
    assert!(matches!(
        repo.complete(&order.trade_no, "stripe", "provider-payload", None)
            .await,
        Err(lmm_api_rs::migration_routes::billing_payments::BillingError::Rejected)
    ));
    assert_eq!(
        sqlx::query_scalar::<_, String>(
            "SELECT status FROM subscription_orders WHERE trade_no = $1",
        )
        .bind(&order.trade_no)
        .fetch_one(&pool)
        .await
        .expect("pending order status after deleted-user rejection"),
        "pending"
    );
    sqlx::query("UPDATE users SET deleted_at = NULL WHERE id = 7")
        .execute(&pool)
        .await
        .expect("restore callback user");
    sqlx::query("UPDATE subscription_plans SET enabled = FALSE WHERE id = 3")
        .execute(&pool)
        .await
        .expect("plan remains disabled at callback time");

    let (first, second) = tokio::join!(
        repo.complete(&order.trade_no, "stripe", "provider-payload", None),
        repo.complete(&order.trade_no, "stripe", "provider-payload", None)
    );
    assert!(
        matches!(first, Ok(Completion::Completed { .. }))
            && matches!(second, Ok(Completion::AlreadySucceeded))
            || matches!(second, Ok(Completion::Completed { .. }))
                && matches!(first, Ok(Completion::AlreadySucceeded)),
        "one callback must complete and its duplicate must be an idempotent success: {first:?}, {second:?}"
    );
    let user = sqlx::query("SELECT \"group\" FROM users WHERE id = 7")
        .fetch_one(&pool)
        .await
        .expect("user after completion");
    assert_eq!(user.get::<String, _>("group"), "pro");
    let subscription = sqlx::query("SELECT source, last_reset_time, next_reset_time, upgrade_group, prev_user_group, downgrade_group, allow_wallet_overflow FROM user_subscriptions")
        .fetch_one(&pool)
        .await
        .expect("subscription snapshot");
    assert_eq!(subscription.get::<String, _>("source"), "order");
    assert!(subscription.get::<i64, _>("last_reset_time") > 0);
    assert!(subscription.get::<i64, _>("next_reset_time") > 0);
    assert_eq!(subscription.get::<String, _>("upgrade_group"), "pro");
    assert_eq!(subscription.get::<String, _>("prev_user_group"), "default");
    assert_eq!(subscription.get::<String, _>("downgrade_group"), "");
    assert!(subscription.get::<bool, _>("allow_wallet_overflow"));
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM user_subscriptions")
            .fetch_one(&pool)
            .await
            .expect("subscription count"),
        1
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM top_ups WHERE trade_no = $1")
            .bind(&order.trade_no)
            .fetch_one(&pool)
            .await
            .expect("top up count"),
        1
    );
}

async fn reset_schema(pool: &PgPool) {
    for table in [
        "top_ups",
        "subscription_orders",
        "user_subscriptions",
        "subscription_plans",
        "users",
    ] {
        sqlx::query(&format!("DROP TABLE IF EXISTS {table} CASCADE"))
            .execute(pool)
            .await
            .expect("drop isolated billing table");
    }
    sqlx::query("CREATE TABLE users (id BIGINT PRIMARY KEY, quota BIGINT NOT NULL, \"group\" TEXT NOT NULL, deleted_at TIMESTAMPTZ)")
        .execute(pool)
        .await
        .expect("users schema");
    sqlx::query("CREATE TABLE subscription_plans (id BIGINT PRIMARY KEY, price_amount NUMERIC(18, 6) NOT NULL, currency TEXT NOT NULL DEFAULT 'CNY', price_currency_version BIGINT NOT NULL DEFAULT 0, enabled BOOLEAN NOT NULL, allow_balance_pay BOOLEAN, max_purchase_per_user BIGINT, total_amount BIGINT, duration_unit TEXT, duration_value BIGINT, custom_seconds BIGINT, upgrade_group TEXT, downgrade_group TEXT, allow_wallet_overflow BOOLEAN, quota_reset_period TEXT, quota_reset_custom_seconds BIGINT)")
        .execute(pool)
        .await
        .expect("plans schema");
    sqlx::query("CREATE TABLE user_subscriptions (id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL, plan_id BIGINT NOT NULL, amount_total BIGINT NOT NULL, amount_used BIGINT NOT NULL, start_time BIGINT NOT NULL, end_time BIGINT NOT NULL, status TEXT NOT NULL, source TEXT NOT NULL, last_reset_time BIGINT NOT NULL, next_reset_time BIGINT NOT NULL, upgrade_group TEXT NOT NULL, prev_user_group TEXT NOT NULL, downgrade_group TEXT NOT NULL, allow_wallet_overflow BOOLEAN NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)")
        .execute(pool)
        .await
        .expect("subscriptions schema");
    sqlx::query("CREATE TABLE subscription_orders (id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL, plan_id BIGINT NOT NULL, money NUMERIC(18, 6) NOT NULL, plan_currency TEXT, trade_no TEXT UNIQUE NOT NULL, payment_method TEXT NOT NULL, payment_provider TEXT NOT NULL, status TEXT NOT NULL, create_time BIGINT NOT NULL, complete_time BIGINT, provider_payload TEXT)")
        .execute(pool)
        .await
        .expect("orders schema");
    sqlx::query("CREATE TABLE top_ups (id BIGSERIAL PRIMARY KEY, user_id BIGINT, amount BIGINT, money NUMERIC(18, 6), trade_no TEXT, payment_method TEXT, payment_provider TEXT, create_time BIGINT, complete_time BIGINT, status TEXT)")
        .execute(pool)
        .await
        .expect("top ups schema");
}
