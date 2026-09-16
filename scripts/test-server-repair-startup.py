#!/usr/bin/env python3
"""Offline tests for the redacted, read-only incident diagnostic."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import types
import unittest

ROOT = Path(__file__).resolve().parents[1]
SHELL = ROOT / "scripts/server-repairs/inspect-startup-343.sh"
TEXT = SHELL.read_text()
SOURCE = TEXT.split("<<'PY'\n", 1)[1].rsplit("\nPY\n", 1)[0]
module = types.ModuleType("startup_diagnostic")
exec(compile(SOURCE, str(SHELL), "exec"), module.__dict__)


class StartupDiagnosticTests(unittest.TestCase):
    def test_shell_syntax(self):
        subprocess.run(["bash", "-n", str(SHELL)], check=True)

    def test_exact_missing_table(self):
        result = module.classify(["failed to configure routes: red packet schema verification failed: missing table red_packets"])
        self.assertEqual(result["missing_red_packet_tables"], ["red_packets"])
        self.assertEqual(result["categories"]["route_configuration"], 1)

    def test_actual_fatal_log_brackets_and_plain_messages(self):
        for table in module.TABLES:
            for wrapped in (False, True):
                with self.subTest(table=table, wrapped=wrapped):
                    message = "failed to configure routes: configure red packet routes: red packet schema verification failed: missing table " + table
                    if wrapped:
                        message = "[FATAL] 2026/09/16 - 18:05:00 | [" + message + "] "
                    result = module.classify([message])
                    self.assertEqual(result["missing_red_packet_tables"], [table])
                    self.assertEqual(result["categories"]["red_packet_schema"], 1)

    def test_source_derived_route_labels_do_not_export_private_suffixes(self):
        cases = {
            "oauth_groups_invalid_json": "configure OAuth server: OAUTH_SERVER_GROUPS must be an explicit JSON string array",
            "oauth_issuer_invalid": "initialize OAuth server: oauth server: issuer must be a canonical HTTPS DNS origin",
            "frontend_current_link_invalid": "configure packaged frontend: LMM_API_FRONTEND_DIR symlink must be an atomic current link",
            "frontend_path_resolve_failure": "configure packaged frontend: resolve frontend directory:",
            "frontend_nested_symlink": "configure packaged frontend: frontend directory contains a symlink:",
            "red_packet_apply_failure": "configure red packet routes: migrate red packet schema:",
            "red_packet_mode_invalid": "configure red packet routes: LMM_DB_MIGRATION_MODE must be exactly apply or verify",
            "red_packet_mode_unsupported": "configure red packet routes: unsupported red packet migration mode",
        }
        for label, message in cases.items():
            with self.subTest(label=label):
                result = module.classify(["[FATAL] now | [failed to configure routes: " + message + " PRIVATE_TOKEN_SENTINEL postgres://private-user:private-password@private-host/db]"])
                self.assertEqual(result["categories"][label], 1)
                for secret in ("PRIVATE_TOKEN_SENTINEL", "private-user", "private-password", "private-host", "postgres://"):
                    self.assertNotIn(secret, json.dumps(result))

    def test_brackets_do_not_allow_unknown_table_suffixes(self):
        for name in ("red_packets_secret", "red_packetsx", "private_customer_table"):
            result = module.classify(["[red packet schema verification failed: missing table " + name + "]"])
            self.assertEqual(result["missing_red_packet_tables"], [])
            self.assertNotIn(name, json.dumps(result))

    def test_secret_log_content_is_never_exported(self):
        secret = "PRIVATE_TOKEN_SENTINEL"
        result = module.classify(["failed to initialize resources: " + secret + " postgresql://user:password@private-host/db", "::error::" + secret])
        encoded = json.dumps(result)
        for forbidden in (secret, "private-host", "postgresql", "password", "::error::"):
            self.assertNotIn(forbidden, encoded)
        self.assertEqual(result["categories"], {"resource_initialization": 1})

    def test_generic_connection_failure_is_not_database_diagnosis(self):
        result = module.classify(["curl: (7) Failed to connect to localhost port 3000"])
        self.assertEqual(result["categories"], {"unclassified_connection_message": 1})
        self.assertEqual(result["missing_red_packet_tables"], [])

    def test_unknown_table_is_not_published(self):
        result = module.classify(["red packet schema verification failed: missing table secret_customer_table"])
        self.assertEqual(result["missing_red_packet_tables"], [])
        self.assertNotIn("secret_customer", json.dumps(result))

    def test_non_text_entries_and_oversized_message(self):
        self.assertEqual(module.classify([None, [], {}])["messages_scanned"], 0)
        result = module.classify(["x" * 17000 + "failed to initialize resources:"])
        self.assertEqual(result["categories"], {})

    def test_column_failure_not_invented_as_table_failure(self):
        result = module.classify(["red packet schema verification failed: missing red_packets.description"])
        self.assertEqual(result["categories"], {"red_packet_schema": 1})
        self.assertEqual(result["missing_red_packet_tables"], [])

    def test_tail_is_bounded(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "log"
            path.write_bytes(b"x" * (module.LIMIT + 100) + b"\nlast\n")
            lines, truncated = module.bounded_tail(path)
            self.assertTrue(truncated)
            self.assertEqual(lines, ["last"])

    def test_symlink_and_fifo_are_rejected_without_blocking(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "target").write_text("private")
            (root / "symlink").symlink_to(root / "target")
            os.mkfifo(root / "fifo")
            with self.assertRaises(OSError):
                module.bounded_tail(root / "symlink")
            with self.assertRaises(ValueError):
                module.bounded_tail(root / "fifo")

    def test_script_does_not_perform_service_or_database_changes(self):
        for forbidden in ("systemctl restart", "systemctl stop", "migrate --apply", "psql", "rm -", "deploy production confirm", "deploy production rollback"):
            self.assertNotIn(forbidden, TEXT)
        self.assertIn('"production_changed": False', TEXT)


if __name__ == "__main__":
    unittest.main(verbosity=2)
