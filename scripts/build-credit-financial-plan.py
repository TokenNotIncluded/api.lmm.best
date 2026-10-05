#!/usr/bin/env python3
"""Build private transition intent and maintenance runner plan; never contact a host."""
import argparse
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import string
import sys

SEED_FORMAT = 'lmm-credit-financial-plan-seed-v1'
INTENT_FORMAT = 'lmm-credit-transition-intent-v1'
PLAN_FORMAT = 'lmm-credit-financial-maintenance-v1'
HEX = re.compile(r'^[0-9a-f]{64}$')
NAME = re.compile(r'^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$')
INVENTORY = ('name', 'ssh', 'hostname', 'deployment_tool', 'service', 'writers', 'guardian_unit')
STAGES = ('capture', 'prebridge', 'post')
OPERATIONS = ('guardian_start', 'capture', 'close', 'stop', 'prebridge_apply', 'prebridge_confirm',
              'prebridge_stop', 'post_apply', 'post_confirm', 'maintenance_release', 'guardian_inspect',
              'writer_inspect', 'publish_receipt', 'guardian_release', 'capture_status', 'prebridge_status',
              'post_status', 'post_retry', 'seal_prebridge', 'seal_post')


class InvalidSeed(ValueError):
    pass


def require(condition, message):
    if not condition:
        raise InvalidSeed(message)


def unique(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, 'duplicate JSON key')
        result[key] = value
    return result


def decode(content):
    return json.loads(content, object_pairs_hook=unique)


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=True).encode()


def digest(content):
    return hashlib.sha256(content).hexdigest()


def absolute(value):
    require(isinstance(value, str) and re.fullmatch(r'/[A-Za-z0-9._/-]+', value) and
            str(Path(value)) == value and '..' not in Path(value).parts and value != '/', 'unsafe absolute path')
    return Path(value)


def binding(value):
    require(isinstance(value, dict) and set(value) == {'path', 'sha256'} and HEX.fullmatch(value['sha256']),
            'artifact requires exact path and SHA-256')
    absolute(value['path'])
    return copy.deepcopy(value)


def read_private(file):
    file = absolute(str(file))
    info = file.lstat()
    require(stat.S_ISREG(info.st_mode) and info.st_nlink == 1 and stat.S_IMODE(info.st_mode) == 0o600 and
            info.st_uid == os.geteuid() and file.resolve() == file, 'input must be an owned private regular file')
    return file.read_bytes()


def read_bound(value):
    value = binding(value)
    file = absolute(value['path'])
    info = file.lstat()
    require(stat.S_ISREG(info.st_mode) and info.st_nlink == 1 and not info.st_mode & 0o022 and
            file.resolve() == file, 'unsafe local bound artifact')
    content = file.read_bytes()
    require(digest(content) == value['sha256'], 'local bound artifact changed')
    return content


