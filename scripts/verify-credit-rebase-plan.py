#!/usr/bin/env python3
"""Independently check a sealed credit plan and emit read-only PostgreSQL assertions.

This program has no database client, connection options, or apply mode. Its SQL
reads actual rows; it never invokes or imports the migration SQL renderer.
Private plans and generated SQL belong in restricted files. The summary contains
only hashes, counts and verification stage, never wallet values or user IDs.
"""
import argparse
from decimal import Decimal, InvalidOperation, ROUND_HALF_UP, localcontext
from fractions import Fraction
import hashlib
import json
import math
import os
from pathlib import Path
import re
import sys

MAX_INTEGER = (1 << 53) - 1
ANCHORS = {"CreditsPerUSD", "PublicCreditsPerUSD", "LegacyPricingQuotaPerUnit", "QuotaPerUnit"}
PRICE_KEYS = {"ModelRatio", "ModelPrice", "billing_setting.billing_expr", "tool_price_setting.prices"}
TOPUP_COLUMNS = set("id user_id status credited_quota amount platform_amount_micros settled_amount_micros expected_amount_micros refunded_quota refunded_amount_micros money payment_provider payment_method settlement_currency".split())
TOPUP_DERIVED = {"effective_credited_quota", "paid_amount_micros", "is_legacy_linuxdo_credit_topup", "classification_reason"}
PENDING_COLUMNS = TOPUP_COLUMNS | set("failure_reason_code pending_credit_rebase_key pending_credit_rebase_original_quota pending_credit_rebase_effective_quota".split())
REFERRAL_COLUMNS = set("id inviter_id invitee_id top_up_id quota revoked_quota penalty_quota penalty_percent max_penalty_quota revision created_at updated_at status reason".split())
ENTITY_COLUMNS = {
    "redemptions": set("id user_id used_user_id quota status created_time redeemed_time expired_time reward_type deleted_at".split()),
    "open_source_bounty_projects": set("id owner_user_id escrow_quota reward_quota net_reward_quota reward_slots platform_fee_quota platform_fee_rate_bps created_at updated_at published_at closed_at archived_at status".split()),
    "open_source_bounty_challenges": set("id project_id participant_user_id reward_quota tip_quota accepted_at submitted_at reviewed_at rejected_at paid_at created_at updated_at status".split()),
    "open_source_bounty_disputes": set("id challenge_id project_id opened_by_user_id against_user_id project_escrow_quota_snapshot reward_quota_snapshot tip_quota_snapshot challenge_status_snapshot status resolved_by_user_id created_at updated_at resolved_at".split()),
}
ENTITY_WRITES = {"redemptions": {"quota"}, "open_source_bounty_projects": {"escrow_quota", "reward_quota", "net_reward_quota"}, "open_source_bounty_challenges": {"reward_quota"}, "open_source_bounty_disputes": set()}
SUB_COLUMNS = set("id user_id plan_id amount_total amount_used quota_version start_time end_time last_reset_time next_reset_time created_at updated_at status source reset_amount renewal_amount".split())
ORDER_COLUMNS = set("id user_id plan_id user_subscription_id charged_quota refunded_quota refunded_amount_micros create_time complete_time expected_amount_micros current_period_start current_period_end provider_event_time_millis status money plan_currency plan_snapshot settlement_currency payment_method payment_provider provider_subscription_state".split())
PLAN_COLUMNS = set("id total_amount created_at updated_at archived_at quota_reset_custom_seconds price_amount currency quota_reset_period enabled".split())
PAYMENT_COLUMNS = set("id subscription_order_id settlement_amount_micros created_time payment_provider provider_event_id provider_transaction_id settlement_currency period_start period_end".split())
REFUND_COLUMNS = set("id subscription_order_id subscription_payment_event_id amount_micros quota_revoked finance_ledger_entry_id created_time payment_provider provider_event_id currency".split())
TOPUP_BASIS_COLUMNS = set("top_up_id user_id original_credited_quota original_refunded_quota original_refunded_amount_micros original_paid_amount_micros refundable_quota rebased_debited_quota".split())
REFERRAL_BASIS_COLUMNS = set("reward_id user_id original_quota rebased_quota rebased_revoked_quota rebased_penalty_quota".split())
SUB_BASIS_COLUMNS = set("subscription_order_id user_subscription_id user_id period_start period_end subscription_end_time original_quota_version original_credit_quota original_refunded_quota original_refunded_amount_micros original_paid_amount_micros refundable_quota reset_quota reset_reduced_quota rebased_debited_quota".split())
OTHER_SPECS = {
    "public_relay_tip_pool": ("public_relay_contributions", set("id user_id tip_quota withdrawn_quota created_at updated_at status".split())),
    "assistant_gift": ("assistant_new_user_gifts", set("id user_id quota amount_cents created_at claimed_at status".split())),
    "grant_gift": ("gifts", set("id quota start_at end_at min_used_quota min_account_age_days created_at enabled".split())),
    "ai_directory_ad_refund": ("ai_directory_ads", set("id owner_user_id charged_quota bid_cents paid_at expires_at hidden_at refunded_at status".split())),
    "violation_fee_refund": ("violation_fee_records", set("id user_id charged_quota requested_quota created_at reversed_at reversed_by status error_code".split())),
    "email_refund_pool": ("hero_sms_email_orders", set("id user_id charge_quota refunded_quota quantity created_at updated_at last_refund_ledger_id status operation".split())),
}
ACTIVATION_COLUMNS = set("id order_id user_id charge_quota refund_quota created_at updated_at refunded_at cancelled_at status cancel_reason".split())
USER_COLUMNS = set("id quota aff_quota used_quota request_count aff_history aff_count status deleted_at".split())
TOKEN_COLUMNS = set("id user_id remain_quota used_quota unlimited_quota status created_time accessed_time expired_time deleted_at".split())
OBLIGATIONS = {
    "wallet_transfers_pending": ("wallet_transfers", "status='pending'"),
    "tool_market_held": ("tool_market_calls", "settlement_status='held'"),
    "tasks_unfinished": ("tasks", "COALESCE(status,'') NOT IN ('SUCCESS','FAILURE')"),
    "tasks_refund_pending": ("tasks", "refund_status='PENDING' OR (status='FAILURE' AND COALESCE(refund_status,'')='' AND (quota<>0 OR refund_quota<>0) AND (submit_time<=0 OR submit_time>=1771718400))"),
    "midjourney_unfinished": ("midjourneys", "progress<>'100%'"),
    "subscription_reservations": ("subscription_pre_consume_records", "status IN ('consumed','settling')"),
    "sms_unfinished": ("hero_sms_sms_orders", "status IN ('pending_provider','purchase_unknown','active','cancel_pending')"),
    "email_orders_unfinished": ("hero_sms_email_orders", "status IN ('pending_provider','purchase_unknown','reconciling')"),
    "email_activations_unfinished": ("hero_sms_email_activations", "status IN ('pending_provider','active','reconciling','cancel_pending')"),
}


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False)


def digest(value):
    return hashlib.sha256(canonical(value).encode()).hexdigest()


def business_plan(plan):
    return {k: v for k, v in plan.items() if k not in {"target", "source_sha256", "plan_sha256", "business_plan_sha256"}}


def exact_int(value):
    if type(value) is not int or abs(value) > MAX_INTEGER:
        raise ValueError("plan requires exact safe integers")
    return value


def scalar(value):
    if value is None or type(value) is bool:
        return
    if type(value) is int:
        exact_int(value)
        return
    if isinstance(value, str) and "\x00" not in value:
        return
    raise ValueError("unsupported plan source value")


def source_projection(source, columns, *, derived=()):
    if not isinstance(source, dict) or not columns <= set(source) or set(source) - columns - set(derived):
        raise ValueError("plan source must include the complete approved projection")
    for value in source.values():
        scalar(value)
    return {key: source[key] for key in sorted(columns)}


def load_plan(path):
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise ValueError("duplicate JSON key")
            result[key] = value
        return result
    return json.loads(Path(path).read_text(), object_pairs_hook=pairs,
                      parse_constant=lambda _: (_ for _ in ()).throw(ValueError("nonfinite JSON value")))


