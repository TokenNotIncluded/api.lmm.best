#!/usr/bin/env python3
"""Offline fixed-confirmation authorization and workflow ordering tests."""
from pathlib import Path
import unittest
import importlib.util
import subprocess
spec=importlib.util.spec_from_file_location('confirmation_authorization',Path(__file__).with_name('test-server-ops-commit-request.py'))
auth=importlib.util.module_from_spec(spec)
spec.loader.exec_module(auth)

class FixedConfirmationAuthorizationTests(auth.OwnerRequestTests):
    def test_confirmation_requires_its_exact_committed_script(self):
        target=auth.module.OPERATIONS[auth.module.CONFIRM_OPERATION]
        Path(target).write_bytes(self.payload)
        self.git('add',target)
        self.git('commit','-qm','reviewed synthetic native confirmation')
        self.parent=self.git('rev-parse','HEAD')
        self.request.update(operation=auth.module.CONFIRM_OPERATION,base_sha=self.parent)
        self.commit_request()
        self.assertEqual(auth.module.validate_request(self.env),self.payload)
        for field in ('GITHUB_ACTOR','GITHUB_TRIGGERING_ACTOR'):
            with self.assertRaises(ValueError):
                auth.module.validate_request(self.env|{field:'github-actions[bot]'})

class ConfirmationWorkflowTests(unittest.TestCase):
    def test_public_acceptance_brackets_only_explicit_confirmation(self):
        text=(Path(__file__).resolve().parents[1]/'.github/workflows/server-ops.yml').read_text()
        before=text.index('      - name: Require public release health before native confirmation')
        execute=text.index('      - name: Execute only the explicitly requested fixed operation')
        after=text.index('      - name: Verify public release health after native confirmation')
        self.assertLess(before,execute)
        self.assertLess(execute,after)
        for part in (text[before:execute],text[after:]):
            self.assertIn("if: steps.request.outputs.operation == 'confirm-recovery-343'",part)
            self.assertIn('verify-public-production.py --expected-backend-version 0.2.51',part)
            self.assertNotIn('continue-on-error',part)
        self.assertLess(text.index('python3 scripts/test-server-confirm-recovery.py'),before)

if __name__=='__main__':unittest.main(verbosity=2)
