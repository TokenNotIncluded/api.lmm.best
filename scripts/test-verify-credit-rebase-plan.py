#!/usr/bin/env python3
"""Offline contract tests; fixtures contain synthetic financial facts only."""
import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("credit_verifier", Path(__file__).with_name("verify-credit-rebase-plan.py"))
v = importlib.util.module_from_spec(spec)
spec.loader.exec_module(v)


def seal(plan):
    plan["business_plan_sha256"] = v.digest(v.business_plan(plan))
    plan["plan_sha256"] = v.digest({k: value for k, value in plan.items() if k != "plan_sha256"})
    return plan


def source(columns, texts=(), nullable=(), booleans=(), **overrides):
    row = {key: 0 for key in columns}
    row.update({key: "" for key in texts})
    row.update({key: None for key in nullable})
    row.update({key: False for key in booleans})
    row.update(overrides)
    return row


def fixture_plan():
    user = source(v.USER_COLUMNS, nullable=("deleted_at",), id=1, quota=6800, aff_quota=680, used_quota=123, status=1)
    debt_user = user | {"id": 2, "quota": -86911, "aff_quota": 0, "used_quota": 45}
    token = source(v.TOKEN_COLUMNS, nullable=("deleted_at",), booleans=("unlimited_quota",), id=10, user_id=1, remain_quota=680, used_quota=40, status=1)
    unlimited = token | {"id":11, "user_id":2, "unlimited_quota":True, "used_quota":80}
    topup = source(v.TOPUP_COLUMNS, texts=("status", "money", "payment_provider", "payment_method", "settlement_currency"), id=20, user_id=1, status="success", credited_quota=6800, settled_amount_micros=1000, expected_amount_micros=1000, refunded_quota=680, refunded_amount_micros=100, money="0.001", payment_provider="stripe", payment_method="stripe", settlement_currency="USD")
    topup |= {"effective_credited_quota":6800, "paid_amount_micros":1000, "is_legacy_linuxdo_credit_topup":False}
    noncash = topup | {"id":22,"credited_quota":0,"amount":2,"settled_amount_micros":0,"expected_amount_micros":0,"refunded_quota":0,"refunded_amount_micros":0,"money":"0.28","payment_provider":"epay","payment_method":"linuxdo_credit","settlement_currency":"","effective_credited_quota":0,"paid_amount_micros":280000,"is_legacy_linuxdo_credit_topup":True,"classification_reason":"existing_isLegacyLinuxDOCreditTopUp"}
    pending = topup | {"id":21,"status":"pending","credited_quota":680,"refunded_quota":0,"refunded_amount_micros":0,"effective_credited_quota":680,"failure_reason_code":"","pending_credit_rebase_key":"","pending_credit_rebase_original_quota":0,"pending_credit_rebase_effective_quota":0}
    blocked = noncash | {"id":23,"amount":0,"status":"failed","payment_provider":"waffo_pancake","failure_reason_code":"checkout_timeout","is_legacy_linuxdo_credit_topup":False,"pending_credit_rebase_key":"","pending_credit_rebase_original_quota":0,"pending_credit_rebase_effective_quota":0}
    blocked.pop("classification_reason")
    referral = source(v.REFERRAL_COLUMNS, texts=("status","reason"), id=9, inviter_id=1, invitee_id=2, top_up_id=20, quota=680, revoked_quota=68, penalty_quota=68, revision=1, status="earned")
    red = source(v.ENTITY_COLUMNS["redemptions"], texts=("reward_type",), nullable=("deleted_at",), id=30, user_id=1, quota=680, status=1, reward_type="quota")
    bounty = source(v.ENTITY_COLUMNS["open_source_bounty_projects"], texts=("status",), id=50, owner_user_id=1, escrow_quota=6800, reward_quota=680, net_reward_quota=612, platform_fee_quota=68, status="published")
    challenge = source(v.ENTITY_COLUMNS["open_source_bounty_challenges"], texts=("status",), id=60, project_id=50, participant_user_id=2, reward_quota=612, tip_quota=125, status="accepted")
    sub = source(v.SUB_COLUMNS, texts=("status","source"), nullable=("reset_amount","renewal_amount"), id=70, user_id=1, plan_id=4, amount_total=6800, amount_used=6120, end_time=200, status="active", source="order")
    order = source(v.ORDER_COLUMNS, texts=("status","money","plan_currency","plan_snapshot","settlement_currency","payment_method","payment_provider","provider_subscription_state"), nullable=("current_period_start","current_period_end"), id=80, user_id=1, plan_id=4, user_subscription_id=70, status="success", money="0.01", expected_amount_micros=10000, plan_snapshot='{"id":4,"total_amount":6800,"waffo_pancake_product_type":"one_time"}')
    pending_order = order | {"id":81, "user_subscription_id":0, "status":"pending"}
    fallback_order = pending_order | {"id":82, "plan_snapshot":None}
    catalog = source(v.PLAN_COLUMNS, texts=("price_amount","currency","quota_reset_period"), booleans=("enabled",), id=4, total_amount=6800, price_amount="0.01", enabled=True)
    payment = source(v.PAYMENT_COLUMNS, texts=("payment_provider","provider_event_id","provider_transaction_id","settlement_currency"), nullable=("period_start","period_end"), id=90, subscription_order_id=80, settlement_amount_micros=10000, payment_provider="stripe", provider_event_id="synthetic-event")
    refund = source(v.REFUND_COLUMNS, texts=("payment_provider","provider_event_id","currency"), id=91, subscription_order_id=80, subscription_payment_event_id=90, amount_micros=100, finance_ledger_entry_id=1, payment_provider="stripe", provider_event_id="synthetic-refund")
    other = []
    configs = {
        "public_relay_tip_pool": {"id":100,"user_id":1,"tip_quota":680,"withdrawn_quota":68,"status":"active"},
        "assistant_gift": {"id":101,"user_id":1,"quota":680,"amount_cents":1,"status":"offered"},
        "grant_gift": {"id":102,"quota":680,"start_at":1,"end_at":2000000,"enabled":True},
        "ai_directory_ad_refund": {"id":103,"owner_user_id":1,"charged_quota":680,"bid_cents":1,"expires_at":2000000,"status":"active"},
        "violation_fee_refund": {"id":104,"user_id":1,"charged_quota":680,"requested_quota":680,"status":"charged","error_code":""},
        "email_refund_pool": {"id":"synthetic-order","user_id":1,"charge_quota":680,"refunded_quota":68,"quantity":1,"last_refund_ledger_id":7,"status":"completed","operation":"purchase"},
    }
    for kind, (_, columns) in v.OTHER_SPECS.items():
        s = source(columns, **configs[kind])
        original = 612 if kind in {"public_relay_tip_pool","email_refund_pool"} else 680
        e = {"kind":kind,"source_id":str(s["id"]),"user_id":0 if kind == "grant_gift" else 1,"original_quota":original,"rebased_quota":90 if original == 612 else 100,"source":s}
        if kind == "email_refund_pool":
            e["activation_sources"] = [source(v.ACTIVATION_COLUMNS, id="synthetic-activation",order_id=s["id"],user_id=1,charge_quota=680,refund_quota=68,status="completed",cancel_reason="")]
        other.append(e)
    plan = {"version":1,"kind":"offline_credit_balance_rebase_preview","migration_id":"synthetic-v1","target":{"database":"postgres","schema":"fixture_credit_verify","system_identifier":"0"},"source_sha256":"1"*64,"business_source_sha256":"2"*64,"usd_credit_conversion":500000,"divisor":"6.8","exact_factor":{"numerator":5,"denominator":34},"rounding":"half-away-from-zero","fx_source":{"kind":"frozen_production_option","key":"USDExchangeRate","value":"6.8"},"snapshot_at":1000000,"user_ids":[1,2],"restore_fixed_anchors":True}
    for key in ("include_affiliate","include_token_limits","include_pending_topups","include_redemptions","include_bounties","include_subscriptions","include_other_rights"):
        plan[key] = True
    plan["snapshot_state"] = "frozen_writers_stopped"
    plan["production_apply_supported"] = "reviewed_postgres_sql_only"
    plan["obligations"] = {key:0 for key in v.OBLIGATIONS}
    plan.update(user_sources=[user,debt_user],token_sources=[token,unlimited],entries=[])
    for table,rid,field,before,after in [("users",1,"quota",6800,1000),("users",1,"aff_quota",680,100),("users",2,"quota",-86911,-12781),("users",2,"aff_quota",0,0),("tokens",10,"remain_quota",680,100)]:
        e = {"table":table,"id":rid,"field":field,"before_credit":before,"after_credit":after,"delta_credit":after-before}
        if table == "tokens":
            e["user_id"] = 1
        plan["entries"].append(e)
    plan["option_entries"] = [{"key":key,"before":"3400000","after":"500000"} for key in sorted(v.ANCHORS)] + [{"key":"ModelPrice","before":'{"synthetic":2}',"after":'{"synthetic":1}'}]
    plan["option_guards"] = [{"key":"USDExchangeRate","value":"6.8","absent":False},{"key":"ModelRatio","value":'{}',"absent":False},{"key":"tool_price_setting.prices","absent":True}]
    plan["refund_bases"] = [{"source":topup,"top_up_id":20,"user_id":1,"original_credited_quota":6800,"original_refunded_quota":680,"original_refunded_amount_micros":100,"original_paid_amount_micros":1000,"refundable_quota":900,"rebased_debited_quota":0}]
    plan["noncash_topups"] = [noncash]
    plan["pending_bases"] = [{"source":pending,"top_up_id":21,"user_id":1,"original_credited_quota":680,"effective_credited_quota":100}]
    plan["blocked_pending_bases"] = [{"source":blocked,"top_up_id":23,"user_id":1,"original_credited_quota":0,"effective_credited_quota":0,"reason":"authority_zero_not_settleable","future_settlement":"blocked_until_separate_audited_payment_reconciliation"}]
    plan["referral_bases"] = [{"source":referral,"reward_id":9,"user_id":1,"original_quota":680,"rebased_quota":100,"rebased_revoked_quota":10,"rebased_penalty_quota":10}]
    plan["entity_updates"] = []
    for table,s,targets in [("redemptions",red,{"quota":100}),("open_source_bounty_projects",bounty,{"escrow_quota":1000,"reward_quota":100,"net_reward_quota":90}),("open_source_bounty_challenges",challenge,{"reward_quota":90})]:
        plan["entity_updates"].append({"table":table,"id":s["id"],"source":s,"updates":{key:{"before_credit":s[key],"after_credit":val} for key,val in targets.items()}})
        if table == "open_source_bounty_challenges":
            plan["entity_updates"][-1]["rights_status"] = "active_future_reward"
    plan["subscriptions"] = [{"id":70,"source":sub,"finite":True,"amount_total":6220,"reset_amount":1000,"renewal_amount":1000,"quota_version":1,"updated_at":1000000}]
    plan["subscription_order_sources"] = [order,pending_order,fallback_order]
    plan["subscription_order_updates"] = [{"id":81,"source":pending_order,"grant_source_kind":"frozen_order_plan_snapshot","original_grant":6800,"effective_grant":1000,"plan_snapshot":'{"id":4,"total_amount":1000,"waffo_pancake_product_type":"one_time"}'},{"id":82,"source":fallback_order,"grant_source_kind":"runtime_current_catalog_fallback","catalog_plan_id":4,"catalog_source":catalog,"original_grant":6800,"effective_grant":1000,"plan_snapshot":None}]
    plan["subscription_plan_updates"] = [{"id":4,"source":catalog,"total_amount":1000,"updated_at":1000000}]
    plan["subscription_payment_events"] = [payment]
    plan["subscription_payment_refunds"] = [refund]
    plan["subscription_refund_bases"] = [{"subscription_order_id":80,"user_subscription_id":70,"user_id":1,"period_start":0,"period_end":0,"subscription_end_time":200,"original_quota_version":1,"original_credit_quota":6800,"original_refunded_quota":0,"original_refunded_amount_micros":0,"original_paid_amount_micros":10000,"refundable_quota":100,"reset_quota":1000,"reset_reduced_quota":0,"rebased_debited_quota":0}]
    plan["other_credit_bases"] = other
    return seal(plan)


