//! Differential contract tests against the frozen Go relayconvert snapshots.

use std::{collections::BTreeMap, fs, path::PathBuf};

use super::*;

const SOURCES: [&str; 4] = ["claude", "gemini", "openai", "openai_responses"];

fn fixture_root() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("tests/fixtures/relayconvert/golden")
}

fn protocol(name: &str) -> Protocol {
    match name {
        "claude" => Protocol::Claude,
        "gemini" => Protocol::Gemini,
        "openai" => Protocol::OpenAi,
        "openai_responses" => Protocol::OpenAiResponses,
        _ => panic!("unknown fixture protocol {name}"),
    }
}

fn kind(name: &str) -> FixtureKind {
    match name {
        "request" => FixtureKind::Request,
        "response" => FixtureKind::Response,
        "stream" => FixtureKind::Stream,
        _ => panic!("unknown fixture kind {name}"),
    }
}

fn fixture(group: &str, from: &str, to: &str) -> String {
    fs::read_to_string(
        fixture_root()
            .join(group)
            .join(format!("{from}_to_{to}.golden.json")),
    )
    .expect("frozen relayconvert fixture must be readable")
}

#[test]
fn all_36_legacy_golden_files_have_typed_contracts() {
    let mut verified = 0;
    for group in ["request", "response", "stream"] {
        for from in SOURCES {
            for to in SOURCES {
                if from == to {
                    continue;
                }
                let json = fixture(group, from, to);
                let validation = validate_golden(kind(group), protocol(to), &json)
                    .unwrap_or_else(|error| panic!("{group}/{from}_to_{to}: {error}"));
                if group == "stream" {
                    assert!(validation.event_count > 0, "{from}_to_{to}");
                    assert!(validation.usage.is_some(), "{from}_to_{to}");
                }
                if group == "response" {
                    assert!(validation.usage.is_some(), "{from}_to_{to}");
                }
                verified += 1;
            }
        }
    }
    assert_eq!(verified, 36);
}

#[test]
fn chat_request_to_responses_matches_go_golden() {
    let source: OpenAiChatRequest = serde_json::from_str(
        r#"{
          "model":"gpt-test","max_tokens":1024,"stream":true,
          "messages":[
            {"role":"system","content":"You are a helpful assistant."},
            {"role":"user","content":[{"type":"text","text":"What is in this image?"},{"type":"image_url","image_url":{"url":"https://example.com/cat.png","detail":"high"}}]},
            {"role":"assistant","tool_calls":[{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},
            {"role":"tool","tool_call_id":"call_abc","content":"15 degrees"},
            {"role":"user","content":"Summarize."}
          ],
          "tools":[{"type":"function","function":{"name":"get_weather","description":"Get weather by city","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}],
          "tool_choice":"auto"
        }"#,
    )
    .expect("source fixture");
    let canonical = openai_chat_request_to_canonical(source)
        .expect("Chat request conversion")
        .value;
    let actual = canonical_request_to_openai_responses(canonical)
        .expect("Responses request conversion")
        .value;
    let expected: OpenAiResponsesRequest =
        serde_json::from_str(&fixture("request", "openai", "openai_responses"))
            .expect("golden Responses request");
    assert_eq!(actual, expected);
}

#[test]
fn responses_request_to_chat_matches_go_golden() {
    let source: OpenAiResponsesRequest = serde_json::from_str(
        r#"{
          "model":"gpt-test","stream":true,"max_output_tokens":1024,
          "instructions":"You are a helpful assistant.",
          "input":[
            {"type":"message","role":"user","content":[{"type":"input_text","text":"What is in this image?"},{"type":"input_image","image_url":"https://example.com/cat.png"}]},
            {"type":"function_call","call_id":"call_abc","name":"get_weather","arguments":"{\"city\":\"Paris\"}"},
            {"type":"function_call_output","call_id":"call_abc","output":"15 degrees"}
          ],
          "tools":[{"type":"function","name":"get_weather","description":"Get weather by city","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}]
        }"#,
    )
    .expect("source fixture");
    let canonical = openai_responses_request_to_canonical(source)
        .expect("Responses request conversion")
        .value;
    let actual = canonical_request_to_openai_chat(canonical)
        .expect("Chat request conversion")
        .value;
    let expected: OpenAiChatRequest =
        serde_json::from_str(&fixture("request", "openai_responses", "openai"))
            .expect("golden Chat request");
    assert_eq!(actual, expected);
}

#[test]
fn response_pair_preserves_text_reasoning_tools_finish_and_usage() {
    let chat: OpenAiChatResponse = serde_json::from_str(
        r#"{
          "id":"chatcmpl-fixed","object":"chat.completion","created":1700000000,"model":"gpt-test",
          "choices":[{"index":0,"message":{"role":"assistant","content":"The answer is 42.","reasoning_content":"Deep thought.","tool_calls":[{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}],
          "usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":2}}
        }"#,
    )
    .expect("source response");
    let canonical = openai_chat_response_to_canonical(chat.clone())
        .expect("Chat response conversion")
        .value;
    let mut responses_wire = openai_chat_response_to_responses(chat)
        .expect("Chat to Responses response conversion")
        .value;
    let mut roundtrip_wire = responses_wire.clone();
    roundtrip_wire.max_output_tokens = None;
    roundtrip_wire.parallel_tool_calls = None;
    roundtrip_wire.store = None;
    roundtrip_wire.temperature = None;
    roundtrip_wire.top_p = None;
    let roundtrip = openai_responses_response_to_canonical(roundtrip_wire)
        .expect("Responses response conversion")
        .value;
    assert_eq!(roundtrip.output, canonical.output);
    assert_eq!(roundtrip.finish_reason, Some(FinishReason::ToolCalls));
    assert_eq!(roundtrip.usage, canonical.usage);

    let mut expected: ResponsesResponse =
        serde_json::from_str(&fixture("response", "openai", "openai_responses"))
            .expect("golden Responses response");
    let mut golden_wire = expected.clone();
    golden_wire.max_output_tokens = None;
    golden_wire.parallel_tool_calls = None;
    golden_wire.store = None;
    golden_wire.temperature = None;
    golden_wire.top_p = None;
    let golden = openai_responses_response_to_canonical(golden_wire)
        .expect("golden canonical response")
        .value;
    assert_eq!(golden.output, canonical.output);
    assert_eq!(golden.finish_reason, canonical.finish_reason);
    assert_eq!(golden.usage, canonical.usage);
    // Billing provenance is transport metadata injected outside relayconvert;
    // every converter-owned target field must match the frozen Go output.
    responses_wire.usage.clone_from(&expected.usage);
    expected.max_output_tokens = None;
    expected.temperature = None;
    expected.top_p = None;
    assert_eq!(responses_wire, expected);
}

#[test]
fn responses_response_to_chat_matches_go_golden_semantics() {
    let source: ResponsesResponse = serde_json::from_str(
        r#"{
          "id":"resp_fixed","object":"response","model":"gpt-test","status":"completed",
          "output":[
            {"type":"reasoning","summary":[{"type":"summary_text","text":"Deep thought."}]},
            {"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"The answer is 42."}]},
            {"type":"function_call","call_id":"call_abc","name":"get_weather","arguments":"{\"city\":\"Paris\"}","status":"completed"}
          ],
          "usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}
        }"#,
    )
    .expect("source Responses response");
    let canonical = openai_responses_response_to_canonical(source.clone())
        .expect("Responses response conversion")
        .value;
    let converted =
        openai_responses_response_to_chat(source).expect("Responses to Chat response conversion");
    assert!(converted.loss.is_lossless());
    let mut actual_wire = converted.value;
    let actual = openai_chat_response_to_canonical(actual_wire.clone())
        .expect("generated Chat response")
        .value;

    let expected: OpenAiChatResponse =
        serde_json::from_str(&fixture("response", "openai_responses", "openai"))
            .expect("golden Chat response");
    let expected = openai_chat_response_to_canonical(expected)
        .expect("golden Chat canonical response")
        .value;
    assert_eq!(actual.output.len(), canonical.output.len());
    for part in &canonical.output {
        assert!(
            actual.output.contains(part),
            "missing converted part: {part:?}"
        );
    }
    let actual_without_reasoning = actual
        .output
        .iter()
        .filter(|part| !matches!(part, CanonicalContent::Reasoning { .. }))
        .cloned()
        .collect::<Vec<_>>();
    assert_eq!(actual_without_reasoning, expected.output);
    assert_eq!(actual.finish_reason, expected.finish_reason);
    assert_eq!(actual.usage, expected.usage);
    assert_eq!(actual.id, canonical.id);
    assert_eq!(actual.model, canonical.model);

    let expected_wire: OpenAiChatResponse =
        serde_json::from_str(&fixture("response", "openai_responses", "openai"))
            .expect("golden Chat response");
    // Rust intentionally preserves Responses reasoning in Chat's supported
    // extension; normalize only that deliberate improvement and billing
    // provenance before checking every legacy target field.
    actual_wire.choices[0].message.reasoning_content = None;
    actual_wire.usage.clone_from(&expected_wire.usage);
    assert_eq!(actual_wire, expected_wire);
}

#[test]
fn stream_pair_preserves_frame_order_text_finish_and_usage() {
    let mut responses: ResponsesStreamSnapshot =
        serde_json::from_str(&fixture("stream", "openai", "openai_responses"))
            .expect("golden Responses stream");
    for event in &mut responses.events {
        if let Some(response) = event.payload.response.as_mut() {
            response.max_output_tokens = None;
            response.parallel_tool_calls = None;
            response.store = None;
            response.temperature = None;
            response.top_p = None;
        }
    }
    let canonical = responses_stream_to_canonical(&responses);
    let text = canonical
        .iter()
        .filter_map(|event| match event {
            CanonicalStreamEvent::TextDelta { delta, .. } => Some(delta.as_str()),
            _ => None,
        })
        .collect::<String>();
    assert_eq!(text, "Hello world");
    assert!(matches!(
        canonical.last(),
        Some(CanonicalStreamEvent::ResponseEnd {
            finish_reason: FinishReason::Stop,
            usage: Some(TokenUsage {
                input_tokens: 4,
                output_tokens: 2,
                total_tokens: 6,
                ..
            }),
            ..
        })
    ));

    let chat_chunks = response_events_to_openai_chunks(&canonical);
    let chat = OpenAiStreamSnapshot {
        events: chat_chunks,
        usage: responses.usage,
        extra: BTreeMap::new(),
    };
    let chat_canonical = openai_stream_to_canonical(&chat);
    let chat_text = chat_canonical
        .iter()
        .filter_map(|event| match event {
            CanonicalStreamEvent::TextDelta { delta, .. } => Some(delta.as_str()),
            _ => None,
        })
        .collect::<String>();
    assert_eq!(chat_text, text);
    assert!(matches!(
        chat_canonical.last(),
        Some(CanonicalStreamEvent::ResponseEnd {
            finish_reason: FinishReason::Stop,
            usage: Some(TokenUsage {
                total_tokens: 6,
                ..
            }),
            ..
        })
    ));
}

#[test]
fn responses_encoder_emits_a_checked_state_machine_stream() {
    let events = vec![
        CanonicalStreamEvent::ResponseStart {
            id: "resp-self-check".to_owned(),
            model: "gpt-test".to_owned(),
        },
        CanonicalStreamEvent::ContentStart {
            index: 0,
            kind: StreamContentKind::Text,
        },
        CanonicalStreamEvent::TextDelta {
            index: 0,
            delta: "hello".to_owned(),
        },
        CanonicalStreamEvent::ContentEnd { index: 0 },
        CanonicalStreamEvent::ResponseEnd {
            finish_reason: FinishReason::Stop,
            usage: None,
            model: None,
        },
    ];
    let snapshot = ResponsesStreamSnapshot {
        events: response_events_to_responses(&events),
        usage: WireUsage::default(),
        extra: BTreeMap::new(),
    };
    let checked = responses_stream_to_canonical_checked(&snapshot)
        .expect("the Responses encoder output must satisfy its checked parser");
    assert!(checked.iter().any(|event| matches!(
        event,
        CanonicalStreamEvent::TextDelta { delta, .. } if delta == "hello"
    )));
    assert!(
        snapshot
            .events
            .iter()
            .any(|event| event.kind == "response.output_item.done")
    );

    let terminal_then_cancelled = vec![
        CanonicalStreamEvent::ResponseStart {
            id: "resp-terminal-cancel".to_owned(),
            model: "gpt-test".to_owned(),
        },
        CanonicalStreamEvent::ResponseEnd {
            finish_reason: FinishReason::Stop,
            usage: None,
            model: None,
        },
        CanonicalStreamEvent::Cancelled,
    ];
    let snapshot = ResponsesStreamSnapshot {
        events: response_events_to_responses(&terminal_then_cancelled),
        usage: WireUsage::default(),
        extra: BTreeMap::new(),
    };
    let checked = responses_stream_to_canonical_checked(&snapshot)
        .expect("a post-terminal cancellation remains a distinct checked event");
    assert_eq!(
        checked
            .iter()
            .filter(|event| matches!(event, CanonicalStreamEvent::ResponseEnd { .. }))
            .count(),
        1
    );
    assert!(
        checked
            .iter()
            .any(|event| matches!(event, CanonicalStreamEvent::Cancelled))
    );
}

#[test]
fn responses_stream_to_chat_matches_reverse_go_golden_semantics() {
    let chat: OpenAiStreamSnapshot =
        serde_json::from_str(&fixture("stream", "openai_responses", "openai"))
            .expect("golden Chat stream");
    let canonical = openai_stream_to_canonical(&chat);
    let text = canonical
        .iter()
        .filter_map(|event| match event {
            CanonicalStreamEvent::TextDelta { delta, .. } => Some(delta.as_str()),
            _ => None,
        })
        .collect::<String>();
    assert_eq!(text, "Hello world");
    assert!(matches!(
        canonical.last(),
        Some(CanonicalStreamEvent::ResponseEnd {
            finish_reason: FinishReason::Stop,
            usage: Some(TokenUsage {
                input_tokens: 4,
                output_tokens: 2,
                total_tokens: 6,
                ..
            }),
            ..
        })
    ));

    let responses_events = response_events_to_responses(&canonical);
    assert_eq!(
        responses_events
            .iter()
            .filter(|event| event.kind == "response.output_text.delta")
            .count(),
        2,
        "each Chat data frame remains a distinct Responses delta event"
    );
}

#[test]
fn openai_pair_golden_streams_execute_source_to_target_frame_by_frame() {
    let chat_source: OpenAiStreamSnapshot = serde_json::from_str(
        r#"{
          "events":[
            {"id":"chatcmpl-fixed","object":"chat.completion.chunk","created":1700000000,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]},
            {"id":"chatcmpl-fixed","object":"chat.completion.chunk","created":1700000000,"model":"gpt-test","choices":[{"index":0,"delta":{"content":" world"}}]},
            {"id":"chatcmpl-fixed","object":"chat.completion.chunk","created":1700000000,"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}
          ],
          "usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}
        }"#,
    )
    .expect("typed Chat source stream");
    let mut canonical = openai_stream_to_canonical(&chat_source);
    if let Some(CanonicalStreamEvent::ResponseStart { id, model }) = canonical.first_mut() {
        // These two values came from legacy ResponseStreamOptions, not from
        // the source payload. Supply the same route context before comparing.
        *id = "stream_fixed".to_owned();
        *model = "stream-model".to_owned();
    }
    let expected_responses: ResponsesStreamSnapshot =
        serde_json::from_str(&fixture("stream", "openai", "openai_responses"))
            .expect("golden Responses target stream");
    let mut actual_responses = ResponsesStreamSnapshot {
        events: response_events_to_responses(&canonical),
        usage: expected_responses.usage.clone(),
        extra: BTreeMap::new(),
    };
    for (actual, expected) in actual_responses
        .events
        .iter_mut()
        .zip(&expected_responses.events)
    {
        if let (Some(actual), Some(expected)) = (
            actual.payload.response.as_mut(),
            expected.payload.response.as_ref(),
        ) {
            // Billing provenance is injected by the relay accounting layer.
            actual.usage.clone_from(&expected.usage);
        }
    }
    for event in &mut actual_responses.events {
        if let Some(response) = event.payload.response.as_mut() {
            response.max_output_tokens = None;
            response.parallel_tool_calls = None;
            response.store = None;
            response.temperature = None;
            response.top_p = None;
        }
    }
    let mut expected_responses = expected_responses;
    for event in &mut expected_responses.events {
        if let Some(response) = event.payload.response.as_mut() {
            response.max_output_tokens = None;
            response.parallel_tool_calls = None;
            response.store = None;
            response.temperature = None;
            response.top_p = None;
        }
    }
    assert_eq!(actual_responses, expected_responses);

    let responses_source: ResponsesStreamSnapshot = serde_json::from_str(
        r#"{
          "events":[
            {"Type":"response.created","Payload":{"type":"response.created","response":{"id":"stream_fixed","object":"response","status":"in_progress","model":"stream-model","output":[]}}},
            {"Type":"response.output_item.added","Payload":{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"stream_fixed_msg_0","status":"in_progress","role":"assistant","content":[]}}},
            {"Type":"response.output_text.delta","Payload":{"type":"response.output_text.delta","delta":"Hello","output_index":0,"content_index":0,"item_id":"stream_fixed_msg_0"}},
            {"Type":"response.output_text.delta","Payload":{"type":"response.output_text.delta","delta":" world","output_index":0,"content_index":0,"item_id":"stream_fixed_msg_0"}},
            {"Type":"response.output_text.done","Payload":{"type":"response.output_text.done","output_index":0,"content_index":0,"item_id":"stream_fixed_msg_0"}},
            {"Type":"response.output_item.done","Payload":{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"stream_fixed_msg_0","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello world","annotations":[]}]}}},
            {"Type":"response.completed","Payload":{"type":"response.completed","response":{"id":"stream_fixed","object":"response","status":"completed","model":"stream-model","output":[],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}}
          ],
          "usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}
        }"#,
    )
    .expect("typed Responses source stream");
    let canonical = responses_stream_to_canonical(&responses_source);
    let expected_chat: OpenAiStreamSnapshot =
        serde_json::from_str(&fixture("stream", "openai_responses", "openai"))
            .expect("golden Chat target stream");
    let mut actual_chat = OpenAiStreamSnapshot {
        events: response_events_to_openai_chunks(&canonical),
        usage: expected_chat.usage.clone(),
        extra: BTreeMap::new(),
    };
    // Rust carries terminal usage on the final Chat chunk as well as the
    // aggregate; the frozen Go fixture stored it only in the aggregate.
    if let Some(last) = actual_chat.events.last_mut() {
        last.usage = None;
    }
    assert_eq!(actual_chat, expected_chat);
}

