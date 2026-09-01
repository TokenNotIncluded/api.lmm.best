//! Source-level acceptance coverage for the protocol IR, converters, SSE
//! framing, rollout boundary, and bounded observability.
//!
//! The corpus is deliberately embedded in Rust so this test remains useful in
//! an offline checkout.  It is semantic coverage, rather than a replacement
//! for the Go oracle or a network integration test.

use std::{
    panic::{AssertUnwindSafe, catch_unwind},
    sync::Arc,
    thread,
    time::{Duration, Instant},
};

use lmm_api_rs::{
    conversion_observability::{
        ClientAbortGuard, ConversionObserver, ConversionResult, ConverterVersion, FailureReason,
        FeatureClass, MetricKind, MetricLabels, StreamTiming,
    },
    protocol_differential_gate::{
        DifferentialEvidenceBundle, DifferentialEvidenceDocument, EVIDENCE_SCHEMA_VERSION,
        MAX_EVIDENCE_JSON_BYTES,
    },
    protocol_rollout::{
        CanaryStage, ConverterPairOverride, DecisionSource, FlagConfig, FlagOverride,
        LocalConversionError, LocalConversionSummary, LocalRequest, MAX_BASIS_POINTS,
        PARSE_ERROR_RATE_PAUSE_PERCENTAGE_POINTS, ProtocolRolloutConfig, ProtocolRolloutControl,
        RollbackAction, RollbackReason, RollbackSignals, RolloutContext, RolloutFlag,
        RolloutSelector, ShadowDifference, ShadowRunner, TTFT_P95_PAUSE_PERCENT,
        bucket_is_in_rollout, evaluate_rollback, stable_bucket, validate_canary_stage,
    },
    protocol_route_gate::{RouteGateDecision, decide_route},
    protocol_runtime_registry::{current_support_matrix, validated_current_registry},
    protocol_stream_pipeline::{
        StreamAdaptor, StreamAdaptorOutput, StreamAdaptorRegistry, StreamAdaptorSession,
        StreamFrameOutput, StreamSessionSpec, StreamSetupFailure, TypedStreamFailure,
        compile_stream_session, compile_stream_session_with_adaptors,
    },
    route_ownership::{
        DifferentialClass, MIN_REVIEW_CANARY_BASIS_POINTS, OwnershipBlocker, OwnershipDecision,
        OwnershipEvidence, OwnershipGate, RouteOwnershipScope,
    },
    routes::sse::{
        DEFAULT_MAX_FRAME_BYTES, SseError, SseFrame, SseFrameParser, UnknownEventAction,
        UnknownEventClass, json_events_from_frames, parse_sse_frames, parse_sse_frames_lenient,
        parse_sse_frames_rejecting_unterminated, unknown_event_decision,
    },
};
use lmm_contracts::relay::{
    CacheUsage, CanonicalContent, CanonicalResponse, ClaudeRequest, ClaudeResponse,
    ClaudeStreamSemanticEvent, ClaudeStreamSnapshot, Direction, Envelope, Feature, FinishReason,
    FunctionData, GeminiRequest, GeminiResponse, GeminiStreamSnapshot, Item, ItemKind, JsonData,
    Loss, LossCode, Media, MediaKind, Money, OpaqueId, OpaqueIdProvenance, OpaqueProviderState,
    OpaqueStateProvenance, OpenAiChatRequest, OpenAiChatResponse, OpenAiResponsesRequest,
    OpenAiStreamSnapshot, Part, PartKind, Protocol, Provenance, RelayConvertError,
    ResponsesResponse, ResponsesStreamSnapshot, Role, SemanticBillingUsage, SemanticUsage,
    TokenUsage, Tool, ToolChoice, canonical_request_to_claude,
    canonical_request_to_gemini_for_model, canonical_request_to_openai_chat,
    canonical_request_to_openai_responses, canonical_response_to_claude,
    canonical_response_to_gemini, claude_request_to_canonical, claude_response_to_canonical,
    claude_stream_to_semantic_events, gemini_request_to_canonical_for_model,
    gemini_response_to_canonical_for_model, gemini_stream_to_canonical,
    openai_chat_request_to_canonical, openai_chat_response_to_canonical,
    openai_responses_request_to_canonical, openai_responses_response_to_canonical,
    openai_stream_to_canonical, preflight_openai_responses_request_to_openai_chat, protocols,
    responses_stream_to_canonical,
};

const CHAT_REQUEST: &str = r#"{
  "model":"gpt-test",
  "messages":[
    {"role":"user","content":"hello"},
    {"role":"assistant","tool_calls":[{"id":"call-chat","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]},
    {"role":"tool","tool_call_id":"call-chat","content":"ok"}
  ]
}"#;

const RESPONSES_REQUEST: &str = r#"{
  "model":"responses-test",
  "input":[
    {"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},
    {"type":"function_call","call_id":"call-responses","name":"lookup","arguments":"{}"},
    {"type":"function_call_output","call_id":"call-responses","output":"ok"}
  ]
}"#;

const GEMINI_REQUEST: &str = r#"{
  "contents":[
    {"role":"model","parts":[{"functionCall":{"id":"call-gemini","name":"lookup","args":{"q":"x"}},"thoughtSignature":"sig-gemini"}]},
    {"role":"user","parts":[{"functionResponse":{"id":"call-gemini","name":"lookup","response":{"ok":true}}}]}
  ]
}"#;

const CLAUDE_REQUEST: &str = r#"{
  "model":"claude-test",
  "max_tokens":128,
  "messages":[
    {"role":"assistant","content":[{"type":"thinking","thinking":"plan","signature":"sig-claude"},{"type":"tool_use","id":"call-claude","name":"lookup","input":{}}]},
    {"role":"user","content":[{"type":"tool_result","tool_use_id":"call-claude","content":"ok"}]}
  ]
}"#;

const CHAT_RESPONSE: &str = r#"{
  "id":"chat-response",
  "object":"chat.completion",
  "created":1,
  "model":"gpt-test",
  "choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],
  "usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3,"prompt_tokens_details":{"cached_tokens":1}}
}"#;

const RESPONSES_RESPONSE: &str = r#"{
  "id":"responses-response",
  "object":"response",
  "status":"completed",
  "model":"responses-test",
  "output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer"}]}],
  "usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}
}"#;

const CLAUDE_RESPONSE: &str = r#"{
  "id":"claude-response",
  "type":"message",
  "role":"assistant",
  "model":"claude-test",
  "content":[{"type":"thinking","thinking":"plan","signature":"sig-claude"},{"type":"text","text":"answer"}],
  "stop_reason":"end_turn",
  "usage":{"input_tokens":2,"output_tokens":1}
}"#;

const GEMINI_RESPONSE: &str = r#"{
  "candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"text":"answer","thoughtSignature":"sig-gemini"}]}}],
  "usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}
}"#;

const CHAT_STREAM: &str = r#"{
  "events":[
    {"id":"chat-stream","model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":"a"}}]},
    {"id":"chat-stream","model":"gpt-test","choices":[{"index":0,"delta":{"content":"b"}}]},
    {"id":"chat-stream","model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}
  ],
  "usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
}"#;

const RESPONSES_STREAM: &str = r#"{
  "events":[
    {"Type":"response.created","Payload":{"type":"response.created","response":{"id":"responses-stream","object":"response","status":"in_progress","model":"responses-test","output":[]}}},
    {"Type":"response.output_item.added","Payload":{"type":"response.output_item.added","item":{"type":"message","id":"responses-stream_msg_0","status":"in_progress","role":"assistant","content":[]},"output_index":0}},
    {"Type":"response.output_text.delta","Payload":{"type":"response.output_text.delta","delta":"ab","output_index":0,"content_index":0,"item_id":"responses-stream_msg_0"}},
    {"Type":"response.output_text.done","Payload":{"type":"response.output_text.done","text":"ab","output_index":0,"content_index":0,"item_id":"responses-stream_msg_0"}},
    {"Type":"response.output_item.done","Payload":{"type":"response.output_item.done","item":{"type":"message","id":"responses-stream_msg_0","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ab"}]},"output_index":0}},
    {"Type":"response.completed","Payload":{"type":"response.completed","response":{"id":"responses-stream","object":"response","status":"completed","model":"responses-test","output":[{"type":"message","id":"responses-stream_msg_0","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ab"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}}
  ],
  "usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}
}"#;

const GEMINI_STREAM: &str = r#"{
  "events":[
    {"candidates":[{"content":{"role":"model","parts":[{"text":"ab","thoughtSignature":"sig-stream"}]}}]},
    {"candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[]}}]}
  ],
  "usage":{}
}"#;

const CLAUDE_STREAM: &str = r#"{
  "events":[
    {"type":"message_start","message":{"id":"claude-stream","type":"message","role":"assistant","model":"claude-test","content":[]}},
    {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":"sig-stream"}},
    {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"plan"}},
    {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-stream"}},
    {"type":"content_block_stop","index":0},
    {"type":"ping"},
    {"type":"message_delta","delta":{"stop_reason":"end_turn"}},
    {"type":"future_event","index":7,"provider_field":"retained"},
    {"type":"message_stop"}
  ],
  "usage":{}
}"#;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
struct CorpusMetadata {
    source_url: &'static str,
    source_vendor: &'static str,
    retrieved_date: &'static str,
    model_family: &'static str,
    feature: &'static str,
    expected_fidelity: &'static str,
}

const CORPUS_METADATA: [CorpusMetadata; 4] = [
    CorpusMetadata {
        source_url: "https://platform.openai.com/docs/api-reference/chat",
        source_vendor: "OpenAI",
        retrieved_date: "2026-08-12",
        model_family: "openai-chat",
        feature: "ordered tool calls and usage",
        expected_fidelity: "exact typed subset",
    },
    CorpusMetadata {
        source_url: "https://platform.openai.com/docs/api-reference/responses",
        source_vendor: "OpenAI",
        retrieved_date: "2026-08-12",
        model_family: "openai-responses",
        feature: "ordered input items and call_id",
        expected_fidelity: "exact typed subset",
    },
    CorpusMetadata {
        source_url: "https://ai.google.dev/api/generate-content",
        source_vendor: "Google",
        retrieved_date: "2026-08-12",
        model_family: "gemini-2.5",
        feature: "function IDs and thought signatures",
        expected_fidelity: "exact with opaque state",
    },
    CorpusMetadata {
        source_url: "https://docs.anthropic.com/en/api/messages",
        source_vendor: "Anthropic",
        retrieved_date: "2026-08-12",
        model_family: "claude-messages",
        feature: "ordered thinking signatures and tool use",
        expected_fidelity: "exact with opaque state",
    },
];

const ARBITRARY_SSE_BYTES: &[u8] = &[
    0, 255, 10, 13, b':', b'd', b'a', b't', b'a', b':', 0, b'\n', b'\n', 0xef, 0xbb, 0xbf, b'e',
    b'v', b'e', b'n', b't', b':', b'f', b'u', b't', b'u', b'r', b'e', b'\r', b'\n', b'd', b'a',
    b't', b'a', b':', b' ', 0xff, b'\n', b'\n', b'r', b'e', b't', b'r', b'y', b':', b' ', b'+',
    b'1', b'\n', b'\n',
];

#[test]
fn corpus_metadata_is_complete_and_deterministic() {
    assert_eq!(CORPUS_METADATA.len(), 4);
    for metadata in CORPUS_METADATA {
        assert!(metadata.source_url.starts_with("https://"));
        assert!(!metadata.source_vendor.is_empty());
        assert_eq!(metadata.retrieved_date.len(), 10);
        assert!(metadata.retrieved_date.as_bytes()[4] == b'-');
        assert!(metadata.retrieved_date.as_bytes()[7] == b'-');
        assert!(!metadata.model_family.is_empty());
        assert!(!metadata.feature.is_empty());
        assert!(!metadata.expected_fidelity.is_empty());
    }
}

#[test]
fn request_conformance_preserves_order_ids_and_provider_signatures() {
    let chat: OpenAiChatRequest = serde_json::from_str(CHAT_REQUEST).expect("Chat corpus");
    let chat = openai_chat_request_to_canonical(chat).expect("Chat conversion");
    assert_eq!(chat.value.model, "gpt-test");
    assert_eq!(chat.value.messages.len(), 3);
    assert!(
        chat.value
            .messages
            .iter()
            .flat_map(|message| &message.parts)
            .any(|part| matches!(part, CanonicalContent::ToolCall { id, .. } if id == "call-chat"))
    );
    assert!(
        chat.value
            .messages
            .iter()
            .any(|message| message.role == lmm_contracts::relay::Role::Tool)
    );

    let responses: OpenAiResponsesRequest =
        serde_json::from_str(RESPONSES_REQUEST).expect("Responses corpus");
    let responses = openai_responses_request_to_canonical(responses).expect("Responses conversion");
    assert_eq!(responses.value.messages.len(), 3);
    assert!(responses
        .value
        .messages
        .iter()
        .flat_map(|message| &message.parts)
        .any(
            |part| matches!(part, CanonicalContent::ToolCall { id, .. } if id == "call-responses")
        ));

    let gemini: GeminiRequest = serde_json::from_str(GEMINI_REQUEST).expect("Gemini corpus");
    let gemini =
        gemini_request_to_canonical_for_model(gemini, "gemini-2.5-pro").expect("Gemini conversion");
    assert_eq!(gemini.value.messages.len(), 2);
    assert!(
        gemini
            .value
            .messages
            .iter()
            .flat_map(|message| &message.parts)
            .any(
                |part| matches!(part, CanonicalContent::ToolCall { id, .. } if id == "call-gemini")
            )
    );
    assert!(gemini.value.messages.iter().flat_map(|message| &message.parts).any(
        |part| matches!(part, CanonicalContent::ProviderState { state } if state.raw == JsonData::String("sig-gemini".to_owned()))
    ));

    let claude: ClaudeRequest = serde_json::from_str(CLAUDE_REQUEST).expect("Claude corpus");
    let claude = claude_request_to_canonical(claude).expect("Claude conversion");
    assert_eq!(claude.value.messages.len(), 2);
    assert!(matches!(
        claude.value.messages[0].parts.first(),
        Some(CanonicalContent::ClaudeThinking { thinking, signature: Some(signature), .. })
            if thinking == "plan" && signature == "sig-claude"
    ));
    assert!(
        claude
            .value
            .messages
            .iter()
            .flat_map(|message| &message.parts)
            .any(
                |part| matches!(part, CanonicalContent::ToolCall { id, .. } if id == "call-claude")
            )
    );
}

#[test]
fn responses_request_round_trip_preserves_input_tool_order_and_fields() {
    let source: OpenAiResponsesRequest = serde_json::from_str(
        r#"{
          "model":"responses-order",
          "input":[
            {"role":"user","content":"hello"},
            {"type":"function_call","call_id":"call-order","name":"lookup","arguments":"{\"q\":\"x\"}"},
            {"type":"function_call_output","call_id":"call-order","output":{"ok":true}}
          ],
          "tools":[{"type":"function","name":"lookup","description":"lookup tool","parameters":{"type":"object","properties":{"q":{"type":"string"}}},"strict":true}]
        }"#,
    )
    .expect("ordered Responses request");
    let canonical =
        openai_responses_request_to_canonical(source).expect("Responses request conversion");
    assert!(matches!(
        canonical.value.messages.as_slice(),
        [
            message,
            call,
            result,
        ] if message.role == Role::User
            && matches!(message.parts.as_slice(), [CanonicalContent::Text { text }] if text == "hello")
            && call.role == Role::Assistant
            && matches!(call.parts.as_slice(), [CanonicalContent::ToolCall { id, name, arguments }] if id == "call-order" && name == "lookup" && arguments == "{\"q\":\"x\"}")
            && result.role == Role::Tool
            && matches!(result.parts.as_slice(), [CanonicalContent::ToolResult { id, output, .. }] if id == "call-order" && *output == JsonData::Object([("ok".to_owned(), JsonData::Bool(true))].into_iter().collect()))
    ));

    let round_trip = canonical_request_to_openai_responses(canonical.value)
        .expect("Responses request round trip");
    let items = match round_trip.value.input.expect("Responses input") {
        lmm_contracts::relay::ResponsesInput::Items(items) => items,
        lmm_contracts::relay::ResponsesInput::String(_)
        | lmm_contracts::relay::ResponsesInput::Json(_) => panic!("Responses input was flattened"),
    };
    // The canonical model keeps the assistant call as a separate message;
    // the Responses encoder therefore emits one structural assistant item
    // before the standalone function_call item.  The call/result items and
    // their relative order remain exact.
    assert_eq!(
        items
            .iter()
            .map(|item| item.kind.as_deref())
            .collect::<Vec<_>>(),
        vec![
            None,
            None,
            Some("function_call"),
            Some("function_call_output")
        ]
    );
    assert_eq!(items[0].role.as_deref(), Some("user"));
    assert!(matches!(
        items[0].content.as_ref(),
        Some(lmm_contracts::relay::StringOrParts::String(text)) if text == "hello"
    ));
    assert_eq!(items[2].call_id.as_deref(), Some("call-order"));
    assert_eq!(items[2].name.as_deref(), Some("lookup"));
    assert_eq!(items[2].arguments.as_deref(), Some("{\"q\":\"x\"}"));
    assert_eq!(items[3].call_id.as_deref(), Some("call-order"));
    assert_eq!(
        items[3].output,
        Some(JsonData::Object(
            [("ok".to_owned(), JsonData::Bool(true))]
                .into_iter()
                .collect()
        ))
    );
    assert_eq!(round_trip.value.tools.len(), 1);
    assert_eq!(round_trip.value.tools[0].name.as_deref(), Some("lookup"));
    assert_eq!(round_trip.value.tools[0].strict, Some(true));
}

