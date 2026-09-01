use std::sync::{
    Arc, Mutex,
    atomic::{AtomicUsize, Ordering},
};

use async_trait::async_trait;
use axum::{
    body::{Body, Bytes, to_bytes},
    extract::Request as AxumRequest,
    http::{HeaderValue, Request, StatusCode},
    response::Response,
};
use lmm_api_rs::models::{ModelView, ModelsError, ModelsErrorKind};
use lmm_api_rs::routes::relay_anthropic_gemini::{
    NativeSseReply, RelayBackend, RelayChannel, RelayFailure, RelayHttpState, RelayIdentity,
    RelayOutcome, RelayProtocol, RelaySseEvent, UpstreamReply, UpstreamRequest, router,
    router_with_model_lookup,
};
use lmm_api_rs::routes::{
    model_lookup::{ModelLookupRequest, ModelLookupService, ModelLookupState},
    relay_media::{RelayMediaHttpState, RelayMediaService, relay_media_router},
    relay_misc::{RelayAuth, RelayMiscHttpState, RelayMiscService, routes as misc_routes},
    relay_openai::{
        OpenAiRelayAuthorization, OpenAiRelayFailure, OpenAiRelayHttpState, OpenAiRelayRequest,
        OpenAiRelayResult, OpenAiRelayService, openai_relay_router,
    },
};
use serde_json::{Value, json};
use tokio::net::TcpListener;
use tower::ServiceExt;

struct TestBackend;

#[async_trait]
impl RelayBackend for TestBackend {
    async fn authenticate(&self, token: &str) -> Result<RelayIdentity, RelayFailure> {
        (token == "test-token")
            .then(|| RelayIdentity {
                token_id: "test-token-id".to_owned(),
            })
            .ok_or(RelayFailure::Unauthorized)
    }

    async fn select_channel(
        &self,
        _: &RelayIdentity,
        _: RelayProtocol,
        model: &str,
    ) -> Result<RelayChannel, RelayFailure> {
        Ok(RelayChannel {
            id: 1,
            upstream_model: model.to_owned(),
        })
    }

