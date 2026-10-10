#!/usr/bin/env python3
"""Only synthetic executors; no PostgreSQL, owner, clone or SSH process runs."""
import copy
import importlib.util
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import unittest
from unittest.mock import patch

HERE=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('resource_inspection',HERE/'inspect-credit-financial-fingerprint-resources.py')
inspect=importlib.util.module_from_spec(spec); spec.loader.exec_module(inspect)
h=inspect.load_prior(); original=h.load_original()


class Fixture:
    def __init__(self):
        self.temp=tempfile.TemporaryDirectory(prefix='fingerprint-resource-test.',dir=Path.home()/'.cache')
        self.work=Path(self.temp.name); self.patches=[]; self.calls=[]; self.events=[]; self.fail={}
        (self.work/'business-artifacts').mkdir(mode=0o700)
        self.plan={'source_sha':h.SOURCE_SHA,'transition_id':h.TRANSITION_ID,'transition_intent_sha256':h.INTENT_SHA,
            'nodes':[{'name':'arch','ssh':'synthetic-owner'}], 'database':{'owner_node':'arch','os_user':'postgres',
            'schema':'lmm_api','socket_directory':'/run/postgresql','port':5432,'peer_role':'postgres','database':'lmm_api','owner_role':'lmm_api'}}
        self.fingerprint=('\n'.join('lmm_api.fixture_'+str(i)+'|0|'+'a'*64 for i in range(167))+'\n').encode()
        self.seal={'business_plan_sha256':h.BUSINESS_SHA,
            'before_sql':self.binding(self.work/'business-artifacts/before.sql',b'BEGIN READ ONLY; before-proof; COMMIT;'),
            'after_sql':self.binding(self.work/'business-artifacts/after.sql',b'BEGIN READ ONLY; after-proof; COMMIT;'),
            'production_sql':self.binding(self.work/'business-artifacts/financial.sql',b'FORBIDDEN_FINANCIAL;'),
            'original_fingerprint':{'before_result':self.binding(self.work/'000732-production-original-before.tsv',self.fingerprint),
                'production_before_sql':self.binding(self.work/'business-artifacts/fp-before.sql',b"BEGIN READ ONLY; SET LOCAL statement_timeout='60s'; fingerprint-before; COMMIT;"),
                'production_after_sql':self.binding(self.work/'business-artifacts/fp-after.sql',b"BEGIN READ ONLY; SET LOCAL statement_timeout='60s'; fingerprint-after; COMMIT;")}}
        seal=self.binding(self.work/'business-artifacts/business-seal.json',original.encode(self.seal))
        self.plan_path=self.work/'financial-plan.bound.json'; plan=self.binding(self.plan_path,original.encode(self.plan))
        intent={'format':'lmm-credit-financial-dispatch-v1','transition_id':h.TRANSITION_ID,'transition_intent_sha256':h.INTENT_SHA,
            'business_plan_sha256':h.BUSINESS_SHA,'business_seal_sha256':seal['sha256'],'production_sql_sha256':self.seal['production_sql']['sha256']}
        intent=self.binding(self.work/'financial-dispatch-intent.json',original.encode(intent))
        self.state={'format':original.FORMAT,'phase':'AMBIGUOUS','sequence':32,'plan_sha256':plan['sha256'],
            'business_plan_sha256':h.BUSINESS_SHA,'business_seal':seal,'dispatch_intent_sha256':intent['sha256'],
            'transition_id':h.TRANSITION_ID,'transition_intent_sha256':h.INTENT_SHA,'guardian_generations':{'fixture':'retained'},'recovery':'original failed fingerprint'}
        logs={}
        for name in inspect.LOGS:
            raw=b'committed original financial evidence' if name.startswith('000733') else b'' if name.startswith('000734') else b'original connectionclosed evidence'
            self.write(self.work/name,raw); logs[name]=(len(raw),original.digest(raw))
        self.write(self.work/'state.json',original.encode(self.state))
        for module,name,value in [(h,'WORK_PATH',self.work),(h,'PLAN_PATH',self.plan_path),(h,'PLAN_SHA',plan['sha256']),
                (h,'SEAL_SHA',seal['sha256']),(inspect,'STATE_SHA',original.digest(original.encode(self.state))),
                (inspect,'INTENT_SHA',intent['sha256']),(inspect,'LOGS',logs)]: self.patch(module,name,value)
        self.controller=inspect.controller_type(h,original)(self.plan,h.PLAN_SHA,self.work,executor=self.executor)
        self.controller.frozen_gates=self.freeze
        persist=self.controller.persist
        def checkpoint(phase,**values): self.events.append('persist:'+phase); persist(phase,**values)
        self.controller.persist=checkpoint

    def patch(self,module,name,value):
        p=patch.object(module,name,value); p.start(); self.patches.append(p)
    def close(self):
        for p in reversed(self.patches): p.stop()
        self.temp.cleanup()
    def write(self,path,raw): path.write_bytes(raw); path.chmod(0o600)
    def binding(self,path,raw): self.write(path,raw); return {'path':str(path),'sha256':original.digest(raw)}
    def snapshot(self): return {str(p.relative_to(self.work)):p.read_bytes() for p in self.work.rglob('*') if p.is_file()}
    def rebind_state(self):
        self.write(self.work/'state.json',original.encode(self.state)); self.controller.state=copy.deepcopy(self.state)
        self.patch(inspect,'STATE_SHA',original.digest(original.encode(self.state)))
    def freeze(self):
        self.events.append('freeze')
        if self.fail.get('freeze'): raise original.GateFailed('synthetic frozen guard refusal')
    def executor(self,argv,**options):
        self.calls.append((copy.deepcopy(argv),copy.deepcopy(options)))
        body=options.get('input') or b''
        if b'FORBIDDEN_FINANCIAL' in body: raise AssertionError('financial SQL must never run again')
        if argv[:3]!=['/usr/bin/ssh','-T','synthetic-owner']: raise AssertionError('only the synthetic owner is accepted')
        remote=shlex.split(argv[3]); event=next((name for name in ('fingerprint-after','fingerprint-before','after-proof','before-proof') if (name+';').encode() in body),'delegate')
        self.events.append(event)
        base=inspect.expected_operation(self.plan)['argv']
        expected=list(base)
        if event.startswith('fingerprint-'): expected[5]+=' '+inspect.GUCS
        if event!='delegate' and remote!=expected: raise AssertionError('target, role or GUC scope changed')
        if options['timeout']!=600 or not options['capture_output'] or set(options)!={'input','capture_output','timeout'}: raise AssertionError('execution kwargs changed')
        if self.fail.get(event)=='timeout': raise subprocess.TimeoutExpired(argv,600,output=b'partial stdout',stderr=b'partial stderr')
        if self.fail.get(event)=='nonzero': return subprocess.CompletedProcess(argv,7,b'raw stdout',b'raw stderr')
        output=self.fingerprint if event.startswith('fingerprint-') else b''
        if self.fail.get(event)=='mismatch': output=b'different fingerprint'
        return subprocess.CompletedProcess(argv,0,output,b'')


