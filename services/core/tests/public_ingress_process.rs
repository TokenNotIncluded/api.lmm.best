//! Opt-in real Core OS ingress regression. Only fresh synthetic owner/runtime
//! databases; raw peers never finish an HTTP head or reach model admission.
use axum::http::StatusCode;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use std::{
    collections::HashSet,
    fs,
    io::{Read, Write},
    net::SocketAddr,
    os::{
        fd::{AsFd, AsRawFd},
        unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt},
    },
    path::{Path, PathBuf},
    process::{Child, Command, Stdio},
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, Instant},
};
use tokio::{
    io::AsyncWriteExt,
    net::{TcpSocket, TcpStream},
};
#[path = "common/model_resource_client.rs"]
mod model_client;
#[path = "common/model_fixture.rs"]
mod model_fixture;
#[path = "common/runtime.rs"]
mod runtime;
use model_fixture::{Provider, fixture};
async fn balanced(pool: &PgPool) {
    let bad:i64=sqlx::query_scalar("SELECT count(*) FROM (SELECT j.id FROM core_billing.ledger_journals j LEFT JOIN core_billing.ledger_entries e ON e.journal_id=j.id GROUP BY j.id HAVING count(e.journal_id)<2 OR sum(e.delta_units)<>0) q").fetch_one(pool).await.unwrap();
    assert_eq!(bad, 0);
}
fn private(path: &Path, bytes: &[u8]) {
    use std::io::Write;
    fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(path)
        .unwrap()
        .write_all(bytes)
        .unwrap();
}
fn save(root: &Path, name: &str, value: Value) {
    private(&root.join(name), &serde_json::to_vec(&value).unwrap());
}
fn fields(raw: &str) -> Value {
    let mut result = serde_json::Map::new();
    for line in raw.lines() {
        let mut parts = line.split_whitespace();
        if let (Some(k), Some(v)) = (parts.next(), parts.next())
            && let Ok(n) = v.parse::<u64>()
        {
            result.insert(k.trim_end_matches(':').into(), json!(n));
        }
    }
    Value::Object(result)
}
fn system(args: &[&str]) -> String {
    let output = Command::new("/usr/bin/systemctl")
        .args(["--user"])
        .args(args)
        .output()
        .unwrap();
    assert!(output.status.success(), "owned systemctl operation failed");
    String::from_utf8(output.stdout).unwrap().trim().to_owned()
}
struct Core {
    unit: String,
    launcher: Option<Child>,
    pid: u32,
    cgroup: PathBuf,
    endpoint: String,
}
impl Drop for Core {
    fn drop(&mut self) {
        if self.launcher.is_some() {
            let _ = Command::new("/usr/bin/systemctl")
                .args(["--user", "--no-block", "stop", &self.unit])
                .status();
            // The attested parent reaps the launcher and confirms owned-unit
            // cleanup. Blocking here would stall this current-thread runtime.
        }
    }
}
impl Core {
    async fn start(config: &Path, address: SocketAddr, root: &Path) -> Self {
        let binary = PathBuf::from(std::env::var_os("LMM_PUBLIC_CORE_BIN").unwrap());
        let expected = std::env::var("LMM_PUBLIC_CORE_SHA256").unwrap();
        assert!(binary.is_absolute() && binary.canonicalize().unwrap() == binary);
        assert_eq!(
            format!("{:x}", Sha256::digest(fs::read(&binary).unwrap())),
            expected
        );
        let target = fs::File::open(&binary).unwrap();
        let target_meta = target.metadata().unwrap();
        let nonce = format!("{:032x}", rand::random::<u128>());
        let unit = format!("lmm-prehead-{nonce}.service");
        let stat = fs::read_to_string("/proc/self/stat").unwrap();
        let start_ticks = stat[stat.rfind(')').unwrap() + 1..]
            .split_whitespace()
            .nth(19)
            .unwrap()
            .parse::<u64>()
            .unwrap();
        let owner = json!({"pid":std::process::id(),"start_ticks":start_ticks,"boot_id":fs::read_to_string("/proc/sys/kernel/random/boot_id").unwrap().trim()});
        save(
            root,
            "core-allocation.json",
            json!({"unit":unit,"nonce":nonce,"expected_binary_sha256":expected,"expected_binary":binary,"test_owner":owner}),
        );
        let log = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(root.join("core.log"))
            .unwrap();
        let launcher = Command::new("/usr/bin/systemd-run")
            .args([
                "--user",
                "--quiet",
                "--wait",
                "--pipe",
                "--collect",
                "--service-type=exec",
                "--unit",
                &unit,
                "--property=CPUQuota=100%",
                "--property=CPUQuotaPeriodSec=100ms",
                "--property=MemoryMax=256M",
                "--property=MemorySwapMax=0",
                "--property=TasksMax=64",
                "--property=RuntimeMaxSec=120",
            ])
            .arg(format!("--property=Description=Owned ingress {nonce}"))
            .arg(&binary)
            .arg("--config")
            .arg(config)
            .stdin(Stdio::null())
            .stdout(log.try_clone().unwrap())
            .stderr(log)
            .spawn()
            .unwrap();
        let mut core = Self {
            unit,
            launcher: Some(launcher),
            pid: 0,
            cgroup: PathBuf::new(),
            endpoint: format!("http://{address}"),
        };
        let client = reqwest::Client::builder()
            .no_proxy()
            .timeout(Duration::from_secs(1))
            .build()
            .unwrap();
        let deadline = Instant::now() + Duration::from_secs(15);
        loop {
            assert!(Instant::now() < deadline, "Core readiness exceeded15s");
            if let Ok(response) = client
                .get(format!("{}/health/ready", core.endpoint))
                .send()
                .await
                && response.status() == StatusCode::OK
            {
                let value: Value = response.json().await.unwrap();
                assert_eq!(value["capabilities"]["models"], true);
                break;
            }
            assert!(
                core.launcher
                    .as_mut()
                    .unwrap()
                    .try_wait()
                    .unwrap()
                    .is_none()
            );
            tokio::time::sleep(Duration::from_millis(25)).await;
        }
        core.pid = system(&["show", &core.unit, "--property=MainPID", "--value"])
            .parse()
            .unwrap();
        assert!(core.pid > 0);
        let mut executed = fs::File::open(format!("/proc/{}/exe", core.pid)).unwrap();
        let metadata = executed.metadata().unwrap();
        let mut bytes = Vec::new();
        executed.read_to_end(&mut bytes).unwrap();
        assert_eq!(format!("{:x}", Sha256::digest(&bytes)), expected);
        assert_eq!(
            (metadata.dev(), metadata.ino()),
            (target_meta.dev(), target_meta.ino())
        );
        core.cgroup = PathBuf::from("/sys/fs/cgroup").join(
            system(&["show", &core.unit, "--property=ControlGroup", "--value"])
                .trim_start_matches('/'),
        );
        let limits = [
            ("cpu.max", "100000 100000"),
            ("memory.max", "268435456"),
            ("memory.swap.max", "0"),
            ("pids.max", "64"),
        ];
        for (name, wanted) in limits {
            assert_eq!(
                fs::read_to_string(core.cgroup.join(name)).unwrap().trim(),
                wanted
            );
        }
        save(
            root,
            "core-ready.json",
            json!({"pid":core.pid,"stat":fs::read_to_string(format!("/proc/{}/stat",core.pid)).unwrap(),"binary_sha256":expected,"device":metadata.dev(),"inode":metadata.ino(),"unit":core.unit,"cgroup":core.cgroup,"limits":limits,"address":address}),
        );
        core
    }
    async fn stop(&mut self) {
        let unit = self.unit.clone();
        let mut launcher = self.launcher.take().unwrap();
        let status = tokio::task::spawn_blocking(move || {
            system(&["stop", &unit]);
            launcher.wait().unwrap()
        })
        .await
        .unwrap();
        assert!(status.success(), "Core did not exit normally");
        assert!(!PathBuf::from(format!("/proc/{}", self.pid)).exists());
        assert!(!self.cgroup.exists());
    }
}
async fn configure(
    pool: &PgPool,
    provider: &Provider,
    keys: &[i64],
    root: &Path,
) -> (SocketAddr, PathBuf, runtime::RuntimeRole) {
    let database = pool.connect_options().get_database().unwrap().to_owned();
    assert!(
        database
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'_')
    );
    sqlx::query(&format!(
        "REVOKE CONNECT ON DATABASE \"{database}\" FROM PUBLIC"
    ))
    .execute(pool)
    .await
    .unwrap();
    let role = runtime::RuntimeRole::create(pool).await;
    role.optional("core_charging").await;
    role.optional("core_model").await;
    lmm_core::runtime_database::check(
        &role.pool,
        lmm_core::runtime_database::RequiredCapabilities {
            models: true,
            ..Default::default()
        },
    )
    .await
    .unwrap();
    let directory = root.join("core-config");
    fs::create_dir(&directory).unwrap();
    fs::set_permissions(&directory, fs::Permissions::from_mode(0o700)).unwrap();
    private(&directory.join("runtime.url"), role.url.as_bytes());
    private(&directory.join("upstream.token"), b"synthetic-upstream-key");
    let grants = keys
        .iter()
        .map(|id| format!("{{key_id={id},group='allowed'}}"))
        .collect::<Vec<_>>()
        .join(",");
    let model = format!(
        r#"version=1
input_token_ceiling=100
lease_seconds=60
timeout_seconds=30
max_output_tokens=20
local_streaming=true
credentials=[{{reference={{id=7,revision=1}},secret_file='{}'}}]
key_groups=[{grants}]
[routing]
version=1
groups=[{{name='allowed',enabled=true}}]
upstreams=[{{id=1,endpoint='{}',protocol='openai_chat',credential={{id=7,revision=1}},enabled=true}}]
routes=[{{id=1,model='local-model',group='allowed',enabled=true,speeds=['standard'],targets=[{{id=1,upstream_id=1,upstream_model='provider-model',enabled=true,priority=0,weight=1,speeds=['standard'],payload='adapt',timeout_ms=30000}}],failover={{max_attempts=1,retry_on=[],allow_replay_after_send=false}}}}]
aliases=[]
[prices]
version=1
rules=[{{id=1,model='local-model',group='allowed',speed='standard',rates={{input=2000000,output=4000000,cache_read=500000,cache_write=0,request=0}},group_multiplier_ppm=1000000,speed_multiplier_ppm=1000000}}]
"#,
        directory.join("upstream.token").display(),
        provider.endpoint
    );
    private(&directory.join("model.toml"), model.as_bytes());
    let address = std::net::TcpListener::bind("127.0.0.1:0")
        .unwrap()
        .local_addr()
        .unwrap();
    let config = directory.join("core.toml");
    private(&config,format!("version=1\n[server]\nlisten='{address}'\nworker_threads=2\nmax_inflight=16\ndrain_timeout_seconds=30\n[database]\nurl_file='{}'\nmax_connections=2\n[model]\nconfig_file='{}'\nlocal_http=true\n",directory.join("runtime.url").display(),directory.join("model.toml").display()).as_bytes());
    (address, config, role)
}
struct Peer {
    retained: fs::File,
    stream: Option<Arc<TcpStream>>,
    outcome: &'static str,
    address: SocketAddr,
    kind: usize,
    fd: i32,
    device: u64,
    inode: u64,
}
struct OwnedTask<T>(Option<tokio::task::JoinHandle<T>>);
impl<T: Send + 'static> OwnedTask<T> {
    fn spawn(future: impl Future<Output = T> + Send + 'static) -> Self {
        Self(Some(tokio::spawn(future)))
    }
    async fn finish(mut self) -> T {
        let result = self.0.as_mut().unwrap().await.unwrap();
        self.0.take();
        result
    }
}
impl<T> Drop for OwnedTask<T> {
    fn drop(&mut self) {
        if let Some(task) = &self.0 {
            task.abort();
        }
    }
}
fn observe(core: &Core, server: SocketAddr, peers: &[Peer], started: Instant) -> Value {
    let filter = format!("( src {} and sport = :{} )", server.ip(), server.port());
    let mut child = Command::new("/usr/bin/timeout")
        .args(["--kill-after=1s", "2s", "/usr/bin/ss", "-atinmeH", &filter])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let stdout = child.stdout.take().unwrap();
    let stderr = child.stderr.take().unwrap();
    let read = std::thread::spawn(move || {
        let mut bytes = Vec::new();
        stdout
            .take(2 * 1024 * 1024 + 1)
            .read_to_end(&mut bytes)
            .unwrap();
        bytes
    });
    let err = std::thread::spawn(move || {
        let mut bytes = Vec::new();
        stderr.take(64 * 1024 + 1).read_to_end(&mut bytes).unwrap();
        bytes
    });
    let raw = read.join().unwrap();
    let errors = err.join().unwrap();
    let status = child.wait().unwrap();
    assert!(
        status.success() && raw.len() <= 2 * 1024 * 1024 && errors.len() <= 64 * 1024,
        "ss observer failed, never treat missing observations as absent sockets"
    );
    let text = String::from_utf8(raw).unwrap();
    let mut blocks: Vec<String> = Vec::new();
    for line in text.lines() {
        if line.starts_with(char::is_whitespace) {
            if let Some(last) = blocks.last_mut() {
                last.push('\n');
                last.push_str(line);
            }
        } else {
            blocks.push(line.into());
        }
    }
    let fds = fs::read_dir(format!("/proc/{}/fd", core.pid))
        .unwrap()
        .filter_map(Result::ok)
        .filter_map(|f| fs::read_link(f.path()).ok())
        .filter_map(|p| {
            p.to_str()
                .and_then(|s| s.strip_prefix("socket:["))
                .and_then(|s| s.strip_suffix(']'))
                .map(str::to_owned)
        })
        .collect::<HashSet<_>>();
    let attacker = peers.iter().map(|p| p.address).collect::<HashSet<_>>();
    let mut sockets = Vec::new();
    let mut public_fds = HashSet::new();
    for block in blocks {
        let columns = block
            .lines()
            .next()
            .unwrap()
            .split_whitespace()
            .collect::<Vec<_>>();
        assert!(columns.len() >= 5);
        if columns[0] == "LISTEN" {
            continue;
        }
        let local: SocketAddr = columns[3].parse().unwrap();
        assert_eq!(local, server);
        let peer: SocketAddr = columns[4].parse().unwrap();
        let inode = block
            .split_whitespace()
            .find_map(|s| s.strip_prefix("ino:"))
            .map(str::to_owned);
        let held = inode.as_ref().is_some_and(|i| fds.contains(i));
        if held {
            public_fds.insert(inode.clone().unwrap());
        }
        sockets.push(json!({"server":local,"client":peer,"state":columns[0],"send_queue":columns[2].parse::<u64>().unwrap(),"inode":inode,"core_fd_owned":held,"attack":attacker.contains(&peer),"raw_ss":block}));
    }
    json!({"elapsed_ms":started.elapsed().as_millis(),"pid":core.pid,"fd_count":fs::read_dir(format!("/proc/{}/fd",core.pid)).unwrap().count(),"public_fd_count":public_fds.len(),"sockets":sockets,"memory_current":fs::read_to_string(core.cgroup.join("memory.current")).unwrap().trim().parse::<u64>().unwrap(),"memory_peak":fs::read_to_string(core.cgroup.join("memory.peak")).unwrap().trim().parse::<u64>().unwrap(),"memory_events":fields(&fs::read_to_string(core.cgroup.join("memory.events")).unwrap()),"process_stat":fs::read_to_string(format!("/proc/{}/stat",core.pid)).unwrap(),"host_sockstat":fs::read_to_string("/proc/net/sockstat").unwrap()})
}

