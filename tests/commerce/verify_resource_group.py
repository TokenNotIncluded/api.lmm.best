#!/usr/bin/env python3
"""Check the local shared cgroup helper with synthetic probes, not products.

Requires the current user's systemd bus and delegated cgroup v2. Run via agcc:
  python3 -B tests/commerce/verify_resource_group.py --output-root /private/path
The new output directory must not exist. No production services are modified.
"""

import argparse
from pathlib import Path
import time
from resource_group import Group, LauncherExited, ResourceError, record, sha


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-root", type=Path, required=True)
    args = parser.parse_args()
    root = args.output_root
    if not root.is_absolute() or root.resolve() != root:
        parser.error("absolute symlink-free output required")
    root.mkdir(mode=0o700)
    binary = Path("/usr/bin/python3").resolve()
    checks = []
    for case in ["shared-cpu", "early-exit", "missing-unit"]:
        directory = root / case
        directory.mkdir(mode=0o700)
        group = Group(directory)
        try:
            group.start()
            group.launch("core", binary, ["-c", "while True: pass"], sha(binary))
            if case == "shared-cpu":
                group.launch(
                    "extensions", binary, ["-c", "while True: pass"], sha(binary)
                )

                def usage():
                    values = dict(
                        line.split()
                        for line in (group.group / "cpu.stat").read_text().splitlines()
                    )
                    return int(values["usage_usec"])

                start = time.monotonic()
                before = usage()
                time.sleep(5)
                elapsed = time.monotonic() - start
                cpu = (usage() - before) / 1e6
                if not 0.5 < cpu / elapsed < 1.2:
                    raise ResourceError("aggregate CPU probe outside expected range")
                record(
                    directory,
                    "probe.json",
                    {
                        "wall_seconds": elapsed,
                        "cpu_seconds": cpu,
                        "combined_cores": cpu / elapsed,
                    },
                )
            elif case == "early-exit":
                launched = True
                try:
                    group.launch(
                        "extensions",
                        binary,
                        ["-c", "raise SystemExit(17)"],
                        sha(binary),
                    )
                except LauncherExited:
                    launched = False
                exited = group.services[-1]["launcher"]
                code = exited.wait(timeout=5)
                if code != 17:
                    raise ResourceError("injected child exit must remain exactly 17")
                record(
                    directory,
                    "exit.json",
                    {
                        "exit_code": code,
                        "identity_verified_before_exit": launched,
                        "expected_exit_code": 17,
                    },
                )
            else:
                # Allocation exists but no launch occurred: cleanup must still stop its sibling.
                group.services.append(
                    {
                        "unit": f"lmmjoint-{group.nonce}-never-created.service",
                        "role": "extensions",
                        "pid": None,
                        "launcher": None,
                        "group": None,
                    }
                )
        finally:
            group.close()
        checks.append(case)
    record(
        root,
        "result.json",
        {
            "result": "PASS",
            "scope": "synthetic resource-helper probes only",
            "checks": checks,
            "products_tested": False,
        },
    )
    print(
        "PASS: shared quota, early exit and missing-unit cleanup; synthetic probes only"
    )


if __name__ == "__main__":
    main()
