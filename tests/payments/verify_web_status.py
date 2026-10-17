#!/usr/bin/env python3
# Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
"""Verify payment status through fresh PG, committed Core and current Go.

Requires existing owned 0700 output/socket roots and cached tools/image/crates.
Uses the cooperative heavy-build lease, offline compilation and a new A09 PG.
No external database URL, image pull, provider request or deployment is accepted.
"""
from pathlib import Path
import argparse, math, hashlib, importlib.util, io, json, os, secrets, shutil, signal, subprocess, sys, tarfile, time
ROOT=next(p for p in Path(__file__).resolve().parents if (p/'scripts/test-resource-lease.py').is_file())
OUT=None
sys.path.insert(0,str(ROOT/'tests/architecture_completion'))
from pg_fixture import Fixture, write_private
from build_lease import private_directory

def digest(p): return hashlib.sha256(p.read_bytes()).hexdigest()
def private_json(path,value):write_private(path,json.dumps(value,indent=2,sort_keys=True)+'\n')
def run(command,cwd,env,log,timeout=600,monitor=False,min_free_gib=10):
    began=time.monotonic();code=None;error_type=None
    try:
        with log.open('xb') as stream:
            os.chmod(log,0o600)
            if not monitor:
                p=subprocess.run(command,cwd=cwd,env=env,stdout=stream,stderr=subprocess.STDOUT,stdin=subprocess.DEVNULL,timeout=timeout)
                code=p.returncode
            else:
                started=time.monotonic();low=0;reason=None
                with (log.parent/'rust-resources.private.jsonl').open('x') as samples:
                    os.chmod(samples.name,0o600)
                    with subprocess.Popen(command,cwd=cwd,env=env,stdout=stream,stderr=subprocess.STDOUT,stdin=subprocess.DEVNULL) as process:
                        while process.poll() is None:
                            memory={line.split(':',1)[0]:int(line.split()[1])*1024 for line in Path('/proc/meminfo').read_text().splitlines() if line.startswith(('MemAvailable:','SwapTotal:','SwapFree:'))}
                            available=memory['MemAvailable'];free=shutil.disk_usage(ROOT).free
                            raw=subprocess.run(['ps','-e','-o','pid=,ppid=,rss=,comm='],capture_output=True,text=True,check=True).stdout
                            processes=[]
                            for line in raw.splitlines():
                                fields=line.split(None,3)
                                if len(fields)==4:processes.append((int(fields[0]),int(fields[1]),int(fields[2]),fields[3]))
                            own={process.pid}
                            while True:
                                more={pid for pid,parent,_,_ in processes if parent in own}
                                if more<=own:break
                                own|=more
                            selected=[{'pid':pid,'comm':name,'rss_kib':rss} for pid,_,rss,name in processes if pid in own]
                            samples.write(json.dumps({'elapsed_seconds':round(time.monotonic()-started,2),'mem_available_bytes':available,'disk_free_bytes':free,'swap_used_bytes':memory['SwapTotal']-memory['SwapFree'],'own_rss_sum_kib':sum(row['rss_kib'] for row in selected),'processes':selected})+'\n');samples.flush()
                            low=low+1 if available<1024**3 else 0
                            if free<min_free_gib*1024**3:reason=f'disk free below {min_free_gib:g} GiB'
                            if low>=3:reason='MemAvailable below 1 GiB for three samples'
                            if time.monotonic()-started>=timeout:reason='compile deadline exceeded'
                            if reason:
                                process.send_signal(signal.SIGINT)
                                try:process.wait(timeout=30)
                                except subprocess.TimeoutExpired:
                                    process.terminate()
                                    try:process.wait(timeout=30)
                                    except subprocess.TimeoutExpired:process.kill();process.wait()
                                break
                            time.sleep(5)
                        code=process.wait()
                if reason:raise RuntimeError(reason+'; owned Cargo interrupted, logs retained')
        if code:raise RuntimeError('owned command failed; inspect '+log.name)
    except BaseException as error:
        error_type=type(error).__name__
        raise
    finally:
        fd=os.open(log.parent/'commands.private.jsonl',os.O_WRONLY|os.O_APPEND|os.O_CREAT,0o600)
        with os.fdopen(fd,'a') as receipts:
            receipts.write(json.dumps({'argv':list(command),'cwd':str(cwd),'log':log.name,'returncode':code,'error_type':error_type,'elapsed_seconds':round(time.monotonic()-began,3),'resource_monitor':monitor})+'\n')

