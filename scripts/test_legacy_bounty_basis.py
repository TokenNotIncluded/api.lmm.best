"""Developer tests for the offline bounty basis gate; synthetic data only."""

import copy
import importlib.util
from pathlib import Path
import unittest

ROOT = Path(__file__).parent


def load(name, file):
    spec = importlib.util.spec_from_file_location(name, ROOT / file)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


F = load("bounty_financial", "migrate-legacy-financial.py")
B = load("bounty_basis", "legacy_bounty_basis.py")


def seal(parent):
    parent.pop("plan_sha256", None)
    parent.pop("business_plan_sha256", None)
    parent["business_plan_sha256"] = B.legacy_digest(
        {
            k: v
            for k, v in parent.items()
            if k
            not in ("target", "source_sha256", "plan_sha256", "business_plan_sha256")
        }
    )
    parent["plan_sha256"] = B.legacy_digest(parent)


def make_source(table, rid, **values):
    spec = B.SPECS[table]
    return {
        "id": rid,
        **dict.fromkeys(spec["int"], 0),
        **dict.fromkeys(spec["text"], ""),
        **values,
    }


def fixture():
    project = make_source(
        B.BOUNTY_TABLES[0],
        1,
        owner_user_id=1,
        status="published",
        escrow_quota=1000,
        reward_quota=200,
        net_reward_quota=100,
        reward_slots=2,
        created_at=1,
    )
    active = make_source(
        B.BOUNTY_TABLES[1],
        2,
        project_id=1,
        participant_user_id=2,
        status="submitted",
        reward_quota=100,
        created_at=2,
    )
    guard = make_source(
        B.BOUNTY_TABLES[1],
        3,
        project_id=1,
        participant_user_id=2,
        status="rejected",
        reward_quota=100,
        rejected_at=1,
        created_at=2,
    )
    snapshot = {
        "snapshot_at": 1000000,
        "entities": {
            "bounty_projects": [project],
            "bounty_challenges": [active, guard],
            "bounty_disputes": [],
        },
    }
    parent = {
        "version": 1,
        "kind": "offline_credit_balance_rebase_preview",
        "migration_id": "old-rebase",
        "include_bounties": True,
        "include_other_rights": True,
        "user_ids": [1, 2],
        "divisor": "10",
        "rounding": "half-away-from-zero",
        "exact_factor": {"numerator": 1, "denominator": 10},
        "usd_credit_conversion": 500000,
        "snapshot_at": 1000000,
        "entity_updates": B.make_entities(
            snapshot, {1, 2}, lambda n: (n + 5) // 10, include_bounties=True
        ),
        "other_credit_bases": [],
    }
    seal(parent)
    tables = {table: [] for table in B.BOUNTY_TABLES}
    for e in parent["entity_updates"]:
        tables[e["table"]].append(
            {**e["source"], **{k: v["after_credit"] for k, v in e["updates"].items()}}
        )
    return parent, tables


def args(parent, tables):
    raw = {
        "migration_id": parent["migration_id"],
        "plan_sha256": parent["plan_sha256"],
        "plan": copy.deepcopy(parent),
    }
    tables = {**tables, "wallet_credit_rebases": [raw]}
    plan = {
        "digest_format": F.DIGEST_FORMAT,
        "mapping_digest_domain": F.MAPPING_DIGEST_DOMAIN,
        "source_evidence": {},
        "refund_lineage": {
            "wallet_rebase_audits": [
                {
                    "source_migration_id": parent["migration_id"],
                    "declared_plan_sha256": parent["plan_sha256"],
                    "source_row_sha256": F.digest(raw),
                    "full_plan_unknown_fields_preserved": parent,
                }
            ]
        },
    }
    for table, rows in tables.items():
        plan["source_evidence"][table] = {
            "row_count": len(rows),
            "row_hashes": [F.digest(row) for row in rows],
            "primary_key_columns": ["id"],
            "source_records": [
                {
                    "source_primary_key": {"id": row["id"]},
                    "source_export_ordinal": n,
                    "source_row_sha256": F.digest(row),
                }
                for n, row in enumerate(rows)
            ]
            if table in B.BOUNTY_TABLES
            else [],
        }
    plan["mapping_sha256"] = F.digest(plan)
    plan["public_summary"] = {"mapping_sha256": plan["mapping_sha256"]}
    return dict(tables=tables, identities={1: 101, 2: 102}, plan=plan)


