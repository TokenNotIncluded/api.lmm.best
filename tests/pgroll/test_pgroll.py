#!/usr/bin/env python3
"""Exercise pgroll on an owned, disposable local PostgreSQL container only."""
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
FIXTURES = Path(__file__).resolve().parent
VERSION = "0.16.3"
# Reviewed PostgreSQL 18.6 image. Never pull implicitly or use an existing DB.
IMAGE = "postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873"
DOCKER = ["docker", "--host", "unix:///var/run/docker.sock"]
OWNER_LABEL = "best.lmm.pgroll-test"
V1, V2 = "public_lmm_pgroll_demo_v1", "public_lmm_pgroll_demo_v2"


def main():
    if not shutil.which("pgroll") or not shutil.which("docker"):
        raise SystemExit("Run through mise; pgroll and local Docker Engine are required.")
    # Ignore ambient database/pgroll configuration. No caller DSN is accepted.
    env = {key: value for key, value in os.environ.items()
           if not key.startswith("PG") and key not in {
               "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"}}
    password = secrets.token_hex(32)
    owner = secrets.token_hex(12)
    name = "lmm-pgroll-test-" + owner
    container = None
    url = ""

    def run(args, *, data=None, command_env=None, timeout=60, allow_failure=False):
        try:
            result = subprocess.run(args, cwd=ROOT, env=command_env or env,
                                    input=data, text=True, capture_output=True, timeout=timeout)
        except subprocess.TimeoutExpired:
            raise RuntimeError(f"{Path(args[0]).name} timed out") from None
        if result.returncode and not allow_failure:
            detail = (result.stderr + result.stdout).replace(password, "[redacted]")
            if url:
                detail = detail.replace(url, "[test database]")
            raise RuntimeError(f"{Path(args[0]).name} failed: {detail.strip()}")
        return result.stdout.strip(), result.returncode

    def docker(*args, **kwargs):
        return run([*DOCKER, *args], **kwargs)

    def sql(statement):
        return docker("exec", "-i", container, "psql", "-XAt", "--set", "ON_ERROR_STOP=1",
                      "--username", "pgroll_demo", "--dbname", "pgroll_demo", data=statement)[0]

    def expect(statement, expected):
        actual = sql(statement)
        if actual != expected:
            raise AssertionError(f"Unexpected synthetic SQL result: {actual!r}; wanted {expected!r}")

    def pgroll(*args):
        return run(["pgroll", "--schema", "public", "--pgroll-schema", "pgroll",
                    "--use-version-schema=true", *args],
                   command_env=dict(env, PGROLL_PG_URL=url))[0]

    version = run(["pgroll", "--version"])[0]
    if version != f"pgroll version {VERSION}":
        raise SystemExit(f"Expected pgroll {VERSION}; run mise install aqua:xataio/pgroll@{VERSION}.")
    docker("image", "inspect", IMAGE)
    # A private env file avoids putting the password in Docker command arguments.
    with tempfile.TemporaryDirectory(prefix="lmm-pgroll-test-") as directory:
        config = Path(directory) / "postgres.env"
        with open(config, "x", opener=lambda path, flags: os.open(path, flags, 0o600)) as file:
            file.write(f"POSTGRES_USER=pgroll_demo\nPOSTGRES_DB=pgroll_demo\nPOSTGRES_PASSWORD={password}\n")
        try:
            container = docker("create", "--pull=never", "--name", name,
                               "--label", OWNER_LABEL + "=" + owner, "--memory", "512m",
                               "--cpus", "1", "--pids-limit", "128", "--env-file", str(config),
                               "--publish", "127.0.0.1::5432",
                               "--tmpfs", "/var/lib/postgresql:rw,size=268435456", IMAGE)[0]
            docker("start", container)
            for _ in range(100):
                # The image first runs a temporary socket-only bootstrap server.
                _, code = docker("exec", container, "pg_isready", "-h", "127.0.0.1", "-U", "pgroll_demo",
                                 "-d", "pgroll_demo", allow_failure=True)
                if code == 0:
                    break
                time.sleep(0.2)
            else:
                raise RuntimeError("Disposable PostgreSQL did not become ready")
            binding = json.loads(docker("inspect", container)[0])[0]["NetworkSettings"]["Ports"]["5432/tcp"]
            if len(binding) != 1 or binding[0]["HostIp"] != "127.0.0.1":
                raise RuntimeError("Test database must bind only to localhost")
            url = (f"postgres://pgroll_demo:{password}@127.0.0.1:{binding[0]['HostPort']}"
                   "/pgroll_demo?sslmode=disable&connect_timeout=5")
            expect("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace "
                   "WHERE n.nspname NOT IN ('pg_catalog','information_schema') "
                   "AND n.nspname NOT LIKE 'pg_toast%';", "0")
            pgroll("init")
            create = str(FIXTURES / "001_create_items.json")
            rename = str(FIXTURES / "002_rename_label.json")
            pgroll("validate", create)
            pgroll("start", create)
            expect(f"SELECT relkind FROM pg_class WHERE oid='{V1}.items'::regclass;", "v")
            pgroll("complete")
            sql(f"SET search_path TO {V1}; INSERT INTO items VALUES (1, 'original');")
            pgroll("validate", rename)
            pgroll("start", rename)
            expect(f"SET search_path TO {V1}; SELECT label FROM items WHERE id=1;", "SET\noriginal")
            expect(f"SET search_path TO {V2}; SELECT display_name FROM items WHERE id=1;", "SET\noriginal")
            sql(f"SET search_path TO {V1}; UPDATE items SET label='old_client' WHERE id=1;")
            expect(f"SELECT display_name FROM {V2}.items WHERE id=1;", "old_client")
            sql(f"SET search_path TO {V2}; INSERT INTO items VALUES (2, 'new_client');")
            expect(f"SELECT label FROM {V1}.items WHERE id=2;", "new_client")
            print("PASS: init, validation, start, coexisting views and sequential client writes", flush=True)
            pgroll("rollback")
            expect(f"SELECT to_regnamespace('{V2}') IS NULL;", "t")
            expect(f"SELECT id || ':' || label FROM {V1}.items ORDER BY id;", "1:old_client\n2:new_client")
            print("PASS: rollback removes the new view and preserves both clients' data", flush=True)
            pgroll("start", rename)
            sql(f"SET search_path TO {V2}; UPDATE items SET display_name='completed' WHERE id=1;")
            pgroll("complete")
            expect(f"SELECT to_regnamespace('{V1}') IS NULL;", "t")
            expect(f"SELECT id || ':' || display_name FROM {V2}.items ORDER BY id;", "1:completed\n2:new_client")
            expect("SELECT column_name FROM information_schema.columns "
                   "WHERE table_schema='public' AND table_name='items' ORDER BY ordinal_position;", "id\ndisplay_name")
            print("PASS: complete retires the old view, renames the physical column and preserves data", flush=True)
        finally:
            # Recover by the preselected name if create succeeded but its response
            # was lost. A daemon error is never treated as proof of absence.
            found = docker("container", "ls", "--all", "--quiet", "--no-trunc",
                           "--filter", "name=^/" + name + "$")[0].splitlines()
            if len(found) > 1 or (container and found and found != [container]):
                raise RuntimeError("Unexpected test container identity; refusing cleanup")
            if found:
                target = found[0]
                info = json.loads(docker("inspect", target)[0])[0]
                labels = info["Config"].get("Labels") or {}
                if labels.get(OWNER_LABEL) != owner:
                    raise RuntimeError("Refusing to remove a container without this test's ownership label")
                docker("rm", "--force", target)
                remaining = docker("container", "ls", "--all", "--quiet", "--no-trunc",
                                   "--filter", "id=" + target)[0]
                if remaining:
                    raise RuntimeError("Disposable test container was not removed")
            print("PASS: owned container absent; no persistent database volume", flush=True)


if __name__ == "__main__":
    main()
