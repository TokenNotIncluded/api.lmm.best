#!/usr/bin/env python3
"""Offline transport and fixed-operation authorization regressions."""
import hashlib,importlib.util,json,os
from pathlib import Path
import subprocess,tempfile,unittest
from unittest.mock import patch

ROOT=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('helper',ROOT/'server-ops-helper-payload.py')
helper=importlib.util.module_from_spec(spec);spec.loader.exec_module(helper)
spec2=importlib.util.spec_from_file_location('auth_tests',ROOT/'test-server-ops-commit-request.py')
auth=importlib.util.module_from_spec(spec2);spec2.loader.exec_module(auth)

class HelperTests(unittest.TestCase):
 def fixture(self,root):
  content=b'\x7fELF'+b'SYNTHETIC_NOT_EXECUTABLE'*4096
  (root/'incident343-recovery').write_bytes(content)
  (root/'receipt.json').write_text(json.dumps({'revision':'a'*40,'sha256':hashlib.sha256(content).hexdigest()}))
  return content
 def test_materializes_exact_bytes_without_executing(self):
  with tempfile.TemporaryDirectory() as directory:
   root=Path(directory); prepared=root/'prepared';prepared.mkdir();content=self.fixture(prepared)
   audit=root/'audit';audit.mkdir();report=audit/'public-report.txt';report.touch()
   payload=helper.prepare_helper_payload(b'printf "script_reached\\n"\n',prepared,'a'*40)
   subprocess.run(['bash','-n'],input=payload,check=True,capture_output=True)
   result=subprocess.run(['bash','-se'],input=payload,capture_output=True,env={**os.environ,'LMM_OPS_REPORT':str(report)})
   self.assertEqual(result.returncode,0,result.stderr)
   self.assertEqual((audit/'incident343-recovery').read_bytes(),content)
   self.assertEqual((audit/'incident343-recovery').stat().st_mode&0o777,0o700)
   self.assertEqual(result.stdout,b'script_reached\n')
   again=subprocess.run(['bash','-se'],input=payload,capture_output=True,env={**os.environ,'LMM_OPS_REPORT':str(report)})
   self.assertNotEqual(again.returncode,0)
   self.assertNotIn(b'script_reached',again.stdout)
 def test_changed_receipt_or_binary_rejected(self):
  for change in ('revision','digest','symlink','magic'):
   with self.subTest(change=change),tempfile.TemporaryDirectory() as directory:
    root=Path(directory);self.fixture(root)
    if change=='revision':expected='b'*40
    else:expected='a'*40
    if change=='digest':(root/'incident343-recovery').write_bytes(b'\x7fELFother')
    if change=='magic':(root/'incident343-recovery').write_bytes(b'not-elf')
    if change=='symlink':
     (root/'incident343-recovery').rename(root/'original');(root/'incident343-recovery').symlink_to(root/'original')
    with self.assertRaises(ValueError):helper.prepare_helper_payload(b'true\n',root,expected)
 def test_size_bound(self):
  with tempfile.TemporaryDirectory() as directory:
   root=Path(directory);self.fixture(root)
   with patch.object(helper,'MAX_BINARY',8),self.assertRaises(ValueError):helper.prepare_helper_payload(b'true\n',root,'a'*40)

class FixedRecoveryAuthorizationTests(auth.OwnerRequestTests):
 def test_fixed_recovery_is_explicit_and_digest_pinned(self):
  self.git('reset','--hard',self.parent)
  target=auth.module.OPERATIONS[auth.module.RECOVERY_OPERATION]
  Path(target).write_bytes(self.payload)
  self.git('add',target);self.git('commit','-qm','reviewed fixed synthetic recovery')
  self.parent=self.git('rev-parse','HEAD')
  self.request.update(operation=auth.module.RECOVERY_OPERATION,base_sha=self.parent)
  self.commit_request()
  self.assertEqual(auth.module.validate_request(self.env),self.payload)
  with self.assertRaises(ValueError):auth.module.validate_request(self.env|{'GITHUB_ACTOR':'github-actions[bot]'})

if __name__=='__main__':unittest.main(verbosity=2)
