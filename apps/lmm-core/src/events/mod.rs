//! Transactional event transport, not a ledger or a business authorization layer.
//! Publishers use their existing transaction. No method calls an extension.
mod inbox;
pub use inbox::{CommandKey, InboxOutcome, begin_command, complete_command};

use rand::{Rng, RngCore, rngs::OsRng};
use sha2::{Digest, Sha256};
use sqlx::{PgConnection, PgPool, Postgres, Row, Transaction, postgres::PgPoolOptions};
use std::{fmt, time::Duration};
use subtle::ConstantTimeEq;

pub const SCHEMA: &str = include_str!("../../schema/events.sql");
pub const MAX_PAYLOAD: usize = 16 * 1024;
pub const MAX_BATCH: u32 = 8;
pub const MAX_BATCH_BYTES: usize = 48 * 1024;
pub const FEATURES: &[&str] = &["events.pull.v1", "events.ack.v1", "events.retry.v1"];

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EventError {
    Invalid,
    Forbidden,
    Missing,
    Conflict,
    LeaseLost,
    Full,
    Busy,
    Storage,
}
impl fmt::Display for EventError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{self:?}")
    }
}
impl std::error::Error for EventError {}
impl From<sqlx::Error> for EventError {
    fn from(error: sqlx::Error) -> Self {
        match error.as_database_error().and_then(|e| e.code()).as_deref() {
            Some("23505") => Self::Conflict,
            Some("23514") => Self::Invalid,
            Some("40001" | "40P01" | "55P03") => Self::Busy,
            _ => Self::Storage,
        }
    }
}
pub type Result<T> = std::result::Result<T, EventError>;
pub fn valid_name(value: &str, max: usize) -> bool {
    !value.is_empty()
        && value.len() <= max
        && value
            .bytes()
            .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b"._-".contains(&b))
}
fn visible(value: &str, min: usize, max: usize) -> bool {
    (min..=max).contains(&value.len()) && value.bytes().all(|b| b.is_ascii_graphic())
}
#[derive(Clone, Copy)]
pub struct NewEvent<'a> {
    pub key: &'a str,
    pub event_type: &'a str,
    pub schema_version: u32,
    pub resource_id: i64,
    pub resource_version: i64,
    pub payload: &'a [u8],
}
impl NewEvent<'_> {
    fn validate(&self) -> Result<()> {
        if !visible(self.key, 1, 128)
            || !valid_name(self.event_type, 96)
            || self.schema_version == 0
            || self.schema_version > i32::MAX as u32
            || self.resource_id <= 0
            || self.resource_version < 0
            || self.payload.len() > MAX_PAYLOAD
        {
            return Err(EventError::Invalid);
        }
        Ok(())
    }
    /// Canonical domain digest, not a hash of a language-specific re-encoding.
    pub fn fingerprint(&self) -> [u8; 32] {
        fn field(h: &mut Sha256, bytes: &[u8]) {
            h.update((bytes.len() as u32).to_be_bytes());
            h.update(bytes);
        }
        let mut h = Sha256::new();
        h.update(b"lmm.event.v1\0");
        field(&mut h, self.key.as_bytes());
        field(&mut h, self.event_type.as_bytes());
        h.update(self.schema_version.to_be_bytes());
        h.update(self.resource_id.to_be_bytes());
        h.update(self.resource_version.to_be_bytes());
        field(&mut h, self.payload);
        h.finalize().into()
    }
}
#[derive(Debug)]
pub struct StoredEvent {
    pub id: i64,
    pub key: String,
    pub event_type: String,
    pub schema_version: u32,
    pub resource_id: i64,
    pub resource_version: i64,
    pub payload: Vec<u8>,
    pub fingerprint: Vec<u8>,
}
// Do not derive Debug: lease tokens are short-lived acknowledgement capabilities.
pub struct Delivery {
    pub event: StoredEvent,
    pub lease_token: Vec<u8>,
    pub attempt: u32,
    pub lease_until_unix_ms: i64,
}
#[derive(Clone)]
pub struct EventStore {
    pool: PgPool,
    lease_seconds: u32,
}
impl EventStore {
    pub fn from_pool(pool: PgPool) -> Self {
        Self {
            pool,
            lease_seconds: 30,
        }
    }
    /// Separate read/write pool for event delivery. Never use the read-only
    /// identity RPC pool, or consume the public model request pool.
    pub async fn connect(url: &str) -> Result<Self> {
        let pool = PgPoolOptions::new()
            .max_connections(2)
            .acquire_timeout(Duration::from_millis(400))
            .after_connect(|c, _| {
                Box::pin(async move {
                    sqlx::query("SET statement_timeout='1500ms'")
                        .execute(&mut *c)
                        .await?;
                    sqlx::query("SET lock_timeout='250ms'")
                        .execute(&mut *c)
                        .await?;
                    sqlx::query("SET idle_in_transaction_session_timeout='2s'")
                        .execute(&mut *c)
                        .await?;
                    Ok(())
                })
            })
            .connect(url)
            .await?;
        // Verify availability only. Schema installation is a separate operation.
        sqlx::query("SELECT singleton FROM core_events.capacity WHERE singleton")
            .fetch_one(&pool)
            .await?;
        Ok(Self::from_pool(pool))
    }
    pub fn with_lease_seconds(mut self, seconds: u32) -> Result<Self> {
        if !(1..=300).contains(&seconds) {
            return Err(EventError::Invalid);
        }
        self.lease_seconds = seconds;
        Ok(self)
    }
    pub fn lease_seconds(&self) -> u32 {
        self.lease_seconds
    }

