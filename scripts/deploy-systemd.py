#!/usr/bin/env python3
"""Deploy standalone LMM artifacts on systemd Linux, without a package manager.

Run on the target as root. Use doctor, upgrade, then explicit confirm.
The granular stage/apply/status/rollback commands remain available.
--migrate requires PostgreSQL backup tools and creates a verified backup
after the old writer stops, before applying migrations.
"""
import argparse
import importlib.util
import contextlib
import fcntl
import hashlib
import grp
import json
import os
from pathlib import Path
import re
import shutil
import shlex
import stat
import subprocess
import sys
import time
import math
import socket
import tarfile
import urllib.request
import urllib.error
import urllib.parse

ROOT = Path('/var/lib/lmm-api-deploy-systemd')
BINARY = Path('/usr/bin/lmm-api-go')
ENTRY = Path('/usr/bin/lmm-api')
FRONTEND = Path('/srv/lmm-api-frontend')
SERVICE = 'lmm-api.service'
ENVIRONMENT = Path('/etc/lmm-api-go/lmm-api-go.env')
NATIVE_TRANSACTION_LEASE = Path('/var/lib/lmm-api-go-deploy/transaction.lock')
RELEASE_PATTERN = r'[A-Za-z0-9][A-Za-z0-9._-]{0,70}'
PHASES = {'STAGED', 'MUTATION_PENDING', 'AWAITING_CONFIRMATION', 'MAINTENANCE_CONFIRMED', 'CONFIRMED', 'ROLLED_BACK', 'ROLLBACK_REQUIRED', 'CAPTURED', 'ADMISSION_CLOSED', 'FROZEN'}
BASE_TOOLS = ('systemctl', 'systemd-run', 'journalctl', 'nginx')
BACKUP_TOOLS = ('psql', 'pg_dump', 'pg_restore')
NGINX_LOCATIONS = Path('/etc/nginx/lmm-api-locations.conf')
PREPARE_DROP_IN = Path('/etc/systemd/system/lmm-api.service.d/90-credit-transition-prepare.conf')
_guardian_spec = importlib.util.spec_from_file_location('maintenance_deploy_guardian', Path(__file__).with_name('maintenance-deploy-guardian.py'))
guardian = importlib.util.module_from_spec(_guardian_spec)
_guardian_spec.loader.exec_module(guardian)


def run(*args, log=None):
    result = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if log:
        log.write_bytes(result.stdout)
        log.chmod(0o600)
    if result.returncode:
        raise RuntimeError(f'{args[0]} failed ({result.returncode}); see {log or "service journal"}')
    return result.stdout.decode().strip()


def digest(path):
    with path.open('rb') as source:
        return hashlib.file_digest(source, 'sha256').hexdigest()


def tree_digest(root):
    result = hashlib.sha256()
    for path in sorted(root.rglob('*')):
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise RuntimeError('frontend contains a non-regular entry')
        if path.is_file():
            result.update(str(path.relative_to(root)).encode() + b'\0')
            result.update(digest(path).encode() + b'\0')
    return result.hexdigest()


def save(work, state):
    if state.get('phase') in ('CONFIRMED', 'ROLLED_BACK'):
        state.setdefault('terminal_at', time.time())
    pending = work / 'state.next'
    with pending.open('w') as output:
        json.dump(state, output, indent=2)
        output.flush()
        os.fsync(output.fileno())
    pending.replace(work / 'state.json')
    fd = os.open(work, os.O_DIRECTORY)
    os.fsync(fd)
    os.close(fd)


def property_value(name):
    return run('systemctl', 'show', SERVICE, '-p', name, '--value')


def install(source, target):
    pending = target.with_name(target.name + '.deploy-next')
    if pending.exists() or pending.is_symlink():
        raise RuntimeError(f'unexpected pending file: {pending}')
    with source.open('rb') as src, pending.open('xb') as dst:
        shutil.copyfileobj(src, dst)
        os.fchmod(dst.fileno(), 0o755)
        os.fsync(dst.fileno())
    pending.replace(target)


