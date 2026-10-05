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
        self.snapshot["referrals"] = []
        import credit_rebase_other_rights as other
        self.snapshot["obligations"] = {key:0 for key in other.OBLIGATIONS}
        for key in ("subscriptions", "subscription_orders", "subscription_plans", "subscription_payment_events", "subscription_payment_refunds"):
            self.snapshot[key] = []
        self.snapshot["target"] = {"database": "fixture", "schema": "fixture_money", "system_identifier": "123456"}
        self.snapshot["options"] = {"USDExchangeRate": "6.8","CreditsPerUSD": "3359744", "PublicCreditsPerUSD": "100000", "LegacyPricingQuotaPerUnit": "500000", "QuotaPerUnit": "500000"}
        self.snapshot["price_review"] = {"status": "verified", "evidence": "synthetic fixture without synced prices", "option_corrections": []}
        from credit_rebase_history import USER_INTS,TOKEN_INTS
        self.snapshot["user_sources"] = [{key:0 for key in USER_INTS} | u | {"deleted_at":None} for u in self.snapshot["users"]]
        self.snapshot["token_sources"] = [{key:0 for key in TOKEN_INTS} | t | {"deleted_at":None} for t in self.snapshot["tokens"]]
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

    def test_explicit_future_entitlements_preserve_paid_history(self):
        from credit_rebase_entitlements import SPECS
        snapshot = copy.deepcopy(self.snapshot)
        def source(table, **overrides):
            row = {key: 0 for key in SPECS[table]["int"]}
            row.update({key: "" for key in SPECS[table]["text"]})
            row.update({key: None for key in SPECS[table]["null"]})
            return row | overrides
        snapshot["snapshot_at"] = 100
        snapshot["entities"] = {
            "redemptions": [source("redemptions", id=30, user_id=1, quota=680, status=1, reward_type="quota")],
            "bounty_projects": [source("open_source_bounty_projects", id=50, owner_user_id=1, escrow_quota=6800, reward_quota=680, net_reward_quota=612, platform_fee_quota=68, status="published")],
            "bounty_challenges": [source("open_source_bounty_challenges", id=60, project_id=50, participant_user_id=2, reward_quota=612, tip_quota=125, status="accepted")],"bounty_disputes":[]}
        plan = r.make_plan(snapshot, **self.kw, include_redemptions=True, include_bounties=True)
        self.assertEqual(len(plan["entity_updates"]), 3)
        for e in plan["entity_updates"]:
            self.assertNotIn("tip_quota", e["updates"])
            self.assertNotIn("platform_fee_quota", e["updates"])
        snapshot["entities"]["redemptions"][0]["reward_type"] = None
        null_plan = r.make_plan(snapshot,**self.kw,restore_fixed_anchors=True,include_redemptions=True)
        self.assertIsNone(null_plan["entity_updates"][0]["source"]["reward_type"])
        self.assertIn('"reward_type" IS NULL',r.postgres_sql(null_plan))
        snapshot["entities"]["bounty_challenges"][0]["paid_at"] = 1
        with self.assertRaises(ValueError):
            r.make_plan(snapshot, **self.kw, include_bounties=True)

    def test_bounty_rejection_window_and_dispute_liabilities(self):
        from credit_rebase_entitlements import SPECS,make_entities
        def source(table,**changes):
            return {key:0 for key in SPECS[table]["int"]} | {key:"" for key in SPECS[table]["text"]} | changes
        project=source("open_source_bounty_projects",id=50,owner_user_id=1,escrow_quota=680,reward_quota=680,net_reward_quota=680,status="published")
        active=source("open_source_bounty_challenges",id=60,project_id=50,participant_user_id=2,reward_quota=680,status="accepted")
        rejected=active|{"id":61,"status":"rejected","rejected_at":395200}
        snapshot={"snapshot_at":1000000,"entities":{"bounty_projects":[project],"bounty_challenges":[active,rejected],"bounty_disputes":[]}}
        plan=make_entities(snapshot,{1,2},lambda q:q//6,include_bounties=True)
        old=[e for e in plan if e["id"]==61][0]
        self.assertEqual(old["updates"],{})
        self.assertEqual(old["rights_status"],"historical_rejection_guard_only")
        rejected["rejected_at"] += 1
        with self.assertRaises(ValueError):make_entities(snapshot,{1,2},lambda q:q//6,include_bounties=True)
        rejected["rejected_at"] -= 1
        dispute=source("open_source_bounty_disputes",id=90,challenge_id=61,project_id=50,opened_by_user_id=2,against_user_id=1,status="open")
        snapshot["entities"]["bounty_disputes"]=[dispute]
        with self.assertRaises(ValueError):make_entities(snapshot,{1,2},lambda q:q//6,include_bounties=True)
        dispute["status"]="resolved_denied"
        rejected["rejected_at"] += 1
        plan=make_entities(snapshot,{1,2},lambda q:q//6,include_bounties=True)
        self.assertEqual([e for e in plan if e["id"]==61][0]["updates"],{})

    def test_noncash_classification_and_auxiliary_bases_are_explicit(self):
        from credit_rebase_auxiliary import REFERRAL_NUMBERS
        snapshot = copy.deepcopy(self.snapshot)
        source = dict(id=20,user_id=1,status="success",credited_quota=0,amount=2,platform_amount_micros=0,settled_amount_micros=0,expected_amount_micros=0,refunded_quota=0,refunded_amount_micros=0,money="0.28",payment_provider="epay",payment_method="epay",settlement_currency="",effective_credited_quota=0,paid_amount_micros=280000,is_legacy_linuxdo_credit_topup=True)
        snapshot["topups"] = [source]
        reward = {key:0 for key in REFERRAL_NUMBERS} | {"id":9,"inviter_id":1,"invitee_id":2,"top_up_id":20,"quota":680,"revoked_quota":680,"penalty_quota":68,"status":"revoked","reason":"abuse"}
        snapshot["referrals"] = [reward]
        pending = source | {"id":21,"status":"failed","payment_provider":"waffo_pancake","failure_reason_code":"checkout_timeout","credited_quota":680,"amount":0,"effective_credited_quota":680,"is_legacy_linuxdo_credit_topup":False,"pending_credit_rebase_key":"","pending_credit_rebase_original_quota":0,"pending_credit_rebase_effective_quota":0}
        snapshot["pending_topups"] = [pending]
        plan = r.make_plan(snapshot, **self.kw, restore_fixed_anchors=True, include_pending_topups=True, include_affiliate=True)
        self.assertEqual(len(plan["refund_bases"]),0)
        self.assertEqual(plan["noncash_topups"][0]["id"],20)
        self.assertEqual(plan["pending_bases"][0]["effective_credited_quota"],100)
        self.assertEqual(plan["referral_bases"][0]["rebased_penalty_quota"],10)
        snapshot["topups"][0]["is_legacy_linuxdo_credit_topup"] = False
        with self.assertRaises(ValueError):
            r.make_plan(snapshot, **self.kw, restore_fixed_anchors=True)
        snapshot["topups"][0].update(is_legacy_linuxdo_credit_topup=True,payment_provider="stripe",payment_method="stripe",settlement_currency="USD",credited_quota=6800)
        with self.assertRaises(ValueError):
            r.make_plan(snapshot, **self.kw, restore_fixed_anchors=True)

    def test_blocked_authority_zero_pending_quotes_remain_explicit(self):
        snapshot = copy.deepcopy(self.snapshot)
        snapshot["pending_topups"] = [dict(id=21,user_id=1,status="pending",failure_reason_code="",credited_quota=0,amount=2,platform_amount_micros=0,settled_amount_micros=0,expected_amount_micros=0,refunded_quota=0,refunded_amount_micros=0,money="0.28",payment_provider="fastpay",payment_method="alipay",settlement_currency="",effective_credited_quota=0,paid_amount_micros=280000,is_legacy_linuxdo_credit_topup=False,pending_credit_rebase_key="",pending_credit_rebase_original_quota=0,pending_credit_rebase_effective_quota=0)]
        plan = r.make_plan(snapshot,**self.kw,restore_fixed_anchors=True,include_pending_topups=True)
        self.assertEqual(plan["pending_bases"],[])
        self.assertEqual(plan["blocked_pending_bases"][0]["reason"],"authority_zero_not_settleable")
        self.assertEqual(len(plan["blocked_pending_bases"]),1)
        self.assertIn("pending_credit_rebase_original_quota=0, pending_credit_rebase_effective_quota=0",r.postgres_sql(plan))

    def test_divisor_is_frozen_production_fx(self):
        bad = copy.deepcopy(self.snapshot)
        bad["options"]["USDExchangeRate"] = "6.710363"
        with self.assertRaises(ValueError):
            r.make_plan(bad, **self.kw)
        del bad["options"]["USDExchangeRate"]
        with self.assertRaises(ValueError):
            r.make_plan(bad, **self.kw)
        plan = r.make_plan(self.snapshot, **(self.kw | {"divisor_text": "6.80"}))
        self.assertEqual(plan["fx_source"]["value"], "6.8")

    def test_clone_changes_target_only_and_retains_business_hashes(self):
        plan = r.make_plan(self.snapshot,**self.kw)
        clone = copy.deepcopy(self.snapshot)
        clone["target"] = {"database":"clone","schema":"clone_money","system_identifier":"789"}
        cloned = r.make_plan(clone,**self.kw)
        self.assertNotEqual(plan["plan_sha256"],cloned["plan_sha256"])
        self.assertNotEqual(plan["source_sha256"],cloned["source_sha256"])
        self.assertEqual(plan["business_plan_sha256"],cloned["business_plan_sha256"])
        self.assertEqual(plan["business_source_sha256"],cloned["business_source_sha256"])

    def test_production_sql_requires_explicit_frozen_writer_attestation(self):
        for state in ("unspecified", "provisional_live_not_frozen", "frozen_writers_stopped"):
            snapshot=copy.deepcopy(self.snapshot)
            snapshot["snapshot_state"]=state
            plan=r.make_plan(snapshot,**self.kw,restore_fixed_anchors=True)
            sql=r.postgres_sql(plan)
            if state=="frozen_writers_stopped":
                self.assertEqual(plan["production_apply_supported"],"reviewed_postgres_sql_only")
                self.assertNotIn("RAISE EXCEPTION 'review-only credit rebase SQL",sql)
            else:
                self.assertIs(plan["production_apply_supported"],False)
                self.assertLess(sql.index("RAISE EXCEPTION 'review-only credit rebase SQL"),sql.index("LOCK TABLE"))

    def test_obligations_cannot_be_skipped_with_other_scope_disabled(self):
        snapshot = copy.deepcopy(self.snapshot)
        snapshot["obligations"]["tasks_refund_pending"] = 1
        with self.assertRaises(ValueError):
            r.make_plan(snapshot,**self.kw,include_other_rights=False)

    def subscription_fixture(self):
        import credit_rebase_subscriptions as s
        snapshot = copy.deepcopy(self.snapshot)
        def row(ints,texts,**overrides):
            return {key:0 for key in ints} | {key:"" for key in texts} | overrides
        snapshot["snapshot_at"] = 100
        snapshot["subscriptions"] = [row(s.SUB_INT,s.SUB_TEXT,id=70,user_id=1,plan_id=4,amount_total=6800,amount_used=6120,status="active",source="order",end_time=200,reset_amount=None,renewal_amount=None)]
        snapshot["subscription_orders"] = [row(s.ORDER_INT,s.ORDER_TEXT,id=80,user_id=1,plan_id=4,user_subscription_id=70,status="success",money="0.01",expected_amount_micros=10000,plan_snapshot='{"id":4,"total_amount":6800,"waffo_pancake_product_type":"one_time"}')]
        snapshot["subscription_plans"] = [row(s.PLAN_INT,s.PLAN_TEXT,id=4,total_amount=6800,price_amount="0.01",enabled=True)]
        return snapshot

    def test_sold_subscription_current_reset_renewal_and_pending_sources(self):
        snapshot = self.subscription_fixture()
        old = copy.deepcopy(snapshot)
        snapshot["subscription_orders"].append(snapshot["subscription_orders"][0] | {"id":81,"user_subscription_id":0,"status":"pending"})
        plan = r.make_plan(snapshot, **self.kw, restore_fixed_anchors=True, include_subscriptions=True)
        sub = plan["subscriptions"][0]
        self.assertEqual((sub["amount_total"],sub["reset_amount"],sub["renewal_amount"],sub["quota_version"]),(6220,1000,1000,1))
        self.assertEqual(plan["subscription_refund_bases"][0]["refundable_quota"],100)
        self.assertEqual(plan["subscription_refund_bases"][0]["original_credit_quota"],6800)
        self.assertIn('"total_amount":1000',plan["subscription_order_updates"][0]["plan_snapshot"])
        self.assertEqual(snapshot["subscriptions"],old["subscriptions"])
        sql = r.postgres_sql(plan)
        self.assertNotIn("SET amount_used",sql)
        self.assertIn("subscription_order_credit_rebases",sql)

    def test_subscription_grants_nullable_zero_and_ambiguous_sources(self):
        snapshot = self.subscription_fixture()
        snapshot["subscriptions"][0]["reset_amount"] = 1000
        with self.assertRaises(ValueError):
            r.make_plan(snapshot,**self.kw,include_subscriptions=True)
        snapshot = self.subscription_fixture()
        snapshot["subscriptions"][0].update(amount_total=1,amount_used=1)
        snapshot["subscription_orders"][0]["plan_snapshot"] = '{"id":4,"total_amount":1}'
        sub = r.make_plan(snapshot,**self.kw,include_subscriptions=True)["subscriptions"][0]
        self.assertEqual((sub["amount_total"],sub["reset_amount"],sub["renewal_amount"]),(1,0,0))
        snapshot["subscription_orders"][0]["status"] = "pending"
        snapshot["subscription_orders"][0]["user_subscription_id"] = 0
        with self.assertRaises(ValueError):
            r.make_plan(snapshot,**self.kw,include_subscriptions=True)
        snapshot = self.subscription_fixture()
        snapshot["subscription_orders"][0]["plan_snapshot"] = ""
        with self.assertRaises(ValueError):
            r.make_plan(snapshot,**self.kw,include_subscriptions=True)
        snapshot["subscription_orders"][0].update(status="pending",user_subscription_id=0)
        snapshot["subscriptions"] = []
        plan = r.make_plan(snapshot,**self.kw,include_subscriptions=True)
        self.assertEqual(plan["subscription_order_updates"][0]["plan_snapshot"],"")
        self.assertEqual(plan["subscription_order_updates"][0]["grant_source_kind"],"runtime_current_catalog_fallback")
        self.assertEqual(plan["subscription_order_updates"][0]["effective_grant"],1000)

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
        self.assertNotIn('SET "used_quota"', sql)
        self.assertIn('UPDATE "fixture_money".options SET value = \'500000\'', sql)


if __name__ == "__main__":
    unittest.main()
