#!/usr/bin/env python3
"""Exercise preservation fingerprints only in a disposable local PostgreSQL cluster."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import re
import shlex
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
    cache = Path.home() / '.cache'
    cache.mkdir(mode=0o700, parents=True, exist_ok=True)
    root = Path(tempfile.mkdtemp(prefix='credit-original-fingerprint-', dir=cache))
    root.chmod(0o700)
    data, socket = root / 'data', root / 'socket'
    socket.mkdir(mode=0o700)
    env = {key: value for key, value in os.environ.items() if not key.startswith('PG')}
    port = str(49152 + os.getpid() % 16000)
    base = ['psql', '-X', '-qAt', '-v', 'ON_ERROR_STOP=1', '-h', str(socket), '-p', port, '-d', 'postgres']
    log = root / 'tests.log'
    log.touch(mode=0o600)
    count, started = 0, False

    def run(command, sql=None, *, log_output=True):
        result = subprocess.run(command, input=sql, text=True, capture_output=True, env=env)
        with log.open('a') as stream:
            stream.write((result.stdout if log_output else '') + result.stderr + '\nexit=' + str(result.returncode) + '\n')
        return result

    def query(sql, *, log_output=True):
        result = run(base, sql, log_output=log_output)
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

    def reference_fingerprint(stage, changes=restorations, declared=()):
        # Reassemble the original UNION wrapper from the individual SELECTs;
        # no ancestor Git object or separately installed source is required.
        generated = helper.fingerprint_sql(inventory, changes, stage, declared)
        marker = 'SELECT statement FROM (VALUES\n'
        prefix, emitter = generated.rsplit(marker, 1)
        if not emitter.endswith('\\gexec\n'):
            raise AssertionError('per-table fingerprint emitter contract changed')
        statements = query(transaction + prefix + marker + emitter.removesuffix('\\gexec\n') + ';\nCOMMIT;\n', log_output=False).splitlines()
        if len(statements) != len(inventory['tables']) or not all(statement.startswith('SELECT ') for statement in statements):
            raise AssertionError('fingerprint emitter must expose only one SELECT per table')
        return (transaction + prefix + 'SELECT table_name,row_count,sha256 FROM (\n'
                + '\nUNION ALL\n'.join(statements) + '\n) fingerprints ORDER BY table_name COLLATE "C";\nCOMMIT;\n')

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
        boot = run(['pg_ctl', '-D', str(data), '-l', str(pglog), '-o', f"-h '' -p {port} -k {socket} -c shared_buffers=32MB -c max_connections=10", '-w', 'start'])
        started = boot.returncode == 0
        if not started:
            raise RuntimeError('pg_ctl start failed; private log: ' + str(log))
        query('CREATE ROLE fixture_reader NOSUPERUSER NOBYPASSRLS;')
        query(setup)
        inventory = json.loads(query(helper.inventory_sql('fixture')))
        original = query(fingerprint('before'))
        check('bounded per-table before output equals fixed original UNION algorithm', original == query(reference_fingerprint('before')))
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
            if name == 'precise quota and nullable restoration':
                check('bounded typed after output equals fixed original UNION algorithm', query(fingerprint('after')) == query(reference_fingerprint('after')))
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

        settings_query = "SELECT json_build_object('jit',current_setting('jit'),'parallel',current_setting('max_parallel_workers_per_gather'),'work_mem',current_setting('work_mem'),'hash',current_setting('hash_mem_multiplier'));\n"
        settings_guard = "DO $resources$ BEGIN IF current_setting('jit')<>'off' OR current_setting('max_parallel_workers_per_gather')<>'0' OR current_setting('work_mem')<>'4MB' OR current_setting('hash_mem_multiplier')<>'1' OR current_setting('transaction_read_only')<>'on' OR current_setting('transaction_isolation')<>'repeatable read' THEN RAISE EXCEPTION 'fingerprint session resource contract'; END IF; END $resources$;\n"
        settings = query(settings_query + transaction + helper.SETTINGS + settings_guard + 'COMMIT;\n' + settings_query).splitlines()
        check('four actual LOCAL resource settings apply only inside read-only RR transaction', len(settings) == 2 and json.loads(settings[0]) == json.loads(settings[1]))

        query(setup + '''
CREATE SCHEMA "Quoted schema";
CREATE TABLE "Quoted schema"."Z odd"("id'quoted" bigint,"value\"\"quoted" text);
CREATE TABLE "Quoted schema"."a'b"(id bigint,note text);
CREATE TABLE "Quoted schema"."éclair"(id bigint,note text);
INSERT INTO "Quoted schema"."Z odd" VALUES(1,'single''quote and double"quote');
INSERT INTO "Quoted schema"."a'b" VALUES(1,NULL);
INSERT INTO "Quoted schema"."éclair" VALUES(1,'UTF8');
''')
        inventory = json.loads(query(helper.inventory_sql('fixture')))
        quoted = query(fingerprint('before')); lines = quoted.splitlines(); labels = [line.split('|')[0] for line in lines]
        check('quoted and UTF8 identifiers produce only C-sorted table/count/hash rows',
              len(lines) == len(inventory['tables']) and labels == sorted(labels, key=lambda name: name.encode('utf-8'))
              and all(re.fullmatch(r'.+\|(0|[1-9][0-9]*)\|[0-9a-f]{64}', line) for line in lines)
              and quoted == query(reference_fingerprint('before')))

        query(setup + '''
DROP SCHEMA "Quoted schema" CASCADE;
CREATE TABLE fixture.snapshot_a(id bigint PRIMARY KEY,note text);
CREATE TABLE fixture.snapshot_b(id bigint PRIMARY KEY,note text);
INSERT INTO fixture.snapshot_a VALUES(1,'old'); INSERT INTO fixture.snapshot_b VALUES(1,'old');
''')
        inventory = json.loads(query(helper.inventory_sql('fixture')))
        snapshot_before = query(fingerprint('before'))
        first = copy.deepcopy(inventory); remaining = copy.deepcopy(inventory)
        first['tables'] = [t for t in inventory['tables'] if t['schema']=='fixture' and t['name']=='snapshot_a']
        remaining['tables'] = [t for t in inventory['tables'] if t not in first['tables']]
        writer = shlex.join(base + ['-c', "UPDATE fixture.snapshot_b SET note='new' WHERE id=1;"])
        interleaved = (transaction + helper.SETTINGS + helper.inventory_guard(inventory, 'before')
                       + helper.fingerprint_sql(first, (), 'before', include_guard=False)
                       + '\\! ' + writer + '\n'
                       + helper.fingerprint_sql(remaining, (), 'before', include_guard=False) + 'COMMIT;\n')
        observed = query(interleaved).splitlines()
        expected = snapshot_before.splitlines()
        check('independent table SELECTs share one RR snapshot across a committed writer',
              sorted(observed) == sorted(expected) and query('SELECT note FROM fixture.snapshot_b;') == 'new'
              and query(fingerprint('after')) != snapshot_before)
        print('PASS: ' + str(count) + ' isolated PostgreSQL fingerprint checks; private log: ' + str(log), flush=True)
    finally:
        if started or (data / 'postmaster.pid').exists():
            stopped = run(['pg_ctl', '-D', str(data), '-m', 'fast', '-w', 'stop'])
            if stopped.returncode:
                raise RuntimeError('pg_ctl stop failed; private log: ' + str(log))
        shutil.rmtree(data, ignore_errors=True)
        shutil.rmtree(socket, ignore_errors=True)


if __name__ == '__main__':
    main()
