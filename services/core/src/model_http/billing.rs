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
            let mut conservative = selected.price.clone();
            conservative.input_per_million = conservative
                .input_per_million
                .max(conservative.cached_per_million);
            let reserve = conservative
                .cost(Usage {
                    upstream_cost_atto_usd: selected
                        .price
                        .upstream_cost_group_ppm
                        .map(|_| crate::upstream_cost::ATTO_USD),
                    input: self.policy.input_token_ceiling,
                    cached: 0,
                    output: i64::from(request.max_output_tokens),
                })
                .map_err(error)?;
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
            let (worker, output_ceiling, price, secret, peer) = {
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
                    op.selected
                        .as_ref()
                        .ok_or(RelayError::RouteUnavailable)?
                        .price
                        .clone(),
                    op.secret.clone(),
                    op.peer,
                )
            };
            let upstream_cost = price.upstream_cost_group_ppm.is_some();
            if reservation.id != report.request_id {
                return Err(RelayError::Conflict);
            }
            let (usage, outcome) = if !report.upstream_attempted {
                (
                    Usage {
                        upstream_cost_atto_usd: upstream_cost.then_some(0),
                        ..Usage::default()
                    },
                    Outcome::Cancelled,
                )
            } else {
                // Partial/local output is not an authoritative final provider
                // usage receipt. Never substitute missing usage with zero.
                if !report.provider_complete {
                    return Err(RelayError::SettlementPending);
                }
                let observed = report.usage;
                if !upstream_cost && observed.cache_creation_input_tokens.is_some_and(|v| v != 0) {
                    return Err(RelayError::SettlementPending);
                }
                if upstream_cost && observed.upstream_cost_atto_usd.is_none() {
                    return Err(RelayError::SettlementPending);
                }
                let input = observed
                    .input_tokens
                    .and_then(|v| i64::try_from(v).ok())
                    .ok_or(RelayError::SettlementPending)?;
                let output = observed
                    .output_tokens
                    .and_then(|v| i64::try_from(v).ok())
                    .ok_or(RelayError::SettlementPending)?;
                let cached = i64::try_from(observed.cached_input_tokens.unwrap_or(0))
                    .map_err(|_| RelayError::SettlementPending)?;
                if input > self.policy.input_token_ceiling || output > i64::from(output_ceiling) {
                    return Err(RelayError::SettlementPending);
                }
                (
                    Usage {
                        upstream_cost_atto_usd: if upstream_cost {
                            observed.upstream_cost_atto_usd
                        } else {
                            None
                        },
                        input,
                        cached,
                        output,
                    },
                    Outcome::Completed,
                )
            };
            if upstream_cost {
                let amount = price.cost(usage).map_err(error)?;
                let charge = self
                    .charging
                    .resolve(&reservation.id)
                    .await
                    .map_err(error)?
                    .ok_or(RelayError::SettlementPending)?;
                if amount > charge.reserved {
                    retry(|| {
                        self.charging.increase_with_context(
                            &secret,
                            &reservation.id,
                            &worker,
                            amount,
                            peer,
                        )
                    })
                    .await
                    .map_err(error)?;
                }
            }
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
                Ok(_) => Ok(()),
                Err(charging::Error::CommitUnknown) => {
                    // Resolve the same operation after a lost COMMIT reply.
                    // Never create another request or dispatch another model call.
                    let expected = match outcome {
                        Outcome::Completed => "completed",
                        Outcome::Cancelled => "cancelled",
                        Outcome::Failed => "failed",
                    };
                    match self.charging.resolve(&reservation.id).await {
                        Ok(Some(charge))
                            if charge.state == "settled"
                                && charge.usage == usage
                                && charge.outcome.as_deref() == Some(expected) =>
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
