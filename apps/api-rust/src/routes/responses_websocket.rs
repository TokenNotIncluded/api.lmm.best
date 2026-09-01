//! Current-Go compatible `GET /v1/responses` WebSocket transport.
//!
//! The HTTP boundary deliberately upgrades only after the performance and
//! relay-token gates succeed. Channel selection is delayed until the first
//! valid `response.create` supplies a model, and every later logical request
//! revalidates token/model/channel authority before rate limiting or billing.
//! Provider selection, request conversion, retry, quota reservation and final
//! settlement remain one fail-closed service boundary; this module owns frame
//! ordering, bidirectional transport, error events and close semantics.
//!
//! No production service adapter is exported here. The route must remain
//! unmounted until the shared relay core can supply complete advanced-security,
//! subscription/tiered/tool/image billing, affinity and channel-policy hooks.

use std::{net::IpAddr, sync::Arc, time::Duration};

use async_trait::async_trait;
use axum::{
    Json, Router,
    extract::ws::{CloseFrame, Message, Utf8Bytes, WebSocket, WebSocketUpgrade},
    extract::{FromRequestParts, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::get,
};
use futures_util::{SinkExt, StreamExt};
use serde::Serialize;
use serde_json::{Map, Value, json};
use tokio::sync::{Mutex, mpsc};
use tokio_tungstenite::{
    connect_async,
    tungstenite::{
        Message as TungsteniteMessage,
        client::IntoClientRequest,
        protocol::{CloseFrame as TungsteniteCloseFrame, frame::coding::CloseCode},
    },
};

use crate::RequestContext;

const UPSTREAM_QUEUE_DEPTH: usize = 32;
const UPSTREAM_IO_TIMEOUT: Duration = Duration::from_secs(30);
const RESPONSES_SUBPROTOCOL: &str = "responses";
const REALTIME_SUBPROTOCOL: &str = "realtime";

/// Sendable request facts established at the listener boundary.
#[derive(Clone, Debug)]
pub struct ResponsesHandshakeRequest {
    headers: HeaderMap,
    request_id: String,
    client_ip: Option<IpAddr>,
}

impl ResponsesHandshakeRequest {
    fn from_http(request: &Request) -> Self {
        let boundary = request.extensions().get::<RequestContext>();
        Self {
            headers: request.headers().clone(),
            request_id: boundary.map_or_else(
                || {
                    request
                        .headers()
                        .get("x-oneapi-request-id")
                        .and_then(|value| value.to_str().ok())
                        .unwrap_or_default()
                        .to_owned()
                },
                |context| context.request_id.clone(),
            ),
            client_ip: boundary.and_then(|context| context.client_ip),
        }
    }

    /// Returns all original handshake headers. Implementations must treat
    /// credentials and cookies as secrets and never forward them upstream.
    #[must_use]
    pub const fn headers(&self) -> &HeaderMap {
        &self.headers
    }

    /// Server-generated request identifier used by compatibility errors.
    #[must_use]
    pub fn request_id(&self) -> &str {
        &self.request_id
    }

    /// Canonical client address after trusted-proxy processing.
    #[must_use]
    pub const fn client_ip(&self) -> Option<IpAddr> {
        self.client_ip
    }
}

/// Opaque authenticated identity retained for one WebSocket connection.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ResponsesSession {
    /// Stable server-side token identity; never the presented secret.
    pub token_id: String,
    connection_id: String,
}

impl ResponsesSession {
    /// Creates an authenticated connection identity for a service adapter.
    #[must_use]
    pub fn new(token_id: impl Into<String>) -> Self {
        Self {
            token_id: token_id.into(),
            connection_id: uuid::Uuid::new_v4().to_string(),
        }
    }

    /// Opaque per-connection key for application-owned session state.
    #[must_use]
    pub fn connection_id(&self) -> &str {
        &self.connection_id
    }
}

/// A channel locked to this connection after the first successful create.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ResponsesChannelLock {
    pub id: i64,
    pub channel_type: i64,
}

/// Refreshed facts returned after each logical-request authorization pass.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ResponsesTurnAuthorization {
    pub session: ResponsesSession,
    pub locked_channel: Option<ResponsesChannelLock>,
}

/// Normalized `response.create` content supplied to selection and billing.
#[derive(Clone, Debug, PartialEq)]
pub struct ResponsesCreate {
    pub event_id: String,
    pub model: String,
    /// Request object after removing WebSocket-only transport fields.
    pub request: Value,
    /// Provider-neutral flattened event. A channel adapter may still map it.
    pub outbound_event: Value,
}

/// One opaque, pre-billed logical request.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ResponsesTurn {
    pub id: String,
}

/// Inputs to rate limiting, selection, conversion, reservation and first send.
#[derive(Clone)]
pub struct ResponsesStartTurn {
    pub session: ResponsesSession,
    pub locked_channel: Option<ResponsesChannelLock>,
    pub existing_upstream: Option<Arc<ResponsesUpstream>>,
    pub create: ResponsesCreate,
}

