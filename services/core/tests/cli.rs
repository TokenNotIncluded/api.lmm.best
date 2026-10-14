use lmm_core::identity::IdentityStore;
use sqlx::PgPool;
use std::{
    io::Write,
    process::{Child, Command, Output, Stdio},
    time::{Duration, Instant},
};
#[path = "common/runtime.rs"]
mod runtime;

fn core(args: &[&str]) -> Command {
    let mut command = file_core(args);
    command
        .arg("--legacy-env")
        .env("LMM_CORE_WORKER_THREADS", "1");
    command
}

fn file_core(args: &[&str]) -> Command {
    let mut command = Command::new(env!("CARGO_BIN_EXE_lmm-core"));
    command
        .args(args)
        .env_clear()
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    command
}

fn run(args: &[&str], input: &[u8], variables: &[(&str, &str)]) -> Output {
    finish(
        core(args).envs(variables.iter().copied()).spawn().unwrap(),
        input,
    )
}
fn finish(mut child: Child, input: &[u8]) -> Output {
    child.stdin.take().unwrap().write_all(input).unwrap();
    let deadline = Instant::now() + Duration::from_secs(5);
    loop {
        if child.try_wait().unwrap().is_some() {
            return child.wait_with_output().unwrap();
        }
        if Instant::now() >= deadline {
            let _ = child.kill();
            let _ = child.wait();
            panic!("core command did not exit within five seconds");
        }
        std::thread::sleep(Duration::from_millis(10));
    }
}

#[test]
fn help_and_version_ignore_database_and_service_configuration() {
    let poisoned = [
        ("LMM_CORE_DATABASE_URL", "invalid-unused-url"),
        ("LMM_CORE_DATABASE_URL_FILE", "/missing-unused-file"),
        ("LMM_CORE_WORKER_THREADS", "invalid"),
        ("LMM_CORE_LISTEN", "invalid"),
        ("LMM_CORE_RPC_SOCKET", "/missing-unused-socket"),
    ];
    for args in [
        vec!["--help"],
        vec!["-h"],
        vec!["serve", "--help"],
        vec!["init-db", "--help"],
        vec!["bootstrap-user", "--help"],
        vec!["complete-setup", "--help"],
        vec!["--version"],
        vec!["-V"],
    ] {
        let output = run(&args, b"", &poisoned);
        assert!(output.status.success());
        assert!(output.stderr.is_empty());
        assert!(
            String::from_utf8(output.stdout)
                .unwrap()
                .contains("lmm-core")
        );
    }
}

#[test]
fn invalid_arguments_are_rejected_without_echoing_or_reading_configuration() {
    for args in [
        vec!["do-not-log-this-argument"],
        vec!["serve", "do-not-log-this-argument"],
        vec!["init-db", "do-not-log-this-argument"],
        vec!["bootstrap-user", "do-not-log-this-argument"],
        vec!["complete-setup", "do-not-log-this-argument"],
        vec!["--help", "do-not-log-this-argument"],
    ] {
        let output = run(&args, b"", &[("LMM_CORE_WORKER_THREADS", "invalid")]);
        assert!(!output.status.success());
        assert!(output.stdout.is_empty());
        let error = String::from_utf8(output.stderr).unwrap();
        assert!(error.contains("Usage: lmm-core"));
        assert!(!error.contains("do-not-log-this-argument"));
    }
}

#[test]
fn bootstrap_input_is_bounded_strict_and_redacted_before_database_access() {
    for input in [
        br#"{"user_id":1,"platform_level":6,"do-not-log-this-field":"secret"}"#.to_vec(),
        br#"{"user_id":"do-not-log-this-value","platform_level":6}"#.to_vec(),
        br#"{"user_id":1,"user_id":2,"platform_level":6}"#.to_vec(),
        br#"{"user_id":1,"platform_level":6} {}"#.to_vec(),
        b"malformed do-not-log-this-value".to_vec(),
        vec![0xff],
    ] {
        let output = run(
            &["bootstrap-user"],
            &input,
            &[("LMM_CORE_DATABASE_URL_FILE", "/missing-unused-file")],
        );
        assert!(!output.status.success());
        assert!(output.stdout.is_empty());
        let error = String::from_utf8(output.stderr).unwrap();
        assert!(error.contains("invalid bootstrap JSON"));
        assert!(!error.contains("do-not-log-this"));
    }
    let output = run(&["bootstrap-user"], &vec![b' '; 4097], &[]);
    assert!(!output.status.success());
    assert!(output.stdout.is_empty());
    assert!(
        String::from_utf8(output.stderr)
            .unwrap()
            .contains("bootstrap input is too large")
    );
}

