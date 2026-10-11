#!/usr/bin/env python3
"""Convert a verified preview archive to an independent native Linux package.

Build-machine tool only. Never executes input binaries, downloads tools, installs
packages, starts services, publishes artifacts or changes a database.
"""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("distribution", ROOT / "scripts/build-distribution.py")
DIST = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DIST)
FORMATS = ("deb", "rpm", "apk", "archlinux")
SUFFIXES = {"deb": ".deb", "rpm": ".rpm", "apk": ".apk", "archlinux": ".pkg.tar.zst"}
LIBRARIES = {
    "libc.so.6": ("libc6", "glibc", "glibc"),
    "ld-linux-x86-64.so.2": ("libc6", "glibc", "glibc"),
    "ld-linux-aarch64.so.1": ("libc6", "glibc", "glibc"),
    "libm.so.6": ("libc6", "glibc", "glibc"),
    "libpthread.so.0": ("libc6", "glibc", "glibc"),
    "libdl.so.2": ("libc6", "glibc", "glibc"),
    "librt.so.1": ("libc6", "glibc", "glibc"),
    "libgcc_s.so.1": ("libgcc-s1", "libgcc_s.so.1()(64bit)", "gcc-libs"),
    "libdbus-1.so.3": ("libdbus-1-3", "libdbus-1.so.3()(64bit)", "dbus"),
}
# Inspect, rather than execute, ELF files. Unknown dependencies fail closed.
INTERPRETERS = {"amd64": "/lib64/ld-linux-x86-64.so.2", "arm64": "/lib/ld-linux-aarch64.so.1"}


def run(*args: str, **kwargs) -> str:
    return subprocess.run(args, check=True, text=True, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, timeout=300, **kwargs).stdout


def inspect_elf(path: Path, arch: str) -> dict:
    data = path.read_bytes()
    DIST.check_binary(data, "linux-" + arch)
    if int.from_bytes(data[16:18], "little") not in {2, 3}:
        raise ValueError("ELF input is not an executable")
    text = run("readelf", "--wide", "--program-headers", "--dynamic", "--version-info", str(path),
               env={**os.environ, "LC_ALL": "C"})
    match = re.search(r"Requesting program interpreter:\s*([^\]]+)\]", text)
    interpreter = match[1].strip() if match else None
    needed = sorted(set(re.findall(r"\(NEEDED\).*?\[([^\]]+)\]", text)))
    loaders = {Path(value).name for value in INTERPRETERS.values()}
    wrong_loader = set(needed) & (loaders - {Path(INTERPRETERS[arch]).name})
    unknown = (set(needed) - LIBRARIES.keys()) | wrong_loader
    if unknown or (interpreter is not None and interpreter != INTERPRETERS[arch]):
        raise ValueError("unsupported ELF interpreter or shared library; use an inspected static build")
    if needed and interpreter is None:
        raise ValueError("shared ELF input has no executable interpreter")
    versions = {tuple(map(int, s.split("."))) for s in re.findall(r"\bGLIBC_(\d+\.\d+(?:\.\d+)?)\b", text)}
    if interpreter and not versions:
        raise ValueError("cannot determine the glibc minimum")
    return {"linkage": "glibc" if interpreter else "static", "needed": needed,
            "glibc_minimum": ".".join(map(str, max(versions))) if versions else None}


def dependencies(reports: list[dict], fmt: str, service: bool, network: bool) -> list[str]:
    deps = {"ca-certificates"} if network else set()
    if service:
        deps.add("openrc" if fmt == "apk" else
                 {"deb": "systemd (>= 247)", "rpm": "systemd >= 247", "archlinux": "systemd>=247"}[fmt])
    if fmt == "apk":
        if any(r["linkage"] != "static" for r in reports):
            raise ValueError("Alpine APK requires a verified static binary, not a glibc archive")
        return sorted(deps)
    column = {"deb": 0, "rpm": 1, "archlinux": 2}[fmt]
    for report in reports:
        for lib in report["needed"]:
            deps.add(LIBRARIES[lib][column])
    versions = [tuple(map(int, r["glibc_minimum"].split("."))) for r in reports if r["glibc_minimum"]]
    if versions:
        version = ".".join(map(str, max(versions)))
        name = "libc6" if fmt == "deb" else "glibc"
        deps.discard(name)
        deps.add({"deb": f"{name} (>= {version})", "rpm": f"{name} >= {version}",
                  "archlinux": f"{name}>={version}"}[fmt])
    return sorted(deps)


