use super::token::{AuthIdentity, LegacyTokenCodec, random_refresh_secret, split_refresh_token};
use super::{
    AuthBundle, AuthError, AuthErrorKind, AuthResponseData, CriticalRateLimitOutcome,
    DashboardAuth, DashboardDeveloperAccessPolicy, DashboardDeveloperAccessUserFacts,
    DashboardSelfUserFacts, DashboardSessionContext, DashboardUser, DashboardUserView,
    LOGIN_SESSION_TTL_SECONDS, LoginOutcome, LoginRequest, LoginSessionView, LogoutRequest,
    LogoutResult, REFRESH_REPLAY_WINDOW_SECONDS, RequestMetadata, SecurityProof,
    SecuritySessionRotationRequest, TWO_FACTOR_FLOW_TTL_SECONDS, TwoFactorChallenge,
    TwoFactorLoginRequest,
};
use async_trait::async_trait;
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use bcrypt::verify;
use hmac::{Hmac, Mac};
use rand::RngCore;
use redis::aio::MultiplexedConnection;
use secrecy::{ExposeSecret, SecretSlice, SecretString};
use serde::Deserialize;
use serde_json::{Map, Value, json};
use sha2::Sha256;
use sqlx::{PgPool, Postgres, Row, Transaction};
use std::time::{Duration, UNIX_EPOCH};
use subtle::ConstantTimeEq;
use totp_rs::{Algorithm, Secret, TOTP};

type HmacSha256 = Hmac<Sha256>;

const ACTIVE: &str = "active";
const REVOKING: &str = "revoking";
const REVOKED: &str = "revoked";
const ENABLED: i64 = 1;

#[derive(Clone, Debug)]
pub struct AuthConfig {
    pub session_secret: SecretString,
    pub active_session_limit: i64,
    pub issuance_limit: i64,
    pub issuance_window: Duration,
    pub session_cache_ttl: Duration,
    pub dependency_timeout: Duration,
    pub critical_rate_limit_enabled: bool,
    pub critical_rate_limit: u64,
    pub critical_rate_limit_window: Duration,
}

impl Default for AuthConfig {
    fn default() -> Self {
        Self {
            session_secret: SecretString::from(String::new()),
            active_session_limit: 50,
            issuance_limit: 100,
            issuance_window: Duration::from_secs(24 * 60 * 60),
            session_cache_ttl: Duration::from_secs(600),
            dependency_timeout: Duration::from_secs(2),
            critical_rate_limit_enabled: true,
            critical_rate_limit: 20,
            critical_rate_limit_window: Duration::from_secs(20 * 60),
        }
    }
}

pub struct PgValkeyDashboardAuth {
    pool: PgPool,
    valkey: redis::Client,
    codec: LegacyTokenCodec,
    session_cache_key: SecretSlice<u8>,
    config: AuthConfig,
    local_acceptance: bool,
}

impl PgValkeyDashboardAuth {
    pub fn new(pool: PgPool, valkey: redis::Client, config: AuthConfig) -> Result<Self, AuthError> {
        if config.session_secret.expose_secret().is_empty()
            || config.active_session_limit <= 0
            || config.issuance_limit <= 0
            || config.issuance_window.is_zero()
            || config.session_cache_ttl.is_zero()
            || config.dependency_timeout.is_zero()
            || (config.critical_rate_limit_enabled
                && (config.critical_rate_limit == 0 || config.critical_rate_limit_window.is_zero()))
        {
            return Err(AuthError::new(AuthErrorKind::Internal));
        }
        let session_cache_key = SecretSlice::from(
            format!(
                "user-session-cache-v1:{}",
                config.session_secret.expose_secret()
            )
            .into_bytes(),
        );
        let codec = LegacyTokenCodec::new(config.session_secret.clone())?;
        Ok(Self {
            pool,
            valkey,
            codec,
            session_cache_key,
            config,
            local_acceptance: false,
        })
    }

    /// Enables the explicitly loopback-scoped local acceptance policy.
    ///
    /// The normal listener supplies the validated configuration value. Frozen
    /// test instances leave this disabled.
    #[must_use]
    pub fn with_local_acceptance(mut self, enabled: bool) -> Self {
        self.local_acceptance = enabled;
        self
    }

    async fn connection(&self) -> Result<MultiplexedConnection, AuthError> {
        tokio::time::timeout(
            self.config.dependency_timeout,
            self.valkey.get_multiplexed_async_connection(),
        )
        .await
        .map_err(|_| AuthError::new(AuthErrorKind::Internal))?
        .map_err(|_| AuthError::new(AuthErrorKind::Internal))
    }

    async fn user_by_username(&self, username: &str) -> Result<UserRecord, AuthError> {
        let row = sqlx::query(USER_SELECT_BY_USERNAME)
            .bind(username)
            .fetch_optional(&self.pool)
            .await
            .map_err(internal)?
            .ok_or_else(|| AuthError::new(AuthErrorKind::InvalidCredentials))?;
        user_from_row(&row)
    }

    async fn create_two_factor_flow(
        &self,
        user: &UserRecord,
    ) -> Result<TwoFactorChallenge, AuthError> {
        let mut bytes = [0_u8; 32];
        rand::rng().fill_bytes(&mut bytes);
        let token = SecretString::from(URL_SAFE_NO_PAD.encode(bytes));
        let token_hash = self.codec.hash_auth_flow(&token)?;
        let payload = json!({"auth_version": user.auth_version}).to_string();
        let expires_at: i64 = sqlx::query_scalar(
            "INSERT INTO auth_flows (token_hash, purpose, user_id, payload, created_at, expires_at) VALUES ($1, '2fa_login', $2, $3, NOW(), NOW() + make_interval(secs => $4)) RETURNING EXTRACT(EPOCH FROM expires_at)::BIGINT",
        )
        .bind(token_hash)
        .bind(user.id)
        .bind(payload)
        .bind(TWO_FACTOR_FLOW_TTL_SECONDS as f64)
        .fetch_one(&self.pool)
        .await
        .map_err(internal)?;
        Ok(TwoFactorChallenge {
            require_2fa: true,
            flow_token: token.expose_secret().to_owned(),
            expires_at,
        })
    }

    async fn user_by_id(&self, id: i64) -> Result<UserRecord, AuthError> {
        let row = sqlx::query(USER_SELECT_BY_ID)
            .bind(id)
            .fetch_optional(&self.pool)
            .await
            .map_err(internal)?
            .ok_or_else(|| AuthError::new(AuthErrorKind::Unauthorized))?;
        user_from_row(&row)
    }

    /// Resolves the legacy opaque personal access token stored on `users`.
    ///
    /// Go's dashboard middleware deliberately accepts this credential after
    /// it has established that the supplied value is not an internal
    /// dashboard JWT.  Keep the deleted-user predicate here rather than
    /// trusting a cache so revocation/deletion is authoritative per request.
    async fn user_by_personal_access_token(&self, token: &str) -> Result<UserRecord, AuthError> {
        let row = sqlx::query(USER_SELECT_BY_PERSONAL_ACCESS_TOKEN)
            .bind(token)
            .fetch_optional(&self.pool)
            .await
            .map_err(internal)?
            .ok_or_else(|| AuthError::new(AuthErrorKind::Unauthorized))?;
        user_from_row(&row)
    }

