"""Bounded offline opening adapter. Called only by migrate-legacy-financial.py.

This module has no HTTP interface and never posts generic credits. Review files
are governance evidence, not signatures: only a separately provisioned offline
owner can execute the generated, whole-batch Core opening transaction.
"""
from __future__ import annotations

import fcntl
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import stat

SCOPE = "financial-opening-only"
DOMAIN = "lmm.offline-opening.readback.v1"
EXCLUDED = ["historical-counters", "key-quota", "subscriptions", "payment-refund-basis", "escrow", "in-flight-claims"]
TOUCHED = {"core_billing.ledger_accounts_id_seq", "core_billing.ledger_journals_id_seq"}
CHANGED = {"core_billing.ledger_accounts", "core_billing.balance_state", "core_billing.ledger_operations",
           "core_billing.ledger_journals", "core_billing.ledger_entries", "core_billing.opening_import_runs",
           "core_billing.opening_imports", "core_migration.import_runs"}
REASON = "Reviewed offline opening; original facts retained by immutable import receipt"
TABLE_PREDICATE = "c.relkind IN ('r','p') AND n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'"


def source_hashes(F):
    return {"financial_planner": F.IDENTITY.sha256_file(Path(F.__file__)),
            "identity_helper": F.IDENTITY.sha256_file(Path(F.IDENTITY.__file__)),
            "opening_writer": F.IDENTITY.sha256_file(Path(__file__))}


def typed_equal(F, actual, expected):
    # Python considers False == 0 and Decimal('4.0') == 4. Approval and
    # contract facts instead use the planner's existing per-type hash domain.
    return F.digest(actual) == F.digest(expected)


def key_for(payload):
    # Current Core uses jsonb_build_array(migration,entity)::text. Both strings
    # are restricted ASCII identifiers, so PostgreSQL's separator is explicit.
    return hashlib.sha256(json.dumps([payload["migration_id"], payload["source_entity_key"]],
                                    ensure_ascii=False, separators=(", ", ": ")).encode()).hexdigest()


def expected_readback(F, payloads):
    positions, clearing, revision = [], 0, 0
    for p in payloads:
        a, f = p["available_units"], p["affiliate_units"]
        F.integer(a, "opening_wallet_out_of_range", minimum=-F.JS_SAFE_MAX, maximum=F.JS_SAFE_MAX)
        F.integer(f, "opening_affiliate_out_of_range", minimum=-F.JS_SAFE_MAX, maximum=F.JS_SAFE_MAX)
        F.require(type(p["reserved_units"]) is int and p["reserved_units"] == 0, "nonzero_or_unknown_reserved_rejected")
        amounts = {"wallet": max(a, 0), "debt": min(a, 0), "reserved": 0, "affiliate": f}
        legs = [{"owner_account_id": p["account_id"], "bucket": b, "delta_units": v,
                 "balance_after_units": v, "balance_revision": 1} for b, v in amounts.items() if v]
        delta = -a-f
        clearing = F.integer(clearing+delta, "opening_clearing_prefix_overflow")
        if delta:
            revision += 1
            legs.append({"owner_account_id": None, "bucket": "clearing", "delta_units": delta,
                         "balance_after_units": clearing, "balance_revision": revision})
        journal = None
        if a or f:
            journal = {"scope": "offline-opening", "operation_key": key_for(p), "kind": "opening",
                       "amount_units": F.integer(abs(a)+abs(f), "opening_journal_amount_overflow", minimum=1),
                       "reason": REASON, "parent_journal_id": None, "payment_reference": None,
                       "refund_reference": None, "actor_user_id": None,
                       "payer_bucket": "clearing", "payee_bucket": "wallet",
                       "entries": sorted(legs, key=lambda row: row["bucket"])}
        positions.append({"request": p, "balances": [{"bucket": b, "balance_units": amounts[b],
                           "revision": int(amounts[b] != 0)} for b in sorted(amounts)], "journal": journal})
    return {"domain": DOMAIN, "positions": positions,
            "clearing": {"balance_units": clearing, "revision": revision},
            "revenue": {"balance_units": 0, "revision": 0}}


def validate_review(F, plan, proof, evidence, review, *, evidence_sha256):
    """Bind actual independently reviewed bytes and all exported tables/users.

    This validates review coverage, not the truth of a reviewer's historical
    economic judgment. Unknown and nonzero rights are deliberately unsupported.
    """
    F.require(isinstance(evidence, dict) and type(evidence.get("format_version")) is int and evidence.get("format_version") == 1
              and evidence.get("scope") == "opening-reservation-evidence", "reservation_evidence_format_invalid")
    F.require(evidence.get("source_archive_sha256") == plan["source_archive_sha256"]
              and evidence.get("mapping_sha256") == plan["mapping_sha256"]
              and evidence.get("source_export_sha256") == proof["source_export_sha256"], "reservation_evidence_source_mismatch")
    actual_counts, expected_counts = evidence.get("exported_table_counts"), proof.get("selected_table_counts")
    for counts in (actual_counts, expected_counts):
        F.require(type(counts) is dict and all(type(name) is str for name in counts), "reservation_table_counts_invalid")
        for count in counts.values():
            F.integer(count, "reservation_table_count_not_nonnegative_integer", minimum=0)
    F.require(set(actual_counts) == set(expected_counts) and typed_equal(F, actual_counts, expected_counts),
              "reservation_evidence_table_coverage_mismatch")
    assessments = evidence.get("table_assessments")
    F.require(isinstance(assessments, list), "reservation_table_assessments_missing")
    seen = set()
    for row in assessments:
        F.require(isinstance(row, dict) and set(row) == {"table", "source_rows_sha256", "opening_holds", "review_basis"},
                  "reservation_table_assessment_invalid")
        F.require(row["table"] not in seen and row["table"] in proof["selected_table_counts"], "reservation_table_duplicate_or_unknown")
        seen.add(row["table"])
        F.require(row["source_rows_sha256"] == proof["opening_table_rows_sha256"][row["table"]], "reservation_table_rows_mismatch")
        F.require(row["opening_holds"] == "proven-none" and isinstance(row["review_basis"], str)
                  and 16 <= len(row["review_basis"]) <= 4096, "unknown_opening_rights_rejected")
    F.require(seen == set(proof["selected_table_counts"]), "reservation_table_review_incomplete")
    F.require(isinstance(review, dict) and type(review.get("format_version")) is int and review.get("format_version") == 1
              and review.get("scope") == "opening-economic-policy-review"
              and review.get("status") == "independently-reviewed-zero-reserved"
              and review.get("source_archive_sha256") == plan["source_archive_sha256"]
              and review.get("mapping_sha256") == plan["mapping_sha256"]
              and review.get("reservation_evidence_file_sha256") == evidence_sha256
              and review.get("excluded_families") == EXCLUDED
              and isinstance(review.get("reviewer"), str) and 1 <= len(review["reviewer"]) <= 128
              and isinstance(review.get("review_basis"), str) and 16 <= len(review["review_basis"]) <= 4096,
              "opening_independent_review_missing_or_mismatched")
    reconciliation = {"source_archive_sha256": plan["source_archive_sha256"], "mapping_sha256": plan["mapping_sha256"],
                      "reviewed_evidence_sha256": evidence_sha256, "positions": evidence.get("positions")}
    F.require(isinstance(reconciliation["positions"], list) and all(type(row.get("reserved_units")) is int
              and row["reserved_units"] == 0 for row in reconciliation["positions"] if isinstance(row, dict)),
              "nonzero_or_unknown_reserved_rejected")
    return reconciliation


