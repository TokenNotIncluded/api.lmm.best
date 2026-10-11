use super::Bridge;
use crate::{
    billing::charging::{self, Outcome, Usage},
    relay::{
        Billing, RelayError, RelayFuture, Report, Request, RequestContext, Reservation, Route,
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
async fn retry<T, F, Fut>(mut operation: F) -> charging::Result<T>
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
            let (secret, worker, fingerprint, selected) = {
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
            let charge = retry(|| self.charging.reserve(&secret, &spec, &worker))
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

    fn finalize<'a>(
        &'a self,
        reservation: &'a Reservation,
        report: &'a Report,
    ) -> RelayFuture<'a, ()> {
        Box::pin(async move {
            let worker = self
                .active
                .lock()
                .map_err(|_| RelayError::BillingUnavailable)?
                .get(&reservation.id)
                .ok_or(RelayError::BillingUnavailable)?
                .worker;
            if reservation.id != report.request_id {
                return Err(RelayError::Conflict);
            }
            let (usage, outcome) = if !report.upstream_attempted {
                (Usage::default(), Outcome::Cancelled)
            } else {
                // Partial/local output is not an authoritative final provider
                // usage receipt. Never substitute missing usage with zero.
                if report.outcome != crate::relay::Outcome::Completed {
                    return Err(RelayError::SettlementPending);
                }
                let observed = report.usage;
                if observed.cache_creation_input_tokens.is_some_and(|v| v != 0) {
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
                (
                    Usage {
                        input,
                        cached,
                        output,
                    },
                    Outcome::Completed,
                )
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