#[test]
fn request_round_trip_keeps_authentic_tool_and_signature_data() {
    let chat: OpenAiChatRequest = serde_json::from_str(CHAT_REQUEST).expect("Chat corpus");
    let canonical = openai_chat_request_to_canonical(chat).expect("Chat conversion");
    let round_trip = canonical_request_to_openai_chat(canonical.value).expect("Chat round trip");
    assert_eq!(round_trip.value.messages[1].tool_calls[0].id, "call-chat");
    assert!(round_trip.loss.is_lossless());

    let gemini: GeminiRequest = serde_json::from_str(GEMINI_REQUEST).expect("Gemini corpus");
    let canonical =
        gemini_request_to_canonical_for_model(gemini, "gemini-2.5-pro").expect("Gemini conversion");
    let round_trip =
        canonical_request_to_gemini_for_model(canonical.value, "gemini-2.5-pro", false)
            .expect("Gemini round trip");
    let function_call = round_trip.value.contents[0].parts[0]
        .function_call
        .as_ref()
        .expect("function call");
    assert_eq!(function_call.id.as_deref(), Some("call-gemini"));
    assert_eq!(
        round_trip.value.contents[0].parts[0]
            .thought_signature
            .as_deref(),
        Some("sig-gemini")
    );

    let claude: ClaudeRequest = serde_json::from_str(CLAUDE_REQUEST).expect("Claude corpus");
    let canonical = claude_request_to_canonical(claude).expect("Claude conversion");
    let round_trip = canonical_request_to_claude(canonical.value).expect("Claude round trip");
    let first_message = &round_trip.value.messages[0];
    let blocks = match &first_message.content {
        lmm_contracts::relay::StringOrParts::Parts(parts) => parts,
        lmm_contracts::relay::StringOrParts::String(_) => panic!("Claude blocks flattened"),
    };
    assert_eq!(blocks[0].signature.as_deref(), Some("sig-claude"));
    assert_eq!(blocks[1].id.as_deref(), Some("call-claude"));
}

#[test]
fn public_converters_reject_unmapped_boundary_fields_with_paths() {
    let response: ResponsesResponse = serde_json::from_str(
        r#"{"id":"resp-fields","object":"response","status":"completed","model":"gpt-test","output":[{"type":"future_output"}]}"#,
    )
    .expect("unknown Responses output item");
    let error = openai_responses_response_to_canonical(response)
        .expect_err("unknown output must not be silently dropped");
    let RelayConvertError::UnsupportedFeature(error) = error else {
        panic!("expected typed output feature error");
    };
    assert_eq!(error.feature, "unknown_output_item_type");
    assert_eq!(error.path, "output[0]");

    let request: OpenAiResponsesRequest = serde_json::from_str(
        r#"{"model":"gpt-test","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"x","image_url":"https://example.test/image.png"}]}]}"#,
    )
    .expect("Responses content extension");
    let error = openai_responses_request_to_canonical(request)
        .expect_err("unmapped input content must be rejected");
    let RelayConvertError::UnsupportedFeature(error) = error else {
        panic!("expected typed input content feature error");
    };
    assert_eq!(error.feature, "content_field");
    assert_eq!(error.path, "input[0].content[0].image_url");

    let request: OpenAiResponsesRequest = serde_json::from_str(
        r#"{"model":"gpt-test","tools":[{"type":"web_search","search_context_size":"high"}]}"#,
    )
    .expect("Responses built-in tool");
    let error = preflight_openai_responses_request_to_openai_chat(&request)
        .expect_err("built-in tool must not cross into Chat as a function");
    let RelayConvertError::UnsupportedFeature(error) = error else {
        panic!("expected typed tool feature error");
    };
    assert_eq!(error.feature, "builtin_web_search");
    assert_eq!(error.path, "tools[0]");

    let chat: OpenAiChatRequest =
        serde_json::from_str(r#"{"model":"gpt-test","messages":[],"n":2}"#)
            .expect("Chat multiplicity option");
    let error = openai_chat_request_to_canonical(chat)
        .expect_err("Chat n > 1 must not be silently reduced to one response");
    assert!(matches!(
        error,
        RelayConvertError::Unsupported(message) if message.contains("n > 1")
    ));
}

#[test]
fn claude_public_converter_preserves_ordered_text_and_tool_blocks() {
    let request: ClaudeRequest = serde_json::from_str(
        r#"{
          "model":"claude-order",
          "max_tokens":128,
          "messages":[
            {"role":"assistant","content":[
              {"type":"text","text":"before"},
              {"type":"tool_use","id":"call-order","name":"lookup","input":{"q":"x"}},
              {"type":"text","text":"after"}
            ]},
            {"role":"user","content":[
              {"type":"tool_result","tool_use_id":"call-order","content":{"ok":true}}
            ]}
          ]
        }"#,
    )
    .expect("ordered Claude request");
    let canonical = claude_request_to_canonical(request).expect("Claude request conversion");
    assert!(matches!(
        canonical.value.messages[0].parts.as_slice(),
        [
            CanonicalContent::Text { text: before },
            CanonicalContent::ToolCall { id, name, .. },
            CanonicalContent::Text { text: after },
        ] if before == "before" && id == "call-order" && name == "lookup" && after == "after"
    ));
    assert!(matches!(
        canonical.value.messages[1].parts.as_slice(),
        [CanonicalContent::ToolResult { id, .. }] if id == "call-order"
    ));

    let round_trip =
        canonical_request_to_claude(canonical.value).expect("Claude ordered request round trip");
    let first_blocks = match &round_trip.value.messages[0].content {
        lmm_contracts::relay::StringOrParts::Parts(blocks) => blocks,
        lmm_contracts::relay::StringOrParts::String(_) => {
            panic!("Claude content blocks were flattened")
        }
    };
    assert_eq!(
        first_blocks
            .iter()
            .map(|block| block.kind.as_str())
            .collect::<Vec<_>>(),
        vec!["text", "tool_use", "text"]
    );
    assert_eq!(first_blocks[1].id.as_deref(), Some("call-order"));
    assert_eq!(first_blocks[2].text.as_deref(), Some("after"));
    let result_block = match &round_trip.value.messages[1].content {
        lmm_contracts::relay::StringOrParts::Parts(blocks) => {
            blocks.first().expect("Claude tool result block")
        }
        lmm_contracts::relay::StringOrParts::String(_) => {
            panic!("Claude tool result was flattened")
        }
    };
    assert_eq!(result_block.kind, "tool_result");
    assert_eq!(result_block.tool_use_id.as_deref(), Some("call-order"));
}

#[test]
fn gemini_public_converter_rejects_mixed_text_and_tool_content() {
    let request: GeminiRequest = serde_json::from_str(
        r#"{"contents":[{"role":"model","parts":[{"text":"prefix","functionCall":{"id":"call-mixed","name":"lookup","args":{}}}]}]}"#,
    )
    .expect("mixed Gemini part");
    let error = gemini_request_to_canonical_for_model(request, "gemini-3-pro")
        .expect_err("mixed text and function call must be rejected");
    assert!(matches!(
        error,
        RelayConvertError::Unsupported(message)
            if message.contains("multiple content payloads")
    ));
}

#[test]
fn gemini3_function_call_id_and_authentic_signature_round_trip() {
    const MODEL: &str = "gemini-3-pro";
    const CALL_ID: &str = "call-gemini-3";
    const SIGNATURE: &str = "authentic-gemini-3-signature";

    let request: GeminiRequest = serde_json::from_value(serde_json::json!({
        "contents": [{
            "role": "model",
            "parts": [{
                "functionCall": {
                    "id": CALL_ID,
                    "name": "lookup",
                    "args": {"q": "rust"}
                },
                "thoughtSignature": SIGNATURE
            }]
        }]
    }))
    .expect("Gemini 3 request");
    let canonical =
        gemini_request_to_canonical_for_model(request, MODEL).expect("Gemini 3 request conversion");
    assert!(matches!(
        canonical.value.messages[0].parts.as_slice(),
        [
            CanonicalContent::ToolCall { id, .. },
            CanonicalContent::ProviderState { state }
        ] if id == CALL_ID
            && state.raw == JsonData::String(SIGNATURE.to_owned())
            && state.provenance == OpaqueStateProvenance::Authentic
    ));
    let round_trip = canonical_request_to_gemini_for_model(canonical.value, MODEL, false)
        .expect("Gemini 3 request round trip");
    assert_eq!(
        round_trip.value.contents[0].parts[0]
            .function_call
            .as_ref()
            .and_then(|call| call.id.as_deref()),
        Some(CALL_ID)
    );
    assert_eq!(
        round_trip.value.contents[0].parts[0]
            .thought_signature
            .as_deref(),
        Some(SIGNATURE)
    );

    let response: GeminiResponse = serde_json::from_value(serde_json::json!({
        "candidates": [{
            "index": 0,
            "finishReason": "STOP",
            "content": {
                "role": "model",
                "parts": [{
                    "functionCall": {
                        "id": CALL_ID,
                        "name": "lookup",
                        "args": {"q": "rust"}
                    },
                    "thoughtSignature": SIGNATURE
                }]
            }
        }]
    }))
    .expect("Gemini 3 response");
    let canonical = gemini_response_to_canonical_for_model(response, MODEL)
        .expect("Gemini 3 response conversion");
    assert!(matches!(
        canonical.value.output.as_slice(),
        [
            CanonicalContent::ToolCall { id, .. },
            CanonicalContent::ProviderState { state }
        ] if id == CALL_ID
            && state.raw == JsonData::String(SIGNATURE.to_owned())
            && state.provenance == OpaqueStateProvenance::Authentic
    ));
    let round_trip =
        canonical_response_to_gemini(canonical.value).expect("Gemini 3 response round trip");
    assert_eq!(
        round_trip.value.candidates[0].content.parts[0]
            .function_call
            .as_ref()
            .and_then(|call| call.id.as_deref()),
        Some(CALL_ID)
    );
    assert_eq!(
        round_trip.value.candidates[0].content.parts[0]
            .thought_signature
            .as_deref(),
        Some(SIGNATURE)
    );
}

#[test]
fn gemini3_stream_keeps_function_id_and_authentic_signature_before_finish() {
    const MODEL: &str = "gemini-3-pro";
    const CALL_ID: &str = "call-gemini-3-stream";
    const SIGNATURE: &str = "authentic-gemini-3-stream-signature";

    let snapshot: GeminiStreamSnapshot = serde_json::from_value(serde_json::json!({
        "events": [
            {
                "candidates": [{
                    "content": {
                        "role": "model",
                        "parts": [{
                            "functionCall": {
                                "id": CALL_ID,
                                "name": "lookup",
                                "args": {"q": "rust"}
                            },
                            "thoughtSignature": SIGNATURE
                        }]
                    }
                }]
            },
            {
                "candidates": [{
                    "finishReason": "STOP",
                    "content": {"role": "model", "parts": []}
                }]
            }
        ],
        "usage": {}
    }))
    .expect("Gemini 3 stream");
    let canonical =
        gemini_stream_to_canonical(&snapshot, MODEL).expect("Gemini 3 stream conversion");
    assert_eq!(canonical.value.finish_reason, Some(FinishReason::Stop));
    assert!(matches!(
        canonical.value.output.as_slice(),
        [
            CanonicalContent::ToolCall { id, .. },
            CanonicalContent::ProviderState { state }
        ] if id == CALL_ID
            && state.raw == JsonData::String(SIGNATURE.to_owned())
            && state.provenance == OpaqueStateProvenance::Authentic
    ));
    let semantic_round_trip =
        canonical_response_to_gemini(canonical.value).expect("Gemini 3 stream semantic round trip");
    assert_eq!(
        semantic_round_trip.value.candidates[0].content.parts[0]
            .function_call
            .as_ref()
            .and_then(|call| call.id.as_deref()),
        Some(CALL_ID)
    );
    assert_eq!(
        semantic_round_trip.value.candidates[0].content.parts[0]
            .thought_signature
            .as_deref(),
        Some(SIGNATURE)
    );
}

#[test]
fn gemini_missing_signature_never_becomes_implicit_authentic_history() {
    let request: OpenAiChatRequest = serde_json::from_value(serde_json::json!({
        "model": "openai-history",
        "messages": [{
            "role": "assistant",
            "tool_calls": [{
                "id": "history-call",
                "type": "function",
                "function": {"name": "lookup", "arguments": "{}"}
            }]
        }]
    }))
    .expect("OpenAI history request");
    let canonical = openai_chat_request_to_canonical(request).expect("history conversion");

    let native_history =
        canonical_request_to_gemini_for_model(canonical.value.clone(), "gemini-2.5-pro", false)
            .expect("Gemini native history");
    assert_eq!(
        native_history.value.contents[0].parts[0]
            .thought_signature
            .as_deref(),
        None
    );
    assert!(
        !native_history
            .loss
            .synthetic_fields
            .contains(&"SYNTHETIC_THOUGHT_SIGNATURE")
    );

    let synthetic_history =
        canonical_request_to_gemini_for_model(canonical.value.clone(), "gemini-2.5-pro", true)
            .expect("Gemini synthetic history");
    assert_eq!(
        synthetic_history.value.contents[0].parts[0]
            .thought_signature
            .as_deref(),
        Some("context_engineering_is_the_way_to_go")
    );
    assert!(
        synthetic_history
            .loss
            .synthetic_fields
            .contains(&"SYNTHETIC_THOUGHT_SIGNATURE")
    );

    let error = canonical_request_to_gemini_for_model(canonical.value, "gemini-3-pro", false)
        .expect_err("Gemini 3 must reject missing authentic signature");
    assert!(error.to_string().contains("missing thoughtSignature"));
}

#[test]
fn claude_thinking_redacted_and_reasoning_provenance_stay_distinct() {
    let opaque_data: JsonData = serde_json::from_value(serde_json::json!({
        "ciphertext": "opaque-redacted",
        "version": 1
    }))
    .expect("opaque redacted payload");
    let request: ClaudeRequest = serde_json::from_value(serde_json::json!({
        "model": "claude-3-7-sonnet",
        "max_tokens": 256,
        "messages": [{
            "role": "assistant",
            "content": [
                {
                    "type": "thinking",
                    "thinking": "private plan",
                    "signature": "opaque-thinking-signature"
                },
                {"type": "redacted_thinking", "data": opaque_data.clone()},
                {"type": "text", "text": "answer"}
            ]
        }]
    }))
    .expect("Claude provenance request");
    let canonical = claude_request_to_canonical(request).expect("Claude provenance conversion");
    assert!(matches!(
        canonical.value.messages[0].parts.as_slice(),
        [
            CanonicalContent::ClaudeThinking {
                thinking,
                signature: Some(signature),
                provenance: OpaqueStateProvenance::Authentic,
                ..
            },
            CanonicalContent::RedactedThinking {
                data,
                provenance: OpaqueStateProvenance::Authentic,
                ..
            },
            CanonicalContent::Text { text }
        ] if thinking == "private plan"
            && signature == "opaque-thinking-signature"
            && data == &opaque_data
            && text == "answer"
    ));
    let round_trip =
        canonical_request_to_claude(canonical.value).expect("Claude provenance round trip");
    let blocks = match &round_trip.value.messages[0].content {
        lmm_contracts::relay::StringOrParts::Parts(parts) => parts,
        lmm_contracts::relay::StringOrParts::String(_) => {
            panic!("Claude provenance blocks flattened")
        }
    };
    assert_eq!(blocks.len(), 3);
    assert_eq!(blocks[0].kind, "thinking");
    assert_eq!(blocks[0].thinking.as_deref(), Some("private plan"));
    assert_eq!(
        blocks[0].signature.as_deref(),
        Some("opaque-thinking-signature")
    );
    assert_eq!(blocks[1].kind, "redacted_thinking");
    assert_eq!(blocks[1].data.as_ref(), Some(&opaque_data));
    assert_eq!(blocks[2].kind, "text");

    let reasoning: OpenAiChatResponse = serde_json::from_value(serde_json::json!({
        "id": "reasoning-boundary",
        "object": "chat.completion",
        "created": 1,
        "model": "openai-reasoning",
        "choices": [{
            "index": 0,
            "message": {
                "role": "assistant",
                "content": "answer",
                "reasoning_content": "ordinary reasoning summary"
            },
            "finish_reason": "stop"
        }]
    }))
    .expect("OpenAI reasoning response");
    let reasoning =
        openai_chat_response_to_canonical(reasoning).expect("OpenAI reasoning conversion");
    assert!(reasoning
        .value
        .output
        .iter()
        .any(|part| matches!(part, CanonicalContent::Reasoning { text } if text == "ordinary reasoning summary")));
    assert!(
        !reasoning
            .value
            .output
            .iter()
            .any(|part| matches!(part, CanonicalContent::ClaudeThinking { .. }))
    );
    let claude =
        canonical_response_to_claude(reasoning.value).expect("ordinary reasoning to Claude");
    assert!(
        claude
            .loss
            .dropped_fields
            .contains(&"ordinary_reasoning->claude_text")
    );
    assert!(
        claude
            .value
            .content
            .iter()
            .all(|block| block.kind != "thinking")
    );

    let synthetic = CanonicalResponse {
        id: "synthetic-claude".to_owned(),
        model: "claude-3-7-sonnet".to_owned(),
        created_at: 0,
        output: vec![CanonicalContent::ClaudeThinking {
            thinking: "generated".to_owned(),
            signature: Some("not-provider-authentic".to_owned()),
            model: Some("claude-3-7-sonnet".to_owned()),
            provenance: OpaqueStateProvenance::Synthetic,
        }],
        finish_reason: None,
        usage: None,
    };
    assert!(
        canonical_response_to_claude(synthetic).is_err(),
        "synthetic Claude thinking must be rejected upstream"
    );
}

