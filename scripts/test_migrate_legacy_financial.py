"""Synthetic financial facts: no real rows, credentials or database access."""

import copy
from decimal import Decimal
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


SPEC = importlib.util.spec_from_file_location("financial_import", Path(__file__).with_name("migrate-legacy-financial.py"))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def user(uid=71, **changes):
    return {"id": uid, "quota": 1234567, "aff_quota": 38, "used_quota": 9001,
            "aff_history": 103, "request_count": 11, "status": 1, "deleted_at": None, **changes}


def token(tid=8, **changes):
    return {"id": tid, "user_id": 71, "remain_quota": 8, "used_quota": 92,
            "unlimited_quota": False, "expired_time": -1, "status": 1, "deleted_at": None,
            "model_limits_enabled": True, "model_limits": "synthetic-model", "allow_ips": "192.0.2.0/24",
            "group": "synthetic-group", "auto_groups": "[]", "cross_group_retry": False,
            "oauth_managed": False, "account_balance_read": False, "one_time_reveal": True,
            "creation_source": "synthetic", **changes}


def subscription(sid=4, **changes):
    return {"id": sid, "user_id": 71, "plan_id": 9, "amount_total": 1000, "amount_used": 400,
            "reset_amount": None, "renewal_amount": None, "quota_version": 1,
            "start_time": 1706745600, "end_time": 1740787200, "last_reset_time": 1706745600,
            "next_reset_time": 1709251200, "status": "active", "source": "order",
            "upgrade_group": "synthetic", "prev_user_group": "base", "downgrade_group": "base",
            "allow_wallet_overflow": False, **changes}


def plan(pid=9, **changes):
    return {"id": pid, "quota_reset_period": "monthly", "quota_reset_custom_seconds": 0, **changes}


def topup(tid=11, **changes):
    return {"id": tid, "user_id": 71, "credited_quota": 234567,
            "refunded_quota": 123, "pending_credit_rebase_original_quota": 0,
            "pending_credit_rebase_effective_quota": 0, "expected_amount_micros": 500000,
            "settled_amount_micros": 500000, "refunded_amount_micros": 100,
            "platform_amount_micros": 0, "money": Decimal("0.500000"), "amount": 17,
            "settlement_currency": "CNY", "payment_method": "synthetic", "payment_provider": "synthetic",
            "provider_event_id": None, "provider_transaction_id": "", "trade_no": "synthetic-trade",
            "status": "success", "create_time": 1706745600, "complete_time": 1706745601,
            "pending_credit_rebase_key": "", **changes}


def transfer(tid=3, **changes):
    return {"id": tid, "sender_id": 71, "recipient_id": 0, "quota": 500,
            "status": "pending", "created_at": 1706745600, "claimed_at": 0,
            "cancelled_at": 0, "token": "synthetic-not-a-valid-bearer", **changes}


def refund_basis(**changes):
    return {"top_up_id": 11, "user_id": 71, "migration_id": "synthetic-rebase",
            "original_credited_quota": 1000, "original_refunded_quota": 0,
            "original_refunded_amount_micros": 0, "original_paid_amount_micros": 1000,
            "refundable_quota": 1000, "rebased_debited_quota": 100, **changes}


def rebase_audit(**changes):
    raw = MODULE.canonical({"user_ids": [71], "refund_bases": [{"top_up_id": 11}],
                            "noncash_topups": [], "future_audit_field": {"exact": "retained"}})
    return {"migration_id": "synthetic-rebase", "plan_sha256": hashlib.sha256(raw.encode()).hexdigest(),
            "plan": raw, "applied_at": 1706745600, **changes}


def tables(**changes):
    return {"users": [user()], "tokens": [token()], "top_ups": [],
            "user_subscriptions": [subscription()], "subscription_plans": [plan()], "options": [], **changes}


class ExactValueTests(unittest.TestCase):
    def test_decimal_retains_all_digits_and_scale(self):
        value = MODULE.parse_json('{"money":123456789.123456,"zero":0.000000}')
        self.assertEqual(value["money"], Decimal("123456789.123456"))
        self.assertIn('"$decimal":"0.000000"', MODULE.canonical(value))

    def test_bigint_json_does_not_round(self):
        self.assertEqual(MODULE.parse_json('{"units":9223372036854775807}')["units"], 2**63 - 1)

    def test_binary_float_bool_and_decimal_are_not_credits(self):
        for value in (1.0, True, Decimal("1"), "1", None):
            with self.subTest(value=type(value).__name__), self.assertRaises(MODULE.MappingError):
                MODULE.integer(value, "not_integer")

    def test_bigint_range_is_explicit(self):
        for value in (2**63, -(2**63) - 1):
            with self.assertRaises(MODULE.MappingError):
                MODULE.integer(value, "not_integer")

    def test_duplicate_json_keys_and_nonfinite_are_rejected(self):
        for text in ('{"quota":1,"quota":2}', '{"quota":NaN}', '{"quota":Infinity}'):
            with self.subTest(text=text), self.assertRaises(MODULE.MappingError):
                MODULE.parse_json(text)

    def test_canonical_rejects_nested_float(self):
        with self.assertRaisesRegex(MODULE.MappingError, "binary_float_rejected"):
            MODULE.canonical({"x": [0.1]})

    def test_hash_types_cannot_collide_with_decimal_display_tag_or_int_key(self):
        self.assertNotEqual(MODULE.digest({"x": Decimal("1.00")}), MODULE.digest({"x": {"$decimal": "1.00"}}))
        self.assertNotEqual(MODULE.digest({1: "account"}), MODULE.digest({"1": "account"}))
        self.assertNotEqual(MODULE.digest(True), MODULE.digest(1))
        self.assertNotEqual(MODULE.digest(Decimal("1.0")), MODULE.digest(Decimal("1.00")))

    def test_hash_rejects_float_or_non_json_type_without_silent_coercion(self):
        for value in (1.0, {"x": 0.1}, {"not-a-json-value"}):
            with self.assertRaises(MODULE.MappingError):
                MODULE.digest(value)

    def test_tool_source_binding_rejects_parallel_importer_or_helper_changes(self):
        expected = {"importer_sha256": "a" * 64, "identity_helper_sha256": "b" * 64}
        with patch.object(MODULE, "tool_source_hashes", return_value=expected):
            MODULE.verify_tool_source_hashes(expected)
        for changes in ({"importer_sha256": "c" * 64}, {"identity_helper_sha256": "c" * 64}):
            with patch.object(MODULE, "tool_source_hashes", return_value={**expected, **changes}), self.assertRaisesRegex(MODULE.MappingError, "financial_tool_source_changed_during_run"):
                MODULE.verify_tool_source_hashes(expected)


