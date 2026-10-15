#!/usr/bin/env python3
"""Offline identity-only import from an owned, immutable PostgreSQL snapshot.

Run through mise. Dry-run is the default. Receipts describe local Unix sockets;
this tool never connects over TCP, creates a schema, or imports funds, sessions,
API keys, OAuth links, passkeys, or second-factor secrets. Private artifacts must
stay below ~/Backups. See --help for the explicit target receipt contract.

For a NOLOGIN target owner, connection.role is the effective owner, and the
explicit pair login_role/set_role names the actual LOGIN member and that same
owner. Startup PGOPTIONS performs the role switch; both session_user and the
effective owner are checked. Source readers cannot provide either field.

Replay is strict and only valid before console or other business imports. An
unconfirmed commit keeps its original migration ID and exact private materials.
A missing registry row does not prove rollback while the original session may
still run: wait for that session to end and fence with advisory lock 741803121
before resolving absence. Never switch IDs or automatically retry a failed SQL
transaction (including a serialization failure).
"""

from __future__ import annotations

import argparse
import base64
from collections import Counter
from datetime import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
from typing import Any


BACKUP_ROOT = Path.home() / "Backups"
# Bound the local client as well as connection setup. A write timeout does not
# establish rollback: retain the run ID/materials and resolve the original session.
PSQL_CLIENT_TIMEOUT_SECONDS = 600
READONLY_PSQL_TIMEOUT_OPTIONS = (
    " -c statement_timeout=300000 -c lock_timeout=5000"
    " -c idle_in_transaction_session_timeout=60000"
)
HEX = re.compile(r"[0-9a-f]{64}\Z")
IDENTIFIER = re.compile(r"[a-z_][a-z_0-9]*\Z")
BCRYPT = re.compile(r"\$2[aby]\$(0[4-9]|1[0-6])\$[./A-Za-z0-9]{53}\Z")
MIGRATION_ID = re.compile(r"[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}\Z")
# This deliberately accepts only the format the Rust verifier currently uses.
ARGON2 = re.compile(r"\$argon2id\$v=19\$m=19456,t=2,p=1\$([A-Za-z0-9+/]+)\$([A-Za-z0-9+/]+)\Z")
TABLES = ("accounts", "users", "user_profiles", "password_logins", "developer_access")
IDENTITY_VERSION = 4
# Bind this importer to the reviewed DDL, rather than trusting a caller's hash.
IDENTITY_FINGERPRINT = "1fe8c54a0afce5d50e322d41dad405b7676f81983e720d5412bf893e40636ac8"
IDENTITY_SCHEMA = Path(__file__).resolve().parents[1] / "services/core/schema/identity.sql"
IDENTITY_SCHEMA_RUST = Path(__file__).resolve().parents[1] / "services/core/src/identity/schema.rs"
ROOT = Path(__file__).resolve().parents[1]
CORE_SEED_CONTRACT = "ledger-zero-events-empty-routing-unset-abuse-default-oauth-contract-v3"
CORE_COMPONENTS = frozenset({
    "identity", "oauth-grants", "ledger", "payments", "charging", "routing",
    "events", "charging-ledger", "migration", "console", "model-gateway",
    "commerce", "abuse",
})
DEVELOPER_POLICY = "legacy-explicit-override-before-manual-v1-paid-unproven"


class MappingError(Exception):
    """A safe machine-readable error. Never put a source value in this message."""


def require(condition: bool, code: str) -> None:
    if not condition:
        raise MappingError(code)


