//! Core-owned users, accounts and authority. The store never calls Go.
//! Identity checks are not reservations: billing must recheck authority in its
//! own transaction before taking funds. Model routes remain unavailable.
mod credentials;
mod schema;
mod teams;
pub use teams::{InviteRequest, TeamSummary};

use crate::{
    accounts::{Account, AccountKind, TeamRole},
    funding::{BillingPreference, FundingPolicy},
};
use rand::{RngCore, rngs::OsRng};
use serde::Serialize;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{
    PgConnection, PgPool, Row,
    postgres::{PgPoolOptions, PgRow},
};
use std::{fmt, time::Duration};

const MAX_TTL: i64 = 31_536_000;
const SESSION_TTL: i64 = 604_800;
const AUTH: &str = "SELECT c.id,c.kind,c.user_id,c.user_version,c.owner_account_id,c.personal_billing_preference,u.personal_account_id,t.id AS team_id,CASE u.platform_role WHEN 'superadmin' THEN 6::smallint WHEN 'admin' THEN 5::smallint ELSE pa.service_level END AS platform_level FROM core_identity.credentials c JOIN core_identity.users u ON u.id=c.user_id JOIN core_identity.accounts pa ON pa.id=u.personal_account_id JOIN core_identity.accounts own ON own.id=c.owner_account_id LEFT JOIN core_identity.teams t ON t.account_id=c.owner_account_id WHERE c.revoked_at IS NULL AND c.expires_at>clock_timestamp() AND u.active AND pa.active AND own.active AND c.user_version=u.auth_version";

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum IdentityError {
    Unauthorized,
    Forbidden,
    Invalid,
    Conflict,
    Storage,
}
impl fmt::Display for IdentityError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{self:?}")
    }
}
impl std::error::Error for IdentityError {}
impl From<sqlx::Error> for IdentityError {
    fn from(error: sqlx::Error) -> Self {
        if let Some(db) = error.as_database_error()
            && matches!(db.code().as_deref(), Some("23505" | "40001" | "40P01"))
        {
            return Self::Conflict;
        }
        // Never expose SQL parameters, connection strings or bearer tokens.
        Self::Storage
    }
}
type Result<T> = std::result::Result<T, IdentityError>;

#[derive(Clone)]
pub struct IdentityStore {
    pool: PgPool,
}
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum CredentialKind {
    Session,
    ApiKey,
}
#[derive(Debug, Serialize)]
pub struct Principal {
    pub credential_id: i64,
    pub credential_kind: CredentialKind,
    pub user_id: i64,
    pub platform_level: i16,
    pub owner: Account,
    pub funding_accounts: Vec<Account>,
    /// Stable storage identity. A public user/team ID is not this account ID.
    pub owner_account_id: i64,
    pub funding_account_ids: Vec<i64>,
    #[serde(skip)]
    user_version: i64,
}
/// Returned once. Deliberately no Debug implementation.
#[derive(Serialize)]
pub struct IssuedCredential {
    pub id: i64,
    pub secret: String,
}

fn digest(secret: &str) -> Result<Vec<u8>> {
    if !(32..=256).contains(&secret.len())
        || !secret
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || c == b'_' || c == b'-')
    {
        return Err(IdentityError::Unauthorized);
    }
    Ok(Sha256::digest(secret.as_bytes()).to_vec())
}
fn new_secret(prefix: &str) -> Result<String> {
    let mut bytes = [0_u8; 32];
    OsRng
        .try_fill_bytes(&mut bytes)
        .map_err(|_| IdentityError::Storage)?;
    let mut secret = String::with_capacity(prefix.len() + 64);
    secret.push_str(prefix);
    for b in bytes {
        use std::fmt::Write;
        write!(&mut secret, "{b:02x}").map_err(|_| IdentityError::Storage)?;
    }
    Ok(secret)
}
fn valid_ttl(ttl: i64, maximum: i64) -> Result<()> {
    if ttl <= 0 || ttl > maximum {
        Err(IdentityError::Invalid)
    } else {
        Ok(())
    }
}
fn role_text(role: TeamRole) -> Result<&'static str> {
    match role {
        TeamRole::Admin => Ok("admin"),
        TeamRole::Member => Ok("member"),
        TeamRole::Owner => Err(IdentityError::Invalid),
    }
}
fn parse_role(role: &str) -> Result<TeamRole> {
    match role {
        "admin" => Ok(TeamRole::Admin),
        "member" => Ok(TeamRole::Member),
        _ => Err(IdentityError::Storage),
    }
}
async fn audit(c: &mut PgConnection, actor: &Principal, action: &str, target: Value) -> Result<()> {
    sqlx::query("INSERT INTO core_identity.audit(actor_user_id,credential_id,action,target) VALUES ($1,$2,$3,$4)")
        .bind(actor.user_id).bind(actor.credential_id).bind(action).bind(target).execute(c).await?;
    Ok(())
}

