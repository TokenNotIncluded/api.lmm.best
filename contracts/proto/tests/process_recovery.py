#!/usr/bin/env python3
"""Actual Unix-socket gRPC and two PostgreSQL databases. No money business fixture.

Requires built Rust and Go fixture executables, psql, and a disposable local
PostgreSQL administrator DATABASE_URL. Creates and drops only random mk06_* DBs.
"""
from __future__ import annotations

import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
from urllib.parse import urlsplit, urlunsplit

ROOT = Path(__file__).resolve().parents[3]
RUST = ROOT / "apps/core-rust/target/debug/examples/protocol-events-fixture"
GO = Path(os.environ["MK06_GO_FIXTURE"])
ADMIN = os.environ["DATABASE_URL"]
URL = urlsplit(ADMIN)
if URL.hostname not in {"127.0.0.1", "localhost"}:
    raise SystemExit("only local disposable PostgreSQL is permitted")
if not RUST.is_file() or not GO.is_file():
    raise SystemExit("build both fixtures before running this test")


def sql(url: str, statement: str) -> str:
    result = subprocess.run(
        ["psql", url, "-XAt", "-v", "ON_ERROR_STOP=1", "-c", statement],
        text=True, capture_output=True, timeout=10,
    )
    if result.returncode:
        raise RuntimeError("fixture SQL failed: " + result.stderr)
    return result.stdout.strip()


def db_url(name: str) -> str:
    return urlunsplit(URL._replace(path="/" + name, query="sslmode=disable"))


def until(check, label: str, timeout: float = 25) -> None:
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if check():
            return
        time.sleep(0.1)
    raise AssertionError("timeout: " + label)


def stop(process: subprocess.Popen | None) -> None:
    if process is not None and process.poll() is None:
        process.kill()
        process.wait(timeout=5)


