#!/usr/bin/env python3
"""Offline source boundaries. Runtime, database and Docker tests are separate."""
import configparser
import json
import re
from pathlib import Path
import subprocess
import tomllib
import unittest

ROOT = Path(__file__).resolve().parents[1]
COMPOSITION_STORAGE = "services/extensions/internal/composition/storage.go"
COMPOSITION_PAYMENTS = "services/extensions/internal/composition/payments.go"
OPERATIONS_POSTGRES = "services/extensions/internal/modules/operations/postgres.go"
OPERATIONS_RELEASES = "services/extensions/internal/modules/operations/releases.go"
OPERATIONS_INJECTION = "services/extensions/internal/modules/operations/scripts.go"
# Source guards are an offline contract only. The PostgreSQL role/schema/grant
# mutations in composition/guards_integration_test.go require a real test DB.
COMPOSITION_STORAGE_GUARDS = (
    'regexp.MustCompile(`^lmm_(store|support|assistant|payments|operations)(_[a-z0-9]+)*$`)',
    '(c.Component != "store" && c.Component != "support" && c.Component != "assistant" && c.Component != "payments" && c.Component != "operations")',
    '!databaseName.MatchString(c.Database)',
    'strings.HasPrefix(c.Database, "lmm_"+c.Component)',
    'if err := c.validate(); err != nil',
    'coreclient.ReadPrivateFile(c.URLFile, 8192)',
    'name != c.Database',
    'key != "sslmode" && key != "sslrootcert" && key != "connect_timeout" && key != "application_name"',
    'len(values) != 1',
    'sql.Open("postgres", dsn)',
    'db.SetMaxOpenConns(2)',
    'db.SetMaxIdleConns(2)',
    'context.WithTimeout(ctx, 3*time.Second)',
    'rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication',
    r"nspname LIKE 'core\_%' ESCAPE '\'",
    r"nspname LIKE 'lmm\_%' ESCAPE '\' AND nspname <> $1",
    "datname LIKE '%core%'",
    r"datname LIKE 'lmm\_%' ESCAPE '\' AND datname <> $2",
    "has_database_privilege(current_user,oid,'CONNECT')",
    "pg_has_role(current_user,oid,'SET')",
    "pg_has_role(current_user,oid,'USAGE')",
    "pg_has_role(current_user,oid,'MEMBER')",
    'if name != c.Database || privileged || foreignSchema || coreConnect || rolePaths',
    'return nil, errors.New("extension storage guard unavailable")',
    'return nil, errors.New("extension storage isolation rejected")',
    'for _, privilege := range table.privileges',
    "has_table_privilege(current_user,to_regclass($1),$2)",
    'err != nil || !allowed',
    'return nil, errors.New("extension storage schema or runtime grants unavailable")',
    'case "operations":',
    'add("lmm_operations.contract", "SELECT")',
    'add("lmm_operations.publications", "SELECT", "INSERT")',
    'add("lmm_operations.reads", "SELECT", "INSERT")',
    "has_sequence_privilege(current_user,to_regclass('lmm_operations.publication_id'),$1)",
    'return nil, errors.New("operations sequence grants unavailable")',
    'if failed {',
    '_ = db.Close()',
)
COMPOSITION_RUNTIME_GUARDS = (
    'openStorage(ctx, cfg.Store)',
    'openStorage(ctx, cfg.Support)',
    'openStorage(ctx, cfg.Assistant)',
    'openStorage(ctx, cfg.Operations)',
    'store.NewPostgres(db)',
    'support.NewPostgres(db)',
    'var funds store.Funds = store.DisabledFunds{}',
    'if cfg.CommerceTokenFile != ""',
    'if err := independentRuntimeCredentials(cfg.HostCredential, token, commerceToken); err != nil',
    'rustfunds.New(r.CommerceClient, caps.GetFeatures())',
    'store.New(repo, auth, funds)',
)