async fn new_account(c: &mut PgConnection, kind: &str, level: i16) -> Result<i64> {
    let id: i64 = sqlx::query_scalar(
        "INSERT INTO core_identity.accounts(kind,service_level) VALUES ($1,$2) RETURNING id",
    )
    .bind(kind)
    .bind(level)
    .fetch_one(&mut *c)
    .await?;
    sqlx::query("INSERT INTO core_billing.account_policies(account_id) VALUES ($1)")
        .bind(id)
        .execute(c)
        .await?;
    Ok(id)
}
async fn native_account(c: &mut PgConnection, account: Account) -> Result<i64> {
    let sql = match account.kind {
        AccountKind::Personal => {
            "SELECT a.id FROM core_identity.accounts a JOIN core_identity.users u ON u.personal_account_id=a.id WHERE u.id=$1 AND a.active AND u.active FOR SHARE OF a,u"
        }
        AccountKind::Team => {
            "SELECT a.id FROM core_identity.accounts a JOIN core_identity.teams t ON t.account_id=a.id WHERE t.id=$1 AND a.active AND t.active FOR SHARE OF a,t"
        }
    };
    sqlx::query_scalar(sql)
        .bind(account.id)
        .fetch_optional(c)
        .await?
        .ok_or(IdentityError::Forbidden)
}
async fn principal(c: &mut PgConnection, row: PgRow) -> Result<Principal> {
    let user_id: i64 = row.try_get("user_id")?;
    let id: i64 = row.try_get("id")?;
    let owner_account_id: i64 = row.try_get("owner_account_id")?;
    let team_id: Option<i64> = row.try_get("team_id")?;
    let kind = match row.try_get::<String, _>("kind")?.as_str() {
        "session" => CredentialKind::Session,
        "api_key" => CredentialKind::ApiKey,
        _ => return Err(IdentityError::Storage),
    };
    if team_id.is_none() && owner_account_id != row.try_get::<i64, _>("personal_account_id")? {
        return Err(IdentityError::Storage);
    }
    let owner = team_id.map_or(
        Account {
            kind: AccountKind::Personal,
            id: user_id,
        },
        |id| Account {
            kind: AccountKind::Team,
            id,
        },
    );
    let mut funding_accounts = Vec::new();
    let mut funding_account_ids = Vec::new();
    if kind == CredentialKind::ApiKey {
        let rows = sqlx::query("SELECT r.position,r.payer_account_id,a.kind,a.active,u.id AS personal_id,t.id AS team_id FROM core_identity.key_funding_rules r JOIN core_identity.accounts a ON a.id=r.payer_account_id LEFT JOIN core_identity.users u ON u.personal_account_id=a.id LEFT JOIN core_identity.teams t ON t.account_id=a.id WHERE r.credential_id=$1 ORDER BY r.position")
            .bind(id).fetch_all(&mut *c).await?;
        if rows.is_empty() || rows.len() > 16 {
            return Err(IdentityError::Storage);
        }
        for (position, rule) in rows.into_iter().enumerate() {
            if usize::try_from(rule.try_get::<i16, _>("position")?).ok() != Some(position) {
                return Err(IdentityError::Storage);
            }
            if !rule.try_get::<bool, _>("active")? {
                return Err(IdentityError::Forbidden);
            }
            let account = match rule.try_get::<String, _>("kind")?.as_str() {
                "personal" => Account {
                    kind: AccountKind::Personal,
                    id: rule.try_get("personal_id")?,
                },
                "team" => Account {
                    kind: AccountKind::Team,
                    id: rule.try_get("team_id")?,
                },
                _ => return Err(IdentityError::Storage),
            };
            funding_accounts.push(account);
            funding_account_ids.push(rule.try_get("payer_account_id")?);
        }
        let preference: Option<String> = row.try_get("personal_billing_preference")?;
        let preference: Option<BillingPreference> = preference
            .map(|text| {
                serde_json::from_value(Value::String(text)).map_err(|_| IdentityError::Storage)
            })
            .transpose()?;
        let policy = FundingPolicy {
            version: 1,
            account_order: Some(funding_accounts),
            personal_billing_preference: preference,
        };
        let grants: Vec<i64> = sqlx::query_scalar("SELECT g.team_id FROM core_identity.credential_grants g JOIN core_identity.teams t ON t.id=g.team_id JOIN core_identity.accounts a ON a.id=t.account_id LEFT JOIN core_identity.memberships m ON m.team_id=t.id AND m.user_id=$2 WHERE g.credential_id=$1 AND t.active AND a.active AND t.version=g.team_version AND ((t.owner_user_id=$2 AND g.membership_version=0) OR (t.owner_user_id<>$2 AND m.active AND m.can_spend AND m.version=g.membership_version))")
            .bind(id).bind(user_id).fetch_all(&mut *c).await?;
        funding_accounts = policy
            .resolve(owner, &grants.into_iter().collect())
            .map_err(|_| IdentityError::Forbidden)?;
    }
    Ok(Principal {
        credential_id: id,
        credential_kind: kind,
        user_id,
        platform_level: row.try_get("platform_level")?,
        owner,
        funding_accounts,
        owner_account_id,
        funding_account_ids,
        user_version: row.try_get("user_version")?,
    })
}
async fn authenticate(c: &mut PgConnection, secret: &str, write: bool) -> Result<Principal> {
    let suffix = if write {
        " FOR SHARE OF c,u,pa,own"
    } else {
        ""
    };
    let row = sqlx::query(&format!("{AUTH} AND c.digest=$1{suffix}"))
        .bind(digest(secret)?)
        .fetch_optional(&mut *c)
        .await?
        .ok_or(IdentityError::Unauthorized)?;
    principal(c, row).await
}
async fn session(c: &mut PgConnection, secret: &str) -> Result<Principal> {
    let actor = authenticate(c, secret, true).await?;
    if actor.credential_kind != CredentialKind::Session {
        return Err(IdentityError::Forbidden);
    }
    Ok(actor)
}
async fn lock_team(c: &mut PgConnection, team_id: i64) -> Result<PgRow> {
    sqlx::query("SELECT t.owner_user_id,t.version FROM core_identity.teams t JOIN core_identity.accounts a ON a.id=t.account_id WHERE t.id=$1 AND t.active AND a.active FOR UPDATE OF t FOR SHARE OF a")
        .bind(team_id).fetch_optional(c).await?.ok_or(IdentityError::Forbidden)
}
async fn team_role(
    c: &mut PgConnection,
    team: &PgRow,
    team_id: i64,
    user_id: i64,
) -> Result<(TeamRole, i64)> {
    if team.try_get::<i64, _>("owner_user_id")? == user_id {
        return Ok((TeamRole::Owner, 0));
    }
    let row = sqlx::query("SELECT role,version FROM core_identity.memberships WHERE team_id=$1 AND user_id=$2 AND active")
        .bind(team_id).bind(user_id).fetch_optional(c).await?.ok_or(IdentityError::Forbidden)?;
    Ok((
        parse_role(&row.try_get::<String, _>("role")?)?,
        row.try_get("version")?,
    ))
}

