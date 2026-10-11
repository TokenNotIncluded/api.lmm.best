#!/usr/bin/python3
"""Keep the three native file locks during an explicit schema maintenance.

This is a file-lock transport, not DDL permission. The signed native controller
independently validates its plan, the live owner and every host observation.
Receivers close SCM_RIGHTS descriptors; they must never call LOCK_UN on them.
"""
import argparse
import array
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import socket
import stat
import struct


FORMAT = 'lmm-schema-maintenance-lock-binding-v1'
PROTOCOL = 'lmm-schema-maintenance-lock-v1'
LOCKS = {'native': '/run/lock/lmm-api-go-deploy.lock',
         'systemd': '/var/lib/lmm-api-deploy-systemd/lock',
         'frontend': '/srv/lmm-api-frontend/.release.lock'}
ID = re.compile(r'[A-Za-z0-9][A-Za-z0-9._-]{0,70}')
SHA = re.compile(r'[0-9a-f]{64}')


def unique_object(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError('duplicate JSON member')
        value[key] = item
    return value


def private_file(path, expected, uid):
    path = Path(path)
    if not path.is_absolute() or str(path) != os.path.normpath(str(path)) or not SHA.fullmatch(expected):
        raise RuntimeError('schema lock binding path or digest is invalid')
    for parent in path.parents:
        info = parent.lstat()
        sticky = info.st_uid == 0 and info.st_mode & stat.S_ISVTX
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid not in (0, uid)
                or info.st_mode & 0o022 and not sticky):
            raise RuntimeError('schema lock binding ancestor is replaceable')
    before = path.lstat()
    if (not stat.S_ISREG(before.st_mode) or before.st_uid != uid
            or before.st_nlink != 1 or stat.S_IMODE(before.st_mode) != 0o600):
        raise RuntimeError('schema lock binding is not private and single linked')
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        opened = os.fstat(descriptor)
        if (before.st_dev, before.st_ino) != (opened.st_dev, opened.st_ino):
            raise RuntimeError('schema lock binding changed before open')
        with os.fdopen(os.dup(descriptor), 'rb') as stream:
            body = stream.read(1048577)
        after, current = os.fstat(descriptor), path.lstat()
        profiles = lambda info: (info.st_dev, info.st_ino, info.st_mode, info.st_uid,
                                 info.st_gid, info.st_nlink, info.st_size, info.st_mtime_ns,
                                 info.st_ctime_ns)
        if (profiles(opened) != profiles(after) or profiles(after) != profiles(current)
                or len(body) > 1048576 or hashlib.sha256(body).hexdigest() != expected):
            raise RuntimeError('schema lock binding changed while read')
        return body
    finally:
        os.close(descriptor)


def binding(path, expected, uid=0):
    value = json.loads(private_file(path, expected, uid), object_pairs_hook=unique_object)
    fields = {'format', 'transition_id', 'plan_path', 'plan_sha256', 'host', 'workspace', 'guardian_socket'}
    if (set(value) != fields or value['format'] != FORMAT
            or not ID.fullmatch(value['transition_id']) or not SHA.fullmatch(value['plan_sha256'])
            or not re.fullmatch(r'[a-z0-9][a-z0-9._-]{0,62}', value['host'])):
        raise RuntimeError('schema lock binding identity is invalid')
    root = Path(value['workspace'])
    if not root.is_absolute() or str(root) != os.path.normpath(str(root)) or root.name != value['transition_id']:
        raise RuntimeError('schema lock workspace is not canonical')
    if (str(path) != str(root / 'state' / 'guardian-binding.json')
            or value['guardian_socket'] != str(root / 'state' / 'guardian.sock')
            or value['plan_path'] != str(root / 'schema-maintenance-plan.json')):
        raise RuntimeError('schema lock files escaped their fixed workspace')
    plan = json.loads(private_file(value['plan_path'], value['plan_sha256'], uid),
                      object_pairs_hook=unique_object)
    if plan.get('transition_id') != value['transition_id']:
        raise RuntimeError('schema lock binding belongs to another immutable plan')
    return value


