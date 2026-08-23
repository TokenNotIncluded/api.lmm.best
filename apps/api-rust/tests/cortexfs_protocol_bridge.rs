use lmm_api_rs::{
    cortexfs_protocol_bridge::{
        CortexFsStreamAdaptorRegistry, transcode_request_protocol, transcode_response_protocol,
    },
    protocol_stream_pipeline::StreamAdaptorRegistry,
};
use lmm_contracts::relay::Protocol;

const CHAT: &[u8] = br#"{"model":"chat-model","messages":[{"role":"user","content":"hi"}]}"#;
const CHAT_RESPONSE: &[u8] = br#"{"id":"chat-run","model":"chat-model","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}"#;

#[test]
fn cortexfs_bridge_transcodes_openai_to_claude_request_and_response() {
    let request =
        transcode_request_protocol(Protocol::OpenAi, Protocol::Claude, CHAT).expect("request");
    assert!(serde_json::from_slice::<serde_json::Value>(&request.bytes).is_ok());

    let response = transcode_response_protocol(
        Protocol::Claude,
        Protocol::OpenAi,
        br#"{"id":"anthropic-run","model":"claude-model","role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}"#,
    )
    .expect("response");
    assert!(serde_json::from_slice::<serde_json::Value>(&response.bytes).is_ok());
}

#[test]
fn cortexfs_stream_registry_exposes_all_twelve_cross_protocol_pairs() {
    let registry = CortexFsStreamAdaptorRegistry;
    let mut count = 0;
    for source in [
        Protocol::OpenAi,
        Protocol::OpenAiResponses,
        Protocol::Claude,
        Protocol::Gemini,
    ] {
        for target in [
            Protocol::OpenAi,
            Protocol::OpenAiResponses,
            Protocol::Claude,
            Protocol::Gemini,
        ] {
            if source == target {
                continue;
            }
            assert!(registry.for_route(source, target).is_some());
            count += 1;
        }
    }
    assert_eq!(count, 12);
}

#[test]
fn cortexfs_bridge_roundtrips_openai_chat_identity() {
    let converted =
        transcode_request_protocol(Protocol::OpenAi, Protocol::OpenAi, CHAT).expect("identity");
    assert_eq!(converted.bytes, CHAT);

    let response = transcode_response_protocol(Protocol::OpenAi, Protocol::OpenAi, CHAT_RESPONSE)
        .expect("identity response");
    assert_eq!(response.bytes, CHAT_RESPONSE);
}
