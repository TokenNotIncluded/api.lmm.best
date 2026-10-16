"""Independent synthetic bounty lineage acceptance; never uses a database."""

import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parent


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


F = load("independent_bounty_financial", "migrate-legacy-financial.py")
B = load("independent_bounty_basis", "legacy_bounty_basis.py")
PROJECT = "open_source_bounty_projects"
CHALLENGE = "open_source_bounty_challenges"
DISPUTE = "open_source_bounty_disputes"


def historical_hash(value):
    return hashlib.sha256(
        json.dumps(value, sort_keys=True, separators=(",", ":")).encode()
    ).hexdigest()


def seal_parent(parent):
    parent.pop("plan_sha256", None)
    parent.pop("business_plan_sha256", None)
    business = {k: v for k, v in parent.items() if k not in ("target", "source_sha256")}
    parent["business_plan_sha256"] = historical_hash(business)
    parent["plan_sha256"] = historical_hash(parent)
    return parent


def seal_mapping(plan):
    plan.pop("mapping_sha256")
    plan.pop("public_summary")
    plan["mapping_sha256"] = F.digest(plan)
    plan["public_summary"] = {"mapping_sha256": plan["mapping_sha256"]}


def parent_fixture():
    project = dict(
        id=10,
        owner_user_id=1,
        escrow_quota=100,
        reward_quota=25,
        net_reward_quota=25,
        reward_slots=4,
        platform_fee_quota=9,
        platform_fee_rate_bps=200,
        created_at=1,
        updated_at=2,
        published_at=2,
        closed_at=0,
        archived_at=0,
        status="published",
    )
    active = dict(
        id=20,
        project_id=10,
        participant_user_id=2,
        reward_quota=25,
        tip_quota=7,
        accepted_at=3,
        submitted_at=0,
        reviewed_at=0,
        rejected_at=0,
        paid_at=0,
        created_at=3,
        updated_at=3,
        status="accepted",
    )
    guard = {**active, "id": 21, "status": "rejected", "rejected_at": 4}
    entries = [
        {
            "table": PROJECT,
            "id": 10,
            "source": project,
            "updates": {
                key: {"before_credit": before, "after_credit": after}
                for key, before, after in [
                    ("escrow_quota", 100, 50),
                    ("reward_quota", 25, 13),
                    ("net_reward_quota", 25, 13),
                ]
            },
        },
        {
            "table": CHALLENGE,
            "id": 20,
            "source": active,
            "updates": {"reward_quota": {"before_credit": 25, "after_credit": 13}},
            "rights_status": "active_future_reward",
        },
        {
            "table": CHALLENGE,
            "id": 21,
            "source": guard,
            "updates": {},
            "rights_status": "historical_rejection_guard_only",
        },
    ]
    return seal_parent(
        dict(
            version=1,
            kind="offline_credit_balance_rebase_preview",
            migration_id="independent-bounty-parent",
            include_bounties=True,
            include_other_rights=True,
            user_ids=[1, 2],
            divisor="2",
            exact_factor={"numerator": 1, "denominator": 2},
            rounding="half-away-from-zero",
            usd_credit_conversion=500000,
            snapshot_at=1000000,
            entity_updates=entries,
            other_credit_bases=[],
        )
    )


def scenario(parent=None, mutate_current=None):
    parent = seal_parent(copy.deepcopy(parent or parent_fixture()))
    tables = {name: [] for name in F.REQUIRED_TABLES}
    tables.update({PROJECT: [], CHALLENGE: [], DISPUTE: []})
    tables["users"] = [
        dict(id=uid, quota=0, aff_quota=0, used_quota=0, request_count=0, aff_history=0)
        for uid in (1, 2)
    ]
    for entry in parent["entity_updates"]:
        if entry["table"] in (PROJECT, CHALLENGE, DISPUTE):
            tables[entry["table"]].append(
                {
                    **entry["source"],
                    **{
                        key: change["after_credit"]
                        for key, change in entry["updates"].items()
                    },
                }
            )
    tables["wallet_credit_rebases"] = [
        dict(
            migration_id=parent["migration_id"],
            plan_sha256=parent["plan_sha256"],
            plan=parent,
        )
    ]
    if mutate_current:
        mutate_current(tables)
    identities = {1: 1001, 2: 1002}
    plan = F.plan_financial(
        tables,
        identities,
        source_sha256="a" * 64,
        primary_keys={
            name: ["id"] if name != "wallet_credit_rebases" else ["migration_id"]
            for name in tables
        },
        column_schema={name: [] for name in tables},
    )
    return dict(tables=tables, identities=identities, plan=plan)


