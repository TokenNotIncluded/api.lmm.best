#!/usr/bin/env python3
"""Generate private read-only SQL; never connect to a database or apply changes."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path

FORMAT = 'lmm-credit-original-table-inventory-v1'
SETTINGS = """SET LOCAL standard_conforming_strings=on;
SET LOCAL search_path=pg_catalog;
SET LOCAL row_security=off;
SET LOCAL TimeZone='UTC';
SET LOCAL DateStyle='ISO, YMD';
SET LOCAL IntervalStyle='postgres';
SET LOCAL extra_float_digits=3;
SET LOCAL bytea_output='hex';
"""


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False, allow_nan=False)


def literal(value):
    return "'" + value.replace("'", "''") + "'"


def ident(value):
    if not isinstance(value, str) or not value or '\0' in value:
        raise ValueError('invalid catalog identifier')
    return '"' + value.replace('"', '""') + '"'


def do_block(body):
    delimiter = '$original_' + hashlib.sha256(body.encode()).hexdigest() + '$'
    while delimiter in body:
        delimiter = delimiter[:-1] + 'x$'
    return 'DO ' + delimiter + '\n' + body + '\n' + delimiter + ';\n'


def catalog_query(schema):
    ident(schema)
    return f"""SELECT jsonb_build_object('format',{literal(FORMAT)},'schema',{literal(schema)},
 'tables',COALESCE(jsonb_agg(t ORDER BY t->>'schema' COLLATE "C",t->>'name' COLLATE "C"),'[]'::jsonb))
FROM (SELECT jsonb_build_object('schema',n.nspname,'name',c.relname,'kind',c.relkind::text,
 'partition',c.relispartition,'parents',COALESCE((SELECT jsonb_agg(format('%I.%I',pn.nspname,p.relname) ORDER BY pn.nspname COLLATE "C",p.relname COLLATE "C")
 FROM pg_inherits i JOIN pg_class p ON p.oid=i.inhparent JOIN pg_namespace pn ON pn.oid=p.relnamespace WHERE i.inhrelid=c.oid),'[]'::jsonb),
 'bound_sha256',CASE WHEN c.relispartition THEN encode(sha256(convert_to(pg_get_expr(c.relpartbound,c.oid,false),'UTF8')),'hex') END,
 'columns',COALESCE((SELECT jsonb_agg(jsonb_build_object('name',a.attname,'type',format_type(a.atttypid,a.atttypmod),
 'collation',CASE WHEN a.attcollation<>0 THEN format('%I.%I',cn.nspname,co.collname) END,'not_null',a.attnotnull) ORDER BY a.attnum)
 FROM pg_attribute a LEFT JOIN pg_collation co ON co.oid=a.attcollation LEFT JOIN pg_namespace cn ON cn.oid=co.collnamespace
 WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped),'[]'::jsonb)) AS t
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname<>'information_schema' AND n.nspname !~ '^pg_' AND c.relkind IN ('r','p')) original_tables"""


def inventory_sql(schema):
    return ('\\set ON_ERROR_STOP on\nBEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;\n' + SETTINGS
            + catalog_query(schema) + ';\nCOMMIT;\n')


def validate_inventory(value):
    if not isinstance(value, dict) or set(value) != {'format', 'schema', 'tables'} or value['format'] != FORMAT:
        raise ValueError('unsupported original inventory')
    ident(value['schema'])
    if not isinstance(value['tables'], list) or not value['tables']:
        raise ValueError('original inventory must contain tables')
    seen = set()
    for table in value['tables']:
        if set(table) != {'schema', 'name', 'kind', 'partition', 'parents', 'bound_sha256', 'columns'}:
            raise ValueError('incomplete original table metadata')
        identity = (table['schema'], table['name'])
        if identity in seen or table['kind'] not in ('r', 'p') or type(table['partition']) is not bool:
            raise ValueError('invalid or duplicate original table')
        seen.add(identity)
        for part in identity:
            ident(part)
        names = [column['name'] for column in table['columns']]
        if not table['columns'] or len(names) != len(set(names)):
            raise ValueError('invalid original columns')
        for column in table['columns']:
            if set(column) != {'name', 'type', 'collation', 'not_null'} or type(column['not_null']) is not bool:
                raise ValueError('incomplete original column metadata')
            ident(column['name'])
    return value


def inventory_guard(inventory, stage):
    expected = literal(canonical(inventory))
    exact = "IF actual IS DISTINCT FROM expected THEN RAISE EXCEPTION 'original inventory changed'; END IF;" if stage == 'before' else ''
    return do_block(f"""DECLARE expected jsonb:={expected}::jsonb; actual jsonb; old_table jsonb; new_table jsonb; old_column jsonb; new_column jsonb;
