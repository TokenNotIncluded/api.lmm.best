"""Install-smoke shell contract/control-flow tests, not product runtime acceptance."""
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time
import tomllib
import unittest
import uuid

HERE = Path(__file__).resolve().parent
SCRIPT = (HERE / "install-smoke.sh").read_text()


def function(name):
    match = re.search(rf"(?m)^{name}\(\) \{{\n.*?^\}}", SCRIPT, re.DOTALL)
    if match is None:
        raise AssertionError(f"missing shell function {name}")
    return match.group(0)


class DockerFixture:
    """One local Engine fixture; never discover/delete resources by prefix."""

    def __init__(self):
        self.name = "lmm-install-smoke-" + uuid.uuid4().hex
        self.label = "lmm.install-smoke.fixture"
        self.cid = None
        # Explicit local socket and no inherited remote context/TLS settings.
        self.docker = ["docker", "--host", "unix:///var/run/docker.sock"]
        self.env = {k: v for k, v in os.environ.items() if not k.startswith("DOCKER_")}
        self.diagnostics = []

    def command(self, args, *, timeout=5, **kwargs):
        return subprocess.run(self.docker + args, env=self.env, text=True,
                              capture_output=True, timeout=timeout, **kwargs)

    def inspect(self, *, timeout=5):
        target = self.cid or self.name
        result = self.command(["inspect", "--type", "container", target], timeout=timeout)
        if result.returncode:
            # Only this exact not-found response proves absence; Engine errors
            # (including unavailable socket/permission errors) are not absence.
            if re.search(r"no such (?:object|container): " + re.escape(target) + r"\s*$",
                         result.stderr, re.IGNORECASE):
                return None
            raise RuntimeError("fixture inspect failed: " + result.stderr)
        details, = json.loads(result.stdout)
        cid = details["Id"]
        if (not re.fullmatch(r"[0-9a-f]{64}", cid)
                or details["Name"] != "/" + self.name
                or details["Config"]["Labels"].get(self.label) != self.name
                or (self.cid is not None and cid != self.cid)):
            raise RuntimeError("unknown fixture identity; preserved: " + target)
        self.cid = cid
        self.diagnostics.append(f"owned fixture {self.name} {cid}: {details['State']}")
        return details

    def cleanup(self):
        # A timed-out create can finish in the daemon after its CLI died.
        # Retry exact-name discovery, but never treat unknown identity as owned.
        deadline = time.monotonic() + 10
        while True:
            details = self.inspect(timeout=min(5, max(0.01, deadline - time.monotonic())))
            if details is not None or self.cid is not None:
                break
            if time.monotonic() >= deadline:
                raise RuntimeError("create identity unresolved; exact name preserved for review: " + self.name)
            time.sleep(0.1)
        if details is None:
            return  # --rm already removed the known immutable ID.
        if details["State"]["Running"]:
            try:
                stopped = self.command(["stop", "--time", "1", self.cid])
                self.diagnostics.append(f"stop {self.cid}: {stopped.returncode} {stopped.stderr}")
            except (subprocess.SubprocessError, OSError) as exc:
                # A failed stop CLI does not establish daemon state. The same
                # verified ID still needs bounded removal and confirmation.
                self.diagnostics.append("stop failed: " + repr(exc))
        # Also removes a created-but-never-started fixture. Only the verified
        # immutable ID is used; a recreated name cannot select another resource.
        try:
            removed = self.command(["rm", "--force", self.cid])
            self.diagnostics.append(f"rm {self.cid}: {removed.returncode} {removed.stderr}")
        except (subprocess.SubprocessError, OSError) as exc:
            # Removal can complete asynchronously after its CLI timed out.
            self.diagnostics.append("rm failed: " + repr(exc))
        deadline = time.monotonic() + 10
        while True:
            if self.inspect(timeout=min(5, max(0.01, deadline - time.monotonic()))) is None:
                return
            if time.monotonic() >= deadline:
                raise RuntimeError("fixture removal not confirmed: " + self.cid)
            time.sleep(0.1)

    def run(self, image, probe):
        failure = None
        try:
            created = self.command(["create", "--rm", "--name", self.name,
                                    "--label", self.label + "=" + self.name,
                                    "--network", "none", "--pull", "never",
                                    "-i", image, "sh", "-seu"], timeout=10, check=True)
            cid = created.stdout.strip()
            if not re.fullmatch(r"[0-9a-f]{64}", cid):
                raise RuntimeError("Docker create returned unknown identity: " + created.stdout)
            self.cid = cid
            if self.inspect() is None:
                raise RuntimeError("created fixture disappeared before start: " + cid)
            return self.command(["start", "--attach", "--interactive", cid],
                                input=probe, timeout=30, check=True)
        except BaseException as exc:
            failure = exc
            raise
        finally:
            try:
                self.cleanup()
            except BaseException as cleanup_error:
                if failure is None:
                    raise
                failure.add_note("fixture cleanup failed: " + repr(cleanup_error))
            if failure is not None:
                failure.add_note("local fixture name=" + self.name + " cid=" + str(self.cid)
                                 + "\n" + "\n".join(self.diagnostics))