def validate_plan(plan):
    if not isinstance(plan, dict) or plan.get("version") != 1 or plan.get("kind") != "offline_credit_balance_rebase_preview":
        raise ValueError("unsupported credit plan")
    expected = digest({k: v for k, v in plan.items() if k != "plan_sha256"})
    if plan.get("plan_sha256") != expected:
        raise ValueError("plan SHA-256 mismatch")
    if plan.get("business_plan_sha256") != digest(business_plan(plan)):
        raise ValueError("business plan SHA-256 mismatch or missing")
    for key in ("source_sha256", "business_source_sha256"):
        if not re.fullmatch(r"[0-9a-f]{64}", plan.get(key, "")):
            raise ValueError("required source digest missing")
    target = plan.get("target")
    if not isinstance(target, dict) or not {"database", "schema", "system_identifier"} <= set(target) or set(target) - {"database", "schema", "system_identifier", "database_oid", "schema_oid"}:
        raise ValueError("explicit target identity required")
    if not isinstance(target["database"], str) or not target["database"] or "\x00" in target["database"]:
        raise ValueError("invalid target database")
    if not isinstance(target["schema"], str) or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]{0,62}", target["schema"]):
        raise ValueError("invalid target schema")
    if not isinstance(target["system_identifier"], str) or not re.fullmatch(r"[0-9]{1,20}", target["system_identifier"]):
        raise ValueError("invalid target system identifier")
    for key in ("database_oid", "schema_oid"):
        if key in target and (not isinstance(target[key], str) or not re.fullmatch(r"[0-9]{1,10}", target[key]) or not 0 < int(target[key]) <= 4294967295):
            raise ValueError("invalid target object identity")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,99}", plan.get("migration_id", "")):
        raise ValueError("invalid migration identity")
    ids = plan.get("user_ids")
    if not isinstance(ids, list) or not ids or any(exact_int(i) <= 0 for i in ids) or ids != sorted(set(ids)):
        raise ValueError("selected users must be sorted unique positive IDs")
    if type(plan.get("snapshot_all_users", False)) is not bool:
        raise ValueError("invalid all-user snapshot marker")
    orphans = plan.get("orphan_pending_user_ids", [])
    if not isinstance(orphans, list) or any(exact_int(i) <= 0 for i in orphans) or orphans != sorted(set(orphans)) or set(orphans) & set(ids):
        raise ValueError("orphan pending owners require a sorted distinct identity list")
    if orphans and not plan.get("snapshot_all_users"):
        raise ValueError("orphan pending owners require a complete all-user snapshot")
    if not plan.get("restore_fixed_anchors") or plan.get("usd_credit_conversion") != 500000:
        raise ValueError("independent verification requires a combined fixed-anchor plan")
    state = plan.get("snapshot_state")
    if state not in {"unspecified", "provisional_live_not_frozen", "frozen_writers_stopped"}:
        raise ValueError("explicit snapshot freeze state required")
    expected_support = "reviewed_postgres_sql_only" if state == "frozen_writers_stopped" else False
    if plan.get("production_apply_supported") != expected_support:
        raise ValueError("snapshot state and production support marker disagree")
    obligations = plan.get("obligations")
    if not isinstance(obligations, dict) or set(obligations) != set(OBLIGATIONS) or any(type(i) is not int or i != 0 for i in obligations.values()):
        raise ValueError("all nine explicit obligation counts must be zero")
    for key in ("include_affiliate", "include_token_limits", "include_pending_topups", "include_redemptions", "include_bounties", "include_subscriptions", "include_other_rights"):
        if type(plan.get(key)) is not bool:
            raise ValueError("explicit scope flag missing")
    if not re.fullmatch(r"[0-9]+(?:\.[0-9]{1,18})?", plan.get("divisor", "")):
        raise ValueError("invalid exact divisor")
    divisor = Fraction(plan["divisor"])
    if divisor <= 1 or plan.get("rounding") not in ("half-away-from-zero", "toward-zero"):
        raise ValueError("invalid correction factor or rounding")
    fx = plan.get("fx_source", {})
    if fx != {"kind": "frozen_production_option", "key": "USDExchangeRate", "value": plan["divisor"]} and (fx.get("kind") != "frozen_production_option" or fx.get("key") != "USDExchangeRate" or Fraction(fx.get("value", "0")) != divisor):
        raise ValueError("frozen FX does not match correction divisor")
    if plan.get("exact_factor") != {"numerator": divisor.denominator, "denominator": divisor.numerator}:
        raise ValueError("exact factor mismatch")
    for key in ("entries", "user_sources", "token_sources", "refund_bases", "noncash_topups", "pending_bases", "blocked_pending_bases", "referral_bases", "entity_updates", "subscriptions", "subscription_order_sources", "subscription_order_updates", "subscription_plan_updates", "subscription_refund_bases", "subscription_payment_events", "subscription_payment_refunds", "other_credit_bases", "option_entries", "option_guards"):
        if not isinstance(plan.get(key), list):
            raise ValueError("complete explicit plan array required: " + key)
    options = plan["option_entries"]
    keys = [e.get("key") for e in options]
    if len(set(keys)) != len(keys) or not ANCHORS <= set(keys) or set(keys) - ANCHORS - PRICE_KEYS:
        raise ValueError("unsupported or missing option correction")
    for e in options:
        if set(e) != {"key", "before", "after"} or any(not isinstance(e[k], str) or "\x00" in e[k] for k in e):
            raise ValueError("exact option CAS strings required")
        if e["key"] in ANCHORS and e["after"] != "500000":
            raise ValueError("fixed anchor target must be exactly 500000")
    guards = plan["option_guards"]
    if len({g.get("key") for g in guards}) != len(guards):
        raise ValueError("duplicate preservation option")
    if not any(g == {"key": "USDExchangeRate", "value": fx["value"], "absent": False} for g in guards):
        raise ValueError("exact frozen FX preservation guard missing")
    for g in guards:
        if type(g.get("absent")) is not bool or set(g) != ({"key", "absent"} if g["absent"] else {"key", "value", "absent"}):
            raise ValueError("invalid option preservation guard")
        if g["key"] in keys or any(not isinstance(g[k], str) or "\x00" in g[k] for k in g if k != "absent"):
            raise ValueError("conflicting preservation option")
    return divisor


def clone_proof(plan, other):
    _postgres_verification_sql(plan, "before")
    _postgres_verification_sql(other, "before")
    if canonical(business_plan(plan)) != canonical(business_plan(other)):
        raise ValueError("clone changed business data beyond target identity")
    return {"target_only_clone": True, "business_plan_sha256": plan["business_plan_sha256"], "business_source_sha256": plan["business_source_sha256"]}


def literal(value):
    if value is None:
        return "NULL"
    if type(value) is bool:
        return "true" if value else "false"
    if type(value) is int:
        return str(exact_int(value))
    if not isinstance(value, str) or "\x00" in value:
        raise ValueError("unsupported SQL literal")
    return "'" + value.replace("'", "''") + "'"


def ident(value):
    if not isinstance(value, str) or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]{0,62}", value):
        raise ValueError("unapproved SQL identifier")
    return '"' + value + '"'


