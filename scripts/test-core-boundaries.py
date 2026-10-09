#!/usr/bin/env python3
"""Offline architectural guards; Docker execution is a separate, required test."""
import json
from pathlib import Path
import tomllib
import unittest

ROOT = Path(__file__).resolve().parents[1]

class CoreBoundaryTests(unittest.TestCase):
    def test_retired_backend_and_assets_are_absent(self):
        for name in ("apps/api-rust", "packaging/aur/lmm-api-rs-git",
                     "packaging/common/lmm-api/lmm-api-rs.env.example",
                     "packaging/common/lmm-api/validate-route-gate"):
            self.assertFalse((ROOT / name).exists(), name)
        self.assertTrue((ROOT / "apps/lmm/Cargo.toml").is_file())
        self.assertTrue((ROOT / "apps/api-go/main.go").is_file())

    def test_executable_entries_do_not_reference_retired_source(self):
        paths = [ROOT / "justfile", ROOT / "package.json"]
        paths += list((ROOT / ".github/workflows").glob("*.yml"))
        paths += list((ROOT / "scripts/ci").glob("*.sh"))
        for path in paths:
            self.assertNotIn("apps/api-rust", path.read_text(), str(path))
        for path in (ROOT / "apps/api-go/controller").glob("*.go"):
            self.assertNotIn("../../api-rust/", path.read_text(), str(path))

    def test_root_core_commands_use_the_pinned_toolchain_directory(self):
        scripts = json.loads((ROOT / "package.json").read_text())["scripts"]
        for name in ("dev", "build", "test", "format", "format-check", "lint", "typecheck"):
            self.assertTrue(scripts[name + ":core"].startswith("cd apps/core-rust && cargo "), name)

    def test_core_has_no_extension_startup_dependency(self):
        cargo = tomllib.loads((ROOT / "apps/core-rust/Cargo.toml").read_text())
        self.assertEqual(cargo["lints"]["rust"]["unsafe_code"], "forbid")
        for name in cargo["dependencies"]:
            self.assertNotIn("extension", name)
        for name in ("main.rs", "http.rs"):
            self.assertNotIn("LMM_EXTENSION", (ROOT / "apps/core-rust/src" / name).read_text())

    def test_compose_projects_are_separate_and_resource_bounded(self):
        core = (ROOT / "deployment/docker/compose.core.yml").read_text()
        extension = (ROOT / "deployment/docker/compose.extensions.yml").read_text()
        self.assertTrue(core.startswith("name: lmm-core\n"))
        self.assertTrue(extension.startswith("name: lmm-extensions\n"))
        for body, owned, forbidden in ((core, "core", "extensions"), (extension, "extensions", "core")):
            services = body.split("services:\n", 1)[1].split("\nnetworks:", 1)[0]
            self.assertRegex(services, rf"(?m)^  {owned}:$")
            self.assertNotRegex(services, rf"(?m)^  {forbidden}:$")
            for field in ("read_only: true", "mem_limit:", "pids_limit:", "127.0.0.1:"):
                self.assertIn(field, body)
            self.assertNotIn("depends_on:", body)
            self.assertNotIn("docker.sock", body)
            self.assertNotIn("privileged: true", body)
        self.assertIn("external: true", extension)
        self.assertNotIn("SQL_DSN", extension)
        self.assertNotIn("DATABASE_URL", extension)

    def test_shared_go_regression_data_survives(self):
        for name in ("account-balance-parity", "epay-current-go-input", "stripe-subscription-checkout-current-go-input"):
            self.assertTrue(json.loads((ROOT / "contracts/go-regression" / f"{name}.json").read_text()))

    def test_funding_cases_are_shared_and_named(self):
        path = ROOT / "contracts/core/v1/funding-cases.json"
        cases = json.loads(path.read_text())
        self.assertGreaterEqual(len(cases), 24)
        self.assertEqual(len({case["name"] for case in cases}), len(cases))
        for name in ("apps/core-rust/src/funding.rs", "apps/api-go/pkg/accountfunding/contract_test.go"):
            self.assertIn("funding-cases.json", (ROOT / name).read_text())

if __name__ == "__main__":
    unittest.main()
