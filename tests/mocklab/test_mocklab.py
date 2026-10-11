"""Component tests for the provider lab, NOT application or ledger acceptance."""
from __future__ import annotations

import base64
import concurrent.futures
import hashlib
import hmac
import http.client
import http.server
import io
import json
from pathlib import Path
import socket
import tempfile
import threading
import time
import unittest
from unittest.mock import patch
from urllib.parse import urlencode

from .client import Client
from .clock import Clock, Canceled
from .config import Config, Invalid, MARKER, loads, local_url, wire
from .mcp import VERSION
from .payments import EPAY_MERCHANT, EPAY_SECRET, STRIPE_KEY, STRIPE_SECRET, STRIPE_VERSION, epay_sign
from .server import Lab


def config(*instances, **extra):
    return Config.parse({"marker": MARKER, "environment": "test", "instances": list(instances), **extra})


def checkout_form(amount="1000", currency="usd", order="mock_order_1"):
    return {"mode": "payment", "client_reference_id": order,
            "metadata[lmm_order_id]": order, "payment_intent_data[metadata][lmm_order_id]": order,
            "line_items[0][quantity]": "1", "line_items[0][price_data][currency]": currency,
            "line_items[0][price_data][unit_amount]": amount,
            "automatic_tax[enabled]": "false", "allow_promotion_codes": "false", "adaptive_pricing[enabled]": "false"}


STRIPE_HEADERS = {"Authorization": "Bearer " + STRIPE_KEY, "Stripe-Version": STRIPE_VERSION,
                  "Idempotency-Key": "mock-checkout-key-0001"}


def wait_until(predicate, seconds=2):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if predicate():
            return
        threading.Event().wait(0.005)  # Scheduler poll, not simulated business time.
    raise AssertionError("condition did not become true before safety deadline")


def events(raw):
    out = []
    for block in raw.decode().split("\n\n"):
        parts = [line[6:] for line in block.splitlines() if line.startswith("data: ")]
        if parts:
            text = "\n".join(parts)
            out.append(text if text == "[DONE]" else loads(text))
    return out


class ConfigurationTests(unittest.TestCase):
    def test_closed_schema(self):
        bad = [{"extra": 1}, {"environment": "production"}, {"seed": True}, {"listen": {"host": "0.0.0.0"}},
               {"limits": {"concurrency": 17}}, {"callbacks": {"wrong": "https://example.invalid/callback"}}]
        for patch in bad:
            with self.subTest(patch=patch), self.assertRaises(Invalid):
                config({"id": "chat", "protocol": "openai-chat"}, **patch)

    def test_duplicate_json_and_non_finite_values(self):
        for raw in ('{"x":1,"x":2}', '{"x":NaN}', '{"x":Infinity}'):
            with self.subTest(raw=raw), self.assertRaises(Invalid):
                loads(raw)

    def test_wrong_protocol_options(self):
        for instance in ({"id": "p", "protocol": "unknown"}, {"id": "p", "protocol": []},
                         {"id": "p", "protocol": "epay-md5-cny", "currency": "USD"},
                         {"id": "p", "protocol": "stripe-v1", "text": "bad"},
                         {"id": "p", "protocol": "openai-chat", "scenario": {"kind": "refund"}},
                         {"id": "p", "protocol": "openai-chat", "cached_tokens": 13}):
            with self.subTest(instance=instance), self.assertRaises(Invalid):
                config(instance)

    def test_callback_urls_are_literal_loopback_only(self):
        for url in ("http://example.invalid:80/", "http://127.0.0.1:80@evil.invalid/", "http://169.254.1.1:80/",
                    "http://127.0.0.1:80/?key=secret", "http://localhost:80/", "http://127.0.0.1:80/#fragment"):
            with self.subTest(url=url), self.assertRaises(Invalid):
                local_url(url)
        self.assertEqual(local_url("http://127.0.0.1:9876/callback"), ("127.0.0.1", 9876, "/callback"))

    def test_clock_has_no_long_real_wait(self):
        clock = Clock(1000)
        with concurrent.futures.ThreadPoolExecutor(1) as executor:
            ready = threading.Event()
            def wait():
                ready.set()
                clock.wait(86400000, time.monotonic() + 1)
            future = executor.submit(wait)
            ready.wait(1)
            wait_until(lambda: clock.waiting > 0)
            clock.advance(86400000)
            future.result(timeout=1)
        self.assertEqual(clock.now(), 87400)
        clock.close()
        with self.assertRaises(Canceled):
            clock.wait(1, time.monotonic() + 1)


