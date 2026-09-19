#!/usr/bin/env python3
"""Deploy standalone LMM artifacts on systemd Linux, without a package manager.

Run on the target as root. Stage and verify before apply; confirmation and
rollback are explicit. --migrate requires PostgreSQL backup tools and creates
a verified backup after the old writer stops, before applying migrations.
"""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
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


def verify(work, label, mode='verify'):
    args = ['systemd-run', '--quiet', '--wait', '--collect', '--pipe',
            '--unit=lmm-schema-' + work.name + '-' + label,
            '-p', 'Type=oneshot', '-p', 'TimeoutStartSec=180',
            '-p', 'EnvironmentFile=/etc/lmm-api-go/lmm-api-go.env',
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
    if not shutil.which('pg_dump') or not shutil.which('pg_restore'):
        raise RuntimeError('migration requires pg_dump and pg_restore')
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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['stage', 'apply', 'status', 'confirm', 'rollback'])
    parser.add_argument('--release', required=True)
    parser.add_argument('--binary', type=Path)
    parser.add_argument('--frontend', type=Path)
    parser.add_argument('--confirm')
    parser.add_argument('--migrate', action='store_true', help='back up PostgreSQL and apply schema migrations after stopping the writer')
    parser.add_argument('--backup-exclude-table', action='append', default=[], help='exact schema.table of an unrelated archive table to exclude; recorded in the plan')
    args = parser.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._-]{0,70}', args.release):
        parser.error('invalid release ID')
    if os.geteuid() != 0:
        parser.error('run on the target as root')
    os.umask(0o077)
    if ROOT.is_symlink():
        raise RuntimeError('deployment root may not be a symlink')
    ROOT.mkdir(mode=0o700, exist_ok=True)
    with (ROOT / 'lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        work = ROOT / args.release
        if args.action == 'stage':
            check_layout()
            if not args.binary or not args.frontend or not (args.frontend / 'index.html').is_file():
                parser.error('stage requires --binary and --frontend with index.html')
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
        else:
            state = json.loads((work / 'state.json').read_text())
            if args.action == 'status':
                print(json.dumps(state))
                return
            if args.confirm != 'api.lmm.best':
                parser.error('mutations require --confirm api.lmm.best')
            check_layout()
            if digest(work / 'lmm-api-go') != state['sha256']:
                raise RuntimeError('staged binary changed')
            if tree_digest(work / 'frontend') != state['frontend_sha256']:
                raise RuntimeError('staged frontend changed')
            if args.action == 'apply':
                if state['phase'] != 'STAGED':
                    raise RuntimeError('apply requires STAGED; use status or explicit rollback')
                for other in ROOT.glob('*/state.json'):
                    value = json.loads(other.read_text())
                    if other.parent != work and value['phase'] not in ('STAGED', 'CONFIRMED', 'ROLLED_BACK'):
                        raise RuntimeError('another deployment needs recovery')
                if state['migrate']:
                    if not args.migrate:
                        raise RuntimeError('this staged plan requires --migrate at apply')
                    db_env = database_environment()
                    backup(work, db_env, schema_only=True, exclude=state['backup_exclude_tables'])
                else:
                    verify(work, 'apply')
                run('nginx', '-t', log=work / 'nginx.log')
                old_frontend = os.readlink(FRONTEND / 'current')
                if not re.fullmatch(r'releases/[A-Za-z0-9][A-Za-z0-9._-]{0,127}', old_frontend):
                    raise RuntimeError('invalid current frontend link')
                shutil.copy2(BINARY, work / 'previous-binary')
                shutil.copy2('/etc/lmm-api-go/lmm-api-go.env', work / 'previous.env')
                state.update(previous_frontend=old_frontend.split('/')[1], previous_sha256=digest(BINARY),
                             previous_version=run(str(ENTRY), 'version'), phase='MUTATION_PENDING')
                save(work, state)
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
        print(json.dumps(state))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print(f'deploy-systemd: {error}', file=sys.stderr)
        sys.exit(1)
