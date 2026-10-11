"""Evidence recording. A test's absence or skip cannot become PASS."""
from __future__ import annotations

import hashlib
import io
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import time
import unittest

from .config import BASELINE, wire
from .client import private_write

STATUSES = {"PASS", "FAIL", "BLOCKED", "NOT RUN"}


def source_info(root: Path) -> dict:
    sha, dirty = None, None
    try:
        if (root / ".git").exists():
            result = subprocess.run(["git", "rev-parse", "HEAD"], cwd=root, capture_output=True, text=True, timeout=3, check=True)
            candidate = result.stdout.strip()
            if len(candidate) == 40 and all(c in "0123456789abcdef" for c in candidate):
                sha = candidate
            dirty = bool(subprocess.run(["git", "status", "--porcelain"], cwd=root, capture_output=True, text=True,
                                        timeout=3, check=True).stdout.strip())
    except (OSError, subprocess.SubprocessError):
        pass
    digest = hashlib.sha256()
    paths = list((root / "tests/mocklab").glob("*.py")) + list((root / "tests/mocklab").glob("*.json")) + [root / "scripts/mocklab.py"]
    paths += list((root / "apps/lmm-extensions/internal/testkit/mocklab").glob("*.go"))
    paths += list((root / "apps/lmm-extensions/internal/modules").glob("*/mocklab_test.go"))
    for path in sorted(paths):
        if path.is_file():
            digest.update(str(path.relative_to(root)).encode() + b"\0" + path.read_bytes())
    return {"actual_repository_sha": sha, "working_tree_dirty": dirty, "reviewed_baseline_sha": BASELINE,
            "patch_code_sha256": digest.hexdigest(), "sha_note": "null means this is an additions-only patch tree, not a checked-out repository"}


def isolated_network() -> bool:
    # Kernel-observed network namespace, not trust in an environment flag.
    names = {name for _, name in socket.if_nameindex()}
    return names == {"lo"}


def row(ident, layer, status, started=None, **evidence):
    if status not in STATUSES:
        raise ValueError("unknown test status")
    return {"scenario_id": ident, "layer": layer, "status": status,
            "duration_ms": round((time.monotonic() - started) * 1000, 3) if started is not None else 0,
            "evidence": evidence}


class RecordingResult(unittest.TextTestResult):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.rows = []
        self.started = {}
        self.failed_subtests = {}

    def startTest(self, test):
        self.started[test.id()] = time.monotonic()
        super().startTest(test)

    def evidence(self, test):
        runs=[]
        for lab in getattr(test,"mock_runs",[]):
            state=lab.snapshot()
            runs.append({"run_id":lab.run_id,"seed":lab.config.seed,"generation":lab.generation,
                         "virtual_ms":state["virtual_ms"],"active":state["active"],"instances":state["instances"]})
        return {"test_runs":runs}

    def addSuccess(self, test):
        self.rows.append(row(test.id(), "mock-component", "PASS", self.started[test.id()], **self.evidence(test)))
        super().addSuccess(test)

    def addFailure(self, test, err):
        self.rows.append(row(test.id(), "mock-component", "FAIL", self.started[test.id()], failure=self._exc_info_to_string(err, test)[-8000:], **self.evidence(test)))
        super().addFailure(test, err)

    def addError(self, test, err):
        self.addFailure(test, err)

    def addSkip(self, test, reason):
        self.rows.append(row(test.id(), "mock-component", "NOT RUN", self.started[test.id()], reason=reason))
        super().addSkip(test, reason)

    def addSubTest(self, test, subtest, err):
        if err is not None:
            self.rows.append(row(subtest.id(), "mock-component", "FAIL", self.started[test.id()], failure=self._exc_info_to_string(err, test)[-8000:], **self.evidence(test)))
        super().addSubTest(test, subtest, err)


def selftests():
    suite = unittest.defaultTestLoader.loadTestsFromName("mocklab.test_mocklab")
    buffer = io.StringIO()
    result = unittest.TextTestRunner(stream=buffer, verbosity=2, resultclass=RecordingResult).run(suite)
    rows = result.rows
    if not rows:
        rows = [row("mocklab.selftests", "mock-component", "FAIL", reason="no executable tests found")]
    return rows, buffer.getvalue()


def run_process(command: list[str], *, cwd: Path, env: dict, timeout: int, output: Path) -> tuple[int | None, str]:
    """Own one process group; cap log bytes and time; terminate only that group."""
    started = time.monotonic()
    process = None
    fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(fd, "wb") as log:
            process = subprocess.Popen(command, cwd=cwd, env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
            while process.poll() is None:
                if time.monotonic() - started > timeout:
                    raise TimeoutError("process safety deadline exceeded")
                if output.stat().st_size > 4 * 1024 * 1024:
                    raise ValueError("process log limit exceeded")
                time.sleep(0.02)
            if output.stat().st_size > 4 * 1024 * 1024:
                return process.returncode, "log limit exceeded"
            return process.returncode, ""
    except (OSError, TimeoutError, ValueError) as exc:
        return None, str(exc)
    finally:
        if process is not None and process.poll() is None:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=2)


