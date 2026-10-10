"""Isolated node fixtures, NOT the Rust/Go services and NOT a deployment adapter."""
from __future__ import annotations

import contextlib
import hashlib
import json
from pathlib import Path
import shutil
import sqlite3
import time

from contract import API, Rejected, Unavailable, canonical, compatible, digest, require


@contextlib.contextmanager
def connection(path: Path):
    db = sqlite3.connect(path, timeout=3)
    db.execute("PRAGMA busy_timeout=3000")
    db.execute("PRAGMA synchronous=FULL")
    try:
        yield db
        db.commit()
    except BaseException:
        db.rollback()
        raise
    finally:
        db.close()


class Fixture:
    def __init__(self, root: Path):
        self.root = root.resolve()
        require((self.root / "ISOLATED_FIXTURE").is_file(), "not an initialized isolated fixture")

    def db_path(self, node: str) -> Path:
        require(node.isalnum() or all(c.isalnum() or c in "-_" for c in node), "invalid fixture node")
        path = self.root / node / "runtime.sqlite"
        require(path.is_file(), "fixture node does not exist")
        return path

    def inspect(self, node: str) -> dict:
        with connection(self.db_path(node)) as db:
            state = json.loads(db.execute("SELECT body FROM node_state").fetchone()[0])
        if state["offline"]:
            raise Unavailable("fixture network is disconnected")
        return state

    def change(self, node: str, **changes) -> None:
        """Test-only fault injection; not exposed by the release CLI."""
        with connection(self.db_path(node)) as db:
            db.execute("BEGIN IMMEDIATE")
            state = json.loads(db.execute("SELECT body FROM node_state").fetchone()[0])
            state.update(changes)
            db.execute("UPDATE node_state SET body=?", (canonical(state),))

    def invoke(self, op: dict) -> dict:
        require(op["api"] == API, "unknown request API")
        request = dict(op)
        request.pop("operation_id")
        require(digest(request) == op["operation_id"], "operation payload was changed")
        ack_lost = False
        with connection(self.db_path(op["node"])) as db:
            db.execute("BEGIN IMMEDIATE")
            state = json.loads(db.execute("SELECT body FROM node_state").fetchone()[0])
            if state["offline"] or state.get("fault") == "before:" + op["action"]:
                raise Unavailable("fixture connection failed before receipt")
            result = {"api": API, "operation_id": op["operation_id"], "epoch": op["epoch"],
                      "status": "ok", "evidence": {}}
            try:
                require(state["cluster"] == op["cluster"], "cluster identity mismatch")
                f = state["fence"]
                require(f["authority"] in {None, op["authority"]}, "another authority owns this node")
                require(op["epoch"] >= f["epoch"], "stale execution epoch")
                if op["action"] == "fence":
                    if op["epoch"] == f["epoch"]:
                        require(f["release_id"] == op["release_id"] and f["plan_digest"] == op["plan_digest"],
                                "same epoch cannot name another release")
                    state["fence"] = {k: op[k] for k in ("authority", "epoch", "release_id", "plan_digest")}
                    db.execute("DELETE FROM receipts WHERE epoch < ?", (op["epoch"] - 1,))
                else:
                    require(op["epoch"] == f["epoch"] and op["release_id"] == f["release_id"]
                            and op["plan_digest"] == f["plan_digest"], "node has not accepted this release fence")
                prior = db.execute("SELECT body FROM receipts WHERE id=?", (op["operation_id"],)).fetchone()
                if prior is not None:
                    return json.loads(prior[0])
                if op["action"] != "fence":
                    self._action(db, state, op, result)
            except Rejected as exc:
                result.update(status="rejected", reason=str(exc))
            if result["status"] != "wait":
                db.execute("INSERT OR IGNORE INTO receipts VALUES(?,?,?)",
                           (op["operation_id"], op["epoch"], canonical(result)))
                if result["status"] == "ok":
                    db.execute("INSERT OR IGNORE INTO effects VALUES(?,?,?)",
                               (op["operation_id"], op["action"], op["epoch"]))
            if state.get("fault") == "after:" + op["action"]:
                state["fault"] = None
                ack_lost = True
            db.execute("UPDATE node_state SET body=?", (canonical(state),))
        if ack_lost:
            raise Unavailable("fixture side effect committed; acknowledgement was lost")
        return result

    def _action(self, db, state: dict, op: dict, reply: dict) -> None:
        target, previous = op["target"], op["previous"]
        component = target["component"]
        current = state["components"][component]
        tid, pid = digest(target), digest(previous)
        live_ids = {digest(m) for m in current["live"]}
        action = op["action"]
        e = reply["evidence"]
        e.update(artifact_digest=target["artifact"]["digest"], config_digest=target["config"]["digest"],
                 capabilities=target["provides"], business_ready=not state["unready"])
        if action == "space":
            available = min(state["free_bytes"], shutil.disk_usage(self.root).free)
            need = (0 if tid in live_ids else target["artifact"]["download_bytes"]
                    + target["artifact"]["unpacked_bytes"]) + op["policy"]["reserve_bytes"]
            e.update(available_bytes=available, required_bytes=need)
            if available < need or (tid not in live_ids and
                    state["memory_headroom"] < target["artifact"]["peak_memory_bytes"]):
                reply.update(status="wait", reason="insufficient disk or update memory; old streams retained")
            elif len(live_ids | {tid}) > op["policy"]["max_live_versions"]:
                reply.update(status="wait", reason="coexisting version limit")
        elif action == "pull":
            artifact = self.root / "artifacts" / (target["artifact"]["digest"].split(":")[1] + ".blob")
            require(artifact.is_file(), "precompiled fixture artifact is missing")
            sha = hashlib.sha256()
            with artifact.open("rb") as stream:
                for chunk in iter(lambda: stream.read(65536), b""):
                    sha.update(chunk)
            require("sha256:" + sha.hexdigest() == target["artifact"]["digest"], "artifact digest mismatch")
            e["verified"] = True
        elif action == "prepare":
            # Reserve again at the point of use, not only during the earlier precheck.
            if tid not in live_ids:
                require(len(live_ids) < op["policy"]["max_live_versions"], "live version limit changed")
                need = target["artifact"]["unpacked_bytes"]
                if (state["free_bytes"] < need + op["policy"]["reserve_bytes"] or
                        state["memory_headroom"] < target["artifact"]["peak_memory_bytes"]):
                    reply.update(status="wait", reason="resources changed before prepare")
                    return
                state["free_bytes"] -= need
                state["memory_headroom"] -= target["artifact"]["peak_memory_bytes"]
                current["live"].append(target)
            if component == "web" and not any(digest(m) == tid for m in state["web_clients"]):
                state["web_clients"].append(target)
            state["assets"] = sorted(set(state["assets"]) | set(target["web"]["assets"]))
        elif action == "ready":
            require(tid in live_ids, "candidate has not been prepared")
            if state["unready"]:
                reply.update(status="wait", reason="process alive but business dependency is unavailable")
            if state["missing_capability"] in target["provides"]:
                reply.update(status="rejected", reason="advertised capability is not actually registered")
        elif action == "switch":
            require(tid in live_ids and not state["unready"], "candidate is not ready at traffic switch")
            require(digest(current["active"]) in {pid, tid}, "traffic revision changed outside this plan")
            # Recheck all peers for split-role clusters, not only local processes.
            # Real adapters obtain this authenticated snapshot through DP-24.
            versions = {}
            peers = [state]
            for directory in sorted(self.root.iterdir()):
                if directory.name != op["node"] and (directory / "runtime.sqlite").is_file():
                    peers.append(self.inspect(directory.name))
            for peer in peers:
                for name, data in peer["components"].items():
                    versions.setdefault(name, []).extend(data["live"])
                versions.setdefault("web", []).extend(peer["web_clients"])
            for peer in peers:
                compatible(versions, peer["schema"], peer["data_states"])
            current["active"] = target
            state["switched_at"][tid] = time.time()
        elif action == "observe":
            if state["bad_digest"] == tid:
                reply.update(status="failed", reason="fixture business observation failed")
                return
            money = state["money_state"]
            if money == "pending":
                reply.update(status="wait", reason="settlement not yet durable")
                return
            require(money == "settled", "unknown money state; never interpret it as success")
            elapsed = max(0, time.time() - state["switched_at"].get(tid, time.time()))
            e.update(samples=state["samples"], money_state=money, window_seconds=elapsed)
            if state["samples"] < op["policy"]["minimum_samples"] or elapsed < op["policy"]["observe_seconds"]:
                reply.update(status="wait", reason="business observation window/samples not met")
        elif action in {"drain", "finish"}:
            count, unsettled = db.execute(
                "SELECT count(*),coalesce(sum(unsettled),0) FROM streams WHERE component=? AND version=?",
                (component, pid)).fetchone()
            e.update(inflight=count, unsettled=unsettled)
            if count or unsettled:
                reply.update(status="wait", reason="accepted old work still running; no forced termination")
                return
            if action == "finish" and pid != tid and pid in live_ids:
                current["live"] = [m for m in current["live"] if digest(m) != pid]
                state["free_bytes"] += previous["artifact"]["unpacked_bytes"]
                state["memory_headroom"] += previous["artifact"]["peak_memory_bytes"]
                # Old page assets and sessions are not removed. DP-28 must track pins and quotas.
        else:
            raise Rejected("unknown lifecycle operation")


