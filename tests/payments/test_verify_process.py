"""Offline provenance rejection tests; no Docker, database or product process."""
import copy
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


class CaseSelectionTests(unittest.TestCase):
    def test_fixed_selection(self):
        command, timeout = v.selected_command("/test", "commit-before-persist")
        self.assertEqual(command, ["/test", v.CASE, "--exact", "--nocapture", "--test-threads=1"])
        self.assertEqual(timeout, 180)
        command, timeout = v.selected_command("/test", "accepted-proof-expiry")
        self.assertEqual(command, ["/test", v.EXPIRY_CASE, "--exact", "--nocapture", "--test-threads=1", "--ignored"])
        self.assertEqual(timeout, 480)

    def test_unknown_case(self):
        with self.assertRaises(v.FixtureError): v.selected_command("/test", "anything")

    def test_exact_discovery(self):
        full = f"{v.CASE}: test\n{v.EXPIRY_CASE}: test\n\n2 tests, 0 benchmarks\n"
        ignored = f"{v.EXPIRY_CASE}: test\n\n1 test, 0 benchmarks\n"
        v.verify_discovery(full, ignored)
        for bad_full, bad_ignored in (
            (full.replace(v.CASE + ": test\n", ""), ignored),
            (full.replace("2 tests", "3 tests"), ignored),
            (full.replace("2 tests", "extra: test\n2 tests"), ignored),
            (full.replace(v.EXPIRY_CASE, v.CASE), ignored),
            (full, ignored.replace(v.EXPIRY_CASE, v.CASE)),
            (full, "0 tests, 0 benchmarks\n"),
            (full, ignored + "extra: test\n"),
        ):
            with self.subTest(full=bad_full, ignored=bad_ignored), self.assertRaises(v.FixtureError):
                v.verify_discovery(bad_full, bad_ignored)

    def test_exact_execution_summary(self):
        log = "test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 1 filtered out; finished in 302.01s\n"
        for case in v.CASES: v.verify_execution(log, case)
        for bad in (log.replace("1 passed", "0 passed"), log.replace("0 ignored", "1 ignored"),
                    log.replace("1 filtered", "0 filtered"), log.replace("0 failed", "1 failed"),
                    log + log, log.replace("ok.", "FAILED."), ""):
            with self.subTest(log=bad), self.assertRaises(v.FixtureError):
                v.verify_execution(bad, "accepted-proof-expiry")


