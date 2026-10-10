//! Rust-owned model forwarding. There is no Go client or database connection here.
//!
//! This module does not register public routes or change readiness. Integration
//! must supply real core routing and durable billing adapters before enabling it.
mod decode;
mod encode;
mod request;
mod sse;
#[cfg(test)]
mod tests;
mod types;
pub use request::{FunctionTool, InputBlock, Message, Request, Role, ToolChoice};
pub use types::*;

use axum::{
    body::{Body, Bytes},
    http::{Response, header},
};
use decode::Decoder;
use encode::Encoder;
use std::{
    collections::BTreeMap,
    future::Future,
    pin::Pin,
    sync::{Arc, Mutex},
    task::{Context, Poll},
    time::Duration,
};
use tokio::{
    sync::{Notify, OwnedSemaphorePermit, Semaphore, mpsc, oneshot, watch},
    time::{Instant, timeout, timeout_at},
};
use tokio_stream::Stream;

#[derive(Clone)]
pub struct Cancellation(watch::Sender<bool>);
impl Default for Cancellation {
    fn default() -> Self {
        Self(watch::channel(false).0)
    }
}
impl Cancellation {
    pub fn cancel(&self) {
        self.0.send_replace(true);
    }
    pub fn is_cancelled(&self) -> bool {
        *self.0.borrow()
    }
    async fn cancelled(&self) {
        let mut rx = self.0.subscribe();
        loop {
            if *rx.borrow_and_update() {
                return;
            }
            if rx.changed().await.is_err() {
                return;
            }
        }
    }
}

