//! Observe the commit boundary on two real HTTP listeners. The forwarding port
//! deliberately has no storage or retry policy; PostgreSQL selection/settlement
//! regressions remain in relay_openai_specific_channel_pg.

use std::{
    convert::Infallible,
    pin::Pin,
    sync::{
        Arc,
        atomic::{AtomicUsize, Ordering},
    },
    task::{Context, Poll},
    time::Duration,
};

use async_trait::async_trait;
use axum::{
    Router,
    body::{Body, Bytes},
    http::{StatusCode, header},
    response::IntoResponse,
    routing::post,
};
use futures_util::{Stream, StreamExt, stream};
use lmm_api_rs::{
    relay_http::{RelayHttpClient, RelayTimeoutConfig},
    routes::relay_openai::{
        OpenAiRelayAuthorization, OpenAiRelayFailure, OpenAiRelayHttpState, OpenAiRelayRequest,
        OpenAiRelayResult, OpenAiRelayService, OpenAiUpstreamClient, OpenAiUpstreamTarget,
        openai_relay_router,
    },
};
use tokio::{
    io::AsyncWriteExt,
    net::{TcpListener, TcpStream},
    sync::{Mutex, oneshot},
    task::JoinHandle,
};

const ROLE: &[u8] = b"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n";
const USAGE: &[u8] = b"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1}}\n\n";
const HEARTBEAT: &[u8] = b": upstream heartbeat\n\n";
const CONTENT: &[u8] = b"data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n";
const REASONING: &[u8] = b"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"}}]}\n\n";
const TOOL: &[u8] = b"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"lookup\"}}]}}]}\n\n";
const FUNCTION: &[u8] =
    b"data: {\"choices\":[{\"delta\":{\"function_call\":{\"arguments\":\"{}\"}}}]}\n\n";
const DONE: &[u8] = b"data: [DONE]\n\n";
const REQUEST: &str =
    r#"{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}"#;
const FIRST_OUTPUT: Duration = Duration::from_millis(500);
const OUTER_DEADLINE: Duration = Duration::from_secs(3);

struct Server(JoinHandle<()>);

impl Drop for Server {
    fn drop(&mut self) {
        self.0.abort();
    }
}

async fn serve(app: Router) -> (String, Server) {
    let listener = TcpListener::bind("127.0.0.1:0").await.expect("listen");
    let address = listener.local_addr().expect("address");
    let server = Server(tokio::spawn(async move {
        axum::serve(listener, app).await.expect("serve");
    }));
    (format!("http://{address}"), server)
}

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

async fn gateway(upstream: &str, timeout: Option<Duration>) -> (String, Server) {
    let client = RelayHttpClient::new(RelayTimeoutConfig {
        response_headers: Some(OUTER_DEADLINE),
        idle: Duration::from_secs(30),
        total: None,
    })
    .expect("relay client")
    .with_openai_first_output_timeout(timeout);
    let service = Arc::new(ForwardRelay {
        client: OpenAiUpstreamClient::new(client),
        target: OpenAiUpstreamTarget {
            base_url: upstream.to_owned(),
            api_key: "local-test-only".to_owned(),
        },
    });
    serve(openai_relay_router(OpenAiRelayHttpState::new(
        service, "test",
    )))
    .await
}

type Frames = Pin<Box<dyn Stream<Item = Result<Bytes, Infallible>> + Send>>;

struct TrackedStream {
    frames: Frames,
    first_poll: Option<oneshot::Sender<()>>,
    dropped: Option<oneshot::Sender<()>>,
}

impl Stream for TrackedStream {
    type Item = Result<Bytes, Infallible>;

    fn poll_next(mut self: Pin<&mut Self>, context: &mut Context<'_>) -> Poll<Option<Self::Item>> {
        if let Some(sender) = self.first_poll.take() {
            let _ = sender.send(());
        }
        self.frames.as_mut().poll_next(context)
    }
}

impl Drop for TrackedStream {
    fn drop(&mut self) {
        if let Some(sender) = self.dropped.take() {
            let _ = sender.send(());
        }
    }
}

struct Upstream {
    url: String,
    _server: Server,
    requests: Arc<AtomicUsize>,
    first_poll: oneshot::Receiver<()>,
    dropped: oneshot::Receiver<()>,
    visible: oneshot::Sender<()>,
    finish: oneshot::Sender<()>,
}

