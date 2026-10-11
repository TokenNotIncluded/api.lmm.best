"""Closed configuration and common limits for the local provider lab."""
from __future__ import annotations

import dataclasses
import ipaddress
import json
import re
from pathlib import Path
from urllib.parse import urlsplit

MARKER = "LMM-MOCK-ONLY-v1"
BASELINE = "bbde7468dcbd08091bc6a666c27946c2a385f2bf"
PROTOCOLS = frozenset({"stripe-v1", "epay-md5-cny", "openai-chat", "openai-responses",
                       "anthropic-messages", "gemini-generate-content", "mcp-2025-11-25"})
MODELS = frozenset({"openai-chat", "openai-responses", "anthropic-messages", "gemini-generate-content"})
IDENT = re.compile(r"[a-z][a-z0-9_-]{0,47}\Z")


class Invalid(ValueError):
    """A test configuration or request violates the declared contract."""


def unique(pairs: list[tuple[str, object]]) -> dict:
    result = {}
    for key, value in pairs:
        if key in result:
            raise Invalid("duplicate JSON field")
        result[key] = value
    return result


def loads(raw: bytes | str) -> object:
    try:
        return json.loads(raw, object_pairs_hook=unique,
                          parse_constant=lambda _: (_ for _ in ()).throw(Invalid("non-finite JSON number")))
    except (UnicodeError, json.JSONDecodeError, RecursionError) as exc:
        raise Invalid("invalid JSON") from exc


def wire(value: object) -> bytes:
    return json.dumps(value, ensure_ascii=False, allow_nan=False, sort_keys=True,
                      separators=(",", ":")).encode()


def object_fields(value: object, fields: set[str], required: set[str] = frozenset()) -> dict:
    if not isinstance(value, dict) or set(value) - fields or required - set(value):
        raise Invalid("missing or unknown fields")
    return value


def integer(value: object, low: int, high: int) -> int:
    if type(value) is not int or not low <= value <= high:
        raise Invalid(f"expected integer in [{low}, {high}]")
    return value


def local_url(raw: str, *, path: bool = True) -> tuple[str, int, str]:
    """No DNS, redirects, proxy, URL credentials or non-loopback destinations."""
    if not isinstance(raw, str) or len(raw) > 2048 or any(c in raw for c in "\r\n\\"):
        raise Invalid("invalid local URL")
    try:
        u = urlsplit(raw)
        addr = ipaddress.ip_address(u.hostname or "")
        port = u.port
    except ValueError as exc:
        raise Invalid("use a literal loopback IP and explicit port") from exc
    if (u.scheme != "http" or addr.version != 4 or not addr.is_loopback or port is None
            or u.username is not None or u.password is not None or u.fragment or u.query
            or (not path and u.path not in ("", "/"))):
        raise Invalid("only declared IPv4 loopback HTTP endpoints are supported")
    integer(port, 1, 65535)
    return str(addr), port, u.path or "/"


@dataclasses.dataclass(frozen=True)
class Limits:
    requests: int = 512
    concurrency: int = 8
    body_bytes: int = 65536
    total_input_bytes: int = 4 * 1024 * 1024
    response_bytes: int = 262144
    wall_seconds: int = 20
    run_seconds: int = 600
    records: int = 512

    @classmethod
    def parse(cls, raw: object) -> "Limits":
        value = object_fields(raw, {f.name for f in dataclasses.fields(cls)})
        maxima = {"requests": 4096, "concurrency": 16, "body_bytes": 1048576,
                  "total_input_bytes": 16 * 1024 * 1024, "response_bytes": 1048576,
                  "wall_seconds": 60, "run_seconds": 3600, "records": 4096}
        for key, v in value.items():
            integer(v, 1, maxima[key])
        result = cls(**value)
        if result.total_input_bytes < result.body_bytes:
            raise Invalid("total input limit is smaller than one request")
        return result


@dataclasses.dataclass(frozen=True)
class Scenario:
    kind: str = "normal"
    first_failures: int = 0
    recover_at_ms: int = 0
    first_byte_ms: int = 0
    chunk_delay_ms: int = 0
    interrupt_after: int = 2

    @classmethod
    def parse(cls, raw: object, protocol: str) -> "Scenario":
        value = object_fields(raw, {f.name for f in dataclasses.fields(cls)})
        result = cls(**value)
        kinds = {"normal", "rate_limit", "upstream_error"}
        if protocol in MODELS:
            kinds |= {"context_exceeded", "empty", "missing_usage", "disconnect"}
        if protocol == "stripe-v1":
            kinds |= {"timeout_after_commit"}
        if protocol == "mcp-2025-11-25":
            kinds |= {"tool_error", "disconnect"}
        if not isinstance(result.kind, str) or result.kind not in kinds:
            raise Invalid("scenario not implemented for this protocol")
        for name in ("first_failures", "interrupt_after"):
            integer(getattr(result, name), 0, 128)
        for name in ("recover_at_ms", "first_byte_ms", "chunk_delay_ms"):
            integer(getattr(result, name), 0, 86_400_000)
        if "chunk_delay_ms" in value and protocol not in MODELS:
            raise Invalid("chunk delays are supported only by model stream fixtures")
        if "interrupt_after" in value and (protocol not in MODELS or result.kind != "disconnect"):
            raise Invalid("interrupt_after requires a model disconnect scenario")
        if result.kind == "timeout_after_commit" and result.first_byte_ms == 0:
            raise Invalid("timeout_after_commit requires a virtual first_byte_ms delay")
        return result


