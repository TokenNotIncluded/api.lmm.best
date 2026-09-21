#!/usr/bin/env python3
"""Deploy standalone LMM artifacts on systemd Linux, without a package manager.

Run on the target as root. Use doctor, upgrade, then explicit confirm.
The granular stage/apply/status/rollback commands remain available.
--migrate requires PostgreSQL backup tools and creates a verified backup
after the old writer stops, before applying migrations.
"""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import shlex
import subprocess
import sys
import time
import urllib.request
import urllib.parse

ROOT = Path('/var/lib/lmm-api-deploy-systemd')
BINARY = Path('/usr/bin/lmm-api-go')
ENTRY = Path('/usr/bin/lmm-api')
FRONTEND = Path('/srv/lmm-api-frontend')
SERVICE = 'lmm-api.service'
ENVIRONMENT = Path('/etc/lmm-api-go/lmm-api-go.env')
RELEASE_PATTERN = r'[A-Za-z0-9][A-Za-z0-9._-]{0,70}'
PHASES = {'STAGED', 'MUTATION_PENDING', 'AWAITING_CONFIRMATION', 'CONFIRMED', 'ROLLED_BACK'}
BASE_TOOLS = ('systemctl', 'systemd-run', 'journalctl', 'nginx')
BACKUP_TOOLS = ('psql', 'pg_dump', 'pg_restore')


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


def verify(work, label, mode='verify'):
    args = ['systemd-run', '--quiet', '--wait', '--collect', '--pipe',
            '--unit=lmm-schema-' + work.name + '-' + label,
            '-p', 'Type=oneshot', '-p', 'TimeoutStartSec=180',
            *[arg for path in service_environment_files() for arg in ('-p', 'EnvironmentFile=' + path)],
            '-p', 'Environment=LMM_DB_MIGRATION_MODE=' + mode,
            '-p', 'MemoryMax=384M', '-p', 'NoNewPrivileges=yes',
            '-p', 'ProtectSystem=strict', '-p', 'PrivateTmp=yes',
            '-p', 'WorkingDirectory=' + str(work), '-p', 'ReadWritePaths=' + str(work),
            str(work / 'lmm-api'), 'migrate', '--' + mode]
    run(*args, log=work / ('verify-' + label + '.log'))


def database_environment():
    pid = property_value('MainPID')
    if pid == '0':
        raise RuntimeError('running service is required to identify database configuration')
    entries = Path('/proc', pid, 'environ').read_bytes().split(b'\0')
    env = dict(item.split(b'=', 1) for item in entries if b'=' in item)
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


def backup(work, env, schema_only=False, exclude=()):
    path = work / ('preflight-schema.sql' if schema_only else 'database.dump')
    command = ['pg_dump', '--no-password', '--file', str(path)]
    schema = subprocess.run(['psql', '-XAtc', 'SELECT current_schema()'], env=env, capture_output=True, check=True).stdout.decode().strip()
    if not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*', schema):
        raise RuntimeError('unsupported database schema name')
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


def healthy(version):
    for route in ('/api/livez', '/api/status'):
        with urllib.request.urlopen('http://127.0.0.1:3000' + route, timeout=10) as response:
            body = json.load(response)
        if body.get('success') is not True:
            raise RuntimeError('health response was not successful')
        if route == '/api/status' and body.get('data', {}).get('version') != version:
            raise RuntimeError('running version does not match candidate')


def stop(work):
    invocation = property_value('InvocationID')
    run('systemctl', 'stop', SERVICE, log=work / 'stop.log')
    if property_value('MainPID') != '0' or property_value('Result') != 'success' or property_value('ExecMainStatus') != '0':
        raise RuntimeError('old writer did not exit cleanly; activation refused')
    journal = run('journalctl', '--no-pager', '-o', 'cat', '_SYSTEMD_INVOCATION_ID=' + invocation)
    (work / 'shutdown.log').write_text(journal)
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