    async fn invoke(
        &self,
        _: &RelayChannel,
        _: UpstreamRequest,
    ) -> Result<UpstreamReply, RelayFailure> {
        Ok(UpstreamReply::Sse(vec![RelaySseEvent {
            kind: Some("content_block_delta".to_owned()),
            payload: json!({"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}),
        }]))
    }

    async fn record_outcome(
        &self,
        _: Option<&RelayIdentity>,
        _: Option<&RelayChannel>,
        _: RelayOutcome,
    ) {
    }
}

fn app() -> axum::Router {
    router(RelayHttpState::new(Arc::new(TestBackend)))
}

struct SharedModelLookup;

#[async_trait]
impl ModelLookupService for SharedModelLookup {
    async fn authenticate(&self, request: ModelLookupRequest) -> Result<(), ModelsError> {
        (request.authorization.as_deref() == Some("Bearer test-token"))
            .then_some(())
            .ok_or_else(|| ModelsError::new(ModelsErrorKind::InvalidToken, "Invalid token"))
    }

    async fn find_static_model(&self, model: &str) -> Result<Option<ModelView>, ModelsError> {
        Ok((model == "gpt-4o").then(|| ModelView::new("gpt-4o", "openai")))
    }
}

fn shared_models_app(backend: Arc<CapturingBackend>) -> axum::Router {
    router_with_model_lookup(
        RelayHttpState::new(backend),
        ModelLookupState::new(Arc::new(SharedModelLookup), "v0.0.0-test"),
    )
}

struct CapturingBackend {
    selection: Result<RelayChannel, RelayFailure>,
    result: Mutex<Option<Result<UpstreamReply, RelayFailure>>>,
    requests: Mutex<Vec<UpstreamRequest>>,
}

impl CapturingBackend {
    fn successful(reply: UpstreamReply) -> Self {
        Self {
            selection: Ok(RelayChannel {
                id: 42,
                upstream_model: "mapped-model".to_owned(),
            }),
            result: Mutex::new(Some(Ok(reply))),
            requests: Mutex::new(Vec::new()),
        }
    }
}

#[async_trait]
impl RelayBackend for CapturingBackend {
    async fn authenticate(&self, token: &str) -> Result<RelayIdentity, RelayFailure> {
        (token == "test-token")
            .then(|| RelayIdentity {
                token_id: "test-token-id".to_owned(),
            })
            .ok_or(RelayFailure::Unauthorized)
    }

    async fn select_channel(
        &self,
        _: &RelayIdentity,
        _: RelayProtocol,
        _: &str,
    ) -> Result<RelayChannel, RelayFailure> {
        self.selection.clone()
    }

    async fn invoke(
        &self,
        _: &RelayChannel,
        request: UpstreamRequest,
    ) -> Result<UpstreamReply, RelayFailure> {
        self.requests.lock().expect("request lock").push(request);
        self.result
            .lock()
            .expect("result lock")
            .take()
            .unwrap_or_else(|| Ok(UpstreamReply::Json(Value::Null)))
    }

    async fn record_outcome(
        &self,
        _: Option<&RelayIdentity>,
        _: Option<&RelayChannel>,
        _: RelayOutcome,
    ) {
    }
}

fn capturing_app(backend: Arc<CapturingBackend>) -> axum::Router {
    router(RelayHttpState::new(backend))
}

struct NoChannelDeleteBackend {
    selections: AtomicUsize,
    outcomes: Mutex<Vec<RelayOutcome>>,
}

#[async_trait]
impl RelayBackend for NoChannelDeleteBackend {
    async fn authenticate(&self, token: &str) -> Result<RelayIdentity, RelayFailure> {
        (token == "test-token")
            .then(|| RelayIdentity {
                token_id: "test-token-id".to_owned(),
            })
            .ok_or(RelayFailure::Unauthorized)
    }

    async fn select_channel(
        &self,
        _: &RelayIdentity,
        _: RelayProtocol,
        _: &str,
    ) -> Result<RelayChannel, RelayFailure> {
        self.selections.fetch_add(1, Ordering::Relaxed);
        Err(RelayFailure::NoChannel)
    }

    async fn invoke(
        &self,
        _: &RelayChannel,
        _: UpstreamRequest,
    ) -> Result<UpstreamReply, RelayFailure> {
        Err(RelayFailure::Upstream)
    }

    async fn record_outcome(
        &self,
        _: Option<&RelayIdentity>,
        _: Option<&RelayChannel>,
        outcome: RelayOutcome,
    ) {
        self.outcomes.lock().expect("outcome lock").push(outcome);
    }
}

fn delete_app(backend: Arc<NoChannelDeleteBackend>) -> axum::Router {
    router(RelayHttpState::new(backend))
}

async fn spawn_tcp_router(router: axum::Router) -> (String, tokio::task::JoinHandle<()>) {
    let listener = TcpListener::bind("127.0.0.1:0").await.expect("listener");
    let address = listener.local_addr().expect("listener address");
    let task = tokio::spawn(async move {
        axum::serve(listener, router)
            .await
            .expect("test listener serves");
    });
    (format!("http://{address}"), task)
}

struct ConstructionOpenAi;

#[async_trait]
impl OpenAiRelayService for ConstructionOpenAi {
    async fn authenticate(&self, _: OpenAiRelayAuthorization) -> Result<(), OpenAiRelayFailure> {
        Ok(())
    }

    async fn relay(&self, _: OpenAiRelayRequest) -> Result<OpenAiRelayResult, OpenAiRelayFailure> {
        Err(OpenAiRelayFailure::new(
            StatusCode::BAD_GATEWAY,
            "upstream_error",
            "not invoked during router construction",
        ))
    }
}

struct ConstructionMisc;

#[async_trait]
impl RelayMiscService for ConstructionMisc {
    async fn authorize(&self, _: &AxumRequest) -> RelayAuth {
        RelayAuth::Authorized
    }

    async fn relay(
        &self,
        _: lmm_api_rs::routes::relay_misc::RelayProtocol,
        _: AxumRequest,
    ) -> Response {
        Response::new(Body::empty())
    }
}

struct ConstructionMedia;

#[async_trait]
impl RelayMediaService for ConstructionMedia {
    async fn relay(&self, _: AxumRequest) -> Response {
        Response::new(Body::empty())
    }
}

#[tokio::test]
async fn messages_should_emit_anthropic_sse_and_channel_header_when_streaming() {
    let response = app()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("authorization", "Bearer test-token")
                .header("x-request-id", "relay-contract-1")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"model":"claude-test","stream":true,"messages":[{"role":"user","content":"hello"}]}"#))
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(response.headers()["x-oneapi-channel-id"], "1");
    assert!(
        response.headers()["content-type"]
            .to_str()
            .expect("header text")
            .starts_with("text/event-stream")
    );
}

#[tokio::test]
async fn gemini_should_require_a_token_before_channel_selection() {
    let response = app()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1beta/models/gemini-test:generateContent")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"contents":[]}"#))
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), StatusCode::NOT_FOUND);
}

