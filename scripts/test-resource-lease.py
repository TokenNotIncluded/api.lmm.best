#!/usr/bin/env python3
"""Cooperative Linux test-resource wrapper; not a product or maintenance fence.

Run: python3 -B scripts/test-resource-lease.py run --root PRIVATE_DIR --task A01
     [--heavy] [--socket] [--postgres] -- mise exec -- COMMAND ...
The root must already exist, be absolute, owned and 0700 without symlinks.
Child stdout/stderr are discarded: log only deliberately sanitized diagnostics
inside LMM_TEST_OUTPUT. Never put credentials in command arguments. No TTL,
reclamation, cache deletion, external DSN, or existing-cluster adoption exists.
"""
from __future__ import annotations

import argparse
from dataclasses import dataclass
import fcntl
import json
import math
import os
from pathlib import Path
import re
import secrets
import signal
import socket
import stat
import subprocess
import sys
import time

# Reuse the repository's existing A09 fixture and Linux supervision primitives.
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'tests/architecture_completion'))
from build_lease import (  # noqa: E402
    _enable_subreaper, _reap_descendants, observed_limits, private_directory,
    process_identity, receipt,
)
from pg_fixture import Fixture, FixtureError, TASKS  # noqa: E402

HEAVY_LOCK = Path('/tmp/lmm-delegate/heavy-build.lock')
TASK = re.compile(r'[A-Za-z][A-Za-z0-9_-]{0,47}')
PROTECTED_PORTS = {18080, 18081, 50837, 47399}


class LeaseError(RuntimeError):
    """Safe diagnostic; never include command, environment or driver output."""


class LeaseCancelled(LeaseError):
    def __init__(self, signum: int):
        super().__init__('cancelled after supervised processes exited')
        self.signum = signum


def private_root(path: Path) -> None:
    if '..' in path.parts:
        raise LeaseError('invalid private path')
    private_directory(path)


def validate_task(task: str) -> None:
    if not TASK.fullmatch(task):
        raise LeaseError('invalid task identifier')


@dataclass(frozen=True)
class LeaseRequest:
    root: Path
    task: str
    command: tuple[str, ...]
    postgres: bool
    timeout: float

    def __post_init__(self) -> None:
        validate_task(self.task)
        private_root(self.root)
        if not math.isfinite(self.timeout) or not 0 <= self.timeout <= 3600:
            raise LeaseError('invalid lock wait timeout')
        if self.command[:3] != ('mise', 'exec', '--') or len(self.command) < 4:
            raise LeaseError('command must start with mise exec --')
        if self.postgres and self.task not in TASKS:
            raise LeaseError('PostgreSQL tasks must be A01-A10 or S11')


# @mark testing.resource-lease role=implementation
def identity_status(expected: dict) -> str:
    """Observation only: even a mismatch is never authority to reclaim anything."""
    try:
        actual = process_identity(expected['pid'])
        if actual != expected:
            return 'generation_mismatch'
        raw = Path(f'/proc/{expected["pid"]}/stat').read_text()
        return 'exited' if raw[raw.rfind(')') + 2:].split()[0] == 'Z' else 'active'
    except FileNotFoundError:
        return 'not_observed'
    except (OSError, KeyError, TypeError, ValueError):
        return 'unknown'


def open_heavy_lock() -> int:
    # This fixed inode also interoperates with the documented shell flock.
    # Never unlink it, and reject symlinks/hardlinks or a replaced inode.
    parent = HEAVY_LOCK.parent
    if parent.resolve() != parent or not parent.is_dir() or parent.stat().st_uid != os.geteuid():
        raise LeaseError('unsafe heavy lock parent')
    if parent.stat().st_mode & 0o022:
        raise LeaseError('unsafe heavy lock parent')
    fd = os.open(HEAVY_LOCK, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600)
    info = os.fstat(fd)
    if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
            or info.st_mode & 0o077 or info.st_nlink != 1):
        os.close(fd)
        raise LeaseError('unsafe heavy lock')
    return fd


def wait_heavy(fd: int, timeout: float) -> None:
    end = time.monotonic() + timeout
    while True:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            info, current = os.fstat(fd), HEAVY_LOCK.lstat()
            if (info.st_dev, info.st_ino) != (current.st_dev, current.st_ino):
                raise LeaseError('heavy lock inode changed')
            return
        except BlockingIOError:
            if time.monotonic() >= end:
                raise LeaseError('heavy lock busy; no holder was stopped') from None
            time.sleep(min(0.05, max(0, end - time.monotonic())))


