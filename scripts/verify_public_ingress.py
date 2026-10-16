#!/usr/bin/env python3
"""Verify bounded Core ingress using sealed executables and fresh owned PostgreSQL.

Unit mode checks local protocol semantics. Red mode requires the reviewed old
product to fail only the connection/retained-kernel contract, while its healthy
paid traffic succeeds. Green mode requires the complete contract. Both resource
modes keep all 512 offered socket identities until the recovery gate. Databases
and generator have separate budgets; this is not whole-host acceptance.
Run under agcc with selected output/process roots. No existing database URL or
ambient application credential is accepted. Failed fixtures remain private.
"""

import argparse
import json
import os
import re
import secrets
import signal
import subprocess
import sys
import threading
import time
from pathlib import Path

repo = Path(__file__).resolve().parents[1]
sys.dont_write_bytecode = True


# @mark runtime.connection-admission role=verification
def main() -> int:
    # Load the repository siblings only when the CLI is invoked.
    sys.path[:0] = [str(repo / "scripts"), str(repo / "tests/commerce")]
    import verify_model_process as v
    import verify_process as commerce
    from pg_fixture import Fixture, FixtureError, private_directory

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=["unit", "red", "green"], required=True)
    for name in [
        "output",
        "process-root",
        "test-binary",
        "core-binary",
        "source-binding",
        "build-receipt",
    ]:
        parser.add_argument("--" + name, type=Path, required=True)
    for name in ["test-sha256", "core-sha256"]:
        parser.add_argument("--" + name, required=True)
    a = parser.parse_args()

    def interrupted(signum, frame):
        raise KeyboardInterrupt("local acceptance interrupted")

    signal.signal(signal.SIGTERM, interrupted)

    def exact(obj, key, value):
        assert type(obj.get(key)) is type(value) and obj[key] == value, key

    recipe_sha = v.sha(Path(__file__))
    for path in [a.output, a.process_root]:
        private_directory(path)
        assert not any(path.iterdir()), "fresh empty owned root required"
    for path, wanted in [
        (a.test_binary, a.test_sha256),
        (a.core_binary, a.core_sha256),
    ]:
        assert (
            path.is_absolute()
            and path.resolve() == path
            and v.sha(v.regular(path)) == wanted
        )
    frozen = commerce.sources()
    v.record(a.output / "inputs.json", frozen)
    reference = {"files": json.loads(a.source_binding.read_text())["files"]}
    build = json.loads(a.build_receipt.read_text())
    assert (
        type(build["exit_code"]) is int
        and build["exit_code"] == 0
        and build["source_drift"] == []
        and build["source_binding_sha256"] == v.sha(a.source_binding)
        and build["binary_sha256"] == a.core_sha256
    )
    if a.mode == "red":
        old_commit = "a731cbf1398d308e781eabc97aba723fc426fec0"
        for name, wanted in reference["files"].items():
            raw = subprocess.check_output(
                ["git", "show", old_commit + ":" + name], cwd=repo
            )
            assert __import__("hashlib").sha256(raw).hexdigest() == wanted
    else:
        assert reference["files"] == v.snapshot(v.PRODUCT_TREES, v.PRODUCT_FILES)
    v.record(
        a.output / "plan.json",
        {
            "mode": a.mode,
            "test_sha256": a.test_sha256,
            "core_sha256": a.core_sha256,
            "source_binding_sha256": v.sha(a.source_binding),
            "build_receipt_sha256": v.sha(a.build_receipt),
            "recipe_sha256": v.sha(Path(__file__)),
            "old_product_commit": old_commit if a.mode == "red" else None,
        },
    )
    env = v.environment()
    env.update(
        XDG_RUNTIME_DIR="/run/user/" + str(os.geteuid()),
        DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/" + str(os.geteuid()) + "/bus",
        TMPDIR=str(a.process_root),
        LMM_PUBLIC_INGRESS_ACCEPTANCE="1",
        LMM_PUBLIC_INGRESS_ROOT=str(a.process_root),
        LMM_PUBLIC_CORE_BIN=str(a.core_binary),
        LMM_PUBLIC_CORE_SHA256=a.core_sha256,
        LMM_PUBLIC_TEST_SHA256=a.test_sha256,
    )
    unit = "lmm-public-generator-" + secrets.token_hex(16) + ".scope"

    def system(*args):
        return subprocess.run(
            ["/usr/bin/systemctl", "--user", *args],
            env=env,
            capture_output=True,
            text=True,
            timeout=3,
        )

    def props(unit):
        result = system(
            "show",
            unit,
            "--property=LoadState",
            "--property=ActiveState",
            "--property=MainPID",
            "--property=ControlGroup",
            "--property=Description",
        )
        return result, {
            k: v
            for k, v in (
                line.split("=", 1) for line in result.stdout.splitlines() if "=" in line
            )
        }

    def absent(unit, group):
        _, state = props(unit)
        gone = state.get("LoadState") == "not-found" or state.get("ActiveState") in [
            "inactive",
            "failed",
        ]
        assert gone and (not group or not Path(group).exists()), (
            "owned unit or cgroup still active"
        )
        assert system("show", "--property=Version").returncode == 0, (
            "cannot distinguish missing unit from failed user bus"
        )
        return state

    stop = threading.Event()
    observed = {}
    monitor_errors = []

    def monitor():
        while not stop.wait(0.1):
            try:
                _, state = props(unit)
                relative = state.get("ControlGroup", "")
                if not relative or ".." in Path(relative).parts:
                    continue
                group = Path("/sys/fs/cgroup") / relative.lstrip("/")
                limits = {
                    n: (group / n).read_text().strip()
                    for n in ["cpu.max", "memory.max", "memory.swap.max", "pids.max"]
                }
                assert limits == {
                    "cpu.max": "100000 100000",
                    "memory.max": "536870912",
                    "memory.swap.max": "0",
                    "pids.max": "128",
                }
                for pid in (group / "cgroup.procs").read_text().split():
                    path = Path("/proc") / pid / "exe"
                    if v.sha(path) != a.test_sha256:
                        continue
                    observed.update(
                        unit=unit,
                        cgroup=str(group),
                        limits=limits,
                        identity=v.process_identity(int(pid)),
                        executed_sha256=a.test_sha256,
                    )
                    v.record(a.output / "generator-observed.json", observed)
                    return
            except (
                OSError,
                ValueError,
                AssertionError,
                subprocess.SubprocessError,
            ) as error:
                monitor_errors.append(type(error).__name__)

    def cleanup_core():
        p = a.process_root / "core-allocation.json"
        if not p.exists():
            return {"allocated": False}
        allocation = json.loads(p.read_text())
        owner = json.loads((a.process_root / "test-owner.json").read_text())
        nonce = allocation["nonce"]
        name = allocation["unit"]
        assert (
            re.fullmatch("[0-9a-f]{32}", nonce)
            and name == "lmm-prehead-" + nonce + ".service"
        )
        assert allocation["expected_binary_sha256"] == a.core_sha256 and allocation[
            "expected_binary"
        ] == str(a.core_binary)
        assert (
            allocation["test_owner"] == owner["identity"]
            and owner["executed_sha256"] == a.test_sha256
        )
        assert observed and observed["identity"] == owner["identity"]
        ready_path = a.process_root / "core-ready.json"
        ready = json.loads(ready_path.read_text()) if ready_path.exists() else {}
        saved_group = ready.get("cgroup", "")
        result, state = props(name)
        if state.get("LoadState") != "not-found":
            assert state.get("Description") == "Owned ingress " + nonce
            pid = int(state.get("MainPID", "0"))
            if pid:
                assert v.sha(Path("/proc") / str(pid) / "exe") == a.core_sha256
                if ready:
                    assert pid == ready["pid"] and v.process_identity(pid)[
                        "start_ticks"
                    ] == int(ready["stat"][ready["stat"].rfind(")") + 2 :].split()[19])
            assert system("stop", name).returncode == 0
        final = absent(name, saved_group)
        if ready:
            assert not (Path("/proc") / str(ready["pid"])).exists()
        return {
            "allocated": True,
            "unit": name,
            "cgroup": saved_group,
            "final": final,
            "actual_pid_absent": not ready
            or not (Path("/proc") / str(ready["pid"])).exists(),
        }

    fixture = None
    failure = None
    execution_error = None
    outcome = "FAIL"
    cleanup = {}
    started = time.monotonic()
    thread = threading.Thread(target=monitor, daemon=True)
    try:
        if a.mode != "unit":
            fixture = Fixture(a.output)
            fixture.env = {
                **v.environment(),
                "DOCKER_CONFIG": fixture.env["DOCKER_CONFIG"],
            }
            fixture.start()
            config = dict(
                line.split("=", 1)
                for line in (fixture.root / "postgres.env").read_text().splitlines()
            )
            env["DATABASE_URL"] = (
                "postgresql://a09_admin:"
                + config["POSTGRES_PASSWORD"]
                + "@127.0.0.1:"
                + str(fixture.binding["port"])
                + "/postgres"
            )
        command = (
            [str(a.test_binary), "--nocapture", "--test-threads=2"]
            if a.mode == "unit"
            else [
                str(a.test_binary),
                "bounded_preheader_peers_do_not_exhaust_or_orphan_real_core",
                "--exact",
                "--ignored",
                "--nocapture",
                "--test-threads=1",
            ]
        )
        command = [
            "/usr/bin/systemd-run",
            "--user",
            "--quiet",
            "--scope",
            "--collect",
            "--unit",
            unit,
            "--property=CPUQuota=100%",
            "--property=CPUQuotaPeriodSec=100ms",
            "--property=MemoryMax=512M",
            "--property=MemorySwapMax=0",
            "--property=TasksMax=128",
            "--",
            *command,
        ]
        v.record(a.output / "command.json", {"command": command, "unit": unit})
        thread.start()
        try:
            commerce.run(
                command, repo / "services/core", env, a.output / "test.log", 180
            )
        except FixtureError as error:
            execution_error = type(error).__name__
        stop.set()
        thread.join(4)
        assert not thread.is_alive() and observed, (
            "actual generator identity/budget not observed"
        )
        status = json.loads((a.output / "test.log.result.json").read_text())
        assert status["process_group_removed"]
        text = (a.output / "test.log").read_text()
        if a.mode == "unit":
            assert execution_error is None and re.search(
                r"^test result: ok\. 6 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out;",
                text,
                re.M,
            )
            outcome = "PASS"
        else:
            owner = json.loads((a.process_root / "test-owner.json").read_text())
            assert (
                owner["identity"] == observed["identity"]
                and owner["executed_sha256"] == a.test_sha256
                and owner["cgroup"] == observed["cgroup"]
            )
            result = json.loads((a.process_root / "result.json").read_text())
            funds = json.loads((a.process_root / "health-and-funds.json").read_text())
            for key, value in {
                "products_reaped": True,
                "offered": 512,
                "complete_models": 45,
            }.items():
                exact(result, key, value)
            exact(result, "same_healthy_h2_tcp", True)
            for key, value in {
                "complete": 45,
                "provider_hits": 45,
                "wallet": 97795,
                "reserved": 0,
            }.items():
                exact(funds, key, value)
            assert (
                type(result["max_public_fd_count"]) is int
                and type(result["retained_expired_kernel_rows"]) is int
            )
            if a.mode == "green":
                assert (
                    execution_error is None
                    and result["result"] == "PASS"
                    and result["failures"] == []
                    and result["max_public_fd_count"] <= 65
                    and result["retained_expired_kernel_rows"] == 0
                )
                assert re.search(
                    r"^test result: ok\. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out;",
                    text,
                    re.M,
                )
                outcome = "PASS"
            else:
                assert (
                    execution_error is not None
                    and result["result"] == "FAIL"
                    and result["failures"]
                    and (
                        result["max_public_fd_count"] > 65
                        or result["retained_expired_kernel_rows"] > 0
                    )
                )
                assert all(
                    re.fullmatch(
                        r"public FD count [0-9]+ exceeded64\+1|[0-9]+ expired/rejected owned TCP states survived while clients still retained",
                        f,
                    )
                    for f in result["failures"]
                )
                assert re.search(
                    r"^test result: FAILED\. 0 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out;",
                    text,
                    re.M,
                )
                outcome = "EXPECTED_RED"
        assert (
            commerce.sources() == frozen
            and v.sha(a.test_binary) == a.test_sha256
            and v.sha(a.core_binary) == a.core_sha256
            and v.sha(Path(__file__)) == recipe_sha
        )
    except BaseException as error:
        failure = type(error).__name__
        outcome = "FAIL"
    finally:
        stop.set()
        if thread.ident:
            thread.join(4)
        try:
            cleanup["core"] = cleanup_core()
            system("stop", unit)
            cleanup["generator"] = absent(unit, observed.get("cgroup", ""))
            if fixture and fixture.container:
                fixture.verify()
                if outcome == "PASS":
                    fixture.cleanup()
                    cleanup["cluster_removed"] = True
                else:
                    cleanup["failed_cluster_retained"] = True
        except BaseException as error:
            failure = (failure or "") + ";cleanup:" + type(error).__name__
            outcome = "FAIL"
        v.record(
            a.output / "result.json",
            {
                "result": outcome,
                "error_type": failure,
                "test_execution_error": execution_error,
                "mode": a.mode,
                "seconds": time.monotonic() - started,
                "cleanup": cleanup,
                "source_drift": [] if commerce.sources() == frozen else ["changed"],
                "generator_observed": bool(observed),
                "monitor_transient_errors": monitor_errors,
                "core_sha256": a.core_sha256,
                "test_sha256": a.test_sha256,
                "whole_project_complete": False,
            },
        )
        print(
            json.dumps(
                {
                    "result": outcome,
                    "mode": a.mode,
                    "seconds": time.monotonic() - started,
                    "error_type": failure,
                    "receipt": str(a.output / "result.json"),
                }
            )
        )
    return 0 if outcome in ["PASS", "EXPECTED_RED"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
