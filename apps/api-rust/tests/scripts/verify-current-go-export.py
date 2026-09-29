#!/usr/bin/env python3
"""Reject skipped, partial or structurally stale current-Go oracle exports."""

import argparse
import json
from pathlib import Path
import re


def verify(kind, source, shared_input=None):
    data = json.loads(Path(source).read_text())
    if kind == "stripe-subscription-checkout":
        if shared_input is None:
            raise ValueError("current Go Stripe checkout verification requires shared input")
        fixtures = json.loads(Path(shared_input).read_text())
        if not isinstance(fixtures, list) or len(fixtures) != 21:
            raise ValueError("current Go Stripe checkout input must contain 21 shared fixtures")
        names = [case.get("name") for case in fixtures]
        if any(not isinstance(name, str) or not name for name in names) or len(set(names)) != len(names):
            raise ValueError("current Go Stripe checkout fixtures need unique names")
        if not isinstance(data, list) or any(not isinstance(case, dict) for case in data) or [case.get("input") for case in data] != fixtures:
            raise ValueError("current Go Stripe checkout export must cover every shared input in order")
        fields = {"name", "input", "http_status", "response", "orders", "session_calls", "price_calls", "persisted_before_checkout", "idempotency_key_stable", "checkout_fields"}
        if any(not isinstance(case, dict) or not fields <= case.keys() or case["name"] != case["input"]["name"] for case in data):
            raise ValueError("current Go Stripe checkout export has incomplete cases")
        return len(fixtures)
    if kind == "relay-price":
        if not isinstance(data, list) or len(data) != 32 or any(not isinstance(case, dict) for case in data):
            raise ValueError("current Go relay price export must contain exactly 32 cases")
        names = [case.get("name") for case in data]
        if any(not isinstance(name, str) or not name for name in names) or len(set(names)) != 32:
            raise ValueError("current Go relay price cases need unique names")
        required = {"name", "options", "wallet", "refund", "tool", "usage", "free", "prepaid", "status", "error_code", "reserved", "settled", "actual", "model_ratio", "completion_ratio", "group_ratio", "model_price", "tool_price", "quota_unit"}
        balances = {"wallet", "token", "used"}
        if any(not required <= case.keys() or any(not isinstance(case.get(phase), dict) or not balances <= case[phase].keys() for phase in ("reserved", "settled")) for case in data):
            raise ValueError("current Go relay price export has incomplete lifecycle evidence")
        return 32
    if kind == "relay-funding":
        if not isinstance(data, list) or len(data) != 28:
            raise ValueError("current Go relay funding export must contain exactly 28 vectors")
        names = [case.get("name") for case in data]
        if any(not isinstance(name, str) or not name for name in names) or len(set(names)) != 28:
            raise ValueError("current Go relay funding vectors must have 28 unique names")
        required = {"name", "preference", "wallet", "token_quota", "token_unlimited", "grants", "force_preconsume", "budget", "grow", "actual", "refund", "delete_token_after_reserve", "source", "reserved_quota", "error_code", "error_status", "settle_error", "after_reserve", "after_final"}
        snapshot = {"wallet", "token_remain", "token_used", "subscriptions", "ledger_status", "ledger_actual_quota", "ledger_wallet_quota"}
        for case in data:
            if not required <= case.keys() or case["budget"] != 10:
                raise ValueError("current Go relay funding vector is incomplete")
            for phase in ("after_reserve", "after_final") + (("after_grow",) if case["grow"] else ()):
                if not isinstance(case.get(phase), dict) or not snapshot <= case[phase].keys():
                    raise ValueError("current Go relay funding snapshot is incomplete")
        keyed = {case["name"]:case for case in data}
        for name in ("high-balance-still-reserves", "force-high-balance-reserves"):
            if keyed.get(name, {}).get("reserved_quota") != 10 or keyed[name]["after_reserve"]["wallet"] != 5999990:
                raise ValueError("current Go relay funding export skipped high-balance reservations")
        if keyed.get("actual-strict-overflow-retains-intent", {}).get("after_final", {}).get("ledger_status") != "settling":
            raise ValueError("current Go relay funding export skipped strict overflow recovery")
        return 28
    if not isinstance(data, dict):
        raise ValueError(f"current Go {kind} export must be an object")
    if kind == "epay":
        fixtures = json.loads(Path(shared_input).read_text())
        if not isinstance(fixtures, list) or not fixtures:
            raise ValueError("current Go ePay fixture input must be a nonempty list")
        names = [fixture.get("name") for fixture in fixtures]
        if any(not isinstance(name, str) or not name for name in names) or len(set(names)) != len(names):
            raise ValueError("current Go ePay fixture names must be nonempty and unique")
        metadata = {"fixture_count", "notification_signature"}
        if data.get("fixture_count") != str(len(fixtures)):
            raise ValueError("current Go ePay export did not execute every shared fixture")
        if set(names) & metadata or set(data) != set(names) | metadata:
            raise ValueError("current Go ePay export does not contain the exact shared fixture set")
        if not isinstance(data.get("notification_signature"), str) or not re.fullmatch(r"[0-9a-f]{32}", data["notification_signature"]):
            raise ValueError("current Go ePay export is missing the independent notification signature")
        return len(fixtures)
    if kind == "stripe":
        if data.get("callback_statuses") != [400, 200, 200] or data.get("refund_statuses") != [200, 200, 200]:
            raise ValueError("current Go Stripe export did not execute all callback/refund cases")
        if not data.get("checkout_fields") or data.get("persisted_before_checkout") is not True:
            raise ValueError("current Go Stripe export has no durable checkout evidence")
        return 6
    if kind == "stripe-subscription":
        if data.get("statuses") != [200] * 10:
            raise ValueError("current Go Stripe subscription export did not execute all lifecycle cases")
        failure = data.get("receipt_failure", {})
        if failure.get("status_on_failure") != 500 or failure.get("status_on_retry") != 200:
            raise ValueError("current Go Stripe subscription export missed the receipt-failure retry")
        if not data.get("plan_snapshot") or not data.get("renewed"):
            raise ValueError("current Go Stripe subscription export is missing the purchased snapshot")
        return 12
    if kind == "catalog":
        if len(data.get("quotes", [])) != 60 or len(data.get("urls", [])) != 22:
            raise ValueError("current Go AI directory export must contain 60 quotes and 22 URL vectors")
        if any(not isinstance(row, dict) or not {"bid_cents", "unit", "quota", "error"} <= row.keys() for row in data["quotes"]):
            raise ValueError("current Go AI directory quote vectors are incomplete")
        return 82
    if kind == "token-pricing":
        required = {"default-discount", "group-override", "two-groups", "model-limit", "wildcard-limit", "expression"}
        cases = data.get("cases", [])
        if len(cases) != len(required) or {case.get("name") for case in cases} != required:
            raise ValueError("current Go token pricing export is missing configured scope cases")
        if any(not case.get("entries") for case in cases) or len(data.get("catalog", [])) != 10:
            raise ValueError("current Go token pricing export is missing priced catalog entries")
        if len(data.get("float_json", [])) != 14 or not data.get("options", {}).get("CacheRatio"):
            raise ValueError("current Go token pricing export has no shared cache options or float vectors")
        return len(cases)
    raise ValueError(f"unknown current Go oracle: {kind}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("kind")
    parser.add_argument("source", type=Path)
    parser.add_argument("--shared-input", type=Path)
    args = parser.parse_args()
    try:
        count = verify(args.kind, args.source, args.shared_input)
    except (OSError, ValueError, TypeError, KeyError) as error:
        raise SystemExit(str(error)) from error
    print(f"current Go {args.kind} oracle verified: {count} cases")


if __name__ == "__main__":
    main()