# @mark migration.bounty-basis role=test
class BountyBasisTest(unittest.TestCase):
    def test_after_matches_without_money_authority(self):
        result = B.build(F, **args(*fixture()))
        self.assertEqual(
            result["lineage_status"], "matches-parent-after-with-unverified-authority"
        )
        self.assertEqual(
            result["public_summary"]["row_state_counts"],
            {"after": 2, "before_and_after": 1},
        )
        self.assertFalse(result["economic_approval_verified"])
        self.assertFalse(result["import_payload_created"])
        self.assertEqual(result["target_writes"], 0)

    def test_before_mixed_and_changed_source_block(self):
        for mode in (
            "before",
            "row-mixed",
            "cross-row-mixed",
            "postactivity",
            "missing",
        ):
            with self.subTest(mode=mode):
                parent, tables = fixture()
                project = tables[B.BOUNTY_TABLES[0]][0]
                if mode == "before":
                    for e in parent["entity_updates"]:
                        tables[e["table"]] = [
                            copy.deepcopy(e["source"]) if r["id"] == e["id"] else r
                            for r in tables[e["table"]]
                        ]
                elif mode == "row-mixed":
                    project["escrow_quota"] = 1000
                elif mode == "cross-row-mixed":
                    tables[B.BOUNTY_TABLES[1]][0]["reward_quota"] = 100
                elif mode == "postactivity":
                    project["updated_at"] = 9
                else:
                    tables[B.BOUNTY_TABLES[0]] = []
                result = B.build(F, **args(parent, tables))
                self.assertEqual(
                    result["lineage_status"], "not-proven-exact-parent-after"
                )

    def test_invalid_parent_math_and_projection_rejected(self):
        for mode in (
            "after",
            "before",
            "extra-write",
            "extra-source",
            "missing-guard",
            "guard-status",
            "duplicate",
            "ratio",
            "bool-factor",
            "rounding",
            "scope",
        ):
            with self.subTest(mode=mode):
                parent, tables = fixture()
                entry = next(
                    e
                    for e in parent["entity_updates"]
                    if e["table"] == B.BOUNTY_TABLES[0]
                )
                guard = next(
                    e
                    for e in parent["entity_updates"]
                    if e.get("rights_status") == "historical_rejection_guard_only"
                )
                if mode == "after":
                    entry["updates"]["escrow_quota"]["after_credit"] += 1
                elif mode == "before":
                    entry["updates"]["escrow_quota"]["before_credit"] += 1
                elif mode == "extra-write":
                    entry["updates"]["platform_fee_quota"] = {
                        "before_credit": 0,
                        "after_credit": 0,
                    }
                elif mode == "extra-source":
                    entry["source"]["extra"] = 1
                elif mode == "missing-guard":
                    parent["entity_updates"].remove(guard)
                elif mode == "guard-status":
                    guard["rights_status"] = "active_future_reward"
                elif mode == "duplicate":
                    parent["entity_updates"].append(copy.deepcopy(entry))
                elif mode == "ratio":
                    parent["exact_factor"] = {"numerator": 10, "denominator": 1}
                elif mode == "bool-factor":
                    parent["exact_factor"]["numerator"] = True
                elif mode == "rounding":
                    parent["rounding"] = "nearest"
                else:
                    parent["include_other_rights"] = False
                seal(parent)
                if mode == "missing-guard":
                    result = B.build(F, **args(parent, tables))
                    self.assertIn(
                        "outside_parent_scope",
                        result["public_summary"]["row_state_counts"],
                    )
                else:
                    with self.assertRaises(F.MappingError):
                        B.build(F, **args(parent, tables))

    def test_unknown_extra_fields_stay_hash_bound_not_projected(self):
        parent, tables = fixture()
        tables[B.BOUNTY_TABLES[0]][0]["private_description"] = "SYNTHETIC-SECRET"
        result = B.build(F, **args(parent, tables))
        self.assertEqual(
            result["lineage_status"], "matches-parent-after-with-unverified-authority"
        )
        self.assertNotIn("SYNTHETIC-SECRET", F.canonical(result))

    def test_fixed_point_only_never_proves_after(self):
        parent, tables = fixture()
        for e in parent["entity_updates"]:
            for key in e["updates"]:
                e["source"][key] = 0
                e["updates"][key] = {"before_credit": 0, "after_credit": 0}
            tables[e["table"]] = [
                copy.deepcopy(e["source"]) if r["id"] == e["id"] else r
                for r in tables[e["table"]]
            ]
        seal(parent)
        result = B.build(F, **args(parent, tables))
        self.assertEqual(result["lineage_status"], "not-proven-exact-parent-after")
        self.assertIn("bounty_no_after_only_conversion_witness", result["blockers"])

    def test_whole_source_duplicate_id_rejected(self):
        parent, tables = fixture()
        tables[B.BOUNTY_TABLES[0]].append(copy.deepcopy(tables[B.BOUNTY_TABLES[0]][0]))
        with self.assertRaisesRegex(F.MappingError, "current_id_duplicate"):
            B.build(F, **args(parent, tables))

    def test_raw_parent_cannot_be_replaced_by_resealed_mapping(self):
        data = args(*fixture())
        audit = data["plan"]["refund_lineage"]["wallet_rebase_audits"][0]
        audit["full_plan_unknown_fields_preserved"] = copy.deepcopy(
            audit["full_plan_unknown_fields_preserved"]
        )
        audit["full_plan_unknown_fields_preserved"]["divisor"] = "20"
        seal(audit["full_plan_unknown_fields_preserved"])
        audit["declared_plan_sha256"] = audit["full_plan_unknown_fields_preserved"][
            "plan_sha256"
        ]
        plan = data["plan"]
        plan.pop("mapping_sha256")
        plan.pop("public_summary")
        plan["mapping_sha256"] = F.digest(plan)
        plan["public_summary"] = {"mapping_sha256": plan["mapping_sha256"]}
        with self.assertRaisesRegex(F.MappingError, "raw_parent_binding_mismatch"):
            B.build(F, **data)

    def test_dispute_basis_required_and_immutable(self):
        for mode in ("valid", "missing", "changed", "duplicate"):
            with self.subTest(mode=mode):
                parent, tables = fixture()
                source = make_source(
                    B.BOUNTY_TABLES[2],
                    4,
                    project_id=1,
                    challenge_id=2,
                    opened_by_user_id=2,
                    against_user_id=1,
                    status="open",
                    reward_quota_snapshot=100,
                    project_escrow_quota_snapshot=1000,
                    challenge_status_snapshot="submitted",
                    created_at=3,
                )
                sources = {
                    "bounty_projects": [],
                    "bounty_challenges": [],
                    "bounty_disputes": [source],
                }
                for e in parent["entity_updates"]:
                    sources[
                        "bounty_projects"
                        if e["table"] == B.BOUNTY_TABLES[0]
                        else "bounty_challenges"
                    ].append(e["source"])
                snapshot = {"snapshot_at": parent["snapshot_at"], "entities": sources}
                parent["entity_updates"] = B.make_entities(
                    snapshot, {1, 2}, lambda n: (n + 5) // 10, include_bounties=True
                )
                bases = B.prepare_disputes(
                    snapshot, {1, 2}, lambda n: (n + 5) // 10, include=True
                )
                parent["other_credit_bases"] = bases
                if mode == "missing":
                    parent["other_credit_bases"] = []
                elif mode == "changed":
                    bases[0]["source"]["reward_quota_snapshot"] += 1
                elif mode == "duplicate":
                    bases.append(copy.deepcopy(bases[0]))
                tables[B.BOUNTY_TABLES[2]] = [source]
                seal(parent)
                if mode == "valid":
                    result = B.build(F, **args(parent, tables))
                    self.assertEqual(
                        result["lineage_status"],
                        "matches-parent-after-with-unverified-authority",
                    )
                    self.assertFalse(result["dispute_snapshots_rescaled"])
                else:
                    with self.assertRaises(F.MappingError):
                        B.build(F, **args(parent, tables))

    def test_display_decimal_encoding_is_not_typed_equality(self):
        from decimal import Decimal

        self.assertFalse(B.typed_equal(F, Decimal("1.0"), {"$decimal": "1.0"}))

    def test_missing_tables_or_parent_never_imply_clear(self):
        parent, tables = fixture()
        del tables[B.BOUNTY_TABLES[2]]
        result = B.build(F, **args(parent, tables))
        self.assertIn("bounty_source_table_coverage_incomplete", result["blockers"])


if __name__ == "__main__":
    unittest.main()