#[test]
fn explicit_error_and_cancel_events_are_not_coalesced() {
    let events = [
        CanonicalStreamEvent::Error {
            code: None,
            message: "upstream failed".to_owned(),
        },
        CanonicalStreamEvent::Cancelled,
    ];
    assert_eq!(events.len(), 2);
    assert!(matches!(events[0], CanonicalStreamEvent::Error { .. }));
    assert_eq!(events[1], CanonicalStreamEvent::Cancelled);
}

#[test]
fn responses_scalar_input_converts_to_one_user_message() {
    let request: OpenAiResponsesRequest =
        serde_json::from_str(r#"{"model":"gpt-test","input":"hello","stream":false}"#)
            .expect("Responses accepts scalar string input");
    let canonical = openai_responses_request_to_canonical(request)
        .expect("scalar input conversion")
        .value;
    assert_eq!(
        canonical.messages,
        [CanonicalMessage {
            role: Role::User,
            parts: vec![CanonicalContent::Text {
                text: "hello".to_owned(),
            }],
        }]
    );
}

#[test]
fn chat_unknown_fields_reject_but_responses_wire_retains_them_for_preflight() {
    let chat = serde_json::from_str::<OpenAiChatRequest>(
        r#"{"model":"gpt-test","messages":[],"made_up":true}"#,
    );
    let responses = serde_json::from_str::<OpenAiResponsesRequest>(
        r#"{"model":"gpt-test","input":[],"made_up":true}"#,
    )
    .expect("Responses retains unknown fields");
    assert!(chat.is_err());
    assert_eq!(responses.extra.get("made_up"), Some(&JsonData::Bool(true)));
    assert!(matches!(
        openai_responses_request_to_canonical(responses),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.path == "made_up" && error.feature == "unknown_field"
    ));
}

#[test]
fn outbound_streams_do_not_drop_tools_errors_or_cancellation() {
    let events = vec![
        CanonicalStreamEvent::ResponseStart {
            id: "resp_test".to_owned(),
            model: "gpt-test".to_owned(),
        },
        CanonicalStreamEvent::ToolCallStart {
            index: 0,
            id: "call_1".to_owned(),
            name: "weather".to_owned(),
        },
        CanonicalStreamEvent::ToolArgumentsDelta {
            index: 0,
            delta: "{\"city\":".to_owned(),
        },
        CanonicalStreamEvent::ToolArgumentsDelta {
            index: 0,
            delta: "\"Paris\"}".to_owned(),
        },
        CanonicalStreamEvent::ReasoningDelta {
            index: 1,
            delta: "thinking".to_owned(),
        },
        CanonicalStreamEvent::ResponseEnd {
            finish_reason: FinishReason::Length,
            usage: Some(TokenUsage {
                input_tokens: 4,
                output_tokens: 2,
                total_tokens: 6,
                cached_input_tokens: 0,
                reasoning_tokens: 1,
            }),
            model: None,
        },
        CanonicalStreamEvent::Error {
            code: Some("upstream_error".to_owned()),
            message: "upstream failed".to_owned(),
        },
        CanonicalStreamEvent::Cancelled,
    ];

    let chat = response_events_to_openai_chunks(&events);
    let responses = response_events_to_responses(&events);
    assert!(
        chat.iter()
            .flat_map(|chunk| &chunk.choices)
            .any(|choice| !choice.delta.tool_calls.is_empty())
    );
    assert!(
        chat.iter()
            .flat_map(|chunk| &chunk.choices)
            .any(|choice| { choice.delta.reasoning_content.as_deref() == Some("thinking") })
    );
    assert!(chat.iter().any(|chunk| {
        chunk
            .usage
            .as_ref()
            .is_some_and(|usage| usage.total_tokens == 6)
            && chunk
                .choices
                .first()
                .is_some_and(|choice| choice.finish_reason.as_deref() == Some("length"))
    }));
    assert!(chat.iter().any(|chunk| {
        chunk.error.as_ref().is_some_and(|error| {
            error.code.as_deref() == Some("upstream_error") && error.message == "upstream failed"
        })
    }));
    assert!(chat.iter().any(|chunk| chunk.cancelled));
    assert_eq!(
        responses
            .iter()
            .filter(|event| event.kind == "response.function_call_arguments.delta")
            .count(),
        2
    );
    assert!(responses.iter().any(|event| event.kind == "response.error"));
    assert!(
        responses
            .iter()
            .any(|event| event.kind == "response.reasoning_summary_text.delta")
    );
    assert!(responses.iter().any(|event| {
        event.kind == "response.incomplete"
            && event.payload.response.as_ref().is_some_and(|response| {
                response
                    .incomplete_details
                    .as_ref()
                    .is_some_and(|details| details.reason == "max_output_tokens")
                    && response
                        .usage
                        .as_ref()
                        .is_some_and(|usage| usage.total_tokens == 6)
            })
    }));
    assert!(
        responses
            .iter()
            .any(|event| event.kind == "response.cancelled")
    );
}

#[test]
fn inbound_responses_stream_preserves_tool_reasoning_error_and_incomplete_finish() {
    let snapshot: ResponsesStreamSnapshot = serde_json::from_str(
        r#"{
          "events":[
            {"Type":"response.created","Payload":{"type":"response.created","response":{"id":"resp_test","object":"response","status":"in_progress","model":"gpt-test","output":[]}}},
            {"Type":"response.output_item.added","Payload":{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_1","name":"weather","status":"in_progress","arguments":""}}},
            {"Type":"response.function_call_arguments.delta","Payload":{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"city\":\"Paris\"}"}},
            {"Type":"response.output_item.added","Payload":{"type":"response.output_item.added","output_index":1,"item":{"type":"reasoning","status":"in_progress","summary":[]}}},
            {"Type":"response.reasoning_summary_text.delta","Payload":{"type":"response.reasoning_summary_text.delta","output_index":1,"delta":"think"}},
            {"Type":"response.function_call_arguments.done","Payload":{"type":"response.function_call_arguments.done","output_index":0}},
            {"Type":"response.output_item.done","Payload":{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"call_1","name":"weather","status":"completed","arguments":"{\"city\":\"Paris\"}"}}},
            {"Type":"response.reasoning_summary_text.done","Payload":{"type":"response.reasoning_summary_text.done","output_index":1,"summary_index":0,"part":{"type":"summary_text","text":"think"}}},
            {"Type":"response.output_item.done","Payload":{"type":"response.output_item.done","output_index":1,"item":{"type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"think"}]}}},
            {"Type":"response.incomplete","Payload":{"type":"response.incomplete","response":{"id":"resp_test","object":"response","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"model":"gpt-test","output":[],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}},
            {"Type":"response.error","Payload":{"type":"response.error","error":{"code":"upstream_error","message":"boom"}}}
          ],
          "usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}
        }"#,
    )
    .expect("typed rich Responses stream");
    let canonical = responses_stream_to_canonical(&snapshot);
    assert!(canonical.iter().any(|event| matches!(
        event,
        CanonicalStreamEvent::ToolCallStart { id, name, .. }
            if id == "call_1" && name == "weather"
    )));
    assert!(canonical.iter().any(|event| matches!(
        event,
        CanonicalStreamEvent::ToolArgumentsDelta { delta, .. }
            if delta == "{\"city\":\"Paris\"}"
    )));
    assert!(canonical.iter().any(|event| matches!(
        event,
        CanonicalStreamEvent::ResponseEnd {
            finish_reason: FinishReason::Length,
            ..
        }
    )));
    assert!(canonical.iter().any(|event| matches!(
        event,
        CanonicalStreamEvent::Error { message, .. } if message == "boom"
    )));
}

#[test]
fn go_supported_chat_request_fields_survive_responses_round_trip() {
    let source: OpenAiChatRequest = serde_json::from_str(
        r#"{
          "model":"gpt-test","messages":[{"role":"user","content":"hello"}],
          "stream":true,"max_completion_tokens":321,"temperature":0.4,"top_p":0.8,
          "reasoning_effort":"high","response_format":{"type":"json_object"},
          "parallel_tool_calls":true,"user":"user-1","store":false,
          "metadata":{"tenant":"acme"},"stream_options":{"include_usage":true},
          "top_logprobs":3,"safety_identifier":"safe-1",
          "prompt_cache_retention":"24h","prompt_cache_key":"cache-1",
          "service_tier":"flex","enable_thinking":true,"thinking_budget":2048
        }"#,
    )
    .expect("rich Chat request");
    let canonical = openai_chat_request_to_canonical(source.clone())
        .expect("Chat to canonical")
        .value;
    let responses = canonical_request_to_openai_responses(canonical)
        .expect("canonical to Responses")
        .value;
    assert_eq!(
        responses.text,
        Some(JsonData::Object(
            [("format".to_owned(), source.response_format.clone().unwrap())]
                .into_iter()
                .collect()
        ))
    );
    let canonical = openai_responses_request_to_canonical(responses)
        .expect("Responses to canonical")
        .value;
    let actual = canonical_request_to_openai_chat(canonical)
        .expect("canonical to Chat")
        .value;
    assert_eq!(actual, source);
}

#[test]
fn canonical_claude_request_reports_unrepresentable_options() {
    let request = CanonicalRequest {
        model: "claude-test".to_owned(),
        instructions: Vec::new(),
        messages: vec![CanonicalMessage {
            role: Role::User,
            parts: vec![CanonicalContent::Text {
                text: "hello".to_owned(),
            }],
        }],
        max_output_tokens: Some(32),
        temperature: None,
        stream: false,
        tools: Vec::new(),
        tool_choice: None,
        options: RequestOptions {
            top_p: Some(0.5),
            user: Some(JsonData::String("tenant-a".to_owned())),
            ..RequestOptions::default()
        },
    };
    let converted = canonical_request_to_claude(request).expect("canonical to Claude");
    assert_eq!(
        converted.loss.dropped_fields,
        ["options.top_p", "options.user"]
    );
}

#[test]
fn non_representable_request_fields_are_reported_or_rejected() {
    let multiple: OpenAiChatRequest =
        serde_json::from_str(r#"{"model":"gpt-test","messages":[],"n":2}"#)
            .expect("typed Chat request");
    assert!(matches!(
        openai_chat_request_to_canonical(multiple),
        Err(RelayConvertError::Unsupported(message)) if message.contains("n > 1")
    ));

    let stateful: OpenAiResponsesRequest = serde_json::from_str(
        r#"{"model":"gpt-test","input":[],"previous_response_id":"resp_previous"}"#,
    )
    .expect("typed stateful Responses request");
    assert!(matches!(
        openai_responses_request_to_canonical(stateful),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.code == ConversionUnsupportedFeature::CODE
                && error.path == "previous_response_id"
                && error.feature == "previous_response_id"
                && !error.retryable
    ));

    let reasoning: OpenAiResponsesRequest = serde_json::from_str(
        r#"{"model":"gpt-test","input":[],"reasoning":{"effort":"high","summary":"auto"}}"#,
    )
    .expect("typed reasoning request");
    assert!(matches!(
        openai_responses_request_to_canonical(reasoning),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "reasoning_summary"
                && error.path == "reasoning.summary"
                && error.loss_code.as_deref() == Some("LOSS_OPAQUE_REASONING")
    ));
}

#[test]
fn openai_stream_nested_unknown_fields_are_typed_rejections() {
    let cases = [
        (
            serde_json::json!({"futureChunkField": true}),
            "events[0].futureChunkField",
        ),
        (
            serde_json::json!({
                "choices":[{"delta":{},"index":0,"futureChoiceField":true}]
            }),
            "events[0].choices[0].futureChoiceField",
        ),
        (
            serde_json::json!({
                "choices":[{"delta":{"futureDeltaField":true},"index":0}]
            }),
            "events[0].choices[0].delta.futureDeltaField",
        ),
        (
            serde_json::json!({
                "choices":[{"delta":{"tool_calls":[{"index":0,"futureToolCallField":true}]},"index":0}]
            }),
            "events[0].choices[0].delta.tool_calls[0].futureToolCallField",
        ),
        (
            serde_json::json!({
                "choices":[{"delta":{"tool_calls":[{"index":0,"function":{"futureFunctionField":true}}]},"index":0}]
            }),
            "events[0].choices[0].delta.tool_calls[0].function.futureFunctionField",
        ),
    ];
    let retained: OpenAiStreamSnapshot = serde_json::from_value(serde_json::json!({
        "events": [{"futureChunkField": true}],
        "usage": {},
    }))
    .expect("unknown OpenAI stream chunk field is retained by the wire DTO");
    assert!(retained.events[0].extra.contains_key("futureChunkField"));

    let snapshot_with_extra: OpenAiStreamSnapshot = serde_json::from_value(serde_json::json!({
        "events": [],
        "usage": {},
        "futureSnapshotField": true,
    }))
    .expect("unknown OpenAI stream snapshot field is retained by the wire DTO");
    let snapshot_error = validate_openai_stream_snapshot(&snapshot_with_extra)
        .expect_err("validator must reject unknown snapshot fields");
    assert!(matches!(
        snapshot_error,
        RelayConvertError::UnsupportedFeature(detail)
            if detail.path == "snapshot.futureSnapshotField"
                && detail.feature == "unknown_field"
    ));

    let error_snapshot: OpenAiStreamSnapshot = serde_json::from_value(serde_json::json!({
        "events": [{
            "error": {"message": "upstream failed", "futureErrorField": true}
        }],
        "usage": {},
    }))
    .expect("unknown OpenAI stream error field is retained by the wire DTO");
    let error_field = validate_openai_stream_snapshot(&error_snapshot)
        .expect_err("validator must reject unknown stream error fields");
    assert!(matches!(
        error_field,
        RelayConvertError::UnsupportedFeature(detail)
            if detail.path == "events[0].error.futureErrorField"
                && detail.feature == "unknown_field"
    ));

    for (event, expected_path) in cases {
        let snapshot: OpenAiStreamSnapshot =
            serde_json::from_value(serde_json::json!({"events":[event],"usage":{}}))
                .expect("unknown OpenAI stream field is retained by the wire DTO");
        let error = openai_stream_to_canonical_checked(&snapshot)
            .expect_err("checked OpenAI stream conversion must reject unknown fields");
        assert!(matches!(
            error,
            RelayConvertError::UnsupportedFeature(detail)
                if detail.feature == "unknown_field"
                    && detail.path == expected_path
                    && detail.source_format == "openai_chat"
                    && detail.target_format == "provider_neutral_ir"
        ));
    }
}

#[test]
fn openai_stream_validator_reports_nested_function_unknown_path() {
    let snapshot: OpenAiStreamSnapshot = serde_json::from_value(serde_json::json!({
        "events": [{
            "choices": [{
                "index": 0,
                "delta": {
                    "tool_calls": [{
                        "index": 0,
                        "function": {"futureFunctionField": true}
                    }]
                }
            }]
        }],
        "usage": {}
    }))
    .expect("unknown OpenAI function field is retained by the wire DTO");

    let error = validate_openai_stream_snapshot(&snapshot)
        .expect_err("validator must reject unknown nested function fields");
    assert!(matches!(
        error,
        RelayConvertError::UnsupportedFeature(detail)
            if detail.path == "events[0].choices[0].delta.tool_calls[0].function.futureFunctionField"
                && detail.feature == "unknown_field"
    ));

    let golden_json = serde_json::to_string(&snapshot).expect("snapshot serializes");
    let golden_error = validate_golden(FixtureKind::Stream, Protocol::OpenAi, &golden_json)
        .expect_err("OpenAI stream golden validation must use the checked validator");
    assert!(matches!(
        golden_error,
        RelayConvertError::UnsupportedFeature(detail)
            if detail.path == "events[0].choices[0].delta.tool_calls[0].function.futureFunctionField"
    ));
}

#[test]
fn openai_stream_usage_unknown_fields_are_typed_rejections() {
    let cases = [
        (
            serde_json::json!({
                "events": [],
                "usage": {"futureUsageField": true}
            }),
            "snapshot.usage.futureUsageField",
        ),
        (
            serde_json::json!({
                "events": [],
                "usage": {
                    "prompt_tokens_details": {"futurePromptField": true}
                }
            }),
            "snapshot.usage.prompt_tokens_details.futurePromptField",
        ),
        (
            serde_json::json!({
                "events": [],
                "usage": {
                    "completion_tokens_details": {"futureCompletionField": true}
                }
            }),
            "snapshot.usage.completion_tokens_details.futureCompletionField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "usage": {
                        "input_tokens_details": {"futureInputField": true}
                    }
                }],
                "usage": {}
            }),
            "events[0].usage.input_tokens_details.futureInputField",
        ),
    ];

    for (document, expected_path) in cases {
        let snapshot: OpenAiStreamSnapshot = serde_json::from_value(document)
            .expect("unknown OpenAI stream usage field is retained by the wire DTO");
        let error = validate_openai_stream_snapshot(&snapshot)
            .expect_err("OpenAI stream usage validator must reject unknown fields");
        assert!(matches!(
            error,
            RelayConvertError::UnsupportedFeature(detail)
                if detail.feature == "unknown_field"
                    && detail.path == expected_path
                    && detail.source_format == "openai_chat"
                    && detail.target_format == "provider_neutral_ir"
        ));
    }
}

#[test]
fn responses_stream_unknown_envelope_fields_are_typed_rejections() {
    let cases = [
        (
            serde_json::json!({
                "events": [],
                "usage": {},
                "futureSnapshotField": true
            }),
            "snapshot.futureSnapshotField",
        ),
        (
            serde_json::json!({
                "events": [],
                "usage": {"futureUsageField": true}
            }),
            "snapshot.usage.futureUsageField",
        ),
        (
            serde_json::json!({
                "events": [],
                "usage": {
                    "prompt_tokens_details": {"futurePromptField": true}
                }
            }),
            "snapshot.usage.prompt_tokens_details.futurePromptField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "Type": "response.error",
                    "Payload": {
                        "type": "response.error",
                        "error": {"message": "boom", "futurePayloadErrorField": true}
                    }
                }],
                "usage": {}
            }),
            "events[0].error.futurePayloadErrorField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "Type": "response.error",
                    "Payload": {
                        "type": "response.error",
                        "response": {
                            "id": "resp-error",
                            "object": "response",
                            "status": "failed",
                            "model": "gpt-test",
                            "error": {"message": "boom", "futureResponseErrorField": true}
                        }
                    }
                }],
                "usage": {}
            }),
            "events[0].response.error.futureResponseErrorField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "Type": "response.completed",
                    "Payload": {
                        "type": "response.completed",
                        "response": {
                            "id": "resp-complete",
                            "object": "response",
                            "status": "completed",
                            "model": "gpt-test",
                            "usage": {"futureResponseUsageField": true}
                        }
                    }
                }],
                "usage": {}
            }),
            "events[0].response.usage.futureResponseUsageField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "Type": "response.completed",
                    "Payload": {
                        "type": "response.completed",
                        "response": {
                            "id": "resp-complete",
                            "object": "response",
                            "status": "completed",
                            "model": "gpt-test",
                            "usage": {
                                "completion_tokens_details": {
                                    "futureCompletionField": true
                                }
                            }
                        }
                    }
                }],
                "usage": {}
            }),
            "events[0].response.usage.completion_tokens_details.futureCompletionField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "Type": "response.completed",
                    "Payload": {
                        "type": "response.completed",
                        "response": {
                            "id": "resp-complete",
                            "object": "response",
                            "status": "completed",
                            "model": "gpt-test",
                            "usage": {
                                "input_tokens_details": {
                                    "futureInputField": true
                                }
                            }
                        }
                    }
                }],
                "usage": {}
            }),
            "events[0].response.usage.input_tokens_details.futureInputField",
        ),
    ];

    for (document, expected_path) in cases {
        let snapshot: ResponsesStreamSnapshot = serde_json::from_value(document)
            .expect("unknown Responses stream envelope field is retained by the wire DTO");
        let error = responses_stream_to_canonical_checked(&snapshot)
            .expect_err("checked Responses stream conversion must reject unknown fields");
        assert!(matches!(
            error,
            RelayConvertError::UnsupportedFeature(detail)
                if detail.feature == "unknown_field"
                    && detail.path == expected_path
                    && detail.source_format == "openai_responses"
                    && detail.target_format == "provider_neutral_ir"
        ));
    }
}

