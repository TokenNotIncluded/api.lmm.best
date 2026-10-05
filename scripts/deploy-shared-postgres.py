#!/usr/bin/env python3
"""One reviewed shared-PostgreSQL maintenance episode, before normal deployment.

Persistent SSH agents retain existing connection metadata only in memory and hold
all deployment locks. No native/standalone transaction state is created or edited.
Failures never replay migrations, restore databases, or restart writers.
"""
import argparse
import array
import fcntl
from decimal import Decimal
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import select
import signal
import shutil
import socket
import stat
import struct
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

HEX = r'[0-9a-f]{64}'
NAME = r'[A-Za-z0-9][A-Za-z0-9._-]{0,70}'
PATH = r'/[A-Za-z0-9._/-]+'
PRICING_KEYS = ('CreditsPerUSD', 'LegacyPricingQuotaPerUnit', 'QuotaPerUnit',
                'ModelRatio', 'CompletionRatio', 'ModelPrice', 'CacheRatio',
                'CreateCacheRatio', 'ImageRatio', 'AudioRatio', 'AudioCompletionRatio',
                'ModelPriceLock', 'GroupRatio', 'GroupGroupRatio', 'ModerationGroupPolicies',
                'ModerationEnabled', 'AssistantModerationEnabled', 'violation_fee.enabled', 'violation_fee.policies', 'billing_setting.billing_mode',
                'billing_setting.billing_expr', 'tool_price_setting.prices')
sys.dont_write_bytecode = True

LOCKS = ('/run/lock/lmm-api-go-deploy.lock',
         '/var/lib/lmm-api-deploy-systemd/lock', '/srv/lmm-api-frontend/.release.lock')


class GateFailed(RuntimeError):
    pass


def require(ok, code):
    if not ok:
        raise GateFailed(code)


def sha(path):
    with Path(path).open('rb') as f:
        return hashlib.file_digest(f, 'sha256').hexdigest()


