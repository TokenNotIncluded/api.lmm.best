#!/usr/bin/env python3
"""Opt-in real full-restore test: isolated PG, actual candidate and N-1 CLIs.

No SSH, production DSN, host service, or provider HTTP process is used. Supply
already verified executable artifacts; all files are newly created private
evidence. The mandatory offline gate lives in test-deploy-shared-postgres.py.
"""
import argparse
import importlib.util
import os
from pathlib import Path
import shutil
import sys
import tempfile

spec = importlib.util.spec_from_file_location('cluster', Path(__file__).with_name('deploy-shared-postgres.py'))
c = importlib.util.module_from_spec(spec)
spec.loader.exec_module(c)


def seed(request):
    r = c.decode(Path(request).read_bytes())
    work, root = Path(r['work']), Path(r['root'])
    c.require(str(Path('/proc/self/ns/net').readlink()) != r['parent_network_namespace'], 'fixture-not-isolated')
    links = c.decode(c.command(['ip', '-j', 'link', 'show']))
    c.require(len(links) == 1 and links[0]['ifname'] == 'lo' and 'UP' in links[0]['flags'], 'fixture-external-network')
    pgdata, sockets = root / 'data', root / 'sockets'
    sockets.mkdir(mode=0o700)
    env = {'PATH': '/usr/bin:/bin', 'HOME': str(root), 'LC_ALL': 'C',
           'PGHOST': '127.0.0.1', 'PGPORT': '5432', 'PGDATABASE': 'lmm_test_fixture',
           'PGUSER': 'lmm_test_cluster', 'PGOPTIONS': '-csearch_path=public'}
    c.command(['initdb', '-D', str(pgdata), '-U', env['PGUSER'], '-A', 'trust', '--encoding=UTF8', '--no-instructions'],
              env=env, log=work / 'fixture-initdb.log')
    started = False
    try:
        c.command(['pg_ctl', '-D', str(pgdata), '-l', str(root / 'postgres.log'), '-o',
                   '-c listen_addresses=127.0.0.1 -c port=5432 -c unix_socket_directories=' + str(sockets), '-w', 'start'],
                  env=env, log=work / 'fixture-pg-start.log')
        started = True
        c.command(['createdb', '--no-password', env['PGDATABASE']], env=env)
        app = {'PATH': env['PATH'], 'HOME': str(root), 'LC_ALL': 'C',
               'SQL_DSN': 'postgresql://lmm_test_cluster@127.0.0.1:5432/lmm_test_fixture?sslmode=disable&search_path=public',
               'NODE_TYPE': 'master', 'REDIS_CONN_STRING': '', 'LOG_SQL_DSN': '',
               'SESSION_SECRET': 'isolated-fixture-synthetic-session-secret',
               'CRYPTO_SECRET': 'isolated-fixture-synthetic-crypto-secret'}
        old = r['plan']['nodes'][0]['rollback']
        c.command([old, 'migrate', '--apply'], env=app, log=work / 'old-initial-apply.log', cwd=root)
        c.psql(env, "INSERT INTO users (username,password,role,status,aff_code,quota,used_quota) VALUES ('fixture-root','unusable-synthetic',100,1,'fixture-root',1234567,76543)")
        c.command([old, 'migrate', '--apply'], env=app, log=work / 'old-setup-apply.log', cwd=root)
        c.command([old, 'migrate', '--verify'], env=app, log=work / 'old-schema-verify.log', cwd=root)
        c.psql(env, "INSERT INTO tokens (user_id,key,name,status,remain_quota,used_quota) VALUES (1,'unusable-synthetic-token','fixture-token',1,654321,12345)")
        c.psql(env, "CREATE SCHEMA extra_fixture; CREATE TABLE extra_fixture.preserved (id int PRIMARY KEY, value text); INSERT INTO extra_fixture.preserved VALUES (1,'synthetic-full-database-backup')")
        c.psql(env, "CREATE TABLE extra_fixture.rls_preserved (id int); INSERT INTO extra_fixture.rls_preserved VALUES (1); ALTER TABLE extra_fixture.rls_preserved ENABLE ROW LEVEL SECURITY; CREATE POLICY hide_fixture ON extra_fixture.rls_preserved USING (false)")
        c.psql(env, "INSERT INTO options (key,value) VALUES ('ModelPrice','{\"fixture-fixed\":0.042}'),('billing_setting.billing_mode','{\"fixture-minute\":\"tiered_expr\"}'),('billing_setting.billing_expr','{\"fixture-minute\":\"tier(\\\"base\\\",audio_s*0.017*1000000/60)\"}') ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value")
        keys = ','.join("'" + k + "'" for k in c.PRICING_KEYS[:3])
        units = c.decode(c.psql(env, 'SELECT json_object_agg(key,value) FROM options WHERE key IN (' + keys + ')'))
        c.require(set(units) == set(c.PRICING_KEYS[:3]), 'fixture-units-missing')
        identity = c.database_identity(env)
        before = {'fingerprints': c.fingerprints(env, units), 'tables': c.database_table_counts(env)}
        # Exercise the actual peer SQL and stdout-FD implementation, without
        # claiming an OS-user switch in this rootless namespace. Only env-i is
        # used here; production runuser is covered separately by preflight.
        def fixture_peer(role, directory):
            c.new_private(directory)
            peer = c.PeerDatabase({'transport':'local_peer','os_user':'task','database_role':role,
                                   'socket_directory':str(sockets),'port':5432},identity,directory)
            peer.prefix = peer.prefix[4:]
            return peer
        c.psql(env, "CREATE ROLE fixture_reader LOGIN; GRANT CONNECT ON DATABASE lmm_test_fixture TO fixture_reader; GRANT USAGE ON SCHEMA public,extra_fixture TO fixture_reader; GRANT EXECUTE ON FUNCTION pg_control_system() TO fixture_reader")
        restricted = fixture_peer('fixture_reader', work/'peer-select-negative')
        denied = False
        try:
            restricted.preflight(restricted.binding())
        except c.GateFailed as error:
            c.require(str(error) == 'backup-read-capability-missing', 'fixture-select-negative-wrong-gate')
            denied = True
        c.require(denied, 'fixture-full-read-permission-must-fail')
        c.psql(env, "GRANT SELECT ON ALL TABLES IN SCHEMA public,extra_fixture TO fixture_reader; GRANT SELECT ON ALL SEQUENCES IN SCHEMA public,extra_fixture TO fixture_reader")
        filtered = fixture_peer('fixture_reader', work/'peer-rls-negative')
        rls_denied = False
        try:
            filtered.preflight(filtered.binding())
        except c.GateFailed as error:
            c.require(str(error) == 'backup-read-capability-missing', 'fixture-rls-negative-wrong-gate')
            rls_denied = True
        c.require(rls_denied, 'fixture-rls-filtering-must-fail')
        c.psql(env, "ALTER ROLE fixture_reader BYPASSRLS")
        capable = fixture_peer('fixture_reader', work/'peer-capable-nonsuperuser')
        capable_binding = capable.binding()
        c.require(not capable_binding['role']['superuser'], 'fixture-role-must-not-be-superuser')
        capable_preflight = capable.preflight(capable_binding)
        c.require(capable_preflight['passed'] and capable.counts(capable_binding) == before['tables'] and
                  capable.fingerprint(capable_binding, units) == before['fingerprints'], 'fixture-peer-proof-incomplete')
        # A genuine old-schema negative gate precedes candidate application.
        missing = False
        try:
            c.command([r['plan']['candidate']['path'], 'migrate', '--verify'], env=app,
                      log=work / 'candidate-before-apply-verify.log', cwd=root)
        except c.GateFailed:
            missing = True
        c.require(missing, 'fixture-must-exercise-a-real-schema-upgrade')
        dump_sha = capable.backup(capable_binding)
        shutil.copyfile(capable.work / 'database.dump', work / 'database.dump')
        os.chmod(work / 'database.dump', 0o600)
        value = {'baseline': before, 'identity': identity, 'units': units,
                 'dump_sha256': dump_sha, 'candidate_old_schema_rejected': missing,
                 'actual_peer_read_sql': True, 'missing_select_rejected': denied, 'rls_filtering_rejected': rls_denied,
                 'capable_nonsuperuser_preflight': True,
                 'scope': 'synthetic namespace PG; no production database or serving workers'}
        c.private_write(work / 'fixture.json', c.encode(value))
    finally:
        if started:
            c.command(['pg_ctl', '-D', str(pgdata), '-m', 'fast', '-w', 'stop'], env=env,
                      log=work / 'fixture-pg-stop.log')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--candidate', type=Path)
    parser.add_argument('--rollback', type=Path, action='append')
    parser.add_argument('--work', type=Path)
    parser.add_argument('--seed-request', type=Path)
    args = parser.parse_args()
    os.umask(0o077)
    if args.seed_request:
        seed(args.seed_request)
        return
    c.require(os.geteuid() != 0 and args.candidate and len(args.rollback or []) >= 2 and args.work,
              'nonroot-explicit-artifacts-and-new-work-required')
    binaries = [args.candidate, *args.rollback]
    for p in binaries:
        c.regular(p)
    work = c.new_private(args.work)
    seed_work = c.new_private(work / 'seed')
    clone_work = c.new_private(work / 'restore')
    # Short, empty temporary roots avoid Unix-socket path truncation. There are
    # no mounts of HOME, host /proc, credentials, or external network interfaces.
    seed_root = Path(tempfile.mkdtemp(prefix='lmm-pg-fixture-'))
    clone_root = Path(tempfile.mkdtemp(prefix='lmm-pg-restore-'))
    clone_root.rmdir()  # run_rehearsal requires exclusive creation.
    script = str(Path(__file__).resolve())
    helper = str(Path(__file__).with_name('deploy-systemd.py').resolve())
    nodes = [{'name': 'previous-' + str(i), 'ssh': 'fixture-' + str(i), 'hostname': 'fixture-' + str(i),
              'script': c.__file__, 'helper': helper, 'plan': '/fixture/plan.json',
              'candidate': str(args.candidate), 'provider_sha256': c.sha(p), 'version': '1.0.' + str(i),
              'invocation': 'b'*32, 'rollback': str(p),
              'probes': [{'url': 'http://127.0.0.1/closed', 'body_sha256': 'c'*64}]} for i,p in enumerate(args.rollback)]
    plan = {'format': 1, 'id': 'synthetic-test-001', 'confirmation': 'fixture-only',
            'script_sha256': c.sha(c.__file__), 'helper_sha256': c.sha(helper), 'source_sha': 'b'*40,
            'candidate': {'path': str(args.candidate), 'sha256': c.sha(args.candidate)}, 'nodes': nodes,
            'database_owner': nodes[0]['name'], 'public_probes': nodes[0]['probes'],
            'database_backup': {'transport': 'local_peer', 'os_user': 'postgres', 'database_role': 'postgres',
                                'socket_directory': '/run/postgresql', 'port': 5432},
            'expected_units': {k:'1' for k in c.PRICING_KEYS[:3]}, 'rehearsal_root': str(clone_root)}
    c.validate_plan(plan)
    request = seed_work / 'request.json'
    c.private_write(request, c.encode({'plan': plan, 'work': str(seed_work), 'root': str(seed_root),
                                      'parent_network_namespace': str(Path('/proc/self/ns/net').readlink())}))
    command = c.isolated_args(seed_work, seed_root, {script, c.__file__, *map(str,binaries)})
    command += ['--chdir', str(seed_root), '--', 'python3', '-B', script, '--seed-request', str(request)]
    c.command(command, log=work / 'seed-process.log', timeout=3600)
    fixture = c.decode((seed_work / 'fixture.json').read_bytes())
    plan['expected_units'] = fixture['units']
    shutil.copyfile(seed_work / 'database.dump', clone_work / 'database.dump')
    os.chmod(clone_work / 'database.dump', 0o600)
    result = c.run_rehearsal(plan, clone_work, fixture['baseline'], fixture['identity'], fixture['dump_sha256'])
    receipt = {'passed': True, 'real_isolated_full_restore': result,
               'candidate_old_schema_rejected': fixture['candidate_old_schema_rejected'],
               'candidate_sha256': plan['candidate']['sha256'], 'n_minus_one_sha256': [n['provider_sha256'] for n in nodes],
               'dump_sha256': fixture['dump_sha256'], 'tables_restored': len(fixture['baseline']['tables']),
               'actual_peer_read_sql': fixture['actual_peer_read_sql'],
               'missing_select_rejected': fixture['missing_select_rejected'],
               'rls_filtering_rejected': fixture['rls_filtering_rejected'],
               'capable_nonsuperuser_preflight': fixture['capable_nonsuperuser_preflight'],
               'scope': 'actual migration CLIs with synthetic isolated PG; no SSH/systemd or production acceptance'}
    c.private_write(work / 'summary.json', c.encode(receipt))
    print(c.encode(receipt).decode())


if __name__ == '__main__':
    try:
        main()
    except Exception:
        print('isolated PostgreSQL test failed; inspect private test evidence', file=sys.stderr)
        sys.exit(1)
