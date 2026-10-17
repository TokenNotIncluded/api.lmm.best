#!/usr/bin/env python3
"""A09 resource tests: real local processes, synthetic data, no existing PG.

LMM_A09_TEST_ROOT must be an existing private directory. Artifacts are retained.
Set LMM_A09_TEST_POSTGRES=1 to exercise only the existing pinned Docker fixture.
"""
from __future__ import annotations

import importlib.util
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).with_name('test-resource-lease.py')
SPEC = importlib.util.spec_from_file_location('test_resource_lease', SCRIPT)
LEASE = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = LEASE
SPEC.loader.exec_module(LEASE)
PYTHON = sys.executable

# The child handshake has a bounded fail-safe, not a lease TTL or a killer.
HOLD = """
import os,time
from pathlib import Path
p=Path(os.environ['LMM_TEST_OUTPUT']); (p/'ready').touch()
end=time.monotonic()+15
while not (p/'release').exists():
    if time.monotonic()>end: raise SystemExit(91)
    time.sleep(.02)
"""


# Adapted from the independent review's cancellation/receipt counterexamples.
# The worker really drops inherited FDs; no mock replaces Popen or flock.
DESCENDANT_HOLD = """
import os,time,json
from pathlib import Path
p=Path(os.environ['LMM_TEST_OUTPUT'])
raw=Path('/proc/self/stat').read_text()
(p/'worker.json').write_text(json.dumps({'pid':os.getpid(),
    'start_ticks':int(raw[raw.rfind(')')+2:].split()[19]),
    'boot_id':Path('/proc/sys/kernel/random/boot_id').read_text().strip()}))
(p/'ready').touch()
end=time.monotonic()+15
while not (p/'release').exists():
    if time.monotonic()>end: raise SystemExit(91)
    time.sleep(.02)
"""
FORK_DESCENDANT = "import subprocess,sys\nsubprocess.Popen([sys.executable,'-B','-c',%r],close_fds=True)" % DESCENDANT_HOLD
RECEIPT_EXCEPTION_DRIVER = """
import importlib.util,sys
from pathlib import Path
spec=importlib.util.spec_from_file_location('receipt_exception_lease',sys.argv[1])
l=importlib.util.module_from_spec(spec);sys.modules[spec.name]=l;spec.loader.exec_module(l)
original=l.receipt
def failed_running(directory,phase,values):
    if phase=='running':
        (directory/'output/receipt-failed').touch()
        raise OSError('synthetic EIO while recording running phase')
    return original(directory,phase,values)
l.receipt=failed_running
try:
    l.run(Path(sys.argv[2]),'Receipt',['mise','exec','--',sys.executable,'-B','-c',sys.argv[3]],heavy=True)
except OSError:
    raise SystemExit(2)
"""


