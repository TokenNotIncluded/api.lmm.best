# Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
import importlib.util
from pathlib import Path
import tempfile
import unittest
import sys
sys.dont_write_bytecode = True

spec = importlib.util.spec_from_file_location("prepare_ci_apt", Path(__file__).with_name("prepare-ci-apt.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class PreparationTests(unittest.TestCase):
    def test_only_unused_chrome_sources_are_disabled(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            ubuntu = "Types: deb\nURIs: https://archive.ubuntu.com/ubuntu\nSuites: noble\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n"
            pg = "deb [signed-by=/keys/postgresql.gpg] https://apt.postgresql.org/pub/repos/apt noble-pgdg main\n"
            (root / "ubuntu.sources").write_text(ubuntu)
            (root / "postgres.list").write_text(pg)
            (root / "chrome.list").write_text("deb [arch=amd64] https://dl.google.com/linux/chrome-stable/deb stable main\n")
            (root / "chrome.sources").write_text("Types: deb\nURIs: https://dl.google.com/linux/chrome/deb\nSuites: stable\nEnabled: yes\n")
            self.assertEqual(module.prepare(root), ["chrome.list", "chrome.sources"])
            self.assertEqual((root / "ubuntu.sources").read_text(), ubuntu)
            self.assertEqual((root / "postgres.list").read_text(), pg)
            self.assertTrue((root / "chrome.list").read_text().startswith("#"))
            self.assertIn("Enabled: no", (root / "chrome.sources").read_text())
            self.assertEqual(module.prepare(root), [])

    def test_mixed_sources_fail_without_changing_the_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "mixed.sources"
            content = "Types: deb\nURIs: https://dl.google.com/linux/chrome/deb https://archive.ubuntu.com/ubuntu\n"
            path.write_text(content)
            with self.assertRaises(ValueError): module.prepare(path.parent)
            self.assertEqual(path.read_text(), content)


if __name__ == "__main__": unittest.main()