// @mark runtime.connection-admission role=test
#[sqlx::test(migrations = false)]
#[ignore = "Requires frozen actual-OS runner, fresh owned PG and cgroup-v2; bounded512 peers/20s"]
async fn bounded_preheader_peers_do_not_exhaust_or_orphan_real_core(pool: PgPool) {
    assert_eq!(
        std::env::var("LMM_PUBLIC_INGRESS_ACCEPTANCE").as_deref(),
        Ok("1")
    );
    let root = PathBuf::from(std::env::var_os("LMM_PUBLIC_INGRESS_ROOT").unwrap());
    assert!(root.is_absolute() && root.canonicalize().unwrap() == root);
    assert_eq!(fs::read_dir(&root).unwrap().count(), 0);
    let pid = std::process::id();
    let stat = fs::read_to_string("/proc/self/stat").unwrap();
    let start_ticks = stat[stat.rfind(')').unwrap() + 1..]
        .split_whitespace()
        .nth(19)
        .unwrap()
        .parse::<u64>()
        .unwrap();
    let identity = json!({"pid":pid,"start_ticks":start_ticks,"boot_id":fs::read_to_string("/proc/sys/kernel/random/boot_id").unwrap().trim()});
    let executable = fs::File::open("/proc/self/exe").unwrap();
    let metadata = executable.metadata().unwrap();
    let actual_sha = format!("{:x}", Sha256::digest(fs::read("/proc/self/exe").unwrap()));
    assert_eq!(actual_sha, std::env::var("LMM_PUBLIC_TEST_SHA256").unwrap());
    let path = fs::read_to_string("/proc/self/cgroup").unwrap();
    let relative = path
        .lines()
        .find_map(|line| line.strip_prefix("0::"))
        .unwrap();
    let group = PathBuf::from("/sys/fs/cgroup").join(relative.trim_start_matches('/'));
    let mut limits = serde_json::Map::new();
    for (name, expected) in [
        ("cpu.max", "100000 100000"),
        ("memory.max", "536870912"),
        ("memory.swap.max", "0"),
        ("pids.max", "128"),
    ] {
        let actual = fs::read_to_string(group.join(name))
            .unwrap()
            .trim()
            .to_owned();
        assert_eq!(actual, expected);
        limits.insert(name.into(), json!(actual));
    }
    save(
        &root,
        "test-owner.json",
        json!({"identity":identity,"executed_sha256":actual_sha,"device":metadata.dev(),"inode":metadata.ino(),"executed_path":fs::read_link("/proc/self/exe").unwrap(),"stat":stat,"cgroup":group,"limits":limits}),
    );
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 100_000).await;
    let provider = Provider::start(true).await;
    let (address, config, role) = configure(&pool, &provider, &[key_id], &root).await;
    role.pool.close().await;
    let mut core = Core::start(&config, address, &root).await;
    let h1 = reqwest::Client::builder()
        .no_proxy()
        .http1_only()
        .timeout(Duration::from_secs(5))
        .build()
        .unwrap();
    let h2 = reqwest::Client::builder()
        .no_proxy()
        .local_address("127.0.0.2".parse::<std::net::IpAddr>().unwrap())
        .http2_prior_knowledge()
        .timeout(Duration::from_secs(20))
        .build()
        .unwrap();
    let mut normal = Vec::new();
    for (client, stream, label) in [
        (&h1, false, "ingress-pre-h1"),
        (&h2, true, "ingress-pre-h2"),
    ] {
        let row = model_client::complete(
            client.clone(),
            core.endpoint.clone(),
            key.clone(),
            label.into(),
            0,
            stream,
            tokio::time::Instant::now(),
        )
        .await;
        assert!(row.success);
        normal.push(row);
    }
    let long = h2
        .post(format!("{}/v1/chat/completions", core.endpoint))
        .bearer_auth(&key)
        .header("idempotency-key", "ingress-long-h2")
        .json(&model_client::body("stream-pause", true))
        .send()
        .await
        .unwrap();
    assert_eq!(long.status(), StatusCode::OK);
    let long_id = model_client::valid_id(&long).unwrap();
    let mut slow_body = TcpStream::connect(address).await.unwrap();
    let body = serde_json::to_vec(&model_client::body("resource", false)).unwrap();
    let split = body.len() / 2;
    let head = format!(
        "POST /v1/chat/completions HTTP/1.1\r\nHost: {address}\r\nAuthorization: Bearer {key}\r\nIdempotency-Key: ingress-slow-body\r\nContent-Type: application/json\r\nContent-Length: {}\r\n\r\n",
        body.len()
    );
    slow_body.write_all(head.as_bytes()).await.unwrap();
    slow_body.write_all(&body[..split]).await.unwrap();
    let baseline = observe(&core, address, &[], Instant::now());
    let h2_socket = baseline["sockets"]
        .as_array()
        .unwrap()
        .iter()
        .find(|x| x["client"].as_str().unwrap().starts_with("127.0.0.2:"))
        .unwrap()
        .clone();
    let started = Instant::now();
    let stop = Arc::new(AtomicBool::new(false));
    let healthy_client = h1.clone();
    let healthy_endpoint = core.endpoint.clone();
    let healthy_key = key.clone();
    let healthy = OwnedTask::spawn(async move {
        let mut rows = Vec::new();
        let begin = tokio::time::Instant::from_std(started);
        for ordinal in 0..40 {
            let due = begin + Duration::from_millis(500 * ordinal as u64);
            tokio::time::sleep_until(due).await;
            rows.push(
                model_client::complete(
                    healthy_client.clone(),
                    healthy_endpoint.clone(),
                    healthy_key.clone(),
                    "ingress-health".into(),
                    ordinal,
                    ordinal % 5 == 4,
                    due,
                )
                .await,
            );
        }
        rows
    });
    let stream_gate = provider.stream_gate.clone();
    let long_controls = OwnedTask::spawn(async move {
        tokio::time::sleep_until(tokio::time::Instant::from_std(started) + Duration::from_secs(12))
            .await;
        stream_gate.add_permits(1);
        let bytes = long.bytes().await.unwrap();
        assert!(model_client::valid_complete_body(&bytes, true, &long_id));
        use tokio::io::AsyncReadExt;
        let rejected = tokio::time::timeout(Duration::from_secs(3), async {
            let mut header = Vec::new();
            while !header.ends_with(b"\r\n\r\n") {
                match slow_body.read_u8().await {
                    Ok(byte) => {
                        header.push(byte);
                        assert!(header.len() < 16384);
                    }
                    Err(error)
                        if matches!(
                            error.kind(),
                            std::io::ErrorKind::ConnectionReset | std::io::ErrorKind::UnexpectedEof
                        ) =>
                    {
                        return "closed-on-input-deadline".to_owned();
                    }
                    Err(error) => panic!("unexpected slow-body rejection: {error}"),
                }
            }
            let header = String::from_utf8(header).unwrap();
            assert!(
                header.starts_with("HTTP/1.1 400 "),
                "Core's existing10s body policy must reject incomplete body before model admission"
            );
            "http400-input-deadline".to_owned()
        })
        .await
        .unwrap();
        (long_id, rejected, slow_body)
    });
    let mut peers = Vec::new();
    let mut used = HashSet::new();
    let mut drips = Vec::new();
    let mut failures = Vec::new();
    let mut safety_abort = false;
    let mut offering_samples = Vec::new();
    let mut progress = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(root.join("connect-progress.jsonl"))
        .unwrap();
    for ordinal in 0..512 {
        let mut chosen = None;
        for _ in 0..64 {
            let socket = TcpSocket::new_v4().unwrap();
            socket.bind("127.0.0.1:0".parse().unwrap()).unwrap();
            let local = socket.local_addr().unwrap();
            if used.insert(local) {
                chosen = Some((socket, local));
                break;
            }
        }
        let (socket, local) = chosen.expect("bounded unique raw endpoint allocation");
        // Keep the same socket alive even when overflow RST wins the connect race.
        // This is one TCP attempt, with no retry or SO_ERROR consumption.
        let retained = fs::File::from(socket.as_fd().try_clone_to_owned().unwrap());
        let fd = retained.as_raw_fd();
        let metadata = retained.metadata().unwrap();
        writeln!(progress, "{}", json!({"phase":"before-connect","ordinal":ordinal,"client":local,"fd":fd,"device":metadata.dev(),"inode":metadata.ino(),"elapsed_ms":started.elapsed().as_millis()})).unwrap();
        let (stream, outcome) =
            match tokio::time::timeout(Duration::from_secs(5), socket.connect(address)).await {
                Ok(Ok(stream)) => (Some(Arc::new(stream)), "connected"),
                Ok(Err(error)) if error.kind() == std::io::ErrorKind::ConnectionReset => {
                    (None, "reset")
                }
                Ok(Err(error)) if error.kind() == std::io::ErrorKind::ConnectionRefused => {
                    (None, "refused")
                }
                Err(_) => (None, "timeout"),
                Ok(Err(error)) => {
                    panic!("unexpected single connect outcome at ordinal{ordinal}: {error}")
                }
            };
        writeln!(progress, "{}", json!({"phase":"after-connect","ordinal":ordinal,"client":local,"outcome":outcome,"elapsed_ms":started.elapsed().as_millis()})).unwrap();
        let kind = ordinal % 4;
        if kind > 0
            && let Some(stream) = &stream
        {
            let bytes = if kind == 3 {
                &b"PRI * HTTP/2.0\r\n"[..]
            } else {
                &b"GET /health/ready HTTP/1.1\r\nHost: test\r\nX-Incomplete:"[..]
            };
            if stream.writable().await.is_ok() {
                let _ = stream.try_write(bytes);
            }
        }
        if kind == 2
            && let Some(stream) = &stream
        {
            let socket = stream.clone();
            let stopped = stop.clone();
            drips.push(tokio::spawn(async move {
                while !stopped.load(Ordering::SeqCst) {
                    if tokio::time::timeout(Duration::from_secs(1), socket.writable())
                        .await
                        .is_err()
                    {
                        break;
                    }
                    if socket.try_write(b"a").is_err() {
                        break;
                    }
                    tokio::time::sleep(Duration::from_millis(500)).await;
                }
            }));
        }
        peers.push(Peer {
            retained,
            stream,
            outcome,
            address: local,
            kind,
            fd,
            device: metadata.dev(),
            inode: metadata.ino(),
        });
        if ordinal % 16 == 15 {
            offering_samples.push(observe(&core, address, &peers, started));
            let memory = fs::read_to_string(core.cgroup.join("memory.current"))
                .unwrap()
                .trim()
                .parse::<u64>()
                .unwrap();
            if memory >= 160 * 1024 * 1024 {
                safety_abort = true;
                stop.store(true, Ordering::SeqCst);
                break;
            }
            tokio::task::yield_now().await;
        }
    }
    save(
        &root,
        "offered.json",
        json!({"scheduled":512,"attempted":peers.len(),"connected":peers.iter().filter(|p|p.stream.is_some()).count(),"round_robin_classes":["silent","partial-h1","drip-h1","partial-h2-preface"],"unique_historical_clients":used.len(),"root_memory_guard":160*1024*1024,"hardware_memory_max":256*1024*1024,"safety_abort":safety_abort,"peers":peers.iter().map(|p|json!({"client":p.address,"kind":p.kind,"fd":p.fd,"device":p.device,"inode":p.inode,"connect_outcome":p.outcome})).collect::<Vec<_>>()}),
    );
    let observations = async {
        let mut samples = Vec::new();
        loop {
            let row = observe(&core, address, &peers, started);
            if row["memory_current"].as_u64().unwrap() >= 160 * 1024 * 1024 {
                stop.store(true, Ordering::SeqCst);
            }
            samples.push(row);
            if started.elapsed() >= Duration::from_secs(20) || stop.load(Ordering::SeqCst) {
                break;
            }
            tokio::time::sleep(Duration::from_millis(200)).await;
        }
        samples
    };
    let (traffic_result, samples) = {
        let traffic = async { tokio::join!(healthy.finish(), long_controls.finish()) };
        tokio::pin!(traffic);
        tokio::pin!(observations);
        tokio::select! {
            value=&mut traffic=>{let samples=observations.await;(Some(value),samples)},
            samples=&mut observations=>{
                if safety_abort||stop.load(Ordering::SeqCst){(None,samples)}else{(Some(traffic.await),samples)}
            }
        }
    };
    offering_samples.extend(samples);
    let samples = offering_samples;
    if safety_abort || stop.load(Ordering::SeqCst) || traffic_result.is_none() {
        stop.store(true, Ordering::SeqCst);
        for task in drips {
            task.abort();
            let _ = task.await;
        }
        drop(peers);
        save(
            &root,
            "memory-safeguard.json",
            json!({"safety_abort":true,"diagnostic_threshold":160*1024*1024,"hardware_memory_max":256*1024*1024,"samples":samples}),
        );
        core.stop().await;
        role.cleanup().await;
        panic!("diagnostic memory safeguard aborted raw clients; not a valid resource PASS");
    }
    let (healthy, (long_id, body_rejection, slow_body)) = traffic_result.unwrap();
    normal.extend(healthy);
    let retained = observe(&core, address, &peers, started);
    let matching_h2 = retained["sockets"].as_array().unwrap().iter().any(|x| {
        x["client"] == h2_socket["client"]
            && x["inode"] == h2_socket["inode"]
            && x["state"] == "ESTAB"
    });
    if !matching_h2 {
        failures.push("established healthy H2 TCP was replaced or closed".to_owned());
    }
    for p in &peers {
        assert_eq!(p.retained.as_raw_fd(), p.fd);
        let m = fs::metadata(format!("/proc/self/fd/{}", p.fd)).unwrap();
        assert_eq!((m.dev(), m.ino()), (p.device, p.inode));
    }
    let max_public = samples
        .iter()
        .map(|x| x["public_fd_count"].as_u64().unwrap())
        .max()
        .unwrap();
    assert!(
        max_public >= 64,
        "raw offers never exercised the connection capacity"
    );
    let admitted_kinds = samples
        .iter()
        .flat_map(|sample| sample["sockets"].as_array().unwrap())
        .filter(|socket| socket["core_fd_owned"] == true && socket["attack"] == true)
        .filter_map(|socket| {
            peers
                .iter()
                .find(|peer| json!(peer.address) == socket["client"])
                .map(|peer| peer.kind)
        })
        .collect::<HashSet<_>>();
    assert_eq!(
        admitted_kinds.len(),
        4,
        "all four raw classes must positively exercise Core admission"
    );
    let kernel_retained = retained["sockets"]
        .as_array()
        .unwrap()
        .iter()
        .filter(|x| x["attack"] == true)
        .count();
    if max_public > 65 {
        failures.push(format!("public FD count {max_public} exceeded64+1"));
    }
    if kernel_retained > 0 {
        failures.push(format!("{kernel_retained} expired/rejected owned TCP states survived while clients still retained"));
    }
    if safety_abort || stop.load(Ordering::SeqCst) {
        failures
            .push("diagnostic memory safeguard triggered; not a successful resource run".into());
    }
    if peers.len() != 512 {
        failures.push("offered fewer than512 raw peers".into());
    }
    save(
        &root,
        "retained-gate.json",
        json!({"elapsed_ms":started.elapsed().as_millis(),"clients_retained":peers.len(),"clients_dropped":false,"admitted_raw_classes":admitted_kinds.len(),"max_public_fd_count":max_public,"kernel_attack_rows":kernel_retained,"same_h2_socket":matching_h2,"sample":retained,"samples":samples,"failures":failures}),
    );
    stop.store(true, Ordering::SeqCst);
    for task in drips {
        tokio::time::timeout(Duration::from_secs(2), task)
            .await
            .unwrap()
            .unwrap();
    }
    drop(peers);
    tokio::time::sleep(Duration::from_secs(1)).await;
    for (client, stream, label) in [
        (&h1, false, "ingress-recovered-h1"),
        (&h2, true, "ingress-recovered-h2"),
    ] {
        let row = model_client::complete(
            client.clone(),
            core.endpoint.clone(),
            key.clone(),
            label.into(),
            0,
            stream,
            tokio::time::Instant::now(),
        )
        .await;
        normal.push(row);
    }
    let mut ids = normal
        .iter()
        .filter(|r| r.success)
        .map(|r| r.id.clone().unwrap())
        .collect::<Vec<_>>();
    if normal.iter().any(|r| !r.success) {
        failures.push("healthy complete request failed".into());
    }
    assert_eq!(normal.len(), 44);
    ids.push(long_id);
    model_client::verify_paid(&pool, account, &ids).await;
    assert_eq!(ids.len(), 45);
    assert_eq!(provider.hits.load(Ordering::SeqCst), 45);
    assert!(
        provider
            .auth
            .lock()
            .unwrap()
            .iter()
            .all(|x| x == "Bearer synthetic-upstream-key")
    );
    assert_eq!(
        provider.stream_closed.load(Ordering::SeqCst),
        normal.iter().filter(|x| x.stream).count() + 1
    );
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!(
        (balance.available_units, balance.reserved_units),
        (100000 - 45 * 49, 0)
    );
    let all: i64 = sqlx::query_scalar("SELECT count(*) FROM core_charging.requests")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(all, 45);
    let outcomes = normal.iter().map(|r| r.value()).collect::<Vec<_>>();
    let latency = normal.iter().map(|r| r.latency_ms).collect::<Vec<_>>();
    let p95 = model_client::percentile(&latency, 95);
    let p99 = model_client::percentile(&latency, 99);
    if p95 > 1000 || p99 > 2000 {
        failures.push("healthy latency exceeded existing1s p95/2s p99 budget".into());
    }
    let final_sample = observe(&core, address, &[], started);
    save(
        &root,
        "health-and-funds.json",
        json!({"complete":45,"normal_outcomes":outcomes,"p95_ms":p95,"p99_ms":p99,"captured_each":49,"surplus_each":91,"wallet":balance.available_units,"reserved":0,"provider_hits":45,"raw_model_dispatch":0,"rejected_incomplete_body":body_rejection,"post_client_drop":final_sample}),
    );
    drop(h1);
    drop(h2);
    drop(slow_body);
    core.stop().await;
    role.cleanup().await;
    save(
        &root,
        "result.json",
        json!({"result":if failures.is_empty(){"PASS"}else{"FAIL"},"offered":512,"minimum_horizon_seconds":20,"hardware_core_cpu":1,"hardware_core_memory":256*1024*1024,"max_public_fd_count":max_public,"retained_expired_kernel_rows":kernel_retained,"complete_models":45,"same_healthy_h2_tcp":matching_h2,"products_reaped":true,"failures":failures}),
    );
    assert!(
        failures.is_empty(),
        "public ingress contract violated; preserve private receipts"
    );
}
