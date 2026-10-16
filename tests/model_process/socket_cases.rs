//! Real product TCP backpressure, isolated from the sustained-load resource gate.
//! Default product output/socket limits remain intact; only this profile's total
//! request/lease windows are extended so the five-second socket write guard wins.
use super::resource_cases::{ResourceHost, sample};
use super::*;
use std::{io::Write, net::SocketAddr};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpSocket, TcpStream},
    task::JoinHandle,
};

const PROMPT: &str = "socket-backpressure";
const DECODED_BYTES: usize = 56 * 16 * 1024;
const OPENING: i64 = 100_000;

struct Evidence(fs::File);
impl Evidence {
    fn new(directory: &Path) -> Self {
        Self(
            fs::OpenOptions::new()
                .write(true)
                .create_new(true)
                .mode(0o600)
                .open(directory.join("socket-observations.jsonl"))
                .unwrap(),
        )
    }
    fn write(&mut self, value: Value) {
        serde_json::to_writer(&mut self.0, &value).unwrap();
        self.0.write_all(b"\n").unwrap();
        self.0.flush().unwrap();
    }
}
struct Sampling {
    task: Option<JoinHandle<Vec<Value>>>,
    stop: Arc<std::sync::atomic::AtomicBool>,
}
impl Sampling {
    async fn finish(mut self) -> Vec<Value> {
        self.stop.store(true, Ordering::SeqCst);
        self.task
            .take()
            .unwrap()
            .await
            .expect("resource sampler failed")
    }
}
impl Drop for Sampling {
    fn drop(&mut self) {
        if let Some(task) = &self.task {
            task.abort();
        }
    }
}

