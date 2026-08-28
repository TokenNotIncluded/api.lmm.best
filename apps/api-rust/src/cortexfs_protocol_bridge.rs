//! Production protocol conversion through the [`cortexfs-protocol`] crate.
//!
//! This module is the only runtime conversion boundary for cross-provider
//! relay traffic.  Same-protocol routes continue to use opaque byte
//! passthrough; every other source/target pair is transcoded through
//! `transcode_request` / `transcode_response` and, for streaming SSE, through
//! normalized [`ModelEvent`] values mapped into the contracts
//! [`CanonicalStreamEvent`] state machine.

use std::fmt;

use cortexfs_protocol::{
    BridgePath, ConversionError, EventStatus, ModelEvent, WireProtocol, decode_response_events,
    transcode_request, transcode_response,
};
use lmm_contracts::relay::{
    CanonicalStreamEvent, Direction, Fidelity, FinishReason, Protocol, TokenUsage, protocols,
};

use crate::{
    migration_routes::sse::SseFrame,
    protocol_stream_pipeline::{
        StreamAdaptor, StreamAdaptorItem, StreamAdaptorOutput, StreamAdaptorRegistry,
        StreamAdaptorSession, StreamCloseReason, StreamSetupFailure, TypedStreamFailure,
    },
};

/// Converter ID prefix shared by every cortexfs-backed route.
pub const CORTEXFS_CONVERTER_PREFIX: &str = "cortexfs";

/// Runtime adaptor prefix for cortexfs-backed routes.
pub const CORTEXFS_RUNTIME_PREFIX: &str = "cortexfs-runtime";

/// Failures surfaced by the bridge before an upstream call is attempted.
#[derive(Debug)]
pub enum CortexFsBridgeError {
    /// The contracts protocol has no cortexfs wire dialect.
    UnsupportedProtocol(Protocol),
    /// The underlying cortexfs conversion failed.
    Conversion(ConversionError),
}

impl fmt::Display for CortexFsBridgeError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::UnsupportedProtocol(protocol) => {
                write!(
                    formatter,
                    "unsupported protocol for cortexfs bridge: {protocol:?}"
                )
            }
            Self::Conversion(error) => write!(formatter, "cortexfs conversion failed: {error}"),
        }
    }
}

impl std::error::Error for CortexFsBridgeError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::UnsupportedProtocol(_) => None,
            Self::Conversion(error) => Some(error),
        }
    }
}

impl From<ConversionError> for CortexFsBridgeError {
    fn from(error: ConversionError) -> Self {
        Self::Conversion(error)
    }
}

/// Result of a successful request transcoding, including the route taken.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CortexFsTranscodedRequest {
    /// Target-protocol request bytes.
    pub bytes: Vec<u8>,
    /// Conversion route selected by cortexfs.
    pub path: BridgePath,
}

/// Result of a successful response transcoding.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CortexFsTranscodedResponse {
    /// Target-protocol response bytes.
    pub bytes: Vec<u8>,
    /// Conversion route selected by cortexfs.
    pub path: BridgePath,
}

/// Maps a contracts protocol to the cortexfs wire dialect.
#[must_use]
pub const fn protocol_to_wire(protocol: Protocol) -> Option<WireProtocol> {
    match protocol {
        Protocol::OpenAi => Some(WireProtocol::OpenAiChat),
        Protocol::OpenAiResponses => Some(WireProtocol::OpenAiResponses),
        Protocol::Claude => Some(WireProtocol::Anthropic),
        Protocol::Gemini => Some(WireProtocol::Gemini),
    }
}

/// Maps a cortexfs wire dialect back to the contracts protocol.
#[must_use]
pub const fn wire_to_protocol(wire: WireProtocol) -> Protocol {
    match wire {
        WireProtocol::OpenAiChat => Protocol::OpenAi,
        WireProtocol::OpenAiResponses => Protocol::OpenAiResponses,
        WireProtocol::Anthropic => Protocol::Claude,
        WireProtocol::Gemini => Protocol::Gemini,
    }
}

