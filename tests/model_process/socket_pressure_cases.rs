//! Fixed arrival pressure on real Core TCP sockets. The clients stay unread
//! through the recovery gate. Dropping them later cannot erase that gate's FAIL.
use super::resource_cases::{ResourceHost, sample};
use super::socket_cases::{Evidence, Raw, raw};
use super::*;
use std::{
    collections::{HashMap, HashSet},
    io::Write,
    net::SocketAddr,
    sync::{Mutex, atomic::AtomicBool},
};
use tokio::task::{JoinHandle, JoinSet};

use super::resource_client::{Outcome, complete, percentile, verify_paid};

const PHASE_SECONDS: u64 = 60;
const HEALTH_RPS: usize = 5;
const SLOW_COUNT: usize = 120;
const SLOW_PERIOD_MS: u64 = 500;
const OPENING: i64 = 100_000;
const MEMORY_MAX: u64 = 256 * 1024 * 1024;
const DRAIN_GROWTH: u64 = 32 * 1024 * 1024;
const SOCK_GROWTH: u64 = 1024 * 1024;

struct FailureEvidence {
    directory: PathBuf,
    cgroup: PathBuf,
    unit: String,
    pid: u32,
    armed: bool,
}
impl Drop for FailureEvidence {
    fn drop(&mut self) {
        if !self.armed {
            return;
        }
        let output=Command::new("systemctl").args(["--user","show",&self.unit,
            "--property=MainPID,Result,ExecMainCode,ExecMainStatus,ActiveState,SubState,OOMKilled,MemoryCurrent,MemoryPeak"]).output();
        let systemd = match output {
            Ok(out) => {
                json!({"exit_code":out.status.code(),"stdout":String::from_utf8_lossy(&out.stdout),"stderr":String::from_utf8_lossy(&out.stderr)})
            }
            Err(error) => json!({"observation_error":format!("{:?}",error.kind())}),
        };
        let read = |name: &str| match fs::read_to_string(self.cgroup.join(name)) {
            Ok(value) => json!({"raw":value}),
            Err(error) => json!({"observation_error":format!("{:?}",error.kind())}),
        };
        let value = json!({"stage":"failure-before-own-Core-Drop","pid":self.pid,
            "unit":self.unit,"systemd":systemd,"memory_events":read("memory.events"),
            "memory_current":read("memory.current"),"memory_peak":read("memory.peak"),
            "missing_OOMKilled_field_is_not_false":true});
        // Best effort during unwind: preserve the original panic if observation fails.
        if let Ok(mut file) = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(self.directory.join("pressure-failure-before-drop.json"))
        {
            let _ = serde_json::to_writer(&mut file, &value);
            let _ = file.write_all(b"\n");
            let _ = file.flush();
        }
    }
}

#[derive(Clone)]
struct Peer {
    server: SocketAddr,
    client: SocketAddr,
    id: String,
    label: String,
}
fn write_json(path: &Path, value: &Value) {
    private(path, &serde_json::to_vec(value).unwrap());
}
fn file(path: &Path) -> fs::File {
    fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(path)
        .unwrap()
}

