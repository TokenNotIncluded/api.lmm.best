#!/usr/bin/env python3
"""Run one source-pinned admission component probe, never the main application.

No target URL, credentials, production settings, downloads or CI are accepted.
Linux, Python 3.10+ and an already installed Go 1.21+ are required. A successful
exit is ONLY a component result. main_acceptance remains NOT_RUN.
"""
from __future__ import annotations

import argparse
import csv
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import resource
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from datetime import datetime, timezone

BASELINE = "2f4164978cf27f6e0e605b0589fbe631aef2c441"
SOURCE_BLOB = "042c060436fd7192fb458824829a79dd1f1ea598"
LIMITS = {
    "wall_seconds": 12, "cpu_seconds_hard": 10, "cpu_affinity_count": 1,
    "address_space_bytes_hard": 2 << 30, "open_files_hard": 64,
    "rss_bytes_safety_stop": 128 << 20, "sample_seconds": 0.05,
    "requests_max": 16, "simultaneous_client_connections_max": 4,
    "request_wire_bytes_total_max": 16 << 20, "body_bytes_per_request_max": 4 << 20,
    "socket_deadline_seconds": 3, "output_file_bytes_hard": 1 << 20,
}


def blob(data: bytes) -> str:
    return hashlib.sha1(b"blob " + str(len(data)).encode() + b"\0" + data).hexdigest()


def child_limits() -> None:
    """Called before exec, in a single-threaded runner; no elevation needed."""
    for key, value in (
        (resource.RLIMIT_AS, LIMITS["address_space_bytes_hard"]),
        (resource.RLIMIT_CPU, LIMITS["cpu_seconds_hard"]),
        (resource.RLIMIT_NOFILE, LIMITS["open_files_hard"]),
        (resource.RLIMIT_FSIZE, LIMITS["output_file_bytes_hard"]),
        (resource.RLIMIT_CORE, 0),
    ):
        resource.setrlimit(key, (value, value))
    os.sched_setaffinity(0, {min(os.sched_getaffinity(0))})


def sample(pid: int, start: float) -> dict | None:
    try:
        root = Path(f"/proc/{pid}")
        status = {}
        for line in (root / "status").read_text().splitlines():
            name, _, value = line.partition(":")
            if name in {"VmRSS", "VmHWM", "VmSize", "Threads"}:
                status[name] = int(value.split()[0])
        descriptors = list((root / "fd").iterdir())
        sockets = 0
        for path in descriptors:
            try:
                sockets += os.readlink(path).startswith("socket:")
            except FileNotFoundError:
                pass
        # Fields after ')' start at Linux stat field 3 (state).
        stats = (root / "stat").read_text().rsplit(")", 1)[1].split()
        cpu_seconds = (int(stats[11]) + int(stats[12])) / os.sysconf("SC_CLK_TCK")
        return {"ms": round((time.monotonic()-start)*1000, 3),
                "rss_bytes": status.get("VmRSS", 0)*1024,
                "hwm_bytes": status.get("VmHWM", 0)*1024,
                "virtual_bytes": status.get("VmSize", 0)*1024,
                "threads": status.get("Threads", 0),
                "fd_count": len(descriptors), "socket_fd_count": sockets,
                "cpu_seconds": cpu_seconds}
    except (FileNotFoundError, ProcessLookupError):
        return None


def kill_group(process: subprocess.Popen) -> None:
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass


def run(root: Path, output: Path) -> int:
    if sys.platform != "linux" or not hasattr(os, "sched_setaffinity"):
        raise RuntimeError("Linux resource limits and /proc are required")
    package = root / "apps/api-go/pkg/admission"
    source = package / "large_requests.go"
    test = package / "resource_bounds_process_test.go"
    data = source.read_bytes()
    if blob(data) != SOURCE_BLOB:
        raise RuntimeError("baseline source mismatch; review the new source before updating the pin")
    if not test.is_file():
        raise RuntimeError("missing RT20 test file")
    go = shutil.which("go")
    if go is None:
        raise RuntimeError("Go is not installed; this runner does not download tools")
    output.mkdir(parents=True, exist_ok=False)
    version = subprocess.check_output([go, "version"], text=True, timeout=5,
                                     env={"PATH": os.environ.get("PATH", ""), "GOTOOLCHAIN": "local"}).strip()
    manifest = {"baseline": BASELINE, "source_blob": SOURCE_BLOB,
                "test_sha256": hashlib.sha256(test.read_bytes()).hexdigest(),
                "runner_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                "started_utc": datetime.now(timezone.utc).isoformat(),
                "go_version": version, "platform": platform.platform(),
                "scope": "admission_component_not_main", "limits": LIMITS,
                "main_acceptance": "NOT_RUN", "funds_delta": None,
                "funds_status": "not_measured_no_database", "application_queue": None,
                "authenticated_account_fairness": "NOT_RUN"}
    with tempfile.TemporaryDirectory(prefix="rt20-") as temporary:
        work = Path(temporary)
        shutil.copy2(source, work / source.name)
        shutil.copy2(test, work / test.name)
        binary = work / "rt20.test"
        cache = Path(os.environ.get("GOCACHE", str(Path.home() / ".cache/go-build")))
        cache.mkdir(parents=True, exist_ok=True)
        env = {"PATH": os.environ.get("PATH", ""), "HOME": str(work),
               "GOCACHE": str(cache), "GOTOOLCHAIN": "local", "GO111MODULE": "off",
               "GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off",
               "CGO_ENABLED": "0", "GOMAXPROCS": "1", "GOMEMLIMIT": "64MiB"}
        build_command = [go, "test", "-c", "-p=1", "-tags=rt20", "-o", str(binary), "."]
        manifest["build_command"] = build_command
        build = subprocess.run(build_command, cwd=work, env=env, text=True,
                               stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=60)
        (output / "build.log").write_text(build.stdout)
        if build.returncode:
            manifest["component_status"] = "BUILD_FAILED"
            manifest["build_returncode"] = build.returncode
            (output / "manifest.json").write_text(json.dumps(manifest, indent=2)+"\n")
            return 1
        env["RT20_BOUNDED_RUNNER"] = "1"
        command = [str(binary), "-test.v", "-test.run=^TestRT20ProcessRecovery$", "-test.count=1", "-test.timeout=11s"]
        manifest["test_command"] = command
        points, stop_reason = [], None
        with (output / "process.log").open("w") as log:
            start = time.monotonic()
            process = subprocess.Popen(command, cwd=work, env=env, stdout=log,
                                       stderr=subprocess.STDOUT, start_new_session=True,
                                       preexec_fn=child_limits)
            try:
                while process.poll() is None:
                    point = sample(process.pid, start)
                    if point:
                        points.append(point)
                        if point["rss_bytes"] >= LIMITS["rss_bytes_safety_stop"]:
                            stop_reason = "rss_safety_stop"
                    if time.monotonic()-start >= LIMITS["wall_seconds"]:
                        stop_reason = "wall_limit"
                    if stop_reason:
                        kill_group(process)
                        break
                    time.sleep(LIMITS["sample_seconds"])
                code = process.wait(timeout=2)
            finally:
                kill_group(process)
                process.wait(timeout=2)
        manifest.update({"process_returncode": code, "stop_reason": stop_reason,
                         "elapsed_seconds": round(time.monotonic()-start, 6)})
    with (output / "process-curve.csv").open("w", newline="") as handle:
        columns = ["ms", "rss_bytes", "hwm_bytes", "virtual_bytes", "threads", "fd_count", "socket_fd_count", "cpu_seconds"]
        writer = csv.DictWriter(handle, fieldnames=columns)
        writer.writeheader(); writer.writerows(points)
    events = []
    for line in (output / "process.log").read_text().splitlines():
        match = re.search(r"RT20 (\{.*\})$", line)
        if match:
            events.append(json.loads(match.group(1)))
    (output / "events.json").write_text(json.dumps(events, indent=2)+"\n")
    results = [event for event in events if event["kind"] == "result"]
    passed = code == 0 and stop_reason is None and len(results) == 1 and results[0].get("component_pass") is True
    manifest["component_status"] = "PASS" if passed else "FAIL"
    manifest["observed"] = results[0] if results else None
    manifest["sampled_peak_rss_bytes"] = max((point["rss_bytes"] for point in points), default=None)
    manifest["sampled_peak_fd_count"] = max((point["fd_count"] for point in points), default=None)
    (output / "manifest.json").write_text(json.dumps(manifest, indent=2)+"\n")
    print(json.dumps(manifest, indent=2))
    return 0 if passed else 1


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--out", type=Path, required=True, help="new local evidence directory")
    args = parser.parse_args()
    try:
        return run(args.root.resolve(), args.out.resolve())
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        print(f"RT20 BLOCKED: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