def execute(args):
    global OUT
    OUT=args.output
    name=args.run_name
    if not name.isalnum():raise RuntimeError('invalid run name')
    for root in (OUT,args.socket_root):
        if not root.is_absolute() or root.resolve()!=root:raise RuntimeError('private output must be absolute without symlinks')
        private_directory(root)
    for namepart in ('tmp','cache','gocache'):
        (OUT/namepart).mkdir(mode=0o700,exist_ok=True)
    lease_path=os.environ.get('LMM_TEST_LEASE')
    if not lease_path:raise RuntimeError('reviewed resource lease required')
    binding=json.loads((Path(lease_path)/'acquired.json').read_text())
    if not binding.get('heavy') or binding.get('lock',{}).get('path')!='/tmp/lmm-delegate/heavy-build.lock':raise RuntimeError('heavy resource binding required')
    work=OUT/name;work.mkdir(mode=0o700)
    head=subprocess.run(['git','rev-parse','--verify',args.core_revision+'^{commit}'],cwd=ROOT,capture_output=True,text=True,check=True).stdout.strip()
    test=ROOT/'services/core/tests/payment_web_status_independent.rs'
    test_relative='services/core/tests/payment_web_status_independent.rs'
    archived=subprocess.run(['git','archive',head,'services/core','contracts','deployment/postgres','services/extensions/internal/modules/payments/schema.sql','mise.toml'],cwd=ROOT,capture_output=True,check=True).stdout
    archive_inputs={}
    with tarfile.open(fileobj=io.BytesIO(archived)) as tar:
        for member in tar.getmembers():
            if member.isfile():archive_inputs[member.name]=hashlib.sha256(tar.extractfile(member).read()).hexdigest()
            elif not member.isdir():raise RuntimeError('unsupported archived link; input review required')
        if args.reuse_build:
            private_directory(args.reuse_build)
            saved=json.loads((args.reuse_build/'inputs.json').read_text())
            overlay=Path(saved.get('overlay_root',str(args.reuse_build/'overlay')))
            private_directory(overlay)
            actual={str(path.relative_to(overlay)):digest(path) for path in sorted(overlay.rglob('*')) if path.is_file() and str(path.relative_to(overlay))!=test_relative}
            expected={path:sha for path,sha in archive_inputs.items() if path!=test_relative}
            if saved['base']!=head or actual!=expected:raise RuntimeError('frozen Core/contracts/schema inputs changed; no reuse')
            prior=overlay/test_relative
            if prior.exists() and digest(prior)!=digest(test):
                shutil.copy2(prior,work/'prior-independent-test.rs')
                os.chmod(work/'prior-independent-test.rs',0o600)
        else:
            overlay=work/'overlay';overlay.mkdir(mode=0o700)
            tar.extractall(overlay,filter='data')
    shutil.copy2(test,overlay/test_relative)
    archive_inputs[test_relative]=digest(test)
    cargo=Path.home()/'.cargo'
    env={k:v for k,v in os.environ.items() if not k.startswith(('PG','DOCKER_','LMM_CORE','DATABASE_URL','SQL_DSN'))}
    env.update(MISE_DISABLE_TOOLS='rust',MISE_AUTO_INSTALL='false',RUSTUP_TOOLCHAIN='1.99.0',RUSTUP_AUTO_INSTALL='0',
        MISE_CACHE_DIR=str(OUT/'cache/mise'),XDG_CACHE_HOME=str(OUT/'cache'),TMPDIR=str(OUT/'tmp'),
        GOTMPDIR=str(OUT/'tmp'),GOCACHE=str(OUT/'gocache'),GOMODCACHE=str(Path.home()/'go/pkg/mod'),GOPROXY='off',GOSUMDB='off',
        CARGO_HOME=str(cargo),CARGO_TARGET_DIR=str(ROOT/'services/core/target'),CARGO_BUILD_JOBS='2',PYTHONDONTWRITEBYTECODE='1')
    paths={str(p.relative_to(ROOT)):digest(p) for p in sorted((ROOT/'services/extensions').rglob('*')) if p.is_file() and (p.suffix in {'.go','.sql'} or p.name in {'go.mod','go.sum'}) and 'node_modules' not in p.parts}
    actual_archive={str(path.relative_to(overlay)):digest(path) for path in sorted(overlay.rglob('*')) if path.is_file()}
    if actual_archive!=archive_inputs:raise RuntimeError('prepared archive does not match frozen inputs')
    corepaths={path:sha for path,sha in archive_inputs.items() if path.startswith('services/core/')}
    runner_path=Path(__file__).resolve();runner_hash=digest(runner_path)
    helper_paths=[ROOT/'scripts/test-resource-lease.py',ROOT/'tests/architecture_completion/pg_fixture.py',ROOT/'tests/architecture_completion/build_lease.py']
    helper_hashes={str(path.relative_to(ROOT)):digest(path) for path in helper_paths}
    cargo_config=cargo/'config.toml';cargo_config_hash=digest(cargo_config) if cargo_config.exists() else None
    private_json(work/'inputs.json',{'base':head,'go':paths,'core_overlay':corepaths,'archive_inputs':archive_inputs,'overlay_root':str(overlay),'new_rust_test':digest(test),
        'resource_budget':{'min_free_gib':args.min_free_gib,'min_mem_available_gib':1,'low_memory_samples':3,'compile_deadline_seconds':720,'cargo_build_jobs':2},
        'runner_path':str(runner_path),'runner_sha256':runner_hash,'verification_helpers':helper_hashes,'cargo_config_sha256':cargo_config_hash,
        'cache_policy':'original Cargo source/cache paths under separately tracked known Cargo root, offline; shared target; private Go and temp caches'})
    versions={}
    for tool,version_args in [('rustc',['rustc','--version']),('cargo',['cargo','--version']),('go',['go','version']),('mise',['mise','--version'])]:
        result=subprocess.run(['mise','exec','--',*version_args],cwd=ROOT,env=env,capture_output=True,text=True,check=True)
        versions[tool]=result.stdout.strip()
    go_settings=subprocess.run(['mise','exec','--','go','env','-json','GOOS','GOARCH','CGO_ENABLED','GOFLAGS','GOCACHE','GOMODCACHE'],cwd=ROOT,env=env,capture_output=True,text=True,check=True)
    versions['go_environment']=json.loads(go_settings.stdout)
    versions['cargo_home_config_sha256']=digest(cargo/'config.toml') if (cargo/'config.toml').exists() else None
    versions['inherited_home_config_sha256']=digest(Path.home()/'.cargo/config.toml') if (Path.home()/'.cargo/config.toml').exists() else None
    private_json(work/'tool-versions.json',versions)
    binary=work/'payment-web-status.test'
    run(['mise','exec','--','go','test','-race','-mod=readonly','-c','-o',str(binary),'./internal/modules/payments'],ROOT/'services/extensions',env,work/'go-compile.private.log')
    run(['mise','exec','--','cargo','test','--offline','--locked','--test','payment_web_status_independent','--no-run','--message-format=json'],overlay/'services/core',env,work/'rust-compile.private.log',timeout=720,monitor=True,min_free_gib=args.min_free_gib)
    rustbinary=None
    for line in (work/'rust-compile.private.log').read_text().splitlines():
        try: obj=json.loads(line)
        except json.JSONDecodeError:continue
        if obj.get('reason')=='compiler-artifact' and obj.get('target',{}).get('name')=='payment_web_status_independent' and obj.get('executable'):rustbinary=obj['executable']
    if rustbinary is None:raise RuntimeError('Rust executable not proven')
    fixture=Fixture(work)
    failure=None;cleanup=None
    try:
        fixture.start()
        allocation=fixture.allocate('A05')
        module_database='lmm_payments_'+fixture.nonce[:12]
        fixture.sql('CREATE DATABASE "'+module_database+'" OWNER "'+allocation['owner']+'" TEMPLATE template0;')
        fixture.databases.add(module_database)
        fixture.sql('REVOKE ALL ON DATABASE "'+module_database+'" FROM PUBLIC;GRANT CONNECT ON DATABASE "'+module_database+'" TO "'+allocation['runtime']+'";')
        fixture.sql('ALTER SCHEMA public OWNER TO "'+allocation['owner']+'";REVOKE ALL ON SCHEMA public FROM PUBLIC;',module_database)
        allocation={**allocation,'database':module_database}
        private_json(work/'module-resource.json',allocation)
        schema=(ROOT/'services/extensions/internal/modules/payments/schema.sql').read_text()
        fixture.sql('SET ROLE "'+allocation['owner']+'";'+schema+';RESET ROLE;GRANT USAGE ON SCHEMA public TO "'+allocation['runtime']+'";GRANT SELECT ON payment_storage_guard TO "'+allocation['runtime']+'";GRANT SELECT,INSERT,UPDATE ON payment_orders,payment_events TO "'+allocation['runtime']+'";',allocation['database'])
        passfile=(fixture.root/'A05/runtime.pgpass').read_text().strip().split(':')
        runtime_url='postgresql://'+allocation['runtime']+':'+passfile[-1]+'@127.0.0.1:'+str(allocation['port'])+'/'+allocation['database']+'?sslmode=disable'
        modulefile=work/'runtime-url.private';write_private(modulefile,runtime_url)
        config=dict(line.split('=',1) for line in (fixture.root/'postgres.env').read_text().splitlines())
        env.update(DATABASE_URL='postgresql://a09_admin:'+config['POSTGRES_PASSWORD']+'@127.0.0.1:'+str(fixture.binding['port'])+'/postgres',
            PAYMENT_WEB_STATUS_OUTPUT=str(work),PAYMENT_WEB_STATUS_SOCKET_DIR=str(args.socket_root/name),
            PAYMENT_WEB_STATUS_GO_BINARY=str(binary),PAYMENT_WEB_STATUS_GO_SOURCE=str(ROOT/'services/extensions/internal/modules/payments'),
            PAYMENT_WEB_STATUS_MODULE_URL_FILE=str(modulefile),PAYMENT_WEB_STATUS_MODULE_DB=allocation['database'])
        run([rustbinary,'real_web_status_preserves_money_and_rechecks_live_authority','--exact','--ignored','--nocapture','--test-threads=1'],overlay/'services/core',env,work/'rust-run.private.log',180)
        log=(work/'rust-run.private.log').read_text()
        if 'running 1 test' not in log or '1 passed' not in log or 'real_web_status_preserves_money_and_rechecks_live_authority' not in log:
            raise RuntimeError('real Rust selected test did not execute')
        fixture.verify()
    except BaseException as error:
        failure=type(error).__name__+': '+str(error)
    finally:
        if fixture.container and fixture.identity:
            try:fixture.cleanup();cleanup={'owned_pg_removed':True,'physical_identity_verified':True,'no_client_backends':True}
            except BaseException as error:cleanup={'owned_pg_removed':False,'reason':type(error).__name__}
        current={str(p.relative_to(ROOT)):digest(p) for p in sorted((ROOT/'services/extensions').rglob('*')) if p.is_file() and (p.suffix in {'.go','.sql'} or p.name in {'go.mod','go.sum'}) and 'node_modules' not in p.parts}
        final_archive={str(path.relative_to(overlay)):digest(path) for path in sorted(overlay.rglob('*')) if path.is_file()}
        final_helpers={str(path.relative_to(ROOT)):digest(path) for path in helper_paths}
        final_config=digest(cargo_config) if cargo_config.exists() else None
        stable=current==paths and final_archive==archive_inputs and digest(test)==digest(overlay/'services/core/tests'/test.name) and digest(runner_path)==runner_hash and final_helpers==helper_hashes and final_config==cargo_config_hash
        private_json(work/'runner-result.json',{'status':'pass' if failure is None and stable and cleanup and cleanup['owned_pg_removed'] else 'fail','failure':failure,'frozen_inputs_unchanged':stable,'cleanup':cleanup,'core_base':head,'go_test_binary_sha256':digest(binary),'rust_test_binary_sha256':digest(Path(rustbinary))})
    if failure or not stable or not cleanup or not cleanup['owned_pg_removed']:raise RuntimeError('independent web status acceptance failed; inspect private owned evidence')
    print('independent web status acceptance PASS; frozen inputs stable; fresh owned PostgreSQL removed')
