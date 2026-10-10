"""Isolated rollout controller and per-request route authority reference.

No production addresses, Go service, deploy API, image pull, or remote CI.
Readiness evidence is deliberately labelled transport-only. A deployment
adapter MUST NOT use this reference to enable real paid model routes.
"""
from __future__ import annotations

from dataclasses import dataclass
import http.client
import json
import os
from pathlib import Path
import signal
import threading
import uuid
from typing import Callable
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class RolloutBlocked(RuntimeError):
    pass


@dataclass(frozen=True)
class Instance:
    name: str
    port: int
    pid: int
    run_dir: Path


def request(instance: Instance, token: str, path: str, method: str = 'GET') -> tuple[int, dict]:
    connection = http.client.HTTPConnection('127.0.0.1', instance.port, timeout=2)
    try:
        connection.request(method, path, body=b'' if method == 'POST' else None,
                           headers={'X-Dp22-Admin': token})
        response = connection.getresponse()
        return response.status, json.loads(response.read())
    finally:
        connection.close()


class Routes:
    def __init__(self, token: str, max_pending: int = 64) -> None:
        self.lock = threading.Lock()
        self.current: str | None = None
        self.instances: dict[str, Instance] = {}
        self.token = token
        self.fail_before_commit = False
        self.unavailable = False
        self.test_before_reply = None  # Deterministic isolated race injection.
        # Selection holds a ticket until the chosen core records admission.
        # An unacknowledged ticket must block old-core drain indefinitely,
        # rather than expire on a timer and risk a late 503 or duplicate POST.
        if not 1 <= max_pending <= 4096:
            raise ValueError('pending ticket limit must be between 1 and 4096')
        self.max_pending = max_pending
        self.pending: dict[str, str] = {}
        self.request_tickets: dict[str, str] = {}
        self.ticket_requests: dict[str, str] = {}
        # Once withdrawn, a generation cannot be selected again, even during
        # the interval between deciding to signal and delivery of SIGTERM.
        self.withdrawn: set[str] = set()
        self.retiring: dict[str, Callable[[], None]] = {}

    def arm_drain(self, name: str, action: Callable[[], None]) -> bool:
        """Signal the old core only after all earlier selections are admitted."""
        with self.lock:
            if name in self.withdrawn:
                raise RolloutBlocked('old core is already marked for drain')
            self.withdrawn.add(name)
            if name in self.pending.values():
                self.retiring[name] = action
                return False
        action()
        return True

    def _resolve(self, admission: str, expected_name: str | None = None) -> bool:
        action = None
        with self.lock:
            name = self.pending.get(admission)
            if name is None or (expected_name is not None and name != expected_name):
                return False
            del self.pending[admission]
            request_id = self.ticket_requests.pop(admission, None)
            if request_id is not None:
                self.request_tickets.pop(request_id, None)
            if name not in self.pending.values():
                action = self.retiring.pop(name, None)
        if action is not None:
            action()
        return True

    def ack(self, name: str, admission: str) -> bool:
        # A consumed or revoked ticket can never confirm a second POST.
        return self._resolve(admission, name)

    def release_request(self, request_id: str) -> bool:
        """Only ingress's authenticated request-completion hook may call this.

        This revokes an unclaimed selection, not an accepted model operation.
        A later core ACK must fail before its business handler can run.
        Unknown/missing completion notifications never expire tickets on a timer.
        """
        with self.lock:
            ticket = self.request_tickets.get(request_id)
        return self._resolve(ticket) if ticket is not None else False

    def commit(self, expected: str | None, target: str) -> None:
        with self.lock:
            if self.fail_before_commit:
                raise RolloutBlocked('injected cutover failure; current route unchanged')
            if self.current != expected:
                raise RolloutBlocked('route generation changed concurrently')
            if target in self.withdrawn:
                raise RolloutBlocked('withdrawn generation cannot be reselected')
            if target not in self.instances:
                raise RolloutBlocked('unregistered generation')
            self.current = target

    def handler(self) -> type[BaseHTTPRequestHandler]:
        routes = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_: object) -> None:
                pass

            def do_GET(self) -> None:
                admission = None
                with routes.lock:
                    name = routes.current
                    original_uri = self.headers.get('X-Original-URI')
                    # Probe override is limited to a read-only path and a
                    # random local test credential, never arbitrary requests.
                    if (original_uri == '/__probe'
                            and self.headers.get('X-Dp22-Probe') == routes.token):
                        name = self.headers.get('X-Dp22-Candidate')
                    instance = routes.instances.get(name) if name else None
                    unavailable = routes.unavailable
                    if instance is not None and not unavailable and original_uri != '/__probe':
                        request_id = self.headers.get('X-Dp22-Request-Id', '')
                        valid_id = len(request_id) == 32 and all(c in '0123456789abcdef' for c in request_id)
                        if (not valid_id or request_id in routes.request_tickets
                                or len(routes.pending) >= routes.max_pending
                                or name in routes.withdrawn):
                            unavailable = True
                        else:
                            admission = uuid.uuid4().hex
                            routes.pending[admission] = name
                            routes.request_tickets[request_id] = admission
                            routes.ticket_requests[admission] = request_id
                if routes.test_before_reply is not None:
                    routes.test_before_reply(name, self.headers)
                if instance is None or unavailable:
                    self.send_response(503)
                else:
                    self.send_response(204)
                    self.send_header('X-Dp22-Target', f'127.0.0.1:{instance.port}')
                    if admission is not None:
                        self.send_header('X-Dp22-Admission', admission)
                self.send_header('Content-Length', '0')
                self.send_header('Cache-Control', 'no-store')
                self.end_headers()

            def do_POST(self) -> None:
                # Both callers are loopback test services with a random secret.
                # Client headers must be overwritten by the trusted ingress.
                if self.headers.get('X-Dp22-Admin') != routes.token:
                    status = 403
                elif self.path == '/__release':
                    routes.release_request(self.headers.get('X-Dp22-Request-Id', ''))
                    status = 204  # Idempotent completion acknowledgement.
                elif self.path == '/__ack':
                    status = 204 if routes.ack(self.headers.get('X-Dp22-Instance', ''),
                                               self.headers.get('X-Dp22-Admission', '')) else 403
                else:
                    status = 404
                self.send_response(status)
                self.send_header('Content-Length', '0')
                self.send_header('Cache-Control', 'no-store')
                self.end_headers()

        return Handler


