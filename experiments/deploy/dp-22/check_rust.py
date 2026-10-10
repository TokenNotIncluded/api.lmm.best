#!/usr/bin/env python3
"""Offline Rust checks. No remote CI, database credentials, deployment, or POST replay.

These checks cover compilation, admission ownership and the protected native
binary. They still do NOT certify a model/SSE + real settlement rollout.
"""
from __future__ import annotations

import http.client
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import threading
import time

HERE = Path(__file__).resolve().parent
CORE = HERE.parents[2] / 'apps' / 'lmm-core'
OUT = HERE / 'evidence' / 'v3' / 'rust-checks.json'


def clean_env() -> dict[str, str]:
    # Deliberate allowlist: do not inherit database/payment/notification settings.
    env = {k: os.environ[k] for k in ['PATH', 'HOME', 'CARGO_HOME', 'RUSTUP_HOME',
                                     'LD_LIBRARY_PATH', 'SSL_CERT_FILE'] if k in os.environ}
    env.update({'CARGO_NET_OFFLINE': 'true', 'RUSTUP_AUTO_INSTALL': '0', 'CARGO_BUILD_JOBS': '2',
                'CARGO_INCREMENTAL': '0', 'CARGO_PROFILE_DEV_DEBUG': '0'})
    return env


def get(port: int, path: str, method: str = 'GET') -> tuple[int, bytes]:
    connection = http.client.HTTPConnection('127.0.0.1', port, timeout=1)
    try:
        connection.request(method, path, body=b'{}' if method == 'POST' else None)
        response = connection.getresponse()
        return response.status, response.read()
    finally:
        connection.close()


def native_checks(binary: Path) -> dict:
    if not binary.is_file():
        raise RuntimeError('local native binary missing')
    children = []
    with tempfile.TemporaryDirectory(prefix='dp22-rust-', dir='/tmp') as tmp:
        root = Path(tmp)

        def start(name: str, shared_socket: Path | None = None):
            directory = root / name
            directory.mkdir(mode=0o700)
            token_file = directory / 'rpc.token'
            token_file.write_text('dp22-test-only-' + 'x' * 48 + '\n')
            token_file.chmod(0o600)
            with socket.socket() as port_probe:
                port_probe.bind(('127.0.0.1', 0))
                port = port_probe.getsockname()[1]
            rpc = shared_socket or directory / 'core.sock'
            env = clean_env()
            env.update({'LMM_CORE_LISTEN': f'127.0.0.1:{port}',
                        'LMM_CORE_RPC_SOCKET': str(rpc),
                        'LMM_CORE_RPC_TOKEN_FILE': str(token_file)})
            log = open(directory / 'stderr.log', 'wb')
            process = subprocess.Popen([str(binary)], env=env,
                stdin=subprocess.DEVNULL, stdout=log, stderr=log)
            log.close()
            children.append(process)
            return process, port, rpc

        def await_live(process, port):
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    raise AssertionError('native core exited before live probe')
                try:
                    if get(port, '/health/live')[0] == 200:
                        return
                except OSError:
                    pass
                threading.Event().wait(0.01)
            raise AssertionError('native live condition deadline')

        try:
            a, port_a, socket_a = start('a'); await_live(a, port_a)
            b, port_b, socket_b = start('b'); await_live(b, port_b)
            assert port_a != port_b and socket_a != socket_b
            for port in [port_a, port_b]:
                assert get(port, '/health/ready')[0] == 503
                assert get(port, '/v1/responses', 'POST')[0] == 503
            inode = socket_a.stat().st_ino
            collision, _, _ = start('collision', shared_socket=socket_a)
            assert collision.wait(timeout=5) != 0
            assert socket_a.stat().st_ino == inode
            assert get(port_a, '/health/live')[0] == 200
            a.terminate(); assert a.wait(timeout=5) == 0
            assert not socket_a.exists()
            assert socket_b.exists() and get(port_b, '/health/live')[0] == 200
            b.terminate(); assert b.wait(timeout=5) == 0
            assert not socket_b.exists()
            return {'result': 'PASS', 'separate_ports_and_rpc_sockets': True,
                    'live_socket_collision_refused': True, 'model_protection_retained': True,
                    'idle_native_shutdown_only': True, 'native_stream_rollout': 'NOT_TESTED'}
        finally:
            for process in children:
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=3)
                    except subprocess.TimeoutExpired:
                        # Isolated test-process cleanup, never a rollout policy.
                        process.kill(); process.wait(timeout=2)


def main() -> int:
    result: dict = {'scope': 'offline_native_rust_checks', 'billing': 'NOT_TESTED',
                    'model_stream_rollout': 'NOT_TESTED', 'commands': []}
    blockers = []
    if shutil.which('cargo') is None:
        blockers.append('cargo/rustc unavailable in this container')
    if not (CORE / 'src' / 'config.rs').exists():
        blockers.append('delivery is a verified source overlay, not a full cloned repository')
    if blockers:
        result.update({'result': 'NOT_RUN', 'blockers': blockers})
        OUT.parent.mkdir(parents=True, exist_ok=True)
        OUT.write_text(json.dumps(result, indent=2) + '\n')
        print(json.dumps(result, indent=2))
        return 78
    commands = [
        ['cargo', 'fmt', '--all', '--check'],
        ['cargo', 'test', '--locked', '--lib', 'lifecycle::'],
        ['cargo', 'test', '--locked', '--lib', 'http::tests::process_health_does_not_advertise_billing_readiness'],
        ['cargo', 'test', '--locked', '--lib', 'http::tests::model_calls_fail_before_any_upstream_or_extension_work'],
        ['cargo', 'clippy', '--locked', '--all-targets', '--', '-D', 'warnings'],
        ['cargo', 'build', '--locked', '--bin', 'lmm-core'],
    ]
    all_ok = True
    for index, command in enumerate(commands):
        try:
            run = subprocess.run(command, cwd=CORE, env=clean_env(),
                                 capture_output=True, text=True, timeout=300)
            log = OUT.parent / f'rust-command-{index}.log'
            log.write_text(run.stdout + run.stderr)
            result['commands'].append({'command': command, 'returncode': run.returncode,
                                       'log': log.name})
            all_ok = all_ok and run.returncode == 0
        except subprocess.TimeoutExpired:
            result['commands'].append({'command': command, 'result': 'TIMEOUT'})
            all_ok = False
    if result['commands'][-1].get('returncode') == 0:
        try:
            result['native_binary'] = native_checks(CORE / 'target' / 'debug' / 'lmm-core')
        except Exception as error:
            result['native_binary'] = {'result': 'FAIL', 'error': repr(error)}
            all_ok = False
    result['result'] = 'PASS_PARTIAL_NATIVE_SCOPE' if all_ok else 'FAIL'
    OUT.write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result, indent=2))
    return 0 if all_ok else 1


if __name__ == '__main__':
    raise SystemExit(main())
