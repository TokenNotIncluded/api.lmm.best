#!/usr/bin/env python3
"""Reproduce tests and small local measurements. Never run CI or deploy a service."""
from __future__ import annotations

import hashlib
import itertools
import json
from pathlib import Path
import platform
import re
import subprocess
import sys
import tempfile
import time

from contract import Rejected, canonical, compatible, configuration_compatible
from fixture import create, make_manifest
from release import Controller, gc_history, init

ROOT = Path(__file__).resolve().parent
EVIDENCE = ROOT / "evidence"
BASE = "72667564c0431754d4856dc2e0db55f360bd2745"


def measured(command: list[str], temporary: Path) -> tuple[dict, dict]:
    clock = time.perf_counter()
    rss_file = temporary / "rss.txt"
    timing = Path("/usr/bin/time")
    argv = ([str(timing), "-f", "%M", "-o", str(rss_file)] + command) if timing.is_file() else command
    result = subprocess.run(argv, capture_output=True, text=True, timeout=30, check=True)
    usage = {"elapsed_seconds": round(time.perf_counter() - clock, 6),
             "peak_rss_kib": int(rss_file.read_text().strip()) if timing.is_file() else None}
    return json.loads(result.stdout), usage


def main() -> int:
    EVIDENCE.mkdir(exist_ok=True)
    start = time.perf_counter()
    result = subprocess.run([sys.executable, "-S", "-B", "-X", "dev", "-W", "error::ResourceWarning",
                             "-m", "unittest", "discover", "-s", "tests", "-v"],
                            cwd=ROOT, capture_output=True, text=True, timeout=60)
    log = result.stdout + result.stderr
    (EVIDENCE / "unittest.txt").write_text(log)
    count = re.search(r"Ran (\d+) tests", log)
    summary = {"base_commit": BASE, "python": platform.python_version(), "system": platform.system(),
               "architecture": platform.machine(), "test_count": int(count.group(1)) if count else None,
               "tests_passed": result.returncode == 0, "test_elapsed_seconds": round(time.perf_counter() - start, 6),
               "scope": "local synthetic node stores plus loopback HTTP/SSE fixture processes; not Rust/Go or PostgreSQL",
               "runtime_flags": ["-S", "-B"],
               "resource_limits": "not CPU-pinned or memory-cgroup-limited; NOT a 1c1g capacity measurement",
               "remote_actions": "none"}
    if result.returncode:
        print(log)
        return result.returncode
    with tempfile.TemporaryDirectory(prefix="dp29-evidence-") as work:
        work = Path(work)
        inv, before = create(work / "nodes", 3)
        store = init(work / "control", inv)
        controller = Controller(store)
        after = {c: make_manifest(c, 2, work / "nodes") for c in before}
        p = controller.plan(after["go-extensions"])
        cmd = [sys.executable, "-S", "-B", str(ROOT / "release.py"), "--state", str(store.root)]
        hashes = {n: hashlib.sha256((work / "nodes" / n / "business.sqlite").read_bytes()).hexdigest()
                  for n in inv["nodes"]}
        p, usage = measured(cmd + ["run", p["id"], "--expect-revision", str(p["revision"])], work)
        summary["synthetic_three_node_release"] = {"stage": p["stage"], **usage}
        _, usage = measured(cmd + ["status", p["id"]], work)
        summary["status_command"] = usage
        p = controller.rollback(p["id"], p["revision"])
        p, usage = measured(cmd + ["run", p["id"], "--expect-revision", str(p["revision"])], work)
        summary["synthetic_three_node_rollback"] = {"stage": p["stage"], **usage}
        summary["business_database_hashes_unchanged"] = all(
            hashlib.sha256((work / "nodes" / n / "business.sqlite").read_bytes()).hexdigest() == hashes[n]
            for n in inv["nodes"])
        for _ in range(20):
            for _ in range(20):
                controller.plan(after["go-extensions"])
            gc_history(store, 3, store.meta()["epoch"])
        summary["high_frequency_queue"] = {"submitted": 400, "remaining_records": len(store.status()["plans"]),
                                            "control_database_bytes": store.db.stat().st_size,
                                            "cleanup": "explicit, every 20 queued submissions; no deployment side effects"}
        cases = []
        names = sorted(before)
        # 8 app choices x 8 independent configuration choices x 4 core/Go protocol choices.
        for app_bits in itertools.product((0, 1), repeat=3):
            for config_bits in itertools.product((0, 1), repeat=3):
                for wire_bits in itertools.product((1, 2), repeat=2):
                    selected = {name: json.loads(canonical((before, after)[bit][name]))
                                for name, bit in zip(names, app_bits)}
                    reason = None
                    try:
                        for name, bit in zip(names, config_bits):
                            configuration_compatible(selected[name], (before, after)[bit][name]["config"])
                        for name, wire in zip(("rust-core", "go-extensions"), wire_bits):
                            selected[name]["protocol"]["emit"] = wire
                        compatible({name: [m] for name, m in selected.items()}, 1, ["pending", "settled"])
                    except Rejected as exc:
                        reason = str(exc)
                    cases.append({"applications": dict(zip(names, app_bits)),
                                  "configurations": dict(zip(names, config_bits)),
                                  "protocol_emit": dict(zip(("rust-core", "go-extensions"), wire_bits)),
                                  "compatible": reason is None, "reason": reason})
        (EVIDENCE / "compatibility-matrix.json").write_text(json.dumps({
            "scope": "manifest contract calculations only, NOT native binary interoperability",
            "assumptions": "synthetic readers accept config 1/2, protocol 1/2, schema 1/2; negative cases are in unittest.txt",
            "cases": cases}, indent=2) + "\n")
        summary["compatibility_contract_cases"] = len(cases)
    summary["source_bytes"] = sum(p.stat().st_size for p in ROOT.rglob("*.py"))
    (EVIDENCE / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    print(json.dumps(summary, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
