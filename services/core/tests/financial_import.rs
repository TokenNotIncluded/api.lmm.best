//! Exercise the offline Python writer against this core's exact SQL contracts.
//! CI opts into its own network-isolated PostgreSQL fixture, never the app DB.
use std::{path::Path, process::Command};

#[test]
fn offline_financial_import_contracts_and_postgres() {
    let root = Path::new(env!("CARGO_MANIFEST_DIR")).join("../..");
    let mut command = Command::new("python3");
    command.current_dir(root).args([
        "-B",
        "-m",
        "unittest",
        "discover",
        "-s",
        "scripts",
        "-p",
        "test*financial*.py",
        "-v",
    ]);
    if std::env::var("GITHUB_ACTIONS").as_deref() == Ok("true") {
        command.env("LMM_FINANCIAL_PG_TEST", "1");
    }
    assert!(
        command
            .status()
            .expect("run financial importer tests")
            .success(),
        "financial importer contract or PostgreSQL tests failed"
    );
}