def held_socket() -> socket.socket:
    # No close-and-bind handoff: the child must adopt this listening descriptor.
    for _ in range(16):
        listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        listener.bind(('127.0.0.1', 0))
        if listener.getsockname()[1] not in PROTECTED_PORTS:
            listener.listen(16)
            return listener
        listener.close()
    raise LeaseError('no unprotected loopback port selected')


def pg_readback(fixture: Fixture, allocation: dict) -> dict:
    """Register only a newly-created fixture after physical and role readback."""
    fixture.verify()
    database, owner, runtime = (allocation[k] for k in ('database', 'owner', 'runtime'))
    # All names originate in Fixture.allocate, never from an external manifest.
    value = json.loads(fixture.sql(
        "SELECT json_build_object('database',current_database(),'oid',(SELECT oid::bigint FROM pg_database "
        "WHERE datname=current_database()),'owner',(SELECT pg_get_userbyid(datdba) FROM pg_database "
        "WHERE datname=current_database()),'owner_login',(SELECT rolcanlogin FROM pg_roles "
        f"WHERE rolname='{owner}'),'runtime',(SELECT rolname FROM pg_roles WHERE rolname='{runtime}'),"
        "'runtime_low_privilege',(SELECT rolcanlogin AND NOT rolsuper AND NOT rolcreatedb "
        "AND NOT rolcreaterole AND NOT rolreplication AND NOT rolbypassrls "
        f"AND NOT pg_has_role(oid,'{owner}','MEMBER') FROM pg_roles WHERE rolname='{runtime}'));",
        database))
    if (value.get('database') != database or value.get('owner') != owner
            or value.get('runtime') != runtime or value.get('owner_login') is not False
            or value.get('runtime_low_privilege') is not True or not isinstance(value.get('oid'), int)):
        raise LeaseError('fixture role or database identity mismatch')
    return {'nonce': fixture.nonce, 'boot_id': fixture.boot,
            'container_binding': fixture.binding, 'cluster_identity': fixture.identity,
            'database_identity': value, 'unit': 'container:' + fixture.container}


# @mark testing.resource-lease role=supervision
def supervise_command(directory: Path, command: tuple[str, ...], environment: dict[str, str],
                      inherited: tuple[int, ...]) -> tuple[int, int]:
    """Drain our child tree on every path before the caller may release resources.

    Cooperative cancellation waits, never discovers/kills PIDs. The first signal
    determines the cancellation status; repeats cannot unwind a blocking wait.
    An existing error (including a failed receipt) remains an error after drain.
    """
    cancelled: int | None = None

    def request_cancel(signum, _frame):
        nonlocal cancelled
        if cancelled is None:
            cancelled = signum

    previous = {sig: signal.getsignal(sig) for sig in (signal.SIGTERM, signal.SIGINT)}
    try:
        for sig in previous:
            signal.signal(sig, request_cancel)
        _enable_subreaper()  # Establish ownership before any child can be spawned.
        if cancelled is not None:
            raise LeaseCancelled(cancelled)
        try:
            with subprocess.Popen(command, env=environment, pass_fds=inherited,
                                  stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                                  stderr=subprocess.DEVNULL) as child:
                try:
                    child_identity = process_identity(child.pid)
                except FileNotFoundError:
                    child_identity = {'pid': child.pid, 'status': 'exited_before_observation'}
                receipt(directory, 'running', {'child': child_identity})
                code = child.wait()
        finally:
            # Popen waits for the direct child even if receipt() raises. Adopted
            # close_fds descendants must also exit before the outer FD finally.
            descendants = _reap_descendants()
        if cancelled is not None:
            raise LeaseCancelled(cancelled)
        return code, descendants
    finally:
        # Restore only after all waits, so repeat SIGTERM/SIGINT cannot expose
        # a live descendant by interrupting either normal or exceptional drain.
        for sig, handler in previous.items():
            signal.signal(sig, handler)


