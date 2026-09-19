#!/usr/bin/env python3
"""Recover the recorded ArchDmit frontend/package drift through signed packages.

This does not upgrade or stop the backend. Package hooks use the native frontend
publisher; a systemd watchdog restores both the old package and active frontend.
The plan and every input must already be root-owned and privately staged.
"""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import sys
import urllib.request

ROOT = Path('/srv/lmm-api-frontend')
WORK = Path('/var/lib/lmm-api-web-reconcile/web-v0.1.76')
OLD_TARGET = 'releases/0.1.75.gc20d27083afa'
OLD_PACKAGE = 'lmm-api-web-bin 0.1.71-2'
NEW_PACKAGE = 'lmm-api-web-bin 0.1.76-1'
UNIT = 'lmm-web-reconcile-0-1-76'


def digest(path):
    with path.open('rb') as source:
        return hashlib.file_digest(source, 'sha256').hexdigest()


def tree_digest(root):
    value = hashlib.sha256()
    for path in sorted(root.rglob('*')):
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise RuntimeError('frontend has an unsafe entry')
        if path.is_file():
            value.update(path.relative_to(root).as_posix().encode() + b'\0')
            value.update(digest(path).encode() + b'\0')
    return value.hexdigest()


def private_file(path):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022 or info.st_nlink != 1:
        raise RuntimeError('unsafe recovery input: ' + path.name)
    if path.resolve() != path:
        raise RuntimeError('recovery input has a symlink component')


def run(*args):
    result = subprocess.run(args, capture_output=True, timeout=180)
    with (WORK / 'commands.log').open('ab') as log:
        log.write(result.stdout + result.stderr)
    if result.returncode:
        raise RuntimeError(Path(args[0]).name + ' failed; see private commands.log')
    return result.stdout.decode().strip()


