//! Explicit mixed acceptance: one real Core, one real Go, one shared CPU budget.
//! Synthetic upstream only. Financial/domain calls always use product endpoints.
use super::*;
use std::{collections::HashSet, sync::atomic::Ordering};
use tokio::task::JoinSet;
#[path = "../../services/core/tests/common/model_resource_client.rs"]
mod model_client;
#[path = "../../services/core/tests/common/model_fixture.rs"]
mod model_fixture;
use model_client::{Outcome as ModelOutcome, body, complete, percentile, valid_id, verify_paid};
use model_fixture::{Provider, fixture};
#[path = "store_load.rs"]
mod store_load;
use store_load::{store_phase, verify_store};
const OPENING: i64 = 10_000_000;
const CAPACITY: usize = 16;

pub(super) struct Mixed {
    pub(super) key_id: i64,
    key: String,
    account: i64,
    ledger: Ledger,
    provider: Provider,
    normal: Vec<ModelOutcome>,
    unknown: Vec<String>,
    labels: Vec<(String, String)>,
    orders: Vec<String>,
}
impl Drop for Mixed {
    fn drop(&mut self) {
        self.provider
            .stream_gate
            .add_permits(self.provider.hits.load(Ordering::SeqCst) + 1);
    }
}

#[derive(Clone)]
pub(super) struct Store {
    pub(super) client: Client,
    pub(super) endpoint: String,
    pub(super) host: String,
    pub(super) buyer: String,
    pub(super) buyer_id: i64,
    pub(super) buyer_account: i64,
    pub(super) seller: String,
    pub(super) seller_account: i64,
    pub(super) outsider: String,
    pub(super) shop: String,
    pub(super) variant: String,
    pub(super) pool: PgPool,
    pub(super) owner: PgPool,
    pub(super) core_pid: u32,
    pub(super) go_pid: u32,
    pub(super) core_role: String,
    pub(super) store_role: String,
}

pub(super) async fn prepare(pool: &PgPool) -> Option<Mixed> {
    match std::env::var("LMM_COMMERCE_MIXED") {
        Err(std::env::VarError::NotPresent) => return None,
        Ok(value) if value == "1" => {}
        _ => panic!("mixed acceptance must be explicitly 1 or unset"),
    }
    assert!(
        std::env::var_os("LMM_COMMERCE_RESOURCE_READY").is_some(),
        "mixed gate requires shared limits"
    );
    let (_identity, ledger, _session, key, key_id, account) = fixture(pool, OPENING).await;
    Some(Mixed {
        key_id,
        key,
        account,
        ledger,
        provider: Provider::start(true).await,
        normal: Vec::new(),
        unknown: Vec::new(),
        labels: Vec::new(),
        orders: Vec::new(),
    })
}

impl Mixed {
    pub(super) fn config(&self, root: &Path) -> PathBuf {
        let secret = private(root, "mixed-upstream.token", b"synthetic-upstream-key");
        let model = format!(
            r#"version=1
input_token_ceiling=100
lease_seconds=20
timeout_seconds=3
max_output_tokens=20
local_streaming=true
credentials=[{{reference={{id=7,revision=1}},secret_file='{}'}}]
key_groups=[{{key_id={},group='allowed'}}]
[routing]
version=1
groups=[{{name='allowed',enabled=true}}]
upstreams=[{{id=1,endpoint='{}',protocol='openai_chat',credential={{id=7,revision=1}},enabled=true}}]
routes=[{{id=1,model='local-model',group='allowed',enabled=true,speeds=['standard'],targets=[{{id=1,upstream_id=1,upstream_model='provider-model',enabled=true,priority=0,weight=1,speeds=['standard'],payload='adapt',timeout_ms=5000}}],failover={{max_attempts=1,retry_on=[],allow_replay_after_send=false}}}}]
aliases=[]
[prices]
version=1
rules=[{{id=1,model='local-model',group='allowed',speed='standard',rates={{input=2000000,output=4000000,cache_read=500000,cache_write=0,request=0}},group_multiplier_ppm=1000000,speed_multiplier_ppm=1000000}}]
"#,
            secret.display(),
            self.key_id,
            self.provider.endpoint
        );
        private(root, "mixed-model.toml", model.as_bytes())
    }

