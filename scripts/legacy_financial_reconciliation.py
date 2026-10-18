"""Offline source facts for review, without inferring opening reservations.

The caller supplies an already verified frozen export and identity map. This
module has no connection, write-to-target, approval or opening-payload API.
All amounts below remain source facts: matching a state or reference does not
prove that the deployed application followed a particular economic policy.
"""

from collections import Counter
import hashlib
import importlib.util
from pathlib import Path


# Explicit projection, rather than a name heuristic that could copy secrets.
# (state fields, integer credit fields, other integer amounts, user references,
#  table references). Other source columns stay in the immutable raw export.
SPECS = {
    "wallet_transfers": (("status",), ("quota",), (), ("sender_id", "recipient_id"), {}),
    "subscription_pre_consume_records": (("status", "recovery_state", "billing_managed", "wallet_overflow"),
        ("pre_consumed", "token_consumed", "actual_quota", "wallet_consumed"), (), ("user_id",),
        {"user_subscription_id": "user_subscriptions", "token_id": "tokens"}),
    "user_subscriptions": (("status",), ("amount_total", "amount_used", "reset_amount", "renewal_amount"), (),
        ("user_id",), {"plan_id": "subscription_plans"}),
    "tokens": (("status", "unlimited_quota"), ("remain_quota", "used_quota"), (), ("user_id",), {}),
    "open_source_bounty_projects": (("status",), ("promotion_quota", "reward_quota", "escrow_quota", "platform_fee_quota", "net_reward_quota"), (), ("owner_user_id",), {}),
    "open_source_bounty_ledgers": (("kind",), ("quota",), (), ("user_id", "counterparty_user_id"),
        {"project_id": "open_source_bounty_projects", "challenge_id": "open_source_bounty_challenges"}),
    "merchant_store_orders": (("status", "fee_held", "currency", "payment_method"),
        ("unit_price_quota", "price_quota", "fee_quota", "original_price_quota", "discount_quota"), ("amount_minor",),
        ("buyer_id", "seller_id", "recipient_id"), {}),
    "merchant_store_refunds": (("status", "currency"), ("principal_quota", "retained_fee_quota"), ("amount_minor",),
        ("requested_by", "decision_by"), {"order_id": "merchant_store_orders"}),
    "merchant_store_transfers": (("kind",), ("quota",), (), ("from_user_id", "to_user_id"),
        {"order_id": ("merchant_store_orders", "merchant_store_promotions")}),
    "merchant_store_promotions": ((), ("quota",), (), ("seller_id",), {"product_id": "merchant_store_products"}),
    "open_source_bounty_challenges": (("status",), ("reward_quota", "tip_quota"), (), ("participant_user_id",), {"project_id": "open_source_bounty_projects"}),
    "tool_market_calls": (("execution_status", "settlement_status", "billing_mode"),
        ("price_quota", "fee_quota", "input_token_price_quota"), (), ("user_id", "owner_id", "recipient_id"), {}),
    "tool_market_transfers": (("kind",), ("quota",), (), ("from_user_id", "to_user_id"),
        {"call_id": "tool_market_calls"}),
    "assistant_new_user_gifts": (("status",), ("quota",), ("amount_cents",), ("user_id",), {}),
    "discount_code_reservations": (("status",), (), (), ("user_id",), {"discount_code_id": "discount_codes"}),
    "redemptions": (("status", "reward_type"), ("quota",), (), ("user_id", "used_user_id"), {}),
    "red_packet_items": (("item_type",), (), (), ("claimed_by",), {"packet_id": "red_packets"}),
    "red_packet_claims": (("item_type",), (), (), ("user_id",), {"packet_id": "red_packets", "item_id": "red_packet_items"}),
    "referral_rewards": (("status",), ("quota", "revoked_quota", "penalty_quota", "max_penalty_quota"), (),
        ("invitee_id", "inviter_id"), {"top_up_id": "top_ups"}),
    "referral_ledger_entries": (("kind",), ("quota",), (), ("user_id",), {"reward_id": "referral_rewards"}),
    "top_ups": (("status", "settlement_currency", "payment_method", "payment_provider"),
        ("credited_quota", "refunded_quota", "pending_credit_rebase_original_quota", "pending_credit_rebase_effective_quota"),
        ("amount", "expected_amount_micros", "settled_amount_micros", "refunded_amount_micros", "platform_amount_micros"), ("user_id",), {}),
}


