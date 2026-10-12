#!/usr/bin/env python3
"""Explicit disposable PostgreSQL restore probe. No caller DSN or existing DB.

This installs a small synthetic contract, NOT the complete Core schema. It
qualifies fixture ownership, dump authentication, independent empty-target
restore, sequences, SQL functions, triggers and runtime privileges. It cannot
qualify Core maintenance, product restart, pgroll, or production deployment.
"""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import tempfile

import backup
from pg_fixture import Fixture, FixtureError, PermissionDenied, require_tools


def seed(owner: str, runtime: str) -> str:
    return f'''
SET ROLE {owner};
CREATE SCHEMA a09_probe AUTHORIZATION {owner};
CREATE TABLE a09_probe.entries (
 id BIGSERIAL PRIMARY KEY,
 request_key TEXT NOT NULL UNIQUE,
 credits BIGINT NOT NULL CHECK (credits>0)
);
CREATE TABLE a09_probe.contract (name TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO a09_probe.contract VALUES ('unit','synthetic_integer_credits'),('schema','a09-probe-v1');
INSERT INTO a09_probe.entries(request_key,credits) VALUES ('request-1',101),('request-2',202),('request-3',303);
CREATE FUNCTION a09_probe.immutable_entry() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN RAISE EXCEPTION 'append only'; END $$;
CREATE TRIGGER append_only BEFORE UPDATE OR DELETE ON a09_probe.entries
FOR EACH ROW EXECUTE FUNCTION a09_probe.immutable_entry();
CREATE FUNCTION a09_probe.record_entry(k TEXT,v BIGINT) RETURNS VOID
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF v<1 OR v>1000 THEN RAISE EXCEPTION 'invalid synthetic credits'; END IF;
 INSERT INTO a09_probe.entries(request_key,credits) VALUES (k,v) ON CONFLICT(request_key) DO NOTHING;
END $$;
REVOKE ALL ON SCHEMA a09_probe FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA a09_probe FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA a09_probe FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA a09_probe FROM PUBLIC;
GRANT USAGE ON SCHEMA a09_probe TO {runtime};
GRANT SELECT ON ALL TABLES IN SCHEMA a09_probe TO {runtime};
GRANT EXECUTE ON FUNCTION a09_probe.record_entry(TEXT,BIGINT) TO {runtime};
RESET ROLE;
'''


SNAPSHOT = '''
BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL search_path=pg_catalog;
SELECT json_build_object(
 'rows',(SELECT json_agg(t ORDER BY id) FROM a09_probe.entries t),
 'contract',(SELECT json_agg(t ORDER BY name) FROM a09_probe.contract t),
 'sequence',(SELECT json_build_array(last_value,is_called) FROM a09_probe.entries_id_seq),
 'columns',(SELECT json_agg(json_build_array(c.relname,a.attnum,a.attname,
   format_type(a.atttypid,a.atttypmod),a.attnotnull,pg_get_expr(d.adbin,d.adrelid)) ORDER BY c.relname,a.attnum)
   FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid
   JOIN pg_namespace n ON n.oid=c.relnamespace
   LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
   WHERE n.nspname='a09_probe' AND a.attnum>0 AND NOT a.attisdropped),
 'relations',(SELECT json_agg(json_build_array(c.relname,c.relkind,
   pg_get_userbyid(c.relowner),c.relacl::text) ORDER BY c.relname)
   FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='a09_probe'),
 'functions',(SELECT json_agg(json_build_array(pg_get_functiondef(p.oid),
   pg_get_userbyid(p.proowner),p.proacl::text) ORDER BY p.proname)
   FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
   WHERE n.nspname='a09_probe' AND p.prokind='f'),
 'indexes',(SELECT json_agg(pg_get_indexdef(i.indexrelid) ORDER BY c.relname)
   FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid
   JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='a09_probe'),
 'triggers',(SELECT json_agg(json_build_array(pg_get_triggerdef(t.oid,true),t.tgenabled) ORDER BY t.tgname)
   FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid
   JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='a09_probe' AND NOT t.tgisinternal),
 'schema_acl',(SELECT json_build_array(pg_get_userbyid(nspowner),nspacl::text)
   FROM pg_namespace WHERE nspname='a09_probe')
);
COMMIT;
'''


def snapshot(fixture: Fixture, database: str) -> dict:
    return json.loads(fixture.sql(SNAPSHOT, database))


def expect_denied(fixture, statement, database, role, password):
    try:
        fixture.sql(statement, database, role=role, password=password)
    except PermissionDenied:
        return
    raise AssertionError('the tested runtime operation was not denied with SQLSTATE 42501')


