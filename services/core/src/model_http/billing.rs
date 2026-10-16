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

#[derive(Clone, Copy, Debug)]
struct RetryObservation {
    attempt: u8,
    duration: Duration,
    error: Option<charging::Error>,
    final_result: bool,
}

/// Observe the selected existing retry policy without broadening replayable errors.
async fn retry_observed<T, F, Fut, O>(
    deadline: Option<tokio::time::Instant>,
    mut operation: F,
    observe: O,
) -> charging::Result<T>
where
    F: FnMut() -> Fut,
    Fut: Future<Output = charging::Result<T>>,
    O: Fn(RetryObservation),
{
    let mut attempts = 0u8;
    let started = tokio::time::Instant::now();
    let observed = || {
        attempts += 1;
        let attempt = attempts;
        let operation = operation();
        let observe = &observe;
        async move {
            let started = tokio::time::Instant::now();
            let result = operation.await;
            observe(RetryObservation {
                attempt,
                duration: started.elapsed(),
                error: result.as_ref().err().copied(),
                final_result: false,
            });
            result
        }
    };
    let result = match deadline {
        Some(deadline) => retry_bounded(deadline, observed).await,
        None => retry(observed).await,
    };
    observe(RetryObservation {
        attempt: attempts,
        duration: started.elapsed(),
        error: result.as_ref().err().copied(),
        final_result: true,
    });
    result
}

#[derive(Clone, Copy)]
enum FinalizeStage {
    ProviderFinal,
    Settle,
}

// @mark model.settlement-retry role=implementation
async fn retry_finalize<T, F, Fut>(
    deadline: tokio::time::Instant,
    request_id: &str,
    stage: FinalizeStage,
    operation: F,
) -> charging::Result<T>
where
    F: FnMut() -> Fut,
    Fut: Future<Output = charging::Result<T>>,
{
    // Never interpolate unchecked identifiers or database error descriptions.
    let request_id =
        if request_id.len() == 64 && request_id.bytes().all(|byte| byte.is_ascii_hexdigit()) {
            request_id
        } else {
            "invalid_request_id"
        };
    let stage = match stage {
        FinalizeStage::ProviderFinal => "provider-final",
        FinalizeStage::Settle => "settle",
    };
    retry_observed(Some(deadline), operation, |observation| {
        if observation.error.is_none()
            && (!observation.final_result || observation.attempt == 1)
        {
            return;
        }
        // charging::Error is a payload-free enum, unlike an underlying SQL error.
        let category = observation.error.map_or_else(|| "Ok".to_owned(), |error| format!("{error:?}"));
        eprintln!(
            "model_finalize request_id={request_id} stage={stage} attempt={} duration_ms={} final={} category={category}",
            observation.attempt,
            observation.duration.as_millis(),
            observation.final_result,
        );
    })
    .await
}

/// Retry only an explicitly rolled-back reservation, preserving its ID/spec/worker.
/// The monotonic window limits when another attempt may START; it does not
/// interrupt an in-flight transaction or change the enclosing hook timeout.
// @mark model.reservation-retry role=implementation
async fn retry_reserve<T, F, Fut>(operation: F) -> charging::Result<T>
where
    F: FnMut() -> Fut,
    Fut: Future<Output = charging::Result<T>>,
{
    retry_bounded(
        tokio::time::Instant::now() + Duration::from_secs(2),
        operation,
    )
    .await
}

