#!/usr/bin/env python3
"""One incident only: restore five local-clone policy IDs, then finish the tail."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys
import types

PRIOR_PATH = Path('/home/lightjunction/.cache/credit-financial-maintenance-20261006/complete-credit-clone-rehearsal.private.py')
PRIOR_SHA = 'bdcbdc96eaecb4ff31e77241b652f047ac97664249cadfd692c1885feea6862d'
BEFORE_SHA = '41ed2ab04104096c753aad7aa795b00b99b15e7ff1bff10308e6149cc2e6a737'
AFTER_SHA = 'abfbdea63392bc5a8f81eb4d9b8c3a71388a206625119937dc9cebbbe4f09a1c'
DIFF_SHA = '3272f07d63526e6da39ee4453ec20ad56d75619c816b02826248974bbb0b9f48'
LATEST = {'000607-clone-candidate-schema-apply.log': (1882, '50fd22af1dba6599acd2b1b6a191e2bddfbbaf193d88ea9525cb3053067b3c60'), '000608-clone-after-candidate-schema.log': (0, 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'), '000609-clone-identity.log': (222, '7fa622dbeb927eaa59b7e460a79992daa76c2b5c06ca87206ae7543df29228a8'), '000610-clone-candidate-verify.log': (815, '6d82be9585d5beb44cb9bd6e564bb02c0675fbf82a776e46bfe20915a1effb1b'), '000611-clone-identity.log': (222, '7fa622dbeb927eaa59b7e460a79992daa76c2b5c06ca87206ae7543df29228a8'), '000612-clone-retained-bridge-verify.log': (815, '57e278c283b04b926f10bc3d0102d103f2d45523bcb2b9bbc21c9499fd2e532a'), '000613-clone-original-after.log': (18024, '453cb38026e5f5fbca090b4ca66071ab22aad99119f357cdfdc13082540f479e')}
OLD_EXTRA = {'current-clone-proof.json': (208, 'bc619c3956a723ecde43d7dd4d2ac34ebf4e87a0d78d05424737669e0f47c174'), 'failure.json': (74, 'f4bd29e4264dbebc6da0c947eed9c25d5d9e1142f5d0dca618cc40497cd46ede'), 'intent.json': (1627, '5adfb29ccf6ee70a49f95b152c70da6e942b80a0bf469e8b888f8f3d96ba5d91'), 'old-state.json': (5601, '0361dc9fbfa5a537deb3bf16e474c0f1fc9bbd8c11bea427206cbcfe4b146353')}
FIELDS = ('ptype', 'v0', 'v1', 'v2', 'v3', 'v4', 'v5')
MAPPING = {755: 750, 756: 751, 757: 752, 758: 753, 759: 754}


def load_prior(path=PRIOR_PATH):
    path = Path(path); before = path.lstat()
    if path.resolve() != path or not stat.S_ISREG(before.st_mode) or before.st_nlink != 1 or before.st_mode & 0o022:
        raise RuntimeError('prior-helper-file-unsafe')
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC), 'rb') as stream:
        info = os.fstat(stream.fileno()); raw = stream.read(); after = os.fstat(stream.fileno())
    identity = lambda i: (i.st_dev, i.st_ino, i.st_mode, i.st_nlink, i.st_size, i.st_mtime_ns, i.st_ctime_ns)
    if identity(before) != identity(info) or identity(info) != identity(after) or identity(after) != identity(path.lstat()) or hashlib.sha256(raw).hexdigest() != PRIOR_SHA:
        raise RuntimeError('prior-helper-seal-changed')
    h = types.ModuleType('_sealed_prior_clone_tail'); h.__file__ = str(path)
    exec(compile(raw, str(path), 'exec'), h.__dict__); h.__sealed_sha256__ = PRIOR_SHA
    return h


def rows(value, *, old):
    if not isinstance(value, list) or len(value) != 7: raise RuntimeError('exact-seven-policy-rows-required')
    result = []
    for row in value:
        if not isinstance(row, dict) or set(row) != {'id', *FIELDS} or any(row[k] is not None and not isinstance(row[k], str) for k in FIELDS): raise RuntimeError('policy-field-type')
        identity = row['id']
        if old:
            if not isinstance(identity, str) or not re.fullmatch('[1-9][0-9]*', identity): raise RuntimeError('old-id-canonical-decimal')
            identity = int(identity)
        elif type(identity) is not int or identity <= 0: raise RuntimeError('new-id-integer')
        result.append(dict(row, id=identity))
    if len({r['id'] for r in result}) != 7 or len({tuple(r[k] for k in FIELDS) for r in result}) != 7: raise RuntimeError('duplicate-policy-or-id')
    return sorted(result, key=lambda r: r['id'])


def controller_type(h, original):
    class VerifyOnlyController(h.controller_type(original)):
        def verify_candidate(self, label, mode='verify'):
            original.require((label, mode) in (('candidate', 'verify'), ('retained-bridge', 'verify')), 'casbin-tail-verify-only')
            return super().verify_candidate(label, mode)
    return VerifyOnlyController


def fingerprints(raw):
    if not raw.endswith(b'\n'): raise RuntimeError('fingerprint-line-ending')
    result = {}
    for line in raw.split(b'\n')[:-1]:
        fields = line.split(b'|')
        if len(fields)!=3 or not re.fullmatch(rb'[A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_][A-Za-z0-9_]*',fields[0]) or fields[0] in result or not re.fullmatch(rb'0|[1-9][0-9]*',fields[1]) or not re.fullmatch(rb'[0-9a-f]{64}',fields[2]): raise RuntimeError('fingerprint-row-format')
        result[fields[0]] = fields[1:]
    if len(result)!=167: raise RuntimeError('complete-167-table-fingerprint-required')
    return result


def guard_sql(h, old, new):
    literal = lambda value: "'" + json.dumps(value, separators=(',', ':')).replace("'", "''") + "'::jsonb"
    relation = '"' + h.CLONE['schema'] + '"."casbin_rule"'
    return f'''BEGIN; LOCK TABLE {relation} IN ACCESS EXCLUSIVE MODE;
DO $ids$ DECLARE actual jsonb; changed integer; sequence regclass; prior_value bigint; prior_called boolean; new_value bigint; new_called boolean; BEGIN
IF pg_catalog.inet_server_addr() IS NOT NULL OR pg_catalog.inet_server_port() IS NOT NULL OR current_user IS DISTINCT FROM 'lmm_api' OR current_database() IS DISTINCT FROM '{h.IDENTITY['database']}' OR current_schema() IS DISTINCT FROM '{h.IDENTITY['schema']}' OR (SELECT system_identifier::text FROM pg_catalog.pg_control_system()) IS DISTINCT FROM '{h.IDENTITY['system_identifier']}' OR (SELECT oid::text FROM pg_catalog.pg_database WHERE datname=current_database()) IS DISTINCT FROM '{h.IDENTITY['database_oid']}' OR (SELECT oid::text FROM pg_catalog.pg_namespace WHERE nspname=current_schema()) IS DISTINCT FROM '{h.IDENTITY['schema_oid']}' THEN RAISE EXCEPTION 'fixed local clone identity changed'; END IF;
IF EXISTS (SELECT 1 FROM pg_catalog.pg_trigger WHERE tgrelid='{relation}'::regclass AND NOT tgisinternal) OR EXISTS (SELECT 1 FROM pg_catalog.pg_rewrite WHERE ev_class='{relation}'::regclass) OR EXISTS (SELECT 1 FROM pg_catalog.pg_constraint WHERE contype='f' AND confrelid='{relation}'::regclass) THEN RAISE EXCEPTION 'policy update has unreviewed indirect writes'; END IF;
SELECT jsonb_agg(to_jsonb(r) ORDER BY id) INTO actual FROM {relation} r;
IF actual IS DISTINCT FROM {literal(new)} THEN RAISE EXCEPTION 'seven complete policy rows changed'; END IF;
sequence := pg_get_serial_sequence('{relation}', 'id')::regclass;
IF sequence IS NULL THEN RAISE EXCEPTION 'policy sequence missing'; END IF;
EXECUTE format('SELECT last_value,is_called FROM %s',sequence) INTO prior_value,prior_called;
UPDATE {relation} SET id=CASE id WHEN 755 THEN 750 WHEN 756 THEN 751 WHEN 757 THEN 752 WHEN 758 THEN 753 WHEN 759 THEN 754 END WHERE id IN (755,756,757,758,759);
GET DIAGNOSTICS changed=ROW_COUNT; IF changed<>5 THEN RAISE EXCEPTION 'exactly five IDs required'; END IF;
SELECT jsonb_agg(to_jsonb(r) ORDER BY id) INTO actual FROM {relation} r;
IF actual IS DISTINCT FROM {literal(old)} THEN RAISE EXCEPTION 'restored complete rows differ'; END IF;
EXECUTE format('SELECT last_value,is_called FROM %s',sequence) INTO new_value,new_called;
IF new_value IS DISTINCT FROM prior_value OR new_called IS DISTINCT FROM prior_called THEN RAISE EXCEPTION 'sequence changed'; END IF;
END $ids$; COMMIT;'''.encode()


def precheck(h, original, controller):
    require = original.require; work = h.WORK_PATH
    require(getattr(h, '__sealed_sha256__', None) == PRIOR_SHA and controller.work == work and controller.plan_hash == h.PLAN_SHA and original.decode(h.bound(h.PLAN_PATH, h.PLAN_SHA)) == controller.plan, 'incident-helper-plan-work')
    state_raw = h.bound(controller.state_path, h.STATE_SHA); state = original.decode(state_raw)
    require(state == controller.state and state['phase'] == 'REHEARSAL_FAILED' and type(state['sequence']) is int and state['sequence'] == 29 and state['plan_sha256'] == h.PLAN_SHA and controller.operation_sequence == 613, 'incident-state-or-sequence')
    require(controller.plan['source_sha'] == h.SOURCE_SHA and controller.plan['clone'] == h.CLONE and state['clone_identity'] == h.IDENTITY and state['clone_table_count'] == 167 and controller.clone_generation() == state['clone_generation'], 'incident-clone-source')
    require(state['business_seal'] == {'path': str(work/'business-artifacts/business-seal.json'), 'sha256': h.SEAL_SHA} and state['business_plan_sha256'] == h.BUSINESS_SHA and state['backup']['sha256'] == h.BACKUP_SHA, 'incident-business-bindings')
    seal = original.decode(h.bound(state['business_seal']['path'], h.SEAL_SHA)); stack = [seal]
    while stack:
        item = stack.pop()
        if isinstance(item, dict):
            if set(item) == {'path', 'sha256'}: h.bound(item['path'], item['sha256'])
            else: stack.extend(item.values())
        elif isinstance(item, list): stack.extend(item)
    require(seal['business_plan_sha256'] == h.BUSINESS_SHA and seal['backup'] == state['backup'] and original.decode(original.read_bound(seal['clone_plan']))['target'] == h.IDENTITY, 'incident-seal-target')
    evidence = {work/name: spec for name, spec in {**h.LOGS, **LATEST}.items()}; old_dir = work/'clone-tail-completion'
    info = old_dir.lstat(); require(old_dir.resolve() == old_dir and stat.S_ISDIR(info.st_mode) and info.st_uid==os.geteuid() and stat.S_IMODE(info.st_mode)==0o700 and set(p.name for p in old_dir.iterdir()) == set(h.LOGS)|set(OLD_EXTRA), 'original-audit-directory-changed')
    evidence.update({old_dir/name: spec for name, spec in {**h.LOGS, **OLD_EXTRA}.items()})
    for file, (size, sha) in evidence.items(): require(len(h.bound(file, sha)) == size, 'incident-evidence-size')
    current = original.decode(h.bound(old_dir/'current-clone-proof.json', OLD_EXTRA['current-clone-proof.json'][1]))['after_fingerprint']; require(current['sha256'] == h.FINGERPRINT_SHA, 'old-current-fingerprint-binding'); original.read_bound(current)
    before_fp = fingerprints(h.bound(work/'000565-clone-original-after.log', h.FINGERPRINT_SHA)); failed_fp = fingerprints(h.bound(work/'000613-clone-original-after.log', LATEST['000613-clone-original-after.log'][1])); table = (h.CLONE['schema']+'.casbin_rule').encode()
    require(len(before_fp) == len(failed_fp) == 167 and before_fp.keys() == failed_fp.keys() and [k for k in before_fp if before_fp[k] != failed_fp[k]] == [table] and before_fp[table][0] == failed_fp[table][0] == b'7', 'only-casbin-seven-row-difference-required')
    old = rows(original.decode(h.bound(work/'clone-casbin-before-from-final-backup.actual.private.json', BEFORE_SHA)), old=True); new = rows(original.decode(h.bound(work/'clone-casbin-after-schema.actual.private.json', AFTER_SHA)), old=False)
    old_by_policy = {tuple(r[k] for k in FIELDS): r['id'] for r in old}
    require(all(tuple(r[k] for k in FIELDS) in old_by_policy and old_by_policy[tuple(r[k] for k in FIELDS)] == MAPPING.get(r['id'], r['id']) for r in new) and {r['id'] for r in new} == {567,568,*MAPPING} and all(r['ptype']=='p' and r['v0']=='role:admin' for r in new if r['id'] in MAPPING), 'exact-policy-preserving-five-id-map')
    receipt = original.decode(h.bound(work/'clone-casbin-diff.actual.private.receipt.json', DIFF_SHA)); require(receipt['policy_without_id_equal'] is True and receipt['backup_rows'] == receipt['clone_rows'] == 7 and receipt['production_read'] is False, 'incident-row-diff-receipt')
    h.bound(h.PROBE_PATH, h.PROBE_SHA); require(not (work/'casbin-id-tail-completion').exists(), 'id-tail-attempt-already-exists')
    return seal, state_raw, old, new, evidence


def complete(h, original, controller, *, execute=False, confirm=None):
    seal, state_raw, old, new, evidence = precheck(h, original, controller)
    if not execute: return {'ok': True, 'execute': False, 'rows': 7, 'id_updates': 5, 'financial_sql_replay': False}
    original.require(confirm == 'api.lmm.best' and os.geteuid() != 0, 'explicit-local-clone-id-repair-confirmation')
    audit = h.WORK_PATH/'casbin-id-tail-completion'; audit.mkdir(mode=0o700)
    original.write_once(audit/'old-state.json', state_raw)
    original.write_once(audit/'intent.json', original.encode({'format':'lmm-credit-clone-casbin-id-tail-v1','wrapper_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'prior_helper_sha256':PRIOR_SHA,'state_sha256':h.STATE_SHA,'before_rows_sha256':BEFORE_SHA,'after_rows_sha256':AFTER_SHA,'logs':{str(p):s for p,s in evidence.items()},'mapping':MAPPING,'financial_sql_replay':False,'schema_apply_replay':False}))
    try:
        controller.frozen_gates(); original.require(controller.clone_identity() == h.IDENTITY, 'fixed-clone-target-changed')
        controller.clone_sql('clone-casbin-five-id-restore', guard_sql(h, old, new))
        controller.clone_sql('clone-after-id-restore', original.read_bound(seal['clone_after_sql'])); restored = controller.fingerprint(seal, 'clone', 'after')
        controller.verify_candidate('candidate'); controller.verify_candidate('retained-bridge'); after = controller.fingerprint(seal, 'clone', 'after')
        regression = controller.plan['regression']; source = controller.execute('regression-source', {'argv':['/usr/bin/git','-C',regression['source_directory'],'rev-parse','HEAD'],'timeout_seconds':30}); original.require(source.decode().strip() == regression['source_sha'] == h.SOURCE_SHA, 'regression-source-changed')
        dirty = controller.execute('regression-clean', {'argv':['/usr/bin/git','-C',regression['source_directory'],'status','--porcelain'],'timeout_seconds':30}); original.require(not dirty.strip(), 'regression-source-dirty')
        controller.execute('offline-financial-regression', {'argv':['/usr/bin/go','test','-p','1',*regression['packages'],'-run',regression['run'],'-count=1'],'timeout_seconds':3600}, cwd=regression['source_directory'], env=controller.local_environment()); controller.frozen_gates()
        h.bound(controller.state_path, h.STATE_SHA)
        for file, (_, sha) in evidence.items(): h.bound(file, sha)
        original.require(restored['path'] != after['path'] and after['path'] != str(h.WORK_PATH/'000565-clone-original-after.tsv'), 'fresh-final-fingerprint-required')
        controller.persist('REHEARSED', rehearsal_seal_sha256=h.SEAL_SHA, clone_before_fingerprint={'path':str(h.WORK_PATH/'000561-clone-original-before.tsv'),'sha256':h.FINGERPRINT_SHA}, clone_after_fingerprint=after)
        original.write_once(audit/'complete.json', original.encode({'phase':controller.state['phase'],'sequence':controller.state['sequence'],'restored_fingerprint':restored,'final_fingerprint':after})); return {'ok':True,'phase':controller.state['phase'],'sequence':controller.state['sequence']}
    except BaseException as error:
        original.write_once(audit/'failure.json', original.encode({'error_type':type(error).__name__,'replay_forbidden':True})); raise


def main(argv=None):
    sys.dont_write_bytecode = True; parser = argparse.ArgumentParser(description=__doc__); parser.add_argument('--execute',action='store_true'); parser.add_argument('--confirm'); args = parser.parse_args(argv)
    try:
        h = load_prior(); original = h.load_original(); plan = original.validate_plan(original.decode(h.bound(h.PLAN_PATH,h.PLAN_SHA))); info = h.WORK_PATH.lstat()
        original.require(stat.S_ISDIR(info.st_mode) and info.st_uid==os.geteuid() and stat.S_IMODE(info.st_mode)==0o700 and h.WORK_PATH.resolve()==h.WORK_PATH, 'controller-work-unsafe')
        path = h.WORK_PATH/'controller.lock'; info = path.lstat(); original.require(stat.S_ISREG(info.st_mode) and info.st_nlink==1 and not info.st_mode&0o077, 'controller-lock-unsafe')
        with os.fdopen(os.open(path,os.O_RDWR|os.O_NOFOLLOW|os.O_CLOEXEC),'r+') as lock:
            opened = os.fstat(lock.fileno()); original.require((opened.st_dev,opened.st_ino)==(info.st_dev,info.st_ino), 'controller-lock-changed'); fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
            print(json.dumps(complete(h,original,controller_type(h,original)(plan,h.PLAN_SHA,h.WORK_PATH),execute=args.execute,confirm=args.confirm)))
        return 0
    except (OSError,ValueError,KeyError,RuntimeError) as error:
        print(json.dumps({'ok':False,'error_type':type(error).__name__,'inspect_before_retry':True}),file=sys.stderr); return 1


if __name__ == '__main__': sys.exit(main())
