#!/usr/bin/env python3
"""Exercise the actual deploy workflow's gates with offline synthetic artifacts.

Bash, jq, tar and sha256sum run normally. Only GitHub and cosign are test doubles.
These tests prove ordering, contract checks and verifier failure propagation;
they are NOT a Sigstore trust-chain or a production deployment qualification.
"""
from __future__ import annotations

import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = ROOT / '.github/workflows/deploy-web-frontend.yml'
PUBLISHER = ROOT / '.github/workflows/release-web.yml'
VERSION = '0.0.123'
TAG = f'web-v{VERSION}'
ARCHIVE = f'lmm-api-web-{VERSION}.tar.gz'
REVISION = 'a' * 40
REPOSITORY = 'TokenNotIncluded/api.lmm.best'


def step_run(name: str) -> str:
    """Read one literal Bash step; fail if the workflow changes its representation."""
    text = WORKFLOW.read_text()
    marker = f'      - name: {name}\n'
    if text.count(marker) != 1:
        raise AssertionError(f'expected exactly one step: {name}')
    block = text.split(marker, 1)[1].split('\n      - name:', 1)[0]
    body = block.split('        run: |\n', 1)[1]
    lines = body.splitlines()
    if any(line and not line.startswith('          ') for line in lines):
        raise AssertionError(f'unsupported run indentation: {name}')
    return '\n'.join(line[10:] if line else '' for line in lines) + '\n'


# Keep subprocess fixtures independent of site-installed startup hooks.
TOOL = r'''#!/usr/bin/env -S python3 -S
import json, os, pathlib, sys
name = pathlib.Path(sys.argv[0]).name
root = pathlib.Path(os.environ['FIXTURE_ROOT'])
with (root / 'trace').open('a') as output:
    output.write(name + '\n')
args = sys.argv[1:]
if name == 'cosign':
    archive = 'deploy/' + os.environ['TEST_ARCHIVE']
    identity = ('https://github.com/' + os.environ['GITHUB_REPOSITORY'] +
                '/.github/workflows/release-web.yml@refs/tags/' + os.environ['RELEASE_TAG'])
    expected = ['verify-blob', '--bundle', archive + '.sigstore.json',
                '--certificate-identity', identity, '--certificate-oidc-issuer',
                'https://token.actions.githubusercontent.com', archive]
    if args != expected or not pathlib.Path(archive + '.sigstore.json').is_file():
        sys.exit(8)
    sys.exit(int(os.environ.get('COSIGN_RESULT', '0')))
elif name == 'gh':
    prefix = 'repos/' + os.environ['GITHUB_REPOSITORY']
    tag = os.environ['RELEASE_TAG']
    if args == ['api', prefix + '/releases/tags/' + tag, '--jq', '.id']:
        print('42')
    elif len(args) == 2 and args[0] == 'api' and args[1].startswith(prefix + '/releases/42?fresh='):
        print((root / 'release.json').read_text())
    elif args == ['api', prefix + '/commits/' + tag, '--jq', '.sha']:
        print((root / 'tag-sha').read_text().strip())
    else:
        sys.exit('unexpected GitHub request: ' + repr(args))
elif name == 'tar':
    os.execv(os.environ['REAL_TAR'], [os.environ['REAL_TAR'], *args])
else:
    sys.exit('network or production command is forbidden in this fixture')
'''


class WebDeployContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        for tool in ('bash', 'jq', 'tar', 'sha256sum', 'cmp'):
            if not shutil.which(tool):
                raise RuntimeError(f'required local test tool is missing: {tool}')
        cls.signature = step_run('Verify release signature')
        cls.contract = step_run('Validate verified release contract')

    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory(prefix='lmm-web-contract-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.deploy = self.root / 'deploy'
        self.deploy.mkdir()
        tools = self.root / 'bin'
        tools.mkdir()
        for name in ('cosign', 'gh', 'tar', 'curl', 'ssh'):
            path = tools / name
            path.write_text(TOOL)
            path.chmod(0o700)
        self.env = {
            'PATH': str(tools) + os.pathsep + os.environ.get('PATH', '/usr/bin:/bin'),
            'HOME': str(self.root),
            'LANG': 'C', 'LC_ALL': 'C',
            'FIXTURE_ROOT': str(self.root),
            'REAL_TAR': shutil.which('tar'),
            'GITHUB_SERVER_URL': 'https://github.com',
            'GITHUB_REPOSITORY': REPOSITORY,
            'GITHUB_RUN_ID': '123',
            'RELEASE_TAG': TAG, 'VERSION': VERSION,
            'TEST_ARCHIVE': ARCHIVE,
            'KEY_MARKER': str(self.root / 'key-step-reached'),
        }
        self.members = {
            'dist/index.html': b'<html>synthetic</html>',
            'REVISION': (REVISION + '\n').encode(),
            'API_ROUTE_CONTRACT_REVISION': ('b' * 64 + '\n').encode(),
            'lmm-api-web.install': b'# synthetic install hook\n',
            'LICENSE': b'synthetic license',
            'NOTICE': b'synthetic notice',
            'THIRD-PARTY-LICENSES.md': b'synthetic notices',
            'dist/legal/terms.html': b'terms',
            'dist/legal/privacy.html': b'privacy',
            'dist/legal/forge-legal.css': b'body {}',
        }
        (self.deploy / (ARCHIVE + '.sigstore.json')).write_text('synthetic verifier fixture\n')
        (self.root / 'tag-sha').write_text(REVISION)
        self.write_archive()

    def write_archive(self, duplicate: str | None = None) -> None:
        with tarfile.open(self.deploy / ARCHIVE, 'w:gz') as archive:
            for name, body in self.members.items():
                info = tarfile.TarInfo(name)
                info.size = len(body)
                archive.addfile(info, io.BytesIO(body))
                if name == duplicate:
                    archive.addfile(info, io.BytesIO(body))
        digest = hashlib.sha256((self.deploy / ARCHIVE).read_bytes()).hexdigest()
        (self.deploy / (ARCHIVE + '.sha256')).write_text(f'{digest}  {ARCHIVE}\n')
        self.refresh_release()

    def refresh_release(self) -> None:
        self.release = {
            'id': 42, 'tag_name': TAG, 'target_commitish': REVISION,
            'name': f'LMM web {TAG}', 'draft': False, 'prerelease': False,
            'assets': [
                {'name': name, 'digest': 'sha256:' + hashlib.sha256((self.deploy / name).read_bytes()).hexdigest()}
                for name in (ARCHIVE, ARCHIVE + '.sha256', ARCHIVE + '.sigstore.json')
            ],
        }
        self.save_release()

    def save_release(self) -> None:
        (self.root / 'release.json').write_text(json.dumps(self.release))

    def run_gates(self, *, signature: str | None = None) -> subprocess.CompletedProcess[str]:
        (self.root / 'key-step-reached').unlink(missing_ok=True)
        (self.root / 'trace').write_text('')
        script = (signature if signature is not None else self.signature) + self.contract
        script += '\nprintf "reached\\n" > "$KEY_MARKER"\n'
        return subprocess.run(['bash', '-c', script], cwd=self.root, env=self.env,
                              text=True, capture_output=True, timeout=15)

    def assert_rejected(self, result: subprocess.CompletedProcess[str]) -> None:
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.root / 'key-step-reached').exists())

    def test_exact_contract_passes(self) -> None:
        result = self.run_gates()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.root / 'key-step-reached').exists())
        trace = (self.root / 'trace').read_text().splitlines()
        self.assertEqual(trace[0], 'cosign')
        self.assertIn('tar', trace)

    def test_verifier_failure_stops_before_metadata_or_archive_parsing(self) -> None:
        self.env['COSIGN_RESULT'] = '1'
        self.assert_rejected(self.run_gates())
        self.assertEqual((self.root / 'trace').read_text(), 'cosign\n')

    def test_missing_bundle_is_not_accepted(self) -> None:
        (self.deploy / (ARCHIVE + '.sigstore.json')).unlink()
        self.assert_rejected(self.run_gates())
        self.assertEqual((self.root / 'trace').read_text(), 'cosign\n')

    def test_identity_and_issuer_are_exact(self) -> None:
        for old, new in (('release-web.yml@refs/tags/', 'release-go.yml@refs/tags/'),
                         ('https://token.actions.githubusercontent.com', 'https://issuer.invalid')):
            with self.subTest(new=new):
                self.assert_rejected(self.run_gates(signature=self.signature.replace(old, new)))

    def test_release_metadata_mismatch_is_rejected(self) -> None:
        for key, value in (('tag_name', 'web-v0.0.999'), ('target_commitish', 'main'),
                           ('target_commitish', 'c' * 40), ('name', 'other release'),
                           ('draft', True), ('prerelease', True), ('draft', 'false')):
            with self.subTest(key=key, value=value):
                self.refresh_release()
                self.release[key] = value
                self.save_release()
                self.assert_rejected(self.run_gates())

    def test_retargeted_tag_is_rejected(self) -> None:
        (self.root / 'tag-sha').write_text('c' * 40)
        self.assert_rejected(self.run_gates())

    def test_missing_extra_or_duplicate_release_asset_is_rejected(self) -> None:
        for mode in ('missing', 'extra', 'duplicate'):
            with self.subTest(mode=mode):
                self.refresh_release()
                if mode == 'missing':
                    self.release['assets'].pop()
                else:
                    asset = dict(self.release['assets'][0])
                    if mode == 'extra':
                        asset['name'] = 'unexpected.txt'
                    self.release['assets'].append(asset)
                self.save_release()
                self.assert_rejected(self.run_gates())

    def test_each_download_must_match_published_digest(self) -> None:
        for index in range(3):
            with self.subTest(index=index):
                self.refresh_release()
                self.release['assets'][index]['digest'] = 'sha256:' + '0' * 64
                self.save_release()
                self.assert_rejected(self.run_gates())

    def test_checksum_is_exactly_one_canonical_record(self) -> None:
        path = self.deploy / (ARCHIVE + '.sha256')
        original = path.read_text()
        for body in ('', original.rstrip('\n'), original + original,
                     original.replace(ARCHIVE, '../other-file'), '0' * 64 + f'  {ARCHIVE}\n'):
            with self.subTest(body=repr(body)):
                path.write_text(body)
                self.refresh_release()
                self.assert_rejected(self.run_gates())

    def test_signed_revision_must_match_release_commit(self) -> None:
        self.members['REVISION'] = ('c' * 40 + '\n').encode()
        self.write_archive()
        self.assert_rejected(self.run_gates())

    def test_required_archive_members_are_unique(self) -> None:
        for member in ('REVISION', 'dist/index.html', 'dist/legal/privacy.html'):
            with self.subTest(member=member, mode='duplicate'):
                self.write_archive(duplicate=member)
                self.assert_rejected(self.run_gates())
            with self.subTest(member=member, mode='missing'):
                body = self.members.pop(member)
                self.write_archive()
                self.assert_rejected(self.run_gates())
                self.members[member] = body

    def test_route_contract_hash_must_be_well_formed(self) -> None:
        self.members['API_ROUTE_CONTRACT_REVISION'] = b'not-a-contract-hash\n'
        self.write_archive()
        self.assert_rejected(self.run_gates())

    def test_manual_trigger_and_before_key_order_are_preserved(self) -> None:
        source = WORKFLOW.read_text()
        self.assertIn('  workflow_dispatch:\n', source)
        self.assertNotIn('  workflow_run:\n', source)
        names = ['Download signed release archive', 'Install cosign',
                 'Verify release signature', 'Validate verified release contract',
                 'Install restricted deploy key', 'Publish to both production origins']
        positions = [source.index(f'      - name: {name}\n') for name in names]
        self.assertEqual(positions, sorted(positions))
        download = step_run(names[0])
        self.assertNotIn('tar -', download)
        self.assertNotIn('sha256sum --check', download)
        self.assertNotIn('--insecure-ignore', self.signature)

    def test_publisher_and_consumer_use_same_signed_bundle_contract(self) -> None:
        publisher = PUBLISHER.read_text()
        for suffix in ('tar.gz', 'tar.gz.sha256', 'tar.gz.sigstore.json'):
            self.assertIn(f'"lmm-api-web-${{version}}.{suffix}"', publisher)
        self.assertIn('cosign sign-blob --yes --bundle "${archive}.sigstore.json" "$archive"', publisher)
        self.assertIn('release-web.yml@refs/tags/${GITHUB_REF_NAME}', publisher)
        self.assertIn('release-web.yml@refs/tags/${RELEASE_TAG}', self.signature)
        self.assertIn("'https://token.actions.githubusercontent.com'", publisher)
        self.assertIn("'https://token.actions.githubusercontent.com'", self.signature)


if __name__ == '__main__':
    unittest.main()