class Controller:
    def __init__(self, routes: Routes, token: str, max_versions: int = 2) -> None:
        if max_versions < 2:
            raise ValueError('same-node rollout needs at least two generation slots')
        self.routes, self.token, self.max_versions = routes, token, max_versions
        self.slots: set[str] = set()
        self.lock = threading.RLock()
        self.state = 'stable'
        self.events: list[dict] = []

    def reserve(self, name: str, *, candidate_mib: int,
                free_mib: int | None, headroom_mib: int) -> None:
        # MUST run before launching a process. Inputs are scenario assumptions,
        # not measurements of a 1-core/1-GB host or admission of actual memory.
        with self.lock:
            if name in self.slots or len(self.slots) >= self.max_versions:
                raise RolloutBlocked('generation limit: wait or use another node')
            if (free_mib is None or candidate_mib <= 0 or headroom_mib < 0
                    or free_mib < candidate_mib + headroom_mib):
                raise RolloutBlocked('memory budget unknown/insufficient: no old-flow eviction')
            self.slots.add(name)

    def register(self, instance: Instance) -> None:
        with self.lock, self.routes.lock:
            if instance.name not in self.slots:
                raise RolloutBlocked('reserve resources before launch')
            if instance.name in self.routes.instances:
                raise RolloutBlocked('generation already registered')
            self.routes.instances[instance.name] = instance

    def reap(self, instance: Instance, returncode: int | None) -> None:
        with self.lock, self.routes.lock:
            if returncode is None:
                raise RolloutBlocked('process still alive; keep its generation slot')
            if self.routes.current == instance.name:
                raise RolloutBlocked('cannot remove the active route')
            self.routes.instances.pop(instance.name, None)
            self.slots.remove(instance.name)

    def switch(self, candidate: Instance, proxy_probe) -> Instance | None:
        with self.lock:
            if self.state == 'cutover_unknown':
                raise RolloutBlocked('reconcile uncertain cutover before another rollout')
            status, proof = request(candidate, self.token, '/__status')
            expected_checks = {'upstream_handshake': True, 'committed_write_read': True,
                               'private_socket_bound': True}
            if (status != 200 or proof.get('instance') != candidate.name
                    or proof.get('phase') != 'ready' or proof.get('checks') != expected_checks
                    or proof.get('model_ready') is not False
                    or proof.get('evidence') != 'python-transport-reference'):
                raise RolloutBlocked('candidate has no valid TRANSPORT preparation proof')
            # Real proxy path to the candidate before changing ordinary traffic.
            if proxy_probe(candidate.name) != candidate.name:
                raise RolloutBlocked('candidate proxy preflight failed')
            with self.routes.lock:
                old_name = self.routes.current
                old = self.routes.instances.get(old_name) if old_name else None
            self.routes.commit(old_name, candidate.name)
            try:
                if proxy_probe(None) != candidate.name:
                    raise RolloutBlocked('cutover acknowledgement mismatch')
            except Exception:
                # Do not guess which version accepted requests. Both remain
                # alive; never replay their POSTs or silently switch back.
                self.state = 'cutover_unknown'
                self.events.append({'event': 'cutover_unknown', 'candidate': candidate.name})
                raise
            self.state = 'stable'
            self.events.append({'event': 'route_committed', 'candidate': candidate.name,
                                'old': old_name, 'evidence': 'transport_only'})
            if old is not None and old.name != candidate.name:
                def drain_old():
                    os.kill(old.pid, signal.SIGTERM)
                    self.events.append({'event': 'drain_signalled', 'instance': old.name})
                if not self.routes.arm_drain(old.name, drain_old):
                    self.events.append({'event': 'old_drain_waits_for_admission', 'instance': old.name})
            return old


def watch_completions(path: Path, routes: Routes, stop: threading.Event) -> None:
    """Read definitive request-finalization events from the isolated ingress.

    The file must be an append-only private Nginx access log, not client data.
    Missing/invalid records never expire or guess the outcome of a ticket.
    Rotation, durable restart reconciliation and multiple ingress owners are
    deliberately NOT provided by this research adapter.
    """
    while not path.exists():
        if stop.wait(0.01):
            return
    with path.open(encoding='utf-8') as log:
        pending = ''
        while not stop.is_set():
            chunk = log.readline(8193)
            if not chunk:
                stop.wait(0.01)  # Poll for an event, never retire on elapsed time.
                continue
            pending += chunk
            if len(pending) > 8192:
                # Corrupt/misconfigured source: stop rather than guessing.
                return
            if not pending.endswith('\n'):
                continue
            try:
                completion = json.loads(pending)
                request_id, status = completion['request_id'], completion['status']
                if (isinstance(request_id, str) and len(request_id) == 32
                        and all(c in '0123456789abcdef' for c in request_id)
                        and isinstance(status, int) and 100 <= status <= 599):
                    routes.release_request(request_id)
            except (ValueError, KeyError, TypeError):
                pass
            pending = ''