impl IdentityStore {
    pub fn from_pool(pool: PgPool) -> Self {
        Self { pool }
    }
    pub async fn connect(url: &str) -> Result<Self> {
        let pool = PgPoolOptions::new()
            .max_connections(8)
            .acquire_timeout(Duration::from_secs(3))
            .after_connect(|c, _| {
                Box::pin(async move {
                    sqlx::query("SET statement_timeout='5s'")
                        .execute(&mut *c)
                        .await?;
                    sqlx::query("SET idle_in_transaction_session_timeout='10s'")
                        .execute(&mut *c)
                        .await?;
                    Ok(())
                })
            })
            .connect(url)
            .await?;
        Ok(Self { pool })
    }
    /// Offline development bootstrap, not registration or an old-user import.
    pub async fn bootstrap_user(&self, user_id: i64, level: i16) -> Result<IssuedCredential> {
        if user_id <= 0 || !(0..=6).contains(&level) {
            return Err(IdentityError::Invalid);
        }
        let secret = new_secret("lmms_")?;
        let mut tx = self.pool.begin().await?;
        let role = match level {
            6 => "superadmin",
            5 => "admin",
            _ => "user",
        };
        let service_level = if level <= 4 { level } else { 0 };
        let account_id = new_account(&mut tx, "personal", service_level).await?;
        sqlx::query("INSERT INTO core_identity.users(id,personal_account_id,platform_role) VALUES ($1,$2,$3)")
            .bind(user_id).bind(account_id).bind(role).execute(&mut *tx).await?;
        let id: i64 = sqlx::query_scalar("INSERT INTO core_identity.credentials(digest,kind,user_id,user_version,owner_account_id,expires_at) VALUES ($1,'session',$2,1,$3,clock_timestamp()+$4::bigint*interval '1 second') RETURNING id")
            .bind(digest(&secret)?).bind(user_id).bind(account_id).bind(SESSION_TTL).fetch_one(&mut *tx).await?;
        let actor = authenticate(&mut tx, &secret, false).await?;
        audit(
            &mut tx,
            &actor,
            "user.bootstrap",
            json!({"user_id":user_id,"account_id":account_id}),
        )
        .await?;
        tx.commit().await?;
        Ok(IssuedCredential { id, secret })
    }
    pub async fn authorize(&self, secret: &str) -> Result<Principal> {
        let mut tx = self.pool.begin().await?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
            .execute(&mut *tx)
            .await?;
        let actor = authenticate(&mut tx, secret, false).await?;
        tx.commit().await?;
        Ok(actor)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn secrets_are_random_bounded_and_not_normalized() {
        let first = new_secret("lmmk_").unwrap();
        let second = new_secret("lmmk_").unwrap();
        assert_eq!(first.len(), 69);
        assert_ne!(digest(&first).unwrap(), digest(&second).unwrap());
        assert_ne!(
            digest(&first).unwrap(),
            digest(&first.to_uppercase()).unwrap()
        );
        assert_eq!(
            digest(&format!(" {first}")),
            Err(IdentityError::Unauthorized)
        );
        assert_eq!(digest(&"a".repeat(257)), Err(IdentityError::Unauthorized));
        assert_eq!(valid_ttl(0, MAX_TTL), Err(IdentityError::Invalid));
        assert_eq!(valid_ttl(MAX_TTL + 1, MAX_TTL), Err(IdentityError::Invalid));
    }
}
