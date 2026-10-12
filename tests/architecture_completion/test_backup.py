"""Real cryptographic/file tests, not PostgreSQL restore evidence."""
import hashlib
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import backup


class BackupTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        # The A09 task requires even complete synthetic dumps to stay here.
        backup.BACKUPS.mkdir(mode=0o700, parents=True, exist_ok=True)
        backup.private_dir(backup.BACKUPS)

    def setUp(self):
        self.dumps = tempfile.TemporaryDirectory(prefix='a09-backup-test-', dir=backup.BACKUPS)
        self.keys = tempfile.TemporaryDirectory(prefix='a09-keys-', dir=backup.BACKUPS)
        self.addCleanup(self.dumps.cleanup)
        self.addCleanup(self.keys.cleanup)
        self.root, self.keyroot = Path(self.dumps.name), Path(self.keys.name)
        self.source = self.root / 'source.dump'
        self.encrypted = self.root / 'dump.aesgcm'
        self.restored = self.root / 'recovered.dump'
        self.key = self.keyroot / 'recovery.key'
        self.binding = 'a' * 64
        self.payload = b'PGDMP synthetic test payload\x00' * 500
        self.source.write_bytes(self.payload)
        self.source.chmod(0o600)
        backup.create_key(self.key)

    def capture(self):
        return backup.encrypt(self.source, self.encrypted, self.key, self.binding)

    def restore(self):
        return backup.decrypt(self.encrypted, self.restored, self.key, self.binding)

    def test_authenticated_round_trip_and_separate_key(self):
        receipt = self.capture()
        recovered = self.restore()
        self.assertEqual(self.restored.read_bytes(), self.payload)
        self.assertEqual(receipt['plaintext_sha256'], hashlib.sha256(self.payload).hexdigest())
        self.assertEqual(receipt['plaintext_sha256'], recovered['plaintext_sha256'])
        self.assertEqual(receipt['ciphertext_sha256'], hashlib.sha256(self.encrypted.read_bytes()).hexdigest())
        for path in (self.encrypted, self.restored, self.key):
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        self.assertNotIn(self.payload[:20], self.encrypted.read_bytes())
        self.assertNotIn(str(self.key), json.dumps(receipt))

    def test_empty_dump_has_valid_authentication(self):
        self.source.write_bytes(b'')
        self.capture()
        self.restore()
        self.assertEqual(self.restored.read_bytes(), b'')

    def test_streams_across_many_chunks(self):
        with patch.object(backup, 'CHUNK', 31):
            self.capture()
            self.restore()
        self.assertEqual(self.restored.read_bytes(), self.payload)

    def test_fresh_nonce_for_every_backup(self):
        self.capture()
        other = self.root / 'other.aesgcm'
        backup.encrypt(self.source, other, self.key, self.binding)
        self.assertNotEqual(self.encrypted.read_bytes(), other.read_bytes())

    def test_corrupt_ciphertext_never_publishes_plaintext(self):
        self.capture()
        contents = self.encrypted.read_bytes()
        for position in (len(contents)//2, len(contents)-1):
            with self.subTest(position=position):
                altered = bytearray(contents)
                altered[position] ^= 1
                self.encrypted.write_bytes(altered)
                with self.assertRaises(backup.BackupError):
                    self.restore()
                self.assertFalse(self.restored.exists())
                self.assertEqual(list(self.root.glob('.a09-*')), [])

    def test_wrong_or_missing_recovery_key(self):
        self.capture()
        self.key.unlink()
        with self.assertRaises(FileNotFoundError):
            self.restore()
        backup.create_key(self.key)
        with self.assertRaises(backup.BackupError):
            self.restore()
        self.assertFalse(self.restored.exists())

    def test_truncation_and_appended_data_rejected(self):
        self.capture()
        original = self.encrypted.read_bytes()
        for value in (original[:10], original[:-1], original + b'extra'):
            self.encrypted.write_bytes(value)
            with self.assertRaises(backup.BackupError):
                self.restore()
            self.assertFalse(self.restored.exists())

    def test_source_target_binding_rejected(self):
        self.capture()
        with self.assertRaises(backup.BackupError):
            backup.decrypt(self.encrypted, self.restored, self.key, 'b'*64)
        self.assertFalse(self.restored.exists())

    def test_wrong_key_size_rejected(self):
        for size in (0, 31, 33):
            self.key.write_bytes(b'k' * size)
            with self.assertRaises(backup.BackupError):
                self.capture()

    def test_nonprivate_files_and_directories_rejected(self):
        for mode in (0o640, 0o644, 0o660):
            self.key.chmod(mode)
            with self.assertRaises(backup.BackupError):
                self.capture()
        self.key.chmod(0o600)
        self.root.chmod(0o750)
        with self.assertRaises(backup.BackupError):
            self.capture()
        self.root.chmod(0o700)

    def test_symlinks_hardlinks_fifo_rejected(self):
        self.source.unlink()
        self.source.symlink_to(self.key)
        with self.assertRaises(OSError):
            self.capture()
        self.source.unlink()
        # Hard links need one filesystem; /tmp may be a different mount.
        peer = self.root / 'peer.dump'
        peer.write_bytes(self.payload)
        peer.chmod(0o600)
        os.link(peer, self.source)
        with self.assertRaises(backup.BackupError):
            self.capture()
        self.source.unlink()
        os.mkfifo(self.source, 0o600)
        with self.assertRaises(backup.BackupError):
            self.capture()

    def test_key_and_complete_dump_require_separate_private_directories(self):
        colocated = self.root / 'bad.key'
        backup.create_key(colocated)
        with self.assertRaises(backup.BackupError):
            backup.encrypt(self.source, self.encrypted, colocated, self.binding)
        self.capture()
        with self.assertRaises(backup.BackupError):
            backup.decrypt(self.encrypted, self.restored, colocated, self.binding)
        with self.assertRaises(backup.BackupError):
            backup.encrypt(self.source, self.keyroot / 'bad.dump', self.key, self.binding)
        with tempfile.TemporaryDirectory() as outside:
            with self.assertRaises(backup.BackupError):
                backup.create_key(Path(outside) / 'bad.key')
            with self.assertRaises(backup.BackupError):
                backup.encrypt(self.source, Path(outside) / 'bad.dump', self.key, self.binding)

    def test_existing_files_are_never_overwritten(self):
        self.capture()
        initial = self.encrypted.read_bytes()
        with self.assertRaises(FileExistsError):
            self.capture()
        self.assertEqual(self.encrypted.read_bytes(), initial)
        self.restored.write_bytes(b'preserve original')
        self.restored.chmod(0o600)
        with self.assertRaises(FileExistsError):
            self.restore()
        self.assertEqual(self.restored.read_bytes(), b'preserve original')
        with self.assertRaises(FileExistsError):
            backup.create_key(self.key)

    def test_dump_size_limit_precedes_publication(self):
        with patch.object(backup, 'MAX_DUMP', len(self.payload)-1):
            with self.assertRaises(backup.BackupError):
                self.capture()
        self.assertFalse(self.encrypted.exists())

    def test_bad_binding_is_rejected_without_writing(self):
        for bad in ('', 'x'*64, 'a'*63, None, 1):
            with self.subTest(binding=bad), self.assertRaises(backup.BackupError):
                backup.encrypt(self.source, self.encrypted, self.key, bad)
        self.assertFalse(self.encrypted.exists())

    def test_failure_during_encryption_does_not_publish(self):
        with patch.object(backup.Cipher, 'encryptor', side_effect=RuntimeError('test failure')):
            with self.assertRaises(RuntimeError):
                self.capture()
        self.assertFalse(self.encrypted.exists())
        self.assertFalse(list(self.root.glob('.a09-*')))


    def test_ciphertext_matches_independent_aesgcm_api(self):
        from cryptography.hazmat.primitives.ciphers.aead import AESGCM
        import struct
        self.capture()
        encoded = self.encrypted.read_bytes()
        offset = len(backup.MAGIC)
        size = struct.unpack('>I',encoded[offset:offset+4])[0]
        boundary = offset+4+size
        header = json.loads(encoded[offset+4:boundary])
        plain = AESGCM(self.key.read_bytes()).decrypt(
            bytes.fromhex(header['nonce']), encoded[boundary:], encoded[:boundary])
        self.assertEqual(plain,self.payload)

    def test_exact_maximum_plaintext_fits_with_ciphertext_header(self):
        with patch.object(backup,'MAX_DUMP',len(self.payload)):
            self.capture()
            self.restore()
        self.assertEqual(self.restored.read_bytes(),self.payload)


if __name__ == '__main__':
    unittest.main()
