#!/usr/bin/env python3
"""Opt-in frozen Core resource gate. Never accepts an external database URL.

Prepare requires a successfully source-bound release build. Run uses only its
sealed binaries and a fresh owned PostgreSQL fixture. Core alone is constrained;
combined Go/node capacity remains outside this gate. The socket-backpressure
case is a separate opt-in regression, not the sustained throughput gate.
Wrap prepare/run in agcc with explicit output and Core target roots.
"""
from __future__ import annotations
import argparse
import json
import os
from pathlib import Path
import re
import secrets
import signal
import subprocess
import sys
import time
import verify_model_process as v
from pg_fixture import Fixture, FixtureError, private_directory

CASE = "resource_cases::independent_process_constrained_resources"
CASES = {
    "sustained": CASE,
    "socket-backpressure": "socket_cases::independent_process_socket_backpressure",
}
SELF = Path(__file__).resolve()


def reviewed_tests(discovered):
    expected = v.PROCESS_TESTS | set(CASES.values())
    if (not isinstance(discovered, list)
            or any(not isinstance(name, str) for name in discovered)
            or len(discovered) != len(expected) or set(discovered) != expected):
        raise FixtureError("test discovery differs from the unique reviewed cases")


def selected_command(binary, selection):
    if selection not in CASES:
        raise FixtureError("unknown resource case")
    return [str(binary), CASES[selection], "--exact", "--ignored",
            "--nocapture", "--test-threads=1"]


def require_selected_success(text, discovered):
    reviewed_tests(discovered)
    counts = re.findall(r"^test result: ok\. (\d+) passed; (\d+) failed; (\d+) ignored; (\d+) measured; (\d+) filtered out;", text, re.M)
    if counts != [("1", "0", "0", "0", str(len(discovered) - 1))]:
        raise FixtureError("exactly one selected resource case must pass; all other reviewed cases filtered")


def inputs():
    return {**v.inputs(), "resource_runner_sha256": v.sha(SELF),
            "resource_runner_tests_sha256": v.sha(v.REPO / "tests/architecture_completion/test_verify_model_resources.py")}


def check(frozen):
    if inputs() != frozen:
        raise FixtureError("resource gate inputs changed; retain receipts and prepare again")


def env():
    result = v.environment()
    result.update(MISE_DISABLE_TOOLS="rust", RUSTUP_TOOLCHAIN="1.99.0",
                  RUSTUP_AUTO_INSTALL="0", CARGO_NET_OFFLINE="true", SQLX_OFFLINE="true")
    return result


