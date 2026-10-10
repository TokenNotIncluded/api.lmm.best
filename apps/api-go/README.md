# Go extension service

This directory is the canonical Go extension process for the fresh-install microkernel branch. It replaces the monolithic API process; it is not a compatibility wrapper around its router, database, or workers. The previous implementation remains in Git history, not in the runtime dependency graph.

**WIP, breaking change:** only the read-only `identity` module is connected. Shop, tool-market, assistant, support, promotions, payment integrations and the old console APIs are not implemented by this host yet. Removing the monolithic runtime is not completion of those business features. Do not deploy this branch as a replacement for the current site.

## Ownership

Rust owns users, sessions, teams, permissions, funds and model traffic. Go gets a bounded Protobuf client, never a core database connection. A module must authenticate the user's own credential through Rust. A service token is not a user identity or a spending grant. There is no local identity fallback, balance writer, automatic schema migration or model relay.

Each future business module owns its own storage and must receive only its narrow dependencies at assembly time. Do not introduce a shared global ORM, generic SQL RPC, shared wallet, or a second permission authority. Compile-time modules are trusted Go code, not a security sandbox; hard process isolation requires separate extension containers.

## Build and run

From this directory:

```sh
go test -race ./...
go build -trimpath -o lmm-extensions ./cmd/extensions
```

The process reads exported environment variables, not `.env` files. It rejects non-empty `SQL_DSN`, `LOG_SQL_DSN`, `DATABASE_URL`, `LMM_CORE_DATABASE_URL`, `LMM_CORE_DATABASE_URL_FILE` and `LMM_DB_MIGRATION_MODE` before any file or network access. Errors contain variable names, never their values. Do not inherit the core process environment.

Configure `LMM_EXTENSION_TOKEN_FILE` with a private service credential file. `LMM_EXTENSION_LISTEN` defaults to `0.0.0.0:8081`. Configure both `LMM_CORE_RPC_SOCKET` and `LMM_CORE_RPC_TOKEN_FILE` to enable identity reads. Construction is lazy, so an offline Rust core does not prevent the extension host from starting. Identity requests fail closed while Rust is unavailable.

`LMM_EXTENSION_MODULES` selects a comma-separated list; `none` disables every module. Empty configuration enables only the explicit defaults: identity when RPC is configured, otherwise no modules. Unknown or duplicate names fail startup. New modules are not enabled automatically.

Requests use `/extensions/v1/<module>/...`. The inventory is `/extensions/v1/modules`; both require the host service credential. The host removes that credential before calling the module. Identity operations additionally use `X-LMM-User-Credential`. Do not give the host service credential to a browser.

Each module admits at most eight requests at once. A full module returns 503 without queueing. Health routes do not wait for core or modules; host readiness does not assert that all business capabilities are implemented. Request bodies and headers are bounded. SIGTERM drains requests with a bounded shutdown period.

Docker builds this directory using `deployment/docker/extensions.Dockerfile`. Update only the extensions Compose project. Never include the Rust service or its database in the extension deployment command.
