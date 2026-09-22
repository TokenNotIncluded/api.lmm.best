"""Exercise the real CLI/state machine in a private filesystem; never touch a host service."""
import contextlib
import fcntl
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('deploy_systemd_cli', Path(__file__).with_name('deploy-systemd.py'))
deploy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(deploy)


class WorkflowTests(unittest.TestCase):
    def setUp(self):
        self.stack = contextlib.ExitStack()
        self.addCleanup(self.stack.close)
        self.base = Path(self.stack.enter_context(tempfile.TemporaryDirectory()))
        paths = {
            'ROOT': self.base / 'transactions',
            'BINARY': self.base / 'bin' / 'lmm-api-go',
            'ENTRY': self.base / 'bin' / 'lmm-api',
            'FRONTEND': self.base / 'web',
            'ENVIRONMENT': self.base / 'service.env',
        }
        for name, value in paths.items():
            self.stack.enter_context(patch.object(deploy, name, value))
        deploy.BINARY.parent.mkdir()
        deploy.BINARY.write_bytes(b'old-provider')
        deploy.ENTRY.symlink_to('lmm-api-go')
        (deploy.FRONTEND / 'releases' / 'previous').mkdir(parents=True)
        (deploy.FRONTEND / 'releases' / 'previous' / 'index.html').write_text('old')
        (deploy.FRONTEND / 'current').symlink_to('releases/previous')
        deploy.ENVIRONMENT.write_text('fixture configuration, not a real credential')
        self.binary = self.base / 'candidate'
        self.binary.write_bytes(b'new-provider')
        self.frontend = self.base / 'candidate-web'
        self.frontend.mkdir()
        (self.frontend / 'index.html').write_text('new')
        self.events = []
        self.stack.enter_context(patch.object(deploy.os, 'geteuid', return_value=0))
        self.layout = self.stack.enter_context(patch.object(deploy, 'check_layout'))
        self.which = self.stack.enter_context(patch.object(deploy.shutil, 'which', return_value='/fixture/tool'))
        self.stack.enter_context(patch.object(deploy, 'property_value', side_effect=lambda key: {
            'MainPID': '123', 'EnvironmentFiles': str(deploy.ENVIRONMENT) + ' (ignore_errors=no)'
        }[key]))
        self.run = self.stack.enter_context(patch.object(deploy, 'run', side_effect=self.native_command))
        self.stop = self.stack.enter_context(patch.object(deploy, 'stop', side_effect=lambda work: self.events.append('stop')))
        self.verify = self.stack.enter_context(patch.object(deploy, 'verify', side_effect=lambda work, label, mode='verify': self.events.append('schema-' + label)))
        self.healthy = self.stack.enter_context(patch.object(deploy, 'healthy'))
        self.database = self.stack.enter_context(patch.object(deploy, 'database_environment', return_value={}))
        self.backup = self.stack.enter_context(patch.object(deploy, 'backup', side_effect=self.fake_backup))

    def native_command(self, *args, log=None):
        if args[-1] == 'version':
            return 'old-v1' if args[0] == str(deploy.ENTRY) else 'candidate-v2'
        if args[:3] == (str(deploy.ENTRY), 'operator', 'frontend') or 'rollback' in args:
            release = args[args.index('--release') + 1]
            if 'publish' in args:
                source = args[args.index('--source') + 1]
                shutil.copytree(source, deploy.FRONTEND / 'releases' / release)
            (deploy.FRONTEND / 'current').unlink()
            (deploy.FRONTEND / 'current').symlink_to('releases/' + release)
        if args[:2] == ('systemctl', 'start'):
            self.events.append('start')
        return ''

    def fake_backup(self, work, env, schema_only=False, exclude=()):
        self.events.append('backup-preflight' if schema_only else 'backup-full')
        if not schema_only:
            path = work / 'database.dump'
            path.write_bytes(b'isolated archive fixture; not an actual PostgreSQL backup')
            return deploy.digest(path)

    def call(self, *args):
        output, errors = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
            try:
                code = deploy.main(list(args))
            except SystemExit as error:
                code = error.code
        return code, output.getvalue(), errors.getvalue()

    def stage_args(self, action='stage', release='r1', migrate=False):
        args = [action, '--release', release, '--binary', str(self.binary), '--frontend', str(self.frontend)]
        if action == 'upgrade':
            args += ['--confirm', 'api.lmm.best']
        if migrate:
            args += ['--migrate']
        return args

    def mutate(self, action, release='r1', *extra):
        return self.call(action, '--release', release, '--confirm', 'api.lmm.best', *extra)

    def state(self, release='r1'):
        return json.loads((deploy.ROOT / release / 'state.json').read_text())

    def test_doctor_does_not_create_root_or_call_provider(self):
        code, output, errors = self.call('doctor', '--json')
        self.assertEqual(0, code, errors)
        self.assertTrue(json.loads(output)['ok'])
        self.assertFalse(deploy.ROOT.exists())
        self.run.assert_not_called()
        self.stop.assert_not_called()

    def test_doctor_aggregates_blockers_without_writes(self):
        self.which.return_value = None
        self.layout.side_effect = RuntimeError('provider is package-owned')
        code, output, _ = self.call('doctor', '--json', '--migrate')
        self.assertEqual(1, code)
        failures = [item for item in json.loads(output)['checks'] if not item['ok']]
        self.assertGreaterEqual(len(failures), 2)
        self.assertIn('psql', failures[0]['error'])
        self.assertFalse(deploy.ROOT.exists())

    def test_empty_status_is_read_only(self):
        code, output, errors = self.call('status', '--json')
        self.assertEqual(0, code, errors)
        self.assertEqual({'deployments': []}, json.loads(output))
        self.assertFalse(deploy.ROOT.exists())
        self.run.assert_not_called()

    def test_incomplete_preparation_is_visible_without_creating_lock(self):
        (deploy.ROOT / 'r1').mkdir(parents=True)
        code, output, _ = self.call('status')
        self.assertEqual(0, code)
        self.assertIn('PREPARATION_INCOMPLETE', output)
        self.assertFalse((deploy.ROOT / 'lock').exists())
        self.assertEqual(1, self.call('doctor', '--json')[0])

    def test_upgrade_keeps_one_lock_across_stage_and_apply(self):
        labels = []

        def verify_locked(work, label, mode='verify'):
            labels.append(label)
            with (deploy.ROOT / 'lock').open('a') as second:
                with self.assertRaises(BlockingIOError):
                    fcntl.flock(second, fcntl.LOCK_EX | fcntl.LOCK_NB)

        self.verify.side_effect = verify_locked
        code, output, errors = self.call(*self.stage_args('upgrade'), '--json')
        self.assertEqual(0, code, errors)
        self.assertEqual(['stage', 'apply'], labels)
        self.assertEqual('', errors)
        self.assertEqual('AWAITING_CONFIRMATION', json.loads(output)['phase'])
        self.assertEqual('AWAITING_CONFIRMATION', self.state()['phase'])
        self.assertEqual(b'new-provider', deploy.BINARY.read_bytes())
        self.stop.assert_called_once()

    def test_human_upgrade_shows_confirmation_and_evidence(self):
        code, output, errors = self.call(*self.stage_args('upgrade'))
        self.assertEqual(0, code, errors)
        self.assertIn('AWAITING_CONFIRMATION', output)
        self.assertIn('confirm --release r1 --confirm api.lmm.best', output)
        self.assertIn('rollback --release r1', output)
        self.assertIn('authenticated flows', output)
        self.assertIn('Checking prerequisites', errors)

    def test_upgrade_requires_authorization_before_creating_state(self):
        args = self.stage_args('upgrade')[:-2]
        self.assertEqual(2, self.call(*args)[0])
        self.assertFalse(deploy.ROOT.exists())
        self.stop.assert_not_called()

    def test_invalid_release_and_unknown_flags_fail_before_writes(self):
        for args in (['status', '--release', '../outside'], ['upgrade', '--force']):
            with self.subTest(args=args):
                self.assertEqual(2, self.call(*args)[0])
        self.assertFalse(deploy.ROOT.exists())

    def test_ignored_mutation_flags_are_rejected(self):
        for args in (
            ['status', '--migrate'],
            ['status', '--binary', str(self.binary)],
            self.stage_args() + ['--backup-exclude-table', 'public.archive'],
        ):
            with self.subTest(args=args):
                self.assertEqual(2, self.call(*args)[0])
        self.assertFalse(deploy.ROOT.exists())

    def test_missing_psql_fails_before_staging_or_stopping(self):
        self.which.side_effect = lambda tool: None if tool == 'psql' else '/fixture/tool'
        code, output, errors = self.call(*self.stage_args('upgrade', migrate=True), '--json')
        self.assertEqual(1, code, errors)
        self.assertIn('psql', json.loads(output)['error'])
        self.assertFalse((deploy.ROOT / 'r1').exists())
        self.backup.assert_not_called()
        self.stop.assert_not_called()

    def test_package_owned_provider_never_enters_standalone_transaction(self):
        self.layout.side_effect = RuntimeError('provider is package-owned; use the package deployment path')
        code, output, _ = self.call(*self.stage_args('upgrade'), '--json')
        self.assertEqual(1, code)
        self.assertIn('package-owned', json.loads(output)['error'])
        self.assertFalse((deploy.ROOT / 'r1').exists())
        self.stop.assert_not_called()

    def test_stage_failure_leaves_evidence_without_stopping(self):
        self.verify.side_effect = RuntimeError('schema is incompatible')
        code, _, errors = self.call(*self.stage_args('upgrade'))
        self.assertEqual(1, code)
        self.assertIn('PREPARATION_INCOMPLETE', errors)
        self.assertTrue((deploy.ROOT / 'r1' / 'lmm-api-go').exists())
        self.assertFalse((deploy.ROOT / 'r1' / 'state.json').exists())
        self.stop.assert_not_called()
        self.assertEqual(b'old-provider', deploy.BINARY.read_bytes())

    def test_repeated_upgrade_does_not_overwrite_or_implicitly_apply(self):
        self.assertEqual(0, self.call(*self.stage_args())[0])
        before = (deploy.ROOT / 'r1' / 'state.json').read_bytes()
        code, _, errors = self.call(*self.stage_args('upgrade'))
        self.assertEqual(1, code)
        self.assertIn('release already exists', errors)
        self.assertIn('apply --release r1', errors)
        self.assertEqual(before, (deploy.ROOT / 'r1' / 'state.json').read_bytes())
        self.stop.assert_not_called()

    def test_apply_requires_exact_migration_choice_in_both_directions(self):
        for release, migrate in [('plain', False), ('schema', True)]:
            with self.subTest(migrate=migrate):
                self.assertEqual(0, self.call(*self.stage_args(release=release, migrate=migrate))[0])
                extra = [] if migrate else ['--migrate']
                code, _, errors = self.mutate('apply', release, *extra)
                self.assertEqual(1, code)
                self.assertIn('immutable staged plan', errors)
                self.assertEqual('STAGED', self.state(release)['phase'])
        self.stop.assert_not_called()

    def test_tampered_staged_artifacts_fail_before_stop(self):
        for release, relative in [('binary', 'lmm-api-go'), ('frontend', 'frontend/index.html')]:
            with self.subTest(relative=relative):
                self.assertEqual(0, self.call(*self.stage_args(release=release))[0])
                (deploy.ROOT / release / relative).write_text('tampered')
                code, _, errors = self.mutate('apply', release)
                self.assertEqual(1, code)
                self.assertIn('changed', errors)
        self.stop.assert_not_called()

    def test_pending_transaction_blocks_second_activation(self):
        self.assertEqual(0, self.call(*self.stage_args('upgrade'))[0])
        code, _, errors = self.call(*self.stage_args('upgrade', release='r2'))
        self.assertEqual(1, code)
        self.assertIn('another deployment needs recovery', errors)
        self.assertEqual('STAGED', self.state('r2')['phase'])
        self.stop.assert_called_once()

    def test_migration_backup_happens_after_stop_and_before_migration(self):
        code, _, errors = self.call(*self.stage_args('upgrade', migrate=True))
        self.assertEqual(0, code, errors)
        self.assertEqual(2, self.events.count('backup-preflight'))
        self.assertLess(self.events.index('stop'), self.events.index('backup-full'))
        self.assertLess(self.events.index('backup-full'), self.events.index('schema-migrate'))
        self.assertLess(self.events.index('schema-post-migrate'), self.events.index('start'))
        self.assertIn('database_backup_sha256', self.state())

    def test_readiness_failure_keeps_pending_state_without_automatic_rollback(self):
        self.healthy.side_effect = RuntimeError('fixture unavailable')
        with patch.object(deploy.time, 'monotonic', side_effect=[0, 121]):
            code, _, errors = self.call(*self.stage_args('upgrade'))
        self.assertEqual(1, code)
        self.assertEqual('MUTATION_PENDING', self.state()['phase'])
        self.assertIn('rollback --release r1', errors)
        self.assertEqual(b'new-provider', deploy.BINARY.read_bytes())
        self.assertEqual(1, self.events.count('start'))

    def test_interrupt_after_mutation_marker_never_retries(self):
        self.stop.side_effect = KeyboardInterrupt
        code, output, _ = self.call(*self.stage_args('upgrade'), '--json')
        self.assertEqual(130, code)
        self.assertFalse(json.loads(output)['ok'])
        self.assertEqual('MUTATION_PENDING', self.state()['phase'])
        self.assertEqual(b'old-provider', deploy.BINARY.read_bytes())
        self.stop.assert_called_once()

    def test_confirm_requires_observation_then_succeeds(self):
        self.assertEqual(0, self.call(*self.stage_args('upgrade'))[0])
        ready = self.state()['ready_at']
        with patch.object(deploy.time, 'time', return_value=ready + 60):
            code, _, errors = self.mutate('confirm')
        self.assertEqual(1, code)
        self.assertIn('120 seconds', errors)
        self.assertEqual('AWAITING_CONFIRMATION', self.state()['phase'])
        with patch.object(deploy.time, 'time', return_value=ready + 121):
            self.assertEqual(0, self.mutate('confirm')[0])
        self.assertEqual('CONFIRMED', self.state()['phase'])
        self.assertTrue((deploy.ROOT / 'r1' / 'previous-binary').exists())

    def test_confirm_rejects_changed_frontend(self):
        self.assertEqual(0, self.call(*self.stage_args('upgrade'))[0])
        (deploy.FRONTEND / 'current').unlink()
        (deploy.FRONTEND / 'current').symlink_to('releases/previous')
        with patch.object(deploy.time, 'time', return_value=self.state()['ready_at'] + 121):
            code, _, errors = self.mutate('confirm')
        self.assertEqual(1, code)
        self.assertIn('active frontend changed', errors)
        self.assertEqual('AWAITING_CONFIRMATION', self.state()['phase'])

    def test_rollback_restores_software_not_database(self):
        self.assertEqual(0, self.call(*self.stage_args('upgrade', migrate=True))[0])
        archive = deploy.ROOT / 'r1' / 'database.dump'
        before = archive.read_bytes()
        code, _, errors = self.mutate('rollback')
        self.assertEqual(0, code, errors)
        self.assertEqual('ROLLED_BACK', self.state()['phase'])
        self.assertEqual(b'old-provider', deploy.BINARY.read_bytes())
        self.assertEqual('releases/previous', os.readlink(deploy.FRONTEND / 'current'))
        self.assertEqual(before, archive.read_bytes())
        self.assertEqual(1, self.events.count('backup-full'))

    def test_legacy_noninteractive_status_keeps_json(self):
        self.assertEqual(0, self.call(*self.stage_args())[0])
        before = {path: path.stat().st_mtime_ns for path in deploy.ROOT.rglob('*')}
        code, output, errors = self.call('status', '--release', 'r1')
        self.assertEqual(0, code, errors)
        self.assertEqual(self.state(), json.loads(output))
        self.assertEqual(before, {path: path.stat().st_mtime_ns for path in deploy.ROOT.rglob('*')})
        self.assertIn('Prepared only', self.call('status', '--release', 'r1', '--human')[1])

    def test_root_and_transaction_symlinks_are_rejected(self):
        outside = self.base / 'outside'
        outside.mkdir()
        deploy.ROOT.symlink_to(outside, target_is_directory=True)
        self.assertEqual(1, self.call('status', '--json')[0])
        self.assertEqual(1, self.call('doctor', '--json')[0])
        self.assertEqual([], list(outside.iterdir()))
        deploy.ROOT.unlink()
        deploy.ROOT.mkdir()
        (deploy.ROOT / 'r1').symlink_to(outside, target_is_directory=True)
        self.assertEqual(1, self.call('status', '--release', 'r1', '--json')[0])
        self.assertEqual(1, self.mutate('apply')[0])
        self.assertEqual([], list(outside.iterdir()))
        self.stop.assert_not_called()

    def test_corrupt_or_linked_state_is_not_repaired(self):
        self.assertEqual(0, self.call(*self.stage_args())[0])
        path = deploy.ROOT / 'r1' / 'state.json'
        for content in ('invalid json', '{"release":"other","phase":"STAGED"}', '[]'):
            path.write_text(content)
            self.assertEqual(1, self.call('status', '--release', 'r1', '--json')[0])
            self.assertEqual(content, path.read_text())
        path.unlink()
        path.symlink_to(self.binary)
        self.assertEqual(1, self.call('status', '--release', 'r1', '--json')[0])
        self.assertEqual(b'new-provider', self.binary.read_bytes())

    def test_nginx_preflight_failure_does_not_stop_writer(self):
        def fail_nginx(*args, **kwargs):
            if args[0] == 'nginx':
                raise RuntimeError('nginx validation failed')
            return self.native_command(*args, **kwargs)

        self.run.side_effect = fail_nginx
        code, output, errors = self.call(*self.stage_args('upgrade'), '--json')
        self.assertEqual(1, code)
        self.assertEqual('', errors)
        self.assertIn('nginx', json.loads(output)['error'])
        self.assertEqual('STAGED', self.state()['phase'])
        self.assertEqual(b'old-provider', deploy.BINARY.read_bytes())
        self.stop.assert_not_called()

    def test_database_backup_failure_retains_recovery_before_install(self):
        def fail_full_backup(work, env, schema_only=False, exclude=()):
            if not schema_only:
                raise RuntimeError('database backup failed')

        self.backup.side_effect = fail_full_backup
        code, _, errors = self.call(*self.stage_args('upgrade', migrate=True))
        self.assertEqual(1, code)
        self.assertEqual('MUTATION_PENDING', self.state()['phase'])
        self.assertEqual(b'old-provider', deploy.BINARY.read_bytes())
        self.assertEqual(b'old-provider', (deploy.ROOT / 'r1' / 'previous-binary').read_bytes())
        self.assertIn('rollback --release r1', errors)
        self.assertNotIn('schema-migrate', self.events)
        self.assertNotIn('start', self.events)
        self.stop.assert_called_once()

    def test_artifact_directory_symlink_is_rejected_before_staging(self):
        link = self.base / 'linked-web'
        link.symlink_to(self.frontend, target_is_directory=True)
        self.frontend = link
        code, output, _ = self.call(*self.stage_args('upgrade'), '--json')
        self.assertEqual(1, code)
        self.assertIn('real frontend directory', json.loads(output)['error'])
        self.assertFalse((deploy.ROOT / 'r1').exists())
        self.stop.assert_not_called()

    def test_lock_symlink_is_not_followed(self):
        deploy.ROOT.mkdir()
        (deploy.ROOT / 'lock').symlink_to(self.binary)
        code, output, _ = self.call(*self.stage_args('upgrade'), '--json')
        self.assertEqual(1, code)
        self.assertIn('lock may not be a symlink', json.loads(output)['error'])
        self.assertEqual(b'new-provider', self.binary.read_bytes())
        self.stop.assert_not_called()

    def test_busy_lock_fails_without_waiting_or_mutation(self):
        deploy.ROOT.mkdir()
        with (deploy.ROOT / 'lock').open('a') as owner:
            fcntl.flock(owner, fcntl.LOCK_EX | fcntl.LOCK_NB)
            code, output, _ = self.call(*self.stage_args('upgrade'), '--json')
        self.assertEqual(1, code)
        self.assertIn('holds the lock', json.loads(output)['error'])
        self.assertFalse((deploy.ROOT / 'r1').exists())
        self.stop.assert_not_called()


