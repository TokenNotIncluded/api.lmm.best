#!/usr/bin/env python3
"""Plan a financial import from the frozen, local SELECT-only legacy snapshot.

The first phase deliberately has no target writer. It preserves every selected
source column privately, derives exact typed economic facts, and names missing
runtime contracts. Archive coverage is never reported as active migration.
"""

from __future__ import annotations

import argparse
from collections import Counter
from datetime import datetime, timezone
from decimal import Decimal
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

import legacy_financial_preflight as FINANCIAL_PREFLIGHT


_SPEC = importlib.util.spec_from_file_location("legacy_identity_import", Path(__file__).with_name("migrate-legacy-snapshot.py"))
IDENTITY = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(IDENTITY)
MappingError = IDENTITY.MappingError
require = IDENTITY.require
BIGINT_MIN, BIGINT_MAX = -(2**63), 2**63 - 1
JS_SAFE_MAX = 9007199254740991
UNIT = "credit_500k_usd"
DIGEST_FORMAT = "sha256-typed-json-tree-v1"
MAPPING_DIGEST_DOMAIN = "persisted-plan-json-excluding-mapping_sha256-and-public_summary-v1"

# These are overlapping semantic facets, not disjoint row-count denominators.
# Twenty rows in the original financial-map, with the user's historical counters
# and independently claimable affiliate position split out, yield 22 facets.
FAMILIES = {
    "wallet_positions": ("users",),
    "historical_user_counters": ("users",),
    "affiliate_positions": ("users", "referral_*", "wallet_referral_credit_rebases"),
    "wallet_transfers": ("wallet_transfers",),
    "api_key_limits": ("tokens",),
    "topup_history": ("top_ups", "payment_refunds"),
    "pending_topup_bases": ("top_ups", "wallet_credit_rebases"),
    "wallet_refund_bases": ("top_ups", "wallet_topup_credit_rebases", "wallet_credit_rebases"),
    "subscription_plans": ("subscription_plans",),
    "subscription_payments": ("subscription_orders", "subscription_payment_events", "waffo_pancake_subscription_*"),
    "subscription_entitlements": ("user_subscriptions",),
    "subscription_unlimited_semantics": ("user_subscriptions",),
    "subscription_calendar_policy": ("user_subscriptions", "subscription_plans"),
    "inflight_billing": ("subscription_pre_consume_records",),
    "subscription_refund_bases": ("subscription_payment_refunds", "subscription_order_credit_rebases", "wallet_credit_rebases"),
    "fiat_finance_history": ("finance_ledger_entries", "finance_payment_methods"),
    "discount_reservations": ("discount_codes", "discount_code_reservations"),
    "reward_claims": ("redemptions", "gifts", "gift_claims", "referral_rewards", "acquisition_first_payments"),
    "other_monetary_rights": ("red_packet*", "open_source_bounty_*", "subscription_reset_*", "checkins", "company_billing_profiles", "assistant_*", "merchant_store_*", "tool_market_*", "hero_sms_*_quota_ledgers"),
    "usage_analytics": ("logs", "quota_data"),
    "routing_prices_and_secrets": ("options", "channels", "abilities"),
    "replay_tombstones": ("waffo_pancake_webhook_receipts",),
}
FINANCIAL_NAME = re.compile(r"wallet|subscription|payment|refund|finance|top_up|budget|token|discount|gift|referral|redemption|bounty|merchant_store|tool_market|quota|red_packet|checkin|company_billing|acquisition_first")
REQUIRED_TABLES = {"users", "tokens", "top_ups", "user_subscriptions", "subscription_plans", "options"}
# These token tables are credentials, not credit balances. Retain their complete
# rows privately, explicitly disabled; neither their names nor archives make
# them an API-key/financial migration contract.
AUXILIARY_CREDENTIAL_TABLES = {"drawing_mcp_tokens", "oauth_server_tokens"}
ECONOMIC_STATE_FIELDS = {
    "subscription_pre_consume_records": ("status", "recovery_state", "billing_managed", "wallet_overflow"),
    "open_source_bounty_projects": ("status",),
    "merchant_store_orders": ("status", "fee_held"),
    "merchant_store_refunds": ("status",),
    "tool_market_calls": ("execution_status", "settlement_status", "billing_mode"),
    "assistant_new_user_gifts": ("status",),
    "subscription_reset_vouchers": ("status",),
    "discount_code_reservations": ("status",),
    "redemptions": ("status",),
    "wallet_transfers": ("status",),
}


def integer(value, code: str, *, minimum=BIGINT_MIN, maximum=BIGINT_MAX) -> int:
    require(type(value) is int and minimum <= value <= maximum, code)
    return value


def exact(value):
    """JSON-safe typed decimal encoding; raw PG JSON is retained separately."""
    if isinstance(value, Decimal):
        require(value.is_finite(), "non_finite_decimal")
        return {"$decimal": str(value)}
    if type(value) is float:
        raise MappingError("binary_float_rejected")
    if isinstance(value, dict):
        return {key: exact(item) for key, item in value.items()}
    if isinstance(value, list):
        return [exact(item) for item in value]
    return value


def canonical(value) -> str:
    return json.dumps(exact(value), ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)


def digest(value) -> str:
    # Tag every type, not just Decimal: a literal JSONB {"$decimal":"1.00"}
    # must not hash as the numeric 1.00. The private raw export is unmodified.
    def typed(item):
        if item is None:
            return ["null"]
        if type(item) is bool:
            return ["boolean", item]
        if type(item) is int:
            return ["integer", str(item)]
        if isinstance(item, Decimal):
            require(item.is_finite(), "non_finite_decimal")
            return ["decimal", str(item)]
        if type(item) is str:
            return ["string", item]
        if isinstance(item, list):
            return ["array", [typed(child) for child in item]]
        if isinstance(item, dict):
            require(all(type(key) in (str, int) for key in item), "hash_object_key_invalid")
            return ["object", sorted([[typed(key), typed(child)] for key, child in item.items()], key=lambda pair: canonical(pair[0]))]
        raise MappingError("binary_float_or_unsupported_hash_type_rejected")
    return hashlib.sha256(canonical(typed(value)).encode()).hexdigest()


