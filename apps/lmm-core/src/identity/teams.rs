use super::*;
use serde::Deserialize;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct InviteRequest {
    pub recipient_user_id: i64,
    pub role: TeamRole,
    pub can_spend: bool,
    pub ttl_seconds: i64,
}
#[derive(Serialize)]
pub struct TeamSummary {
    pub id: i64,
    pub account_id: i64,
    pub created_by_user_id: i64,
    pub version: i64,
    pub name: String,
    pub role: TeamRole,
    pub membership_version: i64,
    pub can_spend: bool,
}

impl IdentityStore {
    pub async fn list_teams(&self, secret: &str, after: i64) -> Result<Vec<TeamSummary>> {
        if after < 0 {
            return Err(IdentityError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
            .execute(&mut *tx)
            .await?;
        let actor = authenticate(&mut tx, secret, false).await?;
        if actor.credential_kind != CredentialKind::Session {
            return Err(IdentityError::Forbidden);
        }
        let rows = sqlx::query("SELECT t.id,t.account_id,t.created_by_user_id,t.version AS team_version,t.name,t.owner_user_id,m.role,m.version,m.can_spend FROM core_identity.teams t JOIN core_identity.accounts a ON a.id=t.account_id LEFT JOIN core_identity.memberships m ON m.team_id=t.id AND m.user_id=$1 WHERE t.active AND a.active AND t.id>$2 AND (t.owner_user_id=$1 OR m.active) ORDER BY t.id LIMIT 100")
            .bind(actor.user_id).bind(after).fetch_all(&mut *tx).await?;
        let mut teams = Vec::with_capacity(rows.len());
        for row in rows {
            let owner = row.try_get::<i64, _>("owner_user_id")? == actor.user_id;
            teams.push(TeamSummary {
                id: row.try_get("id")?,
                account_id: row.try_get("account_id")?,
                created_by_user_id: row.try_get("created_by_user_id")?,
                version: row.try_get("team_version")?,
                name: row.try_get("name")?,
                role: if owner {
                    TeamRole::Owner
                } else {
                    parse_role(&row.try_get::<String, _>("role")?)?
                },
                membership_version: if owner { 0 } else { row.try_get("version")? },
                can_spend: owner || row.try_get::<Option<bool>, _>("can_spend")? == Some(true),
            });
        }
        tx.commit().await?;
        Ok(teams)
    }
    pub async fn create_team(&self, secret: &str) -> Result<i64> {
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let account_id = new_account(&mut tx, "team", 0).await?;
        let id: i64 = sqlx::query_scalar(
            "INSERT INTO core_identity.teams(account_id,owner_user_id,created_by_user_id) VALUES ($1,$2,$2) RETURNING id",
        )
        .bind(account_id)
        .bind(actor.user_id)
        .fetch_one(&mut *tx)
        .await?;
        audit(
            &mut tx,
            &actor,
            "team.create",
            json!({"team_id":id,"account_id":account_id}),
        )
        .await?;
        tx.commit().await?;
        Ok(id)
    }
    pub async fn invite(
        &self,
        secret: &str,
        team_id: i64,
        request: &InviteRequest,
    ) -> Result<IssuedCredential> {
        valid_ttl(request.ttl_seconds, SESSION_TTL)?;
        let role_name = role_text(request.role)?;
        let recipient = request.recipient_user_id;
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let team = lock_team(&mut tx, team_id).await?;
        let (actor_role, actor_version) = team_role(&mut tx, &team, team_id, actor.user_id).await?;
        if !actor_role.can_manage_member(request.role)
            || recipient == actor.user_id
            || recipient == team.try_get::<i64, _>("owner_user_id")?
        {
            return Err(IdentityError::Forbidden);
        }
        let enabled: bool = sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id WHERE u.id=$1 AND u.active AND a.active)")
            .bind(recipient).fetch_one(&mut *tx).await?;
        if !enabled {
            return Err(IdentityError::Forbidden);
        }
        let membership = sqlx::query(
            "SELECT active,version FROM core_identity.memberships WHERE team_id=$1 AND user_id=$2",
        )
        .bind(team_id)
        .bind(recipient)
        .fetch_optional(&mut *tx)
        .await?;
        let recipient_version = match membership {
            Some(row) if row.try_get::<bool, _>("active")? => return Err(IdentityError::Conflict),
            Some(row) => row.try_get::<i64, _>("version")?,
            None => 0,
        };
        let issued = new_secret("lmmi_")?;
        let id: i64 = sqlx::query_scalar("INSERT INTO core_identity.invites(digest,team_id,team_version,recipient_user_id,recipient_version,inviter_credential_id,inviter_membership_version,role,can_spend,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,clock_timestamp()+$10::bigint*interval '1 second') RETURNING id")
            .bind(digest(&issued)?).bind(team_id).bind(team.try_get::<i64,_>("version")?).bind(recipient).bind(recipient_version)
            .bind(actor.credential_id).bind(actor_version).bind(role_name).bind(request.can_spend).bind(request.ttl_seconds).fetch_one(&mut *tx).await?;
        audit(
            &mut tx,
            &actor,
            "team.invite",
            json!({"team_id":team_id,"invite_id":id,"recipient":recipient}),
        )
        .await?;
        tx.commit().await?;
        Ok(IssuedCredential { id, secret: issued })
    }
    pub async fn accept_invite(&self, secret: &str, invite_secret: &str) -> Result<i64> {
        self.accept_invite_digest(secret, digest(invite_secret)?)
            .await
    }
    pub(super) async fn accept_invite_digest(&self, secret: &str, hashed: Vec<u8>) -> Result<i64> {
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let team_id: i64 =
            sqlx::query_scalar("SELECT team_id FROM core_identity.invites WHERE digest=$1")
                .bind(&hashed)
                .fetch_optional(&mut *tx)
                .await?
                .ok_or(IdentityError::Forbidden)?;
        let team = lock_team(&mut tx, team_id).await?;
        let invite = sqlx::query("SELECT * FROM core_identity.invites WHERE digest=$1 AND accepted_at IS NULL AND rejected_at IS NULL AND withdrawn_at IS NULL AND expires_at>clock_timestamp() FOR UPDATE")
            .bind(hashed).fetch_optional(&mut *tx).await?.ok_or(IdentityError::Conflict)?;
        if invite.try_get::<i64, _>("recipient_user_id")? != actor.user_id
            || invite.try_get::<i64, _>("team_version")? != team.try_get::<i64, _>("version")?
        {
            return Err(IdentityError::Forbidden);
        }
        let inviter_row = sqlx::query(&format!("{AUTH} AND c.id=$1 FOR SHARE OF c,u,pa,own"))
            .bind(invite.try_get::<i64, _>("inviter_credential_id")?)
            .fetch_optional(&mut *tx)
            .await?
            .ok_or(IdentityError::Forbidden)?;
        let inviter = principal(&mut tx, inviter_row).await?;
        if inviter.credential_kind != CredentialKind::Session {
            return Err(IdentityError::Forbidden);
        }
        let (inviter_role, version) = team_role(&mut tx, &team, team_id, inviter.user_id).await?;
        let role: String = invite.try_get("role")?;
        if version != invite.try_get::<i64, _>("inviter_membership_version")?
            || !inviter_role.can_manage_member(parse_role(&role)?)
        {
            return Err(IdentityError::Forbidden);
        }
        let updated = sqlx::query("INSERT INTO core_identity.memberships AS m(team_id,user_id,role,can_spend) VALUES ($1,$2,$3,$4) ON CONFLICT(team_id,user_id) DO UPDATE SET role=EXCLUDED.role,can_spend=EXCLUDED.can_spend,active=TRUE,version=m.version+1 WHERE NOT m.active AND m.version=$5")
            .bind(team_id).bind(actor.user_id).bind(role).bind(invite.try_get::<bool,_>("can_spend")?).bind(invite.try_get::<i64,_>("recipient_version")?)
            .execute(&mut *tx).await?.rows_affected();
        if updated != 1 {
            return Err(IdentityError::Conflict);
        }
        let id: i64 = invite.try_get("id")?;
        sqlx::query("UPDATE core_identity.invites SET accepted_at=clock_timestamp() WHERE id=$1")
            .bind(id)
            .execute(&mut *tx)
            .await?;
        audit(
            &mut tx,
            &actor,
            "team.invite.accept",
            json!({"team_id":team_id,"invite_id":id}),
        )
        .await?;
        tx.commit().await?;
        Ok(team_id)
    }
    pub async fn remove_member(
        &self,
        secret: &str,
        team_id: i64,
        user_id: i64,
        expected_version: i64,
    ) -> Result<()> {
        if expected_version <= 0 {
            return Err(IdentityError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let team = lock_team(&mut tx, team_id).await?;
        let (actor_role, _) = team_role(&mut tx, &team, team_id, actor.user_id).await?;
        let (target_role, _) = team_role(&mut tx, &team, team_id, user_id).await?;
        if target_role == TeamRole::Owner
            || (actor.user_id != user_id && !actor_role.can_manage_member(target_role))
        {
            return Err(IdentityError::Forbidden);
        }
        let updated = sqlx::query("UPDATE core_identity.memberships SET active=FALSE,can_spend=FALSE,version=version+1 WHERE team_id=$1 AND user_id=$2 AND active AND version=$3")
            .bind(team_id).bind(user_id).bind(expected_version).execute(&mut *tx).await?.rows_affected();
        if updated != 1 {
            return Err(IdentityError::Conflict);
        }
        audit(
            &mut tx,
            &actor,
            "team.member.remove",
            json!({"team_id":team_id,"user_id":user_id,"version":expected_version}),
        )
        .await?;
        tx.commit().await?;
        Ok(())
    }
}
