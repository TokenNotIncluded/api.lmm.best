#!/usr/bin/env python3
"""Offline opening writer. It does not activate keys, subscriptions or runtime.

The planner stays SELECT-only. This module consumes a separately reviewed opening
and an exact target snapshot. SQL is private, deterministic and approved by hash.
A failed write process means UNKNOWN, not rollback. Resolve the original attempt
before deciding whether the same material may be submitted again.
"""
from __future__ import annotations

import argparse
from datetime import datetime
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import uuid

import legacy_financial_preflight as PREFLIGHT

_SPEC = importlib.util.spec_from_file_location(
    "legacy_financial_planner", Path(__file__).with_name("migrate-legacy-financial.py")
)
PLANNER = importlib.util.module_from_spec(_SPEC)
_SPEC.loader.exec_module(PLANNER)
IDENTITY = PLANNER.IDENTITY
Error = IDENTITY.MappingError
require = IDENTITY.require
SCOPE = "financial-opening-only-v1"
SEED = "identity-bound-zero-ledger-v1"
FENCE = 741803121  # Shared with the offline identity importer.
TABLE_FILTER = "n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'"
CHANGED_TABLES = (
    "core_billing.ledger_accounts", "core_billing.balance_state",
    "core_billing.ledger_operations", "core_billing.ledger_journals",
    "core_billing.ledger_entries", "core_billing.opening_import_runs",
    "core_billing.opening_imports", "core_billing.account_opening_counters",
)
SOURCE_FILES = frozenset({"archive", "source_receipt", "financial_plan", "source_export",
    "source_export_proof", "identity_map", "identity_manifest", "source_review", "reservations"})
CHANGED_SEQUENCES = (
    "core_billing.ledger_accounts_id_seq", "core_billing.ledger_journals_id_seq",
)


def canonical(value) -> str:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)


def sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def digest(value) -> str:
    # Reuse the planner's type-tagged digest, including its integer/bool split.
    return PLANNER.digest(value)


def checked_hash(value, code="invalid_digest") -> str:
    require(type(value) is str and IDENTITY.HEX.fullmatch(value) is not None, code)
    return value


def literal(value) -> str:
    value = value if type(value) is str else canonical(value)
    require("\0" not in value, "nul_in_sql_material")
    return IDENTITY.sql_literal(value)


def relation(value: str) -> str:
    require(type(value) is str and re.fullmatch(r"[a-z_][a-z_0-9]*\.[a-z_][a-z_0-9]*", value) is not None,
            "invalid_relation_name")
    return ".".join('"' + part + '"' for part in value.split("."))


def inventory_sql() -> str:
    return f"""SELECT jsonb_build_object(
 'tables',coalesce(jsonb_agg(n.nspname||'.'||c.relname ORDER BY n.nspname,c.relname)
  FILTER(WHERE c.relkind IN ('r','p')),'[]'::jsonb),
 'sequences',coalesce(jsonb_agg(n.nspname||'.'||c.relname ORDER BY n.nspname,c.relname)
  FILTER(WHERE c.relkind='S'),'[]'::jsonb))
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE {TABLE_FILTER}"""


def validate_inventory(inventory: dict) -> None:
    require(type(inventory) is dict and set(inventory) == {"tables", "sequences"}, "invalid_inventory")
    for kind, values in inventory.items():
        require(type(values) is list and values == sorted(set(values)), "invalid_inventory")
        for name in values:
            relation(name)
    require(set(CHANGED_TABLES) | {"core_migration.import_runs", "core_identity.users"}
            <= set(inventory["tables"]), "incomplete_inventory")
    require(set(CHANGED_SEQUENCES) <= set(inventory["sequences"]), "incomplete_inventory")


def state_sql(inventory: dict, migration_id: str) -> str:
    """Hash all application rows and sequence state, excluding only this receipt.

    Fixed-size row hashes preserve duplicates. Sorting uses UTF-8 bytes, not the
    database collation. Sequence parameters are included, not only last_value.
    """
    validate_inventory(inventory)
    tables = []
    for name in inventory["tables"]:
        where = f" WHERE t.migration_id<>{literal(migration_id)}" if name == "core_migration.import_runs" else ""
        tables.append(f"""SELECT {literal(name)} AS name,jsonb_build_object('rows',count(*),'sha256',
 encode(sha256(coalesce(string_agg(sha256(convert_to(to_jsonb(t)::text,'UTF8')),
 ''::bytea ORDER BY convert_to(to_jsonb(t)::text,'UTF8')),''::bytea)),'hex')) AS value
 FROM {relation(name)} t{where}""")
    sequences = []
    for name in inventory["sequences"]:
        sequences.append(f"""SELECT {literal(name)} AS name,jsonb_build_object(
 'last_value',s.last_value,'is_called',s.is_called,'start',p.seqstart,'increment',p.seqincrement,
 'min',p.seqmin,'max',p.seqmax,'cache',p.seqcache,'cycle',p.seqcycle) AS value
 FROM {relation(name)} s CROSS JOIN pg_sequence p WHERE p.seqrelid={literal(name)}::regclass""")
    return "SELECT jsonb_build_object('tables',(SELECT jsonb_object_agg(name,value) FROM (" + \
        " UNION ALL ".join(tables) + ") t),'sequences',(SELECT jsonb_object_agg(name,value) FROM (" + \
        " UNION ALL ".join(sequences) + ") s),'inventory',(" + inventory_sql() + "))"


