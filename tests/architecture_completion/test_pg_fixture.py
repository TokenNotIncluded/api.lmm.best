"""Offline command/ownership checks. No Docker or PostgreSQL result is simulated as PASS."""
import copy
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import pg_fixture as pg


class FixtureContractTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.fixture = pg.Fixture(self.root)
        self.fixture.container = 'c'*64
        self.info = {'Id': self.fixture.container, 'Image': 'sha256:reviewed',
            'Config': {'Image': pg.IMAGE, 'Labels': {pg.LABEL: self.fixture.nonce}},
            'HostConfig': {'Privileged': False, 'Binds': None, 'Memory': 512*1024**2,
                           'NanoCpus': 10**9, 'PidsLimit': 128,
                           'Tmpfs': {'/var/lib/postgresql': 'rw,size=402653184'}},
            'State': {'Running': True, 'StartedAt': 'synthetic-time', 'Pid': 123},
            'Mounts': [{'Type': 'tmpfs'}],
            'NetworkSettings': {'Ports': {'5432/tcp': [{'HostIp': '127.0.0.1', 'HostPort': '32001'}]}}}

    def binding(self, info=None):
        return pg.container_binding(info or self.info, self.fixture.nonce, self.fixture.container)

    def test_exact_binding(self):
        binding = self.binding()
        self.assertEqual(binding['port'], 32001)
        self.assertNotIn('Config', binding)

    def test_wrong_owner_container_or_image_is_refused(self):
        for key, value in [('Id', 'd'*64), ('Config', {'Image': pg.IMAGE, 'Labels': {}}),
                           ('Config', {'Image': 'unreviewed', 'Labels': {pg.LABEL:self.fixture.nonce}})]:
            info = copy.deepcopy(self.info)
            info[key] = value
            with self.subTest(key=key), self.assertRaises(pg.FixtureError):
                self.binding(info)

    def test_mounts_privilege_or_resource_cap_changes_refused(self):
        for key, value in [('Privileged', True), ('Binds', ['/production:/data']),
                           ('Memory', 0), ('NanoCpus', 0), ('PidsLimit', -1), ('Tmpfs', {})]:
            info = copy.deepcopy(self.info)
            info['HostConfig'][key] = value
            with self.subTest(key=key), self.assertRaises(pg.FixtureError):
                self.binding(info)
        info = copy.deepcopy(self.info)
        info['Mounts'] = [{'Type': 'volume'}]
        with self.assertRaises(pg.FixtureError):
            self.binding(info)

    def test_public_or_protected_port_refused(self):
        for address, port in [('0.0.0.0', '32001'), ('127.0.0.1','18080'),
                              ('127.0.0.1','47399'), ('127.0.0.1','unknown')]:
            info = copy.deepcopy(self.info)
            info['NetworkSettings']['Ports']['5432/tcp'] = [{'HostIp':address,'HostPort':port}]
            with self.subTest(port=port), self.assertRaises(pg.FixtureError):
                self.binding(info)

    def test_incomplete_identity_refused(self):
        for key in self.info:
            info = copy.deepcopy(self.info)
            del info[key]
            with self.subTest(key=key), self.assertRaises(pg.FixtureError):
                self.binding(info)

    def test_host_environment_never_selects_database_or_daemon(self):
        with patch.dict(os.environ, {'PGHOST':'production', 'PGPASSFILE':'private',
                                    'PGSERVICE':'production', 'DATABASE_URL':'secret',
                                    'DOCKER_HOST':'tcp://remote', 'DOCKER_CONTEXT':'production'}):
            environment = pg.clean_environment()
        self.assertNotIn('PGHOST',environment)
        self.assertNotIn('PGPASSFILE',environment)
        self.assertNotIn('PGSERVICE',environment)
        self.assertNotIn('DATABASE_URL',environment)
        self.assertNotIn('DOCKER_HOST',environment)
        self.assertNotIn('DOCKER_CONTEXT',environment)

    def test_runtime_password_is_not_in_argv(self):
        completed = subprocess.CompletedProcess([],0,b'1\n',b'')
        with patch.object(subprocess,'run',return_value=completed) as run:
            self.fixture.sql('SELECT 1;', role='runtime', password='synthetic-secret')
        args, kwargs = run.call_args
        self.assertNotIn('synthetic-secret', ' '.join(args[0]))
        self.assertEqual(kwargs['env']['PGPASSWORD'],'synthetic-secret')
        self.assertEqual(args[0][:3],pg.DOCKER)
        self.assertNotIn('shell',kwargs)

    def test_external_database_or_injected_identifier_refused(self):
        with self.assertRaises(pg.FixtureError):
            self.fixture.sql('SELECT 1;',database='unowned')
        for value in ('public; DROP DATABASE postgres;', 'UPPER', 'a'*64, '', None):
            with self.subTest(value=value), self.assertRaises(pg.FixtureError):
                pg.identifier(value)

    def test_secret_errors_are_not_echoed_and_timeouts_are_unknown(self):
        completed = subprocess.CompletedProcess([],1,b'secret',b'password=secret')
        with patch.object(subprocess,'run',return_value=completed):
            with self.assertRaises(pg.FixtureError) as error:
                self.fixture.docker('inspect','anything')
            self.assertNotIn('secret',str(error.exception))
        with patch.object(subprocess,'run',side_effect=subprocess.TimeoutExpired('secret',1)):
            with self.assertRaisesRegex(pg.FixtureError,'UNKNOWN'):
                self.fixture.docker('start','anything')

    def test_private_credentials_are_exclusive(self):
        path = self.root/'pgpass'
        pg.write_private(path,'secret\n')
        self.assertEqual(path.stat().st_mode & 0o777,0o600)
        with self.assertRaises(FileExistsError):
            pg.write_private(path,'replacement')
        self.assertEqual(path.read_text(),'secret\n')

    def test_missing_identity_prevents_any_cleanup(self):
        with patch.object(self.fixture,'docker') as docker:
            with self.assertRaises(pg.FixtureError):
                self.fixture.cleanup()
            docker.assert_not_called()

    def test_unknown_task_or_duplicate_refused_before_database_access(self):
        self.fixture.allocations['A09'] = {}
        with patch.object(self.fixture,'verify') as verify:
            for task in ('root','A11','A09'):
                with self.assertRaises(pg.FixtureError):
                    self.fixture.allocate(task)
            verify.assert_not_called()

    def test_failed_live_client_check_does_not_remove_container(self):
        self.fixture.binding = self.binding()
        self.fixture.identity = {'system_id':'1234'}
        with patch.object(self.fixture,'verify'), patch.object(self.fixture,'sql',side_effect=pg.FixtureError('client')):
            with patch.object(self.fixture,'docker') as docker:
                with self.assertRaises(pg.FixtureError):
                    self.fixture.cleanup()
                docker.assert_not_called()

    def test_missing_docker_is_blocked_before_create(self):
        fresh = pg.Fixture(self.root)
        with patch.object(pg.shutil,'which',return_value=None):
            with patch.object(fresh,'docker') as docker:
                with self.assertRaisesRegex(pg.FixtureError,'BLOCKED'):
                    fresh.start()
                docker.assert_not_called()
        self.assertFalse((fresh.root/'create-intent.json').exists())


    def test_only_expected_sqlstate_counts_as_permission_denial(self):
        for code, stderr in [(3,b'ERROR: 42501: permission denied'),
                             (2,b'psql: connection failed: FATAL: 42501: permission denied')]:
            result = subprocess.CompletedProcess([],code,b'',stderr)
            with patch.object(subprocess,'run',return_value=result):
                with self.assertRaises(pg.PermissionDenied):
                    self.fixture.sql('SELECT 1;')
        for code, stderr in [(3,b'ERROR: 42601: syntax error'),
                             (3,b'ERROR: P0001: forged ERROR: 42501: denied'),
                             (125,b'ERROR: 42501: daemon failed')]:
            result = subprocess.CompletedProcess([],code,b'',stderr)
            with patch.object(subprocess,'run',return_value=result):
                with self.assertRaises(pg.FixtureError) as error:
                    self.fixture.sql('SELECT 1;')
                self.assertNotIsInstance(error.exception, pg.PermissionDenied)

    def test_restore_without_allocated_target_is_refused(self):
        with patch.object(self.fixture,'verify'):
            with self.assertRaisesRegex(pg.FixtureError,'independent target'):
                self.fixture.restore('unowned_restore_db', self.root/'dump', 'a'*64)

    def test_lost_restore_acknowledgement_cannot_replay(self):
        import backup
        backup.BACKUPS.mkdir(mode=0o700,parents=True,exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='a09-command-test-',dir=backup.BACKUPS) as location:
            source = Path(location)/'synthetic.dump'
            data = b'PGDMP synthetic command-only fixture'
            source.write_bytes(data)
            source.chmod(0o600)
            database = 'a09_command_restore'
            self.fixture.databases.add(database)
            self.fixture.restore_targets.add(database)
            self.fixture.identity = {'system_id':'1234'}
            with patch.object(self.fixture,'verify'), patch.object(self.fixture,'sql',return_value='0'):
                with patch.object(subprocess,'run',side_effect=subprocess.TimeoutExpired('pg_restore',1)) as run:
                    with self.assertRaisesRegex(pg.FixtureError,'UNKNOWN'):
                        self.fixture.restore(database,source,hashlib.sha256(data).hexdigest())
                    with self.assertRaises(FileExistsError):
                        self.fixture.restore(database,source,hashlib.sha256(data).hexdigest())
                    self.assertEqual(run.call_count,1)
            self.assertTrue((self.fixture.root/('restore-intent-'+database+'.json')).is_file())
            self.assertFalse((self.fixture.root/('restore-finished-'+database+'.json')).exists())

    def test_restore_digest_mismatch_never_calls_pg_restore(self):
        import backup
        backup.BACKUPS.mkdir(mode=0o700,parents=True,exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='a09-command-test-',dir=backup.BACKUPS) as location:
            source = Path(location)/'synthetic.dump'
            source.write_bytes(b'PGDMP synthetic command-only fixture')
            source.chmod(0o600)
            database = 'a09_command_restore'
            self.fixture.databases.add(database)
            self.fixture.restore_targets.add(database)
            self.fixture.identity = {'system_id':'1234'}
            with patch.object(self.fixture,'verify'), patch.object(self.fixture,'sql',return_value='0'):
                with patch.object(subprocess,'run') as run:
                    with self.assertRaisesRegex(pg.FixtureError,'digest changed'):
                        self.fixture.restore(database,source,'a'*64)
                    run.assert_not_called()
            self.assertFalse((self.fixture.root/('restore-intent-'+database+'.json')).exists())


if __name__ == '__main__':
    unittest.main()
