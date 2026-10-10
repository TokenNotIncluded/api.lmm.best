"""Archive tests use synthetic ELF fixtures, not claims of real binary builds."""
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import shutil
import struct
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("distribution", ROOT / "scripts/build-distribution.py")
DIST = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DIST)
SHA = "a" * 40


def elf(machine=62):
    data = bytearray(64)
    data[:6] = b"\x7fELF\x02\x01"
    struct.pack_into("<H", data, 18, machine)
    return bytes(data)


class DistributionTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.inputs = self.root / "inputs"
        self.inputs.mkdir()
        for name in ("lmm-core", "lmm-core-admin", "lmm-extensions", "lmm"):
            (self.inputs / name).write_bytes(elf())
        (self.inputs / "index.html").write_text("<!doctype html><title>Test fixture</title>")

    def build(self, component="extensions", **changes):
        args = dict(root=ROOT, component=component, version="0.1.0-preview", revision=SHA,
                    platform="any" if component == "web" else "linux-amd64",
                    inputs=self.inputs, output=self.root / "output", epoch=0)
        args.update(changes)
        return DIST.build(**args)

    def test_each_component_has_exact_binaries_and_licenses(self):
        for component, expected in [("core", 2), ("extensions", 1), ("cli", 1), ("web", 0)]:
            with self.subTest(component=component):
                archive = self.build(component)
                manifest = DIST.verify(archive, SHA)
                files = manifest["files"]
                self.assertEqual(sum(name.startswith("bin/") for name in files), expected)
                for name in ("LICENSE", "NOTICE", "THIRD-PARTY-LICENSES.md"):
                    self.assertIn(name, files)
                self.assertNotIn("lmm-api-web.install", files)
                checksum = archive.with_name(archive.name + ".sha256").read_text()
                self.assertEqual(checksum.split()[0], hashlib.sha256(archive.read_bytes()).hexdigest())

    def test_reproducible_archive_and_contents(self):
        first = self.build(output=self.root / "first")
        second = self.build(output=self.root / "second")
        self.assertEqual(first.read_bytes(), second.read_bytes())
        with tarfile.open(first) as archive:
            config = json.load(archive.extractfile("config/legal/site.example.json"))
            self.assertFalse(config["published"])
            self.assertTrue(all(value == "" for value in config["variables"].values()))
        self.assertNotIn("config/legal/site.json", DIST.verify(first)["files"])

    def test_no_overwrite(self):
        first = self.build()
        original = first.read_bytes()
        with self.assertRaises(FileExistsError):
            self.build()
        self.assertEqual(first.read_bytes(), original)

    def test_missing_binary_and_wrong_architecture(self):
        (self.inputs / "lmm-core-admin").unlink()
        with self.assertRaises(OSError):
            self.build("core")
        (self.inputs / "lmm-extensions").write_bytes(elf(183))
        with self.assertRaises(ValueError):
            self.build()

    def test_symlink_input_rejected(self):
        (self.inputs / "lmm-extensions").unlink()
        (self.inputs / "lmm-extensions").symlink_to(self.inputs / "lmm")
        with self.assertRaises(ValueError):
            self.build()

    def test_web_rejects_secrets_and_links(self):
        for name in (".env", "private.key", "site.json", "symlink"):
            with self.subTest(name=name):
                path = self.inputs / name
                if name == "symlink":
                    path.symlink_to(self.inputs / "index.html")
                else:
                    path.write_text("not for distribution")
                with self.assertRaises(ValueError):
                    self.build("web")
                path.unlink()

    def test_public_discovery_is_preserved_without_allowing_hidden_files(self):
        public = self.inputs / ".well-known"
        public.mkdir()
        source = ROOT / "apps/web/public/.well-known/webmcp.json"
        shutil.copyfile(source, public / "webmcp.json")
        archive = self.build("web", output=self.root / "discovery")
        manifest = DIST.verify(archive, SHA)
        self.assertIn("dist/.well-known/webmcp.json", manifest["files"])
        for name in (".well-known/.env", ".well-known/private.json",
                     "dist/.well-known/.env", "dist/.well-known/private.json",
                     "config/.well-known/webmcp.json"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                DIST.safe_path(name)
        (public / "private.json").write_text("not public")
        with self.assertRaises(ValueError):
            self.build("web", output=self.root / "private")

    def test_identity_validation(self):
        for changes in ({"version":"../../x"}, {"revision":"short"}, {"platform":"any"}, {"epoch":-1}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                self.build(**changes)
        with self.assertRaises(ValueError):
            DIST.verify(self.build(), "b" * 40)

    def test_unsafe_paths(self):
        for name in ("/abs", "../escape", "a/../escape", "a//b", "a\\b", ".env", "a/.secrets/key", "a\nb", "node_modules/x"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                DIST.safe_path(name)

    def test_tamper_extra_files_and_links_rejected(self):
        original = self.build()
        for change in ("tamper", "extra", "link", "duplicate", "traversal"):
            path = self.root / (change + ".tar.gz")
            with tarfile.open(original) as source, tarfile.open(path, "w:gz") as target:
                for entry in source:
                    data = source.extractfile(entry).read()
                    if change == "tamper" and entry.name == "README.md":
                        data = b"x" * len(data)
                    target.addfile(entry, io.BytesIO(data))
                if change in {"extra", "duplicate", "traversal"}:
                    entry = tarfile.TarInfo({"extra":"extra", "duplicate":"README.md", "traversal":"../escape"}[change])
                    entry.mode, entry.size = 0o644, 1
                    target.addfile(entry, io.BytesIO(b"x"))
                elif change == "link":
                    entry = tarfile.TarInfo("linked")
                    entry.type, entry.linkname = tarfile.SYMTYPE, "/etc/passwd"
                    target.addfile(entry)
            with self.subTest(change=change), self.assertRaises(ValueError):
                DIST.verify(path)

    def test_resource_manifest_only_contains_existing_reviewed_files(self):
        spec = json.loads((ROOT / "packaging/distribution.json").read_text())
        for component in spec["components"].values():
            for name in component["resources"] + spec["common"]:
                self.assertTrue((ROOT / name).is_file(), name)
                self.assertFalse((ROOT / name).is_symlink(), name)
        resources = spec["components"]["extensions"]["resources"]
        self.assertEqual(sum(name.startswith("config/legal/templates/") for name in resources), 6)


if __name__ == "__main__":
    unittest.main()