def check_layout():
    if not ENTRY.is_symlink() or os.readlink(ENTRY) != 'lmm-api-go':
        raise RuntimeError('expected /usr/bin/lmm-api -> lmm-api-go')
    if BINARY.is_symlink() or not BINARY.is_file():
        raise RuntimeError('expected a regular Go provider')
    if not Path('/run/systemd/system').is_dir():
        raise RuntimeError('systemd is required')
    # Managed provider packages must use their package transaction instead.
    for command in (['dpkg-query', '-S', str(BINARY)], ['pacman', '-Qqo', str(BINARY)], ['rpm', '-qf', str(BINARY)]):
        if shutil.which(command[0]) and subprocess.run(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            raise RuntimeError('provider is package-owned; use the package deployment path')


def service_environment_files():
    # Preserve drop-in order: cluster settings may override database/cache hosts.
    value = property_value('EnvironmentFiles')
    files = []
    for line in value.splitlines():
        match = re.fullmatch(r'(/[^\s\\"\']+) \(ignore_errors=(yes|no)\)', line)
        if not match:
            raise RuntimeError('unsupported service EnvironmentFiles; schema verification refused')
        path, optional = match.groups()
        if str(Path(path)) != path or '..' in Path(path).parts:
            raise RuntimeError('service environment file path must be canonical')
        files.append(('-' if optional == 'yes' else '') + path)
    if not files:
        raise RuntimeError('service has no EnvironmentFiles; schema verification refused')
    return files


def verify(work, label, mode='verify', maintenance=None):
    args = ['systemd-run', '--quiet', '--wait', '--collect', '--pipe',
            '--unit=lmm-schema-' + work.name + '-' + label,
            '-p', 'Type=oneshot', '-p', 'TimeoutStartSec=180',
            *[arg for path in service_environment_files() for arg in ('-p', 'EnvironmentFile=' + path)],
            '-p', 'Environment=LMM_DB_MIGRATION_MODE=' + mode,
            '-p', 'MemoryMax=384M', '-p', 'NoNewPrivileges=yes',
            '-p', 'ProtectSystem=strict', '-p', 'PrivateTmp=yes',
            '-p', 'WorkingDirectory=' + str(work), '-p', 'ReadWritePaths=' + str(work),
            str(work / 'lmm-api'), 'migrate', '--' + mode]
    if maintenance and maintenance['stage'] == 'prebridge':
        args[-3:-3] = ['-p', 'Environment=LMM_CREDIT_TRANSITION_PLAN=' + maintenance['prepare_config_path'], '-p', 'Environment=LMM_CREDIT_TRANSITION_SHA256=' + maintenance['prepare_config_sha256']]
    run(*args, log=work / ('verify-' + label + '.log'))


def database_environment():
    pid = property_value('MainPID')
    if pid == '0':
        raise RuntimeError('running service is required to identify database configuration')
    entries = Path('/proc', pid, 'environ').read_bytes().split(b'\0')
    env = dict(item.split(b'=', 1) for item in entries if b'=' in item)
    return database_environment_from_values(env)


def database_environment_from_values(env):
    """Parse an existing process/oneshot environment without exposing credentials."""
    dsn = env.get(b'SQL_DSN', b'').decode()
    if not dsn.startswith(('postgres://', 'postgresql://')) or env.get(b'LOG_SQL_DSN'):
        raise RuntimeError('migration currently requires PostgreSQL without a separate log database')
    if any(not shutil.which(tool) for tool in BACKUP_TOOLS):
        raise RuntimeError('migration requires psql, pg_dump and pg_restore')
    url = urllib.parse.urlsplit(dsn)
    result = dict(os.environ, PGHOST=url.hostname or '', PGPORT=str(url.port or 5432),
                  PGUSER=urllib.parse.unquote(url.username or ''),
                  PGPASSWORD=urllib.parse.unquote(url.password or ''),
                  PGDATABASE=urllib.parse.unquote(url.path.lstrip('/')))
    if env.get(b'PGOPTIONS'):
        result['PGOPTIONS'] = env[b'PGOPTIONS'].decode()
    options = {'sslmode': 'PGSSLMODE', 'sslrootcert': 'PGSSLROOTCERT',
               'sslcert': 'PGSSLCERT', 'sslkey': 'PGSSLKEY',
               'connect_timeout': 'PGCONNECT_TIMEOUT', 'options': 'PGOPTIONS',
               'application_name': 'PGAPPNAME'}
    for key, value in urllib.parse.parse_qsl(url.query):
        if key in options:
            result[options[key]] = value
        else:
            raise RuntimeError('unsupported PostgreSQL backup connection option: ' + key)
    return result


def read_environment_file(path):
    values = {}
    for line in Path(path).read_text().splitlines():
        if not line.strip() or line.lstrip().startswith('#'):
            continue
        fields = shlex.split(line)
        if len(fields) != 1 or '=' not in fields[0]:
            raise RuntimeError('sealed environment has an unsupported assignment')
        key, value = fields[0].split('=', 1)
        if key.encode() in values:
            raise RuntimeError('sealed environment contains duplicate assignments')
        values[key.encode()] = value.encode()
    return values


def backup(work, env, schema_only=False, exclude=(), all_schemas=False):
    path = work / ('preflight-schema.sql' if schema_only else 'database.dump')
    command = ['pg_dump', '--no-password', '--file', str(path)]
    schema = subprocess.run(['psql', '-XAtc', 'SELECT current_schema()'], env=env, capture_output=True, check=True).stdout.decode().strip()
    if not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*', schema):
        raise RuntimeError('unsupported database schema name')
    if not all_schemas:
        command += ['--schema', schema]
    for table in exclude:
        if not re.fullmatch(re.escape(schema) + r'\.[A-Za-z_][A-Za-z0-9_]*', table):
            raise RuntimeError('backup exclusion must name an exact table in the active schema')
        command += ['--exclude-table', table]
    command += ['--schema-only'] if schema_only else ['--format=custom']
    result = subprocess.run(command, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (work / 'backup.log').write_bytes(result.stdout)
    if result.returncode:
        raise RuntimeError('database backup failed; see private backup.log')
    path.chmod(0o600)
    if not schema_only:
        run('pg_restore', '--list', str(path), log=work / 'backup-contents.log')
        return digest(path)


def health_json(route, base='http://127.0.0.1:3000', token=None):
    headers = {'Authorization': 'Bearer ' + token} if token else {}
    with urllib.request.urlopen(urllib.request.Request(base + route, headers=headers), timeout=10) as response:
        return json.load(response)


def healthy_prepare(version, maintenance):
    for route in ('/api/livez', '/api/status'):
        body = health_json(route)
        binding = body.get('data', {}).get('credit_transition', {})
        expected = {'format': 'lmm-credit-transition-prepare-v1',
                    'transition_id': maintenance['transition_id'],
                    'transition_intent_sha256': maintenance['transition_intent_sha256'],
                    'prepare_config_sha256': maintenance['prepare_config_sha256'],
                    'provider_sha256': maintenance['provider_sha256'],
                    'target_credits_per_usd': 500000}
        if body.get('success') is not True or body.get('maintenance') is not True or body.get('business_enabled') is not False or body.get('data', {}).get('version') != version or any(binding.get(key) != value for key, value in expected.items()) or (route == '/api/status' and body.get('ready') is not True) or (route == '/api/livez' and body.get('live') is not True):
            raise RuntimeError('maintenance health differs from the frozen prepare binding')


def healthy(version, maintenance=None):
    if maintenance and maintenance['stage'] == 'prebridge':
        healthy_prepare(version, maintenance)
        return
    for route in ('/api/livez', '/api/status'):
        body = health_json(route)
        if body.get('success') is not True or body.get('maintenance') is True or body.get('business_enabled') is False:
            raise RuntimeError('normal health response is not business-ready')
        if route == '/api/status' and (body.get('ready') is not True or body.get('data', {}).get('version') != version):
            raise RuntimeError('running version/readiness does not match candidate')
        if route == '/api/livez' and body.get('live') is not True:
            raise RuntimeError('normal liveness response is not live')
    if maintenance:
        token = guardian.bound_file(maintenance['probe_token_path'], maintenance['probe_token_sha256']).decode().strip()
        if not token or not isinstance(health_json('/v1/models', token=token).get('data'), list):
            raise RuntimeError('strict local authenticated business probe failed')


def maintenance_admission(maintenance):
    expected = ('lmm-credit-transition:' + maintenance['transition_id']).encode()
    for route, method in (('/api/status', 'GET'), ('/v1/models', 'GET'), ('/api/user/self', 'GET'), ('/v1/chat/completions', 'POST')):
        request = urllib.request.Request(maintenance['public_base_url'] + route, data=b'{}' if method == 'POST' else None, method=method)
        try:
            response = urllib.request.urlopen(request, timeout=10)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            if response.status != 503 or response.read() != expected:
                raise RuntimeError('public ingress is not the bound maintenance 503 barrier')


def immutable_write(path, body, mode=0o644):
    pending = path.with_name(path.name + '.maintenance-next')
    if pending.exists() or pending.is_symlink():
        raise RuntimeError('unexpected maintenance pending file')
    with pending.open('xb') as output:
        os.fchmod(output.fileno(), mode)
        output.write(body)
        output.flush()
        os.fsync(output.fileno())
    pending.replace(path)
    directory = os.open(path.parent, os.O_DIRECTORY | os.O_CLOEXEC)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


def maintenance_barrier(maintenance, original):
    if original.count(b'location @lmm_api_backend {') != 1 or b'lmm-credit-transition:' in original or b'lmm-billing-drain:' in original:
        raise RuntimeError('unrecognized LMM-only ingress locations')
    return ('return 503 \'lmm-credit-transition:' + maintenance['transition_id'] + '\';\n').encode() + original


def close_maintenance_admission(work, state, maintenance):
    evidence = work / 'previous-nginx-locations'
    if not evidence.exists():
        original = NGINX_LOCATIONS.read_bytes()
        immutable_write(evidence, original, 0o600)
        state['ingress_original_sha256'] = digest(evidence)
        save(work, state)
    original = guardian.bound_file(evidence, state['ingress_original_sha256'])
    barrier = maintenance_barrier(maintenance, original)
    if NGINX_LOCATIONS.read_bytes() not in (original, barrier):
        raise RuntimeError('maintenance ingress changed outside the owner transaction')
    immutable_write(NGINX_LOCATIONS, barrier)
    run('nginx', '-t', log=work / 'maintenance-nginx.log')
    run('systemctl', 'reload', 'nginx', log=work / 'maintenance-reload.log')
    maintenance_admission(maintenance)
    state['maintenance_admission_closed'] = True
    save(work, state)


def reopen_maintenance_admission(work, state, maintenance):
    original = guardian.bound_file(work / 'previous-nginx-locations', state['ingress_original_sha256'])
    if NGINX_LOCATIONS.read_bytes() != maintenance_barrier(maintenance, original):
        raise RuntimeError('bound maintenance barrier changed before release')
    immutable_write(NGINX_LOCATIONS, original)
    run('nginx', '-t', log=work / 'maintenance-release-nginx.log')
    run('systemctl', 'reload', 'nginx', log=work / 'maintenance-release-reload.log')


def configure_maintenance_service(maintenance, rollback=False):
    if maintenance['stage'] == 'prebridge' and not rollback:
        validate_prepare_service_reader(maintenance)
        PREPARE_DROP_IN.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
        for value in (maintenance['prepare_config_path'], maintenance['prepare_config_sha256']):
            if any(character in value for character in '\r\n"%\\ '):
                raise RuntimeError('unsafe systemd prepare binding syntax')
        immutable_write(PREPARE_DROP_IN, ('[Service]\nSupplementaryGroups=lmm-credit-transition\nEnvironment=LMM_CREDIT_TRANSITION_PLAN=' + maintenance['prepare_config_path'] + '\nEnvironment=LMM_CREDIT_TRANSITION_SHA256=' + maintenance['prepare_config_sha256'] + '\n').encode())
    elif PREPARE_DROP_IN.exists():
        if PREPARE_DROP_IN.is_symlink() or ('LMM_CREDIT_TRANSITION_SHA256=' + maintenance['prepare_config_sha256'] + '\n').encode() not in PREPARE_DROP_IN.read_bytes():
            raise RuntimeError('prepare service drop-in differs from frozen binding')
        PREPARE_DROP_IN.unlink()
    run('systemctl', 'daemon-reload')


def validate_prepare_service_reader(maintenance):
    try:
        gid = grp.getgrnam('lmm-credit-transition').gr_gid
    except KeyError:
        raise RuntimeError('provision the static lmm-credit-transition service reader group')
    path = Path(maintenance['prepare_config_path'])
    info = path.lstat()
    if gid == 0 or info.st_uid != 0 or info.st_gid != gid or info.st_mode & 0o777 != 0o640:
        raise RuntimeError('prepare plan requires root:lmm-credit-transition mode 0640')
    for parent in path.parents:
        info = parent.lstat()
        if parent.is_symlink() or not parent.is_dir() or info.st_mode & 0o022 or (not info.st_mode & 0o001 and (info.st_gid != gid or not info.st_mode & 0o010)):
            raise RuntimeError('prepare service reader cannot traverse a sealed plan directory')


def persist_capture(work, state, maintenance):
    receipt = {'format': 'lmm-credit-maintenance-capture-v1',
               'phase': state['phase'], 'transition_id': maintenance['transition_id'],
               'transition_intent_sha256': maintenance['transition_intent_sha256'],
               'provider_sha256': state['previous_sha256'], 'version': state['previous_version'],
               'pid': state['captured_pid'], 'invocation_id': state['captured_invocation_id'],
               'archived_environment_path': str(work / 'previous.env'),
               'archived_environment_sha256': digest(work / 'previous.env'),
               'process_environment_path': state.get('process_environment_path'),
               'process_environment_sha256': state.get('process_environment_sha256'),
               'frontend_target': 'releases/' + state['previous_frontend'],
               'frontend_sha256': state['frontend_sha256'],
               'was_maintenance_confirmed': state.get('was_maintenance_confirmed', False)}
    if state['phase'] == 'FROZEN':
        receipt.update(shutdown_journal_path=str(work / 'shutdown.log'), shutdown_journal_sha256=state['shutdown_journal_sha256'])
    destination = work / ('maintenance-capture.' + state['phase'] + '.json')
    immutable_write(destination, json.dumps(receipt, indent=2).encode() + b'\n', 0o600)
    state.update(capture_receipt_path=str(destination), capture_receipt_sha256=digest(destination),
                 maintenance_stage=maintenance['stage'], transition_id=maintenance['transition_id'],
                 transition_intent_sha256=maintenance['transition_intent_sha256'], provider_sha256=maintenance['provider_sha256'])
    save(work, state)


def verify_stopped_maintenance(maintenance):
    stopped = maintenance.get('stopped_writer', {})
    if not stopped or property_value('MainPID') != '0' or property_value('ExecMainPID') != str(stopped['pid']) or property_value('InvocationID') != stopped['invocation_id'] or property_value('Result') != 'success' or property_value('ExecMainCode') != '1' or property_value('ExecMainStatus') != '0' or property_value('ActiveState') != 'inactive' or property_value('SubState') != 'dead' or property_value('ControlGroup') or Path('/proc', str(stopped['pid'])).exists():
        raise RuntimeError('sealed maintenance writer is not verifiably stopped')
    journal = guardian.bound_file(stopped['shutdown_journal_path'], stopped['shutdown_journal_sha256'])
    if maintenance['stage'] == 'post':
        if b'credit_transition_prepare shutdown_complete=true business_enabled=false' not in journal or b'server exited' not in journal:
            raise RuntimeError('real prepare shutdown evidence is missing')
    else:
        reports = re.findall(rb'refund_tasks execution_complete=true accepted=(\d+) finished=(\d+) active=0 failed=0', journal)
        if not reports or reports[-1][0] != reports[-1][1] or b'server exited' not in journal:
            raise RuntimeError('ordinary owner shutdown evidence is missing')
    environment = guardian.bound_file(maintenance['archived_environment_path'], maintenance['archived_environment_sha256'])
    captured = json.loads(guardian.bound_file(maintenance['capture_receipt_path'], maintenance['capture_receipt_sha256']))
    if captured.get('format') != 'lmm-credit-maintenance-capture-v1' or captured.get('phase') != 'FROZEN' or any(captured.get(key) != maintenance[key] for key in ('transition_id', 'transition_intent_sha256')) or captured.get('pid') != stopped['pid'] or captured.get('invocation_id') != stopped['invocation_id'] or captured.get('archived_environment_sha256') != maintenance['archived_environment_sha256'] or captured.get('shutdown_journal_sha256') != stopped['shutdown_journal_sha256']:
        raise RuntimeError('stopped handoff differs from the normal owner FROZEN capture receipt')
    if environment != ENVIRONMENT.read_bytes():
        raise RuntimeError('sealed environment differs from current rollback configuration')
    values = {}
    for line in environment.decode().splitlines():
        if not line.strip() or line.lstrip().startswith('#'):
            continue
        fields = shlex.split(line)
        if len(fields) != 1 or '=' not in fields[0]:
            raise RuntimeError('sealed environment has an unsupported assignment')
        key, value = fields[0].split('=', 1)
        values[key.encode()] = value.encode()
    return database_environment_from_values(values)


def verify_maintenance_database(maintenance, env, post):
    prepared = json.loads(guardian.bound_file(maintenance['prepare_config_path'], maintenance['prepare_config_sha256']))
    query = "SELECT json_build_object('database',json_build_object('system_identifier',(SELECT system_identifier::text FROM pg_control_system()),'database',current_database(),'database_oid',(SELECT oid::bigint FROM pg_database WHERE datname=current_database()),'schema',current_schema(),'server_version_num',current_setting('server_version_num')::int,'database_user',current_user),'options',(SELECT json_object_agg(key,value) FROM options WHERE key IN ('CreditsPerUSD','LegacyPricingQuotaPerUnit','QuotaPerUnit','PublicCreditsPerUSD','USDExchangeRate')))"
    result = subprocess.run(['psql', '-X', '-v', 'ON_ERROR_STOP=1', '--no-align', '--tuples-only', '--command', query], env=env, capture_output=True)
    if result.returncode:
        raise RuntimeError('maintenance database identity/options verification failed')
    current = json.loads(result.stdout)
    expected = dict(prepared['options'])
    if post:
        expected.update({key: '500000' for key in expected if key != 'USDExchangeRate'})
    if current.get('database') != prepared['database'] or current.get('options') != expected:
        raise RuntimeError('maintenance database identity or exact five-option snapshot changed')


@contextlib.contextmanager
def deployment_lock(maintenance=None, digest_value=None, all_locks=False):
    if maintenance:
        if all_locks:
            descriptors, lease, receipt = guardian.adopt_all(maintenance, digest_value)
        else:
            descriptor, lease, receipt = guardian.adopt(maintenance, digest_value, ROOT / 'lock', with_receipt=True)
            descriptors = [descriptor]
        try:
            yield receipt
        finally:
            for descriptor in descriptors:
                os.close(descriptor)  # Close only: LOCK_UN would also unlock guardian.
            lease.close()
    else:
        if (ROOT / 'lock').is_symlink():
            raise RuntimeError('deployment lock may not be a symlink')
        with (ROOT / 'lock').open('a') as lock:
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raise RuntimeError('another deployment operation holds the lock; inspect status') from None
            yield


def stop(work, maintenance=None):
    invocation = property_value('InvocationID')
    prepare = maintenance and maintenance['stage'] == 'prebridge' and digest(BINARY) == maintenance['provider_sha256']
    if prepare:
        healthy_prepare(run(str(ENTRY), 'version'), maintenance)
    run('systemctl', 'stop', SERVICE, log=work / 'stop.log')
    if property_value('MainPID') != '0' or property_value('Result') != 'success' or property_value('ExecMainStatus') != '0':
        raise RuntimeError('old writer did not exit cleanly; activation refused')
    journal = run('journalctl', '--no-pager', '-o', 'cat', '_SYSTEMD_INVOCATION_ID=' + invocation)
    (work / 'shutdown.log').write_text(journal)
    if prepare:
        if 'credit_transition_prepare shutdown_complete=true business_enabled=false' not in journal or 'server exited' not in journal:
            raise RuntimeError('bound prepare shutdown evidence is missing')
        return
    reports = re.findall(r'refund_tasks execution_complete=true accepted=(\d+) finished=(\d+) active=0 failed=0', journal)
    if not reports or reports[-1][0] != reports[-1][1] or 'server exited' not in journal:
        raise RuntimeError('graceful shutdown/refund completion evidence is missing')


def check_tools(migrate=False):
    tools = BASE_TOOLS + (BACKUP_TOOLS if migrate else ())
    missing = [tool for tool in tools if not shutil.which(tool)]
    if missing:
        raise RuntimeError('missing required commands: ' + ', '.join(missing))


def read_state(work, allow_incomplete=False):
    if work.is_symlink() or not work.is_dir():
        raise RuntimeError(f'expected a transaction directory: {work}')
    path = work / 'state.json'
    if path.is_symlink():
        raise RuntimeError('transaction state may not be a symlink')
    if allow_incomplete and not path.exists():
        return {'release': work.name, 'phase': 'PREPARATION_INCOMPLETE'}
    state = json.loads(path.read_text())
    if not isinstance(state, dict) or state.get('release') != work.name or state.get('phase') not in PHASES:
        raise RuntimeError(f'invalid transaction state: {path}')
    return state


def read_status(release=None, handoff_path=None, handoff_sha256=None):
    # Do not create ROOT, a lock or a transaction while inspecting the host.
    if ROOT.is_symlink():
        raise RuntimeError('deployment root may not be a symlink')
    if release:
        return maintenance_dispatch_status(ROOT / release, read_state(ROOT / release, allow_incomplete=True),
                                           handoff_path=handoff_path, handoff_sha256=handoff_sha256)
    states = []
    if ROOT.exists():
        for work in sorted(ROOT.iterdir()):
            if work.is_symlink():
                raise RuntimeError(f'unexpected symlink in deployment root: {work}')
            if work.is_dir():
                if not re.fullmatch(RELEASE_PATTERN, work.name):
                    raise RuntimeError(f'invalid transaction directory: {work}')
                states.append(maintenance_dispatch_status(work, read_state(work, allow_incomplete=True)))
    return {'deployments': states}


POST_FREEZING_FIELDS = {'stopped_writer', 'previous_deployment_id', 'archived_environment_path',
                        'archived_environment_sha256', 'capture_receipt_path', 'capture_receipt_sha256'}


def validate_post_staging_intent(maintenance):
    if maintenance.get('stage') == 'post' and not maintenance.get('stopped_writer') and any(key in maintenance for key in POST_FREEZING_FIELDS):
        raise RuntimeError('unstopped post staging intent must contain no freezing evidence fields')


def post_staging_refinement(base_binding, path, expected):
    base = json.loads(guardian.bound_file(base_binding['path'], base_binding['sha256']))
    sealed = json.loads(guardian.bound_file(path, expected))
    validate_post_staging_intent(base)
    if base.get('stage') != 'post' or base.get('stopped_writer') or base.get('previous_deployment_id') or sealed.get('stage') != 'post' or not sealed.get('stopped_writer') or not sealed.get('previous_deployment_id'):
        raise RuntimeError('post handoff refinement requires an unstopped staging intent and a real frozen bridge')
    if {key: value for key, value in base.items() if key not in POST_FREEZING_FIELDS} != {key: value for key, value in sealed.items() if key not in POST_FREEZING_FIELDS}:
        raise RuntimeError('sealed post handoff changed immutable staging intent fields')
    return guardian.handoff(path, expected)


def maintenance_dispatch_status(work, state, uid=0, handoff_path=None, handoff_sha256=None):
    if state['phase'] != 'STAGED' or not state.get('maintenance_handoff'):
        return state
    result = dict(state, dispatch_verified_absent=False)
    try:
        binding = state['maintenance_handoff']
        maintenance = guardian.handoff(binding['path'], binding['sha256'])
        validate_post_staging_intent(maintenance)
        staging_intent = None
        if handoff_path and (str(handoff_path), handoff_sha256) != (binding['path'], binding['sha256']):
            staging_intent = state.get('maintenance_staging_intent', binding)
            maintenance = post_staging_refinement(staging_intent, handoff_path, handoff_sha256)
            binding = {'path': str(handoff_path), 'sha256': handoff_sha256}
        if maintenance['deployment_tool'] != 'systemd':
            raise RuntimeError('staged maintenance handoff belongs to another deployment tool')
        with deployment_lock(maintenance, binding['sha256']) as receipt:
            verify_cleanup_guardian(receipt)
            cleanup_path(work)
            cleanup_path(work / 'state.json', private_file=True)
            if read_state(work) != state:
                raise RuntimeError('staged owner state changed during dispatch inspection')
            if any(state.get(key) != maintenance.get(key) for key in ('transition_id', 'transition_intent_sha256', 'provider_sha256', 'prepare_config_sha256')) or state.get('maintenance_stage') != maintenance['stage']:
                raise RuntimeError('staged owner state differs from the frozen maintenance identity')
            info = cleanup_path(work / 'lmm-api-go')
            if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or digest(work / 'lmm-api-go') != state['sha256'] or state['sha256'] != maintenance['provider_sha256']:
                raise RuntimeError('staged maintenance provider bytes changed')
            entry = work / 'lmm-api'
            if not entry.is_symlink() or os.readlink(entry) != 'lmm-api-go' or entry.lstat().st_uid != uid:
                raise RuntimeError('staged maintenance provider entrypoint changed')
            cleanup_path(work / 'frontend')
            if tree_digest(work / 'frontend') != state['frontend_sha256'] or tree_digest(FRONTEND / 'current') != state['frontend_sha256']:
                raise RuntimeError('staged or frozen active frontend tree changed')
            # No apply mutation marker may exist. Read-only stage schema dumps
            # and verification logs are preparation evidence, not a dispatch.
            prepared_names = {'state.json', 'lmm-api-go', 'lmm-api', 'frontend', 'preflight-schema.sql', 'backup.log', 'verify-stage.log'}
            if any(path.name not in prepared_names for path in work.iterdir()):
                raise RuntimeError('staged workspace contains dispatch or unknown mutation evidence')
            if maintenance.get('stopped_writer'):
                verify_stopped_maintenance(maintenance)
            result.update(phase='NOT_DISPATCHED', dispatch_verified_absent=True,
                          maintenance_handoff_sha256=binding['sha256'], handoff_sha256=binding['sha256'],
                          transition_id=maintenance['transition_id'], transition_intent_sha256=maintenance['transition_intent_sha256'],
                          provider_sha256=maintenance['provider_sha256'], prepare_config_sha256=maintenance['prepare_config_sha256'])
            if staging_intent:
                result.update(maintenance_handoff=binding, maintenance_staging_intent=staging_intent)
    except (OSError, RuntimeError, ValueError, KeyError) as error:
        result['dispatch_verification_error'] = str(error)
    return result


CLEANUP_PAYLOADS = ('lmm-api-go', 'lmm-api', 'frontend', 'previous-binary', 'tmp', 'cache')


def cleanup_path(path, private_file=False, uid=0):
    path = Path(path)
    if not path.is_absolute() or '..' in path.parts or str(path) != os.path.normpath(str(path)):
        raise RuntimeError('cleanup evidence path must be absolute and canonical')
    for parent in path.parents:
        info = parent.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid not in (0, uid) or info.st_mode & 0o022:
            raise RuntimeError('cleanup evidence ancestor ownership or permissions are unsafe')
    info = path.lstat()
    if stat.S_ISLNK(info.st_mode) or info.st_uid != uid or info.st_mode & 0o022:
        raise RuntimeError('cleanup evidence ownership or permissions are unsafe')
    if private_file and (not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or stat.S_IMODE(info.st_mode) not in (0o600, 0o640)):
        raise RuntimeError('cleanup evidence must be a root-private single-linked regular file')
    return info


def verify_financial_archive(path, expected, uid=0):
    path = Path(path)
    if not re.fullmatch(r'[0-9a-f]{64}', expected or '') or path.is_relative_to(ROOT):
        raise RuntimeError('financial backup requires an exact digest and a path outside deployment workspaces')
    before = cleanup_path(path, private_file=True, uid=uid)
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        opened = os.fstat(descriptor)
        if opened.st_uid != uid or (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino):
            raise RuntimeError('financial backup changed while opening')
        with os.fdopen(descriptor, 'rb', closefd=False) as source:
            magic = source.read(5)
            result = hashlib.sha256(magic)
            for block in iter(lambda: source.read(1024 * 1024), b''):
                result.update(block)
        after = os.fstat(descriptor)
        named = path.lstat()
        identity = lambda info: (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns, info.st_mode, info.st_uid, info.st_nlink)
        if identity(before) != identity(after) or identity(after) != identity(named):
            raise RuntimeError('financial backup changed while hashing')
        if magic != b'PGDMP' or result.hexdigest() != expected:
            raise RuntimeError('financial backup is not the sealed PostgreSQL custom archive')
        return {'path': str(path), 'sha256': expected, 'bytes': before.st_size}
    finally:
        os.close(descriptor)


def verify_financial_backup_receipt(args, maintenance, archive):
    receipt = json.loads(guardian.bound_file(args.financial_backup_receipt, args.financial_backup_receipt_sha256))
    if not isinstance(receipt, dict) or receipt.get('format') != 'lmm-credit-financial-backup-v1' or any(receipt.get(key) != maintenance[key] for key in ('transition_id', 'transition_intent_sha256', 'provider_sha256')):
        raise RuntimeError('financial backup receipt differs from the frozen transition identity')
    if any(receipt.get(key) is not True for key in ('full_database', 'preserve_ownership')) or receipt.get('archive_format') != 'custom':
        raise RuntimeError('financial backup receipt does not prove a full custom archive preserving ownership')
    for key, length in (('source_sha', 40), ('frozen_guardian_bindings_sha256', 64)):
        if not isinstance(receipt.get(key), str) or not re.fullmatch(rf'[0-9a-f]{{{length}}}', receipt[key]):
            raise RuntimeError('financial backup receipt lacks the sealed source or guardian binding')
    prepared = json.loads(guardian.bound_file(maintenance['prepare_config_path'], maintenance['prepare_config_sha256']))
    target = receipt.get('target')
    required = {'database', 'schema', 'system_identifier', 'database_oid', 'schema_oid'}
    if not isinstance(target, dict) or set(target) != required or any(target.get(key) != prepared['database'].get(key) for key in required - {'database_oid', 'schema_oid'}):
        raise RuntimeError('financial backup receipt identifies a different PostgreSQL database or schema')
    database_oid = prepared['database'].get('database_oid')
    if type(database_oid) is not int or not 1 <= database_oid <= 4294967295 or not isinstance(target['database_oid'], str) or not re.fullmatch(r'[1-9][0-9]{0,9}', target['database_oid']) or int(target['database_oid']) != database_oid:
        raise RuntimeError('financial backup receipt identifies a different PostgreSQL database or schema')
    if not isinstance(target['schema_oid'], str) or not re.fullmatch(r'[1-9][0-9]{0,9}', target['schema_oid']) or int(target['schema_oid']) > 4294967295:
        raise RuntimeError('financial backup receipt lacks a valid frozen schema OID')
    if receipt.get('backup_sha256') != archive['sha256'] or not isinstance(receipt.get('size_bytes'), int) or isinstance(receipt['size_bytes'], bool) or receipt['size_bytes'] != archive['bytes']:
        raise RuntimeError('financial backup receipt differs from the actual archive bytes or size')
    return {'path': str(args.financial_backup_receipt), 'sha256': args.financial_backup_receipt_sha256,
            'target': target, 'source_sha': receipt['source_sha'],
            'frozen_guardian_bindings_sha256': receipt['frozen_guardian_bindings_sha256']}


def verify_cleanup_guardian(receipt, uid=0):
    locks = receipt.get('locks', []) if isinstance(receipt, dict) else []
    if len(locks) != 3 or {item.get('path') for item in locks} != set(guardian.LOCKS.values()):
        raise RuntimeError('cleanup requires a real guardian lease covering all three owner locks')
    for lock in locks:
        info = os.stat(lock['path'], follow_symlinks=False)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != uid or info.st_nlink != 1 or (info.st_dev, info.st_ino) != (lock.get('device'), lock.get('inode')):
            raise RuntimeError('guardian cleanup lock identity changed')
        descriptor = os.open(lock['path'], os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
        try:
            try:
                fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                pass
            else:
                raise RuntimeError('guardian cleanup lock is independently acquirable')
        finally:
            os.close(descriptor)


def verify_cleanup_protected(work, args, maintenance):
    if not maintenance or maintenance['stage'] != 'post' or args.superseded_by != work.name or args.retain_rollback == work.name:
        raise RuntimeError('cleanup requires the current post-maintenance owner and a distinct bridge rollback owner')
    bridge_work = ROOT / args.retain_rollback
    for protected in (work, bridge_work):
        cleanup_path(protected)
        cleanup_path(protected / 'state.json', private_file=True)
    current, bridge = read_state(work), read_state(bridge_work)
    if current['phase'] != 'CONFIRMED' or current.get('maintenance_admission_reopened') is not True:
        raise RuntimeError('cleanup requires confirmed current release with reopened admission')
    if bridge['phase'] not in ('FROZEN', 'MAINTENANCE_CONFIRMED') or bridge.get('maintenance_confirmation') is not True:
        raise RuntimeError('cleanup requires the retained confirmed bridge rollback workspace')
    bindings = ('transition_id', 'transition_intent_sha256', 'provider_sha256', 'prepare_config_sha256')
    current_binding = current.get('maintenance_handoff', {})
    current_frozen = guardian.handoff(current_binding.get('path', ''), current_binding.get('sha256', ''))
    bridge_binding = bridge.get('maintenance_handoff', {})
    frozen = guardian.handoff(bridge_binding.get('path', ''), bridge_binding.get('sha256', ''))
    if current_frozen['stage'] != 'post' or current_frozen.get('previous_deployment_id') != bridge['release'] or frozen['stage'] != 'prebridge' or any(current_frozen.get(key) != maintenance.get(key) or frozen.get(key) != maintenance.get(key) for key in bindings):
        raise RuntimeError('retained bridge differs from the current transition binding')
    for state in (current, bridge):
        if any(state.get(key) != maintenance.get(key) for key in ('transition_id', 'transition_intent_sha256', 'provider_sha256')):
            raise RuntimeError('protected owner state differs from the frozen transition')
    for binary in (BINARY, work / 'lmm-api-go', work / 'previous-binary', bridge_work / 'lmm-api-go'):
        info = cleanup_path(binary)
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or digest(binary) != maintenance['provider_sha256']:
            raise RuntimeError('current provider or true N-1 bridge bytes changed')
    if current['sha256'] != maintenance['provider_sha256'] or current.get('previous_sha256') != maintenance['provider_sha256'] or bridge['sha256'] != maintenance['provider_sha256'] or current.get('previous_version') != bridge['version']:
        raise RuntimeError('current rollback identity is not the retained bridge')
    for protected in (work, bridge_work):
        cleanup_path(protected / 'previous.env', private_file=True)
        if digest(protected / 'previous.env') != maintenance['archived_environment_sha256']:
            raise RuntimeError('protected rollback environment changed')
    cleanup_path(ENVIRONMENT, private_file=True)
    if digest(ENVIRONMENT) != maintenance['archived_environment_sha256']:
        raise RuntimeError('current service environment differs from the protected rollback configuration')
    original = guardian.bound_file(work / 'previous-nginx-locations', current['ingress_original_sha256'])
    if current['ingress_original_sha256'] != bridge['ingress_original_sha256'] or guardian.bound_file(bridge_work / 'previous-nginx-locations', bridge['ingress_original_sha256']) != original or NGINX_LOCATIONS.read_bytes() != original:
        raise RuntimeError('reopened ingress differs from retained owner evidence')
    frontend_target = 'releases/' + current['previous_frontend']
    if bridge.get('previous_frontend') != current['previous_frontend'] or os.readlink(FRONTEND / 'current') != frontend_target:
        raise RuntimeError('frozen active frontend link changed')
    cleanup_path(FRONTEND / frontend_target)
    cleanup_path(work / 'frontend')
    cleanup_path(bridge_work / 'frontend')
    for frontend in (FRONTEND / 'current', work / 'frontend', bridge_work / 'frontend'):
        if tree_digest(frontend) != current['frontend_sha256'] or bridge['frontend_sha256'] != current['frontend_sha256']:
            raise RuntimeError('frozen complete frontend tree changed')
    healthy(current['version'], maintenance)
    token = guardian.bound_file(maintenance['probe_token_path'], maintenance['probe_token_sha256']).decode().strip()
    for route in ('/api/livez', '/api/status'):
        body = health_json(route, maintenance['public_base_url'])
        if body.get('success') is not True or body.get('maintenance') is True or body.get('business_enabled') is False or (route == '/api/livez' and body.get('live') is not True) or (route == '/api/status' and (body.get('ready') is not True or body.get('data', {}).get('version') != current['version'])):
            raise RuntimeError('current public release is not strictly healthy')
    if not token or not isinstance(health_json('/v1/models', maintenance['public_base_url'], token).get('data'), list):
        raise RuntimeError('current public authenticated models probe failed')
    return current, bridge


def history_root():
    # Outside ROOT: preserved evidence is not another deployable workspace.
    return ROOT.with_name(ROOT.name + '-history')


def released_history_proof(post, controller_raw, confirmation_raw):
    cleanup_path(ROOT / post / 'state.json', private_file=True)
    state = read_state(ROOT / post)
    if state['phase'] != 'CONFIRMED' or state.get('maintenance_stage') != 'post' or state.get('maintenance_admission_reopened') is not True or state.get('maintenance_confirmation') is not True:
        raise RuntimeError('history registration requires confirmed reopened post owner')
    controller, confirmation = json.loads(controller_raw), json.loads(confirmation_raw)
    if controller.get('format') != 'lmm-credit-financial-maintenance-v1' or controller.get('phase') != 'RELEASED' or controller.get('guardian_release') != 'ordinary-owner-only-no-lock-path-deletion':
        raise RuntimeError('financial controller has not formally released its guardians')
    if confirmation.get('format') != 'lmm-credit-maintenance-release-v1' or confirmation.get('all_nodes_confirmed') is not True or controller.get('global_release', {}).get('sha256') != hashlib.sha256(confirmation_raw).hexdigest():
        raise RuntimeError('released controller does not bind the exact all-node confirmation')
    for key in ('transition_id', 'transition_intent_sha256'):
        if not state.get(key) or controller.get(key) != state[key] or confirmation.get(key) != state[key]:
            raise RuntimeError('released history crosses a financial transition')
    if controller.get('confirmations_sha256') != confirmation.get('confirmations_sha256') or controller.get('business_plan_sha256') != confirmation.get('business_plan_sha256') or 'ubuntu' not in confirmation.get('nodes', []):
        raise RuntimeError('released controller confirmation identity differs')
    binding = state.get('maintenance_handoff', {})
    if controller.get('stopped_handoffs', {}).get('ubuntu') != binding:
        raise RuntimeError('released controller does not bind this original post handoff')
    maintenance = guardian.handoff(binding.get('path', ''), binding.get('sha256', ''))
    if maintenance['stage'] != 'post' or maintenance['deployment_tool'] != 'systemd' or any(maintenance.get(k) != state.get(k) for k in ('transition_id', 'transition_intent_sha256', 'provider_sha256', 'prepare_config_sha256')):
        raise RuntimeError('post state differs from its sealed original handoff')
    ancestors = maintenance_ancestor_chain(post, maintenance)
    if not ancestors:
        raise RuntimeError('released post has no proved frozen ancestors')
    hashes = {name: digest(ROOT / name / 'state.json') for name in sorted({post, *ancestors})}
    return {'post': post, 'ancestors': sorted(ancestors), 'state_sha256': hashes,
            'controller_sha256': hashlib.sha256(controller_raw).hexdigest(),
            'confirmation_sha256': hashlib.sha256(confirmation_raw).hexdigest()}


def released_ancestors():
    directory = history_root() / 'released'
    if not directory.exists():
        return set()
    cleanup_path(directory)
    result = set()
    for work in sorted(directory.iterdir()):
        cleanup_path(work)
        cleanup_path(work / 'receipt.json', private_file=True)
        receipt = json.loads((work / 'receipt.json').read_bytes())
        if receipt.get('format') not in ('lmm-systemd-released-history-v1', 'lmm-systemd-released-history-v2') or receipt.get('post') != work.name:
            raise RuntimeError('unknown released-history receipt')
        controller = guardian.bound_file(work / 'controller.json', receipt['controller_sha256'])
        confirmation = guardian.bound_file(work / 'confirmation.json', receipt['confirmation_sha256'])
        if released_history_proof(work.name, controller, confirmation) != {k: receipt[k] for k in ('post', 'ancestors', 'state_sha256', 'controller_sha256', 'confirmation_sha256')}:
            raise RuntimeError('registered original history evidence changed')
        if receipt['format'] == 'lmm-systemd-released-history-v2':
            verify_registered_later_provider(work, receipt['later_provider'])
        result.update(receipt['ancestors'])
    return result


def history_lock_path(path, uid=0, native_lock=Path('/run/lock/lmm-api-go-deploy.lock')):
    path = Path(path)
    if not path.is_absolute() or '..' in path.parts or str(path) != os.path.normpath(str(path)):
        raise RuntimeError('history lock path must be canonical')
    for parent in path.parents:
        info = parent.lstat()
        sticky_native = (path == native_lock and parent == native_lock.parent and
                         info.st_uid == uid and stat.S_IMODE(info.st_mode) == 0o1777)
        if not stat.S_ISDIR(info.st_mode) or info.st_uid not in (0, uid) or (info.st_mode & 0o022 and not sticky_native):
            raise RuntimeError('history lock ancestor ownership or permissions are unsafe')
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != uid or info.st_nlink != 1 or info.st_mode & 0o022:
        raise RuntimeError('history lock must be root-owned, regular, single-linked and not writable by others')
    return info


@contextlib.contextmanager
def history_locks():
    # Normal history repair cannot borrow a financial guardian's live lease.
    if NATIVE_TRANSACTION_LEASE.exists() or NATIVE_TRANSACTION_LEASE.is_symlink():
        raise RuntimeError('native deployment lease is present; history repair refused')
    descriptors, identities = [], []
    try:
        for path in sorted(guardian.LOCKS.values()):
            info = history_lock_path(path)
            fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
            descriptors.append(fd)
            opened = os.fstat(fd)
            named = os.stat(path, follow_symlinks=False)
            if not stat.S_ISREG(opened.st_mode) or opened.st_uid != info.st_uid or opened.st_nlink != 1 or opened.st_mode & 0o022 or (opened.st_dev, opened.st_ino) != (info.st_dev, info.st_ino) or (named.st_dev, named.st_ino) != (opened.st_dev, opened.st_ino):
                raise RuntimeError('history lock is not a single-linked regular file')
            try:
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                raise RuntimeError('live owner or guardian holds a history lock') from None
            identities.append((path, fd, opened.st_dev, opened.st_ino))
        def verify_held_paths():
            if NATIVE_TRANSACTION_LEASE.exists() or NATIVE_TRANSACTION_LEASE.is_symlink():
                raise RuntimeError('native deployment lease appeared during history repair')
            for path, fd, device, inode in identities:
                opened = os.fstat(fd)
                named = history_lock_path(path)
                if (opened.st_dev, opened.st_ino) != (device, inode) or (named.st_dev, named.st_ino) != (device, inode) or opened.st_nlink != 1:
                    raise RuntimeError('held history lock inode changed')
        yield verify_held_paths
    finally:
        for fd in descriptors:
            os.close(fd)


def history_sync(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def history_directory(path):
    if path.exists() or path.is_symlink():
        cleanup_path(path)
    else:
        path.mkdir(mode=0o700)
        history_sync(path.parent)
    if stat.S_IMODE(path.stat().st_mode) != 0o700:
        raise RuntimeError('history directory must be private')


def history_json(raw):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise RuntimeError('history JSON contains duplicate fields')
            result[key] = value
        return result
    value = json.loads(raw, object_pairs_hook=unique)
    if not isinstance(value, dict):
        raise RuntimeError('history JSON must be an object')
    return value


LATER_PROVIDER_FIELDS = {'format', 'release', 'state_sha256', 'capsule_path', 'capsule_sha256',
                         'controller_plan_path', 'controller_plan_sha256', 'frontend_asset',
                         'frontend_asset_sha256', 'frontend_bundle', 'frontend_bundle_sha256',
                         'frontend_version', 'frontend_target', 'boot_id', 'main_pid', 'invocation_id'}


def later_provider_manifest(raw):
    manifest = history_json(raw)
    if set(manifest) != LATER_PROVIDER_FIELDS or manifest['format'] != 'lmm-systemd-later-provider-v1':
        raise RuntimeError('unknown later-provider manifest fields or format')
    if not isinstance(manifest['release'], str) or not re.fullmatch(RELEASE_PATTERN, manifest['release']):
        raise RuntimeError('later-provider release ID is invalid')
    for key in ('state_sha256', 'capsule_sha256', 'controller_plan_sha256', 'frontend_asset_sha256', 'frontend_bundle_sha256'):
        if not isinstance(manifest[key], str) or not re.fullmatch('[0-9a-f]{64}', manifest[key]):
            raise RuntimeError('later-provider digest is invalid')
    if type(manifest['main_pid']) is not int or manifest['main_pid'] <= 1 or not isinstance(manifest['invocation_id'], str) or not re.fullmatch('[0-9a-f]{32}', manifest['invocation_id']):
        raise RuntimeError('later-provider generation is invalid')
    if not isinstance(manifest['boot_id'], str) or not re.fullmatch('[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}', manifest['boot_id']):
        raise RuntimeError('later-provider boot identity is invalid')
    if not isinstance(manifest['frontend_version'], str) or not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+', manifest['frontend_version']):
        raise RuntimeError('later-provider frontend version is invalid')
    if not isinstance(manifest['frontend_target'], str) or not re.fullmatch('releases/[A-Za-z0-9][A-Za-z0-9._-]{0,100}', manifest['frontend_target']):
        raise RuntimeError('later-provider frontend target is invalid')
    for key in ('capsule_path', 'controller_plan_path', 'frontend_asset', 'frontend_bundle'):
        value = manifest[key]
        if not isinstance(value, str) or not Path(value).is_absolute() or '..' in Path(value).parts or os.path.normpath(value) != value:
            raise RuntimeError('later-provider input path is not canonical')
    expected = Path('/var/lib/lmm-api-go-deploy/merchant-capsules') / manifest['release'] / 'capsule.json'
    if Path(manifest['capsule_path']) != expected:
        raise RuntimeError('later-provider capsule is not in its actual release root')
    return manifest


def later_provider_inputs(manifest, state_raw, capsule_raw, plan_raw):
    state, capsule, plan = map(history_json, (state_raw, capsule_raw, plan_raw))
    if state.get('release') != manifest['release'] or state.get('phase') != 'CONFIRMED' or state.get('migrate') is not False or state.get('backup_exclude_tables') != [] or any(key.startswith('maintenance_') for key in state):
        raise RuntimeError('later-provider owner is not a confirmed ordinary transaction')
    root = str(Path(manifest['capsule_path']).parent)
    if capsule.get('format') != 1 or capsule.get('deployment_id') != state['release'] or capsule.get('root') != root or capsule.get('binary') != str(BINARY) or capsule.get('service') != SERVICE or capsule.get('startup_policy') != 'per-start' or capsule.get('schema_mode') != 'verify-existing':
        raise RuntimeError('later-provider capsule scope differs from confirmed owner')
    if capsule.get('controller_plan_sha256') != manifest['controller_plan_sha256'] or plan.get('format') != 7 or plan.get('deployment_id') != state['release'] or plan.get('schema_mode') != 'verify-existing':
        raise RuntimeError('later-provider original controller plan differs')
    for key in ('format', 'system_identifier', 'database', 'database_oid', 'schema', 'schema_oid', 'metadata_sha256', 'signed_unit_sha256'):
        if capsule.get('existing_schema_contract', {}).get(key) != plan.get('existing_schema_contract', {}).get(key):
            raise RuntimeError('later-provider physical schema differs from original plan')
    for key in ('format', 'required_capability', 'system_identifier', 'database', 'database_oid', 'schema', 'schema_oid', 'role', 'signed_unit_sha256', 'recovery_policy', 'candidate', 'rollback'):
        if capsule.get('merchant_store_writer', {}).get(key) != plan.get('merchant_store_writer', {}).get(key):
            raise RuntimeError('later-provider writer qualification differs from original plan')
    fields = ('name', 'version', 'package_sha256', 'git_revision', 'contract_revision', 'payload_sha256',
              'release_asset_sha256', 'signature_bundle_sha256', 'release_tag', 'workflow')
    for role in ('candidate', 'rollback'):
        target, planned = capsule.get(role, {}), plan.get('go_' + role, {})
        if any(target.get(key) != planned.get(key) for key in fields):
            raise RuntimeError('later-provider signed package differs from original plan')
    candidate = capsule['candidate']
    if candidate.get('payload_sha256') != state.get('sha256') or candidate.get('version') != state.get('version', '') + '-1' or candidate.get('release_tag') != 'go-v' + state.get('version', '') or candidate.get('workflow') != 'release-go.yml':
        raise RuntimeError('later-provider official candidate differs from confirmed state')
    return state, capsule, plan


def later_signed_archive(asset, bundle, asset_sha, bundle_sha, version, component):
    # Verify the official certificate before trusting/executing any provider.
    cleanup_path(asset, private_file=True)
    cleanup_path(bundle, private_file=True)
    if digest(asset) != asset_sha or digest(bundle) != bundle_sha:
        raise RuntimeError('later-provider signed artifact bytes changed')
    tag = ('go-v' if component == 'go' else 'web-v') + version
    identity = 'https://github.com/TokenNotIncluded/api.lmm.best/.github/workflows/release-' + component + '.yml@refs/tags/' + tag
    result = subprocess.run(['cosign', 'verify-blob', '--bundle', str(bundle), '--certificate-identity', identity,
                             '--certificate-oidc-issuer', 'https://token.actions.githubusercontent.com', str(asset)],
                            capture_output=True, timeout=180)
    if result.returncode:
        raise RuntimeError('later-provider official artifact signature failed')
    files, seen = {}, set()
    with tarfile.open(asset, 'r:*') as archive:
        for member in archive:
            name = member.name.rstrip('/')
            if not name or name.startswith('/') or '\\' in name or '..' in Path(name).parts or name in seen or not (member.isfile() or member.isdir()):
                raise RuntimeError('later-provider signed archive has unsafe entries')
            seen.add(name)
            if member.isfile():
                if member.size > 128 * 1024 * 1024:
                    raise RuntimeError('later-provider signed archive entry is too large')
                with archive.extractfile(member) as source:
                    files[name] = hashlib.file_digest(source, 'sha256').hexdigest()
                if name.endswith('/REVISION') or name == 'REVISION':
                    revision = archive.extractfile(member).read(100).decode().strip()
    if not isinstance(locals().get('revision'), str) or not re.fullmatch('[0-9a-f]{40}', revision):
        raise RuntimeError('later-provider signed archive lacks its source revision')
    if component == 'go':
        roots = {name.split('/')[0] for name in seen}
        if len(roots) != 1:
            raise RuntimeError('later-provider Go archive has multiple roots')
        prefix = next(iter(roots)) + '/'
        if prefix + 'lmm-api-go' not in files:
            raise RuntimeError('later-provider signed Go payload is missing')
        return {'source_revision': revision, 'payload_sha256': files[prefix + 'lmm-api-go']}
    frontend = {name.removeprefix('dist/'): sha for name, sha in files.items() if name.startswith('dist/')}
    if 'index.html' not in frontend:
        raise RuntimeError('later-provider signed frontend payload is missing')
    tree = hashlib.sha256()
    for name, sha in sorted(frontend.items()):
        tree.update(name.encode() + b'\0' + sha.encode() + b'\0')
    return {'source_revision': revision, 'tree_sha256': tree.hexdigest(), 'index_sha256': frontend['index.html'], 'files': len(frontend)}


def later_generation(manifest):
    names = 'MainPID,InvocationID,NRestarts,ControlPID,ActiveState,SubState,Result,ExecStartPre,Environment'
    loaded = dict(row.split('=', 1) for row in run('systemctl', 'show', SERVICE, '--property=' + names).splitlines())
    if any(loaded.get(k) != v for k, v in {'MainPID': str(manifest['main_pid']), 'InvocationID': manifest['invocation_id'],
          'NRestarts': '0', 'ControlPID': '0', 'ActiveState': 'active', 'SubState': 'running', 'Result': 'success'}.items()):
        raise RuntimeError('later-provider actual service generation differs')
    if Path('/proc/sys/kernel/random/boot_id').read_text().strip() != manifest['boot_id']:
        raise RuntimeError('later-provider boot changed')
    # A real successful typed writer-start must be the loaded startup guard.
    if 'writer-start' not in loaded.get('ExecStartPre', '') or 'status=0' not in loaded['ExecStartPre'] or 'code=exited' not in loaded['ExecStartPre']:
        raise RuntimeError('later-provider actual startup did not complete successfully')
    process = Path('/proc', str(manifest['main_pid']))
    environment = process.joinpath('environ').read_bytes()
    if len(environment) > 1024 * 1024:
        raise RuntimeError('later-provider process environment is too large')
    env_files = []
    for name in service_environment_files():
        path = Path(name.removeprefix('-'))
        cleanup_path(path, private_file=True)
        env_files.append({'path': str(path), 'sha256': digest(path)})
    return {'boot_id': manifest['boot_id'], 'main_pid': manifest['main_pid'], 'invocation_id': manifest['invocation_id'],
            'installed_sha256': digest(BINARY), 'running_sha256': digest(process / 'exe'),
            'process_environment_sha256': hashlib.sha256(environment).hexdigest(),
            'loaded_environment_sha256': hashlib.sha256(loaded['Environment'].encode()).hexdigest(),
            'ordered_environment_files': env_files}


def later_database_status(manifest, capsule):
    process = Path('/proc', str(manifest['main_pid']), 'environ').read_bytes().split(b'\0')
    values = dict(row.split(b'=', 1) for row in process if b'=' in row)
    if values.get(b'LOG_SQL_DSN') not in (None, b'', values.get(b'SQL_DSN')):
        raise RuntimeError('later-provider has a separate unproved log database')
    values.pop(b'LOG_SQL_DSN', None)
    env = database_environment_from_values(values)
    predicate = r"pg_catalog.lower(pg_catalog.btrim(key, E' \t\n\r\f\013\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000')) LIKE 'merchantstoredeploymentfence:%'"
    query = "SELECT json_build_object('system_identifier',(SELECT system_identifier::text FROM pg_control_system()),'database',current_database(),'database_oid',(SELECT oid::bigint FROM pg_database WHERE datname=current_database()),'schema',current_schema(),'schema_oid',(SELECT oid::bigint FROM pg_namespace WHERE nspname=current_schema()),'role',current_user,'reserved_count',(SELECT count(*) FROM options WHERE " + predicate + '))'
    result = subprocess.run(['psql', '-X', '-qAt', '-v', 'ON_ERROR_STOP=1', '--command', 'BEGIN READ ONLY; ' + query + '; ROLLBACK;'], env=env, capture_output=True, timeout=30)
    if result.returncode:
        raise RuntimeError('later-provider actual physical database/owner check failed')
    actual = history_json(result.stdout)
    writer = capsule['merchant_store_writer']
    if actual.get('reserved_count') != 0 or any(actual.get(key) != writer.get(key) for key in ('system_identifier', 'database', 'database_oid', 'schema', 'schema_oid', 'role')):
        raise RuntimeError('later-provider physical database differs or has a live durable owner')
    return actual


def qualify_later_provider(manifest_raw, post):
    manifest = later_provider_manifest(manifest_raw)
    paths = {'origin-state.json': (ROOT / manifest['release'] / 'state.json', manifest['state_sha256']),
             'origin-capsule.json': (Path(manifest['capsule_path']), manifest['capsule_sha256']),
             'origin-plan.json': (Path(manifest['controller_plan_path']), manifest['controller_plan_sha256'])}
    inputs = {name: guardian.bound_file(path, sha) for name, (path, sha) in paths.items()}
    state, capsule, plan = later_provider_inputs(manifest, inputs['origin-state.json'], inputs['origin-capsule.json'], inputs['origin-plan.json'])
    if capsule.get('host') != socket.gethostname():
        raise RuntimeError('later-provider capsule belongs to another host')
    prepared = history_json(guardian.bound_file(post['prepare_config_path'], post['prepare_config_sha256']))
    physical = capsule.get('existing_schema_contract', {})
    if any(physical.get(key) != prepared.get('database', {}).get(key) for key in ('system_identifier', 'database', 'database_oid', 'schema')):
        raise RuntimeError('later-provider crosses the released physical database')
    if capsule.get('merchant_store_writer', {}).get('role') != prepared.get('database', {}).get('database_user'):
        raise RuntimeError('later-provider crosses the released database role')
    candidate = capsule['candidate']; root = Path(capsule['root'])
    backend = later_signed_archive(root / candidate['release_asset'], root / candidate['signature_bundle'],
                                   candidate['release_asset_sha256'], candidate['signature_bundle_sha256'], state['version'], 'go')
    if backend != {'source_revision': candidate['git_revision'], 'payload_sha256': state['sha256']} or digest(BINARY) != state['sha256']:
        raise RuntimeError('later-provider installed ELF is not the signed origin candidate')
    generation = later_generation(manifest)
    if generation['running_sha256'] != state['sha256']:
        raise RuntimeError('later-provider running ELF differs from its signed origin')
    result = subprocess.run([str(BINARY), 'operator', 'production', 'writer-capsule', 'check', '--capsule',
                             manifest['capsule_path'], '--capsule-sha256', manifest['capsule_sha256']],
                            capture_output=True, timeout=900)
    if result.returncode or b'merchant_store_capsule=qualified' not in result.stdout:
        raise RuntimeError('later-provider actual native qualification failed')
    database = later_database_status(manifest, capsule)
    web = later_signed_archive(Path(manifest['frontend_asset']), Path(manifest['frontend_bundle']),
                               manifest['frontend_asset_sha256'], manifest['frontend_bundle_sha256'], manifest['frontend_version'], 'web')
    current = FRONTEND / 'current'
    if not current.is_symlink() or os.readlink(current) != manifest['frontend_target']:
        raise RuntimeError('later-provider active frontend changed')
    published = FRONTEND / manifest['frontend_target']; cleanup_path(published)
    if tree_digest(published) != web['tree_sha256'] or digest(published / 'index.html') != web['index_sha256']:
        raise RuntimeError('later-provider active frontend differs from signed payload')
    healthy(state['version'], None)
    if later_generation(manifest) != generation or any(guardian.bound_file(path, sha) != inputs[name] for name, (path, sha) in paths.items()):
        raise RuntimeError('later-provider identity changed during qualification')
    proof = {'format': 'lmm-systemd-later-provider-qualification-v1', 'release': state['release'],
             'manifest_sha256': hashlib.sha256(manifest_raw).hexdigest(),
             'input_sha256': {name: hashlib.sha256(raw).hexdigest() for name, raw in inputs.items()},
             'generation': generation, 'physical_database': {key: physical[key] for key in ('system_identifier', 'database', 'database_oid', 'schema', 'schema_oid')},
             'actual_database': database,
             'native_check': {'exit_code': result.returncode, 'stdout_sha256': hashlib.sha256(result.stdout).hexdigest(), 'stderr_sha256': hashlib.sha256(result.stderr).hexdigest()},
             'frontend': web, 'frontend_target': manifest['frontend_target']}
    artifacts = {str(root / capsule[role][key]): capsule[role][sha_key]
                 for role in ('candidate', 'rollback')
                 for key, sha_key in (('package_path', 'package_sha256'), ('release_asset', 'release_asset_sha256'), ('signature_bundle', 'signature_bundle_sha256'))}
    artifacts.update({manifest['frontend_asset']: manifest['frontend_asset_sha256'], manifest['frontend_bundle']: manifest['frontend_bundle_sha256']})
    proof['artifact_sha256'] = artifacts
    recheck_later_provider(manifest_raw, proof, inputs)
    return proof, inputs


def recheck_later_provider(manifest_raw, proof, inputs):
    manifest = later_provider_manifest(manifest_raw)
    for name, path, sha in (('origin-state.json', ROOT / manifest['release'] / 'state.json', manifest['state_sha256']),
                            ('origin-capsule.json', Path(manifest['capsule_path']), manifest['capsule_sha256']),
                            ('origin-plan.json', Path(manifest['controller_plan_path']), manifest['controller_plan_sha256'])):
        if guardian.bound_file(path, sha) != inputs[name]:
            raise RuntimeError('later-provider immutable origin changed during registration')
    if later_generation(manifest) != proof['generation']:
        raise RuntimeError('later-provider actual generation changed during registration')
    capsule = history_json(inputs['origin-capsule.json'])
    if later_database_status(manifest, capsule) != proof['actual_database']:
        raise RuntimeError('later-provider database/owner changed during registration')
    for path, sha in proof['artifact_sha256'].items():
        cleanup_path(Path(path), private_file=True)
        if digest(Path(path)) != sha:
            raise RuntimeError('later-provider qualified signed artifacts changed')
    if os.readlink(FRONTEND / 'current') != manifest['frontend_target'] or tree_digest(FRONTEND / manifest['frontend_target']) != proof['frontend']['tree_sha256']:
        raise RuntimeError('later-provider qualified frontend changed during registration')


def verify_registered_later_provider(work, binding):
    manifest_raw = guardian.bound_file(work / 'later-provider.json', binding['manifest_sha256'])
    manifest = later_provider_manifest(manifest_raw)
    qualification = history_json(guardian.bound_file(work / 'later-qualification.json', binding['qualification_sha256']))
    expected_inputs = {'origin-state.json': manifest['state_sha256'], 'origin-capsule.json': manifest['capsule_sha256'], 'origin-plan.json': manifest['controller_plan_sha256']}
    if binding.get('input_sha256') != expected_inputs or qualification.get('release') != manifest['release']:
        raise RuntimeError('registered later-provider manifest identity changed')
    inputs = {name: guardian.bound_file(work / name, sha) for name, sha in binding['input_sha256'].items()}
    if set(inputs) != {'origin-state.json', 'origin-capsule.json', 'origin-plan.json'} or qualification.get('format') != 'lmm-systemd-later-provider-qualification-v1' or qualification.get('manifest_sha256') != binding['manifest_sha256'] or qualification.get('input_sha256') != binding['input_sha256'] or qualification.get('native_check', {}).get('exit_code') != 0:
        raise RuntimeError('registered later-provider qualification changed')
    later_provider_inputs(manifest, inputs['origin-state.json'], inputs['origin-capsule.json'], inputs['origin-plan.json'])
    # Deliberately historical: a subsequent valid upgrade must not require the
    # formerly current provider, environment or frontend to remain installed.


def register_released_history(args):
    controller = guardian.bound_file(args.released_controller, args.released_controller_sha256)
    confirmation = guardian.bound_file(args.global_confirmation, args.global_confirmation_sha256)
    proof = released_history_proof(args.release, controller, confirmation)
    state = read_state(ROOT / args.release)
    later_raw = guardian.bound_file(args.later_provider, args.later_provider_sha256) if getattr(args, 'later_provider', None) else None
    if later_raw is None and digest(BINARY) != state['sha256']:
        raise RuntimeError('released post is not the installed current provider')
    generations = json.loads(controller).get('guardian_generations', {})
    pid = generations.get('ubuntu')
    if not isinstance(pid, int) or isinstance(pid, bool) or pid <= 1 or Path('/proc', str(pid)).exists():
        raise RuntimeError('released guardian absence is not established')
    result = {'format': 'lmm-systemd-released-history-v2' if later_raw else 'lmm-systemd-released-history-v1', **proof, 'execute': args.execute}
    with history_locks() as verify_held_paths:
        verify_held_paths()
        if released_history_proof(args.release, controller, confirmation) != proof or (later_raw is None and digest(BINARY) != state['sha256']):
            raise RuntimeError('history evidence changed before registration')
        qualification, inputs = None, {}
        if later_raw:
            maintenance = guardian.handoff(state['maintenance_handoff']['path'], state['maintenance_handoff']['sha256'])
            qualification, inputs = qualify_later_provider(later_raw, maintenance)
            qualification_raw = json.dumps(qualification, sort_keys=True).encode() + b'\n'
            result['later_provider'] = {'manifest_sha256': hashlib.sha256(later_raw).hexdigest(),
                                      'qualification_sha256': hashlib.sha256(qualification_raw).hexdigest(),
                                      'input_sha256': qualification['input_sha256']}
        verify_held_paths()
        if args.execute:
            root = history_root()
            history_directory(root)
            history_directory(root / 'released')
            work = root / 'released' / args.release
            work.mkdir(mode=0o700)  # Existing/partial registration requires inspection.
            history_sync(work.parent)
            immutable_write(work / 'controller.json', controller, 0o600)
            immutable_write(work / 'confirmation.json', confirmation, 0o600)
            if later_raw:
                immutable_write(work / 'later-provider.json', later_raw, 0o600)
                immutable_write(work / 'later-qualification.json', qualification_raw, 0o600)
                for name, raw in inputs.items():
                    immutable_write(work / name, raw, 0o600)
            verify_held_paths()
            if released_history_proof(args.release, controller, confirmation) != proof:
                raise RuntimeError('history evidence changed during registration')
            if later_raw:
                recheck_later_provider(later_raw, qualification, inputs)
            immutable_write(work / 'receipt.json', json.dumps(result, sort_keys=True).encode() + b'\n', 0o600)
            history_sync(work)
            released_ancestors()
        verify_held_paths()
    return result


def incomplete_inventory(work):
    cleanup_path(work)
    if (work / 'state.json').exists() or (work / 'state.json').is_symlink():
        raise RuntimeError('archive-incomplete cannot archive an owner state')
    allowed = {'lmm-api', 'lmm-api-go', 'frontend', 'logs', 'verify-stage.log', 'preflight-schema.sql', 'backup.log'}
    if any(p.name not in allowed for p in work.iterdir()):
        raise RuntimeError('incomplete workspace has unknown or mutation evidence')
    rows = []
    for p in sorted(work.rglob('*')):
        st = p.lstat()
        if p == work / 'lmm-api' and stat.S_ISLNK(st.st_mode) and os.readlink(p) == 'lmm-api-go' and st.st_uid == os.getuid():
            rows.append({'path': 'lmm-api', 'symlink': 'lmm-api-go'})
            continue
        cleanup_path(p)
        if p.name in {'state.next', 'previous-binary', 'previous.env', 'previous-nginx-locations', 'start.log', 'stop.log', 'frontend.log', 'verify-apply.log', 'verify-migrate.log'}:
            raise RuntimeError('incomplete workspace contains mutation evidence')
        if stat.S_ISDIR(st.st_mode):
            rows.append({'path': str(p.relative_to(work)), 'directory': True, 'mode': stat.S_IMODE(st.st_mode)})
        elif stat.S_ISREG(st.st_mode) and st.st_nlink == 1:
            rows.append({'path': str(p.relative_to(work)), 'size': st.st_size, 'sha256': digest(p), 'mode': stat.S_IMODE(st.st_mode)})
        else:
            raise RuntimeError('incomplete workspace has unsafe links or file types')
    if not rows:
        raise RuntimeError('incomplete workspace has no archiveable preparation evidence')
    return rows


def incomplete_authoritative_references(work, staged_state=None):
    refs = cleanup_process_references(work, entire_workspace=True)
    # Logs and descriptive inventories are not authority. Current/N-1 and
    # active/STAGED owner states are; retain unknown ownership conservatively.
    registered = released_ancestors()
    states = read_status()['deployments']
    current = os.readlink(FRONTEND / 'current')
    values = [str(BINARY.resolve()), str((FRONTEND / current).resolve())]
    confirmed = [state for state in states if state['phase'] == 'CONFIRMED']
    def terminal_order(state):
        value = state.get('terminal_at', state.get('ready_at', 0))
        if not isinstance(value, (int, float)) or isinstance(value, bool) or not math.isfinite(value):
            raise RuntimeError('current owner terminal evidence is invalid')
        return value
    selected = [state for state in confirmed if 'releases/' + state['release'] == current]
    current_state = max(selected or confirmed, key=terminal_order, default={})
    retained = {current_state.get('release'), current_state.get('previous_frontend')}
    if staged_state is not None and (current == 'releases/' + work.name or work.name in retained):
        refs.append({'kind': 'current or retained owner', 'release': work.name})
    for state in states:
        if state['phase'] == 'PREPARATION_INCOMPLETE':
            continue
        if staged_state is not None and state['release'] == work.name:
            if state != staged_state:
                raise RuntimeError('bound STAGED owner state changed during reference inspection')
            continue  # Only the exact unmutated, hash-bound state is excluded.
        if (state['phase'] in ('STAGED', 'MUTATION_PENDING', 'AWAITING_CONFIRMATION', 'ROLLBACK_REQUIRED', 'CAPTURED', 'ADMISSION_CLOSED', 'MAINTENANCE_CONFIRMED') or state['release'] in retained or (state['phase'] == 'FROZEN' and state['release'] not in registered)):
            values.extend(cleanup_strings(state))
            if staged_state is not None and any(state.get(key) in (work.name, 'releases/' + work.name) for key in ('previous_frontend', 'previous_deployment_id')):
                refs.append({'kind': 'retained owner reference', 'release': state['release']})
    for value in values:
        if isinstance(value, str) and value.startswith('/') and Path(os.path.normpath(value)).is_relative_to(work):
            refs.append({'kind': 'authoritative owner reference', 'path': value})
    return refs


def verify_incomplete_archives():
    directory = history_root() / 'incomplete'
    if not directory.exists():
        return
    cleanup_path(directory)
    for archive in sorted(directory.iterdir()):
        cleanup_path(archive)
        cleanup_path(archive / 'receipt.json', private_file=True)
        receipt = json.loads((archive / 'receipt.json').read_bytes())
        if receipt.get('format') != 'lmm-systemd-incomplete-archive-v1' or receipt.get('release') != archive.name or receipt.get('original_path') != str(ROOT / archive.name) or receipt.get('archive_path') != str(archive) or receipt.get('execute') is not True:
            raise RuntimeError('unknown incomplete archive receipt')
        if (ROOT / archive.name).exists() or (ROOT / archive.name).is_symlink() or incomplete_inventory(archive / 'copy') != receipt['inventory'] or incomplete_inventory(archive / 'original') != receipt['inventory']:
            raise RuntimeError('full original incomplete archive changed')


def archive_incomplete(args):
    work = ROOT / args.release
    rows = incomplete_inventory(work)
    if incomplete_authoritative_references(work):
        raise RuntimeError('incomplete workspace is used by an authoritative owner or process')
    result = {'format': 'lmm-systemd-incomplete-archive-v1', 'release': args.release,
              'original_path': str(work), 'inventory': rows, 'execute': args.execute}
    # Even dry-run checks guardian/lease exclusion; a passing inventory alone
    # never authorizes archiving while a transaction owns the three locks.
    with history_locks():
        if incomplete_inventory(work) != rows or incomplete_authoritative_references(work):
            raise RuntimeError('incomplete archive evidence changed')
        if args.execute:
            root = history_root()
            history_directory(root)
            history_directory(root / 'incomplete')
            archive = root / 'incomplete' / args.release
            archive.mkdir(mode=0o700)
            history_sync(archive.parent)
            # Complete private copy and readback before moving the original.
            shutil.copytree(work, archive / 'copy', symlinks=True)
            if incomplete_inventory(archive / 'copy') != rows or incomplete_inventory(work) != rows:
                raise RuntimeError('full incomplete archive readback mismatch')
            for p in (archive / 'copy').rglob('*'):
                if p.is_file() and not p.is_symlink():
                    with p.open('rb') as f:
                        os.fsync(f.fileno())
            for p in sorted((archive / 'copy').rglob('*'), reverse=True):
                if p.is_dir() and not p.is_symlink():
                    history_sync(p)
            history_sync(archive / 'copy')
            result['archive_path'] = str(archive)
            immutable_write(archive / 'intent.json', json.dumps(result, sort_keys=True).encode() + b'\n', 0o600)
            history_sync(archive)
            if incomplete_authoritative_references(work) or incomplete_inventory(work) != rows:
                raise RuntimeError('incomplete workspace became referenced before archival')
            work.rename(archive / 'original')
            history_sync(ROOT)
            history_sync(archive)
            immutable_write(archive / 'receipt.json', json.dumps(result, sort_keys=True).encode() + b'\n', 0o600)
            history_sync(archive)
    return result


STAGED_STATE_FIELDS = {'release', 'version', 'sha256', 'frontend_sha256', 'migrate', 'backup_exclude_tables', 'phase'}


def staged_inventory(work, release, state_sha256):
    """A complete ordinary preparation; never infer absence of mutation from phase alone."""
    cleanup_path(work)
    raw = guardian.bound_file(work / 'state.json', state_sha256)
    def unique_fields(pairs):
        value = {}
        for key, item in pairs:
            if key in value:
                raise RuntimeError('duplicate STAGED state field')
            value[key] = item
        return value
    state = json.loads(raw, object_pairs_hook=unique_fields)
    if (not isinstance(state, dict) or set(state) != STAGED_STATE_FIELDS or
            state.get('release') != release or state.get('phase') != 'STAGED' or
            state.get('migrate') is not False or state.get('backup_exclude_tables') != [] or
            not isinstance(state.get('version'), str) or not state['version'] or
            any(not re.fullmatch(r'[0-9a-f]{64}', state.get(key, '') if isinstance(state.get(key), str) else '') for key in ('sha256', 'frontend_sha256'))):
        raise RuntimeError('archive-staged requires an exact ordinary, unmutated STAGED owner without migrations')
    allowed = {'state.json', 'lmm-api', 'lmm-api-go', 'frontend', 'logs', 'verify-stage.log'}
    if any(path.name not in allowed for path in work.iterdir()):
        raise RuntimeError('STAGED workspace contains unknown or mutation evidence')
    rows = []
    for path in sorted(work.rglob('*')):
        info = path.lstat()
        if path == work / 'lmm-api' and stat.S_ISLNK(info.st_mode) and os.readlink(path) == 'lmm-api-go' and info.st_uid == os.getuid():
            rows.append({'path': 'lmm-api', 'symlink': 'lmm-api-go'})
            continue
        cleanup_path(path, private_file=path == work / 'state.json')
        if path.name.startswith('previous_') or path.name.startswith('previous-') or path.name in {'state.next', 'previous.env', 'start.log', 'stop.log', 'frontend.log', 'verify-apply.log', 'verify-migrate.log'}:
            raise RuntimeError('STAGED workspace contains mutation evidence')
        if stat.S_ISDIR(info.st_mode):
            if path.parent == work and path.name not in ('frontend', 'logs'):
                raise RuntimeError('STAGED workspace has an unexpected directory')
            rows.append({'path': str(path.relative_to(work)), 'directory': True, 'mode': stat.S_IMODE(info.st_mode)})
        elif stat.S_ISREG(info.st_mode) and info.st_nlink == 1:
            rows.append({'path': str(path.relative_to(work)), 'size': info.st_size, 'sha256': digest(path), 'mode': stat.S_IMODE(info.st_mode)})
        else:
            raise RuntimeError('STAGED workspace has unsafe links or file types')
    if (not (work / 'lmm-api').is_symlink() or not (work / 'lmm-api-go').is_file() or
            not (work / 'frontend').is_dir() or not (work / 'frontend/index.html').is_file() or
            digest(work / 'lmm-api-go') != state['sha256'] or tree_digest(work / 'frontend') != state['frontend_sha256']):
        raise RuntimeError('STAGED provider or frontend differs from its original state')
    return state, rows


def staged_current_observation():
    # Read-only unit/file metadata. No candidate execution, service or database action.
    unit = {key: property_value(key) for key in ('MainPID', 'InvocationID', 'ActiveState', 'SubState')}
    if not unit['MainPID'].isdigit() or int(unit['MainPID']) <= 1 or unit['ActiveState'] != 'active' or unit['SubState'] != 'running' or not re.fullmatch(r'[0-9a-f]{32}', unit['InvocationID']):
        raise RuntimeError('archive-staged requires a proved unchanged running current owner')
    cleanup_path(BINARY)
    cleanup_path(FRONTEND)
    link = FRONTEND / 'current'
    info = link.lstat()
    current = os.readlink(link)
    if not stat.S_ISLNK(info.st_mode) or info.st_uid != os.getuid() or not re.fullmatch(r'releases/[A-Za-z0-9][A-Za-z0-9._-]{0,127}', current):
        raise RuntimeError('current frontend ownership or link is unsafe')
    cleanup_path(FRONTEND / current)
    return {'unit': unit, 'provider_sha256': digest(BINARY), 'frontend_current': current,
            'frontend_sha256': tree_digest(FRONTEND / current)}


def reject_archived_release_id(release):
    root = history_root()
    if root.exists() or root.is_symlink():
        cleanup_path(root)
        for kind in ('incomplete', 'staged'):
            directory = root / kind
            if directory.exists() or directory.is_symlink():
                cleanup_path(directory)
                if (directory / release).exists() or (directory / release).is_symlink():
                    raise RuntimeError('release ID was already archived; stage with a new unique ID')


def verify_staged_archives():
    directory = history_root() / 'staged'
    if not directory.exists() and not directory.is_symlink():
        return
    cleanup_path(directory)
    for archive in sorted(directory.iterdir()):
        cleanup_path(archive)
        cleanup_path(archive / 'receipt.json', private_file=True)
        raw = (archive / 'receipt.json').read_bytes()
        receipt = json.loads(raw)
        if (receipt.get('format') != 'lmm-systemd-staged-archive-v1' or receipt.get('release') != archive.name or
                receipt.get('original_path') != str(ROOT / archive.name) or receipt.get('archive_path') != str(archive) or
                receipt.get('execute') is not True or not re.fullmatch(r'[0-9a-f]{64}', receipt.get('state_sha256', '')) or
                not re.fullmatch(r'[0-9a-f]{64}', receipt.get('owner_source_sha256', ''))):
            raise RuntimeError('unknown STAGED archive receipt')
        if guardian.bound_file(archive / 'intent.json', hashlib.sha256(raw).hexdigest()) != raw:
            raise RuntimeError('STAGED archive intent differs from completion receipt')
        if (ROOT / archive.name).exists() or (ROOT / archive.name).is_symlink():
            raise RuntimeError('archived STAGED release ID was reused')
        for name in ('copy', 'original'):
            _, rows = staged_inventory(archive / name, archive.name, receipt['state_sha256'])
            if rows != receipt['inventory']:
                raise RuntimeError('full original STAGED archive changed')


def archive_staged(args):
    source = Path(__file__).absolute()
    cleanup_path(source, private_file=True)
    guardian.bound_file(source, args.owner_source_sha256)
    guardian_source = Path(guardian.__file__).absolute()
    cleanup_path(guardian_source)
    work = ROOT / args.release
    state, rows = staged_inventory(work, args.release, args.staged_state_sha256)
    archive = history_root() / 'staged' / args.release
    result = {'format': 'lmm-systemd-staged-archive-v1', 'release': args.release,
              'original_path': str(work), 'archive_path': str(archive), 'inventory': rows,
              'state_sha256': args.staged_state_sha256, 'owner_source_sha256': args.owner_source_sha256,
              'guardian_source_sha256': digest(guardian_source), 'execute': args.execute}
    with history_locks() as verify_locks:
        verify_staged_archives()
        if archive.exists() or archive.is_symlink():
            raise RuntimeError('STAGED archive already exists; inspect its original evidence')
        observed = staged_current_observation()
        result['current_owner'] = observed
        def unchanged():
            verify_locks()
            guardian.bound_file(source, args.owner_source_sha256)
            if digest(guardian_source) != result['guardian_source_sha256'] or staged_inventory(work, args.release, args.staged_state_sha256) != (state, rows) or incomplete_authoritative_references(work, state) or staged_current_observation() != observed:
                raise RuntimeError('STAGED evidence or authoritative owner references changed')
        unchanged()
        if args.execute:
            history_directory(history_root())
            history_directory(archive.parent)
            archive.mkdir(mode=0o700)  # Never overwrite any completed or partial attempt.
            history_sync(archive.parent)
            shutil.copytree(work, archive / 'copy', symlinks=True)
            if staged_inventory(archive / 'copy', args.release, args.staged_state_sha256) != (state, rows):
                raise RuntimeError('full STAGED archive readback mismatch')
            for path in (archive / 'copy').rglob('*'):
                if path.is_file() and not path.is_symlink():
                    with path.open('rb') as copied:
                        os.fsync(copied.fileno())
            for path in sorted((archive / 'copy').rglob('*'), reverse=True):
                if path.is_dir() and not path.is_symlink():
                    history_sync(path)
            history_sync(archive / 'copy')
            body = json.dumps(result, sort_keys=True).encode() + b'\n'
            immutable_write(archive / 'intent.json', body, 0o600)
            history_sync(archive)
            unchanged()
            work.rename(archive / 'original')
            history_sync(ROOT)
            history_sync(archive)
            # Keep the old incomplete validator strict: only the real rename
            # restores its original ROOT/ID-absent invariant. Never ignore ID.
            verify_incomplete_archives()
            verify_locks()
            for name in ('copy', 'original'):
                if staged_inventory(archive / name, args.release, args.staged_state_sha256) != (state, rows):
                    raise RuntimeError('full STAGED archive changed before completion')
            if staged_current_observation() != observed:
                raise RuntimeError('current owner changed during STAGED archival')
            immutable_write(archive / 'receipt.json', body, 0o600)
            history_sync(archive)
            verify_staged_archives()
    return result


def cleanup_terminal_time(work, state, now):
    value = state.get('terminal_at', state.get('completed_at'))
    if value is None and state.get('ready_at') is not None:
        ready = state['ready_at']
        if not isinstance(ready, (int, float)) or isinstance(ready, bool) or not math.isfinite(ready) or ready <= 0:
            return None
        # Legacy confirmation states have both readiness evidence and the final
        # state write; mtime alone never establishes a terminal transition.
        value = max((work / 'state.json').stat().st_mtime, ready + 120)
    if not isinstance(value, (int, float)) or isinstance(value, bool) or not math.isfinite(value) or value <= 0 or value > now:
        return None
    return value


def cleanup_payload_inventory(work, uid=0):
    entries, total = [], 0
    for name in CLEANUP_PAYLOADS:
        path = work / name
        if not path.exists() and not path.is_symlink():
            continue
        if name == 'lmm-api':
            info = path.lstat()
            if not stat.S_ISLNK(info.st_mode) or os.readlink(path) != 'lmm-api-go' or info.st_uid != uid:
                raise RuntimeError('historical lmm-api is not the fixed provider symlink')
            paths = [path]
        else:
            if path.is_symlink() or (name in ('lmm-api-go', 'previous-binary') and not path.is_file()) or (name in ('frontend', 'tmp', 'cache') and not path.is_dir()):
                raise RuntimeError('historical cleanup payload has an unexpected type')
            paths = [path, *sorted(path.rglob('*'))] if path.is_dir() else [path]
        for item in paths:
            info = item.lstat()
            if item != work / 'lmm-api' and (not (stat.S_ISREG(info.st_mode) or stat.S_ISDIR(info.st_mode)) or info.st_uid != uid or info.st_mode & 0o022 or (stat.S_ISREG(info.st_mode) and info.st_nlink != 1)):
                raise RuntimeError('historical cleanup payload ownership, links or permissions are unsafe')
            entries.append((str(item.relative_to(work)), info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns, info.st_mode, info.st_nlink))
            if stat.S_ISREG(info.st_mode):
                total += info.st_size
    signature = hashlib.sha256(json.dumps(entries).encode()).hexdigest()
    return {'payloads': [name for name in CLEANUP_PAYLOADS if (work / name).exists() or (work / name).is_symlink()], 'bytes': total, 'inventory_sha256': signature}


def cleanup_strings(value):
    if isinstance(value, str):
        yield value
    elif isinstance(value, dict):
        for item in value.values():
            yield from cleanup_strings(item)
    elif isinstance(value, list):
        for item in value:
            yield from cleanup_strings(item)


def path_in_payload(value, work):
    if not isinstance(value, str) or not value.startswith('/'):
        return False
    path = Path(os.path.normpath(value.removesuffix(' (deleted)')))
    return any(path.is_relative_to(work / name) for name in CLEANUP_PAYLOADS)


def cleanup_process_references(work, proc=Path('/proc'), entire_workspace=False):
    def matches(value):
        if entire_workspace:
            return value.startswith('/') and Path(os.path.normpath(value.removesuffix(' (deleted)'))).is_relative_to(work)
        return path_in_payload(value, work)
    found = []
    for process in proc.iterdir():
        if not process.name.isdigit():
            continue
        try:
            links = [process / 'exe', process / 'cwd', *list((process / 'fd').iterdir())]
            for link in links:
                try:
                    target = os.readlink(link)
                except FileNotFoundError:
                    continue  # Process/fd disappeared during the inspection.
                if matches(target):
                    found.append({'pid': int(process.name), 'kind': str(link.relative_to(process)), 'path': target})
            for line in (process / 'maps').read_text().splitlines():
                fields = line.split(maxsplit=5)
                if len(fields) == 6 and matches(fields[5]):
                    found.append({'pid': int(process.name), 'kind': 'maps', 'path': fields[5]})
        except (FileNotFoundError, ProcessLookupError):
            continue
        except (PermissionError, OSError):
            raise RuntimeError('cannot completely inspect live process references; cleanup refused')
    return found


def maintenance_transfer_record(previous, next_state, next_handoff, maintenance):
    if previous['phase'] != 'FROZEN' or previous.get('capture_receipt_sha256') != maintenance.get('capture_receipt_sha256') or previous.get('capture_receipt_path') != maintenance.get('capture_receipt_path'):
        raise RuntimeError('maintenance transfer requires the direct previous owner FROZEN capture')
    source = guardian.handoff(previous['maintenance_handoff']['path'], previous['maintenance_handoff']['sha256'])
    if any(source.get(key) != maintenance.get(key) for key in ('transition_id', 'transition_intent_sha256', 'provider_sha256', 'prepare_config_sha256')):
        raise RuntimeError('maintenance transfer crosses a frozen transition identity')
    captured = json.loads(guardian.bound_file(previous['capture_receipt_path'], previous['capture_receipt_sha256']))
    if captured.get('format') != 'lmm-credit-maintenance-capture-v1' or captured.get('phase') != 'FROZEN' or any(captured.get(key) != maintenance[key] for key in ('transition_id', 'transition_intent_sha256')):
        raise RuntimeError('maintenance transfer capture receipt is not the previous owner evidence')
    if maintenance['stage'] == 'post' and (previous.get('maintenance_confirmation') is not True or captured.get('was_maintenance_confirmed') is not True or captured.get('provider_sha256') != maintenance['provider_sha256']):
        raise RuntimeError('post maintenance transfer requires the confirmed installed bridge capture')
    return {'format': 'lmm-credit-maintenance-transfer-v1',
            **{key: maintenance[key] for key in ('transition_id', 'transition_intent_sha256', 'provider_sha256', 'prepare_config_sha256')},
            'previous_deployment_id': previous['release'], 'next_deployment_id': next_state['release'],
            'next_handoff_sha256': next_handoff['sha256'],
            'capture_receipt_sha256': previous['capture_receipt_sha256']}


def transfer_record_body(record):
    return json.dumps(record, indent=2).encode() + b'\n'


def maintenance_ancestor_chain(start, maintenance):
    ancestors, visited = set(), {start}
    cleanup_path(ROOT / start)
    cleanup_path(ROOT / start / 'state.json', private_file=True)
    next_state = read_state(ROOT / start)
    while True:
        binding = next_state.get('maintenance_handoff', {})
        next_maintenance = guardian.handoff(binding.get('path', ''), binding.get('sha256', ''))
        if any(next_maintenance.get(key) != maintenance.get(key) for key in ('transition_id', 'transition_intent_sha256', 'provider_sha256', 'prepare_config_sha256')):
            raise RuntimeError('maintenance ancestor chain crosses the frozen transition identity')
        previous_id = next_maintenance.get('previous_deployment_id')
        if not previous_id:
            return ancestors
        if not re.fullmatch(RELEASE_PATTERN, previous_id) or previous_id in visited:
            raise RuntimeError('maintenance ancestor chain has an invalid or repeated owner')
        visited.add(previous_id)
        cleanup_path(ROOT / previous_id)
        cleanup_path(ROOT / previous_id / 'state.json', private_file=True)
        previous = read_state(ROOT / previous_id)
        expected = maintenance_transfer_record(previous, next_state, binding, next_maintenance)
        path = ROOT / previous_id / ('maintenance-transfer.' + next_state['release'] + '.json')
        body = transfer_record_body(expected)
        if guardian.bound_file(path, hashlib.sha256(body).hexdigest()) != body:
            raise RuntimeError('maintenance ancestor has no exact immutable owner transfer record')
        ancestors.add(previous_id)
        next_state = previous


def write_maintenance_transfer(previous, next_state, maintenance):
    binding = next_state['maintenance_handoff']
    for owner in (previous['release'], next_state['release']):
        cleanup_path(ROOT / owner)
        cleanup_path(ROOT / owner / 'state.json', private_file=True)
    if maintenance.get('previous_deployment_id') != previous['release']:
        raise RuntimeError('maintenance transfer does not name the direct previous owner')
    body = transfer_record_body(maintenance_transfer_record(previous, next_state, binding, maintenance))
    path = ROOT / previous['release'] / ('maintenance-transfer.' + next_state['release'] + '.json')
    if path.exists() or path.is_symlink():
        if guardian.bound_file(path, hashlib.sha256(body).hexdigest()) != body:
            raise RuntimeError('existing maintenance transfer record differs')
    else:
        immutable_write(path, body, 0o600)


def cleanup_history(args, now, transferred=None):
    transferred = transferred or set()
    states, evidence, protected, candidates, blockers = {}, {}, [], [], []
    for work in sorted(ROOT.iterdir()):
        if not work.is_dir() and not work.is_symlink():
            continue
        if work.name in (args.release, args.retain_rollback):
            reason = 'current release' if work.name == args.release else 'bridge rollback release'
            protected.append({'release': work.name, 'reason': reason})
        try:
            if not re.fullmatch(RELEASE_PATTERN, work.name):
                raise RuntimeError('unknown workspace name')
            cleanup_path(work)
            cleanup_path(work / 'state.json', private_file=True)
            states[work] = read_state(work)
            # Owner evidence is retained. Inspect all JSON evidence outside the
            # removable payloads for cross-workspace references.
            values = []
            for base, directories, files in os.walk(work, followlinks=False):
                if Path(base) == work:
                    directories[:] = [name for name in directories if name not in CLEANUP_PAYLOADS]
                if any((Path(base) / name).is_symlink() for name in directories):
                    raise RuntimeError('retained evidence contains an unknown directory link')
                for name in files:
                    if name.endswith('.json'):
                        path = Path(base) / name
                        cleanup_path(path, private_file=True)
                        if path.stat().st_size > 4 * 1024 * 1024:
                            raise RuntimeError('retained JSON evidence is too large to inspect safely')
                        values.append(json.loads(path.read_text()))
            frozen = states[work].get('maintenance_handoff')
            if frozen:
                values.append(json.loads(guardian.bound_file(frozen['path'], frozen['sha256'])))
            evidence[work] = values
        except (OSError, RuntimeError, ValueError, KeyError) as error:
            protected.append({'release': work.name, 'reason': 'unverified state or retained evidence: ' + str(error)})
            blockers.append(work.name)
    for work, state in states.items():
        if work.name in (args.release, args.retain_rollback):
            continue
        if state['phase'] not in ('CONFIRMED', 'ROLLED_BACK'):
            if state['phase'] == 'FROZEN' and work.name in transferred:
                protected.append({'release': work.name, 'reason': 'formally transferred frozen ancestor; complete evidence and payload retained'})
                continue
            protected.append({'release': work.name, 'reason': 'non-terminal owner state'})
            blockers.append(work.name)
            continue
        terminal = cleanup_terminal_time(work, state, now)
        if terminal is None or now - terminal < args.older_than:
            protected.append({'release': work.name, 'reason': 'terminal age is not reliably at least the requested retention'})
            continue
        try:
            inventory = cleanup_payload_inventory(work)
            refs = []
            for owner, values in evidence.items():
                if owner == work:
                    continue
                for value in cleanup_strings(values):
                    paths = (value,) if value.startswith('/') else (str(owner / value), str(ROOT / value))
                    if any(path_in_payload(path, work) for path in paths):
                        refs.append(owner.name)
            if refs:
                protected.append({'release': work.name, 'reason': 'payload is referenced by another workspace', 'references': sorted(set(refs))})
                continue
            processes = cleanup_process_references(work)
            if processes:
                protected.append({'release': work.name, 'reason': 'payload has live process references', 'references': processes})
                continue
            candidates.append({'release': work.name, 'terminal_at': terminal, **inventory})
        except (OSError, RuntimeError, ValueError) as error:
            protected.append({'release': work.name, 'reason': str(error)})
            if 'completely inspect live process' in str(error):
                blockers.append(work.name)
    return {'candidates': candidates, 'protected': protected, 'blocked_by': sorted(set(blockers))}


def cleanup(work, args, maintenance, guardian_receipt):
    if args.older_than < 86400:
        raise RuntimeError('cleanup retention must be at least 86400 seconds')
    verify_cleanup_guardian(guardian_receipt)
    current, bridge = verify_cleanup_protected(work, args, maintenance)
    archive = verify_financial_archive(args.financial_backup, args.financial_backup_sha256)
    financial_receipt = verify_financial_backup_receipt(args, maintenance, archive)
    transferred = maintenance_ancestor_chain(work.name, maintenance)
    now = time.time()
    plan = cleanup_history(args, now, transferred)
    result = {'format': 'lmm-credit-maintenance-cleanup-v1', 'release': work.name,
              'phase': current['phase'], 'transition_id': maintenance['transition_id'],
              'transition_intent_sha256': maintenance['transition_intent_sha256'],
              'retain_rollback': bridge['release'], 'financial_backup': archive,
              'financial_backup_receipt': financial_receipt,
              'dry_run': not args.execute, 'older_than': args.older_than, **plan}
    if args.execute:
        if plan['blocked_by']:
            raise RuntimeError('cleanup refused because other owner states or retained evidence are unverified: ' + ', '.join(plan['blocked_by']))
        verify_cleanup_guardian(guardian_receipt)
        verify_cleanup_protected(work, args, maintenance)
        if verify_financial_archive(args.financial_backup, args.financial_backup_sha256) != archive or verify_financial_backup_receipt(args, maintenance, archive) != financial_receipt or maintenance_ancestor_chain(work.name, maintenance) != transferred or cleanup_history(args, now, transferred) != plan:
            raise RuntimeError('cleanup proof changed before removal; no payload was removed')
        filesystem = os.statvfs(ROOT)
        free_before = filesystem.f_bavail * filesystem.f_frsize
        for candidate in plan['candidates']:
            historical = ROOT / candidate['release']
            for name in candidate['payloads']:
                path = historical / name
                if path.is_symlink() or path.is_file():
                    path.unlink()
                else:
                    shutil.rmtree(path)
        filesystem = os.statvfs(ROOT)
        free_after = filesystem.f_bavail * filesystem.f_frsize
        result.update(deleted_payload_bytes=sum(item['bytes'] for item in plan['candidates']),
                      filesystem_available_bytes_before=free_before, filesystem_available_bytes_after=free_after)
    return result


def doctor(migrate=False):
    checks = []

    def check(name, operation):
        try:
            operation()
            checks.append({'name': name, 'ok': True})
        except (OSError, RuntimeError, ValueError) as error:
            checks.append({'name': name, 'ok': False, 'error': str(error)})

    def service_config():
        for path in service_environment_files():
            if not path.startswith('-') and not Path(path).is_file():
                raise RuntimeError('required service environment file is missing')
        if not ENVIRONMENT.is_file():
            raise RuntimeError(f'missing environment file: {ENVIRONMENT}')
        if property_value('MainPID') == '0':
            raise RuntimeError('service is not running; inspect its journal before upgrading')

    def frontend_layout():
        target = os.readlink(FRONTEND / 'current')
        if not re.fullmatch(r'releases/[A-Za-z0-9][A-Za-z0-9._-]{0,127}', target):
            raise RuntimeError('invalid current frontend link')
        if not (FRONTEND / target / 'index.html').is_file():
            raise RuntimeError('current frontend is missing index.html')

    def transactions():
        verify_incomplete_archives()
        verify_staged_archives()
        registered = released_ancestors()
        pending = [state['release'] for state in read_status()['deployments']
                   if state['phase'] not in ('STAGED', 'CONFIRMED', 'ROLLED_BACK')
                   and not (state['phase'] == 'FROZEN' and state['release'] in registered)]
        if pending:
            raise RuntimeError('inspect unfinished transactions: ' + ', '.join(pending))

    check('Required commands', lambda: check_tools(migrate))
    check('Standalone Go installation (not package-owned)', check_layout)
    check('Service environment and running process', service_config)
    check('Current frontend', frontend_layout)
    check('Transaction history', transactions)
    return {'ok': all(item['ok'] for item in checks), 'checks': checks}


def command(action, release, migrate=False):
    args = ['python3', str(Path(__file__).resolve()), action, '--release', release]
    if migrate:
        args.append('--migrate')
    if action in ('apply', 'confirm', 'rollback'):
        args += ['--confirm', 'api.lmm.best']
    return shlex.join(args)


def show(result, human=False, output=None):
    output = output or sys.stdout
    if not human:
        print(json.dumps(result), file=output)
        return
    if 'checks' in result:
        for item in result['checks']:
            print(f"{'OK' if item['ok'] else 'FAIL'}  {item['name']}" +
                  (': ' + item['error'] if not item['ok'] else ''), file=output)
        print('Prerequisites only; artifact, schema and health verification still run during upgrade.', file=output)
        return
    if 'deployments' in result:
        print('RELEASE  PHASE  CANDIDATE', file=output)
        for state in result['deployments']:
            print(f"{state['release']}  {state['phase']}  {state.get('version', '-')}", file=output)
        if not result['deployments']:
            print('No deployment transactions recorded.', file=output)
        return
    release, phase = result['release'], result['phase']
    print(f'{release}  {phase}', file=output)
    if result.get('version'):
        print(f"Candidate: {result['version']}  Previous: {result.get('previous_version', '-')}", file=output)
    print(f'Evidence: {ROOT / release}', file=output)
    if phase == 'STAGED':
        print('Prepared only; the running service has not been changed.', file=output)
        print('Next: ' + command('apply', release, result.get('migrate', False)), file=output)
    elif phase == 'AWAITING_CONFIRMATION':
        remaining = max(0, int(120 - (time.time() - result['ready_at']) + 0.999))
        print(f'Check the public site and authenticated flows; observe for at least {remaining}s more.', file=output)
        print('Then: ' + command('confirm', release), file=output)
        print('Recovery: ' + command('rollback', release), file=output)
    elif phase == 'MUTATION_PENDING':
        print('Inspect private logs before recovery. Do not repeat upgrade/apply.', file=output)
        print('Recovery: ' + command('rollback', release), file=output)
    elif phase == 'PREPARATION_INCOMPLETE':
        print('Preparation did not complete. Inspect this directory; it will not be overwritten.', file=output)
    if result.get('migrate'):
        print('Software rollback does not restore the database.', file=output)


def progress(args, message):
    if args.human:
        print(message, file=sys.stderr, flush=True)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['doctor', 'upgrade', 'stage', 'apply', 'status', 'confirm', 'rollback', 'cleanup', 'maintenance-release', 'maintenance-capture', 'maintenance-close', 'maintenance-stop', 'register-released-history', 'archive-incomplete', 'archive-staged'])
    parser.add_argument('--release', help='explicit transaction ID; omit for status to list all transactions')
    parser.add_argument('--released-controller', type=Path)
    parser.add_argument('--released-controller-sha256')
    parser.add_argument('--later-provider', type=Path, help='history registration: exact formally confirmed later-provider qualification inputs')
    parser.add_argument('--later-provider-sha256')
    parser.add_argument('--staged-state-sha256')
    parser.add_argument('--owner-source-sha256')
    parser.add_argument('--binary', type=Path)
    parser.add_argument('--frontend', type=Path)
    parser.add_argument('--confirm')
    parser.add_argument('--maintenance-handoff', type=Path)
    parser.add_argument('--maintenance-handoff-sha256')
    parser.add_argument('--global-confirmation', type=Path)
    parser.add_argument('--global-confirmation-sha256')
    parser.add_argument('--all-admission-closed', type=Path)
    parser.add_argument('--all-admission-closed-sha256')
    parser.add_argument('--superseded-by')
    parser.add_argument('--retain-rollback')
    parser.add_argument('--financial-backup', type=Path)
    parser.add_argument('--financial-backup-sha256')
    parser.add_argument('--financial-backup-receipt', type=Path)
    parser.add_argument('--financial-backup-receipt-sha256')
    parser.add_argument('--older-than', type=int, default=86400)
    parser.add_argument('--execute', action='store_true', help='cleanup: remove only proved historical payloads; the default is a dry run')
    parser.add_argument('--wait', action='store_true', help='confirm: observe healthy candidate until the existing 120-second deadline')
    parser.add_argument('--migrate', action='store_true', help='back up PostgreSQL and apply schema migrations after stopping the writer')
    parser.add_argument('--backup-exclude-table', action='append', default=[], help='exact schema.table of an unrelated archive table to exclude; recorded in the plan')
    output = parser.add_mutually_exclusive_group()
    output.add_argument('--json', action='store_true', help='machine-readable output without progress messages')
    output.add_argument('--human', action='store_true', help='readable status and next commands')
    args = parser.parse_args(argv)
    if args.wait and args.action != 'confirm':
        parser.error('--wait is only valid for confirm')
    if args.release is not None and not re.fullmatch(RELEASE_PATTERN, args.release):
        parser.error('invalid release ID')
    if args.action not in ('doctor', 'status') and not args.release:
        parser.error('--release is required for this action')
    if args.action in ('upgrade', 'apply', 'confirm', 'rollback', 'cleanup', 'maintenance-release', 'maintenance-capture', 'maintenance-close', 'maintenance-stop', 'register-released-history', 'archive-incomplete', 'archive-staged') and args.confirm != 'api.lmm.best':
        parser.error('mutations require --confirm api.lmm.best')
    if args.action not in ('stage', 'upgrade') and (args.binary or args.frontend or args.backup_exclude_table):
        parser.error('artifact and backup-exclusion arguments are only valid for stage/upgrade')
    if args.backup_exclude_table and not args.migrate:
        parser.error('--backup-exclude-table requires --migrate')
    if args.migrate and args.action not in ('doctor', 'stage', 'upgrade', 'apply'):
        parser.error('--migrate is only valid for doctor/stage/upgrade/apply')
    if bool(args.maintenance_handoff) != bool(args.maintenance_handoff_sha256):
        parser.error('maintenance handoff path and digest must be supplied together')
    if args.action == 'maintenance-release' and not (args.global_confirmation and args.global_confirmation_sha256):
        parser.error('maintenance release requires exact global owner confirmation receipt')
    if args.action == 'status' and args.maintenance_handoff and not args.release:
        parser.error('handoff refinement status requires an explicit release')
    if args.action == 'cleanup':
        if not args.superseded_by or not args.retain_rollback or not args.financial_backup or not args.financial_backup_sha256 or not args.financial_backup_receipt or not args.financial_backup_receipt_sha256:
            parser.error('cleanup requires current supersession, retained bridge rollback and a sealed financial backup')
        if not re.fullmatch(RELEASE_PATTERN, args.superseded_by) or not re.fullmatch(RELEASE_PATTERN, args.retain_rollback) or args.older_than < 86400:
            parser.error('cleanup requires valid protected release IDs and at least 86400 seconds retention')
    elif (args.execute and args.action not in ('register-released-history', 'archive-incomplete', 'archive-staged')) or args.superseded_by or args.retain_rollback or args.financial_backup or args.financial_backup_sha256 or args.financial_backup_receipt or args.financial_backup_receipt_sha256 or args.older_than != 86400:
        parser.error('cleanup arguments are only valid for cleanup')
    if args.action == 'register-released-history':
        if not (args.released_controller and args.released_controller_sha256 and args.global_confirmation and args.global_confirmation_sha256):
            parser.error('history registration requires exact RELEASED controller and all-node receipt')
        if bool(args.later_provider) != bool(args.later_provider_sha256):
            parser.error('later-provider path and digest must be supplied together')
    elif args.released_controller or args.released_controller_sha256:
        parser.error('released controller is only valid for history registration')
    if args.action != 'register-released-history' and (args.later_provider or args.later_provider_sha256):
        parser.error('later-provider is only valid for history registration')
    if args.action in ('register-released-history', 'archive-incomplete', 'archive-staged') and args.maintenance_handoff:
        parser.error('ordinary history commands cannot adopt a maintenance guardian')
    if args.action == 'archive-staged':
        if any(not re.fullmatch(r'[0-9a-f]{64}', value or '') for value in (args.staged_state_sha256, args.owner_source_sha256)):
            parser.error('archive-staged requires exact owner source and original STAGED state digests')
        if args.global_confirmation or args.global_confirmation_sha256 or args.all_admission_closed or args.all_admission_closed_sha256:
            parser.error('archive-staged cannot consume financial admission or confirmation receipts')
    elif args.staged_state_sha256 or args.owner_source_sha256:
        parser.error('STAGED state and owner source digests are only valid for archive-staged')
    if os.geteuid() != 0:
        parser.error('run on the target as root')
    # Preserve JSON for existing non-interactive granular commands.
    args.human = args.human or (not args.json and (sys.stdout.isatty() or
                  args.action in ('doctor', 'upgrade') or not args.release))
    try:
        if args.action in ('register-released-history', 'archive-incomplete', 'archive-staged'):
            operation = {'register-released-history': register_released_history, 'archive-incomplete': archive_incomplete, 'archive-staged': archive_staged}[args.action]
            show(operation(args), args.human)
            return 0
        if args.action == 'doctor':
            result = doctor(args.migrate)
            show(result, args.human)
            return 0 if result['ok'] else 1
        if args.action == 'status':
            show(read_status(args.release, args.maintenance_handoff, args.maintenance_handoff_sha256), args.human)
            return 0
        execute(args, parser)
        return 0
    except (Exception, KeyboardInterrupt) as error:
        message = 'interrupted; inspect transaction status before retrying' if isinstance(error, KeyboardInterrupt) else str(error)
        result = {'ok': False, 'error': message}
        if args.release:
            result['status_command'] = command('status', args.release)
        if args.json:
            show(result)
        else:
            print('deploy-systemd: ' + message, file=sys.stderr)
            if args.release:
                print('Inspect: ' + result['status_command'], file=sys.stderr)
                try:
                    show(read_status(args.release), True, sys.stderr)
                except (OSError, RuntimeError, ValueError):
                    pass  # Missing/corrupt state is never guessed or repaired.
        return 130 if isinstance(error, KeyboardInterrupt) else 1


def execute(args, parser):
    os.umask(0o077)
    if ROOT.is_symlink():
        raise RuntimeError('deployment root may not be a symlink')
    maintenance = None
    handoff_sha = args.maintenance_handoff_sha256
    if args.maintenance_handoff:
        maintenance = guardian.handoff(args.maintenance_handoff, handoff_sha)
    elif args.action not in ('stage', 'upgrade'):
        existing = read_state(ROOT / args.release)
        if existing.get('maintenance_handoff'):
            frozen = existing['maintenance_handoff']
            handoff_sha = frozen['sha256']
            maintenance = guardian.handoff(frozen['path'], handoff_sha)
    if maintenance and maintenance['deployment_tool'] != 'systemd':
        raise RuntimeError('maintenance handoff belongs to a different deployment tool')
    if maintenance:
        validate_post_staging_intent(maintenance)
    if maintenance and maintenance['stage'] == 'post' and not maintenance.get('stopped_writer') and args.action != 'stage':
        raise RuntimeError('unstopped post staging intent only authorizes stage/status; frozen owner handoff is required')
    if maintenance and maintenance['stage'] == 'prebridge' and args.action in ('stage', 'upgrade', 'apply', 'maintenance-capture'):
        validate_prepare_service_reader(maintenance)
    if args.action == 'cleanup' and not maintenance:
        raise RuntimeError('financial cleanup requires the current bound maintenance guardian')
    ROOT.mkdir(mode=0o700, exist_ok=True)
    with deployment_lock(maintenance, handoff_sha, all_locks=args.action == 'cleanup') as guardian_receipt:
        work = ROOT / args.release
        if args.action == 'cleanup':
            check_layout()
            show(cleanup(work, args, maintenance, guardian_receipt), args.human)
            return
        if args.action in ('stage', 'upgrade'):
            progress(args, 'Checking prerequisites and preparing immutable artifacts...')
            reject_archived_release_id(args.release)
            check_tools(args.migrate)
            check_layout()
            if work.exists() or work.is_symlink():
                raise RuntimeError('release already exists; inspect status and use the explicit next action')
            if not args.binary or not args.frontend or not (args.frontend / 'index.html').is_file():
                parser.error('stage/upgrade requires --binary and --frontend with index.html')
            if args.binary.is_symlink() or not args.binary.is_file() or args.frontend.is_symlink():
                raise RuntimeError('artifacts must be a regular binary and a real frontend directory')
            if any(p.is_symlink() for p in args.frontend.rglob('*')):
                raise RuntimeError('frontend may not contain symlinks')
            work.mkdir(mode=0o700)
            shutil.copy2(args.binary, work / 'lmm-api-go')
            (work / 'lmm-api-go').chmod(0o755)
            (work / 'lmm-api').symlink_to('lmm-api-go')
            shutil.copytree(args.frontend, work / 'frontend')
            version = run(str(work / 'lmm-api'), 'version')
            run(str(work / 'lmm-api'), 'operator', 'help')
            if args.migrate:
                if not (maintenance and maintenance['stage'] == 'post' and not maintenance.get('stopped_writer')):
                    db_env = verify_stopped_maintenance(maintenance) if maintenance and maintenance.get('stopped_writer') else database_environment()
                    backup(work, db_env, schema_only=True, exclude=args.backup_exclude_table)
            elif not maintenance:
                verify(work, 'stage')
            state = {'release': args.release, 'version': version, 'sha256': digest(work / 'lmm-api-go'),
                     'frontend_sha256': tree_digest(work / 'frontend'), 'migrate': args.migrate,
                     'backup_exclude_tables': args.backup_exclude_table, 'phase': 'STAGED'}
            if maintenance:
                if tree_digest(FRONTEND / 'current') != state['frontend_sha256']:
                    raise RuntimeError('maintenance frontend must exactly match the frozen active frontend tree')
                state['maintenance_handoff'] = {'path': str(args.maintenance_handoff), 'sha256': handoff_sha}
                if maintenance['stage'] == 'post' and not maintenance.get('stopped_writer'):
                    state['maintenance_staging_intent'] = dict(state['maintenance_handoff'])
                state.update(maintenance_stage=maintenance['stage'], transition_id=maintenance['transition_id'],
                             transition_intent_sha256=maintenance['transition_intent_sha256'], provider_sha256=maintenance['provider_sha256'],
                             prepare_config_sha256=maintenance['prepare_config_sha256'])
                if digest(work / 'lmm-api-go') != maintenance['provider_sha256']:
                    raise RuntimeError('candidate provider differs from frozen maintenance identity')
            save(work, state)
        if args.action != 'stage':
            state = read_state(work)
            if maintenance:
                frozen = state.get('maintenance_handoff', {})
                if frozen and (frozen['path'], frozen['sha256']) != (maintenance['_handoff_path'], handoff_sha):
                    if args.action != 'apply' or state['phase'] != 'STAGED':
                        raise RuntimeError('changed owner handoff only permits the official staged post refinement apply')
                    base = state.get('maintenance_staging_intent', frozen)
                    post_staging_refinement(base, maintenance['_handoff_path'], handoff_sha)
                    verify_stopped_maintenance(maintenance)
                    state['maintenance_staging_intent'] = base
                    state['maintenance_handoff'] = {'path': maintenance['_handoff_path'], 'sha256': handoff_sha}
                state.update(maintenance_stage=maintenance['stage'], transition_id=maintenance['transition_id'], transition_intent_sha256=maintenance['transition_intent_sha256'], provider_sha256=maintenance['provider_sha256'], prepare_config_sha256=maintenance['prepare_config_sha256'])
            check_layout()
            if digest(work / 'lmm-api-go') != state['sha256']:
                raise RuntimeError('staged binary changed')
            if tree_digest(work / 'frontend') != state['frontend_sha256']:
                raise RuntimeError('staged frontend changed')
            if args.action == 'maintenance-capture':
                if not maintenance or maintenance['stage'] != 'prebridge' or maintenance.get('stopped_writer') or state['phase'] != 'STAGED':
                    raise RuntimeError('maintenance capture requires live ordinary STAGED owner')
                healthy(run(str(ENTRY), 'version'))
                pid, invocation = property_value('MainPID'), property_value('InvocationID')
                if not pid.isdecimal() or int(pid) <= 1 or len(invocation) != 32:
                    raise RuntimeError('ordinary writer capture identity unavailable')
                process_env = Path('/proc', pid, 'environ').read_bytes()
                process_path = work / ('captured-process.' + invocation + '.environment')
                immutable_write(process_path, process_env, 0o600)
                shutil.copy2(BINARY, work / 'previous-binary')
                shutil.copy2(ENVIRONMENT, work / 'previous.env')
                (work / 'previous.env').chmod(0o600)
                db_env = database_environment_from_values(dict(item.split(b'=', 1) for item in process_env.split(b'\0') if b'=' in item))
                verify_maintenance_database(maintenance, db_env, False)
                if digest(Path('/proc', pid, 'exe')) != digest(BINARY) or property_value('MainPID') != pid or property_value('InvocationID') != invocation:
                    raise RuntimeError('captured ordinary writer executable or generation changed')
                archived = database_environment_from_values(read_environment_file(work / 'previous.env'))
                if db_env != archived:
                    raise RuntimeError('captured process database differs from archived configuration')
                state.update(previous_version=run(str(ENTRY), 'version'), previous_sha256=digest(BINARY), previous_frontend=os.readlink(FRONTEND / 'current').split('/')[1], captured_pid=int(pid), captured_invocation_id=invocation, process_environment_path=str(process_path), process_environment_sha256=digest(process_path), archived_environment_sha256=digest(work / 'previous.env'), phase='CAPTURED')
                persist_capture(work, state, maintenance)
            elif args.action == 'maintenance-close':
                if not maintenance or state['phase'] != 'CAPTURED' or property_value('MainPID') != str(state['captured_pid']) or property_value('InvocationID') != state['captured_invocation_id']:
                    raise RuntimeError('admission close requires the captured ordinary writer generation')
                healthy(state['previous_version'])
                close_maintenance_admission(work, state, maintenance)
                state['phase'] = 'ADMISSION_CLOSED'
                persist_capture(work, state, maintenance)
            elif args.action == 'maintenance-stop':
                if not maintenance or state['phase'] not in ('ADMISSION_CLOSED', 'MAINTENANCE_CONFIRMED'):
                    raise RuntimeError('writer stop requires closed captured admission')
                receipt = json.loads(guardian.bound_file(args.all_admission_closed, args.all_admission_closed_sha256))
                if receipt.get('format') != 'lmm-credit-all-admission-closed-v1' or receipt.get('transition_id') != maintenance['transition_id'] or receipt.get('transition_intent_sha256') != maintenance['transition_intent_sha256'] or receipt.get('all_origins_closed') is not True:
                    raise RuntimeError('all-origin admission closure receipt differs from this transition')
                maintenance_admission(maintenance)
                prepared = state['phase'] == 'MAINTENANCE_CONFIRMED'
                if prepared:
                    healthy_prepare(state['version'], maintenance)
                    state.update(captured_pid=int(property_value('MainPID')), captured_invocation_id=property_value('InvocationID'), was_maintenance_confirmed=True, previous_version=state['version'], previous_sha256=state['sha256'])
                    process_path = work / ('captured-process.' + state['captured_invocation_id'] + '.environment')
                    immutable_write(process_path, Path('/proc', str(state['captured_pid']), 'environ').read_bytes(), 0o600)
                    state.update(process_environment_path=str(process_path), process_environment_sha256=digest(process_path))
                if property_value('MainPID') != str(state['captured_pid']) or property_value('InvocationID') != state['captured_invocation_id']:
                    raise RuntimeError('captured writer generation changed before stop')
                stop(work, maintenance if prepared else None)
                state['phase'] = 'FROZEN'
                state['shutdown_journal_sha256'] = digest(work / 'shutdown.log')
                persist_capture(work, state, maintenance)
            elif args.action in ('apply', 'upgrade'):
                progress(args, 'Verifying staged plan, schema and rollback inputs...')
                check_tools(args.migrate)
                if state['migrate'] != args.migrate:
                    raise RuntimeError('--migrate must match the immutable staged plan')
                if state['phase'] != 'STAGED':
                    raise RuntimeError('apply requires STAGED; use status or explicit rollback')
                if not maintenance:
                    verify_incomplete_archives()
                    verify_staged_archives()
                    if any(item['phase'] == 'PREPARATION_INCOMPLETE' for item in read_status()['deployments']):
                        raise RuntimeError('incomplete preparation requires protected archival before ordinary apply')
                transferred = set() if maintenance else released_ancestors()
                if maintenance and maintenance.get('stopped_writer'):
                    transferred = maintenance_ancestor_chain(maintenance['previous_deployment_id'], maintenance)
                for other in ROOT.glob('*/state.json'):
                    value = read_state(other.parent)
                    if other.parent != work and value['phase'] not in ('STAGED', 'CONFIRMED', 'ROLLED_BACK') and not (value['phase'] == 'FROZEN' and ((not maintenance and value['release'] in transferred) or (maintenance and maintenance.get('stopped_writer') and (value['release'] == maintenance['previous_deployment_id'] or value['release'] in transferred)))):
                        raise RuntimeError('another deployment needs recovery')
                if maintenance and maintenance.get('stopped_writer'):
                    db_env = verify_stopped_maintenance(maintenance)
                    verify_maintenance_database(maintenance, db_env, maintenance['stage'] == 'post')
                    previous = read_state(ROOT / maintenance['previous_deployment_id'])
                    frozen = guardian.handoff(previous['maintenance_handoff']['path'], previous['maintenance_handoff']['sha256'])
                    if previous['phase'] != 'FROZEN' or any(frozen[key] != maintenance[key] for key in ('transition_id', 'transition_intent_sha256', 'provider_sha256', 'prepare_config_sha256')) or (maintenance['stage'] == 'post' and (digest(BINARY) != maintenance['provider_sha256'] or state['sha256'] != previous['sha256'])):
                        raise RuntimeError('post handoff requires the installed confirmed bridge as true N-1')
                    write_maintenance_transfer(previous, state, maintenance)
                    source = ROOT / previous['release'] / 'previous-nginx-locations'
                    original = guardian.bound_file(source, previous['ingress_original_sha256'])
                    immutable_write(work / 'previous-nginx-locations', original, 0o600)
                    state['ingress_original_sha256'] = previous['ingress_original_sha256']
                if state['migrate']:
                    if not (maintenance and maintenance.get('stopped_writer')):
                        db_env = database_environment()
                    backup(work, db_env, schema_only=True, exclude=state['backup_exclude_tables'])
                elif not maintenance:
                    verify(work, 'apply')
                run('nginx', '-t', log=work / 'nginx.log')
                old_frontend = os.readlink(FRONTEND / 'current')
                if not re.fullmatch(r'releases/[A-Za-z0-9][A-Za-z0-9._-]{0,127}', old_frontend):
                    raise RuntimeError('invalid current frontend link')
                shutil.copy2(BINARY, work / 'previous-binary')
                shutil.copy2(ENVIRONMENT, work / 'previous.env')
                state.update(previous_frontend=old_frontend.split('/')[1], previous_sha256=digest(BINARY),
                             previous_version=run(str(ENTRY), 'version'), phase='MUTATION_PENDING')
                save(work, state)
                progress(args, 'Stopping the writer after preflight; preserving rollback evidence...')
                if maintenance:
                    close_maintenance_admission(work, state, maintenance)
                if maintenance and not maintenance.get('stopped_writer'):
                    raise RuntimeError('maintenance apply requires official all-stopped owner handoff')
                if not maintenance:
                    stop(work)
                if state['migrate']:
                    state['database_backup_sha256'] = backup(work, db_env, exclude=state['backup_exclude_tables'])
                    save(work, state)
                    binding = {'maintenance': maintenance} if maintenance else {}
                    verify(work, 'migrate', mode='apply', **binding)
                    verify(work, 'post-migrate', **binding)
                if maintenance and maintenance['stage'] == 'prebridge':
                    verify(work, 'prepare', mode='apply', maintenance=maintenance)
                    verify(work, 'prepare-verify', maintenance=maintenance)
                install(work / 'lmm-api-go', BINARY)
                if not maintenance:
                    run(str(ENTRY), 'operator', 'frontend', 'publish', '--source', str(work / 'frontend'),
                        '--release', args.release, '--keep', '10', log=work / 'frontend.log')
                elif tree_digest(FRONTEND / 'current') != state['frontend_sha256']:
                    raise RuntimeError('frozen active frontend tree changed')
                if maintenance:
                    configure_maintenance_service(maintenance)
                run('systemctl', 'start', SERVICE, log=work / 'start.log')
                progress(args, 'Waiting for the selected version to pass readiness...')
                deadline = time.monotonic() + 120
                while True:
                    try:
                        healthy(state['version'], maintenance)
                        break
                    except Exception:
                        if time.monotonic() >= deadline:
                            raise RuntimeError('candidate failed readiness; explicit rollback required')
                        time.sleep(2)
                if maintenance:
                    maintenance_admission(maintenance)
                state['phase'] = 'AWAITING_CONFIRMATION'
                state['ready_at'] = time.time()
            elif args.action == 'confirm':
                if state['phase'] != 'AWAITING_CONFIRMATION' and not (maintenance and state['phase'] == 'MAINTENANCE_CONFIRMED'):
                    raise RuntimeError('confirm requires AWAITING_CONFIRMATION')
                while args.wait and time.time() - state['ready_at'] < 120:
                    healthy(state['version'], maintenance)
                    if maintenance:
                        maintenance_admission(maintenance)
                    time.sleep(min(2, max(0, state['ready_at'] + 120 - time.time())))
                if time.time() - state['ready_at'] < 120:
                    raise RuntimeError('observe the candidate for at least 120 seconds before confirming')
                healthy(state['version'], maintenance)
                if digest(BINARY) != state['sha256']:
                    raise RuntimeError('installed binary changed')
                if os.readlink(FRONTEND / 'current') != 'releases/' + (state['previous_frontend'] if maintenance else args.release) or (maintenance and tree_digest(FRONTEND / 'current') != state['frontend_sha256']):
                    raise RuntimeError('active frontend changed; confirmation refused')
                if maintenance:
                    maintenance_admission(maintenance)
                    state['maintenance_confirmation'] = True
                    state['phase'] = 'MAINTENANCE_CONFIRMED'
                else:
                    state['phase'] = 'CONFIRMED'
            elif args.action == 'maintenance-release':
                if not maintenance or maintenance['stage'] != 'post' or state['phase'] != 'MAINTENANCE_CONFIRMED':
                    raise RuntimeError('maintenance release requires post owner confirmation')
                receipt = json.loads(guardian.bound_file(args.global_confirmation, args.global_confirmation_sha256))
                if receipt.get('format') != 'lmm-credit-maintenance-release-v1' or receipt.get('transition_id') != maintenance['transition_id'] or receipt.get('transition_intent_sha256') != maintenance['transition_intent_sha256'] or receipt.get('all_nodes_confirmed') is not True:
                    raise RuntimeError('global owner confirmation does not authorize ingress release')
                healthy(state['version'], maintenance)
                maintenance_admission(maintenance)
                reopen_maintenance_admission(work, state, maintenance)
                try:
                    healthy(state['version'])
                    public = health_json('/api/status', maintenance['public_base_url'])
                    token = guardian.bound_file(maintenance['probe_token_path'], maintenance['probe_token_sha256']).decode().strip()
                    models = health_json('/v1/models', maintenance['public_base_url'], token)
                    if public.get('success') is not True or public.get('ready') is not True or public.get('maintenance') is True or public.get('business_enabled') is False or public.get('data', {}).get('version') != state['version'] or not isinstance(models.get('data'), list):
                        raise RuntimeError('public business gates failed after admission release')
                except BaseException:
                    close_maintenance_admission(work, state, maintenance)
                    state['phase'] = 'ROLLBACK_REQUIRED'
                    save(work, state)
                    raise
                state['phase'] = 'CONFIRMED'
                state['maintenance_admission_reopened'] = True
            else:
                if state['phase'] not in ('MUTATION_PENDING', 'AWAITING_CONFIRMATION', 'MAINTENANCE_CONFIRMED', 'ROLLBACK_REQUIRED'):
                    raise RuntimeError('rollback requires a pending deployment')
                if digest(work / 'previous-binary') != state['previous_sha256']:
                    raise RuntimeError('rollback binary changed')
                if maintenance and maintenance['stage'] == 'post' and (state['previous_sha256'] != maintenance['provider_sha256'] or state['sha256'] != maintenance['provider_sha256'] or state['previous_version'] != state['version']):
                    raise RuntimeError('post rollback must retain the same canonical-compatible bridge provider')
                if property_value('MainPID') != '0':
                    if maintenance:
                        stop(work, maintenance)
                    else:
                        stop(work)
                if not maintenance:
                    run(str(work / 'lmm-api'), 'operator', 'frontend', 'rollback', '--release', state['previous_frontend'],
                        '--keep', '10', log=work / 'rollback-frontend.log')
                elif tree_digest(FRONTEND / 'current') != state['frontend_sha256']:
                    raise RuntimeError('frozen frontend changed before rollback')
                install(work / 'previous-binary', BINARY)
                if maintenance:
                    configure_maintenance_service(maintenance, rollback=True)
                run('systemctl', 'start', SERVICE, log=work / 'rollback-start.log')
                progress(args, 'Waiting for the selected version to pass readiness...')
                deadline = time.monotonic() + 120
                while True:
                    try:
                        healthy(state['previous_version'], maintenance if maintenance and maintenance['stage'] == 'post' else None)
                        if maintenance:
                            maintenance_admission(maintenance)
                        break
                    except Exception:
                        if time.monotonic() >= deadline:
                            raise RuntimeError('rollback failed readiness; recovery remains pending')
                        time.sleep(2)
                if maintenance and maintenance['stage'] == 'post':
                    state.update(phase='AWAITING_CONFIRMATION', ready_at=time.time(), maintenance_confirmation=False)
                    save(work, state)
                    try:
                        while True:
                            healthy(state['version'], maintenance)
                            maintenance_admission(maintenance)
                            if digest(BINARY) != maintenance['provider_sha256'] or os.readlink(FRONTEND / 'current') != 'releases/' + state['previous_frontend'] or tree_digest(FRONTEND / 'current') != state['frontend_sha256']:
                                raise RuntimeError('post rollback identity or frozen frontend changed during observation')
                            remaining = state['ready_at'] + 120 - time.time()
                            if remaining <= 0:
                                break
                            time.sleep(min(2, remaining))
                    except BaseException:
                        state['phase'] = 'ROLLBACK_REQUIRED'
                        save(work, state)
                        raise
                    state.update(phase='MAINTENANCE_CONFIRMED', maintenance_confirmation=True)
                else:
                    state['phase'] = 'ROLLED_BACK'
            save(work, state)
        show(state, args.human)


if __name__ == '__main__':
    sys.exit(main())
