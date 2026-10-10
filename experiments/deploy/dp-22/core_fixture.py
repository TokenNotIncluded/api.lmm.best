#!/usr/bin/env python3
"""Loopback transport reference ONLY. This is not Rust and has no ledger.

An append/update SQLite record is written before each upstream POST. Its state
means transport state, never a charge, refund, or settlement. Unknown records
are retained after SIGKILL. No request is replayed on restart.
"""
from __future__ import annotations

import argparse
from contextlib import contextmanager
import fcntl
import http.client
import json
import os
from pathlib import Path
import signal
import socket
import sqlite3
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MAX_BODY = 4096


class Core:
    def __init__(self, name: str, run_dir: Path, upstream: int, token: str) -> None:
        self.name, self.root, self.upstream, self.token = name, run_dir, upstream, token
        self.route_port = int(os.environ['DP22_ROUTE_PORT'])
        run_dir.mkdir(mode=0o700)  # A generation must own a fresh directory.
        self.lock_file = open(run_dir / 'rpc.lock', 'x+b')
        fcntl.flock(self.lock_file, fcntl.LOCK_EX | fcntl.LOCK_NB)
        self.rpc = socket.socket(socket.AF_UNIX)
        self.rpc.bind(str(run_dir / 'rpc.sock'))  # NEVER unlink to make bind work.
        os.chmod(run_dir / 'rpc.sock', 0o600)
        self.rpc.listen(4)
        self.rpc.settimeout(0.2)
        self.rpc_inode = (run_dir / 'rpc.sock').stat().st_ino
        self.db_path = run_dir / 'transport.sqlite'
        with self.db() as db:
            db.execute('CREATE TABLE requests (id TEXT PRIMARY KEY, state TEXT NOT NULL)')
            db.execute('CREATE TABLE proof (value TEXT NOT NULL)')
        self.mutex = threading.Lock()
        self.phase = 'preparing'
        self.active = 0
        self.checks: dict[str, bool] = {}
        self.exit = threading.Event()
        self.completions: dict[str, threading.Event] = {}

    @contextmanager
    def db(self):
        db = sqlite3.connect(self.db_path, timeout=2)
        try:
            db.execute('PRAGMA synchronous=FULL')
            with db:
                yield db
        finally:
            db.close()

    def prepare(self) -> bool:
        # An actual local dependency handshake and committed write/read, not
        # merely a process health response or elapsed time.
        connection = http.client.HTTPConnection('127.0.0.1', self.upstream, timeout=2)
        try:
            connection.request('GET', '/fixture-capabilities')
            response = connection.getresponse()
            capability = json.loads(response.read())
            if response.status != 200 or capability != {'protocol': 'dp22-sse-1', 'paid': False}:
                return False
            with self.db() as db:
                db.execute('INSERT INTO proof VALUES (?)', (self.name,))
            with self.db() as db:
                written = db.execute('SELECT value FROM proof ORDER BY rowid DESC LIMIT 1').fetchone()
                integrity = db.execute('PRAGMA quick_check').fetchone()
            if written != (self.name,) or integrity != ('ok',):
                return False
            with self.mutex:
                if self.phase != 'preparing':
                    return False
                self.checks = {'upstream_handshake': True, 'committed_write_read': True,
                               'private_socket_bound': True}
                self.phase = 'ready'
            return True
        except (OSError, ValueError, sqlite3.Error):
            return False
        finally:
            connection.close()

    def status(self) -> dict:
        with self.mutex:
            phase, active, checks = self.phase, self.active, dict(self.checks)
        with self.db() as db:
            rows = db.execute('SELECT id,state FROM requests ORDER BY id').fetchall()
        return {'instance': self.name, 'phase': phase, 'active': active, 'checks': checks,
                'evidence': 'python-transport-reference', 'model_ready': False,
                'requests': dict(rows), 'rpc_exists': (self.root / 'rpc.sock').exists()}

    def drain(self) -> None:
        with self.mutex:
            self.phase = 'draining'
            if self.active == 0:
                self.exit.set()

    def begin(self, request_id: str, defer: bool) -> bool:
        with self.mutex:
            if self.phase != 'ready':
                return False
            # Written before sending the POST. Duplicates fail, not replay.
            with self.db() as db:
                db.execute('INSERT INTO requests VALUES (?,?)', (request_id, 'transport_open'))
            self.active += 1
            if defer:
                self.completions[request_id] = threading.Event()
        return True

    def ack_admission(self, admission: str) -> bool:
        conn = http.client.HTTPConnection('127.0.0.1', self.route_port, timeout=2)
        try:
            conn.request('POST', '/__ack', body=b'', headers={
                'X-Dp22-Admin': self.token,
                'X-Dp22-Instance': self.name,
                'X-Dp22-Admission': admission})
            response = conn.getresponse()
            response.read()
            return response.status == 204
        except OSError:
            return False
        finally:
            conn.close()

    def finish(self, request_id: str, outcome: str) -> None:
        gate = self.completions.get(request_id)
        if gate is not None:
            # Explicit external test barrier, not a timed fake settlement.
            if not gate.wait(20):
                outcome = 'transport_unknown_completion_barrier_expired'
        with self.db() as db:
            db.execute('UPDATE requests SET state=? WHERE id=?', (outcome, request_id))
        with self.mutex:
            self.active -= 1
            if self.phase == 'draining' and self.active == 0:
                self.exit.set()

    def rpc_loop(self) -> None:
        while not self.exit.is_set():
            try:
                connection, _ = self.rpc.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            with connection:
                connection.sendall(b'transport-fixture-rpc-alive\n')


class HTTPServer(ThreadingHTTPServer):
    daemon_threads = True
    block_on_close = False


