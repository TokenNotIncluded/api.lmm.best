from __future__ import annotations

import copy
import hashlib
import json
import os
from pathlib import Path
import sys
import tempfile
import time
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from dp30 import BASE_SHA, EvidenceError, loads, provisional_plan, verify, write_json
from workload import Limits, SSEParser, local_target, run_load, verify_permit
from fixture_server import CHUNKS, fixture


def dataset() -> tuple[dict, dict[str, list[dict]]]:
    """Synthetic bookkeeping inputs, NEVER measured deployment evidence."""
    run = "synthetic-verifier-fixture"
    digest = hashlib.sha256(b"x").hexdigest()
    manifest = {"schema_version": 1, "run_id": run, "source_sha": BASE_SHA,
                "scope": "verifier_fixture", "scenario": "normal_update",
                "environment": {"kind": "process_fixture", "nodes": [{"node_id": "fixture"}]},
                "expected_requests": 1, "wallet_account_id": "wallet", "balances_before": {"wallet": 100, "revenue": 0},
                "balances_after": {"wallet": 97, "revenue": 3}, "expected_request_wallet_delta": {"r1": -3},
                "expected_effects": {"op1": 1}, "capabilities": {},
                "account_revisions_before": {"wallet": 0, "revenue": 0}, "account_revisions_after": {"wallet": 1, "revenue": 1}}
    rows = {
        "requests": [{"run_id": run, "request_id": "r1", "attempt": 1, "outcome": "complete", "latency_ms": 10,
                      "sequence": [digest], "expected_sequence": [digest], "content_sha256": digest,
                      "expected_content_sha256": digest, "terminal_complete": True, "expected_upstream_calls": 1}],
        "upstream": [{"run_id": run, "request_id": "r1", "upstream_attempt_id": "u1"}],
        "ledger": [{"run_id": run, "request_id": "r1", "entry_id": "e1", "transaction_id": "t1", "account_id": "wallet", "delta_units": -3, "account_revision": 1, "balance_after_units": 97},
                   {"run_id": run, "request_id": "r1", "entry_id": "e2", "transaction_id": "t1", "account_id": "revenue", "delta_units": 3, "account_revision": 1, "balance_after_units": 3}],
        "tasks": [{"run_id": run, "operation_id": "op1", "effect_receipt_id": "effect1", "state": "effect_committed"}],
        "auth": [{"run_id": run, "credential_label": label, "phase": phase, "allowed": label != "revoked_key", "expected_allowed": label != "revoked_key"}
                 for label in ("session", "api_key", "revoked_key") for phase in ("before", "after")],
        "releases": [{"run_id": run, "round": i + 1, "component": ("go", "rust", "config", "frontend")[i % 4],
                      "clock": "controller_monotonic", "requested_ms": i * 100, "download_start_ms": i * 100 + 5,
                      "download_end_ms": i * 100 + 10, "ready_ms": i * 100 + 20, "cutover_ms": i * 100 + 21,
                      "old_stop_accept_ms": i * 100 + 22, "drained_ms": i * 100 + 50, "cleaned_ms": i * 100 + 55,
                      "artifact_digest": digest, "config_digest": digest, "frontend_digest": digest, "forced_kill": False}
                     for i in range(20)],
        "resources": [{"run_id": run, "node_id": "fixture", "round": i, "phase": "after_cleanup" if i == 20 else "during",
                       "memory_current_bytes": 10, "rss_bytes": 8, "virtual_bytes": 20, "shared_bytes": 1,
                       "page_cache_bytes": 1, "cpu_usage_usec": i * 100, "cpu_throttled_usec": 0, "connections": 1,
                       "old_processes": 0, "image_bytes": 10, "log_bytes": 1, "event_backlog": 0, "free_disk_bytes": 100,
                       "sample_elapsed_ms": i * 100} for i in range(21)],
    }
    return manifest, rows


