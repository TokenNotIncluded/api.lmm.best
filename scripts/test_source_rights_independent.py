"""Independent synthetic economic boundary checks; no database or real records."""

from decimal import Decimal
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


F = load("independent_financial", ROOT / "migrate-legacy-financial.py")
R = load("independent_rights", ROOT / "legacy_financial_reconciliation.py")
UID = 9007199254740993
ACCOUNT = 9007199254741093


def scenario(extra=None):
    tables = {name: [] for name in F.REQUIRED_TABLES}
    tables["users"] = [
        {
            "id": UID,
            "quota": -17,
            "aff_quota": 23,
            "used_quota": 71,
            "request_count": 13,
            "aff_history": 29,
            "password": "SYNTHETIC-PRIVATE-CANARY",
        }
    ]
    tables.update(extra or {})
    plan = F.plan_financial(
        tables,
        {UID: ACCOUNT},
        source_sha256="a" * 64,
        primary_keys={name: ["id"] for name in tables},
        column_schema={name: [] for name in tables},
    )
    return dict(
        tables=tables,
        identities={UID: ACCOUNT},
        plan=plan,
        proof={
            "selected_table_counts": {name: len(rows) for name, rows in tables.items()},
            "source_queries_this_run": 0,
            "source_archive_sha256": "a" * 64,
            "source_export_sha256": "b" * 64,
        },
        identity_map_file_sha256="c" * 64,
        identity_manifest_file_sha256="d" * 64,
        tool_hashes={},
    )


def reseal(plan):
    plan.pop("mapping_sha256")
    plan.pop("public_summary")
    plan["mapping_sha256"] = F.digest(plan)
    plan["public_summary"] = {"mapping_sha256": plan["mapping_sha256"]}


