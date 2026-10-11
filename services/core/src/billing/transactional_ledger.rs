//! The real ledger adapter for charging. Every query uses the caller's transaction.
//! No pool, COMMIT, remote call, direct balance update, or independent money store.
use super::{
    Credits,
    charging::{
        Change, Ledger as ChargingLedger, LedgerCommand, LedgerError, LedgerFuture, Source,
    },
    ledger::{Action, Outcome, Rejection, Request, Wallet},
};
use serde_json::Value;
use sqlx::{PgConnection, Row, types::Json};

#[derive(Default)]
pub struct TransactionalLedger;

#[derive(sqlx::FromRow)]
struct Hold {
    position: i32,
    reservation_journal_id: i64,
    reserved: i64,
    capture_journal_id: Option<i64>,
    captured: i64,
    refunded: i64,
    closed: bool,
}

fn database(error: sqlx::Error) -> LedgerError {
    match error.as_database_error().and_then(|e| e.code()).as_deref() {
        Some("40001" | "40P01" | "55P03") => LedgerError::Retry,
        Some("23505") => LedgerError::Conflict,
        _ => LedgerError::Unavailable,
    }
}

fn credits(amount: i64) -> Result<Credits, LedgerError> {
    Credits::try_from(amount).map_err(|_| LedgerError::Conflict)
}

async fn effect(
    c: &mut PgConnection,
    command: &LedgerCommand,
    suffix: &str,
    action: Action,
) -> Result<i64, LedgerError> {
    let request = Request {
        scope: "core-charging".into(),
        operation_key: format!("{}:{suffix}", command.operation_id),
        actor_user_id: Some(command.actor_user_id),
        reason: format!("Model request {}", command.request_id),
        action,
    };
    request.validate().map_err(|_| LedgerError::Conflict)?;
    let Json(result): Json<Outcome> = sqlx::query_scalar("SELECT core_billing.post_ledger($1)")
        .bind(Json(request))
        .fetch_one(c)
        .await
        .map_err(database)?;
    match result {
        Outcome::Posted { journal_id, .. } => Ok(journal_id),
        Outcome::Rejected {
            code: Rejection::InsufficientFunds,
        } => Err(LedgerError::Insufficient),
        Outcome::Rejected { .. } => Err(LedgerError::Conflict),
    }
}

// Also bind the adapter to the already-locked charging record. A programming
// error must not turn a command for one request into a debit of another payer.
async fn check_binding(c: &mut PgConnection, command: &LedgerCommand) -> Result<(), LedgerError> {
    if command.operation_id.len() != 64
        || !command.operation_id.bytes().all(|b| b.is_ascii_hexdigit())
    {
        return Err(LedgerError::Conflict);
    }
    let row = sqlx::query("SELECT actor_user_id,key_id,owner_account_id,payer_account_id,price,source_subscription_id,source_start,source_end FROM core_charging.requests WHERE id=$1 FOR UPDATE")
        .bind(&command.request_id).fetch_optional(c).await.map_err(database)?
        .ok_or(LedgerError::Conflict)?;
    let price: Value = row.try_get("price").map_err(database)?;
    let source = match row
        .try_get::<Option<i64>, _>("source_subscription_id")
        .map_err(database)?
    {
        Some(id) => Source::Subscription {
            id,
            start: row.try_get("source_start").map_err(database)?,
            end: row.try_get("source_end").map_err(database)?,
        },
        None => Source::Wallet,
    };
    if row.try_get::<i64, _>("actor_user_id").map_err(database)? != command.actor_user_id
        || row.try_get::<i64, _>("key_id").map_err(database)? != command.key_id
        || row
            .try_get::<i64, _>("owner_account_id")
            .map_err(database)?
            != command.owner_account_id
        || row
            .try_get::<i64, _>("payer_account_id")
            .map_err(database)?
            != command.payer_account_id
        || price.get("version").and_then(Value::as_str) != Some(&command.price_version)
        || source != command.source
    {
        return Err(LedgerError::Conflict);
    }
    Ok(())
}

impl ChargingLedger for TransactionalLedger {
    fn apply<'a>(&'a self, c: &'a mut PgConnection, command: LedgerCommand) -> LedgerFuture<'a> {
        Box::pin(async move {
            check_binding(c, &command).await?;
            let detail = serde_json::to_value(&command).map_err(|_| LedgerError::Conflict)?;
            let saved: Option<Value> = sqlx::query_scalar(
                "SELECT command FROM core_charging.ledger_commands WHERE operation_id=$1",
            )
            .bind(&command.operation_id)
            .fetch_optional(&mut *c)
            .await
            .map_err(database)?;
            if let Some(saved) = saved {
                return if saved == detail {
                    Ok(())
                } else {
                    Err(LedgerError::Conflict)
                };
            }
            if command.source == Source::Wallet {
                apply_wallet(c, &command).await?;
            }
            // The real charging engine holds/checks the original grant and
            // cycle in this same transaction. This durable consumption receipt
            // audits subscription changes without debiting wallet cash.
            sqlx::query("INSERT INTO core_charging.ledger_commands(operation_id,request_id,command) VALUES ($1,$2,$3)")
                .bind(&command.operation_id).bind(&command.request_id).bind(detail)
                .execute(c).await.map_err(database)?;
            Ok(())
        })
    }
}

