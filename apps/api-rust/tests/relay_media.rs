use std::{
    sync::{
        Arc, Mutex,
        atomic::{AtomicUsize, Ordering},
    },
    time::Duration,
};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{HeaderMap, Request, Response, StatusCode, header},
};
use lmm_api_rs::routes::relay_media::{
    MediaUpstreamClient, MediaUpstreamTarget, RelayMediaHttpState, RelayMediaService,
    relay_media_router,
};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::TcpListener,
};
use tower::ServiceExt;

#[derive(Clone, Debug, Eq, PartialEq)]
struct SeenRequest {
    method: String,
    path: String,
    headers: HeaderMap,
    body: Vec<u8>,
}

#[derive(Default)]
struct StubUpstream {
    requests: Mutex<Vec<SeenRequest>>,
    rejected: AtomicUsize,
}

#[async_trait]
impl RelayMediaService for StubUpstream {
    async fn relay(&self, request: axum::extract::Request) -> axum::response::Response {
        if request
            .headers()
            .get(header::AUTHORIZATION)
            .and_then(|value| value.to_str().ok())
            != Some("Bearer test-media-token")
        {
            self.rejected.fetch_add(1, Ordering::Relaxed);
            return Response::builder()
                .status(StatusCode::UNAUTHORIZED)
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(
                    r#"{"error":{"message":"Invalid token","type":"new_api_error","code":""}}"#,
                ))
                .expect("auth response");
        }
        let (parts, body) = request.into_parts();
        let body = to_bytes(body, usize::MAX).await.expect("stub request body");
        self.requests.lock().expect("stub lock").push(SeenRequest {
            method: parts.method.to_string(),
            path: parts.uri.path().to_owned(),
            headers: parts.headers,
            body: body.to_vec(),
        });

        let (status, content_type, payload) = match parts.uri.path() {
            "/v1/audio/speech" => (StatusCode::OK, "audio/mpeg", b"ID3audio".as_slice()),
            "/v1/files/file-binary/content" => (
                StatusCode::OK,
                "application/octet-stream",
                b"\0\x01file".as_slice(),
            ),
            _ => (
                StatusCode::ACCEPTED,
                "application/json",
                br#"{"ok":true}"#.as_slice(),
            ),
        };
        Response::builder()
            .status(status)
            .header(header::CONTENT_TYPE, content_type)
            .header("x-upstream-request-id", "stub-42")
            .body(Body::from(payload.to_vec()))
            .expect("stub response")
    }
}

fn router(stub: Arc<StubUpstream>) -> axum::Router {
    relay_media_router(RelayMediaHttpState::new(stub))
}

async fn call(router: &axum::Router, request: Request<Body>) -> axum::response::Response {
    router
        .clone()
        .oneshot(request)
        .await
        .expect("router response")
}

async fn spawn_tcp_router(router: axum::Router) -> (String, tokio::task::JoinHandle<()>) {
    let listener = TcpListener::bind("127.0.0.1:0").await.expect("listener");
    let address = listener.local_addr().expect("listener address");
    let server = tokio::spawn(async move {
        axum::serve(listener, router)
            .await
            .expect("test listener serves");
    });
    (format!("http://{address}"), server)
}

#[tokio::test]
async fn image_edit_keeps_multipart_boundary_and_authorization_for_upstream() {
    let stub = Arc::new(StubUpstream::default());
    let response = call(
        &router(Arc::clone(&stub)),
        Request::builder()
            .method("POST")
            .uri("/v1/images/edits")
            .header(header::AUTHORIZATION, "Bearer test-media-token")
            .header(
                header::CONTENT_TYPE,
                "multipart/form-data; boundary=upload-123",
            )
            .body(Body::from("--upload-123\r\ncontent\r\n--upload-123--\r\n"))
            .expect("request"),
    )
    .await;

    assert_eq!(response.status(), StatusCode::ACCEPTED);
    assert_eq!(
        stub.requests.lock().expect("stub lock")[0],
        SeenRequest {
            method: "POST".to_owned(),
            path: "/v1/images/edits".to_owned(),
            headers: HeaderMap::from_iter([
                (
                    header::AUTHORIZATION,
                    "Bearer test-media-token".parse().expect("header")
                ),
                (
                    header::CONTENT_TYPE,
                    "multipart/form-data; boundary=upload-123"
                        .parse()
                        .expect("header")
                ),
            ]),
            body: b"--upload-123\r\ncontent\r\n--upload-123--\r\n".to_vec(),
        }
    );
}

