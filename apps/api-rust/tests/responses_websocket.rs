use std::{
    sync::{
        Arc,
        atomic::{AtomicBool, AtomicUsize, Ordering},
    },
    time::Duration,
};

use async_trait::async_trait;
use axum::{
    body::Body,
    http::{Request, StatusCode, header},
};
use futures_util::{SinkExt, StreamExt};
use lmm_api_rs::routes::responses_websocket::{
    ResponsesChannelLock, ResponsesFrame, ResponsesHandshakeFailure, ResponsesHandshakeRequest,
    ResponsesSession, ResponsesStartTurn, ResponsesStartedTurn, ResponsesTurn,
    ResponsesTurnAuthorization, ResponsesTurnFinish, ResponsesTurnObservation, ResponsesUpstream,
    ResponsesUpstreamPeer, ResponsesWebSocketFailure, ResponsesWebSocketService,
    ResponsesWebSocketState, UnconfiguredResponsesWebSocketService, router,
};
use serde_json::{Value, json};
use tokio::{net::TcpListener, sync::Mutex, task::JoinHandle};
use tokio_tungstenite::{
    connect_async,
    tungstenite::{Message, client::IntoClientRequest},
};
use tower::ServiceExt;

#[derive(Clone)]
struct TestService {
    handshake_calls: Arc<AtomicUsize>,
    authorize_calls: Arc<AtomicUsize>,
    start_calls: Arc<AtomicUsize>,
    reject_handshake: Arc<AtomicBool>,
    reject_start: Arc<AtomicBool>,
    reject_finish: Arc<AtomicBool>,
    reject_observe: Arc<AtomicBool>,
    upstream: Arc<ResponsesUpstream>,
    peer: Arc<ResponsesUpstreamPeer>,
    finishes: Arc<Mutex<Vec<ResponsesTurnFinish>>>,
    observations: Arc<Mutex<Vec<(String, Value)>>>,
    closed: Arc<Mutex<Vec<Option<String>>>>,
}

impl TestService {
    fn new() -> Self {
        let (upstream, peer) = ResponsesUpstream::channel();
        Self {
            handshake_calls: Arc::new(AtomicUsize::new(0)),
            authorize_calls: Arc::new(AtomicUsize::new(0)),
            start_calls: Arc::new(AtomicUsize::new(0)),
            reject_handshake: Arc::new(AtomicBool::new(false)),
            reject_start: Arc::new(AtomicBool::new(false)),
            reject_finish: Arc::new(AtomicBool::new(false)),
            reject_observe: Arc::new(AtomicBool::new(false)),
            upstream,
            peer: Arc::new(peer),
            finishes: Arc::new(Mutex::new(Vec::new())),
            observations: Arc::new(Mutex::new(Vec::new())),
            closed: Arc::new(Mutex::new(Vec::new())),
        }
    }
}

#[async_trait]
impl ResponsesWebSocketService for TestService {
    async fn handshake(
        &self,
        _request: &ResponsesHandshakeRequest,
    ) -> Result<ResponsesSession, ResponsesHandshakeFailure> {
        self.handshake_calls.fetch_add(1, Ordering::SeqCst);
        if self.reject_handshake.load(Ordering::SeqCst) {
            return Err(ResponsesHandshakeFailure::concealed_not_found());
        }
        Ok(ResponsesSession::new("7"))
    }

    async fn authorize_turn(
        &self,
        _request: &ResponsesHandshakeRequest,
        session: &ResponsesSession,
        _model: &str,
        locked_channel: Option<&ResponsesChannelLock>,
    ) -> Result<ResponsesTurnAuthorization, ResponsesWebSocketFailure> {
        self.authorize_calls.fetch_add(1, Ordering::SeqCst);
        Ok(ResponsesTurnAuthorization {
            session: session.clone(),
            locked_channel: locked_channel.cloned(),
        })
    }

    async fn start_turn(
        &self,
        request: ResponsesStartTurn,
    ) -> Result<ResponsesStartedTurn, ResponsesWebSocketFailure> {
        assert!(request.create.request.get("stream_id").is_none());
        assert!(request.create.request.get("event_id").is_none());
        let index = self.start_calls.fetch_add(1, Ordering::SeqCst) + 1;
        if self.reject_start.load(Ordering::SeqCst) {
            return Err(ResponsesWebSocketFailure::new(
                StatusCode::BAD_GATEWAY,
                "do_request_failed",
                "provider unavailable",
            ));
        }
        self.upstream
            .send(ResponsesFrame::Text(
                serde_json::to_string(&request.create.outbound_event).unwrap(),
            ))
            .await
            .unwrap();
        Ok(ResponsesStartedTurn {
            turn: ResponsesTurn {
                id: format!("turn-{index}"),
            },
            channel: ResponsesChannelLock {
                id: 42,
                channel_type: 1,
            },
            upstream: Arc::clone(&self.upstream),
        })
    }

    async fn observe_upstream(
        &self,
        turn: &ResponsesTurn,
        frame: &ResponsesFrame,
    ) -> Result<ResponsesTurnObservation, ResponsesWebSocketFailure> {
        let value: Value = serde_json::from_slice(frame.payload()).unwrap_or_default();
        self.observations
            .lock()
            .await
            .push((turn.id.clone(), value.clone()));
        if self.reject_observe.load(Ordering::SeqCst) {
            return Err(ResponsesWebSocketFailure::new(
                StatusCode::BAD_GATEWAY,
                "bad_response",
                "invalid provider event",
            ));
        }
        Ok(match value.get("type").and_then(Value::as_str) {
            Some("response.completed" | "response.done") => ResponsesTurnObservation::Terminal {
                success: true,
                billable_partial: false,
            },
            Some("response.failed" | "error") => ResponsesTurnObservation::Terminal {
                success: false,
                billable_partial: value["billable_partial"].as_bool().unwrap_or(false),
            },
            _ => ResponsesTurnObservation::Continue,
        })
    }

    async fn finish_turn(
        &self,
        _turn: ResponsesTurn,
        finish: ResponsesTurnFinish,
    ) -> Result<(), ResponsesWebSocketFailure> {
        if self.reject_finish.load(Ordering::SeqCst) {
            return Err(ResponsesWebSocketFailure::new(
                StatusCode::INTERNAL_SERVER_ERROR,
                "billing_failed",
                "settlement unavailable",
            ));
        }
        self.finishes.lock().await.push(finish);
        Ok(())
    }

