"""Test workstation commands and artifact downloads without production access."""
from collections import Counter
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import hashlib
import io
import os
import shutil
import subprocess
import tarfile
import tempfile
import textwrap
import threading
import unittest

ROOT = Path(__file__).resolve().parent.parent
ENTRYPOINT = ROOT / 'scripts/lmm-api-deploy.sh'
WORKFLOW = ROOT / '.github/workflows/deploy-web-frontend.yml'
REPO = 'TokenNotIncluded/api.lmm.best'
TAG = 'web-v1.2.3'
REVISION = 'a' * 40
ARCHIVE = 'lmm-api-web-1.2.3.tar.gz'


def workflow_step(name):
    """Read a literal shell step; no workflow expression is evaluated by tests."""
    step = WORKFLOW.read_text().split('      - name: ' + name + '\n', 1)[1]
    step = step.split('\n      - name: ', 1)[0]
    return textwrap.dedent(step.split('        run: |\n', 1)[1])


class EntrypointTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix='lmm deployment ')
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.script = self.root / 'scripts/lmm-api-deploy.sh'
        self.script.parent.mkdir()
        shutil.copyfile(ENTRYPOINT, self.script)
        self.bin = self.root / 'tools'
        self.bin.mkdir()
        self.log = self.root / 'commands.bin'
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ['PATH'],
                        COMMAND_LOG=str(self.log), FAKE_EXIT='0')
        for key in ('LMM_API_DEPLOY_BINARY', 'LMM_API_BUILD_WORKSPACE',
                    'LMM_API_GITHUB_REPOSITORY'):
            self.env.pop(key, None)
        self.fake = textwrap.dedent('''\
            #!/bin/bash
            set -eu
            name=${0##*/}
            printf '%s\\0' "$(( $# + 1 ))" "$name" "$@" >> "$COMMAND_LOG"
            if [[ $name == bun && $FAKE_EXIT == 0 ]]; then
                target=$PWD/apps/api-go/out/lmm-api-deploy-engine
                mkdir -p -- "${target%/*}"
                cp -- "$0" "$target"
                chmod +x -- "$target"
            fi
            exit "$FAKE_EXIT"
        ''')
        for tool in ('gh', 'bun', 'python3'):
            self.make_tool(self.bin / tool)

    def make_tool(self, path):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(self.fake)
        path.chmod(0o755)

    def call(self, *args):
        return subprocess.run(['bash', str(self.script), *args], env=self.env,
                              capture_output=True, text=True, timeout=10)

    def calls(self):
        if not self.log.exists():
            return []
        # Counted NUL-separated records preserve empty arguments and whitespace.
        fields = self.log.read_bytes().split(b'\0')
        self.assertEqual(b'', fields.pop())
        commands = []
        while fields:
            count = int(fields.pop(0))
            self.assertGreaterEqual(len(fields), count)
            commands.append([os.fsdecode(value) for value in fields[:count]])
            del fields[:count]
        return commands

    def test_help_never_needs_provider_or_authentication(self):
        self.env['LMM_API_DEPLOY_BINARY'] = '/missing/provider'
        for args in ((), ('--help',), ('web', '--help')):
            with self.subTest(args=args):
                result = self.call(*args)
                self.assertEqual(0, result.returncode, result.stderr)
                self.assertIn('web', result.stdout)
        self.assertEqual([], self.calls())

    def test_web_commands_preserve_arguments_and_fail_without_retry(self):
        dispatch = ['workflow', 'run', 'deploy-web-frontend.yml', '--ref', 'main',
                    '--raw-field', 'release_tag=' + TAG]
        cases = (
            (('deploy', TAG), dispatch, 0, None),
            (('deploy', TAG), dispatch, 0, 'example/instance'),
            (('deploy', TAG), dispatch, 4, None),
            (('status', '123'), ['run', 'view', '123', '--exit-status'], 1, None),
            (('watch', '123'), ['run', 'watch', '123', '--exit-status', '--interval', '10'], 1, None),
            (('list',), ['run', 'list', '--workflow', 'deploy-web-frontend.yml',
                        '--event', 'workflow_dispatch', '--limit', '10'], 0, None),
        )
        self.env['LMM_API_DEPLOY_BINARY'] = '/missing/provider'
        for args, command, code, repository in cases:
            with self.subTest(args=args, code=code, repository=repository):
                self.log.unlink(missing_ok=True)
                self.env['FAKE_EXIT'] = str(code)
                self.env.pop('LMM_API_GITHUB_REPOSITORY', None)
                if repository is not None:
                    self.env['LMM_API_GITHUB_REPOSITORY'] = repository
                result = self.call('web', *args)
                self.assertEqual(code, result.returncode, result.stderr)
                self.assertEqual([['gh', *command, '--repo', repository or REPO]], self.calls())

    def test_bad_inputs_fail_before_any_external_command(self):
        for args in (('web', 'deploy'), ('web', 'deploy', 'main'),
                     ('web', 'deploy', 'web-v01.2.3'), ('web', 'deploy', TAG, '--force'),
                     ('web', 'deploy', 'web-v1.2.3;echo bad'), ('web', 'watch'),
                     ('web', 'status', '-1'), ('web', 'watch', '1;echo bad'),
                     ('web', 'list', '--force'), ('web', 'unknown'), ('package', '--force')):
            with self.subTest(args=args):
                self.assertEqual(2, self.call(*args).returncode)
        self.env['LMM_API_GITHUB_REPOSITORY'] = 'owner/repo;echo bad'
        self.assertEqual(2, self.call('web', 'deploy', TAG).returncode)
        self.assertEqual([], self.calls())

    def test_package_reuses_tool_and_builds_artifacts_once(self):
        self.make_tool(self.root / 'apps/api-go/out/lmm-api-deploy-engine')
        self.env['LMM_API_BUILD_WORKSPACE'] = str(self.root / 'workspace with spaces')
        result = self.call('package')
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual([['lmm-api-deploy-engine', 'build', '--repo', str(self.root),
                          '--workspace', self.env['LMM_API_BUILD_WORKSPACE']]], self.calls())

    @unittest.skipIf(Path('/usr/lib/lmm-api-deploy/engine').exists(), 'Do not invoke an installed provider')
    def test_fresh_package_bootstraps_only_deployment_tool(self):
        self.env['LMM_API_BUILD_WORKSPACE'] = str(self.root / 'workspace')
        result = self.call('package')
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(['bun', 'run', 'build:deploy'], self.calls()[0])
        self.assertEqual(['lmm-api-deploy-engine', 'build'], self.calls()[1][:2])
        self.assertEqual(2, len(self.calls()))

    @unittest.skipIf(Path('/usr/lib/lmm-api-deploy/engine').exists(), 'Do not invoke an installed provider')
    def test_bootstrap_failure_stops_before_package(self):
        self.env['LMM_API_BUILD_WORKSPACE'] = str(self.root / 'workspace')
        self.env['FAKE_EXIT'] = '7'
        self.assertEqual(7, self.call('package').returncode)
        self.assertEqual([['bun', 'run', 'build:deploy']], self.calls())

    def test_package_missing_workspace_does_no_work(self):
        self.assertEqual(2, self.call('package').returncode)
        self.assertEqual([], self.calls())

    def test_explicit_broken_tool_never_falls_back_or_builds(self):
        self.env['LMM_API_BUILD_WORKSPACE'] = str(self.root / 'workspace')
        self.env['LMM_API_DEPLOY_BINARY'] = str(self.root / 'missing')
        self.assertEqual(127, self.call('package').returncode)
        self.assertEqual([], self.calls())

    def test_native_and_python_routes_preserve_arguments(self):
        provider = self.root / 'custom provider'
        self.make_tool(provider)
        self.env['LMM_API_DEPLOY_BINARY'] = str(provider)
        self.assertEqual(0, self.call('production', 'status', '--plan', 'plan with spaces').returncode)
        self.assertEqual(['custom provider', 'production', 'status', '--plan', 'plan with spaces'], self.calls()[-1])
        self.assertEqual(0, self.call('systemd', 'doctor', '--json').returncode)
        self.assertEqual(['python3', str(self.script.with_name('deploy-systemd.py')), 'doctor', '--json'], self.calls()[-1])
        self.assertEqual(0, self.call('shared-postgres', 'validate', '--plan', 'plan with spaces').returncode)
        self.assertEqual(['python3', '-B', str(self.script.with_name('deploy-shared-postgres.py')), 'validate', '--plan', 'plan with spaces'], self.calls()[-1])


class DownloadTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        buffer = io.BytesIO()
        with tarfile.open(fileobj=buffer, mode='w:gz') as archive:
            for name, content in {'dist/index.html': b'new frontend', 'REVISION': REVISION.encode(),
                                  'dist/legal/terms.html': b'terms', 'dist/legal/privacy.html': b'privacy',
                                  'dist/legal/forge-legal.css': b'css'}.items():
                entry = tarfile.TarInfo(name)
                entry.size = len(content)
                archive.addfile(entry, io.BytesIO(content))
        data = buffer.getvalue()
        self.assets = {ARCHIVE: data, ARCHIVE + '.sigstore.json': b'fixture, not a real signature',
                       ARCHIVE + '.sha256': (hashlib.sha256(data).hexdigest() + '  ' + ARCHIVE + '\n').encode()}
        self.counts = Counter()
        self.transient = False
        parent = self

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                name = self.path.rsplit('/', 1)[-1]
                parent.counts[name] += 1
                if parent.transient and name.endswith('.sigstore.json') and parent.counts[name] == 1:
                    status, payload = 503, b'transient'
                else:
                    payload = parent.assets.get(name, b'not found')
                    status = 200 if name in parent.assets else 404
                self.send_response(status)
                self.send_header('Content-Length', str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)

            def log_message(self, *args):
                pass

        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(self.server.server_close)
        self.addCleanup(thread.join, 5)
        self.addCleanup(self.server.shutdown)
        tools = self.root / 'tools'
        tools.mkdir()
        gh = tools / 'gh'
        gh.write_text('#!/bin/sh\ncase "$*" in\n  *target_commitish*) printf "%s\\n" "$EXPECTED_REVISION" ;;\n  *) printf "7\\n" ;;\nesac\n')
        gh.chmod(0o755)
        self.env = dict(os.environ, PATH=str(tools) + os.pathsep + os.environ['PATH'],
                        GITHUB_SERVER_URL=f'http://127.0.0.1:{self.server.server_port}',
                        GITHUB_REPOSITORY=REPO, GITHUB_RUN_ID='123', RELEASE_TAG=TAG,
                        VERSION='1.2.3', EXPECTED_REVISION=REVISION)

    def download(self):
        return subprocess.run(['bash', '-c', workflow_step('Download signed release archive')],
                              env=self.env, cwd=self.root, text=True, capture_output=True, timeout=40)

    def test_signed_asset_set_downloads_once(self):
        result = self.download()
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual({name: 1 for name in self.assets}, dict(self.counts))

    def test_transient_signature_failure_never_redownloads_archive(self):
        self.transient = True
        result = self.download()
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(1, self.counts[ARCHIVE])
        self.assertEqual(1, self.counts[ARCHIVE + '.sha256'])
        self.assertEqual(2, self.counts[ARCHIVE + '.sigstore.json'])

    def test_missing_signature_fails_without_batch_retry(self):
        del self.assets[ARCHIVE + '.sigstore.json']
        self.assertNotEqual(0, self.download().returncode)
        self.assertEqual(1, self.counts[ARCHIVE])
        self.assertEqual(1, self.counts[ARCHIVE + '.sigstore.json'])

    def test_checksum_mismatch_is_not_retried(self):
        self.assets[ARCHIVE + '.sha256'] = ('0' * 64 + '  ' + ARCHIVE + '\n').encode()
        self.assertNotEqual(0, self.download().returncode)
        self.assertEqual(1, self.counts[ARCHIVE])

    def test_wrong_release_revision_fails(self):
        self.env['EXPECTED_REVISION'] = 'b' * 40
        self.assertNotEqual(0, self.download().returncode)