class LabTests(unittest.TestCase):
    def remember(self, lab):
        if not hasattr(self,"mock_runs"):
            self.mock_runs=[]
        self.mock_runs.append(lab)
        return lab

    def lab(self, *instances, **extra):
        return self.remember(self.enterContext(Lab(config(*instances, **extra))))

    def stripe(self, *, scenario=None, currency="USD"):
        server = self.lab({"id": "stripe", "protocol": "stripe-v1", "currency": currency, "scenario": scenario or {}})
        return server, Client(server.manifest())

    def create_checkout(self, client, **options):
        status, _, body = client.request("stripe", "POST", "/v1/checkout/sessions", form=checkout_form(**options),
                                          headers=STRIPE_HEADERS)
        self.assertEqual(status, 200, body)
        return loads(body)

    def test_chat_normal_stream_and_usage(self):
        server = self.lab({"id": "chat", "protocol": "openai-chat", "text": "hello世界", "cached_tokens": 3})
        client = Client(server.manifest())
        request = {"model": "mock-small", "messages": [{"role": "user", "content": "test"}]}
        status, _, raw = client.request("chat", "POST", "/v1/chat/completions", request)
        self.assertEqual(status, 200)
        result = loads(raw)
        self.assertEqual(result["choices"][0]["message"]["content"], "hello世界")
        self.assertEqual(result["usage"]["total_tokens"], 18)
        status, _, raw = client.request("chat", "POST", "/v1/chat/completions",
                                        {**request, "stream": True, "stream_options": {"include_usage": True}})
        frames = events(raw)
        self.assertEqual(frames[-1], "[DONE]")
        self.assertEqual(frames[-2]["choices"], [])
        self.assertEqual(frames[-2]["usage"]["prompt_tokens_details"]["cached_tokens"], 3)
        text = "".join(f["choices"][0]["delta"].get("content", "") for f in frames[:-2])
        self.assertEqual(text, "hello世界")

    def test_responses_event_sequence(self):
        server = self.lab({"id": "response", "protocol": "openai-responses"})
        client = Client(server.manifest())
        status, _, raw = client.request("response", "POST", "/v1/responses", {"model": "mock-small", "input": "hello", "stream": True})
        frames = events(raw)
        self.assertEqual(status, 200)
        self.assertEqual([f["sequence_number"] for f in frames], list(range(len(frames))))
        self.assertEqual(frames[0]["type"], "response.created")
        self.assertEqual(frames[-1]["type"], "response.completed")
        self.assertNotIn("[DONE]", frames)
        self.assertEqual(frames[-1]["response"]["usage"]["total_tokens"], 18)

    def test_anthropic_usage_semantics_and_termination(self):
        server = self.lab({"id": "claude", "protocol": "anthropic-messages", "cached_tokens": 4})
        client = Client(server.manifest())
        status, _, raw = client.request("claude", "POST", "/v1/messages",
                                        {"model": "mock-small", "messages": [], "max_tokens": 32, "stream": True})
        frames = events(raw)
        self.assertEqual(status, 200)
        self.assertEqual(frames[0]["type"], "message_start")
        self.assertEqual(frames[0]["message"]["usage"]["input_tokens"], 8)
        self.assertEqual(frames[0]["message"]["usage"]["cache_read_input_tokens"], 4)
        self.assertEqual(frames[-2]["usage"]["output_tokens"], 6)
        self.assertEqual(frames[-1]["type"], "message_stop")

    def test_gemini_candidate_events_and_usage(self):
        server = self.lab({"id": "gemini", "protocol": "gemini-generate-content"})
        client = Client(server.manifest())
        status, _, raw = client.request("gemini", "POST", "/v1beta/models/mock-small:streamGenerateContent?alt=sse",
                                        {"contents": [{"parts": [{"text": "hi"}]}]})
        frames = events(raw)
        self.assertEqual(status, 200)
        self.assertEqual(frames[-1]["candidates"][0]["finishReason"], "STOP")
        self.assertEqual(frames[-1]["usageMetadata"]["totalTokenCount"], 18)
        self.assertNotIn("[DONE]", frames)

    def test_tool_calls_are_different_wire_protocols(self):
        variants = [("openai-chat", "/v1/chat/completions", {"messages": []}),
                    ("openai-responses", "/v1/responses", {"input": "hi"}),
                    ("anthropic-messages", "/v1/messages", {"messages": [], "max_tokens": 32}),
                    ("gemini-generate-content", "/v1beta/models/mock-small:generateContent", {"contents": []})]
        for protocol, path, request in variants:
            with self.subTest(protocol=protocol):
                with self.remember(Lab(config({"id": "model", "protocol": protocol, "output_kind": "tool_call"}))) as server:
                    client = Client(server.manifest())
                    status, _, raw = client.request("model", "POST", path, {"model": "mock-small", **request})
                    self.assertEqual(status, 200, raw)
                    result = loads(raw)
                    if protocol == "openai-chat":
                        self.assertEqual(result["choices"][0]["message"]["tool_calls"][0]["function"]["name"], "mock_add")
                    elif protocol == "openai-responses":
                        self.assertEqual(result["output"][0]["type"], "function_call")
                    elif protocol == "anthropic-messages":
                        self.assertEqual(result["content"][0]["type"], "tool_use")
                    else:
                        self.assertEqual(result["candidates"][0]["content"]["parts"][0]["functionCall"]["args"], {"a": 2, "b": 3})

    def test_json_output_and_missing_usage(self):
        server = self.lab({"id": "json", "protocol": "openai-chat", "output_kind": "json", "text": '{"value":5}'},
                          {"id": "missing", "protocol": "openai-chat", "scenario": {"kind": "missing_usage"}})
        client = Client(server.manifest())
        request = {"model": "mock-small", "messages": []}
        _, _, raw = client.request("json", "POST", "/v1/chat/completions", request)
        self.assertEqual(loads(loads(raw)["choices"][0]["message"]["content"]), {"value": 5})
        _, _, raw = client.request("missing", "POST", "/v1/chat/completions", request)
        self.assertNotIn("usage", loads(raw))

    def test_instance_failures_and_virtual_recovery(self):
        server = self.lab({"id": "primary", "protocol": "openai-chat", "scenario": {"first_failures": 1, "recover_at_ms": 1000}},
                          {"id": "backup", "protocol": "openai-chat"})
        client = Client(server.manifest())
        req = {"model": "mock-small", "messages": []}
        self.assertEqual(client.request("primary", "POST", "/v1/chat/completions", req)[0], 503)
        self.assertEqual(client.request("backup", "POST", "/v1/chat/completions", req)[0], 200)
        client.control("advance", ms=1000)
        self.assertEqual(client.request("primary", "POST", "/v1/chat/completions", req)[0], 200)
        self.assertEqual(client.control("stats")["instances"]["primary"]["attempts"], 2)
        # These are upstream-instance assertions, not a claim about application failover.

    def test_stream_interruption_omits_http_terminator(self):
        server = self.lab({"id": "broken", "protocol": "openai-chat", "scenario": {"kind": "disconnect"}})
        with self.assertRaises(http.client.IncompleteRead):
            Client(server.manifest()).request("broken", "POST", "/v1/chat/completions", {"model": "mock-small", "messages": [], "stream": True})
        wait_until(lambda: server.snapshot()["active"] == 0)

    def test_cancel_releases_virtual_wait_and_concurrency(self):
        server = self.lab({"id": "slow", "protocol": "openai-chat", "scenario": {"first_byte_ms": 60000}},
                          limits={"concurrency": 1, "wall_seconds": 2})
        client = Client(server.manifest())
        conn = http.client.HTTPConnection(client.host, client.port, timeout=1)
        body = wire({"model": "mock-small", "messages": []})
        conn.request("POST", f"/r/{server.run_id}/slow/v1/chat/completions", body,
                     {"Content-Type": "application/json", "X-Mock-Run": server.run_id})
        wait_until(lambda: server.snapshot()["active"] == 1)
        self.assertEqual(client.request("slow", "POST", "/v1/chat/completions", loads(body))[0], 429)
        with self.assertRaises(Invalid):
            client.control("reset")
        conn.close()
        wait_until(lambda: server.snapshot()["active"] == 0)
        self.assertEqual(server.snapshot()["instances"]["slow"]["canceled"], 1)
        self.assertEqual(client.control("reset")["generation"], 1)

    def test_virtual_first_byte_can_be_released(self):
        server = self.lab({"id": "slow", "protocol": "openai-chat", "scenario": {"first_byte_ms": 86400000}})
        client = Client(server.manifest())
        with concurrent.futures.ThreadPoolExecutor(1) as executor:
            future = executor.submit(client.request, "slow", "POST", "/v1/chat/completions", {"model": "mock-small", "messages": []})
            wait_until(lambda: any(t["kind"] == "request" for t in server.snapshot()["trace"]))
            # Advance only once the reply has entered the clock condition wait.
            wait_until(lambda: server.clock.waiting > 0)
            client.control("advance", ms=86400000)
            self.assertEqual(future.result(timeout=2)[0], 200)

    def test_stripe_idempotency_and_signed_event_snapshots(self):
        server, client = self.stripe()
        session = self.create_checkout(client)
        self.assertEqual(self.create_checkout(client), session)
        status, _, _ = client.request("stripe", "POST", "/v1/checkout/sessions", form=checkout_form(amount="1001"), headers=STRIPE_HEADERS)
        self.assertEqual(status, 409)
        failed = client.control("transition", instance="stripe", reference=session["payment_intent"], state="failed")["event"]
        paid = client.control("transition", instance="stripe", reference=session["payment_intent"], state="succeeded")["event"]
        for event_id, expected in ((paid, "payment_intent.succeeded"), (failed, "payment_intent.payment_failed"), (paid, "payment_intent.succeeded")):
            delivery = client.control("delivery", instance="stripe", event=event_id)
            raw = base64.b64decode(delivery["body_base64"])
            self.assertEqual(loads(raw)["type"], expected)
            signature = hmac.new(STRIPE_SECRET.encode(), str(server.clock.now()).encode() + b"." + raw, hashlib.sha256).hexdigest()
            self.assertEqual(delivery["headers"]["Stripe-Signature"], f"t={server.clock.now()},v1={signature}")
            status, _, retrieved = client.request("stripe", "GET", "/v1/events/" + event_id, headers=STRIPE_HEADERS)
            self.assertEqual(status, 200)
            self.assertEqual(raw, retrieved)
        self.assertEqual(server.snapshot()["providers"]["stripe"]["orders"], 1)

    def test_stripe_partial_refunds_and_cumulative_limit(self):
        server, client = self.stripe()
        session = self.create_checkout(client)
        payment = session["payment_intent"]
        client.control("transition", instance="stripe", reference=payment, state="succeeded")
        form = {"payment_intent": payment, "amount": "400", "metadata[lmm_order_id]": "mock_order_1", "metadata[lmm_refund_id]": "refund_1"}
        headers = {**STRIPE_HEADERS, "Idempotency-Key": "mock-refund-key-0001"}
        status, _, raw = client.request("stripe", "POST", "/v1/refunds", form=form, headers=headers)
        self.assertEqual(status, 200, raw)
        refund = loads(raw)
        self.assertEqual(client.request("stripe", "POST", "/v1/refunds", form=form, headers=headers)[2], raw)
        event = client.control("transition", instance="stripe", reference=refund["id"], state="succeeded")["event"]
        delivery = client.control("delivery", instance="stripe", event=event)
        self.assertEqual(loads(base64.b64decode(delivery["body_base64"]))["data"]["object"]["amount"], 400)
        headers["Idempotency-Key"] = "mock-refund-key-0002"
        form.update(amount="601", **{"metadata[lmm_refund_id]": "refund_2"})
        self.assertEqual(client.request("stripe", "POST", "/v1/refunds", form=form, headers=headers)[0], 409)
        form["amount"] = "600"
        self.assertEqual(client.request("stripe", "POST", "/v1/refunds", form=form, headers=headers)[0], 200)
        self.assertEqual(server.snapshot()["providers"]["stripe"]["refunds"], 2)

    def test_timeout_does_not_undo_provider_write(self):
        server, client = self.stripe(scenario={"kind": "timeout_after_commit", "first_byte_ms": 86400000})
        with self.assertRaises(TimeoutError):
            client.request("stripe", "POST", "/v1/checkout/sessions", form=checkout_form(), headers=STRIPE_HEADERS, timeout=0.05)
        wait_until(lambda: server.snapshot()["active"] == 0)
        self.assertEqual(server.snapshot()["providers"]["stripe"]["orders"], 1)
        with concurrent.futures.ThreadPoolExecutor(1) as executor:
            future = executor.submit(client.request, "stripe", "POST", "/v1/checkout/sessions", form=checkout_form(), headers=STRIPE_HEADERS)
            wait_until(lambda: server.clock.waiting > 0)
            client.control("advance", ms=86400000)
            session = loads(future.result(timeout=2)[2])
        event = client.control("transition", instance="stripe", reference=session["payment_intent"], state="succeeded")["event"]
        status, _, raw = client.request("stripe", "GET", "/v1/payment_intents/" + session["payment_intent"], headers=STRIPE_HEADERS)
        self.assertEqual(status, 200)
        self.assertEqual(loads(raw)["status"], "succeeded")
        self.assertTrue(event)
        self.assertEqual(server.snapshot()["providers"]["stripe"]["orders"], 1)

    def test_epay_exact_decimal_and_no_invented_refund(self):
        server = self.lab({"id": "epay", "protocol": "epay-md5-cny", "currency": "CNY"})
        client = Client(server.manifest())
        form = {"pid": EPAY_MERCHANT, "type": "alipay", "out_trade_no": "epay_test_1", "money": "10.01", "sign_type": "MD5"}
        form["sign"] = epay_sign(form)
        self.assertEqual(client.request("epay", "GET", "/submit.php?" + urlencode(form))[0], 200)
        event = client.control("transition", instance="epay", reference="epay_test_1", state="succeeded")["event"]
        delivery = client.control("delivery", instance="epay", event=event)
        self.assertEqual(delivery["headers"]["Content-Type"], "application/x-www-form-urlencoded")
        _, _, raw = client.request("epay", "GET", "/api.php?" + urlencode({"act": "order", "pid": EPAY_MERCHANT, "key": EPAY_SECRET, "out_trade_no": "epay_test_1"}))
        self.assertEqual(loads(raw)["money"], "10.01")
        self.assertEqual(loads(raw)["status"], 1)
        self.assertEqual(client.request("epay", "POST", "/api.php?act=refund", form={})[0], 501)
        form["money"] = "10.001"
        form["sign"] = epay_sign(form)
        self.assertEqual(client.request("epay", "GET", "/submit.php?" + urlencode(form))[0], 400)

    def test_production_payment_key_rejected(self):
        _, client = self.stripe()
        self.assertEqual(client.request("stripe", "POST", "/v1/checkout/sessions", form=checkout_form(),
                          headers={**STRIPE_HEADERS, "Authorization": "Bearer sk_live_not_a_secret"})[0], 401)

    def test_mcp_handshake_tools_calls_and_close(self):
        server = self.lab({"id": "mcp", "protocol": "mcp-2025-11-25"})
        client = Client(server.manifest())
        headers = {"Mcp-Protocol-Version": VERSION}
        def rpc(method, params, ident=1):
            req = {"jsonrpc": "2.0", "method": method, "params": params}
            if ident is not None:
                req["id"] = ident
            return client.request("mcp", "POST", "/mcp", req, headers=headers)
        status, received, raw = rpc("initialize", {"protocolVersion": VERSION, "capabilities": {}, "clientInfo": {"name": "test", "version": "1"}})
        self.assertEqual(status, 200)
        self.assertEqual(loads(raw)["result"]["protocolVersion"], VERSION)
        headers["Mcp-Session-Id"] = received["Mcp-Session-Id"]
        self.assertEqual(rpc("tools/list", {})[0], 400)
        self.assertEqual(rpc("notifications/initialized", {}, None)[0], 202)
        tools = loads(rpc("tools/list", {})[2])["result"]["tools"]
        self.assertEqual(len(tools), 5)
        self.assertTrue(all(t["name"].startswith("mock_") for t in tools))
        result = loads(rpc("tools/call", {"name": "mock_add", "arguments": {"a": 2, "b": 3}})[2])["result"]
        self.assertFalse(result["isError"])
        self.assertEqual(loads(result["content"][0]["text"]), {"value": 5})
        self.assertTrue(loads(rpc("tools/call", {"name": "mock_fail", "arguments": {}})[2])["result"]["isError"])
        self.assertEqual(client.request("mcp", "DELETE", "/mcp", headers=headers)[0], 204)
        self.assertEqual(server.snapshot()["providers"]["mcp"]["sessions"], 0)
        self.assertEqual(server.snapshot()["providers"]["mcp"]["tool_calls"], 2)

    def test_mcp_version_mismatch(self):
        server = self.lab({"id": "mcp", "protocol": "mcp-2025-11-25"})
        status, _, _ = Client(server.manifest()).request("mcp", "POST", "/mcp",
            {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "other"}})
        self.assertEqual(status, 400)
        self.assertEqual(server.snapshot()["providers"]["mcp"]["sessions"], 0)

    def test_two_runs_have_separate_state_and_reset(self):
        a, ca = self.stripe()
        b, cb = self.stripe()
        session = self.create_checkout(ca)
        self.assertNotEqual(a.run_id, b.run_id)
        self.assertEqual(cb.request("stripe", "GET", "/v1/checkout/sessions/" + session["id"], headers=STRIPE_HEADERS)[0], 404)
        self.assertEqual(ca.control("reset")["generation"], 1)
        self.assertEqual(ca.request("stripe", "GET", "/v1/checkout/sessions/" + session["id"], headers=STRIPE_HEADERS)[0], 404)

    def test_request_body_and_record_bounds(self):
        server = self.lab({"id": "chat", "protocol": "openai-chat"}, limits={"body_bytes": 64, "total_input_bytes": 128, "requests": 2})
        client = Client(server.manifest())
        self.assertEqual(client.request("chat", "POST", "/v1/chat/completions", {"text": "x" * 100})[0], 413)
        req = {"model": "mock-small", "messages": []}
        self.assertEqual(client.request("chat", "POST", "/v1/chat/completions", req)[0], 200)
        self.assertEqual(client.request("chat", "POST", "/v1/chat/completions", req)[0], 200)
        self.assertEqual(client.request("chat", "POST", "/v1/chat/completions", req)[0], 429)
        other = self.lab({"id": "stripe", "protocol": "stripe-v1"}, limits={"records": 5})
        c = Client(other.manifest())
        self.create_checkout(c)
        self.assertEqual(c.request("stripe", "POST", "/v1/checkout/sessions", form=checkout_form(order="second"),
                                  headers={**STRIPE_HEADERS, "Idempotency-Key": "other-checkout-key-1"})[0], 429)
        self.assertLessEqual(other.records(), 5)

    def test_missing_run_and_control_token_are_rejected(self):
        server = self.lab({"id": "chat", "protocol": "openai-chat"})
        host, port = server.httpd.server_address
        for path, expected in (("/control/" + server.run_id, 403), (f"/r/{server.run_id}/chat/v1/chat/completions", 403),
                               ("/r/other/chat/v1/chat/completions", 404)):
            conn = http.client.HTTPConnection(host, port, timeout=2)
            conn.request("POST", path, b"{}", {"Content-Type": "application/json"})
            r = conn.getresponse()
            self.assertEqual(r.status, expected)
            r.read()
            conn.close()

    def test_callbacks_delay_duplicate_drop_retry_and_order(self):
        received = []
        statuses = [500, 200, 200, 200]
        class Sink(http.server.BaseHTTPRequestHandler):
            def do_POST(self):
                raw = self.rfile.read(int(self.headers["Content-Length"]))
                received.append(loads(raw)["id"])
                self.send_response(statuses.pop(0) if statuses else 200)
                self.send_header("Content-Length", "0")
                self.end_headers()
            def log_message(self, *_):
                pass
        sink = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Sink)
        thread = threading.Thread(target=sink.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True)
        thread.start()
        self.addCleanup(sink.server_close)
        self.addCleanup(sink.shutdown)
        server = self.lab({"id": "stripe", "protocol": "stripe-v1"},
                          callbacks={"sink": f"http://127.0.0.1:{sink.server_address[1]}/callbacks/1"})
        client = Client(server.manifest())
        payment = self.create_checkout(client)["payment_intent"]
        failed = client.control("transition", instance="stripe", reference=payment, state="failed")["event"]
        paid = client.control("transition", instance="stripe", reference=payment, state="succeeded")["event"]
        client.control("schedule", instance="stripe", event=paid, target="sink", retries=1, retry_delay_ms=50)
        wait_until(lambda: len(received) == 1 and not server.snapshot()["callback_active"])
        client.control("advance", ms=50)
        wait_until(lambda: len(received) == 2)
        client.control("schedule", instance="stripe", event=paid, target="sink", copies=2, drop=True)
        client.control("schedule", instance="stripe", event=failed, target="sink", delay_ms=10, copies=2)
        client.control("advance", ms=10)
        wait_until(lambda: len(received) == 4 and not server.snapshot()["callback_active"])
        self.assertEqual(received, [paid, paid, failed, failed])
        self.assertEqual(sum(t.get("result") == "dropped" for t in server.snapshot()["trace"]), 2)
        with self.assertRaises(Invalid):
            client.control("schedule", instance="stripe", event=paid, target="undeclared")