fn request(prompt: &str) -> Value {
    json!({"model":"local-model","messages":[{"role":"user","content":prompt}],
        "max_tokens":20,"stream":true,"stream_options":{"include_usage":true}})
}
fn counters(provider: &Provider) -> Value {
    json!({"hits":provider.hits.load(Ordering::SeqCst),
        "body_produced_bytes":provider.socket_produced.load(Ordering::SeqCst),
        "body_polled_bytes":provider.socket_polled.load(Ordering::SeqCst),
        "body_closed":provider.stream_closed.load(Ordering::SeqCst),
        "producer_finished":provider.stream_finished.load(Ordering::SeqCst)})
}
fn queue(server: SocketAddr, client: SocketAddr) -> Value {
    assert!(server.ip().is_loopback() && client.ip().is_loopback());
    let filter = format!(
        "( src {} and sport = :{} and dst {} and dport = :{} )",
        server.ip(),
        server.port(),
        client.ip(),
        client.port()
    );
    let output = Command::new("/usr/bin/ss")
        .args(["-otinmH", &filter])
        .output()
        .unwrap();
    assert!(output.status.success(), "ss observation failed");
    let text = String::from_utf8(output.stdout).unwrap();
    let first = text
        .lines()
        .next()
        .unwrap_or("")
        .split_whitespace()
        .collect::<Vec<_>>();
    let state = first.first().copied().unwrap_or("absent");
    let present = state == "ESTAB";
    // Closing the application connection may leave FIN-WAIT data in the kernel.
    // Preserve the observed queue for every TCP state, not just ESTAB.
    let send_queue = if first.len() >= 3 {
        first[2].parse::<u64>().unwrap()
    } else {
        0
    };
    let number = |key: &str| -> Option<u64> {
        text.split_whitespace().find_map(|field| {
            field
                .strip_prefix(key)
                .and_then(|number| number.parse().ok())
        })
    };
    json!({"server":server,"client":client,"state":state,"established":present,"send_queue":send_queue,
        "bytes_acked":number("bytes_acked:"),"snd_wnd":number("snd_wnd:"),
        "notsent":number("notsent:"),"ss":text})
}
struct Raw {
    stream: TcpStream,
    server: SocketAddr,
    client: SocketAddr,
    request_id: String,
}
async fn raw(host: &ResourceHost, key: &str, label: &str, evidence: &mut Evidence) -> Raw {
    let server: SocketAddr = host
        .prepared
        .endpoint
        .strip_prefix("http://")
        .unwrap()
        .parse()
        .unwrap();
    let socket = TcpSocket::new_v4().unwrap();
    socket.set_recv_buffer_size(4096).unwrap();
    let actual_receive_buffer = socket.recv_buffer_size().unwrap();
    evidence.write(
        json!({"stage":"pre-connect","case":label,"requested_rcvbuf":4096,
        "actual_rcvbuf":actual_receive_buffer,"server":server}),
    );
    assert_eq!(
        actual_receive_buffer, 8192,
        "Linux receive buffer readback must match the fixed profile"
    );
    let mut stream = tokio::time::timeout(Duration::from_secs(2), socket.connect(server))
        .await
        .unwrap()
        .unwrap();
    let client = stream.local_addr().unwrap();
    let body = serde_json::to_vec(&request(PROMPT)).unwrap();
    let headers = format!(
        "POST /v1/chat/completions HTTP/1.1\r\nHost: {server}\r\nAuthorization: Bearer {key}\r\nIdempotency-Key: socket-{label}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
        body.len()
    );
    tokio::time::timeout(Duration::from_secs(3), async {
        stream.write_all(headers.as_bytes()).await.unwrap();
        stream.write_all(&body).await.unwrap();
    })
    .await
    .unwrap();
    let mut header = Vec::new();
    tokio::time::timeout(Duration::from_secs(5), async {
        while !header.ends_with(b"\r\n\r\n") {
            header.push(stream.read_u8().await.unwrap());
            assert!(header.len() <= 16_384);
        }
    })
    .await
    .unwrap();
    let header = String::from_utf8(header).unwrap();
    assert!(header.starts_with("HTTP/1.1 200 "));
    assert!(
        header
            .lines()
            .any(|line| line.eq_ignore_ascii_case("transfer-encoding: chunked"))
    );
    let request_id = header
        .lines()
        .find_map(|line| {
            let (name, value) = line.split_once(':')?;
            name.eq_ignore_ascii_case("x-request-id")
                .then(|| value.trim().to_owned())
        })
        .unwrap();
    assert!(request_id.len() == 64 && request_id.bytes().all(|byte| byte.is_ascii_hexdigit()));
    evidence.write(json!({"stage":"head","case":label,"request_id":request_id,"server":server,"client":client}));
    Raw {
        stream,
        server,
        client,
        request_id,
    }
}
async fn blocked(
    pool: &PgPool,
    raw: &Raw,
    provider: &Provider,
    evidence: &mut Evidence,
    label: &str,
) -> Instant {
    let start = Instant::now();
    let mut plateau: Option<(u64, i64, Instant)> = None;
    loop {
        let observation = queue(raw.server, raw.client);
        let (content_checkpoints, terminal): (i64,bool) = sqlx::query_as(
            "SELECT count(*) FILTER(WHERE packet_bytes>16384),COALESCE(bool_or(terminal_observed),false) FROM core_model.output_checkpoints WHERE request_id=$1")
            .bind(&raw.request_id).fetch_one(pool).await.unwrap();
        evidence.write(json!({"stage":"stall-search","case":label,"elapsed_ms":start.elapsed().as_millis(),
            "queue":observation,"provider":counters(provider),"content_checkpoints":content_checkpoints,"terminal_observed":terminal}));
        assert!(
            observation["established"] == true,
            "connection ended without observed backpressure"
        );
        // iproute2 v7.2.0 misc/ss.c omits snd_wnd when it is zero.
        // A kernel persist timer proves zero-window probing; missing snd_wnd alone does not.
        // https://github.com/iproute2/iproute2/blob/v7.2.0/misc/ss.c
        let stalled = observation["send_queue"].as_u64().unwrap() > 0
            && observation["ss"]
                .as_str()
                .unwrap()
                .contains("timer:(persist,")
            && content_checkpoints > 0
            && content_checkpoints < 56
            && !terminal;
        if stalled {
            let acked = observation["bytes_acked"]
                .as_u64()
                .expect("ss lacks bytes_acked");
            match plateau {
                Some((old, old_checkpoints, since))
                    if old == acked && old_checkpoints == content_checkpoints =>
                {
                    if since.elapsed() >= Duration::from_millis(500) {
                        return since;
                    }
                }
                _ => plateau = Some((acked, content_checkpoints, Instant::now())),
            }
        } else {
            plateau = None;
        }
        assert!(
            start.elapsed() < Duration::from_secs(4),
            "no true zero-window Send-Q stall within fixed output cap"
        );
        tokio::time::sleep(Duration::from_millis(100)).await;
    }
}
async fn unresolved(pool: &PgPool, account: i64, id: &str, evidence: &mut Evidence, label: &str) {
    let row: Value = sqlx::query_scalar("SELECT jsonb_build_object('state',r.state,'payer',r.payer_account_id,'reserved',r.reserved,'settled',r.settled,'hold',h.reserved,'captured',h.captured,'closed',h.closed,'capture_journal',h.capture_journal_id) FROM core_charging.requests r JOIN core_charging.ledger_holds h ON h.request_id=r.id WHERE r.id=$1")
        .bind(id).fetch_one(pool).await.unwrap();
    let checkpoints: Value = sqlx::query_scalar("SELECT jsonb_build_object('count',count(*),'terminal',COALESCE(bool_or(terminal_observed),false),'provider_final',(SELECT count(*) FROM core_charging.usage_events WHERE request_id=$1 AND event_id='usage:provider-final')) FROM core_model.output_checkpoints WHERE request_id=$1")
        .bind(id).fetch_one(pool).await.unwrap();
    evidence.write(
        json!({"stage":"funds","case":label,"request_id":id,"row":row,"checkpoints":checkpoints}),
    );
    assert!(checkpoints["count"].as_u64().unwrap() > 0);
    assert_eq!(checkpoints["terminal"], false);
    assert_eq!(checkpoints["provider_final"], 0);
    assert_eq!(row["payer"], account);
    assert_eq!(row["reserved"], 140);
    assert_eq!(row["hold"], 140);
    assert_eq!(row["settled"], 0);
    assert_eq!(row["captured"], 0);
    assert_eq!(row["closed"], false);
    assert_eq!(row["capture_journal"], Value::Null);
    assert!(["streaming", "reconcile"].contains(&row["state"].as_str().unwrap()));
}
async fn healthy(
    pool: &PgPool,
    host: &ResourceHost,
    seed: &Seed,
    client: &reqwest::Client,
    prompt: &str,
    label: &str,
) -> String {
    let mut response = client
        .post(format!("{}/v1/chat/completions", host.prepared.endpoint))
        .bearer_auth(&seed.key)
        .header("idempotency-key", format!("socket-{label}"))
        .json(&request(prompt))
        .send()
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let id = response.headers()["x-request-id"]
        .to_str()
        .unwrap()
        .to_owned();
    let mut bytes = Vec::new();
    while let Some(chunk) = response.chunk().await.unwrap() {
        bytes.extend(chunk);
        assert!(bytes.len() < 8 * 1024 * 1024);
    }
    validate_output(bytes, prompt);
    super::paid(pool, seed.account, &id).await;
    id
}