# @mark migration.bounty-basis role=test
class IndependentBountyLineage(unittest.TestCase):
    def assert_blocked(self, result, state=None):
        self.assertEqual(result["lineage_status"], "not-proven-exact-parent-after")
        self.assertTrue(result["blockers"])
        if state:
            self.assertIn(state, result["public_summary"]["row_state_counts"])

    def test_after_matches_remain_unapproved_and_original_data_unchanged(self):
        args = scenario()
        original = copy.deepcopy(args)
        result = B.build(F, **args)
        self.assertEqual(
            result["lineage_status"], "matches-parent-after-with-unverified-authority"
        )
        self.assertEqual(
            result["public_summary"]["row_state_counts"],
            {"after": 2, "before_and_after": 1},
        )
        for key in (
            "economic_approval_verified",
            "import_payload_created",
            "full_database_rights_cleared",
            "current_amounts_rescaled",
            "dispute_snapshots_rescaled",
        ):
            self.assertIs(result[key], False)
        self.assertTrue(result["blockers"])
        self.assertEqual(args, original)

    def test_single_before_row_and_single_before_field_are_blocked(self):
        for keys in [
            ("reward_quota",),
            ("reward_quota", "net_reward_quota", "escrow_quota"),
        ]:

            def mutate(tables):
                row = tables[PROJECT][0]
                before = parent_fixture()["entity_updates"][0]["source"]
                row.update({key: before[key] for key in keys})

            result = B.build(F, **scenario(mutate_current=mutate))
            with self.subTest(keys=keys):
                self.assert_blocked(
                    result, "before" if len(keys) == 3 else "mixed_or_unmatched_amounts"
                )

    def test_postactivity_in_every_nonwritable_dimension_is_blocked(self):
        for table, key, value in [
            (PROJECT, "owner_user_id", 2),
            (PROJECT, "updated_at", 9),
            (PROJECT, "status", "closed"),
            (PROJECT, "platform_fee_quota", 10),
            (CHALLENGE, "participant_user_id", 1),
            (CHALLENGE, "project_id", 11),
            (CHALLENGE, "paid_at", 9),
            (CHALLENGE, "tip_quota", 8),
        ]:
            result = B.build(
                F, **scenario(mutate_current=lambda t: t[table][0].update({key: value}))
            )
            with self.subTest(table=table, field=key):
                self.assert_blocked(result, "changed_since_snapshot")

    def test_lost_guard_and_extra_active_scope_are_not_cleared(self):
        self.assert_blocked(
            B.build(F, **scenario(mutate_current=lambda t: t[CHALLENGE].pop())),
            "missing",
        )

        def extra(tables):
            tables[PROJECT].append({**tables[PROJECT][0], "id": 11})

        self.assert_blocked(
            B.build(F, **scenario(mutate_current=extra)), "outside_parent_scope"
        )

    def test_invalid_parent_math_allowlist_guard_and_types_are_rejected(self):
        mutations = [
            lambda p: p.update(exact_factor={"numerator": 2, "denominator": 1}),
            lambda p: p.update(rounding="bankers"),
            lambda p: p.update(divisor="1"),
            lambda p: p.update(exact_factor={"numerator": True, "denominator": 2}),
            lambda p: p["entity_updates"][0]["updates"]["reward_quota"].update(
                after_credit=12
            ),
            lambda p: p["entity_updates"][0]["updates"]["escrow_quota"].update(
                before_credit=101
            ),
            lambda p: p["entity_updates"][0]["updates"].update(
                platform_fee_quota={"before_credit": 9, "after_credit": 5}
            ),
            lambda p: p["entity_updates"][0]["source"].update(unapproved=1),
            lambda p: p["entity_updates"][2].update(
                rights_status="active_future_reward"
            ),
            lambda p: p["entity_updates"].append(copy.deepcopy(p["entity_updates"][0])),
        ]
        for index, mutation in enumerate(mutations):
            parent = parent_fixture()
            mutation(parent)
            with self.subTest(mutation=index), self.assertRaises(F.MappingError):
                B.build(F, **scenario(parent))

    def test_guard_at_exact_appeal_cutoff_stays_guard_but_one_second_later_requires_update(
        self,
    ):
        parent = parent_fixture()
        guard = parent["entity_updates"][2]
        guard["source"]["rejected_at"] = parent["snapshot_at"] - 7 * 86400
        B.build(F, **scenario(parent))
        guard["source"]["rejected_at"] += 1
        with self.assertRaises(F.MappingError):
            B.build(F, **scenario(parent))

    def test_current_bool_amount_does_not_alias_integer(self):
        result = B.build(
            F,
            **scenario(
                mutate_current=lambda t: t[CHALLENGE][0].update(reward_quota=True)
            ),
        )
        self.assert_blocked(result, "mixed_or_unmatched_amounts")

    def test_only_fixed_points_do_not_prove_conversion(self):
        parent = parent_fixture()
        for entry in parent["entity_updates"]:
            for field, change in entry["updates"].items():
                entry["source"][field] = 0
                change.update(before_credit=0, after_credit=0)
        self.assert_blocked(B.build(F, **scenario(parent)), "before_and_after")

    def test_incomplete_table_coverage_and_multiple_parent_lineage_are_blocked(self):
        args = scenario()
        del args["tables"][DISPUTE]
        self.assert_blocked(B.build(F, **args))
        args = scenario()
        other = parent_fixture()
        other["migration_id"] = "independent-second-parent"
        seal_parent(other)
        args["tables"]["wallet_credit_rebases"].append(
            dict(
                migration_id=other["migration_id"],
                plan_sha256=other["plan_sha256"],
                plan=other,
            )
        )
        tables = args["tables"]
        args["plan"] = F.plan_financial(
            tables,
            args["identities"],
            source_sha256="a" * 64,
            primary_keys={
                name: ["migration_id"] if name == "wallet_credit_rebases" else ["id"]
                for name in tables
            },
            column_schema={name: [] for name in tables},
        )
        self.assert_blocked(B.build(F, **args))

    def test_duplicate_current_identity_and_unbound_current_evidence_are_rejected(self):
        with self.assertRaises(F.MappingError):
            scenario(
                mutate_current=lambda t: t[CHALLENGE].append(
                    copy.deepcopy(t[CHALLENGE][0])
                )
            )
        args = scenario()
        args["tables"][CHALLENGE][0]["reward_quota"] += 1
        with self.assertRaises(F.MappingError):
            B.build(F, **args)

    def test_dispute_snapshots_stay_old_while_future_basis_is_separate_and_required(
        self,
    ):
        parent = parent_fixture()
        source = dict(
            id=30,
            challenge_id=20,
            project_id=10,
            opened_by_user_id=2,
            against_user_id=1,
            project_escrow_quota_snapshot=100,
            reward_quota_snapshot=25,
            tip_quota_snapshot=7,
            resolved_by_user_id=0,
            created_at=4,
            updated_at=4,
            resolved_at=0,
            challenge_status_snapshot="accepted",
            status="open",
        )
        parent["entity_updates"].append(
            dict(table=DISPUTE, id=30, source=source, updates={})
        )
        basis = dict(
            kind="bounty_dispute_reward",
            source_id="30",
            user_id=2,
            original_quota=25,
            rebased_quota=13,
            source=copy.deepcopy(source),
        )
        parent["other_credit_bases"] = [basis]
        args = scenario(parent)
        result = B.build(F, **args)
        self.assertEqual(args["tables"][DISPUTE][0]["reward_quota_snapshot"], 25)
        self.assertEqual(
            result["lineage_status"], "matches-parent-after-with-unverified-authority"
        )
        for bad in (
            [],
            [basis, basis],
            [{**basis, "user_id": 1}],
            [{**basis, "rebased_quota": 12}],
            [{**basis, "source": {**source, "tip_quota_snapshot": 8}}],
        ):
            broken = copy.deepcopy(parent)
            broken["other_credit_bases"] = bad
            with self.subTest(bases=bad), self.assertRaises(F.MappingError):
                B.build(F, **scenario(broken))

    def test_plan_parent_cannot_be_replaced_independently_of_frozen_audit_row(self):
        args = scenario()
        parent = parent_fixture()
        parent["divisor"] = "4"
        parent["exact_factor"] = {"numerator": 1, "denominator": 4}
        for entry in parent["entity_updates"][:2]:
            for field, change in entry["updates"].items():
                entry["source"][field] *= 2
                change["before_credit"] *= 2
        seal_parent(parent)
        audit = args["plan"]["refund_lineage"]["wallet_rebase_audits"][0]
        audit["full_plan_unknown_fields_preserved"] = parent
        audit["declared_plan_sha256"] = parent["plan_sha256"]
        seal_mapping(args["plan"])
        with self.assertRaises(F.MappingError):
            B.build(F, **args)


if __name__ == "__main__":
    unittest.main()
