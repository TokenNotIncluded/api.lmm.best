//! Native password login and rotating bearer sessions. No Go calls or cookies
//! are used for core API authorization.
use super::*;
use argon2::{
    Algorithm, Argon2, Params, Version,
    password_hash::{PasswordHash, PasswordHasher, PasswordVerifier, SaltString},
};
use serde::Deserialize;
use std::env;

const REGISTER_LOCK: i64 = 741_803_121;
const DUMMY_HASH: &str = "$argon2id$v=19$m=19456,t=2,p=1$bG1tLWR1bW15LXNhbHQxNg$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";

/// Deliberately not Debug: requests contain a password.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct LoginRequest {
    pub login_name: String,
    pub password: String,
}
#[derive(Serialize)]
pub struct CredentialSummary {
    pub id: i64,
    pub kind: String,
    pub user_id: i64,
    pub owner_account_id: i64,
    pub expires_at: i64,
    pub revoked: bool,
    pub created_at: i64,
}

pub(super) async fn lock_registration(c: &mut PgConnection) -> Result<()> {
    sqlx::query("SELECT pg_advisory_xact_lock($1)")
        .bind(REGISTER_LOCK)
        .execute(c)
        .await?;
    Ok(())
}
pub(super) async fn create_user(c: &mut PgConnection) -> Result<i64> {
    lock_registration(c).await?;
    let account_id = new_account(c, "personal", 0).await?;
    let id = sqlx::query_scalar(
        "INSERT INTO core_identity.users(personal_account_id) VALUES ($1) RETURNING id",
    )
    .bind(account_id)
    .fetch_one(c)
    .await?;
    Ok(id)
}
pub(super) async fn issue_session(
    c: &mut PgConnection,
    user_id: i64,
    action: &str,
) -> Result<IssuedCredential> {
    issue_session_with_secret(c, user_id, action, new_secret("lmms_")?).await
}
pub(super) async fn issue_session_with_secret(
    c: &mut PgConnection,
    user_id: i64,
    action: &str,
    secret: String,
) -> Result<IssuedCredential> {
    let user = sqlx::query("SELECT u.personal_account_id,u.auth_version FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id WHERE u.id=$1 AND u.active AND a.active FOR SHARE OF u,a")
        .bind(user_id).fetch_optional(&mut *c).await?.ok_or(IdentityError::Unauthorized)?;
    // This helper has no completed second-factor proof. A provider identity
    // alone must not bypass an account's local second-factor requirement.
    // Read after the user lock with a fresh statement snapshot. Do not lock
    // the profile: profile_policy_generation takes the user lock in turn.
    let second_factor_required: Option<bool> = sqlx::query_scalar(
        "SELECT second_factor_required FROM core_identity.user_profiles WHERE user_id=$1",
    )
    .bind(user_id)
    .fetch_optional(&mut *c)
    .await?;
    if second_factor_required == Some(true) {
        return Err(IdentityError::Unauthorized);
    }
    let id = sqlx::query_scalar("INSERT INTO core_identity.credentials(digest,kind,user_id,user_version,owner_account_id,expires_at) VALUES ($1,'session',$2,$3,$4,clock_timestamp()+interval '7 days') RETURNING id")
        .bind(digest(&secret)?).bind(user_id).bind(user.try_get::<i64,_>("auth_version")?)
        .bind(user.try_get::<i64,_>("personal_account_id")?).fetch_one(&mut *c).await?;
    let actor = authenticate(c, &secret, false).await?;
    audit(
        c,
        &actor,
        action,
        json!({"user_id":user_id,"session_id":id}),
    )
    .await?;
    Ok(IssuedCredential { id, secret })
}
pub(super) fn normalize_login(input: &str) -> Result<String> {
    let name = input.to_ascii_lowercase();
    if !(3..=64).contains(&name.len())
        || !name.as_bytes()[0].is_ascii_alphanumeric()
        || !name
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"_.-".contains(&b))
    {
        return Err(IdentityError::Invalid);
    }
    Ok(name)
}
fn password_policy(password: &str) -> Result<()> {
    if password.len() > 1024 || password.chars().count() < 12 {
        return Err(IdentityError::Invalid);
    }
    Ok(())
}
fn password_engine() -> Result<Argon2<'static>> {
    let params = Params::new(19_456, 2, 1, Some(32)).map_err(|_| IdentityError::Storage)?;
    Ok(Argon2::new(Algorithm::Argon2id, Version::V0x13, params))
}
impl IdentityStore {
    /// Disabled by default, including on from_pool. Enable explicitly at startup.
    pub fn with_registration_enabled(mut self, enabled: bool) -> Self {
        self.registration_enabled = enabled;
        self
    }
    /// Limit simultaneous Argon2 jobs without reducing password hash strength.
    /// Each active job allocates about 19 MiB with the current Argon2 policy.
    pub fn with_password_slots(mut self, slots: usize) -> Result<Self> {
        if !(1..=2).contains(&slots) {
            return Err(IdentityError::Invalid);
        }
        self.password_slots = std::sync::Arc::new(tokio::sync::Semaphore::new(slots));
        Ok(self)
    }
    pub fn with_google_oauth(mut self, config: GoogleOAuth) -> Self {
        self.google = Some(std::sync::Arc::new(config));
        self
    }
    pub fn with_auth_from_env(mut self) -> Result<Self> {
        self.registration_enabled = match env::var("LMM_CORE_REGISTRATION_ENABLED").as_deref() {
            Ok("1" | "true") => true,
            Ok("0" | "false") | Err(env::VarError::NotPresent) => false,
            _ => return Err(IdentityError::Invalid),
        };
        let values = [
            "LMM_CORE_GOOGLE_CLIENT_ID",
            "LMM_CORE_GOOGLE_CLIENT_SECRET",
            "LMM_CORE_GOOGLE_REDIRECT_URI",
        ]
        .map(env::var);
        match values {
            [
                Err(env::VarError::NotPresent),
                Err(env::VarError::NotPresent),
                Err(env::VarError::NotPresent),
            ] => {}
            [Ok(id), Ok(secret), Ok(uri)] => {
                self = self.with_google_oauth(GoogleOAuth::new(id, secret, uri)?)
            }
            _ => return Err(IdentityError::Invalid),
        }
        Ok(self)
    }
    /// Database-backed fixed windows are shared by all Rust replicas. Do not
    /// derive the peer key from untrusted forwarded headers.
    pub async fn auth_rate_limit(&self, scope: &str, key: &str, limit: i32) -> Result<bool> {
        if !(1..=10_000).contains(&limit) || scope.len() > 32 || key.len() > 256 {
            return Err(IdentityError::Invalid);
        }
        let bucket = Sha256::digest(format!("{scope}\0{key}").as_bytes()).to_vec();
        sqlx::query("DELETE FROM core_identity.auth_attempts WHERE bucket IN (SELECT bucket FROM core_identity.auth_attempts WHERE started_at<clock_timestamp()-interval '1 hour' LIMIT 100)")
            .execute(&self.pool).await?;
        let attempts: i32 = sqlx::query_scalar("INSERT INTO core_identity.auth_attempts AS a(bucket,attempts) VALUES ($1,1) ON CONFLICT(bucket) DO UPDATE SET attempts=CASE WHEN a.started_at<=clock_timestamp()-interval '1 minute' THEN 1 ELSE LEAST(a.attempts+1,10001) END,started_at=CASE WHEN a.started_at<=clock_timestamp()-interval '1 minute' THEN clock_timestamp() ELSE a.started_at END RETURNING attempts")
            .bind(bucket).fetch_one(&self.pool).await?;
        Ok(attempts <= limit)
    }
    pub(super) async fn hash_password(&self, password: String) -> Result<String> {
        let permit = self
            .password_slots
            .clone()
            .try_acquire_owned()
            .map_err(|_| IdentityError::Storage)?;
        tokio::task::spawn_blocking(move || {
            let _permit = permit;
            let salt = SaltString::generate(&mut OsRng);
            password_engine()?
                .hash_password(password.as_bytes(), &salt)
                .map(|h| h.to_string())
                .map_err(|_| IdentityError::Storage)
        })
        .await
        .map_err(|_| IdentityError::Storage)?
    }
    async fn verify_password(&self, password: String, hash: String) -> Result<bool> {
        let permit = self
            .password_slots
            .clone()
            .try_acquire_owned()
            .map_err(|_| IdentityError::Storage)?;
        tokio::task::spawn_blocking(move || {
            let _permit = permit;
            let parsed = PasswordHash::new(&hash).map_err(|_| IdentityError::Storage)?;
            Ok(password_engine()?
                .verify_password(password.as_bytes(), &parsed)
                .is_ok())
        })
        .await
        .map_err(|_| IdentityError::Storage)?
    }
    pub async fn register(&self, request: &LoginRequest) -> Result<IssuedCredential> {
        if !self.registration_enabled {
            return Err(IdentityError::Forbidden);
        }
        let name = normalize_login(&request.login_name)?;
        password_policy(&request.password)?;
        let hash = self.hash_password(request.password.clone()).await?;
        let mut tx = self.pool.begin().await?;
        let user_id = create_user(&mut tx).await?;
        sqlx::query("INSERT INTO core_identity.password_logins(login_name,user_id,password_hash) VALUES ($1,$2,$3)")
            .bind(name).bind(user_id).bind(hash).execute(&mut *tx).await?;
        sqlx::query("INSERT INTO core_identity.user_profiles(user_id,username,display_name) SELECT user_id,login_name,login_name FROM core_identity.password_logins WHERE user_id=$1")
            .bind(user_id).execute(&mut *tx).await?;
        let issued = issue_session(&mut tx, user_id, "user.register").await?;
        tx.commit().await?;
        Ok(issued)
    }
    pub(super) async fn verified_password(
        &self,
        name: &str,
        password: &str,
    ) -> Result<(String, Option<String>)> {
        if password.is_empty()
            || password.len() > 1024
            || !self.auth_rate_limit("password", name, 10).await?
        {
            return Err(IdentityError::Unauthorized);
        }
        let row = sqlx::query("SELECT p.password_hash,COALESCE(pr.password_login_allowed,TRUE) AS allowed,COALESCE(pr.second_factor_required,FALSE) AS second_factor,COALESCE(pr.legacy_bcrypt_allowed,FALSE) AS bcrypt FROM core_identity.password_logins p LEFT JOIN core_identity.user_profiles pr ON pr.user_id=p.user_id WHERE p.login_name=$1")
            .bind(name).fetch_optional(&self.pool).await?;
        let hash = row
            .as_ref()
            .map(|r| r.try_get::<String, _>("password_hash"))
            .transpose()?
            .unwrap_or_else(|| DUMMY_HASH.to_owned());
        let legacy =
            hash.starts_with("$2a$") || hash.starts_with("$2b$") || hash.starts_with("$2y$");
        let allowed_bcrypt = row
            .as_ref()
            .map(|r| r.try_get::<bool, _>("bcrypt"))
            .transpose()?
            .unwrap_or(false);
        let valid = if legacy && allowed_bcrypt {
            // Imported bcrypt costs are bounded before starting expensive work.
            let cost = hash
                .get(4..6)
                .and_then(|v| v.parse::<u32>().ok())
                .filter(|cost| (4..=16).contains(cost))
                .ok_or(IdentityError::Unauthorized)?;
            let _ = cost;
            let permit = self
                .password_slots
                .clone()
                .try_acquire_owned()
                .map_err(|_| IdentityError::Storage)?;
            let password = password.to_owned();
            let hash = hash.clone();
            tokio::task::spawn_blocking(move || {
                let _permit = permit;
                bcrypt::verify(password, &hash).unwrap_or(false)
            })
            .await
            .map_err(|_| IdentityError::Storage)?
        } else if legacy {
            false
        } else {
            self.verify_password(password.to_owned(), hash.clone())
                .await?
        };
        if !valid
            || row.is_none()
            || !row.as_ref().unwrap().try_get::<bool, _>("allowed")?
            || row.as_ref().unwrap().try_get::<bool, _>("second_factor")?
        {
            return Err(IdentityError::Unauthorized);
        }
        let upgraded = if legacy {
            Some(self.hash_password(password.to_owned()).await?)
        } else {
            None
        };
        Ok((hash, upgraded))
    }
    pub(super) async fn lock_password_user(
        &self,
        c: &mut PgConnection,
        name: &str,
        verified: &(String, Option<String>),
    ) -> Result<i64> {
        let user_id: i64 = sqlx::query_scalar("SELECT p.user_id FROM core_identity.password_logins p JOIN core_identity.users u ON u.id=p.user_id JOIN core_identity.accounts a ON a.id=u.personal_account_id WHERE p.login_name=$1 AND p.password_hash=$2 AND u.active AND a.active FOR UPDATE OF p FOR SHARE OF u,a")
            .bind(name).bind(&verified.0).fetch_optional(&mut *c).await?.ok_or(IdentityError::Unauthorized)?;
        if let Some(policy) = sqlx::query("SELECT password_login_allowed,second_factor_required,legacy_bcrypt_allowed FROM core_identity.user_profiles WHERE user_id=$1 FOR UPDATE")
            .bind(user_id).fetch_optional(&mut *c).await? {
            if !policy.try_get::<bool,_>("password_login_allowed")? || policy.try_get::<bool,_>("second_factor_required")? || (verified.1.is_some() && !policy.try_get::<bool,_>("legacy_bcrypt_allowed")?) { return Err(IdentityError::Unauthorized); }
        } else if verified.1.is_some() { return Err(IdentityError::Unauthorized); }
        if let Some(upgraded) = &verified.1 {
            sqlx::query(
                "UPDATE core_identity.password_logins SET password_hash=$2 WHERE login_name=$1",
            )
            .bind(name)
            .bind(upgraded)
            .execute(&mut *c)
            .await?;
            sqlx::query("UPDATE core_identity.user_profiles SET legacy_bcrypt_allowed=FALSE WHERE user_id=$1").bind(user_id).execute(&mut *c).await?;
        }
        Ok(user_id)
    }
    pub async fn login(&self, request: &LoginRequest) -> Result<IssuedCredential> {
        let name = normalize_login(&request.login_name).map_err(|_| IdentityError::Unauthorized)?;
        let verified = self.verified_password(&name, &request.password).await?;
        let mut tx = self.pool.begin().await?;
        let user_id = self.lock_password_user(&mut tx, &name, &verified).await?;
        let issued = issue_session(&mut tx, user_id, "session.login").await?;
        tx.commit().await?;
        Ok(issued)
    }
    /// Rotation extends inactivity expiry by seven days, never beyond 30 days
    /// from the original login. The old bearer cannot be replayed.
    pub async fn refresh_session(&self, secret: &str) -> Result<IssuedCredential> {
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let browser: bool=sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM core_identity.web_sessions WHERE current_credential_id=$1)").bind(actor.credential_id).fetch_one(&mut *tx).await?;
        if browser {
            return Err(IdentityError::Forbidden);
        }
        let changed = sqlx::query("UPDATE core_identity.credentials SET revoked_at=clock_timestamp() WHERE id=$1 AND revoked_at IS NULL AND expires_at>clock_timestamp() AND session_started_at+interval '30 days'>clock_timestamp()")
            .bind(actor.credential_id).execute(&mut *tx).await?.rows_affected();
        if changed != 1 {
            return Err(IdentityError::Unauthorized);
        }
        let issued = new_secret("lmms_")?;
        let id = sqlx::query_scalar("INSERT INTO core_identity.credentials(digest,kind,user_id,user_version,owner_account_id,expires_at,session_started_at) SELECT $1,'session',user_id,user_version,owner_account_id,LEAST(clock_timestamp()+interval '7 days',session_started_at+interval '30 days'),session_started_at FROM core_identity.credentials WHERE id=$2 RETURNING id")
            .bind(digest(&issued)?).bind(actor.credential_id).fetch_one(&mut *tx).await?;
        // Rotation preserves pending invitations issued by this login. Explicit
        // revocation still invalidates invitations bound to that credential.
        sqlx::query("UPDATE core_identity.invites SET inviter_credential_id=$1 WHERE inviter_credential_id=$2 AND accepted_at IS NULL AND rejected_at IS NULL AND withdrawn_at IS NULL AND expires_at>clock_timestamp()")
            .bind(id).bind(actor.credential_id).execute(&mut *tx).await?;
        audit(
            &mut tx,
            &actor,
            "session.refresh",
            json!({"old_session_id":actor.credential_id,"session_id":id}),
        )
        .await?;
        tx.commit().await?;
        Ok(IssuedCredential { id, secret: issued })
    }
    pub async fn logout(&self, secret: &str) -> Result<()> {
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        sqlx::query("UPDATE core_identity.credentials SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=$1")
            .bind(actor.credential_id).execute(&mut *tx).await?;
        audit(
            &mut tx,
            &actor,
            "session.logout",
            json!({"session_id":actor.credential_id}),
        )
        .await?;
        tx.commit().await?;
        Ok(())
    }
    /// Sign out all devices without invalidating API keys. Credential creation
    /// takes a shared user lock, so it cannot slip through this transaction.
    pub async fn logout_all(&self, secret: &str) -> Result<()> {
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        sqlx::query("SELECT id FROM core_identity.users WHERE id=$1 FOR UPDATE")
            .bind(actor.user_id)
            .fetch_one(&mut *tx)
            .await?;
        sqlx::query("UPDATE core_identity.credentials SET revoked_at=clock_timestamp() WHERE user_id=$1 AND kind='session' AND revoked_at IS NULL")
            .bind(actor.user_id).execute(&mut *tx).await?;
        audit(
            &mut tx,
            &actor,
            "session.logout_all",
            json!({"user_id":actor.user_id}),
        )
        .await?;
        tx.commit().await?;
        Ok(())
    }
    pub async fn list_credentials(
        &self,
        secret: &str,
        after: i64,
        team_id: Option<i64>,
    ) -> Result<Vec<CredentialSummary>> {
        if after < 0 {
            return Err(IdentityError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let (owner, all_members) = if let Some(id) = team_id {
            let team = lock_team(&mut tx, id).await?;
            let (role, _) = team_role(&mut tx, &team, id, actor.user_id).await?;
            (
                Some(team.try_get::<i64, _>("account_id")?),
                matches!(role, TeamRole::Owner | TeamRole::Admin),
            )
        } else {
            (None, false)
        };
        let rows = sqlx::query("SELECT id,kind,user_id,owner_account_id,EXTRACT(EPOCH FROM expires_at)::bigint AS expires,revoked_at IS NOT NULL AS revoked,EXTRACT(EPOCH FROM created_at)::bigint AS created FROM core_identity.credentials WHERE id>$1 AND ($2::bigint IS NULL OR owner_account_id=$2) AND ($3 OR user_id=$4) ORDER BY id LIMIT 100")
            .bind(after).bind(owner).bind(all_members).bind(actor.user_id).fetch_all(&mut *tx).await?;
        let result = rows
            .into_iter()
            .map(|r| {
                Ok(CredentialSummary {
                    id: r.try_get("id")?,
                    kind: r.try_get("kind")?,
                    user_id: r.try_get("user_id")?,
                    owner_account_id: r.try_get("owner_account_id")?,
                    expires_at: r.try_get("expires")?,
                    revoked: r.try_get("revoked")?,
                    created_at: r.try_get("created")?,
                })
            })
            .collect::<Result<Vec<_>>>()?;
        tx.commit().await?;
        Ok(result)
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn names_and_passwords_are_bounded_without_trimming_secrets() {
        assert_eq!(normalize_login("Alice_1").unwrap(), "alice_1");
        for bad in [
            "ab",
            " alice",
            "alice ",
            "a@example.com",
            "用户一",
            "_alice",
        ] {
            assert_eq!(normalize_login(bad), Err(IdentityError::Invalid));
        }
        assert!(password_policy("a long passphrase with spaces").is_ok());
        assert!(password_policy("12345678901").is_err());
        assert!(password_policy(&"a".repeat(1025)).is_err());
        let dummy = PasswordHash::new(DUMMY_HASH).unwrap();
        assert!(
            password_engine()
                .unwrap()
                .verify_password(b"bad password", &dummy)
                .is_err()
        );
    }
}
