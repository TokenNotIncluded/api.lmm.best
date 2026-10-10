#!/usr/bin/env python3
"""Loopback-only fixture service for real HTTP/SSE process tests. Not LMM billing."""
import argparse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import sys
import time
from urllib.parse import parse_qs, urlparse

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from fixture import connection
from contract import canonical, digest


def serve(root: Path, node: str, endpoint: Path):
    runtime = root / node / "runtime.sqlite"
    business = root / node / "business.sqlite"

    def charge(request_id: str, amount: int):
        with connection(business) as db:
            db.execute("BEGIN IMMEDIATE")
            row = db.execute("SELECT amount FROM ledger WHERE request_id=?", (request_id,)).fetchone()
            if row is None:
                db.execute("INSERT INTO ledger VALUES(?,?)", (request_id, amount))
                db.execute("UPDATE balances SET amount=amount-? WHERE id='alice'", (amount,))
            elif row[0] != amount:
                raise ValueError("request amount changed")

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_GET(self):
            uri = urlparse(self.path)
            params = parse_qs(uri.query)
            with connection(business) as db:
                valid = db.execute("SELECT 1 FROM sessions WHERE id=?",
                                   (self.headers.get("Cookie", "").removeprefix("session="),)).fetchone()
            if uri.path != "/health" and valid is None:
                self.send_error(401)
                return
            try:
                if uri.path == "/stream":
                    seconds = max(.1, min(3, float(params.get("seconds", ["1"])[0])))
                    rid = params["id"][0]
                    with connection(runtime) as db:
                        db.execute("BEGIN IMMEDIATE")
                        state = json.loads(db.execute("SELECT body FROM node_state").fetchone()[0])
                        version = digest(state["components"]["rust-core"]["active"])
                        db.execute("INSERT INTO streams VALUES(?,?,?,1)", (rid, "rust-core", version))
                    self.send_response(200)
                    self.send_header("Content-Type", "text/event-stream")
                    self.end_headers()
                    try:
                        for i in range(5):
                            self.wfile.write(("data: " + canonical({"i": i, "version": version}) + "\n\n").encode())
                            self.wfile.flush()
                            time.sleep(seconds / 5)
                        charge(rid, 1)
                        self.wfile.write(b"event: done\ndata: ok\n\n")
                        self.wfile.flush()
                    except (BrokenPipeError, ConnectionResetError):
                        charge(rid, 1)
                    finally:
                        with connection(runtime) as db:
                            db.execute("DELETE FROM streams WHERE id=?", (rid,))
                    return
                if uri.path == "/consume":
                    amount = int(params.get("amount", ["1"])[0])
                    if not 1 <= amount <= 100:
                        raise ValueError("amount outside fixture limit")
                    charge(params["id"][0], amount)
                    body = b"settled"
                elif uri.path == "/balance":
                    with connection(business) as db:
                        value = db.execute("SELECT amount FROM balances WHERE id='alice'").fetchone()[0]
                    body = str(value).encode()
                elif uri.path.startswith("/assets/"):
                    with connection(runtime) as db:
                        state = json.loads(db.execute("SELECT body FROM node_state").fetchone()[0])
                    if uri.path[len("/assets/"):] not in state["assets"]:
                        self.send_error(404)
                        return
                    body = b"// retained fixture asset"
                else:
                    body = b"fixture-only"
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            except (ValueError, KeyError):
                self.send_error(422)

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    endpoint.write_text(str(server.server_address[1]))
    server.serve_forever(poll_interval=.05)


if __name__ == "__main__":
    p = argparse.ArgumentParser()
    p.add_argument("root", type=Path)
    p.add_argument("node")
    p.add_argument("endpoint", type=Path)
    args = p.parse_args()
    serve(args.root, args.node, args.endpoint)
