#!/usr/bin/env python3
"""Exercise CI suite wiring/failure guards without Cargo, Go, or live services."""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPTS = Path(__file__).resolve().parent
RUNNER = SCRIPTS / "run-real-integration-gates.sh"
SUITES = {"epay":"epay_runtime_postgres", "stripe":"epay_runtime_postgres", "catalog":"ai_directory", "token-queries":"token_queries", "acquisition":"acquisition", "relay-settlement":"relay_openai_settlement_pg", "scripts":"scripts", "shared-trust":"lib", "token-cache":"lib"}
ORACLES = ("LMM_EPAY_GO_ORACLE_OUTPUT", "LMM_STRIPE_GO_ORACLE_OUTPUT", "LMM_STRIPE_SUBSCRIPTION_GO_ORACLE_OUTPUT", "LMM_STRIPE_SUBSCRIPTION_CHECKOUT_GO_ORACLE_OUTPUT", "LMM_AI_DIRECTORY_GO_ORACLE_OUTPUT", "LMM_TOKEN_PRICING_GO_ORACLE_OUTPUT", "LMM_RELAY_FUNDING_GO_VECTORS", "LMM_RELAY_PRICE_GO_VECTORS")

FAKE_CARGO = r'''#!/usr/bin/env python3
import importlib.util, json, os, sys
from pathlib import Path
args = sys.argv[1:]
scripts = Path(os.environ["LMM_SUITE_GUARD_SCRIPTS"])
spec = importlib.util.spec_from_file_location("inventory", scripts / "ignored-test-inventory.py")
module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
if "--test" in args:
    index = args.index("--test")
    target = args[index+1]
    selector = args[index+2] if args[index+2] != "--" else ""
    if "--ignored" in args:
        names, _ = module.inventory(scripts.parent / (target+".rs"))
        names = [name for name in names if selector in name]
        if "--skip" in args:
            skip = args[args.index("--skip")+1]
            names = [name for name in names if skip not in name]
    else:
        names = [selector]
else:
    target = "lib"
    selector = args[args.index("--lib")+1]
    names = [selector]
listing = "--list" in args
if os.environ.get("LMM_SUITE_GUARD_CARGO_MODE") == "omit-model-diagnostics" and target == "relay_openai_settlement_pg":
    names = [name for name in names if not name.startswith("provider_response_model_diagnostics_")]
oracles = {key:value for key,value in os.environ.items() if key.endswith("_GO_ORACLE_OUTPUT") or key in ("LMM_RELAY_FUNDING_GO_VECTORS", "LMM_RELAY_PRICE_GO_VECTORS")}
with open(os.environ["LMM_SUITE_GUARD_TRACE"], "a") as trace:
    trace.write(json.dumps({"command":"cargo", "target":target, "listing":listing, "names":names,
        "oracles":oracles, "database":os.environ.get("LMM_EPAY_TEST_DATABASE_URL"),
        "valkey":os.environ.get("LMM_EPAY_TEST_VALKEY_URL"), "relay_valkey":os.environ.get("LMM_API_TOKEN_TEST_VALKEY_URL")})+"\n")
mode = os.environ.get("LMM_SUITE_GUARD_CARGO_MODE", "pass")
if listing:
    if mode != "empty-list":
        if mode == "wrong-name": names[0] = "unrelated_renamed_test"
        print("\n".join(name+": test" for name in names))
elif mode == "zero-run":
    print("test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out;")
else:
    if target == "epay_runtime_postgres":
        required = ["LMM_STRIPE_GO_ORACLE_OUTPUT", "LMM_STRIPE_SUBSCRIPTION_GO_ORACLE_OUTPUT", "LMM_STRIPE_SUBSCRIPTION_CHECKOUT_GO_ORACLE_OUTPUT"] if "stripe_wallet::" in selector else ["LMM_EPAY_GO_ORACLE_OUTPUT"]
    else:
        required = {"ai_directory":["LMM_AI_DIRECTORY_GO_ORACLE_OUTPUT"], "token_queries":["LMM_TOKEN_PRICING_GO_ORACLE_OUTPUT"]}.get(target, [])
    if target == "relay_openai_settlement_pg": required = ["LMM_RELAY_FUNDING_GO_VECTORS", "LMM_RELAY_PRICE_GO_VECTORS"]
    if "current_go_funding_vectors" in selector: required = ["LMM_RELAY_FUNDING_GO_VECTORS"]
    assert all(Path(os.environ[key]).is_file() for key in required)
    print(f"test result: ok. {len(names)} passed; 0 failed; 0 ignored; 0 measured; 0 filtered out;")
'''