    async fn model_phase(
        &self,
        pool: &PgPool,
        endpoint: &str,
        root: &Path,
        label: &str,
        seconds: usize,
    ) -> Vec<ModelOutcome> {
        let start = tokio::time::Instant::now();
        let mut tasks = JoinSet::new();
        let client = Client::builder()
            .no_proxy()
            .timeout(Duration::from_secs(5))
            .build()
            .unwrap();
        for ordinal in 0..seconds * 5 {
            let due = start + Duration::from_millis(200) * ordinal as u32;
            tokio::time::sleep_until(due).await;
            tasks.spawn(complete(
                client.clone(),
                endpoint.into(),
                self.key.clone(),
                format!("mixed-{label}"),
                ordinal,
                ordinal % 5 == 4,
                due,
            ));
        }
        let mut rows = Vec::new();
        while let Some(row) = tasks.join_next().await {
            rows.push(row.unwrap());
        }
        tokio::time::sleep_until(start + Duration::from_secs(seconds as u64)).await;
        rows.sort_by_key(|row| row.ordinal);
        let successful = rows.iter().filter(|row| row.success).count();
        let times = rows.iter().map(|row| row.latency_ms).collect::<Vec<_>>();
        private(root,&format!("mixed-model-{label}.json"),&serde_json::to_vec(&json!({"offered":seconds*5,"seconds":seconds,"elapsed_ms":start.elapsed().as_millis(),"complete_successes":successful,"p95_ms":percentile(&times,95),"p99_ms":percentile(&times,99),"outcomes":rows.iter().map(ModelOutcome::value).collect::<Vec<_>>()})).unwrap());
        assert_eq!(rows.len(), seconds * 5);
        assert!(
            successful * 100 >= rows.len() * 99,
            "model complete throughput below original 99% gate"
        );
        assert!(percentile(&times, 95) <= 1000 && percentile(&times, 99) <= 2000);
        for row in &rows {
            assert!(
                row.submission_lag_ms < 200,
                "model generator missed fixed 200ms cadence"
            );
            assert!(
                row.success
                    || (row.status == 503 && row.code == "core_not_accepting" && row.id.is_none())
            );
        }
        let ids = rows
            .iter()
            .filter(|row| row.success)
            .map(|row| row.id.clone().unwrap())
            .collect::<Vec<_>>();
        verify_paid(pool, self.account, &ids).await;
        rows
    }

    async fn phase(
        &mut self,
        store: &Store,
        endpoint: &str,
        root: &Path,
        label: &str,
        seconds: usize,
    ) {
        eprintln!("joint phase {label}: {seconds}s, model 5RPS, complete commerce 1RPS");
        let (model, orders) = tokio::join!(
            self.model_phase(&store.pool, endpoint, root, label, seconds),
            store_phase(store, root, label, seconds)
        );
        self.normal.extend(model);
        self.orders.extend(orders);
    }

    async fn paused(&self, endpoint: &str, label: String) -> (reqwest::Response, String, usize) {
        pause_request(endpoint.into(), self.key.clone(), label).await
    }

    async fn close_streams(&self, expected: usize, permits: usize) {
        tokio::time::timeout(Duration::from_secs(2), async {
            while self.provider.stream_closed.load(Ordering::SeqCst) != expected {
                tokio::time::sleep(Duration::from_millis(5)).await;
            }
        })
        .await
        .expect("Core retained upstream bodies after consumer disconnect");
        self.provider.stream_gate.add_permits(permits);
        tokio::time::timeout(Duration::from_secs(2), async {
            while self.provider.stream_finished.load(Ordering::SeqCst) != expected {
                tokio::time::sleep(Duration::from_millis(5)).await;
            }
        })
        .await
        .expect("synthetic producers did not finish");
    }

