"""Bounded loopback provider process. No application database is opened here."""
from __future__ import annotations

import base64
import dataclasses
import hashlib
import heapq
import hmac
import http.client
import http.server
import secrets
import socket
import threading
import time
import uuid
from urllib.parse import parse_qsl, urlsplit

from . import models
from .clock import Canceled, Clock, peer_closed
from .config import Config, Invalid, MARKER, MODELS, integer, loads, local_url, object_fields, wire
from .mcp import MCP
from .payments import Payments
from .protocol import Reply, json_reply, ProviderError


def form_values(raw: str) -> dict[str, str]:
    if len(raw) > 65536:
        raise Invalid("form too large")
    try:
        pairs = parse_qsl(raw, keep_blank_values=True, strict_parsing=True, max_num_fields=64,
                          encoding="utf-8", errors="strict")
    except (ValueError, UnicodeError) as exc:
        raise Invalid("invalid form") from exc
    result = {}
    for key, value in pairs:
        if key in result:
            raise Invalid("duplicate form field")
        result[key] = value
    return result


class Lab:
    def __init__(self, config: Config):
        self.config = config
        self.run_id = uuid.uuid4().hex
        self.token = secrets.token_urlsafe(32)
        self.lock = threading.RLock()
        self.started = time.monotonic()
        self.closed = threading.Event()
        self.generation = 0
        self.active = 0
        self.requests = 0
        self.control_requests = 0
        self.input_bytes = 0
        self.sequence = 0
        self.trace: list[dict] = []
        self.queue: list[tuple[int, int, dict]] = []
        self.queue_seq = 0
        self.callback_active = False
        self._new_state()
        self.httpd = Server(("127.0.0.1", config.port), Handler, self)
        self.base = "http://127.0.0.1:" + str(self.httpd.server_address[1])
        self.thread = threading.Thread(target=self.httpd.serve_forever, kwargs={"poll_interval": 0.05}, daemon=True)
        self.worker = threading.Thread(target=self._callbacks, daemon=True)

    def _new_state(self):
        self.clock = Clock(self.config.epoch)
        self.counts = {i.id: {"attempts": 0, "completed": 0, "canceled": 0, "errors": 0} for i in self.config.instances}
        self.instances = {i.id: i for i in self.config.instances}
        self.providers = {}
        for cfg in self.config.instances:
            uid = lambda prefix, name=cfg.id: self.uid(name, prefix)
            if cfg.protocol in {"stripe-v1", "epay-md5-cny"}:
                self.providers[cfg.id] = Payments(cfg, uid)
            elif cfg.protocol == "mcp-2025-11-25":
                self.providers[cfg.id] = MCP(uid)

    def uid(self, name: str, prefix: str) -> str:
        self.sequence += 1
        # The run namespace is unique; payload contents and event ordering are repeatable.
        raw = wire([self.config.seed, self.run_id, self.generation, name, self.sequence])
        return prefix + "_" + hashlib.sha256(raw).hexdigest()[:24]

    def note(self, kind: str, **fields):
        if len(self.trace) < self.config.limits.records:
            self.trace.append({"sequence": len(self.trace) + 1, "virtual_ms": self.clock.ms,
                               "kind": kind, **fields})

    def records(self) -> int:
        return sum(p.count() if isinstance(p, Payments) else len(p.sessions) for p in self.providers.values())

    def manifest(self) -> dict:
        return {"marker": MARKER, "run_id": self.run_id, "base_url": self.base, "control_token": self.token,
                "seed": self.config.seed, "epoch": self.config.epoch, "generation": self.generation,
                "provider_token": "mock-run-" + self.run_id,
                "config_sha256": hashlib.sha256(wire(dataclasses.asdict(self.config))).hexdigest(),
                "instances": {i.id: {"protocol": i.protocol, "model": i.model,
                                      "url": f"{self.base}/r/{self.run_id}/{i.id}", "fixture": dataclasses.asdict(i)} for i in self.config.instances}}

    def start(self) -> "Lab":
        self.thread.start()
        self.worker.start()
        return self

    def close(self):
        if self.closed.is_set():
            return
        self.closed.set()
        self.clock.close()
        if self.thread.is_alive():
            self.httpd.shutdown()
        self.httpd.close_connections()
        self.httpd.server_close()
        if self.worker.is_alive():
            self.worker.join(4)
        if self.thread.is_alive():
            self.thread.join(2)

    def __enter__(self):
        return self.start()

    def __exit__(self, *_):
        self.close()

    def snapshot(self) -> dict:
        with self.lock:
            providers = {}
            for name, p in self.providers.items():
                if isinstance(p, Payments):
                    providers[name] = {"orders": len(p.sessions) + len(p.epay_orders), "refunds": len(p.refunds),
                                       "events": list(p.events), "provider_records": p.count()}
                else:
                    providers[name] = {"sessions": len(p.sessions), "tool_calls": p.calls}
            return {"run_id": self.run_id, "generation": self.generation, "virtual_ms": self.clock.ms,
                    "active": self.active, "requests": self.requests, "input_bytes": self.input_bytes,
                    "instances": {k: dict(v) for k, v in self.counts.items()}, "providers": providers,
                    "pending_callbacks": len(self.queue), "callback_active": self.callback_active,
                    "trace": list(self.trace), "trace_full": len(self.trace) >= self.config.limits.records}

    def control(self, v: dict) -> Reply:
        op = v.get("op")
        with self.lock:
            if op == "stats":
                object_fields(v, {"op"}, {"op"})
                return json_reply(self.snapshot())
            if op == "advance":
                object_fields(v, {"op", "ms"}, {"op", "ms"})
                return json_reply({"virtual_ms": self.clock.advance(v["ms"])})
            if op == "reset":
                object_fields(v, {"op"}, {"op"})
                if self.active or self.callback_active or self.queue:
                    raise ProviderError(409, "active_run_cannot_reset")
                self.clock.close()
                self.generation += 1
                self.sequence = 0
                # Lifetime request/input counters are intentionally not reset.
                self.trace.clear()
                self._new_state()
                return json_reply({"generation": self.generation})
            if op in {"transition", "delivery", "schedule"}:
                common = {"op", "instance"}
                fields = {"transition": {"reference", "state"}, "delivery": {"event"},
                          "schedule": {"event", "target", "delay_ms", "copies", "drop", "retries", "retry_delay_ms"}}[op]
                required = {"transition": {"reference", "state"}, "delivery": {"event"},
                            "schedule": {"event", "target"}}[op]
                object_fields(v, common | fields, common | required)
                name = v["instance"]
                if not isinstance(name, str) or not isinstance(self.providers.get(name), Payments):
                    raise Invalid("select a payment instance")
                p = self.providers[name]
                if op == "transition":
                    if self.records() + 4 > self.config.limits.records:
                        raise ProviderError(429, "provider_record_limit")
                    if not isinstance(v["reference"], str) or not isinstance(v["state"], str):
                        raise Invalid("invalid provider transition")
                    event = p.transition(v["reference"], v["state"], self.clock.now())
                    self.note("provider_transition", instance=name, event=event)
                    return json_reply({"event": event})
                if not isinstance(v["event"], str):
                    raise Invalid("event must be a string")
                delivery = p.delivery(v["event"], self.clock.now())
                if op == "delivery":
                    return json_reply(delivery)
                target = v["target"]
                if not isinstance(target, str) or target not in self.config.callbacks:
                    raise Invalid("callback target is not explicitly declared")
                delay = integer(v.get("delay_ms", 0), 0, 86400000)
                copies = integer(v.get("copies", 1), 1, 8)
                retries = integer(v.get("retries", 0), 0, 3)
                retry_delay = integer(v.get("retry_delay_ms", 1000), 1, 86400000)
                drop = v.get("drop", False)
                if type(drop) is not bool:
                    raise Invalid("drop must be boolean")
                if len(self.queue) + copies > 32:
                    raise ProviderError(429, "callback_queue_limit")
                for _ in range(copies):
                    self.queue_seq += 1
                    job = {"instance": name, "event": v["event"], "target": target, "drop": drop,
                           "retries": retries, "retry_delay_ms": retry_delay, "attempt": 0}
                    heapq.heappush(self.queue, (self.clock.ms + delay, self.queue_seq, job))
                return json_reply({"queued": copies})
            raise Invalid("unknown control operation")

    def _callbacks(self):
        while not self.closed.wait(0.01):
            with self.lock:
                if not self.queue or self.queue[0][0] > self.clock.ms:
                    continue
                _, _, job = heapq.heappop(self.queue)
                self.callback_active = True
                delivery = self.providers[job["instance"]].delivery(job["event"], self.clock.now())
            status, result = None, "dropped"
            if not job["drop"]:
                conn = None
                try:
                    host, port, path = local_url(self.config.callbacks[job["target"]])
                    conn = http.client.HTTPConnection(host, port, timeout=2)
                    conn.request("POST", path, base64.b64decode(delivery["body_base64"], validate=True), delivery["headers"])
                    response = conn.getresponse()
                    status = response.status
                    response.read(self.config.limits.response_bytes + 1)
                    result = "delivered" if 200 <= status < 300 else "rejected"
                    # Never follow a redirect, including a redirect to another loopback port.
                except (OSError, http.client.HTTPException, ValueError):
                    result = "failed"
                finally:
                    if conn is not None:
                        conn.close()
            with self.lock:
                self.note("callback", instance=job["instance"], event=job["event"], target=job["target"],
                          attempt=job["attempt"] + 1, result=result, http_status=status)
                if result in {"failed", "rejected"} and job["attempt"] < job["retries"] and not self.closed.is_set():
                    job["attempt"] += 1
                    self.queue_seq += 1
                    heapq.heappush(self.queue, (self.clock.ms + job["retry_delay_ms"], self.queue_seq, job))
                self.callback_active = False

    def provider(self, name: str, method: str, path: str, query: dict, body: bytes, headers: dict) -> Reply:
        cfg = self.instances[name]
        with self.lock:
            counters = self.counts[name]
            counters["attempts"] += 1
            self.note("request", instance=name, attempt=counters["attempts"], method=method, path=path)
            if counters["attempts"] > cfg.max_calls:
                raise ProviderError(429, "instance_quota_exhausted")
            kind = cfg.scenario.kind
            if counters["attempts"] <= cfg.scenario.first_failures or self.clock.ms < cfg.scenario.recover_at_ms:
                kind = "upstream_error"
            if kind in {"upstream_error", "rate_limit", "context_exceeded"}:
                return models.fault(cfg.protocol, kind)
            if (method != "GET" or cfg.protocol == "epay-md5-cny" and path == "/submit.php") and self.records() + 5 > self.config.limits.records:
                raise ProviderError(429, "provider_record_limit")
            if cfg.protocol in MODELS:
                if method != "POST" or not headers.get("content-type", "").startswith("application/json"):
                    raise Invalid("model fixture requires a JSON POST")
                value = loads(body)
                if not isinstance(value, dict):
                    raise Invalid("model request must be an object")
                return models.response(cfg, path, value, self.uid(name, "model"), self.clock.now())
            if cfg.protocol == "mcp-2025-11-25":
                value = loads(body) if body else {}
                reply = self.providers[name].handle(method, path, value, headers, kind)
                reply.first_byte_ms += cfg.scenario.first_byte_ms
                return reply
            form = {}
            if body:
                if headers.get("content-type", "").split(";")[0] != "application/x-www-form-urlencoded":
                    raise Invalid("payment fixture requires a form body")
                form = form_values(body.decode("utf-8"))
            p = self.providers[name]
            if cfg.protocol == "stripe-v1":
                reply = p.stripe(method, path, query, form, headers, self.clock.now())
                # The provider write is committed before withholding the response.
                if kind == "timeout_after_commit" and method == "POST":
                    reply.first_byte_ms = cfg.scenario.first_byte_ms
                elif kind == "normal":
                    reply.first_byte_ms = cfg.scenario.first_byte_ms
                return reply
            reply = p.epay(method, path, query, form, headers, self.clock.now())
            reply.first_byte_ms = cfg.scenario.first_byte_ms
            return reply


