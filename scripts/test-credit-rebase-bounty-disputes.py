#!/usr/bin/env python3
"""Check immutable dispute plans and actual atomic guards in isolated local PG."""
import copy
from fractions import Fraction
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

import credit_rebase_bounty_disputes as disputes


def literal(value):
    return "'" + value.replace("'", "''") + "'"


def scale(value):
    ratio = Fraction("6.710363")
    q, rem = divmod(value * ratio.denominator, ratio.numerator)
    return q + (rem * 2 >= ratio.numerator)


source = {field: 0 for field in disputes.SOURCE_INTS} | {field: "" for field in disputes.SOURCE_TEXT}
source.update(id=1, challenge_id=1, project_id=1, opened_by_user_id=2, against_user_id=1,
              project_escrow_quota_snapshot=6_710_363, reward_quota_snapshot=6_710_363,
              tip_quota_snapshot=123, created_at=90, updated_at=90,
              challenge_status_snapshot="accepted", status="open")
project = {"id": 1, "owner_user_id": 1, "escrow_quota": 6_710_363, "status": "published"}
challenge = {"id": 1, "project_id": 1, "participant_user_id": 2, "reward_quota": 6_710_363, "paid_at": 0, "status": "accepted"}
owner_claim = source | {"id": 2, "opened_by_user_id": 1, "against_user_id": 2}
resolved = source | {"id": 3, "challenge_id": 99, "status": "resolved_paid", "resolved_at": 95, "resolved_by_user_id": 3, "updated_at": 95}
snapshot = {"snapshot_at": 100, "entities": {"bounty_projects": [project], "bounty_challenges": [challenge], "bounty_disputes": [source, owner_claim, resolved]}}
original_snapshot = copy.deepcopy(snapshot)
bases = disputes.prepare(snapshot, {1, 2}, scale, include=True)
assert snapshot == original_snapshot and len(bases) == 1
assert bases[0] == {"kind": disputes.KIND, "source_id": "1", "user_id": 2, "original_quota": 6_710_363, "rebased_quota": 1_000_000, "source": source}
assert disputes.prepare(snapshot, {1, 2}, scale) == []
for corrupt in (lambda s: s["entities"].pop("bounty_disputes"),
                lambda s: s["entities"]["bounty_disputes"][0].update(statement="private claim text"),
                lambda s: s["entities"]["bounty_disputes"][0].update(against_user_id=3),
                lambda s: s["entities"]["bounty_disputes"][0].update(reward_quota_snapshot=1),
                lambda s: s["entities"]["bounty_challenges"][0].update(participant_user_id=3)):
    bad = copy.deepcopy(snapshot)
    corrupt(bad)
    try:
        disputes.prepare(bad, {1, 2}, scale, include=True)
    except ValueError:
        pass
    else:
        raise AssertionError("unsafe or inconsistent dispute source accepted")
zero = copy.deepcopy(snapshot)
zero["entities"]["bounty_disputes"][0]["reward_quota_snapshot"] = 1
zero["entities"]["bounty_challenges"][0]["reward_quota"] = 1
assert disputes.prepare(zero, {1, 2}, scale, include=True)[0]["rebased_quota"] == 0

