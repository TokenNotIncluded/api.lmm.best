"""Developer synthetic source-rights tests; no PostgreSQL or process acceptance."""

import copy
from contextlib import redirect_stderr, redirect_stdout
from decimal import Decimal
import importlib.util
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parent


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


F = load("financial_key_reconciliation", ROOT / "migrate-legacy-financial.py")
R = load("rights_key_reconciliation", ROOT / "legacy_financial_reconciliation.py")
UID = 9007199254740993
ACCOUNT = UID + 100


def token(**changes):
    return {"id": 17, "user_id": UID, "remain_quota": -3, "used_quota": 70,
            "unlimited_quota": False, "model_limits_enabled": False,
            "expired_time": -1, "status": 1, "key": "SYNTHETIC-SECRET", **changes}


def fixture(rows=None, *, account_collision=False):
    tables = {table: [] for table in F.REQUIRED_TABLES}
    tables["users"] = [{"id": UID, "quota": 29, "aff_quota": 7,
                        "used_quota": 101, "request_count": 11, "aff_history": 5}]
    tables["tokens"] = [token()] if rows is None else rows
    identities = {UID: ACCOUNT}
    if account_collision:
        tables["users"].append({**tables["users"][0], "id": UID + 1})
        identities[UID + 1] = ACCOUNT
    plan = F.plan_financial(tables, identities, source_sha256="a" * 64,
        primary_keys={name: ["id"] for name in tables},
        column_schema={name: [] for name in tables})
    return dict(tables=tables, identities=identities, plan=plan,
        proof={"selected_table_counts": {name: len(rows) for name, rows in tables.items()},
               "source_queries_this_run": 0, "source_archive_sha256": "a" * 64,
               "source_export_sha256": "b" * 64},
        identity_map_file_sha256="c" * 64, identity_manifest_file_sha256="d" * 64,
        tool_hashes={})


def reseal(args):
    plan = args["plan"]
    plan["mapping_sha256"] = F.digest({k: v for k, v in plan.items()
                                       if k not in ("mapping_sha256", "public_summary")})
    plan["public_summary"]["mapping_sha256"] = plan["mapping_sha256"]


