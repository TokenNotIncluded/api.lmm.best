//! Opt-in resource acceptance: real Core/PG accounting, synthetic external provider.
//! The runner owns a fresh PG fixture and an explicitly bound release executable.
//! This diagnostic gate does not claim real-provider, combined Go, or old-version acceptance.
use super::*;
use std::{collections::HashSet, io::Write, sync::atomic::AtomicU8};
use tokio::{sync::oneshot, task::JoinSet};

const OPENING: i64 = 10_000_000;
const PERIOD: Duration = Duration::from_millis(200);
const MEMORY_MAX: u64 = 256 * 1024 * 1024;
const CAPACITY: usize = 16;
const PLANNED_SECONDS: u64 = 60 + 300 + 60 + 120;

fn systemctl(arguments: &[&str]) -> String {
    let output = Command::new("/usr/bin/systemctl")
        .arg("--user")
        .args(arguments)
        .output()
        .expect("systemctl user manager must be available; no unconstrained fallback");
    assert!(
        output.status.success(),
        "systemctl failed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    String::from_utf8(output.stdout).unwrap().trim().to_owned()
}

fn fields(raw: &str) -> Value {
    let mut value = serde_json::Map::new();
    for line in raw.lines() {
        let mut columns = line.split_whitespace();
        if let (Some(key), Some(number)) = (columns.next(), columns.next())
            && let Ok(number) = number.parse::<u64>()
        {
            value.insert(key.trim_end_matches(':').to_owned(), json!(number));
        }
    }
    Value::Object(value)
}

fn sample(pid: u32, cgroup: &Path, started: Instant, phase: u8) -> Value {
    let tasks = fs::read_dir(format!("/proc/{pid}/task"))
        .unwrap()
        .map(|entry| {
            let entry = entry.unwrap();
            match fs::read_to_string(entry.path().join("stat")) {
                Ok(stat) => json!({"tid":entry.file_name().to_string_lossy(),"stat":stat}),
                Err(error) => json!({"tid":entry.file_name().to_string_lossy(),"observation_error":format!("{:?}",error.kind())}),
            }
        })
        .collect::<Vec<_>>();
    json!({
        "elapsed_ms":started.elapsed().as_millis(), "phase":phase, "pid":pid,
        "process_stat":fs::read_to_string(format!("/proc/{pid}/stat")).unwrap(),
        "status":fields(&fs::read_to_string(format!("/proc/{pid}/status")).unwrap()),
        "smaps_rollup_kib":fields(&fs::read_to_string(format!("/proc/{pid}/smaps_rollup")).unwrap()),
        "fd_count":fs::read_dir(format!("/proc/{pid}/fd")).unwrap().count(), "threads":tasks,
        "cpu_stat":fields(&fs::read_to_string(cgroup.join("cpu.stat")).unwrap()),
        "memory_current":fs::read_to_string(cgroup.join("memory.current")).unwrap().trim().parse::<u64>().unwrap(),
        "memory_peak":fs::read_to_string(cgroup.join("memory.peak")).unwrap().trim().parse::<u64>().unwrap(),
        "memory_events":fields(&fs::read_to_string(cgroup.join("memory.events")).unwrap()),
        "pids_current":fs::read_to_string(cgroup.join("pids.current")).unwrap().trim().parse::<u64>().unwrap(),
        "host_stat":fs::read_to_string("/proc/stat").unwrap(),
        "host_meminfo_kib":fields(&fs::read_to_string("/proc/meminfo").unwrap()),
        "generator_status":fields(&fs::read_to_string("/proc/self/status").unwrap())
    })
}

struct ResourceHost {
    prepared: Host,
    unit: String,
    launcher: Option<Child>,
    pid: u32,
    start_ticks: String,
    cgroup: PathBuf,
    generation: usize,
}

impl Drop for ResourceHost {
    fn drop(&mut self) {
        // The unique transient unit is the only cleanup authority, including failed tests.
        if self.launcher.is_some() {
            let _ = Command::new("/usr/bin/systemctl")
                .args(["--user", "stop", &self.unit])
                .status();
            if let Some(mut launcher) = self.launcher.take() {
                let _ = launcher.wait();
            }
        }
    }
}

impl ResourceHost {
    async fn start(prepared: Host, client: &reqwest::Client) -> Self {
        assert!(prepared.child.is_none());
        // Exclude the provisioning helper's connections from the runtime-role census.
        prepared.role.as_ref().unwrap().pool.close().await;
        let mut host = Self {
            prepared,
            unit: String::new(),
            launcher: None,
            pid: 0,
            start_ticks: String::new(),
            cgroup: PathBuf::new(),
            generation: 0,
        };
        host.launch(client).await;
        host
    }

    async fn launch(&mut self, client: &reqwest::Client) {
        assert!(self.launcher.is_none());
        self.generation += 1;
        self.pid = 0;
        self.unit = format!(
            "lmm-resource-{}-{:016x}.service",
            std::process::id(),
            rand::random::<u64>()
        );
        let binary = core_binary();
        // Persist cleanup authority BEFORE spawning systemd-run: SIGKILL skips Rust Drop.
        let expected = std::env::var("LMM_MODEL_PROCESS_CORE_SHA256").unwrap();
        let owner_pid = std::process::id();
        private(
            &self.prepared.directory.join(format!("resource-unit-{}.json", self.generation)),
            &serde_json::to_vec(&json!({
                "format":"lmm-resource-unit-allocation-v1", "unit":self.unit,
                "generation":self.generation,"binary_path":binary,"binary_sha256":expected,
                "owner":{"pid":owner_pid,"start_ticks":process_start_ticks(owner_pid).unwrap().parse::<u64>().unwrap(),
                    "boot_id":fs::read_to_string("/proc/sys/kernel/random/boot_id").unwrap().trim()}
            })).unwrap(),
        );
        let held = fs::File::open(&binary).unwrap();
        let identity = held.metadata().unwrap();
        let output = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(
                self.prepared
                    .directory
                    .join(format!("resource-core-{}.log", self.generation)),
            )
            .unwrap();
        self.launcher = Some(
            Command::new("/usr/bin/systemd-run")
                .args([
                    "--user",
                    "--wait",
                    "--pipe",
                    "--service-type=exec",
                    "--unit",
                    &self.unit,
                    "--property=CPUQuota=100%",
                    "--property=CPUQuotaPeriodSec=100ms",
                    "--property=MemoryMax=256M",
                    "--property=MemorySwapMax=0",
                    "--property=TasksMax=128",
                    "--property=RuntimeMaxSec=1200",
                    "--property=TimeoutStopSec=10s",
                    "--property=KillMode=control-group",
                    "--",
                    "/usr/bin/env",
                    "-i",
                    "PATH=/usr/bin:/bin",
                ])
                .arg(&binary)
                .arg("--config")
                .arg(&self.prepared.config)
                .stdin(Stdio::null())
                .stdout(output.try_clone().unwrap())
                .stderr(output)
                .spawn()
                .unwrap(),
        );
        let started = Instant::now();
        while started.elapsed() < Duration::from_secs(15) {
            assert!(
                self.launcher
                    .as_mut()
                    .unwrap()
                    .try_wait()
                    .unwrap()
                    .is_none(),
                "resource launcher exited; inspect retained log"
            );
            let output = Command::new("/usr/bin/systemctl")
                .args([
                    "--user",
                    "show",
                    "--property=MainPID",
                    "--value",
                    &self.unit,
                ])
                .output()
                .unwrap();
            if output.status.success()
                && let Ok(pid) = String::from_utf8(output.stdout)
                    .unwrap()
                    .trim()
                    .parse::<u32>()
                && pid > 0
            {
                self.pid = pid;
                break;
            }
            tokio::time::sleep(Duration::from_millis(25)).await;
        }
        assert!(
            self.pid > 0,
            "no actual Core MainPID within readiness budget"
        );
        assert_ne!(
            self.pid,
            self.launcher.as_ref().unwrap().id(),
            "launcher PID is not Core PID"
        );
        self.start_ticks = process_start_ticks(self.pid).unwrap();
        let group = systemctl(&["show", "--property=ControlGroup", "--value", &self.unit]);
        assert!(group.starts_with('/') && !group.contains("..") && group != "/");
        assert_eq!(
            fs::read_to_string(format!("/proc/{}/cgroup", self.pid))
                .unwrap()
                .trim(),
            format!("0::{group}")
        );
        self.cgroup = Path::new("/sys/fs/cgroup").join(group.trim_start_matches('/'));
        let cpu = fs::read_to_string(self.cgroup.join("cpu.max")).unwrap();
        let quota = cpu
            .split_whitespace()
            .map(|x| x.parse::<u64>().unwrap())
            .collect::<Vec<_>>();
        assert_eq!(
            quota,
            vec![100_000, 100_000],
            "actual cgroup must enforce exactly one CPU"
        );
        assert_eq!(
            fs::read_to_string(self.cgroup.join("memory.max"))
                .unwrap()
                .trim(),
            MEMORY_MAX.to_string()
        );
        assert_eq!(
            fs::read_to_string(self.cgroup.join("memory.swap.max"))
                .unwrap()
                .trim(),
            "0"
        );
        assert_eq!(
            fs::read_to_string(self.cgroup.join("pids.max"))
                .unwrap()
                .trim(),
            "128"
        );
        loop {
            self.alive();
            if let Ok(response) = client
                .get(format!("{}/health/ready", self.prepared.endpoint))
                .send()
                .await
                && response.status() == StatusCode::OK
            {
                let body: Value = response.json().await.unwrap();
                assert_eq!(body["capabilities"]["models"], true);
                assert_eq!(body["capabilities"]["models_streaming"], true);
                break;
            }
            assert!(
                started.elapsed() < Duration::from_secs(15),
                "Core readiness exceeded15s"
            );
            tokio::time::sleep(Duration::from_millis(25)).await;
        }
        assert!(started.elapsed() < Duration::from_secs(15));
        let expected = std::env::var("LMM_MODEL_PROCESS_CORE_SHA256").unwrap();
        let observed = executable_observation(self.pid, &expected);
        assert_eq!(observed["matches_expected_sha256"], true);
        assert_eq!(
            fs::read_link(format!("/proc/{}/exe", self.pid)).unwrap(),
            binary
        );
        let actual = fs::metadata(format!("/proc/{}/exe", self.pid)).unwrap();
        assert_eq!(
            (actual.dev(), actual.ino()),
            (identity.dev(), identity.ino())
        );
        let current = fs::metadata(&binary).unwrap();
        assert_eq!(
            (current.dev(), current.ino()),
            (identity.dev(), identity.ino())
        );
        private(&self.prepared.directory.join(format!("resource-identity-{}.json",self.generation)),
            &serde_json::to_vec(&json!({"unit":self.unit,"actual_pid":self.pid,"launcher_pid":self.launcher.as_ref().unwrap().id(),
                "start_ticks":self.start_ticks,"cgroup":self.cgroup,"cpu_max":cpu,"memory_max":MEMORY_MAX,
                "swap_max":0,"tasks_max":128,"ready_ms":started.elapsed().as_millis(),"executable":observed,
                "database_in_core_cgroup":false,"upstream_and_generator_in_core_cgroup":false})).unwrap());
    }

    fn alive(&mut self) {
        assert!(
            self.launcher
                .as_mut()
                .unwrap()
                .try_wait()
                .unwrap()
                .is_none(),
            "Core launcher exited unexpectedly"
        );
        assert_eq!(
            process_start_ticks(self.pid).unwrap(),
            self.start_ticks,
            "actual Core exited or PID reused"
        );
    }

    async fn stop(&mut self) {
        self.alive();
        assert_eq!(
            systemctl(&["show", "--property=MainPID", "--value", &self.unit]),
            self.pid.to_string()
        );
        assert!(
            Command::new("/usr/bin/kill")
                .args(["-TERM", &self.pid.to_string()])
                .status()
                .unwrap()
                .success()
        );
        let deadline = Instant::now() + Duration::from_secs(10);
        loop {
            if let Some(status) = self.launcher.as_mut().unwrap().try_wait().unwrap() {
                assert!(status.success(), "Core/launcher must exit normally");
                break;
            }
            assert!(
                Instant::now() < deadline,
                "resource Core did not drain within10s"
            );
            tokio::time::sleep(Duration::from_millis(25)).await;
        }
        self.launcher.take();
        assert!(
            process_start_ticks(self.pid).is_err(),
            "actual Core still exists after normal exit"
        );
    }

    async fn finish(mut self) {
        self.stop().await;
        self.prepared.role.take().unwrap().cleanup().await;
    }
}

#[derive(Clone)]
struct Outcome {
    ordinal: usize,
    stream: bool,
    status: u16,
    code: String,
    id: Option<String>,
    success: bool,
    latency_ms: u64,
    submission_lag_ms: u64,
}
impl Outcome {
    fn value(&self) -> Value {
        json!({"ordinal":self.ordinal,"stream":self.stream,"status":self.status,
        "code":self.code,"request_id":self.id,"complete_success":self.success,
        "latency_ms":self.latency_ms,"submission_lag_ms":self.submission_lag_ms})
    }
}

fn body(prompt: &str, stream: bool) -> Value {
    json!({"model":"local-model","messages":[{"role":"user","content":prompt}],"max_tokens":20,
        "stream":stream,"stream_options":if stream {json!({"include_usage":true})} else {Value::Null}})
}
fn valid_id(response: &reqwest::Response) -> Option<String> {
    response.headers().get("x-request-id").map(|raw| {
        let id = raw.to_str().unwrap().to_owned();
        assert_eq!(id.len(), 64);
        assert!(id.bytes().all(|x| x.is_ascii_hexdigit()));
        id
    })
}

async fn complete(
    client: reqwest::Client,
    endpoint: String,
    key: String,
    phase: String,
    ordinal: usize,
    stream: bool,
    due: tokio::time::Instant,
) -> Outcome {
    let lag = due.elapsed().as_millis() as u64;
    let mut result = Outcome {
        ordinal,
        stream,
        status: 0,
        code: String::new(),
        id: None,
        success: false,
        latency_ms: 0,
        submission_lag_ms: lag,
    };
    let response = client
        .post(format!("{endpoint}/v1/chat/completions"))
        .bearer_auth(key)
        .header("idempotency-key", format!("resource-{phase}-{ordinal:06}"))
        .json(&body("hello", stream))
        .send()
        .await;
    match response {
        Err(error) => {
            result.code = if error.is_timeout() {
                "client_timeout"
            } else {
                "transport_error"
            }
            .into()
        }
        Ok(mut response) => {
            result.status = response.status().as_u16();
            result.id = valid_id(&response);
            let mut bytes = Vec::new();
            let mut read_ok = true;
            loop {
                match response.chunk().await {
                    Ok(Some(chunk)) => {
                        bytes.extend_from_slice(&chunk);
                        if bytes.len() > 16_384 {
                            read_ok = false;
                            break;
                        }
                    }
                    Ok(None) => break,
                    Err(_) => {
                        read_ok = false;
                        break;
                    }
                }
            }
            if !read_ok {
                result.code = "incomplete_or_oversized_body".into();
            } else if result.status == 200 {
                let text = String::from_utf8(bytes).unwrap();
                let valid = if stream {
                    text.ends_with("data: [DONE]\n\n")
                        && text.contains("local stream answer")
                        && text
                            .lines()
                            .filter_map(|line| line.strip_prefix("data: "))
                            .filter_map(|line| serde_json::from_str::<Value>(line).ok())
                            .any(|v| {
                                v["usage"]["prompt_tokens"] == 10
                                    && v["usage"]["completion_tokens"] == 20
                                    && v["usage"]["prompt_tokens_details"]["cached_tokens"] == 2
                            })
                } else {
                    let value: Value = serde_json::from_str(&text).unwrap();
                    value["choices"][0]["message"]["content"] == "local synthetic answer"
                        && value["usage"]["prompt_tokens"] == 10
                        && value["usage"]["completion_tokens"] == 20
                        && value["usage"]["prompt_tokens_details"]["cached_tokens"] == 2
                };
                result.success = valid && result.id.is_some();
                if !result.success {
                    result.code = "invalid_complete_response".into();
                }
            } else {
                let value: Value = serde_json::from_slice(&bytes).unwrap();
                result.code = value["error"]["code"]
                    .as_str()
                    .unwrap_or("unclassified_error")
                    .into();
            }
        }
    }
    result.latency_ms = due.elapsed().as_millis() as u64;
    result
}

fn percentile(values: &[u64], numerator: usize) -> u64 {
    let mut values = values.to_vec();
    values.sort_unstable();
    values[(values.len() * numerator).div_ceil(100).saturating_sub(1)]
}

async fn verify_paid(pool: &PgPool, account: i64, ids: &[String]) {
    assert_eq!(
        ids.iter().collect::<HashSet<_>>().len(),
        ids.len(),
        "successful logical requests reused a durable ID"
    );
    let rows:Vec<Value>=sqlx::query_scalar("SELECT jsonb_build_object('id',r.id,'state',r.state,'reserved',r.reserved,'settled',r.settled,'payer',r.payer_account_id,'input',r.input_tokens,'cached',r.cached_tokens,'output',r.output_tokens,'hold_reserved',h.reserved,'captured',h.captured,'closed',h.closed,'reservation_journal',h.reservation_journal_id IS NOT NULL,'capture_journal',h.capture_journal_id IS NOT NULL,'wallet_surplus',(SELECT sum(e.delta_units)::bigint FROM core_billing.ledger_entries e JOIN core_billing.ledger_accounts a ON a.id=e.ledger_account_id WHERE e.journal_id=h.capture_journal_id AND a.owner_account_id=r.payer_account_id AND a.bucket='wallet')) FROM core_charging.requests r JOIN core_charging.ledger_holds h ON h.request_id=r.id WHERE r.id=ANY($1)")
        .bind(ids).fetch_all(pool).await.unwrap();
    assert_eq!(rows.len(), ids.len());
    for row in rows {
        assert_eq!(row["state"], "settled");
        assert_eq!(row["reserved"], 140);
        assert_eq!(row["settled"], 49);
        assert_eq!(row["payer"], account);
        assert_eq!(row["input"], 10);
        assert_eq!(row["cached"], 2);
        assert_eq!(row["output"], 20);
        assert_eq!(row["hold_reserved"], 140);
        assert_eq!(row["captured"], 49);
        assert_eq!(row["closed"], true);
        assert_eq!(row["reservation_journal"], true);
        assert_eq!(row["capture_journal"], true);
        assert_eq!(row["wallet_surplus"], 91);
    }
    balanced(pool).await;
}

// @mark model.resource-acceptance role=load
async fn load_phase(
    host: &mut ResourceHost,
    pool: &PgPool,
    seed: &Seed,
    client: &reqwest::Client,
    label: &str,
    seconds: usize,
) -> Vec<Outcome> {
    let start = tokio::time::Instant::now();
    let offered = seconds * 5;
    let mut tasks = JoinSet::new();
    for ordinal in 0..offered {
        let due = start + PERIOD * (ordinal as u32);
        tokio::time::sleep_until(due).await;
        tasks.spawn(complete(
            client.clone(),
            host.prepared.endpoint.clone(),
            seed.key.clone(),
            label.into(),
            ordinal,
            ordinal % 5 == 4,
            due,
        ));
    }
    let mut outcomes = Vec::with_capacity(offered);
    while let Some(result) = tasks.join_next().await {
        outcomes.push(result.unwrap());
    }
    tokio::time::sleep_until(start + Duration::from_secs(seconds as u64)).await;
    outcomes.sort_by_key(|x| x.ordinal);
    let successes = outcomes.iter().filter(|x| x.success).count();
    let latencies = outcomes.iter().map(|x| x.latency_ms).collect::<Vec<_>>();
    private(&host.prepared.directory.join(format!("phase-{label}.json")),&serde_json::to_vec(&json!({
        "phase":label,"scheduled_seconds":seconds,"elapsed_ms":start.elapsed().as_millis(),"offered":offered,
        "complete_successes":successes,"successful_rps":successes as f64/seconds as f64,
        "p95_ms":percentile(&latencies,95),"p99_ms":percentile(&latencies,99),
        "outcomes":outcomes.iter().map(Outcome::value).collect::<Vec<_>>()
    })).unwrap());
    assert_eq!(outcomes.len(), offered);
    assert!(
        successes * 100 >= offered * 99,
        "successful throughput fell below frozen99% gate"
    );
    assert!(percentile(&latencies, 95) <= 1000);
    assert!(percentile(&latencies, 99) <= 2000);
    for outcome in &outcomes {
        assert!(
            outcome.submission_lag_ms < 200,
            "generator missed its fixed200ms offered cadence"
        );
        assert!(
            outcome.success
                || (outcome.status == 503
                    && outcome.code == "core_not_accepting"
                    && outcome.id.is_none()),
            "non-capacity failure is not successful resource protection"
        );
    }
    host.alive();
    verify_paid(
        pool,
        seed.account,
        &outcomes
            .iter()
            .filter(|x| x.success)
            .map(|x| x.id.clone().unwrap())
            .collect::<Vec<_>>(),
    )
    .await;
    outcomes
}

struct ReleaseProducerGate {
    gate: Arc<tokio::sync::Semaphore>,
    dispatches: Arc<std::sync::atomic::AtomicUsize>,
}
impl Drop for ReleaseProducerGate {
    fn drop(&mut self) {
        self.gate
            .add_permits(self.dispatches.load(Ordering::SeqCst) + 1);
    }
}
struct Paused {
    response: reqwest::Response,
    id: String,
    first_bytes: usize,
}
async fn pause(client: reqwest::Client, endpoint: String, key: String, label: String) -> Paused {
    let mut response = client
        .post(format!("{endpoint}/v1/chat/completions"))
        .bearer_auth(key)
        .header("idempotency-key", label)
        .json(&body("stream-pause", true))
        .send()
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let id = valid_id(&response).unwrap();
    let first = tokio::time::timeout(Duration::from_secs(2), response.chunk())
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert!(!first.is_empty());
    assert!(!String::from_utf8_lossy(&first).contains("[DONE]"));
    Paused {
        response,
        id,
        first_bytes: first.len(),
    }
}

async fn streams_closed(provider: &Provider, expected: usize) {
    tokio::time::timeout(Duration::from_secs(2), async {
        while provider.stream_closed.load(Ordering::SeqCst) != expected {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .expect("Core did not close actual upstream response bodies after disconnect");
}

async fn producers_finished(provider: &Provider, expected: usize) {
    tokio::time::timeout(Duration::from_secs(2), async {
        while provider.stream_finished.load(Ordering::SeqCst) != expected {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .expect("synthetic upstream producers did not all exit");
    assert_eq!(provider.stream_closed.load(Ordering::SeqCst), expected);
}

// @mark model.recovery role=test
async fn unknown_holds(pool: &PgPool, ids: &[String], reconciled: bool) {
    assert_eq!(ids.iter().collect::<HashSet<_>>().len(), ids.len());
    let rows:Vec<Value>=sqlx::query_scalar("SELECT jsonb_build_object('id',r.id,'state',r.state,'reserved',r.reserved,'settled',r.settled,'hold_reserved',h.reserved,'captured',h.captured,'closed',h.closed,'checkpoints',(SELECT count(*) FROM core_model.output_checkpoints c WHERE c.request_id=r.id),'terminal',(SELECT count(*) FROM core_model.output_checkpoints c WHERE c.request_id=r.id AND c.terminal_observed)) FROM core_charging.requests r JOIN core_charging.ledger_holds h ON h.request_id=r.id WHERE r.id=ANY($1)")
        .bind(ids).fetch_all(pool).await.unwrap();
    assert_eq!(rows.len(), ids.len());
    for row in rows {
        assert_eq!(row["reserved"], 140);
        assert_eq!(row["settled"], 0);
        assert_eq!(row["hold_reserved"], 140);
        assert_eq!(row["captured"], 0);
        assert_eq!(row["closed"], false);
        assert_eq!(row["terminal"], 0);
        assert!(row["checkpoints"].as_i64().unwrap() >= 1);
        if reconciled {
            assert_eq!(row["state"], "reconcile");
        }
    }
}

// Startup guarantees one bounded batch, not synchronous completion of the whole
// backlog. This separate classification stage leaves the frozen 540s load and
// resource-recovery phases unchanged and never changes UNKNOWN money outcomes.
async fn classify_backlog(
    pool: &PgPool,
    seed: &Seed,
    successful_ids: &[String],
    unknown: &[String],
    reserved: i64,
    spent: i64,
    directory: &Path,
) -> Value {
    let initial_pending: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_charging.requests WHERE state='streaming' AND id=ANY($1)",
    )
    .bind(unknown)
    .fetch_one(pool)
    .await
    .unwrap();
    let initial_pending = u64::try_from(initial_pending).unwrap();
    let deadline_seconds = initial_pending.div_ceil(64) * 5 + 10;
    let started = tokio::time::Instant::now();
    let deadline = started + Duration::from_secs(deadline_seconds);
    private(
        &directory.join("backlog-classification-plan.json"),
        &serde_json::to_vec(&json!({
            "stage":"backlog classification","initial_streaming":initial_pending,
            "batch_limit":64,"poll_seconds":5,"finite_deadline_seconds":deadline_seconds,
            "progress_deadline_seconds":15,"observation_period_ms":500,
            "original_load_and_recovery_plan_seconds":PLANNED_SECONDS,
            "changes_success_throughput_gate":false,"changes_unknown_hold_semantics":false
        }))
        .unwrap(),
    );
    let mut log = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(directory.join("backlog-classification-observations.jsonl"))
        .unwrap();
    let mut interval = tokio::time::interval(Duration::from_millis(500));
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    let mut previous_pending = initial_pending;
    let mut last_progress = started;
    let mut previous_observation = started;
    let mut maximum_observation_gap_ms = 0_u64;
    let mut observations = 0_usize;
    loop {
        interval.tick().await;
        assert!(
            tokio::time::Instant::now() < deadline,
            "finite backlog classification deadline expired"
        );
        // Reads only. Every observation checks each original settled request,
        // each UNKNOWN hold, wallet conservation and the exact capture count.
        // The one-second bound prevents a stalled audit from hiding a stall.
        let audit = async {
            let ((), (), (), counts, capture_count) = tokio::join!(
                balance(&seed.ledger, seed.account, OPENING - spent - reserved, reserved),
                verify_paid(pool, seed.account, successful_ids),
                unknown_holds(pool, unknown, false),
                sqlx::query_as::<_, (i64,i64,i64,i64)>(
                    "SELECT count(*) FILTER(WHERE state='streaming'),count(*) FILTER(WHERE state='reconcile'),count(*) FILTER(WHERE state='settled'),count(*) FROM core_charging.requests"
                ).fetch_one(pool),
                sqlx::query_scalar::<_,i64>(
                    "SELECT count(*) FROM core_billing.ledger_journals WHERE kind='capture'"
                ).fetch_one(pool),
            );
            (counts.unwrap(), capture_count.unwrap())
        };
        let ((streaming, reconciled, settled, total), capture_count) =
            tokio::time::timeout(Duration::from_secs(1), audit)
                .await
                .expect("backlog financial observation exceeded one second");
        let now = tokio::time::Instant::now();
        let observation_gap_ms = now.duration_since(previous_observation).as_millis() as u64;
        maximum_observation_gap_ms = maximum_observation_gap_ms.max(observation_gap_ms);
        observations += 1;
        writeln!(
            log,
            "{}",
            serde_json::to_string(&json!({
                "stage":"backlog classification","elapsed_ms":started.elapsed().as_millis(),
                "streaming":streaming,"reconcile":reconciled,"settled":settled,"total":total,
                "captures":capture_count,"reserved_units":reserved,"settled_units":spent,
                "observation_gap_ms":observation_gap_ms
            }))
            .unwrap()
        )
        .unwrap();
        log.flush().unwrap();
        assert!(
            observation_gap_ms <= 1000,
            "backlog observations were more than one second apart"
        );
        assert_eq!(settled, successful_ids.len() as i64);
        assert_eq!(capture_count, successful_ids.len() as i64);
        assert_eq!(total, (successful_ids.len() + unknown.len()) as i64);
        assert_eq!(
            streaming + reconciled,
            unknown.len() as i64,
            "UNKNOWN requests left streaming/reconcile states"
        );
        let pending = u64::try_from(streaming).unwrap();
        assert!(
            pending <= previous_pending,
            "classification backlog grew without any offered requests"
        );
        if pending < previous_pending {
            last_progress = now;
        }
        if pending > 0 {
            assert!(
                now.duration_since(last_progress) < Duration::from_secs(15),
                "classification made no progress for fifteen seconds"
            );
        }
        assert!(
            now <= deadline,
            "classification exceeded its derived finite deadline"
        );
        if pending == 0 {
            break;
        }
        previous_pending = pending;
        previous_observation = now;
    }
    unknown_holds(pool, unknown, true).await;
    assert!(tokio::time::Instant::now() <= deadline);
    let result = json!({
        "stage":"backlog classification","result":"PASS","initial_streaming":initial_pending,
        "final_streaming":0,"final_reconcile":unknown.len(),"finite_deadline_seconds":deadline_seconds,
        "elapsed_ms":started.elapsed().as_millis(),"observations":observations,
        "maximum_observation_gap_ms":maximum_observation_gap_ms,
        "reserved_units":reserved,"settled_units":spent,"capture_count":successful_ids.len(),
        "separate_from_original_120s_traffic_recovery":true
    });
    private(
        &directory.join("backlog-classification-result.json"),
        &serde_json::to_vec(&result).unwrap(),
    );
    result
}

// @mark model.resource-acceptance role=test
#[sqlx::test(migrations = false)]
#[ignore = "Requires the frozen resource runner, delegated cgroup v2 and a fresh owned PG fixture"]
async fn independent_process_constrained_resources(pool: PgPool) {
    assert_eq!(
        std::env::var("LMM_RESOURCE_ACCEPTANCE").as_deref(),
        Ok("1"),
        "explicit resource runner required"
    );
    let seed = seed(&pool, OPENING).await;
    let provider = Provider::start(true).await;
    let _failed_test_gate_cleanup = ReleaseProducerGate {
        gate: provider.stream_gate.clone(),
        dispatches: provider.hits.clone(),
    };
    let prepared = prepare_core(&pool, &provider, &[seed.key_id], "resource").await;
    let client = reqwest::Client::builder()
        .no_proxy()
        .timeout(Duration::from_secs(5))
        .build()
        .unwrap();
    let mut host = ResourceHost::start(prepared, &client).await;
    let measurement_started = Instant::now();
    let phase = Arc::new(AtomicU8::new(0));
    let (stop_sample, mut stopped) = oneshot::channel();
    let pid = host.pid;
    let cgroup = host.cgroup.clone();
    let directory = host.prepared.directory.clone();
    let phase_flag = phase.clone();
    let ticks = host.start_ticks.clone();
    let sampling_pool = pool.clone();
    let runtime_role = host.prepared.role.as_ref().unwrap().name.clone();
    let sampler = tokio::spawn(async move {
        let mut log = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(directory.join("resource-samples.jsonl"))
            .unwrap();
        let start = Instant::now();
        let mut all = Vec::new();
        loop {
            assert_eq!(process_start_ticks(pid).unwrap(), ticks);
            let mut value = sample(pid, &cgroup, start, phase_flag.load(Ordering::SeqCst));
            let connections: i64 = tokio::time::timeout(
                Duration::from_secs(1),
                sqlx::query_scalar("SELECT count(*) FROM pg_stat_activity WHERE usename=$1")
                    .bind(&runtime_role)
                    .fetch_one(&sampling_pool),
            )
            .await
            .expect("database observation exceeded1s")
            .unwrap();
            value["runtime_database_connections"] = json!(connections);
            writeln!(log, "{}", serde_json::to_string(&value).unwrap()).unwrap();
            log.flush().unwrap();
            all.push(value);
            tokio::select! { _=&mut stopped=>break, _=tokio::time::sleep(Duration::from_secs(1))=>{} }
        }
        all
    });
    let mut normal = Vec::new();
    let mut unknown = Vec::new();
    let mut paused_labels = Vec::new();
    phase.store(1, Ordering::SeqCst);
    normal.extend(load_phase(&mut host, &pool, &seed, &client, "warmup", 60).await);
    let baseline = sample(host.pid, &host.cgroup, Instant::now(), 1);
    phase.store(2, Ordering::SeqCst);
    normal.extend(load_phase(&mut host, &pool, &seed, &client, "steady", 300).await);
    let normal_streams = normal.iter().filter(|x| x.success && x.stream).count();
    producers_finished(&provider, normal_streams).await;
    phase.store(3, Ordering::SeqCst);
    let overload_started = Instant::now();
    let mut rounds = 0;
    let mut rejected = 0;
    while overload_started.elapsed() < Duration::from_secs(60) {
        let fill_started = Instant::now();
        let mut tasks = JoinSet::new();
        for index in 0..CAPACITY {
            let label = format!("resource-overload-{rounds}-{index}");
            paused_labels.push(label.clone());
            tasks.spawn(pause(
                client.clone(),
                host.prepared.endpoint.clone(),
                seed.key.clone(),
                label,
            ));
        }
        let mut retained = Vec::new();
        while let Some(result) = tasks.join_next().await {
            retained.push(result.unwrap());
        }
        assert_eq!(retained.len(), CAPACITY);
        assert!(
            fill_started.elapsed() < Duration::from_secs(2),
            "could not establish saturation before natural model timeout"
        );
        let hits = provider.hits.load(Ordering::SeqCst);
        let requests_before: i64 =
            sqlx::query_scalar("SELECT count(*) FROM core_charging.requests")
                .fetch_one(&pool)
                .await
                .unwrap();
        let mut probes = JoinSet::new();
        for index in 0..32 {
            probes.spawn(complete(
                client.clone(),
                host.prepared.endpoint.clone(),
                seed.key.clone(),
                format!("overload-probe-{rounds}"),
                index,
                false,
                tokio::time::Instant::now(),
            ));
        }
        let mut refusals = Vec::new();
        while let Some(result) = probes.join_next().await {
            let result = result.unwrap();
            assert_eq!(result.status, 503);
            assert_eq!(result.code, "core_not_accepting");
            assert!(result.id.is_none());
            assert!(!result.success);
            assert!(result.latency_ms <= 1000);
            refusals.push(result.value());
            rejected += 1;
        }
        assert!(
            fill_started.elapsed() < Duration::from_secs(3),
            "saturation window crossed natural timeout"
        );
        assert_eq!(provider.hits.load(Ordering::SeqCst), hits);
        let requests_after: i64 = sqlx::query_scalar("SELECT count(*) FROM core_charging.requests")
            .fetch_one(&pool)
            .await
            .unwrap();
        assert_eq!(
            requests_before, requests_after,
            "admission refusal created financial requests"
        );
        let first_bytes = retained.iter().map(|x| x.first_bytes).sum::<usize>();
        for paused in retained {
            unknown.push(paused.id);
            drop(paused.response);
        }
        streams_closed(&provider, normal_streams + unknown.len()).await;
        provider.stream_gate.add_permits(CAPACITY);
        producers_finished(&provider, normal_streams + unknown.len()).await;
        private(&host.prepared.directory.join(format!("overload-round-{rounds}.json")),&serde_json::to_vec(&json!({
            "round":rounds,"occupied":CAPACITY,"first_checkpoint_bytes":first_bytes,"rejections":refusals,
            "provider_hits_before":hits,"provider_hits_after":provider.hits.load(Ordering::SeqCst),
            "financial_rows_before":requests_before,"financial_rows_after":requests_after})).unwrap());
        host.alive();
        rounds += 1;
    }
    assert!(rounds > 0 && rejected >= 32);
    phase.store(4, Ordering::SeqCst);
    for index in 0..4 {
        let started = Instant::now();
        let label = format!("resource-cancel-{index}");
        paused_labels.push(label.clone());
        let paused = pause(
            client.clone(),
            host.prepared.endpoint.clone(),
            seed.key.clone(),
            label,
        )
        .await;
        unknown.push(paused.id);
        drop(paused.response);
        streams_closed(&provider, normal_streams + unknown.len()).await;
        provider.stream_gate.add_permits(1);
        producers_finished(&provider, normal_streams + unknown.len()).await;
        assert!(
            started.elapsed() < Duration::from_secs(2),
            "disconnect not observed before3s natural timeout"
        );
        host.alive();
    }
    unknown_holds(&pool, &unknown, false).await;
    phase.store(5, Ordering::SeqCst);
    normal.extend(load_phase(&mut host, &pool, &seed, &client, "recovery", 60).await);
    phase.store(6, Ordering::SeqCst);
    tokio::time::sleep(Duration::from_secs(60)).await;
    let final_sample = sample(host.pid, &host.cgroup, Instant::now(), 6);
    host.alive();
    stop_sample.send(()).unwrap();
    let samples = sampler.await.unwrap();
    let measured_ms = measurement_started.elapsed().as_millis() as u64;
    assert!(
        measured_ms >= PLANNED_SECONDS * 1000,
        "phase durations were shortened"
    );
    let times = samples
        .iter()
        .map(|x| x["elapsed_ms"].as_u64().unwrap())
        .collect::<Vec<_>>();
    let max_sample_gap_ms = times.windows(2).map(|x| x[1] - x[0]).max().unwrap();
    assert!(
        max_sample_gap_ms <= 2500,
        "resource sampler missed more than2.5s"
    );
    assert!(
        times[0] <= 1000 && times.last().unwrap() + 2500 >= measured_ms,
        "resource samples do not cover full measurement interval"
    );
    assert!(
        samples.len() >= PLANNED_SECONDS as usize / 2,
        "insufficient observations for declared1s sampling"
    );
    let memory_peak = samples
        .iter()
        .map(|x| x["memory_peak"].as_u64().unwrap())
        .max()
        .unwrap();
    assert!(memory_peak <= MEMORY_MAX);
    for value in &samples {
        assert!(value["pids_current"].as_u64().unwrap() <= 128);
        assert!(
            value["runtime_database_connections"].as_i64().unwrap() <= 2,
            "runtime pool exceeded configured2 connections"
        );
        for event in ["oom", "oom_kill", "max"] {
            assert_eq!(
                value["memory_events"][event], samples[0]["memory_events"][event],
                "memory pressure event {event}"
            );
        }
    }
    let baseline_rss = baseline["smaps_rollup_kib"]["Rss"].as_u64().unwrap();
    let final_rss = final_sample["smaps_rollup_kib"]["Rss"].as_u64().unwrap();
    assert!(
        final_rss <= baseline_rss + 32 * 1024,
        "retained Core RSS grew more than32MiB after drain"
    );
    assert!(
        final_sample["fd_count"].as_u64().unwrap() <= baseline["fd_count"].as_u64().unwrap() + 32
    );
    let successful_ids = normal
        .iter()
        .filter(|x| x.success)
        .map(|x| x.id.clone().unwrap())
        .collect::<Vec<_>>();
    verify_paid(&pool, seed.account, &successful_ids).await;
    unknown_holds(&pool, &unknown, false).await;
    assert_eq!(
        provider.hits.load(Ordering::SeqCst),
        successful_ids.len() + unknown.len()
    );
    assert_eq!(
        provider.auth.lock().unwrap().len(),
        successful_ids.len() + unknown.len()
    );
    assert!(
        provider
            .auth
            .lock()
            .unwrap()
            .iter()
            .all(|x| x == "Bearer synthetic-upstream-key")
    );
    producers_finished(
        &provider,
        normal.iter().filter(|x| x.success && x.stream).count() + unknown.len(),
    )
    .await;
    let reserved = 140 * unknown.len() as i64;
    let spent = 49 * successful_ids.len() as i64;
    balance(
        &seed.ledger,
        seed.account,
        OPENING - spent - reserved,
        reserved,
    )
    .await;
    let total: i64 = sqlx::query_scalar("SELECT count(*) FROM core_charging.requests")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(total, successful_ids.len() as i64 + unknown.len() as i64);
    let captures: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_billing.ledger_journals WHERE kind='capture'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(captures, successful_ids.len() as i64);
    balanced(&pool).await;
    host.stop().await;
    let role = host.prepared.role.as_ref().unwrap().name.clone();
    let connections: i64 =
        sqlx::query_scalar("SELECT count(*) FROM pg_stat_activity WHERE usename=$1")
            .bind(&role)
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(
        connections, 0,
        "Core database connections remained after shutdown"
    );
    assert!(
        client
            .get(format!("{}/health/live", host.prepared.endpoint))
            .send()
            .await
            .is_err(),
        "Core port remained open"
    );
    // Preserve unknown holds through natural lease expiry and an actual Core restart.
    tokio::time::sleep(Duration::from_secs(21)).await;
    host.launch(&client).await;
    let backlog_classification = classify_backlog(
        &pool,
        &seed,
        &successful_ids,
        &unknown,
        reserved,
        spent,
        &host.prepared.directory,
    )
    .await;
    host.alive();
    verify_paid(&pool, seed.account, &successful_ids).await;
    let before_replay = provider.hits.load(Ordering::SeqCst);
    for (index, label) in paused_labels.iter().enumerate() {
        let response = client
            .post(format!("{}/v1/chat/completions", host.prepared.endpoint))
            .bearer_auth(&seed.key)
            .header("idempotency-key", label)
            .json(&body("stream-pause", true))
            .send()
            .await
            .unwrap();
        assert_eq!(
            response.status(),
            StatusCode::CONFLICT,
            "UNKNOWN replay {index}"
        );
        let _ = response.bytes().await.unwrap();
    }
    assert_eq!(provider.hits.load(Ordering::SeqCst), before_replay);
    balance(
        &seed.ledger,
        seed.account,
        OPENING - spent - reserved,
        reserved,
    )
    .await;
    unknown_holds(&pool, &unknown, true).await;
    balanced(&pool).await;
    let result_directory = host.prepared.directory.clone();
    let result = json!({
        "result":"PASS","core_cpu":1,"core_memory_max":MEMORY_MAX,"database_outside_budget":true,
        "warmup_seconds":60,"steady_seconds":300,"steady_offered_rps":5,"overload_seconds":60,
        "recovery_load_seconds":60,"recovery_idle_seconds":60,"complete_successes":successful_ids.len(),
        "overload_rounds":rounds,"capacity_refusals":rejected,"unknown_holds":unknown.len(),
        "reserved_units":reserved,"settled_units":spent,"memory_peak":memory_peak,
        "baseline_rss_kib":baseline_rss,"drained_rss_kib":final_rss,"samples":samples.len(),
        "planned_seconds":PLANNED_SECONDS,"measured_ms":measured_ms,"maximum_sample_gap_ms":max_sample_gap_ms,
        "unknown_state_after_natural_expiry_restart":"reconcile","producer_completion_observed":true,
        "backlog_classification":backlog_classification,
        "not_verified":["socket backpressure from slow consumers","Core plus Go","whole-node1C1G","real providers","old architecture comparison"]});
    host.finish().await;
    private(
        &result_directory.join("resource-result.json"),
        &serde_json::to_vec(&result).unwrap(),
    );
}
