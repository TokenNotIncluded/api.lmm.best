#!/usr/bin/env python3
"""Build or inspect independent preview archives. No install, publish or deploy."""
from __future__ import annotations

import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import struct
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
REVISION = re.compile(r"[0-9a-f]{40}")
VERSION = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9][a-zA-Z0-9.-]*)?")
MAX_FILE = 512 * 1024 * 1024
MAX_TOTAL = 2 * 1024 * 1024 * 1024
MAX_FILES = 20000


def safe_path(name: str) -> str:
    path = PurePosixPath(name)
    # Exact public discovery resource, not a blanket hidden-file exception.
    public_discovery = name in {".well-known/webmcp.json", "dist/.well-known/webmcp.json"}
    if (not name or path.is_absolute() or str(path) != name or "\\" in name or ":" in name
            or any(part in {".", ".."} or (part.startswith(".") and not public_discovery)
                   for part in path.parts)
            or any(ord(char) < 32 for char in name)):
        raise ValueError("unsafe archive path")
    if any(part in {"node_modules", "target", "__pycache__", "site.json"} for part in path.parts):
        raise ValueError("private configuration or build cache in archive")
    if path.suffix.lower() in {".pem", ".key", ".p12", ".pfx"}:
        raise ValueError("credential file in archive")
    return name


def read_regular(root: Path, name: str) -> bytes:
    safe_path(name)
    if root.is_symlink():
        raise ValueError("input root must not be a symlink")
    path = root
    for part in PurePosixPath(name).parts:
        path = path / part
        if path.is_symlink():
            raise ValueError("symlinks are not distribution inputs")
    info = path.stat()
    if not stat.S_ISREG(info.st_mode) or info.st_size > MAX_FILE:
        raise ValueError("input must be a bounded regular file")
    data = path.read_bytes()
    if len(data) > MAX_FILE:
        raise ValueError("oversized distribution input")
    return data


def check_binary(data: bytes, platform: str) -> None:
    system, arch = platform.split("-")
    machine = {"amd64": 62, "arm64": 183}[arch]
    if system == "linux":
        valid = (len(data) >= 64 and data[:6] == b"\x7fELF\x02\x01"
                 and struct.unpack_from("<H", data, 18)[0] == machine)
    elif system == "macos":
        cpu = {"amd64": 0x01000007, "arm64": 0x0100000C}[arch]
        valid = (len(data) >= 32 and data[:4] == b"\xcf\xfa\xed\xfe"
                 and struct.unpack_from("<I", data, 4)[0] == cpu)
    else:
        offset = struct.unpack_from("<I", data, 60)[0] if len(data) >= 64 else 0
        cpu = {"amd64": 0x8664, "arm64": 0xAA64}[arch]
        valid = (data[:2] == b"MZ" and offset >= 64 and offset + 6 <= len(data)
                 and data[offset:offset + 4] == b"PE\0\0"
                 and struct.unpack_from("<H", data, offset + 4)[0] == cpu)
    if not valid:
        raise ValueError("binary format or architecture does not match the package")


