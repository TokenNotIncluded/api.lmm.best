#!/usr/bin/env python3
"""Offline console-only migration. Dry-run is the default; never performs DDL.

Only three legacy option keys, setup facts and announcement receipts are copied.
Use an owned immutable local snapshot and a separately initialized, stopped core
target. Both connections require checked private Unix sockets. Raw exports and
SQL contain private data and must remain in a new directory below ~/Backups.
Announcement interpretation follows main 7be6ee81's mandatory_announcement.go;
the original TEXT, revision and epoch values are retained, not recalculated.

Target imports require the exact same-archive identity-v3 import and its private
manifest, mapping and ID map. Replay must reuse their original paths and bytes.
For a NOLOGIN owner, explicitly pair connection.login_role with set_role=role;
the checked startup role option selects the owner, without changing role flags.
An unconfirmed commit keeps its original migration ID and exact materials; a
missing registry row is not rollback evidence while that session may still run.
End or fence that session with advisory lock 741803121 before resolving absence.
"""

from __future__ import annotations

import argparse
import calendar
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import sys


ROOT = Path(__file__).resolve().parents[1]
_spec = importlib.util.spec_from_file_location("lmm_identity_import", ROOT / "scripts/migrate-legacy-snapshot.py")
assert _spec and _spec.loader
IDENTITY = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(IDENTITY)
MappingError, require = IDENTITY.MappingError, IDENTITY.require
canonical, literal = IDENTITY.canonical, IDENTITY.sql_literal
KEYS = ("console_setting.announcements", "Announcements", "Notice")
TABLES = ("options", "setups", "announcement_reads")
RFC3339 = re.compile(r"(\d{4})-(\d{2})-(\d{2})T(\d{1,2}):(\d{2}):(\d{2})(?:[.,]\d+)?(?:Z|([+-])(\d{2}):(\d{2}))\Z", re.ASCII)


class Pairs(list):
    """Preserve duplicate JSON fields so an invalid earlier value cannot vanish."""


def valid_text(value, code: str) -> str:
    require(isinstance(value, str) and "\0" not in value, code)
    try:
        value.encode("utf-8", errors="strict")
    except UnicodeError:
        raise MappingError(code) from None
    return value


def _valid_json_text(value) -> None:
    if isinstance(value, str):
        valid_text(value, "announcement_non_utf8_or_nul")
    elif isinstance(value, Pairs):
        for key, item in value:
            valid_text(key, "announcement_non_utf8_or_nul")
            _valid_json_text(item)
    elif isinstance(value, list):
        for item in value:
            _valid_json_text(item)


def validate_announcements(raw: str) -> None:
    # PostgreSQL JSONB cannot represent escaped NUL or unpaired surrogates. Rust
    # also rejects unpaired surrogates; preflight rejects instead of rewriting.
    if raw == "":
        return
    try:
        value = json.loads(raw, object_pairs_hook=Pairs,
                           parse_constant=lambda _: (_ for _ in ()).throw(ValueError()))
    except (ValueError, RecursionError):
        raise MappingError("announcement_json_invalid") from None
    _valid_json_text(value)
    require(value is None or (isinstance(value, list) and not isinstance(value, Pairs)), "announcement_array_required")
    for item in value or []:
        require(item is None or isinstance(item, Pairs), "announcement_object_required")
        fields = {}
        for key, field in item or []:
            # encoding/json foldName uses Unicode SimpleFold, including these
            # two non-ASCII characters folding to ASCII.
            folded = key.replace("ſ", "s").replace("K", "k").lower()
            if field is None:
                continue  # Go null does not replace a scalar struct field.
            if folded in ("content", "extra", "publishdate", "ackrevision", "revision"):
                require(isinstance(field, str), "announcement_string_field_invalid")
            elif folded == "mandatory":
                require(type(field) is bool, "announcement_mandatory_invalid")
            elif folded in ("id", "read_at"):
                require(type(field) is int and -9223372036854775808 <= field <= 9223372036854775807,
                        "announcement_integer_field_invalid")
            else:
                continue
            fields[folded] = field
        if not fields.get("mandatory", False):
            continue
        IDENTITY.positive_int(fields.get("id", 0), "mandatory_announcement_id_invalid")
        match = RFC3339.fullmatch(fields.get("publishdate", ""))
        require(match is not None, "mandatory_announcement_date_invalid")
        year, month, day, hour, minute, second = map(int, match.groups()[:6])
        require(1 <= month <= 12 and 1 <= day <= calendar.monthrange(year, month)[1]
                and hour <= 23 and minute <= 59 and second <= 59
                and (match[7] is None or (int(match[8]) <= 24 and int(match[9]) <= 60)),
                "mandatory_announcement_date_invalid")