/// Successful start after the first event reached an authorized upstream.
#[derive(Clone)]
pub struct ResponsesStartedTurn {
    pub turn: ResponsesTurn,
    pub channel: ResponsesChannelLock,
    pub upstream: Arc<ResponsesUpstream>,
}

/// Outcome extracted from one upstream data event.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ResponsesTurnObservation {
    Continue,
    Terminal {
        success: bool,
        billable_partial: bool,
    },
}

/// Why a reserved logical request is being finalized.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ResponsesTurnFinish {
    Terminal {
        success: bool,
        billable_partial: bool,
    },
    ClientClosed,
    UpstreamClosed,
    UpstreamWriteFailed,
    PolicyClosed,
}

/// Legacy-compatible error used in post-upgrade error events.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ResponsesWebSocketFailure {
    pub status: StatusCode,
    pub message: String,
    pub code: String,
    pub kind: String,
    pub param: String,
}

impl ResponsesWebSocketFailure {
    /// Builds a current new-api error without provider details.
    #[must_use]
    pub fn new(status: StatusCode, code: impl Into<String>, message: impl Into<String>) -> Self {
        Self {
            status,
            message: message.into(),
            code: code.into(),
            kind: "new_api_error".to_owned(),
            param: String::new(),
        }
    }

    fn invalid_request(message: impl Into<String>) -> Self {
        Self::new(StatusCode::BAD_REQUEST, "invalid_request", message)
    }

    fn bad_response(message: impl Into<String>) -> Self {
        Self::new(StatusCode::INTERNAL_SERVER_ERROR, "bad_response", message)
    }
}

/// Pre-upgrade failure. Its body is returned verbatim as JSON.
#[derive(Clone, Debug)]
pub struct ResponsesHandshakeFailure {
    pub status: StatusCode,
    pub body: Value,
    pub headers: HeaderMap,
}

impl ResponsesHandshakeFailure {
    /// Creates a JSON handshake rejection.
    #[must_use]
    pub fn json(status: StatusCode, body: Value) -> Self {
        Self {
            status,
            body,
            headers: HeaderMap::new(),
        }
    }

    /// Current Go conceals an invalid relay credential with this response.
    #[must_use]
    pub fn concealed_not_found() -> Self {
        Self::json(StatusCode::NOT_FOUND, json!({"message":"Not Found"}))
    }
}

/// Policy and accounting boundary for the long-lived transport.
#[async_trait]
pub trait ResponsesWebSocketService: Send + Sync {
    /// Runs `SystemPerformanceCheck` and then complete relay `TokenAuth`.
    async fn handshake(
        &self,
        request: &ResponsesHandshakeRequest,
    ) -> Result<ResponsesSession, ResponsesHandshakeFailure>;

    /// Re-runs full token policy, model access and locked-channel authority.
    /// This is called for every valid create, before model-lock/concurrency and
    /// before model-rate-limit counters are touched.
    async fn authorize_turn(
        &self,
        request: &ResponsesHandshakeRequest,
        session: &ResponsesSession,
        model: &str,
        locked_channel: Option<&ResponsesChannelLock>,
    ) -> Result<ResponsesTurnAuthorization, ResponsesWebSocketFailure>;

    /// Applies per-logical-request rate limiting, selects/retries a supported
    /// channel, reserves quota, converts the event, connects or reuses the
    /// upstream, and sends the first event.
    ///
    /// Any error must roll back its rate/billing reservation. A provider or
    /// billing failure must never return `Ok` with a synthetic upstream.
    async fn start_turn(
        &self,
        request: ResponsesStartTurn,
    ) -> Result<ResponsesStartedTurn, ResponsesWebSocketFailure>;

    /// Observes usage/output and identifies terminal provider events.
    async fn observe_upstream(
        &self,
        turn: &ResponsesTurn,
        frame: &ResponsesFrame,
    ) -> Result<ResponsesTurnObservation, ResponsesWebSocketFailure>;

    /// Settles exactly once. Successful or billable-partial outcomes commit;
    /// otherwise this must refund and must not increment the success limiter.
    async fn finish_turn(
        &self,
        turn: ResponsesTurn,
        finish: ResponsesTurnFinish,
    ) -> Result<(), ResponsesWebSocketFailure>;

    /// Releases connection-scoped authorization state after both peers close.
    /// If final settlement failed, `unfinished_turn` transfers reconciliation
    /// ownership to the service; it must durably retry or compensate it.
    async fn session_closed(
        &self,
        session: &ResponsesSession,
        unfinished_turn: Option<ResponsesTurn>,
    );
}

/// Explicit fail-closed adapter for compositions missing real policy/billing.
#[derive(Default)]
pub struct UnconfiguredResponsesWebSocketService;

#[async_trait]
impl ResponsesWebSocketService for UnconfiguredResponsesWebSocketService {
    async fn handshake(
        &self,
        _request: &ResponsesHandshakeRequest,
    ) -> Result<ResponsesSession, ResponsesHandshakeFailure> {
        Err(ResponsesHandshakeFailure::json(
            StatusCode::SERVICE_UNAVAILABLE,
            json!({"error":{"message":"responses websocket service is not configured","type":"new_api_error","param":"","code":"service_unavailable"}}),
        ))
    }

