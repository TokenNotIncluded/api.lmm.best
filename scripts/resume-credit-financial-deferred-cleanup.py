#!/usr/bin/python3
"""Resume the sealed controller with one protected, zero-deletion cleanup."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import stat
import sys
import types

ADAPTER=Path('/home/lightjunction/.cache/credit-financial-maintenance-20261006/adapt-credit-financial-owner-status.private.py')
ADAPTER_SHA='945416c563a85a87e7ec9a3833ab2f24e3eb0ac169e056243132ecc7752d7de3'
STATE_SHA='f96ec00add61fe9043a432e523ebb60454b264b0367715a708664c6cd235394f'
LOGS={'000871-arch-owner-cleanup.log':'612cf7434444309c49da358f3709de6aad352311ced749b7e20794d47d51f073','000872-ubuntu-owner-cleanup.log':'fe01a50fb2e9610e616239129e3ea116ac0c46a6fb6eacd01aca625d3787c669'}
REMOTE_HELPER='/var/lib/lmm-credit-transition/credit-financial-20261006/defer-ubuntu-financial-cleanup.py'
REMOTE_SHA='fc66a842e71d76d2e8a91f14ebeff92288d6c6584664b2569638af2c33117ad0'
AUDIT=Path('/home/lightjunction/.cache/cf-20261006/deferred-ubuntu-cleanup-resume')


def read_bound(path,sha):
    p=Path(path); before=p.lstat()
    if not stat.S_ISREG(before.st_mode) or before.st_nlink!=1 or before.st_mode&0o022 or p.resolve()!=p: raise RuntimeError('unsafe-bound-file')
    with os.fdopen(os.open(p,os.O_RDONLY|os.O_NOFOLLOW|os.O_CLOEXEC),'rb') as f: raw=f.read()
    after=p.lstat()
    if (before.st_dev,before.st_ino,before.st_mtime_ns,before.st_ctime_ns,before.st_size)!=(after.st_dev,after.st_ino,after.st_mtime_ns,after.st_ctime_ns,after.st_size) or hashlib.sha256(raw).hexdigest()!=sha: raise RuntimeError('fixed-incident-file-changed')
    return raw


def main(argv=None):
    sys.dont_write_bytecode=True
    p=argparse.ArgumentParser(description=__doc__); p.add_argument('action',choices=['resume']); p.add_argument('--confirm',choices=['api.lmm.best'],required=True); args=p.parse_args(argv)
    raw=read_bound(ADAPTER,ADAPTER_SHA); adapter=types.ModuleType('_sealed_owner_status_adapter'); adapter.__file__=str(ADAPTER); exec(compile(raw,str(ADAPTER),'exec'),adapter.__dict__)
    original=adapter.load_original(); base=adapter.adapter_controller(original,'resume')
    class DeferredCleanupController(base):
        def __init__(self,*args,**kwargs):
            super().__init__(*args,**kwargs)
            old=read_bound(self.state_path,STATE_SHA)
            original.require(self.plan_hash==adapter.PLAN_SHA and self.plan==original.decode(read_bound(adapter.PLAN_PATH,adapter.PLAN_SHA)) and self.plan['source_sha']==adapter.SOURCE_SHA and self.plan['controller']=={'path':str(adapter.ORIGINAL_PATH),'sha256':adapter.ORIGINAL_SHA} and self.state['phase']=='RELEASE_INTENT' and self.state['sequence']==37 and self.operation_sequence==872,'fixed-cleanup-incident-required')
            for name,sha in LOGS.items(): read_bound(self.work/name,sha)
            AUDIT.mkdir(mode=0o700)
            original.write_once(AUDIT/'old-state.json',old)
            original.write_once(AUDIT/'intent.json',original.encode({'format':'lmm-deferred-Ubuntu-cleanup-resume-v1','wrapper_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'adapter_sha256':ADAPTER_SHA,'original_sha256':adapter.ORIGINAL_SHA,'source_sha':adapter.SOURCE_SHA,'plan_sha256':adapter.PLAN_SHA,'old_state_sha256':STATE_SHA,'failure_logs':LOGS,'extra_cleanup_helper':{'path':REMOTE_HELPER,'sha256':REMOTE_SHA},'Arch_previous_actual_deleted_bytes':22004,'Ubuntu_candidate_deletions_authorized':0,'unknown_owner_retained':'currency-price-sync-ubuntu-20261005-005'}))
        def execute(self,label,operation,*,node=None,body=None,variables=None,cwd=None,env=None):
            if label!='ubuntu-owner-cleanup': return super().execute(label,operation,node=node,body=body,variables=variables,cwd=cwd,env=env)
            ubuntu=next(n for n in self.plan['nodes'] if n['name']=='ubuntu')
            expected={'handoff_path':self.state['stopped_handoffs']['ubuntu']['path'],'handoff_sha256':self.state['stopped_handoffs']['ubuntu']['sha256'],'backup_sha256':self.state['backup']['sha256'],'financial_backup_receipt_path':str(Path(ubuntu['receipt_directory'])/'full-financial-backup.receipt.json'),'financial_backup_receipt_sha256':self.state['backup_receipt']['sha256']}
            original.require(node is ubuntu and operation==ubuntu['cleanup'] and variables==expected and body is None and cwd is None and env is None and operation['argv'][1]==str(Path(ubuntu['receipt_directory'])/'deploy-systemd-cleanup.py'),'exact-sealed-Ubuntu-cleanup-call-required')
            changed={'argv':list(operation['argv']),'timeout_seconds':operation['timeout_seconds']}; changed['argv'][1]=REMOTE_HELPER; changed['argv'][2:2]=['--self-sha256',REMOTE_SHA]
            result=super().execute(label,changed,node=node,body=body,variables=variables,cwd=cwd,env=env)
            value=original.decode(result)
            original.require(value.get('format')=='lmm-credit-maintenance-cleanup-v1' and value.get('cleanup_deferred') is True and value.get('original_blocked_by')==['currency-price-sync-ubuntu-20261005-005'] and value.get('candidates')==[] and value.get('deleted_payload_bytes')==0 and value.get('transition_id')==adapter.TRANSITION_ID and value.get('transition_intent_sha256')==adapter.INTENT_SHA,'actual-protected-zero-deletion-cleanup-required')
            original.write_once(AUDIT/'actual-deferred-cleanup.json',result)
            return result
    original.Controller=DeferredCleanupController
    result=original.main(['resume','--plan',str(adapter.PLAN_PATH),'--plan-sha256',adapter.PLAN_SHA,'--work',str(adapter.WORK_PATH),'--confirm',args.confirm])
    if result==0: original.write_once(AUDIT/'complete.json',original.encode({'phase':original.decode((adapter.WORK_PATH/'state.json').read_bytes())['phase'],'original_persist':True,'Ubuntu_deleted_payload_bytes':0}))
    return result


if __name__=='__main__':
    try: sys.exit(main())
    except Exception as error: sys.exit('Deferred original resume refused: '+type(error).__name__)
