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
import urllib.request
import urllib.error
import urllib.parse

ROOT = Path('/var/lib/lmm-api-deploy-systemd')
BINARY = Path('/usr/bin/lmm-api-go')
ENTRY = Path('/usr/bin/lmm-api')
FRONTEND = Path('/srv/lmm-api-frontend')
SERVICE = 'lmm-api.service'
ENVIRONMENT = Path('/etc/lmm-api-go/lmm-api-go.env')
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
    for key in ('source_sha', 'frozen_guardian_bindings_sha256'):
        if not re.fullmatch(r'[0-9a-f]{64}', receipt.get(key, '')):
            raise RuntimeError('financial backup receipt lacks the sealed source or guardian binding')
    prepared = json.loads(guardian.bound_file(maintenance['prepare_config_path'], maintenance['prepare_config_sha256']))
    target = receipt.get('target')
    required = {'database', 'schema', 'system_identifier', 'database_oid', 'schema_oid'}
    if not isinstance(target, dict) or set(target) != required or any(target.get(key) != prepared['database'].get(key) for key in required - {'schema_oid'}):
        raise RuntimeError('financial backup receipt identifies a different PostgreSQL database or schema')
    if not isinstance(target['schema_oid'], int) or isinstance(target['schema_oid'], bool) or target['schema_oid'] <= 0:
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


def cleanup_process_references(work, proc=Path('/proc')):
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
                if path_in_payload(target, work):
                    found.append({'pid': int(process.name), 'kind': str(link.relative_to(process)), 'path': target})
            for line in (process / 'maps').read_text().splitlines():
                fields = line.split(maxsplit=5)
                if len(fields) == 6 and path_in_payload(fields[5], work):
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
        pending = [state['release'] for state in read_status()['deployments']
                   if state['phase'] not in ('STAGED', 'CONFIRMED', 'ROLLED_BACK')]
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
    parser.add_argument('action', choices=['doctor', 'upgrade', 'stage', 'apply', 'status', 'confirm', 'rollback', 'cleanup', 'maintenance-release', 'maintenance-capture', 'maintenance-close', 'maintenance-stop'])
    parser.add_argument('--release', help='explicit transaction ID; omit for status to list all transactions')
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
    if args.action in ('upgrade', 'apply', 'confirm', 'rollback', 'cleanup', 'maintenance-release', 'maintenance-capture', 'maintenance-close', 'maintenance-stop') and args.confirm != 'api.lmm.best':
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
    elif args.execute or args.superseded_by or args.retain_rollback or args.financial_backup or args.financial_backup_sha256 or args.financial_backup_receipt or args.financial_backup_receipt_sha256 or args.older_than != 86400:
        parser.error('cleanup arguments are only valid for cleanup')
    if os.geteuid() != 0:
        parser.error('run on the target as root')
    # Preserve JSON for existing non-interactive granular commands.
    args.human = args.human or (not args.json and (sys.stdout.isatty() or
                  args.action in ('doctor', 'upgrade') or not args.release))
    try:
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
                transferred = set()
                if maintenance and maintenance.get('stopped_writer'):
                    transferred = maintenance_ancestor_chain(maintenance['previous_deployment_id'], maintenance)
                for other in ROOT.glob('*/state.json'):
                    value = read_state(other.parent)
                    if other.parent != work and value['phase'] not in ('STAGED', 'CONFIRMED', 'ROLLED_BACK') and not (maintenance and maintenance.get('stopped_writer') and value['phase'] == 'FROZEN' and (value['release'] == maintenance['previous_deployment_id'] or value['release'] in transferred)):
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
