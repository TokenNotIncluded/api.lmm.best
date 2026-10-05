#!/usr/bin/env python3
"""Real transaction verification in a new local Unix-socket PostgreSQL fixture only."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

spec = importlib.util.spec_from_file_location("r", Path(__file__).with_name("preview-credit-balance-rebase.py"))
r = importlib.util.module_from_spec(spec)
spec.loader.exec_module(r)
root = Path(tempfile.mkdtemp(prefix="credit-rebase-pg-", dir=Path.home() / ".cache"))
data, sock = root / "data", root / "socket"
sock.mkdir()


def run(cmd, **kw):
    return subprocess.run(cmd, text=True, capture_output=True,
                          env={k: v for k, v in os.environ.items() if not k.startswith("PG")}, **kw)


def ok(cmd, **kw):
    result = run(cmd, **kw)
    if result.returncode:
        raise RuntimeError(result.stderr)
    return result.stdout.strip()


ok(["initdb", "-D", str(data), "-A", "trust", "--no-instructions"])
ok(["pg_ctl", "-D", str(data), "-l", str(root / "pg.log"), "-o",
    f"-c listen_addresses= -c unix_socket_directories={sock}", "-w", "start"])
base = ["psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-h", str(sock), "-d", "postgres"]
try:
    ok(base, input="""