#[cfg(unix)]
#[test]
fn toml_cli_requires_one_safe_explicit_source_and_redacts_invalid_values() {
    use std::os::unix::fs::{PermissionsExt, symlink};
    let directory = std::env::temp_dir().join(format!(
        "lmm-config-cli-{}-{}",
        std::process::id(),
        rand::random::<u64>()
    ));
    std::fs::create_dir(&directory).unwrap();
    let path = directory.join("core.toml");
    let invoke = |args: &[&str]| finish(file_core(args).spawn().unwrap(), b"");
    for document in [b"version=1\nversion=2\n".as_slice(),b"version=1\nsecret='do-not-log-this-value'\n",b"version=1\n\xff",b"version=1\n[database]\nurl_file='relative-secret'\n",b"version=1\n[identity]\nregistration_enabled=true\n",b"version=1\n[identity]\napi_key_encryption_key_file='/missing-key'\n",b"version=1\n[identity.google]\nclient_id='id'\nclient_secret_file='/missing-key'\nredirect_uri='https://example.test/core/v1/auth/oauth/google/callback'\n"] {
        std::fs::write(&path,document).unwrap();
        let output = invoke(&["--config",path.to_str().unwrap(),"init-db"]);
        assert!(!output.status.success());
        assert!(!String::from_utf8_lossy(&output.stderr).contains("do-not-log-this"));
    }
    std::fs::write(&path, b"version=1\n").unwrap();
    let conflicted = finish(
        file_core(&["--config", path.to_str().unwrap(), "init-db"])
            .env("LMM_CORE_DATABASE_URL", "do-not-log-this-url")
            .spawn()
            .unwrap(),
        b"",
    );
    assert!(!conflicted.status.success());
    let error = String::from_utf8_lossy(&conflicted.stderr);
    assert!(error.contains("cannot be mixed"));
    assert!(!error.contains("do-not-log-this-url"));
    let missing = directory.join("missing.toml");
    for args in [
        vec!["--config", missing.to_str().unwrap(), "init-db"],
        vec!["--legacy-env", "--config", path.to_str().unwrap()],
        vec!["--config", "relative.toml"],
        vec![
            "--config",
            path.to_str().unwrap(),
            "--config",
            path.to_str().unwrap(),
        ],
    ] {
        assert!(!invoke(&args).status.success());
    }
    assert!(
        invoke(&["--config", missing.to_str().unwrap(), "--help"])
            .status
            .success()
    );
    assert!(
        invoke(&["--config", missing.to_str().unwrap(), "--version"])
            .status
            .success()
    );
    std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o666)).unwrap();
    assert!(
        !invoke(&["init-db", "--config", path.to_str().unwrap()])
            .status
            .success()
    );
    std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o644)).unwrap();
    let linked = directory.join("linked.toml");
    symlink(&path, &linked).unwrap();
    assert!(
        !invoke(&["--config", linked.to_str().unwrap(), "init-db"])
            .status
            .success()
    );
    let secret = directory.join("database-url");
    std::fs::write(&secret, b"do-not-log-this-url").unwrap();
    std::fs::set_permissions(&secret, std::fs::Permissions::from_mode(0o644)).unwrap();
    std::fs::write(
        &path,
        format!("version=1\n[database]\nurl_file='{}'\n", secret.display()),
    )
    .unwrap();
    let output = invoke(&["--config", path.to_str().unwrap(), "init-db"]);
    assert!(!output.status.success());
    assert!(!String::from_utf8_lossy(&output.stderr).contains("do-not-log-this-url"));
    std::fs::remove_dir_all(directory).unwrap();
}

#[cfg(unix)]
#[tokio::test]
async fn toml_only_no_database_process_is_live_but_never_ready() {
    let path = std::env::temp_dir().join(format!(
        "lmm-no-db-{}-{}.toml",
        std::process::id(),
        rand::random::<u64>()
    ));
    let reserved = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let address = reserved.local_addr().unwrap();
    drop(reserved);
    std::fs::write(
        &path,
        format!("version=1\n[server]\nlisten='{address}'\ndrain_timeout_seconds=1\n"),
    )
    .unwrap();
    let mut service = Service(
        file_core(&["--config", path.to_str().unwrap()])
            .spawn()
            .unwrap(),
    );
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(1))
        .build()
        .unwrap();
    let deadline = Instant::now() + Duration::from_secs(5);
    loop {
        assert!(service.0.try_wait().unwrap().is_none());
        if let Ok(response) = client
            .get(format!("http://{address}/health/live"))
            .send()
            .await
        {
            assert_eq!(response.status(), 200);
            break;
        }
        assert!(Instant::now() < deadline);
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert_eq!(
        client
            .get(format!("http://{address}/health/ready"))
            .send()
            .await
            .unwrap()
            .status(),
        503
    );
    drop(service);
    std::fs::remove_file(path).unwrap();
}

fn database_url(pool: &PgPool) -> String {
    let mut url = reqwest::Url::parse(&std::env::var("DATABASE_URL").unwrap()).unwrap();
    assert!(matches!(url.host_str(), Some("localhost" | "127.0.0.1")));
    url.set_path(pool.connect_options().get_database().unwrap());
    url.to_string()
}

