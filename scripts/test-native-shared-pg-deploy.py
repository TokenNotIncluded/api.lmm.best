#!/usr/bin/python3
"""Isolated wrapper hook tests; no systemd/PG/production mutations."""
import importlib.util
from pathlib import Path
from types import SimpleNamespace
import unittest

spec = importlib.util.spec_from_file_location('native_hooks', Path(__file__).with_name('native-shared-pg-deploy.py'))
native = importlib.util.module_from_spec(spec)
spec.loader.exec_module(native)


class HookTests(unittest.TestCase):
    def fixture(self, reject=None):
        calls = []
        capsule = {'root': '/owned/capsule', 'deployment_id': 'release-test',
                   'candidate': {'payload_sha256': 'candidate'}, 'rollback': {'payload_sha256': 'rollback'}}
        hooks = native.CapsuleHooks('/owned/capsule/capsule.json', 'digest', capsule)
        def guard(action):
            calls.append('guard:' + action)
            if action == reject:
                raise RuntimeError('actual guard refused')
        hooks.native = guard
        module = SimpleNamespace(BINARY=Path('/usr/bin/lmm-api-go'), digest=lambda path: str(path),
                                 run=lambda *args, **kw: calls.append('run:' + ':'.join(args)),
                                 install=lambda *args: calls.append('install'),
                                 verify=lambda *args, **kw: calls.append('old-unsealed-verify'),
                                 save=lambda *args: calls.append('save'), healthy=lambda *args: calls.append('healthy'),
                                 stop=lambda *args: calls.append('stop'), execute=lambda *args: calls.append('execute'),
                                 read_state=lambda *args, **kwargs: {'phase': 'STAGED', 'migrate': False})
        hooks.bind(module)
        return hooks, module, calls

    def test_missing_live_owner_blocks_all_mutations(self):
        hooks, module, calls = self.fixture()
        for operation in (lambda: module.run('systemctl', 'stop', 'lmm-api.service'),
                          lambda: module.run('systemctl', 'start', 'lmm-api.service'),
                          lambda: module.install('candidate', module.BINARY), lambda: module.stop('/work'),
                          lambda: module.save('/work', {'phase': 'CONFIRMED'})):
            with self.assertRaises(RuntimeError):
                operation()
        self.assertEqual(calls, [])

    def test_guard_failure_occurs_before_stop_install_start_confirm(self):
        hooks, module, calls = self.fixture('check-held')
        hooks.held = True
        for operation in (lambda: module.run('systemctl', 'stop', 'lmm-api.service'),
                          lambda: module.run('systemctl', 'start', 'lmm-api.service'),
                          lambda: module.install('candidate', module.BINARY),
                          lambda: module.save('/work', {'phase': 'CONFIRMED'})):
            with self.assertRaises(RuntimeError):
                operation()
        self.assertEqual(calls, ['guard:check-held'] * 4)

    def test_terminal_owner_state_then_exact_native_release(self):
        hooks, module, calls = self.fixture()
        hooks.held = True
        module.save('/work', {'phase': 'ROLLED_BACK'})
        self.assertEqual(calls, ['guard:check-held', 'save', 'guard:release'])
        self.assertFalse(hooks.held)

    def test_financial_and_unsealed_verifier_are_unavailable(self):
        hooks, module, calls = self.fixture()
        with self.assertRaises(RuntimeError):
            module.verify('/work', 'apply', mode='apply')
        with self.assertRaises(RuntimeError):
            module.run('/provider', 'migrate', '--apply')
        module.verify('/work', 'stage')
        self.assertEqual(calls, ['guard:check'])

    def test_unsupported_provider_blocks_before_ordinary_execute(self):
        hooks, module, calls = self.fixture('check')
        args = SimpleNamespace(action='apply', release='release-test', migrate=False, maintenance_handoff=None, binary=None)
        with self.assertRaises(RuntimeError):
            module.execute(args, None)
        self.assertEqual(calls, ['guard:check'])


if __name__ == '__main__':
    unittest.main()
