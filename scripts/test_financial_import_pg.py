"""Real PostgreSQL opening tests, in one owned network-isolated Docker container.

Never accepts a DSN or accesses the workflow's database. The exact installed SQL
comes from the compiled core's 11 components. No image is downloaded. Set
LMM_FINANCIAL_PG_TEST=1 to opt in; absence is a visible unittest skip, not PG proof.
The Docker transport exercises generated SQL, not the host Unix-socket CLI gate.
"""
import copy
import json
import os
from pathlib import Path
import re
import selectors
import subprocess
import time
import unittest
import uuid

import financial_import as F
from test_financial_import import source_fixture, material_fixture

ROOT = Path(__file__).resolve().parents[1]


@unittest.skipUnless(os.environ.get("LMM_FINANCIAL_PG_TEST") == "1", "isolated PostgreSQL test not enabled")
class PostgreSQLTests(unittest.TestCase):
    @classmethod
    def docker(cls, *args, input=None, check=True, timeout=90):
        result = subprocess.run(["docker", "--host", "unix:///var/run/docker.sock", *args],
            input=input, text=True, capture_output=True, timeout=timeout, check=False)
        if check and result.returncode:
            raise AssertionError("synthetic Docker fixture failed: " + result.stderr[-4000:])
        return result

    @classmethod
    def setUpClass(cls):
        cls.name = "lmm-a03-" + uuid.uuid4().hex
        cls.label = "lmm.test.a03=" + cls.name
        image = cls.docker("image", "inspect", "postgres:17-alpine", "--format", "{{.Id}}")
        cls.image = image.stdout.strip()
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", cls.image):
            raise AssertionError("preinstalled PostgreSQL image identity missing")
        print("A03_POSTGRES_IMAGE=" + cls.image, flush=True)
        cls.docker("run", "--detach", "--pull=never", "--network=none", "--name", cls.name,
            "--label", cls.label, "--memory=384m", "--cpus=1", "--pids-limit=128",
            "--tmpfs", "/var/lib/postgresql/data:rw,size=512m", "--tmpfs", "/var/run/postgresql:rw,size=16m",
            "--env", "POSTGRES_HOST_AUTH_METHOD=trust", cls.image, "postgres",
            "-c", "listen_addresses=", "-c", "autovacuum=off", "-c", "max_connections=20",
            "-c", "shared_buffers=32MB", "-c", "lmm.migration_instance_nonce=" + cls.name)
        cls.addClassCleanup(cls.cleanup)
        # The image starts a temporary bootstrap server first. Wait until PID 1
        # has execed the final server, not merely for that temporary socket.
        for _ in range(100):
            if cls.docker("exec", cls.name, "sh", "-c",
                'test "$(cat /proc/1/comm)" = postgres && pg_isready -q -U postgres',
                check=False).returncode == 0:
                break
            time.sleep(0.1)
        else:
            raise AssertionError("owned PostgreSQL fixture did not become ready")
        cls.db = "postgres"
        cls.sql("CREATE ROLE a03_owner NOLOGIN; CREATE ROLE a03_operator LOGIN;"
                "GRANT a03_owner TO a03_operator; CREATE ROLE a03_runtime NOLOGIN;"
                "CREATE ROLE a03_other LOGIN;", admin=True)

    @classmethod
    def cleanup(cls):
        # Never remove a container unless the exact random ownership label matches.
        value = cls.docker("inspect", cls.name, "--format", '{{index .Config.Labels "lmm.test.a03"}}').stdout.strip()
        if value != cls.name:
            raise AssertionError("refusing to remove an unowned test container")
        cls.docker("rm", "--force", "--volumes", cls.name)

    @classmethod
    def command(cls, *, admin=False, application_name="a03-synthetic-test"):
        args = ["docker", "--host", "unix:///var/run/docker.sock", "exec", "-i",
                "--env", "PGAPPNAME=" + application_name]
        if not admin:
            args += ["--env", "PGOPTIONS=-c role=a03_owner"]
        return args + [cls.name, "psql", "-X", "--no-password", "-qAt", "-v", "ON_ERROR_STOP=1",
                       "-h", "/var/run/postgresql", "-U", "postgres" if admin else "a03_operator", "-d", cls.db]

    @classmethod
    def sql(cls, sql, *, admin=False, check=True, application_name="a03-synthetic-test"):
        result = subprocess.run(cls.command(admin=admin, application_name=application_name), input=sql,
            text=True, capture_output=True, timeout=140, check=False)
        if check and result.returncode:
            raise AssertionError("synthetic PostgreSQL SQL failed: " + result.stderr[-6000:])
        return result

    @classmethod
    def query(cls, sql):
        return json.loads(cls.sql("SET timezone='UTC'; " + sql).stdout)

    def setUp(self):
        type(self).db = "postgres"
        db = "a03_" + uuid.uuid4().hex
        self.sql("CREATE DATABASE " + db + " OWNER a03_owner;"
                 "REVOKE CONNECT ON DATABASE " + db + " FROM PUBLIC;"
                 "GRANT CONNECT ON DATABASE " + db + " TO a03_operator;", admin=True)
        type(self).db = db
        self.sql("GRANT EXECUTE ON FUNCTION pg_control_system() TO a03_owner;", admin=True)
        contract = F.IDENTITY.current_contract()
        components = []
        for name in contract["component_fingerprints"]:
            path = ROOT / "services/core/schema" / (name.replace("-", "_") + ".sql")
            components.append(path.read_text())
        schema_source = (ROOT / "services/core/src/core_schema.rs").read_text()
        contracts = re.findall(r'const CONTRACT: &str = "(.*?)";', schema_source)
        self.assertEqual(len(contracts), 1)
        ddl = "BEGIN;\n" + "\n".join(components) + contracts[0]
        ddl += "INSERT INTO core_meta.schema_contract(version,fingerprint) VALUES(3,decode('" + \
               contract["identity_fingerprint_sha256"] + "','hex'));"
        for name, fingerprint in contract["component_fingerprints"].items():
            ddl += "INSERT INTO core_meta.component_contract VALUES(" + F.literal(name) + ",decode(" + F.literal(fingerprint) + ",'hex'));"
        ddl += "INSERT INTO core_meta.runtime_contract(catalog_fingerprint) SELECT decode((" + F.IDENTITY.catalog_hash_sql(contract) + "),'hex');COMMIT;"
        self.sql(ddl)
        self.source = source_fixture()
        values = ",".join("(" + str(aid) + ",'personal')" for aid in self.source["identities"].values())
        seed = "BEGIN; INSERT INTO core_identity.accounts(id,kind) OVERRIDING SYSTEM VALUE VALUES " + values + ";"
        seed += "INSERT INTO core_identity.users(id,personal_account_id) VALUES " + ",".join(
            "(" + str(uid) + "," + str(aid) + ")" for uid, aid in self.source["identities"].items()) + ";"
        run = self.source["identity_run"]
        seed += "INSERT INTO core_migration.import_runs(migration_id,source_archive_sha256,mapping_sha256,manifest,completed_at) VALUES(" + \
                ",".join(F.literal(run[k]) for k in ("migration_id", "source_archive_sha256", "mapping_sha256", "manifest", "completed_at")) + ");COMMIT;"
        self.sql(seed)
        self.inventory = self.query(F.inventory_sql())
        self.material = self.capture_material()
        self.apply_sql = F.build_apply_sql(self.material)
        self.before = self.full_state()

    def capture_material(self):
        snapshot = json.loads(self.sql(F.snapshot_sql(self.inventory, "synthetic-financial")).stdout)
        return material_fixture(self.source, snapshot)

    def full_state(self):
        # Include the receipt for independent before/after comparisons, unlike
        # the receipt's self-excluding hash used in the writer transaction.
        return self.query(F.state_sql(self.inventory, "no-such-migration"))

    def rejected_unchanged(self, sql, message):
        before = self.full_state()
        result = self.sql(sql, check=False)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn(message, result.stderr)
        self.assertEqual(self.full_state(), before)

    def test_opening_exact_balanced_and_strict_replay(self):
        result = json.loads(self.sql(self.apply_sql).stdout)
        self.assertEqual(result["status"], "COMMITTED")
        after = self.full_state()
        self.assertNotEqual(after, self.before)
        self.assertEqual(json.loads(self.sql(self.apply_sql).stdout), result)
        self.assertEqual(self.full_state(), after)
        balances = self.query("SELECT jsonb_object_agg(bucket,balance_units) FROM core_billing.ledger_accounts a JOIN core_billing.balance_state b ON b.ledger_account_id=a.id WHERE a.owner_account_id=1")
        self.assertEqual(balances, {"wallet": 0, "debt": -120, "reserved": 7, "affiliate": 38})
        self.assertEqual(self.query("SELECT coalesce(sum(balance_units),0) FROM core_billing.balance_state"), 0)

    def test_same_id_changed_mapping_and_different_id_are_readonly_conflicts(self):
        self.sql(self.apply_sql)
        # A different source plan must be completely rehashed, not just edit one
        # request. Even independently approved SQL must not replace this receipt.
        other = copy.deepcopy(self.material)
        other["source_review"]["evidence_sha256"] = "0" * 64
        self.rejected_unchanged(F.build_apply_sql(other), "financial_replay_conflict")
        other = copy.deepcopy(self.material)
        other["migration_id"] = "different-financial-id"
        for row in other["opening"] + other["counters"]:
            row["migration_id"] = other["migration_id"]
        self.rejected_unchanged(F.build_apply_sql(other), "financial_other_import_conflict")

    def test_wrong_target_oid_boot_nonce_and_catalog_are_rejected(self):
        for field, value in (("database_oid", "1"), ("system_identifier", "1"),
                             ("postmaster_started_at", "2000-01-01 00:00:00+00"),
                             ("server_instance_nonce", "other-instance")):
            material = copy.deepcopy(self.material)
            material["target"]["instance"][field] = value
            self.rejected_unchanged(F.build_apply_sql(material), "financial_target_instance_mismatch")
        material = copy.deepcopy(self.material)
        material["target"]["catalog_fingerprint_sha256"] = "0" * 64
        self.rejected_unchanged(F.build_apply_sql(material), "financial_target_catalog_mismatch")

    def test_actual_catalog_mutation_and_business_fact_cas_are_rejected(self):
        self.sql("UPDATE core_identity.users SET auth_version=2 WHERE id=71;")
        self.rejected_unchanged(self.apply_sql, "financial_target_state_changed")
        self.sql("ALTER TABLE core_identity.users ADD COLUMN unexpected_test_column text;")
        self.rejected_unchanged(self.apply_sql, "financial_target_catalog_mismatch")

    def test_runtime_login_or_unlisted_connect_role_is_rejected(self):
        self.sql("ALTER ROLE a03_runtime LOGIN;", admin=True)
        try:
            self.rejected_unchanged(self.apply_sql, "financial_offline_roles_mismatch")
        finally:
            self.sql("ALTER ROLE a03_runtime NOLOGIN;", admin=True)
        self.sql("GRANT CONNECT ON DATABASE " + self.db + " TO a03_other;", admin=True)
        self.rejected_unchanged(self.apply_sql, "financial_offline_roles_mismatch")

    def test_partial_sql_failure_rolls_back_every_table_and_sequence(self):
        # Inject a failure AFTER a completed opening. Test-only altered SQL is
        # sent directly; the normal CLI rejects it because its hash differs.
        broken = self.apply_sql.replace("PERFORM core_billing.import_opening_balance(v_payload);",
            "PERFORM core_billing.import_opening_balance(v_payload); RAISE EXCEPTION 'synthetic-mid-import';", 1)
        self.rejected_unchanged(broken, "synthetic-mid-import")
        self.assertEqual(self.full_state(), self.before)
        self.assertEqual(json.loads(self.sql(self.apply_sql).stdout)["status"], "COMMITTED")

    def test_terminated_backend_mid_opening_rolls_back_then_resolves(self):
        app = "lmm-financial-" + uuid.uuid4().hex
        call = "PERFORM core_billing.import_opening_balance(v_payload);"
        self.assertIn(call, self.apply_sql)
        # A NOTICE is delivered before COMMIT, unlike NOTIFY. Wait until the
        # first opening has changed rows and sequences, then end only that
        # uniquely named test backend. The normal CLI rejects this altered SQL.
        interrupted = self.apply_sql.replace(call, call +
            " RAISE NOTICE 'A03_OPENING_PAUSED'; PERFORM pg_sleep(30);", 1)
        with subprocess.Popen(self.command(application_name=app), stdin=subprocess.PIPE,
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True) as process:
            self.addCleanup(lambda: process.kill() if process.poll() is None else None)
            process.stdin.write(interrupted)
            process.stdin.close()
            process.stdin = None
            notice = b""
            deadline = time.monotonic() + 10
            with selectors.DefaultSelector() as selector:
                selector.register(process.stderr, selectors.EVENT_READ)
                while b"A03_OPENING_PAUSED" not in notice and time.monotonic() < deadline:
                    if selector.select(timeout=0.2):
                        chunk = os.read(process.stderr.fileno(), 4096)
                        if not chunk:
                            break
                        notice += chunk
            self.assertIn(b"A03_OPENING_PAUSED", notice)
            stopped = self.sql("SELECT pg_terminate_backend(pid) FROM pg_stat_activity "
                "WHERE datname=" + F.literal(self.db) + " AND application_name=" + F.literal(app) +
                " AND pid<>pg_backend_pid();", admin=True)
            self.assertEqual(stopped.stdout.strip(), "t")
            process.communicate(timeout=10)
            self.assertNotEqual(process.returncode, 0)
        self.assertEqual(self.full_state(), self.before)
        resolved = json.loads(self.sql(F.build_resolve_sql(self.material, app)).stdout)
        self.assertEqual(resolved["status"], "NOT_APPLIED")
        self.assertEqual(self.full_state(), self.before)
        self.assertEqual(json.loads(self.sql(self.apply_sql).stdout)["status"], "COMMITTED")

    def test_lost_commit_ack_is_resolved_without_writing_again(self):
        app = "lmm-financial-" + uuid.uuid4().hex
        self.sql(self.apply_sql, application_name=app)  # Intentionally discard commit output.
        after = self.full_state()
        result = json.loads(self.sql(F.build_resolve_sql(self.material, app)).stdout)
        self.assertEqual(result["status"], "COMMITTED")
        self.assertEqual(self.full_state(), after)

    def test_absent_receipt_requires_exact_pre_state(self):
        app = "lmm-financial-" + uuid.uuid4().hex
        resolver = F.build_resolve_sql(self.material, app)
        self.assertEqual(json.loads(self.sql(resolver).stdout)["status"], "NOT_APPLIED")
        self.assertEqual(self.full_state(), self.before)
        self.sql("UPDATE core_identity.users SET auth_version=2 WHERE id=71;")
        self.rejected_unchanged(resolver, "financial_absence_not_proven")

    def test_competing_session_fence_and_unknown_original_session(self):
        app = "lmm-financial-" + uuid.uuid4().hex
        process = subprocess.Popen(self.command(application_name=app), stdin=subprocess.PIPE,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, bufsize=1)
        self.addCleanup(lambda: process.kill() if process.poll() is None else None)
        process.stdin.write("SELECT pg_advisory_lock(" + str(F.FENCE) + "); SELECT 'FENCED';\n")
        process.stdin.flush()
        ready = b""
        deadline = time.monotonic() + 10
        with selectors.DefaultSelector() as selector:
            selector.register(process.stdout, selectors.EVENT_READ)
            while b"FENCED\n" not in ready and time.monotonic() < deadline:
                if selector.select(timeout=0.2):
                    chunk = os.read(process.stdout.fileno(), 4096)
                    if not chunk:
                        break
                    ready += chunk
        self.assertIn(b"FENCED\n", ready)
        # No state probe while this session is alive: it is the sentinel.
        result = self.sql(self.apply_sql, check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("financial_import_busy", result.stderr)
        result = self.sql(F.build_resolve_sql(self.material, app), check=False)
        self.assertIn("financial_import_busy", result.stderr)
        process.stdin.close()
        self.assertEqual(process.wait(timeout=10), 0)
        process.stdout.close()
        process.stderr.close()
        self.assertEqual(self.full_state(), self.before)
        self.assertEqual(json.loads(self.sql(F.build_resolve_sql(self.material, app)).stdout)["status"], "NOT_APPLIED")

    def test_future_ledger_transaction_prevents_strict_opening_replay(self):
        self.sql(self.apply_sql)
        # This is the existing ledger function, not a new arbitrary runtime API.
        request = {"scope": "a03-synthetic", "operation_key": "after-opening", "actor_user_id": 71,
            "reason": "synthetic debt payment", "action": {"kind": "credit", "account_id": 1,
            "amount_units": 150, "payment_reference": "a03-synthetic:payment"}}
        # Match the existing native JSON encoding; verified against ledger types.
        response = json.loads(self.sql("BEGIN ISOLATION LEVEL SERIALIZABLE; SET LOCAL synchronous_commit=on;"
            "SELECT core_billing.post_ledger(" + F.literal(request) + "::jsonb); COMMIT;").stdout)
        self.assertEqual(response["status"], "posted", response)
        balances = self.query("SELECT jsonb_object_agg(bucket,balance_units) FROM core_billing.ledger_accounts a JOIN core_billing.balance_state b ON b.ledger_account_id=a.id WHERE a.owner_account_id=1")
        self.assertEqual(balances, {"wallet": 30, "debt": 0, "reserved": 7, "affiliate": 38})
        self.rejected_unchanged(self.apply_sql, "financial_replay_conflict")


if __name__ == "__main__":
    unittest.main(verbosity=2)
