"""Offline fixture regressions; real Docker bind mounts are tested separately."""

import ast
import contextlib
import hashlib
import importlib.util
import io
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

SOURCE = Path(
    os.environ.get(
        "LMM_TEST_CORE_RPC_SOURCE", Path(__file__).with_name("test-core-rpc-docker.py")
    )
)
SPEC = importlib.util.spec_from_file_location("core_rpc_docker", SOURCE)
FIXTURE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(FIXTURE)
# A differential source snapshot may live outside the repository.
FIXTURE.ROOT = Path(__file__).resolve().parents[1]
FIXTURE.DOCKER = FIXTURE.ROOT / "deployment/docker"


class PrivateFixtureTests(unittest.TestCase):
    def test_existing_secret_symlink_never_mutates_its_external_target(self):
        with tempfile.TemporaryDirectory() as directory:
            parent = Path(directory)
            outside = parent / "unrelated-owner-material"
            outside.write_text("unrelated-private-canary")
            outside.chmod(0o600)
            original = (
                outside.read_bytes(),
                outside.stat().st_ino,
                stat.S_IMODE(outside.stat().st_mode),
            )
            fixture = parent / "fixture"
            fixture.mkdir(mode=0o700)
            (fixture / FIXTURE.SECRET_NAMES[0]).symlink_to(outside)
            values = {name: "new-synthetic-" + name for name in FIXTURE.SECRET_NAMES}
            with self.assertRaises(FileExistsError):
                FIXTURE.write_fixture_secrets(fixture, values)
            self.assertEqual(
                (
                    outside.read_bytes(),
                    outside.stat().st_ino,
                    stat.S_IMODE(outside.stat().st_mode),
                ),
                original,
            )
            self.assertEqual(len(list(fixture.iterdir())), 1)

    def test_mixed_allowed_and_foreign_mount_request_is_rejected_before_docker(self):
        with mock.patch.object(FIXTURE, "run") as run:
            with self.assertRaises(AssertionError):
                FIXTURE.fixture_secrets(
                    "fixture:test",
                    Path("/tmp/fixture"),
                    ("rpc-token", "../../unrelated-owner-secret"),
                    FIXTURE.PREPARE_SECRETS,
                    prepare=True,
                )
            run.assert_not_called()

    def test_fresh_install_component_inventory_is_exact(self):
        source = ast.parse(SOURCE.read_text())
        checks = [
            node.test
            for node in ast.walk(source)
            if isinstance(node, ast.Assert)
            and isinstance(node.test, ast.Compare)
            and ast.unparse(node.test.left) == "set(components)"
        ]
        self.assertEqual(len(checks), 1)
        check = checks[0]
        self.assertEqual(len(check.ops), 1)
        self.assertIsInstance(check.ops[0], ast.Eq)
        self.assertEqual(len(check.comparators), 1)
        expected = check.comparators[0]
        self.assertIsInstance(expected, ast.Set)
        self.assertEqual(len(expected.elts), 13)
        self.assertEqual(
            ast.literal_eval(expected),
            {
                "identity",
                "oauth-grants",
                "ledger",
                "payments",
                "charging",
                "routing",
                "events",
                "charging-ledger",
                "migration",
                "console",
                "model-gateway",
                "commerce",
                "abuse",
            },
        )

    def test_standalone_limits_are_read_from_the_current_toml_contract(self):
        with (FIXTURE.DOCKER / "config/core-standalone.toml").open("rb") as file:
            config = FIXTURE.tomllib.load(file)
        self.assertEqual(config["server"]["worker_threads"], 1)
        self.assertEqual(config["server"]["max_inflight"], 16)
        self.assertEqual(config["database"]["max_connections"], 2)
        self.assertEqual(
            config["database"]["url_file"], "/run/secrets/core_database_url"
        )

    def test_migration_audit_resolves_private_relation_before_switching_identity(self):
        before, after = FIXTURE.RUNTIME_PRIVILEGES_SQL.split(
            "SET SESSION AUTHORIZATION lmm_core_runtime;", 1
        )
        self.assertIn("'core_migration.import_runs'::regclass::oid", before)
        self.assertIn("AS migration_import_runs_oid \\gset", before)
        self.assertNotIn("core_migration.import_runs", after)
        for function, privileges in (
            ("has_table_privilege", "INSERT,UPDATE,DELETE,TRUNCATE"),
            ("has_any_column_privilege", "INSERT,UPDATE"),
        ):
            self.assertIn(
                f"NOT {function}(:'migration_import_runs_oid'::oid,'{privileges}')",
                after,
            )
        self.assertIn("current_user=session_user", after)
        self.assertIn(
            "NOT has_schema_privilege('core_migration','USAGE,CREATE')", after
        )
        self.assertNotRegex(FIXTURE.RUNTIME_PRIVILEGES_SQL, r"\b(?:GRANT|REVOKE)\b")

    def test_charging_audit_resolves_private_relation_before_switching_identity(self):
        before, after = FIXTURE.RUNTIME_PRIVILEGES_SQL.split(
            "SET SESSION AUTHORIZATION lmm_core_runtime;", 1
        )
        self.assertIn("'core_charging.requests'::regclass::oid", before)
        self.assertIn("AS charging_requests_oid \\gset", before)
        self.assertNotIn("core_charging.requests", after)
        for function, privileges in (
            ("has_table_privilege", "INSERT,UPDATE,DELETE,TRUNCATE"),
            ("has_any_column_privilege", "INSERT,UPDATE"),
        ):
            self.assertIn(
                f"NOT {function}(:'charging_requests_oid'::oid,'{privileges}')", after
            )
        self.assertIn("current_user=session_user", after)
        self.assertIn("NOT has_schema_privilege('core_charging','USAGE,CREATE')", after)
        self.assertNotRegex(FIXTURE.RUNTIME_PRIVILEGES_SQL, r"\b(?:GRANT|REVOKE)\b")

    def test_docker_commands_pin_local_socket_and_keep_secret_input_off_argv(self):
        with mock.patch.object(
            FIXTURE.subprocess, "run", return_value=SimpleNamespace(stdout="ok\n")
        ) as run:
            self.assertEqual(
                FIXTURE.run("docker", "version", data="synthetic-private-input"), "ok"
            )
        self.assertEqual(
            run.call_args.args[0],
            ("docker", "--host", "unix:///var/run/docker.sock", "version"),
        )
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

            with mock.patch.object(
                FIXTURE.os, "fdopen", side_effect=private_writer
            ) as opened:
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
            FIXTURE.fixture_secrets(
                "fixture:test",
                Path("/tmp/fixture"),
                FIXTURE.SECRET_NAMES,
                FIXTURE.PREPARE_SECRETS,
                prepare=True,
            )
        args = run.call_args.args
        self.assertIn("--read-only", args)
        self.assertIn("no-new-privileges:true", args)
        self.assertEqual(args[args.index("--network") + 1], "none")
        self.assertEqual(args[args.index("--cap-drop") + 1], "ALL")
        self.assertEqual(args[args.index("--user") + 1], "0:0")
        self.assertEqual(
            [args[i + 1] for i, arg in enumerate(args) if arg == "--cap-add"],
            ["CHOWN", "FOWNER"],
        )
        self.assertEqual(
            [args[i + 1] for i, arg in enumerate(args) if arg == "--mount"],
            [
                f"type=bind,source=/tmp/fixture/{name},target=/fixture-{name}"
                for name in FIXTURE.SECRET_NAMES
            ],
        )

    def test_verification_uses_image_user_and_read_only_mounts(self):
        with mock.patch.object(FIXTURE, "run") as run:
            FIXTURE.fixture_secrets(
                "fixture:test",
                Path("/tmp/fixture"),
                FIXTURE.SECRET_NAMES,
                FIXTURE.VERIFY_SECRETS,
            )
        args = run.call_args.args
        self.assertNotIn("--user", args)
        self.assertNotIn("--cap-add", args)
        self.assertIn('test "$(id -u):$(id -g)" = 65532:65532', FIXTURE.VERIFY_SECRETS)
        for i, arg in enumerate(args):
            if arg == "--mount":
                self.assertTrue(args[i + 1].endswith(",readonly"))

    def test_unknown_file_cannot_be_mounted(self):
        with (
            mock.patch.object(FIXTURE, "run") as run,
            self.assertRaises(AssertionError),
        ):
            FIXTURE.fixture_secrets(
                "fixture:test",
                Path("/tmp/fixture"),
                ("../unrelated",),
                FIXTURE.PREPARE_SECRETS,
                prepare=True,
            )
        run.assert_not_called()

    def test_rotation_passes_value_on_stdin_and_only_mounts_rpc_file_writable(self):
        with mock.patch.object(FIXTURE, "run") as run:
            FIXTURE.fixture_secrets(
                "fixture:test",
                Path("/tmp/fixture"),
                ("rpc-token",),
                FIXTURE.ROTATE_SECRET,
                data="replacement-synthetic-token",
            )
        args = run.call_args.args
        self.assertNotIn("--user", args)
        self.assertNotIn("--cap-add", args)
        self.assertNotIn("replacement-synthetic-token", args)
        self.assertEqual(run.call_args.kwargs, {"data": "replacement-synthetic-token"})
        self.assertEqual(
            [args[i + 1] for i, arg in enumerate(args) if arg == "--mount"],
            ["type=bind,source=/tmp/fixture/rpc-token,target=/fixture-rpc-token"],
        )

    def shell(self, script, *paths, data=None):
        # Exercise the actual shell logic as this test process's unprivileged UID.
        # Container UID 65532 and mount behavior still require the Docker scenario.
        script = script.replace("65532:65532", f"{os.getuid()}:{os.getgid()}")
        return subprocess.run(
            ["/bin/sh", "-ec", script, "fixture-secrets", *map(str, paths)],
            input=data,
            text=True,
            capture_output=True,
        )

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
                self.assertNotEqual(
                    self.shell(FIXTURE.VERIFY_SECRETS, path).returncode, 0
                )
            for mode in (0o444, 0o440, 0o404, 0o600):
                private.chmod(mode)
                self.assertNotEqual(
                    self.shell(FIXTURE.VERIFY_SECRETS, private).returncode, 0
                )

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
                    injected = (
                        'cat() { if [ "$#" = 0 ]; then '
                        + failure
                        + '; else command cat "$@"; fi; }\n'
                    )
                    result = self.shell(
                        injected + FIXTURE.ROTATE_SECRET, path, data="replacement"
                    )
                    self.assertNotEqual(result.returncode, 0)
                    self.assertEqual(path.stat().st_ino, inode)
                    self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o400)