def native_version(version: str, release: int, fmt: str) -> tuple[str, str]:
    """Keep prereleases older than a stable release in each package manager."""
    match = re.fullmatch(r"(\d+\.\d+\.\d+)(?:-(alpha|beta|rc|preview)(?:\.(\d+))?)?", version)
    if not match:
        raise ValueError("native packages require a stable version or alpha/beta/rc/preview[.number]")
    base, tag, number = match.groups()
    if not tag:
        return base, str(release)
    number = number or "0"
    if fmt == "deb":
        return f"{base}~{tag}.{number}", str(release)
    if fmt == "rpm":
        return base, f"0.{tag}.{number}.{release}"
    tag = "pre" if tag == "preview" else tag
    if fmt == "apk":
        return f"{base}_{tag}{number}", str(release)
    return f"{base}{tag}{number}", str(release)


def template(root: Path, name: str, values: dict[str, str]) -> bytes:
    text = DIST.read_regular(root, "packaging/linux/assets/" + name).decode()
    for key, value in values.items():
        text = text.replace("@" + key + "@", value)
    if re.search(r"@[A-Z_]+@", text):
        raise ValueError("unresolved package asset placeholder")
    return text.encode()


def prepare(root: Path, archive: Path, expected_hash: str, revision: str,
            component: str, fmt: str, release: int, stage: Path) -> tuple[dict, dict]:
    if fmt not in FORMATS or not 1 <= release <= 999999:
        raise ValueError("unsupported format or invalid package release")
    if not re.fullmatch(r"[a-f0-9]{64}", expected_hash) or not DIST.REVISION.fullmatch(revision):
        raise ValueError("a trusted SHA-256 and full source revision are required")
    catalog = json.loads(DIST.read_regular(root, "packaging/linux/packages.json"))
    if catalog["schema_version"] != 1 or component not in catalog["packages"]:
        raise ValueError("unknown native package component")
    package = catalog["packages"][component]
    # Snapshot the archive to avoid changing inputs between validation and use.
    if archive.is_symlink() or not archive.is_file() or archive.stat().st_size > DIST.MAX_FILE:
        raise ValueError("input must be a bounded regular archive")
    stage.mkdir(mode=0o700, parents=True, exist_ok=False)
    snapshot = stage / "source.tar.gz"
    with archive.open("rb") as src, snapshot.open("xb") as dst:
        digest, total = hashlib.sha256(), 0
        while chunk := src.read(1024 * 1024):
            total += len(chunk)
            if total > DIST.MAX_FILE:
                raise ValueError("input archive exceeds size limit")
            digest.update(chunk)
            dst.write(chunk)
    if digest.hexdigest() != expected_hash:
        raise ValueError("archive SHA-256 does not match trusted input")
    manifest = DIST.verify(snapshot, revision)
    platform = manifest["platform"]
    if manifest["component"] != package["archive"] or platform not in {"linux-amd64", "linux-arm64", "any"}:
        raise ValueError("archive component or platform mismatch")
    if (component == "web") != (platform == "any"):
        raise ValueError("architecture-independent packages must contain only web assets")
    distribution = json.loads(DIST.read_regular(root, "packaging/distribution.json"))
    spec = distribution["components"][package["archive"]]
    if manifest["package"] != spec["package"] or package["name"] != spec["package"]:
        raise ValueError("archive package identity mismatch")
    if package["binaries"] != spec["binaries"]:
        raise ValueError("native package binary inventory differs from the component archive")
    allowed = set(distribution["common"] + spec["resources"] + ["README.md"])
    allowed.update("bin/" + b for b in spec["binaries"])
    actual = set(manifest["files"])
    if component == "web":
        allowed.update(p for p in actual if p.startswith("dist/"))
    if actual != allowed or (component == "web" and "dist/index.html" not in actual):
        raise ValueError("unexpected or missing archive members")
    epoch = manifest["source_date_epoch"]
    if type(epoch) is not int or not 0 <= epoch <= 0xFFFFFFFF:
        raise ValueError("invalid source timestamp")
    stamp = datetime.fromtimestamp(epoch, timezone.utc).isoformat()
    arch = "all" if component == "web" else platform.split("-")[1]
    name = package["name"]
    pkg_version, pkg_release = native_version(manifest["version"], release, fmt)
    contents, reports, installed = [], [], {}

    def add(dst: str, data: bytes, mode: int = 0o644, kind: str = "") -> Path:
        if not dst.startswith(("/usr/", "/etc/")) or dst in installed:
            raise ValueError("duplicate or invalid installed path")
        src = stage / "root" / dst.lstrip("/")
        src.parent.mkdir(parents=True, exist_ok=True)
        src.write_bytes(data)
        src.chmod(mode)
        os.utime(src, (epoch, epoch))
        record = {"src": str(src.resolve()), "dst": dst,
                  "file_info": {"mode": mode, "mtime": stamp, "owner": "root", "group": "root"}}
        if kind:
            record["type"] = kind
        contents.append(record)
        installed[dst] = {"sha256": hashlib.sha256(data).hexdigest(), "mode": mode, "size": len(data)}
        return src

    with tarfile.open(snapshot, "r:gz") as tar:
        def get(path: str) -> bytes:
            return tar.extractfile(path).read()
        for binary in package["binaries"]:
            path = add("/usr/bin/" + binary, get("bin/" + binary), 0o755)
            reports.append(inspect_elf(path, arch))
        for path in distribution["common"]:
            add(f"/usr/share/licenses/{name}/{path}", get(path))
        add(f"/usr/share/doc/{name}/README.archive.md", get("README.md"))
        for path in spec["resources"]:
            if path == f"config/{component}.example.toml":
                add(f"/etc/lmm.conf.d/{component}.toml", get(path), 0o644, "config|noreplace")
            elif path.startswith("config/legal/"):
                add("/usr/share/lmm/legal/" + path.removeprefix("config/legal/"), get(path))
            elif path == "docs/filesystem-layout.md":
                add(f"/usr/share/doc/{name}/filesystem-layout.md", get(path))
            else:
                add(f"/usr/share/{name}/{path}", get(path))
        if component == "web":
            for path in sorted(actual):
                if path.startswith("dist/"):
                    add("/usr/share/lmm/web/" + path.removeprefix("dist/"), get(path))
    scripts = {}
    if package["service"]:
        if f"/etc/lmm.conf.d/{component}.toml" not in installed:
            raise ValueError("service archive is missing its canonical TOML example")
        values = {"PACKAGE": name, "COMPONENT": component,
                  "MEMORY": "256M" if component == "core" else "192M"}
        if fmt == "apk":
            add(f"/etc/init.d/{name}", template(root, "service.openrc", values), 0o755, "config|noreplace")
            script = stage / "preinstall.sh"
            script.write_bytes(template(root, "alpine.preinstall", values))
            # nFPM uses the hook file mtime for APK control metadata.
            os.utime(script, (epoch, epoch))
            scripts["preinstall"] = str(script.resolve())
        else:
            add(f"/usr/lib/systemd/system/{name}.service", template(root, "service.systemd", values))
            accounts = template(root, "service.sysusers", values)
            if component == "core":
                accounts += b'g lmm-rpc -\n'
            add(f"/usr/lib/sysusers.d/{name}.conf", accounts)
            add(f"/usr/share/doc/{name}/rpc.service.conf.example", template(root, f"rpc-{component}.systemd", values))
    deps = dependencies(reports, fmt, package["service"], bool(package["binaries"]))
    info = {"schema_version": 1, "package": name, "component": component,
            "version": manifest["version"], "release": release, "format": fmt, "arch": arch,
            "native_version": pkg_version, "native_release": pkg_release,
            "source_revision": revision, "source_archive_sha256": expected_hash,
            "preview": True, "source_date_epoch": epoch, "elf": reports,
            "dependencies": deps, "files": dict(installed), "nfpm_version": catalog["nfpm_version"]}
    add(f"/usr/share/doc/{name}/BUILDINFO.json", (json.dumps(info, indent=2, sort_keys=True) + "\n").encode())
    config = {"name": name, "arch": arch, "platform": "linux", "version": pkg_version,
              "release": pkg_release, "version_schema": "none", "mtime": stamp,
              "maintainer": catalog["maintainer"], "homepage": catalog["homepage"],
              "license": "AGPL-3.0-or-later", "section": "net", "priority": "optional",
              "description": f"{name}: independent LMM preview component. No automatic activation or database changes.",
              "disable_globbing": True, "depends": deps, "contents": contents,
              "scripts": scripts, "rpm": {"compression": "gzip", "buildhost": "reproducible"},
              "deb": {"compression": "gzip"}}
    if component == "web":
        config.setdefault(fmt, {})["arch"] = {"deb": "all", "rpm": "noarch", "apk": "noarch", "archlinux": "any"}[fmt]
    return config, info


