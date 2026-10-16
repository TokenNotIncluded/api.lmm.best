#!/usr/bin/env python3
"""Fresh isolated payment recovery with sealed real Core/Go main binaries.
No build, existing DSN, provider I/O, or automatic retry is performed here.
"""
from __future__ import annotations
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import sys
import time

REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO / "tests/architecture_completion"))
sys.path.insert(0, str(REPO / "tests/commerce"))
from build_lease import private_directory
from pg_fixture import Fixture, FixtureError, clean_environment, write_private
spec = importlib.util.spec_from_file_location("commerce_process_runner", REPO / "tests/commerce/verify_process.py")
commerce = importlib.util.module_from_spec(spec)
spec.loader.exec_module(commerce)
digest, run = commerce.digest, commerce.run
CASE = "real_core_and_go_payment_credit_commit_before_local_receipt_crash"
EXPIRY_CASE = "real_core_and_go_payment_accepted_proof_survives_expiry_and_os_restart"
CASES = {"commit-before-persist": (CASE, 180), "accepted-proof-expiry": (EXPIRY_CASE, 480)}


def selected_command(binary, case):
    if case not in CASES:
        raise FixtureError("unknown payment recovery case")
    name, timeout = CASES[case]
    command = [str(binary), name, "--exact", "--nocapture", "--test-threads=1"]
    if case == "accepted-proof-expiry":
        command.append("--ignored")
    return command, timeout


def verify_discovery(full, ignored):
    for text, expected in ((full, [CASE, EXPIRY_CASE]), (ignored, [EXPIRY_CASE])):
        lines = [line for line in text.splitlines() if line]
        count = len(expected)
        summary = f"{count} test{'s' if count != 1 else ''}, 0 benchmarks"
        if not lines or lines[-1] != summary or sorted(lines[:-1]) != sorted(name + ": test" for name in expected):
            raise FixtureError("unexpected payment process test discovery or ignored set")


def verify_execution(log, case):
    selected_command("unused", case)
    summaries = re.findall(r"^test result: .*?$", log, re.MULTILINE)
    expected = r"test result: ok\. 1 passed; 0 failed; 0 ignored; 0 measured; 1 filtered out; finished in [0-9.]+s"
    if len(summaries) != 1 or not re.fullmatch(expected, summaries[0]):
        raise FixtureError("test execution did not pass exactly the selected real case")


def require(condition, message):
    if not condition:
        raise FixtureError(message)


def same_json(left, right):
    """JSON equality that preserves integers, booleans and floating-point types."""
    try:
        return json.dumps(left, sort_keys=True, allow_nan=False) == json.dumps(right, sort_keys=True, allow_nan=False)
    except (TypeError, ValueError) as error:
        raise FixtureError("invalid JSON recovery evidence") from error