def save_dataset(path: Path, manifest: dict, rows: dict[str, list[dict]]) -> None:
    path.mkdir(parents=True, exist_ok=True)
    write_json(path / "manifest.json", manifest)
    for name, data in rows.items():
        (path / (name + ".jsonl")).write_text("".join(json.dumps(row) + "\n" for row in data))


class VerifierTests(unittest.TestCase):
    def setUp(self):
        self.manifest, self.rows = dataset()

    def evaluate(self):
        # Optional evidence sink preserves EVERY evaluated mutation and result.
        evidence = os.getenv("DP30_TEST_EVIDENCE")
        with tempfile.TemporaryDirectory() as directory:
            path = Path(evidence) / self.id().split(".")[-1] if evidence else Path(directory)
            save_dataset(path, self.manifest, self.rows)
            try:
                result = verify(path)
            except Exception as exc:
                if evidence:
                    write_json(path / "observed.json", {"exception": type(exc).__name__, "message": str(exc)})
                raise
            if evidence:
                write_json(path / "observed.json", result)
            return result

    def assert_failed(self, name):
        result = self.evaluate()
        self.assertEqual(result["status"], "failed")
        self.assertTrue(any(c["name"] == name and c["status"] == "failed" for c in result["checks"]))

    def test_fixture_cannot_become_application_acceptance(self):
        result = self.evaluate()
        self.assertEqual(result["failed_checks"], 0)
        self.assertEqual(result["status"], "unverified")
        self.assertFalse(result["application_capacity_claim_allowed"])

    def test_missing_ledger_remains_unknown(self):
        del self.rows["ledger"]
        result = self.evaluate()
        self.assertTrue(any(c["name"] == "ledger_evidence" and c["status"] == "unverified" for c in result["checks"]))

    def test_empty_requests_do_not_pass(self):
        self.rows["requests"] = []
        self.assertEqual(self.evaluate()["status"], "unverified")

    def test_duplicate_request(self):
        self.rows["requests"].append(copy.deepcopy(self.rows["requests"][0]))
        self.assert_failed("request_coverage")

    def test_automatic_replay(self):
        self.rows["requests"][0]["attempt"] = 2
        self.assert_failed("no_automatic_replay")

    def test_terminal_frame_missing(self):
        self.rows["requests"][0]["terminal_complete"] = False
        self.assert_failed("stream_content")

    def test_reordered_content(self):
        self.rows["requests"][0]["sequence"] = ["wrong"]
        self.assert_failed("stream_content")

    def test_upstream_replayed(self):
        self.rows["upstream"].append({"run_id": self.manifest["run_id"], "request_id": "r1", "upstream_attempt_id": "u2"})
        self.assert_failed("upstream_call_count")

    def test_double_debit_even_when_double_entry_balances(self):
        for row in copy.deepcopy(self.rows["ledger"]):
            row["entry_id"] += "copy"
            row["transaction_id"] = "t2"
            self.rows["ledger"].append(row)
        self.manifest["balances_after"] = {"wallet": 94, "revenue": 6}
        self.assert_failed("exact_request_charges")

    def test_duplicate_ledger_rows(self):
        self.rows["ledger"].append(copy.deepcopy(self.rows["ledger"][0]))
        self.assert_failed("ledger_unique_entries")

    def test_money_float_forbidden(self):
        self.rows["ledger"][0]["delta_units"] = -3.0
        with self.assertRaises(EvidenceError):
            self.evaluate()

    def test_ledger_revision_gap_is_not_hidden_by_final_balance(self):
        self.rows["ledger"][0]["account_revision"] = 2
        self.manifest["account_revisions_after"]["wallet"] = 2
        self.assert_failed("ledger_account_revisions")

    def test_orphan_account_forbidden(self):
        self.rows["ledger"][0]["account_id"] = "other-account"
        with self.assertRaises(EvidenceError):
            self.evaluate()

    def test_duplicate_side_effect(self):
        row = copy.deepcopy(self.rows["tasks"][0])
        row["effect_receipt_id"] = "effect2"
        self.rows["tasks"].append(row)
        self.assert_failed("task_side_effects")

    def test_revoked_key_accepted(self):
        self.rows["auth"][-1]["allowed"] = True
        self.assert_failed("auth_results")

    def test_cutover_before_ready(self):
        self.rows["releases"][0]["cutover_ms"] = 1
        self.assert_failed("round_1_order")

    def test_normal_update_must_not_force_kill(self):
        self.rows["releases"][0]["forced_kill"] = True
        self.assert_failed("round_1_not_forced")

    def test_nineteen_is_not_twenty(self):
        self.rows["releases"].pop()
        self.assert_failed("twenty_rounds")

    def test_cross_node_clocks_not_subtracted(self):
        self.rows["releases"][0]["clock"] = "node_wall_clock"
        with self.assertRaises(EvidenceError):
            self.evaluate()

    def test_waiting_time_not_hidden(self):
        row = self.rows["releases"][0]
        for name in ("download_start_ms", "download_end_ms", "ready_ms", "cutover_ms", "old_stop_accept_ms", "drained_ms", "cleaned_ms"):
            row[name] += 1000
        result = self.evaluate()
        self.assertEqual(result["metrics"]["release_timings"][0]["queue_wait_ms"], 1005)

    def test_backlog_not_cleared(self):
        self.rows["resources"][-1]["event_backlog"] = 1
        self.assert_failed("drain_and_backlog_cleanup")

    def test_missing_resource_round(self):
        self.rows["resources"].pop(2)
        self.assert_failed("resource_round_coverage")

    def test_missing_recovery_is_not_zero(self):
        self.manifest["scenario"] = "database_unavailable"
        self.rows["faults"] = [{"run_id": self.manifest["run_id"], "fault": "database_unavailable", "clock": "controller_monotonic", "injected_ms": 1, "service_recovered_ms": None, "restored": True}]
        result = self.evaluate()
        self.assertNotIn("recovery_ms", result["metrics"])
        self.assertTrue(any(c["name"] == "fault_recovery" and c["status"] == "unverified" for c in result["checks"]))

    def test_cross_run_rows_rejected(self):
        self.rows["requests"][0]["run_id"] = "another-run"
        with self.assertRaises(EvidenceError):
            self.evaluate()


