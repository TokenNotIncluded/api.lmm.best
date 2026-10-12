"""A09-owned, disposable PostgreSQL resources on the local Docker socket only.

No external URL, existing cluster, volume, image pull or production credential
is accepted. This is a test helper, not an installed LMM program. Failed or
ambiguous provisioning is retained, never retried or swept by a name prefix.
"""
from __future__ import annotations

import json
import os
from pathlib import Path
import re
import secrets
import shutil
import subprocess
import time

from build_lease import private_directory

IMAGE = 'postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873'
DOCKER = ['docker', '--host', 'unix:///var/run/docker.sock']
LABEL = 'best.lmm.a09-fixture'
TASKS = {f'A{i:02d}' for i in range(1, 11)} | {'S11'}
NAME = re.compile(r'[a-z][a-z0-9_]{0,62}')


class FixtureError(RuntimeError):
    """The resource must not be adopted, retried or removed on this error."""


class PermissionDenied(FixtureError):
    """PostgreSQL reported SQLSTATE 42501, not merely a driver failure."""


def require_tools() -> None:
    if not shutil.which('docker') or not Path('/var/run/docker.sock').is_socket():
        raise FixtureError('BLOCKED: local Docker Engine and the reviewed cached image are required')


def clean_environment() -> dict:
    return {key: value for key, value in os.environ.items()
            if not key.startswith(('PG', 'DOCKER_')) and key not in {'DATABASE_URL', 'SQL_DSN'}}


def write_private(path: Path, value: str) -> None:
    private_directory(path.parent)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write(value)
        stream.flush()
        os.fsync(stream.fileno())
    parent = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(parent)
    finally:
        os.close(parent)


def identifier(value: str) -> str:
    if not isinstance(value, str) or not NAME.fullmatch(value):
        raise FixtureError('invalid synthetic PostgreSQL identifier')
    return '"' + value + '"'


def container_binding(info: dict, nonce: str, container: str) -> dict:
    """Inspect immutable ownership and required sandbox settings before use."""
    try:
        host = info['HostConfig']
        state = info['State']
        ports = info['NetworkSettings']['Ports']['5432/tcp']
        if (info['Id'] != container or info['Config']['Labels'].get(LABEL) != nonce
                or info['Config']['Image'] != IMAGE or not state['Running']
                or host['Privileged'] or host.get('Binds')
                or host['Memory'] != 512 * 1024**2 or host['NanoCpus'] != 10**9
                or host['PidsLimit'] != 128
                or host.get('Tmpfs') != {'/var/lib/postgresql': 'rw,size=402653184'}
                or len(ports) != 1 or ports[0]['HostIp'] != '127.0.0.1'
                or any(mount['Type'] in {'bind', 'volume'} for mount in info['Mounts'])):
            raise FixtureError('owned fixture boundaries changed')
        port = int(ports[0]['HostPort'])
        if not 1024 <= port <= 65535 or port in {18080, 18081, 50837, 47399}:
            raise FixtureError('fixture must use a new unprotected loopback port')
        return {'container': container, 'image_id': info['Image'],
                'started_at': state['StartedAt'], 'pid': state['Pid'], 'port': port}
    except (KeyError, TypeError, ValueError, AttributeError):
        raise FixtureError('fixture identity is incomplete or UNKNOWN') from None


