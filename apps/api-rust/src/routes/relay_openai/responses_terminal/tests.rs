use super::*;
use crate::conversion_observability::{ConversionResult, MetricKind};
use axum::body::to_bytes;
use futures_util::stream;
use lmm_contracts::relay::Protocol;
use std::{io, time::Duration};

const CREATED: &str = "data: {\"type\":\"response.created\",\"sequence_number\":7,\"response\":{\"id\":\"resp_fixture\"}}\n\n";
const COMPLETED: &str = "data: {\"type\":\"response.completed\",\"sequence_number\":8,\"response\":{\"status\":\"completed\"}}\n\n";
fn labels() -> MetricLabels {
    MetricLabels::native_raw(Protocol::OpenAiResponses, true, ConversionResult::Success)
}
fn metric(observer: &ConversionObserver, kind: MetricKind) -> u64 {
    observer
        .snapshot()
        .samples
        .iter()
        .filter(|sample| sample.metric == kind)
        .map(|sample| sample.value)
        .sum()
}
async fn collect(
    chunks: Vec<Result<Bytes, io::Error>>,
    limit: usize,
) -> (String, ConversionObserver) {
    let observer = ConversionObserver::default();
    let body = observe_with_limit(
        Body::from_stream(stream::iter(chunks)),
        observer.clone(),
        labels(),
        limit,
    );
    let result = tokio::time::timeout(Duration::from_secs(2), to_bytes(body, 32 * 1024 * 1024))
        .await
        .unwrap()
        .unwrap();
    (String::from_utf8(result.to_vec()).unwrap(), observer)
}
#[tokio::test]
async fn complete_terminals_preserve_wire_bytes_across_every_chunk_boundary() {
    for kind in [
        "completed",
        "done",
        "incomplete",
        "failed",
        "cancelled",
        "canceled",
    ] {
        for delimiter in ["\n", "\r\n", "\r"] {
            let text = format!("{CREATED}event: response.{kind}\ndata: {{\"type\":\"response.{kind}\",\"future\":true}}\n\n").replace('\n', delimiter);
            for size in [1, 2, 7, 4096] {
                let chunks = text
                    .as_bytes()
                    .chunks(size)
                    .map(|chunk| Ok(Bytes::copy_from_slice(chunk)))
                    .collect();
                let (actual, observer) = collect(chunks, DEFAULT_MAX_FRAME_BYTES).await;
                assert_eq!(
                    actual, text,
                    "{kind}, delimiter={delimiter:?}, chunk={size}"
                );
                assert_eq!(metric(&observer, MetricKind::StreamClientAbortTotal), 0);
                assert_eq!(metric(&observer, MetricKind::StreamQueueDepth), 0);
                assert_eq!(
                    metric(&observer, MetricKind::ConversionFailuresTotal),
                    u64::from(!matches!(kind, "completed" | "done"))
                );
            }
        }
    }
}
#[tokio::test]
async fn early_eof_and_private_read_errors_emit_exactly_one_redacted_failure() {
    for read_error in [false, true] {
        let mut chunks = vec![Ok(Bytes::from_static(CREATED.as_bytes()))];
        if read_error {
            chunks.push(Err(io::Error::other("private upstream credential")));
        }
        let (actual, observer) = collect(chunks, DEFAULT_MAX_FRAME_BYTES).await;
        assert!(actual.starts_with(CREATED));
        assert_eq!(actual.matches("event: response.failed").count(), 1);
        assert!(!actual.contains("private"));
        let failure: Value = serde_json::from_str(
            actual
                .lines()
                .rev()
                .find_map(|line| line.strip_prefix("data: "))
                .unwrap(),
        )
        .unwrap();
        assert_eq!(failure["response"]["id"], "resp_fixture");
        assert_eq!(failure["sequence_number"], 8);
        assert_eq!(
            failure["response"]["error"]["code"],
            "upstream_stream_interrupted"
        );
        assert_eq!(metric(&observer, MetricKind::ConversionFailuresTotal), 1);
        assert_eq!(metric(&observer, MetricKind::StreamClientAbortTotal), 0);
        assert!(
            observer
                .snapshot()
                .samples
                .iter()
                .any(|sample| sample.labels.failure_reason == Some(FailureReason::Stream))
        );
    }
}
#[tokio::test]
async fn interrupted_frame_is_not_forwarded_as_malformed_json_before_failure() {
    let text = format!(
        "{CREATED}data: {{\"type\":\"response.output_text.delta\",\"delta\":\"private unfinished"
    );
    let (actual, _) = collect(vec![Ok(Bytes::from(text))], DEFAULT_MAX_FRAME_BYTES).await;
    assert!(actual.starts_with(CREATED));
    assert!(!actual.contains("private unfinished"));
    for line in actual
        .lines()
        .filter_map(|line| line.strip_prefix("data: "))
    {
        assert!(serde_json::from_str::<Value>(line).is_ok());
    }
}
#[tokio::test]
async fn terminal_stops_upstream_before_late_error_or_duplicate_terminal() {
    let text = format!("{CREATED}{COMPLETED}");
    let chunks = vec![
        Ok(Bytes::from(format!("{text}{COMPLETED}"))),
        Err(io::Error::other("late transport failure")),
    ];
    let (actual, observer) = collect(chunks, DEFAULT_MAX_FRAME_BYTES).await;
    assert_eq!(actual, text);
    assert_eq!(metric(&observer, MetricKind::ConversionFailuresTotal), 0);
}
#[tokio::test]
async fn done_marker_and_empty_body_do_not_fake_a_responses_completion() {
    for text in ["", "data: [DONE]\n\n", ": keepalive\n\n"] {
        let (actual, observer) = collect(
            vec![Ok(Bytes::from_static(text.as_bytes()))],
            DEFAULT_MAX_FRAME_BYTES,
        )
        .await;
        assert_eq!(actual.matches("event: response.failed").count(), 1);
        assert_eq!(metric(&observer, MetricKind::ConversionFailuresTotal), 1);
    }
}
#[tokio::test]
async fn unknown_events_and_multiline_json_remain_unchanged() {
    let text = format!(
        "{CREATED}event: future.event\nid: provider-id\nretry: 100\ndata: {{\"type\":\"future.event\",\ndata: \"unknown\":\"中文\"}}\n\n{COMPLETED}"
    );
    let chunks = text
        .as_bytes()
        .chunks(1)
        .map(|chunk| Ok(Bytes::copy_from_slice(chunk)))
        .collect();
    let (actual, _) = collect(chunks, DEFAULT_MAX_FRAME_BYTES).await;
    assert_eq!(actual, text);
}
#[tokio::test]
async fn oversized_event_fails_closed_and_stops_reading() {
    let text = format!("{CREATED}data: {}\n\n{COMPLETED}", "x".repeat(4096));
    let (actual, observer) = collect(vec![Ok(Bytes::from(text))], 128).await;
    assert!(actual.starts_with(CREATED));
    assert!(!actual.contains("xxxx"));
    assert_eq!(actual.matches("event: response.failed").count(), 1);
    assert_eq!(metric(&observer, MetricKind::ConversionFailuresTotal), 1);
}
#[tokio::test]
async fn complete_frame_is_delivered_without_waiting_for_stream_eof() {
    let observer = ConversionObserver::default();
    let input = stream::once(async { Ok::<_, io::Error>(Bytes::from_static(CREATED.as_bytes())) })
        .chain(stream::pending());
    let mut output =
        observe(Body::from_stream(input), observer.clone(), labels()).into_data_stream();
    let first = tokio::time::timeout(Duration::from_millis(200), output.next())
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert_eq!(first.as_ref(), CREATED.as_bytes());
    drop(output);
    assert_eq!(metric(&observer, MetricKind::StreamClientAbortTotal), 1);
    assert_eq!(metric(&observer, MetricKind::StreamQueueDepth), 0);
    assert_eq!(metric(&observer, MetricKind::ConversionFailuresTotal), 0);
}
#[tokio::test]
async fn failed_status_on_completed_event_is_observed_as_failure_without_replay() {
    let text = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"failed\"}}\n\n";
    let (actual, observer) = collect(
        vec![Ok(Bytes::from_static(text.as_bytes()))],
        DEFAULT_MAX_FRAME_BYTES,
    )
    .await;
    assert_eq!(actual, text);
    assert_eq!(metric(&observer, MetricKind::ConversionFailuresTotal), 1);
}
#[tokio::test]
async fn unsafe_ids_are_not_copied_into_the_synthetic_event() {
    let text = "data: {\"type\":\"response.created\",\"sequence_number\":18446744073709551615,\"response\":{\"id\":\"secret\\nheader\"}}\n\n";
    let (actual, _) = collect(
        vec![Ok(Bytes::from_static(text.as_bytes()))],
        DEFAULT_MAX_FRAME_BYTES,
    )
    .await;
    let failure = actual.split("event: response.failed").nth(1).unwrap();
    assert!(!failure.contains("secret"));
    assert!(!failure.contains("sequence_number"));
}

