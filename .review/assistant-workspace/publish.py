# Runs only in the dedicated publish job after both application checks pass.
# Does not execute application source or copy the temporary review helpers.
from pathlib import Path
import hashlib
import re
import subprocess
import sys

base = '7ccd63413cba38b5212c416bba69c12c7e4a7638'
branch = 'work/assistant-tool-center-reviewed-20261009'
artifacts = Path(sys.argv[1])

def git(*args, data=None):
    return subprocess.run(['git', *args], input=data, check=True, stdout=subprocess.PIPE).stdout

if git('ls-remote', '--heads', 'origin', 'refs/heads/'+branch).strip():
    raise ValueError('Review branch already exists; refusing to overwrite it')
patches = []
for kind, prefixes in [('go', ('apps/api-go/',)), ('web', ('apps/web/src/', 'apps/web/scripts/', 'docs/'))]:
    file = artifacts / (kind+'-source.patch')
    data = file.read_bytes()
    expected = (artifacts/(kind+'-source.sha256')).read_text().split()[0]
    if hashlib.sha256(data).hexdigest() != expected:
        raise ValueError('Artifact hash mismatch')
    headers = re.findall(rb'^diff --git a/(.+) b/(.+)$', data, re.M)
    if not headers or len(headers) > 100:
        raise ValueError('Unexpected patch size')
    for left, right in headers:
        path = right.decode('utf-8')
        if left != right or '..' in Path(path).parts or not path.startswith(prefixes):
            raise ValueError('Unexpected source path: '+path)
    if b'GIT binary patch' in data or re.search(rb'^(?:new file|old|new) mode (?!100644)', data, re.M):
        raise ValueError('Only normal text source files may be published')
    patches.append(data)
git('switch', '--create', branch, base)
for data in patches:
    git('apply', '--index', '--whitespace=error', '-', data=data)
git('diff', '--cached', '--check')
git('-c', 'user.name=github-actions[bot]', '-c', 'user.email=41898282+github-actions[bot]@users.noreply.github.com', 'commit', '-m', 'feat(assistant): unify tool configuration and add safe workspace visualizations')
git('push', 'origin', 'HEAD:refs/heads/'+branch)
print('Published review-only source:', branch, git('rev-parse', 'HEAD').decode().strip())