#[test]
fn response_conformance_preserves_usage_finish_order_and_opaque_state() {
    let chat: OpenAiChatResponse = serde_json::from_str(CHAT_RESPONSE).expect("Chat response");
    let chat = openai_chat_response_to_canonical(chat).expect("Chat response conversion");
    assert_eq!(chat.value.id, "chat-response");
    assert_eq!(chat.value.finish_reason, Some(FinishReason::Stop));
    assert_eq!(
        chat.value.usage.as_ref().map(|usage| usage.total_tokens),
        Some(3)
    );
    assert!(
        matches!(chat.value.output.first(), Some(CanonicalContent::Text { text }) if text == "answer")
    );
    let chat_wire = lmm_contracts::relay::canonical_response_to_openai_chat(chat.value);
    assert_eq!(chat_wire.value.id, "chat-response");

    let responses: ResponsesResponse =
        serde_json::from_str(RESPONSES_RESPONSE).expect("Responses response");
    let responses =
        openai_responses_response_to_canonical(responses).expect("Responses response conversion");
    assert_eq!(responses.value.finish_reason, Some(FinishReason::Stop));
    assert_eq!(
        responses
            .value
            .usage
            .as_ref()
            .map(|usage| usage.total_tokens),
        Some(3)
    );

    let claude: ClaudeResponse = serde_json::from_str(CLAUDE_RESPONSE).expect("Claude response");
    let claude = claude_response_to_canonical(claude).expect("Claude response conversion");
    assert!(claude.value.output.iter().any(
        |part| matches!(part, CanonicalContent::ClaudeThinking { signature: Some(signature), .. } if signature == "sig-claude")
    ));
    assert_eq!(
        claude.value.usage.as_ref().map(|usage| usage.total_tokens),
        Some(3)
    );
    let claude_wire =
        canonical_response_to_claude(claude.value).expect("Claude response round trip");
    assert_eq!(
        claude_wire.value.content[0].signature.as_deref(),
        Some("sig-claude")
    );

    let gemini: GeminiResponse = serde_json::from_str(GEMINI_RESPONSE).expect("Gemini response");
    let gemini = gemini_response_to_canonical_for_model(gemini, "gemini-2.5-pro")
        .expect("Gemini response conversion");
    assert_eq!(gemini.value.finish_reason, Some(FinishReason::Stop));
    assert_eq!(
        gemini.value.usage.as_ref().map(|usage| usage.total_tokens),
        Some(3)
    );
    assert!(gemini.value.output.iter().any(
        |part| matches!(part, CanonicalContent::ProviderState { state } if state.raw == JsonData::String("sig-gemini".to_owned()))
    ));
    let gemini_wire =
        canonical_response_to_gemini(gemini.value).expect("Gemini response round trip");
    assert_eq!(
        gemini_wire.value.candidates[0].content.parts[0]
            .thought_signature
            .as_deref(),
        Some("sig-gemini")
    );
}

fn semantic_usage_from_token_usage(usage: &TokenUsage) -> SemanticUsage {
    SemanticUsage {
        input_tokens: usage.input_tokens,
        output_tokens: usage.output_tokens,
        total_tokens: usage.total_tokens,
        reasoning_tokens: usage.reasoning_tokens,
        cache: CacheUsage {
            read_input_tokens: usage.cached_input_tokens,
            write_input_tokens: 0,
            creation_input_tokens: 0,
            extensions: Default::default(),
        },
        billing: None,
        extensions: Default::default(),
    }
}

#[test]
fn usage_cache_billing_differential_matches_legacy_and_new_semantics() {
    let chat: OpenAiChatResponse = serde_json::from_str(CHAT_RESPONSE).expect("Chat response");
    let chat = openai_chat_response_to_canonical(chat)
        .expect("Chat response conversion")
        .value;
    let responses: ResponsesResponse =
        serde_json::from_str(RESPONSES_RESPONSE).expect("Responses response");
    let responses = openai_responses_response_to_canonical(responses)
        .expect("Responses response conversion")
        .value;
    let claude: ClaudeResponse = serde_json::from_str(CLAUDE_RESPONSE).expect("Claude response");
    let claude = claude_response_to_canonical(claude)
        .expect("Claude response conversion")
        .value;
    let gemini: GeminiResponse = serde_json::from_str(GEMINI_RESPONSE).expect("Gemini response");
    let gemini = gemini_response_to_canonical_for_model(gemini, "gemini-2.5-pro")
        .expect("Gemini response conversion")
        .value;

    let cases = [
        ("openai-chat", chat.usage.expect("Chat usage"), (2, 1, 3, 1)),
        (
            "openai-responses",
            responses.usage.expect("Responses usage"),
            (2, 1, 3, 0),
        ),
        ("claude", claude.usage.expect("Claude usage"), (2, 1, 3, 0)),
        ("gemini", gemini.usage.expect("Gemini usage"), (2, 1, 3, 0)),
    ];
    for (name, new_usage, legacy_usage) in cases {
        let new_tuple = (
            new_usage.input_tokens,
            new_usage.output_tokens,
            new_usage.total_tokens,
        );
        assert_eq!(
            new_tuple,
            (legacy_usage.0, legacy_usage.1, legacy_usage.2),
            "legacy/new token usage: {name}"
        );
        let semantic = semantic_usage_from_token_usage(&new_usage);
        assert_eq!(
            semantic.input_tokens, legacy_usage.0,
            "semantic input: {name}"
        );
        assert_eq!(
            semantic.output_tokens, legacy_usage.1,
            "semantic output: {name}"
        );
        assert_eq!(
            semantic.total_tokens, legacy_usage.2,
            "semantic total: {name}"
        );
        assert_eq!(
            new_usage.cached_input_tokens, legacy_usage.3,
            "legacy cache: {name}"
        );
        assert_eq!(
            semantic.cache.read_input_tokens, legacy_usage.3,
            "semantic cache: {name}"
        );
        assert!(semantic.billing.is_none(), "billing absent: {name}");
    }
}

#[test]
fn cross_protocol_usage_and_billing_matrix_preserves_semantics() {
    let chat: OpenAiChatResponse = serde_json::from_value(serde_json::json!({
        "id": "usage-chat",
        "object": "chat.completion",
        "created": 1,
        "model": "gpt-usage",
        "choices": [{
            "index": 0,
            "message": {"role": "assistant", "content": "answer"},
            "finish_reason": "stop"
        }],
        "usage": {
            "prompt_tokens": 100,
            "completion_tokens": 40,
            "total_tokens": 140,
            "prompt_tokens_details": {"cached_tokens": 7},
            "completion_tokens_details": {"reasoning_tokens": 9}
        }
    }))
    .expect("OpenAI Chat usage DTO");
    let chat = openai_chat_response_to_canonical(chat).expect("OpenAI Chat usage conversion");
    assert!(chat.loss.is_lossless());

    let responses: ResponsesResponse = serde_json::from_value(serde_json::json!({
        "id": "usage-responses",
        "object": "response",
        "status": "completed",
        "model": "responses-usage",
        "output": [{
            "type": "message",
            "role": "assistant",
            "status": "completed",
            "content": [{"type": "output_text", "text": "answer"}]
        }],
        "usage": {
            "input_tokens": 100,
            "output_tokens": 40,
            "total_tokens": 140,
            "input_tokens_details": {"cached_tokens": 7},
            "completion_tokens_details": {"reasoning_tokens": 9}
        }
    }))
    .expect("OpenAI Responses usage DTO");
    let responses =
        openai_responses_response_to_canonical(responses).expect("Responses usage conversion");
    assert!(responses.loss.is_lossless());

    let claude: ClaudeResponse = serde_json::from_value(serde_json::json!({
        "id": "usage-claude",
        "type": "message",
        "role": "assistant",
        "model": "claude-usage",
        "content": [{"type": "text", "text": "answer"}],
        "stop_reason": "end_turn",
        "usage": {
            "input_tokens": 100,
            "output_tokens": 40,
            "cache_read_input_tokens": 7
        }
    }))
    .expect("Claude usage DTO");
    let claude = claude_response_to_canonical(claude).expect("Claude usage conversion");
    assert!(claude.loss.is_lossless());

    let gemini: GeminiResponse = serde_json::from_value(serde_json::json!({
        "candidates": [{
            "content": {"role": "model", "parts": [{"text": "answer"}]},
            "finishReason": "STOP"
        }],
        "usageMetadata": {
            "promptTokenCount": 100,
            "candidatesTokenCount": 40,
            "thoughtsTokenCount": 9,
            "totalTokenCount": 149,
            "cachedContentTokenCount": 7
        }
    }))
    .expect("Gemini usage DTO");
    let gemini = gemini_response_to_canonical_for_model(gemini, "gemini-usage")
        .expect("Gemini usage conversion");
    assert!(gemini.loss.is_lossless());

    let cases = [
        (
            "openai-chat",
            chat.value.usage.expect("Chat canonical usage"),
            (100_u64, 40, 140, 7, 9),
        ),
        (
            "openai-responses",
            responses.value.usage.expect("Responses canonical usage"),
            (100_u64, 40, 140, 7, 9),
        ),
        (
            "claude",
            claude.value.usage.expect("Claude canonical usage"),
            (100_u64, 40, 140, 7, 0),
        ),
        (
            "gemini",
            gemini.value.usage.expect("Gemini canonical usage"),
            (100_u64, 40, 149, 7, 9),
        ),
    ];

    for (name, usage, expected) in cases {
        assert_eq!(
            (
                usage.input_tokens,
                usage.output_tokens,
                usage.total_tokens,
                usage.cached_input_tokens,
                usage.reasoning_tokens,
            ),
            expected,
            "canonical usage matrix mismatch: {name}"
        );

        let mut semantic = semantic_usage_from_token_usage(&usage);
        semantic.cache.write_input_tokens = 3;
        semantic.cache.creation_input_tokens = 2;
        semantic.extensions.insert(
            "future_usage_counter".to_owned(),
            JsonData::Number(serde_json::Number::from(11)),
        );
        semantic.billing = Some(SemanticBillingUsage {
            source: Some(name.to_owned()),
            semantic: Some("provider_reported".to_owned()),
            input_tokens: expected.0,
            output_tokens: expected.1,
            cached_input_tokens: expected.3,
            total_tokens: expected.2,
            cost: Some(Money {
                amount: "0.000000123456789".to_owned(),
                currency: "USD".to_owned(),
            }),
            extensions: [("future_billing_counter".to_owned(), JsonData::Bool(true))]
                .into_iter()
                .collect(),
        });
        let encoded = serde_json::to_vec(&semantic).expect("semantic usage serializes");
        let decoded: SemanticUsage =
            serde_json::from_slice(&encoded).expect("semantic usage round trip");
        assert_eq!(decoded, semantic, "semantic usage changed: {name}");
        assert_eq!(
            decoded
                .billing
                .as_ref()
                .and_then(|billing| billing.cost.as_ref())
                .map(|cost| cost.amount.as_str()),
            Some("0.000000123456789"),
            "billing decimal changed: {name}"
        );
    }
}

#[test]
fn stream_conformance_preserves_order_unknown_events_and_cancel_class() {
    let chat: OpenAiStreamSnapshot = serde_json::from_str(CHAT_STREAM).expect("Chat stream");
    let chat_events = openai_stream_to_canonical(&chat);
    let chat_text = chat_events
        .iter()
        .filter_map(|event| match event {
            lmm_contracts::relay::CanonicalStreamEvent::TextDelta { delta, .. } => {
                Some(delta.as_str())
            }
            _ => None,
        })
        .collect::<Vec<_>>();
    assert_eq!(chat_text, vec!["a", "b"]);
    assert!(chat_events.iter().any(|event| matches!(
        event,
        lmm_contracts::relay::CanonicalStreamEvent::ResponseEnd {
            finish_reason: FinishReason::Stop,
            ..
        }
    )));

    let responses: ResponsesStreamSnapshot =
        serde_json::from_str(RESPONSES_STREAM).expect("Responses stream");
    let responses_events = responses_stream_to_canonical(&responses);
    assert!(responses_events.iter().any(|event| matches!(event, lmm_contracts::relay::CanonicalStreamEvent::TextDelta { delta, .. } if delta == "ab")));
    assert!(responses_events.iter().any(|event| matches!(
        event,
        lmm_contracts::relay::CanonicalStreamEvent::ResponseEnd {
            finish_reason: FinishReason::Stop,
            ..
        }
    )));

    let gemini: GeminiStreamSnapshot = serde_json::from_str(GEMINI_STREAM).expect("Gemini stream");
    let gemini =
        gemini_stream_to_canonical(&gemini, "gemini-2.5-pro").expect("Gemini stream conversion");
    assert!(
        gemini
            .value
            .output
            .iter()
            .any(|part| matches!(part, CanonicalContent::Text { text } if text == "ab"))
    );
    assert!(gemini.value.output.iter().any(|part| matches!(part, CanonicalContent::ProviderState { state } if state.raw == JsonData::String("sig-stream".to_owned()))));

    let claude: ClaudeStreamSnapshot = serde_json::from_str(CLAUDE_STREAM).expect("Claude stream");
    let claude_events =
        claude_stream_to_semantic_events(&claude).expect("Claude stream conversion");
    assert!(claude_events.iter().any(|event| matches!(event, ClaudeStreamSemanticEvent::SignatureDelta { signature, .. } if signature == "sig-stream")));
    assert!(claude_events.iter().any(|event| matches!(event, ClaudeStreamSemanticEvent::Unknown { kind, .. } if kind == "future_event")));
    assert!(
        claude_events
            .iter()
            .any(|event| matches!(event, ClaudeStreamSemanticEvent::Ping))
    );
    assert!(
        claude_events
            .iter()
            .any(|event| matches!(event, ClaudeStreamSemanticEvent::MessageStop))
    );

    let same_protocol = unknown_event_decision(true, Some("future_event"));
    assert_eq!(same_protocol.class, UnknownEventClass::Content);
    assert_eq!(same_protocol.action, UnknownEventAction::Preserve);
    assert_eq!(same_protocol.loss_code, None);
    let metadata = unknown_event_decision(false, Some("message_metadata"));
    assert_eq!(metadata.class, UnknownEventClass::Metadata);
    assert_eq!(metadata.action, UnknownEventAction::RecordLossAndContinue);
    assert!(metadata.loss_code.is_some());
    let termination = unknown_event_decision(false, Some("future_complete"));
    assert_eq!(termination.class, UnknownEventClass::Termination);
    assert_eq!(termination.action, UnknownEventAction::DegradedOrError);

    for event_name in ["response.metadata", "response-usage", "metadata.update"] {
        let metadata = unknown_event_decision(false, Some(event_name));
        assert_eq!(metadata.class, UnknownEventClass::Metadata, "{event_name}");
        assert_eq!(
            metadata.action,
            UnknownEventAction::RecordLossAndContinue,
            "{event_name}"
        );
    }

    let metadata_termination = unknown_event_decision(false, Some("metadata.complete"));
    assert_eq!(metadata_termination.class, UnknownEventClass::Termination);
    assert_eq!(
        metadata_termination.action,
        UnknownEventAction::DegradedOrError
    );
}

#[test]
fn bounded_protocol_json_corpus_is_panic_free() {
    const LENGTHS: &[usize] = &[0, 1, 7, 31, 127, 1024];
    const SEEDS: &[u64] = &[0x0123_4567_89ab_cdef, 0xfedc_ba98_7654_3210];

    for &seed in SEEDS {
        for &length in LENGTHS {
            let mut state = seed;
            let mut bytes = Vec::with_capacity(length);
            for _ in 0..length {
                state ^= state << 13;
                state ^= state >> 7;
                state ^= state << 17;
                bytes.push((state >> 56) as u8);
            }
            assert!(bytes.len() <= 1024);

            let result = catch_unwind(AssertUnwindSafe(|| {
                let _ = serde_json::from_slice::<OpenAiChatRequest>(&bytes);
                let _ = serde_json::from_slice::<OpenAiResponsesRequest>(&bytes);
                let _ = serde_json::from_slice::<ClaudeRequest>(&bytes);
                let _ = serde_json::from_slice::<GeminiRequest>(&bytes);

                if let Ok(response) = serde_json::from_slice::<OpenAiChatResponse>(&bytes) {
                    let _ = openai_chat_response_to_canonical(response);
                }
                if let Ok(response) = serde_json::from_slice::<ResponsesResponse>(&bytes) {
                    let _ = openai_responses_response_to_canonical(response);
                }
                if let Ok(response) = serde_json::from_slice::<ClaudeResponse>(&bytes) {
                    let _ = claude_response_to_canonical(response);
                }
                if let Ok(response) = serde_json::from_slice::<GeminiResponse>(&bytes) {
                    let _ = gemini_response_to_canonical_for_model(response, "fuzz-model");
                }

                if let Ok(snapshot) = serde_json::from_slice::<OpenAiStreamSnapshot>(&bytes) {
                    let _ = lmm_contracts::relay::openai_stream_to_canonical_checked(&snapshot);
                }
                if let Ok(snapshot) = serde_json::from_slice::<ResponsesStreamSnapshot>(&bytes) {
                    let _ = lmm_contracts::relay::responses_stream_to_canonical_checked(&snapshot);
                }
                if let Ok(snapshot) = serde_json::from_slice::<ClaudeStreamSnapshot>(&bytes) {
                    let _ = claude_stream_to_semantic_events(&snapshot);
                }
                if let Ok(snapshot) = serde_json::from_slice::<GeminiStreamSnapshot>(&bytes) {
                    let _ = gemini_stream_to_canonical(&snapshot, "fuzz-model");
                }
            }));
            assert!(
                result.is_ok(),
                "protocol JSON corpus panicked for seed {seed:#x}, length {length}"
            );
        }
    }
}