    async fn authorize_turn(
        &self,
        _request: &ResponsesHandshakeRequest,
        _session: &ResponsesSession,
        _model: &str,
        _locked_channel: Option<&ResponsesChannelLock>,
    ) -> Result<ResponsesTurnAuthorization, ResponsesWebSocketFailure> {
        Err(ResponsesWebSocketFailure::new(
            StatusCode::SERVICE_UNAVAILABLE,
            "service_unavailable",
            "responses websocket service is not configured",
        ))
    }

    async fn start_turn(
        &self,
        _request: ResponsesStartTurn,
    ) -> Result<ResponsesStartedTurn, ResponsesWebSocketFailure> {
        Err(ResponsesWebSocketFailure::new(
            StatusCode::SERVICE_UNAVAILABLE,
            "service_unavailable",
            "responses websocket service is not configured",
        ))
    }

    async fn observe_upstream(
        &self,
        _turn: &ResponsesTurn,
        _frame: &ResponsesFrame,
    ) -> Result<ResponsesTurnObservation, ResponsesWebSocketFailure> {
        Err(ResponsesWebSocketFailure::new(
            StatusCode::SERVICE_UNAVAILABLE,
            "service_unavailable",
            "responses websocket service is not configured",
        ))
    }

    async fn finish_turn(
        &self,
        _turn: ResponsesTurn,
        _finish: ResponsesTurnFinish,
    ) -> Result<(), ResponsesWebSocketFailure> {
        Err(ResponsesWebSocketFailure::new(
            StatusCode::SERVICE_UNAVAILABLE,
            "service_unavailable",
            "responses websocket service is not configured",
        ))
    }

    async fn session_closed(
        &self,
        _session: &ResponsesSession,
        _unfinished_turn: Option<ResponsesTurn>,
    ) {
    }
}

/// Candidate WebSocket state requiring a complete application service.
#[derive(Clone)]
pub struct ResponsesWebSocketState {
    service: Arc<dyn ResponsesWebSocketService>,
}

impl ResponsesWebSocketState {
    /// Creates state from a complete application-owned policy/provider/billing
    /// service. The current repository intentionally provides no production
    /// implementation of that service.
    #[must_use]
    pub fn new(service: Arc<dyn ResponsesWebSocketService>) -> Self {
        Self { service }
    }
}

/// Builds the unmounted candidate GET half of `/v1/responses`.
pub fn router(state: ResponsesWebSocketState) -> Router {
    Router::new()
        .route("/v1/responses", get(responses_websocket))
        .with_state(state)
}

async fn responses_websocket(
    State(state): State<ResponsesWebSocketState>,
    request: Request,
) -> Response {
    let handshake = ResponsesHandshakeRequest::from_http(&request);
    let session = match state.service.handshake(&handshake).await {
        Ok(session) => session,
        Err(failure) => return handshake_failure_response(failure),
    };

    // Extract only after performance and token policy. This preserves Go's
    // concealed auth result even when the caller supplied a malformed upgrade.
    let (mut parts, _body) = request.into_parts();
    let upgrade = match WebSocketUpgrade::from_request_parts(&mut parts, &()).await {
        Ok(upgrade) => upgrade,
        Err(rejection) => {
            state.service.session_closed(&session, None).await;
            return rejection.into_response();
        }
    };
    upgrade
        .protocols([REALTIME_SUBPROTOCOL, RESPONSES_SUBPROTOCOL])
        .on_upgrade(move |socket| run_session(socket, state, handshake, session))
}

struct ActiveSession {
    authorization: ResponsesSession,
    locked_model: Option<String>,
    locked_channel: Option<ResponsesChannelLock>,
    upstream: Option<Arc<ResponsesUpstream>>,
    incoming: Option<mpsc::Receiver<Result<ResponsesFrame, ResponsesUpstreamFailure>>>,
    current: Option<ResponsesTurn>,
}

impl ActiveSession {
    fn new(authorization: ResponsesSession) -> Self {
        Self {
            authorization,
            locked_model: None,
            locked_channel: None,
            upstream: None,
            incoming: None,
            current: None,
        }
    }
}