#[test]
fn responses_response_usage_unknown_fields_are_typed_rejections() {
    let cases = [
        (
            serde_json::json!({"futureUsageField": true}),
            "usage.futureUsageField",
        ),
        (
            serde_json::json!({
                "prompt_tokens_details": {"futurePromptField": true}
            }),
            "usage.prompt_tokens_details.futurePromptField",
        ),
    ];

    for (usage, expected_path) in cases {
        let response: ResponsesResponse = serde_json::from_value(serde_json::json!({
            "id": "resp-usage",
            "object": "response",
            "model": "gpt-test",
            "status": "completed",
            "output": [],
            "usage": usage
        }))
        .expect("unknown Responses response usage field is retained by the wire DTO");
        let error = openai_responses_response_to_canonical(response)
            .expect_err("canonical Responses conversion must reject unknown usage fields");
        assert!(matches!(
            error,
            RelayConvertError::UnsupportedFeature(detail)
                if detail.feature == "unknown_field"
                    && detail.path == expected_path
                    && detail.source_format == "openai_responses"
                    && detail.target_format == "provider_neutral_ir"
        ));
    }
}

#[test]
fn openai_chat_response_usage_unknown_fields_are_typed_rejections() {
    let cases = [
        (
            serde_json::json!({"futureUsageField": true}),
            "usage.futureUsageField",
        ),
        (
            serde_json::json!({
                "prompt_tokens_details": {"futurePromptField": true}
            }),
            "usage.prompt_tokens_details.futurePromptField",
        ),
        (
            serde_json::json!({
                "completion_tokens_details": {"futureCompletionField": true}
            }),
            "usage.completion_tokens_details.futureCompletionField",
        ),
    ];

    for (usage, expected_path) in cases {
        let response: OpenAiChatResponse = serde_json::from_value(serde_json::json!({
            "id": "chat-usage",
            "model": "gpt-test",
            "object": "chat.completion",
            "created": 1,
            "choices": [{
                "index": 0,
                "message": {"role": "assistant"},
                "finish_reason": "stop"
            }],
            "usage": usage
        }))
        .expect("unknown OpenAI Chat response usage field is retained by the wire DTO");
        let error = openai_chat_response_to_canonical(response)
            .expect_err("OpenAI Chat response conversion must reject unknown usage fields");
        assert!(matches!(
            error,
            RelayConvertError::UnsupportedFeature(detail)
                if detail.feature == "unknown_field"
                    && detail.path == expected_path
                    && detail.source_format == "openai_chat"
                    && detail.target_format == "provider_neutral_ir"
        ));
    }
}

#[test]
fn gemini_response_usage_unknown_fields_are_typed_rejections() {
    let response: GeminiResponse = serde_json::from_value(serde_json::json!({
        "candidates": [{
            "content": {"parts": [{"text": "ok"}]}
        }],
        "usageMetadata": {"futureUsageField": true}
    }))
    .expect("unknown Gemini response usage field is retained by the wire DTO");
    let error = gemini_response_to_canonical(response)
        .expect_err("Gemini response conversion must reject unknown usage fields");
    assert!(matches!(
        error,
        RelayConvertError::UnsupportedFeature(detail)
            if detail.feature == "unknown_field"
                && detail.path == "usageMetadata.futureUsageField"
                && detail.source_format == "gemini"
                && detail.target_format == "provider_neutral_ir"
    ));
}

#[test]
fn claude_response_usage_unknown_fields_are_typed_rejections() {
    let response: ClaudeResponse = serde_json::from_value(serde_json::json!({
        "id": "msg-usage",
        "type": "message",
        "role": "assistant",
        "model": "claude-sonnet",
        "content": [{"type": "text", "text": "ok"}],
        "usage": {"futureUsageField": true}
    }))
    .expect("unknown Claude response usage field is retained by the wire DTO");
    let error = claude_response_to_canonical(response)
        .expect_err("Claude response conversion must reject unknown usage fields");
    assert!(matches!(
        error,
        RelayConvertError::UnsupportedFeature(detail)
            if detail.feature == "unknown_field"
                && detail.path == "usage.futureUsageField"
                && detail.source_format == "claude"
                && detail.target_format == "provider_neutral_ir"
    ));
}

#[test]
fn responses_incomplete_details_round_trip_to_chat_finish_reasons() {
    for (reason, expected) in [
        ("max_output_tokens", FinishReason::Length),
        ("content_filter", FinishReason::ContentFilter),
    ] {
        let response: ResponsesResponse = serde_json::from_value(serde_json::json!({
            "id":"resp_test","object":"response","model":"gpt-test",
            "status":"incomplete","incomplete_details":{"reason":reason},"output":[]
        }))
        .expect("typed incomplete response");
        let canonical = openai_responses_response_to_canonical(response)
            .expect("canonical response")
            .value;
        assert_eq!(canonical.finish_reason, Some(expected));
        let responses = canonical_response_to_openai_responses(canonical.clone()).value;
        assert_eq!(responses.status, "incomplete");
        assert_eq!(
            responses
                .incomplete_details
                .as_ref()
                .map(|value| value.reason.as_str()),
            Some(reason)
        );
        let chat = canonical_response_to_openai_chat(canonical).value;
        assert_eq!(
            chat.choices[0].finish_reason.as_deref(),
            Some(if expected == FinishReason::Length {
                "length"
            } else {
                "content_filter"
            })
        );
    }
}

#[test]
fn canonical_stream_to_responses_has_exact_semantic_frame_sequence() {
    let usage = TokenUsage {
        input_tokens: 4,
        output_tokens: 2,
        total_tokens: 6,
        cached_input_tokens: 1,
        reasoning_tokens: 1,
    };
    let events = vec![
        CanonicalStreamEvent::ResponseStart {
            id: "resp_test".to_owned(),
            model: "gpt-test".to_owned(),
        },
        CanonicalStreamEvent::ContentStart {
            index: 0,
            kind: StreamContentKind::Text,
        },
        CanonicalStreamEvent::TextDelta {
            index: 0,
            delta: "Hi".to_owned(),
        },
        CanonicalStreamEvent::ContentEnd { index: 0 },
        CanonicalStreamEvent::ContentStart {
            index: 1,
            kind: StreamContentKind::Reasoning,
        },
        CanonicalStreamEvent::ReasoningDelta {
            index: 1,
            delta: "think".to_owned(),
        },
        CanonicalStreamEvent::ContentEnd { index: 1 },
        CanonicalStreamEvent::ToolCallStart {
            index: 2,
            id: "call_1".to_owned(),
            name: "weather".to_owned(),
        },
        CanonicalStreamEvent::ToolArgumentsDelta {
            index: 2,
            delta: "{}".to_owned(),
        },
        CanonicalStreamEvent::ContentEnd { index: 2 },
        CanonicalStreamEvent::ResponseEnd {
            finish_reason: FinishReason::Length,
            usage: Some(usage),
            model: None,
        },
    ];
    let frames = response_events_to_responses(&events);
    assert_eq!(
        frames
            .iter()
            .map(|frame| frame.kind.as_str())
            .collect::<Vec<_>>(),
        [
            "response.created",
            "response.output_item.added",
            "response.output_text.delta",
            "response.output_text.done",
            "response.output_item.done",
            "response.output_item.added",
            "response.reasoning_summary_text.delta",
            "response.reasoning_summary_text.done",
            "response.output_item.done",
            "response.output_item.added",
            "response.function_call_arguments.delta",
            "response.function_call_arguments.done",
            "response.output_item.done",
            "response.incomplete",
        ]
    );
    assert_eq!(frames[2].payload.delta.as_deref(), Some("Hi"));
    assert_eq!(frames[3].payload.text, None);
    assert_eq!(frames[6].payload.delta.as_deref(), Some("think"));
    assert_eq!(
        frames[7]
            .payload
            .part
            .as_ref()
            .and_then(|part| part.text.as_deref()),
        Some("think")
    );
    assert_eq!(
        frames[9].payload.item.as_ref().unwrap().call_id.as_deref(),
        Some("call_1")
    );
    assert_eq!(
        frames[9].payload.item.as_ref().unwrap().name.as_deref(),
        Some("weather")
    );
    assert_eq!(frames[10].payload.delta.as_deref(), Some("{}"));
    assert_eq!(frames[11].payload.arguments, None);
    let completed = frames.last().unwrap().payload.response.as_ref().unwrap();
    assert_eq!(completed.status, "incomplete");
    assert_eq!(
        completed.incomplete_details.as_ref().unwrap().reason,
        "max_output_tokens"
    );
    assert_eq!(completed.usage.as_ref().unwrap().total_tokens, 6);
    assert_eq!(completed.output.len(), 3);
}

