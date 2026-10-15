//! Core-private abuse policy and resource isolation. Never changes financial truth.
pub mod admin;
mod policy;
use crate::identity::{CredentialKind, IdentityError, Principal};
pub use policy::{
    ClientProtocol, Limits, Mode, Policy, PolicyConfig, UpstreamProtocol, UpstreamTarget,
};
use rand::{RngCore, rngs::OsRng};
use serde::Serialize;
use serde_json::Value;
use sha2::{Digest, Sha256};
use sqlx::{PgConnection, PgPool, Row};
use std::{collections::BTreeSet, fmt, net::IpAddr, sync::Arc};

const RESOURCE_LOCK: i64 = 741_803_140;
pub const SCHEMA: &str = include_str!("../../schema/abuse.sql");
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum AbuseError {
    Unauthorized,
    Forbidden,
    Invalid,
    NotFound,
    OperationNotFound,
    Conflict,
    PreviewExpired,
    RateLimited,
    Unavailable,
}
impl fmt::Display for AbuseError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{self:?}")
    }
}
impl std::error::Error for AbuseError {}
impl From<sqlx::Error> for AbuseError {
    fn from(e: sqlx::Error) -> Self {
        if e.as_database_error()
            .is_some_and(|d| matches!(d.code().as_deref(), Some("23505" | "40001" | "40P01")))
        {
            Self::Conflict
        } else {
            Self::Unavailable
        }
    }
}
impl From<IdentityError> for AbuseError {
    fn from(e: IdentityError) -> Self {
        match e {
            IdentityError::Unauthorized => Self::Unauthorized,
            IdentityError::Forbidden => Self::Forbidden,
            IdentityError::Invalid => Self::Invalid,
            IdentityError::Conflict => Self::Conflict,
            IdentityError::Storage => Self::Unavailable,
        }
    }
}
pub type Result<T> = std::result::Result<T, AbuseError>;
#[derive(Clone)]
pub struct AbuseStore {
    pool: PgPool,
    aliases: Arc<BTreeSet<String>>,
}
pub struct ModelAdmission {
    pub lease_id: String,
    pub user_id: i64,
    pub credential_id: i64,
    pub policy_id: String,
    pub policy_revision: i64,
    pub mode: Mode,
    pub fixed_text: Option<String>,
    pub upstream: Option<UpstreamTarget>,
    pub limits: Limits,
    pub expires_at: i64,
}
pub struct ModelCatalog {
    pub public_models: Vec<String>,
}
pub(super) fn hash(value: &impl Serialize) -> Result<Vec<u8>> {
    Ok(Sha256::digest(serde_json::to_vec(value).map_err(|_| AbuseError::Invalid)?).to_vec())
}
/// Cross-client receipt for the policy actually applied. Objects sort by key;
/// arrays preserve order, UTF-8 stays literal, and no trailing newline is added.
pub fn config_hash(config: &PolicyConfig) -> Result<String> {
    fn sorted(value: Value) -> Value {
        match value {
            Value::Object(map) => {
                let entries: std::collections::BTreeMap<_, _> = map.into_iter().collect();
                Value::Object(entries.into_iter().map(|(k, v)| (k, sorted(v))).collect())
            }
            Value::Array(values) => Value::Array(values.into_iter().map(sorted).collect()),
            scalar => scalar,
        }
    }
    let value = sorted(serde_json::to_value(config).map_err(|_| AbuseError::Invalid)?);
    Ok(hex(&Sha256::digest(
        serde_json::to_vec(&value).map_err(|_| AbuseError::Invalid)?,
    )))
}
pub(super) fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}
fn random_id() -> Result<String> {
    let mut b = [0; 32];
    OsRng
        .try_fill_bytes(&mut b)
        .map_err(|_| AbuseError::Unavailable)?;
    Ok(hex(&b))
}
/// A missing component, missing singleton, denied read or invalid policy is a
/// runtime failure, never evidence that a user is unmarked.
pub async fn is_marked_in_transaction(c: &mut PgConnection, user_id: i64) -> Result<bool> {
    Ok(selected(c, user_id).await?.is_some())
}
pub async fn reject_normal_model_in_transaction(c: &mut PgConnection, user_id: i64) -> Result<()> {
    if is_marked_in_transaction(c, user_id).await? {
        Err(AbuseError::Forbidden)
    } else {
        Ok(())
    }
}
/// Narrow key-management/catalog capability. Caller already holds the user's
/// canonical session authority lock; this never grants a real routing group.
pub async fn public_models_in_transaction(
    c: &mut PgConnection,
    user_id: i64,
) -> Result<Option<Vec<String>>> {
    Ok(selected(c, user_id).await?.map(|(_, p)| p.public_models))
}
pub(super) async fn configuration(c: &mut PgConnection) -> Result<(i64, PolicyConfig)> {
    let r =
        sqlx::query("SELECT revision,document FROM core_abuse.settings WHERE singleton FOR SHARE")
            .fetch_one(c)
            .await?;
    let cfg = serde_json::from_value(r.try_get::<Value, _>("document")?)
        .map_err(|_| AbuseError::Unavailable)?;
    Ok((r.try_get("revision")?, cfg))
}
pub(super) async fn resource_lock(c: &mut PgConnection) -> Result<()> {
    sqlx::query("SELECT pg_advisory_xact_lock($1)")
        .bind(RESOURCE_LOCK)
        .execute(c)
        .await?;
    Ok(())
}
async fn api_key(
    c: &mut PgConnection,
    secret: &str,
    model: Option<&str>,
    peer: Option<IpAddr>,
) -> Result<(Principal, Option<Vec<String>>)> {
    let actor = crate::identity::authorize_in_transaction(c, secret).await?;
    if actor.credential_kind != CredentialKind::ApiKey {
        return Err(AbuseError::Forbidden);
    }
    let r=sqlx::query("SELECT enabled,model_limits_enabled,model_limits,cardinality(allow_ips)=0 OR ($2::text IS NOT NULL AND $2::inet <<= ANY(allow_ips)) AS ip_allowed FROM core_identity.api_key_settings WHERE credential_id=$1 FOR SHARE")
        .bind(actor.credential_id).bind(peer.map(|v|v.to_string())).fetch_optional(c).await?;
    let limits = if let Some(r) = r {
        if !r.try_get::<bool, _>("enabled")? || !r.try_get::<bool, _>("ip_allowed")? {
            return Err(AbuseError::Forbidden);
        }
        if r.try_get::<bool, _>("model_limits_enabled")? {
            Some(r.try_get::<Vec<String>, _>("model_limits")?)
        } else {
            None
        }
    } else {
        None
    };
    if model.is_some_and(|m| limits.as_ref().is_some_and(|v| !v.iter().any(|x| x == m))) {
        return Err(AbuseError::Forbidden);
    }
    Ok((actor, limits))
}
async fn selected(c: &mut PgConnection, user_id: i64) -> Result<Option<(i64, Policy)>> {
    // Start from the REQUIRED settings singleton. A LEFT JOIN represents only
    // absence of this user's mark; it cannot conceal an absent settings row.
    // One plain SELECT also serves SELECT-only identity capability projection.
    let row = sqlx::query(
        "SELECT s.revision,s.document,m.marked,m.policy_id FROM core_abuse.settings s LEFT JOIN core_abuse.user_marks m ON m.user_id=$1 WHERE s.singleton",
    ).bind(user_id).fetch_one(c).await?;
    let revision: i64 = row.try_get("revision")?;
    let cfg: PolicyConfig =
        serde_json::from_value(row.try_get("document")?).map_err(|_| AbuseError::Unavailable)?;
    // Metadata helpers do not hold secret material. Validate the complete
    // persisted structure against its own alias references; model execution
    // additionally validates those references against the host's loaded aliases.
    let referenced: BTreeSet<String> = cfg
        .policies
        .iter()
        .flat_map(|p| p.clients.values().map(|t| t.credential_reference.clone()))
        .collect();
    cfg.validate(&referenced)
        .map_err(|_| AbuseError::Unavailable)?;
    if row.try_get::<Option<bool>, _>("marked")? != Some(true) {
        return Ok(None);
    }
    let id: String = row.try_get("policy_id")?;
    Ok(Some((
        revision,
        cfg.policies
            .into_iter()
            .find(|p| p.id == id)
            .ok_or(AbuseError::Unavailable)?,
    )))
}
impl AbuseStore {
    pub fn new(pool: PgPool, credential_references: BTreeSet<String>) -> Self {
        Self {
            pool,
            aliases: Arc::new(credential_references),
        }
    }
    pub async fn lookup_model_policy(
        &self,
        secret: &str,
        peer: Option<IpAddr>,
    ) -> Result<Option<ModelCatalog>> {
        let mut tx = self.pool.begin().await?;
        resource_lock(&mut tx).await?;
        let (actor, key_models) = api_key(&mut tx, secret, None, peer).await?;
        let result = selected(&mut tx, actor.user_id)
            .await?
            .map(|(_, policy)| ModelCatalog {
                public_models: policy
                    .public_models
                    .into_iter()
                    .filter(|m| key_models.as_ref().is_none_or(|v| v.contains(m)))
                    .collect(),
            });
        tx.commit().await?;
        Ok(result)
    }
    pub async fn admit_model(
        &self,
        secret: &str,
        protocol: ClientProtocol,
        model: &str,
        peer: Option<IpAddr>,
        request_id: &str,
    ) -> Result<Option<ModelAdmission>> {
        if request_id.is_empty()
            || request_id.len() > 128
            || !request_id.bytes().all(|b| b.is_ascii_graphic())
        {
            return Err(AbuseError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        resource_lock(&mut tx).await?;
        let (actor, _) = api_key(&mut tx, secret, Some(model), peer).await?;
        let Some((revision, mut policy)) = selected(&mut tx, actor.user_id).await? else {
            tx.commit().await?;
            return Ok(None);
        };
        if !policy.public_models.iter().any(|m| m == model) {
            return Err(AbuseError::Forbidden);
        }
        // Validate persisted policy again against this host's live credential aliases.
        PolicyConfig {
            version: 1,
            policies: vec![policy.clone()],
        }
        .validate(&self.aliases)
        .map_err(|_| AbuseError::Unavailable)?;
        let now: i64 =
            sqlx::query_scalar("SELECT floor(extract(epoch FROM clock_timestamp()))::bigint")
                .fetch_one(&mut *tx)
                .await?;
        sqlx::query("DELETE FROM core_abuse.model_leases WHERE id IN (SELECT id FROM core_abuse.model_leases WHERE expires_at<clock_timestamp()-interval '1 day' LIMIT 256)").execute(&mut *tx).await?;
        sqlx::query("DELETE FROM core_abuse.model_counters WHERE (subject_id,minute) IN (SELECT subject_id,minute FROM core_abuse.model_counters WHERE minute<$1 LIMIT 256)").bind(now/60-1440).execute(&mut *tx).await?;
        // Every authenticated, marked and valid-model admission attempt counts,
        // including duplicate IDs and concurrency/RPM denials. Commit a denial
        // only after BOTH counters are durable; database errors fail closed.
        let mut rate_limited = false;
        for (subject, limit) in [
            (0, policy.limits.requests_per_minute_global),
            (actor.user_id, policy.limits.requests_per_minute_per_user),
        ] {
            let attempts:i32=sqlx::query_scalar("INSERT INTO core_abuse.model_counters AS c(subject_id,minute,attempts) VALUES ($1,$2,1) ON CONFLICT(subject_id,minute) DO UPDATE SET attempts=LEAST(c.attempts+1,100001) RETURNING attempts")
                .bind(subject).bind(now/60).fetch_one(&mut *tx).await?;
            rate_limited |= i64::from(attempts) > i64::from(limit);
        }
        if rate_limited {
            tx.commit().await?;
            return Err(AbuseError::RateLimited);
        }
        let duplicate:bool=sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM core_abuse.model_leases WHERE credential_id=$1 AND request_id=$2)")
            .bind(actor.credential_id).bind(request_id).fetch_one(&mut *tx).await?;
        if duplicate {
            tx.commit().await?;
            return Err(AbuseError::Conflict);
        }
        let (global,user):(i64,i64)=sqlx::query_as("SELECT count(*),count(*) FILTER (WHERE user_id=$1) FROM core_abuse.model_leases WHERE finished_at IS NULL AND expires_at>clock_timestamp()")
            .bind(actor.user_id).fetch_one(&mut *tx).await?;
        if global >= i64::from(policy.limits.max_concurrent_global)
            || user >= i64::from(policy.limits.max_concurrent_per_user)
        {
            tx.commit().await?;
            return Err(AbuseError::RateLimited);
        }
        let lease_id = random_id()?;
        let expires_at = now + i64::from(policy.limits.total_timeout_seconds) + 5;
        let inserted:Option<String>=sqlx::query_scalar("INSERT INTO core_abuse.model_leases(id,user_id,credential_id,request_id,expires_at) VALUES ($1,$2,$3,$4,to_timestamp($5::double precision)) ON CONFLICT(credential_id,request_id) DO NOTHING RETURNING id")
            .bind(&lease_id).bind(actor.user_id).bind(actor.credential_id).bind(request_id).bind(expires_at).fetch_optional(&mut *tx).await?;
        if inserted.is_none() {
            tx.commit().await?;
            return Err(AbuseError::Conflict);
        }
        let upstream = policy.clients.remove(&protocol);
        let admission = ModelAdmission {
            lease_id,
            user_id: actor.user_id,
            credential_id: actor.credential_id,
            policy_id: policy.id,
            policy_revision: revision,
            mode: policy.mode,
            fixed_text: policy.fixed_text,
            upstream,
            limits: policy.limits,
            expires_at,
        };
        tx.commit().await?;
        Ok(Some(admission))
    }
    pub async fn finish_model(&self, lease_id: &str) -> Result<()> {
        if !policy::identifier(lease_id, 64, 64) {
            return Err(AbuseError::Invalid);
        }
        sqlx::query("UPDATE core_abuse.model_leases SET finished_at=COALESCE(finished_at,clock_timestamp()) WHERE id=$1")
            .bind(lease_id).execute(&self.pool).await?;
        Ok(())
    }
}