def map_console(options: list[dict], setups: list[dict], reads: list[dict], *, source_sha256: str) -> dict:
    require(isinstance(source_sha256, str) and IDENTITY.HEX.fullmatch(source_sha256) is not None, "source_hash_invalid")
    require(all(isinstance(rows, list) for rows in (options, setups, reads)), "source_shape_invalid")
    mapped_options = {}
    for row in options:
        require(isinstance(row, dict) and row.get("key") in KEYS, "option_not_whitelisted")
        key = row["key"]
        require(key not in mapped_options, "duplicate_option_key")
        raw = valid_text(row.get("value"), "option_text_invalid")
        if key != "Notice":
            validate_announcements(raw)
        mapped_options[key] = raw
    require(len(setups) <= 1, "multiple_setup_records")
    initialization = None
    if setups:
        row = setups[0]
        require(isinstance(row, dict) and type(row.get("id")) is int and row["id"] == 1, "setup_id_invalid")
        version = valid_text(row.get("version"), "setup_version_invalid")
        require(1 <= len(version) <= 128, "setup_version_invalid")
        epoch = IDENTITY.positive_int(row.get("initialized_at"), "setup_epoch_invalid")
        # An explicit safe PostgreSQL timestamp domain. Never round or substitute.
        require(epoch <= 253402300799, "setup_epoch_out_of_range")
        initialization = {"version": version, "initialized_at": epoch}
    receipts = {}
    for row in reads:
        require(isinstance(row, dict), "announcement_read_shape_invalid")
        user_id = IDENTITY.positive_int(row.get("user_id"), "read_user_id_invalid")
        item_id = IDENTITY.positive_int(row.get("announcement_id"), "read_announcement_id_invalid")
        revision = row.get("revision")
        require(isinstance(revision, str) and IDENTITY.HEX.fullmatch(revision) is not None, "read_revision_invalid")
        read_at = IDENTITY.positive_int(row.get("read_at"), "read_epoch_invalid")
        key = (user_id, item_id, revision)
        require(key not in receipts or receipts[key]["read_at"] == read_at, "conflicting_duplicate_receipt")
        receipts[key] = {"user_id": user_id, "announcement_id": item_id, "revision": revision, "read_at": read_at}
    data = {"source_archive_sha256": source_sha256,
            "options": [{"key": key, "value": value} for key, value in sorted(mapped_options.items())],
            "initialization": initialization, "receipts": [receipts[key] for key in sorted(receipts)]}
    digest = hashlib.sha256(canonical(data).encode("utf-8")).hexdigest()
    return {**data, "mapping_sha256": digest, "summary": {
        "options_copied": len(mapped_options), "setup_records_copied": len(setups),
        "source_receipts": len(reads), "receipts_copied": len(receipts),
        "exact_duplicate_receipts_deduplicated": len(reads) - len(receipts),
        "financial_rows_written": 0, "identities_written": 0}}


def bind_identity_proof(manifest_bytes: bytes, mapping_bytes: bytes, id_map_bytes: bytes,
                        *, source_sha256: str, contract: dict) -> dict:
    """Bind preserved user IDs to the reviewed v3 identity import, not just IDs.

    The full mapping remains private. The console SQL carries only field hashes
    for names, email, settings and password verifiers, plus authorization facts.
    It must still match the exact identity registry manifest in the transaction.
    """
    try:
        manifest = json.loads(manifest_bytes.decode("utf-8"))
        mapping = json.loads(mapping_bytes.decode("utf-8"))
        id_map = [json.loads(line) for line in id_map_bytes.decode("utf-8").splitlines() if line.strip()]
    except (ValueError, UnicodeError):
        raise MappingError("identity_artifact_json_invalid") from None
    require(isinstance(manifest, dict) and isinstance(mapping, dict), "identity_artifact_shape_invalid")
    expected_contract = {key: contract[key] for key in ("identity_version", "identity_fingerprint_sha256")}
    require(manifest.get("format_version") == 2 and manifest.get("scope") == "identity-only"
            and manifest.get("source_archive_sha256") == source_sha256
            and manifest.get("identity_contract") == expected_contract, "identity_manifest_contract_mismatch")
    records = mapping.get("records")
    require(isinstance(records, list), "identity_mapping_records_invalid")
    IDENTITY.validate_import_material(mapping, source_sha256, manifest)
    mapping_hash = hashlib.sha256(canonical({"source_sha256": source_sha256, "records": records}).encode()).hexdigest()
    require(mapping.get("mapping_sha256") == manifest.get("mapping_sha256") == mapping_hash
            and manifest.get("summary") == mapping.get("summary"), "identity_mapping_hash_mismatch")
    id_map_hash = hashlib.sha256(id_map_bytes).hexdigest()
    require(manifest.get("source_target_id_map_sha256") == id_map_hash, "identity_id_map_hash_mismatch")
    facts, expected_map, users, accounts = [], [], set(), set()
    for record in records:
        require(isinstance(record, dict), "identity_mapping_record_invalid")
        user_id = IDENTITY.positive_int(record.get("user_id"), "identity_mapping_user_invalid")
        account_id = IDENTITY.positive_int(record.get("account_id"), "identity_mapping_account_invalid")
        require(user_id not in users and account_id not in accounts, "identity_mapping_duplicate_id")
        users.add(user_id)
        accounts.add(account_id)
        require(record.get("platform_role") in ("user", "admin", "superadmin"), "identity_mapping_role_invalid")
        row = {key: record[key] for key in ("user_id", "account_id", "platform_role")}
        for key in ("active", "email_login_allowed", "password_login_allowed", "second_factor_required",
                    "legacy_bcrypt_allowed", "developer_access_granted"):
            require(type(record.get(key)) is bool, "identity_mapping_boolean_invalid")
            row[key] = record[key]
        for key in ("auth_version", "developer_access_revision"):
            row[key] = IDENTITY.positive_int(record.get(key), "identity_mapping_version_invalid")
        for key in ("username", "display_name", "email", "setting", "developer_access_source_reference"):
            raw = valid_text(record.get(key), "identity_mapping_text_invalid")
            row[key + "_sha256"] = hashlib.sha256(raw.encode()).hexdigest()
        verifier = record.get("password_hash")
        require(verifier is None or isinstance(verifier, str), "identity_mapping_verifier_invalid")
        row["password_hash_sha256"] = None if verifier is None else hashlib.sha256(valid_text(verifier, "identity_mapping_verifier_invalid").encode()).hexdigest()
        facts.append(row)
        expected_map.append({"source_user_id": user_id, "target_user_id": user_id,
                             "target_personal_account_id": account_id})
    for row in id_map:
        require(isinstance(row, dict) and set(row) == {"source_user_id", "target_user_id", "target_personal_account_id"},
                "identity_id_map_shape_invalid")
        for key in row:
            IDENTITY.positive_int(row[key], "identity_id_map_id_invalid")
    require(sorted(id_map, key=lambda row: row["source_user_id"]) == sorted(expected_map, key=lambda row: row["source_user_id"]),
            "identity_id_map_content_mismatch")
    return {"source_archive_sha256": source_sha256, "manifest": manifest,
            "manifest_sha256": hashlib.sha256(manifest_bytes).hexdigest(),
            "mapping_sha256": mapping_hash, "mapping_file_sha256": hashlib.sha256(mapping_bytes).hexdigest(),
            "source_target_id_map_sha256": id_map_hash, "facts": sorted(facts, key=lambda row: row["user_id"])}


