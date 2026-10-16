"""Offline provenance rejection tests; no Docker, database or product process."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("payment_verifier_under_test", Path(__file__).with_name("verify_process.py"))
v = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(v)


class ReceiptBindingTests(unittest.TestCase):
    def setUp(self):
        self.core = {"exit_code": 0, "source_drift": [], "binary_sha256": "a" * 64, "source_binding_sha256": "b" * 64}
        self.go = {"result": "PASS", "extension_sha256": "c" * 64}
        self.expected = dict(core_sha="a" * 64, binding_sha="b" * 64, go_sha="c" * 64)

    def verify(self, core=None, go=None):
        v.verify_build_receipts(self.core if core is None else core, self.go if go is None else go, **self.expected)

    def test_matching_receipts(self):
        self.verify()

    def test_wrong_core_digest(self):
        wrong = dict(self.core, binary_sha256="d" * 64)
        with self.assertRaises(v.FixtureError): self.verify(core=wrong)

    def test_wrong_binding_digest(self):
        wrong = dict(self.core, source_binding_sha256="d" * 64)
        with self.assertRaises(v.FixtureError): self.verify(core=wrong)

    def test_failed_core_build(self):
        with self.assertRaises(v.FixtureError): self.verify(core=dict(self.core, exit_code=1))

    def test_noninteger_exit_codes(self):
        for value in (False, 0.0, "0", None):
            with self.subTest(value=value), self.assertRaises(v.FixtureError):
                self.verify(core=dict(self.core, exit_code=value))

    def test_core_source_drift(self):
        with self.assertRaises(v.FixtureError): self.verify(core=dict(self.core, source_drift=["services/core/src/main.rs"]))

    def test_go_digest_mismatch(self):
        with self.assertRaises(v.FixtureError): self.verify(go=dict(self.go, extension_sha256="d" * 64))

    def test_go_nonpass(self):
        with self.assertRaises(v.FixtureError): self.verify(go=dict(self.go, result="FAIL"))

    def test_missing_binding_fields(self):
        with self.assertRaises(v.FixtureError): self.verify(core={})
        with self.assertRaises(v.FixtureError): self.verify(go={})

    def test_product_source_drift(self):
        current = {"services/core/src/main.rs": "a", "services/extensions/internal/example.go": "b"}
        with tempfile.TemporaryDirectory(prefix="payment-binding-test-") as directory:
            core_path, go_path = Path(directory) / "core.json", Path(directory) / "go.json"
            core_path.write_text(json.dumps({"files": current}))
            go_path.write_text(json.dumps({"source_sha256": current}))
            with patch.object(v, "sources", return_value=current), patch.object(v, "Fixture") as fixture:
                v.product_bindings(core_path, go_path)
                drifted = dict(current, **{"services/core/src/main.rs": "changed"})
                core_path.write_text(json.dumps({"files": drifted}))
                with self.assertRaises(v.FixtureError): v.product_bindings(core_path, go_path)
                fixture.assert_not_called()


if __name__ == "__main__":
    unittest.main()