FAKE_GO = r'''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
scripts = Path(os.environ["LMM_SUITE_GUARD_SCRIPTS"])
fixtures = scripts.parent / "fixtures"
pattern = sys.argv[sys.argv.index("-run")+1] if "-run" in sys.argv else " ".join(sys.argv)
mode = os.environ.get("LMM_SUITE_GUARD_GO_MODE", "pass")
selected = []
for test, env, fixture in [
    ("relay_funding.go", "LMM_RELAY_FUNDING_GO_VECTORS", "../behavior-oracle/fixtures/relay-funding.json"),
    ("relay_price_lifecycle.go", "LMM_RELAY_PRICE_GO_VECTORS", "../behavior-oracle/fixtures/relay-price-lifecycle.json"),
    ("TestRustEpayCurrentGoOracle", "LMM_EPAY_GO_ORACLE_OUTPUT", "epay-current-go-output.json"),
    ("TestRustStripeCurrentGoOracle", "LMM_STRIPE_GO_ORACLE_OUTPUT", "stripe-current-go-output.json"),
    ("TestRustStripeSubscriptionCurrentGoOracle", "LMM_STRIPE_SUBSCRIPTION_GO_ORACLE_OUTPUT", "stripe-subscription-current-go-output.json"),
    ("TestRustStripeSubscriptionCheckoutCurrentGoOracle", "LMM_STRIPE_SUBSCRIPTION_CHECKOUT_GO_ORACLE_OUTPUT", "stripe-subscription-checkout-current-go-output.json"),
    ("TestRustAIDirectoryCurrentGoOracle", "LMM_AI_DIRECTORY_GO_ORACLE_OUTPUT", "ai-directory-current-go.json"),
    ("TestRustTokenPricingCurrentGoOracle", "LMM_TOKEN_PRICING_GO_ORACLE_OUTPUT", "token-pricing-current-go.json"),
]:
    if test not in pattern: continue
    selected.append(env)
    with open(os.environ["LMM_SUITE_GUARD_TRACE"], "a") as trace:
        trace.write(json.dumps({"command":"go", "variable":env, "oracle":os.environ[env], "fixtures":os.environ.get("LMM_STRIPE_SUBSCRIPTION_CHECKOUT_FIXTURES") if test == "TestRustStripeSubscriptionCheckoutCurrentGoOracle" else os.environ.get("LMM_EPAY_PARITY_FIXTURES")})+"\n")
    if mode == "zero-run": continue
    data = json.loads((fixtures / fixture).read_text())
    if test == "TestRustEpayCurrentGoOracle":
        inputs = json.loads(Path(os.environ["LMM_EPAY_PARITY_FIXTURES"]).read_text())
        data = {row["name"]:{} for row in inputs}
        data.update(fixture_count=str(len(inputs)), notification_signature="a"*32)
    if isinstance(data, list):
        if mode == "wrong-count": data.pop()
        if mode == "wrong-names": data[0]["name"] = data[1]["name"]
        Path(os.environ[env]).write_text(json.dumps(data))
        continue
    if mode == "wrong-count":
        field = next(key for key in ("fixture_count", "callback_statuses", "statuses", "quotes", "cases") if key in data)
        data[field] = "0" if field == "fixture_count" else []
    if mode == "wrong-names":
        if "fixture_count" in data:
            del data[inputs[0]["name"]]; data["unexpected-fixture"] = {}
        elif "cases" in data: data["cases"][0]["name"] = "unexpected-fixture"
        else: data = {}
    Path(os.environ[env]).write_text(json.dumps(data))
assert selected
print("ok synthetic Go command guard")
'''


