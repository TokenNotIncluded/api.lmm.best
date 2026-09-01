//! Focused PostgreSQL 18 proofs for subscription-reset transaction boundaries.
//!
//! These tests require an explicitly supplied disposable database and are
//! serialized by the integration runner because each test owns an isolated
//! schema while exercising real PostgreSQL locks and transactions.

use async_trait::async_trait;
use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Method, Request},
    response::Response,
};
use lmm_api_rs::{
    ClientIpKey,
    auth::{
        AuthBundle, AuthError, AuthErrorKind, DashboardAuth, DashboardUser, LoginOutcome,
        LoginRequest, LogoutRequest, LogoutResult, RequestMetadata, TwoFactorLoginRequest,
    },
    migration_routes::billing_subscriptions::{
        BillingSubscriptionsState, router, spawn_maintenance,
    },
};
use secrecy::{ExposeSecret, SecretString};
use serde_json::{Value, json};
use sqlx::{PgPool, postgres::PgPoolOptions};
use std::{error::Error, sync::Arc, time::Duration};
use tokio::{task::JoinHandle, time::timeout};
use tower::ServiceExt;

const TEST_DATABASE_URL: &str = "LMM_BILLING_SUBSCRIPTIONS_TEST_DATABASE_URL";
type TestResult<T = ()> = Result<T, Box<dyn Error + Send + Sync>>;
type ResponseTask = JoinHandle<Result<Response, std::convert::Infallible>>;

struct FixtureAuth;

#[async_trait]
impl DashboardAuth for FixtureAuth {
    async fn check_critical_rate_limit(
        &self,
        _: &str,
    ) -> Result<lmm_api_rs::auth::CriticalRateLimitOutcome, AuthError> {
        Ok(lmm_api_rs::auth::CriticalRateLimitOutcome::Allowed)
    }

    async fn login(&self, _: LoginRequest, _: RequestMetadata) -> Result<LoginOutcome, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn login_2fa(
        &self,
        _: TwoFactorLoginRequest,
        _: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn refresh(
        &self,
        _: SecretString,
        _: Option<String>,
        _: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn self_user(&self, token: SecretString) -> Result<DashboardUser, AuthError> {
        let (id, username, role) = match token.expose_secret() {
            "root" => (1, "root", 100),
            "admin" => (2, "admin", 10),
            "user" => (7, "voucher-user", 1),
            _ => return Err(AuthError::new(AuthErrorKind::Unauthorized)),
        };
        Ok(DashboardUser {
            id,
            username: username.to_owned(),
            display_name: String::new(),
            role,
            status: 1,
            email: String::new(),
            github_id: String::new(),
            discord_id: String::new(),
            oidc_id: String::new(),
            wechat_id: String::new(),
            telegram_id: String::new(),
            group: "default".to_owned(),
            quota: 0,
            used_quota: 0,
            request_count: 0,
            aff_code: String::new(),
            aff_count: 0,
            aff_quota: 0,
            aff_history_quota: 0,
            inviter_id: 0,
            linux_do_id: String::new(),
            setting: "{}".to_owned(),
            stripe_customer: String::new(),
            sidebar_modules: json!({}),
            permissions: json!({}),
        })
    }

    async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn generate_personal_access_token(&self, _: SecretString) -> Result<String, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }
}

struct PgHarness {
    admin: PgPool,
    pool: PgPool,
    schema: String,
}

impl PgHarness {
    async fn new() -> TestResult<Self> {
        let database_url = std::env::var(TEST_DATABASE_URL)
            .unwrap_or_else(|_| panic!("set {TEST_DATABASE_URL} to isolated PostgreSQL 18"));
        let admin = PgPoolOptions::new()
            .max_connections(2)
            .acquire_timeout(Duration::from_secs(3))
            .connect(&database_url)
            .await?;
        let version: String = sqlx::query_scalar("SHOW server_version")
            .fetch_one(&admin)
            .await?;
        assert!(
            version.starts_with("18."),
            "requires PostgreSQL 18, got {version}"
        );
        let schema = format!("subscription_reset_{}", uuid::Uuid::new_v4().simple());
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&admin)
            .await?;
        let pool = PgPoolOptions::new()
            .max_connections(12)
            .acquire_timeout(Duration::from_secs(3))
            .after_connect({
                let schema = schema.clone();
                move |connection, _metadata| {
                    let statement = format!("SET search_path TO {schema}");
                    Box::pin(async move {
                        sqlx::query(&statement).execute(connection).await?;
                        Ok(())
                    })
                }
            })
            .connect(&database_url)
            .await?;
        let harness = Self {
            admin,
            pool,
            schema,
        };
        harness.create_schema().await?;
        Ok(harness)
    }

