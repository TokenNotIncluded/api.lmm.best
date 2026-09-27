#!/usr/bin/env python3
"""Compare already-running local backend processes on identical successful reads.

Build both optimized binaries first, and run without concurrent compilation.
The report describes these endpoint workloads only, not overall backend parity.
"""

import argparse
import hashlib
import http.client
import json
import os
from pathlib import Path
import platform
import subprocess
import tempfile
import threading
import time
from urllib.parse import urlsplit


def process_sample(pid, identity=None):
    root = Path(f"/proc/{pid}")
    stat = (root / "stat").read_text().rsplit(")", 1)[1].split()
    start_ticks = int(stat[19])
    if identity is not None and identity != start_ticks:
        raise RuntimeError(f"PID {pid} was recycled during the benchmark")
    values = {}
    for line in (root / "status").read_text().splitlines():
        key, _, value = line.partition(":")
        if key in ("VmRSS", "VmHWM", "VmSwap"):
            values[key] = int(value.split()[0]) * 1024
        elif key == "Threads":
            values[key] = int(value.strip())
    proportional = {}
    try:
        for line in (root / "smaps_rollup").read_text().splitlines():
            key, _, value = line.partition(":")
            if key in ("Pss", "Private_Clean", "Private_Dirty"):
                proportional[key] = int(value.split()[0]) * 1024
    except OSError:
        pass
    return {
        "start_ticks": start_ticks, "rss_bytes": values.get("VmRSS", 0),
        "lifetime_peak_rss_bytes": values.get("VmHWM", 0),
        "swap_bytes": values.get("VmSwap", 0), "threads": values.get("Threads", 0),
        "cpu_seconds": (int(stat[11]) + int(stat[12])) / os.sysconf("SC_CLK_TCK"),
        "pss_bytes": proportional.get("Pss"),
        "private_bytes": (proportional.get("Private_Clean", 0) + proportional.get("Private_Dirty", 0)) if proportional else None,
    }


def get_json(base, path):
    url = urlsplit(base)
    if url.scheme != "http" or url.hostname not in ("127.0.0.1", "::1") or url.username:
        raise ValueError("benchmark URLs must use literal loopback HTTP addresses")
    connection = http.client.HTTPConnection(url.hostname, url.port, timeout=10)
    try:
        connection.request("GET", path)
        response = connection.getresponse()
        raw = response.read(4 * 1024 * 1024 + 1)
        if len(raw) > 4 * 1024 * 1024:
            raise RuntimeError("response exceeds the benchmark's 4 MiB response budget")
        value = json.loads(raw)
        if response.status != 200 or not isinstance(value, dict) or value.get("success") is not True:
            raise RuntimeError(f"{base}{path} did not return a successful API response")
        return value
    finally:
        connection.close()


