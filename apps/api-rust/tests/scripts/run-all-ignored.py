#!/usr/bin/env python3
"""Run ignored and known conditional dependency tests, each with a fresh database.

Unlike `cargo test`, this fails if no ignored tests were discovered. The exact
compiled test names are recorded and every invocation must execute one test.
"""

import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time
from urllib.request import ProxyHandler, build_opener

DIRECTORY = Path(__file__).resolve().parent
RUST_ROOT = DIRECTORY.parents[1]
spec = importlib.util.spec_from_file_location("local_services", DIRECTORY / "with-local-services.py")
services_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(services_module)

# These tests were historically reported as passing after an early return when
# their dependency variable was absent. Execute them explicitly without
# --ignored and supply their real dependencies; a renamed/removed test is an
# inventory error, never a successful zero-test run.
CONDITIONAL_DEPENDENCY_TESTS = {
    "postgres_sms_minimum_balance_reservation",
    "postgres_refund_is_transactional_and_idempotent",
    "postgres_repository_reads_authoritative_options_and_enabled_providers",
    "pg_options_lock_is_held_through_key_and_card_commit",
    "pg_commit_time_authorization_fence_rejects_completed_security_mutations",
    "pg_confirmation_is_session_bound_expiring_replay_safe_and_exactly_once",
    "pg_confirmation_revalidates_group_ratio_warning_and_user_state",
    "pg_zero_ratio_warning_matches_go_default_and_explicit_disable_wins",
    "pg_reveal_checks_owner_expiry_and_ciphertext_before_marking_revealed",
    "pg_reveal_is_exactly_once_under_a_deterministic_start_barrier",
    "complete_topup_uses_authoritative_quota_option_and_is_atomic_and_idempotent",
    "go_signed_receipt_and_immutable_manifest_are_compatible",
}


def listed_tests(listing):
    return {line[:-6] for line in listing.splitlines() if line.endswith(": test")}


def compiled_tests(output):
    command = ["cargo", "test", "--manifest-path", str(RUST_ROOT / "Cargo.toml"),
               "--locked", "--workspace", "--all-targets", "--all-features", "--no-run",
               "--message-format=json"]
    with (output / "compile.jsonl").open("w") as stdout, (output / "compile.log").open("w") as stderr:
        result = subprocess.run(command, cwd=RUST_ROOT, stdout=stdout, stderr=stderr)
    if result.returncode:
        raise RuntimeError(f"test compilation failed; see {output / 'compile.log'} and compile.jsonl")
    tests = []
    seen = set()
    found_conditional = set()
    for line in (output / "compile.jsonl").read_text().splitlines():
        message = json.loads(line)
        executable = message.get("executable")
        if message.get("reason") != "compiler-artifact" or not executable or not message.get("profile", {}).get("test"):
            continue
        if executable in seen:
            continue
        seen.add(executable)
        ignored = listed_tests(subprocess.check_output([executable, "--ignored", "--list"], text=True, timeout=30))
        all_tests = listed_tests(subprocess.check_output([executable, "--list"], text=True, timeout=30))
        for name in sorted(all_tests):
            leaf = name.rsplit("::", 1)[-1]
            conditional = leaf in CONDITIONAL_DEPENDENCY_TESTS
            if conditional:
                found_conditional.add(leaf)
            if name in ignored or conditional:
                record = {"target": message["target"]["name"], "executable": executable, "name": name, "ignored": name in ignored}
                if name == "report::tests::report_umask_subprocess":
                    parent = "report::tests::report_should_create_mode_0600_under_umask_022"
                    if parent not in all_tests:
                        raise RuntimeError("umask subprocess helper is missing its compiled parent test")
                    # The parent sets umask 022, supplies a private output
                    # directory, invokes this helper and verifies mode 0600.
                    record.update({"run_name": parent, "run_ignored": False})
                tests.append(record)
    if not any(test["ignored"] for test in tests):
        raise RuntimeError("zero ignored tests discovered; refusing to report an empty pass")
    missing = CONDITIONAL_DEPENDENCY_TESTS - found_conditional
    if missing:
        raise RuntimeError("required conditional dependency tests missing from compiled inventory: " + ", ".join(sorted(missing)))
    return tests


