use std::{
    collections::BTreeMap,
    sync::{Arc, Mutex},
};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{HeaderMap, Method, Request, StatusCode},
    response::Response,
};
use lmm_api_rs::routes::channel_advanced::{
    ChannelAdvancedAuthorizer, ChannelAdvancedCall, ChannelAdvancedChannel, ChannelAdvancedError,
    ChannelAdvancedHttpState, ChannelAdvancedKind, ChannelAdvancedOperation,
    ChannelAdvancedPermission, ChannelAdvancedProvider, ChannelAdvancedReply, ChannelAdvancedStore,
    ChannelAdvancedUpstream, StoreBackedChannelAdvancedProvider, channel_advanced_router,
};
use secrecy::SecretString;
use serde_json::{Value, json};
use tower::ServiceExt;

#[derive(Default)]
struct Allow;

#[async_trait]
impl ChannelAdvancedAuthorizer for Allow {
    async fn authorize(
        &self,
        _: &HeaderMap,
        _: ChannelAdvancedPermission,
    ) -> Result<(), ChannelAdvancedError> {
        Ok(())
    }
}

#[derive(Default)]
struct Deny;

#[async_trait]
impl ChannelAdvancedAuthorizer for Deny {
    async fn authorize(
        &self,
        _: &HeaderMap,
        _: ChannelAdvancedPermission,
    ) -> Result<(), ChannelAdvancedError> {
        Err(ChannelAdvancedError::Unauthorized)
    }
}

struct LowPermission;

#[async_trait]
impl ChannelAdvancedAuthorizer for LowPermission {
    async fn authorize(
        &self,
        _: &HeaderMap,
        _: ChannelAdvancedPermission,
    ) -> Result<(), ChannelAdvancedError> {
        Err(ChannelAdvancedError::PermissionDenied)
    }
}

struct MemoryProvider {
    calls: Mutex<Vec<ChannelAdvancedCall>>,
    fail: bool,
}

struct MemoryStore {
    channels: BTreeMap<i64, ChannelAdvancedChannel>,
    loaded: Mutex<Vec<i64>>,
}

#[async_trait]
impl ChannelAdvancedStore for MemoryStore {
    async fn load_channel(
        &self,
        channel_id: i64,
    ) -> Result<ChannelAdvancedChannel, ChannelAdvancedError> {
        self.loaded
            .lock()
            .map_err(|_| ChannelAdvancedError::Provider)?
            .push(channel_id);
        self.channels
            .get(&channel_id)
            .cloned()
            .ok_or(ChannelAdvancedError::NotFound)
    }
}

#[derive(Default)]
struct RecordingUpstream {
    calls: Mutex<Vec<(ChannelAdvancedCall, Option<i64>)>>,
}

#[async_trait]
impl ChannelAdvancedUpstream for RecordingUpstream {
    async fn execute(
        &self,
        call: ChannelAdvancedCall,
        channel: Option<ChannelAdvancedChannel>,
    ) -> Result<Value, ChannelAdvancedError> {
        self.calls
            .lock()
            .map_err(|_| ChannelAdvancedError::Provider)?
            .push((call, channel.as_ref().map(ChannelAdvancedChannel::id)));
        Ok(json!({"from": "upstream"}))
    }
}

struct SseUpstream;

#[async_trait]
impl ChannelAdvancedUpstream for SseUpstream {
    async fn execute(
        &self,
        _: ChannelAdvancedCall,
        _: Option<ChannelAdvancedChannel>,
    ) -> Result<Value, ChannelAdvancedError> {
        Err(ChannelAdvancedError::Provider)
    }

    async fn execute_reply(
        &self,
        _: ChannelAdvancedCall,
        _: Option<ChannelAdvancedChannel>,
    ) -> Result<ChannelAdvancedReply, ChannelAdvancedError> {
        Ok(ChannelAdvancedReply::from_response(
            Response::builder()
                .status(StatusCode::OK)
                .header("content-type", "text/event-stream")
                .body(Body::from("data: progress\n\ndata: [DONE]\n\n"))
                .expect("SSE response"),
        ))
    }
}

