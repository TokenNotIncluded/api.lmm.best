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
    let listener = TcpListener::bind("127.0.0.1:0").await.expect("listen");
    let address = listener.local_addr().expect("address");
    let server = Server(tokio::spawn(async move {
        axum::serve(listener, upstream).await.expect("serve");
    }));
    let service = Arc::new(ForwardRelay {
        client: OpenAiUpstreamClient::new(
            reqwest::Client::builder()
                .no_proxy()
                .build()
                .expect("client"),
            Duration::from_secs(5),
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
