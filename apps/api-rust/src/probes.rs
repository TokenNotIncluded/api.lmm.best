use async_trait::async_trait;
use lmm_application::{ProbeError, ReadinessProbe};
use sqlx::{PgPool, Row};
use std::time::Duration;

const STATUS_SCHEMA_SELECTS: &[&str] = &[
    "SELECT key, value FROM options WHERE FALSE",
    "SELECT id, name, slug, icon, client_id, authorization_endpoint, scopes FROM custom_oauth_providers WHERE enabled = TRUE ORDER BY id ASC LIMIT 0",
    "SELECT EXISTS(SELECT 1 FROM setups)",
];

const AUTH_SCHEMA_SELECTS: &[&str] = &[
    r#"SELECT id, username, password, display_name, role, status, email, github_id,
discord_id, oidc_id, wechat_id, telegram_id, "group", quota, used_quota,
request_count, aff_code, aff_count, aff_quota, aff_history, inviter_id,
linux_do_id, setting, stripe_customer, auth_version, console_activated_at, access_token, deleted_at
FROM users WHERE FALSE"#,
    r#"SELECT sid, user_id, version, user_auth_version, status, refresh_hash,
previous_refresh_hash, previous_valid_until, login_method, ip, user_agent,
created_at, last_active_at, expires_at, revoked_at, revoked_reason
FROM user_sessions WHERE FALSE"#,
    "SELECT id, user_id, is_enabled, deleted_at FROM two_fas WHERE FALSE",
    "SELECT ptype, v0, v1, v2, v3 FROM casbin_rule WHERE FALSE",
    // EXPLAIN validates INSERT/UPDATE permissions and referenced defaults
    // without changing auth-flow state during a rollout readiness probe.
    "EXPLAIN (COSTS FALSE) INSERT INTO auth_flows (token_hash, purpose, user_id, payload, created_at, expires_at) VALUES ('readiness-capability-check', '2fa_login', 0, '{}', NOW(), NOW())",
    "EXPLAIN (COSTS FALSE) UPDATE auth_flows SET consumed_at = NOW() WHERE FALSE",
];

const API_TOKEN_SCHEMA_SELECTS: &[&str] = &[
    r#"SELECT id, user_id, key, status, name, created_time, accessed_time, expired_time,
remain_quota, unlimited_quota, model_limits_enabled, model_limits, allow_ips, used_quota,
"group", cross_group_retry, deleted_at FROM tokens WHERE FALSE"#,
    // `DEFAULT` invokes the owned ID sequence without writing a row, so the
    // readiness gate proves the serving role can use the token-ID default.
    r#"EXPLAIN (COSTS FALSE) INSERT INTO tokens (id, user_id, key, status, name,
created_time, accessed_time, expired_time, remain_quota, unlimited_quota,
model_limits_enabled, model_limits, allow_ips, used_quota, "group", cross_group_retry)
VALUES (DEFAULT, 0, 'readiness-capability-check', 1, '', 0, 0, -1, 0, FALSE, FALSE, '', '', 0, '', FALSE)"#,
    r#"EXPLAIN (COSTS FALSE) UPDATE tokens SET status = 1, name = '', expired_time = -1,
remain_quota = 0, unlimited_quota = FALSE, model_limits_enabled = FALSE, model_limits = '',
allow_ips = '', "group" = '', cross_group_retry = FALSE, deleted_at = NULL WHERE FALSE"#,
    "EXPLAIN (COSTS FALSE) DELETE FROM tokens WHERE FALSE",
    "EXPLAIN (COSTS FALSE) UPDATE users SET console_activated_at = EXTRACT(EPOCH FROM NOW())::BIGINT WHERE FALSE",
];

const OPEN_SOURCE_BOUNTY_SCHEMA_SELECTS: &[&str] = &[
    "SELECT id, owner_user_id, repository_url, title, description, rules, reward_quota, net_reward_quota, reward_slots, escrow_quota, platform_fee_rate_bps, platform_fee_quota, status, created_at, updated_at, published_at, closed_at FROM open_source_bounty_projects WHERE FALSE",
    "SELECT id, project_id, participant_user_id, github_handle, status, issue_url, pull_request_url, submission_note, review_note, reward_quota, tip_quota, owner_rating_score, owner_rating_comment, owner_rated_at, contributor_rating_score, contributor_rating_comment, contributor_rated_at, owner_rating_overturned, accepted_at, submitted_at, reviewed_at, rejected_at, paid_at, created_at, updated_at FROM open_source_bounty_challenges WHERE FALSE",
    "SELECT id, project_id, challenge_id, user_id, counterparty_user_id, kind, quota, note, recipient_read_at, thanked_at, created_at FROM open_source_bounty_ledgers WHERE FALSE",
    "SELECT id, challenge_id, project_id, opened_by_user_id, against_user_id, reason, statement, project_title_snapshot, repository_url_snapshot, project_rules_snapshot, project_escrow_quota_snapshot, challenge_status_snapshot, issue_url_snapshot, pull_request_url_snapshot, submission_note_snapshot, review_note_snapshot, reward_quota_snapshot, tip_quota_snapshot, owner_rating_score_snapshot, owner_rating_comment_snapshot, contributor_rating_score_snapshot, contributor_rating_comment_snapshot, status, resolution, resolved_by_user_id, created_at, updated_at, resolved_at FROM open_source_bounty_disputes WHERE FALSE",
];