// One kernel query covers all registered peers. Global orphan counters are only
// host context: ownership is always the exact Core->client four tuple.
fn queues(peers: &[Peer], server: SocketAddr) -> Value {
    let filter = format!("( src {} and sport = :{} )", server.ip(), server.port());
    let output = Command::new("/usr/bin/ss")
        .args(["-otinmH", &filter])
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "ss failed; missing observations are not empty queues"
    );
    let text = String::from_utf8(output.stdout).unwrap();
    let mut blocks: Vec<String> = Vec::new();
    for line in text.lines() {
        if line.starts_with(char::is_whitespace) {
            if let Some(last) = blocks.last_mut() {
                last.push('\n');
                last.push_str(line);
            }
        } else {
            blocks.push(line.to_owned());
        }
    }
    let mut indexed = HashMap::new();
    for block in blocks {
        let fields = block
            .lines()
            .next()
            .unwrap()
            .split_whitespace()
            .collect::<Vec<_>>();
        assert!(fields.len() >= 5, "unrecognized ss socket row");
        let source: SocketAddr = fields[3].parse().unwrap();
        let destination: SocketAddr = fields[4].parse().unwrap();
        assert_eq!(source, server);
        let number = |key: &str| -> Option<u64> {
            block
                .split_whitespace()
                .find_map(|field| field.strip_prefix(key).and_then(|v| v.parse().ok()))
        };
        let skmem = block
            .split_whitespace()
            .find_map(|field| field.strip_prefix("skmem:("))
            .map(|value| {
                let mut map = serde_json::Map::new();
                for part in value.trim_end_matches(')').split(',') {
                    let split = part.find(|c: char| c.is_ascii_digit()).unwrap();
                    map.insert(
                        part[..split].to_owned(),
                        json!(part[split..].parse::<u64>().unwrap()),
                    );
                }
                Value::Object(map)
            });
        let value = json!({"state":fields[0],"send_queue":fields[2].parse::<u64>().unwrap(),
            "bytes_acked":number("bytes_acked:"),"notsent":number("notsent:"),
            "snd_wnd":number("snd_wnd:"),"persist":block.contains("timer:(persist,"),
            "skmem":skmem,"raw_ss":block});
        assert!(
            indexed.insert(destination, value).is_none(),
            "duplicate TCP four tuple"
        );
    }
    json!(peers.iter().map(|peer|json!({"request_id":peer.id,"label":peer.label,
        "server":peer.server,"client":peer.client,
        "socket":indexed.get(&peer.client).cloned().unwrap_or_else(||json!({"state":"absent","send_queue":0}))
    })).collect::<Vec<_>>())
}

