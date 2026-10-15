//! Explicitly uncharged moderation traffic. This transport has no routing,
//! billing, wallet, reservation or ordinary-provider handle. The caller must
//! obtain Core's durable moderation admission before starting it.
use super::honeypot_network::{NetworkTarget, client_builder};
pub(crate) use super::honeypot_network::{canonical_origin, validate_base_url, validate_origin};
use super::{
    BlockKind, Cancellation, DrainReport, Event, FinishReason, Gate, Limits, Phase, Registry,
    RelayBody, RelayError, Request, ResponseHead, UpstreamProtocol, decode::Decoder,
    encode::Encoder, sse,
};
use axum::{body::Bytes, http::header};
use reqwest::{Url, header::HeaderValue};
use std::{
    sync::{Arc, Mutex},
    time::Duration,
};
use tokio::{
    sync::{Notify, Semaphore, mpsc, oneshot},
    time::{Instant, sleep_until, timeout_at},
};
use tokio_stream::Stream;

/// Only host-resolved moderation credentials enter this type. It cannot accept
/// client headers, platform provider credentials or proxies. Configured DNS
/// origins are resolved, checked and pinned by the isolated network policy.
pub(crate) struct HoneypotUpstream {
    protocol: UpstreamProtocol,
    model: String,
    endpoint: Url,
    credential: HeaderValue,
    network: Option<NetworkTarget>,
}
impl HoneypotUpstream {
    pub(crate) fn local(
        protocol: UpstreamProtocol,
        model: String,
        endpoint: &str,
        credential: &str,
        stream: bool,
    ) -> Result<Self, RelayError> {
        let endpoint = validate_base_url(endpoint, true)?;
        if !super::honeypot_network::literal_ip(&endpoint).is_some_and(|ip| ip.is_loopback()) {
            return Err(RelayError::Invalid("invalid local upstream"));
        }
        Self::encode(protocol, model, endpoint, credential, stream)
    }

    pub(crate) fn configured(
        protocol: UpstreamProtocol,
        model: String,
        endpoint: &str,
        credential: &str,
        stream: bool,
        network: NetworkTarget,
    ) -> Result<Self, RelayError> {
        let base = validate_base_url(endpoint, network.local_http)?;
        if !network.allowed_origins.contains(&canonical_origin(&base)) {
            return Err(RelayError::RouteUnavailable);
        }
        let mut upstream = Self::encode(protocol, model, base, credential, stream)?;
        upstream.network = Some(network);
        Ok(upstream)
    }

    fn encode(
        protocol: UpstreamProtocol,
        model: String,
        mut endpoint: Url,
        credential: &str,
        stream: bool,
    ) -> Result<Self, RelayError> {
        if model.is_empty()
            || model.len() > 256
            || model.chars().any(char::is_control)
            || credential.is_empty()
            || credential.len() > 4096
        {
            return Err(RelayError::Invalid("invalid moderation upstream"));
        }
        {
            let mut segments = endpoint
                .path_segments_mut()
                .map_err(|_| RelayError::Invalid("invalid moderation upstream"))?;
            segments.pop_if_empty();
            match protocol {
                UpstreamProtocol::Chat => {
                    segments.push("chat").push("completions");
                }
                UpstreamProtocol::Responses => {
                    segments.push("responses");
                }
                UpstreamProtocol::Anthropic => {
                    segments.push("messages");
                }
                UpstreamProtocol::Gemini => {
                    segments.push("models").push(&format!(
                        "{model}:{}",
                        if stream {
                            "streamGenerateContent"
                        } else {
                            "generateContent"
                        }
                    ));
                }
            }
        }
        if protocol == UpstreamProtocol::Gemini && stream {
            endpoint.set_query(Some("alt=sse"));
        }
        let auth = if matches!(
            protocol,
            UpstreamProtocol::Chat | UpstreamProtocol::Responses
        ) {
            format!("Bearer {credential}")
        } else {
            credential.to_owned()
        };
        let mut credential = HeaderValue::from_str(&auth)
            .map_err(|_| RelayError::Invalid("invalid moderation credential"))?;
        credential.set_sensitive(true);
        Ok(Self {
            protocol,
            model,
            endpoint,
            credential,
            network: None,
        })
    }
}

pub(crate) enum HoneypotTarget {
    FixedText(String),
    Upstream(HoneypotUpstream),
}