#[test]
fn chat_stream_allocates_distinct_global_responses_output_indices() {
    let snapshot: OpenAiStreamSnapshot = serde_json::from_str(
        r#"{
          "events":[
            {"id":"chatcmpl_test","model":"gpt-test","choices":[{"index":0,"delta":{"reasoning_content":"think","content":"answer","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"weather","arguments":"{}"}}]}}]},
            {"id":"chatcmpl_test","model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}
          ],
          "usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}
        }"#,
    )
    .expect("mixed Chat stream");
    let canonical = openai_stream_to_canonical(&snapshot);
    assert!(
        canonical
            .iter()
            .any(|event| matches!(event, CanonicalStreamEvent::ReasoningDelta { index: 0, .. }))
    );
    assert!(
        canonical
            .iter()
            .any(|event| matches!(event, CanonicalStreamEvent::TextDelta { index: 1, .. }))
    );
    assert!(
        canonical
            .iter()
            .any(|event| matches!(event, CanonicalStreamEvent::ToolCallStart { index: 2, .. }))
    );
    assert!(canonical.iter().any(|event| matches!(
        event,
        CanonicalStreamEvent::ToolArgumentsDelta { index: 2, .. }
    )));
    let responses = response_events_to_responses(&canonical);
    let completed = responses
        .iter()
        .find(|event| event.kind == "response.completed")
        .and_then(|event| event.payload.response.as_ref())
        .expect("completed response");
    assert_eq!(completed.output.len(), 3);
}

#[test]
fn responses_failed_stream_reads_the_nested_response_error() {
    let snapshot: ResponsesStreamSnapshot = serde_json::from_str(
        r#"{
          "events":[
            {"Type":"response.created","Payload":{"type":"response.created","response":{"id":"resp_test","object":"response","status":"in_progress","model":"gpt-test","output":[]}}},
            {"Type":"response.failed","Payload":{"type":"response.failed","response":{"id":"resp_test","object":"response","status":"failed","model":"gpt-test","output":[],"error":{"code":"model_error","message":"nested boom"}}}}
          ],
          "usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}
        }"#,
    )
    .expect("nested Responses error");
    let canonical = responses_stream_to_canonical(&snapshot);
    assert!(canonical.iter().any(|event| matches!(
        event,
        CanonicalStreamEvent::Error { code, message }
            if code.as_deref() == Some("model_error") && message == "nested boom"
    )));
    assert!(canonical.iter().any(|event| matches!(
        event,
        CanonicalStreamEvent::ResponseEnd {
            finish_reason: FinishReason::Error,
            usage: Some(TokenUsage {
                total_tokens: 4,
                ..
            }),
            ..
        }
    )));
}

#[test]
fn nested_unknown_request_fields_are_retained_then_rejected_by_cross_protocol_preflight() {
    for invalid in [
        r#"{"model":"gpt-test","messages":[{"role":"user","content":"hi","made_up":true}]}"#,
        r#"{"model":"gpt-test","messages":[],"tools":[{"type":"function","function":{"name":"f","parameters":{},"made_up":true}}]}"#,
    ] {
        assert!(serde_json::from_str::<OpenAiChatRequest>(invalid).is_err());
    }
    for invalid in [
        r#"{"model":"gpt-test","input":[{"type":"message","role":"user","content":"hi","made_up":true}]}"#,
        r#"{"model":"gpt-test","input":[],"tools":[{"type":"function","name":"f","parameters":{},"made_up":true}]}"#,
    ] {
        let request = serde_json::from_str::<OpenAiResponsesRequest>(invalid)
            .expect("Responses retains nested extension");
        assert!(matches!(
            openai_responses_request_to_canonical(request),
            Err(RelayConvertError::UnsupportedFeature(error))
                if error.feature == "unknown_field"
        ));
    }

    let source: OpenAiChatRequest = serde_json::from_str(
        r#"{"model":"gpt-test","messages":[],"tools":[{"type":"function","function":{"name":"f","parameters":{},"strict":true}}]}"#,
    )
    .expect("strict Chat tool");
    let canonical = openai_chat_request_to_canonical(source)
        .expect("Chat tool conversion")
        .value;
    let responses = canonical_request_to_openai_responses(canonical)
        .expect("Responses tool conversion")
        .value;
    assert_eq!(responses.tools[0].strict, Some(true));
}

#[test]
fn responses_preflight_rejects_builtin_without_name_and_keeps_builtin_path() {
    let request: OpenAiResponsesRequest = serde_json::from_str(
        r#"{"model":"gpt-test","tools":[{"type":"web_search","search_context_size":"high"}]}"#,
    )
    .expect("built-in tool is retained without a function name");
    let error = preflight_openai_responses_request_to_openai_chat(&request)
        .expect_err("Chat cannot represent a built-in tool");
    let RelayConvertError::UnsupportedFeature(error) = error else {
        panic!("expected typed unsupported feature");
    };
    assert_eq!(error.code, ConversionUnsupportedFeature::CODE);
    assert_eq!(error.feature, "builtin_web_search");
    assert_eq!(error.path, "tools[0]");
    assert_eq!(error.loss_code.as_deref(), Some("LOSS_BUILTIN_TOOL"));
    assert!(!error.retryable);
}

#[test]
fn responses_preflight_maps_every_known_builtin_and_future_tool_kind() {
    for (kind, feature) in [
        ("web_search", "builtin_web_search"),
        ("web_search_preview", "builtin_web_search"),
        ("file_search", "builtin_file_search"),
        ("code_interpreter", "builtin_code_execution"),
        ("mcp", "mcp"),
        ("computer_use", "computer_use"),
        ("computer_use_preview", "computer_use"),
        ("image_generation", "builtin_image_generation"),
        ("hosted_shell", "builtin_hosted_shell"),
        ("apply_patch", "builtin_apply_patch"),
        ("skills", "builtin_skills"),
        ("tool_search", "builtin_tool_search"),
        (
            "programmatic_tool_calling",
            "builtin_programmatic_tool_calling",
        ),
        ("future_tool_type", "unknown_tool_type"),
    ] {
        let request: OpenAiResponsesRequest = serde_json::from_str(&format!(
            r#"{{"model":"gpt-test","tools":[{{"type":"{kind}","provider_option":true}}]}}"#
        ))
        .expect("tool kind retained without requiring function name");
        let error = preflight_openai_responses_request_to_openai_chat(&request)
            .expect_err("non-function tool cannot cross into Chat");
        let RelayConvertError::UnsupportedFeature(error) = error else {
            panic!("expected structured tool feature error");
        };
        assert_eq!(error.code, ConversionUnsupportedFeature::CODE);
        assert_eq!(error.path, "tools[0]");
        assert_eq!(error.feature, feature);
        assert_eq!(
            error.loss_code.as_deref(),
            (kind != "future_tool_type").then_some("LOSS_BUILTIN_TOOL")
        );
        assert!(!error.retryable);
    }
}

#[test]
fn responses_preflight_reports_each_state_field_at_its_source_path() {
    for (field, value, feature) in [
        ("conversation", "{}", "stateful_conversation"),
        ("previous_response_id", "\"resp_1\"", "previous_response_id"),
        ("prompt", "{}", "prompt_template"),
        ("context_management", "{}", "context_management"),
    ] {
        let request: OpenAiResponsesRequest =
            serde_json::from_str(&format!(r#"{{"model":"gpt-test","{field}":{value}}}"#))
                .expect("state field retained");
        let error = preflight_openai_responses_request_to_openai_chat(&request)
            .expect_err("state cannot cross into stateless Chat");
        let RelayConvertError::UnsupportedFeature(error) = error else {
            panic!("expected structured state feature error");
        };
        assert_eq!(error.path, field);
        assert_eq!(error.feature, feature);
        assert_eq!(
            error.loss_code.as_deref(),
            (field != "prompt").then_some("LOSS_STATEFUL_CONTEXT")
        );
        assert!(!error.retryable);
    }
}

#[test]
fn responses_function_calls_match_parallel_results_by_exact_id_in_input_order() {
    let request: OpenAiResponsesRequest = serde_json::from_str(
        r#"{
          "model":"gpt-test",
          "input":[
            {"type":"function_call","call_id":"call_a","name":"one","arguments":"{}"},
            {"type":"function_call","call_id":"call_b","name":"two","arguments":"{}"},
            {"type":"function_call_output","call_id":"call_b","output":"b"},
            {"type":"function_call_output","call_id":"call_a","output":"a"}
          ]
        }"#,
    )
    .expect("parallel function calls");
    assert!(openai_responses_request_to_canonical(request).is_ok());

    let early: OpenAiResponsesRequest = serde_json::from_str(
        r#"{"model":"gpt-test","input":[{"type":"function_call_output","call_id":"call_a","output":"a"},{"type":"function_call","call_id":"call_a","name":"one","arguments":"{}"}]}"#,
    )
    .expect("early result wire");
    assert!(matches!(
        openai_responses_request_to_canonical(early),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.path == "input[0].call_id"
                && error.feature == "function_call_output_id"
    ));
}

#[test]
fn responses_input_items_reject_inapplicable_fields_and_missing_required_values() {
    for (item, expected_path) in [
        (
            r#"{"type":"message","role":"user","call_id":"call_1","content":"x"}"#,
            "input[0].call_id",
        ),
        (
            r#"{"type":"function_call","role":"assistant","call_id":"call_1","name":"run","arguments":"{}"}"#,
            "input[0].role",
        ),
        (
            r#"{"type":"function_call","content":"x","call_id":"call_1","name":"run","arguments":"{}"}"#,
            "input[0].content",
        ),
        (
            r#"{"type":"function_call","output":"x","call_id":"call_1","name":"run","arguments":"{}"}"#,
            "input[0].output",
        ),
        (
            r#"{"type":"function_call","call_id":"call_1","name":"run"}"#,
            "input[0].arguments",
        ),
        (
            r#"{"type":"function_call_output","call_id":"call_1","name":"run","output":"x"}"#,
            "input[0].name",
        ),
        (
            r#"{"type":"function_call_output","call_id":"call_1","arguments":"{}","output":"x"}"#,
            "input[0].arguments",
        ),
        (
            r#"{"type":"function_call_output","call_id":"call_1","content":"x","output":"x"}"#,
            "input[0].content",
        ),
        (
            r#"{"type":"function_call_output","call_id":"call_1","role":"tool","output":"x"}"#,
            "input[0].role",
        ),
        (
            r#"{"type":"function_call_output","call_id":"call_1"}"#,
            "input[0].output",
        ),
    ] {
        let request: OpenAiResponsesRequest =
            serde_json::from_str(&format!(r#"{{"model":"gpt-test","input":[{item}]}}"#))
                .expect("input item retained");
        assert!(matches!(
            openai_responses_request_to_canonical(request),
            Err(RelayConvertError::UnsupportedFeature(error))
                if error.path == expected_path
                    && matches!(
                        error.feature.as_str(),
                        "input_field" | "function_call_arguments" | "function_call_output"
                    )
        ));
    }
}

#[test]
fn responses_input_content_rejects_mixed_text_and_image_payloads() {
    for (content, expected_path) in [
        (
            r#"{"type":"input_text","text":"x","image_url":"https://example.test/image.png"}"#,
            "input[0].content[0].image_url",
        ),
        (
            r#"{"type":"input_image","image_url":"https://example.test/image.png","text":"x"}"#,
            "input[0].content[0].text",
        ),
    ] {
        let request: OpenAiResponsesRequest = serde_json::from_str(&format!(
            r#"{{"model":"gpt-test","input":[{{"type":"message","role":"user","content":[{content}]}}]}}"#
        ))
        .expect("mixed content retained");
        assert!(matches!(
            openai_responses_request_to_canonical(request),
            Err(RelayConvertError::UnsupportedFeature(error))
                if error.feature == "content_field" && error.path == expected_path
        ));
    }
}

#[test]
fn responses_unknown_content_and_custom_items_have_exact_typed_paths() {
    let unknown: OpenAiResponsesRequest = serde_json::from_str(
        r#"{"model":"gpt-test","input":[{"type":"message","role":"user","content":[{"type":"future_part","payload":true}]}]}"#,
    )
    .expect("future content part retained");
    assert!(matches!(
        openai_responses_request_to_canonical(unknown),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.path == "input[0].content[0]"
                && error.feature == "unknown_content_part"
    ));

    let custom: OpenAiResponsesRequest = serde_json::from_str(
        r#"{"model":"gpt-test","input":[{"type":"custom_tool_call","call_id":"custom_1","name":"run","arguments":"{}"}]}"#,
    )
    .expect("custom item retained");
    assert!(matches!(
        openai_responses_request_to_canonical(custom),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.path == "input[0]" && error.feature == "custom_tool"
    ));
}

#[test]
fn responses_unsupported_error_is_serializable_and_target_aware_without_body_text() {
    let request: OpenAiResponsesRequest =
        serde_json::from_str(r#"{"model":"gpt-test","tools":[{"type":"file_search"}]}"#)
            .expect("built-in tool");
    let error = preflight_openai_responses_request_for_target(&request, Protocol::Gemini)
        .expect_err("Gemini target is not Chat-equivalent for this tool");
    let RelayConvertError::UnsupportedFeature(error) = error else {
        panic!("expected typed unsupported feature");
    };
    assert_eq!(error.target_format, "google_gemini_generate_content");
    let serialized = serde_json::to_value(&error).expect("serializable feature error");
    assert_eq!(serialized["code"], ConversionUnsupportedFeature::CODE);
    assert_eq!(serialized["source_format"], "openai_responses");
    assert_eq!(serialized["path"], "tools[0]");
    assert_eq!(serialized["retryable"], false);
    assert!(serialized.get("raw_body").is_none());
}

#[test]
fn responses_response_preflight_rejects_unknown_output_and_missing_function_id() {
    let unknown: ResponsesResponse = serde_json::from_str(
        r#"{"id":"resp-1","object":"response","status":"completed","model":"gpt-test","output":[{"type":"future_output"}]}"#,
    )
    .expect("unknown output item retained");
    assert!(matches!(
        openai_responses_response_to_canonical(unknown),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.path == "output[0]" && error.feature == "unknown_output_item_type"
                && error.target_format == "provider_neutral_ir"
    ));

    let missing_id: ResponsesResponse = serde_json::from_str(
        r#"{"id":"resp-1","object":"response","status":"completed","model":"gpt-test","output":[{"type":"function_call","name":"run","arguments":"{}"}]}"#,
    )
    .expect("function output without call id retained");
    assert!(matches!(
        openai_responses_response_to_canonical(missing_id),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.path == "output[0].call_id" && error.feature == "function_call_id"
    ));
}

#[test]
fn responses_response_item_ids_are_reported_as_explicit_loss() {
    let response: ResponsesResponse = serde_json::from_str(
        r#"{
          "id":"resp-ids","object":"response","status":"completed","model":"gpt-test",
          "output":[
            {"type":"message","id":"msg_1","content":[{"type":"output_text","text":"ok"}]},
            {"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{}"}
          ]
        }"#,
    )
    .expect("response item ids retained");
    let converted = openai_responses_response_to_canonical(response)
        .expect("response item ids use explicit loss");
    assert_eq!(converted.loss.dropped_fields, ["output[].id"]);
}

#[test]
fn responses_response_preflight_rejects_missing_text_and_citations() {
    let missing_text: ResponsesResponse = serde_json::from_str(
        r#"{"id":"resp-text","object":"response","status":"completed","model":"gpt-test","output":[{"type":"message","content":[{"type":"output_text"}]}]}"#,
    )
    .expect("missing output text retained");
    assert!(matches!(
        openai_responses_response_to_canonical(missing_text),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "text_content"
                && error.path == "output[0].content[0].text"
    ));

    let annotated: ResponsesResponse = serde_json::from_str(
        r#"{"id":"resp-citation","object":"response","status":"completed","model":"gpt-test","output":[{"type":"message","content":[{"type":"output_text","text":"","annotations":[{"type":"url_citation"}]}]}]}"#,
    )
    .expect("annotations retained");
    assert!(matches!(
        openai_responses_response_to_canonical(annotated),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "citations"
                && error.path == "output[0].content[0].annotations"
                && error.loss_code.as_deref() == Some("LOSS_CITATION")
    ));
}

#[test]
fn responses_response_rejects_unmapped_output_fields_and_nonterminal_status() {
    for (output, expected_path) in [
        (
            r#"{"type":"message","call_id":"call_1","content":[{"type":"output_text","text":"x"}]}"#,
            "output[0].call_id",
        ),
        (
            r#"{"type":"function_call","content":[{"type":"output_text","text":"x"}],"call_id":"call_1","name":"run","arguments":"{}"}"#,
            "output[0].content",
        ),
        (
            r#"{"type":"function_call","role":"assistant","call_id":"call_1","name":"run","arguments":"{}"}"#,
            "output[0].role",
        ),
    ] {
        let response: ResponsesResponse = serde_json::from_str(&format!(
            r#"{{"id":"resp-fields","object":"response","status":"completed","model":"gpt-test","output":[{output}]}}"#
        ))
        .expect("response output field retained");
        assert!(matches!(
            openai_responses_response_to_canonical(response),
            Err(RelayConvertError::UnsupportedFeature(error))
                if error.path == expected_path
                    && matches!(error.feature.as_str(), "output_field" | "output_role")
        ));
    }

    let in_progress: ResponsesResponse = serde_json::from_str(
        r#"{"id":"resp-progress","object":"response","status":"in_progress","model":"gpt-test"}"#,
    )
    .expect("nonterminal response retained");
    assert!(matches!(
        openai_responses_response_to_canonical(in_progress),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "response_status" && error.path == "status"
    ));
}

#[test]
fn responses_response_controls_reject_present_zero_or_default_values() {
    for (field, value) in [
        ("max_output_tokens", "0"),
        ("temperature", "0.0"),
        ("top_p", "1.0"),
    ] {
        let response: ResponsesResponse = serde_json::from_str(&format!(
            r#"{{"id":"resp-control","object":"response","status":"completed","model":"gpt-test","{field}":{value}}}"#
        ))
        .expect("nullable response control retained");
        assert!(matches!(
            openai_responses_response_to_canonical(response),
            Err(RelayConvertError::UnsupportedFeature(error))
                if error.path == field
        ));
    }
    for (field, value) in [("parallel_tool_calls", "false"), ("store", "false")] {
        let response: ResponsesResponse = serde_json::from_str(&format!(
            r#"{{"id":"resp-control","object":"response","status":"completed","model":"gpt-test","{field}":{value}}}"#
        ))
        .expect("nullable response boolean retained");
        assert!(matches!(
            openai_responses_response_to_canonical(response),
            Err(RelayConvertError::UnsupportedFeature(error))
                if error.path == field
        ));
    }
}

#[test]
fn responses_response_rejects_incomplete_details_on_non_incomplete_status() {
    let response: ResponsesResponse = serde_json::from_str(
        r#"{"id":"resp-details","object":"response","status":"completed","model":"gpt-test","incomplete_details":{"reason":"max_output_tokens"}}"#,
    )
    .expect("incomplete details retained");
    assert!(matches!(
        openai_responses_response_to_canonical(response),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "incomplete_details" && error.path == "incomplete_details"
    ));
}

#[test]
fn responses_incomplete_unknown_reason_is_explicit_loss() {
    let response: ResponsesResponse = serde_json::from_str(
        r#"{"id":"resp-incomplete","object":"response","status":"incomplete","model":"gpt-test","incomplete_details":{"reason":"future_reason"}}"#,
    )
    .expect("unknown incomplete reason retained");
    let converted = openai_responses_response_to_canonical(response)
        .expect("unknown reason uses explicit loss ledger");
    assert_eq!(converted.loss.dropped_fields, ["incomplete_details.reason"]);
    assert_eq!(converted.value.finish_reason, Some(FinishReason::Other));
}

#[test]
fn responses_incomplete_detail_extensions_are_retained_then_rejected_with_path() {
    let response: ResponsesResponse = serde_json::from_str(
        r#"{"id":"resp-incomplete-extra","object":"response","status":"incomplete","model":"gpt-test","incomplete_details":{"reason":"max_output_tokens","future_detail":{"retry_after":1}}}"#,
    )
    .expect("future incomplete detail is retained by the wire DTO");
    assert!(
        response
            .incomplete_details
            .as_ref()
            .is_some_and(|details| details.extra.contains_key("future_detail"))
    );
    let serialized = serde_json::to_value(&response).expect("future detail serializes unchanged");
    assert_eq!(
        serialized["incomplete_details"]["future_detail"]["retry_after"],
        serde_json::json!(1)
    );
    assert!(matches!(
        openai_responses_response_to_canonical(response),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "unknown_field"
                && error.path == "incomplete_details.future_detail"
    ));
}

#[test]
fn responses_response_outer_state_and_non_default_controls_are_typed() {
    let stateful: ResponsesResponse = serde_json::from_str(
        r#"{"id":"resp-state","object":"response","status":"completed","model":"gpt-test","previous_response_id":"resp-old"}"#,
    )
    .expect("stateful response retained");
    assert!(matches!(
        openai_responses_response_to_canonical(stateful),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "stateful_conversation"
                && error.path == "previous_response_id"
    ));

    let controlled: ResponsesResponse = serde_json::from_str(
        r#"{"id":"resp-control","object":"response","status":"completed","model":"gpt-test","max_output_tokens":128}"#,
    )
    .expect("response control retained");
    assert!(matches!(
        openai_responses_response_to_canonical(controlled),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "response_max_output_tokens"
                && error.path == "max_output_tokens"
    ));
}

#[test]
fn responses_checked_stream_rejects_unknown_event_and_compatibility_is_explicit() {
    let snapshot: ResponsesStreamSnapshot = serde_json::from_str(
        r#"{"events":[{"Type":"future.event","Payload":{"type":"future.event","new_field":true}}],"usage":{}}"#,
    )
    .expect("unknown stream event retained");
    assert!(matches!(
        responses_stream_to_canonical_checked(&snapshot),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "unknown_event"
                && error.path == "events[0]"
                && error.loss_code.as_deref() == Some("LOSS_UNKNOWN_EVENT")
    ));
    assert!(matches!(
        responses_stream_to_canonical(&snapshot).as_slice(),
        [CanonicalStreamEvent::Error { code, .. }]
            if code.as_deref() == Some("conversion_unsupported_feature")
    ));
}

#[test]
fn responses_checked_stream_rejects_event_type_mismatch_and_missing_index() {
    let mismatch: ResponsesStreamSnapshot = serde_json::from_str(
        r#"{"events":[{"Type":"response.output_text.delta","Payload":{"type":"response.reasoning_text.delta","delta":"wrong"}}],"usage":{}}"#,
    )
    .expect("mismatched event retained");
    assert!(matches!(
        responses_stream_to_canonical_checked(&mismatch),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "event_type_mismatch"
                && error.path == "events[0].Type"
    ));

    let missing_index: ResponsesStreamSnapshot = serde_json::from_str(
        r#"{"events":[{"Type":"response.function_call_arguments.delta","Payload":{"type":"response.function_call_arguments.delta","delta":"{}"}}],"usage":{}}"#,
    )
    .expect("unindexed tool delta retained");
    assert!(matches!(
        responses_stream_to_canonical_checked(&missing_index),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "stream_index"
                && error.path == "events[0].output_index"
    ));
}

#[test]
fn responses_checked_stream_rejects_custom_and_builtin_output_items() {
    for (kind, feature) in [
        ("custom_tool_call", "custom_tool"),
        ("web_search_call", "builtin_web_search"),
    ] {
        let snapshot: ResponsesStreamSnapshot = serde_json::from_str(&format!(
            r#"{{"events":[{{"Type":"response.output_item.added","Payload":{{"type":"response.output_item.added","output_index":0,"item":{{"type":"{kind}"}}}}}}],"usage":{{}}}}"#
        ))
        .expect("provider output item retained");
        assert!(matches!(
            responses_stream_to_canonical_checked(&snapshot),
            Err(RelayConvertError::UnsupportedFeature(error))
                if error.feature == feature
                    && error.path == "events[0].item"
        ));
    }
}

#[test]
fn responses_checked_stream_rejects_custom_delta_with_custom_loss() {
    let snapshot: ResponsesStreamSnapshot = serde_json::from_str(
        r#"{"events":[{"Type":"response.custom_tool_call_input.delta","Payload":{"type":"response.custom_tool_call_input.delta","output_index":0,"delta":"{}"}}],"usage":{}}"#,
    )
    .expect("custom stream delta retained");
    assert!(matches!(
        responses_stream_to_canonical_checked(&snapshot),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "custom_tool"
                && error.path == "events[0]"
                && error.loss_code.as_deref() == Some("LOSS_CUSTOM_TOOL")
    ));
}

#[test]
fn responses_checked_stream_rejects_unrepresentable_output_item_id() {
    let snapshot: ResponsesStreamSnapshot = serde_json::from_str(
        r#"{"events":[{"Type":"response.output_item.added","Payload":{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_item","status":"in_progress","call_id":"call_1","name":"lookup","arguments":""}}}],"usage":{}}"#,
    )
    .expect("stream item id retained");
    assert!(matches!(
        responses_stream_to_canonical_checked(&snapshot),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "output_item_id"
                && error.path == "events[0].item.id"
    ));
}

#[test]
fn responses_checked_stream_accepts_and_checks_generated_item_identity() {
    let events = vec![
        CanonicalStreamEvent::ResponseStart {
            id: "resp-id".to_owned(),
            model: "gpt-test".to_owned(),
        },
        CanonicalStreamEvent::ContentStart {
            index: 0,
            kind: StreamContentKind::ToolCall,
        },
        CanonicalStreamEvent::ToolCallStart {
            index: 0,
            id: "call-id".to_owned(),
            name: "lookup".to_owned(),
        },
        CanonicalStreamEvent::ToolArgumentsDelta {
            index: 0,
            delta: "{}".to_owned(),
        },
        CanonicalStreamEvent::ContentEnd { index: 0 },
        CanonicalStreamEvent::ResponseEnd {
            finish_reason: FinishReason::ToolCalls,
            usage: None,
            model: None,
        },
    ];
    let snapshot = ResponsesStreamSnapshot {
        events: response_events_to_responses(&events),
        usage: WireUsage::default(),
        extra: BTreeMap::new(),
    };
    assert!(responses_stream_to_canonical_checked(&snapshot).is_ok());

    let mut done_snapshot = snapshot.clone();
    if let Some(event) = done_snapshot
        .events
        .iter_mut()
        .find(|event| event.kind == "response.function_call_arguments.done")
    {
        event.payload.arguments = Some("{}".to_owned());
    }
    assert!(responses_stream_to_canonical_checked(&done_snapshot).is_ok());

    let mut mismatched = snapshot.clone();
    if let Some(event) = mismatched
        .events
        .iter_mut()
        .find(|event| event.kind == "response.function_call_arguments.delta")
    {
        event.payload.item_id = Some("wrong-item".to_owned());
    }
    assert!(matches!(
        responses_stream_to_canonical_checked(&mismatched),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "stream_item_identity"
                && error.loss_code.as_deref() == Some("LOSS_UNKNOWN_EVENT")
    ));

    let mut late_id = snapshot.clone();
    for event in &mut late_id.events {
        if event.kind == "response.output_item.added"
            && let Some(item) = event.payload.item.as_mut()
        {
            item.id.clear();
        }
        event.payload.item_id = None;
    }
    assert!(matches!(
        responses_stream_to_canonical_checked(&late_id),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "output_item_id"
                && error.path.ends_with(".item.id")
                && error.loss_code.as_deref() == Some("LOSS_UNKNOWN_EVENT")
    ));

    let mut missing_sub_done = snapshot.clone();
    missing_sub_done
        .events
        .retain(|event| event.kind != "response.function_call_arguments.done");
    assert!(matches!(
        responses_stream_to_canonical_checked(&missing_sub_done),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "stream_item_lifecycle"
                && error.path.ends_with(".item")
                && error.loss_code.as_deref() == Some("LOSS_UNKNOWN_EVENT")
    ));

    let mut incomplete_item = snapshot.clone();
    if let Some(event) = incomplete_item
        .events
        .iter_mut()
        .find(|event| event.kind == "response.output_item.done")
        && let Some(item) = event.payload.item.as_mut()
    {
        item.status = "incomplete".to_owned();
    }
    assert!(matches!(
        responses_stream_to_canonical_checked(&incomplete_item),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "stream_item_status"
                && error.path.ends_with(".item.status")
                && error.loss_code.as_deref() == Some("LOSS_UNKNOWN_EVENT")
    ));

    let mut usage_conflict = snapshot;
    usage_conflict.usage.prompt_tokens = 1;
    if let Some(event) = usage_conflict
        .events
        .iter_mut()
        .find(|event| event.kind == "response.completed")
        && let Some(response) = event.payload.response.as_mut()
    {
        response.usage = Some(WireUsage {
            prompt_tokens: 2,
            ..WireUsage::default()
        });
    }
    assert!(matches!(
        responses_stream_to_canonical_checked(&usage_conflict),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "stream_usage_conflict"
                && error.loss_code.as_deref() == Some("LOSS_UNKNOWN_EVENT")
    ));
}

#[test]
fn responses_checked_stream_rejects_inapplicable_output_item_fields() {
    for (item, expected_path) in [
        (
            r#"{"type":"message","call_id":"call_1","content":[{"type":"output_text","text":"x"}]}"#,
            "events[0].item.call_id",
        ),
        (
            r#"{"type":"function_call","content":[{"type":"output_text","text":"x"}],"call_id":"call_1","name":"run","arguments":"{}"}"#,
            "events[0].item.content",
        ),
    ] {
        let item: serde_json::Value = serde_json::from_str(item).expect("output item JSON");
        let snapshot: ResponsesStreamSnapshot = serde_json::from_value(serde_json::json!({
            "events": [{
                "Type": "response.output_item.added",
                "Payload": {
                    "type": "response.output_item.added",
                    "output_index": 0,
                    "item": item,
                },
            }],
            "usage": {},
        }))
        .expect("stream output item field retained");
        assert!(matches!(
            responses_stream_to_canonical_checked(&snapshot),
            Err(RelayConvertError::UnsupportedFeature(error))
                if error.feature == "output_field" && error.path == expected_path
        ));
    }
}

#[test]
fn responses_checked_stream_rejects_terminal_full_output_snapshot() {
    let snapshot: ResponsesStreamSnapshot = serde_json::from_str(
        r#"{
          "events":[{"Type":"response.completed","Payload":{"type":"response.completed","response":{"id":"resp-full","object":"response","status":"completed","model":"gpt-test","output":[{"type":"message","content":[{"type":"output_text","text":"answer"}]}]}}}],
          "usage":{}
        }"#,
    )
    .expect("terminal response output retained");
    assert!(matches!(
        responses_stream_to_canonical_checked(&snapshot),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "stream_response_output"
                && error.path == "events[0].response.output"
                && error.loss_code.as_deref() == Some("LOSS_UNKNOWN_EVENT")
    ));
}

#[test]
fn responses_checked_stream_rejects_output_item_initial_payloads() {
    for (item, expected_path) in [
        (
            r#"{"type":"message","content":[{"type":"output_text","text":"seed"}]}"#,
            "events[0].item.content",
        ),
        (
            r#"{"type":"function_call","call_id":"call_1","name":"run","arguments":"{}"}"#,
            "events[0].item.arguments",
        ),
    ] {
        let item: serde_json::Value = serde_json::from_str(item).expect("output item JSON");
        let snapshot: ResponsesStreamSnapshot = serde_json::from_value(serde_json::json!({
            "events": [{
                "Type": "response.output_item.added",
                "Payload": {
                    "type": "response.output_item.added",
                    "output_index": 0,
                    "item": item,
                },
            }],
            "usage": {},
        }))
        .expect("initial output item payload retained");
        assert!(matches!(
            responses_stream_to_canonical_checked(&snapshot),
            Err(RelayConvertError::UnsupportedFeature(error))
                if error.path == expected_path
                    && matches!(
                        error.feature.as_str(),
                        "stream_item_content" | "stream_item_arguments"
                    )
        ));
    }
}

#[test]
fn responses_checked_stream_rejects_annotations_before_terminal_conversion() {
    let snapshot: ResponsesStreamSnapshot = serde_json::from_str(
        r#"{
          "events":[{"Type":"response.completed","Payload":{"type":"response.completed","response":{
            "id":"resp-1","object":"response","status":"completed","model":"gpt-test",
            "output":[{"type":"message","content":[{"type":"output_text","text":"answer","annotations":[{"type":"url_citation"}]}]}]
          }}}],
          "usage":{}
        }"#,
    )
    .expect("annotated response stream retained");
    assert!(matches!(
        responses_stream_to_canonical_checked(&snapshot),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "citations"
                && error.path == "events[0].response.output[0].content[0].annotations"
                && error.loss_code.as_deref() == Some("LOSS_CITATION")
    ));
}

#[test]
fn responses_checked_stream_rejects_duplicate_terminal_events() {
    let snapshot: ResponsesStreamSnapshot = serde_json::from_str(
        r#"{
          "events":[
            {"Type":"response.created","Payload":{"type":"response.created","response":{"id":"resp-1","object":"response","status":"in_progress","model":"gpt-test","output":[]}}},
            {"Type":"response.completed","Payload":{"type":"response.completed","response":{"id":"resp-1","object":"response","status":"completed","model":"gpt-test","output":[]}}},
            {"Type":"response.done","Payload":{"type":"response.done","response":{"id":"resp-1","object":"response","status":"completed","model":"gpt-test","output":[]}}}
          ],
          "usage":{}
        }"#,
    )
    .expect("duplicate terminal stream retained");
    assert!(matches!(
        responses_stream_to_canonical_checked(&snapshot),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "stream_termination"
                && error.path == "events[2]"
    ));
}

#[test]
fn responses_checked_stream_rejects_terminal_item_snapshot_in_cross_conversion() {
    let snapshot: ResponsesStreamSnapshot = serde_json::from_str(
        r#"{
          "events":[{"Type":"response.output_item.done","Payload":{"type":"response.output_item.done","output_index":0,"item":{"type":"message","status":"completed"}}}],
          "usage":{}
        }"#,
    )
    .expect("terminal item snapshot retained");
    assert!(matches!(
        responses_stream_to_canonical_checked(&snapshot),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "stream_terminal_item"
                && error.path == "events[0].item"
                && error.loss_code.as_deref() == Some("LOSS_UNKNOWN_EVENT")
    ));
}

#[test]
fn responses_checked_stream_rejects_duplicate_error_sources() {
    let snapshot: ResponsesStreamSnapshot = serde_json::from_str(
        r#"{
          "events":[{"Type":"response.error","Payload":{"type":"response.error","error":{"code":"outer","message":"outer"},"response":{"id":"resp-error","object":"response","status":"failed","model":"gpt-test","error":{"code":"inner","message":"inner"}}}}],
          "usage":{}
        }"#,
    )
    .expect("duplicate error sources retained");
    assert!(matches!(
        responses_stream_to_canonical_checked(&snapshot),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "duplicate_error"
                && error.path == "events[0].response.error"
                && error.loss_code.as_deref() == Some("LOSS_UNKNOWN_EVENT")
    ));
}

#[test]
fn responses_checked_stream_rejects_present_default_response_controls() {
    for (field, value) in [("parallel_tool_calls", "false"), ("store", "false")] {
        let mut response = serde_json::Map::new();
        response.insert("id".to_owned(), serde_json::json!("resp-control"));
        response.insert("object".to_owned(), serde_json::json!("response"));
        response.insert("status".to_owned(), serde_json::json!("completed"));
        response.insert("model".to_owned(), serde_json::json!("gpt-test"));
        response.insert(
            field.to_owned(),
            serde_json::from_str::<serde_json::Value>(value).expect("response control JSON"),
        );
        let snapshot: ResponsesStreamSnapshot = serde_json::from_value(serde_json::json!({
            "events": [{
                "Type": "response.completed",
                "Payload": {
                    "type": "response.completed",
                    "response": serde_json::Value::Object(response),
                },
            }],
            "usage": {},
        }))
        .expect("stream response control retained");
        assert!(matches!(
            responses_stream_to_canonical_checked(&snapshot),
            Err(RelayConvertError::UnsupportedFeature(error))
                if error.path == format!("events[0].response.{field}")
        ));
    }
}

#[test]
fn responses_checked_stream_rejects_incomplete_details_on_completed_response() {
    let snapshot: ResponsesStreamSnapshot = serde_json::from_str(
        r#"{"events":[{"Type":"response.completed","Payload":{"type":"response.completed","response":{"id":"resp-details","object":"response","status":"completed","model":"gpt-test","incomplete_details":{"reason":"max_output_tokens"}}}}],"usage":{}}"#,
    )
    .expect("stream incomplete details retained");
    assert!(matches!(
        responses_stream_to_canonical_checked(&snapshot),
        Err(RelayConvertError::UnsupportedFeature(error))
            if error.feature == "incomplete_details"
                && error.path == "events[0].response.incomplete_details"
    ));
}

#[test]
fn canonical_response_target_losses_are_never_silent() {
    let canonical = CanonicalResponse {
        id: "resp_test".to_owned(),
        model: "gpt-test".to_owned(),
        created_at: 123,
        output: vec![
            CanonicalContent::Image {
                url: "https://example.test/image.png".to_owned(),
                detail: Some("high".to_owned()),
            },
            CanonicalContent::ToolResult {
                id: "call_1".to_owned(),
                name: None,
                output: JsonData::String("done".to_owned()),
            },
        ],
        finish_reason: Some(FinishReason::Stop),
        usage: None,
    };
    for loss in [
        canonical_response_to_openai_chat(canonical.clone()).loss,
        canonical_response_to_openai_responses(canonical).loss,
    ] {
        assert_eq!(
            loss.dropped_fields,
            ["output[].image", "output[].tool_result"]
        );
    }
}

#[test]
fn gemini_single_function_call_id_is_retained_in_openai_chat() {
    let gemini: GeminiRequest = serde_json::from_str(
        r#"{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call-42","name":"lookup","args":{"q":"rust"}}}]}]}"#,
    )
    .expect("Gemini request");
    let converted = gemini_request_to_openai_chat(gemini).expect("Gemini to Chat");
    let id = converted.value.messages[0].tool_calls[0].id.clone();
    assert_eq!(id, "call-42");
}

#[test]
fn gemini_parallel_same_name_calls_keep_distinct_ids() {
    let gemini: GeminiRequest = serde_json::from_str(
        r#"{"contents":[{"role":"model","parts":[{"functionCall":{"id":"a","name":"lookup","args":{}}},{"functionCall":{"id":"b","name":"lookup","args":{}}}]}]}"#,
    )
    .expect("Gemini request");
    let converted = gemini_request_to_openai_chat(gemini).expect("Gemini to Chat");
    let ids = converted.value.messages[0]
        .tool_calls
        .iter()
        .map(|call| call.id.as_str())
        .collect::<Vec<_>>();
    assert_eq!(ids, ["a", "b"]);
}

#[test]
fn gemini_model_function_calls_become_assistant_and_results_become_tool() {
    let gemini: GeminiRequest = serde_json::from_str(
        r#"{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call","name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"call","name":"lookup","response":{"ok":true}}}]}]}"#,
    )
    .expect("Gemini request");
    let converted = gemini_request_to_openai_chat(gemini).expect("Gemini to Chat");
    assert_eq!(converted.value.messages[0].role, "assistant");
    assert_eq!(converted.value.messages[0].tool_calls[0].id, "call");
    assert_eq!(converted.value.messages[1].role, "tool");
    assert_eq!(
        converted.value.messages[1].tool_call_id.as_deref(),
        Some("call")
    );
}

#[test]
fn gemini_missing_ids_use_global_stable_queue_and_mark_same_name_ambiguity() {
    let gemini: GeminiRequest = serde_json::from_str(
        r#"{"contents":[{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{}}},{"functionCall":{"name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"n":1}}},{"functionResponse":{"name":"lookup","response":{"n":2}}}]}]}"#,
    )
    .expect("Gemini request");
    let converted = gemini_request_to_canonical_for_model(gemini, "gemini-2.5-pro")
        .expect("Gemini to canonical");
    assert_eq!(
        converted.value.messages[0].parts[0],
        CanonicalContent::ToolCall {
            id: "gemini_call_synthetic_0_lookup".to_owned(),
            name: "lookup".to_owned(),
            arguments: "{}".to_owned(),
        }
    );
    assert_eq!(
        converted.value.messages[0].parts[1],
        CanonicalContent::ToolCall {
            id: "gemini_call_synthetic_1_lookup".to_owned(),
            name: "lookup".to_owned(),
            arguments: "{}".to_owned(),
        }
    );
    assert_eq!(
        converted.value.messages[1].parts[0],
        CanonicalContent::ToolResult {
            id: "gemini_call_synthetic_0_lookup".to_owned(),
            name: Some("lookup".to_owned()),
            output: JsonData::Object(
                [(
                    "n".to_owned(),
                    JsonData::Number(serde_json::Number::from(1))
                )]
                .into_iter()
                .collect(),
            ),
        }
    );
    assert!(
        converted
            .loss
            .synthetic_fields
            .contains(&"SYNTHETIC_TOOL_RESULT_ID_AMBIGUOUS")
    );
}

#[test]
fn gemini_part_with_text_and_function_call_is_rejected_instead_of_reordered() {
    let gemini: GeminiRequest = serde_json::from_str(
        r#"{"contents":[{"role":"model","parts":[{"text":"prefix","functionCall":{"id":"call","name":"lookup","args":{}}}]}]}"#,
    )
    .expect("Gemini request");
    assert!(gemini_request_to_canonical(gemini).is_err());
}

#[test]
fn gemini_unordered_function_results_match_by_id() {
    let gemini: GeminiRequest = serde_json::from_str(
        r#"{"contents":[{"role":"model","parts":[{"functionCall":{"id":"a","name":"lookup","args":{}}},{"functionCall":{"id":"b","name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"b","name":"lookup","response":{"value":2}}},{"functionResponse":{"id":"a","name":"lookup","response":{"value":1}}}]}]}"#,
    )
    .expect("Gemini request");
    let converted = gemini_request_to_openai_chat(gemini).expect("Gemini to Chat");
    let results = converted
        .value
        .messages
        .iter()
        .filter_map(|message| message.tool_call_id.as_deref())
        .collect::<Vec<_>>();
    assert_eq!(results, ["b", "a"]);
}

#[test]
fn gemini_explicit_result_orphan_and_name_mismatch_are_rejected() {
    let orphan: GeminiRequest = serde_json::from_str(
        r#"{"contents":[{"role":"user","parts":[{"functionResponse":{"id":"missing","name":"lookup","response":{}}}]}]}"#,
    )
    .expect("Gemini orphan result");
    assert!(gemini_request_to_canonical(orphan).is_err());

    let mismatch: GeminiRequest = serde_json::from_str(
        r#"{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call","name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"call","name":"other","response":{}}}]}]}"#,
    )
    .expect("Gemini mismatched result");
    assert!(gemini_request_to_canonical(mismatch).is_err());
}

#[test]
fn gemini_sequential_tool_steps_keep_each_signature() {
    let gemini: GeminiRequest = serde_json::from_str(
        r#"{"contents":[{"role":"model","parts":[{"functionCall":{"id":"step-1","name":"one","args":{}},"thoughtSignature":"sig-1"}]},{"role":"user","parts":[{"functionResponse":{"id":"step-1","name":"one","response":{}}}]},{"role":"model","parts":[{"functionCall":{"id":"step-2","name":"two","args":{}},"thoughtSignature":"sig-2"}]}]}"#,
    )
    .expect("Gemini request");
    let converted = gemini_request_to_openai_chat(gemini).expect("Gemini to Chat");
    let signatures = converted
        .value
        .messages
        .iter()
        .flat_map(|message| message.tool_calls.iter())
        .filter_map(|call| call.extra_content.as_ref())
        .filter_map(|extra| extra.google.as_ref())
        .filter_map(|google| google.thought_signature.as_deref())
        .collect::<Vec<_>>();
    assert_eq!(signatures, ["sig-1", "sig-2"]);
}

#[test]
fn authentic_gemini_signature_round_trips_without_rewriting() {
    let source: GeminiRequest = serde_json::from_str(
        r#"{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call","name":"lookup","args":{}},"thoughtSignature":"opaque+/=token"}]}]}"#,
    )
    .expect("Gemini request");
    let chat = gemini_request_to_openai_chat(source).expect("Gemini to Chat");
    let roundtrip = canonical_request_to_gemini(
        openai_chat_request_to_canonical(chat.value)
            .expect("Chat to canonical")
            .value,
    )
    .expect("canonical to Gemini");
    assert_eq!(
        roundtrip.value.contents[0].parts[0]
            .thought_signature
            .as_deref(),
        Some("opaque+/=token")
    );
}

#[test]
fn openai_synthetic_history_gets_documented_gemini_dummy_on_2_5() {
    let request: OpenAiChatRequest = serde_json::from_str(
        r#"{"model":"openai-source","messages":[{"role":"assistant","tool_calls":[{"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"}}]}]}"#,
    )
    .expect("Chat request");
    let converted = openai_chat_request_to_gemini_for_model(request, "gemini-2.5-pro")
        .expect("OpenAI to Gemini");
    assert_eq!(
        converted.value.contents[0].parts[0]
            .thought_signature
            .as_deref(),
        Some("context_engineering_is_the_way_to_go")
    );
    assert!(
        converted
            .loss
            .synthetic_fields
            .contains(&"SYNTHETIC_THOUGHT_SIGNATURE")
    );
}

#[test]
fn raw_gemini_dummy_literal_is_authentic_until_explicitly_marked_synthetic() {
    let gemini: GeminiRequest = serde_json::from_str(
        r#"{"contents":[{"role":"model","parts":[{"text":"","thoughtSignature":"context_engineering_is_the_way_to_go"}]}]}"#,
    )
    .expect("Gemini request");
    let canonical = gemini_request_to_canonical(gemini).expect("Gemini conversion");
    assert!(matches!(
        canonical.value.messages[0].parts[1],
        CanonicalContent::ProviderState {
            state: OpaqueProviderState {
                provenance: OpaqueStateProvenance::Authentic,
                ..
            }
        }
    ));
}

#[test]
fn parallel_gemini_history_only_gets_one_first_call_dummy() {
    let request: OpenAiChatRequest = serde_json::from_str(
        r#"{"model":"openai-source","messages":[{"role":"assistant","tool_calls":[{"id":"a","type":"function","function":{"name":"lookup","arguments":"{}"}},{"id":"b","type":"function","function":{"name":"lookup","arguments":"{}"}}]}]}"#,
    )
    .expect("Chat request");
    let converted = openai_chat_request_to_gemini_for_model(request, "gemini-2.5-pro")
        .expect("OpenAI to Gemini");
    assert_eq!(
        converted.value.contents[0].parts[0]
            .thought_signature
            .as_deref(),
        Some("context_engineering_is_the_way_to_go")
    );
    assert_eq!(converted.value.contents[0].parts[1].thought_signature, None);
    assert_eq!(
        converted
            .loss
            .synthetic_fields
            .iter()
            .filter(|field| **field == "SYNTHETIC_THOUGHT_SIGNATURE")
            .count(),
        1
    );
}

#[test]
fn canonical_gemini_rejects_duplicate_and_parallel_second_signatures() {
    let duplicate_state = CanonicalRequest {
        model: "gemini-2.5-pro".to_owned(),
        instructions: Vec::new(),
        messages: vec![CanonicalMessage {
            role: Role::Assistant,
            parts: vec![
                CanonicalContent::ToolCall {
                    id: "call".to_owned(),
                    name: "lookup".to_owned(),
                    arguments: "{}".to_owned(),
                },
                CanonicalContent::ProviderState {
                    state: OpaqueProviderState::authentic_gemini_thought_signature(
                        "first".to_owned(),
                        Some("gemini-2.5-pro".to_owned()),
                    ),
                },
                CanonicalContent::ProviderState {
                    state: OpaqueProviderState::authentic_gemini_thought_signature(
                        "second".to_owned(),
                        Some("gemini-2.5-pro".to_owned()),
                    ),
                },
            ],
        }],
        max_output_tokens: None,
        temperature: None,
        stream: false,
        tools: Vec::new(),
        tool_choice: None,
        options: RequestOptions::default(),
    };
    assert!(
        canonical_request_to_gemini_for_model(duplicate_state, "gemini-2.5-pro", false).is_err()
    );

    let parallel_second_signature = CanonicalRequest {
        model: "gemini-2.5-pro".to_owned(),
        instructions: Vec::new(),
        messages: vec![CanonicalMessage {
            role: Role::Assistant,
            parts: vec![
                CanonicalContent::ToolCall {
                    id: "first".to_owned(),
                    name: "lookup".to_owned(),
                    arguments: "{}".to_owned(),
                },
                CanonicalContent::ProviderState {
                    state: OpaqueProviderState::authentic_gemini_thought_signature(
                        "first-signature".to_owned(),
                        Some("gemini-2.5-pro".to_owned()),
                    ),
                },
                CanonicalContent::ToolCall {
                    id: "second".to_owned(),
                    name: "lookup".to_owned(),
                    arguments: "{}".to_owned(),
                },
                CanonicalContent::ProviderState {
                    state: OpaqueProviderState::authentic_gemini_thought_signature(
                        "second-signature".to_owned(),
                        Some("gemini-2.5-pro".to_owned()),
                    ),
                },
            ],
        }],
        max_output_tokens: None,
        temperature: None,
        stream: false,
        tools: Vec::new(),
        tool_choice: None,
        options: RequestOptions::default(),
    };
    assert!(
        canonical_request_to_gemini_for_model(parallel_second_signature, "gemini-2.5-pro", false)
            .is_err()
    );
}

#[test]
fn authentic_signature_is_not_overwritten_by_2_5_dummy() {
    let request: OpenAiChatRequest = serde_json::from_str(
        r#"{"model":"openai-source","messages":[{"role":"assistant","tool_calls":[{"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"},"extra_content":{"google":{"thought_signature":"authentic"}}}]}]}"#,
    )
    .expect("Chat request");
    let converted = openai_chat_request_to_gemini_for_model(request, "gemini-2.5-pro")
        .expect("OpenAI to Gemini");
    assert_eq!(
        converted.value.contents[0].parts[0]
            .thought_signature
            .as_deref(),
        Some("authentic")
    );
}

#[test]
fn empty_text_part_keeps_its_gemini_signature() {
    let gemini: GeminiRequest = serde_json::from_str(
        r#"{"contents":[{"role":"model","parts":[{"text":"","thoughtSignature":"empty-part"}]}]}"#,
    )
    .expect("Gemini request");
    let chat = gemini_request_to_openai_chat(gemini).expect("Gemini to Chat");
    assert_eq!(
        chat.value.messages[0]
            .extra_content
            .as_ref()
            .and_then(|extra| extra.google.as_ref())
            .and_then(|google| google.thought_signature.as_deref()),
        Some("empty-part")
    );
}

#[test]
fn response_signature_received_before_finish_reason_is_preserved() {
    let gemini: GeminiResponse = serde_json::from_str(
        r#"{"candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"text":"","thoughtSignature":"late"}]}}]}"#,
    )
    .expect("Gemini response");
    let chat = gemini_response_to_openai_chat(gemini).expect("Gemini response to Chat");
    assert_eq!(
        chat.value.choices[0]
            .message
            .extra_content
            .as_ref()
            .and_then(|extra| extra.google.as_ref())
            .and_then(|google| google.thought_signature.as_deref()),
        Some("late")
    );
}

#[test]
fn gemini_stream_signature_is_retained_before_later_finish_reason() {
    let snapshot: GeminiStreamSnapshot = serde_json::from_str(
        r#"{
          "events":[
            {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call","name":"lookup","args":{}},"thoughtSignature":"late"}]}}]},
            {"candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[]}}]}
          ],
          "usage":{}
        }"#,
    )
    .expect("Gemini stream");
    let converted =
        gemini_stream_to_canonical(&snapshot, "gemini-3-pro").expect("Gemini stream conversion");
    assert_eq!(converted.value.finish_reason, Some(FinishReason::Stop));
    assert!(matches!(
        converted.value.output.as_slice(),
        [
            CanonicalContent::ToolCall { id, .. },
            CanonicalContent::ProviderState {
                state: OpaqueProviderState {
                    raw: JsonData::String(signature),
                    provenance: OpaqueStateProvenance::Authentic,
                    ..
                }
            }
        ] if id == "call" && signature == "late"
    ));
}

#[test]
fn gemini_stream_unknown_fields_are_typed_rejections() {
    let cases = [
        (
            serde_json::json!({
                "events": [],
                "usage": {},
                "futureSnapshotField": true
            }),
            "snapshot.futureSnapshotField",
        ),
        (
            serde_json::json!({
                "events": [],
                "usage": {"futureUsageField": true}
            }),
            "snapshot.usage.futureUsageField",
        ),
        (
            serde_json::json!({
                "events": [{"futureEventField": true}],
                "usage": {}
            }),
            "events[0].futureEventField",
        ),
        (
            serde_json::json!({
                "events": [{"usageMetadata": {"futureUsageMetadataField": true}}],
                "usage": {}
            }),
            "events[0].usageMetadata.futureUsageMetadataField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "candidates": [{"content": {}, "futureCandidateField": true}]
                }],
                "usage": {}
            }),
            "events[0].candidates[0].futureCandidateField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "candidates": [{
                        "content": {"futureContentField": true}
                    }]
                }],
                "usage": {}
            }),
            "events[0].candidates[0].content.futureContentField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "candidates": [{
                        "content": {"parts": [{"futurePartField": true}]}
                    }]
                }],
                "usage": {}
            }),
            "events[0].candidates[0].content.parts[0].futurePartField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "candidates": [{
                        "content": {
                            "parts": [{
                                "inlineData": {
                                    "mimeType": "text/plain",
                                    "data": "a",
                                    "futureInlineField": true
                                }
                            }]
                        }
                    }]
                }],
                "usage": {}
            }),
            "events[0].candidates[0].content.parts[0].inlineData.futureInlineField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "candidates": [{
                        "content": {
                            "parts": [{
                                "functionCall": {
                                    "name": "lookup",
                                    "args": {},
                                    "futureFunctionField": true
                                }
                            }]
                        }
                    }]
                }],
                "usage": {}
            }),
            "events[0].candidates[0].content.parts[0].functionCall.futureFunctionField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "candidates": [{
                        "content": {
                            "parts": [{
                                "functionResponse": {
                                    "name": "lookup",
                                    "response": {},
                                    "futureResponseField": true
                                }
                            }]
                        }
                    }]
                }],
                "usage": {}
            }),
            "events[0].candidates[0].content.parts[0].functionResponse.futureResponseField",
        ),
    ];

    for (document, expected_path) in cases {
        let snapshot: GeminiStreamSnapshot = serde_json::from_value(document)
            .expect("unknown Gemini stream field is retained by the wire DTO");
        let error = gemini_stream_to_canonical(&snapshot, "gemini-2.5-pro")
            .expect_err("Gemini stream conversion must reject unknown fields");
        assert!(matches!(
            error,
            RelayConvertError::UnsupportedFeature(detail)
                if detail.feature == "unknown_field"
                    && detail.path == expected_path
                    && detail.source_format == "gemini"
                    && detail.target_format == "provider_neutral_ir"
        ));
    }
}

