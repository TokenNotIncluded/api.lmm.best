"""CI routing regressions: real local Git diffs and fail-closed job aggregation."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from ci_plan import changed_paths, select_jobs
from ci_quality_gate import REQUIRED_JOBS, check_needs, validate_selection

ROOT = Path(__file__).resolve().parent.parent
ALL = set(REQUIRED_JOBS)


class SelectionTests(unittest.TestCase):
    def test_release_and_manual_evidence_is_always_full(self):
        for event in ("push", "workflow_dispatch", "merge_group", "unknown", ""):
            for paths in (None, [], ["docs/notes.md"], ["apps/web/src/app.tsx"]):
                with self.subTest(event=event, paths=paths):
                    self.assertEqual(set(select_jobs(event, paths)), ALL)

    def test_web_change_does_not_build_unrelated_modules(self):
        self.assertEqual(set(select_jobs("pull_request", ["apps/web/src/app.tsx"])), {
            "changes", "repository-contracts", "web", "translations", "route-coverage-contract",
        })

    def test_go_change_keeps_native_and_alignment_contracts(self):
        self.assertEqual(set(select_jobs("pull_request", ["apps/api-go/model/user.go"])), {
            "changes", "repository-contracts", "go", "release-artifact-contract", "route-coverage-contract",
        })

    def test_rust_change_covers_independent_lockfile_real_services_and_web_contract(self):
        selected = set(select_jobs("pull_request", ["apps/api-rust/Cargo.lock"]))
        self.assertTrue({"root-route-acceptance-lockfile", "rust-real-integration", "rustsec", "web"} <= selected)
        self.assertNotIn("aur-package-matrix", selected)

    def test_component_documentation_is_not_treated_as_repository_docs(self):
        self.assertIn("rust-preview", select_jobs("pull_request", ["apps/api-rust/tests/README.md"]))

    def test_provider_is_isolated(self):
        self.assertEqual(set(select_jobs("pull_request", ["packages/pi-lmm-provider/index.ts"])),
                         {"changes", "repository-contracts", "pi-lmm-provider"})

    def test_repository_docs_keep_contracts(self):
        self.assertEqual(set(select_jobs("pull_request", ["README.md", "docs/ci.md"])),
                         {"changes", "repository-contracts"})

    def test_shared_unknown_and_unsafe_paths_force_full_ci(self):
        for name in ("package.json", "bun.lock", ".gitmodules", "vendor/pi", "scripts/tool.py",
                     ".github/workflows/ci.yml", "packaging/aur/test.sh", "new-component/main.go",
                     "docs/generator.py", "../outside", "/absolute", "", "apps\\web\\x"):
            with self.subTest(name=name):
                self.assertEqual(set(select_jobs("pull_request", [name])), ALL)

    def test_unknown_diff_is_full_and_empty_diff_is_explicit(self):
        self.assertEqual(set(select_jobs("pull_request", None)), ALL)
        self.assertEqual(set(select_jobs("pull_request", [])), {"changes", "repository-contracts"})

    def test_schedule_only_scans_advisories(self):
        self.assertEqual(set(select_jobs("schedule")), {"changes", "rustsec"})

    def test_mixed_and_large_diffs_are_not_truncated(self):
        paths = [f"docs/{i}.md" for i in range(350)] + ["apps/web/src/x.tsx", "apps/api-go/model/x.go"]
        selected = select_jobs("pull_request", paths)
        self.assertIn("go", selected)
        self.assertIn("web", selected)
        self.assertEqual(len(selected), len(set(selected)))


class DiffTests(unittest.TestCase):
    def test_invalid_base_and_git_failure_fall_back_to_full(self):
        for base in ("", "0" * 40, "HEAD; exit 0", "main", "z" * 40):
            self.assertIsNone(changed_paths(base))
        with patch("ci_plan.subprocess.check_output", side_effect=subprocess.CalledProcessError(128, "git")):
            self.assertIsNone(changed_paths("a" * 40))

    def test_oversize_and_non_utf8_fall_back_to_full(self):
        for data in (b"x" * (8 * 1024 * 1024 + 1), b"bad-\xff\0"):
            with patch("ci_plan.subprocess.check_output", return_value=data):
                self.assertIsNone(changed_paths("a" * 40))

    def test_real_git_rename_deletion_and_unusual_names(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            def git(*args):
                return subprocess.check_output(["git", "-c", "user.name=CI Test", "-c",
                    "user.email=ci@example.invalid", *args], cwd=root, stderr=subprocess.DEVNULL).decode().strip()
            git("init", "--quiet")
            old = root / "apps/web/src/old name.ts"
            old.parent.mkdir(parents=True)
            old.write_text("fixture\n")
            git("add", ".")
            git("commit", "--quiet", "-m", "base")
            base = git("rev-parse", "HEAD")
            new = root / "apps/api-go/model/new\nname.go"
            new.parent.mkdir(parents=True)
            old.rename(new)
            git("add", "-A")
            git("commit", "--quiet", "-m", "move")
            previous = os.getcwd()
            try:
                os.chdir(root)
                paths = changed_paths(base)
            finally:
                os.chdir(previous)
            self.assertCountEqual(paths, ["apps/web/src/old name.ts", "apps/api-go/model/new\nname.go"])
            self.assertTrue({"web", "go"} <= set(select_jobs("pull_request", paths)))


class PlannedGateTests(unittest.TestCase):
    def evidence(self, selected):
        return {job: {"result": "success" if job in selected else "skipped"} for job in REQUIRED_JOBS}

    def test_planned_omissions_pass_but_legacy_default_remains_strict(self):
        selected = set(select_jobs("pull_request", ["docs/ci.md"]))
        needs = self.evidence(selected)
        self.assertFalse(check_needs(needs, selected)[1])
        self.assertTrue(check_needs(needs)[1])

    def test_selected_failure_cancellation_or_skip_fails(self):
        selected = set(select_jobs("pull_request", ["apps/web/src/x.tsx"]))
        for job in selected:
            for result in ("failure", "cancelled", "skipped"):
                needs = self.evidence(selected)
                needs[job]["result"] = result
                self.assertTrue(check_needs(needs, selected)[1], (job, result))

    def test_even_unselected_failures_cancellations_and_missing_entries_fail(self):
        selected = {"changes", "repository-contracts"}
        for result in ("failure", "cancelled", "unknown"):
            needs = self.evidence(selected)
            needs["go"]["result"] = result
            self.assertTrue(check_needs(needs, selected)[1])
        needs = self.evidence(selected)
        del needs["go"]
        self.assertTrue(check_needs(needs, selected)[1])

    def test_scope_cannot_be_used_for_main_tag_manual_or_merge_queue(self):
        for event in ("push", "workflow_dispatch", "merge_group", "unknown", ""):
            with self.assertRaises(ValueError):
                validate_selection(["changes", "repository-contracts"], event)
            self.assertEqual(validate_selection(list(REQUIRED_JOBS), event), ALL)

    def test_invalid_plans_are_rejected(self):
        for selected in ([], {}, None, ["changes"], ["go"], ["changes", "repository-contracts", "unknown"],
                         ["changes", "changes", "repository-contracts"], [True], [["go"]]):
            with self.assertRaises(ValueError):
                validate_selection(selected, "pull_request")
        with self.assertRaises(ValueError):
            validate_selection(["changes"], "schedule")

    def test_cli_rejects_blank_plan_and_cannot_hide_planner_failure(self):
        selected = ["changes", "repository-contracts"]
        needs = self.evidence(selected)
        for plan in ("", "null", "[]", '["changes"]', json.dumps(selected)):
            needs["changes"]["result"] = "failure"
            env = dict(os.environ, GITHUB_EVENT_NAME="pull_request", CI_NEEDS=json.dumps(needs), CI_SELECTED=plan)
            env.pop("GITHUB_STEP_SUMMARY", None)
            result = subprocess.run([sys.executable, "-B", str(ROOT / "scripts/ci_quality_gate.py")],
                env=env, capture_output=True, text=True, timeout=10)
            self.assertNotEqual(result.returncode, 0)
            self.assertNotIn("**PASS**", result.stdout)

    def test_workflow_has_one_complete_dag_and_stable_gate(self):
        source = (ROOT / ".github/workflows/ci.yml").read_text()
        jobs = re.findall(r"^  ([a-z][a-z0-9-]*):$", source.split("\njobs:\n", 1)[1], re.M)
        self.assertCountEqual(jobs, [*REQUIRED_JOBS, "quality-gate"])
        for job in REQUIRED_JOBS:
            if job != "changes":
                block = source.split(f"  {job}:\n", 1)[1].split("\n  ", 1)[0]
                self.assertIn(f"'{job}'", source.split(f"  {job}:\n", 1)[1][:300])
        self.assertIn("CI_SELECTED: ${{ needs.changes.outputs.required }}", source)
        self.assertNotIn("server-ops-343-request.json", source)
        self.assertNotIn("production-auto-deploy", source)
        self.assertNotIn("continue-on-error", source)


if __name__ == "__main__":
    unittest.main()
