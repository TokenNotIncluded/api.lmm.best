#!/usr/bin/env bash
# Read-only PostgreSQL backup diagnosis; SQL, DSNs and stderr stay on the host.
set -euo pipefail
set +x
: "${LMM_OPS_REPORT:?Use the reviewed owner incident workflow}"
python3 - "$LMM_OPS_REPORT" <<'PY'
import json
import os
from pathlib import Path
import re
import resource
import stat
import subprocess
import sys
import urllib.parse

DEPLOYMENT = 'release-go-v0.2.51-35116594330-attempt-1'
CONFIG = Path('/etc/lmm-api-go/lmm-api-go.env')
AUDIT = Path('/var/lib/lmm-api-go-deploy/work') / DEPLOYMENT / 'state/incident-343-schema-recovery'


def private_bytes(path, limit=1048576):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as f:
        s = os.fstat(f.fileno())
        if not stat.S_ISREG(s.st_mode) or s.st_uid != 0 or s.st_nlink != 1 or s.st_mode & 0o077 or s.st_size > limit:
            raise ValueError('unsafe_private_input')
        return f.read(limit + 1)


def parse_environment(text):
    values = {}
    for line in text.replace('\r\n', '\n').split('\n'):
        line = line.strip()
        if not line or line[0] in '#;':
            continue
        key, sep, value = line.partition('=')
        key, value = key.strip(), value.strip()
        if not sep or not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*', key) or key in values:
            raise ValueError('invalid_environment')
        if any(c in value for c in '\0\r\n'):
            raise ValueError('invalid_environment')
        if value and value[0] in "'\"":
            quote = value[0]
            if len(value) < 2 or value[-1] != quote:
                raise ValueError('invalid_environment')
            value = value[1:-1]
            if quote in value or '`' in value or '$(' in value or (quote == '"' and any(c in value for c in '\\$')):
                raise ValueError('invalid_environment')
        elif any(c in value for c in ' \t`;') or '$(' in value:
            raise ValueError('invalid_environment')
        values[key] = value
    return values


def connection(values):
    found = [values[k] for k in ('SQL_DSN', 'DATABASE_URL') if values.get(k)]
    if len(found) != 1:
        raise ValueError('invalid_database_configuration')
    u = urllib.parse.urlsplit(found[0])
    if u.scheme not in ('postgres', 'postgresql') or not u.hostname:
        raise ValueError('invalid_database_configuration')
    env = {'PATH': '/usr/bin:/bin', 'HOME': '/root', 'LC_ALL': 'C'}
    env.update({k: v for k, v in values.items() if k.startswith('PG')})
    if u.password is not None:
        env['PGPASSWORD'] = urllib.parse.unquote(u.password)
        u = u._replace(netloc=(u.username or '') + '@' + u.netloc.rsplit('@', 1)[1])
    env['PGCONNECT_TIMEOUT'] = '8'
    return urllib.parse.urlunsplit(u), env


def categories(data):
    text = data[:65536].decode('utf-8', errors='replace').lower()
    patterns = {'version_mismatch': 'server version mismatch',
                'permission_denied': 'permission denied', 'authentication': 'authentication failed',
                'connection_refused': 'connection refused', 'connection_timeout': 'timeout expired',
                'lock_timeout': 'lock timeout', 'disk_full': 'no space left',
                'unsupported_option': 'unrecognized option', 'ssl_error': 'ssl error'}
    return [name for name, pattern in patterns.items() if pattern in text]


def bounded_child():
    resource.setrlimit(resource.RLIMIT_FSIZE, (64 * 1024 * 1024, 64 * 1024 * 1024))


def run_private(audit, label, args, env, seconds):
    stderr_path = audit / (label + '.stderr')
    fd = os.open(stderr_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as stderr:
        try:
            p = subprocess.run(args, env=env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                               stderr=stderr, timeout=seconds, check=False, preexec_fn=bounded_child)
            output = {'exit_code': p.returncode, 'categories': categories(private_bytes(stderr_path))}
            return output, p.stdout[:4096]
        except (OSError, subprocess.TimeoutExpired) as e:
            return {'error_type': type(e).__name__}, b''


def diagnose(report_path):
    report = Path(report_path)
    if os.geteuid() != 0 or not report.is_file() or report.is_symlink():
        raise ValueError('private_root_audit_required')
    answer = {'operation': 'inspect-backup-343', 'database_changed': False,
              'service_changed': False, 'previous_recovery': {}, 'tools': {}}
    for name in ('before-manifest.json', 'before-status.json', 'before-schema.database.dump', 'database.sha256', 'schema-created.json'):
        path = AUDIT / name
        try:
            s = path.lstat()
            answer['previous_recovery'][name] = {'exists': True, 'regular': stat.S_ISREG(s.st_mode), 'bytes': s.st_size}
        except FileNotFoundError:
            answer['previous_recovery'][name] = {'exists': False}
    for tool in ('psql', 'pg_dump', 'pg_restore'):
        p = Path('/usr/bin') / tool
        if not p.is_file() or not os.access(p, os.X_OK):
            answer['tools'][tool] = {'available': False}
            continue
        outcome, stdout = run_private(report.parent, tool + '-version', [str(p), '--version'],
                                      {'PATH': '/usr/bin:/bin', 'LC_ALL': 'C'}, 5)
        version = re.search(rb'\(PostgreSQL\) ([0-9]+(?:\.[0-9]+){1,2})', stdout)
        answer['tools'][tool] = {'available': True, **outcome}
        if version:
            answer['tools'][tool]['version'] = version[1].decode('ascii')
    try:
        database, env = connection(parse_environment(private_bytes(CONFIG).decode('utf-8')))
        outcome, stdout = run_private(report.parent, 'server-version',
            ['/usr/bin/psql', '-X', '--no-password', '-At', '-v', 'ON_ERROR_STOP=1',
             '-c', "SELECT pg_catalog.current_setting('server_version_num')", database], env, 12)
        answer['server_probe'] = outcome
        if outcome.get('exit_code') == 0 and re.fullmatch(rb'[0-9]{5,8}\n?', stdout):
            answer['server_version_num'] = int(stdout.strip())
            # Schema-only metadata reproduction: no INSERT/UPDATE/DDL and no service changes.
            # This is NOT a data backup and can never satisfy the recovery backup gate.
            if answer['tools']['pg_dump'].get('available'):
                target = report.parent / 'schema-only-probe.dump'
                if target.exists() or target.is_symlink():
                    raise ValueError('diagnostic_target_exists')
                outcome, _ = run_private(report.parent, 'schema-only-probe',
                    ['/usr/bin/pg_dump', '--no-password', '--schema-only', '--format=custom',
                     '--lock-wait-timeout=5s', '--file=' + str(target), database], env, 30)
                answer['schema_only_probe'] = outcome
    except (ValueError, OSError, UnicodeError) as e:
        answer['diagnostic_error_type'] = type(e).__name__
    report.write_text(json.dumps(answer, sort_keys=True, indent=2) + '\n')


if __name__ == '__main__':
    diagnose(sys.argv[1])
PY
