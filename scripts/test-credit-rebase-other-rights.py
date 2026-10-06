#!/usr/bin/env python3
"""Verify private export and atomic future-right CAS in an isolated local PG."""
import copy
from fractions import Fraction
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

import credit_rebase_other_rights as rights


def literal(value):
    return "'" + value.replace("'", "''") + "'"


def scale(value):
    q, rem = divmod(value * Fraction("6.710363").denominator, Fraction("6.710363").numerator)
    return q + (rem * 2 >= Fraction("6.710363").numerator)


snapshot = {"snapshot_at": 100, "other_rights": {spec[0]: [] for spec in rights.SPECS.values()}}
for index, (kind, (key, table, ints, texts, bools)) in enumerate(rights.SPECS.items(), 1):
    row = {field: 0 for field in ints} | {field: "" for field in texts} | {field: True for field in bools}
    row.update(id="historical-email" if kind == "email_refund_pool" else index)
    if kind == "public_relay_tip_pool":
        row.update(user_id=1, tip_quota=6_710_463, withdrawn_quota=100, status="approved")
    elif kind == "assistant_gift":
        row.update(user_id=1, quota=6_710_363, amount_cents=100, status="offered")
    elif kind == "grant_gift":
        row.update(quota=1, start_at=50, end_at=200)
    elif kind == "ai_directory_ad_refund":
        row.update(owner_user_id=1, charged_quota=6_710_363, bid_cents=100, status="active", expires_at=200)
    elif kind == "violation_fee_refund":
        row.update(user_id=1, charged_quota=6_710_363, requested_quota=6_710_363, status="charged", error_code="moderation.fixture ' \\ $credit_rebase$")
    else:
        row.update(user_id=1, charge_quota=6_710_463, refunded_quota=100, quantity=1, status="completed", operation="purchase", last_refund_ledger_id=5)
    snapshot["other_rights"][key] = [row]
activation = {field: 0 for field in rights.ACTIVATION_INTS} | {field: "" for field in rights.ACTIVATION_TEXT}
activation.update(id="terminal-activation", order_id="historical-email", user_id=1, charge_quota=6_710_463, status="completed")
snapshot["other_rights"]["hero_sms_email_activations"] = [activation]
snapshot["obligations"] = {key: 0 for key in rights.OBLIGATIONS}
assert rights.validate_obligations(snapshot) == snapshot["obligations"]

plan = {"user_ids": [1], "include_other_rights": True, "snapshot_at": 100,
        "divisor": "6.710363", "rounding": "half-away-from-zero"}
plan["other_credit_bases"] = rights.prepare(snapshot, {1}, scale, include=True)
assert len(plan["other_credit_bases"]) == 6
assert next(b for b in plan["other_credit_bases"] if b["kind"] == "grant_gift")["rebased_quota"] == 0
assert all(b["rebased_quota"] == 1_000_000 for b in plan["other_credit_bases"] if b["kind"] != "grant_gift")
for corrupt in (lambda s: s["other_rights"].pop("assistant_gifts"),
                lambda s: s["other_rights"]["assistant_gifts"][0].update(secret="must not enter plan"),
                lambda s: s["other_rights"]["hero_sms_email_activations"].clear(),
                lambda s: s["other_rights"]["public_relay_tip_pools"][0].update(withdrawn_quota=7_000_000)):
    bad = copy.deepcopy(snapshot)
    corrupt(bad)
    try:
        rights.prepare(bad, {1}, scale, include=True)
    except ValueError:
        pass
    else:
        raise AssertionError("invalid source was accepted")

root = Path(tempfile.mkdtemp(prefix="credit-other-rights-pg-", dir=Path.home() / ".cache"))
data, sock = root / "data", root / "socket"
sock.mkdir()
env = {key: value for key, value in os.environ.items() if not key.startswith("PG")}


def run(command, **kwargs):
    return subprocess.run(command, text=True, capture_output=True, env=env, **kwargs)


def ok(command, **kwargs):
    result = run(command, **kwargs)
    if result.returncode:
        raise RuntimeError(result.stderr)
    return result.stdout.strip()


