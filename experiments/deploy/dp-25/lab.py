#!/usr/bin/env python3
"""Local-only PostgreSQL 17 SQL-ledger experiment; NOT full business acceptance.

No DSN input, remote Docker, image pull, build, CI invocation, deployment, or
existing data volume is supported. Missing dependencies produce BLOCKED, not PASS.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import secrets
import shutil
import subprocess
import tempfile
import threading
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

HERE = Path(__file__).resolve().parent
BASE = "72667564c0431754d4856dc2e0db55f360bd2745"
LABEL = "lmm.dp25.run"
IMAGE_PATTERN = re.compile(r"(?:sha256:|[^\s]+@sha256:)[0-9a-f]{64}\Z")
SCHEMAS = {
    "apps/lmm-core/schema/identity.sql": "414580d81a99680b0aaa7f5678bd8512fae73e3f",
    "apps/lmm-core/schema/ledger.sql": "671d57be7064bb5f163f6f40df2e6f2898d2d299",
}


def now() -> str:
    return datetime.now(timezone.utc).isoformat()


def git_blob(data: bytes) -> str:
    return hashlib.sha1(b"blob " + str(len(data)).encode() + b"\0" + data).hexdigest()


def sql_literal(text: str) -> str:
    if "\0" in text:
        raise ValueError("NUL is not allowed in SQL literals")
    return "'" + text.replace("'", "''") + "'"


def ledger_request(key: str, action: dict) -> dict:
    if not re.fullmatch(r"[a-zA-Z0-9._:/-]{1,128}", key):
        raise ValueError("invalid test operation key")
    return {"scope": "dp25", "operation_key": key, "actor_user_id": None,
            "reason": "isolated database recovery test", "action": action}


def ledger_sql(request: dict) -> str:
    value = sql_literal(json.dumps(request, separators=(",", ":")))
    return ("BEGIN; SET TRANSACTION ISOLATION LEVEL READ COMMITTED; "
            "SET LOCAL lock_timeout='3s'; SET LOCAL statement_timeout='10s'; "
            "SET LOCAL synchronous_commit=on; "
            f"SELECT core_billing.post_ledger({value}::jsonb)::text; COMMIT;")


def preflight(source: Path, image: str | None, proxy: Path | None) -> dict:
    blockers = []
    if not shutil.which("docker"):
        blockers.append("Docker executable is absent")
    if not image or not IMAGE_PATTERN.fullmatch(image):
        blockers.append("a locally installed immutable PostgreSQL image digest is required")
    for env in ("GITHUB_ACTIONS", "GITLAB_CI", "TF_BUILD", "BUILDKITE"):
        if os.getenv(env, "").lower() not in {"", "0", "false"}:
            blockers.append("remote CI environments are prohibited")
    if os.getenv("DOCKER_HOST") and not os.environ["DOCKER_HOST"].startswith("unix://"):
        blockers.append("remote Docker endpoints are prohibited")
    schema_hashes = {}
    for relative, expected in SCHEMAS.items():
        path = source / relative
        try:
            if not path.resolve().is_relative_to(source.resolve()):
                raise ValueError("schema path leaves the checkout")
            digest = git_blob(path.read_bytes())
            schema_hashes[relative] = digest
            if digest != expected:
                blockers.append(f"source differs from the pinned baseline: {relative}")
        except (OSError, ValueError):
            blockers.append(f"pinned source file is unavailable: {relative}")
    if proxy is None or not proxy.is_file() or not os.access(proxy, os.X_OK):
        blockers.append("a locally built executable commit-drop proxy is required")
    result = {"at": now(), "base_commit": BASE, "status": "blocked" if blockers else "ready_for_local_probe",
              "blockers": blockers, "schema_blob_hashes": schema_hashes,
              "business_acceptance": False, "database_tests_executed": False}
    # These are environment observations, not PostgreSQL measurements.
    for field, path in (("environment_memory_limit", "/sys/fs/cgroup/memory.max"),
                        ("environment_cpu_limit", "/sys/fs/cgroup/cpu.max")):
        try:
            result[field] = Path(path).read_text().strip()
        except OSError:
            result[field] = None
    return result


class Lab:
    def __init__(self, source: Path, image: str, proxy: Path, out: Path):
        self.source, self.image, self.proxy, self.out = source, image, proxy, out
        self.token = "dp25-" + secrets.token_hex(8)
        self.owned: list[tuple[str, str]] = []
        self.servers: dict[str, str] = {}
        self.passwords = {r: secrets.token_hex(24) for r in
                          ("postgres", "dp25_core", "dp25_ro", "dp25_ext", "dp25_ops")}
        self.private = tempfile.TemporaryDirectory(prefix="dp25-secrets-")
        self.env_files: dict[str, Path] = {}
        self.cleanup_errors: list[str] = []
        for role, password in self.passwords.items():
            f = Path(self.private.name) / role
            f.write_text(f"PGPASSWORD={password}\nPGCONNECT_TIMEOUT=3\nPGSSLMODE=disable\nPGGSSENCMODE=disable\nPGAPPNAME=dp25-lab\n")
            f.chmod(0o600)
            self.env_files[role] = f
        self.server_env = Path(self.private.name) / "server"
        self.server_env.write_text("POSTGRES_DB=dp25_core\nPOSTGRES_USER=postgres\n"
                                   f"POSTGRES_PASSWORD={self.passwords['postgres']}\n"
                                   "POSTGRES_INITDB_ARGS=--auth-host=scram-sha-256\n")
        self.server_env.chmod(0o600)

    def redact(self, text: str) -> str:
        for password in self.passwords.values():
            text = text.replace(password, "[REDACTED]")
        return text

    def docker(self, args: list[str], *, data: str | bytes | None = None,
               timeout: int = 30, check: bool = True, binary: bool = False) -> subprocess.CompletedProcess:
        p = subprocess.run(["docker", *args], input=data, capture_output=True,
                           text=not binary, timeout=timeout, check=False)
        if check and p.returncode:
            err = p.stderr.decode(errors="replace") if binary else p.stderr
            raise RuntimeError(self.redact(err[-3000:]))
        return p

    def start(self) -> dict:
        context = json.loads(self.docker(["context", "inspect"]).stdout)[0]
        endpoint = context["Endpoints"]["docker"]["Host"]
        if not endpoint.startswith("unix://"):
            raise RuntimeError("only a local Unix-socket Docker daemon is allowed")
        info = json.loads(self.docker(["image", "inspect", self.image]).stdout)[0]
        if info.get("Os") != "linux":
            raise RuntimeError("this lab requires a local Linux PostgreSQL image")
        # --internal creates a new private network; no ports are published.
        self.docker(["network", "create", "--internal", "--label", f"{LABEL}={self.token}", self.token])
        self.owned.append(("network", self.token))
        self.start_server("db")
        return {k: info.get(k) for k in ("Id", "Architecture", "Os", "RepoDigests")}

    def start_server(self, alias: str) -> None:
        volume, name = f"{self.token}-{alias}-data", f"{self.token}-{alias}"
        self.docker(["volume", "create", "--label", f"{LABEL}={self.token}", volume])
        self.owned.append(("volume", volume))
        # Register before starting so a partial create is still safely cleaned.
        self.owned.append(("container", name))
        self.docker(["run", "--detach", "--pull=never", "--name", name,
                     "--label", f"{LABEL}={self.token}", "--network", self.token,
                     "--network-alias", alias, "--memory=256m", "--memory-swap=256m",
                     "--cpus=1", "--pids-limit=128", "--shm-size=128m",
                     "--security-opt=no-new-privileges:true", "--env-file", str(self.server_env),
                     "--mount", f"type=volume,src={volume},dst=/var/lib/postgresql/data",
                     "--mount", f"type=bind,src={HERE / 'postgresql.conf'},dst=/etc/dp25.conf,readonly",
                     self.image, "postgres", "-c", "config_file=/etc/dp25.conf"], timeout=60)
        self.servers[alias] = name
        desc = json.loads(self.docker(["inspect", name]).stdout)[0]
        host = desc["HostConfig"]
        if host["Memory"] != 256 * 1024**2 or host["MemorySwap"] != 256 * 1024**2 or host["NanoCpus"] != 10**9:
            raise RuntimeError("Docker did not accept the requested hard CPU/memory limits")
        if host.get("PortBindings"):
            raise RuntimeError("database ports must not be published")
        self.wait_ready(alias)

    def wait_ready(self, alias: str) -> float:
        start = time.monotonic()
        while time.monotonic() - start < 45:
            p = self.docker(["exec", self.servers[alias], "pg_isready", "-U", "postgres", "-d", "dp25_core"],
                            check=False, timeout=5)
            if p.returncode == 0:
                # pg_isready alone is not recovery success. Query a real connection.
                try:
                    if self.sql("SELECT 1;", role="postgres", host=alias).strip() == "1":
                        return time.monotonic() - start
                except (RuntimeError, subprocess.TimeoutExpired):
                    pass
            time.sleep(0.25)
        raise RuntimeError("database did not become SQL-ready within 45 seconds")

    def client(self, tool: str, args: list[str], *, role: str = "dp25_core", host: str = "db",
               database: str = "dp25_core", data: str | bytes | None = None,
               timeout: int = 30, check: bool = True, binary: bool = False) -> subprocess.CompletedProcess:
        name = self.token + "-client-" + secrets.token_hex(4)
        self.owned.append(("container", name))
        cmd = ["run", "--rm", "--interactive", "--pull=never", "--name", name,
               "--label", f"{LABEL}={self.token}", "--network", self.token,
               "--memory=128m", "--memory-swap=128m", "--cpus=1", "--pids-limit=64",
               "--read-only", "--tmpfs", "/tmp:rw,nosuid,noexec,size=16m",
               "--security-opt=no-new-privileges:true", "--env-file", str(self.env_files[role]),
               "--mount", f"type=bind,src={HERE / 'sql'},dst=/dp25-sql,readonly",
               "--entrypoint", tool, self.image, "-h", host, "-U", role]
        # pgbench uses a positional database name; -d enables debug output.
        cmd += [*args, database] if tool == "pgbench" else ["-d", database, *args]
        return self.docker(cmd, data=data, timeout=timeout, check=check, binary=binary)

    def sql(self, text: str, *, role: str = "dp25_core", host: str = "db",
            database: str = "dp25_core", check: bool = True) -> str:
        p = self.client("psql", ["-XqAt", "-v", "ON_ERROR_STOP=1", "-v", "VERBOSITY=sqlstate"],
                        role=role, host=host, database=database, data=text, check=check)
        if not check:
            return json.dumps({"returncode": p.returncode, "stdout": p.stdout,
                               "stderr": self.redact(p.stderr)})
        return p.stdout

    def roles(self, host: str = "db") -> None:
        statements = ["CREATE ROLE dp25_owner NOLOGIN;", "ALTER DATABASE dp25_core OWNER TO dp25_owner;"]
        for role, cap in (("dp25_core", 26), ("dp25_ro", 4), ("dp25_ext", 4), ("dp25_ops", 2)):
            statements.append(f"CREATE ROLE {role} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS "
                              f"CONNECTION LIMIT {cap} PASSWORD {sql_literal(self.passwords[role])};")
        statements += [
            "REVOKE CONNECT, TEMPORARY ON DATABASE dp25_core FROM PUBLIC;",
            "GRANT CONNECT ON DATABASE dp25_core TO dp25_core, dp25_ro, dp25_ops;",
            "GRANT pg_monitor, pg_use_reserved_connections TO dp25_ops;",
            "ALTER ROLE dp25_ro SET default_transaction_read_only=on;",
            "CREATE DATABASE dp25_extension OWNER dp25_ext;",
            "REVOKE CONNECT, TEMPORARY ON DATABASE dp25_extension FROM PUBLIC;",
            "GRANT CONNECT ON DATABASE dp25_extension TO dp25_ext;",
        ]
        self.sql("\n".join(statements), role="postgres", host=host)

    def initialize(self) -> None:
        self.roles()
        prefix = """BEGIN; SET ROLE dp25_owner;
ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
"""
        body = "\n".join((self.source / p).read_text() for p in SCHEMAS)
        suffix = """