def target_contract(F, target, contract):
    F.require(target.get("status") == "initialized-identity-only-opening-target"
              and target.get("application_or_worker_started") is False
              and target.get("exclusive_offline_resource_lease_required") is True, "opening_target_not_fresh_owned")
    conn = target.get("connection", {})
    owner, login = F.IDENTITY.connection_roles(conn)
    F.require(conn.get("login_role") == login and conn.get("set_role") == owner and login != owner,
              "opening_nologin_owner_required")
    ident = target.get("instance_identity", {})
    F.require(ident.get("role") == owner and ident.get("session_user") == login
              and ident.get("database") == conn.get("database") and ident.get("database_owner") == owner
              and ident.get("effective_role_can_login") is False, "opening_target_role_mismatch")
    F.integer(ident.get("database_oid"), "opening_database_oid_invalid", minimum=1, maximum=2**32-1)
    F.integer(ident.get("port"), "opening_database_port_invalid", minimum=1, maximum=65535)
    F.integer(conn.get("port"), "opening_database_port_invalid", minimum=1, maximum=65535)
    F.require(ident["port"] == conn.get("port"), "opening_database_port_mismatch")
    F.require(isinstance(ident.get("cluster_system_identifier"), str)
              and re.fullmatch(r"[1-9][0-9]{0,19}", ident["cluster_system_identifier"]) is not None,
              "opening_cluster_system_id_missing")
    server_version=ident.get("server_version_num")
    F.require(type(server_version) is int or (type(server_version) is str
              and re.fullmatch(r"[1-9][0-9]{5}",server_version) is not None), "opening_postgresql_version_unsupported")
    F.integer(int(server_version), "opening_postgresql_version_unsupported", minimum=180000, maximum=189999)
    for field in ("postmaster_started_at", "server_instance_nonce"):
        F.require(isinstance(ident.get(field), str) and bool(ident[field]) and "\0" not in ident[field], "opening_target_identity_incomplete")
    tc = target.get("contract", {})
    F.require(type(tc) is dict, "opening_component_contract_mismatch")
    F.integer(tc.get("identity_version"), "opening_identity_version_not_integer", minimum=1, maximum=2**31-1)
    fields=("identity_version", "identity_fingerprint_sha256", "component_fingerprints", "catalog_query_sha256", "oauth_catalog_query_sha256")
    F.require(typed_equal(F, {k:tc.get(k) for k in fields}, {k:contract[k] for k in fields}),
              "opening_component_contract_mismatch")
    F.require(F.IDENTITY.HEX.fullmatch(tc.get("catalog_fingerprint_sha256", "")) is not None, "opening_catalog_hash_invalid")
    reference = target.get("identity_import", {})
    F.require(F.IDENTITY.MIGRATION_ID.fullmatch(reference.get("migration_id", "")) is not None, "opening_identity_run_missing")
    F.require(F.IDENTITY.IDENTIFIER.fullmatch(target.get("runtime_role", "")) is not None
              and target["runtime_role"] not in (owner, login), "opening_runtime_role_invalid")
    return ident


def unsafe_runtime_sql(F, runtime):
    """Core runtime_database unsafe_role contract, with an explicit subject.

    The writer connection is the offline owner. Its current_user/session_user
    must never stand in for the runtime whose reachable authority is checked.
    """
    role=F.IDENTITY.sql_literal(runtime)
    return f"""SELECT
 EXISTS(SELECT FROM pg_roles r WHERE r.rolname<>{role} AND (
  pg_has_role({role},r.oid,'SET') OR pg_has_role({role},r.oid,'MEMBER WITH ADMIN OPTION')))
 OR EXISTS(SELECT FROM pg_roles r
  WHERE (r.rolsuper OR r.rolbypassrls OR r.rolcreaterole OR r.rolcreatedb OR r.rolreplication
   OR r.rolname IN ('pg_read_server_files','pg_write_server_files','pg_execute_server_program','pg_read_all_data','pg_write_all_data'))
  AND (r.rolname={role} OR pg_has_role({role},r.oid,'USAGE') OR pg_has_role({role},r.oid,'SET')
   OR pg_has_role({role},r.oid,'MEMBER WITH ADMIN OPTION')))
 OR has_database_privilege({role},current_database(),'CREATE,TEMP')
 OR EXISTS(SELECT FROM pg_namespace n WHERE n.nspname NOT IN ('pg_catalog','information_schema')
  AND left(n.nspname,3)<>'pg_' AND has_schema_privilege({role},n.oid,'CREATE'))
 OR EXISTS(SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE c.relkind IN ('r','p','v','f') AND n.nspname NOT IN ('pg_catalog','information_schema')
  AND left(n.nspname,3)<>'pg_' AND has_table_privilege({role},c.oid,'TRIGGER'))
 OR EXISTS(SELECT FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE p.prosecdef AND n.nspname NOT IN ('pg_catalog','information_schema')
  AND left(n.nspname,3)<>'pg_' AND (
   NOT ('search_path=pg_catalog'=ANY(coalesce(p.proconfig,'{{}}'::text[])))
   OR EXISTS(SELECT FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
    WHERE a.grantee=0 AND a.privilege_type='EXECUTE')))
 OR EXISTS(WITH owners AS (
  SELECT datdba AS owner FROM pg_database WHERE datname=current_database()
  UNION SELECT nspowner FROM pg_namespace WHERE nspname NOT IN ('pg_catalog','information_schema') AND left(nspname,3)<>'pg_'
  UNION SELECT c.relowner FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
   WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'
  UNION SELECT p.proowner FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
   WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'
  UNION SELECT t.typowner FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace
   WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'
 ) SELECT FROM owners o WHERE pg_has_role({role},o.owner,'USAGE') OR pg_has_role({role},o.owner,'SET')
  OR pg_has_role({role},o.owner,'MEMBER WITH ADMIN OPTION'))"""


def validate_sequences(F, snapshot, *, owner):
    F.require(type(snapshot) is dict and all(type(name) is str for name in snapshot), "opening_snapshot_invalid")
    for name,row in snapshot.items():
        if name != 'sequence-state':
            F.require(type(row) is dict and set(row)=={'rows','sha256'}, "opening_table_snapshot_invalid")
            F.integer(row['rows'], "opening_snapshot_count_not_nonnegative_integer", minimum=0)
            F.require(type(row['sha256']) is str and F.IDENTITY.HEX.fullmatch(row['sha256']) is not None,
                      "opening_snapshot_table_hash_invalid")
    sequences = snapshot.get("sequence-state")
    F.require(isinstance(sequences, dict) and TOUCHED <= set(sequences), "opening_sequence_snapshot_incomplete")
    for name in TOUCHED:
        row = sequences[name]
        definition = row.get("definition", {})
        for field,expected in (('seqincrement',1),('seqcache',1),('seqmin',1),('seqmax',F.BIGINT_MAX)):
            F.integer(definition.get(field), "opening_sequence_configuration_unsupported", minimum=expected, maximum=expected)
        F.require(row.get("owner") == owner and definition.get("seqincrement") == 1
                  and definition.get("seqcache") == 1 and definition.get("seqcycle") is False
                  and definition.get("seqmin") == 1 and definition.get("seqmax") == F.BIGINT_MAX
                  and type(row.get("last_value")) is int and 1 <= row["last_value"] < F.BIGINT_MAX
                  and type(row.get("is_called")) is bool, "opening_sequence_configuration_unsupported")


def snapshot_sql(destination):
    return f"""
 {destination} := '{{}}'::jsonb;
 FOR table_row IN SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE {TABLE_PREDICATE} ORDER BY n.nspname,c.relname LOOP
  EXECUTE format('SELECT jsonb_build_object(''rows'',count(*),''sha256'',encode(sha256(coalesce(string_agg(int8send(octet_length(convert_to(to_jsonb(r)::text,''UTF8''))::bigint)||convert_to(to_jsonb(r)::text,''UTF8''),''''::bytea ORDER BY to_jsonb(r)::text COLLATE "C"),''''::bytea)),''hex'')) FROM %I.%I r',table_row.nspname,table_row.relname) INTO snapshot_row;
  {destination} := {destination} || jsonb_build_object(table_row.nspname||'.'||table_row.relname,snapshot_row);
 END LOOP;
 {destination} := {destination} || jsonb_build_object('sequence-state','{{}}'::jsonb);
 FOR table_row IN SELECT n.nspname,c.relname,to_jsonb(s) AS definition,pg_get_userbyid(c.relowner) AS owner
  FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_sequence s ON s.seqrelid=c.oid
  WHERE c.relkind='S' AND n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'
  ORDER BY n.nspname,c.relname LOOP
  EXECUTE format('SELECT jsonb_build_object(''last_value'',last_value,''is_called'',is_called) FROM %I.%I',table_row.nspname,table_row.relname) INTO snapshot_row;
  {destination} := jsonb_set({destination},ARRAY['sequence-state',table_row.nspname||'.'||table_row.relname],
   snapshot_row||jsonb_build_object('definition',table_row.definition,'owner',table_row.owner),true);
 END LOOP;
"""