plan = {"user_ids": [1, 2], "snapshot_at": 100, "include_bounties": True, "include_other_rights": True, "other_credit_bases": bases}
root = Path(tempfile.mkdtemp(prefix="credit-dispute-pg-", dir=Path.home() / ".cache"))
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
    ok(base, input="CREATE SCHEMA dispute_fixture; CREATE TABLE dispute_fixture.users (id bigint PRIMARY KEY,quota bigint); INSERT INTO dispute_fixture.users VALUES (1,6710363); CREATE TABLE dispute_fixture.wallet_credit_rebases (plan jsonb);")
    fixtures = [("open_source_bounty_projects", [project]), ("open_source_bounty_challenges", [challenge]), ("open_source_bounty_disputes", [source, owner_claim, resolved])]
    for table, rows in fixtures:
        columns = [f'"{key}" ' + ("bigint" if type(value) is int else "text") for key, value in rows[0].items()]
        ok(base, input=f"CREATE TABLE dispute_fixture.{table} (" + ",".join(columns) + ");")

    def reset():
        ok(base, input="TRUNCATE dispute_fixture.wallet_credit_rebases; UPDATE dispute_fixture.users SET quota=6710363;")
        for table, rows in fixtures:
            inserts = []
            for row in rows:
                values = [str(value) if type(value) is int else literal(value) for value in row.values()]
                inserts.append(f"INSERT INTO dispute_fixture.{table} (" + ",".join(row) + ") VALUES (" + ",".join(values) + ");")
            ok(base, input=f"TRUNCATE dispute_fixture.{table}; " + " ".join(inserts))

    def transaction(current_plan=plan):
        ddl, checks, locks = disputes.sql(current_plan, "dispute_fixture", literal)
        assert not ddl
        return ("BEGIN; LOCK TABLE dispute_fixture.users" + locks + " IN ACCESS EXCLUSIVE MODE; "
                "INSERT INTO dispute_fixture.wallet_credit_rebases VALUES (" + literal(json.dumps(current_plan)) + "::jsonb); UPDATE dispute_fixture.users SET quota=1000000; "
                "DO $dispute_fixture$ BEGIN " + " ".join(checks) + " END $dispute_fixture$; "
                "UPDATE dispute_fixture.open_source_bounty_projects SET escrow_quota=1000000; UPDATE dispute_fixture.open_source_bounty_challenges SET reward_quota=1000000; COMMIT;")

    reset()
    before = ok(base, input="SELECT jsonb_agg(to_jsonb(d) ORDER BY id) FROM dispute_fixture.open_source_bounty_disputes d;")
    ok(base, input=transaction())
    assert ok(base, input="SELECT quota FROM dispute_fixture.users;") == "1000000"
    assert ok(base, input="SELECT jsonb_agg(to_jsonb(d) ORDER BY id) FROM dispute_fixture.open_source_bounty_disputes d;") == before
    for conflict in ("UPDATE dispute_fixture.open_source_bounty_disputes SET tip_quota_snapshot=124 WHERE id=1;",
                     "UPDATE dispute_fixture.open_source_bounty_disputes SET status='resolved_paid' WHERE id=1;",
                     "UPDATE dispute_fixture.open_source_bounty_disputes SET reward_quota_snapshot=6710364 WHERE id=1;",
                     "UPDATE dispute_fixture.open_source_bounty_challenges SET participant_user_id=3;",
                     "UPDATE dispute_fixture.open_source_bounty_challenges SET paid_at=99;",
                     "UPDATE dispute_fixture.open_source_bounty_projects SET escrow_quota=6710362;",
                     "INSERT INTO dispute_fixture.open_source_bounty_disputes SELECT 4,challenge_id,project_id,opened_by_user_id,against_user_id,project_escrow_quota_snapshot,reward_quota_snapshot,tip_quota_snapshot,resolved_by_user_id,created_at,updated_at,resolved_at,challenge_status_snapshot,status FROM dispute_fixture.open_source_bounty_disputes WHERE id=1;"):
        reset()
        ok(base, input=conflict)
        assert run(base, input=transaction()).returncode != 0, conflict
        assert ok(base, input="SELECT quota FROM dispute_fixture.users;") == "6710363"
        assert ok(base, input="SELECT count(*) FROM dispute_fixture.wallet_credit_rebases;") == "0"
    reset()
    assert run(base, input=transaction(plan | {"other_credit_bases": []})).returncode != 0
    assert ok(base, input="SELECT count(*) FROM dispute_fixture.wallet_credit_rebases;") == "0"
    print("Bounty dispute isolated PostgreSQL passed: accepted future claim, safe immutable evidence, exact payout basis, zero rounding, 8 atomic conflict rollbacks.")
finally:
    ok(["pg_ctl", "-D", str(data), "-m", "immediate", "-w", "stop"])
    shutil.rmtree(root)
