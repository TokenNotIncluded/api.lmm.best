//! Recheck authority on the SAME connection used for the financial transaction.
//! Platform administrator levels never imply permission to spend team funds.
use super::types::*;
use sha2::{Digest, Sha256};
use sqlx::{PgConnection, Row};

pub(super) struct Actor {
    pub user: i64,
    pub key: i64,
    pub owner: i64,
    pub personal: i64,
    pub kind: String,
    pub preference: Option<String>,
}
pub(super) struct Payer {
    pub id: i64,
    pub preference: String,
}
pub(super) fn digest(secret: &str) -> Result<Vec<u8>> {
    if !(32..=256).contains(&secret.len())
        || !secret
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-')
    {
        return Err(Error::Unauthorized);
    }
    Ok(Sha256::digest(secret.as_bytes()).to_vec())
}
pub(super) fn worker_digest(worker: &[u8; 32]) -> Result<Vec<u8>> {
    if *worker == [0; 32] {
        return Err(Error::Invalid);
    }
    Ok(Sha256::digest(worker).to_vec())
}

pub(super) async fn authenticate(c: &mut PgConnection, secret: &str) -> Result<Actor> {
    let row = sqlx::query(
        "SELECT c.id,c.kind,c.user_id,c.owner_account_id,c.personal_billing_preference,u.personal_account_id \
         FROM core_identity.credentials c JOIN core_identity.users u ON u.id=c.user_id \
         JOIN core_identity.accounts a ON a.id=c.owner_account_id \
         JOIN core_identity.accounts p ON p.id=u.personal_account_id \
         WHERE c.digest=$1 AND c.revoked_at IS NULL AND c.expires_at>clock_timestamp() \
         AND c.user_version=u.auth_version AND u.active AND a.active AND p.active \
         FOR SHARE OF c,u,a,p",
    )
    .bind(digest(secret)?)
    .fetch_optional(c)
    .await?
    .ok_or(Error::Unauthorized)?;
    Ok(Actor {
        user: row.try_get("user_id")?,
        key: row.try_get("id")?,
        owner: row.try_get("owner_account_id")?,
        personal: row.try_get("personal_account_id")?,
        kind: row.try_get("kind")?,
        preference: row.try_get("personal_billing_preference")?,
    })
}

pub(super) async fn funding(c: &mut PgConnection, actor: &Actor) -> Result<Vec<Payer>> {
    if actor.kind != "api_key" {
        return Err(Error::Forbidden);
    }
    let rows = sqlx::query(
        "SELECT r.position,r.payer_account_id,a.kind,p.preference FROM core_identity.key_funding_rules r \
         JOIN core_identity.accounts a ON a.id=r.payer_account_id \
         JOIN core_billing.account_policies p ON p.account_id=a.id \
         WHERE r.credential_id=$1 AND a.active ORDER BY r.position FOR SHARE OF r,a,p",
    )
    .bind(actor.key)
    .fetch_all(&mut *c)
    .await?;
    let rule_count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_identity.key_funding_rules WHERE credential_id=$1",
    )
    .bind(actor.key)
    .fetch_one(&mut *c)
    .await?;
    if rows.is_empty() || rows.len() > 16 || rows.len() as i64 != rule_count {
        return Err(Error::Forbidden);
    }
    let team_owned = actor.owner != actor.personal;
    if team_owned && (rows.len() != 1 || actor.preference.is_some()) {
        return Err(Error::Forbidden);
    }
    let mut result = Vec::new();
    for (position, row) in rows.into_iter().enumerate() {
        let id: i64 = row.try_get("payer_account_id")?;
        let kind: String = row.try_get("kind")?;
        if row.try_get::<i16, _>("position")? as usize != position
            || (team_owned && id != actor.owner)
            || (kind == "personal" && id != actor.personal)
        {
            return Err(Error::Forbidden);
        }
        let mut preference: String = row.try_get("preference")?;
        if kind == "team" {
            let team = sqlx::query(
                "SELECT t.id,t.version,t.owner_user_id,g.team_version,g.membership_version \
                 FROM core_identity.teams t JOIN core_identity.credential_grants g ON g.team_id=t.id \
                 WHERE t.account_id=$1 AND g.credential_id=$2 AND t.active FOR SHARE OF t,g",
            )
            .bind(id)
            .bind(actor.key)
            .fetch_optional(&mut *c)
            .await?
            .ok_or(Error::Forbidden)?;
            if team.try_get::<i64, _>("version")? != team.try_get::<i64, _>("team_version")? {
                return Err(Error::Forbidden);
            }
            let grant_version: i64 = team.try_get("membership_version")?;
            if team.try_get::<i64, _>("owner_user_id")? == actor.user {
                if grant_version != 0 {
                    return Err(Error::Forbidden);
                }
            } else {
                let current: Option<i64> = sqlx::query_scalar(
                    "SELECT version FROM core_identity.memberships WHERE team_id=$1 AND user_id=$2 \
                     AND active AND can_spend FOR SHARE",
                )
                .bind(team.try_get::<i64, _>("id")?)
                .bind(actor.user)
                .fetch_optional(&mut *c)
                .await?;
                if current != Some(grant_version) {
                    return Err(Error::Forbidden);
                }
            }
        } else if kind == "personal" {
            if let Some(override_preference) = &actor.preference {
                preference = override_preference.clone();
            }
        } else {
            return Err(Error::Forbidden);
        }
        result.push(Payer { id, preference });
    }
    Ok(result)
}

