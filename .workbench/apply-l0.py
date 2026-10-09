import base64
import hashlib
import json
import lzma
from pathlib import Path
import subprocess
import sys

source = Path(__file__).parent
root = Path(sys.argv[1]).resolve()
data = lzma.decompress(base64.b64decode(''.join((source / f'l0-delta-{n}.b64').read_text().strip() for n in range(1, 4)), validate=True))
assert hashlib.sha256(data).hexdigest() == '8e01541623e51209f0e69905cc74cd54c5b00fc99404d52c3db00fcac9a222f4'
changes = json.loads(data)
for entry in changes:
    path = root / entry['path']
    assert root in path.resolve().parents and not path.is_symlink()
    assert entry['path'].startswith(('apps/web/src/', 'apps/api-go/', 'docs/security/'))
    old = path.read_bytes() if path.exists() else b''
    assert hashlib.sha256(old).hexdigest() == entry['base'], entry['path']
    if entry.get('delete'):
        path.unlink()
        result = b''
    elif 'new' in entry:
        result = entry['new'].encode()
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(result)
    else:
        lines = old.decode().splitlines(keepends=True)
        for start, end, text in reversed(entry['edits']):
            lines[start:end] = [text]
        result = ''.join(lines).encode()
        path.write_bytes(result)
    assert hashlib.sha256(result).hexdigest() == entry['result'], entry['path']
subprocess.run(['git', 'add', '--', *[entry['path'] for entry in changes]], cwd=root, check=True)
(root.parent / 'l0-paths.json').write_text(json.dumps([entry['path'] for entry in changes]))
print(f'Loaded {len(changes)} hash-verified changed paths')