async fn run_session(
    mut client: WebSocket,
    state: ResponsesWebSocketState,
    handshake: ResponsesHandshakeRequest,
    authorization: ResponsesSession,
) {
    let mut session = ActiveSession::new(authorization);
    loop {
        tokio::select! {
            client_message = client.recv() => {
                match client_message {
                    Some(Ok(Message::Text(text))) => {
                        let frame = ResponsesFrame::Text(text.to_string());
                        if !handle_client_data(&mut client, &state, &handshake, &mut session, frame).await {
                            break;
                        }
                    }
                    Some(Ok(Message::Binary(bytes))) => {
                        let frame = ResponsesFrame::Binary(bytes.to_vec());
                        if !handle_client_data(&mut client, &state, &handshake, &mut session, frame).await {
                            break;
                        }
                    }
                    Some(Ok(Message::Close(_))) | None => {
                        finish_current(&state, &mut session, ResponsesTurnFinish::ClientClosed).await;
                        close_upstream(&mut session).await;
                        break;
                    }
                    Some(Err(_)) => {
                        finish_current(&state, &mut session, ResponsesTurnFinish::ClientClosed).await;
                        close_upstream(&mut session).await;
                        break;
                    }
                    Some(Ok(Message::Ping(_) | Message::Pong(_))) => {
                        // Axum performs control-frame replies. Gorilla's
                        // ReadMessage likewise does not expose these to relay logic.
                    }
                }
            }
            upstream_message = recv_upstream(&mut session.incoming), if session.incoming.is_some() => {
                match upstream_message {
                    Some(Ok(frame)) => {
                        if !handle_upstream_frame(&mut client, &state, &mut session, frame).await {
                            break;
                        }
                    }
                    Some(Err(failure)) if failure.policy => {
                        finish_current(&state, &mut session, ResponsesTurnFinish::PolicyClosed).await;
                        let _ = client.send(Message::Close(Some(CloseFrame {
                            code: 1008,
                            reason: Utf8Bytes::from(failure.message),
                        }))).await;
                        close_upstream(&mut session).await;
                        break;
                    }
                    Some(Err(_)) | None => {
                        finish_current(&state, &mut session, ResponsesTurnFinish::UpstreamClosed).await;
                        let _ = client.send(Message::Close(None)).await;
                        close_upstream(&mut session).await;
                        break;
                    }
                }
            }
        }
    }
    let unfinished_turn = session.current.take();
    state
        .service
        .session_closed(&session.authorization, unfinished_turn)
        .await;
}

async fn recv_upstream(
    incoming: &mut Option<mpsc::Receiver<Result<ResponsesFrame, ResponsesUpstreamFailure>>>,
) -> Option<Result<ResponsesFrame, ResponsesUpstreamFailure>> {
    match incoming {
        Some(incoming) => incoming.recv().await,
        None => std::future::pending().await,
    }
}

async fn handle_client_data(
    client: &mut WebSocket,
    state: &ResponsesWebSocketState,
    handshake: &ResponsesHandshakeRequest,
    session: &mut ActiveSession,
    frame: ResponsesFrame,
) -> bool {
    let payload = frame.payload();
    let (event_type, event_id) = match event_metadata(payload) {
        Ok(metadata) => metadata,
        Err(failure) => {
            send_error(client, "", &failure).await;
            return true;
        }
    };
    match event_type.as_str() {
        "response.create" => {
            let create = match normalize_create(payload) {
                Ok(create) => create,
                Err(failure) => {
                    send_error(client, &event_id, &failure).await;
                    return true;
                }
            };
            if create.model.is_empty() {
                send_error(
                    client,
                    &create.event_id,
                    &ResponsesWebSocketFailure::invalid_request("model is required"),
                )
                .await;
                return true;
            }
            let refreshed = match state
                .service
                .authorize_turn(
                    handshake,
                    &session.authorization,
                    &create.model,
                    session.locked_channel.as_ref(),
                )
                .await
            {
                Ok(refreshed) => refreshed,
                Err(failure) => {
                    send_error(client, &create.event_id, &failure).await;
                    return true;
                }
            };
            session.authorization = refreshed.session;
            if let Some(channel) = refreshed.locked_channel {
                session.locked_channel = Some(channel);
            }
            if let Some(locked_model) = session.locked_model.as_deref()
                && locked_model != create.model
            {
                send_error(
                        client,
                        &create.event_id,
                        &ResponsesWebSocketFailure::invalid_request(format!(
                            "responses websocket connection is locked to model {locked_model:?}; got {:?}",
                            create.model
                        )),
                    )
                    .await;
                return true;
            }
            if session.current.is_some() {
                send_error(
                    client,
                    &create.event_id,
                    &ResponsesWebSocketFailure::new(
                        StatusCode::CONFLICT,
                        "invalid_request",
                        "another response.create is already in progress on this websocket connection",
                    ),
                )
                .await;
                return true;
            }
            if session.locked_channel.is_some() && session.upstream.is_none() {
                send_error(
                    client,
                    &create.event_id,
                    &ResponsesWebSocketFailure::bad_response(
                        "locked responses websocket upstream is unavailable",
                    ),
                )
                .await;
                return false;
            }

            let started = match state
                .service
                .start_turn(ResponsesStartTurn {
                    session: session.authorization.clone(),
                    locked_channel: session.locked_channel.clone(),
                    existing_upstream: session.upstream.clone(),
                    create: create.clone(),
                })
                .await
            {
                Ok(started) => started,
                Err(failure) => {
                    send_error(client, &create.event_id, &failure).await;
                    return true;
                }
            };
            if let Some(locked) = session.locked_channel.as_ref()
                && locked != &started.channel
            {
                finish_detached(
                    state,
                    session,
                    started.turn,
                    ResponsesTurnFinish::PolicyClosed,
                )
                .await;
                send_error(
                    client,
                    &create.event_id,
                    &ResponsesWebSocketFailure::new(
                        StatusCode::FORBIDDEN,
                        "get_channel_failed",
                        "locked responses websocket channel changed",
                    ),
                )
                .await;
                return false;
            }
            if let Some(existing) = session.upstream.as_ref() {
                if !Arc::ptr_eq(existing, &started.upstream) {
                    finish_detached(
                        state,
                        session,
                        started.turn,
                        ResponsesTurnFinish::PolicyClosed,
                    )
                    .await;
                    send_error(
                        client,
                        &create.event_id,
                        &ResponsesWebSocketFailure::new(
                            StatusCode::FORBIDDEN,
                            "get_channel_failed",
                            "locked responses websocket upstream changed",
                        ),
                    )
                    .await;
                    return false;
                }
            } else {
                let incoming = started.upstream.take_incoming().await;
                let Some(incoming) = incoming else {
                    finish_detached(
                        state,
                        session,
                        started.turn,
                        ResponsesTurnFinish::UpstreamClosed,
                    )
                    .await;
                    send_error(
                        client,
                        &create.event_id,
                        &ResponsesWebSocketFailure::bad_response(
                            "responses websocket upstream is unavailable",
                        ),
                    )
                    .await;
                    return false;
                };
                session.incoming = Some(incoming);
                session.upstream = Some(Arc::clone(&started.upstream));
            }
            session.locked_model.get_or_insert(create.model);
            session.locked_channel.get_or_insert(started.channel);
            session.current = Some(started.turn);
            true
        }
        "response.cancel" => {
            if session.current.is_none() || session.upstream.is_none() {
                send_error(
                    client,
                    &event_id,
                    &ResponsesWebSocketFailure::invalid_request("no response is active to cancel"),
                )
                .await;
                return true;
            }
            forward_control(client, state, session, frame).await
        }
        _ => {
            if session.upstream.is_none() {
                send_error(
                    client,
                    &event_id,
                    &ResponsesWebSocketFailure::invalid_request(
                        "first responses websocket event must be response.create",
                    ),
                )
                .await;
                return true;
            }
            forward_control(client, state, session, frame).await
        }
    }
}

