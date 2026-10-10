import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import lab


class SafetyTests(unittest.TestCase):
    def test_image_must_be_immutable(self):
        for value in ("postgres:17", "postgres:latest", "postgres", "sha256:bad"):
            self.assertIsNone(lab.IMAGE_PATTERN.fullmatch(value))
        self.assertIsNotNone(lab.IMAGE_PATTERN.fullmatch("postgres@sha256:" + "a" * 64))
        self.assertIsNotNone(lab.IMAGE_PATTERN.fullmatch("sha256:" + "b" * 64))

    def test_git_blob_hash_format(self):
        self.assertEqual(lab.git_blob(b""), "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391")

    def test_literal_quotes_and_nul(self):
        self.assertEqual(lab.sql_literal("a'b"), "'a''b'")
        with self.assertRaises(ValueError):
            lab.sql_literal("a\0b")

    def test_same_operation_key_reconstructs_same_request(self):
        a = lab.ledger_request("retry:1", {"kind":"charge", "account_id":1, "amount_units":1})
        self.assertEqual(a, lab.ledger_request("retry:1", a["action"]))
        self.assertIn("SET LOCAL synchronous_commit=on", lab.ledger_sql(a))
        self.assertTrue(lab.ledger_sql(a).endswith("COMMIT;"))

    def test_invalid_operation_keys_rejected(self):
        for key in ("", "white space", "x' OR true", "a" * 129):
            with self.assertRaises(ValueError):
                lab.ledger_request(key, {})

    def test_missing_dependencies_block_without_database_commands(self):
        with tempfile.TemporaryDirectory() as d, mock.patch("lab.shutil.which", return_value=None), mock.patch("lab.subprocess.run") as run:
            result = lab.preflight(Path(d), None, None)
            self.assertEqual(result["status"], "blocked")
            self.assertFalse(result["database_tests_executed"])
            self.assertFalse(result["business_acceptance"])
            run.assert_not_called()

    def test_remote_docker_is_rejected(self):
        with tempfile.TemporaryDirectory() as d, mock.patch.dict(os.environ, {"DOCKER_HOST":"tcp://server:2376"}):
            result = lab.preflight(Path(d), "sha256:"+"a"*64, None)
            self.assertIn("remote Docker endpoints are prohibited", result["blockers"])

    def test_remote_ci_is_rejected(self):
        with tempfile.TemporaryDirectory() as d, mock.patch.dict(os.environ, {"GITHUB_ACTIONS":"true"}):
            result = lab.preflight(Path(d), "sha256:"+"a"*64, None)
            self.assertIn("remote CI environments are prohibited", result["blockers"])

    def test_wrong_source_cannot_be_run(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            for name in lab.SCHEMAS:
                (root/name).parent.mkdir(parents=True, exist_ok=True)
                (root/name).write_text("SELECT 1;")
            result = lab.preflight(root, "sha256:"+"a"*64, None)
            self.assertTrue(any("source differs" in item for item in result["blockers"]))

    def test_pgbench_does_not_enable_debug_flag(self):
        with tempfile.TemporaryDirectory() as d:
            instance = lab.Lab(Path(d), "sha256:"+"a"*64, Path(d)/"proxy", Path(d))
            try:
                with mock.patch.object(instance, "docker") as execute:
                    instance.client("pgbench", ["-c","1","-t","1"])
                    cmd = execute.call_args.args[0]
                    tail = cmd[cmd.index(instance.image)+1:]
                    self.assertNotIn("-d", tail)
                    self.assertEqual(tail[-1], "dp25_core")
            finally:
                instance.private.cleanup()

    def test_cleanup_refuses_unowned_resource(self):
        with tempfile.TemporaryDirectory() as d:
            instance = lab.Lab(Path(d), "sha256:"+"a"*64, Path(d)/"proxy", Path(d))
            instance.owned = [("volume", "some-other-volume")]
            response = mock.Mock(returncode=0, stdout=json.dumps([{"Labels":{lab.LABEL:"other-run"}}]))
            with mock.patch.object(instance, "docker", return_value=response) as execute:
                instance.close()
                self.assertEqual(execute.call_count, 1)
                self.assertTrue(instance.cleanup_errors)

    def test_generated_sql_uses_repository_ledger_function(self):
        request = lab.ledger_request("case:1", {"kind":"reserve","account_id":1,"amount_units":3})
        self.assertIn("core_billing.post_ledger(", lab.ledger_sql(request))
        self.assertNotIn("CREATE TABLE", lab.ledger_sql(request))


if __name__ == "__main__":
    unittest.main()
