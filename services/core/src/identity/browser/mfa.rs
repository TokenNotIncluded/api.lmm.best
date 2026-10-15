//! MFA management keeps the requesting browser's stable family and deadlines.
use super::*;
use crate::identity::mfa::{self, MfaError, MfaResult, MfaSetup, MfaStatus};

impl BrowserAuth {
    pub async fn mfa_status(&self, access: &str) -> MfaResult<MfaStatus> {
        self.mfa_family(access).await?;
        self.store.mfa_status(access).await
    }

    pub async fn mfa_setup(&self, access: &str) -> MfaResult<MfaSetup> {
        let (_, sid) = self.mfa_family(access).await?;
        // Setup owns its transaction and rechecks this stable family after
        // preparing expensive recovery verifiers outside authority locks.
        self.store.mfa_setup(access, Some(&sid)).await
    }

    pub async fn mfa_enable(&self, access: &str, code: &str) -> MfaResult<BrowserBundle> {
        let (actor, sid) = self.mfa_family(access).await?;
        let mut tx = self.store.pool.begin().await?;
        let current = self.lock_mfa_family(&mut tx, access, &actor, &sid).await?;
        let assurance =
            match mfa::apply_enable(&self.store, &mut tx, &current, Some(&sid), code).await {
                Ok(assurance) => assurance,
                Err(error @ (MfaError::CodeInvalid | MfaError::Locked(_))) => {
                    // Invalid factor attempts must survive this HTTP rejection.
                    // Other failures roll back so infrastructure trouble cannot
                    // consume a verifier or change the factor's enabled state.
                    tx.commit().await?;
                    return Err(error);
                }
                Err(error) => return Err(error),
            };
        let bundle = self
            .rotate_mfa_family(&mut tx, &current, &sid, &assurance, "session.mfa_enable")
            .await?;
        tx.commit().await?;
        Ok(bundle)
    }

    pub async fn mfa_disable(&self, access: &str, code: &str) -> MfaResult<BrowserBundle> {
        let (actor, sid) = self.mfa_family(access).await?;
        let prepared = self.store.prepare_mfa_code(access, code, true).await?;
        let mut tx = self.store.pool.begin().await?;
        let current = self.lock_mfa_family(&mut tx, access, &actor, &sid).await?;
        let assurance = match mfa::apply_disable(&self.store, &mut tx, &current, &prepared).await {
            Ok(assurance) => assurance,
            Err(error @ (MfaError::CodeInvalid | MfaError::Locked(_))) => {
                tx.commit().await?;
                return Err(error);
            }
            Err(error) => return Err(error),
        };
        let bundle = self
            .rotate_mfa_family(&mut tx, &current, &sid, &assurance, "session.mfa_disable")
            .await?;
        tx.commit().await?;
        Ok(bundle)
    }

    pub async fn mfa_regenerate(&self, access: &str, code: &str) -> MfaResult<BrowserBundle> {
        let (actor, sid) = self.mfa_family(access).await?;
        let prepared = self.store.prepare_mfa_code(access, code, false).await?;
        let recovery = self.store.new_backup_codes().await?;
        let mut tx = self.store.pool.begin().await?;
        let current = self.lock_mfa_family(&mut tx, access, &actor, &sid).await?;
        let assurance =
            match mfa::apply_regenerate(&self.store, &mut tx, &current, &prepared, &recovery).await
            {
                Ok(assurance) => assurance,
                Err(error @ (MfaError::CodeInvalid | MfaError::Locked(_))) => {
                    tx.commit().await?;
                    return Err(error);
                }
                Err(error) => return Err(error),
            };
        let mut bundle = self
            .rotate_mfa_family(
                &mut tx,
                &current,
                &sid,
                &assurance,
                "session.mfa_backup_regenerate",
            )
            .await?;
        bundle.data["backup_codes"] = json!(recovery.codes);
        tx.commit().await?;
        Ok(bundle)
    }

    async fn mfa_family(&self, access: &str) -> MfaResult<(Principal, String)> {
        let actor = self.store.authorize(access).await?;
        if actor.credential_kind != CredentialKind::Session {
            return Err(IdentityError::Forbidden.into());
        }
        // A native bearer or an old bearer from the same family cannot manage
        // browser MFA. Setup is bound to SID, while each request still needs
        // the family's current, live access credential.
        let sid = sqlx::query_scalar(
            "SELECT sid FROM core_identity.web_sessions
             WHERE current_credential_id=$1 AND user_id=$2
               AND user_version=$3 AND revoked_at IS NULL
               AND expires_at>clock_timestamp()
               AND (NOT weekly_logout OR created_at>clock_timestamp()-interval '7 days')",
        )
        .bind(actor.credential_id)
        .bind(actor.user_id)
        .bind(actor.user_version)
        .fetch_optional(&self.store.pool)
        .await?
        .ok_or(IdentityError::Unauthorized)?;
        Ok((actor, sid))
    }