#[derive(Clone)]
pub(crate) struct HoneypotTransport {
    client: reqwest::Client,
    permits: Arc<Semaphore>,
    ceiling: Limits,
    registry: Arc<Mutex<Registry>>,
    changed: Arc<Notify>,
    dns_slots: Arc<Semaphore>,
}

/// Both the HTTP body and the producer retain Core's lease. Dropping a body
/// cancels the worker; the lease is released only after both owners disappear.
pub(crate) struct HoneypotBody {
    body: RelayBody,
    _admission: Arc<dyn Send + Sync>,
}
impl Stream for HoneypotBody {
    type Item = Result<Bytes, RelayError>;
    fn poll_next(
        self: std::pin::Pin<&mut Self>,
        cx: &mut std::task::Context<'_>,
    ) -> std::task::Poll<Option<Self::Item>> {
        std::pin::Pin::new(&mut self.get_mut().body).poll_next(cx)
    }
}

pub(crate) struct HoneypotSession {
    pub(crate) head: oneshot::Receiver<Result<ResponseHead, RelayError>>,
    pub(crate) body: HoneypotBody,
    pub(crate) completion: oneshot::Receiver<Result<(), RelayError>>,
}

impl HoneypotTransport {
    pub(crate) fn new(ceiling: Limits) -> Result<Self, RelayError> {
        ceiling.validate()?;
        if ceiling.connect_retries != 0 {
            return Err(RelayError::Invalid("moderation retries are disabled"));
        }
        let client = client_builder(&ceiling)
            .build()
            .map_err(|_| RelayError::Invalid("HTTP client initialization failed"))?;
        let dns_slots = super::honeypot_network::dns_slots(ceiling.max_in_flight);
        Ok(Self {
            client,
            dns_slots,
            permits: Arc::new(Semaphore::new(ceiling.max_in_flight)),
            ceiling,
            registry: Arc::new(Mutex::new(Registry {
                next: 0,
                draining: false,
                active: Default::default(),
            })),
            changed: Arc::new(Notify::new()),
        })
    }