class NewIntegrationSuiteGuards(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="lmm-ci-suite-guards-")
        self.directory = Path(self.temporary.name)
        self.bin = self.directory / "bin"
        self.bin.mkdir()
        for name, content in (("cargo", FAKE_CARGO), ("go", FAKE_GO)):
            executable = self.bin / name
            executable.write_text(content)
            executable.chmod(0o755)
        self.trace = self.directory / "trace.jsonl"

    def tearDown(self):
        self.temporary.cleanup()

    def run_suite(self, suite, **overrides):
        self.trace.unlink(missing_ok=True)
        env = dict(os.environ)
        for name in ("LMM_EPAY_TEST_DATABASE_URL", "LMM_EPAY_TEST_VALKEY_URL", "LMM_API_TOKEN_TEST_VALKEY_URL"):
            env.pop(name, None)
        env.update({
            "PATH": f"{self.bin}:{os.environ['PATH']}",
            "LMM_TEST_DATABASE_URL": "postgresql://fixture:fixture@127.0.0.1:5432/isolated",
            "LMM_AUTH_TEST_VALKEY_URL": "redis://:fixture@127.0.0.1:6379/0",
            "LMM_EPAY_PARITY_FIXTURES": str(self.directory / "wrong-input.json"),
            "LMM_SUITE_GUARD_TRACE": str(self.trace), "LMM_SUITE_GUARD_SCRIPTS":str(SCRIPTS),
            "LMM_SUITE_GUARD_CARGO_MODE": "pass", "LMM_SUITE_GUARD_GO_MODE": "pass",
        })
        for oracle in ORACLES:
            env[oracle] = str(self.directory / (oracle+"-stale.json"))
        env.update(overrides)
        return subprocess.run(["bash", str(RUNNER), suite], env=env, text=True, capture_output=True, timeout=30)

    def events(self):
        return [json.loads(line) for line in self.trace.read_text().splitlines()] if self.trace.exists() else []

    def test_each_suite_executes_its_whole_compiled_inventory(self):
        for suite, target in SUITES.items():
            with self.subTest(suite=suite):
                result = self.run_suite(suite)
                self.assertEqual(result.returncode, 0, result.stdout+result.stderr)
                commands = [event for event in self.events() if event["command"] == "cargo"]
                self.assertTrue(commands)
                self.assertTrue(all(event["target"] == target or (suite == "relay-settlement" and event["target"] == "lib") for event in commands))
                expected = [(True,4),(True,1),(False,1),(False,4)] if suite == "catalog" else [(True,len(commands[0]["names"])),(False,len(commands[0]["names"]))]
                if suite == "relay-settlement": expected = [(True,35),(False,35),(True,1),(False,1)]
                self.assertEqual([(event["listing"],len(event["names"])) for event in commands],expected)
                if suite == "relay-settlement":
                    required = {"provider_response_model_diagnostics_preserve_wire_quota_and_ledger", "provider_response_model_diagnostics_survive_settlement_recovery"}
                    for command in commands[:2]:
                        self.assertTrue(required.issubset(command["names"]))
                if suite == "stripe":
                    self.assertEqual(len(commands[0]["names"]), 12)
                    self.assertTrue(all(name.startswith("stripe_wallet::") for name in commands[0]["names"]))
                    self.assertIn("stripe_wallet::subscription_pay::stripe_current_go_subscription_checkout_reference_matches", commands[0]["names"])
                    self.assertIn("stripe_wallet::subscription_pay::stripe_subscription_checkout_gates_then_completes_persisted_plan_once", commands[0]["names"])
                if suite == "epay": self.assertFalse(any(name.startswith("stripe_wallet::") for name in commands[0]["names"]))

    def test_relay_settlement_cannot_drop_model_diagnostic_regressions(self):
        result = self.run_suite("relay-settlement", LMM_SUITE_GUARD_CARGO_MODE="omit-model-diagnostics")
        self.assertNotEqual(result.returncode, 0, result.stdout+result.stderr)
        self.assertIn("required integration test count mismatch", result.stderr)
        self.assertFalse(any(event["command"] == "cargo" and not event["listing"] for event in self.events()))

    def test_fresh_current_go_exports_replace_all_inherited_oracle_paths(self):
        for suite in ("epay", "stripe", "catalog", "token-queries", "relay-settlement"):
            with self.subTest(suite=suite):
                result = self.run_suite(suite)
                self.assertEqual(result.returncode,0,result.stdout+result.stderr)
                executed = [event for event in self.events() if event["command"] == "cargo" and not event["listing"]]
                for go in (event for event in self.events() if event["command"] == "go"):
                    self.assertNotEqual(go["oracle"],str(self.directory / (go["variable"]+"-stale.json")))
                    self.assertTrue(all(event["oracles"][go["variable"]] == go["oracle"] for event in executed))
                    if suite == "epay": self.assertTrue(go["fixtures"].endswith("/tests/fixtures/epay-current-go-input.json"))
                    if go["variable"] == "LMM_STRIPE_SUBSCRIPTION_CHECKOUT_GO_ORACLE_OUTPUT":
                        self.assertTrue(go["fixtures"].endswith("/tests/fixtures/stripe-subscription-checkout-current-go-input.json"))
                if suite in ("epay","stripe"):
                    self.assertEqual(executed[0]["database"],"postgresql://fixture:fixture@127.0.0.1:5432/isolated")
                    self.assertEqual(executed[0]["valkey"],"redis://:fixture@127.0.0.1:6379/0")

    def test_zero_compiled_or_executed_tests_never_pass(self):
        for suite in SUITES:
            for mode in ("empty-list", "zero-run", "wrong-name"):
                with self.subTest(suite=suite,mode=mode):
                    result = self.run_suite(suite,LMM_SUITE_GUARD_CARGO_MODE=mode)
                    self.assertNotEqual(result.returncode,0,result.stdout+result.stderr)
                    expected = "integration execution did not run all" if mode == "zero-run" else "required integration test"
                    self.assertIn(expected,result.stderr)

    def test_skipped_or_incomplete_go_export_never_reaches_rust_execution(self):
        for suite in ("epay","stripe","catalog","token-queries","relay-settlement"):
            for mode in ("zero-run","wrong-count","wrong-names"):
                with self.subTest(suite=suite,mode=mode):
                    result = self.run_suite(suite,LMM_SUITE_GUARD_GO_MODE=mode)
                    self.assertNotEqual(result.returncode,0)
                    self.assertFalse(any(event["command"] == "cargo" and not event["listing"] for event in self.events()))

    def test_remote_dependencies_and_aliases_are_rejected_before_any_tool(self):
        common = (("LMM_TEST_DATABASE_URL","postgresql://fixture:fixture@example.com:5432/production"),("LMM_AUTH_TEST_VALKEY_URL","redis://:fixture@example.com:6379/0"))
        for suite in SUITES:
            variables = list(common[:1] if suite in ("shared-trust", "acquisition") else common)
            if suite in ("epay","stripe"):
                variables += [("LMM_EPAY_TEST_DATABASE_URL",common[0][1]),("LMM_EPAY_TEST_VALKEY_URL",common[1][1])]
            if suite == "relay-settlement": variables += [("LMM_API_TOKEN_TEST_VALKEY_URL",common[1][1])]
            for variable,value in variables:
                with self.subTest(suite=suite,variable=variable):
                    result = self.run_suite(suite,**{variable:value})
                    self.assertNotEqual(result.returncode,0)
                    self.assertIn(variable+" must use a loopback-only isolated service",result.stderr)
                    self.assertEqual(self.events(),[])

    def test_missing_environment_never_invokes_cargo_or_go(self):
        for suite in SUITES:
            with self.subTest(suite=suite):
                result=self.run_suite(suite,LMM_TEST_DATABASE_URL="",LMM_AUTH_TEST_VALKEY_URL="")
                self.assertNotEqual(result.returncode,0)
                self.assertEqual(self.events(),[])

    def test_all_dispatch_schema_isolation_and_current_migrations_remain_wired(self):
        source = RUNNER.read_text()
        all_branch = next(line for line in source.splitlines() if line.lstrip().startswith("all)"))
        for suite in SUITES:
            self.assertIn("run_"+suite.replace("-","_")+";",all_branch)
        self.assertEqual(source.count("run_exact_migration_test current_catalog_schema "),4)
        self.assertIn("run_exact_migration_test token_management_schema token_management_schema_preserves_rows_and_rejects_weakened_guards", source)
        test=(SCRIPTS.parent/"scripts.rs").read_text().split("async fn repository_options_commit_refresh_runtime_invalidate_cache_and_redact_audit()",1)[1]
        for required in ("uuid::Uuid::new_v4().simple()",".after_connect(","SET search_path TO {schema}","DROP SCHEMA {schema} CASCADE"):
            self.assertIn(required,test)


if __name__ == "__main__":
    unittest.main()
