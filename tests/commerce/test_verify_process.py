"""Offline rejection tests; no product process, database or resource group."""
import copy
import importlib.util
from pathlib import Path
import unittest

SPEC = importlib.util.spec_from_file_location("commerce_verifier_under_test", Path(__file__).with_name("verify_process.py"))
v = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(v)


class ProfileSelectionTests(unittest.TestCase):
    def select(self, mixed_load=False, mixed_slow_sockets=False, shared_resources=False, sealed=False):
        return v.select_profile(mixed_load=mixed_load, mixed_slow_sockets=mixed_slow_sockets,
                                shared_resources=shared_resources, sealed=sealed)

    def test_ordinary_default(self):
        self.assertIsNone(self.select())

    def test_shared_ordinary(self):
        self.assertIsNone(self.select(shared_resources=True, sealed=True))

    def test_original_sustained_profile(self):
        self.assertEqual(self.select(mixed_load=True, shared_resources=True, sealed=True), "sustained")

    def test_separate_socket_profile(self):
        self.assertEqual(self.select(mixed_slow_sockets=True, shared_resources=True, sealed=True), "socket-pressure")

    def test_conflicting_modes(self):
        with self.assertRaises(v.FixtureError):
            self.select(mixed_load=True, mixed_slow_sockets=True, shared_resources=True, sealed=True)

    def test_mixed_profiles_need_shared_limits(self):
        for mode in ("mixed_load", "mixed_slow_sockets"):
            with self.subTest(mode=mode), self.assertRaises(v.FixtureError):
                self.select(**{mode: True, "sealed": True})

    def test_missing_sealed_artifacts(self):
        for mode in (None, "mixed_load", "mixed_slow_sockets"):
            with self.subTest(mode=mode), self.assertRaises(v.FixtureError):
                self.select(**({mode: True} if mode else {}), shared_resources=True)


class SocketOracleTests(unittest.TestCase):
    def setUp(self):
        self.mixed = dict(result="PASS", profile="socket-pressure", model_successes=900,
                          commerce_complete_refunded=180, unknown_holds=120, full_project_complete=False)
        self.result = dict(result="PASS", profile="socket-pressure", minimum_duration_ms=240100,
                           slow_offered=120, slow_accepted=120, retained_at_recovery=120,
                           arrival_period_ms=500, all_120_nonzero_sendq_witness=True,
                           retained_recovery_nonzero_sendq=0, clients_dropped_before_recovery=False,
                           replayed_original_keys=120, provider_hits_before=1020, provider_hits_after=1020,
                           model_complete=900, commerce_complete_refunded=180, unknown_holds=120,
                           full_project_complete=False, proved_ids=[f"{i:064x}" for i in range(120)])
        self.offered = dict(offered=120, accepted=120, peers=[
            dict(id=x, label=f"socket-{i}", server="127.0.0.1:9000", client=f"127.0.0.1:{10000+i}")
            for i, x in enumerate(self.result["proved_ids"])])
        self.recovery = dict(elapsed_ms=180100, retained_clients=120, clients_dropped=False,
                             nonzero_send_queue=0, peers=[
                                 dict(request_id=p["id"], label=p["label"], server=p["server"],
                                      client=p["client"], socket=dict(state="absent", send_queue=0))
                                 for p in self.offered["peers"]], retention_witnesses=[
                                 dict(request_id=p["id"], fd=i+4, device=9, inode=i+100,
                                      same_open_fd=True, original_server=p["server"],
                                      original_client=p["client"], peer_status=dict(
                                          state="reset-or-disconnected-fd-still-open", errno=107))
                                 for i, p in enumerate(self.offered["peers"])])

    def check(self):
        v.verify_joint_socket_result(self.mixed, self.result, self.offered, self.recovery)

    def test_valid_reset_retained_socket(self):
        self.check()

    def test_reject_result_mutations(self):
        mutations = [(key, value) for key, value in self.result.items() if key != "proved_ids"]
        for key, value in mutations:
            replacement = (not value if type(value) is bool else float(value) if type(value) is int else "sustained")
            with self.subTest(key=key):
                old = self.result[key]
                self.result[key] = replacement
                with self.assertRaises(v.FixtureError):
                    self.check()
                self.result[key] = old

    def test_reject_witness_mutations(self):
        for field, value in (("same_open_fd", False), ("inode", 0), ("fd", True),
                             ("original_client", "127.0.0.1:1"), ("request_id", "bad"),
                             ("peer_status", dict(state="reset-or-disconnected-fd-still-open", errno=9))):
            with self.subTest(field=field):
                old = self.recovery["retention_witnesses"][0][field]
                self.recovery["retention_witnesses"][0][field] = value
                with self.assertRaises(v.FixtureError):
                    self.check()
                self.recovery["retention_witnesses"][0][field] = old

    def test_reject_incomplete_or_duplicate_pressure_ids(self):
        for ids in ([], self.result["proved_ids"][:-1], [self.result["proved_ids"][0]] * 120):
            with self.subTest(count=len(ids)):
                self.result["proved_ids"] = ids
                with self.assertRaises(v.FixtureError):
                    self.check()

    def test_reject_recovery_mutations(self):
        baseline = copy.deepcopy(self.recovery)
        for change in (lambda r: r.update(elapsed_ms=179999),
                       lambda r: r.update(elapsed_ms=240000),
                       lambda r: r.update(clients_dropped=True),
                       lambda r: r.update(retention_witnesses=[]),
                       lambda r: r["peers"][0]["socket"].update(send_queue=1),
                       lambda r: r["peers"][0].update(client="127.0.0.1:1"),
                       lambda r: r["peers"][0]["socket"].pop("send_queue"),
                       lambda r: r["peers"].__setitem__(1, r["peers"][0])):
            self.recovery = copy.deepcopy(baseline)
            change(self.recovery)
            with self.assertRaises(v.FixtureError):
                self.check()

    def test_reject_old_sustained_binary(self):
        self.mixed["profile"] = "sustained"
        with self.assertRaises(v.FixtureError):
            self.check()


if __name__ == "__main__":
    unittest.main()
