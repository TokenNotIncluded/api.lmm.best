use super::*;
use std::collections::{BTreeSet, HashSet};

impl IdentityStore {
    pub async fn issue_key(&self, secret: &str, owner: Account, policy: FundingPolicy, ttl: i64) -> Result<IssuedCredential> {
        valid_ttl(ttl, MAX_TTL)?;
        let order = policy.account_order.clone().unwrap_or_else(|| vec![owner]);
        if order.is_empty() || order.len() > 16 || order.iter().any(|a| !a.is_valid())
            || order.iter().collect::<HashSet<_>>().len() != order.len()
        { return Err(IdentityError::Invalid); }
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        if owner.kind == AccountKind::Personal && owner.id != actor.user_id {
            return Err(IdentityError::Forbidden);
        }
        let mut grants = Vec::new();
        let teams: BTreeSet<i64> = order.iter().filter(|a| a.kind == AccountKind::Team).map(|a| a.id).collect();
        for id in teams {
            let row = sqlx::query("SELECT t.owner_user_id,t.version,m.version AS member_version,m.active AS member_active,m.can_spend FROM core_identity.teams t JOIN core_identity.accounts a ON a.id=t.account_id LEFT JOIN core_identity.memberships m ON m.team_id=t.id AND m.user_id=$2 WHERE t.id=$1 AND t.active AND a.active FOR SHARE OF t,a")
                .bind(id).bind(actor.user_id).fetch_optional(&mut *tx).await?.ok_or(IdentityError::Forbidden)?;
            let member_version = if row.try_get::<i64, _>("owner_user_id")? == actor.user_id { 0 } else {
                if row.try_get::<Option<bool>, _>("member_active")? != Some(true)
                    || row.try_get::<Option<bool>, _>("can_spend")? != Some(true)
                { return Err(IdentityError::Forbidden); }
                row.try_get::<i64, _>("member_version")?
            };
            grants.push((id, row.try_get::<i64, _>("version")?, member_version));
        }
        policy.resolve(owner, &grants.iter().map(|g| g.0).collect()).map_err(|_| IdentityError::Forbidden)?;
        let owner_id = native_account(&mut tx, owner).await?;
        let mut funding = Vec::with_capacity(order.len());
        for account in &order { funding.push(native_account(&mut tx, *account).await?); }
        let preference = policy.personal_billing_preference.map(|p| match p {
            BillingPreference::SubscriptionFirst => "subscription_first",
            BillingPreference::WalletFirst => "wallet_first",
            BillingPreference::SubscriptionOnly => "subscription_only",
            BillingPreference::WalletOnly => "wallet_only",
        });
        let issued = new_secret("lmmk_")?;
        let id: i64 = sqlx::query_scalar("INSERT INTO core_identity.credentials(digest,kind,user_id,user_version,owner_account_id,personal_billing_preference,expires_at) VALUES ($1,'api_key',$2,$3,$4,$5,clock_timestamp()+$6::bigint*interval '1 second') RETURNING id")
            .bind(digest(&issued)?).bind(actor.user_id).bind(actor.user_version).bind(owner_id)
            .bind(preference).bind(ttl).fetch_one(&mut *tx).await?;
        for (position, payer) in funding.into_iter().enumerate() {
            let position = i16::try_from(position).map_err(|_| IdentityError::Invalid)?;
            sqlx::query("INSERT INTO core_identity.key_funding_rules(credential_id,position,payer_account_id) VALUES ($1,$2,$3)")
                .bind(id).bind(position).bind(payer).execute(&mut *tx).await?;
        }
        for (team, version, member) in grants {
            sqlx::query("INSERT INTO core_identity.credential_grants(credential_id,team_id,team_version,membership_version) VALUES ($1,$2,$3,$4)")
                .bind(id).bind(team).bind(version).bind(member).execute(&mut *tx).await?;
        }
        audit(&mut tx, &actor, "credential.issue", json!({"credential_id":id,"owner_account_id":owner_id})).await?;
        tx.commit().await?;
        Ok(IssuedCredential { id, secret: issued })
    }
    pub async fn revoke(&self, secret: &str, id: i64) -> Result<()> {
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let changed = sqlx::query("UPDATE core_identity.credentials SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=$1 AND user_id=$2")
            .bind(id).bind(actor.user_id).execute(&mut *tx).await?.rows_affected();
        if changed != 1 { return Err(IdentityError::Forbidden); }
        audit(&mut tx, &actor, "credential.revoke", json!({"credential_id":id})).await?;
        tx.commit().await?;
        Ok(())
    }
}
