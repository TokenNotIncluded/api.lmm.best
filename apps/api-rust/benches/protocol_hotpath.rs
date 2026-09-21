//! Offline microbench calibration for request, response, and stream paths.
//!
//! This intentionally uses only the standard library harness (`Instant` and
//! `black_box`) plus the production contract APIs. It reports measured
//! throughput and per-operation p50/p95 from batch-average samples. An
//! accepted regression baseline remains an external release artifact. The
//! tests are ignored by default so a normal test run does not become a
//! benchmark run.

use std::{hint::black_box, sync::Once, time::Instant};

use lmm_api_rs::{
    protocol_runtime_registry::validated_current_registry, routes::sse::SseFrameParser,
};
use lmm_contracts::relay::{
    CanonicalStreamEvent, ConversionPlan, Fidelity, OpenAiChatRequest, OpenAiChatResponse,
    OpenAiStreamSnapshot, Protocol, canonical_request_to_openai_chat,
    canonical_request_to_openai_responses, openai_chat_request_to_canonical,
    openai_chat_response_to_canonical, openai_stream_to_canonical,
};

const SAMPLE_COUNT: usize = 32;
const ITERATIONS_PER_SAMPLE: usize = 4;
const TOTAL_ITERATIONS: usize = SAMPLE_COUNT * ITERATIONS_PER_SAMPLE;

const REQUEST_CORPUS: &[u8] = br#"{
  "model":"gpt-bench",
  "messages":[{"role":"user","content":"calibrate"}],
  "stream":false
}"#;

const RESPONSE_CORPUS: &[u8] = br#"{
  "id":"bench-response",
  "object":"chat.completion",
  "created":1,
  "model":"gpt-bench",
  "choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],
  "usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
}"#;

const STREAM_CORPUS: &[u8] = br#"{
  "events":[
    {"id":"bench-stream","model":"gpt-bench","choices":[{"index":0,"delta":{"role":"assistant","content":"a"}}]},
    {"id":"bench-stream","model":"gpt-bench","choices":[{"index":0,"delta":{"content":"b"}}]},
    {"id":"bench-stream","model":"gpt-bench","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}
  ],
  "usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
}"#;

const SSE_CORPUS: &[u8] = b"event: update\r\ndata: one\r\ndata: two\r\n\r\ndata: [DONE]\n\n";

const NATIVE_PASSTHROUGH_CORPUS: &[u8] = b"data: provider bytes stay opaque\n\ndata: [DONE]\n\n";

const BENCHMARK_MANIFEST_VERSION: &str = "protocol-hotpath-v2";

#[derive(Clone, Copy, Debug)]
struct BenchmarkScenario {
    label: &'static str,
    workload: &'static str,
    text_bytes: usize,
    history_messages: usize,
    tool_count: usize,
    stream_chunks: usize,
    parallel_tool_calls: bool,
    multimodal: bool,
    reasoning: bool,
    client_abort: bool,
    native_passthrough: bool,
    plan_compile: bool,
}