def instance_sql() -> str:
    base = IDENTITY.IDENTITY_SQL.removesuffix(";").removeprefix("SELECT ")
    # pg_control_system requires an explicit offline grant to the NOLOGIN owner.
    return f"""SELECT ({base})::jsonb - 'read_only' || jsonb_build_object(
 'system_identifier',(SELECT system_identifier::text FROM pg_control_system()),
 'listen_addresses',current_setting('listen_addresses'))"""


def ownership_sql() -> str:
    return f"""SELECT encode(sha256(convert_to(coalesce(jsonb_agg(v ORDER BY v::text),'[]'::jsonb)::text,'UTF8')),'hex')
 FROM (
 SELECT jsonb_build_array('schema',n.nspname,pg_get_userbyid(n.nspowner),n.nspacl::text) v
 FROM pg_namespace n WHERE {TABLE_FILTER}
 UNION ALL SELECT jsonb_build_array('relation',n.nspname,c.relname,pg_get_userbyid(c.relowner),c.relacl::text)
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE {TABLE_FILTER}
 UNION ALL SELECT jsonb_build_array('function',n.nspname,p.proname,pg_get_function_identity_arguments(p.oid),
 pg_get_userbyid(p.proowner),p.proacl::text,p.prosecdef)
 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE {TABLE_FILTER}
 ) permissions"""


def snapshot_sql(inventory: dict, migration_id: str) -> str:
    contract = IDENTITY.current_contract()
    return "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SET LOCAL timezone='UTC';\n" + \
        "SELECT jsonb_build_object('instance',(" + instance_sql() + \
        "),'catalog_fingerprint_sha256',(" + IDENTITY.catalog_hash_sql(contract) + \
        "),'ownership_sha256',(" + ownership_sql() + \
        "),'component_fingerprints',(SELECT jsonb_object_agg(name,encode(fingerprint,'hex')) FROM core_meta.component_contract)," + \
        "'state',(" + state_sql(inventory, migration_id) + ")," + \
        "'native_users',(SELECT coalesce(jsonb_agg(jsonb_build_object('id',id,'personal_account_id',personal_account_id) ORDER BY id),'[]'::jsonb) FROM core_identity.users)," + \
        "'import_runs',(SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY migration_id),'[]'::jsonb) FROM core_migration.import_runs r));\nCOMMIT;\n"


def source_hashes() -> dict:
    return {**PLANNER.tool_source_hashes(), "financial_writer_sha256": IDENTITY.sha256_file(Path(__file__))}


def prepare_material(plan: dict, *, migration_id: str, identities: dict,
                     observation: dict, reservations: dict, review: dict,
                     snapshot: dict, target_contract: dict, runtime_roles: list[str], source_files: dict) -> dict:
    """Bind existing planner adapters to a reviewed source and observed target.

    An opening-only source review never establishes missing Key, subscription,
    refund or escrow semantics. Those remain disabled and require separate work.
    """
    PLANNER.verify_plan_mapping(plan)
    require(type(migration_id) is str and IDENTITY.MIGRATION_ID.fullmatch(migration_id) is not None,
            "migration_id_invalid")
    require(type(review) is dict and set(review) == {
        "format_version", "scope", "decision", "source_archive_sha256", "mapping_sha256",
        "identity_map_sha256", "credits_per_usd", "evidence_sha256", "other_rights_policy",
    }, "source_review_shape_invalid")
    require(type(review["format_version"]) is int and review["format_version"] == 1
            and review["scope"] == SCOPE and review["decision"] == "approved-offline-opening"
            and type(review["credits_per_usd"]) is int and review["credits_per_usd"] == 500000
            and review["other_rights_policy"] == "remain-disabled-pending-separate-import",
            "source_unit_or_review_unproven")
    checked_hash(review["evidence_sha256"], "source_review_evidence_missing")
    for key in ("source_archive_sha256", "mapping_sha256", "identity_map_sha256"):
        require(review[key] == plan.get(key), "source_review_binding_mismatch")
    require(digest(identities) == plan.get("identity_map_sha256"), "identity_map_mismatch")
    runs = [row for row in snapshot["import_runs"]
            if row.get("manifest", {}).get("scope") == "identity-only"]
    require(len(runs) == 1, "target_identity_import_missing_or_ambiguous")
    PLANNER.verify_target_identity_binding(plan, identities, import_run=runs[0], native_users=snapshot["native_users"])
    require(all(row["source_archive_sha256"] == plan["source_archive_sha256"] for row in snapshot["import_runs"]),
            "target_contains_other_source")
    contract = IDENTITY.current_contract()
    require(all(target_contract.get(key) == contract[key] for key in
                ("identity_version", "identity_fingerprint_sha256", "component_fingerprints", "catalog_query_sha256")),
            "target_contract_mismatch")
    require(snapshot["component_fingerprints"] == contract["component_fingerprints"]
            and snapshot["catalog_fingerprint_sha256"] == target_contract.get("catalog_fingerprint_sha256"),
            "target_catalog_mismatch")
    opening = PLANNER.build_opening_payloads(plan, migration_id=migration_id,
        observation=observation, reservation_reconciliation=reservations)
    counters = PLANNER.build_opening_counter_payloads(plan, opening)
    material = {
        "format_version": 1, "scope": SCOPE, "seed_contract": SEED,
        "migration_id": migration_id, "source_archive_sha256": plan["source_archive_sha256"],
        "mapping_sha256": plan["mapping_sha256"], "identity_map_sha256": plan["identity_map_sha256"],
        "source_review": review, "identity_import": runs[0],
        "native_users": snapshot["native_users"], "observation": observation,
        "source_plan": plan, "source_files": source_files,
        "reservation_evidence_sha256": opening["reservation_review_evidence_sha256"],
        "opening": opening["payloads"], "counters": counters["payloads"],
        "target": {key: snapshot[key] for key in ("instance", "catalog_fingerprint_sha256", "ownership_sha256", "component_fingerprints")},
        "before_state": snapshot["state"], "runtime_roles": runtime_roles,
        "tool_source_hashes": source_hashes(),
    }
    validate_material(material)
    return json.loads(canonical(material))


