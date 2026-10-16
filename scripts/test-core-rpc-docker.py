#!/usr/bin/env python3
"""Disposable fresh-install test: Go -> Protobuf -> Rust -> PostgreSQL."""

import json
import copy
import os
from pathlib import Path
import secrets
import subprocess
import argparse
import hashlib
import re
import stat
import signal
import time
import tomllib
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
DOCKER = ROOT / "deployment/docker"
DOCKER_ENDPOINT = "unix:///var/run/docker.sock"
SECRET_NAMES = (
    "database-password",
    "database-owner-url",
    "database-url",
    "rpc-token",
    "extension-token",
)
PREPARE_SECRETS = """
for path in "$@"; do
    test -f "$path"
    test ! -L "$path"
    chown 65532:65532 "$path"
    chmod 0400 "$path"
done
"""
VERIFY_SECRETS = """
test "$(id -u):$(id -g)" = 65532:65532
for path in "$@"; do
    test -f "$path"
    test ! -L "$path"
    test "$(stat -c '%u:%g:%a' "$path")" = 65532:65532:400
    test -r "$path"
    cat "$path" > /dev/null
done
"""
ROTATE_SECRET = (
    VERIFY_SECRETS
    + """
test "$#" = 1
fixture_secret_path=$1
trap 'chmod 0400 "$fixture_secret_path"' EXIT
trap 'exit 1' HUP INT TERM
chmod 0600 "$fixture_secret_path"
cat > "$fixture_secret_path"
chmod 0400 "$fixture_secret_path"
"""
    + VERIFY_SECRETS
)


# Resolve private relations while still the installation user. The runtime
# must not receive schema USAGE merely to inspect its lack of write privileges.
RUNTIME_PRIVILEGES_SQL = r"""SELECT 'core_migration.import_runs'::regclass::oid AS migration_import_runs_oid \gset
SELECT 'core_charging.requests'::regclass::oid AS charging_requests_oid \gset
SET SESSION AUTHORIZATION lmm_core_runtime;
SELECT current_user=session_user
  AND NOT has_database_privilege(current_database(),'CREATE,TEMP')
  AND NOT has_schema_privilege('public','CREATE')
  AND NOT has_table_privilege('core_billing.ledger_entries','INSERT,UPDATE,DELETE,TRUNCATE')
  AND NOT has_table_privilege('core_billing.opening_imports','INSERT,UPDATE,DELETE,TRUNCATE')
  AND NOT has_table_privilege(:'migration_import_runs_oid'::oid,'INSERT,UPDATE,DELETE,TRUNCATE')
  AND NOT has_any_column_privilege(:'migration_import_runs_oid'::oid,'INSERT,UPDATE')
  AND NOT has_schema_privilege('core_migration','USAGE,CREATE')
  AND NOT has_function_privilege('core_billing.import_opening_balance(jsonb)','EXECUTE')
  AND NOT has_function_privilege('core_billing.import_account_opening_counters(jsonb)','EXECUTE')
  AND NOT has_table_privilege('core_billing.account_opening_counters','INSERT,UPDATE,DELETE,TRUNCATE')
  AND NOT has_schema_privilege('core_charging','USAGE,CREATE')
  AND NOT has_table_privilege(:'charging_requests_oid'::oid,'INSERT,UPDATE,DELETE,TRUNCATE')
  AND NOT has_any_column_privilege(:'charging_requests_oid'::oid,'INSERT,UPDATE');
"""


ACTIVE = None
LABEL = "best.lmm.rpc-fixture"


def run(*args, data=None, env=None, timeout=60):
    if ACTIVE is not None:
        return ACTIVE.run(*args, data=data, env=env, timeout=timeout)
    if args and args[0] == "docker":
        args = (args[0], "--host", DOCKER_ENDPOINT, *args[1:])
    return subprocess.run(
        args,
        cwd=ROOT,
        input=data,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=True,
        env=env,
        timeout=timeout,
    ).stdout.strip()