# @mark testing.resource-lease role=test
class TestResourceLease(unittest.TestCase):
    def setUp(self):
        parent = Path(os.environ['LMM_A09_TEST_ROOT'])
        LEASE.private_root(parent)
        self.root = Path(tempfile.mkdtemp(prefix='case-', dir=parent))
        self.processes = []
        self.leases = []

    def tearDown(self):
        # Unblock only this test's own workers, including a failed-receipt case
        # where running.json was never written. Never kill a discovered PID.
        for directory in self.leases:
            self.release(directory)
        # Only processes spawned by this test and their recorded exact identities.
        for directory in self.leases:
            running = directory / 'running.json'
            if running.exists():
                expected = json.loads(running.read_text())['child']
                if LEASE.identity_status(expected) == 'active':
                    os.kill(expected['pid'], signal.SIGKILL)
        for proc in self.processes:
            if proc.poll() is None:
                proc.terminate()
            try:
                proc.communicate(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.communicate(timeout=5)

    def start(self, task, code=HOLD, *options, env=None):
        proc = subprocess.Popen(
            [PYTHON, '-B', str(SCRIPT), 'run', '--root', str(self.root), '--task', task,
             '--wait-seconds', '10', *options, '--', 'mise', 'exec', '--',
             PYTHON, '-B', '-c', code],
            env={**os.environ, **(env or {})}, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.processes.append(proc)
        return proc

    def wait_for(self, predicate, timeout=10):
        end = time.monotonic() + timeout
        while time.monotonic() < end:
            value = predicate()
            if value:
                return value
            time.sleep(.02)
        self.fail('bounded process handshake did not complete')

    def directory(self, task):
        def locate():
            matches = list((self.root / task).glob('*/requested.json'))
            return matches[0].parent if matches else None
        directory = self.wait_for(locate)
        self.leases.append(directory)
        return directory

    def ready(self, directory):
        self.wait_for(lambda: (directory / 'output/ready').exists())

    def release(self, directory):
        (directory / 'output/release').touch()

    def result(self, process, code=0):
        out, err = process.communicate(timeout=15)
        self.assertEqual(process.returncode, code, (out, err))
        return out, err

    def test_two_heavy_jobs_serialize_on_shared_flock(self):
        a = self.start('A', HOLD, '--heavy')
        da = self.directory('A'); self.ready(da)
        b = self.start('B', HOLD, '--heavy')
        db = self.directory('B')
        # Receipt proves B has requested while A owns the actual kernel lock.
        probe = LEASE.open_heavy_lock()
        try:
            with self.assertRaises(BlockingIOError):
                LEASE.fcntl.flock(probe, LEASE.fcntl.LOCK_EX | LEASE.fcntl.LOCK_NB)
        finally:
            os.close(probe)
        self.assertFalse((db / 'acquired.json').exists())
        self.release(da); self.result(a)
        self.ready(db)
        self.release(db); self.result(b)
        for directory in (da, db):
            acquired = json.loads((directory / 'acquired.json').read_text())
            self.assertEqual(acquired['lock']['path'], str(LEASE.HEAVY_LOCK))

    def test_light_jobs_overlap_even_while_heavy_is_held(self):
        heavy = self.start('Heavy', HOLD, '--heavy')
        dh = self.directory('Heavy'); self.ready(dh)
        a = self.start('LightA'); da = self.directory('LightA'); self.ready(da)
        b = self.start('LightB'); db = self.directory('LightB'); self.ready(db)
        self.assertIsNone(a.poll()); self.assertIsNone(b.poll()); self.assertIsNone(heavy.poll())
        for directory, proc in ((da, a), (db, b), (dh, heavy)):
            self.release(directory); self.result(proc)

    def test_normal_exit_releases_and_is_recorded(self):
        p = self.start('Normal', 'pass', '--heavy'); d = self.directory('Normal')
        self.result(p)
        self.assertEqual(json.loads((d / 'finished.json').read_text())['exit_code'], 0)
        self.assert_lock_free()

    def assert_lock_free(self):
        fd = LEASE.open_heavy_lock()
        try:
            LEASE.fcntl.flock(fd, LEASE.fcntl.LOCK_EX | LEASE.fcntl.LOCK_NB)
        finally:
            os.close(fd)

    def test_exception_and_signal_child_exit_release(self):
        failing = self.start('Exception', "raise RuntimeError('synthetic')", '--heavy')
        self.directory('Exception'); self.result(failing, 1); self.assert_lock_free()
        killed = self.start('Signal', HOLD, '--heavy'); d = self.directory('Signal'); self.ready(d)
        child = json.loads((d / 'running.json').read_text())['child']
        self.assertEqual(LEASE.identity_status(child), 'active')
        os.kill(child['pid'], signal.SIGKILL)
        self.result(killed, 137); self.assert_lock_free()

    def test_wrapper_kill_does_not_unlock_surviving_cooperative_child(self):
        a = self.start('Killed', HOLD, '--heavy'); da = self.directory('Killed'); self.ready(da)
        a.kill(); self.result(a, -signal.SIGKILL)
        b = self.start('AfterKill', HOLD, '--heavy'); db = self.directory('AfterKill')
        self.assertFalse((db / 'acquired.json').exists())
        self.release(da)
        self.ready(db); self.release(db); self.result(b)
        self.assert_lock_free()

    def test_supervisor_holds_lock_for_descendant_that_closes_inherited_fds(self):
        code = """
import subprocess,sys
subprocess.Popen([sys.executable,'-B','-c',%r], close_fds=True)
""" % HOLD
        p = self.start('Descendant', code, '--heavy')
        d = self.directory('Descendant'); self.ready(d)
        # Direct child exits; the private subreaper still owns the shared lock.
        child = json.loads((d / 'running.json').read_text())['child']
        self.wait_for(lambda: LEASE.identity_status(child) != 'active')
        self.assertIsNone(p.poll())
        fd = LEASE.open_heavy_lock()
        try:
            with self.assertRaises(BlockingIOError):
                LEASE.fcntl.flock(fd, LEASE.fcntl.LOCK_EX | LEASE.fcntl.LOCK_NB)
        finally:
            os.close(fd)
        self.release(d); self.result(p)
        self.assertEqual(json.loads((d / 'finished.json').read_text())['descendants_reaped'], 1)
        self.assert_lock_free()

    def lock_busy(self):
        fd = LEASE.open_heavy_lock()
        try:
            try:
                LEASE.fcntl.flock(fd, LEASE.fcntl.LOCK_EX | LEASE.fcntl.LOCK_NB)
                return False
            except BlockingIOError:
                return True
        finally:
            os.close(fd)

    def assert_worker_still_protected(self, process, directory):
        worker = json.loads((directory / 'output/worker.json').read_text())
        active = LEASE.identity_status(worker) == 'active'
        busy = self.lock_busy()
        observed = {'worker_active': active, 'lock_busy': busy, 'wrapper_exit': process.poll()}
        with (directory / 'output/protection.jsonl').open('a') as log:
            log.write(json.dumps(observed) + '\n')
        # The independent receipt counterexample's original safety predicate.
        self.assertTrue(not active or busy, observed)
        self.assertTrue(active, observed)
        self.assertIsNone(process.poll(), 'wrapper exited before its worker')
        self.assertFalse((directory / 'finished.json').exists())

    def exercise_cancellation(self, signals, *, direct=False):
        code = DESCENDANT_HOLD if direct else FORK_DESCENDANT
        p = self.start('Cancel', code, '--heavy'); d = self.directory('Cancel'); self.ready(d)
        if not direct:
            child = json.loads((d / 'running.json').read_text())['child']
            self.wait_for(lambda: LEASE.identity_status(child) != 'active')
        self.assert_worker_still_protected(p, d)
        worker = json.loads((d / 'output/worker.json').read_text())
        try:
            for sig in signals:
                p.send_signal(sig)
                time.sleep(.05)
                self.assert_worker_still_protected(p, d)
        finally:
            self.release(d)
        self.result(p, 128 + signals[0])
        self.assertNotEqual(LEASE.identity_status(worker), 'active')
        self.assertTrue((d / 'failed.json').is_file())
        self.assertFalse((d / 'finished.json').exists())
        self.assert_lock_free()

    def test_sigterm_cancellation_waits_for_closed_fd_descendant(self):
        self.exercise_cancellation([signal.SIGTERM])

    def test_sigint_cancellation_waits_for_closed_fd_descendant(self):
        self.exercise_cancellation([signal.SIGINT])

    def test_repeated_sigterm_sigint_cannot_interrupt_descendant_drain(self):
        self.exercise_cancellation([signal.SIGTERM, signal.SIGINT, signal.SIGTERM, signal.SIGINT])

    def test_repeated_sigint_sigterm_cannot_interrupt_direct_child_drain(self):
        self.exercise_cancellation([signal.SIGINT, signal.SIGTERM, signal.SIGINT], direct=True)

    def exercise_receipt_error(self, signals=()):
        p = subprocess.Popen([PYTHON, '-B', '-c', RECEIPT_EXCEPTION_DRIVER, str(SCRIPT),
                              str(self.root), FORK_DESCENDANT],
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.processes.append(p)
        d = self.directory('Receipt'); self.ready(d)
        self.wait_for(lambda: (d / 'output/receipt-failed').exists())
        time.sleep(.15)  # Let the real exception unwind, not only its injection marker.
        self.assertFalse((d / 'running.json').exists())
        self.assert_worker_still_protected(p, d)
        worker = json.loads((d / 'output/worker.json').read_text())
        try:
            for sig in signals:
                p.send_signal(sig)
                time.sleep(.05)
                self.assert_worker_still_protected(p, d)
        finally:
            self.release(d)
        # Preserve the real receipt failure, even when cancellation also arrives.
        self.result(p, 2)
        self.assertNotEqual(LEASE.identity_status(worker), 'active')
        self.assertTrue((d / 'failed.json').exists())
        self.assertFalse((d / 'finished.json').exists())
        self.assert_lock_free()

    def test_running_receipt_oserror_drains_closed_fd_descendant_before_unlock(self):
        self.exercise_receipt_error()

    def test_running_receipt_oserror_and_repeated_cancellation_preserve_failure(self):
        self.exercise_receipt_error([signal.SIGINT, signal.SIGTERM, signal.SIGINT, signal.SIGTERM])

    def test_signal_handlers_restored_after_success_and_receipt_error(self):
        previous = {sig: signal.getsignal(sig) for sig in (signal.SIGTERM, signal.SIGINT)}
        LEASE.run(self.root, 'HandlersSuccess', ['mise', 'exec', '--', PYTHON, '-B', '-c', 'pass'])
        self.assertEqual({sig: signal.getsignal(sig) for sig in previous}, previous)
        original = LEASE.receipt

        def failed_running(directory, phase, values):
            if phase == 'running':
                raise OSError('synthetic running receipt error')
            return original(directory, phase, values)

        with patch.object(LEASE, 'receipt', failed_running), self.assertRaises(OSError):
            LEASE.run(self.root, 'HandlersError', ['mise', 'exec', '--', PYTHON, '-B', '-c', 'pass'])
        self.assertEqual({sig: signal.getsignal(sig) for sig in previous}, previous)

    def test_lock_links_and_replaced_inode_are_rejected(self):
        original = self.root / 'original.lock'; original.touch(mode=0o600)
        linked = self.root / 'linked.lock'; linked.symlink_to(original)
        with patch.object(LEASE, 'HEAVY_LOCK', linked), self.assertRaises(OSError):
            LEASE.open_heavy_lock()
        hardlink = self.root / 'hard.lock'; os.link(original, hardlink)
        with patch.object(LEASE, 'HEAVY_LOCK', hardlink), self.assertRaises(LEASE.LeaseError):
            LEASE.open_heavy_lock()
        lock = self.root / 'replace.lock'
        with patch.object(LEASE, 'HEAVY_LOCK', lock):
            fd = LEASE.open_heavy_lock()
            try:
                lock.rename(self.root / 'retained-old.lock')
                lock.touch(mode=0o600)
                with self.assertRaises(LEASE.LeaseError):
                    LEASE.wait_heavy(fd, 0)
            finally:
                os.close(fd)
        self.assertTrue(original.exists())

    def test_lock_timeout_never_kills_holder(self):
        p = self.start('Holder', HOLD, '--heavy'); d = self.directory('Holder'); self.ready(d)
        fd = LEASE.open_heavy_lock()
        try:
            with self.assertRaises(LEASE.LeaseError):
                LEASE.wait_heavy(fd, 0)
        finally:
            os.close(fd)
        self.assertIsNone(p.poll())
        self.release(d); self.result(p)

    def test_pid_reuse_or_boot_change_is_not_reclamation_authority(self):
        real = LEASE.process_identity(os.getpid())
        self.assertEqual(LEASE.identity_status(real), 'active')
        for key, replacement in [('start_ticks', real['start_ticks'] + 1), ('boot_id', 'different-boot')]:
            stale = {**real, key: replacement}
            self.assertEqual(LEASE.identity_status(stale), 'generation_mismatch')
        self.assertEqual(LEASE.process_identity(os.getpid()), real)

    def test_old_receipt_does_not_clear_active_resources(self):
        p = self.start('Old', HOLD, '--heavy'); d = self.directory('Old'); self.ready(d)
        artifact = d / 'target/active-cache'; artifact.write_text('preserve')
        os.utime(d / 'requested.json', (1, 1))
        inspected = subprocess.run([PYTHON, '-B', str(SCRIPT), 'inspect', '--lease', str(d)],
                                   capture_output=True, check=True)
        value = json.loads(inspected.stdout)
        self.assertEqual(value['holder_status'], 'active')
        self.assertFalse(value['reclaim_authorized'])
        self.assertEqual(artifact.read_text(), 'preserve')
        self.assertIsNone(p.poll())
        self.release(d); self.result(p)

    def test_illegal_tasks_and_path_escape_are_rejected(self):
        for task in ('../escape', '/absolute', 'a/b', '.', '', 'a' * 49, 'x\nsecret'):
            with self.subTest(task=task), self.assertRaises(LEASE.LeaseError):
                LEASE.run(self.root, task, ['mise', 'exec', '--', 'true'])
        outside = self.root / 'outside'; outside.mkdir(mode=0o700)
        (self.root / 'Link').symlink_to(outside, target_is_directory=True)
        with self.assertRaises(ValueError):
            LEASE.run(self.root, 'Link', ['mise', 'exec', '--', 'true'])
        with self.assertRaises(LEASE.LeaseError):
            LEASE.private_root(self.root / '..' / self.root.name)
        self.assertEqual(list(outside.iterdir()), [])
        public = self.root / 'public'; public.mkdir(mode=0o700); public.chmod(0o755)
        with self.assertRaises(ValueError):
            LEASE.private_root(public)

    def test_child_has_unique_target_output_and_eight_jobs(self):
        code = """
import os,json
from pathlib import Path
p=Path(os.environ['LMM_TEST_OUTPUT'])
(p/'environment.json').write_text(json.dumps({k:os.environ[k] for k in
    ['CARGO_TARGET_DIR','CARGO_BUILD_JOBS','LMM_TEST_NONCE','MISE_DISABLE_TOOLS']}))
"""
        seen = []
        for _ in range(2):
            p = self.start('SameTask', code); self.result(p)
        for directory in (self.root / 'SameTask').iterdir():
            data = json.loads((directory / 'output/environment.json').read_text())
            self.assertEqual(data['CARGO_TARGET_DIR'], str(directory / 'target'))
            self.assertEqual(data['CARGO_BUILD_JOBS'], '8')
            self.assertEqual(data['LMM_TEST_NONCE'], directory.name)
            self.assertEqual(data['MISE_DISABLE_TOOLS'], 'rust')
            seen.append(data['CARGO_TARGET_DIR'])
        self.assertEqual(len(set(seen)), 2)

    def test_listener_is_held_and_child_adopts_same_socket(self):
        code = """
import os,socket,json
from pathlib import Path
s=socket.socket(fileno=int(os.environ['LMM_TEST_LISTEN_FD']))
c,_=s.accept()
with c:
    c.sendall(b'held-socket')
    c.recv(1)  # Client closes first: no server-side TCP TIME_WAIT rebind claim.
Path(os.environ['LMM_TEST_OUTPUT'],'port.json').write_text(json.dumps(s.getsockname()))
"""
        p = self.start('Socket', code, '--socket'); d = self.directory('Socket')
        self.wait_for(lambda: (d / 'acquired.json').exists())
        binding = json.loads((d / 'acquired.json').read_text())['socket']
        port = binding['port']
        self.assertNotIn(port, LEASE.PROTECTED_PORTS)
        with socket.socket() as competitor:
            with self.assertRaises(OSError):
                competitor.bind(('127.0.0.1', port))
        with socket.create_connection(('127.0.0.1', port), timeout=5) as client:
            self.assertEqual(client.recv(64), b'held-socket')
        self.result(p)
        self.assertEqual(json.loads((d / 'output/port.json').read_text()), ['127.0.0.1', port])
        with socket.socket() as after:
            after.bind(('127.0.0.1', port))

    def test_credentials_not_in_wrapper_output_or_receipts(self):
        secret = 'synthetic-test-only-secret-92'
        code = "import os,sys; print(os.environ['SYNTHETIC_SECRET']); sys.stderr.write(os.environ['SYNTHETIC_SECRET']); raise SystemExit(7)"
        p = self.start('Secret', code, env={'SYNTHETIC_SECRET': secret})
        d = self.directory('Secret'); out, err = self.result(p, 7)
        self.assertNotIn(secret.encode(), out + err)
        for path in d.rglob('*'):
            if path.is_file():
                self.assertNotIn(secret.encode(), path.read_bytes())
        # Parser diagnostics must not echo rejected arguments either.
        invalid = subprocess.run([PYTHON, '-B', str(SCRIPT), '--' + secret], capture_output=True)
        self.assertEqual(invalid.returncode, 2)
        self.assertNotIn(secret.encode(), invalid.stdout + invalid.stderr)

    def test_launch_failure_releases_without_logging_secret(self):
        # Fault injection only at Popen; acquisition and kernel lock are real.
        with patch.object(LEASE.subprocess, 'Popen', side_effect=OSError('synthetic secret')):
            with self.assertRaises(OSError):
                LEASE.run(self.root, 'Launch', ['mise', 'exec', '--', 'true'], heavy=True)
        self.assert_lock_free()
        for path in (self.root / 'Launch').rglob('*.json'):
            self.assertNotIn('synthetic secret', path.read_text())

    def test_nonfinite_timeout_rejected(self):
        for timeout in (float('nan'), float('inf'), -1, 3601):
            with self.assertRaises(LEASE.LeaseError):
                LEASE.run(self.root, 'Timeout', ['mise', 'exec', '--', 'true'], timeout=timeout)

    @unittest.skipUnless(os.environ.get('LMM_A09_TEST_POSTGRES') == '1', 'explicit new Docker fixture only')
    def test_real_new_pg_fixture_runtime_denials_and_identity(self):
        code = """
import json,os,re,subprocess
from pathlib import Path
base=['psql','-XqAt','--no-password','-v','ON_ERROR_STOP=1','-v','VERBOSITY=verbose']
a=subprocess.run(base,input='SELECT current_user,current_database();',text=True,capture_output=True)
b=subprocess.run(base,input='CREATE TABLE public.forbidden(id int);',text=True,capture_output=True)
state=re.search(r'(?:ERROR|FATAL):\\s+([0-9A-Z]{5}):',b.stderr)
Path(os.environ['LMM_TEST_OUTPUT'],'pg-proof.json').write_text(json.dumps({
    'login':a.returncode == 0,'ddl_denied_42501':b.returncode != 0 and '42501' in b.stderr,
    'login_exit':a.returncode,'ddl_exit':b.returncode,'ddl_sqlstate':state[1] if state else None}))
assert a.returncode == 0
assert b.returncode != 0 and '42501' in b.stderr
"""
        p = self.start('A09', code, '--postgres'); self.result(p)
        d = self.directory('A09')
        binding = json.loads((d / 'acquired.json').read_text())['postgres']
        identity = binding['database_identity']
        self.assertFalse(identity['owner_login'])
        self.assertTrue(identity['runtime_low_privilege'])
        self.assertGreater(identity['oid'], 0)
        self.assertNotIn(binding['container_binding']['port'], LEASE.PROTECTED_PORTS)
        self.assertTrue(list(d.glob('a09-pg-*/removed.json')))
        proof = json.loads((d / 'output/pg-proof.json').read_text())
        self.assertTrue(proof['ddl_denied_42501'])
        # Fixture passwords are file-only and never leak to any receipt/output.
        secrets = [p.read_text().strip().split(':')[-1].encode()
                   for p in d.glob('a09-pg-*/A09/*.pgpass')]
        secrets += [line.split('=', 1)[1].encode()
                    for p in d.glob('a09-pg-*/postgres.env') for line in p.read_text().splitlines()
                    if line.startswith('POSTGRES_PASSWORD=')]
        self.assertEqual(len(secrets), 3)
        for secret in secrets:
            for record in d.rglob('*.json'):
                self.assertNotIn(secret, record.read_bytes())


if __name__ == '__main__':
    unittest.main(verbosity=2)
