# Local development

[Documentation index](README.md) · [中文首页](../README.md) · [English overview](../README_EN.md)

Use this guide for a development instance. For an existing production server, use the [deployment guides](README.md).

## Requirements

| Tool or service | Requirement |
| --- | --- |
| Git and Just | Clone the repository and run the recipes in [`justfile`](../justfile). |
| Bun | 1.3.14, pinned in [`package.json`](../package.json). |
| Node.js | 22.12 or newer. |
| Go | 1.25.1 or newer for the default backend. |
| PostgreSQL and Valkey | Dedicated development services, separate from production. |
| Rust | 1.99.0 for the WIP core (see its rust-toolchain.toml). |

## Set up

```bash
git clone https://github.com/TokenNotIncluded/api.lmm.best.git
cd api.lmm.best
just setup
cp .env.example apps/api-go/.env
```

The Go development process starts from `apps/api-go`. It reads `.env` there, not the copy at the repository root.

Edit the new file before starting the service:

| Variable | Set it to |
| --- | --- |
| `SQL_DSN` | Your development PostgreSQL connection string. URL-encode reserved characters in credentials. |
| `REDIS_CONN_STRING` | Your development Valkey connection string. |
| `SESSION_SECRET` | A new random secret. Keep it stable across restarts. |
| `CRYPTO_SECRET` | A different random secret. Keep it stable across restarts. |
| `SERVER_ADDRESS` | An explicit public HTTPS origin before enabling OAuth or external callbacks. |

Generate each secret separately. Do not copy production credentials or commit `.env`. Startup applies schema migrations by default, so use a dedicated development database.

## Start the application

Run the Go backend from the repository root:

```bash
just dev-go
```

In a second terminal, also from the repository root:

```bash
bun run --filter @lmm/web dev --port 5173 --host 127.0.0.1 --strict-port
```

Open <http://localhost:5173> and complete setup. The API listens on `127.0.0.1:3000`. The frontend proxies API requests to `http://localhost:3000`; use `VITE_REACT_APP_SERVER_URL` for another backend address. `just dev-web` defaults to port 3000, so use the explicit command above when both services run on the same host.

## Checks

```bash
just build
just test
```

`just build` builds Web and Go. `just test` runs their tests. Before a production-facing change, also run `just check` for formatting, lint, type checks, tests, and deployment contracts. See [CONTRIBUTING.md](../CONTRIBUTING.md) for the review process. Record skipped checks and their reasons.

For documentation and logo changes:

```bash
python3 scripts/check-docs-brand.py
```

That check validates local links in the entry documents, language parity for badges, SVG safety, and shared logo geometry. It does not make network requests or replace application tests.

## Optional recipes and limits

| Recipe | Important limit |
| --- | --- |
| `just dev`, `just infra-up`, `just infra-down` | Require a local `docker-compose.dev.yml`. This file is not included. |
| `just dev-core`, `just dev-extensions` | Start the WIP services separately; the extension host requires a local service credential file. |
| `just build-all`, `just test-all` | Include the new Rust core and Go module host. They do not establish production readiness. |
| `just docker-core`, `just docker-extensions` | Build the independent WIP images; see the [Docker guide](../deployment/docker/README.md). |
| `just package` | Requires `LMM_API_BUILD_WORKSPACE`; see the [AUR guide](../packaging/aur/README.md). |
| `just clean-generated` | Removes generated build output. |

Use `just --list` to inspect the current recipes. The new core refuses business traffic until the [migration gates](core-migration.md) pass. The independent [LMM CLI](../apps/lmm/README.md) is also a preview.

## Repository map

| Path | Purpose |
| --- | --- |
| [`apps/web`](../apps/web) | React and TypeScript console and public pages, built with Rsbuild. |
| [`apps/api-go`](../apps/api-go) | Default backend and provider CLI. |
| [`apps/core-rust`](../apps/core-rust) | Fresh stable-core foundation, not business-ready. |
| [`apps/extensions-go`](../apps/extensions-go) | Independent Go module host; feature extraction is pending. |
| [`apps/lmm`](../apps/lmm/README.md) | Preview setup CLI: discovery, planning, and read-only OAuth login. |
| [`packages`](../packages) | Client integrations. |
| [`scripts`](../scripts) | Development, checks, and deployment entry points. |
| [`packaging`](../packaging) | Provider packages and immutable runtime assets. |
| [`docs`](README.md) | Product, API, and operator documentation. |

Go and Web releases are independent. Publishing a release does not deploy it. Use the [release architecture](release-architecture.md) and the guide for your actual installation before updating a server.