def receive_request(connection):
    body = bytearray()
    while b'\n' not in body:
        chunk = connection.recv(4096)
        if not chunk or len(body) + len(chunk) > 16384:
            raise RuntimeError('schema guardian request is incomplete or oversized')
        body.extend(chunk)
    row, trailing = bytes(body).split(b'\n', 1)
    if trailing:
        raise RuntimeError('schema guardian request has trailing bytes')
    return json.loads(row, object_pairs_hook=unique_object)


def peer_uid(connection):
    return struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))[1]


def serve(path, expected, uid=0, lock_paths=None, observed_host=None):
    value = binding(path, expected, uid)
    if value['host'] != (observed_host or socket.gethostname()):
        raise RuntimeError('schema guardian is bound to another actual host')
    paths = lock_paths or LOCKS
    if tuple(paths) != tuple(LOCKS):
        raise RuntimeError('schema guardian requires all three ordered native locks')
    descriptors = {}
    listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        for name, lock_path in paths.items():
            lock = Path(lock_path)
            info = lock.parent.lstat()
            if not stat.S_ISDIR(info.st_mode) or info.st_uid not in (0, uid) or info.st_mode & 0o022:
                raise RuntimeError('schema lock parent is unsafe')
            descriptor = os.open(lock, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
            descriptors[name] = descriptor
            info, current = os.fstat(descriptor), lock.lstat()
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != uid or info.st_nlink != 1
                    or (info.st_dev, info.st_ino) != (current.st_dev, current.st_ino)):
                raise RuntimeError('schema lock file is unsafe or replaced')
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        destination = Path(value['guardian_socket'])
        info = destination.parent.lstat()
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid != uid
                or stat.S_IMODE(info.st_mode) != 0o700 or destination.exists() or destination.is_symlink()):
            raise RuntimeError('schema guardian socket destination is occupied or unsafe')
        listener.bind(str(destination))
        destination.chmod(0o600)
        listener.listen(8)
        while True:
            connection, _ = listener.accept()
            with connection:
                try:
                    connection.settimeout(10)
                    request = receive_request(connection)
                    fields = {'protocol', 'operation', 'binding_path', 'binding_sha256',
                              'transition_id', 'plan_sha256'}
                    if (set(request) != fields or peer_uid(connection) != uid
                            or request['protocol'] != PROTOCOL or request['operation'] != 'adopt-all'
                            or request['binding_path'] != str(path) or request['binding_sha256'] != expected
                            or request['transition_id'] != value['transition_id']
                            or request['plan_sha256'] != value['plan_sha256']
                            or binding(path, expected, uid) != value):
                        raise RuntimeError('schema guardian lease binding differs')
                    locks = []
                    for name, descriptor in descriptors.items():
                        info, current = os.fstat(descriptor), os.stat(paths[name], follow_symlinks=False)
                        if (not stat.S_ISREG(current.st_mode) or current.st_uid != uid or current.st_nlink != 1
                                or (info.st_dev, info.st_ino) != (current.st_dev, current.st_ino)):
                            raise RuntimeError('schema guardian lock path changed')
                        locks.append({'path': paths[name], 'device': info.st_dev, 'inode': info.st_ino, 'held': True})
                    reply = {'protocol': PROTOCOL, 'binding_sha256': expected,
                             'transition_id': value['transition_id'], 'plan_sha256': value['plan_sha256'],
                             'guardian_pid': os.getpid(), 'transferred_paths': list(paths.values()), 'locks': locks}
                    connection.sendmsg([json.dumps(reply).encode()],
                                       [(socket.SOL_SOCKET, socket.SCM_RIGHTS, array.array('i', descriptors.values()))])
                    connection.settimeout(None)
                    while connection.recv(1):
                        raise RuntimeError('schema guardian lease must only be closed')
                except (OSError, RuntimeError, ValueError, KeyError, TypeError):
                    continue
    finally:
        listener.close()
        for descriptor in descriptors.values():
            os.close(descriptor)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binding', required=True)
    parser.add_argument('--binding-sha256', required=True)
    args = parser.parse_args(argv)
    if os.geteuid() != 0 or not __import__('sys').flags.isolated:
        parser.error('run with root /usr/bin/python3 -I')
    serve(args.binding, args.binding_sha256)


if __name__ == '__main__':
    main()
