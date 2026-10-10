#!/usr/bin/env python3
"""Local signed-file staging lab, NOT a production updater or Docker replacement.

It never writes a running-version pointer. A release controller must separately
verify image provenance, schema compatibility and readiness before switching.
Python and cryptography are build/test dependencies, not service dependencies.
"""
from __future__ import annotations
import argparse
import fcntl
import gzip
import hashlib
import json
import os
import platform
import re
import resource
import shutil
import stat
import struct
import subprocess
import tempfile
from pathlib import Path
from typing import BinaryIO, Callable
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey, Ed25519PublicKey
from cryptography.hazmat.primitives import serialization

MAX_FILE = 256 * 1024 * 1024
MAX_MANIFEST = 16 * 1024
RESERVE = 16 * 1024 * 1024
REPOSITORY = 'https://github.com/TokenNotIncluded/api.lmm.best'

class Rejected(ValueError):
    """Candidate failed a verification or local resource rule."""

def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()

def hash_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open('rb') as source:
        while block := source.read(65536):
            h.update(block)
    return h.hexdigest()

def canonical(obj: dict) -> bytes:
    return json.dumps(obj, sort_keys=True, separators=(',', ':'), ensure_ascii=True).encode()

def no_duplicates(pairs: list) -> dict:
    obj = {}
    for key, value in pairs:
        if key in obj:
            raise Rejected('duplicate JSON field')
        obj[key] = value
    return obj

def target_platform() -> str:
    arch = {'x86_64': 'amd64', 'aarch64': 'arm64'}.get(platform.machine())
    if not arch or platform.system() != 'Linux':
        raise Rejected('unsupported host platform')
    return 'linux/' + arch

def validate_manifest(raw: bytes, signature: bytes, trusted_public: bytes,
                      expected_component: str, expected_commit: str,
                      expected_target: str | None = None) -> dict:
    if len(raw) > MAX_MANIFEST or len(signature) != 64 or len(trusted_public) != 32:
        raise Rejected('manifest, key or signature size')
    try:
        Ed25519PublicKey.from_public_bytes(trusted_public).verify(signature, raw)
        m = json.loads(raw, object_pairs_hook=no_duplicates)
    except Exception as exc:
        raise Rejected('signature or manifest rejected') from exc
    if not isinstance(m, dict) or m.get('schema') != 1:
        raise Rejected('unsupported schema')
    required = {'schema', 'component', 'version', 'repository', 'commit', 'target',
                'builder', 'encoding', 'payload_sha256', 'payload_bytes',
                'result_sha256', 'result_bytes'}
    if set(m) not in (required, required | {'base_sha256', 'base_bytes'}):
        raise Rejected('unexpected or missing manifest fields')
    if m['component'] not in {'core', 'core-admin', 'extensions', 'probe'} or m['component'] != expected_component:
        raise Rejected('wrong component')
    if m['repository'] != REPOSITORY or not re.fullmatch('[0-9a-f]{40}', str(m['commit'])) or m['commit'] != expected_commit:
        raise Rejected('unexpected source revision')
    if not re.fullmatch(r'v\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.+-]+)?', str(m['version'])):
        raise Rejected('not an immutable version name')
    if m['target'] != (expected_target or target_platform()):
        raise Rejected('wrong platform')
    if not isinstance(m['builder'], str) or not 1 <= len(m['builder']) <= 128:
        raise Rejected('missing builder identity')
    if m['encoding'] not in {'raw', 'gzip', 'zstd-patch'}:
        raise Rejected('unsupported encoding')
    if (m['encoding'] == 'zstd-patch') != ('base_sha256' in m):
        raise Rejected('delta needs exact base')
    for key in ('payload_sha256', 'result_sha256') + (('base_sha256',) if 'base_sha256' in m else ()):
        if not re.fullmatch('[0-9a-f]{64}', str(m[key])):
            raise Rejected('invalid content digest')
    for key in ('payload_bytes', 'result_bytes') + (('base_bytes',) if 'base_bytes' in m else ()):
        if type(m[key]) is not int or not 0 < m[key] <= MAX_FILE:
            raise Rejected('artifact size is out of bounds')
    return m

def copy_bounded(source: BinaryIO, target: Path, expected_bytes: int, expected_hash: str) -> None:
    h, count = hashlib.sha256(), 0
    with target.open('xb') as out:
        while True:
            data = source.read(min(65536, expected_bytes - count + 1))
            if not data:
                break
            count += len(data)
            if count > expected_bytes:
                raise Rejected('artifact exceeds declared size')
            h.update(data)
            out.write(data)
        if count != expected_bytes or h.hexdigest() != expected_hash:
            raise Rejected('truncated or corrupt artifact')
        out.flush()
        os.fsync(out.fileno())

def fsync_dir(path: Path) -> None:
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)

def check_elf(path: Path, target: str) -> None:
    with path.open('rb') as source:
        header = source.read(64)
    expected = {'linux/amd64': 62, 'linux/arm64': 183}.get(target)
    if len(header) != 64 or header[:6] != b'\x7fELF\x02\x01' or expected is None:
        raise Rejected('not a supported 64-bit little-endian ELF')
    if struct.unpack_from('<H', header, 18)[0] != expected:
        raise Rejected('ELF architecture differs from manifest')
    if struct.unpack_from('<H', header, 16)[0] not in (2, 3):
        raise Rejected('not an executable ELF')
    # Loader/library ABI is checked in a runtime image test, not inferred here.

def disk_bytes(root: Path) -> int:
    seen, total = set(), 0
    for path in [root, *root.rglob('*')]:
        info = path.lstat()
        if stat.S_ISLNK(info.st_mode):
            continue
        identity = (info.st_dev, info.st_ino)
        if identity not in seen:
            seen.add(identity)
            total += info.st_blocks * 512
    return total