def _postgres_verification_sql(plan, stage):
    divisor = validate_plan(plan)
    if stage not in ("before", "after"):
        raise ValueError("stage must be before or after")
    after = stage == "after"
    schema = ident(plan["target"]["schema"])
    selected = "ARRAY[" + ",".join(str(i) for i in plan["user_ids"]) + "]::bigint[]"
    mid = literal(plan["migration_id"])
    checks = []

    def unique(rows, fields):
        identities = [tuple(row[field] for field in fields) for row in rows]
        if len(set(identities)) != len(identities):
            raise ValueError("duplicate financial source or child basis")

    for key, fields in (("refund_bases", ("top_up_id",)), ("noncash_topups", ("id",)), ("referral_bases", ("reward_id",)), ("entity_updates", ("table", "id")), ("subscriptions", ("id",)), ("subscription_order_sources", ("id",)), ("subscription_plan_updates", ("id",)), ("subscription_payment_events", ("id",)), ("subscription_payment_refunds", ("id",)), ("subscription_refund_bases", ("subscription_order_id",)), ("other_credit_bases", ("kind", "source_id"))):
        unique(plan[key], fields)
    unique(plan["pending_bases"] + plan["blocked_pending_bases"], ("top_up_id",))
    paid_ids = {b["top_up_id"] for b in plan["refund_bases"]}
    noncash_ids = {s["id"] for s in plan["noncash_topups"]}
    if paid_ids & noncash_ids or (paid_ids | noncash_ids) & {b["top_up_id"] for b in plan["pending_bases"] + plan["blocked_pending_bases"]}:
        raise ValueError("overlapping topup financial sources")
    scope_arrays = {"include_affiliate": ("referral_bases",), "include_pending_topups": ("pending_bases", "blocked_pending_bases"), "include_subscriptions": ("subscriptions", "subscription_order_sources", "subscription_order_updates", "subscription_plan_updates", "subscription_payment_events", "subscription_payment_refunds", "subscription_refund_bases")}
    for flag, keys in scope_arrays.items():
        if not plan[flag] and any(plan[key] for key in keys):
            raise ValueError("financial rows exceed declared scope")
    if not plan["include_other_rights"] and any(e["kind"] != "bounty_dispute_reward" for e in plan["other_credit_bases"]):
        raise ValueError("other financial rows exceed declared scope")

    def assert_true(condition, label):
        # Labels are fixed categories. SQL diagnostics do not disclose row data.
        checks.append("IF (" + condition + ") IS DISTINCT FROM true THEN RAISE EXCEPTION " + literal("credit verification failed: " + label) + "; END IF;")

    def count(table, predicate, expected, label):
        assert_true(f"(SELECT count(*) FROM {schema}.{ident(table)} r WHERE {predicate}) = {expected}", label + " row count")

    def guard(table, source, columns, *, overrides=None, derived=()):
        facts = source_projection(source, columns, derived=derived)
        if "id" in facts:
            if table in {"hero_sms_email_orders", "hero_sms_email_activations"}:
                if not isinstance(facts["id"], str) or not facts["id"] or len(facts["id"]) > 64:
                    raise ValueError("invalid textual source identity")
            elif exact_int(facts["id"]) <= 0:
                raise ValueError("source identity must be positive")
        facts.update(overrides or {})
        terms = []
        for key, value in sorted(facts.items()):
            if key == "last_refund_ledger_id":
                lhs = f"(SELECT COALESCE(MAX(id),0) FROM {schema}.hero_sms_email_quota_ledgers WHERE order_id={literal(source['id'])} AND entry_type='refund')"
            elif key in {"pending_credit_rebase_key", "pending_credit_rebase_original_quota", "pending_credit_rebase_effective_quota", "reset_amount", "renewal_amount"} and not after:
                # Preflight also accepts the old schema before additive columns exist.
                fallback = "null" if key in {"reset_amount", "renewal_amount"} else canonical("" if key.endswith("_key") else 0)
                terms.append(f"COALESCE(to_jsonb(r)->{literal(key)}, {literal(fallback)}::jsonb) IS NOT DISTINCT FROM {literal(canonical(value))}::jsonb")
                continue
            else:
                lhs = "r." + ident(key)
            rhs = literal(value)
            if value is not None and key in {"money", "price_amount"}:
                rhs += "::double precision" if key == "money" else "::numeric"
            terms.append(lhs + " IS NOT DISTINCT FROM " + rhs)
        assert_true(f"(SELECT count(*) FROM {schema}.{ident(table)} r WHERE " + " AND ".join(terms) + ") = 1", table + " source facts and planned values")

    def scaled(value):
        value = exact_int(value)
        quotient, remainder = divmod(abs(value) * divisor.denominator, divisor.numerator)
        if plan["rounding"] == "half-away-from-zero" and remainder * 2 >= divisor.numerator:
            quotient += 1
        return -quotient if value < 0 else quotient

    changes = {}
    entry_index = {}
    for e in plan["entries"]:
        if e.get("table") not in {"users", "tokens"} or e.get("field") not in ({"quota", "aff_quota"} if e["table"] == "users" else {"remain_quota"}):
            raise ValueError("unapproved wallet or token write")
        exact_int(e["after_credit"])
        exact_int(e["delta_credit"])
        if e["after_credit"] != scaled(e["before_credit"]) or e["delta_credit"] != e["after_credit"] - e["before_credit"]:
            raise ValueError("wallet or token arithmetic mismatch")
        key = (e["table"], exact_int(e["id"]))
        if e["field"] in changes.setdefault(key, {}):
            raise ValueError("duplicate wallet or token write")
        changes[key][e["field"]] = e["after_credit"] if after else e["before_credit"]
        entry_index[(e["table"], e["id"], e["field"])] = e
    users, tokens = plan["user_sources"], plan["token_sources"]
    if {s.get("id") for s in users} != set(plan["user_ids"]) or len(users) != len(plan["user_ids"]):
        raise ValueError("selected user source projection incomplete")
    seen_tokens = set()
    for table, rows, columns in (("users", users, USER_COLUMNS), ("tokens", tokens, TOKEN_COLUMNS)):
        for source in rows:
            if set(source) != columns:
                raise ValueError("complete wallet historical source projection required")
            if table == "tokens":
                if source["user_id"] not in plan["user_ids"] or source["id"] in seen_tokens or type(source["unlimited_quota"]) is not bool:
                    raise ValueError("invalid selected token source")
                seen_tokens.add(source["id"])
            updates = changes.get((table, source["id"]), {})
            for field in updates:
                entry = entry_index[(table, source["id"], field)]
                if source[field] != entry["before_credit"]:
                    raise ValueError("wallet entry conflicts with historical source")
            guard(table, source, set(source), overrides=updates)
    expected_changes = {("users", s["id"], "quota") for s in users}
    if plan["include_affiliate"]:
        expected_changes |= {("users", s["id"], "aff_quota") for s in users}
    if plan["include_token_limits"]:
        expected_changes |= {("tokens", s["id"], "remain_quota") for s in tokens if not s["unlimited_quota"]}
    if {(e["table"], e["id"], e["field"]) for e in plan["entries"]} != expected_changes:
        raise ValueError("wallet/token write scope incomplete or excessive")
    count("users", f"r.id=ANY({selected})", len(users), "selected users")
    if plan.get("snapshot_all_users"):
        count("users", "true", len(users), "all-user frozen snapshot")
    count("tokens", f"r.user_id=ANY({selected})", len(tokens), "selected tokens including unlimited")
    if plan.get("snapshot_all_users"):
        count("tokens", "true", len(tokens), "all-token frozen snapshot")
    for label, (table, predicate) in OBLIGATIONS.items():
        count(table, predicate, 0, label)
    for e in plan["option_entries"]:
        guard("options", {"key": e["key"], "value": e["after"] if after else e["before"]}, {"key", "value"})
    for g in plan["option_guards"]:
        if g["absent"]:
            count("options", "r.key=" + literal(g["key"]), 0, "absent preserved option")
        else:
            guard("options", {"key": g["key"], "value": g["value"]}, {"key", "value"})
    def topup_authority(s):
        if not {"effective_credited_quota", "paid_amount_micros", "is_legacy_linuxdo_credit_topup"} <= set(s):
            raise ValueError("complete topup authority projection required")
        values = [s[key] for key in ("payment_provider", "payment_method", "settlement_currency")]
        if any(not isinstance(value, str) or not value.isascii() for value in values):
            raise ValueError("exact ASCII noncash classification facts required")
        provider, method, currency = (value.strip(" \t\n\r\v\f").lower() for value in values)
        noncash = provider == "epay" and (method in {"ldc", "linuxdo", "linux_do", "linuxdo_credit"} or (method == "epay" and (currency != "cny" or (s["expected_amount_micros"] <= 0 and s["settled_amount_micros"] <= 0))))
        if type(s["is_legacy_linuxdo_credit_topup"]) is not bool or noncash != s["is_legacy_linuxdo_credit_topup"]:
            raise ValueError("noncash classification disagrees with raw source facts")
        for field in TOPUP_COLUMNS - {"status", "money", "payment_provider", "payment_method", "settlement_currency"}:
            exact_int(s[field])
        exact_int(s["effective_credited_quota"])
        exact_int(s["paid_amount_micros"])
        # These are Go's frozen source rules, reconstructed independently from
        # raw facts rather than trusting the exported derived authority fields.
        raw_provider, raw_method = s["payment_provider"], s["payment_method"]
        known = not noncash and (raw_provider in {"epay", "stripe", "creem", "waffo", "waffo_pancake"} or (not raw_provider.strip(" \t\n\r\v\f") and raw_method in {"stripe", "creem", "waffo", "waffo_pancake", "alipay", "wxpay"}))
        immutable_epay = raw_provider == "epay" and s["expected_amount_micros"] > 0 and s["credited_quota"] > 0 and bool(s["settlement_currency"].strip(" \t\n\r\v\f"))
        if s["credited_quota"] > 0 and (known or immutable_epay):
            credited = s["credited_quota"]
        elif not known:
            credited = 0
        elif raw_provider == "creem" or raw_method == "creem":
            credited = s["amount"]
        else:
            old_quota_option = next(e["before"] for e in plan["option_entries"] if e["key"] == "QuotaPerUnit")
            try:
                quota_factor = Fraction(old_quota_option)
                credited = int(s["amount"] * quota_factor) if quota_factor > 0 and s["amount"] > 0 else 0
            except (ValueError, ZeroDivisionError):
                credited = 0
            if not 0 < credited <= MAX_INTEGER:
                credited = 0
        paid = s["settled_amount_micros"] if s["settled_amount_micros"] > 0 else s["expected_amount_micros"]
        if paid <= 0:
            try:
                raw_money = float(s["money"])
                if not math.isfinite(raw_money):
                    raise ValueError("invalid topup money")
                with localcontext() as context:
                    context.prec = 100
                    paid = int((Decimal(str(raw_money)) * Decimal(1_000_000)).quantize(Decimal(1), rounding=ROUND_HALF_UP)) if raw_money > 0 else 0
            except (ValueError, InvalidOperation, OverflowError) as error:
                raise ValueError("invalid historical topup payment") from error
        if s["effective_credited_quota"] != credited or s["paid_amount_micros"] != paid:
            raise ValueError("derived topup authority disagrees with immutable raw facts")
        return noncash

    for b in plan["refund_bases"]:
        s = b["source"]
        guard("top_ups", s, TOPUP_COLUMNS, derived=TOPUP_DERIVED)
        topup_authority(s)
        bindings = {"top_up_id":"id", "user_id":"user_id", "original_credited_quota":"effective_credited_quota", "original_refunded_quota":"refunded_quota", "original_refunded_amount_micros":"refunded_amount_micros", "original_paid_amount_micros":"paid_amount_micros"}
        for field in TOPUP_BASIS_COLUMNS:
            exact_int(b[field])
        if any(b[key] != s[field] for key,field in bindings.items()) or b["user_id"] not in plan["user_ids"]:
            raise ValueError("topup child basis disagrees with source authority")
        if s["status"] != "success" or not (s["credited_quota"] != 0 or s["amount"] != 0) or b["original_credited_quota"] <= 0 or not 0 <= b["original_refunded_quota"] <= b["original_credited_quota"] or b["original_paid_amount_micros"] <= 0 or not 0 <= b["original_refunded_amount_micros"] <= b["original_paid_amount_micros"]:
            raise ValueError("paid topup source exceeds declared financial scope")
        if b["refundable_quota"] != scaled(b["original_credited_quota"] - b["original_refunded_quota"]) or b["rebased_debited_quota"] != 0:
            raise ValueError("topup refund arithmetic mismatch")
    for s in plan["noncash_topups"]:
        guard("top_ups", s, TOPUP_COLUMNS, derived=TOPUP_DERIVED)
        if not topup_authority(s) or s["effective_credited_quota"] != 0 or s["user_id"] not in plan["user_ids"] or s["status"] != "success" or not (s["credited_quota"] != 0 or s["amount"] != 0):
            raise ValueError("invalid noncash topup exemption")
    count("top_ups", f"r.status='success' AND r.user_id=ANY({selected}) AND (r.credited_quota<>0 OR r.amount<>0)", len(plan["refund_bases"]) + len(plan["noncash_topups"]), "paid topups")
    pending = plan["pending_bases"] + plan["blocked_pending_bases"]
    seen_orphan_owners = set()
    for b in pending:
        for field in ("top_up_id", "user_id", "original_credited_quota", "effective_credited_quota"):
            exact_int(b[field])
        s = b["source"]
        topup_authority(s)
        if b["top_up_id"] != s["id"] or b["user_id"] != s["user_id"] or b["original_credited_quota"] != s["effective_credited_quota"]:
            raise ValueError("pending child basis disagrees with source authority")
        if b.get("owner_missing_at_snapshot") is True:
            if b in plan["blocked_pending_bases"] or b["user_id"] not in plan.get("orphan_pending_user_ids", []) or not plan.get("snapshot_all_users") or s["status"] != "failed" or s["payment_provider"] != "waffo_pancake" or s["failure_reason_code"] != "checkout_timeout" or s["credited_quota"] <= 0 or s["expected_amount_micros"] <= 0 or s["settled_amount_micros"] != 0 or s["refunded_quota"] != 0 or s["refunded_amount_micros"] != 0 or b["original_credited_quota"] <= 0 or b["effective_credited_quota"] <= 0:
                raise ValueError("orphan pending payment exceeds explicitly authorized frozen scope")
            seen_orphan_owners.add(b["user_id"])
            count("users", "r.id=" + str(exact_int(b["user_id"])), 0, "orphan pending owner must remain absent")
        elif "owner_missing_at_snapshot" in b or b["user_id"] not in plan["user_ids"]:
            raise ValueError("pending payment owner is outside the selected or explicit orphan scope")
        if any(s[field] != original for field,original in (("pending_credit_rebase_key", ""), ("pending_credit_rebase_original_quota", 0), ("pending_credit_rebase_effective_quota", 0))):
            raise ValueError("pending quote already has migration markers")
        if s["status"] != "pending" and not (s["status"] == "failed" and s["payment_provider"] == "waffo_pancake" and s["failure_reason_code"] == "checkout_timeout"):
            raise ValueError("pending source exceeds recoverable payment scope")
        if b["effective_credited_quota"] != scaled(b["original_credited_quota"]):
            raise ValueError("pending credit arithmetic mismatch")
        if b in plan["blocked_pending_bases"] and (b["original_credited_quota"] != 0 or b["reason"] not in {"legacy_noncash_without_immutable_grant", "authority_zero_not_settleable"}):
            raise ValueError("invalid blocked pending quote")
        override = {"pending_credit_rebase_key": plan["migration_id"], "pending_credit_rebase_original_quota": b["original_credited_quota"], "pending_credit_rebase_effective_quota": b["effective_credited_quota"]} if after else None
        guard("top_ups", b["source"], PENDING_COLUMNS, overrides=override, derived=TOPUP_DERIVED)
    if seen_orphan_owners != set(plan.get("orphan_pending_user_ids", [])):
        raise ValueError("orphan owner list and pending child bases disagree")
    if plan["include_pending_topups"]:
        owner_scope = "true" if plan.get("snapshot_all_users") else f"r.user_id=ANY({selected})"
        count("top_ups", f"{owner_scope} AND (r.status='pending' OR (r.payment_provider='waffo_pancake' AND r.status='failed' AND r.failure_reason_code='checkout_timeout'))", len(pending), "all recoverable pending topups")
    for b in plan["referral_bases"]:
        for field in REFERRAL_BASIS_COLUMNS:
            exact_int(b[field])
        guard("referral_rewards", b["source"], REFERRAL_COLUMNS)
        if b["reward_id"] != b["source"]["id"] or b["user_id"] != b["source"]["inviter_id"] or b["user_id"] not in plan["user_ids"] or b["original_quota"] != b["source"]["quota"]:
            raise ValueError("referral child basis disagrees with source history")
        if b["source"]["status"] not in ("earned", "revoked"):
            raise ValueError("referral source exceeds supported lifecycle scope")
        for target, source in (("rebased_quota", "quota"), ("rebased_revoked_quota", "revoked_quota"), ("rebased_penalty_quota", "penalty_quota")):
            if b[target] != scaled(b["source"][source]):
                raise ValueError("referral arithmetic mismatch")
    if plan["include_affiliate"]:
        count("referral_rewards", f"r.inviter_id=ANY({selected})", len(plan["referral_bases"]), "all referral histories")
    project_entries = {e["id"]: e for e in plan["entity_updates"] if e["table"] == "open_source_bounty_projects"}
    disputes = [e for e in plan["entity_updates"] if e["table"] == "open_source_bounty_disputes"]
    owed = {rid:0 for rid in project_entries}
    for e in plan["entity_updates"]:
        table = e["table"]
        expected_writes = ENTITY_WRITES.get(table)
        source = e["source"]
        if table == "open_source_bounty_challenges":
            cases = [row["source"] for row in disputes if row["source"]["challenge_id"] == e["id"]]
            open_case = any(case["status"] == "open" for case in cases)
            resolved = any(case["status"] in ("resolved_paid", "resolved_denied") for case in cases)
            active = source["status"] in ("accepted", "submitted") or open_case or (source["status"] == "rejected" and source["rejected_at"] > plan["snapshot_at"] - 604800 and not resolved)
            expected_writes = {"reward_quota"} if active else set()
            if e.get("rights_status") != ("active_future_reward" if active else "historical_rejection_guard_only"):
                raise ValueError("challenge future reward eligibility declaration mismatch")
            if any(case["project_id"] != source["project_id"] for case in cases):
                raise ValueError("challenge and dispute project bindings disagree")
        if table not in ENTITY_COLUMNS or set(e["updates"]) != expected_writes:
            raise ValueError("unapproved entitlement write")
        if exact_int(e["id"]) <= 0 or e["id"] != e["source"]["id"]:
            raise ValueError("entitlement identity disagrees with source")
        if table == "redemptions":
            if not plan["include_redemptions"] or source["user_id"] not in plan["user_ids"] or source["status"] != 1 or source["used_user_id"] != 0 or source["deleted_at"] is not None or source["reward_type"] not in (None, "", "quota") or (source["expired_time"] and source["expired_time"] < plan["snapshot_at"]):
                raise ValueError("redemption source exceeds declared eligibility")
        elif not plan["include_bounties"]:
            raise ValueError("bounty source exceeds declared scope")
        elif table == "open_source_bounty_projects":
            if source["owner_user_id"] not in plan["user_ids"] or source["status"] not in ("published", "paused"):
                raise ValueError("bounty project source exceeds declared eligibility")
        elif table == "open_source_bounty_disputes":
            if source["project_id"] not in project_entries or source["opened_by_user_id"] not in plan["user_ids"] or source["against_user_id"] not in plan["user_ids"]:
                raise ValueError("bounty dispute source exceeds declared scope")
        elif source["project_id"] not in project_entries or source["participant_user_id"] not in plan["user_ids"] or source["paid_at"] != 0 or (source["status"] not in ("accepted", "submitted", "rejected") and not open_case):
            raise ValueError("bounty challenge source exceeds declared eligibility")
        override = {}
        for field, change in e["updates"].items():
            exact_int(change["before_credit"])
            exact_int(change["after_credit"])
            if change["before_credit"] != e["source"][field] or change["after_credit"] != scaled(change["before_credit"]):
                raise ValueError("entitlement arithmetic mismatch")
            if after:
                override[field] = change["after_credit"]
        if table == "open_source_bounty_challenges" and active:
            owed[source["project_id"]] += e["updates"]["reward_quota"]["after_credit"]
        guard(table, e["source"], ENTITY_COLUMNS[table], overrides=override)
    if any(amount > project_entries[rid]["updates"]["escrow_quota"]["after_credit"] for rid,amount in owed.items()):
        raise ValueError("future bounty rewards exceed corrected project escrow")
    at = exact_int(plan["snapshot_at"]) if any(plan[k] for k in ("include_redemptions", "include_bounties", "include_subscriptions", "include_other_rights")) else 0
    if any(plan[k] for k in ("include_redemptions", "include_bounties", "include_subscriptions", "include_other_rights")) and at <= 0:
        raise ValueError("frozen positive snapshot timestamp required")
    if plan["include_redemptions"]:
        count("redemptions", f"r.user_id=ANY({selected}) AND r.status=1 AND r.deleted_at IS NULL AND COALESCE(r.reward_type,'quota') IN ('','quota') AND (r.expired_time=0 OR r.expired_time>={at})", sum(e["table"] == "redemptions" for e in plan["entity_updates"]), "usable redemptions")
    if plan["include_bounties"]:
        projects = [e for e in plan["entity_updates"] if e["table"] == "open_source_bounty_projects"]
        count("open_source_bounty_projects", f"r.owner_user_id=ANY({selected}) AND r.status IN ('published','paused')", len(projects), "active bounties")
        project_ids = "ARRAY[" + ",".join(str(exact_int(e["id"])) for e in projects) + "]::bigint[]"
        count("open_source_bounty_challenges", f"r.project_id=ANY({project_ids}) AND r.paid_at=0 AND (r.status IN ('accepted','submitted','rejected') OR EXISTS (SELECT 1 FROM {schema}.open_source_bounty_disputes d WHERE d.challenge_id=r.id AND d.status='open'))", sum(e["table"] == "open_source_bounty_challenges" for e in plan["entity_updates"]), "unpaid bounty challenges including historical guards")
        count("open_source_bounty_disputes", f"r.project_id=ANY({project_ids})", len(disputes), "all related bounty disputes")
    sub_by_id = {e["id"]: e for e in plan["subscriptions"]}
    order_by_id = {s["id"]: s for s in plan["subscription_order_sources"]}
    paid_by_sub = {}
    for source in plan["subscription_order_sources"]:
        if source["user_id"] not in plan["user_ids"] or source["status"] not in ("pending", "success", "failed"):
            raise ValueError("subscription order source exceeds declared scope")
        if source["status"] == "success":
            sid = source["user_subscription_id"]
            if sid not in sub_by_id or sid in paid_by_sub:
                raise ValueError("paid subscription source binding incomplete or ambiguous")
            paid_by_sub[sid] = source
    for e in plan["subscriptions"]:
        s = e["source"]
        if e["id"] != s["id"] or s["user_id"] not in plan["user_ids"] or s["status"] not in ("active", "expired", "cancelled") or type(e["finite"]) is not bool:
            raise ValueError("sold subscription source exceeds declared scope")
        if s["reset_amount"] is not None or s["renewal_amount"] is not None:
            raise ValueError("sold subscription already has corrected grant markers")
        for field in ("id", "amount_total", "quota_version", "updated_at"):
            exact_int(e[field])
        for field in ("reset_amount", "renewal_amount"):
            if e[field] is not None:
                exact_int(e[field])
        paid_order = paid_by_sub.get(e["id"])
        if paid_order:
            sold = json.loads(paid_order["plan_snapshot"])
            full = exact_int(sold["total_amount"])
            if sold["id"] != s["plan_id"] or paid_order["plan_id"] != s["plan_id"] or paid_order["user_id"] != s["user_id"]:
                raise ValueError("sold subscription grant identity mismatch")
        elif s["source"] in ("admin", "balance"):
            full = s["amount_total"]
        else:
            raise ValueError("sold subscription source lacks original grant")
        if e["finite"] != (full > 0) or (full == 0) != (s["amount_total"] == 0):
            raise ValueError("finite versus unlimited subscription mismatch")
        if not paid_order and s["status"] != "active":
            raise ValueError("unpaid sold subscription exceeds catalog selection")
        if paid_order and e["finite"] and s["amount_total"] + paid_order["refunded_quota"] != full:
            raise ValueError("sold grant and historical subscription refund disagree")
        override = {k: e[k] for k in ("amount_total", "reset_amount", "renewal_amount", "quota_version", "updated_at")} if after and e["finite"] else {}
        if e["finite"] and (e["amount_total"] != s["amount_used"] + scaled(max(s["amount_total"] - s["amount_used"], 0)) or e["reset_amount"] != scaled(s["amount_total"]) or e["renewal_amount"] != scaled(full) or e["quota_version"] != s["quota_version"] + 1 or e["updated_at"] != at):
            raise ValueError("sold subscription cap/reset arithmetic mismatch")
        if not e["finite"] and any(e[key] != s[key] for key in ("amount_total", "reset_amount", "renewal_amount", "quota_version", "updated_at")):
            raise ValueError("unlimited subscription must retain original values")
        guard("user_subscriptions", s, SUB_COLUMNS, overrides=override)
    order_updates = {e["id"]: e for e in plan["subscription_order_updates"]}
    if len(order_updates) != len(plan["subscription_order_updates"]):
        raise ValueError("duplicate subscription order update")
    if set(order_updates) != {s["id"] for s in plan["subscription_order_sources"] if s["status"] == "pending"}:
        raise ValueError("all pending subscription orders require declared grant handling")
    catalog_by_id = {e["id"]: e for e in plan["subscription_plan_updates"]}
    for s in plan["subscription_order_sources"]:
        if s["status"] != "pending" and s["user_subscription_id"] not in sub_by_id:
            raise ValueError("subscription order is outside selected sold contracts")
        update = order_updates.get(s["id"])
        override = {}
        if update:
            exact_int(update["id"])
            exact_int(update["original_grant"])
            exact_int(update["effective_grant"])
            if s["user_subscription_id"] != 0:
                raise ValueError("pending order already binds a sold subscription")
            if update["source"] != s or update["effective_grant"] != scaled(update["original_grant"]):
                raise ValueError("pending subscription grant mismatch")
            if update["original_grant"] > 0 and update["effective_grant"] == 0:
                raise ValueError("finite pending subscription grant becomes unlimited sentinel")
            if update["grant_source_kind"] == "frozen_order_plan_snapshot":
                old = json.loads(s["plan_snapshot"])
                new = json.loads(update["plan_snapshot"])
                exact_int(new["total_amount"])
                if old["id"] != s["plan_id"] or update["original_grant"] != old["total_amount"]:
                    raise ValueError("pending order original grant identity mismatch")
                if new != old | {"total_amount": scaled(old["total_amount"])}:
                    raise ValueError("pending order changed non-credit snapshot facts")
                if after:
                    override["plan_snapshot"] = update["plan_snapshot"]
            elif update["grant_source_kind"] == "runtime_current_catalog_fallback":
                catalog = catalog_by_id.get(s["plan_id"])
                if catalog is None or update["catalog_plan_id"] != s["plan_id"] or update["catalog_source"] != catalog["source"] or update["original_grant"] != catalog["source"]["total_amount"] or update["effective_grant"] != catalog["total_amount"]:
                    raise ValueError("pending fallback catalog binding mismatch")
                if (s["plan_snapshot"] or "").strip() or update["plan_snapshot"] != s["plan_snapshot"]:
                    raise ValueError("fallback pending order snapshot must remain unchanged")
            else:
                raise ValueError("unknown pending grant source")
        guard("subscription_orders", s, ORDER_COLUMNS, overrides=override)
    for e in plan["subscription_plan_updates"]:
        for field in ("id", "total_amount", "updated_at"):
            exact_int(e[field])
        if e["id"] != e["source"]["id"] or e["updated_at"] != at:
            raise ValueError("subscription catalog identity or timestamp mismatch")
        if e["total_amount"] != scaled(e["source"]["total_amount"]):
            raise ValueError("subscription catalog arithmetic mismatch")
        if e["source"]["total_amount"] > 0 and e["total_amount"] == 0:
            raise ValueError("finite catalog grant becomes unlimited sentinel")
        guard("subscription_plans", e["source"], PLAN_COLUMNS, overrides={"total_amount": e["total_amount"], "updated_at": e["updated_at"]} if after else None)
    for key, columns in (("subscription_payment_events", PAYMENT_COLUMNS), ("subscription_payment_refunds", REFUND_COLUMNS)):
        for s in plan[key]:
            if s["subscription_order_id"] not in order_by_id or (key == "subscription_payment_refunds" and s["subscription_payment_event_id"] not in {p["id"] for p in plan["subscription_payment_events"]}):
                raise ValueError("subscription financial receipt binding incomplete")
            guard(key, s, columns)
    expected_bases = {}
    for e in plan["subscriptions"]:
        s = e["source"]
        order = paid_by_sub.get(e["id"])
        if not order or not e["finite"]:
            continue
        paid = order["expected_amount_micros"]
        if paid <= 0:
            amount = float(order["money"])
            if not math.isfinite(amount) or amount < 0:
                raise ValueError("invalid historical subscription payment")
            paid = exact_int(math.floor(amount * 1_000_000 + 0.5))
        if paid <= order["refunded_amount_micros"]:
            if paid < order["refunded_amount_micros"] or s["amount_total"] > s["amount_used"]:
                raise ValueError("inconsistent fully refunded subscription")
            continue
        current_events = [payment for payment in plan["subscription_payment_events"] if payment["subscription_order_id"] == order["id"] and (payment["period_start"] or 0) == (order["current_period_start"] or 0) and (payment["period_end"] or 0) == (order["current_period_end"] or 0)]
        if len(current_events) > 1 or any(payment["settlement_amount_micros"] != paid for payment in current_events):
            raise ValueError("current subscription receipt disagrees with paid amount")
        expected_bases[order["id"]] = {"subscription_order_id": order["id"], "user_subscription_id": e["id"], "user_id": s["user_id"], "period_start": order["current_period_start"] or 0, "period_end": order["current_period_end"] or 0, "subscription_end_time": s["end_time"], "original_quota_version": e["quota_version"], "original_credit_quota": s["amount_total"] + order["refunded_quota"], "original_refunded_quota": order["refunded_quota"], "original_refunded_amount_micros": order["refunded_amount_micros"], "original_paid_amount_micros": paid, "refundable_quota": scaled(max(s["amount_total"] - s["amount_used"], 0)), "reset_quota": e["reset_amount"], "reset_reduced_quota": 0, "rebased_debited_quota": 0}
    for b in plan["subscription_refund_bases"]:
        if set(b) != SUB_BASIS_COLUMNS:
            raise ValueError("complete subscription child basis fields required")
        for field in SUB_BASIS_COLUMNS:
            exact_int(b[field])
    if canonical({b["subscription_order_id"]: b for b in plan["subscription_refund_bases"]}) != canonical(expected_bases):
        raise ValueError("subscription refund child basis disagrees with source grant and payment")
    if plan["include_subscriptions"]:
        sub_scope = f"user_id=ANY({selected}) AND (status='active' OR id IN (SELECT user_subscription_id FROM {schema}.subscription_orders WHERE status='success' AND user_subscription_id>0))"
        order_scope = f"user_id=ANY({selected}) AND (status='pending' OR user_subscription_id IN (SELECT id FROM {schema}.user_subscriptions WHERE {sub_scope}))"
        count("user_subscriptions", sub_scope, len(plan["subscriptions"]), "sold subscriptions")
        count("subscription_orders", order_scope, len(plan["subscription_order_sources"]), "all subscription orders")
        count("subscription_plans", "true", len(plan["subscription_plan_updates"]), "subscription catalog")
        for key in ("subscription_payment_events", "subscription_payment_refunds"):
            count(key, f"subscription_order_id IN (SELECT id FROM {schema}.subscription_orders WHERE {order_scope})", len(plan[key]), key)
    other_conditions = {
        "public_relay_tip_pool": f"r.user_id=ANY({selected})",
        "assistant_gift": f"r.user_id=ANY({selected}) AND r.status='offered' AND r.quota>0 AND r.amount_cents>0",
        "grant_gift": f"r.enabled=true AND r.end_at>{at}",
        "ai_directory_ad_refund": f"r.owner_user_id=ANY({selected}) AND r.status='active' AND r.expires_at>{at}",
        "violation_fee_refund": f"r.user_id=ANY({selected}) AND r.status='charged' AND r.charged_quota>0",
        "email_refund_pool": f"r.user_id=ANY({selected}) AND r.charge_quota>r.refunded_quota",
    }
    challenge_entries = {e["id"]:e for e in plan["entity_updates"] if e["table"] == "open_source_bounty_challenges"}
    expected_dispute_bases = []
    for dispute in disputes:
        source = dispute["source"]
        challenge = challenge_entries.get(source["challenge_id"])
        project = project_entries.get(source["project_id"])
        if source["status"] == "open" and challenge and project:
            c,p = challenge["source"],project["source"]
            uid,owner = c["participant_user_id"],p["owner_user_id"]
            if uid == owner or uid not in plan["user_ids"] or owner not in plan["user_ids"]:
                raise ValueError("open dispute requires distinct selected parties")
            if (source["opened_by_user_id"], source["against_user_id"]) not in {(uid,owner), (owner,uid)}:
                raise ValueError("open dispute claimant and respondent binding mismatch")
            if c["status"] in ("accepted", "submitted", "rejected") and c["paid_at"] == 0 and source["opened_by_user_id"] == c["participant_user_id"] and source["against_user_id"] == p["owner_user_id"]:
                if not plan["include_other_rights"] or source["created_at"] <= 0 or source["created_at"] > at or source["resolved_at"] != 0 or source["resolved_by_user_id"] != 0 or source["reward_quota_snapshot"] <= 0 or source["reward_quota_snapshot"] != c["reward_quota"] or p["escrow_quota"] < source["reward_quota_snapshot"]:
                    raise ValueError("open dispute payout snapshot disagrees with current reward principal")
                expected_dispute_bases.append({"kind":"bounty_dispute_reward", "source_id":str(source["id"]), "user_id":c["participant_user_id"], "original_quota":source["reward_quota_snapshot"], "rebased_quota":scaled(source["reward_quota_snapshot"]), "source":source})
        elif source["status"] == "open":
            raise ValueError("open dispute project and challenge source binding incomplete")
    actual_dispute_bases = [e for e in plan["other_credit_bases"] if e["kind"] == "bounty_dispute_reward"]
    if canonical(sorted(actual_dispute_bases,key=lambda e:e["source_id"])) != canonical(sorted(expected_dispute_bases,key=lambda e:e["source_id"])):
        raise ValueError("all participant-filed open dispute payout bases are required")
    for e in plan["other_credit_bases"]:
        for field in ("user_id", "original_quota", "rebased_quota"):
            exact_int(e[field])
        if e["kind"] not in set(OTHER_SPECS) | {"bounty_dispute_reward"} or e["rebased_quota"] != scaled(e["original_quota"]):
            raise ValueError("unknown or inconsistent other credit basis")
        if e["kind"] == "bounty_dispute_reward":
            if not plan["include_bounties"]:
                raise ValueError("dispute credit basis exceeds declared bounty scope")
            guard("open_source_bounty_disputes",e["source"],ENTITY_COLUMNS["open_source_bounty_disputes"])
            continue
        table, columns = OTHER_SPECS[e["kind"]]
        guard(table, e["source"], columns)
        source = e["source"]
        original = (source["tip_quota"] - source["withdrawn_quota"] if e["kind"] == "public_relay_tip_pool" else source["charge_quota"] - source["refunded_quota"] if e["kind"] == "email_refund_pool" else source["quota"] if e["kind"] in {"assistant_gift", "grant_gift"} else source["charged_quota"])
        owner = 0 if e["kind"] == "grant_gift" else source.get("user_id", source.get("owner_user_id"))
        if e["original_quota"] != original or e["source_id"] != str(source["id"]) or e["user_id"] != owner or (owner != 0 and owner not in plan["user_ids"]):
            raise ValueError("other credit child basis disagrees with source history")
        kind = e["kind"]
        if original < 0 or (kind != "public_relay_tip_pool" and original <= 0) or (kind != "grant_gift" and (owner <= 0 or owner not in plan["user_ids"])):
            raise ValueError("invalid future credit principal or owner")
        if kind == "assistant_gift" and (source["status"] != "offered" or source["claimed_at"] != 0 or source["amount_cents"] <= 0):
            raise ValueError("assistant gift source exceeds declared eligibility")
        if kind == "grant_gift" and (source["enabled"] is not True or source["end_at"] <= at or source["start_at"] >= source["end_at"]):
            raise ValueError("grant gift source exceeds declared eligibility")
        if kind == "ai_directory_ad_refund" and (source["status"] != "active" or source["expires_at"] <= at or source["refunded_at"] != 0):
            raise ValueError("ad refund source exceeds declared eligibility")
        if kind == "violation_fee_refund" and (source["status"] != "charged" or source["reversed_at"] != 0):
            raise ValueError("fee refund source exceeds declared eligibility")
        if e["kind"] == "email_refund_pool":
            unique(e["activation_sources"], ("id",))
            if len(e["activation_sources"]) != source["quantity"] or any(s["order_id"] != e["source_id"] or s["user_id"] != owner for s in e["activation_sources"]):
                raise ValueError("email activation source scope incomplete")
            for s in e["activation_sources"]:
                guard("hero_sms_email_activations", s, ACTIVATION_COLUMNS)
            count("hero_sms_email_activations", "r.order_id=" + literal(e["source_id"]), len(e["activation_sources"]), "email activations")
            # Migration must add the nullable marker without backfilling old
            # refund ledgers. New writes are forbidden until after verification.
            marker = "r.original_amount_quota" if after else "NULLIF(to_jsonb(r)->'original_amount_quota','null'::jsonb)"
            count("hero_sms_email_quota_ledgers", "r.order_id=" + literal(e["source_id"]) + " AND " + marker + " IS NOT NULL", 0, "historical email ledger marker remains null")
    if plan["include_other_rights"]:
        for kind, (table, _) in OTHER_SPECS.items():
            count(table, other_conditions[kind], sum(e["kind"] == kind for e in plan["other_credit_bases"]), kind)
    if plan["include_bounties"]:
        count("open_source_bounty_disputes", f"r.status='open' AND EXISTS (SELECT 1 FROM {schema}.open_source_bounty_challenges c JOIN {schema}.open_source_bounty_projects p ON p.id=c.project_id WHERE c.id=r.challenge_id AND c.project_id=r.project_id AND c.paid_at=0 AND c.status IN ('accepted','submitted','rejected') AND c.participant_user_id=ANY({selected}) AND p.owner_user_id=ANY({selected}) AND p.status IN ('published','paused') AND r.opened_by_user_id=c.participant_user_id AND r.against_user_id=p.owner_user_id)", len(actual_dispute_bases), "participant-filed open dispute payout bases")

    assert_true("pg_catalog.current_database() = " + literal(plan["target"]["database"]), "target database")
    assert_true("(SELECT system_identifier::text FROM pg_catalog.pg_control_system()) = " + literal(plan["target"]["system_identifier"]), "target system identifier")
    assert_true("pg_catalog.to_regnamespace(" + literal(plan["target"]["schema"]) + ") IS NOT NULL", "target schema")
    identity_count = 3
    for key, catalog, name in (("database_oid", "pg_database", "datname"), ("schema_oid", "pg_namespace", "nspname")):
        if key in plan["target"]:
            value = plan["target"]["database" if key == "database_oid" else "schema"]
            assert_true(f"(SELECT oid::text FROM pg_catalog.{catalog} WHERE {name}={literal(value)}) = {literal(plan['target'][key])}", key)
            identity_count += 1
    identity_checks = checks[-identity_count:]
    checks = identity_checks + checks[:-identity_count]
    audit_name = literal(schema + '."wallet_credit_rebases"')
    if after:
        assert_true(f"pg_catalog.to_regclass({audit_name}) IS NOT NULL", "migration audit table")
        guard("wallet_credit_rebases", {"migration_id": plan["migration_id"], "plan_sha256": plan["plan_sha256"]}, {"migration_id", "plan_sha256"})
        assert_true(f"(SELECT plan FROM {schema}.wallet_credit_rebases WHERE migration_id={mid}) = {literal(canonical(plan))}::jsonb", "complete parent audit plan and child bases")
        assert_true(f"(SELECT applied_at IS NOT NULL AND isfinite(applied_at) AND applied_at >= to_timestamp({exact_int(plan['snapshot_at'])}) AND applied_at <= transaction_timestamp() FROM {schema}.wallet_credit_rebases WHERE migration_id={mid})", "parent audit timestamp within frozen verification interval")
        count("wallet_credit_rebases", f"EXISTS (SELECT 1 FROM jsonb_array_elements_text(r.plan->'user_ids') u(id) WHERE u.id::bigint=ANY({selected}))", 1, "single selected-user migration")
    else:
        checks.append(f"IF pg_catalog.to_regclass({audit_name}) IS NOT NULL THEN\n"
                      f"IF EXISTS (SELECT 1 FROM {schema}.wallet_credit_rebases r WHERE r.migration_id={mid} OR EXISTS (SELECT 1 FROM jsonb_array_elements_text(r.plan->'user_ids') u(id) WHERE u.id::bigint=ANY({selected}))) THEN RAISE EXCEPTION 'credit verification failed: migration audit must be absent'; END IF;\nEND IF;")
    for key, table, fields, primary in (("refund_bases", "wallet_topup_credit_rebases", TOPUP_BASIS_COLUMNS, "top_up_id"), ("referral_bases", "wallet_referral_credit_rebases", REFERRAL_BASIS_COLUMNS, "reward_id"), ("subscription_refund_bases", "subscription_order_credit_rebases", SUB_BASIS_COLUMNS, "subscription_order_id")):
        enabled = key == "refund_bases" or plan["include_affiliate" if key == "referral_bases" else "include_subscriptions"]
        if not enabled:
            continue
        if after:
            assert_true(f"pg_catalog.to_regclass({literal(schema + '.' + ident(table))}) IS NOT NULL", table + " table")
            for b in plan[key]:
                row = {f: b[f] for f in fields} | {"migration_id": plan["migration_id"]}
                if key == "referral_bases":
                    row |= {"divisor": plan["divisor"], "rounding": plan["rounding"]}
                guard(table, row, set(row))
            count(table, f"r.migration_id={mid}", len(plan[key]), table)
        else:
            ids = "ARRAY[" + ",".join(str(exact_int(b[primary])) for b in plan[key]) + "]::bigint[]"
            checks.append(f"IF pg_catalog.to_regclass({literal(schema + '.' + ident(table))}) IS NOT NULL THEN\nIF EXISTS (SELECT 1 FROM {schema}.{ident(table)} WHERE migration_id={mid} OR {ident(primary)}=ANY({ids})) THEN RAISE EXCEPTION 'credit verification failed: existing child basis'; END IF;\nEND IF;")
    delimiter = "$credit_verify_" + plan["plan_sha256"] + "$"
    body = "\n".join(checks)
    while delimiter in body:
        delimiter = delimiter[:-1] + "x$"
    return ("-- Independent offline verifier. Run only while writers remain frozen.\n"
            "-- Sensitive SQL: restrict file permissions. No row values are printed.\n"
            "\\set ON_ERROR_STOP on\nBEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;\n"
            "SET LOCAL statement_timeout = '60s';\nSET LOCAL standard_conforming_strings = on;\nSET LOCAL search_path = pg_catalog;\n"
            + "DO " + delimiter + "\nBEGIN\n" + body + "\nEND\n" + delimiter + ";\nCOMMIT;\n")


