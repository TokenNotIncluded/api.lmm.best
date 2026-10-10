#!/usr/bin/env python3
"""Synthetic executor only; no PostgreSQL, clone, owner or SSH invocation."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import unittest
from unittest.mock import patch

HERE = Path(__file__).resolve().parent
def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module
repair = load('casbin_tail', HERE/'complete-credit-clone-casbin-ids.py')
previous_tests = load('previous_tail_tests', HERE/'test-complete-credit-clone-rehearsal.py')
h, original = previous_tests.tail, previous_tests.original


class Fixture(previous_tests.Fixture):
    def __init__(self):
        super().__init__()
        self.new_patches = []
        self.patch('__sealed_sha256__', repair.PRIOR_SHA) if hasattr(h, '__sealed_sha256__') else self.tag_helper()
        table = h.CLONE['schema']+'.casbin_rule'
        lines = [table+'|7|'+'a'*64] + [h.CLONE['schema']+'.fixture_'+str(i)+'|0|'+'b'*64 for i in range(166)]
        self.fingerprint = ('\n'.join(sorted(lines))+'\n').encode()
        self.failed_fingerprint = self.fingerprint.replace((table+'|7|'+'a'*64).encode(), (table+'|7|'+'c'*64).encode())
        self.patch('FINGERPRINT_SHA', original.digest(self.fingerprint))
        self.seal['original_fingerprint']['before_result'] = self.binding(self.work/'original-before.tsv', self.fingerprint)
        for name in self.log_specs:
            if 'original-before' in name or 'original-after' in name:
                self.write(self.work/name, self.fingerprint)
                self.log_specs[name] = (len(self.fingerprint), original.digest(self.fingerprint))
        self.seal_binding = self.binding(Path(self.state['business_seal']['path']), original.encode(self.seal))
        self.state['business_seal'] = self.seal_binding; self.patch('SEAL_SHA', self.seal_binding['sha256']); self.rebind_state()
        self.old_rows = []
        for i, identity in enumerate((567,568,750,751,752,753,754)):
            self.old_rows.append({'id':str(identity),'ptype':'p','v0':'role:admin' if identity>=750 else 'fixture:retained',
                'v1':'fixture_resource_'+str(i),'v2':'read','v3':'allow','v4':'','v5':''})
        self.new_rows = [dict(row,id=int(row['id'])+5 if int(row['id'])>=750 else int(row['id'])) for row in self.old_rows]
        self.current_rows = copy.deepcopy(self.new_rows); self.sequence = (900, True); self.mutations = 0
        self.before_path = self.work/'clone-casbin-before-from-final-backup.actual.private.json'
        self.after_path = self.work/'clone-casbin-after-schema.actual.private.json'
        self.diff_path = self.work/'clone-casbin-diff.actual.private.receipt.json'
        for constant, path, data in [('BEFORE_SHA',self.before_path,self.old_rows),('AFTER_SHA',self.after_path,self.new_rows),
                ('DIFF_SHA',self.diff_path,{'backup_rows':7,'clone_rows':7,'policy_without_id_equal':True,'production_read':False})]:
            self.write(path,original.encode(data)); self.patch_new(constant,original.digest(path.read_bytes()))
        latest = {}
        for name in repair.LATEST:
            body = self.failed_fingerprint if name.startswith('000613') else b'' if name.startswith('000608') else b'opaque successful original command log'
            self.write(self.work/name,body); latest[name] = (len(body),original.digest(body))
        self.patch_new('LATEST',latest)
        old_dir = self.work/'clone-tail-completion'; old_dir.mkdir(mode=0o700)
        for name in h.LOGS: self.write(old_dir/name,(self.work/name).read_bytes())
        current_path = self.work/'000605-clone-original-after.tsv'; self.write(current_path,self.fingerprint)
        extras = {'old-state.json':(self.work/'state.json').read_bytes(),'intent.json':b'opaque old immutable attempt intent',
                  'failure.json':b'opaque old immutable fingerprint failure','current-clone-proof.json':original.encode({'after_fingerprint':{'path':str(current_path),'sha256':h.FINGERPRINT_SHA}})}
        for name, body in extras.items(): self.write(old_dir/name,body)
        self.patch_new('OLD_EXTRA',{name:(len(body),original.digest(body)) for name,body in extras.items()})
        self.controller = repair.controller_type(h,original)(self.plan,h.PLAN_SHA,self.work,executor=self.executor)
        self.controller.frozen_gates = self.freeze
        persist = self.controller.persist
        def checkpoint(phase, **values): self.events.append('persist'); persist(phase,**values)
        self.controller.persist = checkpoint

    def tag_helper(self):
        p = patch.object(h,'__sealed_sha256__',repair.PRIOR_SHA,create=True); p.start(); self.patches.append(p)

    def patch_new(self, name, value):
        p = patch.object(repair,name,value); p.start(); self.new_patches.append(p)

    def close(self):
        for p in reversed(self.new_patches): p.stop()
        super().close()

    def executor(self, argv, **options):
        body = options.get('input') or b''
        if '--apply' in argv: raise AssertionError('schema apply is forbidden in this incident')
        if b'LOCK TABLE' in body:
            self.calls.append((argv,options)); self.events.append('restore-ids')
            text = body.decode()
            if re.search(r'\b(DELETE|INSERT|ALTER|TRUNCATE|setval|nextval)\b',text,re.I): raise AssertionError('only UPDATE may write')
            if text.count('UPDATE ') != 1 or text.count('BEGIN;') != 1 or not text.endswith('COMMIT;'): raise AssertionError('single guarded transaction required')
            for guard in ('inet_server_addr()', 'inet_server_port() IS NOT NULL', 'current_schema() IS DISTINCT FROM', 'pg_control_system()', 'pg_database', 'pg_namespace', 'IS DISTINCT FROM', 'changed<>5', 'last_value,is_called', 'new_called IS DISTINCT FROM prior_called'):
                if guard not in text: raise AssertionError('missing transaction guard')
            literals = re.findall(r"IF actual IS DISTINCT FROM '((?:''|[^'])*)'::jsonb",text)
            expected_new, expected_old = [json.loads(value.replace("''","'")) for value in literals]
            if sorted(self.current_rows,key=lambda r:r['id']) != expected_new or self.fail in ('restore-ids','rowcount'):
                return subprocess.CompletedProcess(argv,7,b'synthetic row CAS rejected',b'')
            mapping = {int(a):int(b) for a,b in re.findall(r'WHEN (\d+) THEN (\d+)',text)}
            result = [dict(row,id=mapping.get(row['id'],row['id'])) for row in self.current_rows]
            if len([row for row in self.current_rows if row['id'] in mapping]) != 5 or sorted(result,key=lambda r:r['id']) != expected_old:
                return subprocess.CompletedProcess(argv,7,b'synthetic postimage rejected',b'')
            self.current_rows = result; self.mutations += 1
            return subprocess.CompletedProcess(argv,0,b'',b'')
        if b'fingerprint-after;' in body and sorted(self.current_rows,key=lambda r:r['id']) != repair.rows(self.old_rows,old=True):
            self.calls.append((argv,options)); self.events.append('fingerprint')
            return subprocess.CompletedProcess(argv,0,self.failed_fingerprint,b'')
        return super().executor(argv,**options)


class CasbinIDTailTests(unittest.TestCase):
    def setUp(self): self.f = Fixture(); self.addCleanup(self.f.close)
    def execute(self): return repair.complete(h,original,self.f.controller,execute=True,confirm='api.lmm.best')

    def test_default_precheck_has_no_process_sql_or_write(self):
        before = self.f.snapshot(); result = repair.complete(h,original,self.f.controller)
        self.assertFalse(result['execute']); self.assertEqual(self.f.calls,[]); self.assertEqual(self.f.snapshot(),before)

    def test_exact_five_update_verify_only_order_fresh_fps_original_persist(self):
        original_state = (self.f.work/'state.json').read_bytes(); old_audit = {p.name:p.read_bytes() for p in (self.f.work/'clone-tail-completion').iterdir()}
        result = self.execute(); self.assertEqual(result,{'ok':True,'phase':'REHEARSED','sequence':30})
        self.assertEqual(self.f.events,['freeze','identity','restore-ids','after-sql','fingerprint','identity','verify','identity','verify','fingerprint','source','clean','regression','freeze','persist'])
        self.assertEqual(self.f.mutations,1); self.assertEqual(self.f.sequence,(900,True)); self.assertEqual(sorted(self.f.current_rows,key=lambda r:r['id']),repair.rows(self.f.old_rows,old=True))
        audit = self.f.work/'casbin-id-tail-completion'; proof = original.decode((audit/'complete.json').read_bytes())
        self.assertNotEqual(proof['restored_fingerprint']['path'],proof['final_fingerprint']['path'])
        self.assertEqual(original.read_bound(proof['restored_fingerprint']),self.f.fingerprint); self.assertEqual(original.read_bound(proof['final_fingerprint']),self.f.fingerprint)
        self.assertEqual((audit/'old-state.json').read_bytes(),original_state)
        for name, raw in old_audit.items(): self.assertEqual((self.f.work/'clone-tail-completion'/name).read_bytes(),raw)
        state = original.decode((self.f.work/'state.json').read_bytes())
        self.assertEqual(state['clone_before_fingerprint'],{'path':str(self.f.work/'000561-clone-original-before.tsv'),'sha256':h.FINGERPRINT_SHA})
        allowed = {'phase','sequence','updated_at','rehearsal_seal_sha256','clone_before_fingerprint','clone_after_fingerprint'}
        self.assertEqual({k:v for k,v in state.items() if k not in allowed},{k:v for k,v in self.f.state.items() if k not in allowed})

    def test_verify_only_subclass_and_entry_reject_apply_serve_rehearse(self):
        for label, mode in [('candidate-schema','apply'),('candidate','apply'),('rehearse','verify'),('serve','verify')]:
            with self.subTest(label=label),self.assertRaises(original.GateFailed): self.f.controller.verify_candidate(label,mode)
        self.assertEqual(self.f.calls,[])
        with patch.object(repair,'load_prior',side_effect=AssertionError('must not import')):
            for args in (['--apply'],['serve'],['rehearse']):
                with self.subTest(args=args),self.assertRaises(SystemExit): repair.main(args)

    def test_old_str_and_new_int_ids_reject_bool_float_leading_zero_or_wrongtype(self):
        for old, identity in ((True,'0750'),(True,750),(True,True),(False,'755'),(False,755.0),(False,True)):
            data = copy.deepcopy(self.f.old_rows if old else self.f.new_rows); data[2]['id'] = identity
            with self.subTest(old=old,id=identity),self.assertRaises(RuntimeError): repair.rows(data,old=old)

    def test_all_policy_columns_preserve_null_without_collapsing_empty_string(self):
        rows = copy.deepcopy(self.f.old_rows); rows[2]['v5'] = None
        normalized = repair.rows(rows,old=True); self.assertIsNone(next(r for r in normalized if r['id']==750)['v5'])
        new = copy.deepcopy(self.f.new_rows); new[2]['v5'] = None
        body = repair.guard_sql(h,normalized,repair.rows(new,old=False)); self.assertIn(b'"v5":null',body)
        data = copy.deepcopy(self.f.new_rows); data[2]['v5'] = None; self.f.write(self.f.after_path,original.encode(data)); self.f.patch_new('AFTER_SHA',original.digest(self.f.after_path.read_bytes()))
        with self.assertRaises(original.GateFailed): self.execute()
        self.assertEqual(self.f.calls,[])

    def test_policy_semantic_drift_growth_deletion_duplicate_and_mapping_reject(self):
        for kind in ('policy','grow','delete','duplicate','wrong-id','not-builtin'):
            data = copy.deepcopy(self.f.new_rows)
            if kind=='policy': data[2]['v2']='write'
            elif kind=='grow': data.append(dict(data[-1],id=760))
            elif kind=='delete': data.pop()
            elif kind=='duplicate': data[-1]=copy.deepcopy(data[-2])
            elif kind=='wrong-id': data[-1]['id']=760
            else: data[2]['v0']='fixture:unexpected'
            with self.subTest(kind=kind):
                self.f.write(self.f.after_path,original.encode(data)); self.f.patch_new('AFTER_SHA',original.digest(self.f.after_path.read_bytes()))
                with self.assertRaises((original.GateFailed,RuntimeError)): self.execute()
                self.assertEqual(self.f.calls,[]); self.assertFalse((self.f.work/'casbin-id-tail-completion').exists())

    def test_bound_state_plan_backup_rows_probe_or_each_log_drift_zero_write(self):
        files = [self.f.work/'state.json',self.f.plan_path,Path(self.f.state['backup']['path']),self.f.before_path,self.f.after_path,self.f.diff_path,self.f.probe]+[self.f.work/name for name in repair.LATEST]
        for file in files:
            raw = file.read_bytes(); file.write_bytes(raw+b'changed'); before = self.f.snapshot()
            with self.subTest(file=file.name),self.assertRaises((h.CompletionFailed,original.GateFailed)): self.execute()
            self.assertEqual(self.f.snapshot(),before); self.assertEqual(self.f.calls,[]); file.write_bytes(raw)

    def test_original_audit_extra_missing_or_changed_file_rejects(self):
        directory = self.f.work/'clone-tail-completion'; extra = directory/'unknown.json'; self.f.write(extra,b'extra')
        with self.assertRaises(original.GateFailed): self.execute()
        extra.unlink(); file = directory/'failure.json'; raw=file.read_bytes(); file.write_bytes(raw+b'changed')
        with self.assertRaises(h.CompletionFailed): self.execute()
        file.write_bytes(raw); file.unlink()
        with self.assertRaises(original.GateFailed): self.execute()
        self.assertEqual(self.f.calls,[])

    def test_phase_sequence_target_pid_plan_object_and_only_table_drift_reject(self):
        for field, value in [('phase','REHEARSED'),('sequence',30),('clone_identity',dict(h.IDENTITY,system_identifier='wrong'))]:
            saved = copy.deepcopy(self.f.state); self.f.state[field]=value; self.f.rebind_state()
            with self.subTest(field=field),self.assertRaises(original.GateFailed): self.execute()
            self.f.state=saved; self.f.rebind_state()
        self.f.controller.operation_sequence=614
        with self.assertRaises(original.GateFailed): self.execute()
        self.f.controller.operation_sequence=613; self.f.controller.plan=dict(self.f.plan,source_sha='0'*40)
        with self.assertRaises(original.GateFailed): self.execute()
        self.assertEqual(self.f.calls,[])

    def test_full_fingerprint_other_table_difference_cannot_be_ignored(self):
        file = self.f.work/'000613-clone-original-after.log'; raw = file.read_bytes().replace(b'|0|'+b'b'*64,b'|1|'+b'b'*64,1); self.f.write(file,raw)
        latest = dict(repair.LATEST); latest[file.name]=(len(raw),original.digest(raw)); self.f.patch_new('LATEST',latest)
        with self.assertRaises(original.GateFailed): self.execute()
        self.assertEqual(self.f.calls,[])

    def test_real_pipe_fingerprint_missing_duplicate_malformed_count_and_hash_reject(self):
        self.assertEqual(len(repair.fingerprints(self.f.fingerprint)),167)
        lines = self.f.fingerprint.splitlines(keepends=True)
        invalid = [b''.join(lines[1:]),b''.join(lines[:-1]+[lines[0]]),self.f.fingerprint.replace(b'|',b'\t'),
                   self.f.fingerprint.replace(b'|7|',b'|07|',1),self.f.fingerprint.replace(b'|7|',b'|-7|',1),
                   self.f.fingerprint.replace(b'a'*64,b'a'*63,1),self.f.fingerprint.replace(b'a'*64,b'A'*64,1),self.f.fingerprint[:-1]]
        for raw in invalid:
            with self.subTest(raw_length=len(raw)),self.assertRaises(RuntimeError): repair.fingerprints(raw)

    def test_guarded_current_row_cas_or_rowcount_failure_never_mutates_or_checkpoints(self):
        self.f.current_rows[0]['v3']='changed at execution'
        with self.assertRaises(original.GateFailed): self.execute()
        self.assertEqual(self.f.mutations,0); self.assertNotIn('persist',self.f.events); self.assertNotIn('after-sql',self.f.events)
        audit = self.f.work/'casbin-id-tail-completion'; self.assertTrue((audit/'intent.json').is_file() and (audit/'failure.json').is_file())
        count=len(self.f.calls)
        with self.assertRaises(original.GateFailed): self.execute()
        self.assertEqual(len(self.f.calls),count)

    def test_post_restore_verification_or_final_freeze_failure_never_checkpoints(self):
        for failure in ('verify','fingerprint','source','dirty','regression','final-freeze','rowcount'):
            f = Fixture()
            try:
                f.fail=failure
                with self.subTest(failure=failure),self.assertRaises(original.GateFailed): repair.complete(h,original,f.controller,execute=True,confirm='api.lmm.best')
                self.assertNotIn('persist',f.events); self.assertEqual(original.decode((f.work/'state.json').read_bytes())['phase'],'REHEARSAL_FAILED'); self.assertTrue((f.work/'casbin-id-tail-completion/failure.json').is_file())
            finally: f.close()

    def test_final_state_cas_and_confirmation_are_enforced(self):
        with self.assertRaises(original.GateFailed): repair.complete(h,original,self.f.controller,execute=True,confirm='wrong')
        self.assertEqual(self.f.calls,[])
        def freeze():
            self.f.freeze()
            if self.f.events.count('freeze')==2: (self.f.work/'state.json').write_bytes(b'changed externally')
        self.f.controller.frozen_gates=freeze
        with self.assertRaises(h.CompletionFailed): self.execute()
        self.assertNotIn('persist',self.f.events)

    def test_prior_helper_seal_rejects_wrong_bytes_link_type_and_unsealed_import(self):
        source = self.f.work/'prior.py'; self.f.write(source,repair.PRIOR_PATH.read_bytes()); self.assertEqual(repair.load_prior(source).__sealed_sha256__,repair.PRIOR_SHA)
        source.write_bytes(source.read_bytes()+b'changed')
        with self.assertRaises(RuntimeError): repair.load_prior(source)
        source.unlink(); source.symlink_to(repair.PRIOR_PATH)
        with self.assertRaises(RuntimeError): repair.load_prior(source)
        source.unlink(); os.mkfifo(source,0o600)
        with self.assertRaises(RuntimeError): repair.load_prior(source)
        source.unlink(); self.f.write(source,repair.PRIOR_PATH.read_bytes()); os.link(source,self.f.work/'hardlink.py')
        with self.assertRaises(RuntimeError): repair.load_prior(source)
        with patch.object(h,'__sealed_sha256__','0'*64),self.assertRaises(original.GateFailed): self.execute()


if __name__=='__main__': unittest.main(verbosity=2)
