//! A completed replay record and the business write share one transaction.
//! Authorization must run before begin_command, and be rechecked in that same
//! transaction. Scope uses the authenticated actor, never a caller-supplied ID.
use super::{EventError, Result, valid_name, visible};
use sha2::{Digest, Sha256};
use sqlx::{Postgres, Row, Transaction};

pub struct CommandKey<'a> {
    pub service_id: &'a str,
    pub actor_id: i64,
    pub method: &'a str,
    pub idempotency_key: &'a str,
}
#[derive(Debug, PartialEq, Eq)]
pub enum InboxOutcome {
    New,
    Replay(Vec<u8>),
}
impl CommandKey<'_> {
    fn validate(&self) -> Result<()> {
        if !valid_name(self.service_id, 64)
            || self.actor_id <= 0
            || !visible(self.method, 1, 128)
            || !visible(self.idempotency_key, 16, 128)
        {
            return Err(EventError::Invalid);
        }
        Ok(())
    }
}
/// canonical_request is a domain-defined canonical representation. Raw protobuf
/// re-encodings are not necessarily canonical. Bound and version that encoding.
pub async fn begin_command(
    tx: &mut Transaction<'_, Postgres>,
    key: &CommandKey<'_>,
    canonical_request: &[u8],
) -> Result<InboxOutcome> {
    key.validate()?;
    if canonical_request.len() > 16384 {
        return Err(EventError::Invalid);
    }
    let digest = Sha256::digest(canonical_request);
    let inserted = sqlx::query("INSERT INTO core_events.inbox(service_id,actor_id,method,idempotency_key,request_sha256) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING")
        .bind(key.service_id).bind(key.actor_id).bind(key.method).bind(key.idempotency_key).bind(digest.as_slice())
        .execute(&mut **tx).await?.rows_affected();
    if inserted == 1 {
        return Ok(InboxOutcome::New);
    }
    let row = sqlx::query("SELECT request_sha256,response FROM core_events.inbox WHERE service_id=$1 AND actor_id=$2 AND method=$3 AND idempotency_key=$4 FOR UPDATE")
        .bind(key.service_id).bind(key.actor_id).bind(key.method).bind(key.idempotency_key).fetch_one(&mut **tx).await?;
    if row.try_get::<Vec<u8>, _>("request_sha256")? != digest.as_slice() {
        return Err(EventError::Conflict);
    }
    let response: Option<Vec<u8>> = row.try_get("response")?;
    response
        .map(InboxOutcome::Replay)
        .ok_or(EventError::Conflict)
}
pub async fn complete_command(
    tx: &mut Transaction<'_, Postgres>,
    key: &CommandKey<'_>,
    response: &[u8],
) -> Result<()> {
    key.validate()?;
    if response.len() > 16384 {
        return Err(EventError::Invalid);
    }
    let updated = sqlx::query("UPDATE core_events.inbox SET response=$5,completed_at=clock_timestamp() WHERE service_id=$1 AND actor_id=$2 AND method=$3 AND idempotency_key=$4 AND response IS NULL")
        .bind(key.service_id).bind(key.actor_id).bind(key.method).bind(key.idempotency_key).bind(response)
        .execute(&mut **tx).await?.rows_affected();
    if updated != 1 {
        return Err(EventError::Conflict);
    }
    Ok(())
}
