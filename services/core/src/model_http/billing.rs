use super::Bridge;
use crate::{
    billing::charging::{self, Outcome, Usage},
    relay::{
        Billing, InputBlock, OutputCheckpoint, RelayError, RelayFuture, Report, Request,
        RequestContext, Reservation, Route,
    },
};
use std::{future::Future, time::Duration};

pub(super) fn error(error: charging::Error) -> RelayError {
    match error {
        charging::Error::Unauthorized => RelayError::Unauthorized,
        charging::Error::Forbidden => RelayError::Forbidden,
        charging::Error::NoFunds | charging::Error::BudgetExceeded => RelayError::NoFunds,
        charging::Error::Conflict | charging::Error::StaleWorker | charging::Error::WrongState => {
            RelayError::Conflict
        }
        _ => RelayError::BillingUnavailable,
    }
}

/// Retry the entire rolled-back operation with the same ID, payload and worker.
/// CommitUnknown is deliberately excluded; dispatch is never retried blindly.
pub(super) async fn retry<T, F, Fut>(mut operation: F) -> charging::Result<T>
where
    F: FnMut() -> Fut,
    Fut: Future<Output = charging::Result<T>>,
{
    for attempt in 0..3 {
        match operation().await {
            Err(charging::Error::Retry) if attempt < 2 => {
                tokio::time::sleep(Duration::from_millis(10 * (attempt + 1))).await
            }
            result => return result,
        }
    }
    unreachable!()
}

impl Billing for Bridge {
    fn reserve<'a>(
        &'a self,
        context: &'a RequestContext,
        route: &'a Route,
        request: &'a Request,
    ) -> RelayFuture<'a, Reservation> {
        Box::pin(async move {
            // The local synthetic provider contract budgets one token per UTF-8
            // byte plus bounded framing. This is deliberately not advertised as
            // an external provider tokenizer. Oversize prompts never reserve or
            // dispatch; supplier-specific versioned bounds remain required.
            if local_input_bound(request)? > self.policy.input_token_ceiling as u64 {
                return Err(RelayError::Limit("local model input budget"));
            }
            let (secret, worker, fingerprint, selected, peer) = {
                let active = self
                    .active
                    .lock()
                    .map_err(|_| RelayError::BillingUnavailable)?;
                let op = active
                    .get(&context.request_id)
                    .ok_or(RelayError::Forbidden)?;
                (
                    op.secret.clone(),
                    op.worker,
                    op.fingerprint,
                    op.selected.clone().ok_or(RelayError::RouteUnavailable)?,
                    op.peer,
                )
            };
            if selected.route_id != route.id
                || selected.attempt.quote().version() != route.price_version
                || !selected.attempt.is_available()
            {
                return Err(RelayError::RouteUnavailable);
            }
            let reserve = selected
                .quote
                .reservation_ceiling(
                    self.policy.input_token_ceiling as u64,
                    u64::from(request.max_output_tokens),
                )
                .map_err(|_| RelayError::BillingUnavailable)?;
            let reserve = i64::try_from(reserve.0).map_err(|_| RelayError::BillingUnavailable)?;
            let spec = charging::Request {
                id: context.request_id.clone(),
                fingerprint,
                price: selected.price,
                reserve,
                lease_seconds: self.policy.lease_seconds,
                maximum_seconds: self.policy.lease_seconds,
            };
            let charge = retry(|| {
                self.charging
                    .reserve_with_context(&secret, &spec, &worker, peer)
            })
            .await
            .map_err(error)?;
            if charge.actor_user_id != context.actor_user_id || charge.key_id != context.key_id {
                return Err(RelayError::Forbidden);
            }
            if !selected.attempt.is_available() {
                let _ = retry(|| self.charging.release(&spec.id, &worker)).await;
                return Err(RelayError::RouteUnavailable);
            }
            // A durable dispatch claim precedes any HTTP bytes. A false/unknown
            // result never dispatches; expiry recovery retains ambiguous holds.
            if !retry(|| self.charging.start(&spec.id, &worker))
                .await
                .map_err(error)?
            {
                return Err(RelayError::Conflict);
            }
            Ok(Reservation { id: spec.id })
        })
    }

    fn checkpoint<'a>(
        &'a self,
        reservation: &'a Reservation,
        checkpoint: &'a OutputCheckpoint,
    ) -> RelayFuture<'a, ()> {
        Box::pin(self.persist_output(reservation, checkpoint))
    }

    fn finalize<'a>(
        &'a self,
        reservation: &'a Reservation,
        report: &'a Report,
    ) -> RelayFuture<'a, ()> {
        Box::pin(async move {
            let (worker, output_ceiling, selected) = {
                let active = self
                    .active
                    .lock()
                    .map_err(|_| RelayError::BillingUnavailable)?;
                let op = active
                    .get(&reservation.id)
                    .ok_or(RelayError::BillingUnavailable)?;
                (
                    op.worker,
                    op.output_token_ceiling,
                    op.selected.clone().ok_or(RelayError::BillingUnavailable)?,
                )
            };
            if reservation.id != report.request_id
                || report.route_id.as_deref() != Some(selected.route_id.as_str())
                || report.price_version != Some(selected.quote.price().version())
            {
                return Err(RelayError::Conflict);
            }
            let (usage, outcome) = if !report.upstream_attempted {
                (Usage::default(), Outcome::Cancelled)
            } else {
                // Partial/local output is not an authoritative final provider
                // usage receipt. Never substitute missing usage with zero.
                if !report.provider_complete {
                    return Err(RelayError::SettlementPending);
                }
                let usage = complete_usage(report.usage)?;
                if usage.input > self.policy.input_token_ceiling
                    || usage.output > i64::from(output_ceiling)
                {
                    return Err(RelayError::SettlementPending);
                }
                // The legacy ledger must agree with the captured exact quote.
                // Never reinterpret a price or a missing receipt at settlement.
                let disjoint = crate::routing::Usage::from_totals(
                    usage.input as u64,
                    usage.output as u64,
                    usage.cached as u64,
                    0,
                )
                .map_err(|_| RelayError::SettlementPending)?;
                let quoted = selected
                    .quote
                    .charge(disjoint)
                    .map_err(|_| RelayError::SettlementPending)?;
                let ledger = selected.price.cost(usage).map_err(error)?;
                if i64::try_from(quoted.0).ok() != Some(ledger) {
                    return Err(RelayError::SettlementPending);
                }
                (usage, Outcome::Completed)
            };
            // Non-streaming relay exposes the body only after this durable final
            // transaction. The checkpoint records the provider observation.
            retry(|| {
                self.charging
                    .checkpoint(&reservation.id, &worker, "provider-final", usage)
            })
            .await
            .map_err(error)?;
            let settlement = retry(|| {
                self.charging
                    .settle(&reservation.id, &worker, usage, outcome)
            })
            .await;
            match settlement {
                Ok(charge)
                    if confirmed_settlement(
                        &charge,
                        &reservation.id,
                        &selected.price,
                        usage,
                        outcome,
                    ) =>
                {
                    Ok(())
                }
                Ok(_) => Err(RelayError::SettlementPending),
                Err(charging::Error::CommitUnknown) => {
                    // Resolve the same operation after a lost COMMIT reply.
                    // Never create another request or dispatch another model call.
                    match self.charging.resolve(&reservation.id).await {
                        Ok(Some(charge))
                            if confirmed_settlement(
                                &charge,
                                &reservation.id,
                                &selected.price,
                                usage,
                                outcome,
                            ) =>
                        {
                            Ok(())
                        }
                        _ => Err(RelayError::SettlementPending),
                    }
                }
                Err(e) => Err(error(e)),
            }
        })
    }
}

