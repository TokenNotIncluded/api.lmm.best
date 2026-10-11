"""Offline fixture regressions; real Docker bind mounts are tested separately."""
import importlib.util
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location(
    "core_rpc_docker", Path(__file__).with_name("test-core-rpc-docker.py")
)
FIXTURE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(FIXTURE)


class PrivateFixtureTests(unittest.TestCase):
    def test_secrets_are_private_from_creation_and_never_overwritten(self):
        with tempfile.TemporaryDirectory() as directory:
            temp = Path(directory)
            values = {name: "synthetic-" + name for name in FIXTURE.SECRET_NAMES}
            fdopen = os.fdopen

            def private_writer(fd, *args):
                self.assertEqual(stat.S_IMODE(os.fstat(fd).st_mode), 0o600)
                return fdopen(fd, *args)

            with mock.patch.object(FIXTURE.os, "fdopen", side_effect=private_writer) as opened:
                FIXTURE.write_fixture_secrets(temp, values)
                self.assertEqual(opened.call_count, 4)
            for name, value in values.items():
                path = temp / name
                self.assertEqual(path.read_text(), value)
                self.assertTrue(stat.S_ISREG(path.lstat().st_mode))
                self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            with self.assertRaises(FileExistsError):
                FIXTURE.write_fixture_secrets(temp, values)

    def test_helpers_are_confined_to_exact_fixture_files(self):
        with mock.patch.object(FIXTURE, "run") as run:
            FIXTURE.fixture_secrets("fixture:test", Path("/tmp/fixture"),
                                    FIXTURE.SECRET_NAMES, FIXTURE.PREPARE_SECRETS,
                                    prepare=True)
        args = run.call_args.args
        self.assertIn("--read-only", args)
        self.assertIn("no-new-privileges:true", args)
        self.assertEqual(args[args.index("--network") + 1], "none")
        self.assertEqual(args[args.index("--cap-drop") + 1], "ALL")
        self.assertEqual(args[args.index("--user") + 1], "0:0")
        self.assertEqual([args[i + 1] for i, arg in enumerate(args) if arg == "--cap-add"],
                         ["CHOWN", "FOWNER"])
        self.assertEqual([args[i + 1] for i, arg in enumerate(args) if arg == "--mount"],
                         [f"type=bind,source=/tmp/fixture/{name},target=/fixture-{name}"
                          for name in FIXTURE.SECRET_NAMES])

    def test_verification_uses_image_user_and_read_only_mounts(self):
        with mock.patch.object(FIXTURE, "run") as run:
            FIXTURE.fixture_secrets("fixture:test", Path("/tmp/fixture"),
                                    FIXTURE.SECRET_NAMES, FIXTURE.VERIFY_SECRETS)
        args = run.call_args.args
        self.assertNotIn("--user", args)
        self.assertNotIn("--cap-add", args)
        self.assertIn('test "$(id -u):$(id -g)" = 65532:65532', FIXTURE.VERIFY_SECRETS)
        for i, arg in enumerate(args):
            if arg == "--mount":
                self.assertTrue(args[i + 1].endswith(",readonly"))

    def test_unknown_file_cannot_be_mounted(self):
        with mock.patch.object(FIXTURE, "run") as run, self.assertRaises(AssertionError):
            FIXTURE.fixture_secrets("fixture:test", Path("/tmp/fixture"),
                                    ("../unrelated",), FIXTURE.PREPARE_SECRETS, prepare=True)
        run.assert_not_called()

    def test_rotation_passes_value_on_stdin_and_only_mounts_rpc_file_writable(self):
        with mock.patch.object(FIXTURE, "run") as run:
            FIXTURE.fixture_secrets("fixture:test", Path("/tmp/fixture"), ("rpc-token",),
                                    FIXTURE.ROTATE_SECRET, data="replacement-synthetic-token")
        args = run.call_args.args
        self.assertNotIn("--user", args)
        self.assertNotIn("--cap-add", args)
        self.assertNotIn("replacement-synthetic-token", args)
        self.assertEqual(run.call_args.kwargs, {"data": "replacement-synthetic-token"})
        self.assertEqual([args[i + 1] for i, arg in enumerate(args) if arg == "--mount"],
                         ["type=bind,source=/tmp/fixture/rpc-token,target=/fixture-rpc-token"])

    def shell(self, script, *paths, data=None):
        # Exercise the actual shell logic as this test process's unprivileged UID.
        # Container UID 65532 and mount behavior still require the Docker scenario.
        script = script.replace("65532:65532", f"{os.getuid()}:{os.getgid()}")
        return subprocess.run(["/bin/sh", "-ec", script, "fixture-secrets", *map(str, paths)],
                              input=data, text=True, capture_output=True)

    def test_verification_rejects_public_files_symlinks_and_directories(self):
        with tempfile.TemporaryDirectory() as directory:
            temp = Path(directory)
            private = temp / "private"
            private.write_text("synthetic-token")
            private.chmod(0o400)
            self.assertEqual(self.shell(FIXTURE.VERIFY_SECRETS, private).returncode, 0)
            link = temp / "link"
            link.symlink_to(private)
            for path in (link, temp):
                self.assertNotEqual(self.shell(FIXTURE.VERIFY_SECRETS, path).returncode, 0)
            for mode in (0o444, 0o440, 0o404, 0o600):
                private.chmod(mode)
                self.assertNotEqual(self.shell(FIXTURE.VERIFY_SECRETS, private).returncode, 0)

    def test_rotation_preserves_inode_and_privacy_and_supports_restore(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "rpc-token"
            path.write_text("original-synthetic-token")
            path.chmod(0o400)
            inode = path.stat().st_ino
            for value in ("wrong-synthetic-token", "original-synthetic-token"):
                result = self.shell(FIXTURE.ROTATE_SECRET, path, data=value)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout, "")
                self.assertEqual(path.read_text(), value)
                self.assertEqual(path.stat().st_ino, inode)
                self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o400)
            path.chmod(0o444)
            result = self.shell(FIXTURE.ROTATE_SECRET, path, data="must-not-write")
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(path.read_text(), "original-synthetic-token")

    def test_failed_or_interrupted_rotation_restores_read_only_mode(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "rpc-token"
            path.write_text("synthetic-token")
            path.chmod(0o400)
            inode = path.stat().st_ino
            for failure in ("return 1", "kill -TERM $$"):
                with self.subTest(failure=failure):
                    # File-argument reads in verification still work; only the
                    # stdin writer fails or interrupts its owning shell.
                    injected = 'cat() { if [ "$#" = 0 ]; then ' + failure + \
                               '; else command cat "$@"; fi; }\n'
                    result = self.shell(injected + FIXTURE.ROTATE_SECRET, path, data="replacement")
                    self.assertNotEqual(result.returncode, 0)
                    self.assertEqual(path.stat().st_ino, inode)
                    self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o400)


if __name__ == "__main__":
    unittest.main()