#[test]
fn gemini_2_5_legacy_call_without_signature_is_accepted() {
    let request = CanonicalRequest {
        model: "gemini-2.5-pro".to_owned(),
        instructions: Vec::new(),
        messages: vec![CanonicalMessage {
            role: Role::Assistant,
            parts: vec![CanonicalContent::ToolCall {
                id: "call".to_owned(),
                name: "lookup".to_owned(),
                arguments: "{}".to_owned(),
            }],
        }],
        max_output_tokens: None,
        temperature: None,
        stream: false,
        tools: Vec::new(),
        tool_choice: None,
        options: RequestOptions::default(),
    };
    assert!(canonical_request_to_gemini_for_model(request, "gemini-2.5-pro", false).is_ok());
}

#[test]
fn gemini_source_history_does_not_receive_a_synthetic_dummy_signature() {
    let request: GeminiRequest = serde_json::from_str(
        r#"{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call","name":"lookup","args":{}}}]}]}"#,
    )
    .expect("Gemini source request");
    let decoded = gemini_request_to_envelope_v2(request, "gemini-2.5-pro")
        .expect("decode Gemini source history");
    let encoded =
        envelope_to_gemini_request_v2(decoded.envelope).expect("encode Gemini source history");

    assert_eq!(encoded.value.contents[0].parts[0].thought_signature, None);
    assert!(
        !encoded
            .loss_ledger()
            .as_slice()
            .iter()
            .any(|loss| loss.code == LossCode::SyntheticThoughtSignature)
    );
}

