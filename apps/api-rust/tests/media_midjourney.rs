use std::{
    collections::BTreeSet,
    sync::{Arc, Mutex},
    time::Duration,
};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{Request, StatusCode},
};
use hmac::{Hmac, Mac};
use lmm_api_rs::routes::media_midjourney::{
    BufferedJsonReply, ImageReply, MidjourneyBackend, MidjourneyChannel, MidjourneyFailure,
    MidjourneyHttpState, MidjourneyIdentity, PgMidjourneyBackend, StoredImage, SubmitReply,
    TaskEffect, media_midjourney_dynamic_router, media_midjourney_router,
};
use serde_json::json;
use sha2::Sha256;
use sqlx::PgPool;
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::TcpListener,
};
use tower::ServiceExt;

const IMAGE_SECRET: &[u8] = b"image-signing-contract-secret";
type HmacSha256 = Hmac<Sha256>;

struct TestMidjourneyBackend {
    accepted_tokens: BTreeSet<String>,
    effects: Mutex<Vec<TaskEffect>>,
    submit_calls: Mutex<usize>,
    image_fetches: Mutex<usize>,
    image: Mutex<Option<StoredImage>>,
    stream_target: Mutex<Option<String>>,
    reply: Mutex<BufferedJsonReply>,
}

impl TestMidjourneyBackend {
    fn new(tokens: impl IntoIterator<Item = String>) -> Self {
        Self {
            accepted_tokens: tokens
                .into_iter()
                .filter(|token| !token.is_empty())
                .collect(),
            effects: Mutex::new(Vec::new()),
            submit_calls: Mutex::new(0),
            image_fetches: Mutex::new(0),
            image: Mutex::new(None),
            stream_target: Mutex::new(None),
            reply: Mutex::new(BufferedJsonReply {
                status: StatusCode::OK,
                content_type: "application/json".parse().expect("header"),
                body: json!({"code":1,"description":"ok","result":"mock-job-1"}),
            }),
        }
    }

    fn set_reply(&self, reply: BufferedJsonReply) {
        *self.reply.lock().expect("reply lock") = reply;
    }

    fn set_image(&self, image: StoredImage) {
        *self.image.lock().expect("image lock") = Some(image);
    }

    fn set_stream_target(&self, target: String) {
        *self.stream_target.lock().expect("stream target lock") = Some(target);
    }

    fn effects(&self) -> Vec<TaskEffect> {
        self.effects.lock().expect("effects lock").clone()
    }

    fn submit_calls(&self) -> usize {
        *self.submit_calls.lock().expect("submit calls lock")
    }

    fn image_fetches(&self) -> usize {
        *self.image_fetches.lock().expect("image fetches lock")
    }
}