class WorkflowTests(unittest.TestCase):
    def test_target_settings_resolve_in_the_validation_step(self):
        # Read production-environment variables on the runner, then export them.
        block = WORKFLOW.read_text().split('      - name: Validate deployment targets\n', 1)[1]
        entries = block.split('        env:\n', 1)[1].split('        run: |\n', 1)[0]
        entries = [line for line in entries.splitlines() if line.strip() and not line.lstrip().startswith('#')]
        self.assertEqual({'API_HOST', 'API_PORT', 'INGRESS_HOST', 'INGRESS_PORT', 'PUBLIC_ORIGIN'},
                         {line.strip().split(':', 1)[0] for line in entries})
        for line in entries:
            self.assertTrue(line.startswith('          '), line)
            self.assertFalse(line.startswith('           '), line)

    def test_targets_validate_before_host_changes(self):
        env = dict(os.environ, API_HOST='api.example.com', API_PORT='222',
                   INGRESS_HOST='192.0.2.1', INGRESS_PORT='22', PUBLIC_ORIGIN='https://example.com')
        workspace = tempfile.TemporaryDirectory()
        self.addCleanup(workspace.cleanup)
        exports = Path(workspace.name) / 'environment'
        env['GITHUB_ENV'] = str(exports)
        script = workflow_step('Validate deployment targets')
        result = subprocess.run(['bash', '-c', script], env=env, capture_output=True, timeout=5)
        self.assertEqual(0, result.returncode, result.stderr)
        expected_exports = exports.read_text()
        self.assertEqual({key: env[key] for key in ('API_HOST', 'API_PORT', 'INGRESS_HOST', 'INGRESS_PORT', 'PUBLIC_ORIGIN')},
                         dict(line.split('=', 1) for line in expected_exports.splitlines()))
        for key, value in (('API_HOST', '-oProxyCommand=bad'), ('INGRESS_HOST', 'host\nother'),
                           ('API_PORT', '0'), ('INGRESS_PORT', '65536'), ('API_PORT', '1+2'),
                           ('PUBLIC_ORIGIN', 'http://example.com'), ('PUBLIC_ORIGIN', 'https://user:pass@example.com'),
                           ('PUBLIC_ORIGIN', 'https://example.com/path')):
            with self.subTest(key=key, value=value):
                result = subprocess.run(['bash', '-c', script], env={**env, key: value}, capture_output=True, timeout=5)
                self.assertNotEqual(0, result.returncode)
                self.assertEqual(expected_exports, exports.read_text())

    def test_workflow_keeps_security_and_no_build(self):
        source = WORKFLOW.read_text()
        for contract in ('environment: production', 'cancel-in-progress: false', 'cosign verify-blob',
                         '--certificate-identity', '--certificate-oidc-issuer',
                         'StrictHostKeyChecking=yes', 'if: always()', 'LMM_WEB_DEPLOY_KNOWN_HOSTS'):
            self.assertIn(contract, source)
        publish = workflow_step('Publish to both production origins')
        self.assertNotIn('bun ', source)
        self.assertNotIn('go build', source)
        self.assertNotIn('go run', source)
        self.assertNotIn('StrictHostKeyChecking=no', source)
        self.assertIn('"$API_HOST" "$API_PORT"', publish)
        self.assertIn('"$INGRESS_HOST" "$INGRESS_PORT"', publish)

    def test_all_multiline_workflow_shell_steps_parse(self):
        for block in WORKFLOW.read_text().split('      - name: ')[1:]:
            if '        run: |\n' not in block:
                continue
            name = block.splitlines()[0]
            with self.subTest(step=name):
                result = subprocess.run(['bash', '-n'], input=workflow_step(name), text=True, capture_output=True, timeout=5)
                self.assertEqual(0, result.returncode, result.stderr)


if __name__ == '__main__':
    unittest.main()