#[test]
fn bounded_tool_schema_conversions_are_panic_free() {
    fn next_xorshift(state: &mut u64) -> u64 {
        *state ^= *state << 13;
        *state ^= *state >> 7;
        *state ^= *state << 17;
        *state
    }

    fn schema_value(
        state: &mut u64,
        depth: usize,
        nodes_left: &mut usize,
        forced_kind: Option<u8>,
    ) -> serde_json::Value {
        if *nodes_left == 0 {
            return serde_json::Value::Null;
        }
        *nodes_left -= 1;
        let kind = forced_kind.unwrap_or_else(|| (next_xorshift(state) % 6) as u8);
        if depth >= 4 {
            return match kind % 4 {
                0 => serde_json::Value::String(format!("s{:x}", next_xorshift(state) & 0xff)),
                1 => {
                    serde_json::Value::Number(serde_json::Number::from(next_xorshift(state) % 1000))
                }
                2 => serde_json::Value::Bool(next_xorshift(state) & 1 == 0),
                _ => serde_json::Value::Null,
            };
        }
        match kind {
            0 => {
                let mut object = serde_json::Map::new();
                let count = (next_xorshift(state) % 3) as usize;
                for index in 0..count {
                    if *nodes_left == 0 {
                        break;
                    }
                    object.insert(
                        format!("k{index}"),
                        schema_value(state, depth + 1, nodes_left, None),
                    );
                }
                serde_json::Value::Object(object)
            }
            1 => {
                let count = (next_xorshift(state) % 3) as usize;
                let mut array = Vec::with_capacity(count);
                for _ in 0..count {
                    if *nodes_left == 0 {
                        break;
                    }
                    array.push(schema_value(state, depth + 1, nodes_left, None));
                }
                serde_json::Value::Array(array)
            }
            2 => serde_json::Value::String(format!("s{:x}", next_xorshift(state) & 0xffff)),
            3 => serde_json::Value::Number(serde_json::Number::from(next_xorshift(state) % 1000)),
            4 => serde_json::Value::Bool(next_xorshift(state) & 1 == 0),
            _ => serde_json::Value::Null,
        }
    }

    let mut state = 0x243f_6a88_85a3_08d3_u64;
    for case_index in 0..32_u8 {
        let mut nodes_left = 31;
        let schema = schema_value(&mut state, 0, &mut nodes_left, Some(case_index % 6));
        let schema_bytes = serde_json::to_vec(&schema).expect("schema serialization");
        assert!(schema_bytes.len() <= 4096);

        let chat = serde_json::json!({
            "model": "fuzz-schema",
            "messages": [],
            "tools": [{
                "type": "function",
                "function": {"name": "lookup", "parameters": schema.clone()}
            }]
        });
        let claude = serde_json::json!({
            "model": "fuzz-schema",
            "max_tokens": 128,
            "messages": [],
            "tools": [{"name": "lookup", "input_schema": schema.clone()}]
        });
        let gemini = serde_json::json!({
            "contents": [],
            "tools": [{
                "functionDeclarations": [{"name": "lookup", "parameters": schema.clone()}]
            }]
        });
        let responses = serde_json::json!({
            "model": "fuzz-schema",
            "input": [],
            "tools": [{"type": "function", "name": "lookup", "parameters": schema}]
        });
        let documents = [
            serde_json::to_vec(&chat).expect("Chat schema serialization"),
            serde_json::to_vec(&claude).expect("Claude schema serialization"),
            serde_json::to_vec(&gemini).expect("Gemini schema serialization"),
            serde_json::to_vec(&responses).expect("Responses schema serialization"),
        ];

        let result = catch_unwind(AssertUnwindSafe(|| {
            if let Ok(request) = serde_json::from_slice::<OpenAiChatRequest>(&documents[0]) {
                let _ = openai_chat_request_to_canonical(request);
            }
            if let Ok(request) = serde_json::from_slice::<ClaudeRequest>(&documents[1]) {
                let _ = claude_request_to_canonical(request);
            }
            if let Ok(request) = serde_json::from_slice::<GeminiRequest>(&documents[2]) {
                let _ = gemini_request_to_canonical_for_model(request, "fuzz-schema");
            }
            if let Ok(request) = serde_json::from_slice::<OpenAiResponsesRequest>(&documents[3]) {
                let _ = openai_responses_request_to_canonical(request);
            }
        }));
        assert!(
            result.is_ok(),
            "tool schema conversion panicked at case {case_index}"
        );
    }
}

#[test]
fn malformed_differential_evidence_import_is_bounded_and_panic_free() {
    let mut cases = vec![
        format!(
            r#"{{"schema_version":"{}","future_field":true}}"#,
            EVIDENCE_SCHEMA_VERSION
        ),
        format!(
            r#"{{"schema_version":"{}","documents":[]}}"#,
            EVIDENCE_SCHEMA_VERSION
        ),
        format!(
            r#"{{"schema_version":"{}","documents":["#,
            EVIDENCE_SCHEMA_VERSION
        ),
    ];

    let mut state = 0x517c_c1b7_2722_0a95_u64;
    let mut generated = Vec::with_capacity(4096);
    for _ in 0..4096 {
        state ^= state << 13;
        state ^= state >> 7;
        state ^= state << 17;
        generated.push(32 + ((state >> 56) % 95) as u8);
    }
    for &length in &[0, 1, 7, 31, 127, 1024, 4096] {
        cases.push(String::from_utf8(generated[..length].to_vec()).expect("ASCII corpus bytes"));
    }
    cases.push("x".repeat(MAX_EVIDENCE_JSON_BYTES + 1));

    for (case_index, input) in cases.iter().enumerate() {
        let result = catch_unwind(AssertUnwindSafe(|| {
            let _ = DifferentialEvidenceDocument::from_json(input);
            let _ = DifferentialEvidenceBundle::from_json(input);
        }));
        assert!(
            result.is_ok(),
            "differential evidence import panicked at case {case_index}"
        );
    }
}

#[test]
fn route_gate_closed_evidence_and_rollout_flags_fail_closed() {
    let registry = validated_current_registry().expect("current registry validates");
    let default_config = ProtocolRolloutConfig::default();

    for source in protocols() {
        for target in protocols() {
            if source == target {
                continue;
            }
            for stream in [false, true] {
                let scope = RouteOwnershipScope {
                    source,
                    target,
                    stream,
                };
                let context = RolloutContext::new(
                    "qa-route-gate-closed",
                    source,
                    target,
                    "test-model",
                    stream,
                );
                let evidence = OwnershipEvidence::closed(scope);
                for direction in [Direction::Request, Direction::Response, Direction::Stream] {
                    let decision =
                        decide_route(&default_config, &context, &registry, direction, &evidence);
                    assert!(
                        matches!(decision, RouteGateDecision::Closed { .. }),
                        "closed evidence admitted {source:?} -> {target:?}, stream={stream}, direction={direction:?}"
                    );
                }
            }
        }
    }

    for protocol in protocols() {
        let scope = RouteOwnershipScope {
            source: protocol,
            target: protocol,
            stream: true,
        };
        let context = RolloutContext::new(
            "qa-route-gate-native",
            protocol,
            protocol,
            "test-model",
            true,
        );
        let decision = decide_route(
            &default_config,
            &context,
            &registry,
            Direction::Stream,
            &OwnershipEvidence::closed(scope),
        );
        assert!(
            !matches!(decision, RouteGateDecision::CrossProtocol { .. }),
            "same-protocol route selected cross-protocol conversion for {protocol:?}"
        );
        if let RouteGateDecision::NativeRaw { details } = decision {
            assert_eq!(details.scope, scope);
            assert!(
                details
                    .capability
                    .as_ref()
                    .is_some_and(|capability| capability.raw_passthrough)
            );
        }
    }

    let enabled = ProtocolRolloutConfig {
        conversion_engine_v2: FlagConfig::enabled(10_000).expect("bounded full canary"),
        ..default_config.clone()
    };
    let encoded = serde_json::to_string(&enabled).expect("rollout config serializes");
    assert_eq!(
        serde_json::from_str::<ProtocolRolloutConfig>(&encoded).expect("rollout config parses"),
        enabled
    );
    let rolled_back = enabled.rolled_back();
    assert!(rolled_back.rollback);
    assert!(!rolled_back.conversion_engine_v2.enabled);
    let rollback_encoded = serde_json::to_string(&rolled_back).expect("rollback serializes");
    assert_eq!(
        serde_json::from_str::<ProtocolRolloutConfig>(&rollback_encoded)
            .expect("rollback config parses"),
        rolled_back
    );
}

#[derive(Default)]
struct QaExplicitStreamAdaptor;

impl StreamAdaptor for QaExplicitStreamAdaptor {
    fn source(&self) -> Protocol {
        Protocol::OpenAi
    }

    fn target(&self) -> Protocol {
        Protocol::Claude
    }

    fn compile(
        &self,
        _plan: &lmm_contracts::relay::ConversionPlan,
    ) -> Result<Box<dyn StreamAdaptorSession>, StreamSetupFailure> {
        Ok(Box::new(QaExplicitStreamAdaptorSession))
    }
}

struct QaExplicitStreamAdaptorSession;

impl StreamAdaptorSession for QaExplicitStreamAdaptorSession {
    fn process_frame(
        &mut self,
        _frame: &SseFrame,
    ) -> Result<StreamAdaptorOutput, TypedStreamFailure> {
        Ok(StreamAdaptorOutput::empty())
    }

    fn cancel(&mut self) -> Result<(), TypedStreamFailure> {
        Ok(())
    }
}

#[derive(Default)]
struct QaExplicitStreamAdaptorRegistry {
    adaptor: QaExplicitStreamAdaptor,
}

impl StreamAdaptorRegistry for QaExplicitStreamAdaptorRegistry {
    fn for_route(&self, source: Protocol, target: Protocol) -> Option<&dyn StreamAdaptor> {
        (source == Protocol::OpenAi && target == Protocol::Claude)
            .then_some(&self.adaptor as &dyn StreamAdaptor)
    }
}

#[test]
fn stream_session_decision_and_telemetry_follow_rollout_and_fail_closed() {
    let registry = validated_current_registry().expect("current registry validates");
    let default_config = ProtocolRolloutConfig::default();
    let enabled_config = ProtocolRolloutConfig {
        conversion_engine_v2: FlagConfig::enabled(MAX_BASIS_POINTS)
            .expect("full conversion rollout is bounded"),
        ..default_config.clone()
    };
    let default_rollout = ProtocolRolloutControl::new(default_config)
        .expect("default rollout validates")
        .snapshot();
    let enabled_rollout = ProtocolRolloutControl::new(enabled_config)
        .expect("enabled rollout validates")
        .snapshot();

    let native_scope = RouteOwnershipScope {
        source: Protocol::Claude,
        target: Protocol::Claude,
        stream: true,
    };
    let observer = ConversionObserver::default();
    let mut native = compile_stream_session(StreamSessionSpec::new(
        "qa-native-stream",
        Protocol::Claude,
        Protocol::Claude,
        "claude-test",
        &registry,
        &default_rollout,
        &OwnershipEvidence::closed(native_scope),
    ))
    .expect("validated same-protocol stream admits raw passthrough")
    .with_observer(&observer);
    assert!(native.decision().is_raw_passthrough());
    let frame = parse_sse_frames(b"event: future\ndata: opaque\n\n", DEFAULT_MAX_FRAME_BYTES)
        .expect("bounded SSE frame")
        .into_iter()
        .next()
        .expect("one SSE frame");
    let output = native
        .process_frame(&frame)
        .expect("native frame passthrough");
    assert!(matches!(
        output,
        StreamFrameOutput::RawPassthrough { bytes } if bytes == frame.raw.as_slice()
    ));
    native.complete();
    drop(native);

    let native_samples = observer.snapshot().samples;
    assert!(native_samples.iter().any(|sample| {
        sample.metric == MetricKind::ConversionEventsTotal
            && sample.value == 1
            && sample.labels.source_format == Protocol::Claude
            && sample.labels.target_format == Protocol::Claude
            && sample.labels.converter_version == ConverterVersion::NativeRawV1
            && sample.labels.stream
            && sample.labels.feature_class == FeatureClass::Stream
            && sample.labels.result == ConversionResult::Success
    }));

    let cross_scope = RouteOwnershipScope {
        source: Protocol::OpenAi,
        target: Protocol::Claude,
        stream: true,
    };
    let cross_evidence = OwnershipEvidence::closed(cross_scope);
    let cross_context = RolloutContext::new(
        "qa-cross-stream",
        Protocol::OpenAi,
        Protocol::Claude,
        "gpt-test",
        true,
    );
    let disabled_gate = decide_route(
        &ProtocolRolloutConfig::default(),
        &cross_context,
        &registry,
        Direction::Stream,
        &cross_evidence,
    );
    let enabled_gate = decide_route(
        &ProtocolRolloutConfig {
            conversion_engine_v2: FlagConfig::enabled(MAX_BASIS_POINTS)
                .expect("full conversion rollout is bounded"),
            ..ProtocolRolloutConfig::default()
        },
        &cross_context,
        &registry,
        Direction::Stream,
        &cross_evidence,
    );
    assert!(disabled_gate.is_closed());
    assert!(enabled_gate.is_closed());
    assert!(!disabled_gate.details().flag_decision.enabled);
    assert!(enabled_gate.details().flag_decision.enabled);
    assert!(!disabled_gate.blockers().is_empty());
    assert!(!enabled_gate.blockers().is_empty());

    let disabled_rollout = ProtocolRolloutControl::default().snapshot();
    let disabled_cross = compile_stream_session(StreamSessionSpec::new(
        "qa-cross-stream",
        Protocol::OpenAi,
        Protocol::Claude,
        "gpt-test",
        &registry,
        &disabled_rollout,
        &cross_evidence,
    ))
    .expect("disabled cross-protocol stream returns a closed decision");
    assert!(disabled_cross.decision().is_closed());
    assert!(!disabled_cross.decision().details().flag_decision.enabled);
    assert!(disabled_cross.plan().is_none());
    assert!(disabled_cross.typed_state().is_none());

    let enabled_cross = compile_stream_session(StreamSessionSpec::new(
        "qa-cross-stream",
        Protocol::OpenAi,
        Protocol::Claude,
        "gpt-test",
        &registry,
        &enabled_rollout,
        &cross_evidence,
    ))
    .expect("missing cross-protocol ownership returns a closed decision");
    assert!(enabled_cross.decision().is_closed());
    assert!(enabled_cross.decision().details().flag_decision.enabled);
    assert!(!enabled_cross.decision().blockers().is_empty());
    assert!(enabled_cross.plan().is_none());
    assert!(enabled_cross.typed_state().is_none());

    let explicit_adaptors = QaExplicitStreamAdaptorRegistry::default();
    let with_explicit_adaptor = compile_stream_session_with_adaptors(
        StreamSessionSpec::new(
            "qa-cross-stream",
            Protocol::OpenAi,
            Protocol::Claude,
            "gpt-test",
            &registry,
            &enabled_rollout,
            &cross_evidence,
        ),
        &explicit_adaptors,
    )
    .expect("explicit adaptor cannot bypass a closed route gate");
    assert!(with_explicit_adaptor.decision().is_closed());
    assert!(with_explicit_adaptor.plan().is_none());
    assert!(with_explicit_adaptor.typed_state().is_none());
}

#[test]
fn same_protocol_sse_unknown_event_and_bom_bytes_are_raw_passthrough() {
    let registry = validated_current_registry().expect("current registry validates");
    let scope = RouteOwnershipScope {
        source: Protocol::OpenAi,
        target: Protocol::OpenAi,
        stream: true,
    };
    let rollout = ProtocolRolloutControl::default().snapshot();
    let mut session = compile_stream_session(StreamSessionSpec::new(
        "qa-same-protocol-raw",
        Protocol::OpenAi,
        Protocol::OpenAi,
        "gpt-test",
        &registry,
        &rollout,
        &OwnershipEvidence::closed(scope),
    ))
    .expect("same-protocol stream admits native raw passthrough");
    assert!(session.decision().is_raw_passthrough());
    assert!(session.plan().is_none());

    let input = b"\xef\xbb\xbfevent: future_event\r\ndata: first\r\ndata: second\r\nx-provider-extension: opaque\r\n\r\n";
    let mut parser = SseFrameParser::new(input.len());
    let mut frames = Vec::new();
    for chunk in input.chunks(2) {
        frames.extend(parser.feed(chunk).expect("split SSE feed"));
    }
    frames.extend(parser.finish().expect("SSE EOF"));
    assert_eq!(frames.len(), 1);
    let frame = &frames[0];
    assert_eq!(frame.raw, input);
    assert_eq!(frame.event_name(), Some("future_event"));
    assert_eq!(frame.data(), "first\nsecond");
    assert_eq!(frame.unknown_fields.len(), 1);
    assert_eq!(frame.unknown_fields[0], "x-provider-extension");
    assert!(frame.has_unrepresentable_metadata());

    let output = session
        .process_frame(frame)
        .expect("raw frame does not enter typed conversion");
    assert!(matches!(
        output,
        StreamFrameOutput::RawPassthrough { bytes } if bytes == input
    ));
    session.complete();
}

#[test]
fn rollout_feature_flags_select_deterministically_and_fail_closed() {
    let context = RolloutContext::new(
        "qa-rollout-stable-key",
        Protocol::OpenAi,
        Protocol::OpenAiResponses,
        "gpt-rollout",
        true,
    )
    .with_channel("internal");
    let expected_bucket = stable_bucket(context.request_key);
    assert_eq!(expected_bucket, stable_bucket(context.request_key));

    let disabled = ProtocolRolloutConfig::default();
    let full_flag = || FlagConfig::enabled(MAX_BASIS_POINTS).expect("full canary is bounded");
    let mut enabled = ProtocolRolloutConfig {
        conversion_engine_v2: full_flag(),
        gemini_function_id_v2: full_flag(),
        gemini_thought_signature_v2: full_flag(),
        claude_opaque_thinking_v2: full_flag(),
        sse_parser_v2: full_flag(),
        ..ProtocolRolloutConfig::default()
    };
    enabled
        .push_pair_override(ConverterPairOverride {
            flag: RolloutFlag::ConversionEngineV2,
            source: Protocol::OpenAi,
            target: Protocol::OpenAiResponses,
            channel: Some("internal".to_owned()),
            model_family: Some("gpt-rollout".to_owned()),
            stream: Some(true),
            enabled: true,
            canary_basis_points: Some(MAX_BASIS_POINTS),
        })
        .expect("matching pair override is valid");

    for flag in RolloutFlag::ALL {
        let old_decision = disabled.decide(flag, &context);
        assert!(!old_decision.enabled, "default v1 admitted {flag:?}");

        let new_decision = enabled.decide(flag, &context);
        assert_eq!(new_decision, enabled.decide(flag, &context));
        assert_eq!(new_decision.flag, flag);
        assert_eq!(new_decision.bucket, expected_bucket);
        if flag.is_v2() {
            assert!(
                new_decision.enabled,
                "configured v2 flag stayed disabled: {flag:?}"
            );
        } else {
            assert!(
                !new_decision.enabled,
                "value-shaped flag was enabled: {flag:?}"
            );
            assert_eq!(new_decision.source, DecisionSource::DefaultV1);
        }
    }

    // The old/new choice is a pure configuration decision for the same
    // request dimensions; no converter or upstream state participates here.
    assert!(!disabled.is_enabled(RolloutFlag::ConversionEngineV2, &context));
    assert!(enabled.is_enabled(RolloutFlag::ConversionEngineV2, &context));

    let same_key_other_route = RolloutContext::new(
        context.request_key,
        Protocol::Claude,
        Protocol::Gemini,
        "claude-rollout",
        false,
    );
    for flag in RolloutFlag::ALL {
        assert_eq!(
            enabled.decide(flag, &context).bucket,
            enabled.decide(flag, &same_key_other_route).bucket,
            "bucket depends on route dimensions for {flag:?}"
        );
    }

    let empty_key = RolloutContext::new(
        "",
        Protocol::OpenAi,
        Protocol::OpenAiResponses,
        "gpt-rollout",
        true,
    );
    for flag in RolloutFlag::ALL {
        let decision = enabled.decide(flag, &empty_key);
        assert!(!decision.enabled, "empty request key admitted {flag:?}");
        assert_eq!(decision.source, DecisionSource::EmptyRequestKey);
    }

    let rolled_back = enabled.rolled_back();
    for flag in RolloutFlag::ALL {
        let decision = rolled_back.decide(flag, &context);
        assert!(!decision.enabled, "rollback admitted {flag:?}");
        assert_eq!(decision.source, DecisionSource::ConfigRollback);
    }
}