/// Dropping the HTTP body cancels the upstream, but not the billing finalizer.
pub struct RelayBody {
    receiver: mpsc::Receiver<Result<Bytes, RelayError>>,
    cancel: Cancellation,
}
impl RelayBody {
    pub async fn next_chunk(&mut self) -> Option<Result<Bytes, RelayError>> {
        self.receiver.recv().await
    }
}
impl Stream for RelayBody {
    type Item = Result<Bytes, RelayError>;
    fn poll_next(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<Option<Self::Item>> {
        self.get_mut().receiver.poll_recv(cx)
    }
}
impl Drop for RelayBody {
    fn drop(&mut self) {
        self.cancel.cancel();
    }
}

pub struct Session {
    pub head: oneshot::Receiver<Result<ResponseHead, RelayError>>,
    pub body: RelayBody,
    pub completion: oneshot::Receiver<Completion>,
    pub cancel: Cancellation,
}
impl Session {
    /// A response helper, not a registered public HTTP endpoint. The integration
    /// layer must retain completion records or otherwise observe reconciliation.
    pub async fn into_http(
        self,
    ) -> Result<(Response<Body>, oneshot::Receiver<Completion>), RelayError> {
        let head = self
            .head
            .await
            .map_err(|_| RelayError::SettlementPending)??;
        let response = Response::builder()
            .status(head.status)
            .header(header::CONTENT_TYPE, head.content_type)
            .header(header::CACHE_CONTROL, "no-store")
            .header("x-accel-buffering", "no")
            .body(Body::from_stream(self.body))
            .map_err(|_| RelayError::Invalid("invalid response head"))?;
        Ok((response, self.completion))
    }
}

struct Registry {
    next: u64,
    draining: bool,
    active: BTreeMap<u64, Cancellation>,
}
struct Inner {
    client: reqwest::Client,
    router: Arc<dyn RouteProvider>,
    billing: Arc<dyn Billing>,
    limits: Limits,
    permits: Arc<Semaphore>,
    registry: Mutex<Registry>,
    changed: Notify,
}
#[derive(Clone)]
pub struct Relay {
    inner: Arc<Inner>,
}
struct Active {
    inner: Arc<Inner>,
    id: u64,
    _permit: OwnedSemaphorePermit,
}
impl Drop for Active {
    fn drop(&mut self) {
        self.inner
            .registry
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .active
            .remove(&self.id);
        self.inner.changed.notify_waiters();
    }
}
impl Relay {
    pub fn new(
        router: Arc<dyn RouteProvider>,
        billing: Arc<dyn Billing>,
        limits: Limits,
    ) -> Result<Self, RelayError> {
        limits.validate()?;
        let client = reqwest::Client::builder()
            .connect_timeout(limits.connect_timeout)
            .redirect(reqwest::redirect::Policy::none())
            .retry(reqwest::retry::never())
            .referer(false)
            .no_proxy()
            .no_gzip()
            .no_brotli()
            .no_deflate()
            .no_zstd()
            // No idle connections for an unbounded set of route origins. Active
            // connections are bounded by the admission semaphore, including H2.
            .pool_max_idle_per_host(0)
            .http2_adaptive_window(false)
            .http2_initial_stream_window_size(65_536)
            .http2_initial_connection_window_size(262_144)
            .http2_max_frame_size(16_384)
            .http2_max_header_list_size(16_384)
            .user_agent("lmm-core-relay/0.1")
            .build()
            .map_err(|_| RelayError::Invalid("HTTP client initialization failed"))?;
        Ok(Self {
            inner: Arc::new(Inner {
                client,
                router,
                billing,
                permits: Arc::new(Semaphore::new(limits.max_in_flight)),
                limits,
                registry: Mutex::new(Registry {
                    next: 0,
                    draining: false,
                    active: BTreeMap::new(),
                }),
                changed: Notify::new(),
            }),
        })
    }
    /// Call Request::parse before this method. No permissive billing adapter is
    /// provided; admission cannot send any upstream bytes before reserve succeeds.
    pub fn start(&self, request: Request, context: RequestContext) -> Result<Session, RelayError> {
        context.validate()?;
        let permit = self
            .inner
            .permits
            .clone()
            .try_acquire_owned()
            .map_err(|e| match e {
                tokio::sync::TryAcquireError::Closed => RelayError::Draining,
                tokio::sync::TryAcquireError::NoPermits => RelayError::Busy,
            })?;
        let cancel = Cancellation::default();
        let id = {
            let mut registry = self
                .inner
                .registry
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner);
            if registry.draining {
                return Err(RelayError::Draining);
            }
            let id = registry.next;
            registry.next = registry.next.checked_add(1).ok_or(RelayError::Busy)?;
            registry.active.insert(id, cancel.clone());
            id
        };
        let active = Active {
            inner: self.inner.clone(),
            id,
            _permit: permit,
        };
        let (sender, receiver) = mpsc::channel(self.inner.limits.queue_chunks);
        let (head_tx, head) = oneshot::channel();
        let (done_tx, completion) = oneshot::channel();
        let body = RelayBody {
            receiver,
            cancel: cancel.clone(),
        };
        let worker = Worker::new(
            self.inner.clone(),
            request,
            context,
            cancel.clone(),
            sender,
            head_tx,
            done_tx,
        );
        tokio::spawn(async move {
            let _active = active;
            worker.run().await;
        });
        Ok(Session {
            head,
            body,
            completion,
            cancel,
        })
    }
    pub fn active_requests(&self) -> usize {
        self.inner
            .registry
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .active
            .len()
    }
    async fn wait_empty(&self, until: Instant) -> bool {
        loop {
            let changed = self.inner.changed.notified();
            tokio::pin!(changed);
            changed.as_mut().enable();
            if self.active_requests() == 0 {
                return true;
            }
            if timeout_at(until, changed).await.is_err() {
                return self.active_requests() == 0;
            }
        }
    }
    /// Reject new work, let accepted requests finish, then cancel remaining
    /// upstreams. Never abort a finalizer. `remaining` must be checked by shutdown.
    pub async fn drain(&self, grace: Duration) -> DrainReport {
        {
            let mut registry = self
                .inner
                .registry
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner);
            registry.draining = true;
            self.inner.permits.close();
        }
        if self
            .wait_empty(Instant::now() + grace.min(Duration::from_secs(86_400)))
            .await
        {
            return DrainReport {
                cancelled: 0,
                remaining: 0,
            };
        }
        let cancelled = {
            let registry = self
                .inner
                .registry
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner);
            for c in registry.active.values() {
                c.cancel();
            }
            registry.active.len()
        };
        self.wait_empty(
            Instant::now() + self.inner.limits.hook_timeout * 2 + Duration::from_secs(1),
        )
        .await;
        DrainReport {
            cancelled,
            remaining: self.active_requests(),
        }
    }
}