#[async_trait]
impl MidjourneyBackend for TestMidjourneyBackend {
    async fn authenticate(
        &self,
        headers: &axum::http::HeaderMap,
        _: Option<std::net::IpAddr>,
    ) -> Result<MidjourneyIdentity, MidjourneyFailure> {
        headers
            .get("authorization")
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.strip_prefix("Bearer "))
            .filter(|token| self.accepted_tokens.contains(*token))
            .map(|_| MidjourneyIdentity {
                user_id: 1,
                token_id: "1".to_owned(),
            })
            .ok_or(MidjourneyFailure::Unauthorized)
    }

    async fn submit(
        &self,
        _: &MidjourneyIdentity,
        mode: &str,
        operation: &str,
        _: &axum::http::HeaderMap,
        body: serde_json::Value,
    ) -> Result<SubmitReply, MidjourneyFailure> {
        *self.submit_calls.lock().expect("submit calls lock") += 1;
        match body
            .get("__mock_failure")
            .and_then(serde_json::Value::as_str)
        {
            Some("upstream") => Err(MidjourneyFailure::Upstream),
            Some("invalid-json") => Err(MidjourneyFailure::InvalidUpstreamJson),
            Some(_) => Err(MidjourneyFailure::NotFound),
            None => {
                let response = self.reply.lock().expect("reply lock").clone();
                Ok(SubmitReply {
                    effect: TaskEffect {
                        mode: mode.to_owned(),
                        operation: operation.to_owned(),
                        task_id: response.body["result"]
                            .as_str()
                            .unwrap_or_default()
                            .to_owned(),
                        action: operation.to_ascii_uppercase(),
                        prompt: body["prompt"].as_str().unwrap_or_default().to_owned(),
                        state: body["state"].as_str().unwrap_or_default().to_owned(),
                        code: response.body["code"].as_i64().unwrap_or_default(),
                        description: response.body["description"]
                            .as_str()
                            .unwrap_or_default()
                            .to_owned(),
                        properties: response.body["properties"].clone(),
                        channel_id: 1,
                        quota: 1,
                    },
                    response,
                })
            }
        }
    }

    async fn record_submit(
        &self,
        _: &MidjourneyIdentity,
        effect: TaskEffect,
    ) -> Result<(), MidjourneyFailure> {
        self.effects.lock().expect("effects lock").push(effect);
        Ok(())
    }

    async fn task_read(
        &self,
        _: &MidjourneyIdentity,
        _: &str,
        _: &str,
        _: &axum::http::HeaderMap,
        _: Option<serde_json::Value>,
    ) -> Result<BufferedJsonReply, MidjourneyFailure> {
        Ok(self.reply.lock().expect("reply lock").clone())
    }

    async fn image_for(&self, _: &str) -> Result<StoredImage, MidjourneyFailure> {
        self.image
            .lock()
            .expect("image lock")
            .clone()
            .ok_or(MidjourneyFailure::NotFound)
    }

    async fn image_for_owned(
        &self,
        user_id: i64,
        task_id: &str,
    ) -> Result<StoredImage, MidjourneyFailure> {
        if user_id != 1 {
            return Err(MidjourneyFailure::NotFound);
        }
        self.image_for(task_id).await
    }

    async fn fetch_image(&self, _: &str) -> Result<ImageReply, MidjourneyFailure> {
        *self.image_fetches.lock().expect("image fetches lock") += 1;
        let target = self
            .stream_target
            .lock()
            .expect("stream target lock")
            .clone();
        if let Some(target) = target {
            let response = reqwest::Client::new()
                .get(target)
                .send()
                .await
                .map_err(|_| MidjourneyFailure::Upstream)?;
            return Ok(ImageReply::Stream {
                content_type: response
                    .headers()
                    .get("content-type")
                    .cloned()
                    .unwrap_or_else(|| "image/jpeg".parse().expect("header")),
                body: Body::from_stream(response.bytes_stream()),
            });
        }
        Ok(ImageReply::Stream {
            content_type: "image/png".parse().expect("header"),
            body: Body::from("mock-png"),
        })
    }
}

fn app(backend: Arc<TestMidjourneyBackend>) -> axum::Router {
    media_midjourney_router(
        MidjourneyHttpState::new(backend).with_image_signing_secret(IMAGE_SECRET),
    )
}

fn dynamic_app(backend: Arc<TestMidjourneyBackend>) -> axum::Router {
    media_midjourney_dynamic_router(
        MidjourneyHttpState::new(backend).with_image_signing_secret(IMAGE_SECRET),
    )
}

fn signed_image_path(prefix: &str, user_id: i64, task_id: &str) -> String {
    let mut mac = HmacSha256::new_from_slice(IMAGE_SECRET).expect("HMAC key");
    mac.update(format!("midjourney-image-v1:{user_id}:{task_id}").as_bytes());
    format!(
        "{prefix}/{task_id}?uid={user_id}&sig={}",
        hex::encode(mac.finalize().into_bytes())
    )
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

fn submit(path: &str) -> Request<Body> {
    Request::builder()
        .method("POST")
        .uri(path)
        .header("authorization", "Bearer test-token")
        .header("content-type", "application/json")
        .body(Body::from(
            r#"{"prompt":"cat","accountFilter":"private","notifyHook":"https://private"}"#,
        ))
        .expect("valid request")
}

#[tokio::test]
async fn dynamic_router_mounts_the_mode_prefixed_submit_surface() {
    let backend = Arc::new(TestMidjourneyBackend::new(["test-token".to_owned()]));
    let response = dynamic_app(backend)
        .oneshot(submit("/proxy/mj/submit/imagine"))
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
}

#[tokio::test]
async fn dynamic_submit_should_normalize_replay_and_persist_one_effect() {
    let backend = Arc::new(TestMidjourneyBackend::new(["test-token".to_owned()]));
    backend.set_reply(BufferedJsonReply {
        status: StatusCode::OK,
        content_type: "application/json".parse().expect("header"),
        body: json!({"code":21,"description":"exists","result":"replay-1"}),
    });
    let response = app(Arc::clone(&backend))
        .oneshot(submit("/proxy/mj/submit/imagine"))
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(
            &to_bytes(response.into_body(), usize::MAX)
                .await
                .expect("body")
        )
        .expect("json")["code"],
        1
    );
    assert_eq!(backend.effects().len(), 1);
}

