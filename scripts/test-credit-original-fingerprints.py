#!/usr/bin/env python3
"""Offline CLI receipts and real verifier API; never execute generated SQL."""
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
    root = Path(tempfile.mkdtemp(prefix='credit-original-cli-', dir=Path.home() / '.cache'))
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
    for stage in ('inventory', 'before', 'after'):
        artifacts = [(root / (stage + str(index) + '.sql'), root / (stage + str(index) + '.receipt.json')) for index in (1, 2)]
        for output, receipt in artifacts:
            result = run(stage, output, receipt)
            check(stage + ' CLI succeeded', result.returncode == 0)
            expected = {'format': 'lmm-credit-original-fingerprint-sql-v1', 'stage': stage, 'generator_sha256': digest(inputs[helper_path]), 'inventory_sha256': None if stage == 'inventory' else digest(inputs[root / 'inventory.json']), 'plan_sha256': None if stage == 'inventory' else digest(inputs[root / 'plan.json']), 'verifier_sha256': None if stage == 'inventory' else digest(inputs[verifier_path]), 'sql_sha256': digest(output.read_bytes()), 'table_count': 0 if stage == 'inventory' else 1, 'column_count': 0 if stage == 'inventory' else 3, 'restoration_fields_count': 0 if stage == 'inventory' else sum(len(row['fields']) for row in verifier.planned_restorations(plan)), 'audit_rows_count': 0 if stage == 'inventory' else len(verifier.declared_audit_rows(plan))}
            check(stage + ' receipt binds exact source and SQL bytes', json.loads(receipt.read_bytes()) == expected and json.loads(result.stdout) == expected)
        check(stage + ' repeated SQL and receipts stable', all(left.read_bytes() == right.read_bytes() for left, right in zip(artifacts[0], artifacts[1])))
        sql = artifacts[0][0].read_text(encoding='utf-8')
        if stage != 'inventory':
            head, body = verifier.postgres_verification_sql(plan, stage).removesuffix('COMMIT;\n').split('\nDO ', 1)
            check(stage + ' original independent stage DO preserved', sql.startswith(head + '\n' + helper.SETTINGS + helper.inventory_guard(inventory, stage) + 'DO ' + body) and sql.count('DO ' + body) == 1)
        check(stage + ' single read-only RR transaction', len(re.findall(r'^BEGIN(?: TRANSACTION)? ISOLATION LEVEL REPEATABLE READ READ ONLY;$', sql, re.M)) == 1 and sql.endswith('COMMIT;\n') and len(re.findall(r'^COMMIT;$', sql, re.M)) == 1)
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
    check('all artifacts 0600 under strict umask', all(path.is_file() and path.stat().st_mode & 0o777 == 0o600 for path in root.iterdir()))
    print('PASS: ' + str(count) + ' offline CLI checks', flush=True)


if __name__ == '__main__':
    main()
