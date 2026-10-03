//! Accounting follows response consumption, including downstream cancellation.
//! Every async finalization owns its work so dropping a client cannot cancel
//! an in-flight settlement transaction. The durable reservation remains when
//! storage is unavailable, rather than being silently treated as settled.

use axum::body::{Body, Bytes, to_bytes};
use futures_util::{StreamExt, stream::BoxStream};

use super::{
    MAX_RELAY_BODY_BYTES, OpenAiRelayBody, OpenAiRelayEndpoint, OpenAiRelayFailure,
    OpenAiRelayResult, PgOpenAiRelayService, Reservation, UsageTracker, internal_failure,
};

pub(super) struct Guard {
    service: PgOpenAiRelayService,
    reservation: Option<Reservation>,
    tracker: UsageTracker,
}

impl Guard {
    pub fn reservation(&self) -> Option<&Reservation> {
        self.reservation.as_ref()
    }

    pub fn replace_reservation(
        &mut self,
        reservation: Reservation,
        endpoint: OpenAiRelayEndpoint,
        body: &[u8],
    ) {
        self.tracker = UsageTracker::new(endpoint)
            .with_input(reservation.model.clone(), reservation.estimated_prompt)
            .with_response_models(
                reservation.model.clone(),
                reservation.upstream_model.clone(),
            )
            .with_request(body);
        self.reservation = Some(reservation);
    }
    pub fn new(
        service: PgOpenAiRelayService,
        reservation: Reservation,
        endpoint: OpenAiRelayEndpoint,
        request_body: &[u8],
    ) -> Self {
        let tracker = UsageTracker::new(endpoint)
            .with_input(reservation.model.clone(), reservation.estimated_prompt)
            .with_response_models(
                reservation.model.clone(),
                reservation.upstream_model.clone(),
            )
            .with_request(request_body);
        Self {
            service,
            reservation: Some(reservation),
            tracker,
        }
    }

    pub async fn finish(&mut self) -> Result<(), OpenAiRelayFailure> {
        let Some(reservation) = self.reservation.take() else {
            return Ok(());
        };
        self.tracker.finalize();
        let service = self.service.clone();
        let evidence = self.tracker.evidence.clone();
        let permit = service.settlement_tracker.enter();
        // Dropping the join handle does not abort its transaction. Retry only
        // storage settlement, never the already executed provider request.
        tokio::spawn(async move {
            let _permit = permit;
            service.settle_with_retries(reservation, evidence).await
        })
        .await
        .map_err(|_| internal_failure())?
    }

    pub async fn response(
        mut self,
        mut result: OpenAiRelayResult,
    ) -> Result<OpenAiRelayResult, OpenAiRelayFailure> {
        let OpenAiRelayBody::Upstream { content_type, body } = result.body else {
            return Err(internal_failure());
        };
        let stream = content_type
            .as_ref()
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.split(';').next())
            .is_some_and(|value| value.trim().eq_ignore_ascii_case("text/event-stream"));
        let body = if stream {
            let state = StreamState {
                upstream: body.into_data_stream().boxed(),
                guard: self,
                ended: false,
            };
            Body::from_stream(futures_util::stream::unfold(
                state,
                |mut state| async move {
                    if state.ended {
                        return None;
                    }
                    match state.upstream.next().await {
                        Some(Ok(bytes)) => {
                            state.guard.tracker.feed(&bytes);
                            if state.guard.tracker.evidence.terminal {
                                state.ended = true;
                                // Go keeps an already generated model result even
                                // if final storage settlement needs reconciliation.
                                // The reservation is durable and blocks replays.
                                let _ = state.guard.finish().await;
                            }
                            Some((Ok(bytes), state))
                        }
                        result => {
                            state.ended = true;
                            let clean = result.is_none();
                            state.guard.tracker.eof(clean);
                            let _ = state.guard.finish().await;
                            if clean {
                                None
                            } else {
                                Some((
                                    Err(std::io::Error::other("upstream response interrupted")),
                                    state,
                                ))
                            }
                        }
                    }
                },
            ))
        } else {
            let bytes = match to_bytes(body, MAX_RELAY_BODY_BYTES).await {
                Ok(bytes) => bytes,
                Err(_) => {
                    self.finish().await?;
                    return Err(OpenAiRelayFailure::new(
                        axum::http::StatusCode::INTERNAL_SERVER_ERROR,
                        "read_response_body_failed",
                        "failed to read upstream response body",
                    ));
                }
            };
            let bytes = match self.tracker.json(&bytes) {
                Ok(Some(rewritten)) => {
                    result.headers.remove(axum::http::header::CONTENT_LENGTH);
                    Bytes::from(rewritten)
                }
                Ok(None) => bytes,
                Err(error) => {
                    self.finish().await?;
                    return Err(error);
                }
            };
            let _ = self.finish().await;
            Body::from(bytes)
        };
        result.body = OpenAiRelayBody::Upstream { content_type, body };
        Ok(result)
    }
}

struct StreamState {
    upstream: BoxStream<'static, Result<Bytes, axum::Error>>,
    guard: Guard,
    ended: bool,
}

impl Drop for Guard {
    fn drop(&mut self) {
        let Some(reservation) = self.reservation.take() else {
            return;
        };
        self.tracker.finalize();
        let service = self.service.clone();
        let evidence = self.tracker.evidence.clone();
        if let Ok(runtime) = tokio::runtime::Handle::try_current() {
            let permit = service.settlement_tracker.enter();
            runtime.spawn(async move {
                let _permit = permit;
                let _ = service.settle_with_retries(reservation, evidence).await;
            });
        }
    }
}
