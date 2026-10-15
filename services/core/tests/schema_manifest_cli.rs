//! Actual binary tests; no PostgreSQL fixture or runtime configuration is used.
use std::{
    process::{Command, Output, Stdio},
    time::{Duration, Instant},
};

fn invoke(args: &[&str], variables: &[(&str, &str)]) -> Output {
    let mut child = Command::new(env!("CARGO_BIN_EXE_lmm-core"))
        .args(args)
        .env_clear()
        .envs(variables.iter().copied())
        // Deliberately leave stdin open: this command must not read input.
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let deadline = Instant::now() + Duration::from_secs(5);
    loop {
        if child.try_wait().unwrap().is_some() {
            return child.wait_with_output().unwrap();
        }
        if Instant::now() >= deadline {
            let _ = child.kill();
            let _ = child.wait();
            panic!("schema manifest command did not exit with stdin still open");
        }
        std::thread::sleep(Duration::from_millis(10));
    }
}

#[test]
fn schema_manifest_without_configuration_is_exact_machine_json() {
    let output = invoke(&["schema-manifest"], &[]);
    assert!(output.status.success());
    assert!(output.stderr.is_empty());
    let actual: serde_json::Value = serde_json::from_slice(&output.stdout).unwrap();
    let expected = serde_json::to_value(lmm_core::core_schema::schema_manifest()).unwrap();
    assert_eq!(actual, expected);
    assert_eq!(actual["format_version"], 1);
    assert_eq!(actual["scope"], "compiled-core-schema-contract");
    assert_eq!(
        actual["components"].as_array().unwrap().len(),
        lmm_core::core_schema::component_count()
    );
    for component in actual["components"].as_array().unwrap() {
        let fingerprint = component["raw_schema_sha256"].as_str().unwrap();
        assert_eq!(fingerprint.len(), 64);
        assert!(
            fingerprint
                .bytes()
                .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        );
    }
    assert!(actual.get("database").is_none());
    assert!(actual.get("binary_sha256").is_none());
    assert!(actual.get("source_revision").is_none());
    let mut serialized = serde_json::to_vec(&lmm_core::core_schema::schema_manifest()).unwrap();
    serialized.push(b'\n');
    assert_eq!(output.stdout, serialized);
}

#[test]
fn schema_manifest_ignores_runtime_environment_and_private_file_settings() {
    let baseline = invoke(&["schema-manifest"], &[]);
    let poisoned = invoke(
        &["schema-manifest"],
        &[
            ("LMM_CORE_DATABASE_URL", "do-not-log-this-database-url"),
            (
                "LMM_CORE_DATABASE_URL_FILE",
                "/missing-do-not-read-this-database-file",
            ),
            ("LMM_CORE_WORKER_THREADS", "not-an-integer"),
            ("LMM_CORE_LISTEN", "not-a-listen-address"),
            ("LMM_CORE_RPC_SOCKET", "/missing-do-not-open-this-socket"),
            (
                "LMM_CORE_MODEL_CONFIG_FILE",
                "/missing-do-not-read-this-model-file",
            ),
        ],
    );
    assert!(baseline.status.success());
    assert!(poisoned.status.success());
    assert!(poisoned.stderr.is_empty());
    assert_eq!(poisoned.stdout, baseline.stdout);
    assert!(!String::from_utf8_lossy(&poisoned.stdout).contains("do-not-"));
}

#[test]
fn schema_manifest_rejects_config_options_or_extra_commands_without_echoing_values() {
    for args in [
        vec![
            "schema-manifest",
            "--config",
            "/do-not-log-this-missing-config",
        ],
        vec![
            "--config",
            "/do-not-log-this-missing-config",
            "schema-manifest",
        ],
        vec!["schema-manifest", "--legacy-env"],
        vec!["--legacy-env", "schema-manifest"],
        vec!["schema-manifest", "schema-manifest"],
        vec!["schema-manifest", "init-db"],
        vec!["schema-manifest", "bootstrap-user"],
        vec!["schema-manifest", "--version"],
        vec!["schema-manifest", "do-not-log-this-argument"],
    ] {
        let output = invoke(&args, &[("LMM_CORE_WORKER_THREADS", "invalid")]);
        assert!(!output.status.success());
        assert!(output.stdout.is_empty());
        let error = String::from_utf8(output.stderr).unwrap();
        assert!(error.contains("Usage: lmm-core"));
        assert!(!error.contains("do-not-log-this"));
    }
}

#[test]
fn schema_manifest_help_and_existing_version_exit_before_configuration() {
    for args in [
        vec!["--help"],
        vec!["schema-manifest", "--help"],
        vec![
            "--config",
            "/do-not-log-this-missing-config",
            "schema-manifest",
            "--help",
        ],
    ] {
        let output = invoke(&args, &[("LMM_CORE_WORKER_THREADS", "invalid")]);
        assert!(output.status.success());
        assert!(output.stderr.is_empty());
        let help = String::from_utf8(output.stdout).unwrap();
        assert!(help.contains("schema-manifest"));
        assert!(!help.contains("do-not-log-this"));
    }
    let version = invoke(&["--version"], &[("LMM_CORE_WORKER_THREADS", "invalid")]);
    assert!(version.status.success());
    assert!(version.stderr.is_empty());
    assert_eq!(
        version.stdout,
        format!("lmm-core {}\n", env!("CARGO_PKG_VERSION")).as_bytes()
    );
}
