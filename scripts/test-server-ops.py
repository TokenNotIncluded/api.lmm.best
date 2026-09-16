#!/usr/bin/env python3
"""Offline security/regression tests. Never use credentials or contact production."""
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("server_ops", ROOT / "scripts/server-ops.py")
ops = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ops)


def environment(**overrides):
    env = dict(GITHUB_EVENT_NAME="workflow_dispatch", GITHUB_REPOSITORY=ops.REPOSITORY,
               GITHUB_REF="refs/heads/main", GITHUB_ACTOR="LIghtJUNction",
               GITHUB_TRIGGERING_ACTOR="LIghtJUNction", GITHUB_SHA="a" * 40,
               GITHUB_RUN_ID="123", GITHUB_RUN_ATTEMPT="1", OPS_OPERATION="diagnose",
               OPS_REASON="Incident diagnosis", OPS_REPAIR_SCRIPT="", OPS_CONFIRM="",
               OPS_TIMEOUT="180", OPS_ALLOWED_ACTORS="")
    env.update(overrides)
    return env


def repair_environment(**overrides):
    changes = dict(OPS_OPERATION="repair", OPS_REPAIR_SCRIPT="scripts/server-repairs/example.sh",
                   OPS_CONFIRM="api.lmm.best")
    changes.update(overrides)
    return environment(**changes)


