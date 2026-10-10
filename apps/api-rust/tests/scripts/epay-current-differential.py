#!/usr/bin/env python3
"""Compare current Go ePay handlers with Rust using the same checkout fixtures.

Go uses its native in-memory SQLite test helper; Rust uses disposable native
PostgreSQL and Valkey. This proves business/HTTP/ledger behavior for these
fixtures, not PostgreSQL driver-error equivalence or a production deployment.
"""

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import re


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[4]
    output = args.output_dir.resolve()
    output.mkdir(parents=True, exist_ok=True)
    reference = output / "current-go-epay.json"
    env = dict(os.environ, GOMAXPROCS="2", LMM_EPAY_GO_ORACLE_OUTPUT=str(reference))
    go_command = ["go", "test", "-p", "1", "./controller", "-run", "^TestRustEpayCurrentGoOracle$", "-count=1"]
    with (output / "go.log").open("w") as log:
        subprocess.run(go_command, cwd=root / "apps/api-go", env=env, stdout=log, stderr=subprocess.STDOUT, check=True)
    fixture_count = len(json.loads((root / "apps/api-rust/tests/fixtures/epay-current-go-input.json").read_text()))
    if json.loads(reference.read_text()).get("fixture_count") != str(fixture_count):
        raise RuntimeError("current Go did not produce every requested fixture")
    metadata = {"go_revision": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
                "go_reference": str(reference), "fixture_count": fixture_count,
                "go_database": "in-memory SQLite", "rust_database": "isolated PostgreSQL 18",
                "production_access": False}
    (output / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
    command = [sys.executable, str(Path(__file__).with_name("with-local-services.py")),
               "--output-dir", str(output / "services"), "--", "cargo", "test", "--locked", "--test", "epay_runtime_postgres",
               "current_go_checkout_notification_and_rejection_fixtures_match_rust", "--", "--exact", "--ignored", "--test-threads=1"]
    with (output / "rust.log").open("w") as log:
        subprocess.run(command, cwd=root / "apps/api-rust", env=env, stdout=log, stderr=subprocess.STDOUT, check=True)
    if not re.search(r"test result: ok\. 1 passed; 0 failed; 0 ignored;", (output / "rust.log").read_text()):
        raise RuntimeError("the Rust differential must execute exactly one passing test; see rust.log")
    print(f"Current-Go/Rust ePay differential passed: {output}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
