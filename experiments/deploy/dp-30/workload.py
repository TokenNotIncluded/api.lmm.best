#!/usr/bin/env python3
"""Bounded local HTTP/SSE probe. Never retries and never follows redirects.

The public CLI requires an explicit isolated-test permit and matching local
nonce endpoint. This is an accident guard, not proof of network isolation.
Production credentials and external targets are not accepted by this tool.
"""
from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor, Future
from dataclasses import dataclass
import hashlib
import http.client
import ipaddress
import json
from pathlib import Path
import re
import secrets
import socket
import threading
import time
from typing import Any
from urllib.parse import urlsplit

from dp30 import EvidenceError, finite, integer, loads, read_json


@dataclass(frozen=True)
class Limits:
    requests: int = 16
    concurrency: int = 4
    requests_per_second: float = 40
    deadline_seconds: float = 3
    max_seconds: float = 15
    max_input_bytes: int = 4 * 1024 * 1024
    max_response_bytes: int = 256 * 1024
    max_event_bytes: int = 16 * 1024

    def validate(self) -> None:
        for name in ("requests", "concurrency", "max_input_bytes", "max_response_bytes", "max_event_bytes"):
            integer(getattr(self, name), name, 1)
        for name in ("requests_per_second", "deadline_seconds", "max_seconds"):
            if finite(getattr(self, name), name) == 0:
                raise EvidenceError(f"{name} must be positive")
        if not (self.requests <= 256 and self.concurrency <= 16 and self.requests_per_second <= 100
                and self.deadline_seconds <= 15 and self.max_seconds <= 60
                and self.max_input_bytes <= 4194304 and self.max_response_bytes <= 262144
                and self.max_event_bytes <= 65536):
            raise EvidenceError("probe safety cap exceeded")
        if self.requests / self.requests_per_second + self.deadline_seconds > self.max_seconds:
            raise EvidenceError("arrival schedule plus final deadline exceeds run duration")


class SSEParser:
    """Require a complete terminal frame; EOF never flushes an unfinished event."""

    def __init__(self, max_event_bytes: int = 16384):
        self.max_event_bytes = max_event_bytes
        self.buffer = bytearray()
        self.data: list[bytes] = []
        self.event_bytes = 0
        self.complete = False
        self.sequence: list[str] = []
        self.content = bytearray()

    def feed(self, chunk: bytes) -> None:
        self.buffer.extend(chunk)
        while b"\n" in self.buffer:
            line, _, rest = self.buffer.partition(b"\n")
            self.buffer = bytearray(rest)
            line = line.removesuffix(b"\r")
            self.event_bytes += len(line) + 1
            if self.event_bytes > self.max_event_bytes:
                raise EvidenceError("SSE event limit")
            if not line:
                if self.data:
                    self._dispatch(b"\n".join(self.data))
                self.data.clear()
                self.event_bytes = 0
            elif line.startswith(b"data:"):
                data = bytes(line[5:])
                self.data.append(data[1:] if data.startswith(b" ") else data)
        if self.event_bytes + len(self.buffer) > self.max_event_bytes:
            raise EvidenceError("SSE event limit")

    def _dispatch(self, data: bytes) -> None:
        if self.complete:
            raise EvidenceError("event after terminal")
        if data == b"[DONE]":
            self.complete = True
            return
        event = loads(data.decode("utf-8"))
        if not isinstance(event, dict) or not isinstance(event.get("choices"), list):
            raise EvidenceError("unexpected model event")
        for choice in event["choices"]:
            if not isinstance(choice, dict) or choice.get("index", 0) != 0:
                raise EvidenceError("probe supports a single choice")
            delta = choice.get("delta", {})
            if not isinstance(delta, dict):
                raise EvidenceError("invalid delta")
            text = delta.get("content")
            if text is not None:
                if not isinstance(text, str):
                    raise EvidenceError("non-text content")
                encoded = text.encode("utf-8")
                self.content.extend(encoded)
                self.sequence.append(hashlib.sha256(encoded).hexdigest())

    def finish(self) -> None:
        if self.buffer or self.data or not self.complete:
            raise EvidenceError("incomplete SSE terminal or unfinished frame")


def local_target(base_url: str) -> tuple[str, int]:
    parsed = urlsplit(base_url)
    if parsed.scheme != "http" or parsed.username or parsed.password or parsed.path not in ("", "/") or parsed.query or parsed.fragment:
        raise EvidenceError("target must be a plain isolated loopback HTTP origin")
    try:
        address = ipaddress.ip_address(parsed.hostname or "")
        port = parsed.port
    except ValueError as exc:
        raise EvidenceError("literal loopback address required; no DNS") from exc
    if not address.is_loopback or port is None or not 1024 <= port <= 65535:
        raise EvidenceError("non-loopback or privileged target rejected")
    return str(address), port


