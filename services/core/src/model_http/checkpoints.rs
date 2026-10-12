//! Append-only evidence through an EXECUTE-only PostgreSQL function. A retry
//! keeps every parameter identical, including sequence, after an unknown ACK.
use super::Bridge;
use crate::{
    billing::charging::{self, Outcome},
    relay::{OutputCheckpoint, RelayError, Reservation},
};

/// A discovery row carries evidence, not permission to change the payer or price.
type CompletedStream = (String, i64, serde_json::Value, i64);

impl super::ModelGateway {
    /// Scan one bounded recovery page. Only the trusted Rust host may call this.
    /// The returned pair is (confirmed settlements, next request ID). Continue
    /// with the returned ID even when the count is zero. None ends this sweep;
    /// start the next sweep with None so earlier/newly eligible IDs are revisited.
    ///
    /// Requires S11's EXECUTE-only completed_streams_after(integer,text) contract.
    /// An older database returns Unsupported; never silently fall back to a
    /// first-page query that can starve later requests. No upstream is contacted.
    pub async fn reconcile_completed_streams_page(
        &self,
        limit: i32,
        after_request_id: Option<&str>,
    ) -> Result<(usize, Option<String>), RelayError> {
        validate_recovery_page(limit, after_request_id)?;
        let rows: Vec<CompletedStream> = sqlx::query_as(
            "SELECT request_id,revision,provider_usage,sequence \
             FROM core_model.completed_streams_after($1,$2)",
        )
        .bind(limit)
        .bind(after_request_id)
        .fetch_all(&self.bridge.pool)
        .await
        .map_err(|error| {
            if error.as_database_error().and_then(|e| e.code()).as_deref() == Some("42883") {
                RelayError::Unsupported("paged model recovery requires the database contract")
            } else {
                RelayError::BillingUnavailable
            }
        })?;
        // Advance by scanned rows, not successful settlements. A full page of
        // malformed or concurrently resolved receipts must still yield a cursor.
        let next = next_recovery_page(&rows, limit as usize);
        let recovered = self.bridge.reconcile_stream_rows(rows).await?;
        Ok((recovered, next))
    }
}

fn validate_recovery_page(limit: i32, after: Option<&str>) -> Result<(), RelayError> {
    if !(1..=1000).contains(&limit)
        || after.is_some_and(|id| {
            id.is_empty()
                || id.len() > 128
                || !id
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b"-_.:/".contains(&b))
        })
    {
        return Err(RelayError::Invalid("model recovery page"));
    }
    Ok(())
}

fn next_recovery_page(rows: &[CompletedStream], limit: usize) -> Option<String> {
    rows.last()
        .filter(|_| rows.len() == limit)
        .map(|row| row.0.clone())
}

impl Bridge {
    pub(super) async fn reconcile_completed_streams(
        &self,
        limit: i32,
    ) -> Result<usize, RelayError> {
        validate_recovery_page(limit, None)?;
        let rows: Vec<CompletedStream> = sqlx::query_as(
            "SELECT request_id,revision,provider_usage,sequence FROM core_model.completed_streams($1)"
        ).bind(limit).fetch_all(&self.pool).await.map_err(|_| RelayError::BillingUnavailable)?;
        self.reconcile_stream_rows(rows).await
    }