def make_manifest(component: str, version: int, root: Path) -> dict:
    data = (f"ISOLATED DP-29 FIXTURE, NOT AN OCI IMAGE: {component}/{version}\n").encode()
    sha = "sha256:" + hashlib.sha256(data).hexdigest()
    (root / "artifacts").mkdir(exist_ok=True, parents=True)
    (root / "artifacts" / (sha.split(":")[1] + ".blob")).write_bytes(data)
    return {"api": API, "component": component, "version": "fixture-" + str(version),
            "artifact": {"image": "fixture.invalid/" + component + "@" + sha, "digest": sha,
                         "platform": "linux/amd64", "download_bytes": len(data),
                         "unpacked_bytes": 4096, "peak_memory_bytes": 1048576},
            "config": {"version": version, "accepts": [1, 1000],
                       "digest": digest({"fixture-config": version}), "required_fields": ["listen"],
                       "understood_fields": ["listen", "optional-label"]},
            "protocol": {"major": 1, "emit": 1, "accepts": [1, 2]},
            "schema": {"read": [1, 2], "write": [1, 2]},
            "provides": ["identity"] if component == "rust-core" else ["fixture-read"],
            "requires": {"rust-core": ["identity"]} if component == "go-extensions" else {},
            "understood_states": ["pending", "settled"],
            "web": {"api_emit": 1, "api_accepts": [1, 2], "assets": [f"{component}-{version}.js"],
                    "retain_seconds": 86400}, "critical_fields": []}


