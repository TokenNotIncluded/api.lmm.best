#!/usr/bin/env python3
"""Offline authorization tests; no SSH, credentials or production calls."""
import importlib.util
import json
import hashlib
import os
from pathlib import Path
import subprocess
import tempfile
import time
import unittest

spec = importlib.util.spec_from_file_location('owner_request', Path(__file__).with_name('server-ops-commit-request.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class OwnerRequestTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.cwd = Path.cwd()
        os.chdir(self.temp.name)
        self.git('init', '-q')
        self.git('config', 'user.name', 'test')
        self.git('config', 'user.email', 'test@example.invalid')
        Path(module.SCRIPT).parent.mkdir(parents=True)
        self.payload = b'#!/bin/sh\nprintf "synthetic diagnostic"\n'
        Path(module.SCRIPT).write_bytes(self.payload)
        Path('.github').mkdir()
        self.git('add', '.')
        self.git('commit', '-qm', 'synthetic base')
        self.parent = self.git('rev-parse', 'HEAD')
        self.request = dict(format=1, incident=343, operation='inspect-startup-343', base_sha=self.parent,
                            script_sha256=hashlib.sha256(self.payload).hexdigest(), confirm='api.lmm.best')
        self.env = dict(GITHUB_EVENT_NAME='push', GITHUB_REPOSITORY=module.REPOSITORY,
                        GITHUB_REF='refs/heads/main', GITHUB_ACTOR=module.OWNER,
                        GITHUB_TRIGGERING_ACTOR=module.OWNER, GITHUB_RUN_ATTEMPT='1', GITHUB_RUN_ID='123',
                        GITHUB_EVENT_PATH=str(Path('event.json').resolve()))

    def tearDown(self):
        os.chdir(self.cwd)
        self.temp.cleanup()

    def git(self, *args):
        return subprocess.check_output(['git', *args], stderr=subprocess.DEVNULL, text=True).strip()

    def commit_request(self):
        Path(module.REQUEST).write_text(json.dumps(self.request))
        self.git('add', module.REQUEST)
        self.git('commit', '-qm', 'explicit owner request')
        sha = self.git('rev-parse', 'HEAD')
        self.env['GITHUB_SHA'] = sha
        self.event = dict(after=sha, before=self.parent, ref='refs/heads/main', deleted=False, forced=False,
                          sender={'login':module.OWNER}, repository={'full_name':module.REPOSITORY})
        self.write_event()

    def write_event(self):
        Path(self.env['GITHUB_EVENT_PATH']).write_text(json.dumps(self.event))

    def test_owner_exact_request_passes(self):
        self.commit_request()
        self.assertEqual(module.validate_request(self.env), self.payload)

    def test_bot_or_rerun_actor_rejected(self):
        self.commit_request()
        for key in ('GITHUB_ACTOR','GITHUB_TRIGGERING_ACTOR'):
            with self.subTest(key=key), self.assertRaises(ValueError):
                module.validate_request(self.env | {key:'github-actions[bot]'})

    def test_no_replays(self):
        self.commit_request()
        with self.assertRaises(ValueError): module.validate_request(self.env | {'GITHUB_RUN_ATTEMPT':'2'})

    def test_wrong_context(self):
        self.commit_request()
        for k,v in [('GITHUB_EVENT_NAME','workflow_dispatch'),('GITHUB_REPOSITORY','other/repo'),('GITHUB_REF','refs/heads/feature')]:
            with self.subTest(k=k), self.assertRaises(ValueError): module.validate_request(self.env | {k:v})

    def test_forced_push_rejected(self):
        self.commit_request();self.event['forced']=True;self.write_event()
        with self.assertRaises(ValueError): module.validate_request(self.env)

    def test_expired_request(self):
        self.commit_request()
        with self.assertRaises(ValueError): module.validate_request(self.env, now=time.time()+1900)

    def test_future_request(self):
        self.commit_request()
        with self.assertRaises(ValueError): module.validate_request(self.env, now=time.time()-200)

    def test_arbitrary_repair_cannot_be_selected(self):
        self.request['operation']='repair';self.commit_request()
        with self.assertRaises(ValueError): module.validate_request(self.env)

    def test_wrong_confirmation(self):
        self.request['confirm']='';self.commit_request()
        with self.assertRaises(ValueError): module.validate_request(self.env)

    def test_wrong_hash(self):
        self.request['script_sha256']='0'*64;self.commit_request()
        with self.assertRaises(ValueError): module.validate_request(self.env)

    def test_parent_mismatch(self):
        self.request['base_sha']='a'*40;self.commit_request()
        with self.assertRaises(ValueError): module.validate_request(self.env)

    def test_code_change_in_request_commit_is_rejected(self):
        Path(module.SCRIPT).write_text('echo new-code\n');self.git('add',module.SCRIPT);self.commit_request()
        with self.assertRaises(ValueError): module.validate_request(self.env)

    def test_uncommitted_request_does_not_change_action(self):
        self.commit_request();Path(module.REQUEST).write_text('{"operation":"repair"}')
        self.assertEqual(module.validate_request(self.env),self.payload)

    def test_only_request_changes_allowed(self):
        Path('other').write_text('x');self.git('add','other');self.commit_request()
        with self.assertRaises(ValueError): module.validate_request(self.env)

    def test_event_sender_verified(self):
        self.commit_request();self.event['sender']['login']='other';self.write_event()
        with self.assertRaises(ValueError):module.validate_request(self.env)

    def test_duplicate_json_rejected(self):
        with self.assertRaises(ValueError): json.loads('{"x":1,"x":2}',object_pairs_hook=module.unique_object)

    def test_does_not_spoof_github_identity(self):
        text=Path(__file__).with_name('server-ops-commit-request.py').read_text()
        self.assertNotIn("GITHUB_EVENT_NAME='workflow_dispatch'",text)
        self.assertNotIn('OPS_ALLOWED_ACTORS=',text)


if __name__=='__main__': unittest.main(verbosity=2)