class WalletFactsTests(unittest.TestCase):
    def test_source_user_and_account_ids_are_independent(self):
        facts = MODULE.map_wallet_positions([user()], {71: 2})
        self.assertEqual((facts[0]["source_user_id"], facts[0]["account_id"]), (71, 2))

    def test_negative_wallet_and_affiliate_are_retained_separately(self):
        row = MODULE.map_wallet_positions([user(quota=-500000, aff_quota=-83)], {71: 2})[0]
        self.assertEqual(row["signed_available_units"], -500000)
        self.assertEqual(row["signed_affiliate_units"], -83)
        self.assertEqual(row["historical_affiliate_units"], 103)

    def test_positive_affiliate_is_not_added_to_wallet(self):
        row = MODULE.map_wallet_positions([user(quota=1, aff_quota=99)], {71: 2})[0]
        self.assertEqual(row["signed_available_units"], 1)
        self.assertEqual(row["signed_affiliate_units"], 99)

    def test_original_disabled_and_deleted_facts_are_preserved(self):
        row = MODULE.map_wallet_positions([user(status=2, deleted_at="2026-01-01T00:00:00Z")], {71: 2})[0]
        self.assertEqual(row["source_status"], 2)
        self.assertEqual(row["source_deleted_at"], "2026-01-01T00:00:00Z")

    def test_duplicate_missing_or_unmatched_identity_aborts(self):
        for users, identities in (([user(), user()], {71: 2}), ([user()], {}), ([], {71: 2})):
            with self.subTest(users=len(users), identities=len(identities)), self.assertRaises(MODULE.MappingError):
                MODULE.map_wallet_positions(users, identities)

    def test_unknown_fields_change_provenance_not_economics(self):
        a = MODULE.map_wallet_positions([user(extra_future_fact="a")], {71: 2})[0]
        b = MODULE.map_wallet_positions([user(extra_future_fact="b")], {71: 2})[0]
        self.assertNotEqual(a["source_row_sha256"], b["source_row_sha256"])
        self.assertEqual(a["signed_available_units"], b["signed_available_units"])

    def test_one_to_one_identity_map_and_original_user_ids_required(self):
        valid = {"source_user_id": 71, "target_user_id": 71, "target_personal_account_id": 2}
        self.assertEqual(MODULE.identity_index([valid]), {71: 2})
        for other in ({**valid, "target_user_id": 72}, valid, {**valid, "source_user_id": 72, "target_user_id": 72}):
            with self.assertRaises(MODULE.MappingError):
                MODULE.identity_index([valid, other])


class KeyPolicyFactsTests(unittest.TestCase):
    def test_lifetime_used_is_not_reset_to_remaining(self):
        row = MODULE.map_key_limits([token()], {71: 2})[0]
        self.assertEqual((row["used_units"], row["remaining_units"], row["lifetime_limit_units"]), (92, 8, 100))

    def test_finite_zero_remains_exhausted(self):
        row = MODULE.map_key_limits([token(remain_quota=0)], {71: 2})[0]
        self.assertFalse(row["unlimited"])
        self.assertEqual(row["lifetime_limit_units"], row["used_units"])

    def test_unlimited_and_negative_remaining_are_both_preserved(self):
        row = MODULE.map_key_limits([token(unlimited_quota=True, remain_quota=-17)], {71: 2})[0]
        self.assertTrue(row["unlimited"])
        self.assertIsNone(row["lifetime_limit_units"])
        self.assertEqual(row["remaining_units"], -17)

    def test_finite_overrun_is_not_clamped_or_reset(self):
        row = MODULE.map_key_limits([token(remain_quota=-12)], {71: 2})[0]
        self.assertEqual((row["lifetime_limit_units"], row["used_units"], row["remaining_units"]), (80, 92, -12))

    def test_secret_is_not_projected_as_active_credential(self):
        row = MODULE.map_key_limits([token(key="synthetic-noncredential")], {71: 2})[0]
        self.assertFalse(row["credential_activation_allowed"])
        self.assertNotIn("key", row)

    def test_all_restrictions_and_expiry_sentinel_are_preserved(self):
        source = token()
        row = MODULE.map_key_limits([source], {71: 2})[0]
        self.assertEqual(row["expired_time"], -1)
        for key, value in row["restrictions"].items():
            self.assertEqual(value, source[key])

    def test_orphan_owner_is_explicit_blocker(self):
        row = MODULE.map_key_limits([token(user_id=999)], {71: 2})[0]
        self.assertIn("orphan_key_owner", row["blockers"])
        self.assertIsNone(row["account_id"])

    def test_unknown_limit_boolean_and_duplicate_id_abort(self):
        for rows in ([token(unlimited_quota=None)], [token(), token()]):
            with self.assertRaises(MODULE.MappingError):
                MODULE.map_key_limits(rows, {71: 2})


