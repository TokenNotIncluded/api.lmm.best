"""Opening contract and driver tests. Database evidence lives in the PG suite."""
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import financial_import as F
from test_migrate_legacy_financial import tables, user


def source_fixture():
    identities = {71: 1, 72: 2, 73: 3}
    archive_hash = F.sha(b"synthetic-frozen-archive")
    id_map = "".join(F.canonical({"source_user_id": uid, "target_user_id": uid,
        "target_personal_account_id": aid}) + "\n" for uid, aid in identities.items())
    manifest = {"format_version": 2, "scope": "identity-only", "source_archive_sha256": archive_hash,
        "mapping_sha256": "c" * 64, "source_target_id_map_sha256": F.sha(id_map.encode()),
        "identity_contract": F.IDENTITY.identity_contract()}
    provenance = F.PLANNER.bind_identity_provenance(manifest,
        map_file_sha256=F.sha(id_map.encode()), source_archive_sha256=archive_hash)
    plan = F.PLANNER.plan_financial(tables(users=[user(71, quota=-120), user(72, quota=0, aff_quota=-5),
        user(73, quota=43, aff_quota=0)], tokens=[], user_subscriptions=[], subscription_plans=[]),
        identities, source_sha256=archive_hash, identity_provenance=provenance)
    observation = F.PLANNER.source_observation({"started_utc": "20261001T000000Z"})
    reservations = {"source_archive_sha256": archive_hash, "mapping_sha256": plan["mapping_sha256"],
        "reviewed_evidence_sha256": "d" * 64,
        "positions": [{"source_user_id": uid, "reserved_units": 7 if uid == 71 else 0} for uid in identities]}
    review = {"format_version": 1, "scope": F.SCOPE, "decision": "approved-offline-opening",
        "credits_per_usd": 500000, "source_archive_sha256": archive_hash,
        "mapping_sha256": plan["mapping_sha256"], "identity_map_sha256": plan["identity_map_sha256"],
        "evidence_sha256": "e" * 64, "other_rights_policy": "remain-disabled-pending-separate-import"}
    files = {name: {"path": "/home/test/Backups/" + name, "sha256": "f" * 64} for name in F.SOURCE_FILES}
    files["archive"]["sha256"] = archive_hash
    files["identity_map"]["sha256"] = F.sha(id_map.encode())
    return {"plan": plan, "identities": identities, "observation": observation,
        "reservations": reservations, "review": review, "files": files,
        "identity_run": {"migration_id": "synthetic-identity", "source_archive_sha256": archive_hash,
            "mapping_sha256": manifest["mapping_sha256"], "manifest": manifest,
            "completed_at": "2026-10-01T00:00:01+00:00"}}


def snapshot_fixture(source):
    names = sorted(set(F.CHANGED_TABLES) | {"core_identity.users", "core_identity.accounts",
        "core_migration.import_runs", "core_meta.runtime_contract", "core_meta.component_contract", "public.unrelated"})
    inventory = {"tables": names, "sequences": sorted(F.CHANGED_SEQUENCES)}
    state = {"inventory": inventory, "tables": {name: {"rows": 0, "sha256": F.sha(b"")} for name in names},
        "sequences": {name: {"last_value": 2 if "accounts" in name else 1,
            "is_called": "accounts" in name, "start": 1, "increment": 1, "min": 1,
            "max": 2**63 - 1, "cache": 1, "cycle": False} for name in F.CHANGED_SEQUENCES}}
    return {"state": state, "native_users": [{"id": uid, "personal_account_id": aid}
            for uid, aid in source["identities"].items()], "import_runs": [source["identity_run"]],
        "instance": {"database": "a03_test", "database_owner": "a03_owner", "role": "a03_owner",
            "session_user": "a03_operator", "effective_role_can_login": False,
            "database_oid": "12345", "system_identifier": "7777777", "listen_addresses": "",
            "postmaster_started_at": "2026-10-01 00:00:00+00", "server_instance_nonce": "synthetic-instance",
            "server_version_num": "170006", "port": 5432, "tcp_connection": False},
        "catalog_fingerprint_sha256": "a" * 64, "ownership_sha256": "b" * 64,
        "component_fingerprints": F.IDENTITY.current_contract()["component_fingerprints"]}


def material_fixture(source=None, snapshot=None):
    source = source or source_fixture()
    snapshot = snapshot or snapshot_fixture(source)
    return F.prepare_material(source["plan"], migration_id="synthetic-financial",
        identities=source["identities"], observation=source["observation"], reservations=source["reservations"],
        review=source["review"], snapshot=snapshot, runtime_roles=["a03_runtime"], source_files=source["files"],
        target_contract={**F.IDENTITY.current_contract(), "catalog_fingerprint_sha256": snapshot["catalog_fingerprint_sha256"]})