async fn checkpoint_rows(pool: &PgPool, peers: &[Peer]) -> Vec<Value> {
    let ids = peers.iter().map(|x| x.id.clone()).collect::<Vec<_>>();
    tokio::time::timeout(Duration::from_secs(1),sqlx::query_scalar(
        "SELECT jsonb_build_object('id',request_id,'content',count(*) FILTER(WHERE packet_bytes>16384),'terminal',bool_or(terminal_observed)) FROM core_model.output_checkpoints WHERE request_id=ANY($1) GROUP BY request_id")
        .bind(&ids).fetch_all(pool)).await.expect("checkpoint observation exceeded1s").unwrap()
}
struct Observation {
    samples: Vec<Value>,
    proved: HashSet<String>,
}
struct Observer {
    task: Option<JoinHandle<Observation>>,
    stop: Arc<AtomicBool>,
}
impl Drop for Observer {
    fn drop(&mut self) {
        if let Some(task) = &self.task {
            task.abort();
        }
    }
}
impl Observer {
    async fn finish(mut self) -> Observation {
        self.stop.store(true, Ordering::SeqCst);
        self.task
            .take()
            .unwrap()
            .await
            .expect("pressure observer failed")
    }
}
fn observe(
    host: &ResourceHost,
    pool: PgPool,
    peers: Arc<Mutex<Vec<Peer>>>,
    started: Instant,
) -> Observer {
    let stop = Arc::new(AtomicBool::new(false));
    let stopped = stop.clone();
    let pid = host.pid;
    let ticks = process_start_ticks(pid).unwrap();
    let group = host.cgroup.clone();
    let directory = host.prepared.directory.clone();
    let role = host.prepared.role.as_ref().unwrap().name.clone();
    let server: SocketAddr = host
        .prepared
        .endpoint
        .strip_prefix("http://")
        .unwrap()
        .parse()
        .unwrap();
    Observer {
        stop,
        task: Some(tokio::spawn(async move {
            let mut samples_file = file(&directory.join("pressure-resource-samples.jsonl"));
            let mut sockets_file = file(&directory.join("pressure-socket-samples.jsonl"));
            let mut samples = Vec::new();
            let mut proved = HashSet::new();
            let mut plateau: HashMap<String, (u64, u64, Instant)> = HashMap::new();
            let mut next_socket = Instant::now();
            loop {
                assert_eq!(process_start_ticks(pid).unwrap(), ticks, "Core PID changed");
                let mut value = sample(pid, &group, started, 0);
                if Instant::now() >= next_socket {
                    let registered = peers.lock().unwrap().clone();
                    let queue = queues(&registered, server);
                    let checkpoints = checkpoint_rows(&pool, &registered).await;
                    let by_id = checkpoints
                        .iter()
                        .map(|x| (x["id"].as_str().unwrap(), x))
                        .collect::<HashMap<_, _>>();
                    for row in queue.as_array().unwrap() {
                        let id = row["request_id"].as_str().unwrap();
                        let socket = &row["socket"];
                        let checkpoint = by_id.get(id);
                        let count = checkpoint.and_then(|x| x["content"].as_u64()).unwrap_or(0);
                        let terminal = checkpoint
                            .and_then(|x| x["terminal"].as_bool())
                            .unwrap_or(false);
                        let stalled = socket["state"] == "ESTAB"
                            && socket["send_queue"].as_u64().unwrap() > 0
                            && socket["persist"] == true
                            && count > 0
                            && count < 56
                            && !terminal;
                        if stalled {
                            let acked = socket["bytes_acked"]
                                .as_u64()
                                .expect("missing ACK observation");
                            match plateau.get(id) {
                                Some((old_ack, old_count, since))
                                    if *old_ack == acked && *old_count == count =>
                                {
                                    if since.elapsed() >= Duration::from_millis(500) {
                                        proved.insert(id.to_owned());
                                    }
                                }
                                _ => {
                                    plateau.insert(id.to_owned(), (acked, count, Instant::now()));
                                }
                            }
                        } else {
                            plateau.remove(id);
                        }
                    }
                    let connections: i64 = tokio::time::timeout(
                        Duration::from_secs(1),
                        sqlx::query_scalar(
                            "SELECT count(*) FROM pg_stat_activity WHERE usename=$1",
                        )
                        .bind(&role)
                        .fetch_one(&pool),
                    )
                    .await
                    .expect("runtime pool observation exceeded1s")
                    .unwrap();
                    value["runtime_connections"] = json!(connections);
                    let accounting:Value=tokio::time::timeout(Duration::from_secs(1),sqlx::query_scalar(
                    "SELECT jsonb_build_object('requests',count(*),'reserved',COALESCE(sum(reserved),0),'settled',COALESCE(sum(settled),0),'streaming',count(*) FILTER(WHERE state='streaming'),'reconcile',count(*) FILTER(WHERE state='reconcile')) FROM core_charging.requests").fetch_one(&pool))
                    .await.expect("accounting observation exceeded1s").unwrap();
                    let snapshot = json!({"elapsed_ms":started.elapsed().as_millis(),"peers":queue,
                    "checkpoints":checkpoints,"proved_backpressure":proved.len(),"funds":accounting,
                    "host_sockstat":fs::read_to_string("/proc/net/sockstat").unwrap()});
                    writeln!(
                        sockets_file,
                        "{}",
                        serde_json::to_string(&snapshot).unwrap()
                    )
                    .unwrap();
                    sockets_file.flush().unwrap();
                    next_socket = Instant::now() + Duration::from_secs(1);
                }
                writeln!(samples_file, "{}", serde_json::to_string(&value).unwrap()).unwrap();
                samples_file.flush().unwrap();
                samples.push(value);
                if stopped.load(Ordering::SeqCst) {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(200)).await;
            }
            Observation { samples, proved }
        })),
    }
}

