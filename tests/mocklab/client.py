"""Test runner transport: literal loopback only, no proxies or redirects."""
from __future__ import annotations

import http.client
import os
import re
import stat
from pathlib import Path
from urllib.parse import urlencode

from .config import Invalid, MARKER, loads, local_url, wire


class Client:
    def __init__(self, manifest: dict):
        if not isinstance(manifest, dict) or manifest.get("marker") != MARKER:
            raise Invalid("not a mocklab manifest")
        self.host, self.port, _ = local_url(manifest["base_url"], path=False)
        run = manifest.get("run_id")
        token = manifest.get("control_token")
        if (not isinstance(run, str) or not re.fullmatch(r"[a-f0-9]{32}", run)
                or not isinstance(token, str) or not 32 <= len(token) <= 128):
            raise Invalid("invalid mocklab identity")
        self.manifest = manifest
        for name, item in manifest["instances"].items():
            expected = f'{manifest["base_url"]}/r/{run}/{name}'
            if item.get("url") != expected:
                raise Invalid("instance URL does not belong to this run")

    @classmethod
    def read(cls, path: Path) -> "Client":
        if path.is_symlink():
            raise Invalid("manifest must not be a symlink")
        st = path.stat()
        if not stat.S_ISREG(st.st_mode) or st.st_size > 65536 or st.st_mode & 0o077:
            raise Invalid("manifest must be a private regular file, at most 64 KiB")
        return cls(loads(path.read_bytes()))

    def request(self, instance: str, method: str, path: str, value=None, *, form=None, headers=None, timeout=3):
        if instance not in self.manifest["instances"] or not path.startswith("/") or path.startswith("//"):
            raise Invalid("request outside declared instance")
        h = {"X-Mock-Run": self.manifest["run_id"], **(headers or {})}
        body = None
        if value is not None:
            body, h["Content-Type"] = wire(value), "application/json"
        if form is not None:
            if value is not None:
                raise Invalid("use JSON or form, not both")
            body, h["Content-Type"] = urlencode(form).encode(), "application/x-www-form-urlencoded"
        conn = http.client.HTTPConnection(self.host, self.port, timeout=timeout)
        try:
            conn.request(method, f'/r/{self.manifest["run_id"]}/{instance}' + path, body, h)
            result = conn.getresponse()
            raw = result.read(1048577)
            if len(raw) > 1048576:
                raise Invalid("response exceeds client limit")
            return result.status, dict(result.getheaders()), raw
        finally:
            conn.close()

    def control(self, op: str, **data):
        conn = http.client.HTTPConnection(self.host, self.port, timeout=3)
        try:
            conn.request("POST", "/control/" + self.manifest["run_id"], wire({"op": op, **data}),
                         {"Content-Type": "application/json", "X-Mock-Control": self.manifest["control_token"]})
            r = conn.getresponse()
            raw = r.read(1048577)
            if len(raw) > 1048576:
                raise Invalid("control result exceeds client limit")
            if r.status != 200:
                raise Invalid(f"control rejected ({r.status}): {raw.decode(errors='replace')[:128]}")
            return loads(raw)
        finally:
            conn.close()


def private_write(path: Path, value: bytes):
    """Exclusive creation avoids overwriting another run's evidence or secrets."""
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as out:
        out.write(value)