# @mark migration.financial-rights role=test
class IndependentRightsBoundary(unittest.TestCase):
    def test_archived_nonzero_escrow_is_neither_wallet_nor_migrated_right(self):
        args = scenario(
            {
                "open_source_bounty_projects": [
                    {
                        "id": 11,
                        "owner_user_id": UID,
                        "status": "closed",
                        "archived_at": 123,
                        "escrow_quota": 1007,
                        "platform_fee_quota": 31,
                        "reward_quota": 1038,
                        "rules": "SYNTHETIC-PRIVATE-CANARY",
                    }
                ]
            }
        )
        result = R.build(F, **args)
        row = result["observed_rights_records"][0]
        self.assertEqual(row["observed_integer_amounts"]["escrow_quota"]["value"], 1007)
        self.assertEqual(
            row["observed_integer_amounts"]["platform_fee_quota"]["value"], 31
        )
        self.assertEqual(
            row["source_row_sha256"], F.digest(args["tables"][row["table"]][0])
        )
        self.assertTrue(row["outstanding_right"].startswith("unknown"))
        self.assertEqual(result["wallet_integer_totals"]["signed_available_units"], -17)
        self.assertIsNone(result["wallet_positions"][0]["reserved_units"])
        self.assertFalse(result["public_summary"]["reserved_zero_proven"])
        self.assertEqual(result["public_summary"]["financial_rows_migrated"], 0)
        self.assertNotIn("SYNTHETIC-PRIVATE-CANARY", json.dumps(result))

    def test_dispute_original_unit_and_rebase_stay_unprojected_not_erased(self):
        extra = {
            "open_source_bounty_disputes": [
                {
                    "id": 21,
                    "status": "open",
                    "reward_quota_snapshot": 9000,
                    "project_escrow_quota_snapshot": 18000,
                }
            ],
            "wallet_credit_rebases": [],
        }
        result = R.build(F, **scenario(extra))
        evidence = result["table_evidence"]["open_source_bounty_disputes"]
        self.assertEqual(evidence["row_count"], 1)
        self.assertEqual(
            evidence["detail_projection"], "not-projected-retained-in-raw-export"
        )
        self.assertEqual(
            evidence["source_rows_sha256"],
            F.digest(extra["open_source_bounty_disputes"]),
        )
        self.assertFalse(result["full_database_rights_cleared"])
        self.assertFalse(result["opening_payloads_created"])

    def test_paused_and_unknown_states_preserve_distinct_amount_groups(self):
        rows = [
            {"id": n, "owner_user_id": UID, "status": state, "escrow_quota": amount}
            for n, state, amount in [(1, "paused", 5), (2, "future-policy", 7)]
        ]
        result = R.build(F, **scenario({"open_source_bounty_projects": rows}))
        groups = [
            g for g in result["integer_amount_groups"] if g["field"] == "escrow_quota"
        ]
        self.assertEqual(
            {(g["state"]["status"], g["integer_sum"]) for g in groups},
            {("paused", 5), ("future-policy", 7)},
        )
        self.assertTrue(all(g["unit"] == "source-field-unit-unproven" for g in groups))

    def test_exact_large_identity_and_signed_debt_are_not_offset_by_affiliate(self):
        result = R.build(F, **scenario())
        wallet = result["wallet_positions"][0]
        self.assertEqual(
            (wallet["source_user_id"], wallet["target_account_id"]), (UID, ACCOUNT)
        )
        self.assertEqual(
            (
                wallet["wallet_units"],
                wallet["debt_units_signed"],
                wallet["signed_affiliate_units"],
            ),
            (0, -17, 23),
        )

    def test_bool_decimal_string_escrow_rejected_even_with_matching_source_hash(self):
        for value in (True, Decimal("1.0"), "1"):
            with self.subTest(type=type(value).__name__):
                with self.assertRaises(F.MappingError):
                    args = scenario(
                        {
                            "open_source_bounty_projects": [
                                {"id": 1, "status": "published", "escrow_quota": value}
                            ]
                        }
                    )
                    R.build(F, **args)

    def test_rehashed_wallet_id_type_alias_does_not_bypass_binding(self):
        for field in ("source_user_id", "account_id"):
            args = scenario()
            # Decimal preserves a legal JSON numeric type, rather than testing hash rejection.
            args["plan"]["wallet_positions"][0][field] = Decimal(
                args["plan"]["wallet_positions"][0][field]
            )
            reseal(args["plan"])
            with self.subTest(field=field), self.assertRaises(F.MappingError):
                R.build(F, **args)

    def test_rehashed_wrong_source_ordinal_is_rejected(self):
        args = scenario()
        args["plan"]["source_evidence"]["users"]["source_records"][0][
            "source_export_ordinal"
        ] = 1
        reseal(args["plan"])
        with self.assertRaises(F.MappingError):
            R.build(F, **args)

    def test_proof_boolean_count_is_rejected(self):
        args = scenario()
        args["proof"]["selected_table_counts"]["users"] = True
        with self.assertRaises(F.MappingError):
            R.build(F, **args)

    def test_forbidden_cli_modes_refuse_before_output_or_input_access(self):
        with tempfile.TemporaryDirectory(dir=os.environ.get("TMPDIR")) as tmp:
            base = [
                sys.executable,
                "-B",
                str(ROOT / "migrate-legacy-financial.py"),
                "--source-receipt",
                "/MUST-NOT-READ",
                "--identity-map",
                "/MUST-NOT-READ",
                "--identity-manifest",
                "/MUST-NOT-READ",
                "--migration-id",
                "independent-rights",
                "--source-rights-preview",
            ]
            flags = [
                ("--apply",),
                ("--opening-preview",),
                ("--apply-opening",),
                ("--reconcile-opening",),
                ("--target-receipt", "/MUST-NOT-READ"),
                ("--reservation-evidence", "/MUST-NOT-READ"),
                ("--reservation-review", "/MUST-NOT-READ"),
                ("--opening-approval", "/MUST-NOT-READ"),
                ("--opening-attempt", "/MUST-NOT-READ"),
                (),
            ]
            for index, flag in enumerate(flags):
                output = Path(tmp) / str(index)
                command = base + ["--output", str(output)]
                if flag:
                    command += ["--reuse-export-proof", "/MUST-NOT-READ", *flag]
                child = subprocess.run(
                    command, capture_output=True, text=True, timeout=20
                )
                with self.subTest(flag=flag):
                    self.assertEqual(child.returncode, 1)
                    self.assertFalse(output.exists())
                    self.assertEqual(child.stdout, "")
                    error = json.loads(child.stderr)
                    self.assertEqual(
                        error["error_code"],
                        "financial_apply_contract_not_frozen"
                        if flag == ("--apply",)
                        else "source_rights_preview_is_offline_only",
                    )


if __name__ == "__main__":
    unittest.main()