const SCENARIO_MANIFEST: &[BenchmarkScenario] = &[
    BenchmarkScenario {
        label: "request_text_1k",
        workload: "request",
        text_bytes: 1_024,
        history_messages: 1,
        tool_count: 0,
        stream_chunks: 0,
        parallel_tool_calls: false,
        multimodal: false,
        reasoning: false,
        client_abort: false,
        native_passthrough: false,
        plan_compile: false,
    },
    BenchmarkScenario {
        label: "request_text_16k",
        workload: "request",
        text_bytes: 16_384,
        history_messages: 1,
        tool_count: 0,
        stream_chunks: 0,
        parallel_tool_calls: false,
        multimodal: false,
        reasoning: false,
        client_abort: false,
        native_passthrough: false,
        plan_compile: false,
    },
    BenchmarkScenario {
        label: "request_text_256k",
        workload: "request",
        text_bytes: 262_144,
        history_messages: 1,
        tool_count: 0,
        stream_chunks: 0,
        parallel_tool_calls: false,
        multimodal: false,
        reasoning: false,
        client_abort: false,
        native_passthrough: false,
        plan_compile: false,
    },
    BenchmarkScenario {
        label: "request_history_10",
        workload: "request",
        text_bytes: 64,
        history_messages: 10,
        tool_count: 0,
        stream_chunks: 0,
        parallel_tool_calls: false,
        multimodal: false,
        reasoning: false,
        client_abort: false,
        native_passthrough: false,
        plan_compile: false,
    },
    BenchmarkScenario {
        label: "request_history_100",
        workload: "request",
        text_bytes: 64,
        history_messages: 100,
        tool_count: 0,
        stream_chunks: 0,
        parallel_tool_calls: false,
        multimodal: false,
        reasoning: false,
        client_abort: false,
        native_passthrough: false,
        plan_compile: false,
    },
    BenchmarkScenario {
        label: "request_tools_1",
        workload: "request",
        text_bytes: 64,
        history_messages: 1,
        tool_count: 1,
        stream_chunks: 0,
        parallel_tool_calls: false,
        multimodal: false,
        reasoning: false,
        client_abort: false,
        native_passthrough: false,
        plan_compile: false,
    },
    BenchmarkScenario {
        label: "request_tools_8",
        workload: "request",
        text_bytes: 64,
        history_messages: 1,
        tool_count: 8,
        stream_chunks: 0,
        parallel_tool_calls: false,
        multimodal: false,
        reasoning: false,
        client_abort: false,
        native_passthrough: false,
        plan_compile: false,
    },
    BenchmarkScenario {
        label: "request_tools_32",
        workload: "request",
        text_bytes: 64,
        history_messages: 1,
        tool_count: 32,
        stream_chunks: 0,
        parallel_tool_calls: false,
        multimodal: false,
        reasoning: false,
        client_abort: false,
        native_passthrough: false,
        plan_compile: false,
    },
    BenchmarkScenario {
        label: "stream_chunks_10",
        workload: "stream",
        text_bytes: 0,
        history_messages: 0,
        tool_count: 0,
        stream_chunks: 10,
        parallel_tool_calls: false,
        multimodal: false,
        reasoning: false,
        client_abort: false,
        native_passthrough: false,
        plan_compile: false,
    },
    BenchmarkScenario {
        label: "stream_chunks_100",
        workload: "stream",
        text_bytes: 0,
        history_messages: 0,
        tool_count: 0,
        stream_chunks: 100,
        parallel_tool_calls: false,
        multimodal: false,
        reasoning: false,
        client_abort: false,
        native_passthrough: false,
        plan_compile: false,
    },
    BenchmarkScenario {
        label: "stream_chunks_1000",
        workload: "stream",
        text_bytes: 0,
        history_messages: 0,
        tool_count: 0,
        stream_chunks: 1_000,
        parallel_tool_calls: false,
        multimodal: false,
        reasoning: false,
        client_abort: false,
        native_passthrough: false,
        plan_compile: false,
    },
    BenchmarkScenario {
        label: "request_parallel_tools",
        workload: "request",
        text_bytes: 64,
        history_messages: 1,
        tool_count: 4,
        stream_chunks: 0,
        parallel_tool_calls: true,
        multimodal: false,
        reasoning: false,
        client_abort: false,
        native_passthrough: false,
        plan_compile: false,
    },
    BenchmarkScenario {
        label: "request_multimodal",
        workload: "request",
        text_bytes: 64,
        history_messages: 1,
        tool_count: 0,
        stream_chunks: 0,
        parallel_tool_calls: false,
        multimodal: true,
        reasoning: false,
        client_abort: false,
        native_passthrough: false,
        plan_compile: false,
    },
    BenchmarkScenario {
        label: "request_reasoning",
        workload: "request",
        text_bytes: 64,
        history_messages: 1,
        tool_count: 0,
        stream_chunks: 0,
        parallel_tool_calls: false,
        multimodal: false,
        reasoning: true,
        client_abort: false,
        native_passthrough: false,
        plan_compile: false,
    },
    BenchmarkScenario {
        label: "stream_client_abort",
        workload: "stream",
        text_bytes: 0,
        history_messages: 0,
        tool_count: 0,
        stream_chunks: 10,
        parallel_tool_calls: false,
        multimodal: false,
        reasoning: false,
        client_abort: true,
        native_passthrough: false,
        plan_compile: false,
    },
    BenchmarkScenario {
        label: "native_passthrough",
        workload: "native_passthrough",
        text_bytes: 0,
        history_messages: 0,
        tool_count: 0,
        stream_chunks: 0,
        parallel_tool_calls: false,
        multimodal: false,
        reasoning: false,
        client_abort: false,
        native_passthrough: true,
        plan_compile: false,
    },
    BenchmarkScenario {
        label: "plan_compile",
        workload: "plan_compile",
        text_bytes: 0,
        history_messages: 0,
        tool_count: 0,
        stream_chunks: 0,
        parallel_tool_calls: false,
        multimodal: false,
        reasoning: false,
        client_abort: false,
        native_passthrough: false,
        plan_compile: true,
    },
];

#[derive(Clone, Copy, Debug)]
struct BenchmarkDimension {
    label: &'static str,
    dimension: &'static str,
    value: &'static str,
}