async fn healthy(
    host: &ResourceHost,
    seed: &Seed,
    client: &reqwest::Client,
    label: &str,
) -> Vec<Outcome> {
    let start = tokio::time::Instant::now();
    let mut tasks = JoinSet::new();
    for ordinal in 0..PHASE_SECONDS as usize * HEALTH_RPS {
        let due = start + Duration::from_millis(200 * ordinal as u64);
        tokio::time::sleep_until(due).await;
        tasks.spawn(complete(
            client.clone(),
            host.prepared.endpoint.clone(),
            seed.key.clone(),
            format!("socket-pressure-{label}"),
            ordinal,
            ordinal % 5 == 4,
            due,
        ));
    }
    let mut rows = Vec::new();
    while let Some(row) = tasks.join_next().await {
        rows.push(row.unwrap());
    }
    tokio::time::sleep_until(start + Duration::from_secs(PHASE_SECONDS)).await;
    rows.sort_by_key(|x| x.ordinal);
    let latencies = rows.iter().map(|x| x.latency_ms).collect::<Vec<_>>();
    write_json(
        &host
            .prepared
            .directory
            .join(format!("pressure-healthy-{label}.json")),
        &json!({
        "offered":300,"seconds":PHASE_SECONDS,"elapsed_ms":start.elapsed().as_millis(),
        "successes":rows.iter().filter(|x|x.success).count(),"p95_ms":percentile(&latencies,95),
        "p99_ms":percentile(&latencies,99),"outcomes":rows.iter().map(Outcome::value).collect::<Vec<_>>() }),
    );
    rows
}
fn health_failures(rows: &[Outcome], label: &str) -> Vec<String> {
    let mut failures = Vec::new();
    let latencies = rows.iter().map(|x| x.latency_ms).collect::<Vec<_>>();
    if rows.len() != 300 || rows.iter().filter(|x| x.success).count() * 100 < 300 * 99 {
        failures.push(format!("{label}: success below99%"));
    }
    if percentile(&latencies, 95) > 1000 || percentile(&latencies, 99) > 2000 {
        failures.push(format!("{label}: latency gate"));
    }
    if rows.iter().any(|x| x.submission_lag_ms >= 200) {
        failures.push(format!("{label}: generator cadence"));
    }
    if rows.iter().any(|x| {
        !x.success && !(x.status == 503 && x.code == "core_not_accepting" && x.id.is_none())
    }) {
        failures.push(format!("{label}: non-admission failure"));
    }
    failures
}
async fn slow(
    host: Arc<ResourceHost>,
    key: String,
    peers: Arc<Mutex<Vec<Peer>>>,
) -> (Vec<Raw>, Vec<String>) {
    let start = tokio::time::Instant::now();
    let mut tasks = JoinSet::new();
    for ordinal in 0..SLOW_COUNT {
        let due = start + Duration::from_millis(SLOW_PERIOD_MS * ordinal as u64);
        tokio::time::sleep_until(due).await;
        let host = host.clone();
        let key = key.clone();
        let peers = peers.clone();
        let lag = due.elapsed().as_millis();
        tasks.spawn(async move {
            let label=format!("pressure-{ordinal:03}");
            let mut evidence=Evidence(file(&host.prepared.directory.join(format!("pressure-slow-{ordinal:03}.jsonl"))));
            evidence.write(json!({"stage":"offered","ordinal":ordinal,"submission_lag_ms":lag,"due_ms":ordinal as u64*SLOW_PERIOD_MS}));
            assert!(lag<SLOW_PERIOD_MS as u128,"missed fixed500ms slow arrival cadence");
            let raw=raw(&host,&key,&label,&mut evidence).await;
            peers.lock().unwrap().push(Peer {server:raw.server,client:raw.client,id:raw.request_id.clone(),label});
            raw
        });
    }
    let mut retained = Vec::new();
    let mut failures = Vec::new();
    while let Some(result) = tasks.join_next().await {
        match result {
            Ok(raw) => retained.push(raw),
            Err(error) => failures.push(format!("slow task failed: {error}")),
        }
    }
    tokio::time::sleep_until(start + Duration::from_secs(PHASE_SECONDS)).await;
    (retained, failures)
}