#[test]
fn converter_pair_overrides_prioritize_route_model_stream_channel_and_stage_boundaries() {
    let mut config = ProtocolRolloutConfig::default();
    let pair = |channel: Option<&str>,
                model_family: Option<&str>,
                stream: Option<bool>,
                enabled: bool|
     -> ConverterPairOverride {
        ConverterPairOverride {
            flag: RolloutFlag::ConversionEngineV2,
            source: Protocol::OpenAi,
            target: Protocol::Claude,
            channel: channel.map(str::to_owned),
            model_family: model_family.map(str::to_owned),
            stream,
            enabled,
            canary_basis_points: Some(if enabled { MAX_BASIS_POINTS } else { 0 }),
        }
    };

    // Route-only specificity is two fields; each dimension below adds one,
    // while the final rule combines all dimensions and must win.
    for override_rule in [
        pair(None, None, None, false),
        pair(None, Some("gpt-special"), None, true),
        pair(None, None, Some(true), true),
        pair(Some("internal"), None, None, true),
        pair(Some("internal"), Some("gpt-special"), Some(true), false),
    ] {
        config
            .push_pair_override(override_rule)
            .expect("pair override is valid");
    }

    fn assert_pair_decision(
        config: &ProtocolRolloutConfig,
        context: &RolloutContext<'_>,
        expected_index: usize,
        expected_enabled: bool,
    ) {
        let decision = config.decide(RolloutFlag::ConversionEngineV2, context);
        assert_eq!(
            decision.source,
            DecisionSource::ConverterPairOverride(expected_index)
        );
        assert_eq!(decision.enabled, expected_enabled);
        assert_eq!(
            decision.canary_basis_points,
            if expected_enabled {
                MAX_BASIS_POINTS
            } else {
                0
            }
        );
    }

    let route = RolloutContext::new(
        "pair-route",
        Protocol::OpenAi,
        Protocol::Claude,
        "general",
        false,
    );
    assert_pair_decision(&config, &route, 0, false);
    let model = RolloutContext::new(
        "pair-model",
        Protocol::OpenAi,
        Protocol::Claude,
        "gpt-special",
        false,
    );
    assert_pair_decision(&config, &model, 1, true);
    let stream = RolloutContext::new(
        "pair-stream",
        Protocol::OpenAi,
        Protocol::Claude,
        "general",
        true,
    );
    assert_pair_decision(&config, &stream, 2, true);
    let channel = RolloutContext::new(
        "pair-channel",
        Protocol::OpenAi,
        Protocol::Claude,
        "general",
        false,
    )
    .with_channel("internal");
    assert_pair_decision(&config, &channel, 3, true);
    let all_dimensions = RolloutContext::new(
        "pair-all",
        Protocol::OpenAi,
        Protocol::Claude,
        "gpt-special",
        true,
    )
    .with_channel("internal");
    assert_pair_decision(&config, &all_dimensions, 4, false);

    let unmatched_route = RolloutContext::new(
        "pair-unmatched",
        Protocol::Claude,
        Protocol::OpenAi,
        "gpt-special",
        true,
    )
    .with_channel("internal");
    let unmatched = config.decide(RolloutFlag::ConversionEngineV2, &unmatched_route);
    assert!(!unmatched.enabled);
    assert_eq!(unmatched.source, DecisionSource::BaseConfig);
    let value_shaped = config.decide(RolloutFlag::ConverterPairOverrides, &all_dimensions);
    assert_eq!(
        value_shaped.source,
        DecisionSource::ConverterPairOverride(4)
    );
    assert!(!value_shaped.enabled);

    for stage in CanaryStage::ALL {
        let minimum = stage.minimum_basis_points();
        assert!(
            validate_canary_stage(stage, minimum).is_ok(),
            "stage rejected its minimum: {stage:?}"
        );
        if minimum > 0 {
            assert!(
                validate_canary_stage(stage, minimum - 1).is_err(),
                "stage accepted below its minimum: {stage:?}"
            );
        }
    }
    assert!(validate_canary_stage(CanaryStage::Internal, MAX_BASIS_POINTS).is_ok());
    assert!(validate_canary_stage(CanaryStage::Internal, MAX_BASIS_POINTS + 1).is_err());
    assert!(!bucket_is_in_rollout(0, 0));
    assert!(bucket_is_in_rollout(0, 1));
    assert!(!bucket_is_in_rollout(MAX_BASIS_POINTS, MAX_BASIS_POINTS));
    assert!(bucket_is_in_rollout(MAX_BASIS_POINTS - 1, MAX_BASIS_POINTS));
}

#[test]
fn bounded_rollout_configuration_corpus_is_panic_free_and_fail_closed() {
    const MAX_INPUT_BYTES: usize = 4096;

    fn next_xorshift(state: &mut u64) -> u64 {
        *state ^= *state << 13;
        *state ^= *state >> 7;
        *state ^= *state << 17;
        *state
    }

    let default_config = ProtocolRolloutConfig::default();
    let valid_bytes = serde_json::to_vec(&default_config).expect("default rollout serializes");
    let mut rollback_config = default_config.clone();
    rollback_config.rollback = true;
    let rollback_bytes = serde_json::to_vec(&rollback_config).expect("rollback rollout serializes");

    let mut corpus = vec![valid_bytes.clone(), rollback_bytes];
    for &cut in &[
        0,
        1,
        valid_bytes.len() / 2,
        valid_bytes.len().saturating_sub(1),
        valid_bytes.len(),
    ] {
        corpus.push(valid_bytes[..cut].to_vec());
    }

    let mut state = 0x6a09_e667_f3bc_c909_u64;
    for &length in &[0, 1, 7, 31, 127, 1024, MAX_INPUT_BYTES] {
        let mut bytes = Vec::with_capacity(length);
        for _ in 0..length {
            let value = next_xorshift(&mut state);
            bytes.push(match value & 0x0f {
                0 => b'{',
                1 => b'}',
                2 => b'"',
                3 => b':',
                4 => b',',
                5 => b'[',
                6 => b']',
                _ => b'a' + ((value >> 8) as u8 % 26),
            });
        }
        assert!(bytes.len() <= MAX_INPUT_BYTES);
        corpus.push(bytes);
    }

    let context = RolloutContext::new(
        "qa-rollout-config-corpus",
        Protocol::OpenAi,
        Protocol::OpenAiResponses,
        "rollout-model",
        true,
    );
    for (case_index, input) in corpus.iter().enumerate() {
        let result = catch_unwind(AssertUnwindSafe(|| {
            if let Ok(config) = serde_json::from_slice::<ProtocolRolloutConfig>(input) {
                let _ = config.validate();
                for flag in RolloutFlag::ALL {
                    let _ = config.decide(flag, &context);
                }
            }
        }));
        assert!(
            result.is_ok(),
            "rollout configuration corpus panicked at case {case_index}"
        );
    }

    let mut unknown = serde_json::to_value(&default_config).expect("default rollout value");
    unknown
        .as_object_mut()
        .expect("rollout config object")
        .insert(
            "future_rollout_field".to_owned(),
            serde_json::Value::Bool(true),
        );

    let mut disabled_nonzero = default_config.clone();
    disabled_nonzero.sse_parser_v2.canary_basis_points = 1;

    let mut invalid_basis = default_config.clone();
    invalid_basis.gemini_function_id_v2.canary_basis_points = MAX_BASIS_POINTS + 1;

    let mut invalid_selector = default_config.clone();
    invalid_selector
        .conversion_engine_v2
        .overrides
        .push(FlagOverride {
            selector: RolloutSelector {
                model_family: Some("   ".to_owned()),
                ..RolloutSelector::default()
            },
            enabled: true,
            canary_basis_points: MAX_BASIS_POINTS,
        });

    let mut invalid_pair_basis = default_config.clone();
    invalid_pair_basis
        .converter_pair_overrides
        .push(ConverterPairOverride {
            flag: RolloutFlag::ConversionEngineV2,
            source: Protocol::OpenAi,
            target: Protocol::Claude,
            channel: None,
            model_family: None,
            stream: None,
            enabled: true,
            canary_basis_points: Some(MAX_BASIS_POINTS + 1),
        });

    let rollback_decoded: ProtocolRolloutConfig =
        serde_json::from_slice(&serde_json::to_vec(&rollback_config).expect("rollback bytes"))
            .expect("rollback config parses");
    assert!(rollback_decoded.validate().is_ok());
    for flag in RolloutFlag::ALL {
        let decision = rollback_decoded.decide(flag, &context);
        assert!(!decision.enabled, "rollback admitted {flag:?}");
        assert_eq!(decision.source, DecisionSource::ConfigRollback);
    }

    let explicit = catch_unwind(AssertUnwindSafe(|| {
        assert!(serde_json::from_value::<ProtocolRolloutConfig>(unknown).is_err());
        assert!(disabled_nonzero.validate().is_err());
        assert!(invalid_basis.validate().is_err());
        assert!(invalid_selector.validate().is_err());
        assert!(invalid_pair_basis.validate().is_err());
    }));
    assert!(explicit.is_ok(), "invalid rollout config handling panicked");
}

#[test]
fn rollback_evaluation_covers_plan_thresholds_and_reason_order() {
    fn assert_decision(
        signals: RollbackSignals,
        action: RollbackAction,
        reasons: &[RollbackReason],
    ) {
        let decision = evaluate_rollback(&signals);
        assert_eq!(decision.action, action);
        assert_eq!(decision.reasons.as_slice(), reasons);
        assert_eq!(decision.should_disable(), action == RollbackAction::Disable);
        assert_eq!(decision.should_pause(), action == RollbackAction::Pause);
    }

    assert_decision(RollbackSignals::default(), RollbackAction::Continue, &[]);
    assert_decision(
        RollbackSignals {
            parse_error_rate_percentage_points: Some(PARSE_ERROR_RATE_PAUSE_PERCENTAGE_POINTS),
            ttft_p95_increase_percent: Some(TTFT_P95_PAUSE_PERCENT),
            ..RollbackSignals::default()
        },
        RollbackAction::Continue,
        &[],
    );
    assert_decision(
        RollbackSignals {
            silent_loss: true,
            parse_error_rate_percentage_points: Some(
                PARSE_ERROR_RATE_PAUSE_PERCENTAGE_POINTS + 0.01,
            ),
            ttft_p95_increase_percent: Some(TTFT_P95_PAUSE_PERCENT + 0.01),
            ..RollbackSignals::default()
        },
        RollbackAction::Pause,
        &[
            RollbackReason::SilentLoss,
            RollbackReason::ParseErrorRateExceeded,
            RollbackReason::TtftP95Exceeded,
        ],
    );

    assert_decision(
        RollbackSignals {
            tool_or_signature_400_rate_increased: true,
            ..RollbackSignals::default()
        },
        RollbackAction::Disable,
        &[RollbackReason::ToolSignature400RateIncreased],
    );
    assert_decision(
        RollbackSignals {
            signature_modified: true,
            ..RollbackSignals::default()
        },
        RollbackAction::Disable,
        &[RollbackReason::SignatureModified],
    );
    assert_decision(
        RollbackSignals {
            usage_billing_difference: true,
            ..RollbackSignals::default()
        },
        RollbackAction::Disable,
        &[RollbackReason::UsageBillingDifference],
    );
    assert_decision(
        RollbackSignals {
            sse_interruption_rate_elevated: true,
            ..RollbackSignals::default()
        },
        RollbackAction::Disable,
        &[RollbackReason::SseInterruptionRateElevated],
    );

    let all_immediate = RollbackSignals {
        tool_or_signature_400_rate_increased: true,
        signature_modified: true,
        usage_billing_difference: true,
        sse_interruption_rate_elevated: true,
        ..RollbackSignals::default()
    };
    assert_decision(
        all_immediate,
        RollbackAction::Disable,
        &[
            RollbackReason::ToolSignature400RateIncreased,
            RollbackReason::SignatureModified,
            RollbackReason::UsageBillingDifference,
            RollbackReason::SseInterruptionRateElevated,
        ],
    );

    assert_decision(
        RollbackSignals {
            parse_error_rate_percentage_points: Some(f64::NAN),
            ..RollbackSignals::default()
        },
        RollbackAction::Disable,
        &[RollbackReason::InvalidMetric],
    );
    assert_decision(
        RollbackSignals {
            ttft_p95_increase_percent: Some(-0.01),
            ..RollbackSignals::default()
        },
        RollbackAction::Disable,
        &[RollbackReason::InvalidMetric],
    );
    assert_decision(
        RollbackSignals {
            tool_or_signature_400_rate_increased: true,
            parse_error_rate_percentage_points: Some(f64::NAN),
            ..RollbackSignals::default()
        },
        RollbackAction::Disable,
        &[
            RollbackReason::ToolSignature400RateIncreased,
            RollbackReason::InvalidMetric,
        ],
    );
}

#[test]
fn route_specific_stream_observability_preserves_dimensions() {
    let observer = ConversionObserver::with_max_series(64);
    let labels = MetricLabels::for_route(
        Protocol::OpenAi,
        Protocol::OpenAiResponses,
        ConverterVersion::ProtocolStreamV1,
        1,
        true,
        FeatureClass::Stream,
        ConversionResult::Success,
    );
    observer.record_conversion_duration(labels, Duration::from_nanos(11));
    observer.record_plan_duration(labels, Duration::from_nanos(7));
    observer.record_events(labels, 3);
    observer.record_input_bytes(labels, 128);
    observer.record_output_bytes(labels, 256);
    observer.record_failure_with_reason(labels, FailureReason::Stream);
    observer.record_loss(labels, LossCode::LossUnknownEvent);
    observer.record_synthetic_field(labels, lmm_contracts::relay::SyntheticField::ToolCallId);
    observer.record_unknown_event(labels);
    observer.record_gateway_ttft(labels, Duration::from_nanos(13));
    observer.record_client_abort(labels);

    let snapshot = observer.snapshot();
    let expected = [
        MetricKind::ConversionDurationSeconds,
        MetricKind::ConversionPlanDurationSeconds,
        MetricKind::ConversionEventsTotal,
        MetricKind::ConversionInputBytes,
        MetricKind::ConversionOutputBytes,
        MetricKind::ConversionFailuresTotal,
        MetricKind::ConversionLossesTotal,
        MetricKind::ConversionSyntheticFieldsTotal,
        MetricKind::ConversionUnknownEventsTotal,
        MetricKind::StreamGatewayTtftSeconds,
        MetricKind::StreamClientAbortTotal,
    ];
    for metric in expected {
        assert!(
            snapshot
                .samples
                .iter()
                .any(|sample| sample.metric == metric),
            "missing route metric {metric:?}"
        );
    }
    let route_sample = |metric| {
        snapshot
            .samples
            .iter()
            .find(|sample| sample.metric == metric)
            .expect("route metric sample")
    };
    for metric in [
        MetricKind::ConversionDurationSeconds,
        MetricKind::ConversionPlanDurationSeconds,
        MetricKind::ConversionEventsTotal,
        MetricKind::ConversionInputBytes,
        MetricKind::ConversionOutputBytes,
        MetricKind::StreamGatewayTtftSeconds,
    ] {
        let sample = route_sample(metric);
        assert_eq!(sample.labels.source_format, Protocol::OpenAi);
        assert_eq!(sample.labels.target_format, Protocol::OpenAiResponses);
        assert!(sample.labels.stream);
        assert_eq!(sample.labels.feature_class, FeatureClass::Stream);
        assert_eq!(sample.labels.result, ConversionResult::Success);
    }
    let failure = route_sample(MetricKind::ConversionFailuresTotal);
    assert_eq!(failure.labels.failure_reason, Some(FailureReason::Stream));
    assert_eq!(failure.labels.result, ConversionResult::Failure);
    let loss = route_sample(MetricKind::ConversionLossesTotal);
    assert_eq!(loss.labels.loss_code, Some(LossCode::LossUnknownEvent));
    let abort = route_sample(MetricKind::StreamClientAbortTotal);
    assert_eq!(abort.labels.result, ConversionResult::Cancelled);
}