# @mark deployment.database-authority role=test
class DurableOutputTests(unittest.TestCase):
    def test_cli_requires_explicit_output_before_any_docker_operation(self):
        with mock.patch.object(FIXTURE, "run") as run:
            with self.assertRaises(SystemExit) as error:
                FIXTURE.main([])
            self.assertNotEqual(error.exception.code, 0)
            run.assert_not_called()

    def test_relative_output_is_rejected_without_creating_it(self):
        with tempfile.TemporaryDirectory() as directory:
            parent = Path(directory)
            previous = Path.cwd()
            try:
                os.chdir(parent)
                with self.assertRaises(FIXTURE.FixtureError):
                    FIXTURE.prepare_output_root(Path("relative-evidence"))
                self.assertFalse((parent / "relative-evidence").exists())
            finally:
                os.chdir(previous)

    def test_output_symlink_cannot_redirect_or_destroy_existing_evidence(self):
        with tempfile.TemporaryDirectory() as directory:
            parent = Path(directory)
            target = parent / "existing-failed-evidence"
            target.mkdir(mode=0o700)
            sentinel = target / "failure.json"
            sentinel.write_text('{"result":"preserve"}')
            before = (sentinel.read_bytes(), sentinel.stat().st_ino)
            alias = parent / "new-run"
            alias.symlink_to(target, target_is_directory=True)
            with self.assertRaises(FIXTURE.FixtureError):
                FIXTURE.prepare_output_root(alias)
            self.assertTrue(alias.is_symlink())
            self.assertEqual((sentinel.read_bytes(), sentinel.stat().st_ino), before)

    def test_nonempty_output_is_not_reused_or_cleared(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "previous-failure"
            root.mkdir(mode=0o700)
            sentinel = root / "original-secret"
            sentinel.write_text("synthetic-preserved-value")
            before = (sentinel.read_bytes(), sentinel.stat().st_ino)
            with self.assertRaises(FIXTURE.FixtureError):
                FIXTURE.prepare_output_root(root)
            self.assertEqual((sentinel.read_bytes(), sentinel.stat().st_ino), before)

    def test_new_output_is_private_before_any_secret_is_written(self):
        with tempfile.TemporaryDirectory() as directory:
            requested = Path(directory) / "fresh-run"
            root = FIXTURE.prepare_output_root(requested)
            self.assertEqual(root, requested)
            self.assertEqual(stat.S_IMODE(root.stat().st_mode), 0o700)
            self.assertEqual(list(root.iterdir()), [])


class FailureLifecycleTests(unittest.TestCase):
    """Inject Docker boundary failures, never simulate successful business RPC."""

    def context(self, parent, files=("LICENSE",)):
        root = parent / "fixture"
        root.mkdir(mode=0o700)
        inputs = {
            name: hashlib.sha256((FIXTURE.ROOT / name).read_bytes()).hexdigest()
            for name in files
        }
        patch = mock.patch.object(FIXTURE, "source_inputs", return_value=inputs)
        patch.start()
        self.addCleanup(patch.stop)
        projects = {
            "core": "lmm-fresh-core-offline",
            "extensions": "lmm-fresh-ext-offline",
        }
        return FIXTURE.FixtureRun(root, projects, "offline-nonce", inputs)

    def test_frozen_public_license_preserves_non_root_readable_source_mode(self):
        with tempfile.TemporaryDirectory() as directory:
            ctx = self.context(Path(directory))
            source = FIXTURE.ROOT / "LICENSE"
            frozen = ctx.root / "build-context/LICENSE"
            self.assertEqual(frozen.read_bytes(), source.read_bytes())
            self.assertEqual(
                stat.S_IMODE(frozen.stat().st_mode), stat.S_IMODE(source.stat().st_mode)
            )
            self.assertTrue(frozen.stat().st_mode & stat.S_IROTH)
            self.assertEqual(stat.S_IMODE(ctx.root.stat().st_mode), 0o700)
            self.assertEqual(
                stat.S_IMODE((ctx.root / "build-context").stat().st_mode), 0o700
            )

    def test_source_mode_drift_rejects_even_when_source_bytes_are_unchanged(self):
        with tempfile.TemporaryDirectory() as directory:
            parent = Path(directory)
            source_root = parent / "source"
            source_root.mkdir(mode=0o700)
            source = source_root / "LICENSE"
            source.write_text("synthetic-public-resource")
            source.chmod(0o644)
            with mock.patch.object(FIXTURE, "ROOT", source_root):
                ctx = self.context(parent)
                content = source.read_bytes()
                source.chmod(0o600)
                self.assertEqual(source.read_bytes(), content)
                with self.assertRaises(FIXTURE.FixtureError):
                    ctx.check_sources()

    def test_public_template_directories_keep_search_permission_and_detect_mode_drift(
        self,
    ):
        with tempfile.TemporaryDirectory() as directory:
            parent = Path(directory)
            source_root = parent / "source"
            source_root.mkdir(mode=0o700)
            template = source_root / "config/legal/templates/legal.html"
            template.parent.mkdir(parents=True, mode=0o755)
            (source_root / "LICENSE").write_text("synthetic-license")
            template.write_text("synthetic-public-template")
            template.chmod(0o644)
            with mock.patch.object(FIXTURE, "ROOT", source_root):
                ctx = self.context(
                    parent, ("LICENSE", "config/legal/templates/legal.html")
                )
                for name in ("config", "config/legal", "config/legal/templates"):
                    original = source_root / name
                    copied = ctx.root / "build-context" / name
                    self.assertEqual(
                        stat.S_IMODE(copied.stat().st_mode),
                        stat.S_IMODE(original.stat().st_mode),
                    )
                    self.assertTrue(copied.stat().st_mode & stat.S_IXOTH)
                self.assertEqual(
                    (
                        ctx.root / "build-context/config/legal/templates/legal.html"
                    ).read_bytes(),
                    template.read_bytes(),
                )
                template.parent.chmod(0o700)
                with self.assertRaises(FIXTURE.FixtureError):
                    ctx.check_sources()

    def test_frozen_context_mode_drift_rejects_without_mutating_original_source(self):
        with tempfile.TemporaryDirectory() as directory:
            ctx = self.context(Path(directory))
            source = FIXTURE.ROOT / "LICENSE"
            before = (source.read_bytes(), stat.S_IMODE(source.stat().st_mode))
            frozen = ctx.root / "build-context/LICENSE"
            frozen.chmod(0o600)
            with self.assertRaises(FIXTURE.FixtureError):
                ctx.check_sources()
            self.assertEqual(
                (source.read_bytes(), stat.S_IMODE(source.stat().st_mode)), before
            )

    def test_command_failure_keeps_secret_output_private_and_off_argv_and_receipt(self):
        with tempfile.TemporaryDirectory() as directory:
            ctx = self.context(Path(directory))
            canary = "synthetic-sensitive-command-canary"
            failed = subprocess.CompletedProcess(
                [], 19, canary, "private-error:" + canary
            )
            public = io.StringIO()
            with (
                mock.patch.object(
                    FIXTURE.subprocess, "run", return_value=failed
                ) as called,
                contextlib.redirect_stdout(public),
                contextlib.redirect_stderr(public),
            ):
                with self.assertRaises(subprocess.CalledProcessError) as error:
                    ctx.command(["docker", "version"], data=canary)
                self.assertEqual(called.call_args.kwargs["input"], canary)
                self.assertNotIn(canary, called.call_args.args[0])
                with mock.patch.object(ctx, "capture_owned", return_value=[]):
                    self.assertFalse(ctx.finish(False, error.exception))
            self.assertNotIn(canary, public.getvalue())
            for path in ctx.root.glob("*.json"):
                self.assertNotIn(canary, path.read_text())
                self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            for suffix in ("stdout", "stderr"):
                log = ctx.root / ("command-0001." + suffix)
                self.assertIn(canary, log.read_text())
                self.assertEqual(stat.S_IMODE(log.stat().st_mode), 0o600)
            self.assertEqual(
                json.loads((ctx.root / "result.json").read_text())["result"], "FAIL"
            )

    def test_build_timeout_is_unknown_and_never_becomes_a_success_receipt(self):
        with tempfile.TemporaryDirectory() as directory:
            ctx = self.context(Path(directory))
            error = subprocess.TimeoutExpired(
                ["docker", "build"],
                1,
                output=b"private-build-output",
                stderr=b"private-build-error",
            )
            with mock.patch.object(FIXTURE.subprocess, "run", side_effect=error):
                with self.assertRaises(FIXTURE.FixtureError) as caught:
                    ctx.command(["docker", "build", "."], timeout=1)
            with mock.patch.object(ctx, "capture_owned", return_value=[]):
                self.assertFalse(ctx.finish(False, caught.exception))
            result = json.loads((ctx.root / "result.json").read_text())
            self.assertEqual(result["result"], "FAIL")
            self.assertEqual(result["daemon_build_completion"], "UNKNOWN")
            self.assertTrue(result["failure_resources_retained"])

    def test_daemon_failure_does_not_prove_resource_absence(self):
        with tempfile.TemporaryDirectory() as directory:
            ctx = self.context(Path(directory))
            failure = subprocess.CompletedProcess(
                [], 1, "", "Cannot connect to the Docker daemon"
            )
            with mock.patch.object(FIXTURE.subprocess, "run", return_value=failure):
                with self.assertRaises(FIXTURE.FixtureError):
                    ctx.absent("container", "owned-original-id")

    def test_actual_docker_network_missing_response_proves_only_requested_absence(self):
        with tempfile.TemporaryDirectory() as directory:
            ctx = self.context(Path(directory))
            missing = subprocess.CompletedProcess(
                [],
                1,
                "",
                "Error response from daemon: network own-fresh-network not found\n",
            )
            with mock.patch.object(FIXTURE.subprocess, "run", return_value=missing):
                self.assertTrue(ctx.absent("network", "own-fresh-network"))

    def test_different_missing_network_does_not_prove_requested_network_absent(self):
        with tempfile.TemporaryDirectory() as directory:
            ctx = self.context(Path(directory))
            unrelated = subprocess.CompletedProcess(
                [],
                1,
                "",
                "Error response from daemon: network unrelated-network not found\n",
            )
            with mock.patch.object(FIXTURE.subprocess, "run", return_value=unrelated):
                with self.assertRaises(FIXTURE.FixtureError):
                    ctx.absent("network", "own-fresh-network")

    def test_existing_image_tag_is_not_overwritten_or_accepted_as_this_build(self):
        with tempfile.TemporaryDirectory() as directory:
            ctx = self.context(Path(directory))
            ctx.configs = {
                "core": {"services": {"core": {"image": "foreign-image:existing"}}}
            }
            with (
                mock.patch.object(ctx, "absent", return_value=False),
                mock.patch.object(ctx, "run") as calls,
            ):
                with self.assertRaises(FIXTURE.FixtureError):
                    ctx.preflight_resources()
            calls.assert_not_called()

    def test_unobserved_build_cannot_attest_a_cached_image(self):
        with tempfile.TemporaryDirectory() as directory:
            ctx = self.context(Path(directory))
            with mock.patch.object(ctx, "run") as calls:
                with self.assertRaises(FIXTURE.FixtureError):
                    ctx.bind_image("core", "cached-image:unproven")
            calls.assert_not_called()
            self.assertFalse((ctx.root / "image-core.json").exists())

    def test_existing_data_volume_is_not_adopted_or_deleted(self):
        with tempfile.TemporaryDirectory() as directory:
            ctx = self.context(Path(directory))
            ctx.configs = {
                "core": {
                    "services": {"core": {"image": "fresh-image:test"}},
                    "volumes": {"data": {"name": "foreign-preexisting-data"}},
                }
            }
            with (
                mock.patch.object(
                    ctx,
                    "absent",
                    side_effect=lambda kind, name: (
                        not (kind == "volume" and name == "foreign-preexisting-data")
                    ),
                ),
                mock.patch.object(ctx, "run", return_value="") as calls,
            ):
                with self.assertRaises(FIXTURE.FixtureError):
                    ctx.preflight_resources()
            self.assertFalse(
                any(
                    "rm" in c.args or "stop" in c.args or "prune" in c.args
                    for c in calls.call_args_list
                )
            )

    def test_failure_stops_exact_owned_container_and_preserves_data_and_secrets(self):
        with tempfile.TemporaryDirectory() as directory:
            ctx = self.context(Path(directory))
            secret = ctx.root / "runtime-secret"
            secret.write_text("synthetic-recoverable-secret")
            secret.chmod(0o600)
            row = {"id": "owned-original-id", "project": ctx.projects["core"]}
            info = {
                "Id": row["id"],
                "Config": {
                    "Labels": {
                        FIXTURE.LABEL: ctx.nonce,
                        "com.docker.compose.project": row["project"],
                    }
                },
                "State": {"Running": False},
            }
            commands = []

            def command(*args, **kwargs):
                commands.append(args)
                return json.dumps([info]) if "inspect" in args else ""

            with (
                mock.patch.object(ctx, "capture_owned", return_value=[row]),
                mock.patch.object(ctx, "run", side_effect=command),
                mock.patch.object(
                    ctx,
                    "command",
                    return_value=subprocess.CompletedProcess([], 0, "", ""),
                ),
            ):
                self.assertFalse(ctx.finish(False, RuntimeError("synthetic-failure")))
            self.assertIn(("docker", "stop", "--time", "8", row["id"]), commands)
            self.assertFalse(
                any(
                    "rm" in args or "down" in args or "prune" in args
                    for args in commands
                )
            )
            self.assertEqual(secret.read_text(), "synthetic-recoverable-secret")
            self.assertTrue((ctx.root / "allocation.json").exists())
            self.assertTrue((ctx.root / "source-binding.json").exists())

    def test_changed_ownership_blocks_stop_and_cleanup_success(self):
        with tempfile.TemporaryDirectory() as directory:
            ctx = self.context(Path(directory))
            row = {"id": "formerly-owned-id", "project": ctx.projects["core"]}
            changed = {
                "Id": row["id"],
                "Config": {
                    "Labels": {
                        FIXTURE.LABEL: "foreign-nonce",
                        "com.docker.compose.project": row["project"],
                    }
                },
            }
            with (
                mock.patch.object(ctx, "capture_owned", return_value=[row]),
                mock.patch.object(
                    ctx, "run", return_value=json.dumps([changed])
                ) as calls,
                mock.patch.object(
                    ctx,
                    "command",
                    return_value=subprocess.CompletedProcess([], 0, "", ""),
                ),
            ):
                with self.assertRaises(FIXTURE.FixtureError):
                    ctx.finish(True)
            self.assertFalse(
                any("stop" in c.args or "rm" in c.args for c in calls.call_args_list)
            )
            self.assertEqual(
                json.loads((ctx.root / "result.json").read_text())["result"], "FAIL"
            )

    def test_early_business_failure_keeps_durable_receipt_and_private_fixture(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "failed-run"
            inputs = {
                "LICENSE": hashlib.sha256(
                    (FIXTURE.ROOT / "LICENSE").read_bytes()
                ).hexdigest()
            }
            canary = "synthetic-error-must-not-be-public"

            def fail(root, projects, context):
                FIXTURE.private_write(root / "retained-secret", canary)
                raise RuntimeError(canary)

            public = io.StringIO()
            with (
                mock.patch.object(FIXTURE, "source_inputs", return_value=inputs),
                mock.patch.object(FIXTURE, "execute", side_effect=fail),
                mock.patch.object(FIXTURE.FixtureRun, "capture_owned", return_value=[]),
                contextlib.redirect_stdout(public),
            ):
                self.assertEqual(FIXTURE.main(["--output-root", str(root)]), 1)
            self.assertTrue(root.is_dir())
            self.assertEqual((root / "retained-secret").read_text(), canary)
            self.assertNotIn(canary, public.getvalue())
            self.assertNotIn(canary, (root / "failure.json").read_text())
            self.assertNotIn(canary, (root / "result.json").read_text())
            self.assertEqual(
                json.loads((root / "failure.json").read_text())["error_type"],
                "RuntimeError",
            )


@unittest.skipUnless(shutil.which("docker"), "Docker Compose client is not installed")
class ComposeConfigurationTests(unittest.TestCase):
    def test_actual_compose_overlays_keep_file_only_configuration_and_authority_separate(
        self,
    ):
        # `compose config` needs only the local client. It creates no containers,
        # networks, volumes, database roles or processes from these definitions.
        with tempfile.TemporaryDirectory(prefix="lmm-compose-contract-") as directory:
            temp = Path(directory)
            env = {
                key: value
                for key, value in os.environ.items()
                if not key.startswith(("LMM_DOCKER_", "COMPOSE_"))
            }
            env.update(
                COMPOSE_DISABLE_ENV_FILE="true",
                LMM_DOCKER_DATABASE_URL_SECRET_FILE=str(temp / "runtime-url"),
                LMM_DOCKER_DATABASE_OWNER_URL_SECRET_FILE=str(temp / "owner-url"),
                LMM_DOCKER_DATABASE_PASSWORD_SECRET_FILE=str(temp / "password"),
                LMM_DOCKER_RPC_TOKEN_SECRET_FILE=str(temp / "rpc-token"),
                LMM_DOCKER_EXTENSIONS_TOKEN_SECRET_FILE=str(temp / "host-token"),
                LMM_DOCKER_NETWORK="lmm-compose-contract-runtime",
                LMM_DOCKER_RPC_VOLUME="lmm-compose-contract-rpc",
                LMM_DOCKER_LEGAL_CONFIG_FILE=str(
                    FIXTURE.ROOT / "config/legal/site.example.toml"
                ),
            )
            for name in (
                "runtime-url",
                "owner-url",
                "password",
                "rpc-token",
                "host-token",
            ):
                (temp / name).touch(mode=0o600)

            def render(files, tools=False, without=None):
                selected_env = env.copy()
                if "legal" in files:
                    selected_env["LMM_DOCKER_EXTENSIONS_CONFIG_FILE"] = str(
                        FIXTURE.DOCKER / "config/extensions-legal.toml"
                    )
                if without:
                    selected_env.pop(without, None)
                args = [
                    "docker",
                    "--host",
                    FIXTURE.DOCKER_ENDPOINT,
                    "compose",
                    "-p",
                    "lmm-compose-contract",
                ]
                for name in files:
                    args += ["-f", str(FIXTURE.DOCKER / ("compose." + name + ".yml"))]
                if tools:
                    args += ["--profile", "tools"]
                result = subprocess.run(
                    [*args, "config", "--format", "json"],
                    cwd=FIXTURE.ROOT,
                    env=selected_env,
                    text=True,
                    capture_output=True,
                )
                if without:
                    self.assertNotEqual(result.returncode, 0)
                    return None
                self.assertEqual(result.returncode, 0, result.stderr)
                return json.loads(result.stdout)

            cases = (
                ("core",),
                ("extensions",),
                ("core", "identity"),
                ("core", "identity", "rpc-core"),
                ("core", "core-standalone"),
                ("extensions", "rpc-extensions"),
                ("extensions", "legal"),
            )
            for files in cases:
                with self.subTest(files=files):
                    document = render(files, tools="identity" in files)
                    self.assertEqual(
                        document["networks"]["core"]["name"],
                        "lmm-compose-contract-runtime",
                    )
                    if "rpc-core" in files or "rpc-extensions" in files:
                        self.assertEqual(
                            document["volumes"]["core-rpc"]["name"],
                            "lmm-compose-contract-rpc",
                        )
                    for service, spec in document["services"].items():
                        if service == "core-db":
                            self.assertFalse(spec.get("ports"))
                            continue
                        component = "extensions" if service == "extensions" else "core"
                        cfg = FIXTURE.validate_file_configuration(
                            document, service, component, init=service == "core-init"
                        )
                        if "rpc-core" in files:
                            self.assertEqual(
                                cfg.get("rpc", {}).get("socket"),
                                None
                                if service == "core-init"
                                else "/run/lmm-core-rpc/core.sock",
                            )
                        if "legal" in files:
                            self.assertEqual(
                                cfg["legal"]["config_file"],
                                "/etc/lmm.conf.d/legal.toml",
                            )
                            self.assertIn(
                                "/etc/lmm.conf.d/legal.toml",
                                [value["target"] for value in spec["configs"]],
                            )
            standalone = render(("core", "core-standalone"), tools=True)
            self.assertEqual(set(standalone["services"]), {"core", "core-init"})
            FIXTURE.validate_file_configuration(
                standalone, "core-init", "core", init=True
            )
            runtime = standalone["services"]["core"]
            # Compose versions serialize byte limits as either strings or integers.
            self.assertEqual(int(runtime["mem_limit"]), 256 * 1024 * 1024)
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
