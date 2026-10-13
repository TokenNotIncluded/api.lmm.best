# Go extension service

This directory is the canonical Go extension process for the fresh-install microkernel branch. It replaces the monolithic API process; it is not a compatibility wrapper around its router, database, or workers. The previous implementation remains in Git history, not in the runtime dependency graph.

**WIP, breaking change:** the process explicitly assembles `identity`, `store`, `support`, `assistant` and `operations`. Store spending additionally requires a separate Core commerce credential and verified capabilities. Assistant history/greetings and operations release notes/reviewed scripts do not imply live assistant/model execution. Tool-market, promotions and payment modules are not assembled into this process yet. Removing the monolithic runtime is not completion of those business features. Do not deploy this branch as a replacement for the current site.

## Ownership

Rust owns users, sessions, teams, permissions, funds and model traffic. Go gets a bounded Protobuf client, never a core database connection. A module must authenticate the user's own credential through Rust. A service token is not a user identity or a spending grant. There is no local identity fallback, balance writer, automatic schema migration or model relay.

Each future business module owns its own storage and must receive only its narrow dependencies at assembly time. Do not introduce a shared global ORM, generic SQL RPC, shared wallet, or a second permission authority. Compile-time modules are trusted Go code, not a security sandbox; hard process isolation requires separate extension containers.

## Build and run

From this directory:

```sh
go test -race ./...
go build -trimpath -o lmm-extensions ./cmd/extensions
```

Run `./lmm-extensions --config /absolute/private/extensions.toml` with the versioned, strict TOML contract described in [development](../../docs/development.md). The default path is `/etc/lmm.conf.d/extensions.toml`; `.env` files are not read. Exported configuration is accepted only with explicit `--legacy-env`; the following `LMM_*` examples describe that legacy mode, not the default file mode. It rejects non-empty `SQL_DSN`, `LOG_SQL_DSN`, `DATABASE_URL`, `LMM_CORE_DATABASE_URL`, `LMM_CORE_DATABASE_URL_FILE` and `LMM_DB_MIGRATION_MODE` before any file or network access. Errors contain variable names, never their values. Do not inherit the core process environment.

Configure `LMM_EXTENSION_TOKEN_FILE` with a private service credential file. `LMM_EXTENSION_LISTEN` defaults to `0.0.0.0:8081`. Configure both `LMM_CORE_RPC_SOCKET` and `LMM_CORE_RPC_TOKEN_FILE` to enable identity reads. Construction is lazy, so an offline Rust core does not prevent the extension host from starting. Identity requests fail closed while Rust is unavailable.

`LMM_EXTENSION_MODULES` selects a comma-separated list; `none` disables every module. Empty configuration enables only the explicit defaults: identity when RPC is configured, otherwise no modules. Unknown or duplicate names fail startup. New modules are not enabled automatically.

Requests use `/extensions/v1/<module>/...`. The inventory is `/extensions/v1/modules`; both require the host service credential. The host removes that credential before calling the module. Identity operations additionally use `X-LMM-User-Credential`. Do not give the host service credential to a browser.

Each module admits at most eight requests at once. A full module returns 503 without queueing. Health routes do not wait for core or modules; host readiness does not assert that all business capabilities are implemented. Request bodies and headers are bounded. SIGTERM drains requests with a bounded shutdown period.

Docker builds this directory using `deployment/docker/extensions.Dockerfile`. Update only the extensions Compose project. Never include the Rust service or its database in the extension deployment command.

## Operations content

Select `operations` explicitly in `host.modules`. A configured component must
load successfully; startup never installs or migrates its database. Releases
use their own restricted runtime role and operations-only database. Scripts are
an immutable snapshot of an explicit installation-reviewed SHA-256 manifest.
Either component may be omitted, but selecting the module requires at least one.
Operations configuration is available in file mode, not legacy environment mode.
For example, append these optional sections to a configuration with the existing
host and Core RPC credentials (these paths are placeholders, not setup commands):

```toml
[operations]
database_name = "lmm_operations_local"
database_url_file = "/absolute/private/operations-runtime.url"

[scripts]
root = "/absolute/reviewed-scripts"
manifest = [{ name = "install.sh", sha256 = "REPLACE_WITH_REVIEWED_LOWERCASE_SHA256" }]
```

`manifest = []` means an explicitly reviewed empty catalogue; it still requires
an accessible root. `host.web_compat = true` mounts `/api/release-notes/*`,
`/api/scripts`, `/api/scripts/*` and `/scripts/*` through host admission and drain.
The module-host API remains protected by the separate host credential and fresh
Core user-session verification where required. The actual listener guard rejects
escaped/traversal script aliases before any shared mux can redirect them.

See the [operations contract](internal/modules/operations/README.md) for offline
fresh-database initialization, authority, replay, isolation and snapshot bounds.
This does not enable script execution, repository fetching or an existing-site
schema upgrade. A missing component or dependency fails, never returns invented
successful empty content.

## Public policy documents

`legal.config_file` in TOML (or `LMM_LEGAL_CONFIG_FILE` in explicit legacy mode) optionally selects an external, operator-reviewed policy snapshot. Only `/api/user-agreement`, `/api/privacy-policy` and `/api/refund-policy` are public read routes; protected module routes retain their existing authentication. Draft or missing configuration returns no policy content. Invalid published content fails startup. Policy requests never access the core or a database.

See [policy configuration](../../docs/legal/README.md). Default drafts are shipped as files, not compiled into Go. Change the private configuration and restart only extensions to publish a new snapshot. Existing console settings are not an alternate configuration store.