    /// Rebinds the already-authenticated current session to a freshly bumped
    /// user auth-version after a sensitive account change.
    ///
    /// Every other active session remains bound to the former auth-version and
    /// is rejected by the normal PG/Valkey validation path.  The current
    /// server-authenticated SID receives a new refresh secret, a bumped session
    /// version, and a freshly issued access token.  In particular, a replay of
    /// the old refresh cookie cannot use the normal grace window.
    pub async fn rotate_after_security_change(
        &self,
        request: SecuritySessionRotationRequest,
    ) -> Result<AuthBundle, AuthError> {
        if request.user_id <= 0 || request.session_id.trim().is_empty() || request.auth_version <= 0
        {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        let user = self.user_by_id(request.user_id).await?;
        if user.status != ENABLED || user.auth_version != request.auth_version {
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }

        let now = unix_now();
        let mut tx = self.pool.begin().await.map_err(internal)?;
        let version_locked: Option<(i64, i64)> = sqlx::query_as(
            "SELECT status, auth_version FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
        )
        .bind(request.user_id)
        .fetch_optional(&mut *tx)
        .await
        .map_err(internal)?;
        if version_locked != Some((ENABLED, request.auth_version)) {
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }
        let row = sqlx::query(SESSION_SELECT_FOR_UPDATE)
            .bind(&request.session_id)
            .fetch_optional(&mut *tx)
            .await
            .map_err(internal)?
            .ok_or_else(|| AuthError::new(AuthErrorKind::SessionRevoked))?;
        let mut session = session_from_row(&row)?;
        if session.user_id != request.user_id
            || session.status != ACTIVE
            || session.revoked_at != 0
            || session.expires_at <= now
            || session.user_auth_version >= request.auth_version
        {
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }

        let secret = random_refresh_secret();
        let refresh_hash = self.codec.hash_refresh(&secret)?;
        let next_ip = truncate(request.metadata.ip, 64);
        let next_user_agent = truncate(request.metadata.user_agent, 512);
        let next_version = session
            .version
            .checked_add(1)
            .ok_or_else(|| AuthError::new(AuthErrorKind::Internal))?;
        let updated = sqlx::query(
            "UPDATE user_sessions SET version = $3, user_auth_version = $4, refresh_hash = $5, previous_refresh_hash = NULL, previous_valid_until = 0, last_active_at = $6, ip = $7, user_agent = $8 WHERE sid = $1 AND user_id = $2 AND status = 'active' AND revoked_at = 0 AND expires_at > $6 AND version = $9 AND user_auth_version = $10",
        )
        .bind(&request.session_id)
        .bind(request.user_id)
        .bind(next_version)
        .bind(request.auth_version)
        .bind(&refresh_hash)
        .bind(now)
        .bind(&next_ip)
        .bind(&next_user_agent)
        .bind(session.version)
        .bind(session.user_auth_version)
        .execute(&mut *tx)
        .await
        .map_err(internal)?;
        if updated.rows_affected() != 1 {
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }
        tx.commit().await.map_err(internal)?;

        session.version = next_version;
        session.user_auth_version = request.auth_version;
        session.refresh_hash = refresh_hash;
        session.previous_refresh_hash.clear();
        session.previous_valid_until = 0;
        session.last_active_at = now;
        session.ip = next_ip;
        session.user_agent = next_user_agent;
        if self
            .write_session_cache(&session, ACTIVE, None)
            .await
            .is_err()
        {
            // PostgreSQL remains authoritative, but a missing cache update
            // must never leave a usable session after a security change.
            let _ = self
                .revoke_session(
                    request.user_id,
                    &request.session_id,
                    "security_cache_publish_failed",
                )
                .await;
            return Err(AuthError::new(AuthErrorKind::Internal));
        }
        self.bundle(session, user, secret).await
    }

    async fn record_failed_two_factor_attempt(
        &self,
        flow_id: i64,
        token_hash: &str,
        user_id: i64,
        factor_id: i64,
    ) -> Result<(), AuthError> {
        let mut tx = self.pool.begin().await.map_err(internal)?;
        let claimed = sqlx::query_scalar::<_, i64>(
            "SELECT id FROM auth_flows WHERE id = $1 AND token_hash = $2 AND purpose = '2fa_login' AND user_id = $3 AND consumed_at IS NULL AND expires_at > NOW() FOR UPDATE",
        )
        .bind(flow_id)
        .bind(token_hash)
        .bind(user_id)
        .fetch_optional(&mut *tx)
        .await
        .map_err(internal)?;
        if claimed.is_none() {
            return Err(AuthError::new(AuthErrorKind::TwoFactorFlowExpired));
        }
        let row = sqlx::query(
            "SELECT COALESCE(failed_attempts, 0) AS failed_attempts, locked_until IS NOT NULL AND locked_until > NOW() AS is_locked FROM two_fas WHERE id = $1 AND user_id = $2 AND is_enabled = TRUE AND deleted_at IS NULL FOR UPDATE",
        )
        .bind(factor_id)
        .bind(user_id)
        .fetch_optional(&mut *tx)
        .await
        .map_err(internal)?
        .ok_or_else(|| AuthError::new(AuthErrorKind::TwoFactorUnavailable))?;
        if row.try_get::<bool, _>("is_locked").map_err(internal)? {
            return Err(AuthError::new(AuthErrorKind::TwoFactorLocked));
        }
        let failed_attempts: i64 = row.try_get("failed_attempts").map_err(internal)?;
        let next = (failed_attempts + 1).min(super::TWO_FACTOR_MAX_FAIL_ATTEMPTS);
        sqlx::query(
            "UPDATE two_fas SET failed_attempts = $2, locked_until = CASE WHEN $2 >= $3 THEN NOW() + make_interval(secs => $4) ELSE NULL END, updated_at = NOW() WHERE id = $1",
        )
        .bind(factor_id)
        .bind(next)
        .bind(super::TWO_FACTOR_MAX_FAIL_ATTEMPTS)
        .bind(super::TWO_FACTOR_LOCKOUT_SECONDS as f64)
        .execute(&mut *tx)
        .await
        .map_err(internal)?;
        tx.commit().await.map_err(internal)?;
        Err(AuthError::new(AuthErrorKind::InvalidTwoFactorCode))
    }

    async fn session_by_sid(&self, sid: &str) -> Result<SessionRecord, AuthError> {
        let row = sqlx::query(SESSION_SELECT)
            .bind(sid)
            .fetch_optional(&self.pool)
            .await
            .map_err(internal)?
            .ok_or_else(|| AuthError::new(AuthErrorKind::SessionRevoked))?;
        session_from_row(&row)
    }

    async fn validate_identity(
        &self,
        identity: &AuthIdentity,
    ) -> Result<(SessionRecord, UserRecord), AuthError> {
        let session = self.session_by_sid(&identity.session_id).await?;
        let now = unix_now();
        if session.user_id != identity.user_id
            || session.status != ACTIVE
            || session.revoked_at != 0
            || session.expires_at <= now
            || session.version != identity.session_version
            || session.user_auth_version != identity.user_auth_version
        {
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }
        let user = self.user_by_id(identity.user_id).await?;
        if user.status != ENABLED {
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }
        if user.auth_version != identity.user_auth_version {
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }
        self.validate_valkey_floor(&session, identity).await?;
        Ok((session, user))
    }

    async fn validate_identity_for_optional(
        &self,
        identity: &AuthIdentity,
    ) -> Result<(SessionRecord, UserRecord), AuthError> {
        let session = self.session_by_sid(&identity.session_id).await?;
        let now = unix_now();
        if session.user_id != identity.user_id
            || session.status != ACTIVE
            || session.revoked_at != 0
            || session.expires_at <= now
            || session.version != identity.session_version
            || session.user_auth_version != identity.user_auth_version
        {
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }
        let user = self.user_by_id(identity.user_id).await?;
        if user.status != ENABLED {
            // Go's `ValidateLoginSession` validates the cached dashboard
            // session (including user status) before `UserAuth` runs. A
            // disabled internal session therefore surfaces as revoked;
            // opaque PATs below retain the dedicated UserDisabled error.
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }
        if user.auth_version != identity.user_auth_version {
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }
        self.validate_valkey_floor(&session, identity).await?;
        Ok((session, user))
    }

    async fn validate_valkey_floor(
        &self,
        session: &SessionRecord,
        identity: &AuthIdentity,
    ) -> Result<(), AuthError> {
        let mut connection = self.connection().await?;
        let values: Vec<Option<String>> = redis::cmd("MGET")
            .arg(format!("auth:user:fence:{}", identity.user_id))
            .arg(format!("auth:user:version:{}", identity.user_id))
            .query_async(&mut connection)
            .await
            .map_err(internal)?;
        let floor = values
            .into_iter()
            .flatten()
            .map(|value| value.parse::<i64>())
            .collect::<Result<Vec<_>, _>>()
            .map_err(internal)?
            .into_iter()
            .max()
            .unwrap_or_default();
        if floor > identity.user_auth_version {
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }
        let cached: Vec<Option<String>> = redis::cmd("HMGET")
            .arg(self.session_cache_key(&identity.session_id)?)
            .arg("Status")
            .arg("Version")
            .arg("UserAuthVersion")
            .query_async(&mut connection)
            .await
            .map_err(internal)?;
        if let [Some(status), Some(version), Some(user_version)] = cached.as_slice() {
            let cached_version = version.parse::<i64>().map_err(internal)?;
            let cached_user_version = user_version.parse::<i64>().map_err(internal)?;
            if matches!(status.as_str(), REVOKING | REVOKED)
                || cached_version > session.version
                || cached_user_version > session.user_auth_version
            {
                return Err(AuthError::new(AuthErrorKind::SessionRevoked));
            }
        }
        Ok(())
    }

    async fn create_session(
        &self,
        user: &UserRecord,
        metadata: RequestMetadata,
        login_method: &str,
    ) -> Result<(SessionRecord, SecretString), AuthError> {
        let now = unix_now();
        let mut tx = self.pool.begin().await.map_err(internal)?;
        sqlx::query("SELECT pg_advisory_xact_lock($1)")
            .bind(user.id)
            .execute(&mut *tx)
            .await
            .map_err(internal)?;
        let active: i64 = sqlx::query_scalar(
            "SELECT COUNT(*) FROM user_sessions WHERE user_id = $1 AND status = 'active' AND expires_at > $2",
        )
        .bind(user.id)
        .bind(now)
        .fetch_one(&mut *tx)
        .await
        .map_err(internal)?;
        if active >= self.config.active_session_limit {
            return Err(AuthError::new(AuthErrorKind::SessionLimit));
        }
        let issued: i64 = sqlx::query_scalar(
            "SELECT COUNT(*) FROM user_sessions WHERE user_id = $1 AND created_at > $2",
        )
        .bind(user.id)
        .bind(now - self.config.issuance_window.as_secs() as i64)
        .fetch_one(&mut *tx)
        .await
        .map_err(internal)?;
        if issued >= self.config.issuance_limit {
            return Err(AuthError::new(AuthErrorKind::SessionIssuanceLimit));
        }
        let sid = uuid::Uuid::new_v4().to_string();
        let secret = random_refresh_secret();
        let refresh_hash = self.codec.hash_refresh(&secret)?;
        let session = SessionRecord {
            sid: sid.clone(),
            user_id: user.id,
            version: 1,
            user_auth_version: user.auth_version,
            status: ACTIVE.to_owned(),
            refresh_hash,
            previous_refresh_hash: String::new(),
            previous_valid_until: 0,
            login_method: login_method.to_owned(),
            ip: truncate(metadata.ip, 64),
            user_agent: truncate(metadata.user_agent, 512),
            created_at: now,
            last_active_at: now,
            expires_at: now + LOGIN_SESSION_TTL_SECONDS,
            revoked_at: 0,
            revoked_reason: String::new(),
        };
        insert_session(&mut tx, &session).await?;
        tx.commit().await.map_err(internal)?;
        if self
            .write_session_cache(&session, ACTIVE, None)
            .await
            .is_err()
        {
            let _ = sqlx::query(
                "UPDATE user_sessions SET status = 'revoked', revoked_at = $2, revoked_reason = 'cache_publish_failed' WHERE sid = $1 AND status = 'active'",
            )
            .bind(&session.sid)
            .bind(now)
            .execute(&self.pool)
            .await;
            return Err(AuthError::new(AuthErrorKind::Internal));
        }
        Ok((session, secret))
    }

    async fn bundle(
        &self,
        session: SessionRecord,
        user: UserRecord,
        refresh_secret: SecretString,
    ) -> Result<AuthBundle, AuthError> {
        let identity = AuthIdentity {
            user_id: session.user_id,
            session_id: session.sid.clone(),
            user_auth_version: session.user_auth_version,
            session_version: session.version,
        };
        let (access_token, access_expires_at) = self.codec.issue(&identity)?;
        let refresh_token = SecretString::from(format!(
            "{}.{}",
            session.sid,
            refresh_secret.expose_secret()
        ));
        let capabilities = self.capabilities(user.id, user.role).await?;
        let user = self.dashboard_user_view(user, capabilities).await;
        Ok(AuthBundle {
            data: AuthResponseData {
                access_token,
                token_type: "Bearer",
                access_expires_at,
                session: session.view(),
                user,
            },
            refresh_token,
        })
    }

    async fn capabilities(&self, user_id: i64, role: i64) -> Result<Value, AuthError> {
        let actions = ["read", "operate", "write", "sensitive_write", "secret_view"];
        let mut channel = Map::new();
        if role >= 100 {
            for action in actions {
                channel.insert(action.to_owned(), json!(true));
            }
        } else if role >= 10 {
            let rows = sqlx::query(
                "SELECT COALESCE(v0, '') AS subject, COALESCE(v2, '') AS action, COALESCE(v3, 'allow') AS effect FROM casbin_rule WHERE ptype = 'p' AND v1 = 'channel' AND v0 IN ($1, $2)",
            )
            .bind("role:admin")
            .bind(format!("user:{user_id}"))
            .fetch_all(&self.pool)
            .await
            .map_err(internal)?;
            for action in actions {
                let user_subject = format!("user:{user_id}");
                let mut role_allow = false;
                let mut role_deny = false;
                let mut user_allow = false;
                let mut user_deny = false;
                for row in &rows {
                    let subject: String = row.try_get("subject").map_err(internal)?;
                    let policy_action: String = row.try_get("action").map_err(internal)?;
                    let effect: String = row.try_get("effect").map_err(internal)?;
                    if subject == user_subject && policy_action == action {
                        user_deny |= effect == "deny";
                        user_allow |= effect == "allow" || effect.is_empty();
                    } else if subject == "role:admin" && policy_action == action {
                        role_deny |= effect == "deny";
                        role_allow |= effect == "allow" || effect.is_empty();
                    }
                }
                let baseline = role_allow && !role_deny;
                channel.insert(
                    action.to_owned(),
                    json!(if user_deny {
                        false
                    } else if user_allow {
                        true
                    } else {
                        baseline
                    }),
                );
            }
        } else {
            for action in actions {
                channel.insert(action.to_owned(), json!(false));
            }
        }
        Ok(json!({"channel": channel}))
    }

    async fn dashboard_user_view(
        &self,
        user: UserRecord,
        capabilities: Value,
    ) -> DashboardUserView {
        let facts = self.dashboard_self_user_facts(&user).await;
        DashboardUserView::build(user.dashboard_user(capabilities), facts)
    }

    async fn dashboard_self_user_facts(&self, user: &UserRecord) -> DashboardSelfUserFacts {
        let facts = DashboardDeveloperAccessUserFacts {
            user_id: user.id,
            role: user.role,
            trust_level_override: user.trust_level_override,
            created_at: user.created_at,
            last_api_activity_at: user.last_api_activity_at,
        };
        let payment = if user.role < 10 && user.trust_level_override.is_none() {
            self.current_user_payment_snapshot(user.id)
                .await
                .unwrap_or_default()
        } else {
            PaymentSnapshot::default()
        };
        let credential_complete = sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS(SELECT 1 FROM tokens WHERE user_id = $1 AND status = 1 AND deleted_at IS NULL)",
        )
        .bind(user.id)
        .fetch_one(&self.pool)
        .await
        .unwrap_or(false);
        dashboard_self_user_facts_from_payment(
            facts,
            payment,
            DashboardDeveloperAccessPolicy::new(self.local_acceptance),
            unix_now(),
            credential_complete,
        )
    }