def canonical(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def identity_contract() -> dict:
    """Only the reviewed schema/version may generate or execute an import."""
    versions = re.findall(r"^const VERSION: i32 = ([0-9]+);$", IDENTITY_SCHEMA_RUST.read_text(), re.MULTILINE)
    require(versions == [str(IDENTITY_VERSION)], "identity_source_version_mismatch")
    fingerprint = sha256_file(IDENTITY_SCHEMA)
    require(fingerprint == IDENTITY_FINGERPRINT, "identity_source_fingerprint_mismatch")
    return {"identity_version": IDENTITY_VERSION, "identity_fingerprint_sha256": fingerprint}


def current_contract() -> dict:
    """Extract the exact assembled installer; unknown syntax fails closed."""
    expected = identity_contract()
    path = ROOT / "services/core/src/core_schema.rs"
    source = path.read_text(encoding="utf-8")
    match = re.search(r"const COMPONENTS:\s*&\[\(&str,\s*&str\)\]\s*=\s*&\[(.*?)\];", source, re.S)
    require(match is not None, "component_contract_syntax_unknown")
    entries = re.compile(r'\(\s*"([a-z][a-z-]*)"\s*,\s*include_str!\("(\.\./schema/[a-z_]+\.sql)"\)\s*,?\s*\)')
    require(re.fullmatch(r"[\s,]*", entries.sub("", match[1])) is not None, "component_contract_syntax_unknown")
    components = {}
    for entry in entries.finditer(match[1]):
        name, relative = entry.groups()
        schema = (path.parent / relative).resolve()
        require(schema.parent == (ROOT / "services/core/schema").resolve() and name not in components, "component_contract_invalid")
        components[name] = sha256_file(schema)
    require(components.get("identity") == expected["identity_fingerprint_sha256"]
            and set(components) == CORE_COMPONENTS, "component_contract_incomplete")
    catalogs = re.findall(r'const CATALOG:\s*&str\s*=\s*r#"(.*?)"#;', source, re.S)
    require(len(catalogs) == 1, "catalog_contract_syntax_unknown")
    return {**expected, "component_fingerprints": components,
            "catalog_query_sha256": hashlib.sha256(catalogs[0].encode()).hexdigest(), "catalog_query": catalogs[0],
            "oauth_catalog_query_sha256": hashlib.sha256(oauth_catalog_query().encode()).hexdigest()}


def catalog_hash_sql(contract: dict) -> str:
    # Match Rust's UTF-8 definitions, each prefixed with its 8-byte BE length.
    return "SELECT encode(sha256(coalesce(string_agg(int8send(octet_length(convert_to(definition,'UTF8'))::bigint)||convert_to(definition,'UTF8'),''::bytea ORDER BY definition COLLATE \"C\"),''::bytea)),'hex') FROM (" + contract["catalog_query"] + ") catalog_definitions"


def oauth_catalog_query() -> str:
    source = (ROOT / "services/core/src/identity/grants.rs").read_text(encoding="utf-8")
    catalogs = re.findall(r'const CATALOG:\s*&str\s*=\s*r#"(.*?)"#;', source, re.S)
    require(len(catalogs) == 1, "oauth_catalog_contract_syntax_unknown")
    require('VALUES (1,$1,$2)' in source, "oauth_seed_version_unknown")
    return catalogs[0]


def oauth_seed_guard_sql(contract: dict) -> str:
    """Validate the installer-written OAuth receipt, including its actual catalog."""
    catalog = oauth_catalog_query()
    require(hashlib.sha256(catalog.encode()).hexdigest() == contract.get("oauth_catalog_query_sha256"),
            "oauth_catalog_contract_changed")
    # grants::catalog uses the same UTF-8, 8-byte length-prefixed digest as Core,
    # with its sole output column named d. The complete Core catalog is checked too.
    query = "SELECT d AS definition FROM (" + catalog + ") oauth_definitions"
    actual = catalog_hash_sql({"catalog_query": query})
    fingerprint = sql_literal(contract["component_fingerprints"]["oauth-grants"])
    return ("(SELECT count(*) FROM core_oauth.contract)<>1 OR NOT EXISTS("
            "SELECT 1 FROM core_oauth.contract WHERE singleton AND version=1 "
            "AND encode(ddl_fingerprint,'hex')=" + fingerprint +
            " AND encode(catalog_fingerprint,'hex')=(" + actual + "))")


def id_map_bytes(records: list[dict]) -> str:
    return "".join(canonical({"source_user_id": row["user_id"], "target_user_id": row["user_id"],
                              "target_personal_account_id": row["account_id"]}) + "\n" for row in records)


def validate_import_material(plan: dict, source_sha256: str, manifest: dict) -> None:
    records = plan.get("records")
    require(isinstance(records, list) and all(isinstance(row, dict) for row in records), "mapping_shape_invalid")
    require(hashlib.sha256(canonical({"source_sha256": source_sha256, "records": records}).encode()).hexdigest() == plan.get("mapping_sha256"), "mapping_hash_mismatch")
    ids = [positive_int(row.get("user_id"), "mapped_user_id_invalid") for row in records]
    require(ids == sorted(set(ids)) and [row.get("account_id") for row in records] == list(range(1, len(records) + 1)), "id_map_invalid")
    require(isinstance(manifest, dict) and manifest.get("format_version") == 2 and type(manifest.get("format_version")) is int
            and manifest.get("scope") == "identity-only" and manifest.get("source_archive_sha256") == source_sha256
            and manifest.get("identity_contract") == identity_contract() and manifest.get("developer_access_policy") == DEVELOPER_POLICY
            and manifest.get("initialized_core_seed_contract") == CORE_SEED_CONTRACT
            and manifest.get("mapping_sha256") == plan["mapping_sha256"] and manifest.get("summary") == plan.get("summary")
            and manifest.get("source_target_id_map_sha256") == hashlib.sha256(id_map_bytes(records).encode()).hexdigest(), "identity_manifest_mismatch")
    require(all(isinstance(manifest.get(field), str) and HEX.fullmatch(manifest[field]) for field in
                ("source_export_sha256", "source_baseline_proof_sha256")), "identity_manifest_proof_invalid")


def developer_decision(source: dict) -> tuple[bool, str]:
    """Mirror proven main facts; neither a balance nor generic paid status proves access.

    Main grants administrators access, then uses an explicit trust override
    before manual activation for ordinary users. Invalid or missing source
    policy fields fail closed. Paid activation needs its own authoritative
    net-credit/refund projection and is deliberately not inferred here.
    """
    role = source.get("role")
    if type(role) is not int or role not in (1, 10, 100):
        return False, "unknown_role_fail_closed"
    if role in (10, 100):
        return True, "admin_role_dormant"
    if "trust_level_override" not in source:
        return False, "override_source_missing"
    override = source["trust_level_override"]
    if override is not None:
        if type(override) is not int or override not in range(5):
            return False, "invalid_override_fail_closed"
        return (override > 0, "explicit_override_allow" if override else "explicit_override_deny")
    if "console_activated_at" not in source:
        return False, "manual_activation_source_missing"
    activated_at = source["console_activated_at"]
    if type(activated_at) is not int or not 0 <= activated_at <= 9223372036854775807:
        return False, "invalid_manual_activation_fail_closed"
    if activated_at > 0:
        return True, "approved_console_activation"
    return False, "paid_activation_unproven"


def positive_int(value: Any, code: str) -> int:
    require(type(value) is int and 0 < value <= 9223372036854775807, code)
    return value


def safe_argon2(password: Any) -> bool:
    """Only the current Rust default profile; never trust arbitrary PHC costs."""
    if not isinstance(password, str):
        return False
    match = ARGON2.fullmatch(password)
    if match is None:
        return False
    try:
        decoded = [base64.b64decode(part + "=" * (-len(part) % 4), validate=True) for part in match.groups()]
        return [len(part) for part in decoded] == [16, 32]
    except ValueError:
        return False


def text(value: Any, maximum: int, code: str, default: str | None = None) -> tuple[str, bool]:
    normalized = value is None and default is not None
    if normalized:
        value = default
    require(isinstance(value, str) and len(value) <= maximum and "\0" not in value, code)
    return value, normalized


def map_identity(users: list[dict], second_factors: list[dict], *, source_sha256: str,
                 password_login_enabled: bool = True) -> dict:
    """Pure deterministic mapper. Source dictionaries are never modified.

    Role/status uncertainty blocks an account instead of granting authority.
    All privileged accounts are blocked pending granular permission equivalence.
    Email verification cannot be inferred from a nonempty email, so email login
    is disabled. Service level remains zero. Developer access preserves only
    explicit override/manual facts, never an inferred paid entitlement. The
    original rows are preserved by the caller.
    """
    require(isinstance(source_sha256, str) and HEX.fullmatch(source_sha256) is not None, "source_hash_invalid")
    require(type(password_login_enabled) is bool, "password_login_policy_unknown")
    require(isinstance(users, list) and isinstance(second_factors, list), "source_shape_invalid")
    indexed = {}
    for user in users:
        require(isinstance(user, dict), "user_shape_invalid")
        user_id = positive_int(user.get("id"), "user_id_invalid")
        require(user_id not in indexed, "duplicate_user_id")
        indexed[user_id] = user
    factors: dict[int, bool] = {}
    unknown_factors: set[int] = set()
    for factor in second_factors:
        require(isinstance(factor, dict), "second_factor_shape_invalid")
        user_id = positive_int(factor.get("user_id"), "second_factor_user_id_invalid")
        require(user_id in indexed, "orphan_second_factor")
        require(type(factor.get("enabled")) is bool, "second_factor_policy_unknown")
        require(user_id not in factors, "duplicate_second_factor")
        factors[user_id] = factor["enabled"]
        if factor.get("policy_unknown"):
            unknown_factors.add(user_id)
    records, names, reasons, normalizations, developer_reasons = [], set(), Counter(), Counter(), Counter()
    for account_id, (user_id, source) in enumerate(sorted(indexed.items()), 1):
        username, _ = text(source.get("username"), 128, "username_invalid")
        require(bool(username.strip()), "username_invalid")
        require(username not in names, "duplicate_username")
        names.add(username)
        profile = {}
        normalized = []
        for field, limit, default in (("display_name", 256, ""), ("email", 320, ""), ("setting", 65536, "{}")):
            profile[field], changed = text(source.get(field), limit, field + "_invalid", default)
            if changed:
                normalized.append(field + "_null_default")
        auth_version = positive_int(source.get("auth_version"), "auth_version_invalid")
        blocked = []
        role = source.get("role")
        platform_role = {1: "user", 10: "admin", 100: "superadmin"}.get(role) if type(role) is int else None
        if platform_role is None:
            platform_role = "user"
            blocked.append("unknown_role")
        if role in (10, 100) and type(role) is int:
            blocked.append("privileged_permissions_unmapped")
        status = source.get("status")
        if type(status) is not int or status not in (1, 2):
            blocked.append("unknown_status")
        elif status == 2:
            blocked.append("source_disabled")
        if source.get("deleted_at") not in (None, ""):
            blocked.append("source_deleted")
        active = not blocked
        second_factor = factors.get(user_id, False) or user_id in unknown_factors
        if second_factor:
            blocked.append("second_factor_unimplemented")
        if user_id in unknown_factors:
            blocked.append("second_factor_policy_unknown")
        password = source.get("password")
        require(password is None or (isinstance(password, str) and "\0" not in password), "password_source_invalid")
        legacy_bcrypt = isinstance(password, str) and BCRYPT.fullmatch(password) is not None
        known_argon2 = safe_argon2(password)
        verifier = password if legacy_bcrypt or known_argon2 else None
        if verifier is None:
            blocked.append("password_absent" if password in (None, "") else "password_format_unsupported")
        if not password_login_enabled:
            blocked.append("source_password_login_disabled")
        password_allowed = active and not second_factor and verifier is not None and password_login_enabled
        developer_granted, developer_reason = developer_decision(source)
        record = {
            "user_id": user_id, "account_id": account_id, "platform_role": platform_role,
            "active": active, "auth_version": auth_version, "username": username,
            **profile, "email_login_allowed": False, "password_login_allowed": password_allowed,
            "second_factor_required": second_factor, "legacy_bcrypt_allowed": legacy_bcrypt,
            "password_hash": verifier, "blocked_reasons": sorted(blocked), "normalizations": normalized,
            "developer_access_granted": developer_granted,
            "developer_access_source_reference": f"legacy:{source_sha256}:{developer_reason}",
            "developer_access_revision": 1, "developer_access_reason": developer_reason,
        }
        records.append(record)
        reasons.update(blocked)
        normalizations.update(normalized)
        developer_reasons.update([developer_reason])
    digest = hashlib.sha256(canonical({"source_sha256": source_sha256, "records": records}).encode()).hexdigest()
    return {"records": records, "mapping_sha256": digest, "summary": {
        "source_user_count": len(users), "mapped_user_count": len(records), "dropped_user_count": 0,
        "active_user_count": sum(row["active"] for row in records),
        "password_login_allowed_count": sum(row["password_login_allowed"] for row in records),
        "password_verifier_count": sum(row["password_hash"] is not None for row in records),
        "blocked_user_count": sum(bool(row["blocked_reasons"]) for row in records),
        "blocked_reason_counts": dict(sorted(reasons.items())), "normalization_counts": dict(sorted(normalizations.items())),
        "email_login_allowed_count": 0, "sessions_imported": 0, "api_keys_imported": 0,
        "financial_rows_written": 0,
        "developer_access_granted_count": sum(row["developer_access_granted"] for row in records),
        "developer_access_denied_count": sum(not row["developer_access_granted"] for row in records),
        "developer_access_reason_counts": dict(sorted(developer_reasons.items())),
        "developer_access_policy": DEVELOPER_POLICY,
        "functional_gaps": ["legacy_email_verification_unproven", "legacy_service_levels_unmapped",
                            "legacy_paid_activation_unproven", "legacy_granular_admin_permissions_unmapped",
                            "legacy_oauth_and_passkeys_archived_only", "financial_data_not_migrated"],
    }}


def owned_path(path: Path, *, private: bool, directory: bool = False) -> Path:
    require(not path.is_symlink(), "path_symlink_rejected")
    info = path.stat()
    require(info.st_uid == os.getuid(), "path_owner_mismatch")
    require(stat.S_ISDIR(info.st_mode) if directory else stat.S_ISREG(info.st_mode), "path_type_invalid")
    require(info.st_mode & (0o077 if private else 0o022) == 0, "path_permissions_unsafe")
    return path


def private_backup_path(path: Path, *, directory: bool = False) -> Path:
    root = BACKUP_ROOT.resolve()
    require(path.is_absolute() and path.resolve().is_relative_to(root) and path.resolve() != root, "private_artifact_outside_backups")
    # Check every component below Backups, not just the final path.
    for parent in reversed(path.parents):
        if parent != root and parent.is_relative_to(root):
            owned_path(parent, private=True, directory=True)
    return owned_path(path, private=True, directory=directory)


def write_private(directory: Path, name: str, value: str) -> Path:
    path = directory / name
    # Never replace an existing preview, even if a migration ID is reused.
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        stream.write(value)
        stream.flush()
        os.fsync(stream.fileno())
    return path


def load_receipt(path: Path, private: bool = False) -> dict:
    owned_path(path, private=private)
    with path.open() as stream:
        receipt = json.load(stream)
    require(isinstance(receipt, dict), "receipt_shape_invalid")
    return receipt


def socket_connection(connection: dict) -> dict:
    require(isinstance(connection, dict), "connection_receipt_invalid")
    socket_dir = Path(connection.get("socket_directory", ""))
    require(socket_dir.is_absolute(), "tcp_connection_rejected")
    owned_path(socket_dir, private=True, directory=True)
    port = connection.get("port")
    require(type(port) is int and 1 <= port <= 65535, "socket_port_missing_or_invalid")
    socket_file = socket_dir / (".s.PGSQL." + str(port))
    require(not socket_file.is_symlink(), "socket_symlink_rejected")
    socket_stat = socket_file.stat()
    require(stat.S_ISSOCK(socket_stat.st_mode) and socket_stat.st_uid == os.getuid(), "socket_owner_mismatch")
    pgpass = private_backup_path(Path(connection.get("pgpass_file", "")))
    require(IDENTIFIER.fullmatch(connection.get("database", "")) is not None, "database_name_invalid")
    _, login = connection_roles(connection)
    return {"PGHOST": str(socket_dir), "PGPORT": str(port), "PGDATABASE": connection["database"],
            "PGUSER": login, "PGPASSFILE": str(pgpass)}


def connection_roles(connection: dict) -> tuple[str, str]:
    owner = connection.get("role", "")
    require(isinstance(owner, str) and IDENTIFIER.fullmatch(owner) is not None, "database_role_invalid")
    require(("login_role" in connection) == ("set_role" in connection), "target_login_role_pair_required")
    login = connection.get("login_role", owner)
    require(isinstance(login, str) and IDENTIFIER.fullmatch(login) is not None, "database_login_role_invalid")
    if "set_role" in connection:
        require(connection["set_role"] == owner and login != owner, "target_set_role_invalid")
    return owner, login


def psql_options(connection: dict, *, readonly: bool) -> str:
    options = "-c default_transaction_read_only=" + ("on" if readonly else "off")
    if readonly:
        options += READONLY_PSQL_TIMEOUT_OPTIONS
    if "login_role" in connection or "set_role" in connection:
        owner, _ = connection_roles(connection)
        options += " -c role=" + owner
    return options


def psql(connection: dict, sql: str, *, readonly: bool, private_dir: Path, label: str) -> str:
    variables = socket_connection(connection)
    # Do not inherit PG* DSNs, service files, startup SQL, or ambient libpq options.
    environment = {key: value for key, value in os.environ.items() if not key.startswith("PG")}
    environment.update(variables)
    environment["PGCONNECT_TIMEOUT"] = "5"
    environment["PGAPPNAME"] = "lmm-offline-identity-import"
    environment["PGOPTIONS"] = psql_options(connection, readonly=readonly)
    try:
        result = subprocess.run(["psql", "-X", "--no-password", "-qAt", "-v", "ON_ERROR_STOP=1"],
                                input=sql, text=True, capture_output=True, env=environment, check=False,
                                timeout=PSQL_CLIENT_TIMEOUT_SECONDS)
    except subprocess.TimeoutExpired as error:
        # TimeoutExpired may carry bytes even with text=True. Preserve partial
        # diagnostics only in private files; never echo SQL or source values.
        def partial_text(value: str | bytes | None) -> str:
            return value.decode("utf-8", errors="replace") if isinstance(value, bytes) else value or ""

        write_private(private_dir, label + ".stdout.private.txt", partial_text(error.stdout))
        write_private(private_dir, label + ".stderr.private.txt", partial_text(error.stderr))
        suffix = "_database_command_timed_out" if readonly else "_database_command_timed_out_result_unconfirmed"
        raise MappingError(label + suffix) from None
    write_private(private_dir, label + ".stderr.private.txt", result.stderr)
    require(result.returncode == 0, label + "_database_command_failed")
    return result.stdout


IDENTITY_SQL = """SELECT json_build_object(
 'database',current_database(),'role',current_user,'session_user',session_user,
 'effective_role_can_login',(SELECT rolcanlogin FROM pg_roles WHERE rolname=current_user),
 'database_oid',(SELECT oid FROM pg_database WHERE datname=current_database()),
 'database_owner',(SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname=current_database()),
 'server_version_num',current_setting('server_version_num'),
 'postmaster_started_at',pg_postmaster_start_time()::text,
 'server_instance_nonce',current_setting('lmm.migration_instance_nonce',true),
 'port',current_setting('port')::int,
 'tcp_connection',inet_server_addr() IS NOT NULL,
 'read_only',current_setting('transaction_read_only'),
 'datcollate',(SELECT datcollate FROM pg_database WHERE datname=current_database()),
 'datctype',(SELECT datctype FROM pg_database WHERE datname=current_database()),
 'datcollversion',(SELECT datcollversion FROM pg_database WHERE datname=current_database()));"""


def verify_instance(identity: dict, connection: dict, expected: dict, *, source: bool) -> None:
    if source:
        require("login_role" not in connection and "set_role" not in connection, "source_role_switch_forbidden")
    require(isinstance(expected, dict), "instance_receipt_incomplete")
    expected = dict(expected)
    if "postmaster_started_at" not in expected:
        expected["postmaster_started_at"] = expected.get("postgres_start_time")
    require(identity.get("database") == connection["database"] and identity.get("role") == connection["role"], "database_identity_mismatch")
    # unix_socket_directories is a privileged setting. The owner attests the
    # offline listener in the receipt; libpq uses our checked owned Unix socket.
    listeners = ("",) if source else ("", "127.0.0.1")
    require(identity.get("tcp_connection") is False and expected.get("listen_addresses") in listeners, "database_not_offline")
    require(identity.get("port") == connection["port"], "database_socket_mismatch")
    require(isinstance(expected, dict) and all(key in expected for key in ("database_oid", "postmaster_started_at", "server_version_num")), "instance_receipt_incomplete")
    for key in ("database_oid", "server_version_num"):
        require(str(identity.get(key)) == str(expected[key]), "instance_receipt_mismatch")
    # PostgreSQL JSON timestamps use T/+HH:MM; ::text uses space/+HH.
    require(datetime.fromisoformat(identity["postmaster_started_at"]) == datetime.fromisoformat(expected["postmaster_started_at"]), "instance_receipt_mismatch")
    if source:
        require(identity.get("read_only") == "on", "source_not_readonly")
    else:
        require(expected.get("database_owner") == identity.get("database_owner") == connection["role"], "target_owner_mismatch")
        if "login_role" in connection or "set_role" in connection:
            owner, login = connection_roles(connection)
            require(identity.get("session_user") == login and identity.get("role") == owner
                    and identity.get("effective_role_can_login") is False, "target_session_role_mismatch")
        if expected.get("server_instance_nonce") is not None:
            # A custom GUC is an extra marker, not cryptographic attestation;
            # retain the independent socket/owner/OID/start/version checks.
            require(identity.get("server_instance_nonce") == expected["server_instance_nonce"], "target_instance_nonce_mismatch")


def export_source(receipt: dict, output: Path) -> tuple[dict, list[dict], list[dict], bool]:
    require(receipt.get("status") == "snapshot-restored-baseline-verified-frozen-and-classified", "source_receipt_status_invalid")
    archive = receipt.get("archive", {})
    archive_path = private_backup_path(Path(archive.get("path", "")))
    require(HEX.fullmatch(archive.get("sha256", "")) is not None and sha256_file(archive_path) == archive["sha256"], "source_archive_hash_mismatch")
    reader = receipt.get("readonly_reader", {})
    require("login_role" not in reader and "set_role" not in reader, "source_role_switch_forbidden")
    require(reader.get("verified_no_application_write_privileges") is True and reader.get("verified_no_security_definer_application_function_execute") is True and reader.get("row_security_off_guard_verified") is True, "source_reader_not_verified")
    schema = receipt.get("source", {}).get("schema", "")
    require(IDENTIFIER.fullmatch(schema) is not None, "source_schema_invalid")
    baselines = [Path(path) for path in receipt.get("private_metadata_files", [])
                 if Path(path).name == "native-table-row-count-baseline.jsonl"]
    require(len(baselines) == 1, "source_row_baseline_missing")
    baseline_path = private_backup_path(baselines[0])
    baseline_users = []
    for line in baseline_path.read_text().splitlines():
        if not line.startswith("{"):
            continue  # psql transaction status lines are not metadata rows.
        row = json.loads(line)
        if row.get("table") in ("users", schema + ".users"):
            baseline_users.append(row.get("row_count"))
    require(len(baseline_users) == 1 and type(baseline_users[0]) is int and baseline_users[0] >= 0, "source_row_baseline_invalid")
    # The snapshot owner records this identity after creating the readonly role.
    expected = dict(receipt.get("source_instance_identity", {}))
    identity = json.loads(psql(reader, "BEGIN READ ONLY;\n" + IDENTITY_SQL + "\nCOMMIT;", readonly=True, private_dir=output, label="source-identity"))
    verify_instance(identity, reader, expected, source=True)
    # Fetch complete original user objects, not a lossy selected-field projection.
    # Secrets in these private objects must never reach stdout, fixtures or Git.
    sql = f"""BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL row_security=off;
SELECT json_build_object('kind','identity','row',({IDENTITY_SQL.removesuffix(';').removeprefix('SELECT ')}));
SELECT json_build_object('kind','schema_identity','row',json_build_object('schema',nspname,'schema_owner',pg_get_userbyid(nspowner))) FROM pg_namespace WHERE nspname='{schema}';
SELECT json_build_object('kind','user_count','row',count(*)) FROM "{schema}".users;
SELECT json_build_object('kind','user','row',row_to_json(u)) FROM "{schema}".users u ORDER BY id;
SELECT json_build_object('kind','second_factor','row',json_build_object('user_id',user_id,'is_enabled',is_enabled,'deleted_at',deleted_at)) FROM "{schema}".two_fas ORDER BY user_id;
SELECT json_build_object('kind','password_policy','row',json_build_object('key',key,'value',value)) FROM "{schema}".options WHERE key='PasswordLoginEnabled';
COMMIT;
"""
    raw = psql(reader, sql, readonly=True, private_dir=output, label="source-export")
    write_private(output, "source-identity-export.private.jsonl", raw)
    users, factors, policies, counts = [], [], [], []
    identities, schemas = 0, 0
    for line in raw.splitlines():
        tagged = json.loads(line)
        kind, row = tagged["kind"], tagged["row"]
        if kind == "identity":
            identities += 1
            verify_instance(row, reader, expected, source=True)
            require(row == identity, "source_instance_changed_during_export")
        elif kind == "schema_identity":
            schemas += 1
            require(row.get("schema") == schema and row.get("schema_owner") == expected.get("schema_owner"), "source_schema_owner_mismatch")
        elif kind == "user_count":
            counts.append(row)
        elif kind == "user":
            users.append(row)
        elif kind == "password_policy":
            policies.append(row)
        elif kind == "second_factor" and row["deleted_at"] is None:
            # An unknown MFA flag never silently becomes false.
            factors.append({"user_id": row["user_id"], "enabled": row["is_enabled"] is not False,
                            "policy_unknown": type(row["is_enabled"]) is not bool})
    require(identities == 1 and schemas == 1 and counts == [len(users)], "source_export_completeness_mismatch")
    require(counts == baseline_users, "source_snapshot_baseline_mismatch")
    require(len(policies) <= 1, "duplicate_global_password_policy")
    password_enabled = not policies or policies[0]["value"] == "true"
    write_private(output, "source-instance.private.json", canonical(identity) + "\n")
    write_private(output, "source-baseline-proof.private.json", canonical({
        "row_baseline_sha256": sha256_file(baseline_path), "source_user_row_count_matches_snapshot_baseline": True,
    }) + "\n")
    return identity, users, factors, password_enabled


def sql_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def build_import_sql(plan: dict, *, migration_id: str, source_sha256: str,
                     target_contract: dict, manifest: dict, target_identity: dict) -> str:
    require(MIGRATION_ID.fullmatch(migration_id) is not None, "migration_id_invalid")
    require(HEX.fullmatch(source_sha256) is not None, "source_hash_invalid")
    expected_contract = identity_contract()
    require(isinstance(target_contract, dict) and all(target_contract.get(key) == value for key, value in expected_contract.items()), "target_contract_invalid")
    require(type(target_contract.get("identity_version")) is int, "target_contract_invalid")
    contract = current_contract()
    for key in ("component_fingerprints", "catalog_query_sha256", "oauth_catalog_query_sha256"):
        require(target_contract.get(key) == contract[key], "target_assembled_contract_invalid")
    catalog_hash = target_contract.get("catalog_fingerprint_sha256", "")
    require(isinstance(catalog_hash, str) and HEX.fullmatch(catalog_hash) is not None, "target_catalog_hash_invalid")
    validate_import_material(plan, source_sha256, manifest)
    require(isinstance(target_identity, dict), "target_identity_incomplete")
    target_identity = dict(target_identity)
    # PostgreSQL's JSON encoder represents oid as a decimal string.
    if isinstance(target_identity.get("database_oid"), str) and target_identity["database_oid"].isascii() and target_identity["database_oid"].isdigit():
        target_identity["database_oid"] = int(target_identity["database_oid"])
    for key in ("database", "role", "database_owner", "postmaster_started_at", "server_instance_nonce"):
        require(isinstance(target_identity.get(key), str) and bool(target_identity[key]) and "\0" not in target_identity[key], "target_identity_incomplete")
    require(IDENTIFIER.fullmatch(target_identity["database"]) is not None and IDENTIFIER.fullmatch(target_identity["role"]) is not None
            and target_identity["database_owner"] == target_identity["role"], "target_identity_invalid")
    login = target_identity.get("session_user", target_identity["role"])
    require(isinstance(login, str) and IDENTIFIER.fullmatch(login) is not None, "target_login_identity_invalid")
    role_guard = f"""
 IF current_user<>{sql_literal(target_identity['role'])} OR session_user<>{sql_literal(login)}
 OR (SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname=current_database())<>{sql_literal(target_identity['role'])}
 {"OR EXISTS(SELECT 1 FROM pg_roles WHERE rolname=" + sql_literal(target_identity['role']) + " AND rolcanlogin)" if login != target_identity['role'] else ""} THEN
  RAISE EXCEPTION 'target session or effective owner mismatch';
 END IF;
"""
    positive_int(target_identity.get("database_oid"), "target_identity_invalid")
    require(type(target_identity.get("port")) is int and 0 < target_identity["port"] <= 65535
            and str(target_identity.get("server_version_num", "")).isdigit(), "target_identity_invalid")
    datetime.fromisoformat(target_identity["postmaster_started_at"])
    fingerprint = expected_contract["identity_fingerprint_sha256"]
    payload = sql_literal(canonical(plan["records"]))
    run_id, source_hash, mapping_hash = map(sql_literal, (migration_id, source_sha256, plan["mapping_sha256"]))
    run_manifest = sql_literal(canonical(manifest))
    allow_core_seed = target_contract.get("allow_initialized_core_seed") is True
    if allow_core_seed:
        require(target_contract.get("initialized_core_seed_contract") == CORE_SEED_CONTRACT, "target_seed_contract_invalid")
    allow_seed = target_contract.get("allow_initialized_zero_ledger_seed") is True or allow_core_seed
    seed_exclusion = "AND NOT (n.nspname='core_billing' AND c.relname IN ('ledger_accounts','balance_state'))" if allow_seed else ""
    if allow_core_seed:
        seed_exclusion += " AND NOT (n.nspname='core_events' AND c.relname='capacity') AND NOT (n.nspname='public' AND c.relname='routing_active_release') AND NOT (n.nspname='core_abuse' AND c.relname='settings') AND NOT (n.nspname='core_oauth' AND c.relname='contract')"
    seed_guard = """
 IF (SELECT count(*) FROM core_billing.ledger_accounts)<>2
 OR (SELECT count(*) FROM core_billing.balance_state)<>2
 OR (SELECT count(DISTINCT bucket) FROM core_billing.ledger_accounts)<>2
 OR EXISTS(SELECT 1 FROM core_billing.ledger_accounts a LEFT JOIN core_billing.balance_state b ON b.ledger_account_id=a.id
  WHERE a.owner_account_id IS NOT NULL OR a.bucket NOT IN ('clearing','revenue') OR a.unit<>'credit_500k_usd'
  OR b.ledger_account_id IS NULL OR b.balance_units<>0 OR b.revision<>0) THEN
  RAISE EXCEPTION 'non-canonical initialized ledger seed';
 END IF;
 SELECT jsonb_build_object('accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM core_billing.ledger_accounts a),
  'balances',(SELECT jsonb_agg(to_jsonb(b) ORDER BY ledger_account_id) FROM core_billing.balance_state b)) INTO seed_before;
""" if allow_seed else ""
    if allow_core_seed:
        seed_guard += """
 IF (SELECT count(*) FROM core_events.capacity)<>1
 OR NOT EXISTS(SELECT 1 FROM core_events.capacity WHERE singleton AND max_events=100000 AND max_bytes=268435456 AND pending_events=0 AND pending_bytes=0)
 OR (SELECT count(*) FROM public.routing_active_release)<>1
 OR NOT EXISTS(SELECT 1 FROM public.routing_active_release WHERE singleton AND config_version IS NULL)
 OR (SELECT count(*) FROM core_abuse.settings)<>1
 OR NOT EXISTS(SELECT 1 FROM core_abuse.settings WHERE singleton AND revision=0 AND document='{"version":1,"policies":[]}'::jsonb) THEN
  RAISE EXCEPTION 'non-canonical initialized core seed';
 END IF;
 seed_before := seed_before || jsonb_build_object('event_capacity',(SELECT to_jsonb(c) FROM core_events.capacity c),
  'routing_release',(SELECT to_jsonb(r) FROM public.routing_active_release r),
  'abuse_settings',(SELECT to_jsonb(s) FROM core_abuse.settings s),
  'oauth_contract',(SELECT to_jsonb(c) FROM core_oauth.contract c));
"""
        seed_guard += " IF " + oauth_seed_guard_sql(contract) + " THEN RAISE EXCEPTION 'non-canonical initialized OAuth seed'; END IF;\n"
    seed_readback = """
 IF seed_before - 'event_capacity' - 'routing_release' - 'abuse_settings' - 'oauth_contract' IS DISTINCT FROM jsonb_build_object(
  'accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM core_billing.ledger_accounts a),
  'balances',(SELECT jsonb_agg(to_jsonb(b) ORDER BY ledger_account_id) FROM core_billing.balance_state b)) THEN
  RAISE EXCEPTION 'initialized ledger seed changed';
 END IF;
""" if allow_seed else ""
    if allow_core_seed:
        seed_readback += """
 IF seed_before->'event_capacity' IS DISTINCT FROM (SELECT to_jsonb(c) FROM core_events.capacity c)
 OR seed_before->'routing_release' IS DISTINCT FROM (SELECT to_jsonb(r) FROM public.routing_active_release r)
 OR seed_before->'abuse_settings' IS DISTINCT FROM (SELECT to_jsonb(s) FROM core_abuse.settings s)
 OR seed_before->'oauth_contract' IS DISTINCT FROM (SELECT to_jsonb(c) FROM core_oauth.contract c) THEN
  RAISE EXCEPTION 'initialized core seed changed';
 END IF;
"""
    application_tables = "c.relkind IN ('r','p') AND n.nspname NOT IN ('pg_catalog','information_schema','core_meta','core_migration') AND left(n.nspname,3)<>'pg_'"
    # No persistent DDL and no finance writes. Lock all owned business tables so
    # emptiness, inserts, readback and receipt are one SERIALIZABLE transaction.
    sql = f"""BEGIN ISOLATION LEVEL SERIALIZABLE;
SET LOCAL synchronous_commit=on;
SET LOCAL standard_conforming_strings=on;
SET LOCAL row_security=off;
SET LOCAL search_path=pg_catalog,public;
SET LOCAL lock_timeout='5s';
SET LOCAL statement_timeout='60s';
SELECT pg_advisory_xact_lock(741803121);
DO $lmm_identity_import$
DECLARE payload JSONB := {payload}::jsonb;
 previous core_migration.import_runs%ROWTYPE;
 table_row RECORD;
 occupied BOOLEAN;
 expected_count BIGINT := jsonb_array_length(payload);
 seed_before JSONB;
 actual_catalog TEXT;
BEGIN
 {role_guard}
 IF current_database()<>{sql_literal(target_identity['database'])} OR current_user<>{sql_literal(target_identity['role'])}
 OR (SELECT oid::bigint FROM pg_database WHERE datname=current_database())<>{target_identity['database_oid']}
 OR (SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname=current_database())<>{sql_literal(target_identity['database_owner'])}
 OR pg_postmaster_start_time()<>{sql_literal(target_identity['postmaster_started_at'])}::timestamptz
 OR current_setting('server_version_num')<>{sql_literal(str(target_identity['server_version_num']))}
 OR current_setting('lmm.migration_instance_nonce',true) IS DISTINCT FROM {sql_literal(target_identity['server_instance_nonce'])}
 OR current_setting('port')::int<>{target_identity['port']} OR inet_server_addr() IS NOT NULL THEN
  RAISE EXCEPTION 'target instance identity mismatch';
 END IF;
 IF EXISTS(SELECT 1 FROM pg_stat_activity WHERE datid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND pid<>pg_backend_pid()) THEN
  RAISE EXCEPTION 'target has other database clients';
 END IF;
 LOCK TABLE core_meta.schema_contract,core_meta.component_contract,core_meta.runtime_contract,core_migration.import_runs IN ACCESS EXCLUSIVE MODE;
 IF NOT EXISTS (SELECT 1 FROM core_meta.schema_contract WHERE singleton AND version={IDENTITY_VERSION} AND encode(fingerprint,'hex')={sql_literal(fingerprint)}) THEN
  RAISE EXCEPTION 'identity contract mismatch';
 END IF;
 FOR table_row IN SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE {application_tables} ORDER BY n.nspname,c.relname
 LOOP EXECUTE format('LOCK TABLE %I.%I IN ACCESS EXCLUSIVE MODE',table_row.nspname,table_row.relname); END LOOP;
 SELECT ({catalog_hash_sql(contract)}) INTO actual_catalog;
 IF actual_catalog<>{sql_literal(catalog_hash)}
 OR NOT EXISTS(SELECT 1 FROM core_meta.runtime_contract WHERE singleton AND encode(catalog_fingerprint,'hex')=actual_catalog)
 OR (SELECT jsonb_object_agg(name,encode(fingerprint,'hex')) FROM core_meta.component_contract) IS DISTINCT FROM {sql_literal(canonical(contract['component_fingerprints']))}::jsonb THEN
  RAISE EXCEPTION 'installed core contract or actual catalog mismatch';
 END IF;
 {seed_guard}
 SELECT * INTO previous FROM core_migration.import_runs WHERE migration_id={run_id} FOR UPDATE;
 IF FOUND THEN
  IF previous.source_archive_sha256<>{source_hash} OR previous.mapping_sha256<>{mapping_hash} OR previous.manifest<>{run_manifest}::jsonb THEN
   RAISE EXCEPTION 'migration receipt mismatch';
  END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM core_migration.import_runs) THEN
   RAISE EXCEPTION 'target already belongs to another migration';
  END IF;
  FOR table_row IN SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
   WHERE {application_tables}
   {seed_exclusion}
  LOOP
   EXECUTE format('SELECT EXISTS(SELECT 1 FROM %I.%I)',table_row.nspname,table_row.relname) INTO occupied;
   IF occupied THEN RAISE EXCEPTION 'target business table is not empty'; END IF;
  END LOOP;
  INSERT INTO core_identity.accounts(id,kind,service_level,active) OVERRIDING SYSTEM VALUE
   SELECT (r->>'account_id')::bigint,'personal',0,(r->>'active')::boolean FROM jsonb_array_elements(payload) r;
  INSERT INTO core_identity.users(id,personal_account_id,platform_role,active,auth_version)
   SELECT (r->>'user_id')::bigint,(r->>'account_id')::bigint,r->>'platform_role',(r->>'active')::boolean,(r->>'auth_version')::bigint FROM jsonb_array_elements(payload) r;
  INSERT INTO core_identity.user_profiles(user_id,username,display_name,email,email_login_allowed,setting,password_login_allowed,second_factor_required,legacy_bcrypt_allowed)
   SELECT (r->>'user_id')::bigint,r->>'username',r->>'display_name',r->>'email',false,r->>'setting',(r->>'password_login_allowed')::boolean,(r->>'second_factor_required')::boolean,(r->>'legacy_bcrypt_allowed')::boolean FROM jsonb_array_elements(payload) r;
  INSERT INTO core_identity.password_logins(login_name,user_id,password_hash)
   SELECT 'legacy-'||(r->>'user_id'),(r->>'user_id')::bigint,r->>'password_hash' FROM jsonb_array_elements(payload) r WHERE r->>'password_hash' IS NOT NULL;
  INSERT INTO core_identity.developer_access(user_id,granted,source_reference,revision)
   SELECT (r->>'user_id')::bigint,(r->>'developer_access_granted')::boolean,r->>'developer_access_source_reference',(r->>'developer_access_revision')::bigint FROM jsonb_array_elements(payload) r;
  INSERT INTO core_billing.account_policies(account_id,preference,revision)
   SELECT (r->>'account_id')::bigint,'wallet_only',1 FROM jsonb_array_elements(payload) r;
  -- ALTER SEQUENCE RESTART is transactional; setval would survive a rollback.
  EXECUTE format('ALTER SEQUENCE %s RESTART WITH %s',pg_get_serial_sequence('core_identity.accounts','id'),COALESCE((SELECT max((r->>'account_id')::bigint)+1 FROM jsonb_array_elements(payload) r),1));
  EXECUTE format('ALTER SEQUENCE %s RESTART WITH %s',pg_get_serial_sequence('core_identity.users','id'),COALESCE((SELECT max((r->>'user_id')::bigint)+1 FROM jsonb_array_elements(payload) r),1));
  INSERT INTO core_migration.import_runs(migration_id,source_archive_sha256,mapping_sha256,manifest)
   VALUES({run_id},{source_hash},{mapping_hash},{run_manifest}::jsonb);
 END IF;
 IF (SELECT count(*) FROM core_identity.users)<>expected_count OR (SELECT count(*) FROM core_identity.accounts)<>expected_count
 OR (SELECT count(*) FROM core_identity.user_profiles)<>expected_count OR (SELECT count(*) FROM core_billing.account_policies)<>expected_count
 OR (SELECT count(*) FROM core_identity.developer_access)<>expected_count
 OR (SELECT count(*) FROM core_identity.password_logins)<>(SELECT count(*) FROM jsonb_array_elements(payload) r WHERE r->>'password_hash' IS NOT NULL) THEN
  RAISE EXCEPTION 'identity row count mismatch';
 END IF;
 IF EXISTS(SELECT 1 FROM jsonb_array_elements(payload) r
  LEFT JOIN core_identity.users u ON u.id=(r->>'user_id')::bigint
  LEFT JOIN core_identity.accounts a ON a.id=u.personal_account_id
  LEFT JOIN core_identity.user_profiles p ON p.user_id=u.id
  LEFT JOIN core_identity.password_logins l ON l.user_id=u.id
  LEFT JOIN core_identity.developer_access d ON d.user_id=u.id
  LEFT JOIN core_billing.account_policies b ON b.account_id=a.id
  WHERE ROW(u.personal_account_id,u.platform_role,u.active,u.auth_version,a.kind,a.service_level,a.active,
   p.username,p.display_name,p.email,p.email_login_allowed,p.setting,p.password_login_allowed,p.second_factor_required,p.legacy_bcrypt_allowed,
   l.login_name,l.password_hash,b.preference,b.revision,d.granted,d.source_reference,d.revision) IS DISTINCT FROM
  ROW((r->>'account_id')::bigint,r->>'platform_role',(r->>'active')::boolean,(r->>'auth_version')::bigint,'personal'::text,0::smallint,(r->>'active')::boolean,
   r->>'username',r->>'display_name',r->>'email',false,r->>'setting',(r->>'password_login_allowed')::boolean,(r->>'second_factor_required')::boolean,(r->>'legacy_bcrypt_allowed')::boolean,
   CASE WHEN r->>'password_hash' IS NULL THEN NULL ELSE 'legacy-'||(r->>'user_id') END,r->>'password_hash','wallet_only'::text,1::bigint,
   (r->>'developer_access_granted')::boolean,r->>'developer_access_source_reference',(r->>'developer_access_revision')::bigint)) THEN
  RAISE EXCEPTION 'identity exact readback mismatch';
 END IF;
 FOR table_row IN SELECT n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE {application_tables}
  AND NOT (n.nspname='core_identity' AND c.relname IN ('accounts','users','user_profiles','password_logins','developer_access'))
  AND NOT (n.nspname='core_billing' AND c.relname='account_policies')
  {seed_exclusion}
 LOOP
  EXECUTE format('SELECT EXISTS(SELECT 1 FROM %I.%I)',table_row.nspname,table_row.relname) INTO occupied;
  IF occupied THEN RAISE EXCEPTION 'non-import target table is not empty'; END IF;
 END LOOP;
 {seed_readback}
 {role_guard}
END $lmm_identity_import$;
SELECT json_build_object('status','identity-import-verified','migration_id',{run_id},'mapped_user_count',{len(plan['records'])});
COMMIT;
"""
    # Dollar quoting is lexical: a source string can close a fixed DO delimiter
    # even inside an inner SQL string. Choose a delimiter absent from all input.
    suffix = hashlib.sha256(sql.encode()).hexdigest()[:24]
    delimiter = "$lmm_import_" + suffix + "$"
    while delimiter in sql:
        suffix += "x"
        delimiter = "$lmm_import_" + suffix + "$"
    sql = sql.replace("DO $lmm_identity_import$", "DO " + delimiter, 1)
    before, found, after = sql.rpartition("END $lmm_identity_import$;")
    require(bool(found), "sql_template_invalid")
    return before + "END " + delimiter + ";" + after


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--source-receipt", type=Path, required=True)
    parser.add_argument("--target-receipt", type=Path, help="Owner-provisioned private receipt: status=initialized-empty-identity-target; migration_id; source_archive_sha256; connection={socket_directory,port,database,role,pgpass_file}; instance_identity={database_oid,database_owner,postmaster_started_at,server_version_num,server_instance_nonce,listen_addresses}; contract={identity_version:4,identity_fingerprint_sha256:reviewed-exact-raw-SQL-SHA256,component_fingerprints:exact-13-components,catalog_query_sha256:current-Rust-query-SHA,catalog_fingerprint_sha256:owner-verified-actual-catalog,allow_initialized_core_seed:true,initialized_core_seed_contract:ledger-zero-events-empty-routing-unset-abuse-default-oauth-contract-v3}; application_or_worker_started:false; registry_provisioned:true")
    parser.add_argument("--output", type=Path, required=True, help="New private directory under ~/Backups; must not already exist")
    parser.add_argument("--migration-id", required=True)
    parser.add_argument("--apply", action="store_true", help="Explicitly execute the private SQL plan against the matching empty initialized target")
    parser.add_argument("--confirm-source-sha256", help="Required on apply; exact immutable archive SHA256")
    args = parser.parse_args()
    output = None
    apply_attempted = False
    try:
        require(MIGRATION_ID.fullmatch(args.migration_id) is not None, "migration_id_invalid")
        expected_contract = identity_contract()
        require(args.output.is_absolute() and args.output.resolve().is_relative_to(BACKUP_ROOT.resolve()), "private_artifact_outside_backups")
        private_backup_path(args.output.parent, directory=True)
        args.output.mkdir(mode=0o700)
        output = private_backup_path(args.output, directory=True)
        source = load_receipt(args.source_receipt)
        source_identity, users, factors, password_enabled = export_source(source, output)
        source_hash = source["archive"]["sha256"]
        plan = map_identity(users, factors, source_sha256=source_hash, password_login_enabled=password_enabled)
        write_private(output, "identity-mapping.private.json", canonical(plan) + "\n")
        id_map_path = write_private(output, "source-target-id-map.private.jsonl", id_map_bytes(plan["records"]))
        manifest = {"format_version": 2, "scope": "identity-only", "source_archive_sha256": source_hash,
                    "identity_contract": expected_contract, "developer_access_policy": DEVELOPER_POLICY,
                    "initialized_core_seed_contract": CORE_SEED_CONTRACT,
                    "source_export_sha256": sha256_file(output / "source-identity-export.private.jsonl"),
                    "source_baseline_proof_sha256": sha256_file(output / "source-baseline-proof.private.json"),
                    "source_target_id_map_sha256": sha256_file(id_map_path),
                    "mapping_sha256": plan["mapping_sha256"], "summary": plan["summary"]}
        validate_import_material(plan, source_hash, manifest)
        # Persist the exact replay materials before any possible target commit.
        write_private(output, "manifest.private.json", canonical(manifest) + "\n")
        public = {"status": "dry-run-mapped", "migration_id": args.migration_id, "apply": False,
                  "source_archive_sha256": source_hash, "mapping_sha256": plan["mapping_sha256"],
                  "identity_contract": expected_contract, **plan["summary"]}
        if args.target_receipt:
            target = load_receipt(args.target_receipt, private=True)
            require(target.get("status") == "initialized-empty-identity-target" and target.get("registry_provisioned") is True and target.get("application_or_worker_started") is False, "target_receipt_status_invalid")
            require(target.get("migration_id") == args.migration_id and target.get("source_archive_sha256") == source_hash, "source_target_receipt_mismatch")
            require(isinstance(target.get("contract"), dict) and all(target["contract"].get(key) == value for key, value in expected_contract.items())
                    and type(target["contract"].get("identity_version")) is int, "target_contract_invalid")
            connection = target.get("connection", {})
            instance = target.get("instance_identity", {})
            require(isinstance(instance, dict) and isinstance(instance.get("server_instance_nonce"), str) and bool(instance["server_instance_nonce"]), "target_instance_nonce_receipt_missing")
            # Reject stale DDL/seed receipts before even probing the target.
            _, login_role = connection_roles(connection)
            sql_identity = {**instance, "database": connection.get("database"), "role": connection.get("role"),
                            "session_user": login_role, "port": connection.get("port")}
            sql = build_import_sql(plan, migration_id=args.migration_id, source_sha256=source_hash, target_contract=target.get("contract", {}), manifest=manifest, target_identity=sql_identity)
            target_identity = json.loads(psql(connection, "BEGIN READ ONLY;\n" + IDENTITY_SQL + "\nCOMMIT;", readonly=True, private_dir=output, label="target-identity"))
            verify_instance(target_identity, connection, target.get("instance_identity", {}), source=False)
            require((target_identity["database_oid"], target_identity["postmaster_started_at"]) != (source_identity["database_oid"], source_identity["postmaster_started_at"]), "source_target_same_database")
            for key in ("datcollate", "datctype", "datcollversion"):
                require(target_identity.get(key) == source_identity.get(key), "source_target_collation_mismatch")
            write_private(output, "identity-import.private.sql", sql)
            public["status"] = "dry-run-sql-plan-ready"
            if args.apply:
                require(args.confirm_source_sha256 == source_hash, "apply_source_confirmation_missing")
                require(sha256_file(Path(source["archive"]["path"])) == source_hash, "source_archive_changed_before_import")
                apply_attempted = True
                result = psql(connection, sql, readonly=False, private_dir=output, label="target-import")
                write_private(output, "target-import.private.jsonl", result)
                public.update(status="identity-import-verified", apply=True)
        else:
            require(not args.apply, "apply_requires_target_receipt")
        require(sha256_file(Path(source["archive"]["path"])) == source_hash, "source_archive_changed_during_import")
        write_private(output, "report.private.json", canonical(public) + "\n")
        print(canonical(public))
        return 0
    except (MappingError, OSError, ValueError, KeyError, TypeError) as error:
        # Database stderr can contain PII; it is already private, never echoed.
        code = str(error) if isinstance(error, MappingError) else type(error).__name__
        if output is not None:
            try:
                write_private(output, "failure.private.json", canonical({"status": "apply-outcome-unconfirmed" if apply_attempted else "aborted", "error_code": code}) + "\n")
            except OSError:
                pass
        print(canonical({"status": "apply-outcome-unconfirmed" if apply_attempted else "aborted", "error_code": code}), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
