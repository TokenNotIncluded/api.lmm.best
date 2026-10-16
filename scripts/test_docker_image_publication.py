#!/usr/bin/env python3
"""Offline source contracts and MOCKED Docker orchestration, never GHCR acceptance.

Run: python3 -B scripts/test_docker_image_publication.py -v
Real smoke (already-built, labelled linux/amd64 runtime only):
  bash scripts/smoke-runtime-image.sh core IMAGE PRIVATE_EVIDENCE_DIR
  bash scripts/smoke-runtime-image.sh extensions IMAGE PRIVATE_EVIDENCE_DIR
Requirements for real smoke: local Docker daemon/CLI, Python3, bash, sha256sum;
image must contain sh, curl, coreutils and the existing component entrypoint.
Formal build/publication is exclusively manual microservice-images.yml, on WIP
or an exact-SHA preview tag proven to be an ancestor of WIP.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import textwrap
import unittest

ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = ROOT / '.github/workflows/microservice-images.yml'
SMOKE = ROOT / 'scripts/smoke-runtime-image.sh'
CHECKOUT = '9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0'
UPLOAD = '043fb46d1a93c77aae656e7c1c64a875d1fc6a0a'
DOWNLOAD = 'd3f86a106a0bac45b974a628896c90dbdf5c8093'


class SourceContracts(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workflow = WORKFLOW.read_text()
        cls.smoke = SMOKE.read_text()
        cls.build, cls.publish = cls.workflow.split('  publish:\n', 1)

    def test_manual_only_and_wip_repository_guard(self):
        self.assertRegex(self.workflow, r'^name: [^\n]+\non:\n  workflow_dispatch:\npermissions:')
        for trigger in ('workflow_call:', 'push:', 'pull_request:', 'schedule:'):
            self.assertNotIn(trigger, self.workflow)
        self.assertIn("github.repository == 'TokenNotIncluded/lmm-api'", self.build)
        self.assertIn("github.ref == 'refs/heads/wip/rust-core-go-extensions'", self.build)
        self.assertIn("startsWith(github.ref, 'refs/tags/microservice-preview-')", self.build)
        self.assertIn('test "$GITHUB_REF_NAME" = "microservice-preview-$GITHUB_SHA"', self.build)
        self.assertIn('git merge-base --is-ancestor "$GITHUB_SHA" refs/remotes/origin/wip/rust-core-go-extensions', self.build)
        self.assertIn('fetch-depth: 0', self.build)
        self.assertIn('ref: ${{ github.sha }}', self.build)
        self.assertIn('persist-credentials: false', self.workflow)
        self.assertIn('cancel-in-progress: false', self.workflow)

    def test_two_independent_runtime_components_only(self):
        self.assertEqual(self.workflow.count('component: [core, extensions]'), 2)
        self.assertIn('ghcr.io/tokennotincluded/lmm-${{ matrix.component }}', self.workflow)
        self.assertIn('--platform linux/amd64 --target runtime --load', self.build)
        self.assertIn('deployment/docker/${COMPONENT}.Dockerfile', self.build)
        self.assertNotRegex(self.publish, r'docker (?:build\b|buildx build\b)')
        self.assertNotIn(':latest', self.workflow)
        self.assertNotRegex(self.workflow, r'(?m)^\s+(?:ssh|scp|docker compose|kubectl|helm)\s')
        self.assertNotIn('apps/cli', self.workflow)
        self.assertNotIn('microservice-distribution', self.workflow)

    def test_publication_permissions_and_all_builds_gate(self):
        self.assertNotIn('packages: write', self.build)
        self.assertEqual(self.workflow.count('packages: write'), 1)
        self.assertIn('needs: build', self.publish)
        self.assertNotIn('if: always()\n    runs-on:', self.publish)
        self.assertNotIn('GHCR_TOKEN', self.build)
        self.assertEqual(self.workflow.count('GHCR_TOKEN: ${{ github.token }}'), 1)
        self.assertIn('--password-stdin', self.publish)
        self.assertNotIn('secrets.', self.workflow)
        self.assertNotIn('set -x', self.workflow)
        self.assertIn('rm -rf -- "${DOCKER_CONFIG:?}"', self.publish)

    def test_all_action_pins_reviewed_and_exact(self):
        uses = re.findall(r'uses: ([^\s]+)', self.workflow)
        allowed = {f'actions/checkout@{CHECKOUT}', f'actions/upload-artifact@{UPLOAD}',
                   f'actions/download-artifact@{DOWNLOAD}'}
        self.assertEqual(set(uses), allowed)
        for action in uses:
            self.assertRegex(action, r'@[0-9a-f]{40}$')

    def test_tested_image_staging_not_rebuild(self):
        self.assertLess(self.build.index('bash scripts/smoke-runtime-image.sh'),
                        self.build.index('docker image save'))
        self.assertIn('sha256sum --check image.tar.gz.sha256', self.publish)
        self.assertIn('gzip -dc artifacts/image.tar.gz | docker image load', self.publish)
        self.assertIn("image['Id'] == record['image_id'] == record['smoke']['image_id']", self.publish)
        self.assertIn("record['smoke']['smoke'] == 'passed'", self.publish)
        self.assertIn("manifest['config']['digest'] == record['image_id']", self.publish)
        self.assertIn("len(set(digests.values())) == 1", self.publish)
        self.assertIn('Refusing to replace existing immutable preview tag', self.publish)
        self.assertIn('Registry preflight failed; no publication attempted', self.publish)
        self.assertIn('preview-$(git rev-parse --short=12 HEAD)', self.build)
        self.assertIn('sha-$SOURCE_REVISION', self.build)
        self.assertIn('org.opencontainers.image.source=https://github.com/$GITHUB_REPOSITORY', self.build)
        self.assertIn('org.opencontainers.image.revision=$SOURCE_REVISION', self.build)
        self.assertIn('unsigned workflow build/source summary', self.build)
        self.assertIn("'input_sha256': hashes", self.build)
        self.assertIn("'pushed_digests'", self.publish)
        self.assertIn('ghcr-publication-', self.publish)

    def test_formal_toolchains_and_bounded_core_build(self):
        core = (ROOT / 'deployment/docker/core.Dockerfile').read_text()
        extensions = (ROOT / 'deployment/docker/extensions.Dockerfile').read_text()
        self.assertRegex(core, r'(?m)^FROM rust:1\.99\.0-bookworm@sha256:[0-9a-f]{64} AS build$')
        self.assertIn('cargo build --locked --release --jobs 2 --bin lmm-core', core)
        self.assertRegex(extensions, r'(?m)^FROM golang:1\.27\.2-bookworm@sha256:[0-9a-f]{64} AS build$')
        self.assertIn('./cmd/extensions', extensions)
        for dockerfile in (core, extensions):
            self.assertRegex(dockerfile, r'(?m)^FROM debian:bookworm-slim@sha256:[0-9a-f]{64} AS runtime$')
            self.assertIn('USER 65532:65532', dockerfile)
            self.assertIn('COPY LICENSE NOTICE THIRD-PARTY-LICENSES.md', dockerfile)
            self.assertNotRegex(dockerfile, r'COPY\s+\.\s')
            self.assertNotIn('secrets', dockerfile)
        self.assertIn('COPY config/legal/site.example.toml', extensions)
        self.assertIn('COPY config/legal/templates/', extensions)
        self.assertEqual(len(list((ROOT / 'config/legal/templates').rglob('*.md'))), 6)

    def test_offline_private_smoke_and_scoped_cleanup(self):
        for arg in ('--network none', '--user 65532:65532', '--read-only', '--cap-drop ALL',
                    '--security-opt no-new-privileges', '--tmpfs /tmp:', '--pids-limit 64'):
            self.assertIn(arg, self.smoke)
        self.assertIn('umask 077', self.smoke)
        self.assertIn('chmod 0400 /tmp/image-smoke/host-token', self.smoke)
        self.assertIn('--config /tmp/image-smoke/runtime.toml', self.smoke)
        self.assertIn('modules = [\\"none\\"]', self.smoke)
        self.assertIn('sha256sum "/usr/share/licenses/lmm/$file"', self.smoke)
        self.assertIn('config/legal/site.example.toml', self.smoke)
        self.assertIn('config/legal/templates', self.smoke)
        self.assertIn('http://127.0.0.1:$port/health/live', self.smoke)
        self.assertIn("'business_acceptance': False", self.smoke)
        self.assertIn('docker rm -f "$cid"', self.smoke)
        for unsafe in ('prune', 'volume rm', 'network rm', 'init-db', '--legacy-env', '--privileged', '--mount', '--publish'):
            self.assertNotIn(unsafe, self.smoke)
        self.assertNotIn('test -s /usr/share/lmm/legal/site.example.json', self.smoke)
        self.assertIn('test ! -e /usr/share/lmm/legal/site.example.json', self.smoke)
        subprocess.run(['bash', '-n', str(SMOKE)], check=True)

    def test_container_startup_shell_parses(self):
        startup, = re.findall(r"startup='(.*?)'\ncid=", self.smoke.replace(
            '# Capture ownership before start, so even a failed start is cleaned up precisely.\n', ''), re.S)
        result = subprocess.run(['/bin/sh', '-n'], input=startup, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_workflow_shell_blocks_parse(self):
        scripts = re.findall(r'        run: \|\n(.*?)(?=\n      -|\Z)', self.workflow, re.S)
        self.assertEqual(len(scripts), 8)
        for script in scripts:
            script = textwrap.dedent(script)
            result = subprocess.run(['bash', '-n'], input=script, text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_embedded_python_compiles(self):
        snippets = re.findall(r"          python3 - <<'PY'\n(.*?)\n          PY", self.workflow, re.S)
        self.assertEqual(len(snippets), 3)
        for snippet in snippets:
            compile(textwrap.dedent(snippet), str(WORKFLOW), 'exec')
        snippets = re.findall(r"<<'PY'\n(.*?)\nPY", self.smoke, re.S)
        self.assertEqual(len(snippets), 2)
        for snippet in snippets:
            compile(snippet, str(SMOKE), 'exec')


# Deliberately MOCKED Docker: exercises fail-closed orchestration and cleanup only.
# It does not execute a container or application and cannot prove actual liveness.
MOCK_DOCKER = r'''#!/usr/bin/env python3
import hashlib, json, os, pathlib, sys
args = sys.argv[1:]
with open(os.environ['MOCK_CALLS'], 'a') as f:
    f.write(json.dumps(args) + '\n')
component = os.environ['MOCK_COMPONENT']
mode = os.environ.get('MOCK_MODE', '')
image_id = 'sha256:' + 'a' * 64
if args[:2] == ['image', 'inspect']:
    user = '0' if mode == 'root' else '65532:65532'
    print(json.dumps([{'Id': image_id, 'Os': 'linux',
      'Architecture': 'arm64' if mode == 'arch' else 'amd64',
      'Config': {'User': user, 'Entrypoint': ['/usr/bin/lmm-' + component],
                 'Cmd': ['--config', '/etc/lmm.conf.d/' + component + '.toml'],
                 'Healthcheck': {'Test': ['CMD-SHELL', 'curl /health/live']}}}]))
elif args[0] == 'create':
    print('only-mocked-owned-container')
elif args[0] == 'start':
    if mode == 'start': sys.exit(1)
elif args[0] == 'inspect':
    if '--format' in args:
        print(('false' if mode in ('health', 'redirect') else 'true') if 'Running' in args[2] else ('1' if mode == 'exit' else '0'))
    else:
        print(json.dumps([{'Config': {'User': '65532:65532'}, 'Mounts': [],
          'HostConfig': {'NetworkMode': 'none', 'ReadonlyRootfs': True,
                        'CapDrop': ['ALL'], 'SecurityOpt': ['no-new-privileges']}}]))
elif args[0] == 'exec':
    command = args[2:]
    if command[:2] == ['id', '-u']:
        print('65532')
    elif command[0] == 'sha256sum':
        target = command[1]
        if '/licenses/lmm/' in target:
            source = pathlib.Path(os.environ['MOCK_ROOT']) / target.rsplit('/', 1)[-1]
        else:
            source = pathlib.Path(os.environ['MOCK_ROOT']) / 'config/legal' / target.split('/usr/share/lmm/legal/')[1]
        digest = hashlib.sha256(source.read_bytes()).hexdigest()
        print(('b' * 64 if mode == 'license' else digest) + '  ' + target)
    elif command[0] == 'curl':
        if mode == 'health': sys.exit(22)
        print('{"mock_only":true}\n' + ('302' if mode == 'redirect' else '200'))
    elif command[0] == '/bin/sh':
        if mode == 'resources' and 'site.example.json' in command[-1]: sys.exit(1)
    else:
        sys.exit('unexpected mocked exec: ' + repr(command))
elif args[0] in ('stop', 'rm', 'logs'):
    pass
else:
    sys.exit('unexpected mocked Docker call: ' + repr(args))
'''


class MockedOrchestration(unittest.TestCase):
    def run_mock(self, component='core', mode=''):
        with tempfile.TemporaryDirectory(prefix='ghcr-smoke-mock-') as directory:
            directory = Path(directory)
            binary = directory / 'docker'
            binary.write_text(MOCK_DOCKER)
            binary.chmod(0o700)
            calls_file = directory / 'calls.jsonl'
            evidence = directory / 'evidence'
            env = dict(os.environ, PATH=str(directory) + os.pathsep + os.environ['PATH'],
                       MOCK_ROOT=str(ROOT), MOCK_COMPONENT=component,
                       MOCK_MODE=mode, MOCK_CALLS=str(calls_file))
            result = subprocess.run(['bash', str(SMOKE), component, 'mock-only-image', str(evidence)],
                                    env=env, capture_output=True, text=True, timeout=15)
            calls = [json.loads(line) for line in calls_file.read_text().splitlines()]
            record = json.loads((evidence / 'smoke-result.json').read_text()) if (evidence / 'smoke-result.json').exists() else None
            return result, calls, record

    def test_invalid_invocation_does_not_call_docker(self):
        result = subprocess.run(['bash', str(SMOKE)], capture_output=True, text=True)
        self.assertEqual(result.returncode, 2)
        self.assertIn('Usage:', result.stderr)

    def test_stale_evidence_is_refused_before_docker(self):
        with tempfile.TemporaryDirectory(prefix='ghcr-smoke-stale-') as directory:
            (Path(directory) / 'smoke-result.json').write_text('{"old_result":true}')
            result = subprocess.run(['bash', str(SMOKE), 'core', 'not-a-real-image', directory],
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 2)
            self.assertIn('refusing stale smoke results', result.stderr)

    def test_mock_core_orchestration_only(self):
        result, calls, record = self.run_mock()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(record['business_acceptance'])
        self.assertFalse(record['database_used'])
        run, = [c for c in calls if c[0] == 'create']
        self.assertEqual(run[run.index('--network') + 1], 'none')
        self.assertEqual(run[run.index('--user') + 1], '65532:65532')
        self.assertIn('--read-only', run)
        self.assertIn('--config /tmp/image-smoke/runtime.toml', run[-1])
        self.assertIn(['rm', '-f', 'only-mocked-owned-container'], calls)
        self.assertIn(['stop', '--time', '10', 'only-mocked-owned-container'], calls)

    def test_mock_extensions_resource_orchestration_only(self):
        result, calls, record = self.run_mock('extensions')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(record['component'], 'extensions')
        self.assertEqual(sum(c[:3] == ['exec', 'only-mocked-owned-container', 'sha256sum'] for c in calls), 10)
        self.assertTrue(any(c[0] == 'exec' and '/health/live' in c[-1] and ':8081/' in c[-1] for c in calls))

    def test_mock_invalid_image_fails_before_container(self):
        for mode in ('root', 'arch'):
            with self.subTest(mode=mode):
                result, calls, record = self.run_mock(mode=mode)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(c[0] == 'create' for c in calls))
                self.assertIsNone(record)

    def test_mock_runtime_failures_preserve_cleanup_and_never_mark_pass(self):
        for component, mode in (('core', 'start'), ('core', 'license'), ('core', 'health'),
                                ('core', 'redirect'), ('core', 'exit'),
                                ('extensions', 'resources')):
            with self.subTest(component=component, mode=mode):
                result, calls, record = self.run_mock(component, mode)
                self.assertNotEqual(result.returncode, 0)
                self.assertIsNone(record)
                self.assertIn(['rm', '-f', 'only-mocked-owned-container'], calls)
                self.assertIn(['logs', 'only-mocked-owned-container'], calls)


if __name__ == '__main__':
    unittest.main()
