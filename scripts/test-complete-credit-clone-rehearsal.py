#!/usr/bin/env python3
"""Offline incident fixtures; every process and SQL execution is synthetic."""
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import types
import unittest
from unittest.mock import patch
import urllib.parse

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('clone_tail', HERE / 'complete-credit-clone-rehearsal.py')
tail = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tail)
original = tail.load_original()


class Fixture:
    def __init__(self):
        self.temp = tempfile.TemporaryDirectory(prefix='clone-tail-test.', dir=Path.home() / '.cache')
        self.work = Path(self.temp.name)
        self.patches = []
        self.events, self.calls = [], []
        self.fail = None
        self.fingerprint = b'opaque full 167 table fingerprint\n'
        self.probe = self.work / 'probe'
        self.write(self.probe, b'synthetic binary, never executed')
        artifacts = self.work / 'business-artifacts'
        artifacts.mkdir(mode=0o700)
        self.plan = {'source_sha': tail.SOURCE_SHA, 'provider': {'path': '/never/original/provider', 'sha256': tail.PROVIDER_SHA},
                     'transition_id': tail.TRANSITION_ID, 'transition_intent_sha256': tail.INTENT_SHA, 'clone': copy.deepcopy(tail.CLONE),
                     'regression': {'source_directory': str(self.work / 'original-source'), 'source_sha': tail.SOURCE_SHA, 'packages': ['./model', './internal/appcli'], 'run': 'TestCredit'}}
        self.seal = {'format': 'lmm-credit-business-seal-v1', 'transition_id': tail.TRANSITION_ID, 'transition_intent_sha256': tail.INTENT_SHA,
                     'business_plan_sha256': tail.BUSINESS_SHA, 'backup_frozen_state_sha256': 'a' * 64,
                     'backup': self.binding(self.work / 'final-full-database.dump', b'opaque complete archive'),
                     'clone_plan': self.binding(artifacts / 'clone-plan.json', original.encode({'target': tail.IDENTITY})),
                     'clone_sql': self.binding(artifacts / 'clone-financial.sql', b'FORBIDDEN_FINANCIAL;'),
                     'clone_before_sql': self.binding(artifacts / 'clone-before.sql', b'FORBIDDEN_BEFORE;'),
                     'clone_after_sql': self.binding(artifacts / 'clone-after.sql', b'after-proof;'),
                     'original_fingerprint': {'before_result': self.binding(self.work / 'original-before.tsv', self.fingerprint),
                        'inventory': self.binding(self.work / 'inventory.json', original.encode({'tables': ['synthetic_' + str(i) for i in range(167)]})),
                        'clone_after_receipt': self.binding(artifacts / 'after.receipt.json', original.encode({'table_count': 167})),
                        'clone_after_sql': self.binding(artifacts / 'after-fingerprint.sql', b'fingerprint-after;')}}
        seal_binding = self.binding(artifacts / 'business-seal.json', original.encode(self.seal))
        self.state = {'format': original.FORMAT, 'phase': 'REHEARSAL_FAILED', 'sequence': 29, 'plan_sha256': '',
                      'transition_id': tail.TRANSITION_ID, 'transition_intent_sha256': tail.INTENT_SHA, 'business_plan_sha256': tail.BUSINESS_SHA,
                      'business_seal': seal_binding, 'backup': self.seal['backup'], 'backup_frozen_state_sha256': 'a' * 64,
                      'clone_identity': copy.deepcopy(tail.IDENTITY), 'clone_table_count': 167,
                      'clone_generation': {'pid': 2603003, 'started_at': '1791250839', 'port': tail.CLONE['port'],
                          'data_directory': str(self.work / 'local-clone/data'), 'socket_directory': str(self.work / 'local-clone/socket')},
                      'guardian_lock_bindings': {'opaque': 'retained'}, 'stopped_handoffs': {'opaque': 'retained'}, 'recovery': 'original failure retained'}
        for child in ('local-clone', 'local-clone/data', 'local-clone/socket'):
            (self.work / child).mkdir(mode=0o700)
        self.write(self.work / 'local-clone/data/postmaster.pid', ('2603003\n' + str(self.work / 'local-clone/data') + '\n1791250839\n25591\n' + str(self.work / 'local-clone/socket') + '\n\n').encode())
        self.log_specs = {}
        for name in tail.LOGS:
            raw = self.fingerprint if 'original-before' in name or 'original-after' in name else b''
            if 'financial-dispatch' in name:
                raw = b'original committed financial dispatch evidence'
            elif 'candidate-schema-apply' in name:
                raw = b'inet metadata: converting NULL to string is unsupported\n'
            elif 'identity' in name:
                raw = original.encode(tail.IDENTITY)
            self.write(self.work / name, raw)
            self.log_specs[name] = (len(raw), original.digest(raw))
        self.plan_path = self.work / 'financial-plan.bound.json'
        plan_hash = self.binding(self.plan_path, original.encode(self.plan))['sha256']
        self.state['plan_sha256'] = plan_hash
        self.write(self.work / 'state.json', original.encode(self.state))
        for name, value in {'WORK_PATH': self.work, 'PLAN_PATH': self.plan_path, 'PLAN_SHA': plan_hash,
                            'STATE_SHA': original.digest(original.encode(self.state)), 'SEAL_SHA': seal_binding['sha256'],
                            'BACKUP_SHA': self.seal['backup']['sha256'], 'FINGERPRINT_SHA': original.digest(self.fingerprint),
                            'PROBE_PATH': self.probe, 'PROBE_SHA': original.digest(self.probe.read_bytes()), 'LOGS': self.log_specs}.items():
            self.patch(name, value)
        self.controller = tail.controller_type(original)(self.plan, plan_hash, self.work, executor=self.executor)
        self.controller.frozen_gates = self.freeze
        self.saved_persist = self.controller.persist
        def persist(phase, **values):
            self.events.append('persist')
            self.saved_persist(phase, **values)
        self.controller.persist = persist

    def patch(self, name, value):
        p = patch.object(tail, name, value)
        p.start()
        self.patches.append(p)

    def close(self):
        for p in reversed(self.patches):
            p.stop()
        self.temp.cleanup()

    def write(self, file, body):
        file.write_bytes(body)
        file.chmod(0o600)

    def binding(self, file, body):
        self.write(file, body)
        return {'path': str(file), 'sha256': original.digest(body)}

    def freeze(self):
        self.events.append('freeze')
        if self.fail == 'final-freeze' and self.events.count('freeze') == 2:
            raise original.GateFailed('synthetic final freeze failure')

    def executor(self, argv, **options):
        self.calls.append((argv, options))
        body = options.get('input') or b''
        if b'FORBIDDEN' in body:
            raise AssertionError('before/financial SQL must never be replayed')
        if argv[0] == '/usr/bin/ssh':
            raise AssertionError('no SSH in offline fixture')
        output = b''
        event = None
        if argv[0] == '/usr/bin/psql':
            if b'json_build_object' in body:
                event = 'identity'
                output = original.encode(dict(tail.IDENTITY, local_socket=True, canonical_owner=True))
            elif b'fingerprint-after;' in body:
                event, output = 'fingerprint', self.fingerprint
            elif b'after-proof;' in body:
                event = 'after-sql'
            else:
                raise AssertionError('unknown SQL in synthetic fixture')
        elif argv[0] == str(self.probe):
            self.assert_probe(argv, options)
            event = argv[-1][2:]
            output = b'synthetic original startup and migration success\n'
        elif argv[0] == '/usr/bin/git':
            event = 'source' if argv[-1] == 'HEAD' else 'clean'
            output = (tail.SOURCE_SHA + '\n').encode() if event == 'source' else b''
            if self.fail == 'source':
                output = b'0' * 40
            elif self.fail == 'dirty' and event == 'clean':
                output = b' M synthetic-file'
        elif argv[0] == '/usr/bin/go':
            event = 'regression'
            if argv != ['/usr/bin/go', 'test', '-p', '1', './model', './internal/appcli', '-run', 'TestCredit', '-count=1']:
                raise AssertionError('original regression changed')
        else:
            raise AssertionError('unknown process in synthetic fixture')
        self.events.append(event)
        if self.fail == event and event != 'source':
            return subprocess.CompletedProcess(argv, 7, b'failure body', b'failure stderr')
        if self.fail == 'timeout' and event == 'apply':
            raise subprocess.TimeoutExpired(argv, 600, output=b'partial raw failure', stderr=b'partial stderr')
        return subprocess.CompletedProcess(argv, 0, output, b'')

    def assert_probe(self, argv, options):
        if argv not in ([str(self.probe), 'migrate', '--apply'], [str(self.probe), 'migrate', '--verify']):
            raise AssertionError('probe may only apply or verify migration')
        environment = options['env']
        if set(environment) != {'PATH', 'LANG', 'GOMAXPROCS', 'HOME', 'TMPDIR', 'SQL_DSN'}:
            raise AssertionError('inherited process environment')
        dsn = urllib.parse.urlsplit(environment['SQL_DSN'])
        expected = {'host': [str(self.work / 'local-clone/socket')], 'port': ['25591'], 'sslmode': ['disable'], 'search_path': [tail.CLONE['schema']]}
        if dsn.hostname is not None or dsn.username != tail.CLONE['role'] or dsn.path != '/' + tail.CLONE['database'] or urllib.parse.parse_qs(dsn.query) != expected:
            raise AssertionError('probe clone context changed')

    def rebind_state(self):
        self.write(self.work / 'state.json', original.encode(self.state))
        self.controller.state = copy.deepcopy(self.state)
        self.patch('STATE_SHA', original.digest(original.encode(self.state)))

    def snapshot(self):
        return {str(p.relative_to(self.work)): original.digest(p.read_bytes()) for p in self.work.rglob('*') if p.is_file()}