# @mark migration.financial-rights role=implementation
def source_key_comparison_facts(row, identities):
    """Independent projection, not a call to the planner's candidate mapper.

    This checks exported stored facts, NOT effective runtime authorization or
    historical unit conversion. No source/target fields are trusted as money.
    """
    return {"account_id": identities.get(row["user_id"]),
        "status": row["status"], "deleted_at": row.get("deleted_at"),
        "expired_time": row["expired_time"], "unlimited": row["unlimited_quota"],
        "remaining_units": row["remain_quota"], "used_units": row["used_quota"],
        "lifetime_limit_units": None if row["unlimited_quota"] else row["used_quota"] + row["remain_quota"],
        "secret_sha256": hashlib.sha256(row["key"].encode("utf-8")).hexdigest(),
        "restrictions": {field: row.get(field) for field in (
            "model_limits_enabled", "model_limits", "allow_ips", "group", "auto_groups",
            "cross_group_retry", "oauth_managed", "account_balance_read", "one_time_reveal", "creation_source")}}


# @mark migration.financial-rights role=implementation
def validate_target_key_inventory(F, inventory):
    """Read-only JSON export contract; no connection or plaintext secret input.

    records must be the COMPLETE key set in the reviewed migration scope.
    Absence of an inventory is a failed comparison, not successful acceptance.
    """
    if inventory is None:
        return {}
    F.require(type(inventory) is dict and set(inventory) == {"format_version", "scope", "records"}
        and type(inventory["format_version"]) is int and inventory["format_version"] == 1
        and inventory["scope"] == "legacy-api-keys" and type(inventory["records"]) is list,
        "reconciliation_target_inventory_invalid")
    fields = {"source_key_id", "target_key_id", "account_id", "status", "deleted_at", "expired_time",
              "unlimited", "remaining_units", "used_units", "lifetime_limit_units", "secret_sha256", "restrictions"}
    result, ids, hashes = {}, set(), set()
    for row in inventory["records"]:
        F.require(type(row) is dict and set(row) == fields, "reconciliation_target_key_shape_invalid")
        for field in ("source_key_id", "target_key_id", "account_id"):
            F.integer(row[field], "reconciliation_target_key_identity_invalid", minimum=1)
        F.require(row["source_key_id"] not in result and row["target_key_id"] not in ids,
                  "reconciliation_target_key_duplicate")
        F.require(type(row["status"]) is int and row["status"] in (1, 2, 3, 4), "key_status_unknown")
        F.require(type(row["unlimited"]) is bool, "reconciliation_target_key_unlimited_invalid")
        for field in ("remaining_units", "used_units", "expired_time"):
            F.integer(row[field], "reconciliation_target_key_counter_invalid")
        if row["lifetime_limit_units"] is not None:
            F.integer(row["lifetime_limit_units"], "reconciliation_target_key_limit_invalid", minimum=0)
        F.require(type(row["restrictions"]) is dict, "reconciliation_target_key_restrictions_invalid")
        secret_hash = row["secret_sha256"]
        F.require(type(secret_hash) is str and len(secret_hash) == 64 and
                  all(c in "0123456789abcdef" for c in secret_hash), "reconciliation_target_key_hash_invalid")
        F.require(secret_hash not in hashes, "reconciliation_target_key_bearer_duplicate")
        result[row["source_key_id"]] = row
        ids.add(row["target_key_id"])
        hashes.add(secret_hash)
    return result


# @mark migration.financial-rights role=implementation
def reject_known_key_secrets(F, result, tokens):
    """Refuse metadata-carried bearer leaks as well as direct secret fields."""
    secrets = [row["key"] for row in tokens]
    def visit(value):
        if type(value) is str:
            F.require(not any(secret in value for secret in secrets), "reconciliation_key_secret_in_output")
        elif type(value) is dict:
            for key, child in value.items():
                visit(key)
                visit(child)
        elif type(value) is list:
            for child in value:
                visit(child)
    visit(result)


