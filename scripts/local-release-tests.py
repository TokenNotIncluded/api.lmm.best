#!/usr/bin/env python3
"""Record local tests, or verify their unchanged source before publication.

run --component web --output /private/tests.json -- bun run --filter @lmm/web test
import --component web --revision TESTED_SHA --output /private/tests.json \
  --command 'completed command' --exit-code 0 --stdout /private/test.log
verify RELEASE_SHA --component web [--evidence /private/tests.json]

Import records the operator's completed-command exit code; it does not rerun it.
Records are local evidence, not a signature or production acceptance result.
"""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys

EXTENSION_PATHS = ('apps/lmm-extensions', 'contracts', 'deployment/docker/extensions.Dockerfile',
                   'deployment/docker/compose.extensions.yml', 'deployment/docker/compose.rpc-extensions.yml',
                   'scripts/generate-core-protocol.sh')
PATHS = {
    'web': ('apps/web', 'packages', 'package.json', 'bun.lock', 'contracts/api-route',
            'packaging/common/lmm-api/lmm-api-web.install'),
    'go': EXTENSION_PATHS,
    'extensions': EXTENSION_PATHS,
    'core': ('apps/lmm-core', 'contracts', 'deployment/docker/core.Dockerfile',
             'deployment/docker/compose.core.yml', 'deployment/docker/compose.identity.yml',
             'deployment/docker/compose.rpc-core.yml', 'scripts/generate-core-protocol.sh'),
    'full': ('.',),
}
SHA_RE = re.compile(r'^[0-9a-f]{40}$')
HASH_RE = re.compile(r'^[0-9a-f]{64}$')
digest = lambda raw: hashlib.sha256(raw).hexdigest()


def git(*args):
    result = subprocess.run(['git', '--no-replace-objects', *args], capture_output=True)
    if result.returncode:
        raise ValueError('could not read immutable Git source')
    return result.stdout


def revision(value):
    result = git('rev-parse', '--verify', value + '^{commit}').decode().strip()
    if not SHA_RE.fullmatch(result):
        raise ValueError('source must resolve to a full commit SHA')
    return result


def source_objects(commit, component):
    if component == 'full':
        return {'.': git('rev-parse', commit + '^{tree}').decode().strip()}
    required_root = {'web': 'apps/web', 'go': 'apps/lmm-extensions',
                     'extensions': 'apps/lmm-extensions', 'core': 'apps/lmm-core'}[component]
    if not git('ls-tree', '-d', commit, '--', required_root).strip():
        raise ValueError('component source directory is missing: ' + required_root)
    return {path: git('ls-tree', '-z', commit, '--', path).decode()
            for path in PATHS[component]}