class ParserAndSafetyTests(unittest.TestCase):
    def test_complete_terminal_at_every_byte_boundary(self):
        event = b'data: {"choices":[{"delta":{"content":"ok"}}]}\n\ndata: [DONE]\n\n'
        for split in range(len(event) + 1):
            parser = SSEParser()
            parser.feed(event[:split])
            parser.feed(event[split:])
            parser.finish()
            self.assertEqual(parser.content, b"ok")

    def test_incomplete_terminal_never_flushed_at_eof(self):
        for terminal in (b"data: [DONE]", b"data: [DONE]\n", b"data: [DONE]\r\n"):
            parser = SSEParser()
            parser.feed(terminal)
            with self.assertRaises(EvidenceError):
                parser.finish()

    def test_crlf_and_multiline_data(self):
        parser = SSEParser()
        parser.feed(b'data: {"choices":\r\ndata: []}\r\n\r\ndata: [DONE]\r\n\r\n')
        parser.finish()

    def test_bytes_after_terminal_rejected(self):
        parser = SSEParser()
        parser.feed(b"data: [DONE]\n\n")
        with self.assertRaises(EvidenceError):
            parser.feed(b"data: [DONE]\n\n")

    def test_sse_event_bound(self):
        parser = SSEParser(32)
        with self.assertRaises(EvidenceError):
            parser.feed(b"data: " + b"x" * 33)

    def test_duplicate_json_and_nan_rejected(self):
        for text in ('{"x":1,"x":2}', '{"x":NaN}', '{"x":Infinity}'):
            with self.assertRaises(EvidenceError):
                loads(text)

    def test_non_loopback_and_credentials_rejected(self):
        for url in ("https://api.lmm.best", "http://192.0.2.1:18080", "http://localhost:18080", "http://u:p@127.0.0.1:18080", "http://127.0.0.1:80", "http://127.0.0.1:18080/?a=1"):
            with self.assertRaises(EvidenceError):
                local_target(url)

    def test_safety_limits_cannot_expand_unbounded(self):
        for limits in (Limits(requests=257), Limits(concurrency=17), Limits(max_seconds=61), Limits(requests_per_second=float("nan"))):
            with self.assertRaises(EvidenceError):
                limits.validate()

    def test_plan_does_not_invent_dp21_or_integration_sha(self):
        plan = provisional_plan()
        self.assertIsNone(plan["dp21_workload_sha256"])
        self.assertIsNone(plan["integration_sha"])
        group = next(g for g in plan["groups"] if g["name"] == "normal_update")
        self.assertEqual(len(group["rounds"]), 20)
        self.assertEqual(group["repeats"], 3)
        self.assertLess(1, plan["offered_load"]["stream_seconds"])

    def test_permit_rejects_an_unrelated_local_service(self):
        with fixture() as (origin, permit, _):
            permit["nonce"] = "0" * 32
            with self.assertRaises(EvidenceError):
                verify_permit(origin, permit)

    def test_no_redirect_followed(self):
        with fixture("redirect") as (origin, permit, receipts):
            result = run_load(origin, permit, "redirect", Limits(requests=1), CHUNKS)
            self.assertEqual(result["completed_requests"], 0)
            self.assertEqual(len(receipts), 1)
            self.assertEqual(result["requests"][0]["status_code"], 302)

    def test_test_key_is_not_saved_in_result(self):
        with fixture() as (origin, permit, _):
            result = run_load(origin, permit, "key", Limits(requests=1), CHUNKS, "dp30-unit-secret-not-a-real-key")
            self.assertEqual(result["completed_requests"], 1)
            self.assertNotIn("dp30-unit-secret-not-a-real-key", json.dumps(result))

    def test_credential_file_must_be_private(self):
        from workload import read_test_credential
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "credential"
            path.write_text("test-only-key\n")
            path.chmod(0o644)
            with self.assertRaises(EvidenceError):
                read_test_credential(path)
            path.chmod(0o600)
            self.assertEqual(read_test_credential(path), "test-only-key")

    def test_slow_headers_have_a_total_deadline(self):
        started = time.monotonic()
        with fixture("slow_headers") as (origin, permit, receipts):
            result = run_load(origin, permit, "slowheaders", Limits(requests=1, deadline_seconds=.08), CHUNKS)
            self.assertEqual(result["completed_requests"], 0)
            self.assertEqual(len(receipts), 1)
        self.assertLess(time.monotonic() - started, .5)

    def test_evidence_line_and_partial_write_are_bounded(self):
        from dp30 import read_rows, MAX_LINE_BYTES
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "rows.jsonl"
            path.write_bytes(b"{}")
            with self.assertRaises(EvidenceError):
                read_rows(path)
            path.write_bytes(b" " * MAX_LINE_BYTES + b"{}\n")
            with self.assertRaises(EvidenceError):
                read_rows(path)

    def test_overload_is_recorded_not_silently_delayed(self):
        with fixture("clean", .015) as (origin, permit, receipts):
            result = run_load(origin, permit, "shed", Limits(requests=8, concurrency=1, requests_per_second=100), CHUNKS)
            self.assertGreater(sum(r["outcome"] == "client_shed" for r in result["requests"]), 0)
            self.assertEqual(len(result["requests"]), 8)
            self.assertEqual(result["peak_active"], 1)
            self.assertLess(len(receipts), 8)


if __name__ == "__main__":
    unittest.main(verbosity=2)