def load_identity_proof(path: Path, *, source_sha256: str, contract: dict) -> dict:
    files = [IDENTITY.private_backup_path(p) for p in (path, path.with_name("identity-mapping.private.json"),
                                                       path.with_name("source-target-id-map.private.jsonl"))]
    proof = bind_identity_proof(*(p.read_bytes() for p in files), source_sha256=source_sha256, contract=contract)
    proof["input_file_hashes"] = {str(p): IDENTITY.sha256_file(p) for p in files}
    return proof


def identity_reference(proof: dict, migration_id: str) -> dict:
    require(isinstance(migration_id, str) and IDENTITY.MIGRATION_ID.fullmatch(migration_id) is not None,
            "identity_migration_id_invalid")
    return {"migration_id": migration_id, **{key: proof[key] for key in
            ("source_archive_sha256", "mapping_sha256", "manifest_sha256", "source_target_id_map_sha256")}}


def current_contract(root: Path = ROOT) -> dict:
    """Strictly extract the installer contract; unknown Rust syntax fails closed."""
    source_path = root / "services/core/src/core_schema.rs"
    source = source_path.read_text(encoding="utf-8")
    match = re.search(r"const COMPONENTS:\s*&\[\(&str,\s*&str\)\]\s*=\s*&\[(.*?)\];", source, re.S)
    require(match is not None, "component_contract_syntax_unknown")
    entries = re.compile(r'\(\s*"([a-z][a-z-]*)"\s*,\s*include_str!\("(\.\./schema/[a-z_]+\.sql)"\)\s*,?\s*\)')
    body = match[1]
    require(re.fullmatch(r"[\s,]*", entries.sub("", body)) is not None, "component_contract_syntax_unknown")
    hashes, files = {}, {str(source_path.relative_to(root)): IDENTITY.sha256_file(source_path)}
    schema_dir = (root / "services/core/schema").resolve()
    for entry in entries.finditer(body):
        name, relative = entry.groups()
        path = (source_path.parent / relative).resolve()
        require(path.parent == schema_dir and name not in hashes, "component_contract_invalid")
        hashes[name] = IDENTITY.sha256_file(path)
        files[str(path.relative_to(root))] = hashes[name]
    require("identity" in hashes and "console" in hashes and "migration" in hashes, "component_contract_incomplete")
    version_path = root / "services/core/src/identity/schema.rs"
    versions = re.findall(r"const VERSION:\s*i32\s*=\s*(\d+)\s*;", version_path.read_text())
    require(len(versions) == 1, "identity_version_syntax_unknown")
    files[str(version_path.relative_to(root))] = IDENTITY.sha256_file(version_path)
    catalogs = re.findall(r'const CATALOG:\s*&str\s*=\s*r#"(.*?)"#;', source, re.S)
    require(len(catalogs) == 1, "catalog_contract_syntax_unknown")
    return {"identity_version": int(versions[0]), "identity_fingerprint_sha256": hashes["identity"],
            "component_fingerprints": hashes, "source_file_hashes": files,
            "catalog_query_sha256": hashlib.sha256(catalogs[0].encode()).hexdigest(), "catalog_query": catalogs[0]}


def catalog_hash_sql(contract: dict) -> str:
    # Rust hashes each UTF-8 definition prefixed with its eight-byte BE length.
    return "SELECT encode(sha256(coalesce(string_agg(int8send(octet_length(convert_to(definition,'UTF8'))::bigint)||convert_to(definition,'UTF8'),''::bytea ORDER BY definition COLLATE \"C\"),''::bytea)),'hex') FROM (" + contract["catalog_query"] + ") catalog_definitions"


def source_table_hash_sql(schema: str, table: str) -> str:
    require(IDENTITY.IDENTIFIER.fullmatch(schema) is not None and table in TABLES, "source_identifier_invalid")
    where = " WHERE key IN (" + ",".join(map(literal, KEYS)) + ")" if table == "options" else ""
    return 'SELECT encode(sha256(coalesce(string_agg(int8send(octet_length(convert_to(to_jsonb(r)::text,\'UTF8\'))::bigint)||convert_to(to_jsonb(r)::text,\'UTF8\'),\'\'::bytea ORDER BY to_jsonb(r)::text COLLATE "C"),\'\'::bytea)),\'hex\') FROM "' + schema + '"."' + table + '" r' + where


def _snapshot_sql(destination: str) -> str:
    return f"""
 {destination} := '{{}}'::jsonb;
 FOR table_row IN SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE c.relkind IN ('r','p') AND n.nspname NOT IN ('pg_catalog','information_schema','core_console','core_migration') AND left(n.nspname,3)<>'pg_'
  ORDER BY n.nspname,c.relname
 LOOP
  EXECUTE format('SELECT jsonb_build_object(''rows'',count(*),''sha256'',encode(sha256(coalesce(string_agg(int8send(octet_length(convert_to(to_jsonb(r)::text,''UTF8''))::bigint)||convert_to(to_jsonb(r)::text,''UTF8''),''''::bytea ORDER BY to_jsonb(r)::text COLLATE "C"),''''::bytea)),''hex'')) FROM %I.%I r',table_row.nspname,table_row.relname) INTO snapshot_row;
  {destination} := {destination} || jsonb_build_object(table_row.nspname||'.'||table_row.relname,snapshot_row);
 END LOOP;
 {destination} := {destination} || jsonb_build_object('sequence-state','{{}}'::jsonb);
 FOR table_row IN SELECT n.nspname,c.relname,to_jsonb(s) AS definition
  FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_sequence s ON s.seqrelid=c.oid
  WHERE c.relkind='S' AND n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'
  ORDER BY n.nspname,c.relname
 LOOP
  EXECUTE format('SELECT jsonb_build_object(''last_value'',last_value,''is_called'',is_called) FROM %I.%I',table_row.nspname,table_row.relname) INTO snapshot_row;
  {destination} := jsonb_set({destination},ARRAY['sequence-state',table_row.nspname||'.'||table_row.relname],
   snapshot_row||jsonb_build_object('definition',table_row.definition),true);
 END LOOP;
"""


