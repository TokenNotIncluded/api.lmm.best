"""Authenticated, bounded backup files for isolated A09 tests; never an installer.

Full dumps, recovered plaintext and recovery keys stay under Backups. Keys use
separate private directories, never the directory containing a dump. Authentication finishes before any restored bytes are published to a
consumer. This module does not connect to a database or authorize an upgrade.
"""
from __future__ import annotations

from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import secrets
import stat
import struct
import tempfile

from cryptography.exceptions import InvalidTag
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

BACKUPS = Path('/home/lightjunction/Backups')
MAGIC = b'LMM-A09-BACKUP\x01\n'
CHUNK = 1024 * 1024
MAX_HEADER = 4096
MAX_DUMP = 16 * 1024**3


class BackupError(ValueError):
    """No unverified plaintext is safe to use after this error."""


def private_dir(path: Path) -> None:
    if not path.is_absolute() or path.resolve() != path:
        raise BackupError('an absolute directory without symlinks is required')
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077:
        raise BackupError('directory must be private and owned by the current user')


def in_backups(path: Path) -> None:
    private_dir(BACKUPS)
    private_dir(path.parent)
    if not path.is_absolute() or not path.is_relative_to(BACKUPS) or path == BACKUPS:
        raise BackupError('complete backup files must remain under Backups')


@contextmanager
def private_input(path: Path):
    private_dir(path.parent)
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as stream:
        info = os.fstat(stream.fileno())
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
                or info.st_mode & 0o077 or info.st_nlink != 1):
            raise BackupError('input must be an owned, private, unlinked regular file')
        yield stream


@contextmanager
def publish(path: Path):
    """Stage privately, fsync, then link once. Never replace a reader's file."""
    private_dir(path.parent)
    if path.exists() or path.is_symlink():
        raise FileExistsError('output already exists')
    fd, name = tempfile.mkstemp(prefix='.a09-', dir=path.parent)
    temp = Path(name)
    try:
        with os.fdopen(fd, 'wb') as stream:
            yield stream
            stream.flush()
            os.fsync(stream.fileno())
        os.link(temp, path, follow_symlinks=False)
        directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        temp.unlink(missing_ok=True)


def create_key(path: Path) -> None:
    in_backups(path)
    with publish(path) as stream:
        stream.write(secrets.token_bytes(32))


def load_key(path: Path) -> bytes:
    in_backups(path)
    with private_input(path) as stream:
        key = stream.read(33)
    if len(key) != 32:
        raise BackupError('AES-256 requires exactly 32 private key bytes')
    return key


def digest_stream(stream, limit: int | None = None) -> tuple[str, int]:
    limit = MAX_DUMP if limit is None else limit
    digest, count = hashlib.sha256(), 0
    while chunk := stream.read(CHUNK):
        count += len(chunk)
        if count > limit:
            raise BackupError('backup exceeds the isolated test size bound')
        digest.update(chunk)
    return digest.hexdigest(), count


def binding_check(binding: str) -> None:
    if (not isinstance(binding, str) or len(binding) != 64
            or any(char not in '0123456789abcdef' for char in binding)):
        raise BackupError('a frozen source/target contract SHA-256 is required')