fn local_input_bound(request: &Request) -> Result<u64, RelayError> {
    let mut bytes = 16u64;
    let mut add = |n: usize| -> Result<(), RelayError> {
        bytes = bytes
            .checked_add(n as u64)
            .ok_or(RelayError::Limit("input budget"))?;
        Ok(())
    };
    for message in &request.messages {
        add(32)?;
        for block in &message.content {
            add(16)?;
            match block {
                InputBlock::Text(value) | InputBlock::Refusal(value) => add(value.len())?,
                InputBlock::Thinking { text, signature } => {
                    add(text.len())?;
                    if let Some((_, value)) = signature {
                        add(value.len())?;
                    }
                }
                InputBlock::ToolCall {
                    id,
                    name,
                    arguments,
                    signature,
                } => {
                    add(id.len())?;
                    add(name.len())?;
                    add(arguments.len())?;
                    if let Some((_, value)) = signature {
                        add(value.len())?;
                    }
                }
                InputBlock::ToolResult { id, content } => {
                    add(id.len())?;
                    add(content.len())?;
                }
            }
        }
    }
    for tool in &request.tools {
        add(64)?;
        add(tool.name.len())?;
        if let Some(value) = &tool.description {
            add(value.len())?;
        }
        add(serde_json::to_vec(&tool.parameters)
            .map_err(|_| RelayError::Invalid("tool schema"))?
            .len())?;
    }
    Ok(bytes)
}

/// The existing numeric-loopback Chat/Responses charging contract. Optional
/// cache reads retain its existing zero-default semantics; this is not a
/// normalization policy for external providers. Missing input/output totals
/// and cache creation unsupported by the ledger must retain the original hold.
pub(super) fn complete_usage(observed: crate::relay::Usage) -> Result<Usage, RelayError> {
    observed
        .validate()
        .map_err(|_| RelayError::SettlementPending)?;
    if observed
        .cache_creation_input_tokens
        .is_some_and(|value| value != 0)
    {
        return Err(RelayError::SettlementPending);
    }
    let input = observed
        .input_tokens
        .and_then(|value| i64::try_from(value).ok())
        .ok_or(RelayError::SettlementPending)?;
    let output = observed
        .output_tokens
        .and_then(|value| i64::try_from(value).ok())
        .ok_or(RelayError::SettlementPending)?;
    let cached = i64::try_from(observed.cached_input_tokens.unwrap_or(0))
        .map_err(|_| RelayError::SettlementPending)?;
    Ok(Usage {
        input,
        cached,
        output,
    })
}