/// Metadata-only coverage contract for PLAN 10.1. These rows describe the
/// dimensions a benchmark run must identify; they do not claim that a run has
/// executed or establish a performance baseline. Route-shape and
/// client-behavior rows remain metadata-only because this offline file has no
/// gateway route graph or client lifecycle/abort harness.
const SCENARIO_DIMENSION_MANIFEST: &[BenchmarkDimension] = &[
    BenchmarkDimension {
        label: "request_parallel_tools_1",
        dimension: "parallel_tool_calls",
        value: "1",
    },
    BenchmarkDimension {
        label: "request_parallel_tools_4",
        dimension: "parallel_tool_calls",
        value: "4",
    },
    BenchmarkDimension {
        label: "request_parallel_tools_16",
        dimension: "parallel_tool_calls",
        value: "16",
    },
    BenchmarkDimension {
        label: "request_multimodal_url",
        dimension: "multimodal",
        value: "url",
    },
    BenchmarkDimension {
        label: "request_multimodal_base64",
        dimension: "multimodal",
        value: "base64",
    },
    BenchmarkDimension {
        label: "request_multimodal_file_reference",
        dimension: "multimodal",
        value: "file_reference",
    },
    BenchmarkDimension {
        label: "request_reasoning_none",
        dimension: "reasoning",
        value: "none",
    },
    BenchmarkDimension {
        label: "request_reasoning_summary",
        dimension: "reasoning",
        value: "summary",
    },
    BenchmarkDimension {
        label: "request_reasoning_opaque_signature",
        dimension: "reasoning",
        value: "opaque_signature",
    },
    BenchmarkDimension {
        label: "route_one_hop",
        dimension: "route_shape",
        value: "one_hop",
    },
    BenchmarkDimension {
        label: "route_old_two_hop",
        dimension: "route_shape",
        value: "old_two_hop",
    },
    BenchmarkDimension {
        label: "route_new_ir",
        dimension: "route_shape",
        value: "new_ir",
    },
    BenchmarkDimension {
        label: "client_normal",
        dimension: "client_behavior",
        value: "normal",
    },
    BenchmarkDimension {
        label: "client_slow",
        dimension: "client_behavior",
        value: "slow",
    },
    BenchmarkDimension {
        label: "client_interrupted",
        dimension: "client_behavior",
        value: "interrupted",
    },
];

static MANIFEST_REPORTED: Once = Once::new();

fn report_scenario_manifest() {
    MANIFEST_REPORTED.call_once(|| {
        for scenario in SCENARIO_MANIFEST {
            eprintln!(
                "protocol_hotpath_manifest version={} label={} workload={} text_bytes={} history_messages={} tool_count={} stream_chunks={} parallel_tool_calls={} multimodal={} reasoning={} client_abort={} native_passthrough={} plan_compile={}",
                BENCHMARK_MANIFEST_VERSION,
                scenario.label,
                scenario.workload,
                scenario.text_bytes,
                scenario.history_messages,
                scenario.tool_count,
                scenario.stream_chunks,
                scenario.parallel_tool_calls,
                scenario.multimodal,
                scenario.reasoning,
                scenario.client_abort,
                scenario.native_passthrough,
                scenario.plan_compile,
            );
        }
        for dimension in SCENARIO_DIMENSION_MANIFEST {
            eprintln!(
                "protocol_hotpath_dimension_manifest version={} label={} dimension={} value={}",
                BENCHMARK_MANIFEST_VERSION,
                dimension.label,
                dimension.dimension,
                dimension.value,
            );
        }
    });
}

