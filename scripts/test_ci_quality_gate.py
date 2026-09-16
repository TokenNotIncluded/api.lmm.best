"""Regression tests for CI aggregation and shell guards; no third-party packages."""

import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from ci_quality_gate import REQUIRED_JOBS, check_needs, summary_text

SCRIPTS = Path(__file__).resolve().parent
SCRIPT = SCRIPTS / "ci_quality_gate.py"
WORKFLOW = SCRIPTS.parent / ".github/workflows/ci.yml"


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
        # Pin the current block layout; actionlint separately validates YAML.
        jobs_text = WORKFLOW.read_text(encoding="utf-8").split("\njobs:\n", 1)[1]
        jobs = []
        for line in re.findall(r"(?m)^  (\S[^\n]*)$", jobs_text):
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
        self.assertIn("CI_NEEDS: ${{ toJSON(needs) }}", gate)
        self.assertIn("run: python3 -B scripts/ci_quality_gate.py", gate)

    def test_workflow_does_not_downgrade_checks_to_advisory(self):
        text = WORKFLOW.read_text(encoding="utf-8")
        for value in re.findall(r"(?m)^\s*continue-on-error:\s*([^\n]*)", text):
            self.assertEqual(value.split("#", 1)[0].strip(), "false")
        self.assertIn("run: bun run --filter @lmm/web format:check", text)
        self.assertIn("run: bun run --filter @lmm/web copyright:check", text)
        self.assertIn("run: cargo clippy --workspace --all-targets --all-features --locked -- -D warnings", text)

    def test_workflow_uses_shell_guards_and_read_only_permissions(self):
        text = WORKFLOW.read_text(encoding="utf-8")
        self.assertRegex(text, r"defaults:\n  run:\n(?:    #[^\n]*\n)*    shell: bash\n")
        self.assertIn("bash ../../scripts/check-go-format.sh .", text)
        self.assertIn("bash ../../scripts/check-static-go-binary.sh out/lmm-api-go", text)
        self.assertIn("permissions:\n  contents: read\n", text)
        self.assertNotIn('test -z "$(gofmt -l .)"', text)
        self.assertNotIn("| head -1", text)
        self.assertIn("merge_group:\n    types: [checks_requested]", text)

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

    def test_cli_duplicate_job_cannot_overwrite_failure(self):
        raw = '{"web":{"result":"failure"},' + json.dumps(successful_jobs())[1:]
        process = self.run_gate(raw)
        self.assertEqual(process.returncode, 1)
        self.assertNotIn("**PASS**", process.stdout)

    def test_cli_duplicate_result_cannot_overwrite_failure(self):
        raw = json.dumps(successful_jobs()).replace(
            '"web": {"result": "success"',
            '"web": {"result": "failure", "result": "success"',
        )
        process = self.run_gate(raw)
        self.assertEqual(process.returncode, 1)
        self.assertNotIn("**PASS**", process.stdout)

    def test_cli_nonstandard_constants_fail(self):
        for constant in ("NaN", "Infinity", "-Infinity"):
            with self.subTest(constant=constant):
                raw = json.dumps(successful_jobs()).replace(
                    '"outputs": {}', '"outputs": {"extra": ' + constant + '}', 1,
                )
                self.assertEqual(self.run_gate(raw).returncode, 1)

    def test_cli_deeply_nested_json_fails_without_traceback(self):
        process = self.run_gate("[" * 2000 + "0" + "]" * 2000)
        self.assertEqual(process.returncode, 1)
        self.assertNotIn("Traceback", process.stderr)