async fn audit(
    pool: &PgPool,
    seed: &Seed,
    normal: &[Outcome],
    peers: &[Peer],
    directory: &Path,
    label: &str,
) -> Value {
    let paid = normal
        .iter()
        .filter(|x| x.success)
        .map(|x| x.id.clone().unwrap())
        .collect::<Vec<_>>();
    let unknown = peers.iter().map(|x| x.id.clone()).collect::<Vec<_>>();
    let rows:Vec<Value>=sqlx::query_scalar("SELECT jsonb_build_object('id',r.id,'payer',r.payer_account_id,'state',r.state,'reserved',r.reserved,'settled',r.settled,'hold',h.reserved,'captured',h.captured,'closed',h.closed,'capture_journal',h.capture_journal_id,'terminal',(SELECT COALESCE(bool_or(terminal_observed),false) FROM core_model.output_checkpoints WHERE request_id=r.id),'provider_final',(SELECT count(*) FROM core_charging.usage_events WHERE request_id=r.id AND event_id='usage:provider-final')) FROM core_charging.requests r JOIN core_charging.ledger_holds h ON h.request_id=r.id WHERE r.id=ANY($1)").bind(&unknown).fetch_all(pool).await.unwrap();
    let wallet = seed.ledger.balance(seed.account).await.unwrap();
    let totals: (i64, i64) = sqlx::query_as(
        "SELECT count(*),count(*) FILTER(WHERE state='settled') FROM core_charging.requests",
    )
    .fetch_one(pool)
    .await
    .unwrap();
    let snapshot = json!({"stage":label,"paid_count":paid.len(),"unknown_count":rows.len(),"rows":rows,
        "available":wallet.available_units,"reserved":wallet.reserved_units,"requests":totals.0,"settled":totals.1});
    write_json(
        &directory.join(format!("pressure-funds-{label}.json")),
        &snapshot,
    );
    assert_eq!(rows.len(), unknown.len());
    assert_eq!(totals.0, (paid.len() + unknown.len()) as i64);
    assert_eq!(totals.1, paid.len() as i64);
    verify_paid(pool, seed.account, &paid).await;
    for row in &rows {
        assert_eq!(row["payer"], seed.account);
        assert_eq!(row["reserved"], 140);
        assert_eq!(row["hold"], 140);
        assert_eq!(row["settled"], 0);
        assert_eq!(row["captured"], 0);
        assert_eq!(row["closed"], false);
        assert_eq!(row["capture_journal"], Value::Null);
        assert_eq!(row["terminal"], false);
        assert_eq!(row["provider_final"], 0);
        assert!(["streaming", "reconcile"].contains(&row["state"].as_str().unwrap()));
    }
    balance(
        &seed.ledger,
        seed.account,
        OPENING - paid.len() as i64 * 49 - unknown.len() as i64 * 140,
        unknown.len() as i64 * 140,
    )
    .await;
    snapshot
}