def main() -> None:
    suffix = secrets.token_hex(6)
    core_name, ext_name, role = ("mk06_" + part + "_" + suffix for part in ("core", "ext", "reader"))
    password = secrets.token_hex(24)
    core_url = db_url(core_name)
    ext_url = db_url(ext_name)
    limited_ext_url = urlunsplit(URL._replace(
        netloc=f"{role}:{password}@{URL.hostname}:{URL.port or 5432}",
        path="/" + ext_name, query="sslmode=disable",
    ))
    rust = watcher = crashed = None
    # Private, short UDS path; no credentials are written into test logs.
    with tempfile.TemporaryDirectory(prefix="mk06-", dir="/tmp") as directory:
        path = Path(directory)
        token_file, user_file = path / "service", path / "user"
        token_file.write_text(secrets.token_hex(32))
        token_file.chmod(0o600)
        common = dict(os.environ, LMM_EVENTS_FIXTURE="1", MK06_SOCKET=str(path / "control.sock"),
                      MK06_SERVICE_FILE=str(token_file), MK06_USER_FILE=str(user_file),
                      MK06_EXTENSION_URL=limited_ext_url, MK06_MARKER=str(path / "marker"),
                      MK06_RECEIPT=str(path / "receipt"))
        core_env = dict(common, DATABASE_URL=core_url)
        go_env = dict(common)
        go_env.pop("DATABASE_URL", None)  # Go never receives the core SQL credential.
        logs = []

        def command(binary: Path, args: list[str], env: dict) -> None:
            result = subprocess.run([str(binary), *args], env=env, capture_output=True, text=True, timeout=35)
            if result.returncode:
                raise AssertionError(f"{binary.name} {args[0]} failed:\n{result.stdout}\n{result.stderr}")
            print(result.stdout.strip(), flush=True)

        def start(binary: Path, args: list[str], env: dict) -> subprocess.Popen:
            log = (path / f"process-{len(logs)}.log").open("w+")
            logs.append(log)
            return subprocess.Popen([str(binary), *args], env=env, stdout=log, stderr=subprocess.STDOUT)

        def core_start() -> subprocess.Popen:
            p = start(RUST, ["serve"], core_env)
            def ready() -> bool:
                if p.poll() is not None:
                    raise AssertionError("Rust fixture stopped before socket readiness")
                try:
                    with socket.socket(socket.AF_UNIX) as sock:
                        sock.settimeout(0.2)
                        sock.connect(common["MK06_SOCKET"])
                    return True
                except OSError:
                    return False
            until(ready, "Rust Unix socket readiness")
            return p

        def amount() -> int:
            return int(sql(ext_url, "SELECT amount FROM fixture_effects WHERE singleton"))

        def pending() -> int:
            return int(sql(core_url, "SELECT pending FROM core_events.subscriptions WHERE consumer_id='fixture.consumer'"))

        try:
            sql(ADMIN, f"CREATE ROLE {role} LOGIN PASSWORD '{password}'")
            sql(ADMIN, f"CREATE DATABASE {core_name}")
            sql(ADMIN, f"REVOKE CONNECT ON DATABASE {core_name} FROM PUBLIC")
            sql(ADMIN, f"CREATE DATABASE {ext_name} OWNER {role}")
            command(RUST, ["init"], core_env)
            command(GO, ["setup"], go_env)
            rust = core_start()
            command(GO, ["auth-wire"], go_env)
            command(RUST, ["publish", "fixture-first"], core_env)
            crashed = start(GO, ["crash-during-handler"], go_env)
            until(lambda: Path(common["MK06_MARKER"]).exists(), "Go entered uncommitted SQL transaction")
            stop(crashed)
            until(lambda: amount() == 0, "Go crash rolls back uncommitted effect")
            assert sql(ext_url, "SELECT count(*) FROM extension_events.inbox") == "0"
            assert rust.poll() is None
            print("PASS SIGKILL Go during handler rolls back effect and inbox; Rust remains alive", flush=True)
            command(GO, ["commit-no-ack"], go_env)
            assert amount() == 1 and pending() == 1
            stop(rust)
            rust = core_start()
            command(GO, ["redeliver-ack"], go_env)
            assert amount() == 1 and pending() == 0
            stop(rust)
            rust = core_start()
            command(GO, ["repeat-ack"], go_env)
            Path(common["MK06_MARKER"]).unlink()
            watcher = start(GO, ["watch"], go_env)
            until(lambda: Path(common["MK06_MARKER"]).exists(), "long-lived Go consumer start")
            watcher_pid = watcher.pid
            time.sleep(0.4)
            stop(rust)
            command(RUST, ["publish", "fixture-during-core-restart"], core_env)
            rust = core_start()
            until(lambda: amount() == 2 and pending() == 0, "same Go process reconnects and ACKs")
            assert watcher.pid == watcher_pid and watcher.poll() is None
            print("PASS same Go client process reconnects after Rust SIGKILL and restart", flush=True)
            stop(watcher)
            assert rust.poll() is None
            command(RUST, ["publish", "fixture-while-go-offline"], core_env)
            assert pending() == 1
            command(GO, ["drain"], go_env)
            assert amount() == 3 and pending() == 0
            assert sql(core_url, "SELECT pending_events||':'||pending_bytes FROM core_events.capacity") == "0:0"
            print("PASS Rust publishes while Go is offline; no duplicate local effects; backlog released", flush=True)
            print("All process recovery checks passed (fixture events only, not ledger acceptance).", flush=True)
        finally:
            for process in (crashed, watcher, rust):
                stop(process)
            for log in logs:
                log.flush()
                log.seek(0)
                print(log.read(), end="")
                log.close()
            # These generated names cannot refer to an existing application DB.
            for name in (ext_name, core_name):
                sql(ADMIN, f"DROP DATABASE IF EXISTS {name} WITH (FORCE)")
            sql(ADMIN, f"DROP ROLE IF EXISTS {role}")


if __name__ == "__main__":
    main()
