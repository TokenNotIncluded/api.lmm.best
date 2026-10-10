#!/usr/bin/env python3
"""Bounded, network-isolated source-slice experiments. No production URLs or CI.

Each child has one-CPU affinity, a 2 GiB virtual-address limit, a 12 CPU-second
limit and a 15-second wall deadline. An RSS guard stops the group above 512 MiB.
These are NOT equivalent to a 1 GiB cgroup. We record that distinction explicitly.
"""
import argparse
import datetime
import hashlib
import json
import os
import pathlib
import resource
import signal
import subprocess
import time

SCENARIOS = {
 'idle':dict(bytes=0,input_bytes=0,rate=1,count=0,slow_millis=0,kind='text/event-stream'),
 'small_sse':dict(bytes=4096,input_bytes=0,rate=60,count=120,slow_millis=0,kind='text/event-stream'),
 'stable_sse':dict(bytes=65536,input_bytes=0,rate=60,count=120,slow_millis=0,kind='text/event-stream'),
 'slow_reader':dict(bytes=65536,input_bytes=0,rate=20,count=40,slow_millis=2,kind='text/event-stream'),
 'large_input':dict(bytes=65536,input_bytes=262144,rate=12,count=24,slow_millis=0,kind='text/event-stream'),
 'large_result':dict(bytes=524288,input_bytes=0,rate=8,count=16,slow_millis=0,kind='text/event-stream'),
 'concurrent_sse':dict(bytes=65536,input_bytes=0,rate=80,count=160,slow_millis=2,kind='text/event-stream'),
 'json_control':dict(bytes=65536,input_bytes=0,rate=60,count=120,slow_millis=0,kind='application/json'),
}

CG = pathlib.Path('/sys/fs/cgroup')

def text(path):
    try: return pathlib.Path(path).read_text().strip()
    except (OSError, PermissionError): return None

def environment():
    return {'recorded_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),
      'kernel':os.uname().release,'cpu_affinity':sorted(os.sched_getaffinity(0)),
      'cgroup_cpu_max':text(CG/'cpu.max'),'cgroup_memory_max':text(CG/'memory.max'),
      'cgroup_swap_max':text(CG/'memory.swap.max'),'cgroup_swap_current':text(CG/'memory.swap.current'),
      'cgroup_events':text(CG/'memory.events'),
      'shared_cgroup_memory_current':text(CG/'memory.current'),
      'shared_cgroup_memory_peak':text(CG/'memory.peak'),
      'shared_cgroup_memory_stat':text(CG/'memory.stat'),
      'shared_cgroup_cpu_stat':text(CG/'cpu.stat'),
      'shared_cgroup_memory_pressure':text(CG/'memory.pressure'),
      'shared_cgroup_note':'These values include unrelated sandbox tasks; not per-test container usage.', 'cgroup_mount': next((l for l in pathlib.Path('/proc/mounts').read_text().splitlines() if ' /sys/fs/cgroup ' in l),None),
      'network_isolation':'new user+network namespace, loopback only',
      'cpu_isolation':'affinity to one logical CPU; no CPU quota isolation',
      'memory_isolation':'RLIMIT_AS 2 GiB + 512 MiB sampled RSS guard; NOT 1 GiB cgroup',
      'oom_protection':'unchanged','swap_and_zram':'not configured or changed',
      'per_task_cgroup_charge_bytes':None,
      'filesystem':{'free_bytes':__import__('shutil').disk_usage('/mnt/data' if pathlib.Path('/mnt/data').exists() else '.').free},
    }

def proc_sample(pid):
    out={'time_ns':time.time_ns(),'pid':pid}
    try:
        lines=pathlib.Path(f'/proc/{pid}/status').read_text().splitlines()
        for line in lines:
            key,_,value=line.partition(':')
            if key in ('VmSize','VmRSS','VmHWM','RssAnon','RssFile','RssShmem','VmSwap','Threads'):
                out[key]=int(value.split()[0])
        try:
            for line in pathlib.Path(f'/proc/{pid}/smaps_rollup').read_text().splitlines():
                key,_,value=line.partition(':')
                if key in ('Pss','Shared_Clean','Shared_Dirty','Private_Clean','Private_Dirty','SwapPss'):
                    out[key]=int(value.split()[0])
        except (OSError,ValueError):
            out['Pss']=None
        stat=pathlib.Path(f'/proc/{pid}/stat').read_text().rsplit(')',1)[1].split()
        out['cpu_ticks']=int(stat[11])+int(stat[12])
        return out
    except (OSError,ValueError,IndexError): return None

