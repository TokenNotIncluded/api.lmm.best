#!/usr/bin/env python3
"""Loopback OpenAI-compatible provider for the Rust/Go relay differential."""

from __future__ import annotations

from collections.abc import Mapping
import http.server
import json
import pathlib
import re
import sys
import threading


if len(sys.argv) != 3:
    raise SystemExit("usage: relay_openai_provider.py PORT HITS_FILE")

PORT = int(sys.argv[1])
HITS_FILE = pathlib.Path(sys.argv[2])
LOCK = threading.Lock()


def response_for(path: str, body: Mapping[str, object]) -> dict[str, object]:
    if path == "/v1/completions":
        return {
            "id": "cmpl-relay-fixture",
            "object": "text_completion",
            "choices": [{"text": "hello", "index": 0, "finish_reason": "stop"}],
            "model": body.get("model", "gpt-test"),
            "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
        }
    if path in {"/v1/responses", "/v1/responses/compact"}:
        return {
            "id": "resp-relay-fixture",
            "object": "response",
            "model": body.get("model", "gpt-test"),
            "status": "completed",
            "output": [
                {
                    "type": "message",
                    "role": "assistant",
                    "content": [{"type": "output_text", "text": "hello"}],
                }
            ],
            "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
        }
    if path in {
        "/v1/audio/speech",
        "/v1/audio/transcriptions",
        "/v1/audio/translations",
        "/v1/images/generations",
        "/v1/images/edits",
    }:
        # The listener differential intentionally keeps the provider response
        # opaque.  A stable JSON payload also lets the audio/image routes be
        # compared without introducing a binary fixture into this harness.
        return {
            "id": "media-relay-fixture",
            "object": "media.response",
            "model": body.get("model", "gpt-test"),
            "status": "completed",
            "data": [{"text": "hello", "url": "https://provider.invalid/fixture"}],
        }
    return {
        "id": "chatcmpl-relay-fixture",
        "object": "chat.completion",
        "choices": [
            {
                "index": 0,
                "message": {"role": "assistant", "content": "hello"},
                "finish_reason": "stop",
            }
        ],
        "model": body.get("model", "gpt-test"),
        "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
    }


class Fixture(http.server.BaseHTTPRequestHandler):
    def log_message(self, format: str, *args: object) -> None:
        return

    def do_GET(self) -> None:  # noqa: N802 - stdlib callback name
        if self.path == "/health":
            self.send_response(204)
            self.end_headers()
            return
        self.send_error(404)

    def do_POST(self) -> None:  # noqa: N802 - stdlib callback name
        length = int(self.headers.get("content-length", "0"))
        raw = self.rfile.read(length)
        try:
            body = json.loads(raw)
        except (json.JSONDecodeError, UnicodeDecodeError):
            # Valid audio transcription/translation requests are multipart;
            # retain only the model field because this fixture treats the
            # uploaded bytes as opaque.
            model_match = re.search(rb'name="model"\r\n\r\n([^\r\n]+)', raw)
            body = {"model": model_match.group(1).decode() if model_match else ""}
        record = {
            "authorization": self.headers.get("authorization", ""),
            "body": body,
            "path": self.path,
            "content_type": self.headers.get("content-type", "")
            .split(";", 1)[0]
            .strip()
            .lower(),
        }
        with LOCK, HITS_FILE.open("a", encoding="utf-8") as output:
            output.write(json.dumps(record, sort_keys=True, separators=(",", ":")))
            output.write("\n")
        if record["authorization"] != "Bearer provider-owned-secret":
            payload = {
                "error": {
                    "message": "credential-boundary",
                    "type": "invalid_request_error",
                }
            }
            status = 400
        elif not isinstance(body, dict):
            payload = {
                "error": {"message": "invalid-json", "type": "invalid_request_error"}
            }
            status = 400
        else:
            payload = response_for(self.path, body)
            status = 200
        encoded = json.dumps(payload, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("content-type", "application/json")
        self.send_header("x-request-id", "provider-openai-request-id")
        self.send_header("content-length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)


http.server.ThreadingHTTPServer(("127.0.0.1", PORT), Fixture).serve_forever()
