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