    async fn current_user_payment_snapshot(
        &self,
        user_id: i64,
    ) -> Result<PaymentSnapshot, AuthError> {
        let row = sqlx::query(CURRENT_USER_PAYMENT_SNAPSHOT)
            .bind(user_id)
            .fetch_one(&self.pool)
            .await
            .map_err(internal)?;
        Ok(PaymentSnapshot {
            paid_amount: row.try_get("paid_amount").map_err(internal)?,
            last_paid_complete_at: row.try_get("last_paid_complete_at").map_err(internal)?,
            paid_activation_complete: row.try_get("paid_activation_complete").map_err(internal)?,
        })
    }

    async fn write_session_cache(
        &self,
        session: &SessionRecord,
        status: &str,
        pre_rotation_metadata: Option<(&str, &str)>,
    ) -> Result<(), AuthError> {
        let key = self.session_cache_key(&session.sid)?;
        let now = unix_now();
        let ttl = self
            .config
            .session_cache_ttl
            .as_secs()
            .min((session.expires_at - now).max(1) as u64);
        let script = r#"
local current_status = redis.call('HGET', KEYS[1], 'Status')
local current_version = tonumber(redis.call('HGET', KEYS[1], 'Version') or '0')
if ARGV[5] == 'active' and (current_status == 'revoking' or current_status == 'revoked') then return 0 end
if current_version > tonumber(ARGV[3]) then return 0 end
redis.call('HSET', KEYS[1], 'SID', ARGV[1], 'UserID', ARGV[2], 'Version', ARGV[3], 'UserAuthVersion', ARGV[4], 'Status', ARGV[5], 'LoginMethod', ARGV[6], 'IP', ARGV[7], 'UserAgent', ARGV[8], 'CreatedAt', ARGV[9], 'LastActiveAt', ARGV[10], 'ExpiresAt', ARGV[11], 'RevokedAt', ARGV[12], 'RevokedReason', ARGV[13], 'CacheSchema', '1')
if ARGV[15] ~= '' then
  redis.call('HSET', KEYS[1], 'PreviousIP', ARGV[15], 'PreviousUserAgent', ARGV[16])
else
  redis.call('HDEL', KEYS[1], 'PreviousIP', 'PreviousUserAgent')
end
redis.call('EXPIRE', KEYS[1], ARGV[14])
return 1
"#;
        let mut connection = self.connection().await?;
        let written: i64 = redis::cmd("EVAL")
            .arg(script)
            .arg(1)
            .arg(key)
            .arg(&session.sid)
            .arg(session.user_id)
            .arg(session.version)
            .arg(session.user_auth_version)
            .arg(status)
            .arg(&session.login_method)
            .arg(&session.ip)
            .arg(&session.user_agent)
            .arg(session.created_at)
            .arg(session.last_active_at)
            .arg(session.expires_at)
            .arg(session.revoked_at)
            .arg(&session.revoked_reason)
            .arg(ttl)
            .arg(pre_rotation_metadata.map_or("", |metadata| metadata.0))
            .arg(pre_rotation_metadata.map_or("", |metadata| metadata.1))
            .query_async(&mut connection)
            .await
            .map_err(internal)?;
        if written == 1 {
            Ok(())
        } else {
            Err(AuthError::new(AuthErrorKind::SessionRevoked))
        }
    }

    fn session_cache_key(&self, sid: &str) -> Result<String, AuthError> {
        let mut mac = HmacSha256::new_from_slice(self.session_cache_key.expose_secret())
            .map_err(|_| AuthError::new(AuthErrorKind::Internal))?;
        mac.update(sid.as_bytes());
        let digest = mac
            .finalize()
            .into_bytes()
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect::<String>();
        Ok(format!("auth:session:{digest}"))
    }

    async fn pre_rotation_metadata(&self, sid: &str) -> Result<(String, String), AuthError> {
        let key = self.session_cache_key(sid)?;
        let mut connection = self.connection().await?;
        let values: Vec<Option<String>> = redis::cmd("HMGET")
            .arg(key)
            .arg("PreviousIP")
            .arg("PreviousUserAgent")
            .query_async(&mut connection)
            .await
            .map_err(internal)?;
        match values.as_slice() {
            [Some(ip), Some(user_agent)] => Ok((ip.clone(), user_agent.clone())),
            _ => Err(AuthError::new(AuthErrorKind::RefreshRace)),
        }
    }

    async fn revoke_session(
        &self,
        user_id: i64,
        sid: &str,
        reason: &str,
    ) -> Result<bool, AuthError> {
        let mut session = match self.session_by_sid(sid).await {
            Ok(session) => session,
            Err(error) if error.kind == AuthErrorKind::SessionRevoked => return Ok(false),
            Err(error) => return Err(error),
        };
        let now = unix_now();
        if session.user_id != user_id
            || session.status != ACTIVE
            || session.revoked_at != 0
            || session.expires_at <= now
        {
            return Ok(false);
        }
        session.revoked_at = now;
        session.revoked_reason = reason.to_owned();
        self.write_session_cache(&session, REVOKING, None).await?;
        let result = sqlx::query(
            "UPDATE user_sessions SET status = 'revoked', revoked_at = $3, revoked_reason = $4 WHERE sid = $1 AND user_id = $2 AND status = 'active' AND revoked_at = 0 AND expires_at > $3",
        )
        .bind(sid)
        .bind(user_id)
        .bind(now)
        .bind(reason)
        .execute(&self.pool)
        .await
        .map_err(internal)?;
        if result.rows_affected() == 1 {
            session.status = REVOKED.to_owned();
            self.write_session_cache(&session, REVOKED, None).await?;
            Ok(true)
        } else {
            Ok(false)
        }
    }