#[async_trait]
impl ChannelAdvancedProvider for MemoryProvider {
    async fn execute(&self, call: ChannelAdvancedCall) -> Result<Value, ChannelAdvancedError> {
        self.calls
            .lock()
            .map_err(|_| ChannelAdvancedError::Provider)?
            .push(call.clone());
        if self.fail {
            return Err(ChannelAdvancedError::Provider);
        }
        Ok(
            json!({"operation": format!("{:?}", call.operation), "channel_id": call.channel_id, "input": call.input}),
        )
    }
}

fn app(
    authorizer: Arc<dyn ChannelAdvancedAuthorizer>,
    provider: Arc<dyn ChannelAdvancedProvider>,
) -> axum::Router {
    channel_advanced_router(ChannelAdvancedHttpState::new(authorizer, provider))
}

async fn call(app: axum::Router, method: Method, uri: &str, body: Body) -> Response {
    app.oneshot(
        Request::builder()
            .method(method)
            .uri(uri)
            .header("content-type", "application/json")
            .body(body)
            .expect("request"),
    )
    .await
    .expect("response")
}

async fn json_body(response: Response) -> Value {
    serde_json::from_slice(
        &to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("body"),
    )
    .expect("JSON response")
}

#[tokio::test]
async fn every_frozen_candidate_has_only_its_declared_method_and_path_shape() {
    let provider = Arc::new(MemoryProvider {
        calls: Mutex::new(Vec::new()),
        fail: false,
    });
    let candidates = [
        (Method::DELETE, "/api/channel/ollama/delete"),
        (Method::GET, "/api/channel/7/codex/usage"),
        (Method::GET, "/api/channel/7/codex/usage/reset-credits"),
        (Method::GET, "/api/channel/fetch_models/7"),
        (Method::GET, "/api/channel/ollama/version/7"),
        (Method::GET, "/api/channel/test"),
        (Method::GET, "/api/channel/test/7"),
        (Method::GET, "/api/channel/update_balance"),
        (Method::GET, "/api/channel/update_balance/7"),
        (Method::POST, "/api/channel/7/codex/refresh"),
        (Method::POST, "/api/channel/7/codex/usage/reset"),
        (Method::POST, "/api/channel/7/key"),
        (Method::POST, "/api/channel/fetch_models"),
        (Method::POST, "/api/channel/ollama/pull"),
        (Method::POST, "/api/channel/ollama/pull/stream"),
        (Method::POST, "/api/channel/upstream_updates/apply"),
        (Method::POST, "/api/channel/upstream_updates/apply_all"),
        (Method::POST, "/api/channel/upstream_updates/detect"),
        (Method::POST, "/api/channel/upstream_updates/detect_all"),
    ];
    for (method, uri) in candidates {
        let response = call(
            app(Arc::new(Deny), provider.clone()),
            method,
            uri,
            Body::from("{"),
        )
        .await;
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED, "{uri}");
    }
    assert!(provider.calls.lock().expect("calls lock").is_empty());
}

#[tokio::test]
async fn low_dashboard_permission_is_forbidden_before_provider_access() {
    let provider = Arc::new(MemoryProvider {
        calls: Mutex::new(Vec::new()),
        fail: false,
    });
    let response = call(
        app(Arc::new(LowPermission), provider.clone()),
        Method::POST,
        "/api/channel/7/key",
        Body::from("{"),
    )
    .await;
    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert_eq!(
        json_body(response).await,
        json!({
            "success": false,
            "message": "Unauthorized, insufficient privileges"
        })
    );
    assert!(provider.calls.lock().expect("calls lock").is_empty());
}