# @mark migration.financial-rights role=test
class KeyReconciliationTest(unittest.TestCase):
    def test_signed_remaining_and_usage_are_separate_from_wallet_and_debt(self):
        args = fixture()
        before = copy.deepcopy(args)
        result = R.build(F, **args)
        basis = result["key_quota_reconciliation"]
        row = basis["records"][0]
        self.assertEqual((row["source_key_id"], row["source_user_id"],
                          row["target_account_id"]), (17, UID, ACCOUNT))
        self.assertEqual((row["source_remaining_integer"], row["source_used_integer"],
                          row["display_basis_integer"]), (-3, 70, 67))
        self.assertEqual(row["display_basis_integer"] - row["source_used_integer"], -3)
        self.assertEqual(row["source_row_sha256"], F.digest(args["tables"]["tokens"][0]))
        self.assertEqual(row["source_export_ordinal"], 0)
        self.assertEqual(row["unit"], "source-field-unit-unproven")
        self.assertIsNone(row["target_key_id"])
        self.assertFalse(row["credential_activation_allowed"])
        self.assertFalse(basis["native_import_payloads_created"])
        self.assertEqual(basis["wallet_delta_units"], 0)
        self.assertEqual(basis["target_writes"], 0)
        self.assertEqual(result["wallet_integer_totals"]["wallet_units"], 29)
        self.assertEqual(result["wallet_positions"][0]["historical_used_units"], 101)
        self.assertNotIn("SYNTHETIC-SECRET", json.dumps(result))
        self.assertEqual(args, before)
        self.assertEqual(result, R.build(F, **args))  # Repeat is pure, not a second debit.

    def test_source_quota_is_not_scaled_or_converted_to_usd(self):
        row = R.build(F, **fixture([token(remain_quota=500000, used_quota=17)]))["key_quota_reconciliation"]["records"][0]
        self.assertEqual((row["source_remaining_integer"], row["source_used_integer"],
                          row["display_basis_integer"]), (500000, 17, 500017))
        self.assertEqual(row["unit"], "source-field-unit-unproven")
        self.assertIn("source-unit-and-deployed-policy-unproven", row["blockers"])

    def test_unlimited_retains_signed_display_basis_not_infinite_cash(self):
        result = R.build(F, **fixture([token(unlimited_quota=True, remain_quota=-91)]))
        row = result["key_quota_reconciliation"]["records"][0]
        self.assertTrue(row["source_unlimited"])
        self.assertEqual(row["display_basis_integer"], -21)
        self.assertIsNone(row["candidate_lifetime_limit_integer"])
        self.assertEqual(row["source_remaining_integer"], -91)
        self.assertIn("source-unit-and-deployed-policy-unproven", row["blockers"])

    def test_unknown_state_deleted_and_expired_are_not_activated(self):
        with self.assertRaisesRegex(F.MappingError, "key_status_unknown"):
            R.build(F, **fixture([token(status="future-state", deleted_at=123, expired_time=1)]))
        row = R.build(F, **fixture([token(status=2, deleted_at=123,
                         expired_time=1)]))["key_quota_reconciliation"]["records"][0]
        self.assertEqual(row["source_status"], 2)
        self.assertEqual(row["source_deleted_at"], 123)
        self.assertEqual(row["source_expired_time"], 1)
        self.assertFalse(row["credential_activation_allowed"])
        self.assertIn("source-state-and-restrictions-not-authorized", row["blockers"])

    def test_orphan_owner_retains_fact_but_no_target_account(self):
        row = R.build(F, **fixture([token(user_id=UID + 1)]))["key_quota_reconciliation"]["records"][0]
        self.assertIsNone(row["target_account_id"])
        self.assertIn("orphan_key_owner", row["blockers"])

    def test_two_source_owners_cannot_share_one_target_account(self):
        args = fixture([token(), token(id=18, user_id=UID + 1, key="SECOND-SYNTHETIC-SECRET")], account_collision=True)
        with self.assertRaisesRegex(F.MappingError, "reconciliation_key_account_mapping_not_unique"):
            R.build(F, **args)

    def test_unlimited_display_overflow_is_unknown_not_wrapped(self):
        row = R.build(F, **fixture([token(used_quota=F.BIGINT_MAX, remain_quota=1,
                    unlimited_quota=True)]))["key_quota_reconciliation"]["records"][0]
        self.assertEqual(row["display_basis_integer"], F.BIGINT_MAX + 1)
        self.assertIsNone(row["candidate_lifetime_limit_integer"])
        self.assertIn("key_display_basis_out_of_range", row["blockers"])

    def test_negative_used_and_out_of_range_basis_remain_blocked_raw_integers(self):
        for changes, blocker in (({"used_quota": -1}, "negative_key_used_counter"),
                ({"used_quota": F.BIGINT_MAX, "remain_quota": 1}, "key_lifetime_limit_out_of_range"),
                ({"remain_quota": -71}, "key_lifetime_limit_out_of_range")):
            with self.subTest(changes=changes):
                args = fixture([token(**changes)])
                row = R.build(F, **args)["key_quota_reconciliation"]["records"][0]
                self.assertIn(blocker, row["blockers"])
                self.assertEqual(row["source_used_integer"], args["tables"]["tokens"][0]["used_quota"])
                self.assertFalse(row["credential_activation_allowed"])

    def test_plan_rehash_cannot_reassign_or_recount_key(self):
        for field, value in (("source_user_id", UID + 1), ("account_id", ACCOUNT + 1),
                ("remaining_units", 4), ("used_units", 0), ("unlimited", True),
                ("lifetime_limit_units", 68), ("source_row_sha256", "0" * 64),
                ("credential_activation_allowed", True), ("source_status", 2),
                ("expired_time", 0), ("restrictions", {}),
                ("used_units", Decimal("70.0")),
                ("source_user_id", True)):
            with self.subTest(field=field, value=value):
                args = fixture()
                args["plan"]["key_limits"][0][field] = value
                reseal(args)
                with self.assertRaisesRegex(F.MappingError, "reconciliation_key_plan_mismatch"):
                    R.build(F, **args)

    def test_plan_missing_extra_and_duplicate_records_refuse(self):
        for kind in ("missing", "extra", "duplicate"):
            with self.subTest(kind=kind):
                args = fixture()
                if kind == "missing":
                    args["plan"]["key_limits"] = []
                else:
                    extra = copy.deepcopy(args["plan"]["key_limits"][0])
                    if kind == "extra":
                        extra["source_key_id"] = 18
                    args["plan"]["key_limits"].append(extra)
                reseal(args)
                with self.assertRaisesRegex(F.MappingError, "reconciliation_key_plan_mismatch"):
                    R.build(F, **args)

    def test_numeric_plan_value_cannot_impersonate_persisted_decimal_display_tag(self):
        args = fixture([token(model_limits=Decimal("1.0"))])
        R.build(F, **args)  # Original typed source and persisted planner agree.
        planned = args["plan"]["key_limits"][0]["restrictions"]
        self.assertEqual(planned["model_limits"], {"$decimal": "1.0"})
        planned["model_limits"] = Decimal("1.0")
        reseal(args)
        with self.assertRaisesRegex(F.MappingError, "reconciliation_key_plan_mismatch"):
            R.build(F, **args)

    def test_duplicate_source_id_and_unknown_integer_types_refuse(self):
        # Source PK validation rejects before planner mapping; the mapper must
        # also reject duplicates if called without the source-evidence wrapper.
        with self.assertRaisesRegex(F.MappingError, "source_primary_key_value_duplicate"):
            fixture([token(), token(remain_quota=2)])
        with self.assertRaisesRegex(F.MappingError, "key_id_duplicate"):
            F.map_key_limits([token(), token(remain_quota=2)], {UID: ACCOUNT})
        for field in ("remain_quota", "used_quota", "id", "user_id"):
            for value in (True, Decimal("1.0"), "1", None):
                with self.subTest(field=field, value=value), self.assertRaises(F.MappingError):
                    fixture([token(**{field: value})])
        for field in ("unlimited_quota", "model_limits_enabled"):
            with self.subTest(field=field), self.assertRaises(F.MappingError):
                fixture([token(**{field: None})])

    def test_keys_and_user_usage_overlap_never_sum_as_new_money(self):
        result = R.build(F, **fixture([token(), token(id=18, remain_quota=5, key="SECOND-SYNTHETIC-SECRET")]))
        basis = result["key_quota_reconciliation"]
        self.assertEqual(basis["record_count"], 2)
        self.assertTrue(basis["overlapping_counters_do_not_sum"])
        self.assertEqual(result["wallet_integer_totals"]["wallet_units"], 29)
        self.assertFalse(result["approval_created"])
        self.assertFalse(result["public_summary"]["reserved_zero_proven"])
        self.assertEqual(result["public_summary"]["financial_rows_migrated"], 0)