def postgres_verification_sql(plan, stage):
    validate_plan(plan)
    if plan["snapshot_state"] != "frozen_writers_stopped":
        raise ValueError("verification SQL requires a new snapshot with all writers stopped")
    return _postgres_verification_sql(plan, stage)


def planned_restorations(plan):
    """Typed original fields for a full-table preservation verifier.

    Return [{table, key: {id|key: value}, fields: {field: {before, after}}}].
    The caller must run stage assertions and its canonical fingerprints in one
    read-only transaction, and project only the original inventory's columns.
    These mappings revert only declared fields; they never erase other changes.
    """
    _postgres_verification_sql(plan, "after")
    rows = {}

    def add(table, key_field, key_value, fields):
        identity = (table, key_field, key_value)
        entry = rows.setdefault(identity, {"table":table, "key":{key_field:key_value}, "fields":{}})
        for field, change in fields.items():
            if field in entry["fields"]:
                raise ValueError("duplicate field restoration")
            scalar(change["before"])
            scalar(change["after"])
            entry["fields"][field] = change

    for e in plan["entries"]:
        add(e["table"], "id", e["id"], {e["field"]:{"before":e["before_credit"], "after":e["after_credit"]}})
    for e in plan["entity_updates"]:
        add(e["table"], "id", e["id"], {field:{"before":change["before_credit"], "after":change["after_credit"]} for field,change in e["updates"].items()})
    for e in plan["option_entries"]:
        add("options", "key", e["key"], {"value":{stage:e[stage] for stage in ("before", "after")}})
    for b in plan["pending_bases"] + plan["blocked_pending_bases"]:
        target = {"pending_credit_rebase_key":plan["migration_id"], "pending_credit_rebase_original_quota":b["original_credited_quota"], "pending_credit_rebase_effective_quota":b["effective_credited_quota"]}
        add("top_ups", "id", b["top_up_id"], {field:{"before":b["source"][field], "after":value} for field,value in target.items()})
    for e in plan["subscriptions"]:
        if e["finite"]:
            add("user_subscriptions", "id", e["id"], {field:{"before":e["source"][field], "after":e[field]} for field in ("amount_total", "reset_amount", "renewal_amount", "quota_version", "updated_at")})
    for e in plan["subscription_plan_updates"]:
        add("subscription_plans", "id", e["id"], {field:{"before":e["source"][field], "after":e[field]} for field in ("total_amount", "updated_at")})
    for e in plan["subscription_order_updates"]:
        if e["grant_source_kind"] == "frozen_order_plan_snapshot":
            add("subscription_orders", "id", e["id"], {"plan_snapshot":{"before":e["source"]["plan_snapshot"], "after":e["plan_snapshot"]}})
    return [rows[key] for key in sorted(rows,key=lambda key:(key[0],key[1],canonical(key[2]))) if rows[key]["fields"]]