def write_once(file, content):
    file = absolute(str(file))
    for parent in file.parents:
        info = parent.lstat()
        require(stat.S_ISDIR(info.st_mode) and not stat.S_ISLNK(info.st_mode) and not info.st_mode & 0o022,
                'output ancestors must exist and not be writable by others')
    fd = os.open(file, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
    os.fchmod(fd, 0o600)
    with os.fdopen(fd, 'wb') as output:
        output.write(content)
        output.flush()
        os.fsync(output.fileno())
    directory = os.open(file.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


def operation(argv, timeout=600):
    require(isinstance(argv, list) and argv and all(isinstance(item, str) and '\x00' not in item for item in argv),
            'invalid command argv')
    absolute(argv[0])
    require(type(timeout) is int and 1 <= timeout <= 3600, 'invalid command timeout')
    return {'argv': copy.deepcopy(argv), 'timeout_seconds': timeout}


def validate_substitutions(command, allowed, label):
    formatter = string.Formatter()
    for arg in command['argv']:
        for _, field, specifier, conversion in formatter.parse(arg):
            if field is not None:
                require(field in allowed and not specifier and conversion is None, 'unsupported runtime substitution in ' + label)


def validate_seed(seed):
    require(seed.get('format') == SEED_FORMAT and seed.get('confirmation') == 'api.lmm.best', 'unsupported seed target')
    require(NAME.fullmatch(seed.get('transition_id', '')) and
            re.fullmatch(r'[0-9a-f]{40}', seed.get('source_sha', '')), 'invalid transition or final source SHA')
    binding(seed['provider'])
    require(1 <= len(seed['nodes']) <= 16, 'complete node inventory required')
    names = []
    for node in seed['nodes']:
        for key in ('name', 'ssh', 'hostname', 'guardian_unit'):
            require(NAME.fullmatch(node[key]), 'invalid node identity')
        require(node['deployment_tool'] in ('native', 'systemd') and node['service'] == 'lmm-api.service' and
                node['writers'] == ['lmm-api.service'], 'unsupported writer inventory')
        names.append(node['name'])
    require(len(set(names)) == len(names), 'duplicate nodes')
    db = seed['database']
    require(set(db) == {'transport', 'owner_node', 'os_user', 'peer_role', 'owner_role', 'runtime_role',
                        'database', 'schema', 'port', 'socket_directory'}, 'exact database binding required')
    require(db['transport'] == 'local_peer' and db['owner_node'] in names and
            db['owner_role'] == db['runtime_role'] == 'lmm_api', 'unsupported database owner/runtime role')
    for key in ('os_user', 'peer_role', 'database', 'schema'):
        require(NAME.fullmatch(db[key]), 'invalid database peer identity')
    require(type(db['port']) is int and 1 <= db['port'] <= 65535, 'invalid database port')
    absolute(db['socket_directory'])
    return seed


def make_intent(seed):
    validate_seed(seed)
    # No handoff or prepare-config hashes: these are subsequently bound to the
    # intent by the normal owner. This first pass avoids a hash dependency cycle.
    return {'format': INTENT_FORMAT, 'transition_id': seed['transition_id'], 'source_sha': seed['source_sha'],
            'target_credits_per_usd': 500000, 'provider_sha256': seed['provider']['sha256'],
            'writer_nodes': [{key: copy.deepcopy(node[key]) for key in INVENTORY} for node in seed['nodes']],
            'database': copy.deepcopy(seed['database'])}


def native_command(node, stage, action, base=False):
    owner = node['owners'][stage]
    argv = [owner['operator']['path'], 'operator', 'production', action, '--workspace', owner['workspace'],
            '--maintenance-handoff', '{base_handoff_path}' if base else '{handoff_path}',
            '--maintenance-handoff-sha256', '{base_handoff_sha256}' if base else '{handoff_sha256}']
    if action == 'status':
        argv += ['--staged-plan', owner['staged_plan']['path'], '--staged-plan-sha256', owner['staged_plan']['sha256']]
    return argv


def systemd_command(node, stage, action, base=False):
    owner = node['owners'][stage]
    argv = ['/usr/bin/python3', node['helpers']['systemd_owner']['path'], action, '--release', Path(owner['workspace']).name,
            '--maintenance-handoff', '{base_handoff_path}' if base else '{handoff_path}',
            '--maintenance-handoff-sha256', '{base_handoff_sha256}' if base else '{handoff_sha256}', '--json']
    if action != 'status':
        argv += ['--confirm', 'api.lmm.best']
    if action == 'confirm':
        argv += ['--wait']
    if action == 'apply' and owner.get('migrate', False):
        argv += ['--migrate']
    return argv


def flag_value(argv, flag):
    require(argv.count(flag) == 1, 'native immutable argv missing or repeats ' + flag)
    index = argv.index(flag)
    require(index + 1 < len(argv) and not argv[index + 1].startswith('--'), 'native flag has no value: ' + flag)
    return argv[index + 1]


def native_override(node, key, stage, action):
    supplied = node.get('command_overrides', {}).get(key)
    require(supplied is not None, 'native requires reviewed normal-owner command_overrides.' + key)
    require(set(supplied) == {'argv', 'timeout_seconds'}, 'override must contain exact argv and timeout')
    result = operation(supplied['argv'], supplied['timeout_seconds'])
    argv = result['argv']
    require(argv[:4] == [node['owners'][stage]['operator']['path'], 'operator', 'production', action] and
            '--plan' not in argv and flag_value(argv, '--workspace') == node['owners'][stage]['workspace'],
            'native override must use the actual staged target owner, not controller state')
    for flag in ('--operator-user', '--go-package', '--go-package-sha256', '--go-rollback-package', '--go-rollback-sha256',
                 '--web-package', '--web-package-sha256', '--web-rollback-package', '--web-rollback-sha256',
                 '--probe-binary', '--probe-binary-sha256', '--operator-binary', '--operator-binary-sha256', '--expected-version'):
        value = flag_value(argv, flag)
        if flag.endswith('sha256'):
            require(HEX.fullmatch(value), 'invalid native immutable digest')
        elif flag.endswith('package') or flag.endswith('binary'):
            absolute(value)
    require(flag_value(argv, '--probe-binary-sha256') == node['_provider_sha256'], 'native probe is not the final provider')
    if stage == 'post':
        require(flag_value(argv, '--go-package-sha256') == flag_value(argv, '--go-rollback-sha256'),
                'post candidate and compatible bridge rollback must be the same package')
    require('--go-changed' in argv or '--go-changed=true' in argv, 'maintenance must install the Go candidate')
    require('--web-changed' not in argv and '--web-changed=true' not in argv, 'maintenance must preserve the active frontend')
    # Keep every reviewed immutable package/backup argument. Only the handoff
    # is refined by the formal seal-stopped operation after its writer stops.
    original = node['post_intent'] if stage == 'post' else node['handoff']
    for flag, token, actual in (('--maintenance-handoff', '{base_handoff_path}' if stage == 'capture' else '{handoff_path}', original['path']),
                                ('--maintenance-handoff-sha256', '{base_handoff_sha256}' if stage == 'capture' else '{handoff_sha256}', original['sha256'])):
        value = flag_value(argv, flag)
        require(value in (token, actual), 'override handoff must be its reviewed staging intent or formal refinement token')
        argv[argv.index(flag) + 1] = token
    return result


def node_commands(node):
    native = node['deployment_tool'] == 'native'
    command = native_command if native else systemd_command
    commands = {
        'guardian_start': operation(['/usr/bin/systemctl', 'start', node['guardian_unit']], 60),
        'guardian_release': operation(['/usr/bin/systemctl', 'stop', node['guardian_unit']], 60),
    }
    actions = {'capture': ('capture', 'maintenance-capture'), 'close': ('capture', 'maintenance-close'),
               'stop': ('capture', 'maintenance-stop'), 'prebridge_apply': ('prebridge', 'apply'),
               'prebridge_confirm': ('prebridge', 'confirm'), 'prebridge_stop': ('prebridge', 'maintenance-stop'),
               'post_apply': ('post', 'apply'), 'post_confirm': ('post', 'confirm'),
               'maintenance_release': ('post', 'maintenance-release')}
    for key, (stage, action) in actions.items():
        if native and key in ('capture', 'prebridge_apply', 'post_apply'):
            commands[key] = native_override(node, key, stage, action)
        else:
            commands[key] = operation(command(node, stage, action, base=stage == 'capture'))
    for key in ('stop', 'prebridge_stop'):
        commands[key]['argv'] += ['--all-admission-closed', '{all_closed_path}', '--all-admission-closed-sha256', '{all_closed_sha256}']
    commands['maintenance_release']['argv'] += ['--global-confirmation', '{global_confirmation_path}', '--global-confirmation-sha256', '{global_confirmation_sha256}']
    for stage in STAGES:
        commands[stage + '_status'] = operation(command(node, stage, 'status', base=stage == 'capture'), 120)
    if native:
        commands['post_retry'] = copy.deepcopy(commands['post_apply'])
        commands['post_retry']['argv'][3] = 'maintenance-retry'
    else:
        # systemd never emits the native MAINTENANCE_PREARM_FAILED phase.
        # This unused required slot is a read-only status, not an invented retry.
        commands['post_retry'] = operation(command(node, 'post', 'status'), 120)
    helper = ['/usr/bin/python3', node['helpers']['guardian']['path']]
    handoff = ['--handoff', '{handoff_path}', '--handoff-sha256', '{handoff_sha256}']
    commands['guardian_inspect'] = operation(helper + ['inspect'] + handoff, 30)
    commands['writer_inspect'] = operation(helper + ['inspect-writer'] + handoff, 30)
    for stage, source in (('prebridge', 'capture'), ('post', 'prebridge')):
        seal_handoff = handoff if stage == 'prebridge' else ['--handoff', node['post_intent']['path'], '--handoff-sha256', node['post_intent']['sha256']]
        commands['seal_' + stage] = operation(helper + ['seal-stopped'] + seal_handoff + ['--workspace', node['owners'][source]['workspace'],
            '--stage', stage, '--output', '{handoff_output_path}'], 60)
    commands['publish_receipt'] = operation(['/usr/bin/python3', node['helpers']['runner']['path'], '_publish-receipt',
        '--path', '{receipt_path}', '--sha256', '{receipt_sha256}'], 30)
    for key, value in node.get('command_overrides', {}).items():
        require(key in OPERATIONS, 'unknown command override')
        if native and key in ('capture', 'prebridge_apply', 'post_apply'):
            continue
        require(set(value) == {'argv', 'timeout_seconds'}, 'invalid override shape')
        replacement = operation(value['argv'], value['timeout_seconds'])
        if key == 'post_retry':
            require(replacement == commands[key], 'post retry must be the exact derived normal-owner command')
        elif not native and key in ('capture', 'prebridge_apply', 'post_apply'):
            stage, action = actions[key]
            require(replacement['argv'][:3] == ['/usr/bin/python3', node['helpers']['systemd_owner']['path'], action] and
                    flag_value(replacement['argv'], '--release') == Path(node['owners'][stage]['workspace']).name and
                    '--json' in replacement['argv'] and flag_value(replacement['argv'], '--confirm') == 'api.lmm.best',
                    'systemd override must use its actual normal owner and JSON target confirmation')
            require(('--migrate' in replacement['argv']) == bool(node['owners'][stage].get('migrate', False)) or action != 'apply',
                    'systemd apply migrate flag must match the staged selection')
            original = node['post_intent'] if stage == 'post' else node['handoff']
            for flag, token, actual in (('--maintenance-handoff', '{base_handoff_path}' if stage == 'capture' else '{handoff_path}', original['path']),
                                       ('--maintenance-handoff-sha256', '{base_handoff_sha256}' if stage == 'capture' else '{handoff_sha256}', original['sha256'])):
                require(flag_value(replacement['argv'], flag) in (token, actual), 'systemd override handoff differs from its real staging intent')
                replacement['argv'][replacement['argv'].index(flag) + 1] = token
            commands[key] = replacement
        else:
            commands[key] = replacement
    for key, value in commands.items():
        allowed = {'handoff_path', 'handoff_sha256', 'base_handoff_path', 'base_handoff_sha256'}
        if key in ('guardian_start', 'guardian_release'):
            allowed = set()
        allowed.update({'stop': {'all_closed_path', 'all_closed_sha256'}, 'prebridge_stop': {'all_closed_path', 'all_closed_sha256'},
                        'maintenance_release': {'global_confirmation_path', 'global_confirmation_sha256'},
                        'publish_receipt': {'receipt_path', 'receipt_sha256'},
                        'seal_prebridge': {'handoff_output_path'}, 'seal_post': {'handoff_output_path'}}.get(key, set()))
        validate_substitutions(value, allowed, key)
    require(set(commands) == set(OPERATIONS), 'incomplete normal-owner commands')
    return commands


def make_plan(seed, intent_path, intent_content):
    require(intent_content == canonical(make_intent(seed)), 'intent differs from current final seed inventory')
    plan = {'format': PLAN_FORMAT, 'confirmation': 'api.lmm.best', 'transition_id': seed['transition_id'],
            'source_sha': seed['source_sha'], 'transition_intent_sha256': digest(intent_content),
            'intent': {'path': str(absolute(str(intent_path))), 'sha256': digest(intent_content)}}
    for key in ('controller', 'provider', 'verifier', 'generator', 'fingerprint_generator'):
        plan[key] = binding(seed[key])
        read_bound(plan[key])
    plan['generator_helpers'] = [binding(value) for value in seed['generator_helpers']]
    for value in plan['generator_helpers']:
        read_bound(value)
    for key in ('database', 'clone', 'regression', 'backup_commands'):
        plan[key] = copy.deepcopy(seed[key])
    for key, value in plan['backup_commands'].items():
        require(set(value) == {'argv', 'timeout_seconds'}, 'backup command must contain exact argv and timeout')
        operation(value['argv'], value['timeout_seconds'])
        validate_substitutions(value, {'backup_path', 'backup_sha256'}, key)
    require(plan['regression']['source_sha'] == seed['source_sha'], 'regression must use the final source SHA')
    barrier_hash = digest(('lmm-credit-transition:' + seed['transition_id']).encode())
    plan['public_probes'] = [{'url': url, 'body_sha256': barrier_hash} for url in seed['public_probe_urls']]
    plan['nodes'] = []
    for source in seed['nodes']:
        node = copy.deepcopy(source)
        node['_provider_sha256'] = seed['provider']['sha256']
        absolute(node['receipt_directory'])
        for key in ('handoff', 'post_intent', 'prepare_config'):
            binding(node[key])
        require(set(node['owners']) == set(STAGES), 'three actual staged owners are required')
        owners = node['owners']
        require(len({owner['workspace'] for owner in owners.values()}) == 3, 'capture, prebridge and post must be distinct owner workspaces')
        for owner in owners.values():
            absolute(owner['workspace'])
            if node['deployment_tool'] == 'native':
                binding(owner['operator']); binding(owner['staged_plan'])
                require(owner['operator']['sha256'] == seed['provider']['sha256'], 'staged native operator must be final provider bytes')
                require(owner['staged_plan']['path'] == owner['workspace'] + '/staging/release-plan.json', 'not an actual native staged plan path')
            else:
                require(str(Path(owner['workspace']).parent) == '/var/lib/lmm-api-deploy-systemd', 'systemd owner workspace must match its formal root')
                require(type(owner.get('migrate', False)) is bool, 'immutable systemd migrate selection must be boolean')
        artifacts = []
        for value in node.get('artifacts', []) + [node['handoff'], node['post_intent'], node['prepare_config']] + list(node['helpers'].values()):
            value = binding(value)
            if value not in artifacts:
                artifacts.append(value)
        require(node['helpers']['runner']['sha256'] == seed['controller']['sha256'], 'remote receipt publisher must be the bound runner')
        if node['deployment_tool'] == 'native':
            for owner in owners.values():
                for key in ('operator', 'staged_plan'):
                    if owner[key] not in artifacts:
                        artifacts.append(binding(owner[key]))
        else:
            require('systemd_owner' in node['helpers'], 'systemd owner helper binding required')
        result = {key: copy.deepcopy(node[key]) for key in INVENTORY}
        result.update(handoff=binding(node['handoff']), post_intent=binding(node['post_intent']), prepare_config=binding(node['prepare_config']),
                      receipt_directory=node['receipt_directory'], artifacts=artifacts,
                      probes=[{'url': url, 'body_sha256': barrier_hash} for url in node['probe_urls']], commands=node_commands(node))
        if 'probe_resolve_address' in node:
            result['probe_resolve_address'] = node['probe_resolve_address']
        if 'cleanup' in node:
            result['cleanup'] = operation(node['cleanup']['argv'], node['cleanup']['timeout_seconds'])
            validate_substitutions(result['cleanup'], {'handoff_path', 'handoff_sha256', 'backup_sha256',
                'financial_backup_receipt_path', 'financial_backup_receipt_sha256'}, 'cleanup')
        plan['nodes'].append(result)
    # Use the bound runner's real validator, not a drifting parallel schema.
    # Importing this reviewed script does not execute its CLI or open a DB.
    sys.dont_write_bytecode = True
    spec = importlib.util.spec_from_file_location('credit_financial_plan_schema', plan['controller']['path'])
    runner = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(runner)
    runner.validate_plan(plan)
    require(set(runner.NODE_OPERATIONS) == set(OPERATIONS), 'runner operation schema changed; rebuild templates')
    return plan


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('intent', 'plan'))
    parser.add_argument('--seed', required=True)
    parser.add_argument('--intent', help='plan: existing private canonical intent produced by the first pass')
    parser.add_argument('--output', required=True)
    args = parser.parse_args(argv)
    try:
        seed = validate_seed(decode(read_private(args.seed)))
        read_bound(seed['provider'])
        if args.action == 'intent':
            require(args.intent is None, 'intent action does not accept --intent')
            value = make_intent(seed)
        else:
            require(args.intent is not None, 'plan action requires the already sealed intent')
            value = make_plan(seed, args.intent, read_private(args.intent))
        content = canonical(value)
        write_once(args.output, content)
        print(json.dumps({'path': str(absolute(args.output)), 'sha256': digest(content), 'format': value['format'],
                          'transition_id': seed['transition_id']}))
        return 0
    except (OSError, ValueError, KeyError, TypeError, AttributeError, RuntimeError) as error:
        print(json.dumps({'ok': False, 'error_type': type(error).__name__}), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
