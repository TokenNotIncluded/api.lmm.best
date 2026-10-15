"""Docker fixture lifecycle regressions, not product runtime acceptance."""
import json
import os
import subprocess
import time
import unittest
import uuid
from unittest.mock import patch

import test_install_smoke as smoke


@unittest.skipUnless(os.environ.get("LMM_INSTALL_SMOKE_TEST_IMAGE"),
                     "set LMM_INSTALL_SMOKE_TEST_IMAGE to an existing local image")
class DockerTimeoutRegressionTests(unittest.TestCase):
    def test_real_30_second_timeout_fails_without_owned_container_left(self):
        # Works on the frozen implementation too: fail first on its live orphan.
        # Only the fixture stdin is slowed; the existing 30s deadline is unchanged.
        real_run = subprocess.run
        name = "lmm-install-timeout-regression-" + uuid.uuid4().hex
        label = "lmm.install-timeout-regression=" + name
        owned_name = name
        owned_id = None
        docker = ["docker", "--host", "unix:///var/run/docker.sock"]
        env = {k: v for k, v in os.environ.items() if not k.startswith("DOCKER_")}

        def inspect(target):
            return real_run(docker + ["inspect", target], env=env,
                            text=True, capture_output=True, timeout=5)

        def inject(args, **kwargs):
            nonlocal owned_name, owned_id
            if args[0] == "docker" and "run" in args:
                self.assertEqual(kwargs["timeout"], 30)
                index = args.index("run") + 1
                args = args[:index] + ["--name", name, "--label", label] + args[index:]
                kwargs["input"] = "echo timeout-regression-started; sleep 60\n" + kwargs["input"]
            elif args[0] == "docker" and "create" in args:
                owned_name = args[args.index("--name") + 1]
                result = real_run(args, **kwargs)
                owned_id = result.stdout.strip()
                print("Created owned fixture:", owned_name, owned_id, flush=True)
                return result
            elif args[0] == "docker" and "start" in args:
                self.assertEqual(kwargs["timeout"], 30)
                kwargs["input"] = "echo timeout-regression-started; sleep 60\n" + kwargs["input"]
            return real_run(args, **kwargs)

        try:
            with patch.object(smoke.subprocess, "run", side_effect=inject):
                with self.assertRaises(subprocess.TimeoutExpired) as caught:
                    smoke.ServiceUIDContainerTests(
                        "test_setpriv_uses_real_accounts_and_exec_pid_with_private_file_isolation"
                    ).test_setpriv_uses_real_accounts_and_exec_pid_with_private_file_isolation()
            self.assertEqual(caught.exception.timeout, 30)
            self.assertIn(b"timeout-regression-started", caught.exception.output)
            print("Original timeout retained:", repr(caught.exception),
                  getattr(caught.exception, "__notes__", []), flush=True)
            after = inspect(owned_id or owned_name)
            print("Exact post-timeout inspect:", after.returncode, after.stdout, after.stderr, flush=True)
            self.assertNotEqual(after.returncode, 0, "timeout left owned container behind")
            self.assertIn("no such object", after.stderr.lower())
        finally:
            # Regression safety net, not the behavior under test. Assert above first.
            current = inspect(owned_id or owned_name)
            if current.returncode == 0:
                details = json.loads(current.stdout)[0]
                labels = details["Config"]["Labels"]
                expected = (labels.get("lmm.install-smoke.fixture") == owned_name
                            if owned_id else labels.get("lmm.install-timeout-regression") == name)
                self.assertTrue(expected, "unknown identity: preserved, not removed")
                self.assertEqual(details["Name"], "/" + owned_name)
                cid = details["Id"]
                real_run(docker + ["stop", "--time", "1", cid], env=env,
                         text=True, capture_output=True, timeout=5, check=True)
                deadline = time.monotonic() + 10
                while True:
                    current = inspect(cid)
                    if current.returncode != 0 and "no such object" in current.stderr.lower():
                        break
                    if time.monotonic() >= deadline:
                        self.fail("regression safety-net removal not confirmed: " + cid)
                    time.sleep(0.1)
                print("Safety net confirmed owned fixture removed:", cid, flush=True)