#[tokio::test]
async fn audio_speech_preserves_binary_response_status_and_headers() {
    let stub = Arc::new(StubUpstream::default());
    let response = call(
        &router(stub),
        Request::builder()
            .method("POST")
            .uri("/v1/audio/speech")
            .header(header::AUTHORIZATION, "Bearer test-media-token")
            .body(Body::from(r#"{"model":"tts-1","input":"hello"}"#))
            .expect("request"),
    )
    .await;

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(response.headers()[header::CONTENT_TYPE], "audio/mpeg");
    assert_eq!(response.headers()["x-upstream-request-id"], "stub-42");
    assert_eq!(
        to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("audio body")
            .as_ref(),
        b"ID3audio"
    );
}

#[tokio::test]
async fn only_media_owned_routes_reach_the_forwarding_boundary() {
    let stub = Arc::new(StubUpstream::default());
    let router = router(Arc::clone(&stub));
    for (method, path) in [
        ("POST", "/v1/audio/transcriptions"),
        ("POST", "/v1/audio/translations"),
        ("POST", "/v1/images/generations"),
    ] {
        let request = Request::builder()
            .method(method)
            .uri(path)
            .header(header::AUTHORIZATION, "Bearer test-media-token")
            .body(Body::empty())
            .expect("request");
        let response = call(&router, request).await;
        assert_eq!(response.status(), StatusCode::ACCEPTED);
    }
    assert_eq!(stub.requests.lock().expect("stub lock").len(), 3);
}

#[tokio::test]
async fn every_legacy_501_media_and_file_path_is_excluded_from_the_media_router() {
    let stub = Arc::new(StubUpstream::default());
    let router = router(Arc::clone(&stub));
    for (method, path) in [
        ("POST", "/v1/images/variations"),
        ("GET", "/v1/files"),
        ("POST", "/v1/files"),
        ("GET", "/v1/files/file-1"),
        ("DELETE", "/v1/files/file-1"),
        ("GET", "/v1/files/file-1/content"),
        ("GET", "/v1/fine-tunes"),
        ("POST", "/v1/fine-tunes"),
        ("GET", "/v1/fine-tunes/ft-1"),
        ("POST", "/v1/fine-tunes/ft-1/cancel"),
        ("GET", "/v1/fine-tunes/ft-1/events"),
    ] {
        let response = call(
            &router,
            Request::builder()
                .method(method)
                .uri(path)
                .body(Body::empty())
                .expect("request"),
        )
        .await;
        assert_eq!(response.status(), StatusCode::NOT_FOUND, "{path}");
    }
    assert!(stub.requests.lock().expect("stub lock").is_empty());
}

#[tokio::test]
async fn media_auth_and_route_status_contract_holds_over_a_real_tcp_router() {
    let stub = Arc::new(StubUpstream::default());
    let (base_url, server) = spawn_tcp_router(router(Arc::clone(&stub))).await;
    let client = reqwest::Client::new();

    let missing = client
        .post(format!("{base_url}/v1/audio/speech"))
        .send()
        .await
        .expect("missing-token response");
    assert_eq!(missing.status(), reqwest::StatusCode::UNAUTHORIZED);
    assert_eq!(
        missing
            .json::<serde_json::Value>()
            .await
            .expect("missing JSON")["error"]["type"],
        "new_api_error"
    );

    let bad = client
        .post(format!("{base_url}/v1/audio/speech"))
        .header(header::AUTHORIZATION, "Bearer bad-token")
        .send()
        .await
        .expect("bad-token response");
    assert_eq!(bad.status(), reqwest::StatusCode::UNAUTHORIZED);
    assert_eq!(
        bad.json::<serde_json::Value>().await.expect("bad JSON"),
        serde_json::json!({"error":{"message":"Invalid token","type":"new_api_error","code":""}})
    );

    let accepted = client
        .post(format!("{base_url}/v1/audio/speech"))
        .header(header::AUTHORIZATION, "Bearer test-media-token")
        .send()
        .await
        .expect("accepted response");
    assert_eq!(accepted.status(), reqwest::StatusCode::OK);

    let wrong_method = client
        .get(format!("{base_url}/v1/audio/speech"))
        .header(header::AUTHORIZATION, "Bearer test-media-token")
        .send()
        .await
        .expect("wrong-method response");
    assert_eq!(
        wrong_method.status(),
        reqwest::StatusCode::METHOD_NOT_ALLOWED
    );

    let missing_route = client
        .post(format!("{base_url}/v1/audio/not-a-route"))
        .header(header::AUTHORIZATION, "Bearer test-media-token")
        .send()
        .await
        .expect("missing-route response");
    assert_eq!(missing_route.status(), reqwest::StatusCode::NOT_FOUND);

    assert_eq!(stub.rejected.load(Ordering::Relaxed), 1);
    assert_eq!(stub.requests.lock().expect("stub lock").len(), 1);
    server.abort();
}

#[tokio::test]
async fn concrete_upstream_client_replaces_caller_auth_and_preserves_chunked_sse() {
    let listener = TcpListener::bind("127.0.0.1:0").await.expect("listener");
    let address = listener.local_addr().expect("address");
    let server = tokio::spawn(async move {
        let (mut socket, _) = listener.accept().await.expect("connection");
        let mut request = vec![0; 4096];
        let size = socket.read(&mut request).await.expect("read request");
        let request = String::from_utf8_lossy(&request[..size]).into_owned();
        socket
            .write_all(b"HTTP/1.1 200 OK\r\ncontent-type: text/event-stream\r\ntransfer-encoding: chunked\r\n\r\n")
            .await
            .expect("headers");
        socket
            .write_all(b"d\r\ndata: first\n\n\r\n")
            .await
            .expect("first chunk");
        tokio::time::sleep(Duration::from_millis(10)).await;
        socket
            .write_all(b"e\r\ndata: [DONE]\n\n\r\n0\r\n\r\n")
            .await
            .expect("second chunk");
        request
    });
    let mut headers = HeaderMap::new();
    headers.insert(
        header::AUTHORIZATION,
        "Bearer caller-secret".parse().expect("header"),
    );
    headers.insert(
        header::CONTENT_TYPE,
        "multipart/form-data; boundary=oracle"
            .parse()
            .expect("header"),
    );
    headers.insert(header::CONNECTION, "x-media-local".parse().expect("header"));
    headers.insert("x-media-local", "must-not-forward".parse().expect("header"));
    let response = MediaUpstreamClient::new(reqwest::Client::new(), Duration::from_secs(1))
        .forward(
            &MediaUpstreamTarget {
                base_url: format!("http://{address}/"),
                api_key: "channel-secret".to_owned(),
            },
            "POST".parse().expect("method"),
            "/v1/images/edits",
            &headers,
            b"--oracle\r\npayload\r\n--oracle--\r\n".to_vec(),
            false,
        )
        .await
        .expect("upstream response");
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        response.headers()[header::CONTENT_TYPE],
        "text/event-stream"
    );
    assert_eq!(
        to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("stream body")
            .as_ref(),
        b"data: first\n\ndata: [DONE]\n\n"
    );
    let request = server.await.expect("server task");
    assert!(
        request.contains("authorization: Bearer channel-secret")
            || request.contains("Authorization: Bearer channel-secret")
    );
    assert!(!request.contains("caller-secret"));
    assert!(request.contains("boundary=oracle"));
    assert!(!request.contains("x-media-local: must-not-forward"));
}

#[tokio::test]
async fn concrete_upstream_client_times_out_without_retrying_non_idempotent_submit() {
    let listener = TcpListener::bind("127.0.0.1:0").await.expect("listener");
    let address = listener.local_addr().expect("address");
    let (count_tx, count_rx) = tokio::sync::oneshot::channel();
    let server = tokio::spawn(async move {
        let (mut socket, _) = listener.accept().await.expect("connection");
        let mut request = [0; 512];
        let _ = socket.read(&mut request).await.expect("request");
        count_tx.send(()).expect("count receiver");
        tokio::time::sleep(Duration::from_millis(100)).await;
    });
    let result = MediaUpstreamClient::new(reqwest::Client::new(), Duration::from_millis(15))
        .with_max_attempts(2)
        .forward(
            &MediaUpstreamTarget {
                base_url: format!("http://{address}/"),
                api_key: "channel-secret".to_owned(),
            },
            "POST".parse().expect("method"),
            "/v1/images/generations",
            &HeaderMap::new(),
            b"{}".to_vec(),
            false,
        )
        .await;
    assert!(result.is_err());
    count_rx.await.expect("one upstream request");
    server.await.expect("server task");
}
