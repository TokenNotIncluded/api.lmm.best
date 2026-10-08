"""Test the frontend release orchestration with fake git/gh; no network access."""
from pathlib import Path
import importlib.util
import json
import os
import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parent.parent
REVISION = 'b' * 40

FAKE = f'#!{sys.executable}\n' + textwrap.dedent('''\
    import json, os, pathlib, sys
    name = pathlib.Path(sys.argv[0]).name
    args = sys.argv[1:]
    with open(os.environ['COMMAND_LOG'], 'a') as log:
        log.write(json.dumps([name, *args]) + '\\n')
    state = json.loads(pathlib.Path(os.environ['FAKE_STATE']).read_text())
    if name == 'git' and args[:1] == ['fetch'] and any(arg.startswith('refs/tags/') for arg in args) and state.get('fail_tag_fetch'):
        sys.exit(1)
    fail = state.get('fail', {})
    key = ' '.join([name, *args[:2]])
    if key in fail:
        sys.exit(fail[key])
    if name == 'git':
        if args[:1] == ['rev-parse']:
            print(state.get('fetched_tag_revision', 'c' * 40) if args[1] == 'FETCH_HEAD^{commit}' else state['revision'])
        elif args[:1] == ['show']: print('pkgname=x\\npkgver=' + state['pkgver'])
        elif args[:1] == ['ls-remote']:
            for tag in state['tags']:
                print('f' * 40 + '\\trefs/tags/' + tag)
                print('c' * 40 + '\\trefs/tags/' + tag + '^{}')
        elif args[:2] == ['show-ref', '--verify']:
            sys.exit(0 if args[-1].removeprefix('refs/tags/') in state.get('local_tags', []) else 1)
        elif args[:1] == ['diff']: print(state.get('changed', 'apps/web/src/a.ts'))
        elif args[:1] == ['log']: print('feat(web): subject')
    elif name == 'gh':
        if args[:2] == ['auth', 'token']: print('token')
        elif args[:2] == ['run', 'list']:
            workflow = args[args.index('--workflow') + 1]
            print(json.dumps(state['runs'].get(workflow, [])))
    elif name == 'bash':
        sys.exit(state.get('gate', 0))
''')


class WebShipTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix='lmm web ship ')
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        (self.root / 'scripts').mkdir()
        for name in ('lmm-api-deploy.sh', 'web-ship.py'):
            shutil.copyfile(ROOT / 'scripts' / name, self.root / 'scripts' / name)
        tools = self.root / 'tools'
        tools.mkdir()
        for tool in ('git', 'gh', 'bash'):
            (tools / tool).write_text(FAKE)
            (tools / tool).chmod(0o755)
        self.log = self.root / 'commands.jsonl'
        self.state_file = self.root / 'state.json'
        self.state = {
            'revision': REVISION, 'pkgver': '0.1.100', 'tags': ['web-v0.1.135', 'web-v0.1.136'],
            'runs': {
                'release-web.yml': [{'databaseId': 11, 'headBranch': 'web-v0.1.137', 'displayTitle': 'x'}],
                'deploy-web-frontend.yml': [
                    {'databaseId': 21, 'headBranch': 'main', 'displayTitle': 'Deploy web-v0.1.136 frontend'},
                    {'databaseId': 22, 'headBranch': 'main', 'displayTitle': 'Deploy web-v0.1.137 frontend'}],
            },
        }
        self.env = dict(os.environ, PATH=str(tools) + os.pathsep + os.environ['PATH'],
                        COMMAND_LOG=str(self.log), FAKE_STATE=str(self.state_file),
                        LMM_WEB_SHIP_POLL_SECONDS='0', LMM_WEB_SHIP_POLL_ATTEMPTS='2',
                        LMM_LOCAL_TEST_EVIDENCE_JSON='{"format":1,"component":"web"}')
        self.env.pop('LMM_LOCAL_TEST_EVIDENCE', None)
        self.env.pop('LMM_API_GITHUB_REPOSITORY', None)
        self.env.pop('GITHUB_TOKEN', None)

    def call(self, *args):
        self.state_file.write_text(json.dumps(self.state))
        return subprocess.run([sys.executable, '-B', str(self.root / 'scripts/web-ship.py'), *args],
                              cwd=self.root, env=self.env, capture_output=True, text=True, timeout=20)

    def calls(self, prefix=()):
        rows = [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []
        return [row for row in rows if row[:len(prefix)] == list(prefix)]

    def test_ship_tags_next_patch_once_and_watches_exact_runs(self):
        result = self.call('ship')
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual([['git', 'tag', '-s', 'web-v0.1.137', REVISION, '-m',
                           'LMM web 0.1.137: feat(web): subject']], self.calls(('git', 'tag', '-s')))
        self.assertEqual([['git', 'push', 'origin', 'refs/tags/web-v0.1.137']], self.calls(('git', 'push')))
        dispatches = self.calls(('gh', 'workflow', 'run'))
        self.assertEqual(2, len(dispatches))
        self.assertIn('release-web.yml', dispatches[0])
        self.assertIn('local_test_evidence={"format":1,"component":"web"}', dispatches[0])
        self.assertIn('release_tag=web-v0.1.137', dispatches[1])
        self.assertEqual(['11', '22'], [row[3] for row in self.calls(('gh', 'run', 'watch'))])

    def test_remote_tags_decide_version_and_diff_base(self):
        self.assertEqual(0, self.call('release').returncode)
        self.assertEqual([], self.calls(('git', 'tag', '--list')))
        self.assertNotIn('--tags', self.calls(('git', 'fetch'))[0])
        self.assertIn('--no-tags', self.calls(('git', 'fetch'))[0])
        self.assertEqual(['git', 'fetch', '--quiet', '--no-tags', '--refmap=', 'origin', 'refs/tags/web-v0.1.136'],
                         self.calls(('git', 'fetch'))[1])
        self.assertEqual('c' * 40, self.calls(('git', 'diff'))[0][3])

    def test_remote_tag_changed_between_listing_and_fetch_refuses_to_tag(self):
        self.state['fetched_tag_revision'] = 'd' * 40
        result = self.call('release')
        self.assertEqual(1, result.returncode)
        self.assertIn('changed on origin', result.stderr)
        self.assertEqual([], self.calls(('git', 'tag', '-s')))
        self.assertEqual([], self.calls(('gh', 'workflow')))

    def test_failed_remote_tag_download_refuses_to_tag(self):
        self.state['fail_tag_fetch'] = True
        self.assertEqual(1, self.call('release').returncode)
        self.assertEqual([], self.calls(('git', 'tag', '-s')))

    def test_local_only_next_tag_is_preserved_and_never_published(self):
        self.state['local_tags'] = ['web-v0.1.137']
        result = self.call('release')
        self.assertEqual(1, result.returncode)
        self.assertIn('exists locally but not on origin', result.stderr)
        self.assertEqual([], self.calls(('bash',)))
        self.assertEqual([], self.calls(('git', 'push')))

    def test_requested_tag_must_exceed_aur_floor_before_any_publication(self):
        self.state['pkgver'] = '0.1.137'
        result = self.call('release', 'web-v0.1.137')
        self.assertEqual(1, result.returncode)
        self.assertIn('AUR pkgver', result.stderr)
        self.assertEqual([], self.calls(('git', 'tag', '-s')))

    def test_release_stops_before_deploy(self):
        result = self.call('release')
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(1, len(self.calls(('gh', 'workflow', 'run'))))
        self.assertIn('not deployed', result.stdout)

    def test_failed_local_evidence_gate_creates_no_tag(self):
        self.state['gate'] = 1
        self.assertEqual(1, self.call('ship').returncode)
        self.assertEqual([], self.calls(('git', 'tag', '-s')))
        self.assertEqual([], self.calls(('gh', 'workflow')))

    def test_missing_local_evidence_creates_no_tag_or_token_read(self):
        self.env.pop('LMM_LOCAL_TEST_EVIDENCE_JSON')
        result = self.call('ship')
        self.assertEqual(1, result.returncode)
        self.assertIn('local test record', result.stderr)
        self.assertEqual([], self.calls(('git', 'tag', '-s')))
        self.assertEqual([], self.calls(('gh', 'auth')))

    def test_no_frontend_change_refuses(self):
        self.state['changed'] = ''
        result = self.call('ship')
        self.assertEqual(1, result.returncode)
        self.assertIn('no frontend changes', result.stderr)
        self.assertEqual([], self.calls(('git', 'tag', '-s')))

    def test_existing_or_older_tag_refuses(self):
        for tag in ('web-v0.1.136', 'web-v0.1.10'):
            with self.subTest(tag=tag):
                self.assertEqual(1, self.call('ship', tag).returncode)
        self.assertEqual([], self.calls(('git', 'tag', '-s')))

    def test_failed_release_run_never_deploys_or_redispatches(self):
        self.state['fail'] = {'gh run watch': 1}
        result = self.call('ship')
        self.assertEqual(1, result.returncode)
        self.assertIn('run 11 failed', result.stderr)
        self.assertEqual(1, len(self.calls(('gh', 'workflow', 'run'))))

    def test_missing_run_is_reported_not_redispatched(self):
        self.state['runs']['deploy-web-frontend.yml'] = []
        self.assertEqual(1, self.call('ship').returncode)
        self.assertEqual(2, len(self.calls(('gh', 'workflow', 'run'))))

    def test_pkgver_floor_is_respected_without_tags(self):
        self.state['tags'] = []
        self.state['runs']['release-web.yml'][0]['headBranch'] = 'web-v0.1.101'
        self.assertEqual(0, self.call('release').returncode)
        self.assertIn('web-v0.1.101', self.calls(('git', 'tag', '-s'))[0])

    def test_bad_arguments_run_nothing(self):
        for args in ((), ('deploy',), ('ship', 'main'), ('ship', 'web-v1.2.3;x'), ('ship', 'a', 'b')):
            with self.subTest(args=args):
                self.assertEqual(2, self.call(*args).returncode)
        self.assertEqual([], self.calls())

    def test_entrypoint_routes_ship_from_repo_root(self):
        result = subprocess.run(['/usr/bin/bash', str(self.root / 'scripts/lmm-api-deploy.sh'), 'web', 'ship', 'bad'],
                                cwd='/', env=self.env, capture_output=True, text=True, timeout=20)
        self.assertEqual(2, result.returncode)
        self.assertIn('web-vX.Y.Z', result.stderr)


class RealRemoteTagTests(unittest.TestCase):
    def test_shallow_remote_object_and_divergent_local_tag_with_tag_fetch_config(self):
        with tempfile.TemporaryDirectory(prefix='real remote tags ') as temporary:
            root = Path(temporary)
            source, origin, client = (root / name for name in ('source', 'origin', 'client'))
            for path in (source, client):
                path.mkdir()
                subprocess.run(['git', 'init', '-q', '-b', 'main', str(path)], check=True)
                for key, value in (('user.name', 'Fixture'), ('user.email', 'fixture@example.invalid'),
                                   ('commit.gpgsign', 'false'), ('tag.gpgsign', 'false')):
                    subprocess.run(['git', '-C', str(path), 'config', key, value], check=True)
            subprocess.run(['git', 'init', '-q', '--bare', '-b', 'main', str(origin)], check=True)

            def command(path, *args, required=True):
                result = subprocess.run(['git', '-C', str(path), *args], text=True, capture_output=True)
                if required and result.returncode:
                    self.fail(result.stderr)
                return result

            def commit(path, text):
                (path / 'file').write_text(text)
                command(path, 'add', '.')
                command(path, 'commit', '-qm', text)
                return command(path, 'rev-parse', 'HEAD').stdout.strip()

            remote_commit = commit(source, 'remote old')
            command(source, 'tag', '-a', 'web-v0.1.136', '-m', 'remote release')
            commit(source, 'remote main')
            command(source, 'remote', 'add', 'origin', str(origin))
            command(source, 'push', '-q', 'origin', 'main', 'refs/tags/web-v0.1.136')
            local_commit = commit(client, 'diverged local')
            command(client, 'tag', '-a', 'web-v0.1.136', '-m', 'preserved local release')
            command(client, 'remote', 'add', 'origin', str(origin))
            command(client, 'config', 'remote.origin.tagOpt', '--tags')
            command(client, 'config', '--add', 'remote.origin.fetch', '+refs/tags/*:refs/tags/*')
            command(client, 'fetch', '--quiet', '--depth=1', '--no-tags', 'origin', 'main')
            self.assertNotEqual(0, command(client, 'cat-file', '-e', remote_commit + '^{commit}', required=False).returncode)
            spec = importlib.util.spec_from_file_location('real_web_ship', ROOT / 'scripts/web-ship.py')
            module = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(module)

            def run(*argv, capture=True):
                self.assertEqual('git', argv[0])
                result = command(client, *argv[1:], required=False)
                if result.returncode:
                    raise module.ShipError(result.stderr)
                return result.stdout.strip()

            with patch.object(module, 'run', run):
                self.assertEqual(remote_commit, module.remote_tags()['web-v0.1.136'])
                self.assertEqual(remote_commit, module.fetch_remote_tag('web-v0.1.136', remote_commit))
            self.assertEqual(local_commit, command(client, 'rev-parse', 'refs/tags/web-v0.1.136^{commit}').stdout.strip())


if __name__ == '__main__':
    unittest.main()