# This remains an offline source contract, not an execution of Go. Tokenize
# literals/comments so a commented-out guard cannot satisfy a call/body check.
# Require the complete small pairwise helper, not an error-message substring.
GO_TOKEN = re.compile(
    r'//[^\n]*|/\*[\s\S]*?\*/|"(?:\\.|[^"\\])*"|`[^`]*`|'
    r"'(?:\\.|[^'\\])*'|[A-Za-z_]\w*|\d+|\.\.\.|:=|==|!=|&&|\|\||[^\s]"
)
RUNTIME_CREDENTIAL_BODY = """
for i, token := range credentials {
    if len(token) == 0 {
        continue
    }
    for _, other := range credentials[i+1:] {
        if len(other) != 0 && bytes.Equal(token, other) {
            return errors.New("host and core RPC credentials must be independent")
        }
    }
}
return nil
"""
PAYMENT_CREDENTIAL_BODY = """
paymentToken, err := coreclient.ReadTokenFile(cfg.PaymentTokenFile)
if err != nil {
    return nil, errors.New("private payment RPC credential unavailable")
}
if len(cfg.HostCredential) < 32 {
    return nil, errors.New("payment RPC requires an independent loaded host credential")
}
if err := independentRuntimeCredentials(cfg.HostCredential, identityToken, commerceToken, paymentToken); err != nil {
    return nil, err
}
return paymentToken, nil
"""
OPERATIONS_RUNTIME_BLOCK = """
if selected["operations"] {
    config := operations.Config{Scripts: cfg.Scripts}
    if cfg.Operations.URLFile != "" || cfg.Operations.Database != "" {
        db, err := openStorage(ctx, cfg.Operations)
        if err != nil { return nil, err }
        r.close = append(r.close, db.Close)
        config.Releases = db
    }
    if config.Releases == nil && config.Scripts == nil {
        return nil, errors.New("operations requires configured releases or reviewed scripts")
    }
    r.Operations, err = operations.New(ctx, config)
    if err != nil { return nil, err }
} else if cfg.Operations.URLFile != "" || cfg.Operations.Database != "" || cfg.Scripts != nil {
    return nil, errors.New("operations configuration requires explicit module selection")
}
"""
OPERATIONS_NEW_BODY = """
s := new(Service)
var err error
if c.Releases != nil {
    s.Releases, err = NewPostgres(ctx, c.Releases)
    if err != nil { return nil, err }
}
if c.Scripts != nil {
    s.Scripts, err = LoadScripts(*c.Scripts)
    if err != nil { return nil, err }
}
return s, nil
"""
OPERATIONS_LIST_BODY = """
if limit < 1 || limit > 50 { return nil, ErrInvalid }
ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
defer cancel()
rows, err := s.db.QueryContext(ctx, "SELECT "+noteColumns+" FROM lmm_operations.publications ORDER BY id DESC LIMIT $1", limit)
if err != nil { return nil, ErrUnavailable }
defer rows.Close()
out := []ReleaseNote{}
for rows.Next() {
    n, e := scanNote(rows)
    if e != nil { return nil, ErrUnavailable }
    out = append(out, *n)
}
if rows.Err() != nil { return nil, ErrUnavailable }
return out, nil
"""
OPERATIONS_SEQUENCE_BLOCK = """
if c.Component == "operations" {
    for _, privilege := range []string{"USAGE", "SELECT"} {
        var allowed bool
        if err := db.QueryRowContext(bounded, `SELECT COALESCE(has_sequence_privilege(current_user,to_regclass('lmm_operations.publication_id'),$1),false)`, privilege).Scan(&allowed); err != nil || !allowed {
            return nil, errors.New("operations sequence grants unavailable")
        }
    }
}
"""
OPERATIONS_CHECK_GUARDS = (
    "rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls",
    "FROM pg_auth_members", "has_database_privilege(current_database(),'CREATE')",
    "has_database_privilege(current_database(),'TEMP')", "has_schema_privilege(oid,'CREATE')",
    "c.relrowsecurity OR c.relforcerowsecurity", "FROM pg_policy",
    "'UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER'", "a.is_grantable OR a.privilege_type NOT IN",
    "aclexplode(a.attacl)", "has_sequence_privilege('lmm_operations.publication_id','USAGE')",
    "has_sequence_privilege('lmm_operations.publication_id','SELECT')",
    "NOT has_sequence_privilege('lmm_operations.publication_id','UPDATE')",
)