class InstallSmokeTests(unittest.TestCase):
    def shell(self, script, **env):
        return subprocess.run(["sh", "-eu", "-c", script], text=True,
                              capture_output=True, timeout=20,
                              env={**os.environ, **env})

    def test_shell_syntax_and_explicit_accounts(self):
        subprocess.run(["sh", "-n", str(HERE / "install-smoke.sh")], check=True)
        provision = "systemd-sysusers /usr/lib/sysusers.d/lmm-core.conf /usr/lib/sysusers.d/lmm-extensions.conf"
        self.assertIn('if [ "$format" != apk ]; then\n    ' + provision, SCRIPT)
        self.assertLess(SCRIPT.index("install_packages 1"), SCRIPT.index(provision))
        self.assertLess(SCRIPT.index(provision), SCRIPT.index("core_uid=$(id -u lmm-core)"))
        self.assertIn('[ "$core_uid" -ne "$ext_uid" ]', SCRIPT)
        self.assertIn('[ "$core_gid" -ne "$ext_gid" ]', SCRIPT)
        self.assertIn("apk add --no-cache ca-certificates openrc curl su-exec", SCRIPT)
        self.assertIn("dnf install -y ca-certificates systemd curl-minimal util-linux", SCRIPT)

    def test_launcher_execs_maintained_tools_with_actual_ids(self):
        launcher = function("run_service")
        self.assertIn('uid=$(id -u "$service")', launcher)
        self.assertIn('gid=$(id -g "$service")', launcher)
        self.assertIn('exec su-exec "$uid:$gid" "$@"', launcher)
        self.assertIn('exec setpriv --reuid="$uid" --regid="$gid" --clear-groups --no-new-privs "$@"', launcher)
        self.assertIn('run_service "lmm-$component" "/usr/bin/lmm-$component" --config', function("start_service"))
        self.assertNotRegex(SCRIPT, r"(?m)^/usr/bin/lmm-(?:core|extensions).*&$")
        identity = function("assert_service_identity")
        self.assertIn('"/proc/$service_pid/status"', identity)
        self.assertIn('run_service "lmm-$component" readlink', identity)
        self.assertIn('"/usr/bin/lmm-$component"', identity)

    def test_nonsecret_configs_reference_separate_private_tokens_without_db(self):
        with tempfile.TemporaryDirectory() as directory:
            result = self.shell(function("write_core_config") + "\n" + function("write_extensions_config") + '''
                smoke_dir=$WORK
                write_core_config "$smoke_dir/core/token"
                write_extensions_config "$smoke_dir/extensions/token"
            ''', WORK=directory)
            self.assertEqual(result.returncode, 0, result.stderr)
            core = Path(directory) / "core.toml"
            extensions = Path(directory) / "extensions.toml"
            config = tomllib.loads(core.read_text())
            self.assertNotIn("database", config)
            self.assertEqual(config["rpc"]["token_file"], directory + "/core/token")
            config = tomllib.loads(extensions.read_text())
            self.assertEqual(config["host"]["token_file"], directory + "/extensions/token")
            self.assertEqual(config["host"]["modules"], ["none"])
            self.assertEqual(core.stat().st_mode & 0o777, 0o644)
            self.assertEqual(extensions.stat().st_mode & 0o777, 0o644)

    def test_private_credential_matrix_is_not_replaced_by_readiness_success(self):
        for stage in ("root-owned-0600", "service-owned-0640", "cross-service-0600"):
            self.assertIn("stage=" + (f"'{stage}'" if stage == "service-owned-0640" else stage), SCRIPT)
        self.assertEqual(SCRIPT.count("    expect_credential_rejection\n"), 3)
        self.assertIn('chown "lmm-$component:lmm-$component" "$smoke_dir/$component/token"', SCRIPT)
        self.assertIn('chmod 0640 "$smoke_dir/$component/token"', SCRIPT)
        self.assertIn('chmod 0600 "$smoke_dir/$component/token"', SCRIPT)
        self.assertIn('run_service lmm-core cat "$smoke_dir/extensions/token"', SCRIPT)
        self.assertIn('run_service lmm-extensions cat "$smoke_dir/core/token"', SCRIPT)
        self.assertIn('write_core_config "$smoke_dir/extensions/token"', SCRIPT)
        self.assertIn('write_extensions_config "$smoke_dir/core/token"', SCRIPT)
        self.assertIn('http://127.0.0.1:18080/health/ready)" = 503', SCRIPT)
        rejection = function("expect_credential_rejection")
        self.assertIn('wait "$service_pid" || status=$?', rejection)
        self.assertIn('[ "$status" -ne 0 ]', rejection)
        self.assertIn("grep -Eq", rejection)
        self.assertIn("grep -q 'private credential file unavailable'", rejection)

    def test_stop_reaps_term_child_with_graceful_or_signal_exit(self):
        # These are actual local child processes exercising shell cleanup only.
        # They are not substitutes for core/extension readiness.
        for handler in ("trap 'exit 0' TERM", "trap - TERM"):
            with self.subTest(handler=handler), tempfile.TemporaryDirectory() as directory:
                result = self.shell(function("stop_service") + '''
                    sh -c "$HANDLER; : > \\\"$READY\\\"; while :; do :; done" &
                    service_pid=$!
                    child_pid=$service_pid
                    while [ ! -f "$READY" ]; do sleep 0.01; done
                    stop_service
                    [ -z "$service_pid" ]
                    if kill -0 "$child_pid" 2>/dev/null; then exit 1; fi
                ''', HANDLER=handler, READY=directory + "/ready")
                self.assertEqual(result.returncode, 0, result.stderr)

    def test_stop_preserves_unexpected_child_failure(self):
        result = self.shell(function("stop_service") + '''
            sh -c 'exit 7' &
            service_pid=$!
            child_pid=$service_pid
            sleep 0.05
            result=0
            stop_service || result=$?
            [ "$result" -eq 7 ]
            [ -z "$service_pid" ]
            if kill -0 "$child_pid" 2>/dev/null; then exit 1; fi
        ''')
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_upgrade_removal_and_service_manager_limit_remain_explicit(self):
        self.assertIn("install_packages 2\nassert_not_activated", SCRIPT)
        self.assertIn("remove_packages\nassert_not_activated", SCRIPT)
        self.assertIn("for port in 8080 8081", function("assert_not_activated"))
        self.assertEqual(SCRIPT.count('[ "$(cat /var/lib/lmm/core/keep)" = operator-data ]'), 2)
        self.assertEqual(SCRIPT.count('[ "$(cat /etc/lmm.conf.d/secrets/core/keep)" = synthetic-preservation-fixture ]'), 2)
        self.assertIn("not service-manager acceptance", SCRIPT)
        self.assertNotRegex(SCRIPT, r"\b(?:systemctl|rc-service|init-db|bootstrap-user)\b")
        self.assertIn("trap 'stop_service' EXIT", SCRIPT)
        self.assertIn("trap 'exit 1' HUP INT TERM", SCRIPT)


