#!/usr/bin/env python3
"""Pure local owner/lock fixtures: no service, database or remote operation."""
import contextlib
import fcntl
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('original_owner_tests', Path(__file__).with_name('test-deploy-systemd.py'))
fixtures = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixtures)
deploy = fixtures.deploy


class ArchiveStagedTests(unittest.TestCase):
    write = fixtures.CleanupTests.write
    state = fixtures.CleanupTests.state
    workspace = fixtures.CleanupTests.workspace
    install_chain = fixtures.CleanupTests.install_chain
    maintenance = fixtures.CleanupTests.maintenance
    snapshot = fixtures.CleanupTests.snapshot
    proof = fixtures.OrdinaryHistoryTests.proof
    incomplete = fixtures.OrdinaryHistoryTests.incomplete

    def setUp(self):
        fixtures.CleanupTests.setUp(self)
        self.proof()
        self.history_args.execute = True
        deploy.register_released_history(self.history_args)
        self.unit = {'MainPID': '12345', 'InvocationID': 'a' * 32, 'ActiveState': 'active', 'SubState': 'running'}
        self.stack.enter_context(patch.object(deploy, 'property_value', side_effect=lambda key: self.unit[key]))
        # Test the same exact source digest/private file contract as the installed sibling.
        self.owner = self.write(self.base / 'owner.py', Path(deploy.__file__).read_bytes())
        self.stack.enter_context(patch.object(deploy, '__file__', str(self.owner)))
        self.stack.enter_context(patch.object(deploy, 'run', side_effect=AssertionError('candidate/service/SQL command forbidden')))
        self.stack.enter_context(patch.object(deploy, 'stop', side_effect=AssertionError('stop forbidden')))
        self.stack.enter_context(patch.object(deploy, 'install', side_effect=AssertionError('install forbidden')))
        # Guardian source ownership check uses this process UID, not a fake UID.
        self.guardian_source = self.write(self.base / 'maintenance-deploy-guardian.py', Path(deploy.guardian.__file__).read_bytes())
        self.stack.enter_context(patch.object(deploy.guardian, '__file__', str(self.guardian_source)))
        self.work = deploy.ROOT / 'next'
        self.work.mkdir(mode=0o700)
        self.write(self.work / 'lmm-api-go', b'new signed provider', 0o755)
        (self.work / 'lmm-api').symlink_to('lmm-api-go')
        (self.work / 'frontend').mkdir(mode=0o700)
        self.write(self.work / 'frontend/index.html', b'new signed frontend', 0o644)
        (self.work / 'logs').mkdir(mode=0o700)
        self.write(self.work / 'logs/oneapi.log', b'original preparation evidence')
        self.write(self.work / 'verify-stage.log', b'full candidate verify succeeded')
        self.value = {'release': 'next', 'version': 'v0.2.86', 'sha256': deploy.digest(self.work / 'lmm-api-go'),
                      'frontend_sha256': deploy.tree_digest(self.work / 'frontend'), 'migrate': False,
                      'backup_exclude_tables': [], 'phase': 'STAGED'}
        self.state(self.work, self.value)
        self.args = SimpleNamespace(release='next', staged_state_sha256=deploy.digest(self.work / 'state.json'),
                                    owner_source_sha256=deploy.digest(self.owner), execute=False)

    def replace_state(self, value):
        self.state(self.work, value)
        self.args.staged_state_sha256 = deploy.digest(self.work / 'state.json')

    def test_preview_preserves_every_byte_and_creates_no_archive(self):
        before = self.snapshot(self.base)
        result = deploy.archive_staged(self.args)
        self.assertEqual('lmm-systemd-staged-archive-v1', result['format'])
        self.assertFalse(result['execute'])
        self.assertEqual(before, self.snapshot(self.base))
        self.assertFalse((deploy.history_root() / 'staged').exists())
        self.probes.assert_not_called()

    def test_exact_recreated_id_archives_full_state_and_restores_old_incomplete_invariant(self):
        old, incomplete_args = self.incomplete()
        incomplete_args.execute = True
        deploy.archive_incomplete(incomplete_args)
        old_archive = deploy.history_root() / 'incomplete/incomplete'
        old_bytes = self.snapshot(old_archive)
        self.work.rename(old)
        self.work = old
        self.value['release'] = old.name
        self.replace_state(self.value)
        self.args.release = old.name
        original = self.snapshot(old)
        with self.assertRaises(RuntimeError):
            deploy.verify_incomplete_archives()
        deploy.archive_staged(self.args)  # Preview does not relax or rewrite the old guard.
        with self.assertRaises(RuntimeError):
            deploy.verify_incomplete_archives()
        self.args.execute = True
        result = deploy.archive_staged(self.args)
        archive = Path(result['archive_path'])
        self.assertFalse(old.exists())
        self.assertEqual(original, self.snapshot(archive / 'copy'))
        self.assertEqual(original, self.snapshot(archive / 'original'))
        self.assertEqual(old_bytes, self.snapshot(old_archive))
        self.assertEqual((archive / 'intent.json').read_bytes(), (archive / 'receipt.json').read_bytes())
        self.assertEqual('STAGED', json.loads((archive / 'original/state.json').read_bytes())['phase'])
        deploy.verify_incomplete_archives()
        deploy.verify_staged_archives()
        with self.assertRaises(RuntimeError):
            deploy.reject_archived_release_id(old.name)

    def test_nonordinary_state_or_mutation_evidence_is_refused_without_writes(self):
        for key, value in [('phase', 'AWAITING_CONFIRMATION'), ('migrate', True), ('migrate', 0),
                           ('maintenance_handoff', {}), ('previous_frontend', 'current'), ('backup_exclude_tables', ['public.history'])]:
            with self.subTest(key=key, value=value):
                self.replace_state(dict(self.value, **{key: value}))
                before = self.snapshot(self.base)
                with self.assertRaises(RuntimeError):
                    deploy.archive_staged(self.args)
                self.assertEqual(before, self.snapshot(self.base))
        self.replace_state(self.value)
        for name in ('previous-binary', 'previous_state', 'stop.log', 'state.next', 'unknown'):
            path = self.write(self.work / name, b'unsafe evidence')
            with self.assertRaises(RuntimeError):
                deploy.archive_staged(self.args)
            path.unlink()

    def test_wrong_source_state_provider_or_frontend_digest_refuses(self):
        for field in ('owner_source_sha256', 'staged_state_sha256'):
            old = getattr(self.args, field)
            setattr(self.args, field, 'f' * 64)
            with self.assertRaises(RuntimeError):
                deploy.archive_staged(self.args)
            setattr(self.args, field, old)
        for path in (self.work / 'lmm-api-go', self.work / 'frontend/index.html'):
            old = path.read_bytes()
            path.write_bytes(b'changed')
            with self.assertRaises(RuntimeError):
                deploy.archive_staged(self.args)
            path.write_bytes(old)
        self.assertFalse((deploy.history_root() / 'staged').exists())

    def test_symlink_hardlink_or_unsafe_permissions_refuses(self):
        file = self.work / 'logs/oneapi.log'
        link = self.work / 'logs/link'
        link.symlink_to(self.base / 'service.env')
        with self.assertRaises(RuntimeError):
            deploy.archive_staged(self.args)
        link.unlink()
        os.link(file, link)
        with self.assertRaises(RuntimeError):
            deploy.archive_staged(self.args)
        link.unlink()
        file.chmod(0o666)
        with self.assertRaises(RuntimeError):
            deploy.archive_staged(self.args)

    def test_current_nminus1_other_staged_or_process_reference_refuses(self):
        current = deploy.read_state(self.current)
        current['previous_frontend'] = self.work.name
        self.state(self.current, current)
        # Isolate the N-1 reference guard from released-history's independent
        # original-state integrity guard (covered by the original owner suite).
        with patch.object(deploy, 'released_ancestors', return_value={'bridge', 'capture'}):
            with self.assertRaisesRegex(RuntimeError, 'authoritative owner references'):
                deploy.archive_staged(self.args)
        current['previous_frontend'] = 'frozen'
        self.state(self.current, current)
        other = self.workspace('other', 'STAGED')
        value = deploy.read_state(other)
        for key, ref in [('artifact_reference', str(self.work / 'frontend')), ('previous_deployment_id', self.work.name)]:
            self.state(other, dict(value, **{key: ref}))
            with self.assertRaises(RuntimeError):
                deploy.archive_staged(self.args)
        shutil.rmtree(other)
        with patch.object(deploy, 'cleanup_process_references', return_value=[{'pid': 987, 'kind': 'cwd'}]):
            with self.assertRaises(RuntimeError):
                deploy.archive_staged(self.args)
        self.assertFalse((deploy.history_root() / 'staged').exists())

    def test_all_three_real_locks_and_native_lease_refuse_and_preserve_inodes(self):
        before = {name: Path(path).stat().st_ino for name, path in self.locks.items()}
        for name, path in self.locks.items():
            with self.subTest(lock=name), open(path, 'rb') as held:
                fcntl.flock(held, fcntl.LOCK_EX | fcntl.LOCK_NB)
                with self.assertRaisesRegex(RuntimeError, 'live owner or guardian'):
                    deploy.archive_staged(self.args)
                with open(path, 'rb') as contender:
                    with self.assertRaises(BlockingIOError):
                        fcntl.flock(contender, fcntl.LOCK_EX | fcntl.LOCK_NB)
        deploy.NATIVE_TRANSACTION_LEASE.write_bytes(b'live owner')
        with self.assertRaisesRegex(RuntimeError, 'native deployment lease'):
            deploy.archive_staged(self.args)
        self.assertEqual(before, {name: Path(path).stat().st_ino for name, path in self.locks.items()})

    def test_copy_readback_failure_never_moves_original_and_partial_attempt_refuses_replay(self):
        real_copy = deploy.shutil.copytree
        def corrupt(source, target, *args, **kwargs):
            result = real_copy(source, target, *args, **kwargs)
            if Path(source) == self.work:
                (Path(target) / 'frontend/index.html').write_bytes(b'corrupted copy')
            return result
        self.args.execute = True
        original = self.snapshot(self.work)
        with patch.object(deploy.shutil, 'copytree', side_effect=corrupt):
            with self.assertRaises(RuntimeError):
                deploy.archive_staged(self.args)
        self.assertEqual(original, self.snapshot(self.work))
        self.assertFalse((deploy.history_root() / 'staged/next/intent.json').exists())
        with self.assertRaises((OSError, RuntimeError)):
            deploy.archive_staged(self.args)
        with self.assertRaises(RuntimeError):
            deploy.reject_archived_release_id('next')

    def test_owner_cas_change_after_copy_preserves_original_and_intent(self):
        real_write = deploy.immutable_write
        def drift(path, body, mode):
            real_write(path, body, mode)
            if path.name == 'intent.json':
                self.unit['InvocationID'] = 'b' * 32
        self.args.execute = True
        original = self.snapshot(self.work)
        with patch.object(deploy, 'immutable_write', side_effect=drift):
            with self.assertRaises(RuntimeError):
                deploy.archive_staged(self.args)
        self.assertEqual(original, self.snapshot(self.work))
        self.assertTrue((deploy.history_root() / 'staged/next/intent.json').exists())
        self.assertFalse((deploy.history_root() / 'staged/next/receipt.json').exists())

    def test_state_cas_after_copy_refuses_before_rename(self):
        real_write = deploy.immutable_write
        def drift(path, body, mode):
            real_write(path, body, mode)
            if path.name == 'intent.json':
                self.state(self.work, dict(self.value, phase='MUTATION_PENDING'))
        self.args.execute = True
        with patch.object(deploy, 'immutable_write', side_effect=drift):
            with self.assertRaises(RuntimeError):
                deploy.archive_staged(self.args)
        self.assertTrue(self.work.exists())
        self.assertFalse((deploy.history_root() / 'staged/next/original').exists())

    def test_lock_name_replacement_after_intent_refuses_before_rename(self):
        real_write = deploy.immutable_write
        selected = Path(self.locks['frontend'])
        def drift(path, body, mode):
            real_write(path, body, mode)
            if path.name == 'intent.json':
                selected.unlink()
                self.write(selected, b'replaced lock inode')
        self.args.execute = True
        with patch.object(deploy, 'immutable_write', side_effect=drift):
            with self.assertRaisesRegex(RuntimeError, 'lock inode changed'):
                deploy.archive_staged(self.args)
        self.assertTrue(self.work.exists())
        self.assertFalse((deploy.history_root() / 'staged/next/original').exists())

    def test_old_incomplete_corruption_never_publishes_completion(self):
        old, args = self.incomplete()
        args.execute = True
        deploy.archive_incomplete(args)
        self.write(deploy.history_root() / 'incomplete/incomplete/copy/verify-stage.log', b'bad original archive')
        self.args.execute = True
        with self.assertRaises(RuntimeError):
            deploy.archive_staged(self.args)
        archive = deploy.history_root() / 'staged/next'
        self.assertTrue((archive / 'original/state.json').exists())
        self.assertFalse((archive / 'receipt.json').exists())
        with self.assertRaises((OSError, RuntimeError)):
            deploy.verify_staged_archives()

    def test_receipt_crash_keeps_original_copy_intent_and_blocks_reuse(self):
        real_write = deploy.immutable_write
        def crash(path, body, mode):
            if path.name == 'receipt.json':
                raise OSError('injected durable receipt failure')
            real_write(path, body, mode)
        self.args.execute = True
        original = self.snapshot(self.work)
        with patch.object(deploy, 'immutable_write', side_effect=crash):
            with self.assertRaises(OSError):
                deploy.archive_staged(self.args)
        archive = deploy.history_root() / 'staged/next'
        self.assertEqual(original, self.snapshot(archive / 'original'))
        self.assertEqual(original, self.snapshot(archive / 'copy'))
        self.assertTrue((archive / 'intent.json').exists())
        self.assertFalse((archive / 'receipt.json').exists())
        with self.assertRaises(RuntimeError):
            deploy.reject_archived_release_id('next')

    def test_stage_rejects_complete_or_partial_archived_ids_before_artifact_execution(self):
        for kind in ('incomplete', 'staged'):
            archive = deploy.history_root() / kind / 'reserved'
            archive.mkdir(mode=0o700, parents=True)
            with patch.object(deploy.os, 'geteuid', return_value=0), patch.object(deploy, 'deployment_lock', side_effect=self.fake_lock), patch.object(deploy, 'check_tools') as tools:
                out = io.StringIO()
                with contextlib.redirect_stdout(out):
                    code = deploy.main(['stage', '--release', 'reserved', '--json'])
                self.assertEqual(1, code)
                self.assertIn('new unique ID', out.getvalue())
                tools.assert_not_called()
            self.assertFalse((deploy.ROOT / 'reserved').exists())

    def test_ordinary_apply_refuses_partial_staged_archive_before_stop_or_install(self):
        archive = deploy.history_root() / 'staged' / 'next'
        archive.mkdir(mode=0o700, parents=True)
        with patch.object(deploy.os, 'geteuid', return_value=0), patch.object(deploy, 'deployment_lock', side_effect=self.fake_lock), patch.object(deploy, 'check_tools'), patch.object(deploy, 'stop') as stop, patch.object(deploy, 'install') as install:
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                code = deploy.main(['apply', '--release', 'next', '--confirm', 'api.lmm.best', '--json'])
            self.assertEqual(1, code)
            self.assertIn('receipt.json', out.getvalue())
            stop.assert_not_called()
            install.assert_not_called()
        self.assertEqual('STAGED', deploy.read_state(self.work)['phase'])

    @contextlib.contextmanager
    def fake_lock(self, *args, **kwargs):
        yield

    def test_cli_binds_exact_source_and_state_and_rejects_financial_arguments(self):
        args = ['archive-staged', '--release', 'next', '--owner-source-sha256', self.args.owner_source_sha256,
                '--staged-state-sha256', self.args.staged_state_sha256, '--confirm', 'api.lmm.best', '--json']
        with patch.object(deploy.os, 'geteuid', return_value=0), contextlib.redirect_stdout(io.StringIO()) as out:
            self.assertEqual(0, deploy.main(args))
            self.assertFalse(json.loads(out.getvalue())['execute'])
        for extra in [['--migrate'], ['--all-admission-closed', '/receipt'], ['--global-confirmation', '/receipt']]:
            with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                deploy.main(args + extra)
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            deploy.main(['archive-staged', '--release', 'next', '--confirm', 'api.lmm.best'])


if __name__ == '__main__':
    unittest.main()