#[tokio::test]
async fn gemini_authentication_precedes_invalid_json_parsing() {
    let response = app()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1beta/models/gemini-test:generateContent")
                .header("content-type", "application/json")
                .body(Body::from("{"))
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), StatusCode::NOT_FOUND);
}

#[tokio::test]
async fn gemini_accepts_legacy_query_key_before_json_parsing() {
    let response = app()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1beta/models/gemini-test:generateContent?key=test-token")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"contents":[]}"#))
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), StatusCode::OK);
}

#[tokio::test]
async fn legacy_gemini_embedding_route_selects_the_path_model() {
    let response = app()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/engines/text-embedding-3-large/embeddings")
                .header("x-goog-api-key", "test-token")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"input":"hello"}"#))
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), StatusCode::OK);
}

#[tokio::test]
async fn gemini_wildcard_route_preserves_unknown_action_null_body_and_alt_sse() {
    let backend = Arc::new(CapturingBackend::successful(UpstreamReply::Json(
        Value::Null,
    )));
    let response = capturing_app(backend.clone())
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1beta/models/gemini-2.5-pro:countTokens?alt=sse&key=test-token")
                .header("content-type", "application/json")
                .body(Body::from("null"))
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(response.headers()["x-oneapi-channel-id"], "42");
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(std::str::from_utf8(&body).expect("UTF-8 body"), "null");
    let requests = backend.requests.lock().expect("request lock");
    assert_eq!(requests.len(), 1);
    assert_eq!(requests[0].protocol, RelayProtocol::Gemini);
    assert_eq!(requests[0].model, "gemini-2.5-pro");
    assert_eq!(
        requests[0].request_path,
        "/v1beta/models/gemini-2.5-pro:countTokens"
    );
    assert!(requests[0].streaming);
    assert_eq!(requests[0].body, Value::Null);
}

