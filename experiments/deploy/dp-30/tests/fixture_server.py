"""Loopback-only model fixture. NOT Rust, Go, PostgreSQL or a release controller."""
from __future__ import annotations

from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import secrets
import threading
import time
from typing import Iterator, Any

CHUNKS = [f"dp30-sequence-{i:02d};" for i in range(8)]


@contextmanager
def fixture(mode: str = "clean", delay: float = .005) -> Iterator[tuple[str, dict[str, Any], list[dict[str, Any]]]]:
    nonce = secrets.token_hex(16)
    receipts: list[dict[str, Any]] = []
    lock = threading.Lock()

    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *_: object) -> None:
            pass

        def setup(self) -> None:
            super().setup()
            self.connection.settimeout(1)

        def do_GET(self) -> None:
            if self.path != "/__dp30/permit":
                self.send_error(404)
                return
            body = json.dumps({"nonce": nonce, "isolated_test": True}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Connection", "close")
            self.end_headers()
            self.wfile.write(body)
            self.close_connection = True

        def do_POST(self) -> None:
            if self.path != "/v1/chat/completions":
                self.send_error(404)
                return
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 <= length <= 16384:
                self.send_error(413)
                return
            self.rfile.read(length)
            with lock:
                if len(receipts) >= 256:
                    self.send_error(429)
                    return
                receipts.append({"run_id": self.headers.get("X-DP30-Run-ID"),
                                 "request_id": self.headers.get("X-DP30-Request-ID"),
                                 "upstream_attempt_id": f"fixture-{len(receipts):06d}",
                                 "scope": "fixture_receipt_not_application_upstream"})
            if mode == "slow_headers":
                try:
                    for byte in b"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nConnection: close\r\n\r\n":
                        self.wfile.write(bytes([byte]))
                        self.wfile.flush()
                        time.sleep(.02)
                except (OSError, TimeoutError):
                    pass
                self.close_connection = True
                return
            if mode == "redirect":
                self.send_response(302)
                self.send_header("Location", "http://192.0.2.1/never-follow-this")
                self.send_header("Content-Length", "0")
                self.send_header("Connection", "close")
                self.end_headers()
                self.close_connection = True
                return
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Connection", "close")
            self.end_headers()
            values = list(CHUNKS)
            if mode == "reordered":
                values[1], values[2] = values[2], values[1]
            if mode == "duplicated":
                values.insert(3, values[2])
            if mode == "disconnect":
                values = values[:3]
            try:
                if mode == "stall":
                    time.sleep(.3)
                for text in values:
                    event = {"choices": [{"index": 0, "delta": {"content": text}}]}
                    self.wfile.write(b"data: " + json.dumps(event).encode() + b"\n\n")
                    self.wfile.flush()
                    time.sleep(delay)
                if mode != "disconnect":
                    terminal = b"data: [DONE]\n" if mode == "truncated" else b"data: [DONE]\n\n"
                    self.wfile.write(terminal)
                    self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError, TimeoutError):
                pass
            finally:
                self.close_connection = True

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    server.daemon_threads = True
    thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": .01}, daemon=True)
    thread.start()
    origin = f"http://127.0.0.1:{server.server_port}"
    permit = {"origin": origin, "nonce": nonce, "isolated_test": True, "egress_blocked": True,
              "no_real_money": True, "no_notifications": True}
    try:
        yield origin, permit, receipts
    finally:
        server.shutdown()
        server.server_close()
        thread.join(2)