#[test]
fn benchmark_scenario_manifest_is_complete() {
    assert_eq!(BENCHMARK_MANIFEST_VERSION, "protocol-hotpath-v2");
    for &(text_bytes, history_messages, tool_count) in &[
        (1_024, 1, 0),
        (16_384, 1, 0),
        (262_144, 1, 0),
        (64, 10, 0),
        (64, 100, 0),
        (64, 1, 1),
        (64, 1, 8),
        (64, 1, 32),
    ] {
        assert!(SCENARIO_MANIFEST.iter().any(|scenario| {
            scenario.text_bytes == text_bytes
                && scenario.history_messages == history_messages
                && scenario.tool_count == tool_count
        }));
    }
    for stream_chunks in [10, 100, 1_000] {
        assert!(
            SCENARIO_MANIFEST
                .iter()
                .any(|scenario| scenario.stream_chunks == stream_chunks)
        );
    }
    assert!(
        SCENARIO_MANIFEST
            .iter()
            .any(|scenario| scenario.native_passthrough)
    );
    assert!(
        SCENARIO_MANIFEST
            .iter()
            .any(|scenario| scenario.plan_compile)
    );
    assert!(
        SCENARIO_MANIFEST
            .iter()
            .any(|scenario| scenario.parallel_tool_calls)
    );
    assert!(SCENARIO_MANIFEST.iter().any(|scenario| scenario.multimodal));
    assert!(SCENARIO_MANIFEST.iter().any(|scenario| scenario.reasoning));
    assert!(
        SCENARIO_MANIFEST
            .iter()
            .any(|scenario| scenario.client_abort)
    );

    for expected in [
        ("parallel_tool_calls", "1"),
        ("parallel_tool_calls", "4"),
        ("parallel_tool_calls", "16"),
        ("multimodal", "url"),
        ("multimodal", "base64"),
        ("multimodal", "file_reference"),
        ("reasoning", "none"),
        ("reasoning", "summary"),
        ("reasoning", "opaque_signature"),
        ("route_shape", "one_hop"),
        ("route_shape", "old_two_hop"),
        ("route_shape", "new_ir"),
        ("client_behavior", "normal"),
        ("client_behavior", "slow"),
        ("client_behavior", "interrupted"),
    ] {
        assert!(
            SCENARIO_DIMENSION_MANIFEST
                .iter()
                .any(|scenario| scenario.dimension == expected.0 && scenario.value == expected.1),
            "missing benchmark dimension {}={}",
            expected.0,
            expected.1
        );
    }
}

fn request_scenario(text_bytes: usize, history_messages: usize, tool_count: usize) -> Vec<u8> {
    let content = "x".repeat(text_bytes);
    let messages = (0..history_messages)
        .map(|index| {
            serde_json::json!({
                "role": if index % 2 == 0 { "user" } else { "assistant" },
                "content": content,
            })
        })
        .collect::<Vec<_>>();
    let tools = (0..tool_count)
        .map(|index| {
            serde_json::json!({
                "type": "function",
                "function": {
                    "name": format!("tool_{index}"),
                    "description": "benchmark tool",
                    "parameters": {
                        "type": "object",
                        "properties": {"value": {"type": "string"}},
                    },
                },
            })
        })
        .collect::<Vec<_>>();
    serde_json::to_vec(&serde_json::json!({
        "model": "gpt-bench",
        "messages": messages,
        "tools": tools,
        "stream": false,
    }))
    .expect("serialize request scenario")
}

fn stream_scenario(chunk_count: usize) -> Vec<u8> {
    let mut events = Vec::with_capacity(chunk_count.saturating_add(1));
    for index in 0..chunk_count {
        events.push(serde_json::json!({
            "id": "bench-stream",
            "model": "gpt-bench",
            "choices": [{
                "index": 0,
                "delta": {
                    "role": (index == 0).then_some("assistant"),
                    "content": "x",
                },
            }],
        }));
    }
    events.push(serde_json::json!({
        "id": "bench-stream",
        "model": "gpt-bench",
        "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
        "usage": {"prompt_tokens": 2, "completion_tokens": chunk_count, "total_tokens": chunk_count.saturating_add(2)},
    }));
    serde_json::to_vec(&serde_json::json!({
        "events": events,
        "usage": {"prompt_tokens": 2, "completion_tokens": chunk_count, "total_tokens": chunk_count.saturating_add(2)},
    }))
    .expect("serialize stream scenario")
}

fn calibrate(label: &str, bytes: usize, mut operation: impl FnMut() -> u64) {
    report_scenario_manifest();
    for _ in 0..ITERATIONS_PER_SAMPLE {
        black_box(operation());
    }
    let mut sample_nanos = Vec::with_capacity(SAMPLE_COUNT);
    let mut checksum = 0_u64;
    let mut total_elapsed_nanos = 0_u128;
    for _ in 0..SAMPLE_COUNT {
        let started = Instant::now();
        for _ in 0..ITERATIONS_PER_SAMPLE {
            checksum = checksum.wrapping_add(black_box(operation()));
        }
        let elapsed_nanos = started.elapsed().as_nanos().max(1);
        total_elapsed_nanos = total_elapsed_nanos.saturating_add(elapsed_nanos);
        sample_nanos.push(elapsed_nanos.div_ceil(ITERATIONS_PER_SAMPLE as u128).max(1));
    }
    sample_nanos.sort_unstable();
    let p50_nanos = sample_nanos[(SAMPLE_COUNT / 2).saturating_sub(1)];
    let p95_index = (SAMPLE_COUNT * 95).div_ceil(100).saturating_sub(1);
    let p95_nanos = sample_nanos[p95_index];
    let operations_per_second = (TOTAL_ITERATIONS as u128)
        .saturating_mul(1_000_000_000)
        .checked_div(total_elapsed_nanos.max(1))
        .unwrap_or(0);
    let total_bytes = (bytes as u128).saturating_mul(TOTAL_ITERATIONS as u128);
    let bytes_per_second = if bytes == 0 {
        0
    } else {
        total_bytes
            .checked_mul(1_000_000_000)
            .and_then(|value| value.checked_div(total_elapsed_nanos.max(1)))
            .unwrap_or(0)
    };
    eprintln!(
        "protocol_hotpath label={label} samples={SAMPLE_COUNT} iterations_per_sample={ITERATIONS_PER_SAMPLE} operations_per_second={operations_per_second} bytes_per_second={bytes_per_second} p50_nanos={p50_nanos} p95_nanos={p95_nanos} checksum={checksum}"
    );
    assert_ne!(checksum, 0, "benchmark semantic checksum must be non-zero");
}