#[tokio::test]
async fn shared_models_method_router_preserves_lookup_gemini_and_frozen_delete_boundaries() {
    let backend = Arc::new(CapturingBackend::successful(UpstreamReply::Json(json!({}))));
    let app = shared_models_app(backend.clone());

    let get_single = app
        .clone()
        .oneshot(
            Request::get("/v1/models/gpt-4o")
                .header("authorization", "Bearer test-token")
                .body(Body::empty())
                .expect("request is valid"),
        )
        .await
        .expect("router responds");
    assert_eq!(get_single.status(), StatusCode::OK);

    let get_tail = app
        .clone()
        .oneshot(
            Request::get("/v1/models/gpt-4o/illegal")
                .header("authorization", "Bearer test-token")
                .body(Body::empty())
                .expect("request is valid"),
        )
        .await
        .expect("router responds");
    assert_eq!(get_tail.status(), StatusCode::NOT_FOUND);

    for path in [
        "/v1/models/gemini-2.5-pro:generateContent?key=test-token",
        "/v1/models/gemini-2.5-pro/actions/generateContent?key=test-token",
    ] {
        let response = app
            .clone()
            .oneshot(
                Request::post(path)
                    .header("content-type", "application/json")
                    .body(Body::from(r#"{"contents":[]}"#))
                    .expect("request is valid"),
            )
            .await
            .expect("router responds");
        assert_eq!(response.status(), StatusCode::OK, "{path}");
    }

    let legal_delete = app
        .clone()
        .oneshot(
            Request::delete("/v1/models/gpt-4o")
                .header("authorization", "Bearer test-token")
                .body(Body::empty())
                .expect("request is valid"),
        )
        .await
        .expect("router responds");
    assert_eq!(legal_delete.status(), StatusCode::NOT_IMPLEMENTED);

    let illegal_delete = app
        .oneshot(
            Request::delete("/v1/models/gpt-4o/illegal")
                .header("authorization", "Bearer test-token")
                .body(Body::empty())
                .expect("request is valid"),
        )
        .await
        .expect("router responds");
    assert_eq!(illegal_delete.status(), StatusCode::NOT_FOUND);

    let requests = backend.requests.lock().expect("request lock");
    assert_eq!(requests.len(), 2);
    assert_eq!(requests[0].model, "gemini-2.5-pro");
    assert_eq!(requests[1].model, "gemini-2.5-pro/actions/generateContent");
    assert_eq!(
        requests[1].request_path,
        "/v1/models/gemini-2.5-pro/actions/generateContent"
    );
}

#[tokio::test]
async fn anthropic_sse_should_frame_nullable_payload_and_terminal_event() {
    let backend = Arc::new(CapturingBackend::successful(UpstreamReply::Sse(vec![
        RelaySseEvent {
            kind: None,
            payload: Value::Null,
        },
    ])));
    let response = capturing_app(backend)
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("x-api-key", "test-token")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"model":"claude-test","stream":null}"#))
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), StatusCode::OK);
    assert!(
        response.headers()["content-type"]
            .to_str()
            .expect("header text")
            .starts_with("text/event-stream")
    );
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        std::str::from_utf8(&body).expect("UTF-8 body"),
        "data: null\n\ndata: [DONE]\n\n"
    );
}

#[tokio::test]
async fn native_sse_passthrough_preserves_unknown_frames_and_does_not_append_done() {
    let expected = b"event: future_event\r\ndata: first\r\ndata: second\r\n\r\n";
    let backend = Arc::new(CapturingBackend::successful(UpstreamReply::NativeSse(
        Box::new(NativeSseReply::new(
            StatusCode::CREATED,
            Body::from(expected.to_vec()),
            Some(HeaderValue::from_static(
                "text/event-stream; charset=iso-8859-1",
            )),
        )),
    )));
    let response = capturing_app(backend)
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("x-api-key", "test-token")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"model":"claude-test","stream":true}"#))
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), StatusCode::CREATED);
    assert_eq!(
        response.headers()["content-type"],
        "text/event-stream; charset=iso-8859-1"
    );
    assert_eq!(response.headers()["cache-control"], "no-cache");
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(body.as_ref(), expected);
    assert!(
        !body
            .windows(b"[DONE]".len())
            .any(|window| window == b"[DONE]")
    );
}

