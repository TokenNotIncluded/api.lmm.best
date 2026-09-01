use std::sync::{Arc, Mutex};
use std::{net::IpAddr, str::FromStr};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{Request, StatusCode, header},
};
use lmm_api_rs::RequestContext;
use lmm_api_rs::routes::relay_openai::{
    OpenAiRelayAuthorization, OpenAiRelayBody, OpenAiRelayEndpoint, OpenAiRelayFailure,
    OpenAiRelayHttpState, OpenAiRelayRequest, OpenAiRelayResult, OpenAiRelayService,
    openai_relay_router,
};
use lmm_contracts::relay::{
    CanonicalContent, CanonicalResponse, CanonicalStreamEvent, FinishReason, Protocol,
};
use serde_json::Value;
use tower::ServiceExt;

struct StubRelay {
    authorizations: Mutex<Vec<OpenAiRelayAuthorization>>,
    requests: Mutex<Vec<OpenAiRelayRequest>>,
    authorization: Mutex<Result<(), OpenAiRelayFailure>>,
    results: Mutex<Vec<Result<OpenAiRelayResult, OpenAiRelayFailure>>>,
}

#[async_trait]
impl OpenAiRelayService for StubRelay {
    async fn authenticate(
        &self,
        request: OpenAiRelayAuthorization,
    ) -> Result<(), OpenAiRelayFailure> {
        self.authorizations
            .lock()
            .expect("authorization lock")
            .push(request);
        self.authorization
            .lock()
            .expect("authorization result lock")
            .clone()
    }

    async fn relay(
        &self,
        request: OpenAiRelayRequest,
    ) -> Result<OpenAiRelayResult, OpenAiRelayFailure> {
        self.requests.lock().expect("request lock").push(request);
        self.results
            .lock()
            .expect("result lock")
            .pop()
            .expect("configured relay result")
    }
}

fn service(result: Result<OpenAiRelayResult, OpenAiRelayFailure>) -> Arc<StubRelay> {
    Arc::new(StubRelay {
        authorizations: Mutex::new(Vec::new()),
        requests: Mutex::new(Vec::new()),
        authorization: Mutex::new(Ok(())),
        results: Mutex::new(vec![result]),
    })
}

fn completed() -> OpenAiRelayResult {
    OpenAiRelayResult {
        status: StatusCode::OK,
        headers: Default::default(),
        body: OpenAiRelayBody::Complete(CanonicalResponse {
            id: "chatcmpl-1".to_owned(),
            model: "gpt-4o".to_owned(),
            created_at: 42,
            output: vec![CanonicalContent::Text {
                text: "hello".to_owned(),
            }],
            finish_reason: Some(FinishReason::Stop),
            usage: None,
        }),
    }
}

#[tokio::test]
async fn chat_route_authenticates_through_service_and_returns_legacy_headers() {
    let service = service(Ok(completed()));
    let router = openai_relay_router(OpenAiRelayHttpState::new(service.clone(), "v0.0.0"));
    let response = router
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/chat/completions")
                .header(header::AUTHORIZATION, "Bearer sk-tenant-channel")
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(
                    r#"{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}"#,
                ))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(response.headers()["x-new-api-version"], "v0.0.0");
    assert!(response.headers().contains_key("x-oneapi-request-id"));
    assert_eq!(
        json_body(response).await["choices"][0]["message"]["content"],
        "hello"
    );
    let requests = service.requests.lock().expect("request lock");
    assert_eq!(requests.len(), 1);
    assert_eq!(requests[0].protocol, Protocol::OpenAi);
    assert_eq!(requests[0].endpoint, OpenAiRelayEndpoint::ChatCompletions);
    assert_eq!(requests[0].request.model, "gpt-4o");
    assert_eq!(
        requests[0].raw_body,
        br#"{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}"#
    );
    assert!(!requests[0].request_id.is_empty());
    assert_eq!(
        requests[0].headers[header::AUTHORIZATION],
        "Bearer sk-tenant-channel"
    );
    let authorizations = service.authorizations.lock().expect("authorization lock");
    assert_eq!(authorizations.len(), 1);
    assert_eq!(
        authorizations[0].headers[header::AUTHORIZATION],
        "Bearer sk-tenant-channel"
    );
}

