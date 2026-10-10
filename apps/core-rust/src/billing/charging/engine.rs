use super::{authority, limits, types::*};
use serde::Serialize;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{PgConnection, PgPool, Postgres, Row, Transaction, postgres::PgRow};
use std::sync::Arc;
use subtle::ConstantTimeEq;

#[derive(Clone)]
pub struct Charging {
    pool: PgPool,
    ledger: Arc<dyn Ledger>,
}
struct Stored {
    charge: Charge,
    worker: Vec<u8>,
    spec: Vec<u8>,
}
fn json<T: Serialize>(value: &T) -> Result<Value> {
    serde_json::to_value(value).map_err(|_| Error::Invalid)
}
fn hash<T: Serialize>(value: &T) -> Result<Vec<u8>> {
    Ok(Sha256::digest(serde_json::to_vec(value).map_err(|_| Error::Invalid)?).to_vec())
}
fn decode(row: PgRow) -> Result<Stored> {
    let source = match row.try_get::<Option<i64>, _>("source_subscription_id")? {
        None => Source::Wallet,
        Some(id) => Source::Subscription {
            id,
            start: row.try_get("source_start")?,
            end: row.try_get("source_end")?,
        },
    };
    Ok(Stored {
        worker: row.try_get("worker_digest")?,
        spec: row.try_get("spec_digest")?,
        charge: Charge {
            id: row.try_get("id")?,
            actor_user_id: row.try_get("actor_user_id")?,
            key_id: row.try_get("key_id")?,
            owner_account_id: row.try_get("owner_account_id")?,
            payer_account_id: row.try_get("payer_account_id")?,
            price: serde_json::from_value(row.try_get("price")?).map_err(|_| Error::Storage)?,
            source,
            authorized_at: row.try_get("authorized_at")?,
            lease_until: row.try_get("lease_until")?,
            hard_deadline: row.try_get("hard_deadline")?,
            state: row.try_get("state")?,
            reserved: row.try_get("reserved")?,
            usage: Usage {
                input: row.try_get("input_tokens")?,
                cached: row.try_get("cached_tokens")?,
                output: row.try_get("output_tokens")?,
            },
            settled: row.try_get("settled")?,
            refunded: row.try_get("refunded")?,
            revision: row.try_get("revision")?,
            outcome: row.try_get("outcome")?,
        },
    })
}
async fn now(c: &mut PgConnection) -> Result<i64> {
    Ok(
        sqlx::query_scalar("SELECT floor(extract(epoch FROM clock_timestamp()))::bigint")
            .fetch_one(c)
            .await?,
    )
}
async fn lock(c: &mut PgConnection, id: &str) -> Result<()> {
    if !valid_id(id) {
        return Err(Error::Invalid);
    }
    sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended('core_charging:' || $1,0))")
        .bind(id)
        .execute(c)
        .await?;
    Ok(())
}
async fn load(c: &mut PgConnection, id: &str) -> Result<Stored> {
    sqlx::query("SELECT * FROM core_charging.requests WHERE id=$1 FOR UPDATE")
        .bind(id)
        .fetch_optional(c)
        .await?
        .map(decode)
        .transpose()?
        .ok_or(Error::NotFound)
}
fn worker_matches(stored: &Stored, worker: &[u8; 32]) -> Result<()> {
    if stored
        .worker
        .ct_eq(&authority::worker_digest(worker)?)
        .unwrap_u8()
        == 1
    {
        Ok(())
    } else {
        Err(Error::StaleWorker)
    }
}
fn live(charge: &Charge, at: i64) -> Result<()> {
    if at >= charge.lease_until || at >= charge.hard_deadline {
        Err(Error::StaleWorker)
    } else {
        Ok(())
    }
}
async fn commit(tx: Transaction<'_, Postgres>) -> Result<()> {
    tx.commit().await.map_err(|error| {
        match error.as_database_error().and_then(|e| e.code()).as_deref() {
            Some("40001" | "40P01") => Error::Retry,
            _ => Error::CommitUnknown,
        }
    })
}
fn command(charge: &Charge, tag: &str, change: Change) -> Result<LedgerCommand> {
    let operation_id = hash(&("core-charging-v1", &charge.id, tag))?
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect::<String>();
    Ok(LedgerCommand {
        operation_id,
        request_id: charge.id.clone(),
        actor_user_id: charge.actor_user_id,
        key_id: charge.key_id,
        owner_account_id: charge.owner_account_id,
        payer_account_id: charge.payer_account_id,
        price_version: charge.price.version.clone(),
        source: charge.source,
        change,
    })
}
async fn event(c: &mut PgConnection, id: &str, event_id: &str, detail: Value) -> Result<()> {
    sqlx::query(
        "INSERT INTO core_charging.usage_events(request_id,event_id,detail) VALUES ($1,$2,$3)",
    )
    .bind(id)
    .bind(event_id)
    .bind(detail)
    .execute(c)
    .await?;
    Ok(())
}