class SubscriptionFactsTests(unittest.TestCase):
    def mapped(self, row=None, **kwargs):
        return MODULE.map_subscriptions([row or subscription()], [plan()], {71: 2}, kwargs.get("timezone", "Asia/Taipei"))[0]

    def test_used_and_reset_generation_are_retained(self):
        row = self.mapped()
        self.assertEqual((row["opening_used_units"], row["signed_remaining_units"], row["spendable_remaining_units"], row["quota_version"]), (400, 600, 600, 1))

    def test_null_zero_unlimited_and_exhausted_are_distinct(self):
        unlimited = self.mapped(subscription(amount_total=0, amount_used=400))
        exhausted = self.mapped(subscription(amount_total=0, amount_used=400, reset_amount=0))
        self.assertTrue(unlimited["unlimited"])
        self.assertIsNone(unlimited["current_limit_units"])
        self.assertIsNone(unlimited["signed_remaining_units"])
        self.assertIsNone(unlimited["spendable_remaining_units"])
        self.assertFalse(exhausted["unlimited"])
        self.assertEqual(exhausted["current_limit_units"], 0)
        self.assertEqual(exhausted["opening_used_units"], 400)

    def test_renewal_grant_alone_makes_zero_total_finite(self):
        row = self.mapped(subscription(amount_total=0, renewal_amount=500))
        self.assertFalse(row["unlimited"])
        self.assertEqual(row["renewal_amount"], 500)

    def test_used_above_total_is_retained(self):
        row = self.mapped(subscription(amount_used=1200))
        self.assertEqual(row["opening_used_units"], 1200)
        self.assertEqual(row["spendable_remaining_units"], 0)
        self.assertEqual(row["signed_remaining_units"], -200)
        self.assertEqual(row["original_amount_total"], 1000)

    def test_timezone_and_exact_next_reset_are_not_recomputed(self):
        row = self.mapped()
        self.assertEqual(row["source_timezone"], "Asia/Taipei")
        self.assertEqual(row["calendar_period"], "monthly")
        self.assertEqual(row["next_reset_time"], subscription()["next_reset_time"])

    def test_no_default_database_timezone(self):
        row = self.mapped(timezone=None)
        self.assertIsNone(row["source_timezone"])
        self.assertIn("source_calendar_timezone_unproven", row["blockers"])

    def test_unknown_timezone_rejected(self):
        with self.assertRaisesRegex(MODULE.MappingError, "source_timezone_invalid"):
            self.mapped(timezone="Invalid/Timezone")

    def test_status_orphan_and_negative_facts_are_blocked_not_guessed(self):
        row = MODULE.map_subscriptions([subscription(status="mystery", user_id=999, plan_id=999, amount_used=-1)], [], {71: 2}, None)[0]
        self.assertEqual(set(row["blockers"]), {"orphan_subscription_owner", "orphan_subscription_plan", "negative_subscription_counter", "unknown_subscription_status"})

    def test_duplicate_source_id_aborts(self):
        with self.assertRaises(MODULE.MappingError):
            MODULE.map_subscriptions([subscription(), subscription()], [plan()], {71: 2}, None)


class TransferAndPaymentFactsTests(unittest.TestCase):
    def test_pending_transfer_preserves_escrow_without_second_wallet_debit(self):
        row = MODULE.map_wallet_transfers([transfer()], {71: 2})[0]
        self.assertEqual(row["claimable_escrow_units"], 500)
        self.assertTrue(row["sender_available_already_debited"])
        self.assertEqual(row["wallet_debit_to_apply"], 0)
        self.assertFalse(row["original_bearer_activation_allowed"])
        self.assertNotIn("token", row)

    def test_completed_transfer_does_not_reopen_claim(self):
        row = MODULE.map_wallet_transfers([transfer(status="claimed", recipient_id=72)], {71: 2, 72: 3})[0]
        self.assertEqual(row["claimable_escrow_units"], 0)
        self.assertEqual(row["recipient_account_id"], 3)

    def test_unknown_and_orphan_transfer_are_blocked(self):
        row = MODULE.map_wallet_transfers([transfer(status="mystery", recipient_id=999)], {71: 2})[0]
        self.assertEqual(set(row["blockers"]), {"unknown_transfer_status", "orphan_transfer_recipient"})

    def test_claimed_transfer_requires_recipient(self):
        row = MODULE.map_wallet_transfers([transfer(status="claimed")], {71: 2})[0]
        self.assertIn("claimed_transfer_recipient_missing", row["blockers"])

    def test_original_fiat_and_credit_dimensions_are_independent(self):
        row = MODULE.map_topups([topup()], {71: 2})[0]
        self.assertEqual(row["credit_facts"]["credited_quota"], 234567)
        self.assertEqual(row["fiat_facts"]["settled_amount_micros"], 500000)
        self.assertEqual(row["settlement_currency"], "CNY")
        self.assertEqual(row["legacy_deprecated_amount"], 17)
        self.assertEqual(row["historical_wallet_credit_to_apply"], 0)
        self.assertFalse(row["future_refund_authorized"])

    def test_null_and_empty_provider_evidence_are_distinct(self):
        row = MODULE.map_topups([topup()], {71: 2})[0]
        self.assertIsNone(row["provider_event_id"])
        self.assertEqual(row["provider_transaction_id"], "")
        other = MODULE.map_topups([topup(provider_event_id="", provider_transaction_id=None)], {71: 2})[0]
        self.assertNotEqual(row["source_row_sha256"], other["source_row_sha256"])

    def test_pending_grant_is_not_treated_as_posted_money(self):
        row = MODULE.map_topups([topup(status="pending", credited_quota=0, pending_credit_rebase_key="synthetic-basis", pending_credit_rebase_original_quota=5000, pending_credit_rebase_effective_quota=2500)], {71: 2})[0]
        self.assertFalse(row["terminal"])
        self.assertEqual(row["credit_facts"]["pending_credit_rebase_effective_quota"], 2500)
        self.assertEqual(row["historical_wallet_credit_to_apply"], 0)

    def test_unknown_status_and_missing_completed_credit_basis_are_explicit(self):
        row = MODULE.map_topups([topup(status="unexpected")], {71: 2})[0]
        self.assertIn("unknown_topup_status", row["blockers"])
        row = MODULE.map_topups([topup(credited_quota=0)], {71: 2})[0]
        self.assertIn("completed_topup_credit_basis_unproven", row["blockers"])

    def test_float_money_is_rejected_before_projection(self):
        with self.assertRaisesRegex(MODULE.MappingError, "topup_binary_float_rejected"):
            MODULE.map_topups([topup(money=0.1)], {71: 2})

    def test_source_decimal_precision_and_signed_rebases_not_recomputed(self):
        row = MODULE.map_topups([topup(money=Decimal("123456789.123456"), pending_credit_rebase_effective_quota=-1)], {71: 2})[0]
        self.assertEqual(row["legacy_money_numeric"], Decimal("123456789.123456"))
        self.assertEqual(row["credit_facts"]["pending_credit_rebase_effective_quota"], -1)