/// Returns the Go-aligned fidelity claim for one source/target pair.
#[must_use]
pub fn cross_protocol_fidelity(source: Protocol, target: Protocol) -> Fidelity {
    if source == target {
        return Fidelity::Exact;
    }
    match (source, target) {
        (Protocol::OpenAi, Protocol::OpenAiResponses)
        | (Protocol::OpenAiResponses, Protocol::OpenAi) => Fidelity::Normalized,
        (Protocol::Claude, Protocol::Gemini) | (Protocol::Gemini, Protocol::Claude) => {
            Fidelity::Lossy
        }
        _ => Fidelity::Normalized,
    }
}

/// Stable converter identifier for one direction and conversion phase.
#[must_use]
pub fn converter_id(source: Protocol, target: Protocol, direction: Direction) -> String {
    format!(
        "{CORTEXFS_CONVERTER_PREFIX}-{}-to-{}-{}-v1",
        protocol_slug(source),
        protocol_slug(target),
        direction_slug(direction)
    )
}

/// Stable stream-finalizer identifier for one source/target pair.
#[must_use]
pub fn stream_finalizer_id(source: Protocol, target: Protocol) -> String {
    format!(
        "{CORTEXFS_CONVERTER_PREFIX}-{}-to-{}-stream-finalizer-v1",
        protocol_slug(source),
        protocol_slug(target)
    )
}

/// Stable runtime adaptor identifier for one source/target pair.
#[must_use]
pub fn runtime_adaptor_id(source: Protocol, target: Protocol) -> String {
    format!(
        "{CORTEXFS_RUNTIME_PREFIX}-{}-to-{}-v1",
        protocol_slug(source),
        protocol_slug(target)
    )
}

const fn protocol_slug(protocol: Protocol) -> &'static str {
    match protocol {
        Protocol::OpenAi => "openai-chat",
        Protocol::OpenAiResponses => "openai-responses",
        Protocol::Claude => "claude-messages",
        Protocol::Gemini => "gemini-generate-content",
    }
}

const fn direction_slug(direction: Direction) -> &'static str {
    match direction {
        Direction::Request => "request",
        Direction::Response => "response",
        Direction::Stream => "stream",
    }
}

/// Transcodes a request body from `source` to `target`.
pub fn transcode_request_protocol(
    source: Protocol,
    target: Protocol,
    input: &[u8],
) -> Result<CortexFsTranscodedRequest, CortexFsBridgeError> {
    let source_wire =
        protocol_to_wire(source).ok_or(CortexFsBridgeError::UnsupportedProtocol(source))?;
    let target_wire =
        protocol_to_wire(target).ok_or(CortexFsBridgeError::UnsupportedProtocol(target))?;
    let converted = transcode_request(source_wire, target_wire, input)?;
    Ok(CortexFsTranscodedRequest {
        bytes: converted.bytes,
        path: converted.path,
    })
}

/// Transcodes a complete non-streaming response body from `source` to `target`.
pub fn transcode_response_protocol(
    source: Protocol,
    target: Protocol,
    input: &[u8],
) -> Result<CortexFsTranscodedResponse, CortexFsBridgeError> {
    let source_wire =
        protocol_to_wire(source).ok_or(CortexFsBridgeError::UnsupportedProtocol(source))?;
    let target_wire =
        protocol_to_wire(target).ok_or(CortexFsBridgeError::UnsupportedProtocol(target))?;
    let converted = transcode_response(source_wire, target_wire, input)?;
    Ok(CortexFsTranscodedResponse {
        bytes: converted.bytes,
        path: converted.path,
    })
}

/// Decodes one complete provider response body into normalized events.
pub fn decode_provider_events(
    source: Protocol,
    input: &[u8],
) -> Result<Vec<ModelEvent>, CortexFsBridgeError> {
    let source_wire =
        protocol_to_wire(source).ok_or(CortexFsBridgeError::UnsupportedProtocol(source))?;
    Ok(decode_response_events(source_wire, input)?)
}

