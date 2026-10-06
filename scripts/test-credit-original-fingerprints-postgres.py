#!/usr/bin/env python3
"""Exercise preservation fingerprints only in a disposable local PostgreSQL cluster."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def main():
    required = ('initdb', 'pg_ctl', 'psql')
    if any(shutil.which(tool) is None for tool in required):
        print('SKIP: local PostgreSQL tools unavailable')
        return
    spec = importlib.util.spec_from_file_location('fingerprints', Path(__file__).with_name('fingerprint-credit-rebase-original-tables.py'))
    helper = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(helper)
    root = Path(tempfile.mkdtemp(prefix='credit-original-fingerprint-', dir=Path.home() / '.cache'))
    root.chmod(0o700)
    data, socket = root / 'data', root / 'socket'
    socket.mkdir(mode=0o700)
    env = {key: value for key, value in os.environ.items() if not key.startswith('PG')}
    port = str(49152 + os.getpid() % 16000)
    base = ['psql', '-X', '-qAt', '-v', 'ON_ERROR_STOP=1', '-h', str(socket), '-p', port, '-d', 'postgres']
    log = root / 'tests.log'
    log.touch(mode=0o600)
    count, started = 0, False

    def run(command, sql=None):
        result = subprocess.run(command, input=sql, text=True, capture_output=True, env=env)
        with log.open('a') as stream:
            stream.write(result.stdout + result.stderr + '\nexit=' + str(result.returncode) + '\n')
        return result

    def query(sql):
        result = run(base, sql)
        if result.returncode:
            raise RuntimeError('fixture command failed; private log: ' + str(log))
        return result.stdout.strip()

    def check(name, passed):
        nonlocal count
        if not passed:
            raise AssertionError('FAIL: ' + name + '; private log: ' + str(log))
        count += 1
        print('PASS ' + str(count) + ': ' + name, flush=True)

    setup = """
