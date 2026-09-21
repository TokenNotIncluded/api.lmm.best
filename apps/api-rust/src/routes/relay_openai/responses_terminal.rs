//! Native Responses framing: retain at most one bounded event, never a whole
//! response. Complete frames keep their original bytes and unknown fields.
//! Holding an unfinished frame prevents truncated JSON from corrupting the
//! synthetic failure event. Dropping the downstream body drops upstream I/O.

use crate::{
    conversion_observability::{
        ClientAbortGuard, ConversionObserver, FailureReason, MetricLabels, QueueDepthGuard,
        StreamTiming,
    },
    routes::sse::{DEFAULT_MAX_FRAME_BYTES, SseFrame, SseFrameParser},
};
use axum::body::{Body, Bytes};
use futures_util::{StreamExt, stream::BoxStream};
use serde_json::{Value, json};
use std::collections::VecDeque;

pub(super) fn observe(body: Body, observer: ConversionObserver, labels: MetricLabels) -> Body {
    observe_with_limit(body, observer, labels, DEFAULT_MAX_FRAME_BYTES)
}

fn observe_with_limit(
    body: Body,
    observer: ConversionObserver,
    labels: MetricLabels,
    limit: usize,
) -> Body {
    let state = State {
        upstream: Some(body.into_data_stream().boxed()),
        pending: Bytes::new(),
        frames: VecDeque::new(),
        parser: SseFrameParser::new(limit),
        observer: observer.clone(),
        labels,
        abort: ClientAbortGuard::new(observer.clone(), labels),
        queue: observer.enter_queue(labels),
        timing: StreamTiming::default(),
        response_id: None,
        sequence: None,
        ended: false,
        finished: false,
    };
    Body::from_stream(futures_util::stream::unfold(
        state,
        |mut state| async move {
            state
                .next()
                .await
                .map(|bytes| (Ok::<_, std::io::Error>(bytes), state))
        },
    ))
}

struct State {
    upstream: Option<BoxStream<'static, Result<Bytes, axum::Error>>>,
    pending: Bytes,
    frames: VecDeque<SseFrame>,
    parser: SseFrameParser,
    observer: ConversionObserver,
    labels: MetricLabels,
    abort: ClientAbortGuard,
    queue: QueueDepthGuard,
    timing: StreamTiming,
    response_id: Option<String>,
    sequence: Option<u64>,
    ended: bool,
    finished: bool,
}

impl State {
    fn complete(&mut self) {
        self.finished = true;
        self.upstream = None;
        self.pending = Bytes::new();
        self.frames.clear();
        self.abort.complete();
        self.queue.complete();
    }

    fn record_output(&mut self, bytes: &Bytes) {
        self.timing.mark_upstream_event();
        if self.timing.first_downstream_write_at.is_none() {
            self.timing.mark_downstream_write();
            self.timing.record_gateway_ttft(&self.observer, self.labels);
        }
        self.observer.record_output_bytes(self.labels, bytes.len());
    }

    fn interrupted(&mut self) -> Bytes {
        self.observer
            .record_failure_with_reason(self.labels, FailureReason::Stream);
        let mut event = json!({"type":"response.failed","response":{
            "object":"response","status":"failed","output":[],
            "error":{"code":"upstream_stream_interrupted","message":"Upstream response ended before a terminal event"}
        }});
        if let Some(id) = &self.response_id {
            event["response"]["id"] = json!(id);
        }
        if let Some(sequence) = self.sequence.and_then(|value| value.checked_add(1)) {
            event["sequence_number"] = json!(sequence);
        }
        self.complete();
        let bytes = Bytes::from(format!("event: response.failed\ndata: {event}\n\n"));
        self.record_output(&bytes);
        bytes
    }

    fn inspect(&mut self, frame: &SseFrame) -> bool {
        let Ok(value) = serde_json::from_str::<Value>(&frame.data) else {
            return false;
        };
        if let Some(id) = value.pointer("/response/id").and_then(Value::as_str)
            && id.len() <= 256
            && !id.is_empty()
            && id
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || b"_-".contains(&byte))
        {
            self.response_id = Some(id.to_owned());
        }
        if let Some(sequence) = value.get("sequence_number").and_then(Value::as_u64) {
            self.sequence = Some(
                self.sequence
                    .map_or(sequence, |previous| previous.max(sequence)),
            );
        }
        let kind = value
            .get("type")
            .and_then(Value::as_str)
            .unwrap_or_default();
        let terminal = matches!(
            kind,
            "response.completed"
                | "response.done"
                | "response.incomplete"
                | "response.failed"
                | "response.cancelled"
                | "response.canceled"
        );
        if terminal
            && (matches!(
                kind,
                "response.incomplete"
                    | "response.failed"
                    | "response.cancelled"
                    | "response.canceled"
            ) || value
                .pointer("/response/status")
                .and_then(Value::as_str)
                .is_some_and(|status| {
                    matches!(status, "failed" | "incomplete" | "cancelled" | "canceled")
                }))
        {
            self.observer
                .record_failure_with_reason(self.labels, FailureReason::Stream);
        }
        terminal
    }

    async fn next(&mut self) -> Option<Bytes> {
        if self.finished {
            return None;
        }
        loop {
            if let Some(frame) = self.frames.pop_front() {
                let terminal = self.inspect(&frame);
                let bytes = Bytes::from(frame.raw);
                if terminal {
                    self.complete();
                }
                self.record_output(&bytes);
                return Some(bytes);
            }
            if !self.pending.is_empty() {
                // A later malformed frame in the same transport chunk cannot
                // discard an already completed terminal.
                let end = self
                    .pending
                    .iter()
                    .position(|byte| *byte == b'\n' || *byte == b'\r')
                    .map_or(self.pending.len(), |index| index + 1);
                let fragment = self.pending.split_to(end);
                match self.parser.feed(&fragment) {
                    Ok(frames) => self.frames.extend(frames),
                    Err(_) => return Some(self.interrupted()),
                }
                continue;
            }
            if self.ended {
                return Some(self.interrupted());
            }
            let item = match &mut self.upstream {
                Some(upstream) => upstream.next().await,
                None => None,
            };
            match item {
                Some(Ok(bytes)) => self.pending = bytes,
                Some(Err(_)) | None => {
                    self.ended = true;
                    match self.parser.finish() {
                        Ok(frames) => self.frames.extend(frames),
                        Err(_) => return Some(self.interrupted()),
                    }
                }
            }
        }
    }
}

#[cfg(test)]
mod tests;