def write_fixture_secrets(temp, values):
    for name in SECRET_NAMES:
        # Private from creation, including while the synthetic value is written.
        fd = os.open(temp / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as file:
            file.write(values[name])


def validate_file_configuration(document, service, component, *, init=False):
    """Check the rendered operator contract before starting any container."""
    spec = document["services"][service]
    if init:
        assert spec["entrypoint"] == ["/usr/bin/lmm-core"]
    target = f"/etc/lmm.conf.d/{component}.toml"
    command = ["--config", target] + (["init-db"] if init else [])
    assert spec["command"] == command
    assert not any(name.startswith("LMM_") for name in spec.get("environment", {}))
    selected = [value for value in spec["configs"] if value["target"] == target]
    assert len(selected) == 1
    with open(document["configs"][selected[0]["source"]]["file"], "rb") as file:
        config = tomllib.load(file)
    assert config["version"] == 1
    mounted = {value["source"] for value in spec.get("secrets", [])}
    if component == "core":
        database = config.get("database")
        if database:
            secret = "core_database_owner_url" if init else "core_database_url"
            assert database["url_file"] == "/run/secrets/" + secret
            assert secret in mounted
        assert ("core_database_owner_url" in mounted) == init
        assert "core_database_password" not in mounted
    else:
        assert config["host"]["token_file"] == "/run/secrets/extension_token"
        assert "extension_token" in mounted
        assert not any(name.startswith("core_database") for name in mounted)
    return config


def fixture_secrets(image, temp, names, script, *, data=None, prepare=False):
    # Mount only these disposable files, never their parent or any host secrets.
    # Compose file secrets retain bind-source ownership/mode, so uid/mode fields
    # in Compose alone cannot make host-owned files private and container-readable.
    args = [
        "docker",
        "run",
        "--rm",
        "-i",
        "--network",
        "none",
        "--read-only",
        "--cap-drop",
        "ALL",
        "--security-opt",
        "no-new-privileges:true",
    ]
    if prepare:
        args.extend(["--user", "0:0", "--cap-add", "CHOWN", "--cap-add", "FOWNER"])
    if ACTIVE is not None:
        helper = ACTIVE.projects["core"] + "-helper-" + secrets.token_hex(5)
        ACTIVE.record(
            "helper-" + helper + ".json",
            {"name": helper, "image": image, "nonce": ACTIVE.nonce},
        )
        args.extend(
            [
                "--name",
                helper,
                "--label",
                LABEL + "=" + ACTIVE.nonce,
                "--label",
                "com.docker.compose.project=" + ACTIVE.projects["core"],
            ]
        )
    paths = []
    for name in names:
        if name not in SECRET_NAMES:
            raise AssertionError("Only exact disposable fixture names can be mounted")
        path = "/fixture-" + name
        mount = f"type=bind,source={temp / name},target={path}"
        if not prepare and data is None:
            mount += ",readonly"
        args.extend(["--mount", mount])
        paths.append(path)
    return run(
        *args,
        "--entrypoint",
        "/bin/sh",
        image,
        "-ec",
        script,
        "fixture-secrets",
        *paths,
        data=data,
    )


def request(url, token=None, method="GET", body=None, user=None):
    headers = {"Authorization": "Bearer " + token} if token else {}
    if user is not None:
        headers["X-LMM-User-Credential"] = user
    payload = None
    if body is not None:
        payload = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=payload, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=5) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


class FixtureError(RuntimeError):
    """Safe public description; raw Docker/SQL output stays in private files."""


def digest(path):
    with Path(path).open("rb") as file:
        return hashlib.file_digest(file, "sha256").hexdigest()


def prepare_output_root(path):
    path = Path(path)
    if not path.is_absolute() or path.resolve() != path:
        raise FixtureError("output root must be absolute without symlink components")
    if not path.exists():
        path.mkdir(mode=0o700)
    info = path.lstat()
    if (
        not stat.S_ISDIR(info.st_mode)
        or info.st_uid != os.getuid()
        or stat.S_IMODE(info.st_mode) != 0o700
        or any(path.iterdir())
    ):
        raise FixtureError("output root must be a new empty owned 0700 directory")
    return path


def private_write(path, value):
    data = value if isinstance(value, bytes) else value.encode()
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "wb") as file:
        file.write(data)
        file.flush()
        os.fsync(file.fileno())
    parent = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(parent)
    finally:
        os.close(parent)


def source_inputs():
    # Exact Docker COPY trees plus the fixture/operator inputs. Ignore generated
    # caches and documentation excluded by the reviewed .dockerignore; never
    # send existing test secrets/fixtures as Docker build context.
    trees = [
        "services/core/src",
        "services/core/schema",
        "contracts/proto",
        "services/extensions",
        "deployment/docker",
        "deployment/postgres",
        "config/legal/templates",
    ]
    selected = {
        ROOT / name
        for name in [
            ".dockerignore",
            "LICENSE",
            "NOTICE",
            "THIRD-PARTY-LICENSES.md",
            "services/core/Cargo.toml",
            "services/core/Cargo.lock",
            "services/core/build.rs",
            "config/legal/site.example.toml",
            "scripts/test-core-rpc-docker.py",
        ]
    }
    excluded = {
        ".git",
        "target",
        "node_modules",
        "out",
        ".gocache",
        ".secrets",
        "__pycache__",
        ".ruff_cache",
        ".local-tests",
    }
    for tree in trees:
        for directory, names, files in os.walk(ROOT / tree, followlinks=False):
            names[:] = [name for name in names if name not in excluded]
            if any((Path(directory) / name).is_symlink() for name in names):
                raise FixtureError("source directories must not be symlinks")
            for name in files:
                path = Path(directory) / name
                if (
                    name.startswith(".env")
                    or name == ".gitignore"
                    or (
                        path.suffix == ".md"
                        and not path.is_relative_to(ROOT / "config/legal/templates")
                    )
                ):
                    continue
                if not stat.S_ISREG(path.lstat().st_mode):
                    raise FixtureError("source input must be a regular file")
                selected.add(path)
    if any(not stat.S_ISREG(path.lstat().st_mode) for path in selected):
        raise FixtureError("source input must be a regular file")
    return {str(path.relative_to(ROOT)): digest(path) for path in sorted(selected)}


def source_modes():
    paths = set()
    for name in source_inputs():
        path = ROOT / name
        while path != ROOT:
            paths.add(path)
            path = path.parent
    return {
        str(path.relative_to(ROOT)): stat.S_IMODE(path.lstat().st_mode)
        for path in sorted(paths)
    }