    async fn overload(&self, pool: &PgPool, endpoint: &str, root: &Path) -> Vec<(String, String)> {
        let start = Instant::now();
        let normal_streams = self
            .normal
            .iter()
            .filter(|row| row.success && row.stream)
            .count();
        let mut unknown = Vec::new();
        let mut rounds = 0;
        let mut rejected = 0;
        while start.elapsed() < Duration::from_secs(60) {
            let fill = Instant::now();
            let mut tasks = JoinSet::new();
            for index in 0..CAPACITY {
                // Each paused body is retained until all refusal probes finish.
                let endpoint = endpoint.to_owned();
                let key = self.key.clone();
                tasks.spawn(async move {
                    let label = format!("mixed-overload-{rounds}-{index}");
                    let result = pause_request(endpoint, key, label.clone()).await;
                    (label, result)
                });
            }
            let mut retained = Vec::new();
            while let Some(row) = tasks.join_next().await {
                retained.push(row.unwrap());
            }
            assert_eq!(retained.len(), CAPACITY);
            assert!(fill.elapsed() < Duration::from_secs(2));
            let hits = self.provider.hits.load(Ordering::SeqCst);
            let before: i64 = sqlx::query_scalar("SELECT count(*) FROM core_charging.requests")
                .fetch_one(pool)
                .await
                .unwrap();
            let mut probes = JoinSet::new();
            let client = Client::builder()
                .no_proxy()
                .timeout(Duration::from_secs(5))
                .build()
                .unwrap();
            for index in 0..32 {
                probes.spawn(complete(
                    client.clone(),
                    endpoint.into(),
                    self.key.clone(),
                    format!("mixed-overload-probe-{rounds}"),
                    index,
                    false,
                    tokio::time::Instant::now(),
                ));
            }
            let mut refusals = Vec::new();
            while let Some(row) = probes.join_next().await {
                let row = row.unwrap();
                assert_eq!(row.status, 503);
                assert_eq!(row.code, "core_not_accepting");
                assert!(row.id.is_none() && !row.success && row.latency_ms <= 1000);
                refusals.push(row.value());
                rejected += 1;
            }
            assert!(fill.elapsed() < Duration::from_secs(3));
            assert_eq!(self.provider.hits.load(Ordering::SeqCst), hits);
            let after: i64 = sqlx::query_scalar("SELECT count(*) FROM core_charging.requests")
                .fetch_one(pool)
                .await
                .unwrap();
            assert_eq!(before, after, "refusal made financial rows");
            let first_bytes = retained.iter().map(|row| row.1.2).sum::<usize>();
            for (label, (response, id, _)) in retained {
                unknown.push((id, label));
                drop(response);
            }
            self.close_streams(normal_streams + unknown.len(), CAPACITY)
                .await;
            private(root,&format!("mixed-overload-{rounds}.json"),&serde_json::to_vec(&json!({"round":rounds,"refusals":refusals,"occupied":CAPACITY,"first_checkpoint_bytes":first_bytes,"financial_rows_before":before,"financial_rows_after":after,"provider_hits_before":hits,"provider_hits_after":self.provider.hits.load(Ordering::SeqCst)})).unwrap());
            rounds += 1;
        }
        assert!(rounds > 0 && rejected >= 32);
        unknown
    }

