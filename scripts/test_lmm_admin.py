"""Synthetic transport and artifact tests; no real Core or production access."""
from __future__ import annotations

import contextlib
import copy
import hashlib
import importlib.util
import io
import json
import os
import socket
import stat
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("lmm_admin", Path(__file__).with_name("lmm-admin.py"))
A = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(A)
OP = "12345678-1234-4321-8123-123456789abc"
TOKEN = "synthetic_session_credential_only"
USER = {"user_id": 42, "username": "synthetic-user", "email": "private@example.invalid", "active": True,
        "platform_role": "user", "revision": 3, "marked": False, "policy_id": None}
RESULT = {"version": 1, "status": "applied", "operation_id": OP, "kind": "users", "policy_revision": 4,
          "users": [{**USER, "revision": 4, "marked": True, "policy_id": "test"}]}
LIMITS = {"requests_per_minute_per_user": 2, "requests_per_minute_global": 10,
          "max_concurrent_per_user": 1, "max_concurrent_global": 2,
          "max_input_bytes": 2048, "max_output_bytes": 4096, "max_output_tokens": 128,
          "output_bytes_per_second": 0, "total_timeout_seconds": 10}
CONFIG = {"version": 1, "policies": [{"id": "test", "name": "Synthetic", "mode": "fixed_text",
           "fixed_text": "Fixed output", "clients": {}, "public_models": ["visible-model"], "limits": LIMITS}]}

CAPABILITIES = {"modes": ["fixed_text", "custom_upstream"],
                "client_protocols": ["openai_chat", "openai_responses"],
                "upstream_protocols": ["openai_chat", "openai_responses", "anthropic_messages", "gemini"],
                "numeric_loopback_only": True, "credential_references": []}


def preview_request() -> dict:
    return {"operation_id": OP, "reason": "Synthetic evidence", "action": "mark", "policy_id": "test",
            "expected_policy_revision": 4, "targets": [{"user_id": 42, "expected_revision": 3}]}


def preview() -> dict:
    return {"version": 1, "preview_id": "preview-test-id", "preview_hash": "a" * 64,
            "expires_at": int(time.time()) + 300, "operation_id": OP, "action": "mark",
            "policy_id": "test", "policy_revision": 4,
            "targets": [{"user_id": 42, "username": "synthetic-user", "active": True, "expected_revision": 3,
                         "before": {"marked": False, "policy_id": None},
                         "after": {"marked": True, "policy_id": "test", "active": True}}]}


@contextlib.contextmanager
def server(dispatch):
    seen = []
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass
        def do_GET(self):
            self.handle_request()
        def do_POST(self):
            self.handle_request()
        def do_PUT(self):
            self.handle_request()
        def handle_request(self):
            data = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            seen.append((self.command, self.path, data, self.headers.get("Authorization")))
            result = dispatch(self.command, self.path, data, len(seen))
            if result is None:
                self.connection.shutdown(socket.SHUT_RDWR)
                self.connection.close()
                return
            status, body, headers = result
            raw = body if isinstance(body, bytes) else A.canonical(body)
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            for key, value in headers.items():
                self.send_header(key, value)
            self.end_headers()
            try:
                self.wfile.write(raw)
            except (BrokenPipeError, ConnectionResetError):
                pass
    httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    httpd.daemon_threads = True
    worker = threading.Thread(target=httpd.serve_forever, daemon=True)
    worker.start()
    try:
        yield f"http://127.0.0.1:{httpd.server_port}", seen
    finally:
        httpd.shutdown()
        httpd.server_close()
        worker.join(timeout=2)


def missing():
    return 404, {"error": {"code": "ADMIN_ABUSE_OPERATION_NOT_FOUND"}}, {}