const CURRENT_DASHBOARD_SCHEMA_SELECTS: &[&str] = &[
    "SELECT archived_at FROM open_source_bounty_projects WHERE FALSE",
    "SELECT id, user_id, status, source, reason, ai_recommendation, admin_user_id, admin_note, created_at, reviewed_at FROM developer_access_requests WHERE FALSE",
    "SELECT id, version, revision, content, published_at, published_by FROM release_notes WHERE FALSE",
    "SELECT id, release_note_id, user_id, read_at FROM release_note_reads WHERE FALSE",
    "SELECT id, title, description, quota, start_at, end_at, min_used_quota, min_account_age_days, enabled, created_at FROM gifts WHERE FALSE",
    "SELECT id, gift_id, user_id, username, quota, created_at FROM gift_claims WHERE FALSE",
    r#"SELECT id, created_at, request_id, user_id, username, token_id, channel_id,
model_name, "group", endpoint, decision, rule_id, rule_name, category, layer,
severity, source, rule_version, pattern_digest, input_digest, match_count
FROM advanced_security_events WHERE FALSE"#,
    "SELECT user_id, ip, created_at, updated_at FROM personal_access_ips WHERE FALSE",
];

pub struct InfrastructureProbe {
    pg: PgPool,
    valkey: redis::Client,
    schema_contract: i64,
    timeout: Duration,
}

impl InfrastructureProbe {
    pub fn new(pg: PgPool, valkey: redis::Client, schema_contract: i64, timeout: Duration) -> Self {
        Self {
            pg,
            valkey,
            schema_contract,
            timeout,
        }
    }
}

#[async_trait]
impl ReadinessProbe for InfrastructureProbe {
    async fn postgres(&self) -> Result<(), ProbeError> {
        tokio::time::timeout(self.timeout, sqlx::query("SELECT 1").execute(&self.pg))
            .await
            .map_err(|_| failed("postgres"))?
            .map_err(|_| failed("postgres"))?;
        Ok(())
    }

    async fn valkey(&self) -> Result<(), ProbeError> {
        tokio::time::timeout(self.timeout, async {
            let mut connection = self.valkey.get_multiplexed_async_connection().await?;
            redis::cmd("PING")
                .query_async::<String>(&mut connection)
                .await
        })
        .await
        .map_err(|_| failed("valkey"))?
        .map_err(|_| failed("valkey"))?;
        Ok(())
    }

    async fn schema_compatible(&self) -> Result<(), ProbeError> {
        schema_compatible_with(
            &PostgresSchemaBackend {
                pg: &self.pg,
                timeout: self.timeout,
            },
            self.schema_contract,
        )
        .await
    }
}

#[async_trait]
trait SchemaBackend: Sync {
    async fn reader_range(&self) -> Result<(i64, i64), ProbeError>;
    async fn verify_select(&self, query: &'static str) -> Result<(), ProbeError>;
}

struct PostgresSchemaBackend<'a> {
    pg: &'a PgPool,
    timeout: Duration,
}

#[async_trait]
impl SchemaBackend for PostgresSchemaBackend<'_> {
    async fn reader_range(&self) -> Result<(i64, i64), ProbeError> {
        let row = tokio::time::timeout(
            self.timeout,
            sqlx::query(
                "SELECT min_reader_version, max_reader_version FROM lmm_schema_contract WHERE singleton = TRUE",
            )
            .fetch_one(self.pg),
        )
        .await
        .map_err(|_| failed("schema"))?
        .map_err(|_| failed("schema"))?;
        let min = row
            .try_get("min_reader_version")
            .map_err(|_| failed("schema"))?;
        let max = row
            .try_get("max_reader_version")
            .map_err(|_| failed("schema"))?;
        Ok((min, max))
    }

    async fn verify_select(&self, query: &'static str) -> Result<(), ProbeError> {
        tokio::time::timeout(self.timeout, sqlx::query(query).execute(self.pg))
            .await
            .map_err(|_| failed("schema"))?
            .map_err(|_| failed("schema"))?;
        Ok(())
    }
}

