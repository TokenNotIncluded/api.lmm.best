//! Append-only evidence through an EXECUTE-only PostgreSQL function. A retry
//! keeps every parameter identical, including sequence, after an unknown ACK.
use super::Bridge;
use crate::relay::{OutputCheckpoint, RelayError, Reservation};

impl Bridge {
    pub(super) async fn reconcile_completed_streams(
        &self,
        limit: i32,
    ) -> Result<usize, RelayError> {
        if !(1..=1000).contains(&limit) {
            return Err(RelayError::Invalid("model recovery limit"));
        }
        let rows: Vec<(String,i64,serde_json::Value,i64)> = sqlx::query_as(
            "SELECT request_id,revision,provider_usage,sequence FROM core_model.completed_streams($1)"
        ).bind(limit).fetch_all(&self.pool).await.map_err(|_| RelayError::BillingUnavailable)?;
        let mut recovered = 0;
        for (id, revision, value, sequence) in rows {
            let observed: crate::relay::Usage =
                serde_json::from_value(value).map_err(|_| RelayError::SettlementPending)?;
            if observed.cache_creation_input_tokens.is_some_and(|v| v != 0) {
                continue;
            }
            let (Some(input), Some(output)) = (
                observed.input_tokens.and_then(|v| i64::try_from(v).ok()),
                observed.output_tokens.and_then(|v| i64::try_from(v).ok()),
            ) else {
                continue;
            };
            let Some(cached) = i64::try_from(observed.cached_input_tokens.unwrap_or(0)).ok() else {
                continue;
            };
            let usage = crate::billing::charging::Usage {
                input,
                cached,
                output,
            };
            let evidence = format!("model-stream:{id}:{sequence}");
            super::billing::retry(|| {
                self.charging.reconcile(
                    &id,
                    revision,
                    usage,
                    crate::billing::charging::Outcome::Completed,
                    &evidence,
                )
            })
            .await
            .map_err(super::billing::error)?;
            recovered += 1;
        }
        Ok(recovered)
    }

    pub(super) async fn persist_output(
        &self,
        reservation: &Reservation,
        checkpoint: &OutputCheckpoint,
    ) -> Result<(), RelayError> {
        let (worker, output_ceiling) = {
            let active = self
                .active
                .lock()
                .map_err(|_| RelayError::BillingUnavailable)?;
            let operation = active.get(&reservation.id).ok_or(RelayError::Forbidden)?;
            (operation.worker, operation.output_token_ceiling)
        };
        if checkpoint
            .usage
            .input_tokens
            .is_some_and(|v| v > self.policy.input_token_ceiling as u64)
            || checkpoint
                .usage
                .output_tokens
                .is_some_and(|v| v > u64::from(output_ceiling))
            || checkpoint
                .usage
                .cache_creation_input_tokens
                .is_some_and(|v| v != 0)
            || matches!((checkpoint.usage.cached_input_tokens,checkpoint.usage.input_tokens),(Some(cached),Some(input)) if cached>input)
            || matches!((checkpoint.usage.reasoning_output_tokens,checkpoint.usage.output_tokens),(Some(reasoning),Some(output)) if reasoning>output)
        {
            return Err(RelayError::SettlementPending);
        }
        let usage =
            serde_json::to_value(checkpoint.usage).map_err(|_| RelayError::BillingUnavailable)?;
        let sequence =
            i64::try_from(checkpoint.sequence).map_err(|_| RelayError::SettlementPending)?;
        let bytes =
            i64::try_from(checkpoint.packet_bytes).map_err(|_| RelayError::SettlementPending)?;
        for attempt in 0..3 {
            let mut tx = self
                .pool
                .begin()
                .await
                .map_err(|_| RelayError::BillingUnavailable)?;
            sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
                .execute(&mut *tx)
                .await
                .map_err(|_| RelayError::BillingUnavailable)?;
            sqlx::query("SET LOCAL synchronous_commit=on")
                .execute(&mut *tx)
                .await
                .map_err(|_| RelayError::BillingUnavailable)?;
            let result = sqlx::query("SELECT core_model.checkpoint_output($1,$2,$3,$4,$5,$6,$7)")
                .bind(&reservation.id)
                .bind(worker.as_slice())
                .bind(sequence)
                .bind(checkpoint.packet_sha256.as_slice())
                .bind(bytes)
                .bind(&usage)
                .bind(checkpoint.terminal_observed)
                .execute(&mut *tx)
                .await;
            match result {
                Ok(_) => {
                    // If COMMIT succeeded but its reply was lost, the next
                    // exact function call confirms the already committed row.
                    if tx.commit().await.is_ok() {
                        return Ok(());
                    }
                }
                Err(error) => {
                    let retryable = matches!(
                        error.as_database_error().and_then(|e| e.code()).as_deref(),
                        Some("40001" | "40P01")
                    );
                    let _ = tx.rollback().await;
                    if !retryable {
                        return Err(RelayError::SettlementPending);
                    }
                }
            }
            if attempt < 2 {
                tokio::time::sleep(std::time::Duration::from_millis(10 * (attempt + 1))).await;
            }
        }
        Err(RelayError::SettlementPending)
    }
}
