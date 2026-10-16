#!/usr/bin/env python3
"""Attach a stopped direct test child before it execs the product.

Uses systemd's transient scope API (PIDs:au, RuntimeMaxUSec:t), documented at
https://github.com/systemd/systemd/blob/v262/src/core/dbus-scope.c
No product settings, credentials or existing processes are accepted.
"""

import argparse
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import time
from resource_group import (
    ResourceError,
    bus_environment,
    identity,
    limits,
    private_root,
    properties,
    record,
    sha,
)


# @mark runtime.joint-resource role=handoff
def attach(ready, pid, role, binary, expected):
    root = ready.parent
    private_root(root)
    lease = json.loads(ready.read_text())
    owner = lease["owner"]
    if identity(owner["pid"]) != owner:
        raise ResourceError("parent controller identity changed")
    unit = lease["unit"]
    match = re.fullmatch(r"app-lmmjoint([0-9a-f]{32})\.slice", unit)
    if not match or role not in {"core", "extensions"}:
        raise ResourceError("owned parent and declared product role required")
    nonce = match.group(1)
    observed = properties(unit)
    group = Path(lease["cgroup"])
    if (
        observed.get("Transient") != "yes"
        or observed.get("ActiveState") != "active"
        or observed.get("Description") != "Owned joint acceptance " + nonce
        or group != Path("/sys/fs/cgroup" + observed["ControlGroup"])
        or limits(group) != lease["limits"]
    ):
        raise ResourceError("parent identity or hard limits changed")
    if (
        not re.fullmatch(r"[0-9a-f]{64}", expected)
        or binary.resolve() != binary
        or not binary.is_file()
        or binary.stat().st_mode & 0o022
        or sha(binary) != expected
    ):
        raise ResourceError("unsafe or changed product executable")
    child = identity(pid)
    test = identity(os.getppid())

    def stopped():
        fields = Path(f"/proc/{pid}/stat").read_text().rsplit(") ", 1)[1].split()
        return (
            fields[0] == "T"
            and int(fields[1]) == test["pid"]
            and identity(pid) == child
        )

    if not stopped():
        raise ResourceError("only a stopped direct child may be attached")
    scope = f"lmmjoint-{nonce}-{role}-{pid}.scope"
    data = {
        "format": "lmm-commerce-scope-allocation-v1",
        "unit": scope,
        "parent": unit,
        "owner": owner,
        "test_owner": test,
        "identity": child,
        "role": role,
        "binary_sha256": expected,
        "cgroup": str(group / scope),
    }
    stem = f"scope-{pid}-{secrets.token_hex(8)}"
    record(
        root, stem + "-allocation.json", data
    )  # intent before uncertain bus acknowledgement
    settings = [
        ("Description", "s", "Owned joint child " + nonce),
        ("Slice", "s", unit),
        ("PIDs", "au", "1", str(pid)),
        ("CPUAccounting", "b", "true"),
        ("CPUQuotaPerSecUSec", "t", "1000000"),
        ("CPUQuotaPeriodUSec", "t", "100000"),
        ("MemoryAccounting", "b", "true"),
        ("MemoryMax", "t", str(256 * 1024**2)),
        ("MemorySwapMax", "t", "0"),
        ("TasksMax", "t", "64"),
        ("RuntimeMaxUSec", "t", "900000000"),
        ("TimeoutStopUSec", "t", "3000000"),
    ]
    command = [
        "/usr/bin/busctl",
        "--user",
        "call",
        "org.freedesktop.systemd1",
        "/org/freedesktop/systemd1",
        "org.freedesktop.systemd1.Manager",
        "StartTransientUnit",
        "ssa(sv)a(sa(sv))",
        scope,
        "fail",
        str(len(settings)),
    ]
    for setting in settings:
        command.extend(setting)
    command.append("0")
    result = subprocess.run(
        command, env=bus_environment(), text=True, capture_output=True, timeout=15
    )
    record(
        root,
        stem + "-start.json",
        {
            "exit_code": result.returncode,
            "stdout": result.stdout,
            "stderr": result.stderr,
        },
    )
    if result.returncode:
        raise ResourceError("scope creation failed; inspect private receipt")
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        observed = properties(scope)
        if observed.get("ActiveState") == "active" and observed.get("ControlGroup"):
            break
        time.sleep(0.025)
    else:
        raise ResourceError("scope did not become active")
    path = group / scope
    expected_limits = {
        "cpu.max": "100000 100000",
        "memory.max": str(256 * 1024**2),
        "memory.swap.max": "0",
        "pids.max": "64",
    }
    if (
        observed.get("Slice") != unit
        or observed.get("Transient") != "yes"
        or observed.get("Description") != "Owned joint child " + nonce
        or path != Path("/sys/fs/cgroup" + observed["ControlGroup"])
        or limits(path) != expected_limits
        or not stopped()
        or (path / "cgroup.procs").read_text().split() != [str(pid)]
        or Path(f"/proc/{pid}/cgroup").read_text().strip()
        != "0::" + observed["ControlGroup"]
    ):
        raise ResourceError("stopped child membership or hard limits differ")
    record(
        root,
        stem + "-ready.json",
        {**data, "limits": limits(path), "state": "stopped-before-product-exec"},
    )
    return data


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--group-ready", type=Path, required=True)
    parser.add_argument("--pid", type=int, required=True)
    parser.add_argument("--role", required=True)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--sha256", required=True)
    args = parser.parse_args()
    data = attach(args.group_ready, args.pid, args.role, args.binary, args.sha256)
    print(json.dumps(data, sort_keys=True))


if __name__ == "__main__":
    main()