struct Gate {
    cancel: Cancellation,
    deadline: Instant,
}
impl Gate {
    async fn run<T, F>(&self, duration: Duration, phase: Phase, future: F) -> Result<T, RelayError>
    where
        F: Future<Output = Result<T, RelayError>>,
    {
        if self.cancel.is_cancelled() {
            return Err(RelayError::Cancelled);
        }
        if Instant::now() >= self.deadline {
            return Err(RelayError::Timeout(Phase::Total));
        }
        let end = self.deadline.min(Instant::now() + duration);
        let timeout_phase = if end == self.deadline {
            Phase::Total
        } else {
            phase
        };
        tokio::select! {
            biased;
            _ = self.cancel.cancelled() => Err(RelayError::Cancelled),
            result = timeout_at(end,future) => result.unwrap_or(Err(RelayError::Timeout(timeout_phase))),
        }
    }
}
struct Worker {
    inner: Arc<Inner>,
    request: Request,
    context: RequestContext,
    gate: Gate,
    sender: mpsc::Sender<Result<Bytes, RelayError>>,
    head: Option<oneshot::Sender<Result<ResponseHead, RelayError>>>,
    done: oneshot::Sender<Completion>,
    encoder: Encoder,
    reservation: Option<Reservation>,
    reserve_ambiguous: bool,
    report: Report,
    terminal: Vec<Vec<u8>>,
}
impl Worker {
    fn new(
        inner: Arc<Inner>,
        request: Request,
        context: RequestContext,
        cancel: Cancellation,
        sender: mpsc::Sender<Result<Bytes, RelayError>>,
        head: oneshot::Sender<Result<ResponseHead, RelayError>>,
        done: oneshot::Sender<Completion>,
    ) -> Self {
        let report = Report {
            request_id: context.request_id.clone(),
            route_id: None,
            price_version: None,
            outcome: Outcome::Failed,
            usage: Usage::default(),
            finish_reason: None,
            upstream_attempted: false,
            attempts: 0,
            upstream_status: None,
            upstream_bytes: 0,
            emitted_bytes: 0,
        };
        let encoder = Encoder::new(&request, &context.request_id, &inner.limits);
        let deadline = Instant::now() + inner.limits.total_timeout;
        Self {
            inner,
            request,
            context,
            gate: Gate { cancel, deadline },
            sender,
            head: Some(head),
            done,
            encoder,
            reservation: None,
            reserve_ambiguous: false,
            report,
            terminal: Vec::new(),
        }
    }
    fn send_head(&mut self) -> Result<(), RelayError> {
        if let Some(head) = self.head.take() {
            head.send(Ok(ResponseHead {
                status: 200,
                content_type: if self.request.stream {
                    "text/event-stream; charset=utf-8"
                } else {
                    "application/json"
                },
            }))
            .map_err(|_| RelayError::Cancelled)?;
        }
        Ok(())
    }
    async fn write(&mut self, bytes: &[u8]) -> Result<(), RelayError> {
        for chunk in bytes.chunks(self.inner.limits.chunk_bytes) {
            self.gate
                .run(self.inner.limits.write_timeout, Phase::Write, async {
                    self.sender
                        .send(Ok(Bytes::copy_from_slice(chunk)))
                        .await
                        .map_err(|_| RelayError::Cancelled)
                })
                .await?;
            self.report.emitted_bytes = self.report.emitted_bytes.saturating_add(chunk.len());
        }
        Ok(())
    }
    async fn events(&mut self, events: Vec<Event>) -> Result<(), RelayError> {
        for event in events {
            for packet in self.encoder.consume(event)? {
                self.write(&packet).await?;
            }
        }
        Ok(())
    }
    fn count_input(&mut self, chunk: &Bytes) -> Result<(), RelayError> {
        if chunk.len() > self.inner.limits.transport_chunk_bytes {
            return Err(RelayError::Limit("transport chunk"));
        }
        self.report.upstream_bytes = self
            .report
            .upstream_bytes
            .checked_add(chunk.len())
            .ok_or(RelayError::Limit("upstream bytes"))?;
        if self.report.upstream_bytes > self.inner.limits.upstream_bytes {
            return Err(RelayError::Limit("upstream bytes"));
        }
        Ok(())
    }
    async fn open(
        &mut self,
        route: &Route,
        body: Vec<u8>,
    ) -> Result<reqwest::Response, RelayError> {
        loop {
            if self.gate.cancel.is_cancelled() {
                return Err(RelayError::Cancelled);
            }
            if Instant::now() >= self.gate.deadline {
                return Err(RelayError::Timeout(Phase::Total));
            }
            self.report.attempts += 1;
            self.report.upstream_attempted = true;
            let request = self
                .inner
                .client
                .post(route.endpoint.clone())
                .header(header::CONTENT_TYPE, "application/json")
                .header(header::ACCEPT_ENCODING, "identity")
                .header(
                    header::ACCEPT,
                    if self.request.stream {
                        "text/event-stream"
                    } else {
                        "application/json"
                    },
                )
                .body(body.clone());
            let request = match route.protocol {
                UpstreamProtocol::Chat | UpstreamProtocol::Responses => {
                    request.header(header::AUTHORIZATION, route.credential.clone())
                }
                UpstreamProtocol::Anthropic => request
                    .header("x-api-key", route.credential.clone())
                    .header("anthropic-version", "2023-06-01"),
                UpstreamProtocol::Gemini => {
                    request.header("x-goog-api-key", route.credential.clone())
                }
            };
            let result = self
                .gate
                .run(self.inner.limits.header_timeout, Phase::Headers, async {
                    Ok(request.send().await)
                })
                .await?;
            match result {
                Ok(response) => return Ok(response),
                Err(e)
                    if e.is_connect()
                        && self.report.attempts <= self.inner.limits.connect_retries =>
                {
                    self.gate
                        .run(self.inner.limits.header_timeout, Phase::Connect, async {
                            tokio::time::sleep(self.inner.limits.retry_backoff).await;
                            Ok(())
                        })
                        .await?;
                }
                Err(e) if e.is_timeout() => return Err(RelayError::Timeout(Phase::Connect)),
                Err(_) => return Err(RelayError::UpstreamConnection),
            }
        }
    }
    async fn execute(&mut self) -> Result<(), RelayError> {
        let route = self
            .gate
            .run(
                self.inner.limits.hook_timeout,
                Phase::Route,
                self.inner.router.select(&self.context, &self.request),
            )
            .await?;
        self.report.route_id = Some(route.id.clone());
        self.report.price_version = Some(route.price_version);
        let payload = self.request.upstream(&route)?;
        let body = serde_json::to_vec(&payload)
            .map_err(|_| RelayError::Invalid("request encoding failed"))?;
        if body.len() > self.inner.limits.request_bytes {
            return Err(RelayError::Limit("encoded request"));
        }
        // Do not cancel a reserve future just because the client disconnected:
        // first obtain its committed handle, then finalize without sending HTTP.
        self.reserve_ambiguous = true;
        let reservation = timeout(
            self.inner.limits.hook_timeout,
            self.inner
                .billing
                .reserve(&self.context, &route, &self.request),
        )
        .await
        .map_err(|_| RelayError::Timeout(Phase::Reserve))??;
        self.reservation = Some(reservation);
        self.reserve_ambiguous = false;
        let mut response = self.open(&route, body).await?;
        self.report.upstream_status = Some(response.status().as_u16());
        if response.status() != reqwest::StatusCode::OK {
            return Err(RelayError::UpstreamStatus(response.status().as_u16()));
        }
        let expected = if self.request.stream {
            "text/event-stream"
        } else {
            "application/json"
        };
        let content_type = response
            .headers()
            .get(header::CONTENT_TYPE)
            .and_then(|v| v.to_str().ok())
            .and_then(|v| v.split(';').next())
            .map(str::trim);
        if content_type != Some(expected) {
            return Err(RelayError::Protocol("unexpected upstream content type"));
        }
        if response
            .headers()
            .get(header::CONTENT_ENCODING)
            .is_some_and(|v| v.as_bytes() != b"identity")
        {
            return Err(RelayError::Protocol(
                "compressed upstream bodies are disabled",
            ));
        }
        let max_body = if self.request.stream {
            self.inner.limits.upstream_bytes
        } else {
            self.inner.limits.output_bytes
        };
        if response
            .content_length()
            .is_some_and(|n| n > max_body as u64)
        {
            return Err(RelayError::Limit("upstream body"));
        }
        let mut decoder = Decoder::new(route.protocol, &self.inner.limits);
        if self.request.stream {
            self.send_head()?;
            let mut parser = sse::Parser::new(self.inner.limits.event_bytes);
            'read: loop {
                let next = self
                    .gate
                    .run(self.inner.limits.read_timeout, Phase::Read, async {
                        response
                            .chunk()
                            .await
                            .map_err(|_| RelayError::UpstreamConnection)
                    })
                    .await?;
                let Some(chunk) = next else {
                    parser.finish()?;
                    self.events(decoder.eof()?).await?;
                    break;
                };
                self.count_input(&chunk)?;
                for byte in chunk {
                    if let Some(frame) = parser.feed(byte)? {
                        self.events(decoder.frame(frame)?).await?;
                        if decoder.is_done() {
                            break 'read;
                        }
                    }
                }
            }
        } else {
            let mut data = Vec::new();
            loop {
                let next = self
                    .gate
                    .run(self.inner.limits.read_timeout, Phase::Read, async {
                        response
                            .chunk()
                            .await
                            .map_err(|_| RelayError::UpstreamConnection)
                    })
                    .await?;
                let Some(chunk) = next else {
                    break;
                };
                self.count_input(&chunk)?;
                if data.len().saturating_add(chunk.len()) > max_body {
                    return Err(RelayError::Limit("non-stream response"));
                }
                data.extend_from_slice(&chunk);
            }
            let value = serde_json::from_slice(&data)
                .map_err(|_| RelayError::Protocol("invalid upstream JSON"))?;
            self.events(decoder.json(&value)?).await?;
        }
        // Drop the upstream before any money write. Do not drain an unbounded
        // body after a terminal marker merely to reuse its connection.
        drop(response);
        self.terminal = self.encoder.terminal()?;
        Ok(())
    }
    async fn run(mut self) {
        let mut error = self.execute().await.err();
        self.report.usage = self.encoder.usage();
        self.report.finish_reason = self.encoder.finish_reason();
        self.report.outcome = match &error {
            None => Outcome::Completed,
            Some(RelayError::Cancelled) => Outcome::Cancelled,
            Some(RelayError::Timeout(_)) => Outcome::TimedOut,
            _ => Outcome::Failed,
        };
        let settlement = if let Some(reservation) = &self.reservation {
            match timeout(
                self.inner.limits.hook_timeout,
                self.inner.billing.finalize(reservation, &self.report),
            )
            .await
            {
                Ok(Ok(())) => Settlement::Confirmed,
                _ => Settlement::Pending,
            }
        } else if self.reserve_ambiguous {
            Settlement::Pending
        } else {
            Settlement::NotReserved
        };
        if error.is_none() && settlement != Settlement::Confirmed {
            error = Some(RelayError::SettlementPending);
        }
        if error.is_none() {
            if let Err(e) = self.send_head() {
                error = Some(e);
            }
            if error.is_none() {
                for packet in std::mem::take(&mut self.terminal) {
                    if let Err(e) = self.write(&packet).await {
                        error = Some(e);
                        break;
                    }
                }
            }
        }
        if let Some(e) = &error {
            if let Some(head) = self.head.take() {
                let _ = head.send(Err(e.clone()));
            } else if !self.sender.is_closed() {
                let packet = if self.request.stream {
                    self.encoder.error_packet(e).map(Bytes::from)
                } else {
                    Err(e.clone())
                };
                // Error delivery cannot keep a cancelled or stalled request alive.
                let _ = timeout(Duration::from_millis(100), async {
                    match packet {
                        Ok(bytes) => {
                            for chunk in bytes.chunks(self.inner.limits.chunk_bytes) {
                                self.sender
                                    .send(Ok(Bytes::copy_from_slice(chunk)))
                                    .await
                                    .map_err(|_| ())?;
                            }
                        }
                        Err(error) => self.sender.send(Err(error)).await.map_err(|_| ())?,
                    }
                    Ok::<(), ()>(())
                })
                .await;
            }
        }
        let _ = self.done.send(Completion {
            report: self.report,
            settlement,
            error,
        });
    }
}