fn validate_output(bytes: Vec<u8>, prompt: &str) {
    let text = String::from_utf8(bytes).unwrap();
    assert!(text.ends_with("data: [DONE]\n\n"));
    let mut content = String::new();
    let mut usage = false;
    let mut frames = text.split("\n\n").collect::<Vec<_>>();
    assert_eq!(frames.pop(), Some(""));
    assert_eq!(frames.pop(), Some("data: [DONE]"));
    for frame in frames {
        let value: Value =
            serde_json::from_str(frame.strip_prefix("data: ").expect("unexpected SSE frame"))
                .unwrap();
        assert!(
            value.get("error").is_none(),
            "error SSE is not successful completion"
        );
        if let Some(delta) = value["choices"][0]["delta"]["content"].as_str() {
            content.push_str(delta);
        }
        if value["usage"]["prompt_tokens"] == 10
            && value["usage"]["completion_tokens"] == 20
            && value["usage"]["prompt_tokens_details"]["cached_tokens"] == 2
        {
            usage = true;
        }
    }
    assert!(usage);
    if prompt == PROMPT {
        assert_eq!(content.len(), DECODED_BYTES);
        assert!(content.bytes().all(|byte| byte == 1));
    } else {
        assert_eq!(content, "local stream answer");
    }
}