def build_import_sql(plan: dict, *, migration_id: str, target_contract: dict, manifest: dict,
                     contract: dict, target_identity: dict, identity_proof: dict,
                     target_connection: dict | None = None) -> str:
    require(IDENTITY.MIGRATION_ID.fullmatch(migration_id) is not None, "migration_id_invalid")
    data = {key: plan[key] for key in ("source_archive_sha256", "options", "initialization", "receipts")}
    require(hashlib.sha256(canonical(data).encode()).hexdigest() == plan.get("mapping_sha256"), "mapping_hash_mismatch")
    require(manifest.get("scope") == "console-only" and manifest.get("mapping_sha256") == plan["mapping_sha256"]
            and manifest.get("source_archive_sha256") == plan["source_archive_sha256"] and "applied_at" not in manifest,
            "manifest_invalid")
    require(isinstance(identity_proof, dict) and identity_proof.get("source_archive_sha256") == plan["source_archive_sha256"],
            "identity_proof_required")
    reference = target_contract.get("identity_import", {})
    expected_reference = identity_reference(identity_proof, reference.get("migration_id"))
    require(reference == expected_reference and reference["migration_id"] != migration_id
            and manifest.get("identity_import") == expected_reference, "identity_import_reference_mismatch")
    require(all(row["user_id"] in {fact["user_id"] for fact in identity_proof["facts"]} for row in plan["receipts"]),
            "console_receipt_identity_unmapped")
    for key in ("identity_version", "identity_fingerprint_sha256", "component_fingerprints", "catalog_query_sha256"):
        require(target_contract.get(key) == contract[key], "target_contract_mismatch")
    catalog_hash = target_contract.get("catalog_fingerprint_sha256", "")
    require(isinstance(catalog_hash, str) and IDENTITY.HEX.fullmatch(catalog_hash) is not None, "target_catalog_hash_invalid")
    for key in ("database", "role", "database_oid", "postmaster_started_at", "server_version_num", "server_instance_nonce"):
        require(target_identity.get(key) is not None, "target_identity_incomplete")
    require(type(target_identity.get("port")) is int and 0 < target_identity["port"] <= 65535, "target_identity_incomplete")
    target_connection = target_connection or {"role": target_identity["role"]}
    require(target_connection.get("role") == target_identity["role"], "target_role_mismatch")
    has_role_switch = "login_role" in target_connection or "set_role" in target_connection
    if has_role_switch:
        require(isinstance(target_connection.get("login_role"), str)
                and IDENTITY.IDENTIFIER.fullmatch(target_connection["login_role"]) is not None
                and target_connection.get("set_role") == target_identity["role"]
                and target_connection["login_role"] != target_identity["role"], "target_role_switch_invalid")
    expected_session_user = target_connection["login_role"] if has_role_switch else target_identity["role"]
    require(target_identity.get("session_user", target_identity["role"]) == expected_session_user, "target_session_user_mismatch")
    owner_login_guard = " OR (SELECT rolcanlogin FROM pg_roles WHERE rolname=current_user) IS DISTINCT FROM false" if has_role_switch else ""
    payload, run_id = literal(canonical(data)), literal(migration_id)
    mapping_hash, source_hash = literal(plan["mapping_sha256"]), literal(plan["source_archive_sha256"])
    manifest_json, catalog = literal(canonical(manifest)), catalog_hash_sql(contract)
    components = literal(canonical(contract["component_fingerprints"]))
    identity_facts = literal(canonical(identity_proof["facts"]))
    identity_manifest = literal(canonical(identity_proof["manifest"]))
    identity_run_id = literal(reference["migration_id"])
    table_predicate = "c.relkind IN ('r','p') AND n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_'"
    sql = f"""BEGIN ISOLATION LEVEL SERIALIZABLE;
SET LOCAL synchronous_commit=on;
SET LOCAL standard_conforming_strings=on;
SET LOCAL search_path=pg_catalog,public;
SET LOCAL row_security=off;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='120s';
SELECT pg_advisory_xact_lock(741803121);
DO @DELIMITER@
DECLARE payload JSONB := {payload}::jsonb;
 identity_facts JSONB := {identity_facts}::jsonb;
 previous core_migration.import_runs%ROWTYPE;
 required_identity core_migration.import_runs%ROWTYPE;
 table_row RECORD;
 snapshot_row JSONB;
 before_other JSONB;
 after_other JSONB;
 before_runs JSONB;
 actual_catalog TEXT;
 applied_at TIMESTAMPTZ;
 replay BOOLEAN;
BEGIN
 IF current_database()<>{literal(target_identity['database'])} OR current_user<>{literal(target_identity['role'])}
 OR session_user<>{literal(expected_session_user)}{owner_login_guard}
 OR (SELECT oid::bigint FROM pg_database WHERE datname=current_database())<>{int(target_identity['database_oid'])}
 OR pg_postmaster_start_time()<>{literal(target_identity['postmaster_started_at'])}::timestamptz
 OR current_setting('server_version_num')<>{literal(str(target_identity['server_version_num']))}
 OR current_setting('port')::int<>{target_identity['port']}
 OR current_setting('lmm.migration_instance_nonce',true) IS DISTINCT FROM {literal(target_identity['server_instance_nonce'])}
 OR inet_server_addr() IS NOT NULL
 OR (SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname=current_database())<>current_user THEN
  RAISE EXCEPTION 'target instance identity mismatch';
 END IF;
 IF EXISTS(SELECT 1 FROM pg_stat_activity WHERE datid=(SELECT oid FROM pg_database WHERE datname=current_database())
  AND pid<>pg_backend_pid()) THEN
  RAISE EXCEPTION 'target has another active client';
 END IF;
 FOR table_row IN SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE {table_predicate} ORDER BY n.nspname,c.relname
 LOOP EXECUTE format('LOCK TABLE %I.%I IN SHARE MODE',table_row.nspname,table_row.relname); END LOOP;
 LOCK TABLE core_console.options,core_console.announcement_reads,core_console.initialization,core_migration.import_runs IN ACCESS EXCLUSIVE MODE;
 SELECT ({catalog}) INTO actual_catalog;
 IF actual_catalog<>{literal(catalog_hash)} OR NOT EXISTS(SELECT 1 FROM core_meta.runtime_contract WHERE singleton AND encode(catalog_fingerprint,'hex')=actual_catalog)
 OR NOT EXISTS(SELECT 1 FROM core_meta.schema_contract WHERE singleton AND version={contract['identity_version']} AND encode(fingerprint,'hex')={literal(contract['identity_fingerprint_sha256'])})
 OR (SELECT jsonb_object_agg(name,encode(fingerprint,'hex')) FROM core_meta.component_contract) IS DISTINCT FROM {components}::jsonb THEN
  RAISE EXCEPTION 'installed core contract or actual catalog mismatch';
 END IF;
 {_snapshot_sql('before_other')}
 SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY migration_id),'[]'::jsonb) INTO before_runs FROM core_migration.import_runs r WHERE migration_id<>{run_id};
 SELECT * INTO required_identity FROM core_migration.import_runs WHERE migration_id={identity_run_id};
 IF NOT FOUND THEN RAISE EXCEPTION 'required identity import receipt missing'; END IF;
 IF required_identity.source_archive_sha256<>{source_hash} THEN RAISE EXCEPTION 'identity import archive mismatch'; END IF;
 IF required_identity.mapping_sha256<>{literal(reference['mapping_sha256'])} THEN RAISE EXCEPTION 'identity import mapping proof mismatch'; END IF;
 IF required_identity.manifest IS DISTINCT FROM {identity_manifest}::jsonb THEN RAISE EXCEPTION 'identity import manifest proof mismatch'; END IF;
 IF EXISTS(SELECT 1 FROM core_migration.import_runs WHERE migration_id NOT IN ({run_id},{identity_run_id})) THEN
  RAISE EXCEPTION 'target belongs to another import';
 END IF;
 IF (SELECT count(*) FROM core_identity.users)<>jsonb_array_length(identity_facts)
 OR (SELECT count(*) FROM core_identity.accounts)<>jsonb_array_length(identity_facts)
 OR (SELECT count(*) FROM core_identity.user_profiles)<>jsonb_array_length(identity_facts)
 OR (SELECT count(*) FROM core_identity.developer_access)<>jsonb_array_length(identity_facts)
 OR (SELECT count(*) FROM core_identity.password_logins)<>(SELECT count(*) FROM jsonb_array_elements(identity_facts) f WHERE f->>'password_hash_sha256' IS NOT NULL)
 OR EXISTS(SELECT 1 FROM jsonb_array_elements(identity_facts) f
  LEFT JOIN core_identity.users u ON u.id=(f->>'user_id')::bigint
  LEFT JOIN core_identity.accounts a ON a.id=u.personal_account_id
  LEFT JOIN core_identity.user_profiles p ON p.user_id=u.id
  LEFT JOIN core_identity.developer_access d ON d.user_id=u.id
  LEFT JOIN core_identity.password_logins l ON l.user_id=u.id
  WHERE ROW(u.personal_account_id,u.platform_role,u.active,u.auth_version,a.kind,a.service_level,a.active,
   encode(sha256(convert_to(p.username,'UTF8')),'hex'),encode(sha256(convert_to(p.display_name,'UTF8')),'hex'),
   encode(sha256(convert_to(p.email,'UTF8')),'hex'),encode(sha256(convert_to(p.setting,'UTF8')),'hex'),
   p.email_login_allowed,p.password_login_allowed,p.second_factor_required,p.legacy_bcrypt_allowed,
   d.granted,encode(sha256(convert_to(d.source_reference,'UTF8')),'hex'),d.revision,l.login_name,
   encode(sha256(convert_to(l.password_hash,'UTF8')),'hex')) IS DISTINCT FROM
  ROW((f->>'account_id')::bigint,f->>'platform_role',(f->>'active')::boolean,(f->>'auth_version')::bigint,
   'personal'::text,0::smallint,(f->>'active')::boolean,f->>'username_sha256',f->>'display_name_sha256',
   f->>'email_sha256',f->>'setting_sha256',(f->>'email_login_allowed')::boolean,(f->>'password_login_allowed')::boolean,
   (f->>'second_factor_required')::boolean,(f->>'legacy_bcrypt_allowed')::boolean,(f->>'developer_access_granted')::boolean,
   f->>'developer_access_source_reference_sha256',(f->>'developer_access_revision')::bigint,
   CASE WHEN f->>'password_hash_sha256' IS NULL THEN NULL ELSE 'legacy-'||(f->>'user_id') END,f->>'password_hash_sha256')) THEN
  RAISE EXCEPTION 'identity current facts proof mismatch';
 END IF;
 IF EXISTS(SELECT 1 FROM jsonb_array_elements(payload->'receipts') r LEFT JOIN core_identity.users u ON u.id=(r->>'user_id')::bigint WHERE u.id IS NULL) THEN
  RAISE EXCEPTION 'orphan announcement receipt user';
 END IF;
 SELECT * INTO previous FROM core_migration.import_runs WHERE migration_id={run_id};
 replay := FOUND;
 IF replay THEN
  IF previous.source_archive_sha256<>{source_hash} OR previous.mapping_sha256<>{mapping_hash}
  OR previous.manifest-'applied_at'<>{manifest_json}::jsonb OR NOT previous.manifest ? 'applied_at' THEN
   RAISE EXCEPTION 'console import replay receipt mismatch';
  END IF;
  applied_at := (previous.manifest->>'applied_at')::timestamptz;
 ELSE
  IF EXISTS(SELECT 1 FROM core_console.options) OR EXISTS(SELECT 1 FROM core_console.announcement_reads) OR EXISTS(SELECT 1 FROM core_console.initialization) THEN
   RAISE EXCEPTION 'console target is not empty';
  END IF;
  applied_at := statement_timestamp();
  INSERT INTO core_console.options(key,value,revision,updated_at)
   SELECT r->>'key',r->>'value',1,applied_at FROM jsonb_array_elements(payload->'options') r;
  INSERT INTO core_console.announcement_reads(user_id,announcement_id,revision,read_at)
   SELECT (r->>'user_id')::bigint,(r->>'announcement_id')::bigint,r->>'revision',(r->>'read_at')::bigint FROM jsonb_array_elements(payload->'receipts') r;
  IF payload->'initialization'<>'null'::jsonb THEN
   INSERT INTO core_console.initialization(singleton,version,initialized_at)
    VALUES(true,payload->'initialization'->>'version',to_timestamp((payload->'initialization'->>'initialized_at')::bigint));
  END IF;
  INSERT INTO core_migration.import_runs(migration_id,source_archive_sha256,mapping_sha256,manifest)
   VALUES({run_id},{source_hash},{mapping_hash},{manifest_json}::jsonb||jsonb_build_object('applied_at',applied_at));
 END IF;
 IF (SELECT count(*) FROM core_console.options)<>jsonb_array_length(payload->'options')
 OR (SELECT count(*) FROM core_console.announcement_reads)<>jsonb_array_length(payload->'receipts')
 OR (SELECT count(*) FROM core_console.initialization)<>(CASE WHEN payload->'initialization'='null'::jsonb THEN 0 ELSE 1 END)
 OR EXISTS(SELECT 1 FROM jsonb_array_elements(payload->'options') r LEFT JOIN core_console.options o ON o.key=r->>'key'
   WHERE ROW(o.key,o.value,o.revision,o.updated_at) IS DISTINCT FROM ROW(r->>'key',r->>'value',1::bigint,applied_at))
 OR EXISTS(SELECT 1 FROM jsonb_array_elements(payload->'receipts') r LEFT JOIN core_console.announcement_reads a
   ON a.user_id=(r->>'user_id')::bigint AND a.announcement_id=(r->>'announcement_id')::bigint AND a.revision=r->>'revision'
   WHERE a.read_at IS DISTINCT FROM (r->>'read_at')::bigint)
 OR EXISTS(SELECT 1 FROM core_console.initialization i WHERE ROW(i.singleton,i.version,i.initialized_at) IS DISTINCT FROM
   ROW(true,payload->'initialization'->>'version',to_timestamp((payload->'initialization'->>'initialized_at')::bigint))) THEN
  RAISE EXCEPTION 'console exact readback mismatch';
 END IF;
 {_snapshot_sql('after_other')}
 IF before_other IS DISTINCT FROM after_other OR before_runs IS DISTINCT FROM
  (SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY migration_id),'[]'::jsonb) FROM core_migration.import_runs r WHERE migration_id<>{run_id}) THEN
  RAISE EXCEPTION 'non-console facts changed';
 END IF;
 SELECT ({catalog}) INTO actual_catalog;
 IF actual_catalog<>{literal(catalog_hash)} THEN RAISE EXCEPTION 'target catalog changed during import'; END IF;
 IF current_user<>{literal(target_identity['role'])} OR session_user<>{literal(expected_session_user)}
 OR (SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname=current_database())<>current_user{owner_login_guard} THEN
  RAISE EXCEPTION 'target effective owner or login identity changed';
 END IF;
 IF EXISTS(SELECT 1 FROM pg_stat_activity WHERE datid=(SELECT oid FROM pg_database WHERE datname=current_database())
  AND pid<>pg_backend_pid()) THEN
  RAISE EXCEPTION 'target has another active client';
 END IF;
END;
@DELIMITER@;
COMMIT;
"""
    # A source string may contain any dollar-quote token. Never let it terminate
    # the trusted DO block; choose a deterministic delimiter absent everywhere.
    nonce = 0
    while True:
        delimiter = "$lmm_console_" + hashlib.sha256((sql + str(nonce)).encode()).hexdigest()[:24] + "$"
        if delimiter not in sql:
            break
        nonce += 1
    # Replace only the two trusted structural positions, never a token occurring
    # in an original option value or receipt manifest.
    sql = sql.replace("DO @DELIMITER@\n", "DO " + delimiter + "\n", 1)
    return sql.removesuffix("@DELIMITER@;\nCOMMIT;\n") + delimiter + ";\nCOMMIT;\n"