def go_tokens(source):
    return [token for token in GO_TOKEN.findall(source)
            if not token.startswith(("//", "/*"))]


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
        self.assertRegex(module, r"(?m)^\s*github\.com/lib/pq v[0-9.]+\s*$")
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
        self.assertLess(startup.index("validateEnvironment(os.Getenv)"), startup.index("LoadConfig(path)"))
        self.assertLess(startup.index("LoadConfig(path)"), startup.index("readHostCredential(cfg.Host.TokenFile)"))
        identity = (ROOT / "services/extensions/internal/modules/identity/identity.go").read_text()
        self.assertIn("type Core interface", identity)
        for operation in ("Capabilities", "Authorize", "ListTeams"):
            self.assertIn(operation, identity)
        self.assertIn("corepb", identity)

    def assert_extension_storage(self, path, text):
        stores = {
            "services/extensions/internal/coreclient/sqlinbox.go": "extension_events.",
            "services/extensions/internal/modules/store/postgres.go": "lmm_store.",
            "services/extensions/internal/modules/store/postgres_web.go": "lmm_store.",
            "services/extensions/internal/modules/support/postgres.go": "lmm_support.",
            "services/extensions/internal/modules/support/postgres_web.go": "lmm_support.",
            "services/extensions/internal/modules/assistant/postgres.go": "lmm_assistant.",
            "services/extensions/internal/modules/assistant/history_postgres.go": "lmm_assistant.",
            "services/extensions/internal/modules/promotions/postgres.go": "lmm_promotions.",
            "services/extensions/internal/modules/payments/postgres.go": "payment_",
            OPERATIONS_POSTGRES: "lmm_operations.",
            OPERATIONS_RELEASES: "lmm_operations.",
        }
        # Exactly one production file opens bounded pools. The operations owner
        # initializer imports pq only for reviewed identifier quoting; its
        # runtime constructor and adapters still borrow their own injected pool.
        for forbidden in ("gorm.io", "go-sql-driver", "jackc/pgx", "sqlite"):
            self.assertNotIn(forbidden, text, path)
        if path != COMPOSITION_STORAGE:
            if path == OPERATIONS_POSTGRES:
                self.assert_operations_postgres(text)
            else:
                self.assertNotIn("github.com/lib/pq", text, path)
                if path == OPERATIONS_RELEASES:
                    self.assert_operations_releases(text)
            self.assertNotRegex(text, r"\bsql\s*\.\s*(?:Open|OpenDB)\s*\(", path)
        if path == OPERATIONS_INJECTION:
            self.assert_operations_injection(text)
            for schema in ("core_meta.", "core_identity.", "core_billing.",
                           "core_events.", "core_runtime.") + tuple(stores.values()):
                self.assertNotIn(schema, text, path)
            return
        if path not in stores and path != COMPOSITION_STORAGE:
            self.assertNotIn('"database/sql"', text, path)
            return
        if path == COMPOSITION_STORAGE:
            self.assertIn('"github.com/lib/pq"', text, path)
            self.assertEqual(len(re.findall(r"\bsql\s*\.\s*(?:Open|OpenDB)\s*\(", text)), 1)
            for required in COMPOSITION_STORAGE_GUARDS:
                self.assertIn(required, text, path)
            self.assert_operations_storage(text)
        else:
            self.assertIn(stores[path], text, path)
        schemas = ("core_meta.", "core_identity.", "core_billing.",
                   "core_events.", "core_runtime.") + tuple(stores.values())
        for schema in schemas:
            if schema != stores.get(path):
                if path == COMPOSITION_STORAGE or schema == "payment_":
                    # Permission introspection names are not business-table
                    # access. The resource boundary must never query those rows.
                    qualifier = r"(?:public\.)?" if schema == "payment_" else ""
                    self.assertNotRegex(text, r"(?i)\b(?:FROM|JOIN|UPDATE|INTO|TRUNCATE)\s+" + qualifier + re.escape(schema), path)
                else:
                    self.assertNotIn(schema, text, path)

    def test_only_exact_composition_storage_file_may_import_driver_or_open_pools(self):
        for path in ("services/extensions/internal/app/unapproved.go",
                     "services/extensions/internal/composition/unapproved.go",
                     "services/extensions/internal/modules/store/postgres.go"):
            source = (ROOT / path).read_text() if (ROOT / path).is_file() else "package example\n"
            for forbidden in ('import _ "github.com/lib/pq"', 'sql.Open("postgres", dsn)',
                              'sql.OpenDB(connector)', 'sql . Open ("postgres", dsn)'):
                with self.subTest(path=path, forbidden=forbidden):
                    with self.assertRaises(AssertionError):
                        self.assert_extension_storage(path, source + "\n" + forbidden)
        with self.assertRaises(AssertionError):
            self.assert_extension_storage("services/extensions/internal/composition/unapproved.go",
                                          'package composition\nimport "database/sql"')

    def test_composition_storage_guards_cannot_be_removed_or_gain_table_access(self):
        source = (ROOT / COMPOSITION_STORAGE).read_text()
        self.assert_extension_storage(COMPOSITION_STORAGE, source)
        for required in COMPOSITION_STORAGE_GUARDS:
            with self.subTest(required=required), self.assertRaises(AssertionError):
                self.assert_extension_storage(COMPOSITION_STORAGE, source.replace(required, ""))
        for forbidden in ("core_identity.users", "core_billing.balance_state", "core_events.outbox",
                          "lmm_store.orders", "lmm_support.messages", "lmm_assistant.records",
                          "lmm_promotions.records", "payment_orders", 'sql.OpenDB(connector)'):
            with self.subTest(forbidden=forbidden), self.assertRaises(AssertionError):
                self.assert_extension_storage(COMPOSITION_STORAGE, source + "\nSELECT * FROM " + forbidden)

    def assert_composition_runtime(self, text):
        for required in COMPOSITION_RUNTIME_GUARDS:
            self.assertIn(required, text)
        self.assertEqual(text.count("openStorage("), 4)
        self.assertEqual(text.count("r.close = append(r.close, db.Close)"), 4)
        body = self.go_function_body(
            text, "func New(ctx context.Context, cfg Config) (_ *Runtime, resultErr error)")
        self.token_index(body, OPERATIONS_RUNTIME_BLOCK)
        self.token_index(body, "defer func() { if resultErr != nil { _ = r.Close() } }()")
        close = self.go_function_body(text, "func (r *Runtime) Close() error")
        self.token_index(close, """
            for i := len(r.close) - 1; i >= 0; i-- {
                err = errors.Join(err, r.close[i]())
            }
            r.close = nil
        """)

    def token_index(self, tokens, source):
        expected = go_tokens(source)
        positions = [i for i in range(len(tokens) - len(expected) + 1)
                     if tokens[i:i + len(expected)] == expected]
        self.assertEqual(len(positions), 1, source)
        return positions[0]

    def go_function_body(self, source, signature):
        tokens = go_tokens(source)
        header = go_tokens(signature + " {")
        start = self.token_index(tokens, signature + " {") + len(header)
        depth = 1
        for end in range(start, len(tokens)):
            if tokens[end] == "{":
                depth += 1
            elif tokens[end] == "}":
                depth -= 1
                if depth == 0:
                    return tokens[start:end]
        self.fail("unterminated Go guard function: " + signature)

    def assert_named_go_import(self, text, package):
        tokens = go_tokens(text)
        imports = self.token_index(tokens, "import (") + 2
        end = tokens.index(")", imports)
        imported = self.token_index(tokens[imports:end], json.dumps(package)) + imports
        self.assertTrue(tokens[imported - 1] == "(" or tokens[imported - 1].startswith('"'))
        return tokens

    def assert_no_close_declaration(self, tokens):
        for index, token in enumerate(tokens[:-1]):
            if token != "func":
                continue
            name = index + 1
            if tokens[name] == "(":
                name = tokens.index(")", name) + 1
            self.assertNotEqual(tokens[name], "Close")

    def assert_sql_selectors(self, tokens, expected):
        selectors = [tokens[i + 2] for i in range(len(tokens) - 2)
                     if tokens[i:i + 2] == ["sql", "."]]
        self.assertEqual(selectors, expected)

    def assert_operations_postgres(self, text):
        tokens = self.assert_named_go_import(text, "github.com/lib/pq")
        self.assert_named_go_import(text, "database/sql")
        self.assert_sql_selectors(tokens, ["DB", "DB", "DB"])
        # The existing default named pq import is allowed for the owner-only
        # quoting API. Blank/aliased imports and every connector API stay barred.
        owner = self.go_function_body(
            text, "func Initialize(ctx context.Context, owner *sql.DB, runtimeRole string) error")
        expected = [go_tokens("pq.QuoteIdentifier(runtimeRole)"),
                    go_tokens("pq.QuoteIdentifier(database)"),
                    go_tokens("pq.QuoteIdentifier(database)")]
        for scope in (tokens, owner):
            calls = [scope[i:i + 6] for i in range(len(scope) - 1)
                     if scope[i:i + 2] == ["pq", "."]]
            self.assertEqual(calls, expected)
        constructor = self.go_function_body(
            text, "func NewPostgres(ctx context.Context, db *sql.DB) (*Releases, error)")
        self.assertEqual(constructor, go_tokens("""
            if err := Check(ctx, db); err != nil { return nil, err }
            return &Releases{db}, nil
        """))
        self.assertFalse(any(tokens[i:i + 2] == [".", "Close"] for i in range(len(tokens) - 1)))
        self.assert_no_close_declaration(tokens)
        check = self.go_function_body(text, "func Check(ctx context.Context, runtime *sql.DB) error")
        self.token_index(check, "runtime.QueryRowContext(ctx, isolatedSQL+")
        self.token_index(check, "runtime.QueryRowContext(ctx, fingerprintSQL).Scan(&actual)")
        self.token_index(check, "installed != actual || len(actual) != 32")
        literals = [token[1:-1] for token in check if token.startswith(('"', '`'))]
        for required in OPERATIONS_CHECK_GUARDS:
            self.assertTrue(any(required in literal for literal in literals), required)

    def assert_operations_releases(self, text):
        tokens = self.assert_named_go_import(text, "database/sql")
        self.assert_sql_selectors(tokens, ["DB", "ErrNoRows", "ErrNoRows"])
        closes = [tokens[i - 1:i + 2] for i in range(1, len(tokens) - 1)
                  if tokens[i:i + 2] == [".", "Close"]]
        self.assertEqual(closes, [["rows", ".", "Close"]])
        listing = self.go_function_body(text, "func (s *Releases) List(ctx context.Context, limit int) ([]ReleaseNote, error)")
        self.token_index(listing, "defer rows.Close()")
        # Pin the complete reviewed query/scan lifetime. A token named rows can
        # otherwise shadow the query result with the caller's sql.DB and close
        # that pool while satisfying the same Close selector/name checks.
        self.assertEqual(listing, go_tokens(OPERATIONS_LIST_BODY))
        self.assert_no_close_declaration(tokens)

    def assert_operations_injection(self, text):
        tokens = self.assert_named_go_import(text, "database/sql")
        self.assert_sql_selectors(tokens, ["DB"])
        self.token_index(tokens, "type Config struct { Releases *sql.DB Scripts *ScriptConfig }")
        body = self.go_function_body(text, "func New(ctx context.Context, c Config) (*Service, error)")
        self.assertEqual(body, go_tokens(OPERATIONS_NEW_BODY))
        availability = self.go_function_body(text, "func (s *Service) Availability() Availability")
        self.assertEqual(availability, go_tokens("""
            if s == nil { return Availability{} }
            return Availability{s.Releases != nil, s.Scripts != nil, false}
        """))
        # Every pool-field access is in those two complete reviewed bodies;
        # another function cannot query, close, alias or export the injected pool.
        accesses = lambda scope: sum(scope[i:i + 2] == [".", "Releases"]
                                     for i in range(len(scope) - 1))
        self.assertEqual(accesses(tokens), 4)
        self.assertEqual(accesses(body) + accesses(availability), 4)
        self.assert_no_close_declaration(tokens)

    def assert_operations_storage(self, text):
        body = self.go_function_body(
            text, "func openStorage(ctx context.Context, c StorageConfig) (*sql.DB, error)")
        self.token_index(body, OPERATIONS_SEQUENCE_BLOCK)
        tables = self.go_function_body(text, "func storageTables(component string) []storageTable")
        self.token_index(tables, """
            case "operations":
                add("lmm_operations.contract", "SELECT")
                add("lmm_operations.publications", "SELECT", "INSERT")
                add("lmm_operations.reads", "SELECT", "INSERT")
            case "payments":
        """)

    def assert_runtime_credentials(self, runtime, payments, startup):
        pairwise = self.go_function_body(
            payments, "func independentRuntimeCredentials(credentials ...[]byte) error")
        self.assertEqual(pairwise, go_tokens(RUNTIME_CREDENTIAL_BODY))
        loader = self.go_function_body(
            payments, "func loadPaymentCredential(cfg Config, identityToken, commerceToken []byte) ([]byte, error)")
        self.assertEqual(loader, go_tokens(PAYMENT_CREDENTIAL_BODY))
        body = self.go_function_body(
            runtime, "func New(ctx context.Context, cfg Config) (_ *Runtime, resultErr error)")
        guard = self.token_index(body, """
            if err := independentRuntimeCredentials(cfg.HostCredential, token, commerceToken); err != nil {
                return nil, err
            }
        """)
        payment = self.token_index(body, """
            if selected["payments"] {
                paymentToken, err := loadPaymentCredential(cfg, token, commerceToken)
                if err != nil { return nil, err }
                if err := r.composePayments(ctx, cfg, paymentToken); err != nil {
                    return nil, err
                }
            }
        """)
        self.assertLess(guard, payment)
        self.assertLess(guard, self.token_index(body, "openStorage(ctx, cfg.Store)"))
        self.assertLess(guard, self.token_index(
            body, "r.CommerceClient, err = coreclient.New(cfg.Socket, commerceToken)"))
        compose = self.go_function_body(
            payments, "func (r *Runtime) composePayments(ctx context.Context, cfg Config, paymentToken []byte) error")
        self.token_index(compose, """
            r.PaymentClient, err = coreclient.New(cfg.Socket, paymentToken)
            if err != nil { return err }
        """)
        host_loader = self.go_function_body(
            startup, "func readHostCredential(path string) ([]byte, error)")
        self.assertEqual(host_loader, go_tokens("return coreclient.ReadTokenFile(path)"))
        run = self.go_function_body(startup, "func Run() error")
        loaded = self.token_index(run, """
            credential, err := readHostCredential(cfg.Host.TokenFile)
            if err != nil { return err }
        """)
        composition = self.token_index(run, """
            runtime, err := composition.New(context.Background(), composition.Config{
                Selected: selected, Socket: socket, TokenFile: tokenFile, HostCredential: credential,
        """)
        self.assertLess(loaded, composition)

    def test_composition_keeps_distinct_module_pools_and_disabled_funds(self):
        source = (ROOT / "services/extensions/internal/composition/runtime.go").read_text()
        self.assert_composition_runtime(source)
        for required in COMPOSITION_RUNTIME_GUARDS:
            with self.subTest(required=required), self.assertRaises(AssertionError):
                self.assert_composition_runtime(source.replace(required, ""))

    def test_central_credentials_guard_keeps_all_loaded_roles_pairwise_distinct(self):
        runtime = (ROOT / "services/extensions/internal/composition/runtime.go").read_text()
        payments = (ROOT / COMPOSITION_PAYMENTS).read_text()
        startup = (ROOT / "services/extensions/internal/app/run.go").read_text()
        self.assert_runtime_credentials(runtime, payments, startup)
        for required in ("credentials ...[]byte", "for i, token := range credentials",
                         "len(token) == 0", "credentials[i+1:]", "len(other) != 0",
                         "bytes.Equal(token, other)",
                         'errors.New("host and core RPC credentials must be independent")'):
            with self.subTest(removed=required), self.assertRaises(AssertionError):
                self.assert_runtime_credentials(runtime, payments.replace(required, ""), startup)
        # Each of the six possible role aliases must invalidate the exact
        # loaded-byte call. Empty optional roles are handled by the real helper.
        roles = ["cfg.HostCredential", "identityToken", "commerceToken", "paymentToken"]
        original = "independentRuntimeCredentials(" + ", ".join(roles) + ")"
        for i in range(len(roles)):
            for j in range(i + 1, len(roles)):
                aliased = list(roles)
                aliased[j] = aliased[i]
                changed = "independentRuntimeCredentials(" + ", ".join(aliased) + ")"
                with self.subTest(pair=(roles[i], roles[j])), self.assertRaises(AssertionError):
                    self.assert_runtime_credentials(runtime, payments.replace(original, changed), startup)

    def test_actual_startup_uses_checked_credentials_before_financial_dependencies(self):
        runtime = (ROOT / "services/extensions/internal/composition/runtime.go").read_text()
        payments = (ROOT / COMPOSITION_PAYMENTS).read_text()
        startup = (ROOT / "services/extensions/internal/app/run.go").read_text()
        self.assert_runtime_credentials(runtime, payments, startup)
        guard = "independentRuntimeCredentials(cfg.HostCredential, token, commerceToken)"
        for changed in (runtime.replace(guard, "independentRuntimeCredentials(nil, token, commerceToken)"),
                        runtime.replace(guard, "nil /* " + guard + " */"),
                        runtime.replace(guard, guard + "; false"),
                        runtime.replace("loadPaymentCredential(cfg, token, commerceToken)",
                                        "loadPaymentCredential(cfg, token, token)"),
                        runtime.replace("r.composePayments(ctx, cfg, paymentToken)",
                                        "r.composePayments(ctx, cfg, token)")):
            with self.subTest(runtime=changed), self.assertRaises(AssertionError):
                self.assert_runtime_credentials(changed, payments, startup)
        for required in ("coreclient.ReadTokenFile(cfg.PaymentTokenFile)",
                         "len(cfg.HostCredential) < 32", "return paymentToken, nil"):
            with self.subTest(removed=required), self.assertRaises(AssertionError):
                self.assert_runtime_credentials(runtime, payments.replace(required, ""), startup)
        with self.assertRaises(AssertionError):
            self.assert_runtime_credentials(runtime, payments.replace(
                "coreclient.New(cfg.Socket, paymentToken)", "coreclient.New(cfg.Socket, identityToken)"), startup)
        for old, new in (("HostCredential: credential", "HostCredential: nil"),
                         ("credential, err := readHostCredential(cfg.Host.TokenFile)",
                          "credential, err := []byte(nil), error(nil)"),
                         ("return coreclient.ReadTokenFile(path)", "return nil, nil")):
            with self.subTest(startup=old), self.assertRaises(AssertionError):
                self.assert_runtime_credentials(runtime, payments, startup.replace(old, new))

    def test_operations_adapters_borrow_and_keep_schema_boundaries(self):
        paths = (OPERATIONS_POSTGRES, OPERATIONS_RELEASES, OPERATIONS_INJECTION)
        for path in paths:
            source = (ROOT / path).read_text()
            self.assert_extension_storage(path, source)
            for forbidden in ("core_identity.users", "core_billing.balance_state", "core_events.outbox",
                              "lmm_store.orders", "lmm_support.messages", "lmm_assistant.records",
                              "lmm_promotions.records", "payment_orders"):
                with self.subTest(path=path, table=forbidden), self.assertRaises(AssertionError):
                    self.assert_extension_storage(path, source + "\nSELECT * FROM " + forbidden)
            for opener in ('sql.Open("postgres", dsn)', 'sql.OpenDB(connector)',
                           'sql . Open ("postgres", dsn)'):
                with self.subTest(path=path, opener=opener), self.assertRaises(AssertionError):
                    self.assert_extension_storage(path, source + "\n" + opener)
            aliased = source.replace('"database/sql"', 'dbsql "database/sql"').replace("sql.", "dbsql.")
            aliased += '\nfunc unapproved() { _, _ = dbsql.Open("postgres", "synthetic") }'
            with self.subTest(path=path, alias=True), self.assertRaises(AssertionError):
                self.assert_extension_storage(path, aliased)
            for method in ("Open", "OpenDB"):
                with self.subTest(path=path, method_value=method), self.assertRaises(AssertionError):
                    self.assert_extension_storage(path, source + "\nvar unapproved = sql." + method)
        releases = (ROOT / OPERATIONS_RELEASES).read_text()
        for added in ("func (s *Releases) Close() error { return s.db.Close() }",
                      "func (s *Releases) closeBorrowed() error { borrowed := s.db; return borrowed.Close() }"):
            with self.subTest(releases=added), self.assertRaises(AssertionError):
                self.assert_extension_storage(OPERATIONS_RELEASES, releases + "\n" + added)
        injection = (ROOT / OPERATIONS_INJECTION).read_text()
        for forbidden in ("lmm_operations.publications", "lmm_operations.reads"):
            with self.subTest(injection=forbidden), self.assertRaises(AssertionError):
                self.assert_extension_storage(OPERATIONS_INJECTION,
                                              injection + "\nSELECT * FROM " + forbidden)
        for old, new in (("Releases *sql.DB", "Releases *DatabaseConfig"),
                         ("NewPostgres(ctx, c.Releases)", "Open(ctx, *c.Releases)"),
                         ("return s, nil", "c.Releases.Close(); return s, nil")):
            with self.subTest(injection=old), self.assertRaises(AssertionError):
                self.assert_extension_storage(OPERATIONS_INJECTION, injection.replace(old, new))
        for added in ("func extra(c Config) error { return c.Releases.Close() }",
                      'func extra(c Config) { c.Releases.QueryContext(context.Background(), "SELECT 1") }',
                      "func extra(c Config) { borrowed := c.Releases; borrowed.Close() }",
                      "func (s *Service) Close() error { return nil }"):
            with self.subTest(injection_extra=added), self.assertRaises(AssertionError):
                self.assert_extension_storage(OPERATIONS_INJECTION, injection + "\n" + added)
        for path in ("services/extensions/internal/modules/store/postgres.go",
                     "services/extensions/internal/modules/support/postgres.go",
                     "services/extensions/internal/modules/assistant/postgres.go",
                     "services/extensions/internal/modules/promotions/postgres.go",
                     "services/extensions/internal/modules/payments/postgres.go"):
            source = (ROOT / path).read_text()
            with self.subTest(other_module=path), self.assertRaises(AssertionError):
                self.assert_extension_storage(path, source + "\nSELECT * FROM lmm_operations.publications")

    def test_operations_pool_is_guarded_and_owned_by_composition(self):
        runtime = (ROOT / "services/extensions/internal/composition/runtime.go").read_text()
        storage = (ROOT / COMPOSITION_STORAGE).read_text()
        self.assert_composition_runtime(runtime)
        self.assert_extension_storage(COMPOSITION_STORAGE, storage)
        for old, new in (("openStorage(ctx, cfg.Operations)", "storageURL(cfg.Operations)"),
                         ("if resultErr != nil", "if false"),
                         ("i := len(r.close) - 1", "i := 0"),
                         ("config.Releases = db", "config.Releases = nil"),
                         ("config.Releases = db", "db.Close(); config.Releases = db"),
                         ("r.Operations, err = operations.New(ctx, config)",
                          "r.Operations, err = operations.New(ctx, operations.Config{})")):
            with self.subTest(runtime=old), self.assertRaises(AssertionError):
                self.assert_composition_runtime(runtime.replace(old, new))
        for required in ('add("lmm_operations.contract", "SELECT")',
                         'add("lmm_operations.publications", "SELECT", "INSERT")',
                         'add("lmm_operations.reads", "SELECT", "INSERT")',
                         "has_sequence_privilege(current_user,to_regclass('lmm_operations.publication_id'),$1)",
                         "has_database_privilege(current_user,oid,'CONNECT')",
                         "pg_has_role(current_user,oid,'SET')"):
            with self.subTest(storage=required), self.assertRaises(AssertionError):
                self.assert_extension_storage(COMPOSITION_STORAGE, storage.replace(required, ""))
        for old, new in (('[]string{"USAGE", "SELECT"}', '[]string{"SELECT"}'),
                         ('add("lmm_operations.reads", "SELECT", "INSERT")',
                          'add("lmm_operations.reads", "SELECT", "INSERT", "UPDATE")')):
            with self.subTest(storage=old), self.assertRaises(AssertionError):
                self.assert_extension_storage(COMPOSITION_STORAGE, storage.replace(old, new))

    def test_operations_pq_is_only_offline_identifier_quoting(self):
        source = (ROOT / OPERATIONS_POSTGRES).read_text()
        self.assert_extension_storage(OPERATIONS_POSTGRES, source)
        changes = (
            source + "\nvar unapproved = pq.NewConnector(dsn)",
            source + '\nvar unapproved = pq.QuoteIdentifier("runtime")',
            source.replace('"github.com/lib/pq"', '_ "github.com/lib/pq"'),
            source.replace('"github.com/lib/pq"', '. "github.com/lib/pq"'),
            source.replace('"github.com/lib/pq"', 'custom "github.com/lib/pq"'),
            source.replace("pq.QuoteIdentifier(runtimeRole)", "runtimeRole"),
            source.replace("pq.QuoteIdentifier(database)", "database"),
            source.replace("if err := Check(ctx, db); err != nil {", "if err := error(nil); err != nil {"),
            source.replace("return &Releases{db}, nil", "db.Close(); return &Releases{db}, nil"),
            source.replace("return &Releases{db}, nil", "Initialize(ctx, db, role); return &Releases{db}, nil"),
        )
        for index, changed in enumerate(changes):
            with self.subTest(change=index), self.assertRaises(AssertionError):
                self.assert_extension_storage(OPERATIONS_POSTGRES, changed)
        for required in OPERATIONS_CHECK_GUARDS:
            with self.subTest(check=required), self.assertRaises(AssertionError):
                self.assert_extension_storage(OPERATIONS_POSTGRES, source.replace(required, ""))

    def test_operations_rows_close_cannot_alias_the_borrowed_pool(self):
        source = (ROOT / OPERATIONS_RELEASES).read_text()
        self.assert_extension_storage(OPERATIONS_RELEASES, source)
        signature = "func (s *Releases) List(ctx context.Context, limit int) ([]ReleaseNote, error)"
        start = source.index(signature)
        end = source.index("\n}\n", start) + len("\n}")
        original = source[start:end]
        mutants = {
            "direct_alias": signature + " {\nrows := s.db\ndefer rows.Close()\nreturn []ReleaseNote{}, nil\n}",
            "nested_shadow": original.replace("defer rows.Close()",
                "{ rows := s.db; defer rows.Close() }"),
            "method_value": signature + " {\nrows := s.db\ncloseBorrowed := rows.Close\ndefer closeBorrowed()\nreturn []ReleaseNote{}, nil\n}",
        }
        for name, mutant in mutants.items():
            with self.subTest(alias=name), self.assertRaises(AssertionError):
                self.assert_extension_storage(OPERATIONS_RELEASES, source[:start] + mutant + source[end:])

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
                    self.assert_extension_storage(path, source + "\nSELECT * FROM " + forbidden)
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
                        self.assert_extension_storage(path, source + "\nSELECT * FROM " + forbidden)
            for other in ("lmm_assistant.", "lmm_promotions.", "payment_"):
                if other != own:
                    with self.assertRaises(AssertionError):
                        self.assert_extension_storage(path, source + "\nSELECT * FROM " + other + "records")

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
        self.assertFalse((ROOT / ".env.example").exists())
        for component in ("core", "extensions"):
            sample = tomllib.loads((ROOT / f"config/{component}.example.toml").read_text())
            self.assertEqual(sample["version"], 1)
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
        configuration = tomllib.loads((ROOT / "deployment/docker/config/core-standalone.toml").read_text())
        self.assertEqual(configuration["server"]["worker_threads"], 1)
        self.assertEqual(configuration["server"]["max_inflight"], 16)
        self.assertEqual(configuration["database"]["max_connections"], 2)
        self.assertEqual(configuration["identity"]["password_slots"], 1)
        self.assertEqual(configuration["database"]["url_file"], "/run/secrets/core_database_url")
        self.assertIn("./config/core-standalone.toml", compose)
        self.assertNotIn("extensions:", compose)
        self.assertNotIn("core-db:", compose)
        self.assertIn("name: ${COMPOSE_PROJECT_NAME}-runtime", compose)
        core = (ROOT / "services/core/src/main.rs").read_text()
        self.assertIn("config::database_pool", core)
        configuration = (ROOT / "services/core/src/config.rs").read_text()
        self.assertIn("bounded_database_pool(url, database_connections()?)", configuration)
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