/// Only the original operation and its immutable price can confirm a lost ACK.
/// This verifies a read receipt; it never retries dispatch or changes funds.
pub(super) fn confirmed_settlement(
    charge: &charging::Charge,
    id: &str,
    price: &charging::Price,
    usage: Usage,
    outcome: Outcome,
) -> bool {
    let expected = match outcome {
        Outcome::Completed => "completed",
        Outcome::Cancelled => "cancelled",
        Outcome::Failed => "failed",
    };
    charge.id == id
        && charge.state == "settled"
        && &charge.price == price
        && charge.usage == usage
        && charge.outcome.as_deref() == Some(expected)
        && price.cost(usage).ok() == Some(charge.settled)
}

#[cfg(test)]
mod a07_billing_tests {
    use super::*;
    use crate::relay::Usage as Observed;

    fn receipt() -> charging::Charge {
        charging::Charge {
            id: "synthetic-model-operation".into(),
            actor_user_id: 1,
            key_id: 2,
            owner_account_id: 3,
            payer_account_id: 3,
            price: charging::Price {
                version: "routing-1-rule-1".into(),
                model: "synthetic-model".into(),
                group: "default".into(),
                input_per_million: 1_000_000,
                cached_per_million: 250_000,
                output_per_million: 2_000_000,
            },
            source: charging::Source::Wallet,
            authorized_at: 1,
            lease_until: 2,
            hard_deadline: 3,
            state: "settled".into(),
            reserved: 100,
            usage: Usage {
                input: 10,
                cached: 2,
                output: 20,
            },
            settled: 49,
            refunded: 0,
            revision: 4,
            outcome: Some("completed".into()),
        }
    }

    #[test]
    fn final_receipt_rejects_unknown_or_inconsistent_usage() {
        for observed in [
            Observed::default(),
            Observed {
                input_tokens: Some(1),
                ..Observed::default()
            },
            Observed {
                output_tokens: Some(1),
                ..Observed::default()
            },
            Observed {
                input_tokens: Some(2),
                output_tokens: Some(3),
                cached_input_tokens: Some(3),
                ..Observed::default()
            },
            Observed {
                input_tokens: Some(2),
                output_tokens: Some(3),
                reasoning_output_tokens: Some(4),
                ..Observed::default()
            },
            Observed {
                input_tokens: Some(2),
                output_tokens: Some(3),
                cache_creation_input_tokens: Some(1),
                ..Observed::default()
            },
            Observed {
                input_tokens: Some(i64::MAX as u64 + 1),
                output_tokens: Some(0),
                ..Observed::default()
            },
        ] {
            assert_eq!(complete_usage(observed), Err(RelayError::SettlementPending));
        }
    }

    #[test]
    fn final_receipt_keeps_cache_as_a_subset_not_an_extra_charge() {
        assert_eq!(
            complete_usage(Observed {
                input_tokens: Some(10),
                output_tokens: Some(20),
                cached_input_tokens: Some(2),
                cache_creation_input_tokens: Some(0),
                reasoning_output_tokens: Some(5),
            }),
            Ok(Usage {
                input: 10,
                cached: 2,
                output: 20
            })
        );
    }

    #[test]
    fn settlement_confirmation_requires_original_price_and_exact_amount() {
        let expected = receipt();
        let confirms = |value: &charging::Charge| {
            confirmed_settlement(
                value,
                &expected.id,
                &expected.price,
                expected.usage,
                Outcome::Completed,
            )
        };
        assert!(confirms(&expected));
        for field in 0..8 {
            let mut changed = expected.clone();
            match field {
                0 => changed.id.push_str("-other"),
                1 => changed.state = "reconcile".into(),
                2 => changed.price.version.push_str("-other"),
                3 => changed.price.input_per_million += 1,
                4 => changed.usage.output += 1,
                5 => changed.settled -= 1,
                6 => changed.settled += 1,
                _ => changed.outcome = Some("cancelled".into()),
            }
            assert!(!confirms(&changed));
        }
    }

    #[tokio::test]
    async fn uncertain_commit_is_read_not_retried() {
        let mut calls = 0;
        let result = retry(|| {
            calls += 1;
            std::future::ready(Err::<(), _>(charging::Error::CommitUnknown))
        })
        .await;
        assert_eq!(result, Err(charging::Error::CommitUnknown));
        assert_eq!(calls, 1);
    }

    #[tokio::test]
    async fn rolled_back_operations_keep_a_bounded_retry_count() {
        let mut calls = 0;
        let result = retry(|| {
            calls += 1;
            std::future::ready(Err::<(), _>(charging::Error::Retry))
        })
        .await;
        assert_eq!(result, Err(charging::Error::Retry));
        assert_eq!(calls, 3);
    }
}
