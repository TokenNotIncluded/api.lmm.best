#!/usr/bin/env python3
"""Local-only DP-28 disk lifecycle experiment. No Docker or database writes.

Linux / Python >= 3.11. Mutations require a private directory on a tmpfs
mounted with source name lmm-dp28-lab. This is NOT a production deployer.
"""
from __future__ import annotations

import argparse
import contextlib
import dataclasses
import errno
import fcntl
import hashlib
import json
import math
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tarfile
import time
from typing import Any, Callable, Iterator

PROJECT = "api.lmm.best"
SCOPE = "dp28-local-only"
MARKER = ".lmm-dp28.json"
NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,95}\Z")
DIGEST = re.compile(r"[0-9a-f]{64}\Z")
DIRECTORIES = (
    "control", "objects", "releases", "staging", "logs", "cache",
    "protected", "protected/database", "protected/wal", "protected/audit",
    "protected/dedup", "protected/event_payloads", "backups",
)
MAX_JSON = 2 * 1024 * 1024
NOFOLLOW = os.O_NOFOLLOW | os.O_CLOEXEC


class Refused(RuntimeError):
    """A safety condition was not met. No successful release is implied."""


def encoded(value: Any) -> bytes:
    return (json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True) + "\n").encode()


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def parts(path: str) -> list[str]:
    result = path.split("/")
    if not result or any(not NAME.fullmatch(p) or p in (".", "..") for p in result):
        # Private control names start with a dot, but arbitrary caller paths do not.
        if path not in (MARKER,):
            raise Refused("unsafe relative path")
    return result


def read_json_file(path: Path) -> Any:
    fd = os.open(path, os.O_RDONLY | NOFOLLOW)
    with os.fdopen(fd, "rb") as stream:
        if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode):
            raise Refused("JSON input is not a regular file")
        content = stream.read(MAX_JSON + 1)
    if len(content) > MAX_JSON:
        raise Refused("JSON input exceeds its limit")
    return json.loads(content)


def mount_id(fd: int) -> str:
    with open(f"/proc/self/fdinfo/{fd}", encoding="ascii") as stream:
        for line in stream:
            if line.startswith("mnt_id:"):
                return line.split()[1]
    raise Refused("mount identity unavailable")


def private_root(path: Path) -> int:
    if not path.is_absolute() or ".." in path.parts:
        raise Refused("root must be an absolute non-traversing path")
    fd = os.open("/", os.O_RDONLY | os.O_DIRECTORY | NOFOLLOW)
    try:
        for item in path.parts[1:]:
            nxt = os.open(item, os.O_RDONLY | os.O_DIRECTORY | NOFOLLOW, dir_fd=fd)
            os.close(fd)
            fd = nxt
        info = os.fstat(fd)
        if info.st_uid != os.geteuid() or info.st_mode & 0o077:
            raise Refused("root must be owned by the caller and mode 0700")
        mid = mount_id(fd)
        with open("/proc/self/mountinfo", encoding="utf-8") as stream:
            mounts = [line.split() for line in stream]
        rows = [row for row in mounts if row[0] == mid]
        if len(rows) != 1:
            raise Refused("cannot identify the local lab filesystem")
        tail = rows[0][rows[0].index("-") + 1:]
        if tail[:2] != ["tmpfs", "lmm-dp28-lab"]:
            raise Refused("writes require a dedicated lmm-dp28-lab tmpfs")
        return fd
    except BaseException:
        os.close(fd)
        raise


@dataclasses.dataclass(frozen=True)
class Policy:
    # Operational reserves, NOT assumptions about the host's disk capacity.
    recovery_bytes: int
    recovery_inodes: int
    growth_bytes: int
    growth_inodes: int
    max_draining_versions: int = 2
    max_artifact_bytes: int = 64 * 1024 * 1024
    max_artifact_files: int = 128
    diagnostic_file_bytes: int = 4096
    diagnostic_files: int = 3
    diagnostic_keep_seconds: int = 86400

    def validate(self) -> None:
        for field in dataclasses.fields(self):
            value = getattr(self, field.name)
            if type(value) is not int or value < 0:
                raise Refused("policy values must be nonnegative integers")
        if self.diagnostic_files < 1 or self.diagnostic_file_bytes < 512:
            raise Refused("diagnostic capacity is invalid")
        if self.max_artifact_files < 1 or self.max_artifact_bytes < 1:
            raise Refused("artifact bounds are invalid")


