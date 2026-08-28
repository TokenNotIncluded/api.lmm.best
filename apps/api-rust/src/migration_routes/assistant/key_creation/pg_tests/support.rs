use std::{error::Error, fmt::Display, io, ops::Deref, time::Duration};

use secrecy::SecretString;
use sqlx::{PgPool, postgres::PgPoolOptions};
use tokio::time::timeout;

use super::super::{domain::*, repository::*};
use crate::auth::DashboardDeveloperAccessPolicy;
use crate::migration_routes::assistant::{PgAssistantReadStore, pg_test_support::IsolatedPgSchema};

pub(super) type TestResult<T = ()> = Result<T, Box<dyn Error + Send + Sync>>;

pub(super) fn test_error(error: impl Display) -> Box<dyn Error + Send + Sync> {
    Box::new(io::Error::other(error.to_string()))
}

pub(super) struct PgHarness(IsolatedPgSchema);

impl Deref for PgHarness {
    type Target = IsolatedPgSchema;

    fn deref(&self) -> &Self::Target {
        &self.0
    }
}

impl PgHarness {
    pub(super) async fn new() -> TestResult<Option<Self>> {
        let database_url = match std::env::var("LMM_TEST_DATABASE_URL") {
            Ok(database_url) => database_url,
            Err(std::env::VarError::NotPresent) => return Ok(None),
            Err(error) => return Err(error.into()),
        };
        let admin = PgPool::connect(&database_url).await?;
        let schema = format!("assistant_key_{}", uuid::Uuid::new_v4().simple());
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&admin)
            .await?;
        let pool = match PgPoolOptions::new()
            .max_connections(12)
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
            .await
        {
            Ok(pool) => pool,
            Err(error) => {
                let cleanup = sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
                    .execute(&admin)
                    .await;
                admin.close().await;
                cleanup?;
                return Err(error.into());
            }
        };
        let harness = Self(IsolatedPgSchema {
            admin,
            pool,
            schema,
        });
        if let Err(error) = harness.create_schema().await {
            if let Err(cleanup_error) = harness.cleanup().await {
                return Err(test_error(format!(
                    "{error}; additionally failed to clean the test schema: {cleanup_error}"
                )));
            }
            return Err(error);
        }
        Ok(Some(harness))
    }

    async fn create_schema(&self) -> TestResult {
        for statement in [
            "CREATE TABLE users (id BIGINT PRIMARY KEY, username TEXT NOT NULL, role BIGINT NOT NULL DEFAULT 1, status INTEGER NOT NULL, auth_version BIGINT NOT NULL DEFAULT 1, trust_level_override BIGINT, created_at BIGINT NOT NULL DEFAULT 1, last_api_activity_at BIGINT NOT NULL DEFAULT 0, deleted_at TIMESTAMPTZ, \"group\" TEXT NOT NULL, console_activated_at BIGINT NOT NULL DEFAULT 0)",
            "CREATE TABLE user_sessions (sid TEXT PRIMARY KEY, user_id BIGINT NOT NULL, status TEXT NOT NULL, version BIGINT NOT NULL, user_auth_version BIGINT NOT NULL, expires_at BIGINT NOT NULL, revoked_at BIGINT NOT NULL DEFAULT 0)",
            "CREATE TABLE top_ups (id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL, status TEXT NOT NULL, money NUMERIC NOT NULL DEFAULT 0, quota BIGINT NOT NULL DEFAULT 0, amount BIGINT NOT NULL DEFAULT 0, trade_no TEXT NOT NULL DEFAULT '', complete_time BIGINT NOT NULL DEFAULT 0, deleted_at TIMESTAMPTZ)",
            "CREATE TABLE options (key TEXT PRIMARY KEY, value TEXT NOT NULL)",
            "CREATE TABLE auth_flows (id BIGSERIAL PRIMARY KEY, token_hash TEXT NOT NULL UNIQUE, purpose TEXT NOT NULL, user_id BIGINT NOT NULL, session_id TEXT NOT NULL, payload TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL, consumed_at TIMESTAMPTZ)",
            "CREATE TABLE tokens (id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL, key TEXT NOT NULL, status INTEGER NOT NULL, name TEXT NOT NULL, created_time BIGINT NOT NULL, accessed_time BIGINT NOT NULL, expired_time BIGINT NOT NULL, remain_quota BIGINT NOT NULL, unlimited_quota BOOLEAN NOT NULL, model_limits_enabled BOOLEAN NOT NULL, model_limits TEXT NOT NULL, allow_ips TEXT NOT NULL, used_quota BIGINT NOT NULL, \"group\" TEXT NOT NULL, cross_group_retry BOOLEAN NOT NULL, auto_groups TEXT NOT NULL, deleted_at TIMESTAMPTZ)",
            "CREATE TABLE assistant_secure_cards (id TEXT PRIMARY KEY, owner_user_id BIGINT NOT NULL, conversation_id BIGINT NOT NULL, message_id BIGINT NOT NULL, type TEXT NOT NULL, summary TEXT NOT NULL, ciphertext TEXT NOT NULL, created_at BIGINT NOT NULL, expires_at BIGINT NOT NULL, revealed_at BIGINT NOT NULL)",
            "CREATE TABLE logs (id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL, created_at BIGINT NOT NULL, type INTEGER NOT NULL, content TEXT NOT NULL, username TEXT NOT NULL, request_id TEXT NOT NULL)",
            "CREATE TABLE two_fas (id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL, secret TEXT NOT NULL, is_enabled BOOLEAN NOT NULL, locked_until TIMESTAMPTZ, deleted_at TIMESTAMPTZ, failed_attempts BIGINT NOT NULL DEFAULT 0, last_used_at TIMESTAMPTZ, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())",
            "CREATE TABLE two_fa_backup_codes (id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL, code_hash TEXT NOT NULL, is_used BOOLEAN NOT NULL DEFAULT FALSE, used_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ)",
        ] {
            sqlx::query(statement).execute(&self.pool).await?;
        }
        sqlx::query(
            "INSERT INTO users (id, username, status, auth_version, trust_level_override, \"group\") VALUES (7, 'assistant-user', 1, 1, 1, 'default')",
        )
        .execute(&self.pool)
        .await?;
        for (key, value) in [
            ("UserUsableGroups", r#"{"default":"Default","vip":"VIP"}"#),
            ("GroupRatio", r#"{"default":1,"vip":2}"#),
            ("AutoGroups", r#"["default","vip"]"#),
            ("group_ratio_setting.group_warnings", "{}"),
            ("token_setting.max_user_tokens", "100"),
        ] {
            sqlx::query("INSERT INTO options (key, value) VALUES ($1, $2)")
                .bind(key)
                .bind(value)
                .execute(&self.pool)
                .await?;
        }
        Ok(())
    }

    pub(super) fn store(&self) -> TestResult<PgAssistantReadStore> {
        Ok(PgAssistantReadStore {
            pg: self.pool.clone(),
            valkey: redis::Client::open("redis://127.0.0.1:1/")?,
            session_secret: SecretString::from("assistant-key-pg-test-secret"),
            developer_access_policy: DashboardDeveloperAccessPolicy::new(false),
        })
    }

    pub(super) async fn cleanup(self) -> TestResult {
        let IsolatedPgSchema {
            admin,
            pool,
            schema,
        } = self.0;
        pool.close().await;
        let cleanup = sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
            .execute(&admin)
            .await;
        admin.close().await;
        cleanup?;
        Ok(())
    }
}

pub(super) async fn prepare(
    store: &PgAssistantReadStore,
    session_id: &str,
    group: &str,
) -> TestResult<PreparedKeyAction> {
    sqlx::query(
        "INSERT INTO user_sessions (sid, user_id, status, version, user_auth_version, expires_at) \
         VALUES ($1, 7, 'active', 1, 1, EXTRACT(EPOCH FROM NOW())::BIGINT + 3600) \
         ON CONFLICT (sid) DO NOTHING",
    )
    .bind(session_id)
    .execute(&store.pg)
    .await?;
    let options = load_pg_options(store, "default")
        .await
        .map_err(test_error)?;
    let warning = options
        .iter()
        .find(|option| option.id() == group)
        .ok_or_else(|| test_error("requested test group is not selectable"))?
        .warning
        .clone();
    prepare_pg(
        store,
        7,
        session_id,
        PreparedKeyDraft {
            version: DRAFT_VERSION,
            name: "assistant-created".to_owned(),
            group: RealSelectableGroup::parse(group).map_err(test_error)?,
            conversation_id: 0,
            warning,
        },
    )
    .await
    .map_err(test_error)
}

pub(super) fn confirmation(action: &PreparedKeyAction) -> TestResult<ConfirmationToken> {
    ConfirmationToken::parse(&action.confirmation_token).map_err(test_error)
}

pub(super) fn authorization_fence(session_id: &str) -> TestResult<AuthorizationFence> {
    AuthorizationFence::capture(
        7,
        "assistant-user",
        session_id,
        1,
        1,
        DashboardDeveloperAccessPolicy::new(false),
    )
    .map_err(test_error)
}

pub(super) async fn confirm_action(
    store: &PgAssistantReadStore,
    session_id: &str,
    action: &PreparedKeyAction,
    two_factor_code: &str,
) -> TestResult<Result<CreatedKey, KeyCreationError>> {
    let result = confirm_pg(
        store,
        authorization_fence(session_id)?,
        confirmation(action)?,
        two_factor_code,
    )
    .await;
    Ok(result)
}

pub(super) async fn wait_until_blocked_by(pool: &PgPool, blocker_pid: i32) -> TestResult {
    timeout(Duration::from_secs(5), async {
        loop {
            let blocked = sqlx::query_scalar::<_, bool>(
                "SELECT EXISTS (SELECT 1 FROM pg_stat_activity activity \
                 WHERE $1 = ANY(pg_blocking_pids(activity.pid)))",
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

pub(super) async fn wait_for_granted_option_share(pool: &PgPool, schema: &str) -> TestResult {
    timeout(Duration::from_secs(5), async {
        loop {
            let locked = sqlx::query_scalar::<_, bool>(
                "SELECT EXISTS (SELECT 1 FROM pg_locks locks \
                 JOIN pg_class relation ON relation.oid = locks.relation \
                 JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace \
                 WHERE namespace.nspname = $1 AND relation.relname = 'options' \
                   AND locks.mode = 'ShareLock' AND locks.granted)",
            )
            .bind(schema)
            .fetch_one(pool)
            .await?;
            if locked {
                return Ok::<(), sqlx::Error>(());
            }
            tokio::task::yield_now().await;
        }
    })
    .await??;
    Ok(())
}

pub(super) async fn wait_for_option_update_lock(pool: &PgPool, schema: &str) -> TestResult {
    timeout(Duration::from_secs(5), async {
        loop {
            let blocked = sqlx::query_scalar::<_, bool>(
                "SELECT EXISTS (SELECT 1 FROM pg_locks locks \
                 JOIN pg_class relation ON relation.oid = locks.relation \
                 JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace \
                 WHERE namespace.nspname = $1 AND relation.relname = 'options' \
                   AND locks.mode = 'RowExclusiveLock' AND NOT locks.granted)",
            )
            .bind(schema)
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

pub(super) async fn count(pool: &PgPool, table: &str) -> TestResult<i64> {
    let count = sqlx::query_scalar::<_, i64>(&format!("SELECT COUNT(*) FROM {table}"))
        .fetch_one(pool)
        .await?;
    Ok(count)
}

pub(super) async fn flow_consumed(pool: &PgPool, action: &PreparedKeyAction) -> TestResult<bool> {
    let consumed = sqlx::query_scalar::<_, bool>(
        "SELECT consumed_at IS NOT NULL FROM auth_flows WHERE payload::jsonb->>'name' = $1 ORDER BY id DESC LIMIT 1",
    )
    .bind(&action.name)
    .fetch_one(pool)
    .await?;
    Ok(consumed)
}
