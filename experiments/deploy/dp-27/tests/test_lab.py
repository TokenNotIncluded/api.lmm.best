from __future__ import annotations
import errno
import fcntl
import gzip
import io
import json
import os
import socket
import struct
import subprocess
import sys
import threading
import urllib.request
from pathlib import Path
import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import distribution as d
import impact

COMMIT = '72667564c0431754d4856dc2e0db55f360bd2745'

def elf(extra=b'one'):
    h = bytearray(64)
    h[:6] = b'\x7fELF\x02\x01'
    struct.pack_into('<H', h, 16, 2)
    struct.pack_into('<H', h, 18, 62)
    return bytes(h) + extra * 1024

@pytest.fixture
def candidate(tmp_path):
    result = elf()
    payload = gzip.compress(result, mtime=0)
    m = {'schema': 1, 'component': 'probe', 'version': 'v0.0.0-dp27.1',
         'repository': d.REPOSITORY, 'commit': COMMIT, 'target': d.target_platform(),
         'builder': 'local-test-only', 'encoding': 'gzip',
         'payload_sha256': d.digest(payload), 'payload_bytes': len(payload),
         'result_sha256': d.digest(result), 'result_bytes': len(result)}
    key = Ed25519PrivateKey.generate()
    root = tmp_path / 'stage'
    root.mkdir(mode=0o700)
    (root / 'running-v1').write_bytes(b'old-correct-runtime')
    (root / 'current').symlink_to('running-v1')
    return root, m, key, payload, result

def invoke(c, *, mutation=None, payload=None, fault=None, **kwargs):
    root, m, key, original, result = c
    m = dict(m)
    if mutation:
        m.update(mutation)
    raw, sig, pub = d.sign_manifest(m, key)
    return d.stage(root, raw, sig, pub, lambda: io.BytesIO(original if payload is None else payload),
                   component='probe', commit=COMMIT, fault=fault, **kwargs)

def unchanged(c):
    root = c[0]
    assert os.readlink(root / 'current') == 'running-v1'
    assert (root / 'current').read_bytes() == b'old-correct-runtime'
    assert not list(root.glob('.incoming-*'))

@pytest.mark.parametrize(('path', 'build'), [
    ('apps/lmm-extensions/internal/app/run.go', ['extensions']),
    ('apps/lmm-core/src/main.rs', ['core', 'core-admin']),
    ('apps/web/src/main.tsx', ['web']),
    ('apps/lmm/src/main.rs', ['cli']),
    ('contracts/proto/lmm/core/v1/events.proto', ['core', 'core-admin', 'extensions']),
    ('apps/lmm-extensions/internal/corepb/control.pb.go', ['core', 'core-admin', 'extensions']),
    ('scripts/generate-core-protocol.sh', ['core', 'core-admin', 'extensions']),
    ('bun.lock', ['web']),
    ('deployment/docker/core.Dockerfile', ['core', 'core-admin']),
    ('docs/design.md', []),
])
def test_impact(path, build):
    assert impact.plan([path])['build'] == build

@pytest.mark.parametrize('path', ['packages/shared/api.ts', 'new-backend/main.rs', 'scripts/new-builder.py'])
def test_unknown_blocks(path):
    assert impact.plan([path])['blocked']

@pytest.mark.parametrize('path', ['../secrets', '/etc/shadow', 'a/../../b', 'a\\b'])
def test_unsafe_paths(path):
    with pytest.raises(ValueError):
        impact.plan([path])

def test_rename_paths():
    p = impact.plan(['apps/lmm-extensions/old.go', 'apps/web/new.ts'])
    assert p['build'] == ['extensions', 'web']

def test_stage_and_reuse(candidate):
    first = invoke(candidate)
    assert Path(first['path']).read_bytes() == candidate[-1]
    assert invoke(candidate)['reused']
    unchanged(candidate)

@pytest.mark.parametrize('mutation', [
    {'target': 'linux/arm64'}, {'component': 'core'}, {'commit': '0' * 40},
    {'repository': 'https://example.invalid/other'}, {'version': 'latest'},
    {'schema': 2}, {'payload_bytes': -1}, {'result_bytes': True},
    {'result_bytes': d.MAX_FILE + 1}, {'encoding': 'tar'}, {'unknown': 1},
    {'payload_sha256': '../secret'}, {'builder': ''},
])
def test_signed_metadata_rejection(candidate, mutation):
    with pytest.raises(d.Rejected):
        invoke(candidate, mutation=mutation)
    unchanged(candidate)