def run(parent: Path) -> dict:
    require_tools()  # A missing daemon returns BLOCKED, not a skipped/pass test.
    fixture = Fixture(parent)
    fixture.start()
    completed = False
    try:
        resources = {f'A{i:02d}': fixture.allocate(f'A{i:02d}') for i in range(1, 11)}
        selected = resources['A09']
        source = selected['database']
        role, owner = selected['runtime'], selected['owner']
        with backup.private_input(fixture.root/'A09'/'runtime.pgpass') as stream:
            password = stream.read(4096).decode().strip().split(':')[-1]
        fixture.sql(seed(owner, role), source)
        expect_denied(fixture, f'SET ROLE {owner};', source, role, password)
        expect_denied(fixture, 'SELECT 1;', resources['A01']['database'], role, password)
        expect_denied(fixture, "INSERT INTO a09_probe.entries(request_key,credits) VALUES ('unapproved',1);", source, role, password)
        before = snapshot(fixture, source)
        target = fixture.restore_database('A09')
        # Fence the two test login writers. Existing sessions must also be absent.
        fixture.sql(f'ALTER ROLE {role} NOLOGIN; ALTER ROLE {selected["installer"]} NOLOGIN;')
        if fixture.sql(f"SELECT count(*) FROM pg_stat_activity WHERE datname='{source}';") != '0':
            raise FixtureError('source still has a test writer')
        backup.BACKUPS.mkdir(mode=0o700, parents=True, exist_ok=True)
        backup.private_dir(backup.BACKUPS)
        directory = Path(tempfile.mkdtemp(prefix='a09-restore-', dir=backup.BACKUPS))
        key = fixture.root/'recovery.key'
        backup.create_key(key)
        plain, encrypted, recovered = (directory/name for name in ('source.dump','source.aesgcm','recovered.dump'))
        binding = hashlib.sha256(json.dumps({'nonce':fixture.nonce, 'system_id':fixture.identity['system_id'],
                                            'source':source,'target':target},sort_keys=True).encode()).hexdigest()
        fixture.dump(source, plain)
        sealed = backup.encrypt(plain, encrypted, key, binding)
        # Reload the private key, authenticate all data, and only then restore.
        authenticated = backup.decrypt(encrypted, recovered, key, binding)
        fixture.restore(target, recovered, authenticated['plaintext_sha256'])
        after = snapshot(fixture, target)
        if before != after or before != snapshot(fixture, source):
            raise AssertionError('rows, sequence, contract, definitions or permissions changed')
        fixture.sql(f'ALTER ROLE {role} LOGIN; GRANT CONNECT ON DATABASE {target} TO {role};')
        expect_denied(fixture, f'SET ROLE {owner};', target, role, password)
        expect_denied(fixture, "INSERT INTO a09_probe.entries(request_key,credits) VALUES ('unapproved',1);", target, role, password)
        fixture.sql("SELECT a09_probe.record_entry('restored-request',404); SELECT a09_probe.record_entry('restored-request',404);",
                    target, role=role, password=password)
        actual = fixture.sql('SELECT count(*),sum(credits) FROM a09_probe.entries;', target)
        if actual != '4|1010' or snapshot(fixture, source) != before:
            raise AssertionError('post-restore permission/idempotency or original-source invariant failed')
        try:
            fixture.restore(target, recovered, authenticated['plaintext_sha256'])
        except FixtureError:
            pass
        else:
            raise AssertionError('occupied target accepted a second restore')
        report = {'format':'a09-postgres-recovery-probe-v1','status':'PASS',
                  'scope':'synthetic PostgreSQL fixture only; NOT full Core maintenance',
                  'postgres':fixture.identity['version'],'databases_allocated':10,
                  'independent_restore_databases':1,'backup':sealed,
                  'snapshot_sha256':hashlib.sha256(json.dumps(before,sort_keys=True).encode()).hexdigest(),
                  'verified':['source_unchanged','rows','sequence','contract','columns','owners','ACLs',
                              'SECURITY_DEFINER','append_only_trigger','runtime_denials','idempotency'],
                  'not_verified':['Core schema','global role recreation','DB dead-holder CAS','pgroll','product restart']}
        fixture.record('restore-qualification',report)
        completed = True
        return report
    finally:
        # Cleanup itself checks the exact identity and zero clients. It never
        # sweeps old fixtures. Backup and key files are retained for inspection.
        fixture.cleanup()
        if not completed:
            fixture.record('test-failed',{'status':'FAIL','backup_retained':True})


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--private-root',type=Path,required=True)
    args = parser.parse_args()
    try:
        print(json.dumps(run(args.private_root),sort_keys=True))
        return 0
    except FixtureError as error:
        print(str(error))
        return 2 if str(error).startswith('BLOCKED:') else 1


if __name__ == '__main__':
    raise SystemExit(main())