def limited():
    os.setsid()
    resource.setrlimit(resource.RLIMIT_AS,(2<<30,2<<30))
    resource.setrlimit(resource.RLIMIT_CPU,(12,12))
    resource.setrlimit(resource.RLIMIT_FSIZE,(128<<20,128<<20))
    resource.setrlimit(resource.RLIMIT_NOFILE,(256,256))
    resource.setrlimit(resource.RLIMIT_CORE,(0,0))

def peak(samples,key):
    values=[x[key] for x in samples if x.get(key) is not None]
    return max(values) if values else None

def run_group(build,out,group,variants,config,settings,observe=True):
    if (out/(group+'.json')).exists(): raise RuntimeError('refusing to overwrite '+group)
    logs=[]; procs=[]; samples=[]; handles=[]; results=[]
    cpu=min(os.sched_getaffinity(0))
    observer_cpu=time.process_time(); began=time.monotonic(); aborted=None
    cfg=dict(config,profile=config.get('profile','off'),scenario=config.get('scenario','stable_sse'))
    if len(variants)>1: cfg['start_ns']=time.time_ns()+2_000_000_000
    try:
        for idx,variant in enumerate(variants):
            path=out/f'{group}-{idx}.log'; handle=path.open('x'); handles.append(handle); logs.append(path)
            c=dict(cfg)
            if c['profile']!='off': c['profile_path']=str((out/f'{group}-{idx}.pprof').resolve())
            env=dict(os.environ, GOTOOLCHAIN='local',GOPROXY='off',GOSUMDB='off',GOWORK='off',GOMAXPROCS='1',GOGC='100',GOMEMLIMIT='96MiB',DP26_RUN='1',DP26_CPU_CEILING='0.75',DP26_CONFIG=json.dumps(c))
            env.update(settings)
            # Only a local, previously built binary is accepted; the child has no
            # external interface, default route, proxy or host network namespace.
            cmd=['unshare','--user','--map-root-user','--net','--','sh','-ec',
                 'ip link set lo up; exec taskset -c "$1" "$2" -test.run=^TestDP26Load$ -test.v -test.timeout=12s','dp26',str(cpu),str((build/(variant+'.test')).resolve())]
            p=subprocess.Popen(cmd,env=env,stdout=handle,stderr=subprocess.STDOUT,preexec_fn=limited)
            procs.append(p)
        while any(p.poll() is None for p in procs):
            if time.monotonic()-began>15: aborted='wall_time_guard'; break
            tick=[]
            if observe:
                for p in procs:
                    sample=proc_sample(p.pid)
                    if sample: samples.append(sample); tick.append(sample)
                if sum(s.get('VmRSS',0) for s in tick)>512*1024:
                    aborted='RSS_guard_512_MiB'; break
            time.sleep(.020)
    finally:
        for p in procs:
            if p.poll() is None:
                try: os.killpg(p.pid,signal.SIGKILL)
                except ProcessLookupError: pass
            p.wait(timeout=3)
        for h in handles: h.close()
    observer_cpu=time.process_time()-observer_cpu
    for idx,(p,path,variant) in enumerate(zip(procs,logs,variants)):
        phases={}; result=None
        for line in path.read_text().splitlines():
            if line.startswith('DP26_PHASE '):
                obj=json.loads(line[11:]); phases[obj['stage']]=obj['time_ns']
            if line.startswith('DP26_RESULT '): result=json.loads(line[12:])
        process_samples=[s for s in samples if s['pid']==p.pid]
        windows={}
        for stage,end in [('idle','warmup'),('warmup','warmed'),('warmed','load'),('load','post_load'),('post_load','end')]:
            selected=[s for s in process_samples if phases.get(stage,2**63)<=s['time_ns']<phases.get(end,-1)]
            windows[stage]={'sample_count':len(selected),**{k:peak(selected,k) for k in ['VmRSS','VmHWM','VmSize','RssAnon','RssFile','RssShmem','Pss','Shared_Clean','Shared_Dirty','VmSwap','Threads']}}
        results.append({'variant':variant,'exit_code':p.returncode,'phases':phases,'memory_windows_kib':windows,'lifetime_peak_kib':{k:peak(process_samples,k) for k in ['VmHWM','VmRSS','Pss','VmSize']},'result':result})
    # Sum PSS at near-simultaneous observations, not sum of individual maxima.
    sums=[]
    if len(variants)>1 and observe:
        for i in range(0,len(samples),len(variants)):
            block=samples[i:i+len(variants)]
            if len({s['pid'] for s in block})==len(variants) and all(s.get('Pss') is not None for s in block): sums.append(sum(s['Pss'] for s in block))
    record={'id':group,'variants':variants,'settings':settings,'observe':observe,'sample_interval_ms':20 if observe else None,
      'aborted':aborted,'observer_cpu_seconds':observer_cpu,'wall_seconds':time.monotonic()-began,
      'overlap_sum_pss_peak_kib':max(sums) if sums else None,'memory_scope':'per-process sampled; not container charge; PSS sums are approximate',
      'source_slice':True,'results':results}
    (out/(group+'.json')).write_text(json.dumps(record,indent=2)+'\n')
    (out/(group+'.samples.jsonl')).write_text(''.join(json.dumps(s)+'\n' for s in samples))
    with (out/'runs.jsonl').open('a') as f:
        compact=json.loads(json.dumps(record))
        for row in compact['results']:
            if row['result']: row['result'].pop('observations',None)
        f.write(json.dumps(compact)+'\n')
    status='OK' if not aborted and all(x['exit_code']==0 and x['result'] is not None for x in results) else 'FAILED'
    print(group,status,[(x['result'] or {}).get('success') for x in results],flush=True)
    if status!='OK': raise RuntimeError('experiment failed; evidence retained: '+group)