def go_adapters(root: Path, manifest: Path, output: Path, enabled=True) -> list[dict]:
    import shutil
    domains = [("payments", ["TestMocklabStripeAdapter", "TestMocklabEPayAdapter"]),
               ("toolmarket", ["TestMocklabMCPAdapter"])]
    all_rows = []
    for domain, required in domains:
        start = time.monotonic()
        ident = "real-adapter." + domain
        if not enabled:
            all_rows.append(row(ident, "production-adapter", "NOT RUN", reason="explicit --skip-adapters"))
            continue
        if not isolated_network():
            all_rows.append(row(ident, "production-adapter", "BLOCKED", reason="run isolated: actual application tests require a kernel network namespace with only lo"))
            continue
        module = root / "apps/lmm-extensions"
        if not (module / "go.mod").is_file() or not (module / "internal/modules" / domain / "mocklab_test.go").is_file():
            all_rows.append(row(ident, "production-adapter", "BLOCKED", reason="complete WIP checkout and applied test patch are required"))
            continue
        go = shutil.which("go")
        if go is None:
            all_rows.append(row(ident, "production-adapter", "BLOCKED", reason="Go toolchain missing"))
            continue
        # Do not inherit database, service, payment, cloud or proxy credentials.
        env = {k: os.environ[k] for k in ("PATH", "HOME", "TMPDIR", "GOCACHE", "GOPATH") if k in os.environ}
        env.update(GOTOOLCHAIN="local", GOPROXY="off", GOSUMDB="off", GOENV="off", GOWORK="off",
                   LMM_MOCKLAB_MANIFEST=str(manifest.resolve()))
        command = [go, "test", "-mod=readonly", "-race", "-count=1", "-timeout=40s", "-json", "-run", "^TestMocklab", "./internal/modules/" + domain]
        log = output / (domain + ".go-test.jsonl")
        code, error = run_process(command, cwd=module, env=env, timeout=60, output=log)
        data = log.read_text(errors="replace")[:4 * 1024 * 1024]
        tests = {}
        for line in data.splitlines():
            try:
                event = json.loads(line)
            except ValueError:
                continue
            if event.get("Test") in required and event.get("Action") in {"pass", "fail", "skip"}:
                tests[event["Test"]] = event["Action"]
        if code == 0 and all(tests.get(name) == "pass" for name in required):
            status = "PASS"
        elif code == 0 and any(tests.get(name) == "skip" for name in required):
            status, error = "NOT RUN", "adapter tests were skipped"
        elif code == 0:
            status, error = "FAIL", "command succeeded without the required tests passing"
        elif any(text in data for text in ("requires go >=", "module lookup disabled", "cannot find module", "missing go.sum entry")):
            status, error = "BLOCKED", "offline dependencies or required toolchain unavailable"
        else:
            status = "FAIL"
        all_rows.append(row(ident, "production-adapter", status, start, command=command, exit_code=code,
                            tests=tests, reason=error, log=log.name, log_sha256=hashlib.sha256(log.read_bytes()).hexdigest()))
    return all_rows


def go_transport(root: Path, output: Path) -> dict:
    """Run only our stdlib transport package, not the production Go module."""
    import shutil
    start=time.monotonic()
    go=shutil.which("go")
    if go is None:
        return row("go.test-transport", "test-transport", "BLOCKED", reason="Go toolchain missing")
    if not isolated_network():
        return row("go.test-transport", "test-transport", "BLOCKED", reason="use isolated command for socket tests")
    env={k:os.environ[k] for k in ("PATH","HOME","TMPDIR","GOCACHE","GOPATH") if k in os.environ}
    env.update(GO111MODULE="off",GOTOOLCHAIN="local",GOPROXY="off",GOSUMDB="off",GOENV="off",GOWORK="off")
    command=[go,"test","-race","-count=1","-timeout=20s","-json","."]
    log=output/"test-transport.go-test.jsonl"
    code,error=run_process(command,cwd=root/"apps/lmm-extensions/internal/testkit/mocklab",env=env,timeout=60,output=log)
    tests={}
    for line in log.read_text(errors="replace").splitlines():
        try:
            event=json.loads(line)
        except ValueError:
            continue
        if event.get("Test") and event.get("Action") in {"pass","fail","skip"}:
            tests[event["Test"]]=event["Action"]
    required={"TestTransportMapsOnlyDeclaredHost","TestTransportDoesNotFollowRedirect","TestManifestRejectsNonLoopbackAndCrossRun"}
    status="PASS" if code==0 and all(tests.get(name)=="pass" for name in required) else "FAIL"
    return row("go.test-transport","test-transport",status,start,tests=tests,exit_code=code,reason=error,command=command,
               log=log.name,log_sha256=hashlib.sha256(log.read_bytes()).hexdigest(),
               scope="standalone stdlib test helper only; no production module build")


def save_report(output: Path, metadata: dict, rows: list[dict], *, requirements=None):
    counts = {status: sum(r["status"] == status for r in rows) for status in ("PASS", "FAIL", "BLOCKED", "NOT RUN")}
    report = {"schema_version": 1, **metadata, "summary": counts, "results": rows,
              "acceptance_requirements": requirements or [],
              "warning": "Mock-component and adapter PASS do not prove application ledger correctness."}
    private_write(output / "report.json", json.dumps(report, ensure_ascii=False, indent=2).encode() + b"\n")
    lines = ["# Mocklab test report", "", "These results do not certify the application ledger.", "",
             "Actual repository SHA: `" + str(metadata.get("source", {}).get("actual_repository_sha")) + "`", "",
             "Network namespace has only lo: `" + str(metadata.get("network_isolated")) + "`", "",
             " | ".join(f"{k}: {v}" for k,v in counts.items()), "", "| Scenario | Layer | Result | Evidence |", "|---|---|---|---|"]
    for r in rows:
        reason = str(r["evidence"].get("reason", r["evidence"].get("log", ""))).replace("|", "/").replace("\n", " ")
        lines.append(f'| {r["scenario_id"]} | {r["layer"]} | **{r["status"]}** | {reason[:240]} |')
    private_write(output / "report.md", ("\n".join(lines) + "\n").encode())
    return 1 if counts["FAIL"] else 2 if counts["BLOCKED"] or counts["NOT RUN"] else 0
