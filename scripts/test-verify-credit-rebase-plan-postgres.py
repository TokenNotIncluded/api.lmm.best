#!/usr/bin/env python3
"""Cross-check the independent verifier with an isolated Unix-socket PostgreSQL.

The fixture has no TCP listener and strips all inherited PG connection settings.
--planner-dir can point to an integration worktree with the final planner schema.
Only this fixture runner invokes psql; the verifier itself remains offline.
"""
import argparse
import copy
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

spec = importlib.util.spec_from_file_location("verifier_fixture", Path(__file__).with_name("test-verify-credit-rebase-plan.py"))
f = importlib.util.module_from_spec(spec)
spec.loader.exec_module(f)
v = f.v


def snapshot_from_plan(plan):
    snapshot = {"version":1,"target":plan["target"],"applied_migration_ids":[],"snapshot_at":plan["snapshot_at"],"users":[{"id":r["id"],"quota":r["quota"],"aff_quota":r["aff_quota"]} for r in plan["user_sources"]],"tokens":[{key:r[key] for key in ("id","user_id","remain_quota","unlimited_quota")} for r in plan["token_sources"]],"user_sources":plan["user_sources"],"token_sources":plan["token_sources"],"options":{e["key"]:e["before"] for e in plan["option_entries"]},"price_review":{"status":"verified","evidence":"isolated synthetic independently verified prices","option_corrections":[e for e in plan["option_entries"] if e["key"] not in v.ANCHORS],"unchanged_option_values":{g["key"]:g["value"] for g in plan["option_guards"] if not g["absent"]},"absent_unchanged_options":[g["key"] for g in plan["option_guards"] if g["absent"]]},"topups":[b["source"] for b in plan["refund_bases"]]+plan["noncash_topups"],"pending_topups":[b["source"] for b in plan["pending_bases"]+plan["blocked_pending_bases"]],"referrals":[b["source"] for b in plan["referral_bases"]],"entities":{},"subscriptions":[e["source"] for e in plan["subscriptions"]],"subscription_orders":plan["subscription_order_sources"],"subscription_plans":[e["source"] for e in plan["subscription_plan_updates"]],"subscription_payment_events":plan["subscription_payment_events"],"subscription_payment_refunds":plan["subscription_payment_refunds"],"other_rights":{},"obligations":plan["obligations"]}
    for guard in plan["option_guards"]:
        if not guard["absent"]:
            snapshot["options"][guard["key"]] = guard["value"]
    snapshot["snapshot_state"] = "frozen_writers_stopped"
    for key,table in (("redemptions","redemptions"),("bounty_projects","open_source_bounty_projects"),("bounty_challenges","open_source_bounty_challenges"),("bounty_disputes","open_source_bounty_disputes")):
        snapshot["entities"][key] = [e["source"] for e in plan["entity_updates"] if e["table"] == table]
    names = {"public_relay_tip_pool":"public_relay_tip_pools","assistant_gift":"assistant_gifts","grant_gift":"grant_gifts","ai_directory_ad_refund":"ai_directory_ad_refunds","violation_fee_refund":"violation_fee_refunds","email_refund_pool":"hero_sms_email_refunds"}
    for kind,key in names.items():
        snapshot["other_rights"][key] = [e["source"] for e in plan["other_credit_bases"] if e["kind"] == kind]
    snapshot["other_rights"]["hero_sms_email_activations"] = [s for e in plan["other_credit_bases"] if e["kind"] == "email_refund_pool" for s in e["activation_sources"]]
    return snapshot


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--planner-dir", type=Path, default=Path(__file__).parent)
    args = parser.parse_args()
    sys.path.insert(0,str(args.planner_dir.resolve()))
    pspec = importlib.util.spec_from_file_location("fixture_planner",args.planner_dir / "preview-credit-balance-rebase.py")
    planner = importlib.util.module_from_spec(pspec)
    pspec.loader.exec_module(planner)
    environment = {k:value for k,value in os.environ.items() if not k.startswith("PG")}
    root = Path(tempfile.mkdtemp(prefix="credit-independent-pg-",dir=Path.home()/".cache"))
    data,sock = root/"data",root/"socket"
    sock.mkdir()

    def run(command, **kwargs):
        return subprocess.run(command,text=True,capture_output=True,env=environment,**kwargs)

    def ok(command, **kwargs):
        result = run(command,**kwargs)
        if result.returncode:
            raise RuntimeError(result.stderr)
        return result.stdout.strip()

    ok(["initdb","-D",str(data),"-A","trust","--no-instructions"])
    started = False
    base = ["psql","-X","-q","-A","-t","-v","ON_ERROR_STOP=1","-h",str(sock),"-d","postgres"]
    try:
        ok(["pg_ctl","-D",str(data),"-l",str(root/"pg.log"),"-o",f"-c listen_addresses= -c unix_socket_directories={sock}","-w","start"])
        started = True
        original = f.orphan_case_plan()
        original["target"]["system_identifier"] = ok(base,input="SELECT system_identifier::text FROM pg_control_system();")
        snapshot = snapshot_from_plan(original)
        plan = planner.make_plan(snapshot,divisor_text="6.8",migration_id="synthetic-v1",user_ids=[1,2],rounding="half-away-from-zero",restore_fixed_anchors=True,include_affiliate=True,include_token_limits=True,include_pending_topups=True,include_redemptions=True,include_bounties=True,include_subscriptions=True,include_other_rights=True)
        v.validate_plan(plan)
        schema = v.ident(plan["target"]["schema"])
        ok(base,input="CREATE SCHEMA " + schema + ";")
        plan["target"]["database_oid"] = ok(base,input="SELECT oid::text FROM pg_database WHERE datname=current_database();")
        plan["target"]["schema_oid"] = ok(base,input="SELECT oid::text FROM pg_namespace WHERE nspname=" + v.literal(plan["target"]["schema"]) + ";")
        f.seal(plan)
        tables = {}

        def add(table, row):
            tables.setdefault(table,[]).append(row)

        for row in plan["user_sources"]:
            add("users",row)
        for row in plan["token_sources"]:
            add("tokens",row)
        for e in plan["option_entries"]:
            add("options",{"key":e["key"],"value":e["before"]})
        for g in plan["option_guards"]:
            if not g["absent"]:
                add("options",{"key":g["key"],"value":g["value"]})
        for s in [b["source"] for b in plan["refund_bases"]] + plan["noncash_topups"] + [b["source"] for b in plan["pending_bases"] + plan["blocked_pending_bases"]]:
            # Metadata is intentionally absent on pre-migration fixtures.
            row = {key:s[key] for key in v.TOPUP_COLUMNS}
            row["failure_reason_code"] = s.get("failure_reason_code","")
            add("top_ups",row)
        for b in plan["referral_bases"]:
            add("referral_rewards",b["source"])
        for e in plan["entity_updates"]:
            add(e["table"],e["source"])
        if "open_source_bounty_disputes" not in tables:
            empty_disputes = ",".join(v.ident(key)+(" text" if key in {"status","challenge_status_snapshot"} else " bigint") for key in sorted(v.ENTITY_COLUMNS["open_source_bounty_disputes"]))
            ok(base,input=f"CREATE TABLE {schema}.open_source_bounty_disputes ({empty_disputes});")
        for e in plan["subscriptions"]:
            add("user_subscriptions",{key:value for key,value in e["source"].items() if key not in {"reset_amount","renewal_amount"}})
        for s in plan["subscription_order_sources"]:
            add("subscription_orders",s)
        for e in plan["subscription_plan_updates"]:
            add("subscription_plans",e["source"])
        for key in ("subscription_payment_events","subscription_payment_refunds"):
            for s in plan[key]:
                add(key,s)
        for e in plan["other_credit_bases"]:
            if e["kind"] == "bounty_dispute_reward":
                continue
            table,_ = v.OTHER_SPECS[e["kind"]]
            add(table,{key:value for key,value in e["source"].items() if key != "last_refund_ledger_id"})
            for s in e.get("activation_sources",[]):
                add("hero_sms_email_activations",s)
        add("hero_sms_email_quota_ledgers",{"id":7,"order_id":"synthetic-order","entry_type":"refund","amount_quota":68})
        for table,rows in tables.items():
            fields = sorted({key for row in rows for key in row})
            columns = []
            for key in fields:
                values = [row.get(key) for row in rows]
                if key in {"money","price_amount"}:
                    kind = "double precision" if key == "money" else "numeric"
                elif key == "deleted_at":
                    kind = "timestamptz"
                elif any(type(value) is bool for value in values):
                    kind = "boolean"
                elif any(isinstance(value,str) for value in values) or key == "plan_snapshot":
                    kind = "text"
                else:
                    kind = "bigint"
                primary = " PRIMARY KEY" if key == ("key" if table == "options" else "id") else ""
                columns.append(v.ident(key)+" "+kind+primary)
            ok(base,input="CREATE TABLE " + schema + "." + v.ident(table) + " (" + ",".join(columns) + ");")
            for row in rows:
                ok(base,input="INSERT INTO " + schema + "." + v.ident(table) + " (" + ",".join(v.ident(key) for key in row) + ") VALUES (" + ",".join(v.literal(value) for value in row.values()) + ");")
        for table,columns in {
            "wallet_transfers":"id bigint,status text",
            "tool_market_calls":"id bigint,settlement_status text",
            "tasks":"id bigint,status text,refund_status text,quota bigint,refund_quota bigint,submit_time bigint",
            "midjourneys":"id bigint,progress text",
            "subscription_pre_consume_records":"id bigint,status text",
            "hero_sms_sms_orders":"id bigint,status text",
        }.items():
            ok(base,input=f"CREATE TABLE {schema}.{table} ({columns});")
        before = v.postgres_verification_sql(plan,"before")
        after = v.postgres_verification_sql(plan,"after")
        ok(base,input=before)
        assert run(base,input=after).returncode != 0,"after must reject unapplied fixture"
        for field,wrong in (("database","other_database"),("schema","other_schema"),("system_identifier","1"),("database_oid","1"),("schema_oid","1")):
            bad = copy.deepcopy(plan)
            bad["target"][field] = wrong
            f.seal(bad)
            assert run(base,input=v.postgres_verification_sql(bad,"before")).returncode != 0,field

        def reject_mutation(sql,verification):
            # Change is made in a fixture transaction and rolled back on rejection.
            body = verification.replace("BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;", "BEGIN;")
            body = body.replace("DO $credit_verify_", sql + "\nDO $credit_verify_",1)
            result = run(base,input=body)
            assert result.returncode != 0,sql
            if not sql.startswith("DROP TABLE"):
                assert "credit verification failed:" in result.stderr, "fixture mutation failed before the verifier assertion: " + result.stderr

        # Read-only transaction catches any accidental future writes in verifier SQL.
        assert run(base,input=before.replace("COMMIT;",f"UPDATE {schema}.users SET quota=0;\nCOMMIT;")).returncode != 0
        for mutation in [f"UPDATE {schema}.users SET used_quota=124 WHERE id=1;",f"UPDATE {schema}.tokens SET used_quota=81 WHERE id=11;",f"INSERT INTO {schema}.tokens SELECT 12,user_id,remain_quota,used_quota,unlimited_quota,status,created_time,accessed_time,expired_time,deleted_at FROM {schema}.tokens WHERE id=11;"]:
            # INSERT is intentionally explicit below, independent of column order.
            if mutation.startswith("INSERT"):
                mutation = f"INSERT INTO {schema}.tokens (id,user_id,remain_quota,used_quota,unlimited_quota,status,created_time,accessed_time,expired_time,deleted_at) SELECT 12,user_id,remain_quota,used_quota,unlimited_quota,status,created_time,accessed_time,expired_time,deleted_at FROM {schema}.tokens WHERE id=11;"
            reject_mutation(mutation,before)
        ok(base,input=planner.postgres_sql(plan))
        ok(base,input=after)
        assert ok(base,input=f"SELECT reward_quota FROM {schema}.open_source_bounty_challenges WHERE id=61;") == "680000","expired rejected historical reward must remain unchanged"
        assert run(base,input=before).returncode != 0,"before must reject applied audit"
        ok(base,input=planner.postgres_sql(plan))
        ok(base,input=after)
        mutations = [
            f"UPDATE {schema}.users SET quota=1001 WHERE id=1;",
            f"UPDATE {schema}.users SET used_quota=124 WHERE id=1;",
            f"UPDATE {schema}.tokens SET user_id=2 WHERE id=10;",
            f"UPDATE {schema}.tokens SET remain_quota=681 WHERE id=11;",
            f"UPDATE {schema}.top_ups SET money=0.002 WHERE id=20;",
            f"UPDATE {schema}.top_ups SET refunded_quota=681 WHERE id=20;",
            f"UPDATE {schema}.top_ups SET pending_credit_rebase_effective_quota=1 WHERE id=23;",
            f"INSERT INTO {schema}.users (id,quota,aff_quota,used_quota,request_count,aff_history,aff_count,status,deleted_at) SELECT 3,quota,aff_quota,used_quota,request_count,aff_history,aff_count,status,deleted_at FROM {schema}.users WHERE id=1;",
            f"UPDATE {schema}.referral_rewards SET revoked_quota=69 WHERE id=9;",
            f"UPDATE {schema}.open_source_bounty_projects SET platform_fee_quota=69 WHERE id=50;",
            f"UPDATE {schema}.open_source_bounty_challenges SET tip_quota=126 WHERE id=60;",
            f"UPDATE {schema}.open_source_bounty_challenges SET reward_quota=100000 WHERE id=61;",
            f"UPDATE {schema}.open_source_bounty_disputes SET reward_quota_snapshot=681 WHERE id=202;",
            f"UPDATE {schema}.user_subscriptions SET amount_used=6121 WHERE id=70;",
            f"UPDATE {schema}.user_subscriptions SET renewal_amount=1001 WHERE id=70;",
            f"UPDATE {schema}.subscription_orders SET plan_snapshot='{{}}' WHERE id=80;",
            f"UPDATE {schema}.subscription_orders SET plan_snapshot='{{}}' WHERE id=82;",
            f"UPDATE {schema}.subscription_plans SET price_amount=0.02 WHERE id=4;",
            f"UPDATE {schema}.subscription_payment_events SET settlement_amount_micros=10001 WHERE id=90;",
            f"UPDATE {schema}.subscription_payment_refunds SET quota_revoked=1 WHERE id=91;",
            f"UPDATE {schema}.options SET value='6.80' WHERE key='USDExchangeRate';",
            f"UPDATE {schema}.options SET value='500001' WHERE key='QuotaPerUnit';",
            f"UPDATE {schema}.options SET value='{{}}' WHERE key='ModelPrice';",
            f"UPDATE {schema}.wallet_credit_rebases SET plan_sha256=repeat('0',64);",
            f"UPDATE {schema}.wallet_credit_rebases SET plan=jsonb_set(plan,'{{other_credit_bases,0,rebased_quota}}','999');",
            f"UPDATE {schema}.wallet_topup_credit_rebases SET refundable_quota=901;",
            f"UPDATE {schema}.wallet_referral_credit_rebases SET rebased_penalty_quota=11;",
            f"UPDATE {schema}.subscription_order_credit_rebases SET reset_quota=1001;",
            f"UPDATE {schema}.public_relay_contributions SET withdrawn_quota=69;",
            f"UPDATE {schema}.assistant_new_user_gifts SET amount_cents=2;",
            f"UPDATE {schema}.gifts SET quota=681;",
            f"UPDATE {schema}.ai_directory_ads SET charged_quota=681;",
            f"UPDATE {schema}.violation_fee_records SET charged_quota=681;",
            f"UPDATE {schema}.hero_sms_email_orders SET refunded_quota=69;",
            f"UPDATE {schema}.hero_sms_email_activations SET refund_quota=69;",
            f"INSERT INTO {schema}.hero_sms_email_quota_ledgers (id,order_id,entry_type,amount_quota) VALUES (8,'synthetic-order','refund',1);",
            f"UPDATE {schema}.hero_sms_email_quota_ledgers SET original_amount_quota=68 WHERE id=7;",
            f"DROP TABLE {schema}.wallet_transfers;",
            f"DELETE FROM {schema}.redemptions WHERE id=30;",
            f"DELETE FROM {schema}.subscription_order_credit_rebases;",
            f"INSERT INTO {schema}.wallet_transfers VALUES (1,'pending');",
            f"INSERT INTO {schema}.tool_market_calls VALUES (1,'held');",
            f"INSERT INTO {schema}.tasks VALUES (1,'RUNNING','',0,0,0);",
            f"INSERT INTO {schema}.tasks VALUES (1,'FAILURE','',1,0,1771718400);",
            f"INSERT INTO {schema}.midjourneys VALUES (1,'99%');",
            f"INSERT INTO {schema}.subscription_pre_consume_records VALUES (1,'consumed');",
            f"INSERT INTO {schema}.hero_sms_sms_orders VALUES (1,'active');",
            f"UPDATE {schema}.hero_sms_email_orders SET status='reconciling';",
            f"UPDATE {schema}.hero_sms_email_activations SET status='cancel_pending';",
        ]
        for sql in mutations:
            reject_mutation(sql,after)
        ok(base,input=after)
        assert ok(base,input=f"SELECT original_amount_quota IS NULL FROM {schema}.hero_sms_email_quota_ledgers WHERE id=7;") == "t","historical email refund ledger must remain unfilled"
        print(f"Independent verifier PostgreSQL passed: before/after, read-only transaction, target identities, idempotency and {len(mutations)+3} rejected source/basis/count/obligation mutations; no TCP listener.")
    finally:
        if started:
            ok(["pg_ctl","-D",str(data),"-m","immediate","-w","stop"])
        shutil.rmtree(root)


if __name__ == "__main__":
    main()