async fn upstream(prefix: Vec<u8>, visible: &'static [u8]) -> Upstream {
    let requests = Arc::new(AtomicUsize::new(0));
    let hits = requests.clone();
    let (first_poll, first_poll_wait) = oneshot::channel();
    let (dropped, dropped_wait) = oneshot::channel();
    let (visible_release, visible_wait) = oneshot::channel();
    let (finish, finish_wait) = oneshot::channel();
    let state = Arc::new(Mutex::new(Some((
        prefix,
        first_poll,
        dropped,
        visible_wait,
        finish_wait,
    ))));
    let app = Router::new().route(
        "/v1/chat/completions",
        post(move || {
            let state = state.clone();
            let hits = hits.clone();
            async move {
                hits.fetch_add(1, Ordering::SeqCst);
                let (prefix, first_poll, dropped, visible_wait, finish_wait) =
                    state.lock().await.take().expect("one upstream request");
                let prefix = stream::iter(
                    (!prefix.is_empty()).then(|| Ok::<_, Infallible>(Bytes::from(prefix))),
                );
                let output = stream::once(async move {
                    visible_wait.await.expect("release visible output");
                    Ok::<_, Infallible>(Bytes::from_static(visible))
                });
                let tail = stream::once(async move {
                    finish_wait.await.expect("release stream end");
                    Ok::<_, Infallible>(Bytes::from_static(DONE))
                });
                (
                    [
                        (header::CONTENT_TYPE.as_str(), "text/event-stream"),
                        ("x-codex-turn-state", "local-state"),
                    ],
                    Body::from_stream(TrackedStream {
                        frames: Box::pin(prefix.chain(output).chain(tail)),
                        first_poll: Some(first_poll),
                        dropped: Some(dropped),
                    }),
                )
                    .into_response()
            }
        }),
    );
    let (url, server) = serve(app).await;
    Upstream {
        url,
        _server: server,
        requests,
        first_poll: first_poll_wait,
        dropped: dropped_wait,
        visible: visible_release,
        finish,
    }
}

fn request(url: &str) -> reqwest::RequestBuilder {
    reqwest::Client::builder()
        .timeout(Duration::from_secs(10))
        .build()
        .expect("downstream client")
        .post(format!("{url}/v1/chat/completions"))
        .header(header::CONTENT_TYPE, "application/json")
        .body(REQUEST)
}

async fn bounded_signal(receiver: &mut oneshot::Receiver<()>, context: &str) {
    tokio::time::timeout(OUTER_DEADLINE, receiver)
        .await
        .expect(context)
        .expect(context);
}

async fn read_prefix(response: &mut reqwest::Response, expected: &[u8]) {
    let received = tokio::time::timeout(OUTER_DEADLINE, async {
        let mut received = Vec::new();
        while received.len() < expected.len() {
            received.extend_from_slice(&response.chunk().await.expect("wire chunk").expect("body"));
        }
        received
    })
    .await
    .expect("forward prefix before upstream finishes");
    assert_eq!(
        received, expected,
        "preserve buffered SSE bytes and ordering"
    );
}

#[tokio::test]
async fn disabled_guard_commits_http_headers_before_any_frame_and_disconnect_closes_upstream() {
    for timeout in [None, Some(Duration::ZERO)] {
        let mut upstream = upstream(Vec::new(), CONTENT).await;
        let (url, _gateway) = gateway(&upstream.url, timeout).await;
        let mut response = tokio::time::timeout(OUTER_DEADLINE, request(&url).send())
            .await
            .expect("headers arrive while first frame is gated")
            .expect("HTTP response");
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "text/event-stream"
        );
        assert_eq!(
            response.headers()[header::CACHE_CONTROL],
            "no-cache, no-transform"
        );
        assert_eq!(response.headers()["x-accel-buffering"], "no");
        assert_eq!(response.headers()["x-codex-turn-state"], "local-state");
        bounded_signal(&mut upstream.first_poll, "upstream body started").await;
        assert!(
            tokio::time::timeout(Duration::from_millis(50), response.chunk())
                .await
                .is_err(),
            "observed headers must precede every body frame"
        );
        drop(response);
        bounded_signal(
            &mut upstream.dropped,
            "disconnect closes upstream before idle timeout",
        )
        .await;
        assert_eq!(upstream.requests.load(Ordering::SeqCst), 1);
    }
}