def validate_material(material: dict) -> None:
    require(type(material) is dict and type(material.get("format_version")) is int
            and material["format_version"] == 1 and material.get("scope") == SCOPE
            and material.get("seed_contract") == SEED, "material_contract_invalid")
    require(type(material.get("migration_id")) is str
            and IDENTITY.MIGRATION_ID.fullmatch(material["migration_id"]) is not None, "migration_id_invalid")
    for key in ("source_archive_sha256", "mapping_sha256", "identity_map_sha256", "reservation_evidence_sha256"):
        checked_hash(material.get(key))
    require(material.get("tool_source_hashes") == source_hashes(), "financial_tool_source_changed")
    files = material.get("source_files")
    require(type(files) is dict and set(files) == SOURCE_FILES, "source_files_incomplete")
    for item in files.values():
        require(type(item) is dict and set(item) == {"path", "sha256"}
                and type(item["path"]) is str and Path(item["path"]).is_absolute(), "source_file_shape_invalid")
        checked_hash(item["sha256"])
    require(files["archive"]["sha256"] == material["source_archive_sha256"], "source_archive_binding_mismatch")
    PLANNER.verify_plan_mapping(material["source_plan"])
    require(all(material["source_plan"].get(key) == material[key] for key in
                ("source_archive_sha256", "mapping_sha256", "identity_map_sha256")),
            "material_source_plan_mismatch")
    target = material["target"]
    expected = target["instance"]
    require(type(expected.get("database_oid")) in (str, int) and str(expected["database_oid"]).isascii()
            and str(expected["database_oid"]).isdigit() and int(expected["database_oid"]) > 0, "target_oid_invalid")
    for key in ("database", "role", "database_owner", "session_user"):
        require(type(expected.get(key)) is str and IDENTITY.IDENTIFIER.fullmatch(expected[key]) is not None,
                "target_role_invalid")
    require(expected["database_owner"] == expected["role"] != expected["session_user"]
            and expected.get("effective_role_can_login") is False
            and expected.get("tcp_connection") is False and expected.get("listen_addresses") == "",
            "target_not_offline_owner")
    require(type(expected.get("port")) is int and 0 < expected["port"] <= 65535
            and all(type(expected.get(k)) is str and expected[k] for k in
                    ("postmaster_started_at", "system_identifier", "server_instance_nonce", "server_version_num")),
            "target_instance_incomplete")
    for key in ("catalog_fingerprint_sha256", "ownership_sha256"):
        checked_hash(target.get(key))
    require(target["component_fingerprints"] == IDENTITY.current_contract()["component_fingerprints"],
            "component_source_changed")
    roles = material.get("runtime_roles")
    require(type(roles) is list and roles and all(type(role) is str and IDENTITY.IDENTIFIER.fullmatch(role)
            for role in roles) and len(roles) == len(set(roles))
            and not {expected["role"], expected["session_user"]}.intersection(roles), "runtime_roles_invalid")
    state = material["before_state"]
    validate_inventory(state["inventory"])
    require(set(state["tables"]) == set(state["inventory"]["tables"])
            and set(state["sequences"]) == set(state["inventory"]["sequences"]), "snapshot_inventory_mismatch")
    for value in state["tables"].values():
        PLANNER.integer(value.get("rows"), "snapshot_count_invalid", minimum=0)
        checked_hash(value.get("sha256"))
    PREFLIGHT.preflight_opening_batch(material["opening"])
    PREFLIGHT.validate_opening_counters(material["opening"], material["counters"])
    require(material["opening"], "empty_opening_not_supported")
    native = {row["id"]: row["personal_account_id"] for row in material["native_users"]}
    require(len(native) == len(material["native_users"]) and len(set(native.values())) == len(native)
            and digest(native) == material["identity_map_sha256"]
            and {row["user_id"]: row["account_id"] for row in material["counters"]} == native,
            "opening_identity_coverage_mismatch")
    PLANNER.verify_target_identity_binding(material["source_plan"], native,
        import_run=material["identity_import"], native_users=material["native_users"])
    # Rebuild both adapters from the bound original plan. An edited payload is
    # not allowed to acquire the same source row hash with different money.
    rebuilt = PLANNER.build_opening_payloads(material["source_plan"], migration_id=material["migration_id"],
        observation=material["observation"], reservation_reconciliation={
            "source_archive_sha256": material["source_archive_sha256"], "mapping_sha256": material["mapping_sha256"],
            "reviewed_evidence_sha256": material["reservation_evidence_sha256"],
            "positions": [{"source_user_id": counter["user_id"], "reserved_units": opening["reserved_units"]}
                          for opening, counter in zip(material["opening"], material["counters"])]})
    require(rebuilt["payloads"] == material["opening"] and
            PLANNER.build_opening_counter_payloads(material["source_plan"], rebuilt)["payloads"] == material["counters"],
            "opening_plan_adapter_mismatch")
    review = material["source_review"]
    require(type(review.get("format_version")) is int and review["format_version"] == 1
            and review.get("decision") == "approved-offline-opening" and review.get("scope") == SCOPE
            and type(review.get("credits_per_usd")) is int and review["credits_per_usd"] == 500000
            and review.get("other_rights_policy") == "remain-disabled-pending-separate-import"
            and all(review.get(key) == material[key] for key in ("source_archive_sha256", "mapping_sha256", "identity_map_sha256")),
            "source_unit_or_review_unproven")
    checked_hash(review.get("evidence_sha256"), "source_review_evidence_missing")
    for row in material["opening"]:
        require(all(row[key] == material[key] for key in ("migration_id", "source_archive_sha256", "mapping_sha256")),
                "opening_source_mismatch")
    needed = (4 * len(material["opening"]), sum(bool(row["available_units"] or row["affiliate_units"] or row["reserved_units"])
                                               for row in material["opening"]))
    for name, count in zip(CHANGED_SEQUENCES, needed):
        sequence = state["sequences"][name]
        require(type(sequence.get("increment")) is int and sequence["increment"] == 1
                and type(sequence.get("cache")) is int and sequence["cache"] == 1 and sequence.get("cycle") is False
                and type(sequence.get("is_called")) is bool, "noncanonical_ledger_sequence")
        last = PLANNER.integer(sequence.get("last_value"), "sequence_value_invalid", minimum=1)
        maximum = PLANNER.integer(sequence.get("max"), "sequence_value_invalid", minimum=1)
        require(last + int(sequence["is_called"]) + max(count - 1, 0) <= maximum, "ledger_sequence_overflow")