def save(state):
    pending = WORK / 'state.next'
    with pending.open('w') as output:
        json.dump(state, output, sort_keys=True, indent=2)
        output.flush()
        os.fsync(output.fileno())
    pending.replace(WORK / 'state.json')
    descriptor = os.open(WORK, os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def health(expected_index):
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    for base in ('http://127.0.0.1:3000', 'https://api.lmm.best'):
        for route in ('/api/livez', '/api/status'):
            with opener.open(base + route, timeout=15) as response:
                result = json.load(response)
            if result.get('success') is not True:
                raise RuntimeError('backend health failed')
            if route == '/api/status' and result.get('data', {}).get('version') != '0.2.51':
                raise RuntimeError('backend changed during frontend recovery')
    with opener.open(urllib.request.Request('https://api.lmm.best/?frontend-reconcile=0.1.76', headers={'Cache-Control': 'no-cache'}), timeout=15) as response:
        actual = hashlib.sha256(response.read()).hexdigest()
    if actual != expected_index:
        raise RuntimeError('public frontend differs from the expected release')


def verified_package(plan, label):
    item = plan[label]
    expected_name = 'lmm-api-web-bin-' + ('0.1.76-1' if label == 'candidate' else '0.1.71-2') + '.pkg.tar.zst'
    if item['name'] != expected_name:
        raise RuntimeError('unexpected package filename')
    package = WORK / item['name']
    for path in (package, Path(str(package) + '.sig')):
        private_file(path)
    if digest(package) != item['sha256']:
        raise RuntimeError('package digest changed')
    run('/usr/bin/pacman-key', '--verify', str(package) + '.sig', str(package))
    expected_identity = NEW_PACKAGE if label == 'candidate' else OLD_PACKAGE
    if run('/usr/bin/pacman', '-Qp', str(package)) != expected_identity:
        raise RuntimeError('package identity mismatch')
    return package


def frontend_command(*args):
    if Path('/usr/bin/lmm-api-deploy').is_file():
        return run('/usr/bin/lmm-api-deploy', 'frontend', *args)
    return run('/usr/bin/lmm-api', 'deploy', 'frontend', *args)


def rollback(plan, state):
    if state['phase'] in ('CONFIRMED', 'ROLLED_BACK'):
        return state
    if tree_digest(WORK / 'previous-frontend') != plan['old_tree_sha256']:
        raise RuntimeError('rollback frontend snapshot changed')
    package = verified_package(plan, 'rollback')
    state['phase'] = 'ROLLBACK_REQUIRED'
    save(state)
    run('/usr/bin/pacman', '-U', '--noconfirm', str(package))
    previous = ROOT / OLD_TARGET
    if not previous.exists():
        frontend_command('publish', '--source', str(WORK / 'previous-frontend'), '--release', OLD_TARGET.removeprefix('releases/'))
    if tree_digest(previous) != plan['old_tree_sha256']:
        raise RuntimeError('retained frontend differs from snapshot')
    frontend_command('rollback', '--release', OLD_TARGET.removeprefix('releases/'))
    if os.readlink(ROOT / 'current') != OLD_TARGET or run('/usr/bin/pacman', '-Q', 'lmm-api-web-bin') != OLD_PACKAGE:
        raise RuntimeError('rollback did not restore original state')
    health(plan['old_index_sha256'])
    state['phase'] = 'ROLLED_BACK'
    save(state)
    return state


def apply(plan, script):
    if (WORK / 'state.json').exists():
        raise RuntimeError('existing recovery requires inspection; never replay apply')
    if Path('/etc/hostname').read_text().strip() != 'arch-dmit':
        raise RuntimeError('recovery is restricted to ArchDmit')
    if Path('/var/lib/lmm-api-go-deploy/transaction.lock').exists():
        raise RuntimeError('native backend transaction is active')
    if os.readlink(ROOT / 'current') != OLD_TARGET:
        raise RuntimeError('active frontend changed')
    if run('/usr/bin/pacman', '-Q', 'lmm-api-web-bin') != OLD_PACKAGE:
        raise RuntimeError('installed frontend package changed')
    if run('/usr/bin/pacman', '-Q', 'lmm-api-go-bin') != 'lmm-api-go-bin 0.2.51-1':
        raise RuntimeError('installed backend changed')
    run('/usr/bin/pacman', '-Qkk', 'lmm-api-web-bin')
    space = shutil.disk_usage('/')
    if space.free < 4 * 1024**3 or space.used / space.total >= 0.8:
        raise RuntimeError('production disk gate failed')
    if tree_digest(ROOT / OLD_TARGET) != plan['old_tree_sha256']:
        raise RuntimeError('active frontend content changed')
    candidate = verified_package(plan, 'candidate')
    verified_package(plan, 'rollback')
    health(plan['old_index_sha256'])
    run('/usr/bin/nginx', '-t')
    shutil.copytree(ROOT / OLD_TARGET, WORK / 'previous-frontend')
    if tree_digest(WORK / 'previous-frontend') != plan['old_tree_sha256']:
        raise RuntimeError('frontend backup verification failed')
    state = {'phase': 'ARMED', 'old_target': OLD_TARGET, 'new_package': NEW_PACKAGE}
    save(state)
    run('/usr/bin/systemd-run', '--unit=' + UNIT, '--on-active=300s',
        '--timer-property=AccuracySec=1s', '/usr/bin/python3', str(script), '--action', 'rollback', '--plan-sha256', digest(WORK / 'plan.json'))
    try:
        state['phase'] = 'INSTALLING'
        save(state)
        run('/usr/bin/pacman', '-U', '--noconfirm', str(candidate))
        if run('/usr/bin/pacman', '-Q', 'lmm-api-web-bin') != NEW_PACKAGE:
            raise RuntimeError('package installation did not complete')
        run('/usr/bin/pacman', '-Qkk', 'lmm-api-web-bin')
        revision = Path('/usr/share/doc/lmm-api-web-bin/REVISION').read_text().strip()
        target = 'releases/0.1.76-1.g' + revision[:12]
        if os.readlink(ROOT / 'current') != target:
            raise RuntimeError('native frontend hook did not activate candidate')
        native = json.loads((ROOT / '.deployment-transactions' / (target.removeprefix('releases/') + '.json')).read_text())
        if native['phase'] != 'CONFIRMED' or native['previous'] != OLD_TARGET.removeprefix('releases/'):
            raise RuntimeError('native frontend transaction is not confirmed with the original rollback target')
        if tree_digest(ROOT / target) != tree_digest(Path('/usr/share/lmm-api-web/frontend-dist')):
            raise RuntimeError('active frontend differs from installed package')
        health(digest(ROOT / target / 'index.html'))
        state.update(phase='CONFIRMED', new_target=target)
        save(state)
        run('/usr/bin/systemctl', 'stop', UNIT + '.timer')
        return state
    except Exception:
        rollback(plan, state)
        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--action', choices=('apply', 'rollback'), required=True)
    parser.add_argument('--plan-sha256', required=True)
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise RuntimeError('root is required')
    os.umask(0o077)
    script = Path(__file__).resolve()
    private_file(script)
    private_file(WORK / 'plan.json')
    if digest(WORK / 'plan.json') != args.plan_sha256:
        raise RuntimeError('frozen plan digest mismatch')
    plan = json.loads((WORK / 'plan.json').read_text())
    with Path('/run/lock/lmm-api-go-deploy.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        state = apply(plan, script) if args.action == 'apply' else rollback(plan, json.loads((WORK / 'state.json').read_text()))
    print(json.dumps(state, sort_keys=True))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print('frontend recovery failed: ' + str(error), file=sys.stderr)
        sys.exit(1)