def declared_audit_rows(plan):
    """Exact new audit rows which full-table after fingerprints may exclude.

    The stage after SQL verifies each complete field projection and exactly one
    row; before SQL rejects each primary-key collision and migration collision.
    `field_types` marks the only structured SQL value, the parent's JSONB plan.
    Parent applied_at is dynamic: stage SQL constrains its finite timestamptz to
    [frozen snapshot_at, verification transaction_timestamp()], not a made-up
    fixed value. Every pre-existing historical audit row must remain in hashes.
    """
    _postgres_verification_sql(plan, "after")
    rows = [{"table":"wallet_credit_rebases", "key":{"migration_id":plan["migration_id"]}, "fields":{"migration_id":plan["migration_id"], "plan_sha256":plan["plan_sha256"], "plan":plan}, "field_types":{"plan":"jsonb"}, "dynamic_fields":{"applied_at":"stage_guarded_finite_frozen_interval"}}]
    for key,table,fields,primary in (("refund_bases", "wallet_topup_credit_rebases", TOPUP_BASIS_COLUMNS, "top_up_id"), ("referral_bases", "wallet_referral_credit_rebases", REFERRAL_BASIS_COLUMNS, "reward_id"), ("subscription_refund_bases", "subscription_order_credit_rebases", SUB_BASIS_COLUMNS, "subscription_order_id")):
        for basis in plan[key]:
            values = {field:basis[field] for field in sorted(fields)} | {"migration_id":plan["migration_id"]}
            if key == "referral_bases":
                values |= {"divisor":plan["divisor"], "rounding":plan["rounding"]}
            rows.append({"table":table, "key":{primary:basis[primary]}, "fields":values})
    return rows


