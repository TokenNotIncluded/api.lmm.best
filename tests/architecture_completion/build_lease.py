#!/usr/bin/env python3
"""Local, cooperative heavy-build lease. Not a product or database recovery lock.

A09 creates one private resource directory. Each worker writes receipts only in
its own private request parent. Waiters use the kernel lock queue, with no FIFO
promise and no lease stealing on a clock timeout. Never delete the lock file.
The command runner supervises local Linux descendants, not work submitted to an
external daemon. This is not protection against killing the supervisor itself.
"""
from __future__ import annotations

import argparse
import ctypes
from contextlib import contextmanager
from datetime import datetime, timezone
import fcntl
import hashlib
import json
import os
from pathlib import Path
import resource
import secrets
import stat
import subprocess
import sys
import time

TASKS = {f"A{n:02d}" for n in range(1, 11)} | {"S11"}
KINDS = {"cargo", "frontend", "image"}


def private_directory(path: Path) -> None:
    if not path.is_absolute() or path.resolve() != path:
        raise ValueError("lease directories must be absolute and contain no symlinks")
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077:
        raise ValueError("lease directories must be owned by this user and private")


def process_identity(pid: int) -> dict:
    # A PID alone can be reused. This identity is evidence, not takeover authority.
    raw = Path(f"/proc/{pid}/stat").read_text()
    tail = raw[raw.rfind(")") + 2:].split()
    return {"pid": pid, "start_ticks": int(tail[19]),
            "boot_id": Path("/proc/sys/kernel/random/boot_id").read_text().strip()}


def receipt(directory: Path, phase: str, values: dict) -> None:
    # Append a new phase file. Never rewrite another reader's receipt.
    data = {"format": "a09-build-lease-v1", "phase": phase,
            "recorded_utc": datetime.now(timezone.utc).isoformat(), **values}
    target = directory / (phase + ".json")
    fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "w") as output:
        json.dump(data, output, sort_keys=True)
        output.write("\n")
        output.flush()
        os.fsync(output.fileno())