#[sqlx::test(migrations = false)]
async fn management_commands_initialize_and_emit_one_bootstrap_credential(pool: PgPool) {
    let url = database_url(&pool);
    let variables = [
        ("LMM_CORE_DATABASE_URL", url.as_str()),
        ("LMM_CORE_LISTEN", "invalid"),
    ];
    let initialized = run(&["init-db"], b"", &variables);
    assert!(initialized.status.success());
    assert!(initialized.stderr.is_empty());
    let store = IdentityStore::from_pool(pool.clone());
    store.check_schema().await.unwrap();
    lmm_core::core_schema::check(&pool).await.unwrap();
    let components: i64 = sqlx::query_scalar("SELECT count(*) FROM core_meta.component_contract")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(
        components as usize,
        lmm_core::core_schema::component_count()
    );
    assert!(
        !run(&["complete-setup"], b"", &variables).status.success(),
        "an empty installation has no root and cannot be completed"
    );

    let mut input = br#"{"user_id":9001,"platform_level":6}"#.to_vec();
    input.resize(4096, b' ');
    let output = run(&["bootstrap-user"], &input, &variables);
    assert!(output.status.success());
    assert!(output.stderr.is_empty());
    let issued: serde_json::Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(issued.as_object().unwrap().len(), 2);
    let principal = store
        .authorize(issued["secret"].as_str().unwrap())
        .await
        .unwrap();
    assert_eq!(principal.user_id, 9001);
    assert_eq!(principal.platform_level, 6);
    assert!(run(&["complete-setup"], b"", &variables).status.success());
    let initialized_at: String =
        sqlx::query_scalar("SELECT initialized_at::text FROM core_console.initialization")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(run(&["complete-setup"], b"", &variables).status.success());
    let repeated_at: String =
        sqlx::query_scalar("SELECT initialized_at::text FROM core_console.initialization")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(
        initialized_at, repeated_at,
        "an idempotent offline command must preserve the original setup timestamp"
    );

    for args in [vec!["init-db"], vec!["bootstrap-user"]] {
        let output = run(&args, &input, &variables);
        assert!(!output.status.success());
        assert!(output.stdout.is_empty());
    }
    let users: i64 = sqlx::query_scalar("SELECT count(*) FROM core_identity.users")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(users, 1);
    store.check_schema().await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn ordinary_startup_never_initializes_and_init_preserves_an_occupied_database(pool: PgPool) {
    let url = database_url(&pool);
    let variables = [
        ("LMM_CORE_DATABASE_URL", url.as_str()),
        ("LMM_CORE_LISTEN", "127.0.0.1:0"),
    ];
    for args in [vec![], vec!["serve"]] {
        let output = run(&args, b"", &variables);
        assert!(!output.status.success());
    }
    let exists: bool =
        sqlx::query_scalar("SELECT EXISTS(SELECT FROM pg_namespace WHERE nspname='core_meta')")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(!exists);
    sqlx::raw_sql("CREATE TABLE sentinel(value TEXT); INSERT INTO sentinel VALUES ('keep')")
        .execute(&pool)
        .await
        .unwrap();
    assert!(!run(&["init-db"], b"", &variables).status.success());
    let value: String = sqlx::query_scalar("SELECT value FROM sentinel")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(value, "keep");
}

struct Service(Child);

impl Drop for Service {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}

#[cfg(unix)]
#[sqlx::test(migrations = false)]
async fn financial_runtime_configuration_uses_independent_private_credentials(pool: PgPool) {
    use std::os::unix::fs::PermissionsExt;
    lmm_core::core_schema::init(&pool).await.unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    for schema in [
        "core_charging",
        "core_events",
        "core_payments",
        "core_commerce",
    ] {
        role.optional(schema).await;
    }
    let directory = std::env::temp_dir().join(format!(
        "lmm-financial-process-{}-{:016x}",
        std::process::id(),
        rand::random::<u64>()
    ));
    std::fs::create_dir(&directory).unwrap();
    std::fs::set_permissions(&directory, std::fs::Permissions::from_mode(0o700)).unwrap();
    let private = |name: &str, bytes: &[u8]| {
        let path = directory.join(name);
        std::fs::write(&path, bytes).unwrap();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600)).unwrap();
        path
    };
    let control = private("control", b"identity-transport-01234567890123456789");
    let payment = private("payment", b"payment-principal-01234567890123456789");
    let commerce = private("commerce", b"commerce-principal-01234567890123456789");
    let verification = private("verification", b"whsec_synthetic_runtime_owned_0123456789");
    let payment_document = private("payments.toml", format!("version=1\nprovider='stripe-test'\n[[channels]]\nid=1\nmerchant='acct_runtime_synthetic'\nwebhook_secret_file='{}'\nmax_usd_cents=1000\n[permissions]\nproof_read=true\n", verification.display()).as_bytes());
    let commerce_document = private("commerce.toml", b"version=1\nread=true\n");
    let socket = directory.join("core.sock");
    let reserved = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let address = reserved.local_addr().unwrap();
    drop(reserved);
    let database = private("database-url", role.url.as_bytes());
    let configured = |payment_token: &std::path::Path| {
        let document = format!(
            "version=1\n[server]\nlisten='{address}'\n[database]\nurl_file='{}'\n[rpc]\nsocket='{}'\ntoken_file='{}'\n[payments]\nconfig_file='{}'\ntoken_file='{}'\n[commerce]\nconfig_file='{}'\ntoken_file='{}'\n",
            database.display(),
            socket.display(),
            control.display(),
            payment_document.display(),
            payment_token.display(),
            commerce_document.display(),
            commerce.display()
        );
        let path = private("core.toml", document.as_bytes());
        file_core(&["serve", "--config", path.to_str().unwrap()])
    };
    let mut alias = configured(&control).spawn().unwrap();
    let deadline = Instant::now() + Duration::from_secs(5);
    while alias.try_wait().unwrap().is_none() {
        if Instant::now() > deadline {
            let _ = alias.kill();
            panic!("aliased financial principal was accepted");
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    let alias = alias.wait_with_output().unwrap();
    assert!(!alias.status.success());
    assert!(
        String::from_utf8(alias.stderr)
            .unwrap()
            .contains("must be distinct")
    );
    let mut service = Service(configured(&payment).spawn().unwrap());
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(1))
        .build()
        .unwrap();
    let deadline = Instant::now() + Duration::from_secs(5);
    let health = loop {
        assert!(
            service.0.try_wait().unwrap().is_none(),
            "configured synthetic financial service exited"
        );
        if let Ok(response) = client
            .get(format!("http://{address}/health/ready"))
            .send()
            .await
        {
            assert_eq!(response.status(), 200);
            break response.json::<serde_json::Value>().await.unwrap();
        }
        assert!(Instant::now() < deadline);
        tokio::time::sleep(Duration::from_millis(20)).await;
    };
    assert_eq!(health["capabilities"]["payments"], true);
    assert_eq!(health["capabilities"]["commerce"], true);
    assert_eq!(health["capabilities"]["models"], false);
    let public: serde_json::Value = client
        .get(format!("http://{address}/api/status"))
        .send()
        .await
        .unwrap()
        .json()
        .await
        .unwrap();
    assert_eq!(
        public["data"]["payments_enabled"], false,
        "core test RPC does not imply a working Web checkout"
    );
    assert_eq!(public["data"]["payments_test_rpc_enabled"], true);
    drop(service);
    std::fs::remove_dir_all(directory).unwrap();
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn default_and_explicit_serve_keep_health_behavior_and_reject_contract_changes(pool: PgPool) {
    lmm_core::core_schema::init(&pool).await.unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    lmm_core::core_schema::check(&role.pool).await.unwrap();
    let url = role.url.clone();
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(1))
        .build()
        .unwrap();
    for args in [vec![], vec!["serve"]] {
        let socket = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let address = socket.local_addr().unwrap();
        drop(socket);
        let mut service = Service(
            core(&args)
                .env("LMM_CORE_DATABASE_URL", &url)
                .env("LMM_CORE_LISTEN", address.to_string())
                .spawn()
                .unwrap(),
        );
        let endpoint = format!("http://{address}");
        let deadline = Instant::now() + Duration::from_secs(5);
        loop {
            assert!(service.0.try_wait().unwrap().is_none());
            if let Ok(response) = client.get(format!("{endpoint}/health/live")).send().await {
                assert_eq!(response.status(), 200);
                break;
            }
            assert!(
                Instant::now() < deadline,
                "core service did not become live"
            );
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
        let response = client
            .get(format!("{endpoint}/health/ready"))
            .send()
            .await
            .unwrap();
        assert_eq!(response.status(), 200);
        let health: serde_json::Value = response.json().await.unwrap();
        assert_eq!(health["capabilities"]["identity"], true);
        assert_eq!(health["capabilities"]["models"], false);
        assert_eq!(health["capabilities"]["payments"], false);
        for path in ["/health/model-ready", "/v1/models"] {
            assert_eq!(
                client
                    .get(format!("{endpoint}{path}"))
                    .send()
                    .await
                    .unwrap()
                    .status(),
                503
            );
        }
    }
    let incompatible_version: i32 = sqlx::query_scalar(
        "UPDATE core_meta.schema_contract SET version=version+1 RETURNING version",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    let output = run(&["serve"], b"", &[("LMM_CORE_DATABASE_URL", &url)]);
    assert!(!output.status.success());
    let version: i32 = sqlx::query_scalar("SELECT version FROM core_meta.schema_contract")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(version, incompatible_version);
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn ordinary_startup_rejects_an_identity_only_installation(pool: PgPool) {
    IdentityStore::from_pool(pool.clone())
        .init_database()
        .await
        .unwrap();
    let url = database_url(&pool);
    assert!(
        !run(
            &["serve"],
            b"",
            &[
                ("LMM_CORE_DATABASE_URL", &url),
                ("LMM_CORE_LISTEN", "127.0.0.1:0")
            ]
        )
        .status
        .success()
    );
    let components: bool =
        sqlx::query_scalar("SELECT to_regclass('core_meta.component_contract') IS NOT NULL")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(
        !components,
        "startup must not upgrade an identity-only database"
    );
}

#[cfg(unix)]
#[sqlx::test(migrations = false)]
async fn assembled_service_exposes_web_auth_and_explicit_capabilities_without_go(pool: PgPool) {
    use std::os::unix::fs::PermissionsExt;
    lmm_core::core_schema::init(&pool).await.unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    role.optional("core_events").await;
    role.optional("web_identity").await;
    role.optional("core_console").await;
    lmm_core::core_schema::check(&role.pool).await.unwrap();
    let url = role.url.clone();
    let directory = std::env::temp_dir().join(format!(
        "lmm-runtime-process-{}-{}",
        std::process::id(),
        rand::random::<u64>()
    ));
    std::fs::create_dir(&directory).unwrap();
    std::fs::set_permissions(&directory, std::fs::Permissions::from_mode(0o700)).unwrap();
    let key = directory.join("web-key");
    std::fs::write(&key, [42_u8; 32]).unwrap();
    std::fs::set_permissions(&key, std::fs::Permissions::from_mode(0o600)).unwrap();
    let token = directory.join("rpc-key");
    std::fs::write(&token, b"0123456789abcdef0123456789abcdef").unwrap();
    std::fs::set_permissions(&token, std::fs::Permissions::from_mode(0o600)).unwrap();
    let socket_path = directory.join("core.sock");
    let socket = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let address = socket.local_addr().unwrap();
    drop(socket);
    let database = directory.join("database-url");
    std::fs::write(&database, url.as_bytes()).unwrap();
    std::fs::set_permissions(&database, std::fs::Permissions::from_mode(0o600)).unwrap();
    let configuration = directory.join("core.toml");
    std::fs::write(&configuration, format!("version=1\n[server]\nlisten='{address}'\n[database]\nurl_file='{}'\n[identity]\nregistration_enabled=true\n[identity.web]\norigin='http://api.lmm.local'\nrefresh_key_file='{}'\nlocal_http=true\nuser_agreement_enabled=false\nprivacy_policy_enabled=false\n[console]\nenabled=true\n[rpc]\nsocket='{}'\ntoken_file='{}'\n",database.display(),key.display(),socket_path.display(),token.display())).unwrap();
    let mut service = Service(
        file_core(&["--config", configuration.to_str().unwrap(), "serve"])
            .spawn()
            .unwrap(),
    );
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(2))
        .build()
        .unwrap();
    let endpoint = format!("http://{address}");
    let deadline = Instant::now() + Duration::from_secs(5);
    let health = loop {
        assert!(service.0.try_wait().unwrap().is_none());
        if let Ok(response) = client.get(format!("{endpoint}/health/ready")).send().await {
            assert_eq!(response.status(), 200);
            break response.json::<serde_json::Value>().await.unwrap();
        }
        assert!(Instant::now() < deadline, "assembled service did not start");
        tokio::time::sleep(Duration::from_millis(20)).await;
    };
    assert_eq!(health["capabilities"]["identity"], true);
    assert_eq!(health["capabilities"]["legacy_web_auth"], true);
    assert_eq!(health["capabilities"]["console"], true);
    assert_eq!(health["capabilities"]["events"], true);
    assert_eq!(health["capabilities"]["models"], false);
    assert_eq!(health["capabilities"]["payments"], false);
    assert!(socket_path.exists());
    let public: serde_json::Value = client
        .get(format!("{endpoint}/api/status"))
        .send()
        .await
        .unwrap()
        .json()
        .await
        .unwrap();
    assert_eq!(public["success"], true);
    assert_eq!(public["data"]["system_name"], "LMM API");
    assert_eq!(public["data"]["password_login_enabled"], true);
    assert_eq!(public["data"]["register_enabled"], true);
    assert_eq!(public["data"]["user_agreement_enabled"], false);
    assert_eq!(public["data"]["privacy_policy_enabled"], false);
    assert_eq!(
        public["data"]["backend_capabilities"]["responses_websocket"],
        false
    );
    assert_eq!(public["data"]["payments_enabled"], false);
    assert!(public["data"].get("quota").is_none());
    let setup: serde_json::Value = client
        .get(format!("{endpoint}/api/setup"))
        .send()
        .await
        .unwrap()
        .json()
        .await
        .unwrap();
    assert_eq!(setup["success"], true);
    assert_eq!(setup["data"]["status"], false);
    assert_eq!(
        client
            .post(format!("{endpoint}/api/setup"))
            .body("untrusted setup data")
            .send()
            .await
            .unwrap()
            .status(),
        403
    );
    let registered = client.post(format!("{endpoint}/api/user/register"))
        .header("origin","http://api.lmm.local")
        .json(&serde_json::json!({"username":"runtime_user","password":"abc12345","accepted_legal":false}))
        .send().await.unwrap();
    assert_eq!(registered.status(), 200);
    let registered: serde_json::Value = registered.json().await.unwrap();
    assert_eq!(registered["success"], true);
    let login = client
        .post(format!("{endpoint}/api/user/login"))
        .header("origin", "http://api.lmm.local")
        .json(&serde_json::json!({"username":"runtime_user","password":"abc12345"}))
        .send()
        .await
        .unwrap();
    assert_eq!(login.status(), 200);
    assert!(
        login.headers()["set-cookie"]
            .to_str()
            .unwrap()
            .contains("HttpOnly")
    );
    let bundle: serde_json::Value = login.json().await.unwrap();
    let access = bundle["data"]["access_token"].as_str().unwrap();
    let current = client
        .get(format!("{endpoint}/api/user/self"))
        .bearer_auth(access)
        .send()
        .await
        .unwrap();
    assert_eq!(current.status(), 200);
    let current: serde_json::Value = current.json().await.unwrap();
    assert_eq!(current["data"]["username"], "runtime_user");
    let announcements: serde_json::Value = client
        .get(format!("{endpoint}/api/user/self/announcements"))
        .bearer_auth(access)
        .send()
        .await
        .unwrap()
        .json()
        .await
        .unwrap();
    assert_eq!(announcements["success"], true);
    let initialization_rows: i64 =
        sqlx::query_scalar("SELECT count(*) FROM core_console.initialization")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(
        initialization_rows, 0,
        "runtime startup and HTTP setup must not record offline initialization"
    );
    assert_eq!(current["data"]["financial_projection_available"], true);
    assert_eq!(
        current["data"]["quota"], 0,
        "new accounts have their actual zero ledger balance"
    );
    assert_eq!(
        client
            .get(format!("{endpoint}/health/model-ready"))
            .send()
            .await
            .unwrap()
            .status(),
        503
    );
    let native_registration = client
        .post(format!("{endpoint}/core/v1/auth/register"))
        .json(
            &serde_json::json!({"login_name":"consent_bypass","password":"valid native password"}),
        )
        .send()
        .await
        .unwrap();
    assert_eq!(
        native_registration.status(),
        403,
        "Web consent must not have a native registration bypass"
    );

    // A stalled request body must not pin graceful shutdown. This deadline is
    // confined to body intake and cannot abort a financial finalizer.
    let mut slow_client = std::net::TcpStream::connect(address).unwrap();
    write!(slow_client,"POST /api/user/login HTTP/1.1\r\nHost: api.lmm.local\r\nOrigin: http://api.lmm.local\r\nContent-Type: application/json\r\nContent-Length: 512\r\n\r\n{{\"username\":\"").unwrap();
    slow_client.flush().unwrap();
    tokio::time::sleep(Duration::from_millis(100)).await;
    let started = Instant::now();
    let status = Command::new("/bin/kill")
        .args(["-TERM", &service.0.id().to_string()])
        .status()
        .unwrap();
    assert!(status.success());
    loop {
        if let Some(status) = service.0.try_wait().unwrap() {
            assert!(status.success(), "graceful shutdown failed");
            break;
        }
        assert!(
            started.elapsed() < Duration::from_secs(14),
            "partial body pinned shutdown"
        );
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    assert!(
        !socket_path.exists(),
        "owned RPC socket must be removed after shutdown"
    );
    drop(slow_client);
    drop(service);
    std::fs::remove_dir_all(directory).unwrap();
    role.cleanup().await;
}

#[test]
fn usage_index_stdin_is_bounded_strict_redacted_and_precedes_configuration() {
    for input in [
        vec![],
        vec![b'a'; 63],
        vec![b'a'; 67],
        b"secret-not-a-hash".to_vec(),
        [vec![b'a'; 64], b"\n\n".to_vec()].concat(),
        [vec![b'a'; 64], b" trailing-secret".to_vec()].concat(),
        vec![b'g'; 64],
        vec![0xff; 64],
    ] {
        let output = finish(
            file_core(&[
                "--config",
                "/missing-unused-index-config",
                "install-usage-receipt-index",
            ])
            .spawn()
            .unwrap(),
            &input,
        );
        assert!(!output.status.success());
        assert!(output.stdout.is_empty());
        let error = String::from_utf8_lossy(&output.stderr);
        assert!(error.contains("invalid prior catalog SHA256"), "{error}");
        assert!(!error.contains("secret"));
        assert!(!error.contains("missing-unused-index-config"));
    }
    for ending in [b"".as_slice(), b"\n", b"\r\n"] {
        let input = [vec![b'A'; 64], ending.to_vec()].concat();
        let output = run(
            &["install-usage-receipt-index"],
            &input,
            &[("LMM_CORE_DATABASE_URL", "invalid-unused-url")],
        );
        assert!(!output.status.success());
        let error = String::from_utf8_lossy(&output.stderr);
        assert!(error.contains("requires explicit installer-owner --config"));
        assert!(!error.contains("invalid prior catalog SHA256"));
        assert!(!error.contains("invalid-unused-url"));
    }
}

#[cfg(unix)]
#[sqlx::test(migrations = false)]
async fn usage_index_owner_cli_upgrades_only_fresh_prior_fixture_and_rejects_runtime(pool: PgPool) {
    use std::os::unix::fs::PermissionsExt;
    async fn snapshot(pool: &PgPool) -> Vec<String> {
        const CATALOG: &str = r#"
WITH catalog_path AS MATERIALIZED (
 SELECT pg_catalog.set_config('search_path','pg_catalog',true)
), ns AS MATERIALIZED (
 SELECT n.oid,n.nspname FROM pg_namespace n CROSS JOIN catalog_path
 WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'
), definitions AS (
 SELECT json_build_array('relation',n.nspname,c.relname,c.relkind)::text AS definition
 FROM pg_class c JOIN ns n ON n.oid=c.relnamespace
 UNION ALL
 SELECT json_build_array('column',n.nspname,c.relname,a.attnum,a.attname,
   format_type(a.atttypid,a.atttypmod),a.attnotnull,a.attidentity,a.attgenerated,
   pg_get_expr(d.adbin,d.adrelid))::text
 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN ns n ON n.oid=c.relnamespace
 LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
 WHERE a.attnum>0 AND NOT a.attisdropped
 UNION ALL
 SELECT json_build_array('constraint',n.nspname,c.conname,pg_get_constraintdef(c.oid,true))::text
 FROM pg_constraint c JOIN ns n ON n.oid=c.connamespace
 UNION ALL
 SELECT json_build_array('index',n.nspname,c.relname,pg_get_indexdef(i.indexrelid))::text
 FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN ns n ON n.oid=c.relnamespace
 UNION ALL
 SELECT json_build_array('function',n.nspname,p.proname,pg_get_functiondef(p.oid))::text
 FROM pg_proc p JOIN ns n ON n.oid=p.pronamespace WHERE p.prokind IN ('f','p')
 UNION ALL
 SELECT json_build_array('trigger',n.nspname,c.relname,t.tgname,t.tgenabled,
   pg_get_triggerdef(t.oid,true))::text
 FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN ns n ON n.oid=c.relnamespace
 WHERE NOT t.tgisinternal
 UNION ALL
 -- RI trigger names embed installation OIDs, which change on pg_dump/restore.
 -- Hash their stable constraint/function/trigger properties instead.
 SELECT json_build_array('internal-trigger',n.nspname,c.relname,k.conname,
   pn.nspname,p.proname,t.tgtype,t.tgenabled,t.tgdeferrable,t.tginitdeferred,
   t.tgattr::text,encode(t.tgargs,'hex'),pg_get_expr(t.tgqual,t.tgrelid))::text
 FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN ns n ON n.oid=c.relnamespace
 JOIN pg_proc p ON p.oid=t.tgfoid JOIN pg_namespace pn ON pn.oid=p.pronamespace
 LEFT JOIN pg_constraint k ON k.oid=t.tgconstraint WHERE t.tgisinternal
) SELECT definition FROM definitions ORDER BY definition COLLATE "C"
"#;
        let mut observed: Vec<String> = sqlx::query_scalar(r#"
      SELECT value FROM (
        SELECT json_build_array('component',name,encode(fingerprint,'hex'))::text AS value FROM core_meta.component_contract
        UNION ALL SELECT json_build_array('runtime',singleton,encode(catalog_fingerprint,'hex'))::text FROM core_meta.runtime_contract
        UNION ALL SELECT json_build_array('identity',singleton,version,encode(fingerprint,'hex'))::text FROM core_meta.schema_contract
        UNION ALL SELECT json_build_array('journal',to_jsonb(j))::text FROM core_billing.ledger_journals j
        UNION ALL SELECT json_build_array('entry',to_jsonb(e))::text FROM core_billing.ledger_entries e
        UNION ALL SELECT json_build_array('wallet',to_jsonb(w))::text FROM core_billing.ledger_accounts w
        UNION ALL SELECT json_build_array('balance',to_jsonb(b))::text FROM core_billing.balance_state b
        UNION ALL SELECT json_build_array('operation',to_jsonb(o))::text FROM core_billing.ledger_operations o
        UNION ALL SELECT json_build_array('request',to_jsonb(r))::text FROM core_charging.requests r
        UNION ALL SELECT json_build_array('command',to_jsonb(c))::text FROM core_charging.ledger_commands c
        UNION ALL SELECT json_build_array('schema-acl',n.nspname,n.nspowner,n.nspacl)::text
          FROM pg_namespace n WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'
        UNION ALL SELECT json_build_array('relation-acl',n.nspname,c.relname,c.relowner,c.relacl)::text
          FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
          WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'
        UNION ALL SELECT json_build_array('column-acl',n.nspname,c.relname,a.attnum,a.attname,a.attacl)::text
          FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace
          WHERE a.attnum>0 AND NOT a.attisdropped AND n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'
        UNION ALL SELECT json_build_array('function-acl',n.nspname,p.proname,pg_get_function_identity_arguments(p.oid),p.proowner,p.proacl)::text
          FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
          WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'
        UNION ALL SELECT json_build_array('sequence',to_jsonb(s))::text FROM pg_sequences s
          WHERE s.schemaname NOT IN ('pg_catalog','information_schema') AND left(s.schemaname,3)<>'pg_'
        UNION ALL SELECT json_build_array('index',n.nspname,c.relname,pg_get_indexdef(c.oid))::text
          FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
          WHERE c.relkind='i' AND n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'
      ) rows ORDER BY value COLLATE "C"
    "#).fetch_all(pool).await.unwrap();
        let definitions: Vec<String> = sqlx::query_scalar(CATALOG).fetch_all(pool).await.unwrap();
        observed.extend(
            definitions
                .into_iter()
                .map(|definition| format!("[\"catalog\",{definition}]")),
        );
        observed.sort();
        observed
    }

    fn preserved(rows: Vec<String>) -> Vec<String> {
        rows.into_iter()
            .filter(|row| {
                let value: serde_json::Value =
                    serde_json::from_str(row).expect("invalid index-fixture observation");
                !(value[0] == "component" && value[1] == "charging-ledger")
                    && value[0] != "runtime"
                    && !(value[0] == "index"
                        && value[1] == "core_charging"
                        && value[2] == "ledger_commands_request_operation")
                    && !(matches!(value[0].as_str(), Some("relation-acl" | "column-acl"))
                        && value[1] == "core_charging"
                        && value[2] == "ledger_commands_request_operation")
                    && !(value[0] == "catalog"
                        && value[1][1] == "core_charging"
                        && value[1][2] == "ledger_commands_request_operation"
                        && matches!(value[1][0].as_str(), Some("relation" | "column" | "index")))
            })
            .collect()
    }
    lmm_core::core_schema::init(&pool).await.unwrap();
    // Fresh current-v4 twelve-component schema with only this index removed.
    // core_schema::index_tests independently constructs and pins this exact catalog.
    const PRIOR: &str = "9ce6c5120645792608d4466ef9fe4b6f91add7ec082670d276bffbd94f48c14a";
    const RETIRED_PRIOR: &str = "e8fa053daca7741952caa4c60e27071652f600b4fa48f4b7631ea54ee801df7b";
    const RETIRED_ELEVEN_COMPONENT_PRIOR: &str =
        "5510e0bee0e5659ff940b798c9d6d3c69aea220734d9f73577c847b6841254b7";
    sqlx::raw_sql("DROP INDEX core_charging.ledger_commands_request_operation; UPDATE core_meta.component_contract SET fingerprint=decode('7f84659ad2bd60268256a9f76959299a33911aaaeaf7194156a6f195ab54a734','hex') WHERE name='charging-ledger'; UPDATE core_meta.runtime_contract SET catalog_fingerprint=decode('9ce6c5120645792608d4466ef9fe4b6f91add7ec082670d276bffbd94f48c14a','hex')")
        .execute(&pool).await.unwrap();
    // Nonempty synthetic ledger data makes refusal and upgrade conservation real.
    let store = IdentityStore::from_pool(pool.clone());
    let issued = store.bootstrap_user(97, 0).await.unwrap();
    let owner = store
        .authorize(&issued.secret)
        .await
        .unwrap()
        .owner_account_id;
    let ledger = lmm_core::billing::ledger::Ledger::from_pool(pool.clone());
    ledger.open_wallet(owner).await.unwrap();
    ledger
        .execute(&lmm_core::billing::ledger::Request {
            scope: "index-cli-fixture".into(),
            operation_key: "credit".into(),
            actor_user_id: Some(97),
            reason: "Synthetic current-v4 index CLI fixture".into(),
            action: lmm_core::billing::ledger::Action::Credit {
                account_id: owner,
                amount_units: lmm_core::billing::Credits::try_from(1000).unwrap(),
                payment_reference: "index-cli-fixture-credit".into(),
            },
        })
        .await
        .unwrap();
    let directory = std::env::temp_dir().join(format!(
        "lmm-index-owner-cli-{}-{:x}",
        std::process::id(),
        rand::random::<u64>()
    ));
    std::fs::create_dir(&directory).unwrap();
    std::fs::set_permissions(&directory, std::fs::Permissions::from_mode(0o700)).unwrap();
    let configuration = directory.join("owner.toml");
    let database = directory.join("database-url");
    std::fs::write(
        &configuration,
        format!("version=1\n[database]\nurl_file='{}'\n", database.display()),
    )
    .unwrap();
    std::fs::set_permissions(&configuration, std::fs::Permissions::from_mode(0o600)).unwrap();
    let invoke = |input: &[u8]| {
        finish(
            file_core(&[
                "--config",
                configuration.to_str().unwrap(),
                "install-usage-receipt-index",
            ])
            .spawn()
            .unwrap(),
            input,
        )
    };
    let role = runtime::RuntimeRole::create(&pool).await;
    role.optional("core_charging").await;
    std::fs::write(&database, role.url.as_bytes()).unwrap();
    std::fs::set_permissions(&database, std::fs::Permissions::from_mode(0o600)).unwrap();
    let before_runtime = snapshot(&pool).await;
    let output = invoke(PRIOR.as_bytes());
    assert!(!output.status.success());
    assert!(output.stdout.is_empty());
    assert!(!String::from_utf8_lossy(&output.stderr).contains(&role.url));
    let absent: bool = sqlx::query_scalar(
        "SELECT to_regclass('core_charging.ledger_commands_request_operation') IS NULL",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(absent);
    assert!(
        snapshot(&pool).await == before_runtime,
        "runtime refusal changed database observations"
    );
    let owner_url = database_url(&pool);
    std::fs::write(&database, &owner_url).unwrap();
    // Both retired catalogs must fail for saved=current/approval=retired,
    // saved=retired/approval=retired, and saved=retired/approval=current.
    // No mismatch may change financial rows, receipts, ACLs or definitions.
    for (saved, approval) in [
        (PRIOR, RETIRED_PRIOR),
        (RETIRED_PRIOR, RETIRED_PRIOR),
        (RETIRED_PRIOR, PRIOR),
        (PRIOR, RETIRED_ELEVEN_COMPONENT_PRIOR),
        (
            RETIRED_ELEVEN_COMPONENT_PRIOR,
            RETIRED_ELEVEN_COMPONENT_PRIOR,
        ),
        (RETIRED_ELEVEN_COMPONENT_PRIOR, PRIOR),
    ] {
        sqlx::query("UPDATE core_meta.runtime_contract SET catalog_fingerprint=decode($1,'hex') WHERE singleton")
            .bind(saved).execute(&pool).await.unwrap();
        let before_refusal = snapshot(&pool).await;
        let output = invoke(approval.as_bytes());
        assert!(
            !output.status.success(),
            "retired prior catalog approval was accepted"
        );
        assert!(output.stdout.is_empty());
        assert!(
            String::from_utf8_lossy(&output.stderr).contains("Storage"),
            "retired prior was not rejected by the catalog guard"
        );
        assert!(!String::from_utf8_lossy(&output.stderr).contains(&role.url));
        assert!(!String::from_utf8_lossy(&output.stderr).contains(&owner_url));
        assert!(
            snapshot(&pool).await == before_refusal,
            "retired-prior refusal changed receipts, financial rows, ACLs or indexes"
        );
    }
    sqlx::query("UPDATE core_meta.runtime_contract SET catalog_fingerprint=decode($1,'hex') WHERE singleton")
        .bind(PRIOR).execute(&pool).await.unwrap();
    let before_upgrade = snapshot(&pool).await;
    let output = invoke(&[PRIOR.as_bytes(), b"\n"].concat());
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(output.stderr.is_empty());
    assert!(
        String::from_utf8_lossy(&output.stdout).contains("schema receipts verified atomically")
    );
    lmm_core::core_schema::check(&pool).await.unwrap();
    let same_owner: bool = sqlx::query_scalar("SELECT i.relowner=t.relowner FROM pg_class i CROSS JOIN pg_class t WHERE i.oid='core_charging.ledger_commands_request_operation'::regclass AND t.oid='core_charging.ledger_commands'::regclass")
        .fetch_one(&pool).await.unwrap();
    assert!(
        same_owner,
        "new usage index must retain the ledger-command owner"
    );
    assert!(
        preserved(snapshot(&pool).await) == preserved(before_upgrade),
        "index upgrade changed identity receipts, financial rows or ACLs"
    );
    let after_upgrade = snapshot(&pool).await;
    let output = invoke(PRIOR.as_bytes());
    assert!(!output.status.success());
    assert!(output.stdout.is_empty());
    assert!(
        snapshot(&pool).await == after_upgrade,
        "existing-index refusal changed database observations"
    );
    role.cleanup().await;
    std::fs::remove_dir_all(directory).unwrap();
}