    // @mark runtime.joint-resource role=load
    pub(super) async fn run(&mut self, store: &Store, endpoint: &str, root: &Path) {
        let started = Instant::now();
        let (stop_observer, mut stopping) = tokio::sync::oneshot::channel();
        let pool = store.pool.clone();
        let core_role = store.core_role.clone();
        let store_role = store.store_role.clone();
        let log_path = root.join("mixed-runtime-connections.jsonl");
        let mut observer = tokio::spawn(async move {
            use std::io::Write;
            let mut log = fs::OpenOptions::new()
                .write(true)
                .create_new(true)
                .mode(0o600)
                .open(log_path)
                .unwrap();
            let started = Instant::now();
            let mut observations = Vec::new();
            loop {
                let (core,extensions)=tokio::time::timeout(Duration::from_secs(1),sqlx::query_as::<_,(i64,i64)>("SELECT count(*) FILTER(WHERE usename=$1),count(*) FILTER(WHERE usename=$2) FROM pg_stat_activity").bind(&core_role).bind(&store_role).fetch_one(&pool)).await.expect("runtime connection observation exceeded one second").unwrap();
                let row = json!({"elapsed_ms":started.elapsed().as_millis(),"core_connections":core,"extensions_connections":extensions});
                writeln!(log, "{row}").unwrap();
                log.flush().unwrap();
                observations.push(row);
                assert!(
                    // Public/model, read-only RPC identity, events and commerce each
                    // own a bounded two-connection pool. Keep their isolation.
                    core <= 4 * 2 && extensions <= 2,
                    "runtime exceeded the sum of its configured pool limits"
                );
                tokio::select! {_=&mut stopping=>break,_=tokio::time::sleep(Duration::from_secs(1))=>{}}
            }
            observations
        });
        struct AbortObserver(tokio::task::AbortHandle);
        impl Drop for AbortObserver {
            fn drop(&mut self) {
                self.0.abort();
            }
        }
        let _observer_guard = AbortObserver(observer.abort_handle());
        let workload = async {
            self.phase(store, endpoint, root, "warmup", 60).await;
            let baseline = [
                runtime_snapshot(store.core_pid),
                runtime_snapshot(store.go_pid),
            ];
            self.phase(store, endpoint, root, "steady", 300).await;
            eprintln!("joint phase overload: 60s, full model admission, complete commerce 1RPS");
            let (unknown, orders) = tokio::join!(
                self.overload(&store.pool, endpoint, root),
                store_phase(store, root, "overload", 60)
            );
            for (id, label) in unknown {
                self.unknown.push(id.clone());
                self.labels.push((id, label));
            }
            self.orders.extend(orders);
            let normal_streams = self
                .normal
                .iter()
                .filter(|row| row.success && row.stream)
                .count();
            for index in 0..4 {
                let now = Instant::now();
                let label = format!("mixed-cancel-{index}");
                let (response, id, _) = self.paused(endpoint, label.clone()).await;
                self.labels.push((id.clone(), label));
                self.unknown.push(id);
                drop(response);
                self.close_streams(normal_streams + self.unknown.len(), 1)
                    .await;
                assert!(
                    now.elapsed() < Duration::from_secs(2),
                    "cancel waited for natural timeout"
                );
            }
            self.phase(store, endpoint, root, "recovery", 60).await;
            eprintln!("joint phase idle: 60s");
            tokio::time::sleep(Duration::from_secs(60)).await;
            assert!(
                started.elapsed() >= Duration::from_secs(540),
                "mixed phase durations shortened"
            );
            let post_drain = [
                runtime_snapshot(store.core_pid),
                runtime_snapshot(store.go_pid),
            ];
            for (before, after) in baseline.iter().zip(&post_drain) {
                assert!(
                    after["rss_kib"].as_u64().unwrap()
                        <= before["rss_kib"].as_u64().unwrap() + 32 * 1024,
                    "retained runtime RSS grew more than32MiB after drain"
                );
                assert!(
                    after["pss_kib"].as_u64().unwrap()
                        <= before["pss_kib"].as_u64().unwrap() + 32 * 1024
                );
                assert!(
                    after["fds"].as_u64().unwrap() <= before["fds"].as_u64().unwrap() + 32,
                    "runtime FD count did not return to bounded baseline"
                );
            }
            (baseline, post_drain)
        };
        // Observation failure cancels traffic immediately; it must not remain
        // hidden in a detached task until the full nine-minute run completes.
        let (baseline, post_drain) = tokio::select! {
            snapshots = workload => snapshots,
            result = &mut observer => panic!("runtime observer ended before drain: {result:?}"),
        };
        stop_observer.send(()).unwrap();
        let observations = observer.await.unwrap();
        let times = observations
            .iter()
            .map(|row| row["elapsed_ms"].as_u64().unwrap())
            .collect::<Vec<_>>();
        assert!(
            times[0] <= 1000
                && times.last().unwrap() + 2500 >= started.elapsed().as_millis() as u64
        );
        assert!(times.windows(2).all(|pair| pair[1] - pair[0] <= 2500));
        private(root,"mixed-runtime-drain.json",&serde_json::to_vec(&json!({"baseline":baseline,"post_drain":post_drain,"connection_samples":observations.len(),"measurement_ms":started.elapsed().as_millis()})).unwrap());
        self.verify(&store.pool).await;
        verify_store(store, &self.orders).await;
        let (reserved,claimed): (i64,i64) = sqlx::query_as(
            "SELECT (body->>'reserved')::bigint,(body->>'claimed')::bigint FROM lmm_store.variants WHERE id=$1",
        )
        .bind(&store.variant)
        .fetch_one(&store.owner)
        .await
        .unwrap();
        assert_eq!(reserved, 0, "mixed physical inventory reservation leaked");
        assert_eq!(
            claimed,
            self.orders.len() as i64,
            "financial refund must not restore physical quota"
        );
        private(root,"mixed-load-complete.json",&serde_json::to_vec(&json!({"scope":"540s real model+commerce traffic under shared product budget; synthetic provider","model_successes":self.normal.iter().filter(|row|row.success).count(),"unknown_holds":self.unknown.len(),"commerce_complete_refunded":self.orders.len(),"elapsed_ms":started.elapsed().as_millis(),"model_payer":self.account,"model_upstream_dispatches":self.provider.hits.load(Ordering::SeqCst)})).unwrap());
    }