class FullPlannerTests(unittest.TestCase):
    def planned(self, source=None):
        return MODULE.plan_financial(source or tables(), {71: 2}, source_sha256="a" * 64)

    def test_all_22_families_are_reported_without_active_import_claim(self):
        value = self.planned()
        self.assertEqual(len(value["families"]), 22)
        self.assertEqual(value["public_summary"]["active_financial_rows_imported"], 0)
        self.assertTrue(value["public_summary"]["family_counts_overlap_do_not_sum"])

    def test_decimal_fiat_and_unknown_columns_are_in_provenance(self):
        original = topup(money=Decimal("1.230000"), future_basis={"nullable": None})
        value = self.planned(tables(top_ups=[original]))
        evidence = value["source_evidence"]["top_ups"]
        self.assertEqual(evidence["row_count"], 1)
        self.assertTrue(evidence["unknown_columns_preserved_in_private_raw_export"])
        self.assertEqual(evidence["row_hashes"], [MODULE.digest(original)])

    def test_source_changes_and_identity_changes_change_plan_hash(self):
        first = self.planned()
        changed = self.planned(tables(users=[user(quota=1)]))
        self.assertNotEqual(first["mapping_sha256"], changed["mapping_sha256"])
        third = MODULE.plan_financial(tables(), {71: 999}, source_sha256="a" * 64)
        self.assertNotEqual(first["mapping_sha256"], third["mapping_sha256"])

    def test_deterministic_replanning_does_not_mutate_inputs(self):
        source = tables()
        original = copy.deepcopy(source)
        self.assertEqual(self.planned(source), self.planned(source))
        self.assertEqual(source, original)

    def test_persisted_plan_mapping_hash_roundtrips_with_decimal_and_literal_tag_object(self):
        source = tables(top_ups=[topup(money=Decimal("1.230000"), future_fact={"$decimal": "1.230000"})],
                        wallet_credit_rebases=[rebase_audit(plan={"user_ids": [71], "refund_bases": [],
                                                               "decimal": Decimal("0.000000"), "literal": {"$decimal": "0.000000"}})])
        plan = self.planned(source)
        readback = MODULE.parse_json(MODULE.canonical(plan))
        self.assertEqual(plan, readback)
        MODULE.verify_plan_mapping(readback)
        readback["topup_history"][0]["fiat_facts"]["settled_amount_micros"] += 1
        with self.assertRaisesRegex(MODULE.MappingError, "financial_plan_mapping_hash_mismatch"):
            MODULE.verify_plan_mapping(readback)

    def test_literal_tag_cannot_replace_original_decimal_in_source_provenance(self):
        a = self.planned(tables(users=[user(future_json_fact=Decimal("1.00"))]))
        b = self.planned(tables(users=[user(future_json_fact={"$decimal": "1.00"})]))
        self.assertNotEqual(a["source_evidence"]["users"]["row_hashes"], b["source_evidence"]["users"]["row_hashes"])
        self.assertNotEqual(a["mapping_sha256"], b["mapping_sha256"])

    def test_unknown_financial_table_is_not_silently_dropped(self):
        value = self.planned(tables(new_wallet_debts=[{"id": 8, "quota": -1}]))
        self.assertEqual(value["unclassified_financial_tables"], ["new_wallet_debts"])
        self.assertEqual(value["blocker_counts"]["unclassified_financial_source_table"], 1)
        self.assertIn("new_wallet_debts", value["source_evidence"])

    def test_auxiliary_tokens_are_retained_disabled_without_false_balance_classification(self):
        value = self.planned(tables(drawing_mcp_tokens=[{"id": 8, "key": "synthetic-disabled"}], oauth_server_tokens=[]))
        self.assertEqual(value["unclassified_financial_tables"], [])
        evidence = value["source_evidence"]["drawing_mcp_tokens"]
        self.assertEqual(evidence["row_count"], 1)
        self.assertEqual(evidence["disposition"], "disabled-auxiliary-credential-private-archive")
        self.assertTrue(evidence["unknown_columns_preserved_in_private_raw_export"])

    def test_missing_required_source_table_aborts(self):
        source = tables()
        del source["users"]
        with self.assertRaises(MODULE.MappingError):
            self.planned(source)

    def test_apply_is_rejected_before_any_source_read_or_output_creation(self):
        argv = ["financial-import", "--source-receipt", "/must-not-read", "--identity-map", "/must-not-read", "--identity-manifest", "/must-not-read",
                "--output", "/must-not-write", "--migration-id", "synthetic", "--apply"]
        with patch.object(MODULE.sys, "argv", argv), patch.object(MODULE, "export_source") as export, patch.object(MODULE.IDENTITY, "private_backup_path") as private_path, patch.object(MODULE.sys, "stderr", io.StringIO()) as err:
            self.assertEqual(MODULE.main(), 1)
        export.assert_not_called()
        private_path.assert_not_called()
        self.assertIn("financial_apply_contract_not_frozen", err.getvalue())


