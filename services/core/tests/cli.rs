use lmm_core::identity::IdentityStore;
use sqlx::PgPool;
use std::{
    io::Write,
    process::{Child, Command, Output, Stdio},
    time::{Duration, Instant},
};

fn core(args: &[&str]) -> Command {
    let mut command = Command::new(env!("CARGO_BIN_EXE_lmm-core"));
    command
        .args(args)
        .env_clear()
        .env("LMM_CORE_WORKER_THREADS", "1")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    command
}

fn run(args: &[&str], input: &[u8], variables: &[(&str, &str)]) -> Output {
    let mut child = core(args).envs(variables.iter().copied()).spawn().unwrap();
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

#[sqlx::test(migrations = false)]
async fn default_and_explicit_serve_keep_health_behavior_and_reject_contract_changes(pool: PgPool) {
    IdentityStore::from_pool(pool.clone())
        .init_database()
        .await
        .unwrap();
    let url = database_url(&pool);
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
        assert_eq!(
            client
                .get(format!("{endpoint}/health/ready"))
                .send()
                .await
                .unwrap()
                .status(),
            503
        );
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
}
