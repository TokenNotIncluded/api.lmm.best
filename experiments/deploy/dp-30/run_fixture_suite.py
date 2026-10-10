#!/usr/bin/env python3
"""Three repetitions of each probe case, with all raw results retained.

This launches only the small Python fixture, never the application. It is NOT
an update benchmark, a 1c1g test, a real ledger test, or a node fault experiment.
"""
from __future__ import annotations

import argparse
import collections
from dataclasses import asdict
import datetime
import hashlib
import os
from pathlib import Path
import resource
import sys
import threading
import time

from dp30 import BASE_SHA, percentile, write_json
from workload import Limits, run_load
sys.path.insert(0, str(Path(__file__).parent / "tests"))
from fixture_server import CHUNKS, fixture


def enforce_process_limits() -> dict:
    # RLIMIT_AS is address space, NOT RSS/cgroup RAM. CPU affinity is NOT a quota.
    address_space_limit = 768 * 1024 * 1024
    resource.setrlimit(resource.RLIMIT_AS, (address_space_limit, address_space_limit))
    resource.setrlimit(resource.RLIMIT_CPU, (15, 15))
    resource.setrlimit(resource.RLIMIT_FSIZE, (16 * 1024 * 1024, 16 * 1024 * 1024))
    soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)
    resource.setrlimit(resource.RLIMIT_NOFILE, (min(128, soft), hard))
    threading.stack_size(262144)
    if hasattr(os, "sched_setaffinity"):
        os.sched_setaffinity(0, {min(os.sched_getaffinity(0))})
    return {"rlimit_address_space_bytes": address_space_limit, "rlimit_cpu_time_seconds": 15,
            "rlimit_file_bytes": 16777216, "fd_soft_limit": min(128, soft),
            "cpu_affinity_count": len(os.sched_getaffinity(0)), "cgroup_1c1g": False,
            "note": "fixture, client and observers share ONE process; no application is present"}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    if args.output.exists():
        parser.error("output exists; never overwrite a prior run")
    args.output.mkdir(parents=True)
    limits_observed = enforce_process_limits()
    start = time.monotonic()
    before = resource.getrusage(resource.RUSAGE_SELF)
    modes = (("clean", 16, "complete"), ("truncated", 8, "error"), ("disconnect", 8, "error"),
             ("reordered", 8, "content_mismatch"), ("duplicated", 8, "content_mismatch"), ("stall", 4, "error"))
    summaries = []
    for repeat in range(1, 4):
        # Alternate comparison order instead of always making clean the first run.
        ordered = modes if repeat % 2 else tuple(reversed(modes))
        for mode, count, expected in ordered:
            run_id = f"fixture-{mode}-r{repeat}"
            run_dir = args.output / run_id
            run_dir.mkdir()
            limits = Limits(requests=count, concurrency=4, requests_per_second=40,
                            deadline_seconds=.08 if mode == "stall" else 2, max_seconds=5)
            with fixture(mode) as (origin, permit, receipts):
                result = run_load(origin, permit, run_id, limits, CHUNKS)
                write_json(run_dir / "probe.json", result)
                write_json(run_dir / "fixture-upstream-receipts.json", {"scope": "python_fixture_only", "receipts": receipts})
                requests = result["requests"]
                observed = collections.Counter(r["outcome"] for r in requests)
                matched = len(requests) == count and observed == {expected: count}
                once = len(receipts) == count and len({r["request_id"] for r in receipts}) == count
                latencies = [r["latency_ms"] for r in requests]
                summary = {"run_id": run_id, "mode": mode, "repeat": repeat,
                           "expected_outcome": expected, "outcomes": dict(observed),
                           "offered_requests": count, "fixture_upstream_calls": len(receipts),
                           "no_automatic_replay_observed": once, "peak_active": result["peak_active"],
                           "elapsed_seconds": result["elapsed_seconds"], "latency_samples": len(latencies),
                           "latency_p50_ms": percentile(latencies, .5), "latency_p99_ms": percentile(latencies, .99),
                           "limits": asdict(limits), "passed": matched and once and result["peak_active"] <= limits.concurrency}
                write_json(run_dir / "result.json", summary)
                summaries.append(summary)
    # Let bounded late fixture handlers finish before final thread/FD inventory.
    time.sleep(.35)
    after = resource.getrusage(resource.RUSAGE_SELF)
    report = {"schema_version": 1, "scope": "probe_fixture_regression_only",
              "captured_at_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "audited_application_base_not_executed": BASE_SHA,
              "harness_sha256": {name: hashlib.sha256((Path(__file__).parent / name).read_bytes()).hexdigest()
                                  for name in ("dp30.py", "workload.py", "run_fixture_suite.py", "tests/fixture_server.py")},
              "constraints": limits_observed, "runs": summaries,
              "all_fixture_cases_passed": all(r["passed"] for r in summaries),
              "requests": sum(r["offered_requests"] for r in summaries),
              "fixture_upstream_calls": sum(r["fixture_upstream_calls"] for r in summaries),
              "elapsed_seconds": time.monotonic() - start,
              "process_cpu_seconds_including_fixture_client_observers": after.ru_utime + after.ru_stime - before.ru_utime - before.ru_stime,
              "process_cumulative_maxrss_kib_linux": after.ru_maxrss,
              "final_threads": threading.active_count(),
              "final_open_fds_including_inventory_fd": len(os.listdir("/proc/self/fd")),
              "actual_rust_go_releases": 0, "actual_database_faults": 0, "independent_nodes": 0,
              "application_capacity_claim_allowed": False}
    write_json(args.output / "summary.json", report)
    print(f"{len(summaries)} fixture runs; {report['requests']} requests; all expected={report['all_fixture_cases_passed']}")
    return 0 if report["all_fixture_cases_passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
