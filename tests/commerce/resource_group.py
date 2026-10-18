#!/usr/bin/env python3
"""Owned transient user slice for joint product-process acceptance.

The parent quota applies to the sum of its child services. This helper creates
no installed unit files, changes no unrelated unit and accepts no system bus.
Uses the installed systemd Manager.StartTransientUnit D-Bus API:
https://github.com/systemd/systemd/blob/main/docs/TRANSIENT-SETTINGS.md
This is test infrastructure, not a deployment service or acceptance signoff.
"""

from __future__ import annotations
import errno
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import stat
import subprocess
import time


class ResourceError(RuntimeError):
    pass


class LauncherExited(ResourceError):
    """The launcher terminated before the product identity was verified."""


def identity(pid):
    raw = Path(f"/proc/{pid}/stat").read_text()
    fields = raw[raw.rfind(")") + 2 :].split()
    return {
        "pid": pid,
        "start_ticks": int(fields[19]),
        "boot_id": Path("/proc/sys/kernel/random/boot_id").read_text().strip(),
    }


def sha(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def private_root(path):
    if not path.is_absolute() or path.resolve() != path:
        raise ResourceError("absolute, symlink-free output required")
    info = path.lstat()
    if (
        not stat.S_ISDIR(info.st_mode)
        or info.st_uid != os.geteuid()
        or info.st_mode & 0o077
    ):
        raise ResourceError("output must be private and owned by this user")


def record(root, name, value):
    private_root(root)
    if Path(name).name != name or name in {"", ".", ".."}:
        raise ResourceError("receipt name must be a single filename")
    directory = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    pending = ".pending-receipt-" + secrets.token_hex(16)
    try:
        fd = os.open(
            pending,
            os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW,
            0o600,
            dir_fd=directory,
        )
        try:
            with os.fdopen(fd, "w") as output:
                json.dump(value, output, sort_keys=True)
                output.write("\n")
                output.flush()
                os.fsync(output.fileno())
            # Hard-link publication is atomic and refuses to overwrite a receipt.
            # Readers never observe a named allocation until its JSON is complete.
            os.link(
                pending,
                name,
                src_dir_fd=directory,
                dst_dir_fd=directory,
                follow_symlinks=False,
            )
        finally:
            os.unlink(pending, dir_fd=directory)
            os.fsync(directory)
    finally:
        os.close(directory)


def bus_environment():
    # No inherited DB, product, proxy or alternate systemd host settings.
    return {
        "PATH": "/usr/bin:/bin",
        "LANG": "C.UTF-8",
        "XDG_RUNTIME_DIR": f"/run/user/{os.geteuid()}",
        "DBUS_SESSION_BUS_ADDRESS": f"unix:path=/run/user/{os.geteuid()}/bus",
    }


def control(*arguments):
    result = subprocess.run(
        ["/usr/bin/systemctl", "--user", *arguments],
        env=bus_environment(),
        text=True,
        capture_output=True,
        timeout=15,
    )
    if result.returncode:
        raise ResourceError("user manager command failed; no unconstrained fallback")
    return result.stdout


def properties(unit):
    fields = [
        "MainPID",
        "ActiveState",
        "LoadState",
        "ControlGroup",
        "Transient",
        "Description",
        "Slice",
    ]
    text = control("show", unit, *["--property=" + name for name in fields])
    return dict(line.split("=", 1) for line in text.splitlines() if "=" in line)


def limits(group):
    return {
        key: (group / key).read_text().strip()
        for key in ("cpu.max", "memory.max", "memory.swap.max", "pids.max")
    }


# @mark runtime.joint-resource role=harness
class Group:
    def __init__(self, root):
        private_root(root)
        self.root = root
        self.owner = identity(os.getpid())
        self.nonce = secrets.token_hex(16)
        self.unit = "app-lmmjoint" + self.nonce + ".slice"
        self.description = "Owned joint acceptance " + self.nonce
        self.group = None
        self.services = []
        self.started = False
        self.closed = False
        record(
            root,
            "group-allocation.json",
            {
                "unit": self.unit,
                "description": self.description,
                "owner": self.owner,
                "nonce": self.nonce,
                "cpu_quota": 1,
                "memory_max": 512 * 1024**2,
            },
        )

    def start(self):
        if self.started or self.closed:
            raise ResourceError("group creation is one-shot")
        self.started = True  # intent before uncertain remote acknowledgement
        settings = [
            ("Description", "s", self.description),
            ("CPUAccounting", "b", "true"),
            ("MemoryAccounting", "b", "true"),
            ("CPUQuotaPerSecUSec", "t", "1000000"),
            ("CPUQuotaPeriodUSec", "t", "100000"),
            ("MemoryMax", "t", str(512 * 1024**2)),
            ("MemorySwapMax", "t", "0"),
            ("TasksMax", "t", "256"),
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
            self.unit,
            "fail",
            str(len(settings)),
        ]
        for key, kind, value in settings:
            command.extend([key, kind, value])
        command.append("0")
        result = subprocess.run(
            command, env=bus_environment(), text=True, capture_output=True, timeout=15
        )
        record(
            self.root,
            "group-start.json",
            {
                "exit_code": result.returncode,
                "stdout": result.stdout,
                "stderr": result.stderr,
            },
        )
        if result.returncode:
            raise ResourceError("transient slice creation failed; inspect receipt")
        observed = properties(self.unit)
        if (
            observed.get("Transient") != "yes"
            or observed.get("Description") != self.description
        ):
            raise ResourceError("parent slice identity mismatch")
        path = observed.get("ControlGroup", "")
        if not path.startswith(
            f"/user.slice/user-{os.geteuid()}.slice/"
        ) or ".." in path.split("/"):
            raise ResourceError("parent is not in the owned user hierarchy")
        self.group = Path("/sys/fs/cgroup" + path)
        expected = {
            "cpu.max": "100000 100000",
            "memory.max": str(512 * 1024**2),
            "memory.swap.max": "0",
            "pids.max": "256",
        }
        actual = limits(self.group)
        if actual != expected:
            raise ResourceError("actual parent hard limits differ")
        record(
            self.root,
            "group-ready.json",
            {
                "unit": self.unit,
                "owner": self.owner,
                "properties": observed,
                "limits": actual,
                "cgroup": str(self.group),
            },
        )
        return self

    def launch(
        self, role, binary, arguments, expected_sha256, memory_max=256 * 1024**2
    ):
        if self.group is None or self.closed or role not in {"core", "extensions"}:
            raise ResourceError("ready parent and a declared product role required")
        if memory_max != 256 * 1024**2:
            raise ResourceError("this frozen draft uses 256MiB per child")
        if not re.fullmatch(r"[0-9a-f]{64}", expected_sha256):
            raise ResourceError("expected binary SHA256 required")
        info = binary.lstat()
        if (
            binary.resolve() != binary
            or not stat.S_ISREG(info.st_mode)
            or info.st_mode & 0o022
            or sha(binary) != expected_sha256
        ):
            raise ResourceError("unsafe or changed binary")
        held = binary.open("rb")
        try:
            unit = f"lmmjoint-{self.nonce}-{role}-{len(self.services)}.service"
            item = {
                "unit": unit,
                "role": role,
                "pid": None,
                "launcher": None,
                "group": None,
            }
            self.services.append(item)  # cleanup authority before spawning anything
            stem = "service-" + str(len(self.services))
            record(
                self.root,
                stem + "-allocation.json",
                {
                    "unit": unit,
                    "parent": self.unit,
                    "owner": self.owner,
                    "binary_sha256": expected_sha256,
                    "role": role,
                    "command_sha256": hashlib.sha256(
                        json.dumps([str(binary), *arguments]).encode()
                    ).hexdigest(),
                },
            )
            fd = os.open(
                self.root / (stem + ".log"), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600
            )
            with os.fdopen(fd, "w") as log:
                item["launcher"] = subprocess.Popen(
                    [
                        "/usr/bin/systemd-run",
                        "--user",
                        "--quiet",
                        "--wait",
                        "--pipe",
                        "--service-type=exec",
                        "--unit",
                        unit,
                        "--slice",
                        self.unit,
                        "--property=CPUAccounting=yes",
                        "--property=CPUQuota=100%",
                        "--property=CPUQuotaPeriodSec=100ms",
                        "--property=MemoryAccounting=yes",
                        "--property=MemoryMax=" + str(memory_max),
                        "--property=MemorySwapMax=0",
                        "--property=TasksMax=64",
                        "--property=RuntimeMaxSec=900",
                        "--property=TimeoutStopSec=10s",
                        "--property=KillMode=control-group",
                        "--",
                        "/usr/bin/env",
                        "-i",
                        "PATH=/usr/bin:/bin",
                        "GOMAXPROCS=2",
                        str(binary),
                        *arguments,
                    ],
                    env=bus_environment(),
                    stdin=subprocess.DEVNULL,
                    stdout=log,
                    stderr=subprocess.STDOUT,
                )
            deadline = time.monotonic() + 15
            held_info = os.fstat(held.fileno())
            pinned = None
            startup = {"timeout_seconds": 15, "attempts": 0, "retry_counts": {}}
            stage = "unit-properties"
            try:
                while time.monotonic() < deadline:
                    startup["attempts"] += 1
                    stage = "unit-properties"
                    observed = properties(unit)
                    path = observed.get("ControlGroup", "")
                    if path:
                        candidate = Path("/sys/fs/cgroup" + path)
                        if (
                            observed.get("Slice") != self.unit
                            or candidate.parent != self.group
                            or (
                                item["group"] is not None and candidate != item["group"]
                            )
                        ):
                            raise ResourceError(
                                "child is outside the shared parent budget"
                            )
                        item["group"] = candidate
                    pid = int(observed.get("MainPID", "0"))
                    if item["launcher"].poll() is not None or (pinned and not pid):
                        raise LauncherExited(
                            "launcher or product exited before verified product PID"
                        )
                    if pinned and pid != pinned["pid"]:
                        raise ResourceError("child PID identity changed during startup")
                    if pid and path:
                        try:
                            stage = "identity-before"
                            before = identity(pid)
                            if pinned is None:
                                pinned = before
                            if (
                                before != pinned
                                or before["boot_id"] != self.owner["boot_id"]
                            ):
                                raise ResourceError(
                                    "child PID identity changed during startup"
                                )
                            expected_cgroup = f"0::{path}"
                            process_cgroup = Path(f"/proc/{pid}/cgroup")
                            stage = "cgroup-before"
                            if process_cgroup.read_text().strip() != expected_cgroup:
                                raise ResourceError(
                                    "child process is outside the shared parent budget"
                                )
                            actual = Path(f"/proc/{pid}/exe")
                            stage = "executable-open"
                            with actual.open("rb") as running:
                                actual_info = os.fstat(running.fileno())
                                matches = (actual_info.st_dev, actual_info.st_ino) == (
                                    held_info.st_dev,
                                    held_info.st_ino,
                                )
                                # Hash the same opened inode, not a second /proc lookup.
                                if matches:
                                    stage = "executable-hash"
                                    if (
                                        hashlib.file_digest(
                                            running, "sha256"
                                        ).hexdigest()
                                        != expected_sha256
                                    ):
                                        raise ResourceError(
                                            "running executable SHA256 differs"
                                        )
                            if matches:
                                stage = "executable-after"
                                final_info = actual.stat()
                                if (final_info.st_dev, final_info.st_ino) != (
                                    held_info.st_dev,
                                    held_info.st_ino,
                                ):
                                    raise ResourceError(
                                        "running executable changed during verification"
                                    )
                            stage = "cgroup-after"
                            if process_cgroup.read_text().strip() != expected_cgroup:
                                raise ResourceError(
                                    "child process is outside the shared parent budget"
                                )
                            stage = "identity-after"
                            if identity(pid) != pinned:
                                raise ResourceError(
                                    "child PID identity changed during startup"
                                )
                            if matches and time.monotonic() < deadline:
                                item["pid"] = pid
                                item["identity"] = pinned
                                startup["result"] = "identity-verified"
                                break
                            reason = (
                                "identity-read-completed-after-deadline"
                                if matches
                                else "executable-not-yet-matched"
                            )
                        except OSError as error:
                            # /proc/exe uses ptrace access checks. A denial can be
                            # transient around service startup, but proves neither
                            # exec progress nor identity. Only complete later reads
                            # can pass, under the original, non-renewing deadline.
                            if error.errno not in {errno.EACCES, errno.ENOENT}:
                                raise
                            reason = f"{stage}:{errno.errorcode[error.errno]}"
                        counts = startup["retry_counts"]
                        counts[reason] = counts.get(reason, 0) + 1
                        startup["last_retry"] = reason
                    time.sleep(0.02)
                else:
                    reason = startup.get("last_retry", "no-product-PID")
                    raise ResourceError(
                        f"no verified product PID within startup deadline; last observation: {reason}"
                    )
            except Exception as error:
                startup.update(
                    result="failed",
                    stage=stage,
                    error=f"{type(error).__name__}: {error}",
                )
                raise
            finally:
                startup["pinned_identity"] = pinned
                record(self.root, stem + "-startup.json", startup)
            current = binary.stat()
            if (current.st_dev, current.st_ino) != (held_info.st_dev, held_info.st_ino):
                raise ResourceError("binary path changed during launch")
            if Path(f"/proc/{pid}/cgroup").read_text().strip() != f"0::{path}":
                raise ResourceError("child process is outside the shared parent budget")
            actual_limits = limits(item["group"])
            if (
                actual_limits["memory.max"] != str(memory_max)
                or actual_limits["memory.swap.max"] != "0"
                or actual_limits["pids.max"] != "64"
            ):
                raise ResourceError("child memory/task hard limits differ")
            record(
                self.root,
                stem + "-identity.json",
                {
                    "unit": unit,
                    "parent": self.unit,
                    "role": role,
                    "identity": item["identity"],
                    "launcher_pid": item["launcher"].pid,
                    "executable_sha256": expected_sha256,
                    "cgroup": str(item["group"]),
                    "parent_limits": limits(self.group),
                    "child_limits": actual_limits,
                },
            )
            return item
        finally:
            held.close()

    def sample(self):
        """A bounded point observation; disappearing child scopes are recorded explicitly."""

        def counters(path):
            return dict(
                (key, int(value))
                for key, value in (
                    line.split() for line in path.read_text().splitlines()
                )
            )

        def metrics(path):
            return {
                "cpu": counters(path / "cpu.stat"),
                "memory_current": int((path / "memory.current").read_text()),
                "memory_peak": int((path / "memory.peak").read_text()),
                "memory_events": counters(path / "memory.events"),
                "pids": int((path / "pids.current").read_text()),
            }

        sample = {
            "monotonic": time.monotonic(),
            "unix_ns": time.time_ns(),
            "parent": metrics(self.group),
            "children": [],
        }
        sample["host_stat"] = Path("/proc/stat").read_text()
        sample["host_meminfo"] = Path("/proc/meminfo").read_text()
        sample["observer_status"] = Path("/proc/self/status").read_text()
        for path in sorted(self.root.glob("scope-*-allocation.json")):
            lease = json.loads(path.read_text())
            child = self.group / lease["unit"]
            item = {
                "unit": lease["unit"],
                "role": lease["role"],
                "pid": lease["identity"]["pid"],
            }
            try:
                item["metrics"] = metrics(child)
                pid = item["pid"]
                if identity(pid) != lease["identity"]:
                    raise ResourceError("sampled child PID identity changed")
                status = Path(f"/proc/{pid}/status").read_text().splitlines()
                values = dict(line.split(":", 1) for line in status if ":" in line)
                if "VmRSS" in values:
                    item["rss_kib"] = int(values["VmRSS"].split()[0])
                    item["fd_count"] = len(list(Path(f"/proc/{pid}/fd").iterdir()))
                    item["pss_kib"] = next(
                        int(line.split()[1])
                        for line in Path(f"/proc/{pid}/smaps_rollup")
                        .read_text()
                        .splitlines()
                        if line.startswith("Pss:")
                    )
                    item["state"] = "observed"
                elif values.get("State", "").strip().startswith(("Z", "X")):
                    item["state"] = "exited-during-sample"
                else:
                    raise ResourceError("live product RSS missing from observation")
            except (FileNotFoundError, ProcessLookupError):
                item["state"] = "disappeared-during-or-before-sample"
            sample["children"].append(item)
        return sample

    def adopt_scopes(self):
        """Register only allocation leases written under this controller's private root."""
        known = {item["unit"] for item in self.services}
        for path in sorted(self.root.glob("scope-*-allocation.json")):
            data = json.loads(path.read_text())
            unit = data["unit"]
            nonce = self.nonce
            if (
                data.get("format") != "lmm-commerce-scope-allocation-v1"
                or data.get("owner") != self.owner
                or data.get("parent") != self.unit
                or not re.fullmatch(
                    r"lmmjoint-" + nonce + r"-(core|extensions)-[0-9]+\.scope", unit
                )
                or data.get("cgroup") != str(self.group / unit)
                or data["identity"]["boot_id"] != self.owner["boot_id"]
            ):
                raise ResourceError("refuse foreign scope allocation")
            if unit not in known:
                self.services.append(
                    {
                        "unit": unit,
                        "role": data["role"],
                        "pid": data["identity"]["pid"],
                        "identity": data["identity"],
                        "launcher": None,
                        "group": self.group / unit,
                    }
                )
                known.add(unit)

    def close(self):
        if self.closed:
            return
        if identity(os.getpid()) != self.owner:
            raise ResourceError("cleanup is limited to this group owner")
        errors = []
        try:
            self.adopt_scopes()
        except (ResourceError, OSError, ValueError, KeyError) as error:
            errors.append({"unit": self.unit, "error": "scope lease: " + str(error)})
        foreign = False
        for item in reversed(self.services):
            try:
                observed = properties(item["unit"])
                if observed.get("Slice") not in {self.unit, ""}:
                    foreign = True
                    raise ResourceError("refuse foreign child cleanup")
                if observed.get("LoadState") != "not-found":
                    control("stop", item["unit"])
                if item["launcher"] is not None:
                    item["launcher"].wait(timeout=15)
                observed = properties(item["unit"])
                if int(observed.get("MainPID", "0")) != 0 or observed.get(
                    "ActiveState"
                ) not in {"inactive", "failed"}:
                    raise ResourceError("child stop unconfirmed")
                # Verify the expected path even when startup failed before discovery.
                expected = self.group / item["unit"] if self.group else None
                if any(
                    group is not None and group.exists()
                    for group in [item["group"], expected]
                ):
                    raise ResourceError("child cgroup remains")
                if item.get("identity"):
                    try:
                        if identity(item["pid"]) == item["identity"]:
                            raise ResourceError("original product PID remains")
                    except FileNotFoundError:
                        pass
            except (ResourceError, OSError, subprocess.SubprocessError) as error:
                errors.append(
                    {
                        "unit": item["unit"],
                        "error": type(error).__name__ + ": " + str(error),
                    }
                )
        if self.started:
            try:
                observed = properties(self.unit)
                if foreign or (
                    observed.get("LoadState") != "not-found"
                    and observed.get("Description") != self.description
                ):
                    raise ResourceError("refuse foreign parent cleanup")
                if observed.get("LoadState") != "not-found":
                    control("stop", self.unit)
                observed = properties(self.unit)
                if observed.get("ActiveState") not in {"inactive", "failed"}:
                    raise ResourceError("parent stop unconfirmed")
                if self.group is not None and self.group.exists():
                    raise ResourceError("parent cgroup remains")
            except (ResourceError, OSError, subprocess.SubprocessError) as error:
                errors.append(
                    {
                        "unit": self.unit,
                        "error": type(error).__name__ + ": " + str(error),
                    }
                )
        record(
            self.root,
            "group-cleanup-attempt-" + secrets.token_hex(8) + ".json",
            {"errors": errors},
        )
        if errors:
            raise ResourceError("cleanup incomplete; inspect cleanup attempt receipt")
        self.closed = True
        record(
            self.root,
            "group-closed.json",
            {
                "unit": self.unit,
                "owner": self.owner,
                "child_units": [item["unit"] for item in self.services],
                "processes_and_cgroups_removed": True,
            },
        )
