#!/usr/bin/env python3
"""Offline native confirmation response tests. Never connect to production."""
import json
from pathlib import Path
import subprocess
import tempfile
import types
import unittest
from unittest.mock import patch

SHELL = Path(__file__).with_name('server-repairs') / 'confirm-recovery-343.sh'
SOURCE = SHELL.read_text().split("<<'PY'\n", 1)[1].rsplit('\nPY', 1)[0]
m = types.ModuleType('confirmation')
exec(compile(SOURCE, str(SHELL), 'exec'), m.__dict__)
BASE = {'deployment_id': m.DEPLOYMENT, 'version': '0.2.51', 'phase': 'AWAITING_CONFIRMATION'}
FINAL = BASE | {'phase': 'CONFIRMED'}


class ConfirmationTests(unittest.TestCase):
    def run_case(self, values):
        with tempfile.TemporaryDirectory() as td:
            report = Path(td) / 'report.json'
            report.write_text('{}')
            with patch.object(m, 'invoke', side_effect=values) as call, patch.object(m.os, 'geteuid', return_value=0):
                code = m.main(str(report))
                return code, json.loads(report.read_text()), call.call_args_list

    def test_script_parses(self):
        subprocess.run(['bash', '-n', str(SHELL)], check=True)

    def test_released_cli_empty_id_requires_final_readback(self):
        code, data, calls = self.run_case([BASE, FINAL | {'deployment_id': ''}, FINAL])
        self.assertEqual(code, 0)
        self.assertEqual(data['phase'], 'CONFIRMED')
        self.assertEqual([c.args[1] for c in calls], ['status', 'confirm', 'status'])
        self.assertEqual(calls[2].args[2], 'status-after-confirm')

    def test_complete_response_still_requires_readback(self):
        code, data, calls = self.run_case([BASE, FINAL, FINAL])
        self.assertEqual(code, 0)
        self.assertEqual(len(calls), 3)

    def test_wrong_before_identity_never_confirms(self):
        code, data, calls = self.run_case([BASE | {'deployment_id': 'other'}])
        self.assertEqual(code, 1)
        self.assertEqual(len(calls), 1)

    def test_rollback_required_never_confirms(self):
        code, data, calls = self.run_case([BASE | {'phase': 'ROLLBACK_REQUIRED'}])
        self.assertEqual(code, 1)
        self.assertEqual(len(calls), 1)

    def test_already_confirmed_not_replayed(self):
        code, data, calls = self.run_case([FINAL])
        self.assertEqual(code, 1)
        self.assertEqual(len(calls), 1)

    def test_changed_post_identity_is_failure(self):
        code, data, calls = self.run_case([BASE, FINAL, FINAL | {'deployment_id': 'other'}])
        self.assertEqual(code, 1)

    def test_unconfirmed_response_is_failure(self):
        code, data, calls = self.run_case([BASE, BASE])
        self.assertEqual(code, 1)
        self.assertEqual(len(calls), 2)

    def test_raw_error_not_published(self):
        code, data, calls = self.run_case([BASE, ValueError('SECRET_CREDENTIAL_SENTINEL')])
        self.assertEqual(code, 1)
        self.assertNotIn('SECRET', json.dumps(data))

    def test_invalid_command_rejected(self):
        with self.assertRaises(ValueError):
            m.invoke(Path('/tmp'), 'rollback')


if __name__ == '__main__':
    unittest.main(verbosity=2)