def initialize(directory: Path) -> None:
    private_directory(directory.parent)
    directory.mkdir(mode=0o700, exist_ok=False)
    fd = os.open(directory / "heavy.lock", os.O_RDWR | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    os.close(fd)
    receipt(directory, "resource", {"creator": process_identity(os.getpid()),
                                    "nonce": secrets.token_hex(16), "slots": 1})


def observed_limits() -> dict:
    result = {"cpu_affinity_count": len(os.sched_getaffinity(0)),
              "rlimit_as_bytes": list(resource.getrlimit(resource.RLIMIT_AS)),
              "rlimit_cpu_seconds": list(resource.getrlimit(resource.RLIMIT_CPU))}
    # These are observations, not an assertion that a requested cap is enforced.
    for name in ("cpu.max", "memory.max", "pids.max"):
        try:
            result[name] = (Path("/sys/fs/cgroup") / name).read_text().strip()
        except OSError:
            result[name] = "UNKNOWN"
    return result


@contextmanager
def acquire(directory: Path, requests: Path, task: str, kind: str,
            input_sha256: str, timeout_seconds: float = 600):
    if task not in TASKS or kind not in KINDS:
        raise ValueError("unknown task or heavy-build kind")
    if len(input_sha256) != 64 or any(c not in "0123456789abcdef" for c in input_sha256):
        raise ValueError("a frozen input manifest SHA-256 is required")
    if not 0 <= timeout_seconds <= 3600:
        raise ValueError("timeout must be between 0 and 3600 seconds")
    private_directory(directory)
    private_directory(requests)
    lock = directory / "heavy.lock"
    fd = os.open(lock, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        info = os.fstat(fd)
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
                or info.st_mode & 0o077 or info.st_nlink != 1):
            raise ValueError("unsafe lease lock")
        request = requests / secrets.token_hex(16)
        request.mkdir(mode=0o700)
        binding = {"task": task, "kind": kind, "request": request.name,
                   "holder": process_identity(os.getpid()), "input_sha256": input_sha256,
                   "lock_device": info.st_dev, "lock_inode": info.st_ino}
        receipt(request, "requested", binding)
        started = time.monotonic()
        while True:
            try:
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                remaining = timeout_seconds - (time.monotonic() - started)
                if remaining <= 0:
                    receipt(request, "timeout", binding)
                    raise TimeoutError("heavy-build slot is busy; no holder was stopped") from None
                time.sleep(min(0.05, remaining))
        current = lock.lstat()
        if current.st_ino != info.st_ino or current.st_dev != info.st_dev:
            raise ValueError("lease inode changed; refusing a split lock")
        receipt(request, "acquired", {**binding, "limits": observed_limits()})
        try:
            yield fd, request
        except BaseException:
            receipt(request, "interrupted", binding)
            raise
        else:
            receipt(request, "released", binding)
    finally:
        # Do NOT issue LOCK_UN: a surviving child inherits this same open file
        # description. Closing only this descriptor keeps its build protected.
        os.close(fd)


def _enable_subreaper() -> None:
    # Only the private supervisor adopts orphaned descendants, never the caller.
    libc = ctypes.CDLL(None, use_errno=True)
    libc.prctl.argtypes = [ctypes.c_int] + [ctypes.c_ulong] * 4
    libc.prctl.restype = ctypes.c_int
    if libc.prctl(36, 1, 0, 0, 0) != 0:  # PR_SET_CHILD_SUBREAPER
        raise OSError(ctypes.get_errno(), "cannot supervise build descendants")


def _reap_descendants() -> int:
    count = 0
    while True:
        try:
            os.waitpid(-1, 0)
            count += 1
        except InterruptedError:
            continue
        except ChildProcessError:
            return count


def _supervise(fd: int, request: Path, command: list[str], cwd: Path) -> None:
    _enable_subreaper()  # Fail before spawning a build if unsupported.
    receipt(request, "supervising", {"supervisor": process_identity(os.getpid())})
    environment = {**os.environ, "CARGO_TARGET_DIR": str(request / "target"),
                   "CARGO_BUILD_JOBS": "1"}
    try:
        # No shell or raw command/DSN/token text in receipts. Keep the lock here
        # even when a build launcher closes inherited fds or exits before workers.
        with subprocess.Popen(command, cwd=cwd, env=environment, pass_fds=(fd,)) as child:
            try:
                child_identity = process_identity(child.pid)
            except FileNotFoundError:
                child_identity = {"pid": child.pid, "generation": "EXITED_BEFORE_OBSERVATION"}
            receipt(request, "running", {"child": child_identity,
                    "command_sha256": hashlib.sha256(json.dumps(command).encode()).hexdigest()})
            code = child.wait()
    finally:
        # Popen waits for the direct child even after a receipt error; then reap
        # only this supervisor's adopted descendants. Never scan or kill PIDs.
        descendants = _reap_descendants()
    receipt(request, "finished", {"exit_code": code, "descendants_reaped": descendants})


def _supervisor_main(fd: int, request: Path, payload_fd: int) -> int:
    try:
        with os.fdopen(payload_fd, "rb") as payload:
            data = json.loads(payload.read())
        _supervise(fd, request, data["command"], Path(data["cwd"]))
        return 0
    except BaseException as error:
        try:
            receipt(request, "supervisor-failed", {"error_type": type(error).__name__})
        except OSError:
            pass
        return 1


def run(directory: Path, requests: Path, task: str, kind: str, input_sha256: str,
        command: list[str], cwd: Path, timeout_seconds: float = 600) -> int:
    with acquire(directory, requests, task, kind, input_sha256, timeout_seconds) as (fd, request):
        # A fresh interpreter avoids fork-with-threads hazards in library callers.
        # Its private pipe keeps raw commands out of supervisor argv and receipts.
        encoded = json.dumps({"command": command, "cwd": str(cwd)}).encode()
        reader, writer = os.pipe()
        supervisor = None
        try:
            with os.fdopen(writer, "wb") as payload:
                supervisor = subprocess.Popen(
                    [sys.executable, "-B", str(Path(__file__).resolve()),
                     "--supervise", str(fd), str(request), str(reader)],
                    pass_fds=(fd, reader), start_new_session=True,
                )
                os.close(reader)
                reader = -1
                payload.write(encoded)
        except OSError:
            # Close the writer before waiting, so a failed/partial payload has EOF.
            if supervisor is not None:
                supervisor.wait()
            raise
        finally:
            if reader >= 0:
                os.close(reader)
        if supervisor.wait() != 0:
            raise RuntimeError("build supervision failed; inspect the private request receipts")
        return json.loads((request / "finished.json").read_text())["exit_code"]


def main() -> int:
    if len(sys.argv) == 5 and sys.argv[1] == "--supervise":
        return _supervisor_main(int(sys.argv[2]), Path(sys.argv[3]), int(sys.argv[4]))
    parser = argparse.ArgumentParser(description=__doc__)
    actions = parser.add_subparsers(dest="action", required=True)
    setup = actions.add_parser("init")
    setup.add_argument("resource", type=Path)
    execute = actions.add_parser("run")
    execute.add_argument("--resource", required=True, type=Path)
    execute.add_argument("--requests", required=True, type=Path)
    execute.add_argument("--task", required=True, choices=sorted(TASKS))
    execute.add_argument("--kind", required=True, choices=sorted(KINDS))
    execute.add_argument("--input-sha256", required=True)
    execute.add_argument("--cwd", required=True, type=Path)
    execute.add_argument("--timeout-seconds", type=float, default=600)
    execute.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.action == "init":
        initialize(args.resource)
        return 0
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if command[:3] != ["mise", "exec", "--"] or len(command) < 4:
        parser.error("build command must start with mise exec --")
    os.environ["MISE_DISABLE_TOOLS"] = "rust"
    return run(args.resource, args.requests, args.task, args.kind, args.input_sha256,
               command, args.cwd, args.timeout_seconds)


if __name__ == "__main__":
    raise SystemExit(main())