def run_load(driver, base, pid, identity, path, expected, count, concurrency,
             request_file=None, headers_file=None):
    before = process_sample(pid, identity)
    samples = [before]
    sampler_errors = []
    finished = threading.Event()

    def sample():
        while not finished.wait(.01):
            try:
                samples.append(process_sample(pid, identity))
            except (OSError, RuntimeError) as error:
                sampler_errors.append(str(error))
                break

    sampler = threading.Thread(target=sample, daemon=True)
    sampler.start()
    try:
        command = [str(driver), "-url", base + path, "-requests", str(count),
                   "-concurrency", str(concurrency), "-expected-json", str(expected)]
        if request_file is not None:
            command.extend(["-request-json", str(request_file)])
        if headers_file is not None:
            command.extend(["-headers-json", str(headers_file)])
        result = subprocess.run(
            command,
            capture_output=True, text=True, timeout=max(60, count * 15 / concurrency),
        )
    finally:
        finished.set()
        sampler.join()
    after = process_sample(pid, identity)
    samples.append(after)
    if sampler_errors:
        raise RuntimeError(sampler_errors[0])
    if not result.stdout.strip():
        raise RuntimeError(f"load driver exited {result.returncode}: {result.stderr[:500]}")
    report = json.loads(result.stdout)
    report.update({
        "valid": result.returncode == 0 and report["successes"] == count,
        "process_cpu_seconds": after["cpu_seconds"] - before["cpu_seconds"],
        "rss_before_bytes": before["rss_bytes"], "rss_after_bytes": after["rss_bytes"],
        "sampled_peak_rss_bytes": max(s["rss_bytes"] for s in samples),
        "lifetime_peak_rss_bytes": after["lifetime_peak_rss_bytes"],
        "swap_bytes": max(s["swap_bytes"] for s in samples),
        "threads": after["threads"],
        "pss_after_bytes": after["pss_bytes"], "private_after_bytes": after["private_bytes"],
    })
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for backend in ("go", "rust"):
        parser.add_argument(f"--{backend}-url", required=True)
        parser.add_argument(f"--{backend}-pid", type=int, required=True)
    parser.add_argument("--driver", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--requests", type=int, default=10000)
    parser.add_argument("--rounds", type=int, default=3)
    parser.add_argument("--concurrency", type=int, nargs="+", default=[1, 8, 32])
    parser.add_argument("--paths", nargs="+", default=["/api/livez", "/api/about", "/api/notice"])
    args = parser.parse_args()
    if args.requests < 1 or args.rounds < 1 or min(args.concurrency) < 1:
        parser.error("requests, rounds and concurrency must be positive")
    backends = {}
    for name in ("go", "rust"):
        pid = getattr(args, f"{name}_pid")
        executable = Path(f"/proc/{pid}/exe")
        backends[name] = {
            "url": getattr(args, f"{name}_url").rstrip("/"), "pid": pid,
            "binary": str(executable.resolve()),
            "binary_sha256": hashlib.sha256(executable.read_bytes()).hexdigest(),
            "idle": process_sample(pid),
        }
    report = {
        "scope": "loopback HTTP reads with identical verified successful JSON bodies",
        "limitations": ["excludes upstream AI latency, relay throughput, payment and write workloads",
                        "RSS is backend process memory and excludes PostgreSQL, Valkey, and the load driver",
                        "closed-loop load; latency percentiles describe completed successful requests",
                        "sampled RSS can miss sub-10ms peaks; lifetime VmHWM is reported separately"],
        "host": {"platform": platform.platform(), "cpu_count": os.cpu_count(),
                 "affinity": sorted(os.sched_getaffinity(0)), "load_average": os.getloadavg()},
        "backends": backends, "runs": [], "body_differentials": [], "valid": False,
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    with tempfile.TemporaryDirectory(prefix="lmm-perf-") as directory:
        for path in args.paths:
            bodies = {name: get_json(backend["url"], path) for name, backend in backends.items()}
            if bodies["go"] != bodies["rust"]:
                report["body_differentials"].append({"path": path, "go": bodies["go"], "rust": bodies["rust"]})
                continue
            expected = Path(directory) / "expected.json"
            expected.write_text(json.dumps(bodies["go"]))
            for concurrency in args.concurrency:
                for name, backend in backends.items():
                    warmup = run_load(args.driver, backend["url"], backend["pid"], backend["idle"]["start_ticks"],
                                      path, expected, min(1000, args.requests), concurrency)
                    if not warmup["valid"]:
                        raise RuntimeError(f"{name} {path} warmup failed: {warmup['errors']}")
                for round_index in range(args.rounds):
                    order = ["go", "rust"] if round_index % 2 == 0 else ["rust", "go"]
                    for name in order:
                        backend = backends[name]
                        result = run_load(args.driver, backend["url"], backend["pid"], backend["idle"]["start_ticks"],
                                          path, expected, args.requests, concurrency)
                        result.update({"backend": name, "path": path, "round": round_index + 1})
                        report["runs"].append(result)
                        args.output.write_text(json.dumps(report, indent=2) + "\n")
                        print(f"{name} {path} c={concurrency} round={round_index + 1}: "
                              f"{result['successful_requests_per_second']:.0f} req/s, "
                              f"p95={result['latency_ms']['p95']:.3f} ms, "
                              f"RSS={result['sampled_peak_rss_bytes'] / 1048576:.1f} MiB, "
                              f"valid={result['valid']}", flush=True)
    report["valid"] = bool(report["runs"]) and not report["body_differentials"] and all(run["valid"] for run in report["runs"])
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    return 0 if report["valid"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
