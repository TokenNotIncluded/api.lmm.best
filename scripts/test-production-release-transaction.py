#!/usr/bin/env python3
"""Offline fault-injection tests. No production credentials or network access."""
from __future__ import annotations

import importlib.util
import json
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).with_name("production-release-transaction.py")
spec = importlib.util.spec_from_file_location("production_release_transaction", SCRIPT)
assert spec and spec.loader
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
DEPLOYMENT = "go-v0.2.52-r42-a1"
DIGEST = "a" * 64
VERSION = "0.2.52"


def receipt(phase: str, **overrides: object) -> bytes:
    value = {"deployment_id": DEPLOYMENT, "plan_sha256": DIGEST,
             "version": VERSION, "status": phase}
    value.update(overrides)
    return json.dumps(value).encode()


class Fixture:
    def __init__(self, statuses, *, failures=(), acceptance_ok=True):
        self.statuses = list(statuses)
        self.failures = failures
        self.acceptance_ok = acceptance_ok
        self.calls = []
        self.time = 0.0
        self.transaction = module.ReleaseTransaction(
            self.native, self.acceptance, DEPLOYMENT, DIGEST, VERSION,
            deadline_seconds=3, poll_seconds=1, clock=lambda: self.time,
            sleep=self.sleep)

    def native(self, action):
        self.calls.append(action)
        if action in self.failures:
            raise module.StatusUnavailable("transport failed")
        if action == "status":
            value = self.statuses.pop(0) if len(self.statuses) > 1 else self.statuses[0]
            if isinstance(value, Exception):
                raise value
            return value if isinstance(value, bytes) else receipt(value)
        return b"{}"

    def acceptance(self):
        self.calls.append("public")
        if not self.acceptance_ok:
            raise module.StatusUnavailable("public acceptance failed")

    def sleep(self, seconds):
        self.time += seconds

    def run(self):
        return self.transaction.execute()