async fn schema_compatible_with(
    backend: &dyn SchemaBackend,
    schema_contract: i64,
) -> Result<(), ProbeError> {
    let (min, max) = backend.reader_range().await?;
    if !(min..=max).contains(&schema_contract) {
        return Err(failed("schema"));
    }
    for query in STATUS_SCHEMA_SELECTS {
        backend.verify_select(query).await?;
    }
    for query in AUTH_SCHEMA_SELECTS {
        backend.verify_select(query).await?;
    }
    for query in API_TOKEN_SCHEMA_SELECTS {
        backend.verify_select(query).await?;
    }
    for query in OPEN_SOURCE_BOUNTY_SCHEMA_SELECTS {
        backend.verify_select(query).await?;
    }
    if schema_contract >= 3 {
        for query in CURRENT_DASHBOARD_SCHEMA_SELECTS {
            backend.verify_select(query).await?;
        }
    }
    Ok(())
}

fn failed(dependency: &'static str) -> ProbeError {
    ProbeError { dependency }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::{Mutex, MutexGuard};

    fn lock_unpoisoned<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
        match mutex.lock() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        }
    }

    struct MockSchemaBackend {
        range: (i64, i64),
        failing_query: Option<&'static str>,
        seen: Mutex<Vec<&'static str>>,
    }

    #[async_trait]
    impl SchemaBackend for MockSchemaBackend {
        async fn reader_range(&self) -> Result<(i64, i64), ProbeError> {
            Ok(self.range)
        }

        async fn verify_select(&self, query: &'static str) -> Result<(), ProbeError> {
            lock_unpoisoned(&self.seen).push(query);
            if self.failing_query == Some(query) {
                Err(failed("schema"))
            } else {
                Ok(())
            }
        }
    }

    fn backend(failing_query: Option<&'static str>) -> MockSchemaBackend {
        MockSchemaBackend {
            range: (1, 3),
            failing_query,
            seen: Mutex::new(Vec::new()),
        }
    }

    #[tokio::test]
    async fn schema_readiness_verifies_every_mounted_route_select() {
        let backend = backend(None);
        assert!(schema_compatible_with(&backend, 2).await.is_ok());
        assert_eq!(
            lock_unpoisoned(&backend.seen).as_slice(),
            [
                STATUS_SCHEMA_SELECTS,
                AUTH_SCHEMA_SELECTS,
                API_TOKEN_SCHEMA_SELECTS,
                OPEN_SOURCE_BOUNTY_SCHEMA_SELECTS,
            ]
            .concat()
        );
    }

    #[tokio::test]
    async fn schema_readiness_fails_when_any_mounted_route_select_is_missing_or_denied() {
        for query in STATUS_SCHEMA_SELECTS
            .iter()
            .chain(AUTH_SCHEMA_SELECTS)
            .chain(API_TOKEN_SCHEMA_SELECTS)
            .chain(OPEN_SOURCE_BOUNTY_SCHEMA_SELECTS)
        {
            let backend = backend(Some(query));
            let dependency = schema_compatible_with(&backend, 2)
                .await
                .err()
                .map(|error| error.dependency);
            assert_eq!(dependency, Some("schema"), "query: {query}");
        }
    }

    #[tokio::test]
    async fn contract_three_requires_the_bounty_archive_column() {
        let backend = backend(Some(CURRENT_DASHBOARD_SCHEMA_SELECTS[0]));
        let dependency = schema_compatible_with(&backend, 3)
            .await
            .err()
            .map(|error| error.dependency);

        assert_eq!(dependency, Some("schema"));
        assert_eq!(
            lock_unpoisoned(&backend.seen).last(),
            Some(&CURRENT_DASHBOARD_SCHEMA_SELECTS[0])
        );
    }

    #[tokio::test]
    async fn schema_readiness_rejects_an_incompatible_reader_before_status_selects() {
        let backend = MockSchemaBackend {
            range: (3, 4),
            failing_query: None,
            seen: Mutex::new(Vec::new()),
        };
        let dependency = schema_compatible_with(&backend, 2)
            .await
            .err()
            .map(|error| error.dependency);
        assert_eq!(dependency, Some("schema"));
        assert!(lock_unpoisoned(&backend.seen).is_empty());
    }
}