def summary(plan, stage=None):
    _postgres_verification_sql(plan, stage or "before")
    return {"version": 1, "kind": "independent_credit_rebase_verification", "stage": stage,
            "snapshot_state": plan["snapshot_state"], "production_apply_supported": plan["production_apply_supported"],
            "plan_sha256": plan["plan_sha256"], "business_plan_sha256": plan["business_plan_sha256"],
            "business_source_sha256": plan["business_source_sha256"],
            "selected_user_count": len(plan["user_ids"]), "selected_token_count": len(plan["token_sources"]),
            "planned_field_update_count": len(plan["entries"]) + sum(len(e["updates"]) for e in plan["entity_updates"]) + len(plan["option_entries"]) + 3 * (len(plan["pending_bases"]) + len(plan["blocked_pending_bases"])) + 5 * sum(e["finite"] for e in plan["subscriptions"]) + 2 * len(plan["subscription_plan_updates"]) + sum(e["grant_source_kind"] == "frozen_order_plan_snapshot" for e in plan["subscription_order_updates"]),
            "future_basis_count": sum(len(plan[k]) for k in ("refund_bases", "pending_bases", "blocked_pending_bases", "referral_bases", "subscription_refund_bases", "other_credit_bases")),
            "database_connection_performed": False, "sql_read_only": True}


