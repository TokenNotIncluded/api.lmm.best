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
        discovered = (output / "test-list.log").read_text()
        if discovered.count(CASE + ": test") != 1 or "1 test, 0 benchmarks" not in discovered:
            raise FixtureError("unexpected payment process test discovery")
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
        run([str(args.test_binary), CASE, "--exact", "--nocapture", "--test-threads=1"], REPO / "services/core", env, fixture.root / "payment-test.log", 180)
        log = (fixture.root / "payment-test.log").read_text()
        if "1 passed; 0 failed; 0 ignored;" not in log:
            raise FixtureError("test execution did not pass exactly the required real case")
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
        write_private(fixture.root / "acceptance.json", json.dumps({"result": "PASS", "scope": "synthetic signed bootstrap; real Core OS restart and Go main worker credit recovery only", "source_sha256": before, "product_bindings": binding, "binary_sha256": {kind: getattr(args, kind + "_sha256") for kind in ("core", "go", "test", "bootstrap")}, "seconds": round(time.monotonic()-started, 3), "process_result": str(results[0]), "cluster_removed": True, "full_payment_acceptance": False}, sort_keys=True) + "\n")
        print("PASS: independent payment OS recovery; receipt " + str(fixture.root / "acceptance.json"))
        return 0
    except (Exception, KeyboardInterrupt) as error:
        target = fixture.root if fixture else output
        write_private(target / "failure.json", json.dumps({"result": "FAIL", "error_type": type(error).__name__, "fixture_retained": fixture is not None, "seconds": round(time.monotonic()-started, 3)}, sort_keys=True) + "\n")
        print("FAIL: " + type(error).__name__ + "; private evidence retained at " + str(target), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
