import importlib.util
from pathlib import Path
import tempfile
import unittest
import multiprocessing
import os
import fcntl
import hashlib
import json
import time

spec = importlib.util.spec_from_file_location('guardian', Path(__file__).with_name('maintenance-deploy-guardian.py'))
guardian = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guardian)


class GuardianTests(unittest.TestCase):
    def test_same_open_description_survives_client_crash_and_freezes_three_owners(self):
        cache = Path.home() / '.cache'
        with tempfile.TemporaryDirectory(prefix='guardian-test-', dir=cache) as directory:
            root = Path(directory)
            uid = os.getuid()
            paths = {name: str(root / (name + '.lock')) for name in guardian.LOCKS}
            prepared = {'format': 'lmm-credit-transition-prepare-v1', 'transition_id': 'fixture',
                        'transition_intent_sha256': 'a' * 64, 'provider_sha256': 'b' * 64}
            configuration = root / 'prepare.json'
            configuration.write_text(json.dumps(prepared)); configuration.chmod(0o600)
            value = dict(prepared, format=guardian.FORMAT, stage='prebridge', deployment_tool='native',
                         prepare_config_path=str(configuration), prepare_config_sha256=hashlib.sha256(configuration.read_bytes()).hexdigest(),
                         guardian_socket=str(root / 'guardian.sock'))
            receipt = root / 'handoff.json'
            receipt.write_text(json.dumps(value)); receipt.chmod(0o600)
            expected = hashlib.sha256(receipt.read_bytes()).hexdigest()
            process = multiprocessing.get_context('fork').Process(target=guardian.serve, args=(receipt, expected, uid, paths))
            process.start()
            try:
                deadline = time.monotonic() + 5
                while not Path(value['guardian_socket']).exists() and time.monotonic() < deadline:
                    time.sleep(0.01)
                bound = guardian.handoff(receipt, expected, uid)
                descriptor, lease = guardian.adopt(bound, expected, paths['native'], uid)
                native_inode = os.fstat(descriptor).st_ino
                self.assertEqual(native_inode, os.stat(paths['native']).st_ino)
                for path in paths.values():
                    independent = os.open(path, os.O_RDWR)
                    try:
                        with self.assertRaises(BlockingIOError):
                            fcntl.flock(independent, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    finally:
                        os.close(independent)
                # A receiver disappears without unlocking the guardian OFD.
                os.close(descriptor); lease.close()
                inspected = guardian.inspect(receipt, expected, uid, paths)
                self.assertEqual(guardian.PROTOCOL, 'lmm-maintenance-deploy-lock-v1')
                self.assertEqual(process.pid, inspected['guardian_pid'])
                self.assertEqual(3, len(inspected['locks']))
                self.assertTrue(all(item['held'] for item in inspected['locks']))
                descriptor, lease = guardian.adopt(bound, expected, paths['native'], uid)
                # Even guardian death cannot release the adopted selected OFD.
                process.terminate(); process.join(5)
                independent = os.open(paths['native'], os.O_RDWR)
                try:
                    with self.assertRaises(BlockingIOError):
                        fcntl.flock(independent, fcntl.LOCK_EX | fcntl.LOCK_NB)
                finally:
                    os.close(independent); os.close(descriptor); lease.close()
                independent = os.open(paths['native'], os.O_RDWR)
                fcntl.flock(independent, fcntl.LOCK_EX | fcntl.LOCK_NB)
                os.close(independent)
            finally:
                if process.is_alive():
                    process.terminate(); process.join(5)

    def test_bound_metadata_rejects_replaced_file_and_unsafe_mode(self):
        with tempfile.TemporaryDirectory(prefix='guardian-binding-', dir=Path.home() / '.cache') as directory:
            path = Path(directory) / 'binding'
            path.write_bytes(b'bound'); path.chmod(0o600)
            expected = hashlib.sha256(path.read_bytes()).hexdigest()
            self.assertEqual(b'bound', guardian.bound_file(path, expected, os.getuid()))
            path.write_bytes(b'replaced')
            with self.assertRaises(RuntimeError):
                guardian.bound_file(path, expected, os.getuid())
            path.write_bytes(b'bound'); path.chmod(0o666)
            with self.assertRaises(RuntimeError):
                guardian.bound_file(path, expected, os.getuid())


if __name__ == '__main__':
    unittest.main()