IDENTITY_EXTRA = "SELECT jsonb_build_object('cluster_system_identifier',system_identifier::text,'backend_pid',pg_backend_pid(),'backend_start',(SELECT backend_start::text FROM pg_stat_activity WHERE pid=pg_backend_pid())) FROM pg_control_system();"


def snapshot_query():
    return """BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
DO $lmm_snapshot$ DECLARE table_row RECORD; snapshot_row JSONB; facts JSONB; BEGIN
""" + snapshot_sql("facts") + """
 PERFORM set_config('lmm.opening_snapshot',facts::text,true);
END $lmm_snapshot$;
SELECT current_setting('lmm.opening_snapshot');
COMMIT;
"""


def raw_readback_sql():
    names = sorted(CHANGED - {"core_migration.import_runs"})
    return "SELECT jsonb_build_object(" + ",".join("'"+n+"',(SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text COLLATE \"C\"),'[]'::jsonb) FROM "+n+" r)" for n in names) + ")"


def projection_sql():
    return """SELECT jsonb_build_object('domain','lmm.offline-opening.readback.v1',
 'positions',coalesce((SELECT jsonb_agg(jsonb_build_object('request',o.request,
 'balances',(SELECT jsonb_agg(jsonb_build_object('bucket',a.bucket,'balance_units',b.balance_units,'revision',b.revision) ORDER BY a.bucket)
  FROM core_billing.ledger_accounts a JOIN core_billing.balance_state b ON b.ledger_account_id=a.id WHERE a.owner_account_id=o.account_id),
 'journal',(SELECT jsonb_build_object('scope',j.scope,'operation_key',j.operation_key,'kind',j.kind,'amount_units',j.amount_units,
 'reason',j.reason,'parent_journal_id',j.parent_journal_id,'payment_reference',j.payment_reference,'refund_reference',j.refund_reference,'actor_user_id',j.actor_user_id,
 'payer_bucket',pa.bucket,'payee_bucket',pe.bucket,'entries',
 (SELECT jsonb_agg(jsonb_build_object('owner_account_id',a.owner_account_id,'bucket',a.bucket,'delta_units',e.delta_units,
 'balance_after_units',e.balance_after_units,'balance_revision',e.balance_revision) ORDER BY a.bucket)
 FROM core_billing.ledger_entries e JOIN core_billing.ledger_accounts a ON a.id=e.ledger_account_id WHERE e.journal_id=j.id))
 FROM core_billing.ledger_journals j JOIN core_billing.ledger_accounts pa ON pa.id=j.payer_id JOIN core_billing.ledger_accounts pe ON pe.id=j.payee_id
 WHERE j.id=(o.result->>'journal_id')::bigint)) ORDER BY o.account_id) FROM core_billing.opening_imports o),'[]'::jsonb),
 'clearing',(SELECT jsonb_build_object('balance_units',b.balance_units,'revision',b.revision) FROM core_billing.ledger_accounts a JOIN core_billing.balance_state b ON b.ledger_account_id=a.id WHERE a.bucket='clearing'),
 'revenue',(SELECT jsonb_build_object('balance_units',b.balance_units,'revision',b.revision) FROM core_billing.ledger_accounts a JOIN core_billing.balance_state b ON b.ledger_account_id=a.id WHERE a.bucket='revenue'))"""


def make_approval(F, *, plan, opening, target, contract, file_hashes, review, snapshot):
    ident = target_contract(F, target, contract)
    F.require(target["identity_import"]["migration_id"] != opening["migration_id"], "opening_identity_run_collision")
    projected = expected_readback(F, opening["payloads"])
    return {"format_version": 2, "scope": SCOPE, "migration_id": opening["migration_id"], "unit": F.UNIT,
            "credits_per_usd": 500000, "reserved_policy": "reviewed-zero-only", "sequence_policy": "record-touched-advances",
            "source": {**file_hashes["source"], "source_archive_sha256": plan["source_archive_sha256"],
                       "financial_mapping_sha256": plan["mapping_sha256"],
                       "identity_mapping_sha256": plan["identity_provenance"]["identity_mapping_sha256"]},
            "target": {"target_receipt_file_sha256": file_hashes["target_receipt"],
                       **{k: ident[k] for k in ("cluster_system_identifier", "database_oid", "postmaster_started_at", "server_instance_nonce")},
                       "server_version_num": int(ident["server_version_num"]), "no_login_owner_required": True,
                       "application_or_worker_started": False, "component_catalog_contract_sha256": F.digest(contract),
                       "pre_import_facts_sha256": F.digest({k:v for k,v in snapshot.items() if k != "sequence-state"}),
                       "pre_import_sequences_sha256": F.digest(snapshot["sequence-state"]),
                       "identity_receipt_and_native_mapping_sha256": target["identity_binding_sha256"]},
            "review": {**file_hashes["review"], "full_payload_set_sha256": F.digest(opening["payloads"]),
                       "expected_complete_readback_sha256": F.digest(projected)},
            "tool_sources_sha256": source_hashes(F), "payloads": opening["payloads"], "excluded_families": EXCLUDED}