async fn forward_control(
    client: &mut WebSocket,
    state: &ResponsesWebSocketState,
    session: &mut ActiveSession,
    frame: ResponsesFrame,
) -> bool {
    let Some(upstream) = session.upstream.as_ref() else {
        return true;
    };
    if upstream.send(frame).await.is_ok() {
        return true;
    }
    finish_current(state, session, ResponsesTurnFinish::UpstreamWriteFailed).await;
    close_upstream(session).await;
    send_error(
        client,
        "",
        &ResponsesWebSocketFailure::bad_response("responses websocket upstream write failed"),
    )
    .await;
    false
}

async fn handle_upstream_frame(
    client: &mut WebSocket,
    state: &ResponsesWebSocketState,
    session: &mut ActiveSession,
    frame: ResponsesFrame,
) -> bool {
    if let Some(turn) = session.current.as_ref() {
        match state.service.observe_upstream(turn, &frame).await {
            Ok(ResponsesTurnObservation::Continue) => {}
            Ok(ResponsesTurnObservation::Terminal {
                success,
                billable_partial,
            }) => {
                let Some(turn) = session.current.take() else {
                    send_error(
                        client,
                        "",
                        &ResponsesWebSocketFailure::bad_response(
                            "responses websocket turn state is unavailable",
                        ),
                    )
                    .await;
                    close_upstream(session).await;
                    return false;
                };
                if let Err(failure) = state
                    .service
                    .finish_turn(
                        turn.clone(),
                        ResponsesTurnFinish::Terminal {
                            success,
                            billable_partial,
                        },
                    )
                    .await
                {
                    session.current = Some(turn);
                    send_error(client, "", &failure).await;
                    close_upstream(session).await;
                    return false;
                }
            }
            Err(failure) => {
                finish_current(state, session, ResponsesTurnFinish::UpstreamClosed).await;
                send_error(client, "", &failure).await;
                close_upstream(session).await;
                return false;
            }
        }
    }
    if let Some(message) = frame.into_axum()
        && client.send(message).await.is_err()
    {
        finish_current(state, session, ResponsesTurnFinish::ClientClosed).await;
        close_upstream(session).await;
        return false;
    }
    true
}

async fn finish_current(
    state: &ResponsesWebSocketState,
    session: &mut ActiveSession,
    finish: ResponsesTurnFinish,
) {
    if let Some(turn) = session.current.take()
        && state
            .service
            .finish_turn(turn.clone(), finish)
            .await
            .is_err()
    {
        session.current = Some(turn);
    }
}

async fn finish_detached(
    state: &ResponsesWebSocketState,
    session: &ActiveSession,
    turn: ResponsesTurn,
    finish: ResponsesTurnFinish,
) {
    if state
        .service
        .finish_turn(turn.clone(), finish)
        .await
        .is_err()
    {
        state
            .service
            .session_closed(&session.authorization, Some(turn))
            .await;
    }
}

async fn close_upstream(session: &mut ActiveSession) {
    if let Some(upstream) = session.upstream.take() {
        upstream.close(1000, String::new()).await;
    }
    session.incoming = None;
}

