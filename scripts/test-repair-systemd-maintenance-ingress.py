#!/usr/bin/env python3
"""Offline ingress repair contracts and localhost-only nginx/TLS fixtures."""
import sys
sys.dont_write_bytecode = True

import contextlib
import fcntl
import hashlib
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
import os
from pathlib import Path
import shutil
import socket
import ssl
import subprocess
import tempfile
import threading
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch


def load_module(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


repair = load_module('repair_systemd_ingress', 'repair-systemd-maintenance-ingress.py')
deploy = load_module('deploy_systemd_ingress_fixture', 'deploy-systemd.py')
ACTUAL_ORIGINAL = b'# Temporary whole-origin TLS hop: API, SPA, scripts, public assets and errors.\n# The unchanged Rust probe snippet owns only three exact loopback-only routes.\naccess_log /var/log/nginx/access.log combined if=$lmm_access_loggable;\n\n# No URI component: preserve the incoming path, query and method byte for byte.\n# Use the Arch origin IP, never the public hostname as a routing destination.\nlocation / {\n    proxy_pass https://45.59.187.63;\n    proxy_ssl_server_name on;\n    proxy_ssl_name api.lmm.best;\n    proxy_ssl_verify on;\n    proxy_ssl_verify_depth 4;\n    proxy_ssl_trusted_certificate /etc/ssl/certs/ca-certificates.crt;\n    proxy_next_upstream off;\n    proxy_connect_timeout 2s;\n    proxy_http_version 1.1;\n    proxy_set_header Host api.lmm.best;\n    proxy_set_header X-Real-IP $remote_addr;\n    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n    proxy_set_header X-Forwarded-Proto $scheme;\n    proxy_set_header Upgrade $websocket_upgrade;\n    proxy_set_header Connection $connection_upgrade;\n    proxy_buffering off;\n    proxy_intercept_errors off;\n    proxy_read_timeout 3600s;\n}\n'
ACTUAL_SHA256 = 'ed052676b8c40f6fdcc1581de63531a3f7fa17a2d1fdbf22a50b4862954468b0'


class NormalizationTests(unittest.TestCase):
    def test_exact_actual_config_preserves_every_proxy_body_byte(self):
        self.assertEqual(ACTUAL_SHA256, hashlib.sha256(ACTUAL_ORIGINAL).hexdigest())
        canonical = repair.normalize(ACTUAL_ORIGINAL)
        renamed = ACTUAL_ORIGINAL.replace(b'location / {', b'location @lmm_api_backend {')
        self.assertTrue(canonical.startswith(renamed))
        self.assertEqual(1, canonical.count(b'location @lmm_api_backend {'))
        self.assertEqual(1, canonical.count(b'location / {'))
        self.assertIn(b'error_page 418 = @lmm_api_backend;', canonical[len(renamed):])
        self.assertIn(b'return 418;', canonical[len(renamed):])
        self.assertNotIn(b'lmm-credit-transition:', canonical)

    def test_normalization_refuses_any_unreviewed_config_bytes(self):
        for body in (b'', ACTUAL_ORIGINAL + b'# changed\n', ACTUAL_ORIGINAL.replace(b'proxy_intercept_errors off;', b'proxy_intercept_errors on;')):
            with self.subTest(digest=hashlib.sha256(body).hexdigest()), self.assertRaises(RuntimeError):
                repair.normalize(body)


class RepairContractTests(unittest.TestCase):
    """Real local files and flock guards; service/proc/network are fixtures."""

    def setUp(self):
        self.stack = contextlib.ExitStack()
        self.addCleanup(self.stack.close)
        self.addCleanup(os.umask, os.umask(0o077))  # Match the repair CLI's private-file policy.
        cache = Path.home() / '.cache'
        cache.mkdir(mode=0o700, parents=True, exist_ok=True)
        self.base = Path(self.stack.enter_context(tempfile.TemporaryDirectory(prefix='ingress-repair-contract-', dir=cache)))
        self.uid = os.getuid()
        self.stack.enter_context(patch.object(repair, 'ROOT_UID', self.uid))
        for name, path in {'ROOT': self.base / 'owner', 'BINARY': self.base / 'old-provider',
                           'ENVIRONMENT': self.base / 'current.env', 'FRONTEND': self.base / 'web',
                           'NGINX_LOCATIONS': self.base / 'locations.conf'}.items():
            self.stack.enter_context(patch.object(deploy, name, path))
        deploy.ROOT.mkdir(mode=0o700)
        self.work = deploy.ROOT / 'fixture'
        self.work.mkdir(mode=0o700)
        self.write(deploy.BINARY, b'captured-old-provider', 0o755)
        self.write(self.work / 'previous-binary', deploy.BINARY.read_bytes(), 0o755)
        self.write(self.work / 'lmm-api-go', b'staged-candidate-provider', 0o755)
        self.write(deploy.ENVIRONMENT, b'SQL_DSN=postgresql://fixture/database\n')
        self.write(self.work / 'previous.env', deploy.ENVIRONMENT.read_bytes())
        self.write(deploy.NGINX_LOCATIONS, ACTUAL_ORIGINAL, 0o644)
        self.write(self.work / 'previous-nginx-locations', ACTUAL_ORIGINAL)
        active_frontend = deploy.FRONTEND / 'releases' / 'frozen'
        active_frontend.mkdir(mode=0o700, parents=True)
        self.write(active_frontend / 'index.html', b'frozen-frontend')
        (deploy.FRONTEND / 'current').symlink_to('releases/frozen')
        shutil.copytree(active_frontend, self.work / 'frontend')
        self.pid = 4321
        self.invocation = 'a' * 32
        self.proc_environment = b'SQL_DSN=postgresql://fixture/database\0'
        self.process_path = self.work / ('captured-process.' + self.invocation + '.environment')
        self.write(self.process_path, self.proc_environment)
        identities = {'transition_id': 'fixture', 'transition_intent_sha256': 'b' * 64,
                      'provider_sha256': deploy.digest(self.work / 'lmm-api-go')}
        prepare = self.write(self.base / 'prepare.json', repair.encoded(identities))
        handoff = dict(identities, format='lmm-credit-maintenance-handoff-v1', stage='prebridge',
                       deployment_tool='systemd', prepare_config_path=str(prepare),
                       prepare_config_sha256=deploy.digest(prepare), guardian_socket=str(self.base / 'guardian.sock'))
        handoff_path = self.write(self.base / 'handoff.json', repair.encoded(handoff))
        self.state = dict(identities, release='fixture', version='candidate', sha256=identities['provider_sha256'],
                          frontend_sha256=deploy.tree_digest(self.work / 'frontend'), phase='CAPTURED',
                          maintenance_stage='prebridge', prepare_config_sha256=handoff['prepare_config_sha256'],
                          maintenance_handoff={'path': str(handoff_path), 'sha256': deploy.digest(handoff_path)},
                          previous_version='ordinary-old', previous_sha256=deploy.digest(deploy.BINARY),
                          previous_frontend='frozen', captured_pid=self.pid, captured_invocation_id=self.invocation,
                          archived_environment_sha256=deploy.digest(self.work / 'previous.env'),
                          process_environment_path=str(self.process_path), process_environment_sha256=deploy.digest(self.process_path),
                          ingress_original_sha256=ACTUAL_SHA256)
        capture = {'format': 'lmm-credit-maintenance-capture-v1', 'phase': 'CAPTURED',
                   'transition_id': identities['transition_id'], 'transition_intent_sha256': identities['transition_intent_sha256'],
                   'provider_sha256': self.state['previous_sha256'], 'version': self.state['previous_version'],
                   'pid': self.pid, 'invocation_id': self.invocation, 'archived_environment_path': str(self.work / 'previous.env'),
                   'archived_environment_sha256': self.state['archived_environment_sha256'],
                   'process_environment_path': str(self.process_path), 'process_environment_sha256': self.state['process_environment_sha256'],
                   'frontend_target': 'releases/frozen', 'frontend_sha256': self.state['frontend_sha256'],
                   'was_maintenance_confirmed': False}
        self.capture_path = self.write(self.work / 'maintenance-capture.CAPTURED.json', repair.encoded(capture))
        self.state.update(capture_receipt_path=str(self.capture_path), capture_receipt_sha256=deploy.digest(self.capture_path))
        self.status_path = self.base / 'capture-status.json'
        self.stack.enter_context(patch.object(repair, 'CAPTURE_STATUS_SHA256', ''))
        self.seal_state(self.state)
        self.args = SimpleNamespace(release='fixture', maintenance_handoff=handoff_path,
                                    maintenance_handoff_sha256=deploy.digest(handoff_path),
                                    capture_status=self.status_path, execute=False, confirm='api.lmm.best')
        self.properties = {'MainPID': str(self.pid), 'InvocationID': self.invocation, 'ActiveState': 'active', 'SubState': 'running'}
        self.proc_exe_sha256 = self.state['previous_sha256']
        original_digest, original_cleanup, original_guardian = deploy.digest, deploy.cleanup_path, deploy.verify_cleanup_guardian
        original_handoff, original_read = deploy.guardian.handoff, Path.read_bytes

        def read_bytes(path):
            return self.proc_environment if path == Path('/proc', str(self.pid), 'environ') else original_read(path)

        self.stack.enter_context(patch.object(Path, 'read_bytes', read_bytes))
        self.stack.enter_context(patch.object(deploy, 'digest', side_effect=lambda path: self.proc_exe_sha256 if Path(path) == Path('/proc', str(self.pid), 'exe') else original_digest(path)))
        self.stack.enter_context(patch.object(deploy, 'cleanup_path', side_effect=lambda path, private_file=False: original_cleanup(path, private_file, self.uid)))
        self.stack.enter_context(patch.object(deploy, 'verify_cleanup_guardian', side_effect=lambda receipt: original_guardian(receipt, self.uid)))
        self.stack.enter_context(patch.object(deploy.guardian, 'handoff', side_effect=lambda path, expected: original_handoff(path, expected, self.uid)))
        self.stack.enter_context(patch.object(deploy, 'property_value', side_effect=lambda name: self.properties[name]))
        self.stack.enter_context(patch.object(deploy, 'check_layout'))
        self.health = self.stack.enter_context(patch.object(deploy, 'healthy'))
        self.run = self.stack.enter_context(patch.object(deploy, 'run', side_effect=self.fake_run))
        self.capture_writer = self.stack.enter_context(patch.object(deploy, 'persist_capture', side_effect=AssertionError('original capture receipt may not be rewritten')))
        self.locks = {name: str(self.write(self.base / (name + '.lock'), b'')) for name in ('native', 'systemd', 'frontend')}
        self.stack.enter_context(patch.object(deploy.guardian, 'LOCKS', self.locks))
        fixed_locks = [{'path': path, 'device': Path(path).stat().st_dev, 'inode': Path(path).stat().st_ino, 'held': True}
                       for path in self.locks.values()]
        self.guardian_pid = 9876
        self.guardian_properties = {'MainPID': str(self.guardian_pid), 'InvocationID': 'd' * 32, 'ActiveState': 'active'}
        self.stack.enter_context(patch.object(repair, 'GUARDIAN_PID', self.guardian_pid))
        self.stack.enter_context(patch.object(repair, 'GUARDIAN_INVOCATION', self.guardian_properties['InvocationID']))
        self.stack.enter_context(patch.object(repair, 'GUARDIAN_LOCKS', fixed_locks))
        unit = self.write(self.base / 'guardian.unit', b'[Service]\nExecStart=/fixture/guardian\n')
        self.stack.enter_context(patch.object(repair, 'GUARDIAN_UNIT_SHA256', repair.sha(unit.read_bytes())))
        original_bound = repair.bound_bytes

        def local_bound(path, expected=None, private=False):
            if Path(path) == Path('/etc/systemd/system') / repair.GUARDIAN_UNIT:
                return original_bound(unit, expected, private)
            return original_bound(path, expected, private)

        self.stack.enter_context(patch.object(repair, 'bound_bytes', side_effect=local_bound))
        self.stack.enter_context(patch.object(deploy, 'deployment_lock', side_effect=self.lease))
        self.lock_mode = 'valid'
        self.lease_active = False

    def write(self, path, body, mode=0o600):
        path.write_bytes(body)
        path.chmod(mode)
        return path

    def seal_state(self, state):
        raw = repair.encoded(state)
        self.write(self.work / 'state.json', raw)
        self.write(self.status_path, raw)
        repair.CAPTURE_STATUS_SHA256 = repair.sha(raw)

    def snapshot(self):
        return {str(path.relative_to(self.base)): ('link', os.readlink(path)) if path.is_symlink()
                else ('file', path.read_bytes(), path.stat().st_mode & 0o777) if path.is_file()
                else ('directory', path.stat().st_mode & 0o777) for path in self.base.rglob('*')}

    @contextlib.contextmanager
    def lease(self, maintenance, expected, all_locks=False):
        self.assertTrue(all_locks, 'repair must adopt all three original owner OFDs')
        self.assertEqual(self.args.maintenance_handoff_sha256, expected)
        descriptors, locks = [], []
        try:
            for path in self.locks.values():
                descriptor = os.open(path, os.O_RDONLY | os.O_CLOEXEC)
                fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
                descriptors.append(descriptor)
                info = os.fstat(descriptor)
                locks.append({'path': path, 'device': info.st_dev, 'inode': info.st_ino, 'held': True})
            if self.lock_mode == 'missing':
                locks.pop()
            if self.lock_mode == 'unheld':
                os.close(descriptors.pop())
            self.lease_active = True
            yield {'guardian_pid': self.guardian_pid, 'locks': locks}
        finally:
            self.lease_active = False
            for descriptor in descriptors:
                os.close(descriptor)

    def fake_run(self, *arguments, log=None):
        self.assertTrue(self.lease_active)
        if arguments[:3] == ('systemctl', 'show', repair.GUARDIAN_UNIT):
            self.assertEqual(('-p', '--value'), (arguments[3], arguments[5]))
            return self.guardian_properties[arguments[4]]
        self.assertIn(arguments, (('nginx', '-t'), ('systemctl', 'reload', 'nginx')))
        if log:
            self.write(log, b'local fixture command accepted\n')
        return ''

    def mutation_calls(self):
        return [call for call in self.run.call_args_list if call.args in (('nginx', '-t'), ('systemctl', 'reload', 'nginx'))]

    def refused_without_writes(self):
        before = self.snapshot()
        with self.assertRaises(RuntimeError):
            repair.repair(self.args, owner=deploy)
        self.assertEqual(before, self.snapshot())
        self.assertFalse((self.work / repair.REPAIR_DIR).exists())
        self.assertFalse(self.mutation_calls())
        self.capture_writer.assert_not_called()

    def test_default_dry_run_preserves_all_files_and_original_capture(self):
        before = self.snapshot()
        proof = repair.repair(self.args, owner=deploy)
        self.assertFalse(proof['executed'])
        self.assertEqual('CAPTURED', proof['phase_after'])
        self.assertEqual(before, self.snapshot())
        self.assertFalse(self.mutation_calls())
        self.capture_writer.assert_not_called()

    def test_execute_preserves_raw_lineage_and_changes_only_two_state_fields(self):
        self.args.execute = True
        before_state = (self.work / 'state.json').read_bytes()
        before_capture = self.capture_path.read_bytes()
        proof = repair.repair(self.args, owner=deploy)
        directory = self.work / repair.REPAIR_DIR
        canonical = repair.normalize(ACTUAL_ORIGINAL)
        self.assertTrue(proof['executed'])
        self.assertTrue(proof['writer_unchanged'])
        self.assertEqual(ACTUAL_ORIGINAL, (directory / 'original-nginx-locations').read_bytes())
        self.assertEqual(before_state, (directory / 'original-state.json').read_bytes())
        self.assertEqual(canonical, (directory / 'canonical-nginx-locations').read_bytes())
        self.assertEqual(canonical, deploy.NGINX_LOCATIONS.read_bytes())
        self.assertEqual(0o644, deploy.NGINX_LOCATIONS.stat().st_mode & 0o777)
        self.assertEqual(canonical, (self.work / 'previous-nginx-locations').read_bytes())
        state = deploy.read_state(self.work)
        changed = {key for key in set(state) | set(self.state) if state.get(key) != self.state.get(key)}
        self.assertEqual({'ingress_original_sha256', 'ingress_repair'}, changed)
        self.assertEqual('CAPTURED', state['phase'])
        self.assertEqual(before_capture, self.capture_path.read_bytes())
        self.assertEqual(proof['state_after_sha256'], repair.sha((self.work / 'state.json').read_bytes()))
        self.assertEqual(proof['lineage_sha256'], repair.sha((directory / 'lineage.json').read_bytes()))
        self.assertEqual(2, len(self.mutation_calls()))
        self.capture_writer.assert_not_called()

    def test_missing_or_unheld_guardian_ofd_refuses_before_write(self):
        self.args.execute = True
        for mode in ('missing', 'unheld'):
            with self.subTest(mode=mode):
                self.lock_mode = mode
                self.refused_without_writes()

    def test_original_guardian_pid_and_invocation_change_refuse_before_write(self):
        self.args.execute = True
        self.guardian_properties['InvocationID'] = 'e' * 32
        self.refused_without_writes()
        self.guardian_properties['InvocationID'] = repair.GUARDIAN_INVOCATION
        self.guardian_pid += 1
        self.refused_without_writes()

    def test_capture_state_and_live_generation_mismatches_refuse_before_write(self):
        self.args.execute = True
        for key, value in (('phase', 'STAGED'), ('maintenance_admission_closed', True), ('captured_pid', 1),
                           ('captured_pid', True), ('captured_invocation_id', 'A' * 32),
                           ('provider_sha256', 'f' * 64), ('prepare_config_sha256', 'f' * 64)):
            with self.subTest(state=key, value=value):
                self.seal_state(dict(self.state, **{key: value}))
                self.refused_without_writes()
        self.seal_state(self.state)
        for key, value in (('MainPID', '9999'), ('InvocationID', 'c' * 32), ('ActiveState', 'inactive'), ('SubState', 'dead')):
            previous = self.properties[key]
            try:
                with self.subTest(property=key):
                    self.properties[key] = value
                    self.refused_without_writes()
            finally:
                self.properties[key] = previous

    def test_bound_files_and_runtime_hash_drift_refuse_before_write(self):
        self.args.execute = True
        paths = (self.status_path, self.args.maintenance_handoff, self.capture_path, deploy.BINARY,
                 self.work / 'previous-binary', self.work / 'lmm-api-go', self.work / 'previous.env',
                 deploy.ENVIRONMENT, self.process_path, deploy.NGINX_LOCATIONS,
                 self.work / 'previous-nginx-locations', self.work / 'frontend' / 'index.html')
        for path in paths:
            original = path.read_bytes()
            try:
                with self.subTest(path=path.name):
                    path.write_bytes(original + b'changed')
                    self.refused_without_writes()
            finally:
                path.write_bytes(original)
        self.proc_exe_sha256 = 'f' * 64
        self.refused_without_writes()
        self.proc_exe_sha256 = self.state['previous_sha256']
        self.proc_environment += b'CHANGED=1\0'
        self.refused_without_writes()

    def test_capture_receipt_unknown_fields_cannot_be_resealed_as_original(self):
        capture = json.loads(self.capture_path.read_bytes())
        capture['unreviewed'] = True
        self.write(self.capture_path, repair.encoded(capture))
        self.seal_state(dict(self.state, capture_receipt_sha256=deploy.digest(self.capture_path)))
        self.args.execute = True
        self.refused_without_writes()

    def test_final_prewrite_cas_detects_drift_during_generation_recheck(self):
        self.args.execute = True
        for path in (deploy.NGINX_LOCATIONS, self.work / 'previous-nginx-locations', self.work / 'state.json'):
            original = path.read_bytes()
            calls = 0

            def drift(_version):
                nonlocal calls
                calls += 1
                if calls == 2:
                    path.write_bytes(original + b'outside-owner-drift')

            try:
                with self.subTest(path=path.name), patch.object(deploy, 'healthy', side_effect=drift):
                    with self.assertRaises(RuntimeError):
                        repair.repair(self.args, owner=deploy)
                    self.assertEqual(original + b'outside-owner-drift', path.read_bytes())
                    self.assertFalse((self.work / repair.REPAIR_DIR).exists())
                    self.assertFalse(self.mutation_calls())
            finally:
                path.write_bytes(original)

    def test_existing_partial_lineage_and_pending_state_refuse_blind_replay(self):
        directory = self.work / repair.REPAIR_DIR
        directory.mkdir(mode=0o700)
        self.write(directory / 'intent.json', b'{"incomplete":true}\n')
        self.args.execute = True
        before = self.snapshot()
        with self.assertRaisesRegex(RuntimeError, 'lineage already exists'):
            repair.repair(self.args, owner=deploy)
        self.assertEqual(before, self.snapshot())
        shutil.rmtree(directory)
        self.write(self.work / 'state.next', b'unowned pending state')
        self.refused_without_writes()

    def test_nginx_failure_retains_intent_raw_bytes_and_blocks_replay(self):
        self.args.execute = True
        before_state = (self.work / 'state.json').read_bytes()
        before_capture = self.capture_path.read_bytes()
        def fail_validation(*arguments, **kwargs):
            if arguments == ('nginx', '-t'):
                raise RuntimeError('injected nginx validation failure')
            return self.fake_run(*arguments, **kwargs)

        with patch.object(deploy, 'run', side_effect=fail_validation):
            with self.assertRaisesRegex(RuntimeError, 'injected nginx'):
                repair.repair(self.args, owner=deploy)
        directory = self.work / repair.REPAIR_DIR
        failure = json.loads((directory / 'failure.json').read_bytes())
        self.assertTrue(failure['recovery_required'])
        self.assertTrue(failure['replay_forbidden'])
        self.assertEqual('NGINX_CONFIG', failure['failed_stage'])
        self.assertTrue((directory / 'intent.json').is_file())
        self.assertFalse((directory / 'complete.json').exists())
        self.assertEqual(ACTUAL_ORIGINAL, (directory / 'original-nginx-locations').read_bytes())
        self.assertEqual(before_state, (directory / 'original-state.json').read_bytes())
        self.assertEqual(before_state, (self.work / 'state.json').read_bytes())
        self.assertEqual(before_capture, self.capture_path.read_bytes())
        before_retry = self.snapshot()
        with self.assertRaises(RuntimeError):
            repair.repair(self.args, owner=deploy)
        self.assertEqual(before_retry, self.snapshot())
        self.assertFalse(self.mutation_calls())


class LocalNginxTests(unittest.TestCase):
    """No production address is ever used by these nginx or TLS processes."""

    @classmethod
    def setUpClass(cls):
        cls.nginx = shutil.which('nginx')
        cls.openssl = shutil.which('openssl')
        if not cls.nginx or not cls.openssl:
            raise unittest.SkipTest('localhost nginx/TLS fixture requires installed nginx and openssl')

    def setUp(self):
        self.stack = contextlib.ExitStack()
        self.addCleanup(self.stack.close)
        cache = Path.home() / '.cache'
        cache.mkdir(mode=0o700, parents=True, exist_ok=True)
        self.root = Path(self.stack.enter_context(tempfile.TemporaryDirectory(prefix='systemd-ingress-local-', dir=cache)))
        self.records = []
        self.sni = []
        cert, key = self.root / 'fixture-cert.pem', self.root / 'fixture-key.pem'
        generated = subprocess.run([self.openssl, 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
                                    '-keyout', str(key), '-out', str(cert), '-days', '1',
                                    '-subj', '/CN=api.lmm.best', '-addext', 'subjectAltName=DNS:api.lmm.best'],
                                   stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        self.assertEqual(0, generated.returncode, generated.stdout.decode(errors='replace'))
        cert.chmod(0o600)
        key.chmod(0o600)
        self.cert = cert
        records = self.records

        class Handler(BaseHTTPRequestHandler):
            protocol_version = 'HTTP/1.1'

            def respond(self):
                body = self.rfile.read(int(self.headers.get('Content-Length', '0')))
                record = {'method': self.command, 'path': self.path, 'body': body.decode(),
                          'host': self.headers.get('Host'), 'real_ip': self.headers.get('X-Real-IP'),
                          'forwarded_for': self.headers.get('X-Forwarded-For'),
                          'forwarded_proto': self.headers.get('X-Forwarded-Proto')}
                records.append(record)
                code = 404 if self.path.startswith('/missing') else 418 if self.path.startswith('/teapot') else 200
                payload = json.dumps(record, sort_keys=True).encode()
                self.send_response(code)
                self.send_header('Content-Type', 'application/json')
                self.send_header('X-Fixture-Upstream', 'localhost-tls')
                self.send_header('Content-Length', str(len(payload)))
                self.send_header('Connection', 'close')
                self.end_headers()
                if self.command != 'HEAD':
                    self.wfile.write(payload)

            do_GET = respond
            do_POST = respond
            do_HEAD = respond

            def log_message(self, *_):
                pass

        self.upstream = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.upstream.daemon_threads = True
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cert, key)
        context.set_servername_callback(lambda connection, name, tls_context: self.sni.append(name))
        self.upstream.socket = context.wrap_socket(self.upstream.socket, server_side=True)
        self.thread = threading.Thread(target=self.upstream.serve_forever, daemon=True)
        self.thread.start()
        self.addCleanup(self.close_upstream)

    def close_upstream(self):
        self.upstream.shutdown()
        self.upstream.server_close()
        self.thread.join(timeout=5)

    def local_fragment(self, body):
        body = body.replace(b'proxy_pass https://45.59.187.63;',
                            ('proxy_pass https://127.0.0.1:' + str(self.upstream.server_port) + ';').encode())
        body = body.replace(b'/etc/ssl/certs/ca-certificates.crt', str(self.cert).encode())
        body = body.replace(b'/var/log/nginx/access.log', str(self.root / 'access.log').encode())
        self.assertNotIn(b'45.59.187.63', body)
        self.assertNotIn(b'/etc/ssl/certs/ca-certificates.crt', body)
        return body

    @contextlib.contextmanager
    def serve(self, body, label):
        directory = self.root / label
        directory.mkdir(mode=0o700)
        with socket.socket() as reservation:
            reservation.bind(('127.0.0.1', 0))
            port = reservation.getsockname()[1]
        fragment = directory / 'locations.conf'
        fragment.write_bytes(self.local_fragment(body))
        config = directory / 'nginx.conf'
        config.write_text('daemon off;\nmaster_process off;\npid ' + str(directory / 'nginx.pid') + ';\n'
                          'error_log ' + str(directory / 'error.log') + ' notice;\n'
                          'events { worker_connections 64; }\nhttp {\n'
                          'map $http_upgrade $websocket_upgrade { default $http_upgrade; "" ""; }\n'
                          'map $http_upgrade $connection_upgrade { default upgrade; "" close; }\n'
                          'map $request_uri $lmm_access_loggable { default 0; }\n'
                          'client_body_temp_path ' + str(directory / 'client-body') + ';\n'
                          'proxy_temp_path ' + str(directory / 'proxy-temp') + ';\n'
                          'fastcgi_temp_path ' + str(directory / 'fastcgi-temp') + ';\n'
                          'uwsgi_temp_path ' + str(directory / 'uwsgi-temp') + ';\n'
                          'scgi_temp_path ' + str(directory / 'scgi-temp') + ';\n'
                          'server { listen 127.0.0.1:' + str(port) + '; server_name fixture.invalid;\n'
                          'include ' + str(fragment) + ';\n}\n}\n')
        command = [self.nginx, '-p', str(directory) + '/', '-c', str(config), '-e', str(directory / 'error.log')]
        checked = subprocess.run(command + ['-t'], stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        self.assertEqual(0, checked.returncode, checked.stdout.decode(errors='replace'))
        with (directory / 'process.log').open('wb') as log:
            process = subprocess.Popen(command, stdout=log, stderr=subprocess.STDOUT)
            try:
                deadline = time.monotonic() + 5
                while True:
                    if process.poll() is not None:
                        self.fail('localhost nginx exited: ' + (directory / 'process.log').read_text())
                    try:
                        with socket.create_connection(('127.0.0.1', port), timeout=0.1):
                            break
                    except OSError:
                        if time.monotonic() >= deadline:
                            self.fail('localhost nginx did not listen')
                        time.sleep(0.02)
                yield port
            finally:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)

    def request(self, port, method, path, body=None):
        connection = http.client.HTTPConnection('127.0.0.1', port, timeout=5)
        try:
            connection.request(method, path, body=body)
            response = connection.getresponse()
            return response.status, response.getheader('X-Fixture-Upstream'), response.read()
        finally:
            connection.close()

    def test_real_nginx_tls_routing_barrier_and_restore_preserve_requests(self):
        canonical = repair.normalize(ACTUAL_ORIGINAL)
        probes = [('GET', '/api/status?a=1&a=2&escaped=%2F+', None),
                  ('POST', '/v1/chat/completions?q=fixture', b'{"fixture":"body-preserved"}'),
                  ('HEAD', '/head?x=1', None), ('GET', '/missing?x=404', None), ('GET', '/teapot?x=418', None)]
        expected = []
        for label, body in (('original', ACTUAL_ORIGINAL), ('canonical', canonical)):
            with self.serve(body, label) as port:
                current = []
                for method, path, payload in probes:
                    before = len(self.records)
                    current.append(self.request(port, method, path, payload))
                    self.assertEqual(before + 1, len(self.records), 'one named backend hop, no error-page loop')
                    record = self.records[-1]
                    self.assertEqual(method, record['method'])
                    self.assertEqual(path, record['path'])
                    self.assertEqual((payload or b'').decode(), record['body'])
                    self.assertEqual('api.lmm.best', record['host'])
                    self.assertEqual('127.0.0.1', record['real_ip'])
                    self.assertEqual('127.0.0.1', record['forwarded_for'])
                    self.assertEqual('http', record['forwarded_proto'])
                if label == 'original':
                    expected = current
                else:
                    self.assertEqual(expected, current)
        self.assertEqual([200, 200, 200, 404, 418], [entry[0] for entry in expected])
        self.assertEqual(b'', expected[2][2])
        self.assertTrue(self.sni)
        self.assertEqual({'api.lmm.best'}, set(self.sni))
        maintenance = {'transition_id': 'fixture'}
        barrier = deploy.maintenance_barrier(maintenance, canonical)
        before = len(self.records)
        with self.serve(barrier, 'barrier') as port:
            for method, path, payload in probes:
                status, upstream, body = self.request(port, method, path, payload)
                self.assertEqual(503, status)
                self.assertIsNone(upstream)
                self.assertEqual(b'' if method == 'HEAD' else b'lmm-credit-transition:fixture', body)
        self.assertEqual(before, len(self.records))
        with self.serve(canonical, 'restored') as port:
            restored = [self.request(port, *probe) for probe in probes]
        self.assertEqual(expected, restored)


if __name__ == '__main__':
    unittest.main(verbosity=2)
