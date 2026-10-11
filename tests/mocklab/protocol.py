"""Shared HTTP result contract for independent external-provider fixtures."""
from __future__ import annotations
from dataclasses import dataclass, field
from .config import wire

@dataclass
class Reply:
    status: int = 200
    body: bytes = b""
    headers: dict[str, str] = field(default_factory=dict)
    chunks: list[bytes] | None = None
    interrupt_after: int | None = None
    first_byte_ms: int = 0
    chunk_delay_ms: int = 0
    scheduled_ms: int = 0


def json_reply(value: object, status: int = 200) -> Reply:
    return Reply(status, wire(value), {"Content-Type": "application/json"})


def sse(value: object, event: str = "") -> bytes:
    prefix = f"event: {event}\n" if event else ""
    return (prefix + "data: " + wire(value).decode() + "\n\n").encode()



class ProviderError(Exception):
    def __init__(self, status: int, code: str):
        self.status, self.code = status, code
        super().__init__(code)
