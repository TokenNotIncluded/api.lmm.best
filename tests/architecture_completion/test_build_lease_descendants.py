"""S11 regression: a launcher exiting must not release a live build's slot."""
import importlib.util
import json
import signal
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest

HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("lease_descendants", HERE / "build_lease.py")
LEASE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(LEASE)
SHA = "b" * 64


class DescendantTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.resource = self.root / "resource"
        self.requests = self.root / "S11"
        self.requests.mkdir(mode=0o700)
        LEASE.initialize(self.resource)

    def wait_until(self, predicate, message, timeout=5):
        deadline = time.monotonic() + timeout
        while not predicate():
            if time.monotonic() >= deadline:
                self.fail(message)
            time.sleep(0.01)

    def acquire(self, timeout=0.05):
        return LEASE.acquire(self.resource, self.requests, "S11", "cargo", SHA, timeout)

    def tree(self, close_leader_fds=False):
        ready, done = self.root / "ready", self.root / "done"
        stop, leader_exit = self.root / "stop", self.root / "leader-exit"
        leader_pid = self.root / "leader-pid"
        grandchild = (
            "import time; from pathlib import Path\n"
            f"Path({str(ready)!r}).touch()\n"
            "deadline=time.monotonic()+8\n"
            f"while not Path({str(stop)!r}).exists() and time.monotonic()<deadline: time.sleep(0.01)\n"
            f"Path({str(done)!r}).touch()\n"
        )
        leader = (
            "import os,sys,time,subprocess; from pathlib import Path\n"
            + ("os.closerange(3,256)\n" if close_leader_fds else "")
            + f"Path({str(leader_pid)!r}).write_text(str(os.getpid()))\n"
            + f"subprocess.Popen({[sys.executable, '-S', '-B', '-c', grandchild]!r}, "
              "close_fds=True, start_new_session=True, stdin=subprocess.DEVNULL, "
              "stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)\n"
            + "deadline=time.monotonic()+8\n"
            + f"while not Path({str(leader_exit)!r}).exists() and time.monotonic()<deadline: time.sleep(0.01)\n"
        )
        command = [sys.executable, "-S", "-B", "-c", leader]
        wrapper_code = (
            f"import sys; from pathlib import Path; sys.path.insert(0,{str(HERE)!r}); "
            "import build_lease as L; "
            f"raise SystemExit(L.run(Path({str(self.resource)!r}),Path({str(self.requests)!r}),"
            f"'S11','cargo',{SHA!r},{command!r},Path({str(self.root)!r}),1))"
        )
        wrapper = subprocess.Popen([sys.executable, "-S", "-B", "-c", wrapper_code],
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)

        def cleanup():
            stop.touch()
            leader_exit.touch()
            try:
                wrapper.communicate(timeout=5)
            except subprocess.TimeoutExpired:
                wrapper.kill()  # Only the exact process created above.
                wrapper.communicate(timeout=3)
            if ready.exists():
                self.wait_until(done.exists, "owned descendant did not stop")

        self.addCleanup(cleanup)
        self.wait_until(ready.exists, "descendant did not start")
        return wrapper, stop, done, leader_exit, int(leader_pid.read_text())

    @staticmethod
    def exited(pid):
        try:
            raw = Path(f"/proc/{pid}/stat").read_text()
        except FileNotFoundError:
            return True
        return raw[raw.rfind(")") + 2:].split()[0] == "Z"

    def test_wait_for_descendant_after_launcher_exits(self):
        wrapper, stop, done, leader_exit, leader = self.tree()
        leader_exit.touch()
        self.wait_until(lambda: self.exited(leader), "launcher did not exit")
        self.assertFalse(done.exists())
        with self.assertRaises(TimeoutError):
            with self.acquire():
                pass
        self.assertFalse(list(self.requests.glob("*/finished.json")))
        stop.touch()
        out, err = wrapper.communicate(timeout=5)
        self.assertEqual((wrapper.returncode, out, err), (0, "", ""))
        with self.acquire():
            self.assertTrue(done.exists())

    def test_wrapper_death_and_launcher_exit_do_not_release_descendant(self):
        wrapper, stop, done, leader_exit, leader = self.tree()
        wrapper.kill()
        wrapper.wait(timeout=3)
        leader_exit.touch()
        self.wait_until(lambda: self.exited(leader), "launcher did not exit")
        with self.assertRaises(TimeoutError):
            with self.acquire():
                pass
        stop.touch()
        with self.acquire(timeout=5):
            self.assertTrue(done.exists())

    def test_wrapper_death_with_child_closing_inherited_descriptors(self):
        wrapper, stop, done, leader_exit, _ = self.tree(close_leader_fds=True)
        wrapper.kill()
        wrapper.wait(timeout=3)
        with self.assertRaises(TimeoutError):
            with self.acquire():
                pass
        stop.touch()
        leader_exit.touch()
        with self.acquire(timeout=5):
            self.assertTrue(done.exists())

    def test_spawn_failure_is_private_and_releases_slot(self):
        secret = "synthetic-command-secret"
        command = [str(self.root / "missing-build-program"), secret]
        with self.assertRaisesRegex(RuntimeError, "build supervision failed"):
            LEASE.run(self.resource, self.requests, "S11", "cargo", SHA,
                      command, self.root, 0.1)
        with self.acquire():
            pass
        failure = next(self.requests.glob("*/supervisor-failed.json"))
        self.assertEqual(json.loads(failure.read_text())["error_type"], "FileNotFoundError")
        self.assertFalse(list(self.requests.glob("*/finished.json")))
        for record in self.requests.glob("*/*.json"):
            self.assertNotIn(secret, record.read_text())

    def test_signal_status_preserved_without_waiting_for_unrelated_child(self):
        other = subprocess.Popen([sys.executable, "-S", "-c", "import time; time.sleep(8)"])
        def cleanup():
            if other.poll() is None:
                other.terminate()
            other.wait(timeout=3)
        self.addCleanup(cleanup)
        command = [sys.executable, "-S", "-c", "import os,signal; os.kill(os.getpid(),signal.SIGTERM)"]
        self.assertEqual(LEASE.run(self.resource, self.requests, "S11", "cargo", SHA,
                                  command, self.root, 0.1), -signal.SIGTERM)
        self.assertIsNone(other.poll())
        record = json.loads(next(self.requests.glob("*/finished.json")).read_text())
        self.assertEqual(record["exit_code"], -signal.SIGTERM)
        self.assertEqual(record["descendants_reaped"], 0)


if __name__ == "__main__":
    unittest.main()