def read_status(release=None):
    # Do not create ROOT, a lock or a transaction while inspecting the host.
    if ROOT.is_symlink():
        raise RuntimeError('deployment root may not be a symlink')
    if release:
        return read_state(ROOT / release, allow_incomplete=True)
    states = []
    if ROOT.exists():
        for work in sorted(ROOT.iterdir()):
            if work.is_symlink():
                raise RuntimeError(f'unexpected symlink in deployment root: {work}')
            if work.is_dir():
                if not re.fullmatch(RELEASE_PATTERN, work.name):
                    raise RuntimeError(f'invalid transaction directory: {work}')
                states.append(read_state(work, allow_incomplete=True))
    return {'deployments': states}


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
    parser.add_argument('action', choices=['doctor', 'upgrade', 'stage', 'apply', 'status', 'confirm', 'rollback'])
    parser.add_argument('--release', help='explicit transaction ID; omit for status to list all transactions')
    parser.add_argument('--binary', type=Path)
    parser.add_argument('--frontend', type=Path)
    parser.add_argument('--confirm')
    parser.add_argument('--migrate', action='store_true', help='back up PostgreSQL and apply schema migrations after stopping the writer')
    parser.add_argument('--backup-exclude-table', action='append', default=[], help='exact schema.table of an unrelated archive table to exclude; recorded in the plan')
    output = parser.add_mutually_exclusive_group()
    output.add_argument('--json', action='store_true', help='machine-readable output without progress messages')
    output.add_argument('--human', action='store_true', help='readable status and next commands')
    args = parser.parse_args(argv)
    if args.release is not None and not re.fullmatch(RELEASE_PATTERN, args.release):
        parser.error('invalid release ID')
    if args.action not in ('doctor', 'status') and not args.release:
        parser.error('--release is required for this action')
    if args.action in ('upgrade', 'apply', 'confirm', 'rollback') and args.confirm != 'api.lmm.best':
        parser.error('mutations require --confirm api.lmm.best')
    if args.action not in ('stage', 'upgrade') and (args.binary or args.frontend or args.backup_exclude_table):
        parser.error('artifact and backup-exclusion arguments are only valid for stage/upgrade')
    if args.backup_exclude_table and not args.migrate:
        parser.error('--backup-exclude-table requires --migrate')
    if args.migrate and args.action not in ('doctor', 'stage', 'upgrade', 'apply'):
        parser.error('--migrate is only valid for doctor/stage/upgrade/apply')
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
            show(read_status(args.release), args.human)
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
    ROOT.mkdir(mode=0o700, exist_ok=True)
    if (ROOT / 'lock').is_symlink():
        raise RuntimeError('deployment lock may not be a symlink')
    with (ROOT / 'lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise RuntimeError('another deployment operation holds the lock; inspect status') from None
        work = ROOT / args.release
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
                backup(work, database_environment(), schema_only=True, exclude=args.backup_exclude_table)
            else:
                verify(work, 'stage')
            state = {'release': args.release, 'version': version, 'sha256': digest(work / 'lmm-api-go'),
                     'frontend_sha256': tree_digest(work / 'frontend'), 'migrate': args.migrate,
                     'backup_exclude_tables': args.backup_exclude_table, 'phase': 'STAGED'}
            save(work, state)
        if args.action != 'stage':
            state = read_state(work)
            check_layout()
            if digest(work / 'lmm-api-go') != state['sha256']:
                raise RuntimeError('staged binary changed')
            if tree_digest(work / 'frontend') != state['frontend_sha256']:
                raise RuntimeError('staged frontend changed')
            if args.action in ('apply', 'upgrade'):
                progress(args, 'Verifying staged plan, schema and rollback inputs...')
                check_tools(args.migrate)
                if state['migrate'] != args.migrate:
                    raise RuntimeError('--migrate must match the immutable staged plan')
                if state['phase'] != 'STAGED':
                    raise RuntimeError('apply requires STAGED; use status or explicit rollback')
                for other in ROOT.glob('*/state.json'):
                    value = read_state(other.parent)
                    if other.parent != work and value['phase'] not in ('STAGED', 'CONFIRMED', 'ROLLED_BACK'):
                        raise RuntimeError('another deployment needs recovery')
                if state['migrate']:
                    db_env = database_environment()
                    backup(work, db_env, schema_only=True, exclude=state['backup_exclude_tables'])
                else:
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
                stop(work)
                if state['migrate']:
                    state['database_backup_sha256'] = backup(work, db_env, exclude=state['backup_exclude_tables'])
                    save(work, state)
                    verify(work, 'migrate', mode='apply')
                    verify(work, 'post-migrate')
                install(work / 'lmm-api-go', BINARY)
                run(str(ENTRY), 'operator', 'frontend', 'publish', '--source', str(work / 'frontend'),
                    '--release', args.release, '--keep', '10', log=work / 'frontend.log')
                run('systemctl', 'start', SERVICE, log=work / 'start.log')
                progress(args, 'Waiting for the selected version to pass readiness...')
                deadline = time.monotonic() + 120
                while True:
                    try:
                        healthy(state['version'])
                        break
                    except Exception:
                        if time.monotonic() >= deadline:
                            raise RuntimeError('candidate failed readiness; explicit rollback required')
                        time.sleep(2)
                state['phase'] = 'AWAITING_CONFIRMATION'
                state['ready_at'] = time.time()
            elif args.action == 'confirm':
                if state['phase'] != 'AWAITING_CONFIRMATION':
                    raise RuntimeError('confirm requires AWAITING_CONFIRMATION')
                if time.time() - state['ready_at'] < 120:
                    raise RuntimeError('observe the candidate for at least 120 seconds before confirming')
                healthy(state['version'])
                if digest(BINARY) != state['sha256']:
                    raise RuntimeError('installed binary changed')
                if os.readlink(FRONTEND / 'current') != 'releases/' + args.release:
                    raise RuntimeError('active frontend changed; confirmation refused')
                state['phase'] = 'CONFIRMED'
            else:
                if state['phase'] not in ('MUTATION_PENDING', 'AWAITING_CONFIRMATION'):
                    raise RuntimeError('rollback requires a pending deployment')
                if digest(work / 'previous-binary') != state['previous_sha256']:
                    raise RuntimeError('rollback binary changed')
                if property_value('MainPID') != '0':
                    stop(work)
                run(str(work / 'lmm-api'), 'operator', 'frontend', 'rollback', '--release', state['previous_frontend'],
                    '--keep', '10', log=work / 'rollback-frontend.log')
                install(work / 'previous-binary', BINARY)
                run('systemctl', 'start', SERVICE, log=work / 'rollback-start.log')
                progress(args, 'Waiting for the selected version to pass readiness...')
                deadline = time.monotonic() + 120
                while True:
                    try:
                        healthy(state['previous_version'])
                        break
                    except Exception:
                        if time.monotonic() >= deadline:
                            raise RuntimeError('rollback failed readiness; recovery remains pending')
                        time.sleep(2)
                state['phase'] = 'ROLLED_BACK'
            save(work, state)
        show(state, args.human)


if __name__ == '__main__':
    sys.exit(main())
