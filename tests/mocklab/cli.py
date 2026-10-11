"""Local commands. Production deployment and database changes are not supported."""
from __future__ import annotations

import argparse
import http.client
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time

from .client import Client, private_write
from .config import Config, Invalid, MARKER, loads, local_url, object_fields, wire
from .probes import running_probes
from .report import go_adapters, go_transport, isolated_network, row, run_process, save_report, selftests, source_info
from .scenarios import requirements
from .server import Lab

ROOT = Path(__file__).resolve().parents[2]


def new_output(path: Path) -> Path:
    if path.exists():
        raise Invalid("output directory already exists; use a new per-run directory")
    path.mkdir(mode=0o700, parents=False)
    return path


def acceptance_rows(target_path: Path | None):
    """Read-only readiness evidence; no invented HTTP or ledger test driver."""
    specs = requirements()
    if target_path is None:
        return [row(s["scenario_id"], "application-e2e", "BLOCKED",
                    reason="actual host setup, per-run databases, funds bridge and ledger assertion driver are not provided in this increment") for s in specs]
    if target_path.stat().st_size > 8192:
        raise Invalid("targets file exceeds 8 KiB")
    config = object_fields(loads(target_path.read_bytes()), {"marker", "targets"}, {"marker", "targets"})
    if config["marker"] != MARKER or not isinstance(config["targets"], list) or not 1 <= len(config["targets"]) <= 2:
        raise Invalid("targets require an explicit test marker and one or two entries")
    rows = []
    names=set()
    for target in config["targets"]:
        target=object_fields(target,{"name","url","host_header","declared_sha"},{"name","url","declared_sha"})
        name=target["name"]
        if not isinstance(name,str) or not re.fullmatch(r"[a-z][a-z0-9_.-]{0,47}",name) or name in names:
            raise Invalid("invalid or duplicate target name")
        names.add(name)
        sha=target["declared_sha"]
        if not isinstance(sha,str) or not re.fullmatch(r"[a-f0-9]{40}",sha):
            raise Invalid("record the actual intended application checkout SHA")
        host,port,_=local_url(target["url"],path=False)
        h=target.get("host_header",name)
        if not isinstance(h,str) or not re.fullmatch(r"[a-zA-Z0-9_.-]{1,128}(:[0-9]{1,5})?",h):
            raise Invalid("invalid local virtual-host header")
        conn=http.client.HTTPConnection(host,port,timeout=2)
        code=None
        status="BLOCKED"
        reason="application readiness unavailable"
        digest=None
        try:
            conn.request("GET","/health/ready",headers={"Host":h})
            response=conn.getresponse()
            body=response.read(65537)
            code=response.status
            if len(body)>65536:
                reason="readiness body exceeds limit"
            else:
                import hashlib
                digest=hashlib.sha256(body).hexdigest()
                if code==503 and b"core_migration_incomplete" in body:
                    reason="actual core reports core_migration_incomplete; no business request was attempted"
                elif code==200:
                    status="NOT RUN"
                    reason="ready endpoint is not ledger evidence; real business/DB assertion driver still needs implementation"
                else:
                    reason=f"readiness returned HTTP {code}; redirects are not followed"
        except (OSError,http.client.HTTPException):
            reason="cannot reach declared loopback readiness endpoint"
        finally:
            conn.close()
        for spec in specs:
            rows.append(row(name+":"+spec["scenario_id"],"application-e2e",status,reason=reason,
                            declared_application_sha=sha, readiness_http_status=code, readiness_body_sha256=digest))
    return rows


def metadata(root:Path, manifest=None):
    return {"created_unix":int(time.time()),"source":source_info(root),"network_isolated":isolated_network(),
            "run_id":manifest["run_id"] if manifest else None,"seed":manifest["seed"] if manifest else 7}


def run_suite(manifest_path:Path, output:Path, *, skip_adapters=False, targets=None):
    client=Client.read(manifest_path)
    rows,log=selftests()
    private_write(output/"component-tests.log",log.encode())
    rows+=running_probes(client)
    stats=client.control("stats")
    private_write(output/"provider-trace.json",json.dumps(stats,indent=2).encode()+b"\n")
    rows.append(go_transport(ROOT,output))
    rows+=go_adapters(ROOT,manifest_path,output,not skip_adapters)
    rows+=acceptance_rows(targets)
    final_stats=client.control("stats")
    rows.append(row("run.resource-check","mock-component","FAIL" if final_stats["active"] or final_stats["trace_full"] or
                    final_stats["pending_callbacks"] or final_stats["callback_active"] else "PASS",
                    active=final_stats["active"],trace_full=final_stats["trace_full"],
                    pending_callbacks=final_stats["pending_callbacks"],callback_active=final_stats["callback_active"]))
    private_write(output/"provider-final.json",json.dumps(final_stats,indent=2).encode()+b"\n")
    code=save_report(output,metadata(ROOT,client.manifest),rows,requirements=requirements())
    print(str(output/"report.json"))
    return code


def up(args):
    if args.manifest.exists():
        raise Invalid("manifest already exists; do not overwrite another run")
    cfg=Config.read(args.config)
    # Inherited production config is not needed and must not be mistaken for setup.
    for key in ("SQL_DSN","DATABASE_URL","LMM_CORE_DATABASE_URL","STRIPE_SECRET_KEY"):
        if os.environ.get(key):
            raise Invalid("unset production/service environment variables before starting mocklab")
    with Lab(cfg) as server:
        manifest=server.manifest()
        private_write(args.manifest,wire(manifest)+b"\n")
        print(json.dumps({"marker":MARKER,"manifest":str(args.manifest.resolve()),"run_id":server.run_id,
                          "base_url":server.base,"application_started":False}),flush=True)
        try:
            while not server.closed.wait(0.1):
                if time.monotonic()-server.started>cfg.limits.run_seconds:
                    break
        except KeyboardInterrupt:
            pass
    return 0