    async fn verify(&self, pool: &PgPool) {
        let ids = self
            .normal
            .iter()
            .filter(|row| row.success)
            .map(|row| row.id.clone().unwrap())
            .collect::<Vec<_>>();
        verify_paid(pool, self.account, &ids).await;
        assert_eq!(
            self.unknown.iter().collect::<HashSet<_>>().len(),
            self.unknown.len()
        );
        let unknown:Vec<Value>=sqlx::query_scalar("SELECT jsonb_build_object('id',r.id,'payer',r.payer_account_id,'state',r.state,'reserved',r.reserved,'settled',r.settled,'captured',h.captured,'closed',h.closed,'hold',h.reserved,'checkpoints',(SELECT count(*) FROM core_model.output_checkpoints c WHERE c.request_id=r.id),'terminal',(SELECT count(*) FROM core_model.output_checkpoints c WHERE c.request_id=r.id AND c.terminal_observed)) FROM core_charging.requests r JOIN core_charging.ledger_holds h ON h.request_id=r.id WHERE r.id=ANY($1)").bind(&self.unknown).fetch_all(pool).await.unwrap();
        assert_eq!(unknown.len(), self.unknown.len());
        for row in unknown {
            assert_eq!(row["payer"], self.account);
            assert!(["streaming", "reconcile"].contains(&row["state"].as_str().unwrap()));
            assert_eq!(row["reserved"], 140);
            assert_eq!(row["hold"], 140);
            assert_eq!(row["settled"], 0);
            assert_eq!(row["captured"], 0);
            assert_eq!(row["closed"], false);
            assert_eq!(row["terminal"], 0);
            assert!(row["checkpoints"].as_i64().unwrap() >= 1);
        }
        let balance = self.ledger.balance(self.account).await.unwrap();
        let held = 140 * self.unknown.len() as i64;
        let spent = 49 * ids.len() as i64;
        assert_eq!(
            (balance.available_units, balance.reserved_units),
            (OPENING - held - spent, held)
        );
        let captures: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM core_billing.ledger_journals WHERE kind='capture'",
        )
        .fetch_one(pool)
        .await
        .unwrap();
        assert_eq!(captures, ids.len() as i64);
        let all: i64 = sqlx::query_scalar("SELECT count(*) FROM core_charging.requests")
            .fetch_one(pool)
            .await
            .unwrap();
        assert_eq!(all as usize, ids.len() + self.unknown.len());
        let streams = self
            .normal
            .iter()
            .filter(|row| row.success && row.stream)
            .count()
            + self.unknown.len();
        assert_eq!(self.provider.stream_closed.load(Ordering::SeqCst), streams);
        assert_eq!(
            self.provider.stream_finished.load(Ordering::SeqCst),
            streams
        );
        assert_eq!(
            self.provider.hits.load(Ordering::SeqCst),
            ids.len() + self.unknown.len()
        );
        assert!(
            self.provider
                .auth
                .lock()
                .unwrap()
                .iter()
                .all(|value| value == "Bearer synthetic-upstream-key")
        );
        balanced(pool).await;
    }

    pub(super) async fn after_restart(&self, pool: &PgPool, root: &Path, endpoint: &str) {
        let initial: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM core_charging.requests WHERE id=ANY($1) AND state='streaming'",
        )
        .bind(&self.unknown)
        .fetch_one(pool)
        .await
        .unwrap();
        let seconds = (initial as u64).div_ceil(64) * 5 + 10;
        let start = Instant::now();
        let mut prior = initial;
        let mut progress = start;
        let mut previous = start;
        let mut samples = Vec::new();
        loop {
            let pending=tokio::time::timeout(Duration::from_secs(1),async {
                self.verify(pool).await;
                sqlx::query_scalar::<_,i64>("SELECT count(*) FROM core_charging.requests WHERE id=ANY($1) AND state='streaming'").bind(&self.unknown).fetch_one(pool).await.unwrap()
            }).await.expect("classification financial audit exceeded one second");
            let now = Instant::now();
            let gap = now.duration_since(previous).as_millis();
            samples.push(json!({"elapsed_ms":start.elapsed().as_millis(),"pending":pending,"observation_gap_ms":gap}));
            assert!(
                gap <= 1000 && pending <= prior,
                "classification observation/progress invalid"
            );
            if pending < prior {
                progress = now;
            }
            if pending > 0 {
                assert!(
                    now.duration_since(progress) < Duration::from_secs(15),
                    "classification stalled for 15 seconds"
                );
            }
            assert!(
                start.elapsed() < Duration::from_secs(seconds),
                "bounded UNKNOWN classification failed"
            );
            if pending == 0 {
                break;
            }
            prior = pending;
            previous = now;
            tokio::time::sleep(Duration::from_millis(500)).await;
        }
        let states: Vec<String> =
            sqlx::query_scalar("SELECT state FROM core_charging.requests WHERE id=ANY($1)")
                .bind(&self.unknown)
                .fetch_all(pool)
                .await
                .unwrap();
        assert!(states.iter().all(|state| state == "reconcile"));
        let hits = self.provider.hits.load(Ordering::SeqCst);
        let client = Client::builder()
            .no_proxy()
            .timeout(Duration::from_secs(5))
            .build()
            .unwrap();
        assert_eq!(self.labels.len(), self.unknown.len());
        for (id, label) in &self.labels {
            assert!(self.unknown.contains(id));
            let response = client
                .post(format!("{endpoint}/v1/chat/completions"))
                .bearer_auth(&self.key)
                .header("idempotency-key", label)
                .json(&body("stream-pause", true))
                .send()
                .await
                .unwrap();
            assert_eq!(
                response.status(),
                StatusCode::CONFLICT,
                "UNKNOWN replay must remain bound to original request"
            );
            let _ = response.bytes().await.unwrap();
        }
        assert_eq!(
            self.provider.hits.load(Ordering::SeqCst),
            hits,
            "UNKNOWN replay dispatched again"
        );
        self.verify(pool).await;
        private(root,"mixed-restart-classification.json",&serde_json::to_vec(&json!({"initial_streaming":initial,"bounded_deadline_seconds":seconds,"progress_deadline_seconds":15,"samples":samples,"elapsed_ms":start.elapsed().as_millis(),"funds_unchanged":true,"unknown_replays":self.labels.len(),"provider_hits_before_replay":hits,"provider_hits_after_replay":self.provider.hits.load(Ordering::SeqCst)})).unwrap());
        private(root,"mixed-result.json",&serde_json::to_vec(&json!({"result":"PASS","scope":"shared-budget real model+commerce sustained regression and bounded restart classification","model_successes":self.normal.iter().filter(|row|row.success).count(),"unknown_holds":self.unknown.len(),"commerce_complete_refunded":self.orders.len(),"full_project_complete":false})).unwrap());
    }
}