# @mark migration.financial-rights role=test
class AdversarialKeyTest(unittest.TestCase):
    """All seven independent probes from /tmp/lmm-delegate/adversarial_key_test.py.

    The old unknown-status probe expected normal return. Strict rejection is
    stronger: retain its no-secret assertion on the error, not a private report.
    The original external file is unchanged and its incompatibility is reported.
    """
    def test_wrong_owner_in_resealed_plan_rejected(self):
        args = fixture()
        args["plan"]["key_limits"][0]["account_id"] = 999
        reseal(args)
        with self.assertRaisesRegex(F.MappingError, "reconciliation_key_plan_mismatch"):
            R.build(F, **args)

    def test_revoked_source_reported_active_in_plan_rejected(self):
        args = fixture([token(status=2)])
        args["plan"]["key_limits"][0]["source_status"] = 1
        reseal(args)
        with self.assertRaisesRegex(F.MappingError, "reconciliation_key_plan_mismatch"):
            R.build(F, **args)

    def test_missing_planned_key_rejected(self):
        args = fixture()
        args["plan"]["key_limits"] = []
        reseal(args)
        with self.assertRaisesRegex(F.MappingError, "reconciliation_key_plan_mismatch"):
            R.build(F, **args)

    def test_duplicate_secret_under_distinct_ids_rejected(self):
        row = token(); other = {**row, "id": 78}
        with self.assertRaises(F.MappingError):
            R.build(F, **fixture([row, other]))

    def test_missing_secret_rejected(self):
        row = token(); del row["key"]
        with self.assertRaises(F.MappingError):
            R.build(F, **fixture([row]))

    def test_ordinary_secret_not_in_output_and_never_activates(self):
        result = R.build(F, **fixture())
        self.assertNotIn("SYNTHETIC-SECRET", json.dumps(result))
        key = result["key_quota_reconciliation"]["records"][0]
        self.assertFalse(key["credential_activation_allowed"])
        self.assertIsNone(key["target_key_id"])
        self.assertFalse(result["key_reconciliation_passed"])

    def test_secret_embedded_in_unknown_status_never_leaks(self):
        row = token(); row["status"] = "unexpected-state:SYNTHETIC-SECRET"
        with self.assertRaisesRegex(F.MappingError, "key_status_unknown") as caught:
            R.build(F, **fixture([row]))
        self.assertNotIn("SYNTHETIC-SECRET", str(caught.exception))