    async fn lock_mfa_family(
        &self,
        c: &mut PgConnection,
        access: &str,
        before: &Principal,
        sid: &str,
    ) -> MfaResult<Principal> {
        // Follow refresh's stable-family-first order, but never wait while
        // holding it: profile changes can start with profile then user. A new
        // statement after this lock sees the family's winning credential.
        sqlx::query("SELECT sid FROM core_identity.web_sessions WHERE sid=$1 FOR UPDATE NOWAIT")
            .bind(sid)
            .fetch_optional(&mut *c)
            .await
            .map_err(mfa_lock_error)?
            .ok_or(IdentityError::Unauthorized)?;
        // Acquire the remaining mutable authority rows NOWAIT before
        // session() takes shared locks, avoiding a later lock upgrade cycle.
        let locked = sqlx::query(
            "SELECT u.id FROM core_identity.users u
             JOIN core_identity.accounts a ON a.id=u.personal_account_id
             JOIN core_identity.credentials c ON c.user_id=u.id
             JOIN core_identity.web_sessions s ON s.current_credential_id=c.id
             JOIN core_identity.user_profiles pr ON pr.user_id=u.id
             WHERE u.id=$1 AND c.id=$2 AND s.sid=$3
             FOR UPDATE OF u,c,s,pr NOWAIT FOR SHARE OF a NOWAIT",
        )
        .bind(before.user_id)
        .bind(before.credential_id)
        .bind(sid)
        .fetch_optional(&mut *c)
        .await
        .map_err(mfa_lock_error)?;
        if locked.is_none() {
            return Err(IdentityError::Unauthorized.into());
        }
        // Fresh statement snapshots see a winning rotation or generation
        // change. Core session authorization also checks persisted assurance.
        let current = session(c, access).await?;
        let family = lock_family(c, sid).await.map_err(mfa_browser_error)?;
        require_family_live(&family).map_err(mfa_browser_error)?;
        if current.user_id != before.user_id
            || current.credential_id != before.credential_id
            || current.user_version != before.user_version
            || family.try_get::<i64, _>("current_credential_id")? != current.credential_id
        {
            return Err(IdentityError::Unauthorized.into());
        }
        Ok(current)
    }

    async fn rotate_mfa_family(
        &self,
        c: &mut PgConnection,
        actor: &Principal,
        sid: &str,
        assurance: &mfa::Assurance,
        action: &str,
    ) -> MfaResult<BrowserBundle> {
        let refresh = new_secret("lmmr_")?;
        let access = self.derived_secret(b"browser-access-v1", sid, &refresh, "lmms_")?;
        let issued =
            auth::issue_session_with_assurance(c, actor.user_id, action, access.clone(), assurance)
                .await?;
        sqlx::query(
            "UPDATE core_identity.credentials
             SET expires_at=LEAST(expires_at,clock_timestamp()+interval '15 minutes',
                 (SELECT expires_at FROM core_identity.web_sessions WHERE sid=$2))
             WHERE id=$1",
        )
        .bind(issued.id)
        .bind(sid)
        .execute(&mut *c)
        .await?;
        sqlx::query(
            "UPDATE core_identity.credentials SET revoked_at=clock_timestamp() WHERE id=$1",
        )
        .bind(actor.credential_id)
        .execute(&mut *c)
        .await?;
        // Factor helpers advance the user generation exactly once. Keep only
        // this family current, retain its deadlines and deny every old refresh
        // secret immediately, including one in the ordinary grace window.
        sqlx::query(
            "UPDATE core_identity.web_sessions
             SET user_version=$2,current_credential_id=$3,refresh_digest=$4,
                 previous_refresh_digest=NULL,previous_valid_until=NULL,
                 last_active_at=clock_timestamp()
             WHERE sid=$1",
        )
        .bind(sid)
        .bind(assurance.version())
        .bind(issued.id)
        .bind(digest(&refresh)?)
        .execute(&mut *c)
        .await?;
        sqlx::query(
            "INSERT INTO core_identity.web_session_credentials(credential_id,sid) VALUES ($1,$2)",
        )
        .bind(issued.id)
        .bind(sid)
        .execute(&mut *c)
        .await?;
        audit(
            c,
            actor,
            action,
            json!({"user_id":actor.user_id,"user_version":assurance.version(),"session_id":issued.id}),
        )
        .await?;
        make_bundle(
            c,
            sid,
            &access,
            format!("{sid}.{refresh}"),
            self.store.financial_profile_enabled,
        )
        .await
        .map_err(mfa_browser_error)
    }
}

fn mfa_browser_error(error: BrowserAuthError) -> MfaError {
    match error {
        BrowserAuthError::Identity(error) => error.into(),
        BrowserAuthError::SessionMismatch
        | BrowserAuthError::RefreshRace
        | BrowserAuthError::SessionRevoked => IdentityError::Unauthorized.into(),
    }
}

fn mfa_lock_error(error: sqlx::Error) -> MfaError {
    if error.as_database_error().and_then(|e| e.code()).as_deref() == Some("55P03") {
        MfaError::Identity(IdentityError::Conflict)
    } else {
        MfaError::from(error)
    }
}