class FixtureRun:
    def __init__(self, root, projects, nonce, inputs):
        self.root, self.projects, self.nonce, self.inputs = (
            root,
            projects,
            nonce,
            inputs,
        )
        self.modes = source_modes()
        self.number = 0
        self.phase = "allocation"
        self.configs = {}
        self.images = {}
        self.runtime_hashes = {}
        self.resource_bindings = {}
        self.completed_builds = set()
        self.build_completion_unknown = False
        self.environment = {
            key: value
            for key, value in os.environ.items()
            if not key.startswith(("PG", "DOCKER_", "COMPOSE_", "LMM_"))
            and key not in {"DATABASE_URL", "SQL_DSN"}
        }
        (root / "docker-config").mkdir(mode=0o700)
        self.environment.update(
            DOCKER_CONFIG=str(root / "docker-config"),
            COMPOSE_DISABLE_ENV_FILE="true",
            BUILDKIT_PROGRESS="plain",
        )
        self.record(
            "allocation.json",
            {
                "format": "lmm-rpc-docker-fixture-v1",
                "nonce": nonce,
                "projects": projects,
                "owner_pid": os.getpid(),
                "boot_id": Path("/proc/sys/kernel/random/boot_id").read_text().strip(),
                "docker_endpoint": DOCKER_ENDPOINT,
                "created_unix_ns": time.time_ns(),
                "failure_policy": "stop verified own containers; retain volumes/config/secrets",
                "images_policy": "formal Dockerfile builds; retain images as evidence",
            },
        )
        self.record(
            "source-binding.json",
            {
                "files": inputs,
                "modes": self.modes,
                "script_sha256": digest(Path(__file__)),
            },
        )
        context = root / "build-context"
        context.mkdir(mode=0o700)
        for name, expected in inputs.items():
            destination = context / name
            destination.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            private_write(destination, (ROOT / name).read_bytes())
            destination.chmod(self.modes[name])
            if digest(destination) != expected:
                raise FixtureError("source changed while freezing build context")
        # The private ancestor stays 0700, but Docker COPY must retain original
        # public resource modes (not root-only 0600 legal templates/licenses).
        for name, mode in sorted(
            self.modes.items(), key=lambda item: len(Path(item[0]).parts), reverse=True
        ):
            source = ROOT / name
            if source.is_dir():
                (context / name).chmod(mode)
        self.check_sources()

    def record(self, name, value):
        private_write(
            self.root / name, json.dumps(value, sort_keys=True, indent=2) + "\n"
        )

    def check_sources(self):
        if source_inputs() != self.inputs or source_modes() != self.modes:
            raise FixtureError(
                "source inputs/modes drifted; retain fixture and original build evidence"
            )
        for name, expected in self.inputs.items():
            if digest(self.root / "build-context" / name) != expected:
                raise FixtureError("frozen build context drifted")
        for name, mode in self.modes.items():
            if (
                stat.S_IMODE((self.root / "build-context" / name).lstat().st_mode)
                != mode
            ):
                raise FixtureError("frozen build context mode drifted")

    def command(self, args, *, data=None, env=None, timeout=60, check=True):
        args = list(args)
        if args and args[0] == "docker" and args[1:2] != ["--host"]:
            args[1:1] = ["--host", DOCKER_ENDPOINT]
        self.number += 1
        stem = f"command-{self.number:04}"
        self.record(
            stem + ".json",
            {
                "argv": args,
                "phase": self.phase,
                "timeout_seconds": timeout,
                "stdin_supplied": data is not None,
            },
        )
        started = time.monotonic()
        try:
            result = subprocess.run(
                args,
                cwd=ROOT,
                input=data,
                text=True,
                capture_output=True,
                env=self.environment if env is None else env,
                timeout=timeout,
                check=False,
            )
        except subprocess.TimeoutExpired as error:
            for label, value in (("stdout", error.stdout), ("stderr", error.stderr)):
                private_write(self.root / (stem + "." + label), value or b"")
            if "build" in args:
                self.build_completion_unknown = True
            self.record(
                stem + "-result.json",
                {
                    "status": "UNKNOWN",
                    "reason": "timeout",
                    "seconds": time.monotonic() - started,
                    "daemon_build_completion": "UNKNOWN"
                    if "build" in args
                    else "not-build",
                },
            )
            raise FixtureError(
                "owned command timed out; see private logs; never blindly replay"
            ) from None
        except BaseException:
            if "build" in args:
                self.build_completion_unknown = True
            self.record(
                stem + "-result.json",
                {
                    "status": "UNKNOWN",
                    "reason": "interrupted",
                    "seconds": time.monotonic() - started,
                },
            )
            raise
        private_write(self.root / (stem + ".stdout"), result.stdout or "")
        private_write(self.root / (stem + ".stderr"), result.stderr or "")
        self.record(
            stem + "-result.json",
            {"exit_code": result.returncode, "seconds": time.monotonic() - started},
        )
        if (
            result.returncode == 0
            and len(args) >= 2
            and args[-2] == "build"
            and args[-1] in self.projects
        ):
            self.completed_builds.add(args[-1])
        if check and result.returncode:
            raise subprocess.CalledProcessError(result.returncode, args)
        return result

    def run(self, *args, data=None, env=None, timeout=60):
        return self.command(args, data=data, env=env, timeout=timeout).stdout.strip()

    def absent(self, kind, identity):
        result = self.command(["docker", kind, "inspect", identity], check=False)
        if result.returncode == 0:
            return False
        text = result.stderr.lower()
        # Engine network inspect also uses this distinct grammar. Match the
        # requested identity exactly; another network's error is not absence.
        if kind == "network" and re.fullmatch(
            r"Error response from daemon: network "
            + re.escape(identity)
            + r" not found\s*",
            result.stderr,
        ):
            return True
        if not any(
            message in text
            for message in (
                "no such object",
                "no such image",
                "no such container",
                "no such volume",
                "no such network",
            )
        ):
            raise FixtureError("resource absence is UNKNOWN; retain evidence")
        return True

    def resource_names(self, kind):
        return {
            spec["name"]
            for group in self.configs.values()
            for spec in group.get(kind, {}).values()
            if not spec.get("external")
        }

    def preflight_resources(self):
        planned = {
            kind: sorted(self.resource_names(kind)) for kind in ("volumes", "networks")
        }
        self.record("resource-allocation.json", {"nonce": self.nonce, **planned})
        planned_images = {
            group: spec["services"][group]["image"]
            for group, spec in self.configs.items()
        }
        self.record(
            "image-allocation.json", {"nonce": self.nonce, "images": planned_images}
        )
        for tag in planned_images.values():
            if not self.absent("image", tag):
                raise FixtureError("pre-existing image tag must not be overwritten")
        for kind, names in planned.items():
            for name in names:
                if not self.absent(kind[:-1], name):
                    raise FixtureError("pre-existing resource must not be adopted")
        for project in self.projects.values():
            if self.run(
                "docker",
                "ps",
                "-aq",
                "--filter",
                "label=com.docker.compose.project=" + project,
            ):
                raise FixtureError("pre-existing project must not be adopted")

    def bind_image(self, group, tag):
        if group not in self.completed_builds:
            raise FixtureError(
                "formal Dockerfile build must complete before image binding"
            )
        self.check_sources()
        info = json.loads(self.run("docker", "image", "inspect", tag))[0]
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", info["Id"]):
            raise FixtureError("image identity incomplete")
        if info.get("Config", {}).get("User") != "65532:65532":
            raise FixtureError("built runtime image user is not restricted")
        self.images[group] = info["Id"]
        self.record(
            "image-" + group + ".json",
            {
                "image_id": info["Id"],
                "tag": tag,
                "dockerfile": str(DOCKER / (group + ".Dockerfile")),
                "dockerfile_sha256": self.inputs[
                    "deployment/docker/" + group + ".Dockerfile"
                ],
                "formal_dockerfile_build": True,
                "source_drift": [],
                "repo_digests": info.get("RepoDigests", []),
                "os": info.get("Os"),
                "architecture": info.get("Architecture"),
                "from_references": re.findall(
                    r"^FROM (\S+)",
                    (
                        self.root
                        / "build-context/deployment/docker"
                        / (group + ".Dockerfile")
                    ).read_text(),
                    re.M,
                ),
            },
        )

    def capture_resources(self):
        observed = []
        for kind in ("volume", "network"):
            for name in sorted(self.resource_names(kind + "s")):
                if self.absent(kind, name):
                    continue
                info = json.loads(self.run("docker", kind, "inspect", name))[0]
                labels = info.get("Labels") or {}
                if (
                    info.get("Name") != name
                    or labels.get(LABEL) != self.nonce
                    or labels.get("com.docker.compose.project")
                    not in self.projects.values()
                ):
                    raise FixtureError("allocated volume/network has foreign ownership")
                binding = {
                    "kind": kind,
                    "name": name,
                    "id": info["Id"] if kind == "network" else name,
                    "created": info.get("CreatedAt", info.get("Created")),
                    "driver": info["Driver"],
                    "nonce": self.nonce,
                    "project": labels["com.docker.compose.project"],
                }
                key = kind + ":" + name
                if (
                    key in self.resource_bindings
                    and self.resource_bindings[key] != binding
                ):
                    raise FixtureError("allocated volume/network identity changed")
                self.resource_bindings[key] = binding
                observed.append(binding)
        return observed

    def capture_owned(self, stage, *, runtime=False):
        ids = self.run(
            "docker",
            "ps",
            "-aq",
            "--no-trunc",
            "--filter",
            "label=" + LABEL + "=" + self.nonce,
        ).split()
        observed = []
        for identity in ids:
            info = json.loads(self.run("docker", "inspect", identity))[0]
            labels = info["Config"].get("Labels") or {}
            if (
                info["Id"] != identity
                or labels.get(LABEL) != self.nonce
                or labels.get("com.docker.compose.project")
                not in self.projects.values()
            ):
                raise FixtureError("container ownership changed; do not stop or delete")
            row = {
                "id": identity,
                "project": labels["com.docker.compose.project"],
                "service": labels.get("com.docker.compose.service"),
                "image_id": info["Image"],
                "pid": info["State"]["Pid"],
                "started_at": info["State"]["StartedAt"],
                "running": info["State"]["Running"],
                "restart_count": info["RestartCount"],
            }
            service = row["service"]
            if runtime and row["running"] and service in ("core", "extensions"):
                if info["Image"] != self.images[service]:
                    raise FixtureError("running product does not match built image")
                actual = self.run(
                    "docker",
                    "exec",
                    identity,
                    "sh",
                    "-ec",
                    "cat /proc/1/stat; readlink /proc/1/exe; stat -Lc '%d:%i' /proc/1/exe; sha256sum /proc/1/exe",
                ).splitlines()
                expected_path = (
                    "/usr/bin/lmm-core"
                    if service == "core"
                    else "/usr/bin/lmm-extensions"
                )
                if actual[1] != expected_path:
                    raise FixtureError("actual product executable path changed")
                sha = actual[3].split()[0]
                if (
                    service in self.runtime_hashes
                    and self.runtime_hashes[service] != sha
                ):
                    raise FixtureError("product executable changed across restart")
                self.runtime_hashes[service] = sha
                row.update(
                    executable_sha256=sha,
                    executable_path=actual[1],
                    executable_device_inode=actual[2],
                    startticks=int(actual[0].rsplit(")", 1)[1].split()[19]),
                )
                current = json.loads(self.run("docker", "inspect", identity))[0]
                if (
                    current["State"]["Pid"] != row["pid"]
                    or current["State"]["StartedAt"] != row["started_at"]
                ):
                    raise FixtureError("product identity changed during observation")
            observed.append(row)
        resources = self.capture_resources() if stage != "before-cleanup" else []
        self.record(
            f"owned-{self.number:04}-{stage}.json",
            {"containers": observed, "resources": resources},
        )
        return observed

    def finish(self, success, error=None):
        if success:
            self.check_sources()
        before = self.capture_owned("before-cleanup")
        cleanup_errors = []
        for row in before:
            try:
                self.command(
                    ["docker", "logs", "--tail", "1000", row["id"]], check=False
                )
                # Recheck exact immutable identity immediately before mutation.
                info = json.loads(self.run("docker", "inspect", row["id"]))[0]
                labels = info["Config"].get("Labels") or {}
                if (
                    info["Id"] != row["id"]
                    or labels.get(LABEL) != self.nonce
                    or labels.get("com.docker.compose.project") != row["project"]
                ):
                    raise FixtureError("resource ownership changed before stop")
                self.run("docker", "stop", "--time", "8", row["id"], timeout=20)
                stopped = json.loads(self.run("docker", "inspect", row["id"]))[0]
                if stopped["State"]["Running"]:
                    raise FixtureError("owned container did not stop")
                if success:
                    self.run("docker", "rm", row["id"])
                    if not self.absent("container", row["id"]):
                        raise FixtureError("owned container still exists")
            except BaseException as failure:
                cleanup_errors.append(type(failure).__name__)
        if success and not cleanup_errors:
            try:
                self.capture_resources()
            except BaseException as failure:
                cleanup_errors.append(type(failure).__name__)
        if success and not cleanup_errors:
            for kind in ("volume", "network"):
                for name in sorted(self.resource_names(kind + "s")):
                    try:
                        info = json.loads(self.run("docker", kind, "inspect", name))[0]
                        labels = info.get("Labels") or {}
                        if (
                            labels.get(LABEL) != self.nonce
                            or labels.get("com.docker.compose.project")
                            not in self.projects.values()
                        ):
                            raise FixtureError("volume/network ownership changed")
                        identity = info["Id"] if kind == "network" else info["Name"]
                        binding = self.resource_bindings[kind + ":" + name]
                        if (
                            identity != binding["id"]
                            or info.get("Name") != binding["name"]
                            or info.get("CreatedAt", info.get("Created"))
                            != binding["created"]
                            or info["Driver"] != binding["driver"]
                        ):
                            raise FixtureError(
                                "volume/network identity changed before removal"
                            )
                        self.run("docker", kind, "rm", identity)
                        if not self.absent(kind, identity):
                            raise FixtureError("owned resource still exists")
                    except BaseException as failure:
                        cleanup_errors.append(type(failure).__name__)
        if success and not cleanup_errors:
            if self.run(
                "docker", "ps", "-aq", "--filter", "label=" + LABEL + "=" + self.nonce
            ):
                cleanup_errors.append("UnexpectedOwnedContainerRemaining")
        passed = success and not cleanup_errors
        self.record(
            "result.json",
            {
                "result": "PASS" if passed else "FAIL",
                "nonce": self.nonce,
                "business_assertions_passed": success,
                "cleanup_errors": cleanup_errors,
                "owned_runtime_resources_removed": passed,
                "failure_resources_retained": not passed,
                "images_retained_for_evidence": self.images,
                "runtime_executable_sha256": self.runtime_hashes,
                "daemon_build_completion": "UNKNOWN"
                if self.build_completion_unknown
                else "observed",
                "error_type": type(error).__name__ if error else None,
                "private_logs": "command-*.stdout / command-*.stderr",
                "secrets": "private retained fixture files, never inline in this receipt",
            },
        )
        if cleanup_errors:
            raise FixtureError("owned cleanup incomplete; preserve fixture")
        return passed


