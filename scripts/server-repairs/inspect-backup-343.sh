#!/usr/bin/env bash
# Read-only PostgreSQL backup diagnosis; SQL, DSNs and stderr stay on the host.
set -euo pipefail
set +x
: "${LMM_OPS_REPORT:?Use the reviewed owner incident workflow}"
python3 - "$LMM_OPS_REPORT" <<'PY'
import json
import hashlib
import os
from pathlib import Path
import re
import resource
import pwd
import signal
import time
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
                'permission_denied': 'permission denied',
                'table_privilege': 'permission denied for table',
                'schema_privilege': 'permission denied for schema',
                'sequence_privilege': 'permission denied for sequence',
                'function_privilege': 'permission denied for function',
                'large_object_privilege': 'permission denied for large object',
                'filesystem_output': 'could not open output file',
                'authentication': 'authentication failed',
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


def identity_query(row):
    # Hex literals avoid interpolating private identifiers as SQL syntax.
    schema = row['schema'].encode().hex()
    table = row['table'].encode().hex()
    return """SELECT json_build_object(
      'database',current_database(),'database_oid',d.oid::bigint,
      'started',extract(epoch FROM pg_postmaster_start_time())::text,
      'version',current_setting('server_version_num'),
      'port',current_setting('port'),'namespace_oid',n.oid::bigint,
      'relation_oid',c.oid::bigint,'owner_oid',c.relowner::bigint)
      FROM pg_catalog.pg_database d CROSS JOIN pg_catalog.pg_class c
      JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
      WHERE d.datname=current_database()
        AND n.nspname=convert_from(decode('%s','hex'),'UTF8')
        AND c.relname=convert_from(decode('%s','hex'),'UTF8')""" % (schema,table)


def checked_identity(data):
    value = json.loads(data)
    if not isinstance(value,dict) or set(value) != {'database','database_oid','started','version','port','namespace_oid','relation_oid','owner_oid'}:
        raise ValueError('invalid_database_identity')
    if any(type(value[k]) is not int or value[k] <= 0 for k in ('database_oid','namespace_oid','relation_oid','owner_oid')):
        raise ValueError('invalid_database_identity')
    if not isinstance(value['database'],str) or not 1 <= len(value['database'].encode()) <= 63:
        raise ValueError('invalid_database_identity')
    if not all(isinstance(value[k],str) and re.fullmatch(r'[0-9]+(?:\.[0-9]+)?',value[k]) for k in ('started','version','port')):
        raise ValueError('invalid_database_identity')
    if not 1 <= int(value['port']) <= 65535:
        raise ValueError('invalid_database_port')
    return value


def run_stream(audit, label, args, env, seconds, limit):
    """Root-owned output descriptor stays private when the child drops uid."""
    target = audit/(label+'.dump')
    error = audit/(label+'.stderr')
    flags = os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW
    ofd = os.open(target,flags,0o600)
    efd = os.open(error,flags,0o600)
    def limits():
        resource.setrlimit(resource.RLIMIT_FSIZE,(limit,limit))
    with os.fdopen(ofd,'wb') as output, os.fdopen(efd,'wb') as stderr:
        proc = subprocess.Popen(args,env=env,stdin=subprocess.DEVNULL,stdout=output,
                                stderr=stderr,start_new_session=True,preexec_fn=limits)
        try:
            code = proc.wait(timeout=seconds)
            return {'exit_code':code,'categories':categories(private_bytes(error))},target
        except subprocess.TimeoutExpired:
            # Terminate the entire runuser/pg_dump process group, not just its parent.
            try:
                os.killpg(proc.pid,signal.SIGTERM)
                proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                os.killpg(proc.pid,signal.SIGKILL)
                proc.wait(timeout=3)
            except ProcessLookupError:
                proc.wait(timeout=3)
            return {'error_type':'TimeoutExpired'},target