def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output',required=True,type=Path)
    parser.add_argument('--socket-root',required=True,type=Path)
    parser.add_argument('--core-revision',default='HEAD')
    parser.add_argument('--min-free-gib',type=float,default=10,help='Stop this build below this available disk budget (default: 10 GiB; not a product capacity threshold)')
    parser.add_argument('--run-name',default='run'+secrets.token_hex(6))
    parser.add_argument('--reuse-build',type=Path,help='Reuse frozen Core products/contracts; bind the current independent test and always create fresh PG')
    parser.add_argument('--leased',action='store_true',help=argparse.SUPPRESS)
    args=parser.parse_args()
    if not math.isfinite(args.min_free_gib) or args.min_free_gib<0:parser.error('min-free-gib must be a finite non-negative number')
    if args.leased:
        execute(args)
        return 0
    for root in (args.output,args.socket_root):
        if not root.is_absolute() or root.resolve()!=root:parser.error('output and socket root must be absolute private directories')
        private_directory(root)
    spec=importlib.util.spec_from_file_location('payment_web_status_lease',ROOT/'scripts/test-resource-lease.py')
    lease=importlib.util.module_from_spec(spec);sys.modules[spec.name]=lease;spec.loader.exec_module(lease)
    command=['mise','exec','--',sys.executable,'-B',str(Path(__file__).resolve()),'--output',str(args.output),'--socket-root',str(args.socket_root),'--core-revision',args.core_revision,'--run-name',args.run_name,'--min-free-gib',str(args.min_free_gib),'--leased']
    if args.reuse_build: command+=['--reuse-build',str(args.reuse_build)]
    code=lease.run(args.output,'A05',command,heavy=True)
    result=args.output/args.run_name/'runner-result.json'
    if code==0 and result.is_file() and json.loads(result.read_text()).get('status')=='pass':
        print('payment web status acceptance PASS; selected real tests ran; frozen inputs stable; fresh owned PG removed')
        print('evidence:',result)
        return 0
    print('payment web status acceptance FAILED; inspect private owned output:',args.output/args.run_name,file=sys.stderr)
    return code or 1

if __name__=='__main__':raise SystemExit(main())