def no_duplicate_keys(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate_json_key")
        result[key] = value
    return result


def parse_json(text: str):
    return json.loads(text, parse_float=Decimal, object_pairs_hook=no_duplicate_keys,
                      parse_constant=lambda _: (_ for _ in ()).throw(MappingError("non_finite_json_number")))


def matches(table: str, pattern: str) -> bool:
    return re.fullmatch(re.escape(pattern).replace(r"\*", ".*"), table) is not None


def table_families(table: str) -> list[str]:
    return sorted(family for family, patterns in FAMILIES.items() if any(matches(table, pattern) for pattern in patterns))


def identity_index(rows: list[dict]) -> dict[int, int]:
    result, accounts = {}, set()
    for row in rows:
        user = integer(row.get("source_user_id"), "identity_source_id_invalid", minimum=1)
        require(integer(row.get("target_user_id"), "identity_target_id_invalid", minimum=1) == user, "identity_user_id_changed")
        account = integer(row.get("target_personal_account_id"), "identity_account_id_invalid", minimum=1)
        require(user not in result and account not in accounts, "identity_map_not_one_to_one")
        result[user] = account
        accounts.add(account)
    return result


def bind_identity_provenance(manifest: dict, *, map_file_sha256: str, source_archive_sha256: str) -> dict:
    require(type(manifest.get("format_version")) is int and manifest["format_version"] in (1, 2) and manifest.get("scope") == "identity-only", "identity_manifest_scope_invalid")
    require(manifest.get("source_archive_sha256") == source_archive_sha256, "identity_manifest_source_mismatch")
    require(isinstance(source_archive_sha256, str) and IDENTITY.HEX.fullmatch(source_archive_sha256) is not None, "source_hash_invalid")
    require(manifest.get("source_target_id_map_sha256") == map_file_sha256, "identity_map_file_hash_mismatch")
    mapping_hash = manifest.get("mapping_sha256")
    require(isinstance(mapping_hash, str) and IDENTITY.HEX.fullmatch(mapping_hash) is not None, "identity_mapping_hash_invalid")
    require(isinstance(map_file_sha256, str) and IDENTITY.HEX.fullmatch(map_file_sha256) is not None, "identity_map_file_hash_invalid")
    return {"source_archive_sha256": source_archive_sha256, "identity_mapping_sha256": mapping_hash,
            "identity_map_file_sha256": map_file_sha256, "identity_manifest_sha256": digest(manifest),
            "identity_manifest_format_version": manifest["format_version"],
            "identity_contract": manifest.get("identity_contract"),
            "expected_identity_manifest": manifest,
            "target_identity_import_and_native_accounts_not_yet_verified": True}


def verify_target_identity_binding(plan: dict, identities: dict[int, int], *, import_run: dict,
                                   native_users: list[dict]) -> dict:
    """Check values read from the target; a writer must repeat inside its TX.

    This pure comparator is not a database attestation. The eventual writer must
    obtain the rows itself over a receipt-verified target connection, lock them,
    and re-run this comparison within the opening transaction.
    """
    verify_plan_mapping(plan)
    proof = plan.get("identity_provenance")
    require(isinstance(proof, dict), "identity_provenance_missing")
    require(import_run.get("source_archive_sha256") == proof["source_archive_sha256"] and
            import_run.get("mapping_sha256") == proof["identity_mapping_sha256"] and
            import_run.get("manifest") == proof["expected_identity_manifest"], "target_identity_import_run_mismatch")
    native, accounts = {}, set()
    for row in native_users:
        require(isinstance(row, dict) and set(row) == {"id", "personal_account_id"}, "target_identity_row_shape_invalid")
        uid = integer(row["id"], "target_identity_user_id_invalid", minimum=1)
        aid = integer(row["personal_account_id"], "target_identity_account_id_invalid", minimum=1)
        require(uid not in native and aid not in accounts, "target_identity_not_one_to_one")
        native[uid] = aid
        accounts.add(aid)
    require(native == identities and digest(identities) == plan["identity_map_sha256"], "target_identity_account_map_mismatch")
    return {"identity_mapping_sha256": proof["identity_mapping_sha256"],
            "identity_manifest_sha256": proof["identity_manifest_sha256"],
            "native_identity_account_map_sha256": digest(native), "matched_user_count": len(native),
            "requires_same_transaction_target_receipt_verification_and_row_locks": True}


def map_wallet_positions(users: list[dict], identities: dict[int, int]) -> list[dict]:
    result, seen = [], set()
    for user in users:
        uid = integer(user.get("id"), "wallet_user_id_invalid", minimum=1)
        require(uid not in seen and uid in identities, "wallet_identity_missing_or_duplicate")
        seen.add(uid)
        result.append({"source_user_id": uid, "account_id": identities[uid], "unit": UNIT,
                       "signed_available_units": integer(user.get("quota"), "wallet_quota_invalid"),
                       "signed_affiliate_units": integer(user.get("aff_quota"), "affiliate_quota_invalid"),
                       "historical_used_units": integer(user.get("used_quota"), "wallet_used_invalid"),
                       "historical_affiliate_units": integer(user.get("aff_history"), "affiliate_history_invalid"),
                       "historical_request_count": integer(user.get("request_count"), "request_count_invalid"),
                       "source_status": user.get("status"), "source_deleted_at": user.get("deleted_at"),
                       "source_row_sha256": digest(user)})
    require(seen == set(identities), "wallet_identity_coverage_mismatch")
    return sorted(result, key=lambda row: row["source_user_id"])


def source_observation(receipt: dict) -> dict:
    """Bind a run to the dump window start without inventing a settlement time."""
    value = receipt.get("started_utc")
    require(isinstance(value, str), "source_capture_start_missing")
    try:
        instant = datetime.strptime(value, "%Y%m%dT%H%M%SZ").replace(tzinfo=timezone.utc)
    except ValueError:
        raise MappingError("source_capture_start_invalid") from None
    delta = instant - datetime(1970, 1, 1, tzinfo=timezone.utc)
    return {"observed_at": delta.days * 86400 + delta.seconds,
            "capture_window_started_at_iso": instant.isoformat(), "basis": "frozen_dump_capture_started_utc",
            "exact_financial_snapshot_instant_known": False,
            "must_not_be_used_as_original_payment_or_settlement_time": True}


def build_opening_payloads(plan: dict, *, migration_id: str, observation: dict,
                           reservation_reconciliation: dict) -> dict:
    """Pure adapter to the owner's offline import_opening_balance JSON contract.

    Reservation evidence is deliberately mandatory and complete. This function
    cannot infer held rights from wallet quota, or turn a pending transfer into
    an arbitrary reserved balance. It does not connect to a target or execute SQL.
    """
    verify_plan_mapping(plan)
    require(IDENTITY.MIGRATION_ID.fullmatch(migration_id) is not None, "migration_id_invalid")
    source_hash, mapping_hash = plan.get("source_archive_sha256"), plan.get("mapping_sha256")
    require(isinstance(source_hash, str) and IDENTITY.HEX.fullmatch(source_hash) is not None, "source_hash_invalid")
    require(isinstance(mapping_hash, str) and IDENTITY.HEX.fullmatch(mapping_hash) is not None, "mapping_hash_invalid")
    observed_at = integer(observation.get("observed_at"), "source_observation_epoch_invalid",
                          minimum=-62135596800, maximum=253402300799)
    require(observation.get("basis") == "frozen_dump_capture_started_utc" and
            observation.get("exact_financial_snapshot_instant_known") is False and
            observation.get("must_not_be_used_as_original_payment_or_settlement_time") is True,
            "source_observation_basis_unproven")
    try:
        instant = datetime.fromisoformat(observation.get("capture_window_started_at_iso"))
    except (TypeError, ValueError):
        raise MappingError("source_observation_invalid") from None
    require(instant.tzinfo is not None and instant.utcoffset().total_seconds() == 0, "source_observation_not_utc")
    delta = instant - datetime(1970, 1, 1, tzinfo=timezone.utc)
    require(instant.microsecond == 0 and delta.days * 86400 + delta.seconds == observed_at,
            "source_observation_epoch_iso_mismatch")
    require(reservation_reconciliation.get("source_archive_sha256") == source_hash and
            reservation_reconciliation.get("mapping_sha256") == mapping_hash,
            "reservation_reconciliation_source_mismatch")
    review_hash = reservation_reconciliation.get("reviewed_evidence_sha256")
    require(isinstance(review_hash, str) and IDENTITY.HEX.fullmatch(review_hash) is not None,
            "reservation_reconciliation_evidence_missing")
    rows = reservation_reconciliation.get("positions")
    require(isinstance(rows, list), "reservation_reconciliation_missing")
    reservations = {}
    for row in rows:
        require(isinstance(row, dict) and set(row) == {"source_user_id", "reserved_units"}, "reservation_position_shape_invalid")
        uid = integer(row["source_user_id"], "reservation_user_id_invalid", minimum=1)
        require(uid not in reservations, "reservation_user_duplicate")
        reservations[uid] = integer(row["reserved_units"], "reservation_units_invalid", minimum=0, maximum=JS_SAFE_MAX)
    wallets = plan.get("wallet_positions")
    require(isinstance(wallets, list), "opening_wallet_positions_missing")
    require(set(reservations) == {row["source_user_id"] for row in wallets}, "reservation_coverage_mismatch")
    require(not any(row.get("status") == "pending" for row in plan.get("wallet_transfers", [])),
            "pending_transfer_claim_contract_missing")
    payloads, seen_users, seen_accounts = [], set(), set()
    for row in wallets:
        uid = integer(row.get("source_user_id"), "opening_source_user_invalid", minimum=1)
        aid = integer(row.get("account_id"), "opening_account_invalid", minimum=1)
        require(uid not in seen_users and aid not in seen_accounts, "opening_duplicate_owner")
        seen_users.add(uid); seen_accounts.add(aid)
        row_hash = row.get("source_row_sha256")
        require(isinstance(row_hash, str) and IDENTITY.HEX.fullmatch(row_hash) is not None, "opening_source_row_hash_invalid")
        require(row.get("unit") == UNIT, "opening_unit_mismatch")
        payloads.append({"account_id": aid,
                         "affiliate_units": integer(row.get("signed_affiliate_units"), "opening_affiliate_out_of_range", minimum=-JS_SAFE_MAX, maximum=JS_SAFE_MAX),
                         "available_units": integer(row.get("signed_available_units"), "opening_wallet_out_of_range", minimum=-JS_SAFE_MAX, maximum=JS_SAFE_MAX),
                         "mapping_sha256": mapping_hash, "migration_id": migration_id,
                         "observed_at": observed_at, "reserved_units": reservations[uid],
                         "source_archive_sha256": source_hash, "source_entity_key": f"legacy.users/{uid}",
                         "source_row_sha256": row_hash})
    payloads.sort(key=lambda row: row["account_id"])
    try:
        opening_preflight = FINANCIAL_PREFLIGHT.preflight_opening_batch(payloads).summary()
    except FINANCIAL_PREFLIGHT.FinancialPreflightError as error:
        raise MappingError(str(error)) from None
    result = {"contract": "core_billing.import_opening_balance(jsonb)", "contract_review_status": "requires-final-core-schema-and-target-review",
              "migration_id": migration_id, "source_archive_sha256": source_hash, "mapping_sha256": mapping_hash,
              "observation": observation, "reservation_review_evidence_sha256": review_hash,
              "payloads": payloads, "target_writes": 0,
              "opening_preflight": opening_preflight,
              "historical_user_counters_imported_by_this_contract": False,
              "terminal_transfer_and_payment_basis_imported_by_this_contract": False}
    result["opening_payloads_sha256"] = digest(result)
    return result


def build_opening_counter_payloads(plan: dict, opening: dict) -> dict:
    """Historical counters share the exact user-row identity of the opening."""
    verify_plan_mapping(plan)
    require(digest({key: value for key, value in opening.items() if key != "opening_payloads_sha256"}) == opening.get("opening_payloads_sha256"),
            "opening_payload_hash_mismatch")
    require(opening.get("source_archive_sha256") == plan.get("source_archive_sha256") and
            opening.get("mapping_sha256") == plan.get("mapping_sha256"), "opening_counters_source_mismatch")
    bases = {}
    for row in opening.get("payloads", []):
        aid = integer(row.get("account_id"), "opening_account_invalid", minimum=1)
        require(aid not in bases, "opening_counters_account_duplicate")
        bases[aid] = row
    wallets = plan.get("wallet_positions")
    require(isinstance(wallets, list) and set(bases) == {row["account_id"] for row in wallets}, "opening_counters_coverage_mismatch")
    payloads = []
    for row in wallets:
        base = bases[row["account_id"]]
        require(base.get("source_row_sha256") == row["source_row_sha256"] and
                base.get("source_entity_key") == f"legacy.users/{row['source_user_id']}" and
                base.get("source_archive_sha256") == plan["source_archive_sha256"] and
                base.get("mapping_sha256") == plan["mapping_sha256"], "opening_counters_row_identity_mismatch")
        payloads.append({key: base[key] for key in ("account_id", "migration_id", "source_entity_key", "source_archive_sha256",
                                                   "mapping_sha256", "source_row_sha256", "observed_at")})
        payloads[-1].update(user_id=row["source_user_id"],
            used_quota=integer(row.get("historical_used_units"), "opening_counter_out_of_range", minimum=-JS_SAFE_MAX, maximum=JS_SAFE_MAX),
            request_count=integer(row.get("historical_request_count"), "opening_counter_out_of_range", minimum=-JS_SAFE_MAX, maximum=JS_SAFE_MAX),
            aff_history=integer(row.get("historical_affiliate_units"), "opening_counter_out_of_range", minimum=-JS_SAFE_MAX, maximum=JS_SAFE_MAX))
    payloads.sort(key=lambda row: row["account_id"])
    try:
        FINANCIAL_PREFLIGHT.validate_opening_counters(opening["payloads"], payloads)
    except FINANCIAL_PREFLIGHT.FinancialPreflightError as error:
        raise MappingError(str(error)) from None
    result = {"contract": "core_billing.import_account_opening_counters(jsonb)",
              "contract_review_status": "requires-final-core-schema-and-target-review", "payloads": payloads,
              "target_writes": 0, "wallet_or_affiliate_units_created_by_counters": 0,
              "opening_payloads_sha256": opening.get("opening_payloads_sha256")}
    result["counter_payloads_sha256"] = digest(result)
    return result


def map_key_limits(tokens: list[dict], identities: dict[int, int]) -> list[dict]:
    result, seen = [], set()
    for token in tokens:
        tid = integer(token.get("id"), "key_id_invalid", minimum=1)
        uid = integer(token.get("user_id"), "key_user_id_invalid", minimum=1)
        require(tid not in seen, "key_id_duplicate")
        seen.add(tid)
        used = integer(token.get("used_quota"), "key_used_invalid")
        remain = integer(token.get("remain_quota"), "key_remaining_invalid")
        unlimited = token.get("unlimited_quota")
        require(type(unlimited) is bool, "key_unlimited_policy_unknown")
        blockers = []
        if uid not in identities:
            blockers.append("orphan_key_owner")
        if used < 0:
            blockers.append("negative_key_used_counter")
        limit = None if unlimited else used + remain
        if limit is not None and not 0 <= limit <= BIGINT_MAX:
            blockers.append("key_lifetime_limit_out_of_range")
        restrictions = {field: token.get(field) for field in (
            "model_limits_enabled", "model_limits", "allow_ips", "group", "auto_groups",
            "cross_group_retry", "oauth_managed", "account_balance_read", "one_time_reveal", "creation_source")}
        require(type(restrictions["model_limits_enabled"]) is bool, "key_model_policy_unknown")
        result.append({"source_key_id": tid, "source_user_id": uid, "account_id": identities.get(uid),
                       "unlimited": unlimited, "remaining_units": remain, "used_units": used,
                       "lifetime_limit_units": limit, "expired_time": integer(token.get("expired_time"), "key_expiry_invalid"),
                       "source_status": token.get("status"), "source_deleted_at": token.get("deleted_at"),
                       "restrictions": restrictions, "credential_activation_allowed": False,
                       "source_row_sha256": digest(token), "blockers": blockers})
    return sorted(result, key=lambda row: row["source_key_id"])


def map_wallet_transfers(rows: list[dict], identities: dict[int, int]) -> list[dict]:
    result, seen = [], set()
    for row in rows:
        transfer_id = integer(row.get("id"), "transfer_id_invalid", minimum=1)
        sender = integer(row.get("sender_id"), "transfer_sender_id_invalid", minimum=1)
        recipient = integer(row.get("recipient_id"), "transfer_recipient_id_invalid", minimum=0)
        amount = integer(row.get("quota"), "transfer_amount_invalid", minimum=1)
        require(transfer_id not in seen, "transfer_id_duplicate")
        seen.add(transfer_id)
        status = row.get("status")
        blockers = []
        if status not in ("pending", "claimed", "cancelled"):
            blockers.append("unknown_transfer_status")
        if sender not in identities:
            blockers.append("orphan_transfer_sender")
        if recipient and recipient not in identities:
            blockers.append("orphan_transfer_recipient")
        if status == "claimed" and recipient == 0:
            blockers.append("claimed_transfer_recipient_missing")
        result.append({"source_transfer_id": transfer_id, "sender_user_id": sender,
                       "sender_account_id": identities.get(sender), "recipient_user_id": recipient,
                       "recipient_account_id": identities.get(recipient), "amount_units": amount,
                       "status": status, "claimable_escrow_units": amount if status == "pending" else 0,
                       "sender_available_already_debited": True, "wallet_debit_to_apply": 0,
                       "original_bearer_activation_allowed": False,
                       "created_at": integer(row.get("created_at"), "transfer_time_invalid"),
                       "claimed_at": integer(row.get("claimed_at"), "transfer_time_invalid"),
                       "cancelled_at": integer(row.get("cancelled_at"), "transfer_time_invalid"),
                       "source_row_sha256": digest(row), "blockers": blockers})
    return sorted(result, key=lambda row: row["source_transfer_id"])


def map_topups(rows: list[dict], identities: dict[int, int]) -> list[dict]:
    result, seen = [], set()
    credit_fields = ("credited_quota", "refunded_quota", "pending_credit_rebase_original_quota", "pending_credit_rebase_effective_quota")
    fiat_fields = ("expected_amount_micros", "settled_amount_micros", "refunded_amount_micros", "platform_amount_micros")
    for row in rows:
        topup_id = integer(row.get("id"), "topup_id_invalid", minimum=1)
        uid = integer(row.get("user_id"), "topup_user_id_invalid", minimum=1)
        require(topup_id not in seen, "topup_id_duplicate")
        seen.add(topup_id)
        blockers = []
        if uid not in identities:
            blockers.append("orphan_topup_owner")
        status = row.get("status")
        if status not in ("pending", "success", "failed", "expired"):
            blockers.append("unknown_topup_status")
        credits = {field: integer(row.get(field), "topup_credit_fact_invalid") for field in credit_fields}
        fiat = {field: None if row.get(field) is None else integer(row[field], "topup_fiat_fact_invalid") for field in fiat_fields}
        money = row.get("money")
        require(money is None or type(money) is int or isinstance(money, Decimal), "topup_binary_float_rejected")
        if status == "success" and credits["credited_quota"] == 0:
            blockers.append("completed_topup_credit_basis_unproven")
        result.append({"source_namespace": "legacy.top_ups", "source_topup_id": topup_id,
                       "source_user_id": uid, "account_id": identities.get(uid), "credit_facts": credits,
                       "fiat_facts": fiat, "legacy_money_numeric": money,
                       "legacy_deprecated_amount": row.get("amount"),
                       "settlement_currency": row.get("settlement_currency"),
                       "payment_method": row.get("payment_method"), "payment_provider": row.get("payment_provider"),
                       "provider_event_id": row.get("provider_event_id"),
                       "provider_transaction_id": row.get("provider_transaction_id"),
                       "trade_no": row.get("trade_no"), "status": status,
                       "terminal": status in ("success", "failed", "expired"),
                       "terminal_rule_source": "legacy common/constants.go TopUpStatus constants",
                       "created_at": integer(row.get("create_time"), "topup_time_invalid"),
                       "completed_at": integer(row.get("complete_time"), "topup_time_invalid"),
                       "discount_snapshot": {field: row.get(field) for field in ("discount_code_id", "discount_percent", "discount_qualifying_amount", "discount_qualifying_unit")},
                       "pending_credit_rebase_key": row.get("pending_credit_rebase_key"),
                       "historical_wallet_credit_to_apply": 0, "future_refund_authorized": False,
                       "source_row_sha256": digest(row), "blockers": blockers})
    return sorted(result, key=lambda row: row["source_topup_id"])


def rebased_refund_target(quota: int, paid_micros: int, refunded_micros: int) -> int:
    """Legacy cumulative half-up integer target, never a current-FX conversion."""
    integer(quota, "refund_quota_invalid", minimum=0)
    integer(paid_micros, "refund_paid_invalid", minimum=0)
    integer(refunded_micros, "refund_refunded_invalid", minimum=0)
    if quota == 0 or paid_micros == 0 or refunded_micros == 0:
        return 0
    if refunded_micros >= paid_micros:
        return quota
    quotient, remainder = divmod(quota * refunded_micros, paid_micros)
    return quotient + (2 * remainder >= paid_micros)


def map_refund_lineage(tables: dict[str, list[dict]], identities: dict[int, int]) -> dict:
    """Keep durable rebase plans and original economic dimensions separate.

    The existing normalizedTopUpCreditedQuota/expectedTopUpAmountMicros authority
    has provider-specific legacy and historical float semantics. This planner
    does not invent that projection, issue a refund, or re-post historical funds.
    """
    topups = {integer(row.get("id"), "topup_id_invalid", minimum=1): row for row in tables["top_ups"]}
    require(len(topups) == len(tables["top_ups"]), "topup_id_duplicate")
    children, audits, seen_runs = {}, [], set()
    for row in tables.get("wallet_topup_credit_rebases", []):
        tid = integer(row.get("top_up_id"), "refund_basis_topup_id_invalid", minimum=1)
        require(tid not in children, "refund_basis_topup_duplicate")
        uid = integer(row.get("user_id"), "refund_basis_user_id_invalid", minimum=1)
        migration_id = row.get("migration_id")
        require(isinstance(migration_id, str) and migration_id != "", "refund_basis_migration_id_missing")
        fields = {field: integer(row.get(field), "refund_basis_integer_invalid") for field in (
            "original_credited_quota", "original_refunded_quota", "original_refunded_amount_micros",
            "original_paid_amount_micros", "refundable_quota", "rebased_debited_quota")}
        blockers = []
        source = topups.get(tid)
        if source is None:
            blockers.append("orphan_refund_basis_topup")
        if uid not in identities or (source is not None and source["user_id"] != uid):
            blockers.append("refund_basis_owner_mismatch")
        if any(value < 0 for value in fields.values()) or fields["rebased_debited_quota"] > fields["refundable_quota"] or fields["original_refunded_quota"] > fields["original_credited_quota"] or fields["original_refunded_amount_micros"] > fields["original_paid_amount_micros"] or fields["original_paid_amount_micros"] == 0:
            blockers.append("refund_basis_counter_relationship_invalid")
        rights_exhausted = fields["original_paid_amount_micros"] > 0 and fields["original_refunded_amount_micros"] == fields["original_paid_amount_micros"]
        target = None
        if source is not None and not blockers:
            already = integer(source.get("refunded_amount_micros"), "refund_source_counter_invalid") - fields["original_refunded_amount_micros"]
            remaining_paid = fields["original_paid_amount_micros"] - fields["original_refunded_amount_micros"]
            if already < 0 or already > remaining_paid:
                blockers.append("refund_basis_source_fiat_counter_mismatch")
            elif not rights_exhausted:
                target = rebased_refund_target(fields["refundable_quota"], remaining_paid, already)
                if target != fields["rebased_debited_quota"]:
                    blockers.append("refund_basis_rebased_debit_mismatch")
        children[tid] = {"source_topup_id": tid, "source_user_id": uid, "account_id": identities.get(uid),
                         "source_migration_id": migration_id, "credit_unit": UNIT,
                         "fiat_unit": "original-payment-micros-currency-not-guessed", **fields,
                         "source_row_sha256": digest(row), "recomputed_cumulative_rebased_target_units": target,
                         "original_refund_rights_exhausted_at_basis": rights_exhausted,
                         "provider_normalized_credit_and_paid_authority_not_yet_verified": True,
                         "future_refund_authorized": False, "blockers": blockers}
    for row in tables.get("wallet_credit_rebases", []):
        mid, raw = row.get("migration_id"), row.get("plan")
        require(isinstance(mid, str) and mid != "" and mid not in seen_runs, "wallet_rebase_migration_id_invalid")
        seen_runs.add(mid)
        require(isinstance(raw, (str, dict)), "wallet_rebase_plan_shape_invalid")
        parsed = parse_json(raw) if isinstance(raw, str) else raw
        require(isinstance(parsed, dict), "wallet_rebase_plan_shape_invalid")
        declared = row.get("plan_sha256")
        require(isinstance(declared, str) and IDENTITY.HEX.fullmatch(declared) is not None, "wallet_rebase_plan_hash_invalid")
        # JSONB normalizes object order/whitespace before this snapshot exists.
        # Its declared historical hash is a source fact, not a digest of our
        # canonical encoding. Never claim the unavailable pre-JSONB bytes match.
        actual = hashlib.sha256(raw.encode()).hexdigest() if isinstance(raw, str) else None
        users = parsed.get("user_ids", [])
        require(isinstance(users, list), "wallet_rebase_user_ids_invalid")
        affected = {integer(uid, "wallet_rebase_user_id_invalid", minimum=1) for uid in users}
        require(len(affected) == len(users), "wallet_rebase_user_id_duplicate")
        refs = {}
        for key, id_field in (("refund_bases", "top_up_id"), ("noncash_topups", "id"),
                              ("subscription_refund_bases", "subscription_order_id"), ("subscriptions", "id")):
            rows = parsed.get(key)
            require(rows is None or isinstance(rows, list), "wallet_rebase_reference_shape_invalid")
            ids = [] if rows is None else [integer(ref.get(id_field), "wallet_rebase_reference_id_invalid", minimum=1) for ref in rows]
            require(len(set(ids)) == len(ids), "wallet_rebase_reference_id_duplicate")
            refs[key] = None if rows is None else ids
        audit_blockers = [] if actual is None or declared == actual else ["wallet_rebase_raw_plan_hash_mismatch"]
        missing = [tid for tid in refs["refund_bases"] or [] if tid not in children]
        if missing:
            audit_blockers.append("wallet_rebase_refund_child_missing")
        for tid in refs["refund_bases"] or []:
            if tid in children and children[tid]["source_migration_id"] != mid:
                audit_blockers.append("wallet_rebase_refund_child_run_mismatch")
        audits.append({"source_migration_id": mid, "declared_plan_sha256": declared,
                       "raw_plan_sha256": actual, "source_row_sha256": digest(row),
                       "source_plan_semantic_sha256": digest(parsed),
                       "declared_plan_hash_validation": "exact-source-text-verified" if actual == declared else
                           ("jsonb-original-serialization-unavailable-declared-hash-preserved-not-reverified" if actual is None else "source-text-hash-mismatch"),
                       "affected_source_user_ids": sorted(affected), "references": refs,
                       "full_plan_unknown_fields_preserved": parsed,
                       "missing_refund_child_source_topup_ids": missing, "blockers": sorted(set(audit_blockers))})
    eligibility = []
    for tid, source in sorted(topups.items()):
        blockers, referenced, noncash = [], [], False
        for audit in audits:
            refs = audit["references"]
            affected = source["user_id"] in audit["affected_source_user_ids"]
            included = tid in (refs["refund_bases"] or [])
            noncash_here = tid in (refs["noncash_topups"] or [])
            noncash = noncash or noncash_here
            if affected or included or noncash_here:
                referenced.append(audit["source_row_sha256"])
            if affected and refs["refund_bases"] is None:
                blockers.append("wallet_rebase_affected_user_refund_bases_missing")
            if included and tid not in children:
                blockers.append("wallet_rebase_refund_child_missing")
        if noncash:
            blockers.append("historical_noncash_topup_not_refundable")
        eligibility.append({"source_topup_id": tid, "source_row_sha256": digest(source),
                            "wallet_rebase_audit_row_hashes": referenced, "refund_basis_child_present": tid in children,
                            "historical_noncash": noncash, "future_refund_authorized": False,
                            "blockers": sorted(set(blockers))})
    subscription_bases = []
    seen_orders = set()
    for row in tables.get("subscription_order_credit_rebases", []):
        oid = integer(row.get("subscription_order_id"), "subscription_refund_order_id_invalid", minimum=1)
        require(oid not in seen_orders, "subscription_refund_order_duplicate")
        seen_orders.add(oid)
        fields = {key: integer(value, "subscription_refund_integer_invalid") for key, value in row.items() if key != "migration_id"}
        require(isinstance(row.get("migration_id"), str) and row["migration_id"], "subscription_refund_migration_id_missing")
        subscription_bases.append({"source_fields": fields, "source_migration_id": row["migration_id"],
                                   "source_row_sha256": digest(row), "future_refund_authorized": False,
                                   "source_period_and_quota_generation_must_be_retained": True})
    return {"wallet_refund_children": [children[tid] for tid in sorted(children)],
            "wallet_rebase_audits": audits, "topup_refund_lineage": eligibility,
            "subscription_refund_children": subscription_bases,
            "future_refunds_enabled": False,
            "legacy_provider_credit_and_paid_amount_projection_requires_reviewed_contract": True}


def map_subscriptions(rows: list[dict], plans: list[dict], identities: dict[int, int], source_timezone: str | None) -> list[dict]:
    if source_timezone:
        try:
            ZoneInfo(source_timezone)
        except (ValueError, ZoneInfoNotFoundError):
            raise MappingError("source_timezone_invalid") from None
    plan_index = {}
    for plan in plans:
        pid = integer(plan.get("id"), "subscription_plan_id_invalid", minimum=1)
        require(pid not in plan_index, "subscription_plan_id_duplicate")
        plan_index[pid] = plan
    result, seen = [], set()
    for row in rows:
        sid = integer(row.get("id"), "subscription_id_invalid", minimum=1)
        uid = integer(row.get("user_id"), "subscription_user_id_invalid", minimum=1)
        pid = integer(row.get("plan_id"), "subscription_plan_id_invalid", minimum=1)
        require(sid not in seen, "subscription_id_duplicate")
        seen.add(sid)
        total = integer(row.get("amount_total"), "subscription_total_invalid")
        used = integer(row.get("amount_used"), "subscription_used_invalid")
        optional = {key: None if row.get(key) is None else integer(row[key], "subscription_nullable_grant_invalid") for key in ("reset_amount", "renewal_amount")}
        # This is the legacy hasFiniteQuota implementation, not a guessed target cap.
        unlimited = not (total > 0 or optional["reset_amount"] is not None or optional["renewal_amount"] is not None)
        plan = plan_index.get(pid)
        blockers = []
        if uid not in identities:
            blockers.append("orphan_subscription_owner")
        if plan is None:
            blockers.append("orphan_subscription_plan")
        if min(total, used, *(value for value in optional.values() if value is not None)) < 0:
            blockers.append("negative_subscription_counter")
        if row.get("status") not in ("active", "expired", "cancelled"):
            blockers.append("unknown_subscription_status")
        reset_period = plan.get("quota_reset_period") if plan else None
        if reset_period in ("daily", "weekly", "monthly") and not source_timezone:
            blockers.append("source_calendar_timezone_unproven")
        facts = {key: integer(row.get(key), "subscription_time_or_generation_invalid") for key in (
            "quota_version", "start_time", "end_time", "last_reset_time", "next_reset_time")}
        result.append({"source_subscription_id": sid, "source_user_id": uid, "account_id": identities.get(uid),
                       "source_plan_id": pid, "unlimited": unlimited, "current_limit_units": None if unlimited else total,
                       "original_amount_total": total, "opening_used_units": used,
                       "signed_remaining_units": None if unlimited else total - used,
                       "spendable_remaining_units": None if unlimited else max(total - used, 0), **optional, **facts,
                       "status": row.get("status"), "source": row.get("source"),
                       "calendar_period": reset_period, "source_timezone": source_timezone,
                       "reset_custom_seconds": plan.get("quota_reset_custom_seconds") if plan else None,
                       "upgrade_group": row.get("upgrade_group"), "previous_group": row.get("prev_user_group"),
                       "downgrade_group": row.get("downgrade_group"), "allow_wallet_overflow": row.get("allow_wallet_overflow"),
                       "source_row_sha256": digest(row), "plan_row_sha256": digest(plan) if plan else None,
                       "blockers": blockers})
    return sorted(result, key=lambda row: row["source_subscription_id"])


def classify_economic_states(tables: dict[str, list[dict]]) -> dict:
    """Private typed state evidence, never a guessed zero-reservation rule."""
    states = {}
    for table, fields in ECONOMIC_STATE_FIELDS.items():
        rows = tables.get(table)
        if rows is None:
            continue
        field_counts = {}
        for field in fields:
            counts = Counter()
            for row in rows:
                require(field in row, "economic_state_column_missing")
                value = row[field]
                require(value is None or type(value) in (str, int, bool), "economic_state_shape_invalid")
                counts[canonical(value)] += 1
            field_counts[field] = dict(sorted(counts.items()))
        states[table] = {"row_count": len(rows), "state_counts": field_counts,
                         "source_rows_sha256": digest(rows)}
    bounty_rows = tables.get("open_source_bounty_projects", [])
    for row in bounty_rows:
        integer(row.get("escrow_quota"), "bounty_escrow_fact_invalid")
    return {"status": "requires-kind-aware-source-model-review-before-reserved-opening",
            "table_states": states,
            "bounty_nonzero_escrow_row_count": sum(row["escrow_quota"] != 0 for row in bounty_rows),
            "discount_capacity_is_not_wallet_credit_reserve": True,
            "pending_unpaid_topup_is_not_wallet_credit_reserve": True,
            "reserved_units_not_inferred_from_these_counts": True}


def primary_keys_from_catalog(constraints: list[dict], columns: dict[str, set[str]], schema: str) -> dict[str, list[str]]:
    result = {}
    for row in constraints:
        if row.get("contype") != "p":
            continue
        require(row.get("schema") == schema and row.get("table_name") in columns, "source_primary_key_schema_invalid")
        match = re.fullmatch(r"PRIMARY KEY \((.+)\)", row.get("definition", ""))
        require(match is not None, "source_primary_key_definition_unsupported")
        fields = [field.strip().removeprefix('"').removesuffix('"') for field in match.group(1).split(",")]
        require(all(IDENTITY.IDENTIFIER.fullmatch(field) is not None and field in columns[row["table_name"]] for field in fields), "source_primary_key_column_invalid")
        require(len(set(fields)) == len(fields) and row["table_name"] not in result, "source_primary_key_duplicate")
        result[row["table_name"]] = fields
    return result


def plan_financial(tables: dict[str, list[dict]], identities: dict[int, int], *, source_sha256: str,
                   source_timezone: str | None = None, identity_provenance: dict | None = None,
                   primary_keys: dict | None = None, column_schema: dict | None = None) -> dict:
    require(IDENTITY.HEX.fullmatch(source_sha256) is not None, "source_hash_invalid")
    require(REQUIRED_TABLES <= set(tables), "required_financial_source_table_missing")
    evidence, families = {}, {}
    for table, rows in sorted(tables.items()):
        require(IDENTITY.IDENTIFIER.fullmatch(table) is not None and isinstance(rows, list), "source_table_shape_invalid")
        evidence[table] = {"row_count": len(rows), "row_hashes": [digest(row) for row in rows],
                           "families": table_families(table), "unknown_columns_preserved_in_private_raw_export": True,
                           "disposition": "disabled-auxiliary-credential-private-archive" if table in AUXILIARY_CREDENTIAL_TABLES else "typed-financial-fact-or-runtime-contract-pending"}
        if primary_keys is not None:
            fields = primary_keys.get(table)
            require(fields is None or (isinstance(fields, list) and len(fields) > 0), "primary_key_contract_invalid")
            records, seen_keys = [], set()
            for ordinal, row in enumerate(rows):
                require(fields is None or all(field in row and row[field] is not None for field in fields), "source_primary_key_value_missing")
                key = {field: row[field] for field in fields} if fields else None
                if key is not None:
                    require(canonical(key) not in seen_keys, "source_primary_key_value_duplicate")
                    seen_keys.add(canonical(key))
                records.append({"source_primary_key": key, "source_export_ordinal": ordinal, "source_row_sha256": digest(row)})
            evidence[table].update(primary_key_columns=fields, source_records=records,
                                   primary_key_missing_no_synthetic_id_created=fields is None)
        if column_schema is not None:
            require(table in column_schema, "source_column_schema_missing")
            evidence[table]["source_column_schema"] = column_schema[table]
    for family, patterns in FAMILIES.items():
        source_tables = [table for table in tables if any(matches(table, pattern) for pattern in patterns)]
        count = sum(len(tables[table]) for table in source_tables)
        families[family] = {"source_tables": sorted(source_tables), "source_row_count": count,
                            "status": "requires-reviewed-runtime-import-contract" if count else "no-source-rows",
                            "active_rows_imported": 0}
    wallets = map_wallet_positions(tables["users"], identities)
    keys = map_key_limits(tables["tokens"], identities)
    subscriptions = map_subscriptions(tables["user_subscriptions"], tables["subscription_plans"], identities, source_timezone)
    transfers = map_wallet_transfers(tables.get("wallet_transfers", []), identities)
    topups = map_topups(tables["top_ups"], identities)
    refund_lineage = map_refund_lineage(tables, identities)
    unknown = sorted(table for table in tables if FINANCIAL_NAME.search(table) and not table_families(table) and table not in AUXILIARY_CREDENTIAL_TABLES)
    blockers = Counter(reason for row in keys + subscriptions + transfers + topups for reason in row["blockers"])
    blockers.update(reason for row in refund_lineage["wallet_refund_children"] + refund_lineage["wallet_rebase_audits"] + refund_lineage["topup_refund_lineage"] for reason in row["blockers"])
    if unknown:
        blockers["unclassified_financial_source_table"] += len(unknown)
    if any(row["signed_available_units"] < 0 for row in wallets):
        blockers["requires_signed_wallet_debt_contract"] += sum(row["signed_available_units"] < 0 for row in wallets)
    if any(row["signed_affiliate_units"] != 0 for row in wallets):
        blockers["requires_independent_affiliate_claim_contract"] += sum(row["signed_affiliate_units"] != 0 for row in wallets)
    data = {"format_version": 1, "digest_format": DIGEST_FORMAT, "mapping_digest_domain": MAPPING_DIGEST_DOMAIN,
            "decimal_artifact_display_encoding": "decimal-tags-are-display-only-original-types-bound-by-private-raw-export-and-typed-source-row-digests",
            "source_archive_sha256": source_sha256, "unit": UNIT,
            "credits_per_usd": 500000, "source_timezone": source_timezone,
            "source_evidence": evidence, "families": families, "wallet_positions": wallets,
            "key_limits": keys, "subscription_entitlements": subscriptions, "wallet_transfers": transfers,
            "topup_history": topups,
            "refund_lineage": refund_lineage,
            "economic_activity_classification": classify_economic_states(tables),
            "identity_map_sha256": digest(identities), "identity_provenance": identity_provenance,
            "unclassified_financial_tables": unknown,
            "blocker_counts": dict(sorted(blockers.items()))}
    # Hash the persisted JSON material so an independent readback can verify it.
    # Decimal display tags must never be heuristically decoded: literal JSONB
    # objects may use the same spelling. Original types are instead proven by
    # the untouched raw export and each type-tagged source row digest above.
    data = exact(data)
    data["mapping_sha256"] = digest(data)
    data["public_summary"] = {"status": "financial-typed-dryrun-no-target-writes", "family_count": len(FAMILIES),
                              "source_table_count": len(tables), "source_row_count": sum(len(rows) for rows in tables.values()),
                              "wallet_position_count": len(wallets), "key_policy_count": len(keys),
                              "subscription_count": len(subscriptions), "family_counts_overlap_do_not_sum": True,
                              "wallet_transfer_count": len(transfers), "topup_count": len(topups),
                              "active_financial_rows_imported": 0, "original_credentials_activated": 0,
                              "source_archive_sha256": source_sha256, "mapping_sha256": data["mapping_sha256"],
                              "blocker_counts": data["blocker_counts"], "unclassified_financial_table_count": len(unknown)}
    return data


def verify_plan_mapping(plan: dict) -> None:
    require(plan.get("digest_format") == DIGEST_FORMAT and plan.get("mapping_digest_domain") == MAPPING_DIGEST_DOMAIN,
            "financial_plan_hash_domain_unknown")
    material = {key: value for key, value in plan.items() if key not in ("mapping_sha256", "public_summary")}
    require(digest(material) == plan.get("mapping_sha256"), "financial_plan_mapping_hash_mismatch")
    require(plan.get("public_summary", {}).get("mapping_sha256") == plan.get("mapping_sha256"),
            "financial_plan_summary_hash_mismatch")


def tool_source_hashes() -> dict:
    return {"importer_sha256": IDENTITY.sha256_file(Path(__file__)),
            "identity_helper_sha256": IDENTITY.sha256_file(Path(IDENTITY.__file__)),
            "financial_preflight_helper_sha256": IDENTITY.sha256_file(Path(FINANCIAL_PREFLIGHT.__file__))}


def verify_tool_source_hashes(expected: dict) -> None:
    require(tool_source_hashes() == expected, "financial_tool_source_changed_during_run")


def metadata(receipt: dict):
    paths = [IDENTITY.private_backup_path(Path(path)) for path in receipt.get("private_metadata_files", [])]
    catalog = [path for path in paths if path.name == "native-catalog-metadata.jsonl"]
    baselines = [path for path in paths if path.name == "native-table-row-count-baseline.jsonl"]
    require(len(catalog) == len(baselines) == 1, "source_catalog_or_baseline_missing")
    pieces = [parse_json(line) for line in catalog[0].read_text().splitlines() if line.startswith(("{", "["))]
    require(len(pieces) == 7 and isinstance(pieces[2], list), "source_catalog_framing_invalid")
    columns = {}
    for row in pieces[2]:
        require(row["schema"] == receipt["source"]["schema"], "source_catalog_schema_mismatch")
        columns.setdefault(row["table_name"], set()).add(row["column_name"])
    counts = {}
    for line in baselines[0].read_text().splitlines():
        if line.startswith("{"):
            row = parse_json(line)
            require(row["table"] not in counts, "duplicate_source_baseline_table")
            counts[row["table"]] = integer(row["row_count"], "source_baseline_count_invalid", minimum=0)
    require(set(columns) == set(counts), "source_baseline_catalog_coverage_mismatch")
    primary_keys = primary_keys_from_catalog(pieces[3], columns, receipt["source"]["schema"])
    column_schema = {}
    for row in pieces[2]:
        column_schema.setdefault(row["table_name"], []).append(row)
    return columns, counts, {path.name: IDENTITY.sha256_file(path) for path in (catalog[0], baselines[0])}, primary_keys, column_schema


def read_frozen_export(raw_path: Path, receipt: dict, columns: dict, baseline: dict,
                       selected: list[str], expected_identity: dict) -> dict:
    tables = {table: [] for table in selected}
    identity_count = schema_count = 0
    with raw_path.open() as stream:
        for line in stream:
            tagged = parse_json(line)
            kind, row = tagged.get("kind"), tagged.get("row")
            if kind == "identity":
                identity_count += 1
                IDENTITY.verify_instance(row, receipt["readonly_reader"], receipt.get("source_instance_identity", {}), source=True)
                require(row == expected_identity, "source_instance_changed_during_export")
            elif kind == "schema":
                schema_count += 1
                require(row.get("schema") == receipt["source"]["schema"] and row.get("schema_owner") == receipt["source_instance_identity"].get("schema_owner"), "source_schema_owner_mismatch")
            else:
                table = tagged.get("table")
                require(kind == "row" and table in tables and isinstance(row, dict), "source_export_row_shape_invalid")
                require(set(row) == columns[table], "source_column_coverage_mismatch")
                tables[table].append(row)
    require(identity_count == schema_count == 1, "source_export_identity_framing_invalid")
    require(all(len(rows) == baseline[table] for table, rows in tables.items()), "source_snapshot_row_baseline_mismatch")
    return tables


def reuse_frozen_export(receipt: dict, proof_path: Path, output: Path):
    """Replan a prior, hash-bound private export without querying either DB."""
    require(receipt.get("status") == "snapshot-restored-baseline-verified-frozen-and-classified", "source_receipt_status_invalid")
    proof_path = IDENTITY.private_backup_path(proof_path)
    raw_path = IDENTITY.private_backup_path(proof_path.with_name("source-financial-export.private.jsonl"))
    proof = parse_json(proof_path.read_text())
    archive = receipt["archive"]
    archive_path = IDENTITY.private_backup_path(Path(archive["path"]))
    require(IDENTITY.sha256_file(archive_path) == archive["sha256"] == proof.get("source_archive_sha256"), "source_archive_hash_mismatch")
    raw_hash = IDENTITY.sha256_file(raw_path)
    require(raw_hash == proof.get("source_export_sha256"), "source_export_hash_mismatch")
    columns, baseline, metadata_hashes, primary_keys, column_schema = metadata(receipt)
    require(proof.get("metadata_hashes") == metadata_hashes, "source_export_metadata_hash_mismatch")
    require(proof.get("source_column_coverage_exact") is True and proof.get("source_row_baseline_exact") is True,
            "source_export_baseline_unproven")
    selected = sorted(table for table in columns if table_families(table) or FINANCIAL_NAME.search(table))
    require(proof.get("selected_table_counts") == {table: baseline[table] for table in selected}, "source_export_table_selection_mismatch")
    tables = read_frozen_export(raw_path, receipt, columns, baseline, selected, proof["source_instance"])
    require(IDENTITY.sha256_file(raw_path) == raw_hash and IDENTITY.sha256_file(archive_path) == archive["sha256"],
            "source_artifact_changed_during_replan")
    proof = {**proof, "source_primary_keys": {table: primary_keys.get(table) for table in selected},
             "source_column_schema": {table: column_schema[table] for table in selected},
             "reused_source_export_path": str(raw_path), "reused_source_export_proof_sha256": IDENTITY.sha256_file(proof_path),
             "source_queries_this_run": 0}
    IDENTITY.write_private(output, "source-export-proof.private.json", canonical(proof) + "\n")
    return tables, proof


def export_source(receipt: dict, output: Path):
    require(receipt.get("status") == "snapshot-restored-baseline-verified-frozen-and-classified", "source_receipt_status_invalid")
    archive = receipt.get("archive", {})
    archive_path = IDENTITY.private_backup_path(Path(archive.get("path", "")))
    require(IDENTITY.sha256_file(archive_path) == archive.get("sha256"), "source_archive_hash_mismatch")
    reader = receipt.get("readonly_reader", {})
    require(all(reader.get(key) is True for key in ("verified_no_application_write_privileges", "verified_no_security_definer_application_function_execute", "row_security_off_guard_verified")), "source_reader_not_verified")
    schema = receipt.get("source", {}).get("schema", "")
    require(IDENTITY.IDENTIFIER.fullmatch(schema) is not None, "source_schema_invalid")
    columns, baseline, metadata_hashes, primary_keys, column_schema = metadata(receipt)
    selected = sorted(table for table in columns if table_families(table) or FINANCIAL_NAME.search(table))
    require(REQUIRED_TABLES <= set(selected), "required_financial_source_table_missing")
    identity = parse_json(IDENTITY.psql(reader, "BEGIN READ ONLY;\n" + IDENTITY.IDENTITY_SQL + "\nCOMMIT;", readonly=True, private_dir=output, label="source-preflight"))
    IDENTITY.verify_instance(identity, reader, receipt.get("source_instance_identity", {}), source=True)
    sql = "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;\nSET LOCAL row_security=off;\n"
    sql += "SELECT json_build_object('kind','identity','row',(" + IDENTITY.IDENTITY_SQL.removesuffix(";").removeprefix("SELECT ") + "));\n"
    sql += f"SELECT json_build_object('kind','schema','row',json_build_object('schema',nspname,'schema_owner',pg_get_userbyid(nspowner))) FROM pg_namespace WHERE nspname={IDENTITY.sql_literal(schema)};\n"
    for table in selected:
        require(IDENTITY.IDENTIFIER.fullmatch(table) is not None, "source_table_identifier_invalid")
        sql += f"SELECT json_build_object('kind','row','table',{IDENTITY.sql_literal(table)},'row',row_to_json(r)) FROM \"{schema}\".\"{table}\" r ORDER BY row_to_json(r)::text;\n"
    sql += "COMMIT;\n"
    IDENTITY.write_private(output, "source-export.private.sql", sql)
    raw_path, err_path = output / "source-financial-export.private.jsonl", output / "source-export.stderr.private.txt"
    environment = {key: value for key, value in os.environ.items() if not key.startswith("PG")}
    environment.update(IDENTITY.socket_connection(reader))
    environment.update(PGCONNECT_TIMEOUT="5", PGAPPNAME="lmm-offline-financial-planner", PGOPTIONS="-c default_transaction_read_only=on")
    with os.fdopen(os.open(raw_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600), "w") as raw, os.fdopen(os.open(err_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600), "w") as error:
        result = subprocess.run(["psql", "-X", "--no-password", "-qAt", "-v", "ON_ERROR_STOP=1"], input=sql, text=True, stdout=raw, stderr=error, env=environment, timeout=180, check=False)
        raw.flush(); os.fsync(raw.fileno()); error.flush(); os.fsync(error.fileno())
    require(result.returncode == 0, "source_financial_export_failed")
    tables = read_frozen_export(raw_path, receipt, columns, baseline, selected, identity)
    require(IDENTITY.sha256_file(archive_path) == archive["sha256"], "source_archive_changed_during_export")
    proof = {"source_archive_sha256": archive["sha256"], "source_export_sha256": IDENTITY.sha256_file(raw_path),
             "metadata_hashes": metadata_hashes, "selected_table_counts": {table: len(rows) for table, rows in tables.items()},
             "source_column_coverage_exact": True, "source_row_baseline_exact": True,
             "source_primary_keys": {table: primary_keys.get(table) for table in selected},
             "source_column_schema": {table: column_schema[table] for table in selected},
             "all_source_rows_retained_in_immutable_archive": True, "other_source_tables_not_in_financial_export": sorted(set(columns) - set(tables)),
             "source_instance": identity}
    IDENTITY.write_private(output, "source-export-proof.private.json", canonical(proof) + "\n")
    return tables, proof


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-receipt", type=Path, required=True)
    parser.add_argument("--identity-map", type=Path, required=True, help="Private source-user/target-user/personal-account JSONL from the reviewed identity plan")
    parser.add_argument("--identity-manifest", type=Path, required=True, help="Reviewed identity-only manifest binding this ID-map file to the same source archive")
    parser.add_argument("--source-timezone", help="Proven legacy application IANA timezone; never inferred from the database timezone")
    parser.add_argument("--reuse-export-proof", type=Path, help="Replan a previous owned private export after exact hash/catalog/count checks, without source queries")
    parser.add_argument("--output", type=Path, required=True, help="New 0700 directory under ~/Backups")
    parser.add_argument("--migration-id", required=True)
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    output = None
    try:
        require(not args.apply, "financial_apply_contract_not_frozen")
        require(IDENTITY.MIGRATION_ID.fullmatch(args.migration_id) is not None, "migration_id_invalid")
        tool_hashes = tool_source_hashes()
        IDENTITY.private_backup_path(args.output.parent, directory=True)
        args.output.mkdir(mode=0o700)
        output = IDENTITY.private_backup_path(args.output, directory=True)
        source = IDENTITY.load_receipt(args.source_receipt)
        map_path = IDENTITY.private_backup_path(args.identity_map)
        identity_manifest_path = IDENTITY.private_backup_path(args.identity_manifest)
        identity_manifest = parse_json(identity_manifest_path.read_text())
        identity_provenance = bind_identity_provenance(identity_manifest, map_file_sha256=IDENTITY.sha256_file(map_path),
                                                      source_archive_sha256=source.get("archive", {}).get("sha256"))
        identities = identity_index([parse_json(line) for line in map_path.read_text().splitlines() if line])
        tables, proof = (reuse_frozen_export(source, args.reuse_export_proof, output) if args.reuse_export_proof
                         else export_source(source, output))
        plan = plan_financial(tables, identities, source_sha256=proof["source_archive_sha256"], source_timezone=args.source_timezone,
                              identity_provenance=identity_provenance, primary_keys=proof["source_primary_keys"],
                              column_schema=proof["source_column_schema"])
        plan_path = IDENTITY.write_private(output, "financial-plan.private.json", canonical(plan) + "\n")
        verify_plan_mapping(parse_json(plan_path.read_text()))
        verify_tool_source_hashes(tool_hashes)
        manifest = {"format_version": 1, "digest_format": DIGEST_FORMAT, "scope": "financial-typed-planner-only", "migration_id": args.migration_id,
                    "source_archive_sha256": proof["source_archive_sha256"], "source_export_sha256": proof["source_export_sha256"],
                    "identity_map_file_sha256": IDENTITY.sha256_file(map_path), "mapping_sha256": plan["mapping_sha256"],
                    "financial_plan_file_sha256": IDENTITY.sha256_file(plan_path), "mapping_digest_domain": MAPPING_DIGEST_DOMAIN,
                    "identity_manifest_file_sha256": IDENTITY.sha256_file(identity_manifest_path),
                    "identity_mapping_sha256": identity_provenance["identity_mapping_sha256"],
                    "source_observation": source_observation(source),
                    **tool_hashes, "tool_source_hashes_unchanged_during_run": True,
                    "target_writes": 0, "target_runtime_contract_review_required": True}
        IDENTITY.write_private(output, "manifest.private.json", canonical(manifest) + "\n")
        IDENTITY.write_private(output, "report.private.json", canonical(plan["public_summary"]) + "\n")
        print(canonical(plan["public_summary"]))
        return 0
    except (MappingError, OSError, ValueError, KeyError, TypeError, subprocess.TimeoutExpired) as error:
        code = str(error) if isinstance(error, MappingError) else type(error).__name__
        result = {"status": "financial-planner-aborted-no-target-writes", "error_code": code}
        if output:
            try:
                IDENTITY.write_private(output, "failure.private.json", canonical(result) + "\n")
            except OSError:
                pass
        print(canonical(result), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