#[tokio::test]
async fn auth_receives_only_listener_established_client_ip() {
    let service = service(Ok(completed()));
    let router = openai_relay_router(OpenAiRelayHttpState::new(service.clone(), "v0.0.0"));
    let mut request = Request::builder()
        .method("POST")
        .uri("/v1/chat/completions")
        .header(header::AUTHORIZATION, "Bearer sk-tenant-channel")
        .header(header::CONTENT_TYPE, "application/json")
        // A relay service must never infer the client address from this
        // untrusted header itself; listener middleware establishes it once.
        .header("x-forwarded-for", "203.0.113.99")
        .body(Body::from(
            r#"{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}"#,
        ))
        .expect("request");
    request.extensions_mut().insert(RequestContext {
        request_id: "trusted-request-id".to_owned(),
        client_ip: Some(IpAddr::from_str("198.51.100.7").expect("IP")),
    });
    let response = router.oneshot(request).await.expect("response");

    assert_eq!(response.status(), StatusCode::OK);
    let authorizations = service.authorizations.lock().expect("authorization lock");
    assert_eq!(
        authorizations[0].client_ip,
        Some(IpAddr::from_str("198.51.100.7").expect("IP"))
    );
}

#[tokio::test]
async fn completions_preserves_the_original_wire_body_for_the_adapter() {
    let service = service(Ok(completed()));
    let router = openai_relay_router(OpenAiRelayHttpState::new(service.clone(), "v0.0.0"));
    let response = router
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/completions")
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(
                    r#"{"model":"gpt-4o","prompt":"write a haiku","stream":false,"suffix":"."}"#,
                ))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::OK);
    let requests = service.requests.lock().expect("request lock");
    assert_eq!(requests.len(), 1);
    assert_eq!(requests[0].endpoint, OpenAiRelayEndpoint::Completions);
    assert_eq!(requests[0].request.model, "gpt-4o");
    assert_eq!(
        requests[0].raw_body,
        br#"{"model":"gpt-4o","prompt":"write a haiku","stream":false,"suffix":"."}"#
    );
}

#[tokio::test]
async fn successful_openai_json_has_legacy_json_content_type() {
    let router = openai_relay_router(OpenAiRelayHttpState::new(
        service(Ok(completed())),
        "v0.0.0",
    ));
    let response = router
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/chat/completions")
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(
                    r#"{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}"#,
                ))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(
        response.headers()[header::CONTENT_TYPE],
        "application/json; charset=utf-8"
    );
}

#[tokio::test]
async fn responses_route_serializes_typed_stream_as_named_sse_events() {
    let service = service(Ok(OpenAiRelayResult {
        status: StatusCode::OK,
        headers: Default::default(),
        body: OpenAiRelayBody::Stream(vec![
            CanonicalStreamEvent::ResponseStart {
                id: "resp-1".to_owned(),
                model: "gpt-4o".to_owned(),
            },
            CanonicalStreamEvent::TextDelta {
                index: 0,
                delta: "hello".to_owned(),
            },
            CanonicalStreamEvent::ResponseEnd {
                finish_reason: FinishReason::Stop,
                usage: None,
                model: None,
            },
        ]),
    }));
    let router = openai_relay_router(OpenAiRelayHttpState::new(service, "v0.0.0"));
    let response = router
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/responses")
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(
                    r#"{"model":"gpt-4o","input":"hello","stream":true}"#,
                ))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        response.headers()[header::CONTENT_TYPE],
        "text/event-stream; charset=utf-8"
    );
    let body = String::from_utf8(
        to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("body")
            .to_vec(),
    )
    .expect("UTF-8 SSE");
    assert!(body.contains("event: response.created\n"));
    assert!(body.contains("event: response.output_text.delta\n"));
    assert!(body.contains("event: response.completed\n"));
    assert!(body.ends_with("data: [DONE]\n\n"));
}

#[tokio::test]
async fn invalid_token_failure_keeps_legacy_openai_error_shape() {
    let service = service(Ok(completed()));
    *service
        .authorization
        .lock()
        .expect("authorization result lock") = Err(OpenAiRelayFailure::new(
        StatusCode::UNAUTHORIZED,
        "",
        "Invalid token",
    ));
    let router = openai_relay_router(OpenAiRelayHttpState::new(service.clone(), "v0.0.0"));
    let response = router
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/responses")
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(r#"{"model":"gpt-4o","input":"hello"}"#))
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(
        response.headers()[header::CONTENT_TYPE],
        "application/json; charset=utf-8"
    );
    let body = json_body(response).await;
    assert_eq!(body["error"]["type"], "new_api_error");
    assert_eq!(body["error"]["code"], "");
    assert!(
        body["error"]["message"]
            .as_str()
            .is_some_and(|message| message.starts_with("Invalid token (request id: "))
    );
    assert!(service.requests.lock().expect("request lock").is_empty());
}

async fn json_body(response: axum::response::Response) -> Value {
    serde_json::from_slice(
        &to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("body"),
    )
    .expect("JSON")
}
