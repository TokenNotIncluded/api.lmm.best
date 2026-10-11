"""A virtual clock for provider time, with a real-time safety deadline."""
from __future__ import annotations

import select
import socket
import threading
import time
from collections.abc import Callable

from .config import Invalid, integer


class Canceled(Exception):
    pass


class Clock:
    def __init__(self, epoch: int):
        self.epoch = epoch
        self.ms = 0
        self.closed = False
        self.waiting = 0
        self.condition = threading.Condition()

    def now(self) -> int:
        with self.condition:
            return self.epoch + self.ms // 1000

    def advance(self, ms: int) -> int:
        integer(ms, 0, 86_400_000)
        with self.condition:
            if self.ms + ms > 7 * 86_400_000:
                raise Invalid("virtual run exceeds seven days")
            self.ms += ms
            self.condition.notify_all()
            return self.ms

    def wait(self, delay_ms: int, deadline: float, canceled: Callable[[], bool] = lambda: False) -> None:
        with self.condition:
            until = self.ms + delay_ms
        self.wait_until(until, deadline, canceled)

    def wait_until(self, until: int, deadline: float, canceled: Callable[[], bool] = lambda: False) -> None:
        with self.condition:
            self.waiting += 1
            try:
                while self.ms < until:
                    if self.closed or canceled() or time.monotonic() >= deadline:
                        raise Canceled("request canceled or safety deadline reached")
                    self.condition.wait(min(0.05, max(0, deadline - time.monotonic())))
                if self.closed or canceled() or time.monotonic() >= deadline:
                    raise Canceled("request canceled or safety deadline reached")
            finally:
                self.waiting -= 1

    def close(self) -> None:
        with self.condition:
            self.closed = True
            self.condition.notify_all()


def peer_closed(conn: socket.socket) -> bool:
    try:
        ready, _, _ = select.select([conn], [], [], 0)
        return bool(ready) and conn.recv(1, socket.MSG_PEEK | socket.MSG_DONTWAIT) == b""
    except (OSError, ValueError):
        return True
