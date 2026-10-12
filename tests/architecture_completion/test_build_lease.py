"""Actual Linux process/lock tests; no Cargo, containers or databases."""
import importlib.util
import json
import os
from pathlib import Path
import select
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("build_lease", HERE / "build_lease.py")
LEASE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(LEASE)
SHA = "a" * 64


class LeaseTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.resource = self.root / "resource"
        LEASE.initialize(self.resource)
        self.requests = self.root / "A09"
        self.requests.mkdir(mode=0o700)

    def acquire(self, **changes):
        args = dict(directory=self.resource, requests=self.requests, task="A09",
                    kind="cargo", input_sha256=SHA, timeout_seconds=0.05)
        args.update(changes)
        return LEASE.acquire(**args)

    def worker(self, body):
        source = f"import sys,time,json,os; from pathlib import Path; sys.path.insert(0,{str(HERE)!r}); import build_lease as L; " + body
        return subprocess.Popen([sys.executable, "-S", "-B", "-c", source], stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, text=True)

    def test_private_receipts_and_identity_are_persistent(self):
        with self.acquire() as (_, request):
            self.assertTrue((request / "acquired.json").exists())
            self.assertEqual(json.loads((request / "acquired.json").read_text())["holder"]["pid"], os.getpid())
            self.assertEqual(request.stat().st_mode & 0o777, 0o700)
        self.assertTrue((request / "released.json").exists())
        for file in request.iterdir():
            self.assertEqual(file.stat().st_mode & 0o777, 0o600)
        self.assertTrue((self.resource / "heavy.lock").exists())

    def test_live_holder_blocks_other_build_kinds_and_no_timeout_steal(self):
        with self.acquire():
            with self.assertRaises(TimeoutError):
                with self.acquire(kind="image"):
                    self.fail("overlapping heavy builds")
        with self.acquire(kind="frontend"):
            pass
        self.assertEqual(len(list(self.requests.glob("*/timeout.json"))), 1)

    def test_parallel_process_waits_then_acquires(self):
        with self.acquire():
            body = f"\nwith L.acquire(Path({str(self.resource)!r}),Path({str(self.requests)!r}),'A01','image',{SHA!r},2):\n print('acquired',flush=True)"
            child = self.worker(body)
            self.addCleanup(lambda: child.poll() is None and child.kill())
            self.assertFalse(select.select([child.stdout], [], [], 0.15)[0])
        out, err = child.communicate(timeout=5)
        self.assertEqual((child.returncode, out.strip(), err), (0, "acquired", ""))

    def test_child_retains_lock_when_wrapper_is_killed(self):
        ready, done = self.root / "child-ready", self.root / "child-done"
        task = f"from pathlib import Path; import time; Path({str(ready)!r}).write_text('ready'); time.sleep(0.8); Path({str(done)!r}).write_text('done')"
        cmd = [sys.executable, "-S", "-B", "-c", task]
        body = f"L.run(Path({str(self.resource)!r}),Path({str(self.requests)!r}),'A09','cargo',{SHA!r},{cmd!r},Path({str(self.root)!r}),2)"
        wrapper = self.worker(body)
        deadline = time.monotonic() + 5
        while not ready.exists() and time.monotonic() < deadline:
            if wrapper.poll() is not None:
                self.fail(wrapper.communicate()[1])
            time.sleep(0.01)
        self.assertTrue(ready.exists())
        # This is the exact subprocess created by this test, not a PID lookup.
        wrapper.kill()
        wrapper.wait(timeout=3)
        with self.assertRaises(TimeoutError):
            with self.acquire():
                self.fail("orphaned child lost its slot")
        with self.acquire(timeout_seconds=3):
            self.assertTrue(done.exists())
        wrapper.communicate(timeout=3)

    def test_exception_releases_only_this_holder(self):
        with self.assertRaises(RuntimeError):
            with self.acquire():
                raise RuntimeError("synthetic")
        with self.acquire():
            pass
        self.assertEqual(len(list(self.requests.glob("*/interrupted.json"))), 1)

    def test_no_reinitialization_or_receipt_overwrite(self):
        before = (self.resource / "resource.json").read_bytes()
        with self.assertRaises(FileExistsError):
            LEASE.initialize(self.resource)
        self.assertEqual((self.resource / "resource.json").read_bytes(), before)
        with self.acquire() as (_, request):
            before = (request / "requested.json").read_bytes()
            with self.assertRaises(FileExistsError):
                LEASE.receipt(request, "requested", {})
            self.assertEqual((request / "requested.json").read_bytes(), before)

    def test_symlink_and_hardlink_locks_are_rejected(self):
        original = self.resource / "heavy.lock"
        original.rename(self.resource / "old.lock")
        original.symlink_to(self.resource / "old.lock")
        with self.assertRaises(OSError):
            with self.acquire():
                pass
        original.unlink()
        os.link(self.resource / "old.lock", original)
        with self.assertRaises(ValueError):
            with self.acquire():
                pass

    def test_nonprivate_paths_and_invalid_input_are_rejected(self):
        self.requests.chmod(0o755)
        with self.assertRaises(ValueError):
            with self.acquire():
                pass
        self.requests.chmod(0o700)
        for changes in ({"task": "other"}, {"input_sha256": "invalid"}, {"timeout_seconds": -1}, {"timeout_seconds": float("nan")}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                with self.acquire(**changes):
                    pass

    def test_timeout_crossing_does_not_sleep_for_a_negative_duration(self):
        with self.acquire():
            with patch.object(LEASE.time, "monotonic", side_effect=[0, 0.09, 0.11]):
                with patch.object(LEASE.time, "sleep", side_effect=lambda d: self.assertGreaterEqual(d, 0)):
                    with self.assertRaises(TimeoutError):
                        with self.acquire(timeout_seconds=0.1):
                            self.fail("live holder must not be stolen")

    def test_exact_child_exit_and_separate_target(self):
        command = [sys.executable, "-S", "-c", "import os; assert os.environ['CARGO_BUILD_JOBS']=='1'; raise SystemExit(7)"]
        self.assertEqual(LEASE.run(self.resource, self.requests, "A09", "cargo", SHA,
                                   command, self.root, 0.1), 7)
        record = json.loads(next(self.requests.glob("*/finished.json")).read_text())
        self.assertEqual(record["exit_code"], 7)
        self.assertNotIn("command", json.loads(next(self.requests.glob("*/running.json")).read_text()))


if __name__ == "__main__":
    unittest.main()
