"""Verifier regression tests. All archives, resources and binaries are synthetic."""
from __future__ import annotations

import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import struct
import tarfile
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
SOURCE = Path(os.environ.get("LMM_TEST_DISTRIBUTION_SOURCE", ROOT / "scripts/build-distribution.py"))
SPEC = importlib.util.spec_from_file_location("distribution_verification", SOURCE)
DIST = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DIST)
REVISION = "a" * 40


def binary(platform: str, *, pe_offset: int = 128) -> bytes:
    system, arch = platform.split("-")
    data = bytearray(max(64, pe_offset + 6) if system == "windows" else 64)
    if system == "linux":
        data[:6] = b"\x7fELF\x02\x01"
        struct.pack_into("<H", data, 18, {"amd64": 62, "arm64": 183}[arch])
    elif system == "macos":
        data[:4] = b"\xcf\xfa\xed\xfe"
        struct.pack_into("<I", data, 4, {"amd64": 0x01000007, "arm64": 0x0100000C}[arch])
    else:
        data[:2] = b"MZ"
        struct.pack_into("<I", data, 60, pe_offset)
        data[pe_offset:pe_offset + 4] = b"PE\0\0"
        struct.pack_into("<H", data, pe_offset + 4, {"amd64": 0x8664, "arm64": 0xAA64}[arch])
    return bytes(data)


class VerificationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.catalog = json.loads((ROOT / "packaging/distribution.json").read_text())
        (self.root / "packaging").mkdir()
        (self.root / "packaging/distribution.json").write_text(json.dumps(self.catalog))
        self.addCleanup(patch.stopall)
        patch.object(DIST, "ROOT", self.root).start()
        self.counter = 0

    def files(self, component="core", platform="linux-amd64"):
        chosen = self.catalog["components"][component]
        files = {name: (b"synthetic resource\n", 0o644)
                 for name in self.catalog["common"] + chosen["resources"] + ["README.md"]}
        for name in chosen["binaries"]:
            suffix = ".exe" if platform.startswith("windows-") else ""
            files["bin/" + name + suffix] = (binary(platform), 0o755)
        if component == "web":
            files["dist/index.html"] = (b"<!doctype html><title>synthetic</title>", 0o644)
        return files

    def archive(self, files=None, *, component="core", platform="linux-amd64",
                manifest_edit=None, raw_edit=None, manifest_first=True, extra_entry=None):
        if files is None:
            files = self.files(component, platform)
        manifest = {"schema_version": 1, "component": component,
                    "package": self.catalog["components"][component]["package"],
                    "version": "0.1.0-preview", "revision": REVISION,
                    "platform": platform, "preview": True, "source_date_epoch": 0,
                    "files": {name: {"sha256": hashlib.sha256(data).hexdigest(),
                                     "size": len(data), "mode": mode}
                              for name, (data, mode) in files.items()}}
        if manifest_edit:
            manifest_edit(manifest)
        raw = json.dumps(manifest).encode()
        if raw_edit:
            raw = raw_edit(raw)
        members = list(files.items())
        members.insert(0 if manifest_first else len(members), ("MANIFEST.json", (raw, 0o644)))
        self.counter += 1
        path = self.root / f"fixture-{self.counter}.tar.gz"
        with tarfile.open(path, "w:gz") as archive:
            for name, (payload, mode) in members:
                info = tarfile.TarInfo(name)
                info.mode, info.size = mode, len(payload)
                archive.addfile(info, io.BytesIO(payload))
            if extra_entry:
                extra_entry(archive)
        return path

    def test_all_declared_platforms_and_manifest_positions(self):
        for component, chosen in self.catalog["components"].items():
            for platform in chosen["platforms"]:
                for first in (True, False):
                    with self.subTest(component=component, platform=platform, manifest_first=first):
                        archive = self.archive(component=component, platform=platform, manifest_first=first)
                        self.assertEqual(DIST.verify(archive, REVISION)["component"], component)

    def test_self_consistent_shell_binary_is_rejected(self):
        files = self.files()
        files["bin/lmm-core"] = (b"#!/bin/sh\nexit 0\n", 0o755)
        with self.assertRaisesRegex(ValueError, "binary"):
            DIST.verify(self.archive(files))

    def test_self_consistent_wrong_architecture_is_rejected(self):
        files = self.files()
        files["bin/lmm-core"] = (binary("linux-arm64"), 0o755)
        with self.assertRaisesRegex(ValueError, "binary"):
            DIST.verify(self.archive(files))

    def test_required_resources_cannot_be_deleted_from_both_inventories(self):
        for name in self.catalog["common"] + self.catalog["components"]["core"]["resources"] + ["README.md"]:
            with self.subTest(name=name):
                files = self.files()
                del files[name]
                with self.assertRaisesRegex(ValueError, "missing|inventory|resource"):
                    DIST.verify(self.archive(files))

    def test_self_consistent_unreviewed_resource_is_rejected(self):
        files = self.files()
        files["tools/unreviewed.txt"] = (b"not in reviewed component", 0o644)
        with self.assertRaisesRegex(ValueError, "unexpected|inventory|resource"):
            DIST.verify(self.archive(files))

    def test_web_requires_index(self):
        files = self.files("web", "any")
        del files["dist/index.html"]
        with self.assertRaisesRegex(ValueError, "index|missing|inventory"):
            DIST.verify(self.archive(files, component="web", platform="any"))

    def test_web_resources_must_stay_in_dist(self):
        files = self.files("web", "any")
        files["other/asset.txt"] = (b"synthetic", 0o644)
        with self.assertRaisesRegex(ValueError, "unexpected|inventory|resource"):
            DIST.verify(self.archive(files, component="web", platform="any"))

    def test_web_public_discovery_and_large_asset_are_valid(self):
        files = self.files("web", "any")
        files["dist/.well-known/webmcp.json"] = (b"{}", 0o644)
        files["dist/asset.bin"] = (b"x" * (2 * 1024 * 1024 + 7), 0o644)
        self.assertEqual(DIST.verify(self.archive(files, component="web", platform="any"))["component"], "web")

    def test_epoch_is_an_integer_in_range(self):
        for epoch in (-1, True, "0", 2**32):
            with self.subTest(epoch=epoch), self.assertRaises(ValueError):
                DIST.verify(self.archive(manifest_edit=lambda m: m.update(source_date_epoch=epoch)))

    def test_duplicate_json_keys_are_rejected(self):
        def duplicate(raw):
            return b'{"revision":"' + b"b" * 40 + b'",' + raw[1:]
        with self.assertRaisesRegex(ValueError, "duplicate"):
            DIST.verify(self.archive(raw_edit=duplicate))

    def test_nested_duplicate_json_keys_are_rejected(self):
        def duplicate(raw):
            return raw.replace(b'"size":', b'"size": 0, "size":', 1)
        with self.assertRaisesRegex(ValueError, "duplicate"):
            DIST.verify(self.archive(raw_edit=duplicate))

    def test_boolean_file_size_is_not_an_integer(self):
        files = self.files()
        files["README.md"] = (b"x", 0o644)
        def edit(manifest):
            manifest["files"]["README.md"]["size"] = True
        with self.assertRaises(ValueError):
            DIST.verify(self.archive(files, manifest_edit=edit))

    def test_manifest_read_is_bounded(self):
        archive = self.archive()
        with patch.object(DIST, "MAX_MANIFEST", 32, create=True), self.assertRaisesRegex(ValueError, "manifest"):
            DIST.verify(archive)

    def test_tar_metadata_is_bounded_before_member_iteration(self):
        def oversized_metadata(archive):
            info = tarfile.TarInfo("asset.txt")
            info.mode, info.size = 0o644, 1
            info.pax_headers = {"comment": "x" * 1024}
            archive.addfile(info, io.BytesIO(b"x"))
        archive = self.archive(extra_entry=oversized_metadata)
        with patch.object(DIST, "MAX_METADATA", 32, create=True):
            with self.assertRaisesRegex(ValueError, "metadata"):
                DIST.verify(archive)

    def test_nested_metadata_chain_is_bounded(self):
        def chain(archive):
            record = b"13 comment=x\n"
            for _ in range(17):
                info = tarfile.TarInfo("metadata")
                info.type, info.size = tarfile.XHDTYPE, len(record)
                archive.addfile(info, io.BytesIO(record))
            info = tarfile.TarInfo("asset.txt")
            info.mode, info.size = 0o644, 1
            archive.addfile(info, io.BytesIO(b"x"))
        with self.assertRaisesRegex(ValueError, "metadata"):
            DIST.verify(self.archive(extra_entry=chain))

    def test_sparse_pax_extensions_are_rejected(self):
        def sparse(archive):
            info = tarfile.TarInfo("asset.txt")
            info.mode, info.size = 0o644, 1
            info.pax_headers = {"GNU.sparse.map": "0,1"}
            archive.addfile(info, io.BytesIO(b"x"))
        with self.assertRaisesRegex(ValueError, "sparse"):
            DIST.verify(self.archive(extra_entry=sparse))

    def test_payload_reads_are_bounded(self):
        archive = self.archive()
        original = tarfile.ExFileObject.read
        def bounded(source, size=-1):
            self.assertGreater(size, 0, "unbounded archive payload read")
            self.assertLessEqual(size, 8 * 1024 * 1024 + 1)
            return original(source, size)
        with patch.object(tarfile.ExFileObject, "read", bounded):
            DIST.verify(archive)

    def test_windows_header_on_a_chunk_boundary_and_at_large_offset(self):
        for offset in (64, 1024 * 1024 - 3, 2 * 1024 * 1024 + 7):
            for arch in ("amd64", "arm64"):
                with self.subTest(offset=offset, arch=arch):
                    platform = "windows-" + arch
                    files = self.files("cli", platform)
                    files["bin/lmm.exe"] = (binary(platform, pe_offset=offset), 0o755)
                    DIST.verify(self.archive(files, component="cli", platform=platform))

    def test_windows_header_out_of_bounds_is_rejected(self):
        for offset in (0, 63, 2**32 - 1):
            with self.subTest(offset=offset):
                files = self.files("cli", "windows-amd64")
                data = bytearray(binary("windows-amd64"))
                struct.pack_into("<I", data, 60, offset)
                files["bin/lmm.exe"] = (bytes(data), 0o755)
                with self.assertRaisesRegex(ValueError, "binary"):
                    DIST.verify(self.archive(files, component="cli", platform="windows-amd64"))

    def test_wrong_windows_and_macos_architecture_is_rejected(self):
        for system in ("windows", "macos"):
            with self.subTest(system=system):
                platform = system + "-amd64"
                files = self.files("cli", platform)
                name = "bin/lmm.exe" if system == "windows" else "bin/lmm"
                files[name] = (binary(system + "-arm64"), 0o755)
                with self.assertRaisesRegex(ValueError, "binary"):
                    DIST.verify(self.archive(files, component="cli", platform=platform))

    def test_wrong_revision_and_payload_digest_are_rejected(self):
        with self.assertRaises(ValueError):
            DIST.verify(self.archive(), "b" * 40)
        def edit(manifest):
            manifest["files"]["README.md"]["sha256"] = "0" * 64
        with self.assertRaises(ValueError):
            DIST.verify(self.archive(manifest_edit=edit))

    def test_paths_links_duplicate_members_and_extra_executables_are_rejected(self):
        for name in ("../escape", "/absolute", "a//b", "private.key", "a\\b"):
            with self.subTest(name=name):
                files = self.files()
                files[name] = (b"synthetic", 0o644)
                with self.assertRaises(ValueError):
                    DIST.verify(self.archive(files))
        def link(archive):
            info = tarfile.TarInfo("link")
            info.type, info.linkname = tarfile.SYMTYPE, "/outside"
            archive.addfile(info)
        def duplicate(archive):
            info = tarfile.TarInfo("README.md")
            info.mode, info.size = 0o644, 1
            archive.addfile(info, io.BytesIO(b"x"))
        for extra in (link, duplicate):
            with self.assertRaises(ValueError):
                DIST.verify(self.archive(extra_entry=extra))
        files = self.files()
        files["bin/fourth-product"] = (binary("linux-amd64"), 0o755)
        with self.assertRaisesRegex(ValueError, "executable"):
            DIST.verify(self.archive(files))

    def test_file_count_and_total_bytes_are_bounded(self):
        archive = self.archive()
        with patch.object(DIST, "MAX_FILES", 2), self.assertRaisesRegex(ValueError, "limits"):
            DIST.verify(archive)
        with patch.object(DIST, "MAX_TOTAL", 32), self.assertRaisesRegex(ValueError, "limits"):
            DIST.verify(archive)
        with patch.object(DIST, "MAX_FILE", 32), self.assertRaisesRegex(ValueError, "member"):
            DIST.verify(archive)

    def test_synthetic_build_verify_round_trip_and_reproducibility(self):
        # The real builder runs, but these are synthetic resources and headers.
        for name in self.catalog["common"] + self.catalog["components"]["core"]["resources"]:
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b"synthetic resource\n")
        inputs = self.root / "inputs"
        inputs.mkdir()
        (inputs / "lmm-core").write_bytes(binary("linux-amd64"))
        args = dict(root=self.root, component="core", version="0.1.0-preview",
                    revision=REVISION, platform="linux-amd64", inputs=inputs)
        a = DIST.build(**args, output=self.root / "one")
        b = DIST.build(**args, output=self.root / "two")
        self.assertEqual(a.read_bytes(), b.read_bytes())
        self.assertEqual(DIST.verify(a, REVISION)["component"], "core")
        with self.assertRaises(FileExistsError):
            DIST.build(**args, output=self.root / "one")


if __name__ == "__main__":
    unittest.main()
