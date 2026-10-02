# LMM Forge

[![CI](https://github.com/TokenNotIncluded/api.lmm.best/actions/workflows/ci.yml/badge.svg)](https://github.com/TokenNotIncluded/api.lmm.best/actions/workflows/ci.yml)
[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](./LICENSE)
[![Release](https://img.shields.io/github/v/release/TokenNotIncluded/api.lmm.best?display_name=tag)](https://github.com/TokenNotIncluded/api.lmm.best/releases)
[![Issues](https://img.shields.io/github/issues/TokenNotIncluded/api.lmm.best)](https://github.com/TokenNotIncluded/api.lmm.best/issues)
[![Last Commit](https://img.shields.io/github/last-commit/TokenNotIncluded/api.lmm.best)](https://github.com/TokenNotIncluded/api.lmm.best/commits/main)

LMM Forge combines a multi-provider AI API gateway, a versioned MCP tool marketplace, and open-source bounty collaboration in one web console.

[Website](https://lmm.best) · [Model pricing](https://lmm.best/pricing) · [Client setup](https://lmm.best/guide) · [Tool marketplace](https://lmm.best/tool-market) · [Documentation](docs/README.md)

> **Access policy:** The default edge policy blocks requests geolocated to Mainland China (`CN`). Administrators can configure explicit IP routing rules.

## Table of Contents

- [Overview](#overview)
- [Tool marketplace](#tool-marketplace)
- [Architecture](#architecture)
- [Quick start](#quick-start)
- [Deployment and upgrades](#deployment-and-upgrades)
- [Repository layout](#repository-layout)
- [Documentation and operations](#documentation-and-operations)
- [Workflow and command reference](#workflow-and-command-reference)
- [Contribution and support](#contribution-and-support)
- [Security and legal](#security-and-legal)
- [License and attribution](#license-and-attribution)

## Overview

- **AI API gateway:** OpenAI Chat Completions and Responses, Anthropic Messages, and Gemini protocol routes, with provider routing, account quotas, and usage records. Available models depend on the configured providers and account permissions.
- **Client integrations:** OAuth-based setup for Pi, DSH, OpenCode, and Codewhale, plus preview integrations for VS Code and Zed. Each client receives the permissions explicitly approved by the user.
- **Tool marketplace:** Publish Remote MCP services, price individual tools, and earn platform balance from successful paid calls. Users control tool versions, client permissions, and spending limits.
- **Open-source bounties:** Publish challenges, lock rewards, submit Issue/PR evidence, and follow review, acceptance, and dispute workflows.
- **Account administration:** Manage users, roles, groups, balances, top-ups, and usage through desktop and mobile console views.

The project is a maintained fork of [QuantumNous/new-api](https://github.com/QuantumNous/new-api). [FORK.md](FORK.md) records the upstream relationship and attribution requirements.

## Tool marketplace

1. Register a public HTTPS Remote MCP endpoint, inspect its tool definitions, and set a free or paid price for each tool. Bearer and API Key authentication are supported for remote services.
2. Validate a specific version and submit it for administrator review. Validated, free drafts visible only to their author can be activated directly; public, shared, or paid services require review.
3. Users load a tool version and separately authorize its use, including client, call-count, expiry, and spending limits. Loading a tool does not authorize payment.

Successful paid calls credit the creator's earnings and the platform fee to their respective platform balances. Creators can spend their balance on models and other tools. **Withdrawals are not supported.** The platform fee and recipient account are configured by the super administrator; the software does not impose a fixed fee percentage.

Built-in drawing, wallet, and bounty tools have no tool invocation fee. Drawing still incurs model usage costs, and wallet transfers move the specified balance. Publishing connects an existing Remote MCP service; serverless code uploads are not currently supported.

See the [marketplace implementation](docs/tool-market-implementation.md) and [client connection guide](docs/tool-market-connections.md) for supported protocols, permissions, billing, and execution limits.

## Architecture

| Concern | Status |
| --- | --- |
| Frontend | React and TypeScript application in `apps/web`, built with Rsbuild |
| Default backend | Go provider CLI/service in `apps/api-go` |
| Preview backend | Rust provider CLI/service in `apps/api-rust` (not default production traffic) |
| LMM CLI | Rust setup tool in [`apps/lmm`](apps/lmm/README.md) (preview: discovery, planning and read-only OAuth login) |
| Deployment | Signed package transactions or existing standalone systemd upgrades; see [deployment and upgrades](#deployment-and-upgrades) |
| Packaging | Provider binaries and immutable runtime assets in `packaging/` |

For existing standalone installations on systemd Linux, see
[standalone systemd deployment](docs/manual-systemd-deployment.md).

Providers install real `lmm-api-go` or `lmm-api-rs` binaries. Production and operator actions always enter through the one-hop `lmm-api` provider symlink. The frontend is released independently.

## Quick start

### Prerequisites

- Git, [Just](https://github.com/casey/just), Bun **1.3.14**, and Node.js **22+**
- Go **1.25.1 or newer** for the default backend
- PostgreSQL and Valkey services for a dedicated local development environment
- Optional: Rust **1.91.0** for the preview backend

### Bootstrap

```bash
git clone https://github.com/TokenNotIncluded/api.lmm.best.git
cd api.lmm.best
just setup
cp .env.example apps/api-go/.env
```

Edit `apps/api-go/.env` before starting the backend:

- Set `SQL_DSN` to your development PostgreSQL database and `REDIS_CONN_STRING` to your Valkey instance.
- Set independent, random `SESSION_SECRET` and `CRYPTO_SECRET` values. Keep these stable across restarts.
- The template binds the API to `127.0.0.1:3000`. Use a dedicated development database: startup applies schema migrations by default.

The Go development command runs from `apps/api-go` and loads `.env` from that directory.

### Run the backend and frontend

From the repository root, start the backend in one terminal:

```bash
just dev-go
```

Start the frontend in a second terminal, using a separate port:

```bash
bun run --filter @lmm/web dev --port 5173 --host 127.0.0.1 --strict-port
```

Open <http://localhost:5173> and complete the setup flow. The frontend proxies API requests to `http://localhost:3000`; set `VITE_REACT_APP_SERVER_URL` when using a different backend address.

`just dev`, `just infra-up`, and `just dev-rust` require a local `docker-compose.dev.yml`, which is not included in this repository. The two-terminal flow above uses your existing development database and cache services.

### Production-style local checks

```bash
just build
just test
```

## Deployment and upgrades

Local development commands are not production installers. Choose the path that
matches the existing installation; do not replace package-owned files manually.

| Installation | Entry point | Guide |
| --- | --- | --- |
| Standalone Go on systemd | `sudo bash scripts/lmm-api-deploy.sh systemd doctor`, then `upgrade` and explicit `confirm` | [Standalone workflow](docs/manual-systemd-deployment.md) |
| Package-owned Go/Web | Installed `/usr/bin/lmm-api-deploy production` signed-plan workflow | [Package transactions](docs/seamless-upgrades.md) |
| Frontend-only update | Manually dispatch [`deploy-web-frontend.yml`](.github/workflows/deploy-web-frontend.yml) with a signed `web-vX.Y.Z` release after checking compatibility with the active Go backend | [Component release architecture](docs/release-architecture.md) |

`bash scripts/lmm-api-deploy.sh --help` works without a compiled backend.
The standalone workflow updates an existing server, not a clean installation.
Go and Web have independent `go-vX.Y.Z` and `web-vX.Y.Z` releases. Rust remains a
preview backend. Merging code or publishing a release does not deploy it to
production. Frontend-only deployment switches the static release and preserves
the previous frontend for explicit rollback.

## Repository layout

| Path | Purpose |
| --- | --- |
| [`apps/web`](./apps/web) | Shared React frontend |
| [`apps/api-go`](./apps/api-go) | Go API backend and production default |
| [`apps/api-rust`](./apps/api-rust) | Rust preview backend |
| [`apps/lmm`](./apps/lmm) | Preview setup and discovery CLI |
| [`packages`](./packages) | Client integrations |
| [`scripts`](./scripts) | Development, verification, and deployment tooling |
| [`packaging`](./packaging) | Packaging workflows and local package content |
| [`docs`](./docs) | Operational guides, legal policy, and API references |

## Documentation and operations

| Topic | Guide |
| --- | --- |
| All documentation | [Documentation index](docs/README.md) |
| Authentication and sessions | [Authentication](docs/authentication.md) |
| Tool publishing and billing | [Marketplace implementation](docs/tool-market-implementation.md) |
| MCP client setup | [Marketplace connections](docs/tool-market-connections.md) |
| Bounties and reward settlement | [Open-source bounties](docs/open-source-bounties.md) |
| OpenCode and editor integrations | [OpenCode provider](docs/opencode-provider.md), [VS Code and Zed](docs/editor-providers.md) |
| Component releases and upgrades | [Release architecture](docs/release-architecture.md), [Signed upgrades](docs/seamless-upgrades.md) |
| Database migration and cutover | [PostgreSQL migration](docs/postgresql-migration.md), [Cutover](docs/postgresql-cutover.md) |
| Cache and backend operations | [Valkey](docs/valkey-lmm-api.md), [Rust blue-green preview](docs/rust-blue-green.md) |
| API specifications | [Admin API](docs/openapi/api.json), [Relay API](docs/openapi/relay.json) |
| Dependency licenses | [Third-party licenses](THIRD-PARTY-LICENSES.md) |

## Workflow and command reference

### Primary command groups

```text
just setup           Install workspace dependencies
just dev-go          Start the Go development backend
just dev-web         Start the frontend (defaults to port 3000; choose a separate port above)
just build           Build frontend and Go backend artifacts
just test            Run backend and frontend tests
just check           Formatting, lint, typecheck, tests, and deployment contract checks
just deploy-production  Promote an already-staged signed package release plan
```

### Additional commands

- `just --list` shows all available recipes.
- `just dev` and `just infra-up` / `just infra-down` use a locally supplied Compose file.
- `just build-all` / `just test-all` include the Rust preview backend.
- `just clean-generated` clears generated build artifacts.
- `just package` requires a configured `LMM_API_BUILD_WORKSPACE`; see the [AUR packaging guide](packaging/aur/README.md).
- Docker recipes require locally supplied Dockerfiles, which are not included in this repository.

## Contribution and support

- `Contributing` requirements: [CONTRIBUTING.md](./CONTRIBUTING.md)
- `Support model`: [SUPPORT.md](./SUPPORT.md)
- `Code of Conduct`: [CODE_OF_CONDUCT.md](./CODE_OF_CONDUCT.md)
- `Issue workflows`: use GitHub Issue templates in `.github/ISSUE_TEMPLATE`

## Security and legal

Security policy is maintained at: [SECURITY.md](./SECURITY.md)

Legal documents:

- [docs/legal/user-agreement.md](./docs/legal/user-agreement.md)
- [docs/legal/privacy-policy.md](./docs/legal/privacy-policy.md)
- [docs/legal/terms-of-service.md](./docs/legal/terms-of-service.md)

## License and attribution

This repository is distributed under AGPL-3.0. See [LICENSE](./LICENSE).

Fork attribution and required notices are preserved in [NOTICE](./NOTICE).