fn event_metadata(payload: &[u8]) -> Result<(String, String), ResponsesWebSocketFailure> {
    let value: Value = serde_json::from_slice(payload).map_err(|error| {
        ResponsesWebSocketFailure::invalid_request(format!("invalid websocket event json: {error}"))
    })?;
    let object = value.as_object().ok_or_else(|| {
        ResponsesWebSocketFailure::invalid_request("invalid websocket event json: expected object")
    })?;
    let event_id = optional_string_field(object, "event_id")?.unwrap_or_default();
    let event_type = optional_string_field(object, "type")?.unwrap_or_default();
    if event_type.trim().is_empty() {
        return Err(ResponsesWebSocketFailure::invalid_request(
            "websocket event type is required",
        ));
    }
    Ok((event_type, event_id))
}

fn optional_string_field(
    object: &Map<String, Value>,
    field: &str,
) -> Result<Option<String>, ResponsesWebSocketFailure> {
    match object.get(field) {
        None | Some(Value::Null) => Ok(None),
        Some(Value::String(value)) => Ok(Some(value.clone())),
        Some(_) => Err(ResponsesWebSocketFailure::invalid_request(format!(
            "invalid websocket event json: {field} must be a string"
        ))),
    }
}

fn normalize_create(payload: &[u8]) -> Result<ResponsesCreate, ResponsesWebSocketFailure> {
    let root: Value = serde_json::from_slice(payload).map_err(|error| {
        ResponsesWebSocketFailure::invalid_request(format!("invalid response.create: {error}"))
    })?;
    let root = root.as_object().ok_or_else(|| {
        ResponsesWebSocketFailure::invalid_request("invalid response.create: expected object")
    })?;
    let event_id = root
        .get("event_id")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_owned();
    let top_generate = root.get("generate").cloned();
    let mut request = match root.get("response") {
        Some(Value::Object(response)) => response.clone(),
        Some(Value::Null) => Map::new(),
        Some(_) => {
            return Err(ResponsesWebSocketFailure::invalid_request(
                "invalid response.create response",
            ));
        }
        None => root.clone(),
    };
    let generate = top_generate.or_else(|| request.get("generate").cloned());
    request.remove("type");
    request.remove("event_id");
    request.remove("background");
    request.remove("generate");
    request.remove("stream");
    request.remove("stream_options");
    let model = match request.get("model") {
        Some(Value::String(model)) => model.clone(),
        Some(Value::Null) | None => String::new(),
        Some(_) => {
            return Err(ResponsesWebSocketFailure::invalid_request(
                "invalid response.create model",
            ));
        }
    };
    let mut outbound = request.clone();
    outbound.insert(
        "type".to_owned(),
        Value::String("response.create".to_owned()),
    );
    if let Some(generate) = generate {
        outbound.insert("generate".to_owned(), generate);
    }
    Ok(ResponsesCreate {
        event_id,
        model,
        request: Value::Object(request),
        outbound_event: Value::Object(outbound),
    })
}

#[derive(Serialize)]
struct ErrorEvent<'error> {
    #[serde(rename = "type")]
    event_type: &'static str,
    status: u16,
    #[serde(skip_serializing_if = "str::is_empty")]
    event_id: &'error str,
    error: OpenAiError<'error>,
}

#[derive(Serialize)]
struct OpenAiError<'error> {
    message: &'error str,
    #[serde(rename = "type")]
    kind: &'error str,
    param: &'error str,
    code: &'error str,
}

async fn send_error(client: &mut WebSocket, event_id: &str, failure: &ResponsesWebSocketFailure) {
    let event = ErrorEvent {
        event_type: "error",
        status: failure.status.as_u16(),
        event_id,
        error: OpenAiError {
            message: &failure.message,
            kind: &failure.kind,
            param: &failure.param,
            code: &failure.code,
        },
    };
    if let Ok(payload) = serde_json::to_string(&event) {
        let _ = client.send(Message::Text(payload.into())).await;
    }
}

fn handshake_failure_response(failure: ResponsesHandshakeFailure) -> Response {
    let mut response = (failure.status, Json(failure.body)).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    for (name, value) in &failure.headers {
        response.headers_mut().append(name, value.clone());
    }
    response
}

/// Provider-neutral WebSocket frame retained without lossy JSON conversion.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ResponsesFrame {
    Text(String),
    Binary(Vec<u8>),
    Ping(Vec<u8>),
    Pong(Vec<u8>),
    Close { code: u16, reason: String },
}

impl ResponsesFrame {
    /// Payload used by JSON event parsing; control frames return an empty slice.
    #[must_use]
    pub fn payload(&self) -> &[u8] {
        match self {
            Self::Text(text) => text.as_bytes(),
            Self::Binary(bytes) | Self::Ping(bytes) | Self::Pong(bytes) => bytes,
            Self::Close { .. } => &[],
        }
    }

    fn into_axum(self) -> Option<Message> {
        match self {
            Self::Text(text) => Some(Message::Text(text.into())),
            Self::Binary(bytes) => Some(Message::Binary(bytes.into())),
            Self::Ping(bytes) => Some(Message::Ping(bytes.into())),
            Self::Pong(bytes) => Some(Message::Pong(bytes.into())),
            Self::Close { code, reason } => Some(Message::Close(Some(CloseFrame {
                code,
                reason: reason.into(),
            }))),
        }
    }