#[tokio::test]
async fn terminal_cr_delimiter_survives_a_subsequent_transport_error() {
    for delimiter in ["\n", "\r\n", "\r"] {
        let text = format!("{CREATED}{COMPLETED}").replace('\n', delimiter);
        let chunks = vec![
            Ok(Bytes::from(text.clone())),
            Err(io::Error::other("late private upstream error")),
        ];
        let (actual, observer) = collect(chunks, DEFAULT_MAX_FRAME_BYTES).await;
        assert_eq!(actual, text);
        assert_eq!(metric(&observer, MetricKind::ConversionFailuresTotal), 0);
    }
}

struct HttpStub {
    body: std::sync::Mutex<Option<Body>>,
    calls: std::sync::atomic::AtomicUsize,
}

#[async_trait::async_trait]
impl super::super::OpenAiRelayService for HttpStub {
    async fn authenticate(
        &self,
        _: super::super::OpenAiRelayAuthorization,
    ) -> Result<(), super::super::OpenAiRelayFailure> {
        Ok(())
    }
    async fn relay(
        &self,
        _: super::super::OpenAiRelayRequest,
    ) -> Result<super::super::OpenAiRelayResult, super::super::OpenAiRelayFailure> {
        self.calls.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
        Ok(super::super::OpenAiRelayResult {
            status: axum::http::StatusCode::OK,
            headers: Default::default(),
            body: super::super::OpenAiRelayBody::Upstream {
                content_type: Some(axum::http::HeaderValue::from_static("text/event-stream")),
                body: self
                    .body
                    .lock()
                    .unwrap()
                    .take()
                    .expect("one upstream response, never replay"),
            },
        })
    }
}