def test_wrong_signature(candidate):
    root, m, key, payload, _ = candidate
    raw, sig, _ = d.sign_manifest(m, key)
    _, _, wrong = d.sign_manifest(m, Ed25519PrivateKey.generate())
    with pytest.raises(d.Rejected):
        d.stage(root, raw, sig, wrong, lambda: io.BytesIO(payload), component='probe', commit=COMMIT)
    unchanged(candidate)

def test_tampered_manifest(candidate):
    root, m, key, payload, _ = candidate
    raw, sig, pub = d.sign_manifest(m, key)
    with pytest.raises(d.Rejected):
        d.stage(root, raw + b' ', sig, pub, lambda: io.BytesIO(payload), component='probe', commit=COMMIT)
    unchanged(candidate)

@pytest.mark.parametrize('mode', ['truncated', 'corrupt', 'trailing'])
def test_payload_damage(candidate, mode):
    p = candidate[3]
    p = {'truncated': p[:-4], 'corrupt': b'x' + p[1:], 'trailing': p + b'x'}[mode]
    with pytest.raises(d.Rejected):
        invoke(candidate, payload=p)
    unchanged(candidate)

def test_final_digest(candidate):
    with pytest.raises(d.Rejected):
        invoke(candidate, mutation={'result_sha256': 'a' * 64})
    unchanged(candidate)

def test_decompression_bound(candidate):
    with pytest.raises(d.Rejected):
        invoke(candidate, mutation={'result_bytes': 64})
    unchanged(candidate)

def test_wrong_elf(candidate):
    value = bytearray(candidate[-1])
    struct.pack_into('<H', value, 18, 183)
    payload = gzip.compress(value, mtime=0)
    mutation = {'payload_sha256': d.digest(payload), 'payload_bytes': len(payload),
                'result_sha256': d.digest(value), 'result_bytes': len(value)}
    with pytest.raises(d.Rejected):
        invoke(candidate, mutation=mutation, payload=payload)
    unchanged(candidate)

def test_disk_preflight(candidate):
    with pytest.raises(d.Rejected):
        invoke(candidate, free_bytes=lambda _: 0)
    unchanged(candidate)

@pytest.mark.parametrize('phase', ['before-transfer', 'after-transfer', 'after-decode', 'before-publish'])
def test_enospc_at_phase(candidate, phase):
    def fail(actual):
        if actual == phase:
            raise OSError(errno.ENOSPC, 'injected disk full')
    with pytest.raises(OSError):
        invoke(candidate, fault=fail)
    unchanged(candidate)

def test_lock(candidate):
    with (candidate[0] / '.lock').open('w') as f:
        fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
        with pytest.raises(d.Rejected):
            invoke(candidate)
    unchanged(candidate)

def test_existing_ready_tamper(candidate):
    ready = Path(invoke(candidate)['path'])
    ready.chmod(0o600)
    ready.write_bytes(b'bad')
    with pytest.raises(d.Rejected):
        invoke(candidate)
    unchanged(candidate)