    fn into_tungstenite(self) -> TungsteniteMessage {
        match self {
            Self::Text(text) => TungsteniteMessage::Text(text.into()),
            Self::Binary(bytes) => TungsteniteMessage::Binary(bytes.into()),
            Self::Ping(bytes) => TungsteniteMessage::Ping(bytes.into()),
            Self::Pong(bytes) => TungsteniteMessage::Pong(bytes.into()),
            Self::Close { code, reason } => {
                TungsteniteMessage::Close(Some(TungsteniteCloseFrame {
                    code: CloseCode::from(code),
                    reason: reason.into(),
                }))
            }
        }
    }

    fn from_tungstenite(message: TungsteniteMessage) -> Option<Self> {
        match message {
            TungsteniteMessage::Text(text) => Some(Self::Text(text.to_string())),
            TungsteniteMessage::Binary(bytes) => Some(Self::Binary(bytes.to_vec())),
            TungsteniteMessage::Ping(bytes) => Some(Self::Ping(bytes.to_vec())),
            TungsteniteMessage::Pong(bytes) => Some(Self::Pong(bytes.to_vec())),
            TungsteniteMessage::Close(Some(close)) => Some(Self::Close {
                code: u16::from(close.code),
                reason: close.reason.to_string(),
            }),
            TungsteniteMessage::Close(None) => Some(Self::Close {
                code: 1000,
                reason: String::new(),
            }),
            TungsteniteMessage::Frame(_) => None,
        }
    }
}

/// Safe upstream dial parameters constructed only after channel selection.
#[derive(Clone, Debug)]
pub struct ResponsesUpstreamRequest {
    pub url: String,
    pub headers: HeaderMap,
}

/// Error or policy-close signal from the upstream pump.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ResponsesUpstreamFailure {
    pub message: String,
    pub policy: bool,
}

/// A framed upstream transport shared across sequential logical requests.
pub struct ResponsesUpstream {
    outgoing: mpsc::Sender<ResponsesFrame>,
    incoming: Mutex<Option<mpsc::Receiver<Result<ResponsesFrame, ResponsesUpstreamFailure>>>>,
}

impl ResponsesUpstream {
    /// Connects to a selected provider with a bounded, TLS-capable WebSocket.
    /// Client handshake secrets must not be included in `request.headers`.
    pub async fn connect(
        request: ResponsesUpstreamRequest,
    ) -> Result<Arc<Self>, ResponsesWebSocketFailure> {
        let url = reqwest::Url::parse(&request.url).map_err(|_| {
            ResponsesWebSocketFailure::bad_response("invalid upstream websocket target")
        })?;
        if !matches!(url.scheme(), "ws" | "wss") || url.host_str().is_none() {
            return Err(ResponsesWebSocketFailure::bad_response(
                "invalid upstream websocket target",
            ));
        }
        if test_instance_enabled() && !is_loopback_target(&url) {
            return Err(ResponsesWebSocketFailure::bad_response(
                "invalid upstream websocket target",
            ));
        }
        let mut outbound = url
            .as_str()
            .into_client_request()
            .map_err(|_| ResponsesWebSocketFailure::bad_response("invalid upstream request"))?;
        for (name, value) in &request.headers {
            if !is_handshake_owned_header(name.as_str()) {
                outbound.headers_mut().append(name, value.clone());
            }
        }
        let (socket, _response) =
            tokio::time::timeout(UPSTREAM_IO_TIMEOUT, connect_async(outbound))
                .await
                .map_err(|_| {
                    ResponsesWebSocketFailure::new(
                        StatusCode::GATEWAY_TIMEOUT,
                        "do_request_failed",
                        "responses websocket upstream connect timed out",
                    )
                })?
                .map_err(|_| {
                    ResponsesWebSocketFailure::new(
                        StatusCode::BAD_GATEWAY,
                        "do_request_failed",
                        "failed to connect responses websocket upstream",
                    )
                })?;
        Ok(Self::from_socket(socket))
    }