def approval_fixture(material, sql):
    return {"format_version": 1, "scope": F.SCOPE, "decision": "approve-offline-opening",
        "material_sha256": F.digest(material), "sql_sha256": F.sha(sql.encode()),
        "source_review_sha256": F.digest(material["source_review"])}


class MaterialTests(unittest.TestCase):
    def setUp(self):
        self.material = material_fixture()

    def test_actual_planner_contract_retains_debt_affiliate_and_counters(self):
        self.assertEqual(self.material["opening"][0]["available_units"], -120)
        self.assertEqual(self.material["opening"][0]["affiliate_units"], 38)
        self.assertEqual(self.material["opening"][0]["reserved_units"], 7)
        self.assertEqual(self.material["counters"][0]["used_quota"], 9001)
        self.assertEqual(self.material["counters"][0]["user_id"], 71)
        self.assertEqual(self.material["counters"][0]["account_id"], 1)

    def test_prepare_does_not_modify_arguments(self):
        source = source_fixture()
        snapshot = snapshot_fixture(source)
        before = copy.deepcopy((source, snapshot))
        material_fixture(source, snapshot)
        self.assertEqual((source, snapshot), before)

    def test_edited_payload_cannot_reuse_source_row_hash(self):
        self.material["opening"][0]["available_units"] += 1
        with self.assertRaisesRegex(F.Error, "adapter_mismatch"):
            F.build_apply_sql(self.material)

    def test_unknown_unit_and_unreviewed_rights_fail(self):
        for field, values in {"credits_per_usd": [1, True, 500000.0, None],
            "decision": ["pending"], "other_rights_policy": ["activate-all"]}.items():
            for value in values:
                with self.subTest(field=field, value=value):
                    m = copy.deepcopy(self.material)
                    m["source_review"][field] = value
                    with self.assertRaises(F.Error):
                        F.build_apply_sql(m)

    def test_wrong_mapping_or_source_fails(self):
        for key in ("source_archive_sha256", "mapping_sha256", "identity_map_sha256"):
            m = copy.deepcopy(self.material)
            m[key] = "0" * 64
            with self.subTest(key=key), self.assertRaises(F.Error):
                F.build_apply_sql(m)

    def test_changed_source_plan_fails(self):
        self.material["source_plan"]["wallet_positions"][0]["signed_available_units"] = 1
        with self.assertRaisesRegex(F.Error, "mapping_hash_mismatch"):
            F.validate_material(self.material)

    def test_identity_row_ownership_fails(self):
        self.material["native_users"][0]["personal_account_id"] = 4
        with self.assertRaisesRegex(F.Error, "coverage_mismatch"):
            F.validate_material(self.material)

    def test_wrong_identity_import_manifest_fails(self):
        self.material["identity_import"]["manifest"]["mapping_sha256"] = "0" * 64
        with self.assertRaisesRegex(F.Error, "import_run_mismatch"):
            F.validate_material(self.material)

    def test_network_runtime_or_login_owner_fails(self):
        for key, value in (("tcp_connection", True), ("listen_addresses", "127.0.0.1"),
                           ("effective_role_can_login", True), ("session_user", "a03_owner")):
            m = copy.deepcopy(self.material)
            m["target"]["instance"][key] = value
            with self.subTest(key=key), self.assertRaisesRegex(F.Error, "offline_owner"):
                F.build_apply_sql(m)

    def test_missing_target_boot_nonce_system_id_fails(self):
        for key in ("system_identifier", "postmaster_started_at", "server_instance_nonce"):
            m = copy.deepcopy(self.material)
            del m["target"]["instance"][key]
            with self.subTest(key=key), self.assertRaisesRegex(F.Error, "instance_incomplete"):
                F.build_apply_sql(m)

    def test_source_code_and_component_changes_fail(self):
        for key in ("financial_writer_sha256", "financial_preflight_helper_sha256"):
            m = copy.deepcopy(self.material)
            m["tool_source_hashes"][key] = "0" * 64
            with self.assertRaisesRegex(F.Error, "tool_source_changed"):
                F.build_apply_sql(m)
        self.material["target"]["component_fingerprints"]["ledger"] = "0" * 64
        with self.assertRaisesRegex(F.Error, "component_source_changed"):
            F.build_apply_sql(self.material)

    def test_missing_inventory_or_duplicate_relation_fails(self):
        for inventory in ({"tables": [], "sequences": []},
                          {"tables": ["x.y", "x.y"], "sequences": []}):
            with self.assertRaises(F.Error):
                F.validate_inventory(inventory)

    def test_relation_names_cannot_be_sql(self):
        for name in ("core.foo;drop", "core.foo.bar", "core.\"foo", "core.foo\\bar", "core.foo\x00"):
            with self.assertRaises(F.Error):
                F.relation(name)

    def test_sequence_cache_cycle_boolean_or_overflow_rejected(self):
        for key, value in (("cache", 2), ("cache", True), ("increment", True), ("cycle", True),
                           ("last_value", 2**63 - 1), ("max", 5)):
            m = copy.deepcopy(self.material)
            m["before_state"]["sequences"][F.CHANGED_SEQUENCES[0]][key] = value
            with self.subTest(key=key, value=value), self.assertRaises(F.Error):
                F.build_apply_sql(m)

    def test_sequence_last_available_ids_fit_exactly(self):
        seq = self.material["before_state"]["sequences"][F.CHANGED_SEQUENCES[0]]
        seq.update(last_value=2**63 - 13, is_called=True)
        F.validate_material(self.material)
        seq["last_value"] += 1
        with self.assertRaisesRegex(F.Error, "sequence_overflow"):
            F.validate_material(self.material)

    def test_source_file_inventory_and_archive_hash_are_required(self):
        del self.material["source_files"]["archive"]
        with self.assertRaisesRegex(F.Error, "source_files_incomplete"):
            F.validate_material(self.material)