def encode(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


def unique(pairs):
    value = {}
    for k, v in pairs:
        require(k not in value, 'duplicate-json-key')
        value[k] = v
    return value


def decode(data):
    return json.loads(data, object_pairs_hook=unique)


def absolute(value):
    require(isinstance(value, str) and re.fullmatch(PATH, value) and
            '..' not in Path(value).parts and str(Path(value)) == value, 'unsafe-path')
    return Path(value)


def regular(path, expected=None):
    path = Path(path)
    st = path.lstat()
    require(stat.S_ISREG(st.st_mode) and st.st_nlink == 1 and
            path.resolve() == path and not st.st_mode & 0o022, 'unsafe-file')
    if expected:
        require(sha(path) == expected, 'artifact-changed')


def new_private(path):
    path = absolute(str(path))
    require(path.parent.resolve() == path.parent, 'unsafe-parent')
    path.mkdir(mode=0o700)
    require(path.stat().st_uid == os.geteuid(), 'private-owner')
    return path


def private_write(path, data):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as f:
        f.write(data)
        f.flush()
        os.fsync(f.fileno())


def command(args, *, env=None, log=None, timeout=180, cwd=None):
    # Keep tool output private: migration/PG errors can contain connection data.
    try:
        result = subprocess.run(args, env=env, capture_output=True, timeout=timeout, cwd=cwd)
    except subprocess.TimeoutExpired as e:
        if log:
            private_write(log, (e.stdout or b'') + (e.stderr or b''))
        raise GateFailed('command-timeout-outcome-unknown') from None
    except OSError:
        raise GateFailed('command-unavailable') from None
    if log:
        private_write(log, result.stdout + result.stderr)
    require(result.returncode == 0, 'command-failed')
    return result.stdout


def validate_plan(p):
    require(set(p) == {'format', 'id', 'confirmation', 'script_sha256', 'helper_sha256',
                      'source_sha', 'candidate', 'nodes', 'database_owner', 'database_backup',
                      'public_probes', 'expected_units', 'rehearsal_root'}, 'plan-fields')
    require(p['format'] == 1 and re.fullmatch(NAME, p['id']) and
            re.fullmatch(NAME, p['confirmation']), 'plan-identity')
    for key in ('script_sha256', 'helper_sha256'):
        require(re.fullmatch(HEX, p[key]), 'plan-digest')
    require(re.fullmatch(r'[0-9a-f]{40}', p['source_sha']), 'plan-source')
    require(set(p['candidate']) == {'path', 'sha256'}, 'candidate-fields')
    absolute(p['candidate']['path'])
    require(re.fullmatch(HEX, p['candidate']['sha256']), 'candidate-digest')
    require(2 <= len(p['nodes']) <= 8, 'all-writer-nodes-required')
    names = []
    for n in p['nodes']:
        require(set(n) == {'name', 'ssh', 'hostname', 'script', 'helper', 'plan',
                          'candidate', 'provider_sha256', 'version', 'invocation',
                          'rollback', 'probes'}, 'node-fields')
        for k in ('name', 'ssh', 'hostname'):
            require(re.fullmatch(NAME, n[k]), 'node-identity')
        for k in ('script', 'helper', 'plan', 'candidate', 'rollback'):
            absolute(n[k])
        require(re.fullmatch(HEX, n['provider_sha256']) and
                re.fullmatch(r'[0-9a-f]{32}', n['invocation']), 'node-binding')
        require(re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+', n['version']), 'node-version')
        require(n['probes'], 'origin-barrier-required')
        names.append(n['name'])
    require(len(set(names)) == len(names) and p['database_owner'] in names, 'node-set')
    backup = p['database_backup']
    require(isinstance(backup, dict) and set(backup) == {
        'transport', 'os_user', 'database_role', 'socket_directory', 'port'}, 'backup-fields')
    require(backup['transport'] == 'local_peer' and
            isinstance(backup['os_user'], str) and
            re.fullmatch(r'[a-z_][a-z0-9_-]{0,31}', backup['os_user']) and backup['os_user'] != 'root' and
            isinstance(backup['database_role'], str) and re.fullmatch(NAME, backup['database_role']) and
            type(backup['port']) is int and 1 <= backup['port'] <= 65535, 'backup-role-binding')
    absolute(backup['socket_directory'])
    require(p['public_probes'], 'public-barrier-required')
    for probe in p['public_probes'] + [v for n in p['nodes'] for v in n['probes']]:
        require(set(probe) == {'url', 'body_sha256'}, 'probe-fields')
        u = urllib.parse.urlsplit(probe['url'])
        require(u.scheme in ('http', 'https') and u.hostname and not u.username and
                not u.password and not u.query and not u.fragment and
                re.fullmatch(HEX, probe['body_sha256']), 'unsafe-probe')
    require(set(p['expected_units']) == set(PRICING_KEYS[:3]) and
            all(isinstance(v, str) and re.fullmatch(r'[0-9]+(?:\.[0-9]+)?', v) and
                Decimal(v).is_finite() and Decimal(v) > 0 for v in p['expected_units'].values()), 'unit-binding')
    absolute(p['rehearsal_root'])
    return p


def load_plan(path, digest):
    regular(path, digest)
    return validate_plan(decode(Path(path).read_bytes()))


def probe_all(probes):
    for v in probes:
        try:
            with urllib.request.urlopen(v['url'], timeout=10) as r:
                code, data = r.status, r.read(8193)
        except urllib.error.HTTPError as e:
            code, data = e.code, e.read(8193)
        except Exception:
            raise GateFailed('barrier-unavailable') from None
        require(code == 503 and len(data) <= 8192 and
                hashlib.sha256(data).hexdigest() == v['body_sha256'], 'barrier-not-closed')


def unit():
    data = command(['systemctl', 'show', 'lmm-api.service',
                    '--property=MainPID,ExecMainPID,ExecMainCode,ExecMainStatus,ActiveState,SubState,Result,ControlGroup,InvocationID'])
    return dict(line.split('=', 1) for line in data.decode().splitlines() if '=' in line)


def validate_shutdown(text):
    text = text.lower()
    reports, flushes, others = [], [], []
    for line in text.splitlines():
        line = re.sub(r'^\[sys\] [0-9/ -:]+ \| ', '', line.strip())
        m = re.fullmatch(r'refund_tasks execution_complete=true accepted=(\d+) finished=(\d+) active=0 failed=0 \(execution completion is not financial success\)', line)
        f = re.fullmatch(r'quota dashboard flush: persisted=(\d+) failed=0 dropped=0', line)
        if m:
            reports.append(m.groups())
        elif f:
            flushes.append(f.groups())
        else:
            require('refund_tasks' not in line and 'quota dashboard flush' not in line,
                    'malformed-shutdown-report')
            others.append(line)
    require(len(reports) == 1 and reports[0][0] == reports[0][1] and
            len(flushes) <= 1, 'shutdown-completion-missing')
    rest = '\n'.join(others)
    require('received signal:' in rest and 'server exited' in rest and
            rest.count('batch update started') <= rest.count('batch update finished') and
            not any(x in rest for x in ('error', 'failed', 'panic', 'timed out',
                                       'deadline exceeded', 'killed', 'oom')), 'unclean-shutdown')


def stopped(expected_pid):
    u = unit()
    require(u['MainPID'] == '0' and u['ExecMainPID'] == expected_pid and
            u['ExecMainCode'] == '1' and u['ExecMainStatus'] == '0' and
            u['ActiveState'] == 'inactive' and u['SubState'] == 'dead' and
            u['Result'] == 'success' and not u['ControlGroup'], 'writer-not-cleanly-stopped')
    require(not Path('/proc', expected_pid).exists(), 'old-pid-still-present')
    for f in ('/proc/net/tcp', '/proc/net/tcp6'):
        for line in Path(f).read_text().splitlines()[1:]:
            a = line.split()
            require(int(a[1].rsplit(':', 1)[1], 16) != 3000 or a[3] == '06',
                    'port3000-not-drained')
    for proc in Path('/proc').iterdir():
        if proc.name.isdigit():
            try:
                name = Path(os.readlink(proc / 'exe')).name.removesuffix(' (deleted)')
                require(not name.startswith(('lmm-api', 'lmm-rust')), 'untracked-provider-process')
            except FileNotFoundError:
                # Kernel threads have no userspace executable. An extant
                # userspace PID with an unreadable exe must still fail closed.
                if proc.exists():
                    try:
                        require(re.search(r'^Kthread:\s+1$', (proc / 'status').read_text(), re.M),
                                'provider-executable-unreadable')
                    except FileNotFoundError:
                        require(not proc.exists(), 'provider-scan-race')
            except PermissionError:
                raise GateFailed('provider-scan-permission-denied') from None
    return True


def psql(env, sql):
    return command(['psql', '-XqAt', '--no-password', '-v', 'ON_ERROR_STOP=1', '-c', sql], env=env)


def database_identity(env, query=None):
    v = decode((query or psql)(env, "SELECT json_build_object('system',system_identifier::text,'database',current_database(),'oid',(SELECT oid FROM pg_database WHERE datname=current_database()),'schema',current_schema(),'version',current_setting('server_version_num')) FROM pg_control_system()"))
    require(re.fullmatch(NAME, v['schema']), 'unsafe-schema')
    return v


def no_database_clients(env):
    count = psql(env, "SELECT count(*) FROM pg_stat_activity WHERE datid=(SELECT oid FROM pg_database WHERE datname=current_database()) AND backend_type='client backend' AND pid<>pg_backend_pid()")
    require(count.strip() == b'0', 'unrecognized-database-client')


def fingerprints(env, expected, query=None):
    query = query or psql
    keys = ','.join("'" + k + "'" for k in PRICING_KEYS)
    values = decode(query(env, 'SELECT coalesce(json_object_agg(key,value),\'{}\'::json) FROM options WHERE key IN (' + keys + ')'))
    require(all(values.get(k) == v for k, v in expected.items()), 'immutable-units-changed')
    quotas = {}
    for table, quota_column in (('users', 'quota'), ('tokens', 'remain_quota')):
        sql = "SELECT md5(coalesce(string_agg(id::text||':'||" + quota_column + "::text||':'||used_quota::text,',' ORDER BY id),'')) FROM " + table
        quotas[table] = query(env, sql).strip().decode()
    return {'pricing_and_policies': hashlib.sha256(encode(values)).hexdigest(), 'user_and_token_quota_columns': quotas}


def table_counts(env, schema):
    names = decode(psql(env, "SELECT coalesce(json_agg(tablename ORDER BY tablename),'[]'::json) FROM pg_tables WHERE schemaname='" + schema + "'"))
    result = {}
    for name in names:
        # Identifiers come from the catalog, never from plan text or SQL snippets.
        quoted = '"' + name.replace('"', '""') + '"'
        result[name] = int(psql(env, 'SELECT count(*) FROM "' + schema + '".' + quoted))
    return result


def database_table_counts(env, query=None):
    query = query or psql
    tables = decode(query(env, "SELECT coalesce(json_agg(json_build_array(schemaname,tablename) ORDER BY schemaname,tablename),'[]'::json) FROM pg_tables WHERE schemaname <> 'information_schema' AND schemaname NOT LIKE 'pg_%'"))
    result = {}
    for schema, name in tables:
        quoted = '.'.join('"' + v.replace('"', '""') + '"' for v in (schema, name))
        result[schema + '.' + name] = int(query(env, 'SELECT count(*) FROM ' + quoted))
    return result


def terminate_peer(process):
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.wait()
    try:
        os.killpg(process.pid, 0)
    except ProcessLookupError:
        return
    # A surviving member cannot continue writing a supposedly sealed dump.
    os.killpg(process.pid, signal.SIGKILL)
    raise GateFailed('peer-process-group-survived')


def peer_output(args, output, log, timeout, capture=False):
    """Root owns both FDs; the peer never needs access to the private directory."""
    fd = None
    err = os.open(log, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        if output is not None:
            fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        try:
            process = subprocess.Popen(args, env={'PATH': '/usr/bin:/bin', 'LC_ALL': 'C'},
                                       stdin=subprocess.DEVNULL, stdout=subprocess.PIPE if capture else
                                       (fd if fd is not None else subprocess.DEVNULL),
                                       stderr=err, start_new_session=True)
            data, _ = process.communicate(timeout=timeout)
        except subprocess.TimeoutExpired:
            # Terminate the peer process group and wait. Partial private evidence
            # is never sealed or armed, even if termination was ambiguous.
            terminate_peer(process)
            raise GateFailed('peer-command-timeout-outcome-unknown') from None
        except OSError:
            raise GateFailed('peer-command-unavailable') from None
        terminate_peer(process)  # Also proves no successful child was left behind.
        require(process.returncode == 0, 'peer-command-failed')
        if fd is not None:
            os.fsync(fd)
    finally:
        if fd is not None:
            os.close(fd)
        os.close(err)
    if output is not None:
        regular(output)
    return data if capture else None


class PeerDatabase:
    """Existing OS peer access for read-only full catalog/counts and backup only."""
    def __init__(self, config, identity, work):
        self.config, self.identity, self.work = config, identity, work
        directory = absolute(config['socket_directory'])
        require(directory.is_dir() and directory.resolve() == directory, 'backup-socket-directory-invalid')
        require(re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]{0,62}', identity['schema']), 'backup-schema-invalid')
        self.env = {'PATH': '/usr/bin:/bin', 'LC_ALL': 'C', 'PGHOST': str(directory),
                    'PGPORT': str(config['port']), 'PGDATABASE': identity['database'],
                    'PGUSER': config['database_role'], 'PGPASSFILE': '/dev/null',
                    'PGOPTIONS': '-csearch_path=' + identity['schema'] +
                                 ' -crow_security=off -cdefault_transaction_read_only=on'}
        self.prefix = ['runuser', '-u', config['os_user'], '--', '/usr/bin/env', '-i',
                       *[k + '=' + v for k, v in self.env.items()]]
        self.query_nonce, self.query_count = os.urandom(8).hex(), 0

    def query(self, _env, sql):
        self.query_count += 1
        return peer_output(self.prefix + ['psql', '-XqAt', '--no-password', '-v', 'ON_ERROR_STOP=1', '-c', sql], None,
                           self.work / ('backup-query-' + self.query_nonce + '-' + str(self.query_count) + '.stderr'),
                           180, capture=True)

    def binding(self):
        require(database_identity(self.env, self.query) == self.identity, 'backup-database-mismatch')
        role = decode(self.query(self.env, "SELECT json_build_object('current_user',current_user,'session_user',session_user,'superuser',rolsuper,'bypass_rls',rolbypassrls) FROM pg_roles WHERE rolname=current_user"))
        require(role['current_user'] == role['session_user'] == self.config['database_role'], 'backup-effective-role-mismatch')
        return {'configuration': self.config, 'identity': self.identity, 'role': role}

    def verify(self, expected):
        require(self.binding() == expected, 'backup-role-or-identity-changed')

    def preflight(self, binding):
        self.verify(binding)
        capabilities = decode(self.query(self.env, "SELECT json_build_object("
            "'missing_table_select',(SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname <> 'information_schema' AND n.nspname !~ '^pg_' AND CASE WHEN c.relkind IN ('r','p','m') THEN NOT has_table_privilege(c.oid,'SELECT') ELSE false END),"
            "'missing_schema_usage',(SELECT count(*) FROM pg_namespace n WHERE n.nspname <> 'information_schema' AND n.nspname !~ '^pg_' AND NOT has_schema_privilege(n.oid,'USAGE')),"
            "'missing_sequence_select',(SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname <> 'information_schema' AND n.nspname !~ '^pg_' AND CASE WHEN c.relkind='S' THEN NOT has_sequence_privilege(c.oid,'SELECT') ELSE false END),"
            "'rls_without_bypass',(SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname <> 'information_schema' AND n.nspname !~ '^pg_' AND c.relrowsecurity AND NOT (SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user)),"
            "'missing_large_object_select',(SELECT count(*) FROM pg_largeobject_metadata WHERE NOT has_largeobject_privilege(oid,'SELECT')))"))
        require(set(capabilities) == {'missing_table_select', 'missing_schema_usage', 'missing_sequence_select',
                                     'rls_without_bypass', 'missing_large_object_select'} and
                all(type(v) is int and v == 0 for v in capabilities.values()), 'backup-read-capability-missing')
        # Actual reads catch catalog-only permission mistakes and RLS filtering.
        tables = database_table_counts(self.env, self.query)
        peer_output(self.prefix + ['pg_dump', '--no-password', '--schema-only'], None,
                    self.work / 'backup-preflight-schema.stderr', 180)
        self.verify(binding)
        value = {'passed': True, 'binding': binding, 'capabilities': capabilities,
                 'application_tables_read': len(tables), 'full_schema_dump_exit': 0}
        private_write(self.work / 'backup-preflight.json', encode(value))
        return value

    def counts(self, binding):
        self.verify(binding)
        value = database_table_counts(self.env, self.query)
        self.verify(binding)
        return value

    def fingerprint(self, binding, expected):
        self.verify(binding)
        value = fingerprints(self.env, expected, self.query)
        self.verify(binding)
        return value

    def backup(self, binding):
        self.verify(binding)
        path = self.work / 'database.dump'
        peer_output(self.prefix + ['pg_dump', '--no-password', '--format=custom'], path,
                    self.work / 'backup.stderr', 3600)
        command(['pg_restore', '--list', str(path)], log=self.work / 'backup-contents.log')
        self.verify(binding)
        return sha(path)


def module(path):
    spec = importlib.util.spec_from_file_location('normal_systemd_deploy', path)
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    return m


def response_line(fd, timeout):
    deadline, data = time.monotonic() + timeout, b''
    while b'\n' not in data:
        remaining = deadline - time.monotonic()
        require(remaining > 0, 'rpc-response-timeout')
        ready, _, _ = select.select([fd], [], [], remaining)
        require(ready, 'rpc-response-timeout')
        chunk = os.read(fd, min(4096, 65537 - len(data)))
        require(chunk and len(data) + len(chunk) <= 65536, 'rpc-response-lost-or-oversized')
        data += chunk
    require(data.endswith(b'\n') and data.count(b'\n') == 1, 'rpc-response-trailing-data')
    return data


def start_guardian(agent, plan, node):
    # SCM_RIGHTS duplicates the already-held flock file descriptions into a
    # supervised process. EOF/controller failure cannot release these locks.
    path = agent.work / 'guardian.sock'
    listener = socket.socket(socket.AF_UNIX)
    listener.bind(str(path))
    os.chmod(path, 0o600)
    listener.listen(1)
    listener.settimeout(30)
    name = 'lmm-cluster-lock-' + plan['id'] + '-' + node['name'] + '.service'
    command(['systemd-run', '--quiet', '--unit=' + name,
             '-p', 'Type=simple', '-p', 'MemoryMax=64M', '-p', 'NoNewPrivileges=yes',
             'python3', '-B', node['script'], 'guardian', '--socket', str(path)])
    try:
        conn, _ = listener.accept()
        with conn:
            pid, uid, _ = struct.unpack('3i', conn.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
            mainpid = command(['systemctl', 'show', name, '-p', 'MainPID', '--value']).strip()
            require(uid == 0 and mainpid == str(pid).encode(), 'guardian-peer-invalid')
            fds = array.array('i', agent.locks)
            conn.sendmsg([b'LOCKS'], [(socket.SOL_SOCKET, socket.SCM_RIGHTS, fds.tobytes())])
            conn.settimeout(10)
            require(conn.recv(32) == b'HELD', 'guardian-not-holding-locks')
    finally:
        listener.close()
        path.unlink()
    private_write(agent.work / 'guardian.json', encode({'unit': name, 'locks': list(LOCKS),
                                                       'plan_sha256': sha(node['plan']),
                                                       'database_backup': agent.backup_binding}))
    return name


def receive_guardian_locks(conn, targets):
    data, ancillary, flags, _ = conn.recvmsg(32, socket.CMSG_SPACE(3 * array.array('i').itemsize))
    require(data == b'LOCKS' and not flags & socket.MSG_CTRUNC, 'guardian-fds-missing')
    received = array.array('i')
    for level, kind, payload in ancillary:
        require(level == socket.SOL_SOCKET and kind == socket.SCM_RIGHTS, 'guardian-ancillary-invalid')
        received.frombytes(payload)
    require(len(received) == len(targets), 'guardian-lock-count')
    for fd, target in zip(received, targets):
        a, b = os.fstat(fd), Path(target).stat()
        require((a.st_dev, a.st_ino) == (b.st_dev, b.st_ino), 'guardian-lock-binding')
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
    conn.sendall(b'HELD')
    return received


def hold_guardian(path):
    require(os.geteuid() == 0, 'guardian-authority')
    conn = socket.socket(socket.AF_UNIX)
    conn.settimeout(30)
    conn.connect(str(absolute(str(path))))
    received = receive_guardian_locks(conn, LOCKS)
    conn.close()
    while True:
        signal.pause()


def bound_migrate(plan, node_name, identity_file, binary, mode):
    node = next(n for n in plan['nodes'] if n['name'] == node_name)
    regular(node['helper'], plan['helper_sha256'])
    regular(identity_file)
    expected = decode(Path(identity_file).read_bytes())
    require(binary in (node['candidate'], '/usr/bin/lmm-api-go') and mode in ('apply', 'verify') and
            (mode != 'apply' or binary == node['candidate']) and os.environ.get('NODE_TYPE') == 'master',
            'bound-migration-command-invalid')
    regular(binary, plan['candidate']['sha256'] if binary == node['candidate'] else node['provider_sha256'])
    helper = module(node['helper'])
    values = {os.fsencode(k): os.fsencode(v) for k, v in os.environ.items()}
    env = helper.database_environment_from_values(values)
    require(database_identity(env) == expected, 'effective-migration-database-mismatch')
    no_database_clients(env)
    require(not Path('.env').exists(), 'unexpected-working-environment-file')
    os.execv(binary, [binary, 'migrate', '--' + mode])


class NodeAgent:
    def __init__(self, plan, node):
        self.p, self.n = plan, node
        require(os.geteuid() == 0 and socket.gethostname() == node['hostname'], 'target-authority')
        regular(node['script'], plan['script_sha256'])
        regular(node['helper'], plan['helper_sha256'])
        self.plan_digest = sha(node['plan'])
        self.helper = module(node['helper'])
        self.locks = []
        for path in LOCKS:
            parent = Path(path).parent
            if path.startswith('/var/lib/lmm-api-deploy-systemd/') and not parent.exists():
                parent.mkdir(mode=0o700)
            require(parent.resolve() == parent, 'unsafe-lock-parent')
            fd = os.open(path, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
            st = os.fstat(fd)
            require(stat.S_ISREG(st.st_mode) and st.st_uid == 0 and st.st_nlink == 1 and
                    not st.st_mode & 0o022, 'unsafe-lock')
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            self.locks.append(fd)
        require(not os.path.lexists('/var/lib/lmm-api-go-deploy/transaction.lock'), 'native-lease-active')
        base = Path('/var/lib/lmm-api-cluster-schema')
        if not base.exists():
            base.mkdir(mode=0o700)
        require(base.resolve() == base and base.stat().st_uid == 0 and
                stat.S_IMODE(base.stat().st_mode) == 0o700, 'unsafe-cluster-root')
        self.work = new_private(base / (plan['id'] + '-' + node['name']))
        regular('/usr/bin/lmm-api-go', node['provider_sha256'])
        regular(node['candidate'], plan['candidate']['sha256'])
        self.before = unit()
        require(self.before['ActiveState'] == 'active' and self.before['SubState'] == 'running' and
                self.before['InvocationID'] == node['invocation'] and int(self.before['MainPID']) > 1,
                'old-writer-changed')
        self.env_files = self.helper.service_environment_files()
        self.env_hashes = {f: sha(f.lstrip('-')) for f in self.env_files}
        self.db_env = self.helper.database_environment()
        self.identity = database_identity(self.db_env)
        self.peer = PeerDatabase(plan['database_backup'], self.identity, self.work) if node['name'] == plan['database_owner'] else None
        self.backup_binding = self.peer.binding() if self.peer else None
        if self.peer:
            private_write(self.work / 'database-backup.json', encode(self.backup_binding))
        self.did_stop = self.armed = self.verified_old = self.did_start = self.dispatched = False
        private_write(self.work / 'original-writer.json', encode({
            'unit': self.before, 'agent_pid': os.getpid(), 'env_files': self.env_files,
            'env_hashes': self.env_hashes, 'plan_sha256': sha(node['plan'])}))
        self.guardian_unit = start_guardian(self, plan, node)
        private_write(self.work / 'database-identity.json', encode(self.identity))
        self.dump_sha = None

    def guard(self, needs_stop=False):
        regular(self.n['plan'], self.plan_digest)
        regular(self.n['script'], self.p['script_sha256'])
        regular(self.n['helper'], self.p['helper_sha256'])
        require(command(['systemctl', 'is-active', self.guardian_unit]).strip() == b'active', 'maintenance-guardian-lost')
        regular('/usr/bin/lmm-api-go', self.n['provider_sha256'])
        regular(self.n['candidate'], self.p['candidate']['sha256'])
        require(self.helper.service_environment_files() == self.env_files and
                {f: sha(f.lstrip('-')) for f in self.env_files} == self.env_hashes,
                'service-environment-changed')
        require(database_identity(self.db_env) == self.identity, 'database-identity-changed')
        regular(self.work / 'guardian.json')
        require(decode((self.work / 'guardian.json').read_bytes())['database_backup'] == self.backup_binding,
                'backup-guardian-binding-changed')
        if self.peer:
            regular(self.work / 'database-backup.json')
            require(decode((self.work / 'database-backup.json').read_bytes()) == self.backup_binding,
                    'backup-binding-changed')
            self.peer.verify(self.backup_binding)
        if needs_stop:
            require(self.did_stop, 'stop-not-proven')
            stopped(self.before['MainPID'])
        probe_all(self.n['probes'])

    def migrate(self, binary, mode, label):
        self.guard(True)
        no_database_clients(self.db_env)
        args = ['systemd-run', '--quiet', '--wait', '--collect', '--pipe',
                '--unit=lmm-cluster-' + self.p['id'] + '-' + label,
                '-p', 'Type=oneshot', '-p', 'TimeoutStartSec=180',
                *[a for f in self.env_files for a in ('-p', 'EnvironmentFile=' + f)],
                '-p', 'MemoryMax=384M', '-p', 'NoNewPrivileges=yes',
                '-p', 'ProtectSystem=strict', '-p', 'PrivateTmp=yes',
                '-p', 'WorkingDirectory=' + str(self.work),
                '-p', 'ReadWritePaths=' + str(self.work),
                '/usr/bin/env', 'NODE_TYPE=master', 'python3', '-B', self.n['script'],
                '_bound-migrate', '--plan', self.n['plan'], '--plan-sha256', self.plan_digest,
                '--node', self.n['name'], '--identity', str(self.work / 'database-identity.json'),
                '--binary', binary, '--mode', mode]
        command(args, log=self.work / (label + '.log'), timeout=200)

    def action(self, action, data):
        if action == 'preflight':
            self.guard()
            result = {'identity': self.identity}
            if self.peer:
                result['backup_preflight'] = self.peer.preflight(self.backup_binding)
                require(self.peer.fingerprint(self.backup_binding, self.p['expected_units']) ==
                        fingerprints(self.db_env, self.p['expected_units']), 'backup-app-fingerprint-mismatch')
            return result
        if action == 'stop':
            self.guard()
            require(unit() == self.before, 'old-invocation-changed')
            start = time.time()
            command(['systemctl', 'stop', 'lmm-api.service'], log=self.work / 'stop.log')
            stopped(self.before['MainPID'])
            journal = command(['journalctl', '--no-pager', '-o', 'cat',
                               '--since=@' + str(start), '-u', 'lmm-api.service',
                               '_PID=' + self.before['MainPID'],
                               '_SYSTEMD_INVOCATION_ID=' + self.n['invocation']])
            private_write(self.work / 'shutdown.log', journal)
            validate_shutdown(journal.decode())
            self.did_stop = True
            return {'shutdown_sha256': hashlib.sha256(journal).hexdigest()}
        if action == 'release':
            require(self.did_start and data == {'all_old_writers_ready': True}, 'release-not-ready')
            regular('/usr/bin/lmm-api-go', self.n['provider_sha256'])
            self.helper.healthy(self.n['version'])
            command(['systemctl', 'stop', self.guardian_unit])
            return {'maintenance_locks_released': True}
        self.guard(True)
        if action == 'check':
            return {'stopped': True}
        if action == 'baseline':
            no_database_clients(self.db_env)
            value = {'fingerprints': fingerprints(self.db_env, self.p['expected_units'])}
            if self.peer:
                require(self.peer.fingerprint(self.backup_binding, self.p['expected_units']) == value['fingerprints'],
                        'backup-app-fingerprint-mismatch')
                value['tables'] = self.peer.counts(self.backup_binding)
            if not (self.work / 'baseline-before.json').exists():
                private_write(self.work / 'baseline-before.json', encode(value))
            return value
        if action == 'backup':
            require(self.n['name'] == self.p['database_owner'], 'wrong-backup-owner')
            no_database_clients(self.db_env)
            self.dump_sha = self.peer.backup(self.backup_binding)
            return {'path': str(self.work / 'database.dump'), 'sha256': self.dump_sha}
        if action == 'arm':
            require(not self.dispatched and not self.armed and
                    self.n['name'] == self.p['database_owner'] and self.dump_sha and
                    data == {'dump_sha256': self.dump_sha, 'candidate_sha256': self.p['candidate']['sha256'],
                             'rehearsal_passed': True}, 'rehearsal-not-bound')
            regular(self.work / 'database.dump', self.dump_sha)
            self.armed = True
            return {'armed': True}
        if action == 'apply':
            require(self.armed, 'production-apply-not-armed')
            require(not self.dispatched, 'apply-already-dispatched')
            self.dispatched = True
            self.armed = False  # Exactly one dispatch; failures never make it replayable.
            self.migrate(self.n['candidate'], 'apply', 'candidate-apply')
            return {'applied': True}
        if action == 'verify':
            self.migrate(self.n['candidate'], 'verify', 'candidate-verify')
            self.migrate('/usr/bin/lmm-api-go', 'verify', 'rollback-verify')
            self.verified_old = True
            return {'candidate_and_rollback_verified': True}
        if action == 'start':
            require(self.verified_old, 'rollback-schema-not-verified')
            command(['systemctl', 'start', 'lmm-api.service'], log=self.work / 'restart-old.log')
            deadline = time.monotonic() + 120
            while True:
                try:
                    self.helper.healthy(self.n['version'])
                    break
                except Exception:
                    require(time.monotonic() < deadline, 'old-readiness-failed')
                    time.sleep(2)
            regular('/usr/bin/lmm-api-go', self.n['provider_sha256'])
            self.did_start = True
            return {'old_writer_ready': True, 'version': self.n['version']}
        raise GateFailed('unknown-action')


class RPC:
    def __init__(self, plan, node, digest, work):
        self.node = node
        self.stderr = (work / (node['name'] + '-agent.stderr')).open('xb')
        os.chmod(self.stderr.name, 0o600)
        self.proc = subprocess.Popen(['ssh', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes',
                                      '-o', 'ConnectTimeout=10', '-o', 'ControlMaster=no',
                                      '-o', 'ControlPath=none', '-o', 'ServerAliveInterval=15',
                                      '-o', 'ServerAliveCountMax=2', node['ssh'], 'python3', '-B',
                                      node['script'], 'agent', '--plan', node['plan'], '--plan-sha256',
                                      digest, '--node', node['name'], '--confirm', plan['confirmation']],
                                     stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.stderr)

    def call(self, action, data=None):
        try:
            self.proc.stdin.write(encode({'action': action, 'data': data}) + b'\n')
            self.proc.stdin.flush()
            line = response_line(self.proc.stdout.fileno(), 3600)
            value = decode(line)
            require(set(value) == {'ok', 'data'} and value['ok'] is True, 'agent-gate-failed')
            return value['data']
        except (OSError, ValueError):
            raise GateFailed('agent-outcome-unknown') from None

    def close(self):
        # Agent FDs close, while the independent guardian keeps the same open
        # flock descriptions until successful release or reviewed recovery.
        try:
            self.proc.stdin.close()
        except (OSError, BrokenPipeError):
            pass
        try:
            self.proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            pass
        self.stderr.close()


def journal(work, event, data):
    path = work / 'events.jsonl'
    previous = sha(path) if path.exists() else None
    record = encode({'event': event, 'previous_sha256': previous, 'data': data}) + b'\n'
    fd = os.open(path, os.O_WRONLY | os.O_APPEND | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'ab') as f:
        f.write(record)
        f.flush()
        os.fsync(f.fileno())


def coordinate(p, digest, work, connect=RPC, rehearse=None):
    clients = []
    try:
        probe_all(p['public_probes'])
        for node in p['nodes']:
            clients.append(connect(p, node, digest, work))
        preflights = [c.call('preflight') for c in clients]
        identities = [v['identity'] for v in preflights]
        require(all(v == identities[0] for v in identities), 'nodes-do-not-share-database')
        owner = next(c for c in clients if c.node['name'] == p['database_owner'])
        owner_preflight = preflights[clients.index(owner)].get('backup_preflight')
        require(isinstance(owner_preflight, dict) and owner_preflight.get('passed') is True and
                owner_preflight.get('full_schema_dump_exit') == 0 and
                owner_preflight.get('binding', {}).get('identity') == identities[0], 'backup-preflight-incomplete')
        journal(work, 'preflight', {'plan_sha256': digest, 'source_sha': p['source_sha'],
                                    'identity': identities[0], 'backup': owner_preflight})
        for c in clients:
            journal(work, 'writer-stopped', {'node': c.node['name'], **c.call('stop')})
        def check_all():
            probe_all(p['public_probes'])
            for c in clients:
                c.call('check')
        check_all()
        before = owner.call('baseline')
        require(set(before) == {'fingerprints', 'tables'}, 'owner-baseline-incomplete')
        private_write(work / 'baseline-before.json', encode(before))
        for c in clients:
            if c is not owner:
                other = c.call('baseline')
                require(set(other) == {'fingerprints'} and other['fingerprints'] == before['fingerprints'],
                        'node-baselines-differ')
        backup = owner.call('backup')
        target = work / 'database.dump'
        require(not target.exists(), 'backup-copy-already-exists')
        command(['scp', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes',
                 '-o', 'ControlMaster=no', '-o', 'ControlPath=none',
                 owner.node['ssh'] + ':' + backup['path'], str(target)], timeout=3600)
        os.chmod(target, 0o600)
        regular(target, backup['sha256'])
        journal(work, 'full-backup', {'sha256': backup['sha256']})
        result = (rehearse or run_rehearsal)(p, work, before, identities[0], backup['sha256'])
        require(result == {'actual_restore': True, 'candidate_and_all_n1_verified': True,
                           'fingerprints_unchanged': True}, 'rehearsal-incomplete')
        journal(work, 'rehearsal-passed', result)
        check_all()
        require(owner.call('baseline') == before, 'database-drifted-after-backup')
        owner.call('arm', {'dump_sha256': backup['sha256'],
                          'candidate_sha256': p['candidate']['sha256'], 'rehearsal_passed': True})
        # Persist intent before dispatch. Ambiguity leaves maintenance for review.
        journal(work, 'production-apply-dispatched', {'candidate_sha256': p['candidate']['sha256']})
        owner.call('apply')
        for c in clients:
            check_all()
            c.call('verify')
        after = owner.call('baseline')
        require(after['fingerprints'] == before['fingerprints'], 'production-money-drift')
        for c in clients:
            if c is not owner:
                other = c.call('baseline')
                require(set(other) == {'fingerprints'} and other['fingerprints'] == before['fingerprints'],
                        'production-node-money-drift')
        journal(work, 'production-schema-verified', {'fingerprints': after['fingerprints']})
        check_all()
        for c in clients:
            journal(work, 'old-writer-ready', {'node': c.node['name'], **c.call('start')})
        journal(work, 'all-old-writers-ready', {'scope': 'schema maintenance only; ingress stays closed'})
        for c in clients:
            c.call('release', {'all_old_writers_ready': True})
        journal(work, 'complete', {'scope': 'normal deployment and ingress acceptance still required'})
        return True
    except Exception:
        journal(work, 'recovery-required', {'automatic_retry_restart_or_restore': False})
        raise
    finally:
        for c in clients:
            c.close()


def recovery_context(p, n):
    require(os.geteuid() == 0 and socket.gethostname() == n['hostname'], 'recovery-target')
    work = Path('/var/lib/lmm-api-cluster-schema') / (p['id'] + '-' + n['name'])
    require(work.resolve() == work and work.stat().st_uid == 0 and
            stat.S_IMODE(work.stat().st_mode) == 0o700, 'recovery-work-invalid')
    for filename in ('original-writer.json', 'guardian.json', 'database-identity.json', 'baseline-before.json'):
        regular(work / filename)
    original = decode((work / 'original-writer.json').read_bytes())
    guardian = decode((work / 'guardian.json').read_bytes())
    require(original['plan_sha256'] == guardian['plan_sha256'] == sha(n['plan']), 'recovery-plan-changed')
    require(not Path('/proc', str(original['agent_pid'])).exists(), 'original-agent-not-exited')
    require(command(['systemctl', 'is-active', guardian['unit']]).strip() == b'active', 'recovery-lock-owner-lost')
    regular(n['script'], p['script_sha256'])
    regular(n['helper'], p['helper_sha256'])
    regular(n['candidate'], p['candidate']['sha256'])
    regular('/usr/bin/lmm-api-go', n['provider_sha256'])
    helper = module(n['helper'])
    require(helper.service_environment_files() == original['env_files'] and
            {f: sha(f.lstrip('-')) for f in original['env_files']} == original['env_hashes'],
            'recovery-service-environment-changed')
    identity = decode((work / 'database-identity.json').read_bytes())
    if n['name'] == p['database_owner']:
        regular(work / 'database-backup.json')
        binding = decode((work / 'database-backup.json').read_bytes())
        require(binding['configuration'] == p['database_backup'] and binding['identity'] == identity and
                guardian['database_backup'] == binding, 'recovery-backup-binding-changed')
        PeerDatabase(p['database_backup'], identity, work).verify(binding)
    else:
        require(guardian['database_backup'] is None and
                set(decode((work / 'baseline-before.json').read_bytes())) == {'fingerprints'},
                'recovery-nonowner-baseline-invalid')
    probe_all(n['probes'])
    return work, original, guardian, helper


def recovery_stopped(original):
    current = unit()
    pid = current['ExecMainPID']
    invocation = current['InvocationID']
    require(int(pid) > 1 and re.fullmatch(r'[0-9a-f]{32}', invocation), 'recovery-stop-not-observed')
    stopped(pid)
    # A partial recovery may have restarted an old writer. Operators first
    # stop it normally; freshly prove its actual last shutdown, never reuse the
    # original PID as evidence for a later invocation.
    full_journal = command(['journalctl', '--no-pager', '--all', '-o', 'json', '-u', 'lmm-api.service',
                            '_PID=' + pid, '_SYSTEMD_INVOCATION_ID=' + invocation])
    _, evidence = shutdown_window(full_journal, pid, invocation)
    return {'pid': pid, 'invocation': invocation, **evidence}


def shutdown_window(full_journal, pid, invocation):
    """Qualify one actual stop, retaining the hash of its complete invocation.

    Running services periodically flush quota. Those earlier reports cannot be
    shutdown evidence; neither can another PID/Invocation or a second stop.
    """
    messages, starts, exits, timestamps, boots = [], [], [], [], set()
    for line in full_journal.splitlines():
        try:
            record = decode(line)
        except (ValueError, TypeError):
            raise GateFailed('recovery-journal-json-invalid') from None
        require(isinstance(record, dict), 'recovery-journal-record-invalid')
        require(record.get('_PID') == pid and record.get('_SYSTEMD_INVOCATION_ID') == invocation,
                'recovery-journal-binding-mismatch')
        text = journal_message(record)
        timestamp, boot = record.get('__MONOTONIC_TIMESTAMP'), record.get('_BOOT_ID')
        require(isinstance(text, str) and isinstance(timestamp, str) and timestamp.isdigit() and
                isinstance(boot, str) and re.fullmatch(r'[0-9a-f]{32}', boot), 'recovery-journal-record-invalid')
        timestamps.append(int(timestamp)); boots.add(boot)
        normalized = re.sub(r'^\[sys\] [0-9/ -:]+ \| ', '', text.strip().lower())
        if normalized.startswith('received signal:'):
            starts.append(len(messages))
        if normalized == 'server exited':
            exits.append(len(messages))
        messages.append(text)
    require(len(boots) == 1 and timestamps == sorted(timestamps) and
            len(starts) == len(exits) == 1 and starts[0] < exits[0], 'recovery-shutdown-window-missing-or-duplicated')
    start, end = starts[0], exits[0]
    require(not any('refund_tasks' in v.lower() for v in messages[:start] + messages[end+1:]),
            'recovery-refund-report-outside-shutdown')
    window = ('\n'.join(messages[start:end+1]) + '\n').encode()
    validate_shutdown(window.decode())
    return window, {'full_invocation_journal_sha256': hashlib.sha256(full_journal).hexdigest(),
                    'shutdown_sha256': hashlib.sha256(window).hexdigest(),
                    'shutdown_start_monotonic_us': timestamps[start],
                    'shutdown_end_monotonic_us': timestamps[end], 'boot_id': next(iter(boots)),
                    'dashboard_flush_reports': sum('quota dashboard flush:' in m.lower() for m in messages[start:end+1])}


def journal_message(record):
    text = record.get('MESSAGE')
    # journalctl JSON represents non-printable UTF-8 messages as byte arrays
    # (for example GORM's ANSI colours); preserve their exact decoded text.
    if isinstance(text, list):
        require(all(type(v) is int and 0 <= v <= 255 for v in text), 'recovery-journal-message-invalid')
        try:
            text = bytes(text).decode('utf-8')
        except UnicodeDecodeError:
            raise GateFailed('recovery-journal-message-invalid') from None
    require(isinstance(text, str), 'recovery-journal-message-invalid')
    return text


def recovery_check(p, n, attempt):
    # Called only by the normal systemd-run oneshot with ordered EnvironmentFiles.
    work, original, _, helper = recovery_context(p, n)
    logs = work / ('recovery-' + attempt)
    require(logs.resolve() == logs and logs.is_dir(), 'recovery-attempt-missing')
    shutdown = recovery_stopped(original)
    require(os.environ.get('NODE_TYPE') == 'master' and not Path('.env').exists(), 'recovery-environment-invalid')
    env = helper.database_environment_from_values({os.fsencode(k): os.fsencode(v) for k, v in os.environ.items()})
    identity = decode((work / 'database-identity.json').read_bytes())
    require(database_identity(env) == identity, 'recovery-database-mismatch')
    no_database_clients(env)
    baseline = decode((work / 'baseline-before.json').read_bytes())
    require(fingerprints(env, p['expected_units']) == baseline['fingerprints'], 'recovery-money-drift')
    if n['name'] == p['database_owner']:
        require(set(baseline) == {'fingerprints', 'tables'}, 'recovery-owner-baseline-invalid')
        binding = decode((work / 'database-backup.json').read_bytes())
        require(PeerDatabase(p['database_backup'], identity, work).fingerprint(binding, p['expected_units']) ==
                baseline['fingerprints'], 'recovery-backup-money-drift')
    candidate_verified = True
    try:
        command([n['candidate'], 'migrate', '--verify'], log=logs / 'candidate-verify.log')
    except GateFailed:
        # Before apply, an older compatible schema can legitimately be missing
        # candidate additions. Recovery restores only the verified old writer.
        candidate_verified = False
    command(['/usr/bin/lmm-api-go', 'migrate', '--verify'], log=logs / 'rollback-verify.log')
    require(fingerprints(env, p['expected_units']) == baseline['fingerprints'], 'recovery-verify-money-drift')
    receipt = {'verified': True, 'plan_sha256': sha(n['plan']), 'fingerprints': baseline['fingerprints'],
               'identity': identity, 'rollback_verified': True, 'candidate_verified': candidate_verified,
               'shutdown': shutdown}
    private_write(logs / 'verified.json', encode(receipt))
    return receipt


def recovery_node(p, n, phase, all_verified, attempt):
    work, original, guardian, helper = recovery_context(p, n)
    require(re.fullmatch(NAME, attempt), 'recovery-attempt-required')
    logs = work / ('recovery-' + attempt)
    if phase == 'verify':
        new_private(logs)
        recovery_stopped(original)
        args = ['systemd-run', '--quiet', '--wait', '--collect', '--pipe',
                '--unit=lmm-cluster-' + p['id'] + '-' + n['name'] + '-recovery-' + attempt,
                '-p', 'Type=oneshot', '-p', 'TimeoutStartSec=180',
                *[a for f in original['env_files'] for a in ('-p', 'EnvironmentFile=' + f)],
                '-p', 'MemoryMax=384M', '-p', 'NoNewPrivileges=yes', '-p', 'ProtectSystem=strict',
                '-p', 'PrivateTmp=yes', '-p', 'WorkingDirectory=' + str(work),
                '-p', 'ReadWritePaths=' + str(work), '/usr/bin/env', 'NODE_TYPE=master',
                'python3', '-B', n['script'], '_recovery-check', '--plan', n['plan'],
                '--plan-sha256', sha(n['plan']), '--node', n['name'], '--recovery-id', attempt]
        command(args, log=logs / 'systemd-verify.log', timeout=200)
        return decode((logs / 'verified.json').read_bytes())
    regular(logs / 'verified.json')
    verified = decode((logs / 'verified.json').read_bytes())
    require(all_verified and verified['plan_sha256'] == sha(n['plan']), 'all-node-verification-required')
    if phase == 'start':
        recovery_stopped(original)
        command(['systemctl', 'start', 'lmm-api.service'], log=logs / 'restart.log')
        deadline = time.monotonic() + 120
        while True:
            try:
                helper.healthy(n['version'])
                break
            except Exception:
                require(time.monotonic() < deadline, 'recovery-readiness-failed')
                time.sleep(2)
        return {'old_writer_ready': True}
    require(phase == 'release', 'recovery-phase-invalid')
    helper.healthy(n['version'])
    command(['systemctl', 'stop', guardian['unit']])
    return {'maintenance_locks_released': True}


def recover(p, digest, work, attempt):
    # Recovery never applies/restores the schema or changes deployment state.
    def call(n, phase, all_verified=False):
        args = ['ssh', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes',
                '-o', 'ConnectTimeout=10', '-o', 'ControlMaster=no', '-o', 'ControlPath=none',
                n['ssh'], 'python3', '-B', n['script'], 'recover-node', '--plan', n['plan'],
                '--plan-sha256', digest, '--node', n['name'], '--confirm', p['confirmation'], '--phase', phase,
                '--recovery-id', attempt]
        if all_verified:
            args.append('--all-nodes-verified')
        return decode(command(args, log=work / (n['name'] + '-' + phase + '.log'), timeout=400))
    try:
        probe_all(p['public_probes'])
        results = [call(n, 'verify') for n in p['nodes']]
        require(all(r['verified'] and r['rollback_verified'] and
                    r['identity'] == results[0]['identity'] and
                    r['fingerprints'] == results[0]['fingerprints'] for r in results), 'recovery-verification-incomplete')
        journal(work, 'all-old-binaries-compatible', {'plan_sha256': digest})
        for n in p['nodes']:
            probe_all(p['public_probes'])
            require(call(n, 'start', True) == {'old_writer_ready': True}, 'recovery-old-readiness-failed')
        for n in p['nodes']:
            require(call(n, 'release', True) == {'maintenance_locks_released': True}, 'recovery-release-failed')
        journal(work, 'recovery-complete', {'scope': 'old writers restored; ingress remains closed'})
    except Exception:
        journal(work, 'recovery-required', {'automatic_retry_restart_or_restore': False})
        raise


def rehearsal(p, work, baseline, identity, dump_sha, parent_network_namespace):
    """Run only inside a fresh user+network namespace, with TCP loopback PG."""
    require(os.geteuid() != 0 and str(Path('/proc/self/ns/net').readlink()) != parent_network_namespace, 'rehearsal-not-isolated')
    links = decode(command(['ip', '-j', 'link', 'show']))
    require(len(links) == 1 and links[0]['ifname'] == 'lo' and 'UP' in links[0]['flags'], 'rehearsal-has-external-interface')
    root = Path(p['rehearsal_root'])
    require(root.is_dir() and not root.is_symlink() and not list(root.iterdir()) and
            stat.S_IMODE(root.stat().st_mode) == 0o700, 'rehearsal-root-not-new')
    pgdata, sockets = root / 'data', root / 'sockets'
    sockets.mkdir(mode=0o700)
    require(len(str(sockets)) < 80, 'socket-directory-too-long')
    env = {'PATH': os.environ['PATH'], 'HOME': str(root), 'LC_ALL': 'C',
           'PGHOST': '127.0.0.1', 'PGPORT': '5432', 'PGDATABASE': 'lmm_test_cluster',
           'PGUSER': 'lmm_test_cluster', 'PGOPTIONS': '-csearch_path=' + identity['schema']}
    command(['initdb', '-D', str(pgdata), '-U', env['PGUSER'], '-A', 'trust', '--encoding=UTF8', '--no-instructions'],
            env=env, log=work / 'clone-initdb.log')
    started = False
    try:
        command(['pg_ctl', '-D', str(pgdata), '-l', str(root / 'postgres.log'), '-o',
                 "-c listen_addresses=127.0.0.1 -c unix_socket_directories=" + str(sockets) + ' -c port=5432',
                 '-w', 'start'], env=env, log=work / 'clone-pg-start.log')
        started = True
        command(['createdb', '--no-password', env['PGDATABASE']], env=env)
        clone = database_identity({**env, 'PGOPTIONS': ''})
        require((clone['system'], clone['oid']) != (identity['system'], identity['oid']) and
                clone['database'] == 'lmm_test_cluster', 'restore-target-is-production')
        regular(work / 'database.dump', dump_sha)
        command(['pg_restore', '--no-password', '--exit-on-error', '--single-transaction',
                 '--clean', '--if-exists', '--no-owner', '--no-acl', '--dbname', env['PGDATABASE'], str(work / 'database.dump')],
                env=env, log=work / 'clone-actual-restore.log', timeout=3600)
        require(database_table_counts(env) == baseline['tables'] and
                fingerprints(env, p['expected_units']) == baseline['fingerprints'], 'actual-restore-mismatch')
        u = urllib.parse.urlencode({'sslmode': 'disable', 'search_path': identity['schema']})
        app = {'PATH': env['PATH'], 'HOME': str(root), 'LC_ALL': 'C',
               'SQL_DSN': 'postgresql://lmm_test_cluster@127.0.0.1:5432/lmm_test_cluster?' + u,
               'NODE_TYPE': 'master', 'REDIS_CONN_STRING': '', 'LOG_SQL_DSN': '',
               'SESSION_SECRET': 'isolated-cluster-synthetic-session-secret',
               'CRYPTO_SECRET': 'isolated-cluster-synthetic-crypto-secret'}
        runs = [(p['candidate']['path'], p['candidate']['sha256'], 'apply', 'candidate-apply'),
                (p['candidate']['path'], p['candidate']['sha256'], 'verify', 'candidate-verify')]
        runs += [(n['rollback'], n['provider_sha256'], 'verify', n['name'] + '-rollback-verify') for n in p['nodes']]
        for binary, digest, mode, label in runs:
            regular(binary, digest)
            command([binary, 'migrate', '--' + mode], env=app, log=work / ('clone-' + label + '.log'), cwd=root)
        require(fingerprints(env, p['expected_units']) == baseline['fingerprints'], 'clone-money-drift')
        return {'actual_restore': True, 'candidate_and_all_n1_verified': True, 'fingerprints_unchanged': True}
    finally:
        if started:
            # This is the disposable clone, never a production writer.
            command(['pg_ctl', '-D', str(pgdata), '-m', 'fast', '-w', 'stop'], env=env,
                    log=work / 'clone-pg-stop.log')


def run_rehearsal(p, work, baseline, identity, dump_sha):
    # No home/profile/credential directory or host /proc is exposed. The PG
    # server and native CLIs share only private loopback in this mount namespace.
    root = new_private(p['rehearsal_root'])
    namespace = str(Path('/proc/self/ns/net').readlink())
    request = work / 'rehearsal-request.json'
    private_write(request, encode({'plan': p, 'work': str(work), 'baseline': baseline,
                                  'identity': identity, 'dump_sha256': dump_sha,
                                  'parent_network_namespace': namespace}))
    paths = {str(Path(__file__).resolve()), p['candidate']['path'], *[n['rollback'] for n in p['nodes']]}
    args = isolated_args(work, root, paths)
    args += ['--chdir', str(root), '--', 'python3', '-B', str(Path(__file__).resolve()),
             '_rehearse', '--request', str(request)]
    output = command(args, log=work / 'rehearsal-process.log', timeout=3600)
    return decode(output)


def isolated_args(work, root, paths):
    passwd = work / 'clone-passwd'
    group = work / 'clone-group'
    private_write(passwd, ('task:x:' + str(os.getuid()) + ':' + str(os.getgid()) + ':task:/task:/bin/false\n').encode())
    private_write(group, ('task:x:' + str(os.getgid()) + ':\n').encode())
    args = ['bwrap', '--clearenv', '--setenv', 'PATH', '/usr/bin:/bin',
            '--setenv', 'LC_ALL', 'C', '--unshare-user', '--unshare-net', '--unshare-pid',
            '--die-with-parent', '--new-session',
            '--ro-bind', '/usr', '/usr', '--symlink', 'usr/lib', '/lib',
            '--symlink', 'usr/lib', '/lib64', '--symlink', 'usr/bin', '/bin',
            '--proc', '/proc', '--dev', '/dev', '--tmpfs', '/tmp',
            '--ro-bind', str(passwd), '/etc/passwd', '--ro-bind', str(group), '/etc/group',
            '--bind', str(work), str(work), '--bind', str(root), str(root)]
    for path in sorted(paths):
        args += ['--ro-bind', path, path]
    return args


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('validate', 'run', 'agent', '_rehearse', 'guardian', '_bound-migrate',
                                         'recover', 'recover-node', '_recovery-check'))
    parser.add_argument('--plan', type=Path)
    parser.add_argument('--plan-sha256')
    parser.add_argument('--confirm')
    parser.add_argument('--work', type=Path)
    parser.add_argument('--node')
    parser.add_argument('--request', type=Path)
    parser.add_argument('--socket', type=Path)
    parser.add_argument('--identity', type=Path)
    parser.add_argument('--binary')
    parser.add_argument('--mode', choices=('apply', 'verify'))
    parser.add_argument('--execute-migration', action='store_true')
    parser.add_argument('--phase', choices=('verify', 'start', 'release'))
    parser.add_argument('--all-nodes-verified', action='store_true')
    parser.add_argument('--recovery-id')
    args = parser.parse_args()
    os.umask(0o077)
    if args.action == 'guardian':
        hold_guardian(args.socket)
        return
    if args.action == '_rehearse':
        regular(args.request)
        r = decode(args.request.read_bytes())
        result = rehearsal(validate_plan(r['plan']), Path(r['work']), r['baseline'], r['identity'], r['dump_sha256'], r['parent_network_namespace'])
        print(encode(result).decode())
        return
    require(args.plan and args.plan_sha256 and re.fullmatch(HEX, args.plan_sha256), 'plan-required')
    p = load_plan(args.plan, args.plan_sha256)
    if args.action == '_bound-migrate':
        bound_migrate(p, args.node, args.identity, args.binary, args.mode)
        return
    if args.action == '_recovery-check':
        n = next(n for n in p['nodes'] if n['name'] == args.node)
        recovery_check(p, n, args.recovery_id)
        return
    if args.action == 'validate':
        print('{"valid":true,"scope":"plan only; no host or database access"}')
        return
    require(args.confirm == p['confirmation'], 'confirmation-required')
    if args.action == 'recover-node':
        n = next(n for n in p['nodes'] if n['name'] == args.node)
        print(encode(recovery_node(p, n, args.phase, args.all_nodes_verified, args.recovery_id)).decode())
        return
    if args.action == 'agent':
        n = next(n for n in p['nodes'] if n['name'] == args.node)
        agent = NodeAgent(p, n)
        for line in sys.stdin.buffer:
            try:
                require(len(line) <= 65536, 'oversized-request')
                req = decode(line)
                require(set(req) == {'action', 'data'}, 'request-fields')
                data = agent.action(req['action'], req['data'])
                print(encode({'ok': True, 'data': data}).decode(), flush=True)
            except Exception:
                print('{"ok":false,"data":{"outcome":"recovery-required"}}', flush=True)
                break
        return
    require((args.execute_migration or args.action == 'recover') and args.work and os.geteuid() != 0,
            'explicit-nonroot-controller-required')
    regular(Path(__file__).resolve(), p['script_sha256'])
    regular(p['candidate']['path'], p['candidate']['sha256'])
    for n in p['nodes']:
        regular(n['rollback'], n['provider_sha256'])
    if args.action == 'recover':
        require(args.recovery_id and re.fullmatch(NAME, args.recovery_id), 'recovery-attempt-required')
        recover(p, args.plan_sha256, new_private(args.work), args.recovery_id)
        print('{"recovered":true,"scope":"old writers restored; ingress remains closed"}')
        return
    for tool in ('ssh', 'scp', 'bwrap', 'ip', 'initdb', 'pg_ctl', 'psql', 'pg_restore', 'createdb'):
        require(shutil.which(tool), 'missing-local-tool')
    command(['bwrap', '--unshare-user', '--unshare-net', '--unshare-pid',
             '--ro-bind', '/usr', '/usr', '--symlink', 'usr/lib', '/lib',
             '--symlink', 'usr/lib', '/lib64', '--symlink', 'usr/bin', '/bin', '--proc', '/proc', '--dev', '/dev', '--', 'true'])
    coordinate(p, args.plan_sha256, new_private(args.work))
    print('{"completed":true,"scope":"schema maintenance; normal deployment and ingress acceptance still required"}')


if __name__ == '__main__':
    try:
        main()
    except Exception:
        # Do not print exception/tool text: it can contain database credentials.
        print('shared PostgreSQL maintenance stopped; inspect private evidence, do not retry or resume', file=sys.stderr)
        sys.exit(1)