    pub(crate) fn start(
        &self,
        mut request: Request,
        request_id: &str,
        target: HoneypotTarget,
        limits: Limits,
        output_bytes_per_second: u32,
        admission: Arc<dyn Send + Sync>,
    ) -> Result<HoneypotSession, RelayError> {
        limits.validate()?;
        if output_bytes_per_second > 16 * 1024 * 1024
            || request_id.is_empty()
            || request_id.len() > 128
            || !request_id
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b"-_".contains(&b))
            || limits.connect_retries != 0
            || limits.max_in_flight > self.ceiling.max_in_flight
            || limits.queue_chunks > self.ceiling.queue_chunks
            || limits.chunk_bytes > self.ceiling.chunk_bytes
            || limits.request_bytes > self.ceiling.request_bytes
            || limits.output_bytes > self.ceiling.output_bytes
            || limits.upstream_bytes > self.ceiling.upstream_bytes
            || limits.transport_chunk_bytes > self.ceiling.transport_chunk_bytes
            || limits.event_bytes > self.ceiling.event_bytes
            || limits.max_blocks > self.ceiling.max_blocks
            || limits.tool_argument_bytes > self.ceiling.tool_argument_bytes
            || limits.max_output_tokens > self.ceiling.max_output_tokens
            || limits.total_timeout > self.ceiling.total_timeout
            || limits.connect_timeout > self.ceiling.connect_timeout
            || limits.header_timeout > self.ceiling.header_timeout
            || limits.read_timeout > self.ceiling.read_timeout
            || limits.write_timeout > self.ceiling.write_timeout
        {
            return Err(RelayError::Invalid("invalid moderation transport limits"));
        }
        request.max_output_tokens = request.max_output_tokens.min(limits.max_output_tokens);
        if let HoneypotTarget::FixedText(text) = &target
            && text.len() > limits.output_bytes
        {
            return Err(RelayError::Limit("fixed output"));
        }
        let permit = self
            .permits
            .clone()
            .try_acquire_owned()
            .map_err(|e| match e {
                tokio::sync::TryAcquireError::Closed => RelayError::Draining,
                tokio::sync::TryAcquireError::NoPermits => RelayError::Busy,
            })?;
        let cancel = Cancellation::default();
        let id = {
            let mut registry = self.registry.lock().map_err(|_| RelayError::Draining)?;
            if registry.draining {
                return Err(RelayError::Draining);
            }
            let id = registry.next;
            registry.next = registry.next.checked_add(1).ok_or(RelayError::Busy)?;
            registry.active.insert(id, cancel.clone());
            id
        };
        let active = Active {
            registry: self.registry.clone(),
            changed: self.changed.clone(),
            id,
        };
        let (sender, receiver) = mpsc::channel(limits.queue_chunks);
        let (head_tx, head) = oneshot::channel();
        let (done, completion) = oneshot::channel();
        let body = HoneypotBody {
            body: RelayBody {
                receiver,
                cancel: cancel.clone(),
            },
            _admission: admission.clone(),
        };
        let worker = Worker {
            client: self.client.clone(),
            encoder: Encoder::new(&request, request_id, &limits),
            request,
            limits: limits.clone(),
            gate: Gate {
                cancel,
                deadline: Instant::now() + limits.total_timeout,
            },
            sender,
            head: Some(head_tx),
            emitted: 0,
            upstream_bytes: 0,
            pacing_started: Instant::now(),
            output_bytes_per_second,
            complete: false,
            dns_slots: self.dns_slots.clone(),
        };
        tokio::spawn(async move {
            let _permit = permit;
            let _admission = admission;
            let _active = active;
            let result = worker.run(target).await;
            let _ = done.send(result);
        });
        Ok(HoneypotSession {
            head,
            body,
            completion,
        })
    }

    async fn wait_empty(&self, until: Instant) -> bool {
        loop {
            let changed = self.changed.notified();
            tokio::pin!(changed);
            changed.as_mut().enable();
            if self
                .registry
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner)
                .active
                .is_empty()
            {
                return true;
            }
            if timeout_at(until, changed).await.is_err() {
                return self
                    .registry
                    .lock()
                    .unwrap_or_else(std::sync::PoisonError::into_inner)
                    .active
                    .is_empty();
            }
        }
    }

    pub(crate) async fn drain(&self, grace: Duration) -> DrainReport {
        {
            let mut registry = self
                .registry
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner);
            registry.draining = true;
            self.permits.close();
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
                .registry
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner);
            for cancel in registry.active.values() {
                cancel.cancel();
            }
            registry.active.len()
        };
        self.wait_empty(Instant::now() + Duration::from_secs(1))
            .await;
        let remaining = self
            .registry
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .active
            .len();
        DrainReport {
            cancelled,
            remaining,
        }
    }
}

struct Active {
    registry: Arc<Mutex<Registry>>,
    changed: Arc<Notify>,
    id: u64,
}
impl Drop for Active {
    fn drop(&mut self) {
        self.registry
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .active
            .remove(&self.id);
        self.changed.notify_waiters();
    }
}

