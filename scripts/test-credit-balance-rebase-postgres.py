#!/usr/bin/env python3
"""Real transaction verification in a new local Unix-socket PostgreSQL fixture only."""
import copy
import importlib.util
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
INSERT INTO fixture_money.users VALUES (1,500000000,680,123),(2,-86911,0,45);
INSERT INTO fixture_money.tokens VALUES (10,1,680,false);
""")
    snapshot = {"version": 1, "applied_migration_ids": [],
                "target": {"database": "postgres", "schema": "fixture_money",
                           "system_identifier": ok(base, input="SELECT system_identifier FROM pg_control_system();")},
                "topups": [{"id":20,"user_id":1,"status":"success","credited_quota":6800,"amount":0,"platform_amount_micros":0,"settled_amount_micros":1000,"expected_amount_micros":1000,"refunded_quota":680,"refunded_amount_micros":100,"money":"0.001","payment_provider":"stripe","payment_method":"stripe","settlement_currency":"USD","effective_credited_quota":6800,"paid_amount_micros":1000}],
                "users": [{"id": 1, "quota": 500000000}, {"id": 2, "quota": -86911}],
                "tokens": [{"id": 10, "user_id": 1, "remain_quota": 680, "unlimited_quota": False}],
                "options": {"USDExchangeRate":"6.8", "CreditsPerUSD": "3359744", "PublicCreditsPerUSD": "100000",
                            "LegacyPricingQuotaPerUnit": "500000", "QuotaPerUnit": "500000"},
                "price_review": {"status": "verified", "evidence": "synthetic $credit_rebase$ quote ' and slash \\ fixture",
                                 "option_corrections": []}}
    from credit_rebase_entitlements import SPECS
    def entity_source(table, **overrides):
        row = {key: 0 for key in SPECS[table]["int"]}
        row.update({key: "" for key in SPECS[table]["text"]})
        row.update({key: None for key in SPECS[table]["null"]})
        return row | overrides
    red = entity_source("redemptions", id=30, user_id=1, quota=680, status=1, reward_type="quota")
    bounty = entity_source("open_source_bounty_projects", id=50, owner_user_id=1, escrow_quota=6800, reward_quota=680, net_reward_quota=612, platform_fee_quota=68, status="published")
    challenge = entity_source("open_source_bounty_challenges", id=60, project_id=50, participant_user_id=2, reward_quota=612, tip_quota=125, status="accepted")
    for table, row in [("redemptions", red), ("open_source_bounty_projects", bounty), ("open_source_bounty_challenges", challenge)]:
        spec = SPECS[table]
        columns = ["id bigint PRIMARY KEY"] + [key + " bigint" for key in spec["int"]] + [key + " text" for key in spec["text"]] + [key + " timestamptz" for key in spec["null"]]
        values = ["NULL" if value is None else r.sql_literal(value) if isinstance(value, str) else str(value) for value in row.values()]
        ok(base, input=f"CREATE TABLE fixture_money.{table} (" + ",".join(columns) + "); INSERT INTO fixture_money." + table + " (" + ",".join(row.keys()) + ") VALUES (" + ",".join(values) + ");")
    snapshot["snapshot_at"] = 100
    snapshot["entities"] = {"redemptions":[red], "bounty_projects":[bounty], "bounty_challenges":[challenge]}
    kw = dict(divisor_text="6.8", migration_id="fixture-v1", user_ids=[1, 2],
              rounding="half-away-from-zero", restore_fixed_anchors=True, include_token_limits=True, include_redemptions=True, include_bounties=True)

    def render(source=snapshot, **overrides):
        return r.postgres_sql(r.make_plan(source, **(kw | overrides)))

    def wallet():
        return ok(base, input="SELECT id,quota,used_quota FROM fixture_money.users ORDER BY id;")

    def reset():
        ok(base, input="""
TRUNCATE fixture_money.wallet_topup_credit_rebases, fixture_money.wallet_credit_rebases;
UPDATE fixture_money.options SET value=CASE WHEN key='USDExchangeRate' THEN '6.8' WHEN key='CreditsPerUSD' THEN '3359744' WHEN key='PublicCreditsPerUSD' THEN '100000' ELSE '500000' END;
UPDATE fixture_money.users SET quota=CASE WHEN id=1 THEN 500000000 ELSE -86911 END;
UPDATE fixture_money.tokens SET user_id=1, remain_quota=680;
UPDATE fixture_money.top_ups SET refunded_quota=680;
UPDATE fixture_money.redemptions SET quota=680,user_id=1;
UPDATE fixture_money.open_source_bounty_projects SET escrow_quota=6800,reward_quota=680,net_reward_quota=612,updated_at=0;
UPDATE fixture_money.open_source_bounty_challenges SET reward_quota=612,participant_user_id=2;

""")

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
    first = wallet()
    assert first == "1|73529412|123\n2|-12781|45", first
    assert ok(base, input="SELECT count(*) FROM fixture_money.options WHERE value='500000';") == "4"
    assert ok(base, input="SELECT remain_quota FROM fixture_money.tokens WHERE id=10;") == "100"
    ok(base, input=sql)
    assert wallet() == first
    assert run(base, input=render(migration_id="fixture-v2")).returncode != 0
    assert ok(base, input="SELECT count(*) FROM fixture_money.wallet_credit_rebases;") == "1"

    for conflict in ["UPDATE fixture_money.users SET quota=-86910 WHERE id=2;",
                     "UPDATE fixture_money.options SET value='unexpected-anchor' WHERE key='CreditsPerUSD';",
                     "UPDATE fixture_money.tokens SET user_id=2 WHERE id=10;",
                     "UPDATE fixture_money.top_ups SET refunded_quota=681 WHERE id=20;",
                     "UPDATE fixture_money.options SET value='6.710363' WHERE key='USDExchangeRate';",
                     "UPDATE fixture_money.redemptions SET user_id=2 WHERE id=30;",
                     "UPDATE fixture_money.open_source_bounty_projects SET updated_at=1 WHERE id=50;",
                     "UPDATE fixture_money.open_source_bounty_challenges SET participant_user_id=1 WHERE id=60;"]:
        reset()
        ok(base, input=conflict)
        before = wallet()
        assert run(base, input=sql).returncode != 0
        assert wallet() == before
        assert ok(base, input="SELECT count(*) FROM fixture_money.wallet_credit_rebases;") == "0"
    print("Isolated PostgreSQL passed: non-public schema, target identity, delimiter data, four fixed anchors, token ownership, negative debt, idempotency, all conflict rollbacks.")
finally:
    ok(["pg_ctl", "-D", str(data), "-m", "immediate", "-w", "stop"])
    shutil.rmtree(root)