impl Charging {
    /// No default ledger. Supplying a test double never enables production.
    pub fn new(pool: PgPool, ledger: Arc<dyn Ledger>) -> Self {
        Self { pool, ledger }
    }
    async fn begin(&self) -> Result<Transaction<'_, Postgres>> {
        let mut tx = self.pool.begin().await?;
        for statement in [
            "SET TRANSACTION ISOLATION LEVEL SERIALIZABLE",
            "SET LOCAL lock_timeout='2s'",
            "SET LOCAL statement_timeout='5s'",
            "SET LOCAL idle_in_transaction_session_timeout='10s'",
        ] {
            sqlx::query(statement).execute(&mut *tx).await?;
        }
        Ok(tx)
    }
    /// Stable ID + full request fingerprint + immutable price + worker token.
    /// The worker token must be 32 random bytes created by the Rust gateway.
    /// Candidate funding is completely authenticated BEFORE attempting a hold.
    pub async fn reserve(
        &self,
        secret: &str,
        request: &Request,
        worker: &[u8; 32],
    ) -> Result<Charge> {
        request.validate()?;
        let worker_digest = authority::worker_digest(worker)?;
        let spec = hash(request)?;
        let mut tx = self.begin().await?;
        lock(&mut tx, &request.id).await?;
        let actor = authority::authenticate(&mut tx, secret).await?;
        if actor.kind != "api_key" {
            return Err(Error::Forbidden);
        }
        if let Some(row) =
            sqlx::query("SELECT * FROM core_charging.requests WHERE id=$1 FOR UPDATE")
                .bind(&request.id)
                .fetch_optional(&mut *tx)
                .await?
        {
            let stored = decode(row)?;
            worker_matches(&stored, worker)?;
            if stored.charge.actor_user_id != actor.user
                || stored.charge.key_id != actor.key
                || stored.charge.owner_account_id != actor.owner
                || stored.spec != spec
            {
                return Err(Error::Conflict);
            }
            commit(tx).await?;
            return Ok(stored.charge);
        }
        let payers = authority::funding(&mut tx, &actor).await?;
        let at = now(&mut tx).await?;
        let mut last_error = Error::NoFunds;
        for payer in payers {
            for source in limits::sources(&mut tx, &payer, &request.price, at).await? {
                sqlx::query("SAVEPOINT funding_candidate")
                    .execute(&mut *tx)
                    .await?;
                let (sub, start, end) = match source {
                    Source::Wallet => (None, None, None),
                    Source::Subscription { id, start, end } => (Some(id), Some(start), Some(end)),
                };
                sqlx::query(
                    "INSERT INTO core_charging.requests(id,actor_user_id,key_id,owner_account_id,payer_account_id, \
                     fingerprint,worker_digest,spec_digest,price,source_subscription_id,source_start,source_end, \
                     authorized_at,lease_until,hard_deadline,state,reserved) \
                     VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'reserved',$16)",
                )
                .bind(&request.id).bind(actor.user).bind(actor.key).bind(actor.owner).bind(payer.id)
                .bind(request.fingerprint.to_vec()).bind(&worker_digest).bind(&spec).bind(json(&request.price)?)
                .bind(sub).bind(start).bind(end).bind(at).bind(at + request.lease_seconds)
                .bind(at + request.maximum_seconds).bind(request.reserve).execute(&mut *tx).await?;
                let charge = load(&mut tx, &request.id).await?.charge;
                let attempt = self.hold(&mut tx, &charge, at).await;
                match attempt {
                    Ok(()) => {
                        sqlx::query("RELEASE SAVEPOINT funding_candidate")
                            .execute(&mut *tx)
                            .await?;
                        commit(tx).await?;
                        return Ok(charge);
                    }
                    Err(error @ (Error::NoFunds | Error::BudgetExceeded)) => {
                        last_error = error;
                        sqlx::query("ROLLBACK TO SAVEPOINT funding_candidate")
                            .execute(&mut *tx)
                            .await?;
                        sqlx::query("RELEASE SAVEPOINT funding_candidate")
                            .execute(&mut *tx)
                            .await?;
                    }
                    Err(error) => return Err(error),
                }
            }
        }
        Err(last_error)
    }
    async fn hold(&self, c: &mut PgConnection, charge: &Charge, at: i64) -> Result<()> {
        limits::budgets(c, charge, charge.reserved).await?;
        limits::entitlement(c, charge, charge.reserved, at).await?;
        self.ledger
            .apply(
                c,
                command(
                    charge,
                    &format!("reserve:{}", charge.reserved),
                    Change::Reserve {
                        total: charge.reserved,
                    },
                )?,
            )
            .await?;
        Ok(())
    }
    /// Returns true ONLY for the first durable reserved -> streaming transition.
    /// On false or CommitUnknown, do not dispatch upstream again. A provider
    /// request ID / status check is required to resolve an ambiguous dispatch.
    pub async fn start(&self, id: &str, worker: &[u8; 32]) -> Result<bool> {
        let mut tx = self.begin().await?;
        lock(&mut tx, id).await?;
        let stored = load(&mut tx, id).await?;
        worker_matches(&stored, worker)?;
        live(&stored.charge, now(&mut tx).await?)?;
        if stored.charge.state == "streaming" {
            commit(tx).await?;
            return Ok(false);
        }
        if stored.charge.state != "reserved" {
            return Err(Error::WrongState);
        }
        sqlx::query(
            "UPDATE core_charging.requests SET state='streaming',revision=revision+1 WHERE id=$1",
        )
        .bind(id)
        .execute(&mut *tx)
        .await?;
        event(
            &mut tx,
            id,
            "start",
            json!({"kind":"upstream_dispatch_claim"}),
        )
        .await?;
        commit(tx).await?;
        Ok(true)
    }
    /// Extension keeps the original payer, price, subscription cycle and budget
    /// attribution time. It rechecks current key/team authority. No source switch.
    pub async fn increase(
        &self,
        secret: &str,
        id: &str,
        worker: &[u8; 32],
        total: i64,
    ) -> Result<Charge> {
        let mut tx = self.begin().await?;
        lock(&mut tx, id).await?;
        let stored = load(&mut tx, id).await?;
        worker_matches(&stored, worker)?;
        let at = now(&mut tx).await?;
        live(&stored.charge, at)?;
        let old = stored.charge;
        if !matches!(old.state.as_str(), "reserved" | "streaming") {
            return Err(Error::WrongState);
        }
        let actor = authority::authenticate(&mut tx, secret).await?;
        if actor.key != old.key_id
            || actor.user != old.actor_user_id
            || actor.owner != old.owner_account_id
        {
            return Err(Error::Forbidden);
        }
        if !authority::funding(&mut tx, &actor)
            .await?
            .iter()
            .any(|p| p.id == old.payer_account_id)
        {
            return Err(Error::Forbidden);
        }
        if total < old.reserved {
            return Err(Error::Invalid);
        }
        if total == old.reserved {
            commit(tx).await?;
            return Ok(old);
        }
        sqlx::query(
            "UPDATE core_charging.requests SET reserved=$2,revision=revision+1 WHERE id=$1",
        )
        .bind(id)
        .bind(total)
        .execute(&mut *tx)
        .await?;
        let charge = load(&mut tx, id).await?.charge;
        self.hold(&mut tx, &charge, at).await?;
        commit(tx).await?;
        Ok(charge)
    }
    /// Persist cumulative usage BEFORE exposing corresponding output. Never use
    /// per-chunk rounded charges. Duplicate event IDs must have identical usage.
    pub async fn checkpoint(
        &self,
        id: &str,
        worker: &[u8; 32],
        event_id: &str,
        usage: Usage,
    ) -> Result<Charge> {
        if !valid_id(event_id) || event_id.len() > 120 {
            return Err(Error::Invalid);
        }
        let event_id = format!("usage:{event_id}");
        let detail = json!({"kind":"usage","usage":usage});
        let mut tx = self.begin().await?;
        lock(&mut tx, id).await?;
        let stored = load(&mut tx, id).await?;
        worker_matches(&stored, worker)?;
        let previous: Option<Value> = sqlx::query_scalar(
            "SELECT detail FROM core_charging.usage_events WHERE request_id=$1 AND event_id=$2",
        )
        .bind(id)
        .bind(&event_id)
        .fetch_optional(&mut *tx)
        .await?;
        if let Some(previous) = previous {
            if previous != detail {
                return Err(Error::Conflict);
            }
            commit(tx).await?;
            return Ok(stored.charge);
        }
        live(&stored.charge, now(&mut tx).await?)?;
        if stored.charge.state != "streaming" {
            return Err(Error::WrongState);
        }
        Self::check_usage(&stored.charge, usage)?;
        sqlx::query("UPDATE core_charging.requests SET input_tokens=$2,cached_tokens=$3,output_tokens=$4,revision=revision+1 WHERE id=$1")
            .bind(id).bind(usage.input).bind(usage.cached).bind(usage.output).execute(&mut *tx).await?;
        event(&mut tx, id, &event_id, detail).await?;
        let charge = load(&mut tx, id).await?.charge;
        commit(tx).await?;
        Ok(charge)
    }
    fn check_usage(charge: &Charge, usage: Usage) -> Result<i64> {
        if !usage.follows(charge.usage) {
            return Err(Error::Invalid);
        }
        let amount = charge.price.cost(usage)?;
        if amount > charge.reserved {
            return Err(Error::NeedReservation);
        }
        Ok(amount)
    }
    pub async fn heartbeat(&self, id: &str, worker: &[u8; 32], seconds: i64) -> Result<Charge> {
        if !(1..=300).contains(&seconds) {
            return Err(Error::Invalid);
        }
        let mut tx = self.begin().await?;
        lock(&mut tx, id).await?;
        let stored = load(&mut tx, id).await?;
        worker_matches(&stored, worker)?;
        let at = now(&mut tx).await?;
        live(&stored.charge, at)?;
        if !matches!(stored.charge.state.as_str(), "reserved" | "streaming") {
            return Err(Error::WrongState);
        }
        let lease = stored
            .charge
            .lease_until
            .max(at + seconds)
            .min(stored.charge.hard_deadline);
        sqlx::query(
            "UPDATE core_charging.requests SET lease_until=$2,revision=revision+1 WHERE id=$1",
        )
        .bind(id)
        .bind(lease)
        .execute(&mut *tx)
        .await?;
        let charge = load(&mut tx, id).await?.charge;
        commit(tx).await?;
        Ok(charge)
    }
    /// Cancellation or failure AFTER dispatch must settle authoritative partial
    /// usage; it is not a zero-cost release. Old membership need not remain active.
    pub async fn settle(
        &self,
        id: &str,
        worker: &[u8; 32],
        usage: Usage,
        outcome: Outcome,
    ) -> Result<Charge> {
        let mut tx = self.begin().await?;
        lock(&mut tx, id).await?;
        let stored = load(&mut tx, id).await?;
        worker_matches(&stored, worker)?;
        if stored.charge.state == "settled" {
            if stored.charge.usage != usage
                || stored.charge.outcome.as_deref() != Some(outcome.text())
            {
                return Err(Error::Conflict);
            }
            commit(tx).await?;
            return Ok(stored.charge);
        }
        live(&stored.charge, now(&mut tx).await?)?;
        if stored.charge.state != "streaming" {
            return Err(Error::WrongState);
        }
        let charge = self
            .finish(&mut tx, stored.charge, usage, outcome, None)
            .await?;
        commit(tx).await?;
        Ok(charge)
    }
    async fn finish(
        &self,
        c: &mut PgConnection,
        charge: Charge,
        usage: Usage,
        outcome: Outcome,
        evidence: Option<&str>,
    ) -> Result<Charge> {
        let amount = Self::check_usage(&charge, usage)?;
        self.ledger
            .apply(c, command(&charge, "settle", Change::Settle { amount })?)
            .await?;
        sqlx::query("UPDATE core_charging.requests SET state='settled',input_tokens=$2,cached_tokens=$3,output_tokens=$4,settled=$5,outcome=$6,revision=revision+1 WHERE id=$1")
            .bind(&charge.id).bind(usage.input).bind(usage.cached).bind(usage.output).bind(amount)
            .bind(outcome.text()).execute(&mut *c).await?;
        event(
            c,
            &charge.id,
            "final",
            json!({"kind":"final","usage":usage,"outcome":outcome,"evidence":evidence}),
        )
        .await?;
        Ok(load(c, &charge.id).await?.charge)
    }
    /// Safe only before start(). After upstream dispatch, use settle/reconcile.
    pub async fn release(&self, id: &str, worker: &[u8; 32]) -> Result<Charge> {
        let mut tx = self.begin().await?;
        lock(&mut tx, id).await?;
        let stored = load(&mut tx, id).await?;
        worker_matches(&stored, worker)?;
        if stored.charge.state == "released" {
            commit(tx).await?;
            return Ok(stored.charge);
        }
        if stored.charge.state != "reserved" {
            return Err(Error::WrongState);
        }
        self.release_unstarted(&mut tx, &stored.charge).await?;
        let charge = load(&mut tx, id).await?.charge;
        commit(tx).await?;
        Ok(charge)
    }
    async fn release_unstarted(&self, c: &mut PgConnection, charge: &Charge) -> Result<()> {
        self.ledger
            .apply(c, command(charge, "release", Change::Release)?)
            .await?;
        sqlx::query(
            "UPDATE core_charging.requests SET state='released',revision=revision+1 WHERE id=$1",
        )
        .bind(&charge.id)
        .execute(&mut *c)
        .await?;
        event(
            c,
            &charge.id,
            "release",
            json!({"kind":"release_before_dispatch"}),
        )
        .await
    }
    /// Trusted Rust recovery service only; do not expose as a Go/client action.
    /// Evidence must identify an authoritative provider result, not an estimate.
    pub async fn reconcile(
        &self,
        id: &str,
        expected_revision: i64,
        usage: Usage,
        outcome: Outcome,
        evidence: &str,
    ) -> Result<Charge> {
        Self::evidence(evidence)?;
        let mut tx = self.begin().await?;
        lock(&mut tx, id).await?;
        let stored = load(&mut tx, id).await?;
        if stored.charge.state == "settled" {
            let previous: Value = sqlx::query_scalar("SELECT detail FROM core_charging.usage_events WHERE request_id=$1 AND event_id='final'")
                .bind(id).fetch_one(&mut *tx).await?;
            if stored.charge.usage != usage
                || stored.charge.outcome.as_deref() != Some(outcome.text())
                || previous.get("evidence").and_then(Value::as_str) != Some(evidence)
            {
                return Err(Error::Conflict);
            }
            commit(tx).await?;
            return Ok(stored.charge);
        }
        if stored.charge.state != "reconcile" || stored.charge.revision != expected_revision {
            return Err(Error::WrongState);
        }
        let charge = self
            .finish(&mut tx, stored.charge, usage, outcome, Some(evidence))
            .await?;
        commit(tx).await?;
        Ok(charge)
    }
    /// Bounded recovery. Expired streams retain the entire hold, even with zero
    /// locally observed output. A process crash is not proof of no provider use.
    pub async fn recover_expired(&self, limit: i64) -> Result<Vec<Charge>> {
        if !(1..=1000).contains(&limit) {
            return Err(Error::Invalid);
        }
        let ids: Vec<String> = sqlx::query_scalar(
            "SELECT id FROM core_charging.requests WHERE state IN ('reserved','streaming') \
             AND lease_until<=floor(extract(epoch FROM clock_timestamp()))::bigint ORDER BY lease_until,id LIMIT $1",
        ).bind(limit).fetch_all(&self.pool).await?;
        let mut recovered = Vec::new();
        for id in ids {
            let mut tx = self.begin().await?;
            lock(&mut tx, &id).await?;
            let stored = load(&mut tx, &id).await?;
            if stored.charge.lease_until > now(&mut tx).await? {
                continue;
            }
            match stored.charge.state.as_str() {
                "reserved" => self.release_unstarted(&mut tx, &stored.charge).await?,
                "streaming" => {
                    sqlx::query("UPDATE core_charging.requests SET state='reconcile',revision=revision+1 WHERE id=$1")
                        .bind(&id).execute(&mut *tx).await?;
                    event(
                        &mut tx,
                        &id,
                        "recovery",
                        json!({"kind":"unknown_provider_usage","hold_retained":true}),
                    )
                    .await?;
                }
                _ => continue,
            }
            let charge = load(&mut tx, &id).await?.charge;
            commit(tx).await?;
            recovered.push(charge);
        }
        Ok(recovered)
    }
    /// Internal recovery read: READ COMMITTED intentionally takes a fresh
    /// snapshot AFTER the advisory lock. It waits for any unknown commit to
    /// resolve before reporting absence. Not an unauthenticated status endpoint.
    pub async fn resolve(&self, id: &str) -> Result<Option<Charge>> {
        let mut tx = self.pool.begin().await?;
        for statement in [
            "SET TRANSACTION ISOLATION LEVEL READ COMMITTED",
            "SET LOCAL lock_timeout='2s'",
            "SET LOCAL statement_timeout='5s'",
        ] {
            sqlx::query(statement).execute(&mut *tx).await?;
        }
        lock(&mut tx, id).await?;
        let result = sqlx::query("SELECT * FROM core_charging.requests WHERE id=$1")
            .bind(id)
            .fetch_optional(&mut *tx)
            .await?
            .map(decode)
            .transpose()?
            .map(|s| s.charge);
        commit(tx).await?;
        Ok(result)
    }
    /// Trusted refund service only. The original payer, subscription window and
    /// budget attribution stay fixed, even after key revocation/team removal.
    pub async fn refund(
        &self,
        id: &str,
        refund_id: &str,
        amount: i64,
        evidence: &str,
    ) -> Result<Charge> {
        Self::evidence(evidence)?;
        if !valid_id(refund_id) || amount <= 0 {
            return Err(Error::Invalid);
        }
        let mut tx = self.begin().await?;
        lock(&mut tx, id).await?;
        let stored = load(&mut tx, id).await?;
        if let Some(row) = sqlx::query("SELECT amount,evidence FROM core_charging.refunds WHERE request_id=$1 AND refund_id=$2")
            .bind(id).bind(refund_id).fetch_optional(&mut *tx).await?
        {
            if row.try_get::<i64, _>("amount")? != amount || row.try_get::<String, _>("evidence")? != evidence {
                return Err(Error::Conflict);
            }
            commit(tx).await?;
            return Ok(stored.charge);
        }
        let charge = stored.charge;
        if charge.state != "settled" || amount > charge.settled - charge.refunded {
            return Err(Error::Invalid);
        }
        self.ledger
            .apply(
                &mut tx,
                command(
                    &charge,
                    &format!("refund:{refund_id}"),
                    Change::Refund { amount },
                )?,
            )
            .await?;
        sqlx::query("INSERT INTO core_charging.refunds(request_id,refund_id,amount,evidence) VALUES ($1,$2,$3,$4)")
            .bind(id).bind(refund_id).bind(amount).bind(evidence).execute(&mut *tx).await?;
        sqlx::query("UPDATE core_charging.requests SET refunded=refunded+$2,revision=revision+1 WHERE id=$1")
            .bind(id).bind(amount).execute(&mut *tx).await?;
        let charge = load(&mut tx, id).await?.charge;
        commit(tx).await?;
        Ok(charge)
    }
    fn evidence(evidence: &str) -> Result<()> {
        if evidence.trim().is_empty() || evidence.len() > 500 {
            Err(Error::Invalid)
        } else {
            Ok(())
        }
    }
    /// Budget changes keep stable scope/issuer/period identity. Changing a cap
    /// never clears history. A second issuer adds a constraint, not an override.
    pub async fn set_budget(&self, session: &str, budget: &Budget) -> Result<i64> {
        budget.validate()?;
        let mut tx = self.begin().await?;
        let actor = authority::authenticate(&mut tx, session).await?;
        authority::manage_budget(&mut tx, &actor, budget).await?;
        let id: i64 = sqlx::query_scalar(
            "INSERT INTO core_charging.budgets(scope,account_id,user_id,key_id,issuer_user_id,period,anchor,seconds,limit_credits) \
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) \
             ON CONFLICT (scope,account_id,user_id,key_id,issuer_user_id,period,anchor,seconds) \
             DO UPDATE SET limit_credits=EXCLUDED.limit_credits,revision=core_charging.budgets.revision+1 RETURNING id",
        )
        .bind(&budget.scope).bind(budget.account_id).bind(budget.user_id).bind(budget.key_id)
        .bind(actor.user).bind(&budget.period).bind(budget.anchor).bind(budget.seconds).bind(budget.limit)
        .fetch_one(&mut *tx).await?;
        sqlx::query(
            "INSERT INTO core_charging.policy_events(actor_user_id,budget_id,detail) VALUES ($1,$2,$3)",
        )
        .bind(actor.user)
        .bind(id)
        .bind(json(budget)?)
        .execute(&mut *tx)
        .await?;
        commit(tx).await?;
        Ok(id)
    }
}
