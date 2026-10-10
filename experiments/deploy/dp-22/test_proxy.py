#!/usr/bin/env python3
"""Real Nginx H1/H2/TLS/SSE checks against a PYTHON transport reference.

No result from this file proves Rust compilation, Rust stream behavior,
PostgreSQL settlement, production capacity, or paid-model readiness.
"""
from __future__ import annotations

from collections import Counter
import hashlib
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import pwd
import select
import shutil
import signal
import socket
import sqlite3
import ssl
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import uuid

import h2.config
import h2.connection
import h2.events
from controller import Controller, Instance, RolloutBlocked, Routes, request, watch_completions

HERE = Path(__file__).resolve().parent
EXPECTED = b''.join(f'data: {i}\n\n'.encode() for i in range(6)) + b'data: [DONE]\n\n'
EVIDENCE = HERE / 'evidence' / 'v3'


def until(predicate, seconds: float = 4):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        value = predicate()
        if value:
            return value
        # Poll a condition. Elapsed time alone NEVER marks readiness.
        threading.Event().wait(0.01)
    raise AssertionError('condition deadline exceeded')



class Upstream:
    def __init__(self) -> None:
        self.lock = threading.Lock()
        self.calls: Counter[str] = Counter()
        self.gates: dict[str, threading.Event] = {}
        self.bad_handshake = False
        owner = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = 'HTTP/1.1'

            def log_message(self, *_: object) -> None:
                pass

            def handle(self) -> None:
                try:
                    super().handle()
                except (ConnectionResetError, BrokenPipeError):
                    self.close_connection = True  # Expected cancellation evidence.

            def do_GET(self) -> None:
                data = json.dumps({'protocol': 'bad' if owner.bad_handshake else 'dp22-sse-1',
                                   'paid': False}).encode()
                self.send_response(200)
                self.send_header('Content-Length', str(len(data)))
                self.end_headers()
                self.wfile.write(data)

            def do_POST(self) -> None:
                n = int(self.headers.get('Content-Length', '0'))
                if not 0 < n <= 4096:
                    self.send_error(413)
                    return
                data = json.loads(self.rfile.read(n))
                key = data['id']
                gate = threading.Event()
                if not data.get('hold'):
                    gate.set()
                with owner.lock:
                    owner.calls[key] += 1
                    owner.gates[key] = gate
                self.send_response(200)
                self.send_header('Content-Type', 'text/event-stream')
                self.send_header('Transfer-Encoding', 'chunked')
                self.end_headers()
                try:
                    for i in range(6):
                        if i == 1 and not gate.wait(20):
                            return
                        block = f'data: {i}\n\n'.encode()
                        self.wfile.write(f'{len(block):x}\r\n'.encode() + block + b'\r\n')
                        self.wfile.flush()
                    block = b'data: [DONE]\n\n'
                    self.wfile.write(f'{len(block):x}\r\n'.encode() + block + b'\r\n0\r\n\r\n')
                    self.wfile.flush()
                except OSError:
                    self.close_connection = True

        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.server.daemon_threads = True
        self.thread = threading.Thread(target=self.server.serve_forever,
                                       kwargs={'poll_interval': 0.02}, daemon=True)
        self.thread.start()
        self.port = self.server.server_address[1]

    def release(self, key: str) -> None:
        with self.lock:
            self.gates[key].set()

    def close(self) -> None:
        with self.lock:
            for gate in self.gates.values():
                gate.set()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(1)


