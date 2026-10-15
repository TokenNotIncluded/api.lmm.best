"""Synthetic PostgreSQL acceptance of the complete offline opening CLI.

Run under agcc with explicit matching frozen source and sealed Core binary:
  python3 -B scripts/test_legacy_financial_opening_cli.py --source-root PATH \
    --core PATH --expected-core-sha256 SHA256 --output NEW_PRIVATE_BACKUP_DIR

Creates a new local PostgreSQL 18 cluster without TCP, dumps and restores only
newly generated synthetic users 1..4, then exercises preview/review/apply/replay
and original-attempt reconciliation after an external psql result is discarded.
No production URLs, databases, credentials or existing target are accepted.
The identity rows/receipt are synthetic fixture inputs, not identity acceptance.
All resources and failure evidence are retained; only this cluster is stopped.
Importing this module has no side effects.
"""


def main():
    import os, sys, json, subprocess, hashlib, importlib.util, traceback
    from pathlib import Path
    import argparse

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--source-root",
        type=Path,
        required=True,
        help="Reviewed source snapshot containing scripts and Core schema/source",
    )
    parser.add_argument(
        "--core",
        type=Path,
        required=True,
        help="Sealed Core executable built from the matching schema",
    )
    parser.add_argument("--expected-core-sha256", required=True)
    parser.add_argument(
        "--output",
        type=Path,
        required=True,
        help="New private directory under ~/Backups; all parent directories must be mode 0700",
    )
    args = parser.parse_args()
    ROOT = args.source_root.resolve(strict=True)
    CORE = args.core.resolve(strict=True)
    assert hashlib.sha256(CORE.read_bytes()).hexdigest() == args.expected_core_sha256, (
        "selected Core SHA mismatch"
    )
    R = args.output.absolute()
    sys.dont_write_bytecode = True
    sys.path.insert(0, str(ROOT / "scripts"))
    import test_migrate_legacy_financial as facts

    F = facts.MODULE
    I = F.IDENTITY
    I.private_backup_path(R.parent, directory=True)
    R.mkdir(mode=0o700)
    I.private_backup_path(R, directory=True)
    s = importlib.util.spec_from_file_location(
        "O", ROOT / "scripts/legacy_financial_opening.py"
    )
    O = importlib.util.module_from_spec(s)
    s.loader.exec_module(O)
    ENV = {
        k: v
        for k, v in os.environ.items()
        if not k.startswith(("PG", "LMM_CORE_"))
        and k not in ("DATABASE_URL", "SQL_DSN")
    }
    ENV["PYTHONDONTWRITEBYTECODE"] = "1"
    SOCK = R / "sock"
    DATA = R / "data"
    PORT = 55471
    COUNT = 0
    RESULTS = []

    def write(name, value):
        p = R / name
        p.write_text(value if isinstance(value, str) else F.canonical(value) + "\n")
        p.chmod(0o600)
        return p

    def cmd(argv, env=None):
        p = subprocess.run(
            argv, env=env or ENV, capture_output=True, text=True, timeout=120
        )
        write(
            "command-" + str(len(list(R.glob("command-*.json")))) + ".json",
            {
                "argv": argv,
                "returncode": p.returncode,
                "stdout": p.stdout,
                "stderr": p.stderr,
            },
        )
        if p.returncode:
            raise RuntimeError("command failed: " + str(argv))
        return p.stdout

    def sql(text, db="postgres", owner=False, fail=False, label=None):
        nonlocal COUNT
        COUNT += 1
        label = label or str(COUNT)
        write(label + ".sql", text)
        e = ENV.copy()
        if owner:
            e["PGOPTIONS"] = "-c role=opening_o"
        p = subprocess.run(
            [
                "psql",
                "-XqAt",
                "-v",
                "ON_ERROR_STOP=1",
                "-h",
                str(SOCK),
                "-p",
                str(PORT),
                "-U",
                "opening_i" if owner else "opening_admin",
                "-d",
                db,
            ],
            input=text,
            env=e,
            capture_output=True,
            text=True,
            timeout=140,
        )
        write(label + ".stdout", p.stdout)
        write(label + ".stderr", p.stderr)
        if (p.returncode != 0) != fail:
            raise AssertionError(
                label
                + ": unexpected SQL exit "
                + str(p.returncode)
                + " "
                + p.stderr[-700:]
            )
        return p.stdout.strip()

    def jsql(text, db, owner=True):
        return F.parse_json(sql(text, db, owner))

    def snap(db):
        return jsql(O.snapshot_query(), db)

    # Only synthetic users 1..4; all source, target, archive and review files belong to this fixture.

    def source_fixture():
        from datetime import datetime, timezone

        sql("CREATE ROLE synthetic_reader LOGIN; CREATE DATABASE synthetic_input;")
        table_names = [
            "users",
            "tokens",
            "top_ups",
            "user_subscriptions",
            "subscription_plans",
            "options",
        ]
        ddl = "CREATE SCHEMA legacy; CREATE TABLE legacy.users (id BIGINT PRIMARY KEY,quota BIGINT,aff_quota BIGINT,used_quota BIGINT,aff_history BIGINT,request_count BIGINT,status BIGINT,deleted_at TIMESTAMPTZ);"
        ddl += "".join(
            "CREATE TABLE legacy." + t + " (id BIGINT PRIMARY KEY);"
            for t in table_names
            if t != "users"
        )
        ddl += "INSERT INTO legacy.users VALUES(1,19,3,9001,103,11,1,NULL),(2,-10,-2,9001,103,11,1,NULL),(3,7,-7,9001,103,11,1,NULL),(4,0,0,9001,103,11,1,NULL);"
        sql(ddl, "synthetic_input")
        archive = R / "synthetic-source.dump"
        with archive.open("xb") as f:
            rr = subprocess.run(
                [
                    "pg_dump",
                    "-h",
                    str(SOCK),
                    "-p",
                    str(PORT),
                    "-U",
                    "opening_admin",
                    "-d",
                    "synthetic_input",
                    "--format=custom",
                ],
                stdout=f,
                stderr=subprocess.PIPE,
                env=ENV,
                timeout=60,
            )
        assert rr.returncode == 0
        archive.chmod(0o600)
        started = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        sql("CREATE DATABASE synthetic_restored;")
        cmd(
            [
                "pg_restore",
                "-h",
                str(SOCK),
                "-p",
                str(PORT),
                "-U",
                "opening_admin",
                "-d",
                "synthetic_restored",
                "--exit-on-error",
                str(archive),
            ]
        )
        sql(
            "REVOKE ALL ON DATABASE synthetic_restored FROM PUBLIC; GRANT CONNECT ON DATABASE synthetic_restored TO synthetic_reader; GRANT USAGE ON SCHEMA legacy TO synthetic_reader; GRANT SELECT ON ALL TABLES IN SCHEMA legacy TO synthetic_reader;",
            "synthetic_restored",
        )
        raw = sql(
            "SET ROLE synthetic_reader; BEGIN READ ONLY;" + I.IDENTITY_SQL + "COMMIT;",
            "synthetic_restored",
        )
        ident = F.parse_json(raw)
        ident["database_oid"] = int(ident["database_oid"])
        # Source metadata and frozen rows are obtained only from this actual restored synthetic DB.
        columns = jsql(
            "SELECT jsonb_agg(jsonb_build_object('schema',table_schema,'table_name',table_name,'column_name',column_name,'data_type',data_type,'ordinal_position',ordinal_position) ORDER BY table_name,ordinal_position) FROM information_schema.columns WHERE table_schema='legacy';",
            "synthetic_restored",
            False,
        )
        constraints = jsql(
            "SELECT jsonb_agg(jsonb_build_object('schema',n.nspname,'table_name',c.relname,'contype',x.contype,'definition',pg_get_constraintdef(x.oid)) ORDER BY c.relname) FROM pg_constraint x JOIN pg_class c ON c.oid=x.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='legacy';",
            "synthetic_restored",
            False,
        )
        tables = {
            t: jsql(
                "SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY id),'[]'::jsonb) FROM legacy."
                + t
                + " r;",
                "synthetic_restored",
                False,
            )
            for t in table_names
        }
        catalog = write(
            "native-catalog-metadata.jsonl",
            "\n".join(
                F.canonical(x) for x in [{}, [], columns, constraints, [], [], {}]
            )
            + "\n",
        )
        baseline = write(
            "native-table-row-count-baseline.jsonl",
            "\n".join(
                F.canonical({"table": t, "row_count": len(rows)})
                for t, rows in sorted(tables.items())
            )
            + "\n",
        )
        source_hash = I.sha256_file(archive)
        receipt = {
            "status": "snapshot-restored-baseline-verified-frozen-and-classified",
            "archive": {"path": str(archive), "sha256": source_hash},
            "started_utc": started,
            "source": {"schema": "legacy"},
            "private_metadata_files": [str(catalog), str(baseline)],
            "readonly_reader": {
                "role": "synthetic_reader",
                "database": "synthetic_restored",
                "port": PORT,
            },
            "source_instance_identity": {
                **ident,
                "listen_addresses": "",
                "schema_owner": "opening_admin",
            },
        }
        write("source-receipt.json", receipt)
        export = [
            {"kind": "identity", "row": ident},
            {
                "kind": "schema",
                "row": {"schema": "legacy", "schema_owner": "opening_admin"},
            },
        ] + [
            {"kind": "row", "table": t, "row": row}
            for t, rows in sorted(tables.items())
            for row in rows
        ]
        raw_path = write(
            "source-financial-export.private.jsonl",
            "\n".join(F.canonical(x) for x in export) + "\n",
        )
        proof = {
            "source_archive_sha256": source_hash,
            "source_export_sha256": I.sha256_file(raw_path),
            "metadata_hashes": {p.name: I.sha256_file(p) for p in (catalog, baseline)},
            "selected_table_counts": {
                t: len(rows) for t, rows in sorted(tables.items())
            },
            "source_column_coverage_exact": True,
            "source_row_baseline_exact": True,
            "source_instance": ident,
        }
        write("export-proof.json", proof)
        mapfile = write(
            "identity-map.jsonl",
            "\n".join(
                F.canonical(
                    {
                        "source_user_id": n,
                        "target_user_id": n,
                        "target_personal_account_id": n,
                    }
                )
                for n in range(1, 5)
            )
            + "\n",
        )
        identity_manifest = {
            "format_version": 2,
            "scope": "identity-only",
            "source_archive_sha256": source_hash,
            "source_target_id_map_sha256": I.sha256_file(mapfile),
            "mapping_sha256": "b" * 64,
            "identity_contract": I.current_contract(),
        }
        write("identity-manifest.json", identity_manifest)
        provenance = F.bind_identity_provenance(
            identity_manifest,
            map_file_sha256=I.sha256_file(mapfile),
            source_archive_sha256=source_hash,
        )
        colsets, counts, meta, pk, colschema = F.metadata(receipt)
        plan = F.plan_financial(
            tables,
            {n: n for n in range(1, 5)},
            source_sha256=source_hash,
            identity_provenance=provenance,
            primary_keys=pk,
            column_schema=colschema,
        )
        evidence = {
            "format_version": 1,
            "scope": "opening-reservation-evidence",
            "source_archive_sha256": source_hash,
            "mapping_sha256": plan["mapping_sha256"],
            "source_export_sha256": proof["source_export_sha256"],
            "exported_table_counts": proof["selected_table_counts"],
            "positions": [
                {"source_user_id": n, "reserved_units": 0} for n in range(1, 5)
            ],
            "table_assessments": [
                {
                    "table": t,
                    "source_rows_sha256": F.digest(sorted(rows, key=F.canonical)),
                    "opening_holds": "proven-none",
                    "review_basis": "Independent synthetic fixture review: newly generated wallet rows and empty economic hold tables contain no reserved or in-flight rights.",
                }
                for t, rows in sorted(tables.items())
            ],
        }
        epath = write("reservation-evidence.json", evidence)
        review = {
            "format_version": 1,
            "scope": "opening-economic-policy-review",
            "status": "independently-reviewed-zero-reserved",
            "source_archive_sha256": source_hash,
            "mapping_sha256": plan["mapping_sha256"],
            "reservation_evidence_file_sha256": I.sha256_file(epath),
            "excluded_families": O.EXCLUDED,
            "reviewer": "independent-opening-acceptance",
            "review_basis": "All four newly generated synthetic user rows and complete six-table restored source reviewed; no real users or historical rights. Nonzero reservations explicitly unsupported.",
        }
        write("reservation-review.json", review)
        return receipt, tables, proof, identity_manifest, plan

    def target_fixture(db, receipt, manifest):
        sql("CREATE DATABASE " + db + " OWNER opening_o TEMPLATE template0;")
        sql(
            "REVOKE ALL ON DATABASE "
            + db
            + " FROM PUBLIC; GRANT CONNECT ON DATABASE "
            + db
            + " TO opening_i,opening_r;"
        )
        sql(
            "ALTER DATABASE "
            + db
            + " SET lmm.migration_instance_nonce='synthetic-cli-opening-private';"
        )
        sql(
            "ALTER SCHEMA public OWNER TO opening_o; REVOKE ALL ON SCHEMA public FROM PUBLIC;",
            db,
        )
        e = ENV.copy()
        e["LMM_CORE_DATABASE_URL"] = (
            "postgresql://opening_i@localhost/"
            + db
            + "?host="
            + str(SOCK)
            + "&port="
            + str(PORT)
            + "&options=-c%20role%3Dopening_o"
        )
        e["LMM_CORE_DATABASE_MAX_CONNECTIONS"] = "1"
        cmd([str(CORE), "--legacy-env", "init-db"], e)
        source_hash = receipt["archive"]["sha256"]
        sql(
            "INSERT INTO core_identity.accounts(id,kind) OVERRIDING SYSTEM VALUE SELECT n,'personal' FROM generate_series(1,4)n; INSERT INTO core_identity.users(id,personal_account_id) SELECT n,n FROM generate_series(1,4)n; INSERT INTO core_migration.import_runs(migration_id,source_archive_sha256,mapping_sha256,manifest) VALUES ('synthetic-identity',"
            + I.sql_literal(source_hash)
            + ",'"
            + "b" * 64
            + "',"
            + I.sql_literal(F.canonical(manifest))
            + "::jsonb);",
            db,
            True,
        )
        c = I.current_contract()
        c["catalog_fingerprint_sha256"] = sql(I.catalog_hash_sql(c), db, True)
        ident = jsql(I.IDENTITY_SQL, db)
        ident["database_oid"] = int(ident["database_oid"])
        ident["listen_addresses"] = ""
        ident["cluster_system_identifier"] = sql(
            "SELECT system_identifier FROM pg_control_system();", db, True
        )
        row = jsql(
            "SELECT to_jsonb(r) FROM core_migration.import_runs r WHERE migration_id='synthetic-identity'",
            db,
        )
        binding = {
            "identity_run": row,
            "native_users": [{"id": n, "personal_account_id": n} for n in range(1, 5)],
        }
        lease = write(db + "-lease", "synthetic owner lease\n")
        st = lease.stat()
        pgpass = write(db + ".pgpass", "synthetic-unused-trust\n")
        target = {
            "status": "initialized-identity-only-opening-target",
            "application_or_worker_started": False,
            "exclusive_offline_resource_lease_required": True,
            "connection": {
                "role": "opening_o",
                "login_role": "opening_i",
                "set_role": "opening_o",
                "database": db,
                "port": PORT,
                "socket_directory": str(SOCK),
                "pgpass_file": str(pgpass),
            },
            "instance_identity": ident,
            "contract": c,
            "identity_import": {
                "migration_id": "synthetic-identity",
                "source_archive_sha256": source_hash,
                "mapping_sha256": "b" * 64,
            },
            "identity_registry_row": row,
            "identity_binding_sha256": F.digest(binding),
            "runtime_role": "opening_r",
            "pre_import_snapshot": snap(db),
            "lease_file": str(lease),
            "lease_identity": {"device": st.st_dev, "inode": st.st_ino},
        }
        path = write(db + "-target.json", target)
        return path, target

    def cli(
        mode, target, label, approval=None, attempt=None, lose_ack=False, expected=0
    ):
        args = [
            sys.executable,
            "-B",
            str(ROOT / "scripts/migrate-legacy-financial.py"),
            "--source-receipt",
            str(R / "source-receipt.json"),
            "--identity-map",
            str(R / "identity-map.jsonl"),
            "--identity-manifest",
            str(R / "identity-manifest.json"),
            "--reuse-export-proof",
            str(R / "export-proof.json"),
            "--target-receipt",
            str(target),
            "--reservation-evidence",
            str(R / "reservation-evidence.json"),
            "--reservation-review",
            str(R / "reservation-review.json"),
            "--migration-id",
            "synthetic-cli-opening",
            "--output",
            str(R / label),
            mode,
        ]
        if approval:
            args += ["--opening-approval", str(approval)]
        if attempt:
            args += ["--opening-attempt", str(attempt)]
        e = ENV.copy()
        if lose_ack:
            e["PATH"] = str(R / "proxy") + os.pathsep + e["PATH"]
            e["OPENING_PROXY_EVIDENCE"] = str(R / label / "proxy-ack-loss.json")
        p = subprocess.run(args, capture_output=True, text=True, env=e, timeout=180)
        write(
            label + "-cli.json",
            {
                "args": args,
                "returncode": p.returncode,
                "stdout": p.stdout,
                "stderr": p.stderr,
            },
        )
        assert p.returncode == expected, label + ": " + p.stderr[-1000:]
        return F.parse_json(p.stdout if expected == 0 else p.stderr)

    def review_candidate(label, plan, tables):
        candidate = F.parse_json(
            (R / label / "opening-approval-candidate.private.json").read_text()
        )
        payloads = candidate["payloads"]
        assert len(payloads) == 4
        expected = [(19, 3), (-10, -2), (7, -7), (0, 0)]
        for p, (a, f) in zip(payloads, expected):
            assert (
                p["available_units"],
                p["affiliate_units"],
                p["reserved_units"],
            ) == (a, f, 0)
            assert p["source_row_sha256"] == F.digest(
                tables["users"][p["account_id"] - 1]
            )
            assert p["mapping_sha256"] == plan["mapping_sha256"]
        assert candidate["review"]["full_payload_set_sha256"] == F.digest(payloads)
        assert candidate["review"]["expected_complete_readback_sha256"] == F.digest(
            O.expected_readback(F, payloads)
        )
        write(
            label + "-independent-review.json",
            {
                "status": "reviewed",
                "candidate_sha256": I.sha256_file(
                    R / label / "opening-approval-candidate.private.json"
                ),
                "payloads_verified_against_restored_synthetic_source": True,
                "reviewer": "independent-opening-acceptance",
            },
        )
        return write(label + "-approved.json", candidate)

    def source_binding():
        files = [
            ROOT / "scripts" / name
            for name in (
                "migrate-legacy-financial.py",
                "migrate-legacy-snapshot.py",
                "legacy_financial_opening.py",
            )
        ]
        files += sorted((ROOT / "services/core/schema").glob("*.sql"))
        files += [
            ROOT / "services/core/src" / name
            for name in ("core_schema.rs", "identity/grants.rs", "identity/schema.rs")
        ]
        return {str(p.relative_to(ROOT)): I.sha256_file(p) for p in files}

    INPUT_BINDING = source_binding()
    write("source-binding-before.json", INPUT_BINDING)

    def run():
        SOCK.mkdir(mode=0o700)
        cmd(
            [
                "initdb",
                "-D",
                str(DATA),
                "--auth=trust",
                "--username=opening_admin",
                "--no-locale",
                "--encoding=UTF8",
            ]
        )
        cmd(
            [
                "pg_ctl",
                "-D",
                str(DATA),
                "-l",
                str(R / "pg.log"),
                "-o",
                "-k "
                + str(SOCK)
                + " -p "
                + str(PORT)
                + " -c listen_addresses='' -c max_connections=20",
                "-w",
                "start",
            ]
        )
        sql(
            "CREATE ROLE opening_o NOLOGIN; CREATE ROLE opening_i LOGIN NOINHERIT; CREATE ROLE opening_r LOGIN; GRANT opening_o TO opening_i; GRANT EXECUTE ON FUNCTION pg_control_system() TO opening_o;"
        )
        receipt, tables, proof, manifest, plan = source_fixture()
        proxy = R / "proxy"
        proxy.mkdir(mode=0o700)
        # An external CLI transport shim: only loses an already successful real psql result, never rewrites or mocks business SQL.
        proxy_script = "#!/usr/bin/env python3\nimport subprocess,sys,os,json,hashlib\nfrom pathlib import Path\ndata=sys.stdin.buffer.read()\np=subprocess.run(['/usr/bin/psql',*sys.argv[1:]],input=data,capture_output=True)\nif b'\\\\unset lmm_opening_login_backend_start' in data and p.returncode==0:\n path=Path(os.environ['OPENING_PROXY_EVIDENCE'])\n path.write_text(json.dumps({'real_psql_exit':p.returncode,'input_sql_sha256':hashlib.sha256(data).hexdigest(),'discarded_real_stdout_sha256':hashlib.sha256(p.stdout).hexdigest(),'discarded_real_stdout':p.stdout.decode(),'communication_failure_injected_after_real_psql_success':True}))\n path.chmod(0o600)\n sys.stderr.write('Synthetic communication fault: successful real psql acknowledgement discarded after COMMIT.\\n')\n sys.exit(2)\nsys.stdout.buffer.write(p.stdout);sys.stderr.buffer.write(p.stderr);sys.exit(p.returncode)\n"
        pp = proxy / "psql"
        pp.write_text(proxy_script)
        pp.chmod(0o700)
        for db, ackloss in [("cli_normal", False), ("cli_acklost", True)]:
            targetpath, target = target_fixture(db, receipt, manifest)
            before = snap(db)
            preview = cli("--opening-preview", targetpath, db + "-preview")
            assert preview["status"] == "opening-preview-review-required"
            assert snap(db) == before
            approval = review_candidate(db + "-preview", plan, tables)
            applied = cli(
                "--apply-opening",
                targetpath,
                db + "-apply",
                approval,
                lose_ack=ackloss,
                expected=int(ackloss),
            )
            after = snap(db)
            assert after != before
            projection = jsql(O.projection_sql(), db)
            payloads = F.parse_json(approval.read_text())["payloads"]
            assert projection == O.expected_readback(F, payloads)
            assert jsql(
                "SELECT jsonb_build_object('sum',sum(delta_units),'journals',count(DISTINCT journal_id)) FROM core_billing.ledger_entries;",
                db,
            ) == {"sum": 0, "journals": 3}
            if ackloss:
                assert (
                    applied["status"] == "opening-outcome-unknown"
                    and applied["automatic_retry"] is False
                )
                proxy_evidence = json.loads(
                    (R / (db + "-apply") / "proxy-ack-loss.json").read_text()
                )
                assert proxy_evidence["real_psql_exit"] == 0
                acknowledged = [
                    F.parse_json(l)
                    for l in proxy_evidence["discarded_real_stdout"].splitlines()
                    if l.startswith("{")
                ]
                assert acknowledged[-1]["status"] == "applied"
                reconcile = cli(
                    "--reconcile-opening",
                    targetpath,
                    db + "-reconcile",
                    approval,
                    R / (db + "-apply") / "opening-attempt.private.json",
                )
                assert reconcile["status"] == "opening-confirmed-committed"
                assert snap(db) == after
                RESULTS.append(
                    "full CLI apply real COMMIT success, external psql acknowledgement dropped, UNKNOWN emitted, original attempt reconcile confirmed committed without any table/sequence mutation"
                )
            else:
                assert applied["status"] == "opening-applied"
                replay = cli("--apply-opening", targetpath, db + "-replay", approval)
                assert replay["status"] == "opening-replay-noop"
                assert snap(db) == after
                RESULTS.append(
                    "full CLI independent reviewed preview, actual full-batch apply, strict replay with all tables/sequences unchanged"
                )
            write(
                db + "-independent-state.json",
                {"before": before, "after": after, "projection": projection},
            )
        assert source_binding() == INPUT_BINDING, (
            "input source drifted during acceptance"
        )
        write("source-binding-after.json", source_binding())
        write(
            "independent-cli-signoff.json",
            {
                "status": "passed",
                "cases": RESULTS,
                "tools": O.source_hashes(F),
                "core_sha256": I.sha256_file(CORE),
                "synthetic_users_only": [1, 2, 3, 4],
                "source_archive_dumped_and_restored": True,
                "source_queries_by_cli": 0,
                "business_mocking": False,
                "limits": [
                    "Acceptance applies only to the exact recorded source/schema/binary hashes",
                    "Synthetic identity fixture receipt/rows; identity writer independently accepted elsewhere",
                    "Lost acknowledgement injected at external psql stdout/exit communication boundary, not PostgreSQL wire connection loss",
                ],
            },
        )
        print(json.dumps({"status": "passed", "cases": RESULTS}))

    try:
        run()
    except Exception:
        write("failure.txt", traceback.format_exc())
        print(traceback.format_exc())
        raise
    finally:
        if (DATA / "postmaster.pid").exists():
            cmd(["pg_ctl", "-D", str(DATA), "-m", "fast", "-w", "stop"])
        write(
            "hashes.json",
            {
                str(p.relative_to(R)): hashlib.sha256(p.read_bytes()).hexdigest()
                for p in R.rglob("*")
                if p.is_file()
                and not p.is_relative_to(DATA)
                and p.name != "hashes.json"
            },
        )


if __name__ == "__main__":
    main()
