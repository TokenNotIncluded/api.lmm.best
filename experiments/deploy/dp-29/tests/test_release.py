from __future__ import annotations

import contextlib
import copy
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from contract import API, Rejected, Unavailable, compatibility_matrix, configuration_compatible, digest, manifest
from fixture import Fixture, connection, create, make_manifest
from hooks import Hooks
from release import Controller, MAX_PLANS, PHASES, Store, gc_history, init


class ReleaseTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="dp29-")
        self.root = Path(self.temp.name)
        self.node_root = self.root / "nodes"
        self.inventory, self.before = create(self.node_root, 3)
        self.store = init(self.root / "control", self.inventory)
        self.fixture = Fixture(self.node_root)
        self.controller = Controller(self.store, self.fixture)
        self.after = {c: make_manifest(c, 2, self.node_root) for c in self.before}

    def tearDown(self):
        self.temp.cleanup()

    def plan(self, component="go-extensions"):
        return self.controller.plan(self.after[component])

    def run_plan(self, p):
        return self.controller.run(p["id"], p["revision"])

    def reach(self, p, action, node="n1"):
        for _ in range(100):
            if len(p["fenced"]) == 3 and p["work"].get(node) == PHASES.index(action):
                return p
            self.assertNotEqual(p["stage"], "blocked", p)
            p = self.controller.step(p["id"], p["revision"])
        self.fail("did not reach " + action)

    def active(self, node="n1", component="go-extensions"):
        return self.fixture.inspect(node)["components"][component]["active"]

    def hashes(self):
        return {n: hashlib.sha256((self.node_root / n / "business.sqlite").read_bytes()).hexdigest()
                for n in self.inventory["nodes"]}

    def cli(self, *args):
        return [sys.executable, "-S", "-B", str(ROOT / "release.py"), "--state", str(self.store.root), *args]

    @contextlib.contextmanager
    def service(self, node="n1"):
        endpoint = self.root / ("endpoint-" + node)
        proc = subprocess.Popen([sys.executable, "-S", "-B", str(ROOT / "tests" / "fixture_service.py"),
                                 str(self.node_root), node, str(endpoint)], stdout=subprocess.DEVNULL,
                                stderr=subprocess.PIPE)
        try:
            for _ in range(200):
                if endpoint.exists():
                    break
                if proc.poll() is not None:
                    self.fail(proc.stderr.read().decode())
                time.sleep(.01)
            self.assertTrue(endpoint.exists())
            base = "http://127.0.0.1:" + endpoint.read_text()
            def get(path):
                request = urllib.request.Request(base + path, headers={"Cookie": "session=fixture-session"})
                with urllib.request.urlopen(request, timeout=5) as response:
                    return response.read()
            yield get
        finally:
            proc.terminate()
            try:
                proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
            proc.stderr.close()

    def test_three_node_go_release_does_not_touch_rust_or_business_files(self):
        before_hash = self.hashes()
        p = self.run_plan(self.plan())
        self.assertEqual(p["stage"], "completed")
        for node in p["nodes"]:
            self.assertEqual(self.active(node), self.after["go-extensions"])
            self.assertEqual(self.active(node, "rust-core"), self.before["rust-core"])
        self.assertEqual(before_hash, self.hashes())
        switches = [e["event"] for e in p["events"] if e["event"].endswith(":switch")]
        self.assertEqual(switches, [n + ":deploy:switch" for n in p["nodes"]])

    def test_coalesce_only_not_started_plans(self):
        first, second = self.plan(), self.plan()
        self.assertEqual(self.store.get(first["id"])["stage"], "superseded")
        second = self.controller.step(second["id"], second["revision"])
        third = self.controller.plan(make_manifest("go-extensions", 3, self.node_root))
        fourth = self.controller.plan(make_manifest("go-extensions", 4, self.node_root))
        self.assertEqual(self.store.get(second["id"])["stage"], "running")
        self.assertEqual(self.store.get(third["id"])["stage"], "superseded")
        self.assertEqual(self.store.get(fourth["id"])["stage"], "queued")
        self.assertEqual(self.run_plan(second)["stage"], "completed")

    def test_different_component_queue_is_not_coalesced(self):
        go, core = self.plan(), self.plan("rust-core")
        self.assertEqual(self.store.get(go["id"])["stage"], "queued")
        core = self.controller.step(core["id"], core["revision"])
        with self.assertRaisesRegex(Rejected, "another release"):
            self.run_plan(go)

    def test_two_real_controller_processes_cannot_overwrite_same_revision(self):
        p = self.plan()
        command = self.cli("step", p["id"], "--expect-revision", str(p["revision"]))
        processes = [subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE) for _ in range(2)]
        for proc in processes:
            proc.communicate(timeout=10)
        self.assertEqual(sorted(proc.returncode for proc in processes), [0, 2])
        self.assertEqual(self.store.get(p["id"])["revision"], 2)

    def test_stale_revision_is_rejected(self):
        p = self.plan()
        self.controller.step(p["id"], p["revision"])
        with self.assertRaisesRegex(Rejected, "stale"):
            self.controller.step(p["id"], p["revision"])

    def test_distinct_authority_state_cannot_take_over_nodes(self):
        self.run_plan(self.plan())
        other = Controller(init(self.root / "other-control", self.inventory))
        p = other.plan(make_manifest("go-extensions", 3, self.node_root))
        p = other.run(p["id"], p["revision"])
        self.assertEqual(p["stage"], "blocked")
        self.assertIn("another authority", p["note"])
        self.assertEqual(self.active(), self.after["go-extensions"])

    def test_disconnect_during_fencing_prevents_all_traffic_changes(self):
        p = self.controller.step(self.plan()["id"], 1)
        self.fixture.change("n2", offline=True)
        p = self.run_plan(p)
        self.assertIsNotNone(p["pending"])
        for node in p["nodes"]:
            with connection(self.fixture.db_path(node)) as db:
                self.assertEqual(db.execute("SELECT count(*) FROM effects WHERE action='switch'").fetchone()[0], 0)
        self.fixture.change("n2", offline=False)
        self.assertEqual(self.run_plan(p)["stage"], "completed")

    def test_lost_switch_acknowledgement_replays_same_id_once(self):
        self.fixture.change("n1", fault="after:switch")
        p = self.run_plan(self.plan())
        op = copy.deepcopy(p["pending"])
        self.assertEqual(op["action"], "switch")
        self.assertEqual(self.active(), self.after["go-extensions"])
        p = self.run_plan(p)
        self.assertEqual(p["stage"], "completed")
        with connection(self.fixture.db_path("n1")) as db:
            self.assertEqual(db.execute("SELECT count(*) FROM effects WHERE id=?", (op["operation_id"],)).fetchone()[0], 1)

    def test_process_crash_after_node_commit_before_checkpoint(self):
        p = self.reach(self.plan(), "switch")
        code = '''import os,sys
from pathlib import Path
from release import Store,Controller
from fixture import Fixture
f=Fixture(Path(sys.argv[2]))
class Crash:
 def inspect(self,n): return f.inspect(n)
 def invoke(self,op):
  r=f.invoke(op)
  if op['action']=='switch': os._exit(77)
  return r
Controller(Store(Path(sys.argv[1])),Crash()).step(sys.argv[3],int(sys.argv[4]))
'''
        proc = subprocess.run([sys.executable, "-S", "-B", "-c", code, str(self.store.root), str(self.node_root),
                               p["id"], str(p["revision"])], cwd=ROOT, timeout=10)
        self.assertEqual(proc.returncode, 77)
        saved = self.store.get(p["id"])
        self.assertEqual(saved["pending"]["action"], "switch")
        self.assertEqual(saved["revision"], p["revision"])
        self.assertEqual(self.active(), self.after["go-extensions"])
        self.assertEqual(self.run_plan(saved)["stage"], "completed")
        with connection(self.fixture.db_path("n1")) as db:
            self.assertEqual(db.execute("SELECT count(*) FROM effects WHERE action='switch'").fetchone()[0], 1)

    def test_old_node_command_rejected_after_rollback_epoch(self):
        self.fixture.change("n1", fault="after:switch")
        p = self.run_plan(self.plan())
        old_op = p["pending"]
        p = self.run_plan(p)
        p = self.controller.rollback(p["id"], p["revision"])
        self.assertEqual(self.run_plan(p)["stage"], "rolled_back")
        reply = self.fixture.invoke(old_op)
        self.assertEqual(reply["status"], "rejected")
        self.assertEqual(self.active(), self.before["go-extensions"])

    def test_unknown_outcome_blocks_rollback_until_reconciled(self):
        self.fixture.change("n1", fault="after:switch")
        p = self.run_plan(self.plan())
        with self.assertRaisesRegex(Rejected, "unknown"):
            self.controller.rollback(p["id"], p["revision"])
        self.assertEqual(self.run_plan(p)["stage"], "completed")

    def test_failed_canary_rolls_back_before_expansion(self):
        self.fixture.change("n1", bad_digest=digest(self.after["go-extensions"]))
        p = self.run_plan(self.plan())
        self.assertEqual(p["stage"], "rolled_back")
        for node in p["nodes"]:
            self.assertEqual(self.active(node), self.before["go-extensions"])
        events = [e["event"] for e in p["events"]]
        self.assertNotIn("n2:deploy:switch", events)

    def test_insufficient_observation_stops_expansion(self):
        self.fixture.change("n1", samples=0)
        p = self.run_plan(self.plan())
        self.assertEqual(p["pending"]["action"], "observe")
        self.assertEqual(self.active("n2"), self.before["go-extensions"])
        self.fixture.change("n1", samples=3)
        self.assertEqual(self.run_plan(p)["stage"], "completed")

    def test_observation_window_does_not_complete_early(self):
        # This fixture-only short window is a timing test, not a production policy.
        self.inventory["policy"]["observe_seconds"] = 1
        other = Controller(init(self.root / "timed-control", self.inventory))
        p = other.plan(self.after["go-extensions"])
        p = other.run(p["id"], p["revision"])
        self.assertEqual(p["pending"]["action"], "observe")
        self.assertIn("waiting", p["note"])

    def test_memory_and_disk_wait_without_killing_old_versions(self):
        for key in ("free_bytes", "memory_headroom"):
            with self.subTest(resource=key):
                self.fixture.change("n1", **{key: 0})
                if key == "free_bytes":
                    p = self.run_plan(self.plan())
                else:
                    p = self.run_plan(p)
                self.assertEqual(p["pending"]["action"], "space")
                self.assertEqual(self.active(), self.before["go-extensions"])
                self.fixture.change("n1", **{key: 32 * 1048576})
        self.assertEqual(self.run_plan(p)["stage"], "completed")

    def test_third_live_version_is_not_admitted(self):
        state = self.fixture.inspect("n1")
        state["components"]["go-extensions"]["live"].append(make_manifest("go-extensions", 3, self.node_root))
        self.fixture.change("n1", components=state["components"])
        p = self.run_plan(self.plan())
        self.assertEqual(p["pending"]["action"], "space")
        self.assertIn("version limit", p["note"])

    def test_bad_artifact_digest_never_reaches_prepare_or_switch(self):
        sha = self.after["go-extensions"]["artifact"]["digest"].split(":")[1]
        (self.node_root / "artifacts" / (sha + ".blob")).write_bytes(b"tampered")
        p = self.run_plan(self.plan())
        self.assertEqual(p["stage"], "blocked")
        self.assertEqual(self.active(), self.before["go-extensions"])
        self.assertIn("digest mismatch", p["note"])

    def test_alive_but_not_ready_never_receives_traffic(self):
        self.fixture.change("n1", unready=True)
        p = self.run_plan(self.plan())
        self.assertEqual(p["pending"]["action"], "ready")
        self.assertEqual(self.active(), self.before["go-extensions"])
        self.fixture.change("n1", unready=False)
        self.assertEqual(self.run_plan(p)["stage"], "completed")

    def test_missing_actual_capability_blocks_traffic(self):
        self.fixture.change("n1", missing_capability="fixture-read")
        p = self.run_plan(self.plan())
        self.assertEqual(p["stage"], "blocked")
        self.assertEqual(self.active(), self.before["go-extensions"])

    def test_unknown_funds_state_never_means_success(self):
        self.fixture.change("n1", money_state="NEW_UNRECOGNIZED_STATE")
        p = self.run_plan(self.plan())
        self.assertEqual(p["stage"], "blocked")
        self.assertIn("unknown money", p["note"])
        self.assertEqual(self.active("n2"), self.before["go-extensions"])

    def test_pending_settlement_waits_instead_of_succeeding(self):
        self.fixture.change("n1", money_state="pending")
        p = self.run_plan(self.plan())
        self.assertEqual(p["pending"]["action"], "observe")
        self.fixture.change("n1", money_state="settled")
        self.assertEqual(self.run_plan(p)["stage"], "completed")

    def test_unknown_reply_status_preserves_intent_and_fails_closed(self):
        fixture = self.fixture
        class Unknown:
            def inspect(self, n):
                return fixture.inspect(n)
            def invoke(self, op):
                reply = fixture.invoke(op)
                reply["status"] = "future-success-ish"
                return reply
        controller = Controller(self.store, Unknown())
        p = self.plan()
        p = controller.run(p["id"], p["revision"])
        self.assertIsNotNone(p["pending"])
        self.assertIn("unverified outcome", p["note"])
        self.assertEqual(self.active(), self.before["go-extensions"])
        self.assertEqual(self.run_plan(p)["stage"], "completed")

    def test_manifest_unknown_fields_and_mutable_tag(self):
        candidate = copy.deepcopy(self.after["go-extensions"])
        candidate["optional_future_label"] = "ignored"
        manifest(candidate)
        candidate["critical_fields"] = ["optional_future_label"]
        with self.assertRaises(Rejected):
            manifest(candidate)
        candidate = copy.deepcopy(self.after["go-extensions"])
        candidate["config"]["required_fields"].append("unknown-safety-setting")
        with self.assertRaises(Rejected):
            manifest(candidate)
        candidate = copy.deepcopy(self.after["go-extensions"])
        candidate["artifact"]["image"] = "fixture.invalid/go:latest"
        with self.assertRaises(Rejected):
            manifest(candidate)

    def test_all_mixed_n_n1_manifest_combinations(self):
        rows = compatibility_matrix(self.before, self.after, 1, ["pending", "settled"])
        self.assertEqual(len(rows), 9)
        self.assertTrue(all(row["compatible"] for row in rows))
        bad = copy.deepcopy(self.after)
        bad["go-extensions"]["protocol"]["emit"] = 2
        before = copy.deepcopy(self.before)
        before["rust-core"]["protocol"]["accepts"] = [1, 1]
        self.assertTrue(any(not r["compatible"] for r in compatibility_matrix(before, bad, 1, ["settled"])))

    def test_architecture_mismatch_stops_preflight(self):
        self.fixture.change("n2", platform="linux/arm64")
        p = self.plan()
        with self.assertRaisesRegex(Rejected, "architecture"):
            self.run_plan(p)
        self.assertIsNone(self.store.meta()["active"])

    def test_unsafe_schema_downgrade_is_refused_without_data_restore(self):
        old = copy.deepcopy(self.before["go-extensions"])
        old["schema"] = {"read": [1, 1], "write": [1, 1]}
        for node in self.inventory["nodes"]:
            state = self.fixture.inspect(node)
            state["components"]["go-extensions"] = {"active": old, "live": [old]}
            self.fixture.change(node, components=state["components"])
        p = self.run_plan(self.plan())
        for node in self.inventory["nodes"]:
            self.fixture.change(node, schema=2)
        before_hash = self.hashes()
        with self.assertRaisesRegex(Rejected, "unsafe live schema"):
            self.controller.rollback(p["id"], p["revision"])
        self.assertEqual(self.hashes(), before_hash)
        self.assertEqual(self.active(), self.after["go-extensions"])
        # A completed release with an unsafe downgrade can still receive a compatible forward fix.
        repair = self.controller.plan(make_manifest("go-extensions", 3, self.node_root))
        self.assertEqual(self.run_plan(repair)["stage"], "completed")
        self.assertEqual(self.hashes(), before_hash)

    def test_previous_release_cannot_rollback_over_newer_completed_release(self):
        old = self.run_plan(self.plan())
        new = self.controller.plan(make_manifest("go-extensions", 3, self.node_root))
        new = self.run_plan(new)
        with self.assertRaisesRegex(Rejected, "newer release"):
            self.controller.rollback(old["id"], old["revision"])
        self.assertEqual(self.active()["version"], "fixture-3")

    def test_history_limit_does_not_discard_the_last_queued_plan(self):
        for _ in range(MAX_PLANS):
            p = self.plan()
        with self.assertRaisesRegex(Rejected, "history limit"):
            self.plan()
        self.assertEqual(self.store.get(p["id"])["stage"], "queued")

    def test_init_and_ordinary_start_do_not_overwrite_existing_state(self):
        before_hash = self.hashes()
        with self.assertRaises(Rejected):
            init(self.store.root, self.inventory)
        with self.store.connect() as db:
            db.execute("PRAGMA user_version=99")
        with self.assertRaisesRegex(Rejected, "unknown control schema"):
            Store(self.store.root)
        self.assertEqual(before_hash, self.hashes())

    def test_production_inventory_and_missing_hooks_are_refused(self):
        inv = copy.deepcopy(self.inventory)
        inv["environment"] = "production"
        with self.assertRaises(Rejected):
            init(self.root / "prod", inv)
        with self.assertRaisesRegex(Rejected, "missing dp24"):
            Hooks({}).inspect("n1")

    def test_common_hook_dispatch_uses_task_owners(self):
        class Recorder(Hooks):
            def call(self, owner, request):
                return owner
        hooks = Recorder({})
        self.assertEqual(hooks.inspect("n1"), "dp24")
        for action, owner in (("fence", "dp24"), ("space", "dp28"), ("pull", "dp27"), ("switch", "dp23")):
            self.assertEqual(hooks.invoke({"action": action, "target": {"component": "go-extensions"}}), owner)
        self.assertEqual(hooks.invoke({"action": "drain", "target": {"component": "rust-core"}}), "dp22")

    def test_hook_output_is_bounded_and_executable_is_pinned(self):
        path = self.root / "adapter"
        path.write_text("#!/usr/bin/python3\nprint('x'*70000)\n")
        path.chmod(0o700)
        checksum = "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()
        hooks = Hooks({"dp24": {"executable": str(path), "digest": checksum}})
        with self.assertRaises(Unavailable):
            hooks.inspect("n1")
        path.write_text("#!/usr/bin/python3\nprint('{}')\n")
        with self.assertRaisesRegex(Rejected, "digest changed"):
            hooks.inspect("n1")

    def test_rollback_preserves_real_fixture_transactions_and_sessions(self):
        with self.service() as get:
            get("/consume?id=before&amount=10")
            p = self.run_plan(self.plan())
            get("/consume?id=during&amount=20")
            get("/consume?id=during&amount=20")
            snapshot = self.hashes()
            p = self.controller.rollback(p["id"], p["revision"])
            self.assertEqual(self.run_plan(p)["stage"], "rolled_back")
            self.assertEqual(snapshot, self.hashes())
            self.assertEqual(get("/balance"), b"9970")
            get("/consume?id=after&amount=3")
            self.assertEqual(get("/balance"), b"9967")
            with connection(self.node_root / "n1" / "business.sqlite") as db:
                self.assertEqual(db.execute("SELECT count(*) FROM ledger").fetchone()[0], 3)
                self.assertEqual(db.execute("SELECT count(*) FROM sessions").fetchone()[0], 1)

    def test_http_sse_survives_controller_offline_and_old_stream_is_not_killed(self):
        with self.service() as get:
            results = []
            errors = []
            def stream():
                try:
                    results.append(get("/stream?id=long-stream&seconds=1.5"))
                except Exception as exc:
                    errors.append(exc)
            thread = threading.Thread(target=stream)
            thread.start()
            for _ in range(100):
                with connection(self.fixture.db_path("n1")) as db:
                    if db.execute("SELECT count(*) FROM streams").fetchone()[0]:
                        break
                time.sleep(.005)
            p = self.run_plan(self.plan("rust-core"))
            self.assertEqual(p["pending"]["action"], "drain")
            self.assertEqual(self.active("n2", "rust-core"), self.before["rust-core"])
            # The fixture service never opens the controller state, even during an accepted stream.
            offline = self.root / "controller-offline"
            self.store.root.rename(offline)
            try:
                self.assertEqual(get("/balance"), b"10000")
                thread.join(timeout=5)
                self.assertFalse(thread.is_alive())
                self.assertFalse(errors)
                self.assertIn(b"event: done", results[0])
                self.assertIn(digest(self.before["rust-core"]).encode(), results[0])
                self.assertEqual(get("/balance"), b"9999")
            finally:
                offline.rename(self.store.root)
            self.assertEqual(self.run_plan(p)["stage"], "completed")

    def test_rollback_also_waits_for_new_version_inflight_work(self):
        p = self.run_plan(self.plan())
        with connection(self.fixture.db_path("n1")) as db:
            db.execute("INSERT INTO streams VALUES('accepted-job','go-extensions',?,1)",
                       (digest(self.after["go-extensions"]),))
        p = self.controller.rollback(p["id"], p["revision"])
        p = self.run_plan(p)
        self.assertEqual(p["pending"]["action"], "drain")
        self.assertEqual(p["direction"], "rollback")
        with connection(self.fixture.db_path("n1")) as db:
            db.execute("DELETE FROM streams WHERE id='accepted-job'")
        self.assertEqual(self.run_plan(p)["stage"], "rolled_back")

    def test_frontend_old_assets_and_session_survive_release_and_rollback(self):
        with self.service() as get:
            old_asset = self.before["web"]["web"]["assets"][0]
            content = get("/assets/" + old_asset)
            p = self.run_plan(self.plan("web"))
            self.assertEqual(get("/assets/" + old_asset), content)
            new_asset = self.after["web"]["web"]["assets"][0]
            get("/assets/" + new_asset)
            p = self.controller.rollback(p["id"], p["revision"])
            self.run_plan(p)
            self.assertEqual(get("/assets/" + old_asset), content)
            get("/assets/" + new_asset)
            self.assertEqual(get("/balance"), b"10000")

    def test_ten_consecutive_go_releases_keep_one_final_live_version(self):
        for version in range(2, 12):
            p = self.controller.plan(make_manifest("go-extensions", version, self.node_root))
            self.assertEqual(self.run_plan(p)["stage"], "completed")
            for node in self.inventory["nodes"]:
                state = self.fixture.inspect(node)
                self.assertEqual(len(state["components"]["go-extensions"]["live"]), 1)
                self.assertEqual(state["components"]["rust-core"]["active"], self.before["rust-core"])

    def test_old_browser_contract_survives_frontend_process_retirement(self):
        # New pages use API 2. API 1 tabs remain open after the old web process exits.
        self.after["web"]["web"]["api_emit"] = 2
        self.run_plan(self.plan("web"))
        backend = copy.deepcopy(self.after["go-extensions"])
        backend["web"]["api_accepts"] = [2, 2]
        p = self.controller.plan(backend)
        with self.assertRaisesRegex(Rejected, "old page API"):
            self.run_plan(p)
        self.assertEqual(self.active(), self.before["go-extensions"])

    def test_same_artifact_new_release_record_still_blocks_old_rollback(self):
        old = self.run_plan(self.plan())
        new = self.run_plan(self.plan())
        self.assertNotEqual(old["id"], new["id"])
        with self.assertRaisesRegex(Rejected, "newer release"):
            self.controller.rollback(old["id"], old["revision"])

    def test_configuration_versions_are_checked_independently_of_application(self):
        app = copy.deepcopy(self.before["go-extensions"])
        app["config"]["accepts"] = [1, 1]
        with self.assertRaisesRegex(Rejected, "configuration version"):
            configuration_compatible(app, self.after["go-extensions"]["config"])
        app["config"]["accepts"] = [1, 2]
        configuration_compatible(app, self.after["go-extensions"]["config"])
        config = copy.deepcopy(self.after["go-extensions"]["config"])
        config["required_fields"].append("new-mandatory-field")
        with self.assertRaisesRegex(Rejected, "required configuration"):
            configuration_compatible(app, config)

    def test_history_cleanup_preserves_live_and_latest_rollback_records(self):
        completed = self.run_plan(self.plan())
        active = self.plan("rust-core")
        active = self.controller.step(active["id"], active["revision"])
        for _ in range(15):
            queued = self.plan()
        before_hash = self.hashes()
        result = gc_history(self.store, 3, self.store.meta()["epoch"])
        self.assertGreater(len(result["removed"]), 0)
        self.assertEqual(self.store.get(completed["id"])["stage"], "completed")
        self.assertEqual(self.store.get(active["id"])["stage"], "running")
        self.assertEqual(self.store.get(queued["id"])["stage"], "queued")
        self.assertEqual(before_hash, self.hashes())

    def test_repeated_cleanup_reuses_control_database_pages(self):
        for batch in range(8):
            for _ in range(20):
                p = self.plan()
            gc_history(self.store, 3, self.store.meta()["epoch"])
            self.assertEqual(self.store.get(p["id"])["stage"], "queued")
        self.assertLessEqual(len(self.store.status()["plans"]), 3)
        self.assertLess(self.store.db.stat().st_size, 1048576)

    def test_one_and_five_synthetic_node_rollouts(self):
        for count in (1, 5):
            with self.subTest(nodes=count):
                root = self.root / ("separate-" + str(count))
                inventory, _ = create(root, count)
                controller = Controller(init(self.root / ("control-" + str(count)), inventory))
                p = controller.plan(make_manifest("go-extensions", 2, root))
                p = controller.run(p["id"], p["revision"])
                self.assertEqual(p["stage"], "completed")
                self.assertEqual(len(p["work"]), count)

    def test_fake_health_only_reply_is_not_business_readiness(self):
        fixture = self.fixture
        class HealthOnly:
            def inspect(self, node):
                return fixture.inspect(node)
            def invoke(self, op):
                result = fixture.invoke(op)
                if op["action"] == "ready":
                    result["evidence"] = {"health": "ok"}
                return result
        controller = Controller(self.store, HealthOnly())
        p = self.plan()
        p = controller.run(p["id"], p["revision"])
        self.assertEqual(p["pending"]["action"], "ready")
        self.assertEqual(self.active(), self.before["go-extensions"])

    def test_state_loss_does_not_create_a_fresh_authority_implicitly(self):
        p = self.run_plan(self.plan())
        self.store.db.rename(self.store.root / "lost.sqlite")
        with self.assertRaisesRegex(Rejected, "state is absent"):
            Store(self.store.root)
        self.assertEqual(self.active(), self.after["go-extensions"])

    def test_role_specific_target_group_changes_only_go_nodes(self):
        self.inventory["targets"] = {"rust-core": ["n1"], "go-extensions": ["n2", "n3"], "web": ["n3"]}
        roles = {"n1": {"rust-core"}, "n2": {"go-extensions"}, "n3": {"go-extensions", "web"}}
        for node, allowed in roles.items():
            state = self.fixture.inspect(node)
            self.fixture.change(node, components={c: m for c, m in state["components"].items() if c in allowed})
        controller = Controller(init(self.root / "role-control", self.inventory))
        p = controller.plan(self.after["go-extensions"])
        p = controller.run(p["id"], p["revision"])
        self.assertEqual(p["stage"], "completed")
        self.assertEqual(p["nodes"], ["n2", "n3"])
        self.assertEqual(len(p["fenced"]), 3)
        with connection(self.fixture.db_path("n1")) as db:
            self.assertEqual(db.execute("SELECT count(*) FROM effects WHERE action!='fence'").fetchone()[0], 0)
        p = controller.rollback(p["id"], p["revision"])
        self.assertEqual(controller.run(p["id"], p["revision"])["stage"], "rolled_back")


if __name__ == "__main__":
    unittest.main(verbosity=2)
