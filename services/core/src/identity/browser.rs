//! Browser-session compatibility without importing legacy sessions or funds.
use super::*;
use hmac::{Hmac, Mac};
use subtle::ConstantTimeEq;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum BrowserAuthError {
    Identity(IdentityError),
    SessionMismatch,
    RefreshRace,
    SessionRevoked,
}
impl From<IdentityError> for BrowserAuthError {
    fn from(error: IdentityError) -> Self {
        Self::Identity(error)
    }
}
impl From<sqlx::Error> for BrowserAuthError {
    fn from(error: sqlx::Error) -> Self {
        Self::Identity(error.into())
    }
}
type BrowserResult<T> = std::result::Result<T, BrowserAuthError>;

/// Secrets are intentionally not Debug or serialized.
pub struct BrowserBundle {
    pub data: Value,
    pub refresh_token: String,
    pub expires_at: i64,
}
pub struct BrowserRegistration<'a> {
    pub username: &'a str,
    pub password: &'a str,
    pub email: &'a str,
}
#[derive(Clone, Default)]
pub struct BrowserSessionMetadata {
    pub ip: String,
    pub user_agent: String,
}
#[derive(Clone)]
pub struct BrowserAuth {
    store: IdentityStore,
    key: std::sync::Arc<[u8]>,
}
impl BrowserAuth {
    pub fn new(store: IdentityStore, refresh_key: &[u8]) -> Result<Self> {
        if !(32..=128).contains(&refresh_key.len()) {
            return Err(IdentityError::Invalid);
        }
        Ok(Self {
            store,
            key: refresh_key.into(),
        })
    }
    fn derived_secret(
        &self,
        domain: &[u8],
        sid: &str,
        secret: &str,
        prefix: &str,
    ) -> Result<String> {
        let mut mac =
            Hmac::<Sha256>::new_from_slice(&self.key).map_err(|_| IdentityError::Storage)?;
        mac.update(domain);
        mac.update(b"\0");
        mac.update(sid.as_bytes());
        mac.update(b"\0");
        mac.update(secret.as_bytes());
        let mut value = prefix.to_owned();
        for byte in mac.finalize().into_bytes() {
            use std::fmt::Write;
            write!(&mut value, "{byte:02x}").map_err(|_| IdentityError::Storage)?;
        }
        Ok(value)
    }
    pub async fn register(&self, input: &BrowserRegistration<'_>) -> BrowserResult<()> {
        if !self.store.registration_enabled {
            return Err(IdentityError::Forbidden.into());
        }
        let username = input.username.trim();
        let email = input.email.trim();
        if username.is_empty()
            || username.chars().count() > 20
            || username.len() > 128
            || !(8..=20).contains(&input.password.chars().count())
            || input.password.len() > 1024
            || email.len() > 320
            || email.chars().any(char::is_control)
            || (!email.is_empty() && !email.contains('@'))
        {
            return Err(IdentityError::Invalid.into());
        }
        let hash = self.store.hash_password(input.password.to_owned()).await?;
        let mut tx = self.store.pool.begin().await?;
        let user = auth::create_user(&mut tx).await?;
        let conflict: bool = sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM core_identity.user_profiles WHERE username=$1 OR (email_login_allowed AND email<>'' AND email=$1))")
            .bind(username).fetch_one(&mut *tx).await?;
        if conflict {
            return Err(IdentityError::Conflict.into());
        }
        let canonical = format!("web_{}", &new_secret("")?[..32]);
        sqlx::query("INSERT INTO core_identity.password_logins(login_name,user_id,password_hash) VALUES ($1,$2,$3)").bind(canonical).bind(user).bind(hash).execute(&mut *tx).await?;
        sqlx::query("INSERT INTO core_identity.user_profiles(user_id,username,display_name,email) VALUES ($1,$2,$2,$3)").bind(user).bind(username).bind("").execute(&mut *tx).await?;
        sqlx::query("INSERT INTO core_identity.audit(actor_user_id,action,target) VALUES ($1,'user.register',jsonb_build_object('user_id',$1::bigint))").bind(user).execute(&mut *tx).await?;
        tx.commit().await?;
        Ok(())
    }
    pub async fn login(
        &self,
        username: &str,
        password: &str,
        metadata: &BrowserSessionMetadata,
    ) -> BrowserResult<BrowserBundle> {
        let username = username.trim();
        if username.is_empty()
            || username.len() > 320
            || metadata.ip.len() > 128
            || metadata.user_agent.len() > 1024
        {
            return Err(IdentityError::Unauthorized.into());
        }
        let names:Vec<String>=sqlx::query_scalar("SELECT p.login_name FROM core_identity.password_logins p JOIN core_identity.user_profiles pr ON pr.user_id=p.user_id WHERE pr.username=$1 OR (pr.email_login_allowed AND pr.email<>'' AND pr.email=$1) LIMIT 2").bind(username).fetch_all(&self.store.pool).await?;
        // Still perform a bounded dummy hash for unknown and ambiguous identities.
        let canonical = if names.len() == 1 {
            names[0].clone()
        } else {
            format!("missing_{}", &new_secret("")?[..32])
        };
        let verified = self.store.verified_password(&canonical, password).await?;
        if names.len() != 1 {
            return Err(IdentityError::Unauthorized.into());
        }
        let mut tx = self.store.pool.begin().await?;
        let user = self
            .store
            .lock_password_user(&mut tx, &canonical, &verified)
            .await?;
        let sid = new_secret("")?;
        let refresh = new_secret("lmmr_")?;
        let access = self.derived_secret(b"browser-access-v1", &sid, &refresh, "lmms_")?;
        let credential =
            auth::issue_session_with_secret(&mut tx, user, "session.web_login", access.clone())
                .await?;
        sqlx::query("UPDATE core_identity.credentials SET expires_at=LEAST(expires_at,clock_timestamp()+interval '15 minutes') WHERE id=$1").bind(credential.id).execute(&mut *tx).await?;
        let setting: String =
            sqlx::query_scalar("SELECT setting FROM core_identity.user_profiles WHERE user_id=$1")
                .bind(user)
                .fetch_one(&mut *tx)
                .await?;
        let weekly = serde_json::from_str::<Value>(&setting)
            .ok()
            .and_then(|v| v.get("session_auto_logout").and_then(Value::as_bool))
            .unwrap_or(true);
        sqlx::query("INSERT INTO core_identity.web_sessions(sid,user_id,user_version,current_credential_id,refresh_digest,ip,user_agent,weekly_logout) SELECT $1,$2,auth_version,$3,$4,$5,$6,$7 FROM core_identity.users WHERE id=$2").bind(&sid).bind(user).bind(credential.id).bind(digest(&refresh)?).bind(&metadata.ip).bind(&metadata.user_agent).bind(weekly).execute(&mut *tx).await?;
        let bundle = make_bundle(&mut tx, &sid, &access, format!("{sid}.{refresh}")).await?;
        tx.commit().await?;
        Ok(bundle)
    }
    /// A previous token inside the race window deterministically recovers the
    /// same response. Unknown secrets never revoke an otherwise valid family.
    pub async fn refresh(
        &self,
        token: &str,
        expected_sid: Option<&str>,
    ) -> BrowserResult<BrowserBundle> {
        let (sid, presented) = parse_refresh(token)?;
        if expected_sid.is_some_and(|expected| expected != sid) {
            return Err(BrowserAuthError::SessionMismatch);
        }
        let mut tx = self.store.pool.begin().await?;
        let row = lock_family(&mut tx, sid).await?;
        require_family_live(&row)?;
        let hash = digest(presented)?;
        let current: Vec<u8> = row.try_get("refresh_digest")?;
        let previous: Option<Vec<u8>> = row.try_get("previous_refresh_digest")?;
        let next = self.derived_secret(b"browser-refresh-v1", sid, presented, "lmmr_")?;
        if constant_digest(&current, &hash) {
            let access = self.derived_secret(b"browser-access-v1", sid, &next, "lmms_")?;
            let old: i64 = row.try_get("current_credential_id")?;
            sqlx::query("UPDATE core_identity.credentials SET revoked_at=clock_timestamp() WHERE id=$1 AND revoked_at IS NULL").bind(old).execute(&mut *tx).await?;
            let user: i64 = row.try_get("user_id")?;
            let issued = auth::issue_session_with_secret(
                &mut tx,
                user,
                "session.web_refresh",
                access.clone(),
            )
            .await?;
            sqlx::query("UPDATE core_identity.credentials SET expires_at=LEAST(expires_at,clock_timestamp()+interval '15 minutes',(SELECT expires_at FROM core_identity.web_sessions WHERE sid=$2)) WHERE id=$1").bind(issued.id).bind(sid).execute(&mut *tx).await?;
            sqlx::query("UPDATE core_identity.invites SET inviter_credential_id=$1 WHERE inviter_credential_id=$2 AND accepted_at IS NULL AND rejected_at IS NULL AND withdrawn_at IS NULL AND expires_at>clock_timestamp()")
                .bind(issued.id).bind(old).execute(&mut *tx).await?;
            sqlx::query("UPDATE core_identity.web_sessions SET previous_refresh_digest=refresh_digest,previous_valid_until=clock_timestamp()+interval '30 seconds',refresh_digest=$2,current_credential_id=$3,last_active_at=clock_timestamp() WHERE sid=$1").bind(sid).bind(digest(&next)?).bind(issued.id).execute(&mut *tx).await?;
            let bundle = make_bundle(&mut tx, sid, &access, format!("{sid}.{next}")).await?;
            tx.commit().await?;
            Ok(bundle)
        } else if previous
            .as_ref()
            .is_some_and(|previous| constant_digest(previous, &hash))
        {
            if !row.try_get::<bool, _>("previous_live")? {
                revoke_family(&mut tx, sid).await?;
                tx.commit().await?;
                return Err(BrowserAuthError::SessionRevoked);
            }
            if !constant_digest(&current, &digest(&next)?) {
                return Err(BrowserAuthError::RefreshRace);
            }
            let access = self.derived_secret(b"browser-access-v1", sid, &next, "lmms_")?;
            let bundle = make_bundle(&mut tx, sid, &access, format!("{sid}.{next}")).await?;
            tx.commit().await?;
            Ok(bundle)
        } else {
            Err(IdentityError::Unauthorized.into())
        }
    }
    pub async fn logout(
        &self,
        cookie: Option<&str>,
        access: Option<&str>,
        expected_sid: Option<&str>,
    ) -> BrowserResult<()> {
        if let Some(cookie) = cookie {
            let (sid, secret) = parse_refresh(cookie)?;
            if expected_sid.is_some_and(|expected| expected != sid) {
                return Err(BrowserAuthError::SessionMismatch);
            }
            let mut tx = self.store.pool.begin().await?;
            let row = match lock_family(&mut tx, sid).await {
                Ok(row) => row,
                Err(BrowserAuthError::Identity(IdentityError::Unauthorized)) => return Ok(()),
                Err(error) => return Err(error),
            };
            let hash = digest(secret)?;
            let current: Vec<u8> = row.try_get("refresh_digest")?;
            let previous: Option<Vec<u8>> = row.try_get("previous_refresh_digest")?;
            let valid = constant_digest(&current, &hash)
                || (row.try_get::<bool, _>("previous_live")?
                    && previous.as_ref().is_some_and(|p| constant_digest(p, &hash)));
            if !valid {
                return Err(IdentityError::Unauthorized.into());
            }
            revoke_family(&mut tx, sid).await?;
            tx.commit().await?;
            return Ok(());
        }
        if let Some(access) = access {
            let mut tx = self.store.pool.begin().await?;
            // Resolve only the candidate family without taking credential
            // locks. All browser mutations lock family before credentials.
            let sid: Option<String> = sqlx::query_scalar("SELECT s.sid FROM core_identity.web_sessions s JOIN core_identity.credentials c ON c.id=s.current_credential_id WHERE c.digest=$1")
                .bind(digest(access)?).fetch_optional(&mut *tx).await?;
            let Some(sid) = sid else {
                return Err(IdentityError::Unauthorized.into());
            };
            let _ = lock_family(&mut tx, &sid).await?;
            let actor = match session(&mut tx, access).await {
                Ok(actor) => actor,
                Err(IdentityError::Unauthorized) => return Ok(()),
                Err(error) => return Err(error.into()),
            };
            let sid:Option<String>=sqlx::query_scalar("SELECT sid FROM core_identity.web_sessions WHERE current_credential_id=$1 FOR UPDATE").bind(actor.credential_id).fetch_optional(&mut *tx).await?;
            let Some(sid) = sid else {
                return Err(IdentityError::Forbidden.into());
            };
            if expected_sid.is_some_and(|expected| expected != sid) {
                return Err(BrowserAuthError::SessionMismatch);
            }
            revoke_family(&mut tx, &sid).await?;
            tx.commit().await?;
        }
        Ok(())
    }
    pub async fn current_user(&self, access: &str) -> BrowserResult<Value> {
        let mut tx = self.store.pool.begin().await?;
        let actor = session(&mut tx, access).await?;
        let valid:bool=sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM core_identity.web_sessions WHERE current_credential_id=$1 AND revoked_at IS NULL)").bind(actor.credential_id).fetch_one(&mut *tx).await?;
        if !valid {
            return Err(IdentityError::Forbidden.into());
        }
        let user = user_projection(&mut tx, actor.user_id).await?;
        tx.commit().await?;
        Ok(user)
    }
}
fn constant_digest(a: &[u8], b: &[u8]) -> bool {
    a.ct_eq(b).into()
}
fn parse_refresh(token: &str) -> BrowserResult<(&str, &str)> {
    let (sid, secret) = token.split_once('.').ok_or(IdentityError::Unauthorized)?;
    if sid.len() != 64
        || !sid
            .bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
        || !secret.starts_with("lmmr_")
        || secret.len() != 69
    {
        return Err(IdentityError::Unauthorized.into());
    }
    digest(secret)?;
    Ok((sid, secret))
}
async fn lock_family(c: &mut PgConnection, sid: &str) -> BrowserResult<PgRow> {
    // Lock the stable family first, then use a new statement snapshot for the
    // rotated credential. A joined FOR UPDATE can retain stale join rows after
    // waiting for the winning refresher under PostgreSQL READ COMMITTED.
    sqlx::query("SELECT sid FROM core_identity.web_sessions WHERE sid=$1 FOR UPDATE")
        .bind(sid)
        .fetch_optional(&mut *c)
        .await?
        .ok_or(IdentityError::Unauthorized)?;
    Ok(sqlx::query("SELECT s.*,COALESCE(s.previous_valid_until>=clock_timestamp(),FALSE) AS previous_live,s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND (NOT s.weekly_logout OR s.created_at>clock_timestamp()-interval '7 days') AND u.active AND a.active AND u.auth_version=s.user_version AND c.revoked_at IS NULL AND c.user_version=u.auth_version AS live FROM core_identity.web_sessions s JOIN core_identity.users u ON u.id=s.user_id JOIN core_identity.accounts a ON a.id=u.personal_account_id JOIN core_identity.credentials c ON c.id=s.current_credential_id WHERE s.sid=$1 FOR SHARE OF u,a FOR UPDATE OF c").bind(sid).fetch_optional(c).await?.ok_or(IdentityError::Unauthorized)?)
}
fn require_family_live(row: &PgRow) -> BrowserResult<()> {
    if !row.try_get::<bool, _>("live")? {
        return Err(BrowserAuthError::SessionRevoked);
    }
    Ok(())
}
async fn revoke_family(c: &mut PgConnection, sid: &str) -> BrowserResult<()> {
    sqlx::query("UPDATE core_identity.credentials SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=(SELECT current_credential_id FROM core_identity.web_sessions WHERE sid=$1)").bind(sid).execute(&mut *c).await?;
    sqlx::query("UPDATE core_identity.web_sessions SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE sid=$1").bind(sid).execute(&mut *c).await?;
    Ok(())
}
async fn user_projection(c: &mut PgConnection, user: i64) -> BrowserResult<Value> {
    let r=sqlx::query("SELECT u.id,u.active,u.platform_role,p.username,p.display_name,p.email,p.setting FROM core_identity.users u JOIN core_identity.user_profiles p ON p.user_id=u.id WHERE u.id=$1").bind(user).fetch_one(c).await?;
    let role = match r.try_get::<String, _>("platform_role")?.as_str() {
        "user" => 1,
        "admin" => 10,
        "superadmin" => 100,
        _ => return Err(IdentityError::Storage.into()),
    };
    Ok(
        json!({"id":r.try_get::<i64,_>("id")?,"username":r.try_get::<String,_>("username")?,"display_name":r.try_get::<String,_>("display_name")?,"email":r.try_get::<String,_>("email")?,"setting":r.try_get::<String,_>("setting")?,"role":role,"status":if r.try_get::<bool,_>("active")? {1}else{2},"usage_projection_available":false}),
    )
}
async fn make_bundle(
    c: &mut PgConnection,
    sid: &str,
    access: &str,
    refresh: String,
) -> BrowserResult<BrowserBundle> {
    let r=sqlx::query("SELECT s.*,extract(epoch FROM s.created_at)::bigint AS created,extract(epoch FROM s.last_active_at)::bigint AS active,extract(epoch FROM s.expires_at)::bigint AS expires,extract(epoch FROM c.expires_at)::bigint AS access_expires FROM core_identity.web_sessions s JOIN core_identity.credentials c ON c.id=s.current_credential_id WHERE s.sid=$1").bind(sid).fetch_one(&mut *c).await?;
    let expires: i64 = r.try_get("expires")?;
    let user = user_projection(c, r.try_get("user_id")?).await?;
    Ok(BrowserBundle {
        data: json!({"access_token":access,"token_type":"Bearer","access_expires_at":r.try_get::<i64,_>("access_expires")?,"user":user,"session":{"sid":sid,"current":true,"login_method":"password","ip":r.try_get::<String,_>("ip")?,"user_agent":r.try_get::<String,_>("user_agent")?,"created_at":r.try_get::<i64,_>("created")?,"last_active_at":r.try_get::<i64,_>("active")?,"expires_at":expires}}),
        refresh_token: refresh,
        expires_at: expires,
    })
}