#[test]
fn gemini_3_missing_signature_is_rejected_before_upstream() {
    let request: OpenAiChatRequest = serde_json::from_str(
        r#"{"model":"openai-source","messages":[{"role":"assistant","tool_calls":[{"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"}}]}]}"#,
    )
    .expect("Chat request");
    let error = openai_chat_request_to_gemini_for_model(request, "gemini-3-pro")
        .expect_err("Gemini 3 must reject missing signature");
    assert!(error.to_string().contains("missing thoughtSignature"));
}

#[test]
fn openai_gemini_openai_round_trip_keeps_call_id() {
    let request: OpenAiChatRequest = serde_json::from_str(
        r#"{"model":"openai-source","messages":[{"role":"assistant","tool_calls":[{"id":"stable-id","type":"function","function":{"name":"lookup","arguments":"{}"}}]}]}"#,
    )
    .expect("Chat request");
    let gemini = openai_chat_request_to_gemini_for_model(request, "gemini-2.5-pro")
        .expect("OpenAI to Gemini");
    let chat = gemini_request_to_openai_chat(gemini.value).expect("Gemini to Chat");
    assert_eq!(chat.value.messages[0].tool_calls[0].id, "stable-id");
}

#[test]
fn openai_google_extension_round_trips_authentic_and_synthetic_provenance() {
    let request: OpenAiChatRequest = serde_json::from_str(
        r#"{"model":"source","messages":[{"role":"assistant","tool_calls":[{"id":"call","type":"function","function":{"name":"lookup","arguments":"{}"},"extra_content":{"google":{"thought_signature":"synthetic-token","synthetic":true}}}]}]}"#,
    )
    .expect("Chat request");
    let canonical = openai_chat_request_to_canonical(request)
        .expect("Chat to canonical")
        .value;
    assert!(matches!(
        canonical.messages[0].parts.get(1),
        Some(CanonicalContent::ProviderState {
            state: OpaqueProviderState {
                provenance: OpaqueStateProvenance::Synthetic,
                ..
            }
        })
    ));
    let chat = canonical_request_to_openai_chat(canonical)
        .expect("canonical to Chat")
        .value;
    let google = chat.messages[0].tool_calls[0]
        .extra_content
        .as_ref()
        .and_then(|extra| extra.google.as_ref())
        .expect("Google extension");
    assert_eq!(google.thought_signature.as_deref(), Some("synthetic-token"));
    assert_eq!(google.synthetic, Some(true));
}

