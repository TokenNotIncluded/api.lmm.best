#!/usr/bin/env python3
"""Dependency-harness safety regressions without starting a database."""

import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


services_module = load("local_services_test", "with-local-services.py")
runner = load("dependency_runner_test", "run-all-ignored.py")


class LocalServiceIsolationTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.services = services_module.LocalServices(self.directory.name)

    def tearDown(self):
        self.services.close()
        self.directory.cleanup()

    def test_inherited_libpq_and_remote_urls_cannot_override_new_cluster(self):
        inherited = {
            "PGHOSTADDR": "192.0.2.1", "PGSERVICE": "production", "PGSERVICEFILE": "/production/service",
            "PGOPTIONS": "-csearch_path=production", "PGDATABASE": "production", "PGSSLMODE": "verify-full",
            "LMM_RANKINGS_TEST_DATABASE_URL": "postgresql://remote/production",
            "LMM_TEST_POSTGRES_URL": "postgresql://remote/production", "TEST_REDIS_URL": "redis://remote:6379",
            "SQL_DSN": "postgresql://remote/production", "LOG_SQL_DSN": "postgresql://remote/logs",
            "CARGO_TARGET_DIR": "/safe/compiler/cache",
        }
        with patch.dict(os.environ, inherited):
            env = self.services.environment()
        for name in ("PGHOSTADDR", "PGSERVICE", "PGSERVICEFILE", "PGOPTIONS", "TEST_REDIS_URL"):
            self.assertNotIn(name, env)
        for name in ("SQL_DSN", "LOG_SQL_DSN", "LMM_TEST_POSTGRES_URL", "LMM_RANKINGS_TEST_DATABASE_URL"):
            self.assertEqual(env[name], self.services.database_url)
        self.assertEqual(env["PGHOST"], "127.0.0.1")
        self.assertEqual(env["PGSSLMODE"], "disable")
        self.assertEqual(env["CARGO_TARGET_DIR"], "/safe/compiler/cache")

    def test_sql_rejects_connection_strings_before_starting_psql(self):
        with patch.object(services_module.subprocess, "run") as command:
            for database in ("postgresql://remote/production", "host=remote dbname=production", "x;DROP", "UPPER"):
                with self.assertRaises(ValueError):
                    self.services.sql("SELECT 1", database)
            command.assert_not_called()

    def test_psql_uses_only_new_cluster_credentials_and_explicit_address(self):
        with patch.dict(os.environ, {"PGHOSTADDR": "192.0.2.1", "PGSERVICE": "production"}), patch.object(
            services_module.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, "1\n", "")
        ) as command:
            self.assertEqual(self.services.sql("SELECT 1", "postgres"), "1\n")
        arguments = command.call_args.args[0]
        self.assertEqual(arguments[arguments.index("-h") + 1], "127.0.0.1")
        self.assertNotIn("PGHOSTADDR", command.call_args.kwargs["env"])
        self.assertNotIn("PGSERVICE", command.call_args.kwargs["env"])

    def test_every_database_alias_is_remapped_for_each_individual_test(self):
        env = runner.environment_for(self.services, "lmm_test_case_19")
        original_urls = {self.services.database_url, self.services.database_url.rsplit("/", 1)[0] + "/lmm_test_adopt"}
        self.assertFalse(original_urls.intersection(env.values()))
        for name in ("DATABASE_URL", "TEST_POSTGRES_URL", "LMM_TEST_POSTGRES_URL", "LMM_RANKINGS_TEST_DATABASE_URL", "LMM_TEST_ADOPT_DATABASE_URL"):
            self.assertTrue(env[name].endswith("/lmm_test_case_19"), name)
        self.assertEqual(env["PGDATABASE"], "lmm_test_case_19")

    def test_conditional_dependency_inventory_is_explicit_and_list_parsing_is_exact(self):
        self.assertIn("postgres_sms_minimum_balance_reservation", runner.CONDITIONAL_DEPENDENCY_TESTS)
        self.assertIn("postgres_refund_is_transactional_and_idempotent", runner.CONDITIONAL_DEPENDENCY_TESTS)
        self.assertIn("go_signed_receipt_and_immutable_manifest_are_compatible", runner.CONDITIONAL_DEPENDENCY_TESTS)
        self.assertEqual(runner.listed_tests("a: test\na::b: test\nfake: benchmark\n2 tests, 1 benchmark\n"), {"a", "a::b"})

    def test_subprocess_helper_is_executed_through_its_existing_umask_parent(self):
        helper = "report::tests::report_umask_subprocess"
        parent = "report::tests::report_should_create_mode_0600_under_umask_022"
        names = sorted(runner.CONDITIONAL_DEPENDENCY_TESTS | {helper, parent})
        artifact = {"reason":"compiler-artifact", "executable":"/synthetic-test-binary", "profile":{"test":True}, "target":{"name":"lmm_db_migrate"}}
        def compile_command(*_args, **kwargs):
            kwargs["stdout"].write(json.dumps(artifact) + "\n")
            return subprocess.CompletedProcess([], 0)
        def listing(command, **_kwargs):
            selected = [helper] if "--ignored" in command else names
            return "".join(f"{name}: test\n" for name in selected)
        with patch.object(runner.subprocess, "run", side_effect=compile_command), patch.object(runner.subprocess, "check_output", side_effect=listing):
            tests = runner.compiled_tests(Path(self.directory.name))
        record = next(test for test in tests if test["name"] == helper)
        self.assertEqual(record["run_name"], parent)
        self.assertFalse(record["run_ignored"])

    def test_adoption_fixture_configures_public_without_relaxing_the_verifier(self):
        with patch.object(self.services, "sql") as sql:
            runner.prepare_test(self.services, "lmm_test_case_4", "adoption_should_commit_once_replay_without_writes_and_reject_partial_ledger")
        sql.assert_called_once_with('ALTER DATABASE "lmm_test_case_4" SET search_path = public;', "postgres")


if __name__ == "__main__":
    unittest.main()