#[test]
#[ignore = "run explicitly as an offline calibration benchmark"]
fn request_conversion_hotpath_calibration() {
    calibrate("request", REQUEST_CORPUS.len(), || {
        let request: OpenAiChatRequest =
            serde_json::from_slice(black_box(REQUEST_CORPUS)).expect("request corpus");
        let converted =
            black_box(openai_chat_request_to_canonical(request).expect("request conversion"));
        (converted.value.model.len() as u64).wrapping_add(converted.value.messages.len() as u64)
    });
}

#[test]
#[ignore = "run explicitly as an offline calibration benchmark"]
fn request_conversion_dimension_matrix_calibration() {
    for (label, text_bytes, history_messages, tool_count) in [
        ("request_text_1k", 1_024, 1, 0),
        ("request_text_16k", 16_384, 1, 0),
        ("request_text_256k", 262_144, 1, 0),
        ("request_history_10", 64, 10, 0),
        ("request_history_100", 64, 100, 0),
        ("request_tools_1", 64, 1, 1),
        ("request_tools_8", 64, 1, 8),
        ("request_tools_32", 64, 1, 32),
    ] {
        let corpus = request_scenario(text_bytes, history_messages, tool_count);
        calibrate(label, corpus.len(), || {
            let request: OpenAiChatRequest =
                serde_json::from_slice(black_box(corpus.as_slice())).expect("request scenario");
            let converted = black_box(
                openai_chat_request_to_canonical(request).expect("request scenario conversion"),
            );
            (converted.value.model.len() as u64)
                .wrapping_add(converted.value.messages.len() as u64)
                .wrapping_add(converted.value.tools.len() as u64)
        });
    }
}

#[test]
#[ignore = "run explicitly as an offline calibration benchmark"]
fn request_feature_dimension_matrix_calibration() {
    // This is an offline typed-conversion matrix, not an HTTP/provider run.
    // OpenAiChatContentPart models image_url.url, so the file-reference case
    // uses a file URI without claiming that a gateway upload was measured.
    for parallel_tool_count in [1, 4, 16] {
        for (multimodal_label, image_url) in [
            ("url", "https://example.invalid/bench-image.png"),
            ("base64", "data:image/png;base64,YmVuY2htYXJrLWltYWdl"),
            ("file_reference", "file://benchmark/image.png"),
        ] {
            for (reasoning_label, reasoning_effort) in [
                ("none", None),
                ("summary", Some("summary")),
                // The opaque case is represented by the authentic Google
                // thought_signature extension below, not a control string.
                ("opaque", None),
            ] {
                let label = format!(
                    "request_feature_matrix_parallel_{parallel_tool_count}_{multimodal_label}_{reasoning_label}"
                );
                let tools = (0..parallel_tool_count)
                    .map(|index| {
                        serde_json::json!({
                            "type": "function",
                            "function": {
                                "name": format!("feature_tool_{index}"),
                                "description": "benchmark feature-matrix tool",
                                "parameters": {
                                    "type": "object",
                                    "properties": {
                                        "value": {"type": "string"},
                                    },
                                },
                            },
                        })
                    })
                    .collect::<Vec<_>>();
                let request_json = serde_json::json!({
                    "model": "gpt-bench",
                    "messages": [{
                        "role": "user",
                        "content": [
                            {"type": "text", "text": "feature matrix"},
                            {"type": "image_url", "image_url": {"url": image_url}},
                        ],
                        "extra_content": if reasoning_label == "opaque" {
                            serde_json::json!({
                                "google": {
                                    "thought_signature": "bench-authentic-thought-signature",
                                },
                            })
                        } else {
                            serde_json::Value::Null
                        },
                    }],
                    "tools": tools,
                    "parallel_tool_calls": true,
                    "reasoning_effort": reasoning_effort,
                    "stream": false,
                });
                let corpus =
                    serde_json::to_vec(&request_json).expect("serialize feature matrix request");
                calibrate(label.as_str(), corpus.len(), || {
                    let request: OpenAiChatRequest =
                        serde_json::from_slice(black_box(corpus.as_slice()))
                            .expect("feature matrix request");
                    let converted = black_box(
                        openai_chat_request_to_canonical(request)
                            .expect("feature matrix request conversion"),
                    );
                    let canonical_bytes = serde_json::to_vec(&converted.value)
                        .expect("serialize canonical feature matrix");
                    (canonical_bytes.len() as u64)
                        .wrapping_add(converted.value.messages.len() as u64)
                        .wrapping_add(converted.value.tools.len() as u64)
                        .wrapping_add(u64::from(
                            converted.value.options.parallel_tool_calls.unwrap_or(false),
                        ))
                });
            }
        }
    }
}