class H2Client:
    def __init__(self, port: int) -> None:
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
        context.check_hostname = False
        context.verify_mode = ssl.CERT_NONE  # Temporary loopback certificate ONLY.
        context.set_alpn_protocols(['h2'])
        self.socket = context.wrap_socket(socket.create_connection(('127.0.0.1', port), timeout=3),
                                          server_hostname='localhost')
        assert self.socket.selected_alpn_protocol() == 'h2'
        self.connection = h2.connection.H2Connection(config=h2.config.H2Configuration(
            client_side=True, header_encoding='utf-8'))
        self.connection.initiate_connection()
        self.socket.sendall(self.connection.data_to_send())
        self.streams: dict[int, dict] = {}
        self.local_port = self.socket.getsockname()[1]
        self.connection_errors: list[str] = []

    def start(self, key: str, *, request_headers: dict | None = None, **options) -> int:
        stream = self.connection.get_next_available_stream_id()
        body = json.dumps({'id': key, **options}).encode()
        self.streams[stream] = {'body': b'', 'ended': False, 'headers': {}, 'error': None}
        headers = [(':method', 'POST'), (':scheme', 'https'),
            (':authority', 'localhost'), (':path', '/transport'),
            ('content-type', 'application/json'), ('content-length', str(len(body)))]
        headers.extend((str(k).lower(), str(v)) for k, v in (request_headers or {}).items())
        self.connection.send_headers(stream, headers)
        self.connection.send_data(stream, body, end_stream=True)
        self.socket.sendall(self.connection.data_to_send())
        return stream

    def pump(self) -> None:
        data = self.socket.recv(65536)
        if not data:
            for item in self.streams.values():
                if not item['ended']:
                    item['error'] = 'TCP_closed'
            return
        for event in self.connection.receive_data(data):
            stream = getattr(event, 'stream_id', None)
            if isinstance(event, h2.events.ResponseReceived):
                self.streams[stream]['headers'] = dict(event.headers)
            elif isinstance(event, h2.events.DataReceived):
                self.streams[stream]['body'] += event.data
                self.connection.acknowledge_received_data(event.flow_controlled_length, stream)
            elif isinstance(event, h2.events.StreamEnded):
                self.streams[stream]['ended'] = True
            elif isinstance(event, h2.events.StreamReset):
                self.streams[stream]['error'] = f'RST_STREAM:{int(event.error_code)}'
            elif isinstance(event, h2.events.ConnectionTerminated):
                message = f'GOAWAY:{int(event.error_code)}'
                self.connection_errors.append(message)
                for item in self.streams.values():
                    if not item['ended']:
                        item['error'] = message
        output = self.connection.data_to_send()
        if output:
            self.socket.sendall(output)

    def first(self, stream: int) -> None:
        deadline = time.monotonic() + 4
        while b'\n\n' not in self.streams[stream]['body']:
            assert time.monotonic() < deadline
            self.pump()
            assert not self.streams[stream]['error'], self.streams[stream]

    def finish(self, stream: int) -> dict:
        deadline = time.monotonic() + 4
        while not self.streams[stream]['ended'] and not self.streams[stream]['error']:
            assert time.monotonic() < deadline
            self.pump()
        return self.streams[stream]

    def cancel(self, stream: int) -> None:
        self.connection.reset_stream(stream, error_code=8)
        self.socket.sendall(self.connection.data_to_send())

    def close(self) -> None:
        self.socket.close()


