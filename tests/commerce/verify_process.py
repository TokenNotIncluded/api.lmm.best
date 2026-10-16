#!/usr/bin/env python3
"""Run the actual Go Store -> Rust Commerce regression in an owned local cluster.

No production URL, existing database or image pull is accepted. Invoke through
agcc with roots for this repository's outputs and Cargo/Go caches. Private logs,
synthetic credentials and receipts stay in .local-tests, never in Git.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import signal
import shutil
from pathlib import Path
import subprocess
import sys
import time
import threading

REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO / "tests/architecture_completion"))
from build_lease import private_directory
from pg_fixture import Fixture, FixtureError, clean_environment, write_private
from resource_group import Group, ResourceError


def digest(path: Path) -> str:
    with path.open("rb") as stream:
        value = hashlib.file_digest(stream, "sha256")
    return value.hexdigest()


def sources() -> dict:
    roots = [
        REPO / "services/core/src",
        REPO / "services/core/tests",
        REPO / "services/core/schema",
        REPO / "contracts/proto",
        REPO / "services/extensions/internal",
        REPO / "services/extensions/cmd",
        REPO / "tests/commerce",
        REPO / "tests/model_process",
        REPO / "tests/architecture_completion",
    ]
    files = [
        root / name
        for root, names in [
            (REPO / "services/core", ["Cargo.toml", "Cargo.lock", "build.rs"]),
            (REPO / "services/extensions", ["go.mod", "go.sum"]),
            (REPO, ["mise.toml", "rust-toolchain.toml"]),
        ]
        for name in names
    ]
    for root in roots:
        files.extend(
            path
            for path in root.rglob("*")
            if path.is_file()
            and not path.is_symlink()
            and "__pycache__" not in path.parts
        )
    return {
        str(path.relative_to(REPO)): digest(path)
        for path in sorted(files)
        if path.is_file()
    }


def group_exists(pgid: int) -> bool:
    try:
        os.killpg(pgid, 0)
        return True
    except ProcessLookupError:
        return False


def stop_group(process: subprocess.Popen) -> None:
    # start_new_session gives this invocation its own group, including products.
    for sig in (signal.SIGTERM, signal.SIGKILL):
        try:
            os.killpg(process.pid, sig)
        except ProcessLookupError:
            pass
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline:
            process.poll()  # reap the direct child before checking the group
            if not group_exists(process.pid):
                process.wait(timeout=1)
                return
            time.sleep(0.025)
    raise FixtureError("owned command group cleanup unconfirmed; inspect private logs")


def run(command: list[str], cwd: Path, env: dict, output: Path, timeout: int) -> None:
    # Logs can contain driver errors; never forward them to a remote service.
    with output.open("xb") as log:
        os.chmod(output, 0o600)
        process = subprocess.Popen(
            command,
            cwd=cwd,
            env=env,
            stdout=log,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        status = {"pid": process.pid, "pgid": process.pid, "result": "incomplete"}
        try:
            code = process.wait(timeout=timeout)
            if group_exists(process.pid):
                raise FixtureError("command exited but left live descendants")
            status.update(exit_code=code, result="passed" if code == 0 else "failed")
            if code:
                raise FixtureError(f"local check failed ({code}); inspect {output}")
        except BaseException as error:
            status.update(result="failed", error_type=type(error).__name__)
            stop_group(process)
            raise
        finally:
            status["process_group_removed"] = not group_exists(process.pid)
            write_private(
                output.with_suffix(output.suffix + ".result.json"),
                json.dumps(status, sort_keys=True) + "\n",
            )


def interrupted(signum, frame):
    raise KeyboardInterrupt("local acceptance interrupted")


def select_profile(*, mixed_load, mixed_slow_sockets, shared_resources, sealed):
    if mixed_load and mixed_slow_sockets:
        raise FixtureError("--mixed-load and --mixed-slow-sockets are mutually exclusive")
    profile = "socket-pressure" if mixed_slow_sockets else "sustained" if mixed_load else None
    if profile and not shared_resources:
        raise FixtureError("mixed profiles require --shared-resources")
    if shared_resources and not sealed:
        raise FixtureError("--shared-resources requires all three sealed executable/SHA pairs")
    return profile


# Independent parent check prevents an older sustained binary from passing this profile.
# @mark commerce.socket-pressure role=verification

def verify_joint_socket_result(mixed, result, offered, recovery):
    def require(condition, message):
        if not condition:
            raise FixtureError("joint socket evidence: " + message)

    def exact(obj, key, expected):
        require(type(obj.get(key)) is type(expected) and obj[key] == expected, key)

    for obj in (mixed, result):
        require(type(obj) is dict, "result object")
        for key, expected in (("result", "PASS"), ("profile", "socket-pressure"),
                              ("commerce_complete_refunded", 180), ("unknown_holds", 120),
                              ("full_project_complete", False)):
            exact(obj, key, expected)
    exact(mixed, "model_successes", 900)
    for key, expected in (("slow_offered", 120), ("slow_accepted", 120),
                          ("retained_at_recovery", 120), ("arrival_period_ms", 500),
                          ("all_120_nonzero_sendq_witness", True),
                          ("retained_recovery_nonzero_sendq", 0),
                          ("clients_dropped_before_recovery", False),
                          ("replayed_original_keys", 120), ("provider_hits_before", 1020),
                          ("provider_hits_after", 1020), ("model_complete", 900)):
        exact(result, key, expected)
    duration = result.get("minimum_duration_ms")
    elapsed = recovery.get("elapsed_ms")
    require(type(duration) is int and duration >= 240000, "duration")
    require(type(elapsed) is int and elapsed >= 180000 and duration >= elapsed + 60000,
            "retained recovery and idle duration")
    for obj, values in ((offered, {"offered": 120, "accepted": 120}),
                        (recovery, {"retained_clients": 120, "clients_dropped": False,
                                    "nonzero_send_queue": 0})):
        for key, expected in values.items():
            exact(obj, key, expected)
    peers = offered.get("peers")
    require(type(peers) is list and len(peers) == 120, "offered peer count")
    by_id = {}
    tuples = set()
    for peer in peers:
        require(type(peer) is dict, "peer object")
        request_id = peer.get("id")
        require(type(request_id) is str and len(request_id) == 64 and
                all(c in "0123456789abcdefABCDEF" for c in request_id), "request id")
        require(request_id not in by_id, "duplicate request id")
        require(all(type(peer.get(k)) is str and peer[k] for k in ("server", "client", "label")),
                "peer binding")
        pair = (peer["server"], peer["client"])
        require(pair not in tuples, "duplicate socket tuple")
        tuples.add(pair)
        by_id[request_id] = peer
    proved = result.get("proved_ids")
    require(type(proved) is list and all(type(x) is str for x in proved) and
            len(proved) == 120 and len(set(proved)) == 120 and set(proved) == set(by_id),
            "all offered peers must have pressure witnesses")
    recovered = recovery.get("peers")
    require(type(recovered) is list and len(recovered) == 120, "recovery peer count")
    seen = set()
    for peer in recovered:
        request_id = peer.get("request_id")
        require(type(request_id) is str and request_id in by_id and request_id not in seen,
                "recovery request binding")
        seen.add(request_id)
        original = by_id[request_id]
        require(all(peer.get(k) == original[k] for k in ("server", "client", "label")),
                "recovery socket binding")
        require(type(peer.get("socket")) is dict, "recovery socket observation")
        exact(peer["socket"], "send_queue", 0)


    witnesses = recovery.get("retention_witnesses")
    require(type(witnesses) is list and len(witnesses) == 120, "retention witnesses")
    seen = set()
    socket_ids = set()
    for witness in witnesses:
        request_id = witness.get("request_id")
        require(type(request_id) is str and request_id in by_id and request_id not in seen,
                "retention request binding")
        seen.add(request_id)
        exact(witness, "same_open_fd", True)
        for key in ("fd", "device", "inode"):
            require(type(witness.get(key)) is int and witness[key] >= (1 if key == "inode" else 0),
                    "retained socket identity")
        identity = (witness["device"], witness["inode"])
        require(identity not in socket_ids, "duplicate retained socket identity")
        socket_ids.add(identity)
        original = by_id[request_id]
        require(witness.get("original_server") == original["server"] and
                witness.get("original_client") == original["client"], "retained tuple")
        status = witness.get("peer_status")
        require(type(status) is dict, "retained peer status")
        if status.get("state") == "connected":
            exact(status, "peer", original["server"])
        else:
            exact(status, "state", "reset-or-disconnected-fd-still-open")
            require(type(status.get("errno")) is int and status["errno"] in (107, 104),
                    "retained reset status")


def main() -> int:
    signal.signal(signal.SIGTERM, interrupted)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--output-root",
        type=Path,
        required=True,
        help="absolute bounded directory under this repo/.local-tests",
    )
    parser.add_argument(
        "--process-root",
        type=Path,
        required=True,
        help="short absolute private directory for Unix sockets and process logs",
    )
    parser.add_argument(
        "--rust-toolchain",
        help="explicit local fallback only; absent uses the repository pin",
    )
    parser.add_argument(
        "--core-binary",
        type=Path,
        help="sealed local Core executable, paired with --core-sha256",
    )
    parser.add_argument("--core-sha256", help="expected SHA256 of --core-binary")
    parser.add_argument(
        "--test-binary",
        type=Path,
        help="previously compiled commerce_process test, paired with --test-sha256",
    )
    parser.add_argument("--test-sha256", help="expected SHA256 of --test-binary")
    parser.add_argument(
        "--extension-binary",
        type=Path,
        help="sealed Go executable, paired with --extension-sha256",
    )
    parser.add_argument(
        "--extension-sha256", help="expected SHA256 of --extension-binary"
    )
    parser.add_argument(
        "--shared-resources",
        action="store_true",
        help="run the two real products under a shared 1CPU/512MiB parent",
    )
    parser.add_argument(
        "--mixed-load",
        action="store_true",
        help="run frozen 540s model and complete commerce traffic together",
    )
    parser.add_argument(
        "--mixed-slow-sockets", action="store_true",
        help="run separate 240s model/commerce gate with 120 retained unread TCP clients",
    )
    args = parser.parse_args()
    try:
        profile = select_profile(
            mixed_load=args.mixed_load, mixed_slow_sockets=args.mixed_slow_sockets,
            shared_resources=args.shared_resources,
            sealed=bool(args.core_binary and args.test_binary and args.extension_binary),
        )
    except FixtureError as error:
        parser.error(str(error))
    if bool(args.core_binary) != bool(args.core_sha256):
        parser.error("--core-binary and --core-sha256 must be supplied together")
    if args.core_binary and (
        not args.core_binary.is_absolute()
        or not args.core_binary.is_file()
        or digest(args.core_binary) != args.core_sha256
    ):
        parser.error("Core executable does not match the declared frozen input")
    if bool(args.test_binary) != bool(args.test_sha256):
        parser.error("--test-binary and --test-sha256 must be supplied together")
    if args.test_binary and (
        not args.test_binary.is_absolute()
        or not args.test_binary.is_file()
        or digest(args.test_binary) != args.test_sha256
    ):
        parser.error("test executable does not match the declared frozen input")
    if bool(args.extension_binary) != bool(args.extension_sha256):
        parser.error(
            "--extension-binary and --extension-sha256 must be supplied together"
        )
    if args.extension_binary and (
        not args.extension_binary.is_absolute()
        or not args.extension_binary.is_file()
        or digest(args.extension_binary) != args.extension_sha256
    ):
        parser.error("Go executable does not match the declared frozen input")
    if not args.output_root.is_absolute():
        parser.error("output root must be absolute")
    output = args.output_root.resolve()
    if not output.is_relative_to(REPO / ".local-tests"):
        parser.error("output must be inside this repo/.local-tests")
    private_directory(output)
    process_root = args.process_root
    private_directory(process_root)
    if len(os.fsencode(process_root)) > 35:
        parser.error("process root must be short enough for the Unix socket limit")
    env = clean_environment()
    # Avoid inherited product settings and unrelated test endpoints/credentials.
    env = {
        key: value
        for key, value in env.items()
        if not key.startswith(("LMM_", "SQLX_", "RUSTUP_TOOLCHAIN"))
    }
    if args.rust_toolchain:
        env["RUSTUP_TOOLCHAIN"] = args.rust_toolchain
    before = sources()
    fixture = Fixture(output)
    try:
        binary = fixture.root / "lmm-extensions"
        if args.extension_binary:
            shutil.copyfile(args.extension_binary, binary)
            binary.chmod(0o500)
            if digest(binary) != args.extension_sha256:
                raise FixtureError("sealed Go copy differs from frozen input")
        else:
            # `mise exec` may auto-install unrelated configured tools. Resolve the
            # existing pinned Go tool only; a missing installation is a blocker.
            go_home = subprocess.check_output(
                ["mise", "where", "go"], cwd=REPO, env=env, text=True
            ).strip()
            go = Path(go_home) / "bin/go"
            if not go.is_absolute() or not go.is_file():
                raise FixtureError("repository-pinned Go installation is unavailable")
            run(
                [str(go), "build", "-o", str(binary), "./cmd/extensions"],
                REPO / "services/extensions",
                env,
                fixture.root / "go-build.log",
                300,
            )
        fixture.start()
        # Only this freshly created, identity-checked loopback cluster is passed
        # to SQLx. No externally supplied DATABASE_URL is ever consumed.
        config = dict(
            line.split("=", 1)
            for line in (fixture.root / "postgres.env").read_text().splitlines()
        )
        env["DATABASE_URL"] = (
            f"postgresql://a09_admin:{config['POSTGRES_PASSWORD']}"
            f"@127.0.0.1:{fixture.binding['port']}/postgres"
        )
        env["LMM_RELAY_EXTENSION_BIN"] = str(binary)
        extension_sha256 = digest(binary)
        env["LMM_RELAY_EXTENSION_SHA256"] = extension_sha256
        if args.core_binary:
            env["LMM_COMMERCE_CORE_BIN"] = str(args.core_binary)
            env["LMM_COMMERCE_CORE_SHA256"] = args.core_sha256
        if profile:
            env["LMM_COMMERCE_MIXED"] = "1"
            env["LMM_COMMERCE_MIXED_PROFILE"] = profile
        env["TMPDIR"] = str(process_root)
        env["CARGO_BUILD_JOBS"] = "2"
        started = time.monotonic()
        command = (
            [str(args.test_binary)]
            if args.test_binary
            else ["cargo", "test", "--locked", "--test", "commerce_process", "--"]
        )
        resources = None
        sampler = None
        sampling_stop = threading.Event()
        samples = []
        sampling_errors = []

        def observe():
            try:
                while not sampling_stop.is_set():
                    samples.append(resources.sample())
                    sampling_stop.wait(0.25)
            except Exception as error:
                sampling_errors.append(type(error).__name__ + ": " + str(error))

        try:
            if args.shared_resources:
                resource_root = fixture.root / "resources"
                resource_root.mkdir(mode=0o700)
                resources = Group(resource_root)
                resources.start()
                sampler = threading.Thread(
                    target=observe, name="joint-resource-observer"
                )
                sampler.start()
                env["LMM_COMMERCE_RESOURCE_READY"] = str(
                    resource_root / "group-ready.json"
                )
                env["LMM_COMMERCE_RESOURCE_HELPER"] = str(
                    REPO / "tests/commerce/resource_scope.py"
                )
            run(
                [
                    *command,
                    "real_core_and_go_store_replay_refund_and_restart_after_lost_ack",
                    "--exact",
                    "--nocapture",
                    "--test-threads=1",
                ],
                REPO / "services/core",
                env,
                fixture.root / "commerce-test.log",
                600 if profile == "socket-pressure" else 900,
            )
            if resources:
                resources.adopt_scopes()
                allocations = list(resource_root.glob("scope-*-allocation.json"))
                ready = list(resource_root.glob("scope-*-ready.json"))
                roles = [json.loads(path.read_text())["role"] for path in allocations]
                if (
                    len(ready) != 4
                    or len(resources.services) != 4
                    or sorted(roles) != ["core", "core", "extensions", "extensions"]
                ):
                    raise FixtureError(
                        "shared resource test must bind all four product starts"
                    )
        finally:
            if resources:
                sampling_stop.set()
                if sampler:
                    sampler.join(timeout=5)
                try:
                    if sampler and sampler.is_alive():
                        raise FixtureError("resource observer did not stop")
                    if resources.group is not None:
                        samples.append(resources.sample())
                    write_private(
                        resource_root / "samples.json",
                        json.dumps(
                            {
                                "scope": "short real commerce regression, not sustained-load acceptance",
                                "samples": samples,
                                "errors": sampling_errors,
                            },
                            sort_keys=True,
                        )
                        + "\n",
                    )
                    if sampling_errors:
                        raise FixtureError("resource observations incomplete")
                    if samples and any(
                        samples[-1]["parent"]["memory_events"].get(key, 0)
                        for key in ["oom", "oom_kill", "max"]
                    ):
                        raise FixtureError(
                            "memory hard-limit event during commerce regression"
                        )
                finally:
                    resources.close()
        if (
            "test result: ok. 1 passed; 0 failed; 0 ignored;"
            not in (fixture.root / "commerce-test.log").read_text()
        ):
            raise FixtureError("expected real commerce case did not pass exactly once")
        if args.test_binary and digest(args.test_binary) != args.test_sha256:
            raise FixtureError("frozen test executable changed during acceptance")
        if args.core_binary and digest(args.core_binary) != args.core_sha256:
            raise FixtureError("frozen Core executable changed during acceptance")
        if digest(binary) != extension_sha256:
            raise FixtureError("frozen Go executable changed during acceptance")
        if sources() != before:
            raise FixtureError(
                "tested sources changed during acceptance; result not frozen"
            )
        if profile:
            results = list(
                process_root.glob("lmm-commerce-process-*/mixed-result.json")
            )
            if (
                len(results) != 1
                or json.loads(results[0].read_text()).get("result") != "PASS"
            ):
                raise FixtureError(
                    "frozen mixed load did not produce its own final result"
                )
            if profile == "socket-pressure":
                evidence_root = results[0].parent
                try:
                    evidence = [json.loads((evidence_root / name).read_text()) for name in (
                        "joint-socket-result.json", "joint-socket-offered.json",
                        "joint-socket-retained-recovery.json")]
                    verify_joint_socket_result(json.loads(results[0].read_text()), *evidence)
                except (OSError, ValueError, KeyError, TypeError) as error:
                    raise FixtureError("joint socket proof missing or malformed") from error
            observed = json.loads((resource_root / "samples.json").read_text())
            times = [sample["monotonic"] for sample in observed["samples"]]
            if (
                len(times) < 2
                or times[-1] - times[0] < (240 if profile == "socket-pressure" else 540)
                or max(b - a for a, b in zip(times, times[1:])) > 2.5
            ):
                raise FixtureError("mixed sampling missed full frozen interval")
        fixture.cleanup()
        receipt = {
            "format": "lmm-commerce-process-acceptance-v1",
            "result": "PASS",
            "source_sha256": before,
            "extension_sha256": extension_sha256,
            "rust_toolchain_override": args.rust_toolchain,
            "external_extension_sha256": args.extension_sha256,
            "external_test_sha256": args.test_sha256,
            "external_core_sha256": args.core_sha256,
            "external_core_binary": str(args.core_binary) if args.core_binary else None,
            "process_evidence_root": str(process_root),
            "seconds": round(time.monotonic() - started, 2),
            "cluster_removed": True,
            "mixed_load": args.mixed_load,
            "mixed_slow_sockets": args.mixed_slow_sockets,
            "mixed_profile": profile,
            "mixed_result": str(results[0]) if profile else None,
            "shared_resources": args.shared_resources,
            "shared_resource_receipt": str(resource_root / "group-closed.json")
            if resources
            else None,
            "scope": "synthetic credits; local product processes only",
        }
        write_private(
            fixture.root / "acceptance.json", json.dumps(receipt, sort_keys=True) + "\n"
        )
        print(
            f"PASS: real Store/Commerce processes; receipt {fixture.root / 'acceptance.json'}"
        )
        return 0
    except (FixtureError, ResourceError, OSError, subprocess.SubprocessError) as error:
        # No optimistic cleanup/retry after an uncertain or failed check. The
        # fixture's immutable identity and local private logs remain inspectable.
        print(f"FAIL: {error}; retained local evidence {fixture.root}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
