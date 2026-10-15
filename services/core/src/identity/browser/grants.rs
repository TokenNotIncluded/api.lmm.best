//! Cookie identity for explicit native-client consent; never rotates a session.
use super::*;

pub(in crate::identity) struct OAuthBrowserIdentity {
    pub(in crate::identity) user: i64,
    pub(in crate::identity) account: i64,
    pub(in crate::identity) version: i64,
    pub(in crate::identity) sid: String,
}
impl BrowserAuth {
    pub(in crate::identity) async fn oauth_identity_in_transaction(
        c: &mut PgConnection,
        cookie: &str,
    ) -> Result<OAuthBrowserIdentity> {
        let error = |error: BrowserAuthError| match error {
            BrowserAuthError::Identity(error) => error,
            _ => IdentityError::Unauthorized,
        };
        let (sid, secret) = parse_refresh(cookie).map_err(error)?;
        let row = lock_family(c, sid).await.map_err(error)?;
        require_family_live(&row).map_err(error)?;
        let presented = digest(secret)?;
        let current: Vec<u8> = row.try_get("refresh_digest")?;
        let previous: Option<Vec<u8>> = row.try_get("previous_refresh_digest")?;
        if !constant_digest(&current, &presented)
            && !(row.try_get::<bool, _>("previous_live")?
                && previous
                    .as_ref()
                    .is_some_and(|old| constant_digest(old, &presented)))
        {
            return Err(IdentityError::Unauthorized);
        }
        // lock_family has already verified persisted assurance on the current
        // credential. Stable SID/version survive normal refresh rotation.
        let user = row.try_get("user_id")?;
        let account =
            sqlx::query_scalar("SELECT personal_account_id FROM core_identity.users WHERE id=$1")
                .bind(user)
                .fetch_one(c)
                .await?;
        Ok(OAuthBrowserIdentity {
            user,
            account,
            version: row.try_get("user_version")?,
            sid: sid.into(),
        })
    }
}