def _guard(material: dict) -> str:
    target = material["target"]
    roles = material["runtime_roles"]
    contract = IDENTITY.current_contract()
    return f"""
 PERFORM pg_stat_clear_snapshot();
 IF ({instance_sql()}) IS DISTINCT FROM {literal(target['instance'])}::jsonb THEN
  RAISE EXCEPTION 'financial_target_instance_mismatch'; END IF;
 IF EXISTS(SELECT 1 FROM pg_stat_activity WHERE datid=(SELECT oid FROM pg_database WHERE datname=current_database())
  AND pid<>pg_backend_pid()) THEN RAISE EXCEPTION 'financial_target_has_other_clients'; END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=current_user AND NOT rolcanlogin AND NOT rolsuper
  AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls)
 OR NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=session_user AND rolcanlogin AND NOT rolsuper
  AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls)
 OR (SELECT count(*) FROM pg_roles WHERE rolname IN ({','.join(literal(r) for r in roles)}) AND NOT rolcanlogin AND NOT rolsuper)<>{len(roles)}
 OR EXISTS(SELECT 1 FROM pg_roles WHERE rolcanlogin AND NOT rolsuper AND rolname<>session_user
  AND has_database_privilege(oid,current_database(),'CONNECT'))
 THEN RAISE EXCEPTION 'financial_offline_roles_mismatch'; END IF;
 IF EXISTS(SELECT 1 FROM pg_namespace n WHERE {TABLE_FILTER} AND n.nspname<>'public' AND pg_get_userbyid(n.nspowner)<>current_user)
 OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE {TABLE_FILTER} AND pg_get_userbyid(c.relowner)<>current_user)
 OR EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE {TABLE_FILTER} AND pg_get_userbyid(p.proowner)<>current_user)
 THEN RAISE EXCEPTION 'financial_schema_owner_mismatch'; END IF;
 IF ({IDENTITY.catalog_hash_sql(contract)}) IS DISTINCT FROM {literal(target['catalog_fingerprint_sha256'])}
 OR ({ownership_sql()}) IS DISTINCT FROM {literal(target['ownership_sha256'])}
 OR NOT EXISTS(SELECT 1 FROM core_meta.runtime_contract WHERE singleton
  AND encode(catalog_fingerprint,'hex')={literal(target['catalog_fingerprint_sha256'])})
 OR (SELECT jsonb_object_agg(name,encode(fingerprint,'hex')) FROM core_meta.component_contract)
  IS DISTINCT FROM {literal(target['component_fingerprints'])}::jsonb
 OR ({inventory_sql()}) IS DISTINCT FROM {literal(material['before_state']['inventory'])}::jsonb
 THEN RAISE EXCEPTION 'financial_target_catalog_mismatch'; END IF;
"""


def _dollar_quote(sql: str) -> str:
    tag = "$lmm_financial_" + sha(sql.encode())[:24] + "$"
    require(tag not in sql, "sql_delimiter_collision")
    # Only replace the four template delimiters. A source string may itself
    # contain the old delimiter, including escaped SQL-looking text.
    prefix, separator, body = sql.partition("END $lmm_financial$;")
    require(bool(separator), "sql_template_invalid")
    prefix = prefix.replace("DO $lmm_financial$", "DO " + tag, 1)
    body = body.replace("DO $lmm_financial$", "DO " + tag, 1)
    middle, separator, suffix = body.rpartition("END $lmm_financial$;")
    require(bool(separator), "sql_template_invalid")
    return prefix + "END " + tag + ";" + middle + "END " + tag + ";" + suffix


def _start(material: dict, *, readonly: bool) -> str:
    # Take a session fence BEFORE choosing the transaction snapshot. A wait inside
    # SERIALIZABLE would not refresh that snapshot. Never retry automatically.
    return f"""SET standard_conforming_strings=on;
SET timezone='UTC';
SET lock_timeout='5s';
SET statement_timeout='120s';
DO $lmm_financial$ BEGIN
 IF NOT pg_try_advisory_lock({FENCE}) THEN RAISE EXCEPTION 'financial_import_busy'; END IF;
END $lmm_financial$;
BEGIN ISOLATION LEVEL SERIALIZABLE{' READ ONLY' if readonly else ''};
SET LOCAL synchronous_commit=on;
SET LOCAL row_security=off;
SET LOCAL search_path=pg_catalog,public;
"""


