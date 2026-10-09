# Temporary source-only review helper; excluded from the final feature commit.
from pathlib import Path
import hashlib
import json
import re
import subprocess

root = Path.cwd()
workspace = root / '.review/assistant-workspace'

def git(*args, data=None):
    return subprocess.run(['git', *args], input=data, check=True, stdout=subprocess.PIPE).stdout

def safe(path):
    if '..' in Path(path).parts or not path.startswith(('apps/api-go/', 'apps/web/src/', 'docs/')) or (root / path).is_symlink():
        raise ValueError('Path outside reviewed source: ' + path)

for patch in sorted(workspace.glob('final-*.diff')):
    data = patch.read_bytes()
    entries = []
    for block in re.split(rb'(?=^diff --git )', data, flags=re.M)[1:]:
        header = re.match(rb'diff --git a/(.+) b/(.+)\n', block)
        if not header or header[1] != header[2]:
            raise ValueError('Unexpected path change')
        path = header[2].decode()
        safe(path)
        digest = re.search(rb'^index ([0-9a-f]{40})\.\.([0-9a-f]{40})', block, re.M)
        if not digest:
            raise ValueError('Missing full blob IDs: ' + path)
        before, after = digest[1].decode(), digest[2].decode()
        actual = git('hash-object', path).decode().strip() if (root/path).exists() else '0'*40
        if actual != before:
            raise ValueError('Before hash mismatch: '+path)
        entries.append((path, after))
    git('apply', '--index', '--unidiff-zero', '--whitespace=error', '-', data=data)
    for path, expected in entries:
        if git('hash-object', path).decode().strip() != expected:
            raise ValueError('After hash mismatch: '+path)
        print('Verified completion:', path, flush=True)

for path, spec in json.loads((workspace/'final-ui.json').read_text()).items():
    safe(path)
    original = (root/path).read_bytes()
    if hashlib.sha256(original).hexdigest() != spec['before']:
        raise ValueError('Original UI hash mismatch: '+path)
    lines = original.decode().splitlines(keepends=True)
    result = ''.join(part if isinstance(part, str) else ''.join(lines[part[0]:part[1]]) for part in spec['parts']).encode()
    if hashlib.sha256(result).hexdigest() != spec['after']:
        raise ValueError('Updated UI hash mismatch: '+path)
    (root/path).write_bytes(result)
    git('add', '--', path)
    print('Verified moved settings:', path, flush=True)
