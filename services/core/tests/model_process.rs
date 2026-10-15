//! Real lmm-core OS processes, TCP HTTP/SSE and an owned PostgreSQL fixture.
//! Run scripts/verify_model_process.py; no external database or supplier is used.
//! The owner seeds synthetic accounts; the Core child only gets a runtime DSN.
use axum::http::StatusCode;
use lmm_core::{
    accounts::{Account, AccountKind},
    billing::{
        Credits,
        ledger::{Action, Ledger, Request as LedgerRequest},
        transactional_ledger::TransactionalLedger,
    },
    funding::FundingPolicy,
    identity::IdentityStore,
};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use std::{
    sync::{Arc, atomic::Ordering},
    time::Duration,
};

#[path = "common/model_fixture.rs"]
mod model_fixture;
use model_fixture::{Provider, fixture};
use std::{
    fs,
    os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt},
    path::{Path, PathBuf},
    process::{Child, Command, Stdio},
    time::Instant,
};
#[path = "common/runtime.rs"]
mod restricted_runtime;
#[path = "../../../tests/model_process/stream_cases.rs"]
mod stream_cases;
struct Seed {
    identity: IdentityStore,
    ledger: Ledger,
    session: String,
    key: String,
    key_id: i64,
    account: i64,
}
async fn seed(pool: &PgPool, amount: i64) -> Seed {
    let (identity, ledger, session, key, key_id, account) = fixture(pool, amount).await;
    Seed {
        identity,
        ledger,
        session,
        key,
        key_id,
        account,
    }
}
fn core_binary() -> PathBuf {
    let path = std::env::var_os("LMM_MODEL_PROCESS_CORE_BIN")
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from(env!("CARGO_BIN_EXE_lmm-core")));
    assert!(path.is_absolute() && path.is_file());
    assert_eq!(
        path.canonicalize().unwrap(),
        path,
        "Core binary must not use a symlink"
    );
    let expected = std::env::var("LMM_MODEL_PROCESS_CORE_SHA256")
        .expect("use the owned-PG runner with an explicitly frozen Core digest");
    assert_eq!(expected.len(), 64);
    assert!(
        expected
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
    );
    let actual = format!("{:x}", Sha256::digest(fs::read(&path).unwrap()));
    assert_eq!(actual, expected, "frozen Core binary changed before spawn");
    path
}
fn root() -> PathBuf {
    let p = PathBuf::from(std::env::var_os("LMM_MODEL_PROCESS_ROOT").unwrap());
    assert!(p.is_absolute());
    p
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
fn record(label: &str, value: &Value) {
    assert!(
        label
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
    );
    private(
        &root().join(format!("case-{label}.json")),
        serde_json::to_vec(value).unwrap().as_slice(),
    );
}
fn process_start_ticks(pid: u32) -> std::io::Result<String> {
    let raw = fs::read_to_string(format!("/proc/{pid}/stat"))?;
    let end = raw.rfind(')').ok_or(std::io::ErrorKind::InvalidData)?;
    let value = raw[end + 1..]
        .split_whitespace()
        .nth(19)
        .ok_or(std::io::ErrorKind::InvalidData)?;
    value
        .parse::<u64>()
        .map_err(|_| std::io::ErrorKind::InvalidData)?;
    Ok(value.to_owned())
}
fn executable_observation(pid: u32, expected: &str) -> Value {
    use std::{io::Read, os::fd::AsRawFd};
    let observe = || -> std::io::Result<Value> {
        let path = PathBuf::from(format!("/proc/{pid}/exe"));
        let mut file = fs::File::open(&path)?;
        let metadata = file.metadata()?;
        let mut bytes = Vec::new();
        file.read_to_end(&mut bytes)?;
        let sha256 = format!("{:x}", Sha256::digest(&bytes));
        let current_proc_path = fs::read_link(&path);
        Ok(json!({
            "pid": pid,
            "executed_path": fs::read_link(format!("/proc/self/fd/{}", file.as_raw_fd()))?,
            "current_proc_path": current_proc_path.as_ref().ok(),
            "current_proc_path_error": current_proc_path.as_ref().err().map(|error| format!("{:?}", error.kind())),
            "executed_sha256": sha256,
            "matches_expected_sha256": sha256 == expected,
            "device": metadata.dev(),
            "inode": metadata.ino()
        }))
    };
    match observe() {
        Ok(value) => value,
        Err(error) => json!({
            "pid": pid,
            "error_kind": format!("{:?}", error.kind()),
            "os_error": error.raw_os_error()
        }),
    }
}
struct Host {
    endpoint: String,
    config: PathBuf,
    directory: PathBuf,
    child: Option<Child>,
    generation: u32,
    role: Option<restricted_runtime::RuntimeRole>,
}
impl Drop for Host {
    fn drop(&mut self) {
        if let Some(mut p) = self.child.take() {
            let _ = p.kill();
            let _ = p.wait();
        }
    }
}
impl Host {
    async fn stop(&mut self) {
        if let Some(mut child) = self.child.take() {
            assert!(
                child.try_wait().unwrap().is_none(),
                "owned Core exited before requested shutdown"
            );
            let result = Command::new("/usr/bin/kill")
                .args(["-TERM", &child.id().to_string()])
                .status()
                .unwrap();
            assert!(result.success());
            let deadline = Instant::now() + Duration::from_secs(10);
            loop {
                if let Some(status) = child.try_wait().unwrap() {
                    assert!(status.success(), "owned Core did not exit normally");
                    break;
                }
                if Instant::now() >= deadline {
                    let _ = child.kill();
                    let _ = child.wait();
                    panic!("owned Core graceful shutdown timed out");
                }
                tokio::time::sleep(Duration::from_millis(25)).await;
            }
        }
    }
    async fn restart(&mut self) {
        self.stop().await;
        self.generation += 1;
        let log = self.directory.join(format!("core-{}.log", self.generation));
        let output = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(log)
            .unwrap();
        let binary = core_binary();
        // Hold the verified target inode through the ready/identity boundary.
        let target_file = fs::File::open(&binary).unwrap();
        let target_metadata = target_file.metadata().unwrap();
        self.child = Some(
            Command::new(&binary)
                .env_clear()
                .env("PATH", "/usr/bin:/bin")
                .arg("--config")
                .arg(&self.config)
                .stdin(Stdio::null())
                .stdout(output.try_clone().unwrap())
                .stderr(output)
                .spawn()
                .unwrap(),
        );
        let pid = self.child.as_ref().unwrap().id();
        let expected = std::env::var("LMM_MODEL_PROCESS_CORE_SHA256").unwrap();
        let startup = executable_observation(pid, &expected);
        let started = process_start_ticks(pid);
        private(
            &self.directory.join(format!("startup-{}.json", self.generation)),
            &serde_json::to_vec(&json!({
                "pid": pid,
                "start_ticks": started.as_ref().ok(),
                "start_ticks_error": started.as_ref().err().map(|error| format!("{:?}", error.kind())),
                "executable_observation": startup
            }))
            .unwrap(),
        );
        let start_ticks = started.unwrap();
        let client = reqwest::Client::builder()
            .no_proxy()
            .timeout(Duration::from_secs(2))
            .build()
            .unwrap();
        let deadline = Instant::now() + Duration::from_secs(15);
        loop {
            assert_eq!(self.child.as_ref().unwrap().id(), pid);
            assert_eq!(process_start_ticks(pid).unwrap(), start_ticks);
            assert!(
                self.child.as_mut().unwrap().try_wait().unwrap().is_none(),
                "owned Core exited; inspect private log"
            );
            if let Ok(response) = client
                .get(format!("{}/health/ready", self.endpoint))
                .send()
                .await
                && response.status() == StatusCode::OK
            {
                let value: Value = response.json().await.unwrap();
                assert_eq!(value["capabilities"]["models"], true);
                assert_eq!(value["capabilities"]["models_streaming"], true);
                break;
            }
            assert!(Instant::now() < deadline, "owned Core readiness timeout");
            tokio::time::sleep(Duration::from_millis(25)).await;
        }
        // Readiness never substitutes for executable identity. This is one
        // strict final check, not a loop that waits for a hash to match.
        use std::io::Read;
        let executable = PathBuf::from(format!("/proc/{pid}/exe"));
        let mut executed_file = fs::File::open(&executable).unwrap();
        let executed_metadata = executed_file.metadata().unwrap();
        let mut executed_bytes = Vec::new();
        executed_file.read_to_end(&mut executed_bytes).unwrap();
        let actual_sha256 = format!("{:x}", Sha256::digest(&executed_bytes));
        assert_eq!(actual_sha256, expected);
        let target_identity = (target_metadata.dev(), target_metadata.ino());
        assert_eq!(
            (executed_metadata.dev(), executed_metadata.ino()),
            target_identity
        );
        let held_target = target_file.metadata().unwrap();
        assert_eq!((held_target.dev(), held_target.ino()), target_identity);
        let current_target = fs::metadata(&binary).unwrap();
        assert_eq!(
            (current_target.dev(), current_target.ino()),
            target_identity
        );
        assert_eq!(fs::read_link(&executable).unwrap(), binary);
        let current_executed = fs::metadata(&executable).unwrap();
        assert_eq!(
            (current_executed.dev(), current_executed.ino()),
            target_identity
        );
        assert_eq!(self.child.as_ref().unwrap().id(), pid);
        assert_eq!(process_start_ticks(pid).unwrap(), start_ticks);
        assert!(self.child.as_mut().unwrap().try_wait().unwrap().is_none());
        private(
            &self
                .directory
                .join(format!("process-{}.json", self.generation)),
            &serde_json::to_vec(&json!({
                "pid": pid,
                "start_ticks": start_ticks,
                "boot_id": fs::read_to_string("/proc/sys/kernel/random/boot_id").unwrap().trim(),
                "endpoint": self.endpoint,
                "role": self.role.as_ref().unwrap().name,
                "config": self.config,
                "generation": self.generation,
                "binary_path": binary,
                "executed_path": fs::read_link(&executable).unwrap(),
                "executed_sha256": actual_sha256,
                "device": executed_metadata.dev(),
                "inode": executed_metadata.ino(),
                "startup_observation": format!("startup-{}.json", self.generation)
            }))
            .unwrap(),
        );
    }
    async fn finish(mut self) {
        self.stop().await;
        self.role.take().unwrap().cleanup().await;
    }
}
async fn start_core(pool: &PgPool, provider: &Provider, keys: &[i64], label: &str) -> Host {
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
    let role = restricted_runtime::RuntimeRole::create(pool).await;
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
    let directory = root().join(format!("{label}-{:016x}", rand::random::<u64>()));
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
lease_seconds=20
timeout_seconds=3
max_output_tokens=20
local_streaming=true
credentials=[{{reference={{id=7,revision=1}},secret_file='{}'}}]
key_groups=[{grants}]
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
        directory.join("upstream.token").display(),
        provider.endpoint
    );
    private(&directory.join("model.toml"), model.as_bytes());
    let address = std::net::TcpListener::bind("127.0.0.1:0")
        .unwrap()
        .local_addr()
        .unwrap();
    let config = directory.join("core.toml");
    private(&config,format!("version=1\n[server]\nlisten='{address}'\nworker_threads=2\nmax_inflight=16\ndrain_timeout_seconds=3\n[database]\nurl_file='{}'\nmax_connections=2\n[model]\nconfig_file='{}'\nlocal_http=true\n",directory.join("runtime.url").display(),directory.join("model.toml").display()).as_bytes());
    let mut host = Host {
        endpoint: format!("http://{address}"),
        config,
        directory,
        child: None,
        generation: 0,
        role: Some(role),
    };
    host.restart().await;
    host
}
async fn http(
    host: &Host,
    key: Option<&str>,
    path: &str,
    prompt: &str,
    id: &str,
    stream: bool,
) -> reqwest::Response {
    let client = reqwest::Client::builder()
        .no_proxy()
        .timeout(Duration::from_secs(10))
        .build()
        .unwrap();
    let body = if path.ends_with("responses") {
        json!({"model":"local-model","input":prompt,"max_output_tokens":20,"stream":stream})
    } else {
        json!({"model":"local-model","messages":[{"role":"user","content":prompt}],"max_tokens":20,"stream":stream,"stream_options":if stream{json!({"include_usage":true})}else{Value::Null}})
    };
    let mut request = client
        .post(format!("{}{path}", host.endpoint))
        .header("idempotency-key", id)
        .json(&body);
    if let Some(key) = key {
        request = request.bearer_auth(key);
    }
    request.send().await.unwrap()
}
async fn balance(ledger: &Ledger, account: i64, available: i64, reserved: i64) {
    let b = ledger.balance(account).await.unwrap();
    assert_eq!((b.available_units, b.reserved_units), (available, reserved));
}
async fn balanced(pool: &PgPool) {
    let bad:i64=sqlx::query_scalar("SELECT count(*) FROM (SELECT j.id FROM core_billing.ledger_journals j LEFT JOIN core_billing.ledger_entries e ON e.journal_id=j.id GROUP BY j.id HAVING count(e.journal_id)<2 OR sum(e.delta_units)<>0) q").fetch_one(pool).await.unwrap();
    assert_eq!(bad, 0, "every journal needs at least two balanced entries");
}
async fn paid(pool: &PgPool, account: i64, id: &str) {
    let row:(String,i64,i64,i64,i64,i64,i64)=sqlx::query_as("SELECT state,reserved,settled,payer_account_id,input_tokens,cached_tokens,output_tokens FROM core_charging.requests WHERE id=$1").bind(id).fetch_one(pool).await.unwrap();
    assert_eq!(row, ("settled".into(), 140, 49, account, 10, 2, 20));
    let hold:(i64,i64,bool,bool,bool)=sqlx::query_as("SELECT reserved,captured,closed,reservation_journal_id IS NOT NULL,capture_journal_id IS NOT NULL FROM core_charging.ledger_holds WHERE request_id=$1").bind(id).fetch_one(pool).await.unwrap();
    assert_eq!(hold, (140, 49, true, true, true));
    let surplus:i64=sqlx::query_scalar("SELECT e.delta_units FROM core_charging.ledger_holds h JOIN core_billing.ledger_entries e ON e.journal_id=h.capture_journal_id JOIN core_billing.ledger_accounts a ON a.id=e.ledger_account_id WHERE h.request_id=$1 AND a.owner_account_id=$2 AND a.bucket='wallet'").bind(id).bind(account).fetch_one(pool).await.unwrap();
    assert_eq!(surplus, 91);
    balanced(pool).await;
}
async fn denied_empty(pool: &PgPool, provider: &Provider) {
    assert_eq!(provider.hits.load(Ordering::SeqCst), 0);
    for table in ["requests", "ledger_holds", "ledger_commands"] {
        let n: i64 = sqlx::query_scalar(&format!("SELECT count(*) FROM core_charging.{table}"))
            .fetch_one(pool)
            .await
            .unwrap();
        assert_eq!(n, 0);
    }
    balanced(pool).await;
}
#[sqlx::test(migrations = false)]
async fn independent_process_personal_permissions_and_idempotency(pool: PgPool) {
    let s = seed(&pool, 1000).await;
    let poor = s.identity.bootstrap_user(12, 1).await.unwrap();
    let poor_account = s
        .identity
        .authorize(&poor.secret)
        .await
        .unwrap()
        .owner_account_id;
    s.ledger.open_wallet(poor_account).await.unwrap();
    let poor_key = s
        .identity
        .issue_key(
            &poor.secret,
            Account {
                kind: AccountKind::Personal,
                id: 12,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    sqlx::query("INSERT INTO core_identity.api_key_settings(credential_id,name,group_name) VALUES ($1,'synthetic-wrong-group','wrong-group')").bind(s.key_id).execute(&pool).await.unwrap();
    sqlx::query("INSERT INTO core_identity.user_routing_groups(user_id,group_name,source_reference) VALUES (7,'allowed','synthetic:process-model')").execute(&pool).await.unwrap();
    let p = Provider::start(true).await;
    let host = start_core(&pool, &p, &[s.key_id, poor_key.id], "personal").await;
    for (credential, expected, id) in [
        (None, StatusCode::UNAUTHORIZED, "missing"),
        (Some(s.session.as_str()), StatusCode::FORBIDDEN, "session"),
        (Some(s.key.as_str()), StatusCode::FORBIDDEN, "wrong-group"),
        (
            Some(poor_key.secret.as_str()),
            StatusCode::PAYMENT_REQUIRED,
            "poor",
        ),
    ] {
        let response = http(
            &host,
            credential,
            "/v1/chat/completions",
            "hello",
            id,
            false,
        )
        .await;
        assert_eq!(response.status(), expected);
        let _: Value = response.json().await.unwrap();
        denied_empty(&pool, &p).await;
    }
    sqlx::query(
        "UPDATE core_identity.api_key_settings SET group_name='allowed' WHERE credential_id=$1",
    )
    .bind(s.key_id)
    .execute(&pool)
    .await
    .unwrap();
    for (path, id) in [
        ("/v1/chat/completions", "normal-chat"),
        ("/v1/responses", "normal-responses"),
    ] {
        let r = http(&host, Some(&s.key), path, "hello", id, false).await;
        assert_eq!(r.status(), StatusCode::OK);
        let request_id = r.headers()["x-request-id"].to_str().unwrap().to_owned();
        let _: Value = r.json().await.unwrap();
        paid(&pool, s.account, &request_id).await;
        let duplicate = http(&host, Some(&s.key), path, "hello", id, false).await;
        assert_eq!(duplicate.status(), StatusCode::CONFLICT);
        let changed = http(&host, Some(&s.key), path, "changed", id, false).await;
        assert_eq!(changed.status(), StatusCode::CONFLICT);
    }
    let (one, two) = tokio::join!(
        http(
            &host,
            Some(&s.key),
            "/v1/chat/completions",
            "hello",
            "same-key",
            false
        ),
        http(
            &host,
            Some(&s.key),
            "/v1/chat/completions",
            "hello",
            "same-key",
            false
        )
    );
    assert!(
        (one.status() == StatusCode::OK && two.status() == StatusCode::CONFLICT)
            || (two.status() == StatusCode::OK && one.status() == StatusCode::CONFLICT)
    );
    let winner = if one.status() == StatusCode::OK {
        one
    } else {
        two
    };
    let id = winner.headers()["x-request-id"]
        .to_str()
        .unwrap()
        .to_owned();
    let _: Value = winner.json().await.unwrap();
    paid(&pool, s.account, &id).await;
    balance(&s.ledger, s.account, 853, 0).await;
    balance(&s.ledger, poor_account, 0, 0).await;
    assert_eq!(p.hits.load(Ordering::SeqCst), 3);
    assert!(
        p.auth
            .lock()
            .unwrap()
            .iter()
            .all(|v| v == "Bearer synthetic-upstream-key")
    );
    record(
        "personal",
        &json!({"real_core_tcp":true,"dispatches":3,"available":853,"reserved":0,"denials":[401,403,403,402],"same_key_successes":1}),
    );
    host.finish().await;
}
#[sqlx::test(migrations = false)]
async fn independent_process_team_payer_fallback_and_revocation(pool: PgPool) {
    let s = seed(&pool, 0).await;
    let member = s.identity.bootstrap_user(8, 0).await.unwrap();
    let personal = s
        .identity
        .authorize(&member.secret)
        .await
        .unwrap()
        .owner_account_id;
    s.ledger.open_wallet(personal).await.unwrap();
    let team = s.identity.create_team(&s.session).await.unwrap();
    let invitation = s
        .identity
        .invite(
            &s.session,
            team,
            &lmm_core::identity::InviteRequest {
                recipient_user_id: 8,
                role: lmm_core::accounts::TeamRole::Member,
                can_spend: true,
                ttl_seconds: 3600,
            },
        )
        .await
        .unwrap();
    s.identity
        .accept_invite(&member.secret, &invitation.secret)
        .await
        .unwrap();
    let team_ref = Account {
        kind: AccountKind::Team,
        id: team,
    };
    let team_key = s
        .identity
        .issue_key(
            &member.secret,
            team_ref,
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let mut policy = FundingPolicy::new_personal_key();
    policy.account_order = Some(vec![
        Account {
            kind: AccountKind::Personal,
            id: 8,
        },
        team_ref,
    ]);
    let fallback = s
        .identity
        .issue_key(
            &member.secret,
            Account {
                kind: AccountKind::Personal,
                id: 8,
            },
            policy,
            3600,
        )
        .await
        .unwrap();
    let team_account = s
        .identity
        .authorize(&team_key.secret)
        .await
        .unwrap()
        .owner_account_id;
    s.ledger.open_wallet(team_account).await.unwrap();
    s.ledger
        .execute(&LedgerRequest {
            scope: "independent-process-model-team".into(),
            operation_key: "synthetic-opening".into(),
            actor_user_id: Some(7),
            reason: "Owned disposable PG synthetic team credits".into(),
            action: Action::Credit {
                account_id: team_account,
                amount_units: Credits::try_from(1000).unwrap(),
                payment_reference: "synthetic:model-process-team".into(),
            },
        })
        .await
        .unwrap();
    let p = Provider::start(true).await;
    let host = start_core(&pool, &p, &[team_key.id, fallback.id], "team").await;
    for (index, key) in [&team_key, &fallback].into_iter().enumerate() {
        let r = http(
            &host,
            Some(&key.secret),
            "/v1/chat/completions",
            "hello",
            &format!("team-{index}"),
            false,
        )
        .await;
        assert_eq!(r.status(), StatusCode::OK);
        let id = r.headers()["x-request-id"].to_str().unwrap().to_owned();
        let _: Value = r.json().await.unwrap();
        paid(&pool, team_account, &id).await;
        let owner: i64 =
            sqlx::query_scalar("SELECT owner_account_id FROM core_charging.requests WHERE id=$1")
                .bind(&id)
                .fetch_one(&pool)
                .await
                .unwrap();
        assert_eq!(owner, if index == 0 { team_account } else { personal });
        balance(&s.ledger, personal, 0, 0).await;
        balance(&s.ledger, s.account, 0, 0).await;
    }
    let version: i64 = sqlx::query_scalar(
        "SELECT version FROM core_identity.memberships WHERE team_id=$1 AND user_id=8",
    )
    .bind(team)
    .fetch_one(&pool)
    .await
    .unwrap();
    s.identity
        .remove_member(&s.session, team, 8, version)
        .await
        .unwrap();
    let journals: i64 = sqlx::query_scalar("SELECT count(*) FROM core_billing.ledger_journals")
        .fetch_one(&pool)
        .await
        .unwrap();
    for (index, key) in [&team_key, &fallback].into_iter().enumerate() {
        assert_eq!(
            http(
                &host,
                Some(&key.secret),
                "/v1/chat/completions",
                "hello",
                &format!("revoked-{index}"),
                false
            )
            .await
            .status(),
            StatusCode::FORBIDDEN
        );
    }
    assert_eq!(p.hits.load(Ordering::SeqCst), 2);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM core_billing.ledger_journals")
            .fetch_one(&pool)
            .await
            .unwrap(),
        journals
    );
    balance(&s.ledger, team_account, 902, 0).await;
    balanced(&pool).await;
    record(
        "team",
        &json!({"real_core_tcp":true,"team_payer":team_account,"personal_payer":personal,"team_available":902,"personal_available":0,"revoked_dispatch_increment":0}),
    );
    host.finish().await;
}
#[sqlx::test(migrations = false)]
async fn independent_process_distinct_requests_compete_for_real_budget(pool: PgPool) {
    let s = seed(&pool, 1000).await;
    lmm_core::billing::charging::Charging::new(pool.clone(), Arc::new(TransactionalLedger))
        .set_budget(
            &s.session,
            &lmm_core::billing::charging::Budget {
                scope: "key".into(),
                account_id: None,
                user_id: None,
                key_id: Some(s.key_id),
                period: "lifetime".into(),
                anchor: 0,
                seconds: 0,
                limit: 200,
            },
        )
        .await
        .unwrap();
    let p = Provider::start(true).await;
    let host = start_core(&pool, &p, &[s.key_id], "budget").await;
    let (one, two) = tokio::join!(
        http(
            &host,
            Some(&s.key),
            "/v1/chat/completions",
            "stream-pause",
            "budget-one",
            true
        ),
        http(
            &host,
            Some(&s.key),
            "/v1/chat/completions",
            "stream-pause",
            "budget-two",
            true
        )
    );
    assert!(
        (one.status() == StatusCode::OK && two.status() == StatusCode::PAYMENT_REQUIRED)
            || (two.status() == StatusCode::OK && one.status() == StatusCode::PAYMENT_REQUIRED)
    );
    let winner = if one.status() == StatusCode::OK {
        one
    } else {
        two
    };
    let id = winner.headers()["x-request-id"]
        .to_str()
        .unwrap()
        .to_owned();
    assert_eq!(p.hits.load(Ordering::SeqCst), 1);
    balance(&s.ledger, s.account, 860, 140).await;
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM core_charging.requests")
            .fetch_one(&pool)
            .await
            .unwrap(),
        1
    );
    p.stream_gate.add_permits(1);
    let body = winner.text().await.unwrap();
    assert!(body.ends_with("data: [DONE]\n\n"));
    paid(&pool, s.account, &id).await;
    balance(&s.ledger, s.account, 951, 0).await;
    record(
        "budget",
        &json!({"real_core_tcp":true,"wallet_opening":1000,"budget":200,"reserve":140,"concurrent_distinct_dispatches":1,"settled":49,"available":951,"reserved":0}),
    );
    host.finish().await;
}
