#!/usr/bin/env python3
"""On-demand, isolated DP-29 release controller. Python standard library only."""
from __future__ import annotations

import argparse
import contextlib
import fcntl
import json
import os
from pathlib import Path
import sqlite3
import sys
import uuid

from contract import (API, COMPONENTS, IDENT, Rejected, Unavailable, canonical,
                      compatible, digest, integer, manifest, require)

PHASES = ("space", "pull", "prepare", "ready", "switch", "observe", "drain", "finish")
TERMINAL = {"completed", "rolled_back", "superseded"}
MAX_PLANS = 64  # Backpressure, not silent deletion of evidence or rollback pins.


def load(path: Path, limit: int = 1048576) -> dict:
    with path.open("rb") as stream:
        raw = stream.read(limit + 1)
    require(len(raw) <= limit, "input exceeds byte limit")
    try:
        value = json.loads(raw)
    except (ValueError, UnicodeError) as exc:
        raise Rejected("invalid JSON") from exc
    require(isinstance(value, dict), "expected JSON object")
    return value


class Store:
    def __init__(self, root: Path):
        self.root = root.resolve()
        self.db = self.root / "control.sqlite"
        require(self.db.is_file(), "state is absent; use init explicitly, never reconstruct it automatically")
        with self.connect(readonly=True) as db:
            require(db.execute("PRAGMA user_version").fetchone()[0] == 1,
                    "unknown control schema; this executable must not rewrite it")

    @contextlib.contextmanager
    def connect(self, readonly: bool = False):
        db = sqlite3.connect(self.db.as_uri() + ("?mode=ro" if readonly else "?mode=rw"),
                             uri=True, timeout=3)
        db.row_factory = sqlite3.Row
        db.execute("PRAGMA busy_timeout=3000")
        if not readonly:
            db.execute("PRAGMA synchronous=FULL")
            db.execute("PRAGMA max_page_count=4096")
        try:
            yield db
            if not readonly:
                db.commit()
        except BaseException:
            db.rollback()
            raise
        finally:
            db.close()

    @contextlib.contextmanager
    def locked(self):
        # One authority host, multiple operators/processes. Not an NFS/distributed lock.
        with (self.root / "control.lock").open("a") as lock:
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError as exc:
                raise Rejected("another controller owns this step; retry with a fresh revision") from exc
            try:
                yield
            finally:
                fcntl.flock(lock, fcntl.LOCK_UN)

    def meta(self) -> dict:
        with self.connect(readonly=True) as db:
            return json.loads(db.execute("SELECT body FROM metadata").fetchone()[0])

    def get(self, plan_id: str) -> dict:
        with self.connect(readonly=True) as db:
            row = db.execute("SELECT body FROM plans WHERE id=?", (plan_id,)).fetchone()
        require(row is not None, "unknown release ID")
        return json.loads(row[0])

    def save(self, p: dict, meta: dict | None = None, *, insert: bool = False) -> None:
        body = canonical(p)
        require(len(body.encode()) < 196608, "plan journal is full; do not drop active evidence")
        with self.connect() as db:
            db.execute("BEGIN IMMEDIATE")
            if insert:
                require(db.execute("SELECT count(*) FROM plans").fetchone()[0] < MAX_PLANS,
                        "control history limit reached; archive/review before accepting new plans")
                db.execute("INSERT INTO plans VALUES(?,?,?)", (p["id"], p["revision"], body))
            else:
                db.execute("UPDATE plans SET revision=?,body=? WHERE id=?",
                           (p["revision"], body, p["id"]))
            if meta is not None:
                db.execute("UPDATE metadata SET body=?", (canonical(meta),))

    def status(self, plan_id: str | None = None) -> dict:
        if plan_id:
            return self.get(plan_id)
        with self.connect(readonly=True) as db:
            rows = db.execute("SELECT body FROM plans ORDER BY rowid").fetchall()
        m = self.meta()
        return {"api": API, "authority": m["authority"], "active": m["active"],
                "epoch": m["epoch"], "plans": [json.loads(row[0]) for row in rows]}