async fn role(c: &mut PgConnection, account: i64, user: i64) -> Result<String> {
    let team = sqlx::query(
        "SELECT t.id,t.owner_user_id FROM core_identity.teams t \
         JOIN core_identity.accounts a ON a.id=t.account_id \
         WHERE t.account_id=$1 AND t.active AND a.active FOR SHARE OF t,a",
    )
    .bind(account)
    .fetch_optional(&mut *c)
    .await?
    .ok_or(Error::Forbidden)?;
    if team.try_get::<i64, _>("owner_user_id")? == user {
        return Ok("owner".into());
    }
    sqlx::query_scalar(
        "SELECT role FROM core_identity.memberships WHERE team_id=$1 AND user_id=$2 AND active FOR SHARE",
    )
    .bind(team.try_get::<i64, _>("id")?)
    .bind(user)
    .fetch_optional(c)
    .await?
    .ok_or(Error::Forbidden)
}

pub(super) async fn manage_budget(
    c: &mut PgConnection,
    actor: &Actor,
    budget: &Budget,
) -> Result<()> {
    if actor.kind != "session" {
        return Err(Error::Forbidden);
    }
    match budget.scope.as_str() {
        "self" if budget.user_id == Some(actor.user) => Ok(()),
        "account" => {
            let account = budget.account_id.ok_or(Error::Invalid)?;
            if account == actor.personal || role(c, account, actor.user).await? == "owner" {
                Ok(())
            } else {
                Err(Error::Forbidden)
            }
        }
        "member" => {
            let account = budget.account_id.ok_or(Error::Invalid)?;
            let subject = budget.user_id.ok_or(Error::Invalid)?;
            let actor_role = role(c, account, actor.user).await?;
            if actor_role == "owner"
                || (actor_role == "admin"
                    && (subject == actor.user || role(c, account, subject).await? == "member"))
            {
                Ok(())
            } else {
                Err(Error::Forbidden)
            }
        }
        "key" => {
            let key = sqlx::query(
                "SELECT user_id,owner_account_id FROM core_identity.credentials \
                 WHERE id=$1 AND kind='api_key' FOR SHARE",
            )
            .bind(budget.key_id)
            .fetch_optional(&mut *c)
            .await?
            .ok_or(Error::Forbidden)?;
            let account: i64 = key.try_get("owner_account_id")?;
            let subject: i64 = key.try_get("user_id")?;
            if account == actor.personal && subject == actor.user {
                return Ok(());
            }
            let actor_role = role(c, account, actor.user).await?;
            if actor_role == "owner"
                || (actor_role == "admin"
                    && (subject == actor.user || role(c, account, subject).await? == "member"))
            {
                Ok(())
            } else {
                Err(Error::Forbidden)
            }
        }
        _ => Err(Error::Forbidden),
    }
}
