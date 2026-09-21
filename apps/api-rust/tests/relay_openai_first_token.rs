use std::{
    convert::Infallible,
    sync::Arc,
    time::{Duration, Instant},
};

use async_trait::async_trait;
use axum::{
    Router,
    body::{Body, Bytes, to_bytes},
    extract::Request,
    http::{StatusCode, header},
    response::IntoResponse,
    routing::post,
};
use futures_util::{StreamExt, stream};
use lmm_api_rs::routes::relay_openai::{
    OpenAiRelayAuthorization, OpenAiRelayFailure, OpenAiRelayHttpState, OpenAiRelayRequest,
    OpenAiRelayResult, OpenAiRelayService, OpenAiUpstreamClient, OpenAiUpstreamTarget,
    openai_relay_router,
};
use tokio::{
    net::TcpListener,
    sync::{Mutex, oneshot},
    task::JoinHandle,
};
use tower::ServiceExt;

const FIRST: &[u8] = b"data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n";
const DONE: &[u8] = b"data: [DONE]\n\n";

struct ForwardRelay {
    client: OpenAiUpstreamClient,
    target: OpenAiUpstreamTarget,
}

#[async_trait]
impl OpenAiRelayService for ForwardRelay {
    async fn authenticate(&self, _: OpenAiRelayAuthorization) -> Result<(), OpenAiRelayFailure> {
        Ok(())
    }

    async fn relay(
        &self,
        request: OpenAiRelayRequest,
    ) -> Result<OpenAiRelayResult, OpenAiRelayFailure> {
        self.client.forward(&self.target, &request).await
    }
}

// Abort the loopback server even if a test assertion fails.
struct Server(JoinHandle<()>);

impl Drop for Server {
    fn drop(&mut self) {
        self.0.abort();
    }
}

async fn relay_to(upstream: Router) -> (Router, Server) {
    relay_with_deadlines(upstream, Duration::from_secs(5), Duration::from_secs(5)).await
}

async fn relay_with_deadlines(
    upstream: Router,
    headers: Duration,
    idle: Duration,
) -> (Router, Server) {
    let listener = TcpListener::bind("127.0.0.1:0").await.expect("listen");
    let address = listener.local_addr().expect("address");
    let server = Server(tokio::spawn(async move {
        axum::serve(listener, upstream).await.expect("serve");
    }));
    let service = Arc::new(ForwardRelay {
        client: OpenAiUpstreamClient::new(
            lmm_api_rs::relay_http::RelayHttpClient::new(
                lmm_api_rs::relay_http::RelayTimeoutConfig {
                    response_headers: Some(headers),
                    idle,
                    total: None,
                },
            )
            .expect("relay client"),
        ),
        target: OpenAiUpstreamTarget {
            base_url: format!("http://{address}"),
            api_key: "local-test-only".to_owned(),
        },
    });
    (
        openai_relay_router(OpenAiRelayHttpState::new(service, "test")),
        server,
    )
}

fn request(body: Bytes) -> Request {
    Request::builder()
        .method("POST")
        .uri("/v1/chat/completions")
        .header(header::CONTENT_TYPE, "application/json")
        .body(Body::from(body))
        .expect("request")
}