class Lab:
    def __init__(self, name: str) -> None:
        self.name = name
        self.root = Path(tempfile.mkdtemp(prefix='dp22-', dir='/tmp'))
        self.token = uuid.uuid4().hex
        self.upstream = Upstream()
        self.routes = Routes(self.token)
        self.controller = Controller(self.routes, self.token)
        self.route_server = ThreadingHTTPServer(('127.0.0.1', 0), self.routes.handler())
        self.route_server.daemon_threads = True
        self.route_thread = threading.Thread(target=self.route_server.serve_forever,
                                            kwargs={'poll_interval': 0.02}, daemon=True)
        self.route_thread.start()
        self.children: dict[str, tuple[Instance, subprocess.Popen]] = {}
        self.clients: list = []
        self.observations: dict = {'scope': 'real_nginx_python_transport_reference',
                                  'rust_executed': False, 'billing_executed': False}
        self.nginx = None
        self.nginx_err = None
        self.start_proxy()
        self.completion_stop = threading.Event()
        self.completion_thread = threading.Thread(target=watch_completions,
            args=(self.root / 'nginx' / 'completion.log', self.routes, self.completion_stop),
            daemon=True)
        self.completion_thread.start()

    def start_proxy(self) -> None:
        prefix = self.root / 'nginx'
        prefix.mkdir(mode=0o700)
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
            '-keyout', str(prefix / 'key.pem'), '-out', str(prefix / 'cert.pem'),
            '-days', '1', '-subj', '/CN=localhost'], check=True,
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10)
        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            self.port = probe.getsockname()[1]
        config = f'''user {pwd.getpwuid(os.geteuid()).pw_name};
worker_processes 1;
daemon off;
pid {prefix}/nginx.pid;
error_log {prefix}/error.log info;
events {{ worker_connections 64; }}
http {{
    log_format proof '$connection $connection_requests $server_protocol '
                     '$request_method $uri $status $upstream_addr';
    access_log {prefix}/access.log proof;
    log_format completed escape=json '{{"request_id":"$request_id","status":$status}}';
    access_log {prefix}/completion.log completed;
    client_body_temp_path {prefix}/body;
    proxy_temp_path {prefix}/proxy;
    server {{
        listen 127.0.0.1:{self.port} ssl;
        http2 on;
        http2_max_concurrent_streams 16;
        ssl_certificate {prefix}/cert.pem;
        ssl_certificate_key {prefix}/key.pem;
        client_max_body_size 4k;
        client_body_timeout 5s;
        keepalive_timeout 30s;
        location = /__nginx_probe {{ return 204; }}
        location = /_route {{
            internal;
            proxy_pass http://127.0.0.1:{self.route_server.server_address[1]}/route;
            proxy_pass_request_body off;
            proxy_set_header Content-Length "";
            proxy_set_header X-Original-URI $request_uri;
            proxy_set_header X-Dp22-Request-Id $request_id;
            proxy_set_header X-Dp22-Probe $http_x_dp22_probe;
            proxy_set_header X-Dp22-Candidate $http_x_dp22_candidate;
            proxy_set_header X-Dp22-Test-Barrier $http_x_dp22_test_barrier;
            proxy_connect_timeout 1s;
            proxy_read_timeout 2s;
        }}
        location / {{
            auth_request /_route;
            auth_request_set $dp22_target $upstream_http_x_dp22_target;
            auth_request_set $dp22_admission $upstream_http_x_dp22_admission;
            proxy_pass http://$dp22_target$request_uri;
            proxy_set_header X-Dp22-Admission $dp22_admission;
            proxy_http_version 1.1;
            proxy_set_header Connection "";
            proxy_buffering off;
            proxy_ignore_client_abort off;
            proxy_next_upstream off;
            proxy_connect_timeout 1s;
            proxy_read_timeout 25s;
            proxy_send_timeout 5s;
        }}
    }}
}}
'''
        (prefix / 'nginx.conf').write_text(config)
        subprocess.run(['nginx', '-t', '-p', str(prefix), '-c', str(prefix / 'nginx.conf')],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, timeout=5)
        self.nginx_err = open(prefix / 'launcher.log', 'wb')
        self.nginx = subprocess.Popen(['prlimit', '--as=268435456:268435456',
            '--nofile=128:128', '--cpu=30:35', '--core=0:0', '--fsize=16777216:16777216',
            '--', 'nginx', '-p', str(prefix), '-c', str(prefix / 'nginx.conf')],
            stdout=self.nginx_err, stderr=self.nginx_err)

        def listening():
            if self.nginx.poll() is not None:
                raise AssertionError((prefix / 'launcher.log').read_text())
            try:
                connection = self.h1()
                connection.request('GET', '/__nginx_probe')
                response = connection.getresponse()
                result = response.status == 204
                response.read()
                connection.close()
                return result
            except OSError:
                return False
        until(listening)

    def h1(self) -> http.client.HTTPSConnection:
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
        context.check_hostname = False
        context.verify_mode = ssl.CERT_NONE
        context.set_alpn_protocols(['http/1.1'])
        connection = http.client.HTTPSConnection('127.0.0.1', self.port, context=context, timeout=4)
        self.clients.append(connection)
        return connection

    def h2(self) -> H2Client:
        connection = H2Client(self.port)
        self.clients.append(connection)
        return connection

    def proxy_probe(self, candidate: str | None) -> str:
        connection = self.h1()
        headers = {} if candidate is None else {'X-Dp22-Probe': self.token,
                                                'X-Dp22-Candidate': candidate}
        connection.request('GET', '/__probe', headers=headers)
        response = connection.getresponse()
        data = json.loads(response.read())
        connection.close()
        if response.status != 200:
            raise RolloutBlocked('proxy probe failed')
        return data['instance']

    def start(self, name: str, prepare: bool = True) -> Instance:
        self.controller.reserve(name, candidate_mib=128, free_mib=384, headroom_mib=64)
        err = open(self.root / f'{name}.stderr', 'wb')
        process = subprocess.Popen(['prlimit', '--as=536870912:536870912',
            '--nofile=128:128', '--cpu=30:35', '--core=0:0', '--fsize=16777216:16777216',
            '--', sys.executable, str(HERE / 'core_fixture.py'),
            '--name', name, '--run-dir', str(self.root / name),
            '--upstream-port', str(self.upstream.port)], stdout=subprocess.PIPE, stderr=err,
            env={'PATH': os.environ['PATH'], 'PYTHONUNBUFFERED': '1',
                 'DP22_ADMIN_TOKEN': self.token,
                 'DP22_ROUTE_PORT': str(self.route_server.server_address[1])})
        err.close()
        if not select.select([process.stdout], [], [], 12)[0]:
            process.kill(); process.wait(timeout=2); process.stdout.close()
            raise AssertionError('fixture descriptor deadline; child reaped')
        line = process.stdout.readline()
        if not line:
            process.kill(); process.wait(timeout=2); process.stdout.close()
            raise AssertionError((self.root / f'{name}.stderr').read_text())
        data = json.loads(line)
        instance = Instance(name, data['port'], data['pid'], Path(data['run_dir']))
        self.children[name] = (instance, process)
        self.controller.register(instance)
        if prepare:
            status, proof = request(instance, self.token, '/__prepare', 'POST')
            assert status == 200, proof
        return instance

    def activate(self, instance: Instance) -> None:
        self.controller.switch(instance, self.proxy_probe)

    def status(self, instance: Instance) -> dict:
        return request(instance, self.token, '/__status')[1]

    def wait_exit(self, instance: Instance) -> int:
        return self.children[instance.name][1].wait(timeout=4)

    def body(self, client, key: str, **options):
        client.request('POST', '/transport', body=json.dumps({'id': key, **options}),
                       headers={'Content-Type': 'application/json'})
        response = client.getresponse()
        assert response.status == 200, (response.status, response.read())
        assert response.version == 11
        return response

    def records(self, instance: Instance) -> dict:
        db = sqlite3.connect(instance.run_dir / 'transport.sqlite')
        try:
            return dict(db.execute('SELECT id,state FROM requests ORDER BY id').fetchall())
        finally:
            db.close()

    def close(self) -> None:
        for client in self.clients:
            client.close()
        with self.upstream.lock:
            for gate in self.upstream.gates.values():
                gate.set()
        cleanup_kills = []
        for instance, process in self.children.values():
            if process.poll() is None:
                try:
                    status = self.status(instance)
                    for key in status['requests']:
                        request(instance, self.token, f'/__complete/{key}', 'POST')
                except (OSError, http.client.HTTPException, ValueError):
                    pass
                process.terminate()
                try:
                    process.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    cleanup_kills.append(instance.name)
                    process.kill()
                    process.wait(timeout=2)
            if process.stdout:
                process.stdout.close()
        if self.nginx is not None and self.nginx.poll() is None:
            self.nginx.send_signal(signal.SIGQUIT)
            try:
                self.nginx.wait(timeout=2)
            except subprocess.TimeoutExpired:
                self.nginx.kill()
                self.nginx.wait(timeout=2)
                cleanup_kills.append('nginx')
        if self.nginx_err is not None:
            self.nginx_err.close()
        self.completion_stop.set()
        self.completion_thread.join(1)
        self.route_server.shutdown()
        self.route_server.server_close()
        self.route_thread.join(1)
        self.upstream.close()
        self.observations['upstream_calls'] = dict(self.upstream.calls)
        self.observations['controller_events'] = self.controller.events
        self.observations['pending_ingress_tickets'] = len(self.routes.pending)
        self.observations['records'] = {name: self.records(instance)
                                       for name, (instance, _) in self.children.items()}
        self.observations['exit_codes'] = {name: p.returncode for name, (_, p) in self.children.items()}
        self.observations['remaining_child_processes'] = sum(p.poll() is None for _, p in self.children.values())
        self.observations['cleanup_forced_kills'] = cleanup_kills
        self.observations['nginx_exit_code'] = self.nginx.returncode if self.nginx else None
        destination = EVIDENCE / self.name
        destination.mkdir(parents=True, exist_ok=True)
        for filename in ['access.log', 'completion.log', 'error.log', 'nginx.conf']:
            path = self.root / 'nginx' / filename
            if path.exists():
                shutil.copy2(path, destination / filename)
        (destination / 'observations.json').write_text(json.dumps(self.observations, indent=2) + '\n')
        shutil.rmtree(self.root)


