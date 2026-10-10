#!/usr/bin/env python3
"""Offline source boundaries. Runtime, database and Docker tests are separate."""
import json
from pathlib import Path
import tomllib
import unittest

ROOT = Path(__file__).resolve().parents[1]


class CoreBoundaryTests(unittest.TestCase):
    def test_retired_backends_and_migration_entry_points_are_absent(self):
        for name in ("apps/api-rust", "apps/extensions-go", "apps/core-rust/migrations",
                     "apps/api-go/model", "apps/api-go/controller", "apps/api-go/relay",
                     "apps/api-go/router", "apps/api-go/migration", "apps/api-go/internal/appcli",
                     "scripts/ci/qualify-go-migration-startup.sh", "scripts/deploy-systemd.py",
                     "scripts/deploy-shared-postgres.py", "packaging/common/lmm-api/migration-compatibility.env"):
            self.assertFalse((ROOT / name).exists(), name)
        self.assertTrue((ROOT / "apps/lmm/Cargo.toml").is_file())
        self.assertTrue((ROOT / "apps/api-go/cmd/extensions/main.go").is_file())

    def test_extension_has_no_core_database_or_authority_dependency(self):
        module = (ROOT / "apps/api-go/go.mod").read_text()
        for forbidden in ("gorm.io", "go-sql-driver", "lib/pq", "jackc/pgx", "sqlite"):
            self.assertNotIn(forbidden, module)
        for path in (ROOT / "apps/api-go").rglob("*.go"):
            if path.name.endswith("_test.go"):
                continue
            text = path.read_text()
            self.assertNotIn("AutoMigrate(", text, str(path))
            self.assert_extension_storage(path.relative_to(ROOT).as_posix(), text)
            if path.name != "environment.go":
                for forbidden in ("SQL_DSN", "LMM_CORE_DATABASE_URL"):
                    self.assertNotIn(forbidden, text, str(path))
        startup = (ROOT / "apps/api-go/internal/app/run.go").read_text()
        self.assertLess(startup.index("validateEnvironment(os.Getenv)"), startup.index("readHostCredential(os.Getenv"))
        identity = (ROOT / "apps/api-go/internal/modules/identity/identity.go").read_text()
        self.assertIn("type Core interface", identity)
        for operation in ("Capabilities", "Authorize", "ListTeams"):
            self.assertIn(operation, identity)
        self.assertIn("corepb", identity)

    def assert_extension_storage(self, path, text):
        stores = {
            "apps/api-go/internal/coreclient/sqlinbox.go": "extension_events.",
            "apps/api-go/internal/modules/store/postgres.go": "lmm_store.",
            "apps/api-go/internal/modules/support/postgres.go": "lmm_support.",
        }
        if path not in stores:
            self.assertNotIn('"database/sql"', text, path)
            return
        # Only an injected extension pool. Never open a connection, reference a
        # core table, or access another extension's schema from these adapters.
        self.assertNotIn("sql.Open(", text, path)
        self.assertNotIn("sql.OpenDB(", text, path)
        self.assertIn(stores[path], text, path)
        schemas = ("core_meta.", "core_identity.", "core_billing.",
                   "core_events.", "core_runtime.") + tuple(stores.values())
        for schema in schemas:
            if schema != stores[path]:
                self.assertNotIn(schema, text, path)

    def test_extension_storage_guard_rejects_core_and_cross_module_access(self):
        path = "apps/api-go/internal/modules/store/postgres.go"
        source = (ROOT / path).read_text()
        self.assert_extension_storage(path, source)
        for forbidden in ("core_identity.users", "core_billing.balance_state",
                          "core_events.outbox", "lmm_support.messages",
                          "sql.Open(", "sql.OpenDB("):
            with self.subTest(forbidden=forbidden):
                with self.assertRaises(AssertionError):
                    self.assert_extension_storage(path, source + "\n" + forbidden)
        with self.assertRaises(AssertionError):
            self.assert_extension_storage("apps/api-go/internal/app/unapproved.go", source)

    def test_fresh_schema_is_explicit_and_relational(self):
        schema = (ROOT / "apps/core-rust/schema/identity.sql").read_text()
        for table in ("accounts", "users", "teams", "memberships", "credentials", "key_funding_rules"):
            self.assertIn("CREATE TABLE core_identity." + table, schema)
        self.assertNotIn("funding_policy JSONB", schema)
        self.assertNotIn("DROP ", schema)
        runtime = (ROOT / "apps/core-rust/src/identity/mod.rs").read_text()
        self.assertNotIn("MIGRATOR", runtime)
        self.assertNotIn("sqlx::migrate!", runtime)
        admin = (ROOT / "apps/core-rust/src/bin/lmm-core-admin.rs").read_text()
        self.assertIn('"init-db"', admin)
        self.assertNotIn('"migrate"', admin)
        self.assertNotIn("init_database", (ROOT / "apps/core-rust/src/main.rs").read_text())

    def test_core_has_no_extension_startup_dependency(self):
        cargo = tomllib.loads((ROOT / "apps/core-rust/Cargo.toml").read_text())
        self.assertEqual(cargo["lints"]["rust"]["unsafe_code"], "forbid")
        self.assertNotIn("migrate", cargo["dependencies"]["sqlx"]["features"])
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
        self.assertIn("COPY apps/api-go/", (ROOT / "deployment/docker/extensions.Dockerfile").read_text())
        self.assertIn("COPY apps/core-rust/schema", (ROOT / "deployment/docker/core.Dockerfile").read_text())

    def test_development_and_qualification_use_only_new_entry_points(self):
        sample = (ROOT / ".env.example").read_text()
        for retired in ("SQL_DSN=", "SESSION_SECRET=", "SKIP_64BIT_QUOTA_SCHEMA_CHECK", "LMM_DB_MIGRATION_MODE="):
            self.assertNotIn(retired, sample)
        self.assertIn("LMM_EXTENSION_TOKEN_FILE=", sample)
        for name in ("ci.yml", "server-release-qualification.yml"):
            body = (ROOT / ".github/workflows" / name).read_text()
            self.assertIn("uses: ./.github/workflows/core-protocol.yml", body)
            for retired in ("migrate --", "credit-balance-rebase", "./model", "./relaykit", "apps/extensions-go"):
                self.assertNotIn(retired, body)

    def test_no_workflow_calls_deleted_go_business_packages(self):
        retired = ("apps/api-go/relaykit", "./oauthserver", "./oidcprovider",
                   "./internal/marketprovider", "./model", "./controller", "./router",
                   "./service", "./middleware")
        for path in (ROOT / ".github/workflows").glob("*.yml"):
            body = path.read_text()
            for package in retired:
                self.assertNotIn(package, body, str(path))
        self.assertFalse((ROOT / ".github/workflows/coweft-identity.yml").exists())
        for name, expected in (("lmm.yml", "working-directory: apps/lmm"),
                               ("codewhale-lmm-provider.yml", "packages/codewhale-lmm-provider"),
                               ("wallet-red-packet-review.yml", "working-directory: apps/web"),
                               ("tool-market-provider-verify.yml", "working-directory: apps/web")):
            self.assertIn(expected, (ROOT / ".github/workflows" / name).read_text())

    def test_core_owns_the_funding_contract(self):
        cases = json.loads((ROOT / "contracts/core/v1/funding-cases.json").read_text())
        self.assertGreaterEqual(len(cases), 24)
        self.assertEqual(len({case["name"] for case in cases}), len(cases))
        self.assertIn("funding-cases.json", (ROOT / "apps/core-rust/src/funding.rs").read_text())


if __name__ == "__main__":
    unittest.main()
