#!/usr/bin/env python3
"""Fail-closed component build plan. No builds, network or repository writes."""
from __future__ import annotations
import argparse
import json
import subprocess
from pathlib import PurePosixPath

SERVER = {'core', 'core-admin', 'extensions'}

def plan(paths: list[str]) -> dict:
    build, verify, reasons, unresolved = set(), set(), [], []
    for path in sorted(set(paths)):
        parts = PurePosixPath(path).parts
        if not path or path.startswith('/') or '..' in parts or '\\' in path:
            raise ValueError(f'unsafe repository path: {path!r}')
        b, v = set(), set()
        if path.startswith('experiments/deploy/dp-27/'):
            v = {'dp27-unit', 'dp27-local-distribution'}
        elif path.startswith('contracts/') or path == 'scripts/generate-core-protocol.sh' or path.startswith('apps/lmm-extensions/internal/corepb/'):
            b = SERVER.copy()
            v = {'generated-bindings', 'protocol-old-new', 'rust-tests', 'go-tests', 'http-contract', 'web-typecheck'}
        elif path.startswith('apps/lmm-core/'):
            b = {'core', 'core-admin'}
            v = {'rust-tests', 'ledger-durability', 'protocol-old-new'}
        elif path.startswith('apps/lmm-extensions/'):
            b = {'extensions'}
            v = {'go-tests', 'protocol-old-new', 'core-boundaries'}
        elif path.startswith('apps/web/'):
            b = {'web'}
            v = {'web-tests', 'web-typecheck', 'http-contract'}
        elif path.startswith('apps/lmm/'):
            b = {'cli'}
            v = {'cli-tests'}
        elif path in {'bun.lock', 'turbo.json'}:
            b = {'web'}
            v = {'workspace-lock', 'web-tests', 'web-typecheck'}
        elif path == 'package.json':
            b = {'web'}
            v = {'workspace-lock', 'web-tests', 'build-entry-review'}
            # It also defines backend build scripts, not just web dependencies.
            unresolved.append(path)
        elif path in {'deployment/docker/core.Dockerfile'}:
            b = {'core', 'core-admin'}
            v = {'image-runtime', 'ca-dns-timezone', 'rollback'}
        elif path in {'deployment/docker/extensions.Dockerfile'}:
            b = {'extensions'}
            v = {'image-runtime', 'ca-dns-timezone', 'rollback'}
        elif path == '.dockerignore':
            b = SERVER | {'web'}
            v = {'image-context', 'image-runtime'}
        elif path.startswith('deployment/docker/'):
            v = {'deployment-contract', 'no-node-build', 'rollback'}
        elif path.startswith(('.github/', 'scripts/', 'packages/', 'packaging/')):
            # Do not guess which shared package or build script is harmless.
            unresolved.append(path)
            v = {'manual-dependency-review'}
        elif path.startswith('docs/') or path.endswith('.md') or path in {'LICENSE', 'NOTICE', 'VERSION'}:
            v = {'docs-and-release-metadata'}
        else:
            unresolved.append(path)
            v = {'manual-dependency-review'}
        build |= b
        verify |= v
        reasons.append({'path': path, 'build': sorted(b), 'verify': sorted(v)})
    return {'schema': 1, 'build': sorted(build), 'verify': sorted(verify),
            'blocked': bool(unresolved), 'unclassified': unresolved, 'reasons': reasons,
            'node_builds': [], 'note': 'Validation of a peer is not a rebuild of that peer.'}

def changed_paths(base: str, head: str) -> list[str]:
    # --name-only --no-renames includes both sides of renames and deleted paths.
    # Missing history is an error, never an empty successful plan.
    refs = []
    for ref in (base, head):
        if ref.startswith('-'):
            raise ValueError('ref cannot start with -')
        refs.append(subprocess.check_output(['git', 'rev-parse', '--verify', ref + '^{commit}'], text=True).strip())
    data = subprocess.check_output(['git', 'diff', '--name-only', '--no-renames', '-z', *refs, '--'])
    return [p.decode('utf-8', 'strict') for p in data.split(b'\0') if p]

def main() -> int:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('paths', nargs='*')
    p.add_argument('--base')
    p.add_argument('--head', default='HEAD')
    args = p.parse_args()
    if args.base and args.paths:
        p.error('use paths or --base, not both')
    result = plan(changed_paths(args.base, args.head) if args.base else args.paths)
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 2 if result['blocked'] else 0

if __name__ == '__main__':
    raise SystemExit(main())