def build(args, root: Path = ROOT) -> Path:
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    catalog = json.loads((root / "packaging/linux/packages.json").read_text())
    tool_version = run(args.nfpm, "--version")
    if not re.search(r"\b" + re.escape(catalog["nfpm_version"]) + r"\b", tool_version):
        raise ValueError("nFPM version differs from the reviewed package builder")
    with tempfile.TemporaryDirectory(prefix=".linux-package-", dir=output) as work:
        stage = Path(work) / "stage"
        config, info = prepare(root, args.archive, args.sha256, args.revision,
                               args.component, args.format, args.release, stage)
        cfg = stage / "nfpm.json"
        cfg.write_text(json.dumps(config, indent=2) + "\n")
        name = f'{info["package"]}-{info["version"]}-{info["release"]}-{info["arch"]}{SUFFIXES[args.format]}'
        final = output / name
        checksum = output / (name + ".sha256")
        evidence = output / (name + ".buildinfo.json")
        if any(p.exists() or p.is_symlink() for p in (final, checksum, evidence)):
            raise FileExistsError("package outputs already exist")
        staged = stage / name
        run(args.nfpm, "package", "--config", str(cfg), "--packager", args.format, "--target", str(staged),
            env={"PATH": os.environ.get("PATH", ""), "HOME": str(stage), "LC_ALL": "C",
                 "SOURCE_DATE_EPOCH": str(info["source_date_epoch"])})
        if not staged.is_file() or staged.stat().st_size == 0:
            raise ValueError("native package builder produced no package")
        published = []
        try:
            for destination, data in [(evidence, (json.dumps(info, indent=2, sort_keys=True) + "\n").encode()),
                                      (checksum, (hashlib.sha256(staged.read_bytes()).hexdigest() + "  " + name + "\n").encode())]:
                temp = stage / (destination.name + ".tmp")
                temp.write_bytes(data)
                os.link(temp, destination)
                published.append(destination)
            os.link(staged, final)  # Publish the package last, without overwriting a concurrent build.
        except BaseException:
            for path in published:
                path.unlink()
            raise
        return final


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive", type=Path, required=True)
    parser.add_argument("--sha256", required=True, help="SHA-256 from a trusted build record, not trust inferred from the archive")
    parser.add_argument("--revision", required=True)
    parser.add_argument("--component", choices=("core", "extensions", "cli", "web"), required=True)
    parser.add_argument("--format", choices=FORMATS, required=True)
    parser.add_argument("--release", type=int, default=1)
    parser.add_argument("--output", type=Path, default=Path("out/linux-packages"))
    parser.add_argument("--nfpm", default="nfpm", help="preinstalled, reviewed nFPM executable")
    args = parser.parse_args()
    try:
        print(build(args))
    except (OSError, ValueError, KeyError, TypeError, tarfile.TarError, subprocess.SubprocessError) as error:
        print(f"Linux packaging failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
