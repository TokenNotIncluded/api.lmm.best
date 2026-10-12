#!/usr/bin/env python3
"""Pinned pgroll installation and a self-contained, disposable PostgreSQL rehearsal.

This entrypoint deliberately accepts no database URL and never sources .env.
It does not alter Go startup, deployment ownership or production schema gates.
"""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tempfile
import urllib.request
import uuid
from concurrent.futures import ThreadPoolExecutor

ROOT = Path(__file__).resolve().parent.parent
LOCK_PATH = ROOT / "tools/pgroll.lock.json"
MIGRATION = ROOT / "migrations/pgroll/rehearsal/001_amount_as_text.json"
INTERFACE = MIGRATION.with_name("000_versioned_interface.json")
MAX_DOWNLOAD = 64 * 1024 * 1024


class PgrollError(RuntimeError):
    pass


def sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        while chunk := source.read(1024 * 1024):
            digest.update(chunk)
    return digest.hexdigest()


def load_lock():
    lock = json.loads(LOCK_PATH.read_text())
    if (lock.get("format") != 1 or
            not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", lock.get("version", "")) or
            lock.get("repository") != "https://github.com/xataio/pgroll"):
        raise PgrollError("invalid pgroll lock")
    for value in [lock["checksums_sha256"]] + [a["sha256"] for a in lock["assets"].values()]:
        if not re.fullmatch(r"[0-9a-f]{64}", value):
            raise PgrollError("invalid pinned checksum")
    return lock


def asset_for(lock, system=None, machine=None):
    system = (system or platform.system()).lower()
    machine = (machine or platform.machine()).lower()
    architecture = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(machine)
    key = f"{system}-{architecture}"
    if key not in lock["assets"]:
        raise PgrollError(f"unsupported pgroll platform: {system}/{machine}")
    asset = lock["assets"][key]
    if not re.fullmatch(r"pgroll\.(linux|macos)\.(amd64|arm64)", asset["name"]):
        raise PgrollError("invalid pgroll asset name")
    return asset


def xdg_directory(variable, fallback):
    path = Path(os.environ.get(variable) or fallback).expanduser()
    if not path.is_absolute():
        raise PgrollError(f"{variable} must be an absolute path")
    return path


def binary_path(lock):
    return xdg_directory("XDG_CACHE_HOME", Path.home() / ".cache") / "lmm-api/tools/pgroll" / lock["version"] / asset_for(lock)["name"]


def verify_binary(path, asset):
    if path.is_symlink() or not path.is_file() or sha256(path) != asset["sha256"]:
        raise PgrollError("pgroll binary does not match its pinned checksum; remove the invalid cache file and install again")
    if not os.access(path, os.X_OK):
        raise PgrollError("pinned pgroll binary is not executable")
    return path


def download(url, destination):
    request = urllib.request.Request(url, headers={"User-Agent": "lmm-api-pgroll-installer"})
    size = 0
    with urllib.request.urlopen(request, timeout=60) as response, destination.open("wb") as target:
        while chunk := response.read(1024 * 1024):
            size += len(chunk)
            if size > MAX_DOWNLOAD:
                raise PgrollError("pgroll download exceeds size limit")
            target.write(chunk)
        target.flush()
        os.fsync(target.fileno())


def install(lock):
    asset = asset_for(lock)
    target = binary_path(lock)
    if target.exists() or target.is_symlink():
        return verify_binary(target, asset)
    target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    base = f'{lock["repository"]}/releases/download/v{lock["version"]}'
    with tempfile.TemporaryDirectory(prefix=".download-", dir=target.parent) as temporary:
        temporary = Path(temporary)
        checksums = temporary / "checksums.txt"
        download(f'{base}/pgroll_{lock["version"]}_checksums.txt', checksums)
        if sha256(checksums) != lock["checksums_sha256"]:
            raise PgrollError("official checksum manifest does not match the lock")
        entries = {}
        for line in checksums.read_text().splitlines():
            digest, name = line.split(maxsplit=1)
            entries[name.lstrip("*")] = digest
        if entries.get(asset["name"]) != asset["sha256"]:
            raise PgrollError("official asset checksum does not match the lock")
        candidate = temporary / "pgroll"
        download(f'{base}/{asset["name"]}', candidate)
        if sha256(candidate) != asset["sha256"]:
            raise PgrollError("downloaded pgroll binary checksum mismatch")
        candidate.chmod(0o755)
        # All accepted bytes are pinned; concurrent installers produce the same file.
        os.replace(candidate, target)
    return verify_binary(target, asset)