def isolated(args):
    output=new_output(args.output)
    unshare,ip=shutil.which("unshare"),shutil.which("ip")
    start=time.monotonic()
    if unshare is None or ip is None:
        return save_report(output,metadata(ROOT),[row("network-isolation","environment","BLOCKED",reason="Linux unshare and ip are required")])
    # No shell, interpolation, daemon or persistent namespace is created.
    command=[unshare,"--user","--map-root-user","--net",sys.executable,"-B",str(ROOT/"scripts/mocklab.py"),"_isolated",
             "--config",str(args.config.resolve()),"--output",str(output.resolve())]
    if args.skip_adapters:
        command.append("--skip-adapters")
    env={k:os.environ[k] for k in ("PATH","HOME","TMPDIR","GOCACHE","GOPATH") if k in os.environ}
    env.update(PYTHONDONTWRITEBYTECODE="1",PYTHONHASHSEED="0")
    code,error=run_process(command,cwd=ROOT,env=env,timeout=180,output=output/"isolation.log")
    if not (output/"report.json").exists():
        return save_report(output,metadata(ROOT),[row("network-isolation","environment","BLOCKED",start,
                           reason="isolated child did not complete: "+error,exit_code=code,log="isolation.log")])
    # Do not hide a namespace/child crash behind a partial successful report.
    if code not in {0,1,2}:
        print("isolated process did not exit normally",file=sys.stderr)
        return 1
    print(str(output/"report.json"))
    return code


def isolated_child(args):
    subprocess.run([shutil.which("ip") or "ip","link","set","lo","up"],check=True,timeout=3)
    if not isolated_network():
        raise Invalid("kernel network namespace has non-loopback interfaces")
    # Manifest and all mutable provider state die with this scope. Reports survive.
    with tempfile.TemporaryDirectory(prefix="lmm-mocklab-") as directory:
        manifest=Path(directory)/"run.json"
        # A mapped user may not traverse the parent HOME. Keep build output in
        # this run; never chmod a user home or share mutable production state.
        os.environ["GOCACHE"]=str(Path(directory)/"go-cache")
        with Lab(Config.read(args.config)) as server:
            private_write(manifest,wire(server.manifest())+b"\n")
            return run_suite(manifest,args.output,skip_adapters=args.skip_adapters)


def main():
    parser=argparse.ArgumentParser(description="Explicitly test-only provider mock lab; never deploys an application")
    sub=parser.add_subparsers(dest="command",required=True)
    p=sub.add_parser("up",help="run optional loopback provider process in foreground; does not start the WIP app")
    p.add_argument("--config",type=Path,default=ROOT/"tests/mocklab/example.json")
    p.add_argument("--manifest",type=Path,required=True)
    p=sub.add_parser("run",help="probe providers, test adapters when isolated, and emit honest acceptance gaps")
    p.add_argument("--manifest",type=Path,required=True)
    p.add_argument("--output",type=Path,required=True)
    p.add_argument("--skip-adapters",action="store_true")
    p.add_argument("--targets",type=Path)
    p=sub.add_parser("selftest",help="only executable provider-component tests; no E2E claims")
    p.add_argument("--output",type=Path,required=True)
    for name in ("isolated","_isolated"):
        p=sub.add_parser(name,help="run the lab and tests inside a disposable Linux network namespace")
        p.add_argument("--config",type=Path,default=ROOT/"tests/mocklab/example.json")
        p.add_argument("--output",type=Path,required=True)
        p.add_argument("--skip-adapters",action="store_true")
    p=sub.add_parser("control",help="advance/reset or inspect this private provider run only")
    p.add_argument("--manifest",type=Path,required=True)
    p.add_argument("--data",required=True,help='closed JSON command, e.g. {"op":"advance","ms":1000}')
    p=sub.add_parser("requirements",help="emit the uncompleted real-application acceptance specifications")
    args=parser.parse_args()
    try:
        if args.command=="up":
            return up(args)
        if args.command=="control":
            value=loads(args.data)
            if not isinstance(value,dict) or not isinstance(value.get("op"),str):
                raise Invalid("control requires an op field")
            op=value.pop("op")
            print(json.dumps(Client.read(args.manifest).control(op,**value),ensure_ascii=False,indent=2))
            return 0
        if args.command=="requirements":
            print(json.dumps(requirements(),ensure_ascii=False,indent=2));return 0
        if args.command=="selftest":
            output=new_output(args.output)
            rows,log=selftests()
            private_write(output/"component-tests.log",log.encode())
            code=save_report(output,metadata(ROOT),rows)
            print(str(output/"report.json"));return code
        if args.command=="run":
            return run_suite(args.manifest,new_output(args.output),skip_adapters=args.skip_adapters,targets=args.targets)
        if args.command=="isolated":
            return isolated(args)
        return isolated_child(args)
    except KeyboardInterrupt:
        return 130
    except (Invalid,OSError,subprocess.SubprocessError) as exc:
        print("mocklab: "+str(exc),file=sys.stderr)
        return 1