DROP SCHEMA IF EXISTS fixture CASCADE; DROP SCHEMA IF EXISTS elsewhere CASCADE;
CREATE SCHEMA fixture; CREATE SCHEMA elsewhere;
CREATE TABLE fixture.wallet(id bigint PRIMARY KEY,quota bigint,nullable bigint,note text,j json,a int[]);
INSERT INTO fixture.wallet VALUES(1,10,NULL,'historical',' { "x": 1 } ','[0:1]={1,2}'),(2,20,4,'other','null','{1,2}');
CREATE TABLE fixture.duplicates(value text); INSERT INTO fixture.duplicates VALUES('same'),('same'),(NULL);
CREATE TABLE fixture.empty(value text);
CREATE TABLE fixture.nonunique(id bigint,quota bigint); INSERT INTO fixture.nonunique VALUES(3,10),(3,11);
CREATE TABLE fixture.audit(migration text,id bigint,tag text,amount bigint,note text,PRIMARY KEY(migration,id));
CREATE TABLE fixture.history(id bigint PRIMARY KEY,tag text); INSERT INTO fixture.history VALUES(1,'historical');
CREATE TABLE elsewhere.data(id bigint PRIMARY KEY,note text); INSERT INTO elsewhere.data VALUES(1,'elsewhere');
CREATE TABLE fixture.partitioned(id bigint,note text) PARTITION BY RANGE(id);
CREATE TABLE fixture.partition_leaf PARTITION OF fixture.partitioned FOR VALUES FROM(0) TO(10);
CREATE TABLE fixture.protected(id bigint,note text); INSERT INTO fixture.protected VALUES(1,'private');
ALTER TABLE fixture.protected ENABLE ROW LEVEL SECURITY;
GRANT USAGE ON SCHEMA fixture,elsewhere TO fixture_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA fixture,elsewhere TO fixture_reader;
"""
    restorations = [{'table': 'wallet', 'key': {'id': 1}, 'fields': {'quota': {'before': 10, 'after': 2}, 'nullable': {'before': None, 'after': 9}}}]
    audits = [{'table': 'audit', 'key': {'migration': 'v1', 'id': 50}, 'fields': {'migration': 'v1', 'id': 50, 'tag': 'new', 'amount': 2, 'note': None}}]
    inserted = "INSERT INTO fixture.audit VALUES('v1',50,'new',2,NULL);"
    transaction = 'BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;\n'

    def fingerprint(stage, changes=restorations, declared=(), role=''):
        guard = "DO $readonly$ BEGIN IF current_setting('transaction_read_only')<>'on' OR current_setting('transaction_isolation')<>'repeatable read' THEN RAISE EXCEPTION 'fixture requires read-only RR'; END IF; END $readonly$;\n"
        return transaction + role + guard + helper.fingerprint_sql(inventory, changes, stage, declared) + 'COMMIT;\n'

    def rejected(stage, changes=restorations, declared=(), role=''):
        try:
            sql = fingerprint(stage, changes, declared, role)
        except ValueError:
            return True
        return run(base, sql).returncode != 0

    try:
        init = run(['initdb', '-D', str(data), '-A', 'trust', '--no-locale', '-E', 'UTF8', '--no-instructions'])
        if init.returncode:
            raise RuntimeError('initdb failed; private log: ' + str(log))
        pglog = root / 'postgres.log'
        pglog.touch(mode=0o600)
        boot = run(['pg_ctl', '-D', str(data), '-l', str(pglog), '-o', f"-h '' -p {port} -k {socket}", '-w', 'start'])
        started = boot.returncode == 0
        if not started:
            raise RuntimeError('pg_ctl start failed; private log: ' + str(log))
        query('CREATE ROLE fixture_reader NOSUPERUSER NOBYPASSRLS;')
        query(setup)
        inventory = json.loads(query(helper.inventory_sql('fixture')))
        original = query(fingerprint('before'))
        rows = {line.split('|')[0]: line.split('|')[1] for line in original.splitlines()}
        check('no-PK duplicate rows and empty original table', rows['fixture.duplicates'] == '3' and rows['fixture.empty'] == '0')
        mutations = [
            ('precise quota and nullable restoration', 'UPDATE fixture.wallet SET quota=2,nullable=9 WHERE id=1;', True),
            ('new nullable columns and new audit table ignored', 'ALTER TABLE fixture.wallet ADD COLUMN fresh bigint; CREATE TABLE fixture.new_audit(id bigint); INSERT INTO fixture.new_audit VALUES(1);', True),
            ('unplanned history on planned row', "UPDATE fixture.wallet SET quota=2,nullable=9,note='changed' WHERE id=1;", False),
            ('unexpected planned field value', 'UPDATE fixture.wallet SET quota=3,nullable=9 WHERE id=1;', False),
            ('another schema changed', "UPDATE elsewhere.data SET note='changed';", False),
            ('original row deleted', 'DELETE FROM fixture.wallet WHERE id=2;', False),
            ('extra original row', "INSERT INTO fixture.wallet(id,quota,note) VALUES(3,30,'extra');", False),
            ('no-PK duplicate removed', "DELETE FROM fixture.duplicates WHERE ctid=(SELECT ctid FROM fixture.duplicates WHERE value='same' LIMIT 1);", False),
            ('no-PK duplicate added', "INSERT INTO fixture.duplicates VALUES('same');", False),
            ('empty original table gains row', "INSERT INTO fixture.empty VALUES('extra');", False),
            ('SQL NULL differs from JSON null', 'UPDATE fixture.wallet SET j=NULL WHERE id=2;', False),
            ('raw JSON whitespace preserved', "UPDATE fixture.wallet SET j='{\"x\":1}' WHERE id=1;", False),
            ('array lower bounds preserved', "UPDATE fixture.wallet SET a='{1,2}' WHERE id=1;", False),
        ]
        for name, mutation, equal in mutations:
            query(setup + mutation)
            check(name, (query(fingerprint('after')) == original) == equal)
        for name, mutation in [('original table missing', 'DROP TABLE elsewhere.data;'), ('original column missing', 'ALTER TABLE fixture.wallet DROP COLUMN note;'), ('original type changed', 'ALTER TABLE fixture.wallet ALTER COLUMN note TYPE varchar;'), ('partition detach refused', 'ALTER TABLE fixture.partitioned DETACH PARTITION fixture.partition_leaf;')]:
            query(setup + mutation)
            check(name, rejected('after'))
        query(setup)
        duplicate = [{'table': 'nonunique', 'key': {'id': 3}, 'fields': {'quota': {'before': 10, 'after': 2}}}]
        check('duplicate restoration row key refused', rejected('before', duplicate))
        check('nonowner RLS policy hides original rows', query('SET ROLE fixture_reader; SET row_security=on; SELECT count(*) FROM fixture.protected;') == '0')
        rls = run(base, fingerprint('before', role='SET LOCAL ROLE fixture_reader;\n'))
        check('nonowner RLS with row_security off refused', rls.returncode != 0 and 'row-level security' in rls.stderr)
        audit_before = query(fingerprint('before', declared=audits))
        check('declared audit after row missing refused', rejected('after', declared=audits))
        query(inserted)
        check('declared audit before PK collision refused', rejected('before', declared=audits))
        check('declared new row in original empty audit excluded precisely', query(fingerprint('after', declared=audits)) == audit_before)
        query("INSERT INTO fixture.audit VALUES('v1',51,'new',2,NULL);")
        check('extra row in same audit table detected', query(fingerprint('after', declared=audits)) != audit_before)
        query(setup + inserted + "UPDATE fixture.history SET tag='changed';")
        check('historical original table change detected', query(fingerprint('after', declared=audits)) != audit_before)
        query(setup + "INSERT INTO fixture.audit VALUES('old',1,'historical',100,NULL);")
        historical_before = query(fingerprint('before', declared=audits))
        query(inserted)
        check('declared new row preserves same-table historical audit', query(fingerprint('after', declared=audits)) == historical_before)
        query("UPDATE fixture.audit SET tag='changed' WHERE migration='old' AND id=1;")
        check('same-table historical audit change detected', query(fingerprint('after', declared=audits)) != historical_before)
        query(setup + inserted + "UPDATE fixture.audit SET tag='changed';")
        wrong = run(base, fingerprint('after', declared=audits))
        check('declared audit original field mismatch detected', wrong.returncode != 0 or wrong.stdout.strip() != audit_before)
        for name, changed in [('incomplete composite audit PK refused', {'key': {'id': 50}}), ('non-PK audit key refused', {'key': {'migration': 'v1', 'id': 50, 'tag': 'new'}}), ('incomplete audit fields refused', {'fields': {'tag': 'new'}})]:
            bad = copy.deepcopy(audits)
            bad[0].update(changed)
            query(setup)
            check(name, rejected('before', declared=bad))

        class Verifier:
            planned_restorations = staticmethod(lambda plan: restorations)
            declared_audit_rows = staticmethod(lambda plan: ())

            @staticmethod
            def postgres_verification_sql(plan, stage):
                expected = 10 if stage == 'before' else 2
                return '\\set ON_ERROR_STOP on\n' + transaction + f"DO $stage$ BEGIN IF current_setting('transaction_read_only')<>'on' OR current_setting('transaction_isolation')<>'repeatable read' OR (SELECT quota FROM fixture.wallet WHERE id=1)<>{expected} THEN RAISE EXCEPTION 'independent stage guard'; END IF; END $stage$;\nCOMMIT;\n"

        for stage in ('before', 'after'):
            query(setup + ('' if stage == 'before' else 'UPDATE fixture.wallet SET quota=2,nullable=9 WHERE id=1;'))
            sql, restored, declared = helper.stage_sql(inventory, {'target': {'schema': 'fixture'}}, stage, Verifier)
            check('independent DO and fingerprint share read-only RR stage ' + stage, query(sql) == original and restored == restorations and declared == ())
        print('PASS: ' + str(count) + ' isolated PostgreSQL fingerprint checks', flush=True)
    finally:
        if started or (data / 'postmaster.pid').exists():
            stopped = run(['pg_ctl', '-D', str(data), '-m', 'immediate', '-w', 'stop'])
            if stopped.returncode:
                raise RuntimeError('pg_ctl stop failed; private log: ' + str(log))
        shutil.rmtree(data, ignore_errors=True)
        shutil.rmtree(socket, ignore_errors=True)


if __name__ == '__main__':
    main()
