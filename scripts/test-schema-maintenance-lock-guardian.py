import array
from contextlib import contextmanager
import fcntl
import hashlib
import importlib.util
import json
import multiprocessing
import os
from pathlib import Path
import socket
import tempfile
import time
import unittest


spec = importlib.util.spec_from_file_location('schema_guardian', Path(__file__).with_name('schema-maintenance-lock-guardian.py'))
guardian = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guardian)


class SchemaGuardianTests(unittest.TestCase):
    def private(self, path, value):
        path.write_bytes(value if isinstance(value, bytes) else json.dumps(value).encode())
        path.chmod(0o600)
        return hashlib.sha256(path.read_bytes()).hexdigest()

    @contextmanager
    def fixture(self):
        with tempfile.TemporaryDirectory(prefix='schema-lock-test-') as temporary:
            root = Path(temporary) / 'fixture'
            state = root / 'state'
            state.mkdir(mode=0o700, parents=True)
            root.chmod(0o700)
            plan = root / 'schema-maintenance-plan.json'
            plan_sha = self.private(plan, {'transition_id': root.name})
            value = {'format': guardian.FORMAT, 'transition_id': root.name,
                     'plan_path': str(plan), 'plan_sha256': plan_sha, 'host': 'fixture-host',
                     'workspace': str(root), 'guardian_socket': str(state / 'guardian.sock')}
            binding = state / 'guardian-binding.json'
            expected = self.private(binding, value)
            locks = {name: str(root / (name + '.lock')) for name in guardian.LOCKS}
            yield root, value, binding, expected, locks

    @contextmanager
    def running(self):
        with self.fixture() as inputs:
            root, value, binding, expected, locks = inputs
            process = multiprocessing.get_context('fork').Process(
                target=guardian.serve, args=(binding, expected, os.getuid(), locks, 'fixture-host'))
            process.start()
            try:
                deadline = time.monotonic() + 5
                while not Path(value['guardian_socket']).exists() and time.monotonic() < deadline:
                    self.assertTrue(process.is_alive())
                    time.sleep(0.01)
                self.assertTrue(Path(value['guardian_socket']).exists())
                yield *inputs, process
            finally:
                if process.is_alive():
                    process.terminate()
                process.join(5)
                self.assertFalse(process.is_alive())

    def adopt(self, value, binding, expected, **changes):
        connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        connection.settimeout(2)
        connection.connect(value['guardian_socket'])
        request = {'protocol': guardian.PROTOCOL, 'operation': 'adopt-all',
                   'binding_path': str(binding), 'binding_sha256': expected,
                   'transition_id': value['transition_id'], 'plan_sha256': value['plan_sha256']}
        request.update(changes)
        connection.sendall(json.dumps(request).encode() + b'\n')
        body, ancillary, flags, _ = connection.recvmsg(16384, socket.CMSG_SPACE(12))
        descriptors = []
        for level, kind, payload in ancillary:
            if level == socket.SOL_SOCKET and kind == socket.SCM_RIGHTS:
                received = array.array('i')
                received.frombytes(payload)
                descriptors.extend(received)
        return connection, body, descriptors, flags

    def assert_locked(self, path):
        descriptor = os.open(path, os.O_RDWR)
        try:
            with self.assertRaises(BlockingIOError):
                fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        finally:
            os.close(descriptor)

    def test_real_scm_descriptors_keep_all_three_locks_after_guardian_exit(self):
        with self.running() as (_, value, binding, expected, locks, process):
            connection, body, descriptors, flags = self.adopt(value, binding, expected)
            try:
                reply = json.loads(body)
                self.assertEqual(3, len(descriptors))
                self.assertEqual(0, flags & (socket.MSG_TRUNC | socket.MSG_CTRUNC))
                self.assertEqual(process.pid, reply['guardian_pid'])
                self.assertEqual(list(locks.values()), reply['transferred_paths'])
                self.assertEqual(value['plan_sha256'], reply['plan_sha256'])
                for descriptor, item in zip(descriptors, reply['locks']):
                    info = os.fstat(descriptor)
                    self.assertEqual((info.st_dev, info.st_ino), (item['device'], item['inode']))
                    self.assert_locked(item['path'])
                process.terminate()
                process.join(5)
                for path in locks.values():
                    self.assert_locked(path)
            finally:
                for descriptor in descriptors:
                    os.close(descriptor)
                connection.close()
            for path in locks.values():
                descriptor = os.open(path, os.O_RDWR)
                try:
                    fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
                finally:
                    os.close(descriptor)

    def test_wrong_plan_and_partial_lock_request_receive_no_descriptors(self):
        for changes in ({'plan_sha256': 'a' * 64}, {'operation': 'adopt'}, {'extra': True}):
            with self.subTest(changes=changes), self.running() as (_, value, binding, expected, locks, _):
                connection, body, descriptors, _ = self.adopt(value, binding, expected, **changes)
                try:
                    self.assertEqual(b'', body)
                    self.assertEqual([], descriptors)
                    for path in locks.values():
                        self.assert_locked(path)
                finally:
                    connection.close()

    def test_changed_binding_is_refused_without_releasing_locks(self):
        with self.running() as (_, value, binding, expected, locks, _):
            self.private(binding, dict(value, host='changed-host'))
            connection, body, descriptors, _ = self.adopt(value, binding, expected)
            try:
                self.assertEqual(b'', body)
                self.assertEqual([], descriptors)
                for path in locks.values():
                    self.assert_locked(path)
            finally:
                connection.close()

    def test_replaced_lock_inode_is_refused(self):
        with self.running() as (_, value, binding, expected, locks, _):
            first = Path(locks['native'])
            first.rename(first.with_suffix('.original'))
            self.private(first, b'')
            connection, body, descriptors, _ = self.adopt(value, binding, expected)
            try:
                self.assertEqual(b'', body)
                self.assertEqual([], descriptors)
                self.assert_locked(str(first.with_suffix('.original')))
                self.assert_locked(locks['systemd'])
                self.assert_locked(locks['frontend'])
            finally:
                connection.close()

    def test_foreign_lock_is_never_stolen(self):
        with self.fixture() as (_, value, binding, expected, locks):
            descriptor = os.open(locks['native'], os.O_CREAT | os.O_RDWR, 0o600)
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
            try:
                with self.assertRaises(BlockingIOError):
                    guardian.serve(binding, expected, os.getuid(), locks, 'fixture-host')
                self.assertFalse(Path(value['guardian_socket']).exists())
                self.assert_locked(locks['native'])
            finally:
                os.close(descriptor)

    def test_financial_binding_and_noncanonical_paths_are_not_schema_authority(self):
        with self.fixture() as (_, value, binding, expected, _):
            for invalid in (dict(value, format='lmm-credit-maintenance-handoff-v1'),
                            dict(value, guardian_socket='/tmp/unbound.sock'),
                            dict(value, plan_path='/tmp/unbound-plan.json')):
                digest = self.private(binding, invalid)
                with self.assertRaises(RuntimeError):
                    guardian.binding(binding, digest, os.getuid())


if __name__ == '__main__':
    unittest.main()
