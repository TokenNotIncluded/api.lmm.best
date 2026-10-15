//! Purpose-limited delegated model authority, never a native key or Session.
use super::*;
use serde::{Deserialize, Serialize};

/// Only Core verification constructs this proof. Deserialize supports checking
/// Core's immutable stored receipts; no public HTTP/Go input is trusted as proof.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct OAuthSource {
    pub(crate) grant_id: String,
    pub(crate) access_id: String,
    pub(crate) client_id: String,
    pub(crate) issuer: String,
    pub(crate) resource: String,
    pub(crate) user_id: i64,
    pub(crate) personal_account_id: i64,
    pub(crate) user_version: i64,
    pub(crate) consent_version: i64,
    pub(crate) scopes: Vec<String>,
    pub(crate) expires_at: i64,
    pub(crate) budget_credits: i64,
}
impl OAuthSource {
    pub fn grant_id(&self) -> &str {
        &self.grant_id
    }
    pub fn access_id(&self) -> &str {
        &self.access_id
    }
    pub fn client_id(&self) -> &str {
        &self.client_id
    }
    pub fn user_id(&self) -> i64 {
        self.user_id
    }
    pub fn personal_account_id(&self) -> i64 {
        self.personal_account_id
    }
    pub fn groups(&self) -> Result<Vec<String>> {
        issuer::scope_groups(&self.scopes)
    }
    pub(crate) fn validate_stored(&self) -> Result<()> {
        if self.client_id != "lmm-pi"
            || self.user_id <= 0
            || self.personal_account_id <= 0
            || self.user_version <= 0
            || self.consent_version <= 0
            || !(1..=9_007_199_254_740_991).contains(&self.budget_credits)
            || self.expires_at <= 0
            || self.resource != format!("{}/api/oauth2", self.issuer)
            || !self.scopes.iter().any(|s| s == "models:invoke")
        {
            return Err(IdentityError::Storage);
        }
        for (value, prefix) in [(&self.grant_id, "lmm_gr_"), (&self.access_id, "lmm_ac_")] {
            if value.len() != prefix.len() + 64
                || !value.starts_with(prefix)
                || !value[prefix.len()..]
                    .bytes()
                    .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
            {
                return Err(IdentityError::Storage);
            }
        }
        issuer::scope_groups(&self.scopes)?;
        Ok(())
    }
}