class ExpiryEvidenceTests(unittest.TestCase):
    def setUp(self):
        proofs = [{"id": "proof_" + digit * 64, "event_id": "evt_" + digit,
                   "payload_hash": [int(digit)] * 32, "signed_payload_sha256": "a" * 64,
                   "signature_sha256": "b" * 64, "receipt_wire_sha256": digit * 64} for digit in ("1", "2")]
        expiry = {"monotonic_wait_ms": 302000, "signature_age_seconds": 303,
                  "zero_credit_before_restart": True, "fresh_unaccepted_stale_proof_denied": True,
                  "accepted_stale_accept_also_denied": True, "durable_get_original_wire_control": True,
                  "secret_unchanged": True, "original_payload_sha256": "c" * 64,
                  "original_proof_wire_sha256": "1" * 64, "proof_manifest": proofs, "observations": 62}
        self.result = {"result": "PASS", "case": "accepted-proof-expiry", "products_reaped": True,
                       "core_os_restart": True, "go_main_worker_recovery": True, "credit_exactly_once": True,
                       "original_jobs_preserved": True, "full_payment_acceptance": False,
                       "product_pids": [101, 102, 103, 104], "expiry": expiry,
                       "first_credit_after_both_restarted_ready": True}
        self.main = {"expiry": copy.deepcopy(expiry), "first_credit_after_both_restarted_ready": True,
                     "core_pid": 103, "go_pid": 104}
        owner = {"owner_backend_pid": 77, "owner_xid": "88", "transaction_alive": True,
                 "exclusive_xid_lock": True, "row_share_relation_lock": True, "order_xmax_matches_owner_xid": True}
        self.evidence = {"expiry_before": copy.deepcopy(expiry),
                         "ready": {"core_pid": 103, "go_pid": 104, "zero_credit": True, "original_job_snapshot_unchanged": True},
                         "observations": [{"elapsed_ms": ms, "original_core_pid": 101, "original_go_pid": 102,
                                           "credit_receipts": 0, "journals": 0, "wallet": 0,
                                           "proof_manifest": copy.deepcopy(proofs), "go_snapshot_sha256": "d" * 64,
                                           "original_payload_sha256": "c" * 64, "owner_lock": dict(owner)}
                                          for ms in [*range(0, 301000, 5000), 302000]]}

    def verify(self):
        v.verify_case_result(self.result, self.main, "accepted-proof-expiry", **self.evidence)

    def test_complete_evidence(self):
        self.verify()

    def test_fast_case_has_separate_scope(self):
        result = {key: value for key, value in self.result.items() if key != "expiry"}
        result["case"] = "commit-before-persist"
        v.verify_case_result(result, {}, "commit-before-persist")
        with self.assertRaises(v.FixtureError):
            v.verify_case_result(result, self.main, "commit-before-persist")

    def test_nonobject_evidence(self):
        for value in (None, [], "PASS"):
            with self.subTest(value=value), self.assertRaises(v.FixtureError):
                v.verify_case_result(value, self.main, "accepted-proof-expiry")

    def test_missing_recovery_invariants(self):
        original = copy.deepcopy(self.result)
        for field in ("products_reaped", "core_os_restart", "go_main_worker_recovery", "credit_exactly_once", "original_jobs_preserved"):
            self.result = copy.deepcopy(original)
            self.result[field] = 1
            with self.subTest(field=field), self.assertRaises(v.FixtureError): self.verify()

    def test_wrong_case(self):
        self.result["case"] = "commit-before-persist"
        with self.assertRaises(v.FixtureError): self.verify()

    def test_wait_and_age_are_strict_integers(self):
        original = copy.deepcopy(self.result)
        for field, values in (("monotonic_wait_ms", (301999, True, 302000.0, "302000", None)),
                              ("signature_age_seconds", (300, True, 303.0, "303", None))):
            for value in values:
                self.result = copy.deepcopy(original)
                self.result["expiry"][field] = value
                with self.subTest(field=field, value=value), self.assertRaises(v.FixtureError): self.verify()

    def test_missing_or_false_expiry_invariants(self):
        original = copy.deepcopy(self.result)
        for field in ("zero_credit_before_restart", "fresh_unaccepted_stale_proof_denied",
                      "accepted_stale_accept_also_denied", "durable_get_original_wire_control", "secret_unchanged"):
            for value in (False, 1, "true", None):
                self.result = copy.deepcopy(original)
                self.result["expiry"][field] = value
                with self.subTest(field=field, value=value), self.assertRaises(v.FixtureError): self.verify()

    def test_changed_cross_restart_evidence(self):
        self.evidence["expiry_before"]["signature_age_seconds"] += 1
        with self.assertRaises(v.FixtureError): self.verify()

    def test_missing_readiness_boundary(self):
        self.evidence["ready"]["zero_credit"] = False
        with self.assertRaises(v.FixtureError): self.verify()

    def test_cross_stage_json_type_changes(self):
        original = copy.deepcopy(self.evidence)
        for field, value in (("secret_unchanged", 1), ("monotonic_wait_ms", 302000.0)):
            self.evidence = copy.deepcopy(original)
            self.evidence["expiry_before"][field] = value
            with self.subTest(field=field), self.assertRaises(v.FixtureError): self.verify()
        self.evidence = original
        self.main["expiry"]["secret_unchanged"] = 1
        with self.assertRaises(v.FixtureError): self.verify()

    def test_floating_point_pids(self):
        ready, main, observations = copy.deepcopy((self.evidence["ready"], self.main, self.evidence["observations"]))
        for stage in ("ready", "main", "observation"):
            self.evidence["ready"], self.main, self.evidence["observations"] = copy.deepcopy((ready, main, observations))
            if stage == "observation":
                self.evidence["observations"][20]["original_core_pid"] = 101.0
            elif stage == "ready":
                self.evidence["ready"]["core_pid"] = 103.0
            else:
                self.main["core_pid"] = 103.0
            with self.subTest(stage=stage), self.assertRaises(v.FixtureError): self.verify()

    def test_wait_proof_json_type_changes(self):
        self.evidence["observations"][20]["proof_manifest"][0]["payload_hash"][0] = 1.0
        with self.assertRaises(v.FixtureError): self.verify()

    def test_wrong_new_pid(self):
        self.evidence["ready"]["go_pid"] = 102
        with self.assertRaises(v.FixtureError): self.verify()

    def test_duplicate_product_pid(self):
        self.result["product_pids"][3] = 102
        with self.assertRaises(v.FixtureError): self.verify()

    def test_missing_wait_sample(self):
        del self.evidence["observations"][20]
        with self.assertRaises(v.FixtureError): self.verify()

    def test_wait_sampling_gap(self):
        self.evidence["observations"][20]["elapsed_ms"] += 2000
        with self.assertRaises(v.FixtureError): self.verify()

    def test_money_during_wait(self):
        self.evidence["observations"][20]["credit_receipts"] = 1
        with self.assertRaises(v.FixtureError): self.verify()

    def test_missing_owner_lock(self):
        self.evidence["observations"][20]["owner_lock"]["order_xmax_matches_owner_xid"] = False
        with self.assertRaises(v.FixtureError): self.verify()

    def test_changed_owner_transaction(self):
        self.evidence["observations"][20]["owner_lock"]["owner_xid"] = "89"
        with self.assertRaises(v.FixtureError): self.verify()

    def test_changed_original_job(self):
        self.evidence["observations"][20]["go_snapshot_sha256"] = "e" * 64
        with self.assertRaises(v.FixtureError): self.verify()

    def test_changed_original_proof(self):
        self.evidence["observations"][20]["proof_manifest"][0]["signature_sha256"] = "e" * 64
        with self.assertRaises(v.FixtureError): self.verify()

    def test_duplicate_accepted_proof(self):
        self.result["expiry"]["proof_manifest"][1] = self.result["expiry"]["proof_manifest"][0]
        with self.assertRaises(v.FixtureError): self.verify()

    def test_wrong_original_wire_hash(self):
        self.result["expiry"]["original_proof_wire_sha256"] = "e" * 64
        with self.assertRaises(v.FixtureError): self.verify()


if __name__ == "__main__":
    unittest.main()