def environment_for(services, database):
    env = services.environment()
    database_url = services.database_url.rsplit("/", 1)[0] + "/" + database
    for key, value in list(env.items()):
        if value in (services.database_url, services.database_url.rsplit("/", 1)[0] + "/lmm_test_adopt"):
            env[key] = database_url
    env.update({
        "LMM_TEST_PG_SOCKET": "127.0.0.1", "LMM_TEST_PG_PORT": str(services.pg_port),
        "LMM_TEST_PG_DATABASE": database, "PGUSER": services.role,
        "PGPASSWORD": services.password, "PGDATABASE": database,
    })
    return env


def prepare_test(services, database, name):
    extra_env = {}
    if name.endswith("loopback_provider_contract"):
        port = services_module.free_port()
        provider = services.launch(
            [sys.executable, str(DIRECTORY / "relay-misc-provider-fixture.py"), str(port),
             str(services.runtime / f"{database}-relay-misc-hits.jsonl")],
            f"{database}-relay-misc-provider",
        )
        base_url = f"http://127.0.0.1:{port}"
        opener = build_opener(ProxyHandler({}))
        deadline = time.monotonic() + 10
        while True:
            if provider.poll() is not None:
                raise RuntimeError("loopback relay provider exited before readiness")
            try:
                with opener.open(base_url + "/health", timeout=1) as response:
                    if response.status == 200:
                        break
            except OSError:
                if time.monotonic() >= deadline:
                    raise RuntimeError("loopback relay provider readiness timed out") from None
                time.sleep(0.02)
        extra_env["LMM_RELAY_MISC_PROVIDER_URL"] = base_url
    if name in (
        "adoption_should_commit_once_replay_without_writes_and_reject_partial_ledger",
        "adoption_lock_timeout_should_not_create_ledger",
    ):
        # Adoption intentionally rejects PostgreSQL's default "$user",public
        # search_path. Configure the fresh test database as the real adoption
        # rehearsal does; do not relax the production schema-resolution check.
        services.sql(f'ALTER DATABASE "{database}" SET search_path = public;', "postgres")
    # This existing fixture deliberately consumes the deployed contract-7
    # profile schema. Other fixtures create their own minimal tables/schemas.
    if name.rsplit("::", 1)[-1] in {
        "company_billing_profile_get_put_is_owner_scoped_and_cascades_with_user",
        "rankings_pg_snapshot_keeps_history_previous_rank_and_vendor_metadata",
        "token_auth_pg_lookup_requires_an_active_owner_and_carries_owner_context",
    }:
        baseline = RUST_ROOT / "crates/lmm-db-migrate/schema/postgresql-baseline.sql"
        services.sql(baseline.read_text(), database)
        for migration in sorted((RUST_ROOT / "migrations").glob("*.sql")):
            if int(migration.name.split("_", 1)[0]) <= 7:
                services.sql(migration.read_text().replace("__LMM_APP_SCHEMA__", "public"), database)
    if name == "sqlite_and_postgres_should_have_identical_canonical_table_hashes":
        # The legacy equivalence fixture explicitly invokes psql -U postgres.
        # This role exists only inside the newly created disposable cluster.
        services.sql(
            "DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='postgres') THEN "
            f"CREATE ROLE postgres LOGIN SUPERUSER PASSWORD '{services.password}'; "
            "END IF; END $$;", "postgres",
        )
    if name.endswith("go_signed_receipt_and_immutable_manifest_are_compatible"):
        fixture = services.runtime / "controller-backup-interop"
        if not fixture.exists():
            env = services.environment()
            env["LMM_CONTROLLER_BACKUP_INTEROP_FIXTURE_DIR"] = str(fixture)
            env.setdefault("GOMAXPROCS", "2")
            env.setdefault("GOFLAGS", "-p=2")
            with (services.runtime / "controller-backup-interop.log").open("w") as log:
                subprocess.run(["go", "test", "./internal/appcli", "-run", "^TestControllerReceiptExportInterop$", "-count=1"],
                               cwd=RUST_ROOT.parent / "api-go", env=env, stdout=log, stderr=subprocess.STDOUT,
                               check=True, timeout=180)
        for required in ("manifest.json", "receipt.json"):
            if not (fixture / required).is_file():
                raise RuntimeError("Go controller-backup export did not produce its required fixture")
        extra_env["LMM_CONTROLLER_BACKUP_INTEROP_FIXTURE_DIR"] = str(fixture)
    return extra_env


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--filter", action="append", default=[], help="substring of target::test; repeat for a union of selections, never allowing an empty selection")
    parser.add_argument("--fuzz-seconds", type=int, default=5)
    args = parser.parse_args()
    if args.fuzz_seconds <= 0:
        parser.error("--fuzz-seconds must be positive; zero-duration fuzzing is not a pass")
    output = args.output_dir.resolve()
    output.mkdir(parents=True, exist_ok=True)
    tests = compiled_tests(output)
    tests = [test for test in tests if not args.filter or any(value in f"{test['target']}::{test['name']}" for value in args.filter)]
    if not tests:
        parser.error("filter selected zero ignored tests")
    (output / "inventory.json").write_text(json.dumps(tests, indent=2) + "\n")
    report = {"selected": len(tests), "passed": 0, "failed": 0, "tests": []}
    with services_module.local_services(output) as services:
        for index, test in enumerate(tests, 1):
            database = f"lmm_test_case_{index}"
            log_path = output / f"{index:03d}-{test['target']}.log"
            started = time.monotonic()
            code = -1
            try:
                services.sql(f'CREATE DATABASE "{database}";', "postgres")
                fixture_env = prepare_test(services, database, test["name"])
                env = environment_for(services, database)
                env.update(fixture_env)
                env["LMM_FUZZ_SECONDS"] = str(args.fuzz_seconds)
                subprocess.run(["valkey-cli", "-h", "127.0.0.1", "-p", str(services.valkey_port), "FLUSHALL"],
                               env=dict(env, VALKEYCLI_AUTH=services.password), check=True, capture_output=True)
                with log_path.open("w") as log:
                    command = [test["executable"], test.get("run_name", test["name"]), "--exact", "--test-threads=1"]
                    if test.get("run_ignored", test["ignored"]):
                        command.append("--ignored")
                    result = subprocess.run(command,
                                            cwd=RUST_ROOT, env=env, stdout=log, stderr=subprocess.STDOUT, timeout=300)
                text = log_path.read_text()
                code = result.returncode
                passed = code == 0 and re.search(r"test result: ok\. 1 passed; 0 failed; 0 ignored;", text) is not None
            except (subprocess.SubprocessError, OSError, RuntimeError) as error:
                # Do not persist exception command arguments, which can carry
                # generated service passwords or database connection strings.
                with log_path.open("a") as log:
                    log.write(f"\nharness failure: {type(error).__name__}\n")
                passed = False
            finally:
                services.sql(f'DROP DATABASE IF EXISTS "{database}" WITH (FORCE);', "postgres")
            report["passed" if passed else "failed"] += 1
            report["tests"].append({**test, "passed": passed, "exit_code": code,
                                    "seconds": time.monotonic() - started, "log": str(log_path)})
            (output / "results.json").write_text(json.dumps(report, indent=2) + "\n")
            print(f"[{index}/{len(tests)}] {'PASS' if passed else 'FAIL'} {test['target']}::{test['name']}", flush=True)
    print(f"dependency tests: {report['passed']} passed, {report['failed']} failed", flush=True)
    return 0 if report["failed"] == 0 else 1


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
    sys.exit(main())