class IdentityProvenanceTests(unittest.TestCase):
    def manifest(self, **changes):
        return {"format_version": 1, "scope": "identity-only", "source_archive_sha256": "a" * 64,
                "source_target_id_map_sha256": "b" * 64, "mapping_sha256": "c" * 64, **changes}

    def provenance(self):
        return MODULE.bind_identity_provenance(self.manifest(), map_file_sha256="b" * 64, source_archive_sha256="a" * 64)

    def test_id_map_is_bound_to_identity_manifest_and_same_source(self):
        proof = self.provenance()
        self.assertEqual(proof["identity_mapping_sha256"], "c" * 64)
        self.assertTrue(proof["target_identity_import_and_native_accounts_not_yet_verified"])

    def test_wrong_archive_map_hash_or_scope_is_rejected(self):
        for changes in ({"source_archive_sha256": "d" * 64}, {"source_target_id_map_sha256": "d" * 64},
                        {"scope": "not-identity"}, {"mapping_sha256": "invalid"}):
            with self.subTest(changes=changes), self.assertRaises(MODULE.MappingError):
                MODULE.bind_identity_provenance(self.manifest(**changes), map_file_sha256="b" * 64, source_archive_sha256="a" * 64)

    def test_v3_manifest_policy_is_retained_and_unknown_format_is_rejected(self):
        manifest = self.manifest(format_version=2, identity_contract={"version": 3, "schema_sha256": "d" * 64},
                                 developer_policy={"unproven_paid_activation": False})
        value = MODULE.bind_identity_provenance(manifest, map_file_sha256="b" * 64, source_archive_sha256="a" * 64)
        self.assertEqual(value["identity_contract"]["version"], 3)
        self.assertEqual(value["expected_identity_manifest"], manifest)
        for version in (True, 3, "2"):
            with self.subTest(version=version), self.assertRaises(MODULE.MappingError):
                MODULE.bind_identity_provenance(self.manifest(format_version=version), map_file_sha256="b" * 64, source_archive_sha256="a" * 64)

    def test_target_import_run_and_native_account_mapping_must_both_match(self):
        identities = {71: 2}
        plan = MODULE.plan_financial(tables(), identities, source_sha256="a" * 64, identity_provenance=self.provenance())
        run = {"source_archive_sha256": "a" * 64, "mapping_sha256": "c" * 64, "manifest": self.manifest()}
        users = [{"id": 71, "personal_account_id": 2}]
        verified = MODULE.verify_target_identity_binding(plan, identities, import_run=run, native_users=users)
        self.assertEqual(verified["matched_user_count"], 1)
        self.assertTrue(verified["requires_same_transaction_target_receipt_verification_and_row_locks"])
        for bad_users in ([], [{"id": 71, "personal_account_id": 99}], users * 2,
                          users + [{"id": 72, "personal_account_id": 3}]):
            with self.subTest(users=bad_users), self.assertRaises(MODULE.MappingError):
                MODULE.verify_target_identity_binding(plan, identities, import_run=run, native_users=bad_users)

    def test_target_identity_manifest_policy_differences_are_not_ignored(self):
        identities = {71: 2}
        plan = MODULE.plan_financial(tables(), identities, source_sha256="a" * 64, identity_provenance=self.provenance())
        users = [{"id": 71, "personal_account_id": 2}]
        for changes in ({"source_archive_sha256": "d" * 64}, {"mapping_sha256": "d" * 64},
                        {"manifest": self.manifest(summary={"blocked_user_count": 0})}):
            run = {"source_archive_sha256": "a" * 64, "mapping_sha256": "c" * 64, "manifest": self.manifest(), **changes}
            with self.subTest(changes=changes), self.assertRaises(MODULE.MappingError):
                MODULE.verify_target_identity_binding(plan, identities, import_run=run, native_users=users)


