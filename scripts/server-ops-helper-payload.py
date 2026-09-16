"""Construct a bounded private transport payload for the locally tested helper."""
import base64
import gzip
import hashlib
import json
from pathlib import Path
import stat

MAX_BINARY = 128 * 1024 * 1024
MAX_COMPRESSED = 48 * 1024 * 1024


def prepare_helper_payload(payload, root, revision):
    root = Path(root)
    binary = root / 'incident343-recovery'
    receipt = root / 'receipt.json'
    for path, limit in ((binary, MAX_BINARY), (receipt, 4096)):
        info = path.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_size <= 0 or info.st_size > limit:
            raise ValueError('Invalid prepared helper file')
    data = binary.read_bytes()
    metadata = json.loads(receipt.read_text())
    digest = hashlib.sha256(data).hexdigest()
    if data[:4] != b'\x7fELF' or metadata != {'revision': revision, 'sha256': digest}:
        raise ValueError('Helper receipt does not match the tested revision and ELF bytes')
    compressed = gzip.compress(data, compresslevel=6, mtime=0)
    if len(compressed) > MAX_COMPRESSED:
        raise ValueError('Prepared helper exceeds compressed transfer limit')
    encoded = base64.b64encode(compressed).decode('ascii')
    # The generated code and binary go only through pinned SSH stdin, never logs.
    prefix = """set -euo pipefail
python3 - "$LMM_OPS_REPORT" <<'INCIDENT343_BINARY'
import base64,gzip,hashlib,io,json,os
from pathlib import Path
report=Path(__import__('sys').argv[1])
target=report.parent/'incident343-recovery'
compressed=base64.b64decode(ENCODED,validate=True)
if len(compressed)>50331648:raise ValueError('compressed helper is oversized')
count=0
hasher=hashlib.sha256()
fd=os.open(target,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o700)
with os.fdopen(fd,'wb') as output,gzip.GzipFile(fileobj=io.BytesIO(compressed)) as source:
    while True:
        chunk=source.read(65536)
        if not chunk:break
        count+=len(chunk)
        if count>134217728:raise ValueError('helper is oversized')
        hasher.update(chunk);output.write(chunk)
    output.flush();os.fsync(output.fileno())
if hasher.hexdigest()!=DIGEST:raise ValueError('helper transfer identity mismatch')
(target.parent/'helper-receipt.json').write_text(json.dumps({'revision':REVISION,'sha256':DIGEST})+'\\n')
INCIDENT343_BINARY
"""
    prefix = prefix.replace('ENCODED', repr(encoded)).replace('DIGEST', repr(digest)).replace('REVISION', repr(revision))
    return prefix.encode() + payload