#[tokio::test]
async fn guarded_headers_role_usage_and_heartbeat_stalls_return_real_uncommitted_504() {
    for prefix in [Vec::new(), ROLE.to_vec(), [ROLE, USAGE, HEARTBEAT].concat()] {
        let mut upstream = upstream(prefix, CONTENT).await;
        let (url, _gateway) = gateway(&upstream.url, Some(FIRST_OUTPUT)).await;
        let response = request(&url).send();
        tokio::pin!(response);
        // Drive the downstream request while synchronizing on real upstream I/O.
        tokio::select! {
            result = &mut response => panic!("headers committed before visible output: {result:?}"),
            _ = bounded_signal(&mut upstream.first_poll, "upstream body started") => {},
        }
        assert!(
            tokio::time::timeout(Duration::from_millis(50), &mut response)
                .await
                .is_err(),
            "nonvisible provider activity must not commit downstream headers"
        );
        let response = tokio::time::timeout(OUTER_DEADLINE, response)
            .await
            .expect("first output deadline")
            .expect("HTTP error response");
        assert_eq!(response.status(), StatusCode::GATEWAY_TIMEOUT);
        assert_ne!(
            response.headers()[header::CONTENT_TYPE],
            "text/event-stream"
        );
        let error: serde_json::Value = response.json().await.expect("JSON error envelope");
        assert_eq!(error["error"]["code"], "upstream_timeout");
        bounded_signal(
            &mut upstream.dropped,
            "timeout closes upstream response body",
        )
        .await;
        assert_eq!(upstream.requests.load(Ordering::SeqCst), 1);
    }
}

#[tokio::test]
async fn visible_content_reasoning_tool_and_function_commit_once_and_retire_deadline() {
    for visible in [CONTENT, REASONING, TOOL, FUNCTION] {
        let prefix = [ROLE, USAGE, HEARTBEAT].concat();
        let expected = [prefix.as_slice(), visible].concat();
        let mut upstream = upstream(prefix, visible).await;
        let (url, _gateway) = gateway(&upstream.url, Some(FIRST_OUTPUT)).await;
        let response = request(&url).send();
        tokio::pin!(response);
        tokio::select! {
            result = &mut response => panic!("headers committed before visible output: {result:?}"),
            _ = bounded_signal(&mut upstream.first_poll, "upstream body started") => {},
        }
        assert!(
            tokio::time::timeout(Duration::from_millis(50), &mut response)
                .await
                .is_err()
        );
        upstream
            .visible
            .send(())
            .expect("upstream still waiting for visibility");
        let mut response = tokio::time::timeout(OUTER_DEADLINE, response)
            .await
            .expect("visible frame commits response")
            .expect("HTTP response");
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "text/event-stream"
        );
        read_prefix(&mut response, &expected).await;
        tokio::time::sleep(FIRST_OUTPUT + Duration::from_millis(50)).await;
        upstream
            .finish
            .send(())
            .expect("visible output retired first output deadline");
        let tail = tokio::time::timeout(OUTER_DEADLINE, response.bytes())
            .await
            .expect("terminal frame")
            .expect("stream survives original deadline");
        assert_eq!(tail.as_ref(), DONE);
        bounded_signal(&mut upstream.dropped, "completed upstream body dropped").await;
        assert_eq!(upstream.requests.load(Ordering::SeqCst), 1);
    }
}

#[tokio::test]
async fn downstream_disconnect_after_visible_commit_closes_upstream_without_another_attempt() {
    let mut upstream = upstream(ROLE.to_vec(), CONTENT).await;
    let (url, _gateway) = gateway(&upstream.url, Some(FIRST_OUTPUT)).await;
    upstream.visible.send(()).expect("release visible output");
    let mut response = tokio::time::timeout(OUTER_DEADLINE, request(&url).send())
        .await
        .expect("visible response deadline")
        .expect("HTTP response");
    read_prefix(&mut response, &[ROLE, CONTENT].concat()).await;
    drop(response);
    bounded_signal(
        &mut upstream.dropped,
        "disconnect stops pending upstream tail",
    )
    .await;
    assert_eq!(upstream.requests.load(Ordering::SeqCst), 1);
}

#[tokio::test]
async fn downstream_disconnect_before_commit_cancels_pending_upstream_read() {
    let mut upstream = upstream(ROLE.to_vec(), CONTENT).await;
    let (url, _gateway) = gateway(&upstream.url, Some(Duration::from_secs(10))).await;
    let address = url.strip_prefix("http://").expect("loopback HTTP URL");
    let mut downstream = TcpStream::connect(address)
        .await
        .expect("downstream socket");
    let request = format!(
        "POST /v1/chat/completions HTTP/1.1\r\nHost: {address}\r\nContent-Type: application/json\r\nContent-Length: {}\r\n\r\n{REQUEST}",
        REQUEST.len()
    );
    downstream
        .write_all(request.as_bytes())
        .await
        .expect("send HTTP request");
    bounded_signal(
        &mut upstream.first_poll,
        "upstream body started before disconnect",
    )
    .await;
    drop(downstream);
    bounded_signal(
        &mut upstream.dropped,
        "disconnect cancels read before first-output timeout",
    )
    .await;
    assert_eq!(upstream.requests.load(Ordering::SeqCst), 1);
}
