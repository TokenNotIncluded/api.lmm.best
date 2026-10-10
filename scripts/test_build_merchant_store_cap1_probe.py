import contextlib
import importlib.util
import io
from pathlib import Path
import subprocess
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('cap1_probe_builder', Path(__file__).with_name('build-merchant-store-cap1-probe.py'))
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)


class CapabilityOneProbeSourceBoundary(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.source = Path(self.temporary.name) / 'source'
        self.source.mkdir()
        subprocess.run(['git', 'init', '-q', str(self.source)], check=True)
        self.git('config', 'user.name', 'Local fixture')
        self.git('config', 'user.email', 'fixture@example.test')
        self.git('config', 'commit.gpgsign', 'false')
        self.gate = self.source / builder.GATE_SOURCE
        self.gate.parent.mkdir(parents=True)
        self.gate.write_text('package model\nconst MerchantStoreWriterCapability = 1\n')
        (self.source/'apps/api-go/go.mod').write_text('module fixture\ngo 1.25.1\n')
        self.commit()

    def git(self, *args):
        return subprocess.check_output(['git', '-C', str(self.source), *args], stderr=subprocess.PIPE).decode().strip()

    def commit(self):
        self.git('add', '.')
        self.git('commit', '-qm', 'local fixture')
        self.revision = self.git('rev-parse', 'HEAD')

    def test_missing_source_or_revision_is_rejected_without_build(self):
        for args in ([], ['--cap1-source-tree', str(self.source)]):
            with self.subTest(args=args), contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as result:
                builder.main(args)
            self.assertEqual(2, result.exception.code)

    def test_capability_two_is_rejected_and_source_is_not_rewritten(self):
        self.gate.write_text('package model\nconst MerchantStoreWriterCapability = 2\n')
        self.commit()
        before = self.gate.read_bytes()
        with self.assertRaisesRegex(ValueError, 'exact capability 1'):
            builder.reviewed_source(self.source, self.revision)
        self.assertEqual(before, self.gate.read_bytes())

    def test_wrong_revision_is_rejected(self):
        with self.assertRaisesRegex(ValueError, 'differs from the reviewed revision'):
            builder.reviewed_source(self.source, '0'*40)

    def test_assume_unchanged_cannot_hide_source_tampering(self):
        self.git('update-index', '--assume-unchanged', builder.GATE_SOURCE)
        self.gate.write_text('package model\nconst MerchantStoreWriterCapability = 2\n')
        self.assertEqual('', self.git('status', '--porcelain'))
        with self.assertRaisesRegex(ValueError, 'differs from reviewed Git content'):
            builder.reviewed_source(self.source, self.revision)

    def test_valid_exact_source_validation_never_runs_go(self):
        before = self.gate.read_bytes()
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            self.assertEqual(0, builder.main(['--cap1-source-tree', str(self.source), '--cap1-source-revision', self.revision, '--validate-only']))
        self.assertIn('source-validated-no-build', output.getvalue())
        self.assertEqual(before, self.gate.read_bytes())
        self.assertEqual('', self.git('status', '--porcelain'))


if __name__ == '__main__':
    unittest.main()