class SQLTests(unittest.TestCase):
    def setUp(self):
        self.material = material_fixture()
        self.sql = F.build_apply_sql(self.material)

    def test_sql_is_deterministic_and_serializable_with_session_fence_first(self):
        self.assertEqual(self.sql, F.build_apply_sql(copy.deepcopy(self.material)))
        self.assertLess(self.sql.index("pg_try_advisory_lock"), self.sql.index("BEGIN ISOLATION LEVEL SERIALIZABLE"))
        self.assertIn("SET LOCAL synchronous_commit=on", self.sql)
        self.assertIn("ACCESS EXCLUSIVE MODE", self.sql)
        self.assertNotIn("setval(", self.sql)
        self.assertEqual(self.sql.count("ALTER SEQUENCE"), 2)

    def test_success_output_follows_commit_not_a_precommit_receipt(self):
        self.assertLess(self.sql.index("COMMIT;"), self.sql.rindex("'COMMITTED'"))
        self.assertIn("financial_exact_readback_failed", self.sql)
        self.assertIn("financial_ledger_reconciliation_failed", self.sql)
        self.assertIn("financial_unrelated_state_changed", self.sql)

    def test_sql_has_runtime_role_and_live_catalog_checks(self):
        for fragment in ("financial_offline_roles_mismatch", "pg_control_system()", "financial_schema_owner_mismatch",
                         "has_database_privilege", "pg_stat_clear_snapshot()", "financial_target_catalog_mismatch"):
            self.assertIn(fragment, self.sql)

    def test_marker_in_private_source_is_preserved_inside_outer_quote(self):
        marker = "END $lmm_financial$; DO $lmm_financial$ SELECT 'inert';"
        self.material["target"]["instance"]["server_instance_nonce"] = marker
        sql = F.build_apply_sql(self.material)
        self.assertIn(marker.replace("'", "''"), sql)
        self.assertTrue(sql.startswith("SET standard_conforming_strings=on"))

    def test_resolver_is_readonly_and_binds_original_attempt(self):
        sql = F.build_resolve_sql(self.material, "lmm-financial-" + "a" * 32)
        self.assertIn("BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY", sql)
        self.assertIn("financial_absence_not_proven", sql)
        self.assertNotIn("import_opening_balance(", sql)
        self.assertNotIn("INSERT INTO", sql)
        for app in ("", "lmm-financial-x", "lmm-financial-' OR 1=1"):
            with self.assertRaises(F.Error):
                F.build_resolve_sql(self.material, app)

    def test_exact_approval_not_boolean_equivalent_or_template(self):
        approval = approval_fixture(self.material, self.sql)
        F.verify_approval(self.material, self.sql, approval)
        for key, value in (("format_version", True), ("decision", "REVIEW_REQUIRED"),
                           ("sql_sha256", "0" * 64), ("source_review_sha256", "0" * 64)):
            with self.subTest(key=key), self.assertRaises(F.Error):
                F.verify_approval(self.material, self.sql, {**approval, key: value})


