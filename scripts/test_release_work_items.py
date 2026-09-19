"""Offline regressions for the mandatory pre-publication work-item barrier."""

import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPTS = Path(__file__).resolve().parent
SCRIPT = SCRIPTS / "verify-release-work-items.py"
spec = importlib.util.spec_from_file_location("release_work_items", SCRIPT)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
REPOSITORY = "TokenNotIncluded/api.lmm.best"


class ReleaseWorkItemsTests(unittest.TestCase):
    def runner(self, payload, code=0):
        def run(command, **options):
            self.assertEqual(command[2:6], ["--hostname", "github.com", "--method", "GET"])
            self.assertEqual(command[-1], f"repos/{REPOSITORY}/issues?state=open&per_page=1")
            self.assertEqual(options["timeout"], 30)
            self.assertTrue(options["capture_output"])
            self.assertFalse(options["check"])
            return subprocess.CompletedProcess(command, code, json.dumps(payload), "secret-stderr")
        return run

    def test_empty_first_page_passes(self):
        module.verify(REPOSITORY, self.runner([]))

    def test_open_issue_blocks_release(self):
        with self.assertRaisesRegex(module.ReleaseBlocked, "open issue #271"):
            module.verify(REPOSITORY, self.runner([{"number": 271, "state": "open"}]))

    def test_open_pr_including_drafts_blocks_release(self):
        for draft in (False, True):
            with self.subTest(draft=draft), self.assertRaisesRegex(module.ReleaseBlocked, "pull request #339"):
                module.verify(REPOSITORY, self.runner([{
                    "number": 339, "state": "open", "draft": draft, "pull_request": {},
                }]))

    def test_invalid_collection_never_passes(self):
        for payload in ({}, {"message": "Not Found"}, None, True, "[]", 0, [{}, {}]):
            with self.subTest(payload=payload), self.assertRaises(module.ReleaseBlocked):
                module.verify(REPOSITORY, self.runner(payload))

    def test_malformed_or_closed_items_never_pass(self):
        for item in (None, {}, [], "open", {"number": True, "state": "open"},
                     {"number": -1, "state": "open"}, {"number": "1", "state": "open"},
                     {"number": 1, "state": "closed"}, {"number": 1},
                     {"number": 1, "state": "open", "pull_request": None}):
            with self.subTest(item=item), self.assertRaises(module.ReleaseBlocked):
                module.verify(REPOSITORY, self.runner([item]))

    def test_api_failure_cannot_be_mistaken_for_empty_repository(self):
        with self.assertRaisesRegex(module.ReleaseBlocked, "query failed"):
            module.verify(REPOSITORY, self.runner([], code=1))

    def test_invalid_json_blocks_release(self):
        def run(*args, **kwargs):
            return subprocess.CompletedProcess(args[0], 0, "invalid-json", "")
        with self.assertRaisesRegex(module.ReleaseBlocked, "invalid work-item response"):
            module.verify(REPOSITORY, run)

    def test_timeout_and_missing_cli_block_release(self):
        for failure in (FileNotFoundError("secret-path"), subprocess.TimeoutExpired("gh", 30)):
            def run(*args, **kwargs):
                raise failure
            with self.subTest(error=type(failure)), self.assertRaisesRegex(module.ReleaseBlocked, "could not verify"):
                module.verify(REPOSITORY, run)

    def test_invalid_repository_is_rejected_before_network(self):
        def forbidden(*args, **kwargs):
            self.fail("invalid repository must not reach the network")
        for repository in (None, "", "owner", "../repo", "owner/repo?state=closed", "owner/repo/extra", "owner/repo\n"):
            with self.subTest(repository=repository), self.assertRaises(module.ReleaseBlocked):
                module.verify(repository, forbidden)

    def test_issue_content_is_not_logged_or_executed(self):
        payload = [{"number": 1, "state": "open", "title": "\n::error::injected", "body": "secret-body"}]
        with self.assertRaises(module.ReleaseBlocked) as caught:
            module.verify(REPOSITORY, self.runner(payload))
        self.assertNotIn("injected", str(caught.exception))
        self.assertNotIn("secret", str(caught.exception))

    def test_real_cli_exit_status_and_sanitized_errors(self):
        with tempfile.TemporaryDirectory() as directory:
            executable = Path(directory) / "gh"
            executable.write_text(
                f"#!{sys.executable}\nimport os,sys\n"
                "print(os.environ['TEST_REPLY'])\n"
                "print('secret-cli-diagnostic', file=sys.stderr)\n"
                "sys.exit(int(os.environ['TEST_EXIT']))\n", encoding="utf-8",
            )
            executable.chmod(0o700)
            env = {**os.environ, "PATH": directory + os.pathsep + os.environ.get("PATH", ""), "GITHUB_REPOSITORY": REPOSITORY}
            for raw, code, expected in (("[]", 0, 0), ('[{"number":1,"state":"open"}]', 0, 1), ("[]", 1, 1), ("not-json", 0, 1)):
                with self.subTest(raw=raw, code=code):
                    result = subprocess.run([sys.executable, "-B", str(SCRIPT)], env={**env, "TEST_REPLY": raw, "TEST_EXIT": str(code)}, capture_output=True, text=True, timeout=10, check=False)
                    self.assertEqual(result.returncode, expected)
                    self.assertNotIn("secret-cli-diagnostic", result.stderr)

    def test_automatic_release_workflows_are_removed_for_manual_deployment(self):
        for component in ("go", "web"):
            with self.subTest(component=component):
                self.assertFalse(
                    (SCRIPTS.parent / f".github/workflows/release-{component}.yml").exists()
                )

    def test_contract_suite_is_part_of_blocking_ci(self):
        ci = (SCRIPTS.parent / ".github/workflows/ci.yml").read_text()
        self.assertIn("python3 -B -m unittest discover -s scripts -p test_release_work_items.py", ci)


if __name__ == "__main__":
    unittest.main()