# @mark payments.proof-expiry role=oracle
def verify_case_result(result, main, case, *, expiry_before=None, ready=None, observations=None):
    selected_command("unused", case)
    require(isinstance(result, dict) and isinstance(main, dict), "invalid recovery evidence object")
    require(result.get("case") == case and result.get("result") == "PASS", "wrong payment recovery result case")
    for field in ("products_reaped", "core_os_restart", "go_main_worker_recovery", "credit_exactly_once", "original_jobs_preserved"):
        require(result.get(field) is True, "missing product recovery invariant: " + field)
    require(result.get("full_payment_acceptance") is False, "limited recovery scope mislabeled")
    pids = result.get("product_pids")
    require(isinstance(pids, list) and len(pids) == 4 and all(type(pid) is int and pid > 0 for pid in pids) and len(set(pids)) == 4, "four distinct real product PIDs required")
    if case != "accepted-proof-expiry":
        require("expiry" not in result and "expiry" not in main, "fast case contains expiry evidence")
        return
    expiry = result.get("expiry")
    require(isinstance(expiry, dict), "missing expiry evidence")
    require(type(expiry.get("monotonic_wait_ms")) is int and expiry["monotonic_wait_ms"] >= 302000, "expiry wait shorter than real 302 seconds")
    require(type(expiry.get("signature_age_seconds")) is int and expiry["signature_age_seconds"] > 300, "original signature did not expire")
    for field in ("zero_credit_before_restart", "fresh_unaccepted_stale_proof_denied", "accepted_stale_accept_also_denied", "durable_get_original_wire_control", "secret_unchanged"):
        require(expiry.get(field) is True, "missing expiry invariant: " + field)
    for field in ("original_payload_sha256", "original_proof_wire_sha256"):
        require(isinstance(expiry.get(field), str) and re.fullmatch(r"[a-f0-9]{64}", expiry[field]), "invalid original proof binding")
    proofs = expiry.get("proof_manifest")
    require(isinstance(proofs, list) and len(proofs) == 2 and all(isinstance(proof, dict) for proof in proofs), "two original accepted proofs required")
    for proof in proofs:
        require(isinstance(proof.get("id"), str) and re.fullmatch(r"proof_[a-f0-9]{64}", proof["id"]), "invalid durable proof ID")
        require(isinstance(proof.get("event_id"), str) and bool(proof["event_id"]), "missing original event ID")
        for field in ("signed_payload_sha256", "signature_sha256", "receipt_wire_sha256"):
            require(isinstance(proof.get(field), str) and re.fullmatch(r"[a-f0-9]{64}", proof[field]), "invalid proof manifest hash")
        payload_hash = proof.get("payload_hash")
        require(isinstance(payload_hash, list) and len(payload_hash) == 32 and all(type(byte) is int and 0 <= byte < 256 for byte in payload_hash), "invalid original payload hash")
    require(len({proof["id"] for proof in proofs}) == 2 and len({proof["event_id"] for proof in proofs}) == 2, "duplicate original proof manifest")
    require(expiry["original_proof_wire_sha256"] in {proof["receipt_wire_sha256"] for proof in proofs}, "original receipt wire is not an accepted proof")
    require(same_json(expiry_before, expiry) and same_json(main.get("expiry"), expiry), "expiry evidence changed across restart")
    require(result.get("first_credit_after_both_restarted_ready") is True and main.get("first_credit_after_both_restarted_ready") is True, "first credit readiness boundary missing")
    require(isinstance(ready, dict) and all(type(holder.get(field)) is int and holder[field] > 0 for holder in (ready, main) for field in ("core_pid", "go_pid")) and ready.get("zero_credit") is True and ready.get("original_job_snapshot_unchanged") is True and [ready.get("core_pid"), ready.get("go_pid")] == pids[2:] and [main.get("core_pid"), main.get("go_pid")] == pids[2:], "credit not bound to both new ready PIDs")
    require(isinstance(observations, list) and type(expiry.get("observations")) is int and len(observations) == expiry["observations"] and len(observations) >= 62, "missing continuous expiry observations")
    previous = 0
    original_snapshot = None
    owner_identity = None
    for index, sample in enumerate(observations):
        require(isinstance(sample, dict), "invalid expiry observation")
        elapsed = sample.get("elapsed_ms")
        require(type(elapsed) is int and previous <= elapsed <= previous + 6000 and (index == 0 or elapsed > previous), "expiry observation time gap")
        previous = elapsed
        require(all(type(sample.get(field)) is int for field in ("original_core_pid", "original_go_pid")) and [sample.get("original_core_pid"), sample.get("original_go_pid")] == pids[:2], "original live PID evidence changed")
        for field in ("credit_receipts", "journals", "wallet"):
            require(type(sample.get(field)) is int and sample[field] == 0, "money changed before expiry restart")
        require(same_json(sample.get("proof_manifest"), proofs) and sample.get("original_payload_sha256") == expiry["original_payload_sha256"], "original proof changed while waiting")
        snapshot = sample.get("go_snapshot_sha256")
        require(isinstance(snapshot, str) and re.fullmatch(r"[a-f0-9]{64}", snapshot), "missing original job snapshot hash")
        original_snapshot = snapshot if original_snapshot is None else original_snapshot
        require(snapshot == original_snapshot, "original job changed while waiting")
        lock = sample.get("owner_lock")
        require(isinstance(lock, dict), "missing owner row-lock witness")
        for field in ("transaction_alive", "exclusive_xid_lock", "row_share_relation_lock", "order_xmax_matches_owner_xid"):
            require(lock.get(field) is True, "owner row lock lost")
        require(type(lock.get("owner_backend_pid")) is int and lock["owner_backend_pid"] > 0 and isinstance(lock.get("owner_xid"), str) and lock["owner_xid"].isdigit(), "invalid owner lock identity")
        current_owner = (lock["owner_backend_pid"], lock["owner_xid"])
        owner_identity = current_owner if owner_identity is None else owner_identity
        require(current_owner == owner_identity, "owner transaction changed while waiting")
    require(previous == expiry["monotonic_wait_ms"], "last wait observation differs from expiry duration")


def sources():
    result = commerce.sources()
    grant = REPO / "deployment/postgres/grant-core-runtime.sql"
    result[str(grant.relative_to(REPO))] = digest(grant)
    for path in sorted((REPO / "tests/payments").rglob("*")):
        if path.is_file() and not path.is_symlink() and "__pycache__" not in path.parts:
            result[str(path.relative_to(REPO))] = digest(path)
    return dict(sorted(result.items()))