    async fn session_closed(
        &self,
        _session: &ResponsesSession,
        unfinished_turn: Option<ResponsesTurn>,
    ) {
        self.closed
            .lock()
            .await
            .push(unfinished_turn.map(|turn| turn.id));
    }
}

#[tokio::test]
async fn unconfigured_transport_remains_fail_closed_without_stream_identity() {
    let response = router(ResponsesWebSocketState::new(Arc::new(
        UnconfiguredResponsesWebSocketService,
    )))
    .oneshot(
        Request::builder()
            .uri("/v1/responses")
            .body(Body::empty())
            .unwrap(),
    )
    .await
    .unwrap();
    assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
    let bytes = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .unwrap();
    let error: Value = serde_json::from_slice(&bytes).unwrap();
    assert!(error.get("stream_id").is_none());
}

#[tokio::test]
async fn token_gate_runs_before_axum_validates_the_upgrade() {
    let service = TestService::new();
    service.reject_handshake.store(true, Ordering::SeqCst);
    let response = router(ResponsesWebSocketState::new(Arc::new(service.clone())))
        .oneshot(
            Request::builder()
                .uri("/v1/responses")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();

    assert_eq!(response.status(), StatusCode::NOT_FOUND);
    assert_eq!(service.handshake_calls.load(Ordering::SeqCst), 1);
    assert_eq!(service.authorize_calls.load(Ordering::SeqCst), 0);
}

#[tokio::test]
async fn valid_auth_followed_by_a_malformed_upgrade_releases_the_session() {
    let service = TestService::new();
    let response = router(ResponsesWebSocketState::new(Arc::new(service.clone())))
        .oneshot(
            Request::builder()
                .uri("/v1/responses")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();

    assert_eq!(response.status(), StatusCode::BAD_REQUEST);
    assert_eq!(service.closed.lock().await.as_slice(), &[None]);
}

#[tokio::test]
async fn first_create_locks_model_and_channel_and_settles_before_terminal_forward() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;

    socket
        .send(Message::Text(
            json!({"type":"input_audio_buffer.append","event_id":"before"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let error = receive_json(&mut socket).await;
    assert_eq!(error["status"], 400);
    assert_eq!(error["event_id"], "before");
    assert_eq!(service.authorize_calls.load(Ordering::SeqCst), 0);

    socket
        .send(Message::Text(
            json!({
                "type":"response.create",
                "event_id":"first",
                "model":"gpt-5.6-sol",
                "input":"hello",
                "stream":true,
                "background":true
            })
            .to_string()
            .into(),
        ))
        .await
        .unwrap();
    let forwarded = peer_json(&service.peer).await;
    assert_eq!(forwarded["type"], "response.create");
    assert_eq!(forwarded["model"], "gpt-5.6-sol");
    assert!(forwarded.get("event_id").is_none());
    assert!(forwarded.get("stream").is_none());
    assert!(forwarded.get("background").is_none());
    assert_eq!(service.authorize_calls.load(Ordering::SeqCst), 1);

    service
        .peer
        .send(ResponsesFrame::Text(
            json!({"type":"response.output_text.delta","delta":"ok"}).to_string(),
        ))
        .await
        .unwrap();
    assert_eq!(receive_json(&mut socket).await["delta"], "ok");

    socket
        .send(Message::Text(
            json!({"type":"response.create","event_id":"busy","model":"gpt-5.6-sol"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let conflict = receive_json(&mut socket).await;
    assert_eq!(conflict["status"], 409);
    assert_eq!(service.authorize_calls.load(Ordering::SeqCst), 2);
    assert_eq!(service.start_calls.load(Ordering::SeqCst), 1);

    service
        .peer
        .send(ResponsesFrame::Text(
            json!({"type":"response.completed","response":{"status":"completed"}}).to_string(),
        ))
        .await
        .unwrap();
    assert_eq!(
        receive_json(&mut socket).await["type"],
        "response.completed"
    );
    assert_eq!(
        service.finishes.lock().await.as_slice(),
        &[ResponsesTurnFinish::Terminal {
            success: true,
            billable_partial: false
        }]
    );

    socket
        .send(Message::Text(
            json!({"type":"response.create","event_id":"changed","model":"gpt-5.6-luna"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let locked = receive_json(&mut socket).await;
    assert_eq!(locked["status"], 400);
    assert_eq!(service.authorize_calls.load(Ordering::SeqCst), 3);
    assert_eq!(service.start_calls.load(Ordering::SeqCst), 1);

    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn provider_or_billing_start_failure_is_an_error_never_a_fake_completion() {
    let service = TestService::new();
    service.reject_start.store(true, Ordering::SeqCst);
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;

    socket
        .send(Message::Text(
            json!({"type":"response.create","event_id":"failed","stream_id":"planner","model":"gpt-5.6-sol"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let failure = receive_json(&mut socket).await;
    assert_eq!(failure["type"], "error");
    assert_eq!(failure["status"], 502);
    assert_eq!(failure["stream_id"], "planner");
    assert_eq!(failure["error"]["code"], "do_request_failed");
    assert!(service.finishes.lock().await.is_empty());
    assert_eq!(service.start_calls.load(Ordering::SeqCst), 1);

    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn failed_write_to_a_locked_upstream_ends_the_session_before_another_create() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;

    socket
        .send(Message::Text(
            json!({"type":"response.create","model":"gpt-5.6-sol"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let _ = peer_json(&service.peer).await;
    service
        .peer
        .send(ResponsesFrame::Text(
            json!({"type":"response.completed","response":{"status":"completed"}}).to_string(),
        ))
        .await
        .unwrap();
    assert_eq!(
        receive_json(&mut socket).await["type"],
        "response.completed"
    );

    service.peer.reject_gateway_writes().await;
    socket
        .send(Message::Text(
            json!({"type":"input_audio_buffer.append"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let error = receive_json(&mut socket).await;
    assert_eq!(error["error"]["code"], "bad_response");
    wait_for_closed(&service).await;

    let _ = socket
        .send(Message::Text(
            json!({"type":"response.create","model":"gpt-5.6-sol"})
                .to_string()
                .into(),
        ))
        .await;
    tokio::time::sleep(Duration::from_millis(20)).await;
    assert_eq!(service.start_calls.load(Ordering::SeqCst), 1);

    server.abort();
}

#[tokio::test]
async fn failed_terminal_settlement_is_handed_to_session_reconciliation() {
    let service = TestService::new();
    service.reject_finish.store(true, Ordering::SeqCst);
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;

    socket
        .send(Message::Text(
            json!({"type":"response.create","model":"gpt-5.6-sol","stream_id":"planner"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let _ = peer_json(&service.peer).await;
    service
        .peer
        .send(ResponsesFrame::Text(
            json!({"type":"response.completed","response":{"status":"completed"}}).to_string(),
        ))
        .await
        .unwrap();

    let error = receive_json(&mut socket).await;
    assert_eq!(error["error"]["code"], "billing_failed");
    assert_eq!(error["stream_id"], "planner");
    tokio::time::timeout(Duration::from_secs(2), async {
        loop {
            if service
                .closed
                .lock()
                .await
                .iter()
                .any(|turn| turn.as_deref() == Some("turn-1"))
            {
                break;
            }
            tokio::task::yield_now().await;
        }
    })
    .await
    .expect("unfinished turn was not handed to reconciliation");

    server.abort();
}

#[tokio::test]
async fn malformed_create_preserves_stream_identity() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    socket
        .send(Message::Text(
            json!({"type":"response.create","event_id":"bad-create","stream_id":"planner"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let error = receive_json(&mut socket).await;
    assert_eq!(error["stream_id"], "planner");
    assert_eq!(error["event_id"], "bad-create");
    assert_eq!(service.start_calls.load(Ordering::SeqCst), 0);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn stream_identity_is_envelope_only_and_does_not_leak_between_turns() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    for stream_id in [Some("planner"), Some("writer"), None] {
        let mut create = json!({"type":"response.create","model":"gpt-5.6-sol"});
        let event_id = stream_id.map(|id| format!("{id}-create"));
        if let Some(id) = stream_id {
            create["stream_id"] = json!(id);
            create["event_id"] = json!(event_id);
        }
        if stream_id == Some("writer") {
            create = json!({"type":"response.create","event_id":event_id,"stream_id":"writer","response":{"model":"gpt-5.6-sol","event_id":"must-not-leak","stream_id":"must-not-leak"}});
        } else if stream_id.is_none() {
            create = json!({"type":"response.create","response":{"model":"gpt-5.6-sol","event_id":"nested-only"}});
        }
        socket
            .send(Message::Text(create.to_string().into()))
            .await
            .unwrap();
        let forwarded = peer_json(&service.peer).await;
        assert_eq!(
            forwarded.get("stream_id").and_then(Value::as_str),
            stream_id
        );
        assert!(forwarded.get("event_id").is_none());
        service.peer.send(ResponsesFrame::Text(json!({"type":"response.output_text.delta","stream_id":"provider-explicit","delta":"original"}).to_string())).await.unwrap();
        let explicit = receive_json(&mut socket).await;
        assert_eq!(explicit["stream_id"], "provider-explicit");
        for kind in ["response.created", "response.completed"] {
            service
                .peer
                .send(ResponsesFrame::Text(json!({"type":kind}).to_string()))
                .await
                .unwrap();
            let event = receive_json(&mut socket).await;
            assert_eq!(event.get("stream_id").and_then(Value::as_str), stream_id);
        }
        socket
            .send(Message::Text(
                json!({"type":"response.cancel","event_id":"idle"})
                    .to_string()
                    .into(),
            ))
            .await
            .unwrap();
        let idle = receive_json(&mut socket).await;
        assert!(idle.get("stream_id").is_none());
    }
    assert_eq!(service.start_calls.load(Ordering::SeqCst), 3);
    assert_eq!(service.finishes.lock().await.len(), 3);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn overlap_and_cancellation_do_not_replace_active_stream_identity() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    socket
        .send(Message::Text(
            json!({"type":"response.create","model":"gpt-5.6-sol","stream_id":"planner"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let _ = peer_json(&service.peer).await;
    socket.send(Message::Text(json!({"type":"response.create","model":"gpt-5.6-sol","stream_id":"rejected","event_id":"overlap"}).to_string().into())).await.unwrap();
    let conflict = receive_json(&mut socket).await;
    assert_eq!(conflict["status"], 409);
    assert_eq!(conflict["stream_id"], "rejected");
    assert_eq!(conflict["event_id"], "overlap");
    socket
        .send(Message::Text(
            json!({"type":"response.cancel","stream_id":"rejected","event_id":"wrong-cancel"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let rejected = receive_json(&mut socket).await;
    assert_eq!(rejected["stream_id"], "rejected");
    assert_eq!(rejected["event_id"], "wrong-cancel");
    assert_eq!(rejected["status"], 400);
    assert!(service.finishes.lock().await.is_empty());
    socket
        .send(Message::Text(
            json!({"type":"response.cancel","stream_id":"planner","event_id":"cancel"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let cancel = peer_json(&service.peer).await;
    assert_eq!(cancel["event_id"], "cancel");
    service
        .peer
        .send(ResponsesFrame::Binary(
            json!({"type":"error","error":{"code":"cancelled"}})
                .to_string()
                .into_bytes(),
        ))
        .await
        .unwrap();
    let Message::Binary(bytes) = tokio::time::timeout(Duration::from_secs(2), socket.next())
        .await
        .unwrap()
        .unwrap()
        .unwrap()
    else {
        panic!("expected binary upstream error");
    };
    let active: Value = serde_json::from_slice(&bytes).unwrap();
    assert_eq!(active["stream_id"], "planner");
    assert_eq!(service.start_calls.load(Ordering::SeqCst), 1);
    assert_eq!(service.finishes.lock().await.len(), 1);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn malformed_metadata_uses_only_valid_incoming_identity() {
    let service = TestService::new();
    let (url, server) = spawn(service).await;
    let mut socket = connect(&url).await;
    for envelope in [
        json!({"stream_id":"planner","event_id":"bad"}),
        json!({"type":123,"stream_id":"planner","event_id":"bad"}),
        json!({"type":"response.create","response":[],"stream_id":"planner","event_id":"bad"}),
    ] {
        socket
            .send(Message::Text(envelope.to_string().into()))
            .await
            .unwrap();
        let error = receive_json(&mut socket).await;
        assert_eq!(error["stream_id"], "planner");
        assert_eq!(error["event_id"], "bad");
        assert_eq!(error["status"], 400);
    }
    socket
        .send(Message::Text(
            json!({"type":"response.create","model":"gpt-5.6-sol","stream_id":123})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    assert!(receive_json(&mut socket).await.get("stream_id").is_none());
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn active_observer_error_retains_identity_after_settlement() {
    let service = TestService::new();
    service.reject_observe.store(true, Ordering::SeqCst);
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    socket
        .send(Message::Text(
            json!({"type":"response.create","model":"gpt-5.6-sol","stream_id":"planner"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let _ = peer_json(&service.peer).await;
    service
        .peer
        .send(ResponsesFrame::Text(
            json!({"type":"response.created"}).to_string(),
        ))
        .await
        .unwrap();
    assert_eq!(receive_json(&mut socket).await["stream_id"], "planner");
    wait_for_closed(&service).await;
    assert_eq!(
        service.finishes.lock().await.as_slice(),
        &[ResponsesTurnFinish::UpstreamClosed]
    );
    server.abort();
}

#[tokio::test]
async fn control_write_error_uses_control_identity_and_settles_once() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    socket
        .send(Message::Text(
            json!({"type":"response.create","model":"gpt-5.6-sol","stream_id":"planner"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let _ = peer_json(&service.peer).await;
    service.peer.reject_gateway_writes().await;
    socket
        .send(Message::Text(
            json!({"type":"input_audio_buffer.append","stream_id":"control","event_id":"append"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let error = receive_json(&mut socket).await;
    assert_eq!(error["stream_id"], "control");
    assert_eq!(error["event_id"], "append");
    wait_for_closed(&service).await;
    assert_eq!(
        service.finishes.lock().await.as_slice(),
        &[ResponsesTurnFinish::UpstreamWriteFailed]
    );
    server.abort();
}

#[tokio::test]
async fn cancellation_without_identity_keeps_legacy_active_turn_behavior() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    socket
        .send(Message::Text(
            json!({"type":"response.create","model":"gpt-5.6-sol","stream_id":"planner"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let _ = peer_json(&service.peer).await;
    socket
        .send(Message::Text(
            json!({"type":"response.cancel","event_id":"legacy"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let cancel = peer_json(&service.peer).await;
    assert_eq!(cancel["event_id"], "legacy");
    assert!(cancel.get("stream_id").is_none());
    assert!(service.finishes.lock().await.is_empty());
    service
        .peer
        .send(ResponsesFrame::Text(
            json!({"type":"response.failed"}).to_string(),
        ))
        .await
        .unwrap();
    assert_eq!(receive_json(&mut socket).await["stream_id"], "planner");
    assert_eq!(service.finishes.lock().await.len(), 1);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn asynchronous_control_errors_keep_incoming_identity_without_observing_active_turn() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    start_stream(&mut socket, &service.peer, "planner").await;
    service.reject_observe.store(true, Ordering::SeqCst);
    for (index, identity) in [Some("control"), None].into_iter().enumerate() {
        let event_id = format!("append-{index}");
        let mut control = json!({"type":"input_audio_buffer.append","event_id":event_id});
        if let Some(identity) = identity {
            control["stream_id"] = json!(identity);
        }
        socket
            .send(Message::Text(control.to_string().into()))
            .await
            .unwrap();
        assert_eq!(peer_json(&service.peer).await, control);
        let error = if index == 0 {
            json!({"type":"error","event_id":event_id,"error":{"code":"invalid_audio"}})
        } else {
            json!({"type":"error","event_id":"provider-event","error":{"event_id":event_id,"code":"invalid_audio"}})
        };
        service
            .peer
            .send(ResponsesFrame::Binary(error.to_string().into_bytes()))
            .await
            .unwrap();
        let Message::Binary(bytes) = tokio::time::timeout(Duration::from_secs(2), socket.next())
            .await
            .unwrap()
            .unwrap()
            .unwrap()
        else {
            panic!("expected binary control error");
        };
        let returned: Value = serde_json::from_slice(&bytes).unwrap();
        assert_eq!(returned.get("stream_id").and_then(Value::as_str), identity);
        assert_eq!(returned["error"], error["error"]);
        assert_eq!(returned["event_id"], error["event_id"]);
    }
    assert!(service.observations.lock().await.is_empty());
    assert!(service.finishes.lock().await.is_empty());
    service.reject_observe.store(false, Ordering::SeqCst);
    assert_eq!(
        provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"response.completed"})
        )
        .await["stream_id"],
        "planner"
    );
    assert_eq!(service.finishes.lock().await.len(), 1);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn late_and_repeated_control_errors_cannot_settle_a_subsequent_turn() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    start_stream(&mut socket, &service.peer, "planner").await;
    socket.send(Message::Text(json!({"type":"input_audio_buffer.append","event_id":"old-append","stream_id":"old-control"}).to_string().into())).await.unwrap();
    let _ = peer_json(&service.peer).await;
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.completed"}),
    )
    .await;
    start_stream(&mut socket, &service.peer, "writer").await;
    let observations = service.observations.lock().await.len();
    for _ in 0..2 {
        let error = provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"error","event_id":"old-append","error":{"code":"invalid_audio"}}),
        )
        .await;
        assert_eq!(error["stream_id"], "old-control");
    }
    assert_eq!(service.observations.lock().await.len(), observations);
    assert_eq!(service.finishes.lock().await.len(), 1);
    assert_eq!(
        provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"response.completed"})
        )
        .await["stream_id"],
        "writer"
    );
    assert_eq!(service.finishes.lock().await.len(), 2);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn cancel_operation_errors_match_target_or_unique_rejection_without_finishing_generation() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    start_stream(&mut socket, &service.peer, "planner").await;
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.created","response":{"id":"active"}}),
    )
    .await;
    socket
        .send(Message::Text(
            json!({"type":"response.cancel","response_id":"missing","stream_id":"planner"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let _ = peer_json(&service.peer).await;
    assert_eq!(
        provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"error","response_id":"missing","error":{"code":"target_rejected"}})
        )
        .await["stream_id"],
        "planner"
    );
    socket
        .send(Message::Text(
            json!({"type":"response.cancel"}).to_string().into(),
        ))
        .await
        .unwrap();
    let _ = peer_json(&service.peer).await;
    let rejection = provider_event(&mut socket, &service.peer, json!({"type":"error","error":{"type":"invalid_request_error","code":"response_not_active"}})).await;
    assert!(rejection.get("stream_id").is_none());
    socket.send(Message::Text(json!({"type":"response.cancel","event_id":"target-check","response_id":"expected","stream_id":"planner"}).to_string().into())).await.unwrap();
    let _ = peer_json(&service.peer).await;
    let mismatched = json!({"type":"error","response_id":"different","error":{"type":"invalid_request_error","code":"response_not_active"}});
    assert_eq!(
        provider_event(&mut socket, &service.peer, mismatched.clone()).await,
        mismatched
    );
    assert_eq!(
        provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"error","error":{"event_id":"target-check"}})
        )
        .await["stream_id"],
        "planner"
    );
    assert_eq!(service.observations.lock().await.len(), 1);
    assert!(service.finishes.lock().await.is_empty());
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.completed","response":{"id":"active"}}),
    )
    .await;
    assert_eq!(service.finishes.lock().await.len(), 1);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn foreign_events_and_unknown_nested_references_bypass_active_observation() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    start_stream(&mut socket, &service.peer, "planner").await;
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.created","response":{"id":"previous"}}),
    )
    .await;
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.completed","response":{"id":"previous"}}),
    )
    .await;
    start_stream(&mut socket, &service.peer, "planner").await;
    let before = service.observations.lock().await.len();
    for event in [
        json!({"type":"response.failed","stream_id":"other","response":{"id":"foreign"}}),
        json!({"type":"response.failed","stream_id":123}),
        json!({"type":"error","stream_id":{}}),
        json!({"type":"error","stream_id":[]}),
        json!({"type":"error","stream_id":"other","error":{"code":"late"}}),
        json!({"type":"error","event_id":"provider-event","error":{"event_id":"unknown","code":"rejected"}}),
        json!({"type":"response.completed","response":{"id":"previous"}}),
        json!({"type":"error","response_id":"previous","error":{"code":"late"}}),
    ] {
        assert_eq!(
            provider_event(&mut socket, &service.peer, event.clone()).await,
            event
        );
    }
    assert_eq!(service.observations.lock().await.len(), before);
    assert_eq!(service.finishes.lock().await.len(), 1);
    // A provider-generated top-level event ID alone retains legacy active-error behavior.
    assert_eq!(
        provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"error","event_id":"provider-event","error":{"code":"server_error"}})
        )
        .await["stream_id"],
        "planner"
    );
    assert_eq!(service.finishes.lock().await.len(), 2);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn ambiguous_control_references_never_choose_or_settle_an_active_turn() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    start_stream(&mut socket, &service.peer, "planner").await;
    for event_id in ["first", "second"] {
        socket.send(Message::Text(json!({"type":"input_audio_buffer.append","event_id":event_id,"stream_id":event_id}).to_string().into())).await.unwrap();
        let _ = peer_json(&service.peer).await;
    }
    let ambiguous = json!({"type":"error","event_id":"first","error":{"event_id":"second","code":"invalid_audio"}});
    assert_eq!(
        provider_event(&mut socket, &service.peer, ambiguous.clone()).await,
        ambiguous
    );
    assert!(service.observations.lock().await.is_empty());
    assert!(service.finishes.lock().await.is_empty());
    assert_eq!(
        provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"error","error":{"event_id":"first"}})
        )
        .await["stream_id"],
        "first"
    );
    // A reference conflict remains ambiguous across pending and resolved entries.
    assert_eq!(
        provider_event(&mut socket, &service.peer, ambiguous.clone()).await,
        ambiguous
    );
    assert_eq!(
        provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"error","error":{"event_id":"second"}})
        )
        .await["stream_id"],
        "second"
    );
    assert!(service.observations.lock().await.is_empty());
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.completed"}),
    )
    .await;
    assert_eq!(service.finishes.lock().await.len(), 1);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn active_create_rejections_settle_the_turn_before_pending_cancel_inference() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;

    for (index, nested_reference) in [true, false].into_iter().enumerate() {
        let create_id = format!("create-{index}");
        socket
            .send(Message::Text(
                json!({"type":"response.create","event_id":create_id,"model":"gpt-5.6-sol","stream_id":"planner"})
                    .to_string()
                    .into(),
            ))
            .await
            .unwrap();
        let forwarded = peer_json(&service.peer).await;
        assert_eq!(forwarded["type"], "response.create");
        assert_eq!(forwarded["model"], "gpt-5.6-sol");
        assert_eq!(forwarded["stream_id"], "planner");
        assert!(forwarded.get("event_id").is_none());
        assert_eq!(service.start_calls.load(Ordering::SeqCst), index + 1);
        socket
            .send(Message::Text(
                json!({"type":"response.cancel","event_id":format!("cancel-{index}"),"response_id":"missing"})
                    .to_string()
                    .into(),
            ))
            .await
            .unwrap();
        let _ = peer_json(&service.peer).await;

        let mut error = json!({"type":"error","error":{"type":"invalid_request_error","code":"response_not_found"}});
        if nested_reference {
            error["event_id"] = json!("provider-event");
            error["error"]["event_id"] = json!(create_id);
        } else {
            error["event_id"] = json!(create_id);
        }
        assert_eq!(
            provider_event(&mut socket, &service.peer, error).await["stream_id"],
            "planner"
        );
        assert_eq!(service.observations.lock().await.len(), index + 1);
        assert_eq!(
            service.finishes.lock().await.as_slice(),
            vec![
                ResponsesTurnFinish::Terminal {
                    success: false,
                    billable_partial: false,
                };
                index + 1
            ]
        );
    }

    start_stream(&mut socket, &service.peer, "writer").await;
    for _ in 0..2 {
        let late = json!({"type":"error","error":{"event_id":"create-0","code":"invalid_request"}});
        assert_eq!(
            provider_event(&mut socket, &service.peer, late.clone()).await,
            late
        );
    }
    assert_eq!(service.observations.lock().await.len(), 2);
    assert_eq!(service.finishes.lock().await.len(), 2);
    assert_eq!(
        provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"response.completed"})
        )
        .await["stream_id"],
        "writer"
    );
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn conflicting_create_and_control_references_keep_pending_controls_intact() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    socket
        .send(Message::Text(
            json!({"type":"response.create","event_id":"create","model":"gpt-5.6-sol","stream_id":"planner"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let _ = peer_json(&service.peer).await;
    for index in 0..32 {
        socket
            .send(Message::Text(
                json!({"type":"input_audio_buffer.append","event_id":format!("control-{index}"),"stream_id":"control"})
                    .to_string()
                    .into(),
            ))
            .await
            .unwrap();
        let _ = peer_json(&service.peer).await;
    }
    for (event_id, reference) in [
        ("control-0", "unknown"),
        ("control-0", "create"),
        ("create", "control-0"),
        ("control-0", "control-1"),
    ] {
        let conflicting = json!({"type":"error","event_id":event_id,"error":{"event_id":reference,"code":"invalid_audio"}});
        assert_eq!(
            provider_event(&mut socket, &service.peer, conflicting.clone()).await,
            conflicting
        );
    }
    assert!(service.observations.lock().await.is_empty());
    assert!(service.finishes.lock().await.is_empty());
    socket
        .send(Message::Text(
            json!({"type":"input_audio_buffer.append","event_id":"overflow","stream_id":"rejected"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let rejected = receive_json(&mut socket).await;
    assert_eq!(rejected["event_id"], "overflow");
    assert_eq!(rejected["stream_id"], "rejected");
    assert_eq!(rejected["status"], 400);
    assert_eq!(
        provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"error","event_id":"provider-event","error":{"event_id":"control-0"}})
        )
        .await["stream_id"],
        "control"
    );
    socket
        .send(Message::Text(
            json!({"type":"input_audio_buffer.append","event_id":"replacement"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    assert_eq!(peer_json(&service.peer).await["event_id"], "replacement");
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.completed"}),
    )
    .await;
    assert_eq!(service.finishes.lock().await.len(), 1);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn empty_response_id_uses_nested_identity_for_current_and_late_events() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    start_stream(&mut socket, &service.peer, "previous").await;
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.completed","response":{"id":"previous-response"}}),
    )
    .await;
    start_stream(&mut socket, &service.peer, "current").await;
    assert_eq!(
        provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"response.created","response_id":"","response":{"id":"current-response"}})
        )
        .await["stream_id"],
        "current"
    );
    let before = service.observations.lock().await.len();
    for response_id in ["previous-response", "foreign-response"] {
        let late =
            json!({"type":"response.completed","response_id":"","response":{"id":response_id}});
        assert_eq!(
            provider_event(&mut socket, &service.peer, late.clone()).await,
            late
        );
    }
    assert_eq!(service.observations.lock().await.len(), before);
    assert_eq!(service.finishes.lock().await.len(), 1);
    assert_eq!(
        provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"response.completed","response_id":"","response":{"id":"current-response"}})
        )
        .await["stream_id"],
        "current"
    );
    assert_eq!(service.finishes.lock().await.len(), 2);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn completed_create_errors_and_reused_ids_do_not_affect_a_new_turn() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    start_identified_stream(&mut socket, &service.peer, "previous-create", "shared").await;
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.completed"}),
    )
    .await;
    start_identified_stream(&mut socket, &service.peer, "current-create", "shared").await;
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.created","response":{"id":"current-response"}}),
    )
    .await;
    socket
        .send(Message::Text(
            json!({"type":"response.cancel","event_id":"cancel","stream_id":"shared"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let _ = peer_json(&service.peer).await;
    let observations = service.observations.lock().await.len();
    for late in [
        json!({"type":"error","event_id":"previous-create","error":{"code":"server_error"}}),
        json!({"type":"error","event_id":"previous-create","stream_id":"shared","error":{"type":"invalid_request_error","code":"response_not_found"}}),
        json!({"type":"error","stream_id":"shared","response_id":"current-response","error":{"event_id":"previous-create","code":"server_error"}}),
    ] {
        for _ in 0..2 {
            assert_eq!(
                provider_event(&mut socket, &service.peer, late.clone()).await,
                late
            );
        }
    }
    assert_eq!(service.observations.lock().await.len(), observations);
    assert_eq!(service.finishes.lock().await.len(), 1);
    socket
        .send(Message::Text(
            json!({"type":"input_audio_buffer.append","event_id":"previous-create","stream_id":"rejected"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let rejected = receive_json(&mut socket).await;
    assert_eq!(rejected["status"], 400);
    assert_eq!(rejected["stream_id"], "rejected");
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.completed","response":{"id":"current-response"}}),
    )
    .await;
    socket
        .send(Message::Text(
            json!({"type":"response.create","event_id":"previous-create","model":"gpt-5.6-sol","stream_id":"rejected"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    assert_eq!(receive_json(&mut socket).await["status"], 400);
    assert_eq!(service.start_calls.load(Ordering::SeqCst), 2);
    start_identified_stream(&mut socket, &service.peer, "next-create", "next").await;
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.completed"}),
    )
    .await;
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn current_response_id_or_distinct_stream_overrides_completed_create_top_level_id() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    start_identified_stream(&mut socket, &service.peer, "previous-create", "shared").await;
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.completed"}),
    )
    .await;
    for (index, stream_id) in ["distinct", "shared"].into_iter().enumerate() {
        start_identified_stream(
            &mut socket,
            &service.peer,
            &format!("current-create-{index}"),
            stream_id,
        )
        .await;
        let _ = provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"response.created","response":{"id":format!("current-response-{index}")}}),
        )
        .await;
        socket
            .send(Message::Text(
                json!({"type":"response.cancel","event_id":format!("cancel-{index}"),"stream_id":stream_id})
                    .to_string()
                    .into(),
            ))
            .await
            .unwrap();
        let _ = peer_json(&service.peer).await;
        let mut error = json!({"type":"error","event_id":"previous-create","error":{"type":"invalid_request_error","code":"response_not_found"}});
        if index == 0 {
            error["stream_id"] = json!(stream_id);
        } else {
            error["response_id"] = json!(format!("current-response-{index}"));
        }
        assert_eq!(
            provider_event(&mut socket, &service.peer, error).await["stream_id"],
            stream_id
        );
        assert_eq!(service.finishes.lock().await.len(), index + 2);
    }
    assert_eq!(service.observations.lock().await.len(), 5);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn completed_create_history_allows_identity_reuse_after_count_eviction() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    for index in 0..33 {
        start_identified_stream(
            &mut socket,
            &service.peer,
            &format!("create-{index}"),
            "shared",
        )
        .await;
        let _ = provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"response.completed"}),
        )
        .await;
    }
    start_identified_stream(&mut socket, &service.peer, "create-0", "shared").await;
    socket
        .send(Message::Text(
            json!({"type":"input_audio_buffer.append","event_id":"create-1"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    assert_eq!(receive_json(&mut socket).await["status"], 400);
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.completed"}),
    )
    .await;
    socket
        .send(Message::Text(
            json!({"type":"input_audio_buffer.append","event_id":"create-1"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    assert_eq!(peer_json(&service.peer).await["event_id"], "create-1");
    assert_eq!(service.finishes.lock().await.len(), 34);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn completed_create_history_counts_event_and_stream_identity_bytes() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    let first_id = "a".repeat(32 * 1024 - 1);
    let second_id = "b".repeat(32 * 1024 - 1);
    for event_id in [first_id.as_str(), second_id.as_str(), "c"] {
        start_identified_stream(&mut socket, &service.peer, event_id, "s").await;
        let _ = provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"response.completed"}),
        )
        .await;
    }
    start_identified_stream(&mut socket, &service.peer, &first_id, "s").await;
    socket
        .send(Message::Text(
            json!({"type":"input_audio_buffer.append","event_id":second_id})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    assert_eq!(receive_json(&mut socket).await["status"], 400);
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.completed"}),
    )
    .await;
    socket
        .send(Message::Text(
            json!({"type":"response.create","event_id":"large","stream_id":"s".repeat(64 * 1024),"model":"gpt-5.6-sol"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let rejected = receive_json(&mut socket).await;
    assert_eq!(rejected["status"], 400);
    assert_eq!(rejected["event_id"], "large");
    assert_eq!(service.start_calls.load(Ordering::SeqCst), 4);
    // Without a client create event ID there is no completed identity to retain.
    start_stream(&mut socket, &service.peer, &"s".repeat(64 * 1024)).await;
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.completed"}),
    )
    .await;
    assert_eq!(service.start_calls.load(Ordering::SeqCst), 5);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn retained_control_ids_and_capacity_are_rejected_locally() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    start_stream(&mut socket, &service.peer, "planner").await;
    for index in 0..32 {
        socket.send(Message::Text(json!({"type":"input_audio_buffer.append","event_id":format!("append-{index}"),"stream_id":"control"}).to_string().into())).await.unwrap();
        let _ = peer_json(&service.peer).await;
    }
    for event_id in ["append-0", "overflow"] {
        socket.send(Message::Text(json!({"type":"input_audio_buffer.append","event_id":event_id,"stream_id":"rejected"}).to_string().into())).await.unwrap();
        let rejected = receive_json(&mut socket).await;
        assert_eq!(rejected["stream_id"], "rejected");
        assert_eq!(rejected["event_id"], event_id);
        assert_eq!(rejected["status"], 400);
    }
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"error","event_id":"append-0"}),
    )
    .await;
    // Resolving frees a pending slot but keeps a tombstone against event ID reuse.
    socket
        .send(Message::Text(
            json!({"type":"input_audio_buffer.append","event_id":"append-0","stream_id":"reused"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    assert_eq!(receive_json(&mut socket).await["stream_id"], "reused");
    socket.send(Message::Text(json!({"type":"input_audio_buffer.append","event_id":"replacement","stream_id":"control"}).to_string().into())).await.unwrap();
    assert_eq!(peer_json(&service.peer).await["event_id"], "replacement");
    assert!(service.observations.lock().await.is_empty());
    assert!(service.finishes.lock().await.is_empty());
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.completed"}),
    )
    .await;
    assert_eq!(service.finishes.lock().await.len(), 1);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn control_identity_byte_limit_keeps_unidentified_legacy_controls_working() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    start_stream(&mut socket, &service.peer, "planner").await;
    socket.send(Message::Text(json!({"type":"input_audio_buffer.append","event_id":"large","stream_id":"control","response_id":"r".repeat(64 * 1024)}).to_string().into())).await.unwrap();
    let rejected = receive_json(&mut socket).await;
    assert_eq!(rejected["event_id"], "large");
    assert_eq!(rejected["stream_id"], "control");
    assert_eq!(rejected["status"], 400);
    for _ in 0..40 {
        socket
            .send(Message::Text(
                json!({"type":"input_audio_buffer.append","audio":"legacy"})
                    .to_string()
                    .into(),
            ))
            .await
            .unwrap();
        assert_eq!(peer_json(&service.peer).await["audio"], "legacy");
    }
    assert!(service.finishes.lock().await.is_empty());
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.completed"}),
    )
    .await;
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn successful_turns_retire_controls_and_remember_recent_response_ids() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    for index in 0..40 {
        start_stream(&mut socket, &service.peer, "planner").await;
        let _ = provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"response.created","response":{"id":format!("response-{index}")}}),
        )
        .await;
        socket.send(Message::Text(json!({"type":"input_audio_buffer.append","event_id":format!("append-{index}"),"stream_id":"control"}).to_string().into())).await.unwrap();
        assert_eq!(
            peer_json(&service.peer).await["event_id"],
            format!("append-{index}")
        );
        let _ = provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"response.completed","response":{"id":format!("response-{index}")}}),
        )
        .await;
        if index < 39 {
            socket.send(Message::Text(json!({"type":"input_audio_buffer.append","event_id":format!("idle-{index}"),"stream_id":"control"}).to_string().into())).await.unwrap();
            assert_eq!(
                peer_json(&service.peer).await["event_id"],
                format!("idle-{index}")
            );
        }
    }
    start_stream(&mut socket, &service.peer, "planner").await;
    let before = service.observations.lock().await.len();
    assert_eq!(
        provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"error","event_id":"append-30"})
        )
        .await["stream_id"],
        "control"
    );
    let late = json!({"type":"response.completed","response":{"id":"response-10"}});
    assert_eq!(
        provider_event(&mut socket, &service.peer, late.clone()).await,
        late
    );
    assert_eq!(service.observations.lock().await.len(), before);
    assert_eq!(service.finishes.lock().await.len(), 40);
    let _ = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"response.completed","response":{"id":"current"}}),
    )
    .await;
    assert_eq!(service.finishes.lock().await.len(), 41);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn an_unknown_active_response_id_cannot_prove_a_control_target_mismatch() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    start_stream(&mut socket, &service.peer, "planner").await;
    socket
        .send(Message::Text(
            json!({"type":"response.cancel","event_id":"cancel","response_id":"active"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let _ = peer_json(&service.peer).await;
    let error = provider_event(
        &mut socket,
        &service.peer,
        json!({"type":"error","response_id":"active","error":{"code":"server_error"}}),
    )
    .await;
    assert_eq!(error["stream_id"], "planner");
    assert_eq!(service.observations.lock().await.len(), 1);
    assert_eq!(service.finishes.lock().await.len(), 1);
    socket.send(Message::Text(json!({"type":"input_audio_buffer.append","response_id":"idle-target","stream_id":"idle-control"}).to_string().into())).await.unwrap();
    let _ = peer_json(&service.peer).await;
    assert_eq!(
        provider_event(
            &mut socket,
            &service.peer,
            json!({"type":"error","response_id":"idle-target"})
        )
        .await["stream_id"],
        "idle-control"
    );
    assert_eq!(service.observations.lock().await.len(), 1);
    assert_eq!(service.finishes.lock().await.len(), 1);
    socket.close(None).await.unwrap();
    server.abort();
}

#[tokio::test]
async fn omitted_client_identity_keeps_provider_identity_and_legacy_terminal_observation() {
    let service = TestService::new();
    let (url, server) = spawn(service.clone()).await;
    let mut socket = connect(&url).await;
    socket
        .send(Message::Text(
            json!({"type":"response.create","model":"gpt-5.6-sol"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    assert!(peer_json(&service.peer).await.get("stream_id").is_none());
    let event = json!({"type":"response.completed","stream_id":"provider"});
    assert_eq!(
        provider_event(&mut socket, &service.peer, event.clone()).await,
        event
    );
    assert_eq!(service.observations.lock().await.len(), 1);
    assert_eq!(service.finishes.lock().await.len(), 1);
    socket.close(None).await.unwrap();
    server.abort();
}

async fn start_identified_stream<S>(
    socket: &mut tokio_tungstenite::WebSocketStream<S>,
    peer: &ResponsesUpstreamPeer,
    event_id: &str,
    stream_id: &str,
) where
    S: tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin,
{
    socket
        .send(Message::Text(
            json!({"type":"response.create","event_id":event_id,"model":"gpt-5.6-sol","stream_id":stream_id})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let forwarded = peer_json(peer).await;
    assert_eq!(forwarded["type"], "response.create");
    assert_eq!(forwarded["model"], "gpt-5.6-sol");
    assert!(forwarded.get("event_id").is_none());
    assert_eq!(forwarded["stream_id"], stream_id);
}

async fn start_stream<S>(
    socket: &mut tokio_tungstenite::WebSocketStream<S>,
    peer: &ResponsesUpstreamPeer,
    stream_id: &str,
) where
    S: tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin,
{
    socket
        .send(Message::Text(
            json!({"type":"response.create","model":"gpt-5.6-sol","stream_id":stream_id})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    assert_eq!(peer_json(peer).await["stream_id"], stream_id);
}

async fn provider_event<S>(
    socket: &mut tokio_tungstenite::WebSocketStream<S>,
    peer: &ResponsesUpstreamPeer,
    event: Value,
) -> Value
where
    S: tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin,
{
    peer.send(ResponsesFrame::Text(event.to_string()))
        .await
        .unwrap();
    receive_json(socket).await
}

async fn spawn(service: TestService) -> (String, JoinHandle<()>) {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let app = router(ResponsesWebSocketState::new(Arc::new(service)));
    let server = tokio::spawn(async move {
        axum::serve(listener, app).await.unwrap();
    });
    (format!("ws://{address}/v1/responses"), server)
}

async fn connect(
    url: &str,
) -> tokio_tungstenite::WebSocketStream<tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>> {
    let mut request = url.into_client_request().unwrap();
    request
        .headers_mut()
        .insert(header::AUTHORIZATION, "Bearer good".parse().unwrap());
    request
        .headers_mut()
        .insert(header::SEC_WEBSOCKET_PROTOCOL, "responses".parse().unwrap());
    let (socket, response) = connect_async(request).await.unwrap();
    assert_eq!(
        response.headers().get(header::SEC_WEBSOCKET_PROTOCOL),
        Some(&"responses".parse().unwrap())
    );
    socket
}

async fn receive_json<S>(socket: &mut tokio_tungstenite::WebSocketStream<S>) -> Value
where
    S: tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin,
{
    let message = tokio::time::timeout(Duration::from_secs(2), socket.next())
        .await
        .expect("client response timeout")
        .expect("client connection closed")
        .expect("client frame failed");
    let Message::Text(payload) = message else {
        panic!("expected text frame, got {message:?}");
    };
    serde_json::from_str(&payload).unwrap()
}

async fn peer_json(peer: &ResponsesUpstreamPeer) -> Value {
    let frame = tokio::time::timeout(Duration::from_secs(2), peer.recv())
        .await
        .expect("provider frame timeout")
        .expect("provider connection closed");
    serde_json::from_slice(frame.payload()).unwrap()
}

async fn wait_for_closed(service: &TestService) {
    tokio::time::timeout(Duration::from_secs(2), async {
        loop {
            if !service.closed.lock().await.is_empty() {
                break;
            }
            tokio::task::yield_now().await;
        }
    })
    .await
    .expect("websocket session did not close");
}
