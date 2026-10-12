//! Change a browser user's password and rotate that same login atomically.
use super::*;

impl BrowserAuth {
    /// Requires both a current browser bearer and the existing password.
    /// Advancing the user generation invalidates other sessions and API keys.
    /// The current family retains its original absolute and weekly deadlines.
    pub async fn change_password(
        &self,
        access: &str,
        original_password: &str,
        password: &str,
        display_name: Option<&str>,
        expected_sid: Option<&str>,
    ) -> BrowserResult<BrowserBundle> {
        let actor = self.store.authorize(access).await?;
        if actor.credential_kind != CredentialKind::Session {
            return Err(IdentityError::Forbidden.into());
        }
        if !(8..=20).contains(&password.chars().count())
            || password.len() > 1024
            || original_password.is_empty()
            || original_password.len() > 1024
            || display_name.is_some_and(|v| v.len() > 256 || v.chars().any(char::is_control))
        {
            return Err(IdentityError::Invalid.into());
        }
        let (sid, login): (String, String) = sqlx::query_as(
            "SELECT s.sid,p.login_name FROM core_identity.web_sessions s
             JOIN core_identity.password_logins p ON p.user_id=s.user_id
             WHERE s.current_credential_id=$1 AND s.user_id=$2",
        )
        .bind(actor.credential_id)
        .bind(actor.user_id)
        .fetch_optional(&self.store.pool)
        .await?
        .ok_or(IdentityError::Forbidden)?;
        if expected_sid.is_some_and(|expected| expected != sid) {
            return Err(BrowserAuthError::SessionMismatch);
        }
        // Do expensive work without holding transaction locks. Recheck the
        // exact password, policy, generation and browser family after locking.
        let verified = self
            .store
            .verified_password(&login, original_password)
            .await?;
        let hash = self.store.hash_password(password.to_owned()).await?;
        let mut tx = self.store.pool.begin().await?;
        // Refresh locks the family before the user; policy updates lock the
        // profile first. Take all required row locks without waiting instead
        // of upgrading shared locks and introducing a lock-order cycle.
        let locked = sqlx::query(
            "SELECT u.id FROM core_identity.users u
             JOIN core_identity.accounts a ON a.id=u.personal_account_id
             JOIN core_identity.credentials c ON c.user_id=u.id
             JOIN core_identity.web_sessions s ON s.current_credential_id=c.id
             JOIN core_identity.password_logins p ON p.user_id=u.id
             JOIN core_identity.user_profiles pr ON pr.user_id=u.id
             WHERE u.id=$1 AND c.id=$2 AND s.sid=$3
             FOR UPDATE OF u,c,s,p,pr NOWAIT FOR SHARE OF a NOWAIT",
        )
        .bind(actor.user_id)
        .bind(actor.credential_id)
        .bind(&sid)
        .fetch_optional(&mut *tx)
        .await
        .map_err(|error| {
            if error.as_database_error().and_then(|e| e.code()).as_deref() == Some("55P03") {
                IdentityError::Conflict
            } else {
                IdentityError::from(error)
            }
        })?;
        if locked.is_none() {
            return Err(IdentityError::Unauthorized.into());
        }
        // New statement snapshots are important after acquiring the locks.
        let current = session(&mut tx, access).await?;
        let family = lock_family(&mut tx, &sid).await?;
        require_family_live(&family)?;
        let user = self
            .store
            .lock_password_user(&mut tx, &login, &verified)
            .await?;
        if user != current.user_id || current.user_version != actor.user_version {
            return Err(IdentityError::Unauthorized.into());
        }
        let version: i64 = sqlx::query_scalar(
            "UPDATE core_identity.users SET auth_version=auth_version+1
             WHERE id=$1 AND auth_version=$2 RETURNING auth_version",
        )
        .bind(user)
        .bind(current.user_version)
        .fetch_optional(&mut *tx)
        .await?
        .ok_or(IdentityError::Conflict)?;
        sqlx::query("UPDATE core_identity.password_logins SET password_hash=$2 WHERE user_id=$1")
            .bind(user)
            .bind(hash)
            .execute(&mut *tx)
            .await?;
        sqlx::query(
            "UPDATE core_identity.user_profiles
             SET legacy_bcrypt_allowed=FALSE,display_name=COALESCE($2,display_name)
             WHERE user_id=$1",
        )
        .bind(user)
        .bind(display_name)
        .execute(&mut *tx)
        .await?;
        let refresh = new_secret("lmmr_")?;
        let next = self.derived_secret(b"browser-access-v1", &sid, &refresh, "lmms_")?;
        let issued = auth::issue_session_with_secret(
            &mut tx,
            user,
            "session.password_change",
            next.clone(),
        )
        .await?;
        sqlx::query(
            "UPDATE core_identity.credentials
             SET expires_at=LEAST(expires_at,clock_timestamp()+interval '15 minutes',
                 (SELECT expires_at FROM core_identity.web_sessions WHERE sid=$2))
             WHERE id=$1",
        )
        .bind(issued.id)
        .bind(&sid)
        .execute(&mut *tx)
        .await?;
        sqlx::query(
            "UPDATE core_identity.credentials SET revoked_at=clock_timestamp() WHERE id=$1",
        )
        .bind(current.credential_id)
        .execute(&mut *tx)
        .await?;
        // Never grant a grace window to a refresh secret from before a
        // password change, and never extend the existing family lifetime.
        sqlx::query(
            "UPDATE core_identity.web_sessions
             SET user_version=$2,current_credential_id=$3,refresh_digest=$4,
                 previous_refresh_digest=NULL,previous_valid_until=NULL,
                 last_active_at=clock_timestamp()
             WHERE sid=$1",
        )
        .bind(&sid)
        .bind(version)
        .bind(issued.id)
        .bind(digest(&refresh)?)
        .execute(&mut *tx)
        .await?;
        sqlx::query(
            "INSERT INTO core_identity.web_session_credentials(credential_id,sid) VALUES ($1,$2)",
        )
        .bind(issued.id)
        .bind(&sid)
        .execute(&mut *tx)
        .await?;
        audit(
            &mut tx,
            &current,
            "password.change",
            json!({"user_id":user,"user_version":version,"session_id":issued.id}),
        )
        .await?;
        let bundle = make_bundle(
            &mut tx,
            &sid,
            &next,
            format!("{sid}.{refresh}"),
            self.store.financial_profile_enabled,
        )
        .await?;
        tx.commit().await?;
        Ok(bundle)
    }
}
