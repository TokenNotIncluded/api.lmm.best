use super::*;
use axum::{
    Router,
    extract::State,
    http::{HeaderMap, StatusCode, Uri},
    response::IntoResponse,
    routing::post,
};
use serde_json::{Value, json};
use std::sync::{
    Mutex,
    atomic::{AtomicUsize, Ordering},
};
use tokio::time::timeout;
use tokio_stream::StreamExt;

fn request(client: super::super::ClientProtocol, stream: bool) -> Request {
    let value = match client {
        super::super::ClientProtocol::Chat => {
            json!({"model":"visible-alias","messages":[{"role":"user","content":"input"}],"stream":stream,"stream_options":{"include_usage":true}})
        }
        super::super::ClientProtocol::Responses => {
            json!({"model":"visible-alias","input":"input","stream":stream})
        }
    };
    Request::parse(
        client,
        &serde_json::to_vec(&value).unwrap(),
        &Limits::default(),
    )
    .unwrap()
}

async fn collect(session: HoneypotSession) -> (Vec<u8>, Result<(), RelayError>) {
    assert_eq!(session.head.await.unwrap().unwrap().status, 200);
    let mut body = session.body;
    let mut bytes = Vec::new();
    while let Some(chunk) = body.next().await {
        bytes.extend_from_slice(&chunk.unwrap());
    }
    (bytes, session.completion.await.unwrap())
}

fn frames(bytes: &[u8]) -> Vec<Value> {
    std::str::from_utf8(bytes)
        .unwrap()
        .split("\n\n")
        .filter_map(|frame| {
            frame
                .lines()
                .find_map(|line| line.strip_prefix("data: "))
                .filter(|v| *v != "[DONE]")
                .map(|data| serde_json::from_str(data).unwrap())
        })
        .collect()
}

fn text(client: super::super::ClientProtocol, stream: bool, bytes: &[u8]) -> String {
    use super::super::ClientProtocol::{Chat, Responses};
    if !stream {
        let v: Value = serde_json::from_slice(bytes).unwrap();
        assert_eq!(v["model"], "visible-alias");
        return match client {
            Chat => {
                assert_eq!(v["object"], "chat.completion");
                v["choices"][0]["message"]["content"]
                    .as_str()
                    .unwrap()
                    .to_owned()
            }
            Responses => {
                assert_eq!(v["object"], "response");
                assert_eq!(v["status"], "completed");
                v["output"][0]["content"][0]["text"]
                    .as_str()
                    .unwrap()
                    .to_owned()
            }
        };
    }
    let frames = frames(bytes);
    match client {
        Chat => {
            assert_eq!(
                std::str::from_utf8(bytes)
                    .unwrap()
                    .matches("data: [DONE]\n\n")
                    .count(),
                1
            );
            assert_eq!(
                frames
                    .iter()
                    .filter(|v| v["choices"][0]["finish_reason"] == "stop")
                    .count(),
                1
            );
            assert!(
                frames
                    .iter()
                    .all(|v| v["object"] == "chat.completion.chunk")
            );
            frames
                .iter()
                .filter_map(|v| v["choices"][0]["delta"]["content"].as_str())
                .collect()
        }
        Responses => {
            let kinds: Vec<_> = frames.iter().map(|v| v["type"].as_str().unwrap()).collect();
            assert_eq!(kinds.first(), Some(&"response.created"));
            assert_eq!(kinds.last(), Some(&"response.completed"));
            for kind in [
                "response.in_progress",
                "response.output_item.added",
                "response.content_part.added",
                "response.output_text.done",
                "response.content_part.done",
                "response.output_item.done",
                "response.completed",
            ] {
                assert_eq!(kinds.iter().filter(|v| **v == kind).count(), 1, "{kind}");
            }
            for (index, v) in frames.iter().enumerate() {
                assert_eq!(v["sequence_number"], index);
            }
            frames
                .iter()
                .filter(|v| v["type"] == "response.output_text.delta")
                .map(|v| v["delta"].as_str().unwrap())
                .collect()
        }
    }
}