# @mark deployment.database-authority role=verification
def main(argv=None):
    global ACTIVE
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-root", required=True, type=Path)
    args = parser.parse_args(argv)
    root = prepare_output_root(args.output_root)
    suffix = secrets.token_hex(12)
    projects = {
        "core": "lmm-fresh-core-" + suffix,
        "extensions": "lmm-fresh-ext-" + suffix,
    }
    context = None
    error = None
    success = False
    # SIGTERM has the same retention path as Ctrl-C; never destroy a failed DB.
    old_handler = signal.signal(
        signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt())
    )
    try:
        inputs = source_inputs()
        context = FixtureRun(root, projects, suffix, inputs)
        ACTIVE = context
        execute(root, projects, context)
        success = True
    except BaseException as failure:
        error = failure
        # Private diagnostics contain no stdin; no exception text/argv is printed.
        private_write(
            root / "failure.json",
            json.dumps(
                {
                    "result": "FAIL",
                    "error_type": type(failure).__name__,
                    "phase": context.phase if context else "allocation",
                    "resources_retained": True,
                }
            )
            + "\n",
        )
    finally:
        try:
            if context is not None:
                success = context.finish(success, error)
        except BaseException as failure:
            success = False
            private_write(
                root / "cleanup-failure.json",
                json.dumps(
                    {
                        "error_type": type(failure).__name__,
                        "resources_retained": True,
                        "cleanup_status": "UNKNOWN",
                    }
                )
                + "\n",
            )
            if not (root / "result.json").exists():
                private_write(
                    root / "result.json",
                    json.dumps(
                        {
                            "result": "FAIL",
                            "error_type": type(failure).__name__,
                            "resources_retained": True,
                            "cleanup_status": "UNKNOWN",
                        }
                    )
                    + "\n",
                )
        ACTIVE = None
        signal.signal(signal.SIGTERM, old_handler)
    if not (root / "result.json").exists():
        private_write(
            root / "result.json",
            json.dumps(
                {
                    "result": "FAIL",
                    "error_type": type(error).__name__,
                    "resources_retained": True,
                    "cleanup_status": "not-created-or-UNKNOWN",
                }
            )
            + "\n",
        )
    print(
        "PASS: owned Docker installation assertions and cleanup verified"
        if success
        else "FAIL: private evidence and recoverable resources retained at "
        + str(root),
        flush=True,
    )
    return 0 if success else 1


