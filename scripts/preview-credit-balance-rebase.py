#!/usr/bin/env python3
"""Produce an offline review artifact. Never connects to or updates a database."""
import argparse
from fractions import Fraction
import hashlib
import json
from pathlib import Path
import re
import sys

MAX_QUOTA = (1 << 53) - 1


def integer(value, label, *, positive=False):
    if type(value) is not int or abs(value) > MAX_QUOTA or (positive and value <= 0):
        raise ValueError(f"{label} must be an exact {'positive ' if positive else ''}safe integer")
    return value


def scale_credit(value, divisor, rounding):
    """Integer arithmetic, including negative wallet balances; no float conversion."""
    amount = abs(value) * divisor.denominator
    result, remainder = divmod(amount, divisor.numerator)
    if rounding == "half-away-from-zero" and remainder * 2 >= divisor.numerator:
        result += 1
    return -result if value < 0 else result


def make_plan(snapshot, *, divisor_text, migration_id, user_ids, rounding,
              include_affiliate=False, include_token_limits=False, restore_fixed_anchors=False):
    if not re.fullmatch(r"[0-9]+(?:\.[0-9]{1,18})?", divisor_text):
        raise ValueError("divisor must be an explicit positive decimal, not a float or expression")
    divisor = Fraction(divisor_text)
    if divisor <= 1:
        raise ValueError("divisor must be greater than 1 for a balance reduction")
    if rounding not in ("half-away-from-zero", "toward-zero"):
        raise ValueError("choose an explicit supported rounding policy")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,99}", migration_id):
        raise ValueError("invalid migration id")
    if not user_ids or len(set(user_ids)) != len(user_ids):
        raise ValueError("provide a nonempty unique explicit user id list")
    selected = {integer(i, "user id", positive=True) for i in user_ids}
    if not isinstance(snapshot, dict) or snapshot.get("version") != 1:
        raise ValueError("snapshot version must be 1")
    applied = snapshot.get("applied_migration_ids")
    if not isinstance(applied, list) or any(not isinstance(i, str) for i in applied):
        raise ValueError("snapshot must include applied_migration_ids (verified audit ids)")
    if migration_id in applied:
        raise ValueError("migration id already applied; refuse a second balance reduction")
    for table in ("users", "tokens"):
        if not isinstance(snapshot.get(table), list):
            raise ValueError(f"snapshot must include {table} array")
    option_entries = []
    if restore_fixed_anchors:
        options = snapshot.get("options", {})
        for key in ("CreditsPerUSD", "PublicCreditsPerUSD", "LegacyPricingQuotaPerUnit", "QuotaPerUnit"):
            if not isinstance(options.get(key), str):
                raise ValueError(f"fixed-anchor plan requires exact current options.{key} string")
            option_entries.append({"key": key, "before": options[key], "after": "500000"})
        review = snapshot.get("price_review", {})
        if review.get("status") != "verified" or not isinstance(review.get("evidence"), str) or not review["evidence"].strip():
            raise ValueError("fixed-anchor switch requires independently verified price review evidence")
        corrections = review.get("option_corrections")
        if not isinstance(corrections, list):
            raise ValueError("price_review must explicitly list option_corrections (or empty after verification)")
        allowed = {"ModelRatio", "ModelPrice", "billing_setting.billing_expr", "tool_price_setting.prices"}
        seen_options = set()
        for correction in corrections:
            key = correction.get("key")
            if key not in allowed or key in seen_options:
                raise ValueError("unsupported or duplicate price correction key")
            seen_options.add(key)
            if not isinstance(correction.get("before"), str) or not isinstance(correction.get("after"), str):
                raise ValueError("price correction before/after must be exact option strings")
            option_entries.append({"key": key, "before": correction["before"], "after": correction["after"]})
    entries = []
    seen_users, seen_tokens = set(), set()

    def entry(table, row_id, field, value):
        value = integer(value, f"{table}.{field}")
        after = scale_credit(value, divisor, rounding)
        entries.append({"table": table, "id": row_id, "field": field,
                        "before_credit": value, "after_credit": after,
                        "delta_credit": after - value})

    for user in snapshot["users"]:
        uid = integer(user["id"], "user id", positive=True)
        if uid in seen_users:
            raise ValueError("duplicate user id in snapshot")
        seen_users.add(uid)
        if uid not in selected:
            continue
        entry("users", uid, "quota", user["quota"])
        if include_affiliate:
            entry("users", uid, "aff_quota", user["aff_quota"])
    if selected - seen_users:
        raise ValueError("selected users missing from snapshot")
    for token in snapshot["tokens"]:
        tid = integer(token["id"], "token id", positive=True)
        if tid in seen_tokens:
            raise ValueError("duplicate token id in snapshot")
        seen_tokens.add(tid)
        uid = integer(token["user_id"], "token user id", positive=True)
        if include_token_limits and uid in selected:
            if type(token.get("unlimited_quota")) is not bool:
                raise ValueError("token unlimited_quota must be a boolean")
            if not token["unlimited_quota"]:
                entry("tokens", tid, "remain_quota", token["remain_quota"])
    entries.sort(key=lambda e: (e["table"], e["id"], e["field"]))
    source_digest = hashlib.sha256(json.dumps(snapshot, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    plan = {"version": 1, "kind": "offline_credit_balance_rebase_preview",
            "migration_id": migration_id, "source_sha256": source_digest,
            "usd_credit_conversion": 500000, "divisor": divisor_text,
            "exact_factor": {"numerator": divisor.denominator, "denominator": divisor.numerator},
            "rounding": rounding, "user_ids": sorted(selected),
            "include_affiliate": include_affiliate, "include_token_limits": include_token_limits,
            "price_review_evidence": snapshot.get("price_review", {}).get("evidence") if restore_fixed_anchors else None,
            "entries": entries, "option_entries": option_entries, "restore_fixed_anchors": restore_fixed_anchors, "wallet_totals": {
                k: sum(e[k] for e in entries if e["table"] == "users" and e["field"] == "quota")
                for k in ("before_credit", "after_credit", "delta_credit")},
            "production_apply_supported": "reviewed_postgres_sql_only",
            "required_before_apply": ["Confirm affected users, exact divisor, rounding and non-wallet rights scope",
                "Stop all writers; drain reservations, pending settlement and refunds",
                "Resolve pending legacy payment callbacks and escrow rights",
                "Back up database and verify restore; persist unique migration audit and before/after values atomically",
                "Compare every planned before value; abort transaction on any mismatch",
                "Invalidate affected user/token caches before reopening writers"]}
    plan["plan_sha256"] = hashlib.sha256(json.dumps(plan, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    return plan



def sql_literal(value):
    return "'" + value.replace("'", "''") + "'"


def postgres_sql(plan):
    """Render guarded SQL only; no connection or automatic invocation exists."""
    if not plan.get("restore_fixed_anchors"):
        raise ValueError("SQL requires a combined fixed-anchor and reviewed-price plan")
    plan_json = sql_literal(json.dumps(plan, sort_keys=True, separators=(",", ":")))
    mid, digest = sql_literal(plan["migration_id"]), sql_literal(plan["plan_sha256"])
    user_ids = ",".join(str(uid) for uid in plan["user_ids"])
    updates = []
    for e in plan["entries"]:
        # All table/column identifiers come exclusively from make_plan's allowlist.
        table, field = e["table"], e["field"]
        updates.append(f'UPDATE public."{table}" SET "{field}" = {e["after_credit"]} '
                       f'WHERE id = {e["id"]} AND "{field}" = {e["before_credit"]};\n'
                       "GET DIAGNOSTICS changed = ROW_COUNT;\n"
                       f"IF changed <> 1 THEN RAISE EXCEPTION 'before-value mismatch: {table} id {e['id']} {field}'; END IF;")
    for e in plan["option_entries"]:
        updates.append(f"UPDATE public.options SET value = {sql_literal(e['after'])} "
                       f"WHERE key = {sql_literal(e['key'])} AND value = {sql_literal(e['before'])};\n"
                       "GET DIAGNOSTICS changed = ROW_COUNT;\n"
                       f"IF changed <> 1 THEN RAISE EXCEPTION 'option before-value mismatch: {e['key']}'; END IF;")
    statements = "\n".join(updates)
    return f"""-- OFFLINE operation: reviewed plan; stop ALL writers and drain/cache reset first.
-- No application startup or deployment hook may execute this artifact.
\\set ON_ERROR_STOP on
BEGIN;
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '60s';
SET LOCAL search_path = pg_catalog, public;
SELECT pg_advisory_xact_lock(500000, 680001);
LOCK TABLE public.users, public.tokens, public.options IN ACCESS EXCLUSIVE MODE;
CREATE TABLE IF NOT EXISTS public.wallet_credit_rebases (
    migration_id text PRIMARY KEY,
    plan_sha256 text NOT NULL,
    plan jsonb NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
);
DO $credit_rebase$
DECLARE existing_hash text; changed bigint;
BEGIN
    SELECT plan_sha256 INTO existing_hash FROM public.wallet_credit_rebases WHERE migration_id = {mid};
    IF FOUND THEN
        IF existing_hash <> {digest} THEN RAISE EXCEPTION 'migration id already bound to a different plan'; END IF;
        RAISE NOTICE 'migration already applied; no balances changed';
        RETURN;
    END IF;
    IF EXISTS (
        SELECT 1 FROM public.wallet_credit_rebases r,
        LATERAL jsonb_array_elements_text(r.plan->'user_ids') AS u(id)
        WHERE u.id::bigint = ANY(ARRAY[{user_ids}]::bigint[])
    ) THEN RAISE EXCEPTION 'selected user already rebased under another migration id'; END IF;
{statements}
    INSERT INTO public.wallet_credit_rebases (migration_id, plan_sha256, plan)
    VALUES ({mid}, {digest}, {plan_json}::jsonb);
END
$credit_rebase$;
COMMIT;
-- Keep writers stopped: reset affected wallet/token caches, verify balance/audit, then reopen.
"""


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--snapshot", required=True, type=Path)
    parser.add_argument("--divisor", required=True)
    parser.add_argument("--migration-id", required=True)
    parser.add_argument("--user-id", required=True, action="append", type=int)
    parser.add_argument("--rounding", required=True, choices=("half-away-from-zero", "toward-zero"))
    parser.add_argument("--include-affiliate", action="store_true")
    parser.add_argument("--include-token-limits", action="store_true")
    parser.add_argument("--restore-fixed-anchors", action="store_true", help="Combine anchors and independently reviewed price corrections")
    parser.add_argument("--emit-postgres-sql", action="store_true", help="Render SQL only; never execute it")
    parser.add_argument("--reviewed-plan-sha256", help="Exact hash from a previously reviewed JSON preview")
    args = parser.parse_args()
    try:
        snapshot = json.loads(args.snapshot.read_text(), parse_constant=lambda s: (_ for _ in ()).throw(ValueError(s)))
        plan = make_plan(snapshot, divisor_text=args.divisor, migration_id=args.migration_id,
                         user_ids=args.user_id, rounding=args.rounding,
                         include_affiliate=args.include_affiliate, include_token_limits=args.include_token_limits, restore_fixed_anchors=args.restore_fixed_anchors)
    except (ValueError, KeyError, TypeError, OSError) as error:
        parser.exit(2, f"error: {error}\n")
    if args.emit_postgres_sql:
        if args.reviewed_plan_sha256 != plan["plan_sha256"]:
            parser.exit(2, "error: SQL generation requires the exact reviewed preview hash\n")
        try:
            sql = postgres_sql(plan)
        except ValueError as error:
            parser.exit(2, f"error: {error}\n")
        sys.stdout.write(sql)
        return
    json.dump(plan, sys.stdout, indent=2, ensure_ascii=False)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
