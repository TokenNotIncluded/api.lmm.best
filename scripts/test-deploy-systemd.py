import importlib.util
import contextlib
import fcntl
import io
import json
import multiprocessing
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('deploy_systemd', Path(__file__).with_name('deploy-systemd.py'))
deploy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(deploy)


class DeploymentTests(unittest.TestCase):
    def test_shared_database_backup_includes_other_schemas_without_changing_default(self):
        for full in (False,True):
            with tempfile.TemporaryDirectory() as d, patch.object(deploy.subprocess,'run') as execute, patch.object(deploy,'run'), patch.object(deploy,'digest',return_value='digest'):
                (Path(d)/'database.dump').write_bytes(b'synthetic-dump-output')
                execute.return_value.stdout=b'public';execute.return_value.returncode=0
                deploy.backup(Path(d),{},all_schemas=full)
                dump=execute.call_args.args[0]
                self.assertEqual(not full,'--schema' in dump)
                self.assertIn('--format=custom',dump)

    def test_verification_uses_ordered_service_environment_overrides(self):
        files = '/etc/lmm-api-go/lmm-api-go.env (ignore_errors=yes)\n/etc/lmm-api/cluster.env (ignore_errors=no)'
        with patch.object(deploy, 'property_value', return_value=files), patch.object(deploy, 'run') as run:
            deploy.verify(Path('/private/work'), 'stage')
        environment_files = [arg for arg in run.call_args.args if arg.startswith('EnvironmentFile=')]
        self.assertEqual(['EnvironmentFile=-/etc/lmm-api-go/lmm-api-go.env', 'EnvironmentFile=/etc/lmm-api/cluster.env'], environment_files)
        self.assertEqual(('migrate', '--verify'), run.call_args.args[-2:])

    def test_environment_metadata_is_not_guessed(self):
        for files in ('', 'unexpected output', '/etc/../secret (ignore_errors=no)', '/etc/has space.env (ignore_errors=no)'):
            with self.subTest(files=files), patch.object(deploy, 'property_value', return_value=files):
                with self.assertRaises(RuntimeError):
                    deploy.service_environment_files()

    def test_backup_credentials_use_environment_and_preserve_search_path(self):
        environment = b'SQL_DSN=postgresql://app:pa%24ss@db.example:5433/service?sslmode=require&connect_timeout=10&application_name=backup\0PGOPTIONS=-c search_path=production\0'
        with patch.object(deploy, 'property_value', return_value='123'), patch.object(Path, 'read_bytes', return_value=environment), patch.object(deploy.shutil, 'which', return_value='/usr/bin/tool'):
            result = deploy.database_environment()
        self.assertEqual('db.example', result['PGHOST'])
        self.assertEqual('5433', result['PGPORT'])
        self.assertEqual('service', result['PGDATABASE'])
        self.assertEqual('pa$ss', result['PGPASSWORD'])
        self.assertEqual('require', result['PGSSLMODE'])
        self.assertEqual('10', result['PGCONNECT_TIMEOUT'])
        self.assertEqual('backup', result['PGAPPNAME'])
        self.assertEqual('-c search_path=production', result['PGOPTIONS'])

    def test_separate_database_does_not_get_an_incomplete_backup(self):
        environment = b'SQL_DSN=postgresql://db/service\0LOG_SQL_DSN=postgresql://db/logs\0'
        with patch.object(deploy, 'property_value', return_value='123'), patch.object(Path, 'read_bytes', return_value=environment):
            with self.assertRaisesRegex(RuntimeError, 'separate log database'):
                deploy.database_environment()

    def test_frontend_digest_detects_changes_and_rejects_symlinks(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'index.html').write_text('first')
            before = deploy.tree_digest(root)
            (root / 'index.html').write_text('second')
            self.assertNotEqual(before, deploy.tree_digest(root))
            (root / 'link').symlink_to(root / 'index.html')
            with self.assertRaises(RuntimeError):
                deploy.tree_digest(root)

    def test_forced_stop_never_authorizes_activation(self):
        with tempfile.TemporaryDirectory() as directory:
            values = {'InvocationID': 'fixture', 'MainPID': '0', 'Result': 'timeout'}
            with patch.object(deploy, 'property_value', side_effect=values.__getitem__), patch.object(deploy, 'run'):
                with self.assertRaisesRegex(RuntimeError, 'did not exit cleanly'):
                    deploy.stop(Path(directory))

    def test_refund_completion_is_required(self):
        with tempfile.TemporaryDirectory() as directory:
            values = {'InvocationID': 'fixture', 'MainPID': '0', 'Result': 'success', 'ExecMainStatus': '0'}
            with patch.object(deploy, 'property_value', side_effect=values.__getitem__):
                with patch.object(deploy, 'run', return_value='server exited'):
                    with self.assertRaisesRegex(RuntimeError, 'completion evidence'):
                        deploy.stop(Path(directory))
                report = 'refund_tasks execution_complete=true accepted=2 finished=2 active=0 failed=0\nserver exited'
                with patch.object(deploy, 'run', return_value=report):
                    deploy.stop(Path(directory))

    def test_install_does_not_follow_preexisting_pending_link(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / 'candidate'
            source.write_text('new')
            target = root / 'installed'
            target.write_text('old')
            (root / 'installed.deploy-next').symlink_to(target)
            with self.assertRaises(RuntimeError):
                deploy.install(source, target)
            self.assertEqual('old', target.read_text())

    def test_ordinary_health_rejects_ready_maintenance(self):
        body = {'success': True, 'ready': True, 'live': True, 'maintenance': True,
                'business_enabled': False, 'data': {'version': '0.2.83'}}
        with patch.object(deploy, 'health_json', return_value=body):
            with self.assertRaisesRegex(RuntimeError, 'business-ready'):
                deploy.healthy('0.2.83')

    def test_bound_prepare_health_rejects_changed_intent(self):
        handoff = {'transition_id': 'fixture', 'transition_intent_sha256': 'a' * 64,
                   'provider_sha256': 'b' * 64, 'prepare_config_sha256': 'c' * 64}
        binding = dict(handoff, format='lmm-credit-transition-prepare-v1', target_credits_per_usd=500000)
        body = {'success': True, 'ready': True, 'live': True, 'maintenance': True,
                'business_enabled': False, 'data': {'version': '0.2.83', 'credit_transition': binding}}
        with patch.object(deploy, 'health_json', return_value=body):
            deploy.healthy_prepare('0.2.83', handoff)
            binding['transition_intent_sha256'] = 'changed'
            with self.assertRaisesRegex(RuntimeError, 'frozen prepare binding'):
                deploy.healthy_prepare('0.2.83', handoff)

    def test_barrier_preserves_original_and_rejects_nested_owner(self):
        original = b'location @lmm_api_backend { proxy_pass http://127.0.0.1:3000; }\n'
        handoff = {'transition_id': 'fixture'}
        barrier = deploy.maintenance_barrier(handoff, original)
        self.assertTrue(barrier.endswith(original))
        self.assertIn(b'return 503', barrier)
        with self.assertRaises(RuntimeError):
            deploy.maintenance_barrier(handoff, barrier)


class CleanupTests(unittest.TestCase):
    """Local files and fake service probes only; no target service is accessed."""

    def setUp(self):
        self.stack = contextlib.ExitStack()
        self.addCleanup(self.stack.close)
        cache = Path.home() / '.cache'
        cache.mkdir(mode=0o700, exist_ok=True)
        self.base = Path(self.stack.enter_context(tempfile.TemporaryDirectory(dir=cache)))
        self.uid = os.getuid()
        self.now = time.time()
        self.stack.enter_context(patch.object(deploy.time, 'time', return_value=self.now))
        paths = {'ROOT': self.base / 'work', 'BINARY': self.base / 'bin' / 'lmm-api-go',
                 'ENTRY': self.base / 'bin' / 'lmm-api', 'FRONTEND': self.base / 'web',
                 'ENVIRONMENT': self.base / 'service.env', 'NGINX_LOCATIONS': self.base / 'locations'}
        for name, value in paths.items():
            self.stack.enter_context(patch.object(deploy, name, value))
        deploy.ROOT.mkdir(mode=0o700)
        deploy.BINARY.parent.mkdir(mode=0o700)
        self.write(deploy.BINARY, b'canonical bridge', 0o755)
        deploy.ENTRY.symlink_to('lmm-api-go')
        self.write(deploy.ENVIRONMENT, b'SQL_DSN=postgresql://fixture/database\n')
        self.original_ingress = b'location @lmm_api_backend { proxy_pass http://127.0.0.1:3000; }\n'
        self.write(deploy.NGINX_LOCATIONS, self.original_ingress)
        frontend = deploy.FRONTEND / 'releases' / 'frozen'
        frontend.mkdir(mode=0o700, parents=True)
        self.write(frontend / 'index.html', b'frozen web')
        (deploy.FRONTEND / 'current').symlink_to('releases/frozen')
        self.binding = {'transition_id': 'fixture', 'transition_intent_sha256': 'a' * 64,
                        'provider_sha256': deploy.digest(deploy.BINARY)}
        self.database = {'database': 'fixture', 'schema': 'public', 'system_identifier': '123456789',
                         'database_oid': 42, 'server_version_num': 170000, 'database_user': 'fixture'}
        prepared = self.write(self.base / 'prepare.json', json.dumps(dict(self.binding, database=self.database)).encode())
        token = self.write(self.base / 'probe-token', b'fixture-token')
        self.binding.update(prepare_config_path=str(prepared), prepare_config_sha256=deploy.digest(prepared),
                            archived_environment_sha256=deploy.digest(deploy.ENVIRONMENT),
                            probe_token_path=str(token), probe_token_sha256=deploy.digest(token),
                            public_base_url='https://fixture.invalid', guardian_socket=str(self.base / 'guardian.sock'),
                            deployment_tool='systemd', format='lmm-credit-maintenance-handoff-v1')
        self.handoffs = {}
        for release, stage in (('current', 'post'), ('bridge', 'prebridge')):
            handoff = dict(self.binding, stage=stage)
            path = self.write(self.base / (release + '-handoff.json'), json.dumps(handoff).encode())
            self.handoffs[release] = {'path': str(path), 'sha256': deploy.digest(path)}
        self.current = self.workspace('current', 'CONFIRMED')
        self.bridge = self.workspace('bridge', 'FROZEN')
        self.old = self.workspace('old', 'CONFIRMED')
        for release, work in (('current', self.current), ('bridge', self.bridge)):
            state = deploy.read_state(work)
            state.update(maintenance_handoff=self.handoffs[release], maintenance_confirmation=True,
                         maintenance_admission_reopened=release == 'current',
                         previous_frontend='frozen', previous_version='bridge-v1',
                         previous_sha256=self.binding['provider_sha256'],
                         ingress_original_sha256=deploy.digest(work / 'previous-nginx-locations'), **self.binding)
            self.state(work, state)
        self.archive = self.write(self.base / 'financial.dump', b'PGDMP' + b'x' * (2 * 1024 * 1024))
        self.financial_receipt = {'format': 'lmm-credit-financial-backup-v1',
                                 **{key: self.binding[key] for key in ('transition_id', 'transition_intent_sha256', 'provider_sha256')},
                                 'source_sha': 'd' * 40, 'frozen_guardian_bindings_sha256': 'e' * 64,
                                 'target': {key: self.database[key] for key in ('database', 'schema', 'system_identifier', 'database_oid')},
                                 'full_database': True, 'preserve_ownership': True, 'archive_format': 'custom',
                                 'backup_sha256': deploy.digest(self.archive), 'size_bytes': self.archive.stat().st_size}
        self.financial_receipt['target'].update(database_oid=str(self.database['database_oid']), schema_oid='2200')
        receipt = self.write(self.base / 'financial-receipt.json', json.dumps(self.financial_receipt).encode())
        self.args = SimpleNamespace(release='current', superseded_by='current', retain_rollback='bridge',
                                    financial_backup=self.archive, financial_backup_sha256=deploy.digest(self.archive),
                                    financial_backup_receipt=receipt, financial_backup_receipt_sha256=deploy.digest(receipt),
                                    older_than=86400, execute=False)
        self.real_cleanup_path = deploy.cleanup_path
        self.real_inventory = deploy.cleanup_payload_inventory
        self.real_archive = deploy.verify_financial_archive
        self.real_bound = deploy.guardian.bound_file
        self.real_handoff = deploy.guardian.handoff
        self.real_proc = deploy.cleanup_process_references
        self.real_guardian = deploy.verify_cleanup_guardian
        self.stack.enter_context(patch.object(deploy, 'cleanup_path', side_effect=lambda path, private_file=False, uid=0: self.real_cleanup_path(path, private_file, self.uid)))
        self.stack.enter_context(patch.object(deploy, 'cleanup_payload_inventory', side_effect=lambda work: self.real_inventory(work, self.uid)))
        self.stack.enter_context(patch.object(deploy, 'verify_financial_archive', side_effect=lambda path, expected: self.real_archive(path, expected, self.uid)))
        self.stack.enter_context(patch.object(deploy.guardian, 'bound_file', side_effect=lambda path, expected, uid=0: self.real_bound(path, expected, self.uid)))
        self.stack.enter_context(patch.object(deploy.guardian, 'handoff', side_effect=lambda path, expected, uid=0: self.real_handoff(path, expected, self.uid)))
        self.probes = self.stack.enter_context(patch.object(deploy, 'healthy'))
        self.stack.enter_context(patch.object(deploy, 'health_json', side_effect=lambda route, base='http://127.0.0.1:3000', token=None: {'success': True, 'live': True, 'ready': True, 'data': [] if route == '/v1/models' else {'version': 'bridge-v1'}}))
        self.stack.enter_context(patch.object(deploy, 'cleanup_process_references', return_value=[]))
        self.guardian = self.stack.enter_context(patch.object(deploy, 'verify_cleanup_guardian'))
        self.stack.enter_context(patch.object(deploy, 'verify_stopped_maintenance', return_value={'sealed': 'environment'}))
        self.install_chain()

    def write(self, path, body, mode=0o600):
        path.write_bytes(body)
        path.chmod(mode)
        return path

    def state(self, work, state):
        self.write(work / 'state.json', json.dumps(state).encode())

    def workspace(self, name, phase):
        work = deploy.ROOT / name
        work.mkdir(mode=0o700)
        self.write(work / 'lmm-api-go', deploy.BINARY.read_bytes(), 0o755)
        self.write(work / 'previous-binary', deploy.BINARY.read_bytes(), 0o755)
        (work / 'lmm-api').symlink_to('lmm-api-go')
        shutil.copytree(deploy.FRONTEND / 'releases' / 'frozen', work / 'frontend')
        for directory in ('cache', 'tmp'):
            (work / directory).mkdir(mode=0o700)
            self.write(work / directory / 'payload', b'removable')
        self.write(work / 'previous.env', deploy.ENVIRONMENT.read_bytes())
        self.write(work / 'previous-nginx-locations', self.original_ingress)
        self.write(work / 'database.dump', b'historical fixture retained verbatim')
        self.write(work / 'private.log', b'historical evidence')
        self.state(work, {'release': name, 'phase': phase, 'version': 'bridge-v1',
                          'sha256': deploy.digest(work / 'lmm-api-go'),
                          'frontend_sha256': deploy.tree_digest(work / 'frontend'),
                          'terminal_at': self.now - 90000, 'ready_at': self.now - 90121})
        return work

    def maintenance(self):
        return deploy.guardian.handoff(**dict(path=self.handoffs['current']['path'], expected=self.handoffs['current']['sha256']))

    def install_chain(self, include_capture=False):
        bridge_state = deploy.read_state(self.bridge)
        bridge_handoff = json.loads(Path(self.handoffs['bridge']['path']).read_text())
        if include_capture:
            capture = self.workspace('capture', 'FROZEN')
            capture_handoff = dict(self.binding, stage='prebridge')
            path = self.write(self.base / 'capture-handoff.json', json.dumps(capture_handoff).encode())
            capture_receipt = {'format': 'lmm-credit-maintenance-capture-v1', 'phase': 'FROZEN',
                               'transition_id': self.binding['transition_id'], 'transition_intent_sha256': self.binding['transition_intent_sha256'],
                               'provider_sha256': 'f' * 64, 'was_maintenance_confirmed': False}
            receipt_path = self.write(capture / 'maintenance-capture.FROZEN.json', json.dumps(capture_receipt).encode())
            capture_state = deploy.read_state(capture)
            capture_state.update(maintenance_handoff={'path': str(path), 'sha256': deploy.digest(path)},
                                 capture_receipt_path=str(receipt_path), capture_receipt_sha256=deploy.digest(receipt_path))
            self.state(capture, capture_state)
            bridge_handoff.update(previous_deployment_id='capture', capture_receipt_path=str(receipt_path),
                                  capture_receipt_sha256=deploy.digest(receipt_path), stopped_writer={'pid': 100})
            bridge_path = Path(self.handoffs['bridge']['path'])
            self.write(bridge_path, json.dumps(bridge_handoff).encode())
            self.handoffs['bridge']['sha256'] = deploy.digest(bridge_path)
            bridge_state['maintenance_handoff'] = self.handoffs['bridge']
            self.state(self.bridge, bridge_state)
            deploy.write_maintenance_transfer(capture_state, bridge_state, deploy.guardian.handoff(bridge_path, deploy.digest(bridge_path)))
        receipt = {'format': 'lmm-credit-maintenance-capture-v1', 'phase': 'FROZEN',
                   'transition_id': self.binding['transition_id'], 'transition_intent_sha256': self.binding['transition_intent_sha256'],
                   'provider_sha256': self.binding['provider_sha256'], 'was_maintenance_confirmed': True}
        receipt_path = self.write(self.bridge / 'maintenance-capture.FROZEN.json', json.dumps(receipt).encode())
        bridge_state.update(capture_receipt_path=str(receipt_path), capture_receipt_sha256=deploy.digest(receipt_path))
        self.state(self.bridge, bridge_state)
        current_handoff = dict(self.binding, stage='post', previous_deployment_id='bridge',
                               capture_receipt_path=str(receipt_path), capture_receipt_sha256=deploy.digest(receipt_path),
                               stopped_writer={'pid': 200})
        current_path = Path(self.handoffs['current']['path'])
        self.write(current_path, json.dumps(current_handoff).encode())
        self.handoffs['current']['sha256'] = deploy.digest(current_path)
        current_state = deploy.read_state(self.current)
        current_state['maintenance_handoff'] = self.handoffs['current']
        self.state(self.current, current_state)
        deploy.write_maintenance_transfer(bridge_state, current_state, self.maintenance())

    def snapshot(self, root):
        return {str(path.relative_to(root)): ('link', os.readlink(path)) if path.is_symlink() else ('file', path.read_bytes()) if path.is_file() else ('directory',)
                for path in root.rglob('*')}

    def seal_financial_receipt(self, receipt):
        self.write(self.args.financial_backup_receipt, json.dumps(receipt).encode())
        self.args.financial_backup_receipt_sha256 = deploy.digest(self.args.financial_backup_receipt)

    def test_dry_run_retains_every_byte_and_complete_protected_workspaces(self):
        before = self.snapshot(self.base)
        result = deploy.cleanup(self.current, self.args, self.maintenance(), {'fixture': True})
        self.assertTrue(result['dry_run'])
        self.assertEqual(['old'], [entry['release'] for entry in result['candidates']])
        self.assertEqual([], result['blocked_by'])
        self.assertEqual(before, self.snapshot(self.base))
        self.probes.assert_called_once()

    def test_execute_removes_only_whitelist_and_retains_evidence_and_financial_archive(self):
        protected = {name: self.snapshot(deploy.ROOT / name) for name in ('current', 'bridge')}
        evidence = {name: (self.old / name).read_bytes() for name in ('state.json', 'previous.env', 'previous-nginx-locations', 'database.dump', 'private.log')}
        archive = self.archive.read_bytes()
        self.args.execute = True
        result = deploy.cleanup(self.current, self.args, self.maintenance(), {'fixture': True})
        self.assertFalse(result['dry_run'])
        self.assertGreater(result['deleted_payload_bytes'], 0)
        for name in deploy.CLEANUP_PAYLOADS:
            self.assertFalse((self.old / name).exists() or (self.old / name).is_symlink())
        for name, content in evidence.items():
            self.assertEqual(content, (self.old / name).read_bytes())
        for name, content in protected.items():
            self.assertEqual(content, self.snapshot(deploy.ROOT / name))
        self.assertEqual(archive, self.archive.read_bytes())

    def test_missing_historical_database_backup_is_not_fabricated(self):
        (self.old / 'database.dump').unlink()
        self.args.execute = True
        deploy.cleanup(self.current, self.args, self.maintenance(), {'fixture': True})
        self.assertFalse((self.old / 'database.dump').exists())
        self.assertTrue((self.old / 'previous.env').exists())

    def test_pending_or_unknown_state_blocks_all_execute(self):
        for phase in ('STAGED', 'AWAITING_CONFIRMATION', 'FROZEN', 'UNKNOWN'):
            with self.subTest(phase=phase):
                extra = deploy.ROOT / 'other'
                if extra.exists():
                    shutil.rmtree(extra)
                extra = self.workspace('other', phase)
                self.args.execute = False
                result = deploy.cleanup(self.current, self.args, self.maintenance(), {'fixture': True})
                self.assertIn('other', result['blocked_by'])
                self.args.execute = True
                with self.assertRaisesRegex(RuntimeError, 'other owner states'):
                    deploy.cleanup(self.current, self.args, self.maintenance(), {'fixture': True})
                self.assertTrue((self.old / 'lmm-api-go').exists())

    def test_cross_workspace_payload_reference_protects_historical_payload(self):
        self.write(self.current / 'retained-reference.json', json.dumps({'payload': str(self.old / 'frontend' / 'index.html')}).encode())
        result = deploy.cleanup(self.current, self.args, self.maintenance(), {'fixture': True})
        self.assertEqual([], result['candidates'])
        self.assertTrue(any(item['release'] == 'old' and 'another workspace' in item['reason'] for item in result['protected']))

    def test_no_terminal_time_and_new_terminal_are_protected(self):
        state = deploy.read_state(self.old)
        state.pop('terminal_at')
        state.pop('ready_at')
        self.state(self.old, state)
        os.utime(self.old / 'state.json', (self.now - 90000, self.now - 90000))
        self.assertEqual([], deploy.cleanup_history(self.args, self.now)['candidates'])
        state.update(terminal_at=self.now - 100)
        self.state(self.old, state)
        self.assertEqual([], deploy.cleanup_history(self.args, self.now)['candidates'])

    def test_legacy_ready_evidence_and_final_write_age_are_both_required(self):
        state = deploy.read_state(self.old)
        state.pop('terminal_at')
        self.state(self.old, state)
        os.utime(self.old / 'state.json', (self.now - 90000, self.now - 90000))
        self.assertEqual(['old'], [item['release'] for item in deploy.cleanup_history(self.args, self.now)['candidates']])
        os.utime(self.old / 'state.json', (self.now - 1, self.now - 1))
        self.assertEqual([], deploy.cleanup_history(self.args, self.now)['candidates'])

    def test_changed_protected_bridge_current_or_frontend_refuses_cleanup(self):
        for path in (self.bridge / 'lmm-api-go', self.current / 'previous-binary', deploy.BINARY, self.bridge / 'frontend' / 'index.html'):
            with self.subTest(path=path):
                original = path.read_bytes()
                path.write_bytes(b'changed')
                with self.assertRaises(RuntimeError):
                    deploy.cleanup(self.current, self.args, self.maintenance(), {'fixture': True})
                path.write_bytes(original)
        state = deploy.read_state(self.current)
        state['maintenance_admission_reopened'] = False
        self.state(self.current, state)
        with self.assertRaisesRegex(RuntimeError, 'reopened admission'):
            deploy.cleanup(self.current, self.args, self.maintenance(), {'fixture': True})

    def test_archive_streaming_permissions_links_magic_and_exact_digest(self):
        self.assertGreater(self.real_archive(self.archive, self.args.financial_backup_sha256, self.uid)['bytes'], 1024 * 1024)
        with self.assertRaisesRegex(RuntimeError, 'sealed PostgreSQL'):
            self.real_archive(self.archive, 'f' * 64, self.uid)
        self.archive.chmod(0o644)
        with self.assertRaises(RuntimeError):
            self.real_archive(self.archive, self.args.financial_backup_sha256, self.uid)
        self.archive.chmod(0o600)
        link = self.base / 'linked.dump'
        link.symlink_to(self.archive)
        with self.assertRaises(RuntimeError):
            self.real_archive(link, self.args.financial_backup_sha256, self.uid)
        link.unlink()
        os.link(self.archive, link)
        with self.assertRaises(RuntimeError):
            self.real_archive(self.archive, self.args.financial_backup_sha256, self.uid)
        link.unlink()
        invalid = self.write(self.base / 'invalid.dump', b'not PostgreSQL')
        with self.assertRaisesRegex(RuntimeError, 'sealed PostgreSQL'):
            self.real_archive(invalid, deploy.digest(invalid), self.uid)
        inside = self.write(self.current / 'financial.dump', b'PGDMPfixture')
        with self.assertRaisesRegex(RuntimeError, 'outside deployment'):
            self.real_archive(inside, deploy.digest(inside), self.uid)
        with self.assertRaises(RuntimeError):
            self.real_archive(self.archive, self.args.financial_backup_sha256, self.uid + 1)

    def test_financial_receipt_rejects_wrong_transition_database_partial_archive_and_types(self):
        archive = {'path': str(self.archive), 'sha256': self.args.financial_backup_sha256, 'bytes': self.archive.stat().st_size}
        for key, value in (('transition_intent_sha256', 'f' * 64), ('provider_sha256', 'f' * 64),
                           ('full_database', False), ('full_database', 1), ('preserve_ownership', 1),
                           ('archive_format', 'plain'), ('source_sha', 'wrong'), ('frozen_guardian_bindings_sha256', 'wrong'),
                           ('backup_sha256', 'f' * 64), ('size_bytes', 1), ('size_bytes', True)):
            with self.subTest(key=key, value=value):
                changed = dict(self.financial_receipt, **{key: value})
                self.write(self.args.financial_backup_receipt, json.dumps(changed).encode())
                self.args.financial_backup_receipt_sha256 = deploy.digest(self.args.financial_backup_receipt)
                with self.assertRaises(RuntimeError):
                    deploy.verify_financial_backup_receipt(self.args, self.maintenance(), archive)
        for key, value in (('database', 'wrong'), ('schema', 'wrong'), ('system_identifier', 'wrong'), ('database_oid', 99), ('schema_oid', True), ('schema_oid', 0)):
            with self.subTest(target=key):
                changed = dict(self.financial_receipt, target=dict(self.financial_receipt['target'], **{key: value}))
                self.write(self.args.financial_backup_receipt, json.dumps(changed).encode())
                self.args.financial_backup_receipt_sha256 = deploy.digest(self.args.financial_backup_receipt)
                with self.assertRaises(RuntimeError):
                    deploy.verify_financial_backup_receipt(self.args, self.maintenance(), archive)

    def test_financial_receipt_from_real_runner_finish_backup_accepts_text_oids(self):
        spec = importlib.util.spec_from_file_location('financial_runner', Path(__file__).with_name('run-credit-financial-maintenance.py'))
        runner = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(runner)
        plan = {'transition_id': self.binding['transition_id'],
                'transition_intent_sha256': self.binding['transition_intent_sha256'],
                'provider': {'sha256': self.binding['provider_sha256']},
                'source_sha': '7af4bf9e56055a9a283b6545433e271a84b60026',
                'database': dict(self.database, owner_node='fixture'),
                'nodes': [{'name': 'fixture'}], 'backup_commands': {'offhost_copy': {}, 'offhost_verify': {}}}
        controller = runner.Controller(plan, 'f' * 64, self.base)
        controller.state.update(backup={'path': str(self.archive), 'sha256': deploy.digest(self.archive)},
                                guardian_lock_bindings={'fixture': {'pid': 123, 'invocation_id': 'a' * 32}})
        target = dict(self.financial_receipt['target'])
        proof = {'backup_sha256': deploy.digest(self.archive), 'size_bytes': self.archive.stat().st_size}

        def publish(name, receipt):
            self.assertEqual('full-financial-backup.receipt.json', name)
            self.seal_financial_receipt(receipt)
            return {'path': str(self.args.financial_backup_receipt), 'sha256': self.args.financial_backup_receipt_sha256}

        # Execute the real receipt producer, with all commands and publication
        # replaced by local fixtures. No SSH, database, or service is accessed.
        with patch.object(controller, 'frozen_gates'), patch.object(controller, 'execute', side_effect=[b'', runner.encode(proof)]), patch.object(controller, 'sql_bytes', return_value=runner.encode(target)) as sql, patch.object(controller, 'publish_receipt', side_effect=publish), patch.object(controller, 'persist'):
            controller.finish_backup()
        self.assertIn(b'oid::text', sql.call_args.args[1])
        archive = deploy.verify_financial_archive(self.archive, self.args.financial_backup_sha256)
        result = deploy.verify_financial_backup_receipt(self.args, self.maintenance(), archive)
        self.assertEqual(plan['source_sha'], result['source_sha'])
        self.assertEqual(target, result['target'])
        self.assertEqual('42', result['target']['database_oid'])
        self.assertEqual('2200', result['target']['schema_oid'])
        self.assertEqual(runner.digest(runner.encode(controller.state['guardian_lock_bindings'])), result['frozen_guardian_bindings_sha256'])

    def test_financial_receipt_source_requires_exact_lowercase_git_commit(self):
        archive = deploy.verify_financial_archive(self.archive, self.args.financial_backup_sha256)
        for source in ('d' * 39, 'd' * 41, 'd' * 64, 'D' * 40, 'd' * 40 + '\n', '', None, 123, True):
            with self.subTest(source=source):
                self.seal_financial_receipt(dict(self.financial_receipt, source_sha=source))
                with self.assertRaisesRegex(RuntimeError, 'sealed source or guardian binding'):
                    deploy.verify_financial_backup_receipt(self.args, self.maintenance(), archive)
        for guardian in ('e' * 40, 'E' * 64, None, 123, True):
            with self.subTest(guardian=guardian):
                self.seal_financial_receipt(dict(self.financial_receipt, frozen_guardian_bindings_sha256=guardian))
                with self.assertRaisesRegex(RuntimeError, 'sealed source or guardian binding'):
                    deploy.verify_financial_backup_receipt(self.args, self.maintenance(), archive)

    def test_financial_receipt_oids_require_canonical_positive_uint32_text(self):
        archive = deploy.verify_financial_archive(self.archive, self.args.financial_backup_sha256)
        invalid = (None, True, 42, 42.0, '', '0', '-1', '+42', '042', ' 42', '42 ', '42\n', '4.2e1', '４２', '4294967296', '99999999999')
        for key in ('database_oid', 'schema_oid'):
            for value in invalid:
                with self.subTest(oid=key, value=value):
                    target = dict(self.financial_receipt['target'], **{key: value})
                    self.seal_financial_receipt(dict(self.financial_receipt, target=target))
                    with self.assertRaises(RuntimeError):
                        deploy.verify_financial_backup_receipt(self.args, self.maintenance(), archive)
        self.seal_financial_receipt(dict(self.financial_receipt, target=dict(self.financial_receipt['target'], database_oid='43')))
        with self.assertRaisesRegex(RuntimeError, 'different PostgreSQL database or schema'):
            deploy.verify_financial_backup_receipt(self.args, self.maintenance(), archive)

    def test_financial_receipt_oid_comparison_rejects_noninteger_prepare_oid(self):
        archive = deploy.verify_financial_archive(self.archive, self.args.financial_backup_sha256)
        maintenance = self.maintenance()
        prepared_path = Path(maintenance['prepare_config_path'])
        prepared = json.loads(prepared_path.read_text())
        for value in ('42', 42.0, 42.5, True, None, 0, -1, 4294967296):
            with self.subTest(prepare_oid=value):
                prepared['database']['database_oid'] = value
                self.write(prepared_path, json.dumps(prepared).encode())
                maintenance['prepare_config_sha256'] = deploy.digest(prepared_path)
                with self.assertRaisesRegex(RuntimeError, 'different PostgreSQL database or schema'):
                    deploy.verify_financial_backup_receipt(self.args, maintenance, archive)

    def test_financial_receipt_oid_uint32_boundaries_match_numeric_prepare(self):
        archive = deploy.verify_financial_archive(self.archive, self.args.financial_backup_sha256)
        maintenance = self.maintenance()
        prepared_path = Path(maintenance['prepare_config_path'])
        prepared = json.loads(prepared_path.read_text())
        for oid in (1, 4294967295):
            with self.subTest(oid=oid):
                prepared['database']['database_oid'] = oid
                self.write(prepared_path, json.dumps(prepared).encode())
                maintenance['prepare_config_sha256'] = deploy.digest(prepared_path)
                target = dict(self.financial_receipt['target'], database_oid=str(oid), schema_oid=str(oid))
                self.seal_financial_receipt(dict(self.financial_receipt, target=target))
                result = deploy.verify_financial_backup_receipt(self.args, maintenance, archive)
                self.assertEqual(target, result['target'])

    def test_cleanup_lock_adopts_all_three_descriptors_and_closes_without_unlocking(self):
        descriptors, paths = [], []
        for name in ('native', 'systemd', 'frontend'):
            path = self.write(self.base / (name + '-adopt.lock'), b'')
            descriptor = os.open(path, os.O_RDONLY)
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
            paths.append(path)
            descriptors.append(descriptor)
        lease = SimpleNamespace(close=lambda: None)
        with patch.object(deploy.guardian, 'adopt_all', return_value=(descriptors, lease, {'locks': []})) as adopt, patch.object(deploy.guardian, 'adopt', side_effect=AssertionError('cleanup may not adopt only one descriptor')):
            with deploy.deployment_lock(self.maintenance(), 'a' * 64, all_locks=True):
                for path in paths:
                    with path.open('rb') as fresh:
                        with self.assertRaises(BlockingIOError):
                            fcntl.flock(fresh, fcntl.LOCK_EX | fcntl.LOCK_NB)
            for path in paths:
                with path.open('rb') as fresh:
                    fcntl.flock(fresh, fcntl.LOCK_EX | fcntl.LOCK_NB)
        adopt.assert_called_once()

    def test_actual_scm_three_lock_cleanup_lease_survives_guardian_death(self):
        paths = {name: str(self.base / (name + '-scm.lock')) for name in ('native', 'systemd', 'frontend')}
        value = self.maintenance()
        binding = self.handoffs['current']
        actual_adopt_all = deploy.guardian.adopt_all
        process = multiprocessing.get_context('fork').Process(target=deploy.guardian.serve,
                                                              args=(binding['path'], binding['sha256'], self.uid, paths))
        process.start()
        try:
            deadline = time.monotonic() + 5
            while not Path(value['guardian_socket']).exists():
                if time.monotonic() > deadline or not process.is_alive():
                    self.fail('private fixture guardian did not start')
                time.sleep(0.01)
            with patch.object(deploy.guardian, 'adopt_all', side_effect=lambda maintenance, sha: actual_adopt_all(maintenance, sha, self.uid, paths)), patch.object(deploy.guardian, 'LOCKS', paths):
                with deploy.deployment_lock(value, binding['sha256'], all_locks=True) as receipt:
                    self.real_guardian(receipt, self.uid)
                    process.terminate()
                    process.join(timeout=5)
                    self.assertFalse(process.is_alive())
                    # All three same OFDs, not only the systemd descriptor,
                    # remain exclusively held by this actual SCM receiver.
                    self.real_guardian(receipt, self.uid)
                for path in paths.values():
                    with open(path, 'rb') as fresh:
                        fcntl.flock(fresh, fcntl.LOCK_EX | fcntl.LOCK_NB)
        finally:
            if process.is_alive():
                process.terminate()
                process.join(timeout=5)

    def test_formal_three_owner_chain_retains_frozen_capture_while_cleaning_terminal_history(self):
        self.install_chain(include_capture=True)
        self.assertEqual({'bridge', 'capture'}, deploy.maintenance_ancestor_chain('current', self.maintenance()))
        before = self.snapshot(deploy.ROOT / 'capture')
        self.args.execute = True
        result = deploy.cleanup(self.current, self.args, self.maintenance(), {'fixture': True})
        self.assertEqual([], result['blocked_by'])
        self.assertEqual(['old'], [item['release'] for item in result['candidates']])
        self.assertTrue(any(item['release'] == 'capture' and 'formally transferred' in item['reason'] for item in result['protected']))
        self.assertEqual(before, self.snapshot(deploy.ROOT / 'capture'))

    def test_missing_or_changed_formal_transfer_record_blocks_cleanup_without_deleting(self):
        self.install_chain(include_capture=True)
        record = deploy.ROOT / 'capture' / 'maintenance-transfer.bridge.json'
        original = record.read_bytes()
        record.unlink()
        self.args.execute = True
        with self.assertRaises((RuntimeError, OSError)):
            deploy.cleanup(self.current, self.args, self.maintenance(), {'fixture': True})
        self.assertTrue((self.old / 'lmm-api-go').exists())
        changed = json.loads(original)
        changed['next_deployment_id'] = 'wrong'
        self.write(record, json.dumps(changed).encode())
        with self.assertRaises(RuntimeError):
            deploy.cleanup(self.current, self.args, self.maintenance(), {'fixture': True})
        self.assertTrue((self.old / 'lmm-api-go').exists())

    def test_post_apply_accepts_only_formally_transferred_frozen_ancestors(self):
        self.install_chain(include_capture=True)
        state = deploy.read_state(self.current)
        state.update(phase='STAGED', migrate=False, backup_exclude_tables=[], maintenance_admission_reopened=False)
        self.state(self.current, state)
        with patch.object(deploy.os, 'geteuid', return_value=0), patch.object(deploy, 'check_layout'), patch.object(deploy, 'check_tools'), patch.object(deploy, 'deployment_lock', side_effect=self.fake_lease), patch.object(deploy, 'verify_maintenance_database'), patch.object(deploy, 'run', return_value='bridge-v1'), patch.object(deploy, 'configure_maintenance_service'), patch.object(deploy, 'close_maintenance_admission'), patch.object(deploy, 'maintenance_admission'):
            result = self.cli('apply', '--release', 'current', '--confirm', 'api.lmm.best', '--json')
        self.assertEqual('AWAITING_CONFIRMATION', result['phase'])
        self.assertEqual('FROZEN', deploy.read_state(deploy.ROOT / 'capture')['phase'])
        self.assertEqual({'bridge', 'capture'}, deploy.maintenance_ancestor_chain('current', self.maintenance()))

    def test_payload_symlinks_and_hardlinks_are_protected(self):
        shutil.rmtree(self.old / 'tmp')
        (self.old / 'tmp').symlink_to(self.base)
        self.assertEqual([], deploy.cleanup_history(self.args, self.now)['candidates'])
        (self.old / 'tmp').unlink()
        shared = self.base / 'shared-provider'
        os.link(self.old / 'lmm-api-go', shared)
        self.assertEqual([], deploy.cleanup_history(self.args, self.now)['candidates'])

    def test_live_process_cwd_and_fd_and_maps_are_detected(self):
        controlled = self.base / 'proc'
        controlled.mkdir()
        process = subprocess.Popen([sys.executable, '-c', 'import sys,time; f=open(sys.argv[1],"rb"); print("ready",flush=True); time.sleep(30)', str(self.old / 'lmm-api-go')], cwd=self.old / 'cache', stdout=subprocess.PIPE, text=True)
        try:
            self.assertEqual('ready', process.stdout.readline().strip())
            (controlled / str(process.pid)).symlink_to(Path('/proc') / str(process.pid))
            found = self.real_proc(self.old, controlled)
            self.assertTrue(any(item['kind'] == 'cwd' for item in found))
            self.assertTrue(any(item['kind'].startswith('fd/') for item in found))
        finally:
            process.terminate()
            process.wait(timeout=5)
            process.stdout.close()
        shutil.rmtree(controlled)
        fake = controlled / '123'
        (fake / 'fd').mkdir(parents=True)
        (fake / 'exe').symlink_to('/usr/bin/fixture')
        (fake / 'cwd').symlink_to('/')
        (fake / 'maps').write_text('1000-2000 r-xp 00000000 00:01 1 ' + str(self.old / 'previous-binary') + '\n')
        self.assertTrue(any(item['kind'] == 'maps' for item in self.real_proc(self.old, controlled)))

    def test_guardian_three_real_locks_are_checked_without_unlocking(self):
        paths, receipt = {}, {'locks': []}
        with contextlib.ExitStack() as stack:
            for name in ('native', 'systemd', 'frontend'):
                path = self.write(self.base / (name + '.lock'), b'')
                owner = stack.enter_context(path.open('rb'))
                fcntl.flock(owner, fcntl.LOCK_EX | fcntl.LOCK_NB)
                info = path.stat()
                paths[name] = str(path)
                receipt['locks'].append({'path': str(path), 'device': info.st_dev, 'inode': info.st_ino})
            with patch.object(deploy.guardian, 'LOCKS', paths):
                self.real_guardian(receipt, self.uid)
                for path in paths.values():
                    with open(path, 'rb') as other:
                        with self.assertRaises(BlockingIOError):
                            fcntl.flock(other, fcntl.LOCK_EX | fcntl.LOCK_NB)
                with self.assertRaises(RuntimeError):
                    self.real_guardian({'locks': receipt['locks'][:2]}, self.uid)
        with patch.object(deploy.guardian, 'LOCKS', paths):
            with self.assertRaisesRegex(RuntimeError, 'independently acquirable'):
                self.real_guardian(receipt, self.uid)

    def test_terminal_save_adds_timestamp_once(self):
        state = deploy.read_state(self.old)
        state.pop('terminal_at')
        deploy.save(self.old, state)
        self.assertEqual(self.now, state['terminal_at'])
        with patch.object(deploy.time, 'time', return_value=self.now + 100):
            deploy.save(self.old, state)
        self.assertEqual(self.now, deploy.read_state(self.old)['terminal_at'])

    def cli(self, *argv):
        with contextlib.redirect_stdout(io.StringIO()) as output, contextlib.redirect_stderr(io.StringIO()) as errors:
            result = deploy.main(list(argv))
        self.assertEqual(0, result, errors.getvalue() + output.getvalue())
        return json.loads(output.getvalue())

    @contextlib.contextmanager
    def fake_lease(self, *args, **kwargs):
        yield {'fixture': True}

    def test_cleanup_dispatches_before_staged_payload_checks_and_never_changes_phase(self):
        before = (self.current / 'state.json').read_bytes()
        with patch.object(deploy.os, 'geteuid', return_value=0), patch.object(deploy, 'check_layout'), patch.object(deploy, 'deployment_lock', side_effect=self.fake_lease), patch.object(deploy, 'stop') as stop, patch.object(deploy, 'install') as install:
            result = self.cli('cleanup', '--release', 'current', '--superseded-by', 'current', '--retain-rollback', 'bridge', '--financial-backup', str(self.archive), '--financial-backup-sha256', self.args.financial_backup_sha256, '--financial-backup-receipt', str(self.args.financial_backup_receipt), '--financial-backup-receipt-sha256', self.args.financial_backup_receipt_sha256, '--older-than', '86400', '--confirm', 'api.lmm.best', '--json')
        self.assertEqual('lmm-credit-maintenance-cleanup-v1', result['format'])
        self.assertEqual('CONFIRMED', result['phase'])
        self.assertEqual(before, (self.current / 'state.json').read_bytes())
        stop.assert_not_called()
        install.assert_not_called()

    def test_stopped_maintenance_stage_uses_archived_environment_not_running_service(self):
        handoff = json.loads(Path(self.handoffs['current']['path']).read_text())
        handoff['stopped_writer'] = {'pid': 123}
        path = self.write(self.base / 'stopped-handoff.json', json.dumps(handoff).encode())
        source = self.write(self.base / 'source-provider', deploy.BINARY.read_bytes(), 0o755)
        with patch.object(deploy.os, 'geteuid', return_value=0), patch.object(deploy, 'check_layout'), patch.object(deploy, 'check_tools'), patch.object(deploy, 'deployment_lock', side_effect=self.fake_lease), patch.object(deploy, 'run', return_value='bridge-v1'), patch.object(deploy, 'verify_stopped_maintenance', return_value={'sealed': 'environment'}) as stopped, patch.object(deploy, 'database_environment', side_effect=AssertionError('running service read is forbidden')) as running, patch.object(deploy, 'backup') as backup:
            self.cli('stage', '--release', 'stopped-stage', '--binary', str(source), '--frontend', str(self.current / 'frontend'), '--maintenance-handoff', str(path), '--maintenance-handoff-sha256', deploy.digest(path), '--migrate', '--json')
        stopped.assert_called_once()
        running.assert_not_called()
        self.assertEqual({'sealed': 'environment'}, backup.call_args.args[1])
        self.assertTrue(backup.call_args.kwargs['schema_only'])

    def test_maintenance_confirm_rechecks_without_install_or_reopen_and_normal_repeat_refuses(self):
        state = deploy.read_state(self.current)
        state.update(phase='MAINTENANCE_CONFIRMED', ready_at=self.now - 121)
        self.state(self.current, state)
        with patch.object(deploy.os, 'geteuid', return_value=0), patch.object(deploy, 'check_layout'), patch.object(deploy, 'deployment_lock', side_effect=self.fake_lease), patch.object(deploy, 'maintenance_admission') as admission, patch.object(deploy, 'install') as install, patch.object(deploy, 'reopen_maintenance_admission') as reopen:
            result = self.cli('confirm', '--release', 'current', '--confirm', 'api.lmm.best', '--json')
        self.assertEqual('MAINTENANCE_CONFIRMED', result['phase'])
        self.assertEqual(state['ready_at'], result['ready_at'])
        self.assertTrue(result['maintenance_confirmation'])
        admission.assert_called_once()
        install.assert_not_called()
        reopen.assert_not_called()
        state.pop('maintenance_handoff')
        self.state(self.current, state)
        with patch.object(deploy.os, 'geteuid', return_value=0), patch.object(deploy, 'check_layout'), patch.object(deploy, 'deployment_lock', side_effect=self.fake_lease), contextlib.redirect_stdout(io.StringIO()) as output:
            result = deploy.main(['confirm', '--release', 'current', '--confirm', 'api.lmm.best', '--json'])
        self.assertEqual(1, result)
        self.assertIn('AWAITING_CONFIRMATION', output.getvalue())

    def test_post_rollback_reobserves_and_returns_maintenance_confirmed_with_admission_held(self):
        state = deploy.read_state(self.current)
        state.update(phase='MAINTENANCE_CONFIRMED', ready_at=self.now - 1000)
        self.state(self.current, state)
        with patch.object(deploy.os, 'geteuid', return_value=0), patch.object(deploy, 'check_layout'), patch.object(deploy, 'deployment_lock', side_effect=self.fake_lease), patch.object(deploy, 'property_value', return_value='0'), patch.object(deploy, 'run'), patch.object(deploy, 'configure_maintenance_service'), patch.object(deploy, 'maintenance_admission') as admission, patch.object(deploy, 'reopen_maintenance_admission') as reopen, patch.object(deploy.time, 'time', side_effect=[self.now, self.now + 1, self.now + 121]), patch.object(deploy.time, 'sleep') as sleep:
            result = self.cli('rollback', '--release', 'current', '--confirm', 'api.lmm.best', '--json')
        self.assertEqual('MAINTENANCE_CONFIRMED', result['phase'])
        self.assertTrue(result['maintenance_confirmation'])
        self.assertEqual(self.now, result['ready_at'])
        self.assertGreaterEqual(admission.call_count, 3)
        sleep.assert_called_once()
        reopen.assert_not_called()

    def status_workspace(self):
        work = self.workspace('staged', 'STAGED')
        for path in list(work.iterdir()):
            if path.name not in ('state.json', 'lmm-api-go', 'lmm-api', 'frontend'):
                if path.is_dir():
                    shutil.rmtree(path)
                else:
                    path.unlink()
        state = {'release': 'staged', 'phase': 'STAGED', 'version': 'bridge-v1',
                 'sha256': self.binding['provider_sha256'], 'frontend_sha256': deploy.tree_digest(work / 'frontend'),
                 'maintenance_handoff': self.handoffs['current'], 'maintenance_stage': 'post',
                 **{name: self.binding[name] for name in ('transition_id', 'transition_intent_sha256', 'provider_sha256', 'prepare_config_sha256')}}
        self.state(work, state)
        return work, state

    def test_staged_status_proves_not_dispatched_under_guardian_and_never_persists_phase(self):
        work, state = self.status_workspace()
        before = self.snapshot(self.base)
        with patch.object(deploy, 'deployment_lock', side_effect=self.fake_lease):
            result = deploy.maintenance_dispatch_status(work, state, self.uid)
        self.assertEqual('NOT_DISPATCHED', result['phase'])
        self.assertTrue(result['dispatch_verified_absent'])
        self.assertEqual(self.handoffs['current']['sha256'], result['maintenance_handoff_sha256'])
        self.assertEqual(before, self.snapshot(self.base))
        self.assertEqual('STAGED', deploy.read_state(work)['phase'])

    def test_staged_status_with_mutation_evidence_or_unavailable_guardian_remains_unproved(self):
        work, state = self.status_workspace()
        self.write(work / 'shutdown.log', b'dispatched writer evidence')
        with patch.object(deploy, 'deployment_lock', side_effect=self.fake_lease):
            result = deploy.maintenance_dispatch_status(work, state, self.uid)
        self.assertEqual('STAGED', result['phase'])
        self.assertFalse(result['dispatch_verified_absent'])
        self.assertIn('mutation evidence', result['dispatch_verification_error'])
        (work / 'shutdown.log').unlink()
        with patch.object(deploy, 'deployment_lock', side_effect=RuntimeError('guardian unavailable')):
            result = deploy.maintenance_dispatch_status(work, state, self.uid)
        self.assertEqual('STAGED', result['phase'])
        self.assertFalse(result['dispatch_verified_absent'])
        self.assertIn('guardian unavailable', result['dispatch_verification_error'])

    def test_staged_status_checks_stopped_receipt_and_terminal_status_does_not_need_guardian(self):
        work, state = self.status_workspace()
        handoff = json.loads(Path(self.handoffs['current']['path']).read_text())
        handoff['stopped_writer'] = {'pid': 123}
        path = self.write(self.base / 'status-stopped.json', json.dumps(handoff).encode())
        state['maintenance_handoff'] = {'path': str(path), 'sha256': deploy.digest(path)}
        self.state(work, state)
        with patch.object(deploy, 'deployment_lock', side_effect=self.fake_lease), patch.object(deploy, 'verify_stopped_maintenance') as stopped:
            result = deploy.maintenance_dispatch_status(work, state, self.uid)
        self.assertEqual('NOT_DISPATCHED', result['phase'])
        stopped.assert_called_once()
        current = deploy.read_state(self.current)
        with patch.object(deploy, 'deployment_lock', side_effect=AssertionError('terminal status must not adopt guardian')):
            self.assertEqual(current, deploy.maintenance_dispatch_status(self.current, current, self.uid))

    def post_intent_stage(self, migrate=False):
        intent = json.loads(Path(self.handoffs['current']['path']).read_text())
        intent = {key: value for key, value in intent.items() if key not in deploy.POST_FREEZING_FIELDS}
        path = self.write(self.base / 'post-staging-intent.json', json.dumps(intent).encode())
        source = self.write(self.base / 'intent-provider', deploy.BINARY.read_bytes(), 0o755)
        argv = ['stage', '--release', 'intent', '--binary', str(source), '--frontend', str(self.current / 'frontend'),
                '--maintenance-handoff', str(path), '--maintenance-handoff-sha256', deploy.digest(path), '--json']
        if migrate:
            argv.append('--migrate')
        with patch.object(deploy.os, 'geteuid', return_value=0), patch.object(deploy, 'check_layout'), patch.object(deploy, 'check_tools'), patch.object(deploy, 'deployment_lock', side_effect=self.fake_lease), patch.object(deploy, 'run', return_value='bridge-v1'), patch.object(deploy, 'database_environment', side_effect=AssertionError('intent cannot inspect a live DB')), patch.object(deploy, 'verify_stopped_maintenance', side_effect=AssertionError('intent has no frozen proof')), patch.object(deploy, 'backup', side_effect=AssertionError('intent cannot dump a live DB')), patch.object(deploy, 'healthy', side_effect=AssertionError('intent cannot make business probes')):
            result = self.cli(*argv)
        return deploy.ROOT / 'intent', result, {'path': str(path), 'sha256': deploy.digest(path)}

    def test_post_staging_intent_with_migrate_only_stages_artifacts_and_freezes_base(self):
        work, result, binding = self.post_intent_stage(migrate=True)
        self.assertEqual('STAGED', result['phase'])
        self.assertTrue(result['migrate'])
        self.assertEqual(binding, result['maintenance_staging_intent'])
        self.assertEqual(binding, result['maintenance_handoff'])
        self.assertEqual({'lmm-api', 'lmm-api-go', 'frontend', 'state.json'}, {path.name for path in work.iterdir()})

    def test_post_staging_intent_rejects_seeded_fake_freezing_evidence_in_stage_and_status(self):
        work, state, binding = self.post_intent_stage()
        intent = json.loads(Path(binding['path']).read_text())
        intent['capture_receipt_sha256'] = 'f' * 64
        path = self.write(self.base / 'seeded-intent.json', json.dumps(intent).encode())
        seeded = {'path': str(path), 'sha256': deploy.digest(path)}
        state['maintenance_handoff'] = seeded
        self.state(work, state)
        before = self.snapshot(self.base)
        result = deploy.maintenance_dispatch_status(work, state, self.uid)
        self.assertEqual('STAGED', result['phase'])
        self.assertFalse(result['dispatch_verified_absent'])
        self.assertIn('no freezing evidence fields', result['dispatch_verification_error'])
        with patch.object(deploy.os, 'geteuid', return_value=0), patch.object(deploy, 'deployment_lock', side_effect=AssertionError('seeded intent must fail before lease')), contextlib.redirect_stdout(io.StringIO()) as output:
            code = deploy.main(['stage', '--release', 'seeded-stage', '--binary', str(deploy.BINARY), '--frontend', str(self.current / 'frontend'), '--maintenance-handoff', seeded['path'], '--maintenance-handoff-sha256', seeded['sha256'], '--json'])
        self.assertEqual(1, code)
        self.assertIn('no freezing evidence fields', output.getvalue())
        self.assertEqual(before, self.snapshot(self.base))

    def test_unstopped_post_intent_rejects_every_mutation_before_owner_writes(self):
        work, state, binding = self.post_intent_stage()
        before = self.snapshot(self.base)
        for action in ('upgrade', 'apply', 'maintenance-capture', 'maintenance-close', 'maintenance-stop', 'confirm', 'rollback', 'maintenance-release', 'cleanup'):
            with self.subTest(action=action):
                argv = [action, '--release', 'intent', '--maintenance-handoff', binding['path'], '--maintenance-handoff-sha256', binding['sha256'], '--confirm', 'api.lmm.best', '--json']
                if action == 'maintenance-release':
                    argv += ['--global-confirmation', str(self.archive), '--global-confirmation-sha256', self.args.financial_backup_sha256]
                if action == 'cleanup':
                    argv += ['--superseded-by', 'intent', '--retain-rollback', 'bridge', '--financial-backup', str(self.archive), '--financial-backup-sha256', self.args.financial_backup_sha256, '--financial-backup-receipt', str(self.args.financial_backup_receipt), '--financial-backup-receipt-sha256', self.args.financial_backup_receipt_sha256]
                with patch.object(deploy.os, 'geteuid', return_value=0), patch.object(deploy, 'deployment_lock', side_effect=AssertionError('intent must fail before leasing')), contextlib.redirect_stdout(io.StringIO()) as output:
                    code = deploy.main(argv)
                self.assertEqual(1, code)
                self.assertIn('unstopped post staging intent', output.getvalue())
                self.assertEqual(before, self.snapshot(self.base))
        absent = self.base / 'never-created-owner-root'
        with patch.object(deploy, 'ROOT', absent), patch.object(deploy.os, 'geteuid', return_value=0), contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(1, deploy.main(['apply', '--release', 'absent', '--maintenance-handoff', binding['path'], '--maintenance-handoff-sha256', binding['sha256'], '--confirm', 'api.lmm.best', '--json']))
        self.assertFalse(absent.exists())

    def test_status_accepts_exact_sealed_refinement_without_persisting_or_relaxing_intent(self):
        work, state, base = self.post_intent_stage()
        before = self.snapshot(self.base)
        sealed = self.handoffs['current']
        with patch.object(deploy, 'deployment_lock', side_effect=self.fake_lease), patch.object(deploy, 'verify_stopped_maintenance') as stopped:
            result = deploy.maintenance_dispatch_status(work, state, self.uid, handoff_path=sealed['path'], handoff_sha256=sealed['sha256'])
        self.assertEqual('NOT_DISPATCHED', result['phase'])
        self.assertTrue(result['dispatch_verified_absent'])
        self.assertEqual(sealed, result['maintenance_handoff'])
        self.assertEqual(base, result['maintenance_staging_intent'])
        self.assertEqual(before, self.snapshot(self.base))
        stopped.assert_called_once()
        changed = json.loads(Path(sealed['path']).read_text())
        changed['public_base_url'] = 'https://changed.invalid'
        path = self.write(self.base / 'bad-refinement.json', json.dumps(changed).encode())
        with patch.object(deploy, 'deployment_lock', side_effect=self.fake_lease):
            result = deploy.maintenance_dispatch_status(work, state, self.uid, handoff_path=str(path), handoff_sha256=deploy.digest(path))
        self.assertEqual('STAGED', result['phase'])
        self.assertFalse(result['dispatch_verified_absent'])
        self.assertIn('immutable staging intent', result['dispatch_verification_error'])

    def test_official_apply_consumes_sealed_refinement_and_retains_immutable_staging_base(self):
        work, state, base = self.post_intent_stage()
        sealed = self.handoffs['current']
        with patch.object(deploy.os, 'geteuid', return_value=0), patch.object(deploy, 'check_layout'), patch.object(deploy, 'check_tools'), patch.object(deploy, 'deployment_lock', side_effect=self.fake_lease), patch.object(deploy, 'verify_maintenance_database'), patch.object(deploy, 'run', return_value='bridge-v1'), patch.object(deploy, 'configure_maintenance_service'), patch.object(deploy, 'close_maintenance_admission'), patch.object(deploy, 'maintenance_admission'):
            result = self.cli('apply', '--release', 'intent', '--maintenance-handoff', sealed['path'], '--maintenance-handoff-sha256', sealed['sha256'], '--confirm', 'api.lmm.best', '--json')
        self.assertEqual('AWAITING_CONFIRMATION', result['phase'])
        self.assertEqual(sealed, result['maintenance_handoff'])
        self.assertEqual(base, result['maintenance_staging_intent'])
        record = json.loads((self.bridge / 'maintenance-transfer.intent.json').read_text())
        self.assertEqual(sealed['sha256'], record['next_handoff_sha256'])


class OrdinaryHistoryTests(unittest.TestCase):
    setUp = CleanupTests.setUp
    write = CleanupTests.write
    state = CleanupTests.state
    workspace = CleanupTests.workspace
    install_chain = CleanupTests.install_chain
    maintenance = CleanupTests.maintenance
    snapshot = CleanupTests.snapshot
    cli = CleanupTests.cli

    def proof(self):
        self.install_chain(include_capture=True)
        state = deploy.read_state(self.current)
        state['maintenance_stage'] = 'post'
        self.state(self.current, state)
        confirmation = {'format': 'lmm-credit-maintenance-release-v1', 'all_nodes_confirmed': True,
                        'transition_id': state['transition_id'], 'transition_intent_sha256': state['transition_intent_sha256'],
                        'business_plan_sha256': 'd' * 64, 'confirmations_sha256': 'e' * 64, 'nodes': ['arch', 'ubuntu']}
        confirmed = self.write(self.base / 'confirmation.json', json.dumps(confirmation).encode())
        controller = {'format': 'lmm-credit-financial-maintenance-v1', 'phase': 'RELEASED',
                      'guardian_release': 'ordinary-owner-only-no-lock-path-deletion',
                      'transition_id': state['transition_id'], 'transition_intent_sha256': state['transition_intent_sha256'],
                      'business_plan_sha256': 'd' * 64, 'confirmations_sha256': 'e' * 64,
                      'global_release': {'path': '/original/controller/path', 'sha256': deploy.digest(confirmed)},
                      'stopped_handoffs': {'ubuntu': state['maintenance_handoff']}, 'guardian_generations': {'ubuntu': 2147483647}}
        released = self.write(self.base / 'controller.json', json.dumps(controller).encode())
        self.history_args = SimpleNamespace(release='current', released_controller=released,
                             released_controller_sha256=deploy.digest(released), global_confirmation=confirmed,
                             global_confirmation_sha256=deploy.digest(confirmed), execute=False)
        self.stack.enter_context(patch.object(deploy, 'NATIVE_TRANSACTION_LEASE', self.base / 'native.lease'))
        self.locks = {key: str(self.write(self.base / (key + '.lock'), b'')) for key in ['native', 'systemd', 'frontend']}
        self.stack.enter_context(patch.object(deploy.guardian, 'LOCKS', self.locks))
        real_lock_path = deploy.history_lock_path
        self.stack.enter_context(patch.object(deploy, 'history_lock_path', side_effect=lambda path: real_lock_path(path, uid=self.uid)))
        return controller

    def register(self):
        self.proof()
        self.history_args.execute = True
        deploy.register_released_history(self.history_args)

    def incomplete(self):
        work = deploy.ROOT / 'incomplete'
        work.mkdir(mode=0o700)
        self.write(work / 'lmm-api-go', b'failed candidate', 0o755)
        (work / 'lmm-api').symlink_to('lmm-api-go')
        (work / 'frontend').mkdir(mode=0o700)
        self.write(work / 'frontend/index.html', b'failed candidate frontend', 0o644)
        (work / 'logs').mkdir(mode=0o700)
        self.write(work / 'logs/oneapi.log', b'all historical logs preserved')
        self.write(work / 'verify-stage.log', b'failed before state publication')
        return work, SimpleNamespace(release=work.name, execute=False)

    def test_only_exact_native_lock_accepts_real_sticky_parent(self):
        sticky = self.base / 'run-lock';sticky.mkdir(mode=0o700);sticky.chmod(0o1777)
        native = self.write(sticky / 'lmm-api-go-deploy.lock', b'')
        # Actual filesystem ownership/mode inspection, with fixture UID only.
        deploy.history_lock_path(native, uid=self.uid, native_lock=native)
        other = self.write(sticky / 'another.lock', b'')
        with self.assertRaises(RuntimeError):
            deploy.history_lock_path(other, uid=self.uid, native_lock=native)
        sticky.chmod(0o777)
        with self.assertRaises(RuntimeError):
            deploy.history_lock_path(native, uid=self.uid, native_lock=native)
        sticky.chmod(0o1777)
        outside = self.base / 'another-sticky';outside.mkdir();outside.chmod(0o1777)
        wrong = self.write(outside / native.name, b'')
        with self.assertRaises(RuntimeError):
            deploy.history_lock_path(wrong, uid=self.uid, native_lock=native)
        link = sticky / 'link';link.symlink_to(native)
        with self.assertRaises(RuntimeError):
            deploy.history_lock_path(link, uid=self.uid, native_lock=link)
        hardlink = self.base / 'hardlink';os.link(native, hardlink)
        with self.assertRaises(RuntimeError):
            deploy.history_lock_path(native, uid=self.uid, native_lock=native)
        hardlink.unlink()
        native.chmod(0o666)
        with self.assertRaises(RuntimeError):
            deploy.history_lock_path(native, uid=self.uid, native_lock=native)
        native.chmod(0o600)

    def test_history_lock_name_replacement_is_rejected_after_nofollow_open(self):
        self.proof()
        actual_open = os.open
        selected = sorted(self.locks.values())[0]
        def replace_after_open(path, flags, *args):
            fd = actual_open(path, flags, *args)
            if str(path) == selected:
                Path(path).unlink()
                self.write(Path(path), b'replacement inode')
            return fd
        with patch.object(deploy.os, 'open', side_effect=replace_after_open):
            with self.assertRaisesRegex(RuntimeError, 'single-linked'):
                with deploy.history_locks():
                    self.fail('changed named lock must never enter the protected section')

    def test_registration_dry_run_and_exact_chain_preserve_original_bytes(self):
        self.proof()
        before = self.snapshot(deploy.ROOT)
        result = deploy.register_released_history(self.history_args)
        self.assertEqual(['bridge', 'capture'], result['ancestors'])
        self.assertFalse(deploy.history_root().exists())
        self.assertEqual(set(), deploy.released_ancestors())
        self.history_args.execute = True
        deploy.register_released_history(self.history_args)
        self.assertEqual({'bridge', 'capture'}, deploy.released_ancestors())
        self.assertEqual(before, self.snapshot(deploy.ROOT))
        with self.assertRaises(FileExistsError):
            deploy.register_released_history(self.history_args)

    def test_doctor_only_passes_after_complete_registration_and_archival(self):
        self.proof()
        with patch.object(deploy, 'check_tools'), patch.object(deploy, 'check_layout'), patch.object(deploy, 'service_environment_files', return_value=[str(deploy.ENVIRONMENT)]), patch.object(deploy, 'property_value', return_value=str(os.getpid())):
            self.assertFalse(deploy.doctor()['ok'])
            self.history_args.execute = True
            deploy.register_released_history(self.history_args)
            self.assertTrue(deploy.doctor()['ok'])
            work, args = self.incomplete()
            self.assertFalse(deploy.doctor()['ok'])
            args.execute = True
            deploy.archive_incomplete(args)
            self.assertTrue(deploy.doctor()['ok'])
            self.workspace('unknown', 'FROZEN')
            self.assertFalse(deploy.doctor()['ok'])

    def test_registration_rejects_unreleased_wrong_confirmation_and_post_identity(self):
        original = self.proof()
        for key, value in [('phase', 'RELEASE_INTENT'), ('transition_id', 'another'),
                           ('global_release', {'sha256': 'f' * 64}), ('stopped_handoffs', {'ubuntu': {'path': '/wrong', 'sha256': 'f' * 64}})]:
            with self.subTest(key=key):
                changed = dict(original, **{key: value})
                self.write(self.history_args.released_controller, json.dumps(changed).encode())
                self.history_args.released_controller_sha256 = deploy.digest(self.history_args.released_controller)
                with self.assertRaises(RuntimeError):
                    deploy.register_released_history(self.history_args)
        self.assertFalse(deploy.history_root().exists())

    def test_registered_history_revalidates_original_states_and_transfer_records(self):
        self.register()
        frozen = deploy.ROOT / 'capture/state.json'
        original = frozen.read_bytes()
        self.write(frozen, original + b' ')
        with self.assertRaises(RuntimeError):
            deploy.released_ancestors()
        self.write(frozen, original)
        (deploy.ROOT / 'capture/maintenance-transfer.bridge.json').unlink()
        with self.assertRaises(OSError):
            deploy.released_ancestors()

    def test_normal_apply_accepts_only_registered_ancestors_before_stopping(self):
        self.register()
        next_work = self.workspace('next', 'STAGED')
        state = deploy.read_state(next_work)
        state.update(migrate=False, backup_exclude_tables=[])
        self.state(next_work, state)
        unknown = self.workspace('unknown', 'FROZEN')
        with patch.object(deploy.os, 'geteuid', return_value=0), patch.object(deploy, 'check_layout'), patch.object(deploy, 'check_tools'), patch.object(deploy, 'verify'), patch.object(deploy, 'run', return_value='bridge-v1'), patch.object(deploy, 'stop') as stop:
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                code = deploy.main(['apply', '--release', 'next', '--confirm', 'api.lmm.best', '--json'])
            self.assertEqual(1, code)
            self.assertIn('another deployment needs recovery', out.getvalue())
            stop.assert_not_called()
            # Unknown is explicitly terminalized by the fixture, never ignored.
            value = deploy.read_state(unknown);value['phase'] = 'ROLLED_BACK';self.state(unknown, value)
            result = self.cli('apply', '--release', 'next', '--confirm', 'api.lmm.best', '--json')
            self.assertEqual('AWAITING_CONFIRMATION', result['phase'])
            stop.assert_called_once()
        self.assertEqual('FROZEN', deploy.read_state(self.bridge)['phase'])
        self.assertEqual('FROZEN', deploy.read_state(deploy.ROOT / 'capture')['phase'])

    def test_unarchived_incomplete_preparation_blocks_ordinary_apply(self):
        self.register()
        incomplete, args = self.incomplete()
        work = self.workspace('next', 'STAGED')
        state = deploy.read_state(work);state.update(migrate=False, backup_exclude_tables=[]);self.state(work, state)
        with patch.object(deploy.os, 'geteuid', return_value=0), patch.object(deploy, 'check_layout'), patch.object(deploy, 'check_tools'), patch.object(deploy, 'stop') as stop:
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                code = deploy.main(['apply', '--release', 'next', '--confirm', 'api.lmm.best', '--json'])
            self.assertEqual(1, code)
            self.assertIn('incomplete preparation', out.getvalue())
            stop.assert_not_called()
        self.assertTrue(incomplete.exists())

    def test_incomplete_full_archive_preserves_logs_links_and_original_without_phase(self):
        self.register()
        work, args = self.incomplete()
        original = self.snapshot(work)
        deploy.archive_incomplete(args)
        self.assertEqual(original, self.snapshot(work))
        self.assertFalse((deploy.history_root() / 'incomplete').exists())
        # A historical descriptive inventory is not an active reference.
        self.write(self.old / 'inventory.json', json.dumps({'observed_path': str(work)}).encode())
        args.execute = True
        deploy.archive_incomplete(args)
        self.assertFalse(work.exists())
        archive = deploy.history_root() / 'incomplete' / args.release
        self.assertEqual(original, self.snapshot(archive / 'copy'))
        self.assertEqual(original, self.snapshot(archive / 'original'))
        self.assertFalse((archive / 'original/state.json').exists())
        deploy.verify_incomplete_archives()
        self.write(archive / 'copy/verify-stage.log', b'changed')
        with self.assertRaises(RuntimeError):
            deploy.verify_incomplete_archives()

    def test_incomplete_refuses_mutation_unknown_symlink_and_active_reference(self):
        self.register()
        work, args = self.incomplete()
        args.execute = True
        for name in ['state.next', 'previous-binary', 'unexpected']:
            marker = self.write(work / name, b'mutation or unknown')
            with self.assertRaises(RuntimeError):
                deploy.archive_incomplete(args)
            marker.unlink()
        link = work / 'logs/secret-link';link.symlink_to(self.base / 'service.env')
        with self.assertRaises(RuntimeError):
            deploy.archive_incomplete(args)
        link.unlink()
        staged = self.workspace('active', 'STAGED')
        value = deploy.read_state(staged);value['artifact_reference'] = str(work / 'frontend');self.state(staged, value)
        with self.assertRaisesRegex(RuntimeError, 'authoritative'):
            deploy.archive_incomplete(args)
        self.assertTrue(work.exists())
        self.assertFalse((deploy.history_root() / 'incomplete').exists())

    def test_archive_refuses_real_guardian_lock_native_lease_and_inflight_process(self):
        self.register()
        work, args = self.incomplete()
        args.execute = True
        with open(self.locks['frontend'], 'rb') as fd:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            with self.assertRaisesRegex(RuntimeError, 'guardian'):
                deploy.archive_incomplete(args)
        self.write(deploy.NATIVE_TRANSACTION_LEASE, b'pending owner')
        with self.assertRaisesRegex(RuntimeError, 'lease'):
            deploy.archive_incomplete(args)
        deploy.NATIVE_TRANSACTION_LEASE.unlink()
        with patch.object(deploy, 'cleanup_process_references', return_value=[{'pid': 1, 'path': str(work / 'logs')}]) as inspect:
            with self.assertRaisesRegex(RuntimeError, 'authoritative'):
                deploy.archive_incomplete(args)
            inspect.assert_called_with(work, entire_workspace=True)
        self.assertTrue(work.exists())

    def test_interrupted_archive_still_blocks_normal_doctor(self):
        self.register()
        work, args = self.incomplete();args.execute = True
        real_write = deploy.immutable_write
        def interrupted(path, *rest):
            if path.name == 'receipt.json':
                raise KeyboardInterrupt()
            return real_write(path, *rest)
        with patch.object(deploy, 'immutable_write', side_effect=interrupted):
            with self.assertRaises(KeyboardInterrupt):
                deploy.archive_incomplete(args)
        self.assertFalse(work.exists())
        archive = deploy.history_root() / 'incomplete' / args.release
        self.assertTrue((archive / 'copy/verify-stage.log').exists())
        self.assertTrue((archive / 'original/verify-stage.log').exists())
        with self.assertRaises(OSError):
            deploy.verify_incomplete_archives()


if __name__ == '__main__':
    unittest.main()