# @mark model.resource-acceptance role=runner
def prepare(args):
    private_directory(args.output)
    private_directory(args.resource)
    private_directory(args.requests)
    binary = v.regular(args.core_binary)
    binding = v.read_json(args.source_binding)
    build = v.read_json(args.build_receipt)
    reference_sha = {"binding": v.sha(args.source_binding), "build": v.sha(args.build_receipt)}
    frozen = inputs()
    if (type(build.get("exit_code")) is not int or build.get("exit_code") != 0 or build.get("source_drift") != []
            or build.get("source_binding_sha256") != v.sha(v.regular(args.source_binding))
            or build.get("binary_sha256") != v.sha(binary)
            or binding.get("files") != frozen["product"]):
        raise FixtureError("release build/source binding does not match current product")
    if (args.output / "prepared.json").exists():
        raise FixtureError("prepare is one-shot; preserve the previous output")
    target = v.REPO / "services/core/target"
    info = target.lstat()
    if target.resolve() != target or info.st_uid != os.geteuid() or info.st_mode & 0o022:
        raise FixtureError("unsafe standard Core target")
    pin = v.tomllib.loads((v.REPO / "services/core/rust-toolchain.toml").read_text())["toolchain"]["channel"]
    mise_pin = v.tomllib.loads((v.REPO / "mise.toml").read_text())["tools"]["rust"]["version"]
    if pin != "1.99.0" or mise_pin != pin:
        raise FixtureError("resource runner and exact repository Rust pins must agree")
    environment = env()
    compile_tmp = args.output / "compile-tmp"
    compile_tmp.mkdir(mode=0o700)
    environment["TMPDIR"] = str(compile_tmp)
    rustup = subprocess.check_output(["rustup", "which", "--toolchain", "1.99.0", "cargo"],
                                     env=environment, text=True, timeout=30).strip()
    cargo = v.regular(Path(rustup).resolve())
    rustc = v.regular(cargo.parent / "rustc")
    version = subprocess.check_output([str(rustc), "--version"], env=environment,
                                      text=True, timeout=30).strip()
    if version.split()[:2] != ["rustc", "1.99.0"]:
        raise FixtureError("installed Rust differs from frozen repository pin")
    environment["RUSTC"] = str(rustc)
    command = ["mise", "exec", "--", str(cargo), "test", "--locked", "--offline",
               "--no-run", "--features", "resource-acceptance", "--test", "model_process",
               "--jobs", "2", "--target-dir", str(target), "--message-format=json"]
    manifest = args.output / "inputs.json"
    v.record(manifest, frozen)
    lease = v.REPO / "tests/architecture_completion/build_lease.py"
    with (args.output / "compile.jsonl").open("x") as output:
        result = subprocess.run([sys.executable, "-B", str(lease), "run",
            "--resource", str(args.resource), "--requests", str(args.requests),
            "--task", "S11", "--kind", "cargo", "--input-sha256", v.sha(manifest),
            "--cwd", str(v.REPO / "services/core"), "--timeout-seconds", "1200", "--", *command],
            env=environment, stdout=output, stderr=subprocess.STDOUT, timeout=1800)
    if result.returncode:
        raise FixtureError("resource test compilation failed; inspect compile.jsonl")
    check(frozen)
    executables = []
    for line in (args.output / "compile.jsonl").read_text().splitlines():
        try:
            message = json.loads(line)
        except json.JSONDecodeError:
            continue
        if (message.get("reason") == "compiler-artifact" and message.get("executable")
                and message.get("target", {}).get("name") == "model_process"
                and message["target"].get("kind") == ["test"]):
            executables.append(v.regular(Path(message["executable"])))
    if len(executables) != 1 or not executables[0].is_relative_to(target):
        raise FixtureError("expected exactly one test executable inside explicit target")
    artifacts = args.output / "artifacts"
    artifacts.mkdir(mode=0o700)
    for name, source in {"lmm-core": binary, "model_process": executables[0]}.items():
        v.copy_binary(source, artifacts / name)
    discovered = v.test_list(artifacts / "model_process", environment, args.output / "test-list.log")
    reviewed_tests(discovered)
    check(frozen)
    if (build["binary_sha256"] != v.sha(binary)
            or build["binary_sha256"] != v.sha(artifacts / "lmm-core")
            or reference_sha != {"binding": v.sha(args.source_binding), "build": v.sha(args.build_receipt)}):
        raise FixtureError("release binary or original build reference changed during prepare")
    receipt = {"format": "lmm-resource-prepared-v1", "inputs": frozen,
               "artifacts": {"artifacts/"+name: v.sha(artifacts/name)
                             for name in ("lmm-core", "model_process")},
               "rustc": version, "test_list": discovered,
               "source_binding_sha256": v.sha(args.source_binding),
               "build_receipt_sha256": v.sha(args.build_receipt)}
    v.record(args.output / "prepared.json", receipt)
    print("PREPARED (not acceptance): " + v.sha(args.output / "prepared.json"))
    return 0


def artifacts_check(output, artifacts):
    names = {"artifacts/lmm-core", "artifacts/model_process"}
    if set(artifacts) != names or {str(p.relative_to(output)) for p in v.tree(output / "artifacts")} != names:
        raise FixtureError("resource artifact collection changed")
    for name, expected in artifacts.items():
        if not isinstance(expected, str) or not v.HEX.fullmatch(expected) or v.sha(v.regular(output / name)) != expected:
            raise FixtureError("resource artifact digest changed")


