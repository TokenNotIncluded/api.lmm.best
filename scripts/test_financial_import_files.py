"""Private files and complete prepare flow; only target query transport is a fake."""
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import financial_import as F
from test_financial_import import approval_fixture, material_fixture, snapshot_fixture, source_fixture
from test_migrate_legacy_financial import tables, user


class PrivateFileFixture:
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.addCleanup(patch.stopall)
        patch.object(F.IDENTITY, "BACKUP_ROOT", self.root).start()
        self.input = self.root / "source"
        self.input.mkdir(mode=0o700)
        self.source = source_fixture()
        self.material = material_fixture(self.source)
        self.files = self.material["source_files"]
        self.write("archive", "synthetic-frozen-archive")
        # This file-verifier fixture has an empty synthetic catalog. The prepare
        # fixture below provides the complete catalog and real export framing.
        metadata = self.input / "metadata"
        metadata.mkdir(mode=0o700)
        catalog = F.write_private(metadata, "native-catalog-metadata.jsonl", "[]\n" * 7)
        baseline = F.write_private(metadata, "native-table-row-count-baseline.jsonl", "")
        self.metadata_hashes = {path.name: F.IDENTITY.sha256_file(path) for path in (catalog, baseline)}
        self.write("source_receipt", {"started_utc": "20261001T000000Z", "source": {"schema": "public"},
            "private_metadata_files": [str(catalog), str(baseline)]})
        self.write("financial_plan", self.material["source_plan"])
        self.write("source_review", self.material["source_review"])
        self.write("identity_manifest", self.material["identity_import"]["manifest"])
        id_map = "".join(F.canonical({"source_user_id": uid, "target_user_id": uid,
            "target_personal_account_id": aid}) + "\n" for uid, aid in self.source["identities"].items())
        self.write("identity_map", id_map)
        self.write("source_export", "synthetic-export-not-connected\n")
        self.write("source_export_proof", {"source_archive_sha256": self.material["source_archive_sha256"],
            "source_export_sha256": self.files["source_export"]["sha256"], "metadata_hashes": self.metadata_hashes})
        self.write("reservations", self.source["reservations"])

    def write(self, name, value):
        path = self.input / (name + ".private")
        if path.exists():
            path.unlink()
        content = value if type(value) is str else F.canonical(value) + "\n"
        F.write_private(self.input, path.name, content)
        self.files[name] = {"path": str(path), "sha256": F.sha(content.encode())}
        return path

    def output(self, name="output"):
        output = self.root / name
        output.mkdir(mode=0o700)
        return output


