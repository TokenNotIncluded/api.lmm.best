#!/usr/bin/env python3
"""Measured 20-round local native-artifact experiment. No capacity claim for LMM."""
from __future__ import annotations
import csv
import dataclasses
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import time
from disk_lifecycle import digest, encoded
from lab_support import DEFAULT_POLICY, assert_sentinels, df_du, make_archive, new_store, protect_sentinels


def main() -> None:
    lab = Path(os.environ["DP28_LAB"])
    out = Path(os.environ["DP28_OUT"])
    packages = out / "packages"
    packages.mkdir()
    artifacts = [make_archive(packages, Path(os.environ["DP28_PROBE"]), i) for i in range(21)]
    rows, rounds = [], []
    trace = []
    with new_store(lab, "twenty-rounds") as store:
        sentinels = protect_sentinels(store)
        def capture(event: str) -> None:
            # A lock is already held during install. fstatvfs is read-only.
            fs = os.fstatvfs(store.fd)
            trace.append({"phase": event, "used_bytes": (fs.f_blocks - fs.f_bfree) * fs.f_frsize,
                          "free_inodes": fs.f_favail})
        store.hook = capture
        before = store.bill()
        first_install = store.install_fixture(*artifacts[0])
        initial_install_phases = list(trace)
        preview = store.gc()
        first_gc = store.gc(preview["plan"]["plan_sha256"])
        baseline = store.bill()
        # Use a real process plus a mandatory inherited lifecycle lease. The
        # shared executable is one inode; it must not keep every old release.
        with store.lease("r000") as lease:
            running = subprocess.Popen(
                [str(store.root / "releases/r000/probe"), "--hold"], cwd=store.root / "releases/r000",
                stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                pass_fds=(lease,), env={"PATH": "/usr/bin:/bin", "LC_ALL": "C"}, text=True,
            )
            assert running.stdout is not None
            started = json.loads(running.stdout.readline())
            assert started["version"] == "r000"
            try:
                for index in range(1, 21):
                    trace.clear()
                    pre = store.bill()
                    change = store.install_fixture(*artifacts[index])
                    peak_before_gc = store.bill()
                    # Only explicit synthetic business records grow. They are
                    # not counted as release garbage and never enter GC.
                    (store.root / "protected/event_payloads" / f"business-{index:03d}.json").write_bytes(
                        encoded({"synthetic": True, "event": index, "confirmed": False}))
                    (store.root / "protected/dedup" / f"key-{index:03d}.json").write_bytes(
                        encoded({"synthetic": True, "effect": index, "keep": True}))
                    for _ in range(80):
                        assert store.diagnostic("request-complete")
                    preview = store.gc()
                    assert all(not entry["path"].startswith("protected/") for entry in preview["plan"]["delete"])
                    actual = store.gc(preview["plan"]["plan_sha256"])
                    after = store.bill()
                    assert_sentinels(store, sentinels)
                    fallback = store.rollback_probe()
                    assert fallback["release"] == f"r{index - 1:03d}"
                    names = sorted(p.name for p in (store.root / "releases").iterdir())
                    assert names == sorted({"r000", f"r{index-1:03d}", f"r{index:03d}"}), names
                    assert not list((store.root / "staging").iterdir())
                    release_bytes = sum(value["allocated_unique_bytes"] for key, value in after["categories"].items()
                                        if key in {"objects", "releases", "staging"})
                    data_bytes = sum(value["allocated_unique_bytes"] for key, value in after["categories"].items()
                                     if key.startswith("protected/") or key == "backups")
                    row = {"round": index, "release_bytes": release_bytes,
                           "total_reachable_bytes": after["reachable_allocated_bytes"],
                           "filesystem_used_bytes": after["filesystem"]["used_bytes"],
                           "peak_used_bytes": max([p["used_bytes"] for p in trace] + [peak_before_gc["filesystem"]["used_bytes"]]),
                           "business_and_backup_bytes": data_bytes,
                           "diagnostic_bytes": after["categories"].get("logs", {}).get("allocated_unique_bytes", 0),
                           "retained_releases": len(names), "deleted_entries": len(actual["deleted_this_attempt"]),
                           "reclaimed_files_upper": preview["plan"]["reclaimable_file_bytes"],
                           "free_inodes": after["filesystem"]["available_inodes"]}
                    rows.append(row)
                    rounds.append({"round": index, "before": pre, "install": change, "phases": list(trace),
                                   "before_gc": peak_before_gc, "preview": preview, "actual": actual,
                                   "after": after, "rollback": fallback, "retained_versions": names})
                    print(json.dumps(row), flush=True)
                assert len({r["release_bytes"] for r in rows[1:]}) == 1, "release footprint did not plateau"
                assert running.poll() is None, "old live process was lost"
                before_drain_exit = store.bill()
                rollback = store.rollback_probe()
                raw_tools = df_du(store.root)
            finally:
                if running.stdin:
                    running.stdin.close()
                running.wait(timeout=5)
        drain_preview = store.gc()
        drain_gc = store.gc(drain_preview["plan"]["plan_sha256"])
        final_bill = store.bill()
        assert sorted(p.name for p in (store.root / "releases").iterdir()) == ["r019", "r020"]
        assert_sentinels(store, sentinels)
        environment = {
            "run_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "base_commit": "72667564c0431754d4856dc2e0db55f360bd2745",
            "python": platform.python_version(), "kernel": platform.release(), "architecture": platform.machine(),
            "filesystem": "private capped tmpfs in a user/mount/network namespace",
            "fixture_filesystem_limit_bytes": 16 * 1024 * 1024, "fixture_filesystem_inodes": 512,
            "policy": dataclasses.asdict(DEFAULT_POLICY), "docker_available": shutil.which("docker") is not None,
            "scope": "real local filesystem syscalls and native fixture; NOT LMM binaries, PostgreSQL or Docker",
            "peak_method": "fstatvfs after each completed prototype allocation and phase; not kernel or physical-disk telemetry",
            "native_binary_bytes": Path(os.environ["DP28_PROBE"]).stat().st_size,
            "native_binary_sha256": digest(Path(os.environ["DP28_PROBE"]).read_bytes()),
        }
        evidence = {"environment": environment, "initial": before, "install_0": first_install,
                    "gc_0": first_gc, "install_0_phases": initial_install_phases, "after_install_0": baseline, "rounds": rounds,
                    "before_drain_exit": before_drain_exit, "after_drain_exit": final_bill,
                    "drain_gc_preview": drain_preview, "drain_gc_actual": drain_gc,
                    "fallback_cold_start": rollback, "gnu_df_du": raw_tools, "protected_file_checksums": sentinels}
        (out / "twenty-rounds.json").write_bytes(encoded(evidence))
        with (out / "twenty-rounds.csv").open("w", newline="") as stream:
            writer = csv.DictWriter(stream, fieldnames=rows[0].keys(), lineterminator="\n")
            writer.writeheader()
            writer.writerows(rows)
        (out / "environment.json").write_bytes(encoded(environment))
    # The mount remains for the tests. Only remove this exact owned fixture
    # after recording evidence. This is test harness teardown, not the GC tool.
    shutil.rmtree(lab / "twenty-rounds")


if __name__ == "__main__":
    main()
