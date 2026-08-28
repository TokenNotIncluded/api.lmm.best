#!/usr/bin/env python3
"""Loopback provider fixture for the relay-misc listener differential."""

from __future__ import annotations

import http.server
import json
import pathlib
import sys
import threading


if len(sys.argv) != 3:
    raise SystemExit("usage: relay_misc_provider.py PORT HITS_FILE")

PORT = int(sys.argv[1])
HITS_FILE = pathlib.Path(sys.argv[2])
WRITE_LOCK = threading.Lock()
SUCCESS_BODY = (
    b'{"data":[{"embedding":[0.25],"index":0}],"model":"gpt-test",'
    b'"usage":{"prompt_tokens":1,"total_tokens":1}}'
)
ERROR_BODIES = {
    "fail": b'{"error":"fixture-rate-limit"}',
    "fail-message": b'{"message":"fixture-message"}',
    "fail-openai": (
        b'{"error":{"message":"fixture-openai","type":"server_error",'
        b'"param":"capacity","code":"busy"}}'
    ),
    "fail-invalid-json": b"not-json",
}


class Fixture(http.server.BaseHTTPRequestHandler):
    # Keep the loopback provider connection semantics explicit.  The Go
    # oracle reuses HTTP/1.1 connections; relying on BaseHTTPRequestHandler's
    # HTTP/1.0 default makes the second 429 response susceptible to a stale
    # keep-alive/broken-pipe race and turns a provider error into a transport
    # error before either relay can compare it.
    protocol_version = "HTTP/1.1"

    def log_message(self, format: str, *args: object) -> None:
        return

    def do_GET(self) -> None:  # noqa: N802 - stdlib callback name
        if self.path != "/health":
            self.send_error(404)
            return
        self.send_response(204)
        self.end_headers()

    def do_POST(self) -> None:  # noqa: N802 - stdlib callback name
        length = int(self.headers.get("content-length", "0"))
        raw_body = self.rfile.read(length)
        record = {
            "authorization_valid": self.headers.get("authorization", "")
            == "Bearer provider-owned-secret",
            "body": json.loads(raw_body),
            "caller_secret_present": bool(self.headers.get("x-caller-secret", "")),
            "content_encoding": self.headers.get("content-encoding", ""),
            "content_type": self.headers.get("content-type", ""),
            "path": self.path,
        }
        with WRITE_LOCK, HITS_FILE.open("a", encoding="utf-8") as output:
            output.write(json.dumps(record, sort_keys=True, separators=(",", ":")))
            output.write("\n")

        if not record["authorization_valid"]:
            self.send_response(400)
            self.send_header("content-type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"error":"credential-boundary"}')
            return

        input_value = record["body"].get("input")
        failing = input_value in ERROR_BODIES
        response_body = ERROR_BODIES.get(input_value, SUCCESS_BODY)
        self.send_response(429 if failing else 200)
        self.send_header("content-type", "application/json")
        self.send_header("x-request-id", "provider-generic-request-id")
        self.send_header("x-oneapi-request-id", "provider-shadow-request-id")
        self.send_header("connection", "x-hop-leak, keep-alive")
        self.send_header("x-hop-leak", "must-not-reach-caller")
        if failing:
            self.send_header("retry-after", "7")
        self.send_header("content-length", str(len(response_body)))
        self.end_headers()
        self.wfile.write(response_body)


http.server.ThreadingHTTPServer(("127.0.0.1", PORT), Fixture).serve_forever()