class PrivateFilesTests(PrivateFileFixture, unittest.TestCase):
    def test_real_private_files_validate_without_any_database_call(self):
        with patch.object(F, "run_sql") as run:
            F.validate_material(self.material)
            F.verify_source_files(self.material)
            run.assert_not_called()
        self.assertTrue(all(Path(row["path"]).stat().st_mode & 0o777 == 0o600 for row in self.files.values()))

    def test_changed_source_bytes_are_rejected_even_when_plan_is_unchanged(self):
        path = Path(self.files["source_export"]["path"])
        path.write_text("tampered-export")
        with self.assertRaisesRegex(F.Error, "source_file_changed"):
            F.verify_source_files(self.material)

    def test_private_mode_and_symlink_are_enforced(self):
        path = Path(self.files["source_export"]["path"])
        path.chmod(0o644)
        with self.assertRaisesRegex(F.Error, "permissions_unsafe"):
            F.verify_source_files(self.material)
        path.chmod(0o600)
        link = self.input / "symbolic"
        link.symlink_to(path)
        self.files["source_export"]["path"] = str(link)
        with self.assertRaisesRegex(F.Error, "symlink_rejected"):
            F.verify_source_files(self.material)

    def test_rehashed_file_cannot_replace_bound_plan_or_review_with_equal_other_types(self):
        for name, key, value in (("financial_plan", "format_version", True),
                                 ("source_review", "credits_per_usd", 500000.0),
                                 ("identity_manifest", "format_version", 2.0)):
            with self.subTest(name=name):
                path = Path(self.files[name]["path"])
                before = path.read_text()
                data = F.PLANNER.parse_json(before)
                data[key] = value
                self.write(name, data)
                with self.assertRaisesRegex(F.Error, "source_file_content_mismatch"):
                    F.verify_source_files(self.material)
                self.write(name, before)

    def test_rehashed_id_map_cannot_change_account_ownership(self):
        self.write("identity_map", '\n'.join(F.canonical({"source_user_id": uid, "target_user_id": uid,
            "target_personal_account_id": aid + 4}) for uid, aid in self.source["identities"].items()))
        with self.assertRaisesRegex(F.Error, "source_id_map_file_mismatch"):
            F.verify_source_files(self.material)

    def test_rehashed_export_proof_and_observation_cannot_change_source_binding(self):
        self.write("source_export_proof", {"source_archive_sha256": "0" * 64,
            "source_export_sha256": self.files["source_export"]["sha256"]})
        with self.assertRaisesRegex(F.Error, "source_export_file_mismatch"):
            F.verify_source_files(self.material)
        self.write("source_export_proof", {"source_archive_sha256": self.material["source_archive_sha256"],
            "source_export_sha256": self.files["source_export"]["sha256"]})
        self.write("source_receipt", {"started_utc": "20261002T000000Z"})
        with self.assertRaisesRegex(F.Error, "source_observation_or_reservation_mismatch"):
            F.verify_source_files(self.material)

    def test_unexpected_post_submission_verifier_error_is_unknown_not_refused(self):
        connection = {"role": "a03_owner", "login_role": "a03_operator", "set_role": "a03_owner",
                      "database": "a03_test", "port": 5432}
        sql = F.build_apply_sql(self.material)
        result = {"status": "COMMITTED", "migration_id": self.material["migration_id"],
                  "material_sha256": F.digest(self.material)}
        actual_verify = F.verify_source_files
        for index, error in enumerate((KeyError("synthetic secret field"), RuntimeError("synthetic verifier defect"))):
            calls = []
            def verify(material):
                calls.append(True)
                if len(calls) == 1:
                    actual_verify(material)
                else:
                    raise error
            with self.subTest(error=type(error).__name__), \
                 patch.object(F.IDENTITY, "socket_connection", return_value={}), \
                 patch.object(F, "verify_source_files", side_effect=verify), \
                 patch.object(F, "run_sql", return_value=(0, F.canonical(result))) as run:
                outcome = F.execute(self.material, approval=approval_fixture(self.material, sql),
                    original_sql=sql, connection=connection, output=self.output("attempt" + str(index)))
                self.assertEqual(outcome, {**result, "status": "UNKNOWN"})
                run.assert_called_once()


