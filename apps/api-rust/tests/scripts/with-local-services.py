#!/usr/bin/env python3
"""Run a test command against newly created, loopback-only PostgreSQL and Valkey.

No existing database, daemon, container, or inherited service URL is reused.
Logs remain under --output-dir; only processes started here are stopped.
"""

import argparse
import contextlib
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time


def clean_service_environment():
    """Discard libpq and inherited database/cache destinations before any I/O."""
    env = dict(os.environ)
    for key in list(env):
        # PGHOSTADDR can override the address selected by psql's explicit -h;
        # service files and PGOPTIONS can also redirect or mutate test setup.
        if key.startswith("PG") or key in ("REDISCLI_AUTH", "VALKEYCLI_AUTH"):
            del env[key]
        elif ("TEST" in key and any(word in key for word in ("DATABASE", "POSTGRES", "VALKEY", "REDIS"))) or key in (
            "DATABASE_URL", "POSTGRES_URL", "SQL_DSN", "LOG_SQL_DSN", "VALKEY_URL", "REDIS_URL", "REDIS_CONN_STRING",
        ):
            del env[key]
    return env


def free_port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def stop(process):
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)


class LocalServices:
    def __init__(self, output):
        self.output = Path(output).resolve()
        self.output.mkdir(parents=True, exist_ok=True)
        self.runtime = Path(tempfile.mkdtemp(prefix="services-", dir=self.output))
        self.processes = []
        self.logs = []
        self.password = secrets.token_hex(24)
        self.pg_port = free_port()
        self.valkey_port = free_port()
        while self.valkey_port == self.pg_port:
            self.valkey_port = free_port()
        self.role = "lmm_test_parity"
        self.database = "lmm_test_parity"
        self.database_url = (
            f"postgresql://{self.role}:{self.password}@127.0.0.1:"
            f"{self.pg_port}/{self.database}"
        )
        self.valkey_url = f"redis://:{self.password}@127.0.0.1:{self.valkey_port}/0"

    def launch(self, args, name):
        log = (self.runtime / f"{name}.log").open("wb")
        self.logs.append(log)
        process = subprocess.Popen(args, stdout=log, stderr=subprocess.STDOUT, env=clean_service_environment())
        self.processes.append(process)
        return process

    def sql(self, sql, database=None):
        database = database or self.database
        if re.fullmatch(r"[a-z_][a-z0-9_]{0,62}", database) is None:
            raise ValueError("local database must be a plain isolated database name")
        env = dict(clean_service_environment(), PGPASSWORD=self.password, PGCONNECT_TIMEOUT="2", PGSSLMODE="disable")
        return subprocess.run(
            ["psql", "-X", "-h", "127.0.0.1", "-p", str(self.pg_port),
             "-U", self.role, "-d", database,
             "-v", "ON_ERROR_STOP=1", "-At"],
            input=sql, text=True, capture_output=True, env=env, check=True,
        ).stdout

    def start(self):
        for command in ("initdb", "postgres", "psql", "valkey-server", "valkey-cli"):
            if not shutil.which(command):
                raise RuntimeError(f"missing executable: {command}")
        version = subprocess.check_output(["postgres", "--version"], text=True)
        if "PostgreSQL) 18." not in version:
            raise RuntimeError("these integration contracts require PostgreSQL 18")
        password_file = self.runtime / "postgres-password"
        password_file.write_text(self.password + "\n")
        password_file.chmod(0o600)
        with (self.runtime / "initdb.log").open("wb") as log:
            subprocess.run(
                ["initdb", "--no-locale", "--encoding=UTF8", "--auth=scram-sha-256",
                 f"--username={self.role}", f"--pwfile={password_file}",
                 "-D", str(self.runtime / "postgres")],
                check=True, stdout=log, stderr=subprocess.STDOUT, env=clean_service_environment(),
            )
        postgres = self.launch(
            ["postgres", "-D", str(self.runtime / "postgres"), "-h", "127.0.0.1",
             "-p", str(self.pg_port), "-k", "",
             "-c", "shared_buffers=64MB", "-c", "max_connections=80"], "postgres",
        )
        deadline = time.monotonic() + 30
        while True:
            try:
                if postgres.poll() is not None:
                    raise RuntimeError(f"PostgreSQL exited during startup; see {self.runtime}")
                data_directory = self.sql("SHOW data_directory;", "postgres").strip()
                if postgres.poll() is not None or Path(data_directory).resolve() != (self.runtime / "postgres").resolve():
                    raise RuntimeError("PostgreSQL readiness reached a different process/data directory")
                break
            except subprocess.CalledProcessError:
                if postgres.poll() is not None or time.monotonic() >= deadline:
                    raise RuntimeError(f"PostgreSQL did not start; see {self.runtime}")
                time.sleep(0.05)
        self.sql(f'CREATE DATABASE "{self.database}";', "postgres")
        # All destructive integration fixtures point at this disposable cluster.
        self.sql('CREATE DATABASE "lmm_test_adopt";', "postgres")
        valkey = self.launch(
            ["valkey-server", "--bind", "127.0.0.1", "--port", str(self.valkey_port),
             "--save", "", "--appendonly", "no", "--requirepass", self.password,
             "--dir", str(self.runtime)], "valkey",
        )
        deadline = time.monotonic() + 15
        while True:
            result = subprocess.run(
                ["valkey-cli", "-h", "127.0.0.1", "-p", str(self.valkey_port), "PING"],
                env=dict(clean_service_environment(), VALKEYCLI_AUTH=self.password),
                capture_output=True, text=True,
            )
            if valkey.poll() is None and result.returncode == 0 and result.stdout.strip() == "PONG":
                break
            if valkey.poll() is not None or time.monotonic() >= deadline:
                raise RuntimeError(f"Valkey did not start; see {self.runtime}")
            time.sleep(0.05)
        metadata = {
            "postgres_version": version.strip(), "postgres_port": self.pg_port,
            "valkey_version": subprocess.check_output(["valkey-server", "--version"], text=True).strip(),
            "valkey_port": self.valkey_port,
            "database": self.database, "loopback_only": True,
        }
        (self.runtime / "services.json").write_text(json.dumps(metadata, indent=2) + "\n")
        return self

    def environment(self):
        env = clean_service_environment()
        for name in ("AUTH", "MODELS", "API_TOKEN", "BILLING", "BILLING_SUBSCRIPTIONS",
                     "SYSTEM_CONFIG", "IDENTITY", "CHANNEL", "DEPLOYMENT", "CONTROL_PUBLIC",
                     "RELAY_MISC", "EPAY", "MANDATORY_ANNOUNCEMENTS", "RANKINGS"):
            env[f"LMM_{name}_TEST_DATABASE_URL"] = self.database_url
            env[f"LMM_{name}_TEST_VALKEY_URL"] = self.valkey_url
            env[f"LMM_{name}_TEST_ALLOW_SCHEMA_RESET"] = "1"
        env.update({
            "LMM_TEST_DATABASE_URL": self.database_url,
            "LMM_TEST_POSTGRES_URL": self.database_url,
            "LMM_TEST_ADOPT_DATABASE_URL": self.database_url.rsplit("/", 1)[0] + "/lmm_test_adopt",
            "TEST_DATABASE_URL": self.database_url,
            "TEST_POSTGRES_URL": self.database_url,
            "DATABASE_URL": self.database_url,
            "SQL_DSN": self.database_url,
            "LOG_SQL_DSN": self.database_url,
            "VALKEY_URL": self.valkey_url,
            "REDIS_CONN_STRING": self.valkey_url,
            "LMM_LOCAL_TEST_ARTIFACTS": str(self.runtime),
            "PGHOST": "127.0.0.1", "PGPORT": str(self.pg_port),
            "PGUSER": self.role, "PGPASSWORD": self.password,
            "PGDATABASE": self.database, "PGSSLMODE": "disable",
        })
        return env

    def close(self):
        for process in reversed(self.processes):
            stop(process)
        for log in self.logs:
            log.close()
        # Retain evidence, remove disposable data (never a caller-owned path).
        shutil.rmtree(self.runtime / "postgres", ignore_errors=True)
        (self.runtime / "postgres-password").unlink(missing_ok=True)


@contextlib.contextmanager
def local_services(output):
    services = LocalServices(output)
    try:
        yield services.start()
    finally:
        services.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command:
        parser.error("a test command is required after --")
    # SIGTERM should run the same cleanup as Ctrl-C.
    signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
    with local_services(args.output_dir) as services:
        print(f"isolated service evidence: {services.runtime}", flush=True)
        child = subprocess.Popen(command, env=services.environment())
        try:
            return child.wait()
        finally:
            stop(child)


if __name__ == "__main__":
    sys.exit(main())