def test_real_loopback_disconnect(candidate):
    root, m, key, payload, _ = candidate
    server = socket.socket()
    server.bind(('127.0.0.1', 0))
    server.listen(1)
    port = server.getsockname()[1]
    def reply():
        conn, _ = server.accept()
        with conn:
            conn.recv(4096)
            conn.sendall(f'HTTP/1.1 200 OK\r\nContent-Length: {len(payload)}\r\nConnection: close\r\n\r\n'.encode() + payload[:len(payload)//2])
        server.close()
    thread = threading.Thread(target=reply)
    thread.start()
    raw, sig, pub = d.sign_manifest(m, key)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with pytest.raises((d.Rejected, OSError)):
            d.stage(root, raw, sig, pub, lambda: opener.open(f'http://127.0.0.1:{port}/payload', timeout=3),
                    component='probe', commit=COMMIT)
    finally:
        thread.join(timeout=5)
    unchanged(candidate)

def test_delta_roundtrip_and_base_rejection(candidate, tmp_path):
    root, m, key, _, result = candidate
    old = tmp_path / 'old'
    new = tmp_path / 'new'
    patch = tmp_path / 'patch.zst'
    old.write_bytes(elf(b'two'))
    new.write_bytes(result)
    subprocess.run(['zstd', '-q', '-f', '--patch-from=' + str(old), str(new), '-o', str(patch)], check=True)
    payload = patch.read_bytes()
    update = {'encoding': 'zstd-patch', 'payload_sha256': d.digest(payload), 'payload_bytes': len(payload),
              'base_sha256': d.hash_file(old), 'base_bytes': old.stat().st_size}
    old.write_bytes(elf(b'bad'))
    with pytest.raises(d.Rejected):
        invoke(candidate, mutation=update, payload=payload, base=old)
    unchanged(candidate)
    old.write_bytes(elf(b'two'))
    staged = invoke(candidate, mutation=update, payload=payload, base=old)
    assert Path(staged['path']).read_bytes() == result
    unchanged(candidate)

import build_images
import oci_cost
from run_lab import make_oci

def test_pin_required():
    with pytest.raises(ValueError):
        build_images.command('core', Path('/tmp/in'), Path('/tmp/out'), 'linux/amd64', 'debian:latest')

def test_build_is_local_only():
    cmd = build_images.command('extensions', Path('/tmp/in'), Path('/tmp/out'), 'linux/amd64', 'example/base@sha256:' + 'a' * 64)
    assert '--push' not in cmd and '--load' not in cmd
    assert '--network=none' in cmd
    assert any(x.startswith('type=oci,dest=') for x in cmd)

def test_oci_reuse_and_content_damage(tmp_path):
    a, b = tmp_path / 'a', tmp_path / 'b'
    a.write_bytes(elf(b'A'))
    b.write_bytes(elf(b'B'))
    root = tmp_path / 'oci'
    make_oci(root, {'a': a, 'b': b}, COMMIT)
    old, new = oci_cost.inspect(root, 'a'), oci_cost.inspect(root, 'b')
    fresh = oci_cost.cost([], [old])
    update = oci_cost.cost([old], [new])
    assert fresh['new_unique_layer_blobs'] == 2
    assert update['new_unique_layer_blobs'] == 1
    assert oci_cost.cost([old, new], [new])['download_blob_bytes'] == 0
    target = root / 'blobs' / 'sha256' / new['layers'][-1]['digest'].split(':')[1]
    target.write_bytes(b'X' * target.stat().st_size)
    with pytest.raises(ValueError):
        oci_cost.inspect(root, 'b')

def test_oci_uncompressed_id_validation(tmp_path):
    binary = tmp_path / 'binary'
    binary.write_bytes(elf())
    root = tmp_path / 'oci'
    make_oci(root, {'one': binary}, COMMIT)
    index = json.loads((root / 'index.json').read_bytes())
    md = index['manifests'][0]
    manifest = json.loads(oci_cost.blob(root, md))
    cd = manifest['config']
    config = json.loads(oci_cost.blob(root, cd))
    config['rootfs']['diff_ids'][-1] = 'sha256:' + '0' * 64
    from run_lab import put
    manifest['config'] = put(root, d.canonical(config), cd['mediaType'])
    new_md = put(root, d.canonical(manifest), md['mediaType'])
    new_md['annotations'] = md['annotations']
    index['manifests'][0] = new_md
    (root / 'index.json').write_bytes(d.canonical(index))
    with pytest.raises(ValueError):
        oci_cost.inspect(root, 'one')

def test_storage_count_deduplicates_hardlinks(tmp_path):
    path = tmp_path / 'one'
    path.write_bytes(b'x' * 4096)
    before = d.disk_bytes(tmp_path)
    os.link(path, tmp_path / 'two')
    assert d.disk_bytes(tmp_path) == before

def test_root_package_script_review():
    p = impact.plan(['package.json'])
    assert p['blocked']
    assert 'cli' not in p['build']

def test_git_rename_and_deletion_use_both_paths(tmp_path, monkeypatch):
    subprocess.run(['git', 'init', '-q', str(tmp_path)], check=True)
    def git(*args):
        return subprocess.check_output(['git', '-C', str(tmp_path), '-c', 'user.name=DP27 test',
                                        '-c', 'user.email=test@localhost', *args], text=True).strip()
    a = tmp_path / 'apps/lmm-extensions/old name.go'
    a.parent.mkdir(parents=True)
    a.write_text('old')
    git('add', '.')
    git('commit', '-qm', 'old')
    base = git('rev-parse', 'HEAD')
    b = tmp_path / 'apps/web/new name.ts'
    b.parent.mkdir(parents=True)
    a.rename(b)
    git('add', '-A')
    git('commit', '-qm', 'rename')
    monkeypatch.chdir(tmp_path)
    paths = impact.changed_paths(base, 'HEAD')
    assert set(paths) == {'apps/lmm-extensions/old name.go', 'apps/web/new name.ts'}
    assert impact.plan(paths)['build'] == ['extensions', 'web']