    async fn revoke_by_refresh(
        &self,
        raw: &SecretString,
        expected_sid: Option<&str>,
    ) -> Result<Option<String>, AuthError> {
        let Some((sid, secret)) = split_refresh_token(raw) else {
            return Ok(None);
        };
        if expected_sid.is_some_and(|expected| expected != sid) {
            return Err(AuthError::new(AuthErrorKind::SessionMismatch));
        }
        let session = match self.session_by_sid(&sid).await {
            Ok(session) => session,
            Err(error) if error.kind == AuthErrorKind::SessionRevoked => return Ok(None),
            Err(error) => return Err(error),
        };
        let digest = self.codec.hash_refresh(&secret)?;
        let valid_current = ct_equal(&session.refresh_hash, &digest);
        let valid_previous = !session.previous_refresh_hash.is_empty()
            && unix_now() <= session.previous_valid_until
            && ct_equal(&session.previous_refresh_hash, &digest);
        if !valid_current && !valid_previous {
            return Ok(None);
        }
        self.revoke_session(session.user_id, &sid, "logout")
            .await
            .map(|revoked| revoked.then_some(sid))
    }
}

#[async_trait]
impl DashboardAuth for PgValkeyDashboardAuth {
    async fn check_critical_rate_limit(
        &self,
        client_ip: &str,
    ) -> Result<CriticalRateLimitOutcome, AuthError> {
        if !self.config.critical_rate_limit_enabled {
            return Ok(CriticalRateLimitOutcome::Allowed);
        }
        let mut connection = self.connection().await?;
        let key = format!("rateLimit:v2:ip:CT:{client_ip}");
        let script = redis::Script::new(
            r#"
local count = redis.call('INCR', KEYS[1])
if count == 1 then
    redis.call('EXPIRE', KEYS[1], ARGV[2])
end
local ttl = redis.call('TTL', KEYS[1])
if count <= tonumber(ARGV[1]) then
    return {1, ttl}
end
return {0, ttl}
"#,
        );
        let result: (i64, i64) = tokio::time::timeout(
            self.config.dependency_timeout,
            script
                .key(key)
                .arg(self.config.critical_rate_limit)
                .arg(self.config.critical_rate_limit_window.as_secs())
                .invoke_async(&mut connection),
        )
        .await
        .map_err(|_| AuthError::new(AuthErrorKind::Internal))?
        .map_err(|_| AuthError::new(AuthErrorKind::Internal))?;
        if result.0 == 1 {
            Ok(CriticalRateLimitOutcome::Allowed)
        } else {
            Ok(CriticalRateLimitOutcome::Rejected {
                retry_after_seconds: result.1.max(0) as u64,
            })
        }
    }

