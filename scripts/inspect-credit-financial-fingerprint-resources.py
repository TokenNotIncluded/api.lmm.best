#!/usr/bin/env python3
"""Inspect one committed ambiguous episode using bounded fingerprint sessions."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import stat
import sys
import types

PRIOR_PATH = Path('/home/lightjunction/.cache/credit-financial-maintenance-20261006/complete-credit-clone-rehearsal.private.py')
PRIOR_SHA = 'bdcbdc96eaecb4ff31e77241b652f047ac97664249cadfd692c1885feea6862d'
STATE_SHA = '6e2a7e5d6b8af547299fe86da5098e16b9bcc949f7d6a1f9dae23931675eb705'
INTENT_SHA = 'a8ddd19e41298b483aa8040bc5da2f49d215c5efb29b05ad4fc3968e71e1033f'
GUCS = '-c jit=off -c max_parallel_workers_per_gather=0 -c work_mem=4MB -c hash_mem_multiplier=1'
LOGS = {'000733-production-financial-dispatch.log':(477,'c7a7b26bfe7826af07079e26959086fbd2663cae52243bc7f02b5e374da6e3f1'), '000734-production-after.log':(0,'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'), '000735-production-original-after.log':(167,'2d11cfcbb5ba556df2789ddadb0049a715554e238f50ff3e61e7fc624d3390cb')}
LABELS = {'production-original-before':'production_before_sql','production-original-after':'production_after_sql'}


def load_prior(path=PRIOR_PATH):
    path=Path(path); before=path.lstat()
    if path.resolve()!=path or not stat.S_ISREG(before.st_mode) or before.st_nlink!=1 or before.st_mode&0o022: raise RuntimeError('prior-helper-file-unsafe')
    with os.fdopen(os.open(path,os.O_RDONLY|os.O_NOFOLLOW|os.O_CLOEXEC),'rb') as file:
        opened=os.fstat(file.fileno()); raw=file.read(); after=os.fstat(file.fileno())
    identity=lambda i:(i.st_dev,i.st_ino,i.st_mode,i.st_nlink,i.st_size,i.st_mtime_ns,i.st_ctime_ns)
    if identity(before)!=identity(opened) or identity(opened)!=identity(after) or identity(after)!=identity(path.lstat()) or hashlib.sha256(raw).hexdigest()!=PRIOR_SHA: raise RuntimeError('prior-helper-seal-changed')
    helper=types.ModuleType('_sealed_resource_inspection_helper'); helper.__file__=str(path)
    exec(compile(raw,str(path),'exec'),helper.__dict__); helper.__sealed_sha256__=PRIOR_SHA
    return helper


def expected_operation(plan):
    db=plan['database']
    return {'argv':['/usr/bin/runuser','-u',db['os_user'],'--','/usr/bin/env','PGOPTIONS=-c search_path='+db['schema']+',pg_catalog','/usr/bin/psql','-XqAt','--no-password','-v','ON_ERROR_STOP=1','-h',db['socket_directory'],'-p',str(db['port']),'-U',db['peer_role'],'-d',db['database']], 'timeout_seconds':600}


def controller_type(h, original):
    original.require(getattr(h,'__sealed_sha256__',None)==PRIOR_SHA and getattr(original,'__sealed_sha256__',None)==h.ORIGINAL_SHA,'sealed-inspection-source-required')
    class ResourceInspectionController(original.Controller):
        def execute(self,label,operation,*,node=None,body=None,variables=None,cwd=None,env=None):
            if label not in LABELS: return super().execute(label,operation,node=node,body=body,variables=variables,cwd=cwd,env=env)
            original.require(original.decode(h.bound(h.PLAN_PATH,h.PLAN_SHA))==self.plan,'sealed-fingerprint-plan-changed')
            owner=next(n for n in self.plan['nodes'] if n['name']==self.plan['database']['owner_node'])
            expected=expected_operation(self.plan); seal=self.business_seal()
            prefix=('SET ROLE "'+self.plan['database']['owner_role']+'";\n').encode()
            original.require(type(operation) is dict and set(operation)=={'argv','timeout_seconds'} and type(operation['argv']) is list and type(operation['timeout_seconds']) is int and operation==expected and node is owner and type(body) is bytes and body==prefix+original.read_bound(seal['original_fingerprint'][LABELS[label]]) and variables is None and cwd is None and env is None,'exact-original-fingerprint-execution-required')
            h.bound(self.state_path,STATE_SHA)
            changed={'argv':list(operation['argv']),'timeout_seconds':operation['timeout_seconds']}; changed['argv'][5]+=' '+GUCS
            return super().execute(label,changed,node=node,body=body,variables=variables,cwd=cwd,env=env)
    return ResourceInspectionController


def precheck(h, original, controller):
    require=original.require; work=h.WORK_PATH
    require(controller.work==work and controller.plan_hash==h.PLAN_SHA and original.decode(h.bound(h.PLAN_PATH,h.PLAN_SHA))==controller.plan and controller.plan['source_sha']==h.SOURCE_SHA,'fixed-inspection-plan-work-source')
    raw=h.bound(controller.state_path,STATE_SHA); state=original.decode(raw)
    require(state==controller.state and state['phase']=='AMBIGUOUS' and type(state['sequence']) is int and state['sequence']==32 and state['plan_sha256']==h.PLAN_SHA and state['dispatch_intent_sha256']==INTENT_SHA and controller.operation_sequence==735,'fixed-ambiguous-incident-state')
    require(state['business_seal']=={'path':str(work/'business-artifacts/business-seal.json'),'sha256':h.SEAL_SHA} and state['business_plan_sha256']==h.BUSINESS_SHA,'fixed-business-seal')
    seal=original.decode(h.bound(state['business_seal']['path'],h.SEAL_SHA)); intent=h.bound(work/'financial-dispatch-intent.json',INTENT_SHA)
    stack=[seal]
    while stack:
        item=stack.pop()
        if isinstance(item,dict):
            if set(item)=={'path','sha256'}: h.bound(item['path'],item['sha256'])
            else: stack.extend(item.values())
        elif isinstance(item,list): stack.extend(item)
    for name,(size,sha) in LOGS.items(): require(len(h.bound(work/name,sha))==size,'fixed-dispatch-evidence-size')
    require(not (work/'fingerprint-resource-inspection').exists(),'inspection-attempt-already-exists')
    return raw,intent,seal


def inspect(h, original, controller, *, execute=False, confirm=None):
    old_state,intent,seal=precheck(h,original,controller)
    if not execute: return {'ok':True,'execute':False,'phase':'AMBIGUOUS','sequence':32,'financial_sql_retry':False}
    original.require(confirm=='api.lmm.best','explicit-incident-inspection-confirmation')
    audit=h.WORK_PATH/'fingerprint-resource-inspection'; audit.mkdir(mode=0o700)
    original.write_once(audit/'old-state.json',old_state); original.write_once(audit/'financial-dispatch-intent.json',intent)
    for name,(_,sha) in LOGS.items(): original.write_once(audit/name,h.bound(h.WORK_PATH/name,sha))
    original.write_once(audit/'intent.json',original.encode({'format':'lmm-credit-financial-fingerprint-resource-inspection-v1','wrapper_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'original_sha256':h.ORIGINAL_SHA,'source_sha':h.SOURCE_SHA,'work':str(h.WORK_PATH),'plan_sha256':h.PLAN_SHA,'state_sha256':STATE_SHA,'dispatch_intent_sha256':INTENT_SHA,'business_seal_sha256':h.SEAL_SHA,'fingerprint_sql':{k:seal['original_fingerprint'][v] for k,v in LABELS.items()},'resource_gucs':GUCS,'logs':LOGS,'financial_sql_retry':False}))
    try:
        controller.inspect()
        original.require(controller.state['phase']=='APPLIED' and controller.state.get('inspected_after_ambiguous_dispatch') is True,'original-inspection-not-applied')
        for name,(_,sha) in LOGS.items(): h.bound(h.WORK_PATH/name,sha)
        h.bound(h.WORK_PATH/'financial-dispatch-intent.json',INTENT_SHA)
        original.write_once(audit/'complete.json',original.encode({'phase':controller.state['phase'],'sequence':controller.state['sequence'],'production_after_fingerprint':controller.state['production_after_fingerprint']}))
        return {'ok':True,'phase':controller.state['phase'],'sequence':controller.state['sequence']}
    except BaseException as error:
        original.write_once(audit/'failure.json',original.encode({'error_type':type(error).__name__,'phase':controller.state.get('phase'),'replay_forbidden':True})); raise


def main(argv=None):
    sys.dont_write_bytecode=True; parser=argparse.ArgumentParser(description=__doc__); parser.add_argument('action',nargs='?',choices=('inspect',),default='inspect'); parser.add_argument('--execute',action='store_true'); parser.add_argument('--confirm'); args=parser.parse_args(argv)
    try:
        h=load_prior(); original=h.load_original(); plan=original.validate_plan(original.decode(h.bound(h.PLAN_PATH,h.PLAN_SHA))); info=h.WORK_PATH.lstat()
        original.require(stat.S_ISDIR(info.st_mode) and info.st_uid==os.geteuid() and stat.S_IMODE(info.st_mode)==0o700 and h.WORK_PATH.resolve()==h.WORK_PATH,'controller-work-unsafe')
        file=h.WORK_PATH/'controller.lock'; info=file.lstat(); original.require(stat.S_ISREG(info.st_mode) and info.st_nlink==1 and stat.S_IMODE(info.st_mode)==0o600,'controller-lock-unsafe')
        with os.fdopen(os.open(file,os.O_RDWR|os.O_NOFOLLOW|os.O_CLOEXEC),'r+') as lock:
            opened=os.fstat(lock.fileno()); original.require((opened.st_dev,opened.st_ino)==(info.st_dev,info.st_ino),'controller-lock-changed'); fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
            print(json.dumps(inspect(h,original,controller_type(h,original)(plan,h.PLAN_SHA,h.WORK_PATH),execute=args.execute,confirm=args.confirm)))
        return 0
    except (OSError,ValueError,KeyError,RuntimeError) as error:
        print(json.dumps({'ok':False,'error_type':type(error).__name__,'inspect_before_retry':True}),file=sys.stderr); return 1


if __name__=='__main__': sys.exit(main())
