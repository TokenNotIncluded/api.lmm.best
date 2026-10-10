import copy
import unittest

from budget import current_plan, evaluate, integer, operator_memory_mib


class ConnectionBudgetTests(unittest.TestCase):
    def test_source_host_static_caps(self):
        for nodes, steady, rolling, full in [(1, 10, 20, 20), (3, 30, 40, 60), (5, 50, 60, 100)]:
            with self.subTest(nodes=nodes):
                self.assertEqual(evaluate(current_plan(nodes, 0))["steady_application_connections"], steady)
                self.assertEqual(evaluate(current_plan(nodes, 1))["peak_application_connections"], rolling)
                self.assertEqual(evaluate(current_plan(nodes, nodes))["peak_application_connections"], full)

    def test_single_node_rollout_plus_admin_exceeds_budget(self):
        r = evaluate(current_plan(1, 1, 1))
        self.assertEqual(r["peak_with_reserves"], 34)
        self.assertFalse(r["within_connection_budget"])

    def test_pool_capacity_does_not_claim_open_connections(self):
        r = evaluate(current_plan(1, 0, 1))
        self.assertEqual(r["peak_application_connections"], 18)
        self.assertIn("not_observation", r["measurement_kind"])
        self.assertFalse(r["business_capacity_accepted"])

    def test_unverified_superuser_role_blocks_even_small_plan(self):
        r = evaluate(current_plan(1, 0))
        self.assertTrue(r["within_connection_budget"])
        self.assertEqual(r["gate"], "blocked")

    def test_exact_boundary(self):
        p = current_plan(1, 0)
        p["role_isolation_verified"] = True
        p["pools"] = [{"id": "one", "kind": "direct", "instances": 1, "surge": 0, "max_connections": 26}]
        self.assertEqual(evaluate(p)["gate"], "pass")
        p["pools"][0]["max_connections"] = 27
        self.assertEqual(evaluate(p)["gate"], "blocked")

    def test_duplicate_physical_pool_rejected(self):
        p = current_plan(1, 0)
        p["pools"].append(copy.deepcopy(p["pools"][0]))
        with self.assertRaises(ValueError):
            evaluate(p)

    def test_cloned_handles_do_not_multiply_pool(self):
        p = current_plan(1, 0)
        p["pools"][0]["consumers"] *= 10
        self.assertEqual(evaluate(p)["peak_application_connections"], 10)

    def test_unknown_active_pool_is_not_zero(self):
        p = current_plan(1, 0)
        p["pools"][0]["max_connections"] = None
        r = evaluate(p)
        self.assertIsNone(r["peak_application_connections"])
        self.assertEqual(r["gate"], "blocked")

    def test_inactive_unknown_pool_does_not_claim_enabled(self):
        p = current_plan(1, 0)
        p["pools"].append({"id": "future", "kind": "direct", "instances": 0, "surge": 0, "max_connections": None})
        self.assertEqual(evaluate(p)["peak_application_connections"], 10)

    def test_negative_fraction_bool_rejected(self):
        for invalid in (-1, 1.5, True, "8", None):
            with self.subTest(value=invalid), self.assertRaises(ValueError):
                integer(invalid, "count")

    def test_pgbouncer_reserve_database_user_and_replica_multiplication(self):
        p = current_plan(1, 0)
        p["pools"] = []
        for user in ("core", "extension"):
            p["pools"].append({"id": user, "kind": "pgbouncer_backend", "proxy": "shared",
                "database": "db", "user": user, "instances": 2, "surge": 1,
                "max_connections": 4, "reserve_pool_size": 1})
        r = evaluate(p)
        self.assertEqual(r["peak_application_connections"], 30)
        self.assertFalse(r["within_connection_budget"])

    def test_pgbouncer_identity_required(self):
        p = current_plan(1, 0)
        p["pools"][0]["kind"] = "pgbouncer_backend"
        with self.assertRaises(ValueError):
            evaluate(p)

    def test_duplicate_backend_tuple_rejected(self):
        p = current_plan(1, 0)
        row = {"id": "a", "kind": "pgbouncer_backend", "proxy": "p", "database": "d", "user": "u",
               "instances": 1, "surge": 0, "max_connections": 2, "reserve_pool_size": 0}
        p["pools"] = [row, dict(row, id="b")]
        with self.assertRaises(ValueError):
            evaluate(p)

    def test_fully_reserved_database_rejected(self):
        p = current_plan(1, 0)
        p["max_connections"] = 6
        with self.assertRaises(ValueError):
            evaluate(p)

    def test_memory_counts_operators_not_database(self):
        self.assertEqual(operator_memory_mib(1, 8, 2, 1, 2), 32)
        self.assertEqual(operator_memory_mib(1, 8, 2, 1, 2, 2), 96)


if __name__ == "__main__":
    unittest.main()