class RolloutTests(unittest.TestCase):
    def setUp(self):
        self.lab = Lab(self._testMethodName)

    def tearDown(self):
        self.lab.close()
        self.assertTrue(all(n == 1 for n in self.lab.upstream.calls.values()))
        self.assertEqual(self.lab.observations['remaining_child_processes'], 0)
        self.assertEqual(self.lab.observations['cleanup_forced_kills'], [])

    def assert_stream(self, body: bytes):
        self.assertEqual(body, EXPECTED)
        self.lab.observations.setdefault('complete_body_sha256', []).append(hashlib.sha256(body).hexdigest())

    def test_01_control_http1_keepalive(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        client = lab.h1()
        first = lab.body(client, 'control-h1-old', hold=True)
        prefix = first.readline() + first.readline()
        port = client.sock.getsockname()[1]
        lab.upstream.release('control-h1-old')
        self.assert_stream(prefix + first.read())
        second = lab.body(client, 'control-h1-new')
        self.assert_stream(second.read())
        self.assertEqual(client.sock.getsockname()[1], port)
        until(lambda: lab.status(a)['active'] == 0)
        lab.observations.update({'same_client_tcp': True, 'updated': False, 'protocol': 'HTTP/1.1'})

    def test_02_control_http2_multiplex(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        client = lab.h2()
        old = client.start('control-h2-old', hold=True); client.first(old)
        new = client.start('control-h2-new')
        self.assert_stream(client.finish(new)['body'])
        self.assertFalse(client.streams[old]['ended'])
        lab.upstream.release('control-h2-old')
        self.assert_stream(client.finish(old)['body'])
        lab.observations.update({'same_client_tcp': True, 'simultaneous_stream_ids': [old, new],
                                 'updated': False, 'protocol': 'h2'})

    def test_03_rollout_http1_old_keepalive_new_work(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        idle = lab.h1()
        idle.request('GET', '/__probe'); idle.getresponse().read()
        idle_port = idle.sock.getsockname()[1]
        stream_client = lab.h1()
        stream = lab.body(stream_client, 'rollout-h1-old', hold=True)
        prefix = stream.readline() + stream.readline()
        b = lab.start('b'); lab.activate(b)
        until(lambda: lab.status(a)['phase'] == 'draining')
        self.assertIsNone(lab.children['a'][1].poll())
        newer = lab.body(idle, 'rollout-h1-new')
        self.assertEqual(newer.getheader('X-Dp22-Instance'), 'b')
        self.assert_stream(newer.read())
        self.assertEqual(idle.sock.getsockname()[1], idle_port)
        lab.upstream.release('rollout-h1-old')
        self.assert_stream(prefix + stream.read())
        self.assertEqual(lab.wait_exit(a), 0)
        self.assertFalse((a.run_dir / 'rpc.sock').exists())
        lab.observations.update({'same_client_tcp': True, 'updated': True, 'protocol': 'HTTP/1.1',
                                 'old_stream_completed': True, 'new_backend': 'b'})

    def test_04_rollout_http2_same_tcp_multiplex(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        client = lab.h2()
        old = client.start('rollout-h2-old', hold=True); client.first(old)
        b = lab.start('b'); lab.activate(b)
        new = client.start('rollout-h2-new')
        newer = client.finish(new)
        self.assertEqual(newer['headers']['x-dp22-instance'], 'b')
        self.assert_stream(newer['body'])
        self.assertFalse(client.streams[old]['ended'])
        self.assertEqual(client.socket.getsockname()[1], client.local_port)
        lab.upstream.release('rollout-h2-old')
        older = client.finish(old)
        self.assertEqual(older['headers']['x-dp22-instance'], 'a')
        self.assert_stream(older['body'])
        self.assertEqual(lab.wait_exit(a), 0)
        lab.observations.update({'same_client_tcp': True, 'simultaneous_stream_ids': [old, new],
                                 'updated': True, 'protocol': 'h2', 'no_goaway': not client.connection_errors})

    def test_05_live_200_is_not_preparation(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        b = lab.start('b', prepare=False)
        self.assertEqual(request(b, lab.token, '/health/live')[0], 200)
        with self.assertRaises(RolloutBlocked):
            lab.activate(b)
        self.assertEqual(lab.routes.current, 'a')
        self.assertEqual(lab.status(a)['phase'], 'ready')
        lab.upstream.bad_handshake = True
        self.assertEqual(request(b, lab.token, '/__prepare', 'POST')[0], 503)
        with self.assertRaises(RolloutBlocked):
            lab.activate(b)
        self.assertEqual(request(b, lab.token, '/health/ready')[0], 503)

    def test_06_cutover_failure_does_not_drain_old(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        b = lab.start('b')
        lab.routes.fail_before_commit = True
        with self.assertRaises(RolloutBlocked):
            lab.activate(b)
        self.assertEqual(lab.routes.current, 'a')
        self.assertEqual(lab.status(a)['phase'], 'ready')
        self.assert_stream(lab.body(lab.h1(), 'cutover-failed-still-old').read())

    def test_07_generation_and_memory_limits_before_launch(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        stream = lab.body(lab.h1(), 'capacity-old', hold=True)
        prefix = stream.readline() + stream.readline()
        b = lab.start('b'); lab.activate(b)
        with self.assertRaises(RolloutBlocked):
            lab.controller.reserve('c', candidate_mib=128, free_mib=999, headroom_mib=0)
        self.assertIsNone(lab.children['a'][1].poll())
        self.assertEqual(set(lab.children), {'a', 'b'})
        lab.upstream.release('capacity-old')
        self.assert_stream(prefix + stream.read())
        lab.wait_exit(a); lab.controller.reap(a, lab.children['a'][1].returncode)
        for free in [None, 150]:
            with self.assertRaises(RolloutBlocked):
                lab.controller.reserve('c', candidate_mib=128, free_mib=free, headroom_mib=64)
        lab.observations['capacity_inputs_are_synthetic_not_1c1g_measurements'] = True

    def test_08_http2_cancel_releases_reference_resources(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        client = lab.h2()
        stream = client.start('cancel-old', hold=True); client.first(stream)
        client.cancel(stream)
        lab.upstream.release('cancel-old')
        until(lambda: lab.status(a)['active'] == 0)
        self.assertEqual(lab.upstream.calls['cancel-old'], 1)
        fresh = client.start('after-cancel')
        self.assert_stream(client.finish(fresh)['body'])
        lab.observations['client_cancel'] = 'RST_STREAM(CANCEL)'

    def test_09_completion_barrier_and_drain_deadline_do_not_kill(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        self.assert_stream(lab.body(lab.h1(), 'completion-old', defer=True).read())
        b = lab.start('b'); lab.activate(b)
        until(lambda: lab.status(a)['phase'] == 'draining')
        with self.assertRaises(subprocess.TimeoutExpired):
            lab.children['a'][1].wait(timeout=0.08)
        self.assertIsNone(lab.children['a'][1].poll())
        self.assertEqual(lab.status(a)['active'], 1)
        self.assertEqual(lab.records(a)['completion-old'], 'transport_open')
        with socket.socket(socket.AF_UNIX) as rpc:
            rpc.connect(str(a.run_dir / 'rpc.sock'))
            self.assertEqual(rpc.recv(80), b'transport-fixture-rpc-alive\n')
        self.assertEqual(request(a, lab.token, '/__complete/completion-old', 'POST')[0], 200)
        self.assertEqual(lab.wait_exit(a), 0)
        self.assertEqual(lab.records(a)['completion-old'], 'transport_complete')
        lab.observations['completion_is_transport_barrier_not_money_settlement'] = True

    def test_10_sigkill_breaks_stream_retains_unknown_without_repost(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        client = lab.h2()
        old = client.start('killed-old', hold=True); client.first(old)
        b = lab.start('b'); lab.activate(b)
        lab.children['a'][1].kill(); lab.wait_exit(a)
        result = client.finish(old)
        self.assertTrue(result['error'] or result['body'] != EXPECTED)
        self.assertEqual(lab.records(a)['killed-old'], 'transport_open')
        self.assertEqual(lab.upstream.calls['killed-old'], 1)
        lab.upstream.release('killed-old')
        # A new request, not replay of the lost POST or migration of its stream.
        fresh = client.start('after-kill-new-request')
        self.assert_stream(client.finish(fresh)['body'])
        lab.observations['killed_stream_result'] = {'error': result['error'],
            'bytes_received': len(result['body']), 'expected_bytes': len(EXPECTED)}
        lab.observations['durable_recovery_candidate'] = 'killed-old'

    def test_11_socket_paths_and_direct_old_connection_gate(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        direct = http.client.HTTPConnection('127.0.0.1', a.port, timeout=3)
        lab.clients.append(direct)
        direct.request('GET', '/health/live'); direct.getresponse().read()
        old_socket = (a.run_dir / 'rpc.sock').stat().st_ino
        with socket.socket(socket.AF_UNIX) as collision:
            with self.assertRaises(OSError):
                collision.bind(str(a.run_dir / 'rpc.sock'))
        self.assertEqual((a.run_dir / 'rpc.sock').stat().st_ino, old_socket)
        stream = lab.body(lab.h1(), 'direct-gate-held', hold=True)
        prefix = stream.readline() + stream.readline()
        b = lab.start('b'); lab.activate(b)
        self.assertNotEqual(a.port, b.port)
        self.assertNotEqual(a.run_dir, b.run_dir)
        until(lambda: lab.status(a)['phase'] == 'draining')
        direct.request('POST', '/transport', body=json.dumps({'id': 'must-not-reach-upstream'}))
        denied = direct.getresponse()
        self.assertEqual(denied.status, 503)
        self.assertEqual(denied.getheader('Connection'), 'close'); denied.read()
        self.assertNotIn('must-not-reach-upstream', lab.upstream.calls)
        lab.upstream.release('direct-gate-held')
        self.assert_stream(prefix + stream.read()); lab.wait_exit(a)

    def test_12_uncertain_cutover_keeps_both_instances(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        b = lab.start('b')

        def lost_ack(candidate):
            if candidate is None:
                raise OSError('injected acknowledgement loss after route commit')
            return lab.proxy_probe(candidate)

        with self.assertRaises(OSError):
            lab.controller.switch(b, lost_ack)
        self.assertEqual(lab.controller.state, 'cutover_unknown')
        self.assertEqual(lab.routes.current, 'b')
        self.assertEqual(lab.status(a)['phase'], 'ready')
        self.assertEqual(lab.status(b)['phase'], 'ready')
        with self.assertRaises(RolloutBlocked):
            lab.activate(b)
        lab.observations['uncertain_cutover_no_automatic_rollback'] = True

    def test_13_route_authority_loss_no_retry_or_old_stream_kill(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        stream = lab.body(lab.h1(), 'authority-loss-old', hold=True)
        prefix = stream.readline() + stream.readline()
        lab.routes.unavailable = True
        client = lab.h1()
        client.request('POST', '/transport', body=json.dumps({'id': 'blocked-new'}))
        response = client.getresponse()
        self.assertGreaterEqual(response.status, 500); response.read()
        self.assertNotIn('blocked-new', lab.upstream.calls)
        lab.upstream.release('authority-loss-old')
        self.assert_stream(prefix + stream.read())
        lab.observations['route_authority_is_single_point_of_failure'] = True

    def test_14_preselected_request_stays_admitted_before_old_drain(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        held = lab.body(lab.h1(), 'race-existing-stream', hold=True)
        prefix = held.readline() + held.readline()
        b = lab.start('b')
        selected, resume = threading.Event(), threading.Event()
        result = {}

        def pause(name, headers):
            if headers.get('X-Dp22-Test-Barrier') == 'selected-before-cutover':
                result['selected_backend'] = name
                selected.set()
                if not resume.wait(5):
                    raise AssertionError('race injection deadline')

        lab.routes.test_before_reply = pause
        client = lab.h1()

        def late_request():
            try:
                client.request('POST', '/transport', body=json.dumps({'id': 'race-late-admission'}),
                    headers={'X-Dp22-Test-Barrier': 'selected-before-cutover'})
                response = client.getresponse()
                result['status'] = response.status
                result['body'] = response.read().decode()
            except Exception as error:
                result['error'] = repr(error)

        thread = threading.Thread(target=late_request, daemon=True)
        thread.start()
        try:
            self.assertTrue(selected.wait(3))
            lab.activate(b)
            # The selected but not yet admitted request keeps A accepting.
            self.assertEqual(lab.status(a)['phase'], 'ready')
            self.assertTrue(any(e['event'] == 'old_drain_waits_for_admission'
                                for e in lab.controller.events))
        finally:
            resume.set()
        thread.join(3)
        self.assertFalse(thread.is_alive())
        self.assertNotIn('error', result)
        self.assertEqual(result['selected_backend'], 'a')
        # Route selection is a registered ticket. Drain follows the backend
        # ACK, never just the proxy's target-selection acknowledgement.
        self.assertEqual(result['status'], 200)
        self.assert_stream(result['body'].encode())
        self.assertEqual(lab.upstream.calls['race-late-admission'], 1)
        self.assertTrue(any(e['event'] == 'old_drain_waits_for_admission'
                            for e in lab.controller.events))
        lab.upstream.release('race-existing-stream')
        self.assert_stream(prefix + held.read()); lab.wait_exit(a)
        lab.observations['selected_before_cutover_completed_once'] = result
        lab.observations['transport_only_preselected_race'] = 'PASSED'

    def test_15_cancel_before_core_admission_revokes_ticket_without_post(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        held = lab.body(lab.h1(), 'cancel-before-ack-held', hold=True)
        prefix = held.readline() + held.readline()
        b = lab.start('b')
        selected, resume = threading.Event(), threading.Event()
        captured = {}

        def pause(name, headers):
            if headers.get('X-Dp22-Test-Barrier') == 'cancel-before-ack':
                captured['request_id'] = headers.get('X-Dp22-Request-Id')
                with lab.routes.lock:
                    captured['ticket'] = lab.routes.request_tickets[captured['request_id']]
                selected.set()
                if not resume.wait(5):
                    raise AssertionError('cancellation barrier deadline')

        lab.routes.test_before_reply = pause
        client = lab.h2()
        stream = client.start('cancel-before-ack-never-post',
                              request_headers={'X-Dp22-Test-Barrier': 'cancel-before-ack'})
        try:
            self.assertTrue(selected.wait(3))
            lab.activate(b)
            self.assertEqual(lab.status(a)['phase'], 'ready')
            client.cancel(stream)
            # Actual ingress request completion, not a ticket TTL, revokes
            # the selection. Even a later core ACK must now be denied.
            until(lambda: not lab.routes.pending)
            self.assertFalse(lab.routes.ack('a', captured['ticket']))
            until(lambda: lab.status(a)['phase'] == 'draining')
        finally:
            resume.set()
        self.assertNotIn('cancel-before-ack-never-post', lab.upstream.calls)
        lab.upstream.release('cancel-before-ack-held')
        self.assert_stream(prefix + held.read())
        self.assertEqual(lab.wait_exit(a), 0)
        lab.observations['client_cancel_before_admission'] = {
            'upstream_posts': lab.upstream.calls.get('cancel-before-ack-never-post', 0),
            'ticket_revoked': True, 'old_stream_completed': True,
            'mechanism': 'Nginx finalized-request access log, not elapsed-time eviction',
        }

    def test_16_missing_or_forged_direct_ticket_never_calls_upstream(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        for key, headers in [('missing-ticket', {}),
                             ('forged-ticket', {'X-Dp22-Admission': 'f' * 32})]:
            direct = http.client.HTTPConnection('127.0.0.1', a.port, timeout=3)
            lab.clients.append(direct)
            direct.request('POST', '/transport', body=json.dumps({'id': key}), headers=headers)
            response = direct.getresponse()
            self.assertEqual(response.status, 503)
            response.read()
            self.assertNotIn(key, lab.upstream.calls)
        self.assertEqual(lab.status(a)['active'], 0)
        lab.observations['missing_and_forged_tickets_rejected_before_upstream'] = True

    def test_17_withdrawn_generation_cannot_be_selected_again(self):
        lab = self.lab
        a = lab.start('a'); lab.activate(a)
        held = lab.body(lab.h1(), 'withdrawn-held', hold=True)
        prefix = held.readline() + held.readline()
        b = lab.start('b'); lab.activate(b)
        with self.assertRaises(RolloutBlocked):
            lab.routes.commit('b', 'a')
        self.assertEqual(lab.routes.current, 'b')
        lab.upstream.release('withdrawn-held')
        self.assert_stream(prefix + held.read())
        lab.wait_exit(a)
        lab.observations['withdrawn_generation_reselection_blocked'] = True


if __name__ == '__main__':
    EVIDENCE.mkdir(parents=True, exist_ok=True)
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(RolloutTests)
    started = time.monotonic()
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    summary = {'tests_run': result.testsRun, 'failures': len(result.failures),
        'errors': len(result.errors), 'elapsed_seconds': round(time.monotonic() - started, 3),
        'scope': 'real_nginx_python_transport_reference',
        'rust_runtime': 'NOT_RUN_NO_TOOLCHAIN', 'billing': 'NOT_CONNECTED_NOT_TESTED',
        'normal_rollout_acceptance': 'NOT_PASSED_REAL_RUST_AND_SETTLEMENT_NOT_TESTED',
        'known_gaps': ['Rust ACK adapter is written but not compiled or executed',
                       'unknown request completion intentionally retains a bounded ticket; '
                       'the ingress authority is still an in-memory research service'],
        'base': '72667564c0431754d4856dc2e0db55f360bd2745',
        'nginx': subprocess.run(['nginx', '-v'], capture_output=True, text=True).stderr.strip(),
        'details': [{'test': test.id(), 'traceback': tb} for test, tb in result.failures + result.errors]}
    (EVIDENCE / 'summary.json').write_text(json.dumps(summary, indent=2) + '\n')
    sys.exit(not result.wasSuccessful())