@dataclasses.dataclass(frozen=True)
class Instance:
    id: str
    protocol: str
    scenario: Scenario = Scenario()
    model: str = "mock-small"
    text: str = "Hello from mock."
    chunk_chars: int = 4
    input_tokens: int = 12
    output_tokens: int = 6
    cached_tokens: int = 0
    output_kind: str = "text"
    currency: str = "USD"
    max_calls: int = 512

    @classmethod
    def parse(cls, raw: object) -> "Instance":
        fields = {f.name for f in dataclasses.fields(cls)}
        value = dict(object_fields(raw, fields, {"id", "protocol"}))
        name, protocol = value["id"], value["protocol"]
        if not isinstance(name, str) or not IDENT.fullmatch(name) or not isinstance(protocol, str) or protocol not in PROTOCOLS:
            raise Invalid("invalid instance or unsupported protocol")
        value["scenario"] = Scenario.parse(value.get("scenario", {}), protocol)
        result = cls(**value)
        for name in ("input_tokens", "output_tokens", "cached_tokens"):
            integer(getattr(result, name), 0, 1_000_000)
        integer(result.chunk_chars, 1, 512)
        integer(result.max_calls, 1, 4096)
        if result.cached_tokens > result.input_tokens:
            raise Invalid("cached tokens exceed total input tokens")
        if not isinstance(result.text, str) or len(result.text.encode()) > 4096:
            raise Invalid("text exceeds fixture limit")
        if not isinstance(result.model, str) or not re.fullmatch(r"[A-Za-z0-9._-]{1,96}", result.model):
            raise Invalid("invalid test model name")
        if not isinstance(result.output_kind, str) or result.output_kind not in {"text", "json", "tool_call"}:
            raise Invalid("unsupported output kind")
        if result.output_kind == "json":
            loads(result.text)
        if not isinstance(result.currency, str) or result.currency not in {"USD", "CNY", "JPY", "KWD"}:
            raise Invalid("currency fixture is not declared")
        if protocol == "epay-md5-cny" and result.currency != "CNY":
            raise Invalid("legacy ePay profile supports only CNY")
        model_fields = {"model", "text", "chunk_chars", "input_tokens", "output_tokens", "cached_tokens", "output_kind"}
        if protocol not in MODELS and set(value) & model_fields:
            raise Invalid("model options on a non-model instance")
        if protocol not in {"stripe-v1", "epay-md5-cny"} and "currency" in value:
            raise Invalid("payment currency on a model instance")
        return result


@dataclasses.dataclass(frozen=True)
class Config:
    seed: int
    epoch: int
    port: int
    limits: Limits
    instances: tuple[Instance, ...]
    callbacks: dict[str, str]

    @classmethod
    def parse(cls, raw: object) -> "Config":
        v = object_fields(raw, {"marker", "environment", "seed", "epoch", "listen", "limits", "instances", "callbacks"},
                          {"marker", "environment", "instances"})
        if v["marker"] != MARKER or v["environment"] != "test":
            raise Invalid("mocklab requires explicit test configuration")
        listen = object_fields(v.get("listen", {}), {"host", "port"})
        if listen.get("host", "127.0.0.1") != "127.0.0.1":
            raise Invalid("mocklab listens only on 127.0.0.1")
        port = integer(listen.get("port", 0), 0, 65535)
        items = v["instances"]
        if not isinstance(items, list) or not 1 <= len(items) <= 16:
            raise Invalid("declare between 1 and 16 upstream instances")
        instances = tuple(Instance.parse(i) for i in items)
        if len({i.id for i in instances}) != len(instances):
            raise Invalid("duplicate upstream instance")
        callbacks = v.get("callbacks", {})
        if not isinstance(callbacks, dict) or len(callbacks) > 8:
            raise Invalid("invalid callback allowlist")
        for key, endpoint in callbacks.items():
            if not IDENT.fullmatch(key):
                raise Invalid("invalid callback label")
            local_url(endpoint)
        return cls(integer(v.get("seed", 7), 0, 2**63 - 1),
                   integer(v.get("epoch", 1791633600), 1, 2**40), port,
                   Limits.parse(v.get("limits", {})), instances, dict(callbacks))

    @classmethod
    def read(cls, path: Path) -> "Config":
        if path.stat().st_size > 65536:
            raise Invalid("configuration exceeds 64 KiB")
        return cls.parse(loads(path.read_bytes()))