async fn line(stream: &mut TcpStream) -> Vec<u8> {
    let mut line = Vec::new();
    while !line.ends_with(b"\r\n") {
        line.push(
            stream
                .read_u8()
                .await
                .expect("truncated HTTP chunk framing"),
        );
        assert!(line.len() <= 1024);
    }
    line.truncate(line.len() - 2);
    line
}
async fn resume(mut raw: Raw, pool: &PgPool, account: i64, evidence: &mut Evidence) -> String {
    let started = Instant::now();
    let bytes = tokio::time::timeout(Duration::from_secs(30), async {
        let mut bytes = Vec::new();
        loop {
            let header = String::from_utf8(line(&mut raw.stream).await).unwrap();
            assert!(!header.is_empty() && header.bytes().all(|byte| byte.is_ascii_hexdigit()));
            let size = usize::from_str_radix(&header, 16).unwrap();
            if size == 0 {
                assert!(
                    line(&mut raw.stream).await.is_empty(),
                    "unexpected trailers"
                );
                break;
            }
            assert!(size <= 8 * 1024 * 1024 && bytes.len() + size < 8 * 1024 * 1024);
            let offset = bytes.len();
            bytes.resize(offset + size, 0);
            raw.stream
                .read_exact(&mut bytes[offset..])
                .await
                .expect("truncated HTTP chunk body");
            assert!(
                line(&mut raw.stream).await.is_empty(),
                "missing HTTP chunk CRLF"
            );
        }
        bytes
    })
    .await
    .expect("stalled stream did not complete after resuming reads");
    evidence.write(
        json!({"stage":"resumed-body","request_id":raw.request_id,"wire_bytes":bytes.len(),
        "resume_ms":started.elapsed().as_millis()}),
    );
    validate_output(bytes, PROMPT);
    super::paid(pool, account, &raw.request_id).await;
    raw.request_id
}
fn exits(provider: &Provider) -> (usize, usize) {
    (
        provider.stream_closed.load(Ordering::SeqCst),
        provider.stream_finished.load(Ordering::SeqCst),
    )
}
async fn exited(
    provider: &Provider,
    before: (usize, usize),
    started: Instant,
    evidence: &mut Evidence,
    label: &str,
) {
    loop {
        let after = exits(provider);
        evidence.write(
            json!({"stage":"upstream-exit","case":label,"elapsed_ms":started.elapsed().as_millis(),
            "before":before,"after":after,"provider":counters(provider)}),
        );
        if after.0 > before.0 && after.1 > before.1 {
            break;
        }
        assert!(
            started.elapsed() < Duration::from_secs(2),
            "upstream body/task did not exit within two seconds"
        );
        tokio::time::sleep(Duration::from_millis(25)).await;
    }
    assert!(started.elapsed() < Duration::from_secs(2));
}
async fn replay(
    pool: &PgPool,
    host: &ResourceHost,
    seed: &Seed,
    client: &reqwest::Client,
    provider: &Provider,
    case: (&str, &str),
    evidence: &mut Evidence,
) {
    let (id, label) = case;
    let hits = provider.hits.load(Ordering::SeqCst);
    let response = client
        .post(format!("{}/v1/chat/completions", host.prepared.endpoint))
        .bearer_auth(&seed.key)
        .header("idempotency-key", format!("socket-{label}"))
        .json(&request(PROMPT))
        .timeout(Duration::from_secs(2))
        .send()
        .await
        .unwrap();
    let status = response.status();
    let replay_id = response.headers()["x-request-id"]
        .to_str()
        .unwrap()
        .to_owned();
    let value: Value = response.json().await.unwrap();
    evidence.write(json!({"stage":"unknown-replay","case":label,"request_id":replay_id,"status":status.as_u16(),"error":value["error"]["code"]}));
    assert_eq!(status, StatusCode::CONFLICT);
    assert_eq!(replay_id, id);
    assert_eq!(value["error"]["code"], "request_conflict");
    assert_eq!(provider.hits.load(Ordering::SeqCst), hits);
    unresolved(pool, seed.account, id, evidence, label).await;
}

