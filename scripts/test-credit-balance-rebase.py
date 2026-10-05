#!/usr/bin/env python3
import copy
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("rebase", Path(__file__).with_name("preview-credit-balance-rebase.py"))
r = importlib.util.module_from_spec(spec)
spec.loader.exec_module(r)


class RebaseTests(unittest.TestCase):
    def setUp(self):
        self.snapshot = {"version": 1, "applied_migration_ids": [], "users": [
            {"id": 1, "quota": 500000000, "aff_quota": 680, "used_quota": 123},
            {"id": 2, "quota": -86911, "aff_quota": 0}], "tokens": [
            {"id": 1, "user_id": 1, "remain_quota": 680, "unlimited_quota": False},
            {"id": 2, "user_id": 1, "remain_quota": 680, "unlimited_quota": True}]}
        self.snapshot["topups"] = []
        self.snapshot["target"] = {"database": "fixture", "schema": "fixture_money", "system_identifier": "123456"}
        self.snapshot["options"] = {"CreditsPerUSD": "3359744", "PublicCreditsPerUSD": "100000", "LegacyPricingQuotaPerUnit": "500000", "QuotaPerUnit": "500000"}
        self.snapshot["price_review"] = {"status": "verified", "evidence": "synthetic fixture without synced prices", "option_corrections": []}
        self.kw = dict(divisor_text="6.8", migration_id="rmb-balance-v1", user_ids=[1, 2], rounding="half-away-from-zero")

    def test_exact_integer_math_preserves_history_and_defaults(self):
        before = copy.deepcopy(self.snapshot)
        p = r.make_plan(self.snapshot, **self.kw)
        self.assertEqual([(e["before_credit"], e["after_credit"]) for e in p["entries"]], [(500000000, 73529412), (-86911, -12781)])
        self.assertEqual(self.snapshot, before)
        self.assertEqual(p["usd_credit_conversion"], 500000)
        self.assertEqual(p, r.make_plan(self.snapshot, **self.kw))

    def test_negative_ties_small_balances_and_maximum(self):
        from fractions import Fraction
        self.assertEqual(r.scale_credit(-5, Fraction(10), "half-away-from-zero"), -1)
        self.assertEqual(r.scale_credit(-5, Fraction(10), "toward-zero"), 0)
        self.assertEqual(r.scale_credit(1, Fraction("6.8"), "half-away-from-zero"), 0)
        q = r.MAX_QUOTA
        self.assertEqual(r.scale_credit(q, Fraction("6.8"), "toward-zero"), q * 5 // 34)

    def test_rights_require_explicit_opt_in_unlimited_tokens_unchanged(self):
        p = r.make_plan(self.snapshot, **self.kw, include_affiliate=True, include_token_limits=True)
        self.assertEqual(len(p["entries"]), 5)
        self.assertEqual([e["id"] for e in p["entries"] if e["table"] == "tokens"], [1])

    def test_repeated_migration_missing_users_invalid_inputs_rejected(self):
        cases = []
        s = copy.deepcopy(self.snapshot); s["applied_migration_ids"] = [self.kw["migration_id"]]; cases.append((s, {}))
        s = copy.deepcopy(self.snapshot); s["users"][0]["quota"] = 12.5; cases.append((s, {}))
        s = copy.deepcopy(self.snapshot); s["users"][0]["quota"] = True; cases.append((s, {}))
        s = copy.deepcopy(self.snapshot); s["users"].append(s["users"][0]); cases.append((s, {}))
        cases += [(self.snapshot, {"divisor_text": "6.8e0"}), (self.snapshot, {"divisor_text": "0"}),
                  (self.snapshot, {"user_ids": [999]}), (self.snapshot, {"user_ids": [1, 1]})]
        for snapshot, overrides in cases:
            with self.subTest(overrides=overrides), self.assertRaises(ValueError):
                r.make_plan(snapshot, **(self.kw | overrides))

    def test_fixed_anchor_requires_price_review_and_sql_requires_combined_plan(self):
        with self.assertRaises(ValueError):
            r.postgres_sql(r.make_plan(self.snapshot, **self.kw))
        bad = copy.deepcopy(self.snapshot)
        bad["price_review"] = {"status": "verified", "evidence": "", "option_corrections": []}
        with self.assertRaises(ValueError):
            r.make_plan(bad, **self.kw, restore_fixed_anchors=True)
        bad["price_review"] = {"status": "verified", "evidence": "fixture", "option_corrections": [
            {"key": "GroupRatio", "before": "{}", "after": "{}"}]}
        with self.assertRaises(ValueError):
            r.make_plan(bad, **self.kw, restore_fixed_anchors=True)

    def test_sql_is_atomic_guarded_and_does_not_rewrite_prices_history(self):
        sql = r.postgres_sql(r.make_plan(self.snapshot, **self.kw, restore_fixed_anchors=True))
        self.assertIn("BEGIN;", sql)
        self.assertIn("WHERE id = 1 AND \"quota\" = 500000000", sql)
        self.assertIn("GET DIAGNOSTICS changed = ROW_COUNT", sql)
        self.assertIn("selected user already rebased", sql)
        self.assertIn("migration already applied; no balances changed", sql)
        self.assertNotIn("used_quota", sql)
        self.assertIn('UPDATE "fixture_money".options SET value = \'500000\'', sql)


if __name__ == "__main__":
    unittest.main()
