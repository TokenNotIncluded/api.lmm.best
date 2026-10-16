//! Actual Core and Go main-process payment recovery. Synthetic signed proofs;
//! bootstrap replaces provider transport only. No external provider or real funds.
#![cfg(unix)]
use base64::{Engine as _, engine::general_purpose::STANDARD};
use hmac::{Hmac, Mac};
use lmm_core::{
    billing::ledger::Ledger,
    core_schema,
    identity::IdentityStore,
    internal_rpc::pb,
    payments::{Channel, PaymentsStore},
};
use prost::Message;
use reqwest::{Client, StatusCode};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{PgPool, postgres::PgPoolOptions};
use std::{
    fs,
    os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt},
    path::{Path, PathBuf},
    process::{Child, Command, Stdio},
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};
#[path = "common/runtime.rs"]
mod runtime;
const SECRET: &str = "whsec_local_synthetic_endpoint_only";
const MERCHANT: &str = "acct_local_synthetic";
struct Process(Child, Value, PathBuf, String);

fn binary_sha(binary: &Path, variable: &str, required: bool) -> String {
    assert!(binary.is_absolute() && binary.is_file());
    let actual = format!("{:x}", Sha256::digest(fs::read(binary).unwrap()));
    match std::env::var(variable) {
        Ok(expected) => {
            assert!(expected.len() == 64 && expected.bytes().all(|b| b.is_ascii_hexdigit()));
            assert_eq!(
                actual,
                expected.to_ascii_lowercase(),
                "binary digest differs from frozen input"
            );
        }
        Err(std::env::VarError::NotPresent) => {
            assert!(!required, "external Core requires its frozen SHA256")
        }
        Err(error) => panic!("invalid binary digest environment: {error}"),
    }
    actual
}

fn observation(pid: u32, expected: &str) -> Value {
    use std::io::Read;
    let stat = || {
        let text = fs::read_to_string(format!("/proc/{pid}/stat")).unwrap();
        text.rsplit_once(") ")
            .unwrap()
            .1
            .split_whitespace()
            .nth(19)
            .unwrap()
            .parse::<u64>()
            .unwrap()
    };
    let startticks = stat();
    let path = PathBuf::from(format!("/proc/{pid}/exe"));
    let mut file = fs::File::open(&path).unwrap();
    let metadata = file.metadata().unwrap();
    let mut bytes = Vec::new();
    file.read_to_end(&mut bytes).unwrap();
    let digest = format!("{:x}", Sha256::digest(&bytes));
    assert_eq!(
        digest, expected,
        "actual product executable differs from frozen input"
    );
    let after = fs::metadata(&path).unwrap();
    assert_eq!((metadata.dev(), metadata.ino()), (after.dev(), after.ino()));
    assert_eq!(
        startticks,
        stat(),
        "product identity changed during observation"
    );
    json!({"pid":pid,"startticks":startticks,"executed_sha256":digest,
        "device":metadata.dev(),"inode":metadata.ino(),
        "executed_path":fs::read_link(&path).unwrap(),
        "boot_id":fs::read_to_string("/proc/sys/kernel/random/boot_id").unwrap().trim()})
}

fn record_identity(process: &Process, stage: &str) {
    let actual = observation(
        process.0.id(),
        process.1["executed_sha256"].as_str().unwrap(),
    );
    assert_eq!(
        actual, process.1,
        "product identity changed before/after readiness"
    );
    private(
        &process.2,
        &format!("{}.identity.{stage}.json", process.3),
        &serde_json::to_vec(&json!({"format":"lmm-commerce-process-identity-v1",
            "stage":stage,"identity":actual}))
        .unwrap(),
    );
}
impl Drop for Process {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}

fn private(root: &Path, name: &str, bytes: &[u8]) -> PathBuf {
    use std::io::Write;
    let path = root.join(name);
    fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(&path)
        .unwrap()
        .write_all(bytes)
        .unwrap();
    path
}

fn spawn(binary: &Path, expected: &str, config: &Path, root: &Path, log: &str) -> Process {
    assert_eq!(
        format!("{:x}", Sha256::digest(fs::read(binary).unwrap())),
        expected,
        "product binary changed before spawn"
    );
    let output = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(root.join(log))
        .unwrap();
    let mut command = Command::new(binary);
    // Neither process inherits a core DSN, caller-selected runtime settings or
    // unrelated credentials. File mode exercises the production config boundary.
    command
        .env_clear()
        .env("PATH", "/usr/bin:/bin")
        .env("GOMAXPROCS", "2")
        .arg("--config")
        .arg(config)
        .stdin(Stdio::null())
        .stdout(output.try_clone().unwrap())
        .stderr(output);
    // Install the kill/wait guard before any fallible /proc observation.
    let mut process = Process(
        command.spawn().expect("product process must start"),
        Value::Null,
        root.to_owned(),
        log.to_owned(),
    );
    process.1 = observation(process.0.id(), expected);
    record_identity(&process, "spawn");
    process
}

