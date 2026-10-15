//! Read-only OAuth authority and explicit native/opening USD provenance.
use super::issuer::{LmmIssuer, scope_groups};
use super::protocol;
use super::*;

#[derive(Clone, Copy, Debug)]
pub enum ReadError {
    Identity(IdentityError),
    Unavailable,
}
impl From<IdentityError> for ReadError {
    fn from(error: IdentityError) -> Self {
        Self::Identity(error)
    }
}
impl From<sqlx::Error> for ReadError {
    fn from(_: sqlx::Error) -> Self {
        Self::Identity(IdentityError::Storage)
    }
}
impl From<crate::oauth2_balance::BalanceError> for ReadError {
    fn from(_: crate::oauth2_balance::BalanceError) -> Self {
        Self::Unavailable
    }
}
/// Private purpose-limited authority; never a Principal or spend credential.
struct ReadAuthority {
    user: i64,
    account: i64,
    groups: Vec<String>,
    grant_id: String,
    client_id: String,
}
pub use crate::oauth2_balance::BalanceV2;
impl LmmIssuer {
    async fn authorize_read(
        &self,
        c: &mut PgConnection,
        access: &str,
        scope: &str,
    ) -> std::result::Result<ReadAuthority, ReadError> {
        protocol::canonical(access, "lmm_at_").map_err(|_| IdentityError::Unauthorized)?;
        let hash = digest(access)?;
        let token = sqlx::query(
            "SELECT family_id,consent_version,scopes FROM core_oauth.access_tokens WHERE digest=$1",
        )
        .bind(&hash)
        .fetch_optional(&mut *c)
        .await?
        .ok_or(IdentityError::Unauthorized)?;
        let f = family(c, &token.try_get::<String, _>("family_id")?, false).await?;
        let current = self.live(c, &f, token.try_get("consent_version")?).await?;
        let snapshot: Vec<String> = token.try_get("scopes")?;
        let allowed: Vec<String> = f.try_get("scopes")?;
        let groups: Vec<String> = scope_groups(&snapshot)?
            .into_iter()
            .filter(|group| current.contains(group))
            .collect();
        if !snapshot.iter().any(|s| s == scope)
            || !allowed.iter().any(|s| s == scope)
            || groups.is_empty()
        {
            return Err(IdentityError::Forbidden.into());
        }
        let live: bool = sqlx::query_scalar(
            "SELECT expires_at>clock_timestamp() FROM core_oauth.access_tokens WHERE digest=$1",
        )
        .bind(hash)
        .fetch_one(c)
        .await?;
        if !live {
            return Err(IdentityError::Unauthorized.into());
        }
        Ok(ReadAuthority {
            user: f.try_get("user_id")?,
            account: f.try_get("owner_account_id")?,
            groups,
            grant_id: f.try_get("id")?,
            client_id: f.try_get("client_id")?,
        })
    }
    pub async fn usage(&self, access: &str) -> std::result::Result<serde_json::Value, ReadError> {
        let mut tx = transaction(&self.identity.pool).await?;
        let actor = self.authorize_read(&mut tx, access, "usage:read").await?;
        if actor.client_id != "lmm-pi" {
            return Err(IdentityError::Forbidden.into());
        }
        let result =
            crate::usage::read_oauth_usage(&mut tx, actor.user, actor.account, &actor.grant_id)
                .await
                .map_err(|_| ReadError::Unavailable)?;
        tx.commit().await?;
        Ok(result)
    }
    pub async fn balance(&self, access: &str) -> std::result::Result<BalanceV2, ReadError> {
        let mut tx = transaction(&self.identity.pool).await?;
        let actor = self.authorize_read(&mut tx, access, "balance:read").await?;
        let result =
            crate::oauth2_balance::read_balance(&mut tx, actor.user, actor.account).await?;
        tx.commit().await?;
        Ok(result)
    }
    pub async fn catalog(
        &self,
        access: &str,
    ) -> std::result::Result<crate::model_http::CatalogV1, ReadError> {
        let mut tx = transaction(&self.identity.pool).await?;
        let actor = self.authorize_read(&mut tx, access, "catalog:read").await?;
        let surface = self
            .catalog_surface
            .as_ref()
            .ok_or(ReadError::Unavailable)?;
        let updated_at: i64 =
            sqlx::query_scalar("SELECT floor(extract(epoch FROM clock_timestamp()))::bigint")
                .fetch_one(&mut *tx)
                .await?;
        let updated_at = u64::try_from(updated_at).map_err(|_| ReadError::Unavailable)?;
        let result = surface
            .project(&actor.groups, self.profile.resource(), updated_at)
            .map_err(|_| ReadError::Unavailable)?;
        tx.commit().await?;
        Ok(result)
    }
}
