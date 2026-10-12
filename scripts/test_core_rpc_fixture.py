"""Offline fixture regressions; real Docker bind mounts are tested separately."""
import importlib.util
import json
import os
from pathlib import Path
import stat
import shutil
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location(
    "core_rpc_docker", Path(__file__).with_name("test-core-rpc-docker.py")
)
FIXTURE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(FIXTURE)


class PrivateFixtureTests(unittest.TestCase):
    def test_standalone_limits_are_read_from_the_current_toml_contract(self):
        with (FIXTURE.DOCKER / "config/core-standalone.toml").open("rb") as file:
            config = FIXTURE.tomllib.load(file)
        self.assertEqual(config["server"]["worker_threads"], 1)
        self.assertEqual(config["server"]["max_inflight"], 16)
        self.assertEqual(config["database"]["max_connections"], 2)
        self.assertEqual(config["database"]["url_file"], "/run/secrets/core_database_url")

    def test_migration_audit_resolves_private_relation_before_switching_identity(self):
        before, after = FIXTURE.RUNTIME_PRIVILEGES_SQL.split(
            "SET SESSION AUTHORIZATION lmm_core_runtime;", 1)
        self.assertIn("'core_migration.import_runs'::regclass::oid", before)
        self.assertIn("AS migration_import_runs_oid \\gset", before)
        self.assertNotIn("core_migration.import_runs", after)
        for function, privileges in (
            ("has_table_privilege", "INSERT,UPDATE,DELETE,TRUNCATE"),
            ("has_any_column_privilege", "INSERT,UPDATE"),
        ):
            self.assertIn(
                f"NOT {function}(:'migration_import_runs_oid'::oid,'{privileges}')", after)
        self.assertIn("current_user=session_user", after)
        self.assertIn("NOT has_schema_privilege('core_migration','USAGE,CREATE')", after)
        self.assertNotRegex(FIXTURE.RUNTIME_PRIVILEGES_SQL, r"\b(?:GRANT|REVOKE)\b")

    def test_docker_commands_pin_local_socket_and_keep_secret_input_off_argv(self):
        with mock.patch.object(FIXTURE.subprocess, "run", return_value=SimpleNamespace(stdout="ok\n")) as run:
            self.assertEqual(FIXTURE.run("docker", "version", data="synthetic-private-input"), "ok")
        self.assertEqual(run.call_args.args[0],
                         ("docker", "--host", "unix:///var/run/docker.sock", "version"))
        self.assertEqual(run.call_args.kwargs["input"], "synthetic-private-input")
        self.assertNotIn("synthetic-private-input", run.call_args.args[0])

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
                self.assertEqual(opened.call_count, len(FIXTURE.SECRET_NAMES))
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


