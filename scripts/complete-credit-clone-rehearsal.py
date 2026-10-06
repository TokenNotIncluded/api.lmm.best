#!/usr/bin/env python3
"""Complete only the sealed 20261006 failed local clone's remaining rehearsal."""
import argparse
import fcntl
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import sys
import types
import urllib.parse

ORIGINAL_PATH = Path('/home/lightjunction/.cache/api-release-preparation-20261006/final/source/scripts/run-credit-financial-maintenance.py')
ORIGINAL_SHA = '8e5a894f3ca806a1c9f86641e3ac96d2b0de310acc251d5f30a5439b285c611b'
WORK_PATH = Path('/home/lightjunction/.cache/cf-20261006')
PLAN_PATH = WORK_PATH / 'financial-plan.bound.json'
PLAN_SHA = '095347319e48ce3e2400fb1031a611455ed1230b2ad80606e7e5cc7b702617ce'
STATE_SHA = '0361dc9fbfa5a537deb3bf16e474c0f1fc9bbd8c11bea427206cbcfe4b146353'
SEAL_SHA = '7d580e3c8bd65fd53521d0124b262b05b53e37288343fdbe3420f99f6e19e080'
BUSINESS_SHA = '655914ba1c7c743f3ff38893525004fa51131b82919b500f51e8464d9148b6fa'
BACKUP_SHA = '9dce3db049955862885718f856f8fe9ea9549140b273c744d848792f17a98377'
FINGERPRINT_SHA = 'a1ae1f32772beacab4af326c17b0fddb0be22fb72dd3cd3479278ae4304b936f'
SOURCE_SHA = '7af4bf9e56055a9a283b6545433e271a84b60026'
TRANSITION_ID = 'credit-financial-20261006'
INTENT_SHA = '60720d3b217167231ec10df4b95aab704366a05884c8b53e27dced40fb92bdcd'
PROVIDER_SHA = '19dd6ff9d514cfea8338ba0c37b9a62d8ed1e10ddf77c7ba0f41d69a4eacdec9'
PROBE_PATH = Path('/home/lightjunction/.cache/credit-financial-maintenance-20261006/lmm-api-clone-schema-unix-probe')
PROBE_SHA = '32e70f82e5fe5f132ca7022fa0184a7251f45ed4cb1cc82eb9a19cc316639e74'
CLONE = {'database': 'credit_rebase_clone_20261006', 'role': 'lmm_api', 'schema': 'lmm_prod_20260802', 'port': 25591}
IDENTITY = {'database': CLONE['database'], 'schema': CLONE['schema'], 'database_oid': '16385', 'schema_oid': '16386', 'system_identifier': '7693363761550653411'}
LOGS = {
    '000561-clone-original-before.log': (18024, FINGERPRINT_SHA),
    '000561-clone-original-before.tsv': (18024, FINGERPRINT_SHA),
    '000562-clone-before.log': (0, 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'),
    '000563-clone-financial-dispatch.log': (477, 'c7a7b26bfe7826af07079e26959086fbd2663cae52243bc7f02b5e374da6e3f1'),
    '000564-clone-after.log': (0, 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'),
    '000565-clone-original-after.log': (18024, FINGERPRINT_SHA),
    '000565-clone-original-after.tsv': (18024, FINGERPRINT_SHA),
    '000566-clone-identity.log': (222, '7fa622dbeb927eaa59b7e460a79992daa76c2b5c06ca87206ae7543df29228a8'),
    '000567-clone-candidate-schema-apply.log': (610, 'ad62190a6386f4666b894127ecf5ab456b963142b0f1d1b0188ad93fa1dd0e82'),
}


class CompletionFailed(RuntimeError):
    pass


def bound(file, expected):
    file = Path(file)
    if not isinstance(expected, str) or not re.fullmatch('[0-9a-f]{64}', expected) or not file.is_absolute() or file.resolve() != file:
        raise CompletionFailed('sealed-path-or-digest')
    before = file.lstat()
    if not stat.S_ISREG(before.st_mode) or before.st_nlink != 1 or before.st_mode & 0o022:
        raise CompletionFailed('sealed-file-type-or-permissions')
    with os.fdopen(os.open(file, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC), 'rb') as stream:
        opened = os.fstat(stream.fileno())
        data = stream.read()
        after = os.fstat(stream.fileno())
    identity = lambda info: (info.st_dev, info.st_ino, info.st_mode, info.st_nlink, info.st_uid, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
    if identity(before) != identity(opened) or identity(opened) != identity(after) or identity(after) != identity(file.lstat()) or hashlib.sha256(data).hexdigest() != expected:
        raise CompletionFailed('sealed-bytes-changed')
    return data


def load_original(path=ORIGINAL_PATH, expected=ORIGINAL_SHA):
    if expected != ORIGINAL_SHA:
        raise CompletionFailed('original-digest-fixed')
    raw = bound(path, ORIGINAL_SHA)
    module = types.ModuleType('_sealed_clone_tail_original')
    module.__file__ = str(path)
    module.__spec__ = importlib.util.spec_from_file_location(module.__name__, path)
    module.__sealed_sha256__ = ORIGINAL_SHA
    exec(compile(raw, str(path), 'exec'), module.__dict__)
    return module


def controller_type(original):
    if getattr(original, '__sealed_sha256__', None) != ORIGINAL_SHA:
        raise CompletionFailed('unsealed-original')

    class CloneTailController(original.Controller):
        def verify_candidate(self, label, mode='verify'):
            original.require((label, mode) in (('candidate-schema', 'apply'), ('candidate', 'verify'), ('retained-bridge', 'verify')), 'clone-tail-operation-only')
            original.require(self.plan['clone'] == CLONE and self.work == WORK_PATH and self.clone_identity() == IDENTITY, 'clone-tail-target-changed')
            bound(PROBE_PATH, PROBE_SHA)
            _, _, socket = self.clone_paths()
            cwd = self.work / 'clone-verify-cwd'
            cwd.mkdir(mode=0o700, exist_ok=True)
            info = cwd.lstat()
            original.require(stat.S_ISDIR(info.st_mode) and info.st_uid == os.geteuid() and not info.st_mode & 0o077 and cwd.resolve() == cwd and not (cwd / '.env').exists(), 'clone-verify-directory-unsafe')
            environment = self.local_environment()
            environment['SQL_DSN'] = 'postgresql://' + urllib.parse.quote(CLONE['role'], safe='') + '@/' + CLONE['database'] + '?' + urllib.parse.urlencode({'host': str(socket), 'port': CLONE['port'], 'sslmode': 'disable', 'search_path': CLONE['schema']})
            self.execute('clone-' + label + '-' + mode, {'argv': [str(PROBE_PATH), 'migrate', '--' + mode], 'timeout_seconds': 600}, cwd=str(cwd), env=environment)

    return CloneTailController


def precheck(original, controller):
    require = original.require
    require(controller.work == WORK_PATH and controller.plan_hash == PLAN_SHA, 'incident-work-or-plan')
    require(original.decode(bound(PLAN_PATH, PLAN_SHA)) == controller.plan, 'incident-plan-object-changed')
    state_raw = bound(controller.state_path, STATE_SHA)
    state = original.decode(state_raw)
    require(state == controller.state and state.get('phase') == 'REHEARSAL_FAILED' and type(state.get('sequence')) is int and state['sequence'] == 29 and state.get('plan_sha256') == PLAN_SHA, 'incident-state')
    require(state.get('transition_id') == TRANSITION_ID and state.get('transition_intent_sha256') == INTENT_SHA and state.get('business_plan_sha256') == BUSINESS_SHA and controller.plan['source_sha'] == SOURCE_SHA and controller.plan['provider']['sha256'] == PROVIDER_SHA, 'incident-identity')
    generation = {'pid': 2603003, 'started_at': '1791250839', 'data_directory': str(WORK_PATH / 'local-clone/data'), 'socket_directory': str(WORK_PATH / 'local-clone/socket'), 'port': CLONE['port']}
    require(controller.plan['clone'] == CLONE and state.get('clone_identity') == IDENTITY and state.get('clone_generation') == generation and state.get('clone_table_count') == 167, 'incident-clone-context')
    require(controller.clone_generation() == generation, 'incident-clone-generation')
    require(state['business_seal'] == {'path': str(WORK_PATH / 'business-artifacts/business-seal.json'), 'sha256': SEAL_SHA} and state['backup'] == {'path': str(WORK_PATH / 'final-full-database.dump'), 'sha256': BACKUP_SHA}, 'incident-seal-or-backup')
    seal = original.decode(bound(state['business_seal']['path'], SEAL_SHA))
    require(seal['business_plan_sha256'] == BUSINESS_SHA and seal['transition_id'] == TRANSITION_ID and seal['transition_intent_sha256'] == INTENT_SHA and seal['backup'] == state['backup'] and seal['backup_frozen_state_sha256'] == state['backup_frozen_state_sha256'], 'incident-business-seal')
    def bindings(value):
        if isinstance(value, dict):
            if set(value) == {'path', 'sha256'}:
                bound(value['path'], value['sha256'])
            else:
                for child in value.values():
                    bindings(child)
        elif isinstance(value, list):
            for child in value:
                bindings(child)
    bindings(seal)
    require(original.decode(original.read_bound(seal['clone_plan']))['target'] == IDENTITY and seal['original_fingerprint']['before_result']['sha256'] == FINGERPRINT_SHA, 'incident-clone-plan-or-fingerprint')
    require(len(original.decode(original.read_bound(seal['original_fingerprint']['inventory']))['tables']) == 167 and type(original.decode(original.read_bound(seal['original_fingerprint']['clone_after_receipt']))['table_count']) is int and original.decode(original.read_bound(seal['original_fingerprint']['clone_after_receipt']))['table_count'] == 167, 'incident-full-table-inventory')
    for name, (size, sha) in LOGS.items():
        require(len(bound(WORK_PATH / name, sha)) == size, 'incident-log-size')
    require(controller.operation_sequence == 567, 'incident-operation-sequence')
    require(b'converting NULL to string is unsupported' in bound(WORK_PATH / '000567-clone-candidate-schema-apply.log', LOGS['000567-clone-candidate-schema-apply.log'][1]), 'incident-failure-changed')
    bound(PROBE_PATH, PROBE_SHA)
    require(not (WORK_PATH / 'clone-tail-completion').exists(), 'completion-attempt-already-exists')
    return seal, state_raw


def complete(original, controller, *, execute=False, confirm=None):
    seal, state_raw = precheck(original, controller)
    if not execute:
        return {'ok': True, 'execute': False, 'phase': controller.state['phase'], 'sequence': 29, 'table_count': 167, 'probe_sha256': PROBE_SHA}
    original.require(confirm == 'api.lmm.best' and os.geteuid() != 0, 'explicit-unprivileged-clone-completion-required')
    attempt = WORK_PATH / 'clone-tail-completion'
    attempt.mkdir(mode=0o700)
    original.write_once(attempt / 'old-state.json', state_raw)
    for name in LOGS:
        original.write_once(attempt / name, bound(WORK_PATH / name, LOGS[name][1]))
    original.write_once(attempt / 'intent.json', original.encode({'format': 'lmm-credit-clone-tail-intent-v1', 'state_sha256': STATE_SHA, 'plan_sha256': PLAN_SHA, 'business_seal_sha256': SEAL_SHA, 'backup_sha256': BACKUP_SHA, 'original_sha256': ORIGINAL_SHA, 'probe_sha256': PROBE_SHA, 'wrapper_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), 'logs': LOGS, 'financial_sql_replay': False}))
    try:
        controller.frozen_gates()
        original.require(controller.clone_identity() == IDENTITY, 'clone-generation-changed')
        controller.clone_sql('clone-after-before-tail', original.read_bound(seal['clone_after_sql']))
        current = controller.fingerprint(seal, 'clone', 'after')
        original.require(current['path'] != str(WORK_PATH / '000565-clone-original-after.tsv'), 'fresh-current-fingerprint-required')
        original.write_once(attempt / 'current-clone-proof.json', original.encode({'after_fingerprint': current, 'financial_sql_replay': False}))
        controller.verify_candidate('candidate-schema', 'apply')
        controller.clone_sql('clone-after-candidate-schema', original.read_bound(seal['clone_after_sql']))
        controller.verify_candidate('candidate')
        controller.verify_candidate('retained-bridge')
        after = controller.fingerprint(seal, 'clone', 'after')
        regression = controller.plan['regression']
        source = controller.execute('regression-source', {'argv': ['/usr/bin/git', '-C', regression['source_directory'], 'rev-parse', 'HEAD'], 'timeout_seconds': 30})
        original.require(source.decode().strip() == regression['source_sha'] == SOURCE_SHA, 'regression-source-changed')
        dirty = controller.execute('regression-clean', {'argv': ['/usr/bin/git', '-C', regression['source_directory'], 'status', '--porcelain'], 'timeout_seconds': 30})
        original.require(not dirty.strip(), 'regression-source-dirty')
        controller.execute('offline-financial-regression', {'argv': ['/usr/bin/go', 'test', '-p', '1', *regression['packages'], '-run', regression['run'], '-count=1'], 'timeout_seconds': 3600}, cwd=regression['source_directory'], env=controller.local_environment())
        controller.frozen_gates()
        bound(controller.state_path, STATE_SHA)
        for name, (_, sha) in LOGS.items():
            bound(WORK_PATH / name, sha)
        original.require(after['path'] != str(WORK_PATH / '000565-clone-original-after.tsv'), 'fresh-after-fingerprint-required')
        controller.persist('REHEARSED', rehearsal_seal_sha256=SEAL_SHA, clone_before_fingerprint={'path': str(WORK_PATH / '000561-clone-original-before.tsv'), 'sha256': FINGERPRINT_SHA}, clone_after_fingerprint=after)
        original.write_once(attempt / 'complete.json', original.encode({'phase': controller.state['phase'], 'sequence': controller.state['sequence'], 'current_fingerprint': current, 'after_fingerprint': after}))
        return {'ok': True, 'phase': controller.state['phase'], 'sequence': controller.state['sequence']}
    except BaseException as error:
        original.write_once(attempt / 'failure.json', original.encode({'error_type': type(error).__name__, 'replay_forbidden': True, 'review_required': True}))
        raise


def main(argv=None):
    sys.dont_write_bytecode = True
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--execute', action='store_true')
    parser.add_argument('--confirm')
    args = parser.parse_args(argv)
    try:
        original = load_original()
        plan = original.validate_plan(original.decode(bound(PLAN_PATH, PLAN_SHA)))
        info = WORK_PATH.lstat()
        original.require(stat.S_ISDIR(info.st_mode) and info.st_uid == os.geteuid() and stat.S_IMODE(info.st_mode) == 0o700 and WORK_PATH.resolve() == WORK_PATH, 'controller-work-unsafe')
        lock_path = WORK_PATH / 'controller.lock'
        before = lock_path.lstat()
        original.require(stat.S_ISREG(before.st_mode) and before.st_nlink == 1 and not before.st_mode & 0o077, 'controller-lock-unsafe')
        with os.fdopen(os.open(lock_path, os.O_RDWR | os.O_NOFOLLOW | os.O_CLOEXEC), 'r+') as lock:
            info = os.fstat(lock.fileno())
            original.require((info.st_dev, info.st_ino) == (before.st_dev, before.st_ino), 'controller-lock-changed')
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            controller = controller_type(original)(plan, PLAN_SHA, WORK_PATH)
            print(json.dumps(complete(original, controller, execute=args.execute, confirm=args.confirm)))
        return 0
    except (OSError, ValueError, KeyError, CompletionFailed, RuntimeError) as error:
        print(json.dumps({'ok': False, 'error_type': type(error).__name__, 'inspect_before_retry': True}), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