class ShellGuardTests(unittest.TestCase):
    def run_shell(self, script, args=(), cwd=None, env=None):
        return subprocess.run(
            ["bash", str(SCRIPTS / script), *map(str, args)], cwd=cwd, env=env,
            text=True, capture_output=True, timeout=10, check=False,
        )

    def test_go_format_preserves_parse_errors(self):
        self.assertIsNotNone(shutil.which("gofmt"), "gofmt must be installed")
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "broken.go"
            path.write_text("package broken\nfunc (\n", encoding="utf-8")
            process = self.run_shell("check-go-format.sh", cwd=directory)
            self.assertNotEqual(process.returncode, 0)
            self.assertIn("broken.go", process.stderr)

    def test_go_format_reports_drift_without_changing_files(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "space name.go"
            source = "package fixture\nfunc main( ){println(1)}\n"
            path.write_text(source, encoding="utf-8")
            process = self.run_shell("check-go-format.sh", [path])
            self.assertNotEqual(process.returncode, 0)
            self.assertIn("space name.go", process.stderr)
            self.assertEqual(path.read_text(encoding="utf-8"), source)

    def test_go_format_accepts_formatted_files_and_multiple_paths(self):
        with tempfile.TemporaryDirectory() as directory:
            paths = [Path(directory) / "one.go", Path(directory) / "two file.go"]
            for path in paths:
                path.write_text("package fixture\n", encoding="utf-8")
            process = self.run_shell("check-go-format.sh", paths)
            self.assertEqual(process.returncode, 0, process.stderr)
            self.assertEqual(process.stdout, "")

    def test_go_format_does_not_hide_missing_path_errors(self):
        with tempfile.TemporaryDirectory() as directory:
            process = self.run_shell("check-go-format.sh", [Path(directory) / "missing.go"])
            self.assertNotEqual(process.returncode, 0)

    def run_static_check(self, output, status):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ldd = root / "ldd"
            ldd.write_text(
                '#!/bin/sh\n[ "$LC_ALL" = C ] || exit 91\n'
                'printf "%s" "$LMM_TEST_LDD_OUTPUT"\nexit "$LMM_TEST_LDD_STATUS"\n',
                encoding="utf-8",
            )
            ldd.chmod(0o700)
            binary = root / "built binary"
            binary.write_text("fixture\n", encoding="utf-8")
            binary.chmod(0o700)
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ["PATH"],
                       LMM_TEST_LDD_OUTPUT=output, LMM_TEST_LDD_STATUS=str(status))
            return self.run_shell("check-static-go-binary.sh", [binary], env=env)

    def test_static_check_accepts_documented_linux_static_outcomes(self):
        for output, status in (("\tnot a dynamic executable\n", 1),
                               ("\tstatically linked\n", 0),
                               ("  statically linked  \n", 1)):
            with self.subTest(output=output, status=status):
                process = self.run_static_check(output, status)
                self.assertEqual(process.returncode, 0, process.stderr)

    def test_static_check_rejects_dynamic_empty_and_uninspectable_outputs(self):
        cases = (
            ("libc.so.6 => /lib/libc.so.6\n", 0),
            ("ldd: permission denied\n", 1),
            ("ldd: command not found\n", 127),
            ("", 0), ("", 1),
            ("not a dynamic executable\n", 2),
            ("statically linked\nlibssl.so.3 => /lib/libssl.so.3\n", 0),
            ("ldd: /missing: not a dynamic executable\n", 1),
        )
        for output, status in cases:
            with self.subTest(output=output, status=status):
                process = self.run_static_check(output, status)
                self.assertNotEqual(process.returncode, 0)
                self.assertIn("Cannot confirm static", process.stderr)

    def test_static_check_requires_an_executable_file(self):
        process = self.run_shell("check-static-go-binary.sh")
        self.assertNotEqual(process.returncode, 0)
        self.assertIn("Usage:", process.stderr)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "not executable"
            path.write_text("fixture\n", encoding="utf-8")
            process = self.run_shell("check-static-go-binary.sh", [path])
            self.assertNotEqual(process.returncode, 0)

    def test_bash_pipefail_preserves_failed_pipeline_status(self):
        process = subprocess.run(
            ["bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", "(exit 7) | cat"],
            capture_output=True, text=True, timeout=10, check=False,
        )
        self.assertEqual(process.returncode, 7)


if __name__ == "__main__":
    unittest.main()
