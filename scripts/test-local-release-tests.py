"""Local evidence gate integration tests; actual Git objects, no network."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


class LocalReleaseTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix='local release evidence ')
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.repo = self.root / 'repo'
        (self.repo / 'scripts').mkdir(parents=True)
        (self.repo / 'apps/web').mkdir(parents=True)
        (self.repo / 'apps/web/source.ts').write_text('export const value = 1;\n')
        for name in ('local-release-tests.py', 'verify-release-commit-checks.sh'):
            shutil.copyfile(ROOT / 'scripts' / name, self.repo / 'scripts' / name)
        self.env = dict(os.environ)
        for name in ('LMM_LOCAL_TEST_EVIDENCE', 'LMM_LOCAL_TEST_EVIDENCE_JSON', 'GITHUB_TOKEN'):
            self.env.pop(name, None)
        self.git('init', '-q')
        self.git('config', 'user.email', 'test@example.invalid')
        self.git('config', 'user.name', 'Local evidence test')
        self.commit()
        self.record = self.root / 'record.json'

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.repo, env=self.env).decode().strip()

    def commit(self):
        self.git('add', '.')
        self.git('-c', 'commit.gpgsign=false', 'commit', '-qm', 'fixture')
        return self.git('rev-parse', 'HEAD')

    def call(self, *args, env=None):
        return subprocess.run([sys.executable, '-B', 'scripts/local-release-tests.py', *args],
                              cwd=self.repo, env=env or self.env, text=True, capture_output=True, timeout=20)

    def run_check(self, exit_code=0):
        return self.call('run', '--component', 'web', '--output', str(self.record), '--',
                         sys.executable, '-c', f'print("actual test output");raise SystemExit({exit_code})')

    def verify(self, component='web'):
        return self.call('verify', self.git('rev-parse', 'HEAD'), '--component', component,
                         '--evidence', str(self.record))

    def test_actual_run_records_logs_and_passes_shell_gate_without_token(self):
        self.assertEqual(0, self.run_check().returncode)
        record = json.loads(self.record.read_bytes())
        self.assertEqual(hashlib.sha256(b'actual test output\n').hexdigest(), record['checks'][0]['stdout_sha256'])
        env = dict(self.env, LMM_LOCAL_TEST_EVIDENCE=str(self.record))
        result = subprocess.run(['bash', 'scripts/verify-release-commit-checks.sh', self.git('rev-parse', 'HEAD'),
                                 '--component', 'web'], cwd=self.repo, env=env, capture_output=True, text=True)
        self.assertEqual(0, result.returncode, result.stderr)

    def test_unrelated_commit_reuses_exact_component_but_web_change_rejects(self):
        self.assertEqual(0, self.run_check().returncode)
        (self.repo / 'README.md').write_text('unrelated change\n')
        self.commit()
        self.assertEqual(0, self.verify().returncode)
        (self.repo / 'apps/web/source.ts').write_text('export const value = 2;\n')
        self.commit()
        result = self.verify()
        self.assertEqual(1, result.returncode)
        self.assertIn('differ', result.stderr)

    def test_failure_record_is_preserved_and_rejected(self):
        self.assertEqual(1, self.run_check(3).returncode)
        self.assertTrue(self.record.exists())
        self.assertEqual(3, json.loads(self.record.read_bytes())['checks'][0]['exit_code'])
        self.assertEqual(1, self.verify().returncode)
        self.assertEqual(1, self.run_check().returncode)

    def test_import_reports_its_origin_and_preserves_the_actual_log_hash(self):
        log = self.root / 'completed.log'
        log.write_bytes(b'previously completed test\n')
        result = self.call('import', '--component', 'web', '--output', str(self.record),
                           '--command', 'completed test command', '--exit-code', '0', '--stdout', str(log))
        self.assertEqual(0, result.returncode, result.stderr)
        check = json.loads(self.record.read_bytes())['checks'][0]
        self.assertEqual('completed-log-import', check['recorded_from'])
        self.assertEqual(hashlib.sha256(log.read_bytes()).hexdigest(), check['stdout_sha256'])
        self.assertEqual(0, self.verify().returncode)

    def test_wrong_component_empty_checks_bad_log_hash_and_boolean_exit_reject(self):
        self.assertEqual(0, self.run_check().returncode)
        original = json.loads(self.record.read_bytes())
        self.assertEqual(1, self.verify('go').returncode)
        for kind in ('empty', 'hash', 'boolean'):
            record = json.loads(json.dumps(original))
            if kind == 'empty': record['checks'] = []
            elif kind == 'hash': record['checks'][0]['stdout_sha256'] = 'unknown'
            else: record['checks'][0]['exit_code'] = False
            self.record.write_text(json.dumps(record))
            self.assertEqual(1, self.verify().returncode, kind)

    def test_missing_and_duplicate_evidence_reject_without_network(self):
        result = self.call('verify', self.git('rev-parse', 'HEAD'), '--component', 'web')
        self.assertEqual(1, result.returncode)
        self.assertIn('required', result.stderr)
        self.record.write_text('{"format":1,"format":1}')
        self.assertEqual(1, self.verify().returncode)

    def test_dirty_source_refuses_to_record_success(self):
        (self.repo / 'apps/web/source.ts').write_text('uncommitted changes\n')
        self.assertEqual(1, self.run_check().returncode)
        self.assertFalse(self.record.exists())


    def add_microkernel_fixture(self):
        for name in ('apps/lmm-extensions/host.go', 'apps/lmm-core/schema/identity.sql',
                     'apps/lmm/Cargo.toml', 'packaging/distribution.json',
                     'packaging/linux/packages.json', 'scripts/build-distribution.py',
                     'contracts/proto/lmm/core/v1/control.proto',
                     'deployment/docker/compose.rpc-core.yml',
                     'deployment/docker/compose.rpc-extensions.yml'):
            path = self.repo / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text('original fixture\n')
        self.commit()

    def record_component(self, component):
        self.record = self.root / (component + '-record.json')
        result = self.call('run', '--component', component, '--output', str(self.record), '--',
                           sys.executable, '-c', 'print("evidence gate fixture")')
        self.assertEqual(0, result.returncode, result.stderr)
        return self.record

    def test_extension_evidence_tracks_the_canonical_go_directory(self):
        self.add_microkernel_fixture()
        self.record_component('extensions')
        (self.repo / 'apps/lmm-extensions/host.go').write_text('changed host\n')
        self.commit()
        self.assertEqual(1, self.verify('extensions').returncode)

    def test_protocol_change_invalidates_both_core_and_extension_evidence(self):
        self.add_microkernel_fixture()
        records = {component: self.record_component(component) for component in ('core', 'extensions')}
        (self.repo / 'contracts/proto/lmm/core/v1/control.proto').write_text('changed contract\n')
        self.commit()
        for component, record in records.items():
            self.record = record
            self.assertEqual(1, self.verify(component).returncode, component)

    def test_extension_only_change_does_not_invalidate_core_evidence(self):
        self.add_microkernel_fixture()
        self.record_component('core')
        (self.repo / 'apps/lmm-extensions/host.go').write_text('independent extension change\n')
        self.commit()
        self.assertEqual(0, self.verify('core').returncode)
        (self.repo / 'apps/lmm-core/schema/identity.sql').write_text('changed core schema\n')
        self.commit()
        self.assertEqual(1, self.verify('core').returncode)

    def test_missing_component_source_cannot_produce_success_evidence(self):
        result = self.call('run', '--component', 'extensions', '--output', str(self.record), '--',
                           sys.executable, '-c', 'raise SystemExit(0)')
        self.assertEqual(1, result.returncode)
        self.assertIn('source directory is missing', result.stderr)
        self.assertFalse(self.record.exists())

    def test_distribution_change_invalidates_every_component_record(self):
        self.add_microkernel_fixture()
        records = {component: self.record_component(component)
                   for component in ('web', 'core', 'extensions', 'cli')}
        (self.repo / 'packaging/distribution.json').write_text('changed archive contract\n')
        self.commit()
        for component, record in records.items():
            self.record = record
            self.assertEqual(1, self.verify(component).returncode, component)

    def test_native_package_change_invalidates_cli_evidence_but_not_static_web(self):
        self.add_microkernel_fixture()
        records = {component: self.record_component(component) for component in ('web', 'cli')}
        (self.repo / 'packaging/linux/packages.json').write_text('changed native package contract\n')
        self.commit()
        for component, expected in (('web', 0), ('cli', 1)):
            self.record = records[component]
            self.assertEqual(expected, self.verify(component).returncode, component)

    def test_cli_gitlink_pin_change_invalidates_local_evidence(self):
        origin = self.root / 'cli-origin'
        origin.mkdir()
        for args in (('init', '-q'), ('config', 'user.name', 'CLI fixture'),
                     ('config', 'user.email', 'fixture@example.invalid')):
            subprocess.run(['git', '-C', str(origin), *args], check=True, capture_output=True)
        (origin / 'Cargo.toml').write_text('CLI source fixture\n')
        subprocess.run(['git', '-C', str(origin), 'add', '.'], check=True, capture_output=True)
        subprocess.run(['git', '-C', str(origin), '-c', 'commit.gpgsign=false',
                        'commit', '-qm', 'Initial CLI fixture'], check=True, capture_output=True)
        self.git('-c', 'protocol.file.allow=always', 'submodule', 'add', '-q', str(origin), 'apps/lmm')
        self.commit()
        self.record_component('cli')
        self.assertEqual(0, self.verify('cli').returncode)
        child = self.repo / 'apps/lmm'
        (child / 'Cargo.toml').write_text('Different CLI source fixture\n')
        subprocess.run(['git', '-C', str(child), 'add', '.'], check=True, capture_output=True)
        subprocess.run(['git', '-C', str(child), '-c', 'user.name=CLI fixture',
                        '-c', 'user.email=fixture@example.invalid', '-c', 'commit.gpgsign=false',
                        'commit', '-qm', 'Changed CLI fixture'], check=True, capture_output=True)
        self.commit()
        self.assertEqual(1, self.verify('cli').returncode)

    def test_cli_submodule_url_change_invalidates_local_evidence(self):
        self.add_microkernel_fixture()
        path = self.repo / '.gitmodules'
        path.write_text('[submodule "apps/lmm"]\n\tpath = apps/lmm\n'
                        '\turl = https://example.invalid/lmm.git\n')
        self.commit()
        self.record_component('cli')
        path.write_text(path.read_text().replace('example.invalid', 'other.invalid'))
        self.commit()
        self.assertEqual(1, self.verify('cli').returncode)

if __name__ == '__main__':
    unittest.main()