# @mark migration.financial-rights role=implementation
def key_quota_reconciliation(F, *, tables, identities, plan, target_key_inventory=None):
    """Bind Key counters independently; never turn a spending cap into money.

    Called only after build verifies frozen source evidence and identity types.
    The existing planner defines the candidate lifetime limit, not an approved
    native credential or a proven conversion to current credits. Recompute it
    from source rather than trusting a self-consistently rehashed plan.
    """
    source = tables.get("tokens", [])
    expected = F.map_key_limits(source, identities)
    # The persisted planner explicitly encodes decimal display tags. Compare
    # its exact stored types; display JSON alone aliases a Decimal and an object.
    F.require(F.digest(plan.get("key_limits", [])) == F.digest(F.exact(expected)),
              "reconciliation_key_plan_mismatch")
    F.require(len(set(identities.values())) == len(identities),
              "reconciliation_key_account_mapping_not_unique")
    # Planner consistency is NOT target evidence. The target side below never
    # calls map_key_limits and is compared against the frozen raw source.
    target_rows = validate_target_key_inventory(F, target_key_inventory)
    records = []
    by_id = {row["source_key_id"]: row for row in expected}
    for ordinal, row in enumerate(source):
        item = by_id[row["id"]]
        used, remaining = item["used_units"], item["remaining_units"]
        # Even unlimited Keys have signed display counters. This addition is
        # source arithmetic only, not an exchange rate or a wallet posting.
        display_basis = used + remaining
        blockers = [*item["blockers"], "source-unit-and-deployed-policy-unproven",
                    "native-key-binding-not-reviewed",
                    "source-state-and-restrictions-not-authorized"]
        if not -F.BIGINT_MAX - 1 <= display_basis <= F.BIGINT_MAX:
            blockers.append("key_display_basis_out_of_range")
        records.append({"source_key_id": item["source_key_id"],
            "source_user_id": item["source_user_id"], "target_account_id": item["account_id"],
            **plan["source_evidence"]["tokens"]["source_records"][ordinal],
            "source_remaining_integer": remaining, "source_used_integer": used,
            "source_unlimited": item["unlimited"], "display_basis_integer": display_basis,
            "display_basis_minus_used_equals_remaining": display_basis - used == remaining,
            "candidate_lifetime_limit_integer": item["lifetime_limit_units"],
            "unit": "source-field-unit-unproven",
            "source_status": item["source_status"], "source_deleted_at": item["source_deleted_at"],
            "source_expired_time": item["expired_time"], "target_key_id": None,
            "credential_activation_allowed": False, "blockers": blockers})
        target = target_rows.pop(row["id"], None)
        record = records[-1]
        record["comparison"] = "missing_in_target"
        record["mismatched_fields"] = []
        if target is not None:
            record["target_key_id"] = target["target_key_id"]
            facts = source_key_comparison_facts(row, identities)
            record["mismatched_fields"] = [field for field, value in facts.items()
                if F.digest(target[field]) != F.digest(value)]
            record["comparison"] = "field_mismatch" if record["mismatched_fields"] else "matched"
    for source_id, target in sorted(target_rows.items()):
        records.append({"source_key_id": source_id, "target_key_id": target["target_key_id"],
            "comparison": "unexpected_in_target", "mismatched_fields": [],
            "credential_activation_allowed": False})
    passed = target_key_inventory is not None and all(r["comparison"] == "matched" for r in records)
    return {"scope": "offline-source-to-target-key-reconciliation",
        "status": "matched" if passed else "failed", "passed": passed,
        "target_inventory_supplied": target_key_inventory is not None,
        "target_inventory_provenance_verified": False,
        "target_inventory_scope_completeness_verified": False,
        "target_inventory_sha256": F.digest(target_key_inventory) if target_key_inventory is not None else None,
        "comparison_counts": dict(Counter(r["comparison"] for r in records)), "records": records,
        "record_count": len(records), "overlapping_counters_do_not_sum": True,
        "historical_usage_is_not_a_new_charge": True, "wallet_delta_units": 0,
        "native_import_payloads_created": False, "target_writes": 0}