def export_source(receipt: dict, output: Path) -> tuple[dict, dict, dict]:
    require(receipt.get("status") == "snapshot-restored-baseline-verified-frozen-and-classified", "source_receipt_status_invalid")
    archive = receipt.get("archive", {})
    archive_path = IDENTITY.private_backup_path(Path(archive.get("path", "")))
    require(IDENTITY.HEX.fullmatch(archive.get("sha256", "")) is not None
            and IDENTITY.sha256_file(archive_path) == archive["sha256"], "source_archive_hash_mismatch")
    reader, expected = receipt.get("readonly_reader", {}), receipt.get("source_instance_identity", {})
    require(not any(key in reader for key in ("login_role", "set_role")), "source_role_switch_rejected")
    require(all(reader.get(key) is True for key in ("verified_no_application_write_privileges",
            "verified_no_security_definer_application_function_execute", "row_security_off_guard_verified")), "source_reader_not_verified")
    schema = receipt.get("source", {}).get("schema", "")
    require(IDENTITY.IDENTIFIER.fullmatch(schema) is not None, "source_schema_invalid")
    paths = [Path(path) for path in receipt.get("private_metadata_files", []) if Path(path).name == "native-table-row-count-baseline.jsonl"]
    require(len(paths) == 1, "source_baseline_missing")
    baseline_path = IDENTITY.private_backup_path(paths[0])
    baseline = {}
    for line in baseline_path.read_text().splitlines():
        if line.startswith("{"):
            row = json.loads(line)
            name = row.get("table", "").removeprefix(schema + ".")
            if name in TABLES:
                require(name not in baseline and type(row.get("row_count")) is int and row["row_count"] >= 0, "source_baseline_invalid")
                baseline[name] = row["row_count"]
    require(set(baseline) == set(TABLES), "source_baseline_incomplete")
    sql = "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;\nSET LOCAL search_path=pg_catalog,public;\nSET LOCAL row_security=off;\nSET LOCAL statement_timeout='60s';\n"
    sql += "SELECT json_build_object('kind','identity','row',(" + IDENTITY.IDENTITY_SQL.removesuffix(";").removeprefix("SELECT ") + "));\n"
    sql += f"SELECT json_build_object('kind','schema','row',json_build_object('schema',nspname,'owner',pg_get_userbyid(nspowner))) FROM pg_namespace WHERE nspname={literal(schema)};\n"
    for table in TABLES:
        sql += f'SELECT json_build_object(\'kind\',\'count\',\'table\',{literal(table)},\'row\',json_build_object(\'count\',count(*),\'sha256\',({source_table_hash_sql(schema, table)}))) FROM "{schema}"."{table}";\n'
        where = " WHERE key IN (" + ",".join(map(literal, KEYS)) + ")" if table == "options" else ""
        sql += f'SELECT json_build_object(\'kind\',\'data\',\'table\',{literal(table)},\'row\',row_to_json(r)) FROM "{schema}"."{table}" r{where} ORDER BY row_to_json(r)::text COLLATE "C";\n'
    sql += "COMMIT;\n"
    raw = IDENTITY.psql(reader, sql, readonly=True, private_dir=output, label="source-console-export")
    raw_path = IDENTITY.write_private(output, "source-console-export.private.jsonl", raw)
    tables, counts, identities, schemas = {table: [] for table in TABLES}, {}, [], []
    for line in raw.splitlines():
        tagged = json.loads(line)
        kind, row = tagged.get("kind"), tagged.get("row")
        if kind == "identity":
            IDENTITY.verify_instance(row, reader, expected, source=True)
            identities.append(row)
        elif kind == "schema":
            require(row == {"schema": schema, "owner": expected.get("schema_owner")}, "source_schema_owner_mismatch")
            schemas.append(row)
        elif kind == "count":
            require(tagged.get("table") in TABLES and tagged["table"] not in counts, "source_count_framing_invalid")
            counts[tagged["table"]] = row
        else:
            require(kind == "data" and tagged.get("table") in TABLES and isinstance(row, dict), "source_export_shape_invalid")
            tables[tagged["table"]].append(row)
    require(len(identities) == len(schemas) == 1 and {key: value["count"] for key, value in counts.items()} == baseline, "source_baseline_mismatch")
    require(all(len(tables[key]) == baseline[key] for key in ("setups", "announcement_reads")), "source_export_completeness_mismatch")
    proof = {"source_archive_sha256": archive["sha256"], "source_export_sha256": IDENTITY.sha256_file(raw_path),
             "source_baseline_sha256": IDENTITY.sha256_file(baseline_path), "source_table_counts": baseline,
             "source_selected_row_hashes": {key: value["sha256"] for key, value in counts.items()}}
    IDENTITY.write_private(output, "source-console-proof.private.json", canonical(proof) + "\n")
    return identities[0], tables, proof


