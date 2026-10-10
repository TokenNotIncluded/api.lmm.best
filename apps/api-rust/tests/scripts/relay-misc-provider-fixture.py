#!/usr/bin/env python3
"""Loopback-only provider for relay_misc::tests::loopback_provider_contract.

Usage: python3 relay-misc-provider-fixture.py PORT HITS_FILE
The parent runner owns the process and supplies LMM_RELAY_MISC_PROVIDER_URL.
"""

import http.server
import json
from pathlib import Path
import sys
import threading


class Provider(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_args):
        pass

    def reply(self, status, content_type, body, headers=None):
        self.send_response(status)
        self.send_header("content-type", content_type)
        self.send_header("content-length", str(len(body)))
        for key, value in (headers or {}).items():
            self.send_header(key, value)
        self.end_headers()
        self.wfile.write(body)
        self.wfile.flush()

    def do_GET(self):
        if self.path != "/health":
            self.reply(404, "application/json", b'{"error":"not-found"}')
            return
        self.reply(200, "application/json", b'{"fixture":"healthy"}')

    def do_POST(self):
        length = int(self.headers.get("content-length", "0"))
        if length < 0 or length > 64 * 1024 * 1024:
            self.reply(413, "application/json", b'{"error":"body-too-large"}')
            return
        body = self.rfile.read(length)
        authorized = self.headers.get("authorization") == "Bearer provider-owned-secret"
        mode = self.headers.get("x-fixture-mode", "")
        with self.server.hit_lock, self.server.hits.open("a", encoding="utf-8") as output:
            output.write(json.dumps({"path": self.path, "authorization_valid": authorized,
                                     "mode": mode, "body_bytes": len(body)}) + "\n")
        if not authorized:
            self.reply(400, "application/json", b'{"error":"credential-boundary"}')
        elif mode == "sse":
            self.reply(200, "text/event-stream", b'data: {"fixture":true}\n\ndata: [DONE]\n\n',
                       {"cache-control": "no-cache"})
        elif mode == "error":
            self.reply(429, "application/json", b'{"error":"fixture-rate-limit"}',
                       {"retry-after": "7"})
        else:
            self.reply(200, "application/json", b'{"fixture":"loopback"}')


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: relay-misc-provider-fixture.py PORT HITS_FILE")
    server = http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), Provider)
    server.hits = Path(sys.argv[2])
    server.hit_lock = threading.Lock()
    server.serve_forever()


if __name__ == "__main__":
    main()