    /// Administrative configuration, not an RPC. New subscribers receive every
    /// matching retained payload; already compacted history is not replayed.
    /// Exact repeats are safe. Changes to an existing subscription are rejected.
    pub async fn provision(
        tx: &mut Transaction<'_, Postgres>,
        consumer: &str,
        service: &str,
        event_types: &[String],
        max_pending: i64,
    ) -> Result<()> {
        if !valid_name(consumer, 64)
            || !valid_name(service, 64)
            || event_types.is_empty()
            || event_types.len() > 32
            || event_types.iter().any(|t| !valid_name(t, 96))
            || !(1..=1_000_000).contains(&max_pending)
        {
            return Err(EventError::Invalid);
        }
        let mut types = event_types.to_vec();
        types.sort();
        types.dedup();
        lock_capacity(tx).await?;
        if let Some(row) = sqlx::query("SELECT service_id,event_types,max_pending FROM core_events.subscriptions WHERE consumer_id=$1")
            .bind(consumer).fetch_optional(&mut **tx).await? {
            return if row.try_get::<String,_>("service_id")? == service
                && row.try_get::<Vec<String>,_>("event_types")? == types
                && row.try_get::<i64,_>("max_pending")? == max_pending { Ok(()) } else { Err(EventError::Conflict) };
        }
        let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_events.subscriptions")
            .fetch_one(&mut **tx)
            .await?;
        if count >= 64 {
            return Err(EventError::Full);
        }
        let pending: i64 = sqlx::query_scalar("SELECT count(*) FROM core_events.outbox WHERE payload IS NOT NULL AND event_type=ANY($1)")
            .bind(&types).fetch_one(&mut **tx).await?;
        if pending > max_pending {
            return Err(EventError::Full);
        }
        sqlx::query("INSERT INTO core_events.subscriptions(consumer_id,service_id,event_types,max_pending,pending) VALUES($1,$2,$3,$4,$5)")
            .bind(consumer).bind(service).bind(&types).bind(max_pending).bind(pending).execute(&mut **tx).await?;
        sqlx::query("INSERT INTO core_events.deliveries(consumer_id,event_id) SELECT $1,id FROM core_events.outbox WHERE payload IS NOT NULL AND event_type=ANY($2)")
            .bind(consumer).bind(&types).execute(&mut **tx).await?;
        sqlx::query("UPDATE core_events.outbox SET pending_deliveries=pending_deliveries+1 WHERE payload IS NOT NULL AND event_type=ANY($1)")
            .bind(&types).execute(&mut **tx).await?;
        Ok(())
    }
    /// Call near the end of the business transaction; commit both or neither.
    /// On Busy/Full/error the caller must roll back the ENTIRE business write.
    pub async fn publish(tx: &mut Transaction<'_, Postgres>, event: NewEvent<'_>) -> Result<i64> {
        event.validate()?;
        let fingerprint = event.fingerprint();
        let capacity = lock_capacity(tx).await?;
        if let Some(row) =
            sqlx::query("SELECT id,content_sha256 FROM core_events.outbox WHERE event_key=$1")
                .bind(event.key)
                .fetch_optional(&mut **tx)
                .await?
        {
            return if row.try_get::<Vec<u8>, _>("content_sha256")? == fingerprint {
                Ok(row.try_get("id")?)
            } else {
                Err(EventError::Conflict)
            };
        }
        let bytes = event.payload.len() as i64;
        if capacity.try_get::<i64, _>("pending_events")?
            >= capacity.try_get::<i64, _>("max_events")?
            || bytes
                > capacity.try_get::<i64, _>("max_bytes")?
                    - capacity.try_get::<i64, _>("pending_bytes")?
        {
            return Err(EventError::Full);
        }
        let full: bool = sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM core_events.subscriptions WHERE $1=ANY(event_types) AND pending>=max_pending)")
            .bind(event.event_type).fetch_one(&mut **tx).await?;
        if full {
            return Err(EventError::Full);
        }
        let id: i64 = sqlx::query_scalar("INSERT INTO core_events.outbox(event_key,event_type,schema_version,resource_id,resource_version,payload,content_sha256,pending_deliveries) VALUES($1,$2,$3,$4,$5,$6,$7,(SELECT count(*) FROM core_events.subscriptions WHERE $2=ANY(event_types))) RETURNING id")
            .bind(event.key).bind(event.event_type).bind(event.schema_version as i32)
            .bind(event.resource_id).bind(event.resource_version).bind(event.payload)
            .bind(fingerprint.as_slice()).fetch_one(&mut **tx).await?;
        sqlx::query("INSERT INTO core_events.deliveries(consumer_id,event_id) SELECT consumer_id,$1 FROM core_events.subscriptions WHERE $2=ANY(event_types)")
            .bind(id).bind(event.event_type).execute(&mut **tx).await?;
        sqlx::query(
            "UPDATE core_events.subscriptions SET pending=pending+1 WHERE $1=ANY(event_types)",
        )
        .bind(event.event_type)
        .execute(&mut **tx)
        .await?;
        sqlx::query("UPDATE core_events.capacity SET pending_events=pending_events+1,pending_bytes=pending_bytes+$1 WHERE singleton")
            .bind(bytes).execute(&mut **tx).await?;
        Ok(id)
    }
    pub async fn event_types(&self, service: &str, consumer: &str) -> Result<Vec<String>> {
        let mut c = self.pool.acquire().await?;
        subscription(&mut c, service, consumer).await
    }
    /// No cursor: sequence allocation order is not transaction commit order.
    pub async fn pull(&self, service: &str, consumer: &str, limit: u32) -> Result<Vec<Delivery>> {
        if limit == 0 || limit > MAX_BATCH {
            return Err(EventError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        subscription(&mut tx, service, consumer).await?;
        let rows = sqlx::query("SELECT o.id,o.event_key,o.event_type,o.schema_version,o.resource_id,o.resource_version,o.payload,o.content_sha256 FROM core_events.deliveries d JOIN core_events.outbox o ON o.id=d.event_id WHERE d.consumer_id=$1 AND d.acknowledged_at IS NULL AND d.available_at<=clock_timestamp() ORDER BY d.available_at,d.event_id LIMIT $2 FOR UPDATE OF d SKIP LOCKED")
            .bind(consumer).bind(i64::from(limit)).fetch_all(&mut *tx).await?;
        let mut deliveries = Vec::new();
        let mut used = 0;
        for row in rows {
            let event = StoredEvent {
                id: row.try_get("id")?,
                key: row.try_get("event_key")?,
                event_type: row.try_get("event_type")?,
                schema_version: row.try_get::<i32, _>("schema_version")? as u32,
                resource_id: row.try_get("resource_id")?,
                resource_version: row.try_get("resource_version")?,
                payload: row.try_get("payload")?,
                fingerprint: row.try_get("content_sha256")?,
            };
            let size = event.payload.len() + event.key.len() + event.event_type.len() + 256;
            if used + size > MAX_BATCH_BYTES {
                break;
            }
            used += size;
            let mut token = [0u8; 32];
            OsRng
                .try_fill_bytes(&mut token)
                .map_err(|_| EventError::Storage)?;
            let digest: [u8; 32] = Sha256::digest(token).into();
            let lease = sqlx::query("UPDATE core_events.deliveries SET attempts=LEAST(attempts,2147483646)+1,lease_digest=$3,lease_until=clock_timestamp()+($4::integer*interval '1 second'),available_at=clock_timestamp()+($4::integer*interval '1 second'),retry_reason=NULL WHERE consumer_id=$1 AND event_id=$2 RETURNING attempts,(extract(epoch FROM lease_until)*1000)::bigint AS until_ms")
                .bind(consumer).bind(event.id).bind(digest.as_slice()).bind(i32::try_from(self.lease_seconds).map_err(|_| EventError::Invalid)?)
                .fetch_one(&mut *tx).await?;
            deliveries.push(Delivery {
                event,
                lease_token: token.to_vec(),
                attempt: lease.try_get::<i32, _>("attempts")? as u32,
                lease_until_unix_ms: lease.try_get("until_ms")?,
            });
        }
        tx.commit().await?;
        Ok(deliveries)
    }
    /// Returns true for an exact repeat of an already committed acknowledgement.
    pub async fn acknowledge(
        &self,
        service: &str,
        consumer: &str,
        id: i64,
        token: &[u8],
    ) -> Result<bool> {
        check_receipt(id, token)?;
        let mut tx = self.pool.begin().await?;
        match acknowledge_transaction(&mut tx, service, consumer, id, token).await {
            Ok(repeated) => {
                tx.commit().await?;
                Ok(repeated)
            }
            Err(error) => {
                // Drop only queues rollback. Finish it before reporting a
                // rejected receipt so a following request can take NOWAIT locks.
                tx.rollback().await?;
                Err(error)
            }
        }
    }
    /// Durable capped exponential retry; never drops a poison event. Repeating
    /// the same release does not move its next attempt further into the future.
    pub async fn retry(
        &self,
        service: &str,
        consumer: &str,
        id: i64,
        token: &[u8],
        reason: i32,
    ) -> Result<bool> {
        check_receipt(id, token)?;
        if !(1..=3).contains(&reason) {
            return Err(EventError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        subscription(&mut tx, service, consumer).await?;
        let row = receipt(&mut tx, consumer, id, token).await?;
        let acked: bool = row.try_get("acked")?;
        if acked || !row.try_get::<bool, _>("leased")? {
            tx.commit().await?;
            return Ok(acked);
        }
        let attempt: i32 = row.try_get("attempts")?;
        let delay = 250_i64 * (1_i64 << attempt.min(8)) + rand::thread_rng().gen_range(0..=250_i64);
        sqlx::query("UPDATE core_events.deliveries SET lease_until=NULL,available_at=clock_timestamp()+($3::bigint*interval '1 millisecond'),retry_reason=$4 WHERE consumer_id=$1 AND event_id=$2")
            .bind(consumer).bind(id).bind(delay).bind(reason as i16).execute(&mut *tx).await?;
        tx.commit().await?;
        Ok(false)
    }
}
async fn acknowledge_transaction(
    c: &mut PgConnection,
    service: &str,
    consumer: &str,
    id: i64,
    token: &[u8],
) -> Result<bool> {
    lock_capacity(c).await?;
    subscription(c, service, consumer).await?;
    let row = receipt(c, consumer, id, token).await?;
    if row.try_get::<bool, _>("acked")? {
        return Ok(true);
    }
    if !row.try_get::<bool, _>("leased")? {
        return Err(EventError::LeaseLost);
    }
    sqlx::query("UPDATE core_events.deliveries SET acknowledged_at=clock_timestamp(),lease_until=NULL WHERE consumer_id=$1 AND event_id=$2")
        .bind(consumer).bind(id).execute(&mut *c).await?;
    sqlx::query("UPDATE core_events.subscriptions SET pending=pending-1 WHERE consumer_id=$1")
        .bind(consumer)
        .execute(&mut *c)
        .await?;
    let event = sqlx::query("UPDATE core_events.outbox SET pending_deliveries=pending_deliveries-1 WHERE id=$1 RETURNING pending_deliveries,octet_length(payload) AS bytes")
        .bind(id).fetch_one(&mut *c).await?;
    if event.try_get::<i64, _>("pending_deliveries")? == 0 {
        let bytes: i32 = event.try_get("bytes")?;
        sqlx::query("UPDATE core_events.outbox SET payload=NULL WHERE id=$1")
            .bind(id)
            .execute(&mut *c)
            .await?;
        sqlx::query("UPDATE core_events.capacity SET pending_events=pending_events-1,pending_bytes=pending_bytes-$1 WHERE singleton")
            .bind(i64::from(bytes)).execute(&mut *c).await?;
    }
    Ok(false)
}
async fn lock_capacity(c: &mut PgConnection) -> Result<sqlx::postgres::PgRow> {
    // Immediate admission failure instead of an unbounded wait on business work.
    Ok(
        sqlx::query("SELECT * FROM core_events.capacity WHERE singleton FOR UPDATE NOWAIT")
            .fetch_one(c)
            .await?,
    )
}
async fn subscription(c: &mut PgConnection, service: &str, consumer: &str) -> Result<Vec<String>> {
    if !valid_name(service, 64) || !valid_name(consumer, 64) {
        return Err(EventError::Invalid);
    }
    let row = sqlx::query(
        "SELECT service_id,event_types FROM core_events.subscriptions WHERE consumer_id=$1",
    )
    .bind(consumer)
    .fetch_optional(c)
    .await?
    .ok_or(EventError::Missing)?;
    if row.try_get::<String, _>("service_id")? != service {
        return Err(EventError::Forbidden);
    }
    Ok(row.try_get("event_types")?)
}
fn check_receipt(id: i64, token: &[u8]) -> Result<()> {
    if id <= 0 || token.len() != 32 {
        return Err(EventError::Invalid);
    }
    Ok(())
}
async fn receipt(
    c: &mut PgConnection,
    consumer: &str,
    id: i64,
    token: &[u8],
) -> Result<sqlx::postgres::PgRow> {
    let row = sqlx::query("SELECT lease_digest,attempts,acknowledged_at IS NOT NULL AS acked,lease_until IS NOT NULL AS leased FROM core_events.deliveries WHERE consumer_id=$1 AND event_id=$2 FOR UPDATE")
        .bind(consumer).bind(id).fetch_optional(c).await?.ok_or(EventError::Missing)?;
    let stored: Option<Vec<u8>> = row.try_get("lease_digest")?;
    let digest = Sha256::digest(token);
    if !stored.is_some_and(|v| bool::from(v.as_slice().ct_eq(digest.as_slice()))) {
        return Err(EventError::LeaseLost);
    }
    Ok(row)
}

#[cfg(test)]
mod tests;