def diagnose(report_path):
    report = Path(report_path)
    if os.geteuid() != 0 or not report.is_file() or report.is_symlink():
        raise ValueError('private_root_audit_required')
    answer = {'operation':'inspect-backup-343','database_changed':False,
              'service_changed':False,'diagnostic_version':5}
    try:
        database, env = connection(parse_environment(private_bytes(CONFIG).decode('utf-8')))
        u = urllib.parse.urlsplit(database)
        if u.hostname not in ('localhost','127.0.0.1','::1'):
            raise ValueError('loopback_database_required')
        row = json.loads(private_bytes(Path('/var/log/lmm-server-ops/35143545201-1/denied-relation.private.json')))
        if not isinstance(row,dict) or any(not isinstance(row.get(k),str) or not 1 <= len(row[k].encode()) <= 63 for k in ('schema','table','owner')):
            raise ValueError('invalid_recorded_relation')
        if hashlib.sha256((row['schema']+'\0'+row['table']).encode()).hexdigest() != '1b01f3a47f727c95b417c17c0a1b3459cc95a860044c74754a88d64c4d080baa':
            raise ValueError('recorded_relation_mismatch')
        query = identity_query(row)
        psql = ['/usr/bin/psql','-X','--no-password','-At','-v','ON_ERROR_STOP=1','-c']
        outcome, stdout = run_private(report.parent,'app-database-identity',psql+[query,database],env,15)
        answer['app_identity_probe'] = outcome
        if outcome.get('exit_code') != 0:
            raise ValueError('application_identity_unavailable')
        identity = checked_identity(stdout)
        account = pwd.getpwnam('postgres')
        if account.pw_uid == 0 or not Path('/usr/bin/runuser').is_file():
            raise ValueError('local_postgres_account_unavailable')
        local_env = {'PATH':'/usr/bin:/bin','HOME':account.pw_dir,'LC_ALL':'C','PGCONNECT_TIMEOUT':'8'}
        prefix = ['/usr/bin/runuser','-u','postgres','--']
        matched = None
        answer['local_identity_attempts'] = []
        for i, directory in enumerate(('/run/postgresql','/tmp')):
            socket = Path(directory)/('.s.PGSQL.'+identity['port'])
            try:
                info = socket.lstat()
                if not stat.S_ISSOCK(info.st_mode) or info.st_uid != account.pw_uid:
                    continue
            except FileNotFoundError:
                continue
            local = 'postgresql:///' + urllib.parse.quote(identity['database'],safe='') + '?' + urllib.parse.urlencode({'host':directory,'port':identity['port'],'user':'postgres','sslmode':'disable','connect_timeout':'8'})
            outcome, stdout = run_private(report.parent,'local-database-identity-'+str(i),prefix+psql+[query,local],local_env,15)
            answer['local_identity_attempts'].append(outcome)
            if outcome.get('exit_code') == 0 and checked_identity(stdout) == identity:
                matched = local
                answer['local_identity_matches_application'] = True
                break
        if matched is None:
            raise ValueError('no_verified_local_database_identity')
        # Full backup, with no schema/table exclusions and no application-role grants.
        outcome, stdout = run_private(report.parent,'local-database-size',prefix+psql+['SELECT pg_catalog.pg_database_size(pg_catalog.current_database())',matched],local_env,15)
        if outcome.get('exit_code') != 0 or not re.fullmatch(rb'[0-9]+\n?',stdout):
            raise ValueError('database_size_unverified')
        size = int(stdout.strip())
        fs = os.statvfs(report.parent)
        if not 0 < size <= 2*1024**3 or fs.f_bavail*fs.f_frsize < 2*size+1024**3:
            raise ValueError('insufficient_bounded_backup_capacity')
        outcome, target = run_stream(report.parent,'verified-local-full-backup',
            prefix+['/usr/bin/pg_dump','--no-password','--format=custom','--lock-wait-timeout=5s',matched],local_env,100,2*1024**3)
        answer['local_full_backup'] = outcome
        if outcome.get('exit_code') != 0:
            raise ValueError('local_full_backup_failed')
        info = target.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_nlink != 1 or info.st_mode & 0o077 or not 0 < info.st_size <= 2*1024**3:
            raise ValueError('unsafe_full_backup_output')
        outcome, _ = run_private(report.parent,'local-backup-toc',['/usr/bin/pg_restore','--list',str(target)],local_env,15)
        answer['backup_catalog_validation'] = outcome
        if outcome.get('exit_code') != 0:
            raise ValueError('invalid_full_backup_archive')
        digest = hashlib.sha256()
        with target.open('rb') as f:
            for chunk in iter(lambda:f.read(1024*1024),b''):
                digest.update(chunk)
        answer['full_backup_bytes'] = info.st_size
        answer['full_backup_sha256'] = digest.hexdigest()
        evidence = {'database_identity':identity,'local_database_url':matched,'backup_file':str(target),
                    'backup_sha256':digest.hexdigest(),'backup_bytes':info.st_size,'created_unix':int(time.time()),
                    'database_changed':False,'application_privileges_changed':False}
        fd = os.open(report.parent/'verified-local-backup.private.json',os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
        with os.fdopen(fd,'w') as f:
            json.dump(evidence,f,sort_keys=True)
            f.write('\n')
        answer['application_privileges_changed'] = False
    except (ValueError,OSError,UnicodeError,KeyError) as e:
        answer['diagnostic_error_type'] = type(e).__name__
        allowed = {'invalid_database_identity','invalid_database_port','local_postgres_account_unavailable',
                   'no_verified_local_database_identity','database_size_unverified',
                   'insufficient_bounded_backup_capacity','local_full_backup_failed',
                   'unsafe_full_backup_output','invalid_full_backup_archive',
                   'application_identity_unavailable','invalid_recorded_relation','recorded_relation_mismatch'}
        if type(e) is ValueError and str(e) in allowed:
            answer['diagnostic_error_code'] = str(e)
    report.write_text(json.dumps(answer,sort_keys=True,indent=2)+'\n')


if __name__ == '__main__':
    diagnose(sys.argv[1])
PY