impl LmmIssuer {
    /// Actual bearer verification on the caller's financial transaction. The
    /// returned DTO cannot be submitted as a replacement for this operation.
    /// `None` model/group is authenticated admission metadata, never spending.
    pub async fn authorize_model_in_transaction(
        &self,
        c: &mut PgConnection,
        access: &str,
        model: Option<&str>,
        group: Option<&str>,
    ) -> Result<OAuthSource> {
        protocol::canonical(access, "lmm_at_").map_err(|_| IdentityError::Unauthorized)?;
        let ids: Option<(String, String)> =
            sqlx::query_as("SELECT family_id,id FROM core_oauth.access_tokens WHERE digest=$1")
                .bind(digest(access)?)
                .fetch_optional(&mut *c)
                .await?;
        let (grant, id) = ids.ok_or(IdentityError::Unauthorized)?;
        self.model_source(c, &grant, &id, model, group).await
    }
    /// Trusted worker start rechecks the ORIGINAL stored source. No new token,
    /// payer, group, cap or client can be substituted after reservation.
    pub(crate) async fn recheck_model_source(
        &self,
        c: &mut PgConnection,
        source: &OAuthSource,
        model: &str,
        group: &str,
    ) -> Result<()> {
        source.validate_stored()?;
        let current = self
            .model_source(
                c,
                &source.grant_id,
                &source.access_id,
                Some(model),
                Some(group),
            )
            .await?;
        if current.grant_id != source.grant_id
            || current.access_id != source.access_id
            || current.client_id != source.client_id
            || current.issuer != source.issuer
            || current.resource != source.resource
            || current.user_id != source.user_id
            || current.personal_account_id != source.personal_account_id
            || current.user_version != source.user_version
            || current.consent_version != source.consent_version
            || current.budget_credits != source.budget_credits
            || current.expires_at != source.expires_at
        {
            return Err(IdentityError::Unauthorized);
        }
        Ok(())
    }
    async fn model_source(
        &self,
        c: &mut PgConnection,
        grant: &str,
        access: &str,
        model: Option<&str>,
        group: Option<&str>,
    ) -> Result<OAuthSource> {
        let maximum = self
            .profile
            .pi_budget_limit()
            .ok_or(IdentityError::Forbidden)?;
        // All new metered model writes take the same family UPDATE lock as
        // refresh/revoke. No read-before-revoke result is carried across txs.
        let f = family(c, grant, true).await?;
        // Runtime access tokens are append-only: no UPDATE/DELETE privilege is
        // granted just to acquire a row lock. Refresh/revoke serialize on the
        // already locked family; privileged offline token changes must take
        // that same family lock. Check wall-clock expiry again after authority.
        let t=sqlx::query("SELECT *,expires_at>clock_timestamp() AS live,floor(extract(epoch FROM expires_at))::bigint AS expiry FROM core_oauth.access_tokens WHERE id=$1 AND family_id=$2")
            .bind(access).bind(grant).fetch_optional(&mut *c).await?.ok_or(IdentityError::Unauthorized)?;
        let cap: Option<i64> = f.try_get("budget_credits")?;
        if f.try_get::<String, _>("client_id")? != "lmm-pi"
            || f.try_get::<String, _>("issuance_kind")? != "browser"
            || f.try_get::<String, _>("purpose")? != "normal_model"
            || f.try_get::<String, _>("issuer")? != self.profile.issuer()
            || f.try_get::<String, _>("resource")? != self.profile.resource()
            || sqlx::query_scalar::<_, bool>(
                "SELECT revoked_at IS NOT NULL FROM core_oauth.families WHERE id=$1",
            )
            .bind(grant)
            .fetch_one(&mut *c)
            .await?
            || t.try_get::<i64, _>("consent_version")? != f.try_get::<i64, _>("consent_version")?
            || cap.is_none_or(|v| v <= 0 || v > maximum)
        {
            return Err(IdentityError::Unauthorized);
        }
        let token_scopes: Vec<String> = t.try_get("scopes")?;
        let family_scopes: Vec<String> = f.try_get("scopes")?;
        if !token_scopes.iter().any(|s| s == "models:invoke")
            || !family_scopes.iter().any(|s| s == "models:invoke")
        {
            return Err(IdentityError::Forbidden);
        }
        let user = f.try_get("user_id")?;
        let account = f.try_get("owner_account_id")?;
        let version = f.try_get("user_version")?;
        // This locks live user/account and requires REAL developer approval and
        // current group policy. A configured Pi client cannot self-promote L0.
        let current = self.current_model_groups(c, user, account, version).await?;
        let mut effective: Vec<String> = token_scopes
            .into_iter()
            .filter(|s| family_scopes.contains(s))
            .collect();
        effective.retain(|scope| {
            !scope.starts_with("group:")
                || protocol::decode_group(scope).is_ok_and(|group| current.contains(&group))
        });
        let groups = issuer::scope_groups(&effective)?;
        if group.is_some_and(|g| !groups.iter().any(|v| v == g) || !current.iter().any(|v| v == g))
            || groups.iter().all(|g| !current.contains(g))
        {
            return Err(IdentityError::Forbidden);
        }
        if let Some(model) = model {
            if model.is_empty() || model.len() > 128 || model.chars().any(char::is_control) {
                return Err(IdentityError::Invalid);
            }
            let group = group.ok_or(IdentityError::Invalid)?;
            let router = self
                .identity
                .dashboard_routing
                .as_ref()
                .ok_or(IdentityError::Storage)?;
            if !router
                .catalog(group)
                .map_err(|_| IdentityError::Storage)?
                .iter()
                .any(|v| v.name == model)
            {
                return Err(IdentityError::Forbidden);
            }
        }
        // A lock wait cannot authorize an access/family that expired meantime.
        let live:bool=sqlx::query_scalar("SELECT f.revoked_at IS NULL AND f.expires_at>clock_timestamp() AND t.expires_at>clock_timestamp() FROM core_oauth.families f JOIN core_oauth.access_tokens t ON t.family_id=f.id WHERE f.id=$1 AND t.id=$2")
            .bind(grant).bind(access).fetch_one(c).await?;
        if !live {
            return Err(IdentityError::Unauthorized);
        }
        let source = OAuthSource {
            grant_id: grant.into(),
            access_id: access.into(),
            client_id: "lmm-pi".into(),
            issuer: self.profile.issuer().into(),
            resource: self.profile.resource().into(),
            user_id: user,
            personal_account_id: account,
            user_version: version,
            consent_version: f.try_get("consent_version")?,
            scopes: effective,
            expires_at: t.try_get("expiry")?,
            budget_credits: cap.ok_or(IdentityError::Storage)?,
        };
        source.validate_stored()?;
        Ok(source)
    }
}