#[test]
fn route_request_response_metrics_are_protocol_specific_and_body_free() {
    let observer = ConversionObserver::with_max_series(64);
    for (protocol, converter_version) in [
        (Protocol::OpenAi, ConverterVersion::OpenAiChatV1),
        (
            Protocol::OpenAiResponses,
            ConverterVersion::OpenAiResponsesV1,
        ),
    ] {
        let labels = MetricLabels::for_route(
            protocol,
            protocol,
            converter_version,
            0,
            false,
            FeatureClass::Text,
            ConversionResult::Success,
        );

        // These fixed values stand in for request parsing/plan and response
        // serialization observations without retaining either body.
        observer.record_conversion_duration(labels, Duration::from_nanos(17));
        observer.record_plan_duration(labels, Duration::from_nanos(5));
        observer.record_events(labels, 2);
        observer.record_input_bytes(labels, 128);
        observer.record_output_bytes(labels, 256);
        observer.record_failure_with_reason(labels, FailureReason::Unsupported);
        observer.record_loss(labels, LossCode::LossUnknownEvent);
    }

    let snapshot = observer.snapshot();
    for (protocol, converter_version) in [
        (Protocol::OpenAi, ConverterVersion::OpenAiChatV1),
        (
            Protocol::OpenAiResponses,
            ConverterVersion::OpenAiResponsesV1,
        ),
    ] {
        let route_sample = |metric| {
            snapshot
                .samples
                .iter()
                .find(|sample| {
                    sample.metric == metric
                        && sample.labels.source_format == protocol
                        && sample.labels.target_format == protocol
                })
                .expect("route metric sample")
        };
        for metric in [
            MetricKind::ConversionDurationSeconds,
            MetricKind::ConversionPlanDurationSeconds,
            MetricKind::ConversionEventsTotal,
            MetricKind::ConversionInputBytes,
            MetricKind::ConversionOutputBytes,
        ] {
            let sample = route_sample(metric);
            assert_eq!(sample.labels.converter_version, converter_version);
            assert!(!sample.labels.stream);
            assert_eq!(sample.labels.feature_class, FeatureClass::Text);
            assert_eq!(sample.labels.result, ConversionResult::Success);
        }
        let failure = route_sample(MetricKind::ConversionFailuresTotal);
        assert_eq!(failure.labels.converter_version, converter_version);
        assert!(!failure.labels.stream);
        assert_eq!(failure.labels.feature_class, FeatureClass::Text);
        assert_eq!(failure.labels.result, ConversionResult::Failure);
        assert_eq!(
            failure.labels.failure_reason,
            Some(FailureReason::Unsupported)
        );
        let loss = route_sample(MetricKind::ConversionLossesTotal);
        assert_eq!(loss.labels.converter_version, converter_version);
        assert!(!loss.labels.stream);
        assert_eq!(loss.labels.feature_class, FeatureClass::Text);
        assert_eq!(loss.labels.result, ConversionResult::Success);
        assert_eq!(loss.labels.loss_code, Some(LossCode::LossUnknownEvent));
    }

    let serialized = serde_json::to_string(&snapshot).expect("bounded metrics snapshot JSON");
    for forbidden in [
        "request_key",
        "model",
        "tool",
        "body",
        "private request",
        "secret response",
    ] {
        assert!(
            !serialized.contains(forbidden),
            "metric snapshot leaked forbidden field {forbidden:?}"
        );
    }
}

#[test]
fn claude_stream_state_machine_is_bounded_and_panic_free() {
    const MAX_STEPS: usize = 64;
    const MAX_EVENTS: usize = 4;
    const MAX_BYTES: usize = 4096;

    let source: serde_json::Value = serde_json::from_str(CLAUDE_STREAM).expect("Claude corpus");
    let templates = source
        .get("events")
        .and_then(serde_json::Value::as_array)
        .expect("Claude event corpus");
    let mut state = 0x9e37_79b9_7f4a_7c15_u64;
    let mut history = Vec::new();

    for step in 0..MAX_STEPS {
        state ^= state << 13;
        state ^= state >> 7;
        state ^= state << 17;
        history.push(templates[(state as usize) % templates.len()].clone());
        if history.len() > MAX_EVENTS {
            history.remove(0);
        }
        let document = serde_json::json!({"events": history, "usage": {}});
        let bytes = serde_json::to_vec(&document).expect("Claude state serialization");
        assert!(bytes.len() <= MAX_BYTES);
        let truncated_at = ((state >> 32) as usize) % (bytes.len() + 1);

        let result = catch_unwind(AssertUnwindSafe(|| {
            if let Ok(snapshot) = serde_json::from_slice::<ClaudeStreamSnapshot>(&bytes) {
                let _ = claude_stream_to_semantic_events(&snapshot);
            }
            if let Ok(snapshot) =
                serde_json::from_slice::<ClaudeStreamSnapshot>(&bytes[..truncated_at])
            {
                let _ = claude_stream_to_semantic_events(&snapshot);
            }
        }));
        assert!(
            result.is_ok(),
            "Claude stream state panicked at step {step}"
        );
    }
}

#[test]
fn sse_regression_corpus_is_incremental_bounded_and_panic_free() {
    const CORPUS: &[&[u8]] = &[
        b"data: one\ndata: two\n\n",
        b"\xef\xbb\xbfevent: update\r\ndata: [DONE]\r\n\r\n",
        b"retry: +1\nid: bad\0id\ndata: {\"ok\":true}\n\n",
        b"event: future_content\ndata: raw\n\n",
        b"data: {\xff}\n\n",
        b"data: unfinished\n",
        b"\r\n\n",
    ];

    for input in CORPUS {
        for width in 1..=3 {
            let result = catch_unwind(AssertUnwindSafe(|| {
                let mut parser = SseFrameParser::new(64);
                let mut offset = 0;
                while offset < input.len() {
                    let end = offset.saturating_add(width).min(input.len());
                    let _ = parser.feed(&input[offset..end]);
                    assert!(parser.buffered_frame_bytes() <= 64);
                    offset = end;
                }
                let _ = parser.finish();
                assert!(parser.buffered_frame_bytes() <= 64);
            }));
            assert!(result.is_ok(), "SSE parser panicked for width {width}");
        }
    }

    let mut parser = SseFrameParser::new(128);
    let mut frames = Vec::new();
    for byte in b"event: future\ndata: {\"x\":1}\n\n" {
        frames.extend(
            parser
                .feed(std::slice::from_ref(byte))
                .expect("valid byte feed"),
        );
    }
    frames.extend(parser.finish().expect("valid EOF"));
    assert_eq!(frames.len(), 1);
    assert_eq!(frames[0].event_name(), Some("future"));
    assert_eq!(frames[0].raw, b"event: future\ndata: {\"x\":1}\n\n");
    assert!(json_events_from_frames(&frames).is_ok());
    let done =
        parse_sse_frames(b"data: [DONE]\n\n", DEFAULT_MAX_FRAME_BYTES).expect("terminal SSE frame");
    assert!(done[0].is_done());
    assert!(
        parse_sse_frames(b"data: unfinished\n", 64)
            .expect("strict EOF")
            .is_empty()
    );
    assert_eq!(
        parse_sse_frames(b"data: 123\n\n", 5),
        Err(SseError::FrameTooLarge {
            limit: 5,
            observed: 6,
        })
    );
    assert!(matches!(
        json_events_from_frames(&[lmm_api_rs::routes::sse::SseFrame {
            event: None,
            id: Some("id".to_owned()),
            retry: None,
            data: "{}".to_owned(),
            comments: Vec::new(),
            unknown_fields: Vec::new(),
            has_data: true,
            raw: b"id: id\ndata: {}\n\n".to_vec(),
        }]),
        Err(SseError::UnsupportedMetadata { field: "id", .. })
    ));
}

#[test]
fn arbitrary_sse_bytes_are_bounded_without_panics() {
    for width in 1..=7 {
        let result = catch_unwind(AssertUnwindSafe(|| {
            let mut parser = SseFrameParser::new(64);
            let mut offset = 0;
            while offset < ARBITRARY_SSE_BYTES.len() {
                let end = offset.saturating_add(width).min(ARBITRARY_SSE_BYTES.len());
                let _ = parser.feed(&ARBITRARY_SSE_BYTES[offset..end]);
                assert!(parser.buffered_frame_bytes() <= 64);
                offset = end;
            }
            let _ = parser.finish();
            assert!(parser.buffered_frame_bytes() <= 64);
        }));
        assert!(
            result.is_ok(),
            "arbitrary SSE bytes panicked at width {width}"
        );
    }
}

#[test]
fn deterministic_sse_boundaries_are_bounded_and_panic_free() {
    fn next_xorshift(state: &mut u64) -> u64 {
        *state ^= *state << 13;
        *state ^= *state >> 7;
        *state ^= *state << 17;
        *state
    }

    const MAX_INPUT_BYTES: usize = 4096;
    const MAX_FRAME_BYTES: usize = 512;
    const MAX_FRAMES: usize = 16;
    let mut state = 0xd1b5_4a32_d192_ed03_u64;

    for case_index in 0..24 {
        let mode = (next_xorshift(&mut state) % 5) as usize;
        let mut input = match mode {
            0 => b"\xef\xbb\xbfevent: future\ndata: first\ndata: second\n\n".to_vec(),
            1 => b"event: update\r\ndata: {\"part\":\"one\"}\rdata: two\r\n\r\n".to_vec(),
            2 => b": comment\nunknown: value\ndata: [DONE]\n\n".to_vec(),
            3 => b"event: truncated\ndata: final".to_vec(),
            _ => b"\r\ndata: empty\n\n".to_vec(),
        };
        input.extend_from_slice(b"event: fuzz\nunknown_field: ");
        let suffix_len = (next_xorshift(&mut state) % 96) as usize;
        for _ in 0..suffix_len {
            let byte = (next_xorshift(&mut state) >> 56) as u8;
            input.push(if mode == 4 { byte } else { b'a' + (byte % 26) });
        }
        if mode != 3 {
            input.extend_from_slice(b"\ndata: generated\n\n");
        }
        assert!(input.len() <= MAX_INPUT_BYTES);

        let chunk_width = (next_xorshift(&mut state) % 11 + 1) as usize;
        let result = catch_unwind(AssertUnwindSafe(|| {
            let mut parser = SseFrameParser::new(MAX_FRAME_BYTES);
            let mut frames = Vec::new();
            let mut offset = 0;
            while offset < input.len() {
                let end = offset.saturating_add(chunk_width).min(input.len());
                match parser.feed(&input[offset..end]) {
                    Ok(new_frames) => frames.extend(new_frames),
                    Err(_) => break,
                }
                assert!(parser.buffered_frame_bytes() <= MAX_FRAME_BYTES);
                offset = end;
            }
            let _ = parser.finish();
            assert!(frames.len() <= MAX_FRAMES);

            for frames in [
                parse_sse_frames(&input, MAX_FRAME_BYTES),
                parse_sse_frames_lenient(&input, MAX_FRAME_BYTES),
                parse_sse_frames_rejecting_unterminated(&input, MAX_FRAME_BYTES),
            ]
            .into_iter()
            .flatten()
            {
                assert!(frames.len() <= MAX_FRAMES);
            }
        }));
        assert!(
            result.is_ok(),
            "SSE boundary corpus panicked at case {case_index}"
        );
    }
}

#[test]
fn sse_acceptance_covers_repeated_fields_mixed_line_endings_and_eof_modes() {
    let input = b"event: first\r\nid: old\rretry: 7\n:event note\ndata: first\r\ndata: second\n\n";
    let frames = parse_sse_frames(input, DEFAULT_MAX_FRAME_BYTES).expect("mixed SSE corpus");
    assert_eq!(frames.len(), 1);
    assert_eq!(frames[0].event_name(), Some("first"));
    assert_eq!(frames[0].id.as_deref(), Some("old"));
    assert_eq!(frames[0].retry_ms(), Some(7));
    assert_eq!(frames[0].comments, vec!["event note"]);
    assert_eq!(frames[0].data(), "first\nsecond");

    let repeated = parse_sse_frames(
        b"event: old\nevent: new\nid: old\nid:\nretry: 1\nretry: 2\ndata: {}\n\n",
        DEFAULT_MAX_FRAME_BYTES,
    )
    .expect("repeated field corpus");
    assert_eq!(repeated[0].event_name(), Some("new"));
    assert_eq!(repeated[0].id.as_deref(), Some(""));
    assert_eq!(repeated[0].retry_ms(), Some(2));

    assert_eq!(
        parse_sse_frames(b"data: final\n", DEFAULT_MAX_FRAME_BYTES)
            .expect("strict EOF")
            .len(),
        0
    );
    assert_eq!(
        parse_sse_frames_lenient(b"data: final\n", DEFAULT_MAX_FRAME_BYTES)
            .expect("legacy EOF flush")[0]
            .data(),
        "final"
    );
    assert_eq!(
        parse_sse_frames_rejecting_unterminated(b"data: final\n", DEFAULT_MAX_FRAME_BYTES),
        Err(SseError::UnterminatedFrame)
    );
}

#[test]
fn sse_incremental_partitions_match_one_shot_frames() {
    let corpora: &[&[u8]] = &[
        b"event: update\r\ndata: {\"x\":1}\r\n\r\ndata: [DONE]\n\n",
        b": heartbeat\n\ndata: one\ndata: two\r\n\r\n",
    ];

    for input in corpora {
        let expected = parse_sse_frames(input, DEFAULT_MAX_FRAME_BYTES).expect("one-shot SSE");
        for width in 1..=input.len() {
            let mut parser = SseFrameParser::new(DEFAULT_MAX_FRAME_BYTES);
            let mut actual = Vec::new();
            let mut offset = 0;
            while offset < input.len() {
                let end = offset.saturating_add(width).min(input.len());
                actual.extend(parser.feed(&input[offset..end]).expect("incremental SSE"));
                offset = end;
            }
            actual.extend(parser.finish().expect("incremental EOF"));
            assert_eq!(actual, expected, "chunk width {width}");
        }
    }
}

#[test]
fn sse_parser_lifecycle_errors_are_typed_and_non_panicking() {
    let mut finished = SseFrameParser::new(DEFAULT_MAX_FRAME_BYTES);
    finished.finish().expect("empty EOF");
    assert_eq!(
        finished.feed(b"data: late\n\n"),
        Err(SseError::AlreadyFinished)
    );
    assert_eq!(finished.finish(), Err(SseError::AlreadyFinished));

    let mut failed = SseFrameParser::new(1);
    assert_eq!(
        failed.feed(b"xyz"),
        Err(SseError::FrameTooLarge {
            limit: 1,
            observed: 2,
        })
    );
    assert_eq!(failed.feed(b""), Err(SseError::ParserFailed));
    assert_eq!(failed.finish(), Err(SseError::ParserFailed));
}

#[test]
fn observer_and_parser_are_race_ready_with_per_thread_parser_state() {
    let observer = Arc::new(ConversionObserver::with_max_series(8));
    let mut handles = Vec::new();
    for _worker in 0..4 {
        let observer = Arc::clone(&observer);
        handles.push(thread::spawn(move || {
            let labels =
                MetricLabels::native_raw(Protocol::OpenAi, true, ConversionResult::Success);
            for _ in 0..64 {
                observer.record_events(labels, 1);
                let mut parser = SseFrameParser::new(64);
                let _ = parser.feed(b"data: chunk\n\n");
                assert!(parser.buffered_frame_bytes() <= 64);
                let _ = parser.finish();
            }
        }));
    }
    for handle in handles {
        handle.join().expect("worker completed");
    }
    let snapshot = observer.snapshot();
    assert!(snapshot.samples.iter().any(|sample| {
        sample.metric == MetricKind::ConversionEventsTotal && sample.value == 256
    }));
}

#[test]
fn deterministic_generated_cases_keep_each_data_frame_in_order() {
    for seed in 0_u16..32 {
        let body = format!("data: generated-{seed}\n\n");
        let frames = parse_sse_frames(body.as_bytes(), 128).expect("generated frame");
        assert_eq!(frames.len(), 1);
        assert_eq!(frames[0].data, format!("generated-{seed}"));
        assert_eq!(frames[0].raw, body.as_bytes());
    }
}

#[test]
fn ordered_ir_keeps_authentic_state_and_requires_structured_loss_for_mutation() {
    let mut envelope = Envelope::new(Protocol::OpenAiResponses, "responses-test");
    let mut message = Item::new(
        ItemKind::Message,
        Role::User,
        Provenance::new(Protocol::OpenAiResponses),
    );
    message.push_part(Part::text("first"));
    message.push_part(Part::opaque(
        OpaqueProviderState::authentic_gemini_thought_signature(
            "sig-authentic".to_owned(),
            Some("gemini-2.5-pro".to_owned()),
        ),
    ));
    envelope.push_item(message).expect("valid ordered message");
    assert_eq!(
        envelope.ordered_items()[0].ordered_parts()[0]
            .text
            .as_deref(),
        Some("first")
    );
    assert!(envelope.validate_exact_round_trip().is_ok());

    let mut second = Item::new(
        ItemKind::Message,
        Role::User,
        Provenance::new(Protocol::OpenAiResponses),
    );
    second.push_part(Part::text("second"));
    envelope.push_item(second).expect("second ordered message");
    assert!(envelope.reorder_items(0, 1, None).is_err());
    envelope
        .reorder_items(0, 1, Some(Loss::new(LossCode::LossContentOrder, None)))
        .expect("explicit order loss");
    assert_eq!(
        envelope.ordered_items()[0].ordered_parts()[0]
            .text
            .as_deref(),
        Some("second")
    );
    assert_eq!(envelope.loss_ledger().len(), 1);
    assert!(envelope.validate_exact_round_trip().is_err());
}

