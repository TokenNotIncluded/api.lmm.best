#!/usr/bin/env python3
"""Offline CLI receipts and sealed-controller compatibility; no database IO."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

sys.dont_write_bytecode = True


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


def main():
    helper_path = Path(__file__).with_name('fingerprint-credit-rebase-original-tables.py')
    fixtures_path = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).with_name('test-verify-credit-rebase-plan.py')
    helper, fixtures = module('fingerprints', helper_path), module('fixtures', fixtures_path)
    verifier, plan = fixtures.v, fixtures.fixture_plan()
    verifier_path = Path(verifier.__file__)
    inventory = {'format': helper.FORMAT, 'schema': plan['target']['schema'], 'tables': [{'schema': plan['target']['schema'], 'name': 'users', 'kind': 'r', 'partition': False, 'parents': [], 'bound_sha256': None, 'columns': [{'name': name, 'type': 'bigint', 'collation': None, 'not_null': name == 'id'} for name in ('id', 'quota', 'aff_quota')]}]}
    cache = Path.home() / '.cache'
    cache.mkdir(mode=0o700, parents=True, exist_ok=True)
    root = Path(tempfile.mkdtemp(prefix='credit-original-cli-', dir=cache))
    root.chmod(0o700)
    log = root / 'tests.log'
    log.touch(mode=0o600)
    inputs = {helper_path: helper_path.read_bytes(), verifier_path: verifier_path.read_bytes()}
    for name, value in [('inventory', inventory), ('plan', plan)]:
        path = root / (name + '.json')
        path.write_bytes((helper.canonical(value) + '\n').encode('utf-8'))
        path.chmod(0o600)
        inputs[path] = path.read_bytes()
    env = {key: value for key, value in os.environ.items() if not key.startswith('PG')}
    env['PYTHONDONTWRITEBYTECODE'] = '1'
    count = 0

    def check(name, passed):
        nonlocal count
        assert passed, 'FAIL: ' + name + '; private log: ' + str(log)
        count += 1
        print('PASS ' + str(count) + ': ' + name, flush=True)

    def run(stage, output, receipt, plan_path=root / 'plan.json'):
        args = ['inventory', '--schema', inventory['schema']] if stage == 'inventory' else ['stage', '--stage', stage, '--inventory', str(root / 'inventory.json'), '--plan', str(plan_path), '--verifier', str(verifier_path)]
        result = subprocess.run([sys.executable, str(helper_path), *args, '--output', str(output), '--receipt', str(receipt)], capture_output=True, text=True, env=env, preexec_fn=lambda: os.umask(0o777))
        with log.open('a', encoding='utf-8') as stream:
            stream.write(result.stdout + result.stderr + '\nexit=' + str(result.returncode) + '\n')
        return result

    digest = lambda value: hashlib.sha256(value).hexdigest()
    stage_artifacts = {}
    for stage in ('inventory', 'before', 'after'):
        artifacts = [(root / (stage + str(index) + '.sql'), root / (stage + str(index) + '.receipt.json')) for index in (1, 2)]
        for output, receipt in artifacts:
            result = run(stage, output, receipt)
            check(stage + ' CLI succeeded', result.returncode == 0)
            expected = {'format': 'lmm-credit-original-fingerprint-sql-v1', 'stage': stage, 'generator_sha256': digest(inputs[helper_path]), 'inventory_sha256': None if stage == 'inventory' else digest(inputs[root / 'inventory.json']), 'plan_sha256': None if stage == 'inventory' else digest(inputs[root / 'plan.json']), 'verifier_sha256': None if stage == 'inventory' else digest(inputs[verifier_path]), 'sql_sha256': digest(output.read_bytes()), 'table_count': 0 if stage == 'inventory' else 1, 'column_count': 0 if stage == 'inventory' else 3, 'restoration_fields_count': 0 if stage == 'inventory' else sum(len(row['fields']) for row in verifier.planned_restorations(plan)), 'audit_rows_count': 0 if stage == 'inventory' else len(verifier.declared_audit_rows(plan))}
            check(stage + ' receipt binds exact source and SQL bytes', json.loads(receipt.read_bytes()) == expected and json.loads(result.stdout) == expected)
        check(stage + ' repeated SQL and receipts stable', all(left.read_bytes() == right.read_bytes() for left, right in zip(artifacts[0], artifacts[1])))
        sql = artifacts[0][0].read_text(encoding='utf-8')
        stage_artifacts[stage] = artifacts[0]
        check(stage + ' bounded resource settings present', all(setting in sql for setting in (
            'SET LOCAL jit=off;', 'SET LOCAL max_parallel_workers_per_gather=0;',
            "SET LOCAL work_mem='4MB';", 'SET LOCAL hash_mem_multiplier=1;')))
        if stage != 'inventory':
            head, body = verifier.postgres_verification_sql(plan, stage).removesuffix('COMMIT;\n').split('\nDO ', 1)
            check(stage + ' original independent stage DO preserved', sql.startswith(head + '\n' + helper.SETTINGS + helper.inventory_guard(inventory, stage) + 'DO ' + body) and sql.count('DO ' + body) == 1)
            check(stage + ' executes each table separately', '\nUNION ALL\n' not in sql and sql.count('\\gexec\n') == 1)
        check(stage + ' single read-only RR transaction', len(re.findall(r'^BEGIN(?: TRANSACTION)? ISOLATION LEVEL REPEATABLE READ READ ONLY;$', sql, re.M)) == 1 and sql.endswith('COMMIT;\n') and len(re.findall(r'^COMMIT;$', sql, re.M)) == 1)
    large = json.loads(helper.canonical(inventory))
    original = large['tables'][0]
    large['tables'].extend(dict(original, name='table_' + str(index).zfill(3)) for index in range(166))
    for stage in ('before', 'after'):
        sql = helper.fingerprint_sql(large, [], stage)
        check(stage + ' full 167-table output uses separate query texts',
              '\nUNION ALL\n' not in sql and sql.count(' AS table_name,count(*)') == 167 and
              sql.count('\\gexec\n') == 1 and
              ') per_table(table_name,statement) ORDER BY table_name COLLATE "C"' in sql)

    runner = module('fingerprint_runner_compatibility', Path(__file__).with_name('run-credit-financial-maintenance.py'))
    bind = lambda path: {'path':str(path), 'sha256':digest(path.read_bytes())}
    baseline = root / 'baseline.tsv'
    baseline.write_bytes(b'fixture_credit_verify.users|2|' + b'a'*64 + b'\n')
    baseline.chmod(0o600)
    bindings = {'inventory':bind(root/'inventory.json'), 'before_result':bind(baseline)}
    for prefix in ('production', 'clone'):
        for stage in ('before', 'after'):
            sql_path, receipt_path = stage_artifacts[stage]
            bindings[prefix+'_'+stage+'_sql'] = bind(sql_path)
            bindings[prefix+'_'+stage+'_receipt'] = bind(receipt_path)
    seal = {'original_fingerprint':bindings, 'business_plan':bind(root/'plan.json'), 'clone_plan':bind(root/'plan.json')}
    work = root/'pipeline'
    work.mkdir(mode=0o700)
    pipeline_plan = {'transition_id':'offline-fixture', 'transition_intent_sha256':'1'*64,
                     'database':{'schema':inventory['schema']}, 'fingerprint_generator':bind(helper_path), 'verifier':bind(verifier_path)}
    controller = runner.Controller(pipeline_plan, '2'*64, work)
    database_calls = []
    def mock_sql(label, binding):
        database_calls.append((label, binding))
        return baseline.read_bytes()
    controller.sql = mock_sql
    controller.verify_fingerprint_seal(seal)
    check('unmodified controller regenerates all four exact sealed SQL and receipts',
          controller.operation_sequence == 4 and len(list(work.glob('generated-original-*.sql'))) == 4 and
          database_calls == [('seal-original-before', bindings['production_before_sql'])])
    value = controller.fingerprint(seal, 'production', 'after')
    check('unmodified controller compares complete output bytes to historical baseline',
          value['sha256'] == digest(baseline.read_bytes()) and Path(value['path']).read_bytes() == baseline.read_bytes())
    controller.sql = lambda *args: baseline.read_bytes().replace(b'|2|', b'|3|')
    try:
        controller.fingerprint(seal, 'production', 'after')
    except runner.GateFailed as error:
        rejected = 'full-original-table-fingerprint-mismatch' in str(error)
    else:
        rejected = False
    check('unmodified controller refuses any complete-output mismatch', rejected)
    output, receipt = artifacts[0]
    before = output.read_bytes()
    absent_receipt = root / 'absent.receipt.json'
    result = run('after', output, absent_receipt)
    check('existing output refused without overwrite', result.returncode != 0 and 'FileExistsError' in result.stderr and not result.stdout and output.read_bytes() == before and not absent_receipt.exists())
    before = receipt.read_bytes()
    fresh_output = root / 'receipt-collision.sql'
    result = run('after', fresh_output, receipt)
    check('existing receipt refused without overwrite', result.returncode != 0 and 'FileExistsError' in result.stderr and not result.stdout and receipt.read_bytes() == before and fresh_output.read_bytes() == output.read_bytes())
    bad = root / 'bad-plan.json'
    bad.write_bytes((helper.canonical(plan | {'plan_sha256': '0' * 64}) + '\n').encode('utf-8'))
    bad.chmod(0o600)
    inputs[bad] = bad.read_bytes()
    rejected_output, rejected_receipt = root / 'rejected.sql', root / 'rejected.receipt.json'
    result = run('before', rejected_output, rejected_receipt, bad)
    check('bad plan SHA rejected before artifacts', result.returncode != 0 and 'plan SHA-256 mismatch' in result.stderr and not result.stdout and not rejected_output.exists() and not rejected_receipt.exists())
    check('all input bytes unchanged', all(path.read_bytes() == value for path, value in inputs.items()))
    check('all artifacts 0600 under strict umask', all(path.stat().st_mode & 0o777 == 0o600 for path in root.rglob('*') if path.is_file()))
    print('PASS: ' + str(count) + ' offline CLI checks', flush=True)


if __name__ == '__main__':
    main()