#[tokio::test]
async fn fixed_text_real_body_has_complete_four_protocol_shapes_and_null_usage() {
    use super::super::ClientProtocol::{Chat, Responses};
    let expected = "固定文本 \"hello\"\n🙂";
    for client in [Chat, Responses] {
        for stream in [false, true] {
            let limits = Limits::default();
            let transport = HoneypotTransport::new(limits.clone()).unwrap();
            let session = transport
                .start(
                    request(client, stream),
                    "fixed_nonce",
                    HoneypotTarget::FixedText(expected.into()),
                    limits,
                    0,
                    Arc::new(()),
                )
                .unwrap();
            let (bytes, completion) = collect(session).await;
            completion.unwrap();
            assert_eq!(text(client, stream, &bytes), expected);
            assert!(!std::str::from_utf8(&bytes).unwrap().contains("honeypot"));
            let values = if stream {
                frames(&bytes)
            } else {
                vec![serde_json::from_slice::<Value>(&bytes).unwrap()]
            };
            for v in values {
                assert!(v.get("usage").is_none_or(Value::is_null));
                assert!(v["response"].get("usage").is_none_or(Value::is_null));
            }
        }
    }
}

fn source(protocol: UpstreamProtocol, stream: bool) -> Vec<u8> {
    let full = match protocol {
        UpstreamProtocol::Chat => {
            json!({"id":"upstream-chat","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"upstream-answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}})
        }
        UpstreamProtocol::Responses => {
            json!({"id":"upstream-resp","object":"response","status":"completed","output":[{"id":"item1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"upstream-answer","annotations":[]}]}],"usage":{"input_tokens":2,"output_tokens":3}})
        }
        UpstreamProtocol::Anthropic => {
            json!({"id":"upstream-ant","type":"message","role":"assistant","content":[{"type":"text","text":"upstream-answer"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":3}})
        }
        UpstreamProtocol::Gemini => {
            json!({"responseId":"upstream-gem","candidates":[{"index":0,"content":{"parts":[{"text":"upstream-answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3}})
        }
    };
    if !stream {
        return serde_json::to_vec(&full).unwrap();
    }
    let events = match protocol {
        UpstreamProtocol::Chat => vec![
            json!({"id":"upstream-chat","choices":[{"index":0,"delta":{"role":"assistant","content":"upstream-answer"},"finish_reason":null}]}),
            json!({"id":"upstream-chat","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}),
        ],
        UpstreamProtocol::Responses => vec![
            json!({"type":"response.created","response":{"id":"upstream-resp","status":"in_progress","output":[]}}),
            json!({"type":"response.output_item.added","output_index":0,"item":{"id":"item1","type":"message","role":"assistant","content":[]}}),
            json!({"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}),
            json!({"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"upstream-answer"}),
            json!({"type":"response.content_part.done","output_index":0,"content_index":0}),
            json!({"type":"response.output_item.done","output_index":0,"item":full["output"][0]}),
            json!({"type":"response.completed","response":full}),
        ],
        UpstreamProtocol::Anthropic => vec![
            json!({"type":"message_start","message":{"id":"upstream-ant","type":"message","role":"assistant","content":[],"usage":{"input_tokens":2,"output_tokens":0}}}),
            json!({"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}),
            json!({"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"upstream-answer"}}),
            json!({"type":"content_block_stop","index":0}),
            json!({"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}),
            json!({"type":"message_stop"}),
        ],
        UpstreamProtocol::Gemini => vec![full],
    };
    let mut data = String::new();
    for value in events {
        if let Some(kind) = value["type"].as_str() {
            data.push_str(&format!("event: {kind}\n"));
        }
        data.push_str(&format!("data: {value}\n\n"));
    }
    if protocol == UpstreamProtocol::Chat {
        data.push_str("data: [DONE]\n\n");
    }
    data.into_bytes()
}

type Observation = (String, HeaderMap, Value);
#[derive(Clone)]
struct Fixture {
    observed: Arc<Mutex<Vec<Observation>>>,
    protocol: UpstreamProtocol,
    stream: bool,
}
async fn fixture(
    State(f): State<Fixture>,
    uri: Uri,
    headers: HeaderMap,
    body: Bytes,
) -> impl IntoResponse {
    f.observed.lock().unwrap().push((
        uri.to_string(),
        headers,
        serde_json::from_slice(&body).unwrap(),
    ));
    (
        [(
            header::CONTENT_TYPE,
            if f.stream {
                "text/event-stream"
            } else {
                "application/json"
            },
        )],
        source(f.protocol, f.stream),
    )
}

#[tokio::test]
async fn custom_four_upstream_protocols_convert_both_clients_and_stream_modes_over_http() {
    use super::super::ClientProtocol::{Chat, Responses};
    for protocol in [
        UpstreamProtocol::Chat,
        UpstreamProtocol::Responses,
        UpstreamProtocol::Anthropic,
        UpstreamProtocol::Gemini,
    ] {
        for client in [Chat, Responses] {
            for stream in [false, true] {
                let observed = Arc::new(Mutex::new(Vec::new()));
                let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
                let address = listener.local_addr().unwrap();
                let server = tokio::spawn(
                    axum::serve(
                        listener,
                        Router::new().fallback(post(fixture)).with_state(Fixture {
                            observed: observed.clone(),
                            protocol,
                            stream,
                        }),
                    )
                    .into_future(),
                );
                let limits = Limits::default();
                let transport = HoneypotTransport::new(limits.clone()).unwrap();
                let route = HoneypotUpstream::local(
                    protocol,
                    "server-model".into(),
                    &format!("http://{address}/v1"),
                    "separate-moderation-secret",
                    stream,
                )
                .unwrap();
                let session = transport
                    .start(
                        request(client, stream),
                        "custom_nonce",
                        HoneypotTarget::Upstream(route),
                        limits,
                        0,
                        Arc::new(()),
                    )
                    .unwrap();
                let (bytes, result) = collect(session).await;
                result.unwrap_or_else(|error| {
                    panic!("{protocol:?} -> {client:?}, stream={stream}: {error:?}")
                });
                assert_eq!(text(client, stream, &bytes), "upstream-answer");
                {
                    let observed = observed.lock().unwrap();
                    assert_eq!(observed.len(), 1);
                    let (path, headers, body) = &observed[0];
                    let (expected, auth) = match protocol {
                        UpstreamProtocol::Chat => {
                            ("/v1/chat/completions".to_owned(), "authorization")
                        }
                        UpstreamProtocol::Responses => {
                            ("/v1/responses".to_owned(), "authorization")
                        }
                        UpstreamProtocol::Anthropic => ("/v1/messages".to_owned(), "x-api-key"),
                        UpstreamProtocol::Gemini => (
                            format!(
                                "/v1/models/server-model:{}{}",
                                if stream {
                                    "streamGenerateContent"
                                } else {
                                    "generateContent"
                                },
                                if stream { "?alt=sse" } else { "" }
                            ),
                            "x-goog-api-key",
                        ),
                    };
                    assert_eq!(path, &expected);
                    assert_eq!(
                        headers[auth],
                        if auth == "authorization" {
                            "Bearer separate-moderation-secret"
                        } else {
                            "separate-moderation-secret"
                        }
                    );
                    for forbidden in [
                        "cookie",
                        "idempotency-key",
                        "x-forwarded-for",
                        "x-real-ip",
                        "proxy-authorization",
                    ] {
                        assert!(headers.get(forbidden).is_none());
                    }
                    if protocol != UpstreamProtocol::Gemini {
                        assert_eq!(body["model"], "server-model");
                        assert_eq!(body["stream"], stream);
                    }
                }
                server.abort();
                assert!(server.await.unwrap_err().is_cancelled());
            }
        }
    }
}

#[tokio::test]
async fn redirected_upstream_is_never_followed_or_retried() {
    let first = Arc::new(AtomicUsize::new(0));
    let second = Arc::new(AtomicUsize::new(0));
    let listener2 = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address2 = listener2.local_addr().unwrap();
    let second_handler = second.clone();
    let server2 = tokio::spawn(
        axum::serve(
            listener2,
            Router::new().fallback(post(move || {
                let second = second_handler.clone();
                async move {
                    second.fetch_add(1, Ordering::SeqCst);
                    StatusCode::OK
                }
            })),
        )
        .into_future(),
    );
    let listener1 = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address1 = listener1.local_addr().unwrap();
    let first_handler = first.clone();
    let server1 = tokio::spawn(
        axum::serve(
            listener1,
            Router::new().fallback(post(move || {
                let first = first_handler.clone();
                async move {
                    first.fetch_add(1, Ordering::SeqCst);
                    (
                        StatusCode::TEMPORARY_REDIRECT,
                        [(
                            header::LOCATION,
                            format!("http://{address2}/v1/chat/completions"),
                        )],
                    )
                }
            })),
        )
        .into_future(),
    );
    let limits = Limits::default();
    let transport = HoneypotTransport::new(limits.clone()).unwrap();
    let route = HoneypotUpstream::local(
        UpstreamProtocol::Chat,
        "server".into(),
        &format!("http://{address1}/v1"),
        "secret",
        false,
    )
    .unwrap();
    let session = transport
        .start(
            request(super::super::ClientProtocol::Chat, false),
            "redirect_nonce",
            HoneypotTarget::Upstream(route),
            limits,
            0,
            Arc::new(()),
        )
        .unwrap();
    assert_eq!(
        session.head.await.unwrap().unwrap_err(),
        RelayError::UpstreamStatus(307)
    );
    assert_eq!(
        session.completion.await.unwrap(),
        Err(RelayError::UpstreamStatus(307))
    );
    assert_eq!(first.load(Ordering::SeqCst), 1);
    assert_eq!(second.load(Ordering::SeqCst), 0);
    drop(session.body);
    server1.abort();
    server2.abort();
}

#[test]
fn upstream_address_boundary_rejects_external_dns_and_credential_injection() {
    for url in [
        "http://localhost/v1",
        "https://example.com/v1",
        "http://169.254.169.254/v1",
        "http://10.0.0.1/v1",
        "http://user:pass@127.0.0.1/v1",
        "http://127.0.0.1/v1?url=https://example.com",
        "http://127.0.0.1/v1#secret",
        "http://127.0.0.1/v1\n",
        "http://127.0.0.1/v1\\evil",
    ] {
        assert!(
            HoneypotUpstream::local(UpstreamProtocol::Chat, "model".into(), url, "secret", false)
                .is_err(),
            "{url}"
        );
    }
    assert!(
        HoneypotUpstream::local(
            UpstreamProtocol::Chat,
            "model".into(),
            "http://127.0.0.1/v1",
            "secret\r\nx-evil: yes",
            false
        )
        .is_err()
    );
}

#[tokio::test]
async fn fixed_text_encoded_byte_cap_and_utf8_output_budget_are_enforced() {
    use super::super::ClientProtocol::{Chat, Responses};
    let limits = Limits {
        output_bytes: 64,
        event_bytes: 64,
        tool_argument_bytes: 64,
        ..Limits::default()
    };
    let transport = HoneypotTransport::new(limits.clone()).unwrap();
    let session = transport
        .start(
            request(Chat, false),
            "small_nonce",
            HoneypotTarget::FixedText("text".into()),
            limits,
            0,
            Arc::new(()),
        )
        .unwrap();
    assert!(matches!(
        session.head.await.unwrap(),
        Err(RelayError::Limit("encoded output"))
    ));
    assert!(session.completion.await.unwrap().is_err());
    for client in [Chat, Responses] {
        for stream in [false, true] {
            let limits = Limits::default();
            let transport = HoneypotTransport::new(limits.clone()).unwrap();
            let mut req = request(client, stream);
            req.max_output_tokens = 5;
            let session = transport
                .start(
                    req,
                    "unicode_nonce",
                    HoneypotTarget::FixedText("🙂🙂".into()),
                    limits,
                    0,
                    Arc::new(()),
                )
                .unwrap();
            let (bytes, result) = collect(session).await;
            result.unwrap();
            let values = if stream {
                frames(&bytes)
            } else {
                vec![serde_json::from_slice::<Value>(&bytes).unwrap()]
            };
            assert!(std::str::from_utf8(&bytes).unwrap().contains("🙂"));
            match client {
                Chat => assert!(
                    values
                        .iter()
                        .any(|v| v["choices"][0]["finish_reason"] == "length")
                ),
                Responses => assert!(
                    values
                        .iter()
                        .any(|v| v["status"] == "incomplete" || v["type"] == "response.incomplete")
                ),
            }
        }
    }
}

#[tokio::test]
async fn output_is_paced_and_body_drop_cancels_producer_and_releases_lease() {
    let limits = Limits::default();
    let transport = HoneypotTransport::new(limits.clone()).unwrap();
    let start = Instant::now();
    let session = transport
        .start(
            request(super::super::ClientProtocol::Chat, false),
            "paced_nonce",
            HoneypotTarget::FixedText("paced".into()),
            limits.clone(),
            1000,
            Arc::new(()),
        )
        .unwrap();
    let (bytes, result) = collect(session).await;
    result.unwrap();
    assert!(start.elapsed() >= Duration::from_millis(bytes.len() as u64 - 20));
    struct Guard(Arc<AtomicUsize>);
    impl Drop for Guard {
        fn drop(&mut self) {
            self.0.fetch_add(1, Ordering::SeqCst);
        }
    }
    let drops = Arc::new(AtomicUsize::new(0));
    let session = transport
        .start(
            request(super::super::ClientProtocol::Chat, true),
            "drop_nonce",
            HoneypotTarget::FixedText("long".into()),
            limits,
            1,
            Arc::new(Guard(drops.clone())),
        )
        .unwrap();
    session.head.await.unwrap().unwrap();
    assert_eq!(drops.load(Ordering::SeqCst), 0);
    drop(session.body);
    assert_eq!(
        timeout(Duration::from_secs(1), session.completion)
            .await
            .unwrap()
            .unwrap(),
        Err(RelayError::Cancelled)
    );
    assert_eq!(drops.load(Ordering::SeqCst), 1);
}

#[tokio::test]
async fn no_unbounded_wait_when_pacing_exceeds_total_deadline() {
    let started = Instant::now();
    let limits = Limits {
        total_timeout: Duration::from_millis(50),
        ..Limits::default()
    };
    let transport = HoneypotTransport::new(limits.clone()).unwrap();
    let session = transport
        .start(
            request(super::super::ClientProtocol::Chat, true),
            "deadline_nonce",
            HoneypotTarget::FixedText("slow".into()),
            limits,
            1,
            Arc::new(()),
        )
        .unwrap();
    session.head.await.unwrap().unwrap();
    let mut body = session.body;
    let mut bytes = Vec::new();
    while let Some(chunk) = body.next().await {
        bytes.extend_from_slice(&chunk.unwrap());
    }
    assert_eq!(
        session.completion.await.unwrap(),
        Err(RelayError::Timeout(Phase::Total))
    );
    assert!(!std::str::from_utf8(&bytes).unwrap().contains("[DONE]"));
    // At 1 byte/sec, neither data nor an error control frame fits 50 ms.
    assert_eq!(bytes.len(), 0, "deadline exhaustion must not bypass pacing");
    assert!(started.elapsed() >= Duration::from_millis(45));
}

#[tokio::test]
async fn independent_transport_concurrency_is_bounded_and_drain_cancels_without_money_hooks() {
    let limits = Limits {
        max_in_flight: 1,
        ..Limits::default()
    };
    let transport = HoneypotTransport::new(limits.clone()).unwrap();
    let session = transport
        .start(
            request(super::super::ClientProtocol::Chat, true),
            "active_nonce",
            HoneypotTarget::FixedText("long".into()),
            limits.clone(),
            1,
            Arc::new(()),
        )
        .unwrap();
    session.head.await.unwrap().unwrap();
    assert!(matches!(
        transport.start(
            request(super::super::ClientProtocol::Chat, true),
            "busy_nonce",
            HoneypotTarget::FixedText("text".into()),
            limits.clone(),
            1,
            Arc::new(())
        ),
        Err(RelayError::Busy)
    ));
    let report = transport.drain(Duration::ZERO).await;
    assert_eq!(report.cancelled, 1);
    assert_eq!(report.remaining, 0);
    assert_eq!(
        session.completion.await.unwrap(),
        Err(RelayError::Cancelled)
    );
    assert!(matches!(
        transport.start(
            request(super::super::ClientProtocol::Chat, true),
            "drained_nonce",
            HoneypotTarget::FixedText("text".into()),
            limits,
            1,
            Arc::new(())
        ),
        Err(RelayError::Draining)
    ));
    drop(session.body);
}

#[tokio::test]
async fn truncated_stream_and_excess_provider_usage_never_emit_success_terminal() {
    for (stream, excess_usage) in [(true, false), (false, true)] {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let handler = move || async move {
            let bytes = if stream {
                b"data: {\"id\":\"partial\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n".to_vec()
            } else {
                let mut value: Value =
                    serde_json::from_slice(&source(UpstreamProtocol::Chat, false)).unwrap();
                value["usage"]["completion_tokens"] = json!(5000);
                serde_json::to_vec(&value).unwrap()
            };
            (
                [(
                    header::CONTENT_TYPE,
                    if stream {
                        "text/event-stream"
                    } else {
                        "application/json"
                    },
                )],
                bytes,
            )
        };
        let server = tokio::spawn(
            axum::serve(listener, Router::new().fallback(post(handler))).into_future(),
        );
        let limits = Limits::default();
        let transport = HoneypotTransport::new(limits.clone()).unwrap();
        let route = HoneypotUpstream::local(
            UpstreamProtocol::Chat,
            "server".into(),
            &format!("http://{address}/v1"),
            "secret",
            stream,
        )
        .unwrap();
        let session = transport
            .start(
                request(super::super::ClientProtocol::Chat, stream),
                "invalid_nonce",
                HoneypotTarget::Upstream(route),
                limits,
                0,
                Arc::new(()),
            )
            .unwrap();
        if excess_usage {
            assert!(matches!(
                session.head.await.unwrap(),
                Err(RelayError::Limit("upstream output tokens"))
            ));
            assert!(matches!(
                session.completion.await.unwrap(),
                Err(RelayError::Limit("upstream output tokens"))
            ));
            drop(session.body);
        } else {
            let (bytes, result) = collect(session).await;
            assert_eq!(result, Err(RelayError::Truncated));
            let body = std::str::from_utf8(&bytes).unwrap();
            assert!(body.contains("partial"));
            assert!(body.contains("invalid_upstream_response"));
            assert!(!body.contains("[DONE]"));
        }
        server.abort();
        let _ = server.await;
    }
}

#[tokio::test]
async fn error_control_frames_follow_actual_encoded_byte_pacing() {
    use super::super::ClientProtocol::{Chat, Responses};
    for client in [Chat, Responses] {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let handler = || async move {
            ([(header::CONTENT_TYPE, "text/event-stream")],
             b"data: {\"id\":\"partial\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n".to_vec())
        };
        let server = tokio::spawn(
            axum::serve(listener, Router::new().fallback(post(handler))).into_future(),
        );
        let limits = Limits::default();
        let transport = HoneypotTransport::new(limits.clone()).unwrap();
        let route = HoneypotUpstream::local(
            UpstreamProtocol::Chat,
            "server".into(),
            &format!("http://{address}/v1"),
            "secret",
            true,
        )
        .unwrap();
        let rate = 1200;
        let started = Instant::now();
        let session = transport
            .start(
                request(client, true),
                "error_pacing_nonce",
                HoneypotTarget::Upstream(route),
                limits.clone(),
                rate,
                Arc::new(()),
            )
            .unwrap();
        let (bytes, result) = collect(session).await;
        assert_eq!(result, Err(RelayError::Truncated));
        assert!(bytes.len() <= limits.output_bytes);
        let body = std::str::from_utf8(&bytes).unwrap();
        assert!(body.contains("invalid_upstream_response"));
        assert!(!body.contains("[DONE]"));
        assert!(!body.contains("response.completed"));
        let required = Duration::from_secs_f64(bytes.len() as f64 / f64::from(rate));
        assert!(
            started.elapsed() + Duration::from_millis(20) >= required,
            "{client:?}: {} bytes at {rate} bytes/sec need {required:?}; actual {:?}",
            bytes.len(),
            started.elapsed()
        );
        server.abort();
        let _ = server.await;
    }
}