def build_import_sql(F, *, approval, target, contract, plan, identities, identity_manifest, snapshot,
                     reconcile_attempt=None, original_backend=None):
    """No connection. All dynamic inputs are JSON/text literals, never SQL names."""
    I, R = F.IDENTITY, F.require
    ident = target_contract(F, target, contract)
    F.integer(approval.get('credits_per_usd'), 'opening_approval_contract_mismatch', minimum=500000, maximum=500000)
    R(type(approval.get("format_version")) is int and approval.get("format_version") == 2 and approval.get("scope") == SCOPE
      and approval.get("unit") == F.UNIT and approval.get("credits_per_usd") == 500000
      and approval.get("reserved_policy") == "reviewed-zero-only"
      and approval.get("sequence_policy") == "record-touched-advances"
      and approval.get("excluded_families") == EXCLUDED
      and approval.get("tool_sources_sha256") == source_hashes(F), "opening_approval_contract_mismatch")
    payloads = approval["payloads"]
    R(isinstance(payloads, list) and payloads and payloads == sorted(payloads, key=lambda p:p["account_id"]), "opening_payload_order_invalid")
    run = approval["migration_id"]
    R(I.MIGRATION_ID.fullmatch(run) is not None and run != target["identity_import"]["migration_id"], "opening_migration_id_invalid")
    F.verify_plan_mapping(plan)
    R(approval["source"]["financial_mapping_sha256"] == plan["mapping_sha256"]
      and approval["source"]["source_archive_sha256"] == plan["source_archive_sha256"], "opening_plan_source_mismatch")
    R(F.digest(payloads) == approval["review"]["full_payload_set_sha256"], "opening_payload_hash_mismatch")
    expected = expected_readback(F, payloads)
    R(F.digest(expected) == approval["review"]["expected_complete_readback_sha256"], "opening_expected_readback_hash_mismatch")
    R(F.digest(snapshot["sequence-state"]) == approval["target"]["pre_import_sequences_sha256"]
      and F.digest({k:v for k,v in snapshot.items() if k != "sequence-state"}) == approval["target"]["pre_import_facts_sha256"], "opening_prestate_hash_mismatch")
    validate_sequences(F, snapshot, owner=target["connection"]["role"])
    successful_sequences=copy.deepcopy(snapshot['sequence-state'])
    calls={'core_billing.ledger_accounts_id_seq':4*len(payloads),
           'core_billing.ledger_journals_id_seq':sum(bool(p['available_units'] or p['affiliate_units']) for p in payloads)}
    for name,count in calls.items():
        state=successful_sequences[name]
        if count:
            state['last_value']=F.integer(state['last_value']+count-(not state['is_called']), 'opening_sequence_exhaustion', minimum=1)
            state['is_called']=True
    R(len(payloads) == len(identities) and {p["account_id"] for p in payloads} == set(identities.values()), "opening_payload_identity_coverage_mismatch")
    wallet = {row["account_id"]:row for row in plan["wallet_positions"]}
    keys = {"account_id","affiliate_units","available_units","mapping_sha256","migration_id","observed_at","reserved_units","source_archive_sha256","source_entity_key","source_row_sha256"}
    for p in payloads:
        R(set(p) == keys and p["migration_id"] == run and p["source_archive_sha256"] == plan["source_archive_sha256"]
          and p["mapping_sha256"] == plan["mapping_sha256"] and p["source_entity_key"] == "legacy.users/"+str(wallet[p["account_id"]]["source_user_id"])
          and p["source_row_sha256"] == wallet[p["account_id"]]["source_row_sha256"]
          and p["available_units"] == wallet[p["account_id"]]["signed_available_units"]
          and p["affiliate_units"] == wallet[p["account_id"]]["signed_affiliate_units"], "opening_payload_original_row_mismatch")
        F.integer(p["account_id"], "opening_account_invalid", minimum=1)
        F.integer(p["observed_at"], "opening_observation_invalid", minimum=-62135596800, maximum=253402300799)
    R(len({p["observed_at"] for p in payloads}) == 1, "opening_observation_mismatch")
    proof = plan["identity_provenance"]
    R(proof["expected_identity_manifest"] == identity_manifest, "opening_identity_manifest_mismatch")
    # Snapshot table hashes retain complete private identity facts; native IDs
    # and the original identity import receipt are checked independently in TX.
    L = I.sql_literal
    J = lambda value: L(F.canonical(value))+"::jsonb"
    expected_native = [{"id":u,"personal_account_id":a} for u,a in sorted(identities.items())]
    other_keys = ",".join(L(k) for k in sorted(CHANGED))
    touched_keys = ",".join(L(k) for k in sorted(TOUCHED))
    runtime, login, owner = target["runtime_role"], target["connection"]["login_role"], target["connection"]["role"]
    scope = L(run)
    backend_guard = ""
    rollback_branch = ""
    if reconcile_attempt is not None:
        R(isinstance(original_backend,dict), 'opening_original_backend_identity_incomplete')
        pid=F.integer(original_backend.get('pid'),'opening_backend_pid_invalid',minimum=1,maximum=2**31-1)
        backend_guard=f"IF EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid={pid}) THEN RAISE EXCEPTION 'opening original backend has not ended'; END IF;"
        prior=reconcile_attempt['prestate']
        rollback_branch=f"""
 IF NOT replay THEN
  IF before_state-'sequence-state' IS DISTINCT FROM {J({k:v for k,v in prior.items() if k!='sequence-state'})}
  OR (before_state->'sequence-state')-ARRAY[{touched_keys}] IS DISTINCT FROM {J({k:v for k,v in prior['sequence-state'].items() if k not in TOUCHED})}
  THEN RAISE EXCEPTION 'opening absent receipt inconsistent with original business prestate'; END IF;
  FOR table_row IN SELECT key,value FROM jsonb_each(before_state->'sequence-state') WHERE key IN ({touched_keys}) LOOP
   IF table_row.value->'definition' IS DISTINCT FROM {J(prior['sequence-state'])}->table_row.key->'definition'
   OR table_row.value->'owner' IS DISTINCT FROM {J(prior['sequence-state'])}->table_row.key->'owner'
   OR (table_row.value->>'last_value')::numeric < ({J(prior['sequence-state'])}->table_row.key->>'last_value')::numeric
   OR (({J(prior['sequence-state'])}->table_row.key->>'is_called')::boolean AND NOT (table_row.value->>'is_called')::boolean)
   THEN RAISE EXCEPTION 'opening rollback sequence effects are unexpected'; END IF;
  END LOOP;
  PERFORM set_config('lmm.opening_result',jsonb_build_object('status','confirmed-business-rollback','poststate',before_state)::text,true);
  RETURN;
 END IF;
"""
    sql = f"""BEGIN ISOLATION LEVEL SERIALIZABLE;
SET LOCAL synchronous_commit=on;
SET LOCAL standard_conforming_strings=on;
SET LOCAL search_path=pg_catalog,public;
SET LOCAL row_security=off;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='120s';
SELECT pg_advisory_xact_lock(741803121);
SELECT pg_advisory_xact_lock(hashtextextended('lmm-opening-import-v1',0));
DO @OPENING_DELIMITER@
DECLARE payload JSONB := {J(payloads)}; approved JSONB := {J(approval)};
 expected JSONB := {J(expected)}; expected_pre JSONB := {J(snapshot)};
 before_state JSONB; after_state JSONB; posted_state JSONB; actual_projection JSONB; raw_readback JSONB;
 previous core_migration.import_runs%ROWTYPE; identity_run core_migration.import_runs%ROWTYPE;
 table_row RECORD; snapshot_row JSONB; item JSONB;
 before_runs JSONB;
 replay BOOLEAN; occupied BOOLEAN; actual_catalog TEXT; result JSONB;
BEGIN
 {backend_guard}
 IF current_database()<>{L(ident['database'])} OR current_user<>{L(owner)} OR session_user<>{L(login)}
 OR (SELECT rolcanlogin FROM pg_roles WHERE rolname=current_user) IS DISTINCT FROM false
 OR (SELECT rolcanlogin FROM pg_roles WHERE rolname=session_user) IS DISTINCT FROM true
 OR (SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls FROM pg_roles WHERE rolname=current_user)
 OR (SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls FROM pg_roles WHERE rolname=session_user)
 OR NOT pg_has_role(session_user,current_user,'SET')
 OR (SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname=current_database())<>current_user
 OR (SELECT oid::bigint FROM pg_database WHERE datname=current_database())<>{ident['database_oid']}
 OR (SELECT system_identifier::text FROM pg_control_system())<>{L(ident['cluster_system_identifier'])}
 OR pg_postmaster_start_time()<>{L(ident['postmaster_started_at'])}::timestamptz
 OR current_setting('server_version_num')<>{L(str(ident['server_version_num']))}
 OR current_setting('port')::int<>{ident['port']}
 OR current_setting('lmm.migration_instance_nonce',true) IS DISTINCT FROM {L(ident['server_instance_nonce'])}
 OR inet_server_addr() IS NOT NULL THEN RAISE EXCEPTION 'opening target identity mismatch'; END IF;
 IF EXISTS(SELECT 1 FROM pg_stat_activity WHERE datid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND pid<>pg_backend_pid())
 THEN RAISE EXCEPTION 'opening target has another client'; END IF;
 FOR table_row IN SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE {TABLE_PREDICATE} ORDER BY n.nspname,c.relname LOOP
  EXECUTE format('LOCK TABLE %I.%I IN ACCESS EXCLUSIVE MODE',table_row.nspname,table_row.relname);
 END LOOP;
 SELECT ({I.catalog_hash_sql(contract)}) INTO actual_catalog;
 IF actual_catalog<>{L(target['contract']['catalog_fingerprint_sha256'])}
 OR NOT EXISTS(SELECT 1 FROM core_meta.runtime_contract WHERE singleton AND encode(catalog_fingerprint,'hex')=actual_catalog)
 OR NOT EXISTS(SELECT 1 FROM core_meta.schema_contract WHERE singleton AND version={contract['identity_version']} AND encode(fingerprint,'hex')={L(contract['identity_fingerprint_sha256'])})
 OR (SELECT jsonb_object_agg(name,encode(fingerprint,'hex')) FROM core_meta.component_contract) IS DISTINCT FROM {J(contract['component_fingerprints'])}
 THEN RAISE EXCEPTION 'opening installed contract mismatch'; END IF;
 IF ({unsafe_runtime_sql(F, runtime)})
 OR has_function_privilege({L(runtime)},'core_billing.import_opening_balance(jsonb)','EXECUTE')
 OR has_function_privilege({L(runtime)},'core_billing.import_account_opening_counters(jsonb)','EXECUTE')
 OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE c.relkind IN ('r','p','v','m','f') AND (n.nspname IN ('core_meta','core_migration') OR (n.nspname='core_billing'
 AND c.relname IN ('opening_import_runs','opening_imports','ledger_accounts','balance_state','ledger_journals','ledger_entries','ledger_operations','account_opening_counters')))
 AND (has_table_privilege({L(runtime)},c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER,REFERENCES,MAINTAIN')
 OR has_any_column_privilege({L(runtime)},c.oid,'INSERT,UPDATE,REFERENCES')))
 OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE CASE WHEN c.relkind='S' AND n.nspname='core_billing'
 THEN has_sequence_privilege({L(runtime)},c.oid,'USAGE,SELECT,UPDATE') ELSE false END) THEN RAISE EXCEPTION 'opening runtime authority is unsafe'; END IF;
 {snapshot_sql('before_state')}
 FOR table_row IN SELECT key,value FROM jsonb_each(before_state->'sequence-state') LOOP
  IF table_row.value->'definition' IS DISTINCT FROM expected_pre->'sequence-state'->table_row.key->'definition'
  OR table_row.value->'owner' IS DISTINCT FROM expected_pre->'sequence-state'->table_row.key->'owner'
  THEN RAISE EXCEPTION 'opening approved sequence definition changed'; END IF;
 END LOOP;
 IF (before_state->'sequence-state')-ARRAY[{touched_keys}] IS DISTINCT FROM (expected_pre->'sequence-state')-ARRAY[{touched_keys}]
 THEN RAISE EXCEPTION 'opening unrelated sequence differs from approval'; END IF;
 SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY migration_id),'[]'::jsonb) INTO before_runs FROM core_migration.import_runs r WHERE migration_id<>{scope};
 IF before_state-ARRAY[{other_keys}]-'sequence-state' IS DISTINCT FROM expected_pre-ARRAY[{other_keys}]-'sequence-state'
 THEN RAISE EXCEPTION 'opening unrelated or identity facts differ from review'; END IF;
 SELECT * INTO identity_run FROM core_migration.import_runs WHERE migration_id={L(target['identity_import']['migration_id'])};
 IF NOT FOUND OR identity_run.source_archive_sha256<>{L(proof['source_archive_sha256'])}
 OR identity_run.mapping_sha256<>{L(proof['identity_mapping_sha256'])} OR identity_run.manifest IS DISTINCT FROM {J(identity_manifest)}
 THEN RAISE EXCEPTION 'opening identity receipt mismatch'; END IF;
 IF to_jsonb(identity_run) IS DISTINCT FROM {J(target['identity_registry_row'])} THEN RAISE EXCEPTION 'opening original identity receipt changed'; END IF;
 IF (SELECT coalesce(jsonb_agg(jsonb_build_object('id',id,'personal_account_id',personal_account_id) ORDER BY id),'[]'::jsonb) FROM core_identity.users) IS DISTINCT FROM {J(expected_native)}
 OR (SELECT count(*) FROM core_identity.accounts)<>{len(identities)}
 OR EXISTS(SELECT 1 FROM core_identity.accounts WHERE kind<>'personal') THEN RAISE EXCEPTION 'opening native identity map mismatch'; END IF;
 SELECT * INTO previous FROM core_migration.import_runs WHERE migration_id={scope};
 replay := FOUND;
 {rollback_branch}
 IF replay THEN
  IF previous.source_archive_sha256<>{L(plan['source_archive_sha256'])} OR previous.mapping_sha256<>{L(plan['mapping_sha256'])}
  OR previous.manifest->'approval' IS DISTINCT FROM approved THEN RAISE EXCEPTION 'opening whole batch conflict'; END IF;
  IF previous.manifest->'sequence_state' IS DISTINCT FROM before_state->'sequence-state'
  THEN RAISE EXCEPTION 'opening sealed sequence state changed'; END IF;
 ELSE
  IF before_state IS DISTINCT FROM expected_pre THEN RAISE EXCEPTION 'opening complete fresh prestate mismatch'; END IF;
  IF EXISTS(SELECT 1 FROM core_migration.import_runs WHERE migration_id<>{L(target['identity_import']['migration_id'])})
  THEN RAISE EXCEPTION 'opening target belongs to another batch'; END IF;
  IF (SELECT count(*) FROM core_billing.ledger_accounts)<>2 OR (SELECT count(*) FROM core_billing.balance_state)<>2
  OR EXISTS(SELECT 1 FROM core_billing.ledger_accounts a JOIN core_billing.balance_state b ON b.ledger_account_id=a.id
    WHERE a.owner_account_id IS NOT NULL OR a.bucket NOT IN ('clearing','revenue') OR b.balance_units<>0 OR b.revision<>0)
  OR (SELECT count(*) FROM core_events.capacity)<>1
  OR NOT EXISTS(SELECT 1 FROM core_events.capacity WHERE singleton AND max_events=100000 AND max_bytes=268435456 AND pending_events=0 AND pending_bytes=0)
  OR (SELECT count(*) FROM public.routing_active_release)<>1
  OR NOT EXISTS(SELECT 1 FROM public.routing_active_release WHERE singleton AND config_version IS NULL)
  OR (SELECT count(*) FROM core_abuse.settings)<>1
  OR NOT EXISTS(SELECT 1 FROM core_abuse.settings WHERE singleton AND revision=0 AND document='{{"version":1,"policies":[]}}'::jsonb)
  OR ({I.oauth_seed_guard_sql(contract)})
  THEN RAISE EXCEPTION 'opening noncanonical seed'; END IF;
  FOR table_row IN SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
   WHERE {TABLE_PREDICATE} AND n.nspname NOT IN ('core_meta','core_migration')
   AND (n.nspname||'.'||c.relname) NOT IN ('core_identity.accounts','core_identity.users','core_identity.user_profiles','core_identity.password_logins','core_identity.developer_access','core_billing.account_policies','core_billing.ledger_accounts','core_billing.balance_state','core_events.capacity','public.routing_active_release','core_abuse.settings','core_oauth.contract') LOOP
   EXECUTE format('SELECT EXISTS(SELECT 1 FROM %I.%I)',table_row.nspname,table_row.relname) INTO occupied;
   IF occupied THEN RAISE EXCEPTION 'opening business target is not fresh'; END IF;
  END LOOP;
  IF pg_get_serial_sequence('core_billing.ledger_accounts','id')<>'core_billing.ledger_accounts_id_seq'
  OR pg_get_serial_sequence('core_billing.ledger_journals','id')<>'core_billing.ledger_journals_id_seq'
  THEN RAISE EXCEPTION 'opening sequence identity mismatch'; END IF;
  -- No nextval, opening call, sequence ALTER or business write precedes this point.
  FOR item IN SELECT value FROM jsonb_array_elements(payload) ORDER BY (value->>'account_id')::bigint LOOP
   result := core_billing.import_opening_balance(item);
   IF result->>'status'<>'posted' THEN RAISE EXCEPTION 'opening row was not posted'; END IF;
  END LOOP;
 END IF;
 SET CONSTRAINTS ALL IMMEDIATE;
 SELECT ({projection_sql()}) INTO actual_projection;
 IF actual_projection IS DISTINCT FROM expected THEN RAISE EXCEPTION 'opening complete projection mismatch'; END IF;
 IF (SELECT count(*) FROM core_billing.ledger_accounts)<>2+4*jsonb_array_length(payload)
 OR (SELECT count(*) FROM core_billing.balance_state)<>2+4*jsonb_array_length(payload)
 OR (SELECT count(*) FROM core_billing.opening_import_runs)<>1
 OR (SELECT count(*) FROM core_billing.opening_imports)<>jsonb_array_length(payload)
 OR (SELECT count(*) FROM core_billing.ledger_operations)<>jsonb_array_length(payload)
 OR (SELECT count(*) FROM core_billing.ledger_journals)<>{sum(bool(p['available_units'] or p['affiliate_units']) for p in payloads)}
 OR (SELECT count(*) FROM core_billing.ledger_entries)<>{sum(len(p['journal']['entries']) if p['journal'] else 0 for p in expected['positions'])}
 OR EXISTS(SELECT 1 FROM core_billing.ledger_entries e JOIN core_billing.ledger_accounts a ON a.id=e.ledger_account_id
  WHERE a.bucket='revenue')
 OR EXISTS(SELECT 1 FROM (SELECT e.*,row_number() OVER(PARTITION BY ledger_account_id ORDER BY balance_revision) AS required_revision,
  sum(delta_units::numeric) OVER(PARTITION BY ledger_account_id ORDER BY balance_revision ROWS UNBOUNDED PRECEDING) AS required_balance
  FROM core_billing.ledger_entries e) e WHERE e.balance_revision<>e.required_revision OR e.balance_after_units::numeric<>e.required_balance)
 OR EXISTS(SELECT 1 FROM core_billing.balance_state b LEFT JOIN core_billing.ledger_entries e ON e.ledger_account_id=b.ledger_account_id
  GROUP BY b.ledger_account_id,b.balance_units,b.revision HAVING b.balance_units::numeric<>coalesce(sum(e.delta_units::numeric),0) OR b.revision<>count(e.*))
 OR EXISTS(SELECT 1 FROM core_billing.ledger_accounts WHERE unit<>'credit_500k_usd')
 OR EXISTS(SELECT 1 FROM core_billing.ledger_journals j JOIN core_billing.ledger_accounts a ON a.id=j.payee_id
 JOIN core_billing.opening_imports o ON (o.result->>'journal_id')::bigint=j.id WHERE a.owner_account_id IS DISTINCT FROM o.account_id)
 OR EXISTS(SELECT 1 FROM core_billing.ledger_journals j LEFT JOIN core_billing.ledger_entries e ON e.journal_id=j.id GROUP BY j.id HAVING sum(e.delta_units::numeric) IS DISTINCT FROM 0)
 OR EXISTS(SELECT 1 FROM core_billing.opening_imports o WHERE o.migration_id<>{scope}
 OR o.source_entity_key<>o.request->>'source_entity_key' OR o.source_row_sha256<>o.request->>'source_row_sha256'
 OR o.account_id<>(o.request->>'account_id')::bigint OR o.result->>'status'<>'posted'
 OR o.result IS DISTINCT FROM jsonb_build_object('status','posted','journal_id',(o.result->>'journal_id')::bigint,'entries',
  (SELECT coalesce(jsonb_agg(jsonb_build_object('ledger_account_id',e.ledger_account_id,'delta_units',e.delta_units,'balance_after_units',e.balance_after_units,'balance_revision',e.balance_revision) ORDER BY e.ledger_account_id),'[]'::jsonb) FROM core_billing.ledger_entries e WHERE e.journal_id=(o.result->>'journal_id')::bigint))
 OR NOT EXISTS(SELECT 1 FROM core_billing.ledger_operations l WHERE l.scope='offline-opening'
 AND l.operation_key=encode(sha256(convert_to(jsonb_build_array(o.migration_id,o.source_entity_key)::text,'UTF8')),'hex') AND l.request=o.request AND l.result=o.result))
 OR NOT EXISTS(SELECT 1 FROM core_billing.opening_import_runs WHERE migration_id={scope}
  AND source_archive_sha256={L(plan['source_archive_sha256'])} AND mapping_sha256={L(plan['mapping_sha256'])} AND observed_at={payloads[0]['observed_at']})
 THEN RAISE EXCEPTION 'opening full receipts or conservation mismatch'; END IF;
 SELECT ({raw_readback_sql()}) INTO raw_readback;
 IF replay THEN
  IF previous.manifest->'raw_readback' IS DISTINCT FROM raw_readback OR previous.manifest->'projection' IS DISTINCT FROM actual_projection
  OR previous.manifest->>'raw_readback_pg_jsonb_sha256' IS DISTINCT FROM encode(sha256(convert_to(raw_readback::text,'UTF8')),'hex')
  THEN RAISE EXCEPTION 'opening replay raw readback mismatch'; END IF;
 ELSE
  {snapshot_sql('posted_state')}
  INSERT INTO core_migration.import_runs(migration_id,source_archive_sha256,mapping_sha256,manifest)
  VALUES({scope},{L(plan['source_archive_sha256'])},{L(plan['mapping_sha256'])},jsonb_build_object('scope','financial-opening-only','approval',approved,
   'projection',actual_projection,'raw_readback',raw_readback,'sequence_state',posted_state->'sequence-state','raw_readback_pg_jsonb_sha256',encode(sha256(convert_to(raw_readback::text,'UTF8')),'hex')));
 END IF;
 {snapshot_sql('after_state')}
 IF after_state->'sequence-state' IS DISTINCT FROM {J(successful_sequences)}
 THEN RAISE EXCEPTION 'opening exact successful sequence consumption mismatch'; END IF;
 IF before_state-ARRAY[{other_keys}]-'sequence-state' IS DISTINCT FROM after_state-ARRAY[{other_keys}]-'sequence-state'
 OR (before_state->'sequence-state')-ARRAY[{touched_keys}] IS DISTINCT FROM (after_state->'sequence-state')-ARRAY[{touched_keys}]
 OR (replay AND before_state IS DISTINCT FROM after_state)
 OR before_runs IS DISTINCT FROM (SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY migration_id),'[]'::jsonb) FROM core_migration.import_runs r WHERE migration_id<>{scope})
 OR (SELECT count(*) FROM core_migration.import_runs)<>2 THEN RAISE EXCEPTION 'opening unrelated facts or replay changed'; END IF;
 FOR table_row IN SELECT key,value FROM jsonb_each(after_state->'sequence-state') LOOP
  IF table_row.value->'definition' IS DISTINCT FROM before_state->'sequence-state'->table_row.key->'definition'
  OR table_row.value->'owner' IS DISTINCT FROM before_state->'sequence-state'->table_row.key->'owner'
  OR (table_row.value->>'last_value')::numeric < (before_state->'sequence-state'->table_row.key->>'last_value')::numeric
  THEN RAISE EXCEPTION 'opening sequence definition or monotonic state mismatch'; END IF;
 END LOOP;
 IF ({I.catalog_hash_sql(contract)})<>{L(target['contract']['catalog_fingerprint_sha256'])}
 OR EXISTS(SELECT 1 FROM pg_stat_activity WHERE datid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND pid<>pg_backend_pid())
 THEN RAISE EXCEPTION 'opening final catalog or client mismatch'; END IF;
 PERFORM set_config('lmm.opening_result',jsonb_build_object('status',CASE WHEN replay THEN 'replay-noop' ELSE 'applied' END,'projection',actual_projection,'raw_readback',raw_readback,'poststate',after_state)::text,true);
END;
@OPENING_DELIMITER@;
SELECT current_setting('lmm.opening_result');
COMMIT;
"""
    # Never replace a source-containing occurrence of the structural marker.
    delimiter = "$lmm_opening_"+hashlib.sha256(sql.encode()).hexdigest()[:24]+"$"
    R(delimiter not in sql, "opening_sql_delimiter_collision")
    sql = sql.replace("DO @OPENING_DELIMITER@\n", "DO "+delimiter+"\n", 1)
    sql=sql.removesuffix("@OPENING_DELIMITER@;\nSELECT current_setting('lmm.opening_result');\nCOMMIT;\n")+delimiter+";\nSELECT current_setting('lmm.opening_result');\nCOMMIT;\n"
    if reconcile_attempt is not None:
        # Session lock in autocommit is deliberately before BEGIN's first
        # snapshot and stays held until this one psql connection closes.
        sql="SELECT pg_advisory_lock(741803121);\n"+sql.replace('BEGIN ISOLATION LEVEL SERIALIZABLE;','BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY;',1)
        sql=sql.replace(' IN ACCESS EXCLUSIVE MODE',' IN ACCESS SHARE MODE')
    return sql


