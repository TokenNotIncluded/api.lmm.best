"""Synthetic mapper/SQL guards; optional isolated PostgreSQL transaction tests.

Normal invocation runs pure tests. Set LMM_CONSOLE_ETL_TEST_FIXTURE_RECEIPT only
to the resource owner's ready-isolated-synthetic-fixture receipt to run PG tests.
Each PG test creates/drops only its own random database on that checked fixture.
No test opens a legacy snapshot or production data.
"""

from copy import deepcopy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import select
import secrets
import signal
import subprocess
import tempfile
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).with_name("migrate-legacy-console.py")
SPEC = importlib.util.spec_from_file_location("lmm_console_import_tests", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
H = MODULE.IDENTITY
SOURCE = "a" * 64


def option(value="[]", key="console_setting.announcements"):
    return {"key": key, "value": value}


def read(user_id=7, item_id=2, revision="b" * 64, epoch=1700000000, **changes):
    return {"id": 13, "user_id": user_id, "announcement_id": item_id,
            "revision": revision, "read_at": epoch, **changes}


def setup():
    return {"id": 1, "version": "legacy-synthetic-0.2.107", "initialized_at": 1699999999}


def mapped(options=None, setups=None, reads=None):
    return MODULE.map_console(options or [], setups or [], reads or [], source_sha256=SOURCE)


def identity_material(contract=None):
    contract = contract or MODULE.current_contract()
    users = [{"id": 7, "username": "console-identity-synthetic", "password": "$2b$10$" + "." * 53,
              "display_name": "Console fixture", "email": "console@example.invalid", "setting": "{}",
              "role": 1, "status": 1, "auth_version": 1, "deleted_at": None,
              "trust_level_override": 0, "console_activated_at": 0}]
    mapping = H.map_identity(users, [], source_sha256=SOURCE)
    id_map = H.id_map_bytes(mapping["records"]).encode()
    identity_manifest = {"format_version": 2, "scope": "identity-only", "source_archive_sha256": SOURCE,
                         "identity_contract": {key: contract[key] for key in ("identity_version", "identity_fingerprint_sha256")},
                         "developer_access_policy": H.DEVELOPER_POLICY,
                         "initialized_core_seed_contract": H.CORE_SEED_CONTRACT,
                         "source_export_sha256": "e" * 64, "source_baseline_proof_sha256": "f" * 64,
                         "source_target_id_map_sha256": hashlib.sha256(id_map).hexdigest(),
                         "mapping_sha256": mapping["mapping_sha256"], "summary": mapping["summary"]}
    manifest_bytes = (H.canonical(identity_manifest) + "\n").encode()
    mapping_bytes = (H.canonical(mapping) + "\n").encode()
    proof = MODULE.bind_identity_proof(manifest_bytes, mapping_bytes, id_map, source_sha256=SOURCE, contract=contract)
    return mapping, identity_manifest, (manifest_bytes, mapping_bytes, id_map), proof


def manifest(plan, proof):
    return {"format_version": 1, "scope": "console-only", "source_archive_sha256": SOURCE,
            "mapping_sha256": plan["mapping_sha256"], "summary": plan["summary"],
            "identity_import": MODULE.identity_reference(proof, "identity-synthetic")}


def synthetic_identity():
    return {"database": "console_synthetic", "role": "console_owner", "database_oid": 1234,
            "postmaster_started_at": "2026-10-12 00:00:00+00", "server_version_num": "180006",
            "server_instance_nonce": "synthetic-nonce", "port": 5432}


def build(plan=None, contract=None, target=None, identity=None, run="console-synthetic", proof=None, connection=None):
    plan = plan or mapped([option()], [setup()], [read()])
    contract = contract or MODULE.current_contract()
    proof = proof or identity_material(contract)[3]
    target = target or {**contract, "catalog_fingerprint_sha256": "c" * 64,
                        "identity_import": MODULE.identity_reference(proof, "identity-synthetic")}
    return MODULE.build_import_sql(plan, migration_id=run, contract=contract,
                                   target_contract=target, manifest=manifest(plan, proof),
                                   target_identity=identity or synthetic_identity(), identity_proof=proof,
                                   target_connection=connection)


class MapperTests(unittest.TestCase):
    def test_preserves_raw_text_setup_and_receipt_without_rehash(self):
        raw = ' [ {"id":2,"content":"<公告>&","extra":"x","mandatory":true,"publishDate":"2020-01-01T0:00:00,12345678999+24:60","ackRevision":" g1 "} ] '
        rows = [option(raw), option(" raw notice\n ", "Notice")]
        originals = deepcopy((rows, [setup()], [read()]))
        plan = mapped(rows, [setup()], [read()])
        self.assertEqual(plan["options"], sorted(rows, key=lambda r: r["key"]))
        self.assertEqual(plan["initialization"], {"version": setup()["version"], "initialized_at": setup()["initialized_at"]})
        self.assertEqual(plan["receipts"], [{k: v for k, v in read().items() if k != "id"}])
        self.assertEqual((rows, [setup()], [read()]), originals)

    def test_canonical_empty_is_retained_and_missing_setup_is_missing(self):
        plan = mapped([option(""), option('[{"mandatory":false}]', "Announcements")])
        self.assertEqual(plan["options"][1]["value"], "")
        self.assertIsNone(plan["initialization"])
        self.assertEqual(plan["summary"]["setup_records_copied"], 0)

    def test_only_whitelist_and_duplicate_options_rejected(self):
        for rows in ([option("secret", "PrivateKey")], [option(), option()]):
            with self.subTest(rows=rows), self.assertRaises(MODULE.MappingError):
                mapped(rows)

    def test_null_and_unknown_go_fields_preserved(self):
        for raw in ("null", "[null]", '[{"MANDATORY":null,"unknown":[{"x":2}]}]',
                    '[{"mandatory":true,"Mandatory":null,"id":2,"publishDate":"2020-01-01T00:00:00Z"}]'):
            self.assertEqual(mapped([option(raw)])["options"][0]["value"], raw)

    def test_invalid_json_and_wrong_known_field_types_rejected(self):
        for raw in ("{}", "[true]", "[1]", "[] []", "[NaN]", '[{"content":1,"content":"ok"}]',
                    '[{"mandatory":"true"}]', '[{"ackRevision":3}]', '[{"revision":[]} ]',
                    '[{"id":1.0}]', '[{"read_at":9223372036854775808}]'):
            with self.subTest(raw=raw), self.assertRaises(MODULE.MappingError):
                mapped([option(raw)])

    def test_surrogates_raw_nul_and_non_text_rejected(self):
        for value in (None, 1, "\ud800", "\0", '[{"content":"\\ud800"}]', '[{"unknown":"\\udfff"}]'):
            with self.subTest(value=repr(value)), self.assertRaises(MODULE.MappingError):
                mapped([option(value)])

    def test_invalid_mandatory_dates_rejected_without_normalizing_valid(self):
        for date in ("2025-02-30T00:00:00Z", "2025-01-01T00:00:60Z", "2025-01-01 00:00:00Z",
                     "2025-01-01T24:00:00Z", "2025-01-01T00:00:00+25:00"):
            with self.subTest(date=date), self.assertRaises(MODULE.MappingError):
                mapped([option(json.dumps([{"id": 1, "mandatory": True, "publishDate": date}]))])
        mapped([option('[{"mandatory":false,"publishDate":"not a date"}]')])

    def test_invalid_setup_facts_fail_closed(self):
        for row in ({**setup(), "id": 2}, {**setup(), "id": True}, {**setup(), "version": ""},
                    {**setup(), "initialized_at": -1}, {**setup(), "initialized_at": 1.5},
                    {**setup(), "initialized_at": 253402300800}):
            with self.subTest(row=row), self.assertRaises(MODULE.MappingError):
                mapped(setups=[row])
        with self.assertRaises(MODULE.MappingError):
            mapped(setups=[setup(), setup()])

    def test_only_exact_receipt_duplicates_deduplicate(self):
        plan = mapped(reads=[read(), read(id=999)])
        self.assertEqual(len(plan["receipts"]), 1)
        self.assertEqual(plan["summary"]["exact_duplicate_receipts_deduplicated"], 1)
        with self.assertRaisesRegex(MODULE.MappingError, "conflicting_duplicate_receipt"):
            mapped(reads=[read(), read(epoch=1700000001)])

    def test_same_item_distinct_users_and_revisions_retained(self):
        plan = mapped(reads=[read(), read(user_id=8), read(revision="c" * 64)])
        self.assertEqual(len(plan["receipts"]), 3)

    def test_receipt_integer_and_revision_domains(self):
        for changes in ({"user_id": True}, {"announcement_id": 0}, {"read_at": 0}, {"read_at": 1.5},
                        {"revision": "B" * 64}, {"revision": "b" * 63}):
            with self.subTest(changes=changes), self.assertRaises(MODULE.MappingError):
                mapped(reads=[read(**changes)])

    def test_mapping_hash_deterministic_and_binds_every_fact(self):
        a = mapped([option(), option("notice", "Notice")], [setup()], [read(), read(user_id=8)])
        b = mapped([option("notice", "Notice"), option()], [setup()], [read(user_id=8), read()])
        self.assertEqual(a["mapping_sha256"], b["mapping_sha256"])
        for altered in (mapped([option("null"), option("notice", "Notice")], [setup()], [read(), read(user_id=8)]),
                        mapped([option(), option("notice", "Notice")], [setup()], [read(epoch=1700000001), read(user_id=8)])):
            self.assertNotEqual(a["mapping_sha256"], altered["mapping_sha256"])


class SqlGuardTests(unittest.TestCase):
    def test_real_component_list_dynamic_and_identity_version(self):
        contract = MODULE.current_contract()
        source = (MODULE.ROOT / "services/core/src/core_schema.rs").read_text()
        expected = dict(re.findall(r'"([a-z][a-z-]*)"\s*,\s*include_str!\("(\.\./schema/[a-z_]+\.sql)"\)', source))
        self.assertEqual(set(contract["component_fingerprints"]), set(expected))
        self.assertIn("console", expected)
        self.assertIn("model-gateway", expected)
        self.assertIn("commerce", expected)
        self.assertEqual(contract["identity_version"], int(re.search(r"const VERSION:\s*i32\s*=\s*(\d+)", (MODULE.ROOT / "services/core/src/identity/schema.rs").read_text())[1]))

    def test_unknown_component_syntax_fails_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path = root / "services/core/src/core_schema.rs"
            path.parent.mkdir(parents=True)
            path.write_text('const COMPONENTS: &[(&str, &str)] = &[("console", external_macro!("x"))];')
            with self.assertRaisesRegex(MODULE.MappingError, "component_contract_syntax_unknown"):
                MODULE.current_contract(root)

    def test_no_ddl_or_non_console_writes_and_transaction_guards(self):
        sql = build()
        self.assertIn("BEGIN ISOLATION LEVEL SERIALIZABLE", sql)
        self.assertIn("synchronous_commit=on", sql)
        self.assertIn("SET LOCAL search_path=pg_catalog,public", sql)
        self.assertIn("pg_advisory_xact_lock(741803121)", sql)
        self.assertIn("current_setting('port')::int", sql)
        self.assertEqual(sql.count("target has another active client"), 2)
        self.assertIn("IN SHARE MODE", sql)
        self.assertIn("IN ACCESS EXCLUSIVE MODE", sql)
        self.assertEqual(set(re.findall(r"\bINSERT INTO\s+([a-z_.]+)", sql)),
                         {"core_console.options", "core_console.announcement_reads", "core_console.initialization", "core_migration.import_runs"})
        self.assertIsNone(re.search(r"\b(?:CREATE|ALTER|DROP|TRUNCATE|UPDATE|DELETE)\b", sql))
        self.assertIn("non-console facts changed", sql)
        self.assertIn("sequence-state", sql)
        self.assertIn("''is_called'',is_called", sql)
        self.assertIn("JOIN pg_sequence s ON s.seqrelid=c.oid", sql)
        self.assertIn("console exact readback mismatch", sql)

    def test_dollar_delimiter_and_standard_strings_do_not_rewrite_data(self):
        attack = "@DELIMITER@ $lmm_console_import$ '\\n'; DROP SCHEMA core_identity CASCADE;--"
        plan = mapped([option(attack, "Notice")])
        sql = build(plan)
        delimiter = re.search(r"DO (\$[a-z_0-9]+\$)\n", sql)[1]
        self.assertEqual(sql.count(delimiter), 2)
        self.assertIn(H.sql_literal(H.canonical({key: plan[key] for key in ("source_archive_sha256", "options", "initialization", "receipts")})), sql)
        self.assertIn("@DELIMITER@", sql)
        self.assertTrue(sql.endswith(delimiter + ";\nCOMMIT;\n"))

    def test_tampered_mapping_manifest_contract_and_target_rejected(self):
        plan = mapped([option()])
        plan["options"][0]["value"] = "null"
        with self.assertRaisesRegex(MODULE.MappingError, "mapping_hash_mismatch"):
            build(plan)
        contract = MODULE.current_contract()
        for changes in ({"identity_version": contract["identity_version"] - 1}, {"component_fingerprints": {}},
                        {"catalog_query_sha256": "d" * 64}, {"catalog_fingerprint_sha256": "invalid"}):
            with self.subTest(changes=changes), self.assertRaises(MODULE.MappingError):
                build(contract=contract, target={**contract, "catalog_fingerprint_sha256": "c" * 64, **changes})
        with self.assertRaises(MODULE.MappingError):
            build(identity={**synthetic_identity(), "server_instance_nonce": None})

    def test_actual_catalog_hash_matches_rust_utf8_length_prefix(self):
        sql = MODULE.catalog_hash_sql(MODULE.current_contract())
        self.assertIn("int8send(octet_length(convert_to(definition,'UTF8'))::bigint)", sql)
        self.assertIn('ORDER BY definition COLLATE "C"', sql)
        self.assertIn("sha256", sql)

    def test_explicit_login_and_nologin_owner_are_bound_independently(self):
        identity = {**synthetic_identity(), "session_user": "console_login"}
        connection = {"role": identity["role"], "login_role": "console_login", "set_role": identity["role"]}
        sql = build(identity=identity, connection=connection)
        self.assertEqual(sql.count("session_user<>'console_login'"), 2)
        self.assertEqual(sql.count("rolcanlogin FROM pg_roles WHERE rolname=current_user"), 2)
        self.assertIn("target effective owner or login identity changed", sql)
        for invalid in ({"role": identity["role"], "login_role": "console_login"},
                        {**connection, "set_role": "another_owner"},
                        {**connection, "login_role": identity["role"]}):
            with self.subTest(invalid=invalid), self.assertRaisesRegex(MODULE.MappingError, "target_role_switch_invalid"):
                build(identity=identity, connection=invalid)
        with self.assertRaisesRegex(MODULE.MappingError, "target_session_user_mismatch"):
            build(identity=synthetic_identity(), connection=connection)

    def test_replay_branch_has_no_write(self):
        sql = build()
        branch = sql[sql.index(" IF replay THEN"):sql.index(" ELSE\n", sql.index(" IF replay THEN"))]
        self.assertIsNone(re.search(r"\b(?:INSERT|UPDATE|DELETE|ALTER)\b", branch))
        self.assertIn("previous.manifest-'applied_at'", branch)
        self.assertIn("SELECT * INTO required_identity", sql)
        self.assertIn("required identity import receipt missing", sql)
        self.assertIn("orphan announcement receipt user", sql)

    def test_source_hash_query_whitelist_and_identifier_guard(self):
        sql = MODULE.source_table_hash_sql("legacy_synthetic", "options")
        self.assertIn("WHERE key IN ('console_setting.announcements','Announcements','Notice')", sql)
        for schema, table in (("public; DROP SCHEMA public", "options"), ("public", "users")):
            with self.assertRaises(MODULE.MappingError):
                MODULE.source_table_hash_sql(schema, table)


    def test_source_role_switch_rejected_before_any_database_read(self):
        for field in ("login_role", "set_role"):
            receipt = {"status": "snapshot-restored-baseline-verified-frozen-and-classified",
                       "archive": {"path": "/synthetic/archive", "sha256": SOURCE},
                       "readonly_reader": {field: "synthetic_owner"}}
            with self.subTest(field=field), patch.object(H, "private_backup_path", side_effect=lambda path: path), \
                    patch.object(H, "sha256_file", return_value=SOURCE), patch.object(H, "psql") as database:
                with self.assertRaisesRegex(MODULE.MappingError, "source_role_switch_rejected"):
                    MODULE.export_source(receipt, Path("/synthetic/output"))
                database.assert_not_called()


class IdentityProofTests(unittest.TestCase):
    def test_validated_proof_contains_field_hashes_not_profile_or_verifier_text(self):
        mapping, _, _, proof = identity_material()
        facts = H.canonical(proof["facts"])
        for key in ("username", "email", "setting", "password_hash"):
            self.assertNotIn(mapping["records"][0][key], facts)
        self.assertEqual(proof["facts"][0]["user_id"], 7)

    def test_wrong_archive_or_old_identity_contract_rejected(self):
        _, original, material, _ = identity_material()
        for change in ({"source_archive_sha256": "b" * 64},
                       {"identity_contract": {**original["identity_contract"], "identity_version": 2}}):
            with self.subTest(change=change), self.assertRaises(MODULE.MappingError):
                MODULE.bind_identity_proof(H.canonical({**original, **change}).encode(), material[1], material[2],
                                           source_sha256=SOURCE, contract=MODULE.current_contract())

    def test_mapping_mutation_without_matching_manifest_rejected(self):
        mapping, _, material, _ = identity_material()
        mapping["records"][0]["username"] = "other-person-same-id"
        with self.assertRaises(MODULE.MappingError):
            MODULE.bind_identity_proof(material[0], H.canonical(mapping).encode(), material[2],
                                       source_sha256=SOURCE, contract=MODULE.current_contract())

    def test_id_map_bytes_and_preserved_ids_both_required(self):
        _, identity_manifest, material, _ = identity_material()
        for raw in (material[2] + b"\n", b'{"source_user_id":7,"target_user_id":8,"target_personal_account_id":1}\n'):
            with self.subTest(raw=raw), self.assertRaises(MODULE.MappingError):
                MODULE.bind_identity_proof(material[0], material[1], raw, source_sha256=SOURCE, contract=MODULE.current_contract())
        # Even a newly self-consistent file hash cannot authorize remapping IDs.
        raw = b'{"source_user_id":7,"target_user_id":8,"target_personal_account_id":1}\n'
        identity_manifest["source_target_id_map_sha256"] = hashlib.sha256(raw).hexdigest()
        with self.assertRaises(MODULE.MappingError):
            MODULE.bind_identity_proof(H.canonical(identity_manifest).encode(), material[1], raw,
                                       source_sha256=SOURCE, contract=MODULE.current_contract())

    def test_missing_identity_proof_and_unmapped_receipts_rejected_before_database(self):
        plan = mapped(reads=[read()])
        contract = MODULE.current_contract()
        with self.assertRaisesRegex(MODULE.MappingError, "identity_proof_required"):
            MODULE.build_import_sql(plan, migration_id="console-synthetic", target_contract={}, manifest=manifest(plan, identity_material()[3]),
                                    contract=contract, target_identity=synthetic_identity(), identity_proof=None)
        with self.assertRaisesRegex(MODULE.MappingError, "console_receipt_identity_unmapped"):
            build(mapped(reads=[read(user_id=999)]))

    def test_target_reference_must_pin_exact_identity_artifacts(self):
        contract = MODULE.current_contract()
        proof = identity_material(contract)[3]
        reference = MODULE.identity_reference(proof, "identity-synthetic")
        for key in ("source_archive_sha256", "mapping_sha256", "manifest_sha256", "source_target_id_map_sha256"):
            with self.subTest(key=key), self.assertRaisesRegex(MODULE.MappingError, "identity_import_reference_mismatch"):
                build(contract=contract, proof=proof, target={**contract, "catalog_fingerprint_sha256": "c" * 64,
                                                           "identity_import": {**reference, key: "d" * 64}})


@unittest.skipUnless(os.environ.get("LMM_CONSOLE_ETL_TEST_FIXTURE_RECEIPT"), "isolated PG receipt not requested")
class PostgresTests(unittest.TestCase):
    """Executes the importer, including exact replay and rollback, against PG18."""

    @classmethod
    def setUpClass(cls):
        receipt = H.load_receipt(Path(os.environ["LMM_CONSOLE_ETL_TEST_FIXTURE_RECEIPT"]))
        H.require(receipt.get("status") == "ready-isolated-synthetic-fixture" and
                  receipt.get("isolation", {}).get("synthetic_only") is True, "synthetic_fixture_required")
        fixture = receipt["fixtures"]["console"]
        cls.fixture_receipt = receipt
        cls.fixture = fixture
        login_role = fixture.get("login_role", fixture["owner"])
        role = receipt["roles"][login_role]
        cls.artifacts = H.BACKUP_ROOT / ("api-lmm-console-etl-tests-" + secrets.token_hex(10))
        cls.artifacts.mkdir(mode=0o700)
        H.private_backup_path(cls.artifacts, directory=True)
        password = H.private_backup_path(Path(role["password_file"])).read_text().strip()
        escape = lambda value: str(value).replace("\\", "\\\\").replace(":", "\\:")
        pgpass = H.write_private(cls.artifacts, "pgpass.private", ":".join(map(escape,
                    (role["socket_directory"], role["port"], "*", role["role"], password))) + "\n")
        # Wildcard is structural, not an escaped password field.
        cls.connection = {"socket_directory": role["socket_directory"], "port": role["port"],
                          "role": fixture["owner"], "database": fixture["database"], "pgpass_file": str(pgpass)}
        if login_role != fixture["owner"]:
            cls.connection.update(login_role=login_role, set_role=fixture["owner"])
        cls.counter = 0
        cls.owned = []
        cls.history = []
        cls.known_backends = {}
        cls.backend_history = []
        fixture_identity = json.loads(cls.query("BEGIN READ ONLY;\nSET LOCAL search_path=pg_catalog,public;\n" + H.IDENTITY_SQL + "\nCOMMIT;", readonly=True))
        H.verify_instance(fixture_identity, cls.connection,
                          {**receipt["instance_identity"], **fixture["empty_owner_baseline"]}, source=False)
        H.write_private(cls.artifacts, "fixture-identity.private.json", H.canonical(fixture_identity) + "\n")
        cls.contract = MODULE.current_contract()
        cls.sources = {path: (MODULE.ROOT / path).read_text() for path in cls.contract["source_file_hashes"]}
        H.require(all(hashlib.sha256(raw.encode()).hexdigest() == cls.contract["source_file_hashes"][path]
                      for path, raw in cls.sources.items()), "fixture_source_changed_before_freeze")
        H.write_private(cls.artifacts, "input-contract.private.json", H.canonical(cls.contract) + "\n")

    @classmethod
    def query(cls, sql, *, connection=None, readonly=False):
        cls.counter += 1
        connection = connection or cls.connection
        label = "pg-" + str(cls.counter)
        pid_file = H.write_private(cls.artifacts, label + ".pid.private.txt", "")
        # Capture this exact session's PID even when ON_ERROR_STOP rejects SQL.
        # This is fixture-only psql output routing, with no business SQL change.
        wrapped = "\\o " + str(pid_file) + "\nSELECT pg_backend_pid();\n\\o\n" + sql
        try:
            return H.psql(connection, wrapped, readonly=readonly, private_dir=cls.artifacts, label=label)
        finally:
            raw = pid_file.read_text().strip()
            if raw:
                pid = H.positive_int(int(raw), "fixture_backend_pid_invalid")
                cls.known_backends.setdefault(connection["database"], set()).add(pid)
                cls.backend_history.append({"label": label, "database": connection["database"], "pid": pid})

    @classmethod
    def idle_sql(cls, database):
        H.require(H.IDENTIFIER.fullmatch(database) is not None, "fixture_database_invalid")
        known = ",".join(str(pid) for pid in sorted(cls.known_backends.get(database, set()))) or "0"
        predicate = "datname=" + H.sql_literal(database) + " AND pid<>pg_backend_pid()"
        return """DO $fixture_idle$ DECLARE deadline TIMESTAMPTZ := clock_timestamp()+interval '5 seconds'; BEGIN
 LOOP
  PERFORM pg_stat_clear_snapshot();
  IF EXISTS(SELECT 1 FROM pg_stat_activity WHERE """ + predicate + " AND pid NOT IN (" + known + """)) THEN
   RAISE EXCEPTION 'fixture has an unknown backend';
  END IF;
  IF NOT EXISTS(SELECT 1 FROM pg_stat_activity WHERE """ + predicate + """ ) THEN RETURN; END IF;
  IF clock_timestamp()>=deadline THEN RAISE EXCEPTION 'fixture known backend did not leave'; END IF;
  PERFORM pg_sleep(least(0.02,greatest(0,extract(epoch FROM deadline-clock_timestamp()))));
 END LOOP;
END $fixture_idle$;
"""

    def setUp(self):
        self.database = "lmm_console_etl_nologin_" + secrets.token_hex(10)
        self.query('CREATE DATABASE "' + self.database + '";')
        self.owned.append(self.database)
        self.history.append({"database": self.database, "created": True, "dropped": False})
        self.addCleanup(self.cleanup_database)
        # Keep the resource owner's immutable cluster nonce. The restricted
        # owner cannot change that parameter; database OID binds each new DB.
        self.db = {**self.connection, "database": self.database}
        sql = "BEGIN;\n"
        for path in self.contract["source_file_hashes"]:
            if "/schema/" in path:
                sql += self.sources[path] + "\n"
        core = self.sources["services/core/src/core_schema.rs"]
        sql += re.search(r'const CONTRACT: &str = "(.*?)";', core)[1] + "\n"
        sql += f"INSERT INTO core_meta.schema_contract(version,fingerprint) VALUES({self.contract['identity_version']},decode('{self.contract['identity_fingerprint_sha256']}','hex'));\n"
        for name, sha in self.contract["component_fingerprints"].items():
            sql += f"INSERT INTO core_meta.component_contract(name,fingerprint) VALUES({H.sql_literal(name)},decode('{sha}','hex'));\n"
        sql += "INSERT INTO core_meta.runtime_contract(catalog_fingerprint) SELECT decode(catalog_hash,'hex') FROM (" + MODULE.catalog_hash_sql(self.contract) + ") h(catalog_hash);\nCOMMIT;\n"
        self.query(sql, connection=self.db)
        self.identity = json.loads(self.query("BEGIN READ ONLY;\n" + H.IDENTITY_SQL + "\nCOMMIT;", connection=self.db, readonly=True))
        self.catalog = self.query(MODULE.catalog_hash_sql(self.contract) + ";", connection=self.db, readonly=True).strip()
        self.identity_plan, self.identity_manifest, self.identity_artifacts, self.proof = identity_material(self.contract)
        self.target = {**self.contract, "catalog_fingerprint_sha256": self.catalog,
                       "identity_import": MODULE.identity_reference(self.proof, "identity-synthetic")}
        identity_sql = H.build_import_sql(self.identity_plan, migration_id="identity-synthetic", source_sha256=SOURCE,
                                         target_contract={**self.target, "allow_initialized_core_seed": True,
                                                          "initialized_core_seed_contract": H.CORE_SEED_CONTRACT},
                                         manifest=self.identity_manifest, target_identity=self.identity)
        # A completed psql client can precede its PostgreSQL backend's exit by
        # a few milliseconds. Fence fixture setup in this same import session;
        # keep both importers' fail-closed no-other-backend guards unchanged.
        self.query(self.idle_sql(self.database) + identity_sql, connection=self.db)

    def cleanup_database(self):
        self.query(self.idle_sql(self.database) + 'DROP DATABASE "' + self.database + '";')
        self.owned.remove(self.database)
        next(item for item in self.history if item["database"] == self.database)["dropped"] = True

    @classmethod
    def tearDownClass(cls):
        remaining = cls.query("SELECT coalesce(json_agg(datname),'[]'::json) FROM pg_database WHERE datname ~ '^lmm_console_etl_nologin_[0-9a-f]+$';", readonly=True)
        H.write_private(cls.artifacts, "cleanup-receipt.private.json", H.canonical({"synthetic_only": True,
                        "database_history": cls.history, "remaining_owned": cls.owned,
                        "backend_history": cls.backend_history,
                        "remaining_console_etl_databases": json.loads(remaining)}) + "\n")
        final_contract = MODULE.current_contract()
        H.write_private(cls.artifacts, "input-contract-after.private.json", H.canonical(final_contract) + "\n")
        H.require(final_contract == cls.contract, "fixture_source_contract_changed_during_tests")
        if cls.owned:
            raise AssertionError("owned synthetic databases not cleaned")

    def apply(self, plan=None, **kwargs):
        sql = build(plan=plan, contract=self.contract, target=self.target, identity=self.identity, proof=self.proof, connection=self.db, **kwargs)
        return self.query(self.idle_sql(self.database) + sql, connection=self.db)

    def reject(self, message, plan=None):
        # Require the intended database guard, not any earlier mapper/parse error.
        sql = build(plan=plan, contract=self.contract, target=self.target, identity=self.identity, proof=self.proof, connection=self.db)
        if message != "target has another active client":
            sql = self.idle_sql(self.database) + sql
        with self.assertRaisesRegex(MODULE.MappingError, r"pg-\d+_database_command_failed"):
            self.query(sql, connection=self.db)
        stderr = (self.artifacts / ("pg-" + str(type(self).counter) + ".stderr.private.txt")).read_text()
        self.assertIn(message, stderr.splitlines()[0])

    def facts(self):
        return json.loads(self.query("SELECT jsonb_build_object('options',(SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY key),'[]') FROM core_console.options r),'reads',(SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY user_id,announcement_id,revision),'[]') FROM core_console.announcement_reads r),'setup',(SELECT coalesce(jsonb_agg(to_jsonb(r)),'[]') FROM core_console.initialization r),'runs',(SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY migration_id),'[]') FROM core_migration.import_runs r));", connection=self.db, readonly=True))

    def other_facts(self):
        tables = json.loads(self.query("SELECT json_agg(json_build_array(n.nspname,c.relname) ORDER BY n.nspname,c.relname) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind IN ('r','p') AND n.nspname NOT IN ('pg_catalog','information_schema','core_console','core_migration') AND left(n.nspname,3)<>'pg_';", connection=self.db, readonly=True))
        pieces = []
        for schema, table in tables:
            H.require(H.IDENTIFIER.fullmatch(schema) is not None and H.IDENTIFIER.fullmatch(table) is not None, "test_catalog_identifier_invalid")
            pieces.append("SELECT " + H.sql_literal(schema + "." + table) + " AS name,jsonb_build_object('count',count(*),'sha256',encode(sha256(coalesce(string_agg(int8send(octet_length(convert_to(to_jsonb(r)::text,'UTF8'))::bigint)||convert_to(to_jsonb(r)::text,'UTF8'),''::bytea ORDER BY to_jsonb(r)::text COLLATE \"C\"),''::bytea)),'hex')) AS value FROM \"" + schema + "\".\"" + table + "\" r")
        sequences = json.loads(self.query("SELECT coalesce(json_agg(json_build_array(n.nspname,c.relname) ORDER BY n.nspname,c.relname),'[]') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind='S' AND n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_';", connection=self.db, readonly=True))
        sequence_pieces = []
        for schema, name in sequences:
            H.require(H.IDENTIFIER.fullmatch(schema) is not None and H.IDENTIFIER.fullmatch(name) is not None, "test_sequence_identifier_invalid")
            qualified = schema + "." + name
            sequence_pieces.append("SELECT " + H.sql_literal(qualified) + " AS name,jsonb_build_object('last_value',last_value,'is_called',is_called,'definition',(SELECT to_jsonb(s) FROM pg_sequence s WHERE seqrelid=" + H.sql_literal(qualified) + "::regclass)) AS value FROM \"" + schema + "\".\"" + name + "\"")
        sequence_sql = "(SELECT jsonb_object_agg(name,value) FROM (" + " UNION ALL ".join(sequence_pieces) + ") s)" if sequence_pieces else "'{}'::jsonb"
        sql = "SELECT jsonb_build_object('tables',(SELECT jsonb_object_agg(name,value) FROM (" + " UNION ALL ".join(pieces) + ") t),'sequences'," + sequence_sql + ");"
        return json.loads(self.query(sql, connection=self.db, readonly=True))

    def test_exact_import_then_replay_no_rows_or_timestamps_changed(self):
        plan = mapped([option(" raw\n@DELIMITER@ '<>& ", "Notice"), option("")], [setup()], [read()])
        other_before = self.other_facts()
        self.apply(plan)
        other_after_import = self.other_facts()
        before = self.facts()
        self.apply(plan)
        self.assertEqual(self.facts(), before)
        other_after_replay = self.other_facts()
        self.assertEqual(other_before, other_after_import)
        self.assertEqual(other_before, other_after_replay)
        H.write_private(self.artifacts, "exact-replay-other-facts.private.json", H.canonical({
                        "before": other_before, "after_import": other_after_import, "after_replay": other_after_replay}) + "\n")
        self.assertEqual({r["key"]: r["value"] for r in before["options"]}, {r["key"]: r["value"] for r in plan["options"]})
        self.assertEqual(before["reads"][0]["read_at"], read()["read_at"])
        epoch = self.query("SELECT extract(epoch FROM initialized_at)::bigint FROM core_console.initialization;", connection=self.db, readonly=True).strip()
        self.assertEqual(int(epoch), setup()["initialized_at"])

    def test_missing_setup_remains_uninitialized(self):
        self.apply(mapped([option()], [], []))
        self.assertEqual(self.facts()["setup"], [])

    def test_explicit_login_enters_nologin_owner_without_role_mutation(self):
        self.assertIn("login_role", self.db, "NOLOGIN owner fixture is required for final acceptance")
        sql = "SELECT jsonb_build_object('owner',current_user,'login',session_user,'owner_can_login',(SELECT rolcanlogin FROM pg_roles WHERE rolname=current_user),'login_can_login',(SELECT rolcanlogin FROM pg_roles WHERE rolname=session_user));"
        before_role = json.loads(self.query(sql, connection=self.db, readonly=True))
        self.assertEqual(before_role, {"owner": self.db["role"], "login": self.db["login_role"],
                                       "owner_can_login": False, "login_can_login": True})
        before_other = self.other_facts()
        self.apply()
        self.assertEqual(json.loads(self.query(sql, connection=self.db, readonly=True)), before_role)
        self.assertEqual(self.other_facts(), before_other)

    def test_login_without_membership_cannot_assume_owner(self):
        runtime = self.fixture_receipt["roles"][self.fixture["runtime"]]
        password = H.private_backup_path(Path(runtime["password_file"])).read_text().strip()
        escape = lambda value: str(value).replace("\\", "\\\\").replace(":", "\\:")
        pgpass = H.write_private(self.artifacts, "nonmember-" + self.database + ".pgpass.private",
                                ":".join(map(escape, (runtime["socket_directory"], runtime["port"],
                                                       self.fixture["database"], runtime["role"], password))) + "\n")
        connection = {**self.connection, "login_role": runtime["role"], "pgpass_file": str(pgpass)}
        before = self.other_facts()
        with self.assertRaisesRegex(MODULE.MappingError, r"pg-\d+_database_command_failed"):
            self.query("SELECT current_user,session_user;", connection=connection, readonly=True)
        error = (self.artifacts / ("pg-" + str(type(self).counter) + ".stderr.private.txt")).read_text()
        self.assertIn("permission denied to set role", error)
        self.assertEqual(self.other_facts(), before)

    def test_sequence_proof_observes_restart_when_pg_sequences_hides_value(self):
        name = self.query("SELECT pg_get_serial_sequence('core_identity.users','id');", connection=self.db, readonly=True).strip()
        H.require(re.fullmatch(r"[a-z_][a-z_0-9]*\.[a-z_][a-z_0-9]*", name) is not None, "test_sequence_identifier_invalid")
        def visible_value():
            return self.query("SELECT coalesce(last_value::text,'NULL') FROM pg_sequences WHERE schemaname||'.'||sequencename=" + H.sql_literal(name) + ";", connection=self.db, readonly=True).strip()
        before = self.other_facts()
        self.assertIs(before["sequences"][name]["is_called"], False)
        self.assertEqual(visible_value(), "NULL")
        self.query("ALTER SEQUENCE " + name + " RESTART WITH 4321;", connection=self.db)
        after = self.other_facts()
        self.assertEqual(visible_value(), "NULL")
        self.assertIs(after["sequences"][name]["is_called"], False)
        self.assertEqual(after["sequences"][name]["last_value"], 4321)
        self.assertNotEqual(before["sequences"], after["sequences"])

    def test_existing_same_archive_identity_run_allowed_and_unchanged(self):
        before = self.facts()["runs"]
        self.apply()
        self.assertEqual([r for r in self.facts()["runs"] if r["migration_id"] == "identity-synthetic"], before)

    def test_missing_mapped_identity_rejects_without_any_console_or_registry_write(self):
        self.query("DELETE FROM core_identity.password_logins; DELETE FROM core_identity.developer_access; DELETE FROM core_identity.user_profiles; DELETE FROM core_billing.account_policies; DELETE FROM core_identity.users; DELETE FROM core_identity.accounts;", connection=self.db)
        before = self.facts()
        self.reject("identity current facts proof mismatch")
        self.assertEqual(self.facts(), before)

    def test_nonempty_console_or_foreign_registry_rejected(self):
        self.query("INSERT INTO core_console.options(key,value) VALUES('Notice','existing');", connection=self.db)
        before = self.facts()
        self.reject("console target is not empty")
        self.assertEqual(self.facts(), before)
        self.query("DELETE FROM core_console.options; INSERT INTO core_migration.import_runs(migration_id,source_archive_sha256,mapping_sha256,manifest) VALUES('foreign','" + "e"*64 + "','" + "f"*64 + "','{\"scope\":\"identity-only\"}');", connection=self.db)
        before = self.facts()
        self.reject("target belongs to another import")
        self.assertEqual(self.facts(), before)

    def test_actual_catalog_drift_rejected_even_when_old_markers_unchanged(self):
        self.query("ALTER TABLE core_console.options ADD COLUMN unrecognized TEXT;", connection=self.db)
        before = self.facts()
        self.reject("installed core contract or actual catalog mismatch")
        self.assertEqual(self.facts(), before)

    def test_mutated_replay_or_receipt_data_rejected_without_repair(self):
        self.apply()
        self.query("UPDATE core_console.announcement_reads SET read_at=read_at+1;", connection=self.db)
        before = self.facts()
        self.reject("console exact readback mismatch")
        self.assertEqual(self.facts(), before)

    def test_write_failure_rolls_back_options_setup_receipts_registry(self):
        # A CHECK replacement modifies actual catalog, so bind the fresh trusted
        # test contract to that catalog to exercise a failure after option INSERT.
        self.query("ALTER TABLE core_console.announcement_reads ADD CONSTRAINT synthetic_write_failure CHECK(read_at<10);", connection=self.db)
        self.catalog = self.query(MODULE.catalog_hash_sql(self.contract) + ";", connection=self.db, readonly=True).strip()
        self.query("UPDATE core_meta.runtime_contract SET catalog_fingerprint=decode('" + self.catalog + "','hex');", connection=self.db)
        self.target["catalog_fingerprint_sha256"] = self.catalog
        before = self.facts()
        self.reject("synthetic_write_failure")
        self.assertEqual(self.facts(), before)

    def test_source_hash_detects_same_count_changes_only_in_whitelist(self):
        self.query("INSERT INTO core_console.options(key,value) VALUES('Notice','original'),('ignored-secret','synthetic-only');", connection=self.db)
        sql = MODULE.source_table_hash_sql("core_console", "options") + ";"
        first = self.query(sql, connection=self.db, readonly=True)
        self.query("UPDATE core_console.options SET value='different' WHERE key='ignored-secret';", connection=self.db)
        self.assertEqual(first, self.query(sql, connection=self.db, readonly=True))
        self.query("UPDATE core_console.options SET value='changed' WHERE key='Notice';", connection=self.db)
        self.assertNotEqual(first, self.query(sql, connection=self.db, readonly=True))

    def test_missing_identity_registry_rejected_even_when_user_ids_exist(self):
        self.query("DELETE FROM core_migration.import_runs;", connection=self.db)
        before = self.facts()
        self.reject("required identity import receipt missing")
        self.assertEqual(self.facts(), before)

    def test_identity_registry_wrong_archive_rejected(self):
        self.query("UPDATE core_migration.import_runs SET source_archive_sha256='" + "c" * 64 + "';", connection=self.db)
        before = self.facts()
        self.reject("identity import archive mismatch")
        self.assertEqual(self.facts(), before)

    def test_identity_registry_wrong_mapping_rejected(self):
        self.query("UPDATE core_migration.import_runs SET mapping_sha256='" + "c" * 64 + "';", connection=self.db)
        before = self.facts()
        self.reject("identity import mapping proof mismatch")
        self.assertEqual(self.facts(), before)

    def test_identity_registry_wrong_id_map_manifest_rejected(self):
        self.query("UPDATE core_migration.import_runs SET manifest=jsonb_set(manifest,'{source_target_id_map_sha256}',to_jsonb('" + "c" * 64 + "'::text));", connection=self.db)
        before = self.facts()
        self.reject("identity import manifest proof mismatch")
        self.assertEqual(self.facts(), before)

    def test_same_user_ids_different_profile_cannot_absorb_receipts(self):
        self.query("UPDATE core_identity.user_profiles SET username='different-person-same-id' WHERE user_id=7;", connection=self.db)
        self.assertEqual(self.query("SELECT id FROM core_identity.users;", connection=self.db, readonly=True).strip(), "7")
        before = self.facts()
        self.reject("identity current facts proof mismatch")
        self.assertEqual(self.facts(), before)

    def test_same_user_ids_different_developer_facts_rejected(self):
        self.query("UPDATE core_identity.developer_access SET granted=true WHERE user_id=7;", connection=self.db)
        before = self.facts()
        self.reject("identity current facts proof mismatch")
        self.assertEqual(self.facts(), before)

    def test_same_user_ids_different_account_facts_rejected(self):
        self.query("UPDATE core_identity.accounts SET service_level=1;", connection=self.db)
        before = self.facts()
        self.reject("identity current facts proof mismatch")
        self.assertEqual(self.facts(), before)

    def test_transaction_binds_actual_socket_port(self):
        wrong = {**self.identity, "port": self.identity["port"] + 1}
        sql = build(contract=self.contract, target=self.target, identity=wrong, proof=self.proof, connection=self.db)
        before = self.facts()
        with self.assertRaisesRegex(MODULE.MappingError, r"pg-\d+_database_command_failed"):
            self.query(self.idle_sql(self.database) + sql, connection=self.db)
        stderr = (self.artifacts / ("pg-" + str(type(self).counter) + ".stderr.private.txt")).read_text()
        self.assertIn("target instance identity mismatch", stderr.splitlines()[0])
        self.assertEqual(self.facts(), before)

    def test_concurrent_owned_client_prevents_import(self):
        environment = {key: value for key, value in os.environ.items() if not key.startswith("PG")}
        environment.update(H.socket_connection(self.db))
        environment.update(PGCONNECT_TIMEOUT="5", PGOPTIONS=H.psql_options(self.db, readonly=True))
        path = self.artifacts / "owned-client.stderr.private.txt"
        with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600), "w") as error:
            process = subprocess.Popen(["psql", "-X", "--no-password", "-qAt", "-v", "ON_ERROR_STOP=1"],
                                       stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=error, text=True, env=environment)
            backend_pid = None
            try:
                process.stdin.write("SELECT pg_backend_pid();\nSELECT pg_sleep(30);\n")
                process.stdin.flush()
                self.assertTrue(select.select([process.stdout], [], [], 5)[0], "owned synthetic client did not become ready")
                backend_pid = int(process.stdout.readline().strip())
                self.known_backends.setdefault(self.database, set()).add(backend_pid)
                self.backend_history.append({"label": "owned-competing-client", "database": self.database, "pid": backend_pid})
                before = self.facts()
                self.reject("target has another active client")
                self.assertEqual(self.facts(), before)
            finally:
                try:
                    # Signal only this Popen child. libpq cancels its own query;
                    # the restricted owner has no pg_signal_backend privilege.
                    if process.poll() is None:
                        process.send_signal(signal.SIGINT)
                    process.stdin.close()
                    process.wait(timeout=5)
                finally:
                    process.stdout.close()


if __name__ == "__main__":
    unittest.main()
