use super::*;
use std::collections::{BTreeSet, HashSet};

impl IdentityStore {
    pub async fn issue_key(
        &self,
        secret: &str,
        owner: Account,
        policy: FundingPolicy,
        ttl: i64,
    ) -> Result<IssuedCredential> {
        valid_ttl(ttl, MAX_TTL)?;
        let order = policy.account_order.clone().unwrap_or_else(|| vec![owner]);
        if order.is_empty()
            || order.len() > 16
            || order.iter().any(|a| !a.is_valid())
            || order.iter().collect::<HashSet<_>>().len() != order.len()
        {
            return Err(IdentityError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        if owner.kind == AccountKind::Personal && owner.id != actor.user_id {
            return Err(IdentityError::Forbidden);
        }
        let mut grants = Vec::new();
        // Stable lock order. Team mutations take these same rows exclusively.
        let teams: BTreeSet<i64> = order
            .iter()
            .filter(|a| a.kind == AccountKind::Team)
            .map(|a| a.id)
            .collect();
        for id in teams {
            let row = sqlx::query("SELECT t.owner_user_id,t.version,m.version AS member_version,m.active AS member_active,m.can_spend FROM core_identity.teams t LEFT JOIN core_identity.memberships m ON m.team_id=t.id AND m.user_id=$2 WHERE t.id=$1 AND t.active FOR SHARE OF t")
                .bind(id).bind(actor.user_id).fetch_optional(&mut *tx).await?.ok_or(IdentityError::Forbidden)?;
            let member_version = if row.try_get::<i64, _>("owner_user_id")? == actor.user_id {
                0
            } else {
                if row.try_get::<Option<bool>, _>("member_active")? != Some(true)
                    || row.try_get::<Option<bool>, _>("can_spend")? != Some(true)
                {
                    return Err(IdentityError::Forbidden);
                }
                row.try_get::<i64, _>("member_version")?
            };
            grants.push((id, row.try_get::<i64, _>("version")?, member_version));
        }
        policy
            .resolve(owner, &grants.iter().map(|g| g.0).collect())
            .map_err(|_| IdentityError::Forbidden)?;
        let issued = new_secret("lmmk_")?;
        let team_id = (owner.kind == AccountKind::Team).then_some(owner.id);
        let id: i64 = sqlx::query_scalar("INSERT INTO core_identity.credentials(digest,kind,user_id,user_version,team_id,funding_policy,expires_at) VALUES ($1,'api_key',$2,$3,$4,$5,clock_timestamp()+$6::bigint*interval '1 second') RETURNING id")
            .bind(digest(&issued)?).bind(actor.user_id).bind(actor.user_version).bind(team_id)
            .bind(serde_json::to_value(policy).map_err(|_| IdentityError::Invalid)?).bind(ttl).fetch_one(&mut *tx).await?;
        for (team, version, member) in grants {
            sqlx::query("INSERT INTO core_identity.credential_grants(credential_id,team_id,team_version,membership_version) VALUES ($1,$2,$3,$4)")
                .bind(id).bind(team).bind(version).bind(member).execute(&mut *tx).await?;
        }
        audit(
            &mut tx,
            &actor,
            "credential.issue",
            json!({"credential_id":id,"owner":owner}),
        )
        .await?;
        tx.commit().await?;
        Ok(IssuedCredential { id, secret: issued })
    }
    pub async fn revoke(&self, secret: &str, id: i64) -> Result<()> {
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let changed = sqlx::query("UPDATE core_identity.credentials SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=$1 AND user_id=$2")
            .bind(id).bind(actor.user_id).execute(&mut *tx).await?.rows_affected();
        if changed != 1 {
            return Err(IdentityError::Forbidden);
        }
        audit(
            &mut tx,
            &actor,
            "credential.revoke",
            json!({"credential_id":id}),
        )
        .await?;
        tx.commit().await?;
        Ok(())
    }
}
