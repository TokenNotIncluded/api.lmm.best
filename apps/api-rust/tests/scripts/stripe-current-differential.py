#!/usr/bin/env python3
"""Rebuild current Go Stripe wallet/subscription references, then compare Rust/PG.

Both providers are loopback HTTP fixtures. Go uses its in-memory SQLite test
database and Rust uses disposable PostgreSQL/Valkey, never production data.
"""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[4]
    output = args.output_dir.resolve()
    output.mkdir(parents=True, exist_ok=True)
    reference = output / "current-go-stripe.json"
    subscription_reference = output / "current-go-stripe-subscription.json"
    checkout_reference = output / "current-go-stripe-subscription-checkout.json"
    for path in (reference, subscription_reference, checkout_reference):
        path.unlink(missing_ok=True)
    env = dict(os.environ, GOMAXPROCS="2", LMM_STRIPE_GO_ORACLE_OUTPUT=str(reference),
               LMM_STRIPE_SUBSCRIPTION_GO_ORACLE_OUTPUT=str(subscription_reference),
               LMM_STRIPE_SUBSCRIPTION_CHECKOUT_GO_ORACLE_OUTPUT=str(checkout_reference))
    go_tests = {"TestRustStripeCurrentGoOracle", "TestRustStripeSubscriptionCurrentGoOracle",
                "TestRustStripeSubscriptionCheckoutCurrentGoOracle"}
    with (output / "go.log").open("w") as log:
        subprocess.run(["go", "test", "-json", "-p", "1", "./controller", "-run",
                        "^(TestRustStripeCurrentGoOracle|TestRustStripeSubscriptionCurrentGoOracle|TestRustStripeSubscriptionCheckoutCurrentGoOracle)$", "-count=1"],
                       cwd=root / "apps/api-go", env=env, stdout=log, stderr=subprocess.STDOUT, check=True)
    events = []
    for line in (output / "go.log").read_text().splitlines():
        try:
            events.append(json.loads(line))
        except json.JSONDecodeError:
            continue
    passed = {event.get("Test") for event in events if event.get("Action") == "pass"}
    if not go_tests.issubset(passed):
        raise RuntimeError("Go must execute all three reference exporters without skipping; see go.log")
    data = json.loads(reference.read_text())
    if data.get("callback_statuses") != [400, 200, 200] or data.get("refund_statuses") != [200, 200, 200]:
        raise RuntimeError("current Go Stripe reference did not complete its payment/refund cases")
    subscription = json.loads(subscription_reference.read_text())
    if subscription.get("statuses") != [200] * 10 or subscription.get("receipt_failure") != {
        "status_on_failure": 500,
        "order_status_on_failure": "success",
        "new_entitlements_on_failure": 1,
        "receipt_count_on_failure": 0,
        "status_on_retry": 200,
        "new_entitlements_after_retry": 1,
        "receipt_count_after_retry": 1,
        "used_quota_after_retry": 31,
    }:
        raise RuntimeError("current Go Stripe reference did not complete subscription lifecycle/fault cases")
    checkout = json.loads(checkout_reference.read_text())
    inputs = json.loads((root / "apps/api-rust/tests/fixtures/stripe-subscription-checkout-current-go-input.json").read_text())
    if not inputs or not isinstance(checkout, list) or [case.get("input") for case in checkout] != inputs:
        raise RuntimeError("current Go Stripe checkout reference must cover every shared input in order")
    metadata = {"go_revision": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
                "go_database": "in-memory SQLite", "rust_database": "isolated PostgreSQL 18",
                "provider_network": "loopback only", "production_access": False,
                "rust_comparisons": 3, "subscription_lifecycle_callbacks": 10,
                "subscription_receipt_failure_retries": 2, "subscription_checkout_cases": len(inputs)}
    (output / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
    for name, test in (
        ("wallet", "stripe_wallet::stripe_current_go_checkout_settlement_and_refund_reference_matches"),
        ("subscription", "stripe_wallet::stripe_current_go_subscription_reference_matches"),
        ("subscription-checkout", "stripe_wallet::subscription_pay::stripe_current_go_subscription_checkout_reference_matches"),
    ):
        command = [sys.executable, str(Path(__file__).with_name("with-local-services.py")),
                   "--output-dir", str(output / f"services-{name}"), "--", "cargo", "test", "--locked", "--test", "epay_runtime_postgres",
                   test, "--", "--exact", "--ignored", "--test-threads=1"]
        log_path = output / f"rust-{name}.log"
        with log_path.open("w") as log:
            subprocess.run(command, cwd=root / "apps/api-rust", env=env, stdout=log, stderr=subprocess.STDOUT, check=True)
        if not re.search(r"test result: ok\. 1 passed; 0 failed; 0 ignored;", log_path.read_text()):
            raise RuntimeError(f"Rust must execute exactly one passing Stripe {name} comparison; see {log_path}")
    print(f"Current-Go/Rust Stripe differential passed: {output}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
