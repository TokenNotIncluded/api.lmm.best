//! Manage only the authenticated user's native browser families.
use super::*;

fn busy(error: sqlx::Error) -> IdentityError {
    if error.as_database_error().and_then(|e| e.code()).as_deref() == Some("55P03") {
        IdentityError::Conflict
    } else {
        error.into()
    }
}

impl BrowserAuth {
    pub async fn login_sessions(&self, access: &str) -> BrowserResult<Value> {
        let mut tx = self.store.pool.begin().await?;
        let actor = session(&mut tx, access).await?;
        let current: String = sqlx::query_scalar("SELECT sid FROM core_identity.web_sessions WHERE user_id=$1 AND current_credential_id=$2 AND revoked_at IS NULL")
            .bind(actor.user_id).bind(actor.credential_id).fetch_optional(&mut *tx).await?.ok_or(IdentityError::Forbidden)?;
        let rows = sqlx::query("SELECT s.sid,s.ip,s.user_agent,extract(epoch FROM s.created_at)::bigint AS created,extract(epoch FROM s.last_active_at)::bigint AS active,extract(epoch FROM LEAST(s.expires_at,CASE WHEN s.weekly_logout THEN s.created_at+interval '7 days' ELSE s.expires_at END))::bigint AS expires,EXISTS(SELECT 1 FROM core_identity.web_session_credentials m JOIN core_identity.audit a ON a.target->>'session_id'=m.credential_id::text WHERE m.sid=s.sid AND a.action='session.google_browser_login') AS google_login FROM core_identity.web_sessions s JOIN core_identity.credentials c ON c.id=s.current_credential_id WHERE s.user_id=$1 AND s.user_version=$2 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND (NOT s.weekly_logout OR s.created_at>clock_timestamp()-interval '7 days') AND c.revoked_at IS NULL ORDER BY s.last_active_at DESC,s.sid")
            .bind(actor.user_id).bind(actor.user_version).fetch_all(&mut *tx).await?;
        let mut data = Vec::new();
        for row in rows {
            let sid: String = row.try_get("sid")?;
            let ip: String = row.try_get("ip")?;
            let user_agent: String = row.try_get("user_agent")?;
            data.push(json!({"current":sid==current,"sid":sid,"login_method":if row.try_get::<bool,_>("google_login")? {"google"} else {"password"},"ip":(!ip.is_empty()).then_some(ip),"user_agent":(!user_agent.is_empty()).then_some(user_agent),"created_at":row.try_get::<i64,_>("created")?,"last_active_at":row.try_get::<i64,_>("active")?,"expires_at":row.try_get::<i64,_>("expires")?}));
        }
        let raw: String =
            sqlx::query_scalar("SELECT setting FROM core_identity.user_profiles WHERE user_id=$1")
                .bind(actor.user_id)
                .fetch_one(&mut *tx)
                .await?;
        let settings: Value = serde_json::from_str(&raw).map_err(|_| IdentityError::Storage)?;
        let weekly = settings
            .get("session_auto_logout")
            .and_then(Value::as_bool)
            .unwrap_or(true);
        tx.commit().await?;
        Ok(json!({"success":true,"data":data,"session_auto_logout":weekly}))
    }