#[test]
#[ignore = "run explicitly as an offline calibration benchmark"]
fn route_shape_conversion_calibration() {
    // These are offline local conversion costs only. They do not exercise the
    // production gateway route graph, HTTP transport, or provider runtime.
    calibrate("route_one_hop", REQUEST_CORPUS.len(), || {
        let request: OpenAiChatRequest =
            serde_json::from_slice(black_box(REQUEST_CORPUS)).expect("route request corpus");
        let canonical = black_box(
            openai_chat_request_to_canonical(request).expect("one-hop request conversion"),
        );
        let canonical_bytes =
            serde_json::to_vec(&canonical.value).expect("serialize one-hop canonical request");
        (REQUEST_CORPUS.len() as u64).wrapping_add(canonical_bytes.len() as u64)
    });

    calibrate("route_old_two_hop", REQUEST_CORPUS.len(), || {
        let request: OpenAiChatRequest =
            serde_json::from_slice(black_box(REQUEST_CORPUS)).expect("route request corpus");
        let canonical = black_box(
            openai_chat_request_to_canonical(request).expect("old two-hop source conversion"),
        );
        let canonical_bytes =
            serde_json::to_vec(&canonical.value).expect("serialize old two-hop canonical");
        let chat = black_box(
            canonical_request_to_openai_chat(canonical.value)
                .expect("old two-hop canonical-to-chat conversion"),
        );
        let chat_bytes = serde_json::to_vec(&chat.value).expect("serialize old two-hop chat wire");
        let roundtrip = black_box(
            openai_chat_request_to_canonical(
                serde_json::from_slice(black_box(chat_bytes.as_slice()))
                    .expect("parse old two-hop chat wire"),
            )
            .expect("old two-hop target conversion"),
        );
        let roundtrip_bytes =
            serde_json::to_vec(&roundtrip.value).expect("serialize old two-hop target canonical");
        (REQUEST_CORPUS.len() as u64)
            .wrapping_add(canonical_bytes.len() as u64)
            .wrapping_add(chat_bytes.len() as u64)
            .wrapping_add(roundtrip_bytes.len() as u64)
    });

    calibrate("route_new_ir", REQUEST_CORPUS.len(), || {
        let request: OpenAiChatRequest =
            serde_json::from_slice(black_box(REQUEST_CORPUS)).expect("route request corpus");
        let canonical =
            black_box(openai_chat_request_to_canonical(request).expect("new-IR source conversion"));
        let canonical_bytes =
            serde_json::to_vec(&canonical.value).expect("serialize new-IR canonical request");
        let responses = black_box(
            canonical_request_to_openai_responses(canonical.value)
                .expect("new-IR canonical-to-Responses conversion"),
        );
        let responses_bytes =
            serde_json::to_vec(&responses.value).expect("serialize new-IR Responses wire");
        (REQUEST_CORPUS.len() as u64)
            .wrapping_add(canonical_bytes.len() as u64)
            .wrapping_add(responses_bytes.len() as u64)
    });
}

#[test]
#[ignore = "run explicitly as an offline calibration benchmark"]
fn response_conversion_hotpath_calibration() {
    calibrate("response", RESPONSE_CORPUS.len(), || {
        let response: OpenAiChatResponse =
            serde_json::from_slice(black_box(RESPONSE_CORPUS)).expect("response corpus");
        let converted =
            black_box(openai_chat_response_to_canonical(response).expect("response conversion"));
        (converted.value.id.len() as u64)
            .wrapping_add(converted.value.output.len() as u64)
            .wrapping_add(
                converted
                    .value
                    .usage
                    .as_ref()
                    .map_or(0, |usage| usage.total_tokens),
            )
    });
}