def bounty_case_plan():
    plan = fixture_plan()
    at = plan["snapshot_at"]
    base = next(e["source"] for e in plan["entity_updates"] if e["table"] == "open_source_bounty_challenges")
    for rid,reward,status,rejected_at,active in [(61,680000,"rejected",at-604800,False),(62,68,"rejected",at-604800+1,True),(63,680000,"rejected",at-1,False),(64,680,"rejected",1,True),(65,68,"rejected",1,True),(66,68,"accepted",0,True)]:
        row = base | {"id":rid,"reward_quota":reward,"status":status,"rejected_at":rejected_at}
        plan["entity_updates"].append({"table":"open_source_bounty_challenges","id":rid,"source":row,"rights_status":"active_future_reward" if active else "historical_rejection_guard_only","updates":{"reward_quota":{"before_credit":reward,"after_credit":100 if reward == 680 else 10}} if active else {}})
    for rid,challenge,status,opener,against,reward in [(201,63,"resolved_denied",2,1,680000),(202,64,"open",2,1,680),(203,65,"open",1,2,68),(204,66,"open",2,1,68),(205,99,"resolved_paid",2,1,680000)]:
        row = source(v.ENTITY_COLUMNS["open_source_bounty_disputes"],texts=("status","challenge_status_snapshot"),id=rid,challenge_id=challenge,project_id=50,opened_by_user_id=opener,against_user_id=against,project_escrow_quota_snapshot=6800,reward_quota_snapshot=reward,tip_quota_snapshot=125,created_at=at-10,status=status,challenge_status_snapshot="rejected")
        if status != "open":
            row |= {"resolved_at":at-1,"resolved_by_user_id":1}
        plan["entity_updates"].append({"table":"open_source_bounty_disputes","id":rid,"source":row,"updates":{}})
        if status == "open" and opener == 2:
            plan["other_credit_bases"].append({"kind":"bounty_dispute_reward","source_id":str(rid),"user_id":2,"original_quota":reward,"rebased_quota":100 if reward == 680 else 10,"source":row})
    return seal(plan)