class Server(http.server.ThreadingHTTPServer):
    daemon_threads = True
    block_on_close = False
    allow_reuse_address = False

    def __init__(self, address, handler, lab):
        self.lab = lab
        self.slots = threading.BoundedSemaphore(lab.config.limits.concurrency + 2)
        self.connections = set()
        self.connection_lock = threading.Condition()
        super().__init__(address, handler)

    def process_request(self, request, client_address):
        if not self.slots.acquire(blocking=False):
            try:
                request.settimeout(0.1)
                request.sendall(b"HTTP/1.1 503 Service Unavailable\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
            except OSError:
                pass
            self.shutdown_request(request)
            return
        try:
            super().process_request(request, client_address)
        except BaseException:
            self.slots.release()
            raise

    @staticmethod
    def stop_socket(conn):
        try:
            conn.shutdown(socket.SHUT_RDWR)
        except OSError:
            pass
        conn.close()

    def close_connections(self):
        with self.connection_lock:
            connections=list(self.connections)
        for conn in connections:
            self.stop_socket(conn)
        deadline=time.monotonic()+2
        with self.connection_lock:
            while self.connections and time.monotonic()<deadline:
                self.connection_lock.wait(0.01)

    def process_request_thread(self, *args):
        conn=args[0]
        # An absolute watchdog also bounds slow request headers/body reads.
        # Its count is bounded by the accepted-connection semaphore.
        timer=threading.Timer(self.lab.config.limits.wall_seconds,self.stop_socket,args=(conn,))
        timer.daemon=True
        with self.connection_lock:
            self.connections.add(conn)
        timer.start()
        try:
            super().process_request_thread(*args)
        finally:
            timer.cancel()
            with self.connection_lock:
                self.connections.discard(conn)
                self.connection_lock.notify_all()
            self.slots.release()

    def handle_error(self, *_):
        # No provider arguments, URL credentials or test tokens in tracebacks.
        with self.lab.lock:
            self.lab.note("handler_error")


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "LMM-MOCK-ONLY/1"
    sys_version = ""

    def setup(self):
        super().setup()
        self.connection.settimeout(self.server.lab.config.limits.wall_seconds)

    def log_message(self, *_):
        pass

    def do_GET(self):
        self._handle()

    def do_POST(self):
        self._handle()

    def do_DELETE(self):
        self._handle()

    def _handle(self):
        lab = self.server.lab
        self.close_connection = True
        name = None
        entered = False
        status = 500
        canceled = False
        injected_disconnect = False
        started = time.monotonic()
        deadline = started + lab.config.limits.wall_seconds
        try:
            if time.monotonic() - lab.started > lab.config.limits.run_seconds or lab.closed.is_set():
                raise ProviderError(503, "mock_run_expired")
            if len(self.path) > 4096 or "%" in self.path.split("?", 1)[0] or "\\" in self.path:
                raise Invalid("ambiguous or oversized fixture path")
            url = urlsplit(self.path)
            if url.scheme or url.netloc or url.fragment:
                raise Invalid("absolute URLs are not accepted")
            headers = {}
            for key in self.headers:
                if len(self.headers.get_all(key)) != 1:
                    raise Invalid("duplicate request header")
                headers[key.lower()] = self.headers[key]
            if headers.get("host") != self.server.lab.base.removeprefix("http://"):
                raise ProviderError(403, "mock_host_required")
            if "transfer-encoding" in headers:
                raise Invalid("chunked requests are not part of these small fixtures")
            if self.command == "POST" and "content-length" not in headers:
                raise ProviderError(411, "content_length_required")
            try:
                size = int(headers.get("content-length", "0"))
            except ValueError as exc:
                raise Invalid("invalid content length") from exc
            if not 0 <= size <= lab.config.limits.body_bytes:
                raise ProviderError(413, "input_body_limit")
            control = url.path == "/control/" + lab.run_id
            with lab.lock:
                if control:
                    lab.control_requests += 1
                    if lab.control_requests > 4096:
                        raise ProviderError(429, "control_request_limit")
                else:
                    lab.requests += 1
                    if lab.requests > lab.config.limits.requests:
                        raise ProviderError(429, "run_request_limit")
                if lab.input_bytes + size > lab.config.limits.total_input_bytes:
                    raise ProviderError(413, "aggregate_input_limit")
                lab.input_bytes += size
            if control:
                if self.command != "POST" or not hmac.compare_digest(headers.get("x-mock-control", ""), lab.token):
                    raise ProviderError(403, "control_token_required")
            else:
                bits = url.path.split("/", 4)
                if len(bits) != 5 or bits[1] != "r" or bits[2] != lab.run_id or bits[3] not in lab.instances:
                    raise ProviderError(404, "mock_run_or_instance_not_found")
                name = bits[3]
                token="mock-run-" + lab.run_id
                opted_in = (headers.get("x-mock-run") == lab.run_id
                            or headers.get("authorization") == "Bearer " + token
                            or headers.get("x-api-key") == token
                            or headers.get("x-goog-api-key") == token)
                if not opted_in:
                    raise ProviderError(403, "explicit_mock_run_header_or_key_required")
                with lab.lock:
                    if lab.active >= lab.config.limits.concurrency:
                        raise ProviderError(429, "concurrency_limit")
                    lab.active += 1
                    entered = True
            body = self.rfile.read(size)
            if len(body) != size:
                raise Invalid("incomplete request body")
            if control:
                value = loads(body)
                if not isinstance(value, dict):
                    raise Invalid("control request must be an object")
                reply = lab.control(value)
            else:
                with lab.lock:
                    reply = lab.provider(name, self.command, "/" + bits[4], form_values(url.query), body, headers)
                    reply.scheduled_ms = lab.clock.ms
            status = reply.status
            injected_disconnect = reply.interrupt_after is not None
            self._reply(reply, lab.clock, deadline)
        except (Invalid, UnicodeError, TypeError, KeyError, ValueError):
            status = 400
            self._safe_error(status, "invalid_mock_request")
        except ProviderError as exc:
            status = exc.status
            self._safe_error(status, exc.code)
        except (Canceled, BrokenPipeError, ConnectionResetError, TimeoutError, OSError):
            canceled = True
        finally:
            if entered:
                with lab.lock:
                    lab.active -= 1
                    counts = lab.counts[name]
                    counts["canceled" if canceled else "errors" if status >= 400 or injected_disconnect else "completed"] += 1
                    lab.note("response", instance=name, status=status, canceled=canceled, injected_disconnect=injected_disconnect,
                             duration_ms=round((time.monotonic() - started) * 1000, 3))

    def _safe_error(self, status, code):
        try:
            self._reply(json_reply({"error": {"code": code}}, status), self.server.lab.clock,
                        time.monotonic() + 1)
        except (OSError, Canceled):
            pass

    def _reply(self, reply: Reply, clock: Clock, deadline: float):
        limit = self.server.lab.config.limits.response_bytes
        size = sum(map(len, reply.chunks)) if reply.chunks is not None else len(reply.body)
        if size > limit:
            raise ProviderError(413, "fixture_response_limit")
        if reply.first_byte_ms:
            clock.wait_until(reply.scheduled_ms + reply.first_byte_ms, deadline, lambda: peer_closed(self.connection))
        self.send_response(reply.status)
        for key, value in reply.headers.items():
            self.send_header(key, value)
        self.send_header("X-Mock-Only", MARKER)
        self.send_header("Connection", "close")
        if reply.chunks is None:
            self.send_header("Content-Length", str(len(reply.body)))
            self.end_headers()
            self.wfile.write(reply.body)
            self.wfile.flush()
            return
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        self.wfile.flush()
        for index, chunk in enumerate(reply.chunks):
            if reply.interrupt_after is not None and index >= reply.interrupt_after:
                self.connection.shutdown(socket.SHUT_RDWR)
                return
            if index and reply.chunk_delay_ms:
                clock.wait_until(reply.scheduled_ms + reply.first_byte_ms + index * reply.chunk_delay_ms, deadline,
                                 lambda: peer_closed(self.connection))
            if chunk:
                self.wfile.write(f"{len(chunk):x}\r\n".encode() + chunk + b"\r\n")
                self.wfile.flush()
        if reply.interrupt_after is not None:
            self.connection.shutdown(socket.SHUT_RDWR)
            return
        self.wfile.write(b"0\r\n\r\n")
        self.wfile.flush()