class ResourceInspectionTests(unittest.TestCase):
    def setUp(self):
        self.real_process=patch('subprocess.run',side_effect=AssertionError('real process forbidden')); self.real_process.start(); self.addCleanup(self.real_process.stop)
        self.f=Fixture(); self.addCleanup(self.f.close)
    def execute(self): return inspect.inspect(h,original,self.f.controller,execute=True,confirm='api.lmm.best')

    def test_default_precheck_performs_no_sql_process_or_write(self):
        before=self.f.snapshot(); proof=inspect.inspect(h,original,self.f.controller)
        self.assertEqual(proof,{'ok':True,'execute':False,'phase':'AMBIGUOUS','sequence':32,'financial_sql_retry':False})
        self.assertEqual(self.f.snapshot(),before); self.assertEqual(self.f.calls,[])

    def test_success_keeps_original_inspect_body_logs_and_only_original_checkpoint(self):
        before=self.f.snapshot(); self.assertEqual(self.execute(),{'ok':True,'phase':'APPLIED','sequence':33})
        self.assertEqual(self.f.events,['freeze','after-proof','fingerprint-after','persist:APPLIED'])
        state=original.decode((self.f.work/'state.json').read_bytes()); self.assertTrue(state['inspected_after_ambiguous_dispatch'])
        self.assertNotIn('production_before_fingerprint',state)
        allowed={'phase','sequence','updated_at','production_after_fingerprint','inspected_after_ambiguous_dispatch'}
        self.assertEqual({k:v for k,v in state.items() if k not in allowed},{k:v for k,v in self.f.state.items() if k not in allowed})
        self.assertEqual(original.read_bound(state['production_after_fingerprint']),self.f.fingerprint)
        audit=self.f.work/'fingerprint-resource-inspection'; intent=original.decode((audit/'intent.json').read_bytes())
        self.assertEqual(intent['resource_gucs'],inspect.GUCS); self.assertFalse(intent['financial_sql_retry'])
        self.assertEqual((audit/'old-state.json').read_bytes(),before['state.json'])
        for name in inspect.LOGS: self.assertEqual((self.f.work/name).read_bytes(),before[name]); self.assertEqual((audit/name).read_bytes(),before[name])
        self.assertEqual(self.f.calls[0][1]['input'],b'SET ROLE "lmm_api";\n'+original.read_bound(self.f.seal['after_sql']))
        self.assertEqual(self.f.calls[1][1]['input'],b'SET ROLE "lmm_api";\n'+original.read_bound(self.f.seal['original_fingerprint']['production_after_sql']))

    def test_exact_operation_shape_type_target_role_body_node_and_kwargs_refuse_before_dispatch(self):
        base=inspect.expected_operation(self.f.plan); owner=self.f.plan['nodes'][0]
        body=b'SET ROLE "lmm_api";\n'+original.read_bound(self.f.seal['original_fingerprint']['production_after_sql'])
        cases=[({'extra':True},{}),({'timeout_seconds':600.0},{}),({'timeout_seconds':True},{}),({'timeout_seconds':601},{}),
            ({'argv':tuple(base['argv'])},{}),({'argv':base['argv']+['extra']},{}),({'argv':base['argv'][:5]+[base['argv'][5]+' '+inspect.GUCS]+base['argv'][6:]},{}),
            ({},{'node':dict(owner)}),({},{'node':None}),({},{'body':body+b'changed'}),({},{'body':body.decode()}),
            ({},{'body':body.replace(b'lmm_api',b'wrong_role',1)}),({},{'variables':{}}),({},{'cwd':str(self.f.work)}),({},{'env':{}})]
        for change,kwargs in cases:
            with self.subTest(change=change,kwargs=kwargs),self.assertRaises(original.GateFailed):
                self.f.controller.execute('production-original-after',dict(base,**change),**dict({'node':owner,'body':body},**kwargs))
        self.assertEqual(self.f.calls,[]); self.assertEqual(self.f.controller.operation_sequence,735)

    def test_selected_before_has_same_narrow_profile_and_copies_original_operation(self):
        operation=inspect.expected_operation(self.f.plan); saved=copy.deepcopy(operation)
        body=b'SET ROLE "lmm_api";\n'+original.read_bound(self.f.seal['original_fingerprint']['production_before_sql'])
        self.f.controller.execute('production-original-before',operation,node=self.f.plan['nodes'][0],body=body)
        self.assertEqual(operation,saved); self.assertEqual(self.f.calls[0][1]['input'],body)

    def test_every_other_label_delegates_unchanged_including_after_drain_financial(self):
        for label in ('inspect-after','inspect-before','database-client-drain','production-financial-dispatch','production-after','other'):
            operation=inspect.expected_operation(self.f.plan); saved=copy.deepcopy(operation)
            self.f.controller.execute(label,operation,node=self.f.plan['nodes'][0],body=b'opaque delegate body')
            self.assertEqual(shlex.split(self.f.calls[-1][0][3]),saved['argv']); self.assertEqual(operation,saved)

    def test_after_failure_original_before_classification_is_not_false_success(self):
        self.f.fail['after-proof']='nonzero'
        with self.assertRaises(original.GateFailed): self.execute()
        self.assertEqual(self.f.events,['freeze','after-proof','before-proof','fingerprint-before','persist:NOT_APPLIED'])
        self.assertEqual(self.f.controller.state['phase'],'NOT_APPLIED'); self.assertTrue((self.f.work/'fingerprint-resource-inspection/failure.json').is_file())
        count=len(self.f.calls)
        with self.assertRaises((original.GateFailed,h.CompletionFailed)): self.execute()
        self.assertEqual(len(self.f.calls),count)

    def test_both_readonly_paths_failed_preserve_original_ambiguous_checkpoint(self):
        self.f.fail={'after-proof':'nonzero','before-proof':'nonzero'}
        with self.assertRaises(original.GateFailed): self.execute()
        self.assertEqual(self.f.controller.state['phase'],'AMBIGUOUS'); self.assertEqual(self.f.controller.state['sequence'],33)
        self.assertEqual(self.f.events,['freeze','after-proof','before-proof','persist:AMBIGUOUS'])
        self.assertFalse((self.f.work/'fingerprint-resource-inspection/complete.json').exists())

    def test_fingerprint_nonzero_timeout_and_digest_mismatch_use_original_fallback_and_preserve_raw(self):
        for failure in ('nonzero','timeout','mismatch'):
            f=Fixture()
            try:
                f.fail={'fingerprint-after':failure,'before-proof':'nonzero'}
                with self.subTest(failure=failure),self.assertRaises(original.GateFailed): inspect.inspect(h,original,f.controller,execute=True,confirm='api.lmm.best')
                raw=(f.work/'000737-production-original-after.log').read_bytes()
                self.assertEqual(raw,{'nonzero':b'raw stdoutraw stderr','timeout':b'partial stdoutpartial stderr','mismatch':b'different fingerprint'}[failure])
                self.assertEqual(f.controller.state['phase'],'AMBIGUOUS'); self.assertNotIn('production_after_fingerprint',f.controller.state)
            finally: f.close()

    def test_frozen_gate_refusal_does_not_dispatch_or_handwrite_checkpoint(self):
        old=(self.f.work/'state.json').read_bytes(); self.f.fail['freeze']=True
        with self.assertRaises(original.GateFailed): self.execute()
        self.assertEqual(self.f.calls,[]); self.assertEqual((self.f.work/'state.json').read_bytes(),old)
        self.assertTrue((self.f.work/'fingerprint-resource-inspection/intent.json').is_file())

    def test_state_plan_seal_sql_intent_and_each_old_log_cas_drift_zero_write(self):
        files=[self.f.work/'state.json',self.f.plan_path,self.f.work/'financial-dispatch-intent.json',Path(self.f.state['business_seal']['path'])]
        files += [Path(value['path']) for value in self.f.seal['original_fingerprint'].values()]+[self.f.work/name for name in inspect.LOGS]
        for file in files:
            raw=file.read_bytes(); file.write_bytes(raw+b'changed'); before=self.f.snapshot()
            with self.subTest(file=file.name),self.assertRaises((original.GateFailed,h.CompletionFailed)): self.execute()
            self.assertEqual(self.f.snapshot(),before); self.assertEqual(self.f.calls,[]); file.write_bytes(raw)

    def test_fixed_phase_sequence_source_plan_object_maxoperation_and_confirmation(self):
        for field,value in [('phase','DISPATCH_INTENT'),('sequence',33),('sequence',32.0),('sequence',True),('dispatch_intent_sha256','0'*64)]:
            saved=copy.deepcopy(self.f.state); self.f.state[field]=value; self.f.rebind_state()
            with self.subTest(field=field,value=value),self.assertRaises(original.GateFailed): self.execute()
            self.f.state=saved; self.f.rebind_state()
        self.f.controller.operation_sequence=736
        with self.assertRaises(original.GateFailed): self.execute()
        self.f.controller.operation_sequence=735; self.f.controller.plan=copy.deepcopy(self.f.plan); self.f.controller.plan['source_sha']='0'*40
        with self.assertRaises(original.GateFailed): self.execute()
        self.f.controller.plan=self.f.plan
        with self.assertRaises(original.GateFailed): inspect.inspect(h,original,self.f.controller,execute=True,confirm='wrong')
        self.assertEqual(self.f.calls,[]); self.assertFalse((self.f.work/'fingerprint-resource-inspection').exists())

    def test_plan_changes_between_precheck_and_fingerprint_cannot_change_target(self):
        def freeze(): self.f.plan['database']['database']='changed_database'
        self.f.controller.frozen_gates=freeze
        with self.assertRaises(original.GateFailed): self.execute()
        self.assertEqual([event for event in self.f.events if event.startswith('fingerprint')],[])

    def test_attempt_exists_even_partial_or_symlink_refuses_no_write(self):
        audit=self.f.work/'fingerprint-resource-inspection'; audit.mkdir(mode=0o700); before=self.f.snapshot()
        with self.assertRaises(original.GateFailed): self.execute()
        self.assertEqual(self.f.snapshot(),before); self.assertEqual(self.f.calls,[])
        audit.rmdir(); audit.symlink_to(self.f.work, target_is_directory=True)
        with self.assertRaises(original.GateFailed): self.execute()

    def test_source_hash_type_link_and_import_seals_refuse(self):
        file=self.f.work/'prior.py'; raw=inspect.PRIOR_PATH.read_bytes(); self.f.write(file,raw+b'changed')
        with self.assertRaises(RuntimeError): inspect.load_prior(file)
        self.f.write(file,raw); linked=self.f.work/'symlink.py'; linked.symlink_to(file)
        with self.assertRaises(RuntimeError): inspect.load_prior(linked)
        hard=self.f.work/'hardlink.py'; os.link(file,hard)
        with self.assertRaises(RuntimeError): inspect.load_prior(file)
        hard.unlink(); fifo=self.f.work/'fifo.py'; os.mkfifo(fifo,0o600)
        with self.assertRaises(RuntimeError): inspect.load_prior(fifo)
        with self.assertRaises(RuntimeError): inspect.load_prior(self.f.work)
        for module in (h,original):
            with patch.object(module,'__sealed_sha256__','0'*64),self.assertRaises(original.GateFailed): inspect.controller_type(h,original)

    def test_forbidden_actions_are_rejected_before_sealed_import(self):
        with patch.object(inspect,'load_prior',side_effect=AssertionError('must not import')):
            for args in (['apply'],['rehearse'],['serve'],['release'],['--apply']):
                with self.subTest(args=args),self.assertRaises(SystemExit): inspect.main(args)


if __name__=='__main__': unittest.main()