    async fn login(
        &self,
        request: LoginRequest,
        metadata: RequestMetadata,
    ) -> Result<LoginOutcome, AuthError> {
        let user = self.user_by_username(&request.username).await?;
        let password = request.password;
        let hash = user.password.clone();
        let valid = tokio::task::spawn_blocking(move || verify(password.expose_secret(), &hash))
            .await
            .map_err(internal)?
            .map_err(|_| AuthError::new(AuthErrorKind::InvalidCredentials))?;
        if !valid {
            return Err(AuthError::new(AuthErrorKind::InvalidCredentials));
        }
        if user.status != ENABLED || user.auth_version <= 0 {
            return Err(AuthError::new(AuthErrorKind::InvalidCredentials));
        }
        let two_factor: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM two_fas WHERE user_id = $1 AND is_enabled = TRUE AND deleted_at IS NULL)",
        )
        .bind(user.id)
        .fetch_one(&self.pool)
        .await
        .map_err(internal)?;
        if two_factor {
            return self
                .create_two_factor_flow(&user)
                .await
                .map(LoginOutcome::TwoFactorRequired);
        }
        let (session, secret) = self.create_session(&user, metadata, "password").await?;
        let _ = sqlx::query("UPDATE users SET last_login_at = $2 WHERE id = $1")
            .bind(user.id)
            .bind(unix_now())
            .execute(&self.pool)
            .await;
        self.bundle(session, user, secret)
            .await
            .map(Box::new)
            .map(LoginOutcome::Authenticated)
    }

    async fn login_2fa(
        &self,
        request: TwoFactorLoginRequest,
        metadata: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        let token_hash = self.codec.hash_auth_flow(&request.flow_token)?;
        let flow = sqlx::query(
            "SELECT id, user_id, COALESCE(payload, '') AS payload FROM auth_flows WHERE token_hash = $1 AND purpose = '2fa_login' AND consumed_at IS NULL AND expires_at > NOW() LIMIT 1",
        )
        .bind(&token_hash)
        .fetch_optional(&self.pool)
        .await
        .map_err(internal)?
        .ok_or_else(|| AuthError::new(AuthErrorKind::TwoFactorFlowExpired))?;
        let flow_id: i64 = flow.try_get("id").map_err(internal)?;
        let user_id: i64 = flow.try_get("user_id").map_err(internal)?;
        let payload: TwoFactorFlowPayload =
            serde_json::from_str(&flow.try_get::<String, _>("payload").map_err(internal)?)
                .map_err(|_| AuthError::new(AuthErrorKind::TwoFactorFlowExpired))?;
        let user = self.user_by_id(user_id).await?;
        if user.status != ENABLED
            || user.auth_version <= 0
            || user.auth_version != payload.auth_version
        {
            return Err(AuthError::new(AuthErrorKind::TwoFactorFlowExpired));
        }
        let factor = sqlx::query(
            "SELECT id, secret, locked_until IS NOT NULL AND locked_until > NOW() AS is_locked FROM two_fas WHERE user_id = $1 AND is_enabled = TRUE AND deleted_at IS NULL LIMIT 1",
        )
        .bind(user.id)
        .fetch_optional(&self.pool)
        .await
        .map_err(internal)?
        .ok_or_else(|| AuthError::new(AuthErrorKind::TwoFactorUnavailable))?;
        let factor_id: i64 = factor.try_get("id").map_err(internal)?;
        let secret: String = factor.try_get("secret").map_err(internal)?;
        if factor.try_get::<bool, _>("is_locked").map_err(internal)? {
            return Err(AuthError::new(AuthErrorKind::TwoFactorLocked));
        }
        let code = request.code.expose_secret().trim().to_owned();
        let valid_totp = validate_totp(&secret, &code);
        let backup_rows = if valid_totp {
            Vec::new()
        } else {
            sqlx::query(
                "SELECT id, code_hash FROM two_fa_backup_codes WHERE user_id = $1 AND is_used = FALSE AND deleted_at IS NULL",
            )
            .bind(user.id)
            .fetch_all(&self.pool)
            .await
            .map_err(internal)?
        };
        let backup_candidates = backup_rows
            .iter()
            .map(|row| {
                Ok((
                    row.try_get::<i64, _>("id").map_err(internal)?,
                    row.try_get::<String, _>("code_hash").map_err(internal)?,
                ))
            })
            .collect::<Result<Vec<_>, AuthError>>()?;
        let backup_code = code.clone();
        let backup_id = if valid_totp {
            None
        } else {
            tokio::task::spawn_blocking(move || {
                backup_candidates.into_iter().find_map(|(id, hash)| {
                    verify(&backup_code, &hash)
                        .ok()
                        .filter(|valid| *valid)
                        .map(|_| id)
                })
            })
            .await
            .map_err(internal)?
        };
        if !valid_totp && backup_id.is_none() {
            self.record_failed_two_factor_attempt(flow_id, &token_hash, user.id, factor_id)
                .await?;
            return Err(AuthError::new(AuthErrorKind::Internal));
        }
        let mut tx = self.pool.begin().await.map_err(internal)?;
        let claimed = sqlx::query_scalar::<_, i64>(
            "SELECT id FROM auth_flows WHERE id = $1 AND token_hash = $2 AND purpose = '2fa_login' AND user_id = $3 AND consumed_at IS NULL AND expires_at > NOW() FOR UPDATE",
        )
        .bind(flow_id)
        .bind(&token_hash)
        .bind(user.id)
        .fetch_optional(&mut *tx)
        .await
        .map_err(internal)?;
        if claimed.is_none() {
            return Err(AuthError::new(AuthErrorKind::TwoFactorFlowExpired));
        }
        let current_auth_version: i64 =
            sqlx::query_scalar("SELECT auth_version FROM users WHERE id = $1 FOR UPDATE")
                .bind(user.id)
                .fetch_one(&mut *tx)
                .await
                .map_err(internal)?;
        if current_auth_version != payload.auth_version {
            return Err(AuthError::new(AuthErrorKind::TwoFactorFlowExpired));
        }
        let factor_locked: bool = sqlx::query_scalar(
            "SELECT locked_until IS NOT NULL AND locked_until > NOW() FROM two_fas WHERE id = $1 AND user_id = $2 AND is_enabled = TRUE AND deleted_at IS NULL FOR UPDATE",
        )
        .bind(factor_id)
        .bind(user.id)
        .fetch_optional(&mut *tx)
        .await
        .map_err(internal)?
        .ok_or_else(|| AuthError::new(AuthErrorKind::TwoFactorUnavailable))?;
        if factor_locked {
            return Err(AuthError::new(AuthErrorKind::TwoFactorLocked));
        }
        if let Some(backup_id) = backup_id {
            let used = sqlx::query(
                "UPDATE two_fa_backup_codes SET is_used = TRUE, used_at = NOW() WHERE id = $1 AND user_id = $2 AND is_used = FALSE",
            )
            .bind(backup_id)
            .bind(user.id)
            .execute(&mut *tx)
            .await
            .map_err(internal)?;
            if used.rows_affected() != 1 {
                return Err(AuthError::new(AuthErrorKind::InvalidTwoFactorCode));
            }
        }
        let consumed = sqlx::query(
            "UPDATE auth_flows SET consumed_at = NOW() WHERE id = $1 AND consumed_at IS NULL AND expires_at > NOW()",
        )
        .bind(flow_id)
        .execute(&mut *tx)
        .await
        .map_err(internal)?;
        if consumed.rows_affected() != 1 {
            return Err(AuthError::new(AuthErrorKind::TwoFactorFlowExpired));
        }
        sqlx::query(
            "UPDATE two_fas SET failed_attempts = 0, locked_until = NULL, last_used_at = NOW() WHERE id = $1",
        )
        .bind(factor_id)
        .execute(&mut *tx)
        .await
        .map_err(internal)?;
        tx.commit().await.map_err(internal)?;
        let (session, secret) = self.create_session(&user, metadata, "2fa").await?;
        self.bundle(session, user, secret).await
    }

    async fn refresh(
        &self,
        refresh_token: SecretString,
        expected_sid: Option<String>,
        metadata: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        let (sid, secret) = split_refresh_token(&refresh_token)
            .ok_or_else(|| AuthError::new(AuthErrorKind::Unauthorized))?;
        if expected_sid
            .as_deref()
            .is_some_and(|expected| expected != sid)
        {
            return Err(AuthError::new(AuthErrorKind::SessionMismatch));
        }
        let mut session = self.session_by_sid(&sid).await?;
        let now = unix_now();
        if session.status != ACTIVE || session.revoked_at != 0 || session.expires_at <= now {
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }
        let user = self.user_by_id(session.user_id).await?;
        if user.status != ENABLED || user.auth_version != session.user_auth_version {
            let _ = self
                .revoke_session(session.user_id, &sid, "user_security_changed")
                .await;
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }
        let identity = AuthIdentity {
            user_id: session.user_id,
            session_id: sid.clone(),
            user_auth_version: session.user_auth_version,
            session_version: session.version,
        };
        self.validate_valkey_floor(&session, &identity).await?;
        let presented = self.codec.hash_refresh(&secret)?;
        let next_secret = self.codec.derive_next_refresh(&sid, &secret)?;
        let next_hash = self.codec.hash_refresh(&next_secret)?;
        let response_ip = truncate(metadata.ip, 64);
        let response_user_agent = truncate(metadata.user_agent, 512);
        // Hold onto the snapshot observed before attempting the CAS.  The
        // legacy race response is built from that pre-rotation snapshot. The
        // database and Valkey cache preserve login metadata for every refresh;
        // only the winner's HTTP response reflects its request metadata.
        let pre_rotation_session = session.clone();
        let mut cas_loser_response = None;
        let mut winner_response = None;
        let mut won_rotation = false;
        if ct_equal(&session.refresh_hash, &presented) {
            let result = sqlx::query(
                "UPDATE user_sessions SET previous_refresh_hash = refresh_hash, previous_valid_until = $4, refresh_hash = $3, last_active_at = $5 WHERE sid = $1 AND user_id = $2 AND status = 'active' AND revoked_at = 0 AND expires_at > $5 AND refresh_hash = $6",
            )
            .bind(&sid)
            .bind(session.user_id)
            .bind(&next_hash)
            .bind(now + REFRESH_REPLAY_WINDOW_SECONDS)
            .bind(now)
            .bind(&session.refresh_hash)
            .execute(&self.pool)
            .await
            .map_err(internal)?;
            if result.rows_affected() != 1 {
                // A simultaneous request using this same cookie can lose the
                // compare-and-swap after the winner has already published the
                // deterministic replacement. Re-read the authoritative row so
                // that only the one permitted previous-hash grace retry is
                // recovered; a later rotation remains a real race.
                session = self.session_by_sid(&sid).await?;
                let recovered = session.status == ACTIVE
                    && session.revoked_at == 0
                    && session.expires_at > now
                    && !session.previous_refresh_hash.is_empty()
                    && now <= session.previous_valid_until
                    && ct_equal(&session.previous_refresh_hash, &presented)
                    && ct_equal(&session.refresh_hash, &next_hash);
                if !recovered {
                    return Err(AuthError::new(AuthErrorKind::RefreshRace));
                }
                cas_loser_response = Some(pre_rotation_session.clone());
            } else {
                won_rotation = true;
                session.previous_refresh_hash = session.refresh_hash;
                session.previous_valid_until = now + REFRESH_REPLAY_WINDOW_SECONDS;
                session.refresh_hash = next_hash;
                session.last_active_at = now;
                let mut response = session.clone();
                response.ip = response_ip.clone();
                response.user_agent = response_user_agent.clone();
                winner_response = Some(response);
            }
        } else if !session.previous_refresh_hash.is_empty()
            && ct_equal(&session.previous_refresh_hash, &presented)
        {
            if now > session.previous_valid_until {
                self.revoke_session(session.user_id, &sid, "refresh_reuse")
                    .await?;
                return Err(AuthError::new(AuthErrorKind::SessionRevoked));
            }
            if !ct_equal(&session.refresh_hash, &next_hash) {
                return Err(AuthError::new(AuthErrorKind::RefreshRace));
            }
            let (ip, user_agent) = self.pre_rotation_metadata(&sid).await?;
            let mut snapshot = session.clone();
            snapshot.ip = ip;
            snapshot.user_agent = user_agent;
            cas_loser_response = Some(snapshot);
        } else {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        if won_rotation {
            self.write_session_cache(
                &session,
                ACTIVE,
                Some((&pre_rotation_session.ip, &pre_rotation_session.user_agent)),
            )
            .await?;
        }
        self.bundle(
            cas_loser_response.or(winner_response).unwrap_or(session),
            user,
            next_secret,
        )
        .await
    }

    async fn self_user(&self, access_token: SecretString) -> Result<DashboardUser, AuthError> {
        let user = if self.codec.is_dashboard_token_candidate(&access_token) {
            let identity = self.codec.parse(&access_token)?;
            let (_, user) = self.validate_identity(&identity).await?;
            user
        } else {
            let raw = access_token.expose_secret().trim();
            if raw.is_empty() {
                return Err(AuthError::new(AuthErrorKind::Unauthorized));
            }
            let user = self.user_by_personal_access_token(raw).await?;
            if user.status != ENABLED {
                return Err(AuthError::new(AuthErrorKind::UserDisabled));
            }
            user
        };
        let capabilities = self.capabilities(user.id, user.role).await?;
        Ok(user.dashboard_user(capabilities))
    }

    async fn current_session(
        &self,
        access_token: SecretString,
    ) -> Result<DashboardSessionContext, AuthError> {
        let identity = self.codec.parse(&access_token)?;
        let (session, user) = self.validate_identity(&identity).await?;
        let capabilities = self.capabilities(user.id, user.role).await?;
        Ok(DashboardSessionContext {
            user_auth_version: user.auth_version,
            user: user.dashboard_user(capabilities),
            session_id: session.sid,
            session_version: session.version,
            client_ip: session.ip,
            user_agent: session.user_agent,
        })
    }

    async fn issue_security_proof(
        &self,
        user_id: i64,
        session_id: &str,
        method: &str,
        scopes: &[String],
    ) -> Result<SecurityProof, AuthError> {
        if user_id <= 0
            || session_id.trim().is_empty()
            || method.trim().is_empty()
            || scopes.is_empty()
        {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        let session = self.session_by_sid(session_id).await?;
        let now = unix_now();
        if session.user_id != user_id
            || session.status != ACTIVE
            || session.revoked_at != 0
            || session.expires_at <= now
        {
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }
        let user = self.user_by_id(user_id).await?;
        if user.status != ENABLED || user.auth_version != session.user_auth_version {
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }
        let identity = AuthIdentity {
            user_id,
            session_id: session.sid.clone(),
            user_auth_version: session.user_auth_version,
            session_version: session.version,
        };
        self.validate_valkey_floor(&session, &identity).await?;
        let (token, expires_at) = self.codec.issue_security_proof(&identity, method, scopes)?;
        Ok(SecurityProof { token, expires_at })
    }

    async fn verify_security_proof(
        &self,
        raw: SecretString,
        user_id: i64,
        session_id: &str,
        required_scope: &str,
        allowed_methods: &[String],
    ) -> Result<String, AuthError> {
        if user_id <= 0 || session_id.trim().is_empty() {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        let session = self.session_by_sid(session_id).await?;
        let now = unix_now();
        if session.user_id != user_id
            || session.status != ACTIVE
            || session.revoked_at != 0
            || session.expires_at <= now
        {
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }
        let user = self.user_by_id(user_id).await?;
        if user.status != ENABLED || user.auth_version != session.user_auth_version {
            return Err(AuthError::new(AuthErrorKind::SessionRevoked));
        }
        let identity = AuthIdentity {
            user_id,
            session_id: session.sid.clone(),
            user_auth_version: session.user_auth_version,
            session_version: session.version,
        };
        self.validate_valkey_floor(&session, &identity).await?;
        self.codec
            .verify_security_proof(&raw, &identity, required_scope, allowed_methods)
    }

    async fn create_assistant_l1_confirmation(
        &self,
        user_id: i64,
        session_id: &str,
        payload: &str,
        ttl: Duration,
    ) -> Result<String, AuthError> {
        if user_id <= 0 || session_id.trim().is_empty() || ttl.is_zero() {
            return Err(AuthError::new(AuthErrorKind::Internal));
        }
        let mut bytes = [0_u8; 32];
        rand::rng().fill_bytes(&mut bytes);
        let token = SecretString::from(URL_SAFE_NO_PAD.encode(bytes));
        let token_hash = self.codec.hash_auth_flow(&token)?;
        sqlx::query(
            "INSERT INTO auth_flows (token_hash, purpose, user_id, session_id, payload, created_at, expires_at) VALUES ($1, 'assistant_l1_recommendation', $2, $3, $4, NOW(), NOW() + make_interval(secs => $5))",
        )
        .bind(token_hash)
        .bind(user_id)
        .bind(session_id.trim())
        .bind(payload)
        .bind(ttl.as_secs() as f64)
        .execute(&self.pool)
        .await
        .map_err(internal)?;
        Ok(token.expose_secret().to_owned())
    }

    async fn consume_assistant_l1_confirmation(
        &self,
        user_id: i64,
        session_id: &str,
        token: SecretString,
    ) -> Result<String, crate::auth::AssistantL1ConfirmationError> {
        use crate::auth::AssistantL1ConfirmationError;

        if user_id <= 0 || session_id.trim().is_empty() || token.expose_secret().trim().is_empty() {
            return Err(AssistantL1ConfirmationError::Invalid);
        }
        let token_hash = self
            .codec
            .hash_auth_flow(&token)
            .map_err(|_| AssistantL1ConfirmationError::Internal)?;
        sqlx::query_scalar::<_, String>(
            "UPDATE auth_flows SET consumed_at = NOW() WHERE token_hash = $1 AND purpose = 'assistant_l1_recommendation' AND user_id = $2 AND session_id = $3 AND consumed_at IS NULL AND expires_at > NOW() RETURNING payload",
        )
        .bind(token_hash)
        .bind(user_id)
        .bind(session_id.trim())
        .fetch_optional(&self.pool)
        .await
        .map_err(|_| AssistantL1ConfirmationError::Internal)?
        .ok_or(AssistantL1ConfirmationError::Invalid)
    }

    async fn self_user_for_optional(
        &self,
        access_token: SecretString,
    ) -> Result<DashboardUser, AuthError> {
        let user = if self.codec.is_dashboard_token_candidate(&access_token) {
            let identity = self.codec.parse(&access_token)?;
            let (_, user) = self.validate_identity_for_optional(&identity).await?;
            user
        } else {
            let raw = access_token.expose_secret().trim();
            if raw.is_empty() {
                return Err(AuthError::new(AuthErrorKind::Unauthorized));
            }
            let user = self.user_by_personal_access_token(raw).await?;
            if user.status != ENABLED {
                return Err(AuthError::new(AuthErrorKind::UserDisabled));
            }
            user
        };
        let capabilities = self.capabilities(user.id, user.role).await?;
        Ok(user.dashboard_user(capabilities))
    }

    async fn self_user_view_for_optional(
        &self,
        access_token: SecretString,
    ) -> Result<DashboardUserView, AuthError> {
        let user = if self.codec.is_dashboard_token_candidate(&access_token) {
            let identity = self.codec.parse(&access_token)?;
            // The frozen Go `UserAuth` middleware validates a dashboard
            // session before the handler-level role/status policy. A disabled
            // session therefore surfaces as AUTH_SESSION_REVOKED, while an
            // opaque personal token reaches the explicit user-disabled
            // branch below. Keep the optional self projection faithful to
            // that ordering.
            let (_, user) = self.validate_identity(&identity).await?;
            user
        } else {
            let raw = access_token.expose_secret().trim();
            if raw.is_empty() {
                return Err(AuthError::new(AuthErrorKind::Unauthorized));
            }
            let user = self.user_by_personal_access_token(raw).await?;
            if user.status != ENABLED {
                return Err(AuthError::new(AuthErrorKind::UserDisabled));
            }
            user
        };
        let capabilities = self.capabilities(user.id, user.role).await?;
        Ok(self.dashboard_user_view(user, capabilities).await)
    }

    async fn logout(&self, request: LogoutRequest) -> Result<LogoutResult, AuthError> {
        let cookie_sid = request
            .refresh_token
            .as_ref()
            .and_then(split_refresh_token)
            .map(|(sid, _)| sid);
        if request
            .expected_sid
            .as_deref()
            .zip(cookie_sid.as_deref())
            .is_some_and(|(expected, cookie)| expected != cookie)
        {
            return Err(AuthError::new(AuthErrorKind::SessionMismatch));
        }
        if let Some(access_token) = request.access_token.as_ref()
            && let Ok(identity) = self.codec.parse(access_token)
        {
            if request
                .expected_sid
                .as_deref()
                .is_some_and(|expected| expected != identity.session_id)
            {
                return Err(AuthError::new(AuthErrorKind::SessionMismatch));
            }
            self.revoke_session(identity.user_id, &identity.session_id, "logout")
                .await?;
            let cookie_cleared = if cookie_sid.as_deref() == Some(identity.session_id.as_str()) {
                if let Some(refresh) = request.refresh_token.as_ref() {
                    self.revoke_by_refresh(refresh, Some(&identity.session_id))
                        .await?;
                }
                true
            } else {
                false
            };
            return Ok(LogoutResult {
                revoked_sid: Some(identity.session_id),
                cookie_cleared: Some(cookie_cleared),
            });
        }
        if let Some(refresh_token) = request.refresh_token.as_ref() {
            self.revoke_by_refresh(refresh_token, request.expected_sid.as_deref())
                .await?;
        }
        Ok(LogoutResult {
            revoked_sid: None,
            cookie_cleared: None,
        })
    }

    async fn generate_personal_access_token(
        &self,
        access_token: SecretString,
    ) -> Result<String, AuthError> {
        let identity = self.codec.parse(&access_token)?;
        let (_, user) = self.validate_identity(&identity).await?;
        for _ in 0..5 {
            let mut bytes = [0_u8; 24];
            rand::rng().fill_bytes(&mut bytes);
            let token = base64::engine::general_purpose::STANDARD.encode(bytes);
            let result = sqlx::query(
                "UPDATE users SET access_token = $2 WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM users WHERE access_token = $2)",
            )
            .bind(user.id)
            .bind(&token)
            .execute(&self.pool)
            .await
            .map_err(legacy_personal_access_token_write_error)?;
            if result.rows_affected() == 1 {
                return Ok(token);
            }
        }
        Err(AuthError::new(AuthErrorKind::Internal))
    }
}

const USER_SELECT_BY_USERNAME: &str = concat!(
    "SELECT ",
    r#"id, COALESCE(username, '') AS username, password, COALESCE(display_name, '') AS display_name,
COALESCE(role, 1) AS role, COALESCE(status, 1) AS status, COALESCE(email, '') AS email, COALESCE(github_id, '') AS github_id,
COALESCE(discord_id, '') AS discord_id, COALESCE(oidc_id, '') AS oidc_id,
COALESCE(wechat_id, '') AS wechat_id, COALESCE(telegram_id, '') AS telegram_id,
COALESCE("group", 'default') AS user_group, COALESCE(quota, 0) AS quota, COALESCE(used_quota, 0) AS used_quota, COALESCE(request_count, 0) AS request_count,
COALESCE(aff_code, '') AS aff_code, COALESCE(aff_count, 0) AS aff_count, COALESCE(aff_quota, 0) AS aff_quota, COALESCE(aff_history, 0) AS aff_history_quota,
COALESCE(inviter_id, 0) AS inviter_id, COALESCE(linux_do_id, '') AS linux_do_id, COALESCE(setting, '') AS setting,
COALESCE(stripe_customer, '') AS stripe_customer, auth_version,
CASE WHEN COALESCE(to_jsonb(users)->>'created_at', '') ~ '^-?[0-9]+$'
  THEN (to_jsonb(users)->>'created_at')::BIGINT ELSE 0 END AS created_at,
CASE WHEN COALESCE(to_jsonb(users)->>'last_api_activity_at', '') ~ '^-?[0-9]+$'
  THEN (to_jsonb(users)->>'last_api_activity_at')::BIGINT ELSE 0 END AS last_api_activity_at,
CASE WHEN COALESCE(to_jsonb(users)->>'trust_level_override', '') ~ '^-?[0-9]+$'
  THEN (to_jsonb(users)->>'trust_level_override')::BIGINT ELSE NULL END AS trust_level_override
FROM users WHERE (username = $1 OR email = $1) AND deleted_at IS NULL LIMIT 1"#
);

const USER_SELECT_BY_ID: &str = concat!(
    "SELECT ",
    r#"id, COALESCE(username, '') AS username, password, COALESCE(display_name, '') AS display_name,
COALESCE(role, 1) AS role, COALESCE(status, 1) AS status, COALESCE(email, '') AS email, COALESCE(github_id, '') AS github_id,
COALESCE(discord_id, '') AS discord_id, COALESCE(oidc_id, '') AS oidc_id,
COALESCE(wechat_id, '') AS wechat_id, COALESCE(telegram_id, '') AS telegram_id,
COALESCE("group", 'default') AS user_group, COALESCE(quota, 0) AS quota, COALESCE(used_quota, 0) AS used_quota, COALESCE(request_count, 0) AS request_count,
COALESCE(aff_code, '') AS aff_code, COALESCE(aff_count, 0) AS aff_count, COALESCE(aff_quota, 0) AS aff_quota, COALESCE(aff_history, 0) AS aff_history_quota,
COALESCE(inviter_id, 0) AS inviter_id, COALESCE(linux_do_id, '') AS linux_do_id, COALESCE(setting, '') AS setting,
COALESCE(stripe_customer, '') AS stripe_customer, auth_version,
CASE WHEN COALESCE(to_jsonb(users)->>'created_at', '') ~ '^-?[0-9]+$'
  THEN (to_jsonb(users)->>'created_at')::BIGINT ELSE 0 END AS created_at,
CASE WHEN COALESCE(to_jsonb(users)->>'last_api_activity_at', '') ~ '^-?[0-9]+$'
  THEN (to_jsonb(users)->>'last_api_activity_at')::BIGINT ELSE 0 END AS last_api_activity_at,
CASE WHEN COALESCE(to_jsonb(users)->>'trust_level_override', '') ~ '^-?[0-9]+$'
  THEN (to_jsonb(users)->>'trust_level_override')::BIGINT ELSE NULL END AS trust_level_override
FROM users WHERE id = $1 AND deleted_at IS NULL LIMIT 1"#
);

/// Same projection as `USER_SELECT_BY_ID`, with the legacy `access_token`
/// lookup used by dashboard personal-access-token authentication.  It is
/// intentionally separate from the API-key `tokens` table.
const USER_SELECT_BY_PERSONAL_ACCESS_TOKEN: &str = concat!(
    "SELECT ",
    r#"id, COALESCE(username, '') AS username, password, COALESCE(display_name, '') AS display_name,
COALESCE(role, 1) AS role, COALESCE(status, 1) AS status, COALESCE(email, '') AS email, COALESCE(github_id, '') AS github_id,
COALESCE(discord_id, '') AS discord_id, COALESCE(oidc_id, '') AS oidc_id,
COALESCE(wechat_id, '') AS wechat_id, COALESCE(telegram_id, '') AS telegram_id,
COALESCE("group", 'default') AS user_group, COALESCE(quota, 0) AS quota, COALESCE(used_quota, 0) AS used_quota, COALESCE(request_count, 0) AS request_count,
COALESCE(aff_code, '') AS aff_code, COALESCE(aff_count, 0) AS aff_count, COALESCE(aff_quota, 0) AS aff_quota, COALESCE(aff_history, 0) AS aff_history_quota,
COALESCE(inviter_id, 0) AS inviter_id, COALESCE(linux_do_id, '') AS linux_do_id, COALESCE(setting, '') AS setting,
COALESCE(stripe_customer, '') AS stripe_customer, auth_version,
CASE WHEN COALESCE(to_jsonb(users)->>'created_at', '') ~ '^-?[0-9]+$'
  THEN (to_jsonb(users)->>'created_at')::BIGINT ELSE 0 END AS created_at,
CASE WHEN COALESCE(to_jsonb(users)->>'last_api_activity_at', '') ~ '^-?[0-9]+$'
  THEN (to_jsonb(users)->>'last_api_activity_at')::BIGINT ELSE 0 END AS last_api_activity_at,
CASE WHEN COALESCE(to_jsonb(users)->>'trust_level_override', '') ~ '^-?[0-9]+$'
  THEN (to_jsonb(users)->>'trust_level_override')::BIGINT ELSE NULL END AS trust_level_override
FROM users WHERE access_token = $1 AND deleted_at IS NULL LIMIT 1"#
);

const CURRENT_USER_PAYMENT_SNAPSHOT: &str = r#"
WITH parsed AS (
    SELECT
        COALESCE(row_data->>'status', '') AS status,
        COALESCE(row_data->>'payment_method', '') AS payment_method,
        COALESCE(row_data->>'payment_provider', '') AS payment_provider,
        CASE WHEN COALESCE(row_data->>'money', '') ~ '^-?[0-9]+([.][0-9]+)?$'
            THEN (row_data->>'money')::DOUBLE PRECISION ELSE 0 END AS money,
        CASE WHEN COALESCE(row_data->>'amount', '') ~ '^-?[0-9]+$'
            THEN (row_data->>'amount')::BIGINT ELSE 0 END AS amount,
        CASE WHEN COALESCE(row_data->>'credited_quota', '') ~ '^-?[0-9]+$'
            THEN (row_data->>'credited_quota')::BIGINT ELSE 0 END AS credited_quota,
        CASE WHEN COALESCE(row_data->>'settled_amount_micros', '') ~ '^-?[0-9]+$'
            THEN (row_data->>'settled_amount_micros')::BIGINT ELSE 0 END AS settled_amount_micros,
        CASE WHEN COALESCE(row_data->>'create_time', '') ~ '^-?[0-9]+$'
            THEN (row_data->>'create_time')::BIGINT ELSE 0 END AS create_time,
        CASE WHEN COALESCE(row_data->>'complete_time', '') ~ '^-?[0-9]+$'
            THEN (row_data->>'complete_time')::BIGINT ELSE 0 END AS complete_time
    FROM (
        SELECT to_jsonb(top_ups) AS row_data
        FROM top_ups
        WHERE user_id = $1
    ) rows
), qualified AS (
    SELECT *,
        status = 'success'
        AND payment_method <> 'balance'
        AND payment_provider <> 'balance'
        AND (settled_amount_micros > 0 OR (settled_amount_micros = 0 AND money > 0))
        AND (credited_quota > 0 OR amount > 0)
        AND (
            payment_provider IN ('epay', 'stripe', 'creem', 'waffo', 'waffo_pancake')
            OR (
                payment_provider = ''
                AND payment_method IN ('stripe', 'creem', 'waffo', 'waffo_pancake', 'alipay', 'wxpay')
            )
        ) AS qualifies
    FROM parsed
)
SELECT
    (
        COALESCE(SUM(CASE WHEN qualifies AND settled_amount_micros > 0
            THEN settled_amount_micros ELSE 0 END), 0)::DOUBLE PRECISION
        + ROUND(COALESCE(SUM(CASE WHEN qualifies AND settled_amount_micros = 0
            THEN money ELSE 0 END), 0) * 1000000.0)
    ) / 1000000.0 AS paid_amount,
    COALESCE(MAX(CASE WHEN qualifies THEN
        CASE WHEN complete_time > 0 THEN complete_time ELSE create_time END
        ELSE 0 END), 0)::BIGINT AS last_paid_complete_at,
    COALESCE(BOOL_OR(qualifies), FALSE) AS paid_activation_complete
FROM qualified
"#;

const SESSION_SELECT: &str = r#"
SELECT sid, user_id, version, user_auth_version, status,
TRIM(TRAILING FROM refresh_hash) AS refresh_hash,
COALESCE(TRIM(TRAILING FROM previous_refresh_hash), '') AS previous_refresh_hash,
previous_valid_until, login_method, COALESCE(ip, '') AS ip,
COALESCE(user_agent, '') AS user_agent, created_at, last_active_at, expires_at,
revoked_at, COALESCE(revoked_reason, '') AS revoked_reason
FROM user_sessions WHERE sid = $1 LIMIT 1
"#;

const SESSION_SELECT_FOR_UPDATE: &str = r#"
SELECT sid, user_id, version, user_auth_version, status,
       refresh_hash, COALESCE(previous_refresh_hash, '') AS previous_refresh_hash,
       previous_valid_until, login_method, ip, user_agent, created_at,
       last_active_at, expires_at, revoked_at, COALESCE(revoked_reason, '') AS revoked_reason
FROM user_sessions WHERE sid = $1 FOR UPDATE
"#;

#[derive(Clone)]
struct UserRecord {
    id: i64,
    username: String,
    password: String,
    display_name: String,
    role: i64,
    status: i64,
    email: String,
    github_id: String,
    discord_id: String,
    oidc_id: String,
    wechat_id: String,
    telegram_id: String,
    group: String,
    quota: i64,
    used_quota: i64,
    request_count: i64,
    aff_code: String,
    aff_count: i64,
    aff_quota: i64,
    aff_history_quota: i64,
    inviter_id: i64,
    linux_do_id: String,
    setting: String,
    stripe_customer: String,
    auth_version: i64,
    created_at: i64,
    last_api_activity_at: i64,
    trust_level_override: Option<i64>,
}

#[derive(Clone, Copy, Debug, Default)]
struct PaymentSnapshot {
    paid_amount: f64,
    last_paid_complete_at: i64,
    paid_activation_complete: bool,
}

fn dashboard_self_user_facts_from_payment(
    user: DashboardDeveloperAccessUserFacts,
    payment: PaymentSnapshot,
    policy: DashboardDeveloperAccessPolicy,
    now: i64,
    credential_complete: bool,
) -> DashboardSelfUserFacts {
    DashboardSelfUserFacts {
        trust_level_override: user.trust_level_override,
        paid_amount: payment.paid_amount,
        paid_activation_complete: payment.paid_activation_complete,
        local_acceptance: policy.local_acceptance(),
        activity_anchor: user
            .created_at
            .max(user.last_api_activity_at)
            .max(payment.last_paid_complete_at),
        last_api_activity_at: user.last_api_activity_at,
        now,
        credential_complete,
    }
}

pub(crate) async fn dashboard_self_user_facts_in_transaction(
    transaction: &mut Transaction<'_, Postgres>,
    user: DashboardDeveloperAccessUserFacts,
    policy: DashboardDeveloperAccessPolicy,
) -> Result<DashboardSelfUserFacts, sqlx::Error> {
    let payment = if user.role < 10 && user.trust_level_override.is_none() {
        // Payment-policy rows are an authorization fact. A table SHARE lock
        // makes their snapshot stable through credential commit and serializes
        // concurrent UPDATE/DELETE/INSERT mutations after this transaction.
        sqlx::query("LOCK TABLE top_ups IN SHARE MODE")
            .execute(&mut **transaction)
            .await?;
        let row = sqlx::query(CURRENT_USER_PAYMENT_SNAPSHOT)
            .bind(user.user_id)
            .fetch_one(&mut **transaction)
            .await?;
        PaymentSnapshot {
            paid_amount: row.try_get("paid_amount")?,
            last_paid_complete_at: row.try_get("last_paid_complete_at")?,
            paid_activation_complete: row.try_get("paid_activation_complete")?,
        }
    } else {
        PaymentSnapshot::default()
    };
    Ok(dashboard_self_user_facts_from_payment(
        user,
        payment,
        policy,
        unix_now(),
        false,
    ))
}

impl UserRecord {
    fn dashboard_user(self, admin_permissions: Value) -> DashboardUser {
        let sidebar_modules = serde_json::from_str::<Value>(&self.setting)
            .ok()
            .and_then(|value| {
                value
                    .get("sidebar_modules")
                    .and_then(Value::as_str)
                    .map(|modules| json!(modules))
            })
            // Legacy `UserSetting.SidebarModules` is a string, including the
            // empty-string zero value when no setting has been saved.
            .unwrap_or_else(|| json!(""));
        DashboardUser {
            id: self.id,
            username: self.username,
            display_name: self.display_name,
            role: self.role,
            status: self.status,
            email: self.email,
            github_id: self.github_id,
            discord_id: self.discord_id,
            oidc_id: self.oidc_id,
            wechat_id: self.wechat_id,
            telegram_id: self.telegram_id,
            group: self.group,
            quota: self.quota,
            used_quota: self.used_quota,
            request_count: self.request_count,
            aff_code: self.aff_code,
            aff_count: self.aff_count,
            aff_quota: self.aff_quota,
            aff_history_quota: self.aff_history_quota,
            inviter_id: self.inviter_id,
            linux_do_id: self.linux_do_id,
            setting: self.setting,
            stripe_customer: self.stripe_customer,
            sidebar_modules,
            permissions: permissions(self.role, admin_permissions),
        }
    }
}

#[derive(Clone)]
struct SessionRecord {
    sid: String,
    user_id: i64,
    version: i64,
    user_auth_version: i64,
    status: String,
    refresh_hash: String,
    previous_refresh_hash: String,
    previous_valid_until: i64,
    login_method: String,
    ip: String,
    user_agent: String,
    created_at: i64,
    last_active_at: i64,
    expires_at: i64,
    revoked_at: i64,
    revoked_reason: String,
}

impl SessionRecord {
    fn view(&self) -> LoginSessionView {
        LoginSessionView {
            sid: self.sid.clone(),
            current: true,
            login_method: self.login_method.clone(),
            ip: self.ip.clone(),
            user_agent: self.user_agent.clone(),
            created_at: self.created_at,
            last_active_at: self.last_active_at,
            expires_at: self.expires_at,
        }
    }
}

async fn insert_session(
    tx: &mut Transaction<'_, Postgres>,
    session: &SessionRecord,
) -> Result<(), AuthError> {
    sqlx::query(
        r#"INSERT INTO user_sessions
        (sid, user_id, version, user_auth_version, status, refresh_hash,
         previous_refresh_hash, previous_valid_until, login_method, ip, user_agent,
         created_at, last_active_at, expires_at, revoked_at, revoked_reason)
        VALUES ($1,$2,$3,$4,$5,$6,NULL,0,$7,$8,$9,$10,$11,$12,0,'')"#,
    )
    .bind(&session.sid)
    .bind(session.user_id)
    .bind(session.version)
    .bind(session.user_auth_version)
    .bind(&session.status)
    .bind(&session.refresh_hash)
    .bind(&session.login_method)
    .bind(&session.ip)
    .bind(&session.user_agent)
    .bind(session.created_at)
    .bind(session.last_active_at)
    .bind(session.expires_at)
    .execute(&mut **tx)
    .await
    .map_err(internal)?;
    Ok(())
}

fn user_from_row(row: &sqlx::postgres::PgRow) -> Result<UserRecord, AuthError> {
    Ok(UserRecord {
        id: row.try_get("id").map_err(internal)?,
        username: row.try_get("username").map_err(internal)?,
        password: row.try_get("password").map_err(internal)?,
        display_name: row.try_get("display_name").map_err(internal)?,
        role: row.try_get("role").map_err(internal)?,
        status: row.try_get("status").map_err(internal)?,
        email: row.try_get("email").map_err(internal)?,
        github_id: row.try_get("github_id").map_err(internal)?,
        discord_id: row.try_get("discord_id").map_err(internal)?,
        oidc_id: row.try_get("oidc_id").map_err(internal)?,
        wechat_id: row.try_get("wechat_id").map_err(internal)?,
        telegram_id: row.try_get("telegram_id").map_err(internal)?,
        group: row.try_get("user_group").map_err(internal)?,
        quota: row.try_get("quota").map_err(internal)?,
        used_quota: row.try_get("used_quota").map_err(internal)?,
        request_count: row.try_get("request_count").map_err(internal)?,
        aff_code: row.try_get("aff_code").map_err(internal)?,
        aff_count: row.try_get("aff_count").map_err(internal)?,
        aff_quota: row.try_get("aff_quota").map_err(internal)?,
        aff_history_quota: row.try_get("aff_history_quota").map_err(internal)?,
        inviter_id: row.try_get("inviter_id").map_err(internal)?,
        linux_do_id: row.try_get("linux_do_id").map_err(internal)?,
        setting: row.try_get("setting").map_err(internal)?,
        stripe_customer: row.try_get("stripe_customer").map_err(internal)?,
        auth_version: row.try_get("auth_version").map_err(internal)?,
        created_at: row.try_get("created_at").map_err(internal)?,
        last_api_activity_at: row.try_get("last_api_activity_at").map_err(internal)?,
        trust_level_override: row.try_get("trust_level_override").map_err(internal)?,
    })
}

fn session_from_row(row: &sqlx::postgres::PgRow) -> Result<SessionRecord, AuthError> {
    Ok(SessionRecord {
        sid: row.try_get("sid").map_err(internal)?,
        user_id: row.try_get("user_id").map_err(internal)?,
        version: row.try_get("version").map_err(internal)?,
        user_auth_version: row.try_get("user_auth_version").map_err(internal)?,
        status: row.try_get("status").map_err(internal)?,
        refresh_hash: row.try_get("refresh_hash").map_err(internal)?,
        previous_refresh_hash: row.try_get("previous_refresh_hash").map_err(internal)?,
        previous_valid_until: row.try_get("previous_valid_until").map_err(internal)?,
        login_method: row.try_get("login_method").map_err(internal)?,
        ip: row.try_get("ip").map_err(internal)?,
        user_agent: row.try_get("user_agent").map_err(internal)?,
        created_at: row.try_get("created_at").map_err(internal)?,
        last_active_at: row.try_get("last_active_at").map_err(internal)?,
        expires_at: row.try_get("expires_at").map_err(internal)?,
        revoked_at: row.try_get("revoked_at").map_err(internal)?,
        revoked_reason: row.try_get("revoked_reason").map_err(internal)?,
    })
}

fn permissions(role: i64, admin_permissions: Value) -> Value {
    let (sidebar_settings, sidebar_modules) = if role == 100 {
        (false, json!({}))
    } else if role == 10 {
        (true, json!({"admin": {"setting": false}}))
    } else {
        (true, json!({"admin": false}))
    };
    let mut result = Map::new();
    result.insert("sidebar_settings".to_owned(), json!(sidebar_settings));
    result.insert("sidebar_modules".to_owned(), sidebar_modules);
    result.insert("admin_permissions".to_owned(), admin_permissions);
    Value::Object(result)
}

#[derive(Deserialize)]
struct TwoFactorFlowPayload {
    auth_version: i64,
}

fn validate_totp(secret: &str, code: &str) -> bool {
    if code.len() != 6 || !code.bytes().all(|byte| byte.is_ascii_digit()) {
        return false;
    }
    let Ok(secret) = Secret::Encoded(secret.trim().to_ascii_uppercase()).to_bytes() else {
        return false;
    };
    let Ok(totp) = TOTP::new(Algorithm::SHA1, 6, 1, 30, secret) else {
        return false;
    };
    totp.check_current(code).unwrap_or(false)
}

fn ct_equal(left: &str, right: &str) -> bool {
    left.as_bytes().ct_eq(right.as_bytes()).into()
}

fn truncate(value: String, max: usize) -> String {
    let trimmed = value.trim();
    trimmed.chars().take(max).collect()
}

fn unix_now() -> i64 {
    UNIX_EPOCH
        .elapsed()
        .map_or(0, |elapsed| elapsed.as_secs() as i64)
}

fn internal<E>(_error: E) -> AuthError {
    AuthError::new(AuthErrorKind::Internal)
}

fn legacy_personal_access_token_write_error(error: sqlx::Error) -> AuthError {
    let message = error.as_database_error().map_or_else(
        || error.to_string(),
        |database| legacy_postgres_error(database.message(), database.code().as_deref()),
    );
    AuthError::with_legacy_response_message(AuthErrorKind::Internal, message)
}

fn legacy_postgres_error(message: &str, code: Option<&str>) -> String {
    match code {
        Some(code) if !code.is_empty() => format!("ERROR: {message} (SQLSTATE {code})"),
        _ => message.to_owned(),
    }
}

#[cfg(test)]
mod legacy_personal_access_token_error_tests {
    use super::legacy_postgres_error;

    #[test]
    fn legacy_database_write_error_keeps_postgres_message_and_sqlstate() {
        assert_eq!(
            legacy_postgres_error("transaction fixture injected write failure", Some("P0001")),
            "ERROR: transaction fixture injected write failure (SQLSTATE P0001)"
        );
    }

    #[test]
    fn legacy_database_write_error_keeps_non_postgres_message_without_sqlstate() {
        assert_eq!(
            legacy_postgres_error("connection closed", None),
            "connection closed"
        );
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn digest_comparison_is_content_constant_time() {
        assert!(ct_equal(&"a".repeat(64), &"a".repeat(64)));
        assert!(!ct_equal(&"a".repeat(64), &"b".repeat(64)));
    }

    #[test]
    fn metadata_is_bounded_like_the_go_service() {
        assert_eq!(truncate(" x ".to_owned(), 64), "x");
        assert_eq!(truncate("x".repeat(70), 64).len(), 64);
    }

    #[test]
    fn login_user_permissions_do_not_expose_console_activation_timestamp() {
        let value = permissions(1, json!({"read": true}));
        assert!(
            value
                .get("admin_permissions")
                .and_then(Value::as_object)
                .and_then(|admin| admin.get("read"))
                .and_then(Value::as_bool)
                .unwrap_or(false)
        );
        assert!(value.get("console_activated_at").is_none());
    }

    #[test]
    fn source_mapping_covers_every_p0_legacy_layer() {
        let sources = super::super::LEGACY_AUTH_SOURCE_MAP
            .iter()
            .map(|(source, _)| *source)
            .collect::<Vec<_>>();
        for required in [
            "controller/user.go",
            "controller/auth_session.go",
            "middleware/auth.go",
            "service/auth_token.go",
            "service/auth_session.go",
            "model/user.go",
            "model/user_auth_cache.go",
            "model/user_session.go",
            "router/api-router.go",
        ] {
            assert!(
                sources.contains(&required),
                "missing migration source {required}"
            );
        }
    }
}