    fn from_socket<S>(socket: tokio_tungstenite::WebSocketStream<S>) -> Arc<Self>
    where
        S: tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin + Send + 'static,
    {
        let (outgoing_tx, mut outgoing_rx) = mpsc::channel(UPSTREAM_QUEUE_DEPTH);
        let (incoming_tx, incoming_rx) = mpsc::channel(UPSTREAM_QUEUE_DEPTH);
        let (mut sink, mut stream) = socket.split();
        tokio::spawn(async move {
            loop {
                tokio::select! {
                    outgoing = outgoing_rx.recv() => {
                        let Some(outgoing) = outgoing else {
                            let _ = tokio::time::timeout(UPSTREAM_IO_TIMEOUT, sink.close()).await;
                            break;
                        };
                        let closing = matches!(outgoing, ResponsesFrame::Close { .. });
                        if !matches!(
                            tokio::time::timeout(
                                UPSTREAM_IO_TIMEOUT,
                                sink.send(outgoing.into_tungstenite()),
                            )
                            .await,
                            Ok(Ok(()))
                        ) {
                            let _ = incoming_tx.send(Err(ResponsesUpstreamFailure {
                                message: "responses websocket upstream write failed".to_owned(),
                                policy: false,
                            })).await;
                            break;
                        }
                        if closing {
                            break;
                        }
                    }
                    incoming = stream.next() => {
                        match incoming {
                            Some(Ok(message)) => {
                                if let Some(frame) = ResponsesFrame::from_tungstenite(message) {
                                    let closing = matches!(frame, ResponsesFrame::Close { .. });
                                    if incoming_tx.send(Ok(frame)).await.is_err() || closing {
                                        break;
                                    }
                                }
                            }
                            Some(Err(_)) => {
                                let _ = incoming_tx.send(Err(ResponsesUpstreamFailure {
                                    message: "responses websocket upstream read failed".to_owned(),
                                    policy: false,
                                })).await;
                                break;
                            }
                            None => break,
                        }
                    }
                }
            }
        });
        Arc::new(Self {
            outgoing: outgoing_tx,
            incoming: Mutex::new(Some(incoming_rx)),
        })
    }

    /// Creates a bounded in-memory peer for contract tests or an explicit
    /// application adapter. It performs no implicit provider success.
    #[must_use]
    pub fn channel() -> (Arc<Self>, ResponsesUpstreamPeer) {
        let (outgoing_tx, outgoing_rx) = mpsc::channel(UPSTREAM_QUEUE_DEPTH);
        let (incoming_tx, incoming_rx) = mpsc::channel(UPSTREAM_QUEUE_DEPTH);
        (
            Arc::new(Self {
                outgoing: outgoing_tx,
                incoming: Mutex::new(Some(incoming_rx)),
            }),
            ResponsesUpstreamPeer {
                outgoing: Mutex::new(outgoing_rx),
                incoming: incoming_tx,
            },
        )
    }

    /// Sends one frame to the selected upstream.
    pub async fn send(&self, frame: ResponsesFrame) -> Result<(), ResponsesUpstreamFailure> {
        tokio::time::timeout(UPSTREAM_IO_TIMEOUT, self.outgoing.send(frame))
            .await
            .map_err(|_| ResponsesUpstreamFailure {
                message: "responses websocket upstream write timed out".to_owned(),
                policy: false,
            })?
            .map_err(|_| ResponsesUpstreamFailure {
                message: "responses websocket upstream is not connected".to_owned(),
                policy: false,
            })
    }

    async fn take_incoming(
        &self,
    ) -> Option<mpsc::Receiver<Result<ResponsesFrame, ResponsesUpstreamFailure>>> {
        self.incoming.lock().await.take()
    }

    async fn close(&self, code: u16, reason: String) {
        let _ = self.send(ResponsesFrame::Close { code, reason }).await;
    }
}

/// The provider side of [`ResponsesUpstream::channel`].
pub struct ResponsesUpstreamPeer {
    outgoing: Mutex<mpsc::Receiver<ResponsesFrame>>,
    incoming: mpsc::Sender<Result<ResponsesFrame, ResponsesUpstreamFailure>>,
}

impl ResponsesUpstreamPeer {
    /// Receives the next gateway-to-provider frame.
    pub async fn recv(&self) -> Option<ResponsesFrame> {
        self.outgoing.lock().await.recv().await
    }

    /// Makes subsequent gateway writes fail. This is useful for explicit
    /// adapters and for proving that a broken locked upstream ends a session.
    pub async fn reject_gateway_writes(&self) {
        self.outgoing.lock().await.close();
    }

    /// Sends one provider frame to the gateway.
    pub async fn send(&self, frame: ResponsesFrame) -> Result<(), ResponsesUpstreamFailure> {
        self.incoming
            .send(Ok(frame))
            .await
            .map_err(|_| ResponsesUpstreamFailure {
                message: "responses websocket client is closed".to_owned(),
                policy: false,
            })
    }

    /// Closes both sides for an administrator/channel policy change.
    pub async fn policy_close(&self, reason: impl Into<String>) {
        let _ = self
            .incoming
            .send(Err(ResponsesUpstreamFailure {
                message: reason.into(),
                policy: true,
            }))
            .await;
    }
}

fn is_handshake_owned_header(name: &str) -> bool {
    let name = name.to_ascii_lowercase();
    name.starts_with("sec-websocket-")
        || matches!(
            name.as_str(),
            "host"
                | "connection"
                | "upgrade"
                | "content-length"
                | "proxy-authenticate"
                | "proxy-authorization"
                | "te"
                | "trailer"
                | "transfer-encoding"
        )
}

fn test_instance_enabled() -> bool {
    std::env::var("LMM_RS_TEST_INSTANCE").is_ok_and(|value| value == "1")
}

fn is_loopback_target(url: &reqwest::Url) -> bool {
    url.host_str().is_some_and(|host| {
        host.eq_ignore_ascii_case("localhost")
            || host
                .parse::<IpAddr>()
                .is_ok_and(|address| address.is_loopback())
    })
}
