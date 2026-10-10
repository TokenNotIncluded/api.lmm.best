# Local development

[Documentation index](README.md) · [中文首页](../README.md) · [English overview](../README_EN.md) · [Agent guide](agent-workflows.md)

Use this guide for a development instance. For an existing production server, use the [deployment workflow](deployment-workflow.md).

## Requirements

| Tool or service | Requirement |
| --- | --- |
| Git and Just | Clone the repository and run the recipes in [justfile](../justfile). |
| Bun | 1.3.14, pinned in [package.json](../package.json). |
| Node.js | 22.12 or newer. |
| Go | 1.25.1 or newer for the default backend. |
| PostgreSQL and Valkey | Dedicated development services, separate from production. |
| Python | 3.10 or newer for the documentation checks in this guide. |
| Rust | Optional: 1.91.0 for the preview backend. |

## Set up

```bash
git clone https://github.com/TokenNotIncluded/api.lmm.best.git
cd api.lmm.best
just setup
cp .env.example apps/api-go/.env
```

The Go development process starts from `apps/api-go`. It reads `.env` there, not the copy at the repository root. Do not overwrite an existing development configuration.

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

Open <http://localhost:5173> and complete setup. The API uses port 3000 by default; check its actual listen address before exposing a development host. The frontend proxies API requests to `http://localhost:3000`; use `VITE_REACT_APP_SERVER_URL` for another backend address. `just dev-web` defaults to port 3000, so use the explicit command above when both services run on the same host.

## Checks

For application changes:

```bash
just build
just test
```

`just build` builds Web and Go. `just test` runs their tests. Before a production-facing code change, also run `just check` for formatting, lint, type checks, tests, and deployment contracts. See [CONTRIBUTING.md](../CONTRIBUTING.md). Record skipped checks and their reasons. Tests run locally; read [Actions](ci-workflow-layout.md) before assuming a PR or push triggers CI.

For documentation and skills:

```bash
python3 -B scripts/check-docs-brand.py --docs-only
python3 -B -m unittest discover -s scripts -p test_check_docs_brand.py
```

These checks cover maintained entry-document links, project skill metadata and paths, and README badge parity. They do not check all Markdown files, remote URLs, heading anchors, or application behavior. See the [Agent working guide](agent-workflows.md) and [skill index](../.agents/skills/README.md).

For translation helper changes:

```bash
node --test .agents/skills/i18n-translate/scripts/apply-translations.test.mjs
```

For logo changes, run `python3 -B scripts/check-docs-brand.py` without `--docs-only`. The full check also validates SVG safety, shared geometry, and raster hashes. It does not make network requests. Do not run whole-tree formatting or production commands merely to validate documentation.

## Optional recipes and limits

| Recipe | Important limit |
| --- | --- |
| `just dev`, `just infra-up`, `just infra-down` | Require a local `docker-compose.dev.yml`. This file is not included. |
| `just dev-rust` | Also requires that local Compose file and its `rust-preview` profile. |
| `just build-all`, `just test-all` | Include the Rust preview backend. They do not establish production readiness. |
| `just docker`, `just docker-rust` | Require local Dockerfiles, which are not included. |
| `just package` | Requires `LMM_API_BUILD_WORKSPACE`; see the [AUR guide](../packaging/aur/README.md). |
| `just clean-generated` | Removes generated build output. |

Use `just --list` to inspect current recipes. Do not describe missing Compose files or Dockerfiles as ready-to-run deployment methods. For Rust rollout limits, see [Rust blue-green](rust-blue-green.md). The independent [LMM CLI](../apps/lmm/README.md) is also a preview. Refactor branches can have different entry points; read the selected revision instead of mixing guides from different branches.

## Repository map

| Path | Purpose |
| --- | --- |
| [apps/web](../apps/web) | React and TypeScript console and public pages, built with Rsbuild. |
| [apps/api-go](../apps/api-go) | Default backend and provider CLI. |
| [apps/api-rust](../apps/api-rust) | Preview backend. |
| [apps/lmm](../apps/lmm/README.md) | Preview setup CLI: discovery, planning, and read-only OAuth login. |
| [packages](../packages) | Client integrations. |
| [scripts](../scripts) | Development, checks, and deployment entry points. |
| [packaging](../packaging) | Provider packages and immutable runtime assets. |
| [.agents/skills](../.agents/skills/README.md) | Task-specific Agent instructions and their references or helpers. |
| [docs](README.md) | Product, API, and operator documentation. |

Go and Web releases are independent. Publishing a release does not deploy it. Use the [release architecture](release-architecture.md) and the guide for the actual installation before updating a server.