class TransportTests(unittest.TestCase):
    def test_origin_is_https_or_explicit_literal_loopback(self):
        self.assertEqual(A.origin_url("https://EXAMPLE.invalid:443/", False), "https://example.invalid")
        self.assertEqual(A.origin_url("http://[::1]:8123", True), "http://[::1]:8123")
        for value, flag in [("http://127.0.0.1", False), ("http://localhost", True), ("http://10.0.0.1", True),
                            ("https://secret@example.invalid", False), ("https://example.invalid/path", False),
                            ("https://example.invalid?q=secret", False), ("https://example.invalid#x", False),
                            ("https://example.invalid:0", False), ("https://example.invalid\n", False)]:
            with self.subTest(value=value), self.assertRaises(A.ClientError):
                A.origin_url(value, flag)

    def test_read_only_list_does_not_expose_email_or_extra_fields(self):
        with server(lambda *_: (200, {"version": 1, "users": [{**USER, "ip": "secret", "token": "secret"}], "next_after": None}, {})) as (url, seen):
            with patch.dict(os.environ, {"LMM_ADMIN_SESSION": TOKEN}):
                args = A.parser().parse_args(["--server", url, "--allow-insecure-loopback", "users", "list"])
                result = A.run(args)
            self.assertNotIn("private@example.invalid", json.dumps(result))
            self.assertNotIn("secret", json.dumps(result))
            self.assertEqual([row[0] for row in seen], ["GET"])
            self.assertEqual(seen[0][3], "Bearer " + TOKEN)

    def test_redirect_is_not_followed_or_forwarded(self):
        with server(lambda *_: (200, {"ok": True}, {})) as (other, received):
            with server(lambda *_: (302, {}, {"Location": other + "/steal"})) as (url, _):
                with self.assertRaises(A.ApiError) as error:
                    A.Client(url, TOKEN).request("GET", A.API + "/policies")
            self.assertEqual(error.exception.status, 302)
            self.assertEqual(received, [])

    def test_oversized_response_rejected(self):
        with server(lambda *_: (200, b" " * (A.MAX_BODY + 1), {})) as (url, _):
            with self.assertRaisesRegex(A.ClientError, "too large"):
                A.Client(url, TOKEN).request("GET", A.API + "/policies")

    def test_error_body_does_not_leak_messages_or_unknown_codes(self):
        with server(lambda *_: (403, {"error": {"code": "SECRET_TOKEN_TEXT", "message": TOKEN}}, {})) as (url, _):
            with self.assertRaises(A.ApiError) as error:
                A.Client(url, TOKEN).request("GET", A.API + "/policies")
            self.assertNotIn(TOKEN, str(error.exception))
            self.assertNotIn("SECRET_TOKEN_TEXT", str(error.exception))

    def test_non_object_error_is_safe(self):
        with server(lambda *_: (400, {"error": [TOKEN]}, {})) as (url, _):
            with self.assertRaises(A.ApiError):
                A.Client(url, TOKEN).request("GET", A.API + "/policies")

    def test_error_code_with_wrong_type_is_safe(self):
        with server(lambda *_: (400, {"error": {"code": [TOKEN]}}, {})) as (url, _):
            with self.assertRaises(A.ApiError) as error:
                A.Client(url, TOKEN).request("GET", A.API + "/policies")
        self.assertNotIn(TOKEN, str(error.exception))

    def test_duplicate_json_and_nonfinite_and_surrogate_rejected(self):
        for raw in (b'{"x":1,"x":2}', b'{"x":NaN}', b'{"x":"\\ud800"}'):
            with self.subTest(raw=raw), self.assertRaises(A.ClientError):
                A.decode_json(raw)

    def test_drip_fed_body_has_total_deadline(self):
        class Drip:
            def read1(self, _):
                time.sleep(0.012)
                return b" "
            read = read1
        started = time.monotonic()
        with self.assertRaisesRegex(A.ClientError, "deadline"):
            A.read_bounded(Drip(), 1000, 0.03)
        self.assertLess(time.monotonic() - started, 0.2)


class ArtifactTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="lmm-admin-synthetic-")
        self.root = Path(self.temp.name)
    def tearDown(self):
        self.temp.cleanup()
    def make(self, url):
        path = self.root / "preview.json"
        digest = A.private_write(str(path), A.artifact(url, "users", OP, {"request": preview_request(), "preview": preview()}))
        return path, digest
    def args(self, path, digest=None, apply=True):
        return SimpleNamespace(preview=str(path), confirm=digest, apply=apply)

    def test_private_mode_symlink_and_fifo_rejected(self):
        path = self.root / "token"
        path.write_text(TOKEN)
        path.chmod(0o644)
        with self.assertRaises(A.ClientError): A.private_read(str(path))
        path.chmod(0o600)
        self.assertEqual(A.private_read(str(path)), TOKEN.encode())
        link = self.root / "link"
        link.symlink_to(path)
        with self.assertRaises(A.ClientError): A.private_read(str(link))
        fifo = self.root / "fifo"
        os.mkfifo(fifo, 0o600)
        with self.assertRaises(A.ClientError): A.private_read(str(fifo))

    def test_artifact_is_private_never_overwritten(self):
        path, _ = self.make("https://example.invalid")
        self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
        with self.assertRaises(A.ClientError): A.private_write(str(path), {"replace": True})

    def test_dry_run_has_zero_network_calls(self):
        path, digest = self.make("https://example.invalid")
        client = A.Client("https://example.invalid", "")
        with patch.object(client, "request", side_effect=AssertionError("network forbidden")):
            result = A.apply_artifact(client, self.args(path, apply=False))
        self.assertTrue(result["dry_run"])
        self.assertEqual(result["artifact_sha256"], digest)

    def test_confirmation_hash_and_origin_checked_before_network(self):
        path, digest = self.make("https://example.invalid")
        for url, confirmation in [("https://example.invalid", "b" * 64), ("https://other.invalid", digest)]:
            client = A.Client(url, TOKEN)
            with patch.object(client, "request", side_effect=AssertionError("network forbidden")):
                with self.assertRaises(A.ClientError): A.apply_artifact(client, self.args(path, confirmation))
        path.write_bytes(path.read_bytes() + b" ")
        with self.assertRaisesRegex(A.ClientError, "SHA256"):
            A.apply_artifact(A.Client("https://example.invalid", TOKEN), self.args(path, digest))

    def test_single_commit_and_later_reuse_no_second_post(self):
        applied = False
        def dispatch(method, path, data, _):
            nonlocal applied
            if method == "GET": return (200, RESULT, {}) if applied else missing()
            self.assertEqual(json.loads(data), {"operation_id": OP, "preview_id": "preview-test-id", "preview_hash": "a" * 64})
            applied = True
            return 200, RESULT, {}
        with server(dispatch) as (url, seen):
            path, digest = self.make(url)
            first = A.apply_artifact(A.Client(url, TOKEN), self.args(path, digest))
            second = A.apply_artifact(A.Client(url, TOKEN), self.args(path, digest))
        self.assertTrue(first["submitted"])
        self.assertTrue(second["reused"])
        self.assertEqual([row[0] for row in seen], ["GET", "POST", "GET"])
        self.assertNotIn("private@example.invalid", json.dumps(first))

    def test_lost_commit_response_queries_same_operation_once(self):
        applied = False
        def dispatch(method, path, _data, _):
            nonlocal applied
            if method == "GET": return (200, RESULT, {}) if applied else missing()
            applied = True
            return None
        with server(dispatch) as (url, seen):
            path, digest = self.make(url)
            result = A.apply_artifact(A.Client(url, TOKEN), self.args(path, digest))
        self.assertTrue(result["reconciled"])
        self.assertEqual([row[0] for row in seen], ["GET", "POST", "GET"])
        self.assertEqual(seen[-1][1], A.API + "/operations/" + OP)

    def test_unknown_commit_never_replayed(self):
        with server(lambda method, *_: missing() if method == "GET" else (503, {"error": {"code": "ADMIN_ABUSE_UNAVAILABLE"}}, {})) as (url, seen):
            path, digest = self.make(url)
            with self.assertRaises(A.OutcomeUnknown) as error:
                A.apply_artifact(A.Client(url, TOKEN), self.args(path, digest))
        self.assertIn(OP, str(error.exception))
        self.assertEqual([row[0] for row in seen], ["GET", "POST", "GET"])

    def test_fourxx_failure_does_not_replay(self):
        with server(lambda method, *_: missing() if method == "GET" else (409, {"error": {"code": "ADMIN_ABUSE_CONFLICT"}}, {})) as (url, seen):
            path, digest = self.make(url)
            with self.assertRaises(A.ApiError): A.apply_artifact(A.Client(url, TOKEN), self.args(path, digest))
        self.assertEqual([row[0] for row in seen], ["GET", "POST"])

    def test_proxy_timeout_fourxx_is_unknown_and_never_replayed(self):
        with server(lambda method, *_: missing() if method == "GET" else (408, b"Proxy timeout", {})) as (url, seen):
            path, digest = self.make(url)
            with self.assertRaises(A.OutcomeUnknown):
                A.apply_artifact(A.Client(url, TOKEN), self.args(path, digest))
        self.assertEqual([row[0] for row in seen], ["GET", "POST", "GET"])

    def test_preflight_unavailable_prevents_mutation(self):
        with server(lambda *_: (503, {"error": {"code": "ADMIN_ABUSE_UNAVAILABLE"}}, {})) as (url, seen):
            path, digest = self.make(url)
            with self.assertRaises(A.ApiError): A.apply_artifact(A.Client(url, TOKEN), self.args(path, digest))
        self.assertEqual([row[0] for row in seen], ["GET"])

    def test_policy_local_preview_sends_exact_put_without_before_snapshot(self):
        result = {**RESULT, "kind": "policies", "policy_revision": 5, "users": [], "applied_config_hash": A.policy_config_hash(CONFIG)}
        def dispatch(method, path, data, _):
            if method == "GET": return missing()
            self.assertEqual(method, "PUT")
            self.assertEqual(path, A.API + "/policies")
            self.assertEqual(set(json.loads(data)), {"operation_id", "reason", "expected_revision", "config"})
            return 200, result, {}
        with server(dispatch) as (url, seen):
            payload = {"operation_id": OP, "reason": "Synthetic reason", "expected_revision": 4, "config": CONFIG,
                       "before_config": {"version": 1, "policies": []}}
            path = self.root / "policy.json"
            digest = A.private_write(str(path), A.artifact(url, "policy-local-diff", OP, payload))
            A.apply_artifact(A.Client(url, TOKEN), self.args(path, digest))
        self.assertEqual([row[0] for row in seen], ["GET", "PUT"])

    def test_policy_preflight_requires_exact_applied_config_hash(self):
        for config_hash, exception in [(None, A.OutcomeUnknown), ("bad-hash", A.OutcomeUnknown), ("f" * 64, A.ClientError)]:
            result = {**RESULT, "kind": "policies", "policy_revision": 5, "users": [], "applied_config_hash": config_hash}
            with server(lambda *_: (200, result, {})) as (url, seen):
                payload = {"operation_id": OP, "reason": "Synthetic reason", "expected_revision": 4,
                           "config": CONFIG, "before_config": {"version": 1, "policies": []}}
                path = self.root / ("policy-" + str(config_hash) + ".json")
                digest = A.private_write(str(path), A.artifact(url, "policy-local-diff", OP, payload))
                with self.assertRaises(exception): A.apply_artifact(A.Client(url, TOKEN), self.args(path, digest))
            self.assertEqual([row[0] for row in seen], ["GET"])

    def test_online_policy_preview_requires_complete_compatible_capabilities(self):
        custom = copy.deepcopy(CONFIG)
        policy = custom["policies"][0]
        policy["mode"] = "custom_upstream"; del policy["fixed_text"]
        target = {"base_url": "http://127.0.0.1:9123/v1", "upstream_protocol": "openai_chat",
                  "model": "fixture", "credential_reference": "unreviewed_SYNTHETIC_ALIAS"}
        policy["clients"] = {"openai_chat": target, "openai_responses": copy.deepcopy(target)}
        valid = {"version": 1, "revision": 2, "config": CONFIG, "capabilities": CAPABILITIES}
        malformed = [{key: value for key, value in valid.items() if key != "capabilities"},
                     {**valid, "version": 2}, {**valid, "version": True},
                     {**valid, "capabilities": {**CAPABILITIES, "credential_references": None}},
                     {**valid, "capabilities": {**CAPABILITIES, "credential_references": ["/tmp/credential"]}},
                     {**valid, "capabilities": {**CAPABILITIES, "numeric_loopback_only": False}},
                     {**valid, "capabilities": {**CAPABILITIES, "modes": ["fixed_text", "unknown"]}}]
        for index, input_config in enumerate((CONFIG, custom)):
            for variant, response in enumerate(malformed):
                with self.subTest(config=index, variant=variant), server(lambda *_: (200, response, {})) as (url, seen):
                    source = self.root / f"input-{index}-{variant}.json"
                    A.private_write(str(source), input_config)
                    out = self.root / f"preview-{index}-{variant}.json"
                    args = A.parser().parse_args(["--server", url, "--allow-insecure-loopback", "policy", "preview",
                                                 "--file", str(source), "--out", str(out), "--reason-stdin"])
                    with patch.dict(os.environ, {"LMM_ADMIN_SESSION": TOKEN}), patch.object(sys, "stdin", io.TextIOWrapper(io.BytesIO(b"Synthetic evidence"))):
                        with self.assertRaisesRegex(A.ClientError, "INVALID_RESPONSE"):
                            A.run(args)
                    self.assertFalse(out.exists())
                    self.assertEqual([row[0] for row in seen], ["GET"])
        with self.assertRaisesRegex(A.ClientError, "INVALID_RESPONSE"):
            A.policy_config(custom, None)

    def test_wrong_recorded_user_poststate_is_not_reported_as_applied(self):
        bad = {**RESULT, "users": [{**USER, "revision": 99}]}
        with server(lambda *_: (200, bad, {})) as (url, seen):
            path, digest = self.make(url)
            with self.assertRaisesRegex(A.ClientError, "post-state"):
                A.apply_artifact(A.Client(url, TOKEN), self.args(path, digest))
        self.assertEqual([row[0] for row in seen], ["GET"])

    def test_full_cli_preview_apply_readback_uses_frozen_target_revisions(self):
        generated = None
        applied = False
        def dispatch(method, path, data, _):
            nonlocal generated, applied
            if path == A.API + "/policies":
                return 200, {"version": 1, "revision": 4, "config": CONFIG, "capabilities": CAPABILITIES}, {}
            if path == A.API + "/users/42": return 200, USER, {}
            if path == A.API + "/users/preview":
                body = json.loads(data); generated = body["operation_id"]
                self.assertEqual(body["targets"], [{"user_id": 42, "expected_revision": 3}])
                return 200, {**preview(), "operation_id": generated}, {}
            if path.startswith(A.API + "/operations/"):
                return (200, {**RESULT, "operation_id": generated}, {}) if applied else missing()
            if path == A.API + "/users/commit":
                self.assertEqual(json.loads(data)["operation_id"], generated)
                applied = True
                return 200, {**RESULT, "operation_id": generated}, {}
            raise AssertionError("Unexpected route")
        with server(dispatch) as (url, seen), patch.dict(os.environ, {"LMM_ADMIN_SESSION": TOKEN}):
            base = ["--server", url, "--allow-insecure-loopback"]
            out = self.root / "cli-preview.json"
            args = A.parser().parse_args([*base, "honeypot", "preview", "--user-id", "42", "--action", "mark",
                                         "--policy-id", "test", "--reason-stdin", "--out", str(out)])
            with patch.object(sys, "stdin", io.TextIOWrapper(io.BytesIO(b"Synthetic evidence"))):
                result = A.run(args)
            args = A.parser().parse_args([*base, "apply", "--preview", str(out), "--confirm", result["artifact_sha256"], "--apply"])
            applied_result = A.run(args)
            self.assertEqual(applied_result["users"][0]["revision"], 4)
        self.assertEqual([row[0] for row in seen], ["GET", "GET", "POST", "GET", "POST"])
        saved = A.decode_json(out.read_bytes())
        self.assertEqual(saved["payload"]["request"]["reason"], "Synthetic evidence")

    def test_expired_preview_can_reconcile_existing_operation_without_commit(self):
        with server(lambda *_: (200, RESULT, {})) as (url, seen):
            expired = preview(); expired["expires_at"] = 1
            path = self.root / "expired.json"
            digest = A.private_write(str(path), A.artifact(url, "users", OP, {"request": preview_request(), "preview": expired}))
            result = A.apply_artifact(A.Client(url, TOKEN), self.args(path, digest))
        self.assertTrue(result["reused"])
        self.assertFalse(result["exact_request_match_verified"])
        self.assertEqual([row[0] for row in seen], ["GET"])