CREATE SCHEMA dp25_lab;
CREATE SEQUENCE dp25_lab.operation_seq;
INSERT INTO core_identity.accounts(kind) VALUES ('personal');
GRANT USAGE ON SCHEMA core_billing, dp25_lab TO dp25_core;
GRANT SELECT ON core_billing.ledger_accounts, core_billing.balance_state,
  core_billing.ledger_operations, core_billing.ledger_journals, core_billing.ledger_entries TO dp25_core;
GRANT USAGE ON SEQUENCE dp25_lab.operation_seq TO dp25_core;
GRANT EXECUTE ON FUNCTION core_billing.open_ledger_wallet(bigint),core_billing.post_ledger(jsonb) TO dp25_core;
GRANT USAGE ON SCHEMA core_identity TO dp25_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA core_identity TO dp25_ro;
COMMIT;
"""
        self.sql(prefix + body + suffix, role="postgres")
        self.sql("SELECT core_billing.open_ledger_wallet(1);")

    def settings(self, host: str = "db") -> dict:
        settings = json.loads(self.sql("""SELECT jsonb_object_agg(name,setting)
          FROM pg_settings WHERE name IN ('server_version_num','max_connections','reserved_connections',
          'superuser_reserved_connections','fsync','full_page_writes','synchronous_commit',
          'shared_buffers','work_mem','hash_mem_multiplier','maintenance_work_mem','autovacuum_work_mem',
          'max_parallel_workers_per_gather','wal_buffers');""", role="postgres", host=host))
        if not 170000 <= int(settings["server_version_num"]) < 180000:
            raise RuntimeError("the SQL lab is pinned to PostgreSQL major 17")
        for field in ("fsync", "full_page_writes", "synchronous_commit"):
            if settings.get(field) != "on":
                raise RuntimeError("required durable-write setting is not enabled: " + field)
        return settings

    def post(self, request: dict, host: str = "db") -> dict:
        return json.loads(self.sql(ledger_sql(request), host=host))

    def snapshot(self, host: str = "db") -> dict:
        s = json.loads(self.sql((HERE / "sql/assert_ledger.sql").read_text(), host=host))
        if (s["net_units"] != "0" or s["unbalanced_journals"] or s["projection_mismatches"]
                or s["negative_customer_buckets"]):
            raise RuntimeError("real ledger conservation check failed")
        return s

    def metrics(self) -> dict:
        server = self.servers["db"]
        shell = """for n in memory.current memory.peak memory.stat memory.events cpu.stat io.stat; do