def build_apply_sql(material: dict) -> str:
    validate_material(material)
    inventory = material["before_state"]["inventory"]
    state = state_sql(inventory, material["migration_id"])
    guard = _guard(material)
    locks = ",".join(relation(name) for name in inventory["tables"])
    changed_tables = "ARRAY[" + ",".join(literal(n) for n in CHANGED_TABLES) + "]::text[]"
    changed_sequences = "ARRAY[" + ",".join(literal(n) for n in CHANGED_SEQUENCES) + "]::text[]"
    restarts = []
    for name in CHANGED_SEQUENCES:
        seq = material["before_state"]["sequences"][name]
        next_value = seq["last_value"] + int(seq["is_called"])
        # RESTART is transactional. It also makes subsequent nextval calls use
        # rollbackable sequence storage, unlike setval or a plain nextval.
        restarts.append(f"ALTER SEQUENCE {relation(name)} RESTART WITH {next_value};")
    sql = _start(material, readonly=False) + f"""
DO $lmm_financial$
DECLARE m jsonb := {literal(material)}::jsonb; previous core_migration.import_runs%ROWTYPE;
 before_state jsonb; after_state jsonb; v_payload jsonb;
BEGIN
 {guard}
 LOCK TABLE {locks} IN ACCESS EXCLUSIVE MODE;
 {guard}
 SELECT ({state}) INTO before_state;
 SELECT * INTO previous FROM core_migration.import_runs WHERE migration_id=m->>'migration_id';
 IF FOUND THEN
  IF previous.source_archive_sha256<>m->>'source_archive_sha256' OR previous.mapping_sha256<>m->>'mapping_sha256'
   OR previous.manifest->'material' IS DISTINCT FROM m
   OR previous.manifest->>'scope' IS DISTINCT FROM {literal(SCOPE)}
   OR previous.manifest->'post_state' IS DISTINCT FROM before_state
  THEN RAISE EXCEPTION 'financial_replay_conflict'; END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM core_billing.opening_import_runs)
   OR EXISTS(SELECT 1 FROM core_migration.import_runs WHERE manifest->>'scope'={literal(SCOPE)})
  THEN RAISE EXCEPTION 'financial_other_import_conflict'; END IF;
  IF before_state IS DISTINCT FROM m->'before_state' THEN RAISE EXCEPTION 'financial_target_state_changed'; END IF;
  IF (SELECT count(*) FROM core_billing.ledger_accounts)<>2 OR (SELECT count(*) FROM core_billing.balance_state)<>2
   OR EXISTS(SELECT 1 FROM core_billing.ledger_accounts a JOIN core_billing.balance_state b ON b.ledger_account_id=a.id
    WHERE a.owner_account_id IS NOT NULL OR a.bucket NOT IN ('clearing','revenue') OR a.unit<>'credit_500k_usd'
    OR b.balance_units<>0 OR b.revision<>0)
   OR EXISTS(SELECT 1 FROM core_billing.ledger_operations) OR EXISTS(SELECT 1 FROM core_billing.ledger_journals)
   OR EXISTS(SELECT 1 FROM core_billing.ledger_entries) OR EXISTS(SELECT 1 FROM core_billing.opening_imports)
   OR EXISTS(SELECT 1 FROM core_billing.account_opening_counters)
  THEN RAISE EXCEPTION 'financial_ledger_seed_mismatch'; END IF;
  IF (SELECT coalesce(jsonb_agg(jsonb_build_object('id',id,'personal_account_id',personal_account_id) ORDER BY id),'[]'::jsonb)
   FROM core_identity.users) IS DISTINCT FROM m->'native_users'
   OR NOT EXISTS(SELECT 1 FROM core_migration.import_runs r WHERE to_jsonb(r)=m->'identity_import')
  THEN RAISE EXCEPTION 'financial_identity_binding_mismatch'; END IF;
  {' '.join(restarts)}
  FOR v_payload IN SELECT value FROM jsonb_array_elements(m->'opening') ORDER BY (value->>'account_id')::bigint LOOP
   PERFORM core_billing.import_opening_balance(v_payload);
  END LOOP;
  FOR v_payload IN SELECT value FROM jsonb_array_elements(m->'counters') ORDER BY (value->>'account_id')::bigint LOOP
   PERFORM core_billing.import_account_opening_counters(v_payload);
  END LOOP;
  SET CONSTRAINTS ALL IMMEDIATE;
  IF (SELECT count(*) FROM core_billing.opening_imports)<>jsonb_array_length(m->'opening')
   OR (SELECT count(*) FROM core_billing.account_opening_counters)<>jsonb_array_length(m->'counters')
   OR EXISTS(SELECT 1 FROM jsonb_array_elements(m->'opening') p LEFT JOIN core_billing.opening_imports o
    ON o.account_id=(p->>'account_id')::bigint WHERE o.request IS DISTINCT FROM p)
   OR EXISTS(SELECT 1 FROM jsonb_array_elements(m->'counters') p LEFT JOIN core_billing.account_opening_counters c
    ON c.account_id=(p->>'account_id')::bigint WHERE c.request IS DISTINCT FROM p)
  THEN RAISE EXCEPTION 'financial_exact_readback_failed'; END IF;
  IF EXISTS(SELECT 1 FROM jsonb_array_elements(m->'opening') p CROSS JOIN
    (VALUES ('wallet'),('debt'),('reserved'),('affiliate')) expected(bucket)
    LEFT JOIN core_billing.ledger_accounts a ON a.owner_account_id=(p->>'account_id')::bigint AND a.bucket=expected.bucket
    LEFT JOIN core_billing.balance_state b ON b.ledger_account_id=a.id
    WHERE b.balance_units IS DISTINCT FROM CASE expected.bucket
     WHEN 'wallet' THEN greatest((p->>'available_units')::bigint,0)
     WHEN 'debt' THEN least((p->>'available_units')::bigint,0)
     WHEN 'reserved' THEN (p->>'reserved_units')::bigint ELSE (p->>'affiliate_units')::bigint END)
   OR EXISTS(SELECT 1 FROM core_billing.ledger_journals j LEFT JOIN core_billing.ledger_entries e ON e.journal_id=j.id
    GROUP BY j.id HAVING count(e.journal_id)<2 OR sum(e.delta_units)<>0)
   OR EXISTS(SELECT 1 FROM core_billing.balance_state b LEFT JOIN
    (SELECT ledger_account_id,sum(delta_units) units,count(*) entries FROM core_billing.ledger_entries GROUP BY ledger_account_id) e
    ON e.ledger_account_id=b.ledger_account_id WHERE b.balance_units::numeric<>coalesce(e.units,0) OR b.revision<>coalesce(e.entries,0))
   OR (SELECT coalesce(sum(balance_units),0) FROM core_billing.balance_state)<>0
  THEN RAISE EXCEPTION 'financial_ledger_reconciliation_failed'; END IF;
  SELECT ({state}) INTO after_state;
  IF (before_state->'tables') - {changed_tables} IS DISTINCT FROM (after_state->'tables') - {changed_tables}
   OR (before_state->'sequences') - {changed_sequences} IS DISTINCT FROM (after_state->'sequences') - {changed_sequences}
  THEN RAISE EXCEPTION 'financial_unrelated_state_changed'; END IF;
  INSERT INTO core_migration.import_runs(migration_id,source_archive_sha256,mapping_sha256,manifest)
   VALUES(m->>'migration_id',m->>'source_archive_sha256',m->>'mapping_sha256',
    jsonb_build_object('scope',{literal(SCOPE)},'material',m,'post_state',after_state));
 END IF;
 {guard}
END $lmm_financial$;
COMMIT;
SELECT jsonb_build_object('status','COMMITTED','migration_id',{literal(material['migration_id'])},
 'material_sha256',{literal(digest(material))});
"""
    return _dollar_quote(sql)


