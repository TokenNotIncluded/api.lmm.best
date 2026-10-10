#!/usr/bin/python3
"""One episode: original Ubuntu release waits for genuine Arch confirmation."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import socket
import stat
import sys
import time
import types

ROOT=Path('/var/lib/lmm-credit-transition/credit-financial-20261006')
ORIGINAL=ROOT/'deploy-systemd.py'
ORIGINAL_SHA='72bdf42f1ef5f3de998166e08efa94faddf5a5c0cf3d396633c464b1ca98f951'
GUARDIAN=ROOT/'maintenance-deploy-guardian.py'
GUARDIAN_SHA='f27fdb84aca03a1509d91817c198be8e62903fe0e84923f28efeffd312ef23b1'
WORK=Path('/var/lib/lmm-api-deploy-systemd/credit-financial-20261006-ubuntu-post')
HANDOFF=ROOT/'post-stopped-handoff.json'
HANDOFF_SHA='da07e2249e1c1b86d6d095c3820fbe3b247dbc8c00a73112c270d3a6ec1864bb'
CONFIRMATION=ROOT/'all-nodes-confirmed.json'
CONFIRMATION_SHA='d3ab34fcb23a7d3fd458e7796ad64d215731fe39bf7e5ad39cf49feb54baffc1'
PROVIDER_SHA='19dd6ff9d514cfea8338ba0c37b9a62d8ed1e10ddf77c7ba0f41d69a4eacdec9'
TRANSITION='credit-financial-20261006'
INTENT_SHA='60720d3b217167231ec10df4b95aab704366a05884c8b53e27dced40fb92bdcd'
BATON=ROOT/'arch-confirmed-release-baton.json'
WAIT_EVIDENCE=ROOT/'ubuntu-origin-open-awaiting-arch.json'
DEADLINE_SECONDS=180


def read_private(path, expected=None, mode=None):
    path=Path(path)
    for parent in path.parents:
        s=parent.lstat()
        if not stat.S_ISDIR(s.st_mode) or s.st_uid!=0 or s.st_mode&0o022: raise RuntimeError('unsafe-root-parent')
    before=path.lstat()
    if not stat.S_ISREG(before.st_mode) or before.st_uid!=0 or before.st_nlink!=1 or before.st_mode&0o022 or (mode is not None and stat.S_IMODE(before.st_mode)!=mode): raise RuntimeError('unsafe-root-file')
    with os.fdopen(os.open(path,os.O_RDONLY|os.O_NOFOLLOW|os.O_CLOEXEC),'rb') as f:
        opened=os.fstat(f.fileno()); raw=f.read(1024*1024+1); after=os.fstat(f.fileno())
    identity=lambda s:(s.st_dev,s.st_ino,s.st_size,s.st_mode,s.st_nlink,s.st_mtime_ns,s.st_ctime_ns)
    if len(raw)>1024*1024 or identity(before)!=identity(opened) or identity(opened)!=identity(after) or identity(after)!=identity(path.lstat()) or (expected is not None and hashlib.sha256(raw).hexdigest()!=expected): raise RuntimeError('root-seal-changed')
    return raw


def write_once(path, value):
    data=(json.dumps(value,sort_keys=True,indent=2)+'\n').encode()
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW|os.O_CLOEXEC,0o600)
    with os.fdopen(fd,'wb') as f: f.write(data); f.flush(); os.fsync(f.fileno())
    fd=os.open(path.parent,os.O_RDONLY|os.O_DIRECTORY); os.fsync(fd); os.close(fd)


def check_arch_baton():
    try: raw=read_private(BATON,mode=0o400)
    except FileNotFoundError: return False
    value=json.loads(raw)
    expected={'format':2,'deployment_id':'credit-financial-20261006-arch-post','phase':'CONFIRMED','version':'0.2.82','maintenance_stage':'post','handoff_sha256':'e4d8c7fdefd9a99991eb41d77b65352de0730030821ad3594d880b0eb41e8b96','provider_sha256':PROVIDER_SHA,'transition_id':TRANSITION,'transition_intent_sha256':INTENT_SHA,'maintenance_admission_reopened':True}
    if any(type(value.get(k)) is not type(v) or value.get(k)!=v for k,v in expected.items()): raise RuntimeError('Arch-authoritative-confirmation-baton-mismatch')
    return True


def wait_after_reopen(original, work,state,maintenance, *, clock=None,sleep=None,check_baton=None,write_evidence=None):
    clock=clock or time.monotonic; sleep=sleep or time.sleep; check_baton=check_baton or check_arch_baton
    write_evidence=write_evidence or (lambda: write_once(WAIT_EVIDENCE,{'format':'lmm-ubuntu-origin-open-awaiting-arch-v1','phase':'OPEN_PENDING','owner_phase':'MAINTENANCE_CONFIRMED','wrapper_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'original_sha256':ORIGINAL_SHA,'guardian_sha256':GUARDIAN_SHA,'source_sha':'7af4bf9e56055a9a283b6545433e271a84b60026','controller_state_sha256':'f96ec00add61fe9043a432e523ebb60454b264b0367715a708664c6cd235394f','handoff_sha256':HANDOFF_SHA,'global_confirmation_sha256':CONFIRMATION_SHA,'state_before_sha256':original.wait_state_sha256,'deadline_seconds':DEADLINE_SECONDS,'baton_path':str(BATON)}))
    end=clock()+DEADLINE_SECONDS
    try:
        original.original_reopen(work,state,maintenance)
        write_evidence()
        while True:
            remaining=end-clock()
            if remaining<=0: raise TimeoutError('Arch-confirmation-baton-deadline')
            if check_baton(): return
            sleep(min(0.2,remaining))
    except BaseException:
        try: original.close_maintenance_admission(work,state,maintenance)
        finally:
            state['phase']='ROLLBACK_REQUIRED'; original.save(work,state)
        raise


def main(argv=None):
    sys.dont_write_bytecode=True
    parser=argparse.ArgumentParser(description=__doc__); parser.add_argument('action',choices=['maintenance-release']); parser.add_argument('--confirm',choices=['api.lmm.best'],required=True); args=parser.parse_args(argv)
    if os.geteuid()!=0 or socket.gethostname()!='dmit-ubuntu': raise RuntimeError('exact-Ubuntu-root-required')
    if BATON.exists() or BATON.is_symlink() or WAIT_EVIDENCE.exists() or WAIT_EVIDENCE.is_symlink(): raise RuntimeError('episode-evidence-already-exists')
    raw=read_private(ORIGINAL,ORIGINAL_SHA); read_private(GUARDIAN,GUARDIAN_SHA)
    read_private(HANDOFF,HANDOFF_SHA); read_private(CONFIRMATION,CONFIRMATION_SHA)
    old_state=read_private(WORK/'state.json'); value=json.loads(old_state)
    expected={'release':WORK.name,'phase':'MAINTENANCE_CONFIRMED','version':'0.2.82','sha256':PROVIDER_SHA,'maintenance_confirmation':True,'maintenance_admission_closed':True,'maintenance_handoff':{'path':str(HANDOFF),'sha256':HANDOFF_SHA}}
    if any(type(value.get(k)) is not type(v) or value.get(k)!=v for k,v in expected.items()): raise RuntimeError('exact-confirmed-Ubuntu-owner-required')
    original=types.ModuleType('_sealed_original_ubuntu_owner'); original.__file__=str(ORIGINAL)
    exec(compile(raw,str(ORIGINAL),'exec'),original.__dict__)
    read_private(ORIGINAL,ORIGINAL_SHA); read_private(GUARDIAN,GUARDIAN_SHA)
    original.original_reopen=original.reopen_maintenance_admission; original.wait_state_sha256=hashlib.sha256(old_state).hexdigest()
    def waiting_reopen(work,state,maintenance):
        if work!=WORK or read_private(WORK/'state.json')!=old_state or state['phase']!='MAINTENANCE_CONFIRMED' or maintenance['transition_id']!=TRANSITION or maintenance['transition_intent_sha256']!=INTENT_SHA or maintenance['provider_sha256']!=PROVIDER_SHA: raise RuntimeError('owner-state-before-reopen-changed')
        return wait_after_reopen(original,work,state,maintenance)
    original.reopen_maintenance_admission=waiting_reopen
    sys.argv=[str(ORIGINAL),'maintenance-release','--release',WORK.name,'--maintenance-handoff',str(HANDOFF),'--maintenance-handoff-sha256',HANDOFF_SHA,'--global-confirmation',str(CONFIRMATION),'--global-confirmation-sha256',CONFIRMATION_SHA,'--json','--confirm',args.confirm]
    return original.main()


if __name__=='__main__':
    try: sys.exit(main())
    except Exception as error: sys.exit('Ubuntu original release wait refused: '+type(error).__name__)