#[tokio::test]
async fn authorization_precedes_malformed_path_query_and_body_validation() {
    let provider = Arc::new(MemoryProvider {
        calls: Mutex::new(Vec::new()),
        fail: false,
    });
    let response = call(
        app(Arc::new(Deny), provider.clone()),
        Method::POST,
        "/api/channel/not-an-id/codex/refresh?%zz",
        Body::from("{"),
    )
    .await;
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert!(provider.calls.lock().expect("calls lock").is_empty());
}

#[tokio::test]
async fn successful_operation_passes_normalized_id_and_json_to_the_provider() {
    let provider = Arc::new(MemoryProvider {
        calls: Mutex::new(Vec::new()),
        fail: false,
    });
    let response = call(
        app(Arc::new(Allow), provider.clone()),
        Method::POST,
        "/api/channel/7/codex/usage/reset",
        Body::from(r#"{"scope":"hour"}"#),
    )
    .await;
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        json_body(response).await,
        json!({
            "success": true,
            "message": "",
            "data": {"operation": "CodexUsageReset", "channel_id": 7, "input": {"scope": "hour"}}
        })
    );
    assert_eq!(
        provider.calls.lock().expect("calls lock").as_slice(),
        &[ChannelAdvancedCall {
            operation: ChannelAdvancedOperation::CodexUsageReset,
            channel_id: Some(7),
            input: json!({"scope":"hour"}),
        }]
    );
}

#[tokio::test]
async fn provider_failure_is_a_legacy_safe_gateway_envelope() {
    let provider = Arc::new(MemoryProvider {
        calls: Mutex::new(Vec::new()),
        fail: true,
    });
    let response = call(
        app(Arc::new(Allow), provider),
        Method::GET,
        "/api/channel/test",
        Body::empty(),
    )
    .await;
    assert_eq!(response.status(), StatusCode::BAD_GATEWAY);
    assert_eq!(
        json_body(response).await,
        json!({"success": false, "message": "渠道上游操作失败"})
    );
}

#[tokio::test]
async fn root_channel_key_fails_closed_without_secure_verification_after_store_lookup() {
    let store = Arc::new(MemoryStore {
        channels: BTreeMap::from([(
            7,
            ChannelAdvancedChannel::new(
                7,
                ChannelAdvancedKind::Codex,
                "stored-codex".into(),
                "https://codex.example.test".into(),
                SecretString::from("stored-secret"),
                false,
            ),
        )]),
        loaded: Mutex::new(Vec::new()),
    });
    let upstream = Arc::new(RecordingUpstream::default());
    let provider = Arc::new(StoreBackedChannelAdvancedProvider::new(
        store.clone(),
        upstream.clone(),
    ));
    let response = call(
        app(Arc::new(Allow), provider),
        Method::POST,
        "/api/channel/7/key",
        Body::empty(),
    )
    .await;
    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert_eq!(
        json_body(response).await,
        json!({
            "success": false,
            "message": "Unauthorized, insufficient privileges"
        })
    );
    assert_eq!(*store.loaded.lock().expect("store loads"), vec![7]);
    assert!(upstream.calls.lock().expect("upstream calls").is_empty());
}

