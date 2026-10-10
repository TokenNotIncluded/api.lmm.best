#!/usr/bin/env python3
"""Build-local OCI exporter. Never pushes, starts, deploys, or calls the user CLI.

This must run on a builder with Docker/Buildx and precompiled, tested artifacts.
No implicit base image or architecture is accepted. Commands are printed by
 default; --execute is a separate explicit local-build opt-in.
"""
import argparse
import json
import re
import subprocess
from pathlib import Path

HERE = Path(__file__).resolve().parent

def command(component, context, output, platform, base=None):
    if component not in {'glibc-base', 'static-base', 'core', 'core-admin', 'extensions', 'web'}:
        raise ValueError('unsupported component')
    if platform not in {'linux/amd64', 'linux/arm64'}:
        raise ValueError('explicit supported platform required')
    args = ['docker', 'buildx', 'build', '--platform', platform, '--network=none',
            '--provenance=mode=max', '--sbom=true', '--file', str(HERE / 'docker' / (component + '.Dockerfile')),
            '--output', 'type=oci,dest=' + str(output), '--tag', 'dp27-local/' + component + ':candidate']
    if component != 'static-base':
        if not base or not re.fullmatch(r'[^\s]+@sha256:[0-9a-f]{64}', base):
            raise ValueError('base image must use @sha256 with a complete digest')
        name = {'glibc-base': 'OS_BASE', 'web': 'WEB_BASE'}.get(component, 'RUNTIME_BASE')
        args += ['--build-arg', name + '=' + base]
    args += [str(context)]
    return args

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('component')
    parser.add_argument('--context', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--platform', required=True)
    parser.add_argument('--base')
    parser.add_argument('--execute', action='store_true')
    args = parser.parse_args()
    cmd = command(args.component, args.context, args.output, args.platform, args.base)
    print(json.dumps({'command': cmd, 'executed': args.execute}, indent=2))
    if args.execute:
        if not args.context.is_dir():
            raise ValueError('missing precompiled artifact context')
        if args.output.exists():
            raise ValueError('refuse to overwrite an existing OCI export')
        subprocess.run(cmd, check=True)

if __name__ == '__main__':
    main()
