#!/usr/bin/env python3
"""Regression checks for the source inventory used to guard compiled CI filters."""
import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("inventory", Path(__file__).with_name("ignored-test-inventory.py"))
inventory_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(inventory_module)


class Inventory(unittest.TestCase):
    def test_declared_path_and_default_nested_modules_are_qualified(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "target.rs").write_text('#[tokio::test]\n#[ignore = "PG"]\nasync fn base() {}\n#[path = "children/payment.rs"]\nmod stripe_wallet;\nmod standard;\n')
            (root / "children").mkdir()
            (root / "children/payment.rs").write_text('#[test]\n#[ignore]\nfn nested() {}\n#[tokio::test]\nasync fn ordinary() {}\nmod deeper;\n')
            (root / "children/payment").mkdir()
            (root / "children/payment/deeper.rs").write_text('#[tokio::test(flavor = "multi_thread")]\n#[ignore = "PG"]\nasync fn funds() {}\n')
            (root / "standard").mkdir()
            (root / "standard/mod.rs").write_text('#[test]\n#[ignore]\nfn standard() {}\n')
            # An undeclared sibling is not part of the target's inventory.
            (root / "undeclared.rs").write_text('#[test]\n#[ignore]\nfn misleading() {}\n')
            names, files = inventory_module.inventory(root / "target.rs")
            self.assertEqual(names, ["base", "standard::standard", "stripe_wallet::deeper::funds", "stripe_wallet::nested"])
            self.assertEqual(len(files), 4)

    def test_missing_or_cyclic_declared_modules_fail_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "target.rs"
            source.write_text('mod missing;\n')
            with self.assertRaises(OSError):
                inventory_module.inventory(source)
            source.write_text('#[path = "target.rs"]\nmod recursive;\n')
            with self.assertRaisesRegex(ValueError, "cyclic"):
                inventory_module.inventory(source)

    def test_real_stripe_submodule_remains_in_the_payment_inventory(self):
        source = Path(__file__).resolve().parent.parent / "epay_runtime_postgres.rs"
        names, files = inventory_module.inventory(source)
        self.assertEqual(len([name for name in names if name.startswith("stripe_wallet::")]), 12)
        self.assertEqual(len([name for name in names if not name.startswith("stripe_wallet::")]), 12)
        self.assertTrue(any(path.name == "stripe_wallet.rs" for path in files))


if __name__ == "__main__":
    unittest.main()