    async fn create_schema(&self) -> TestResult {
        for statement in [
            "CREATE TABLE options (key TEXT PRIMARY KEY, value TEXT NOT NULL)",
            "CREATE TABLE users (id BIGINT PRIMARY KEY, username TEXT NOT NULL, email TEXT, \"group\" TEXT NOT NULL, setting TEXT NOT NULL DEFAULT '{}', deleted_at TIMESTAMPTZ)",
            "CREATE TABLE subscription_plans (id BIGINT PRIMARY KEY, title TEXT NOT NULL, price_amount NUMERIC NOT NULL DEFAULT 1, enabled BOOLEAN NOT NULL DEFAULT TRUE, total_amount BIGINT NOT NULL DEFAULT 100, duration_unit TEXT NOT NULL DEFAULT 'day', duration_value BIGINT NOT NULL DEFAULT 1, upgrade_group TEXT NOT NULL DEFAULT '', downgrade_group TEXT NOT NULL DEFAULT '', archived_at BIGINT NOT NULL DEFAULT 0)",
            "CREATE TABLE user_subscriptions (id BIGINT PRIMARY KEY, user_id BIGINT NOT NULL, plan_id BIGINT NOT NULL, amount_total BIGINT NOT NULL, amount_used BIGINT NOT NULL, start_time BIGINT NOT NULL, end_time BIGINT NOT NULL, status TEXT NOT NULL, source TEXT NOT NULL, last_reset_time BIGINT NOT NULL DEFAULT 0, next_reset_time BIGINT NOT NULL DEFAULT 0, upgrade_group TEXT NOT NULL DEFAULT '', prev_user_group TEXT NOT NULL DEFAULT '', downgrade_group TEXT NOT NULL DEFAULT '', allow_wallet_overflow BOOLEAN NOT NULL DEFAULT TRUE, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)",
            "CREATE TABLE subscription_reset_vouchers (id BIGINT PRIMARY KEY, user_id BIGINT NOT NULL, plan_id BIGINT NOT NULL, operation_id VARCHAR(64) NOT NULL, status VARCHAR(16) NOT NULL DEFAULT 'available', expires_at BIGINT NOT NULL, redeemed_at BIGINT NOT NULL DEFAULT 0, created_by BIGINT NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)",
            "CREATE TABLE subscription_reset_events (id BIGSERIAL PRIMARY KEY, operation_id VARCHAR(64) NOT NULL, user_id BIGINT NOT NULL, plan_id BIGINT NOT NULL, mode VARCHAR(24) NOT NULL, actor_user_id BIGINT NOT NULL, voucher_id BIGINT NOT NULL DEFAULT 0, reset_count BIGINT NOT NULL DEFAULT 0, restored_quota BIGINT NOT NULL DEFAULT 0, voucher_expiry BIGINT NOT NULL DEFAULT 0, created_at BIGINT NOT NULL, UNIQUE(operation_id,user_id,plan_id,mode))",
            "CREATE TABLE subscription_reset_previews (token VARCHAR(64) PRIMARY KEY, actor_user_id BIGINT NOT NULL, mode VARCHAR(16) NOT NULL, targets_json TEXT NOT NULL, payload_hash VARCHAR(64) NOT NULL, target_count BIGINT NOT NULL, active_subscriptions BIGINT NOT NULL, quota_to_restore BIGINT NOT NULL, voucher_expires_at BIGINT NOT NULL DEFAULT 0, expires_at BIGINT NOT NULL, consumed_at BIGINT NOT NULL DEFAULT 0, operation_id VARCHAR(64) NOT NULL DEFAULT '', created_at BIGINT NOT NULL)",
            "CREATE TABLE subscription_reset_operations (operation_id VARCHAR(64) PRIMARY KEY, preview_token VARCHAR(64) NOT NULL UNIQUE, actor_user_id BIGINT NOT NULL, mode VARCHAR(16) NOT NULL, payload_hash VARCHAR(64) NOT NULL, result_json TEXT NOT NULL, created_at BIGINT NOT NULL, completed_at BIGINT NOT NULL)",
            "CREATE TABLE logs (id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL, created_at BIGINT NOT NULL, type BIGINT NOT NULL, content TEXT NOT NULL, username TEXT NOT NULL, ip TEXT NOT NULL, other TEXT NOT NULL)",
        ] {
            sqlx::query(statement).execute(&self.pool).await?;
        }
        sqlx::query("INSERT INTO options (key,value) VALUES ('payment_setting.compliance_confirmed','true'),('payment_setting.compliance_terms_version','v1')")
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    fn app(&self) -> Router {
        router(BillingSubscriptionsState::new(
            self.pool.clone(),
            None,
            Arc::new(FixtureAuth),
        ))
    }

    async fn cleanup(self) -> TestResult {
        self.pool.close().await;
        sqlx::query(&format!("DROP SCHEMA {} CASCADE", self.schema))
            .execute(&self.admin)
            .await?;
        self.admin.close().await;
        Ok(())
    }
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18; run this test binary with --test-threads=1"]
async fn voucher_redeem_racing_subscription_invalidation_never_commits_zero_reset_event()
-> TestResult {
    voucher_race_should_not_commit_zero_reset(false, 91_001).await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18; run this test binary with --test-threads=1"]
async fn voucher_redeem_racing_subscription_deletion_never_commits_zero_reset_event() -> TestResult
{
    voucher_race_should_not_commit_zero_reset(true, 91_002).await
}

async fn voucher_race_should_not_commit_zero_reset(
    delete_subscription: bool,
    advisory_key: i64,
) -> TestResult {
    let harness = PgHarness::new().await?;
    seed_active_subscription(&harness.pool, 7, 3, 11, 47).await?;
    let now = database_timestamp(&harness.pool).await?;
    sqlx::query("INSERT INTO subscription_reset_vouchers (id,user_id,plan_id,operation_id,status,expires_at,redeemed_at,created_by,created_at,updated_at) VALUES (21,7,3,'soft-reset','available',$1,0,1,$2,$2)")
        .bind(now + 3_600)
        .bind(now)
        .execute(&harness.pool)
        .await?;
    sqlx::query(&format!(
        "CREATE FUNCTION block_voucher_claim() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock({advisory_key}); RETURN NEW; END $$"
    ))
    .execute(&harness.pool)
    .await?;
    sqlx::query(
        "CREATE TRIGGER block_voucher_claim BEFORE UPDATE OF status ON subscription_reset_vouchers FOR EACH ROW WHEN (NEW.status='redeemed') EXECUTE FUNCTION block_voucher_claim()",
    )
    .execute(&harness.pool)
    .await?;

    let mut blocker = harness.pool.begin().await?;
    let blocker_pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
        .fetch_one(&mut *blocker)
        .await?;
    sqlx::query("SELECT pg_advisory_xact_lock($1)")
        .bind(advisory_key)
        .execute(&mut *blocker)
        .await?;

    let redeem = tokio::spawn(request(
        harness.app(),
        Method::POST,
        "/api/subscription/self/reset-vouchers/21/redeem",
        "user",
        None,
    ));
    wait_until_blocked_by(&harness.pool, blocker_pid).await?;

    let mutation_path = if delete_subscription {
        "/api/subscription/admin/user_subscriptions/11"
    } else {
        "/api/subscription/admin/user_subscriptions/11/invalidate"
    };
    let mutation = tokio::spawn(request(
        harness.app(),
        if delete_subscription {
            Method::DELETE
        } else {
            Method::POST
        },
        mutation_path,
        "admin",
        None,
    ));
    wait_until_finished_or_blocked_behind_redeem(&harness.pool, blocker_pid, &mutation).await?;
    blocker.rollback().await?;

    let mutation_body = response_json(mutation.await??).await?;
    assert_eq!(
        mutation_body["success"], true,
        "subscription mutation failed"
    );
    let _redeem_body = response_json(redeem.await??).await?;
    let invariant_holds: bool = sqlx::query_scalar(
        "SELECT NOT EXISTS (SELECT 1 FROM subscription_reset_vouchers voucher LEFT JOIN subscription_reset_events event ON event.voucher_id=voucher.id AND event.mode='voucher_redeem' WHERE voucher.id=21 AND voucher.status='redeemed' AND COALESCE(event.reset_count,0)=0)",
    )
    .fetch_one(&harness.pool)
    .await?;
    assert!(
        invariant_holds,
        "a voucher must not commit as redeemed without resetting at least one subscription"
    );
    harness.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18; run this test binary with --test-threads=1"]
async fn reset_execute_audit_failure_rolls_back_mutation() -> TestResult {
    let harness = PgHarness::new().await?;
    seed_active_subscription(&harness.pool, 7, 3, 11, 53).await?;
    sqlx::query(
        "CREATE FUNCTION reject_reset_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.other::jsonb #>> '{op,action}' = 'subscription.reset.execute' THEN RAISE EXCEPTION 'forced reset audit failure'; END IF; RETURN NEW; END $$",
    )
    .execute(&harness.pool)
    .await?;
    sqlx::query(
        "CREATE TRIGGER reject_reset_audit BEFORE INSERT ON logs FOR EACH ROW EXECUTE FUNCTION reject_reset_audit()",
    )
    .execute(&harness.pool)
    .await?;
    let app = harness.app();
    let token = create_preview(&app, "hard", json!([{"user_id":7,"plan_id":3}])).await?;
    let body = response_json(
        request(
            app,
            Method::POST,
            "/api/subscription/root/reset",
            "root",
            Some(json!({"operation_id":"audit-rollback","preview_token":token})),
        )
        .await?,
    )
    .await?;
    assert_eq!(body["success"], false);
    let state: (i64, i64, i64, i64) = sqlx::query_as(
        "SELECT (SELECT amount_used FROM user_subscriptions WHERE id=11), (SELECT COUNT(*) FROM subscription_reset_events), (SELECT COUNT(*) FROM subscription_reset_operations), (SELECT consumed_at FROM subscription_reset_previews WHERE token=$1)",
    )
    .bind(&token)
    .fetch_one(&harness.pool)
    .await?;
    assert_eq!(state, (53, 0, 0, 0));
    harness.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18; run this test binary with --test-threads=1"]
async fn reset_execute_idempotent_replay_does_not_duplicate_durable_audit() -> TestResult {
    let harness = PgHarness::new().await?;
    seed_active_subscription(&harness.pool, 7, 3, 11, 61).await?;
    let app = harness.app();
    let token = create_preview(&app, "hard", json!([{"user_id":7,"plan_id":3}])).await?;
    let payload = json!({"operation_id":"audit-replay","preview_token":token});
    let first = response_json(
        request(
            app.clone(),
            Method::POST,
            "/api/subscription/root/reset",
            "root",
            Some(payload.clone()),
        )
        .await?,
    )
    .await?;
    let replay = response_json(
        request(
            app,
            Method::POST,
            "/api/subscription/root/reset",
            "root",
            Some(payload),
        )
        .await?,
    )
    .await?;
    assert_eq!(first["data"], replay["data"]);
    let state: (i64, i64, i64, i64, String) = sqlx::query_as(
        "SELECT (SELECT amount_used FROM user_subscriptions WHERE id=11), (SELECT COUNT(*) FROM subscription_reset_events WHERE operation_id='audit-replay'), (SELECT COUNT(*) FROM subscription_reset_operations WHERE operation_id='audit-replay'), (SELECT COUNT(*) FROM logs WHERE other::jsonb #>> '{op,action}'='subscription.reset.execute'), (SELECT ip FROM logs WHERE other::jsonb #>> '{op,action}'='subscription.reset.execute' LIMIT 1)",
    )
    .fetch_one(&harness.pool)
    .await?;
    assert_eq!(state, (0, 1, 1, 1, "203.0.113.7".to_owned()));
    harness.cleanup().await
}

#[tokio::test]
#[ignore = "requires TEST_POSTGRES_URL from run-real-integration-gates.sh"]
async fn reset_execute_rejects_a_deleted_target_user_without_consuming_preview() -> TestResult {
    let harness = PgHarness::new().await?;
    seed_active_subscription(&harness.pool, 8, 4, 12, 71).await?;
    let app = harness.app();
    let token = create_preview(&app, "soft", json!([{"user_id":8,"plan_id":4}])).await?;
    sqlx::query("UPDATE users SET deleted_at=CURRENT_TIMESTAMP WHERE id=8")
        .execute(&harness.pool)
        .await?;
    let response = response_json(
        request(
            app,
            Method::POST,
            "/api/subscription/root/reset",
            "root",
            Some(json!({"operation_id":"deleted-target","preview_token":token})),
        )
        .await?,
    )
    .await?;
    assert_eq!(response["success"], false);
    let state: (i64, i64, i64, i64, i64) = sqlx::query_as(
        "SELECT (SELECT consumed_at FROM subscription_reset_previews WHERE token=$1), (SELECT COUNT(*) FROM subscription_reset_vouchers WHERE operation_id='deleted-target'), (SELECT COUNT(*) FROM subscription_reset_events WHERE operation_id='deleted-target'), (SELECT COUNT(*) FROM subscription_reset_operations WHERE operation_id='deleted-target'), (SELECT amount_used FROM user_subscriptions WHERE id=12)",
    )
    .bind(&token)
    .fetch_one(&harness.pool)
    .await?;
    assert_eq!(state, (0, 0, 0, 0, 71));
    harness.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18; run this test binary with --test-threads=1"]
async fn admin_delete_waits_for_user_before_locking_subscription() -> TestResult {
    let harness = PgHarness::new().await?;
    seed_active_subscription(&harness.pool, 8, 4, 12, 71).await?;
    sqlx::query("UPDATE users SET \"group\"='pro' WHERE id=8")
        .execute(&harness.pool)
        .await?;
    sqlx::query("UPDATE user_subscriptions SET upgrade_group='pro',prev_user_group='default',downgrade_group='default' WHERE id=12")
        .execute(&harness.pool)
        .await?;
    let app = harness.app();

    let mut payment = harness.pool.begin().await?;
    let payment_pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
        .fetch_one(&mut *payment)
        .await?;
    sqlx::query("SELECT id FROM users WHERE id=8 FOR UPDATE")
        .execute(&mut *payment)
        .await?;
    let deletion = tokio::spawn(request(
        app,
        Method::DELETE,
        "/api/subscription/admin/user_subscriptions/12",
        "root",
        None,
    ));
    wait_until_blocked_by(&harness.pool, payment_pid).await?;
    assert_subscription_row_unlocked(&harness.pool, 12).await?;
    payment.rollback().await?;

    let body = response_json(deletion.await??).await?;
    assert_eq!(body["success"], true, "delete failed: {body}");
    let state: (i64, String) = sqlx::query_as(
        "SELECT (SELECT COUNT(*) FROM user_subscriptions WHERE id=12), (SELECT \"group\" FROM users WHERE id=8)",
    )
    .fetch_one(&harness.pool)
    .await?;
    assert_eq!(state, (0, "default".to_owned()));
    harness.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18; run this test binary with --test-threads=1"]
async fn maintenance_expiration_waits_for_user_before_updating_subscription() -> TestResult {
    let harness = PgHarness::new().await?;
    seed_active_subscription(&harness.pool, 8, 4, 12, 71).await?;
    let current = database_timestamp(&harness.pool).await?;
    sqlx::query("UPDATE users SET \"group\"='pro' WHERE id=8")
        .execute(&harness.pool)
        .await?;
    sqlx::query("UPDATE user_subscriptions SET end_time=$1-1,upgrade_group='pro',prev_user_group='default',downgrade_group='default' WHERE id=12")
        .bind(current)
        .execute(&harness.pool)
        .await?;

    let mut payment = harness.pool.begin().await?;
    let payment_pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
        .fetch_one(&mut *payment)
        .await?;
    sqlx::query("SELECT id FROM users WHERE id=8 FOR UPDATE")
        .execute(&mut *payment)
        .await?;
    let maintenance = spawn_maintenance(harness.pool.clone(), None)
        .ok_or_else(|| std::io::Error::other("subscription maintenance is disabled"))?;
    wait_until_blocked_by(&harness.pool, payment_pid).await?;
    assert_subscription_row_unlocked(&harness.pool, 12).await?;
    payment.rollback().await?;

    timeout(Duration::from_secs(5), async {
        loop {
            let state: (String, String) = sqlx::query_as(
                "SELECT (SELECT status FROM user_subscriptions WHERE id=12), (SELECT \"group\" FROM users WHERE id=8)",
            )
            .fetch_one(&harness.pool)
            .await?;
            if state == ("expired".to_owned(), "default".to_owned()) {
                return Ok::<(), sqlx::Error>(());
            }
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    })
    .await??;
    maintenance.abort();
    let _ = maintenance.await;
    harness.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18; run this test binary with --test-threads=1"]
async fn reset_execute_rechecks_subscriptions_after_waiting_for_user_lock() -> TestResult {
    let harness = PgHarness::new().await?;
    seed_active_subscription(&harness.pool, 8, 4, 12, 71).await?;
    let now = database_timestamp(&harness.pool).await?;
    let app = harness.app();
    let token = create_preview(&app, "hard", json!([{"user_id":8,"plan_id":4}])).await?;

    let mut payment = harness.pool.begin().await?;
    let payment_pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
        .fetch_one(&mut *payment)
        .await?;
    sqlx::query("SELECT id FROM users WHERE id=8 FOR UPDATE")
        .execute(&mut *payment)
        .await?;
    let execute = tokio::spawn(request(
        app,
        Method::POST,
        "/api/subscription/root/reset",
        "root",
        Some(json!({"operation_id":"payment-race","preview_token":token})),
    ));
    wait_until_blocked_by(&harness.pool, payment_pid).await?;
    sqlx::query("INSERT INTO user_subscriptions (id,user_id,plan_id,amount_total,amount_used,start_time,end_time,status,source,created_at,updated_at) VALUES (13,8,4,100,29,$1-60,$1+3600,'active','order',$1,$1)")
        .bind(now)
        .execute(&mut *payment)
        .await?;
    payment.commit().await?;

    let body = response_json(execute.await??).await?;
    assert_eq!(body["success"], false, "stale preview executed: {body}");
    let state: (i64, i64, i64, i64) = sqlx::query_as(
        "SELECT (SELECT consumed_at FROM subscription_reset_previews WHERE token=$1), (SELECT COUNT(*) FROM subscription_reset_operations WHERE operation_id='payment-race'), (SELECT amount_used FROM user_subscriptions WHERE id=12), (SELECT amount_used FROM user_subscriptions WHERE id=13)",
    )
    .bind(&token)
    .fetch_one(&harness.pool)
    .await?;
    assert_eq!(state, (0, 0, 71, 29));
    harness.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18; run this test binary with --test-threads=1"]
async fn target_search_and_all_matching_preview_match_plan_titles() -> TestResult {
    let harness = PgHarness::new().await?;
    seed_active_subscription(&harness.pool, 8, 4, 12, 71).await?;
    sqlx::query("UPDATE subscription_plans SET title='Enterprise Gold' WHERE id=4")
        .execute(&harness.pool)
        .await?;
    let app = harness.app();

    let targets = response_json(
        request(
            app.clone(),
            Method::GET,
            "/api/subscription/root/reset-targets?query=ENTERPRISE",
            "root",
            None,
        )
        .await?,
    )
    .await?;
    assert_eq!(targets["success"], true, "target search failed: {targets}");
    assert_eq!(targets["data"]["total"], 1);
    assert_eq!(targets["data"]["items"][0]["user_id"], 8);
    assert_eq!(targets["data"]["items"][0]["plan_id"], 4);

    let preview = response_json(
        request(
            app,
            Method::POST,
            "/api/subscription/root/reset/preview",
            "root",
            Some(json!({
                "mode":"hard",
                "all_matching":true,
                "filter":{"query":"ENTERPRISE"}
            })),
        )
        .await?,
    )
    .await?;
    assert_eq!(preview["success"], true, "preview failed: {preview}");
    assert_eq!(preview["data"]["target_count"], 1);
    assert_eq!(preview["data"]["targets"][0]["user_id"], 8);
    assert_eq!(preview["data"]["targets"][0]["plan_id"], 4);
    harness.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18; run this test binary with --test-threads=1"]
async fn near_limit_reset_preview_payload_is_not_truncated() -> TestResult {
    let harness = PgHarness::new().await?;
    let now = database_timestamp(&harness.pool).await?;
    sqlx::query("INSERT INTO subscription_plans (id,title) VALUES (3,'Near Limit')")
        .execute(&harness.pool)
        .await?;
    sqlx::query("INSERT INTO users (id,username,email,\"group\") SELECT 10000+n,'user-'||n,'user-'||n||'@example.test','default' FROM generate_series(1,4999) n")
        .execute(&harness.pool)
        .await?;
    sqlx::query("INSERT INTO user_subscriptions (id,user_id,plan_id,amount_total,amount_used,start_time,end_time,status,source,created_at,updated_at) SELECT 20000+n,10000+n,3,100,n%100,$1-60,$1+3600,'active','admin',$1,$1 FROM generate_series(1,4999) n")
        .bind(now)
        .execute(&harness.pool)
        .await?;
    let body = response_json(
        request(
            harness.app(),
            Method::POST,
            "/api/subscription/root/reset/preview",
            "root",
            Some(json!({"mode":"hard","all_matching":true,"filter":{}})),
        )
        .await?,
    )
    .await?;
    assert_eq!(body["data"]["target_count"], 4_999);
    assert_eq!(
        body["data"]["targets"].as_array().map(Vec::len),
        Some(4_999)
    );
    let token = body["data"]["token"].as_str().expect("preview token");
    let stored: String =
        sqlx::query_scalar("SELECT targets_json FROM subscription_reset_previews WHERE token=$1")
            .bind(token)
            .fetch_one(&harness.pool)
            .await?;
    assert_eq!(
        serde_json::from_str::<Vec<Value>>(&stored)?.len(),
        4_999,
        "the durable preview payload must retain every selected target"
    );
    harness.cleanup().await
}

async fn seed_active_subscription(
    pool: &PgPool,
    user_id: i64,
    plan_id: i64,
    subscription_id: i64,
    amount_used: i64,
) -> TestResult {
    let now = database_timestamp(pool).await?;
    sqlx::query("INSERT INTO users (id,username,email,\"group\") VALUES ($1,'voucher-user','voucher@example.test','default')")
        .bind(user_id)
        .execute(pool)
        .await?;
    sqlx::query("INSERT INTO subscription_plans (id,title) VALUES ($1,'Reset Plan')")
        .bind(plan_id)
        .execute(pool)
        .await?;
    sqlx::query("INSERT INTO user_subscriptions (id,user_id,plan_id,amount_total,amount_used,start_time,end_time,status,source,created_at,updated_at) VALUES ($1,$2,$3,100,$4,$5-60,$5+3600,'active','admin',$5,$5)")
        .bind(subscription_id)
        .bind(user_id)
        .bind(plan_id)
        .bind(amount_used)
        .bind(now)
        .execute(pool)
        .await?;
    Ok(())
}

async fn create_preview(app: &Router, mode: &str, targets: Value) -> TestResult<String> {
    let body = response_json(
        request(
            app.clone(),
            Method::POST,
            "/api/subscription/root/reset/preview",
            "root",
            Some(json!({"mode":mode,"targets":targets})),
        )
        .await?,
    )
    .await?;
    assert_eq!(body["success"], true, "preview failed: {body}");
    Ok(body["data"]["token"]
        .as_str()
        .expect("preview token")
        .to_owned())
}

async fn request(
    app: Router,
    method: Method,
    uri: &str,
    token: &str,
    body: Option<Value>,
) -> Result<Response, std::convert::Infallible> {
    let mut builder = Request::builder()
        .method(method)
        .uri(uri)
        .header("authorization", format!("Bearer {token}"));
    let body = match body {
        Some(value) => {
            builder = builder.header("content-type", "application/json");
            Body::from(value.to_string())
        }
        None => Body::empty(),
    };
    let mut request = builder.body(body).expect("request");
    request
        .extensions_mut()
        .insert(ClientIpKey("203.0.113.7".to_owned()));
    app.oneshot(request).await
}

async fn response_json(response: Response) -> TestResult<Value> {
    let bytes = to_bytes(response.into_body(), usize::MAX).await?;
    Ok(serde_json::from_slice(&bytes)?)
}

async fn database_timestamp(pool: &PgPool) -> TestResult<i64> {
    Ok(
        sqlx::query_scalar("SELECT EXTRACT(EPOCH FROM NOW())::BIGINT")
            .fetch_one(pool)
            .await?,
    )
}

async fn assert_subscription_row_unlocked(pool: &PgPool, id: i64) -> TestResult {
    let mut probe = pool.begin().await?;
    let locked_id: i64 =
        sqlx::query_scalar("SELECT id FROM user_subscriptions WHERE id=$1 FOR UPDATE NOWAIT")
            .bind(id)
            .fetch_one(&mut *probe)
            .await?;
    probe.rollback().await?;
    assert_eq!(locked_id, id);
    Ok(())
}

async fn wait_until_blocked_by(pool: &PgPool, blocker_pid: i32) -> TestResult {
    timeout(Duration::from_secs(5), async {
        loop {
            let blocked: bool = sqlx::query_scalar(
                "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))",
            )
            .bind(blocker_pid)
            .fetch_one(pool)
            .await?;
            if blocked {
                return Ok::<(), sqlx::Error>(());
            }
            tokio::task::yield_now().await;
        }
    })
    .await??;
    Ok(())
}

async fn wait_until_finished_or_blocked_behind_redeem(
    pool: &PgPool,
    advisory_blocker_pid: i32,
    mutation: &ResponseTask,
) -> TestResult {
    timeout(Duration::from_secs(5), async {
        loop {
            if mutation.is_finished() {
                return Ok::<(), sqlx::Error>(());
            }
            let blocked_behind_redeem: bool = sqlx::query_scalar(
                "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE CARDINALITY(pg_blocking_pids(pid))>0 AND NOT $1=ANY(pg_blocking_pids(pid)))",
            )
            .bind(advisory_blocker_pid)
            .fetch_one(pool)
            .await?;
            if blocked_behind_redeem {
                return Ok(());
            }
            tokio::task::yield_now().await;
        }
    })
    .await??;
    Ok(())
}