class PrepareFilesTests(PrivateFileFixture, unittest.TestCase):
    # These use real archive/export/catalog/ID-map file parsing and hashing. The
    # transport returns a synthetic target snapshot; this is not PG evidence.
    def setUp(self):
        super().setUp()
        self.tables = tables(users=[user(71, quota=-120), user(72, quota=0, aff_quota=-5),
            user(73, quota=43, aff_quota=0)], tokens=[], user_subscriptions=[], subscription_plans=[])
        self.source_instance = {"database": "old_snapshot", "role": "readonly_source", "database_oid": "99",
            "postmaster_started_at": "2026-09-30 00:00:00+00", "server_version_num": "170006",
            "port": 5433, "tcp_connection": False, "read_only": "on", "schema_owner": "source_owner"}
        columns = {name: sorted(rows[0]) if rows else (["key", "value"] if name == "options" else ["id"])
                   for name, rows in self.tables.items()}
        column_rows = [{"schema": "public", "table_name": name, "column_name": column}
                       for name in sorted(columns) for column in columns[name]]
        constraints = [{"schema": "public", "table_name": name, "contype": "p", "definition": "PRIMARY KEY (id)"}
                       for name in sorted(columns) if "id" in columns[name]]
        catalog = F.write_private(self.input, "native-catalog-metadata.jsonl", "\n".join(
            F.canonical(value) for value in ([], [], column_rows, constraints, [], [], [])) + "\n")
        counts = F.write_private(self.input, "native-table-row-count-baseline.jsonl", "\n".join(
            F.canonical({"table": name, "row_count": len(rows)}) for name, rows in sorted(self.tables.items())) + "\n")
        raw = [F.canonical({"kind": "identity", "row": self.source_instance}),
               F.canonical({"kind": "schema", "row": {"schema": "public", "schema_owner": "source_owner"}})]
        raw += [F.canonical({"kind": "row", "table": name, "row": row})
                for name, rows in sorted(self.tables.items()) for row in rows]
        export = F.write_private(self.input, "source-financial-export.private.jsonl", "\n".join(raw) + "\n")
        self.files["source_export"] = {"path": str(export), "sha256": F.IDENTITY.sha256_file(export)}
        self.receipt = {"status": "snapshot-restored-baseline-verified-frozen-and-classified",
            "started_utc": "20261001T000000Z", "archive": self.files["archive"],
            "private_metadata_files": [str(catalog), str(counts)], "source": {"schema": "public"},
            "readonly_reader": {"database": "old_snapshot", "role": "readonly_source", "port": 5433},
            "source_instance_identity": {**self.source_instance, "listen_addresses": ""}}
        self.write("source_receipt", self.receipt)
        proof = {"source_archive_sha256": self.material["source_archive_sha256"],
            "source_export_sha256": F.IDENTITY.sha256_file(export),
            "metadata_hashes": {path.name: F.IDENTITY.sha256_file(path) for path in (catalog, counts)},
            "source_column_coverage_exact": True, "source_row_baseline_exact": True,
            "selected_table_counts": {name: len(rows) for name, rows in self.tables.items()},
            "source_instance": self.source_instance}
        self.write("source_export_proof", proof)
        schema = {name: [row for row in column_rows if row["table_name"] == name] for name in columns}
        primary = F.PLANNER.primary_keys_from_catalog(constraints, {name: set(items) for name, items in columns.items()}, "public")
        plan = F.PLANNER.plan_financial(self.tables, self.source["identities"],
            source_sha256=self.material["source_archive_sha256"], identity_provenance=self.source["plan"]["identity_provenance"],
            primary_keys=primary, column_schema=schema)
        self.source["plan"] = plan
        for item in (self.source["review"], self.source["reservations"]):
            item["mapping_sha256"] = plan["mapping_sha256"]
        self.write("financial_plan", plan)
        self.write("source_review", self.source["review"])
        self.write("reservations", self.source["reservations"])
        self.snapshot = snapshot_fixture(self.source)
        target = {"status": "initialized-identity-bound-financial-target", "application_or_worker_started": False,
            "migration_id": "synthetic-financial", "source_archive_sha256": plan["source_archive_sha256"],
            "connection": {"database": "a03_test", "role": "a03_owner", "login_role": "a03_operator", "set_role": "a03_owner", "port": 5432},
            "instance_identity": self.snapshot["instance"],
            "contract": {**F.IDENTITY.current_contract(), "catalog_fingerprint_sha256": self.snapshot["catalog_fingerprint_sha256"]}}
        target_path = F.write_private(self.input, "target.private.json", F.canonical(target))
        self.args = SimpleNamespace(plan=Path(self.files["financial_plan"]["path"]),
            source_receipt=Path(self.files["source_receipt"]["path"]), export_proof=Path(self.files["source_export_proof"]["path"]),
            identity_map=Path(self.files["identity_map"]["path"]), identity_manifest=Path(self.files["identity_manifest"]["path"]),
            source_review=Path(self.files["source_review"]["path"]), reservations=Path(self.files["reservations"]["path"]),
            target_receipt=target_path, migration_id="synthetic-financial", runtime_role=["a03_runtime"])
        self.source["files"] = self.files
        self.material = material_fixture(self.source, self.snapshot)
        self.files = self.material["source_files"]

    def prepare(self, output):
        with patch.object(F, "run_sql", side_effect=[
            (0, F.canonical(self.snapshot["state"]["inventory"])), (0, F.canonical(self.snapshot))]) as run, \
             patch.object(F.IDENTITY, "psql", side_effect=AssertionError("source SQL is forbidden")):
            result = F.prepare(self.args, output)
            self.assertEqual(run.call_count, 2)
            for call in run.call_args_list:
                self.assertEqual(call.args[0]["database"], "a03_test")
                self.assertIs(call.kwargs["readonly"], True)
            return result

    def test_complete_prepare_rebuilds_real_private_export_and_never_auto_approves(self):
        output = self.output()
        result = self.prepare(output)
        self.assertEqual(result["status"], "PREPARED_NOT_APPLIED")
        self.assertEqual(result["target_writes"], 0)
        material = F.PLANNER.parse_json(F.read_private(output / "material.private.json"))
        F.validate_material(material)
        F.verify_source_files(material)
        approval = F.PLANNER.parse_json(F.read_private(output / "approval-required.private.json"))
        with self.assertRaisesRegex(F.Error, "offline_approval_mismatch"):
            F.verify_approval(material, F.read_private(output / "opening.private.sql"), approval)
        self.assertEqual(F.PLANNER.parse_json(F.read_private(output / "source-export-proof.private.json"))["source_queries_this_run"], 0)

    def test_source_and_target_same_oid_boot_with_different_timestamp_spelling_rejected(self):
        self.snapshot["instance"]["database_oid"] = "99"
        self.snapshot["instance"]["postmaster_started_at"] = "2026-09-30T00:00:00+00:00"
        target = F.PLANNER.parse_json(F.read_private(self.args.target_receipt))
        target["instance_identity"] = self.snapshot["instance"]
        self.args.target_receipt.write_text(F.canonical(target))
        with self.assertRaisesRegex(F.Error, "source_target_same_database"):
            self.prepare(self.output())

    def prepared_attempt(self, name):
        output = self.output(name + "-prepared")
        self.prepare(output)
        material = F.PLANNER.parse_json(F.read_private(output / "material.private.json"))
        sql = F.read_private(output / "opening.private.sql")
        connection = F.PLANNER.parse_json(F.read_private(self.args.target_receipt))["connection"]
        return material, sql, connection

    def test_source_metadata_changed_after_prepare_never_reaches_apply(self):
        for index, name in enumerate(("native-catalog-metadata.jsonl", "native-table-row-count-baseline.jsonl")):
            with self.subTest(name=name):
                material, sql, connection = self.prepared_attempt("metadata-drift-" + str(index))
                path = self.input / name
                original = path.read_bytes()
                try:
                    # A byte-only change is enough: approval binds original bytes,
                    # not merely equivalent parsed columns or counts.
                    path.write_bytes(original + b"\n")
                    with patch.object(F.IDENTITY, "socket_connection", return_value={}), \
                         patch.object(F, "run_sql") as run:
                        with self.assertRaisesRegex(F.Error, "source_export_metadata_hash_mismatch"):
                            F.execute(material, approval=approval_fixture(material, sql), original_sql=sql,
                                connection=connection, output=self.output("metadata-drift-" + str(index) + "-apply"))
                        run.assert_not_called()
                finally:
                    path.write_bytes(original)

    def test_source_metadata_missing_after_prepare_never_reaches_apply(self):
        material, sql, connection = self.prepared_attempt("metadata-missing")
        path = self.input / "native-table-row-count-baseline.jsonl"
        path.unlink()
        with patch.object(F.IDENTITY, "socket_connection", return_value={}), \
             patch.object(F, "run_sql") as run:
            with self.assertRaises(FileNotFoundError):
                F.execute(material, approval=approval_fixture(material, sql), original_sql=sql,
                    connection=connection, output=self.output("metadata-missing-apply"))
            run.assert_not_called()

    def test_source_metadata_mode_or_symlink_changed_after_prepare_is_rejected(self):
        material, sql, connection = self.prepared_attempt("metadata-path")
        path = self.input / "native-catalog-metadata.jsonl"
        original = path.read_bytes()
        replacement = self.input / "catalog-copy.private"
        F.write_private(self.input, replacement.name, original.decode())
        for index, mode in enumerate(("public", "symlink")):
            with self.subTest(mode=mode):
                if mode == "public":
                    path.chmod(0o644)
                else:
                    path.unlink()
                    path.symlink_to(replacement)
                try:
                    with patch.object(F.IDENTITY, "socket_connection", return_value={}), \
                         patch.object(F, "run_sql") as run:
                        with self.assertRaisesRegex(F.Error, "permissions_unsafe|symlink_rejected"):
                            F.execute(material, approval=approval_fixture(material, sql), original_sql=sql,
                                connection=connection, output=self.output("metadata-path-apply-" + str(index)))
                        run.assert_not_called()
                finally:
                    path.unlink()
                    F.write_private(self.input, path.name, original.decode())

    def test_source_metadata_changed_after_submission_keeps_original_attempt_unknown(self):
        material, sql, connection = self.prepared_attempt("metadata-after-submit")
        expected = {"status": "COMMITTED", "migration_id": material["migration_id"],
                    "material_sha256": F.digest(material)}
        path = self.input / "native-table-row-count-baseline.jsonl"
        original = path.read_bytes()
        output = self.output("metadata-after-submit-apply")
        def transport(*args, **kwargs):
            path.write_bytes(original + b"\n")
            return 0, F.canonical(expected)
        with patch.object(F.IDENTITY, "socket_connection", return_value={}), \
             patch.object(F, "run_sql", side_effect=transport) as run:
            result = F.execute(material, approval=approval_fixture(material, sql), original_sql=sql,
                connection=connection, output=output)
            self.assertEqual(result, {**expected, "status": "UNKNOWN"})
            run.assert_called_once()
        attempt = F.PLANNER.parse_json(F.read_private(output / "attempt.private.json"))
        self.assertEqual(attempt["operation"], "apply")
        self.assertEqual(attempt["material_sha256"], F.digest(material))
        self.assertEqual(F.PLANNER.parse_json(F.read_private(output / "result.private.json")), result)
        # Restore the approved bytes, then resolve that same attempt. This is a
        # transport fake: it proves the driver selects read-only recovery SQL,
        # not that a PostgreSQL commit occurred.
        path.write_bytes(original)
        with patch.object(F.IDENTITY, "socket_connection", return_value={}), \
             patch.object(F, "run_sql", return_value=(0, F.canonical(expected))) as run:
            resolved = F.execute(material, approval=approval_fixture(material, sql), original_sql=sql,
                connection=connection, output=self.output("metadata-after-submit-resolve"), original_attempt=attempt)
            self.assertEqual(resolved, expected)
            self.assertEqual(run.call_args.args[1], F.build_resolve_sql(material, attempt["application_name"]))
            run.assert_called_once()

    def test_changed_catalog_metadata_is_rejected_before_target_query(self):
        path = self.input / "native-catalog-metadata.jsonl"
        path.write_text(path.read_text().replace('"column_name":"id"', '"column_name":"edited_id"'))
        with patch.object(F, "run_sql") as run, self.assertRaises(F.Error):
            F.prepare(self.args, self.output())
        run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