def build_resolve_sql(material: dict, original_application_name: str) -> str:
    validate_material(material)
    require(type(original_application_name) is str and re.fullmatch(r"lmm-financial-[0-9a-f]{32}", original_application_name),
            "original_attempt_identity_invalid")
    state = state_sql(material["before_state"]["inventory"], material["migration_id"])
    sql = _start(material, readonly=True) + f"""
DO $lmm_financial$
DECLARE m jsonb := {literal(material)}::jsonb; previous core_migration.import_runs%ROWTYPE; actual jsonb;
BEGIN
 {_guard(material)}
 IF EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name={literal(original_application_name)}
  AND datid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND pid<>pg_backend_pid())
 THEN RAISE EXCEPTION 'financial_original_session_still_active'; END IF;
 SELECT ({state}) INTO actual;
 SELECT * INTO previous FROM core_migration.import_runs WHERE migration_id=m->>'migration_id';
 IF FOUND THEN
  IF previous.source_archive_sha256<>m->>'source_archive_sha256' OR previous.mapping_sha256<>m->>'mapping_sha256'
   OR previous.manifest->'material' IS DISTINCT FROM m OR previous.manifest->>'scope' IS DISTINCT FROM {literal(SCOPE)}
   OR previous.manifest->'post_state' IS DISTINCT FROM actual
  THEN RAISE EXCEPTION 'financial_resolution_conflict'; END IF;
 ELSIF actual IS DISTINCT FROM m->'before_state' THEN RAISE EXCEPTION 'financial_absence_not_proven'; END IF;
 {_guard(material)}
END $lmm_financial$;
SELECT jsonb_build_object('status',CASE WHEN EXISTS(SELECT 1 FROM core_migration.import_runs
 WHERE migration_id={literal(material['migration_id'])}) THEN 'COMMITTED' ELSE 'NOT_APPLIED' END,
 'migration_id',{literal(material['migration_id'])},'material_sha256',{literal(digest(material))});
COMMIT;
"""
    return _dollar_quote(sql)


def verify_approval(material: dict, sql: str, approval: dict) -> None:
    require(type(approval) is dict and type(approval.get("format_version")) is int and approval == {
        "format_version": 1, "decision": "approve-offline-opening", "scope": SCOPE,
        "material_sha256": digest(material), "sql_sha256": sha(sql.encode()),
        "source_review_sha256": digest(material["source_review"]),
    }, "offline_approval_mismatch")


def write_private(directory: Path, name: str, value: str) -> Path:
    result = IDENTITY.write_private(directory, name, value)
    fd = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)
    return result


def read_private(path: Path) -> str:
    return IDENTITY.private_backup_path(path).read_text(encoding="utf-8")