class LifecycleTests(unittest.TestCase):
    def test_complete_shutdown_closes_slow_connection(self):
        lab = Lab(config({"id":"chat","protocol":"openai-chat"},limits={"wall_seconds":2})).start()
        connection = socket.create_connection(lab.httpd.server_address, timeout=2)
        try:
            connection.sendall(b"POST /incomplete HTTP/1.1\r\n")
            wait_until(lambda: len(lab.httpd.connections)==1)
            lab.close()
            self.assertEqual(connection.recv(1),b"")
            self.assertFalse(lab.httpd.connections)
        finally:
            connection.close()
            lab.close()

    def test_existing_provider_key_field_can_select_explicit_mock_run(self):
        with Lab(config({"id":"chat","protocol":"openai-chat"})) as lab:
            conn=http.client.HTTPConnection(*lab.httpd.server_address,timeout=2)
            conn.request("POST",f"/r/{lab.run_id}/chat/v1/chat/completions",wire({"model":"mock-small","messages":[]}),
                         {"Content-Type":"application/json","Authorization":"Bearer "+lab.manifest()["provider_token"]})
            response=conn.getresponse()
            self.assertEqual(response.status,200)
            response.read()
            conn.close()


class NetworkReportingTests(unittest.TestCase):
    def test_network_status_requires_exact_loopback(self):
        from . import report
        for interfaces, expected in (([(1, "lo")], True), ([(1, "lo"), (2, "eth0")], False), ([], False)):
            with self.subTest(interfaces=interfaces), patch.object(report.socket, "if_nameindex", return_value=interfaces):
                self.assertIs(report.network_status()["network_isolated"], expected)
                self.assertIsNone(report.network_status()["network_inspection_error"])
                self.assertIs(report.isolated_network(), expected)

    def test_metadata_records_inspection_errors_and_gate_fails_closed(self):
        from . import cli, report
        for error in (PermissionError(1, "Operation not permitted"), OSError(38, "Function not implemented")):
            with self.subTest(error=error), patch.object(report.socket, "if_nameindex", side_effect=error):
                result = cli.metadata(cli.ROOT)
                self.assertIsNone(result["network_isolated"])
                self.assertIn(type(error).__name__, result["network_inspection_error"])
                self.assertIn(str(error), result["network_inspection_error"])
                self.assertIs(report.isolated_network(), False)

    def test_selftest_preserves_results_when_inspection_fails(self):
        from . import cli, report
        # Use a small runner-result fixture to avoid recursively running this test.
        for status, expected_code in (("PASS", 2), ("FAIL", 1)):
            with self.subTest(status=status), tempfile.TemporaryDirectory() as folder:
                output = Path(folder) / "report"
                completed = [report.row("fixture.completed-test", "mock-component", status)]
                with patch.object(report.socket, "if_nameindex", side_effect=PermissionError(1, "Operation not permitted")), \
                        patch.object(cli, "selftests", return_value=(completed, "completed test log\n")), \
                        patch.object(cli.sys, "argv", ["mocklab.py", "selftest", "--output", str(output)]), \
                        patch.object(cli.sys, "stdout", io.StringIO()):
                    self.assertEqual(cli.main(), expected_code)
                result = json.loads((output / "report.json").read_text())
                self.assertEqual(result["results"][0], completed[0])
                self.assertEqual(len(completed), 1)  # Saving must not mutate caller-owned rows.
                self.assertEqual(result["summary"][status], 1)
                self.assertEqual(result["summary"]["BLOCKED"], 1)
                self.assertIsNone(result["network_isolated"])
                self.assertEqual(result["results"][-1]["scenario_id"], "network-inspection")
                self.assertIn("PermissionError", result["results"][-1]["evidence"]["reason"])
                self.assertIn("BLOCKED", (output / "report.md").read_text())
                self.assertEqual((output / "component-tests.log").read_text(), "completed test log\n")

    def test_failed_isolated_child_still_writes_blocked_report(self):
        from . import cli, report
        with tempfile.TemporaryDirectory() as folder:
            output = Path(folder) / "report"
            def failed_child(command, **kwargs):
                self.assertIn("--net", command)
                kwargs["output"].write_text("Cannot open netlink socket: Operation not permitted\n")
                return 1, ""
            args = cli.argparse.Namespace(output=output, config=cli.ROOT / "tests/mocklab/example.json", skip_adapters=False)
            with patch.object(report.socket, "if_nameindex", side_effect=PermissionError(1, "Operation not permitted")), \
                    patch.object(cli.shutil, "which", side_effect=lambda name: "/usr/bin/" + name), \
                    patch.object(cli, "run_process", side_effect=failed_child):
                self.assertEqual(cli.isolated(args), 2)
            result = json.loads((output / "report.json").read_text())
            self.assertEqual(result["summary"], {"PASS": 0, "FAIL": 0, "BLOCKED": 2, "NOT RUN": 0})
            self.assertIsNone(result["network_isolated"])
            isolation = next(r for r in result["results"] if r["scenario_id"] == "network-isolation")
            self.assertEqual(isolation["evidence"]["exit_code"], 1)
            self.assertEqual(isolation["evidence"]["log"], "isolation.log")
            self.assertIn("PermissionError", result["network_inspection_error"])
            self.assertIn("BLOCKED", (output / "report.md").read_text())

    def test_missing_isolation_tools_still_writes_blocked_report(self):
        from . import cli, report
        with tempfile.TemporaryDirectory() as folder:
            output = Path(folder) / "report"
            args = cli.argparse.Namespace(output=output)
            with patch.object(report.socket, "if_nameindex", side_effect=PermissionError(1, "Operation not permitted")), \
                    patch.object(cli.shutil, "which", return_value=None), patch.object(cli, "run_process") as run:
                self.assertEqual(cli.isolated(args), 2)
                run.assert_not_called()
            result = json.loads((output / "report.json").read_text())
            self.assertEqual(result["summary"]["BLOCKED"], 2)
            self.assertIn("unshare and ip", result["results"][0]["evidence"]["reason"])

    def test_go_checks_do_not_run_when_inspection_fails(self):
        from . import cli, report
        with tempfile.TemporaryDirectory() as folder:
            output = Path(folder)
            with patch.object(report.socket, "if_nameindex", side_effect=PermissionError(1, "Operation not permitted")), \
                    patch("shutil.which", return_value="/not-executed/go"), patch.object(report, "run_process") as run:
                rows = report.go_adapters(cli.ROOT, output / "absent-manifest.json", output)
                rows.append(report.go_transport(cli.ROOT, output))
                run.assert_not_called()
            self.assertEqual(len(rows), 3)
            self.assertTrue(all(r["status"] == "BLOCKED" for r in rows))
            self.assertTrue(all("PermissionError" in r["evidence"]["reason"] for r in rows))

    def test_isolated_child_stops_before_provider_when_inspection_fails(self):
        from . import cli, report
        with patch.object(report.socket, "if_nameindex", side_effect=PermissionError(1, "Operation not permitted")), \
                patch.object(cli.subprocess, "run"), patch.object(cli, "Lab") as provider, \
                patch.object(cli, "run_suite") as suite:
            with self.assertRaisesRegex(Invalid, "PermissionError"):
                cli.isolated_child(cli.argparse.Namespace())
            provider.assert_not_called()
            suite.assert_not_called()


