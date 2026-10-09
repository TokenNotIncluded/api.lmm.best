# Temporary source recovery. Never deploy this helper.
from pathlib import Path
import base64
import re
import subprocess
import tempfile
import zlib

ROOT = Path.cwd()
BASE = 'ed3cb848c733c7ba16704f6ef964cecd132bf686'
MAIN = '7ccd63413cba38b5212c416bba69c12c7e4a7638'
WORKSPACE = ROOT / '.review/assistant-workspace'

def git(*args, cwd=ROOT, data=None):
    return subprocess.run(['git', *args], cwd=cwd, input=data, check=True, stdout=subprocess.PIPE).stdout

# A previous upload stopped mid-archive. Recover only whole diffs whose complete
# file contents match the original Git blob IDs. Incomplete data is never applied.
decoder = zlib.decompressobj(31)
raw = bytearray()
for part in sorted(WORKSPACE.glob('payload-*.txt')):
    encoded = part.read_text().strip()
    raw.extend(decoder.decompress(base64.b64decode(encoded[:len(encoded)//4*4], validate=True)))
blocks = re.split(rb'(?=^diff --git )', bytes(raw), flags=re.M)[1:]
with tempfile.TemporaryDirectory() as temp:
    tree = Path(temp) / 'source'
    git('worktree', 'add', '--detach', str(tree), BASE)
    paths = []
    try:
        for block in blocks[:-1]:
            text = block.decode('utf-8')
            match = re.match(r'diff --git a/(.+) b/(.+)\n', text)
            if not match or match[1] != match[2]:
                raise ValueError('Unexpected source path')
            path = match[2]
            if '..' in Path(path).parts or not path.startswith(('apps/api-go/', 'apps/web/src/features/')):
                raise ValueError('Source path outside review scope')
            expected = re.search(r'^index [0-9a-f]+\.\.([0-9a-f]+)', text, re.M)
            if not expected:
                raise ValueError('Missing original blob digest')
            git('apply', '--unidiff-zero', '-', cwd=tree, data=block)
            actual = git('hash-object', path, cwd=tree).decode().strip()
            if not actual.startswith(expected[1]):
                raise ValueError('Recovered blob digest mismatch: '+path)
            paths.append(path)
            print('Verified source:', path, flush=True)
        if len(paths) != 41:
            raise ValueError('Unexpected recovery file count')
        git('add', '-N', '--', *paths, cwd=tree)
        patch = git('diff', '--full-index', '--', *paths, cwd=tree)
    finally:
        git('worktree', 'remove', '--force', str(tree))
# Full-context three-way application preserves concurrent main changes.
git('-c', 'user.name=Source review', '-c', 'user.email=review@localhost', 'merge', '--no-edit', MAIN)
git('apply', '--3way', '-', data=patch)
for patchfile in sorted(WORKSPACE.glob('complete-*.diff')):
    git('apply', '--index', '--whitespace=error', str(patchfile))