class SourceEvidenceTests(unittest.TestCase):
    def test_composite_and_quoted_primary_keys_are_actual_catalog_keys(self):
        columns = {"abilities": {"group", "model", "channel_id"}}
        constraints = [{"schema": "legacy", "table_name": "abilities", "contype": "p",
                        "definition": 'PRIMARY KEY ("group", model, channel_id)'}]
        self.assertEqual(MODULE.primary_keys_from_catalog(constraints, columns, "legacy"),
                         {"abilities": ["group", "model", "channel_id"]})

    def test_invalid_primary_key_expression_schema_or_missing_column_aborts(self):
        good = {"schema": "legacy", "table_name": "users", "contype": "p", "definition": "PRIMARY KEY (id)"}
        for changes in ({"schema": "different"}, {"definition": "PRIMARY KEY (missing)"},
                        {"definition": "PRIMARY KEY (lower(id))"}):
            with self.subTest(changes=changes), self.assertRaises(MODULE.MappingError):
                MODULE.primary_keys_from_catalog([{**good, **changes}], {"users": {"id"}}, "legacy")

    def test_generic_source_rows_keep_actual_ids_and_no_fabricated_id_for_keyless_tables(self):
        source = tables(custom_wallet_history=[{"event": "synthetic", "quota": 1}])
        value = MODULE.plan_financial(source, {71: 2}, source_sha256="a" * 64,
                                     primary_keys={"users": ["id"]})
        users = value["source_evidence"]["users"]
        self.assertEqual(users["source_records"][0]["source_primary_key"], {"id": 71})
        events = value["source_evidence"]["custom_wallet_history"]
        self.assertIsNone(events["source_records"][0]["source_primary_key"])
        self.assertTrue(events["primary_key_missing_no_synthetic_id_created"])
        self.assertEqual(events["source_records"][0]["source_export_ordinal"], 0)

    def test_duplicate_or_null_catalog_primary_key_values_are_rejected(self):
        for rows in ([token(), token()], [token(id=None)]):
            with self.subTest(rows=rows), self.assertRaises(MODULE.MappingError):
                MODULE.plan_financial(tables(tokens=rows), {71: 2}, source_sha256="a" * 64, primary_keys={"tokens": ["id"]})

    def test_activity_states_preserve_recovery_and_fee_held_without_guessing_zero_reserved(self):
        source = {"subscription_pre_consume_records": [
            {"status": "settled", "recovery_state": "", "billing_managed": True, "wallet_overflow": False},
            {"status": "settling", "recovery_state": "manual", "billing_managed": True, "wallet_overflow": True}],
            "merchant_store_orders": [{"status": "paid", "fee_held": True}],
            "open_source_bounty_projects": [{"status": "published", "escrow_quota": 17}]}
        value = MODULE.classify_economic_states(source)
        self.assertEqual(value["table_states"]["subscription_pre_consume_records"]["state_counts"]["recovery_state"],
                         {'""': 1, '"manual"': 1})
        self.assertEqual(value["bounty_nonzero_escrow_row_count"], 1)
        self.assertTrue(value["reserved_units_not_inferred_from_these_counts"])
        self.assertTrue(value["discount_capacity_is_not_wallet_credit_reserve"])
        self.assertNotIn("reserved_units", value)

    def test_missing_activity_state_column_is_not_given_a_default(self):
        with self.assertRaisesRegex(MODULE.MappingError, "economic_state_column_missing"):
            MODULE.classify_economic_states({"merchant_store_orders": [{"status": "paid"}]})

    def test_frozen_export_rejects_dropped_columns_rows_and_changed_instance(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "synthetic.jsonl"
            receipt = {"readonly_reader": {}, "source_instance_identity": {"schema_owner": "owner"}, "source": {"schema": "legacy"}}
            identity = {"synthetic": "instance"}
            tags = [{"kind": "identity", "row": identity},
                    {"kind": "schema", "row": {"schema": "legacy", "schema_owner": "owner"}},
                    {"kind": "row", "table": "users", "row": {"id": 71, "quota": 17}}]
            path.write_text("".join(MODULE.canonical(tag) + "\n" for tag in tags))
            with patch.object(MODULE.IDENTITY, "verify_instance") as verify:
                rows = MODULE.read_frozen_export(path, receipt, {"users": {"id", "quota"}}, {"users": 1}, ["users"], identity)
                self.assertEqual(rows["users"], [{"id": 71, "quota": 17}])
                verify.assert_called_once()
                for columns, baseline, expected in (({"users": {"id", "quota", "future"}}, {"users": 1}, identity),
                                                     ({"users": {"id", "quota"}}, {"users": 2}, identity),
                                                     ({"users": {"id", "quota"}}, {"users": 1}, {"changed": "instance"})):
                    with self.assertRaises(MODULE.MappingError):
                        MODULE.read_frozen_export(path, receipt, columns, baseline, ["users"], expected)

    def test_replanning_is_hash_bound_and_never_calls_psql(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            archive = root / "synthetic.dump"; archive.write_bytes(b"synthetic-not-production")
            raw = root / "source-financial-export.private.jsonl"
            identity = {"synthetic": "instance"}
            raw.write_text("\n".join(MODULE.canonical(tag) for tag in (
                {"kind": "identity", "row": identity},
                {"kind": "schema", "row": {"schema": "legacy", "schema_owner": "owner"}})) + "\n")
            proof_path = root / "source-export-proof.private.json"
            metadata_hashes = {"catalog": "c" * 64}
            proof = {"source_archive_sha256": MODULE.IDENTITY.sha256_file(archive),
                     "source_export_sha256": MODULE.IDENTITY.sha256_file(raw), "metadata_hashes": metadata_hashes,
                     "source_column_coverage_exact": True, "source_row_baseline_exact": True,
                     "selected_table_counts": {}, "source_instance": identity}
            proof_path.write_text(MODULE.canonical(proof))
            receipt = {"status": "snapshot-restored-baseline-verified-frozen-and-classified", "archive": {"path": str(archive), "sha256": proof["source_archive_sha256"]},
                       "source": {"schema": "legacy"}, "source_instance_identity": {"schema_owner": "owner"}, "readonly_reader": {}}
            output = root / "replan"; output.mkdir()
            with patch.object(MODULE.IDENTITY, "private_backup_path", side_effect=lambda path: path), \
                 patch.object(MODULE.IDENTITY, "verify_instance"), \
                 patch.object(MODULE.IDENTITY, "psql", side_effect=AssertionError("must-not-query")) as psql, \
                 patch.object(MODULE, "metadata", return_value=({}, {}, metadata_hashes, {}, {})):
                rows, bound = MODULE.reuse_frozen_export(receipt, proof_path, output)
                self.assertEqual(rows, {})
                self.assertEqual(bound["source_queries_this_run"], 0)
                self.assertEqual(bound["source_export_sha256"], proof["source_export_sha256"])
                raw.write_text(raw.read_text() + "\n")
                with self.assertRaisesRegex(MODULE.MappingError, "source_export_hash_mismatch"):
                    MODULE.reuse_frozen_export(receipt, proof_path, output)
                psql.assert_not_called()


class RefundLineageTests(unittest.TestCase):
    def mapped(self, **changes):
        return MODULE.map_refund_lineage(tables(top_ups=[topup()], wallet_topup_credit_rebases=[refund_basis()],
                                                wallet_credit_rebases=[rebase_audit()], **changes), {71: 2})

    def test_rebase_keeps_original_units_and_validates_cumulative_debit(self):
        value = self.mapped()
        child = value["wallet_refund_children"][0]
        self.assertEqual(child["original_credited_quota"], 1000)
        self.assertEqual(child["recomputed_cumulative_rebased_target_units"], 100)
        self.assertEqual(child["blockers"], [])
        self.assertFalse(value["future_refunds_enabled"])
        self.assertFalse(child["future_refund_authorized"])
        self.assertTrue(child["provider_normalized_credit_and_paid_authority_not_yet_verified"])

    def test_half_up_cumulative_math_is_integer_and_not_current_fx(self):
        self.assertEqual(MODULE.rebased_refund_target(1, 2, 1), 1)
        self.assertEqual(MODULE.rebased_refund_target(10, 3, 1), 3)
        self.assertEqual(MODULE.rebased_refund_target(10, 3, 2), 7)
        self.assertEqual(MODULE.rebased_refund_target(2**63 - 1, 3, 2), 6148914691236517205)
        self.assertEqual(MODULE.rebased_refund_target(10, 3, 3), 10)
        self.assertEqual(MODULE.rebased_refund_target(10, 3, 0), 0)

    def test_counter_mismatch_blocks_without_fixing_or_reposting(self):
        source = tables(top_ups=[topup()], wallet_topup_credit_rebases=[refund_basis(rebased_debited_quota=99)])
        child = MODULE.map_refund_lineage(source, {71: 2})["wallet_refund_children"][0]
        self.assertIn("refund_basis_rebased_debit_mismatch", child["blockers"])
        self.assertEqual(child["rebased_debited_quota"], 99)

    def test_fully_refunded_original_basis_is_valid_history_without_remaining_refund_right(self):
        source = tables(top_ups=[topup(refunded_amount_micros=1000)],
                        wallet_topup_credit_rebases=[refund_basis(original_refunded_amount_micros=1000, rebased_debited_quota=0)])
        row = MODULE.map_refund_lineage(source, {71: 2})["wallet_refund_children"][0]
        self.assertTrue(row["original_refund_rights_exhausted_at_basis"])
        self.assertIsNone(row["recomputed_cumulative_rebased_target_units"])
        self.assertEqual(row["blockers"], [])
        self.assertFalse(row["future_refund_authorized"])

    def test_missing_child_in_durable_plan_never_reenables_old_unit_refund(self):
        source = tables(top_ups=[topup()], wallet_credit_rebases=[rebase_audit()])
        value = MODULE.map_refund_lineage(source, {71: 2})
        self.assertEqual(value["wallet_rebase_audits"][0]["missing_refund_child_source_topup_ids"], [11])
        self.assertIn("wallet_rebase_refund_child_missing", value["topup_refund_lineage"][0]["blockers"])
        self.assertFalse(value["topup_refund_lineage"][0]["future_refund_authorized"])

    def test_affected_owner_missing_bases_and_noncash_are_explicit(self):
        raw = MODULE.canonical({"user_ids": [71], "noncash_topups": [{"id": 11}]})
        audit = rebase_audit(plan=raw, plan_sha256=hashlib.sha256(raw.encode()).hexdigest())
        value = MODULE.map_refund_lineage(tables(top_ups=[topup()], wallet_credit_rebases=[audit]), {71: 2})
        row = value["topup_refund_lineage"][0]
        self.assertTrue(row["historical_noncash"])
        self.assertEqual(set(row["blockers"]), {"wallet_rebase_affected_user_refund_bases_missing", "historical_noncash_topup_not_refundable"})

    def test_audit_raw_plan_unknown_fields_and_hash_are_retained(self):
        value = self.mapped()
        audit = value["wallet_rebase_audits"][0]
        self.assertEqual(audit["declared_plan_sha256"], audit["raw_plan_sha256"])
        self.assertEqual(audit["full_plan_unknown_fields_preserved"]["future_audit_field"], {"exact": "retained"})
        bad = MODULE.map_refund_lineage(tables(top_ups=[topup()], wallet_credit_rebases=[rebase_audit(plan_sha256="d" * 64)]), {71: 2})
        self.assertIn("wallet_rebase_raw_plan_hash_mismatch", bad["wallet_rebase_audits"][0]["blockers"])

    def test_jsonb_preserves_semantics_and_declared_hash_without_faking_original_json_bytes(self):
        original = rebase_audit()
        parsed = MODULE.parse_json(original["plan"])
        audit = rebase_audit(plan=parsed, plan_sha256="e" * 64)
        value = MODULE.map_refund_lineage(tables(top_ups=[topup()], wallet_topup_credit_rebases=[refund_basis()],
                                                 wallet_credit_rebases=[audit]), {71: 2})
        row = value["wallet_rebase_audits"][0]
        self.assertEqual(row["declared_plan_sha256"], "e" * 64)
        self.assertIsNone(row["raw_plan_sha256"])
        self.assertEqual(row["source_plan_semantic_sha256"], MODULE.digest(parsed))
        self.assertIn("not-reverified", row["declared_plan_hash_validation"])
        self.assertEqual(row["blockers"], [])

    def test_owner_and_migration_lineage_mismatch_are_explicit(self):
        source = tables(top_ups=[topup()], wallet_topup_credit_rebases=[refund_basis(user_id=99, migration_id="other")],
                        wallet_credit_rebases=[rebase_audit()])
        value = MODULE.map_refund_lineage(source, {71: 2})
        self.assertIn("refund_basis_owner_mismatch", value["wallet_refund_children"][0]["blockers"])
        self.assertIn("wallet_rebase_refund_child_run_mismatch", value["wallet_rebase_audits"][0]["blockers"])

    def test_malformed_plan_and_duplicate_child_abort_whole_plan(self):
        for source in (tables(wallet_credit_rebases=[rebase_audit(plan="not-json")]),
                       tables(wallet_topup_credit_rebases=[refund_basis(), refund_basis()])):
            with self.subTest(source_keys=list(source)), self.assertRaises((MODULE.MappingError, ValueError)):
                MODULE.map_refund_lineage(source, {71: 2})


class OpeningPayloadTests(unittest.TestCase):
    def planned(self, **changes):
        return MODULE.plan_financial(tables(users=[user(**changes)]), {71: 2}, source_sha256="a" * 64)

    def observation(self):
        return MODULE.source_observation({"started_utc": "20261011T174353Z"})

    def reservations(self, plan, **changes):
        return {"source_archive_sha256": plan["source_archive_sha256"], "mapping_sha256": plan["mapping_sha256"],
                "reviewed_evidence_sha256": "b" * 64,
                "positions": [{"source_user_id": 71, "reserved_units": 0}], **changes}

    def build(self, plan=None, observation=None, reservations=None):
        plan = self.planned() if plan is None else plan
        return MODULE.build_opening_payloads(plan, migration_id="synthetic-financial",
            observation=self.observation() if observation is None else observation,
            reservation_reconciliation=self.reservations(plan) if reservations is None else reservations)

    def test_only_exact_ten_owner_contract_keys_are_sent(self):
        value = self.build()
        self.assertEqual(set(value["payloads"][0]), {"account_id", "affiliate_units", "available_units", "mapping_sha256",
            "migration_id", "observed_at", "reserved_units", "source_archive_sha256", "source_entity_key", "source_row_sha256"})
        self.assertEqual(value["target_writes"], 0)

    def test_negative_wallet_and_affiliate_are_not_erased_or_combined(self):
        row = self.build(self.planned(quota=-83, aff_quota=-17))["payloads"][0]
        self.assertEqual((row["available_units"], row["affiliate_units"], row["reserved_units"]), (-83, -17, 0))
        self.assertEqual(row["source_entity_key"], "legacy.users/71")
        self.assertEqual(row["account_id"], 2)

    def test_zero_position_still_produces_receipt_payload(self):
        value = self.build(self.planned(quota=0, aff_quota=0))
        self.assertEqual(len(value["payloads"]), 1)
        self.assertEqual(value["payloads"][0]["available_units"], 0)

    def test_source_original_row_hash_binds_unknown_fields(self):
        a = self.build(self.planned(extra_fact="a"))
        b = self.build(self.planned(extra_fact="b"))
        self.assertNotEqual(a["payloads"][0]["source_row_sha256"], b["payloads"][0]["source_row_sha256"])
        self.assertNotEqual(a["opening_payloads_sha256"], b["opening_payloads_sha256"])

    def test_opening_rejects_tampered_plan_even_when_supplied_mapping_hash_is_well_formed(self):
        plan = self.planned()
        plan["wallet_positions"][0]["signed_available_units"] += 1
        with self.assertRaisesRegex(MODULE.MappingError, "financial_plan_mapping_hash_mismatch"):
            self.build(plan)

    def test_js_safe_limits_apply_before_database(self):
        for change in ({"quota": MODULE.JS_SAFE_MAX + 1}, {"quota": -MODULE.JS_SAFE_MAX - 1}, {"aff_quota": MODULE.JS_SAFE_MAX + 1}):
            with self.subTest(change=change), self.assertRaises(MODULE.MappingError):
                self.build(self.planned(**change))

    def test_reserved_has_no_implicit_zero_or_partial_coverage(self):
        for positions in (None, [], [{"source_user_id": 72, "reserved_units": 0}],
                          [{"source_user_id": 71, "reserved_units": -1}],
                          [{"source_user_id": 71, "reserved_units": True}],
                          [{"source_user_id": 71, "reserved_units": 0}] * 2):
            plan = self.planned()
            with self.subTest(positions=positions), self.assertRaises(MODULE.MappingError):
                self.build(plan, reservations=self.reservations(plan, positions=positions))

    def test_explicit_reviewed_reservation_is_retained(self):
        plan = self.planned()
        row = self.build(plan, reservations=self.reservations(plan, positions=[{"source_user_id": 71, "reserved_units": 500}]))["payloads"][0]
        self.assertEqual(row["reserved_units"], 500)
        self.assertEqual(row["available_units"], user()["quota"])

    def test_evidence_and_same_source_mapping_are_required(self):
        plan = self.planned()
        for changes in ({"source_archive_sha256": "c" * 64}, {"mapping_sha256": "c" * 64},
                        {"reviewed_evidence_sha256": None}):
            with self.subTest(changes=changes), self.assertRaises(MODULE.MappingError):
                self.build(plan, reservations=self.reservations(plan, **changes))

    def test_pending_transfer_cannot_be_relabelled_as_arbitrary_reserve(self):
        plan = MODULE.plan_financial(tables(wallet_transfers=[transfer()]), {71: 2}, source_sha256="a" * 64)
        with self.assertRaisesRegex(MODULE.MappingError, "pending_transfer_claim_contract_missing"):
            self.build(plan)

    def test_observed_at_is_frozen_window_start_not_now_or_settlement_time(self):
        a = self.build()
        self.assertEqual(a["payloads"][0]["observed_at"], 1791740633)
        self.assertIs(type(a["payloads"][0]["observed_at"]), int)
        self.assertEqual(a["observation"]["capture_window_started_at_iso"], "2026-10-11T17:43:53+00:00")
        self.assertFalse(a["observation"]["exact_financial_snapshot_instant_known"])
        self.assertEqual(a, self.build())

    def test_missing_bad_or_naive_source_time_is_rejected(self):
        for receipt in ({}, {"started_utc": "not-a-time"}):
            with self.assertRaises(MODULE.MappingError):
                MODULE.source_observation(receipt)
        for observed_at in ("2026-10-11T17:43:53", "2026-10-11T17:43:53+08:00", "now", None, True, 1791740633.0):
            with self.subTest(observed_at=observed_at), self.assertRaises(MODULE.MappingError):
                self.build(observation={**self.observation(), "observed_at": observed_at})

    def test_source_epoch_and_iso_evidence_must_match_exactly(self):
        for iso in ("2026-10-11T17:43:54+00:00", "2026-10-11T17:43:53.000001+00:00", "2026-10-11T17:43:53+08:00"):
            with self.subTest(iso=iso), self.assertRaises(MODULE.MappingError):
                self.build(observation={**self.observation(), "capture_window_started_at_iso": iso})

    def test_paid_permission_counters_and_refund_basis_are_not_claimed_imported(self):
        result = self.build()
        self.assertFalse(result["historical_user_counters_imported_by_this_contract"])
        self.assertFalse(result["terminal_transfer_and_payment_basis_imported_by_this_contract"])
        self.assertNotIn("historical_used_units", result["payloads"][0])

    def test_signed_historical_counters_share_full_user_row_hash_and_create_no_money(self):
        plan = self.planned(used_quota=-11, request_count=17, aff_history=-5)
        opening = self.build(plan)
        result = MODULE.build_opening_counter_payloads(plan, opening)
        row = result["payloads"][0]
        self.assertEqual(set(row), {"account_id", "user_id", "migration_id", "source_entity_key", "source_archive_sha256",
            "mapping_sha256", "source_row_sha256", "observed_at", "used_quota", "request_count", "aff_history"})
        self.assertEqual((row["used_quota"], row["request_count"], row["aff_history"]), (-11, 17, -5))
        self.assertEqual(row["source_row_sha256"], opening["payloads"][0]["source_row_sha256"])
        self.assertEqual(result["wallet_or_affiliate_units_created_by_counters"], 0)

    def test_counter_payload_cannot_mix_account_source_row_or_migration_basis(self):
        plan = self.planned()
        for changes in ({"source_row_sha256": "e" * 64}, {"source_entity_key": "legacy.users/72"},
                        {"account_id": 99}, {"mapping_sha256": "e" * 64}):
            opening = self.build(plan)
            opening["payloads"][0].update(changes)
            with self.subTest(changes=changes), self.assertRaises(MODULE.MappingError):
                MODULE.build_opening_counter_payloads(plan, opening)

    def test_counter_signed_limits_are_not_saturated_or_rebased(self):
        plan = self.planned(request_count=MODULE.JS_SAFE_MAX + 1)
        with self.assertRaisesRegex(MODULE.MappingError, "opening_counter_out_of_range"):
            MODULE.build_opening_counter_payloads(plan, self.build(plan))


if __name__ == "__main__":
    unittest.main()