#[test]
fn ordered_ir_tool_links_keep_item_id_distinct_from_call_id() {
    let mut envelope = Envelope::new(Protocol::OpenAi, "gpt-test");
    let mut call = Item::tool_call(
        OpaqueId::authentic("call-authentic", Protocol::OpenAi),
        vec![Part::function(FunctionData::call("lookup", JsonData::Null))],
        Provenance::new(Protocol::OpenAi),
    );
    call.id = Some(OpaqueId::authentic("item-id", Protocol::OpenAi));
    let result = Item::tool_result(
        OpaqueId::authentic("call-authentic", Protocol::OpenAi),
        vec![Part::function(FunctionData::result(
            None,
            JsonData::String("ok".to_owned()),
        ))],
        Provenance::new(Protocol::OpenAi),
    );
    envelope.push_item(call).expect("tool call");
    envelope.push_item(result).expect("linked tool result");
    assert_eq!(
        envelope.ordered_items()[0]
            .id
            .as_ref()
            .map(OpaqueId::as_str),
        Some("item-id")
    );
    assert_eq!(
        envelope.ordered_items()[0]
            .call_id
            .as_ref()
            .map(OpaqueId::as_str),
        Some("call-authentic")
    );
    assert_eq!(
        envelope.ordered_items()[1]
            .call_id
            .as_ref()
            .map(OpaqueId::as_str),
        Some("call-authentic")
    );
    assert!(envelope.validate().is_ok());

    let authentic = OpaqueId::authentic("same-bytes", Protocol::OpenAi);
    let synthetic = OpaqueId::synthetic("same-bytes", Protocol::OpenAi);
    assert_eq!(authentic.value, synthetic.value);
    assert_eq!(authentic.provenance, OpaqueIdProvenance::Authentic);
    assert_eq!(synthetic.provenance, OpaqueIdProvenance::Synthetic);
    let mut synthetic_item = Item::new(
        ItemKind::Message,
        Role::User,
        Provenance::new(Protocol::OpenAi),
    );
    synthetic_item.id = Some(synthetic);
    let mut synthetic_envelope = Envelope::new(Protocol::OpenAi, "gpt-test");
    synthetic_envelope
        .push_item(synthetic_item)
        .expect("synthetic id is structurally valid");
    assert!(synthetic_envelope.validate_exact_round_trip().is_err());
}

#[test]
fn usage_cache_billing_and_extensions_round_trip_without_float_loss() {
    let usage = SemanticUsage {
        input_tokens: 10,
        output_tokens: 5,
        total_tokens: 15,
        reasoning_tokens: 2,
        cache: CacheUsage {
            read_input_tokens: 3,
            write_input_tokens: 4,
            creation_input_tokens: 5,
            extensions: Default::default(),
        },
        billing: Some(SemanticBillingUsage {
            source: Some("gateway".to_owned()),
            semantic: Some("provider_reported".to_owned()),
            input_tokens: 10,
            output_tokens: 5,
            cached_input_tokens: 3,
            total_tokens: 15,
            cost: Some(Money {
                amount: "0.000000123456789".to_owned(),
                currency: "USD".to_owned(),
            }),
            extensions: Default::default(),
        }),
        extensions: Default::default(),
    };
    let encoded = serde_json::to_string(&usage).expect("usage serialization");
    let decoded: SemanticUsage = serde_json::from_str(&encoded).expect("usage deserialization");
    assert_eq!(decoded, usage);
    let mut changed_cache = usage.clone();
    changed_cache.cache.read_input_tokens = 4;
    assert_ne!(changed_cache, usage);

    let mut envelope = Envelope::new(Protocol::Claude, "claude-test");
    envelope
        .extensions
        .insert("future_usage_field".to_owned(), JsonData::Bool(true));
    envelope.usage = Some(usage);
    assert!(envelope.validate_exact_round_trip().is_err());
    let encoded = serde_json::to_string(&envelope).expect("envelope serialization");
    assert!(encoded.contains("future_usage_field"));
}

#[test]
fn registry_runtime_matrix_is_complete_and_fail_closed() {
    let matrix = current_support_matrix().expect("validated runtime support matrix");
    let protocols = lmm_contracts::relay::protocols();
    assert_eq!(matrix.routes.len(), protocols.len() * protocols.len());
    for source in protocols {
        for target in protocols {
            let route = matrix.route(source, target).expect("matrix route");
            if route.supports_any_direction() {
                assert!(route.runtime_adaptor().is_some());
                assert!(!route.converter_ids().is_empty());
                assert!(route.stream().finalizer_id.is_some());
            } else {
                assert!(route.runtime_adaptor().is_none());
                assert!(route.converter_ids().is_empty());
                assert!(route.stream().finalizer_id.is_none());
            }
        }
    }
    assert!(matrix.to_json().is_ok());
}

fn local_summary(converter_id: &str, fingerprint: u8) -> LocalConversionSummary {
    LocalConversionSummary {
        converter_id: converter_id.to_owned(),
        plan_fingerprint: [fingerprint; 32],
        semantic_fingerprint: [fingerprint.wrapping_add(1); 32],
        losses: Vec::new(),
        synthetic: Vec::new(),
    }
}

#[test]
fn ownership_gate_defaults_closed_and_requires_attested_differentials() {
    let scope = RouteOwnershipScope {
        source: Protocol::OpenAi,
        target: Protocol::Claude,
        stream: true,
    };
    let gate = OwnershipGate::default();
    assert!(matches!(
        gate.evaluate(&OwnershipEvidence::closed(scope)),
        OwnershipDecision::ClosedByDefault { .. }
    ));

    let mut complete = OwnershipEvidence::closed(scope);
    for class in DifferentialClass::all() {
        complete.mark_green(*class);
    }
    complete.set_shadow_identical(true);
    complete
        .set_canary_basis_points(MIN_REVIEW_CANARY_BASIS_POINTS)
        .expect("bounded canary");
    complete.approve_rollout();
    assert!(matches!(
        gate.evaluate(&complete),
        OwnershipDecision::ClosedByDefault { blockers, .. }
            if blockers.contains(&OwnershipBlocker::UntrustedEvidence)
    ));

    for missing in DifferentialClass::all() {
        let mut green = complete.green_classes().clone();
        green.remove(missing);
        let mut evidence = OwnershipEvidence::closed(scope);
        for class in green {
            evidence.mark_green(class);
        }
        evidence.set_shadow_identical(true);
        evidence
            .set_canary_basis_points(MIN_REVIEW_CANARY_BASIS_POINTS)
            .expect("bounded canary");
        evidence.approve_rollout();
        assert!(matches!(
            gate.evaluate(&evidence),
            OwnershipDecision::ClosedByDefault { .. }
        ));
    }

    let mut below_canary = complete.clone();
    below_canary
        .set_canary_basis_points(99)
        .expect("bounded canary");
    assert!(matches!(
        gate.evaluate(&below_canary),
        OwnershipDecision::ClosedByDefault { .. }
    ));
    let mut shadow_mismatch = complete;
    shadow_mismatch.set_shadow_identical(false);
    assert!(matches!(
        gate.evaluate(&shadow_mismatch),
        OwnershipDecision::ClosedByDefault { .. }
    ));
}

#[test]
fn shadow_comparison_is_body_free_and_client_abort_is_bounded() {
    let runner = ShadowRunner::new(
        |_request: &LocalRequest<'_>| {
            Ok::<LocalConversionSummary, LocalConversionError>(local_summary("old", 7))
        },
        |_request: &LocalRequest<'_>| {
            Ok::<LocalConversionSummary, LocalConversionError>(local_summary("old", 7))
        },
        Protocol::OpenAi,
        Protocol::OpenAiResponses,
        false,
    );
    let identical = runner.compare(&LocalRequest::new(b"private request body"));
    assert!(identical.is_identical());
    assert!(identical.differences.is_empty());

    let mismatch = ShadowRunner::new(
        |_request: &LocalRequest<'_>| {
            Ok::<LocalConversionSummary, LocalConversionError>(local_summary("old", 7))
        },
        |_request: &LocalRequest<'_>| {
            Ok::<LocalConversionSummary, LocalConversionError>(local_summary("new", 7))
        },
        Protocol::OpenAi,
        Protocol::OpenAiResponses,
        true,
    )
    .compare(&LocalRequest::new(b"private request body"));
    assert!(
        mismatch
            .differences
            .contains(&ShadowDifference::ConverterId)
    );
    assert!(!mismatch.differences.is_empty());
    assert!(mismatch.is_identical());

    let observer = ConversionObserver::with_max_series(2);
    let labels = MetricLabels::new(
        Protocol::OpenAi,
        Protocol::OpenAiResponses,
        ConverterVersion::OpenAiChatV1,
        1000,
        true,
        FeatureClass::Stream,
        ConversionResult::Success,
    );
    {
        let _abort = ClientAbortGuard::new(observer.clone(), labels);
    }
    observer.record_unknown_event(labels);
    observer.record_client_abort(labels);
    let snapshot = observer.snapshot();
    assert!(snapshot.samples.len() <= 2);
    assert!(snapshot.samples.iter().any(|sample| {
        sample.metric == MetricKind::StreamClientAbortTotal
            && sample.labels.result == ConversionResult::Cancelled
            && sample.value == 2
    }));

    let now = Instant::now();
    let mut timing = StreamTiming::default();
    timing.mark_upstream_event_at(now);
    timing.mark_downstream_write_at(now + Duration::from_millis(1));
    assert!(timing.gateway_ttft_tax().is_some());
}

#[test]
fn typed_mutation_and_provider_types_are_reachable_from_public_api() {
    let tool = Tool::function("lookup", JsonData::Null);
    assert_eq!(tool.name.as_deref(), Some("lookup"));
    assert_eq!(ToolChoice::default(), ToolChoice::Auto);
    let media = Media::new(MediaKind::Image);
    let media_part = Part::media(media);
    assert_eq!(media_part.kind, PartKind::Media);
    assert!(!Feature::all().is_empty());
}

#[test]
#[ignore = "fixed-duration fuzz harness; run explicitly in QA"]
fn fixed_duration_sse_fuzz_is_panic_free() {
    const INITIAL_SEED: u64 = 0x8f3c_1a27_d4e5_b609;
    const MAX_INPUT_BYTES: usize = 4096;
    const MAX_FRAME_BYTES: usize = 512;
    const MAX_FRAMES: usize = 64;

    fn next_xorshift(state: &mut u64) -> u64 {
        *state ^= *state << 13;
        *state ^= *state >> 7;
        *state ^= *state << 17;
        *state
    }

    let seconds = std::env::var("LMM_FUZZ_SECONDS")
        .ok()
        .and_then(|value| value.parse::<u64>().ok())
        .unwrap_or(1)
        .clamp(1, 30);
    let deadline = Instant::now() + Duration::from_secs(seconds);
    let mut seed = INITIAL_SEED;
    let mut iterations = 0_u64;

    while Instant::now() < deadline {
        let result = catch_unwind(AssertUnwindSafe(|| {
            let frame_count = (next_xorshift(&mut seed) % 16 + 1) as usize;
            let mut input = Vec::with_capacity(MAX_INPUT_BYTES);
            for frame_index in 0..frame_count {
                let mode = (next_xorshift(&mut seed) % 4) as usize;
                let prefix = match mode {
                    0 => b"event: future\ndata: {\"ok\":true}\n".as_slice(),
                    1 => b"data: [DONE]\n".as_slice(),
                    2 => b": heartbeat\nunknown: extension\ndata: value\n".as_slice(),
                    _ => b"data: ".as_slice(),
                };
                input.extend_from_slice(prefix);
                let suffix_len = (next_xorshift(&mut seed) % 96) as usize;
                for _ in 0..suffix_len {
                    let byte = (next_xorshift(&mut seed) >> 56) as u8;
                    input.push(match byte % 8 {
                        0 => b'{',
                        1 => b'}',
                        2 => b'[',
                        3 => b']',
                        4 => b':',
                        _ => b'a' + (byte % 26),
                    });
                }
                if frame_index + 1 < frame_count || next_xorshift(&mut seed) & 1 == 0 {
                    input.extend_from_slice(b"\n\n");
                }
            }
            if input.len() > MAX_INPUT_BYTES {
                input.truncate(MAX_INPUT_BYTES);
            }
            assert!(input.len() <= MAX_INPUT_BYTES);

            let mut parser = SseFrameParser::new(MAX_FRAME_BYTES);
            let mut frames = Vec::new();
            let mut offset = 0_usize;
            while offset < input.len() {
                let width = (next_xorshift(&mut seed) % 64 + 1) as usize;
                let end = offset.saturating_add(width).min(input.len());
                match parser.feed(&input[offset..end]) {
                    Ok(new_frames) => frames.extend(new_frames),
                    Err(_) => break,
                }
                assert!(parser.buffered_frame_bytes() <= MAX_FRAME_BYTES);
                assert!(frames.len() <= MAX_FRAMES);
                offset = end;
            }
            let _ = parser.finish();
            assert!(parser.buffered_frame_bytes() <= MAX_FRAME_BYTES);
            assert!(frames.len() <= MAX_FRAMES);

            for parsed in [
                parse_sse_frames(&input, MAX_FRAME_BYTES),
                parse_sse_frames_lenient(&input, MAX_FRAME_BYTES),
                parse_sse_frames_rejecting_unterminated(&input, MAX_FRAME_BYTES),
            ]
            .into_iter()
            .flatten()
            {
                assert!(parsed.len() <= MAX_FRAMES);
                let _ = json_events_from_frames(&parsed);
                for frame in parsed {
                    if let Ok(snapshot) = serde_json::from_str::<OpenAiStreamSnapshot>(&frame.data)
                    {
                        let _ = openai_stream_to_canonical(&snapshot);
                    }
                    if let Ok(snapshot) =
                        serde_json::from_str::<ResponsesStreamSnapshot>(&frame.data)
                    {
                        let _ = responses_stream_to_canonical(&snapshot);
                    }
                    if let Ok(snapshot) = serde_json::from_str::<ClaudeStreamSnapshot>(&frame.data)
                    {
                        let _ = claude_stream_to_semantic_events(&snapshot);
                    }
                    if let Ok(snapshot) = serde_json::from_str::<GeminiStreamSnapshot>(&frame.data)
                    {
                        let _ = gemini_stream_to_canonical(&snapshot, "fuzz-model");
                    }
                }
            }
        }));
        assert!(
            result.is_ok(),
            "fixed SSE fuzz panic: seed={seed:#x}, iteration={iterations}"
        );
        iterations = iterations.saturating_add(1);
    }

    eprintln!(
        "fixed SSE fuzz complete: initial_seed={INITIAL_SEED:#x}, final_seed={seed:#x}, iterations={iterations}"
    );
    assert!(
        iterations > 0,
        "fixed SSE fuzz did not execute an iteration"
    );
}

#[test]
#[ignore = "fixed-duration JSON conversion fuzz harness; run explicitly in QA"]
fn fixed_duration_json_conversion_fuzz_is_panic_free() {
    const INITIAL_SEED: u64 = 0x5d7a_91c3_24ef_0861;
    const MAX_INPUT_BYTES: usize = 8192;
    const MAX_ITERATIONS: u64 = 1_000_000;

    fn next_xorshift(state: &mut u64) -> u64 {
        *state ^= *state << 13;
        *state ^= *state >> 7;
        *state ^= *state << 17;
        *state
    }

    let seconds = std::env::var("LMM_FUZZ_SECONDS")
        .ok()
        .and_then(|value| value.parse::<u64>().ok())
        .unwrap_or(1)
        .clamp(1, 30);
    let deadline = Instant::now() + Duration::from_secs(seconds);
    let mut seed = INITIAL_SEED;
    let mut iterations = 0_u64;

    while Instant::now() < deadline && iterations < MAX_ITERATIONS {
        let template = match next_xorshift(&mut seed) % 9 {
            0 => serde_json::json!({
                "model": "fuzz-model",
                "messages": []
            }),
            1 => serde_json::json!({
                "model": "fuzz-model",
                "messages": [{"role": "user", "content": "x"}],
                "tools": [{
                    "type": "function",
                    "function": {
                        "name": "lookup",
                        "parameters": {"type": "object"}
                    }
                }]
            }),
            2 => serde_json::json!({
                "model": "fuzz-model",
                "input": "x",
                "tools": [{
                    "type": "function",
                    "name": "lookup",
                    "parameters": {"type": "object"}
                }]
            }),
            3 => serde_json::json!({
                "model": "fuzz-model",
                "max_tokens": 1,
                "messages": [{"role": "user", "content": "x"}],
                "tools": [{
                    "name": "lookup",
                    "input_schema": {"type": "object"}
                }]
            }),
            4 => serde_json::json!({
                "contents": [{"role": "user", "parts": [{"text": "x"}]}],
                "tools": [{
                    "functionDeclarations": [{
                        "name": "lookup",
                        "parameters": {"type": "object"}
                    }]
                }]
            }),
            5 => serde_json::json!({
                "id": "fuzz",
                "model": "fuzz-model",
                "object": "chat.completion",
                "created": 0,
                "choices": []
            }),
            6 => serde_json::json!({
                "id": "fuzz",
                "object": "response",
                "created_at": 0,
                "status": "completed",
                "model": "fuzz-model",
                "output": []
            }),
            7 => serde_json::json!({
                "id": "fuzz",
                "type": "message",
                "role": "assistant",
                "model": "fuzz-model",
                "content": []
            }),
            _ => serde_json::json!({"candidates": []}),
        };
        let mut input = serde_json::to_vec(&template).expect("JSON fuzz template");

        match next_xorshift(&mut seed) % 3 {
            0 => {}
            1 => {
                let suffix_len = (next_xorshift(&mut seed) % 128) as usize;
                for _ in 0..suffix_len {
                    let byte = (next_xorshift(&mut seed) >> 56) as u8;
                    input.push(match byte % 8 {
                        0 => b'{',
                        1 => b'}',
                        2 => b'[',
                        3 => b']',
                        4 => b':',
                        5 => b',',
                        _ => b'a' + (byte % 26),
                    });
                }
            }
            _ => {
                let cut = (next_xorshift(&mut seed) as usize) % (input.len() + 1);
                input.truncate(cut);
            }
        }
        if input.len() > MAX_INPUT_BYTES {
            input.truncate(MAX_INPUT_BYTES);
        }
        assert!(input.len() <= MAX_INPUT_BYTES);

        let result = catch_unwind(AssertUnwindSafe(|| {
            if let Ok(request) = serde_json::from_slice::<OpenAiChatRequest>(&input)
                && let Ok(converted) = openai_chat_request_to_canonical(request)
            {
                let _ = canonical_request_to_openai_responses(converted.value);
            }
            if let Ok(request) = serde_json::from_slice::<OpenAiResponsesRequest>(&input)
                && let Ok(converted) = openai_responses_request_to_canonical(request)
            {
                let _ = canonical_request_to_openai_chat(converted.value);
            }
            if let Ok(request) = serde_json::from_slice::<ClaudeRequest>(&input)
                && let Ok(converted) = claude_request_to_canonical(request)
            {
                let _ = canonical_request_to_claude(converted.value);
            }
            if let Ok(request) = serde_json::from_slice::<GeminiRequest>(&input)
                && let Ok(converted) = gemini_request_to_canonical_for_model(request, "fuzz-model")
            {
                let _ = canonical_request_to_gemini_for_model(converted.value, "fuzz-model", false);
            }

            if let Ok(response) = serde_json::from_slice::<OpenAiChatResponse>(&input) {
                let _ = openai_chat_response_to_canonical(response);
            }
            if let Ok(response) = serde_json::from_slice::<ResponsesResponse>(&input) {
                let _ = openai_responses_response_to_canonical(response);
            }
            if let Ok(response) = serde_json::from_slice::<ClaudeResponse>(&input) {
                let _ = claude_response_to_canonical(response);
            }
            if let Ok(response) = serde_json::from_slice::<GeminiResponse>(&input) {
                let _ = gemini_response_to_canonical_for_model(response, "fuzz-model");
            }
        }));
        assert!(
            result.is_ok(),
            "fixed JSON conversion fuzz panic: seed={seed:#x}, iteration={iterations}"
        );
        iterations = iterations.saturating_add(1);
    }

    eprintln!(
        "fixed JSON conversion fuzz complete: initial_seed={INITIAL_SEED:#x}, final_seed={seed:#x}, iterations={iterations}"
    );
    assert!(
        iterations > 0,
        "fixed JSON conversion fuzz did not execute an iteration"
    );
}