class CloneTailTests(unittest.TestCase):
    def setUp(self):
        self.f = Fixture()
        self.addCleanup(self.f.close)

    def run_tail(self):
        return tail.complete(original, self.f.controller, execute=True, confirm='api.lmm.best')

    def test_dry_precheck_performs_no_process_sql_or_file_write(self):
        before = self.f.snapshot()
        result = tail.complete(original, self.f.controller)
        self.assertFalse(result['execute'])
        self.assertEqual(self.f.calls, [])
        self.assertEqual(self.f.snapshot(), before)

    def test_only_subclass_override_and_sealed_original_identity(self):
        cls = tail.controller_type(original)
        self.assertEqual(set(cls.__dict__) - {'__module__', '__doc__', '__qualname__', '__firstlineno__', '__static_attributes__'}, {'verify_candidate'})
        for method in ('execute', 'rehearse', 'clone_sql', 'fingerprint', 'frozen_gates', 'persist'):
            self.assertIs(getattr(cls, method), getattr(original.Controller, method))
        self.assertEqual(original.__file__, str(tail.ORIGINAL_PATH))

    def test_remaining_tail_order_real_original_fingerprint_and_persist(self):
        old_state = (self.f.work / 'state.json').read_bytes()
        old_logs = {name: (self.f.work / name).read_bytes() for name in tail.LOGS}
        with patch.dict(os.environ, {'SQL_DSN': 'forbidden production', 'LOG_SQL_DSN': 'forbidden logs', 'PGPASSWORD': 'forbidden credentials'}):
            result = self.run_tail()
        self.assertEqual(result, {'ok': True, 'phase': 'REHEARSED', 'sequence': 30})
        self.assertEqual(self.f.events, ['freeze', 'identity', 'after-sql', 'fingerprint', 'identity', 'apply', 'after-sql', 'identity', 'verify', 'identity', 'verify', 'fingerprint', 'source', 'clean', 'regression', 'freeze', 'persist'])
        now = original.decode((self.f.work / 'state.json').read_bytes())
        self.assertEqual({k: v for k, v in now.items() if k not in {'phase', 'sequence', 'updated_at', 'rehearsal_seal_sha256', 'clone_before_fingerprint', 'clone_after_fingerprint'}}, {k: v for k, v in self.f.state.items() if k not in {'phase', 'sequence'}})
        self.assertEqual(now['clone_before_fingerprint']['path'], str(self.f.work / '000561-clone-original-before.tsv'))
        self.assertNotEqual(now['clone_after_fingerprint']['path'], str(self.f.work / '000565-clone-original-after.tsv'))
        self.assertEqual(original.read_bound(now['clone_after_fingerprint']), self.f.fingerprint)
        attempt = self.f.work / 'clone-tail-completion'
        self.assertEqual((attempt / 'old-state.json').read_bytes(), old_state)
        self.assertTrue((attempt / 'intent.json').is_file() and (attempt / 'complete.json').is_file())
        current = original.decode((attempt / 'current-clone-proof.json').read_bytes())['after_fingerprint']
        self.assertNotEqual(current['path'], now['clone_after_fingerprint']['path'])
        self.assertNotEqual(current['path'], str(self.f.work / '000565-clone-original-after.tsv'))
        self.assertEqual(original.read_bound(current), self.f.fingerprint)
        for name, body in old_logs.items():
            self.assertEqual((self.f.work / name).read_bytes(), body)
            self.assertEqual((attempt / name).read_bytes(), body)

    def test_wrong_state_phase_sequence_plan_and_seals_refuse_before_write(self):
        for field, value in [('phase', 'SEALED'), ('sequence', 28), ('sequence', True), ('plan_sha256', '0' * 64), ('business_plan_sha256', '0' * 64), ('clone_table_count', 166)]:
            with self.subTest(field=field, value=value):
                saved = copy.deepcopy(self.f.state)
                self.f.state[field] = value
                self.f.rebind_state()
                with self.assertRaises(original.GateFailed):
                    self.run_tail()
                self.assertEqual(self.f.calls, [])
                self.assertFalse((self.f.work / 'clone-tail-completion').exists())
                self.f.state = saved
                self.f.rebind_state()

    def test_state_plan_seal_backup_log_and_probe_hash_drift_zero_write(self):
        files = [self.f.work / 'state.json', self.f.plan_path, Path(self.f.state['business_seal']['path']), Path(self.f.state['backup']['path']), self.f.work / '000563-clone-financial-dispatch.log', self.f.work / '000567-clone-candidate-schema-apply.log', self.f.probe]
        for file in files:
            with self.subTest(file=file.name):
                old = file.read_bytes()
                file.write_bytes(old + b'changed')
                before = self.f.snapshot()
                with self.assertRaises((tail.CompletionFailed, original.GateFailed)):
                    self.run_tail()
                self.assertEqual(self.f.snapshot(), before)
                self.assertEqual(self.f.calls, [])
                file.write_bytes(old)

    def test_clone_target_pid_generation_socket_and_system_identifier_refuse(self):
        for field, value in [('clone_identity', dict(tail.IDENTITY, system_identifier='wrong')), ('clone_generation', dict(self.f.state['clone_generation'], pid=2603004))]:
            with self.subTest(field=field):
                saved = copy.deepcopy(self.f.state)
                self.f.state[field] = value
                self.f.rebind_state()
                with self.assertRaises(original.GateFailed):
                    self.run_tail()
                self.assertEqual(self.f.calls, [])
                self.f.state = saved
                self.f.rebind_state()
        file = self.f.work / 'local-clone/data/postmaster.pid'
        file.write_bytes(file.read_bytes().replace(b'25591', b'25592'))
        with self.assertRaises(original.GateFailed):
            self.run_tail()
        self.assertFalse((self.f.work / 'clone-tail-completion').exists())

    def test_unbound_plan_object_and_newer_operation_refuse(self):
        self.f.controller.plan = dict(self.f.plan, source_sha='0' * 40)
        with self.assertRaises(original.GateFailed):
            self.run_tail()
        self.f.controller.plan = self.f.plan
        self.f.controller.operation_sequence = 568
        with self.assertRaises(original.GateFailed):
            self.run_tail()
        self.assertEqual(self.f.calls, [])

    def test_confirmation_and_existing_attempt_reject_without_process(self):
        with self.assertRaises(original.GateFailed):
            tail.complete(original, self.f.controller, execute=True, confirm='wrong')
        (self.f.work / 'clone-tail-completion').mkdir(mode=0o700)
        with self.assertRaises(original.GateFailed):
            self.run_tail()
        self.assertEqual(self.f.calls, [])

    def test_apply_failure_archives_exact_failure_and_blocks_replay(self):
        self.f.fail = 'apply'
        old_state = (self.f.work / 'state.json').read_bytes()
        with self.assertRaises(original.GateFailed):
            self.run_tail()
        self.assertEqual((self.f.work / 'state.json').read_bytes(), old_state)
        self.assertEqual((self.f.work / '000572-clone-candidate-schema-apply.log').read_bytes(), b'failure bodyfailure stderr')
        failure = original.decode((self.f.work / 'clone-tail-completion/failure.json').read_bytes())
        self.assertTrue(failure['replay_forbidden'])
        count = len(self.f.calls)
        with self.assertRaises(original.GateFailed):
            self.run_tail()
        self.assertEqual(len(self.f.calls), count)
        self.assertEqual(self.f.events.count('after-sql'), 1)

    def test_timeout_preserves_original_raw_log_and_no_downstream(self):
        self.f.fail = 'timeout'
        with self.assertRaises(original.GateFailed):
            self.run_tail()
        self.assertEqual((self.f.work / '000572-clone-candidate-schema-apply.log').read_bytes(), b'partial raw failurepartial stderr')
        self.assertEqual(self.f.events.count('after-sql'), 1)
        self.assertNotIn('persist', self.f.events)

    def test_remaining_tail_failure_never_checkpoints_rehearsed(self):
        for failure in ('after-sql', 'verify', 'source', 'dirty', 'regression', 'final-freeze'):
            with self.subTest(failure=failure):
                f = Fixture()
                try:
                    f.fail = failure
                    with self.assertRaises(original.GateFailed):
                        tail.complete(original, f.controller, execute=True, confirm='api.lmm.best')
                    self.assertEqual(original.decode((f.work / 'state.json').read_bytes())['phase'], 'REHEARSAL_FAILED')
                    self.assertNotIn('persist', f.events)
                    self.assertTrue((f.work / 'clone-tail-completion/failure.json').is_file())
                finally:
                    f.close()

    def test_current_after_sql_or_full_fingerprint_failure_never_applies_schema(self):
        for failure in ('after-sql', 'fingerprint'):
            with self.subTest(failure=failure):
                f = Fixture()
                try:
                    f.fail = failure
                    with self.assertRaises(original.GateFailed):
                        tail.complete(original, f.controller, execute=True, confirm='api.lmm.best')
                    self.assertNotIn('apply', f.events)
                    self.assertNotIn('persist', f.events)
                    self.assertEqual(original.decode((f.work / 'state.json').read_bytes())['phase'], 'REHEARSAL_FAILED')
                    self.assertTrue((f.work / 'clone-tail-completion/failure.json').is_file())
                finally:
                    f.close()

    def test_final_state_cas_drift_refuses_original_persist(self):
        def freeze():
            self.f.freeze()
            if self.f.events.count('freeze') == 2:
                (self.f.work / 'state.json').write_bytes(b'changed externally')
        self.f.controller.frozen_gates = freeze
        with self.assertRaises(tail.CompletionFailed):
            self.run_tail()
        self.assertNotIn('persist', self.f.events)
        self.assertTrue((self.f.work / 'clone-tail-completion/failure.json').is_file())

    def test_probe_entry_rejects_serve_and_any_unreviewed_operation(self):
        for label, mode in [('serve', 'serve'), ('candidate', 'apply'), ('candidate-schema', 'verify'), ('candidate', 'apply --serve')]:
            with self.subTest(label=label, mode=mode), self.assertRaises(original.GateFailed):
                self.f.controller.verify_candidate(label, mode)
        self.assertEqual(self.f.calls, [])
        with patch.object(tail, 'load_original', side_effect=AssertionError('must reject before import')):
            with self.assertRaises(SystemExit) as caught:
                tail.main(['serve'])
            self.assertEqual(caught.exception.code, 2)

    def test_source_seal_type_symlink_hardlink_importguard(self):
        with self.assertRaises(tail.CompletionFailed):
            tail.load_original(expected='0' * 64)
        with self.assertRaises(tail.CompletionFailed):
            tail.controller_type(types.SimpleNamespace(Controller=original.Controller))
        source = self.f.work / 'original.py'
        self.f.write(source, Path(tail.ORIGINAL_PATH).read_bytes())
        self.assertEqual(tail.load_original(source).__sealed_sha256__, tail.ORIGINAL_SHA)
        source.write_bytes(source.read_bytes() + b'changed')
        with self.assertRaises(tail.CompletionFailed):
            tail.load_original(source)
        source.unlink()
        source.symlink_to(tail.ORIGINAL_PATH)
        with self.assertRaises(tail.CompletionFailed):
            tail.load_original(source)
        source.unlink()
        os.mkfifo(source, 0o600)
        with self.assertRaises(tail.CompletionFailed):
            tail.load_original(source)
        source.unlink()
        self.f.write(source, Path(tail.ORIGINAL_PATH).read_bytes())
        os.link(source, self.f.work / 'linked-source.py')
        with self.assertRaises(tail.CompletionFailed):
            tail.load_original(source)


if __name__ == '__main__':
    unittest.main(verbosity=2)