def execute(temp, projects, context):
    password, runtime_password, rpc_token, ext_token = (
        secrets.token_hex(32) for _ in range(4)
    )
    values = {
        "database-password": password,
        "database-owner-url": f"postgres://lmm_core_admin:{password}@core-db:5432/lmm_core?options=-c%20role%3Dlmm_core_owner",
        "database-url": f"postgres://lmm_core_runtime:{runtime_password}@core-db:5432/lmm_core",
        "rpc-token": rpc_token,
        "extension-token": ext_token,
    }
    write_fixture_secrets(temp, values)
    env = dict(
        context.environment,
        COMPOSE_DISABLE_ENV_FILE="true",
        LMM_DOCKER_DATABASE_URL_SECRET_FILE=str(temp / "database-url"),
        LMM_DOCKER_DATABASE_OWNER_URL_SECRET_FILE=str(temp / "database-owner-url"),
        LMM_DOCKER_DATABASE_PASSWORD_SECRET_FILE=str(temp / "database-password"),
        LMM_DOCKER_RPC_TOKEN_SECRET_FILE=str(temp / "rpc-token"),
        LMM_DOCKER_EXTENSIONS_TOKEN_SECRET_FILE=str(temp / "extension-token"),
        LMM_DOCKER_CORE_CONFIG_FILE=str(DOCKER / "config/core-identity-rpc.toml"),
        LMM_DOCKER_INIT_CONFIG_FILE=str(DOCKER / "config/core-init.toml"),
        LMM_DOCKER_EXTENSIONS_CONFIG_FILE=str(
            DOCKER / "config/extensions-identity.toml"
        ),
        LMM_DOCKER_NETWORK=projects["core"] + "-runtime",
        LMM_DOCKER_RPC_VOLUME=projects["core"] + "-rpc",
    )
    configs = {}
    for group, files in (
        ("core", ["core", "identity", "rpc-core"]),
        ("extensions", ["extensions", "rpc-extensions"]),
    ):
        args = ["docker", "compose", "-p", projects[group]]
        for name in files:
            args.extend(["-f", str(DOCKER / ("compose." + name + ".yml"))])
        data = json.loads(
            run(*args, "--profile", "tools", "config", "--format", "json", env=env)
        )
        data["name"] = projects[group]
        data["networks"]["core"]["name"] = projects["core"] + "-runtime"
        for service in data["services"].values():
            service["restart"] = "no"
            service["stop_grace_period"] = "8s"
        service = data["services"][group]
        service["image"] = projects[group] + ":test"
        service["ports"] = [
            {
                "target": 8080 if group == "core" else 8081,
                "published": "0",
                "host_ip": "127.0.0.1",
                "protocol": "tcp",
            }
        ]
        for spec in data["services"].values():
            spec.setdefault("labels", {})[LABEL] = context.nonce
        service["build"]["context"] = str(context.root / "build-context")
        for spec in data.get("configs", {}).values():
            original = Path(spec["file"]).relative_to(ROOT)
            spec["file"] = str(context.root / "build-context" / original)
        for spec in data.get("networks", {}).values():
            if not spec.get("external"):
                spec.setdefault("labels", {})[LABEL] = context.nonce
        for spec in data.get("volumes", {}).values():
            if not spec.get("external"):
                spec.setdefault("labels", {})[LABEL] = context.nonce
        configs[group] = data
    core, ext = configs["core"], configs["extensions"]
    assert set(core["services"]) == {"core", "core-db", "core-init"}
    assert set(ext["services"]) == {"extensions"}
    assert not core["services"]["core-db"].get("ports")
    assert core["networks"]["identity"]["internal"]
    assert "identity" not in ext["networks"] and ext["volumes"]["core-rpc"]["external"]
    mount = next(
        v
        for v in ext["services"]["extensions"]["volumes"]
        if v["target"] == "/run/lmm-core-rpc"
    )
    assert mount["read_only"]
    core["services"]["core-init"]["image"] = core["services"]["core"]["image"]
    core["networks"]["identity"]["name"] = projects["core"] + "-database"
    core["volumes"]["core-database-data"]["name"] = projects["core"] + "-data"
    runtime_secrets = {value["source"] for value in core["services"]["core"]["secrets"]}
    install_secrets = {
        value["source"] for value in core["services"]["core-init"]["secrets"]
    }
    assert "core_database_owner_url" not in runtime_secrets
    assert "core_database_password" not in runtime_secrets
    assert install_secrets == {"core_database_owner_url"}
    ext["secrets"]["extension_token"]["file"] = str(temp / "extension-token")
    validate_file_configuration(core, "core", "core")
    validate_file_configuration(core, "core-init", "core", init=True)
    validate_file_configuration(ext, "extensions", "extensions")
    for group, data in configs.items():
        context.record(group + ".json", data)

    context.configs = configs
    context.preflight_resources()
    context.record(
        "rendered-inputs.json",
        {group: digest(temp / (group + ".json")) for group in configs},
    )

    def compose(group, *args, data=None):
        return run(
            "docker",
            "compose",
            "-f",
            str(temp / (group + ".json")),
            *args,
            data=data,
            timeout=1800 if "build" in args else 180 if "--wait" in args else 60,
        )

    def psql(sql, *variables):
        args = [
            "exec",
            "-T",
            "core-db",
            "psql",
            "-X",
            "-q",
            "-At",
            "-U",
            "lmm_core_admin",
            "-d",
            "lmm_core",
            "-v",
            "ON_ERROR_STOP=1",
        ]
        for variable in variables:
            args.extend(["-v", variable])
        return compose("core", *args, data=sql)

    def reject_runtime(owner=False):
        rejected = copy.deepcopy(core)
        if owner:
            # Select a separate owner TOML; never restore the removed env path.
            rejected["configs"]["core_config"]["file"] = core["configs"][
                "core_init_config"
            ]["file"]
            rejected["services"]["core"]["secrets"] = [
                {"source": "core_database_owner_url"}
            ]
        config_path = temp / ("reject-owner.json" if owner else "reject-missing.json")
        config_path.write_text(json.dumps(rejected))
        name = projects["core"] + ("-owner-rejected" if owner else "-missing-rejected")
        result = context.command(
            [
                "docker",
                "compose",
                "-f",
                str(config_path),
                "run",
                "--rm",
                "-T",
                "--no-deps",
                "--name",
                name,
                "core",
            ],
            timeout=30,
            check=False,
        )
        expected = (
            ("UnsafePrivileges", "core runtime database authority is unsafe")
            if owner
            else ("MissingPrivileges", "core runtime database authority is incomplete")
        )
        assert result.returncode != 0
        assert any(message in result.stderr for message in expected)

    def endpoint(group):
        return "http://" + compose(
            group, "port", group, "8080" if group == "core" else "8081"
        )

    def core_call(path, token=None, method="GET", body=None):
        return request(endpoint("core") + path, token, method, body)

    def rpc(path, user=None, token=ext_token):
        return request(
            endpoint("extensions") + "/extensions/v1/identity/" + path, token, user=user
        )

    def wait(call):
        for _ in range(100):
            try:
                status, body = call()
                if status == 200:
                    context.capture_owned("ready", runtime=True)
                return json.loads(body)
            except (OSError, TimeoutError):
                pass
            time.sleep(0.3)
        raise AssertionError("test endpoint did not become available")

    def snapshot(service):
        data = json.loads(
            run("docker", "inspect", compose("core", "ps", "-q", service))
        )[0]
        return data["Id"], data["State"]["StartedAt"], data["RestartCount"]

    def rewrite_rpc_token(value):
        # Rewrite the same inode: running Compose secret bind mounts must
        # observe the value, and it must stay private during both rotations.
        fixture_secrets(
            core["services"]["core"]["image"],
            temp,
            ("rpc-token",),
            ROTATE_SECRET,
            data=value,
        )

    context.phase = "formal-core-image-build"
    compose("core", "build", "core")
    context.bind_image("core", core["services"]["core"]["image"])
    image = core["services"]["core"]["image"]
    fixture_secrets(image, temp, SECRET_NAMES, PREPARE_SECRETS, prepare=True)
    fixture_secrets(image, temp, SECRET_NAMES, VERIFY_SECRETS)
    compose("core", "up", "-d", "--wait", "core-db")
    psql((ROOT / "deployment/postgres/create-core-roles.sql").read_text())
    # Synthetic password travels on stdin, never in a process argument.
    psql(f"ALTER ROLE lmm_core_runtime PASSWORD '{runtime_password}';")
    compose(
        "core",
        "run",
        "--rm",
        "-T",
        "--no-deps",
        "--entrypoint",
        "/bin/sh",
        "core-init",
        "-ec",
        VERIFY_SECRETS,
        "fixture-secrets",
        "/run/secrets/core_database_owner_url",
    )
    compose(
        "core",
        "run",
        "--rm",
        "-T",
        "core-init",
        "--config",
        "/etc/lmm.conf.d/core.toml",
        "init-db",
    )
    compiled = json.loads(
        compose(
            "core", "run", "--rm", "-T", "--no-deps", "core-init", "schema-manifest"
        )
    )
    assert compiled["format_version"] == 1
    assert compiled["scope"] == "compiled-core-schema-contract"
    compiled_names = [item["name"] for item in compiled["components"]]
    assert len(compiled_names) == len(set(compiled_names))
    components = json.loads(
        psql("SELECT json_agg(name ORDER BY name) FROM core_meta.component_contract;")
    )
    assert len(components) == len(set(components))
    assert set(components) == {
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
    }
    assert set(compiled_names) == set(components)
    assert (
        psql(
            "SELECT bool_and(n.nspowner='lmm_core_owner'::regrole) FROM pg_namespace n WHERE n.nspname LIKE 'core\\_%' ESCAPE '\\';"
        ).strip()
        == "t"
    )
    assert (
        psql(
            "SELECT NOT EXISTS (\n                SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace\n                WHERE (n.nspname LIKE 'core\\_%' ESCAPE '\\' OR n.nspname='public')\n                  AND c.relowner<>'lmm_core_owner'::regrole\n                UNION ALL\n                SELECT FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace\n                WHERE (n.nspname LIKE 'core\\_%' ESCAPE '\\' OR n.nspname='public')\n                  AND p.proowner<>'lmm_core_owner'::regrole);\n                "
        ).strip()
        == "t"
    )
    assert (
        psql(
            "SELECT NOT rolcanlogin FROM pg_roles WHERE rolname='lmm_core_owner';"
        ).strip()
        == "t"
    )
    assert (
        psql(
            "SELECT NOT EXISTS(SELECT FROM pg_auth_members WHERE member='lmm_core_runtime'::regrole);"
        ).strip()
        == "t"
    )
    try:
        compose(
            "core",
            "run",
            "--rm",
            "-T",
            "core-init",
            "--config",
            "/etc/lmm.conf.d/core.toml",
            "init-db",
        )
    except subprocess.CalledProcessError:
        pass
    else:
        raise AssertionError("second initialization must reject the existing database")
    user_id = 9007199254740993
    session = json.loads(
        compose(
            "core",
            "run",
            "--rm",
            "-T",
            "core-init",
            "--config",
            "/etc/lmm.conf.d/core.toml",
            "bootstrap-user",
            data=json.dumps({"user_id": user_id, "platform_level": 1}),
        )
    )
    reject_runtime(owner=True)
    # Namespace lookup is allowed, but table/function rights remain absent.
    psql(
        "GRANT USAGE ON SCHEMA core_meta,core_identity,core_billing TO lmm_core_runtime;"
    )
    reject_runtime()
    psql(
        (ROOT / "deployment/postgres/grant-core-runtime.sql").read_text(),
        "enable_events=true",
    )
    assert psql(RUNTIME_PRIVILEGES_SQL).strip() == "t"
    compose("core", "up", "-d", "core")
    wait(lambda: core_call("/health/live"))
    assert core_call("/health/ready")[0] == 200
    assert core_call("/health/model-ready")[0] == 503
    status, body = core_call("/core/v1/teams", session["secret"], "POST")
    assert status == 201
    team_id = json.loads(body)["id"]
    status, body = core_call(
        "/core/v1/keys",
        session["secret"],
        "POST",
        {"owner": {"kind": "team", "id": team_id}, "ttl_seconds": 3600},
    )
    assert status == 201
    key = json.loads(body)
    context.phase = "formal-extension-image-build"
    compose("extensions", "build", "extensions")
    context.bind_image("extensions", ext["services"]["extensions"]["image"])
    compose("extensions", "up", "-d", "--no-build", "extensions")
    capabilities = wait(lambda: rpc("capabilities"))
    assert capabilities["protocol_major"] == 1 and capabilities["identity_available"]
    actor = wait(lambda: rpc("self", key["secret"]))
    assert actor["user_id"] == str(user_id)
    assert actor["owner"] == {"kind": "ACCOUNT_KIND_TEAM", "id": str(team_id)}
    assert actor["funding_accounts"] == [actor["owner"]]
    teams = wait(lambda: rpc("teams", session["secret"]))["teams"]
    assert teams[0]["id"] == str(team_id) and teams[0]["role"] == "TEAM_ROLE_OWNER"
    assert rpc("teams", key["secret"])[0] == 403
    assert rpc("self")[0] == 401
    assert rpc("self", key["secret"], token="wrong")[0] == 401
    compose(
        "extensions",
        "exec",
        "-T",
        "extensions",
        "sh",
        "-c",
        "if touch /run/lmm-core-rpc/forbidden 2>/dev/null; then exit 1; fi",
    )
    print(
        "CHECK: fresh installation, repeat-init rejection, native teams, protobuf and separate credentials",
        flush=True,
    )
    before = {service: snapshot(service) for service in ("core", "core-db")}
    run("docker", "kill", compose("extensions", "ps", "-q", "extensions"))
    assert core_call("/core/v1/identity", key["secret"])[0] == 200
    compose("extensions", "start", "extensions")
    wait(lambda: rpc("self", key["secret"]))
    compose("extensions", "up", "-d", "--no-deps", "--force-recreate", "extensions")
    wait(lambda: rpc("self", key["secret"]))
    rewrite_rpc_token(secrets.token_hex(32))
    compose("extensions", "up", "-d", "--no-deps", "--force-recreate", "extensions")
    wait(lambda: request(endpoint("extensions") + "/health/live"))
    assert rpc("capabilities")[0] == 401
    assert core_call("/core/v1/identity", key["secret"])[0] == 200
    rewrite_rpc_token(rpc_token)
    compose("extensions", "up", "-d", "--no-deps", "--force-recreate", "extensions")
    wait(lambda: rpc("self", key["secret"]))
    assert all(snapshot(service) == value for service, value in before.items())
    print(
        "CHECK: extension restart, replacement and wrong service token do not restart core or database",
        flush=True,
    )
    ext_id = compose("extensions", "ps", "-q", "extensions")
    compose("core", "restart", "core")
    wait(lambda: rpc("self", key["secret"]))
    assert compose("extensions", "ps", "-q", "extensions") == ext_id
    compose("core", "stop", "core-db")
    started = time.monotonic()
    assert rpc("self", key["secret"])[0] in (503, 504)
    assert time.monotonic() - started < 4
    assert core_call("/health/live")[0] == 200
    assert request(endpoint("extensions") + "/health/live")[0] == 200
    compose("core", "start", "core-db")
    wait(lambda: rpc("self", key["secret"]))
    assert (
        core_call(f"/core/v1/credentials/{key['id']}", session["secret"], "DELETE")[0]
        == 204
    )
    assert rpc("self", key["secret"])[0] == 401
    assert core_call("/health/ready")[0] == 200
    assert core_call("/v1/models", key["secret"])[0] == 503
    print(
        "CHECK: reconnect, bounded database failure, persistent keys, immediate revocation and closed model routes",
        flush=True,
    )
    context.record(
        "business-checks.json",
        {
            "result": "PASS",
            "schema_components": components,
            "compiled_schema_manifest": compiled,
            "runtime_privileges_checked": True,
            "owner_and_incomplete_runtime_rejected": True,
            "rpc_and_restarts": True,
            "token_rotation_and_database_outage": True,
            "revocation_and_closed_models": True,
        },
    )


if __name__ == "__main__":
    raise SystemExit(main())
