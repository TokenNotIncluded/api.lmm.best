//! One stalled HTTP/2 stream must not poison its shared, writable TCP connection.
//! This is stream flow-control isolation, not a TCP zero-window experiment.
use super::resource_cases::{ResourceHost, sample};
use super::*;
use std::{io::Write, net::SocketAddr};

const STREAM_WINDOW: u32 = 16 * 1024;
const CONNECTION_WINDOW: u32 = 4 * 1024 * 1024;
const NORMAL_REQUESTS: usize = 12;
const OPENING: i64 = 100_000;

struct Evidence(fs::File);
impl Evidence {
    fn new(directory: &Path) -> Self {
        Self(
            fs::OpenOptions::new()
                .write(true)
                .create_new(true)
                .mode(0o600)
                .open(directory.join("h2-socket-observations.jsonl"))
                .unwrap(),
        )
    }
    fn write(&mut self, value: Value) {
        serde_json::to_writer(&mut self.0, &value).unwrap();
        self.0.write_all(b"\n").unwrap();
        self.0.flush().unwrap();
    }
}
fn request(prompt: &str) -> Value {
    json!({"model":"local-model","messages":[{"role":"user","content":prompt}],
        "max_tokens":20,"stream":true,"stream_options":{"include_usage":true}})
}
fn connection(server: SocketAddr) -> Value {
    assert!(server.ip().is_loopback());
    let filter = format!("( src {} and sport = :{} )", server.ip(), server.port());
    let output = Command::new("/usr/bin/ss")
        .args(["-otinmH", &filter])
        .output()
        .unwrap();
    assert!(output.status.success());
    let text = String::from_utf8(output.stdout).unwrap();
    let rows = text
        .lines()
        .filter_map(|line| {
            let fields = line.split_whitespace().collect::<Vec<_>>();
            (fields.first() == Some(&"ESTAB")).then(|| {
                assert!(fields.len() >= 5);
                assert_eq!(fields[3].parse::<SocketAddr>().unwrap(), server);
                json!({"server":fields[3],"client":fields[4],
                "recv_queue":fields[1].parse::<u64>().unwrap(),
                "send_queue":fields[2].parse::<u64>().unwrap()})
            })
        })
        .collect::<Vec<_>>();
    json!({"established":rows,"ss":text})
}
fn same_connection(server: SocketAddr, peer: &Value, evidence: &mut Evidence, label: &str) {
    let observed = connection(server);
    evidence.write(json!({"stage":"shared-tcp","case":label,"connection":observed}));
    let rows = observed["established"].as_array().unwrap();
    assert_eq!(
        rows.len(),
        1,
        "must retain precisely one shared product TCP connection"
    );
    assert_eq!(
        &rows[0]["client"], peer,
        "HTTP/2 client reconnected instead of sharing TCP"
    );
}
fn provider_exits(provider: &Provider) -> (usize, usize) {
    (
        provider.stream_closed.load(Ordering::SeqCst),
        provider.stream_finished.load(Ordering::SeqCst),
    )
}
async fn all_provider_exited(
    host: &mut ResourceHost,
    provider: &Provider,
    expected: (usize, usize),
    evidence: &mut Evidence,
    label: &str,
) {
    let started = Instant::now();
    loop {
        host.alive();
        let actual = provider_exits(provider);
        evidence.write(json!({"stage":"provider-exit-census","case":label,
            "body_closed":actual.0,"producer_finished":actual.1,
            "expected_body_closed":expected.0,"expected_producer_finished":expected.1,
            "elapsed_ms":started.elapsed().as_millis()}));
        assert!(
            actual.0 <= expected.0 && actual.1 <= expected.1,
            "unexpected provider body or producer beyond dispatched requests"
        );
        if actual == expected {
            break;
        }
        assert!(
            started.elapsed() < Duration::from_secs(3),
            "provider bodies or producer tasks remain after stream cancellation"
        );
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
}
async fn checkpoints(pool: &PgPool, id: &str) -> (i64, bool) {
    sqlx::query_as("SELECT count(*) FILTER(WHERE packet_bytes>16384), COALESCE(bool_or(terminal_observed),false) FROM core_model.output_checkpoints WHERE request_id=$1")
        .bind(id).fetch_one(pool).await.unwrap()
}
async fn unknown(pool: &PgPool, account: i64, id: &str, evidence: &mut Evidence) {
    let row: Value = sqlx::query_scalar("SELECT jsonb_build_object('state',r.state,'payer',r.payer_account_id,'reserved',r.reserved,'settled',r.settled,'hold',h.reserved,'captured',h.captured,'closed',h.closed,'capture_journal',h.capture_journal_id,'provider_final',(SELECT count(*) FROM core_charging.usage_events WHERE request_id=r.id AND event_id='usage:provider-final')) FROM core_charging.requests r JOIN core_charging.ledger_holds h ON h.request_id=r.id WHERE r.id=$1")
        .bind(id).fetch_one(pool).await.unwrap();
    evidence.write(json!({"stage":"slow-funds","request_id":id,"row":row}));
    assert_eq!(row["payer"], account);
    assert_eq!(row["reserved"], 140);
    assert_eq!(row["hold"], 140);
    assert_eq!(row["settled"], 0);
    assert_eq!(row["captured"], 0);
    assert_eq!(row["closed"], false);
    assert_eq!(row["capture_journal"], Value::Null);
    assert_eq!(row["provider_final"], 0);
    assert!(["streaming", "reconcile"].contains(&row["state"].as_str().unwrap()));
}
async fn normal(
    pool: &PgPool,
    host: &ResourceHost,
    seed: &Seed,
    client: &reqwest::Client,
    ordinal: usize,
    shared: (SocketAddr, &Value),
    evidence: &mut Evidence,
) -> String {
    let (server, peer) = shared;
    let started = Instant::now();
    let mut response = tokio::time::timeout(
        Duration::from_secs(2),
        client
            .post(format!("{}/v1/chat/completions", host.prepared.endpoint))
            .bearer_auth(&seed.key)
            .header("idempotency-key", format!("h2-normal-{ordinal}"))
            .json(&request("normal"))
            .send(),
    )
    .await
    .unwrap()
    .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(response.version(), reqwest::Version::HTTP_2);
    assert_eq!(response.remote_addr(), Some(server));
    let id = response.headers()["x-request-id"]
        .to_str()
        .unwrap()
        .to_owned();
    same_connection(server, peer, evidence, "normal-headers");
    let mut bytes = Vec::new();
    tokio::time::timeout(Duration::from_secs(2), async {
        while let Some(chunk) = response.chunk().await.unwrap() {
            bytes.extend_from_slice(&chunk);
            assert!(bytes.len() < 64 * 1024);
        }
    })
    .await
    .unwrap();
    let text = String::from_utf8(bytes).unwrap();
    let mut frames = text.split("\n\n").collect::<Vec<_>>();
    assert_eq!(frames.pop(), Some(""));
    assert_eq!(frames.pop(), Some("data: [DONE]"));
    let mut content = String::new();
    let mut usage = 0;
    for frame in frames {
        let value: Value = serde_json::from_str(frame.strip_prefix("data: ").unwrap()).unwrap();
        assert!(value.get("error").is_none());
        if let Some(delta) = value["choices"][0]["delta"]["content"].as_str() {
            content.push_str(delta);
        }
        if value["usage"].is_object() {
            assert_eq!(value["usage"]["prompt_tokens"], 10);
            assert_eq!(value["usage"]["completion_tokens"], 20);
            assert_eq!(value["usage"]["prompt_tokens_details"]["cached_tokens"], 2);
            usage += 1;
        }
    }
    assert_eq!(usage, 1);
    assert_eq!(content, "local stream answer");
    super::paid(pool, seed.account, &id).await;
    evidence.write(
        json!({"stage":"normal-complete","ordinal":ordinal,"request_id":id,
        "elapsed_ms":started.elapsed().as_millis(),"captured":49,"wallet_surplus":91}),
    );
    id
}

// @mark model.socket-h2 role=test
#[sqlx::test(migrations = false)]
#[ignore = "Requires frozen resource runner, cgroup v2, ss and fresh owned PostgreSQL"]
async fn independent_process_h2_stream_backpressure(pool: PgPool) {
    assert_eq!(std::env::var("LMM_RESOURCE_ACCEPTANCE").as_deref(), Ok("1"));
    let seed = seed(&pool, OPENING).await;
    let provider = Provider::start(true).await;
    let prepared = prepare_core(&pool, &provider, &[seed.key_id], "socket-h2").await;
    let model_path = prepared.directory.join("model.toml");
    let model = fs::read_to_string(&model_path).unwrap();
    assert_eq!(model.matches("lease_seconds=20").count(), 1);
    assert_eq!(model.matches("timeout_seconds=3").count(), 1);
    fs::write(
        &model_path,
        model
            .replace("lease_seconds=20", "lease_seconds=60")
            .replace("timeout_seconds=3", "timeout_seconds=30")
            .replace("timeout_ms=5000", "timeout_ms=30000"),
    )
    .unwrap();
    let health = reqwest::Client::builder()
        .no_proxy()
        .pool_max_idle_per_host(0)
        .timeout(Duration::from_secs(3))
        .build()
        .unwrap();
    let mut host = ResourceHost::start(prepared, &health).await;
    drop(health);
    let mut evidence = Evidence::new(&host.prepared.directory);
    let server: SocketAddr = host
        .prepared
        .endpoint
        .strip_prefix("http://")
        .unwrap()
        .parse()
        .unwrap();
    let client = reqwest::Client::builder()
        .no_proxy()
        .http2_prior_knowledge()
        .http2_initial_stream_window_size(STREAM_WINDOW)
        .http2_initial_connection_window_size(CONNECTION_WINDOW)
        .http2_adaptive_window(false)
        .timeout(Duration::from_secs(35))
        .build()
        .unwrap();
    evidence.write(
        json!({"stage":"profile","cpu":1,"memory_bytes":256*1024*1024,
        "stream_window":STREAM_WINDOW,"connection_window":CONNECTION_WINDOW,"adaptive_window":false,
        "slow_response_body_polled":false,"tcp_zero_window_test":false,
        "database_and_generator_outside_core_cgroup":true}),
    );
    let exit_baseline = provider_exits(&provider);
    evidence.write(
        json!({"stage":"provider-baseline","body_closed":exit_baseline.0,
        "producer_finished":exit_baseline.1}),
    );
    // Keep the entire Response alive and never call chunk/bytes/bytes_stream on it.
    let slow = client
        .post(format!("{}/v1/chat/completions", host.prepared.endpoint))
        .bearer_auth(&seed.key)
        .header("idempotency-key", "h2-slow")
        .json(&request("socket-backpressure"))
        .send()
        .await
        .unwrap();
    assert_eq!(slow.status(), StatusCode::OK);
    assert_eq!(slow.version(), reqwest::Version::HTTP_2);
    assert_eq!(slow.remote_addr(), Some(server));
    let slow_id = slow.headers()["x-request-id"].to_str().unwrap().to_owned();
    let observed = connection(server);
    evidence.write(json!({"stage":"initial-tcp","connection":observed,"request_id":slow_id}));
    let connections = observed["established"].as_array().unwrap();
    assert_eq!(connections.len(), 1);
    let peer = connections[0]["client"].clone();
    let started = Instant::now();
    let mut plateau: Option<(i64, Instant)> = None;
    let stable_checkpoints = loop {
        host.alive();
        let (count, terminal) = checkpoints(&pool, &slow_id).await;
        evidence.write(
            json!({"stage":"flow-control-plateau","count":count,"terminal":terminal,
            "provider_polled_bytes":provider.socket_polled.load(Ordering::SeqCst)}),
        );
        assert!(
            !terminal,
            "client buffering consumed the entire supposedly stalled stream"
        );
        assert!(
            count < 56,
            "all large packets reached checkpoints despite bounded stream window"
        );
        if count > 0 {
            match plateau {
                Some((old, since)) if old == count && since.elapsed() >= Duration::from_secs(1) => {
                    break count;
                }
                Some((old, _)) if old == count => {}
                _ => plateau = Some((count, Instant::now())),
            }
        }
        assert!(
            started.elapsed() < Duration::from_secs(5),
            "no bounded stream checkpoint plateau"
        );
        tokio::time::sleep(Duration::from_millis(100)).await;
    };
    let active = Instant::now();
    let mut completed = Vec::new();
    for ordinal in 0..NORMAL_REQUESTS {
        completed.push(
            normal(
                &pool,
                &host,
                &seed,
                &client,
                ordinal,
                (server, &peer),
                &mut evidence,
            )
            .await,
        );
        same_connection(server, &peer, &mut evidence, "slow-stream-still-held");
        assert_eq!(
            checkpoints(&pool, &slow_id).await,
            (stable_checkpoints, false)
        );
        assert_eq!(provider.hits.load(Ordering::SeqCst), ordinal + 2);
        let exits = provider_exits(&provider);
        evidence.write(
            json!({"stage":"provider-during-shared-traffic","ordinal":ordinal,
            "normal_completed":ordinal+1,"body_closed":exits.0,"producer_finished":exits.1,
            "all_dispatched_bodies_already_closed":exits.0 == exit_baseline.0+ordinal+2,
            "all_dispatched_producers_already_finished":exits.1 == exit_baseline.1+ordinal+2}),
        );
        unknown(&pool, seed.account, &slow_id, &mut evidence).await;
        let resource = sample(host.pid, &host.cgroup, started, 0);
        evidence.write(json!({"stage":"resource","sample":resource}));
        for event in ["max", "oom", "oom_kill"] {
            assert_eq!(resource["memory_events"][event], 0);
        }
        assert!(resource["memory_peak"].as_u64().unwrap() <= 256 * 1024 * 1024);
        tokio::time::sleep(Duration::from_secs(1)).await;
    }
    assert!(
        active.elapsed() >= Duration::from_secs(10),
        "must outlive default five-second TCP write guard twice"
    );
    let expected_exits = (
        exit_baseline.0 + NORMAL_REQUESTS + 1,
        exit_baseline.1 + NORMAL_REQUESTS + 1,
    );
    let before_cancel = provider_exits(&provider);
    evidence.write(
        json!({"stage":"before-slow-stream-cancel","body_closed":before_cancel.0,
        "producer_finished":before_cancel.1,"expected_body_closed":expected_exits.0,
        "expected_producer_finished":expected_exits.1,
        "all_bodies_already_closed":before_cancel.0 == expected_exits.0,
        "all_producers_already_finished":before_cancel.1 == expected_exits.1,
        "exit_cause":"not inferred from cumulative counters"}),
    );
    // Upstream may already have exited through relay timeout or bounded buffering.
    // Cancellation need not produce a new close; require all dispatched work exited.
    drop(slow);
    all_provider_exited(
        &mut host,
        &provider,
        expected_exits,
        &mut evidence,
        "after-slow-stream-cancel",
    )
    .await;
    unknown(&pool, seed.account, &slow_id, &mut evidence).await;
    completed.push(
        normal(
            &pool,
            &host,
            &seed,
            &client,
            NORMAL_REQUESTS,
            (server, &peer),
            &mut evidence,
        )
        .await,
    );
    let hits = provider.hits.load(Ordering::SeqCst);
    let replay = client
        .post(format!("{}/v1/chat/completions", host.prepared.endpoint))
        .bearer_auth(&seed.key)
        .header("idempotency-key", "h2-slow")
        .json(&request("socket-backpressure"))
        .send()
        .await
        .unwrap();
    assert_eq!(replay.status(), StatusCode::CONFLICT);
    assert_eq!(replay.version(), reqwest::Version::HTTP_2);
    assert_eq!(replay.headers()["x-request-id"].to_str().unwrap(), slow_id);
    let replay_body = replay.json::<Value>().await.unwrap();
    assert_eq!(replay_body["error"]["code"], "request_conflict");
    evidence.write(json!({"stage":"slow-replay","body":replay_body,"provider_hits":provider.hits.load(Ordering::SeqCst)}));
    assert_eq!(provider.hits.load(Ordering::SeqCst), hits);
    assert_eq!(hits, NORMAL_REQUESTS + 2);
    same_connection(
        server,
        &peer,
        &mut evidence,
        "after-stream-cancel-and-replay",
    );
    unknown(&pool, seed.account, &slow_id, &mut evidence).await;
    all_provider_exited(
        &mut host,
        &provider,
        (
            exit_baseline.0 + NORMAL_REQUESTS + 2,
            exit_baseline.1 + NORMAL_REQUESTS + 2,
        ),
        &mut evidence,
        "after-final-normal-and-replay",
    )
    .await;
    let balance = seed.ledger.balance(seed.account).await.unwrap();
    assert_eq!(
        balance.available_units,
        OPENING - (completed.len() as i64) * 49 - 140
    );
    assert_eq!(balance.reserved_units, 140);
    balanced(&pool).await;
    let pid = host.pid;
    let cgroup = host.cgroup.clone();
    host.finish().await;
    record(
        "socket-h2",
        &json!({"status":"PASS","actual_core_pid":pid,"cgroup":cgroup,
        "same_tcp_peer":peer,"http_version":"HTTP/2","normal_ids":completed,"slow_request_id":slow_id,
        "slow_checkpoint_plateau":stable_checkpoints,"active_ms":active.elapsed().as_millis(),
        "unknown_hold":140,"provider_hits":hits,"stream_window":STREAM_WINDOW,
        "scope":"H2 per-stream flow control on writable shared TCP, not TCP zero window"}),
    );
}