def write_private(path, content):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, "w") as stream:
        os.fchmod(stream.fileno(), 0o600)
        stream.write(content)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plan", required=True, type=Path)
    parser.add_argument("--emit-postgres-verification-sql", choices=("before", "after"))
    parser.add_argument("--output", type=Path, help="Restricted SQL artifact; otherwise SQL goes to stdout")
    parser.add_argument("--summary-output", type=Path)
    parser.add_argument("--compare-plan", type=Path, help="Prove that a rehearsal clone changed only target identity")
    args = parser.parse_args()
    try:
        plan = load_plan(args.plan)
        result = summary(plan, args.emit_postgres_verification_sql)
        if args.compare_plan:
            result["clone_proof"] = clone_proof(plan, load_plan(args.compare_plan))
        sql = postgres_verification_sql(plan, args.emit_postgres_verification_sql) if args.emit_postgres_verification_sql else None
        if args.output and not sql:
            raise ValueError("--output requires --emit-postgres-verification-sql")
        destinations = [path.resolve() for path in (args.output, args.summary_output) if path]
        inputs = {path.resolve() for path in (args.plan, args.compare_plan) if path}
        if inputs & set(destinations) or len(set(destinations)) != len(destinations):
            raise ValueError("output paths must be distinct from the input plan and one another")
        for destination in destinations:
            if destination.exists() and any(destination.samefile(source) for source in inputs):
                raise ValueError("output must not share the input plan inode")
        if args.output:
            write_private(args.output, sql)
        if args.summary_output:
            write_private(args.summary_output, json.dumps(result, indent=2) + "\n")
    except (ValueError, TypeError, KeyError, OSError):
        parser.exit(2, "error: plan or artifact validation failed; no database connection was made\n")
    if sql and not args.output:
        sys.stdout.write(sql)
    else:
        json.dump(result, sys.stdout, indent=2)
        sys.stdout.write("\n")


if __name__ == "__main__":
    main()
