//! Opt-in 240-second shared Core+Go slow-socket gate. Product limits unchanged.
use super::*;
use std::{
    collections::{HashMap, HashSet},
    io::Write,
    net::SocketAddr,
    os::fd::AsRawFd,
    sync::{Arc, Mutex},
};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpSocket, TcpStream},
};

const SLOW_COUNT: usize = 120;
const PERIOD_MS: u64 = 500;
const PHASE_SECONDS: usize = 60;
#[derive(Clone)]
struct Peer {
    server: SocketAddr,
    client: SocketAddr,
    id: String,
    label: String,
}
struct Evidence(fs::File);
impl Evidence {
    fn write(&mut self, value: Value) {
        writeln!(self.0, "{value}").unwrap();
        self.0.flush().unwrap();
    }
}
fn log(root: &Path, name: &str) -> Evidence {
    Evidence(
        fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(root.join(name))
            .unwrap(),
    )
}
fn save(root: &Path, name: &str, value: Value) {
    private(root, name, &serde_json::to_vec(&value).unwrap());
}
async fn first_content(stream: &mut TcpStream) {
    tokio::time::timeout(Duration::from_secs(2), async {
        let mut pending = Vec::new();
        loop {
            let mut line = Vec::new();
            while !line.ends_with(b"\r\n") {
                line.push(stream.read_u8().await.unwrap());
                assert!(line.len() <= 1024);
            }
            let header = std::str::from_utf8(&line[..line.len() - 2]).unwrap();
            assert!(!header.is_empty() && header.bytes().all(|b| b.is_ascii_hexdigit()));
            let size = usize::from_str_radix(header, 16).unwrap();
            assert!(size > 0 && size <= 256 * 1024);
            let mut packet = vec![0; size];
            stream.read_exact(&mut packet).await.unwrap();
            let mut crlf = [0; 2];
            stream.read_exact(&mut crlf).await.unwrap();
            assert_eq!(&crlf, b"\r\n");
            pending.extend(packet);
            assert!(pending.len() <= 512 * 1024);
            while let Some(end) = pending.windows(2).position(|x| x == b"\n\n") {
                let event = pending.drain(..end + 2).collect::<Vec<_>>();
                let text = std::str::from_utf8(&event).unwrap();
                for data in text.lines().filter_map(|line| line.strip_prefix("data: ")) {
                    assert_ne!(data, "[DONE]");
                    let value: Value = serde_json::from_str(data).unwrap();
                    assert!(value.get("error").is_none());
                    assert!(
                        value["usage"].is_null(),
                        "usage terminal before first actual content"
                    );
                    assert!(
                        value["choices"][0]["finish_reason"].is_null(),
                        "finish terminal before first actual content"
                    );
                    if let Some(content) = value["choices"][0]["delta"]["content"].as_str() {
                        // Core's legal initial role delta carries empty content.
                        // It is not the first actual payload or backpressure evidence.
                        if content.is_empty() {
                            continue;
                        }
                        assert_eq!(content.len(), 16 * 1024);
                        assert!(content.bytes().all(|b| b == 1));
                        return;
                    }
                }
            }
        }
    })
    .await
    .expect("slow client did not read its first actual content frame");
}
struct Raw {
    stream: TcpStream,
    fd: i32,
    device: u64,
    inode: u64,
    server: SocketAddr,
    client: SocketAddr,
    request_id: String,
}
async fn raw(
    endpoint: &str,
    key: &str,
    label: &str,
    evidence: &mut Evidence,
    used: &Mutex<HashSet<SocketAddr>>,
) -> Raw {
    let server: SocketAddr = endpoint.strip_prefix("http://").unwrap().parse().unwrap();
    assert!(server.is_ipv4() && server.ip().is_loopback());
    let selection_started = Instant::now();
    let mut selected = None;
    // A retained reset FD does not reserve its old ephemeral port. Reserve the
    // historical endpoint before connect/HTTP, never retry a dispatched flow.
    // Distinct historical four tuples keep ss registry evidence attributable
    // after RST releases a port while its original client FD remains open.
    for attempt in 1..=64 {
        assert!(
            selection_started.elapsed() < Duration::from_millis(200),
            "unique endpoint selection exceeded200ms"
        );
        let candidate = TcpSocket::new_v4().unwrap();
        candidate.bind(SocketAddr::new(server.ip(), 0)).unwrap();
        let local = candidate.local_addr().unwrap();
        if used.lock().unwrap().insert(local) {
            selected = Some((candidate, local, attempt));
            break;
        }
        // Only an unconnected candidate is dropped on a historical collision.
    }
    let (socket, selected_client, selection_attempts) =
        selected.expect("could not reserve unique endpoint in64 attempts");
    socket.set_recv_buffer_size(4096).unwrap();
    let actual_receive_buffer = socket.recv_buffer_size().unwrap();
    evidence.write(
        json!({"stage":"pre-connect","case":label,"requested_rcvbuf":4096,
        "actual_rcvbuf":actual_receive_buffer,"server":server,"reserved_client":selected_client,"endpoint_selection_attempts":selection_attempts,"endpoint_selection_ms":selection_started.elapsed().as_millis()}),
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
    assert_eq!(client, selected_client, "connect changed reserved endpoint");
    let body = serde_json::to_vec(&body("socket-backpressure", true)).unwrap();
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
    evidence.write(json!({"stage":"raw-status","case":label,
        "status_line":header.lines().next(),"server":server,"client":client}));
    if !header.starts_with("HTTP/1.1 200 ") {
        let mut bytes = Vec::new();
        let read = tokio::time::timeout(Duration::from_millis(500), async {
            let mut chunk = [0_u8; 1024];
            while bytes.len() < 16_384 {
                let count = stream.read(&mut chunk).await?;
                if count == 0 {
                    break;
                }
                bytes.extend_from_slice(&chunk[..count]);
            }
            Ok::<(), std::io::Error>(())
        })
        .await;
        // This bounded failure-only body may contain HTTP chunk framing. Keep
        // its raw bytes and decode the first JSON object when one is present.
        let error_code = bytes
            .iter()
            .position(|byte| *byte == b'{')
            .and_then(|start| {
                serde_json::Deserializer::from_slice(&bytes[start..])
                    .into_iter::<Value>()
                    .next()
                    .and_then(Result::ok)
            })
            .and_then(|value| value["error"]["code"].as_str().map(str::to_owned));
        evidence.write(json!({"stage":"raw-rejected-before-assert","case":label,
            "status_line":header.lines().next(),"error_code":error_code,
            "bounded_body":String::from_utf8_lossy(&bytes),"bytes":bytes.len(),
            "body_read_finished":matches!(read,Ok(Ok(()))),"body_limit":16_384}));
    }
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
    first_content(&mut stream).await;
    let fd = stream.as_raw_fd();
    let metadata = fs::metadata(format!("/proc/self/fd/{fd}")).unwrap();
    let (device, inode) = (metadata.dev(), metadata.ino());
    evidence.write(json!({"stage":"first-content-read-stop","case":label,"request_id":request_id,"fd":fd,"device":device,"inode":inode}));
    Raw {
        stream,
        fd,
        device,
        inode,
        server,
        client,
        request_id,
    }
}
// One kernel query covers all registered peers. Global orphan counters are only
// host context: ownership is always the exact Core->client four tuple.
fn queues(peers: &[Peer], server: SocketAddr) -> Value {
    let filter = format!("( src {} and sport = :{} )", server.ip(), server.port());
    struct Reap(std::process::Child);
    impl Drop for Reap {
        fn drop(&mut self) {
            let _ = self.0.kill();
            let _ = self.0.wait();
        }
    }
    let mut child = Reap(
        Command::new("/usr/bin/timeout")
            .args(["--kill-after=1s", "2s", "/usr/bin/ss", "-otinmH", &filter])
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap(),
    );
    let stdout = child.0.stdout.take().unwrap();
    let stderr = child.0.stderr.take().unwrap();
    // Read at most limit+1 bytes per pipe. The external timeout bounds blocked
    // readers; Drop kills/reaps this owned child even on diagnostic failure.
    let out = std::thread::spawn(move || {
        let mut bytes = Vec::new();
        std::io::Read::read_to_end(
            &mut std::io::Read::take(stdout, 1024 * 1024 + 1),
            &mut bytes,
        )
        .unwrap();
        bytes
    });
    let err = std::thread::spawn(move || {
        let mut bytes = Vec::new();
        std::io::Read::read_to_end(&mut std::io::Read::take(stderr, 64 * 1024 + 1), &mut bytes)
            .unwrap();
        bytes
    });
    let stdout = out.join().unwrap();
    let stderr = err.join().unwrap();
    let status = child.0.wait().unwrap();
    assert!(
        status.success(),
        "ss observation failed ({status}): {}",
        String::from_utf8_lossy(&stderr)
    );
    assert!(
        stdout.len() <= 1024 * 1024 && stderr.len() <= 64 * 1024,
        "ss diagnostic output cap exceeded"
    );
    let text = String::from_utf8(stdout).unwrap();
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

async fn slow(
    endpoint: String,
    key: String,
    root: PathBuf,
    registry: Arc<Mutex<Vec<Peer>>>,
    used: Arc<Mutex<HashSet<SocketAddr>>>,
) -> Vec<Raw> {
    let start = tokio::time::Instant::now();
    let mut tasks = JoinSet::new();
    for ordinal in 0..SLOW_COUNT {
        let due = start + Duration::from_millis(PERIOD_MS * ordinal as u64);
        tokio::time::sleep_until(due).await;
        let (endpoint, key, root, registry) = (
            endpoint.clone(),
            key.clone(),
            root.clone(),
            registry.clone(),
        );
        let used = used.clone();
        tasks.spawn(async move {
            let lag=due.elapsed().as_millis();
            let label=format!("joint-pressure-{ordinal:03}");
            let mut evidence=log(&root,&format!("joint-slow-{ordinal:03}.jsonl"));
            evidence.write(json!({"stage":"offered","ordinal":ordinal,"due_ms":ordinal as u64*PERIOD_MS,"submission_lag_ms":lag}));
            assert!(lag<PERIOD_MS as u128,"fixed slow arrival cadence missed");
            let raw=raw(&endpoint,&key,&label,&mut evidence,&used).await;
            registry.lock().unwrap().push(Peer{server:raw.server,client:raw.client,id:raw.request_id.clone(),label:format!("socket-{label}")});
            raw
        });
    }
    let mut retained = Vec::new();
    while let Some(row) = tasks.join_next().await {
        retained.push(row.expect("slow admission/content failed; see per-flow diagnostic"));
    }
    tokio::time::sleep_until(start + Duration::from_secs(60)).await;
    retained
}

async fn observe(
    store: Store,
    root: PathBuf,
    peers: Arc<Mutex<Vec<Peer>>>,
    server: SocketAddr,
    started: Instant,
    mut stop: tokio::sync::oneshot::Receiver<()>,
) -> HashSet<String> {
    let pool = store.pool.clone();
    let mut evidence = log(&root, "joint-socket-samples.jsonl");
    let mut proved = HashSet::new();
    let mut plateau: HashMap<String, (u64, u64, Instant)> = HashMap::new();
    let mut previous = started;
    loop {
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
            let content = checkpoint.and_then(|v| v["content"].as_u64()).unwrap_or(0);
            let terminal = checkpoint
                .and_then(|v| v["terminal"].as_bool())
                .unwrap_or(false);
            if socket["state"] == "ESTAB"
                && socket["send_queue"].as_u64().unwrap() > 0
                && socket["persist"] == true
                && content > 0
                && content < 56
                && !terminal
            {
                let ack = socket["bytes_acked"]
                    .as_u64()
                    .expect("missing actual ACK count");
                match plateau.get(id) {
                    Some((old_ack, old_count, since))
                        if *old_ack == ack && *old_count == content =>
                    {
                        if since.elapsed() >= Duration::from_millis(500) {
                            proved.insert(id.to_owned());
                        }
                    }
                    _ => {
                        plateau.insert(id.to_owned(), (ack, content, Instant::now()));
                    }
                }
            } else {
                plateau.remove(id);
            }
        }
        let (core,go):(i64,i64)=tokio::time::timeout(Duration::from_secs(1),sqlx::query_as("SELECT count(*) FILTER(WHERE usename=$1),count(*) FILTER(WHERE usename=$2) FROM pg_stat_activity").bind(&store.core_role).bind(&store.store_role).fetch_one(&pool)).await.expect("runtime connection observer timeout").unwrap();
        let snapshots = [
            runtime_snapshot(store.core_pid),
            runtime_snapshot(store.go_pid),
        ];
        let now = Instant::now();
        evidence.write(json!({"elapsed_ms":started.elapsed().as_millis(),"gap_ms":now.duration_since(previous).as_millis(),"peers":queue,"checkpoints":checkpoints,"proved_ids":proved,"core_connections":core,"go_connections":go,"products":snapshots,"host_sockstat":fs::read_to_string("/proc/net/sockstat").unwrap()}));
        assert!(
            core <= 8 && go <= 2,
            "runtime exceeded existing four Core pools/two Go connections"
        );
        assert!(
            now.duration_since(previous) <= Duration::from_millis(2500),
            "socket observation gap"
        );
        previous = now;
        tokio::select! {_=&mut stop=>break,_=tokio::time::sleep(Duration::from_millis(500))=>{}}
    }
    proved
}

// @mark commerce.socket-pressure role=test
pub(super) async fn run(mixed: &mut Mixed, store: &Store, endpoint: &str, root: &Path) {
    assert!(socket_profile());
    let started = Instant::now();
    let server: SocketAddr = endpoint.strip_prefix("http://").unwrap().parse().unwrap();
    let registry = Arc::new(Mutex::new(Vec::new()));
    let used = Arc::new(Mutex::new(HashSet::new()));
    let (stop, stopping) = tokio::sync::oneshot::channel();
    let mut observer = tokio::spawn(observe(
        store.clone(),
        root.to_owned(),
        registry.clone(),
        server,
        started,
        stopping,
    ));
    struct Abort(tokio::task::AbortHandle);
    impl Drop for Abort {
        fn drop(&mut self) {
            self.0.abort();
        }
    }
    let _abort = Abort(observer.abort_handle());
    save(
        root,
        "joint-socket-plan.json",
        json!({"profile":"socket-pressure","phase_seconds":[60,60,60,60],"model_rps":5,"model_ratio":"80%JSON20%SSE","store_full_refund_rps":1,"model_complete_required":900,"store_complete_required":180,"slow_offered":120,"arrival_period_ms":500,"decoded_bytes_per_slow":56*16*1024,"timeout_seconds":30,"lease_seconds":60,"retain_through_recovery":true,"shared_cpu_cores":1,"shared_memory_bytes":512*1024*1024,"each_product_memory_bytes":256*1024*1024}),
    );
    let workload = async {
        mixed
            .phase(store, endpoint, root, "socket-warmup", PHASE_SECONDS)
            .await;
        let baseline = [
            runtime_snapshot(store.core_pid),
            runtime_snapshot(store.go_pid),
        ];
        let key = mixed.key.clone();
        let (_, retained) = tokio::join!(
            mixed.phase(store, endpoint, root, "socket-overlap", PHASE_SECONDS),
            slow(
                endpoint.into(),
                key,
                root.to_owned(),
                registry.clone(),
                used.clone()
            )
        );
        let peers = registry.lock().unwrap().clone();
        let mut reserved_endpoints = used.lock().unwrap().iter().copied().collect::<Vec<_>>();
        reserved_endpoints.sort();
        assert_eq!(
            reserved_endpoints.len(),
            120,
            "historical endpoint reservation count"
        );
        let admitted_endpoints = peers.iter().map(|peer| peer.client).collect::<HashSet<_>>();
        assert_eq!(
            admitted_endpoints.len(),
            120,
            "duplicate historical admitted endpoint"
        );
        assert_eq!(
            admitted_endpoints,
            *used.lock().unwrap(),
            "admissions differ from pre-dispatch endpoint reservations"
        );
        save(
            root,
            "joint-socket-offered.json",
            json!({"offered":120,"accepted":retained.len(),"unique_reserved_endpoints":reserved_endpoints,"used_endpoint_count":reserved_endpoints.len(),"peers":peers.iter().map(|p|json!({"id":p.id,"label":p.label,"server":p.server,"client":p.client})).collect::<Vec<_>>()}),
        );
        assert_eq!(retained.len(), 120);
        assert_eq!(peers.len(), 120);
        for peer in &peers {
            mixed.unknown.push(peer.id.clone());
            mixed.labels.push((peer.id.clone(), peer.label.clone()));
        }
        mixed.verify_finances(&store.pool).await;
        mixed
            .phase(
                store,
                endpoint,
                root,
                "socket-retained-recovery",
                PHASE_SECONDS,
            )
            .await;
        let retention_witnesses=retained.iter().map(|raw| {
            let fd=raw.stream.as_raw_fd();
            assert_eq!(fd,raw.fd,"retained client FD changed");
            let metadata=fs::metadata(format!("/proc/self/fd/{fd}")).expect("retained client FD no longer open");
            assert_eq!((metadata.dev(),metadata.ino()),(raw.device,raw.inode),"retained client socket identity changed");
            let peer_status=match raw.stream.peer_addr() {
                Ok(address)=>{assert_eq!(address,raw.server);json!({"state":"connected","peer":address})},
                Err(error)=>{
                    assert!(matches!(error.raw_os_error(),Some(107|104)),"unexpected retained peer_addr failure: {error}");
                    json!({"state":"reset-or-disconnected-fd-still-open","errno":error.raw_os_error()})
                }
            };
            json!({"request_id":raw.request_id,"fd":fd,"device":metadata.dev(),"inode":metadata.ino(),"same_open_fd":true,"original_server":raw.server,"original_client":raw.client,"peer_status":peer_status})
        }).collect::<Vec<_>>();
        let recovery = queues(&peers, server);
        let nonzero = recovery
            .as_array()
            .unwrap()
            .iter()
            .filter(|p| p["socket"]["send_queue"].as_u64().unwrap() > 0)
            .count();
        save(
            root,
            "joint-socket-retained-recovery.json",
            json!({"elapsed_ms":started.elapsed().as_millis(),"retained_clients":retained.len(),"clients_dropped":false,"retention_witnesses":retention_witnesses,"nonzero_send_queue":nonzero,"peers":recovery}),
        );
        assert_eq!(
            nonzero, 0,
            "unread retained sockets still have kernel Send-Q; drop cannot erase this FAIL"
        );
        mixed.verify(&store.pool).await;
        let hits = mixed.provider.hits.load(Ordering::SeqCst);
        let client = Client::builder()
            .no_proxy()
            .timeout(Duration::from_secs(5))
            .build()
            .unwrap();
        for peer in &peers {
            let response = client
                .post(format!("{endpoint}/v1/chat/completions"))
                .bearer_auth(&mixed.key)
                .header("idempotency-key", &peer.label)
                .json(&body("socket-backpressure", true))
                .send()
                .await
                .unwrap();
            let status = response.status();
            let id = valid_id(&response);
            let error: Value = response.json().await.unwrap();
            assert_eq!(status, StatusCode::CONFLICT);
            assert_eq!(id.as_ref(), Some(&peer.id));
            assert_eq!(error["error"]["code"], "request_conflict");
        }
        assert_eq!(
            mixed.provider.hits.load(Ordering::SeqCst),
            hits,
            "original socket replay dispatched provider again"
        );
        save(
            root,
            "joint-socket-replay.json",
            json!({"original_keys":120,"provider_hits_before":hits,"provider_hits_after":mixed.provider.hits.load(Ordering::SeqCst)}),
        );
        drop(retained);
        let idle = Instant::now();
        tokio::time::sleep(Duration::from_secs(60)).await;
        assert!(idle.elapsed() >= Duration::from_secs(60));
        assert!(started.elapsed() >= Duration::from_secs(240));
        let post = [
            runtime_snapshot(store.core_pid),
            runtime_snapshot(store.go_pid),
        ];
        save(
            root,
            "joint-socket-drain.json",
            json!({"baseline":baseline,"post_drain":post,"idle_ms":idle.elapsed().as_millis(),"peers":queues(&peers,server)}),
        );
        for (before, after) in baseline.iter().zip(&post) {
            for key in ["rss_kib", "pss_kib"] {
                assert!(after[key].as_u64().unwrap() <= before[key].as_u64().unwrap() + 32 * 1024);
            }
            assert!(after["fds"].as_u64().unwrap() <= before["fds"].as_u64().unwrap() + 32);
        }
        mixed.verify(&store.pool).await;
        verify_store(store, &mixed.orders).await;
        assert_eq!(
            mixed.normal.iter().filter(|r| r.success).count(),
            900,
            "new profile requires900 complete responses, not merely offered"
        );
        assert_eq!(mixed.orders.len(), 180);
        let inventory:(i64,i64)=sqlx::query_as("SELECT (body->>'reserved')::bigint,(body->>'claimed')::bigint FROM lmm_store.variants WHERE id=$1").bind(&store.variant).fetch_one(&store.owner).await.unwrap();
        assert_eq!(inventory, (0, 180));
        (hits, nonzero)
    };
    let (hits, nonzero) = tokio::select! {result=workload=>result,result=&mut observer=>panic!("joint socket observer failed before completion: {result:?}")};
    stop.send(()).unwrap();
    let proved = observer.await.unwrap();
    assert_eq!(
        proved.len(),
        120,
        "all120 streams need actual ESTAB nonzero Send-Q/persist/ACK+checkpoint plateau witness"
    );
    save(
        root,
        "joint-socket-result.json",
        json!({"result":"PASS","profile":"socket-pressure","minimum_duration_ms":started.elapsed().as_millis(),"slow_offered":120,"slow_accepted":120,"retained_at_recovery":120,"arrival_period_ms":500,"all_120_nonzero_sendq_witness":proved.len()==120,"proved_ids":proved,"retained_recovery_nonzero_sendq":nonzero,"clients_dropped_before_recovery":false,"replayed_original_keys":120,"provider_hits_before":hits,"provider_hits_after":mixed.provider.hits.load(Ordering::SeqCst),"model_complete":900,"commerce_complete_refunded":180,"unknown_holds":120,"full_project_complete":false}),
    );
}

// Real loopback HTTP chunk framing, independent of PostgreSQL or products.
async fn local_chunk_stream(events: Vec<String>) -> (TcpStream, tokio::task::JoinHandle<()>) {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let server = tokio::spawn(async move {
        let (mut stream, _) = listener.accept().await.unwrap();
        for event in events {
            let bytes = format!("data: {event}\n\n").into_bytes();
            let chunk = format!("{:x}\r\n", bytes.len());
            stream.write_all(chunk.as_bytes()).await.unwrap();
            stream.write_all(&bytes).await.unwrap();
            stream.write_all(b"\r\n").await.unwrap();
        }
        stream.shutdown().await.unwrap();
    });
    (TcpStream::connect(address).await.unwrap(), server)
}

#[tokio::test]
async fn socket_probe_accepts_empty_initial_content() {
    let initial =
        json!({"choices":[{"delta":{"role":"assistant","content":""},"finish_reason":null}]})
            .to_string();
    let content =
        json!({"choices":[{"delta":{"content":"\u{1}".repeat(16*1024)},"finish_reason":null}]})
            .to_string();
    let (mut stream, server) = local_chunk_stream(vec![initial, content]).await;
    first_content(&mut stream).await;
    server.await.unwrap();
}

#[tokio::test]
#[should_panic]
async fn socket_probe_rejects_done_before_content() {
    let (mut stream, _server) = local_chunk_stream(vec!["[DONE]".into()]).await;
    first_content(&mut stream).await;
}

#[tokio::test]
#[should_panic(expected = "finish terminal before first actual content")]
async fn socket_probe_rejects_finish_before_content() {
    let terminal = json!({"choices":[{"delta":{"content":""},"finish_reason":"stop"}]}).to_string();
    let (mut stream, _server) = local_chunk_stream(vec![terminal]).await;
    first_content(&mut stream).await;
}