def cleanup_units(process_root, environment, expected_binary_sha, child):
    observed = []
    for path in sorted(process_root.glob("*/resource-unit-*.json")):
        record = v.read_json(v.regular(path))
        unit = record.get("unit", "")
        if (not re.fullmatch(r"lmm-resource-[0-9]+-[0-9a-f]{16}\.service", unit)
                or record.get("binary_sha256") != expected_binary_sha
                or record.get("owner") != child
                or not unit.startswith("lmm-resource-"+str(child.get("pid"))+"-")):
            raise FixtureError("invalid own-unit allocation receipt; no cleanup replay")
        subprocess.run(["systemctl", "--user", "stop", unit], env=environment,
                       capture_output=True, text=True, timeout=15)
        state = subprocess.run(["systemctl", "--user", "show", unit,
            "--property=MainPID", "--property=ActiveState", "--property=ControlGroup", "--property=LoadState"],
            env=environment, capture_output=True, text=True, timeout=15)
        properties = dict(line.split("=",1) for line in state.stdout.splitlines() if "=" in line)
        if state.returncode:
            identity = path.parent / ("resource-identity-"+str(record["generation"])+".json")
            saved = v.read_json(identity) if identity.exists() else {}
            group = saved.get("cgroup", "")
            ping = subprocess.run(["systemctl", "--user", "show", "--property=Version"],
                env=environment, capture_output=True, text=True, timeout=15)
            if (properties.get("LoadState") == "not-found" and ping.returncode == 0
                    and group.startswith("/sys/fs/cgroup/") and ".." not in Path(group).parts
                    and not Path(group).exists()):
                observed.append({"unit":unit,"state":"garbage-collected","cgroup_removed":True})
                continue
        if state.returncode or properties.get("MainPID") != "0" or properties.get("ActiveState") not in {"inactive","failed"}:
            raise FixtureError("own resource unit stop not confirmed; retain fixture")
        group = properties.get("ControlGroup", "")
        if group and (not group.startswith("/") or ".." in group.split("/")
                      or Path("/sys/fs/cgroup"+group).exists()):
            raise FixtureError("own resource cgroup remains; retain fixture")
        observed.append({"unit":unit, **properties})
    return observed