class DockerFixtureContractTests(unittest.TestCase):
    def setUp(self):
        self.fixture = smoke.DockerFixture()
        self.fixture.cid = "a" * 64

    def details(self, **updates):
        result = {"Id": self.fixture.cid, "Name": "/" + self.fixture.name,
                  "Config": {"Labels": {self.fixture.label: self.fixture.name}},
                  "State": {"Running": True}}
        result.update(updates)
        return result

    def response(self, details=None, *, error=None):
        return subprocess.CompletedProcess([], int(error is not None),
                                           json.dumps([details]) if details else "[]",
                                           error or "")

    def missing(self):
        return self.response(error="error: no such container: " + self.fixture.cid)

    def test_remote_environment_is_not_inherited(self):
        with patch.dict(os.environ, {"DOCKER_HOST": "tcp://remote:2375",
                                     "DOCKER_CONTEXT": "remote", "DOCKER_TLS_VERIFY": "1"}):
            fixture = smoke.DockerFixture()
        with patch.object(smoke.subprocess, "run", return_value=self.missing()) as run:
            fixture.command(["inspect", fixture.name])
        self.assertEqual(run.call_args.args[0][:3],
                         ["docker", "--host", "unix:///var/run/docker.sock"])
        self.assertFalse(any(k.startswith("DOCKER_") for k in run.call_args.kwargs["env"]))

    def test_exact_not_found_only_not_daemon_error_or_other_identity(self):
        for error in ("Cannot connect to the Docker daemon", "permission denied",
                      "no such object: another-name"):
            with self.subTest(error=error), patch.object(
                    self.fixture, "command", return_value=self.response(error=error)):
                with self.assertRaisesRegex(RuntimeError, "inspect failed"):
                    self.fixture.inspect()
        with patch.object(self.fixture, "command", return_value=self.missing()):
            self.assertIsNone(self.fixture.inspect())

    def test_unknown_name_label_or_id_is_never_removed(self):
        for updates in ({"Name": "/unrelated"}, {"Id": "b" * 64},
                        {"Config": {"Labels": {}}}):
            with self.subTest(updates=updates), patch.object(
                    self.fixture, "command", return_value=self.response(self.details(**updates))) as command:
                with self.assertRaisesRegex(RuntimeError, "unknown fixture identity"):
                    self.fixture.cleanup()
                self.assertEqual(command.call_count, 1)
                self.assertEqual(command.call_args.args[0][0], "inspect")

    def test_async_removal_race_waits_for_exact_id_absence(self):
        responses = [self.response(self.details()), self.response(),
                     self.response(error="removal of container is already in progress"),
                     self.response(self.details(State={"Running": False})), self.missing()]
        with patch.object(self.fixture, "command", side_effect=responses) as command, \
                patch.object(smoke.time, "sleep"):
            self.fixture.cleanup()
        args = [call.args[0] for call in command.call_args_list]
        self.assertEqual(args[1], ["stop", "--time", "1", self.fixture.cid])
        self.assertEqual(args[2], ["rm", "--force", self.fixture.cid])
        self.assertEqual(len(args), 5)

    def test_stop_timeout_still_removes_and_confirms_exact_owned_id(self):
        responses = [self.response(self.details()), subprocess.TimeoutExpired("stop", 5),
                     self.response(), self.missing()]
        with patch.object(self.fixture, "command", side_effect=responses) as command:
            self.fixture.cleanup()
        self.assertEqual(command.call_args_list[2].args[0], ["rm", "--force", self.fixture.cid])
        self.assertIn("stop failed", "\n".join(self.fixture.diagnostics))

    def test_removal_client_timeout_still_waits_for_daemon_absence(self):
        responses = [self.response(self.details(State={"Running": False})),
                     subprocess.TimeoutExpired("rm", 5), self.missing()]
        with patch.object(self.fixture, "command", side_effect=responses):
            self.fixture.cleanup()
        self.assertIn("rm failed", "\n".join(self.fixture.diagnostics))

    def test_removal_not_confirmed_is_failure_with_bounded_wait(self):
        responses = [self.response(self.details()), self.response(), self.response(),
                     self.response(self.details(State={"Running": False}))]
        with patch.object(self.fixture, "command", side_effect=responses), \
                patch.object(smoke.time, "monotonic", side_effect=[0, 0, 0, 0, 11]):
            with self.assertRaisesRegex(RuntimeError, "removal not confirmed"):
                self.fixture.cleanup()

    def test_create_timeout_retries_exact_name_until_owned_identity_known(self):
        cid = self.fixture.cid
        self.fixture.cid = None
        responses = [self.response(error="No such object: " + self.fixture.name),
                     self.response(self.details(Id=cid, State={"Running": False})),
                     self.response(), self.response(error="No such object: " + cid)]
        with patch.object(self.fixture, "command", side_effect=responses), \
                patch.object(smoke.time, "sleep"):
            self.fixture.cleanup()
        self.assertEqual(self.fixture.cid, cid)

    def test_unknown_create_identity_remains_failure_and_is_not_blindly_deleted(self):
        self.fixture.cid = None
        with patch.object(self.fixture, "command", return_value=self.response(
                error="No such object: " + self.fixture.name)) as command, \
                patch.object(smoke.time, "monotonic", side_effect=[0, 0, 11]):
            with self.assertRaisesRegex(RuntimeError, "identity unresolved"):
                self.fixture.cleanup()
        self.assertEqual(command.call_count, 1)

    def test_original_exception_and_output_survive_cleanup_failure(self):
        error = subprocess.TimeoutExpired("start", 30, output=b"diagnostic", stderr=b"failure")
        with patch.object(self.fixture, "command", side_effect=[
                subprocess.CompletedProcess([], 0, self.fixture.cid, ""), error]), \
                patch.object(self.fixture, "inspect", return_value=self.details()), \
                patch.object(self.fixture, "cleanup", side_effect=RuntimeError("daemon unavailable")):
            with self.assertRaises(subprocess.TimeoutExpired) as caught:
                self.fixture.run("local-image", "probe")
        self.assertIs(caught.exception, error)
        self.assertEqual(error.output, b"diagnostic")
        self.assertEqual(error.stderr, b"failure")
        self.assertIn("daemon unavailable", "\n".join(error.__notes__))


