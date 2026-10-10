#!/usr/bin/env python3
"""Offline, loopback-only process tests. Does not call Docker, git, CI or a core.
Uses the repository host code plus synthetic modules; see README limitations.
"""
from __future__ import annotations
import argparse
import concurrent.futures
import csv
import hashlib
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import select
import shutil
import signal
import sqlite3
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

TOKEN = "Bearer " + "x" * 32
ENV = dict(os.environ, GOWORK="off", GOPROXY="off", GOTOOLCHAIN="local",
           GOSUMDB="off", GOMAXPROCS="1", GOMEMLIMIT="64MiB", CGO_ENABLED="1")


def require(condition: bool, message: str) -> None:
    if not condition:
        raise AssertionError(message)


def request(base: str, path: str, body: bytes | None = None, timeout: float = 5):
    req = urllib.request.Request(base + path, data=body,
        headers={"Authorization": TOKEN, "Content-Type": "application/json"})
    # No environment proxies; no external hosts are used.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with opener.open(req, timeout=timeout) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def identity(pid: int) -> dict:
    text = Path(f"/proc/{pid}/stat").read_text()
    return {"pid": pid, "start_ticks": int(text.split(") ", 1)[1].split()[19])}


def memory(pid: int) -> dict | None:
    try:
        def read_values(path):
            return {line.split(":", 1)[0]: int(line.split()[1])
                    for line in Path(path).read_text().splitlines()
                    if ":" in line and len(line.split()) > 1 and line.split()[1].isdigit()}
        a = read_values(f"/proc/{pid}/status")
        b = read_values(f"/proc/{pid}/smaps_rollup")
        return {"pid": pid, "rss_kib": a.get("VmRSS", 0),
                "hwm_kib": a.get("VmHWM", 0), "pss_kib": b.get("Pss", 0)}
    except (FileNotFoundError, ProcessLookupError, PermissionError):
        return None


class Provider(BaseHTTPRequestHandler):
    db_path: Path
    def log_message(self, *args):
        pass
    def answer(self, code, data):
        b = json.dumps(data).encode()
        self.send_response(code); self.send_header("Content-Length", str(len(b)))
        self.end_headers(); self.wfile.write(b)
    def do_POST(self):
        if self.path != "/apply":
            self.answer(404, {}); return
        n = int(self.headers.get("Content-Length", "0"))
        if n > 100000:
            self.answer(413, {}); return
        value = json.loads(self.rfile.read(n))
        # Deliberately NOT idempotent: every apply inserts a new effect. A
        # duplicate worker call therefore fails the experiment's assertions.
        with sqlite3.connect(self.db_path) as db:
            cur = db.execute("INSERT INTO effects(event_key,digest) VALUES(?,?)",
                             (value["key"], value["digest"]))
            seq = cur.lastrowid
        self.answer(200, {"Key": value["key"], "Digest": value["digest"],
                          "Reference": f"mock-effect-{seq}"})
    def do_GET(self):
        q = urllib.parse.urlparse(self.path)
        key = urllib.parse.parse_qs(q.query).get("key", [""])[0]
        with sqlite3.connect(self.db_path) as db:
            rows = db.execute("SELECT rowid,digest FROM effects WHERE event_key=?", (key,)).fetchall()
        if q.path != "/lookup" or len(rows) != 1:
            self.answer(404 if not rows else 409, {}); return
        seq, digest = rows[0]
        self.answer(200, {"Key": key, "Digest": digest, "Reference": f"mock-effect-{seq}"})