def verify_source_files(material: dict) -> None:
    files = material["source_files"]
    for item in files.values():
        require(IDENTITY.sha256_file(IDENTITY.private_backup_path(Path(item["path"]))) == item["sha256"],
                "source_file_changed")
    load = lambda name: PLANNER.parse_json(read_private(Path(files[name]["path"])))
    require(all(digest(load(name)) == digest(expected) for name, expected in (
        ("financial_plan", material["source_plan"]), ("source_review", material["source_review"]),
        ("identity_manifest", material["identity_import"]["manifest"]))), "source_file_content_mismatch")
    identities = PLANNER.identity_index([PLANNER.parse_json(line) for line in
        read_private(Path(files["identity_map"]["path"])).splitlines() if line])
    require(digest(identities) == material["identity_map_sha256"] and files["identity_map"]["sha256"] ==
            material["source_plan"]["identity_provenance"]["identity_map_file_sha256"], "source_id_map_file_mismatch")
    proof = load("source_export_proof")
    require(proof["source_archive_sha256"] == material["source_archive_sha256"] and
            proof["source_export_sha256"] == files["source_export"]["sha256"], "source_export_file_mismatch")
    source = load("source_receipt")
    opening = PLANNER.build_opening_payloads(material["source_plan"], migration_id=material["migration_id"],
        observation=material["observation"], reservation_reconciliation=load("reservations"))
    require(opening["payloads"] == material["opening"] and
            PLANNER.source_observation(source) == material["observation"], "source_observation_or_reservation_mismatch")
    # These files are bound transitively by the receipt's private paths and the
    # approved export proof's hashes. Recheck them before submission and after
    # a successful transport return, just like the directly listed source files.
    # This reads local files only; it cannot reconnect to the source database.
    _, _, metadata_hashes, _, _ = PLANNER.metadata(source)
    require(digest(metadata_hashes) == digest(proof.get("metadata_hashes")),
            "source_export_metadata_hash_mismatch")


def run_sql(connection: dict, sql: str, output: Path, application_name: str, *, readonly=False,
            label="database") -> tuple[int, str]:
    environment = {key: value for key, value in os.environ.items() if not key.startswith("PG")}
    environment.update(IDENTITY.socket_connection(connection))
    environment.update(PGCONNECT_TIMEOUT="5", PGAPPNAME=application_name,
                       PGOPTIONS=IDENTITY.psql_options(connection, readonly=readonly))
    result = subprocess.run(["psql", "-X", "--no-password", "-qAt", "-v", "ON_ERROR_STOP=1"],
        input=sql, text=True, capture_output=True, env=environment, timeout=150, check=False)
    write_private(output, label + ".stderr.private.txt", result.stderr)
    write_private(output, label + ".stdout.private.txt", result.stdout)
    return result.returncode, result.stdout


def execute(material: dict, *, approval: dict, original_sql: str, connection: dict,
            output: Path, original_attempt: dict | None = None) -> dict:
    """One execution, never an automatic retry. All post-start errors are UNKNOWN."""
    validate_material(material)
    require(original_sql == build_apply_sql(material), "original_sql_changed")
    verify_approval(material, original_sql, approval)
    verify_source_files(material)
    owner, login = IDENTITY.connection_roles(connection)
    expected = material["target"]["instance"]
    require(owner == expected["role"] and login == expected["session_user"]
            and connection.get("database") == expected["database"] and connection.get("port") == expected["port"],
            "target_connection_binding_mismatch")
    IDENTITY.socket_connection(connection)  # Reject transport/secret paths before recording a write attempt.
    application_name = "lmm-financial-" + uuid.uuid4().hex
    attempt = {"format_version": 1, "migration_id": material["migration_id"],
        "material_sha256": digest(material), "sql_sha256": sha(original_sql.encode()),
        "application_name": application_name, "operation": "resolve" if original_attempt else "apply"}
    sql = original_sql
    if original_attempt is not None:
        require(original_attempt.get("operation") == "apply"
                and all(original_attempt.get(k) == attempt[k] for k in ("migration_id", "material_sha256", "sql_sha256")),
                "original_attempt_material_mismatch")
        sql = build_resolve_sql(material, original_attempt.get("application_name"))
    write_private(output, "attempt.private.json", canonical(attempt) + "\n")
    write_private(output, "executed.private.sql", sql)
    # Artifacts and attempt identity are durable BEFORE a server can receive SQL.
    outcome = {"status": "UNKNOWN", "migration_id": material["migration_id"], "material_sha256": digest(material)}
    try:
        code, stdout = run_sql(connection, sql, output, application_name)
        if code == 0:
            verify_source_files(material)
            require(source_hashes() == material["tool_source_hashes"], "financial_tool_source_changed")
            result = PLANNER.parse_json(stdout)
            allowed = ("COMMITTED", "NOT_APPLIED") if original_attempt else ("COMMITTED",)
            if type(result) is dict and set(result) == set(outcome) and result.get("status") in allowed \
                    and all(result.get(key) == outcome[key] for key in ("migration_id", "material_sha256")):
                outcome = result
    except Exception:
        # After a server may have received SQL, even an unexpected verifier error
        # must not escape as REFUSED (which would imply no write was attempted).
        # The durable original attempt is the only safe recovery reference.
        pass
    try:
        write_private(output, "result.private.json", canonical(outcome) + "\n")
    except Exception:
        outcome = {**outcome, "status": "UNKNOWN"}
    return outcome


