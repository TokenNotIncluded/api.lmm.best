#!/usr/bin/env python3
"""Run bounded source-slice correctness checks and fixed-count microbenchmarks."""
import argparse,json,os,pathlib,resource,subprocess,time

def limit():
    resource.setrlimit(resource.RLIMIT_AS,(2<<30,2<<30))
    resource.setrlimit(resource.RLIMIT_CPU,(12,12))
    resource.setrlimit(resource.RLIMIT_NOFILE,(256,256))
    resource.setrlimit(resource.RLIMIT_CORE,(0,0))

def main():
    p=argparse.ArgumentParser();p.add_argument('--build',type=pathlib.Path,required=True);p.add_argument('--out',type=pathlib.Path,required=True);a=p.parse_args();a.out.mkdir(parents=True,exist_ok=True)
    tests='^TestDP26(RPCValidation|SSEFraming|StatusesAndCancellation|ResponseOwnershipAndIsolation)$'
    env=dict(os.environ,GOMAXPROCS='1',GOGC='100',GOMEMLIMIT='96MiB',GOTOOLCHAIN='local',GOPROXY='off',GOSUMDB='off',GOWORK='off');env.pop('DP26_RUN',None)
    events=[]
    for rep in range(1,4):
        for variant in (['baseline','candidate'] if rep%2 else ['candidate','baseline']):
            cmd=['unshare','--user','--map-root-user','--net','--','taskset','-c',str(min(os.sched_getaffinity(0))),str((a.build/(variant+'.test')).resolve()),'-test.run='+tests,'-test.v','-test.bench=^BenchmarkDP26RPC$','-test.benchtime=50x','-test.benchmem','-test.timeout=12s']
            logfile=a.out/f'checks-{variant}-r{rep}.txt'
            with logfile.open('x') as f:
                began=time.monotonic();result=subprocess.run(cmd,env=env,stdout=f,stderr=subprocess.STDOUT,preexec_fn=limit,timeout=15)
            events.append({'variant':variant,'repeat':rep,'exit_code':result.returncode,'wall_seconds':time.monotonic()-began,'log':logfile.name,'command':cmd,'scope':'source slice only; no model or funds; microbenchmark is not service capacity'})
            (a.out/'checks.json').write_text(json.dumps(events,indent=2)+'\n');print(logfile.name,result.returncode,flush=True)
            if result.returncode: raise SystemExit(result.returncode)
if __name__=='__main__':main()
