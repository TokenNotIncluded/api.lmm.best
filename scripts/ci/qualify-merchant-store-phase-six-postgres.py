#!/usr/bin/env python3
"""Validate the additional phase-six evidence without changing cap1 or 13/20 proof."""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path
import re

PACKAGE = 'github.com/LIghtJUNction/api.lmm.best/model'
PARENT = 'TestMerchantStorePhaseSixPreparationPostgresParents'
LEAVES = (
    PARENT + '/preserves_actual_phase_five_facts_and_separates_activation',
    PARENT + '/DDL_failure_rolls_back_every_phase_six_addition',
    PARENT + '/phase_six_business_transaction_retains_real_fence_and_floor_SHARE',
)
REQUIRED = (PARENT, 'TestMerchantStoreSchemaPlan', *LEAVES)


def strict_json(payload):
    def fields(pairs):
        row = {}
        for key, value in pairs:
            if key in row:
                raise ValueError('duplicate evidence field')
            row[key] = value
        return row
    return json.loads(payload, object_pairs_hook=fields)


def validate(events, plan, source_revision):
    if not re.fullmatch('[0-9a-f]{40}', source_revision):
        raise ValueError('exact source revision is required')
    if not events or any(row.get('Package') != PACKAGE or row.get('Action') in ('fail', 'skip') for row in events):
        raise ValueError('phase-six proof requires actual model-package PASS without failures or skips')
    passed = Counter(row.get('Test') for row in events if row.get('Action') == 'pass')
    if passed != Counter((None, *REQUIRED)):
        raise ValueError('phase-six schema parent and every PostgreSQL leaf must pass exactly once')
    if type(plan.get('writer_capability')) is not int or plan['writer_capability'] != 6:
        raise ValueError('compiled source plan must report writer capability six')
    tables = plan['tables']
    names = [table['name'] for table in tables]
    if len(set(names)) != len(names) or any(not name.startswith('merchant_store_') for name in names):
        raise ValueError('invalid dynamically compiled store model set')
    by_name = {table['name']: table for table in tables}
    for name in ('merchant_store_categories', 'merchant_store_product_likes'):
        if name not in by_name:
            raise ValueError('compiled phase-six model is missing')
    product = by_name['merchant_store_products']
    category = next((column for column in product['columns'] if column['name'] == 'category_id'), None)
    if not category or category.get('size') != 36 or category.get('not_null') is not True:
        raise ValueError('compiled category reference differs from the reviewed model')
    return {
        'format': 'merchant-store-phase-six-postgres-v1',
        'source_revision': source_revision,
        'writer_capability': plan['writer_capability'],
        'required_parents': [PARENT, 'TestMerchantStoreSchemaPlan'],
        'required_pg_leaves': list(LEAVES),
        'store_tables': names,
        'store_table_count': len(names),
        'phase_six_additions': ['merchant_store_categories', 'merchant_store_product_likes', 'merchant_store_products.category_id'],
        'failed': 0,
        'skipped': 0,
        'status': 'passed',
        'scope': 'disposable PostgreSQL schema and source-model proof; not production activation or native release qualification',
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--events', type=Path, required=True)
    parser.add_argument('--source-plan', type=Path, required=True)
    parser.add_argument('--source-revision', required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    event_data = args.events.read_bytes()
    plan_data = args.source_plan.read_bytes()
    events = [strict_json(line) for line in event_data.splitlines() if line.strip()]
    receipt = validate(events, strict_json(plan_data), args.source_revision)
    receipt['events_sha256'] = hashlib.sha256(event_data).hexdigest()
    receipt['source_plan_sha256'] = hashlib.sha256(plan_data).hexdigest()
    with args.output.open('x', encoding='utf8') as output:
        output.write(json.dumps(receipt, ensure_ascii=False, indent=2) + '\n')


if __name__ == '__main__':
    main()
