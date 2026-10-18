"""Startup identity rejection tests; real systemd probes remain independent."""

import errno
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch


SPEC = importlib.util.spec_from_file_location(
    "resource_group_under_test", Path(__file__).with_name("resource_group.py")
)
v = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(v)


class StartupIdentityTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.binary = self.root / "product"
        self.binary.write_bytes(b"frozen product executable")
        self.digest = hashlib.sha256(self.binary.read_bytes()).hexdigest()
        self.info = self.binary.stat()
        self.group = v.Group(self.root)
        self.group.group = Path("/sys/fs/cgroup/unit-test.slice")
        self.pid = 12345
        self.process_identity = dict(
            pid=self.pid, start_ticks=100, boot_id=self.group.owner["boot_id"]
        )
        self.launcher = Mock(pid=23456)
        self.launcher.poll.return_value = None
        self.now = 100.0
        self.properties_calls = 0
        self.identity_calls = 0
        self.exe_opens = 0
        self.exe_stats = 0
        self.cgroup_reads = 0
        self.open_errors = []
        self.identity_error = None
        self.identity_mutation = None
        self.observation_mutation = None
        self.cgroup_mutation = None
        self.final_exe_info = self.info
        self.original_open = Path.open
        self.original_stat = Path.stat
        self.original_read_text = Path.read_text

    def observe(self, unit):
        self.properties_calls += 1
        result = dict(
            MainPID=str(self.pid),
            Slice=self.group.unit,
            ControlGroup=f"/unit-test.slice/{unit}",
            ActiveState="active",
        )
        if self.observation_mutation:
            self.observation_mutation(result)
        return result

    def identify(self, pid):
        self.identity_calls += 1
        if self.identity_error:
            error, self.identity_error = self.identity_error, None
            raise error
        result = dict(self.process_identity)
        if self.identity_mutation:
            self.identity_mutation(result)
        return result

    def open_path(self, path, *args, **kwargs):
        if str(path) == f"/proc/{self.pid}/exe":
            self.exe_opens += 1
            if self.open_errors:
                error = self.open_errors.pop(0)
                if error:
                    raise error
            return self.original_open(self.binary, *args, **kwargs)
        return self.original_open(path, *args, **kwargs)

    def stat_path(self, path, *args, **kwargs):
        if str(path) == f"/proc/{self.pid}/exe":
            self.exe_stats += 1
            return self.final_exe_info
        return self.original_stat(path, *args, **kwargs)

    def read_path(self, path, *args, **kwargs):
        if str(path) == f"/proc/{self.pid}/cgroup":
            self.cgroup_reads += 1
            result = f"0::/unit-test.slice/{self.group.services[-1]['unit']}"
            if self.cgroup_mutation:
                return self.cgroup_mutation(result)
            return result
        return self.original_read_text(path, *args, **kwargs)

    def advance(self, duration):
        self.now += duration

    def launch(self):
        with (
            patch.object(v, "properties", side_effect=self.observe),
            patch.object(v, "identity", side_effect=self.identify),
            patch.object(v, "sha", return_value=self.digest),
            patch.object(v.subprocess, "Popen", return_value=self.launcher),
            patch.object(v.time, "monotonic", side_effect=lambda: self.now),
            patch.object(v.time, "sleep", side_effect=self.advance),
            patch.object(
                v,
                "limits",
                return_value={
                    "cpu.max": "100000 100000",
                    "memory.max": str(256 * 1024**2),
                    "memory.swap.max": "0",
                    "pids.max": "64",
                },
            ),
            patch.object(Path, "open", autospec=True, side_effect=self.open_path),
            patch.object(Path, "stat", autospec=True, side_effect=self.stat_path),
            patch.object(Path, "read_text", autospec=True, side_effect=self.read_path),
        ):
            return self.group.launch("core", self.binary, [], self.digest)

    def receipt(self):
        return json.loads((self.root / "service-1-startup.json").read_text())

    def test_complete_snapshot_after_transient_executable_denials(self):
        self.open_errors = [PermissionError(errno.EACCES, "startup") for _ in range(3)]
        item = self.launch()
        self.assertEqual(item["identity"], self.process_identity)
        self.assertEqual(self.exe_opens, 4)
        self.assertEqual(self.receipt()["retry_counts"], {"executable-open:EACCES": 3})
        self.assertEqual(self.cgroup_reads, 6)
        self.assertTrue((self.root / "service-1-identity.json").exists())

    def test_unreadable_initial_identity_is_not_pinned(self):
        self.identity_error = PermissionError(errno.EACCES, "startup")
        self.assertEqual(self.launch()["identity"], self.process_identity)
        self.assertEqual(self.receipt()["retry_counts"], {"identity-before:EACCES": 1})

    def test_permanent_denial_expires_without_identity_receipt(self):
        self.open_errors = [PermissionError(errno.EACCES, "policy") for _ in range(800)]
        with self.assertRaisesRegex(
            v.ResourceError, "deadline.*executable-open:EACCES"
        ):
            self.launch()
        self.assertGreaterEqual(self.now, 115)
        self.assertLess(self.now, 115.03)
        self.assertLessEqual(self.exe_opens, 751)
        self.assertEqual(self.receipt()["result"], "failed")
        self.assertFalse((self.root / "service-1-identity.json").exists())
        # Cleanup authority was retained before the failure.
        self.assertEqual(len(self.group.services), 1)
        self.assertIs(self.group.services[0]["launcher"], self.launcher)

    def test_only_exact_retryable_errnos_are_retried(self):
        for code in (errno.EPERM, errno.EIO):
            with self.subTest(errno=code):
                self.open_errors = [OSError(code, "not a startup denial")]
                with self.assertRaises(OSError) as caught:
                    self.launch()
                self.assertEqual(caught.exception.errno, code)
                self.assertEqual(self.now, 100)

    def test_missing_executable_can_be_followed_by_stable_snapshot(self):
        self.open_errors = [FileNotFoundError(errno.ENOENT, "exec handoff")]
        self.assertEqual(self.launch()["identity"], self.process_identity)
        self.assertEqual(self.receipt()["retry_counts"], {"executable-open:ENOENT": 1})

    def test_exit_is_not_retried_as_success(self):
        self.launcher.poll.return_value = 17
        with self.assertRaises(v.LauncherExited):
            self.launch()
        self.assertEqual(self.exe_opens, 0)
        self.assertEqual(self.launcher.poll.return_value, 17)
        self.assertEqual(self.receipt()["result"], "failed")

    def test_pid_replacement_after_denial_is_rejected(self):
        self.open_errors = [PermissionError(errno.EACCES, "startup")]
        self.observation_mutation = lambda result: result.update(
            MainPID=str(self.pid + (self.properties_calls > 1))
        )
        with self.assertRaisesRegex(v.ResourceError, "PID identity changed"):
            self.launch()

    def test_start_ticks_and_boot_changes_are_rejected(self):
        for field, replacement in (("start_ticks", 101), ("boot_id", "new-boot")):
            with self.subTest(field=field):
                self.identity_calls = 0
                self.identity_mutation = lambda result: result.update(
                    **({field: replacement} if self.identity_calls > 1 else {})
                )
                with self.assertRaisesRegex(v.ResourceError, "PID identity changed"):
                    self.launch()

    def test_pid_identity_cannot_reset_after_retry(self):
        self.open_errors = [PermissionError(errno.EACCES, "startup")]
        self.identity_mutation = lambda result: result.update(
            start_ticks=100 + (self.identity_calls > 1)
        )
        with self.assertRaisesRegex(v.ResourceError, "PID identity changed"):
            self.launch()

    def test_cgroup_movement_during_snapshot_is_rejected(self):
        self.cgroup_mutation = lambda result: (
            result if self.cgroup_reads == 1 else "0::/foreign"
        )
        with self.assertRaisesRegex(
            v.ResourceError, "outside the shared parent budget"
        ):
            self.launch()

    def test_systemd_cgroup_path_cannot_change_after_denial(self):
        self.open_errors = [PermissionError(errno.EACCES, "startup")]
        self.observation_mutation = lambda result: result.update(
            ControlGroup=result["ControlGroup"]
            + ("-changed" if self.properties_calls > 1 else "")
        )
        with self.assertRaisesRegex(
            v.ResourceError, "outside the shared parent budget"
        ):
            self.launch()

    def test_executable_change_after_open_is_rejected(self):
        self.final_exe_info = SimpleNamespace(
            st_dev=self.info.st_dev, st_ino=self.info.st_ino + 1
        )
        with self.assertRaisesRegex(v.ResourceError, "executable changed"):
            self.launch()

    def test_executable_content_mismatch_is_rejected(self):
        self.digest = "f" * 64
        with self.assertRaisesRegex(v.ResourceError, "SHA256 differs"):
            self.launch()


if __name__ == "__main__":
    unittest.main()
