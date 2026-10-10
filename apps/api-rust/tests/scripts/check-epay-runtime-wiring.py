#!/usr/bin/env python3
"""Reject ePay implementation credit if its ordinary listener regresses to a shell."""

from pathlib import Path
import re
import sys


ROOT = Path(__file__).resolve().parents[4]
EPAY_ROUTES = {("POST", "/api/user/pay"), ("GET", "/api/user/epay/notify"), ("POST", "/api/user/epay/notify")}


def check_source(source):
    match = re.search(r"\blet\s+epay\s*=(.*?);", source, re.S)
    if not match:
        raise ValueError("ordinary listener has no ePay construction")
    block = re.sub(r"/\*.*?\*/|//[^\n]*", "", match[1], flags=re.S)
    for required in (r"\bepay_router\s*\(", r"\bUserTopupState::new\s*\(", r"\bPgEpayRepository::new\s*\(", r"\.with_valkey\s*\(", r"\bPgEpayGateway::new\s*\("):
        if not re.search(required, block):
            raise ValueError("ordinary ePay listener lacks a required durable adapter or cache boundary")
    if re.search(r"\b(?:Disabled|FailClosed|Unconfigured)\w*", block):
        raise ValueError("ordinary ePay listener still contains a blocker adapter")
    if not re.search(r"\.merge\s*\(\s*epay\s*\)", source):
        raise ValueError("ordinary ePay listener is not merged")


def main():
    check_source((ROOT / "apps/api-rust/src/main.rs").read_text())
    directory = ROOT / "apps/api-rust/tests/fixtures/routes"
    for filename, must_exist in (("rust-implemented-routes.tsv", True), ("rust-normal-mounted-routes.tsv", True), ("rust-mounted-fail-closed-shells.tsv", False)):
        rows = [line.split("\t") for line in (directory / filename).read_text().splitlines() if line and not line.startswith("#")]
        for method, path in EPAY_ROUTES:
            count = sum(row[:2] == [method, path] for row in rows)
            if count != int(must_exist):
                raise ValueError(f"{filename} misclassifies {method} {path}")
    tests = (ROOT / "apps/api-rust/tests/epay_runtime_postgres.rs").read_text()
    for name in ("current_go_checkout_notification_and_rejection_fixtures_match_rust", "concurrent_replays_credit_coupon_and_referral_exactly_once", "logs_and_cache_are_post_commit_effects_and_failure_cannot_unpay_an_order"):
        if not re.search(r"\basync\s+fn\s+" + name + r"\s*\(", tests):
            raise ValueError(f"required ePay capability regression is missing: {name}")
    print("ePay ordinary listener: concrete PostgreSQL gateway/repository plus Valkey; behavioral test results remain separate")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError) as error:
        print(f"ePay runtime wiring: {error}", file=sys.stderr)
        sys.exit(1)
