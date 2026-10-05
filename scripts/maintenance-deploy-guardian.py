#!/usr/bin/env python3
"""Hold a deployment flock across an explicit maintenance handoff.

Each lease receives the guardian's same open file description through
SCM_RIGHTS. Receivers must close it, never issue LOCK_UN. The Unix connection
serializes operations which share that open file description. No lock path is
removed and a crashed client does not release the guardian's flock.
"""
import argparse
import array
import fcntl
import hashlib
import json
import os
import re
from pathlib import Path
import socket
import stat
import struct
import subprocess

FORMAT = 'lmm-credit-maintenance-handoff-v1'
PROTOCOL = 'lmm-maintenance-deploy-lock-v1'
LOCKS = {'native': '/run/lock/lmm-api-go-deploy.lock',
         'systemd': '/var/lib/lmm-api-deploy-systemd/lock',
         'frontend': '/srv/lmm-api-frontend/.release.lock'}


def bound_file(path, expected, uid=0):
    path = Path(path)
    if not path.is_absolute() or '..' in path.parts:
        raise RuntimeError('bound file path must be absolute and canonical')
    for parent in (path, *path.parents):
        info = parent.lstat()
        if stat.S_ISLNK(info.st_mode) or info.st_uid not in (0, uid) or info.st_mode & 0o022:
            raise RuntimeError('bound file ownership or ancestor permissions are unsafe')
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or stat.S_IMODE(info.st_mode) not in (0o600, 0o640):
        raise RuntimeError('bound file must be private, regular and single-linked')
    body = path.read_bytes()
    if len(body) > 1024 * 1024 or hashlib.sha256(body).hexdigest() != expected:
        raise RuntimeError('bound file digest mismatch')
    return body


def handoff(path, expected, uid=0):
    value = json.loads(bound_file(path, expected, uid))
    if value.get('format') != FORMAT or value.get('stage') not in ('prebridge', 'post') or value.get('deployment_tool') not in ('native', 'systemd'):
        raise RuntimeError('unsupported maintenance handoff')
    for name in ('transition_id', 'transition_intent_sha256', 'provider_sha256', 'prepare_config_sha256', 'guardian_socket'):
        if not value.get(name):
            raise RuntimeError('missing maintenance binding: ' + name)
    prepared = json.loads(bound_file(value['prepare_config_path'], value['prepare_config_sha256'], uid))
    for name in ('transition_id', 'transition_intent_sha256', 'provider_sha256'):
        if prepared.get(name) != value[name]:
            raise RuntimeError('prepare configuration differs from maintenance handoff')
    value['_handoff_path'] = str(Path(path))
    return value


def peer_uid(connection):
    return struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))[1]


def receive_line(connection):
    body = bytearray()
    while b'\n' not in body:
        part = connection.recv(4096)
        if not part or len(body) + len(part) > 16384:
            raise RuntimeError('invalid guardian request')
        body += part
    line, trailing = bytes(body).split(b'\n', 1)
    if trailing:
        raise RuntimeError('guardian request has trailing bytes')
    return json.loads(line)


def adopt(value, expected, lock_path, uid=0, with_receipt=False, all_locks=False, lock_paths_override=None):
    """Return (fd, lease). Keep lease open for the whole operation; close only."""
    connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    descriptors = []
    try:
        connection.settimeout(125)
        connection.connect(value['guardian_socket'])
        if peer_uid(connection) != uid:
            raise RuntimeError('guardian peer owner mismatch')
        request = {'protocol': PROTOCOL, 'handoff_sha256': expected,
                   'transition_id': value['transition_id'], 'handoff_path': value['_handoff_path'], 'operation': 'adopt-all' if all_locks else 'adopt'}
        connection.sendall(json.dumps(request).encode() + b'\n')
        count = 3 if all_locks else 1
        body, ancillary, flags, _ = connection.recvmsg(16384, socket.CMSG_SPACE(array.array('i').itemsize * count))
        for level, kind, payload in ancillary:
            if level == socket.SOL_SOCKET and kind == socket.SCM_RIGHTS:
                received = array.array('i')
                received.frombytes(payload[:len(payload) - len(payload) % received.itemsize])
                descriptors.extend(received)
        if len(descriptors) != count or flags & (socket.MSG_TRUNC | socket.MSG_CTRUNC):
            raise RuntimeError('guardian did not provide the requested complete lock descriptor set')
        reply = json.loads(body)
        peer_pid = struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))[0]
        if reply.get('guardian_pid') != peer_pid:
            raise RuntimeError('guardian reply PID differs from actual Unix peer')
        if reply.get('protocol') != PROTOCOL or reply.get('handoff_sha256') != expected or reply.get('transition_id') != value['transition_id']:
            raise RuntimeError('guardian lock identity mismatch')
        paths = lock_paths_override or LOCKS
        transferred = [paths[name] for name in ('native', 'systemd', 'frontend')] if all_locks else [str(lock_path)]
        if all_locks and reply.get('transferred_paths') != transferred:
            raise RuntimeError('guardian did not transfer the three normal owner OFDs')
        for descriptor, path in zip(descriptors, transferred):
            os.set_inheritable(descriptor, False)
            info, path_info = os.fstat(descriptor), os.stat(path, follow_symlinks=False)
            if not stat.S_ISREG(info.st_mode) or info.st_uid != uid or info.st_nlink != 1 or (info.st_dev, info.st_ino) != (path_info.st_dev, path_info.st_ino):
                raise RuntimeError('guardian transferred lock inode differs from its owner path')
        connection.settimeout(None)
        if all_locks:
            return descriptors, connection, reply
        return (descriptors[0], connection, reply) if with_receipt else (descriptors[0], connection)
    except BaseException:
        for descriptor in descriptors:
            os.close(descriptor)
        connection.close()
        raise