fn runtime_snapshot(pid: u32) -> Value {
    let status = fs::read_to_string(format!("/proc/{pid}/status")).unwrap();
    let rss = status
        .lines()
        .find(|line| line.starts_with("VmRSS:"))
        .unwrap()
        .split_whitespace()
        .nth(1)
        .unwrap()
        .parse::<u64>()
        .unwrap();
    let smaps = fs::read_to_string(format!("/proc/{pid}/smaps_rollup")).unwrap();
    let pss = smaps
        .lines()
        .find(|line| line.starts_with("Pss:"))
        .unwrap()
        .split_whitespace()
        .nth(1)
        .unwrap()
        .parse::<u64>()
        .unwrap();
    json!({"pid":pid,"rss_kib":rss,"pss_kib":pss,"fds":fs::read_dir(format!("/proc/{pid}/fd")).unwrap().count(),"tasks":fs::read_dir(format!("/proc/{pid}/task")).unwrap().count()})
}

async fn pause_request(
    endpoint: String,
    key: String,
    label: String,
) -> (reqwest::Response, String, usize) {
    let client = Client::builder()
        .no_proxy()
        .timeout(Duration::from_secs(5))
        .build()
        .unwrap();
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
    let chunk = tokio::time::timeout(Duration::from_secs(2), response.chunk())
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert!(!chunk.is_empty());
    assert!(!String::from_utf8_lossy(&chunk).contains("[DONE]"));
    (response, id, chunk.len())
}