async fn apply_wallet(c: &mut PgConnection, command: &LedgerCommand) -> Result<(), LedgerError> {
    let holds: Vec<Hold> = sqlx::query_as("SELECT position,reservation_journal_id,reserved,capture_journal_id,captured,refunded,closed FROM core_charging.ledger_holds WHERE request_id=$1 ORDER BY position FOR UPDATE")
        .bind(&command.request_id).fetch_all(&mut *c).await.map_err(database)?;
    match command.change {
        Change::Reserve { total } => {
            if total < 0 || holds.iter().any(|h| h.closed) {
                return Err(LedgerError::Conflict);
            }
            let old = holds
                .iter()
                .try_fold(0_i64, |n, h| n.checked_add(h.reserved))
                .ok_or(LedgerError::Conflict)?;
            let delta = total
                .checked_sub(old)
                .filter(|n| *n >= 0)
                .ok_or(LedgerError::Conflict)?;
            if delta != 0 {
                let _: Json<Wallet> =
                    sqlx::query_scalar("SELECT core_billing.open_ledger_wallet($1)")
                        .bind(command.payer_account_id)
                        .fetch_one(&mut *c)
                        .await
                        .map_err(database)?;
                let journal = effect(
                    c,
                    command,
                    "hold",
                    Action::Reserve {
                        account_id: command.payer_account_id,
                        amount_units: credits(delta)?,
                    },
                )
                .await?;
                let position = i32::try_from(holds.len()).map_err(|_| LedgerError::Conflict)?;
                sqlx::query("INSERT INTO core_charging.ledger_holds(request_id,position,reservation_journal_id,reserved) VALUES ($1,$2,$3,$4)")
                    .bind(&command.request_id).bind(position).bind(journal).bind(delta)
                    .execute(c).await.map_err(database)?;
            }
        }
        Change::Settle { amount } => {
            let total = holds
                .iter()
                .try_fold(0_i64, |n, h| n.checked_add(h.reserved))
                .ok_or(LedgerError::Conflict)?;
            if amount < 0 || amount > total || holds.iter().any(|h| h.closed) {
                return Err(LedgerError::Conflict);
            }
            let mut remaining = amount;
            for hold in holds {
                let captured = remaining.min(hold.reserved);
                let journal = effect(
                    c,
                    command,
                    &format!("capture:{}", hold.position),
                    Action::Capture {
                        reservation_journal_id: hold.reservation_journal_id,
                        amount_units: credits(captured)?,
                    },
                )
                .await?;
                sqlx::query("UPDATE core_charging.ledger_holds SET closed=TRUE,captured=$3,capture_journal_id=$4 WHERE request_id=$1 AND position=$2")
                    .bind(&command.request_id).bind(hold.position).bind(captured).bind(journal)
                    .execute(&mut *c).await.map_err(database)?;
                remaining -= captured;
            }
        }
        Change::Release => {
            if holds.iter().any(|h| h.closed) {
                return Err(LedgerError::Conflict);
            }
            for hold in holds {
                effect(
                    c,
                    command,
                    &format!("release:{}", hold.position),
                    Action::Release {
                        reservation_journal_id: hold.reservation_journal_id,
                    },
                )
                .await?;
                sqlx::query("UPDATE core_charging.ledger_holds SET closed=TRUE WHERE request_id=$1 AND position=$2")
                    .bind(&command.request_id).bind(hold.position).execute(&mut *c).await.map_err(database)?;
            }
        }
        Change::Refund { amount } => {
            let total = holds
                .iter()
                .try_fold(0_i64, |n, h| n.checked_add(h.captured - h.refunded))
                .ok_or(LedgerError::Conflict)?;
            if amount <= 0 || amount > total || holds.iter().any(|h| !h.closed) {
                return Err(LedgerError::Conflict);
            }
            let mut remaining = amount;
            for hold in holds {
                let refund = remaining.min(hold.captured - hold.refunded);
                if refund == 0 {
                    continue;
                }
                let journal = hold.capture_journal_id.ok_or(LedgerError::Conflict)?;
                effect(
                    c,
                    command,
                    &format!("refund:{}", hold.position),
                    Action::Refund {
                        journal_id: journal,
                        amount_units: credits(refund)?,
                    },
                )
                .await?;
                sqlx::query("UPDATE core_charging.ledger_holds SET refunded=refunded+$3 WHERE request_id=$1 AND position=$2")
                    .bind(&command.request_id).bind(hold.position).bind(refund)
                    .execute(&mut *c).await.map_err(database)?;
                remaining -= refund;
            }
        }
    }
    Ok(())
}