#[tokio::test]
async fn failed_submit_should_not_create_a_task_effect() {
    let backend = Arc::new(TestMidjourneyBackend::new(["test-token".to_owned()]));
    backend.set_reply(BufferedJsonReply {
        status: StatusCode::BAD_GATEWAY,
        content_type: "application/json".parse().expect("header"),
        body: json!({"code":5,"description":"provider down"}),
    });
    let response = app(Arc::clone(&backend))
        .oneshot(submit("/proxy/mj/submit/imagine"))
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::BAD_GATEWAY);
    assert!(backend.effects().is_empty());
}

#[tokio::test]
async fn upstream_failure_should_use_the_legacy_error_without_a_task_effect() {
    let backend = Arc::new(TestMidjourneyBackend::new(["test-token".to_owned()]));
    let request = Request::builder()
        .method("POST")
        .uri("/proxy/mj/submit/imagine")
        .header("authorization", "Bearer test-token")
        .header("content-type", "application/json")
        .body(Body::from(r#"{"__mock_failure":"upstream"}"#))
        .expect("request");
    let response = app(Arc::clone(&backend))
        .oneshot(request)
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::BAD_REQUEST);
    assert!(backend.effects().is_empty());
}

#[tokio::test]
async fn simultaneous_replays_should_reach_the_backend_twice_without_deduplication() {
    let backend = Arc::new(TestMidjourneyBackend::new(["test-token".to_owned()]));
    backend.set_reply(BufferedJsonReply {
        status: StatusCode::OK,
        content_type: "application/json".parse().expect("header"),
        body: json!({"code":22,"result":"queued-1"}),
    });
    let router = app(Arc::clone(&backend));
    let (first, second) = tokio::join!(
        router.clone().oneshot(submit("/edge/mj/submit/imagine")),
        router.oneshot(submit("/edge/mj/submit/imagine"))
    );
    assert_eq!(first.expect("first").status(), StatusCode::OK);
    assert_eq!(second.expect("second").status(), StatusCode::OK);
    assert_eq!(backend.effects().len(), 2);
}

#[tokio::test]
async fn public_image_should_stream_exact_binary_without_token() {
    let backend = Arc::new(TestMidjourneyBackend::new(Vec::<String>::new()));
    backend.set_image(StoredImage {
        url: "https://images.example.test/image.png".to_owned(),
    });
    let response = app(backend)
        .oneshot(
            Request::builder()
                .uri(signed_image_path("/proxy/mj/image", 1, "task-1"))
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(response.headers()["content-type"], "image/png");
    assert_eq!(
        to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("image")
            .as_ref(),
        b"mock-png"
    );
}

#[tokio::test]
async fn public_image_returns_headers_before_the_upstream_stream_finishes() {
    let listener = TcpListener::bind("127.0.0.1:0").await.expect("listener");
    let address = listener.local_addr().expect("address");
    let (release, released) = tokio::sync::oneshot::channel::<()>();
    let upstream = tokio::spawn(async move {
        let (mut socket, _) = listener.accept().await.expect("connection");
        let mut request = [0_u8; 1024];
        let _ = socket.read(&mut request).await.expect("request");
        socket
            .write_all(
                b"HTTP/1.1 200 OK\r\ncontent-type: image/png\r\ntransfer-encoding: chunked\r\n\r\n6\r\nfirst-\r\n",
            )
            .await
            .expect("first chunk");
        let _ = released.await;
        socket
            .write_all(b"4\r\nlast\r\n0\r\n\r\n")
            .await
            .expect("last chunk");
    });
    let backend = Arc::new(TestMidjourneyBackend::new(Vec::<String>::new()));
    backend.set_image(StoredImage {
        url: "https://images.example.test/stream.png".to_owned(),
    });
    backend.set_stream_target(format!("http://{address}/stream.png"));
    let response = tokio::time::timeout(
        Duration::from_secs(3),
        app(backend).oneshot(
            Request::builder()
                .uri(signed_image_path("/proxy/mj/image", 1, "task-stream"))
                .body(Body::empty())
                .expect("request"),
        ),
    )
    .await
    .expect("handler must not buffer the unfinished image")
    .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(response.headers()["content-type"], "image/png");
    release.send(()).expect("release stream");
    assert_eq!(
        to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("stream body")
            .as_ref(),
        b"first-last"
    );
    upstream.await.expect("upstream task");
}

#[tokio::test]
async fn public_image_rejects_ipv6_loopback_before_any_upstream_fetch() {
    let backend = Arc::new(TestMidjourneyBackend::new(Vec::<String>::new()));
    backend.set_image(StoredImage {
        url: "http://[::1]/private.png".to_owned(),
    });
    let response = app(Arc::clone(&backend))
        .oneshot(
            Request::builder()
                .uri(signed_image_path("/proxy/mj/image", 1, "task-1"))
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(
            &to_bytes(response.into_body(), usize::MAX)
                .await
                .expect("body"),
        )
        .expect("JSON")["error"],
        "request blocked: unsafe image URL"
    );
    assert_eq!(backend.image_fetches(), 0);
}

#[tokio::test]
async fn malformed_and_invalid_submit_bodies_stop_before_the_upstream_boundary() {
    let backend = Arc::new(TestMidjourneyBackend::new(["test-token".to_owned()]));
    let router = app(Arc::clone(&backend));
    for (payload, description) in [
        (r#"{"#, "bind_request_body_failed "),
        (r#"{}"#, "prompt_is_required "),
    ] {
        let response = router
            .clone()
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/proxy/mj/submit/imagine")
                    .header("authorization", "Bearer test-token")
                    .header("content-type", "application/json")
                    .body(Body::from(payload))
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(response.status(), StatusCode::BAD_REQUEST);
        let body = serde_json::from_slice::<serde_json::Value>(
            &to_bytes(response.into_body(), usize::MAX)
                .await
                .expect("body"),
        )
        .expect("JSON");
        assert_eq!(body["description"], description);
    }
    assert_eq!(backend.submit_calls(), 0);
    assert!(backend.effects().is_empty());
}

#[tokio::test]
async fn unknown_public_image_uses_the_frozen_not_found_envelope() {
    let backend = Arc::new(TestMidjourneyBackend::new(Vec::<String>::new()));
    let response = app(Arc::clone(&backend))
        .oneshot(
            Request::builder()
                .uri(signed_image_path("/proxy/mj/image", 1, "missing"))
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::NOT_FOUND);
    let body = serde_json::from_slice::<serde_json::Value>(
        &to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("body"),
    )
    .expect("JSON");
    assert_eq!(body["error"], "midjourney_task_not_found");
    assert_eq!(backend.image_fetches(), 0);
}

#[tokio::test]
async fn protected_task_read_should_require_a_token() {
    let backend = Arc::new(TestMidjourneyBackend::new(["test-token".to_owned()]));
    let response = app(backend)
        .oneshot(
            Request::builder()
                .uri("/proxy/mj/task/task-1/fetch")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(
            &to_bytes(response.into_body(), usize::MAX)
                .await
                .expect("body"),
        )
        .expect("JSON"),
        json!({"error":{"message":"Invalid token","type":"new_api_error","code":""}})
    );
}

#[tokio::test]
async fn dynamic_midjourney_auth_and_route_status_contract_holds_over_a_real_tcp_router() {
    let backend = Arc::new(TestMidjourneyBackend::new(["test-token".to_owned()]));
    let (base_url, server) = spawn_tcp_router(app(backend)).await;
    let client = reqwest::Client::new();

    let public_image = client
        .get(format!(
            "{base_url}{}",
            signed_image_path("/proxy/mj/image", 1, "missing")
        ))
        .send()
        .await
        .expect("public-image response");
    assert_eq!(public_image.status(), reqwest::StatusCode::NOT_FOUND);
    assert_eq!(
        public_image
            .json::<serde_json::Value>()
            .await
            .expect("public-image JSON"),
        json!({"error":"midjourney_task_not_found"})
    );

    for path in ["/proxy/mj/submit/imagine", "/mj/submit/imagine"] {
        for authorization in [None, Some("Bearer bad-token")] {
            let mut request = client.post(format!("{base_url}{path}"));
            if let Some(authorization) = authorization {
                request = request.header("authorization", authorization);
            }
            let response = request
                .json(&json!({"prompt":"cat"}))
                .send()
                .await
                .expect("auth response");
            assert_eq!(response.status(), reqwest::StatusCode::UNAUTHORIZED);
            assert_eq!(
                response
                    .json::<serde_json::Value>()
                    .await
                    .expect("auth JSON"),
                json!({"error":{"message":"Invalid token","type":"new_api_error","code":""}})
            );
        }
    }

    let wrong_method = client
        .get(format!("{base_url}/proxy/mj/submit/imagine"))
        .send()
        .await
        .expect("wrong-method response");
    assert_eq!(
        wrong_method.status(),
        reqwest::StatusCode::METHOD_NOT_ALLOWED
    );

    let missing_route = client
        .post(format!("{base_url}/proxy/mj/not-a-route"))
        .send()
        .await
        .expect("missing-route response");
    assert_eq!(missing_route.status(), reqwest::StatusCode::NOT_FOUND);
    server.abort();
}

#[tokio::test]
async fn pg_adapter_uses_channel_secret_and_only_compatibility_headers_for_mock_upstream() {
    let listener = TcpListener::bind("127.0.0.1:0").await.expect("listener");
    let address = listener.local_addr().expect("address");
    let upstream = tokio::spawn(async move {
        let (mut socket, _) = listener.accept().await.expect("connection");
        let mut request = [0; 4096];
        let size = socket.read(&mut request).await.expect("request");
        socket
            .write_all(b"HTTP/1.1 200 OK\r\ncontent-type: application/json\r\ncontent-length: 45\r\n\r\n{\"code\":1,\"description\":\"ok\",\"result\":\"up-1\"}")
            .await
            .expect("response");
        String::from_utf8_lossy(&request[..size]).into_owned()
    });
    let backend = PgMidjourneyBackend::new(
        PgPool::connect_lazy("postgres://oracle:oracle@127.0.0.1:1/oracle").expect("lazy pool"),
        reqwest::Client::new(),
        MidjourneyChannel {
            id: 9,
            base_url: format!("http://{address}/"),
            api_key: "channel-mj-secret".to_owned(),
            quota: 0,
        },
        Duration::from_secs(1),
        16 * 1024,
    )
    .with_settings(lmm_api_rs::routes::media_midjourney::MidjourneySettings {
        require_successful_parent: false,
        ..Default::default()
    });
    let mut headers = axum::http::HeaderMap::new();
    headers.insert("content-type", "application/json".parse().expect("header"));
    headers.insert("accept", "application/json".parse().expect("header"));
    headers.insert(
        "authorization",
        "Bearer caller-secret".parse().expect("header"),
    );
    let response = backend
        .submit(
            &MidjourneyIdentity {
                user_id: 1,
                token_id: "1".to_owned(),
            },
            "proxy",
            "imagine",
            &headers,
            json!({
                "prompt":"mock upstream",
                "accountFilter":"private-account",
                "notifyHook":"https://private.invalid/hook"
            }),
        )
        .await
        .expect("submit response");
    assert_eq!(response.response.status, StatusCode::OK);
    assert_eq!(response.response.body["result"], "up-1");
    let request = upstream.await.expect("upstream task");
    assert!(request.starts_with("POST /mj/submit/imagine HTTP/1.1"));
    assert!(
        request.contains("mj-api-secret: channel-mj-secret")
            || request.contains("Mj-Api-Secret: channel-mj-secret")
    );
    assert!(!request.contains("caller-secret"));
    assert!(
        request.contains("content-type: application/json")
            || request.contains("Content-Type: application/json")
    );
    assert!(
        request.contains("accept: application/json")
            || request.contains("Accept: application/json")
    );
    assert!(!request.contains("accountFilter"));
    assert!(!request.contains("notifyHook"));
}