class TargetLease:
    """Resource-owner supplied, stable per-target inode; never an output lock."""
    def __init__(self, F, target):
        self.F, self.target, self.fd = F, target, None

    def __enter__(self):
        F = self.F
        path = F.IDENTITY.private_backup_path(Path(self.target.get("lease_file", "")))
        info = path.stat()
        F.require(stat.S_ISREG(info.st_mode) and info.st_nlink == 1, "opening_target_lease_invalid")
        expected = self.target.get("lease_identity", {})
        F.require(expected == {"device": info.st_dev, "inode": info.st_ino}, "opening_target_lease_identity_mismatch")
        self.fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
        opened = os.fstat(self.fd)
        if (opened.st_dev,opened.st_ino,opened.st_mode,opened.st_uid,opened.st_nlink) != (info.st_dev,info.st_ino,info.st_mode,info.st_uid,info.st_nlink):
            os.close(self.fd); self.fd = None
            raise F.MappingError("opening_target_lease_changed")
        try:
            fcntl.flock(self.fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            os.close(self.fd); self.fd = None
            raise F.MappingError("target_lease_busy") from None
        return self

    def __exit__(self, *_):
        if self.fd is not None:
            fcntl.flock(self.fd, fcntl.LOCK_UN)
            os.close(self.fd)
            self.fd = None


def read_json(F, path):
    owned = F.IDENTITY.private_backup_path(Path(path))
    F.require(owned.stat().st_size <= 256*1024*1024, "opening_input_too_large")
    value = F.parse_json(owned.read_text())
    F.require(isinstance(value, dict), "opening_input_object_required")
    return owned, value


def target_identity_sql(F):
    return "BEGIN READ ONLY;\n"+F.IDENTITY.IDENTITY_SQL+"\n"+IDENTITY_EXTRA+"\nCOMMIT;\n"


def inspect_target(F, target, output, label):
    conn, expected = target["connection"], target["instance_identity"]
    raw = F.IDENTITY.psql(conn, target_identity_sql(F), readonly=True, private_dir=output, label=label+"-identity")
    values = [F.parse_json(line) for line in raw.splitlines() if line.strip()]
    F.require(len(values) == 2, "opening_target_identity_framing_invalid")
    F.IDENTITY.verify_instance(values[0], conn, expected, source=False)
    F.require(values[1].get("cluster_system_identifier") == expected["cluster_system_identifier"], "opening_cluster_system_id_mismatch")
    run = F.IDENTITY.sql_literal(target["identity_import"]["migration_id"])
    raw_binding = F.IDENTITY.psql(conn, "BEGIN READ ONLY; SELECT jsonb_build_object('identity_run',"
        "(SELECT to_jsonb(r) FROM core_migration.import_runs r WHERE migration_id="+run+"),'native_users',"
        "(SELECT coalesce(jsonb_agg(jsonb_build_object('id',id,'personal_account_id',personal_account_id) ORDER BY id),'[]'::jsonb) FROM core_identity.users)); COMMIT;",
        readonly=True, private_dir=output, label=label+"-binding")
    binding = F.parse_json(raw_binding)
    F.require(typed_equal(F, binding.get("identity_run"), target.get("identity_registry_row"))
              and F.digest(binding) == target.get("identity_binding_sha256"), "opening_target_identity_binding_changed")
    return binding


def attempt_session_sql(F, target, backend_path):
    """Capture the same connection's real start before restoring its owner.

    PGOPTIONS initially sets the NOLOGIN owner, which hides LOGIN backend stats.
    SET ROLE NONE explicitly returns to session_user; RESET ROLE would restore
    that connection-time owner. psql quotes the captured value as an SQL literal.
    """
    I, R = F.IDENTITY, F.require
    owner,login=I.connection_roles(target['connection'])
    R(target['connection'].get('set_role')==owner and login!=owner, 'opening_nologin_owner_required')
    R("'" not in str(backend_path) and "\\" not in str(backend_path) and "\n" not in str(backend_path), "opening_output_path_unrepresentable")
    L=I.sql_literal
    return f"""\\unset lmm_opening_login_backend_start
SET ROLE NONE;
DO $lmm_opening_login_backend$
BEGIN
 IF current_user<>session_user OR current_user<>{L(login)} THEN
  RAISE EXCEPTION 'opening original backend login identity mismatch';
 END IF;
 IF (SELECT count(*)<>1 OR count(backend_start)<>1 FROM pg_stat_activity WHERE pid=pg_backend_pid()) THEN
  RAISE EXCEPTION 'opening original backend start is unavailable';
 END IF;
END $lmm_opening_login_backend$;
SELECT backend_start::text AS lmm_opening_login_backend_start FROM pg_stat_activity WHERE pid=pg_backend_pid()
\\gset
SET ROLE {L(owner)};
DO $lmm_opening_restored_owner$
BEGIN
 IF current_user<>{L(owner)} OR session_user<>{L(login)}
 OR (SELECT rolcanlogin FROM pg_roles WHERE rolname=current_user) IS DISTINCT FROM false THEN
  RAISE EXCEPTION 'opening original backend owner was not restored';
 END IF;
END $lmm_opening_restored_owner$;
\\o '{backend_path}'
SELECT jsonb_build_object('pid',pg_backend_pid(),'backend_start',:'lmm_opening_login_backend_start',
 'database_oid',(SELECT oid::bigint FROM pg_database WHERE datname=current_database()),
 'system_id',(SELECT system_identifier::text FROM pg_control_system()));
\\o
\\unset lmm_opening_login_backend_start
"""


def run(F, args, *, output, source, tables, proof, plan, planner_manifest, identities,
        identity_manifest, source_path, map_path, identity_manifest_path):
    """Concrete preview/apply/reconcile entry, with a stable external target lease."""
    I, R = F.IDENTITY, F.require
    R(args.reuse_export_proof is not None, "opening_requires_frozen_export_no_source_queries")
    target_path, target = read_json(F, args.target_receipt)
    evidence_path, evidence = read_json(F, args.reservation_evidence)
    review_path, review = read_json(F, args.reservation_review)
    contract = I.current_contract()
    target_contract(F, target, contract)
    proof = {**proof, "opening_table_rows_sha256": {name:F.digest(sorted(rows, key=F.canonical)) for name,rows in tables.items()}}
    reconciliation = validate_review(F, plan, proof, evidence, review, evidence_sha256=I.sha256_file(evidence_path))
    opening = F.build_opening_payloads(plan, migration_id=args.migration_id, observation=F.source_observation(source), reservation_reconciliation=reconciliation)
    R(target.get("identity_import", {}).get("source_archive_sha256") == plan["source_archive_sha256"]
      and target["identity_import"].get("mapping_sha256") == plan["identity_provenance"]["identity_mapping_sha256"], "opening_target_identity_reference_mismatch")
    snapshot = target.get("pre_import_snapshot", {})
    validate_sequences(F, snapshot, owner=target["connection"]["role"])
    file_hashes = {"source": {"source_receipt_file_sha256": I.sha256_file(source_path),
                    "source_export_sha256": proof["source_export_sha256"],
                    "source_export_proof_file_sha256": I.sha256_file(I.private_backup_path(args.reuse_export_proof)),
                    "financial_plan_file_sha256": I.sha256_file(output/"financial-plan.private.json"),
                    "planner_manifest_file_sha256": I.sha256_file(output/"manifest.private.json"),
                    "identity_map_file_sha256": I.sha256_file(map_path), "identity_manifest_file_sha256": I.sha256_file(identity_manifest_path)},
                   "target_receipt": I.sha256_file(target_path),
                   "review": {"reservation_evidence_file_sha256": I.sha256_file(evidence_path),
                    "reservation_review_manifest_file_sha256": I.sha256_file(review_path),
                    "opening_economic_policy_review_file_sha256": I.sha256_file(review_path)}}
    candidate = make_approval(F, plan=plan, opening=opening, target=target, contract=contract,
                              file_hashes=file_hashes, review=review, snapshot=snapshot)
    paths = [source_path,map_path,identity_manifest_path,target_path,evidence_path,review_path,I.private_backup_path(args.reuse_export_proof),output/"financial-plan.private.json",output/"manifest.private.json",
             I.private_backup_path(Path(source["archive"]["path"])),
             I.private_backup_path(args.reuse_export_proof.with_name("source-financial-export.private.jsonl")),
             *[I.private_backup_path(Path(p)) for p in source.get("private_metadata_files",[])]]
    frozen_files = {str(p):I.sha256_file(p) for p in paths}
    approval = candidate
    if args.apply_opening or args.reconcile_opening:
        approval_path, approval = read_json(F, args.opening_approval)
        R(typed_equal(F, approval, candidate), "opening_approved_material_mismatch")
        frozen_files[str(approval_path)] = I.sha256_file(approval_path)
    I.write_private(output, "opening-approval-candidate.private.json", F.canonical(candidate)+"\n")
    I.write_private(output, "opening-payloads.private.json", F.canonical(opening)+"\n")
    with TargetLease(F, target):
        binding = inspect_target(F, target, output, "opening-target")
        F.verify_target_identity_binding(plan, identities, import_run=binding["identity_run"], native_users=binding["native_users"])
        source_instance = proof.get("source_instance", {})
        R((str(source_instance.get("database_oid")),source_instance.get("postmaster_started_at")) !=
          (str(target["instance_identity"]["database_oid"]),target["instance_identity"]["postmaster_started_at"]), "opening_source_target_same_database")
        sql = build_import_sql(F, approval=approval, target=target, contract=contract, plan=plan,
                               identities=identities, identity_manifest=identity_manifest, snapshot=snapshot)
        I.write_private(output, "opening-import.private.sql", sql)
        I.write_private(output, "opening-input-files.private.json", F.canonical(frozen_files)+"\n")
        if args.reconcile_opening:
            R(args.opening_attempt is not None, "opening_original_attempt_required")
            return reconcile(F, args, target=target, approval=approval, snapshot=snapshot, output=output)
        actual_raw = I.psql(target["connection"], snapshot_query(), readonly=True, private_dir=output, label="opening-prestate")
        actual = F.parse_json(actual_raw)
        I.write_private(output, "opening-actual-prestate.private.json", F.canonical(actual)+"\n")
        if not args.apply_opening:
            R(typed_equal(F, actual, snapshot), "opening_preview_fresh_prestate_mismatch")
            return {"status":"opening-preview-review-required", "target_writes":0, "real_financial_import_complete":False}
        # Freeze bytes and session identity BEFORE the write SQL can begin.
        R(I.current_contract() == contract and source_hashes(F) == approval["tool_sources_sha256"], "opening_sources_changed_before_attempt")
        R(all(I.sha256_file(I.private_backup_path(Path(p))) == h for p,h in frozen_files.items()), "opening_input_changed_before_attempt")
        backend_path = I.write_private(output, "opening-backend.private.json", "")
        session_sql = attempt_session_sql(F,target,backend_path)
        I.write_private(output,'opening-session-prefix.private.sql',session_sql)
        attempted = {"format_version":1,"scope":SCOPE,"status":"attempted-outcome-unknown","migration_id":approval["migration_id"],
                     "approval_sha256":F.digest(approval),"sql_sha256":hashlib.sha256(sql.encode()).hexdigest(),
                     "session_sql_sha256":hashlib.sha256(session_sql.encode()).hexdigest(),
                     "backend_file":str(backend_path),"target_receipt_file_sha256":file_hashes["target_receipt"],
                     "prestate":actual,"automatic_retry":False}
        I.write_private(output, "opening-attempt.private.json", F.canonical(attempted)+"\n")
        # File fsync is provided by write_private; persist directory entries
        # (including the new output directory) before execution may start.
        for directory in (output,output.parent):
            fd=os.open(directory,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
            try: os.fsync(fd)
            finally: os.close(fd)
        try:
            raw = I.psql(target["connection"], session_sql+sql, readonly=False, private_dir=output, label="opening-apply")
            I.write_private(output, "opening-apply.stdout.private.txt", raw)
            rows = [F.parse_json(line) for line in raw.splitlines() if line.strip().startswith("{")]
            R(len(rows) == 1 and rows[0].get("status") in ("applied","replay-noop"), "opening_apply_ack_invalid")
            R(typed_equal(F, rows[0].get("projection"), expected_readback(F, approval["payloads"])), "opening_apply_projection_invalid")
            after = F.parse_json(I.psql(target["connection"], snapshot_query(), readonly=True, private_dir=output, label="opening-poststate"))
            R(typed_equal(F, after, rows[0].get('poststate')), 'opening_postcommit_state_changed_or_unconfirmed')
            I.write_private(output, "opening-actual-poststate.private.json", F.canonical(after)+"\n")
            I.write_private(output, "opening-success.private.json", F.canonical({"status":rows[0]["status"],"approval_sha256":F.digest(approval),"prestate":actual,"poststate":after,"raw_ack_sha256":hashlib.sha256(raw.encode()).hexdigest()})+"\n")
            return {"status":"opening-"+rows[0]["status"],"real_financial_import_complete":False}
        except (F.MappingError,OSError,ValueError,KeyError,TypeError) as error:
            I.write_private(output, "opening-outcome-unknown.private.json", F.canonical({"status":"opening-outcome-unknown","automatic_retry":False,"error_code":str(error) if isinstance(error,F.MappingError) else type(error).__name__})+"\n")
            raise F.MappingError("opening_outcome_unknown_reconcile_original_attempt") from None


def reconcile(F, args, *, target, approval, snapshot, output):
    """Read-only fresh snapshot after the original backend has ended and fence.

    It never retries the writer. Boot changes or missing/hidden original session
    evidence remain UNKNOWN and need an owner reviewed recovery chain.
    """
    I, R = F.IDENTITY, F.require
    attempt_path, attempt = read_json(F, args.opening_attempt)
    R(type(attempt.get("format_version")) is int and attempt.get("format_version") == 1
      and attempt.get("scope") == SCOPE and attempt.get("migration_id") == approval["migration_id"]
      and attempt.get("approval_sha256") == F.digest(approval), "opening_attempt_material_mismatch")
    original_sql=I.private_backup_path(attempt_path.with_name('opening-import.private.sql'))
    original_session=I.private_backup_path(attempt_path.with_name('opening-session-prefix.private.sql'))
    R(I.sha256_file(original_sql)==attempt.get('sql_sha256')
      and I.sha256_file(original_session)==attempt.get('session_sql_sha256')
      and attempt.get('target_receipt_file_sha256')==approval['target']['target_receipt_file_sha256'], 'opening_original_attempt_bytes_mismatch')
    _, backend = read_json(F, attempt.get("backend_file", ""))
    pid = F.integer(backend.get("pid"), "opening_backend_pid_invalid", minimum=1, maximum=2**31-1)
    R(backend.get("system_id") == target["instance_identity"]["cluster_system_identifier"]
      and backend.get("database_oid") == target["instance_identity"]["database_oid"]
      and isinstance(backend.get("backend_start"),str), "opening_original_backend_identity_incomplete")
    normal = build_import_sql(F, approval=approval,target=target,contract=I.current_contract(),plan=args._opening_plan,
                              identities=args._opening_identities,identity_manifest=args._opening_identity_manifest,snapshot=snapshot)
    R(hashlib.sha256(normal.encode()).hexdigest()==attempt['sql_sha256'], 'opening_original_sql_contract_changed')
    sql = build_import_sql(F, approval=approval,target=target,contract=I.current_contract(),plan=args._opening_plan,
                           identities=args._opening_identities,identity_manifest=args._opening_identity_manifest,snapshot=snapshot,
                           reconcile_attempt=attempt,original_backend=backend)
    I.write_private(output,'opening-reconcile.private.sql',sql)
    raw=I.psql(target['connection'],sql,readonly=True,private_dir=output,label='opening-reconcile-fenced-readback')
    I.write_private(output,'opening-reconcile.stdout.private.txt',raw)
    rows=[F.parse_json(line) for line in raw.splitlines() if line.strip().startswith('{')]
    R(len(rows)==1 and rows[0].get('status') in ('replay-noop','confirmed-business-rollback'), 'opening_reconciliation_unconfirmed')
    current=rows[0]['poststate']
    I.write_private(output,'opening-reconcile-state.private.json',F.canonical(current)+'\n')
    result={'status':'opening-confirmed-committed' if rows[0]['status']=='replay-noop' else 'opening-confirmed-business-rollback',
            'automatic_retry':False,'real_financial_import_complete':False}
    if rows[0]['status']=='confirmed-business-rollback':
        result['new_reviewed_sequence_envelope_required']=True
    I.write_private(output,'opening-reconciliation.private.json',F.canonical({**result,
        'original_attempt_file_sha256':I.sha256_file(attempt_path),
        'raw_fenced_readback_sha256':hashlib.sha256(raw.encode()).hexdigest(),
        'touched_sequences':{n:current['sequence-state'][n] for n in sorted(TOUCHED)}})+'\n')
    return result