/// Maps one normalized [`ModelEvent`] into zero or more canonical stream events.
pub fn model_event_to_canonical(
    event: &ModelEvent,
    state: &mut StreamMapState,
) -> Vec<CanonicalStreamEvent> {
    match event {
        ModelEvent::Start { run, model } => {
            state.run_id = Some(run.clone());
            state.started = true;
            vec![CanonicalStreamEvent::ResponseStart {
                id: run.clone(),
                model: model.clone(),
            }]
        }
        ModelEvent::TextDelta { text, .. } => {
            let index = state.ensure_text_block();
            vec![CanonicalStreamEvent::TextDelta {
                index,
                delta: text.clone(),
            }]
        }
        ModelEvent::ReasoningDelta { text, .. } => {
            let index = state.ensure_reasoning_block();
            vec![CanonicalStreamEvent::ReasoningDelta {
                index,
                delta: text.clone(),
            }]
        }
        ModelEvent::ToolCall { call, .. } => {
            let index = state.next_block_index();
            let arguments =
                serde_json::to_string(&call.arguments).unwrap_or_else(|_| "{}".to_owned());
            vec![
                CanonicalStreamEvent::ToolCallStart {
                    index,
                    id: call.id.clone(),
                    name: call.name.clone(),
                },
                CanonicalStreamEvent::ToolArgumentsDelta {
                    index,
                    delta: arguments,
                },
                CanonicalStreamEvent::ContentEnd { index },
            ]
        }
        ModelEvent::Message { message, .. } => {
            let text = message.content.text_value();
            if text.is_empty() {
                return Vec::new();
            }
            let index = state.ensure_text_block();
            vec![CanonicalStreamEvent::TextDelta { index, delta: text }]
        }
        ModelEvent::Usage { usage, .. } => {
            state.pending_usage = Some(TokenUsage {
                input_tokens: usage.input_tokens,
                output_tokens: usage.output_tokens,
                total_tokens: usage.input_tokens.saturating_add(usage.output_tokens),
                cached_input_tokens: usage.cached_tokens.unwrap_or(0),
                reasoning_tokens: usage.reasoning_tokens.unwrap_or(0),
            });
            Vec::new()
        }
        ModelEvent::Error { error, .. } => vec![CanonicalStreamEvent::Error {
            code: Some(error.code.clone()),
            message: error.message.clone(),
        }],
        ModelEvent::Done { status, .. } => {
            let finish_reason = match status {
                EventStatus::Ok => FinishReason::Stop,
                EventStatus::Error => FinishReason::Error,
                EventStatus::Cancelled => FinishReason::Cancelled,
            };
            vec![CanonicalStreamEvent::ResponseEnd {
                finish_reason,
                usage: state.pending_usage.take(),
                model: None,
            }]
        }
    }
}

/// Mutable mapping state for one streaming conversion session.
#[derive(Clone, Debug, Default)]
pub struct StreamMapState {
    run_id: Option<String>,
    started: bool,
    next_index: usize,
    text_block: Option<usize>,
    reasoning_block: Option<usize>,
    pending_usage: Option<TokenUsage>,
}

impl StreamMapState {
    fn ensure_text_block(&mut self) -> usize {
        if let Some(index) = self.text_block {
            return index;
        }
        let index = self.next_block_index();
        self.text_block = Some(index);
        index
    }

    fn ensure_reasoning_block(&mut self) -> usize {
        if let Some(index) = self.reasoning_block {
            return index;
        }
        let index = self.next_block_index();
        self.reasoning_block = Some(index);
        index
    }

    fn next_block_index(&mut self) -> usize {
        let index = self.next_index;
        self.next_index += 1;
        index
    }
}

/// Registry exposing cortexfs-backed stream adaptors for every cross-protocol pair.
#[derive(Clone, Copy, Debug, Default)]
pub struct CortexFsStreamAdaptorRegistry;

fn adaptor_table() -> &'static [CortexFsStreamAdaptor] {
    static TABLE: std::sync::OnceLock<Vec<CortexFsStreamAdaptor>> = std::sync::OnceLock::new();
    TABLE.get_or_init(|| {
        let mut adaptors = Vec::new();
        for source in protocols() {
            for target in protocols() {
                if source != target {
                    adaptors.push(CortexFsStreamAdaptor { source, target });
                }
            }
        }
        adaptors
    })
}

