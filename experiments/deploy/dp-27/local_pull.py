#!/usr/bin/env python3
"""Measure loopback whole-blob transfer of the signed standalone probe OCI layout.

Not Docker or an application release. The cache is temporary and local.
"""
import http.server
import json
import re
import tempfile
import threading
import time
import urllib.request
from pathlib import Path
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey
import distribution as d
import oci_cost

HERE = Path(__file__).resolve().parent


def measure(layout: Path, public_key: Path, signature: Path) -> dict:
    Ed25519PublicKey.from_public_bytes(public_key.read_bytes()).verify(signature.read_bytes(), (layout / 'index.json').read_bytes())
    images = {name: oci_cost.inspect(layout, name) for name in ('probe-v1', 'probe-v2')}
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            if not re.fullmatch(r'/blobs/sha256/[0-9a-f]{64}', self.path):
                self.send_error(404)
                return
            path = layout / self.path.lstrip('/')
            if not path.is_file() or path.is_symlink():
                self.send_error(404)
                return
            self.send_response(200)
            self.send_header('Content-Length', str(path.stat().st_size))
            self.end_headers()
            with path.open('rb') as source:
                while block := source.read(65536):
                    self.wfile.write(block)
        def log_message(self, *_):
            pass
    server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    result = {}
    try:
        with tempfile.TemporaryDirectory(prefix='dp27-loopback-') as directory:
            cache = Path(directory)
            for scenario, name in [('first_pull', 'probe-v1'), ('single_probe_update', 'probe-v2'), ('after_pre_pull', 'probe-v2')]:
                image = images[name]
                descriptors = dict(image['metadata'])
                descriptors.update({layer['digest']: layer['compressed_bytes'] for layer in image['layers']})
                transferred = requests = 0
                start = time.perf_counter()
                for digest, size in descriptors.items():
                    sha = digest.split(':')[1]
                    dest = cache / sha
                    if dest.exists():
                        if d.hash_file(dest) != sha:
                            raise ValueError('corrupt cached blob')
                        continue
                    url = f'http://127.0.0.1:{server.server_port}/blobs/sha256/{sha}'
                    with opener.open(url, timeout=5) as stream:
                        d.copy_bounded(stream, dest, size, sha)
                    transferred += size
                    requests += 1
                result[scenario] = {'received_payload_bytes': transferred, 'http_requests': requests,
                                    'wall_seconds': time.perf_counter() - start,
                                    'cache_allocated_bytes': d.disk_bytes(cache),
                                    'http_headers_tcp_tls_overhead_counted': False,
                                    'docker_used': False, 'scope': 'standalone probe OCI blobs only'}
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=3)
    return result


if __name__ == '__main__':
    out = HERE / 'out'
    result = measure(out / 'oci', out / 'signed' / 'TEST-ONLY-public-key.raw', out / 'signed' / 'oci-index.sig')
    (HERE / 'evidence' / 'loopback-pull.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result, indent=2))