@unittest.skipUnless(shutil.which("docker"), "Docker Compose client is not installed")
class ComposeConfigurationTests(unittest.TestCase):
    def test_actual_compose_overlays_keep_file_only_configuration_and_authority_separate(self):
        # `compose config` needs only the local client. It creates no containers,
        # networks, volumes, database roles or processes from these definitions.
        with tempfile.TemporaryDirectory(prefix="lmm-compose-contract-") as directory:
            temp = Path(directory)
            env = {key: value for key, value in os.environ.items()
                   if not key.startswith(("LMM_DOCKER_", "COMPOSE_"))}
            env.update(COMPOSE_DISABLE_ENV_FILE="true",
                       LMM_DOCKER_DATABASE_URL_SECRET_FILE=str(temp / "runtime-url"),
                       LMM_DOCKER_DATABASE_OWNER_URL_SECRET_FILE=str(temp / "owner-url"),
                       LMM_DOCKER_DATABASE_PASSWORD_SECRET_FILE=str(temp / "password"),
                       LMM_DOCKER_RPC_TOKEN_SECRET_FILE=str(temp / "rpc-token"),
                       LMM_DOCKER_EXTENSIONS_TOKEN_SECRET_FILE=str(temp / "host-token"),
                       LMM_DOCKER_NETWORK="lmm-compose-contract-runtime",
                       LMM_DOCKER_RPC_VOLUME="lmm-compose-contract-rpc",
                       LMM_DOCKER_LEGAL_CONFIG_FILE=str(FIXTURE.ROOT / "config/legal/site.example.toml"))
            for name in ("runtime-url", "owner-url", "password", "rpc-token", "host-token"):
                (temp / name).touch(mode=0o600)

            def render(files, tools=False, without=None):
                selected_env = env.copy()
                if "legal" in files:
                    selected_env["LMM_DOCKER_EXTENSIONS_CONFIG_FILE"] = str(
                        FIXTURE.DOCKER / "config/extensions-legal.toml")
                if without:
                    selected_env.pop(without, None)
                args = ["docker", "--host", FIXTURE.DOCKER_ENDPOINT, "compose", "-p", "lmm-compose-contract"]
                for name in files:
                    args += ["-f", str(FIXTURE.DOCKER / ("compose." + name + ".yml"))]
                if tools:
                    args += ["--profile", "tools"]
                result = subprocess.run([*args, "config", "--format", "json"],
                                        cwd=FIXTURE.ROOT, env=selected_env,
                                        text=True, capture_output=True)
                if without:
                    self.assertNotEqual(result.returncode, 0)
                    return None
                self.assertEqual(result.returncode, 0, result.stderr)
                return json.loads(result.stdout)

            cases = (("core",), ("extensions",), ("core", "identity"),
                     ("core", "identity", "rpc-core"), ("core", "core-standalone"),
                     ("extensions", "rpc-extensions"), ("extensions", "legal"))
            for files in cases:
                with self.subTest(files=files):
                    document = render(files, tools="identity" in files)
                    self.assertEqual(document["networks"]["core"]["name"], "lmm-compose-contract-runtime")
                    if "rpc-core" in files or "rpc-extensions" in files:
                        self.assertEqual(document["volumes"]["core-rpc"]["name"], "lmm-compose-contract-rpc")
                    for service, spec in document["services"].items():
                        if service == "core-db":
                            self.assertFalse(spec.get("ports"))
                            continue
                        component = "extensions" if service == "extensions" else "core"
                        cfg = FIXTURE.validate_file_configuration(document, service, component,
                                                                  init=service == "core-init")
                        if "rpc-core" in files:
                            self.assertEqual(cfg.get("rpc", {}).get("socket"),
                                             None if service == "core-init" else "/run/lmm-core-rpc/core.sock")
                        if "legal" in files:
                            self.assertEqual(cfg["legal"]["config_file"], "/etc/lmm.conf.d/legal.toml")
                            self.assertIn("/etc/lmm.conf.d/legal.toml",
                                          [value["target"] for value in spec["configs"]])
            standalone = render(("core", "core-standalone"), tools=True)
            self.assertEqual(set(standalone["services"]), {"core", "core-init"})
            FIXTURE.validate_file_configuration(standalone, "core-init", "core", init=True)
            runtime = standalone["services"]["core"]
            self.assertEqual(runtime["mem_limit"], 256 * 1024 * 1024)
            self.assertEqual(runtime["pids_limit"], 64)
            self.assertEqual(float(runtime["cpus"]), 1.0)
            config = FIXTURE.validate_file_configuration(standalone, "core", "core")
            self.assertEqual(config["server"]["worker_threads"], 1)
            self.assertEqual(config["database"]["max_connections"], 2)
            self.assertEqual(config["server"]["max_inflight"], 16)
            render(("extensions",), without="LMM_DOCKER_NETWORK")
            render(("core", "identity", "rpc-core"), without="LMM_DOCKER_RPC_VOLUME")
            render(("extensions", "rpc-extensions"), without="LMM_DOCKER_RPC_VOLUME")


if __name__ == "__main__":
    unittest.main()