CREATE SCHEMA fixture_money;
CREATE TABLE fixture_money.options (key text PRIMARY KEY,value text);
INSERT INTO fixture_money.options VALUES ('USDExchangeRate','6.8'),('CreditsPerUSD','3359744'),('PublicCreditsPerUSD','100000'),('LegacyPricingQuotaPerUnit','500000'),('QuotaPerUnit','500000');
CREATE TABLE fixture_money.top_ups (id bigint PRIMARY KEY,user_id bigint,status text,credited_quota bigint,amount bigint,platform_amount_micros bigint,settled_amount_micros bigint,expected_amount_micros bigint,refunded_quota bigint,refunded_amount_micros bigint,money double precision,payment_provider text,payment_method text,settlement_currency text);
INSERT INTO fixture_money.top_ups VALUES (20,1,'success',6800,0,0,1000,1000,680,100,0.001,'stripe','stripe','USD');
CREATE TABLE fixture_money.users (id bigint PRIMARY KEY, quota bigint, aff_quota bigint, used_quota bigint);
CREATE TABLE fixture_money.tokens (id bigint PRIMARY KEY, user_id bigint, remain_quota bigint, unlimited_quota bool);
CREATE TABLE fixture_money.referral_rewards (id bigint PRIMARY KEY,inviter_id bigint,invitee_id bigint,top_up_id bigint,quota bigint,revoked_quota bigint,penalty_quota bigint,penalty_percent bigint,max_penalty_quota bigint,revision bigint,created_at bigint,updated_at bigint,status text,reason text);
INSERT INTO fixture_money.referral_rewards VALUES (9,1,2,20,680,680,68,10,0,1,0,0,'revoked','abuse');
INSERT INTO fixture_money.top_ups VALUES (21,1,'pending',680,0,0,1000,1000,0,0,0.001,'stripe','stripe','USD');
INSERT INTO fixture_money.users VALUES (1,500000000,680,123),(2,-86911,0,45);
INSERT INTO fixture_money.tokens VALUES (10,1,680,false);
ALTER TABLE fixture_money.users ADD COLUMN request_count bigint DEFAULT 0,ADD COLUMN aff_history bigint DEFAULT 0,ADD COLUMN aff_count bigint DEFAULT 0,ADD COLUMN status bigint DEFAULT 1,ADD COLUMN deleted_at timestamptz;
ALTER TABLE fixture_money.tokens ADD COLUMN used_quota bigint DEFAULT 0,ADD COLUMN status bigint DEFAULT 1,ADD COLUMN created_time bigint DEFAULT 0,ADD COLUMN accessed_time bigint DEFAULT 0,ADD COLUMN expired_time bigint DEFAULT -1,ADD COLUMN deleted_at timestamptz;
""")
    snapshot = {"version": 1, "applied_migration_ids": [],
                "target": {"database": "postgres", "schema": "fixture_money",
                           "system_identifier": ok(base, input="SELECT system_identifier FROM pg_control_system();")},
                "topups": [{"id":20,"user_id":1,"status":"success","credited_quota":6800,"amount":0,"platform_amount_micros":0,"settled_amount_micros":1000,"expected_amount_micros":1000,"refunded_quota":680,"refunded_amount_micros":100,"money":"0.001","payment_provider":"stripe","payment_method":"stripe","settlement_currency":"USD","effective_credited_quota":6800,"paid_amount_micros":1000,"is_legacy_linuxdo_credit_topup":False}],
                "users": [{"id": 1, "quota": 500000000}, {"id": 2, "quota": -86911}],
                "tokens": [{"id": 10, "user_id": 1, "remain_quota": 680, "unlimited_quota": False}],
                "options": {"USDExchangeRate":"6.8", "CreditsPerUSD": "3359744", "PublicCreditsPerUSD": "100000",
                            "LegacyPricingQuotaPerUnit": "500000", "QuotaPerUnit": "500000"},
                "price_review": {"status": "verified", "evidence": "synthetic $credit_rebase$ quote ' and slash \\ fixture",
                                 "option_corrections": []}}
    snapshot["referrals"] = [{"id":9,"inviter_id":1,"invitee_id":2,"top_up_id":20,"quota":680,"revoked_quota":680,"penalty_quota":68,"penalty_percent":10,"max_penalty_quota":0,"revision":1,"created_at":0,"updated_at":0,"status":"revoked","reason":"abuse"}]
    snapshot["pending_topups"] = [snapshot["topups"][0] | {"id":21,"status":"pending","failure_reason_code":"","credited_quota":680,"refunded_quota":0,"refunded_amount_micros":0,"effective_credited_quota":680,"pending_credit_rebase_key":"","pending_credit_rebase_original_quota":0,"pending_credit_rebase_effective_quota":0}]
    noncash = snapshot["topups"][0] | {"id":22,"credited_quota":0,"amount":2,"settled_amount_micros":0,"expected_amount_micros":0,"refunded_quota":0,"refunded_amount_micros":0,"money":"0.28","payment_provider":"epay","payment_method":"epay","settlement_currency":"","effective_credited_quota":0,"paid_amount_micros":280000,"is_legacy_linuxdo_credit_topup":True}
    snapshot["topups"].append(noncash)
    ok(base,input="ALTER TABLE fixture_money.top_ups ADD COLUMN failure_reason_code text NOT NULL DEFAULT '';")
    ok(base,input="INSERT INTO fixture_money.top_ups VALUES (22,1,'success',0,2,0,0,0,0,0,0.28,'epay','epay','','');")
    blocked = noncash | {"id":23,"status":"pending","payment_provider":"fastpay","payment_method":"alipay","failure_reason_code":"","is_legacy_linuxdo_credit_topup":False,"pending_credit_rebase_key":"","pending_credit_rebase_original_quota":0,"pending_credit_rebase_effective_quota":0}
    late = snapshot["pending_topups"][0] | {"id":24,"status":"failed","payment_provider":"waffo_pancake","failure_reason_code":"checkout_timeout"}
    snapshot["pending_topups"] += [blocked,late]
    ok(base,input="INSERT INTO fixture_money.top_ups VALUES (23,1,'pending',0,2,0,0,0,0,0,0.28,'fastpay','alipay','',''),(24,1,'failed',680,0,0,1000,1000,0,0,0.001,'waffo_pancake','stripe','USD','checkout_timeout');")
    snapshot["users"][1]["aff_quota"] = 0
    snapshot["users"][0]["aff_quota"] = 680
    snapshot["user_sources"] = [dict(id=1,quota=500000000,aff_quota=680,used_quota=123,request_count=0,aff_history=0,aff_count=0,status=1,deleted_at=None),dict(id=2,quota=-86911,aff_quota=0,used_quota=45,request_count=0,aff_history=0,aff_count=0,status=1,deleted_at=None)]
    snapshot["token_sources"] = [dict(id=10,user_id=1,remain_quota=680,unlimited_quota=False,used_quota=0,status=1,created_time=0,accessed_time=0,expired_time=-1,deleted_at=None)]
    from credit_rebase_entitlements import SPECS
    def entity_source(table, **overrides):
        row = {key: 0 for key in SPECS[table]["int"]}
        row.update({key: "" for key in SPECS[table]["text"]})
        row.update({key: None for key in SPECS[table]["null"]})
        return row | overrides
    red = entity_source("redemptions", id=30, user_id=1, quota=680, status=1, reward_type=None)
    bounty = entity_source("open_source_bounty_projects", id=50, owner_user_id=1, escrow_quota=6800, reward_quota=680, net_reward_quota=612, platform_fee_quota=68, status="published")
    challenge = entity_source("open_source_bounty_challenges", id=60, project_id=50, participant_user_id=2, reward_quota=612, tip_quota=125, status="accepted")
    for table, row in [("redemptions", red), ("open_source_bounty_projects", bounty), ("open_source_bounty_challenges", challenge)]:
        spec = SPECS[table]
        columns = ["id bigint PRIMARY KEY"] + [key + " bigint" for key in spec["int"]] + [key + " text" for key in spec["text"]] + [key + " timestamptz" for key in spec["null"]]
        values = ["NULL" if value is None else r.sql_literal(value) if isinstance(value, str) else str(value) for value in row.values()]
        ok(base, input=f"CREATE TABLE fixture_money.{table} (" + ",".join(columns) + "); INSERT INTO fixture_money." + table + " (" + ",".join(row.keys()) + ") VALUES (" + ",".join(values) + ");")
    snapshot["snapshot_at"] = 1000000
    historical_rejection=entity_source("open_source_bounty_challenges",id=61,project_id=50,participant_user_id=2,reward_quota=68000,status="rejected",rejected_at=395200)
    rejection_values=[r.sql_literal(value) if isinstance(value,str) else str(value) for value in historical_rejection.values()]
    ok(base,input="INSERT INTO fixture_money.open_source_bounty_challenges ("+",".join(historical_rejection)+") VALUES ("+",".join(rejection_values)+");")
    snapshot["entities"] = {"redemptions":[red], "bounty_projects":[bounty], "bounty_challenges":[challenge,historical_rejection],"bounty_disputes":[]}
    dispute_spec=SPECS["open_source_bounty_disputes"]
    ok(base,input="CREATE TABLE fixture_money.open_source_bounty_disputes (id bigint PRIMARY KEY,"+",".join(key+" bigint" for key in dispute_spec["int"])+","+",".join(key+" text" for key in dispute_spec["text"])+");")
    import credit_rebase_subscriptions as subscriptions
    def subscription_source(ints,texts,**overrides):
        return {key:0 for key in ints} | {key:"" for key in texts} | overrides
    sold = subscription_source(subscriptions.SUB_INT,subscriptions.SUB_TEXT,id=70,user_id=1,plan_id=4,amount_total=6800,amount_used=6120,status="active",source="order",end_time=200,reset_amount=None,renewal_amount=None)
    order = subscription_source(subscriptions.ORDER_INT,subscriptions.ORDER_TEXT,id=80,user_id=1,plan_id=4,user_subscription_id=70,status="success",money="0.01",expected_amount_micros=10000,plan_snapshot='{"id":4,"total_amount":6800,"waffo_pancake_product_type":"one_time"}')
    pending_order = order | {"id":81,"user_subscription_id":0,"status":"pending"}
    fallback_order = pending_order | {"id":82,"plan_snapshot":""}
    catalog = subscription_source(subscriptions.PLAN_INT,subscriptions.PLAN_TEXT,id=4,total_amount=6800,price_amount="0.01",enabled=True)
    snapshot.update(subscriptions=[sold],subscription_orders=[order,pending_order,fallback_order],subscription_plans=[catalog],subscription_payment_events=[],subscription_payment_refunds=[])
    for table,ints,texts,nullable,booleans,sources in [
        ("user_subscriptions",subscriptions.SUB_INT,subscriptions.SUB_TEXT,(),(),[sold]),
        ("subscription_orders",subscriptions.ORDER_INT,subscriptions.ORDER_TEXT,(),(),[order,pending_order,fallback_order]),
        ("subscription_plans",subscriptions.PLAN_INT,subscriptions.PLAN_TEXT,(),("enabled",),[catalog]),
        ("subscription_payment_events",subscriptions.PAYMENT_INT,subscriptions.PAYMENT_TEXT,("period_start","period_end"),(),[]),
        ("subscription_payment_refunds",subscriptions.REFUND_INT,subscriptions.REFUND_TEXT,(),(),[])]:
        columns = ["id bigint PRIMARY KEY"] + [key + " bigint" for key in ints+nullable] + [key + (" double precision" if key=="money" else " numeric" if key=="price_amount" else " text") for key in texts] + [key + " boolean" for key in booleans]
        ok(base,input=f"CREATE TABLE fixture_money.{table} (" + ",".join(columns) + ");")
        for source in sources:
            original = {key:value for key,value in source.items() if key not in ("reset_amount","renewal_amount")}
            values = ["NULL" if value is None else "true" if value is True else "false" if value is False else r.sql_literal(value) if isinstance(value,str) else str(value) for value in original.values()]
            ok(base,input=f"INSERT INTO fixture_money.{table} (" + ",".join(original) + ") VALUES (" + ",".join(values) + ");")
    import credit_rebase_other_rights as other
    snapshot["obligations"] = {key:0 for key in other.OBLIGATIONS}
    snapshot["other_rights"] = {spec[0]:[] for spec in other.SPECS.values()} | {"hero_sms_email_activations":[]}
    for _,(_,table,ints,texts,bools) in other.SPECS.items():
        fields = [key+" bigint" for key in ints if key != "last_refund_ledger_id"]+[key+" text" for key in texts]+[key+" boolean" for key in bools]
        ok(base,input=f"CREATE TABLE fixture_money.{table} ("+",".join(fields)+");")
    activation_columns = [key+" bigint" for key in other.ACTIVATION_INTS]+[key+" text" for key in other.ACTIVATION_TEXT]
    ok(base,input="CREATE TABLE fixture_money.hero_sms_email_activations ("+",".join(activation_columns)+"); CREATE TABLE fixture_money.hero_sms_email_quota_ledgers (id bigint,order_id text,entry_type text,amount_quota bigint);")
    ok(base,input="CREATE TABLE fixture_money.wallet_transfers(status text); CREATE TABLE fixture_money.tool_market_calls(settlement_status text); CREATE TABLE fixture_money.tasks(status text,refund_status text,quota bigint,refund_quota bigint,submit_time bigint); CREATE TABLE fixture_money.midjourneys(progress text); CREATE TABLE fixture_money.subscription_pre_consume_records(status text); CREATE TABLE fixture_money.hero_sms_sms_orders(status text);")
    exported = json.loads(ok(base+["-v","target_schema=fixture_money"],input=Path(__file__).with_name("export-credit-rebase-frozen-private.sql").read_text()))
    assert len(exported["users"]) == 2 and len(exported["user_sources"]) == 2
    assert len(exported["topups"]) == 2 and len(exported["pending_topups"]) == 3
    assert len(exported["subscription_orders"]) == 3 and exported["applied_migration_ids"] == []
    assert exported["entities"]["redemptions"][0]["reward_type"] is None
    kw = dict(divisor_text="6.8", migration_id="fixture-v1", user_ids=[1, 2],
              rounding="half-away-from-zero", restore_fixed_anchors=True, include_token_limits=True, include_affiliate=True, include_pending_topups=True, include_redemptions=True, include_bounties=True,include_subscriptions=True,include_other_rights=True)

    def render(source=snapshot, **overrides):
        return r.postgres_sql(r.make_plan(source, **(kw | overrides)))

    def wallet():
        return ok(base, input="SELECT id,quota,used_quota FROM fixture_money.users ORDER BY id;")

    def reset():
        ok(base, input="""
