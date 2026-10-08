#!/usr/bin/env python3
"""Tag, sign-publish and optionally deploy one frontend release from main.

    web-ship.py release [web-vX.Y.Z]   tag origin/main, run release-web.yml, wait
    web-ship.py ship    [web-vX.Y.Z]   release, then deploy-web-frontend.yml, wait

Every workflow dispatch happens at most once. A failure after a dispatch prints
the exact run and stops; inspect it instead of rerunning this command.
"""
import json
import os
import re
import subprocess
import sys
import time

TAG_RE = re.compile(r'^web-v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$')
REPO_RE = re.compile(r'^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$')
WEB_PATHS = ('apps/web', 'packages', 'package.json', 'bun.lock')
POLL_SECONDS = float(os.environ.get('LMM_WEB_SHIP_POLL_SECONDS', '5'))
POLL_ATTEMPTS = int(os.environ.get('LMM_WEB_SHIP_POLL_ATTEMPTS', '24'))


class ShipError(Exception):
    pass


def run(*args, capture=True):
    result = subprocess.run(args, text=True, capture_output=capture)
    if result.returncode:
        detail = (result.stderr or '').strip() if capture else ''
        raise ShipError(f'{" ".join(args[:3])} failed ({result.returncode}) {detail}'.strip())
    return result.stdout.strip() if capture else ''


def version_key(tag):
    return tuple(int(part) for part in TAG_RE.match(tag).groups())


def next_tag(tags, pkgver):
    known = [tag for tag in tags if TAG_RE.match(tag)]
    floor = tuple(int(part) for part in pkgver.split('.'))
    latest = max([version_key(tag) for tag in known] + [floor])
    return 'web-v%d.%d.%d' % (latest[0], latest[1], latest[2] + 1), known


def find_run(repo, workflow, branch, title):
    for _ in range(POLL_ATTEMPTS):
        args = ['gh', 'run', 'list', '--repo', repo, '--workflow', workflow,
                '--event', 'workflow_dispatch', '--limit', '20',
                '--json', 'databaseId,headBranch,displayTitle']
        for item in json.loads(run(*args) or '[]'):
            if item.get('headBranch') == branch and (title is None or item.get('displayTitle') == title):
                return str(item['databaseId'])
        time.sleep(POLL_SECONDS)
    raise ShipError(f'dispatched {workflow} but could not find its run; check `gh run list --workflow {workflow}`')


def watch(repo, run_id, label):
    print(f'{label}: run {run_id}', flush=True)
    try:
        run('gh', 'run', 'watch', run_id, '--repo', repo, '--exit-status', '--interval', '15', capture=False)
    except ShipError:
        raise ShipError(f'{label} run {run_id} failed; inspect it, do not redispatch blindly')


def release(repo, requested):
    run('git', 'fetch', '--quiet', '--tags', 'origin', 'main')
    revision = run('git', 'rev-parse', 'origin/main^{commit}')
    pkgver = run('git', 'show', 'origin/main:packaging/aur/lmm-api-web-bin/PKGBUILD')
    pkgver = re.search(r'^pkgver=(\d+\.\d+\.\d+)$', pkgver, re.M)
    if not pkgver:
        raise ShipError('cannot read pkgver from packaging/aur/lmm-api-web-bin/PKGBUILD')
    tag, known = next_tag(run('git', 'tag', '--list', 'web-v*').split(), pkgver.group(1))
    if requested:
        if requested in known:
            raise ShipError(f'{requested} already exists; deploy it with `web deploy {requested}`')
        if known and version_key(requested) <= max(map(version_key, known)):
            raise ShipError(f'{requested} must be newer than {max(known, key=version_key)}')
        tag = requested
    if known:
        latest = max(known, key=version_key)
        changed = run('git', 'diff', '--name-only', f'{latest}^{{commit}}', revision, '--', *WEB_PATHS)
        if not changed:
            raise ShipError(f'no frontend changes since {latest}; nothing to release')
    print(f'{tag} <- origin/main {revision[:12]}', flush=True)

    # Refuse before tagging: release-web.yml repeats this gate and would fail later.
    env = dict(os.environ, GITHUB_TOKEN=os.environ.get('GITHUB_TOKEN') or run('gh', 'auth', 'token'),
               GITHUB_REPOSITORY=repo)
    gate = subprocess.run(['bash', 'scripts/verify-release-commit-checks.sh', revision, '--component', 'web'],
                          env=env, text=True)
    if gate.returncode:
        raise ShipError(f'Go/Web release checks are not green for {revision[:12]}')

    subject = run('git', 'log', '-1', '--format=%s', revision)
    run('git', 'tag', '-s', tag, revision, '-m', f'LMM web {tag[5:]}: {subject}')
    run('git', 'push', 'origin', f'refs/tags/{tag}')
    run('gh', 'workflow', 'run', 'release-web.yml', '--repo', repo, '--ref', tag)
    watch(repo, find_run(repo, 'release-web.yml', tag, None), f'release {tag}')
    return tag


def deploy(repo, tag):
    run('gh', 'workflow', 'run', 'deploy-web-frontend.yml', '--repo', repo, '--ref', 'main',
        '--raw-field', f'release_tag={tag}')
    watch(repo, find_run(repo, 'deploy-web-frontend.yml', 'main', f'Deploy {tag} frontend'), f'deploy {tag}')


def main(argv):
    if len(argv) not in (1, 2) or argv[0] not in ('release', 'ship'):
        print(__doc__.strip(), file=sys.stderr)
        return 2
    requested = argv[1] if len(argv) == 2 else None
    if requested is not None and not TAG_RE.match(requested):
        print('tag must be web-vX.Y.Z', file=sys.stderr)
        return 2
    repo = os.environ.get('LMM_API_GITHUB_REPOSITORY', 'TokenNotIncluded/api.lmm.best')
    if not REPO_RE.match(repo):
        print('LMM_API_GITHUB_REPOSITORY must be owner/repo', file=sys.stderr)
        return 2
    try:
        tag = release(repo, requested)
        if argv[0] == 'ship':
            deploy(repo, tag)
    except ShipError as error:
        print(f'web {argv[0]}: {error}', file=sys.stderr)
        return 1
    print(f'{tag}: {"released and deployed" if argv[0] == "ship" else "released (not deployed)"}')
    return 0


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
