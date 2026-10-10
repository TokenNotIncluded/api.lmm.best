# Local development

This branch is a fresh-install microkernel WIP. Do not connect it to an existing site database. There is no data-import, schema-upgrade, backfill, balance conversion or dual-write workflow.

## Services

- `apps/lmm-core`: authoritative identity, accounts and permissions. Billing and model forwarding are not complete.
- `apps/lmm-extensions`: the Go extension host. Only read-only identity queries are implemented.
- `apps/web`: retained frontend source; the full application APIs are not connected yet.
- `apps/lmm`: the separate CLI, not the core server.

Rust uses the pinned toolchain in `apps/lmm-core/rust-toolchain.toml`. The verification workflow pins Go 1.27.2. Web development uses Bun 1.3.14 and Node.js 22.12 or later.

## Start a development stack

Use [the Docker instructions](../deployment/docker/README.md) for secrets, an isolated PostgreSQL database and the private RPC socket. The explicit `lmm-core-admin init-db` command installs an empty database once. It refuses an existing installation. Service startup only checks the installed contract.

Core and extensions are separate Compose projects. Updating the extensions project must not replace the core or its database. Never use a database reset or delete-volume command as an upgrade path.

## Run the Go host locally

Go reads exported environment variables. It does **not** load `.env` automatically.

```sh
umask 077
# Fail rather than replace an existing credential.
(set -C; openssl rand -hex 32 > /tmp/lmm-extension-dev-token)
export LMM_EXTENSION_LISTEN=127.0.0.1:8081
export LMM_EXTENSION_TOKEN_FILE=/tmp/lmm-extension-dev-token
export LMM_EXTENSION_MODULES=none
just dev-go
```

The `none` configuration starts only the host and its health/inventory endpoints. To enable `identity`, configure both `LMM_CORE_RPC_SOCKET` and `LMM_CORE_RPC_TOKEN_FILE`, and set `LMM_EXTENSION_MODULES=identity`. The core remains responsible for user authorization.

Do not inherit `SQL_DSN`, `LOG_SQL_DSN`, `DATABASE_URL`, `LMM_CORE_DATABASE_URL`, `LMM_CORE_DATABASE_URL_FILE` or `LMM_DB_MIGRATION_MODE`. Go rejects these non-empty settings before file or network access. Each future business module must have its own explicitly named storage configuration.

## Verification

```sh
just check-boundaries
just check-protocol
just test-go
just test-core
just test-docker
```

The Rust database tests require a disposable local PostgreSQL service and test-only `DATABASE_URL`. Do not export that variable into the Go server process. The Docker test uses randomly named temporary projects and only removes its own test volumes.

`core-protocol.yml` runs the Rust database tests, Go race tests, generated protocol checks, source boundaries and actual Docker communication tests. Both manual `ci.yml` and `server-release-qualification.yml` call this same workflow. There is no old Go migration or package-release path.

The optional full manual CI also retains the frontend checks and translation regression test. A green implemented-component check does not mean billing, streaming or the missing business extensions are ready for production.

## Frontend

```sh
just setup
bun run --filter @lmm/web dev --port 5173 --host 127.0.0.1 --strict-port
```

This starts frontend development, not a complete working application. Do not treat host health checks as availability of the old `/api/*` routes. No local setup wizard or browser login is promised by the current core.
