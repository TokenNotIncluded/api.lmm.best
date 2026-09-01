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
    ResponsesWebSocketState, router,
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
    upstream: Arc<ResponsesUpstream>,
    peer: Arc<ResponsesUpstreamPeer>,
    finishes: Arc<Mutex<Vec<ResponsesTurnFinish>>>,
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
            upstream,
            peer: Arc::new(peer),
            finishes: Arc::new(Mutex::new(Vec::new())),
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
        _turn: &ResponsesTurn,
        frame: &ResponsesFrame,
    ) -> Result<ResponsesTurnObservation, ResponsesWebSocketFailure> {
        let value: Value = serde_json::from_slice(frame.payload()).unwrap_or_default();
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
            json!({"type":"response.create","event_id":"failed","model":"gpt-5.6-sol"})
                .to_string()
                .into(),
        ))
        .await
        .unwrap();
    let failure = receive_json(&mut socket).await;
    assert_eq!(failure["type"], "error");
    assert_eq!(failure["status"], 502);
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

    let error = receive_json(&mut socket).await;
    assert_eq!(error["error"]["code"], "billing_failed");
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