class TransactionTests(unittest.TestCase):
    def test_success_requires_public_and_native_confirmation(self):
        f = Fixture(["AWAITING_CONFIRMATION", "AWAITING_CONFIRMATION", "CONFIRMED"])
        self.assertTrue(f.run())
        self.assertEqual(f.calls, ["promote", "status", "public", "status", "confirm", "status"])
        self.assertEqual(f.transaction.outcome, "confirmed")

    def test_failed_public_acceptance_rolls_back_but_fails_release(self):
        f = Fixture(["AWAITING_CONFIRMATION", "AWAITING_CONFIRMATION", "ROLLED_BACK"], acceptance_ok=False)
        self.assertFalse(f.run())
        self.assertIn("rollback", f.calls)
        self.assertNotIn("confirm", f.calls)
        self.assertEqual(f.transaction.outcome, "failed-rolled-back")

    def test_activation_failure_rolls_back_without_public_probe(self):
        f = Fixture(["ROLLBACK_REQUIRED", "ROLLBACK_REQUIRED", "ROLLED_BACK"], failures={"promote"})
        self.assertFalse(f.run())
        self.assertNotIn("public", f.calls)
        self.assertEqual(f.calls.count("promote"), 1)
        self.assertEqual(f.calls.count("rollback"), 1)

    def test_lost_promote_reply_reconciles_without_redispatch(self):
        f = Fixture(["MIGRATING", "OBSERVING", "AWAITING_CONFIRMATION", "AWAITING_CONFIRMATION", "CONFIRMED"], failures={"promote"})
        self.assertTrue(f.run())
        self.assertEqual(f.calls.count("promote"), 1)
        self.assertNotIn("rollback", f.calls)

    def test_lost_confirm_reply_can_still_be_confirmed(self):
        f = Fixture(["AWAITING_CONFIRMATION", "AWAITING_CONFIRMATION", "CONFIRMING", "CONFIRMED"], failures={"confirm"})
        self.assertTrue(f.run())
        self.assertEqual(f.calls.count("confirm"), 1)
        self.assertNotIn("rollback", f.calls)

    def test_ambiguous_confirm_does_not_race_with_rollback(self):
        f = Fixture(["AWAITING_CONFIRMATION"], failures={"confirm"})
        self.assertFalse(f.run())
        self.assertNotIn("rollback", f.calls)
        self.assertEqual(f.transaction.reason, "native-confirmation-not-completed")

    def test_lost_rollback_reply_is_reconciled(self):
        f = Fixture(["ROLLBACK_REQUIRED", "ROLLBACK_REQUIRED", "ROLLING_BACK", "ROLLED_BACK"], failures={"rollback"})
        self.assertFalse(f.run())
        self.assertEqual(f.calls.count("rollback"), 1)
        self.assertEqual(f.transaction.outcome, "failed-rolled-back")

    def test_rollback_failure_never_reports_success(self):
        f = Fixture(["ROLLBACK_REQUIRED"])
        self.assertFalse(f.run())
        self.assertEqual(f.transaction.outcome, "recovery-required")
        self.assertEqual(f.calls.count("rollback"), 1)

    def test_confirmed_is_not_a_rollback_receipt(self):
        f = Fixture(["ROLLBACK_REQUIRED", "ROLLBACK_REQUIRED", "CONFIRMED"])
        self.assertFalse(f.run())
        self.assertEqual(f.transaction.outcome, "recovery-required")

    def test_bad_or_mismatched_status_never_authorizes_mutation(self):
        for response in [b"{}", b"[]", b"null", b"garbage", b"{\"status\":\"CONFIRMED\",\"status\":\"ROLLED_BACK\"}",
                         receipt("CONFIRMED", deployment_id="another"), receipt("CONFIRMED", plan_sha256="b"*64),
                         receipt("CONFIRMED", version="0.2.51"), receipt("SOMETHING_NEW"), b" " * 65537,
                         receipt("CONFIRMED") + b"{}", b"\xff"]:
            with self.subTest(response=response[:100]):
                f = Fixture([response])
                self.assertFalse(f.run())
                self.assertEqual(f.calls, ["promote", "status"])
                self.assertEqual(f.transaction.phase, "UNVERIFIED")

    def test_every_inflight_phase_times_out_without_rollback(self):
        for phase in module.MOVING:
            with self.subTest(phase=phase):
                f = Fixture([phase])
                self.assertFalse(f.run())
                self.assertNotIn("confirm", f.calls)
                self.assertNotIn("rollback", f.calls)
                self.assertEqual(f.calls.count("promote"), 1)

    def test_native_prearm_failure_does_not_roll_back(self):
        f = Fixture(["FAILED_PREARM"])
        self.assertFalse(f.run())
        self.assertEqual(f.calls, ["promote", "status"])
        self.assertEqual(f.transaction.outcome, "failed-before-mutation")

    def test_already_rolled_back_stays_failure(self):
        f = Fixture(["ROLLED_BACK"])
        self.assertFalse(f.run())
        self.assertNotIn("rollback", f.calls)

    def test_confirmed_reentry_still_requires_public_health(self):
        f = Fixture(["CONFIRMED"])
        self.assertTrue(f.run())
        self.assertNotIn("confirm", f.calls)

    def test_confirmed_health_failure_does_not_downgrade(self):
        f = Fixture(["CONFIRMED"], acceptance_ok=False)
        self.assertFalse(f.run())
        self.assertNotIn("rollback", f.calls)
        self.assertEqual(f.transaction.outcome, "confirmed-health-failed")

    def test_native_health_change_before_confirm_selects_rollback(self):
        f = Fixture(["AWAITING_CONFIRMATION", "ROLLBACK_REQUIRED", "ROLLBACK_REQUIRED", "ROLLED_BACK"])
        self.assertFalse(f.run())
        self.assertNotIn("confirm", f.calls)
        self.assertEqual(f.transaction.outcome, "failed-rolled-back")

    def test_concurrent_terminal_state_blocks_rollback(self):
        f = Fixture(["AWAITING_CONFIRMATION", "CONFIRMED"], acceptance_ok=False)
        self.assertFalse(f.run())
        self.assertNotIn("rollback", f.calls)

    def test_status_transport_failure_is_read_only(self):
        f = Fixture([module.StatusUnavailable("lost")])
        self.assertFalse(f.run())
        self.assertEqual(f.transaction.phase, "UNKNOWN")
        self.assertNotIn("rollback", f.calls)
        self.assertNotIn("confirm", f.calls)

    def test_stale_receipt_is_not_reused_when_status_is_lost(self):
        f = Fixture(["AWAITING_CONFIRMATION", module.StatusUnavailable("lost")], acceptance_ok=False)
        self.assertFalse(f.run())
        self.assertEqual(f.transaction.phase, "UNKNOWN")
        self.assertNotIn("rollback", f.calls)

    def test_status_transport_can_recover(self):
        f = Fixture([module.StatusUnavailable("lost"), "AWAITING_CONFIRMATION", "AWAITING_CONFIRMATION", "CONFIRMED"])
        self.assertTrue(f.run())

    def test_report_is_private_and_contains_no_command_or_credentials(self):
        f = Fixture(["CONFIRMED"])
        self.assertTrue(f.run())
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "result.json"
            module.write_report(path, f.transaction.report())
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            value = json.loads(path.read_text())
            self.assertEqual(set(value), {"deployment_id", "plan_sha256", "expected_backend_version",
                                          "native_status", "outcome", "reason"})
            self.assertEqual(value["outcome"], "confirmed")
            self.assertEqual(list(Path(tmp).iterdir()), [path])

    def test_report_failure_before_rename_preserves_previous_evidence(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "result.json"
            module.write_report(path, {"outcome": "previous"})
            with patch.object(module.os, "replace", side_effect=OSError("disk failure")):
                with self.assertRaises(OSError):
                    module.write_report(path, {"outcome": "confirmed"})
            self.assertEqual(json.loads(path.read_text()), {"outcome": "previous"})
            self.assertEqual(list(Path(tmp).iterdir()), [path])


class WorkflowWiringTests(unittest.TestCase):
    def test_public_acceptance_runs_inside_the_transaction(self):
        root = SCRIPT.parent.parent
        shell = (root / 'scripts/auto-deploy-production-release.sh').read_text()
        action = (root / '.github/actions/deploy-production/action.yml').read_text()
        self.assertIn('scripts/production-release-transaction.py', shell)
        self.assertIn('--expected-backend-version "$expected_backend_version"', shell)
        self.assertIn('expected_backend_version=${installed_version[lmm-api-go-bin]%-*}', shell)
        self.assertIn('-- "${probe_runner[@]}" "$probe"', shell)
        self.assertNotIn('production_promote_with_transport_retry', shell)
        self.assertNotIn('python3 -B scripts/verify-public-production.py', action)

    def test_receipt_is_retained_outside_deleted_temporary_directory(self):
        root = SCRIPT.parent.parent
        action = (root / '.github/actions/deploy-production/action.yml').read_text()
        self.assertIn('PRODUCTION_RESULT_FILE: ${{ runner.temp }}/lmm-production-result-', action)
        self.assertIn('path: ${{ runner.temp }}/lmm-production-result-', action)
        self.assertNotIn('path: ${{ runner.temp }}/\n', action)
        self.assertIn('if: always()', action)

    def test_regression_suite_is_mandatory_in_existing_qualification(self):
        text = (SCRIPT.parent.parent / '.github/workflows/server-release-qualification.yml').read_text()
        self.assertIn('run: python3 -B scripts/test-production-release-transaction.py', text)
        self.assertNotIn('test -f scripts/test-production-release-transaction.py', text)

    def test_legacy_fallback_has_no_production_credentials_or_mutation(self):
        text = (SCRIPT.parent.parent / '.github/workflows/deploy-production.yml').read_text()
        self.assertIn('reject-legacy-deploy:', text)
        self.assertIn('cancel-in-progress: false', text)
        self.assertNotIn('secrets.', text)
        self.assertNotIn('environment: production', text)
        self.assertNotIn('run: bash scripts/auto-deploy-production-release.sh', text)


class ProcessIntegrationTests(unittest.TestCase):
    def exercise(self, public_ok):
        with tempfile.TemporaryDirectory(prefix='lmm transaction ') as directory:
            root = Path(directory)
            (root / 'plan.json').write_text('{}')
            fixture = root / 'native.py'
            fixture.write_text("""import json,sys
from pathlib import Path
root = Path(__file__).parent
action = sys.argv[3]
assert sys.argv[1:3] == ['deploy','production']
assert sys.argv[4:6] == ['--plan', str(root/'plan.json')]
assert sys.argv[6:10] == ['--plan-sha256', 'a'*64, '--confirm', 'api.lmm.best']
with (root/'calls').open('a') as log: log.write(action+'\\n')
state = root/'state'
if action == 'promote': state.write_text('AWAITING_CONFIRMATION')
if action == 'confirm': state.write_text('CONFIRMED')
if action == 'rollback':
    assert sys.argv[10:] == ['--reason','workflow-release-acceptance-failed']
    state.write_text('ROLLED_BACK')
print(json.dumps({'deployment_id':'go-v0.2.52-r42-a1','plan_sha256':'a'*64,'version':'0.2.52','status':state.read_text()}))
""")
            public = root / 'public.py'
            public.write_text("import sys\nassert sys.argv[1:] == ['--expected-backend-version','0.2.52']\nraise SystemExit(" + str(0 if public_ok else 1) + ")\n")
            result_path = root/'receipt.json'
            result = subprocess.run([sys.executable, '-B', str(SCRIPT),
                '--deployment-id', DEPLOYMENT, '--plan', str(root/'plan.json'),
                '--plan-sha256', DIGEST, '--expected-backend-version', VERSION,
                '--acceptance-script', str(public), '--result-file', str(result_path),
                '--', sys.executable, str(fixture)], capture_output=True, text=True, timeout=10)
            receipt_value = json.loads(result_path.read_text())
            return result, receipt_value, (root/'calls').read_text().splitlines()

    def test_cli_success_and_quoted_paths(self):
        result, value, calls = self.exercise(True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(value['outcome'], 'confirmed')
        self.assertEqual(calls, ['promote','status','status','confirm','status'])

    def test_cli_failure_rolls_back_and_keeps_nonzero_exit(self):
        result, value, calls = self.exercise(False)
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertEqual(value['outcome'], 'failed-rolled-back')
        self.assertEqual(calls, ['promote','status','status','rollback','status'])

    def test_failed_commands_do_not_expose_stderr_secrets(self):
        with patch.object(module.subprocess, 'run', return_value=subprocess.CompletedProcess(['native'], 1, b'', b'DSN=private-secret')):
            with self.assertRaises(module.StatusUnavailable) as caught:
                module.run_command(['native'], 1)
        self.assertNotIn('private-secret', str(caught.exception))


if __name__ == "__main__":
    unittest.main(verbosity=2)