def target_fixture():
    # Handwritten target EXPORT, deliberately NOT generated by map_key_limits
    # or source_key_comparison_facts. This is the independent comparison boundary.
    return {"format_version": 1, "scope": "legacy-api-keys", "records": [{
        "source_key_id": 17, "target_key_id": 501, "account_id": ACCOUNT,
        "status": 1, "deleted_at": None, "expired_time": -1,
        "unlimited": False, "remaining_units": -3, "used_units": 70,
        "lifetime_limit_units": 67,
        "secret_sha256": "51325b6df2b8f087f99b463606e6794596f8589d3301539abee173985d1d4d77",
        "restrictions": {"model_limits_enabled": False, "model_limits": None,
            "allow_ips": None, "group": None, "auto_groups": None,
            "cross_group_retry": None, "oauth_managed": None, "account_balance_read": None,
            "one_time_reveal": None, "creation_source": None}}]}


# @mark migration.financial-rights role=test
class TargetKeyReconciliationTest(unittest.TestCase):
    def reconcile(self, inventory):
        return R.build(F, **fixture(), target_key_inventory=inventory)

    def test_export_matches_and_binds_real_target_id_without_writes(self):
        inventory = target_fixture(); before = copy.deepcopy(inventory)
        result = self.reconcile(inventory)
        keys = result["key_quota_reconciliation"]
        self.assertTrue(result["key_reconciliation_passed"])
        self.assertEqual(keys["comparison_counts"], {"matched": 1})
        self.assertEqual(keys["records"][0]["target_key_id"], 501)
        self.assertEqual(keys["target_inventory_sha256"], F.digest(inventory))
        self.assertFalse(keys["records"][0]["credential_activation_allowed"])
        self.assertEqual(result["target_writes"], 0)
        self.assertEqual(inventory, before)

    def test_missing_inventory_and_empty_inventory_fail_closed(self):
        for inventory in (None, {"format_version": 1, "scope": "legacy-api-keys", "records": []}):
            with self.subTest(inventory=inventory):
                result = self.reconcile(inventory)
                self.assertFalse(result["key_reconciliation_passed"])
                self.assertEqual(result["status"], "failed-key-reconciliation")
                self.assertEqual(result["key_quota_reconciliation"]["comparison_counts"], {"missing_in_target": 1})

    def test_all_target_facts_compared_not_just_plan_recomputed(self):
        # Planner and reconciliation share map_key_limits only to check plan
        # integrity. A target mutation MUST fail while that source/plan is intact.
        for field, value in (("account_id", ACCOUNT + 1), ("status", 2), ("deleted_at", 123),
                ("expired_time", 1), ("unlimited", True), ("remaining_units", 0),
                ("used_units", 0), ("lifetime_limit_units", 70),
                ("secret_sha256", "0" * 64), ("restrictions", {})):
            with self.subTest(field=field):
                inventory = target_fixture(); inventory["records"][0][field] = value
                result = self.reconcile(inventory)
                self.assertFalse(result["key_reconciliation_passed"])
                row = result["key_quota_reconciliation"]["records"][0]
                self.assertEqual(row["comparison"], "field_mismatch")
                self.assertEqual(row["mismatched_fields"], [field])
                self.assertEqual(row["target_key_id"], 501)

    def test_every_restriction_compared(self):
        for field in target_fixture()["records"][0]["restrictions"]:
            inventory = target_fixture()
            inventory["records"][0]["restrictions"][field] = "corrupted"
            self.assertFalse(self.reconcile(inventory)["key_reconciliation_passed"], field)

    def test_literal_decimal_tag_cannot_impersonate_numeric_target_restriction(self):
        args = fixture([token(model_limits={"$decimal": "1.0"})])
        inventory = target_fixture()
        inventory["records"][0]["restrictions"]["model_limits"] = Decimal("1.0")
        # Display JSON matches, but the original typed facts do not.
        self.assertEqual(F.canonical({"$decimal": "1.0"}), F.canonical(Decimal("1.0")))
        result = R.build(F, **args, target_key_inventory=inventory)
        self.assertFalse(result["key_reconciliation_passed"])
        self.assertEqual(result["key_quota_reconciliation"]["records"][0]["mismatched_fields"], ["restrictions"])

    def test_unexpected_and_missing_keys_fail_together(self):
        inventory = target_fixture(); inventory["records"][0]["source_key_id"] = 99
        result = self.reconcile(inventory)
        self.assertFalse(result["key_reconciliation_passed"])
        self.assertEqual(result["key_quota_reconciliation"]["comparison_counts"],
                         {"missing_in_target": 1, "unexpected_in_target": 1})
        self.assertEqual(result["key_quota_reconciliation"]["records"][1]["target_key_id"], 501)

    def test_invalid_export_and_duplicate_bindings_refused(self):
        for field, value in (("status", "unexpected-state:SYNTHETIC-SECRET"), ("status", True),
                ("target_key_id", None), ("account_id", True), ("secret_sha256", "plaintext"),
                ("remaining_units", True), ("unlimited", 1)):
            with self.subTest(field=field, value=value):
                inventory = target_fixture(); inventory["records"][0][field] = value
                with self.assertRaises(F.MappingError):
                    self.reconcile(inventory)
        for field in ("source_key_id", "target_key_id", "secret_sha256"):
            inventory = target_fixture()
            other = copy.deepcopy(inventory["records"][0])
            other.update(source_key_id=18, target_key_id=502, secret_sha256="0" * 64)
            other[field] = inventory["records"][0][field]
            inventory["records"].append(other)
            with self.subTest(field=field), self.assertRaises(F.MappingError):
                self.reconcile(inventory)

    def test_bearer_metadata_in_other_projected_fields_refused(self):
        for field in ("deleted_at",):
            with self.assertRaisesRegex(F.MappingError, "reconciliation_key_secret_in_output"):
                R.build(F, **fixture([token(**{field: "SYNTHETIC-SECRET"})]))

    def test_empty_or_nonstring_bearers_and_unknown_states_refused(self):
        for secret in (None, "", "  ", 3, False):
            with self.subTest(secret=secret), self.assertRaisesRegex(F.MappingError, "key_bearer_missing"):
                fixture([token(key=secret)])
        for status in (None, True, "1", 0, 5, {"status": 1}):
            with self.subTest(status=status), self.assertRaisesRegex(F.MappingError, "key_status_unknown"):
                fixture([token(status=status)])


