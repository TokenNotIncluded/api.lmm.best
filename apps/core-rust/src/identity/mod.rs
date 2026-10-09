//! Core-owned identity state. No Go calls, cached grants, passwords or balances.
//! Identity checks are not reservations. Billing must recheck authority in its
//! own transaction before taking funds. Model routes remain unavailable.
mod credentials;
mod teams;
pub use teams::{InviteRequest, TeamSummary};

use crate::{
    accounts::{Account, AccountKind, TeamRole},
    funding::FundingPolicy,
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

pub static MIGRATOR: sqlx::migrate::Migrator = sqlx::migrate!("./migrations");
const MAX_TTL: i64 = 31_536_000;
const SESSION_TTL: i64 = 604_800;
const AUTH: &str = "SELECT c.id, c.kind, c.user_id, c.user_version, c.team_id, c.funding_policy, u.platform_level FROM core_identity.credentials c JOIN core_identity.users u ON u.id=c.user_id WHERE c.revoked_at IS NULL AND c.expires_at>clock_timestamp() AND u.active AND c.user_version=u.auth_version";

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
        // Never log connection strings, SQL parameters or bearer tokens.
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
async fn principal(c: &mut PgConnection, row: PgRow) -> Result<Principal> {
    let user_id: i64 = row.try_get("user_id")?;
    let id: i64 = row.try_get("id")?;
    let team_id: Option<i64> = row.try_get("team_id")?;
    let kind = match row.try_get::<String, _>("kind")?.as_str() {
        "session" => CredentialKind::Session,
        "api_key" => CredentialKind::ApiKey,
        _ => return Err(IdentityError::Storage),
    };
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
    let funding_accounts = if kind == CredentialKind::ApiKey {
        let policy: FundingPolicy = serde_json::from_value(row.try_get("funding_policy")?)
            .map_err(|_| IdentityError::Storage)?;
        let grants: Vec<i64> = sqlx::query_scalar("SELECT g.team_id FROM core_identity.credential_grants g JOIN core_identity.teams t ON t.id=g.team_id LEFT JOIN core_identity.memberships m ON m.team_id=t.id AND m.user_id=$2 WHERE g.credential_id=$1 AND t.active AND t.version=g.team_version AND ((t.owner_user_id=$2 AND g.membership_version=0) OR (t.owner_user_id<>$2 AND m.active AND m.can_spend AND m.version=g.membership_version))")
            .bind(id).bind(user_id).fetch_all(&mut *c).await?;
        policy
            .resolve(owner, &grants.into_iter().collect())
            .map_err(|_| IdentityError::Forbidden)?
    } else {
        Vec::new()
    };
    Ok(Principal {
        credential_id: id,
        credential_kind: kind,
        user_id,
        platform_level: row.try_get("platform_level")?,
        owner,
        funding_accounts,
        user_version: row.try_get("user_version")?,
    })
}
async fn authenticate(c: &mut PgConnection, secret: &str, write: bool) -> Result<Principal> {
    let suffix = if write { " FOR SHARE OF c,u" } else { "" };
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
    sqlx::query(
        "SELECT owner_user_id,version FROM core_identity.teams WHERE id=$1 AND active FOR UPDATE",
    )
    .bind(team_id)
    .fetch_optional(c)
    .await?
    .ok_or(IdentityError::Forbidden)
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
    /// Explicit operator command only; serving HTTP never runs migrations.
    pub async fn migrate(&self) -> Result<()> {
        MIGRATOR
            .run(&self.pool)
            .await
            .map_err(|_| IdentityError::Storage)
    }
    pub async fn check_schema(&self) -> Result<()> {
        let version: i32 =
            sqlx::query_scalar("SELECT version FROM core_identity.schema_version WHERE singleton")
                .fetch_one(&self.pool)
                .await?;
        if version == 1 {
            Ok(())
        } else {
            Err(IdentityError::Storage)
        }
    }
    /// Offline bootstrap, not a public registration endpoint or an upsert.
    pub async fn bootstrap_user(&self, user_id: i64, level: i16) -> Result<IssuedCredential> {
        if user_id <= 0 || !(0..=6).contains(&level) {
            return Err(IdentityError::Invalid);
        }
        let secret = new_secret("lmms_")?;
        let mut tx = self.pool.begin().await?;
        sqlx::query("INSERT INTO core_identity.users(id,platform_level) VALUES ($1,$2)")
            .bind(user_id)
            .bind(level)
            .execute(&mut *tx)
            .await?;
        let id: i64 = sqlx::query_scalar("INSERT INTO core_identity.credentials(digest,kind,user_id,user_version,expires_at) VALUES ($1,'session',$2,1,clock_timestamp()+$3::bigint*interval '1 second') RETURNING id")
            .bind(digest(&secret)?).bind(user_id).bind(SESSION_TTL).fetch_one(&mut *tx).await?;
        let actor = authenticate(&mut tx, &secret, false).await?;
        audit(
            &mut tx,
            &actor,
            "user.bootstrap",
            json!({"user_id":user_id}),
        )
        .await?;
        tx.commit().await?;
        Ok(IssuedCredential { id, secret })
    }
    pub async fn authorize(&self, secret: &str) -> Result<Principal> {
        let mut tx = self.pool.begin().await?;
        // Coherent authority snapshot, without locking each team on reads.
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
