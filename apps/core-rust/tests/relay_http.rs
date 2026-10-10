//! Real TCP upstream and downstream tests; no external API keys or paid requests.
use axum::{
    Router,
    body::{Body, Bytes},
    http::Response,
    routing::post,
};
use lmm_core::relay::*;
use serde_json::{Value, json};
use std::{
    io,
    process::{Child, Command, Stdio},
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, AtomicUsize, Ordering},
    },
    time::Duration,
};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpListener, TcpStream},
    sync::{Semaphore, mpsc},
    task::JoinHandle,
    time::{sleep, timeout},
};

const WAIT: Duration = Duration::from_secs(8);
static IDS: AtomicUsize = AtomicUsize::new(1);
fn context() -> RequestContext {
    RequestContext {
        request_id: format!("test_{}", IDS.fetch_add(1, Ordering::Relaxed)),
        actor_user_id: 7,
        key_id: 9,
    }
}

#[derive(Clone)]
enum Step {
    Data(Vec<u8>),
    Sleep(Duration),
    Wait(Arc<Semaphore>),
}
#[derive(Clone)]
struct Script {
    status: u16,
    content_type: &'static str,
    header_delay: Duration,
    steps: Vec<Step>,
    complete: bool,
}
impl Script {
    fn sse(data: Vec<u8>) -> Self {
        Self {
            status: 200,
            content_type: "text/event-stream",
            header_delay: Duration::ZERO,
            steps: vec![Step::Data(data)],
            complete: true,
        }
    }
    fn json(v: Value) -> Self {
        Self {
            content_type: "application/json",
            ..Self::sse(serde_json::to_vec(&v).unwrap())
        }
    }
}
#[derive(Default)]
struct Observed {
    hits: AtomicUsize,
    chunks: AtomicUsize,
    disconnected: AtomicBool,
    requests: Mutex<Vec<(String, Value)>>,
}
struct Upstream {
    address: String,
    observed: Arc<Observed>,
    task: JoinHandle<()>,
}
impl Drop for Upstream {
    fn drop(&mut self) {
        self.task.abort();
    }
}
async fn hold(socket: &mut TcpStream, step: &Step) -> bool {
    let mut byte = [0u8; 1];
    match step {
        Step::Sleep(d) => {
            tokio::select! { _ = sleep(*d) => true, _ = socket.read(&mut byte) => false }
        }
        Step::Wait(s) => {
            tokio::select! { permit = s.acquire() => { permit.unwrap().forget(); true }, _ = socket.read(&mut byte) => false }
        }
        Step::Data(_) => true,
    }
}
async fn read_request(socket: &mut TcpStream) -> io::Result<(String, Value)> {
    let mut bytes = Vec::new();
    let mut buf = [0u8; 4096];
    let end = loop {
        if let Some(i) = bytes.windows(4).position(|b| b == b"\r\n\r\n") {
            break i + 4;
        }
        let n = socket.read(&mut buf).await?;
        if n == 0 {
            return Err(io::ErrorKind::UnexpectedEof.into());
        }
        bytes.extend_from_slice(&buf[..n]);
        if bytes.len() > 2 * 1024 * 1024 {
            return Err(io::ErrorKind::InvalidData.into());
        }
    };
    let headers =
        String::from_utf8(bytes[..end].to_vec()).map_err(|_| io::ErrorKind::InvalidData)?;
    let len = headers
        .lines()
        .find_map(|l| {
            let (k, v) = l.split_once(':')?;
            k.eq_ignore_ascii_case("content-length")
                .then(|| v.trim().parse::<usize>().ok())
                .flatten()
        })
        .unwrap_or(0);
    if len > 2 * 1024 * 1024 {
        return Err(io::ErrorKind::InvalidData.into());
    }
    while bytes.len() < end + len {
        let n = socket.read(&mut buf).await?;
        if n == 0 {
            return Err(io::ErrorKind::UnexpectedEof.into());
        }
        bytes.extend_from_slice(&buf[..n]);
    }
    let v =
        serde_json::from_slice(&bytes[end..end + len]).map_err(|_| io::ErrorKind::InvalidData)?;
    Ok((headers, v))
}
impl Upstream {
    async fn start(script: Script) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = format!("http://{}", listener.local_addr().unwrap());
        let observed = Arc::new(Observed::default());
        let state = observed.clone();
        let task = tokio::spawn(async move {
            while let Ok((mut socket, _)) = listener.accept().await {
                let Ok(Ok(request)) = timeout(WAIT, read_request(&mut socket)).await else {
                    continue;
                };
                state.hits.fetch_add(1, Ordering::SeqCst);
                state.requests.lock().unwrap().push(request);
                if !hold(&mut socket, &Step::Sleep(script.header_delay)).await {
                    state.disconnected.store(true, Ordering::SeqCst);
                    continue;
                }
                let headers = format!(
                    "HTTP/1.1 {} Test\r\nContent-Type: {}\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n",
                    script.status, script.content_type
                );
                if socket.write_all(headers.as_bytes()).await.is_err() {
                    state.disconnected.store(true, Ordering::SeqCst);
                    continue;
                }
                for step in &script.steps {
                    if let Step::Data(data) = step {
                        if data.is_empty() {
                            continue;
                        }
                        let mut chunk = format!("{:x}\r\n", data.len()).into_bytes();
                        chunk.extend(data);
                        chunk.extend(b"\r\n");
                        if socket.write_all(&chunk).await.is_err() {
                            state.disconnected.store(true, Ordering::SeqCst);
                            break;
                        }
                        state.chunks.fetch_add(1, Ordering::SeqCst);
                    } else if !hold(&mut socket, step).await {
                        state.disconnected.store(true, Ordering::SeqCst);
                        break;
                    }
                }
                if script.complete {
                    let _ = socket.write_all(b"0\r\n\r\n").await;
                }
                let _ = socket.shutdown().await;
            }
        });
        Self {
            address,
            observed,
            task,
        }
    }
}
struct Routes {
    address: String,
    protocol: UpstreamProtocol,
}
impl RouteProvider for Routes {
    fn select<'a>(&'a self, _: &'a RequestContext, r: &'a Request) -> RelayFuture<'a, Route> {
        Box::pin(async move {
            let path = match self.protocol {
                UpstreamProtocol::Chat => "/v1/chat/completions",
                UpstreamProtocol::Responses => "/v1/responses",
                UpstreamProtocol::Anthropic => "/v1/messages",
                UpstreamProtocol::Gemini => {
                    if r.stream {
                        "/v1beta/models/upstream-real:streamGenerateContent?alt=sse"
                    } else {
                        "/v1beta/models/upstream-real:generateContent"
                    }
                }
            };
            Route::new(
                "route_test".into(),
                self.protocol,
                &format!("{}{path}", self.address),
                "upstream-real".into(),
                "upstream-test-secret",
                42,
            )
        })
    }
}
#[derive(Default)]
struct Money {
    reserves: AtomicUsize,
    finals: Mutex<Vec<Report>>,
    pinned: Mutex<Vec<(String, u64)>>,
    reject: bool,
    fail_finalize: bool,
    reserve_wait: Option<Arc<Semaphore>>,
    finalize_wait: Option<Arc<Semaphore>>,
}
impl Billing for Money {
    fn reserve<'a>(
        &'a self,
        c: &'a RequestContext,
        r: &'a Route,
        _: &'a Request,
    ) -> RelayFuture<'a, Reservation> {
        Box::pin(async move {
            self.reserves.fetch_add(1, Ordering::SeqCst);
            assert_eq!((c.actor_user_id, c.key_id), (7, 9));
            self.pinned
                .lock()
                .unwrap()
                .push((c.request_id.clone(), r.price_version));
            if let Some(gate) = &self.reserve_wait {
                gate.acquire().await.unwrap().forget();
            }
            if self.reject {
                return Err(RelayError::BillingUnavailable);
            }
            Ok(Reservation {
                id: format!("reservation_{}", c.request_id),
            })
        })
    }
    fn finalize<'a>(&'a self, r: &'a Reservation, report: &'a Report) -> RelayFuture<'a, ()> {
        Box::pin(async move {
            assert_eq!(r.id, format!("reservation_{}", report.request_id));
            self.finals.lock().unwrap().push(report.clone());
            if let Some(gate) = &self.finalize_wait {
                gate.acquire().await.unwrap().forget();
            }
            if self.fail_finalize {
                Err(RelayError::SettlementPending)
            } else {
                Ok(())
            }
        })
    }
}
fn relay(u: &Upstream, p: UpstreamProtocol, l: Limits, m: Arc<Money>) -> Relay {
    Relay::new(
        Arc::new(Routes {
            address: u.address.clone(),
            protocol: p,
        }),
        m,
        l,
    )
    .unwrap()
}
fn input(client: ClientProtocol, stream: bool) -> Value {
    let function = json!({"name":"weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}});
    match client {
        ClientProtocol::Chat => {
            json!({"model":"public-model","messages":[{"role":"user","content":"hello"}],"stream":stream,
            "stream_options":{"include_usage":true},"tools":[{"type":"function","function":function}]})
        }
        ClientProtocol::Responses => {
            let mut tool = function;
            tool["type"] = json!("function");
            json!({"model":"public-model","input":"hello","stream":stream,"tools":[tool]})
        }
    }
}
fn request(client: ClientProtocol, stream: bool, l: &Limits) -> Request {
    Request::parse(
        client,
        &serde_json::to_vec(&input(client, stream)).unwrap(),
        l,
    )
    .unwrap()
}
fn data(v: Value) -> Vec<u8> {
    format!("data: {v}\r\n\r\n").into_bytes()
}
fn event(v: Value) -> Vec<u8> {
    format!(
        "event: {}\r\ndata: {v}\r\n\r\n",
        v["type"].as_str().unwrap()
    )
    .into_bytes()
}
fn cat(parts: Vec<Vec<u8>>) -> Vec<u8> {
    parts.into_iter().flatten().collect()
}
fn openai_usage(chat: bool) -> Value {
    if chat {
        json!({"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":2}})
    } else {
        json!({"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":2}})
    }
}
fn full(p: UpstreamProtocol) -> Value {
    let args = json!({"city":"上海"});
    match p {
        UpstreamProtocol::Chat => {
            json!({"id":"up_1","choices":[{"index":0,"message":{"role":"assistant","content":"鲸鱼","reasoning_content":"think",
            "tool_calls":[{"id":"call_1","type":"function","function":{"name":"weather","arguments":args.to_string()}}]},"finish_reason":"tool_calls"}],"usage":openai_usage(true)})
        }
        UpstreamProtocol::Responses => json!({"id":"up_1","status":"completed","output":[
            {"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"think"}]},
            {"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"鲸鱼","annotations":[]}]},
            {"id":"fc_1","type":"function_call","call_id":"call_1","name":"weather","arguments":args.to_string()}],"usage":openai_usage(false)}),
        UpstreamProtocol::Anthropic => {
            json!({"id":"up_1","type":"message","content":[{"type":"thinking","thinking":"think","signature":"signed_thinking"},
            {"type":"text","text":"鲸鱼"},{"type":"tool_use","id":"call_1","name":"weather","input":args}],"stop_reason":"tool_use",
            "usage":{"input_tokens":7,"cache_read_input_tokens":3,"output_tokens":5}})
        }
        UpstreamProtocol::Gemini => {
            json!({"responseId":"up_1","candidates":[{"index":0,"content":{"role":"model","parts":[
            {"text":"think","thought":true,"thoughtSignature":"gemini_think"},{"text":"鲸鱼"},
            {"functionCall":{"id":"call_1","name":"weather","args":args},"thoughtSignature":"gemini_call"}]},"finishReason":"STOP"}],
            "usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"thoughtsTokenCount":2,"cachedContentTokenCount":3}})
        }
    }
}
fn streaming(p: UpstreamProtocol) -> Vec<u8> {
    match p {
        UpstreamProtocol::Chat => cat(vec![
            data(
                json!({"id":"up_1","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}),
            ),
            data(
                json!({"id":"up_1","choices":[{"index":0,"delta":{"reasoning_content":"think"}}]}),
            ),
            data(json!({"id":"up_1","choices":[{"index":0,"delta":{"content":"鲸"}}]})),
            data(json!({"id":"up_1","choices":[{"index":0,"delta":{"content":"鱼"}}]})),
            data(
                json!({"id":"up_1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"weather","arguments":""}}]}}]}),
            ),
            data(
                json!({"id":"up_1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\""}}]}}]}),
            ),
            data(
                json!({"id":"up_1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"上海\"}"}}]}}]}),
            ),
            data(
                json!({"id":"up_1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}),
            ),
            data(json!({"id":"up_1","choices":[],"usage":openai_usage(true)})),
            b"data: [DONE]\n\n".to_vec(),
        ]),
        UpstreamProtocol::Responses => cat(vec![
            event(
                json!({"type":"response.created","response":{"id":"up_1","status":"in_progress"}}),
            ),
            event(
                json!({"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}),
            ),
            event(
                json!({"type":"response.reasoning_summary_part.added","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":""}}),
            ),
            event(
                json!({"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"think"}),
            ),
            event(
                json!({"type":"response.reasoning_summary_part.done","output_index":0,"summary_index":0}),
            ),
            event(
                json!({"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}),
            ),
            event(
                json!({"type":"response.content_part.added","output_index":1,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}),
            ),
            event(
                json!({"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"鲸鱼"}),
            ),
            event(json!({"type":"response.content_part.done","output_index":1,"content_index":0})),
            event(
                json!({"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"weather","arguments":""}}),
            ),
            event(
                json!({"type":"response.function_call_arguments.delta","output_index":2,"delta":"{\"city\":\""}),
            ),
            event(
                json!({"type":"response.function_call_arguments.delta","output_index":2,"delta":"上海\"}"}),
            ),
            event(
                json!({"type":"response.function_call_arguments.done","output_index":2,"arguments":"{\"city\":\"上海\"}"}),
            ),
            event(json!({"type":"response.output_item.done","output_index":2})),
            event(
                json!({"type":"response.completed","response":{"id":"up_1","status":"completed","usage":openai_usage(false)}}),
            ),
        ]),
        UpstreamProtocol::Anthropic => cat(vec![
            event(
                json!({"type":"message_start","message":{"id":"up_1","usage":{"input_tokens":7,"cache_read_input_tokens":3,"output_tokens":0}}}),
            ),
            event(
                json!({"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}),
            ),
            event(
                json!({"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"think"}}),
            ),
            event(
                json!({"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signed_"}}),
            ),
            event(
                json!({"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"thinking"}}),
            ),
            event(json!({"type":"content_block_stop","index":0})),
            event(
                json!({"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}),
            ),
            event(
                json!({"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"鲸鱼"}}),
            ),
            event(json!({"type":"content_block_stop","index":1})),
            event(
                json!({"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call_1","name":"weather","input":{}}}),
            ),
            event(
                json!({"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\""}}),
            ),
            event(
                json!({"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"上海\"}"}}),
            ),
            event(json!({"type":"content_block_stop","index":2})),
            event(
                json!({"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}),
            ),
            event(json!({"type":"message_stop"})),
        ]),
        UpstreamProtocol::Gemini => cat(vec![
            data(
                json!({"responseId":"up_1","candidates":[{"index":0,"content":{"parts":[{"text":"think","thought":true,"thoughtSignature":"gemini_think"}]}}]}),
            ),
            data(
                json!({"responseId":"up_1","candidates":[{"index":0,"content":{"parts":[{"text":"鲸鱼"}]}}]}),
            ),
            data(
                json!({"responseId":"up_1","candidates":[{"index":0,"content":{"parts":[{"functionCall":{"id":"call_1","name":"weather","args":{"city":"上海"}},"thoughtSignature":"gemini_call"}]}}]}),
            ),
            data(
                json!({"responseId":"up_1","candidates":[{"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"thoughtsTokenCount":2,"cachedContentTokenCount":3}}),
            ),
        ]),
    }
}
async fn collect(s: Session) -> (Result<ResponseHead, RelayError>, String, Completion) {
    let Session {
        head,
        mut body,
        completion,
        ..
    } = s;
    let head = timeout(WAIT, head).await.unwrap().unwrap();
    let mut data = Vec::new();
    while let Some(chunk) = timeout(WAIT, body.next_chunk()).await.unwrap() {
        data.extend(chunk.unwrap());
    }
    let done = timeout(WAIT, completion).await.unwrap().unwrap();
    (head, String::from_utf8(data).unwrap(), done)
}
fn packets(s: &str) -> Vec<Value> {
    s.lines()
        .filter_map(|l| l.strip_prefix("data: "))
        .filter(|s| *s != "[DONE]")
        .map(|s| serde_json::from_str(s).unwrap())
        .collect()
}
fn assert_output(client: ClientProtocol, stream: bool, body: &str) {
    if stream {
        let packets = packets(body);
        let mut text = String::new();
        let mut thinking = String::new();
        let mut args = String::new();
        match client {
            ClientProtocol::Chat => {
                assert!(body.ends_with("data: [DONE]\n\n"));
                for p in &packets {
                    if let Some(c) = p["choices"].as_array().and_then(|c| c.first()) {
                        if let Some(t) = c["delta"]["content"].as_str() {
                            text.push_str(t);
                        }
                        if let Some(t) = c["delta"]["reasoning_content"].as_str() {
                            thinking.push_str(t);
                        }
                        if let Some(calls) = c["delta"]["tool_calls"].as_array() {
                            for c in calls {
                                if let Some(t) = c["function"]["arguments"].as_str() {
                                    args.push_str(t);
                                }
                            }
                        }
                    }
                }
                let usage = packets.last().unwrap();
                assert_eq!(usage["usage"]["prompt_tokens"], 10);
                assert_eq!(usage["usage"]["completion_tokens"], 5);
                assert_eq!(
                    packets[packets.len() - 2]["choices"][0]["finish_reason"],
                    "tool_calls"
                );
            }
            ClientProtocol::Responses => {
                for (i, p) in packets.iter().enumerate() {
                    assert_eq!(p["sequence_number"].as_u64(), Some(i as u64));
                    match p["type"].as_str() {
                        Some("response.output_text.delta") => {
                            text.push_str(p["delta"].as_str().unwrap())
                        }
                        Some("response.reasoning_summary_text.delta") => {
                            thinking.push_str(p["delta"].as_str().unwrap())
                        }
                        Some("response.function_call_arguments.delta") => {
                            args.push_str(p["delta"].as_str().unwrap())
                        }
                        _ => {}
                    }
                }
                let last = packets.last().unwrap();
                assert_eq!(last["type"], "response.completed");
                assert_eq!(last["response"]["usage"]["input_tokens"], 10);
                assert_eq!(last["response"]["usage"]["output_tokens"], 5);
                assert_eq!(last["response"]["output"][2]["call_id"], "call_1");
                assert_ne!(last["response"]["output"][2]["id"], "call_1");
            }
        }
        assert_eq!(text, "鲸鱼");
        assert_eq!(thinking, "think");
        assert_eq!(
            serde_json::from_str::<Value>(&args).unwrap(),
            json!({"city":"上海"})
        );
    } else {
        let v: Value = serde_json::from_str(body).unwrap();
        let args = match client {
            ClientProtocol::Chat => {
                let message = &v["choices"][0]["message"];
                assert_eq!(message["content"], "鲸鱼");
                assert_eq!(message["reasoning_content"], "think");
                assert_eq!(message["tool_calls"][0]["id"], "call_1");
                assert_eq!(v["choices"][0]["finish_reason"], "tool_calls");
                assert_eq!(v["usage"]["prompt_tokens"], 10);
                assert_eq!(v["usage"]["completion_tokens"], 5);
                message["tool_calls"][0]["function"]["arguments"]
                    .as_str()
                    .unwrap()
            }
            ClientProtocol::Responses => {
                assert_eq!(v["status"], "completed");
                assert_eq!(v["output"][0]["summary"][0]["text"], "think");
                assert_eq!(v["output"][1]["content"][0]["text"], "鲸鱼");
                assert_eq!(v["output"][2]["call_id"], "call_1");
                assert_eq!(v["usage"]["input_tokens"], 10);
                assert_eq!(v["usage"]["output_tokens"], 5);
                v["output"][2]["arguments"].as_str().unwrap()
            }
        };
        assert_eq!(
            serde_json::from_str::<Value>(args).unwrap(),
            json!({"city":"上海"})
        );
    }
    assert!(!body.contains("upstream-test-secret"));
    assert!(!body.contains("upstream-real"));
}

#[tokio::test]
async fn sixteen_protocol_streaming_and_json_round_trips_use_real_tcp() {
    for p in [
        UpstreamProtocol::Chat,
        UpstreamProtocol::Responses,
        UpstreamProtocol::Anthropic,
        UpstreamProtocol::Gemini,
    ] {
        for client in [ClientProtocol::Chat, ClientProtocol::Responses] {
            for stream in [false, true] {
                let script = if stream {
                    let mut s = Script::sse(Vec::new());
                    s.steps = streaming(p)
                        .chunks(3)
                        .map(|b| Step::Data(b.to_vec()))
                        .collect();
                    s
                } else {
                    Script::json(full(p))
                };
                let upstream = Upstream::start(script).await;
                let money = Arc::new(Money::default());
                let limits = Limits::default();
                let relay = relay(&upstream, p, limits.clone(), money.clone());
                let id = context();
                let expected_id = id.request_id.clone();
                let (head, body, done) =
                    collect(relay.start(request(client, stream, &limits), id).unwrap()).await;
                assert!(head.is_ok(), "{p:?} {client:?} {stream}: {head:?}");
                assert!(
                    done.error.is_none(),
                    "{p:?} {client:?} {stream}: {:?} {body}",
                    done.error
                );
                assert_eq!(done.settlement, Settlement::Confirmed);
                assert_eq!(done.report.outcome, Outcome::Completed);
                assert_eq!(done.report.usage.input_tokens, Some(10));
                assert_eq!(done.report.usage.output_tokens, Some(5));
                assert_eq!(done.report.finish_reason, Some(FinishReason::ToolCalls));
                assert_eq!(done.report.attempts, 1);
                assert_output(client, stream, &body);
                assert_eq!(money.finals.lock().unwrap().len(), 1);
                assert_eq!(money.pinned.lock().unwrap()[0], (expected_id, 42));
                assert_eq!(upstream.observed.hits.load(Ordering::SeqCst), 1);
                let requests = upstream.observed.requests.lock().unwrap();
                let (headers, payload) = &requests[0];
                let auth_name = match p {
                    UpstreamProtocol::Anthropic => "x-api-key",
                    UpstreamProtocol::Gemini => "x-goog-api-key",
                    _ => "authorization",
                };
                assert!(headers.to_ascii_lowercase().contains(auth_name));
                assert!(headers.contains("upstream-test-secret"));
                match p {
                    UpstreamProtocol::Chat => {
                        assert_eq!(payload["model"], "upstream-real");
                        if stream {
                            assert_eq!(payload["stream_options"]["include_usage"], true);
                        }
                    }
                    UpstreamProtocol::Responses | UpstreamProtocol::Anthropic => {
                        assert_eq!(payload["model"], "upstream-real")
                    }
                    UpstreamProtocol::Gemini => {
                        assert!(headers.contains("models/upstream-real:"));
                        assert_eq!(payload["contents"][0]["role"], "user");
                    }
                }
                println!("round_trip provider={p:?} client={client:?} stream={stream} passed");
            }
        }
    }
}

fn first_text(text: &str) -> Vec<u8> {
    data(json!({"id":"live_1","choices":[{"index":0,"delta":{"role":"assistant","content":text}}]}))
}
fn final_text(text: &str) -> Vec<u8> {
    cat(vec![
        data(
            json!({"id":"live_1","choices":[{"index":0,"delta":{"content":text},"finish_reason":"stop"}]}),
        ),
        data(json!({"id":"live_1","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3}})),
        b"data: [DONE]\n\n".to_vec(),
    ])
}
async fn until(body: &mut reqwest::Response, seen: &mut Vec<u8>, needle: &str) {
    timeout(WAIT, async {
        while !String::from_utf8_lossy(seen).contains(needle) {
            seen.extend(
                body.chunk()
                    .await
                    .unwrap()
                    .expect("stream ended before the expected chunk"),
            );
        }
    })
    .await
    .unwrap();
}
struct Frontend {
    url: String,
    reports: mpsc::Receiver<Completion>,
    task: JoinHandle<()>,
}
impl Drop for Frontend {
    fn drop(&mut self) {
        self.task.abort();
    }
}
impl Frontend {
    async fn start(relay: Relay, client: ClientProtocol, limits: Limits) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let url = format!("http://{}/model", listener.local_addr().unwrap());
        let (tx, reports) = mpsc::channel(4);
        let app = Router::new().route(
            "/model",
            post(move |bytes: Bytes| {
                let relay = relay.clone();
                let limits = limits.clone();
                let tx = tx.clone();
                async move {
                    let result = Request::parse(client, &bytes, &limits)
                        .and_then(|r| relay.start(r, context()));
                    match result {
                        Err(e) => Response::builder()
                            .status(e.status())
                            .body(Body::from(e.json().to_string()))
                            .unwrap(),
                        Ok(session) => {
                            let Session {
                                head,
                                body,
                                completion,
                                ..
                            } = session;
                            tokio::spawn(async move {
                                if let Ok(c) = completion.await {
                                    let _ = tx.send(c).await;
                                }
                            });
                            match head.await.unwrap() {
                                Ok(h) => Response::builder()
                                    .status(h.status)
                                    .header("content-type", h.content_type)
                                    .body(Body::from_stream(body))
                                    .unwrap(),
                                Err(e) => Response::builder()
                                    .status(e.status())
                                    .body(Body::from(e.json().to_string()))
                                    .unwrap(),
                            }
                        }
                    }
                }
            }),
        );
        let task = tokio::spawn(async move {
            axum::serve(listener, app).await.unwrap();
        });
        Self { url, reports, task }
    }
}

#[tokio::test]
async fn downstream_receives_text_before_upstream_completes() {
    let gate = Arc::new(Semaphore::new(0));
    let upstream = Upstream::start(Script {
        steps: vec![
            Step::Data(first_text("before")),
            Step::Wait(gate.clone()),
            Step::Data(final_text("after")),
        ],
        ..Script::sse(Vec::new())
    })
    .await;
    let money = Arc::new(Money::default());
    let l = Limits::default();
    let relay = relay(&upstream, UpstreamProtocol::Chat, l.clone(), money.clone());
    let mut front = Frontend::start(relay.clone(), ClientProtocol::Chat, l).await;
    let mut response = reqwest::Client::new()
        .post(&front.url)
        .json(&input(ClientProtocol::Chat, true))
        .send()
        .await
        .unwrap();
    assert_eq!(response.status(), 200);
    let mut seen = Vec::new();
    until(&mut response, &mut seen, "before").await;
    assert_eq!(relay.active_requests(), 1);
    assert!(money.finals.lock().unwrap().is_empty());
    assert!(!String::from_utf8_lossy(&seen).contains("[DONE]"));
    gate.add_permits(1);
    while let Some(c) = response.chunk().await.unwrap() {
        seen.extend(c);
    }
    let done = timeout(WAIT, front.reports.recv()).await.unwrap().unwrap();
    assert!(done.error.is_none());
    let output = String::from_utf8(seen).unwrap();
    assert!(output.contains("after"));
    assert!(output.contains("[DONE]"));
}

#[tokio::test]
async fn actual_downstream_disconnect_cancels_upstream_and_finalizes_once() {
    let upstream = Upstream::start(Script {
        steps: vec![Step::Data(first_text("before")), Step::Sleep(WAIT)],
        ..Script::sse(Vec::new())
    })
    .await;
    let l = Limits::default();
    let money = Arc::new(Money::default());
    let relay = relay(&upstream, UpstreamProtocol::Chat, l.clone(), money.clone());
    let mut front = Frontend::start(relay.clone(), ClientProtocol::Chat, l).await;
    let mut response = reqwest::Client::builder()
        .http1_only()
        .build()
        .unwrap()
        .post(&front.url)
        .json(&input(ClientProtocol::Chat, true))
        .send()
        .await
        .unwrap();
    let mut seen = Vec::new();
    until(&mut response, &mut seen, "before").await;
    drop(response);
    let done = timeout(WAIT, front.reports.recv()).await.unwrap().unwrap();
    assert_eq!(done.error, Some(RelayError::Cancelled));
    assert_eq!(done.report.outcome, Outcome::Cancelled);
    assert_eq!(done.report.usage, Usage::default());
    assert_eq!(done.settlement, Settlement::Confirmed);
    assert_eq!(money.finals.lock().unwrap().len(), 1);
    timeout(WAIT, async {
        while !upstream.observed.disconnected.load(Ordering::SeqCst) {
            sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .unwrap();
    assert_eq!(relay.drain(Duration::ZERO).await.remaining, 0);
}

#[tokio::test]
async fn cancel_during_reservation_waits_for_handle_then_finalizes_without_sending() {
    let gate = Arc::new(Semaphore::new(0));
    let money = Arc::new(Money {
        reserve_wait: Some(gate.clone()),
        ..Money::default()
    });
    let upstream = Upstream::start(Script::json(full(UpstreamProtocol::Chat))).await;
    let l = Limits::default();
    let relay = relay(&upstream, UpstreamProtocol::Chat, l.clone(), money.clone());
    let session = relay
        .start(request(ClientProtocol::Chat, false, &l), context())
        .unwrap();
    timeout(WAIT, async {
        while money.reserves.load(Ordering::SeqCst) == 0 {
            tokio::task::yield_now().await;
        }
    })
    .await
    .unwrap();
    session.cancel.cancel();
    gate.add_permits(1);
    let (head, _, done) = collect(session).await;
    assert_eq!(head.unwrap_err(), RelayError::Cancelled);
    assert_eq!(done.report.outcome, Outcome::Cancelled);
    assert_eq!(upstream.observed.hits.load(Ordering::SeqCst), 0);
    assert_eq!(money.finals.lock().unwrap().len(), 1);
    assert_eq!(done.settlement, Settlement::Confirmed);
}

#[tokio::test]
async fn admission_and_drain_are_bounded_and_do_not_abort_settlement() {
    let upstream = Upstream::start(Script {
        steps: vec![Step::Data(first_text("live")), Step::Sleep(WAIT)],
        ..Script::sse(Vec::new())
    })
    .await;
    let l = Limits {
        max_in_flight: 1,
        ..Limits::default()
    };
    let money = Arc::new(Money::default());
    let relay = relay(&upstream, UpstreamProtocol::Chat, l.clone(), money.clone());
    let mut s = relay
        .start(request(ClientProtocol::Chat, true, &l), context())
        .unwrap();
    timeout(WAIT, &mut s.head).await.unwrap().unwrap().unwrap();
    assert!(matches!(
        relay.start(request(ClientProtocol::Chat, true, &l), context()),
        Err(RelayError::Busy)
    ));
    let drained = relay.drain(Duration::ZERO).await;
    assert_eq!(
        drained,
        DrainReport {
            cancelled: 1,
            remaining: 0
        }
    );
    assert!(matches!(
        relay.start(request(ClientProtocol::Chat, true, &l), context()),
        Err(RelayError::Draining)
    ));
    let done = timeout(WAIT, s.completion).await.unwrap().unwrap();
    assert_eq!(done.error, Some(RelayError::Cancelled));
    assert_eq!(money.finals.lock().unwrap().len(), 1);
    assert_eq!(relay.active_requests(), 0);
}

#[tokio::test]
async fn rejected_billing_never_opens_upstream_and_never_changes_readiness() {
    let upstream = Upstream::start(Script::json(full(UpstreamProtocol::Chat))).await;
    let l = Limits::default();
    let money = Arc::new(Money {
        reject: true,
        ..Money::default()
    });
    let relay = relay(&upstream, UpstreamProtocol::Chat, l.clone(), money.clone());
    let (head, body, done) = collect(
        relay
            .start(request(ClientProtocol::Chat, false, &l), context())
            .unwrap(),
    )
    .await;
    assert_eq!(head.unwrap_err(), RelayError::BillingUnavailable);
    assert!(body.is_empty());
    assert_eq!(upstream.observed.hits.load(Ordering::SeqCst), 0);
    assert!(money.finals.lock().unwrap().is_empty());
    assert!(!done.report.upstream_attempted);
    // The module itself neither registers endpoints nor touches core readiness.
    let public_source = include_str!("../src/http.rs");
    assert!(public_source.contains("SERVICE_UNAVAILABLE"));
}

#[tokio::test]
async fn settlement_failure_suppresses_success_for_both_clients_and_modes() {
    for client in [ClientProtocol::Chat, ClientProtocol::Responses] {
        for stream in [false, true] {
            let upstream = Upstream::start(if stream {
                Script::sse(streaming(UpstreamProtocol::Chat))
            } else {
                Script::json(full(UpstreamProtocol::Chat))
            })
            .await;
            let l = Limits::default();
            let money = Arc::new(Money {
                fail_finalize: true,
                ..Money::default()
            });
            let relay = relay(&upstream, UpstreamProtocol::Chat, l.clone(), money.clone());
            let (head, body, done) =
                collect(relay.start(request(client, stream, &l), context()).unwrap()).await;
            assert_eq!(done.error, Some(RelayError::SettlementPending));
            assert_eq!(done.settlement, Settlement::Pending);
            assert!(!body.contains("[DONE]"));
            assert!(!body.contains("response.completed"));
            assert_eq!(money.finals.lock().unwrap().len(), 1);
            if stream {
                assert!(head.is_ok());
                assert!(body.contains("settlement_pending"));
            } else {
                assert_eq!(head.unwrap_err(), RelayError::SettlementPending);
                assert!(body.is_empty());
            }
        }
    }
}

#[tokio::test]
async fn non_stream_head_waits_for_settlement_and_hooks_have_a_deadline() {
    let gate = Arc::new(Semaphore::new(0));
    let upstream = Upstream::start(Script::json(full(UpstreamProtocol::Chat))).await;
    let l = Limits {
        hook_timeout: Duration::from_millis(250),
        ..Limits::default()
    };
    let money = Arc::new(Money {
        finalize_wait: Some(gate),
        ..Money::default()
    });
    let relay = relay(&upstream, UpstreamProtocol::Chat, l.clone(), money.clone());
    let mut session = relay
        .start(request(ClientProtocol::Chat, false, &l), context())
        .unwrap();
    timeout(WAIT, async {
        while money.finals.lock().unwrap().is_empty() {
            sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .unwrap();
    assert!(matches!(
        session.head.try_recv(),
        Err(tokio::sync::oneshot::error::TryRecvError::Empty)
    ));
    let (head, body, done) = collect(session).await;
    assert_eq!(head.unwrap_err(), RelayError::SettlementPending);
    assert!(body.is_empty());
    assert_eq!(done.settlement, Settlement::Pending);
}

#[tokio::test]
async fn reservation_timeout_is_ambiguous_not_a_free_request() {
    let upstream = Upstream::start(Script::json(full(UpstreamProtocol::Chat))).await;
    let l = Limits {
        hook_timeout: Duration::from_millis(150),
        ..Limits::default()
    };
    let money = Arc::new(Money {
        reserve_wait: Some(Arc::new(Semaphore::new(0))),
        ..Money::default()
    });
    let relay = relay(&upstream, UpstreamProtocol::Chat, l.clone(), money.clone());
    let (head, _, done) = collect(
        relay
            .start(request(ClientProtocol::Chat, false, &l), context())
            .unwrap(),
    )
    .await;
    assert_eq!(head.unwrap_err(), RelayError::Timeout(Phase::Reserve));
    assert_eq!(done.settlement, Settlement::Pending);
    assert_eq!(upstream.observed.hits.load(Ordering::SeqCst), 0);
    assert!(money.finals.lock().unwrap().is_empty());
}

#[tokio::test]
async fn http_statuses_and_redirects_are_never_retried_or_leaked() {
    for status in [302, 401, 429, 500, 503] {
        let upstream = Upstream::start(Script {
            status,
            ..Script::json(json!({"error":"secret-upstream-body"}))
        })
        .await;
        let l = Limits {
            connect_retries: 2,
            ..Limits::default()
        };
        let money = Arc::new(Money::default());
        let relay = relay(&upstream, UpstreamProtocol::Chat, l.clone(), money.clone());
        let (head, body, done) = collect(
            relay
                .start(request(ClientProtocol::Chat, false, &l), context())
                .unwrap(),
        )
        .await;
        let e = head.unwrap_err();
        assert_eq!(e, RelayError::UpstreamStatus(status));
        assert!(!e.to_string().contains("secret"));
        assert!(body.is_empty());
        assert_eq!(done.report.attempts, 1);
        assert_eq!(upstream.observed.hits.load(Ordering::SeqCst), 1);
        assert_eq!(money.finals.lock().unwrap().len(), 1);
    }
}

#[tokio::test]
async fn connect_retries_are_limited_to_the_configured_number() {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = format!("http://{}", listener.local_addr().unwrap());
    drop(listener);
    let l = Limits {
        connect_retries: 2,
        retry_backoff: Duration::from_millis(20),
        ..Limits::default()
    };
    let money = Arc::new(Money::default());
    let relay = Relay::new(
        Arc::new(Routes {
            address,
            protocol: UpstreamProtocol::Chat,
        }),
        money.clone(),
        l.clone(),
    )
    .unwrap();
    let (_, _, done) = collect(
        relay
            .start(request(ClientProtocol::Chat, false, &l), context())
            .unwrap(),
    )
    .await;
    assert_eq!(done.error, Some(RelayError::UpstreamConnection));
    assert_eq!(done.report.attempts, 3);
    assert_eq!(money.finals.lock().unwrap().len(), 1);
}

#[tokio::test]
async fn header_read_and_total_deadlines_are_distinct() {
    let cases = [
        (
            Phase::Headers,
            Script {
                header_delay: Duration::from_secs(2),
                ..Script::sse(Vec::new())
            },
            Limits {
                header_timeout: Duration::from_millis(150),
                ..Limits::default()
            },
        ),
        (
            Phase::Read,
            Script {
                steps: vec![
                    Step::Data(first_text("live")),
                    Step::Sleep(Duration::from_secs(2)),
                ],
                ..Script::sse(Vec::new())
            },
            Limits {
                read_timeout: Duration::from_millis(150),
                ..Limits::default()
            },
        ),
        (
            Phase::Total,
            Script {
                steps: (0..20)
                    .flat_map(|_| {
                        [
                            Step::Data(b": heartbeat\n\n".to_vec()),
                            Step::Sleep(Duration::from_millis(50)),
                        ]
                    })
                    .collect(),
                ..Script::sse(Vec::new())
            },
            Limits {
                total_timeout: Duration::from_millis(250),
                read_timeout: Duration::from_millis(200),
                ..Limits::default()
            },
        ),
    ];
    for (phase, script, l) in cases {
        let upstream = Upstream::start(script).await;
        let money = Arc::new(Money::default());
        let relay = relay(&upstream, UpstreamProtocol::Chat, l.clone(), money.clone());
        let (_, body, done) = collect(
            relay
                .start(request(ClientProtocol::Chat, true, &l), context())
                .unwrap(),
        )
        .await;
        assert_eq!(done.error, Some(RelayError::Timeout(phase)));
        assert_eq!(done.report.outcome, Outcome::TimedOut);
        assert_eq!(done.report.attempts, 1);
        assert!(!body.contains("[DONE]"));
        assert_eq!(money.finals.lock().unwrap().len(), 1);
    }
}

#[tokio::test]
async fn a_slow_client_cannot_create_an_unbounded_queue() {
    let upstream = Upstream::start(Script::sse(streaming(UpstreamProtocol::Chat))).await;
    let l = Limits {
        queue_chunks: 1,
        chunk_bytes: 32,
        write_timeout: Duration::from_millis(150),
        ..Limits::default()
    };
    let money = Arc::new(Money::default());
    let relay = relay(&upstream, UpstreamProtocol::Chat, l.clone(), money.clone());
    let mut session = relay
        .start(request(ClientProtocol::Chat, true, &l), context())
        .unwrap();
    timeout(WAIT, &mut session.head)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    // Deliberately retain, but do not poll, the body until the worker terminates.
    let done = timeout(WAIT, &mut session.completion)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(done.error, Some(RelayError::Timeout(Phase::Write)));
    assert!(done.report.emitted_bytes <= 32);
    let mut count = 0;
    while let Some(chunk) = session.body.next_chunk().await {
        assert!(chunk.unwrap().len() <= 32);
        count += 1;
    }
    assert_eq!(count, 1);
    assert_eq!(relay.drain(Duration::ZERO).await.remaining, 0);
    assert_eq!(money.finals.lock().unwrap().len(), 1);
}

#[tokio::test]
async fn event_output_tool_and_raw_body_limits_are_enforced() {
    let base = Limits {
        output_bytes: 2048,
        event_bytes: 1024,
        tool_argument_bytes: 64,
        ..Limits::default()
    };
    let mut argument = full(UpstreamProtocol::Chat);
    argument["choices"][0]["message"]["tool_calls"][0]["function"]["arguments"] =
        json!({"city":"x".repeat(100)}).to_string().into();
    let cases = [
        (
            Script::sse([b"data: ".as_slice(), vec![b'x'; 2048].as_slice()].concat()),
            true,
            base.clone(),
        ),
        (
            Script::sse(cat((0..30).map(|_| first_text(&"x".repeat(100))).collect())),
            true,
            base.clone(),
        ),
        (Script::json(argument), false, base.clone()),
        (
            Script::json(json!({"padding":"x".repeat(4096)})),
            false,
            base.clone(),
        ),
    ];
    for (script, stream, l) in cases {
        let upstream = Upstream::start(script).await;
        let money = Arc::new(Money::default());
        let relay = relay(&upstream, UpstreamProtocol::Chat, l.clone(), money.clone());
        let (_, body, done) = collect(
            relay
                .start(request(ClientProtocol::Chat, stream, &l), context())
                .unwrap(),
        )
        .await;
        assert!(
            matches!(done.error, Some(RelayError::Limit(_))),
            "{:?}",
            done.error
        );
        assert!(!body.contains("[DONE]"));
        assert_eq!(money.finals.lock().unwrap().len(), 1);
    }
}

#[tokio::test]
async fn missing_terminal_invalid_utf8_and_broken_http_fail_without_replay() {
    let cases = [
        Script::sse(first_text("partial")),
        Script {
            complete: false,
            steps: vec![Step::Data(first_text("partial"))],
            ..Script::sse(Vec::new())
        },
        Script::sse(b"data: \xff\n\n".to_vec()),
        Script {
            content_type: "text/html",
            ..Script::sse(b"secret error page".to_vec())
        },
    ];
    for script in cases {
        let upstream = Upstream::start(script).await;
        let l = Limits {
            connect_retries: 2,
            ..Limits::default()
        };
        let money = Arc::new(Money::default());
        let relay = relay(&upstream, UpstreamProtocol::Chat, l.clone(), money.clone());
        let (_, body, done) = collect(
            relay
                .start(request(ClientProtocol::Chat, true, &l), context())
                .unwrap(),
        )
        .await;
        assert!(done.error.is_some());
        assert_ne!(done.report.outcome, Outcome::Completed);
        assert_eq!(done.report.attempts, 1);
        assert!(!body.contains("[DONE]"));
        assert!(!body.contains("secret error page"));
        assert_eq!(money.finals.lock().unwrap().len(), 1);
    }
}

#[tokio::test]
async fn missing_usage_stays_unknown_and_partial_tools_only_allow_incomplete_finish() {
    for reason in ["length", "tool_calls"] {
        let mut v = full(UpstreamProtocol::Chat);
        v.as_object_mut().unwrap().remove("usage");
        v["choices"][0]["finish_reason"] = json!(reason);
        v["choices"][0]["message"]["tool_calls"][0]["function"]["arguments"] = json!("{\"city\":");
        let upstream = Upstream::start(Script::json(v)).await;
        let l = Limits::default();
        let money = Arc::new(Money::default());
        let relay = relay(&upstream, UpstreamProtocol::Chat, l.clone(), money.clone());
        let (_, body, done) = collect(
            relay
                .start(request(ClientProtocol::Responses, false, &l), context())
                .unwrap(),
        )
        .await;
        assert_eq!(done.report.usage, Usage::default());
        assert_eq!(money.finals.lock().unwrap()[0].usage, Usage::default());
        if reason == "length" {
            assert!(done.error.is_none());
            let v: Value = serde_json::from_str(&body).unwrap();
            assert_eq!(v["status"], "incomplete");
            assert!(v["usage"].is_null());
        } else {
            assert!(matches!(done.error, Some(RelayError::Protocol(_))));
        }
    }
}

#[tokio::test]
async fn immutable_request_history_is_converted_without_losing_tool_results() {
    for p in [
        UpstreamProtocol::Chat,
        UpstreamProtocol::Responses,
        UpstreamProtocol::Anthropic,
        UpstreamProtocol::Gemini,
    ] {
        let upstream = Upstream::start(Script::json(full(p))).await;
        let l = Limits::default();
        let money = Arc::new(Money::default());
        let relay = relay(&upstream, p, l.clone(), money);
        let body = json!({"model":"public-model","messages":[{"role":"system","content":"rules"},{"role":"user","content":"weather?"},
            {"role":"assistant","tool_calls":[{"type":"function","id":"call_old","function":{"name":"weather","arguments":"{\"city\":\"上海\"}"}}]},
            {"role":"tool","tool_call_id":"call_old","content":"sunny"}],"max_tokens":100});
        let r = Request::parse(
            ClientProtocol::Chat,
            &serde_json::to_vec(&body).unwrap(),
            &l,
        )
        .unwrap();
        let (_, _, done) = collect(relay.start(r, context()).unwrap()).await;
        assert!(done.error.is_none(), "{p:?}: {:?}", done.error);
        let observed = upstream.observed.requests.lock().unwrap();
        let v = &observed[0].1;
        match p {
            UpstreamProtocol::Chat => {
                assert_eq!(v["messages"][3]["tool_call_id"], "call_old");
                assert_eq!(v["messages"][3]["content"], "sunny");
            }
            UpstreamProtocol::Responses => {
                assert_eq!(v["input"][3]["call_id"], "call_old");
                assert_eq!(v["input"][3]["output"], "sunny");
            }
            UpstreamProtocol::Anthropic => {
                assert_eq!(v["system"][0]["text"], "rules");
                assert_eq!(v["messages"][2]["content"][0]["tool_use_id"], "call_old");
            }
            UpstreamProtocol::Gemini => {
                assert_eq!(v["systemInstruction"]["parts"][0]["text"], "rules");
                assert_eq!(
                    v["contents"][2]["parts"][0]["functionResponse"]["name"],
                    "weather"
                );
                assert_eq!(
                    v["contents"][2]["parts"][0]["functionResponse"]["response"]["output"],
                    "sunny"
                );
            }
        }
    }
}

#[test]
fn unsupported_or_ambiguous_input_and_invalid_routes_fail_closed() {
    let l = Limits::default();
    for v in [
        json!({"model":"x","messages":[],"n":2}),
        json!({"model":"x","messages":[],"unknown":true}),
        json!({"model":"x","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/x"}}]}]}),
        json!({"model":"x","messages":[{"role":"tool","tool_call_id":"orphan","content":"result"}]}),
        json!({"model":"x","messages":[],"max_tokens":1,"max_completion_tokens":2}),
        json!({"model":"x","messages":[],"store":true}),
    ] {
        assert!(
            Request::parse(ClientProtocol::Chat, &serde_json::to_vec(&v).unwrap(), &l).is_err()
        );
    }
    for url in [
        "file:///tmp/key",
        "http://example.com/v1",
        "https://user:password@example.com/v1",
        "https://example.com/v1?key=secret",
        "https://example.com/v1#fragment",
    ] {
        assert!(
            Route::new(
                "r".into(),
                UpstreamProtocol::Chat,
                url,
                "m".into(),
                "test",
                1
            )
            .is_err()
        );
    }
    let route = Route::new(
        "r".into(),
        UpstreamProtocol::Chat,
        "https://example.com/v1",
        "m".into(),
        "hidden-key",
        1,
    )
    .unwrap();
    assert!(!format!("{route:?}").contains("hidden-key"));
    assert!(!format!("{route:?}").contains("example.com"));
}

struct Process(Child);
impl Drop for Process {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}
#[tokio::test]
async fn stopping_the_actual_go_extension_does_not_cut_a_rust_http_stream() {
    let binary = std::env::var("LMM_RELAY_EXTENSION_BIN").expect("build apps/api-go/cmd/extensions and set LMM_RELAY_EXTENSION_BIN; this acceptance test must not be skipped");
    let dir = std::env::temp_dir().join(format!(
        "lmm-relay-go-{}-{}",
        std::process::id(),
        IDS.fetch_add(1, Ordering::Relaxed)
    ));
    std::fs::create_dir_all(&dir).unwrap();
    let token = dir.join("extension-token");
    std::fs::write(&token, b"relay-test-only-credential-0123456789abcdef").unwrap();
    let port = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let address = port.local_addr().unwrap();
    drop(port);
    let mut command = Command::new(binary);
    command
        .env("LMM_EXTENSION_TOKEN_FILE", &token)
        .env("LMM_EXTENSION_LISTEN", address.to_string())
        .env_remove("LMM_CORE_RPC_SOCKET")
        .env_remove("LMM_CORE_RPC_TOKEN_FILE")
        .env_remove("LMM_EXTENSION_MODULES")
        .stdout(Stdio::null())
        .stderr(Stdio::inherit());
    for name in [
        "SQL_DSN",
        "LOG_SQL_DSN",
        "DATABASE_URL",
        "LMM_CORE_DATABASE_URL",
        "LMM_CORE_DATABASE_URL_FILE",
        "LMM_DB_MIGRATION_MODE",
    ] {
        command.env_remove(name);
    }
    let mut process = Process(command.spawn().unwrap());
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(2))
        .build()
        .unwrap();
    timeout(WAIT, async {
        loop {
            assert!(
                process.0.try_wait().unwrap().is_none(),
                "the real Go extension exited before the test"
            );
            if client
                .get(format!("http://{address}/healthz"))
                .send()
                .await
                .is_ok()
            {
                break;
            }
            sleep(Duration::from_millis(25)).await;
        }
    })
    .await
    .unwrap();
    let gate = Arc::new(Semaphore::new(0));
    let upstream = Upstream::start(Script {
        steps: vec![
            Step::Data(first_text("before_go_stop")),
            Step::Wait(gate.clone()),
            Step::Data(final_text("after_go_stop")),
        ],
        ..Script::sse(Vec::new())
    })
    .await;
    let l = Limits::default();
    let money = Arc::new(Money::default());
    let relay = relay(&upstream, UpstreamProtocol::Chat, l.clone(), money.clone());
    let mut front = Frontend::start(relay.clone(), ClientProtocol::Chat, l).await;
    let mut response = reqwest::Client::new()
        .post(&front.url)
        .json(&input(ClientProtocol::Chat, true))
        .send()
        .await
        .unwrap();
    let mut seen = Vec::new();
    until(&mut response, &mut seen, "before_go_stop").await;
    assert_eq!(relay.active_requests(), 1);
    let pid = process.0.id();
    process.0.kill().unwrap();
    assert!(!process.0.wait().unwrap().success());
    assert_eq!(relay.active_requests(), 1);
    gate.add_permits(1);
    while let Some(bytes) = timeout(WAIT, response.chunk()).await.unwrap().unwrap() {
        seen.extend(bytes);
    }
    let done = timeout(WAIT, front.reports.recv()).await.unwrap().unwrap();
    let text = String::from_utf8(seen).unwrap();
    assert!(text.contains("after_go_stop"));
    assert!(text.ends_with("data: [DONE]\n\n"));
    assert!(done.error.is_none());
    assert_eq!(done.report.usage.input_tokens, Some(2));
    assert_eq!(done.report.usage.output_tokens, Some(3));
    assert_eq!(money.finals.lock().unwrap().len(), 1);
    assert_eq!(relay.drain(Duration::ZERO).await.remaining, 0);
    std::fs::remove_dir_all(dir).unwrap();
    println!(
        "actual Go extension pid={pid} killed after first HTTP output; Rust emitted later output and settled once; no Rust restart was performed"
    );
}
