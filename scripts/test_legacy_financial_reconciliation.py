#!/usr/bin/env python3
"""Developer tests for the source-only reconciliation projection."""
import copy
import importlib.util
from pathlib import Path
import subprocess
import unittest


ROOT = Path(__file__).parent
def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module
F = load("financial_for_rights", ROOT / "migrate-legacy-financial.py")
R = load("rights", ROOT / "legacy_financial_reconciliation.py")


# @mark migration.financial-rights role=test
class ReconciliationTest(unittest.TestCase):
    def fixture(self, extra=None):
        tables = {"users": [{"id": 1, "password": "must-not-copy", "quota": -7, "aff_quota": 4, "used_quota": 19, "request_count": 3, "aff_history": 11}]}
        tables.update(extra or {})
        identities = {1: 101}
        plan = {"source_evidence": {}, "wallet_positions": [{"source_user_id": 1, "account_id": 101,
            "source_row_sha256": F.digest(tables["users"][0]), "signed_available_units": -7,
            "signed_affiliate_units": 4, "historical_used_units": 19,
            "historical_request_count": 3, "historical_affiliate_units": 11}]}
        for table, rows in tables.items():
            plan["source_evidence"][table] = {"row_count": len(rows), "row_hashes": [F.digest(row) for row in rows],
                "primary_key_columns": ["id"], "source_column_schema": [{"column_name": key} for key in sorted(rows[0])] if rows else [],
                "source_records": [{"source_primary_key": {"id": row["id"]}, "source_export_ordinal": n,
                    "source_row_sha256": F.digest(row)} for n, row in enumerate(rows)]}
        plan.update(digest_format=F.DIGEST_FORMAT, mapping_digest_domain=F.MAPPING_DIGEST_DOMAIN)
        plan["mapping_sha256"] = F.digest(plan)
        plan["public_summary"] = {"mapping_sha256": plan["mapping_sha256"]}
        proof = {"selected_table_counts": {table: len(rows) for table, rows in tables.items()},
                 "source_queries_this_run": 0, "source_archive_sha256": "a"*64, "source_export_sha256": "b"*64}
        return dict(tables=tables, identities=identities, plan=plan, proof=proof,
            identity_map_file_sha256="c"*64, identity_manifest_file_sha256="d"*64, tool_hashes={"test": "e"*64})

    def build(self, args):
        return R.build(F, **args)

    def test_signed_debt_identity_and_affiliate_stay_independent(self):
        result = self.build(self.fixture())
        self.assertEqual(result["wallet_integer_totals"], {"signed_available_units": -7,
            "wallet_units": 0, "debt_units_signed": -7, "signed_affiliate_units": 4})
        self.assertEqual(result["wallet_positions"][0]["historical_affiliate_units"], 11)

    def test_no_reservation_or_approval_inferred(self):
        result = self.build(self.fixture())
        self.assertIsNone(result["wallet_positions"][0]["reserved_units"])
        self.assertFalse(result["opening_payloads_created"])
        self.assertFalse(result["approval_created"])
        self.assertFalse(result["public_summary"]["reserved_zero_proven"])
        self.assertEqual(result["target_queries"], 0)
        self.assertEqual(result["target_writes"], 0)

    def test_selected_export_never_claims_full_database_rights_clear(self):
        result = self.build(self.fixture())
        for item in (result, result["public_summary"]):
            self.assertIs(item["selected_export_is_not_full_database"], True)
            self.assertIs(item["full_database_rights_cleared"], False)

    def test_complete_table_coverage_required(self):
        args = self.fixture(); args["proof"]["selected_table_counts"]["unknown_wallet"] = 0
        with self.assertRaisesRegex(F.MappingError, "table_coverage_mismatch"):
            self.build(args)

    def test_row_tamper_rejected(self):
        args = self.fixture(); args["tables"]["users"][0]["quota"] = -8
        with self.assertRaisesRegex(F.MappingError, "row_hash_mismatch"):
            self.build(args)

    def test_boolean_count_rejected(self):
        args = self.fixture(); args["proof"]["selected_table_counts"]["users"] = True
        with self.assertRaisesRegex(F.MappingError, "table_count_mismatch"):
            self.build(args)

    def test_boolean_amount_rejected(self):
        args = self.fixture({"wallet_transfers": [{"id": 2, "quota": True, "sender_id": 1, "recipient_id": 0, "status": "pending"}]})
        with self.assertRaisesRegex(F.MappingError, "amount_not_integer"):
            self.build(args)

    def test_nullable_amount_is_unknown_and_never_coerced_zero(self):
        result = self.build(self.fixture({"user_subscriptions": [{"id": 2, "user_id": 1, "status": "active",
            "reset_amount": None, "renewal_amount": 7}]}))
        group = next(g for g in result["integer_amount_groups"] if g["field"] == "reset_amount")
        self.assertEqual(group["null_count"], 1)
        self.assertEqual(group["known_integer_count"], 0)
        self.assertFalse(group["integer_sum_is_complete"])
        self.assertIsNone(result["observed_rights_records"][0]["observed_integer_amounts"]["reset_amount"]["value"])

    def test_source_field_unit_remains_unproven(self):
        result = self.build(self.fixture({"referral_rewards": [{"id": 2, "quota": 100}],
            "referral_ledger_entries": [{"id": 3, "quota": 10}]}))
        self.assertTrue(all(g["unit"] == "source-field-unit-unproven" for g in result["integer_amount_groups"]))

    def test_unknown_reference_and_state_retained(self):
        result = self.build(self.fixture({"wallet_transfers": [{"id": 2, "quota": 9,
            "sender_id": 999, "recipient_id": 0, "status": "future-state"}]}))
        row = result["observed_rights_records"][0]
        self.assertEqual(row["user_references"]["sender_id"]["mapping_status"], "unmapped")
        self.assertEqual(row["observed_state"]["status"], "future-state")
        self.assertTrue(row["outstanding_right"].startswith("unknown"))

    def test_merchant_promotion_reference_is_not_false_order_orphan(self):
        result = self.build(self.fixture({"merchant_store_promotions": [{"id": "promotion1", "quota": 8, "seller_id": 1}],
            "merchant_store_transfers": [{"id": "transfer1", "order_id": "promotion1", "kind": "promotion",
                "quota": 8, "from_user_id": 1, "to_user_id": 0}]}))
        row = next(row for row in result["observed_rights_records"] if row["table"] == "merchant_store_transfers")
        self.assertEqual(row["row_references"]["order_id"]["matched_tables"], ["merchant_store_promotions"])
        self.assertEqual(row["user_references"]["to_user_id"]["mapping_status"], "zero-or-unassigned")

    def test_null_registration_topup_is_not_orphan(self):
        result = self.build(self.fixture({"referral_rewards": [{"id": 2, "invitee_id": 1, "inviter_id": 1,
            "top_up_id": None, "quota": 3, "status": "earned"}]}))
        self.assertEqual(result["observed_rights_records"][0]["row_references"]["top_up_id"]["status"], "zero-or-unassigned")

    def test_secret_columns_never_projected(self):
        result = self.build(self.fixture({"tokens": [{"id": 2, "key": "must-not-copy", "user_id": 1,
            "remain_quota": 5, "used_quota": 4, "status": 1, "unlimited_quota": False}]}))
        self.assertNotIn("must-not-copy", F.canonical(result))
        self.assertNotIn('"key":', F.canonical(result))

    def test_rehashed_plan_cannot_change_source_wallet_fact(self):
        args = self.fixture()
        args["plan"]["wallet_positions"][0]["signed_available_units"] = 0
        material = {k: v for k, v in args["plan"].items() if k not in ("mapping_sha256", "public_summary")}
        args["plan"]["mapping_sha256"] = F.digest(material)
        args["plan"]["public_summary"]["mapping_sha256"] = args["plan"]["mapping_sha256"]
        with self.assertRaisesRegex(F.MappingError, "wallet_source_mismatch"):
            self.build(args)

    def rehash(self, args):
        plan = args["plan"]
        plan["mapping_sha256"] = F.digest({k: v for k, v in plan.items() if k not in ("mapping_sha256", "public_summary")})
        plan["public_summary"]["mapping_sha256"] = plan["mapping_sha256"]

    def test_rehashed_source_record_tamper_rejected(self):
        for field, value in (("source_export_ordinal", False), ("source_primary_key", {"id": True}),
                             ("source_row_sha256", "0" * 64)):
            with self.subTest(field=field):
                args = self.fixture()
                args["plan"]["source_evidence"]["users"]["source_records"][0][field] = value
                self.rehash(args)
                with self.assertRaisesRegex(F.MappingError, "source_record_mismatch"):
                    self.build(args)

    def test_rehashed_boolean_plan_count_rejected(self):
        args = self.fixture()
        args["plan"]["source_evidence"]["users"]["row_count"] = True
        self.rehash(args)
        with self.assertRaisesRegex(F.MappingError, "table_count_mismatch"):
            self.build(args)

    def test_boolean_identity_mapping_rejected(self):
        args = self.fixture()
        args["identities"] = {True: 101}
        with self.assertRaisesRegex(F.MappingError, "identity_integer_invalid"):
            self.build(args)

    def test_rehashed_wallet_identity_types_rejected(self):
        for field, value in (("source_user_id", True), ("account_id", True)):
            with self.subTest(field=field):
                args = self.fixture()
                args["plan"]["wallet_positions"][0][field] = value
                self.rehash(args)
                with self.assertRaisesRegex(F.MappingError, "wallet_identity_integer_invalid"):
                    self.build(args)

    def test_float_wallet_identity_cannot_enter_typed_plan(self):
        for field, value in (("source_user_id", 1.0), ("account_id", 101.0)):
            with self.subTest(field=field):
                args = self.fixture()
                args["plan"]["wallet_positions"][0][field] = value
                with self.assertRaises(F.MappingError):
                    self.build(args)

    def test_deterministic_projection(self):
        args = self.fixture(); before = copy.deepcopy(args)
        self.assertEqual(F.digest(self.build(args)), F.digest(self.build(args)))
        self.assertEqual(args, before)

    def test_cli_rejects_any_target_or_opening_input_before_output(self):
        required = ["--source-receipt", "/nonexistent-source", "--identity-map", "/nonexistent-map",
            "--identity-manifest", "/nonexistent-manifest", "--output", "/nonexistent-output",
            "--migration-id", "source-rights-test", "--source-rights-preview"]
        for extra in ([], ["--reuse-export-proof", "/nonexistent-proof", "--target-receipt", "/target"],
                      ["--reuse-export-proof", "/nonexistent-proof", "--opening-preview"],
                      ["--reuse-export-proof", "/nonexistent-proof", "--opening-approval", "/approval"]):
            with self.subTest(extra=extra):
                run = subprocess.run(["python3", "-B", str(ROOT / "migrate-legacy-financial.py"), *required, *extra],
                                     text=True, capture_output=True)
                self.assertEqual(run.returncode, 1)
                self.assertIn("source_rights_preview_is_offline_only", run.stderr)


if __name__ == "__main__":
    unittest.main()