BEGIN
 {catalog_query(inventory['schema'])} INTO actual;
 {exact}
 FOR old_table IN SELECT value FROM jsonb_array_elements(expected->'tables') LOOP
  SELECT value INTO new_table FROM jsonb_array_elements(actual->'tables') WHERE value->>'schema'=old_table->>'schema' AND value->>'name'=old_table->>'name';
  IF new_table IS NULL OR new_table-'columns' IS DISTINCT FROM old_table-'columns' THEN RAISE EXCEPTION 'original table missing or metadata changed'; END IF;
  FOR old_column IN SELECT value FROM jsonb_array_elements(old_table->'columns') LOOP
   SELECT value INTO new_column FROM jsonb_array_elements(new_table->'columns') WHERE value->>'name'=old_column->>'name';
   IF new_column IS DISTINCT FROM old_column THEN RAISE EXCEPTION 'original column missing or metadata changed'; END IF;
  END LOOP;
 END LOOP;
 IF EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname<>'information_schema' AND n.nspname !~ '^pg_' AND c.relkind='f') THEN RAISE EXCEPTION 'foreign tables require a separate complete preservation proof'; END IF;
END;""")


def fingerprint_sql(inventory, restorations, stage, audit_rows=(), *, include_guard=True):
    validate_inventory(inventory)
    if stage not in ('before', 'after'):
        raise ValueError('invalid fingerprint stage')
    sql = [SETTINGS, inventory_guard(inventory, stage)] if include_guard else []
    selects = []
    for table in inventory['tables']:
        relation = ident(table['schema']) + '.' + ident(table['name'])
        columns = {column['name'] for column in table['columns']}
        changes = {}

        def typed(field, value):
            return f"(jsonb_populate_record(NULL::{relation},{literal(canonical({field:value}))}::jsonb)).{ident(field)}"

        def matches(values):
            return ' AND '.join(f"convert_to(r.{ident(field)}::text,'UTF8') IS NOT DISTINCT FROM convert_to(({typed(field,value)})::text,'UTF8')" for field,value in values.items())

        for row in restorations if table['schema'] == inventory['schema'] else ():
            if row['table'] != table['name']:
                continue
            if not row['key'] or set(row['key']) - columns:
                raise ValueError('restoration row key missing from original columns')
            condition = matches(row['key'])
            sql.append(do_block(f"BEGIN IF (SELECT count(*) FROM {relation} r WHERE {condition})<>1 THEN RAISE EXCEPTION 'original restoration key is not unique'; END IF; END;"))
            for field, change in row['fields'].items():
                if field not in columns or stage == 'before':
                    continue  # New nullable fields are outside the original projection.
                after = matches({field:change['after']})
                before = f"to_jsonb(({typed(field,change['before'])})::text)"
                changes.setdefault(field, []).append(f'WHEN ({condition}) AND ({after}) THEN {before}')
        exclusions = []
        for row in audit_rows if table['schema'] == inventory['schema'] else ():
            if row['table'] != table['name']:
                continue
            dynamic = row.get('dynamic_fields', {})
            if dynamic and dynamic != {'applied_at':'stage_guarded_finite_frozen_interval'}:
                raise ValueError('unsupported dynamic audit column')
            if columns - set(row['fields']) - set(dynamic):
                raise ValueError('declared audit row does not cover every original column')
            condition = matches(row['key'])
            expected = matches(row['fields'])
            count = 0 if stage == 'before' else 1
            primary = f"SELECT jsonb_agg(a.attname ORDER BY a.attname COLLATE \"C\") FROM pg_index i CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY k(attnum,position) JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.attnum WHERE i.indrelid=to_regclass({literal(relation)}) AND i.indisprimary AND k.position<=i.indnkeyatts"
            sql.append(do_block(f"BEGIN IF ({primary}) IS DISTINCT FROM {literal(canonical(sorted(row['key'])))}::jsonb THEN RAISE EXCEPTION 'declared audit key is not the complete primary key'; END IF; IF (SELECT count(*) FROM {relation} r WHERE {condition})<>{count} THEN RAISE EXCEPTION 'declared new audit row collision or missing'; END IF; END;"))
            if stage == 'after':
                exclusions.append(f'(({condition}) AND ({expected}))')
        projection = []
        for column in table['columns']:
            field = column['name']
            original = f'to_jsonb(r.{ident(field)}::text)'
            expression = 'CASE ' + ' '.join(changes[field]) + f' ELSE {original} END' if field in changes else original
            projection.append(expression + ' AS ' + ident(field))
        where = ' WHERE NOT (' + ' OR '.join(exclusions) + ')' if exclusions else ''
        # Parent queries include inherited/partitioned rows; metadata binds topology.
        rows = f"SELECT {','.join(projection)} FROM {relation} r{where}"
        row_hash = "encode(sha256(convert_to(to_jsonb(original_row)::text,'UTF8')),'hex')"
        selects.append(f"SELECT format('%I.%I',{literal(table['schema'])},{literal(table['name'])}) AS table_name,count(*) AS row_count,encode(sha256(convert_to(COALESCE(string_agg(row_hash,'' ORDER BY row_hash COLLATE \"C\"),''),'UTF8')),'hex') AS sha256 FROM (SELECT {row_hash} AS row_hash FROM ({rows}) original_row) hashed_rows")
    sql.append('SELECT table_name,row_count,sha256 FROM (\n' + '\nUNION ALL\n'.join(selects) + '\n) fingerprints ORDER BY table_name COLLATE "C";\n')
    return ''.join(sql)


def stage_sql(inventory, plan, stage, verifier):
    if inventory['schema'] != plan['target']['schema']:
        raise ValueError('original inventory target schema differs from plan')
    prefix = verifier.postgres_verification_sql(plan, stage)
    if not prefix.endswith('COMMIT;\n'):
        raise ValueError('independent verifier transaction suffix changed')
    audits = verifier.declared_audit_rows(plan)
    restores = verifier.planned_restorations(plan)
    body = prefix.removesuffix('COMMIT;\n')
    if '\nDO ' not in body:
        raise ValueError('independent stage assertions missing')
    guard = SETTINGS + inventory_guard(inventory,stage)
    body = body.replace('\nDO ','\n'+guard+'DO ',1)
    return body + fingerprint_sql(inventory, restores, stage, audits,include_guard=False) + 'COMMIT;\n', restores, audits


def private_write(path, text):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    os.fchmod(descriptor,0o600)
    with os.fdopen(descriptor, 'w',encoding='utf-8',newline='\n') as stream:
        stream.write(text)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=('inventory', 'stage'))
    for flag in ('schema', 'inventory', 'plan', 'stage', 'verifier', 'output', 'receipt'):
        parser.add_argument('--' + flag, required=flag in ('output', 'receipt'))
    args = parser.parse_args()
    source = Path(__file__).read_bytes()
    receipt = {'format':'lmm-credit-original-fingerprint-sql-v1','stage':args.stage if args.command=='stage' else 'inventory','generator_sha256':hashlib.sha256(source).hexdigest(),'inventory_sha256':None,'plan_sha256':None,'verifier_sha256':None,'table_count':0,'column_count':0,'restoration_fields_count':0,'audit_rows_count':0}
    if args.command == 'inventory':
        if not args.schema:
            parser.error('--schema required')
        sql = inventory_sql(args.schema)
    else:
        if not all((args.inventory,args.plan,args.stage,args.verifier)):
            parser.error('--inventory, --plan, --stage and --verifier required')
        inventory_bytes,plan_bytes,verifier_bytes = (Path(path).read_bytes() for path in (args.inventory,args.plan,args.verifier))
        spec = importlib.util.spec_from_file_location('independent_credit_verifier',args.verifier)
        verifier = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(verifier)
        inventory = validate_inventory(json.loads(inventory_bytes))
        plan = verifier.load_plan(args.plan)
        sql,restores,audits = stage_sql(inventory,plan,args.stage,verifier)
        receipt.update(inventory_sha256=hashlib.sha256(inventory_bytes).hexdigest(),plan_sha256=hashlib.sha256(plan_bytes).hexdigest(),verifier_sha256=hashlib.sha256(verifier_bytes).hexdigest(),table_count=len(inventory['tables']),column_count=sum(len(t['columns']) for t in inventory['tables']),restoration_fields_count=sum(len(row['fields']) for row in restores),audit_rows_count=len(audits))
    receipt['sql_sha256'] = hashlib.sha256(sql.encode()).hexdigest()
    private_write(args.output,sql)
    private_write(args.receipt,canonical(receipt)+'\n')
    print(canonical(receipt))


if __name__ == '__main__':
    main()