def adopt_all(value, expected, uid=0, lock_paths_override=None):
    paths = lock_paths_override or LOCKS
    return adopt(value, expected, paths[value['deployment_tool']], uid, True, True, paths)


def inspect(path, expected, uid=0, lock_paths_override=None):
    value = handoff(path, expected, uid)
    paths = lock_paths_override or LOCKS
    descriptor, lease, reply = adopt(value, expected, paths[value['deployment_tool']], uid, True)
    try:
        received = reply.get('locks', [])
        if len(received) != 3 or {item.get('path') for item in received} != set(paths.values()):
            raise RuntimeError('guardian freeze must cover all three normal owner locks')
        for item in received:
            current = os.stat(item['path'], follow_symlinks=False)
            if not stat.S_ISREG(current.st_mode) or current.st_uid != uid or current.st_nlink != 1 or (current.st_dev, current.st_ino) != (item['device'], item['inode']):
                raise RuntimeError('guardian frozen lock inode changed')
            check = os.open(item['path'], os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
            try:
                try:
                    fcntl.flock(check, fcntl.LOCK_EX | fcntl.LOCK_NB)
                except BlockingIOError:
                    item['held'] = True
                else:
                    raise RuntimeError('guardian freeze lock is independently acquirable')
            finally:
                os.close(check)  # Never unlock the received OFD.
        return {'format': 'lmm-credit-maintenance-guardian-v1',
                'transition_id': value['transition_id'],
                'transition_intent_sha256': value['transition_intent_sha256'],
                'provider_sha256': value['provider_sha256'],
                'prepare_config_sha256': value['prepare_config_sha256'],
                'guardian_pid': reply['guardian_pid'], 'locks': received}
    finally:
        os.close(descriptor)
        lease.close()


def serve(path, expected, uid=0, lock_paths_override=None):
    value = handoff(path, expected, uid)
    paths = lock_paths_override or LOCKS
    descriptors = {}
    listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        for name in ('native', 'systemd', 'frontend'):
            lock_path = Path(paths[name])
            lock_path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            if lock_path.is_symlink():
                raise RuntimeError('deployment lock may not be a symlink')
            descriptor = os.open(lock_path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
            descriptors[name] = descriptor
            info = os.fstat(descriptor)
            if info.st_uid != uid or not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
                raise RuntimeError('deployment lock ownership is unsafe')
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        socket_path = Path(value['guardian_socket'])
        parent = socket_path.parent.lstat()
        if not socket_path.is_absolute() or not stat.S_ISDIR(parent.st_mode) or parent.st_uid != uid or parent.st_mode & 0o077 or socket_path.exists() or socket_path.is_symlink():
            raise RuntimeError('guardian socket needs a new path in an owner-private directory')
        listener.bind(str(socket_path))
        socket_path.chmod(0o600)
        listener.listen(8)
        while True:
            connection, _ = listener.accept()
            with connection:
                try:
                    connection.settimeout(10)
                    request = receive_line(connection)
                    current = handoff(request.get('handoff_path', ''), request.get('handoff_sha256', ''), uid)
                    identities = ('transition_id', 'transition_intent_sha256', 'provider_sha256', 'prepare_config_sha256', 'guardian_socket', 'deployment_tool')
                    if peer_uid(connection) != uid or any(current[name] != value[name] for name in identities) or request.get('protocol') != PROTOCOL or request.get('operation') not in ('adopt', 'adopt-all') or request.get('transition_id') != value['transition_id']:
                        raise RuntimeError('guardian lease binding mismatch')
                    locks = []
                    for name, descriptor in descriptors.items():
                        info = os.fstat(descriptor)
                        path_info = os.stat(paths[name], follow_symlinks=False)
                        if (info.st_dev, info.st_ino) != (path_info.st_dev, path_info.st_ino):
                            raise RuntimeError('frozen lock path was replaced')
                        locks.append({'path': paths[name], 'device': info.st_dev, 'inode': info.st_ino, 'held': True})
                    reply = {'protocol': PROTOCOL, 'handoff_sha256': request['handoff_sha256'],
                             'transition_id': value['transition_id'], 'guardian_pid': os.getpid(), 'locks': locks}
                    names = ('native', 'systemd', 'frontend') if request['operation'] == 'adopt-all' else (value['deployment_tool'],)
                    reply['transferred_paths'] = [paths[name] for name in names]
                    connection.sendmsg([json.dumps(reply).encode()], [(socket.SOL_SOCKET, socket.SCM_RIGHTS, array.array('i', [descriptors[name] for name in names]))])
                    connection.settimeout(None)
                    while connection.recv(1):
                        pass
                except (OSError, RuntimeError, ValueError):
                    continue
    finally:
        listener.close()
        for descriptor in descriptors.values():
            os.close(descriptor)  # No LOCK_UN; surviving receivers keep the OFD lock.


def inspect_writer(path, expected):
    value = handoff(path, expected)
    return inspect_writer_value(value)


def inspect_writer_value(value, uid=0):
    stopped = value.get('stopped_writer')
    if not stopped:
        raise RuntimeError('writer inspection requires a sealed stopped writer receipt')
    command = ['systemctl', 'show', 'lmm-api.service', '--property=MainPID,ExecMainPID,ExecMainCode,ExecMainStatus,ActiveState,SubState,Result,ControlGroup,InvocationID']
    result = subprocess.run(command, capture_output=True, check=True)
    unit = dict(line.split('=', 1) for line in result.stdout.decode().splitlines() if '=' in line)
    expected_values = {'MainPID': '0', 'ExecMainPID': str(stopped['pid']), 'ExecMainCode': '1',
                       'ExecMainStatus': '0', 'ActiveState': 'inactive', 'SubState': 'dead',
                       'Result': 'success', 'ControlGroup': '', 'InvocationID': stopped['invocation_id']}
    if any(unit.get(key) != expected for key, expected in expected_values.items()) or Path('/proc', str(stopped['pid'])).exists():
        raise RuntimeError('writer stopped identity, cgroup or exit evidence changed')
    journal = bound_file(stopped['shutdown_journal_path'], stopped['shutdown_journal_sha256'], uid)
    if b'server exited' not in journal or re.search(rb'\b(?:panic|fatal)\b', journal.lower()):
        raise RuntimeError('writer shutdown evidence is incomplete')
    if value['stage'] == 'post':
        if b'credit_transition_prepare shutdown_complete=true business_enabled=false' not in journal:
            raise RuntimeError('prepared bridge shutdown receipt is missing')
    else:
        reports = re.findall(rb'refund_tasks execution_complete=true accepted=(\d+) finished=(\d+) active=0 failed=0', journal)
        if not reports or reports[-1][0] != reports[-1][1]:
            raise RuntimeError('ordinary writer refund shutdown receipt is missing')
    return {'format': 'lmm-credit-maintenance-writer-v1',
            'transition_id': value['transition_id'], 'transition_intent_sha256': value['transition_intent_sha256'],
            'provider_sha256': value['provider_sha256'], 'prepare_config_sha256': value['prepare_config_sha256'],
            'stopped': True, 'pid': stopped['pid'], 'invocation_id': stopped['invocation_id'], 'unit': unit}


def seal_stopped(path, expected, workspace, stage, output, uid=0, lock_paths_override=None):
    value = handoff(path, expected, uid)
    descriptor, lease = adopt(value, expected, (lock_paths_override or LOCKS)[value['deployment_tool']], uid)
    try:
        workspace = Path(workspace)
        state_path = workspace / ('state/status.json' if value['deployment_tool'] == 'native' else 'state.json')
        # The state is produced by the normal owner under this guardian lease;
        # the caller does not supply or guess its evolving digest.
        state_bytes = state_path.read_bytes()
        state = json.loads(bound_file(state_path, hashlib.sha256(state_bytes).hexdigest(), uid))
        previous_id = state.get('deployment_id') if value['deployment_tool'] == 'native' else state.get('release')
        if workspace.name != previous_id or not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,70}', previous_id or '') or state.get('phase') != 'FROZEN':
            raise RuntimeError('seal-stopped requires the actual normal owner FROZEN workspace')
        for key in ('transition_id', 'transition_intent_sha256', 'provider_sha256'):
            if state.get(key) != value[key]:
                raise RuntimeError('FROZEN owner differs from guardian transition binding')
        if stage == 'post' and state.get('maintenance_confirmation') is not True:
            raise RuntimeError('post seal requires a truly installed and confirmed bridge')
        receipt_path = Path(state['capture_receipt_path'])
        if not receipt_path.is_relative_to(workspace):
            raise RuntimeError('normal owner capture receipt escaped its workspace')
        receipt = json.loads(bound_file(receipt_path, state['capture_receipt_sha256'], uid))
        if receipt.get('format') != 'lmm-credit-maintenance-capture-v1' or receipt.get('phase') != 'FROZEN' or any(receipt.get(key) != value[key] for key in ('transition_id', 'transition_intent_sha256')):
            raise RuntimeError('FROZEN receipt is not the same transition')
        if stage == 'post' and (receipt.get('was_maintenance_confirmed') is not True or receipt.get('provider_sha256') != value['provider_sha256']):
            raise RuntimeError('post N-1 is not the actual confirmed bridge provider')
        for name in ('archived_environment', 'process_environment', 'shutdown_journal'):
            if not Path(receipt[name + '_path']).is_relative_to(workspace):
                raise RuntimeError('normal owner frozen evidence escaped its workspace')
            bound_file(receipt[name + '_path'], receipt[name + '_sha256'], uid)
        enriched = {key: item for key, item in value.items() if not key.startswith('_') and key not in ('path', 'sha256')}
        enriched.update(stage=stage, previous_deployment_id=previous_id,
                        capture_receipt_path=str(receipt_path), capture_receipt_sha256=state['capture_receipt_sha256'],
                        archived_environment_path=receipt['archived_environment_path'], archived_environment_sha256=receipt['archived_environment_sha256'],
                        stopped_writer={'pid': receipt['pid'], 'invocation_id': receipt['invocation_id'],
                                        'shutdown_journal_path': receipt['shutdown_journal_path'], 'shutdown_journal_sha256': receipt['shutdown_journal_sha256']})
        inspect_writer_value(enriched, uid)
        target = Path(output)
        if not target.is_absolute() or str(target) != os.path.normpath(str(target)):
            raise RuntimeError('new handoff path must be absolute and canonical')
        for parent in target.parents:
            info = parent.lstat()
            if not stat.S_ISDIR(info.st_mode) or info.st_uid not in (0, uid) or info.st_mode & 0o022:
                raise RuntimeError('new handoff directory must be sealed and root owned')
        content = json.dumps(enriched, indent=2).encode() + b'\n'
        digest = hashlib.sha256(content).hexdigest()
        reused = False
        try:
            fd = os.open(target, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
        except FileExistsError:
            if bound_file(target, digest, uid) != content:
                raise RuntimeError('existing handoff output differs from reverified FROZEN owner')
            reused = True
        else:
            with os.fdopen(fd, 'wb') as stream:
                stream.write(content); stream.flush(); os.fsync(stream.fileno())
        parent_fd = os.open(target.parent, os.O_DIRECTORY | os.O_CLOEXEC)
        try: os.fsync(parent_fd)
        finally: os.close(parent_fd)
        return {'format': 'lmm-credit-maintenance-handoff-seal-v1', 'handoff_path': str(target), 'handoff_sha256': digest,
                'transition_id': enriched['transition_id'], 'transition_intent_sha256': enriched['transition_intent_sha256'],
                'provider_sha256': enriched['provider_sha256'], 'prepare_config_sha256': enriched['prepare_config_sha256'],
                'previous_deployment_id': previous_id, 'stage': stage, 'reused': reused}
    finally:
        os.close(descriptor); lease.close()


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['serve', 'inspect', 'inspect-writer', 'seal-stopped'])
    parser.add_argument('--handoff', required=True)
    parser.add_argument('--handoff-sha256', required=True)
    parser.add_argument('--workspace')
    parser.add_argument('--stage', choices=['prebridge', 'post'])
    parser.add_argument('--output')
    args = parser.parse_args(argv)
    if os.geteuid() != 0:
        parser.error('guardian must run as root')
    if args.action == 'seal-stopped':
        if not args.workspace or not args.stage or not args.output:
            parser.error('seal-stopped requires --workspace --stage --output')
        print(json.dumps(seal_stopped(args.handoff, args.handoff_sha256, args.workspace, args.stage, args.output)))
    elif args.action == 'inspect-writer':
        print(json.dumps(inspect_writer(args.handoff, args.handoff_sha256)))
    elif args.action == 'inspect':
        print(json.dumps(inspect(args.handoff, args.handoff_sha256)))
    else:
        serve(args.handoff, args.handoff_sha256)


if __name__ == '__main__':
    main()