TRUNCATE fixture_money.subscription_order_credit_rebases, fixture_money.wallet_referral_credit_rebases, fixture_money.wallet_topup_credit_rebases, fixture_money.wallet_credit_rebases;
UPDATE fixture_money.options SET value=CASE WHEN key='USDExchangeRate' THEN '6.8' WHEN key='CreditsPerUSD' THEN '3359744' WHEN key='PublicCreditsPerUSD' THEN '100000' ELSE '500000' END;
UPDATE fixture_money.users SET aff_quota=CASE WHEN id=1 THEN 680 ELSE 0 END;
UPDATE fixture_money.users SET quota=CASE WHEN id=1 THEN 500000000 ELSE -86911 END;
UPDATE fixture_money.users SET used_quota=CASE WHEN id=1 THEN 123 ELSE 45 END;
UPDATE fixture_money.tokens SET user_id=1, remain_quota=680;
UPDATE fixture_money.tokens SET unlimited_quota=false;
UPDATE fixture_money.top_ups SET refunded_quota=680 WHERE id=20;
UPDATE fixture_money.top_ups SET payment_provider='epay',expected_amount_micros=0 WHERE id=22;
UPDATE fixture_money.top_ups SET pending_credit_rebase_key='',pending_credit_rebase_original_quota=0,pending_credit_rebase_effective_quota=0 WHERE id=21;
UPDATE fixture_money.top_ups SET pending_credit_rebase_key='',pending_credit_rebase_original_quota=0,pending_credit_rebase_effective_quota=0 WHERE id IN (23,24);
UPDATE fixture_money.top_ups SET failure_reason_code='checkout_timeout',status='failed' WHERE id=24;
UPDATE fixture_money.referral_rewards SET revision=1;
UPDATE fixture_money.redemptions SET quota=680,user_id=1;
UPDATE fixture_money.open_source_bounty_projects SET escrow_quota=6800,reward_quota=680,net_reward_quota=612,updated_at=0;
UPDATE fixture_money.open_source_bounty_challenges SET reward_quota=612,participant_user_id=2 WHERE id=60;
UPDATE fixture_money.open_source_bounty_challenges SET reward_quota=68000,participant_user_id=2,rejected_at=395200 WHERE id=61;
UPDATE fixture_money.user_subscriptions SET amount_total=6800,amount_used=6120,reset_amount=NULL,renewal_amount=NULL,quota_version=0,updated_at=0,user_id=1;
UPDATE fixture_money.subscription_plans SET total_amount=6800,updated_at=0;
TRUNCATE fixture_money.tasks,fixture_money.midjourneys;
TRUNCATE fixture_money.open_source_bounty_disputes;

