"""Thin, fail-closed bindings for DP-22/23/24/27/28. No Docker/SSH implementation."""
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import selectors
import signal
import subprocess
import time

from contract import API, Rejected, Unavailable, canonical, require


class Hooks:
    def __init__(self, bindings: dict):
        self.bindings = bindings

    def inspect(self, node: str) -> dict:
        return self.call("dp24", {"api": API, "action": "inspect", "node": node})

    def invoke(self, request: dict) -> dict:
        action = request["action"]
        if action == "fence":
            owner = "dp24"
        elif action == "space":
            owner = "dp28"
        elif action == "pull":
            owner = "dp27"
        else:
            owner = {"rust-core": "dp22", "go-extensions": "dp23", "web": "frontend"}[request["target"]["component"]]
        return self.call(owner, request)

    def call(self, owner: str, request: dict) -> dict:
        require(owner in self.bindings, "missing " + owner + " adapter; real integration is not qualified")
        binding = self.bindings[owner]
        path = Path(binding["executable"])
        require(path.is_absolute() and path.is_file(), "adapter must be an absolute executable path")
        hasher = hashlib.sha256()
        with path.open("rb") as source:
            for data in iter(lambda: source.read(65536), b""):
                hasher.update(data)
        require("sha256:" + hasher.hexdigest() == binding["digest"], "adapter executable digest changed")
        payload = canonical(request).encode() + b"\n"
        require(len(payload) <= 65536, "adapter request is too large")
        # No shell interpolation, no inherited service keys/DB credentials, no persistent worker.
        proc = subprocess.Popen([str(path)], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                stderr=subprocess.DEVNULL, start_new_session=True,
                                env={"PATH": "/usr/bin:/bin", "LANG": "C.UTF-8"})
        output = bytearray()
        deadline = time.monotonic() + 5
        sent = 0
        try:
            with selectors.DefaultSelector() as selector:
                for pipe in (proc.stdin, proc.stdout):
                    os.set_blocking(pipe.fileno(), False)
                selector.register(proc.stdin, selectors.EVENT_WRITE)
                selector.register(proc.stdout, selectors.EVENT_READ)
                while selector.get_map():
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        raise Unavailable("adapter timeout; reconcile operation ID")
                    for key, _ in selector.select(remaining):
                        if key.fileobj is proc.stdin:
                            try:
                                sent += os.write(proc.stdin.fileno(), payload[sent:sent + 4096])
                            except BrokenPipeError:
                                sent = len(payload)
                            if sent == len(payload):
                                selector.unregister(proc.stdin)
                                proc.stdin.close()
                        else:
                            data = os.read(proc.stdout.fileno(), 4096)
                            output.extend(data)
                            if len(output) > 65536:
                                raise Unavailable("adapter response exceeds 64 KiB; outcome is unknown")
                            if not data:
                                selector.unregister(proc.stdout)
                proc.wait(timeout=max(.001, deadline - time.monotonic()))
            if proc.returncode:
                raise Unavailable("adapter failed; outcome may be unknown")
            try:
                result = json.loads(output)
            except (ValueError, UnicodeError) as exc:
                raise Unavailable("invalid adapter result; outcome is unknown") from exc
            require(isinstance(result, dict), "adapter response must be an object")
            return result
        except subprocess.TimeoutExpired as exc:
            raise Unavailable("adapter timeout") from exc
        finally:
            if proc.poll() is None:
                os.killpg(proc.pid, signal.SIGKILL)
                proc.wait()
            for pipe in (proc.stdin, proc.stdout):
                if pipe and not pipe.closed:
                    pipe.close()