#[tokio::test]
async fn native_responses_are_guarded_at_the_real_http_boundary() {
    use super::super::{OpenAiRelayHttpState, openai_relay_router};
    use std::sync::{
        Arc, Mutex,
        atomic::{AtomicUsize, Ordering},
    };
    for path in ["/v1/responses", "/v1/chat/completions"] {
        for completed in [false, true] {
            for read_error in [false, true] {
                // A malformed final frame is deliberately not complete.
                let suffix = if completed {
                    COMPLETED
                } else {
                    "data: {\"type\":\"unfinished-private"
                };
                let source = format!("{CREATED}{suffix}");
                let mut chunks = vec![Ok(Bytes::from(source.clone()))];
                if read_error {
                    chunks.push(Err(io::Error::other("private upstream body error")));
                }
                // Chat keeps the pre-existing raw passthrough error contract.
                if path == "/v1/chat/completions" && read_error {
                    continue;
                }
                let stub = Arc::new(HttpStub {
                    body: Mutex::new(Some(Body::from_stream(stream::iter(chunks)))),
                    calls: AtomicUsize::new(0),
                });
                let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
                let address = listener.local_addr().unwrap();
                let router =
                    openai_relay_router(OpenAiRelayHttpState::new(stub.clone(), "http-regression"));
                let server = tokio::spawn(async move {
                    axum::serve(listener, router).await.unwrap();
                });
                let client = reqwest::Client::builder()
                    .no_proxy()
                    .timeout(Duration::from_secs(3))
                    .build()
                    .unwrap();
                let response = client.post(format!("http://{address}{path}"))
                    .json(&json!({"model":"gpt-4o","input":"hello","messages":[{"role":"user","content":"hello"}],"stream":true}))
                    .send().await.unwrap();
                assert_eq!(response.status(), axum::http::StatusCode::OK);
                assert_eq!(
                    response.headers()["cache-control"],
                    "no-cache, no-transform"
                );
                assert_eq!(response.headers()["x-accel-buffering"], "no");
                assert!(response.headers().get("content-encoding").is_none());
                let actual = response
                    .text()
                    .await
                    .expect("upstream interruption must not break downstream HTTP framing");
                server.abort();
                assert_eq!(stub.calls.load(Ordering::SeqCst), 1);
                if path == "/v1/chat/completions" || completed {
                    assert_eq!(actual, source);
                } else {
                    assert!(actual.starts_with(CREATED));
                    assert_eq!(actual.matches("event: response.failed").count(), 1);
                    assert!(!actual.contains("unfinished-private"));
                    assert!(!actual.contains("private upstream"));
                    assert!(actual.contains("upstream_stream_interrupted"));
                }
            }
        }
    }
}