""")
        ok(base,input="UPDATE fixture_money.subscription_orders SET plan_snapshot=" + r.sql_literal(order["plan_snapshot"]) + " WHERE id IN (80,81); UPDATE fixture_money.subscription_orders SET plan_snapshot='' WHERE id=82;")

    sql = render()
    original = wallet()
    for field, wrong in [("database", "wrong_database"), ("system_identifier", "0"), ("schema", "wrong_schema")]:
        bad = copy.deepcopy(snapshot)
        bad["target"][field] = wrong
        assert run(base, input=render(bad)).returncode != 0
        assert wallet() == original
    ok(base, input=sql)
    assert ok(base, input="SELECT refundable_quota FROM fixture_money.wallet_topup_credit_rebases WHERE top_up_id=20;") == "900"
    assert ok(base, input="SELECT quota FROM fixture_money.redemptions WHERE id=30;") == "100"
    assert ok(base, input="SELECT escrow_quota,platform_fee_quota FROM fixture_money.open_source_bounty_projects WHERE id=50;") == "1000|68"
    assert ok(base, input="SELECT reward_quota,tip_quota FROM fixture_money.open_source_bounty_challenges WHERE id=60;") == "90|125"
    assert ok(base,input="SELECT reward_quota FROM fixture_money.open_source_bounty_challenges WHERE id=61;") == "68000"
    assert ok(base,input="SELECT rebased_quota,rebased_revoked_quota,rebased_penalty_quota,rounding FROM fixture_money.wallet_referral_credit_rebases;") == "100|100|10|half-away-from-zero"
    assert ok(base,input="SELECT pending_credit_rebase_original_quota,pending_credit_rebase_effective_quota,credited_quota FROM fixture_money.top_ups WHERE id=21;") == "680|100|680"
    assert ok(base,input="SELECT pending_credit_rebase_key,pending_credit_rebase_original_quota,pending_credit_rebase_effective_quota FROM fixture_money.top_ups WHERE id=23;") == "fixture-v1|0|0"
    assert ok(base,input="SELECT status,pending_credit_rebase_effective_quota FROM fixture_money.top_ups WHERE id=24;") == "failed|100"
    assert ok(base,input="SELECT amount_total,amount_used,reset_amount,renewal_amount,quota_version FROM fixture_money.user_subscriptions;") == "6220|6120|1000|1000|1"
    assert ok(base,input="SELECT original_credit_quota,refundable_quota,reset_quota,original_quota_version FROM fixture_money.subscription_order_credit_rebases;") == "6800|100|1000|1"
    assert ok(base,input="SELECT total_amount FROM fixture_money.subscription_plans;") == "1000"
    assert ok(base,input="SELECT plan_snapshot::jsonb->>'total_amount' FROM fixture_money.subscription_orders WHERE id=81;") == "1000"
    assert ok(base,input="SELECT plan_snapshot::jsonb->>'total_amount' FROM fixture_money.subscription_orders WHERE id=80;") == "6800"
    assert ok(base,input="SELECT length(plan_snapshot) FROM fixture_money.subscription_orders WHERE id=82;") == "0"
    assert ok(base,input="SELECT is_nullable,column_default IS NULL FROM information_schema.columns WHERE table_schema='fixture_money' AND table_name='hero_sms_email_quota_ledgers' AND column_name='original_amount_quota';") == "YES|t"
    first = wallet()
    assert first == "1|73529412|123\n2|-12781|45", first
    assert ok(base, input="SELECT count(*) FROM fixture_money.options WHERE value='500000';") == "4"
    assert ok(base, input="SELECT remain_quota FROM fixture_money.tokens WHERE id=10;") == "100"
    ok(base, input=sql)
    assert wallet() == first
    assert run(base, input=render(include_token_limits=False)).returncode != 0
    assert run(base, input=render(migration_id="fixture-v2")).returncode != 0
    assert ok(base, input="SELECT count(*) FROM fixture_money.wallet_credit_rebases;") == "1"

    for conflict in ["UPDATE fixture_money.users SET quota=-86910 WHERE id=2;",
                     "UPDATE fixture_money.options SET value='unexpected-anchor' WHERE key='CreditsPerUSD';",
                     "UPDATE fixture_money.tokens SET user_id=2 WHERE id=10;",
                     "UPDATE fixture_money.tokens SET unlimited_quota=true WHERE id=10;",
                     "UPDATE fixture_money.users SET used_quota=124 WHERE id=1;",
                     "UPDATE fixture_money.top_ups SET refunded_quota=681 WHERE id=20;",
                     "UPDATE fixture_money.options SET value='6.710363' WHERE key='USDExchangeRate';",
                     "UPDATE fixture_money.redemptions SET user_id=2 WHERE id=30;",
                     "UPDATE fixture_money.open_source_bounty_projects SET updated_at=1 WHERE id=50;",
                     "UPDATE fixture_money.open_source_bounty_challenges SET participant_user_id=1 WHERE id=60;",
                     "UPDATE fixture_money.open_source_bounty_challenges SET rejected_at=395201 WHERE id=61;",
                     "INSERT INTO fixture_money.open_source_bounty_disputes (id,challenge_id,project_id,status) VALUES (90,61,50,'open');",
                     "UPDATE fixture_money.referral_rewards SET revision=2 WHERE id=9;",
                     "UPDATE fixture_money.top_ups SET payment_provider='stripe' WHERE id=22;",
                     "UPDATE fixture_money.top_ups SET expected_amount_micros=1 WHERE id=22;",
                     "UPDATE fixture_money.top_ups SET failure_reason_code='unexpected_failure' WHERE id=24;",
                     "UPDATE fixture_money.user_subscriptions SET reset_amount=1 WHERE id=70;",
                     "UPDATE fixture_money.user_subscriptions SET user_id=2 WHERE id=70;",
                     "UPDATE fixture_money.user_subscriptions SET quota_version=1 WHERE id=70;",
                     "UPDATE fixture_money.subscription_orders SET plan_snapshot='{}' WHERE id=80;",
                     "UPDATE fixture_money.subscription_plans SET total_amount=6801 WHERE id=4;",
                     "UPDATE fixture_money.top_ups SET pending_credit_rebase_key='other' WHERE id=21;"]:
        reset()
        ok(base, input=conflict)
        before = wallet()
        assert run(base, input=sql).returncode != 0
        assert wallet() == before
        assert ok(base, input="SELECT count(*) FROM fixture_money.wallet_credit_rebases;") == "0"
    for key in ("pending_topups","referrals","topups","tokens","subscription_plans"):
        reset()
        incomplete = copy.deepcopy(snapshot)
        incomplete[key] = []
        before = wallet()
        try:
            incomplete_sql = render(incomplete)
        except ValueError:
            assert key in ("subscription_plans","tokens"), key
        else:
            assert run(base,input=incomplete_sql).returncode != 0, key
        assert wallet() == before
        assert ok(base,input="SELECT count(*) FROM fixture_money.wallet_credit_rebases;") == "0"
    for conflict in ["INSERT INTO fixture_money.tasks VALUES ('FAILURE','',680,0,1771718400);","INSERT INTO fixture_money.midjourneys VALUES ('99%');"]:
        reset()
        ok(base,input=conflict)
        before=wallet()
        assert run(base,input=render(include_other_rights=False)).returncode != 0
        assert wallet()==before
        assert ok(base,input="SELECT count(*) FROM fixture_money.wallet_credit_rebases;") == "0"
    print("Isolated PostgreSQL passed: non-public schema, target identity, delimiter data, four fixed anchors, token ownership, negative debt, idempotency, all conflict rollbacks.")
finally:
    ok(["pg_ctl", "-D", str(data), "-m", "immediate", "-w", "stop"])
    shutil.rmtree(root)
