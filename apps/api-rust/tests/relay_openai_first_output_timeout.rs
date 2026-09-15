use std::{convert::Infallible, time::Duration};

use axum::{
    Router,
    body::{Body, Bytes},
    http::{Method, StatusCode, header},
    response::IntoResponse,
    routing::post,
};
use futures_util::{StreamExt, stream};
use lmm_api_rs::relay_http::{RelayHttpClient, RelayHttpError, RelayTimeoutConfig};
use tokio::{net::TcpListener, task::JoinHandle};

const ROLE_ONLY: &[u8] = b"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n";
const VISIBLE: &[u8] = b"data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n";
const DONE: &[u8] = b"data: [DONE]\n\n";

struct Server(JoinHandle<()>);

impl Drop for Server {
    fn drop(&mut self) {
        self.0.abort();
    }
}

async fn serve(app: Router) -> (reqwest::Url, Server) {
    let listener = TcpListener::bind("127.0.0.1:0").await.expect("listen");
    let address = listener.local_addr().expect("address");
    let server = Server(tokio::spawn(async move {
        axum::serve(listener, app).await.expect("serve");
    }));
    (
        format!("http://{address}/v1/chat/completions")
            .parse()
            .expect("url"),
        server,
    )
}

fn client(timeout: Option<Duration>) -> RelayHttpClient {
    RelayHttpClient::new(RelayTimeoutConfig {
        response_headers: Some(Duration::from_secs(2)),
        idle: Duration::from_secs(5),
        total: None,
    })
    .expect("relay client")
    .with_openai_first_output_timeout(timeout)
}

#[tokio::test]
async fn headers_then_stall_times_out_before_relay_response_is_released() {
    let app = Router::new().route(
        "/v1/chat/completions",
        post(|| async {
            (
                [(header::CONTENT_TYPE, "text/event-stream")],
                Body::from_stream(stream::pending::<Result<Bytes, Infallible>>()),
            )
                .into_response()
        }),
    );
    let (url, _server) = serve(app).await;
    let relay = client(Some(Duration::from_millis(50)));
    let result = relay.send(relay.request(Method::POST, url)).await;
    assert!(matches!(result, Err(RelayHttpError::FirstOutput)));
}

#[tokio::test]
async fn role_only_frame_does_not_satisfy_first_visible_output_deadline() {
    let app = Router::new().route(
        "/v1/chat/completions",
        post(|| async {
            let role = stream::once(async { Ok::<_, Infallible>(Bytes::from_static(ROLE_ONLY)) });
            (
                [(header::CONTENT_TYPE, "text/event-stream")],
                Body::from_stream(role.chain(stream::pending())),
            )
                .into_response()
        }),
    );
    let (url, _server) = serve(app).await;
    let relay = client(Some(Duration::from_millis(50)));
    let result = relay.send(relay.request(Method::POST, url)).await;
    assert!(matches!(result, Err(RelayHttpError::FirstOutput)));
}

#[tokio::test]
async fn visible_content_retires_deadline_and_preserves_buffered_wire_bytes() {
    let app = Router::new().route(
        "/v1/chat/completions",
        post(|| async {
            let prefix = stream::iter([
                Ok::<_, Infallible>(Bytes::from_static(ROLE_ONLY)),
                Ok(Bytes::from_static(VISIBLE)),
            ]);
            let tail = stream::once(async {
                tokio::time::sleep(Duration::from_millis(100)).await;
                Ok::<_, Infallible>(Bytes::from_static(DONE))
            });
            (
                [(header::CONTENT_TYPE, "text/event-stream")],
                Body::from_stream(prefix.chain(tail)),
            )
                .into_response()
        }),
    );
    let (url, _server) = serve(app).await;
    let relay = client(Some(Duration::from_millis(50)));
    let mut response = relay
        .send(relay.request(Method::POST, url))
        .await
        .expect("visible output should release the response");
    assert_eq!(response.status(), StatusCode::OK);
    let first = response
        .chunk()
        .await
        .expect("first buffered chunk")
        .expect("first buffered chunk present");
    let expected_prefix = [ROLE_ONLY, VISIBLE].concat();
    assert_eq!(first.as_ref(), expected_prefix.as_slice());
    tokio::time::sleep(Duration::from_millis(100)).await;
    assert_eq!(
        response
            .chunk()
            .await
            .expect("tail chunk")
            .expect("tail chunk present")
            .as_ref(),
        DONE
    );
}

#[tokio::test]
async fn disabled_first_output_guard_keeps_header_first_streaming_behavior() {
    let app = Router::new().route(
        "/v1/chat/completions",
        post(|| async {
            (
                [(header::CONTENT_TYPE, "text/event-stream")],
                Body::from_stream(stream::pending::<Result<Bytes, Infallible>>()),
            )
                .into_response()
        }),
    );
    let (url, _server) = serve(app).await;
    let relay = client(None);
    let response = tokio::time::timeout(
        Duration::from_millis(200),
        relay.send(relay.request(Method::POST, url)),
    )
    .await
    .expect("disabled guard must not wait for body output")
    .expect("response headers should be sufficient");
    assert_eq!(response.status(), StatusCode::OK);
}