def product_bindings(core_binding, go_reference):
    core = json.loads(core_binding.read_text())["files"]
    go = json.loads(go_reference.read_text())["source_sha256"]
    current = sources()
    core_prefix = ("services/core/src/", "services/core/schema/", "contracts/proto/")
    core_names = {"services/core/Cargo.toml", "services/core/Cargo.lock", "services/core/build.rs", "deployment/postgres/grant-core-runtime.sql"}
    expected_core = {k: v for k, v in core.items() if k.startswith(core_prefix) or k in core_names}
    expected_go = {k: v for k, v in go.items() if k.startswith("services/extensions/")}
    actual_core = {k: v for k, v in current.items() if k.startswith(core_prefix) or k in core_names}
    actual_go = {k: v for k, v in current.items() if k.startswith("services/extensions/")}
    if not expected_core or not expected_go or expected_core != actual_core or expected_go != actual_go:
        raise FixtureError("sealed product source subsets do not exactly match current source")
    return {"core_source_count": len(actual_core), "go_source_count": len(actual_go),
            "core_binding_sha256": digest(core_binding), "go_reference_sha256": digest(go_reference),
            "product_source_drift": []}


def verify_build_receipts(core, go, *, core_sha, binding_sha, go_sha):
    """Pure fail-closed artifact provenance checks, before fixture allocation."""
    if (type(core.get("exit_code")) is not int or core.get("exit_code") != 0
            or core.get("source_drift") != []
            or core.get("binary_sha256") != core_sha
            or core.get("source_binding_sha256") != binding_sha):
        raise FixtureError("Core successful release receipt does not bind this binary and source manifest")
    if go.get("result") != "PASS" or go.get("extension_sha256") != go_sha:
        raise FixtureError("Go successful acceptance does not bind this binary")