#[tokio::test]
async fn dropping_native_sse_response_drops_the_upstream_stream() {
    struct DropSignal(Arc<AtomicUsize>);

    impl Drop for DropSignal {
        fn drop(&mut self) {
            self.0.fetch_add(1, Ordering::Relaxed);
        }
    }

    let dropped = Arc::new(AtomicUsize::new(0));
    let guard = DropSignal(dropped.clone());
    let stream = futures_util::stream::once(async move {
        let _guard = guard;
        Ok::<Bytes, std::io::Error>(Bytes::from_static(b"data: never\n\n"))
    });
    let backend = Arc::new(CapturingBackend::successful(UpstreamReply::NativeSse(
        Box::new(NativeSseReply::new(
            StatusCode::OK,
            Body::from_stream(stream),
            None,
        )),
    )));
    let response = capturing_app(backend)
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("x-api-key", "test-token")
                .header("content-type", "application/json")
                .body(Body::from(r#"{"model":"claude-test","stream":true}"#))
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    drop(response);
    assert_eq!(dropped.load(Ordering::Relaxed), 1);
}

#[tokio::test]
async fn native_sse_body_uses_a_bounded_stream_without_prefetch_queue() {
    let (sender, receiver) = tokio::sync::mpsc::channel::<Bytes>(1);
    let stream = futures_util::stream::unfold(receiver, |mut receiver| async move {
        receiver
            .recv()
            .await
            .map(|chunk| (Ok::<Bytes, std::io::Error>(chunk), receiver))
    });
    let body = Body::from_stream(stream);
    sender
        .send(Bytes::from_static(b"data: first\n\n"))
        .await
        .expect("bounded stream receiver");
    assert!(matches!(
        sender.try_send(Bytes::from_static(b"data: second\n\n")),
        Err(tokio::sync::mpsc::error::TrySendError::Full(_))
    ));

    let reader = tokio::spawn(to_bytes(body, usize::MAX));
    tokio::time::timeout(
        std::time::Duration::from_secs(1),
        sender.send(Bytes::from_static(b"data: second\n\n")),
    )
    .await
    .expect("body should release one bounded slot")
    .expect("bounded stream receiver");
    drop(sender);
    let bytes = reader.await.expect("body reader task").expect("body bytes");
    assert_eq!(bytes.as_ref(), b"data: first\n\ndata: second\n\n");
}

#[tokio::test]
async fn gemini_upstream_failure_should_use_the_gemini_compatibility_envelope() {
    let backend = Arc::new(CapturingBackend {
        selection: Ok(RelayChannel {
            id: 42,
            upstream_model: "mapped-model".to_owned(),
        }),
        result: Mutex::new(Some(Err(RelayFailure::Upstream))),
        requests: Mutex::new(Vec::new()),
    });
    let response = capturing_app(backend)
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/models/gemini-test:generateContent?key=test-token")
                .header("content-type", "application/json")
                .body(Body::from("{}"))
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), StatusCode::BAD_GATEWAY);
    let body: Value = serde_json::from_slice(
        &to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body"),
    )
    .expect("JSON error");
    assert_eq!(
        body,
        json!({"error":{"code":502,"message":"relay request could not be completed","status":"UNAVAILABLE"}})
    );
}

#[tokio::test]
async fn delete_model_uses_the_single_segment_route_owner() {
    let response = app()
        .oneshot(
            Request::builder()
                .method("DELETE")
                .uri("/v1/models/gpt-4o")
                .header("authorization", "Bearer test-token")
                .body(Body::empty())
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), StatusCode::NOT_IMPLEMENTED);
    assert!(!response.headers().contains_key("x-oneapi-channel-id"));
}

#[tokio::test]
async fn delete_model_returns_501_without_channel_selection_or_outcome() {
    let backend = Arc::new(NoChannelDeleteBackend {
        selections: AtomicUsize::new(0),
        outcomes: Mutex::new(Vec::new()),
    });
    let response = delete_app(backend.clone())
        .oneshot(
            Request::builder()
                .method("DELETE")
                .uri("/v1/models/gpt-4o")
                .header("authorization", "Bearer test-token")
                .body(Body::empty())
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), StatusCode::NOT_IMPLEMENTED);
    assert!(!response.headers().contains_key("x-oneapi-channel-id"));
    assert_eq!(backend.selections.load(Ordering::Relaxed), 0);
    assert!(backend.outcomes.lock().expect("outcome lock").is_empty());
}