def encrypt(source: Path, target: Path, key_file: Path, binding: str) -> dict:
    binding_check(binding)
    in_backups(source)
    in_backups(target)
    in_backups(key_file)
    if key_file.parent in {source.parent, target.parent}:
        raise BackupError('keep the recovery key in a separate private directory')
    key = load_key(key_file)
    with private_input(source) as stream:
        digest, size = digest_stream(stream)
        nonce = secrets.token_bytes(12)
        header = {'format': 1, 'binding': binding, 'sha256': digest,
                  'size': size, 'nonce': nonce.hex()}
        raw = json.dumps(header, sort_keys=True, separators=(',', ':')).encode('ascii')
        aad = MAGIC + struct.pack('>I', len(raw)) + raw
        cipher = Cipher(algorithms.AES(key), modes.GCM(nonce)).encryptor()
        cipher.authenticate_additional_data(aad)
        stream.seek(0)
        repeated, count = hashlib.sha256(), 0
        with publish(target) as output:
            output.write(aad)
            while chunk := stream.read(CHUNK):
                count += len(chunk)
                if count > size:
                    raise BackupError('dump changed while being encrypted')
                repeated.update(chunk)
                output.write(cipher.update(chunk))
            if count != size or repeated.hexdigest() != digest:
                raise BackupError('dump changed while being encrypted')
            output.write(cipher.finalize())
            output.write(cipher.tag)
    # The returned receipt has no rows, keys, credential paths or connection URLs.
    with private_input(target) as stream:
        encrypted_sha, encrypted_size = digest_stream(stream, MAX_DUMP + MAX_HEADER + 64)
    return {'format': 'a09-encrypted-backup-v1', 'binding': binding,
            'plaintext_sha256': digest, 'plaintext_bytes': size,
            'ciphertext_sha256': encrypted_sha, 'ciphertext_bytes': encrypted_size}


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise BackupError('duplicate backup header field')
        result[key] = value
    return result


def decrypt(source: Path, target: Path, key_file: Path, binding: str) -> dict:
    binding_check(binding)
    in_backups(source)
    in_backups(target)
    in_backups(key_file)
    if key_file.parent in {source.parent, target.parent}:
        raise BackupError('keep the recovery key in a separate private directory')
    key = load_key(key_file)
    with private_input(source) as stream:
        if stream.read(len(MAGIC)) != MAGIC:
            raise BackupError('unknown backup format')
        length = stream.read(4)
        if len(length) != 4 or not 1 <= (size := struct.unpack('>I', length)[0]) <= MAX_HEADER:
            raise BackupError('invalid backup header length')
        raw = stream.read(size)
        try:
            header = json.loads(raw, object_pairs_hook=unique_object)
            if (not isinstance(header, dict)
                    or set(header) != {'format', 'binding', 'sha256', 'size', 'nonce'}
                    or type(header['format']) is not int or header['format'] != 1
                    or header['binding'] != binding or type(header['size']) is not int
                    or not 0 <= header['size'] <= MAX_DUMP):
                raise BackupError('backup contract or size does not match')
            binding_check(header['sha256'])
            if not isinstance(header['nonce'], str) or len(header['nonce']) != 24:
                raise BackupError('invalid backup nonce')
            nonce = bytes.fromhex(header['nonce'])
            if len(nonce) != 12 or nonce.hex() != header['nonce']:
                raise BackupError('noncanonical backup nonce')
        except (KeyError, TypeError, UnicodeError, json.JSONDecodeError, ValueError):
            raise BackupError('invalid or mismatched backup header') from None
        aad = MAGIC + length + raw
        payload_size = header['size']
        if os.fstat(stream.fileno()).st_size != len(aad) + payload_size + 16:
            raise BackupError('truncated or extended encrypted backup')
        stream.seek(-16, os.SEEK_END)
        tag = stream.read(16)
        stream.seek(len(aad))
        cipher = Cipher(algorithms.AES(key), modes.GCM(nonce, tag)).decryptor()
        cipher.authenticate_additional_data(aad)
        digest, remaining = hashlib.sha256(), payload_size
        try:
            with publish(target) as output:
                while remaining:
                    chunk = stream.read(min(CHUNK, remaining))
                    if not chunk:
                        raise BackupError('truncated ciphertext')
                    remaining -= len(chunk)
                    data = cipher.update(chunk)
                    digest.update(data)
                    output.write(data)
                tail = cipher.finalize()
                digest.update(tail)
                output.write(tail)
                if digest.hexdigest() != header['sha256']:
                    raise BackupError('authenticated dump digest does not match')
        except InvalidTag:
            raise BackupError('backup authentication failed; do not restore') from None
    return {'format': 'a09-authenticated-dump-v1', 'binding': binding,
            'plaintext_sha256': digest.hexdigest(), 'plaintext_bytes': payload_size}