class ContractTests(unittest.TestCase):
    def test_preview_binds_all_ids_revisions_policy_and_transitions(self):
        request = {"operation_id": OP, "action": "mark", "policy_id": "test", "expected_policy_revision": 4,
                   "targets": [{"user_id": 42, "expected_revision": 3}]}
        self.assertEqual(A.check_preview(preview(), request), preview())
        mutations = [lambda v: v.update(policy_revision=5), lambda v: v.update(operation_id="other-operation"),
                     lambda v: v["targets"][0].update(user_id=99), lambda v: v["targets"][0].update(expected_revision=9),
                     lambda v: v["targets"][0]["after"].update(marked=False), lambda v: v.update(expires_at=0),
                     lambda v: v["targets"].append(copy.deepcopy(v["targets"][0]))]
        for mutate in mutations:
            value = preview(); mutate(value)
            with self.assertRaises(A.ClientError): A.check_preview(value, request)

    def test_policy_exact_fields_limits_public_models_no_literals(self):
        A.policy_config(CONFIG)
        changes = [lambda v: v["policies"][0].update(token="secret"),
                   lambda v: v["policies"][0].update(public_models=[]),
                   lambda v: v["policies"][0].update(public_models=["x", "x"]),
                   lambda v: v["policies"][0]["limits"].update(max_concurrent_per_user=10),
                   lambda v: v["policies"][0]["limits"].update(max_output_bytes=True),
                   lambda v: v["policies"][0]["limits"].update(max_output_bytes=1)]
        for change in changes:
            value = copy.deepcopy(CONFIG); change(value)
            with self.assertRaises(A.ClientError): A.policy_config(value)

    def test_custom_upstream_conversion_uses_loopback_preloaded_alias(self):
        config = copy.deepcopy(CONFIG)
        policy = config["policies"][0]
        policy["mode"] = "custom_upstream"; del policy["fixed_text"]
        target = {"base_url": "http://127.0.0.1:9123/v1", "upstream_protocol": "anthropic_messages",
                  "model": "upstream-model", "credential_reference": "sink-server"}
        policy["clients"] = {"openai_chat": target, "openai_responses": copy.deepcopy(target)}
        A.policy_config(config, ["sink-server"])
        for key, value in [("base_url", "https://public.invalid/v1"), ("base_url", "http://localhost/v1"),
                           ("base_url", "http://127.0.0.1/v1?token=secret"), ("credential_reference", "/tmp/token"),
                           ("upstream_protocol", "unknown")]:
            bad = copy.deepcopy(config); bad["policies"][0]["clients"]["openai_chat"][key] = value
            with self.assertRaises(A.ClientError): A.policy_config(bad, ["sink-server"])
        with self.assertRaises(A.ClientError): A.policy_config(config, ["different-alias"])

    def test_no_token_argument_or_fake_disable_reward_command(self):
        parser = A.parser()
        for tail in (["--token"], ["--token", TOKEN], ["users", "disable", "42"], ["users", "revoke-reward", "42"]):
            with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                parser.parse_args(["--server", "https://example.invalid", *tail])

    def test_config_hash_is_key_order_independent_utf8_without_newline(self):
        first = {"b": "中文", "a": [1, "é"]}
        other = {"a": [1, "é"], "b": "中文"}
        expected = '{"a":[1,"é"],"b":"中文"}'.encode("utf-8")
        self.assertEqual(A.canonical_bytes_no_newline(first), expected)
        self.assertEqual(A.policy_config_hash(first), hashlib.sha256(expected).hexdigest())
        self.assertEqual(A.policy_config_hash(first), A.policy_config_hash(other))
        self.assertNotEqual(A.policy_config_hash(first), A.policy_config_hash({"a": ["é", 1], "b": "中文"}))

    def test_real_cli_parser_errors_never_echo_user_supplied_values(self):
        secret_like = "independent_SYNTHETIC_ONLY"
        script = Path(__file__).with_name("lmm-admin.py")
        invalid = [["--token", secret_like, "users", "list"],
                   ["users", "inspect", secret_like],
                   ["users", secret_like],
                   ["users", "list", "--limit", secret_like],
                   ["users", "list", "--unknown", secret_like]]
        for tail in invalid:
            with self.subTest(tail=tail):
                result = subprocess.run([sys.executable, "-B", str(script), "--server", "https://example.invalid", *tail],
                                        stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=5)
                self.assertEqual(result.returncode, 2)
                self.assertEqual(result.stdout, b"")
                self.assertNotIn(secret_like.encode(), result.stderr)
                self.assertEqual(result.stderr, b"Invalid arguments; use --help for supported options.\n")

    def test_operation_result_requires_applied_and_exact_operation(self):
        for delta in ({"status": "pending"}, {"operation_id": "another-operation"}, {"version": True}):
            with self.assertRaises(A.ClientError): A.safe_operation({**RESULT, **delta}, OP)


if __name__ == "__main__":
    unittest.main(verbosity=2)
