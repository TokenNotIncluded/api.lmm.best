"""Run package() into a temporary directory; this is not a makepkg build.

Signature verification is tested separately. No package is installed and no
service or network is used here. Fixtures model an already-extracted archive.
"""
from pathlib import Path
import os
import re
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
SHARED = ROOT / 'packaging/common/lmm-api'
RECIPE = ROOT / 'packaging/aur/lmm-api-go-bin/PKGBUILD'


class DeploymentPayloadTests(unittest.TestCase):
    def package(self, modern):
        temp = tempfile.TemporaryDirectory(prefix='lmm package payload ')
        self.addCleanup(temp.cleanup)
        root = Path(temp.name)
        src = root / 'src'
        src.mkdir()
        version = '0.2.0' if modern else '0.1.69'
        name = f'lmm-api-go-{version}-linux-amd64'
        bundle = src / name
        bundle.mkdir()
        backend = bundle / ('lmm-api-go' if modern else 'lmm-api')
        backend.write_text('#!/bin/sh\nexit 0\n')
        backend.chmod(0o755)
        for filename in ('lmm-api.service', 'lmm-api-go.env', 'lmm-api-memory.conf',
                         'lmm-api-operator.sysusers', 'lmm-api-operator.tmpfiles',
                         'lmm-api-operator.sudoers'):
            shutil.copyfile(SHARED / filename, bundle / filename)
        for filename in ('LICENSE', 'NOTICE', 'THIRD-PARTY-LICENSES.md', 'REVISION'):
            (bundle / filename).write_text('fixture\n')
        (bundle / 'API_ROUTE_CONTRACT_REVISION').write_text('a' * 64 + '\n')
        (bundle / 'edge-policy').mkdir()
        (bundle / 'edge-policy/fixture').write_text('fixture\n')
        (src / (name + '.tar.gz')).write_bytes(b'fixture archive identity')
        if modern:
            shutil.copyfile(SHARED / 'lmm-api-deploy', bundle / 'lmm-api-deploy')
            (bundle / 'lmm-api-deploy-engine').write_text('#!/bin/sh\necho separate-tool\n')
        else:
            (bundle / 'CLI_TRANSITION_PHASE').write_text('t0\n')
        recipe = root / 'PKGBUILD'
        recipe.write_text(re.sub(r'^pkgver=.*$', 'pkgver=' + version, RECIPE.read_text(), flags=re.MULTILINE))
        output = root / 'pkg'
        result = subprocess.run(['bash', '-euc', 'source "$1"; package', '_', str(recipe)],
                                env=dict(os.environ, startdir=str(SHARED), srcdir=str(src),
                                         pkgdir=str(output), CARCH='x86_64'),
                                cwd=src, text=True, capture_output=True, timeout=15)
        self.assertEqual(0, result.returncode, result.stderr)
        return output, bundle

    def test_modern_package_keeps_distinct_server_tool_and_fixed_script(self):
        output, source = self.package(True)
        self.assertEqual((source / 'lmm-api-go').read_bytes(), (output / 'usr/bin/lmm-api-go').read_bytes())
        tool = output / 'usr/lib/lmm-api-deploy/engine'
        self.assertEqual((source / 'lmm-api-deploy-engine').read_bytes(), tool.read_bytes())
        self.assertEqual(0o755, tool.stat().st_mode & 0o777)
        self.assertIn('exec /usr/lib/lmm-api-deploy/engine "$@"', (output / 'usr/bin/lmm-api-deploy').read_text())
        self.assertFalse((output / 'usr/bin/lmm-api').exists())

    def test_legacy_rollback_is_not_contaminated_with_current_tool_or_script(self):
        output, source = self.package(False)
        self.assertEqual((source / 'lmm-api').read_bytes(), (output / 'usr/bin/lmm-api').read_bytes())
        self.assertEqual('lmm-api', os.readlink(output / 'usr/bin/lmm-api-go'))
        self.assertFalse((output / 'usr/bin/lmm-api-deploy').exists())
        self.assertFalse((output / 'usr/lib/lmm-api-deploy/engine').exists())


if __name__ == '__main__':
    unittest.main()