def main():
    p=argparse.ArgumentParser()
    p.add_argument('--build',type=pathlib.Path,required=True)
    p.add_argument('--out',type=pathlib.Path,required=True)
    p.add_argument('--suite',choices=['scenario','settings','profiles','observer','overlap'],required=True)
    p.add_argument('--scenario',choices=SCENARIOS,default='stable_sse')
    a=p.parse_args(); a.out.mkdir(parents=True,exist_ok=True)
    if not (a.out/'environment.json').exists(): (a.out/'environment.json').write_text(json.dumps(environment(),indent=2)+'\n')
    if sum(f.stat().st_size for f in a.out.rglob('*') if f.is_file())>64<<20: raise RuntimeError('evidence directory exceeds 64 MiB')
    config=dict(SCENARIOS[a.scenario],scenario=a.scenario)
    for rep in range(1,4):
        order=['baseline','candidate'] if rep%2 else ['candidate','baseline']
        if a.suite=='scenario':
            for v in order: run_group(a.build,a.out,f'{a.scenario}-{v}-r{rep}',[v],config,{})
        elif a.suite=='settings':
            # Each candidate trial changes one parameter from candidate default.
            for name,env in [('gc50',{'GOGC':'50'}),('mem64',{'GOMEMLIMIT':'64MiB'}),('procs2',{'GOMAXPROCS':'2'})]:
                run_group(a.build,a.out,f'setting-{name}-r{rep}',['candidate'],config,env)
        elif a.suite=='profiles':
            for v in order:
                for mode in ('cpu','alloc'):
                    run_group(a.build,a.out,f'profile-{mode}-{v}-r{rep}',[v],dict(config,profile=mode),{})
        elif a.suite=='observer':
            for v in order: run_group(a.build,a.out,f'observer-off-{v}-r{rep}',[v],config,{},observe=False)
        elif a.suite=='overlap':
            # Same total offered rate as one-process stable_sse, split in half.
            cfg=dict(config,rate=30,count=60)
            pairs=[('bb',['baseline','baseline']),('bc',['baseline','candidate'])]
            if rep%2==0: pairs.reverse()
            for name,variants in pairs: run_group(a.build,a.out,f'overlap-{name}-r{rep}',variants,cfg,{})
if __name__=='__main__': main()