def orphan_case_plan():
    plan = bounty_case_plan()
    plan["snapshot_all_users"] = True
    plan["orphan_pending_user_ids"] = [3]
    original = plan["pending_bases"][0]
    row = original["source"] | {"id":24,"user_id":3,"status":"failed","payment_provider":"waffo_pancake","payment_method":"waffo_pancake","failure_reason_code":"checkout_timeout","settled_amount_micros":0}
    plan["pending_bases"].append({"source":row,"top_up_id":24,"user_id":3,"original_credited_quota":680,"effective_credited_quota":100,"owner_missing_at_snapshot":True})
    return seal(plan)


class OfflineVerifierTests(unittest.TestCase):
    def test_read_only_independent_output(self):
        plan = fixture_plan()
        for stage in ("before","after"):
            sql = v.postgres_verification_sql(plan, stage)
            self.assertIn("REPEATABLE READ READ ONLY", sql)
            self.assertNotRegex(sql, r"\b(?:UPDATE|INSERT|DELETE|ALTER|CREATE|TRUNCATE|LOCK)\b")
            self.assertIn('"used_quota"', sql)
            self.assertIn('"platform_fee_quota"', sql)
            self.assertIn('"tip_quota"', sql)
            self.assertIn('"plan_snapshot"', sql)
            self.assertIn("MAX(id)", sql)

    def test_hashes_and_duplicate_json_keys(self):
        plan = fixture_plan()
        plan["entries"][0]["after_credit"] = 999
        with self.assertRaisesRegex(ValueError,"SHA-256"):
            v.postgres_verification_sql(plan,"after")
        with tempfile.TemporaryDirectory() as d:
            path = Path(d)/"bad.json"
            path.write_text('{"version":1,"version":1}')
            with self.assertRaisesRegex(ValueError,"duplicate JSON"):
                v.load_plan(path)

    def test_only_target_identity_changes_prove_clone(self):
        plan = fixture_plan()
        clone = copy.deepcopy(plan)
        clone["target"] = {"database":"fixture_clone","schema":"fixture_copy","system_identifier":"999"}
        clone["source_sha256"] = "3"*64
        seal(clone)
        self.assertTrue(v.clone_proof(plan,clone)["target_only_clone"])
        clone["user_sources"][0]["used_quota"] += 1
        seal(clone)
        with self.assertRaisesRegex(ValueError,"business data"):
            v.clone_proof(plan,clone)

    def test_arithmetic_is_checked_without_renderer(self):
        plan = fixture_plan()
        plan["entries"][0]["after_credit"] = 999
        plan["entries"][0]["delta_credit"] = 999 - 6800
        seal(plan)
        with self.assertRaisesRegex(ValueError,"arithmetic"):
            v.postgres_verification_sql(plan,"after")

    def test_full_user_token_and_history_projection_required(self):
        for key,field in (("user_sources","used_quota"),("token_sources","used_quota")):
            plan = fixture_plan()
            del plan[key][0][field]
            seal(plan)
            with self.assertRaisesRegex(ValueError,"historical"):
                v.postgres_verification_sql(plan,"before")

    def test_unknown_source_columns_and_historical_writes_rejected(self):
        plan = fixture_plan()
        plan["entity_updates"][1]["updates"]["platform_fee_quota"] = {"before_credit":68,"after_credit":10}
        seal(plan)
        with self.assertRaisesRegex(ValueError,"unapproved entitlement"):
            v.postgres_verification_sql(plan,"after")
        plan = fixture_plan()
        plan["refund_bases"][0]["source"]["provider_payload"] = "synthetic-sensitive"
        seal(plan)
        with self.assertRaisesRegex(ValueError,"approved projection"):
            v.postgres_verification_sql(plan,"before")

    def test_pending_snapshot_and_fx_preservation(self):
        plan = fixture_plan()
        plan["subscription_order_updates"][0]["plan_snapshot"] = '{"id":4,"total_amount":1000,"waffo_pancake_product_type":"changed"}'
        seal(plan)
        with self.assertRaisesRegex(ValueError,"non-credit"):
            v.postgres_verification_sql(plan,"after")

    def test_child_basis_binds_authoritative_source(self):
        for key,field in (("refund_bases","original_paid_amount_micros"),("pending_bases","original_credited_quota"),("referral_bases","original_quota"),("other_credit_bases","original_quota")):
            plan = fixture_plan()
            plan[key][0][field] += 1
            seal(plan)
            with self.assertRaises(ValueError):
                v.postgres_verification_sql(plan,"after")

    def test_resealed_malformed_plans_fail_closed(self):
        def raw_principal(plan):
            basis = plan["refund_bases"][0]
            basis["source"]["effective_credited_quota"] = 13600
            basis["original_credited_quota"] = 13600
            basis["refundable_quota"] = 1900
        def raw_paid(plan):
            basis = plan["refund_bases"][0]
            basis["source"]["paid_amount_micros"] = 2000
            basis["original_paid_amount_micros"] = 2000
        changes = [
            lambda p:p["subscriptions"][0].update(renewal_amount=999999),
            lambda p:p["subscription_refund_bases"][0].update(refundable_quota=999999),
            lambda p:p.update(subscription_refund_bases=[]),
            lambda p:p.update(subscription_order_updates=[]),
            lambda p:p["subscription_order_updates"][1].update(catalog_plan_id=999),
            lambda p:p["entity_updates"][1].update(id="50,(SELECT 0 FROM pg_sleep(1))"),
            lambda p:p.update(refund_bases=p["refund_bases"]*2,noncash_topups=[]),
            lambda p:p["entries"][0].update(after_credit=1000.0),
            lambda p:p["subscription_payment_events"][0].update(settlement_amount_micros=10001),
            lambda p:p["subscriptions"][0]["source"].update(reset_amount=123),
            lambda p:p["pending_bases"][0]["source"].update(pending_credit_rebase_key="earlier"),
            lambda p:p["refund_bases"][0]["source"].update(status="failed"),
            lambda p:p["pending_bases"][0]["source"].update(status="success"),
            lambda p:p["referral_bases"][0]["source"].update(status="pending"),
            raw_principal,raw_paid,
        ]
        for change in changes:
            plan = fixture_plan()
            change(plan)
            seal(plan)
            for stage in ("before","after"):
                with self.subTest(change=changes.index(change),stage=stage):
                    with self.assertRaises((ValueError,TypeError)):
                        v.postgres_verification_sql(plan,stage)

    def test_cli_preserves_both_input_plans(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            plan_path,clone_path = root/"plan.json",root/"clone.json"
            plan = fixture_plan()
            clone = copy.deepcopy(plan)
            clone["target"]["schema"] = "fixture_clone"
            seal(clone)
            plan_path.write_text(json.dumps(plan))
            before = json.dumps(clone)
            clone_path.write_text(before)
            run = subprocess.run([sys.executable,str(Path(v.__file__)),"--plan",str(plan_path),"--compare-plan",str(clone_path),"--summary-output",str(clone_path)],capture_output=True,text=True)
            self.assertEqual(run.returncode,2)
            self.assertEqual(clone_path.read_text(),before)

    def test_cli_errors_do_not_echo_private_values(self):
        with tempfile.TemporaryDirectory() as d:
            plan = fixture_plan()
            private_value = "synthetic-private-customer-data"
            plan["fx_source"]["value"] = private_value
            seal(plan)
            path = Path(d)/"plan.json"
            path.write_text(json.dumps(plan))
            run = subprocess.run([sys.executable,str(Path(v.__file__)),"--plan",str(path)],capture_output=True,text=True)
            self.assertEqual(run.returncode,2)
            self.assertNotIn(private_value,run.stderr)

    def test_expired_rejections_and_participant_disputes(self):
        plan = bounty_case_plan()
        for stage in ("before","after"):
            v.postgres_verification_sql(plan,stage)
        expired = next(e for e in plan["entity_updates"] if e["table"] == "open_source_bounty_challenges" and e["id"] == 61)
        expired["updates"] = {"reward_quota":{"before_credit":680000,"after_credit":100000}}
        seal(plan)
        with self.assertRaises(ValueError):
            v.postgres_verification_sql(plan,"after")
        plan = bounty_case_plan()
        plan["other_credit_bases"] = [e for e in plan["other_credit_bases"] if e["kind"] != "bounty_dispute_reward" or e["source_id"] != "204"]
        seal(plan)
        with self.assertRaisesRegex(ValueError,"open dispute"):
            v.postgres_verification_sql(plan,"before")

    def test_provisional_summary_cannot_emit_verification_sql(self):
        plan = fixture_plan()
        plan["snapshot_state"] = "provisional_live_not_frozen"
        plan["production_apply_supported"] = False
        seal(plan)
        self.assertFalse(v.summary(plan)["production_apply_supported"])
        for stage in ("before","after"):
            with self.assertRaisesRegex(ValueError,"writers stopped"):
                v.postgres_verification_sql(plan,stage)

    def test_orphan_pending_owner_requires_complete_explicit_frozen_scope(self):
        plan = orphan_case_plan()
        for stage in ("before","after"):
            sql = v.postgres_verification_sql(plan,stage)
            self.assertIn("orphan pending owner must remain absent",sql)
            self.assertIn("all recoverable pending topups",sql)
        bad = copy.deepcopy(plan)
        bad["snapshot_all_users"] = False
        seal(bad)
        with self.assertRaises(ValueError):
            v.postgres_verification_sql(bad,"before")
        bad = copy.deepcopy(plan)
        bad["pending_bases"][-1]["source"]["settled_amount_micros"] = 1000
        seal(bad)
        with self.assertRaises(ValueError):
            v.postgres_verification_sql(bad,"before")

    def test_typed_restorations_only_include_declared_updates(self):
        rows = v.planned_restorations(orphan_case_plan())
        indexed = {(r["table"],json.dumps(r["key"],sort_keys=True)):r["fields"] for r in rows}
        wallet = indexed[("users",'{"id": 1}')]
        self.assertEqual(wallet["quota"],{"before":6800,"after":1000})
        self.assertNotIn("used_quota",wallet)
        self.assertNotIn(("open_source_bounty_challenges",'{"id": 61}'),indexed)
        self.assertNotIn(("open_source_bounty_disputes",'{"id": 202}'),indexed)
        pending = indexed[("top_ups",'{"id": 24}')]
        self.assertEqual(set(pending),{"pending_credit_rebase_key","pending_credit_rebase_original_quota","pending_credit_rebase_effective_quota"})
        self.assertNotIn("credited_quota",pending)
        plan = fixture_plan()
        plan["option_guards"][0]["value"] = "6.80"
        seal(plan)
        with self.assertRaisesRegex(ValueError,"FX"):
            v.postgres_verification_sql(plan,"after")

    def test_delimiter_and_literals_cannot_escape(self):
        plan = fixture_plan()
        plan["option_guards"][1]["value"] = "quote ' slash \\ ; $credit_verify$"
        seal(plan)
        sql = v.postgres_verification_sql(plan,"before")
        self.assertIn("quote '' slash \\",sql)

    def test_summary_and_cli_do_not_expose_wallet_values(self):
        plan = fixture_plan()
        result = v.summary(plan,"after")
        self.assertEqual(result["selected_token_count"],2)
        self.assertNotIn("quota", json.dumps(result))
        self.assertNotIn("synthetic-order", json.dumps(result))
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            (root/"plan.json").write_text(json.dumps(plan))
            output = root/"verify.sql"
            run = subprocess.run([sys.executable,str(Path(v.__file__)),"--plan",str(root/"plan.json"),"--emit-postgres-verification-sql","after","--output",str(output)],capture_output=True,text=True)
            self.assertEqual(run.returncode,0,run.stderr)
            self.assertEqual(output.stat().st_mode & 0o777,0o600)
            self.assertNotIn("synthetic-order",run.stdout)
            self.assertTrue(json.loads(run.stdout)["sql_read_only"])


if __name__ == "__main__":
    unittest.main()