def verify_permit(base_url: str, permit: dict[str, Any]) -> None:
    host, port = local_target(base_url)
    fields = ("isolated_test", "egress_blocked", "no_real_money", "no_notifications")
    nonce = permit.get("nonce")
    if not all(permit.get(k) is True for k in fields) or permit.get("origin") != base_url:
        raise EvidenceError("explicit matching isolation permit required")
    if not isinstance(nonce, str) or not re.fullmatch(r"[0-9a-f]{32,128}", nonce):
        raise EvidenceError("invalid test permit nonce")
    conn = http.client.HTTPConnection(host, port, timeout=1)
    holder: list[socket.socket | None] = [None]

    def stop_permit_read() -> None:
        target_socket = holder[0] or conn.sock
        if target_socket is not None:
            try:
                target_socket.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass

    watchdog = threading.Timer(1, stop_permit_read)
    watchdog.daemon = True
    watchdog.start()
    started = time.monotonic()
    try:
        conn.request("GET", "/__dp30/permit", headers={"Connection": "close"})
        response = conn.getresponse()
        holder[0] = getattr(getattr(response.fp, "raw", None), "_sock", None)
        body = response.read(4097)
        if time.monotonic() - started > 1 or len(body) > 4096 or response.status != 200 or loads(body.decode("utf-8")) != {"nonce": nonce, "isolated_test": True}:
            raise EvidenceError("local target did not match permit")
    finally:
        watchdog.cancel()
        watchdog.join()
        conn.close()


def probe(base_url: str, run_id: str, request_id: str, path: str, body: bytes,
          expected_chunks: list[str], limits: Limits, scheduled_at: float,
          run_deadline: float, credential: str | None = None) -> dict[str, Any]:
    host, port = local_target(base_url)
    if path != "/v1/chat/completions":
        raise EvidenceError("only the declared model path is allowed")
    start = time.monotonic()
    deadline = min(start + limits.deadline_seconds, run_deadline)
    parser = SSEParser(limits.max_event_bytes)
    row: dict[str, Any] = {"run_id": run_id, "request_id": request_id, "attempt": 1,
                           "outcome": "error", "terminal_complete": False,
                           "expected_sequence": [hashlib.sha256(s.encode()).hexdigest() for s in expected_chunks],
                           "expected_content_sha256": hashlib.sha256("".join(expected_chunks).encode()).hexdigest(),
                           "first_byte_ms": None, "status_code": None, "response_bytes": 0,
                           "queue_delay_ms": max(0, (start - scheduled_at) * 1000),
                           "expected_upstream_calls": None}
    conn = http.client.HTTPConnection(host, port, timeout=max(.001, deadline - start))
    socket_holder: list[socket.socket | None] = [None]

    def interrupt_at_deadline() -> None:
        # A per-read socket timeout alone permits a slow header drip forever.
        target_socket = socket_holder[0] or conn.sock
        if target_socket is not None:
            try:
                target_socket.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass

    watchdog = threading.Timer(max(.001, deadline - start), interrupt_at_deadline)
    watchdog.daemon = True
    watchdog.start()
    try:
        headers = {"Content-Type": "application/json", "Connection": "close",
                   "X-DP30-Run-ID": run_id, "X-DP30-Request-ID": request_id}
        if credential is not None:
            headers["Authorization"] = "Bearer " + credential
        conn.request("POST", path, body, headers)
        response = conn.getresponse()
        row["status_code"] = response.status
        if response.status != 200:
            raise EvidenceError("model request rejected (no retry or redirect)")
        if response.getheader("Content-Type", "").split(";", 1)[0].strip() != "text/event-stream":
            raise EvidenceError("unexpected content type")
        # HTTPConnection.sock can be None after a Connection: close response.
        # Keep a reference to the actual response socket for total-deadline reads.
        response_socket = getattr(getattr(response.fp, "raw", None), "_sock", None)
        socket_holder[0] = response_socket
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError("total request deadline")
            if response_socket is not None:
                response_socket.settimeout(remaining)
            chunk = response.read1(min(4096, limits.max_response_bytes + 1 - row["response_bytes"]))
            if not chunk:
                break
            if row["first_byte_ms"] is None:
                row["first_byte_ms"] = (time.monotonic() - start) * 1000
            row["response_bytes"] += len(chunk)
            if row["response_bytes"] > limits.max_response_bytes:
                raise EvidenceError("response byte limit")
            parser.feed(chunk)
        parser.finish()
        row["terminal_complete"] = True
        row["outcome"] = "complete"
    except (OSError, TimeoutError, EvidenceError, http.client.HTTPException, UnicodeError) as exc:
        row["error_type"] = type(exc).__name__
        row["error"] = str(exc)[:160]
    finally:
        watchdog.cancel()
        watchdog.join()
        conn.close()
        row["latency_ms"] = (time.monotonic() - scheduled_at) * 1000
        row["service_latency_ms"] = (time.monotonic() - start) * 1000
        row["sequence"] = parser.sequence
        row["content_sha256"] = hashlib.sha256(parser.content).hexdigest()
        if row["outcome"] == "complete" and (row["sequence"] != row["expected_sequence"] or row["content_sha256"] != row["expected_content_sha256"]):
            row["outcome"] = "content_mismatch"
    return row