fn streaming_request() -> Request {
    request(Bytes::from_static(br#"{"model":"gpt-4o","stream":true}"#))
}

#[tokio::test]
async fn three_second_headers_do_not_use_dependency_or_body_idle_timeout() {
    for headers in [Duration::from_secs(1800), Duration::from_millis(100)] {
        let upstream = Router::new().route(
            "/v1/chat/completions",
            post(|| async {
                tokio::time::sleep(Duration::from_secs(3)).await;
                (
                    [(header::CONTENT_TYPE, "text/event-stream")],
                    [FIRST, DONE].concat(),
                )
            }),
        );
        let (relay, _server) =
            relay_with_deadlines(upstream, headers, Duration::from_secs(2)).await;
        let response =
            tokio::time::timeout(Duration::from_secs(15), relay.oneshot(streaming_request()))
                .await
                .expect("outer deadline")
                .expect("response");
        if headers == Duration::from_secs(1800) {
            assert_eq!(response.status(), StatusCode::OK);
            assert_eq!(
                to_bytes(response.into_body(), 4096).await.unwrap().as_ref(),
                [FIRST, DONE].concat()
            );
        } else {
            assert!(!response.status().is_success());
            let body = to_bytes(response.into_body(), 4096).await.unwrap();
            assert!(String::from_utf8_lossy(&body).contains("upstream response timed out"));
        }
    }
}

#[tokio::test]
async fn stalled_error_body_preserves_upstream_status_and_safe_fallback() {
    let upstream = Router::new().route(
        "/v1/chat/completions",
        post(|| async {
            let incomplete =
                stream::once(async { Ok::<_, Infallible>(Bytes::from_static(b"{\"error\":")) });
            (
                StatusCode::TOO_MANY_REQUESTS,
                [(header::RETRY_AFTER, "7")],
                Body::from_stream(incomplete.chain(stream::pending())),
            )
        }),
    );
    let (relay, _server) =
        relay_with_deadlines(upstream, Duration::from_secs(2), Duration::from_millis(100)).await;
    let response = tokio::time::timeout(Duration::from_secs(5), relay.oneshot(streaming_request()))
        .await
        .expect("error body deadline")
        .expect("response");
    assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
    assert_eq!(response.headers()[header::RETRY_AFTER], "7");
    let request_id = response.headers()["x-oneapi-request-id"]
        .to_str()
        .unwrap()
        .to_owned();
    let body = to_bytes(response.into_body(), 4096).await.unwrap();
    let error: serde_json::Value = serde_json::from_slice(&body).unwrap();
    assert_eq!(
        error["error"]["message"],
        format!("upstream returned an error (request id: {request_id})")
    );
    assert_eq!(error["error"]["code"], "upstream_error");
}

#[tokio::test]
async fn relay_rejects_missing_response_headers() {
    let upstream = Router::new().route(
        "/v1/chat/completions",
        post(|| async {
            std::future::pending::<()>().await;
            "unreachable"
        }),
    );
    let (relay, _server) =
        relay_with_deadlines(upstream, Duration::from_millis(100), Duration::from_secs(5)).await;
    let response = tokio::time::timeout(Duration::from_secs(2), relay.oneshot(streaming_request()))
        .await
        .expect("header deadline")
        .expect("response");
    assert!(!response.status().is_success());
    let body = to_bytes(response.into_body(), 4096)
        .await
        .expect("error body");
    assert!(String::from_utf8_lossy(&body).contains("upstream response timed out"));
}

#[tokio::test]
async fn relay_bounds_body_stalls_after_headers() {
    let upstream = Router::new().route(
        "/v1/chat/completions",
        post(|| async {
            let first = stream::once(async { Ok::<_, Infallible>(Bytes::from_static(FIRST)) });
            (
                [(header::CONTENT_TYPE, "text/event-stream")],
                Body::from_stream(first.chain(stream::pending())),
            )
        }),
    );
    let (relay, _server) =
        relay_with_deadlines(upstream, Duration::from_secs(2), Duration::from_millis(100)).await;
    let response = relay.oneshot(streaming_request()).await.expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    let result = tokio::time::timeout(Duration::from_secs(2), to_bytes(response.into_body(), 4096))
        .await
        .expect("stalled body must fail within the idle deadline");
    assert!(result.is_err(), "do not turn a truncated SSE into success");
}

#[tokio::test]
async fn relay_resets_idle_deadline_for_progressing_stream() {
    let upstream = Router::new().route(
        "/v1/chat/completions",
        post(|| async {
            let chunks = stream::unfold(0, |index| async move {
                if index == 9 {
                    return None;
                }
                if index > 0 {
                    tokio::time::sleep(Duration::from_millis(100)).await;
                }
                Some((
                    Ok::<_, Infallible>(Bytes::from_static(if index == 8 { DONE } else { FIRST })),
                    index + 1,
                ))
            });
            (
                [(header::CONTENT_TYPE, "text/event-stream")],
                Body::from_stream(chunks),
            )
        }),
    );
    let idle = Duration::from_millis(400);
    let (relay, _server) = relay_with_deadlines(upstream, Duration::from_millis(300), idle).await;
    let started = Instant::now();
    let response = relay.oneshot(streaming_request()).await.expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    let body = tokio::time::timeout(Duration::from_secs(5), to_bytes(response.into_body(), 4096))
        .await
        .expect("outer deadline")
        .expect("progressing stream");
    assert!(started.elapsed() > idle);
    assert_eq!(body.as_ref(), [FIRST.repeat(8), DONE.to_vec()].concat());
}

struct SignalDrop(Option<oneshot::Sender<()>>);
impl Drop for SignalDrop {
    fn drop(&mut self) {
        if let Some(sender) = self.0.take() {
            let _ = sender.send(());
        }
    }
}

#[tokio::test]
async fn dropping_downstream_releases_upstream_body() {
    let (dropped, wait) = oneshot::channel();
    let signal = Arc::new(Mutex::new(Some(dropped)));
    let upstream = Router::new().route(
        "/v1/chat/completions",
        post(move || {
            let signal = signal.clone();
            async move {
                let guard = SignalDrop(signal.lock().await.take());
                let chunks = stream::unfold((false, guard), |(sent, guard)| async move {
                    if sent {
                        std::future::pending::<()>().await;
                    }
                    Some((
                        Ok::<_, Infallible>(Bytes::from_static(FIRST)),
                        (true, guard),
                    ))
                });
                (
                    [(header::CONTENT_TYPE, "text/event-stream")],
                    Body::from_stream(chunks),
                )
            }
        }),
    );
    let (relay, _server) =
        relay_with_deadlines(upstream, Duration::from_secs(5), Duration::from_secs(30)).await;
    let response = relay.oneshot(streaming_request()).await.expect("response");
    let mut chunks = response.into_body().into_data_stream();
    assert_eq!(
        chunks.next().await.expect("chunk").expect("data").as_ref(),
        FIRST
    );
    drop(chunks);
    tokio::time::timeout(Duration::from_secs(3), wait)
        .await
        .expect("release before idle timeout")
        .expect("upstream dropped");
}

#[tokio::test]
async fn native_relay_delivers_content_before_upstream_finishes() {
    let payload = Bytes::from_static(
        br#"{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}"#,
    );
    let (finish, wait) = oneshot::channel();
    let wait = Arc::new(Mutex::new(Some(wait)));
    let expected = payload.clone();
    let upstream = Router::new().route(
        "/v1/chat/completions",
        post(move |request: Request| {
            let wait = wait.clone();
            let expected = expected.clone();
            async move {
                assert_eq!(
                    to_bytes(request.into_body(), 1024)
                        .await
                        .expect("request body"),
                    expected
                );
                let wait = wait.lock().await.take().expect("one upstream request");
                let first = stream::once(async { Ok::<_, Infallible>(Bytes::from_static(FIRST)) });
                let tail = stream::once(async move {
                    wait.await.expect("release upstream tail");
                    Ok::<_, Infallible>(Bytes::from_static(DONE))
                });
                (
                    [(header::CONTENT_TYPE, "text/event-stream")],
                    Body::from_stream(first.chain(tail)),
                )
                    .into_response()
            }
        }),
    );
    let (relay, _server) = relay_to(upstream).await;
    let mut chunks = tokio::time::timeout(Duration::from_secs(5), async {
        let response = relay
            .oneshot(request(payload))
            .await
            .expect("relay response");
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(response.headers()["x-accel-buffering"], "no");
        let mut chunks = response.into_body().into_data_stream();
        let mut received = Vec::new();
        while received.len() < FIRST.len() {
            received.extend_from_slice(&chunks.next().await.expect("first content").expect("body"));
        }
        assert_eq!(received, FIRST);
        chunks
    })
    .await
    .expect("first content must arrive while upstream is still held open");

    finish.send(()).expect("upstream still waiting");
    let tail = tokio::time::timeout(Duration::from_secs(5), async {
        let mut tail = Vec::new();
        while let Some(chunk) = chunks.next().await {
            tail.extend_from_slice(&chunk.expect("tail"));
        }
        tail
    })
    .await
    .expect("stream completes");
    assert_eq!(tail, DONE);
}

/// Local gateway timing only: no database, remote model, or incoming network upload.
/// Run with `cargo test --release --test relay_openai_first_token -- --ignored --nocapture`.
#[tokio::test]
#[ignore = "manual first-content latency measurement; not a timing-sensitive CI gate"]
async fn measure_native_relay_first_content() {
    let upstream = Router::new().route(
        "/v1/chat/completions",
        post(|request: Request| async move {
            let _body = to_bytes(request.into_body(), 64 * 1024 * 1024)
                .await
                .expect("request body");
            (
                [(header::CONTENT_TYPE, "text/event-stream")],
                Body::from_stream(stream::iter([
                    Ok::<_, Infallible>(Bytes::from_static(FIRST)),
                    Ok(Bytes::from_static(DONE)),
                ])),
            )
        }),
    );
    let (relay, _server) = relay_to(upstream).await;
    for size in [1024, 1024 * 1024, 16 * 1024 * 1024] {
        let payload = Bytes::from(
            serde_json::to_vec(&serde_json::json!({
                "model": "gpt-4o", "stream": true,
                "messages": [{"role": "user", "content": "x".repeat(size)}],
            }))
            .expect("payload"),
        );
        let mut samples = Vec::new();
        for iteration in 0..45 {
            let input = request(payload.clone());
            let started = Instant::now();
            let response = relay.clone().oneshot(input).await.expect("response");
            assert_eq!(response.status(), StatusCode::OK);
            let mut chunks = response.into_body().into_data_stream();
            let mut received = Vec::new();
            while received.len() < FIRST.len() {
                received.extend_from_slice(&chunks.next().await.expect("content").expect("body"));
            }
            let elapsed = started.elapsed();
            assert!(received.starts_with(FIRST));
            // Drain after timing so the next request can reuse the upstream connection.
            while let Some(chunk) = chunks.next().await {
                received.extend_from_slice(&chunk.expect("tail"));
            }
            assert_eq!(received, [FIRST, DONE].concat());
            if iteration >= 5 {
                samples.push(elapsed);
            }
        }
        samples.sort_unstable();
        println!(
            "body_bytes={} samples={} first_content_p50_us={} first_content_p95_us={}",
            payload.len(),
            samples.len(),
            samples[20].as_micros(),
            samples[37].as_micros()
        );
    }
}
