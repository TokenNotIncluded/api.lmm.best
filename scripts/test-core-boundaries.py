#!/usr/bin/env python3
"""Offline source boundaries. Runtime, database and Docker tests are separate."""
import configparser
import json
from pathlib import Path
import subprocess
import tomllib
import unittest

ROOT = Path(__file__).resolve().parents[1]


class CoreBoundaryTests(unittest.TestCase):
    def test_services_and_user_apps_have_separate_roots(self):
        for name in ("apps/lmm-core", "apps/lmm-extensions", "apps/lmm"):
            self.assertFalse((ROOT / name).exists(), name)
        for name in ("services/core/Cargo.toml", "services/extensions/go.mod",
                     "apps/web/package.json"):
            self.assertTrue((ROOT / name).is_file(), name)

    def test_mise_pins_match_component_toolchain_contracts(self):
        tools = tomllib.loads((ROOT / "mise.toml").read_text())["tools"]
        rust = tomllib.loads((ROOT / "services/core/rust-toolchain.toml").read_text())["toolchain"]
        package = json.loads((ROOT / "package.json").read_text())
        self.assertEqual(tools["rust"]["version"], rust["channel"])
        self.assertEqual(tools["rust"]["profile"], rust["profile"])
        self.assertEqual(set(tools["rust"]["components"]), set(rust["components"]))
        self.assertEqual(f"bun@{tools['bun']}", package["packageManager"])

    def test_retired_backends_and_migration_entry_points_are_absent(self):
        for name in ("apps/api-rust", "apps/extensions-go", "apps/core-rust", "apps/api-go", "services/core/migrations",
                     "services/extensions/model", "services/extensions/controller", "services/extensions/relay",
                     "services/extensions/router", "services/extensions/migration", "services/extensions/internal/appcli",
                     "scripts/ci/qualify-go-migration-startup.sh", "scripts/deploy-systemd.py",
                     "scripts/deploy-shared-postgres.py", "packaging/common/lmm-api/migration-compatibility.env"):
            self.assertFalse((ROOT / name).exists(), name)
        self.assertTrue((ROOT / "services/extensions/cmd/extensions/main.go").is_file())

    def test_cli_is_an_independent_pinned_subproject(self):
        entry = subprocess.run(['git', 'ls-files', '--stage', '--', 'apps/cli'],
                               cwd=ROOT, check=True, capture_output=True, text=True).stdout.strip()
        self.assertRegex(entry, r'^160000 [0-9a-f]{40} 0\tapps/cli$')
        modules = configparser.ConfigParser(interpolation=None)
        modules.read(ROOT / '.gitmodules')
        self.assertEqual(modules['submodule "apps/cli"']['path'], 'apps/cli')
        self.assertEqual(modules['submodule "apps/cli"']['url'],
                         'https://github.com/TokenNotIncluded/lmm.git')

    def test_extension_has_no_core_database_or_authority_dependency(self):
        module = (ROOT / "services/extensions/go.mod").read_text()
        for forbidden in ("gorm.io", "go-sql-driver", "jackc/pgx", "sqlite"):
            self.assertNotIn(forbidden, module)
        self.assertIn("github.com/lib/pq v1.10.9", module)
        for path in (ROOT / "services/extensions").rglob("*.go"):
            if path.name.endswith("_test.go"):
                continue
            text = path.read_text()
            self.assertNotIn("AutoMigrate(", text, str(path))
            self.assert_extension_storage(path.relative_to(ROOT).as_posix(), text)
            if path.name != "environment.go":
                for forbidden in ("SQL_DSN", "LMM_CORE_DATABASE_URL"):
                    self.assertNotIn(forbidden, text, str(path))
        startup = (ROOT / "services/extensions/internal/app/run.go").read_text()
        self.assertLess(startup.index("validateEnvironment(os.Getenv)"), startup.index("readHostCredential(os.Getenv"))
        identity = (ROOT / "services/extensions/internal/modules/identity/identity.go").read_text()
        self.assertIn("type Core interface", identity)
        for operation in ("Capabilities", "Authorize", "ListTeams"):
            self.assertIn(operation, identity)
        self.assertIn("corepb", identity)

    def assert_extension_storage(self, path, text):
        if path == "services/extensions/internal/composition/storage.go":
            self.assertEqual(text.count('sql.Open("postgres", dsn)'), 1, path)
            self.assertNotIn("sql.OpenDB(", text, path)
            for forbidden in ("core_identity.", "core_billing.", "core_events.",
                              "core_meta.", "core_runtime.", "AutoMigrate("):
                self.assertNotIn(forbidden, text, path)
            for required in ("current_database()", "has_database_privilege", "has_table_privilege",
                             "pg_has_role(current_user,oid,'SET')", "pg_has_role(current_user,oid,'MEMBER')",
                             "rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication",
                             "extension storage isolation rejected"):
                self.assertIn(required, text, path)
            return
        stores = {
            "services/extensions/internal/coreclient/sqlinbox.go": "extension_events.",
            "services/extensions/internal/modules/store/postgres.go": "lmm_store.",
            "services/extensions/internal/modules/store/postgres_web.go": "lmm_store.",
            "services/extensions/internal/modules/support/postgres.go": "lmm_support.",
            "services/extensions/internal/modules/support/postgres_web.go": "lmm_support.",
            "services/extensions/internal/modules/assistant/postgres.go": "lmm_assistant.",
            "services/extensions/internal/modules/promotions/postgres.go": "lmm_promotions.",
            "services/extensions/internal/modules/payments/postgres.go": "payment_",
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
        path = "services/extensions/internal/modules/store/postgres.go"
        source = (ROOT / path).read_text()
        self.assert_extension_storage(path, source)
        for forbidden in ("core_identity.users", "core_billing.balance_state",
                          "core_events.outbox", "lmm_support.messages", "lmm_assistant.records",
                          "lmm_promotions.records", "payment_orders",
                          "sql.Open(", "sql.OpenDB("):
            with self.subTest(forbidden=forbidden):
                with self.assertRaises(AssertionError):
                    self.assert_extension_storage(path, source + "\n" + forbidden)
        with self.assertRaises(AssertionError):
            self.assert_extension_storage("services/extensions/internal/app/unapproved.go", source)

    def test_assistant_promotions_and_payment_storage_remain_narrow(self):
        for module, own in (("assistant", "lmm_assistant."), ("promotions", "lmm_promotions."),
                            ("payments", "payment_")):
            path = f"services/extensions/internal/modules/{module}/postgres.go"
            source = (ROOT / path).read_text()
            self.assert_extension_storage(path, source)
            for forbidden in ("core_identity.users", "core_billing.balance_state", "core_events.outbox",
                              "lmm_store.records", "lmm_support.records", "sql.Open(", "sql.OpenDB("):
                with self.subTest(module=module, forbidden=forbidden):
                    with self.assertRaises(AssertionError):
                        self.assert_extension_storage(path, source + "\n" + forbidden)
            for other in ("lmm_assistant.", "lmm_promotions.", "payment_"):
                if other != own:
                    with self.assertRaises(AssertionError):
                        self.assert_extension_storage(path, source + "\n" + other + "records")

    def test_fresh_schema_is_explicit_and_relational(self):
        schema = (ROOT / "services/core/schema/identity.sql").read_text()
        for table in ("accounts", "users", "teams", "memberships", "credentials", "key_funding_rules"):
            self.assertIn("CREATE TABLE core_identity." + table, schema)
        self.assertNotIn("funding_policy JSONB", schema)
        self.assertNotIn("DROP ", schema)
        runtime = (ROOT / "services/core/src/identity/mod.rs").read_text()
        self.assertNotIn("MIGRATOR", runtime)
        self.assertNotIn("sqlx::migrate!", runtime)
        admin = (ROOT / "services/core/src/admin.rs").read_text()
        self.assertIn("core_schema::init(&pool)", admin)
        self.assertIn("core_schema::check(&pool)", admin)
        self.assertNotIn('"migrate"', admin)
        main = (ROOT / "services/core/src/main.rs").read_text()
        self.assertIn('Some("init-db")', main)
        self.assertIn('Some("bootstrap-user")', main)
        self.assertNotIn("init_database", main.split("async fn run_core()", 1)[1])

    def test_core_management_shares_the_single_core_executable(self):
        cargo = tomllib.loads((ROOT / "services/core/Cargo.toml").read_text())
        self.assertFalse(cargo["package"]["autobins"])
        self.assertEqual(cargo["bin"], [{"name": "lmm-core", "path": "src/main.rs"}])
        self.assertFalse((ROOT / "services/core/src/bin/lmm-core-admin.rs").exists())

    def test_core_has_no_extension_startup_dependency(self):
        cargo = tomllib.loads((ROOT / "services/core/Cargo.toml").read_text())
        self.assertEqual(cargo["lints"]["rust"]["unsafe_code"], "forbid")
        self.assertNotIn("migrate", cargo["dependencies"]["sqlx"]["features"])
        for name in cargo["dependencies"]:
            self.assertNotIn("extension", name)
        for name in ("main.rs", "http.rs"):
            self.assertNotIn("LMM_EXTENSION", (ROOT / "services/core/src" / name).read_text())

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
        self.assertIn("COPY services/extensions/", (ROOT / "deployment/docker/extensions.Dockerfile").read_text())
        self.assertIn("COPY services/core/schema", (ROOT / "deployment/docker/core.Dockerfile").read_text())

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
        retired = ("services/extensions/relaykit", "./oauthserver", "./oidcprovider",
                   "./internal/marketprovider", "./model", "./controller", "./router",
                   "./service", "./middleware")
        for path in (ROOT / ".github/workflows").glob("*.yml"):
            body = path.read_text()
            for package in retired:
                self.assertNotIn(package, body, str(path))
        self.assertFalse((ROOT / ".github/workflows/coweft-identity.yml").exists())
        for name, expected in (("lmm.yml", "working-directory: apps/cli"),
                               ("codewhale-lmm-provider.yml", "packages/codewhale-lmm-provider"),
                               ("wallet-red-packet-review.yml", "working-directory: apps/web"),
                               ("tool-market-provider-verify.yml", "working-directory: apps/web")):
            self.assertIn(expected, (ROOT / ".github/workflows" / name).read_text())

    def test_standalone_core_has_bounded_resources_and_no_go_runtime(self):
        ignore = (ROOT / ".dockerignore").read_text().splitlines()
        self.assertIn("experiments/", ignore)
        compose = (ROOT / "deployment/docker/compose.core-standalone.yml").read_text()
        for expected in ("LMM_CORE_WORKER_THREADS: \"1\"",
                         "LMM_CORE_DB_CONNECTIONS: \"2\"",
                         "LMM_CORE_PASSWORD_SLOTS: \"1\"",
                         "LMM_CORE_MAX_INFLIGHT: \"16\"",
                         "LMM_CORE_DATABASE_URL_FILE: /run/secrets/core_database_url"):
            self.assertIn(expected, compose)
        self.assertNotIn("extensions:", compose)
        self.assertNotIn("core-db:", compose)
        self.assertIn("lmm-core-standalone-runtime", compose)
        core = (ROOT / "services/core/src/main.rs").read_text()
        self.assertIn("config::database_pool", core)
        configuration = (ROOT / "services/core/src/config.rs").read_text()
        self.assertIn(".max_connections(database_connections()?)", configuration)
        self.assertIn("Lifecycle::with_capacity", core)
        self.assertIn("with_password_slots", core)
        self.assertNotIn("lmm_extensions", core)

    def test_core_runtime_privileges_are_checked_without_writes(self):
        main = (ROOT / "services/core/src/main.rs").read_text()
        guard = (ROOT / "services/core/src/runtime_database.rs").read_text()
        self.assertIn("runtime_database::check", main)
        for required in ("REPEATABLE READ READ ONLY", "rolsuper", "rolbypassrls",
                         "rolcreaterole", "rolcreatedb", "rolreplication",
                         "has_any_column_privilege", "MEMBER WITH ADMIN OPTION",
                         "pg_has_role(current_user,r.oid,'SET')",
                         "core_migration", "balance_state", "open_ledger_wallet"):
            self.assertIn(required, guard)
        for mutation in ("GRANT ", "ALTER ROLE ", "CREATE TABLE ", "INSERT INTO ",
                         "UPDATE core_", "DELETE FROM "):
            self.assertNotIn(mutation, guard)

    def test_core_owns_the_funding_contract(self):
        cases = json.loads((ROOT / "contracts/core/v1/funding-cases.json").read_text())
        self.assertGreaterEqual(len(cases), 24)
        self.assertEqual(len({case["name"] for case in cases}), len(cases))
        self.assertIn("funding-cases.json", (ROOT / "services/core/src/funding.rs").read_text())


if __name__ == "__main__":
    unittest.main()