class SourceRightsCLITest(unittest.TestCase):
    """Exercise main with frozen synthetic files and an isolated backup root.

    Only the backup location is replaced, keeping all parsing, path permission,
    frozen-export, mapping and comparison checks live. This is developer testing,
    not an independent CLI/process or PostgreSQL import acceptance.
    """

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir=os.environ.get("LMM_FINANCIAL_TEST_ROOT"))
        self.addCleanup(self.temp.cleanup)
        backup_root = Path(self.temp.name).resolve()
        self.root = backup_root / "task"
        self.root.mkdir(mode=0o700)
        self.root.chmod(0o700)
        self.inputs = self.root / "inputs"
        self.inputs.mkdir(mode=0o700)
        self.backup = mock.patch.object(F.IDENTITY, "BACKUP_ROOT", backup_root)
        self.backup.start()
        self.addCleanup(self.backup.stop)
        self.tables = fixture()["tables"]
        self.write("archive", "synthetic frozen export fixture, not a real database archive\n")
        source_hash = F.IDENTITY.sha256_file(self.inputs / "archive")
        columns = [{"schema": "legacy", "table_name": table, "column_name": field}
            for table, rows in sorted(self.tables.items())
            for field in sorted(rows[0] if rows else {"id": None})]
        constraints = [{"schema": "legacy", "table_name": table, "contype": "p",
                        "definition": "PRIMARY KEY (id)"} for table in sorted(self.tables)]
        catalog = self.write("native-catalog-metadata.jsonl",
            "\n".join(F.canonical(row) for row in ({}, [], columns, constraints, [], [], {})) + "\n")
        counts = self.write("native-table-row-count-baseline.jsonl", "\n".join(
            F.canonical({"table": table, "row_count": len(rows)})
            for table, rows in sorted(self.tables.items())) + "\n")
        identity = {"database": "synthetic", "role": "synthetic_reader", "port": 55439,
            "tcp_connection": False, "database_oid": 123, "server_version_num": 180000,
            "postmaster_started_at": "2026-10-17T01:00:00+00:00", "read_only": "on"}
        self.write("source.json", {"status": "snapshot-restored-baseline-verified-frozen-and-classified",
            "archive": {"path": str(self.inputs / "archive"), "sha256": source_hash},
            "started_utc": "20261017T010000Z", "source": {"schema": "legacy"},
            "private_metadata_files": [str(catalog), str(counts)],
            "readonly_reader": {"database": "synthetic", "role": "synthetic_reader", "port": 55439},
            "source_instance_identity": {**identity, "listen_addresses": "", "schema_owner": "synthetic_owner"}})
        export = [{"kind": "identity", "row": identity},
                  {"kind": "schema", "row": {"schema": "legacy", "schema_owner": "synthetic_owner"}}]
        export.extend({"kind": "row", "table": table, "row": row}
                      for table, rows in sorted(self.tables.items()) for row in rows)
        raw = self.write("source-financial-export.private.jsonl", "\n".join(map(F.canonical, export)) + "\n")
        self.write("proof.json", {"source_archive_sha256": source_hash,
            "source_export_sha256": F.IDENTITY.sha256_file(raw),
            "metadata_hashes": {p.name: F.IDENTITY.sha256_file(p) for p in (catalog, counts)},
            "selected_table_counts": {table: len(rows) for table, rows in self.tables.items()},
            "source_column_coverage_exact": True, "source_row_baseline_exact": True, "source_instance": identity})
        mapping = self.write("map.jsonl", F.canonical({"source_user_id": UID,
            "target_user_id": UID, "target_personal_account_id": ACCOUNT}) + "\n")
        self.write("identity.json", {"format_version": 2, "scope": "identity-only",
            "source_archive_sha256": source_hash, "source_target_id_map_sha256": F.IDENTITY.sha256_file(mapping),
            "mapping_sha256": "b" * 64})

    def write(self, name, value):
        path = self.inputs / name
        path.write_text(value if isinstance(value, str) else F.canonical(value) + "\n", encoding="utf-8")
        path.chmod(0o600)
        return path

    def reseal_source_rows(self):
        raw_path = self.inputs / "source-financial-export.private.jsonl"
        framing = [F.parse_json(line) for line in raw_path.read_text().splitlines()][:2]
        framing.extend({"kind": "row", "table": table, "row": row}
            for table, rows in sorted(self.tables.items()) for row in rows)
        self.write(raw_path.name, "\n".join(map(F.canonical, framing)) + "\n")
        catalog_path = self.inputs / "native-catalog-metadata.jsonl"
        pieces = [F.parse_json(line) for line in catalog_path.read_text().splitlines()]
        pieces[2] = [{"schema": "legacy", "table_name": table, "column_name": field}
            for table, rows in sorted(self.tables.items())
            for field in sorted(rows[0] if rows else {"id": None})]
        self.write(catalog_path.name, "\n".join(map(F.canonical, pieces)) + "\n")
        proof = F.parse_json((self.inputs / "proof.json").read_text())
        proof["source_export_sha256"] = F.IDENTITY.sha256_file(raw_path)
        proof["metadata_hashes"][catalog_path.name] = F.IDENTITY.sha256_file(catalog_path)
        self.write("proof.json", proof)

    def invoke(self, *, inventory=None, preview=True, proof=None, extra=()):
        output = self.root / ("output-" + str(len(list(self.root.glob("output-*")))))
        argv = ["migrate-legacy-financial.py", "--source-receipt", str(self.inputs / "source.json"),
            "--identity-map", str(self.inputs / "map.jsonl"), "--identity-manifest", str(self.inputs / "identity.json"),
            "--reuse-export-proof", str(proof or self.inputs / "proof.json"), "--migration-id", "key-cli-test", "--output", str(output)]
        if preview:
            argv.append("--source-rights-preview")
        if inventory is not None:
            argv.extend(("--target-key-inventory", str(inventory)))
        stdout, stderr = io.StringIO(), io.StringIO()
        with mock.patch.object(sys, "argv", [*argv, *extra]), redirect_stdout(stdout), redirect_stderr(stderr):
            code = F.main()
        return code, output, stdout.getvalue(), stderr.getvalue()

    def test_exact_inventory_bytes_and_both_hashes_are_bound(self):
        original = json.dumps(target_fixture(), indent=2, ensure_ascii=False) + "\r\n\n"
        inventory = self.inputs / "target.json"
        inventory.write_bytes(original.encode("utf-8")); inventory.chmod(0o600)
        code, output, stdout, stderr = self.invoke(inventory=inventory)
        self.assertEqual((code, stderr), (0, ""))
        self.assertEqual((output / "target-key-inventory.private.json").read_bytes(), inventory.read_bytes())
        manifest = F.parse_json((output / "manifest.private.json").read_text())
        self.assertEqual(manifest["target_key_inventory_file_sha256"], F.IDENTITY.sha256_file(inventory))
        self.assertEqual(manifest["target_key_inventory_typed_sha256"], F.digest(target_fixture()))
        self.assertFalse(manifest["target_key_inventory_provenance_verified"])
        self.assertFalse(manifest["full_source_manifest_created"])
        self.assertFalse(manifest["raw_source_rows_copied"])
        self.assertTrue(manifest["original_frozen_source_export_retained"])
        self.assertFalse((output / "financial-full-source-manifest.private.json").exists())
        public = F.parse_json(stdout)
        self.assertTrue(public["key_reconciliation_passed"])
        self.assertEqual(public["status"], "blocked-for-opening-economic-review")
        self.assertEqual(public["target_writes"], 0)
        self.assertNotIn("SYNTHETIC-SECRET", stdout + stderr)
        for path in output.iterdir():
            self.assertNotIn(b"SYNTHETIC-SECRET", path.read_bytes(), path.name)

    def test_missing_extra_and_wrong_target_fail_with_complete_report(self):
        for kind in ("missing", "extra", "owner", "restriction", "unknown_restriction"):
            with self.subTest(kind=kind):
                inventory = target_fixture()
                if kind == "missing":
                    inventory["records"] = []
                elif kind == "extra":
                    inventory["records"][0]["source_key_id"] = 99
                elif kind == "owner":
                    inventory["records"][0]["account_id"] += 1
                else:
                    field = "model_limits_enabled" if kind == "restriction" else "extra"
                    inventory["records"][0]["restrictions"][field] = "corrupted"
                code, output, stdout, stderr = self.invoke(inventory=self.write("target.json", inventory))
                self.assertEqual((code, stderr), (1, ""))
                self.assertEqual(F.parse_json(stdout)["status"], "failed-key-reconciliation")
                self.assertFalse(F.parse_json(stdout)["key_reconciliation_passed"])
                self.assertTrue((output / "target-key-inventory.private.json").is_file())
                self.assertTrue((output / "manifest.private.json").is_file())
                self.assertFalse((output / "failure.private.json").exists())

    def test_source_only_preview_keeps_success_exit_but_not_key_signoff(self):
        code, output, stdout, stderr = self.invoke()
        self.assertEqual((code, stderr), (0, ""))
        public = F.parse_json(stdout)
        self.assertFalse(public["key_reconciliation_passed"])
        self.assertEqual(public["status"], "failed-key-reconciliation")
        self.assertFalse((output / "target-key-inventory.private.json").exists())
        self.assertFalse((output / "financial-full-source-manifest.private.json").exists())
        for path in output.iterdir():
            self.assertNotIn(b"SYNTHETIC-SECRET", path.read_bytes(), path.name)

    def test_ordinary_planner_preserves_existing_private_full_source_contract(self):
        code, output, _, stderr = self.invoke(preview=False)
        self.assertEqual((code, stderr), (0, ""))
        self.assertTrue((output / "financial-full-source-manifest.private.json").is_file())
        manifest = F.parse_json((output / "manifest.private.json").read_text())
        self.assertIn("full_source_manifest_file_sha256", manifest)

    def test_inventory_disallowed_outside_preview_or_with_opening_inputs(self):
        inventory = self.write("target.json", target_fixture())
        for preview, extra, error in ((False, (), "target_key_inventory_requires_source_rights_preview"),
                (True, ("--opening-preview",), "source_rights_preview_is_offline_only"),
                (True, ("--target-receipt", "/unused"), "source_rights_preview_is_offline_only")):
            code, output, _, stderr = self.invoke(inventory=inventory, preview=preview, extra=extra)
            self.assertEqual(code, 1)
            self.assertIn(error, stderr)
            self.assertFalse(output.exists())

    def test_malformed_duplicate_properties_and_unknown_fields_refused_before_copy(self):
        cases = ("{", '{"format_version":1,"format_version":1,"scope":"legacy-api-keys","records":[]}',
            '{"format_version":NaN,"scope":"legacy-api-keys","records":[]}',
            F.canonical({**target_fixture(), "extra": True}))
        for value in cases:
            code, output, stdout, stderr = self.invoke(inventory=self.write("target.json", value))
            self.assertEqual(code, 1)
            self.assertEqual(stdout, "")
            self.assertTrue(stderr)
            self.assertFalse((output / "target-key-inventory.private.json").exists())
            self.assertFalse((output / "manifest.private.json").exists())

    def test_symlink_and_open_permissions_refused(self):
        inventory = self.write("target.json", target_fixture())
        link = self.inputs / "link.json"; link.symlink_to(inventory)
        code, output, _, _ = self.invoke(inventory=link)
        self.assertEqual(code, 1)
        self.assertFalse((output / "target-key-inventory.private.json").exists())
        inventory.chmod(0o644)
        code, output, _, _ = self.invoke(inventory=inventory)
        self.assertEqual(code, 1)
        self.assertFalse((output / "target-key-inventory.private.json").exists())

    def test_known_bearers_hidden_in_target_metadata_are_never_retained(self):
        for field in ("deleted_at", "restrictions"):
            inventory = target_fixture()
            inventory["records"][0][field] = "SYNTHETIC-SECRET" if field == "deleted_at" else {"extra": "SYNTHETIC-SECRET"}
            code, output, stdout, stderr = self.invoke(inventory=self.write("target.json", inventory))
            self.assertEqual(code, 1)
            self.assertNotIn("SYNTHETIC-SECRET", stdout + stderr)
            self.assertIn("reconciliation_key_secret_in_output", stderr)
            self.assertFalse((output / "target-key-inventory.private.json").exists())
            self.assertFalse((output / "manifest.private.json").exists())

    def assert_secret_rejection_without_derived_files(self, invocation):
        code, output, stdout, stderr = invocation
        self.assertEqual(code, 1)
        self.assertEqual(stdout, "")
        self.assertIn("reconciliation_key_secret_in_output", stderr)
        self.assertNotIn("SYNTHETIC-SECRET", stderr)
        self.assertEqual({path.name for path in output.iterdir()}, {"failure.private.json"})
        self.assertNotIn(b"SYNTHETIC-SECRET", (output / "failure.private.json").read_bytes())

    def test_source_restrictions_and_deleted_state_refuse_before_any_derived_write(self):
        for field in ("model_limits", "allow_ips", "deleted_at"):
            with self.subTest(field=field):
                self.tables["tokens"][0][field] = "SYNTHETIC-SECRET"
                self.reseal_source_rows()
                before = {p: p.read_bytes() for p in self.inputs.iterdir() if p.is_file()}
                self.assert_secret_rejection_without_derived_files(self.invoke())
                self.assertTrue(all(p.read_bytes() == raw for p, raw in before.items()))
                del self.tables["tokens"][0][field]

    def test_invalid_target_cannot_publish_a_source_plan_with_bearer_metadata(self):
        self.tables["tokens"][0]["model_limits"] = "SYNTHETIC-SECRET"
        self.reseal_source_rows()
        inventory = target_fixture(); inventory["records"][0]["status"] = "invalid"
        self.assert_secret_rejection_without_derived_files(self.invoke(inventory=self.write("target.json", inventory)))

    def test_frozen_proof_metadata_and_raw_export_path_are_checked_before_copy(self):
        proof = F.parse_json((self.inputs / "proof.json").read_text())
        proof["additional_note"] = "SYNTHETIC-SECRET"
        self.write("proof.json", proof)
        self.assert_secret_rejection_without_derived_files(self.invoke())
        del proof["additional_note"]
        nested = self.inputs / "SYNTHETIC-SECRET"
        nested.mkdir(mode=0o700)
        nested_proof = nested / "proof.json"
        nested_proof.write_text(F.canonical(proof) + "\n"); nested_proof.chmod(0o600)
        nested_raw = nested / "source-financial-export.private.jsonl"
        nested_raw.write_bytes((self.inputs / nested_raw.name).read_bytes()); nested_raw.chmod(0o600)
        self.assert_secret_rejection_without_derived_files(self.invoke(proof=nested_proof))

    def test_publish_io_failure_keeps_only_prechecked_partial_files_and_failure(self):
        writer = F.IDENTITY.write_private
        def fail_plan(directory, name, value):
            if name == "financial-plan.private.json":
                raise OSError("synthetic publish fault")
            return writer(directory, name, value)
        with mock.patch.object(F.IDENTITY, "write_private", side_effect=fail_plan):
            code, output, stdout, stderr = self.invoke()
        self.assertEqual(code, 1)
        self.assertEqual(stdout, "")
        self.assertIn('"error_code":"OSError"', stderr)
        self.assertEqual({p.name for p in output.iterdir()}, {"source-export-proof.private.json", "failure.private.json"})
        for path in output.iterdir():
            self.assertNotIn(b"SYNTHETIC-SECRET", path.read_bytes(), path.name)


if __name__ == "__main__":
    unittest.main()
