#!/usr/bin/python3
"""Isolated wrapper hook tests; no systemd/PG/production mutations."""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('native_hooks', Path(__file__).with_name('native-shared-pg-deploy.py'))
native = importlib.util.module_from_spec(spec)
spec.loader.exec_module(native)


class HookTests(unittest.TestCase):
    def fixture(self, reject=None, read_state=None):
        calls = []
        capsule = {'root': '/owned/capsule', 'deployment_id': 'release-test',
                   'candidate': {'payload_sha256': 'candidate'}, 'rollback': {'payload_sha256': 'rollback'}}
        hooks = native.CapsuleHooks('/owned/capsule/capsule.json', 'digest', capsule)
        def guard(action):
            calls.append('guard:' + action)
            if action == reject:
                raise RuntimeError('actual guard refused')
        hooks.native = guard
        module = SimpleNamespace(ROOT=Path('/owned/transactions'), BINARY=Path('/usr/bin/lmm-api-go'), digest=lambda path: str(path),
                                 run=lambda *args, **kw: calls.append('run:' + ':'.join(args)),
                                 install=lambda *args: calls.append('install'),
                                 verify=lambda *args, **kw: calls.append('old-unsealed-verify'),
                                 save=lambda *args: calls.append('save'), healthy=lambda *args: calls.append('healthy'),
                                 stop=lambda *args: calls.append('stop'), execute=lambda *args: calls.append('execute'),
                                 read_state=read_state or (lambda *args, **kwargs: {'phase': 'STAGED', 'migrate': False}))
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

    def test_capsule_transaction_cannot_adopt_financial_state(self):
        for financial in ({'migrate': True}, {'maintenance_handoff': {'path': '/private/handoff'}}):
            with self.subTest(financial=financial):
                state = {'phase': 'STAGED', **financial}
                reads = []
                def read(work, allow_incomplete=False):
                    reads.append((work, allow_incomplete))
                    return state
                hooks, module, calls = self.fixture(read_state=read)
                work = module.ROOT / hooks.capsule['deployment_id']
                with self.assertRaisesRegex(RuntimeError, 'historical financial owner'):
                    module.read_state(work=work, allow_incomplete=True)
                self.assertEqual([(work, True)], reads)
                self.assertEqual(calls, [])

    def test_other_financial_history_remains_available_to_owner(self):
        state = {'phase': 'CONFIRMED', 'migrate': True, 'maintenance_handoff': {'path': '/private/handoff'}}
        hooks, module, calls = self.fixture(read_state=lambda *args, **kwargs: state)
        self.assertIs(state, module.read_state(module.ROOT / 'previous-financial-release'))
        self.assertEqual(calls, [])

    def test_ordinary_apply_preserves_owner_history_lifecycle_checks(self):
        # Use the real owner so accepting a history read cannot accidentally
        # bypass its preflight conflict check or permit an unregistered FROZEN
        # owner. No service, native provider or database command is executed.
        for phase in ('CONFIRMED', 'ROLLED_BACK', 'FROZEN', 'MUTATION_PENDING'):
            with self.subTest(phase=phase), tempfile.TemporaryDirectory() as directory:
                spec = importlib.util.spec_from_file_location('native_test_owner', Path(__file__).with_name('deploy-systemd.py'))
                owner = importlib.util.module_from_spec(spec)
                spec.loader.exec_module(owner)
                root = Path(directory) / 'transactions'
                work = root / 'release-test'
                history = root / 'previous-financial-release'
                work.mkdir(parents=True)
                history.mkdir()
                (work / 'lmm-api-go').write_bytes(b'fixture candidate')
                (work / 'frontend').mkdir()
                (work / 'frontend/index.html').write_text('fixture frontend')
                state = {'release': work.name, 'phase': 'STAGED', 'migrate': False,
                         'sha256': owner.digest(work / 'lmm-api-go'),
                         'frontend_sha256': owner.tree_digest(work / 'frontend')}
                (work / 'state.json').write_text(json.dumps(state))
                historical = {'release': history.name, 'phase': phase, 'migrate': True,
                              'maintenance_handoff': {'path': '/private/handoff', 'sha256': 'historical'}}
                history_file = history / 'state.json'
                history_file.write_text(json.dumps(historical))
                original_history = history_file.read_bytes()
                guards = []
                capsule = {'root': '/owned/capsule', 'deployment_id': work.name,
                           'candidate': {'payload_sha256': state['sha256']},
                           'rollback': {'payload_sha256': 'rollback'}}
                hooks = native.CapsuleHooks('/owned/capsule/capsule.json', 'digest', capsule)
                hooks.native = guards.append
                with patch.object(owner, 'ROOT', root), patch.object(owner.os, 'geteuid', return_value=0), \
                        patch.object(owner, 'check_layout'), patch.object(owner, 'check_tools'), \
                        patch.object(owner, 'stop') as stop, \
                        patch.object(owner, 'run', side_effect=RuntimeError('fixture reached nginx preflight')) as run:
                    hooks.bind(owner)
                    output = io.StringIO()
                    with contextlib.redirect_stdout(output):
                        code = owner.main(['apply', '--release', work.name, '--confirm', 'api.lmm.best', '--json'])
                    self.assertEqual(1, code)
                    if phase in ('CONFIRMED', 'ROLLED_BACK'):
                        self.assertIn('fixture reached nginx preflight', output.getvalue())
                        run.assert_called_once()
                        self.assertEqual(['check', 'ensure', 'check-held'], guards)
                    else:
                        self.assertIn('another deployment needs recovery', output.getvalue())
                        run.assert_not_called()
                        self.assertEqual(['check', 'ensure'], guards)
                    stop.assert_not_called()
                self.assertEqual(original_history, history_file.read_bytes())
                self.assertEqual('STAGED', json.loads((work / 'state.json').read_text())['phase'])


if __name__ == '__main__':
    unittest.main()
