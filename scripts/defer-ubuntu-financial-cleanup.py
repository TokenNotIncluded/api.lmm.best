#!/usr/bin/python3
"""Retain all candidates when the one known legacy workspace is unverified."""
import hashlib
import json
import os
from pathlib import Path
import socket
import stat
import sys
import types

ROOT=Path('/var/lib/lmm-credit-transition/credit-financial-20261006')
PRIOR=ROOT/'deploy-systemd-cleanup.py'
PRIOR_SHA='7dacfdce908f5dd17adfc66ff11f9e5352517066434e9a53341f5e45d689622f'
UNKNOWN='currency-price-sync-ubuntu-20261005-005'


def defer_plan(plan):
    if plan['blocked_by']!=[UNKNOWN]: raise RuntimeError('exact-known-unverified-owner-required; no deletions authorized')
    result=json.loads(json.dumps(plan))
    candidates=result['candidates']
    result['protected'].extend({'release':c['release'],'reason':'deferred: retained unverified owner may reference this complete payload','deferred_candidate_inventory':c} for c in candidates)
    result.update(candidates=[],blocked_by=[],original_blocked_by=[UNKNOWN],cleanup_deferred=True,
                  deferred_unverified_owner=UNKNOWN,deferred_candidate_payload_bytes=sum(c['bytes'] for c in candidates),
                  actual_candidate_deletions_authorized=0)
    return result


def main(argv=None):
    args=list(sys.argv[1:] if argv is None else argv)
    if len(args)<3 or args[0]!='--self-sha256' or hashlib.sha256(Path(__file__).read_bytes()).hexdigest()!=args[1]: raise RuntimeError('extra-cleanup-artifact-seal-required')
    args=args[2:]
    if not args or args[0]!='cleanup' or os.geteuid()!=0 or socket.gethostname()!='dmit-ubuntu': raise RuntimeError('fixed-Ubuntu-cleanup-only')
    fixed={'--release':'credit-financial-20261006-ubuntu-post','--superseded-by':'credit-financial-20261006-ubuntu-post','--retain-rollback':'credit-financial-20261006-ubuntu-prebridge','--maintenance-handoff':str(ROOT/'post-stopped-handoff.json'),'--maintenance-handoff-sha256':'da07e2249e1c1b86d6d095c3820fbe3b247dbc8c00a73112c270d3a6ec1864bb','--confirm':'api.lmm.best'}
    for flag,value in fixed.items():
        if args.count(flag)!=1 or args[args.index(flag)+1]!=value: raise RuntimeError('fixed-cleanup-owner-binding-required')
    before=PRIOR.lstat()
    if not stat.S_ISREG(before.st_mode) or before.st_nlink!=1 or before.st_uid!=0 or before.st_mode&0o022: raise RuntimeError('sealed-prior-cleanup-file-required')
    fd=os.open(PRIOR,os.O_RDONLY|os.O_NOFOLLOW|os.O_CLOEXEC)
    with os.fdopen(fd,'rb') as f: raw=f.read(1024*1024+1)
    if hashlib.sha256(raw).hexdigest()!=PRIOR_SHA: raise RuntimeError('sealed-prior-cleanup-hash-required')
    prior=types.ModuleType('_sealed_cleanup_only'); prior.__file__=str(PRIOR); exec(compile(raw,str(PRIOR),'exec'),prior.__dict__)
    owner=prior.load_cleanup_owner(); history=owner.cleanup_history
    owner.cleanup_history=lambda args,now,transferred=None: defer_plan(history(args,now,transferred))
    return owner.main(args)


if __name__=='__main__':
    sys.dont_write_bytecode=True
    try: sys.exit(main())
    except Exception as e: sys.exit('Deferred normal cleanup refused: '+type(e).__name__)