class Entry(BaseHTTPRequestHandler):
    target = ""
    target_lock = threading.Lock()
    def log_message(self, *args):
        pass
    def forward(self):
        with Entry.target_lock:
            target = Entry.target
        parsed = urllib.parse.urlparse(target)
        connection = http.client.HTTPConnection(parsed.hostname, parsed.port, timeout=5)
        try:
            n = int(self.headers.get("Content-Length", "0"))
            if n > 100000:
                self.send_error(413); return
            data = self.rfile.read(n) if n else None
            connection.request(self.command, self.path, data,
                               {"Authorization": TOKEN, "Content-Type": "application/json"})
            response = connection.getresponse()
            self.send_response(response.status)
            self.send_header("Content-Type", response.getheader("Content-Type", "application/json"))
            self.send_header("Connection", "close")
            self.end_headers()
            while block := response.read1(4096):
                self.wfile.write(block); self.wfile.flush()
        except (ConnectionError, OSError, http.client.HTTPException):
            # No retry: an interrupted request must remain visible to the test.
            self.close_connection = True
        finally:
            connection.close()
    do_GET = forward
    do_POST = forward


class Harness:
    def __init__(self, root: Path, output: Path):
        self.root, self.output = root, output
        self.temp = tempfile.TemporaryDirectory(prefix="dp23-local-")
        self.tmp = Path(self.temp.name)
        self.children = []
        self.logs = []
        self.rows = []
        self.phase = "build"
        self.stopping = threading.Event()
        self.servers = []
        self.pool = concurrent.futures.ThreadPoolExecutor(max_workers=8)
        self.report = {"scope": "real Go host and handoff packages; synthetic modules and external effects",
            "base": "72667564c0431754d4856dc2e0db55f360bd2745",
            "production_main_built": False, "rust_core_tested": False,
            "rollback_scope": "two binaries from the same source with different build IDs; format 1 only; not a historical production downgrade",
            "postgres_tested": False, "docker_tested": False,
            "source": {}, "rounds": [], "crash_tests": [], "failed_candidates": [],
            "runtime_limits": {"GOMAXPROCS": 1, "GOMEMLIMIT": "64MiB", "sampler_ms": 10,
                               "note": "Go memory limit is not a cgroup hard limit"}}
    def run_command(self, args, cwd=None, timeout=60):
        return subprocess.run(args, cwd=cwd, env=dict(ENV, GOMEMLIMIT="1GiB", GOMAXPROCS="2"), check=True,
                              capture_output=True, timeout=timeout)
    def build(self):
        module_root = self.tmp / "build"
        module_root.mkdir()
        (module_root / "go.mod").write_text(
            "module github.com/TokenNotIncluded/api.lmm.best/extensions\n\ngo 1.23.0\n")
        relative = ["internal/modules/host.go", "internal/modules/selection.go",
                    "internal/modules/lifecycle.go", "internal/modules/lifecycle_test.go",
                    "internal/handoff/store_linux.go", "internal/handoff/store_linux_test.go"]
        source = self.root / "apps/lmm-extensions"
        for rel in relative:
            src, dst = source / rel, module_root / rel
            dst.parent.mkdir(parents=True, exist_ok=True); shutil.copy2(src, dst)
            self.report["source"][str(src.relative_to(self.root))] = hashlib.sha256(src.read_bytes()).hexdigest()
        probe = self.root / "experiments/deploy/dp-23/probe.go"
        dest = module_root / "cmd/dp23/main.go"
        dest.parent.mkdir(parents=True); shutil.copy2(probe, dest)
        self.report["source"][str(probe.relative_to(self.root))] = hashlib.sha256(probe.read_bytes()).hexdigest()
        self.report["go_version"] = self.run_command(["go", "version"]).stdout.decode().strip()
        tests = self.run_command(["go", "test", "-race", "-count=1", "-json", "./internal/modules", "./internal/handoff"], module_root)
        (self.output / "go-tests.jsonl").write_bytes(tests.stdout)
        (self.output / "go-tests-stderr.log").write_bytes(tests.stderr)
        vet = self.run_command(["go", "vet", "./internal/modules", "./internal/handoff", "./cmd/dp23"], module_root)
        (self.output / "go-vet.log").write_bytes(vet.stdout + vet.stderr)
        self.binaries = {}
        for version in ("v1", "v2"):
            binary = self.tmp / f"probe-{version}"
            self.run_command(["go", "build", "-trimpath", "-ldflags", f"-s -w -X main.buildVersion={version}",
                              "-o", str(binary), "./cmd/dp23"], module_root)
            self.binaries[version] = binary
        self.report["probe_binaries"] = {v: {"sha256": hashlib.sha256(p.read_bytes()).hexdigest(),
                                             "bytes": p.stat().st_size} for v, p in self.binaries.items()}
        self.report["production_go_mod_unchanged"] = True
    def data(self, name):
        d = self.tmp / name; d.mkdir(mode=0o700)
        # Explicit empty fixture, not automatic production schema installation.
        fd = os.open(d / "tasks.json", os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as f:
            json.dump({"format": 1, "owner": {"id": "", "epoch": 0}, "tasks": {}}, f)
            f.flush(); os.fsync(f.fileno())
        fd = os.open(d, os.O_RDONLY); os.fsync(fd); os.close(fd)
        return d
    def server(self, handler):
        s = ThreadingHTTPServer(("127.0.0.1", 0), handler)
        s.daemon_threads = True
        t = threading.Thread(target=s.serve_forever, daemon=True); t.start()
        self.servers.append(s)
        return f"http://127.0.0.1:{s.server_port}"
    def start_services(self):
        Provider.db_path = self.tmp / "mock-effects.sqlite"
        with sqlite3.connect(Provider.db_path) as db:
            db.execute("CREATE TABLE effects(event_key TEXT NOT NULL,digest TEXT NOT NULL)")
        self.provider = self.server(Provider)
        self.entry = self.server(Entry)
        self.entry_identity = identity(os.getpid())
        self.report["entry"] = dict(self.entry_identity, url=self.entry, kind="Python test entry, NOT Rust")
        self.monitor = threading.Thread(target=self.sample, daemon=True); self.monitor.start()
    def sample(self):
        while not self.stopping.is_set():
            snapshots = [memory(p.pid) for p in list(self.children) if p.poll() is None]
            values = [m for m in snapshots if m]
            self.rows.append({"monotonic": time.monotonic(), "phase": self.phase,
                "processes": len(values), "rss_kib": sum(m["rss_kib"] for m in values),
                "pss_kib": sum(m["pss_kib"] for m in values)})
            self.stopping.wait(0.01)
    def spawn(self, d: Path, version="v2", modules="alpha,beta,gamma", barrier="", fail=False):
        start = time.monotonic()
        args = [str(self.binaries[version]), "-data", str(d), "-upstream", self.provider,
                "-modules", modules]
        if barrier: args += ["-barrier", barrier]
        if fail: args += ["-fail-start"]
        log = open(self.output / f"process-{len(self.children)+1:02d}.log", "wb")
        self.logs.append(log)
        p = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=log, env=ENV)
        self.children.append(p)
        p.dp23_identity = identity(p.pid)
        if fail:
            code = p.wait(timeout=4)
            require(code != 0, "failed candidate unexpectedly started")
            return p, {"pid": p.pid, "exit_code": code, "elapsed_ms": (time.monotonic()-start)*1000}
        require(bool(select.select([p.stdout], [], [], 4)[0]), "host did not emit startup record")
        record = json.loads(p.stdout.readline())
        p.base = "http://" + record["listen"]
        for _ in range(100):
            status, _ = request(p.base, "/health/ready")
            if status == 200: break
            time.sleep(0.01)
        require(status == 200, "host not ready")
        p.startup_ms = (time.monotonic()-start)*1000
        p.record = record
        return p
    def stop(self, p, sig=signal.SIGTERM):
        if p.poll() is None:
            require(identity(p.pid) == p.dp23_identity, "PID identity changed; refusing signal")
            p.send_signal(sig)
        p.wait(timeout=5)
    def peak(self, phase):
        rows = [r for r in self.rows if r["phase"] == phase]
        return {"samples": len(rows), "rss_kib": max((r["rss_kib"] for r in rows), default=0),
                "pss_kib": max((r["pss_kib"] for r in rows), default=0),
                "processes": max((r["processes"] for r in rows), default=0)}
    def state(self, p):
        code, b = request(p.base, "/extensions/v1/alpha/state")
        require(code == 200, f"state read {code}"); return json.loads(b)
    def callback(self, base, key):
        return request(base, "/extensions/v1/alpha/callback?key=" + key,
                       json.dumps({"event": key, "payer": "test-account", "operation": "mock-effect"}, sort_keys=True).encode())
    def work(self, base, key, action="work"):
        return request(base, f"/extensions/v1/alpha/{action}?key=" + key, b"{}")
    def effects(self, key):
        with sqlite3.connect(Provider.db_path) as db:
            return db.execute("SELECT count(*) FROM effects WHERE event_key=?", (key,)).fetchone()[0]
    def wait_barrier(self, d):
        deadline = time.monotonic() + 5
        while not (d / "barrier").exists():
            require(time.monotonic() < deadline, "kill point not reached")
            time.sleep(0.005)
    def profile(self):
        self.phase = "single-host-three-modules"
        p = self.spawn(self.data("profile-one"))
        for name in ("alpha", "beta", "gamma"):
            require(request(p.base, f"/extensions/v1/{name}/ping")[0] == 200, "profile module absent")
        time.sleep(0.4)
        one = memory(p.pid)
        self.stop(p)
        self.phase = "two-hosts-three-modules"
        a = self.spawn(self.data("profile-a"), modules="alpha")
        b = self.spawn(self.data("profile-b"), modules="beta,gamma")
        time.sleep(0.4)
        two = [memory(a.pid), memory(b.pid)]
        before = identity(b.pid)
        self.stop(a, signal.SIGKILL)
        status, _ = request(b.base, "/extensions/v1/beta/ping")
        isolated = status == 200 and identity(b.pid) == before
        self.stop(b)
        self.report["group_profile"] = {"single": one, "split": two,
            "other_group_survived_sigkill": isolated,
            "single_peak": self.peak("single-host-three-modules"),
            "split_peak": self.peak("two-hosts-three-modules"),
            "scope": "empty synthetic module handlers; lower bound on host overhead, not business memory"}
        require(isolated, "selected group stop disturbed other group")
    def stream(self, started):
        req = urllib.request.Request(self.entry + "/extensions/v1/alpha/stream", headers={"Authorization": TOKEN})
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        frames = []; times = []
        with opener.open(req, timeout=5) as response:
            for raw in response:
                line = raw.decode().strip()
                if line.startswith("data: "):
                    frames.append(line[6:]); times.append(time.monotonic()); started.set()
        require(frames == [str(i) for i in range(80)] + ["complete"], "stream lost/reordered frames")
        return {"data_frames": len(frames), "max_gap_ms": max((b-a)*1000 for a,b in zip(times,times[1:]))}
    def rounds(self, count):
        self.phase = "rollout-setup"
        d = self.data("rounds")
        old = self.spawn(d, "v1")
        with Entry.target_lock: Entry.target = old.base
        failures = []; traffic = []; done = threading.Event()
        def ping():
            while not done.is_set():
                try:
                    status, _ = request(self.entry, "/extensions/v1/alpha/ping", timeout=2)
                    traffic.append(status)
                    if status != 200: failures.append(f"status {status}")
                except Exception as error:
                    failures.append(type(error).__name__)
                done.wait(0.025)
        thread = threading.Thread(target=ping, daemon=True); thread.start()
        try:
            for i in range(1, count+1):
                self.phase = f"round-{i:02d}"
                key = f"round-{i:02d}"
                require(self.callback(self.entry, key)[0] == 202, "callback not acknowledged")
                backlog_before = sum(t["state"] != "done" for t in self.state(old)["tasks"].values())
                if i in (3,7):
                    epoch = self.state(old)["owner"]
                    failed, info = self.spawn(d, fail=True)
                    require(Entry.target == old.base and self.state(old)["owner"] == epoch,
                            "failed candidate changed traffic/ownership")
                    require(request(self.entry, "/extensions/v1/alpha/ping")[0] == 200,
                            "failed start interrupted active host")
                    self.report["failed_candidates"].append(dict(info, round=i))
                start = time.monotonic()
                entered = threading.Event()
                stream = self.pool.submit(self.stream, entered)
                require(entered.wait(3), "old stream not established")
                version = "v1" if i in (4,8,10) else "v2"
                fresh = self.spawn(d, version)
                with Entry.target_lock: Entry.target = fresh.base
                require(self.work(self.entry,key)[0] == 200, "task did not recover in new host")
                old_info = identity(old.pid)
                self.stop(old)
                stream_result = stream.result(timeout=5)
                require(old.returncode == 0, "old host failed graceful shutdown")
                require(self.effects(key) == 1, "external effect repeated")
                backlog_after = sum(t["state"] != "done" for t in self.state(fresh)["tasks"].values())
                retired = [p.pid for p in self.children if p is not fresh and p.poll() is None]
                require(not retired, "old process leaked")
                require(identity(os.getpid()) == self.entry_identity, "test entry restarted")
                self.report["rounds"].append({"round": i, "version": version, "old": old_info,
                    "new": identity(fresh.pid), "old_exit_code": old.returncode,
                    "backlog_before": backlog_before, "backlog_after": backlog_after,
                    "retired_alive": len(retired), "new_ready_ms": fresh.startup_ms,
                    "total_handoff_ms": (time.monotonic()-start)*1000,
                    "memory_peak": self.peak(self.phase), "stream": stream_result,
                    "mock_effect_count": self.effects(key)})
                old = fresh
        finally:
            done.set(); thread.join(timeout=3)
            self.stop(old)
        self.report["continuous_requests"] = {"completed": len(traffic), "failures": failures,
                                               "proxy_retries": 0}
        require(not failures, f"visible request failures: {failures}")
    def crashes(self):
        points = ["after_callback_commit", "after_claim_before_start", "after_start_before_effect",
                  "after_effect_before_commit", "after_commit_before_ack"]
        for point in points:
            self.phase = point
            d = self.data(point)
            old = self.spawn(d, "v1", barrier=point)
            key = "crash-" + point
            if point == "after_callback_commit":
                future = self.pool.submit(self.callback, old.base, key)
            else:
                require(self.callback(old.base,key)[0] == 202, "seed callback")
                future = self.pool.submit(self.work, old.base, key)
            self.wait_barrier(d)
            start = time.monotonic()
            self.stop(old, signal.SIGKILL)
            try: future.result(timeout=4)
            except (urllib.error.URLError, ConnectionError, http.client.HTTPException, TimeoutError): pass
            fresh = self.spawn(d, "v2")
            require(self.callback(fresh.base,key)[0] == 202, "callback replay failed")
            state = self.state(fresh)["tasks"][key]["state"]
            if state in ("pending", "done"):
                require(self.work(fresh.base,key)[0] == 200, "safe task recovery failed")
            else:
                require(self.work(fresh.base,key)[0] == 409, "unknown task replayed")
                code, _ = self.work(fresh.base,key,"reconcile")
                require(code == (409 if point == "after_start_before_effect" else 200), "lookup reconciliation")
            task = self.state(fresh)["tasks"][key]
            expected = 0 if point == "after_start_before_effect" else 1
            require(self.effects(key) == expected, "duplicate/lost external effect")
            require(task["state"] == ("uncertain" if expected == 0 else "done"), "wrong durable result")
            self.report["crash_tests"].append({"point": point, "killed": old.pid,
                "recovered": fresh.pid, "mock_effect_count": self.effects(key), "state": task["state"],
                "recovery_check_ms": (time.monotonic()-start)*1000})
            self.stop(fresh)
        # Suspend, replace, reconcile, then resume the OLD process after a new
        # result is durable. It must not overwrite the new owner's task.
        self.phase = "stale-resumed"
        d = self.data("stale-resumed");key="stale-resumed"
        old=self.spawn(d,"v1",barrier="after_effect_before_commit")
        require(self.callback(old.base,key)[0]==202,"seed stale event")
        future=self.pool.submit(self.work,old.base,key)
        self.wait_barrier(d);old.send_signal(signal.SIGSTOP)
        fresh=self.spawn(d,"v2")
        require(self.work(fresh.base,key,"reconcile")[0]==200,"new owner reconciliation")
        before=self.state(fresh)["tasks"][key]
        (d/"resume").write_text("resume")
        old.send_signal(signal.SIGCONT)
        code,_=future.result(timeout=4)
        require(code==409,"stale process result accepted")
        after=self.state(fresh)["tasks"][key]
        require(before==after and self.effects(key)==1,"stale process overwrote new result")
        self.report["crash_tests"].append({"point":"stale-resumed","old_pid":old.pid,
            "new_pid":fresh.pid,"stale_http_status":code,"new_result_unchanged":True,"mock_effect_count":1})
        self.stop(old);self.stop(fresh)
        self.phase = "corrupt-callback-storage"
        d=self.data("corrupt-callback-storage");p=self.spawn(d)
        (d/"tasks.json").write_text("{broken-json")
        code,_=self.callback(p.base,"corrupt-callback")
        require(code==503 and self.effects("corrupt-callback")==0,"callback acknowledged without persistence")
        self.report["crash_tests"].append({"point":"corrupt-callback-storage","http_status":code,"mock_effect_count":0})
        self.stop(p)
    def finish(self):
        self.stopping.set()
        if hasattr(self,"monitor"):self.monitor.join(timeout=2)
        for p in self.children:
            if p.poll() is None:
                # Only child objects created in this run are eligible for signals.
                try:p.send_signal(signal.SIGCONT);self.stop(p,signal.SIGKILL)
                except ProcessLookupError:pass
        for log in self.logs:log.close()
        for s in self.servers:s.shutdown();s.server_close()
        self.pool.shutdown(wait=True,cancel_futures=True)
        self.report["remaining_children"]=[p.pid for p in self.children if p.poll() is None]
        self.report["process_count_started"]=len(self.children)
        (self.output/"results.json").write_text(json.dumps(self.report,indent=2)+"\n")
        if self.rows:
            with (self.output/"memory.csv").open("w") as f:
                w=csv.DictWriter(f,fieldnames=list(self.rows[0]));w.writeheader();w.writerows(self.rows)
        self.temp.cleanup()


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output",type=Path,default=Path(__file__).resolve().parent / "local-evidence")
    parser.add_argument("--rounds",type=int,default=10)
    args=parser.parse_args()
    require(10<=args.rounds<=20,"use 10..20 bounded local update rounds")
    require(os.name=="posix" and Path("/proc/self/status").exists(),"Linux /proc is required")
    args.output.mkdir(parents=True,exist_ok=True)
    root=Path(__file__).resolve().parents[3]
    h=Harness(root,args.output.resolve())
    try:
        print("build", flush=True); h.build()
        print("profiles", flush=True); h.start_services();h.profile()
        print("rollout rounds", flush=True); h.rounds(args.rounds)
        print("crash tests", flush=True); h.crashes()
        h.report["status"]="passed_with_explicit_unverified_integration_gates"
        print(json.dumps({"rounds":len(h.report["rounds"]),"fault_scenarios":len(h.report["crash_tests"]),
            "continuous_requests":h.report["continuous_requests"],"scope":h.report["scope"]}))
    except BaseException as error:
        h.report["status"]="failed";h.report["error"]=repr(error)
        raise
    finally:h.finish()

if __name__=="__main__":main()