#[test]
fn claude_thinking_and_redacted_blocks_typed_raw_round_trip_including_empty_thinking() {
    let response: ClaudeResponse = serde_json::from_str(
        r#"{
          "id":"msg_cla",
          "type":"message",
          "role":"assistant",
          "model":"claude-sonnet",
          "content":[
            {"type":"thinking","thinking":"","signature":"sig-empty"},
            {"type":"redacted_thinking","data":"opaque-redacted"}
          ],
          "stop_reason":"end_turn"
        }"#,
    )
    .expect("Claude response");
    assert_eq!(response.content[0].thinking.as_deref(), Some(""));
    let raw = serde_json::to_string(&response).expect("Claude response JSON");
    let reparsed: ClaudeResponse = serde_json::from_str(&raw).expect("Claude response JSON");
    assert_eq!(reparsed, response);

    let canonical = claude_response_to_canonical(response)
        .expect("Claude response conversion")
        .value;
    assert!(matches!(
        canonical.output.as_slice(),
        [
            CanonicalContent::ClaudeThinking {
                thinking,
                signature: Some(signature),
                provenance: OpaqueStateProvenance::Authentic,
                ..
            },
            CanonicalContent::RedactedThinking {
                data: JsonData::String(data),
                provenance: OpaqueStateProvenance::Authentic,
                ..
            }
        ] if thinking.is_empty() && signature == "sig-empty" && data == "opaque-redacted"
    ));
}

#[test]
fn claude_known_block_with_multiple_payloads_is_rejected() {
    let response: ClaudeResponse = serde_json::from_str(
        r#"{
          "id":"msg-invalid",
          "type":"message",
          "role":"assistant",
          "model":"claude-sonnet",
          "content":[{"type":"thinking","thinking":"plan","signature":"sig","data":"wrong"}],
          "stop_reason":"end_turn"
        }"#,
    )
    .expect("Claude response");
    assert!(claude_response_to_canonical(response).is_err());

    let chat: OpenAiChatResponse = serde_json::from_str(
        r#"{
          "id":"chat-invalid",
          "model":"claude-sonnet",
          "object":"chat.completion",
          "created":0,
          "choices":[{"index":0,"message":{"role":"assistant","content":null,"extra_content":{"anthropic":{"blocks":[{"type":"thinking","thinking":"plan","signature":"sig","id":"wrong"}]}}},"finish_reason":"stop"}]
        }"#,
    )
    .expect("OpenAI extension response");
    assert!(openai_chat_response_to_canonical(chat).is_err());
}

#[test]
fn claude_openai_extension_preserves_all_ordered_blocks_without_duplicates() {
    let response: ClaudeResponse = serde_json::from_str(
        r#"{
          "id":"msg_order",
          "type":"message",
          "role":"assistant",
          "model":"claude-sonnet",
          "content":[
            {"type":"text","text":"before"},
            {"type":"thinking","thinking":"plan","signature":"sig"},
            {"type":"text","text":"after"},
            {"type":"tool_use","id":"same-name-a","name":"lookup","input":{"n":1}},
            {"type":"tool_use","id":"same-name-b","name":"lookup","input":{"n":2}},
            {"type":"redacted_thinking","data":{"cipher":"opaque"}}
          ],
          "stop_reason":"tool_use"
        }"#,
    )
    .expect("Claude response");
    let chat = claude_response_to_openai_chat(response).expect("Claude to Chat");
    let blocks = chat.value.choices[0]
        .message
        .extra_content
        .as_ref()
        .and_then(|extra| extra.anthropic.as_ref())
        .expect("Anthropic extension")
        .blocks
        .clone();
    let kinds = blocks
        .iter()
        .map(|block| block.kind.as_str())
        .collect::<Vec<_>>();
    assert_eq!(
        kinds,
        vec![
            "text",
            "thinking",
            "text",
            "tool_use",
            "tool_use",
            "redacted_thinking"
        ]
    );
    assert_eq!(
        blocks
            .iter()
            .filter(|block| block.kind == "thinking" || block.kind == "redacted_thinking")
            .count(),
        2
    );
    assert_eq!(blocks[3].id.as_deref(), Some("same-name-a"));
    assert_eq!(blocks[4].id.as_deref(), Some("same-name-b"));

    let round_trip = openai_chat_response_to_claude(chat.value)
        .expect("Chat to Claude")
        .value;
    let round_trip_kinds = round_trip
        .content
        .iter()
        .map(|block| block.kind.as_str())
        .collect::<Vec<_>>();
    assert_eq!(round_trip_kinds, kinds);
    assert_eq!(round_trip.content[3].id.as_deref(), Some("same-name-a"));
    assert_eq!(round_trip.content[4].id.as_deref(), Some("same-name-b"));
}

#[test]
fn claude_request_tool_round_uses_ordered_extension_and_preserves_tool_result() {
    let request: ClaudeRequest = serde_json::from_str(
        r#"{
          "model":"claude-sonnet",
          "max_tokens":256,
          "messages":[
            {"role":"assistant","content":[
              {"type":"thinking","thinking":"plan","signature":"sig"},
              {"type":"tool_use","id":"call-a","name":"lookup","input":{}}
            ]},
            {"role":"user","content":[
              {"type":"tool_result","tool_use_id":"call-a","content":"ok"}
            ]}
          ]
        }"#,
    )
    .expect("Claude request");
    let chat = claude_request_to_openai_chat(request).expect("Claude to Chat");
    let assistant = chat
        .value
        .messages
        .iter()
        .find(|message| message.role == "assistant")
        .expect("assistant message");
    let blocks = assistant
        .extra_content
        .as_ref()
        .and_then(|extra| extra.anthropic.as_ref())
        .expect("Anthropic extension")
        .blocks
        .clone();
    assert_eq!(blocks.len(), 2);
    assert_eq!(blocks[0].kind, "thinking");
    assert_eq!(blocks[1].kind, "tool_use");
    assert_eq!(blocks[1].id.as_deref(), Some("call-a"));

    let tool = chat
        .value
        .messages
        .iter()
        .find(|message| message.role == "tool")
        .expect("tool message");
    assert_eq!(tool.tool_call_id.as_deref(), Some("call-a"));
    assert_eq!(
        tool.extra_content
            .as_ref()
            .and_then(|extra| extra.anthropic.as_ref())
            .map(|extra| extra.blocks[0].kind.as_str()),
        Some("tool_result")
    );
}