// @mark model.socket-backpressure role=test
#[sqlx::test(migrations = false)]
#[ignore = "Requires frozen resource runner, cgroup v2, ss and fresh owned PostgreSQL"]
async fn independent_process_socket_backpressure(pool: PgPool) {
    assert_eq!(std::env::var("LMM_RESOURCE_ACCEPTANCE").as_deref(), Ok("1"));
    let seed = seed(&pool, OPENING).await;
    let provider = Provider::start(true).await;
    let prepared = prepare_core(&pool, &provider, &[seed.key_id], "socket").await;
    let model_path = prepared.directory.join("model.toml");
    let model = fs::read_to_string(&model_path).unwrap();
    assert_eq!(model.matches("lease_seconds=20").count(), 1);
    assert_eq!(model.matches("timeout_seconds=3").count(), 1);
    let model = model
        .replace("lease_seconds=20", "lease_seconds=60")
        .replace("timeout_seconds=3", "timeout_seconds=30")
        .replace("timeout_ms=5000", "timeout_ms=30000");
    // Only a newly created private fixture config is changed; product limits stay default.
    fs::write(&model_path, model).unwrap();
    let client = reqwest::Client::builder()
        .no_proxy()
        .timeout(Duration::from_secs(35))
        .build()
        .unwrap();
    let mut host = ResourceHost::start(prepared, &client).await;
    let mut evidence = Evidence::new(&host.prepared.directory);
    let pid = host.pid;
    let group = host.cgroup.clone();
    let directory = host.prepared.directory.clone();
    let started = Instant::now();
    let stop = Arc::new(std::sync::atomic::AtomicBool::new(false));
    let stop_flag = stop.clone();
    let ticks = process_start_ticks(pid).unwrap();
    let sampling = Sampling {
        stop,
        task: Some(tokio::spawn(async move {
            let mut file = fs::OpenOptions::new()
                .write(true)
                .create_new(true)
                .mode(0o600)
                .open(directory.join("socket-resource-samples.jsonl"))
                .unwrap();
            let mut rows = Vec::new();
            loop {
                assert_eq!(process_start_ticks(pid).unwrap(), ticks);
                let observation = sample(pid, &group, started, 0);
                serde_json::to_writer(&mut file, &observation).unwrap();
                file.write_all(b"\n").unwrap();
                file.flush().unwrap();
                for event in ["max", "oom", "oom_kill"] {
                    assert_eq!(observation["memory_events"][event], 0);
                }
                assert!(observation["memory_peak"].as_u64().unwrap() <= 256 * 1024 * 1024);
                rows.push(observation);
                if stop_flag.load(Ordering::SeqCst) {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(200)).await;
            }
            rows
        })),
    };
    evidence.write(
        json!({"stage":"profile","default_limits":true,"total_seconds":30,"lease_seconds":60,
        "decoded_bytes":DECODED_BYTES,"cpu":1,"core_memory_bytes":256*1024*1024,
        "database_and_generator_outside_core_cgroup":true,"provider":counters(&provider)}),
    );
    let baseline = healthy(&pool, &host, &seed, &client, PROMPT, "healthy-large").await;
    evidence.write(json!({"stage":"healthy-large-complete","request_id":baseline,"provider":counters(&provider)}));
    let baseline_fds = fs::read_dir(format!("/proc/{}/fd", host.pid))
        .unwrap()
        .count();
    let resumable = raw(&host, &seed.key, "resume", &mut evidence).await;
    let _ = blocked(&pool, &resumable, &provider, &mut evidence, "resume").await;
    let resumed = resume(resumable, &pool, seed.account, &mut evidence).await;
    let timeout_exits = exits(&provider);
    let stalled = raw(&host, &seed.key, "write-timeout", &mut evidence).await;
    let stall_started = blocked(&pool, &stalled, &provider, &mut evidence, "write-timeout").await;
    loop {
        host.alive();
        let observation = queue(stalled.server, stalled.client);
        evidence.write(json!({"stage":"wait-write-guard","elapsed_since_stall_ms":stall_started.elapsed().as_millis(),
            "queue":observation,"provider":counters(&provider)}));
        if observation["established"] == false {
            break;
        }
        assert!(
            stall_started.elapsed() < Duration::from_secs(8),
            "true blocked socket did not close within write guard bound"
        );
        tokio::time::sleep(Duration::from_millis(100)).await;
    }
    assert!(
        stall_started.elapsed() >= Duration::from_secs(4),
        "closed before default five-second socket guard"
    );
    let closed_at = Instant::now();
    exited(
        &provider,
        timeout_exits,
        closed_at,
        &mut evidence,
        "write-timeout",
    )
    .await;
    unresolved(
        &pool,
        seed.account,
        &stalled.request_id,
        &mut evidence,
        "write-timeout",
    )
    .await;
    let timeout_id = stalled.request_id.clone();
    drop(stalled);
    replay(
        &pool,
        &host,
        &seed,
        &client,
        &provider,
        (&timeout_id, "write-timeout"),
        &mut evidence,
    )
    .await;
    let recovery1 = healthy(&pool, &host, &seed, &client, "hello", "after-write-timeout").await;
    let disconnect_exits = exits(&provider);
    let disconnected = raw(&host, &seed.key, "disconnect", &mut evidence).await;
    let _ = blocked(&pool, &disconnected, &provider, &mut evidence, "disconnect").await;
    let id = disconnected.request_id.clone();
    let server = disconnected.server;
    let peer = disconnected.client;
    let disconnect_started = Instant::now();
    drop(disconnected);
    loop {
        let observation = queue(server, peer);
        evidence.write(
            json!({"stage":"wait-disconnect","elapsed_ms":disconnect_started.elapsed().as_millis(),
            "queue":observation,"provider":counters(&provider)}),
        );
        if observation["established"] == false {
            break;
        }
        assert!(disconnect_started.elapsed() < Duration::from_secs(2));
        tokio::time::sleep(Duration::from_millis(25)).await;
    }
    exited(
        &provider,
        disconnect_exits,
        disconnect_started,
        &mut evidence,
        "disconnect",
    )
    .await;
    unresolved(&pool, seed.account, &id, &mut evidence, "disconnect").await;
    replay(
        &pool,
        &host,
        &seed,
        &client,
        &provider,
        (&id, "disconnect"),
        &mut evidence,
    )
    .await;
    let recovery2 = healthy(&pool, &host, &seed, &client, "hello", "after-disconnect").await;
    assert_eq!(
        provider.hits.load(Ordering::SeqCst),
        6,
        "no provider replay is allowed"
    );
    balance(
        &seed.ledger,
        seed.account,
        OPENING - 4 * 49 - 2 * 140,
        2 * 140,
    )
    .await;
    balanced(&pool).await;
    let idle_started = Instant::now();
    loop {
        host.alive();
        let fds = fs::read_dir(format!("/proc/{}/fd", host.pid))
            .unwrap()
            .count();
        let runtime_connections: i64 =
            sqlx::query_scalar("SELECT count(*) FROM pg_stat_activity WHERE usename=$1")
                .bind(&host.prepared.role.as_ref().unwrap().name)
                .fetch_one(&pool)
                .await
                .unwrap();
        evidence.write(
            json!({"stage":"idle-drain","elapsed_ms":idle_started.elapsed().as_millis(),
            "fd_count":fds,"baseline_fds":baseline_fds,"runtime_connections":runtime_connections}),
        );
        assert!(runtime_connections <= 2);
        if idle_started.elapsed() >= Duration::from_secs(2) && fds <= baseline_fds + 1 {
            break;
        }
        assert!(
            idle_started.elapsed() < Duration::from_secs(5),
            "Core socket descriptors did not drain"
        );
        tokio::time::sleep(Duration::from_millis(200)).await;
    }
    let final_sample = sample(host.pid, &host.cgroup, started, 0);
    evidence
        .write(json!({"stage":"final","resources":final_sample,"provider":counters(&provider)}));
    for event in ["max", "oom", "oom_kill"] {
        assert_eq!(final_sample["memory_events"][event], 0);
    }
    let samples = sampling.finish().await;
    assert!(samples.len() >= 10);
    assert!(samples[0]["elapsed_ms"].as_u64().unwrap() <= 500);
    let max_gap = samples
        .windows(2)
        .map(|pair| {
            pair[1]["elapsed_ms"].as_u64().unwrap() - pair[0]["elapsed_ms"].as_u64().unwrap()
        })
        .max()
        .unwrap();
    assert!(max_gap <= 1500, "resource sampling has an unexplained gap");
    assert!(
        started.elapsed().as_millis()
            - samples.last().unwrap()["elapsed_ms"].as_u64().unwrap() as u128
            <= 500
    );
    evidence.write(json!({"stage":"sampling-complete","count":samples.len(),"max_gap_ms":max_gap,"duration_ms":started.elapsed().as_millis()}));
    let actual_pid = host.pid;
    let cgroup = host.cgroup.clone();
    host.finish().await;
    record(
        "socket-backpressure",
        &json!({"status":"PASS","healthy_complete_requests":4,"blocked_unknown_holds":2,
        "normal_ids":[baseline,resumed,recovery1,recovery2],"socket_write_guard_seconds":5,
        "real_send_queue_and_zero_window_observed":true,"actual_core_pid":actual_pid,"cgroup":cgroup,
        "not_verified":["sustained mixed traffic","real provider token accounting","whole node budget"]}),
    );
}