@unittest.skipUnless(os.environ.get("LMM_INSTALL_SMOKE_TEST_IMAGE"), "local image required")
class DockerFailureRegressionTests(unittest.TestCase):
    def assert_absent(self, fixture):
        self.assertIsNotNone(fixture.cid)
        self.assertIsNone(fixture.inspect())
        print("Exact owned ID absent:", fixture.name, fixture.cid, flush=True)

    def test_real_nonzero_exit_preserves_output_and_removes_fixture(self):
        fixture = smoke.DockerFixture()
        with self.assertRaises(subprocess.CalledProcessError) as caught:
            fixture.run(os.environ["LMM_INSTALL_SMOKE_TEST_IMAGE"], "echo original-exit-seven; exit 7\n")
        self.assertEqual(caught.exception.returncode, 7)
        self.assertIn("original-exit-seven", caught.exception.stdout)
        self.assert_absent(fixture)

    def test_real_created_fixture_is_removed_when_start_client_fails(self):
        fixture = smoke.DockerFixture()
        command = fixture.command
        def fail_start(args, **kwargs):
            if args[0] == "start":
                raise OSError("injected start client failure")
            return command(args, **kwargs)
        sibling = smoke.DockerFixture()
        image = os.environ["LMM_INSTALL_SMOKE_TEST_IMAGE"]
        created = sibling.command(["create", "--rm", "--name", sibling.name,
                                   "--label", sibling.label + "=" + sibling.name,
                                   "--network", "none", "--pull", "never", image,
                                   "sh", "-c", "exit 0"], check=True)
        sibling.cid = created.stdout.strip()
        try:
            self.assertEqual(sibling.inspect()["State"]["Status"], "created")
            with patch.object(fixture, "command", side_effect=fail_start):
                with self.assertRaisesRegex(OSError, "injected start client failure"):
                    fixture.run(image, "exit 0\n")
            self.assert_absent(fixture)
            self.assertEqual(sibling.inspect()["State"]["Status"], "created")
            print("Unrelated same-prefix owned sibling preserved:", sibling.cid, flush=True)
        finally:
            sibling.cleanup()
            self.assert_absent(sibling)


if __name__ == "__main__":
    unittest.main()