def prepare(args, output: Path) -> dict:
    """Read an existing frozen export; query only the explicitly selected target."""
    required = ("plan", "source_receipt", "export_proof", "identity_map", "identity_manifest",
                "source_review", "reservations", "target_receipt", "migration_id", "runtime_role")
    require(all(getattr(args, name, None) for name in required), "prepare_arguments_missing")
    load = lambda path: PLANNER.parse_json(read_private(path))
    source = load(args.source_receipt)
    plan = load(args.plan)
    identity_manifest = load(args.identity_manifest)
    provenance = PLANNER.bind_identity_provenance(identity_manifest,
        map_file_sha256=IDENTITY.sha256_file(IDENTITY.private_backup_path(args.identity_map)),
        source_archive_sha256=source["archive"]["sha256"])
    identities = PLANNER.identity_index([PLANNER.parse_json(line) for line in read_private(args.identity_map).splitlines() if line])
    # This reuses a prior local read-only export; it cannot connect to the source.
    tables, proof = PLANNER.reuse_frozen_export(source, args.export_proof, output)
    rebuilt = PLANNER.plan_financial(tables, identities, source_sha256=proof["source_archive_sha256"],
        source_timezone=plan.get("source_timezone"), identity_provenance=provenance,
        primary_keys=proof["source_primary_keys"], column_schema=proof["source_column_schema"])
    require(digest(rebuilt) == digest(plan), "source_plan_rebuild_mismatch")
    target = load(args.target_receipt)
    require(target.get("status") == "initialized-identity-bound-financial-target"
            and target.get("application_or_worker_started") is False
            and target.get("migration_id") == args.migration_id
            and target.get("source_archive_sha256") == plan["source_archive_sha256"], "target_receipt_invalid")
    connection = target["connection"]
    code, raw = run_sql(connection, "BEGIN READ ONLY;" + inventory_sql() + ";COMMIT;", output,
        "lmm-financial-preview", readonly=True, label="inventory")
    require(code == 0, "target_inventory_read_failed")
    inventory = PLANNER.parse_json(raw)
    code, raw = run_sql(connection, snapshot_sql(inventory, args.migration_id), output,
        "lmm-financial-preview", readonly=True, label="snapshot")
    require(code == 0, "target_snapshot_read_failed")
    snapshot = PLANNER.parse_json(raw)
    IDENTITY.verify_instance(snapshot["instance"], connection, target["instance_identity"], source=False)
    require(snapshot["instance"]["system_identifier"] == target["instance_identity"]["system_identifier"],
            "target_system_identifier_mismatch")
    require((str(snapshot["instance"]["database_oid"]), datetime.fromisoformat(snapshot["instance"]["postmaster_started_at"])) !=
            (str(proof["source_instance"]["database_oid"]), datetime.fromisoformat(proof["source_instance"]["postmaster_started_at"])),
            "source_target_same_database")
    paths = {"archive": Path(source["archive"]["path"]), "source_receipt": args.source_receipt,
        "financial_plan": args.plan, "source_export": Path(proof["reused_source_export_path"]),
        "source_export_proof": args.export_proof, "identity_map": args.identity_map,
        "identity_manifest": args.identity_manifest, "source_review": args.source_review, "reservations": args.reservations}
    files = {name: {"path": str(IDENTITY.private_backup_path(path)), "sha256": IDENTITY.sha256_file(path)}
             for name, path in paths.items()}
    material = prepare_material(plan, migration_id=args.migration_id, identities=identities,
        observation=PLANNER.source_observation(source), reservations=load(args.reservations), review=load(args.source_review),
        snapshot=snapshot, target_contract=target["contract"], runtime_roles=args.runtime_role, source_files=files)
    verify_source_files(material)
    sql = build_apply_sql(material)
    write_private(output, "material.private.json", canonical(material) + "\n")
    write_private(output, "opening.private.sql", sql)
    # An approval template is NOT authorization. An independent offline review
    # must bind all three hashes and replace the decision with the documented value.
    approval = {"format_version": 1, "scope": SCOPE, "decision": "REVIEW_REQUIRED",
        "material_sha256": digest(material), "sql_sha256": sha(sql.encode()),
        "source_review_sha256": digest(material["source_review"])}
    write_private(output, "approval-required.private.json", canonical(approval) + "\n")
    return {"status": "PREPARED_NOT_APPLIED", "migration_id": args.migration_id,
            "material_sha256": digest(material), "target_writes": 0}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("material", "sql", "approval", "target-receipt", "plan", "source-receipt",
                 "export-proof", "identity-map", "identity-manifest", "source-review", "reservations"):
        parser.add_argument("--" + name, type=Path)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--migration-id")
    parser.add_argument("--runtime-role", action="append")
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--prepare", action="store_true")
    mode.add_argument("--apply", action="store_true")
    mode.add_argument("--resolve-attempt", type=Path)
    args = parser.parse_args()
    try:
        IDENTITY.private_backup_path(args.output.parent, directory=True)
        args.output.mkdir(mode=0o700)
        output = IDENTITY.private_backup_path(args.output, directory=True)
        if args.prepare:
            result = prepare(args, output)
        else:
            require(all((args.material, args.approval, args.sql, args.target_receipt)), "apply_arguments_missing")
            material = PLANNER.parse_json(read_private(args.material))
            approval = PLANNER.parse_json(read_private(args.approval))
            target = PLANNER.parse_json(read_private(args.target_receipt))
            original = PLANNER.parse_json(read_private(args.resolve_attempt)) if args.resolve_attempt else None
            require(target.get("application_or_worker_started") is False, "application_start_not_excluded")
            result = execute(material, approval=approval, original_sql=read_private(args.sql),
                             connection=target["connection"], output=output, original_attempt=original)
        print(canonical(result))
        return 2 if result["status"] == "UNKNOWN" else 0
    except BrokenPipeError:
        # A missing console ACK does not cancel a committed database transaction.
        return 2
    except (OSError, ValueError, TypeError, KeyError, Error, PREFLIGHT.FinancialPreflightError, subprocess.TimeoutExpired) as error:
        print(canonical({"status": "REFUSED", "error_code": str(error) if isinstance(error, Error)
                         else type(error).__name__}), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
