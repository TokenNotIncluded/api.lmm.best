"""Exercise real nFPM encoders with synthetic ELF fixtures; never install them."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
from types import SimpleNamespace
import unittest

import test_build as fixtures

PKG = fixtures.PKG


class NativePackageTests(fixtures.PackageTests):
    def native(self, archive, fmt, output, component="core"):
        return PKG.build(SimpleNamespace(archive=archive,
            sha256=hashlib.sha256(archive.read_bytes()).hexdigest(), revision=fixtures.REVISION,
            component=component, format=fmt, release=1, output=output,
            nfpm=os.environ.get("NFPM_BIN", "nfpm")), self.root)

    def test_native_formats_are_reproducible_and_cannot_overwrite(self):
        archive = self.archive()
        for fmt in PKG.FORMATS:
            with self.subTest(fmt=fmt):
                first = self.native(archive, fmt, self.work / (fmt + "-first"))
                second = self.native(archive, fmt, self.work / (fmt + "-second"))
                before = first.read_bytes()
                self.assertEqual(before, second.read_bytes())
                with self.assertRaises(FileExistsError):
                    self.native(archive, fmt, first.parent)
                self.assertEqual(before, first.read_bytes())
                info = json.loads(Path(str(first) + ".buildinfo.json").read_text())
                self.assertEqual(info["source_revision"], fixtures.REVISION)
                self.assertEqual(info["format"], fmt)

    def test_real_deb_metadata_payload_modes_and_config_registration(self):
        package = self.native(self.archive(arch=self.host_arch), "deb", self.work / "deb")
        version = subprocess.check_output(["dpkg-deb", "-f", str(package), "Version"], text=True).strip()
        self.assertEqual(version, "0.1.0~preview.1-1")
        subprocess.run(["dpkg", "--compare-versions", version, "lt", "0.1.0-1"], check=True)
        target = self.work / "unpacked"
        subprocess.run(["dpkg-deb", "--raw-extract", str(package), str(target)], check=True)
        self.assertIn("/etc/lmm-core/service.env", (target / "DEBIAN/conffiles").read_text())
        self.assertEqual((target / "etc/lmm-core/service.env").stat().st_mode & 0o777, 0o600)
        info = json.loads((target / "usr/share/doc/lmm-core/BUILDINFO.json").read_text())
        for path, expected in info["files"].items():
            file = target / path.lstrip("/")
            self.assertEqual(hashlib.sha256(file.read_bytes()).hexdigest(), expected["sha256"], path)
        self.assertFalse((target / "DEBIAN/postinst").exists())
        self.assertFalse((target / "DEBIAN/prerm").exists())
        subprocess.run([str(target / "usr/bin/lmm-core")], check=True, stdout=subprocess.PIPE)

    def test_noarch_web_builds_in_every_format(self):
        archive = self.archive("web")
        for fmt in PKG.FORMATS:
            with self.subTest(fmt=fmt):
                package = self.native(archive, fmt, self.work / (fmt + "-web"), "web")
                self.assertGreater(package.stat().st_size, 0)


if __name__ == "__main__":
    unittest.main()