def run_load(base_url: str, permit: dict[str, Any], run_id: str,
             limits: Limits, expected_chunks: list[str], credential: str | None = None) -> dict[str, Any]:
    limits.validate()
    if credential is not None and (not isinstance(credential, str) or not re.fullmatch(r"[!-~]{1,4096}", credential)):
        raise EvidenceError("invalid test credential")
    verify_permit(base_url, permit)
    if not re.fullmatch(r"[A-Za-z0-9_-]{1,64}", run_id):
        raise EvidenceError("invalid run_id")
    if not expected_chunks or len(expected_chunks) > 128 or any(not isinstance(s, str) for s in expected_chunks):
        raise EvidenceError("bounded expected chunks required")
    if sum(len(s.encode()) for s in expected_chunks) > 65536:
        raise EvidenceError("expected output too large")
    body = json.dumps({"model": "dp30-controlled-upstream", "stream": True,
                       "messages": [{"role": "user", "content": "DP30 isolated deterministic stream probe"}]}).encode()
    if len(body) * limits.requests > limits.max_input_bytes:
        raise EvidenceError("total input cap")
    start = time.monotonic()
    deadline = start + limits.max_seconds
    semaphore = threading.BoundedSemaphore(limits.concurrency)
    active = 0
    peak_active = 0
    lock = threading.Lock()
    rows = []
    futures: list[Future[dict[str, Any]]] = []

    def worker(rid: str, planned: float) -> dict[str, Any]:
        nonlocal active, peak_active
        with lock:
            active += 1
            peak_active = max(peak_active, active)
        try:
            return probe(base_url, run_id, rid, "/v1/chat/completions", body, expected_chunks, limits, planned, deadline, credential)
        finally:
            with lock:
                active -= 1
            semaphore.release()

    with ThreadPoolExecutor(max_workers=limits.concurrency) as pool:
        for i in range(limits.requests):
            planned = start + i / limits.requests_per_second
            time.sleep(max(0, planned - time.monotonic()))
            rid = f"{run_id}-{i:04d}"
            if not semaphore.acquire(blocking=False):
                rows.append({"run_id": run_id, "request_id": rid, "attempt": 1, "outcome": "client_shed",
                             "latency_ms": max(0, (time.monotonic() - planned) * 1000),
                             "expected_upstream_calls": 0, "first_byte_ms": None, "terminal_complete": False})
                continue
            futures.append(pool.submit(worker, rid, planned))
        rows.extend(f.result() for f in futures)
    elapsed = time.monotonic() - start
    return {"scope": "local_probe_not_capacity", "run_id": run_id, "requests": sorted(rows, key=lambda r: r["request_id"]),
            "offered_requests": limits.requests, "peak_active": peak_active,
            "input_bytes_upper_bound": len(body) * limits.requests, "elapsed_seconds": elapsed,
            "client_retries": 0, "completed_requests": sum(r["outcome"] == "complete" for r in rows)}


def read_test_credential(path: Path) -> str:
    if path.is_symlink() or not path.is_file() or path.stat().st_size > 4097 or path.stat().st_mode & 0o077:
        raise EvidenceError("test credential must be a private regular file, at most 4097 bytes")
    token = path.read_text(encoding="ascii").strip()
    if not re.fullmatch(r"[!-~]{1,4096}", token):
        raise EvidenceError("invalid test credential")
    return token


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--origin", required=True)
    parser.add_argument("--permit", type=Path, required=True)
    parser.add_argument("--expected-chunks", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--limits-file", type=Path, help="bounded Limits JSON; preserve its hash with the run")
    parser.add_argument("--credential-file", type=Path, help="isolated test API key only; never written to output")
    parser.add_argument("--authorize-isolated-test", action="store_true", required=True)
    args = parser.parse_args()
    try:
        chunks = read_json(args.expected_chunks)["chunks"]
        if args.output.exists():
            raise EvidenceError("output already exists; evidence must not be overwritten")
        limits = Limits(**read_json(args.limits_file)) if args.limits_file else Limits()
        credential = read_test_credential(args.credential_file) if args.credential_file else None
        result = run_load(args.origin, read_json(args.permit), "dp30-" + secrets.token_hex(8), limits, chunks, credential)
        with args.output.open("x", encoding="utf-8") as output:
            json.dump(result, output, ensure_ascii=False, allow_nan=False, indent=2)
            output.write("\n")
        return 0 if result["completed_requests"] == result["offered_requests"] else 1
    except (OSError, EvidenceError, KeyError, TypeError, http.client.HTTPException, UnicodeError) as exc:
        print(json.dumps({"status": "failed", "error": str(exc)}))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