def unique_object(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError('duplicate evidence field')
        value[key] = item
    return value


def parse(raw):
    return json.loads(raw, object_pairs_hook=unique_object)


def evidence(args):
    path = args.evidence or os.environ.get('LMM_LOCAL_TEST_EVIDENCE')
    raw = Path(path).read_bytes() if path else os.environ.get('LMM_LOCAL_TEST_EVIDENCE_JSON', '').encode()
    if not raw:
        raise ValueError('local test evidence is required; use LMM_LOCAL_TEST_EVIDENCE or --evidence')
    if len(raw) > 48000:
        raise ValueError('local test evidence exceeds the workflow input budget')
    return parse(raw)


def verify(args):
    if not SHA_RE.fullmatch(args.revision):
        raise ValueError('release revision must be a full lowercase SHA')
    commit = revision(args.revision)
    record = evidence(args)
    if not isinstance(record, dict) or record.get('format') != 1 or record.get('component') != args.component:
        raise ValueError('local test evidence component/format differs')
    tested = record.get('source_revision', '')
    if not isinstance(tested, str) or not SHA_RE.fullmatch(tested):
        raise ValueError('invalid tested source revision')
    objects = source_objects(commit, args.component)
    if record.get('source_objects') != objects:
        raise ValueError('release sources differ from the locally tested component')
    # A checkout after a squash merge need not retain the tested commit object.
    # Git object IDs still bind every declared component source byte.
    checks = record.get('checks')
    if not isinstance(checks, list) or not checks:
        raise ValueError('local test evidence has no completed checks')
    for check in checks:
        if not isinstance(check, dict) or type(check.get('exit_code')) is not int or check['exit_code'] != 0:
            raise ValueError('local tests did not complete successfully')
        if not isinstance(check.get('command'), str) or not check['command'].strip():
            raise ValueError('local test command is missing')
        if check.get('recorded_from') not in ('local-run', 'completed-log-import'):
            raise ValueError('local test record origin is missing')
        for name in ('stdout_sha256', 'stderr_sha256'):
            if not isinstance(check.get(name), str) or not HASH_RE.fullmatch(check[name]):
                raise ValueError('local test log digest is missing')
    print(f"local {args.component} tests verified for {commit}: {len(checks)} completed check(s), tested {tested}")


def write_record(args):
    output = Path(args.output).resolve()
    if output.exists():
        raise ValueError('evidence already exists; retain it and choose a new output')
    tested = revision(args.revision)
    objects = source_objects(tested, args.component)
    if args.action == 'run':
        if tested != revision('HEAD') or git('status', '--porcelain', '--untracked-files=no'):
            raise ValueError('run local tests from a clean checkout of the tested commit')
        command = args.command
        if command[:1] == ['--']:
            command = command[1:]
        if not command:
            raise ValueError('a local test command is required after --')
        logdir = output.with_suffix(output.suffix + '.logs')
        logdir.mkdir(mode=0o700)
        with (logdir / 'stdout').open('xb') as out, (logdir / 'stderr').open('xb') as err:
            result = subprocess.run(command, stdout=out, stderr=err)
        if tested != revision('HEAD') or git('status', '--porcelain', '--untracked-files=no'):
            raise ValueError('test command changed the tested checkout; logs retained without a passing record')
        stdout, stderr = (logdir / 'stdout').read_bytes(), (logdir / 'stderr').read_bytes()
        exit_code = result.returncode
        command_text = shlex.join(command)
        origin = 'local-run'
    else:
        stdout = Path(args.stdout).read_bytes()
        stderr = Path(args.stderr).read_bytes() if args.stderr else b''
        exit_code, command_text, origin = args.exit_code, args.command, 'completed-log-import'
    record = {'format': 1, 'component': args.component, 'source_revision': tested,
              'source_objects': objects, 'recorded_utc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
              'checks': [{'command': command_text, 'exit_code': exit_code, 'recorded_from': origin,
                          'stdout_sha256': digest(stdout), 'stderr_sha256': digest(stderr)}]}
    fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump(record, stream, indent=2)
        stream.write('\n')
        stream.flush()
        os.fsync(stream.fileno())
    print(f'{output}: local {args.component} test exit {exit_code}; logs preserved')
    return 0 if exit_code == 0 else 1


def main(argv):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    actions = parser.add_subparsers(dest='action', required=True)
    check = actions.add_parser('verify')
    check.add_argument('revision')
    check.add_argument('--component', choices=PATHS, default='full')
    check.add_argument('--evidence')
    for action in ('run', 'import'):
        sub = actions.add_parser(action)
        sub.add_argument('--component', choices=PATHS, required=True)
        sub.add_argument('--revision', default='HEAD')
        sub.add_argument('--output', required=True)
        if action == 'run':
            sub.add_argument('command', nargs=argparse.REMAINDER)
        else:
            sub.add_argument('--command', required=True)
            sub.add_argument('--exit-code', required=True, type=int)
            sub.add_argument('--stdout', required=True)
            sub.add_argument('--stderr')
    args = parser.parse_args(argv)
    try:
        if args.action == 'verify':
            verify(args)
            return 0
        return write_record(args)
    except (ValueError, OSError, KeyError, TypeError) as error:
        print(f'local-release-tests: {error}', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