@unittest.skipUnless(os.environ.get("LMM_INSTALL_SMOKE_TEST_IMAGE"),
                     "set LMM_INSTALL_SMOKE_TEST_IMAGE to a local Debian-family image with setpriv/useradd")
class ServiceUIDContainerTests(unittest.TestCase):
    def test_setpriv_uses_real_accounts_and_exec_pid_with_private_file_isolation(self):
        # Executor qualification only: sleep/cat are not product runtime fixtures.
        # No network, package installation or service host changes are permitted.
        probe = function("run_service") + "\n" + function("stop_service") + r'''
            format=deb
            useradd --system --user-group --no-create-home --shell /usr/sbin/nologin lmm-core
            useradd --system --user-group --no-create-home --shell /usr/sbin/nologin lmm-extensions
            mkdir /probe
            chmod 0755 /probe
            printf 'private-core-fixture\n' > /probe/core-token
            printf 'private-extension-fixture\n' > /probe/extensions-token
            chmod 0600 /probe/*-token
            if (run_service lmm-core cat /probe/core-token) >/dev/null 2>&1; then exit 1; fi
            if (run_service lmm-extensions cat /probe/extensions-token) >/dev/null 2>&1; then exit 1; fi
            chown lmm-core:lmm-core /probe/core-token
            chown lmm-extensions:lmm-extensions /probe/extensions-token
            [ "$(run_service lmm-core cat /probe/core-token)" = private-core-fixture ]
            [ "$(run_service lmm-extensions cat /probe/extensions-token)" = private-extension-fixture ]
            if (run_service lmm-core cat /probe/extensions-token) >/dev/null 2>&1; then exit 1; fi
            if (run_service lmm-extensions cat /probe/core-token) >/dev/null 2>&1; then exit 1; fi
            service_pid=
            trap 'stop_service' EXIT
            for service in lmm-core lmm-extensions; do
                run_service "$service" sleep 60 &
                service_pid=$!
                expected_exe=$(readlink -f "$(command -v sleep)")
                launched=0
                for _ in 1 2 3 4 5 6 7 8 9 10; do
                    if [ "$(run_service "$service" readlink "/proc/$service_pid/exe")" = "$expected_exe" ]; then
                        launched=1
                        break
                    fi
                    sleep 0.1
                done
                [ "$launched" -eq 1 ]
                uid=$(id -u "$service")
                gid=$(id -g "$service")
                awk -v uid="$uid" -v gid="$gid" '
                    /^Uid:/ { if ($2 != uid || $3 != uid || $4 != uid || $5 != uid) exit 1; u=1 }
                    /^Gid:/ { if ($2 != gid || $3 != gid || $4 != gid || $5 != gid) exit 1; g=1 }
                    /^Groups:/ { if (NF != 1) exit 1 }
                    /^NoNewPrivs:/ { if ($2 != 1) exit 1; n=1 }
                    END { if (!u || !g || !n) exit 1 }
                ' "/proc/$service_pid/status"
                stop_service
                [ -z "$service_pid" ]
            done
        '''
        result = DockerFixture().run(os.environ["LMM_INSTALL_SMOKE_TEST_IMAGE"], probe)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
