//! Formal one-shot check-db behavior against real SQLx-owned PostgreSQL.
use lmm_core::{
    accounts::{Account, AccountKind},
    billing::{
        Credits,
        charging::{Charging, Price, Request as ChargeRequest},
        ledger::{Action, Ledger, Request as LedgerRequest},
        transactional_ledger::TransactionalLedger,
    },
    core_schema,
    funding::FundingPolicy,
    identity::IdentityStore,
};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Row};
use std::{
    fs::{self, File, OpenOptions},
    io::{Read, Write},
    os::unix::fs::OpenOptionsExt,
    path::PathBuf,
    process::{Command, Output, Stdio},
    sync::Arc,
    time::{Duration, Instant},
};
#[path = "common/runtime.rs"]
mod runtime;
fn command(args: &[&str]) -> Command {
    let mut c = Command::new(env!("CARGO_BIN_EXE_lmm-core"));
    c.args(args)
        .env_clear()
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    c
}
fn finish(mut c: Command) -> (u32, Output) {
    let mut child = c.spawn().unwrap();
    let pid = child.id();
    let until = Instant::now() + Duration::from_secs(20);
    loop {
        if child.try_wait().unwrap().is_some() {
            return (pid, child.wait_with_output().unwrap());
        }
        if Instant::now() >= until {
            let _ = child.kill();
            let _ = child.wait();
            panic!("check-db did not exit within its bounded test deadline");
        }
        std::thread::sleep(Duration::from_millis(10));
    }
}
fn parsed(output: &Output) -> Value {
    assert!(
        output.stderr.is_empty(),
        "check-db leaked an unsanitized error/progress line"
    );
    assert!(
        output.stdout.len() <= 16384 && output.stdout.ends_with(b"\n"),
        "check-db response is not one bounded JSON line"
    );
    serde_json::from_slice(&output.stdout).expect("check-db response must be valid JSON")
}
fn closed(value: &Value, expected: &[&str]) {
    let mut actual = value
        .as_object()
        .expect("required JSON object is missing")
        .keys()
        .map(String::as_str)
        .collect::<Vec<_>>();
    let mut expected = expected.to_vec();
    actual.sort_unstable();
    expected.sort_unstable();
    assert!(
        actual == expected,
        "check-db object does not have the fixed contract keys"
    );
}
fn rejected(output: &Output, exit: i32, code: &str) {
    assert_eq!(output.status.code(), Some(exit));
    let v = parsed(output);
    assert!(
        v == json!({"version":1,"protocol":"core.check-db.v1","status":"rejected","scope":"runtime_configured_capabilities","code":code}),
        "unexpected fixed rejection shape"
    );
    assert!(
        !String::from_utf8_lossy(&output.stdout).contains("do-not-echo"),
        "arbitrary argument or credential escaped rejection"
    );
}
struct PrivateConfig {
    dir: PathBuf,
    path: PathBuf,
}
impl PrivateConfig {
    fn new(url: &str, extra: &str) -> Self {
        use std::os::unix::fs::PermissionsExt;
        let dir = std::env::temp_dir().join(format!(
            "lmm-check-db-{}-{:016x}",
            std::process::id(),
            rand::random::<u64>()
        ));
        fs::create_dir(&dir).unwrap();
        fs::set_permissions(&dir, fs::Permissions::from_mode(0o700)).unwrap();
        let url_file = dir.join("database.url");
        let mut f = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(&url_file)
            .unwrap();
        f.write_all(url.as_bytes()).unwrap();
        f.write_all(b"\n").unwrap();
        let path = dir.join("runtime.toml");
        let document = format!(
            "version=1\n[database]\nurl_file={}\nmax_connections=1\n{}",
            json!(url_file.to_str().unwrap()),
            extra
        );
        let mut f = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(&path)
            .unwrap();
        f.write_all(document.as_bytes()).unwrap();
        Self { dir, path }
    }
    fn run(&self) -> (u32, Output) {
        finish(command(&[
            "--config",
            self.path.to_str().unwrap(),
            "check-db",
        ]))
    }
    fn cleanup(self) {
        fs::remove_dir_all(self.dir).unwrap();
    }
}
fn q(v: &str) -> String {
    format!("\"{}\"", v.replace('"', "\"\""))
}
async fn whole_state(pool: &PgPool) -> Value {
    let mut result = serde_json::Map::new();
    let tables=sqlx::query("SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_' AND c.relkind IN ('r','p') ORDER BY n.nspname COLLATE \"C\",c.relname COLLATE \"C\"").fetch_all(pool).await.unwrap();
    for r in tables {
        let schema: String = r.get("nspname");
        let name: String = r.get("relname");
        let rows:Value=sqlx::query_scalar(&format!("SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text COLLATE \"C\"),'[]'::jsonb) FROM {}.{} t",q(&schema),q(&name))).fetch_one(pool).await.unwrap();
        result.insert(format!("rows:{schema}.{name}"), rows);
    }
    let sequences=sqlx::query("SELECT n.nspname,c.relname,pg_get_userbyid(c.relowner) AS owner,c.relacl::text AS acl,s.seqtypid::bigint,s.seqstart,s.seqincrement,s.seqmin,s.seqmax,s.seqcache,s.seqcycle FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_sequence s ON s.seqrelid=c.oid WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_' ORDER BY n.nspname COLLATE \"C\",c.relname COLLATE \"C\"").fetch_all(pool).await.unwrap();
    for r in sequences {
        let schema: String = r.get("nspname");
        let name: String = r.get("relname");
        let (last, called): (i64, bool) = sqlx::query_as(&format!(
            "SELECT last_value,is_called FROM {}.{}",
            q(&schema),
            q(&name)
        ))
        .fetch_one(pool)
        .await
        .unwrap();
        result.insert(format!("sequence:{schema}.{name}"),json!({"last_value":last,"is_called":called,"type":r.get::<i64,_>("seqtypid"),"start":r.get::<i64,_>("seqstart"),"increment":r.get::<i64,_>("seqincrement"),"min":r.get::<i64,_>("seqmin"),"max":r.get::<i64,_>("seqmax"),"cache":r.get::<i64,_>("seqcache"),"cycle":r.get::<bool,_>("seqcycle"),"owner":r.get::<String,_>("owner"),"acl":r.get::<Option<String>,_>("acl")}));
    }
    let catalog = sqlx::query(core_schema::schema_manifest().catalog.query_utf8)
        .fetch_all(pool)
        .await
        .unwrap()
        .iter()
        .map(|r| r.get::<String, _>("definition"))
        .collect::<Vec<_>>();
    result.insert("catalog".into(), json!(catalog));
    let acl:Value=sqlx::query_scalar("SELECT jsonb_build_object('schemas',(SELECT jsonb_agg(jsonb_build_array(nspname,nspowner,nspacl::text) ORDER BY nspname COLLATE \"C\") FROM pg_namespace WHERE nspname NOT IN ('pg_catalog','information_schema') AND left(nspname,3)<>'pg_'),'relations',(SELECT jsonb_agg(jsonb_build_array(n.nspname,c.relname,c.relowner,c.relacl::text) ORDER BY n.nspname COLLATE \"C\",c.relname COLLATE \"C\") FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'),'functions',(SELECT jsonb_agg(jsonb_build_array(n.nspname,p.proname,p.proowner,p.proacl::text,p.prosecdef,p.proconfig) ORDER BY n.nspname COLLATE \"C\",p.proname COLLATE \"C\",p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'))").fetch_one(pool).await.unwrap();
    result.insert("owners_acl_secdef".into(), acl);
    Value::Object(result)
}
async fn funded_fixture(pool: &PgPool, role: &runtime::RuntimeRole) {
    let identity = IdentityStore::from_pool(pool.clone());
    let user = identity.bootstrap_user(7001, 0).await.unwrap();
    let account: i64 =
        sqlx::query_scalar("SELECT personal_account_id FROM core_identity.users WHERE id=7001")
            .fetch_one(pool)
            .await
            .unwrap();
    let key = identity
        .issue_key(
            &user.secret,
            Account {
                kind: AccountKind::Personal,
                id: 7001,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    let ledger = Ledger::from_pool(pool.clone());
    ledger.open_wallet(account).await.unwrap();
    ledger
        .execute(&LedgerRequest {
            scope: "check-db-synthetic".into(),
            operation_key: "credit".into(),
            actor_user_id: None,
            reason: "Isolated read-only operator acceptance".into(),
            action: Action::Credit {
                account_id: account,
                amount_units: Credits::try_from(1000).unwrap(),
                payment_reference: "synthetic-only".into(),
            },
        })
        .await
        .unwrap();
    let charging = Charging::new(role.pool.clone(), Arc::new(TransactionalLedger));
    let request = ChargeRequest {
        id: "check-db-original-hold".into(),
        fingerprint: [33; 32],
        reserve: 50,
        lease_seconds: 60,
        maximum_seconds: 120,
        price: Price {
            upstream_cost_group_ppm: None,
            version: "synthetic-v1".into(),
            model: "public-model".into(),
            group: "synthetic-group".into(),
            input_per_million: 1_000_000,
            cached_per_million: 0,
            output_per_million: 1_000_000,
        },
    };
    charging
        .reserve(&key.secret, &request, &[31; 32])
        .await
        .unwrap();
    charging.start(&request.id, &[31; 32]).await.unwrap();
}
fn executable_hash() -> String {
    let mut f = File::open(env!("CARGO_BIN_EXE_lmm-core")).unwrap();
    let mut h = Sha256::new();
    let mut buf = [0; 65536];
    loop {
        let n = f.read(&mut buf).unwrap();
        if n == 0 {
            break;
        }
        h.update(&buf[..n]);
    }
    format!("{:x}", h.finalize())
}
#[test]
fn explicit_config_legacy_and_unknown_arguments_fail_closed_without_echo() {
    let (_, o) = finish(command(&["check-db"]));
    rejected(&o, 2, "CHECK_DB_CONFIG_REQUIRED");
    let (_, o) = finish(command(&["--legacy-env", "check-db"]));
    rejected(&o, 2, "CHECK_DB_LEGACY_ENV_FORBIDDEN");
    for args in [
        &["check-db", "--config", "relative-do-not-echo"][..],
        &["check-db", "--unknown", "do-not-echo"],
        &["check-db", "serve"],
        &["check-db", "--config", "/missing", "--legacy-env"],
    ] {
        let (_, o) = finish(command(args));
        rejected(&o, 2, "CHECK_DB_ARGUMENTS_INVALID");
    }
    let (_, o) = finish(command(&[
        "--config",
        "/nonexistent-do-not-echo/runtime.toml",
        "check-db",
    ]));
    rejected(&o, 2, "CHECK_DB_CONFIG_INVALID");
    // This socket is owned by the test and never speaks PostgreSQL. Keeping it
    // bound prevents contacting an unrelated local database on a raced port.
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let cfg = PrivateConfig::new(
        &format!(
            "postgresql://do-not-echo:do-not-echo@{}/unavailable",
            listener.local_addr().unwrap()
        ),
        "",
    );
    let (_, o) = cfg.run();
    rejected(&o, 3, "CHECK_DB_DATABASE_UNAVAILABLE");
    cfg.cleanup();
}
#[sqlx::test(migrations = false)]
async fn full_fresh_runtime_check_is_readonly_and_never_serves_or_loads_models(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    role.optional("core_charging").await;
    role.optional("core_model").await;
    funded_fixture(&pool, &role).await;
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let first = PrivateConfig::new(&role.url, "");
    let model = first.dir.join("invalid-model.toml");
    let mut f = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(&model)
        .unwrap();
    f.write_all(b"do-not-echo-invalid-model-document").unwrap();
    drop(f);
    let cfg = PrivateConfig::new(
        &role.url,
        &format!(
            "[server]\nlisten={}\n[model]\nconfig_file={}\n",
            json!(address.to_string()),
            json!(model.to_str().unwrap())
        ),
    );
    let before = whole_state(&pool).await;
    let expected = sqlx::query("SELECT current_database()::text AS name,d.oid::text AS oid,inet_server_addr()::text AS address,inet_server_port() AS port,floor(extract(epoch FROM pg_postmaster_start_time())*1000000)::bigint AS started,current_setting('server_version_num')::integer AS version,floor(extract(epoch FROM clock_timestamp())*1000000)::bigint AS lower FROM pg_database d WHERE d.datname=current_database()")
        .fetch_one(&pool).await.unwrap();
    let (pid, output) = cfg.run();
    let upper: i64 =
        sqlx::query_scalar("SELECT floor(extract(epoch FROM clock_timestamp())*1000000)::bigint")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(
        output.status.success(),
        "formal read-only runtime check rejected a legitimate fixture"
    );
    let v = parsed(&output);
    closed(
        &v,
        &[
            "version",
            "protocol",
            "status",
            "scope",
            "compiled_schema_manifest_sha256",
            "compiled_schema_manifest_hash_domain",
            "components",
            "catalog",
            "runtime_authority",
            "database",
            "observation",
            "system_identifier",
            "execution",
            "not_checked",
        ],
    );
    closed(
        &v["catalog"],
        &[
            "status",
            "fingerprint_sha256",
            "fingerprint_hash_domain",
            "matches_installed_receipt",
        ],
    );
    closed(
        &v["runtime_authority"],
        &["status", "required_capabilities"],
    );
    closed(
        &v["database"],
        &[
            "name",
            "oid",
            "server_address",
            "server_port",
            "postmaster_started_at_unix_micros",
            "observed_at_unix_micros",
            "session_user",
            "current_user",
            "server_version_num",
        ],
    );
    closed(
        &v["execution"],
        &["pid", "boot_id", "start_ticks", "executable_sha256"],
    );
    assert_eq!(
        v["compiled_schema_manifest_hash_domain"],
        "sha256-exact-schema-manifest-stdout-v1"
    );
    assert_eq!(
        v["catalog"]["fingerprint_hash_domain"],
        "sha256-u64be-byte-length-prefixed-utf8-v1"
    );
    assert_eq!(v["catalog"]["matches_installed_receipt"], true);
    assert_eq!(v["runtime_authority"]["status"], "validated");
    assert_eq!(
        v["runtime_authority"]["required_capabilities"],
        json!({"models":true,"events":false,"payments":false,"console":false,"commerce":false,"web_identity":false,"oauth2":false})
    );
    assert_eq!(
        v["not_checked"],
        json!([
            "maintenance_fence",
            "restore_readiness",
            "backup_integrity",
            "physical_system_identifier",
            "external_services",
            "historical_migration"
        ])
    );
    assert!(
        v["database"]["name"] == json!(expected.get::<String, _>("name"))
            && v["database"]["oid"] == json!(expected.get::<String, _>("oid"))
            && v["database"]["server_address"]
                == json!(expected.get::<Option<String>, _>("address"))
            && v["database"]["server_port"] == json!(expected.get::<Option<i32>, _>("port"))
            && v["database"]["postmaster_started_at_unix_micros"]
                == json!(expected.get::<i64, _>("started").to_string())
            && v["database"]["server_version_num"] == json!(expected.get::<i32, _>("version")),
        "database incarnation was not the actual isolated PostgreSQL observation"
    );
    let observed = v["database"]["observed_at_unix_micros"].as_str().unwrap();
    let observed_value = observed.parse::<i64>().unwrap();
    assert!(
        observed_value.to_string() == observed
            && observed_value >= expected.get::<i64, _>("lower")
            && observed_value <= upper,
        "database observation time is not canonical or within the actual query interval"
    );
    assert_eq!(v["version"], 1);
    assert_eq!(v["protocol"], "core.check-db.v1");
    assert_eq!(v["status"], "validated");
    assert_eq!(v["scope"], "runtime_configured_capabilities");
    assert_eq!(
        v["observation"],
        json!({"transaction_read_only":true,"transaction_isolation":"repeatable_read","snapshot":"single_repeatable_read_transaction"})
    );
    assert_eq!(v["database"]["session_user"], role.name);
    assert_eq!(v["database"]["current_user"], role.name);
    assert_eq!(
        v["runtime_authority"]["required_capabilities"]["models"],
        true
    );
    assert_eq!(
        v["components"].as_array().unwrap().len(),
        core_schema::component_count()
    );
    let (_, manifest) = finish(command(&["schema-manifest"]));
    assert!(manifest.status.success());
    assert_eq!(
        v["compiled_schema_manifest_sha256"],
        format!("{:x}", Sha256::digest(&manifest.stdout))
    );
    assert_eq!(
        v["components"],
        serde_json::from_slice::<Value>(&manifest.stdout).unwrap()["components"]
    );
    let saved: Vec<u8> = sqlx::query_scalar(
        "SELECT catalog_fingerprint FROM core_meta.runtime_contract WHERE singleton",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    let saved: String = saved.iter().map(|n| format!("{n:02x}")).collect();
    assert_eq!(v["catalog"]["fingerprint_sha256"], saved);
    assert_eq!(v["catalog"]["status"], "validated");
    assert_eq!(
        v["system_identifier"],
        json!({"status":"not_observed","value":null})
    );
    assert_eq!(v["execution"]["pid"], pid);
    assert_eq!(v["execution"]["executable_sha256"], executable_hash());
    assert_eq!(
        v["execution"]["boot_id"],
        fs::read_to_string("/proc/sys/kernel/random/boot_id")
            .unwrap()
            .trim()
    );
    assert!(
        v["execution"]["start_ticks"]
            .as_str()
            .unwrap()
            .parse::<u64>()
            .unwrap()
            > 0
    );
    assert!(
        before == whole_state(&pool).await,
        "check-db changed application rows/sequences/catalog/owners/ACL/SECDEF"
    );
    assert_eq!(listener.local_addr().unwrap(), address);
    cfg.cleanup();
    first.cleanup();
    role.cleanup().await;
}
#[sqlx::test(migrations = false)]
async fn owner_or_installer_and_insufficient_runtime_permissions_are_rejected_without_writes(
    pool: PgPool,
) {
    core_schema::init(&pool).await.unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    let mut owner = reqwest::Url::parse(&std::env::var("DATABASE_URL").unwrap()).unwrap();
    owner.set_path(pool.connect_options().get_database().unwrap());
    let cfg = PrivateConfig::new(owner.as_str(), "");
    let before = whole_state(&pool).await;
    let (_, o) = cfg.run();
    rejected(&o, 4, "CHECK_DB_RUNTIME_UNSAFE");
    assert!(
        before == whole_state(&pool).await,
        "owner check performed a write"
    );
    cfg.cleanup();
    sqlx::raw_sql(&format!(
        "REVOKE INSERT ON core_identity.accounts FROM {}",
        role.name
    ))
    .execute(&pool)
    .await
    .unwrap();
    let cfg = PrivateConfig::new(&role.url, "");
    let before = whole_state(&pool).await;
    let (_, o) = cfg.run();
    rejected(&o, 4, "CHECK_DB_RUNTIME_INCOMPLETE");
    assert!(
        before == whole_state(&pool).await,
        "rejected privilege check performed a write"
    );
    cfg.cleanup();
    role.cleanup().await;
}
#[sqlx::test(migrations = false)]
async fn missing_schema_and_ambient_legacy_credentials_never_initialize_or_fallback(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    let cfg = PrivateConfig::new(&role.url, "");
    let before = whole_state(&pool).await;
    let mut c = command(&["check-db", "--config", cfg.path.to_str().unwrap()]);
    c.env("LMM_CORE_DATABASE_URL", "do-not-echo-environment-dsn");
    let (_, o) = finish(c);
    rejected(&o, 2, "CHECK_DB_CONFIG_INVALID");
    assert!(
        before == whole_state(&pool).await,
        "ambient rejection changed data"
    );
    sqlx::query("DROP TABLE core_meta.component_contract")
        .execute(&pool)
        .await
        .unwrap();
    let before = whole_state(&pool).await;
    let (_, o) = cfg.run();
    rejected(&o, 3, "CHECK_DB_SCHEMA_UNVERIFIED");
    assert!(
        before == whole_state(&pool).await,
        "missing schema was initialized/repaired or mutated"
    );
    cfg.cleanup();
    role.cleanup().await;
}