printf '\\nFILE %s\\n' "$n"; cat "/sys/fs/cgroup/$n" || exit 1; done
printf '\\nFILE pss_kib\\n'
for f in /proc/[0-9]*/smaps_rollup; do awk '/^Pss:/{print $2}' "$f" 2>/dev/null; done | awk '{s+=$1}END{print s}'
"""
        probe_start = time.monotonic()
        raw = self.docker(["exec", "--user", "0", server, "sh", "-c", shell], timeout=10).stdout
        chunks = re.split(r"\nFILE ", raw)[1:]
        cgroup = {s.split("\n", 1)[0]: s.split("\n", 1)[1].strip() for s in chunks}
        db_metrics = json.loads(self.sql((HERE / "sql/metrics.sql").read_text(), role="dp25_ops"))
        return {"at": now(), "postgres": db_metrics, "cgroup_raw": cgroup,
                "probe_wall_seconds": time.monotonic() - probe_start,
                "scope": "database container including in-container metrics probe, excluding client containers"}

    def unknown_commit(self) -> dict:
        name = self.token + "-commit-drop"
        self.owned.append(("container", name))
        self.docker(["run", "--detach", "--pull=never", "--name", name,
                     "--label", f"{LABEL}={self.token}", "--network", self.token, "--network-alias", "drop",
                     "--memory=32m", "--memory-swap=32m", "--cpus=0.25", "--pids-limit=32",
                     "--read-only", "--user", "65534:65534", "--cap-drop=ALL",
                     "--security-opt=no-new-privileges:true",
                     "--mount", f"type=bind,src={self.proxy.resolve()},dst=/commit-drop,readonly",
                     "--entrypoint", "/commit-drop", self.image, "-listen", ":5432", "-target", "db:5432"])
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            if '"event":"ready"' in self.docker(["logs", name]).stdout:
                break
            time.sleep(0.1)
        else:
            raise RuntimeError("commit-drop proxy did not become ready")
        req = ledger_request("unknown-commit", {"kind": "charge", "account_id": 1, "amount_units": 7})
        p = self.client("psql", ["-XqAt", "-v", "ON_ERROR_STOP=1"], host="drop",
                        data=ledger_sql(req), check=False)
        logs = self.docker(["logs", name]).stdout
        if p.returncode == 0 or '"event":"commit_command_complete_dropped"' not in logs:
            raise RuntimeError("COMMIT acknowledgement loss was not proven")
        # Direct authoritative query comes BEFORE any replay, never from a replica.
        stored = json.loads(self.sql("SELECT jsonb_build_object('request',request,'result',result)::text "
                                     "FROM core_billing.ledger_operations "
                                     "WHERE scope='dp25' AND operation_key='unknown-commit';"))
        if stored["request"] != req or stored["result"].get("status") != "posted":
            raise RuntimeError("authoritative unknown-commit lookup failed")
        before = self.snapshot()
        if self.post(req) != stored["result"] or self.snapshot() != before:
            raise RuntimeError("replay changed the ledger")
        return {"status": "passed", "client_returncode": p.returncode,
                "commit_ack_dropped": True, "authoritative_lookup_before_replay": True,
                "replay_changed_ledger": False, "receipt": stored["result"]}

    def close(self) -> None:
        for kind, name in reversed(self.owned):
            try:
                args = ["inspect", name] if kind == "container" else [kind, "inspect", name]
                p = self.docker(args, check=False)
                if p.returncode:
                    continue  # --rm clients are already absent.
                value = json.loads(p.stdout)[0]
                labels = value.get("Config", {}).get("Labels") if kind == "container" else value.get("Labels")
                if not labels or labels.get(LABEL) != self.token:
                    self.cleanup_errors.append("ownership mismatch: " + name)
                    continue
                self.docker(["rm", "--force", name] if kind == "container" else [kind, "rm", name])
            except Exception as exc:
                self.cleanup_errors.append(self.redact(str(exc)))
        self.private.cleanup()


def run_experiment(lab: Lab, transactions: int) -> dict:
    report: dict[str, Any] = {"at": now(), "base_commit": BASE, "status": "running",
        "measurement_scope": "isolated PostgreSQL SQL ledger; not Rust HTTP/RPC or real model traffic",
        "business_acceptance": False, "stages": {}, "missing_acceptance": [
            "live Rust charging-to-ledger integration and pool reconnection",
            "event backlog with actual EventStore",
            "PgBouncer and actual SQLx/Go driver compatibility",
            "dedicated-node versus co-location comparison",
            "replica promotion, old-primary fencing and network partitions",
            "continuous WAL archive restore and independent backup storage failure",
            "3/5 independent 1c1g nodes and monitoring-overhead control run"]}
    try:
        report["image"] = lab.start()
        report["settings"] = lab.settings()
        lab.initialize()
        roles = json.loads(lab.sql("""SELECT jsonb_agg(jsonb_build_object(
          'role',rolname,'superuser',rolsuper,'bypass_rls',rolbypassrls,'connection_limit',rolconnlimit))
          FROM pg_roles WHERE rolname IN ('dp25_core','dp25_ro','dp25_ext','dp25_ops');""", role="postgres"))
        if any(r["superuser"] or r["bypass_rls"] for r in roles):
            raise RuntimeError("an application role has excess privileges")
        report["roles"] = roles
        denied = json.loads(lab.sql("SELECT 1;", role="dp25_ext", check=False))
        if denied["returncode"] == 0:
            raise RuntimeError("extension role unexpectedly connected to core database")
        if lab.sql("SELECT 1;", role="dp25_ext", database="dp25_extension").strip() != "1":
            raise RuntimeError("extension cannot connect to its own database")
        acl = json.loads(lab.sql("""SELECT jsonb_build_object(
            'core_can_update_balance',has_table_privilege('dp25_core','core_billing.balance_state','UPDATE'),
            'ro_can_update_identity',has_table_privilege('dp25_ro','core_identity.accounts','UPDATE'),
            'ext_can_use_billing',has_schema_privilege('dp25_ext','core_billing','USAGE'),
            'ext_can_post_ledger',has_function_privilege('dp25_ext','core_billing.post_ledger(jsonb)','EXECUTE')
        )::text;""", role="postgres"))
        if any(acl.values()):
            raise RuntimeError("core/RPC/extension SQL permission separation failed")
        mutation = json.loads(lab.sql("UPDATE core_billing.balance_state SET balance_units=0 WHERE false;", check=False))
        if mutation["returncode"] == 0 or "42501" not in mutation["stderr"]:
            raise RuntimeError("runtime direct balance writes were not rejected by SQL permissions")
        report["stages"]["database_isolation"] = {"status":"passed", "acl":acl}
        seed = ledger_request("seed", {"kind": "credit", "account_id": 1,
                                       "amount_units": 1_000_000_000, "payment_reference": "dp25-fake-seed"})
        if lab.post(seed).get("status") != "posted":
            raise RuntimeError("fake ledger funding failed")
        report["stages"]["initial_conservation"] = lab.snapshot()
        original_seed = lab.post(seed)
        if original_seed.get("status") != "posted" or lab.snapshot() != report["stages"]["initial_conservation"]:
            raise RuntimeError("exact credit replay changed the real ledger")
        changed_seed = ledger_request("seed", {**seed["action"], "amount_units": 1_000_000_001})
        conflict = json.loads(lab.sql(ledger_sql(changed_seed), check=False))
        if conflict["returncode"] == 0 or "P1001" not in conflict["stderr"]:
            raise RuntimeError("same operation key with changed parameters was not rejected")
        if lab.snapshot() != report["stages"]["initial_conservation"]:
            raise RuntimeError("idempotency conflict changed the ledger")
        report["stages"]["replay_and_conflict"] = {"exact_replay_unchanged": True,
            "changed_parameters_rejected": True, "sqlstate": "P1001"}
        metrics = [lab.metrics()]
        report["metrics"] = metrics
        for clients in (1, 4, 8, 16):
            before_load = lab.snapshot()
            stop = threading.Event()
            def observe():
                while not stop.is_set():
                    try:
                        metrics.append(lab.metrics())
                    except Exception as exc:
                        metrics.append({"at":now(), "probe_error":lab.redact(str(exc))})
                    stop.wait(1)
            monitor = threading.Thread(target=observe, daemon=True)
            monitor.start()
            try:
                output = lab.client("pgbench", ["-n", "-M", "prepared", "-c", str(clients), "-j", str(min(4, clients)),
                                    "-t", str(transactions), "-r", "-f", "/dp25-sql/hot-reserve-capture.pgbench"], timeout=60)
            finally:
                stop.set()
                monitor.join(45)
                if monitor.is_alive():
                    raise RuntimeError("metrics worker did not stop")
            (lab.out / f"pgbench-{clients}.txt").write_text(output.stdout + output.stderr)
            after_load = lab.snapshot()
            report["stages"][f"load_{clients}"] = after_load
            if after_load["reserved_units"] != "0":
                raise RuntimeError("completed workload left an unfinalized reservation")
            for counter in ("operations", "journals"):
                if after_load[counter] - before_load[counter] != 2 * clients * transactions:
                    raise RuntimeError("not every requested reserve/capture operation was posted")
            metrics.append(lab.metrics())
        report["stages"]["unknown_commit"] = lab.unknown_commit()
        before_crash = lab.snapshot()
        crash_start = time.monotonic()
        lab.docker(["kill", "--signal", "KILL", lab.servers["db"]])
        lab.docker(["start", lab.servers["db"]])
        lab.wait_ready("db")
        after_crash = lab.snapshot()
        if after_crash != before_crash:
            raise RuntimeError("acknowledged ledger state changed after crash recovery")
        report["stages"]["crash_recovery"] = {"seconds_to_sql_and_ledger_verified": time.monotonic() - crash_start,
            "confirmed_snapshot_preserved": True, "test_scope": "process crash, disk retained; not power-loss or storage failure"}
        t = time.monotonic()
        lab.sql("CHECKPOINT;", role="postgres")
        report["stages"]["checkpoint"] = {"seconds": time.monotonic() - t, "concurrent_load": False}
        metrics.append(lab.metrics())
        # A logical backup is independently restored. No live data directory is copied.
        original = lab.snapshot()
        t = time.monotonic()
        backup = lab.client("pg_dump", ["--format=custom"], role="postgres", binary=True, timeout=60).stdout
        if len(backup) > 64 * 1024**2:
            raise RuntimeError("backup exceeded the bounded experiment size")
        archive = lab.out / "ledger.dump"
        archive.write_bytes(backup)
        archive.chmod(0o600)
        report["stages"]["backup"] = {"seconds": time.monotonic()-t, "bytes": len(backup),
            "sha256": hashlib.sha256(backup).hexdigest(), "kind": "logical", "concurrent_load": False,
            "independent_failure_domain": False}
        t = time.monotonic()
        lab.start_server("restore")
        lab.roles(host="restore")
        lab.client("pg_restore", ["--exit-on-error"], role="postgres", host="restore", data=backup, binary=True, timeout=60)
        if lab.snapshot("restore") != original:
            raise RuntimeError("restored ledger differs from the backed-up snapshot")
        seed_before = lab.snapshot("restore")
        if lab.post(seed, "restore").get("status") != "posted" or lab.snapshot("restore") != seed_before:
            raise RuntimeError("restored idempotency state did not stop a duplicate credit")
        report["stages"]["restore"] = {"seconds_to_restore_and_ledger_verified": time.monotonic()-t,
                                      "snapshot_equal": True, "duplicate_credit_blocked": True}
        report["peak_client_backends_sampled"] = max(m["postgres"]["client_backends"] for m in metrics if "postgres" in m)
        report["connection_peak_note"] = "sampled lower bound; short spikes may be missed; includes the monitor connection"
        report["probe_control_comparison"] = "not run; probe duration is recorded, not its causal effect on throughput"
        # Even every implemented stage passing is NOT the requested whole-stack acceptance.
        report["status"] = "partial_sql_database_tests_passed"
    except Exception as exc:
        report["status"] = "failed_or_blocked"
        report["error"] = lab.redact(str(exc))
    finally:
        if "db" in lab.servers:
            try:
                desc = json.loads(lab.docker(["inspect", lab.servers["db"]]).stdout)[0]
                report["database_final_state"] = {k:desc["State"].get(k) for k in ("Running", "OOMKilled", "ExitCode")}
                logs = lab.docker(["logs", "--tail", "1000", lab.servers["db"]], check=False)
                (lab.out / "postgres.log").write_text(lab.redact(logs.stdout + logs.stderr))
            except Exception as exc:
                report["final_probe_error"] = lab.redact(str(exc))
        lab.close()
        report["cleanup_errors"] = lab.cleanup_errors
        if lab.cleanup_errors:
            report["status"] = "failed_cleanup"
        (lab.out / "result.json").write_text(json.dumps(report, indent=2))
    return report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True, help="local checkout of the pinned source")
    parser.add_argument("--image", help="already installed PostgreSQL 17 sha256 ID or repository@sha256 digest")
    parser.add_argument("--proxy", type=Path, help="locally compiled static Linux commit-drop binary")
    parser.add_argument("--out", type=Path, required=True, help="new private evidence directory; never overwritten")
    parser.add_argument("--run", action="store_true", help="create an isolated LOCAL database after preflight")
    parser.add_argument("--transactions", type=int, default=50, help="1..100 iterations per pgbench client")
    args = parser.parse_args()
    if not 1 <= args.transactions <= 100:
        parser.error("transactions must be in 1..100")
    args.source = args.source.resolve()
    args.out.mkdir(parents=True, mode=0o700, exist_ok=False)
    args.out.chmod(0o700)
    check = preflight(args.source, args.image, args.proxy)
    (args.out / "preflight.json").write_text(json.dumps(check, indent=2))
    if check["blockers"] or not args.run:
        print(json.dumps(check, indent=2))
        return 2 if check["blockers"] else 0
    lab = Lab(args.source, args.image, args.proxy, args.out)
    report = run_experiment(lab, args.transactions)
    print(json.dumps({"status": report["status"], "business_acceptance": False, "report": str(args.out / 'result.json')}, indent=2))
    return 0 if report["status"] == "partial_sql_database_tests_passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