ok(["initdb", "-D", str(data), "-A", "trust", "--no-instructions"])
ok(["pg_ctl", "-D", str(data), "-l", str(root / "pg.log"), "-o", f"-c listen_addresses= -c unix_socket_directories={sock}", "-w", "start"])
base = ["psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-h", str(sock), "-d", "postgres"]
try:
    ok(base, input="CREATE SCHEMA fixture_more; CREATE TABLE fixture_more.users (id bigint PRIMARY KEY,quota bigint); INSERT INTO fixture_more.users VALUES (1,6710363); CREATE TABLE fixture_more.wallet_credit_rebases (migration_id text PRIMARY KEY,plan jsonb NOT NULL);")
    fixtures = []
    for kind, (key, table, ints, texts, bools) in rights.SPECS.items():
        columns = [f'"{f}" bigint' for f in ints if f != "last_refund_ledger_id"] + [f'"{f}" text' for f in texts] + [f'"{f}" bool' for f in bools]
        ok(base, input=f"CREATE TABLE fixture_more.{table} (" + ",".join(columns) + ");")
        row = {k: v for k, v in snapshot["other_rights"][key][0].items() if k != "last_refund_ledger_id"}
        fixtures.append((table, row))
    columns = [f'"{f}" bigint' for f in rights.ACTIVATION_INTS] + [f'"{f}" text' for f in rights.ACTIVATION_TEXT]
    ok(base, input="CREATE TABLE fixture_more.hero_sms_email_activations (" + ",".join(columns) + "); CREATE TABLE fixture_more.hero_sms_email_quota_ledgers (id bigint,order_id text,entry_type text,amount_quota bigint);")
    ok(base, input="CREATE TABLE fixture_more.wallet_transfers (status text); CREATE TABLE fixture_more.tool_market_calls (settlement_status text); CREATE TABLE fixture_more.tasks (status text,refund_status text,quota bigint,refund_quota bigint,submit_time bigint); CREATE TABLE fixture_more.midjourneys (progress text); CREATE TABLE fixture_more.subscription_pre_consume_records (status text); CREATE TABLE fixture_more.hero_sms_sms_orders (status text);")
    fixtures.append(("hero_sms_email_activations", activation))

    def reset():
        ok(base, input="TRUNCATE fixture_more.wallet_credit_rebases, fixture_more.hero_sms_email_quota_ledgers; UPDATE fixture_more.users SET quota=6710363;")
        for table, row in fixtures:
            encoded = ["true" if value is True else "false" if value is False else str(value) if type(value) is int else literal(value) for value in row.values()]
            ok(base, input=f"TRUNCATE fixture_more.{table}; INSERT INTO fixture_more.{table} (" + ",".join(row) + ") VALUES (" + ",".join(encoded) + ");")
        ok(base, input="INSERT INTO fixture_more.hero_sms_email_quota_ledgers VALUES (5,'historical-email','refund',100);")

    def transaction(current_plan=plan):
        ddl, checks, locks = rights.sql(current_plan, "fixture_more", literal)
        checks += rights.obligation_guards("fixture_more")
        return "BEGIN; SET LOCAL standard_conforming_strings=on; LOCK TABLE fixture_more.users" + locks + rights.obligation_locks("fixture_more") + " IN ACCESS EXCLUSIVE MODE; " + " ".join(ddl) + " INSERT INTO fixture_more.wallet_credit_rebases VALUES ('fixture'," + literal(json.dumps(current_plan)) + "::jsonb); UPDATE fixture_more.users SET quota=1000000; DO $fixture_more$ BEGIN " + " ".join(checks) + " END $fixture_more$; COMMIT;"

    reset()
    exported = json.loads(ok(base + ["-v", "target_schema=fixture_more", "-v", "snapshot_at=100", "-f", str(Path(__file__).with_name("export-credit-rebase-other-rights-private.sql"))]))
    assert exported == snapshot, "private export must exactly match the approved source projection"
    before = {table: ok(base, input=f"SELECT jsonb_agg(to_jsonb(r)) FROM fixture_more.{table} r;") for table, _ in fixtures}
    ok(base, input=transaction())
    assert ok(base, input="SELECT quota FROM fixture_more.users;") == "1000000"
    assert all(ok(base, input=f"SELECT jsonb_agg(to_jsonb(r)) FROM fixture_more.{table} r;") == value for table, value in before.items()), "history must remain unchanged"
    conflicts = ["UPDATE fixture_more.public_relay_contributions SET withdrawn_quota=101;",
                 "UPDATE fixture_more.assistant_new_user_gifts SET status='claimed';",
                 "UPDATE fixture_more.gifts SET quota=2;",
                 "UPDATE fixture_more.ai_directory_ads SET refunded_at=99;",
                 "UPDATE fixture_more.violation_fee_records SET user_id=2;",
                 "UPDATE fixture_more.hero_sms_email_orders SET refunded_quota=101;",
                 "UPDATE fixture_more.hero_sms_email_activations SET status='cancel_pending';",
                 "INSERT INTO fixture_more.hero_sms_email_quota_ledgers VALUES (6,'historical-email','refund',1);"]
    for conflict in conflicts:
        reset()
        ok(base, input=conflict)
        assert run(base, input=transaction()).returncode != 0, conflict
        assert ok(base, input="SELECT quota FROM fixture_more.users;") == "6710363"
        assert ok(base, input="SELECT count(*) FROM fixture_more.wallet_credit_rebases;") == "0"
    reset()
    incomplete = copy.deepcopy(plan)
    incomplete["other_credit_bases"] = [b for b in incomplete["other_credit_bases"] if b["kind"] != "public_relay_tip_pool"]
    assert run(base, input=transaction(incomplete)).returncode != 0
    assert ok(base, input="SELECT quota FROM fixture_more.users;") == "6710363"
    for insert in ("INSERT INTO fixture_more.tasks VALUES ('FAILURE','',6,0,1771718400);", "INSERT INTO fixture_more.midjourneys VALUES ('90%');"):
        reset()
        ok(base, input=insert)
        assert run(base, input=transaction()).returncode != 0
        assert ok(base, input="SELECT quota FROM fixture_more.users;") == "6710363"
        assert ok(base, input="SELECT count(*) FROM fixture_more.wallet_credit_rebases;") == "0"
        ok(base, input="TRUNCATE fixture_more.tasks,fixture_more.midjourneys;")
    # Optional future-right mode cannot switch off the independent blockers.
    reset()
    ok(base, input="INSERT INTO fixture_more.midjourneys VALUES ('90%');")
    no_rights = plan | {"include_other_rights": False, "other_credit_bases": []}
    assert run(base, input=transaction(no_rights)).returncode != 0
    assert ok(base, input="SELECT quota FROM fixture_more.users;") == "6710363"
    print("Other-rights isolated PostgreSQL passed: exact private export, unchanged source facts, zero rounding, full-count guards, 8 conflict rollbacks.")
finally:
    ok(["pg_ctl", "-D", str(data), "-m", "immediate", "-w", "stop"])
    shutil.rmtree(root)