    /// A single SID revocation returns whether the current family was revoked.
    /// None means revoke all other families, preserving the current family.
    pub async fn revoke_login_sessions(
        &self,
        access: &str,
        target: Option<&str>,
        expected: Option<&str>,
    ) -> BrowserResult<(bool, usize)> {
        if target.is_some_and(|sid| {
            sid.len() != 64
                || !sid
                    .bytes()
                    .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
        }) {
            return Err(IdentityError::Invalid.into());
        }
        let initial = self.store.authorize(access).await?;
        let mut tx = self.store.pool.begin().await?;
        let (actor, current) = self
            .lock_session_management(&mut tx, access, &initial)
            .await?;
        if expected.is_some_and(|sid| sid != current) {
            return Err(BrowserAuthError::SessionMismatch);
        }
        if let Some(sid) = target {
            let owned: bool = sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM core_identity.web_sessions WHERE sid=$1 AND user_id=$2)").bind(sid).bind(actor.user_id).fetch_one(&mut *tx).await?;
            if !owned {
                return Err(IdentityError::Forbidden.into());
            }
        }
        let targets: Vec<String> = sqlx::query_scalar("SELECT sid FROM core_identity.web_sessions WHERE user_id=$1 AND revoked_at IS NULL AND (($2::text IS NOT NULL AND sid=$2) OR ($2::text IS NULL AND sid<>$3)) ORDER BY sid")
            .bind(actor.user_id).bind(target).bind(&current).fetch_all(&mut *tx).await?;
        for sid in &targets {
            revoke_family(&mut tx, sid).await?;
        }
        audit(
            &mut tx,
            &actor,
            "session.browser_revoke",
            json!({"sids":targets,"others":target.is_none()}),
        )
        .await?;
        tx.commit().await?;
        Ok((target == Some(current.as_str()), targets.len()))
    }

    pub async fn update_login_session_settings(
        &self,
        access: &str,
        weekly: bool,
        expected: Option<&str>,
    ) -> BrowserResult<()> {
        let initial = self.store.authorize(access).await?;
        let mut tx = self.store.pool.begin().await?;
        let (actor, current) = self
            .lock_session_management(&mut tx, access, &initial)
            .await?;
        if expected.is_some_and(|sid| sid != current) {
            return Err(BrowserAuthError::SessionMismatch);
        }
        let raw: String = sqlx::query_scalar(
            "SELECT setting FROM core_identity.user_profiles WHERE user_id=$1 FOR UPDATE NOWAIT",
        )
        .bind(actor.user_id)
        .fetch_one(&mut *tx)
        .await
        .map_err(busy)?;
        let mut settings: Value = serde_json::from_str(&raw).map_err(|_| IdentityError::Storage)?;
        if !settings.is_object() {
            return Err(IdentityError::Storage.into());
        }
        // An elapsed weekly deadline is terminal. Opt-out must never revive
        // such a family; opt-in permanently revokes already week-old logins.
        let elapsed: Vec<String> = sqlx::query_scalar("SELECT sid FROM core_identity.web_sessions WHERE user_id=$1 AND revoked_at IS NULL AND (weekly_logout OR $2) AND created_at<=clock_timestamp()-interval '7 days' ORDER BY sid")
            .bind(actor.user_id).bind(weekly).fetch_all(&mut *tx).await?;
        for sid in &elapsed {
            revoke_family(&mut tx, sid).await?;
        }
        settings["session_auto_logout"] = json!(weekly);
        sqlx::query("UPDATE core_identity.user_profiles SET setting=$2 WHERE user_id=$1")
            .bind(actor.user_id)
            .bind(settings.to_string())
            .execute(&mut *tx)
            .await?;
        sqlx::query("UPDATE core_identity.web_sessions SET weekly_logout=$2 WHERE user_id=$1 AND revoked_at IS NULL").bind(actor.user_id).bind(weekly).execute(&mut *tx).await?;
        audit(
            &mut tx,
            &actor,
            "session.browser_settings",
            json!({"session_auto_logout":weekly,"revoked_sids":elapsed}),
        )
        .await?;
        tx.commit().await?;
        Ok(())
    }

    async fn lock_session_management(
        &self,
        tx: &mut PgConnection,
        access: &str,
        initial: &Principal,
    ) -> BrowserResult<(Principal, String)> {
        if initial.credential_kind != CredentialKind::Session {
            return Err(IdentityError::Forbidden.into());
        }
        // User exclusion serializes new logins too. NOWAIT on each mutable
        // authority row avoids cycles with refresh, password and MFA operations.
        sqlx::query("SELECT id FROM core_identity.users WHERE id=$1 FOR UPDATE NOWAIT")
            .bind(initial.user_id)
            .fetch_one(&mut *tx)
            .await
            .map_err(busy)?;
        sqlx::query("SELECT sid FROM core_identity.web_sessions WHERE user_id=$1 ORDER BY sid FOR UPDATE NOWAIT").bind(initial.user_id).fetch_all(&mut *tx).await.map_err(busy)?;
        sqlx::query("SELECT c.id FROM core_identity.credentials c JOIN core_identity.web_sessions s ON s.current_credential_id=c.id WHERE s.user_id=$1 ORDER BY c.id FOR UPDATE OF c NOWAIT").bind(initial.user_id).fetch_all(&mut *tx).await.map_err(busy)?;
        let actor = session(tx, access).await?;
        if actor.credential_id != initial.credential_id
            || actor.user_version != initial.user_version
        {
            return Err(IdentityError::Unauthorized.into());
        }
        let current: String = sqlx::query_scalar("SELECT sid FROM core_identity.web_sessions WHERE user_id=$1 AND current_credential_id=$2 AND revoked_at IS NULL").bind(actor.user_id).bind(actor.credential_id).fetch_optional(&mut *tx).await?.ok_or(IdentityError::Forbidden)?;
        Ok((actor, current))
    }
}