def stage(root: Path, raw: bytes, signature: bytes, trusted_public: bytes,
          open_payload: Callable[[], BinaryIO], *, component: str, commit: str,
          base: Path | None = None, reserve: int = RESERVE,
          free_bytes: Callable[[Path], int] | None = None,
          fault: Callable[[str], None] | None = None) -> dict:
    """Verify, copy, decode, verify ELF, and publish only an immutable ready file.

    The private root is local to this experiment. Same-UID malicious mutation,
    power-loss durability on an untested filesystem and active-version switching
    are outside this lab. Fault hooks are test-only and never available in CLI.
    """
    m = validate_manifest(raw, signature, trusted_public, component, commit)
    if reserve < 0:
        raise Rejected('negative reserve')
    root = Path(root).absolute()
    if root.is_symlink():
        raise Rejected('symlink stage root')
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    st = root.stat()
    if st.st_uid != os.getuid() or st.st_mode & 0o077:
        raise Rejected('stage root must be private and owned by current user')
    lockfd = os.open(root / '.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    hook = fault or (lambda _: None)
    try:
        try:
            fcntl.flock(lockfd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise Rejected('another stage operation holds the lock') from exc
        required = m['payload_bytes'] + m['result_bytes'] + m.get('base_bytes', 0) + reserve
        free = free_bytes(root) if free_bytes else shutil.disk_usage(root).free
        if free < required:
            raise Rejected('insufficient staging space')
        target = root / ('ready-' + m['result_sha256'])
        if target.is_symlink():
            raise Rejected('symlink ready path')
        if target.exists():
            if not target.is_file() or hash_file(target) != m['result_sha256']:
                raise Rejected('corrupt existing ready file')
            return {'reused': True, 'path': str(target), 'download_bytes': 0,
                    'active_changed': False, 'manifest_sha256': digest(raw)}
        temp = Path(tempfile.mkdtemp(prefix='.incoming-', dir=root))
        observations = []
        def observe(phase: str) -> None:
            observations.append({'phase': phase, 'allocated_bytes': disk_bytes(root)})
            hook(phase)
        try:
            observe('before-transfer')
            with open_payload() as source:
                copy_bounded(source, temp / 'payload', m['payload_bytes'], m['payload_sha256'])
            observe('after-transfer')
            result = temp / 'result'
            if m['encoding'] == 'zstd-patch':
                if base is None or base.is_symlink():
                    raise Rejected('missing or symlink delta base')
                with base.open('rb') as source:
                    copy_bounded(source, temp / 'base', m['base_bytes'], m['base_sha256'])
                # Work from the verified private copy, not a mutable running file.
                def limit_output() -> None:
                    resource.setrlimit(resource.RLIMIT_FSIZE, (m['result_bytes'], m['result_bytes']))
                proc = subprocess.run(['zstd', '-d', '--memory=64MB', '--patch-from=' + str(temp / 'base'),
                                       str(temp / 'payload'), '-o', str(result), '-q'],
                                      timeout=30, check=False, capture_output=True, preexec_fn=limit_output)
                if proc.returncode:
                    raise Rejected('delta decode failed')
            else:
                opener = gzip.open if m['encoding'] == 'gzip' else open
                with opener(temp / 'payload', 'rb') as source:
                    copy_bounded(source, result, m['result_bytes'], m['result_sha256'])
            if result.stat().st_size != m['result_bytes'] or hash_file(result) != m['result_sha256']:
                raise Rejected('final artifact mismatch')
            check_elf(result, m['target'])
            result.chmod(0o500)
            with result.open('rb') as file:
                os.fsync(file.fileno())
            observe('after-decode')
            observe('before-publish')
            # Link is atomic and cannot replace any existing file, including one
            # created unexpectedly. Publication never touches a live pointer.
            os.link(result, target, follow_symlinks=False)
            fsync_dir(root)
            observe('after-publish')
            return {'reused': False, 'path': str(target), 'download_bytes': m['payload_bytes'],
                    'preflight_required_bytes': required, 'phase_samples': observations,
                    'sampled_peak_allocated_bytes': max(x['allocated_bytes'] for x in observations),
                    'active_changed': False, 'manifest_sha256': digest(raw)}
        finally:
            shutil.rmtree(temp)
            fsync_dir(root)
    finally:
        os.close(lockfd)

def sign_manifest(m: dict, key: Ed25519PrivateKey) -> tuple[bytes, bytes, bytes]:
    raw = canonical(m)
    public = key.public_key().public_bytes(serialization.Encoding.Raw, serialization.PublicFormat.Raw)
    return raw, key.sign(raw), public

def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, required=True)
    parser.add_argument('--manifest', type=Path, required=True)
    parser.add_argument('--signature', type=Path, required=True)
    parser.add_argument('--trusted-public-key', type=Path, required=True)
    parser.add_argument('--payload', type=Path, required=True)
    parser.add_argument('--component', required=True)
    parser.add_argument('--commit', required=True)
    parser.add_argument('--base', type=Path)
    args = parser.parse_args()
    def small(path: Path, maximum: int) -> bytes:
        with path.open('rb') as file:
            data = file.read(maximum + 1)
        if len(data) > maximum:
            raise Rejected('metadata too large')
        return data
    result = stage(args.root, small(args.manifest, MAX_MANIFEST), small(args.signature, 64),
                   small(args.trusted_public_key, 32), lambda: args.payload.open('rb'),
                   component=args.component, commit=args.commit, base=args.base)
    print(json.dumps(result, indent=2))

if __name__ == '__main__':
    main()
