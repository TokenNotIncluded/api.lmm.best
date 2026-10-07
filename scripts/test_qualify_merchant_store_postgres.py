"""Offline contracts only: no Go execution, DB, provider, or network access."""
import importlib.util
import io
import json
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch


def load(name, relative):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).parent / relative)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


runner = load('merchant_pg_ci', 'ci/qualify-merchant-store-postgres.py')
builder = load('merchant_cap1_builder', 'build-merchant-store-cap1-probe.py')


def passes(names):
    return [{'Action': 'pass', 'Package': runner.PACKAGE, 'Test': name} for name in names] + [
        {'Action': 'pass', 'Package': runner.PACKAGE}]


class MerchantPostgresEvidenceContract(unittest.TestCase):
    def setUp(self):
        self.events = passes((*runner.PARENTS, *runner.LEAVES))

    def test_finite_selection_matches_real_source_and_requires_all_parents_and_pg_leaves(self):
        self.assertEqual(13, len(set(runner.PARENTS)))
        self.assertEqual(20, len(set(runner.LEAVES)))
        source = '\n'.join(path.read_text() for path in (runner.REPOSITORY / 'apps/api-go/model').glob('merchant_store*test.go'))
        actual = set(re.findall(r'^func (TestMerchantStore\w+)\(t \*testing.T\)', source, re.M))
        self.assertTrue(set(runner.PARENTS).issubset(actual))
        for leaf in runner.LEAVES:
            with self.subTest(leaf=leaf):
                # testing normalizes the two space-containing schema case names.
                self.assertTrue(leaf.split('/')[1] in source or leaf.split('/')[1].replace('_', ' ') in source)
        self.assertEqual({'parents_passed': 13, 'pg_leaves_passed': 20, 'failed': 0, 'skipped': 0}, runner.validate_pg(self.events))
        for name in (*runner.PARENTS, *runner.LEAVES):
            with self.subTest(missing=name), self.assertRaises(ValueError):
                runner.validate_pg([event for event in self.events if event.get('Test') != name])

    def test_parent_pass_cannot_replace_guest_pg_leaf_or_any_other_pg_leaf(self):
        with self.assertRaises(ValueError):
            runner.validate_pg(passes(runner.PARENTS))

    def test_any_skip_fail_package_failure_duplicate_or_wrong_package_is_rejected(self):
        for action in ('skip', 'fail'):
            for name in (None, runner.PARENTS[0], runner.LEAVES[-1], 'unexpected-test'):
                with self.subTest(action=action, name=name), self.assertRaises(ValueError):
                    runner.validate_pg(self.events + [{'Action': action, 'Package': runner.PACKAGE, 'Test': name}])
        for suffix in ([self.events[0]], [{'Action': 'pass', 'Package': 'other/model', 'Test': runner.PARENTS[0]}]):
            with self.subTest(suffix=suffix), self.assertRaises(ValueError):
                runner.validate_pg(self.events + suffix)
        with self.assertRaises(ValueError):
            runner.validate_pg(self.events[:-1])

    def test_empty_and_malformed_json_evidence_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'events.jsonl'
            for content in ('', '{}\nnot JSON', '[]\n', '{"Action":"fail","Action":"pass"}'):
                path.write_text(content)
                with self.subTest(content=content), self.assertRaises((ValueError, json.JSONDecodeError)):
                    runner.read_events(path)

    def test_actual_compiled_getter_requires_exact_integer_one_and_no_testmain_output(self):
        runner.validate_getter('{"merchant_store_writer_capability":1}\n')
        for payload in ('', '{"merchant_store_writer_capability":5}', '{"merchant_store_writer_capability":true}',
                        '{"merchant_store_writer_capability":"1"}', '{"merchant_store_writer_capability":1,"extra":0}',
                        'TestMain output\n{"merchant_store_writer_capability":1}', '[1]',
                        '{"merchant_store_writer_capability":1}\n{"merchant_store_writer_capability":1}',
                        '{"merchant_store_writer_capability":2,"merchant_store_writer_capability":1}'):
            with self.subTest(payload=payload), self.assertRaises(ValueError):
                runner.validate_getter(payload)

    def test_local_runner_is_refused_before_any_go_or_database_operation(self):
        with patch.dict(runner.os.environ, {}, clear=True), patch.object(runner.subprocess, 'run') as command:
            with self.assertRaisesRegex(ValueError, 'restricted to Actions'):
                runner.qualify(Path('unused'), Path('unused'))
            command.assert_not_called()

    def test_signature_failure_retains_actual_stderr_and_stage_without_running_go(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            artifacts = root / 'evidence'
            failure = subprocess.CalledProcessError(1, ['git', 'verify-commit', runner.CAP1_REVISION],
                                                    stderr=b'[GNUPG:] BADSIG actual-public-diagnostic\n')
            with patch.dict(runner.os.environ, {'GITHUB_ACTIONS': 'true', 'MERCHANT_STORE_POSTGRES_TEST_DSN': 'never-used'}, clear=True), \
                    patch.object(runner, 'git', return_value=b'candidate\n'), \
                    patch.object(runner, 'verify_ancestor', side_effect=failure), \
                    patch.object(runner.subprocess, 'run') as command:
                with self.assertRaises(subprocess.CalledProcessError):
                    runner.qualify(artifacts, root)
                command.assert_not_called()
            receipt = json.loads((artifacts / 'result.json').read_text())
            self.assertEqual('failed', receipt['status'])
            self.assertEqual('verify-pinned-cap1-source', receipt['failure_stage'])
            self.assertEqual(failure.stderr, (artifacts / 'stage-error.log').read_bytes())

    def test_workflow_preserves_full_checkout_caches_evidence_and_runs_offline_contracts(self):
        source = (runner.REPOSITORY / '.github/workflows/server-release-qualification.yml').read_text()
        go_section = source.split('  go-server:', 1)[1].split('  release-gate:', 1)[0]
        self.assertIn('fetch-depth: 0', go_section)
        self.assertIn('python3 -B scripts/ci/qualify-merchant-store-postgres.py', go_section)
        self.assertIn('MERCHANT_STORE_POSTGRES_TEST_DSN:', go_section)
        self.assertIn('path: qualification-artifacts/go/', go_section)
        self.assertIn('python3 -B scripts/test_qualify_merchant_store_postgres.py', source)
        self.assertNotIn("-run '^TestMerchantStorePostgres(DSNGuard|Concurrency)$'", go_section)


class CapabilityOneBuildSchedulingContract(unittest.TestCase):
    def test_independent_build_is_p_one_runtime_two_and_still_strictly_offline(self):
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode='w') as tar:
            directory = tarfile.TarInfo('apps/api-go/model')
            directory.type = tarfile.DIRTYPE
            tar.addfile(directory)
            payload = b'module fixture\ngo 1.25.1\n'
            member = tarfile.TarInfo('apps/api-go/go.mod')
            member.size = len(payload)
            tar.addfile(member, io.BytesIO(payload))
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / 'source'
            source.mkdir()
            output = root / 'build'
            calls = []

            def fake_compile(command, **kwargs):
                calls.append((command, kwargs['env']))
                (output / 'capability-one-model.test').write_bytes(b'fake executable for offline argument inspection only')
                return type('Result', (), {'returncode': 0})()

            with patch.object(builder, 'reviewed_source', return_value=(archive.getvalue(), {}, 'source-hash')), \
                    patch.object(builder.subprocess, 'run', side_effect=fake_compile), \
                    patch('sys.stdout', new_callable=io.StringIO):
                self.assertEqual(0, builder.main(['--cap1-source-tree', str(source), '--cap1-source-revision', runner.CAP1_REVISION,
                                                '--output-directory', str(output), '--go-jobs', '1', '--go-procs', '2']))
            command, env = calls[0]
            self.assertEqual('1', command[command.index('-p') + 1])
            self.assertEqual(('2', 'off', 'off'), (env['GOMAXPROCS'], env['GOPROXY'], env['GOSUMDB']))
            receipt = json.loads((output / 'receipt.json').read_text())
            self.assertEqual((1, 2), (receipt['go_parallelism'], receipt['go_max_procs']))
            self.assertIn('not official N-1', receipt['kind'])


if __name__ == '__main__':
    unittest.main()