fn address() -> std::net::SocketAddr {
    std::net::TcpListener::bind("127.0.0.1:0")
        .unwrap()
        .local_addr()
        .unwrap()
}

async fn ready(client: &Client, endpoint: &str, process: &mut Process) -> Value {
    record_identity(process, "before-ready");
    let deadline = Instant::now() + Duration::from_secs(15);
    loop {
        assert!(
            process.0.try_wait().unwrap().is_none(),
            "product exited before readiness; inspect private process log"
        );
        if let Ok(response) = client.get(format!("{endpoint}/health/ready")).send().await
            && response.status() == StatusCode::OK
        {
            let body = response.json().await.unwrap();
            record_identity(process, "ready");
            assert!(
                process.0.try_wait().unwrap().is_none(),
                "product exited at readiness"
            );
            return body;
        }
        assert!(Instant::now() < deadline, "product readiness timed out");
        tokio::time::sleep(Duration::from_millis(25)).await;
    }
}

fn signed_event(intent: &pb::PaymentIntent, id: &str) -> Value {
    let event = json!({"id":id,"type":"payment_intent.succeeded","livemode":false,
        "data":{"object":{"id":format!("pi_{}",intent.intent_id),"object":"payment_intent",
        "status":"succeeded","amount":intent.amount_minor,"amount_received":intent.amount_minor,
        "currency":"usd","livemode":false,"metadata":{"lmm_core_intent_id":intent.intent_id.to_string(),
        "lmm_merchant":MERCHANT,"lmm_order_id":format!("top_{}",intent.intent_id)}}}});
    let payload = serde_json::to_string(&event).unwrap();
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_secs();
    let mut mac = Hmac::<Sha256>::new_from_slice(SECRET.as_bytes()).unwrap();
    mac.update(format!("{now}.").as_bytes());
    mac.update(payload.as_bytes());
    let hex: String = mac
        .finalize()
        .into_bytes()
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect();
    json!({"payload":payload,"signature":format!("t={now},v1={hex}")})
}
// Bootstrap is a short-lived test executable, not a product readiness target.
// Pin its actual executed image while alive, before expensive unoptimized SHA.
// Holding that FD permits ordinary exit during hashing without weakening any
// live-before/after observation of the long-running Core and Go products.
fn bootstrap_observation(pid: u32, expected: &str) -> Value {
    use std::io::Read;
    let ticks = || {
        fs::read_to_string(format!("/proc/{pid}/stat"))
            .unwrap()
            .rsplit_once(") ")
            .unwrap()
            .1
            .split_whitespace()
            .nth(19)
            .unwrap()
            .parse::<u64>()
            .unwrap()
    };
    let startticks = ticks();
    let mut executed = fs::File::open(format!("/proc/{pid}/exe")).unwrap();
    let metadata = executed.metadata().unwrap();
    assert_eq!(
        startticks,
        ticks(),
        "bootstrap PID identity changed before pinned-image hashing"
    );
    let mut bytes = Vec::new();
    executed.read_to_end(&mut bytes).unwrap();
    let digest = format!("{:x}", Sha256::digest(&bytes));
    assert_eq!(
        digest, expected,
        "actual bootstrap executed image differs from sealed artifact"
    );
    let after = executed.metadata().unwrap();
    assert_eq!((metadata.dev(), metadata.ino()), (after.dev(), after.ino()));
    json!({"pid":pid,"startticks":startticks,"device":metadata.dev(),"inode":metadata.ino(),"executed_sha256":digest,"attestation":"actual /proc/PID/exe FD pinned and startticks rechecked before hashing","short_process_may_exit_during_hash":true,"boot_id":fs::read_to_string("/proc/sys/kernel/random/boot_id").unwrap().trim()})
}
async fn bootstrap(binary: &Path, sha: &str, fixture: &Path, root: &Path, phase: &str) {
    assert_eq!(
        format!("{:x}", Sha256::digest(fs::read(binary).unwrap())),
        sha
    );
    let log = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(root.join(format!("bootstrap-{phase}.log")))
        .unwrap();
    let child = Command::new(binary)
        .env_clear()
        .env("PATH", "/usr/bin:/bin")
        .env("PAYMENT_CORE_PROOF_FIXTURE", fixture)
        .env("PAYMENT_CORE_PROOF_PHASE", phase)
        .args([
            "-test.run",
            "^TestRustDurableProofAcrossGoRestart$",
            "-test.v",
            "-test.timeout=50s",
        ])
        .stdin(Stdio::null())
        .stdout(log.try_clone().unwrap())
        .stderr(log)
        .spawn()
        .unwrap();
    let mut guard = Process(
        child,
        Value::Null,
        root.to_owned(),
        format!("bootstrap-{phase}"),
    );
    guard.1 = bootstrap_observation(guard.0.id(), sha);
    private(
        root,
        &format!("bootstrap-{phase}.identity.fd.json"),
        &serde_json::to_vec(&json!({"stage":"fd-attested","identity":guard.1})).unwrap(),
    );
    let end = Instant::now() + Duration::from_secs(55);
    let status = loop {
        if let Some(status) = guard.0.try_wait().unwrap() {
            break status;
        }
        assert!(Instant::now() < end, "bootstrap deadline");
        tokio::time::sleep(Duration::from_millis(20)).await;
    };
    use std::os::unix::process::ExitStatusExt;
    let exit_signal = status.signal();
    private(root,&format!("bootstrap-{phase}-exit.json"),&serde_json::to_vec(&json!({"exit_code":status.code(),"exit_signal":exit_signal,"fd_based_attestation":guard.1,"sha256":sha,"provider_transport":"synthetic in-process only","core_rpc":"real product OS process"})).unwrap());
    assert!(status.success(), "bootstrap failed; inspect private log");
}
fn crash(mut process: Process) {
    use std::os::unix::process::ExitStatusExt;
    let pid = process.0.id();
    assert_eq!(
        observation(pid, process.1["executed_sha256"].as_str().unwrap()),
        process.1
    );
    process.0.kill().unwrap();
    let status = process.0.wait().unwrap();
    assert_eq!(status.signal(), Some(9));
    private(
        &process.2,
        &format!("{}.crash.json", process.3),
        &serde_json::to_vec(&json!({"pid":pid,"signal":status.signal(),"reaped":true})).unwrap(),
    );
}
async fn financial(pool: &PgPool, account: i64, credits: i64) -> Value {
    let balance = Ledger::from_pool(pool.clone())
        .balance(account)
        .await
        .unwrap();
    assert_eq!(balance.available_units, credits);
    assert_eq!(balance.reserved_units, 0);
    let count: i64 =
        sqlx::query_scalar("SELECT count(*) FROM core_payments.receipts WHERE method='credit'")
            .fetch_one(pool)
            .await
            .unwrap();
    assert_eq!(count, if credits == 0 { 0 } else { 1 });
    let journals: i64 = sqlx::query_scalar("SELECT count(*) FROM core_billing.ledger_journals")
        .fetch_one(pool)
        .await
        .unwrap();
    assert_eq!(journals, count);
    let unbalanced:i64=sqlx::query_scalar("SELECT count(*) FROM (SELECT j.id FROM core_billing.ledger_journals j LEFT JOIN core_billing.ledger_entries e ON e.journal_id=j.id GROUP BY j.id HAVING count(e.journal_id)<>2 OR sum(e.delta_units)<>0) x").fetch_one(pool).await.unwrap();
    assert_eq!(unbalanced, 0);
    json!({"wallet":credits,"reserved":0,"credit_receipts":count,"journals":journals,"unbalanced":unbalanced})
}
// Bind immutable payment receipt bytes to the original job and exact ledger
// legs. A balanced unrelated journal must never satisfy this money assertion.
async fn credit_association(
    pool: &PgPool,
    account: i64,
    intent_id: i64,
    order: &Value,
    local_persisted: bool,
) -> Value {
    let jobs: Vec<_> = order["jobs"]
        .as_object()
        .unwrap()
        .values()
        .filter(|j| j["kind"] == "credit")
        .collect();
    assert_eq!(jobs.len(), 1);
    let job = jobs[0];
    let key = job["key"].as_str().unwrap();
    let row:(String,i64,String,Vec<u8>,Vec<u8>)=sqlx::query_as("SELECT id,intent_id,operation_key,request_hash,receipt_wire FROM core_payments.receipts WHERE method='credit'").fetch_one(pool).await.unwrap();
    let receipt = pb::PaymentReceipt::decode(row.4.as_slice()).unwrap();
    assert_eq!(receipt.receipt_id, row.0);
    assert_eq!(receipt.intent_id, row.1);
    assert_eq!(receipt.intent_id, intent_id);
    assert_eq!(receipt.idempotency_key, row.2);
    assert_eq!(receipt.idempotency_key, key);
    assert_eq!(receipt.state, pb::PaymentReceiptState::CreditApplied as i32);
    assert!(receipt.rejection_code.is_empty());
    assert_eq!(receipt.request_sha256.len(), 32);
    assert_eq!(receipt.request_sha256, row.3);
    let intent: (i64, i64, i64) = sqlx::query_as(
        "SELECT account_id,credit_units,credit_journal_id FROM core_payments.intents WHERE id=$1",
    )
    .bind(intent_id)
    .fetch_one(pool)
    .await
    .unwrap();
    assert_eq!(intent, (account, 500000, receipt.journal_id));
    assert!(receipt.journal_id > 0);
    let journal:(String,i64,i64,i64,String,String)=sqlx::query_as("SELECT kind,amount_units,payer_id,payee_id,scope,operation_key FROM core_billing.ledger_journals WHERE id=$1").bind(receipt.journal_id).fetch_one(pool).await.unwrap();
    assert_eq!(journal.0, "credit");
    assert_eq!(journal.1, 500000);
    let legs:Vec<(i64,Option<i64>,String,i64)>=sqlx::query_as("SELECT a.id,a.owner_account_id,a.bucket,e.delta_units FROM core_billing.ledger_entries e JOIN core_billing.ledger_accounts a ON a.id=e.ledger_account_id WHERE e.journal_id=$1 ORDER BY a.id").bind(receipt.journal_id).fetch_all(pool).await.unwrap();
    assert_eq!(legs.len(), 2);
    assert!(
        legs.iter()
            .any(|(id, owner, bucket, delta)| *id == journal.3
                && *owner == Some(account)
                && bucket == "wallet"
                && *delta == 500000)
    );
    assert!(
        legs.iter()
            .any(|(id, owner, bucket, delta)| *id == journal.2
                && owner.is_none()
                && bucket == "clearing"
                && *delta == -500000)
    );
    if local_persisted {
        let local = &job["receipt"];
        assert_eq!(order["credit_receipt"], receipt.receipt_id);
        assert_eq!(local["id"], receipt.receipt_id);
        assert_eq!(local["intent_id"], intent_id);
        assert_eq!(local["key"], key);
        assert_eq!(local["state"], "credit_applied");
        assert_eq!(local["journal_id"], receipt.journal_id);
        assert_eq!(
            local["request_hash"],
            STANDARD.encode(&receipt.request_sha256)
        );
    } else {
        assert!(job["receipt"].is_null());
    }
    json!({"receipt_id":receipt.receipt_id,"intent_id":intent_id,"original_operation_key":key,"request_sha256":receipt.request_sha256,"receipt_wire_sha256":format!("{:x}",Sha256::digest(&row.4)),"journal_id":receipt.journal_id,"journal_kind":journal.0,"journal_amount":journal.1,"payer_ledger_account":journal.2,"payee_ledger_account":journal.3,"journal_scope":journal.4,"journal_operation_key":journal.5,"exact_legs":legs})
}
async fn snapshot(go: &PgPool, id: &str) -> Value {
    sqlx::query_scalar("SELECT snapshot FROM public.payment_orders WHERE id=$1")
        .bind(id)
        .fetch_one(go)
        .await
        .unwrap()
}
// @mark payments.process-recovery role=test
#[sqlx::test(migrations = false)]
async fn real_core_and_go_payment_credit_commit_before_local_receipt_crash(pool: PgPool) {
    let required = |name: &str| {
        PathBuf::from(
            std::env::var_os(name).unwrap_or_else(|| panic!("required sealed input {name}")),
        )
    };
    let core_bin = required("LMM_PAYMENT_CORE_BIN");
    let core_sha = binary_sha(&core_bin, "LMM_PAYMENT_CORE_SHA256", true);
    let go_bin = required("LMM_PAYMENT_GO_BIN");
    let go_sha = binary_sha(&go_bin, "LMM_PAYMENT_GO_SHA256", true);
    let boot_bin = required("LMM_PAYMENT_BOOTSTRAP_BIN");
    let boot_sha = binary_sha(&boot_bin, "LMM_PAYMENT_BOOTSTRAP_SHA256", true);
    let root = std::env::temp_dir().join(format!(
        "pay-{}-{:08x}",
        std::process::id(),
        rand::random::<u32>()
    ));
    fs::create_dir(&root).unwrap();
    fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
    core_schema::init(&pool).await.unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    let user = identity.bootstrap_user(101, 0).await.unwrap().secret;
    let account = identity.authorize(&user).await.unwrap().owner_account_id;
    Ledger::from_pool(pool.clone())
        .open_wallet(account)
        .await
        .unwrap();
    let key = "prepare_real_go_durable";
    let store = PaymentsStore::new(
        pool.clone(),
        vec![Channel::stripe_test(
            1,
            MERCHANT.into(),
            SECRET.into(),
            100000,
        )],
    )
    .unwrap();
    let intent = store
        .prepare(
            &user,
            pb::PrepareTopupRequest {
                mutation: Some(pb::MutationContext {
                    idempotency_key: key.into(),
                    expected_version: None,
                }),
                account_id: account,
                payment_channel_id: 1,
                currency: "USD".into(),
                amount_minor: 100,
            },
        )
        .await
        .unwrap();
    let db = pool.connect_options().get_database().unwrap().to_owned();
    assert!(db.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'_'));
    sqlx::query(&format!("REVOKE CONNECT ON DATABASE \"{db}\" FROM PUBLIC"))
        .execute(&pool)
        .await
        .unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    role.optional("core_payments").await;
    role.optional("core_events").await;
    let database = format!("lmm_payments_proof_{:016x}", rand::random::<u64>());
    let owner = format!("{database}_owner");
    let login = format!("{database}_runtime");
    let password = format!("{:032x}", rand::random::<u128>());
    sqlx::raw_sql(&format!("CREATE ROLE {owner} NOLOGIN;CREATE ROLE {login} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '{password}';")).execute(&pool).await.unwrap();
    sqlx::query(&format!(
        "CREATE DATABASE {database} OWNER {owner} TEMPLATE template0"
    ))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::raw_sql(&format!("REVOKE ALL ON DATABASE {database} FROM PUBLIC;GRANT CONNECT ON DATABASE {database} TO {login};")).execute(&pool).await.unwrap();
    let mut url = reqwest::Url::parse(&std::env::var("DATABASE_URL").unwrap()).unwrap();
    assert!(matches!(url.host_str(), Some("127.0.0.1" | "localhost")));
    url.set_path(&database);
    let go_owner = PgPoolOptions::new()
        .max_connections(2)
        .connect(url.as_str())
        .await
        .unwrap();
    sqlx::raw_sql(include_str!(
        "../../extensions/internal/modules/payments/schema.sql"
    ))
    .execute(&go_owner)
    .await
    .unwrap();
    sqlx::raw_sql(&format!("REVOKE ALL ON SCHEMA public FROM PUBLIC;GRANT USAGE ON SCHEMA public TO {login};GRANT SELECT ON payment_storage_guard TO {login};GRANT SELECT,INSERT,UPDATE ON payment_orders,payment_events TO {login};REVOKE CREATE,TEMPORARY ON DATABASE {database} FROM PUBLIC;")).execute(&go_owner).await.unwrap();
    url.set_username(&login).unwrap();
    url.set_password(Some(&password)).unwrap();
    // This URL was allocated above in this fresh owned loopback cluster only.
    url.set_query(Some("sslmode=disable"));
    let go_url = private(&root, "go.url", url.as_str().as_bytes());
    let core_url = private(&root, "core.url", role.url.as_bytes());
    let host = "synthetic-payment-host-012345678901234567890123456789";
    let token = "payment_fixture_service_token_0123456789abcdef";
    let host_file = private(&root, "host.token", host.as_bytes());
    let identity_file = private(
        &root,
        "identity.token",
        b"payment-identity-only-012345678901234567890123456789",
    );
    let payment_file = private(&root, "payment.token", token.as_bytes());
    let secret_file = private(&root, "webhook.secret", SECRET.as_bytes());
    let permission=private(&root,"payments.toml",format!("version=1\nprovider='stripe-test'\n[permissions]\nprepare=true\ncredit_evidence=true\nread_user=true\nreconcile=true\nproof_ingress=true\nproof_read=true\n[[channels]]\nid=1\nmerchant='{MERCHANT}'\nwebhook_secret_file='{}'\nmax_usd_cents=100000\n",secret_file.display()).as_bytes());
    let adapters = private(
        &root,
        "go-payments.toml",
        b"version=1\nprovider='stripe-test'\nchannels=[]\n",
    );
    let socket = root.join("core.sock");
    let core_address = address();
    let go_address = address();
    let core_config=private(&root,"core.toml",format!("version=1\n[server]\nlisten='{core_address}'\n[database]\nurl_file='{}'\n[rpc]\nsocket='{}'\ntoken_file='{}'\n[payments]\nconfig_file='{}'\ntoken_file='{}'\n",core_url.display(),socket.display(),identity_file.display(),permission.display(),payment_file.display()).as_bytes());
    let go_config=private(&root,"go.toml",format!("version=1\n[host]\nlisten='{go_address}'\ntoken_file='{}'\nmodules=['payments']\n[core]\nsocket='{}'\nidentity_token_file='{}'\npayment_token_file='{}'\n[payments]\ndatabase_name='{database}'\ndatabase_url_file='{}'\nconfig_file='{}'\ncallbacks_enabled=false\n",host_file.display(),socket.display(),identity_file.display(),payment_file.display(),go_url.display(),adapters.display()).as_bytes());
    let fixture=private(&root,"bootstrap.json",&serde_json::to_vec(&json!({"socket":socket,"token":token,"user":user,"account":account,"channel":{"id":1,"provider":"stripe","merchant":MERCHANT,"environment":"test","currency":"USD","enabled":true},"intent_id":intent.intent_id,"prepare_key":key,"currency":"USD","amount_minor":100,"go_database_url_file":go_url,"go_database_name":database,"events":[signed_event(&intent,"evt_real_go_durable"),signed_event(&intent,"evt_real_go_ack_lost")]})).unwrap());
    let client = Client::builder()
        .no_proxy()
        .timeout(Duration::from_secs(3))
        .build()
        .unwrap();
    let core_endpoint = format!("http://{core_address}");
    let go_endpoint = format!("http://{go_address}");
    role.pool.close().await;
    let mut core = spawn(
        &core_bin,
        &core_sha,
        &core_config,
        &root,
        "core-initial.log",
    );
    assert_eq!(
        ready(&client, &core_endpoint, &mut core).await["capabilities"]["payments"],
        true
    );
    bootstrap(&boot_bin, &boot_sha, &fixture, &root, "recv").await;
    let initial = snapshot(&go_owner, &format!("top_{}", intent.intent_id)).await;
    assert!(initial.get("credit_receipt").is_none_or(Value::is_null));
    financial(&pool, account, 0).await;
    // A second genuine Core-accepted proof encounters an actual Go inbox error.
    sqlx::raw_sql("CREATE FUNCTION public.test_inbox_failure() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$ BEGIN RAISE EXCEPTION 'synthetic owned inbox failure'; END $$;REVOKE ALL ON FUNCTION public.test_inbox_failure() FROM PUBLIC;CREATE TRIGGER test_inbox_failure BEFORE INSERT ON public.payment_events FOR EACH ROW EXECUTE FUNCTION public.test_inbox_failure();").execute(&go_owner).await.unwrap();
    bootstrap(&boot_bin, &boot_sha, &fixture, &root, "recv-lost").await;
    let proofs: i64 = sqlx::query_scalar("SELECT count(*) FROM core_payments.accepted_proofs")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(proofs, 2);
    let inbox: i64 = sqlx::query_scalar("SELECT count(*) FROM payment_events")
        .fetch_one(&go_owner)
        .await
        .unwrap();
    assert_eq!(inbox, 1);
    financial(&pool, account, 0).await;
    sqlx::raw_sql("DROP TRIGGER test_inbox_failure ON public.payment_events;DROP FUNCTION public.test_inbox_failure();").execute(&go_owner).await.unwrap();
    // Owned test trigger blocks only local receipt persistence, AFTER Core money
    // commit. This is not a claim of precisely losing the Core network reply.
    sqlx::raw_sql("CREATE FUNCTION public.test_credit_persist_pause() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$ BEGIN IF NEW.snapshot->'credit_receipt' IS NOT NULL AND NEW.snapshot->'credit_receipt'<>'null'::jsonb THEN PERFORM pg_catalog.pg_sleep(20); END IF; RETURN NEW; END $$;REVOKE ALL ON FUNCTION public.test_credit_persist_pause() FROM PUBLIC;CREATE TRIGGER test_credit_persist_pause BEFORE UPDATE ON public.payment_orders FOR EACH ROW EXECUTE FUNCTION public.test_credit_persist_pause();").execute(&go_owner).await.unwrap();
    let mut go = spawn(&go_bin, &go_sha, &go_config, &root, "go-initial.log");
    ready(&client, &go_endpoint, &mut go).await;
    let end = Instant::now() + Duration::from_secs(20);
    let window = loop {
        let credit: i64 =
            sqlx::query_scalar("SELECT count(*) FROM core_payments.receipts WHERE method='credit'")
                .fetch_one(&pool)
                .await
                .unwrap();
        let sleeping: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND wait_event='PgSleep'",
        )
        .bind(&login)
        .fetch_one(&go_owner)
        .await
        .unwrap();
        let before = snapshot(&go_owner, &format!("top_{}", intent.intent_id)).await;
        if credit == 1 && sleeping == 1 && before.get("credit_receipt").is_none_or(Value::is_null) {
            break before;
        }
        assert!(
            Instant::now() < end,
            "did not witness real Core commit before local receipt persistence"
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    };
    record_identity(&core, "before-crash");
    record_identity(&go, "before-crash");
    let old_core = core.0.id();
    let old_go = go.0.id();
    let crash_association =
        credit_association(&pool, account, intent.intent_id, &window, false).await;
    private(&root,"crash-window.json",&serde_json::to_vec(&json!({"fault":"Core credit committed before Go local credit_receipt persistence","core_pid":old_core,"go_pid":old_go,"go_snapshot":window,"ledger_association":crash_association,"funds":financial(&pool,account,500000).await,"accepted_proofs":2,"inbox":1,"provider_transport_configured_during_main":false})).unwrap());
    crash(go);
    crash(core);
    assert!(!Path::new(&format!("/proc/{old_core}")).exists());
    assert!(!Path::new(&format!("/proc/{old_go}")).exists());
    sqlx::raw_sql("DROP TRIGGER test_credit_persist_pause ON public.payment_orders;DROP FUNCTION public.test_credit_persist_pause();").execute(&go_owner).await.unwrap();
    let mut core = spawn(
        &core_bin,
        &core_sha,
        &core_config,
        &root,
        "core-restart.log",
    );
    ready(&client, &core_endpoint, &mut core).await;
    let mut go = spawn(&go_bin, &go_sha, &go_config, &root, "go-restart.log");
    ready(&client, &go_endpoint, &mut go).await;
    assert_ne!(core.0.id(), old_core);
    assert_ne!(go.0.id(), old_go);
    // Honor the existing 30-second job lease; never rewrite it to speed recovery.
    let start = Instant::now();
    let end = start + Duration::from_secs(55);
    let final_order = loop {
        financial(&pool, account, 500000).await;
        let current = snapshot(&go_owner, &format!("top_{}", intent.intent_id)).await;
        if current
            .get("credit_receipt")
            .and_then(Value::as_str)
            .is_some_and(|v| !v.is_empty())
        {
            break current;
        }
        assert!(
            Instant::now() < end,
            "actual Go main worker did not recover original durable job"
        );
        tokio::time::sleep(Duration::from_millis(100)).await;
    };
    assert_eq!(
        window["jobs"]
            .as_object()
            .unwrap()
            .keys()
            .collect::<Vec<_>>(),
        final_order["jobs"]
            .as_object()
            .unwrap()
            .keys()
            .collect::<Vec<_>>()
    );
    let wire: Vec<u8> =
        sqlx::query_scalar("SELECT receipt_wire FROM core_payments.receipts WHERE method='credit'")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(!wire.is_empty());
    let receipt_id: String =
        sqlx::query_scalar("SELECT id FROM core_payments.receipts WHERE method='credit'")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(final_order["credit_receipt"], receipt_id);
    assert_eq!(final_order["credit_state"], "confirmed");
    let credit_jobs: Vec<_> = final_order["jobs"]
        .as_object()
        .unwrap()
        .values()
        .filter(|j| j["kind"] == "credit")
        .collect();
    assert_eq!(credit_jobs.len(), 1);
    let credit_job = credit_jobs[0];
    assert_eq!(credit_job["state"], "done");
    assert_eq!(credit_job["receipt"]["id"], receipt_id);
    let operation_key: String = sqlx::query_scalar(
        "SELECT operation_key FROM core_payments.receipts WHERE method='credit'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(credit_job["key"], operation_key);
    assert_eq!(credit_job["receipt"]["key"], operation_key);
    let accepted: i64 = sqlx::query_scalar("SELECT count(*) FROM core_payments.accepted_proofs")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(accepted, 2);
    let inbox_done: i64 =
        sqlx::query_scalar("SELECT count(*) FROM payment_events WHERE status='done'")
            .fetch_one(&go_owner)
            .await
            .unwrap();
    assert_eq!(inbox_done, 1);
    // Another automatic scheduler tick must remain idempotent.
    tokio::time::sleep(Duration::from_secs(6)).await;
    let replay = snapshot(&go_owner, &format!("top_{}", intent.intent_id)).await;
    assert_eq!(replay["credit_receipt"], final_order["credit_receipt"]);
    assert_eq!(replay["credit_state"], final_order["credit_state"]);
    assert_eq!(replay["intent"], final_order["intent"]);
    assert_eq!(
        replay["jobs"]
            .as_object()
            .unwrap()
            .keys()
            .collect::<Vec<_>>(),
        final_order["jobs"]
            .as_object()
            .unwrap()
            .keys()
            .collect::<Vec<_>>()
    );
    for (key, job) in replay["jobs"].as_object().unwrap() {
        if job["kind"] == "credit" {
            assert_eq!(job, &final_order["jobs"][key]);
        }
        if job["kind"] == "checkout" {
            assert_eq!(job["attempts"], 0);
            assert!(job["receipt"].is_null());
        }
    }
    let funds = financial(&pool, account, 500000).await;
    // Freeze actual Go main-worker recovery BEFORE any bootstrap money work.
    let main_association =
        credit_association(&pool, account, intent.intent_id, &replay, true).await;
    assert_eq!(main_association, crash_association);
    let main_recovery_ms = start.elapsed().as_millis();
    private(&root,"main-recovered.json",&serde_json::to_vec(&json!({"result":"PASS","stage":"actual Go main StartWorkers only","ledger_association":main_association,"core_pid":core.0.id(),"go_pid":go.0.id(),"funds":funds,"credit_receipt":receipt_id,"credit_job_key":operation_key,"order":replay,"inbox_done":1,"accepted_proofs":2,"recovery_ms":main_recovery_ms})).unwrap());
    // Same original signed payloads, no re-signing and no new provider event:
    // the second proof previously got HTTP503 after Core accepted it. This
    // explicit bootstrap replay is NOT the product's public callback ingress.
    let replay_start = Instant::now();
    bootstrap(&boot_bin, &boot_sha, &fixture, &root, "settle").await;
    let recovered = snapshot(&go_owner, &format!("top_{}", intent.intent_id)).await;
    assert_eq!(recovered["credit_receipt"], receipt_id);
    assert_eq!(recovered["credit_state"], "confirmed");
    assert_eq!(
        recovered["jobs"]
            .as_object()
            .unwrap()
            .keys()
            .collect::<Vec<_>>(),
        replay["jobs"]
            .as_object()
            .unwrap()
            .keys()
            .collect::<Vec<_>>()
    );
    for (key, job) in replay["jobs"].as_object().unwrap() {
        if job["kind"] == "credit" {
            assert_eq!(recovered["jobs"][key]["receipt"], job["receipt"]);
            assert_eq!(recovered["jobs"][key]["state"], "done");
            assert_eq!(recovered["jobs"][key]["key"], operation_key);
        }
    }
    let after_proofs: i64 =
        sqlx::query_scalar("SELECT count(*) FROM core_payments.accepted_proofs")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(after_proofs, 2);
    let inbox_counts: (i64, i64) =
        sqlx::query_as("SELECT count(*),count(*) FILTER (WHERE status='done') FROM payment_events")
            .fetch_one(&go_owner)
            .await
            .unwrap();
    assert_eq!(inbox_counts, (2, 2));
    financial(&pool, account, 500000).await;
    tokio::time::sleep(Duration::from_secs(6)).await;
    let stable = snapshot(&go_owner, &format!("top_{}", intent.intent_id)).await;
    assert_eq!(stable["credit_receipt"], receipt_id);
    assert_eq!(stable["credit_state"], "confirmed");
    let after_receipt: String =
        sqlx::query_scalar("SELECT id FROM core_payments.receipts WHERE method='credit'")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(after_receipt, receipt_id);
    let funds = financial(&pool, account, 500000).await;
    let replay_association =
        credit_association(&pool, account, intent.intent_id, &stable, true).await;
    assert_eq!(replay_association, crash_association);
    private(&root,"bootstrap-replay-recovered.json",&serde_json::to_vec(&json!({"result":"PASS","stage":"bootstrap synthetic transport HTTP replay after main-recovered.json","same_original_payloads":true,"ledger_association":replay_association,"accepted_proofs":after_proofs,"inbox":2,"inbox_done":2,"credit_receipt":receipt_id,"original_credit_job_key":operation_key,"funds":funds,"elapsed_ms":replay_start.elapsed().as_millis()})).unwrap());
    record_identity(&core, "final");
    record_identity(&go, "final");
    let new_core = core.0.id();
    let new_go = go.0.id();
    drop(go);
    drop(core);
    assert!(!Path::new(&format!("/proc/{new_core}")).exists());
    assert!(!Path::new(&format!("/proc/{new_go}")).exists());
    private(&root,"result.json",&serde_json::to_vec(&json!({"result":"PASS","funds":funds,"ledger_association":replay_association,"core_os_restart":true,"go_main_worker_recovery":true,"credit_exactly_once":true,"original_jobs_preserved":true,"accepted_proofs":2,"inbox":2,"inbox_done":2,"main_recovery_receipt":"main-recovered.json","bootstrap_replay_receipt":"bootstrap-replay-recovered.json","recovery_ms":main_recovery_ms,"product_pids":[old_core,old_go,new_core,new_go],"products_reaped":true,"full_payment_acceptance":false,"limits":["bootstrap synthetic provider transport; not production Stripe HTTP ingress","no true Core network reply loss claim","no 302s dual OS expiry test","refund release intentionally unavailable"]})).unwrap());
    go_owner.close().await;
    role.cleanup().await;
}
