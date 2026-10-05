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
from contextlib import contextmanager
from unittest import mock

spec = importlib.util.spec_from_file_location('guardian', Path(__file__).with_name('maintenance-deploy-guardian.py'))
guardian = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guardian)


class GuardianTests(unittest.TestCase):
    @staticmethod
    def private_file(path, content):
        path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        path.write_bytes(content if isinstance(content, bytes) else json.dumps(content).encode())
        path.chmod(0o600)
        return hashlib.sha256(path.read_bytes()).hexdigest()

    @contextmanager
    def running_guardian(self, tool='native'):
        with tempfile.TemporaryDirectory(prefix='guardian-seal-', dir=Path.home() / '.cache') as directory:
            root = Path(directory)
            paths = {name: str(root / (name + '.lock')) for name in guardian.LOCKS}
            prepared = {'format': 'lmm-credit-transition-prepare-v1', 'transition_id': 'fixture',
                        'transition_intent_sha256': 'a' * 64, 'provider_sha256': 'b' * 64}
            configuration = root / 'prepare.json'
            configuration_sha256 = self.private_file(configuration, prepared)
            value = dict(prepared, format=guardian.FORMAT, stage='prebridge', deployment_tool=tool,
                         prepare_config_path=str(configuration), prepare_config_sha256=configuration_sha256,
                         guardian_socket=str(root / 'guardian.sock'))
            original = root / 'original-handoff.json'
            expected = self.private_file(original, value)
            process = multiprocessing.get_context('fork').Process(
                target=guardian.serve, args=(original, expected, os.getuid(), paths))
            process.start()
            try:
                deadline = time.monotonic() + 5
                while not Path(value['guardian_socket']).exists() and time.monotonic() < deadline:
                    self.assertTrue(process.is_alive(), 'fixture guardian exited before accepting a lease')
                    time.sleep(0.01)
                self.assertTrue(Path(value['guardian_socket']).exists(), 'fixture guardian did not become ready')
                yield root, value, original, expected, paths, process
            finally:
                if process.is_alive():
                    process.terminate()
                process.join(5)
                self.assertFalse(process.is_alive(), 'fixture guardian did not terminate')

    def frozen_owner(self, root, value, stage):
        workspace = root / ('native-release' if value['deployment_tool'] == 'native' else 'systemd-release')
        workspace.mkdir(mode=0o700)
        environment = workspace / 'state' / 'previous.env'
        process_environment = workspace / 'state' / 'process-environment.bin'
        journal = workspace / 'state' / 'shutdown.log'
        environment_sha256 = self.private_file(environment, b'SQL_DSN=postgres://fixture.invalid/fixture\n')
        process_sha256 = self.private_file(process_environment, b'SQL_DSN=postgres://fixture.invalid/fixture\0PORT=3000\0')
        journal_body = (b'credit_transition_prepare shutdown_complete=true business_enabled=false\nserver exited\n'
                        if stage == 'post' else
                        b'refund_tasks execution_complete=true accepted=2 finished=2 active=0 failed=0\nserver exited\n')
        journal_sha256 = self.private_file(journal, journal_body)
        receipt = dict(format='lmm-credit-maintenance-capture-v1', phase='FROZEN',
                       transition_id=value['transition_id'], transition_intent_sha256=value['transition_intent_sha256'],
                       provider_sha256=value['provider_sha256'], was_maintenance_confirmed=stage == 'post',
                       archived_environment_path=str(environment), archived_environment_sha256=environment_sha256,
                       process_environment_path=str(process_environment), process_environment_sha256=process_sha256,
                       shutdown_journal_path=str(journal), shutdown_journal_sha256=journal_sha256,
                       pid=2147483647, invocation_id='0123456789abcdef0123456789abcdef')
        self.assertFalse(Path('/proc', str(receipt['pid'])).exists())
        receipt_path = workspace / 'state' / 'capture.json'
        receipt_sha256 = self.private_file(receipt_path, receipt)
        state = dict(phase='FROZEN', transition_id=value['transition_id'],
                     transition_intent_sha256=value['transition_intent_sha256'], provider_sha256=value['provider_sha256'],
                     maintenance_confirmation=stage == 'post', capture_receipt_path=str(receipt_path),
                     capture_receipt_sha256=receipt_sha256)
        state['deployment_id' if value['deployment_tool'] == 'native' else 'release'] = workspace.name
        state_path = workspace / ('state/status.json' if value['deployment_tool'] == 'native' else 'state.json')
        self.private_file(state_path, state)
        return workspace, state_path, state, receipt_path, receipt

    @staticmethod
    def stopped_unit(receipt, **changes):
        unit = dict(MainPID='0', ExecMainPID=str(receipt['pid']), ExecMainCode='1', ExecMainStatus='0',
                    ActiveState='inactive', SubState='dead', Result='success', ControlGroup='',
                    InvocationID=receipt['invocation_id'])
        unit.update(changes)
        return unit

    @staticmethod
    def systemctl_result(unit):
        return guardian.subprocess.CompletedProcess(
            ['systemctl', 'show', 'lmm-api.service'], 0,
            stdout=''.join(f'{key}={value}\n' for key, value in unit.items()).encode(), stderr=b'')

    def assert_three_locks_held(self, paths):
        for path in paths.values():
            independent = os.open(path, os.O_RDWR)
            try:
                with self.assertRaises(BlockingIOError):
                    fcntl.flock(independent, fcntl.LOCK_EX | fcntl.LOCK_NB)
            finally:
                os.close(independent)

    def assert_writer_inspection(self, probe):
        probe.assert_called_once_with(
            ['systemctl', 'show', 'lmm-api.service',
             '--property=MainPID,ExecMainPID,ExecMainCode,ExecMainStatus,ActiveState,SubState,Result,ControlGroup,InvocationID'],
            capture_output=True, check=True)

    def test_seal_stopped_native_prebridge_is_immutable_and_retains_real_guardian_locks(self):
        with self.running_guardian('native') as (root, value, original, expected, paths, process):
            workspace, _, _, receipt_path, receipt = self.frozen_owner(root, value, 'prebridge')
            output = root / 'stopped-prebridge-handoff.json'
            with mock.patch.object(guardian.subprocess, 'run', return_value=self.systemctl_result(self.stopped_unit(receipt))) as probe:
                sealed = guardian.seal_stopped(original, expected, workspace, 'prebridge', output, os.getuid(), paths)
                self.assert_writer_inspection(probe)
            self.assertEqual('lmm-credit-maintenance-handoff-seal-v1', sealed['format'])
            self.assertFalse(sealed['reused'])
            self.assertEqual(str(output), sealed['handoff_path'])
            self.assertEqual(hashlib.sha256(output.read_bytes()).hexdigest(), sealed['handoff_sha256'])
            self.assertEqual(0o600, output.stat().st_mode & 0o777)
            self.assertEqual(1, output.stat().st_nlink)
            bound = guardian.handoff(output, sealed['handoff_sha256'], os.getuid())
            self.assertEqual(workspace.name, bound['previous_deployment_id'])
            self.assertEqual(str(receipt_path), bound['capture_receipt_path'])
            self.assertEqual(receipt['archived_environment_sha256'], bound['archived_environment_sha256'])
            self.assertEqual(receipt['pid'], bound['stopped_writer']['pid'])
            self.assertEqual(receipt['invocation_id'], bound['stopped_writer']['invocation_id'])
            self.assertEqual(receipt['shutdown_journal_sha256'], bound['stopped_writer']['shutdown_journal_sha256'])
            inspected = guardian.inspect(output, sealed['handoff_sha256'], os.getuid(), paths)
            self.assertEqual(process.pid, inspected['guardian_pid'])
            self.assertTrue(all(item['held'] for item in inspected['locks']))
            self.assert_three_locks_held(paths)
            content = output.read_bytes()
            with mock.patch.object(guardian.subprocess, 'run', return_value=self.systemctl_result(self.stopped_unit(receipt))) as probe:
                reused = guardian.seal_stopped(original, expected, workspace, 'prebridge', output, os.getuid(), paths)
                self.assert_writer_inspection(probe)
            self.assertTrue(reused['reused'])
            self.assertEqual(sealed['handoff_sha256'], reused['handoff_sha256'])
            self.assertEqual(content, output.read_bytes())
            # Reuse still requires a fresh writer inspection, and never overwrites a different output.
            with mock.patch.object(guardian.subprocess, 'run', return_value=self.systemctl_result(self.stopped_unit(receipt, MainPID='123'))):
                with self.assertRaises(RuntimeError):
                    guardian.seal_stopped(original, expected, workspace, 'prebridge', output, os.getuid(), paths)
            self.assertEqual(content, output.read_bytes())
            altered = b'other evidence\n'
            self.private_file(output, altered)
            with mock.patch.object(guardian.subprocess, 'run', return_value=self.systemctl_result(self.stopped_unit(receipt))):
                with self.assertRaises(RuntimeError):
                    guardian.seal_stopped(original, expected, workspace, 'prebridge', output, os.getuid(), paths)
            self.assertEqual(altered, output.read_bytes())
            self.assert_three_locks_held(paths)

    def test_seal_stopped_systemd_post_binds_confirmed_bridge_and_real_scm_lease(self):
        with self.running_guardian('systemd') as (root, value, original, expected, paths, process):
            workspace, _, _, _, receipt = self.frozen_owner(root, value, 'post')
            output = root / 'stopped-post-handoff.json'
            with mock.patch.object(guardian.subprocess, 'run', return_value=self.systemctl_result(self.stopped_unit(receipt))) as probe:
                sealed = guardian.seal_stopped(original, expected, workspace, 'post', output, os.getuid(), paths)
                self.assert_writer_inspection(probe)
            bound = guardian.handoff(output, sealed['handoff_sha256'], os.getuid())
            self.assertEqual('post', bound['stage'])
            self.assertEqual(workspace.name, bound['previous_deployment_id'])
            self.assertEqual(value['provider_sha256'], bound['provider_sha256'])
            inspected = guardian.inspect(output, sealed['handoff_sha256'], os.getuid(), paths)
            self.assertEqual(process.pid, inspected['guardian_pid'])
            self.assertEqual(set(paths.values()), {item['path'] for item in inspected['locks']})
            self.assert_three_locks_held(paths)

    def test_seal_stopped_refuses_tampered_receipt_and_unsafe_output_directory(self):
        for rejection in ('receipt-digest', 'receipt-escape', 'archived_environment',
                          'process_environment', 'shutdown_journal', 'unsafe-output'):
            with self.subTest(rejection=rejection), self.running_guardian() as (root, value, original, expected, paths, _):
                workspace, state_path, state, receipt_path, receipt = self.frozen_owner(root, value, 'prebridge')
                output = root / 'refused-handoff.json'
                if rejection == 'receipt-digest':
                    self.private_file(receipt_path, dict(receipt, pid=receipt['pid'] - 1))
                elif rejection == 'receipt-escape':
                    escaped = root / 'escaped-capture.json'
                    state['capture_receipt_path'] = str(escaped)
                    state['capture_receipt_sha256'] = self.private_file(escaped, receipt)
                    self.private_file(state_path, state)
                elif rejection in ('archived_environment', 'process_environment', 'shutdown_journal'):
                    escaped = root / ('escaped-' + rejection)
                    original_evidence = Path(receipt[rejection + '_path'])
                    receipt[rejection + '_path'] = str(escaped)
                    receipt[rejection + '_sha256'] = self.private_file(escaped, original_evidence.read_bytes())
                    state['capture_receipt_sha256'] = self.private_file(receipt_path, receipt)
                    self.private_file(state_path, state)
                else:
                    unsafe = root / 'unsafe'
                    unsafe.mkdir(mode=0o700)
                    unsafe.chmod(0o777)
                    output = unsafe / 'handoff.json'
                with mock.patch.object(guardian.subprocess, 'run', return_value=self.systemctl_result(self.stopped_unit(receipt))):
                    with self.assertRaises(RuntimeError):
                        guardian.seal_stopped(original, expected, workspace, 'prebridge', output, os.getuid(), paths)
                self.assertFalse(output.exists())
                self.assert_three_locks_held(paths)

    def test_seal_stopped_refuses_unconfirmed_bridge_or_changed_writer_unit(self):
        for rejection in ('owner-confirmation', 'receipt-confirmation', 'active-writer', 'changed-invocation'):
            with self.subTest(rejection=rejection), self.running_guardian('systemd') as (root, value, original, expected, paths, _):
                workspace, state_path, state, receipt_path, receipt = self.frozen_owner(root, value, 'post')
                output = root / 'refused-post-handoff.json'
                unit = self.stopped_unit(receipt)
                if rejection == 'owner-confirmation':
                    state['maintenance_confirmation'] = False
                    self.private_file(state_path, state)
                elif rejection == 'receipt-confirmation':
                    receipt['was_maintenance_confirmed'] = False
                    state['capture_receipt_sha256'] = self.private_file(receipt_path, receipt)
                    self.private_file(state_path, state)
                elif rejection == 'active-writer':
                    unit.update(MainPID='123', ActiveState='active', SubState='running')
                else:
                    unit['InvocationID'] = 'f' * 32
                with mock.patch.object(guardian.subprocess, 'run', return_value=self.systemctl_result(unit)):
                    with self.assertRaises(RuntimeError):
                        guardian.seal_stopped(original, expected, workspace, 'post', output, os.getuid(), paths)
                self.assertFalse(output.exists())
                self.assert_three_locks_held(paths)

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

    def test_adopt_all_preserves_three_lock_descriptions_after_guardian_exit(self):
        with self.running_guardian() as (_, value, original, expected, paths, process):
            bound = guardian.handoff(original, expected, os.getuid())
            descriptors, lease, reply = guardian.adopt_all(bound, expected, os.getuid(), paths)
            try:
                self.assertEqual(3, len(descriptors))
                self.assertEqual([paths[name] for name in ('native', 'systemd', 'frontend')],
                                 reply['transferred_paths'])
                for descriptor, path in zip(descriptors, reply['transferred_paths']):
                    self.assertEqual(os.stat(path).st_ino, os.fstat(descriptor).st_ino)
                self.assert_three_locks_held(paths)
                process.terminate()
                process.join(5)
                self.assertFalse(process.is_alive())
                self.assert_three_locks_held(paths)
            finally:
                # Closing the adopted OFDs is sufficient; LOCK_UN would unlock
                # the guardian's description while it is still alive.
                for descriptor in descriptors:
                    os.close(descriptor)
                lease.close()
            for path in paths.values():
                independent = os.open(path, os.O_RDWR)
                try:
                    fcntl.flock(independent, fcntl.LOCK_EX | fcntl.LOCK_NB)
                finally:
                    os.close(independent)


if __name__ == '__main__':
    unittest.main()