class AuthorizationTests(unittest.TestCase):
    def test_owner_diagnose_and_repair(self):
        ops.validate(environment())
        ops.validate(repair_environment())

    def test_wrong_event_repository_branch_and_actor(self):
        for key, value in [("GITHUB_EVENT_NAME", "push"), ("GITHUB_EVENT_NAME", "pull_request_target"),
                           ("GITHUB_REPOSITORY", "attacker/api.lmm.best"),
                           ("GITHUB_REF", "refs/heads/untrusted"),
                           ("GITHUB_ACTOR", "untrusted"), ("GITHUB_TRIGGERING_ACTOR", "untrusted")]:
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                ops.validate(environment(**{key: value}))

    def test_additional_bot_must_match_a_complete_login(self):
        env = environment(OPS_ALLOWED_ACTORS="trusted[bot], helper", GITHUB_ACTOR="trusted[bot]")
        ops.validate(env)
        env["GITHUB_ACTOR"] = "trusted"
        with self.assertRaises(ValueError):
            ops.validate(env)

    def test_mutation_confirmation_and_rerun(self):
        for changes in ({"OPS_CONFIRM": ""}, {"OPS_CONFIRM": "wrong"}, {"GITHUB_RUN_ATTEMPT": "2"}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                ops.validate(repair_environment(**changes))
        ops.validate(environment(GITHUB_RUN_ATTEMPT="2"))

    def test_script_path_injection_and_traversal(self):
        for path in ["", "scripts/server-repairs/../x.sh", "scripts/server-repairs/x.sh;id",
                     "/tmp/a.sh", "scripts/server-repairs/sub/a.sh", "$(id).sh",
                     "scripts/server-repairs/a.sh\n", "scripts/server-repairs/a.txt"]:
            with self.subTest(path=path), self.assertRaises(ValueError):
                ops.validate(repair_environment(OPS_REPAIR_SCRIPT=path))

    def test_invalid_metadata_and_timeout(self):
        for key, values in {"OPS_OPERATION": ["restart", "$(id)"],
                            "OPS_REASON": ["", " ", "a\nb", "x" * 241, "\x1b"],
                            "OPS_TIMEOUT": ["0", "-1", "601", "180;id"],
                            "GITHUB_SHA": ["main", "a" * 39],
                            "GITHUB_RUN_ID": ["../x", "0"],
                            "GITHUB_RUN_ATTEMPT": ["", "0"]}.items():
            for value in values:
                with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                    ops.validate(environment(**{key: value}))

    def test_diagnosis_cannot_accidentally_carry_mutation_inputs(self):
        for changes in ({"OPS_REPAIR_SCRIPT": "scripts/server-repairs/x.sh"},
                        {"OPS_CONFIRM": "api.lmm.best"}):
            with self.assertRaises(ValueError):
                ops.validate(environment(**changes))


class PayloadTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.old_cwd = Path.cwd()
        os.chdir(self.temp.name)
        self.git("init", "-q")
        self.git("config", "user.name", "test")
        self.git("config", "user.email", "test@example.invalid")
        Path("scripts/server-repairs").mkdir(parents=True)

    def tearDown(self):
        os.chdir(self.old_cwd)
        self.temp.cleanup()

    def git(self, *args):
        return subprocess.check_output(["git", *args], stderr=subprocess.DEVNULL, text=True).strip()

    def commit(self):
        self.git("add", ".")
        self.git("commit", "-qm", "fixture")
        return self.git("rev-parse", "HEAD")

    def test_reads_the_pinned_git_object_not_a_modified_worktree(self):
        file = Path("scripts/server-repairs/example.sh")
        file.write_text("#!/bin/bash\nprintf 'ok'\n")
        sha = self.commit()
        file.write_text("uncommitted malicious content")
        payload = ops.load_repair(repair_environment(GITHUB_SHA=sha))
        self.assertEqual(payload, b"#!/bin/bash\nprintf 'ok'\n")

    def test_rejects_symlink(self):
        Path("scripts/server-repairs/example.sh").symlink_to("/etc/passwd")
        with self.assertRaises(ValueError):
            ops.load_repair(repair_environment(GITHUB_SHA=self.commit()))

    def test_rejects_invalid_shell(self):
        Path("scripts/server-repairs/example.sh").write_text("if then\n")
        with self.assertRaises(ValueError):
            ops.load_repair(repair_environment(GITHUB_SHA=self.commit()))

    def test_rejects_oversized_payload(self):
        Path("scripts/server-repairs/example.sh").write_text("#" * 65537)
        with self.assertRaises(ValueError):
            ops.load_repair(repair_environment(GITHUB_SHA=self.commit()))

    def test_rejects_crlf(self):
        Path("scripts/server-repairs/example.sh").write_bytes(b"true\r\n")
        with self.assertRaises(ValueError):
            ops.load_repair(repair_environment(GITHUB_SHA=self.commit()))

    def test_diagnose_does_not_read_a_repair(self):
        self.assertEqual(ops.load_repair(environment()), b"")


class TransportTests(unittest.TestCase):
    def test_generated_remote_scripts_parse_and_do_not_embed_unquoted_input(self):
        for env, payload in [(environment(), b""),
                             (repair_environment(OPS_REASON="incident ' $(touch /tmp/pwned)"), b"true\n")]:
            with self.subTest(operation=env["OPS_OPERATION"]):
                script = ops.remote_script(env, payload)
                result = subprocess.run(["bash", "-n"], input=script, capture_output=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                if payload:
                    self.assertIn(b'"$audit/repair.log" 2>&1', script)
                    self.assertNotIn(b'cat "$audit/repair.log"', script)
                    self.assertIn(b"sha256sum --check --status", script)

    def test_remote_output_cannot_create_workflow_commands(self):
        output = ops.safe_output(b"::add-mask::x\r\n\x1b[31m::error::forged\n")
        self.assertTrue(all(line.startswith("remote | ") for line in output.splitlines()))
        self.assertNotIn("\r", output)
        self.assertNotIn("\x1b", output)

    def test_ssh_is_strict_secrets_are_not_forwarded_and_files_are_removed(self):
        env = environment(PRODUCTION_SSH_PRIVATE_KEY="TEST-PRIVATE-KEY",
                          PRODUCTION_SSH_KNOWN_HOSTS="TEST-PINNED-HOST")
        paths = []

        def run(command, **kwargs):
            self.assertEqual(command[0], "ssh")
            self.assertIn("StrictHostKeyChecking=yes", command)
            self.assertIn("BatchMode=yes", command)
            self.assertIn("ConnectionAttempts=1", command)
            self.assertIn("timeout --signal=TERM --kill-after=10s 180s", command[-1])
            self.assertNotIn("TEST-PRIVATE-KEY", repr(command))
            self.assertNotIn("PRODUCTION_SSH_PRIVATE_KEY", kwargs["env"])
            self.assertNotIn("GITHUB_TOKEN", kwargs["env"])
            key = Path(command[command.index("-i") + 1])
            paths.append(key)
            self.assertEqual(key.stat().st_mode & 0o777, 0o600)
            self.assertEqual(key.read_text(), "TEST-PRIVATE-KEY\n")
            return subprocess.CompletedProcess(command, 0, b"diagnostics\n")

        with patch.object(ops.subprocess, "run", side_effect=run):
            self.assertEqual(ops.execute(env, b""), 0)
        self.assertFalse(paths[0].exists())

    def test_missing_credentials_fail_before_ssh(self):
        with patch.object(ops.subprocess, "run") as run, self.assertRaises(ValueError):
            ops.execute(environment(), b"")
        run.assert_not_called()

    def test_workflow_manual_entry_and_owner_request_are_separate(self):
        text = (ROOT / ".github/workflows/server-ops.yml").read_text()
        self.assertIn("  workflow_dispatch:", text)
        for event in ("schedule:", "pull_request:", "workflow_run:", "repository_dispatch:"):
            self.assertNotIn(event, text)
        self.assertIn("  push:\n    branches: [main]\n    paths: [.github/server-ops-343-request.json]", text)
        manual, owner_request = text.split("  owner-request:", 1)
        self.assertIn("github.event_name == 'workflow_dispatch'", manual)
        self.assertIn("github.event_name == 'push'", owner_request)
        self.assertIn("github.actor == 'LIghtJUNction'", owner_request)
        self.assertIn("github.triggering_actor == 'LIghtJUNction'", owner_request)
        self.assertIn("server-ops-commit-request.py --validate-only", owner_request)
        self.assertIn("group: production-auto-deploy", text)
        self.assertIn("cancel-in-progress: false", text)
        self.assertIn("persist-credentials: false", text)
        self.assertIn("ref: ${{ github.sha }}", text)
        self.assertNotIn("write-all", text)

    def test_public_health_checks_json_success_not_just_http(self):
        for data, expected in [(b'{"success":true}', True), (b'{"success":false}', False),
                               (b'not json', False), (b'[]', False)]:
            with self.subTest(data=data), patch.object(ops.urllib.request, "build_opener") as build:
                response = build.return_value.open.return_value.__enter__.return_value
                response.status = 200
                response.read.return_value = data
                self.assertEqual(ops.public_health(), expected)

    def test_remote_repair_preserves_failure_and_keeps_raw_logs_private(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fakebin = root / "bin"
            fakebin.mkdir()
            for name in ("systemctl", "curl"):
                stub = fakebin / name
                stub.write_text("#!/bin/sh\nexit 0\n")
                stub.chmod(0o700)
            payload = (b"printf 'PRIVATE_TOKEN_MUST_NOT_LEAK\\n'\n"
                       b"printf 'Reviewed report\\n' >\"$LMM_OPS_REPORT\"\nexit 7\n")
            env = repair_environment(OPS_REASON="Incident ' $(touch SHOULD_NOT_EXIST)")
            script = ops.remote_script(env, payload).decode()
            script = script.replace("export PATH=", f"export PATH={fakebin}:")
            script = script.replace("/var/log/lmm-server-ops", str(root / "audit"))
            script = script.replace("/run/lock/lmm-server-ops.lock", str(root / "ops.lock"))
            result = subprocess.run(["bash", "-se"], input=script, text=True,
                                    capture_output=True, cwd=root, timeout=10)
            self.assertEqual(result.returncode, 7, result.stderr)
            self.assertIn("Reviewed report", result.stdout)
            self.assertNotIn("PRIVATE_TOKEN_MUST_NOT_LEAK", result.stdout + result.stderr)
            audit = root / "audit/123-1"
            self.assertIn("PRIVATE_TOKEN_MUST_NOT_LEAK", (audit / "repair.log").read_text())
            self.assertEqual((audit / "exit-code").read_text().strip(), "7")
            self.assertFalse((root / "SHOULD_NOT_EXIST").exists())
            second = subprocess.run(["bash", "-se"], input=script, text=True,
                                    capture_output=True, cwd=root, timeout=10)
            self.assertNotEqual(second.returncode, 0)
            self.assertNotIn("repair_started=true", second.stdout)

    def test_ssh_timeout_cleans_up_private_key(self):
        env = environment(PRODUCTION_SSH_PRIVATE_KEY="TEST-KEY",
                          PRODUCTION_SSH_KNOWN_HOSTS="TEST-HOST")
        paths = []

        def run(command, **kwargs):
            paths.append(Path(command[command.index("-i") + 1]))
            raise subprocess.TimeoutExpired(command, kwargs["timeout"])

        with patch.object(ops.subprocess, "run", side_effect=run), self.assertRaises(subprocess.TimeoutExpired):
            ops.execute(env, b"")
        self.assertFalse(paths[0].exists())

    def test_main_does_not_turn_remote_failure_into_success(self):
        with patch.dict(os.environ, environment(), clear=True), \
             patch.object(ops.sys, "argv", ["server-ops.py"]), \
             patch.object(ops, "execute", return_value=7), \
             patch.object(ops, "public_health", return_value=True):
            self.assertEqual(ops.main(), 7)

    def test_main_marks_external_health_failure(self):
        with patch.dict(os.environ, environment(), clear=True), \
             patch.object(ops.sys, "argv", ["server-ops.py"]), \
             patch.object(ops, "execute", return_value=0), \
             patch.object(ops, "public_health", return_value=False):
            self.assertEqual(ops.main(), 1)


if __name__ == "__main__":
    unittest.main(verbosity=2)
