#!/usr/bin/env python3
"""Start both ordinary listeners on the same disposable PostgreSQL data and benchmark.

Only prebuilt binaries are accepted; compilation must finish before measurement.
"""

import argparse
import importlib.util
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.request

import relay_workload

SERVICES_PATH = Path(__file__).parents[1] / "scripts" / "with-local-services.py"
spec = importlib.util.spec_from_file_location("local_services", SERVICES_PATH)
services_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(services_module)


def wait_ready(process, url, log):
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *_args, **_kwargs):
            return None

    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f"listener exited {process.returncode}; see {log}")
        try:
            with opener.open(url + "/api/livez", timeout=2) as response:
                if response.status == 200 and json.load(response).get("success") is True:
                    return
        except (OSError, urllib.error.URLError, ValueError):
            pass
        time.sleep(.1)
    raise RuntimeError(f"listener startup timed out; see {log}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go-binary", type=Path, required=True)
    parser.add_argument("--rust-binary", type=Path, required=True)
    parser.add_argument("--driver", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--requests", type=int, default=10000)
    parser.add_argument("--rounds", type=int, default=3)
    parser.add_argument("--concurrency", type=int, nargs="+", default=[1, 8, 32])
    parser.add_argument("--relay-provider", type=Path,
                        help="optional prebuilt relay_provider.go; enables billed relay comparison")
    parser.add_argument("--relay-requests", type=int, default=500)
    args = parser.parse_args()
    for binary in (args.go_binary, args.rust_binary, args.driver):
        if not binary.is_file() or not os.access(binary, os.X_OK):
            parser.error(f"prebuilt executable missing: {binary}")
    if args.relay_provider is not None and (not args.relay_provider.is_file() or not os.access(args.relay_provider, os.X_OK)):
        parser.error("relay provider must be a prebuilt executable")
    if min(args.requests, args.rounds, args.relay_requests, *args.concurrency) < 1:
        parser.error("request counts, rounds, and concurrency must be positive")
    args.output_dir.mkdir(parents=True, exist_ok=True)
    # A failed rerun must never leave an older successful report presented as
    # the result of this invocation.
    (args.output_dir / "comparison.json").write_text(json.dumps({
        "valid": False, "phase": "starting", "started_unix": time.time(),
    }, indent=2) + "\n")
    if args.relay_provider is not None:
        (args.output_dir / "relay-comparison.json").write_text(json.dumps({
            "valid": False, "phase": "waiting_for_listeners", "started_unix": time.time(),
        }, indent=2) + "\n")
    with services_module.local_services(args.output_dir) as services:
        # The two engines get the same database and separate empty cache DBs.
        # An empty working directory prevents Go's dotenv loader reading the
        # checkout's environment or a user's configured production services.
        env = {key: value for key, value in os.environ.items()
               if key in ("PATH", "HOME", "LANG", "LC_ALL", "LD_LIBRARY_PATH", "SSL_CERT_FILE", "SSL_CERT_DIR")}
        env.update({
            "DATABASE_URL": services.database_url, "SQL_DSN": services.database_url,
            "VALKEY_URL": services.valkey_url, "REDIS_CONN_STRING": services.valkey_url,
            "SESSION_SECRET": "Parity-A1!" + secrets.token_hex(32), "CRYPTO_SECRET": "Parity-B2!" + secrets.token_hex(32),
            "GLOBAL_API_RATE_LIMIT_ENABLE": "false", "CRITICAL_RATE_LIMIT_ENABLE": "false",
            "SEARCH_RATE_LIMIT_ENABLE": "false", "TRUSTED_PROXIES": "none",
            "LMM_LOCAL_ACCEPTANCE": "true", "AUTH_COOKIE_SECURE": "false",
            "GIN_MODE": "release", "VERSION": "v0.0.0-local-parity",
            "GOMAXPROCS": "4", "TOKIO_WORKER_THREADS": "4",
            # Match SQLx's actual pinned defaults instead of comparing Go's
            # default 1000-open-connection pool with Rust's 10-connection pool.
            "SQL_MAX_OPEN_CONNS": "10", "SQL_MAX_IDLE_CONNS": "10",
            "SQL_MAX_LIFETIME": "1800",
            "PASSWORD_LOGIN_ENABLED": "true", "LMM_DB_MIGRATION_MODE": "apply",
        })
        go_port = services_module.free_port()
        rust_port = services_module.free_port()
        while rust_port == go_port:
            rust_port = services_module.free_port()
        processes = []
        logs = []

        def launch(name, binary, environment, arguments=()):
            log_path = services.runtime / f"{name}-listener.log"
            log = log_path.open("wb")
            logs.append(log)
            process = subprocess.Popen([str(binary.resolve()), *arguments], cwd=services.runtime,
                                       env=environment, stdout=log, stderr=subprocess.STDOUT)
            processes.append(process)
            return process, log_path

        try:
            start = time.monotonic()
            go, go_log = launch("go", args.go_binary, dict(env, PORT=str(go_port),
                                LMM_API_PORT=str(go_port), LMM_API_BIND_ADDRESS="127.0.0.1"),
                                arguments=("serve", "--log-dir=",))
            go_url = f"http://127.0.0.1:{go_port}"
            wait_ready(go, go_url, go_log)
            go_initialization_seconds = time.monotonic() - start
            # Schema creation is setup work, not part of a serving-process
            # startup comparison. Restart Go in its normal verify mode.
            services_module.stop(go)
            services.sql(f'ALTER DATABASE "{services.database}" SET search_path=public;', "postgres")
            services.sql("""
                INSERT INTO users(id,username,password,display_name,role,status,auth_version,quota,"group",setting)
                    VALUES(1,'parity-root','unused-local-fixture','Parity root',100,1,1,1000000,'default','{}');
                INSERT INTO setups(id,version,initialized_at)
                    VALUES(1,'v0.0.0-local-parity',EXTRACT(EPOCH FROM NOW())::BIGINT);
                CREATE TABLE IF NOT EXISTS lmm_schema_contract (
                    singleton BOOLEAN PRIMARY KEY, min_reader_version BIGINT NOT NULL,
                    max_reader_version BIGINT NOT NULL);
                INSERT INTO lmm_schema_contract VALUES (TRUE, 1, 1)
                    ON CONFLICT(singleton) DO UPDATE SET min_reader_version=1, max_reader_version=1;
            """)
            # Current Go creates the common schema. Rust also needs durable
            # settlement state and subscription amount snapshots.
            for migration_name in ("0014_relay_settlement.sql", "0018_subscription_amount_snapshots.sql"):
                migration = Path(__file__).parents[2] / "migrations" / migration_name
                services.sql(migration.read_text().replace("__LMM_APP_SCHEMA__", "public"))
            relay_keys = None
            provider_url = None
            if args.relay_provider is not None:
                provider_port = services_module.free_port()
                while provider_port in (go_port, rust_port):
                    provider_port = services_module.free_port()
                provider_url = f"http://127.0.0.1:{provider_port}"
                provider = services.launch([str(args.relay_provider.resolve()), "-listen",
                                            f"127.0.0.1:{provider_port}"], "relay-provider")
                deadline = time.monotonic() + 10
                while True:
                    if provider.poll() is not None:
                        raise RuntimeError("local relay provider exited before readiness")
                    try:
                        relay_workload.request(provider_url, "/stats")
                        break
                    except OSError:
                        if time.monotonic() >= deadline:
                            raise RuntimeError("local relay provider startup timed out") from None
                        time.sleep(.025)
                relay_keys = relay_workload.prepare(services, provider_url)
            start = time.monotonic()
            go, go_log = launch("go-serving", args.go_binary, dict(env, PORT=str(go_port),
                                LMM_API_PORT=str(go_port), LMM_API_BIND_ADDRESS="127.0.0.1",
                                LMM_DB_MIGRATION_MODE="verify"), arguments=("serve", "--log-dir=",))
            wait_ready(go, go_url, go_log)
            go_start_seconds = time.monotonic() - start
            rust_url = f"http://127.0.0.1:{rust_port}"
            start = time.monotonic()
            rust, rust_log = launch("rust", args.rust_binary, dict(env,
                LMM_RS_LISTEN_ADDR=f"127.0.0.1:{rust_port}", LMM_RS_SLOT="blue",
                LMM_SCHEMA_CONTRACT="1", VALKEY_URL=services.valkey_url[:-1] + "1"))
            wait_ready(rust, rust_url, rust_log)
            rust_start_seconds = time.monotonic() - start
            metadata = {
                "go_initialization_with_schema_creation_seconds": go_initialization_seconds,
                "go_start_existing_schema_seconds": go_start_seconds,
                "rust_start_existing_schema_seconds": rust_start_seconds,
                "startup_comparable": True,
                "startup_measurement": "one warm-host start per ordinary listener against the same initialized schema; excludes Go's initial schema creation",
                "tokenizer_note": "Go eagerly initializes its default tokenizer; Rust loads tokenizers on demand. Public-read RSS does not describe warmed relay memory.",
                "go_maxprocs": 4, "tokio_worker_threads": 4,
                "database_max_open_connections_each": 10,
                "database_max_lifetime_seconds_each": 1800,
                "normal_listeners": True, "shared_database": True, "separate_cache_databases": True,
                "schema": "current Go schema plus real Rust migrations 0014 relay settlement and 0018 subscription amount snapshots",
                "access_logging_required": True,
                "logging_sinks": "one redirected stdout/stderr file per process; Go --log-dir= disables its additional duplicate log file",
                "logging_formats": "Go text and Rust JSON retain each implementation's normal completed-request fields",
            }
            (services.runtime / "listener-metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
            output = args.output_dir.resolve() / "comparison.json"
            print(f"both ordinary listeners ready; logs: {services.runtime}", flush=True)
            # Give startup background tasks time to settle before idle sampling.
            time.sleep(2)
            result = subprocess.call([
                sys.executable, str(Path(__file__).with_name("compare_backends.py")),
                "--go-url", go_url, "--go-pid", str(go.pid), "--rust-url", rust_url,
                "--rust-pid", str(rust.pid), "--driver", str(args.driver.resolve()),
                "--output", str(output), "--requests", str(args.requests),
                "--rounds", str(args.rounds), "--concurrency", *map(str, args.concurrency),
            ])
            if result == 0 and relay_keys is not None:
                relay_workload.run(args, services, provider_url, relay_keys,
                                   {"go": (go_url, go.pid), "rust": (rust_url, rust.pid)})
            public_report = json.loads(output.read_text())
            runs = public_report.get("runs", [])
            paths = {run["path"] for run in runs}
            warmed = {(run["path"], run["concurrency"]) for run in runs}
            minimum_events = (sum(run["requests"] for run in runs if run["backend"] == "go")
                              + len(warmed) * min(1000, args.requests) + len(paths))
            relay_report = (json.loads((args.output_dir / "relay-comparison.json").read_text())
                            if relay_keys is not None else None)
            metadata["access_logs"] = {}
            for name, log_path, marker in (("go", go_log, b"[GIN]"),
                                            ("rust", rust_log, b"http request completed")):
                contents = log_path.read_bytes()
                count = contents.count(marker)
                minimum_for_backend = minimum_events
                if relay_report is not None:
                    minimum_for_backend += (sum(run["requests"] for run in relay_report.get("runs", [])
                                                if run["backend"] == name)
                                            + sum(cold["backend"] == name for cold in relay_report.get("cold_requests", [])))
                metadata["access_logs"][name] = {"bytes": len(contents), "completed_events": count,
                                                "minimum_verified_requests": minimum_for_backend}
                if count < minimum_for_backend:
                    result = 1
                    metadata["access_logging_required_satisfied"] = False
            metadata.setdefault("access_logging_required_satisfied", True)
            (services.runtime / "listener-metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
            if not metadata["access_logging_required_satisfied"]:
                public_report["valid"] = False
                public_report["access_logging_error"] = "not enough completed request logs from both backends"
                if relay_report is not None:
                    relay_report["valid"] = False
                    relay_report["access_logging_error"] = public_report["access_logging_error"]
            public_report["listener_metadata"] = metadata
            output.write_text(json.dumps(public_report, indent=2) + "\n")
            if relay_report is not None:
                relay_report["listener_metadata"] = metadata
                (args.output_dir / "relay-comparison.json").write_text(json.dumps(relay_report, indent=2) + "\n")
            return result
        finally:
            for process in reversed(processes):
                services_module.stop(process)
            for log in logs:
                log.close()


if __name__ == "__main__":
    sys.exit(main())