class Handler(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    @property
    def core(self) -> Core:
        return self.server.core  # type: ignore[attr-defined]

    def log_message(self, *_: object) -> None:
        pass

    def reply(self, status: int, value: dict, close: bool = False) -> None:
        payload = json.dumps(value).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(payload)))
        self.send_header('Cache-Control', 'no-store')
        self.send_header('X-Dp22-Instance', self.core.name)
        if close:
            self.send_header('Connection', 'close')
            self.close_connection = True
        self.end_headers()
        self.wfile.write(payload)
        self.wfile.flush()

    def authorized(self) -> bool:
        return self.headers.get('X-Dp22-Admin') == self.core.token

    def do_GET(self) -> None:
        if self.path == '/health/live':
            return self.reply(200, {'alive': True})
        if self.path == '/health/ready':
            return self.reply(503, {'model_ready': False, 'billing': 'not_connected'})
        if self.path == '/__probe':
            status = self.core.status()
            return self.reply(200 if status['phase'] == 'ready' else 503, status)
        if self.path == '/__status' and self.authorized():
            return self.reply(200, self.core.status())
        self.reply(404, {'error': 'not_found'})

    def do_POST(self) -> None:
        try:
            size = int(self.headers.get('Content-Length', '-1'))
        except ValueError:
            size = -1
        if not 0 <= size <= MAX_BODY:
            return self.reply(413, {'error': 'body_limit'}, close=True)
        body = self.rfile.read(size)
        if self.path.startswith('/__'):
            if not self.authorized():
                return self.reply(403, {'error': 'not_authorized'})
            if self.path == '/__prepare':
                ok = self.core.prepare()
                return self.reply(200 if ok else 503, self.core.status())
            if self.path.startswith('/__complete/'):
                key = self.path.removeprefix('/__complete/')
                gate = self.core.completions.get(key)
                if gate is None:
                    return self.reply(404, {'error': 'no_completion'})
                gate.set()
                return self.reply(200, {'released': key})
            return self.reply(404, {'error': 'not_found'})
        if self.path != '/transport':
            return self.reply(503, {'error': 'model_routes_remain_disabled'})
        try:
            data = json.loads(body)
            key = data['id']
            if not isinstance(key, str) or not 1 <= len(key) <= 80:
                raise ValueError('id')
            if not self.core.begin(key, bool(data.get('defer', False))):
                return self.reply(503, {'error': 'draining'}, close=True)
        except sqlite3.IntegrityError:
            return self.reply(409, {'error': 'duplicate_not_replayed'}, close=True)
        except (ValueError, KeyError, TypeError):
            return self.reply(400, {'error': 'invalid_body'}, close=True)
        outcome = 'transport_unknown'
        admission = self.headers.get('X-Dp22-Admission')
        if not admission or not self.core.ack_admission(admission):
            # No upstream side effect occurred. Keep an explicit transport
            # record and decline safely; do not invent an ACK or retry.
            self.core.finish(key, 'transport_ack_failed_before_upstream')
            return self.reply(503, {'error': 'route_admission_not_confirmed'}, close=True)
        connection = http.client.HTTPConnection('127.0.0.1', self.core.upstream, timeout=20)
        try:
            # Exactly one outbound POST. There is deliberately no retry loop.
            connection.request('POST', '/stream', body=body,
                               headers={'Content-Type': 'application/json'})
            upstream = connection.getresponse()
            if upstream.status != 200:
                self.reply(502, {'error': 'upstream_rejected'})
                return
            self.send_response(200)
            self.send_header('Content-Type', 'text/event-stream')
            self.send_header('Cache-Control', 'no-store')
            self.send_header('Transfer-Encoding', 'chunked')
            self.send_header('X-Dp22-Instance', self.core.name)
            self.end_headers()
            terminal = False
            while line := upstream.readline():
                terminal = terminal or line == b'data: [DONE]\n'
                self.wfile.write(f'{len(line):x}\r\n'.encode() + line + b'\r\n')
                self.wfile.flush()
            if not terminal:
                raise OSError('upstream closed without terminal')
            self.wfile.write(b'0\r\n\r\n')
            self.wfile.flush()
            outcome = 'transport_complete'
        except (OSError, http.client.HTTPException):
            self.close_connection = True
        finally:
            connection.close()
            self.core.finish(key, outcome)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument('--name', required=True)
    parser.add_argument('--run-dir', type=Path, required=True)
    parser.add_argument('--upstream-port', type=int, required=True)
    args = parser.parse_args()
    token = os.environ['DP22_ADMIN_TOKEN']
    core = Core(args.name, args.run_dir, args.upstream_port, token)
    server = HTTPServer(('127.0.0.1', 0), Handler)
    server.core = core  # type: ignore[attr-defined]
    signal.signal(signal.SIGTERM, lambda *_: core.drain())
    signal.signal(signal.SIGINT, lambda *_: core.drain())
    threading.Thread(target=core.rpc_loop, daemon=True).start()
    threading.Thread(target=server.serve_forever, kwargs={'poll_interval': 0.05}, daemon=True).start()
    print(json.dumps({'instance': core.name, 'port': server.server_address[1],
                      'pid': os.getpid(), 'run_dir': str(core.root)}), flush=True)
    core.exit.wait()
    server.shutdown()
    server.server_close()
    core.rpc.close()
    path = core.root / 'rpc.sock'
    if path.exists() and path.stat().st_ino == core.rpc_inode:
        path.unlink()
    core.lock_file.close()
    (core.root / 'exit.json').write_text(json.dumps(core.status()) + '\n')


if __name__ == '__main__':
    main()