class Store:
    def __init__(self, root: Path, hook: Callable[[str], None] | None = None):
        self.root = root
        self.fd = private_root(root)
        self.mid = mount_id(self.fd)
        self.hook = hook or (lambda _: None)
        try:
            self.marker = self.read_json(MARKER)
            if self.marker.get("project") != PROJECT or self.marker.get("scope") != SCOPE:
                raise Refused("root has no matching project marker")
            if self.marker.get("format") != 1:
                raise Refused("unsupported root format")
            self.policy = Policy(**self.marker["policy"])
            self.policy.validate()
        except BaseException:
            os.close(self.fd)
            self.fd = -1
            raise

    def close(self) -> None:
        if self.fd >= 0:
            os.close(self.fd)
            self.fd = -1

    def __enter__(self) -> Store:
        return self

    def __exit__(self, *_: Any) -> None:
        self.close()

    @classmethod
    def initialize(cls, root: Path, policy: Policy) -> None:
        policy.validate()
        fd = private_root(root)
        try:
            if os.listdir(fd):
                raise Refused("initialization requires an empty private lab directory")
            marker = {"format": 1, "project": PROJECT, "scope": SCOPE,
                      "nonce": os.urandom(16).hex(), "policy": dataclasses.asdict(policy)}
            out = os.open(MARKER, os.O_WRONLY | os.O_CREAT | os.O_EXCL | NOFOLLOW, 0o600, dir_fd=fd)
            with os.fdopen(out, "wb") as stream:
                stream.write(encoded(marker))
                stream.flush()
                os.fsync(stream.fileno())
            for name in DIRECTORIES:
                os.mkdir(root / name, 0o700)
            out = os.open("control/lock", os.O_WRONLY | os.O_CREAT | os.O_EXCL | NOFOLLOW, 0o600, dir_fd=fd)
            os.close(out)
            os.fsync(fd)
        finally:
            os.close(fd)
        with cls(root) as store, store.lock():
            store.atomic_json("control/state.json", {
                "generation": 0, "current": None, "previous_verified": None, "pins": {},
            })
            store.atomic_json("control/gc.json", {"status": "idle"})
            store.atomic_json("control/install.json", {"status": "idle"})

    @contextlib.contextmanager
    def directory(self, path: str = "") -> Iterator[int]:
        fd = os.dup(self.fd)
        try:
            for item in parts(path) if path else []:
                nxt = os.open(item, os.O_RDONLY | os.O_DIRECTORY | NOFOLLOW, dir_fd=fd)
                os.close(fd)
                fd = nxt
                if mount_id(fd) != self.mid:
                    raise Refused("nested or foreign mount refused")
            yield fd
        finally:
            os.close(fd)

    @contextlib.contextmanager
    def parent(self, path: str) -> Iterator[tuple[int, str]]:
        segments = parts(path)
        with self.directory("/".join(segments[:-1])) as fd:
            yield fd, segments[-1]

    def open(self, path: str, flags: int = os.O_RDONLY, mode: int = 0o600) -> int:
        with self.parent(path) as (parent, name):
            fd = os.open(name, flags | NOFOLLOW, mode, dir_fd=parent)
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or mount_id(fd) != self.mid:
            os.close(fd)
            raise Refused("expected a regular file on the lab filesystem")
        return fd

    def info(self, path: str) -> os.stat_result:
        with self.parent(path) as (parent, name):
            return os.stat(name, dir_fd=parent, follow_symlinks=False)

    def exists(self, path: str) -> bool:
        try:
            self.info(path)
            return True
        except FileNotFoundError:
            return False

    def read(self, path: str, limit: int = MAX_JSON) -> bytes:
        with os.fdopen(self.open(path), "rb") as stream:
            value = stream.read(limit + 1)
        if len(value) > limit:
            raise Refused("file exceeds its read limit")
        return value

    def read_json(self, path: str) -> Any:
        return json.loads(self.read(path))

    def file_digest(self, path: str) -> str:
        result = hashlib.sha256()
        with os.fdopen(self.open(path), "rb") as stream:
            while chunk := stream.read(128 * 1024):
                result.update(chunk)
        return result.hexdigest()

    def atomic_json(self, path: str, value: Any) -> None:
        content = encoded(value)
        if len(content) > MAX_JSON:
            raise Refused("control record exceeds its bound")
        with self.parent(path) as (parent, name):
            temp = name + ".next"
            for item in (name, temp):
                try:
                    info = os.stat(item, dir_fd=parent, follow_symlinks=False)
                    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
                        raise Refused("unsafe control file")
                except FileNotFoundError:
                    pass
            try:
                os.unlink(temp, dir_fd=parent)
            except FileNotFoundError:
                pass
            fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | NOFOLLOW, 0o600, dir_fd=parent)
            try:
                with os.fdopen(fd, "wb") as stream:
                    stream.write(content)
                    stream.flush()
                    os.fsync(stream.fileno())
                self.hook("allocated:atomic-json:" + path)
                os.replace(temp, name, src_dir_fd=parent, dst_dir_fd=parent)
                os.fsync(parent)
            except BaseException:
                # The previous complete record remains authoritative. A .next
                # file is not recovery evidence and is cleaned only under lock.
                raise

    @contextlib.contextmanager
    def lock(self) -> Iterator[None]:
        fd = self.open("control/lock", os.O_RDWR)
        try:
            if os.fstat(fd).st_nlink != 1:
                raise Refused("unsafe lock file")
            try:
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError as exc:
                raise Refused("busy: another local release or cleanup owns the lock") from exc
            yield
        finally:
            os.close(fd)

    def state(self) -> dict[str, Any]:
        value = self.read_json("control/state.json")
        if type(value.get("generation")) is not int or not isinstance(value.get("pins"), dict):
            raise Refused("invalid release state")
        return value

    def require_no_gc(self) -> None:
        if self.read_json("control/gc.json").get("status") == "pending":
            raise Refused("an interrupted cleanup must be resumed first")

    def manifest(self, release: str) -> dict[str, Any]:
        if not NAME.fullmatch(release):
            raise Refused("invalid release identifier")
        record = self.read_json(f"releases/{release}/release.json")
        if record.get("nonce") != self.marker["nonce"] or record.get("project") != PROJECT:
            raise Refused("unowned release")
        if record.get("manifest", {}).get("release") != release:
            raise Refused("release identity mismatch")
        return record

    def verify_release(self, release: str) -> dict[str, Any]:
        record = self.manifest(release)
        if record.get("verification") != "native-fixture-probe":
            raise Refused("release has no supported verification receipt")
        for item in record["manifest"]["files"]:
            path = f"releases/{release}/{item['name']}"
            if self.info(path).st_size != item["bytes"] or self.file_digest(path) != item["sha256"]:
                raise Refused("retained release failed its checksum")
        return record

    @contextlib.contextmanager
    def lease(self, release: str) -> Iterator[int]:
        # Registration and lease opening share the same lock as GC. The fd can
        # be inherited by a child and must remain open through final settlement.
        with self.lock():
            self.require_no_gc()
            self.verify_release(release)
            fd = self.open(f"releases/{release}/lease", os.O_RDWR)
            fcntl.flock(fd, fcntl.LOCK_SH)
        try:
            yield fd
        finally:
            os.close(fd)

    def pin(self, release: str, reason: str | None) -> None:
        with self.lock():
            self.require_no_gc()
            self.verify_release(release)
            state = self.state()
            if reason is None:
                state["pins"].pop(release, None)
            else:
                if reason not in ("inflight", "incident", "controller-unknown"):
                    raise Refused("unsupported pin reason")
                state["pins"][release] = reason
            state["generation"] += 1
            self.atomic_json("control/state.json", state)

    def release_roots(self, state: dict[str, Any]) -> dict[str, list[str]]:
        roots: dict[str, list[str]] = {}
        for role in ("current", "previous_verified"):
            if state.get(role):
                self.verify_release(state[role])
                roots.setdefault(state[role], []).append(role)
        for release, reason in state["pins"].items():
            self.verify_release(release)
            roots.setdefault(release, []).append("pin:" + reason)
        return roots

    def load_artifact(self, archive: Path, manifest_path: Path, expected_manifest: str) -> dict[str, Any]:
        if not DIGEST.fullmatch(expected_manifest):
            raise Refused("a pinned manifest SHA-256 is required")
        fd = os.open(manifest_path, os.O_RDONLY | NOFOLLOW)
        with os.fdopen(fd, "rb") as stream:
            raw = stream.read(MAX_JSON + 1)
        if len(raw) > MAX_JSON or digest(raw) != expected_manifest:
            raise Refused("manifest checksum mismatch")
        manifest = json.loads(raw)
        if manifest.get("project") != PROJECT or manifest.get("kind") not in {"dp28-native-fixture", "lmm-release-package-v1"}:
            raise Refused("unsupported measured package format")
        if not NAME.fullmatch(manifest.get("release", "")):
            raise Refused("invalid release identifier")
        files = manifest.get("files")
        if not isinstance(files, list) or not 1 <= len(files) <= self.policy.max_artifact_files:
            raise Refused("invalid file count")
        names = set()
        for item in files:
            name = item.get("name", "")
            if not NAME.fullmatch(name) or name in {"lease", "release.json"} or name in names:
                raise Refused("invalid or duplicate archive filename")
            names.add(name)
            if type(item.get("bytes")) is not int or item["bytes"] < 0:
                raise Refused("invalid expanded size")
            if not DIGEST.fullmatch(item.get("sha256", "")) or item.get("mode") not in (0o444, 0o555):
                raise Refused("invalid file digest or mode")
        if manifest.get("kind") == "dp28-native-fixture" and names != {"probe", "payload.bin", "version.txt"}:
            raise Refused("unexpected fixture layout")
        if sum(item["bytes"] for item in files) > self.policy.max_artifact_bytes:
            raise Refused("expanded artifact exceeds policy")
        with os.fdopen(os.open(archive, os.O_RDONLY | NOFOLLOW), "rb") as stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode) or info.st_size != manifest.get("archive_bytes"):
                raise Refused("archive size mismatch")
            if info.st_size > self.policy.max_artifact_bytes:
                raise Refused("compressed artifact exceeds policy")
            sha = hashlib.file_digest(stream, "sha256").hexdigest()
            if sha != manifest.get("archive_sha256"):
                raise Refused("archive checksum mismatch")
            stream.seek(0)
            expected = {item["name"]: item for item in files}
            seen = set()
            with tarfile.open(fileobj=stream, mode="r|gz") as tar:
                for member in tar:
                    if not member.isfile() or member.name not in expected or member.name in seen:
                        raise Refused("archive has an unsafe, unknown or duplicate member")
                    if member.size != expected[member.name]["bytes"]:
                        raise Refused("archive expanded size mismatch")
                    seen.add(member.name)
            if seen != names:
                raise Refused("archive members are incomplete")
        return manifest

    @staticmethod
    def object_key(item: dict[str, Any]) -> str:
        return item["sha256"] + "-" + format(item["mode"], "o")

    def _preflight(self, manifest: dict[str, Any]) -> dict[str, Any]:
        self.require_no_gc()
        state = self.state()
        roots = self.release_roots(state)
        if self.read_json("control/install.json").get("status") == "pending":
            raise Refused("recover the pending install journal before a new release")
        with self.directory("staging") as stage:
            if os.listdir(stage):
                raise Refused("cleanup or recover the existing staging transaction first")
        with self.directory("releases") as releases:
            for release in os.listdir(releases):
                if not NAME.fullmatch(release):
                    raise Refused("unexpected release directory")
                try:
                    fd = self.open(f"releases/{release}/lease", os.O_RDWR)
                except OSError as exc:
                    raise Refused("cannot establish release references") from exc
                try:
                    try:
                        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    except BlockingIOError:
                        roots.setdefault(release, []).append("live-lease")
                finally:
                    os.close(fd)
        # After commit, current becomes the fallback. Anything else still
        # referenced must fit the explicit draining-version count budget.
        draining = {release for release, reasons in roots.items()
                    if release != state.get("current")
                    and any(reason.startswith("pin:") or reason == "live-lease" for reason in reasons)}
        if len(draining) > self.policy.max_draining_versions:
            raise Refused("draining-version budget reached; do not remove active versions")
        vfs = os.fstatvfs(self.fd)
        block = vfs.f_frsize
        rounded = lambda size: math.ceil(size / block) * block
        expanded = sum(rounded(item["bytes"]) for item in manifest["files"])
        # Conservative: count all expanded files even when blobs are shared.
        # These blocks cover manifest/state/journal replacement and directories.
        metadata_blocks = 24 + 4 * len(manifest["files"])
        diagnostics = self.policy.diagnostic_file_bytes * (self.policy.diagnostic_files + 1)
        required = (rounded(manifest["archive_bytes"]) + expanded + metadata_blocks * block
                    + diagnostics + self.policy.recovery_bytes + self.policy.growth_bytes)
        inodes = (16 + 2 * len(manifest["files"]) + self.policy.recovery_inodes
                  + self.policy.growth_inodes + self.policy.diagnostic_files + 1)
        result = {
            "ok": vfs.f_bavail * block >= required and vfs.f_favail >= inodes,
            "release": manifest["release"], "free_bytes": vfs.f_bavail * block,
            "free_inodes": vfs.f_favail, "required_bytes": required, "required_inodes": inodes,
            "terms": {"archive_allocated_upper": rounded(manifest["archive_bytes"]),
                      "expanded_allocated_upper": expanded, "metadata_upper": metadata_blocks * block,
                      "diagnostics_upper": diagnostics, "recovery_bytes": self.policy.recovery_bytes,
                      "growth_bytes": self.policy.growth_bytes},
            "protected": roots, "credit_for_future_cleanup": 0,
        }
        if not result["ok"]:
            raise Refused("insufficient space or inodes: " + encoded(result).decode().strip())
        return result

    def preflight(self, archive: Path, manifest: Path, expected_manifest: str) -> dict[str, Any]:
        with self.lock():
            artifact = self.load_artifact(archive, manifest, expected_manifest)
            return self._preflight(artifact)

    def _mkdir(self, path: str) -> None:
        with self.parent(path) as (parent, name):
            os.mkdir(name, 0o700, dir_fd=parent)
            os.fsync(parent)
            self.hook("allocated:directory:" + path)

    def _new_bytes(self, path: str, value: bytes, mode: int = 0o600) -> None:
        with os.fdopen(self.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, mode), "wb") as stream:
            stream.write(value)
            stream.flush()
            os.fsync(stream.fileno())

    def _link(self, src: str, dst: str) -> None:
        with self.parent(src) as (sfd, sname), self.parent(dst) as (dfd, dname):
            os.link(sname, dname, src_dir_fd=sfd, dst_dir_fd=dfd, follow_symlinks=False)
            os.fsync(dfd)

    def _unlink(self, path: str) -> None:
        with self.parent(path) as (fd, name):
            os.unlink(name, dir_fd=fd)
            os.fsync(fd)

    def install_fixture(self, archive: Path, manifest_path: Path, expected_manifest: str) -> dict[str, Any]:
        with self.lock():
            manifest = self.load_artifact(archive, manifest_path, expected_manifest)
            if manifest["kind"] != "dp28-native-fixture":
                raise Refused("install-fixture must not execute an actual product package")
            plan = self._preflight(manifest)
            release = manifest["release"]
            if self.exists(f"releases/{release}"):
                raise Refused("release identifiers are immutable and cannot be reused")
            self.hook("after-preflight")
            self.atomic_json("control/install.json", {
                "status": "pending", "nonce": self.marker["nonce"],
                "project": PROJECT, "release": release,
            })
            self._mkdir("staging/pending")
            self.atomic_json("staging/pending/owner.json", {
                "nonce": self.marker["nonce"], "project": PROJECT, "release": release,
            })
            self.hook("before-copy")
            with os.fdopen(os.open(archive, os.O_RDONLY | NOFOLLOW), "rb") as source:
                with os.fdopen(self.open("staging/pending/package.tar.gz", os.O_WRONLY | os.O_CREAT | os.O_EXCL), "wb") as target:
                    sha = hashlib.sha256()
                    total = 0
                    while chunk := source.read(128 * 1024):
                        total += len(chunk)
                        if total > manifest["archive_bytes"]:
                            raise Refused("archive changed during copy")
                        target.write(chunk)
                        sha.update(chunk)
                    target.flush()
                    os.fsync(target.fileno())
            if total != manifest["archive_bytes"] or sha.hexdigest() != manifest["archive_sha256"]:
                raise Refused("archive changed during copy")
            self.hook("archive-durable")
            self._mkdir("staging/pending/release")
            self._new_bytes("staging/pending/release/lease", b"")
            entries = {item["name"]: item for item in manifest["files"]}
            seen = set()
            with os.fdopen(self.open("staging/pending/package.tar.gz"), "rb") as compressed:
                with tarfile.open(fileobj=compressed, mode="r|gz") as tar:
                    for member in tar:
                        if not member.isfile() or member.name not in entries or member.name in seen:
                            raise Refused("unsafe archive member")
                        item = entries[member.name]
                        if member.size != item["bytes"]:
                            raise Refused("unsafe archive size")
                        seen.add(member.name)
                        key = self.object_key(item)
                        obj = "objects/" + key
                        source = tar.extractfile(member)
                        if source is None:
                            raise Refused("missing archive content")
                        sha = hashlib.sha256()
                        temp = "staging/pending/" + key
                        with os.fdopen(self.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, item["mode"]), "wb") as out:
                            count = 0
                            while chunk := source.read(128 * 1024):
                                count += len(chunk)
                                if count > item["bytes"]:
                                    raise Refused("expanded size limit exceeded")
                                sha.update(chunk)
                                out.write(chunk)
                            out.flush()
                            os.fsync(out.fileno())
                        self.hook("allocated:expanded-file:" + member.name)
                        if count != item["bytes"] or sha.hexdigest() != item["sha256"]:
                            raise Refused("expanded content checksum mismatch")
                        if self.exists(obj):
                            if self.file_digest(obj) != item["sha256"] or stat.S_IMODE(self.info(obj).st_mode) != item["mode"]:
                                raise Refused("existing shared object is invalid")
                        else:
                            self._link(temp, obj)
                        self._link(obj, "staging/pending/release/" + member.name)
                        self._unlink(temp)
            if seen != set(entries):
                raise Refused("archive content is incomplete")
            self.hook("extracted")
            # Native test program startup is verified BEFORE current changes.
            receipt = self._probe_at("staging/pending/release", manifest)
            self.atomic_json("staging/pending/release/release.json", {
                "nonce": self.marker["nonce"], "project": PROJECT, "manifest": manifest,
                "manifest_sha256": expected_manifest, "verification": "native-fixture-probe",
                "probe_receipt": receipt,
            })
            with self.directory("staging/pending") as src, self.directory("releases") as dst:
                os.rename("release", release, src_dir_fd=src, dst_dir_fd=dst)
                os.fsync(src)
                os.fsync(dst)
            self.hook("release-durable")
            state = self.state()
            state["previous_verified"] = state["current"]
            state["current"] = release
            state["generation"] += 1
            self.hook("before-state")
            self.atomic_json("control/state.json", state)
            self.hook("state-durable")
            # Leave the owned compressed download for preview + explicit GC.
            return {"status": "fixture-installed", "state": state, "preflight": plan, "probe": receipt}

    def _probe_at(self, relpath: str, manifest: dict[str, Any]) -> dict[str, Any]:
        with self.directory(relpath):
            for item in manifest["files"]:
                if self.file_digest(relpath + "/" + item["name"]) != item["sha256"]:
                    raise Refused("probe input failed verification")
            result = subprocess.run(
                [str(self.root / relpath / "probe")], cwd=self.root / relpath,
                env={"PATH": "/usr/bin:/bin", "LC_ALL": "C"},
                capture_output=True, timeout=5, check=True,
            )
        if len(result.stdout) > 1024 or len(result.stderr) > 1024:
            raise Refused("oversized fixture probe output")
        value = json.loads(result.stdout)
        if value.get("version") != manifest["release"]:
            raise Refused("wrong version started")
        expected_size = next(item["bytes"] for item in manifest["files"] if item["name"] == "payload.bin")
        if value.get("payload_bytes") != expected_size:
            raise Refused("probe did not read its full payload")
        return value

    def rollback_probe(self) -> dict[str, Any]:
        with self.lock():
            self.require_no_gc()
            state = self.state()
            release = state.get("previous_verified")
            if not release:
                raise Refused("no verified fallback exists yet")
            record = self.verify_release(release)
            proof = self._probe_at("releases/" + release, record["manifest"])
            return {"status": "fallback-cold-start-verified", "release": release, "probe": proof,
                    "traffic_switch_tested": False, "database_schema_rollback_tested": False}

    def walk(self, start: str = "") -> list[dict[str, Any]]:
        result = []
        def visit(path: str) -> None:
            with self.directory(path) as fd:
                for name in sorted(os.listdir(fd)):
                    relative = path + "/" + name if path else name
                    try:
                        parts(relative)
                        info = os.stat(name, dir_fd=fd, follow_symlinks=False)
                        if stat.S_ISDIR(info.st_mode):
                            with self.directory(relative):
                                pass
                        elif stat.S_ISREG(info.st_mode):
                            opened = self.open(relative)
                            os.close(opened)
                        else:
                            raise Refused("non-regular entry")
                    except (OSError, Refused) as exc:
                        result.append({"path": relative, "unsafe": type(exc).__name__})
                        continue
                    result.append({"path": relative, "dev": info.st_dev, "ino": info.st_ino,
                                   "mode": info.st_mode, "bytes": info.st_size,
                                   "allocated": info.st_blocks * 512, "mtime_ns": info.st_mtime_ns,
                                   "links": info.st_nlink, "directory": stat.S_ISDIR(info.st_mode)})
                    if stat.S_ISDIR(info.st_mode):
                        visit(relative)
        visit(start)
        return result

    def process_references(self) -> dict[str, Any]:
        # Observational only. Never close a foreign fd or kill a process.
        # Unreadable processes are reported; /proc is NOT a complete Docker
        # reference authority. The mandatory lifecycle lease is the authority.
        references: dict[tuple[int, int], dict[str, Any]] = {}
        inaccessible = set()
        unknown_maps = []
        prefix = str(self.root) + "/"
        for pid in sorted(name for name in os.listdir("/proc") if name.isdigit()):
            try:
                fds = os.listdir(f"/proc/{pid}/fd")
            except FileNotFoundError:
                continue
            except PermissionError:
                inaccessible.add(pid)
                continue
            for descriptor in fds:
                path = f"/proc/{pid}/fd/{descriptor}"
                try:
                    target = os.readlink(path)
                    if not target.startswith(prefix):
                        continue
                    info = os.stat(path)
                    if not stat.S_ISREG(info.st_mode):
                        continue
                    key = info.st_dev, info.st_ino
                    record = references.setdefault(key, {"dev": info.st_dev, "ino": info.st_ino,
                        "allocated": info.st_blocks * 512, "deleted": info.st_nlink == 0,
                        "path": target, "pids": []})
                    if pid not in record["pids"]:
                        record["pids"].append(pid)
                except FileNotFoundError:
                    continue
                except PermissionError:
                    inaccessible.add(pid)
            try:
                with open(f"/proc/{pid}/maps", encoding="utf-8") as maps:
                    for line in maps:
                        fields = line.rstrip().split(None, 5)
                        if len(fields) < 6 or not fields[5].startswith(prefix):
                            continue
                        device = fields[3].split(":")
                        key = os.makedev(int(device[0], 16), int(device[1], 16)), int(fields[4])
                        if key not in references:
                            try:
                                info = os.stat(f"/proc/{pid}/map_files/{fields[0]}")
                                references[key] = {"dev": info.st_dev, "ino": info.st_ino,
                                    "allocated": info.st_blocks * 512, "deleted": info.st_nlink == 0,
                                    "path": fields[5], "pids": [pid]}
                            except (FileNotFoundError, PermissionError):
                                references[key] = {"dev": key[0], "ino": key[1], "allocated": 0,
                                    "deleted": fields[5].endswith(" (deleted)"), "path": fields[5],
                                    "pids": [pid], "allocated_unknown": True}
                                unknown_maps.append({"pid": pid, "path": fields[5]})
            except FileNotFoundError:
                pass
            except PermissionError:
                inaccessible.add(pid)
        return {"files": list(references.values()), "inaccessible_pids": sorted(inaccessible),
                "unknown_mapping_sizes": unknown_maps, "scope": "visible-local-processes-only"}

    def _gc_plan(self) -> dict[str, Any]:
        state = self.state()
        roots = self.release_roots(state)
        entries = self.walk()
        bypath = {item["path"]: item for item in entries}
        references = self.process_references()
        open_inodes = {(item["dev"], item["ino"]) for item in references["files"]}
        with self.directory("releases") as directory:
            releases = sorted(os.listdir(directory))
        retained = dict(roots)
        owned = {}
        for release in releases:
            try:
                owned[release] = self.verify_release(release)
                fd = self.open(f"releases/{release}/lease", os.O_RDWR)
                try:
                    try:
                        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    except BlockingIOError:
                        retained.setdefault(release, []).append("live-lease")
                finally:
                    os.close(fd)
                expected_names = {"release.json", "lease"} | {f["name"] for f in owned[release]["manifest"]["files"]}
                with self.directory("releases/" + release) as rfd:
                    if set(os.listdir(rfd)) != expected_names:
                        retained.setdefault(release, []).append("unknown-content")
                for entry in entries:
                    if entry["path"].startswith("releases/" + release + "/"):
                        if "unsafe" in entry:
                            retained.setdefault(release, []).append("unsafe-path")
                        elif any(ref["path"].removesuffix(" (deleted)").startswith(
                                str(self.root / "releases" / release) + "/")
                                for ref in references["files"]):
                            # A shared binary inode is not evidence that every
                            # release using that binary is a running instance.
                            retained.setdefault(release, []).append("visible-process-reference")
            except (Refused, OSError, ValueError, KeyError):
                retained.setdefault(release, []).append("unowned-or-invalid")
        # An external hard link prevents claiming that this inode is reclaimable.
        counts: dict[tuple[int, int], int] = {}
        for entry in entries:
            if "unsafe" not in entry and not entry["directory"]:
                key = entry["dev"], entry["ino"]
                counts[key] = counts.get(key, 0) + 1
        external = {key for key, count in counts.items()
                    if any((e.get("dev"), e.get("ino")) == key and e.get("links", 0) > count for e in entries)}
        for release in owned:
            if any(e["path"].startswith("releases/" + release + "/") and (e.get("dev"), e.get("ino")) in external for e in entries):
                retained.setdefault(release, []).append("external-hardlink")
        removals: list[dict[str, Any]] = []
        for release in owned:
            if release not in retained:
                removals.extend(e for e in entries if e["path"] == "releases/" + release or e["path"].startswith("releases/" + release + "/"))
        # Unknown releases prevent shared-object collection. A foreign marker
        # must not become a reason to delete a dependency we cannot identify.
        uncertain = set(releases) - set(owned)
        needed = set()
        for release in retained:
            if release in owned:
                needed.update(self.object_key(item) for item in owned[release]["manifest"]["files"])
        for entry in entries:
            if entry["path"].startswith("objects/") and entry["path"].count("/") == 1 and "unsafe" not in entry:
                key = entry["path"].split("/")[1]
                if (not uncertain and key not in needed and re.fullmatch(r"[0-9a-f]{64}-(444|555)", key)
                        and (entry["dev"], entry["ino"]) not in external | open_inodes):
                    if not entry["directory"] and self.file_digest(entry["path"]) == key[:64]:
                        removals.append(entry)
        if "staging/pending" in bypath:
            try:
                owner = self.read_json("control/install.json")
                stage = [e for e in entries if e["path"] == "staging/pending" or e["path"].startswith("staging/pending/")]
                if (owner.get("status") == "pending" and owner.get("nonce") == self.marker["nonce"]
                        and owner.get("project") == PROJECT):
                    if not any("unsafe" in e or (e.get("dev"), e.get("ino")) in external | open_inodes for e in stage):
                        removals.extend(stage)
            except (OSError, Refused, ValueError):
                pass
        # Only these two areas are candidates. No cache/volume/log age heuristic
        # can reach database, WAL, audit, deduplication, payloads or backups.
        if any(not e["path"].startswith(("releases/", "objects/", "staging/pending")) for e in removals):
            raise Refused("internal cleanup boundary violation")
        removals = sorted({e["path"]: e for e in removals}.values(),
                          key=lambda e: (-e["path"].count("/"), e["directory"], e["path"]))
        names = {e["path"] for e in removals}
        unique = {}
        for entry in removals:
            if not entry["directory"]:
                key = entry["dev"], entry["ino"]
                all_links_removed = all(e["path"] in names for e in entries
                                        if (e.get("dev"), e.get("ino")) == key)
                if all_links_removed and key not in external | open_inodes:
                    unique[key] = entry["allocated"]
        # Directory byte counts can vary after child unlink. Never credit them.
        payload = {"project": PROJECT, "nonce": self.marker["nonce"], "generation": state["generation"],
                   "delete": removals, "retain": {k: sorted(set(v)) for k, v in retained.items()},
                   "reclaimable_file_bytes": sum(unique.values()),
                   "protected_data_policy": "never-delete", "uncertain_releases": sorted(uncertain)}
        return {**payload, "plan_sha256": digest(encoded(payload))}

    def gc(self, apply_hash: str | None = None) -> dict[str, Any]:
        with self.lock():
            journal = self.read_json("control/gc.json")
            if (apply_hash is not None and journal.get("status") == "done"
                    and apply_hash == journal.get("plan", {}).get("plan_sha256")):
                completed = journal["plan"]
                if digest(encoded({k: v for k, v in completed.items() if k != "plan_sha256"})) != apply_hash:
                    raise Refused("completed cleanup receipt checksum mismatch")
                if completed.get("nonce") != self.marker["nonce"]:
                    raise Refused("completed cleanup belongs to another root")
                if not self.exists("staging/pending") and self.read_json("control/install.json").get("status") == "pending":
                    self.atomic_json("control/install.json", {"status": "idle"})
                return {"status": "already-done", "receipt": journal}
            if journal.get("status") == "pending":
                plan = journal["plan"]
                if apply_hash is None:
                    return {"status": "resume-required", "plan": plan}
                if apply_hash != plan["plan_sha256"]:
                    raise Refused("use the approved interrupted cleanup plan to resume")
            else:
                plan = self._gc_plan()
                if apply_hash is None:
                    return {"status": "preview", "plan": plan}
                if apply_hash != plan["plan_sha256"]:
                    raise Refused("stale cleanup preview; no deletion was started")
                journal = {"status": "pending", "plan": plan}
                self.atomic_json("control/gc.json", journal)
            if digest(encoded({k: v for k, v in plan.items() if k != "plan_sha256"})) != plan["plan_sha256"]:
                raise Refused("cleanup plan checksum mismatch")
            self.release_roots(self.state())
            if self.state()["generation"] != plan["generation"]:
                raise Refused("release state changed during interrupted cleanup")
            if plan["nonce"] != self.marker["nonce"]:
                raise Refused("cleanup plan belongs to another root")
            deleted, already_missing = [], []
            open_now = self.process_references()["files"]
            remaining: dict[tuple[int, int], list[tuple[dict[str, Any], os.stat_result]]] = {}
            for entry in plan["delete"]:
                if entry["directory"]:
                    continue
                try:
                    info = self.info(entry["path"])
                except FileNotFoundError:
                    continue
                remaining.setdefault((info.st_dev, info.st_ino), []).append((entry, info))
            for ref in open_now:
                aliases = remaining.get((ref["dev"], ref["ino"]), [])
                if not aliases:
                    continue
                exact_path = any(str(self.root / entry["path"]) == ref["path"].removesuffix(" (deleted)")
                                 for entry, _ in aliases)
                final_link = len(aliases) >= aliases[0][1].st_nlink
                if exact_path or final_link:
                    raise Refused("cleanup candidate has a visible process reference; retry after release")
            # Hold exclusive leases while deleting. Starts use the global lock.
            with contextlib.ExitStack() as stack:
                candidates = {e["path"].split("/")[1] for e in plan["delete"] if e["path"].startswith("releases/")}
                for release in sorted(candidates):
                    if self.exists(f"releases/{release}/lease"):
                        fd = self.open(f"releases/{release}/lease", os.O_RDWR)
                        stack.callback(os.close, fd)
                        try:
                            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                        except BlockingIOError as exc:
                            raise Refused("release still has an active lease") from exc
                for entry in plan["delete"]:
                    path = entry["path"]
                    # Resume never interprets new arbitrary paths from disk as a
                    # to-delete list. The durable intent binds inode and type.
                    if not path.startswith(("releases/", "objects/", "staging/pending")):
                        raise Refused("cleanup journal has an invalid path")
                    try:
                        info = self.info(path)
                    except FileNotFoundError:
                        already_missing.append(path)
                        continue
                    if (info.st_dev, info.st_ino, info.st_mode) != (entry["dev"], entry["ino"], entry["mode"]):
                        raise Refused("cleanup entry was replaced")
                    if not entry["directory"] and (info.st_size, info.st_mtime_ns) != (entry["bytes"], entry["mtime_ns"]):
                        raise Refused("cleanup entry changed")
                    self.hook("before-unlink")
                    with self.parent(path) as (fd, name):
                        if entry["directory"]:
                            os.rmdir(name, dir_fd=fd)
                        else:
                            os.unlink(name, dir_fd=fd)
                        os.fsync(fd)
                    deleted.append(path)
                    self.hook("after-unlink")
            receipt = {"status": "done", "plan": plan, "deleted_this_attempt": deleted,
                       "already_missing_on_resume": already_missing}
            self.hook("before-gc-receipt")
            self.atomic_json("control/gc.json", receipt)
            if not self.exists("staging/pending"):
                self.atomic_json("control/install.json", {"status": "idle"})
            return receipt

    def bill(self) -> dict[str, Any]:
        with self.lock():
            entries = self.walk()
            refs = self.process_references()
            categories: dict[str, dict[str, int]] = {}
            unique = set()
            apparent = 0
            # Count immutable shared objects first, once per physical inode.
            entries.sort(key=lambda e: (not e["path"].startswith("objects/"), e["path"]))
            for entry in entries:
                if "unsafe" in entry:
                    continue
                path = entry["path"]
                category = "/".join(path.split("/")[:2]) if path.startswith("protected/") else path.split("/")[0]
                bucket = categories.setdefault(category, {"allocated_unique_bytes": 0, "inodes": 0})
                key = entry["dev"], entry["ino"]
                apparent += entry["bytes"]
                if key not in unique:
                    unique.add(key)
                    bucket["allocated_unique_bytes"] += entry["allocated"]
                    bucket["inodes"] += 1
            filesystem = os.fstatvfs(self.fd)
            deleted = [item for item in refs["files"] if item["deleted"]]
            # Multiple descriptors/mappings of one inode are already coalesced.
            return {"categories": categories, "reachable_allocated_bytes": sum(v["allocated_unique_bytes"] for v in categories.values()),
                    "apparent_with_hardlink_duplicates": apparent,
                    "filesystem": {"total_bytes": filesystem.f_blocks * filesystem.f_frsize,
                                   "used_bytes": (filesystem.f_blocks - filesystem.f_bfree) * filesystem.f_frsize,
                                   "available_bytes": filesystem.f_bavail * filesystem.f_frsize,
                                   "available_inodes": filesystem.f_favail,
                                   "total_inodes": filesystem.f_files},
                    "open_deleted_allocated_lower_bound": sum(item["allocated"] for item in deleted),
                    "process_references": refs, "unsafe_entries": [e for e in entries if "unsafe" in e],
                    "state": self.state()}

    def diagnostic(self, event: str, count: int = 1) -> bool:
        # No raw message, request, response, URL, credential or arbitrary fields.
        if event not in {"request-complete", "release-refused", "cleanup-complete"} or type(count) is not int or not 0 <= count <= 10**9:
            raise Refused("diagnostic fields are not allow-listed")
        record = encoded({"event": event, "count": count, "time": int(time.time())})
        try:
            with self.lock():
                self.require_no_gc()
                with self.directory("logs") as fd:
                    allowed = {f"diag-{i}.jsonl" for i in range(self.policy.diagnostic_files)}
                    for name in os.listdir(fd):
                        if name not in allowed:
                            raise Refused("unknown diagnostic file")
                        info = os.stat(name, dir_fd=fd, follow_symlinks=False)
                        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
                            raise Refused("unsafe diagnostic file")
                    now = time.time()
                    for i in range(1, self.policy.diagnostic_files):
                        name = f"diag-{i}.jsonl"
                        try:
                            info = os.stat(name, dir_fd=fd, follow_symlinks=False)
                            if now - info.st_mtime > self.policy.diagnostic_keep_seconds:
                                os.unlink(name, dir_fd=fd)
                        except FileNotFoundError:
                            pass
                    current = "diag-0.jsonl"
                    size = os.stat(current, dir_fd=fd).st_size if current in os.listdir(fd) else 0
                    if size + len(record) > self.policy.diagnostic_file_bytes:
                        oldest = f"diag-{self.policy.diagnostic_files - 1}.jsonl"
                        try:
                            os.unlink(oldest, dir_fd=fd)
                        except FileNotFoundError:
                            pass
                        for i in range(self.policy.diagnostic_files - 2, -1, -1):
                            try:
                                os.rename(f"diag-{i}.jsonl", f"diag-{i+1}.jsonl", src_dir_fd=fd, dst_dir_fd=fd)
                            except FileNotFoundError:
                                pass
                    out = os.open(current, os.O_WRONLY | os.O_APPEND | os.O_CREAT | NOFOLLOW, 0o600, dir_fd=fd)
                    with os.fdopen(out, "wb") as stream:
                        stream.write(record)
                        stream.flush()
                        os.fsync(stream.fileno())
                    os.fsync(fd)
                    self.hook("allocated:diagnostic-log")
            return True
        except OSError as exc:
            if exc.errno not in {errno.ENOSPC, errno.EDQUOT, errno.EIO, errno.EROFS, errno.EACCES}:
                raise
            # Diagnostic loss is explicit. Required lifecycle state/GC receipts
            # use atomic_json instead and MUST propagate their write failure.
            return False


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, required=True)
    commands = parser.add_subparsers(dest="command", required=True)
    init = commands.add_parser("init")
    init.add_argument("--policy", type=Path, required=True)
    commands.add_parser("bill")
    clean = commands.add_parser("gc", help="preview unless --apply-plan-sha256 is supplied")
    clean.add_argument("--apply-plan-sha256")
    for name in ("preflight", "install-fixture"):
        action = commands.add_parser(name)
        action.add_argument("--archive", type=Path, required=True)
        action.add_argument("--manifest", type=Path, required=True)
        action.add_argument("--manifest-sha256", required=True)
    commands.add_parser("rollback-probe")
    args = parser.parse_args()
    try:
        if args.command == "init":
            Store.initialize(args.root, Policy(**read_json_file(args.policy)))
            result = {"status": "initialized-local-lab"}
        else:
            with Store(args.root) as store:
                if args.command == "bill":
                    result = store.bill()
                elif args.command == "gc":
                    result = store.gc(args.apply_plan_sha256)
                elif args.command == "rollback-probe":
                    result = store.rollback_probe()
                else:
                    action = store.preflight if args.command == "preflight" else store.install_fixture
                    result = action(args.archive, args.manifest, args.manifest_sha256)
        print(json.dumps(result, indent=2, sort_keys=True))
        return 0
    except (Refused, OSError, ValueError, KeyError, TypeError, tarfile.TarError, subprocess.SubprocessError) as exc:
        print(json.dumps({"status": "failed", "error": str(exc), "success": False}), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