def clean_environment():
    # Do not inherit SQL_DSN, LOG_SQL_DSN, PG*, PGROLL*, service files, options,
    # application secrets or a caller's database defaults.
    keys = ("PATH", "HOME", "LANG", "LC_ALL", "TMPDIR", "SYSTEMROOT")
    return {key: os.environ[key] for key in keys if key in os.environ} | {"LC_ALL": "C"}


def postgres_tools(directory=None):
    tools = {}
    major = None
    for name in ("initdb", "pg_ctl", "psql"):
        executable = Path(directory) / name if directory else Path(shutil.which(name) or "/missing")
        if not executable.is_file() or not os.access(executable, os.X_OK):
            raise PgrollError(f"PostgreSQL tool missing: {name}; use --pg-bin DIRECTORY")
        executable = executable.resolve()
        result = subprocess.run([str(executable), "--version"], env=clean_environment(), text=True, capture_output=True, check=True)
        match = re.search(r"PostgreSQL\) (\d+)\.", result.stdout)
        if not match or int(match[1]) < 17:
            raise PgrollError("the local rehearsal requires PostgreSQL 17 or later")
        if major is not None and major != int(match[1]):
            raise PgrollError("PostgreSQL tools must have the same major version")
        major = int(match[1])
        tools[name] = str(executable)
        if name == "psql":
            tools["version"] = result.stdout.strip()
    return tools


def private_write(path, value):
    with path.open("x", encoding="utf-8") as target:
        os.chmod(path, 0o600)
        target.write(value)


class Rehearsal:
    def __init__(self, binary, tools, evidence, working_directory):
        self.binary, self.tools, self.evidence = binary, tools, evidence
        self.working_directory = working_directory
        home = working_directory / "home"
        home.mkdir(mode=0o700)
        configuration = home / ".config"
        configuration.mkdir(mode=0o700)
        self.environment = clean_environment()
        # v0.16.3 does not read Viper config files, but isolation must not depend
        # on the caller's cwd, HOME, .pgpass or future configuration discovery.
        self.environment |= {"HOME": str(home), "XDG_CONFIG_HOME": str(configuration)}
        self.sequence = 0
        self.steps = []

    def run(self, name, command, sql=None, allow_failure=False):
        self.sequence += 1
        result = subprocess.run(command, input=sql, env=self.environment, text=True,
                                cwd=self.working_directory, capture_output=True, timeout=90)
        stem = f"{self.sequence:02d}-{name}"
        for suffix, value in (("stdout", result.stdout), ("stderr", result.stderr)):
            private_write(self.evidence / f"{stem}.{suffix}.log", value)
        self.steps.append({"name": name, "exit_code": result.returncode})
        if result.returncode and not allow_failure:
            raise PgrollError(f"rehearsal step {name} failed; inspect its private evidence logs")
        return result

    def sql(self, name, sql, allow_failure=False):
        command = [self.tools["psql"], "-X", "--no-password", "-qAt", "-v", "ON_ERROR_STOP=1"]
        return self.run(name, command, sql, allow_failure)

    def expect(self, name, sql, expected):
        actual = self.sql(name, sql).stdout.strip()
        if actual != str(expected):
            raise PgrollError(f"rehearsal assertion {name} failed")

    def pgroll(self, name, *arguments, allow_failure=False):
        return self.run(name, [str(self.binary), *arguments], allow_failure=allow_failure)

    def status(self, name, version, expected):
        status = json.loads(self.pgroll(name, "status").stdout)
        if status.get("schema") != "public" or status.get("version") != version or status.get("status") != expected:
            raise PgrollError(f"rehearsal pgroll status {name} mismatch")

    def overlap_writes(self, old, new):
        # Two independent connections update separate rows while both versions exist.
        # This checks the trigger mapping, not a production latency/load SLO.
        def writer(schema, row, expression):
            sql = f"SET search_path TO {schema};\n" + "\n".join(
                f"UPDATE pgroll_fixture_items SET amount={expression} WHERE id={row};" for _ in range(12))
            return subprocess.run([self.tools["psql"], "-X", "--no-password", "-qAt", "-v", "ON_ERROR_STOP=1"],
                                  input=sql, env=self.environment, text=True, cwd=self.working_directory,
                                  capture_output=True, timeout=30)
        with ThreadPoolExecutor(max_workers=2) as pool:
            futures = [pool.submit(writer, old, 1001, "amount+1"),
                       pool.submit(writer, new, 1002, "(amount::integer+1)::text")]
            for index, future in enumerate(futures):
                result = future.result()
                private_write(self.evidence / f"overlap-{index}.stdout.log", result.stdout)
                private_write(self.evidence / f"overlap-{index}.stderr.log", result.stderr)
                self.steps.append({"name": f"overlap-writer-{index}", "exit_code": result.returncode})
                if result.returncode:
                    raise PgrollError("dual-version concurrent writes failed")