struct Worker {
    client: reqwest::Client,
    request: Request,
    encoder: Encoder,
    limits: Limits,
    gate: Gate,
    sender: mpsc::Sender<Result<Bytes, RelayError>>,
    head: Option<oneshot::Sender<Result<ResponseHead, RelayError>>>,
    emitted: usize,
    upstream_bytes: usize,
    pacing_started: Instant,
    output_bytes_per_second: u32,
    complete: bool,
    dns_slots: Arc<Semaphore>,
}
impl Worker {
    fn head(&mut self) -> Result<(), RelayError> {
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

    async fn write(&mut self, packet: &[u8]) -> Result<(), RelayError> {
        let end = self
            .emitted
            .checked_add(packet.len())
            .ok_or(RelayError::Limit("encoded output"))?;
        if end > self.limits.output_bytes {
            return Err(RelayError::Limit("encoded output"));
        }
        // Count actual encoded bytes, including framing and repeated terminal
        // fields. Pacing has no initial unbounded burst and applies to JSON too.
        for chunk in packet.chunks(self.limits.chunk_bytes) {
            let next = self.emitted + chunk.len();
            if self.output_bytes_per_second != 0 {
                let nanos = (next as u128 * 1_000_000_000)
                    .div_ceil(u128::from(self.output_bytes_per_second));
                let at = self.pacing_started
                    + Duration::from_nanos(
                        u64::try_from(nanos).map_err(|_| RelayError::Limit("output pacing"))?,
                    );
                self.gate
                    .run(self.limits.total_timeout, Phase::Write, async {
                        sleep_until(at).await;
                        Ok(())
                    })
                    .await?;
            }
            self.gate
                .run(self.limits.write_timeout, Phase::Write, async {
                    self.sender
                        .send(Ok(Bytes::copy_from_slice(chunk)))
                        .await
                        .map_err(|_| RelayError::Cancelled)
                })
                .await?;
            self.emitted = next;
        }
        Ok(())
    }

    async fn events(&mut self, events: Vec<Event>) -> Result<(), RelayError> {
        let terminal = events.iter().any(|event| matches!(event, Event::Done));
        let mut packets = Vec::new();
        for event in events {
            if let Event::Usage(usage) = &event
                && usage
                    .output_tokens
                    .is_some_and(|n| n > u64::from(self.request.max_output_tokens))
            {
                return Err(RelayError::Limit("upstream output tokens"));
            }
            packets.extend(self.encoder.consume(event)?);
        }
        if terminal {
            packets.extend(self.encoder.terminal()?);
            self.complete = true;
        }
        for packet in packets {
            self.write(&packet).await?;
        }
        Ok(())
    }

    async fn fixed(&mut self, mut text: String) -> Result<(), RelayError> {
        // Usage is deliberately unknown/null. Synthetic text is not a provider
        // usage or money receipt, and has no connection to ordinary billing.
        // A conservative UTF-8 byte ceiling avoids inventing a tokenizer or
        // synthetic token count. A small requested output budget truncates the
        // configured text with the protocol's genuine length finish state.
        let mut end = text.len().min(self.request.max_output_tokens as usize);
        while !text.is_char_boundary(end) {
            end -= 1;
        }
        let limited = end != text.len();
        text.truncate(end);
        let mut events = vec![
            Event::Start(String::new()),
            Event::Block {
                index: 0,
                kind: BlockKind::Text,
            },
        ];
        // UTF-8 chunks retain code-point boundaries and keep individual frames
        // bounded. The encoder owns protocol IDs and terminal sequences.
        let mut remaining = text.as_str();
        while !remaining.is_empty() {
            let mut end = remaining.len().min(256);
            while !remaining.is_char_boundary(end) {
                end -= 1;
            }
            events.push(Event::Delta {
                index: 0,
                text: remaining[..end].to_owned(),
            });
            remaining = &remaining[end..];
        }
        events.extend([
            Event::EndBlock(0),
            Event::Finish(if limited {
                FinishReason::Length
            } else {
                FinishReason::Stop
            }),
            Event::Done,
        ]);
        // Validate all fixed packets and total framing bytes before HTTP 200.
        let mut packets = Vec::new();
        for event in events {
            packets.extend(self.encoder.consume(event)?);
        }
        packets.extend(self.encoder.terminal()?);
        let total = packets
            .iter()
            .try_fold(0usize, |total, p| total.checked_add(p.len()))
            .ok_or(RelayError::Limit("encoded output"))?;
        if total > self.limits.output_bytes {
            return Err(RelayError::Limit("encoded output"));
        }
        self.complete = true;
        self.head()?;
        for packet in packets {
            self.write(&packet).await?;
        }
        Ok(())
    }

    fn count_input(&mut self, chunk: &Bytes) -> Result<(), RelayError> {
        if chunk.len() > self.limits.transport_chunk_bytes {
            return Err(RelayError::Limit("transport chunk"));
        }
        self.upstream_bytes = self
            .upstream_bytes
            .checked_add(chunk.len())
            .ok_or(RelayError::Limit("upstream bytes"))?;
        if self.upstream_bytes > self.limits.upstream_bytes {
            return Err(RelayError::Limit("upstream bytes"));
        }
        Ok(())
    }

    async fn upstream(&mut self, route: HoneypotUpstream) -> Result<(), RelayError> {
        let prepared = if let Some(network) = &route.network {
            Some(
                self.gate
                    .run(
                        self.limits.connect_timeout,
                        Phase::Connect,
                        network.prepare(&route.endpoint, &self.limits, self.dns_slots.clone()),
                    )
                    .await?,
            )
        } else {
            None
        };
        let payload = self.request.upstream_to(route.protocol, &route.model)?;
        let bytes = serde_json::to_vec(&payload)
            .map_err(|_| RelayError::Invalid("request encoding failed"))?;
        if bytes.len() > self.limits.request_bytes {
            return Err(RelayError::Limit("encoded request"));
        }
        let client = prepared
            .as_ref()
            .map(|connection| &connection.client)
            .unwrap_or(&self.client);
        let builder = client
            .post(route.endpoint)
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
            .body(bytes);
        let builder = match route.protocol {
            UpstreamProtocol::Chat | UpstreamProtocol::Responses => {
                builder.header(header::AUTHORIZATION, route.credential)
            }
            UpstreamProtocol::Anthropic => builder
                .header("x-api-key", route.credential)
                .header("anthropic-version", "2023-06-01"),
            UpstreamProtocol::Gemini => builder.header("x-goog-api-key", route.credential),
        };
        // One attempt only. Requests have no caller headers or platform keys.
        let mut response = self
            .gate
            .run(self.limits.header_timeout, Phase::Headers, async {
                builder
                    .send()
                    .await
                    .map_err(|_| RelayError::UpstreamConnection)
            })
            .await?;
        if prepared
            .as_ref()
            .is_some_and(|connection| !connection.matches_peer(response.remote_addr()))
        {
            return Err(RelayError::UpstreamConnection);
        }
        if !response.status().is_success() {
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
            self.limits.upstream_bytes
        } else {
            self.limits.output_bytes
        };
        if response
            .content_length()
            .is_some_and(|n| n > max_body as u64)
        {
            return Err(RelayError::Limit("upstream body"));
        }
        let mut decoder = Decoder::new(route.protocol, &self.limits);
        if self.request.stream {
            self.head()?;
            let mut parser = sse::Parser::new(self.limits.event_bytes);
            'read: loop {
                let next = self
                    .gate
                    .run(self.limits.read_timeout, Phase::Read, async {
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
                    .run(self.limits.read_timeout, Phase::Read, async {
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
            // Buffer non-stream bytes until protocol validation is complete;
            // reserve/finalize do not exist on this uncharged path.
            let events = decoder.json(&value)?;
            let mut packets = Vec::new();
            for event in events {
                if let Event::Usage(usage) = &event
                    && usage
                        .output_tokens
                        .is_some_and(|n| n > u64::from(self.request.max_output_tokens))
                {
                    return Err(RelayError::Limit("upstream output tokens"));
                }
                packets.extend(self.encoder.consume(event)?);
            }
            packets.extend(self.encoder.terminal()?);
            if packets
                .iter()
                .try_fold(0usize, |total, p| total.checked_add(p.len()))
                .is_none_or(|total| total > self.limits.output_bytes)
            {
                return Err(RelayError::Limit("encoded output"));
            }
            self.complete = true;
            self.head()?;
            for packet in packets {
                self.write(&packet).await?;
            }
        }
        drop(response);
        if !self.complete {
            return Err(RelayError::Truncated);
        }
        Ok(())
    }

    async fn run(mut self, target: HoneypotTarget) -> Result<(), RelayError> {
        let result = match target {
            HoneypotTarget::FixedText(text) => self.fixed(text).await,
            HoneypotTarget::Upstream(route) => self.upstream(route).await,
        };
        if let Err(error) = &result {
            if let Some(head) = self.head.take() {
                let _ = head.send(Err(error.clone()));
            } else if !self.sender.is_closed()
                && !self.gate.cancel.is_cancelled()
                && Instant::now() < self.gate.deadline
            {
                // Protocol errors contain no upstream body/URL/credential.
                // A terminal failure has no [DONE]/response.completed marker.
                if self.request.stream {
                    if let Ok(packet) = self.encoder.error_packet(error) {
                        // Control frames have the same byte budget, pacing,
                        // cancellation and total deadline as every data packet.
                        // If none fits, close without manufacturing output.
                        let _ = self.write(&packet).await;
                    }
                } else {
                    // This aborts the body; it is not an unpaced wire payload.
                    let _ = self
                        .gate
                        .run(self.limits.write_timeout, Phase::Write, async {
                            self.sender
                                .send(Err(error.clone()))
                                .await
                                .map_err(|_| RelayError::Cancelled)
                        })
                        .await;
                }
            }
        }
        result
    }
}

#[cfg(test)]
#[path = "honeypot_tests.rs"]
mod tests;