def verify_source_after(receipt: dict, source_identity: dict, proof: dict, output: Path,
                        *, label: str = "source-console-after") -> None:
    schema = receipt["source"]["schema"]
    sql = "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;\nSET LOCAL search_path=pg_catalog,public;\nSET LOCAL row_security=off;\nSET LOCAL statement_timeout='60s';\n" + IDENTITY.IDENTITY_SQL + "\n"
    for table in TABLES:
        sql += f'SELECT json_build_object(\'table\',{literal(table)},\'row_count\',count(*),\'sha256\',({source_table_hash_sql(schema, table)})) FROM "{schema}"."{table}";\n'
    sql += "COMMIT;\n"
    raw = IDENTITY.psql(receipt["readonly_reader"], sql, readonly=True, private_dir=output, label=label)
    rows = [json.loads(line) for line in raw.splitlines()]
    require(len(rows) == 4 and rows[0] == source_identity and {row["table"]: row["row_count"] for row in rows[1:]} == proof["source_table_counts"]
            and {row["table"]: row["sha256"] for row in rows[1:]} == proof["source_selected_row_hashes"], "source_changed_during_import")
    baselines = [Path(path) for path in receipt.get("private_metadata_files", []) if Path(path).name == "native-table-row-count-baseline.jsonl"]
    require(len(baselines) == 1 and IDENTITY.sha256_file(IDENTITY.private_backup_path(baselines[0])) == proof["source_baseline_sha256"], "source_baseline_changed_during_import")
    require(IDENTITY.sha256_file(IDENTITY.private_backup_path(Path(receipt["archive"]["path"]))) == proof["source_archive_sha256"], "source_archive_changed_during_import")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-receipt", type=Path, required=True)
    parser.add_argument("--target-receipt", type=Path, help="Private receipt: status=initialized-console-import-target; migration_id/source_archive_sha256; connection/instance_identity from owner; contract=current identity version/fingerprint, component_fingerprints, catalog_query_sha256, catalog_fingerprint_sha256, identity_import={migration_id,source_archive_sha256,mapping_sha256,manifest_sha256,source_target_id_map_sha256}; registry_provisioned=true; application_or_worker_started=false")
    parser.add_argument("--identity-manifest", type=Path, help="Required with target: reviewed private identity-v3 manifest; reads identity-mapping.private.json and source-target-id-map.private.jsonl in the same directory")
    parser.add_argument("--output", type=Path, required=True, help="New 0700 directory below ~/Backups")
    parser.add_argument("--migration-id", required=True)
    parser.add_argument("--apply", action="store_true")
    parser.add_argument("--confirm-source-sha256")
    args = parser.parse_args()
    output, attempted, manifest_persisted = None, False, False
    try:
        require(IDENTITY.MIGRATION_ID.fullmatch(args.migration_id) is not None, "migration_id_invalid")
        require(not args.target_receipt or args.identity_manifest is not None, "target_requires_identity_manifest")
        require(not args.apply or args.target_receipt is not None, "apply_requires_target_receipt")
        require(args.output.is_absolute() and args.output.resolve().is_relative_to(IDENTITY.BACKUP_ROOT.resolve()), "private_artifact_outside_backups")
        IDENTITY.private_backup_path(args.output.parent, directory=True)
        args.output.mkdir(mode=0o700)
        output = IDENTITY.private_backup_path(args.output, directory=True)
        source = IDENTITY.load_receipt(args.source_receipt)
        source_identity, tables, proof = export_source(source, output)
        source_hash = proof["source_archive_sha256"]
        plan = map_console(tables["options"], tables["setups"], tables["announcement_reads"], source_sha256=source_hash)
        IDENTITY.write_private(output, "console-mapping.private.json", canonical(plan) + "\n")
        contract = current_contract()
        identity_proof = load_identity_proof(args.identity_manifest, source_sha256=source_hash, contract=contract) if args.identity_manifest else None
        manifest = {"format_version": 1, "scope": "console-only", **proof,
                    "mapping_sha256": plan["mapping_sha256"], "summary": plan["summary"],
                    "target_source_hashes": contract["source_file_hashes"]}
        if identity_proof is not None:
            manifest["identity_artifact_sha256"] = identity_proof["input_file_hashes"]
        public = {"status": "dry-run-mapped", "migration_id": args.migration_id, "apply": False,
                  "source_archive_sha256": source_hash, "mapping_sha256": plan["mapping_sha256"], **plan["summary"]}
        if args.target_receipt:
            target = IDENTITY.load_receipt(IDENTITY.private_backup_path(args.target_receipt), private=True)
            require(target.get("status") == "initialized-console-import-target" and target.get("registry_provisioned") is True
                    and target.get("application_or_worker_started") is False, "target_receipt_status_invalid")
            require(target.get("migration_id") == args.migration_id and target.get("source_archive_sha256") == source_hash, "source_target_receipt_mismatch")
            manifest["identity_import"] = identity_reference(identity_proof, target.get("contract", {}).get("identity_import", {}).get("migration_id"))
            require(target.get("contract", {}).get("identity_import") == manifest["identity_import"], "identity_import_reference_mismatch")
            require(isinstance(target.get("instance_identity", {}).get("server_instance_nonce"), str)
                    and bool(target["instance_identity"]["server_instance_nonce"]), "target_instance_nonce_receipt_missing")
            connection = target.get("connection", {})
            target_identity = json.loads(IDENTITY.psql(connection, "BEGIN READ ONLY;\nSET LOCAL search_path=pg_catalog,public;\n" + IDENTITY.IDENTITY_SQL + "\nCOMMIT;", readonly=True, private_dir=output, label="target-console-identity"))
            IDENTITY.verify_instance(target_identity, connection, target.get("instance_identity", {}), source=False)
            require((target_identity["database_oid"], target_identity["postmaster_started_at"]) != (source_identity["database_oid"], source_identity["postmaster_started_at"]), "source_target_same_database")
            for key in ("datcollate", "datctype", "datcollversion"):
                require(target_identity.get(key) == source_identity.get(key), "source_target_collation_mismatch")
            sql = build_import_sql(plan, migration_id=args.migration_id, target_contract=target.get("contract", {}),
                                   manifest=manifest, contract=contract, target_identity=target_identity,
                                   identity_proof=identity_proof, target_connection=connection)
            IDENTITY.write_private(output, "console-import.private.sql", sql)
            # Keep immutable replay material before a possible COMMIT-ACK loss.
            IDENTITY.write_private(output, "manifest.private.json", canonical(manifest) + "\n")
            manifest_persisted = True
            public["status"] = "dry-run-sql-plan-ready"
            if args.apply:
                require(args.confirm_source_sha256 == source_hash, "apply_source_confirmation_missing")
                require(current_contract() == contract, "target_source_contract_changed")
                require(all(IDENTITY.sha256_file(IDENTITY.private_backup_path(Path(path))) == digest
                            for path, digest in identity_proof["input_file_hashes"].items()), "identity_artifacts_changed_during_import")
                verify_source_after(source, source_identity, proof, output, label="source-console-before")
                attempted = True
                raw = IDENTITY.psql(connection, sql, readonly=False, private_dir=output, label="target-console-import")
                IDENTITY.write_private(output, "target-console-import.private.txt", raw)
                public.update(status="console-import-verified", apply=True)
        else:
            require(not args.apply, "apply_requires_target_receipt")
        verify_source_after(source, source_identity, proof, output)
        require(current_contract() == contract, "target_source_contract_changed")
        if identity_proof is not None:
            require(all(IDENTITY.sha256_file(IDENTITY.private_backup_path(Path(path))) == digest
                        for path, digest in identity_proof["input_file_hashes"].items()), "identity_artifacts_changed_during_import")
        if not manifest_persisted:
            IDENTITY.write_private(output, "manifest.private.json", canonical(manifest) + "\n")
        IDENTITY.write_private(output, "report.private.json", canonical(public) + "\n")
        print(canonical(public))
        return 0
    except (MappingError, OSError, ValueError, KeyError, TypeError, RecursionError) as error:
        code = str(error) if isinstance(error, MappingError) else type(error).__name__
        failure = {"status": "apply-outcome-unconfirmed" if attempted else "aborted", "error_code": code}
        if output is not None:
            try:
                IDENTITY.write_private(output, "failure.private.json", canonical(failure) + "\n")
            except OSError:
                pass
        print(canonical(failure), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