def build(root: Path, component: str, version: str, revision: str,
          platform: str, inputs: Path, output: Path, epoch: int = 0) -> Path:
    if not VERSION.fullmatch(version) or not REVISION.fullmatch(revision):
        raise ValueError("version or full source revision is invalid")
    if not 0 <= epoch <= 0xFFFFFFFF:
        raise ValueError("SOURCE_DATE_EPOCH is out of range")
    spec = json.loads((root / "packaging/distribution.json").read_text())
    if spec["schema_version"] != 1 or component not in spec["components"]:
        raise ValueError("unknown distribution schema or component")
    selected = spec["components"][component]
    if platform not in selected["platforms"]:
        raise ValueError("unsupported component platform")
    package = selected["package"]
    if not re.fullmatch(r"[a-z][a-z0-9-]*", package):
        raise ValueError("invalid package name")
    files: dict[str, tuple[bytes, int]] = {}
    total = 0

    def add(name: str, data: bytes, mode: int = 0o644) -> None:
        nonlocal total
        safe_path(name)
        if name in files or name == "MANIFEST.json":
            raise ValueError("duplicate or reserved archive entry")
        total += len(data)
        if len(files) >= MAX_FILES or total > MAX_TOTAL:
            raise ValueError("distribution exceeds file or size limits")
        files[name] = (data, mode)

    for name in spec["common"] + selected["resources"]:
        add(name, read_regular(root, name))
    for binary in selected["binaries"]:
        name = binary + (".exe" if platform.startswith("windows-") else "")
        data = read_regular(inputs, name)
        check_binary(data, platform)
        add("bin/" + name, data, 0o755)
    if component == "web":
        if not (inputs / "index.html").is_file():
            raise ValueError("web input has no index.html")
        if inputs.is_symlink():
            raise ValueError("web input must not be a symlink")
        for path in sorted(inputs.rglob("*")):
            if path.is_symlink():
                raise ValueError("web input contains a symlink")
            if not path.is_dir():
                name = path.relative_to(inputs).as_posix()
                add("dist/" + name, read_regular(inputs, name))
    readme = (f"# {package} {version}\n\n"
              f"Preview only. Source revision: `{revision}`. Platform: `{platform}`.\n\n"
              "This archive does not install or start services, change a database, "
              "or qualify a site for production. It has no post-install hook. "
              "Keep the core and extension processes separate.\n\n"
              "Check the archive SHA-256 against your trusted build record. "
              "MANIFEST.json records the contents; it is not a signature. "
              "Do not overwrite local settings when replacing a program.\n")
    if component == "extensions":
        readme += ("\nPolicy drafts are in config/legal. Copy that directory to a private "
                   "configuration directory, fill and review site.example.json, save it "
                   "as site.json, then set LMM_LEGAL_CONFIG_FILE. Never edit installed "
                   "templates in place. See docs/legal/README.md.\n")
    add("README.md", readme.encode())
    manifest = {"schema_version": 1, "component": component, "package": package,
                "version": version, "revision": revision, "platform": platform,
                "preview": True, "source_date_epoch": epoch,
                "files": {name: {"sha256": hashlib.sha256(data).hexdigest(),
                                 "size": len(data), "mode": mode}
                          for name, (data, mode) in sorted(files.items())}}
    files["MANIFEST.json"] = ((json.dumps(manifest, sort_keys=True, indent=2) + "\n").encode(), 0o644)
    output.mkdir(parents=True, exist_ok=True)
    archive = output / f"{package}-{version}-{platform}.tar.gz"
    checksum = archive.with_name(archive.name + ".sha256")
    if archive.exists() or checksum.exists():
        raise FileExistsError("distribution output already exists; refusing to replace it")
    # Stage and verify before exposing a complete archive. Exclusive creation
    # prevents two builds from silently overwriting one another's output.
    with tempfile.TemporaryDirectory(dir=output) as temporary:
        staged = Path(temporary) / "archive.tar.gz"
        with staged.open("wb") as raw:
            with gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=epoch) as compressed:
                with tarfile.open(fileobj=compressed, mode="w|", format=tarfile.PAX_FORMAT) as tar:
                    for name, (data, mode) in sorted(files.items()):
                        info = tarfile.TarInfo(name)
                        info.size, info.mode, info.mtime = len(data), mode, epoch
                        info.uid = info.gid = 0
                        info.uname = info.gname = ""
                        tar.addfile(info, io.BytesIO(data))
        verify(staged, revision)
        digest = hashlib.sha256(staged.read_bytes()).hexdigest()
        # Hard links are atomic, fail when the destination exists and never
        # expose a partially written archive. The staging directory is local.
        os.link(staged, archive)
        try:
            with checksum.open("x", encoding="utf-8") as file:
                file.write(f"{digest}  {archive.name}\n")
        except BaseException:
            archive.unlink()
            raise
    return archive


def verify(archive: Path, revision: str | None = None) -> dict:
    files: dict[str, tuple[bytes, int]] = {}
    total = 0
    with tarfile.open(archive, "r:gz") as tar:
        for entry in tar:
            safe_path(entry.name)
            if (not entry.isfile() or entry.name in files or entry.mode not in {0o644, 0o755}
                    or entry.uid != 0 or entry.gid != 0 or not 0 <= entry.size <= MAX_FILE):
                raise ValueError("unsafe or duplicate archive member")
            total += entry.size
            if total > MAX_TOTAL or len(files) >= MAX_FILES:
                raise ValueError("archive exceeds inspection limits")
            source = tar.extractfile(entry)
            if source is None:
                raise ValueError("unreadable archive member")
            files[entry.name] = (source.read(), entry.mode)
    if "MANIFEST.json" not in files:
        raise ValueError("archive has no manifest")
    manifest = json.loads(files.pop("MANIFEST.json")[0])
    if (manifest["schema_version"] != 1 or manifest["preview"] is not True
            or not REVISION.fullmatch(manifest["revision"])
            or not VERSION.fullmatch(manifest["version"])
            or (revision is not None and manifest["revision"] != revision)):
        raise ValueError("manifest identity does not match")
    if set(manifest["files"]) != set(files):
        raise ValueError("manifest inventory does not match")
    for name, (data, mode) in files.items():
        expected = manifest["files"][name]
        if expected != {"sha256": hashlib.sha256(data).hexdigest(), "size": len(data), "mode": mode}:
            raise ValueError("manifest content does not match")
    return manifest


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    actions = parser.add_subparsers(dest="action", required=True)
    create = actions.add_parser("build")
    create.add_argument("--component", required=True)
    create.add_argument("--version", required=True)
    create.add_argument("--revision", required=True)
    create.add_argument("--platform", required=True)
    create.add_argument("--input", required=True, type=Path)
    create.add_argument("--output", default=ROOT / "out/distributions", type=Path)
    create.add_argument("--epoch", type=int, default=os.environ.get("SOURCE_DATE_EPOCH", "0"))
    inspect = actions.add_parser("verify")
    inspect.add_argument("archive", type=Path)
    inspect.add_argument("--revision")
    args = parser.parse_args()
    try:
        if args.action == "build":
            print(build(ROOT, args.component, args.version, args.revision,
                        args.platform, args.input, args.output, args.epoch))
        else:
            manifest = verify(args.archive, args.revision)
            print(f"Verified {manifest['component']}: {manifest['revision']}")
    except (OSError, ValueError, KeyError, TypeError, tarfile.TarError) as error:
        parser.exit(1, f"Distribution failed: {error}\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