impl StreamAdaptorRegistry for CortexFsStreamAdaptorRegistry {
    fn for_route(&self, source: Protocol, target: Protocol) -> Option<&dyn StreamAdaptor> {
        adaptor_table()
            .iter()
            .find(|adaptor| adaptor.source == source && adaptor.target == target)
            .map(|adaptor| adaptor as &dyn StreamAdaptor)
    }
}

/// One cortexfs-backed typed stream adaptor.
#[derive(Clone, Copy, Debug)]
struct CortexFsStreamAdaptor {
    source: Protocol,
    target: Protocol,
}

impl StreamAdaptor for CortexFsStreamAdaptor {
    fn source(&self) -> Protocol {
        self.source
    }

    fn target(&self) -> Protocol {
        self.target
    }

    fn compile(
        &self,
        _plan: &lmm_contracts::relay::ConversionPlan,
    ) -> Result<Box<dyn StreamAdaptorSession>, StreamSetupFailure> {
        Ok(Box::new(CortexFsStreamSession {
            source: self.source,
            target: self.target,
            map_state: StreamMapState::default(),
        }))
    }
}

struct CortexFsStreamSession {
    source: Protocol,
    target: Protocol,
    map_state: StreamMapState,
}

impl StreamAdaptorSession for CortexFsStreamSession {
    fn process_frame(
        &mut self,
        frame: &SseFrame,
    ) -> Result<StreamAdaptorOutput, TypedStreamFailure> {
        let _ = self.target;
        if frame.data.trim() == "[DONE]" {
            return Ok(StreamAdaptorOutput::empty());
        }
        let payload = frame.data.as_bytes();
        if payload.is_empty() {
            return Ok(StreamAdaptorOutput::empty());
        }
        let events = decode_provider_events(self.source, payload)
            .map_err(|_| TypedStreamFailure::UnknownEvent)?;
        let mut items = Vec::new();
        for event in events {
            for canonical in model_event_to_canonical(&event, &mut self.map_state) {
                items.push(StreamAdaptorItem::Canonical { event: canonical });
            }
        }
        Ok(StreamAdaptorOutput::new(items))
    }

    fn cancel(&mut self) -> Result<(), TypedStreamFailure> {
        Ok(())
    }
}

/// Returns whether the bridge can transcode between two protocols.
#[must_use]
pub fn supports_transcode(source: Protocol, target: Protocol) -> bool {
    protocol_to_wire(source).is_some() && protocol_to_wire(target).is_some()
}

/// Returns the local close reason when no typed adaptor is available.
#[must_use]
pub const fn typed_adaptor_close_reason() -> StreamCloseReason {
    StreamCloseReason::TypedAdaptorUnavailable
}

#[cfg(test)]
mod tests {
    use super::*;
    use cortexfs_protocol::BridgePath;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    const CHAT: &[u8] = br#"{"model":"chat-model","messages":[{"role":"user","content":"hi"}]}"#;
    const RESPONSES: &[u8] = br#"{"model":"responses-model","input":"hi"}"#;
    const GEMINI: &[u8] =
        br#"{"model":"gemini-model","contents":[{"role":"user","parts":[{"text":"hi"}]}]}"#;
    const ANTHROPIC: &[u8] =
        br#"{"model":"claude-model","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}"#;
    const PROTOCOLS: [Protocol; 4] = [
        Protocol::OpenAi,
        Protocol::OpenAiResponses,
        Protocol::Claude,
        Protocol::Gemini,
    ];

    fn protocol_pairs() -> impl Iterator<Item = (Protocol, Protocol)> {
        PROTOCOLS
            .into_iter()
            .flat_map(|source| PROTOCOLS.into_iter().map(move |target| (source, target)))
    }

