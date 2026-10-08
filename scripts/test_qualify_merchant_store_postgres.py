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
phase_six = load('merchant_phase_six_pg_ci', 'ci/qualify-merchant-store-phase-six-postgres.py')
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


class PhaseSixSourceWriterEvidenceContract(unittest.TestCase):
    def setUp(self):
        self.events = passes(phase_six.REQUIRED)
        self.plan = {
            'writer_capability': 7,
            'tables': [
                {'name': 'merchant_store_products', 'columns': [
                    {'name': 'category_id', 'size': 36, 'not_null': True}]},
                {'name': 'merchant_store_categories'},
                {'name': 'merchant_store_product_likes'},
            ],
        }
        self.revision = 'a' * 40

    def test_reviewed_writer_seven_preserves_phase_six_evidence_identity(self):
        result = phase_six.validate(self.events, self.plan, self.revision)
        self.assertEqual(7, result['writer_capability'])
        self.assertEqual('merchant-store-phase-six-postgres-v1', result['format'])
        self.assertEqual(list(phase_six.LEAVES), result['required_pg_leaves'])
        self.assertEqual(['merchant_store_categories', 'merchant_store_product_likes',
                          'merchant_store_products.category_id'], result['phase_six_additions'])
        self.assertEqual((0, 0, 'passed'), (result['failed'], result['skipped'], result['status']))

    def test_stale_future_non_integer_and_missing_writer_markers_are_refused(self):
        for marker in (1, 4, 5, 6, 8, 9, True, 7.0, '7', None):
            with self.subTest(marker=marker), self.assertRaises(ValueError):
                phase_six.validate(self.events, {**self.plan, 'writer_capability': marker}, self.revision)
        with self.assertRaises(ValueError):
            phase_six.validate(self.events, {'tables': self.plan['tables']}, self.revision)

    def test_writer_update_never_substitutes_for_actual_pg_parent_and_leaf_passes(self):
        for event in self.events:
            with self.subTest(missing=event.get('Test')), self.assertRaises(ValueError):
                phase_six.validate([row for row in self.events if row != event], self.plan, self.revision)
        for action in ('skip', 'fail'):
            with self.subTest(action=action), self.assertRaises(ValueError):
                phase_six.validate(self.events + [{'Package': phase_six.PACKAGE, 'Action': action}],
                                   self.plan, self.revision)
        with self.assertRaises(ValueError):
            phase_six.validate(self.events + [self.events[0]], self.plan, self.revision)
        with self.assertRaises(ValueError):
            phase_six.validate([*self.events[:-1], {'Action': 'pass', 'Package': 'other/model'}],
                               self.plan, self.revision)


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