def execute_test(command, environment, log, timeout):
    started = time.monotonic()
    interrupted, timed_out = False, False
    with log.open("x") as output:
        child = subprocess.Popen(command, cwd=v.REPO / "services/core", env=environment,
            stdout=output, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL,
            start_new_session=True)
        try:
            identity = v.process_identity(child.pid)
        except FileNotFoundError:
            identity = {"pid":child.pid, "generation":"EXITED_BEFORE_OBSERVATION"}
        try:
            code = child.wait(timeout=timeout)
        except (subprocess.TimeoutExpired, KeyboardInterrupt) as stopped:
            interrupted = isinstance(stopped, KeyboardInterrupt)
            timed_out = isinstance(stopped, subprocess.TimeoutExpired)
            try:
                os.killpg(child.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                code = child.wait(timeout=10)
            except subprocess.TimeoutExpired:
                os.killpg(child.pid, signal.SIGKILL)
                code = child.wait()
    receipt = {"child":identity, "exit_code":code, "timed_out":timed_out,
               "interrupted":interrupted,"seconds":time.monotonic()-started,"log":str(log)}
    v.record(log.with_suffix(".log.json"), receipt)
    return receipt


def run(args):
    private_directory(args.output)
    private_directory(args.process_root)
    if len(os.fsencode(args.process_root)) > 35 or any(args.process_root.iterdir()):
        raise FixtureError("process root must be empty, private and at most35 bytes")
    prepared = args.output / "prepared.json"
    if not v.HEX.fullmatch(args.prepared_sha256) or v.sha(v.regular(prepared)) != args.prepared_sha256:
        raise FixtureError("prepared SHA256 mismatch")
    data = v.read_json(prepared)
    command = selected_command(args.output / "artifacts/model_process", args.case)
    case = command[1]
    if data.get("format") != "lmm-resource-prepared-v1":
        raise FixtureError("unsupported resource preparation")
    check(data["inputs"])
    artifacts_check(args.output, data["artifacts"])
    reviewed_tests(data.get("test_list"))
    attempt = args.output / ("run-" + secrets.token_hex(8))
    attempt.mkdir(mode=0o700)
    environment = env()
    environment.update(XDG_RUNTIME_DIR="/run/user/"+str(os.geteuid()),
        DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/"+str(os.geteuid())+"/bus",
        TMPDIR=str(args.process_root), LMM_MODEL_PROCESS_ROOT=str(args.process_root),
        LMM_MODEL_PROCESS_CORE_BIN=str(args.output/"artifacts/lmm-core"),
        LMM_MODEL_PROCESS_CORE_SHA256=data["artifacts"]["artifacts/lmm-core"],
        LMM_RESOURCE_ACCEPTANCE="1")
    fixture = Fixture(attempt)
    fixture.env = {**env(), "DOCKER_CONFIG": fixture.env["DOCKER_CONFIG"]}
    started = time.monotonic()
    result, error, removed = None, None, False
    units = None
    try:
        fixture.start()
        config = dict(line.split("=",1) for line in (fixture.root/"postgres.env").read_text().splitlines())
        environment["DATABASE_URL"] = ("postgresql://a09_admin:"+config["POSTGRES_PASSWORD"]+
            "@127.0.0.1:"+str(fixture.binding["port"])+"/postgres")
        result = execute_test(command, environment,
            attempt/"resource-test.log", 1000)
        if result["interrupted"]:
            raise FixtureError("resource test interrupted; preserve failed fixture")
        v.require_success(result)
        text = (attempt/"resource-test.log").read_text()
        require_selected_success(text, data["test_list"])
        check(data["inputs"])
        artifacts_check(args.output, data["artifacts"])
        if v.sha(prepared) != args.prepared_sha256:
            raise FixtureError("prepared receipt changed during run")
    except (FixtureError, OSError, subprocess.SubprocessError) as caught:
        error = str(caught)
    finally:
        # Cleanup only this fixture after re-verifying ownership; uncertain state is retained.
        try:
            units = cleanup_units(args.process_root, environment, data["artifacts"]["artifacts/lmm-core"],
                                  result["child"] if result else {})
            if fixture.container is not None:
                fixture.verify()
                if error is None:
                    fixture.cleanup()
                    removed = True
        except (FixtureError, OSError, subprocess.SubprocessError) as cleanup_error:
            error = (error or "") + "; fixture cleanup UNKNOWN: " + str(cleanup_error)
    passed = error is None and removed
    v.record(attempt/"result.json", {"result":"PASS" if passed else "FAIL", "error":error,
        "test":result, "cluster_removed":removed, "unit_cleanup":units,
        "failed_fixture_retained":bool(error and fixture.container),
        "fixture_root":str(fixture.root), "prepared_sha256":args.prepared_sha256,
        "seconds":time.monotonic()-started, "process_evidence_root":str(args.process_root),
        "case":args.case, "test_case":case,
        "scope":"Core-only1CPU256MiB; synthetic upstream/credits; fresh ownedPG",
        "not_verified":["whole repository", "Core plus Go", "real providers", "old architecture comparison"]
            + (["slow-consumer socket backpressure"] if args.case == "sustained"
               else ["sustained mixed traffic with slow consumers"]), "not_acceptance_signoff":True})
    print(("PASS" if passed else "FAIL")+": "+str(attempt/"result.json"))
    return 0 if passed else 1


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    modes = parser.add_subparsers(dest="mode", required=True)
    prep = modes.add_parser("prepare")
    for name in ("output","resource","requests","core-binary","source-binding","build-receipt"):
        prep.add_argument("--"+name, type=Path, required=True)
    execute = modes.add_parser("run")
    execute.add_argument("--output", type=Path, required=True)
    execute.add_argument("--process-root", type=Path, required=True)
    execute.add_argument("--prepared-sha256", required=True)
    execute.add_argument("--case", choices=tuple(CASES), default="sustained")
    args = parser.parse_args()
    try:
        return prepare(args) if args.mode == "prepare" else run(args)
    except (FixtureError, OSError, subprocess.SubprocessError) as error:
        print("FAIL: "+str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