def create(root: Path, nodes: int = 3) -> tuple[dict, dict]:
    require(not root.exists(), "fixture already exists")
    root.mkdir(mode=0o700, parents=True)
    (root / "ISOLATED_FIXTURE").write_text("No real services, credentials or funds.\n")
    components = {c: make_manifest(c, 1, root) for c in ("rust-core", "go-extensions", "web")}
    names = ["n" + str(i + 1) for i in range(nodes)]
    for node in names:
        directory = root / node
        directory.mkdir()
        state = {"node": node, "cluster": "dp29-lab", "environment": "isolated", "transport": "fixture-local",
                 "platform": "linux/amd64", "schema": 1, "data_states": ["pending", "settled"],
                 "web_clients": [components["web"]],
                 "components": {c: {"active": m, "live": [m]} for c, m in components.items()},
                 "offline": False, "fault": None, "unready": False, "missing_capability": "",
                 "bad_digest": "", "money_state": "settled", "samples": 3,
                 "free_bytes": 32 * 1048576, "memory_headroom": 16 * 1048576,
                 "assets": [a for m in components.values() for a in m["web"]["assets"]], "switched_at": {},
                 "fence": {"authority": None, "epoch": 0, "release_id": None, "plan_digest": None}}
        with connection(directory / "runtime.sqlite") as db:
            db.executescript("""
                CREATE TABLE node_state(body TEXT NOT NULL);
                CREATE TABLE receipts(id TEXT PRIMARY KEY, epoch INTEGER, body TEXT NOT NULL);
                CREATE TABLE effects(id TEXT PRIMARY KEY, action TEXT, epoch INTEGER);
                CREATE TABLE streams(id TEXT PRIMARY KEY, component TEXT, version TEXT, unsettled INTEGER);
            """)
            db.execute("INSERT INTO node_state VALUES(?)", (canonical(state),))
        with connection(directory / "business.sqlite") as db:
            db.executescript("""
                CREATE TABLE balances(id TEXT PRIMARY KEY, amount INTEGER NOT NULL);
                INSERT INTO balances VALUES('alice',10000);
                CREATE TABLE ledger(request_id TEXT PRIMARY KEY, amount INTEGER NOT NULL);
                CREATE TABLE sessions(id TEXT PRIMARY KEY, user_id TEXT NOT NULL);
                INSERT INTO sessions VALUES('fixture-session','alice');
            """)
    inventory = {"api": API, "cluster": "dp29-lab", "environment": "isolated", "nodes": names,
                 "adapter": {"kind": "fixture", "root": str(root.resolve())},
                 "policy": {"max_parallel_nodes": 1, "max_live_versions": 2, "observe_seconds": 0,
                            "minimum_samples": 3, "reserve_bytes": 1048576, "automatic_rollback": True}}
    return inventory, components
