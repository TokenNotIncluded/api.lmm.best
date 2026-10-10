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
    let user = sqlx::query("SELECT u.personal_account_id,u.auth_version FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id WHERE u.id=$1 AND u.active AND a.active FOR SHARE OF u,a")
        .bind(user_id).fetch_optional(&mut *c).await?.ok_or(IdentityError::Unauthorized)?;
    let secret = new_secret("lmms_")?;
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
fn normalize_login(input: &str) -> Result<String> {
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
    async fn hash_password(&self, password: String) -> Result<String> {
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
        let issued = issue_session(&mut tx, user_id, "user.register").await?;
        tx.commit().await?;
        Ok(issued)
    }
    pub async fn login(&self, request: &LoginRequest) -> Result<IssuedCredential> {
        let name = normalize_login(&request.login_name).map_err(|_| IdentityError::Unauthorized)?;
        if request.password.is_empty()
            || request.password.len() > 1024
            || !self.auth_rate_limit("password", &name, 10).await?
        {
            return Err(IdentityError::Unauthorized);
        }
        let hash: Option<String> = sqlx::query_scalar(
            "SELECT password_hash FROM core_identity.password_logins WHERE login_name=$1",
        )
        .bind(&name)
        .fetch_optional(&self.pool)
        .await?;
        let valid = self
            .verify_password(
                request.password.clone(),
                hash.clone().unwrap_or_else(|| DUMMY_HASH.to_owned()),
            )
            .await?;
        if !valid || hash.is_none() {
            return Err(IdentityError::Unauthorized);
        }
        let mut tx = self.pool.begin().await?;
        // Recheck the exact hash under a lock after expensive verification.
        let user_id: i64 = sqlx::query_scalar("SELECT p.user_id FROM core_identity.password_logins p JOIN core_identity.users u ON u.id=p.user_id JOIN core_identity.accounts a ON a.id=u.personal_account_id WHERE p.login_name=$1 AND p.password_hash=$2 AND u.active AND a.active FOR SHARE OF p,u,a")
            .bind(name).bind(hash).fetch_optional(&mut *tx).await?.ok_or(IdentityError::Unauthorized)?;
        let issued = issue_session(&mut tx, user_id, "session.login").await?;
        tx.commit().await?;
        Ok(issued)
    }
    /// Rotation extends inactivity expiry by seven days, never beyond 30 days
    /// from the original login. The old bearer cannot be replayed.
    pub async fn refresh_session(&self, secret: &str) -> Result<IssuedCredential> {
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
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