class ReportPolicyTests(unittest.TestCase):
    def test_ready_does_not_imply_business_or_ledger_pass(self):
        import tempfile
        from pathlib import Path
        from .cli import acceptance_rows
        mode = {"status":503}
        class Readiness(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                body=wire({"error":{"code":"core_migration_incomplete"}} if mode["status"]==503 else {"ready":True})
                self.send_response(mode["status"])
                self.send_header("Content-Length",str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            def log_message(self,*_):
                pass
        server=http.server.ThreadingHTTPServer(("127.0.0.1",0),Readiness)
        thread=threading.Thread(target=server.serve_forever,kwargs={"poll_interval":0.01},daemon=True)
        thread.start()
        try:
            with tempfile.TemporaryDirectory() as folder:
                path=Path(folder)/"targets.json"
                path.write_bytes(wire({"marker":MARKER,"targets":[{"name":"wip.local","host_header":"wip.local",
                    "url":f"http://127.0.0.1:{server.server_address[1]}","declared_sha":"a"*40}]}))
                rows=acceptance_rows(path)
                self.assertEqual(len(rows),14)
                self.assertTrue(all(r["status"]=="BLOCKED" for r in rows))
                mode["status"]=200
                rows=acceptance_rows(path)
                self.assertEqual(len(rows),14)
                self.assertTrue(all(r["status"]=="NOT RUN" for r in rows))
        finally:
            server.shutdown()
            server.server_close()
            thread.join(1)


if __name__ == "__main__":
    unittest.main()