#[test]
fn claude_request_multiple_tool_results_keep_native_order_without_extension_duplicates() {
    let request: ClaudeRequest = serde_json::from_str(
        r#"{
          "model":"claude-sonnet",
          "max_tokens":256,
          "messages":[{"role":"user","content":[
            {"type":"tool_result","tool_use_id":"call-a","content":"first"},
            {"type":"tool_result","tool_use_id":"call-b","content":"second"}
          ]}]
        }"#,
    )
    .expect("Claude request");
    let chat = claude_request_to_openai_chat(request).expect("Claude to Chat");
    let tool_messages = chat
        .value
        .messages
        .iter()
        .filter(|message| message.role == "tool")
        .collect::<Vec<_>>();
    assert_eq!(tool_messages.len(), 2);
    assert_eq!(
        tool_messages
            .iter()
            .filter_map(|message| message.tool_call_id.as_deref())
            .collect::<Vec<_>>(),
        vec!["call-a", "call-b"]
    );
    let ordered_extension = tool_messages[0]
        .extra_content
        .as_ref()
        .and_then(|extra| extra.anthropic.as_ref())
        .expect("ordered Anthropic extension");
    assert_eq!(
        ordered_extension
            .blocks
            .iter()
            .filter_map(|block| block.tool_use_id.as_deref())
            .collect::<Vec<_>>(),
        vec!["call-a", "call-b"]
    );
    assert!(tool_messages[1].extra_content.is_none());

    let round_trip = openai_chat_request_to_claude(chat.value)
        .expect("Chat to Claude")
        .value;
    let ids = round_trip
        .messages
        .into_iter()
        .flat_map(|message| match message.content {
            StringOrParts::Parts(parts) => parts,
            StringOrParts::String(_) => Vec::new(),
        })
        .filter_map(|block| block.tool_use_id)
        .collect::<Vec<_>>();
    assert_eq!(ids, vec!["call-a".to_owned(), "call-b".to_owned()]);
}

#[test]
fn claude_mixed_thinking_and_tool_result_stays_in_one_ordered_extension() {
    let request: ClaudeRequest = serde_json::from_str(
        r#"{
          "model":"claude-sonnet",
          "max_tokens":256,
          "messages":[{"role":"user","content":[
            {"type":"thinking","thinking":"context","signature":"sig"},
            {"type":"tool_result","tool_use_id":"call-a","content":"done"}
          ]}]
        }"#,
    )
    .expect("Claude request");
    let chat = claude_request_to_openai_chat(request).expect("Claude to Chat");
    let tool = chat
        .value
        .messages
        .iter()
        .find(|message| message.role == "tool")
        .expect("tool message");
    let blocks = tool
        .extra_content
        .as_ref()
        .and_then(|extra| extra.anthropic.as_ref())
        .expect("ordered Anthropic extension")
        .blocks
        .iter()
        .map(|block| block.kind.as_str())
        .collect::<Vec<_>>();
    assert_eq!(blocks, vec!["thinking", "tool_result"]);
    let round_trip = openai_chat_request_to_claude(chat.value)
        .expect("Chat to Claude")
        .value;
    let kinds = round_trip
        .messages
        .into_iter()
        .flat_map(|message| match message.content {
            StringOrParts::Parts(parts) => parts,
            StringOrParts::String(_) => Vec::new(),
        })
        .map(|block| block.kind)
        .collect::<Vec<_>>();
    assert_eq!(kinds, vec!["thinking".to_owned(), "tool_result".to_owned()]);
}

#[test]
fn ordinary_openai_reasoning_is_not_encoded_as_claude_thinking() {
    let response: OpenAiChatResponse = serde_json::from_str(
        r#"{
          "id":"chat-reasoning",
          "model":"openai-model",
          "object":"chat.completion",
          "created":1,
          "choices":[{"index":0,"message":{"role":"assistant","content":"answer","reasoning_content":"summary"},"finish_reason":"stop"}]
        }"#,
    )
    .expect("OpenAI response");
    let canonical = openai_chat_response_to_canonical(response)
        .expect("Chat conversion")
        .value;
    assert!(
        canonical
            .output
            .iter()
            .any(|part| matches!(part, CanonicalContent::Reasoning { text } if text == "summary"))
    );
    assert!(
        !canonical
            .output
            .iter()
            .any(|part| matches!(part, CanonicalContent::ClaudeThinking { .. }))
    );
    let claude = canonical_response_to_claude(canonical).expect("canonical to Claude");
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
}

#[test]
fn synthetic_claude_redacted_thinking_is_rejected_upstream() {
    let response = CanonicalResponse {
        id: "synthetic".to_owned(),
        model: "claude-sonnet".to_owned(),
        created_at: 0,
        output: vec![CanonicalContent::RedactedThinking {
            data: JsonData::String("opaque".to_owned()),
            model: Some("claude-sonnet".to_owned()),
            provenance: OpaqueStateProvenance::Synthetic,
        }],
        finish_reason: Some(FinishReason::Stop),
        usage: None,
    };
    assert!(canonical_response_to_claude(response).is_err());
}

#[test]
fn claude_stream_events_keep_signature_partial_json_ping_error_unknown_and_cancel() {
    let snapshot: ClaudeStreamSnapshot = serde_json::from_str(
        r#"{
          "events":[
            {"type":"message_start","message":{"id":"msg-stream","type":"message","role":"assistant","model":"claude-sonnet","content":[]}},
            {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":"sig"}},
            {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"plan"}},
            {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}},
            {"type":"content_block_stop","index":0},
            {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call","name":"lookup","input":{}}},
            {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}},
            {"type":"content_block_stop","index":1},
            {"type":"ping"},
            {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":1,"output_tokens":2}},
            {"type":"error","error":{"code":"rate","message":"retry"}},
            {"type":"future_event","index":7,"delta":{"type":"text_delta","text":"future"},"provider_field":"kept"},
            {"type":"fallback","reason":"provider-fallback"},
            {"type":"message_stop"}
          ],
          "usage":{}
        }"#,
    )
    .expect("Claude stream");
    let events = claude_stream_to_semantic_events(&snapshot).expect("Claude semantic stream");
    assert!(events.iter().any(|event| matches!(
        event,
        ClaudeStreamSemanticEvent::SignatureDelta { signature, .. } if signature == "sig"
    )));
    assert!(!events.iter().any(|event| matches!(
        event,
        ClaudeStreamSemanticEvent::TextDelta { delta, .. } if delta == "\n"
    )));
    assert!(events.iter().any(|event| matches!(
        event,
        ClaudeStreamSemanticEvent::ToolInputJsonDelta { delta, .. } if delta == "{\"a\":"
    )));
    assert!(
        events
            .iter()
            .any(|event| matches!(event, ClaudeStreamSemanticEvent::Ping))
    );
    assert!(events.iter().any(|event| matches!(
        event,
        ClaudeStreamSemanticEvent::Error { message, .. } if message == "retry"
    )));
    assert!(events.iter().any(|event| matches!(
        event,
        ClaudeStreamSemanticEvent::Unknown { kind, .. } if kind == "future_event"
    )));
    assert!(events.iter().any(|event| matches!(
        event,
        ClaudeStreamSemanticEvent::Unknown {
            kind,
            fields: JsonData::Object(fields)
        } if kind == "future_event"
            && fields.contains_key("index")
            && fields.contains_key("delta")
            && fields.contains_key("provider_field")
    )));
    assert!(events.iter().any(|event| matches!(
        event,
        ClaudeStreamSemanticEvent::Unknown { kind, .. } if kind == "fallback"
    )));

    let mut state = ClaudeStreamState::default();
    for event in &events {
        state.apply(event).expect("valid Claude event sequence");
    }
    assert!(
        state
            .apply(&ClaudeStreamSemanticEvent::MessageStop)
            .is_err()
    );

    let mut interrupted = ClaudeStreamState::default();
    interrupted.apply(&events[0]).expect("stream start");
    interrupted.apply(&events[1]).expect("open thinking block");
    interrupted
        .apply(&claude_stream_cancelled())
        .expect("client cancellation");
    assert!(interrupted.is_cancelled());
    assert!(interrupted.has_open_blocks());
}

#[test]
fn claude_stream_known_event_unknown_fields_are_typed_rejections() {
    let cases = [
        (
            serde_json::json!({
                "events": [],
                "usage": {},
                "futureSnapshotField": true
            }),
            "snapshot.futureSnapshotField",
        ),
        (
            serde_json::json!({
                "events": [],
                "usage": {"futureUsageField": true}
            }),
            "snapshot.usage.futureUsageField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "type": "message_delta",
                    "futureMessageDeltaField": true
                }],
                "usage": {}
            }),
            "events[0].futureMessageDeltaField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "type": "content_block_delta",
                    "delta": {
                        "type": "text_delta",
                        "text": "x",
                        "futureDeltaField": true
                    }
                }],
                "usage": {}
            }),
            "events[0].delta.futureDeltaField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "type": "content_block_delta",
                    "delta": {
                        "type": "text_delta",
                        "text": "x",
                        "stop_reason": "end_turn",
                        "futureStopReasonField": true
                    }
                }],
                "usage": {}
            }),
            "events[0].delta.futureStopReasonField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "type": "content_block_start",
                    "futureContentBlockStartField": true
                }],
                "usage": {}
            }),
            "events[0].futureContentBlockStartField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "type": "content_block_stop",
                    "futureContentBlockStopField": true
                }],
                "usage": {}
            }),
            "events[0].futureContentBlockStopField",
        ),
        (
            serde_json::json!({
                "events": [{
                    "type": "error",
                    "error": {
                        "message": "boom",
                        "futureErrorField": true
                    }
                }],
                "usage": {}
            }),
            "events[0].error.futureErrorField",
        ),
    ];

    for (document, expected_path) in cases {
        let snapshot: ClaudeStreamSnapshot = serde_json::from_value(document)
            .expect("unknown Claude stream field is retained by the wire DTO");
        let error = validate_claude_stream_snapshot(&snapshot)
            .expect_err("Claude stream validator must reject unknown known-event fields");
        assert!(matches!(
            error,
            RelayConvertError::UnsupportedFeature(detail)
                if detail.feature == "unknown_field"
                    && detail.path == expected_path
                    && detail.source_format == "claude"
                    && detail.target_format == "provider_neutral_ir"
        ));
    }

    let block_snapshot: ClaudeStreamSnapshot = serde_json::from_value(serde_json::json!({
        "events": [{
            "type": "content_block_start",
            "content_block": {
                "type": "text",
                "text": "kept",
                "futureBlockField": true
            }
        }],
        "usage": {}
    }))
    .expect("Claude content block extra is retained by the wire DTO");
    assert!(
        block_snapshot.events[0]
            .content_block
            .as_ref()
            .expect("content block")
            .extra
            .contains_key("futureBlockField")
    );
    validate_claude_stream_snapshot(&block_snapshot)
        .expect("ClaudeContentBlock.extra is an intentional retention point");
}

#[test]
fn claude_stream_state_requires_typed_deltas_and_monotonic_non_concurrent_blocks() {
    let snapshot: ClaudeStreamSnapshot = serde_json::from_str(
        r#"{
          "events":[
            {"type":"message_start","message":{"id":"msg","type":"message","role":"assistant","model":"claude-sonnet","content":[]}},
            {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"one","signature":"sig-one"}},
            {"type":"content_block_stop","index":0},
            {"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":"two","signature":"sig-two"}},
            {"type":"content_block_stop","index":1},
            {"type":"message_stop"}
          ],
          "usage":{}
        }"#,
    )
    .expect("two thinking blocks");
    let events = claude_stream_to_semantic_events(&snapshot).expect("semantic events");
    let mut state = ClaudeStreamState::default();
    for event in &events {
        state.apply(event).expect("sequential thinking blocks");
    }

    let wrong_delta: ClaudeStreamSnapshot = serde_json::from_str(
        r#"{
          "events":[
            {"type":"message_start","message":{"id":"msg","type":"message","role":"assistant","model":"claude-sonnet","content":[]}},
            {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"one","signature":"sig"}},
            {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}
          ],
          "usage":{}
        }"#,
    )
    .expect("wrong delta");
    let wrong_events = claude_stream_to_semantic_events(&wrong_delta).expect("semantic events");
    let mut wrong_state = ClaudeStreamState::default();
    wrong_state.apply(&wrong_events[0]).expect("stream start");
    wrong_state
        .apply(&wrong_events[1])
        .expect("open thinking block");
    assert!(wrong_state.apply(&wrong_events[2]).is_err());

    let mut text_signature_state = ClaudeStreamState::default();
    text_signature_state
        .apply(&ClaudeStreamSemanticEvent::MessageStart { message: None })
        .expect("stream start");
    text_signature_state
        .apply(&ClaudeStreamSemanticEvent::ContentBlockStart {
            index: 0,
            block: ClaudeContentBlock {
                kind: "text".to_owned(),
                text: Some("text".to_owned()),
                thinking: None,
                signature: None,
                data: None,
                id: None,
                name: None,
                input: None,
                tool_use_id: None,
                content: None,
                source: None,
                extra: std::collections::BTreeMap::new(),
            },
        })
        .expect("open text block");
    assert!(
        text_signature_state
            .apply(&ClaudeStreamSemanticEvent::SignatureDelta {
                index: 0,
                signature: "wrong-block".to_owned(),
            })
            .is_err()
    );

    let concurrent = ClaudeStreamSemanticEvent::ContentBlockStart {
        index: 1,
        block: ClaudeContentBlock {
            kind: "text".to_owned(),
            text: Some("second".to_owned()),
            thinking: None,
            signature: None,
            data: None,
            id: None,
            name: None,
            input: None,
            tool_use_id: None,
            content: None,
            source: None,
            extra: std::collections::BTreeMap::new(),
        },
    };
    let mut concurrent_state = ClaudeStreamState::default();
    concurrent_state
        .apply(&ClaudeStreamSemanticEvent::MessageStart { message: None })
        .expect("stream start");
    concurrent_state
        .apply(&ClaudeStreamSemanticEvent::ContentBlockStart {
            index: 0,
            block: ClaudeContentBlock {
                kind: "text".to_owned(),
                text: Some("first".to_owned()),
                thinking: None,
                signature: None,
                data: None,
                id: None,
                name: None,
                input: None,
                tool_use_id: None,
                content: None,
                source: None,
                extra: std::collections::BTreeMap::new(),
            },
        })
        .expect("first block");
    assert!(concurrent_state.apply(&concurrent).is_err());
}

#[test]
fn claude_stream_state_rejects_duplicate_stop_and_events_after_end() {
    let mut state = ClaudeStreamState::default();
    state
        .apply(&ClaudeStreamSemanticEvent::MessageStart { message: None })
        .expect("start");
    state
        .apply(&ClaudeStreamSemanticEvent::ContentBlockStart {
            index: 0,
            block: ClaudeContentBlock {
                kind: "text".to_owned(),
                text: None,
                thinking: None,
                signature: None,
                data: None,
                id: None,
                name: None,
                input: None,
                tool_use_id: None,
                content: None,
                source: None,
                extra: std::collections::BTreeMap::new(),
            },
        })
        .expect("block start");
    state
        .apply(&ClaudeStreamSemanticEvent::ContentBlockStop { index: 0 })
        .expect("block stop");
    state
        .apply(&ClaudeStreamSemanticEvent::MessageStop)
        .expect("message stop");
    assert!(
        state
            .apply(&ClaudeStreamSemanticEvent::MessageStop)
            .is_err()
    );
    assert!(
        state
            .apply(&ClaudeStreamSemanticEvent::MessageDelta {
                stop_reason: Some("stop".to_owned()),
                usage: None,
            })
            .is_err()
    );
    assert!(
        state
            .apply(&ClaudeStreamSemanticEvent::TextDelta {
                index: 0,
                delta: "late".to_owned(),
            })
            .is_err()
    );
}