#[tokio::test]
async fn static_model_lookup_contract_holds_over_a_real_tcp_listener() {
    let backend = Arc::new(CapturingBackend::successful(UpstreamReply::Json(json!({}))));
    let (base_url, server) = spawn_tcp_router(shared_models_app(backend)).await;
    let client = reqwest::Client::new();

    let known = client
        .get(format!("{base_url}/v1/models/gpt-4o"))
        .header("authorization", "Bearer test-token")
        .send()
        .await
        .expect("known TCP response");
    assert_eq!(known.status(), reqwest::StatusCode::OK);
    assert_eq!(
        known.json::<Value>().await.expect("known JSON")["owned_by"],
        "openai"
    );

    let missing = client
        .get(format!("{base_url}/v1/models/not-in-static-map"))
        .header("authorization", "Bearer test-token")
        .send()
        .await
        .expect("missing TCP response");
    assert_eq!(missing.status(), reqwest::StatusCode::OK);
    assert_eq!(
        missing.json::<Value>().await.expect("missing JSON")["error"]["code"],
        "model_not_found"
    );

    let denied = client
        .get(format!("{base_url}/v1/models/gpt-4o"))
        .send()
        .await
        .expect("denied TCP response");
    // The frozen Go relay uses TokenAuth for this route, so a missing
    // credential is the OpenAI-compatible 401 envelope, not a generic 404.
    assert_eq!(denied.status(), reqwest::StatusCode::UNAUTHORIZED);
    let denied_body = denied.json::<Value>().await.expect("denied JSON");
    assert_eq!(denied_body["error"]["type"], "new_api_error");
    assert_eq!(denied_body["error"]["code"], "");
    let denied_message = denied_body["error"]["message"].as_str().unwrap_or_default();
    assert!(denied_message.starts_with("Invalid token (request id: "));
    server.abort();
}

#[tokio::test]
async fn delete_model_frozen_501_contract_holds_over_a_real_tcp_listener() {
    let (base_url, server) = spawn_tcp_router(app()).await;
    let response = reqwest::Client::new()
        .delete(format!("{base_url}/v1/models/gpt-4o"))
        .header("authorization", "Bearer test-token")
        .send()
        .await
        .expect("TCP response");

    assert_eq!(response.status(), reqwest::StatusCode::NOT_IMPLEMENTED);
    assert_eq!(
        response.headers()[reqwest::header::CONTENT_TYPE],
        "application/json"
    );
    let body: serde_json::Value = response.json().await.expect("JSON body");
    assert_eq!(
        body,
        json!({"error":{"message":"API not implemented","type":"new_api_error","param":"","code":"api_not_implemented"}})
    );
    server.abort();
}

#[tokio::test]
async fn delete_model_rejects_a_wildcard_tail_that_legacy_colon_model_cannot_match() {
    let response = app()
        .oneshot(
            Request::builder()
                .method("DELETE")
                .uri("/v1/models/gpt-4o/extra")
                .header("authorization", "Bearer test-token")
                .body(Body::empty())
                .expect("request is valid"),
        )
        .await
        .expect("router responds");

    assert_eq!(response.status(), StatusCode::NOT_FOUND);
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("error body");
    assert!(
        std::str::from_utf8(&body)
            .expect("UTF-8 error")
            .contains("Invalid URL (DELETE /v1/models/gpt-4o/extra)")
    );
}

#[test]
fn all_relay_routers_merge_without_an_axum_path_conflict() {
    let _router = openai_relay_router(OpenAiRelayHttpState::new(
        Arc::new(ConstructionOpenAi),
        "v0.0.0",
    ))
    .merge(router(RelayHttpState::new(Arc::new(TestBackend))))
    .merge(misc_routes(RelayMiscHttpState::new(Arc::new(
        ConstructionMisc,
    ))))
    .merge(relay_media_router(RelayMediaHttpState::new(Arc::new(
        ConstructionMedia,
    ))));
}
