"""Deterministic fixture packages and local-only test helpers."""
from __future__ import annotations
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import random
import subprocess
import tarfile
from typing import Any
from disk_lifecycle import PROJECT, Policy, Store, digest, encoded

DEFAULT_POLICY = Policy(
    recovery_bytes=128 * 1024, recovery_inodes=16,
    growth_bytes=64 * 1024, growth_inodes=16,
    max_draining_versions=2,
)


def make_archive(directory: Path, binary: Path, version: int, payload_bytes: int = 262144) -> tuple[Path, Path, str]:
    release = f"r{version:03d}"
    files = {
        "probe": (binary.read_bytes(), 0o555),
        "payload.bin": (random.Random(version).randbytes(payload_bytes), 0o444),
        "version.txt": ((release + "\n").encode(), 0o444),
    }
    archive = directory / f"{release}.tar.gz"
    with archive.open("wb") as raw:
        with gzip.GzipFile(fileobj=raw, mode="wb", mtime=0, filename="") as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as tar:
                for name, (content, mode) in files.items():
                    info = tarfile.TarInfo(name)
                    info.mode, info.size, info.mtime = mode, len(content), 0
                    tar.addfile(info, io.BytesIO(content))
    manifest = {
        "project": PROJECT, "kind": "dp28-native-fixture", "release": release,
        "archive_sha256": digest(archive.read_bytes()), "archive_bytes": archive.stat().st_size,
        "files": [{"name": name, "sha256": digest(content), "bytes": len(content), "mode": mode}
                  for name, (content, mode) in files.items()],
    }
    path = directory / f"{release}.manifest.json"
    path.write_bytes(encoded(manifest))
    return archive, path, digest(path.read_bytes())


def new_store(directory: Path, name: str = "project", policy: Policy = DEFAULT_POLICY) -> Store:
    root = directory / name
    root.mkdir(mode=0o700)
    Store.initialize(root, policy)
    return Store(root)


def clean(store: Store) -> dict[str, Any]:
    preview = store.gc()
    return store.gc(preview["plan"]["plan_sha256"])


def install(store: Store, artifact: tuple[Path, Path, str]) -> dict[str, Any]:
    value = store.install_fixture(*artifact)
    clean(store)
    return value


def protect_sentinels(store: Store) -> dict[str, str]:
    files = {
        "protected/database/synthetic-db.bin": b"SYNTHETIC ONLY -- NOT POSTGRES\n" * 128,
        "protected/wal/synthetic-wal.bin": b"SYNTHETIC RECOVERY EVIDENCE\n" * 128,
        "protected/dedup/synthetic-keys.bin": b"do-not-reapply-this-fixture-operation\n" * 64,
        "protected/audit/synthetic-audit.bin": b"preserve-audit-fixture\n" * 64,
        "backups/synthetic-backup.bin": b"NOT A RESTORABLE DATABASE BACKUP\n" * 128,
        "protected/event_payloads/unconfirmed.bin": b"UNCONFIRMED FIXTURE EVENT\n" * 64,
    }
    for path, content in files.items():
        (store.root / path).write_bytes(content)
    return {path: digest(content) for path, content in files.items()}


def assert_sentinels(store: Store, sentinels: dict[str, str]) -> None:
    for path, expected in sentinels.items():
        assert store.file_digest(path) == expected, path


def fill_blocks(directory: Path, leave_bytes: int = 0) -> Path:
    """Fill only a test-owned pressure file on a known small lab tmpfs."""
    filename = directory / "pressure.bin"
    fd = os.open(filename, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        while True:
            vfs = os.fstatvfs(fd)
            available = vfs.f_bavail * vfs.f_frsize
            remaining = available - leave_bytes
            if remaining <= 0:
                break
            try:
                os.write(fd, b"x" * min(65536, remaining))
            except OSError as exc:
                if exc.errno != 28:
                    raise
                break
        os.fsync(fd)
    finally:
        os.close(fd)
    return filename


def df_du(root: Path) -> dict[str, Any]:
    output = {}
    for name, command in {
        "df_bytes": ["df", "-B1", str(root)],
        "df_inodes": ["df", "-i", str(root)],
        "du_allocated": ["du", "-sx", "-B1", str(root)],
        "du_apparent": ["du", "-sx", "--apparent-size", "-B1", str(root)],
    }.items():
        output[name] = subprocess.run(command, capture_output=True, text=True, check=True).stdout
    return output