    async fn reconcile_stream_rows(&self, rows: Vec<CompletedStream>) -> Result<usize, RelayError> {
        let mut recovered = 0;
        for (id, revision, value, sequence) in rows {
            // A malformed historical receipt must not block the other rows in
            // this bounded batch. Never turn failed decoding into zero usage.
            let Some(usage) = recovery_usage(value, sequence) else {
                continue;
            };
            let Some(original) = self
                .charging
                .resolve(&id)
                .await
                .map_err(super::billing::error)?
            else {
                continue;
            };
            if original.state != "reconcile" || original.revision != revision {
                continue;
            }
            let evidence = format!("model-stream:{id}:{sequence}");
            let result = super::billing::retry(|| {
                self.charging.reconcile(
                    &id,
                    revision,
                    usage,
                    Outcome::Completed,
                    &evidence,
                )
            })
            .await;
            let confirmed = match result {
                Ok(charge) => Some(charge),
                Err(
                    charging::Error::CommitUnknown
                    | charging::Error::Conflict
                    | charging::Error::WrongState,
                ) => {
                    // A lost ACK or another recovery worker is resolved by a
                    // read of the SAME operation. No model call or new hold.
                    self.charging
                        .resolve(&id)
                        .await
                        .map_err(super::billing::error)?
                }
                Err(charging::Error::Invalid | charging::Error::NeedReservation) => continue,
                Err(error) => return Err(super::billing::error(error)),
            };
            if confirmed.as_ref().is_some_and(|charge| {
                super::billing::confirmed_settlement(
                    charge,
                    &id,
                    &original.price,
                    usage,
                    Outcome::Completed,
                )
            }) {
                recovered += 1;
            }
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
        checkpoint
            .usage
            .validate()
            .map_err(|_| RelayError::SettlementPending)?;
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

fn recovery_usage(value: serde_json::Value, sequence: i64) -> Option<charging::Usage> {
    if !(1..=65_536).contains(&sequence) {
        return None;
    }
    let observed = serde_json::from_value(value).ok()?;
    super::billing::complete_usage(observed).ok()
}

#[cfg(test)]
mod a07_recovery_tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn recovery_page_rejects_unbounded_or_invalid_cursors() {
        for limit in [i32::MIN, -1, 0, 1001, i32::MAX] {
            assert!(validate_recovery_page(limit, None).is_err());
        }
        for cursor in ["", "has space", "line\nbreak", "查询", "quote'", "\0"] {
            assert!(validate_recovery_page(1, Some(cursor)).is_err());
        }
        assert!(validate_recovery_page(1, Some(&"a".repeat(129))).is_err());
        for limit in [1, 1000] {
            for cursor in [None, Some("a-_.:/Z09")] {
                assert!(validate_recovery_page(limit, cursor).is_ok());
            }
        }
        assert!(validate_recovery_page(1, Some(&"a".repeat(128))).is_ok());
    }

    #[test]
    fn full_invalid_page_still_advances_by_the_last_scanned_id() {
        let rows = vec![
            ("a".into(), 1, json!({"input_tokens":-1,"output_tokens":2}), 1),
            ("b".into(), 1, json!({"input_tokens":1,"output_tokens":2,"cached_input_tokens":2}), 1),
        ];
        assert!(rows.iter().all(|row| recovery_usage(row.2.clone(), row.3).is_none()));
        assert_eq!(next_recovery_page(&rows, 2), Some("b".into()));
        assert_eq!(next_recovery_page(&rows[..1], 1), Some("a".into()));
    }

    #[test]
    fn short_or_empty_page_ends_the_sweep_not_the_recovery_service() {
        let rows = vec![("last".into(), 1, json!({"input_tokens":0,"output_tokens":0}), 1)];
        assert_eq!(next_recovery_page(&rows, 2), None);
        assert_eq!(next_recovery_page(&[], 1), None);
        // A page exactly at the limit needs a further (possibly empty) query.
        assert_eq!(next_recovery_page(&rows, 1), Some("last".into()));
    }

    #[test]
    fn malformed_receipt_does_not_hide_the_next_valid_receipt() {
        let receipts = [
            json!({"input_tokens":-1,"output_tokens":2}),
            json!({"input_tokens":4,"output_tokens":2,"cached_input_tokens":"invalid"}),
            json!({"input_tokens":4,"output_tokens":2,"cached_input_tokens":1}),
        ];
        let valid: Vec<_> = receipts
            .into_iter()
            .filter_map(|value| recovery_usage(value, 1))
            .collect();
        assert_eq!(valid, vec![charging::Usage { input: 4, cached: 1, output: 2 }]);
    }

    #[test]
    fn persisted_subsets_are_validated_again_before_reconciliation() {
        for value in [
            json!({"input_tokens":4,"output_tokens":2,"cached_input_tokens":5}),
            json!({"input_tokens":4,"output_tokens":2,"reasoning_output_tokens":3}),
            json!({"input_tokens":4,"output_tokens":2,"cache_creation_input_tokens":1}),
            json!({"input_tokens":4,"output_tokens":2,"future_counter":1}),
            json!({"input_tokens":u64::MAX,"output_tokens":2}),
        ] {
            assert!(recovery_usage(value, 1).is_none());
        }
    }

    #[test]
    fn unknown_totals_stay_held_but_explicit_zero_is_recoverable() {
        for value in [json!({}), json!({"input_tokens":0}), json!({"output_tokens":0})] {
            assert!(recovery_usage(value, 1).is_none());
        }
        assert_eq!(
            recovery_usage(json!({"input_tokens":0,"output_tokens":0}), 1),
            Some(charging::Usage::default())
        );
    }

    #[test]
    fn checkpoint_sequence_must_fit_the_persistent_contract() {
        let value = json!({"input_tokens":4,"output_tokens":2});
        for sequence in [-1, 0, 65_537, i64::MAX] {
            assert!(recovery_usage(value.clone(), sequence).is_none());
        }
        for sequence in [1, 65_536] {
            assert!(recovery_usage(value.clone(), sequence).is_some());
        }
    }
}
