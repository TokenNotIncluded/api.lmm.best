"""Reproduce reviewed Git changes and store immutable objects; never move a remote ref."""
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import time
import urllib.request

WIP = 'a28f06e88b7adf3701937f2a4018689bdc96ad85'
MAIN = '3948a0a1ee1cefdd016a7aa45a11851545bb4b85'
BASE = 'de2c439f26446792d4a605b2b91fa2b84777e284'
TREE = '23f1132edb782ff434254b5663a20c0cf262d06b'
OUT = Path(os.environ['S11_OUTPUT']).resolve()
OUT.mkdir(parents=True, exist_ok=True)

def git(*args, data=None):
    return subprocess.check_output(['git', *args], input=data)

def text(*args):
    return git(*args).decode().strip()

def conflicts():
    rows = {}
    for record in git('ls-files', '-u', '-z').split(b'\0'):
        if record:
            fields, path = record.split(b'\t', 1)
            rows.setdefault(path.decode(), []).append(fields.decode())
    return rows

def replay(plan_path):
    plan = json.loads(Path(plan_path).read_text())
    if text('status', '--porcelain'):
        raise RuntimeError('review checkout is not clean')
    git('config', 'user.name', 'LIghtJUNction')
    git('config', 'user.email', 'lightjunction.me@gmail.com')
    git('config', 'rerere.enabled', 'false')
    hooks = OUT / 'hooks'
    hooks.mkdir(exist_ok=True)
    hook = hooks / 'post-rewrite'
    hook.write_text('#!/bin/sh\ncat >> ' + shlex.quote(str(OUT / 'rewritten')) + '\n')
    hook.chmod(0o700)
    git('config', 'core.hooksPath', str(hooks))
    git('switch', '--detach', WIP)
    env = dict(os.environ, GIT_EDITOR='true')
    command = ['git', 'rebase', '--rebase-merges', '--onto', MAIN, BASE]
    with (OUT / 'rebase.log').open('w') as log:
        for row in plan:
            result = subprocess.run(command, env=env, stdout=log, stderr=subprocess.STDOUT)
            if result.returncode == 0 or text('rev-parse', 'REBASE_HEAD') != row['commit']:
                raise RuntimeError('unexpected rebase stop')
            staged = conflicts()
            digest = hashlib.sha256(json.dumps(staged, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
            if digest != row['preimage_sha256']:
                (OUT / 'unexpected-conflicts.json').write_text(json.dumps(staged, indent=2))
                raise RuntimeError('conflict preimage differs at ' + row['commit'] + ': ' + digest)
            choices = {f[0]: f[1:] for f in row['files']}
            if not set(choices).issubset(staged):
                raise RuntimeError('resolution path missing')
            for name in staged:
                choice = choices.get(name, [row['default']])
                kind = choice[0]
                if kind == 'delete':
                    git('rm', '-f', '--', name)
                    continue
                if kind in ('1', '2', '3'):
                    data = git('show', ':' + kind + ':' + name)
                elif kind == 'original':
                    data = git('show', row['commit'] + ':' + name)
                elif kind == 'conflict-theirs':
                    data = re.sub(r'<<<<<<< HEAD\n.*?=======\n(.*?)>>>>>>>[^\n]*\n', r'\1', Path(name).read_text(), flags=re.S).encode()
                elif kind in ('replace', 'append', 'patch'):
                    data = git('show', ':' + choice[1] + ':' + name)
                    if kind == 'replace':
                        before, after = choice[2].encode(), choice[3].encode()
                        if data.count(before) != 1:
                            raise RuntimeError('replacement does not match once')
                        data = data.replace(before, after)
                    elif kind == 'append':
                        data += choice[2].encode()
                    else:
                        Path(name).write_bytes(data)
                        git('apply', '--whitespace=nowarn', data=choice[2].encode())
                        data = Path(name).read_bytes()
                else:
                    raise RuntimeError('unsupported reviewed resolution')
                if kind in ('replace', 'append', 'patch', 'conflict-theirs'):
                    actual = hashlib.sha1(b'blob ' + str(len(data)).encode() + b'\0' + data).hexdigest()
                    if actual != choice[-1]:
                        raise RuntimeError('resolution bytes differ: ' + name)
                Path(name).write_bytes(data)
                git('add', '--', name)
            command = ['git', 'rebase', '--continue']
        subprocess.run(command, env=env, stdout=log, stderr=subprocess.STDOUT, check=True)
    if text('rev-parse', 'HEAD^{tree}') != TREE:
        raise RuntimeError('final rebase tree differs from locally reviewed tree')
    if text('rev-list', '--count', MAIN + '..HEAD') != '113':
        raise RuntimeError('unexpected rebased commit count')
    git('bundle', 'create', str(OUT / 'rebased.bundle'), 'HEAD', '^' + MAIN, '^' + WIP)
    (OUT / 'source.json').write_text(json.dumps({'head': text('rev-parse', 'HEAD'), 'tree': TREE, 'main': MAIN, 'old_wip': WIP}, indent=2))
    print('Rebase reproduced:', text('rev-parse', 'HEAD'), TREE, flush=True)

def tree_entries(commit):
    rows = {}
    for row in git('ls-tree', '-r', '-z', commit).split(b'\0'):
        if row:
            fields, path = row.split(b'\t', 1)
            mode, kind, sha = fields.decode().split()
            rows[path.decode()] = {'path': path.decode(), 'mode': mode, 'type': kind, 'sha': sha}
    return rows

def publish():
    if text('rev-parse', 'HEAD^{tree}') != TREE:
        raise RuntimeError('publish tree mismatch')
    token = os.environ['GH_TOKEN']
    api = 'https://api.github.com/repos/TokenNotIncluded/api.lmm.best/git/'
    def post(kind, payload):
        if kind not in ('blobs', 'trees', 'commits'):
            raise RuntimeError('remote ref writes are not supported')
        request = urllib.request.Request(api + kind, data=json.dumps(payload).encode(), method='POST', headers={
            'Authorization': 'Bearer ' + token, 'Accept': 'application/vnd.github+json',
            'X-GitHub-Api-Version': '2022-11-28', 'Content-Type': 'application/json'})
        time.sleep(1.1)
        with urllib.request.urlopen(request, timeout=60) as response:
            return json.load(response)['sha']
    known = {line.split()[0] for line in text('rev-list', '--objects', WIP, MAIN).splitlines()}
    original = {}
    for line in (OUT / 'rewritten').read_text().splitlines():
        old, new = line.split()[:2]
        original.setdefault(new, []).append(old)
    mapping = {}
    records = []
    for commit in text('rev-list', '--reverse', '--topo-order', 'HEAD', '^' + MAIN).splitlines():
        raw = git('cat-file', 'commit', commit)
        message = raw.split(b'\n\n', 1)[1].decode()
        candidates = original[commit]
        old = next((c for c in candidates if git('cat-file', 'commit', c).split(b'\n\n', 1)[1].decode() == message), candidates[0])
        before, after = tree_entries(old), tree_entries(commit)
        changes = []
        for path in sorted(before.keys() | after.keys()):
            if before.get(path) == after.get(path):
                continue
            entry = dict(after.get(path) or before[path])
            if path not in after:
                entry['sha'] = None
            elif entry['type'] == 'blob' and entry['sha'] not in known:
                actual = post('blobs', {'content': base64.b64encode(git('cat-file', 'blob', entry['sha'])).decode(), 'encoding': 'base64'})
                if actual != entry['sha']:
                    raise RuntimeError('uploaded blob mismatch')
                known.add(actual)
            changes.append(entry)
        tree = text('rev-parse', old + '^{tree}')
        if changes:
            tree = post('trees', {'base_tree': tree, 'tree': changes})
        if tree != text('rev-parse', commit + '^{tree}'):
            raise RuntimeError('uploaded tree mismatch')
        fields = text('show', '-s', '--format=%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI%x00%P', commit).split('\0')
        author = dict(zip(('name', 'email', 'date'), fields[:3]))
        committer = dict(zip(('name', 'email', 'date'), fields[3:6]))
        parents = [mapping.get(p, p) for p in fields[6].split()]
        remote = post('commits', {'message': message, 'tree': tree, 'parents': parents, 'author': author, 'committer': committer})
        mapping[commit] = remote
        records.append({'local': commit, 'remote': remote, 'original': old, 'tree': tree, 'parents': parents})
        (OUT / 'published.json').write_text(json.dumps(records, indent=2))
        print('Stored immutable commit', len(records), remote, flush=True)
    head = mapping[text('rev-parse', 'HEAD')]
    (OUT / 'remote-head.json').write_text(json.dumps({'head': head, 'tree': TREE, 'main': MAIN, 'commits': len(records), 'remote_refs_changed': False}, indent=2))
    print('No remote refs changed. Candidate:', head, flush=True)

if __name__ == '__main__':
    if sys.argv[1] == 'replay':
        replay(sys.argv[2])
    elif sys.argv[1] == 'publish':
        publish()
    else:
        raise SystemExit('expected replay or publish')
