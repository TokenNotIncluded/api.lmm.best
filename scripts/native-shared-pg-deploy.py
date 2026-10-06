#!/usr/bin/python3
"""Guard the existing standalone ordinary deployment with a native capsule.

Invoke with /usr/bin/python3 -I. The financial/maintenance and DDL paths are
unavailable here. All original transaction/rollback evidence is preserved.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import stat
import subprocess
import sys

ACTIONS = {'stage', 'upgrade', 'apply', 'confirm', 'rollback'}
SOURCE_PATHS = ('scripts/native-shared-pg-deploy.py', 'scripts/deploy-systemd.py',
                'scripts/maintenance-deploy-guardian.py')


def bound_private(path, expected):
    path = Path(path)
    if not path.is_absolute() or '..' in path.parts:
        raise RuntimeError('native capsule file path is not canonical')
    for parent in (path, *path.parents):
        info = parent.lstat()
        if stat.S_ISLNK(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
            raise RuntimeError('native capsule file/ancestor is not root owned and protected')
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or stat.S_IMODE(info.st_mode) != 0o600:
        raise RuntimeError('native capsule source must be private and single linked')
    raw = path.read_bytes()
    if len(raw) > 1024 * 1024 or hashlib.sha256(raw).hexdigest() != expected:
        raise RuntimeError('native capsule bound file changed')
    return raw


class CapsuleHooks:
    def __init__(self, capsule_path, capsule_sha, capsule, native_runner=None):
        self.path, self.sha, self.capsule = str(capsule_path), capsule_sha, capsule
        self.root = Path(capsule['root'])
        self.operator = self.root / 'tmp/migrations/merchant-store-candidate/lmm-api'
        self.native_runner = native_runner or self._native_run
        self.held = False

    def _native_run(self, action):
        # Exact signed provider; no fallback to the installed candidate or
        # shell-provided executable. Native checks the actual capsule itself.
        for parent in self.operator.parents:
            info = parent.lstat()
            if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o022:
                raise RuntimeError('native provider ancestor is replaceable or a symlink')
        entry = self.operator.lstat()
        if entry.st_uid != 0:
            raise RuntimeError('native provider entry is not root owned')
        if self.operator.is_symlink() and os.readlink(self.operator) == 'lmm-api-go':
            provider = self.operator.with_name('lmm-api-go')
        else:
            raise RuntimeError('native qualified provider entry is missing or changed')
        info = provider.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_nlink != 1 or stat.S_IMODE(info.st_mode) != 0o700:
            raise RuntimeError('native provider file is unsafe')
        if hashlib.sha256(provider.read_bytes()).hexdigest() != self.capsule['candidate']['payload_sha256']:
            raise RuntimeError('native provider differs from official candidate ELF')
        result = subprocess.run([str(self.operator), 'operator', 'production', 'writer-capsule', action,
                                '--capsule', self.path, '--capsule-sha256', self.sha],
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False,
                                env={'PATH': '/usr/bin:/bin', 'LANG': 'C', 'LC_ALL': 'C'})
        if result.returncode:
            # Native errors contain metadata only. Never log the ambient
            # environment or the child command's private database arguments.
            raise RuntimeError('native merchant guard refused ' + action + ': ' +
                               result.stderr.decode(errors='replace').strip())

    def native(self, action):
        bound_private(self.path, self.sha)
        for source in self.capsule['source_files']:
            bound_private(self.root / 'source' / source['path'], source['sha256'])
        self.native_runner(action)

    def bind(self, module):
        original_run, original_install, original_verify = module.run, module.install, module.verify
        original_save, original_healthy, original_stop = module.save, module.healthy, module.stop
        original_read_state = module.read_state
        original_execute = module.execute

        def held():
            if not self.held:
                raise RuntimeError('ordinary mutation requires the explicit live native owner')
            self.native('check-held')

        def run(*args, **kwargs):
            if 'migrate' in args and '--apply' in args:
                raise RuntimeError('native shared-PG wrapper does not authorize DDL or financial replay')
            if (args[:2] in (('systemctl', 'stop'), ('systemctl', 'start'), ('systemctl', 'restart'),
                             ('systemctl', 'reload')) or 'frontend' in args):
                held()
            return original_run(*args, **kwargs)

        def install(source, target):
            held()
            if Path(target) == module.BINARY:
                digest = module.digest(source)
                if digest not in (self.capsule['candidate']['payload_sha256'],
                                  self.capsule['rollback']['payload_sha256']):
                    raise RuntimeError('ordinary install is outside the explicit signed target set')
            return original_install(source, target)

        def verify(work, label, mode='verify', maintenance=None):
            if mode != 'verify' or maintenance:
                raise RuntimeError('native shared-PG capsule only authorizes migrate --verify')
            # The native child uses exact ordered sealed EnvFiles and runs both
            # actual providers. Do not invoke the old unsealed migration helper.
            self.native('check-held' if self.held else 'check')

        def healthy(version, maintenance=None):
            held()
            return original_healthy(version, maintenance)

        def stop(work, maintenance=None):
            held()
            if maintenance:
                raise RuntimeError('financial maintenance is unavailable through this wrapper')
            return original_stop(work)

        def save(work, state):
            if state.get('migrate') or state.get('maintenance_handoff'):
                raise RuntimeError('native same-schema transaction cannot become financial maintenance')
            if state['phase'] in ('CONFIRMED', 'ROLLED_BACK'):
                held()
            original_save(work, state)
            if state['phase'] in ('CONFIRMED', 'ROLLED_BACK'):
                self.native('release')
                self.held = False

        def execute(args, parser):
            if args.action not in ACTIONS or args.release != self.capsule['deployment_id'] or args.migrate or args.maintenance_handoff:
                raise RuntimeError('ordinary command differs from the immutable capsule scope')
            if args.binary and module.digest(args.binary) != self.capsule['candidate']['payload_sha256']:
                raise RuntimeError('staged ELF differs from the exact official candidate')
            self.native('check')  # Unsupported retained CLI blocks before any mutation.
            if args.action != 'stage':
                self.native('ensure')
                self.held = True
            return original_execute(args, parser)

        def read_state(*args, **kwargs):
            state = original_read_state(*args, **kwargs)
            if state.get('migrate') or state.get('maintenance_handoff'):
                raise RuntimeError('historical financial owner cannot enter the native ordinary wrapper')
            return state

        module.run, module.install, module.verify = run, install, verify
        module.healthy, module.stop, module.save, module.execute = healthy, stop, save, execute
        module.read_state = read_state


def main(argv=None):
    if os.geteuid() != 0 or not sys.flags.isolated:
        raise RuntimeError('run this root-private wrapper with /usr/bin/python3 -I')
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--capsule', type=Path, required=True)
    parser.add_argument('--capsule-sha256', required=True)
    args, ordinary = parser.parse_known_args(argv)
    raw = bound_private(args.capsule, args.capsule_sha256)
    capsule = json.loads(raw)
    if tuple(item['path'] for item in capsule['source_files']) != SOURCE_PATHS:
        raise RuntimeError('portable capsule source closure is unknown')
    root = Path(capsule['root'])
    expected_self = root / 'source' / SOURCE_PATHS[0]
    if Path(__file__).absolute() != expected_self:
        raise RuntimeError('portable wrapper is outside its exact capsule source root')
    for source in capsule['source_files']:
        bound_private(root / 'source' / source['path'], source['sha256'])
    if not ordinary or ordinary[0] not in ACTIONS or any(word.startswith(('--migrate', '--maintenance-', '--financial-')) for word in ordinary):
        raise RuntimeError('only ordinary same-schema actions are available')
    hooks = CapsuleHooks(args.capsule, args.capsule_sha256, capsule)
    hooks.native('check')
    spec = importlib.util.spec_from_file_location('native_bound_standalone_owner', root / 'source/scripts/deploy-systemd.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    hooks.bind(module)
    previous = sys.argv
    try:
        sys.argv = [str(spec.origin), *ordinary]
        return module.main()
    finally:
        sys.argv = previous


if __name__ == '__main__':
    try:
        sys.exit(main())
    except Exception as error:
        print('native-shared-pg-deploy: ' + str(error), file=sys.stderr)
        sys.exit(1)