class Fixture:
    def __init__(self, parent: Path):
        private_directory(parent)
        self.nonce = secrets.token_hex(12)
        self.root = parent / ('a09-pg-' + self.nonce)
        self.root.mkdir(mode=0o700)
        self.name = self.root.name
        self.container = None
        self.binding = None
        self.identity = None
        self.allocations = {}
        self.databases = set()
        self.restore_targets = set()
        self.boot = Path('/proc/sys/kernel/random/boot_id').read_text().strip()
        self.env = clean_environment()
        config = self.root / 'docker-config'
        config.mkdir(mode=0o700)
        self.env['DOCKER_CONFIG'] = str(config)

    def record(self, phase: str, values: dict) -> None:
        write_private(self.root/(phase+'.json'), json.dumps(
            {'format': 'a09-postgres-resource-v1', 'nonce': self.nonce, **values},
            sort_keys=True)+'\n')

    def docker(self, *args, data=None, env=None, timeout=60, allow_failure=False):
        try:
            result = subprocess.run([*DOCKER, *args], input=data, capture_output=True,
                                    env=self.env if env is None else env, timeout=timeout)
        except (OSError, subprocess.TimeoutExpired):
            raise FixtureError('Docker result is UNKNOWN; no automatic replay or cleanup') from None
        if result.returncode and not allow_failure:
            # Do not expose Docker inspect Env, SQL, passwords or driver errors.
            raise FixtureError('isolated Docker operation failed; resource retained')
        return result

    def sql(self, sql: str, database='postgres', *, role=None, password=None):
        if database != 'postgres' and database not in self.databases:
            raise FixtureError('database was not allocated by this fixture')
        identifier(database)
        args = ['exec', '-i']
        env = self.env.copy()
        if role:
            identifier(role)
            if not isinstance(password, str):
                raise FixtureError('runtime authentication requires a private fixture credential')
            env['PGPASSWORD'] = password
            args += ['--env', 'PGPASSWORD']  # Name only; value is never in argv.
        args += [self.container, 'psql', '-XqAt', '--no-password', '-v', 'ON_ERROR_STOP=1',
                 '-v', 'VERBOSITY=verbose', '-U', role or 'a09_admin', '-d', database]
        if role:
            args += ['-h', '127.0.0.1']
        result = self.docker(*args, data=sql.encode(), env=env, allow_failure=True)
        if result.returncode:
            error = re.search(rb'(ERROR|FATAL):\s+([0-9A-Z]{5}):', result.stderr)
            if result.returncode in {2, 3} and error and error[2] == b'42501':
                raise PermissionDenied('PostgreSQL denied the operation with SQLSTATE 42501')
            raise FixtureError('isolated SQL failed; not proof of a permission denial')
        return result.stdout.decode().strip()

    def inspect(self):
        if not self.container:
            raise FixtureError('no owned container identity')
        try:
            info = json.loads(self.docker('inspect', self.container).stdout)
            if len(info) != 1:
                raise FixtureError('ambiguous container identity')
            return container_binding(info[0], self.nonce, self.container)
        except (json.JSONDecodeError, TypeError):
            raise FixtureError('unreadable container identity') from None

    def database_identity(self):
        return json.loads(self.sql("SELECT json_build_object("
            "'system_id',system_identifier::text,'postmaster_start',pg_postmaster_start_time()::text,"
            "'datadir',current_setting('data_directory'),'socket',current_setting('unix_socket_directories'),"
            "'version',current_setting('server_version')) FROM pg_control_system();"))

    def verify(self):
        if (not self.binding or not self.identity
                or self.inspect() != self.binding or self.database_identity() != self.identity
                or Path('/proc/sys/kernel/random/boot_id').read_text().strip() != self.boot):
            raise FixtureError('owned cluster identity changed or is UNKNOWN')

    def start(self):
        if self.container is not None or (self.root / 'create-intent.json').exists():
            raise FixtureError('creation is one-shot; inspect the original resource')
        require_tools()
        self.docker('image', 'inspect', IMAGE)  # Never download an image implicitly.
        password = secrets.token_hex(32)
        config = self.root / 'postgres.env'
        write_private(config, f'POSTGRES_USER=a09_admin\nPOSTGRES_DB=postgres\nPOSTGRES_PASSWORD={password}\n')
        self.record('create-intent', {'name': self.name, 'image': IMAGE, 'host_boot_id': self.boot})
        self.container = self.docker('create', '--pull=never', '--name', self.name,
            '--label', LABEL+'='+self.nonce, '--memory', '512m', '--cpus', '1',
            '--pids-limit', '128', '--env-file', str(config), '--publish', '127.0.0.1::5432',
            '--tmpfs', '/var/lib/postgresql:rw,size=402653184', IMAGE,
            '-c', 'shared_buffers=16MB', '-c', 'max_connections=32', '-c', 'log_statement=none',
            '-c', 'log_min_error_statement=panic').stdout.decode().strip()
        if not re.fullmatch('[0-9a-f]{64}', self.container):
            raise FixtureError('container creation response is UNKNOWN; do not retry')
        self.record('created', {'container': self.container})
        self.docker('start', self.container)
        for _ in range(100):
            result = self.docker('exec', self.container, 'pg_isready', '-h', '127.0.0.1',
                                 '-U', 'a09_admin', '-d', 'postgres', allow_failure=True)
            if result.returncode == 0:
                break
            time.sleep(0.2)
        else:
            raise FixtureError('isolated PostgreSQL did not become ready')
        self.binding = self.inspect()
        self.identity = self.database_identity()
        if not self.identity['version'].startswith('18.6'):
            raise FixtureError('unexpected PostgreSQL version in the reviewed image')
        self.record('ready', {**self.binding, **self.identity, 'unit': 'container:'+self.container,
                              'host_boot_id': self.boot})

    def allocate(self, task: str) -> dict:
        if task not in TASKS or task in self.allocations:
            raise FixtureError('unknown task or duplicate allocation')
        self.verify()
        directory = self.root / task
        directory.mkdir(mode=0o700, exist_ok=False)
        database = f'a09_{self.nonce[:12]}_{task.lower()}'
        owner, installer, runtime = database+'_o', database+'_i', database+'_r'
        installation_password, runtime_password = secrets.token_hex(32), secrets.token_hex(32)
        for value in (database, owner, installer, runtime):
            identifier(value)
        # Privileges are explicit, local and separated; no role is supplied by a caller.
        self.sql(f'BEGIN; CREATE ROLE {owner} NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS; '
            f"CREATE ROLE {installer} LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '{installation_password}'; "
            f"CREATE ROLE {runtime} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '{runtime_password}'; "
            f'GRANT {owner} TO {installer}; COMMIT;')
        self.sql(f'CREATE DATABASE {database} OWNER {owner} TEMPLATE template0;')
        self.databases.add(database)
        self.sql(f'REVOKE ALL ON DATABASE {database} FROM PUBLIC; GRANT CONNECT ON DATABASE {database} TO {installer},{runtime};')
        self.sql(f'ALTER SCHEMA public OWNER TO {owner}; REVOKE ALL ON SCHEMA public FROM PUBLIC;', database)
        for purpose, role, secret in [('owner', installer, installation_password), ('runtime', runtime, runtime_password)]:
            write_private(directory/(purpose+'.pgpass'),
                          f'127.0.0.1:{self.binding["port"]}:{database}:{role}:{secret}\n')
        record = {'task': task, 'database': database, 'owner': owner, 'installer': installer,
                  'runtime': runtime, 'port': self.binding['port'], 'system_id': self.identity['system_id']}
        write_private(directory/'resource.json', json.dumps(record, sort_keys=True)+'\n')
        self.allocations[task] = record
        return record.copy()

    def restore_database(self, task: str) -> str:
        self.verify()
        source = self.allocations[task]
        database = source['database'] + '_restore_' + secrets.token_hex(4)
        self.sql(f'CREATE DATABASE {database} OWNER {source["owner"]} TEMPLATE template0;')
        self.databases.add(database)
        self.sql(f'REVOKE ALL ON DATABASE {database} FROM PUBLIC;')
        self.restore_targets.add(database)
        return database

    def dump(self, database: str, output: Path):
        from backup import in_backups, publish
        self.verify()
        if database not in self.databases:
            raise FixtureError('dump source was not allocated by this fixture')
        in_backups(output)
        with publish(output) as stream:
            try:
                result = subprocess.run([*DOCKER, 'exec', self.container, 'pg_dump', '-U', 'a09_admin',
                    '-d', database, '--format=custom'], stdout=stream, stderr=subprocess.PIPE,
                    env=self.env, timeout=120)
            except (OSError, subprocess.TimeoutExpired):
                raise FixtureError('dump outcome is UNKNOWN') from None
            if result.returncode:
                raise FixtureError('complete isolated dump failed')

    def restore(self, database: str, source: Path, expected_sha256: str):
        from backup import binding_check, digest_stream, in_backups, private_input
        self.verify()
        if database not in self.restore_targets:
            raise FixtureError('restore requires a new independent target')
        occupied = self.sql("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace "
                            "WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND left(n.nspname,3)<>'pg_';", database)
        if occupied != '0':
            raise FixtureError('restore refuses an occupied target')
        in_backups(source)
        binding_check(expected_sha256)
        with private_input(source) as stream:
            if stream.read(5) != b'PGDMP':
                raise FixtureError('restore input is not a PostgreSQL custom archive')
            stream.seek(0)
            if digest_stream(stream)[0] != expected_sha256:
                raise FixtureError('authenticated dump digest changed before restore')
            stream.seek(0)
            # Seal before any mutation, and never retry a lost acknowledgement.
            self.record('restore-intent-'+database, {'database': database,
                'system_id': self.identity['system_id'], 'plaintext_sha256': expected_sha256})
            try:
                result = subprocess.run([*DOCKER, 'exec', '-i', self.container, 'pg_restore', '-U', 'a09_admin',
                    '-d', database, '--single-transaction', '--exit-on-error'], stdin=stream,
                    stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, env=self.env, timeout=120)
            except (OSError, subprocess.TimeoutExpired):
                raise FixtureError('restore result is UNKNOWN; do not replay') from None
        if result.returncode:
            raise FixtureError('restore failed; retained target requires inspection')
        self.record('restore-finished-'+database, {'database': database})

    def cleanup(self):
        self.verify()
        system_id = self.identity['system_id']
        if not re.fullmatch(r'[0-9]+', system_id):
            raise FixtureError('invalid physical cluster identity')
        # Block new clients, then check again after COMMIT on this same session.
        # A live client leaves the fixture fenced and retained, not forcibly killed.
        self.sql("BEGIN; SET LOCAL lock_timeout='3s'; DO $$ DECLARE d RECORD; BEGIN "
            f"IF (SELECT system_identifier::text FROM pg_control_system()) <> '{system_id}' THEN RAISE EXCEPTION 'identity mismatch'; END IF; "
            "IF EXISTS(SELECT 1 FROM pg_stat_activity WHERE backend_type='client backend' AND pid<>pg_backend_pid()) THEN RAISE EXCEPTION 'live client'; END IF; "
            "FOR d IN SELECT datname FROM pg_database WHERE datallowconn LOOP "
            "EXECUTE format('ALTER DATABASE %I ALLOW_CONNECTIONS false',d.datname); END LOOP; END $$; COMMIT; "
            "DO $$ BEGIN PERFORM pg_stat_clear_snapshot(); IF EXISTS(SELECT 1 FROM pg_stat_activity "
            "WHERE backend_type='client backend' AND pid<>pg_backend_pid()) THEN RAISE EXCEPTION 'live client after fence'; END IF; END $$;")
        if self.inspect() != self.binding:
            raise FixtureError('container generation changed before cleanup')
        self.docker('rm', '--force', self.container)
        remaining = self.docker('container', 'ls', '--all', '--quiet', '--no-trunc',
                                 '--filter', 'id='+self.container).stdout.strip()
        if remaining:
            raise FixtureError('owned fixture removal is unconfirmed')
        self.record('removed', {'container': self.container, 'no_client_backends': True})