# @mark migration.financial-rights role=implementation
def build(F, *, tables, identities, plan, proof, identity_map_file_sha256,
          identity_manifest_file_sha256, tool_hashes, target_key_inventory=None):
    # Validate key states before any private projection, including grouped state.
    F.map_key_limits(tables.get("tokens", []), identities)
    F.verify_plan_mapping(plan)
    F.require(set(tables) == set(plan["source_evidence"]) == set(proof["selected_table_counts"]),
              "reconciliation_table_coverage_mismatch")
    F.require(proof.get("source_queries_this_run") == 0 and type(proof.get("source_queries_this_run")) is int,
              "reconciliation_requires_frozen_export")
    table_evidence = {}
    indexes = {}
    for table, rows in sorted(tables.items()):
        expected = plan["source_evidence"][table]
        F.require(type(expected["row_count"]) is int, "reconciliation_table_count_mismatch")
        F.require([F.digest(row) for row in rows] == expected["row_hashes"],
                  "reconciliation_row_hash_mismatch")
        fields = expected["primary_key_columns"]
        F.require(fields is None or (type(fields) is list and bool(fields) and
                  all(type(field) is str for field in fields) and len(set(fields)) == len(fields)),
                  "reconciliation_primary_key_contract_invalid")
        records = expected["source_records"]
        F.require(type(records) is list and len(records) == len(rows),
                  "reconciliation_source_record_mismatch")
        for ordinal, row in enumerate(rows):
            F.require(type(row) is dict and (fields is None or all(field in row and row[field] is not None for field in fields)),
                      "reconciliation_primary_key_value_missing")
            record = records[ordinal]
            F.require(type(record) is dict and type(record.get("source_export_ordinal")) is int and
                      F.canonical(record) == F.canonical({"source_primary_key": {field: row[field] for field in fields} if fields else None,
                          "source_export_ordinal": ordinal, "source_row_sha256": F.digest(row)}),
                      "reconciliation_source_record_mismatch")
        F.require(type(proof["selected_table_counts"][table]) is int and
                  len(rows) == expected["row_count"] == proof["selected_table_counts"][table],
                  "reconciliation_table_count_mismatch")
        table_evidence[table] = {"row_count": len(rows), "source_rows_sha256": F.digest(rows),
            "source_column_schema_sha256": F.digest(expected["source_column_schema"]),
            "source_records_sha256": F.digest(expected["source_records"]),
            "detail_projection": "explicit-source-facts" if table in SPECS else "not-projected-retained-in-raw-export",
            "opening_holds": "unknown-requires-independent-source-model-review"}
        if expected["primary_key_columns"] == ["id"]:
            indexes[table] = {F.digest(row["id"]): row for row in rows}
    wallets = []
    F.require(all(type(row.get("id")) is int and row["id"] > 0 for row in tables["users"]) and
              all(type(key) is int and key > 0 and type(value) is int and value > 0 for key, value in identities.items()),
              "reconciliation_identity_integer_invalid")
    source_users = {row["id"]: row for row in tables["users"]}
    F.require(len(source_users) == len(tables["users"]) == len(identities), "reconciliation_source_user_coverage_mismatch")
    totals = Counter()
    for item in plan["wallet_positions"]:
        F.require(type(item.get("source_user_id")) is int and item["source_user_id"] > 0 and
                  type(item.get("account_id")) is int and item["account_id"] > 0,
                  "reconciliation_wallet_identity_integer_invalid")
        user = source_users.get(item["source_user_id"])
        F.require(user is not None and F.digest(user) == item["source_row_sha256"] and
            all(type(item[destination]) is int and item[destination] == F.integer(user[source], "reconciliation_wallet_source_integer_invalid")
                for source, destination in (("quota", "signed_available_units"), ("aff_quota", "signed_affiliate_units"),
                    ("used_quota", "historical_used_units"), ("request_count", "historical_request_count"),
                    ("aff_history", "historical_affiliate_units"))), "reconciliation_wallet_source_mismatch")
        quota = F.integer(item["signed_available_units"], "reconciliation_quota_invalid")
        affiliate = F.integer(item["signed_affiliate_units"], "reconciliation_affiliate_invalid")
        wallet, debt = max(quota, 0), min(quota, 0)
        F.require(wallet + debt == quota and identities.get(item["source_user_id"]) == item["account_id"],
                  "reconciliation_wallet_mapping_mismatch")
        wallets.append({"source_user_id": item["source_user_id"], "target_account_id": item["account_id"],
            "source_row_sha256": item["source_row_sha256"], "signed_available_units": quota,
            "wallet_units": wallet, "debt_units_signed": debt, "signed_affiliate_units": affiliate,
            "wallet_plus_signed_debt_equals_source_quota": True,
            "historical_used_units": item["historical_used_units"],
            "historical_request_count": item["historical_request_count"],
            "historical_affiliate_units": item["historical_affiliate_units"],
            "reserved_units": None, "reserved_status": "unknown-not-inferred-from-wallet-or-state-counts"})
        totals.update({"signed_available_units": quota, "wallet_units": wallet,
                       "debt_units_signed": debt, "signed_affiliate_units": affiliate})
    F.require(len(wallets) == len(identities) and len({w["source_user_id"] for w in wallets}) == len(identities),
              "reconciliation_wallet_coverage_mismatch")
    records, reference_counts, grouped = [], Counter(), {}
    for table, spec in sorted(SPECS.items()):
        if table not in tables:
            continue
        states, credits, other_amounts, owners, links = spec
        for ordinal, row in enumerate(tables[table]):
            source = plan["source_evidence"][table]["source_records"][ordinal]
            state = {}
            for field in states:
                if field in row:
                    value = row[field]
                    F.require(value is None or type(value) in (str, int, bool), "reconciliation_state_type_invalid")
                    state[field] = value
            amounts = {}
            for field in credits + other_amounts:
                if field in row:
                    value = None if row[field] is None else F.integer(row[field], "reconciliation_amount_not_integer")
                    amounts[field] = {"value": value, "value_status": "source-null-unresolved" if value is None else "source-integer",
                        "unit": "source-field-unit-unproven",
                        "field_category": "credit-named-source-field" if field in credits else "other-source-amount"}
                    # Separate table, field, complete observed state and currency.
                    # Never total overlapping families, fields or currencies.
                    group_key = F.canonical([table, field, state])
                    group = grouped.setdefault(group_key, {"table": table, "field": field, "state": state,
                        "unit": amounts[field]["unit"], "row_count": 0, "known_integer_count": 0, "null_count": 0, "integer_sum": 0,
                        "integer_sum_is_complete": True})
                    group["row_count"] += 1
                    if value is None:
                        group["null_count"] += 1
                        group["integer_sum_is_complete"] = False
                    else:
                        group["known_integer_count"] += 1
                        group["integer_sum"] += value
            user_refs = {}
            for field in owners:
                if field in row:
                    value = None if row[field] is None else F.integer(row[field], "reconciliation_user_reference_not_integer")
                    status = "null-unresolved" if value is None else "mapped" if value in identities else "zero-or-unassigned" if value == 0 else "unmapped"
                    user_refs[field] = {"source_user_id": value, "mapping_status": status,
                                        "target_account_id": identities.get(value)}
                    reference_counts["user_" + status] += 1
            refs = {}
            for field, target in links.items():
                if field in row:
                    value = row[field]
                    F.require(value is None or type(value) in (str, int), "reconciliation_reference_type_invalid")
                    targets = target if isinstance(target, tuple) else (target,)
                    matched = [t for t in targets if F.digest(value) in indexes.get(t, {})]
                    status = "zero-or-unassigned" if value in (None, 0, "") else (
                        "present" if matched else "missing-or-unindexed")
                    refs[field] = {"source_value": value, "candidate_target_tables": list(targets),
                                   "matched_tables": matched, "status": status,
                                   "association_does_not_prove_economic_semantics": True}
                    reference_counts["row_" + status] += 1
            records.append({"table": table, **source, "observed_state": state, "observed_integer_amounts": amounts,
                "user_references": user_refs, "row_references": refs,
                "wallet_debit_already_applied": "unknown-until-source-model-provenance-and-policy-review",
                "outstanding_right": "unknown-not-derived-from-state-name",
                "blocking_reason": "source-model-provenance-and-full-economic-review-not-established"})
    keys = key_quota_reconciliation(F, tables=tables, identities=identities, plan=plan,
                                    target_key_inventory=target_key_inventory)
    spec = importlib.util.spec_from_file_location("legacy_bounty_basis", Path(__file__).with_name("legacy_bounty_basis.py"))
    helper = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(helper)
    bounty = helper.build(F, tables=tables, identities=identities, plan=plan)
    result = {"format_version": 1, "digest_format": F.DIGEST_FORMAT,
        "scope": "source-rights-reconciliation-only",
        "status": "blocked-for-opening-economic-review" if keys["passed"] else "failed-key-reconciliation",
        "key_reconciliation_passed": keys["passed"],
        "source_archive_sha256": proof["source_archive_sha256"], "source_export_sha256": proof["source_export_sha256"],
        "identity_map_file_sha256": identity_map_file_sha256, "identity_manifest_file_sha256": identity_manifest_file_sha256,
        "financial_mapping_sha256": plan["mapping_sha256"], "tool_source_hashes": tool_hashes,
        "source_model_provenance": {"status": "not-established", "capture_tool_revision_is_not_deployed_application_revision": True},
        "table_evidence": table_evidence, "wallet_positions": wallets, "wallet_integer_totals": dict(totals),
        "wallet_totals_are_source_positions_not_new_credits": True,
        "wallet_positions_are_raw_integer_quota_not_usd_conversion": True,
        "bounty_rebase_lineage": bounty, "key_quota_reconciliation": keys,
        "observed_rights_records": records, "reference_counts": dict(sorted(reference_counts.items())),
        "integer_amount_groups": [grouped[key] for key in sorted(grouped)],
        "historical_counters_are_not_wallet_or_reward_lineage": True,
        "integer_credit_field_labels_do_not_prove_deployed_economic_policy": True,
        "source_queries": 0, "target_queries": 0, "target_writes": 0,
        "opening_payloads_created": False, "approval_created": False,
        "raw_export_preserved": True, "families_overlap_do_not_sum": True,
        "unprojected_tables_are_not_proven_clear": True,
        "selected_export_is_not_full_database": True, "full_database_rights_cleared": False,
        "public_summary": {"status": "blocked-for-opening-economic-review" if keys["passed"] else "failed-key-reconciliation",
            "key_reconciliation_passed": keys["passed"],
            "full_source_manifest_created": False, "raw_source_rows_copied": False,
            "bounty_rebase_lineage": bounty["public_summary"],
            "key_quota_reconciliation": {"status": keys["status"], "record_count": keys["record_count"],
                "target_inventory_supplied": keys["target_inventory_supplied"],
                "comparison_counts": keys["comparison_counts"],
                "target_inventory_provenance_verified": False,
                "target_inventory_scope_completeness_verified": False,
                "native_import_payloads_created": False, "target_writes": 0},
            "selected_export_is_not_full_database": True, "full_database_rights_cleared": False,
            "source_table_count": len(tables), "source_row_count": sum(map(len, tables.values())),
            "wallet_position_count": len(wallets), "projected_table_count": sum(t in tables for t in SPECS),
            "projected_record_count": len(records), "unmapped_user_reference_count": reference_counts["user_unmapped"],
            "missing_or_unindexed_reference_count": reference_counts["row_missing-or-unindexed"],
            "source_queries": 0, "target_queries": 0, "target_writes": 0,
            "reserved_zero_proven": False, "financial_rows_migrated": 0}}
    reject_known_key_secrets(F, result, tables.get("tokens", []))
    return result