#[test]
#[ignore = "run explicitly as an offline calibration benchmark"]
fn stream_conversion_hotpath_calibration() {
    calibrate("stream", STREAM_CORPUS.len(), || {
        let snapshot: OpenAiStreamSnapshot =
            serde_json::from_slice(black_box(STREAM_CORPUS)).expect("stream corpus");
        let events = black_box(openai_stream_to_canonical(&snapshot));
        events.iter().fold(0_u64, |value, event| match event {
            CanonicalStreamEvent::TextDelta { delta, .. }
            | CanonicalStreamEvent::ReasoningDelta { delta, .. }
            | CanonicalStreamEvent::ToolArgumentsDelta { delta, .. } => {
                value.wrapping_add(delta.len() as u64)
            }
            _ => value.wrapping_add(1),
        })
    });
}

#[test]
#[ignore = "run explicitly as an offline calibration benchmark"]
fn stream_chunk_cardinality_calibration() {
    for (label, chunk_count) in [
        ("stream_chunks_10", 10),
        ("stream_chunks_100", 100),
        ("stream_chunks_1000", 1_000),
    ] {
        let corpus = stream_scenario(chunk_count);
        calibrate(label, corpus.len(), || {
            let snapshot: OpenAiStreamSnapshot =
                serde_json::from_slice(black_box(corpus.as_slice())).expect("stream scenario");
            let events = black_box(openai_stream_to_canonical(&snapshot));
            events.iter().fold(0_u64, |value, event| match event {
                CanonicalStreamEvent::TextDelta { delta, .. }
                | CanonicalStreamEvent::ReasoningDelta { delta, .. }
                | CanonicalStreamEvent::ToolArgumentsDelta { delta, .. } => {
                    value.wrapping_add(delta.len() as u64)
                }
                _ => value.wrapping_add(1),
            })
        });
    }
}

#[test]
#[ignore = "run explicitly as an offline calibration benchmark"]
fn sse_frame_parser_hotpath_calibration() {
    calibrate("sse", SSE_CORPUS.len(), || {
        let mut parser = SseFrameParser::new(1024);
        let frames = black_box(parser.feed(SSE_CORPUS).expect("SSE corpus"));
        let tail = black_box(parser.finish().expect("SSE EOF"));
        frames
            .iter()
            .map(|frame| frame.raw.len() as u64)
            .sum::<u64>()
            .wrapping_add(
                frames
                    .iter()
                    .map(|frame| frame.data.len() as u64)
                    .sum::<u64>(),
            )
            .wrapping_add(tail.iter().map(|frame| frame.raw.len() as u64).sum::<u64>())
    });
}

#[test]
#[ignore = "run explicitly as an offline calibration benchmark"]
fn sse_incremental_byte_partition_calibration() {
    calibrate("sse_incremental_one_byte", SSE_CORPUS.len(), || {
        let mut parser = SseFrameParser::new(1024);
        let mut checksum = 0_u64;
        for chunk in black_box(SSE_CORPUS).chunks(1) {
            let frames = parser.feed(chunk).expect("incremental SSE corpus");
            checksum = frames.iter().fold(checksum, |value, frame| {
                value.wrapping_add(frame.raw.len() as u64)
            });
        }
        let tail = parser.finish().expect("incremental SSE EOF");
        tail.iter().fold(checksum, |value, frame| {
            value.wrapping_add(frame.raw.len() as u64)
        })
    });
}

#[test]
#[ignore = "run explicitly as an offline calibration benchmark"]
fn client_sse_lifecycle_calibration() {
    // These scenarios measure only local SSE parser lifecycle costs. They do
    // not represent HTTP scheduling, client backpressure, network pacing, or
    // production client-abort metrics.
    calibrate("client_normal", SSE_CORPUS.len(), || {
        let mut parser = SseFrameParser::new(1024);
        let frames = black_box(parser.feed(SSE_CORPUS).expect("normal SSE feed"));
        let tail = black_box(parser.finish().expect("normal SSE finish"));
        frames
            .iter()
            .chain(tail.iter())
            .fold(SSE_CORPUS.len() as u64, |value, frame| {
                value
                    .wrapping_add(frame.raw.len() as u64)
                    .wrapping_add(frame.data.len() as u64)
            })
    });

    calibrate("client_slow", SSE_CORPUS.len(), || {
        let mut parser = SseFrameParser::new(1024);
        let mut checksum = SSE_CORPUS.len() as u64;
        for chunk in black_box(SSE_CORPUS).chunks(1) {
            let frames = parser.feed(chunk).expect("slow SSE feed");
            checksum = frames.iter().fold(checksum, |value, frame| {
                value
                    .wrapping_add(frame.raw.len() as u64)
                    .wrapping_add(frame.data.len() as u64)
            });
        }
        let tail = parser.finish().expect("slow SSE finish");
        tail.iter().fold(checksum, |value, frame| {
            value
                .wrapping_add(frame.raw.len() as u64)
                .wrapping_add(frame.data.len() as u64)
        })
    });

    calibrate(
        "client_interrupted",
        SSE_CORPUS.len().saturating_sub(2),
        || {
            let prefix_len = SSE_CORPUS.len().saturating_sub(2);
            let prefix = &SSE_CORPUS[..prefix_len];
            let mut parser = SseFrameParser::new(1024);
            let frames = parser
                .feed(black_box(prefix))
                .expect("interrupted SSE feed");
            assert!(parser.has_unfinished_frame());
            let checksum = frames.iter().fold(prefix_len as u64, |value, frame| {
                value
                    .wrapping_add(frame.raw.len() as u64)
                    .wrapping_add(frame.data.len() as u64)
            });
            let buffered = parser.buffered_frame_bytes() as u64;
            drop(parser);
            checksum.wrapping_add(buffered)
        },
    );
}