def init(root: Path, inventory: dict) -> Store:
    require(inventory.get("api") == API and inventory.get("environment") == "isolated",
            "this prototype accepts only explicitly isolated inventories")
    require(bool(IDENT.fullmatch(inventory.get("cluster", ""))), "invalid cluster ID")
    nodes = inventory.get("nodes", [])
    require(isinstance(nodes, list) and 1 <= len(nodes) <= 5 and len(set(nodes)) == len(nodes)
            and all(isinstance(n, str) and IDENT.fullmatch(n) for n in nodes), "expected 1..5 unique nodes")
    targets = inventory.get("targets", {c: nodes for c in COMPONENTS})
    require(isinstance(targets, dict) and set(targets) == COMPONENTS, "target groups must name all components")
    for selected in targets.values():
        require(isinstance(selected, list) and selected and len(set(selected)) == len(selected)
                and set(selected) <= set(nodes), "invalid component target group")
    require(inventory.get("adapter", {}).get("kind") in {"fixture", "hooks"}, "unknown adapter")
    policy = inventory.get("policy", {})
    require(policy.get("max_parallel_nodes") == 1 and policy.get("max_live_versions") == 2,
            "prototype supports one updating node and at most two live versions per component")
    require(integer(policy.get("observe_seconds"), 0, 3600)
            and integer(policy.get("minimum_samples"), 1, 10000)
            and integer(policy.get("reserve_bytes"), 1), "invalid observation/space policy")
    require(type(policy.get("automatic_rollback")) is bool, "automatic_rollback must be explicit")
    if inventory["adapter"]["kind"] == "hooks":
        require(policy["observe_seconds"] >= 30, "real hooks require a nonzero observation window")
    root = root.resolve()
    try:
        root.mkdir(mode=0o700, parents=False, exist_ok=False)
    except FileExistsError as exc:
        raise Rejected("state directory already exists; refusing to overwrite authority") from exc
    previous = os.umask(0o077)
    try:
        db = sqlite3.connect(root / "control.sqlite")
        db.executescript("""
            PRAGMA synchronous=FULL;
            PRAGMA user_version=1;
            PRAGMA max_page_count=4096;
            CREATE TABLE metadata(body TEXT NOT NULL);
            CREATE TABLE plans(id TEXT PRIMARY KEY, revision INTEGER NOT NULL, body TEXT NOT NULL);
        """)
        db.execute("INSERT INTO metadata VALUES(?)", (canonical({
            "authority": str(uuid.uuid4()), "epoch": 0, "active": None, "latest": {},
            "inventory": inventory, "inventory_digest": digest(inventory)}),))
        db.commit()
        db.close()
        (root / "control.lock").touch(mode=0o600)
        fd = os.open(root, os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
    finally:
        os.umask(previous)
    return Store(root)


def gc_history(store: Store, keep: int, expected_epoch: int) -> dict:
    require(integer(keep, 3, MAX_PLANS), "keep must be 3..64")
    with store.locked():
        meta = store.meta()
        require(meta["epoch"] == expected_epoch, "stale epoch; inspect status before history cleanup")
        with store.connect() as db:
            db.execute("BEGIN IMMEDIATE")
            rows = [json.loads(r[0]) for r in db.execute("SELECT body FROM plans ORDER BY rowid")]
            protected = {p["id"] for p in rows[-keep:]}
            protected.update(meta["latest"].values())
            protected.add(meta["active"])
            removed = [p["id"] for p in rows if p["id"] not in protected
                       and p["stage"] in TERMINAL and p["pending"] is None]
            for plan_id in removed:
                db.execute("DELETE FROM plans WHERE id=?", (plan_id,))
            # Keep only a bounded summary. Export test/audit evidence before explicit cleanup.
            meta["last_history_gc"] = {"count": len(removed), "ids_digest": digest(removed)}
            db.execute("UPDATE metadata SET body=?", (canonical(meta),))
        return {"removed": removed, "kept": len(rows) - len(removed), "epoch": meta["epoch"],
                "note": "control history only; node receipts, assets, business data and sessions are untouched"}


class Controller:
    def __init__(self, store: Store, adapter=None):
        self.store = store
        self.inventory = store.meta()["inventory"]
        if adapter is None:
            if self.inventory["adapter"]["kind"] == "fixture":
                from fixture import Fixture
                adapter = Fixture(Path(self.inventory["adapter"]["root"]))
            else:
                from hooks import Hooks
                adapter = Hooks(self.inventory["adapter"].get("bindings", {}))
        self.adapter = adapter

    def plan(self, release: dict) -> dict:
        release = json.loads(canonical(manifest(release)))
        with self.store.locked():
            # Validate capacity before changing the previous queued request.
            with self.store.connect(readonly=True) as db:
                rows = db.execute("SELECT body FROM plans").fetchall()
            require(len(rows) < MAX_PLANS, "control history limit reached")
            p = {"api": API, "id": str(uuid.uuid4()), "revision": 1, "stage": "queued",
                 "manifest": release, "manifest_digest": digest(release), "epoch": None,
                 "nodes": self.inventory.get("targets", {}).get(release["component"], self.inventory["nodes"]),
                 "cluster_nodes": self.inventory["nodes"], "baseline": {}, "work": {}, "fenced": [],
                 "pending": None, "direction": "deploy", "note": None, "events": []}
            # Insertion and coalescing must be one transaction: never lose both plans on crash.
            with self.store.connect() as db:
                db.execute("BEGIN IMMEDIATE")
                for row in rows:
                    old = json.loads(row[0])
                    if old["stage"] == "queued" and old["manifest"]["component"] == release["component"]:
                        old["stage"] = "superseded"
                        old["revision"] += 1
                        old["note"] = "replaced before any deployment action by " + p["id"]
                        db.execute("UPDATE plans SET revision=?,body=? WHERE id=?",
                                   (old["revision"], canonical(old), old["id"]))
                db.execute("INSERT INTO plans VALUES(?,?,?)", (p["id"], 1, canonical(p)))
            return p

    def inspect_all(self) -> dict:
        return {node: self.adapter.inspect(node) for node in self.inventory["nodes"]}

    def check_target(self, p: dict, live: dict, *, rollback: bool = False) -> None:
        component = p["manifest"]["component"]
        # Check the full cluster cross-product, not just the peers on the changed node.
        versions = {c: [] for c in COMPONENTS}
        for node, state in live.items():
            require(state.get("environment") == "isolated", "node isolation is not confirmed")
            require(state.get("cluster") == self.inventory["cluster"], "wrong node cluster")
            require(state.get("node") == node, "wrong node identity")
            require(state.get("transport") in {"fixture-local", "dp24-authenticated"},
                    "missing authenticated transport evidence")
            require(isinstance(state.get("web_clients"), list), "retained browser API contracts are missing")
            versions["web"].extend(state["web_clients"])
            for c, running in state["components"].items():
                for m in running["live"]:
                    versions[c].append(m)
            if node in p["nodes"]:
                require(component in state["components"], "target node does not host this component")
                target = p["baseline"][node] if rollback else p["manifest"]
                require(state["platform"] == target["artifact"]["platform"], "architecture mismatch")
                versions[component].append(target)
        for state in live.values():
            compatible(versions, state["schema"], state["data_states"])

    def _revision(self, plan_id: str, expected: int) -> dict:
        p = self.store.get(plan_id)
        require(p["revision"] == expected, "stale execution revision; reload status")
        require(p["manifest_digest"] == digest(p["manifest"]), "manifest changed after plan creation")
        return p

    def _rollback(self, p: dict, meta: dict) -> None:
        require(p["stage"] not in {"queued", "superseded", "rolled_back"}, "release has not started or is final")
        require(p["pending"] is None, "operation outcome is unknown; reconcile the same ID before rollback")
        require(meta["active"] in {None, p["id"]}, "another release is active")
        component = p["manifest"]["component"]
        if meta["active"] is None:
            require(meta["latest"].get(component) == p["id"],
                    "a newer release record exists; an old controller cannot roll it back")
        live = self.inspect_all()
        for node in p["nodes"]:
            state = live[node]
            active = state["components"][component]["active"]
            require(digest(active) in {digest(p["manifest"]), digest(p["baseline"][node])},
                    "a newer release is active; this rollback must not overwrite it")
        self.check_target(p, live, rollback=True)
        # No balance snapshot, no table change, no session rotation is an allowed operation.
        meta["epoch"] += 1
        meta["active"] = p["id"]
        p.update(stage="running", direction="rollback", epoch=meta["epoch"], fenced=[],
                 work={node: 0 for node in p["nodes"]}, note="application rollback only; data remains live")
        self._event(p, "rollback-requested")

    def rollback(self, plan_id: str, expected: int) -> dict:
        with self.store.locked():
            p = self._revision(plan_id, expected)
            meta = self.store.meta()
            self._rollback(p, meta)
            p["revision"] += 1
            self.store.save(p, meta)
            return p

    @staticmethod
    def _event(p: dict, event: str) -> None:
        require(len(p["events"]) < 160, "event journal is full; intervention is required")
        p["events"].append({"revision": p["revision"], "epoch": p["epoch"], "event": event})

    def step(self, plan_id: str, expected: int) -> dict:
        with self.store.locked():
            p = self._revision(plan_id, expected)
            meta = self.store.meta()
            require(p["stage"] not in TERMINAL, "release is final")
            require(p["stage"] != "blocked", "release is blocked; inspect the reason and use a safe rollback or forward repair")
            if p["stage"] == "queued":
                require(meta["active"] is None, "another release is active; queued plan must wait")
                live = self.inspect_all()
                self.check_target(p, live)
                c = p["manifest"]["component"]
                p["baseline"] = {node: live[node]["components"][c]["active"] for node in p["nodes"]}
                meta["epoch"] += 1
                meta["active"] = p["id"]
                p.update(stage="running", epoch=meta["epoch"], work={node: 0 for node in p["nodes"]})
                self._event(p, "preflight-complete")
                p["revision"] += 1
                self.store.save(p, meta)
                return p
            require(meta["active"] == p["id"] and meta["epoch"] == p["epoch"], "execution was superseded")
            if p["pending"] is None:
                unfenced = [n for n in p["cluster_nodes"] if n not in p["fenced"]]
                node = unfenced[0] if unfenced else next((n for n in p["nodes"] if p["work"][n] < len(PHASES)), None)
                if node is None:
                    p["stage"] = "rolled_back" if p["direction"] == "rollback" else "completed"
                    meta["active"] = None
                    meta["latest"][p["manifest"]["component"]] = p["id"]
                    p["revision"] += 1
                    self._event(p, p["stage"])
                    self.store.save(p, meta)
                    return p
                action = "fence" if unfenced else PHASES[p["work"][node]]
                target = p["baseline"].get(node, p["manifest"]) if p["direction"] == "rollback" else p["manifest"]
                previous = p["manifest"] if p["direction"] == "rollback" else p["baseline"].get(node, p["manifest"])
                op = {"api": API, "authority": meta["authority"], "cluster": self.inventory["cluster"],
                      "release_id": p["id"], "epoch": p["epoch"], "node": node,
                      "direction": p["direction"], "action": action, "target": target, "previous": previous,
                      "plan_digest": p["manifest_digest"], "policy": self.inventory["policy"]}
                op["operation_id"] = digest(op)
                p["pending"] = op
                # Durable intent precedes every possible remote side effect. Same ID survives a crash.
                self.store.save(p)
            op = p["pending"]
            try:
                reply = self.adapter.invoke(op)
                require(isinstance(reply, dict), "adapter response is not an object")
                require(reply.get("api") == API and reply.get("operation_id") == op["operation_id"]
                        and reply.get("epoch") == op["epoch"], "unbound or stale adapter response")
                status = reply.get("status")
                require(status in {"ok", "wait", "failed", "rejected"}, "unknown adapter status; fail closed")
                if status == "wait":
                    p["note"] = "waiting: " + str(reply.get("reason", "not ready"))[:200]
                elif status in {"failed", "rejected"}:
                    # A definitive rejection resolves the command, unlike a lost acknowledgement.
                    p["pending"] = None
                    p["stage"] = "blocked"
                    p["note"] = "blocked: " + str(reply.get("reason", "failed"))[:200]
                    if status == "failed" and p["direction"] == "deploy" and self.inventory["policy"]["automatic_rollback"]:
                        try:
                            self._rollback(p, meta)
                        except (Rejected, Unavailable) as exc:
                            p["note"] += "; rollback refused: " + str(exc)[:200]
                else:
                    evidence = reply.get("evidence", {})
                    if op["action"] in {"ready", "switch"}:
                        require(evidence.get("artifact_digest") == op["target"]["artifact"]["digest"]
                                and evidence.get("config_digest") == op["target"]["config"]["digest"]
                                and evidence.get("business_ready") is True
                                and set(op["target"]["provides"]) <= set(evidence.get("capabilities", [])),
                                "not business-ready for the exact artifact/configuration")
                    if op["action"] == "pull":
                        require(evidence.get("verified") is True
                                and evidence.get("artifact_digest") == op["target"]["artifact"]["digest"],
                                "artifact verification is absent or mismatched")
                    if op["action"] == "observe":
                        require(evidence.get("money_state") == "settled", "unknown or pending funds state is not success")
                        require(integer(evidence.get("samples"), self.inventory["policy"]["minimum_samples"])
                                and evidence.get("window_seconds", -1) >= self.inventory["policy"]["observe_seconds"],
                                "insufficient business observation")
                    if op["action"] in {"drain", "finish"}:
                        require(integer(evidence.get("inflight"), 0, 0) and integer(evidence.get("unsettled"), 0, 0),
                                "old work or settlement is not complete")
                    if op["action"] == "fence":
                        p["fenced"].append(op["node"])
                    else:
                        p["work"][op["node"]] += 1
                    self._event(p, op["node"] + ":" + p["direction"] + ":" + op["action"])
                    p["pending"] = None
                    p["note"] = None
            except Unavailable:
                p["note"] = "transport unavailable or acknowledgement lost; same operation ID must be reconciled"
            except Rejected as exc:
                # Malformed acknowledgement is not proof that an operation did not execute.
                p["note"] = "unverified outcome: " + str(exc)[:200]
            p["revision"] += 1
            self.store.save(p, meta)
            return p

    def run(self, plan_id: str, expected: int, max_steps: int = 100) -> dict:
        require(integer(max_steps, 1, 500), "max_steps must be 1..500")
        for _ in range(max_steps):
            p = self.step(plan_id, expected)
            if p["stage"] in TERMINAL or p["stage"] == "blocked" or p["pending"] is not None:
                return p  # No resident loop. The next invocation resumes this exact intent.
            expected = p["revision"]
        return p


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state", type=Path, required=True)
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("init").add_argument("--inventory", type=Path, required=True)
    sub.add_parser("plan").add_argument("manifest", type=Path)
    sub.add_parser("status").add_argument("id", nargs="?")
    gc = sub.add_parser("gc")
    gc.add_argument("--keep", type=int, default=12)
    gc.add_argument("--expect-epoch", type=int, required=True)
    for name in ("step", "run", "rollback"):
        cmd = sub.add_parser(name)
        cmd.add_argument("id")
        cmd.add_argument("--expect-revision", type=int, required=True)
        if name == "run":
            cmd.add_argument("--max-steps", type=int, default=100)
    args = parser.parse_args()
    try:
        if args.command == "init":
            store = init(args.state, load(args.inventory))
            value = {"authority": store.meta()["authority"], "api": API}
        else:
            store = Store(args.state)
            if args.command == "status":
                value = store.status(args.id)
            elif args.command == "gc":
                value = gc_history(store, args.keep, args.expect_epoch)
            else:
                controller = Controller(store)
                if args.command == "plan":
                    value = controller.plan(load(args.manifest, 65536))
                elif args.command == "run":
                    value = controller.run(args.id, args.expect_revision, args.max_steps)
                else:
                    value = getattr(controller, args.command)(args.id, args.expect_revision)
        print(canonical(value))
        return 0
    except (Rejected, Unavailable, OSError, sqlite3.Error, ValueError, KeyError, TypeError) as exc:
        print(canonical({"error": type(exc).__name__, "reason": str(exc)[:300]}), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