class DriverTests(unittest.TestCase):
    def setUp(self):
        self.material = material_fixture()
        self.sql = F.build_apply_sql(self.material)
        self.approval = approval_fixture(self.material, self.sql)
        self.connection = {"role": "a03_owner", "login_role": "a03_operator", "set_role": "a03_owner",
                           "database": "a03_test", "port": 5432}
        self.result = {"status": "COMMITTED", "migration_id": self.material["migration_id"],
                       "material_sha256": F.digest(self.material)}

    def execute(self, output, **kwargs):
        return F.execute(self.material, approval=self.approval, original_sql=self.sql,
                         connection=self.connection, output=output, **kwargs)

    def test_changed_approval_sql_or_files_never_reaches_database(self):
        with tempfile.TemporaryDirectory() as root, patch.object(F, "run_sql") as run:
            for sql in (self.sql + "\n", self.sql.replace("SERIALIZABLE", "READ COMMITTED")):
                with self.assertRaisesRegex(F.Error, "original_sql_changed"):
                    F.execute(self.material, approval=self.approval, original_sql=sql,
                              connection=self.connection, output=Path(root))
            with patch.object(F, "verify_source_files", side_effect=F.Error("source_file_changed")):
                with self.assertRaisesRegex(F.Error, "source_file_changed"):
                    self.execute(Path(root))
            run.assert_not_called()

    def test_file_and_attempt_are_written_before_single_database_call(self):
        with tempfile.TemporaryDirectory() as root, patch.object(F, "verify_source_files"), \
             patch.object(F.IDENTITY, "socket_connection", return_value={}):
            output = Path(root)
            def transport(*args, **kwargs):
                self.assertTrue((output / "attempt.private.json").is_file())
                self.assertEqual((output / "executed.private.sql").read_text(), self.sql)
                self.assertEqual((output / "attempt.private.json").stat().st_mode & 0o777, 0o600)
                return 0, F.canonical(self.result)
            with patch.object(F, "run_sql", side_effect=transport) as run:
                self.assertEqual(self.execute(output), self.result)
                run.assert_called_once()

    def test_nonzero_exit_lost_ack_bad_json_timeout_are_unknown_without_retry(self):
        values = [(1, ""), (0, ""), (0, "not-json"), (1, F.canonical(self.result)),
                  (0, F.canonical({**self.result, "migration_id": "wrong"})),
                  subprocess.TimeoutExpired("synthetic", 1), OSError("synthetic-private-error")]
        for value in values:
            with self.subTest(value=type(value).__name__), tempfile.TemporaryDirectory() as root, \
                 patch.object(F, "verify_source_files"), patch.object(F.IDENTITY, "socket_connection", return_value={}):
                with patch.object(F, "run_sql", **({"side_effect": value} if isinstance(value, Exception)
                                                  else {"return_value": value})) as run:
                    result = self.execute(Path(root))
                    self.assertEqual(result, {**self.result, "status": "UNKNOWN"})
                    run.assert_called_once()

    def test_resolve_only_uses_matching_original_identity(self):
        attempt = {"operation": "apply", "migration_id": self.material["migration_id"],
            "material_sha256": F.digest(self.material), "sql_sha256": F.sha(self.sql.encode()),
            "application_name": "lmm-financial-" + "a" * 32}
        with tempfile.TemporaryDirectory() as root, patch.object(F, "verify_source_files"), \
             patch.object(F.IDENTITY, "socket_connection", return_value={}), \
             patch.object(F, "run_sql", return_value=(0, F.canonical({**self.result, "status": "NOT_APPLIED"}))) as run:
            result = self.execute(Path(root), original_attempt=attempt)
            self.assertEqual(result["status"], "NOT_APPLIED")
            self.assertIn("READ ONLY", run.call_args.args[1])
        with tempfile.TemporaryDirectory() as root, patch.object(F, "verify_source_files"), \
             patch.object(F.IDENTITY, "socket_connection", return_value={}), patch.object(F, "run_sql") as run:
            attempt["migration_id"] = "another-id"
            with self.assertRaisesRegex(F.Error, "original_attempt_material_mismatch"):
                self.execute(Path(root), original_attempt=attempt)
            run.assert_not_called()

    def test_unknown_remains_unknown_when_private_result_cannot_be_saved(self):
        original_write = F.write_private
        def write(directory, name, value):
            if name == "result.private.json":
                raise OSError("synthetic disk full")
            return original_write(directory, name, value)
        with tempfile.TemporaryDirectory() as root, patch.object(F, "verify_source_files"), \
             patch.object(F.IDENTITY, "socket_connection", return_value={}), \
             patch.object(F, "run_sql", return_value=(0, F.canonical(self.result))), \
             patch.object(F, "write_private", side_effect=write):
            self.assertEqual(self.execute(Path(root))["status"], "UNKNOWN")


if __name__ == "__main__":
    unittest.main()