class EntrypointTests(unittest.TestCase):
    def test_help_without_provider(self):
        launcher = Path(__file__).with_name('lmm-api-deploy.sh')
        env = dict(os.environ, LMM_API_PROVIDER_BINARY='/nonexistent-fixture-provider')
        for args in ([], ['help'], ['--help'], ['-h'], ['systemd', '--help']):
            with self.subTest(args=args):
                result = subprocess.run(['bash', str(launcher), *args], env=env, capture_output=True, text=True)
                self.assertEqual(0, result.returncode, result.stderr)
                self.assertIn('upgrade', result.stdout)
                self.assertEqual('', result.stderr)

    def test_native_commands_preserve_arguments_and_missing_provider_is_actionable(self):
        launcher = Path(__file__).with_name('lmm-api-deploy.sh')
        with tempfile.TemporaryDirectory() as directory:
            provider = Path(directory) / 'provider with spaces'
            provider.write_text('#!/usr/bin/env bash\nprintf "%s\\n" "$@"\n')
            provider.chmod(0o755)
            env = dict(os.environ, LMM_API_PROVIDER_BINARY=str(provider))
            result = subprocess.run(['bash', str(launcher), 'production', 'plan', 'a b'], env=env, capture_output=True, text=True)
            self.assertEqual(0, result.returncode, result.stderr)
            self.assertEqual(['operator', 'production', 'plan', 'a b'], result.stdout.splitlines())
            provider.unlink()
            result = subprocess.run(['bash', str(launcher), 'build'], env=env, capture_output=True, text=True)
            self.assertEqual(127, result.returncode)
            self.assertIn('Use --help', result.stderr)


if __name__ == '__main__':
    unittest.main()
