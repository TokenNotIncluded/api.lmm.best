#!/usr/bin/env python3
"""Normalize one captured forwarding ingress; never close, stop, or resume it."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import sys
import time

sys.dont_write_bytecode = True
ROOT_UID = 0
OWNER_PATH = Path('/var/lib/lmm-credit-transition/credit-financial-20261006/deploy-systemd.py')
OWNER_SHA256 = '72bdf42f1ef5f3de998166e08efa94faddf5a5c0cf3d396633c464b1ca98f951'
GUARDIAN_SHA256 = 'f27fdb84aca03a1509d91817c198be8e62903fe0e84923f28efeffd312ef23b1'
OLD_SHA256 = 'ed052676b8c40f6fdcc1581de63531a3f7fa17a2d1fdbf22a50b4862954468b0'
NEW_SHA256 = '0626cceda839a87bc1f848f2276161077c5337df4496b201200a903027c6285c'
CAPTURE_STATUS_SHA256 = 'f4d70a655822fe02cb3ce28c30afc98120ae2857ecd60d1f0788aa4f998433b7'
REPAIR_DIR = 'ingress-normalization-repair-v1'
GUARDIAN_UNIT = 'lmm-credit-financial-20261006-ubuntu.service'
GUARDIAN_UNIT_SHA256 = 'cdeebf6c78e68a21316de9fac054d33ec1e1cef9d5c950ac17fd9788b904a80f'
GUARDIAN_PID = 497719
GUARDIAN_INVOCATION = 'd6ec63e6f0e74d5fa1c0149fa328e8d1'
GUARDIAN_LOCKS = [
    {'path': '/run/lock/lmm-api-go-deploy.lock', 'device': 27, 'inode': 4259, 'held': True},
    {'path': '/var/lib/lmm-api-deploy-systemd/lock', 'device': 64769, 'inode': 294488, 'held': True},
    {'path': '/srv/lmm-api-frontend/.release.lock', 'device': 64769, 'inode': 292828, 'held': True},
]


def sha(body):
    return hashlib.sha256(body).hexdigest()


def encoded(value):
    return json.dumps(value, sort_keys=True, indent=2).encode() + b'\n'


def bound_bytes(path, expected=None, private=False):
    path = Path(path)
    if not path.is_absolute() or '..' in path.parts or str(path) != os.path.normpath(str(path)):
        raise RuntimeError('repair evidence path must be absolute and canonical')
    for parent in path.parents:
        info = parent.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid not in (0, ROOT_UID) or info.st_mode & 0o022:
            raise RuntimeError('repair evidence ancestor is unsafe')
    before = path.lstat()
    if not stat.S_ISREG(before.st_mode) or before.st_uid != ROOT_UID or before.st_nlink != 1 or before.st_mode & 0o022 or (private and stat.S_IMODE(before.st_mode) != 0o600):
        raise RuntimeError('repair evidence must be a safe root-owned single-linked regular file')
    identity = lambda info: (info.st_dev, info.st_ino, info.st_mode, info.st_uid, info.st_nlink, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        opened = os.fstat(descriptor)
        if identity(before) != identity(opened) or opened.st_size > 4 * 1024 * 1024:
            raise RuntimeError('repair evidence changed while opening or is oversized')
        with os.fdopen(descriptor, 'rb', closefd=False) as source:
            body = source.read(4 * 1024 * 1024 + 1)
        if identity(opened) != identity(os.fstat(descriptor)) or identity(opened) != identity(path.lstat()) or len(body) > 4 * 1024 * 1024:
            raise RuntimeError('repair evidence changed while reading')
        if expected is not None and sha(body) != expected:
            raise RuntimeError('repair evidence hash differs: ' + path.name)
        return body
    finally:
        os.close(descriptor)


def load_owner():
    body = bound_bytes(OWNER_PATH, OWNER_SHA256, private=True)
    bound_bytes(OWNER_PATH.with_name('maintenance-deploy-guardian.py'), GUARDIAN_SHA256, private=True)
    specification = importlib.util.spec_from_file_location('sealed_ingress_repair_owner', OWNER_PATH)
    owner = importlib.util.module_from_spec(specification)
    exec(compile(body, str(OWNER_PATH), 'exec'), owner.__dict__)
    return owner


def normalize(original):
    if sha(original) != OLD_SHA256 or original.count(b'location / {') != 1:
        raise RuntimeError('repair only accepts the reviewed original forwarding ingress')
    candidate = original.replace(b'location / {', b'location @lmm_api_backend {', 1)
    candidate += b'\nlocation / {\n    error_page 418 = @lmm_api_backend;\n    return 418;\n}\n'
    if sha(candidate) != NEW_SHA256:
        raise RuntimeError('normalized ingress differs from the reviewed candidate')
    return candidate


def verify_guardian_generation(owner, receipt):
    if receipt.get('guardian_pid') != GUARDIAN_PID or receipt.get('locks') != GUARDIAN_LOCKS:
        raise RuntimeError('repair guardian differs from the original generation or three frozen lock inodes')
    bound_bytes(Path('/etc/systemd/system') / GUARDIAN_UNIT, GUARDIAN_UNIT_SHA256)
    for key, expected in {'MainPID': str(GUARDIAN_PID), 'InvocationID': GUARDIAN_INVOCATION, 'ActiveState': 'active'}.items():
        if owner.run('systemctl', 'show', GUARDIAN_UNIT, '-p', key, '--value') != expected:
            raise RuntimeError('original guardian generation is no longer active')


def verify_capture(owner, work, state, maintenance):
    if state.get('phase') != 'CAPTURED' or state.get('maintenance_admission_closed') or maintenance.get('stage') != 'prebridge' or maintenance.get('stopped_writer'):
        raise RuntimeError('repair requires the still-live ordinary CAPTURED owner')
    binding = {'path': str(maintenance['_handoff_path']), 'sha256': maintenance['_handoff_sha256']}
    if state.get('maintenance_handoff') != binding or state.get('maintenance_stage') != 'prebridge' or any(state.get(key) != maintenance.get(key) for key in ('transition_id', 'transition_intent_sha256', 'provider_sha256', 'prepare_config_sha256')):
        raise RuntimeError('captured state differs from the original handoff')
    owner.check_layout()
    pid, invocation = state['captured_pid'], state['captured_invocation_id']
    if type(pid) is not int or pid <= 1 or not isinstance(invocation, str) or not re.fullmatch('[0-9a-f]{32}', invocation) or any(owner.property_value(key) != expected for key, expected in {'MainPID': str(pid), 'InvocationID': invocation, 'ActiveState': 'active', 'SubState': 'running'}.items()):
        raise RuntimeError('captured writer generation or active state changed')
    for path, expected in ((owner.BINARY, state['previous_sha256']), (work / 'previous-binary', state['previous_sha256']), (work / 'lmm-api-go', state['sha256'])):
        owner.cleanup_path(path)
        if owner.digest(path) != expected:
            raise RuntimeError('captured or staged provider bytes changed')
    if state['sha256'] != maintenance['provider_sha256'] or owner.digest(Path('/proc', str(pid), 'exe')) != state['previous_sha256']:
        raise RuntimeError('captured running executable differs from its original provider')
    archived = bound_bytes(work / 'previous.env', state['archived_environment_sha256'], private=True)
    if bound_bytes(owner.ENVIRONMENT) != archived:
        raise RuntimeError('current environment differs from the captured original')
    process_path = work / ('captured-process.' + invocation + '.environment')
    if state['process_environment_path'] != str(process_path):
        raise RuntimeError('captured process environment has an unexpected path')
    process = bound_bytes(process_path, state['process_environment_sha256'], private=True)
    if Path('/proc', str(pid), 'environ').read_bytes() != process:
        raise RuntimeError('captured process environment changed')
    if not (owner.FRONTEND / 'current').is_symlink() or os.readlink(owner.FRONTEND / 'current') != 'releases/' + state['previous_frontend'] or owner.tree_digest(owner.FRONTEND / 'current') != state['frontend_sha256'] or owner.tree_digest(work / 'frontend') != state['frontend_sha256']:
        raise RuntimeError('captured or staged frontend changed')
    capture_path = work / 'maintenance-capture.CAPTURED.json'
    if state['capture_receipt_path'] != str(capture_path):
        raise RuntimeError('capture receipt path is not the ordinary CAPTURED receipt')
    capture = json.loads(bound_bytes(capture_path, state['capture_receipt_sha256'], private=True))
    expected = {'format': 'lmm-credit-maintenance-capture-v1', 'phase': 'CAPTURED',
                'transition_id': maintenance['transition_id'], 'transition_intent_sha256': maintenance['transition_intent_sha256'],
                'provider_sha256': state['previous_sha256'], 'version': state['previous_version'],
                'pid': pid, 'invocation_id': invocation, 'archived_environment_path': str(work / 'previous.env'),
                'archived_environment_sha256': state['archived_environment_sha256'],
                'process_environment_path': str(process_path), 'process_environment_sha256': state['process_environment_sha256'],
                'frontend_target': 'releases/' + state['previous_frontend'], 'frontend_sha256': state['frontend_sha256'],
                'was_maintenance_confirmed': False}
    if capture != expected:
        raise RuntimeError('original CAPTURED receipt differs from the live owner evidence')
    owner.healthy(state['previous_version'])
    if owner.property_value('MainPID') != str(pid) or owner.property_value('InvocationID') != invocation:
        raise RuntimeError('captured writer generation changed during verification')


def repair(args, owner=None):
    owner = owner or load_owner()
    snapshot = json.loads(bound_bytes(args.capture_status, CAPTURE_STATUS_SHA256, private=True))
    maintenance = owner.guardian.handoff(args.maintenance_handoff, args.maintenance_handoff_sha256)
    maintenance['_handoff_sha256'] = args.maintenance_handoff_sha256
    work = owner.ROOT / args.release
    owner.cleanup_path(work)
    with owner.deployment_lock(maintenance, args.maintenance_handoff_sha256, all_locks=True) as receipt:
        owner.verify_cleanup_guardian(receipt)
        verify_guardian_generation(owner, receipt)
        state_raw = bound_bytes(work / 'state.json', private=True)
        state = owner.read_state(work)
        if state != snapshot or json.loads(state_raw) != state or state.get('release') != args.release:
            raise RuntimeError('captured state differs from the reviewed capture-status snapshot')
        verify_capture(owner, work, state, maintenance)
        original = bound_bytes(owner.NGINX_LOCATIONS, OLD_SHA256)
        bound_bytes(work / 'previous-nginx-locations', OLD_SHA256, private=True)
        if state.get('ingress_original_sha256') != OLD_SHA256:
            raise RuntimeError('captured original ingress identity changed')
        candidate = normalize(original)
        directory = work / REPAIR_DIR
        if directory.exists() or directory.is_symlink():
            raise RuntimeError('repair lineage already exists; inspect it instead of replaying the repair')
        if (work / 'state.next').exists() or (work / 'state.next').is_symlink():
            raise RuntimeError('unexpected owner state pending file')
        proof = {'format': 'lmm-systemd-captured-ingress-repair-v1', 'release': args.release,
                 'transition_id': maintenance['transition_id'], 'transition_intent_sha256': maintenance['transition_intent_sha256'],
                 'handoff_path': str(args.maintenance_handoff), 'handoff_sha256': args.maintenance_handoff_sha256,
                 'original_owner_sha256': OWNER_SHA256, 'guardian_sha256': GUARDIAN_SHA256,
                 'capture_status_sha256': CAPTURE_STATUS_SHA256, 'capture_receipt_sha256': state['capture_receipt_sha256'],
                 'state_before_sha256': sha(state_raw), 'old_ingress_sha256': OLD_SHA256,
                 'new_ingress_sha256': NEW_SHA256, 'phase_before': 'CAPTURED', 'phase_after': 'CAPTURED',
                 'captured_pid': state['captured_pid'], 'captured_invocation_id': state['captured_invocation_id'],
                 'guardian_generation': {'unit': GUARDIAN_UNIT, 'unit_sha256': GUARDIAN_UNIT_SHA256,
                                         'pid': GUARDIAN_PID, 'invocation_id': GUARDIAN_INVOCATION,
                                         'locks': GUARDIAN_LOCKS},
                 'executed': False}
        if not args.execute:
            verify_guardian_generation(owner, receipt)
            return proof
        if args.confirm != 'api.lmm.best':
            raise RuntimeError('repair execution requires --confirm api.lmm.best')
        verify_capture(owner, work, state, maintenance)
        verify_guardian_generation(owner, receipt)
        bound_bytes(work / 'state.json', sha(state_raw), private=True)
        bound_bytes(owner.NGINX_LOCATIONS, OLD_SHA256)
        bound_bytes(work / 'previous-nginx-locations', OLD_SHA256, private=True)
        config_mode = stat.S_IMODE(owner.NGINX_LOCATIONS.lstat().st_mode)
        directory.mkdir(mode=0o700)
        stage = 'ARCHIVE'
        try:
            for name, body in (('original-nginx-locations', original), ('original-state.json', state_raw), ('canonical-nginx-locations', candidate)):
                owner.immutable_write(directory / name, body, 0o600)
            intent = dict(proof, executed=True, intent_at=time.time(), tool_sha256=sha(bound_bytes(Path(__file__))))
            owner.immutable_write(directory / 'intent.json', encoded(intent), 0o600)
            stage = 'NGINX_CONFIG'
            bound_bytes(owner.NGINX_LOCATIONS, OLD_SHA256)
            owner.immutable_write(owner.NGINX_LOCATIONS, candidate, config_mode)
            owner.run('nginx', '-t', log=directory / 'nginx-test.log')
            owner.run('systemctl', 'reload', 'nginx', log=directory / 'nginx-reload.log')
            verify_capture(owner, work, state, maintenance)
            verify_guardian_generation(owner, receipt)
            bound_bytes(owner.NGINX_LOCATIONS, NEW_SHA256)
            bound_bytes(work / 'state.json', sha(state_raw), private=True)
            bound_bytes(work / 'previous-nginx-locations', OLD_SHA256, private=True)
            stage = 'ORIGINAL_EVIDENCE'
            owner.immutable_write(work / 'previous-nginx-locations', candidate, 0o600)
            lineage = dict(intent, canonical_verified=True, preserved_original_path=str(directory / 'original-nginx-locations'),
                           preserved_state_path=str(directory / 'original-state.json'))
            lineage_raw = encoded(lineage)
            owner.immutable_write(directory / 'lineage.json', lineage_raw, 0o600)
            updated = dict(state, ingress_original_sha256=NEW_SHA256,
                           ingress_repair={'path': str(directory / 'lineage.json'), 'sha256': sha(lineage_raw)})
            stage = 'OWNER_STATE'
            bound_bytes(work / 'state.json', sha(state_raw), private=True)
            owner.save(work, updated)
            state_after = bound_bytes(work / 'state.json', private=True)
            if json.loads(state_after) != updated:
                raise RuntimeError('owner state write did not preserve the captured identity')
            bound_bytes(owner.NGINX_LOCATIONS, NEW_SHA256)
            bound_bytes(work / 'previous-nginx-locations', NEW_SHA256, private=True)
            verify_capture(owner, work, updated, maintenance)
            verify_guardian_generation(owner, receipt)
            complete = dict(proof, executed=True, state_after_sha256=sha(state_after), lineage_sha256=sha(lineage_raw),
                            complete_at=time.time(), writer_unchanged=True)
            owner.immutable_write(directory / 'complete.json', encoded(complete), 0o600)
            return complete
        except BaseException as error:
            failure = dict(proof, executed=True, failed_stage=stage, error_type=type(error).__name__, error=str(error),
                           recovery_required=True, replay_forbidden=True, failure_at=time.time())
            owner.immutable_write(directory / 'failure.json', encoded(failure), 0o600)
            raise


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--release', required=True)
    parser.add_argument('--maintenance-handoff', type=Path, required=True)
    parser.add_argument('--maintenance-handoff-sha256', required=True)
    parser.add_argument('--capture-status', type=Path, required=True, help='root-private exact reviewed status snapshot')
    parser.add_argument('--execute', action='store_true')
    parser.add_argument('--confirm')
    args = parser.parse_args(argv)
    if os.geteuid() != 0 or not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,70}', args.release) or not re.fullmatch('[0-9a-f]{64}', args.maintenance_handoff_sha256):
        parser.error('repair requires root, a canonical release ID and an exact handoff SHA256')
    if args.execute and args.confirm != 'api.lmm.best':
        parser.error('--execute requires --confirm api.lmm.best')
    os.umask(0o077)
    print(encoded(repair(args)).decode(), end='')
    return 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (OSError, RuntimeError, ValueError, KeyError) as error:
        print(json.dumps({'ok': False, 'error': str(error), 'repair_replay_forbidden': True}), file=sys.stderr)
        sys.exit(1)