// @mark model.socket-pressure role=test
#[sqlx::test(migrations = false)]
#[ignore = "Requires frozen socket-pressure runner, cgroup v2, ss and fresh owned PG"]
async fn independent_process_sustained_socket_backpressure(pool: PgPool) {
    assert_eq!(std::env::var("LMM_RESOURCE_ACCEPTANCE").as_deref(), Ok("1"));
    let seed = seed(&pool, OPENING).await;
    let provider = Provider::start(true).await;
    let prepared = prepare_core(&pool, &provider, &[seed.key_id], "socket-pressure").await;
    let config = prepared.directory.join("model.toml");
    let model = fs::read_to_string(&config).unwrap();
    for needle in ["lease_seconds=20", "timeout_seconds=3", "timeout_ms=5000"] {
        assert_eq!(model.matches(needle).count(), 1);
    }
    fs::write(
        config,
        model
            .replace("lease_seconds=20", "lease_seconds=60")
            .replace("timeout_seconds=3", "timeout_seconds=30")
            .replace("timeout_ms=5000", "timeout_ms=30000"),
    )
    .unwrap();
    let client = reqwest::Client::builder()
        .no_proxy()
        .timeout(Duration::from_secs(5))
        .build()
        .unwrap();
    let host = Arc::new(ResourceHost::start(prepared, &client).await);
    let directory = host.prepared.directory.clone();
    let server = host
        .prepared
        .endpoint
        .strip_prefix("http://")
        .unwrap()
        .parse::<SocketAddr>()
        .unwrap();
    let registry = Arc::new(Mutex::new(Vec::<Peer>::new()));
    let started = Instant::now();
    write_json(
        &directory.join("pressure-plan.json"),
        &json!({"phase_seconds":PHASE_SECONDS,"health_rps":HEALTH_RPS,
        "health_ratio":"80%JSON20%SSE","health_success_percent":99,"p95_ms":1000,"p99_ms":2000,
        "slow_count":SLOW_COUNT,"slow_period_ms":SLOW_PERIOD_MS,"slow_decoded_bytes":56*16*1024,
        "retain_peers_until_recovery_gate":true,"core_cpu_max":"100000100000","memory_max":MEMORY_MAX,
        "total_seconds":30,"lease_seconds":60,"planned_minimum_seconds":240,
        "generator_and_provider":"outside Core cgroup; runner supplies independent budget","pg":"separate1CPU512MiB"}),
    );
    let mut failure_evidence = FailureEvidence {
        directory: directory.clone(),
        cgroup: host.cgroup.clone(),
        unit: host
            .cgroup
            .file_name()
            .unwrap()
            .to_str()
            .unwrap()
            .to_owned(),
        pid: host.pid,
        armed: true,
    };
    let observer = observe(&host, pool.clone(), registry.clone(), started);
    let warm = healthy(&host, &seed, &client, "warmup").await;
    let baseline = sample(host.pid, &host.cgroup, started, 1);
    let mut failures = health_failures(&warm, "warmup");
    let mut normal = warm.clone();
    audit(&pool, &seed, &normal, &[], &directory, "warmup").await;
    let (overlap, (retained, slow_errors)) = tokio::join!(
        healthy(&host, &seed, &client, "overlap"),
        slow(host.clone(), seed.key.clone(), registry.clone())
    );
    failures.extend(slow_errors);
    failures.extend(health_failures(&overlap, "overlap"));
    normal.extend(overlap.clone());
    let peers = registry.lock().unwrap().clone();
    write_json(
        &directory.join("pressure-offered.json"),
        &json!({"scheduled":SLOW_COUNT,"accepted":retained.len(),"registry":peers.iter().map(|x|json!({"id":x.id,"label":x.label,"server":x.server,"client":x.client})).collect::<Vec<_>>(),"failures":failures}),
    );
    audit(&pool, &seed, &normal, &peers, &directory, "overlap").await;
    let recovered = healthy(&host, &seed, &client, "recovery-peers-retained").await;
    failures.extend(health_failures(&recovered, "recovery"));
    normal.extend(recovered.clone());
    let pre_release = queues(&peers, server);
    let retained_nonzero = pre_release
        .as_array()
        .unwrap()
        .iter()
        .filter(|x| x["socket"]["send_queue"].as_u64().unwrap() > 0)
        .count();
    // Save while every accepted client is still unread and retained. This result
    // remains binding even if dropping the clients makes the later drain pass.
    write_json(
        &directory.join("pressure-recovery-peers-retained.json"),
        &json!({"elapsed_ms":started.elapsed().as_millis(),
        "retained_clients":retained.len(),"nonzero_send_queue_count":retained_nonzero,"peers":pre_release,
        "desired_all_queues_drained":retained_nonzero==0,"clients_dropped":false}),
    );
    if retained.len() != SLOW_COUNT {
        failures.push("not all120 fixed-arrival slow flows accepted".into());
    }
    if retained_nonzero != 0 {
        failures.push(format!(
            "recovery with unread peers retained left {retained_nonzero} nonzero kernel send queues"
        ));
    }
    audit(
        &pool,
        &seed,
        &normal,
        &peers,
        &directory,
        "recovery-peers-retained",
    )
    .await;
    let hits = provider.hits.load(Ordering::SeqCst);
    for peer in &peers {
        let response=client.post(format!("{}/v1/chat/completions",host.prepared.endpoint)).bearer_auth(&seed.key)
            .header("idempotency-key",format!("socket-{}",peer.label)).json(&json!({"model":"local-model","messages":[{"role":"user","content":"socket-backpressure"}],"max_tokens":20,"stream":true,"stream_options":{"include_usage":true}})).send().await.unwrap();
        let status = response.status();
        let id = response.headers()["x-request-id"]
            .to_str()
            .unwrap()
            .to_owned();
        let body: Value = response.json().await.unwrap();
        assert_eq!(status, StatusCode::CONFLICT);
        assert_eq!(id, peer.id);
        assert_eq!(body["error"]["code"], "request_conflict");
    }
    assert_eq!(provider.hits.load(Ordering::SeqCst), hits);
    write_json(
        &directory.join("pressure-replay.json"),
        &json!({"replayed_original_keys":peers.len(),"status_each":409,"provider_before":hits,"provider_after":provider.hits.load(Ordering::SeqCst)}),
    );
    drop(retained);
    let released_at = Instant::now();
    tokio::time::sleep(Duration::from_secs(PHASE_SECONDS)).await;
    let final_sample = sample(host.pid, &host.cgroup, started, 4);
    let final_queues = queues(&peers, server);
    write_json(
        &directory.join("pressure-post-release-drain.json"),
        &json!({"idle_elapsed_ms":released_at.elapsed().as_millis(),"baseline":baseline,"final":final_sample,"peers":final_queues}),
    );
    // Classification is eventual and remains a separate, bounded stage. It
    // cannot erase the retained-client queue failure recorded sixty seconds ago.
    let ids = peers.iter().map(|x| x.id.clone()).collect::<Vec<_>>();
    let initial_pending: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_charging.requests WHERE id=ANY($1) AND state='streaming'",
    )
    .bind(&ids)
    .fetch_one(&pool)
    .await
    .unwrap();
    let classification_seconds = (initial_pending as u64).div_ceil(64) * 5 + 10;
    let classification_start = Instant::now();
    let mut last_progress = classification_start;
    let mut previous = initial_pending;
    let mut observations = Vec::new();
    loop {
        let stage = audit(
            &pool,
            &seed,
            &normal,
            &peers,
            &directory,
            &format!("classification-{:03}", observations.len()),
        )
        .await;
        let pending = stage["rows"]
            .as_array()
            .unwrap()
            .iter()
            .filter(|x| x["state"] == "streaming")
            .count() as i64;
        let now = Instant::now();
        observations.push(
            json!({"elapsed_ms":classification_start.elapsed().as_millis(),"streaming":pending}),
        );
        write_json(
            &directory.join(format!(
                "classification-observation-{:03}.json",
                observations.len() - 1
            )),
            &json!({"initial_streaming":initial_pending,"deadline_seconds":classification_seconds,"observations":observations}),
        );
        assert!(pending <= previous, "classification backlog grew");
        if pending < previous {
            last_progress = now;
        }
        assert!(
            classification_start.elapsed() < Duration::from_secs(classification_seconds),
            "classification deadline"
        );
        if pending == 0 {
            break;
        }
        assert!(
            last_progress.elapsed() < Duration::from_secs(15),
            "classification no progress"
        );
        previous = pending;
        tokio::time::sleep(Duration::from_millis(500)).await;
    }
    let final_funds = audit(&pool, &seed, &normal, &peers, &directory, "final").await;
    let observed = observer.finish().await;
    if observed.proved.len() != SLOW_COUNT {
        failures.push(format!(
            "only {} of120 real backpressure plateaus proved",
            observed.proved.len()
        ));
    }
    if final_sample["memory_current"].as_u64().unwrap()
        > baseline["memory_current"].as_u64().unwrap() + DRAIN_GROWTH
    {
        failures.push("post-drain memory_current grew".into());
    }
    for key in ["Rss", "Pss"] {
        if final_sample["smaps_rollup_kib"][key].as_u64().unwrap()
            > baseline["smaps_rollup_kib"][key].as_u64().unwrap() + DRAIN_GROWTH / 1024
        {
            failures.push(format!("post-drain {key} grew"));
        }
    }
    if final_sample["memory_stat"]["sock"].as_u64().unwrap()
        > baseline["memory_stat"]["sock"].as_u64().unwrap() + SOCK_GROWTH
    {
        failures.push("post-drain cgroup sock grew".into());
    }
    if final_sample["fd_count"].as_u64().unwrap() > baseline["fd_count"].as_u64().unwrap() + 1 {
        failures.push("post-drain FD grew".into());
    }
    if final_queues
        .as_array()
        .unwrap()
        .iter()
        .any(|x| x["socket"]["send_queue"].as_u64().unwrap() > 0)
    {
        failures.push("post-release queue not drained".into());
    }
    let samples = &observed.samples;
    let max_gap = samples
        .windows(2)
        .map(|x| x[1]["elapsed_ms"].as_u64().unwrap() - x[0]["elapsed_ms"].as_u64().unwrap())
        .max()
        .unwrap();
    if max_gap > 1500
        || samples[0]["elapsed_ms"].as_u64().unwrap() > 500
        || started.elapsed().as_millis()
            - samples.last().unwrap()["elapsed_ms"].as_u64().unwrap() as u128
            > 500
    {
        failures.push("observer coverage/gap".into());
    }
    for row in samples {
        if row["memory_peak"].as_u64().unwrap() > MEMORY_MAX {
            failures.push("memory hard cap".into());
        }
        for event in ["max", "oom", "oom_kill"] {
            if row["memory_events"][event] != 0 {
                failures.push(format!("memory event {event}"));
            }
        }
        if row
            .get("runtime_connections")
            .and_then(Value::as_i64)
            .is_some_and(|n| n > 2)
        {
            failures.push("runtime pool exceeded2".into());
        }
    }
    let successes = normal.iter().filter(|x| x.success).count();
    assert_eq!(
        provider.hits.load(Ordering::SeqCst),
        successes + peers.len()
    );
    assert_eq!(
        provider.stream_closed.load(Ordering::SeqCst),
        normal.iter().filter(|x| x.success && x.stream).count() + peers.len()
    );
    assert_eq!(
        provider.stream_finished.load(Ordering::SeqCst),
        normal.iter().filter(|x| x.success && x.stream).count() + peers.len()
    );
    failures.sort();
    failures.dedup();
    write_json(
        &directory.join("pressure-result.json"),
        &json!({"result":if failures.is_empty(){"PASS"}else{"FAIL"},"failures":failures,
        "elapsed_ms":started.elapsed().as_millis(),"normal_successes":successes,"normal_offered":900,"slow_offered":SLOW_COUNT,"slow_admitted":peers.len(),
        "backpressure_proved":observed.proved.len(),"retained_peer_nonzero_queues":retained_nonzero,"sample_count":samples.len(),"max_sample_gap_ms":max_gap,
        "funds":final_funds,"full_project_complete":false,"not_verified":["Go","whole node budget","real provider token accounting"]}),
    );
    assert!(started.elapsed() >= Duration::from_secs(240));
    assert!(
        failures.is_empty(),
        "frozen socket pressure gate failed: {failures:?}"
    );
    drop(client);
    let host = match Arc::try_unwrap(host) {
        Ok(host) => host,
        Err(_) => panic!("Core still borrowed by producer"),
    };
    host.finish().await;
    failure_evidence.armed = false;
}