def run(root: Path, task: str, command: list[str], *, heavy=False, use_socket=False,
        postgres=False, timeout=600.0) -> int:
    request = LeaseRequest(root, task, tuple(command), postgres, timeout)
    task_root = request.root / request.task
    try:
        task_root.mkdir(mode=0o700)
    except FileExistsError:
        pass
    private_root(task_root)
    nonce = secrets.token_hex(16)
    directory = task_root / nonce
    directory.mkdir(mode=0o700)
    for name in ('output', 'target'):
        (directory / name).mkdir(mode=0o700)
    binding = {'task': task, 'nonce': nonce, 'holder': process_identity(os.getpid()),
               'heavy': heavy, 'output': str(directory / 'output'),
               'target': str(directory / 'target'), 'limits': observed_limits()}
    receipt(directory, 'requested', binding)
    fd, listener, fixture = None, None, None
    try:
        if heavy:
            fd = open_heavy_lock()
            wait_heavy(fd, timeout)
            info = os.fstat(fd)
            binding['lock'] = {'path': str(HEAVY_LOCK), 'device': info.st_dev, 'inode': info.st_ino}
        environment = {**os.environ, 'MISE_DISABLE_TOOLS': 'rust', 'CARGO_BUILD_JOBS': '8',
                       'CARGO_TARGET_DIR': str(directory / 'target'),
                       'LMM_TEST_OUTPUT': str(directory / 'output'),
                       'LMM_TEST_LEASE': str(directory), 'LMM_TEST_NONCE': nonce}
        inherited = [] if fd is None else [fd]
        if use_socket:
            listener = held_socket()
            inherited.append(listener.fileno())
            environment.update(LMM_TEST_LISTEN_FD=str(listener.fileno()),
                               LMM_TEST_PORT=str(listener.getsockname()[1]))
            binding['socket'] = {'address': '127.0.0.1', 'port': listener.getsockname()[1],
                                 'handoff': 'inherited_listening_fd'}
        if postgres:
            fixture = Fixture(directory)
            fixture.start()  # Existing runner accepts no DSN/cluster to adopt.
            allocation = fixture.allocate(task)
            binding['postgres'] = pg_readback(fixture, allocation)
            environment.update(PGHOST='127.0.0.1', PGPORT=str(allocation['port']),
                               PGDATABASE=allocation['database'], PGUSER=allocation['runtime'],
                               PGPASSFILE=str(fixture.root / task / 'runtime.pgpass'))
            # Prevent inherited credentials/services from overriding this fixture.
            for key in list(environment):
                if key.startswith('PG') and key not in {'PGHOST', 'PGPORT', 'PGDATABASE', 'PGUSER', 'PGPASSFILE'}:
                    del environment[key]
            environment.pop('DATABASE_URL', None)
            environment.pop('SQL_DSN', None)
        receipt(directory, 'acquired', binding)
        # No raw commands, argv hashes (low-entropy secrets), environment, or child
        # diagnostics in receipts. Child-owned files still require caller hygiene.
        code, descendants = supervise_command(directory, request.command, environment, tuple(inherited))
        if fixture is not None:
            if pg_readback(fixture, allocation) != binding['postgres']:
                raise LeaseError('fixture changed during child execution')
            # Existing fixture fences and refuses cleanup with any live client.
            fixture.cleanup()
        receipt(directory, 'finished', {'exit_code': code, 'descendants_reaped': descendants})
        return code if code >= 0 else 128 - code
    except BaseException:
        receipt(directory, 'failed', {'status': 'retained_for_review',
                                     'postgres_may_remain': fixture is not None})
        raise
    finally:
        if listener is not None:
            listener.close()
        if fd is not None:
            # Never LOCK_UN: a surviving cooperative child may retain this FD.
            os.close(fd)


class SafeParser(argparse.ArgumentParser):
    def error(self, message):
        self.exit(2, 'lease: invalid arguments (see --help)\n')


def main() -> int:
    parser = SafeParser(description=__doc__)
    actions = parser.add_subparsers(dest='action', required=True)
    execute = actions.add_parser('run')
    execute.add_argument('--root', required=True, type=Path)
    execute.add_argument('--task', required=True)
    execute.add_argument('--heavy', action='store_true')
    execute.add_argument('--socket', action='store_true')
    execute.add_argument('--postgres', action='store_true')
    execute.add_argument('--wait-seconds', type=float, default=600)
    execute.add_argument('command', nargs=argparse.REMAINDER)
    inspect = actions.add_parser('inspect')
    inspect.add_argument('--lease', required=True, type=Path)
    args = parser.parse_args()
    try:
        if args.action == 'inspect':
            private_root(args.lease)
            data = json.loads((args.lease / 'requested.json').read_text())
            print(json.dumps({'holder_status': identity_status(data['holder']),
                              'reclaim_authorized': False,
                              'finished_record': (args.lease / 'finished.json').is_file()}))
            return 0
        command = args.command[1:] if args.command[:1] == ['--'] else args.command
        return run(args.root, args.task, command, heavy=args.heavy, use_socket=args.socket,
                   postgres=args.postgres, timeout=args.wait_seconds)
    except LeaseCancelled as error:
        print('lease: cancelled after supervised processes exited', file=sys.stderr)
        return 128 + error.signum
    except (OSError, ValueError, LeaseError, FixtureError, KeyError, TypeError):
        print('lease: operation failed; retained receipts require review', file=sys.stderr)
        return 2


if __name__ == '__main__':
    raise SystemExit(main())
