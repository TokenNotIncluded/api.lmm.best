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


def adopt(value, expected, lock_path, uid=0, with_receipt=False):
    """Return (fd, lease). Keep lease open for the whole operation; close only."""
    connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    descriptor = None
    try:
        connection.settimeout(125)
        connection.connect(value['guardian_socket'])
        if peer_uid(connection) != uid:
            raise RuntimeError('guardian peer owner mismatch')
        request = {'protocol': PROTOCOL, 'handoff_sha256': expected,
                   'transition_id': value['transition_id'], 'handoff_path': value['_handoff_path'], 'operation': 'adopt'}
        connection.sendall(json.dumps(request).encode() + b'\n')
        body, ancillary, flags, _ = connection.recvmsg(16384, socket.CMSG_SPACE(array.array('i').itemsize))
        descriptors = []
        for level, kind, payload in ancillary:
            if level == socket.SOL_SOCKET and kind == socket.SCM_RIGHTS:
                received = array.array('i')
                received.frombytes(payload[:len(payload) - len(payload) % received.itemsize])
                descriptors.extend(received)
        if len(descriptors) != 1 or flags & (socket.MSG_TRUNC | socket.MSG_CTRUNC):
            for item in descriptors:
                os.close(item)
            raise RuntimeError('guardian did not provide one complete lock descriptor')
        descriptor = descriptors[0]
        os.set_inheritable(descriptor, False)
        reply = json.loads(body)
        peer_pid = struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))[0]
        if reply.get('guardian_pid') != peer_pid:
            raise RuntimeError('guardian reply PID differs from actual Unix peer')
        info, path_info = os.fstat(descriptor), os.stat(lock_path, follow_symlinks=False)
        if reply.get('protocol') != PROTOCOL or reply.get('handoff_sha256') != expected or reply.get('transition_id') != value['transition_id'] or not stat.S_ISREG(info.st_mode) or info.st_uid != uid or info.st_nlink != 1 or (info.st_dev, info.st_ino) != (path_info.st_dev, path_info.st_ino):
            raise RuntimeError('guardian lock identity mismatch')
        connection.settimeout(None)
        return (descriptor, connection, reply) if with_receipt else (descriptor, connection)
    except BaseException:
        if descriptor is not None:
            os.close(descriptor)
        connection.close()
        raise


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
                    if peer_uid(connection) != uid or any(current[name] != value[name] for name in identities) or request.get('protocol') != PROTOCOL or request.get('operation') != 'adopt' or request.get('transition_id') != value['transition_id']:
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
                    connection.sendmsg([json.dumps(reply).encode()], [(socket.SOL_SOCKET, socket.SCM_RIGHTS, array.array('i', [descriptors[value['deployment_tool']]]))])
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
    journal = bound_file(stopped['shutdown_journal_path'], stopped['shutdown_journal_sha256'])
    if b'server exited' not in journal:
        raise RuntimeError('writer shutdown evidence is incomplete')
    return {'format': 'lmm-credit-maintenance-writer-v1',
            'transition_id': value['transition_id'], 'transition_intent_sha256': value['transition_intent_sha256'],
            'provider_sha256': value['provider_sha256'], 'prepare_config_sha256': value['prepare_config_sha256'],
            'stopped': True, 'pid': stopped['pid'], 'invocation_id': stopped['invocation_id'], 'unit': unit}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['serve', 'inspect', 'inspect-writer'])
    parser.add_argument('--handoff', required=True)
    parser.add_argument('--handoff-sha256', required=True)
    args = parser.parse_args(argv)
    if os.geteuid() != 0:
        parser.error('guardian must run as root')
    if args.action == 'inspect-writer':
        print(json.dumps(inspect_writer(args.handoff, args.handoff_sha256)))
    elif args.action == 'inspect':
        print(json.dumps(inspect(args.handoff, args.handoff_sha256)))
    else:
        serve(args.handoff, args.handoff_sha256)


if __name__ == '__main__':
    main()
