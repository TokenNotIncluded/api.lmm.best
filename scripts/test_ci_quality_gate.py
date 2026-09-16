"""Regression tests for fail-closed CI aggregation; no third-party packages."""

import json
import os
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from ci_quality_gate import REQUIRED_JOBS, check_needs, summary_text

SCRIPT = Path(__file__).with_name("ci_quality_gate.py")


def successful_jobs():
    return {job: {"result": "success", "outputs": {}} for job in REQUIRED_JOBS}


class QualityGateTests(unittest.TestCase):
    def test_all_required_jobs_succeed(self):
        results, errors = check_needs(successful_jobs())
        self.assertEqual(errors, [])
        self.assertEqual(set(results.values()), {"success"})

    def test_every_job_rejects_each_non_success_result(self):
        for job in REQUIRED_JOBS:
            for status in ("failure", "cancelled", "skipped", "neutral", "timed_out", ""):
                with self.subTest(job=job, status=status):
                    needs = successful_jobs()
                    needs[job]["result"] = status
                    results, errors = check_needs(needs)
                    self.assertTrue(errors)
                    self.assertNotEqual(results[job], "success")

    def test_every_required_job_must_be_present(self):
        for job in REQUIRED_JOBS:
            with self.subTest(job=job):
                needs = successful_jobs()
                del needs[job]
                results, errors = check_needs(needs)
                self.assertEqual(results[job], "missing")
                self.assertTrue(any(job in error for error in errors))

    def test_empty_object_is_not_success(self):
        results, errors = check_needs({})
        self.assertEqual(len(errors), len(REQUIRED_JOBS))
        self.assertEqual(set(results.values()), {"missing"})

    def test_non_object_payloads_fail(self):
        for value in (None, [], "success", 0, True):
            with self.subTest(value=value):
                self.assertTrue(check_needs(value)[1])

    def test_malformed_job_entries_fail(self):
        for entry in (None, [], "success", {}, {"result": True}, {"result": []}):
            with self.subTest(entry=entry):
                needs = successful_jobs()
                needs["web"] = entry
                results, errors = check_needs(needs)
                self.assertEqual(results["web"], "invalid")
                self.assertTrue(errors)

    def test_unknown_job_requires_inventory_review(self):
        needs = successful_jobs()
        needs["new-job"] = {"result": "success"}
        self.assertTrue(check_needs(needs)[1])

    def test_successful_output_cannot_mask_failed_job(self):
        needs = successful_jobs()
        needs["web"] = {"result": "failure", "outputs": {"result": "success"}}
        self.assertTrue(check_needs(needs)[1])

    def test_results_do_not_leak_outputs_or_inject_workflow_commands(self):
        needs = successful_jobs()
        payload = "\n::warning::untrusted-value"
        needs["web"] = {"result": payload, "outputs": {"secret": "private-output"}}
        text = summary_text(*check_needs(needs))
        self.assertNotIn(payload, text)
        self.assertNotIn("private-output", text)
        self.assertIn("| web | invalid |", text)

    def test_workflow_gate_covers_every_job(self):
        # Deliberately require the current block layout; actionlint checks YAML.
        # Fail rather than silently ignoring an unfamiliar/inline job definition.
        text = (SCRIPT.parent.parent / ".github/workflows/ci.yml").read_text(encoding="utf-8")
        jobs_text = text.split("\njobs:\n", 1)[1]
        job_lines = re.findall(r"(?m)^  (\S[^\n]*)$", jobs_text)
        jobs = []
        for line in job_lines:
            if line.startswith("#"):
                continue
            self.assertRegex(line, r"^[a-zA-Z_][a-zA-Z0-9_-]*:$")
            jobs.append(line[:-1])
        self.assertCountEqual(jobs, (*REQUIRED_JOBS, "quality-gate"))
        gate = jobs_text.split("  quality-gate:\n", 1)[1]
        gate = re.split(r"\n  [a-zA-Z_][a-zA-Z0-9_-]*:", gate, maxsplit=1)[0]
        needs = re.search(r"(?m)^    needs:\n((?:      - [a-zA-Z0-9_-]+\n)+)", gate)
        self.assertIsNotNone(needs)
        self.assertCountEqual(re.findall(r"- ([a-zA-Z0-9_-]+)", needs.group(1)), REQUIRED_JOBS)
        self.assertIn("    if: ${{ always() }}\n", gate)

    def test_workflow_does_not_downgrade_checks_to_advisory(self):
        text = (SCRIPT.parent.parent / ".github/workflows/ci.yml").read_text(encoding="utf-8")
        for value in re.findall(r"(?m)^\s*continue-on-error:\s*([^\n]*)", text):
            self.assertEqual(value.split("#", 1)[0].strip(), "false")

    def run_gate(self, raw, summary_path=None):
        env = os.environ.copy()
        env.pop("CI_NEEDS", None)
        env.pop("GITHUB_STEP_SUMMARY", None)
        if raw is not None:
            env["CI_NEEDS"] = raw
        if summary_path is not None:
            env["GITHUB_STEP_SUMMARY"] = str(summary_path)
        return subprocess.run(
            [sys.executable, "-B", str(SCRIPT)],
            env=env, capture_output=True, text=True, timeout=10, check=False,
        )

    def test_cli_success_and_summary_append(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "summary.md"
            path.write_text("Existing evidence\n", encoding="utf-8")
            process = self.run_gate(json.dumps(successful_jobs()), path)
            self.assertEqual(process.returncode, 0, process.stderr)
            text = path.read_text(encoding="utf-8")
            self.assertTrue(text.startswith("Existing evidence\n"))
            self.assertIn("**PASS**", text)
            self.assertNotIn("::error::", process.stdout)

    def test_cli_failure_writes_evidence_and_returns_nonzero(self):
        needs = successful_jobs()
        needs["rust-preview"]["result"] = "skipped"
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "summary.md"
            process = self.run_gate(json.dumps(needs), path)
            self.assertEqual(process.returncode, 1)
            self.assertIn("::error::rust-preview", process.stdout)
            self.assertIn("| rust-preview | skipped |", path.read_text(encoding="utf-8"))
            self.assertIn("**FAIL**", process.stdout)

    def test_cli_missing_and_invalid_json_fail(self):
        for raw in (None, "", "{broken", "null", "[]", "{}"):
            with self.subTest(raw=raw):
                process = self.run_gate(raw)
                self.assertEqual(process.returncode, 1)
                self.assertIn("::error::", process.stdout)
                self.assertNotIn("Traceback", process.stderr)

    def test_cli_unwritable_summary_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            process = self.run_gate(json.dumps(successful_jobs()), Path(directory))
            self.assertEqual(process.returncode, 1)
            self.assertIn("Unable to write", process.stderr)


if __name__ == "__main__":
    unittest.main()
