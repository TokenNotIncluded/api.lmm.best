#!/usr/bin/env python3
"""Authenticate explicit owner requests for fixed incident diagnosis or recovery."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time

REPOSITORY = 'TokenNotIncluded/api.lmm.best'
OWNER = 'LIghtJUNction'
REQUEST = '.github/server-ops-343-request.json'
SCRIPT = 'scripts/server-repairs/inspect-startup-343.sh'
RECOVERY_OPERATION = 'recover-red-packet-schema-343'
OPERATIONS = {'inspect-startup-343': SCRIPT,
              RECOVERY_OPERATION: 'scripts/server-repairs/recover-red-packet-schema-343.sh'}


def git(*args):
    return subprocess.check_output(['git', *args], stderr=subprocess.DEVNULL, timeout=10)


def read_object(sha, path, limit):
    entry = git('ls-tree', sha, '--', path).decode().split()
    if len(entry) != 4 or entry[0] not in ('100644', '100755') or entry[3] != path:
        raise ValueError('Expected a committed regular file')
    if not 1 <= int(git('cat-file', '-s', sha + ':' + path)) <= limit:
        raise ValueError('Committed file exceeds its limit')
    return git('show', sha + ':' + path)


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('Duplicate JSON field')
        result[key] = value
    return result


def validate_request(env, now=None):
    if (env.get('GITHUB_EVENT_NAME') != 'push' or env.get('GITHUB_REPOSITORY') != REPOSITORY
            or env.get('GITHUB_REF') != 'refs/heads/main'):
        raise ValueError('Owner requests require a push to this repository main')
    if any(env.get(key, '').casefold() != OWNER.casefold()
           for key in ('GITHUB_ACTOR', 'GITHUB_TRIGGERING_ACTOR')):
        raise ValueError('Both actual GitHub actors must be the owner; bots are not delegated')
    if env.get('GITHUB_RUN_ATTEMPT') != '1' or not re.fullmatch(r'[1-9][0-9]{0,19}', env.get('GITHUB_RUN_ID', '')):
        raise ValueError('A new run is required; no replay')
    sha = env.get('GITHUB_SHA', '')
    if not re.fullmatch(r'[0-9a-f]{40}', sha):
        raise ValueError('An immutable commit is required')
    event_path = Path(env['GITHUB_EVENT_PATH'])
    if event_path.stat().st_size > 1048576:
        raise ValueError('Oversized event')
    event = json.loads(event_path.read_text())
    if (event.get('after') != sha or event.get('ref') != 'refs/heads/main'
            or event.get('deleted') or event.get('forced')
            or event.get('sender', {}).get('login', '').casefold() != OWNER.casefold()
            or event.get('repository', {}).get('full_name') != REPOSITORY):
        raise ValueError('Event identity mismatch or forced update')
    parents = git('rev-list', '--parents', '-n', '1', sha).decode().split()
    if len(parents) != 2 or parents[1] != event.get('before'):
        raise ValueError('Request must be a single non-merge commit')
    if git('diff-tree', '--no-commit-id', '--name-only', '-r', sha).decode().splitlines() != [REQUEST]:
        raise ValueError('Request commit must change only the request, not executable code')
    committed_at = int(git('show', '-s', '--format=%ct', sha))
    age = (time.time() if now is None else now) - committed_at
    if not -60 <= age <= 1800:
        raise ValueError('Request expired or commit clock invalid')
    data = json.loads(read_object(sha, REQUEST, 4096), object_pairs_hook=unique_object)
    if not isinstance(data, dict) or set(data) != {'format', 'incident', 'operation', 'base_sha', 'script_sha256', 'confirm'}:
        raise ValueError('Unexpected request fields')
    if (type(data['format']) is not int or data['format'] != 1 or type(data['incident']) is not int or data['incident'] != 343
            or not isinstance(data['operation'], str) or data['operation'] not in OPERATIONS or data['confirm'] != 'api.lmm.best'
            or data['base_sha'] != parents[1]):
        raise ValueError('Only the explicitly confirmed fixed incident operations are delegated')
    payload = read_object(sha, OPERATIONS[data['operation']], 65536)
    if hashlib.sha256(payload).hexdigest() != data['script_sha256']:
        raise ValueError('Reviewed script digest mismatch')
    if b'\0' in payload or b'\r' in payload:
        raise ValueError('Invalid script encoding')
    payload.decode('utf-8')
    if subprocess.run(['bash', '--noprofile', '--norc', '-n'], input=payload,
                      stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10).returncode:
        raise ValueError('Script syntax rejected')
    return payload


def main():
    try:
        env = dict(os.environ)
        payload = validate_request(env)
        operation = json.loads(read_object(env['GITHUB_SHA'], REQUEST, 4096))['operation']
        if sys.argv[1:] == ['--validate-only']:
            print('Validated owner-commit operation=' + operation + '; script_sha256=' + hashlib.sha256(payload).hexdigest())
            if env.get('GITHUB_OUTPUT'):
                with open(env['GITHUB_OUTPUT'], 'a', encoding='utf-8') as output:
                    output.write('operation=' + operation + '\n')
            return 0
        if sys.argv[1:]:
            raise ValueError('Unknown argument')
        spec = importlib.util.spec_from_file_location('manual_ops', Path(__file__).with_name('server-ops-transport.py'))
        ops = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(ops)
        if operation == RECOVERY_OPERATION:
            helper_spec = importlib.util.spec_from_file_location('helper_payload', Path(__file__).with_name('server-ops-helper-payload.py'))
            helper = importlib.util.module_from_spec(helper_spec)
            helper_spec.loader.exec_module(helper)
            payload = helper.prepare_helper_payload(payload, Path(env['RUNNER_TEMP']) / 'lmm-incident343', env['GITHUB_SHA'])
        env.update(OPS_OPERATION='repair', OPS_REASON='Owner-authorized incident #343: ' + operation,
                   OPS_REPAIR_SCRIPT=OPERATIONS[operation], OPS_CONFIRM='api.lmm.best',
                   OPS_TIMEOUT='600' if operation == RECOVERY_OPERATION else '180')
        result = ops.execute(env, payload)
        public_ok = ops.public_health()
        print('public_status_success=' + str(public_ok).lower())
        return result if result else (0 if public_ok else 1)
    except (ValueError, OSError, KeyError, subprocess.SubprocessError) as error:
        print('Owner request stopped: ' + str(error) if isinstance(error, ValueError)
              else 'Owner request stopped: ' + type(error).__name__)
        return 1


if __name__ == '__main__':
    sys.exit(main())
