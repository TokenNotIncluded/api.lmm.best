#![allow(clippy::unwrap_used, clippy::expect_used)]

use serde_json::Value;
use std::process::{Command, Output, Stdio};

fn run(args: &[&str]) -> Output {
    let home = tempfile::tempdir().unwrap();
    Command::new(env!("CARGO_BIN_EXE_lmm"))
        .args(args)
        .env("HOME", home.path())
        .env("USERPROFILE", home.path())
        .env("PATH", "")
        .stdin(Stdio::null())
        .output()
        .unwrap()
}

#[test]
fn catalog_distinguishes_discovery_from_qualified_integrations() {
    let output = run(&["catalog", "astrbot", "--json"]);
    assert!(output.status.success());
    let catalog: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(catalog[0]["id"], "astrbot");
    assert_eq!(catalog[0]["installation"], "unavailable");
    assert_eq!(catalog[0]["verified_combinations"], serde_json::json!([]));
}

#[test]
fn status_never_claims_unchecked_software_is_usable() {
    let output = run(&["status", "astrbot", "--json"]);
    assert!(output.status.success());
    let report: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(
        report["targets"][0]["verification"]["target_call"],
        "not_checked"
    );
    assert_eq!(report["targets"][0]["installation"], "unknown");
    assert_eq!(
        report["targets"][0]["verification"]["verified_at"],
        Value::Null
    );
}

#[test]
fn unattended_setup_without_target_fails_instead_of_waiting() {
    let output = run(&["setup", "--json", "--yes"]);
    assert_eq!(output.status.code(), Some(2));
    let report: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(report["outcome"], "invalid_request");
}

#[test]
fn all_does_not_promote_catalog_entries_into_install_targets() {
    let output = run(&["setup", "--all", "--json", "--yes"]);
    assert_eq!(output.status.code(), Some(3));
    let report: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(report["targets"], serde_json::json!([]));
    assert_eq!(report["plans"], serde_json::json!([]));
}

#[test]
fn setup_dry_run_reports_blockers_instead_of_success() {
    let output = run(&["setup", "astrbot", "--dry-run", "--json"]);
    assert_eq!(output.status.code(), Some(3));
    let report: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(report["plans"][0]["paid_test"], false);
    assert_eq!(report["plans"][0]["changes"], serde_json::json!([]));
    assert_eq!(report["outcome"], "blocked");
}

#[test]
fn standalone_install_never_requires_oauth_login() {
    let output = run(&["install", "astrbot", "--dry-run", "--json"]);
    let report: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(report["plans"][0]["account"], "not_required");
    assert!(!report["plans"][0]["blockers"].to_string().contains("OAuth"));
}

#[test]
fn login_in_unattended_mode_fails_before_touching_vault_or_network() {
    let output = run(&["login", "--non-interactive", "--json"]);
    assert_eq!(output.status.code(), Some(2));
    let report: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(report["outcome"], "invalid_request");
}

#[test]
fn authentication_rejects_insecure_issuer_before_accessing_secrets() {
    for command in ["logout", "models"] {
        let output = run(&[command, "--issuer", "http://127.0.0.1", "--json"]);
        assert_eq!(output.status.code(), Some(3));
        let report: Value = serde_json::from_slice(&output.stdout).unwrap();
        assert!(report["error"].as_str().unwrap().contains("HTTPS"));
    }
}

#[test]
fn unknown_or_conflicting_targets_fail_before_execution() {
    for args in [
        vec!["setup", "astrbot", "--all"],
        vec!["setup", "astrbot", "--activate", "--add-only"],
        vec!["status", "not-a-known-project"],
        vec!["status", "astrbot", "--instance", "relative"],
        vec!["status", "--instance", "/tmp/test"],
    ] {
        assert_eq!(run(&args).status.code(), Some(2), "{args:?}");
    }
}

#[test]
fn doctor_reports_incomplete_checks_with_nonzero_exit_status() {
    for args in [vec!["doctor", "--json"], vec!["doctor", "--fix", "--json"]] {
        assert_eq!(run(&args).status.code(), Some(3));
    }
}

#[test]
fn shareable_report_redacts_instance_and_evidence_paths() {
    let root = tempfile::tempdir().unwrap();
    let path = root.path().to_str().unwrap();
    let output = run(&[
        "doctor",
        "astrbot",
        "--instance",
        path,
        "--report",
        "--json",
    ]);
    let text = String::from_utf8(output.stdout).unwrap();
    assert!(!text.contains(path));
    assert!(text.contains("[redacted]"));
}

#[test]
fn read_only_commands_do_not_create_state_or_modify_instances() {
    let root = tempfile::tempdir().unwrap();
    std::fs::create_dir(root.path().join("data")).unwrap();
    let config = root.path().join("data/cmd_config.json");
    let original = b"{\"secret\":\"KEEP_PRIVATE\"}";
    std::fs::write(&config, original).unwrap();
    for command in ["status", "doctor", "setup", "restore", "disconnect"] {
        let output = run(&[
            command,
            "astrbot",
            "--instance",
            root.path().to_str().unwrap(),
            "--json",
        ]);
        assert!(!String::from_utf8_lossy(&output.stdout).contains("KEEP_PRIVATE"));
        assert_eq!(std::fs::read(&config).unwrap(), original);
    }
    assert_eq!(std::fs::read_dir(root.path()).unwrap().count(), 1);
}