def rehearse(lock, args):
    if os.geteuid() == 0:
        raise PgrollError("run the disposable PostgreSQL rehearsal as an ordinary user")
    binary = verify_binary(binary_path(lock), asset_for(lock))
    tools = postgres_tools(args.pg_bin)
    if args.evidence:
        evidence = Path(args.evidence).expanduser().absolute()
    else:
        stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        evidence = xdg_directory("XDG_STATE_HOME", Path.home() / ".local/state") / "lmm-api/pgroll-rehearsals" / f"{stamp}-{uuid.uuid4().hex[:8]}"
    evidence.parent.mkdir(parents=True, exist_ok=True)
    evidence.mkdir(mode=0o700)  # Existing evidence is never overwritten.
    fixture = Path(tempfile.mkdtemp(prefix="lmm-pgr-", dir="/tmp"))
    data, sockets = fixture / "data", fixture / "socket"
    sockets.mkdir(mode=0o700)
    run = Rehearsal(binary, tools, evidence, fixture)
    report = {"format": 1, "scope": "disposable local cluster only", "pgroll_version": lock["version"],
              "pgroll_sha256": sha256(binary),
              "wrapper_sha256": sha256(Path(__file__)), "lock_sha256": sha256(LOCK_PATH),
              "migration_sha256": {path.name: sha256(path) for path in (INTERFACE, MIGRATION)},
              "postgres_version": tools["version"], "passed": False, "checks": []}
    error = None
    try:
        run.run("initdb", [tools["initdb"], "-D", str(data), "-U", "lmm_pgroll_fixture", "-A", "trust", "--encoding=UTF8", "--locale=C", "--no-instructions"])
        run.run("start-postgres", [tools["pg_ctl"], "-D", str(data), "-l", str(fixture / "postgres.log"),
                                  "-o", f"-c listen_addresses= -c unix_socket_directories={sockets} -c port=55439", "-w", "start"])
        run.environment |= {"PGHOST": str(sockets), "PGPORT": "55439", "PGUSER": "lmm_pgroll_fixture", "PGDATABASE": "postgres"}
        run.expect("no-tcp-listener", "SHOW listen_addresses;", "")
        report["postgres_server_version"] = run.sql("server-version", "SHOW server_version;").stdout.strip()
        run.sql("create-fixture-database", "CREATE DATABASE lmm_pgroll_fixture;")
        run.environment["PGDATABASE"] = "lmm_pgroll_fixture"
        run.environment |= {"PGROLL_PG_URL": f"postgresql://lmm_pgroll_fixture@/lmm_pgroll_fixture?host={sockets}&port=55439&sslmode=disable",
                            "PGROLL_SCHEMA": "public", "PGROLL_STATE_SCHEMA": "pgroll", "PGROLL_LOCK_TIMEOUT": "500"}
        run.sql("seed-existing-schema", "CREATE TABLE public.pgroll_fixture_items (id integer PRIMARY KEY, amount integer NOT NULL); INSERT INTO public.pgroll_fixture_items SELECT id,id*10 FROM generate_series(1,128) AS id;")
        run.pgroll("pgroll-init", "init")
        run.pgroll("baseline-existing-schema", "baseline", "000_fixture_existing", str(fixture / "baseline"), "--json", "--yes")
        # baseline records existing state but does not create version views.
        # A native empty migration establishes the old application interface.
        run.pgroll("validate-versioned-interface", "validate", str(INTERFACE))
        run.pgroll("start-versioned-interface", "start", str(INTERFACE))
        run.pgroll("complete-versioned-interface", "complete")
        old = "public_000_versioned_interface"
        new = "public_001_amount_as_text"
        run.expect("baseline-version", f"SELECT count(*) FROM {old}.pgroll_fixture_items;", 128)
        run.pgroll("validate-migration", "validate", str(MIGRATION))
        run.pgroll("start-migration", "start", str(MIGRATION), "--backfill-batch-size", "32")
        run.expect("old-new-column-types", f"SELECT data_type FROM information_schema.columns WHERE table_name='pgroll_fixture_items' AND column_name='amount' AND table_schema IN ('{old}','{new}') ORDER BY table_schema;", "integer\ntext")
        run.expect("backfill", f"SELECT count(*) FROM {new}.pgroll_fixture_items WHERE amount::integer=id*10;", 128)
        run.status("in-progress-status", "001_amount_as_text", "In progress")
        run.sql("old-new-inserts", f"SET search_path TO {old}; INSERT INTO pgroll_fixture_items (id,amount) VALUES (1001,17); SET search_path TO {new}; INSERT INTO pgroll_fixture_items (id,amount) VALUES (1002,'23');")
        run.expect("old-sees-new-write", f"SELECT amount FROM {old}.pgroll_fixture_items WHERE id=1002;", 23)
        run.expect("new-sees-old-write", f"SELECT amount FROM {new}.pgroll_fixture_items WHERE id=1001;", 17)
        run.overlap_writes(old, new)
        run.expect("overlap-old-value", f"SELECT amount FROM {new}.pgroll_fixture_items WHERE id=1001;", 29)
        run.expect("overlap-new-value", f"SELECT amount FROM {old}.pgroll_fixture_items WHERE id=1002;", 35)
        run.expect("view-upsert-returning", f"SET search_path TO {new}; INSERT INTO pgroll_fixture_items (id,amount) VALUES (1002,'41') ON CONFLICT(id) DO UPDATE SET amount=excluded.amount RETURNING amount;", 41)
        run.expect("view-do-nothing", f"SET search_path TO {old}; WITH ignored AS (INSERT INTO pgroll_fixture_items (id,amount) VALUES (1002,99) ON CONFLICT DO NOTHING RETURNING id) SELECT count(*) FROM ignored;", 0)
        run.expect("upsert-reaches-old-version", f"SELECT amount FROM {old}.pgroll_fixture_items WHERE id=1002;", 41)
        # Views have no physical indexes; current Go verification must not be bypassed.
        run.expect("version-view-kind", f"SELECT relkind FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='{new}' AND c.relname='pgroll_fixture_items';", "v")
        run.expect("version-has-no-physical-index", f"SELECT count(*) FROM pg_catalog.pg_indexes WHERE schemaname='{new}' AND tablename='pgroll_fixture_items';", 0)
        truncate = run.sql("truncate-view-rejected", f"BEGIN; TRUNCATE {new}.pgroll_fixture_items; ROLLBACK;", allow_failure=True)
        if not truncate.returncode or "not a table" not in truncate.stderr:
            raise PgrollError("expected TRUNCATE view incompatibility was not observed")
        run.pgroll("rollback-active-migration", "rollback")
        run.status("rolled-back-status", "000_versioned_interface", "Complete")
        run.expect("rollback-removes-new-version", f"SELECT count(*) FROM pg_catalog.pg_namespace WHERE nspname='{new}';", 0)
        run.expect("rollback-preserves-new-writes", f"SELECT amount FROM {old}.pgroll_fixture_items WHERE id IN (1001,1002) ORDER BY id;", "29\n41")
        run.pgroll("restart-migration", "start", str(MIGRATION), "--backfill-batch-size", "32")
        run.expect("restarted-backfill", f"SELECT amount FROM {new}.pgroll_fixture_items WHERE id IN (1001,1002) ORDER BY id;", "29\n41")
        # All fixture writers are finished before retiring the old version.
        run.pgroll("complete-migration", "complete")
        run.status("complete-status", "001_amount_as_text", "Complete")
        run.expect("complete-removes-old-version", f"SELECT count(*) FROM pg_catalog.pg_namespace WHERE nspname='{old}';", 0)
        run.expect("complete-preserves-new-version", f"SELECT count(*) FROM {new}.pgroll_fixture_items;", 130)
        run.expect("complete-new-writer", f"SET search_path TO {new}; INSERT INTO pgroll_fixture_items (id,amount) VALUES (1003,'47') RETURNING amount;", 47)
        run.expect("complete-physical-type", "SELECT data_type FROM information_schema.columns WHERE table_schema='public' AND table_name='pgroll_fixture_items' AND column_name='amount';", "text")
        rejected_rollback = run.pgroll("rollback-completed-migration-rejected", "rollback", allow_failure=True)
        if not rejected_rollback.returncode:
            raise PgrollError("completed migration unexpectedly allowed rollback")
        run.expect("rejected-rollback-preserves-data", f"SELECT amount FROM {new}.pgroll_fixture_items WHERE id=1003;", 47)
        report["checks"] = ["existing-schema-baseline", "type-change-backfill", "dual-version-read-write", "concurrent-writers",
                            "view-upsert-returning", "view-do-nothing", "active-rollback-preserves-writes", "restart-after-rollback",
                            "complete-retires-old-version", "new-version-write-after-complete", "completed-rollback-rejected",
                            "physical-catalog-incompatibility-observed", "truncate-view-incompatibility-observed"]
        report["passed"] = True
    except Exception as caught:
        error = caught
        report["error"] = str(caught)
    finally:
        if (data / "postmaster.pid").exists():
            try:
                run.run("stop-postgres", [tools["pg_ctl"], "-D", str(data), "-m", "fast", "-w", "stop"])
            except Exception as caught:
                error = caught
                report["passed"] = False
                report["error"] = "fixture shutdown failed; temporary cluster retained"
                report["retained_fixture"] = str(fixture)
        # Stop first even when evidence storage is unavailable.
        try:
            if (fixture / "postgres.log").exists():
                private_write(evidence / "postgres.log", (fixture / "postgres.log").read_text())
        except OSError as caught:
            error = caught
            report["passed"] = False
            report["error"] = "could not retain PostgreSQL server log"
        if not (data / "postmaster.pid").exists():
            shutil.rmtree(fixture)
            report["fixture_removed"] = True
        report["steps"] = run.steps
        private_write(evidence / "result.json", json.dumps(report, indent=2) + "\n")
        print(f"pgroll rehearsal evidence: {evidence}")
    if error:
        raise PgrollError(str(error)) from error
    print(f"pgroll {lock['version']} rehearsal passed on {tools['version']}")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    actions = parser.add_subparsers(dest="action", required=True)
    actions.add_parser("install", help="download and verify the locked official binary")
    actions.add_parser("version", help="verify the cached binary and print its version/path")
    fixture = actions.add_parser("rehearse", help="exercise pgroll in a new temporary PostgreSQL cluster only")
    fixture.add_argument("--pg-bin", type=Path, help="directory containing PostgreSQL 17+ initdb, pg_ctl and psql")
    fixture.add_argument("--evidence", type=Path, help="new private evidence directory (must not exist)")
    args = parser.parse_args(argv)
    lock = load_lock()
    if args.action == "install":
        print(install(lock))
    elif args.action == "version":
        binary = verify_binary(binary_path(lock), asset_for(lock))
        subprocess.run([str(binary), "--version"], env=clean_environment(), check=True)
        print(binary)
    else:
        rehearse(lock, args)


if __name__ == "__main__":
    try:
        main()
    except (PgrollError, OSError, ValueError, subprocess.SubprocessError) as error:
        print(f"pgroll: {error}", file=sys.stderr)
        sys.exit(1)