#[test]
#[ignore = "run explicitly as an offline calibration benchmark"]
fn native_passthrough_hotpath_calibration() {
    calibrate(
        "native_passthrough",
        NATIVE_PASSTHROUGH_CORPUS.len(),
        || {
            // This is deliberately a slice observation, not an HTTP benchmark:
            // the pointer assertion makes an accidental body copy visible.
            let bytes = black_box(NATIVE_PASSTHROUGH_CORPUS);
            assert_eq!(bytes.as_ptr(), NATIVE_PASSTHROUGH_CORPUS.as_ptr());
            bytes.iter().fold(0_u64, |value, byte| {
                value.wrapping_mul(257).wrapping_add(u64::from(*byte))
            })
        },
    );
}

#[test]
#[ignore = "run explicitly as an offline calibration benchmark"]
fn plan_compile_hotpath_calibration() {
    let registry = validated_current_registry().expect("runtime registry");
    // Plan compilation has no meaningful byte denominator, so this reports
    // operations/second and latency while leaving bytes/second at zero.
    calibrate("plan_compile", 0, || {
        let plan = black_box(
            ConversionPlan::compile_with_validated_registry(
                Protocol::OpenAi,
                Protocol::OpenAi,
                "gpt-bench",
                &registry,
            )
            .expect("raw conversion plan"),
        );
        let fidelity: u64 = match plan.fidelity {
            Fidelity::Exact => 0,
            Fidelity::Normalized => 1,
            Fidelity::Lossy => 2,
            Fidelity::Unsupported => 3,
        };
        fidelity
            .wrapping_add(plan.hop_count as u64)
            .wrapping_add(plan.converter_ids.len() as u64)
    });
}

#[test]
#[ignore = "run explicitly as an offline calibration benchmark"]
fn request_parallel_tool_dimension_matrix_calibration() {
    // The wire contract exposes parallel_tool_calls as a boolean; the
    // requested 1/4/16 dimension is represented by the number of generated
    // tool definitions while the flag remains enabled.  Message-history
    // variants are included here as executable request workloads.  Route
    // shape and client pacing/abort rows remain metadata-only because this
    // offline contract benchmark has no HTTP/session driver.
    for (label, parallel_tool_calls, history_messages) in [
        ("request_parallel_tools_1", 1, 1),
        ("request_parallel_tools_4", 4, 1),
        ("request_parallel_tools_16", 16, 1),
        ("request_parallel_tools_4_history_10", 4, 10),
        ("request_parallel_tools_4_history_100", 4, 100),
    ] {
        let mut value: serde_json::Value =
            serde_json::from_slice(&request_scenario(64, history_messages, parallel_tool_calls))
                .expect("parallel request scenario");
        value["parallel_tool_calls"] = serde_json::Value::Bool(true);
        let corpus = serde_json::to_vec(&value).expect("serialize parallel request scenario");
        calibrate(label, corpus.len(), || {
            let request: OpenAiChatRequest = serde_json::from_slice(black_box(corpus.as_slice()))
                .expect("parallel request scenario");
            let converted = black_box(
                openai_chat_request_to_canonical(request)
                    .expect("parallel request scenario conversion"),
            );
            assert_eq!(converted.value.messages.len(), history_messages);
            assert_eq!(converted.value.tools.len(), parallel_tool_calls);
            (converted.value.messages.len() as u64)
                .wrapping_mul(257)
                .wrapping_add(converted.value.tools.len() as u64)
        });
    }
}
