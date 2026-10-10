use super::*;
use serde::Deserialize;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct MemberUpdate {
    pub role: TeamRole,
    pub can_spend: bool,
    pub expected_version: i64,
}
#[derive(Serialize)]
pub struct MemberSummary {
    pub user_id: i64,
    pub role: TeamRole,
    pub can_spend: bool,
    pub membership_version: i64,
}
#[derive(Serialize)]
pub struct InviteSummary {
    pub id: i64,
    pub team_id: i64,
    pub recipient_user_id: i64,
    pub role: TeamRole,
    pub can_spend: bool,
    pub status: String,
    pub expires_at: i64,
}
impl IdentityStore {
    pub async fn list_members(
        &self,
        secret: &str,
        team_id: i64,
        after: i64,
    ) -> Result<Vec<MemberSummary>> {
        if after < 0 {
            return Err(IdentityError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let team = lock_team(&mut tx, team_id).await?;
        team_role(&mut tx, &team, team_id, actor.user_id).await?;
        let rows = sqlx::query("SELECT user_id,role,can_spend,version FROM (SELECT owner_user_id AS user_id,'owner'::text AS role,TRUE AS can_spend,0::bigint AS version FROM core_identity.teams WHERE id=$1 UNION ALL SELECT user_id,role,can_spend,version FROM core_identity.memberships WHERE team_id=$1 AND active) members WHERE user_id>$2 ORDER BY user_id LIMIT 100")
            .bind(team_id).bind(after).fetch_all(&mut *tx).await?;
        let result = rows
            .into_iter()
            .map(|r| {
                let role: String = r.try_get("role")?;
                Ok(MemberSummary {
                    user_id: r.try_get("user_id")?,
                    role: if role == "owner" {
                        TeamRole::Owner
                    } else {
                        parse_role(&role)?
                    },
                    can_spend: r.try_get("can_spend")?,
                    membership_version: r.try_get("version")?,
                })
            })
            .collect::<Result<Vec<_>>>()?;
        tx.commit().await?;
        Ok(result)
    }
    pub async fn update_member(
        &self,
        secret: &str,
        team_id: i64,
        user_id: i64,
        change: &MemberUpdate,
    ) -> Result<()> {
        if change.expected_version <= 0 || user_id <= 0 {
            return Err(IdentityError::Invalid);
        }
        let desired = role_text(change.role)?;
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let team = lock_team(&mut tx, team_id).await?;
        let (actor_role, _) = team_role(&mut tx, &team, team_id, actor.user_id).await?;
        let (old_role, _) = team_role(&mut tx, &team, team_id, user_id).await?;
        if actor.user_id == user_id
            || !actor_role.can_manage_member(old_role)
            || !actor_role.can_manage_member(change.role)
        {
            return Err(IdentityError::Forbidden);
        }
        let changed = sqlx::query("UPDATE core_identity.memberships SET role=$1,can_spend=$2,version=version+1 WHERE team_id=$3 AND user_id=$4 AND active AND version=$5")
            .bind(desired).bind(change.can_spend).bind(team_id).bind(user_id).bind(change.expected_version)
            .execute(&mut *tx).await?.rows_affected();
        if changed != 1 {
            return Err(IdentityError::Conflict);
        }
        audit(&mut tx, &actor, "team.member.update", json!({"team_id":team_id,"user_id":user_id,"role":change.role,"can_spend":change.can_spend,"version":change.expected_version+1})).await?;
        tx.commit().await?;
        Ok(())
    }
    /// Only the current owner may transfer to an active member. The immutable
    /// creator keeps their creation slot; the former owner becomes a non-spending
    /// administrator. Every previous team grant is invalidated by the team epoch.
    pub async fn transfer_team(
        &self,
        secret: &str,
        team_id: i64,
        new_owner: i64,
        expected_version: i64,
    ) -> Result<()> {
        if new_owner <= 0 || expected_version <= 0 {
            return Err(IdentityError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let team = lock_team(&mut tx, team_id).await?;
        if team.try_get::<i64, _>("owner_user_id")? != actor.user_id || new_owner == actor.user_id {
            return Err(IdentityError::Forbidden);
        }
        if team.try_get::<i64, _>("version")? != expected_version {
            return Err(IdentityError::Conflict);
        }
        let eligible: bool = sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM core_identity.memberships m JOIN core_identity.users u ON u.id=m.user_id JOIN core_identity.accounts a ON a.id=u.personal_account_id WHERE m.team_id=$1 AND m.user_id=$2 AND m.active AND u.active AND a.active)")
            .bind(team_id).bind(new_owner).fetch_one(&mut *tx).await?;
        if !eligible {
            return Err(IdentityError::Forbidden);
        }
        // Coordinate with disabling a recipient during transfer.
        sqlx::query("SELECT u.id FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id WHERE u.id=$1 AND u.active AND a.active FOR SHARE OF u,a")
            .bind(new_owner).fetch_optional(&mut *tx).await?.ok_or(IdentityError::Forbidden)?;
        sqlx::query("UPDATE core_identity.memberships SET active=FALSE,can_spend=FALSE,version=version+1 WHERE team_id=$1 AND user_id=$2")
            .bind(team_id).bind(new_owner).execute(&mut *tx).await?;
        sqlx::query(
            "UPDATE core_identity.teams SET owner_user_id=$1,version=version+1 WHERE id=$2",
        )
        .bind(new_owner)
        .bind(team_id)
        .execute(&mut *tx)
        .await?;
        sqlx::query("INSERT INTO core_identity.memberships AS m(team_id,user_id,role,can_spend) VALUES ($1,$2,'admin',FALSE) ON CONFLICT(team_id,user_id) DO UPDATE SET role='admin',can_spend=FALSE,active=TRUE,version=m.version+1")
            .bind(team_id).bind(actor.user_id).execute(&mut *tx).await?;
        audit(&mut tx, &actor, "team.owner.transfer", json!({"team_id":team_id,"previous_owner":actor.user_id,"owner_user_id":new_owner,"version":expected_version+1})).await?;
        tx.commit().await?;
        Ok(())
    }
    /// Soft closure only. Never deletes accounts, usage, balances or ledger rows.
    pub async fn close_team(
        &self,
        secret: &str,
        team_id: i64,
        expected_version: i64,
    ) -> Result<()> {
        if expected_version <= 0 {
            return Err(IdentityError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let team = lock_team(&mut tx, team_id).await?;
        if team.try_get::<i64, _>("owner_user_id")? != actor.user_id {
            return Err(IdentityError::Forbidden);
        }
        if team.try_get::<i64, _>("version")? != expected_version {
            return Err(IdentityError::Conflict);
        }
        sqlx::query("UPDATE core_identity.teams SET active=FALSE,version=version+1 WHERE id=$1")
            .bind(team_id)
            .execute(&mut *tx)
            .await?;
        sqlx::query("UPDATE core_identity.accounts SET active=FALSE WHERE id=$1")
            .bind(team.try_get::<i64, _>("account_id")?)
            .execute(&mut *tx)
            .await?;
        sqlx::query("UPDATE core_identity.memberships SET active=FALSE,can_spend=FALSE,version=version+1 WHERE team_id=$1 AND active")
            .bind(team_id).execute(&mut *tx).await?;
        sqlx::query("UPDATE core_identity.invites SET withdrawn_at=clock_timestamp() WHERE team_id=$1 AND accepted_at IS NULL AND rejected_at IS NULL AND withdrawn_at IS NULL")
            .bind(team_id).execute(&mut *tx).await?;
        audit(
            &mut tx,
            &actor,
            "team.close",
            json!({"team_id":team_id,"version":expected_version+1}),
        )
        .await?;
        tx.commit().await?;
        Ok(())
    }
    pub async fn rename_team(
        &self,
        secret: &str,
        team_id: i64,
        name: &str,
        expected_version: i64,
    ) -> Result<()> {
        let name = name.trim();
        if name.is_empty()
            || name.chars().count() > 100
            || name.chars().any(char::is_control)
            || expected_version <= 0
        {
            return Err(IdentityError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let team = lock_team(&mut tx, team_id).await?;
        let (role, _) = team_role(&mut tx, &team, team_id, actor.user_id).await?;
        if !matches!(role, TeamRole::Owner | TeamRole::Admin) {
            return Err(IdentityError::Forbidden);
        }
        if team.try_get::<i64, _>("version")? != expected_version {
            return Err(IdentityError::Conflict);
        }
        sqlx::query("UPDATE core_identity.teams SET name=$1,version=version+1 WHERE id=$2")
            .bind(name)
            .bind(team_id)
            .execute(&mut *tx)
            .await?;
        audit(
            &mut tx,
            &actor,
            "team.rename",
            json!({"team_id":team_id,"version":expected_version+1}),
        )
        .await?;
        tx.commit().await?;
        Ok(())
    }
    pub async fn list_invites(
        &self,
        secret: &str,
        team_id: Option<i64>,
        after: i64,
    ) -> Result<Vec<InviteSummary>> {
        if after < 0 {
            return Err(IdentityError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        if let Some(id) = team_id {
            let team = lock_team(&mut tx, id).await?;
            let (role, _) = team_role(&mut tx, &team, id, actor.user_id).await?;
            if !matches!(role, TeamRole::Owner | TeamRole::Admin) {
                return Err(IdentityError::Forbidden);
            }
        }
        let rows = sqlx::query("SELECT i.id,i.team_id,i.recipient_user_id,i.role,i.can_spend,EXTRACT(EPOCH FROM i.expires_at)::bigint AS expires,CASE WHEN i.accepted_at IS NOT NULL THEN 'accepted' WHEN i.rejected_at IS NOT NULL THEN 'rejected' WHEN i.withdrawn_at IS NOT NULL THEN 'withdrawn' WHEN i.expires_at<=clock_timestamp() THEN 'expired' WHEN NOT t.active OR t.version<>i.team_version THEN 'stale' ELSE 'pending' END AS status FROM core_identity.invites i JOIN core_identity.teams t ON t.id=i.team_id WHERE i.id>$1 AND (($2::bigint IS NULL AND i.recipient_user_id=$3) OR i.team_id=$2) ORDER BY i.id LIMIT 100")
            .bind(after).bind(team_id).bind(actor.user_id).fetch_all(&mut *tx).await?;
        let result = rows
            .into_iter()
            .map(|r| {
                Ok(InviteSummary {
                    id: r.try_get("id")?,
                    team_id: r.try_get("team_id")?,
                    recipient_user_id: r.try_get("recipient_user_id")?,
                    role: parse_role(&r.try_get::<String, _>("role")?)?,
                    can_spend: r.try_get("can_spend")?,
                    status: r.try_get("status")?,
                    expires_at: r.try_get("expires")?,
                })
            })
            .collect::<Result<Vec<_>>>()?;
        tx.commit().await?;
        Ok(result)
    }
    /// Inbox acceptance is bound to the authenticated recipient. The token
    /// variant remains available for direct invitation links.
    pub async fn accept_invite_by_id(&self, secret: &str, id: i64) -> Result<i64> {
        let actor = self.authorize(secret).await?;
        if actor.credential_kind != CredentialKind::Session {
            return Err(IdentityError::Forbidden);
        }
        let hash = sqlx::query_scalar(
            "SELECT digest FROM core_identity.invites WHERE id=$1 AND recipient_user_id=$2",
        )
        .bind(id)
        .bind(actor.user_id)
        .fetch_optional(&self.pool)
        .await?
        .ok_or(IdentityError::Forbidden)?;
        self.accept_invite_digest(secret, hash).await
    }
    pub async fn reject_invite(&self, secret: &str, id: i64) -> Result<()> {
        self.finish_invite(secret, id, false).await
    }
    pub async fn withdraw_invite(&self, secret: &str, id: i64) -> Result<()> {
        self.finish_invite(secret, id, true).await
    }
    async fn finish_invite(&self, secret: &str, id: i64, withdraw: bool) -> Result<()> {
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let row = sqlx::query(
            "SELECT team_id,recipient_user_id,role FROM core_identity.invites WHERE id=$1",
        )
        .bind(id)
        .fetch_optional(&mut *tx)
        .await?
        .ok_or(IdentityError::Forbidden)?;
        let team_id: i64 = row.try_get("team_id")?;
        let team = lock_team(&mut tx, team_id).await?;
        if withdraw {
            let (role, _) = team_role(&mut tx, &team, team_id, actor.user_id).await?;
            if !role.can_manage_member(parse_role(&row.try_get::<String, _>("role")?)?) {
                return Err(IdentityError::Forbidden);
            }
        } else if row.try_get::<i64, _>("recipient_user_id")? != actor.user_id {
            return Err(IdentityError::Forbidden);
        }
        // Identifiers are selected internally, never interpolated from a request.
        let (column, action) = if withdraw {
            ("withdrawn_at", "team.invite.withdraw")
        } else {
            ("rejected_at", "team.invite.reject")
        };
        let changed = sqlx::query(&format!("UPDATE core_identity.invites SET {column}=clock_timestamp() WHERE id=$1 AND accepted_at IS NULL AND rejected_at IS NULL AND withdrawn_at IS NULL AND expires_at>clock_timestamp()"))
            .bind(id).execute(&mut *tx).await?.rows_affected();
        if changed != 1 {
            return Err(IdentityError::Conflict);
        }
        audit(
            &mut tx,
            &actor,
            action,
            json!({"team_id":team_id,"invite_id":id}),
        )
        .await?;
        tx.commit().await?;
        Ok(())
    }
}