# @mark payments.process-recovery role=runner
def main():
    signal.signal(signal.SIGTERM, commerce.interrupted)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--case", choices=tuple(CASES), default="commit-before-persist")
    parser.add_argument("--output-root", type=Path, required=True)
    parser.add_argument("--process-root", type=Path, required=True)
    parser.add_argument("--core-binding", type=Path, required=True)
    parser.add_argument("--core-build-receipt", type=Path, required=True)
    parser.add_argument("--go-reference", type=Path, required=True)
    parser.add_argument("--compiled-inputs", type=Path, required=True,
                        help="exact sources() map frozen before compiling both test artifacts")
    for kind in ("core", "go", "test", "bootstrap"):
        parser.add_argument(f"--{kind}-binary", type=Path, required=True)
        parser.add_argument(f"--{kind}-sha256", required=True)
    args = parser.parse_args()
    for kind in ("core", "go", "test", "bootstrap"):
        path, sha = getattr(args, kind + "_binary"), getattr(args, kind + "_sha256")
        if not path.is_absolute() or not path.is_file() or path.is_symlink() or not re.fullmatch("[a-f0-9]{64}", sha) or digest(path) != sha:
            parser.error(f"{kind} sealed binary mismatch")
    output, proc = args.output_root, args.process_root
    if not output.is_absolute() or not output.resolve().is_relative_to(REPO / ".local-tests"):
        parser.error("output must be absolute and under repository .local-tests")
    if not proc.is_absolute() or len(os.fsencode(proc)) > 35 or proc.is_symlink():
        parser.error("process root must be private, absolute, short, and not a symlink")
    private_directory(output)
    private_directory(proc)
    if any(proc.iterdir()):
        parser.error("process root must be fresh and empty")
    fixture = None
    started = time.monotonic()
    try:
        before = sources()
        if json.loads(args.compiled_inputs.read_text()) != before:
            raise FixtureError("compiled test input manifest differs from frozen current inputs")
        verify_build_receipts(
            json.loads(args.core_build_receipt.read_text()),
            json.loads(args.go_reference.read_text()),
            core_sha=args.core_sha256, binding_sha=digest(args.core_binding),
            go_sha=args.go_sha256)
        binding = product_bindings(args.core_binding, args.go_reference)
        binding["core_build_receipt_sha256"] = digest(args.core_build_receipt)
        write_private(output / "input-binding.json", json.dumps({"source_sha256": before, **binding}, sort_keys=True) + "\n")
        env = {k: v for k, v in clean_environment().items() if not k.startswith(("LMM_", "SQLX_", "PAYMENT_"))}
        env["TMPDIR"] = str(proc)
        # Discovery is validated before creating any database.
        run([str(args.test_binary), "--list"], REPO / "services/core", env, output / "test-list.log", 10)
        run([str(args.test_binary), "--list", "--ignored"], REPO / "services/core", env, output / "ignored-test-list.log", 10)
        verify_discovery((output / "test-list.log").read_text(), (output / "ignored-test-list.log").read_text())
        run([str(args.bootstrap_binary), "-test.list", "^TestRustDurableProofAcrossGoRestart$"], REPO / "services/extensions", env, output / "bootstrap-list.log", 10)
        if (output / "bootstrap-list.log").read_text().strip() != "TestRustDurableProofAcrossGoRestart":
            raise FixtureError("unexpected bootstrap test discovery")
        fixture = Fixture(output)
        fixture.start()
        config = dict(line.split("=", 1) for line in (fixture.root / "postgres.env").read_text().splitlines())
        env["DATABASE_URL"] = f"postgresql://a09_admin:{config['POSTGRES_PASSWORD']}@127.0.0.1:{fixture.binding['port']}/postgres"
        for kind in ("core", "go", "bootstrap"):
            env[f"LMM_PAYMENT_{kind.upper()}_BIN"] = str(getattr(args, kind + "_binary"))
            env[f"LMM_PAYMENT_{kind.upper()}_SHA256"] = getattr(args, kind + "_sha256")
        command, timeout = selected_command(args.test_binary, args.case)
        run(command, REPO / "services/core", env, fixture.root / "payment-test.log", timeout)
        verify_execution((fixture.root / "payment-test.log").read_text(), args.case)
        results = list(proc.glob("pay-*/result.json"))
        if len(results) != 1:
            raise FixtureError("missing unique independent process result")
        result = json.loads(results[0].read_text())
        if result["result"] != "PASS" or not result["products_reaped"]:
            raise FixtureError("product identity or recovery result incomplete")
        for pid in result["product_pids"]:
            if Path(f"/proc/{pid}").exists():
                raise FixtureError("owned product PID still exists")
        main_recovered = json.loads((results[0].parent / "main-recovered.json").read_text())
        replay_recovered = json.loads((results[0].parent / "bootstrap-replay-recovered.json").read_text())
        evidence = {}
        if args.case == "accepted-proof-expiry":
            evidence = {"expiry_before": json.loads((results[0].parent / "expiry-before-restart.json").read_text()),
                        "ready": json.loads((results[0].parent / "new-products-ready-before-first-credit.json").read_text()),
                        "observations": [json.loads(path.read_text()) for path in sorted(results[0].parent.glob("expiry-observation-*.json"))]}
        verify_case_result(result, main_recovered, args.case, **evidence)
        if (main_recovered["result"] != "PASS" or main_recovered["inbox_done"] != 1
                or replay_recovered["result"] != "PASS" or replay_recovered["inbox_done"] != 2
                or main_recovered["credit_receipt"] != replay_recovered["credit_receipt"]
                or main_recovered["credit_job_key"] != replay_recovered["original_credit_job_key"]
                or main_recovered["funds"] != replay_recovered["funds"]):
            raise FixtureError("main-worker and later bootstrap replay evidence disagree")
        identities = list(results[0].parent.glob("*.identity.*.json"))
        # Four products each get spawn/before-ready/ready and crash/final witness.
        product_identities = [p for p in identities if not p.name.startswith("bootstrap-")]
        if len(product_identities) != 16:
            raise FixtureError("product identity witness count differs from four real processes")
        for kind in ("core", "go", "test", "bootstrap"):
            if digest(getattr(args, kind + "_binary")) != getattr(args, kind + "_sha256"):
                raise FixtureError("sealed artifact drift after run")
        if sources() != before:
            raise FixtureError("source drift after real execution")
        fixture.verify()
        fixture.cleanup()
        write_private(fixture.root / "acceptance.json", json.dumps({"result": "PASS", "case": args.case, "scope": "synthetic signed bootstrap; real Core OS restart and Go main worker credit recovery only", "source_sha256": before, "product_bindings": binding, "binary_sha256": {kind: getattr(args, kind + "_sha256") for kind in ("core", "go", "test", "bootstrap")}, "seconds": round(time.monotonic()-started, 3), "process_result": str(results[0]), "cluster_removed": True, "full_payment_acceptance": False}, sort_keys=True) + "\n")
        print("PASS: independent payment OS recovery; receipt " + str(fixture.root / "acceptance.json"))
        return 0
    except (Exception, KeyboardInterrupt) as error:
        target = fixture.root if fixture else output
        write_private(target / "failure.json", json.dumps({"result": "FAIL", "error_type": type(error).__name__, "fixture_retained": fixture is not None, "seconds": round(time.monotonic()-started, 3)}, sort_keys=True) + "\n")
        print("FAIL: " + type(error).__name__ + "; private evidence retained at " + str(target), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