#[tokio::test]
async fn ollama_operation_rejects_the_wrong_persisted_channel_kind_before_upstream_access() {
    let store = Arc::new(MemoryStore {
        channels: BTreeMap::from([(
            8,
            ChannelAdvancedChannel::new(
                8,
                ChannelAdvancedKind::Codex,
                "not-ollama".into(),
                "https://codex.example.test".into(),
                SecretString::from("stored-secret"),
                false,
            ),
        )]),
        loaded: Mutex::new(Vec::new()),
    });
    let upstream = Arc::new(RecordingUpstream::default());
    let provider = Arc::new(StoreBackedChannelAdvancedProvider::new(
        store.clone(),
        upstream.clone(),
    ));
    let response = call(
        app(Arc::new(Allow), provider),
        Method::DELETE,
        "/api/channel/ollama/delete",
        Body::from(r#"{"channel_id":8,"model_name":"llama3"}"#),
    )
    .await;
    assert_eq!(response.status(), StatusCode::BAD_REQUEST);
    assert_eq!(
        json_body(response).await,
        json!({
            "success": false,
            "message": "This operation is only supported for Ollama channels"
        })
    );
    assert_eq!(*store.loaded.lock().expect("store loads"), vec![8]);
    assert!(upstream.calls.lock().expect("upstream calls").is_empty());
}

#[tokio::test]
async fn persisted_codex_channel_is_forwarded_to_the_upstream_adapter_after_store_lookup() {
    let store = Arc::new(MemoryStore {
        channels: BTreeMap::from([(
            9,
            ChannelAdvancedChannel::new(
                9,
                ChannelAdvancedKind::Codex,
                "stored-codex".into(),
                "https://codex.example.test".into(),
                SecretString::from("stored-secret"),
                false,
            ),
        )]),
        loaded: Mutex::new(Vec::new()),
    });
    let upstream = Arc::new(RecordingUpstream::default());
    let provider = Arc::new(StoreBackedChannelAdvancedProvider::new(
        store.clone(),
        upstream.clone(),
    ));
    let response = call(
        app(Arc::new(Allow), provider),
        Method::GET,
        "/api/channel/9/codex/usage",
        Body::empty(),
    )
    .await;
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(*store.loaded.lock().expect("store loads"), vec![9]);
    assert_eq!(
        upstream.calls.lock().expect("upstream calls").as_slice(),
        &[(
            ChannelAdvancedCall {
                operation: ChannelAdvancedOperation::CodexUsage,
                channel_id: Some(9),
                input: json!({}),
            },
            Some(9),
        )]
    );
}

#[tokio::test]
async fn model_preview_with_zero_channel_id_does_not_mistake_preview_data_for_a_stored_channel() {
    let store = Arc::new(MemoryStore {
        channels: BTreeMap::new(),
        loaded: Mutex::new(Vec::new()),
    });
    let upstream = Arc::new(RecordingUpstream::default());
    let provider = Arc::new(StoreBackedChannelAdvancedProvider::new(
        store.clone(),
        upstream.clone(),
    ));
    let response = call(
        app(Arc::new(Allow), provider),
        Method::POST,
        "/api/channel/fetch_models",
        Body::from(r#"{"channel_id":0,"type":1,"key":"preview-key"}"#),
    )
    .await;
    assert_eq!(response.status(), StatusCode::OK);
    assert!(store.loaded.lock().expect("store loads").is_empty());
    assert_eq!(
        upstream.calls.lock().expect("upstream calls").as_slice(),
        &[(
            ChannelAdvancedCall {
                operation: ChannelAdvancedOperation::FetchModels,
                channel_id: None,
                input: json!({"channel_id":0,"type":1,"key":"preview-key"}),
            },
            None,
        )]
    );
}

#[tokio::test]
async fn ollama_pull_stream_preserves_the_upstream_sse_response() {
    let store = Arc::new(MemoryStore {
        channels: BTreeMap::from([(
            10,
            ChannelAdvancedChannel::new(
                10,
                ChannelAdvancedKind::Ollama,
                "ollama".into(),
                "https://ollama.example.test".into(),
                SecretString::from("stored-secret"),
                false,
            ),
        )]),
        loaded: Mutex::new(Vec::new()),
    });
    let provider = Arc::new(StoreBackedChannelAdvancedProvider::new(
        store,
        Arc::new(SseUpstream),
    ));
    let response = call(
        app(Arc::new(Allow), provider),
        Method::POST,
        "/api/channel/ollama/pull/stream",
        Body::from(r#"{"channel_id":10,"model_name":"llama3"}"#),
    )
    .await;
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(response.headers()["content-type"], "text/event-stream");
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("SSE body");
    assert_eq!(body.as_ref(), b"data: progress\n\ndata: [DONE]\n\n");
}