    fn fixture(protocol: Protocol) -> &'static [u8] {
        match protocol {
            Protocol::OpenAi => CHAT,
            Protocol::OpenAiResponses => RESPONSES,
            Protocol::Claude => ANTHROPIC,
            Protocol::Gemini => GEMINI,
        }
    }

    #[test]
    fn protocol_mapping_is_bijective_for_supported_dialects() -> TestResult {
        for protocol in PROTOCOLS {
            let wire = protocol_to_wire(protocol)
                .ok_or_else(|| std::io::Error::other("missing wire dialect"))?;
            assert_eq!(wire_to_protocol(wire), protocol);
        }
        Ok(())
    }

    #[test]
    fn request_matrix_matches_cortexfs_capabilities() -> TestResult {
        for (source, target) in protocol_pairs() {
            let converted = transcode_request_protocol(source, target, fixture(source))?;
            assert!(serde_json::from_slice::<serde_json::Value>(&converted.bytes).is_ok());
            if source == target {
                assert_eq!(converted.path, BridgePath::Identity);
                assert_eq!(converted.bytes, fixture(source));
            } else if matches!(
                (source, target),
                (Protocol::OpenAi, Protocol::Gemini) | (Protocol::Gemini, Protocol::OpenAi)
            ) {
                assert_eq!(converted.path, BridgePath::Direct);
            } else {
                assert_eq!(converted.path, BridgePath::ViaIr);
            }
        }
        Ok(())
    }

    #[test]
    fn response_matrix_matches_cortexfs_capabilities() -> TestResult {
        const CHAT_RESPONSE: &[u8] = br#"{"id":"chat-run","model":"chat-model","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}"#;
        const RESPONSES_RESPONSE: &[u8] = br#"{"id":"responses-run","model":"responses-model","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":3,"output_tokens":2}}"#;
        const GEMINI_RESPONSE: &[u8] = br#"{"responseId":"gemini-run","modelVersion":"gemini-model","candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2}}"#;
        const ANTHROPIC_RESPONSE: &[u8] = br#"{"id":"anthropic-run","model":"claude-model","role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}"#;

        let response_fixture = |protocol: Protocol| match protocol {
            Protocol::OpenAi => CHAT_RESPONSE,
            Protocol::OpenAiResponses => RESPONSES_RESPONSE,
            Protocol::Claude => ANTHROPIC_RESPONSE,
            Protocol::Gemini => GEMINI_RESPONSE,
        };

        for (source, target) in protocol_pairs() {
            let converted = transcode_response_protocol(source, target, response_fixture(source))?;
            assert!(serde_json::from_slice::<serde_json::Value>(&converted.bytes).is_ok());
            if source == target {
                assert_eq!(converted.path, BridgePath::Identity);
            } else {
                assert_eq!(converted.path, BridgePath::ViaIr);
            }
        }
        Ok(())
    }

    #[test]
    fn converter_ids_are_stable_and_unique() {
        let mut seen = std::collections::BTreeSet::new();
        for (source, target) in protocol_pairs() {
            for direction in [Direction::Request, Direction::Response, Direction::Stream] {
                assert!(seen.insert(converter_id(source, target, direction)));
            }
            assert!(seen.insert(stream_finalizer_id(source, target)));
            assert!(seen.insert(runtime_adaptor_id(source, target)));
        }
    }

    #[test]
    fn model_event_start_and_done_map_to_canonical_terminal_sequence() {
        let mut state = StreamMapState::default();
        let start = model_event_to_canonical(
            &ModelEvent::Start {
                run: "run-1".to_owned(),
                model: "model".to_owned(),
            },
            &mut state,
        );
        assert!(matches!(
            start.as_slice(),
            [CanonicalStreamEvent::ResponseStart { .. }]
        ));
        let done = model_event_to_canonical(
            &ModelEvent::Done {
                run: "run-1".to_owned(),
                status: EventStatus::Ok,
            },
            &mut state,
        );
        assert!(matches!(
            done.as_slice(),
            [CanonicalStreamEvent::ResponseEnd { .. }]
        ));
    }

    #[test]
    fn stream_registry_exposes_only_cross_protocol_pairs() {
        let registry = CortexFsStreamAdaptorRegistry;
        assert!(
            registry
                .for_route(Protocol::OpenAi, Protocol::OpenAi)
                .is_none()
        );
        assert!(
            registry
                .for_route(Protocol::OpenAi, Protocol::Claude)
                .is_some()
        );
    }
}
