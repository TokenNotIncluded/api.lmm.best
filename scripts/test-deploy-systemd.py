import importlib.util
from pathlib import Path
import tempfile
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


if __name__ == '__main__':
    unittest.main()
