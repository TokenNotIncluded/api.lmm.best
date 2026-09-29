"""Reject misleading benchmark wins caused by errors or incorrect response bodies."""

import importlib.util
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
import unittest

import relay_workload

DIRECTORY = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("compare_backends", DIRECTORY / "compare_backends.py")
compare = importlib.util.module_from_spec(spec)
spec.loader.exec_module(compare)


class BenchmarkEvidenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.runtime = tempfile.TemporaryDirectory(prefix="lmm-benchmark-test-")
        cls.driver = Path(cls.runtime.name) / "http-load"
        subprocess.run(["go", "build", "-o", str(cls.driver), str(DIRECTORY / "http_load.go")],
                       check=True, timeout=60, env=dict(os.environ, GOMAXPROCS="2"))
        cls.expected = Path(cls.runtime.name) / "expected.json"
        cls.expected.write_text('{"success":true,"data":"same"}')

    @classmethod
    def tearDownClass(cls):
        cls.runtime.cleanup()

    def exercise(self, status, body, payload=None):
        observed = []

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def do_GET(self):
                observed.append(("GET", b"", self.headers.get("Authorization")))
                self.respond()

            def do_POST(self):
                data = self.rfile.read(int(self.headers["Content-Length"]))
                observed.append(("POST", data, self.headers.get("Authorization")))
                self.respond()

            def respond(self):
                self.send_response(status)
                self.send_header("Content-Length", str(len(body)))
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *_):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            command = [str(self.driver), "-url", f"http://127.0.0.1:{server.server_port}/api/read",
                       "-requests", "8", "-concurrency", "2", "-expected-json", str(self.expected)]
            if payload is not None:
                request_file = Path(self.runtime.name) / "request.json"
                request_file.write_bytes(payload)
                header_file = Path(self.runtime.name) / "headers.json"
                header_file.write_text('{"Authorization":"Bearer local-test"}')
                command += ["-request-json", str(request_file), "-headers-json", str(header_file)]
            result = subprocess.run(
                command,
                capture_output=True, text=True, timeout=10,
            )
            if payload is not None:
                self.assertEqual(observed, [("POST", payload, "Bearer local-test")] * 8)
            return result
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)

    def test_key_order_does_not_create_a_false_mismatch(self):
        result = self.exercise(200, b'{"data":"same","success":true}')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["successes"], 8)

    def test_errors_and_wrong_work_are_never_counted_as_fast_successes(self):
        for status, body in (
            (503, b'{"success":false}'),
            (200, b'{"success":false}'),
            (200, b'{"success":true,"data":"different"}'),
            (200, b'not JSON'),
            (302, b'{"success":true,"data":"same"}'),
        ):
            with self.subTest(status=status, body=body):
                result = self.exercise(status, body)
                self.assertEqual(result.returncode, 1)
                report = json.loads(result.stdout)
                self.assertEqual(report["successes"], 0)
                self.assertEqual(report["successful_requests_per_second"], 0)
                self.assertEqual(sum(report["errors"].values()), 8)

    def test_memory_sampling_checks_process_identity(self):
        sample = compare.process_sample(os.getpid())
        self.assertGreater(sample["rss_bytes"], 0)
        self.assertGreaterEqual(sample["lifetime_peak_rss_bytes"], sample["rss_bytes"])
        with self.assertRaisesRegex(RuntimeError, "recycled"):
            compare.process_sample(os.getpid(), sample["start_ticks"] + 1)

    def test_post_workloads_replay_the_entire_body_and_authorization_for_every_request(self):
        result = self.exercise(200, b'{"data":"same","success":true}',
                               b'{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["successes"], 8)

    def test_http_success_cannot_hide_wrong_or_pending_relay_accounting(self):
        correct = {"quota": relay_workload.INITIAL_QUOTA - 640, "used_quota": 640,
                   "request_count": 1, "token_quota": relay_workload.INITIAL_QUOTA - 640,
                   "token_used": 640, "log_count": 1, "log_quota": 640,
                   "prompt_tokens": 512, "completion_tokens": 128, "pending_settlements": 0,
                   "channel_quota": 640}

        class Database:
            def __init__(self, row):
                self.row = row

            def sql(self, _):
                return json.dumps(self.row)

        self.assertEqual(relay_workload.assert_accounting(Database(correct), 1001, 1, timeout=0), correct)
        for field in correct:
            with self.subTest(field=field):
                wrong = dict(correct)
                wrong[field] += 1
                with self.assertRaisesRegex(RuntimeError, "accounting mismatch"):
                    relay_workload.assert_accounting(Database(wrong), 1001, 1, timeout=0)

    def test_distinct_large_integers_never_collapse_into_equal_float_values(self):
        original = self.expected.read_text()
        self.expected.write_text('{"success":true,"data":9007199254740992}')
        try:
            result = self.exercise(200, b'{"success":true,"data":9007199254740993}')
            self.assertEqual(result.returncode, 1)
            self.assertEqual(json.loads(result.stdout)["errors"], {"body_mismatch": 8})
        finally:
            self.expected.write_text(original)

    def test_non_loopback_requests_are_rejected_before_connecting(self):
        result = subprocess.run([str(self.driver), "-url", "http://example.com/api/livez"],
                                capture_output=True, text=True, timeout=5)
        self.assertEqual(result.returncode, 2)
        with self.assertRaises(ValueError):
            compare.get_json("http://example.com", "/api/livez")


if __name__ == "__main__":
    unittest.main()