#[test]
#[ignore = "fixed-duration Claude stream state-machine fuzz harness; run explicitly in QA"]
fn fixed_duration_claude_stream_state_machine_fuzz_is_panic_free() {
    const INITIAL_SEED: u64 = 0xa7f2_4c91_6e03_b58d;
    const MAX_EVENTS: usize = 64;
    const MAX_BYTES: usize = 4096;
    const MAX_ITERATIONS: u64 = 1_000_000;

    fn next_xorshift(state: &mut u64) -> u64 {
        *state ^= *state << 13;
        *state ^= *state >> 7;
        *state ^= *state << 17;
        *state
    }

    let seconds = std::env::var("LMM_FUZZ_SECONDS")
        .ok()
        .and_then(|value| value.parse::<u64>().ok())
        .unwrap_or(1)
        .clamp(1, 30);
    let deadline = Instant::now() + Duration::from_secs(seconds);
    let source: serde_json::Value =
        serde_json::from_str(CLAUDE_STREAM).expect("Claude event corpus");
    let templates = source
        .get("events")
        .and_then(serde_json::Value::as_array)
        .expect("Claude event corpus array");
    assert!(!templates.is_empty());

    let mut seed = INITIAL_SEED;
    let mut iterations = 0_u64;
    while Instant::now() < deadline && iterations < MAX_ITERATIONS {
        let result = catch_unwind(AssertUnwindSafe(|| {
            let event_count = (next_xorshift(&mut seed) % (MAX_EVENTS as u64 + 1)) as usize;
            let mut events = Vec::with_capacity(event_count);
            for event_index in 0..event_count {
                let template_index = (next_xorshift(&mut seed) as usize) % templates.len();
                let event = match next_xorshift(&mut seed) % 9 {
                    0 => templates[template_index].clone(),
                    1 => serde_json::json!({
                        "type": "message_start",
                        "message": {
                            "id": format!("fuzz-{event_index}"),
                            "type": "message",
                            "role": "assistant",
                            "model": "fuzz-model",
                            "content": []
                        }
                    }),
                    2 => serde_json::json!({
                        "type": "content_block_start",
                        "index": event_index,
                        "content_block": {
                            "type": "thinking",
                            "thinking": "",
                            "signature": format!("sig-{}", next_xorshift(&mut seed) % 32)
                        }
                    }),
                    3 => serde_json::json!({
                        "type": "content_block_delta",
                        "index": event_index,
                        "delta": {
                            "type": "thinking_delta",
                            "thinking": format!("plan-{}", next_xorshift(&mut seed) % 32)
                        }
                    }),
                    4 => serde_json::json!({
                        "type": "content_block_delta",
                        "index": event_index,
                        "delta": {
                            "type": "signature_delta",
                            "signature": format!("sig-{}", next_xorshift(&mut seed) % 32)
                        }
                    }),
                    5 => serde_json::json!({
                        "type": "content_block_stop",
                        "index": event_index
                    }),
                    6 => serde_json::json!({
                        "type": "message_delta",
                        "delta": {
                            "stop_reason": if next_xorshift(&mut seed) & 1 == 0 {
                                "end_turn"
                            } else {
                                "tool_use"
                            }
                        }
                    }),
                    7 => serde_json::json!({"type": "message_stop"}),
                    _ => serde_json::json!({
                        "type": format!(
                            "future_event_{}",
                            next_xorshift(&mut seed) % 32
                        ),
                        "provider_field": next_xorshift(&mut seed)
                    }),
                };
                events.push(event);
            }
            assert!(events.len() <= MAX_EVENTS);

            let document = serde_json::json!({
                "events": events,
                "usage": {
                    "input_tokens": next_xorshift(&mut seed) % 32,
                    "output_tokens": next_xorshift(&mut seed) % 32
                }
            });
            let mut bytes = serde_json::to_vec(&document).expect("Claude fuzz serialization");
            if bytes.len() > MAX_BYTES {
                bytes.truncate(MAX_BYTES);
            }
            assert!(bytes.len() <= MAX_BYTES);

            let prefix_len = if bytes.is_empty() {
                0
            } else {
                (next_xorshift(&mut seed) as usize) % (bytes.len() + 1)
            };
            for candidate in [bytes.as_slice(), &bytes[..prefix_len]] {
                if let Ok(snapshot) = serde_json::from_slice::<ClaudeStreamSnapshot>(candidate) {
                    let _ = claude_stream_to_semantic_events(&snapshot);
                }
            }
        }));
        assert!(
            result.is_ok(),
            "Claude stream state fuzz panic: seed={seed:#x}, iteration={iterations}"
        );
        iterations = iterations.saturating_add(1);
    }

    eprintln!(
        "Claude stream state fuzz complete: initial_seed={INITIAL_SEED:#x}, final_seed={seed:#x}, iterations={iterations}"
    );
    assert!(
        iterations > 0,
        "Claude stream state fuzz did not execute an iteration"
    );
}

#[test]
#[ignore = "fixed-duration tool-schema fuzz harness; run explicitly in QA"]
fn fixed_duration_tool_schema_conversion_fuzz_is_panic_free() {
    const INITIAL_SEED: u64 = 0x1d4c_8f72_a903_e6b5;
    const MAX_INPUT_BYTES: usize = 4096;
    const MAX_SCHEMA_DEPTH: usize = 6;
    const MAX_SCHEMA_NODES: usize = 63;
    const MAX_TOOLS: usize = 8;
    const MAX_ITERATIONS: u64 = 1_000_000;

    fn next_xorshift(state: &mut u64) -> u64 {
        *state ^= *state << 13;
        *state ^= *state >> 7;
        *state ^= *state << 17;
        *state
    }

    fn schema_value(
        state: &mut u64,
        depth: usize,
        nodes_left: &mut usize,
        forced_kind: Option<u8>,
    ) -> serde_json::Value {
        if *nodes_left == 0 {
            return serde_json::Value::Null;
        }
        *nodes_left -= 1;
        let kind = forced_kind.unwrap_or_else(|| (next_xorshift(state) % 6) as u8);
        if depth >= MAX_SCHEMA_DEPTH {
            return match kind % 4 {
                0 => serde_json::Value::String(format!("s{:x}", next_xorshift(state) & 0xff)),
                1 => {
                    serde_json::Value::Number(serde_json::Number::from(next_xorshift(state) % 1000))
                }
                2 => serde_json::Value::Bool(next_xorshift(state) & 1 == 0),
                _ => serde_json::Value::Null,
            };
        }

        match kind {
            0 => {
                let mut object = serde_json::Map::new();
                let count = (next_xorshift(state) % 3) as usize;
                for index in 0..count {
                    if *nodes_left == 0 {
                        break;
                    }
                    object.insert(
                        format!("k{index}"),
                        schema_value(state, depth + 1, nodes_left, None),
                    );
                }
                serde_json::Value::Object(object)
            }
            1 => {
                let count = (next_xorshift(state) % 3) as usize;
                let mut array = Vec::with_capacity(count);
                for _ in 0..count {
                    if *nodes_left == 0 {
                        break;
                    }
                    array.push(schema_value(state, depth + 1, nodes_left, None));
                }
                serde_json::Value::Array(array)
            }
            2 => serde_json::Value::String(format!("s{:x}", next_xorshift(state) & 0xffff)),
            3 => serde_json::Value::Number(serde_json::Number::from(next_xorshift(state) % 1000)),
            4 => serde_json::Value::Bool(next_xorshift(state) & 1 == 0),
            _ => serde_json::Value::Null,
        }
    }

    let seconds = std::env::var("LMM_FUZZ_SECONDS")
        .ok()
        .and_then(|value| value.parse::<u64>().ok())
        .unwrap_or(1)
        .clamp(1, 30);
    let deadline = Instant::now() + Duration::from_secs(seconds);
    let mut seed = INITIAL_SEED;
    let mut iterations = 0_u64;

    while Instant::now() < deadline && iterations < MAX_ITERATIONS {
        let result = catch_unwind(AssertUnwindSafe(|| {
            let tool_count = (next_xorshift(&mut seed) % MAX_TOOLS as u64 + 1) as usize;
            let mut schemas = Vec::with_capacity(tool_count);
            for tool_index in 0..tool_count {
                let mut nodes_left = MAX_SCHEMA_NODES;
                schemas.push(schema_value(
                    &mut seed,
                    0,
                    &mut nodes_left,
                    Some((tool_index % 6) as u8),
                ));
            }
            assert!(schemas.len() <= MAX_TOOLS);

            let chat = serde_json::json!({
                "model": "fuzz-schema",
                "messages": [],
                "tools": schemas.iter().enumerate().map(|(tool_index, schema)| {
                    serde_json::json!({
                        "type": "function",
                        "function": {
                            "name": format!("lookup_{tool_index}"),
                            "parameters": schema.clone()
                        }
                    })
                }).collect::<Vec<_>>()
            });
            let claude = serde_json::json!({
                "model": "fuzz-schema",
                "max_tokens": 128,
                "messages": [],
                "tools": schemas.iter().enumerate().map(|(tool_index, schema)| {
                    serde_json::json!({
                        "name": format!("lookup_{tool_index}"),
                        "input_schema": schema.clone()
                    })
                }).collect::<Vec<_>>()
            });
            let gemini = serde_json::json!({
                "contents": [],
                "tools": [{
                    "functionDeclarations": schemas.iter().enumerate().map(|(tool_index, schema)| {
                        serde_json::json!({
                            "name": format!("lookup_{tool_index}"),
                            "parameters": schema.clone()
                        })
                    }).collect::<Vec<_>>()
                }]
            });
            let responses = serde_json::json!({
                "model": "fuzz-schema",
                "input": [],
                "tools": schemas.iter().enumerate().map(|(tool_index, schema)| {
                    serde_json::json!({
                        "type": "function",
                        "name": format!("lookup_{tool_index}"),
                        "parameters": schema.clone()
                    })
                }).collect::<Vec<_>>()
            });

            let documents = vec![
                serde_json::to_vec(&chat).expect("Chat schema serialization"),
                serde_json::to_vec(&claude).expect("Claude schema serialization"),
                serde_json::to_vec(&gemini).expect("Gemini schema serialization"),
                serde_json::to_vec(&responses).expect("Responses schema serialization"),
            ];
            for (document_index, mut input) in documents.into_iter().enumerate() {
                match next_xorshift(&mut seed) % 3 {
                    0 => {}
                    1 => {
                        let suffix_len = (next_xorshift(&mut seed) % 96) as usize;
                        for _ in 0..suffix_len {
                            let byte = (next_xorshift(&mut seed) >> 56) as u8;
                            input.push(match byte % 8 {
                                0 => b'{',
                                1 => b'}',
                                2 => b'[',
                                3 => b']',
                                4 => b':',
                                5 => b',',
                                _ => b'a' + (byte % 26),
                            });
                        }
                    }
                    _ => {
                        let cut = (next_xorshift(&mut seed) as usize) % (input.len() + 1);
                        input.truncate(cut);
                    }
                }
                if input.len() > MAX_INPUT_BYTES {
                    input.truncate(MAX_INPUT_BYTES);
                }
                assert!(input.len() <= MAX_INPUT_BYTES);

                match document_index {
                    0 => {
                        if let Ok(request) = serde_json::from_slice::<OpenAiChatRequest>(&input) {
                            let _ = openai_chat_request_to_canonical(request);
                        }
                    }
                    1 => {
                        if let Ok(request) = serde_json::from_slice::<ClaudeRequest>(&input) {
                            let _ = claude_request_to_canonical(request);
                        }
                    }
                    2 => {
                        if let Ok(request) = serde_json::from_slice::<GeminiRequest>(&input) {
                            let _ = gemini_request_to_canonical_for_model(request, "fuzz-schema");
                        }
                    }
                    _ => {
                        if let Ok(request) =
                            serde_json::from_slice::<OpenAiResponsesRequest>(&input)
                        {
                            let _ = openai_responses_request_to_canonical(request);
                        }
                    }
                }
            }
        }));
        assert!(
            result.is_ok(),
            "tool schema fuzz panic: seed={seed:#x}, iteration={iterations}"
        );
        iterations = iterations.saturating_add(1);
    }

    eprintln!(
        "tool schema fuzz complete: initial_seed={INITIAL_SEED:#x}, final_seed={seed:#x}, iterations={iterations}"
    );
    assert!(
        iterations > 0,
        "tool schema fuzz did not execute an iteration"
    );
}

#[test]
#[ignore = "fixed-duration observer/parser race harness; run explicitly in QA"]
fn fixed_duration_observer_and_parser_race_is_panic_free() {
    const INITIAL_SEED: u64 = 0xc31b_5e79_04a2_d6f8;
    const WORKER_COUNT: usize = 4;
    const MAX_ITERATIONS_PER_WORKER: u64 = 1_000_000;
    const MAX_INPUT_BYTES: usize = 128;
    const MAX_FRAME_BYTES: usize = 64;
    const MAX_FRAMES: usize = 8;

    fn next_xorshift(state: &mut u64) -> u64 {
        *state ^= *state << 13;
        *state ^= *state >> 7;
        *state ^= *state << 17;
        *state
    }

    let seconds = std::env::var("LMM_FUZZ_SECONDS")
        .ok()
        .and_then(|value| value.parse::<u64>().ok())
        .unwrap_or(1)
        .clamp(1, 30);
    let deadline = Instant::now() + Duration::from_secs(seconds);
    let observer = Arc::new(ConversionObserver::with_max_series(8));
    let mut handles = Vec::with_capacity(WORKER_COUNT);

    for worker_index in 0..WORKER_COUNT {
        let observer = Arc::clone(&observer);
        let worker_seed = INITIAL_SEED ^ (worker_index as u64).wrapping_mul(0x9e37_79b9);
        handles.push(thread::spawn(move || {
            catch_unwind(AssertUnwindSafe(|| {
                let labels =
                    MetricLabels::native_raw(Protocol::OpenAi, true, ConversionResult::Success);
                let mut seed = worker_seed;
                let mut iterations = 0_u64;

                while Instant::now() < deadline && iterations < MAX_ITERATIONS_PER_WORKER {
                    let input: &[u8] = if next_xorshift(&mut seed) & 1 == 0 {
                        b"data: chunk\n\n"
                    } else {
                        b"event: update\r\ndata: chunk\r\n\r\n"
                    };
                    assert!(input.len() <= MAX_INPUT_BYTES);

                    let mut parser = SseFrameParser::new(MAX_FRAME_BYTES);
                    let mut offset = 0_usize;
                    while offset < input.len() {
                        let width = (next_xorshift(&mut seed) % 8 + 1) as usize;
                        let end = offset.saturating_add(width).min(input.len());
                        let frames = parser
                            .feed(&input[offset..end])
                            .expect("race harness SSE input is valid");
                        assert!(frames.len() <= MAX_FRAMES);
                        assert!(parser.buffered_frame_bytes() <= MAX_FRAME_BYTES);
                        offset = end;
                    }
                    let _ = parser.finish();
                    assert!(parser.buffered_frame_bytes() <= MAX_FRAME_BYTES);

                    observer.record_events(labels, 1);
                    if next_xorshift(&mut seed) & 15 == 0 {
                        assert!(observer.snapshot().samples.len() <= 8);
                    }
                    iterations = iterations.saturating_add(1);
                }

                (iterations, seed)
            }))
        }));
    }

    let mut total_iterations = 0_u64;
    for (worker_index, handle) in handles.into_iter().enumerate() {
        let result = handle.join().expect("race harness worker joined");
        match result {
            Ok((iterations, final_seed)) => {
                total_iterations = total_iterations.saturating_add(iterations);
                eprintln!(
                    "observer/parser race worker complete: worker={worker_index}, final_seed={final_seed:#x}, iterations={iterations}"
                );
            }
            Err(_) => panic!(
                "observer/parser race panic: worker={worker_index}, initial_seed={:#x}",
                INITIAL_SEED ^ (worker_index as u64).wrapping_mul(0x9e37_79b9)
            ),
        }
    }

    assert!(total_iterations > 0, "race harness executed no iterations");
    let snapshot = observer.snapshot();
    let event_total = snapshot
        .samples
        .iter()
        .find(|sample| sample.metric == MetricKind::ConversionEventsTotal)
        .map_or(0, |sample| sample.value);
    assert_eq!(event_total, total_iterations);
}