/// Shared rollback-only policy: at most eight starts in the supplied monotonic window.
async fn retry_bounded<T, F, Fut>(
    deadline: tokio::time::Instant,
    mut operation: F,
) -> charging::Result<T>
where
    F: FnMut() -> Fut,
    Fut: Future<Output = charging::Result<T>>,
{
    for attempt in 0..8 {
        if tokio::time::Instant::now() >= deadline {
            return Err(charging::Error::Retry);
        }
        match operation().await {
            Err(charging::Error::Retry) if attempt < 7 => {
                let next = tokio::time::Instant::now() + Duration::from_millis(10u64 << attempt);
                tokio::time::sleep_until(next.min(deadline)).await;
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
            let (secret, worker, fingerprint, selected, peer, oauth_issuer) = {
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
                    op.oauth_issuer.clone(),
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
            let charge = retry_reserve(|| async {
                if let Some(issuer) = &oauth_issuer {
                    self.charging
                        .reserve_oauth_with_context(issuer, &secret, &spec, &worker, peer)
                        .await
                } else {
                    self.charging
                        .reserve_with_context(&secret, &spec, &worker, peer)
                        .await
                }
            })
            .await
            .map_err(error)?;
            if charge.actor_user_id != context.actor_user_id
                || charge.key_id != context.key_id
                || charge.oauth_source != context.oauth_source
            {
                return Err(RelayError::Forbidden);
            }
            if !selected.attempt.is_available() {
                let _ = retry(|| self.charging.release(&spec.id, &worker)).await;
                return Err(RelayError::RouteUnavailable);
            }
            // A durable dispatch claim precedes any HTTP bytes. A false/unknown
            // result never dispatches; expiry recovery retains ambiguous holds.
            let started = retry(|| async {
                if let Some(issuer) = &oauth_issuer {
                    self.charging.start_oauth(issuer, &spec.id, &worker).await
                } else {
                    self.charging.start(&spec.id, &worker).await
                }
            })
            .await
            .map_err(error)?;
            if !started {
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
            let (worker, output_ceiling, price, secret, peer, oauth_issuer) = {
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
                    op.oauth_issuer.clone(),
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
                    retry(|| async {
                        if let Some(issuer) = &oauth_issuer {
                            self.charging
                                .increase_oauth_with_context(
                                    issuer,
                                    &secret,
                                    &reservation.id,
                                    &worker,
                                    amount,
                                    peer,
                                )
                                .await
                        } else {
                            self.charging
                                .increase_with_context(
                                    &secret,
                                    &reservation.id,
                                    &worker,
                                    amount,
                                    peer,
                                )
                                .await
                        }
                    })
                    .await
                    .map_err(error)?;
                }
            }
            // Non-streaming relay exposes the body only after this durable final
            // transaction. The checkpoint records the provider observation.
            // Both finalization stages share one start window; neither stage
            // cancels an in-flight transaction or replays an unknown commit.
            let deadline = tokio::time::Instant::now() + Duration::from_secs(2);
            retry_finalize(
                deadline,
                &reservation.id,
                FinalizeStage::ProviderFinal,
                || {
                    self.charging
                        .checkpoint(&reservation.id, &worker, "provider-final", usage)
                },
            )
            .await
            .map_err(error)?;
            let settlement =
                retry_finalize(deadline, &reservation.id, FinalizeStage::Settle, || {
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

#[cfg(test)]
mod reserve_retry_tests {
    use super::*;
    use std::{cell::Cell, future, task::Poll};

    #[tokio::test]
    async fn explicit_rollbacks_beyond_three_attempts_preserve_success() {
        let calls = Cell::new(0);
        let result = retry_reserve(|| {
            calls.set(calls.get() + 1);
            future::ready(if calls.get() <= 4 {
                Err(charging::Error::Retry)
            } else {
                Ok("original-reservation-handle")
            })
        })
        .await;
        assert_eq!(result, Ok("original-reservation-handle"));
        assert_eq!(calls.get(), 5);
    }

    #[tokio::test]
    async fn explicit_rollbacks_beyond_three_attempts_preserve_business_rejection() {
        let calls = Cell::new(0);
        let result = retry_reserve(|| {
            calls.set(calls.get() + 1);
            future::ready(Err::<(), _>(if calls.get() <= 4 {
                charging::Error::Retry
            } else {
                charging::Error::BudgetExceeded
            }))
        })
        .await;
        assert_eq!(result, Err(charging::Error::BudgetExceeded));
        assert_eq!(calls.get(), 5);
    }

    #[tokio::test]
    async fn every_non_retry_error_returns_without_replay() {
        use charging::Error::*;
        for original in [
            Invalid,
            Unauthorized,
            Forbidden,
            Conflict,
            BudgetExceeded,
            NoFunds,
            NeedReservation,
            StaleWorker,
            WrongState,
            NotFound,
            Storage,
            Ledger,
            CommitUnknown,
        ] {
            let calls = Cell::new(0);
            let result = retry_reserve(|| {
                calls.set(calls.get() + 1);
                future::ready(Err::<(), _>(original))
            })
            .await;
            assert_eq!(result, Err(original));
            assert_eq!(calls.get(), 1, "unexpected replay of {original:?}");
        }
    }

    #[tokio::test]
    // @mark model.reservation-retry role=test
    async fn permanent_retry_has_a_finite_attempt_limit() {
        let calls = Cell::new(0);
        let result = retry_reserve(|| {
            calls.set(calls.get() + 1);
            future::ready(Err::<(), _>(charging::Error::Retry))
        })
        .await;
        assert_eq!(result, Err(charging::Error::Retry));
        assert!(calls.get() > 0 && calls.get() <= 8);
    }

    #[tokio::test]
    async fn elapsed_transaction_time_prevents_a_new_attempt_after_window() {
        let calls = Cell::new(0);
        let result = retry_reserve(|| {
            calls.set(calls.get() + 1);
            async {
                tokio::time::sleep(Duration::from_millis(2050)).await;
                Err::<(), _>(charging::Error::Retry)
            }
        })
        .await;
        assert_eq!(result, Err(charging::Error::Retry));
        assert_eq!(calls.get(), 1);
    }

    #[tokio::test]
    async fn an_in_flight_transaction_result_is_not_interrupted_by_window() {
        let calls = Cell::new(0);
        let result = retry_reserve(|| {
            calls.set(calls.get() + 1);
            async {
                tokio::time::sleep(Duration::from_millis(2050)).await;
                Ok("committed-after-start-window")
            }
        })
        .await;
        assert_eq!(result, Ok("committed-after-start-window"));
        assert_eq!(calls.get(), 1);
    }

    #[tokio::test]
    async fn dropping_during_backoff_never_starts_another_operation() {
        let calls = Cell::new(0);
        let mut pending = Box::pin(retry_reserve(|| {
            calls.set(calls.get() + 1);
            future::ready(Err::<(), _>(charging::Error::Retry))
        }));
        // One deterministic poll completes the first operation and reaches the
        // pending backoff. Dropping models an actual hook-timeout future drop,
        // not a client disconnect (which deliberately does not cancel reserve).
        let first = future::poll_fn(|cx| Poll::Ready(pending.as_mut().poll(cx))).await;
        assert!(first.is_pending());
        assert_eq!(calls.get(), 1);
        drop(pending);
        tokio::time::sleep(Duration::from_millis(25)).await;
        assert_eq!(calls.get(), 1);
    }

    #[tokio::test]
    async fn existing_non_reservation_retry_keeps_three_attempts() {
        let calls = Cell::new(0);
        let result = retry(|| {
            calls.set(calls.get() + 1);
            future::ready(Err::<(), _>(charging::Error::Retry))
        })
        .await;
        assert_eq!(result, Err(charging::Error::Retry));
        assert_eq!(calls.get(), 3);
    }
}

#[cfg(test)]
mod finalize_diagnostic_tests {
    use super::*;
    use std::{
        cell::{Cell, RefCell},
        future,
    };

    async fn observed_case(original: charging::Result<&'static str>, expected_attempts: u8) {
        let calls = Cell::new(0u8);
        let observations = RefCell::new(Vec::new());
        let result = retry_observed(
            None,
            || {
                calls.set(calls.get() + 1);
                future::ready(original)
            },
            |observation| observations.borrow_mut().push(observation),
        )
        .await;
        assert_eq!(result, original);
        assert_eq!(calls.get(), expected_attempts);
        let observations = observations.into_inner();
        assert_eq!(observations.len(), usize::from(expected_attempts) + 1);
        for (index, observation) in observations[..usize::from(expected_attempts)]
            .iter()
            .enumerate()
        {
            assert_eq!(observation.attempt, index as u8 + 1);
            assert!(!observation.final_result);
            assert_eq!(observation.error, original.err());
        }
        let final_result = observations.last().unwrap();
        assert!(final_result.final_result);
        assert_eq!(final_result.attempt, expected_attempts);
        assert_eq!(final_result.error, original.err());
    }

    #[tokio::test]
    async fn retry_exhaustion_is_observed_after_exactly_three_attempts() {
        observed_case(Err(charging::Error::Retry), 3).await;
    }

    #[tokio::test]
    async fn commit_unknown_is_observed_without_replaying() {
        observed_case(Err(charging::Error::CommitUnknown), 1).await;
    }

    #[tokio::test]
    async fn non_retry_errors_are_observed_without_replaying() {
        use charging::Error::*;
        for error in [
            Invalid,
            Unauthorized,
            Forbidden,
            Conflict,
            BudgetExceeded,
            NoFunds,
            NeedReservation,
            StaleWorker,
            WrongState,
            NotFound,
            Storage,
            Ledger,
        ] {
            observed_case(Err(error), 1).await;
        }
    }

    #[tokio::test]
    async fn success_preserves_the_original_return_value() {
        observed_case(Ok("original-settlement"), 1).await;
    }
}

#[cfg(test)]
mod settlement_retry_tests {
    use super::*;
    use std::{cell::Cell, future};
    const REQUEST: &str = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";

    #[tokio::test]
    async fn three_explicit_rollbacks_then_success_preserves_the_original_value() {
        let calls = Cell::new(0);
        let deadline = tokio::time::Instant::now() + Duration::from_secs(2);
        let result = retry_finalize(deadline, REQUEST, FinalizeStage::Settle, || {
            calls.set(calls.get() + 1);
            future::ready(if calls.get() <= 3 {
                Err(charging::Error::Retry)
            } else {
                Ok("original-settlement")
            })
        })
        .await;
        assert_eq!(result, Ok("original-settlement"));
        assert_eq!(calls.get(), 4);
    }

    #[tokio::test]
    async fn every_non_retry_error_including_unknown_commit_never_replays() {
        use charging::Error::*;
        for error in [
            Invalid,
            Unauthorized,
            Forbidden,
            Conflict,
            BudgetExceeded,
            NoFunds,
            NeedReservation,
            StaleWorker,
            WrongState,
            NotFound,
            Storage,
            Ledger,
            CommitUnknown,
        ] {
            let calls = Cell::new(0);
            let result = retry_finalize(
                tokio::time::Instant::now() + Duration::from_secs(2),
                REQUEST,
                FinalizeStage::Settle,
                || {
                    calls.set(calls.get() + 1);
                    future::ready(Err::<(), _>(error))
                },
            )
            .await;
            assert_eq!(result, Err(error));
            assert_eq!(calls.get(), 1);
        }
    }

    #[tokio::test]
    async fn permanent_rollbacks_have_exactly_eight_attempts() {
        let calls = Cell::new(0);
        let result = retry_finalize(
            tokio::time::Instant::now() + Duration::from_secs(2),
            REQUEST,
            FinalizeStage::Settle,
            || {
                calls.set(calls.get() + 1);
                future::ready(Err::<(), _>(charging::Error::Retry))
            },
        )
        .await;
        assert_eq!(result, Err(charging::Error::Retry));
        assert_eq!(calls.get(), 8);
    }

    #[tokio::test]
    async fn expired_shared_window_does_not_start_settle_after_checkpoint() {
        let deadline = tokio::time::Instant::now() + Duration::from_millis(20);
        let checkpoint =
            retry_finalize(deadline, REQUEST, FinalizeStage::ProviderFinal, || async {
                tokio::time::sleep(Duration::from_millis(40)).await;
                Ok("checkpoint-committed")
            })
            .await;
        assert_eq!(checkpoint, Ok("checkpoint-committed"));
        let calls = Cell::new(0);
        let settlement = retry_finalize(deadline, REQUEST, FinalizeStage::Settle, || {
            calls.set(calls.get() + 1);
            future::ready(Ok(()))
        })
        .await;
        assert_eq!(settlement, Err(charging::Error::Retry));
        assert_eq!(calls.get(), 0);
    }

    #[tokio::test]
    async fn an_inflight_settlement_is_not_cancelled_by_the_start_window() {
        let result = retry_finalize(
            tokio::time::Instant::now() + Duration::from_millis(20),
            REQUEST,
            FinalizeStage::Settle,
            || async {
                tokio::time::sleep(Duration::from_millis(40)).await;
                Ok("settlement-committed-after-start-window")
            },
        )
        .await;
        assert_eq!(result, Ok("settlement-committed-after-start-window"));
    }

    #[tokio::test]
    async fn rollback_after_the_window_cannot_start_a_second_settlement() {
        let calls = Cell::new(0);
        let result = retry_finalize(
            tokio::time::Instant::now() + Duration::from_millis(20),
            REQUEST,
            FinalizeStage::Settle,
            || {
                calls.set(calls.get() + 1);
                async {
                    tokio::time::sleep(Duration::from_millis(40)).await;
                    Err::<(), _>(charging::Error::Retry)
                }
            },
        )
        .await;
        assert_eq!(result, Err(charging::Error::Retry));
        assert_eq!(calls.get(), 1);
    }
}
