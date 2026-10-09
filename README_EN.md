<div align="center">
  <img src=".github/assets/lmm-logo.svg" alt="LMM Forge" width="96" height="96" />
  <h1>LMM Forge</h1>
  <p>Model access, MCP tools, and open-source work. One console.</p>
  <p>
    <a href="https://github.com/TokenNotIncluded/api.lmm.best/actions/workflows/ci.yml"><img src="https://github.com/TokenNotIncluded/api.lmm.best/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI" /></a>
    <a href="https://github.com/TokenNotIncluded/api.lmm.best/releases?q=go-v"><img src="https://img.shields.io/github/v/release/TokenNotIncluded/api.lmm.best?filter=go-v%2A&amp;label=Go&amp;display_name=tag" alt="Go release" /></a>
    <a href="https://github.com/TokenNotIncluded/api.lmm.best/releases?q=web-v"><img src="https://img.shields.io/github/v/release/TokenNotIncluded/api.lmm.best?filter=web-v%2A&amp;label=Web&amp;display_name=tag" alt="Web release" /></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue" alt="License: AGPL-3.0" /></a>
  </p>
  <p><a href="README.md">简体中文</a> · <strong>English</strong></p>
  <p><a href="https://api.lmm.best">Live site</a> · <a href="https://api.lmm.best/guide">Client setup</a> · <a href="docs/README.md">Documentation</a> · <a href="https://github.com/TokenNotIncluded/api.lmm.best/issues">Report an issue</a></p>
</div>

## Overview

LMM Forge is an open-source console for AI services. Model access, a Remote MCP tool marketplace, and open-source bounties share one account and credit system. Connect a client, publish paid tools, or earn platform balance by completing open-source tasks.

This project is a maintained fork of [QuantumNous/new-api](https://github.com/QuantumNous/new-api). Go is the default backend. The web app uses React and TypeScript. The Rust backend and standalone CLI are previews.

## Core features

| Area | What it does |
| --- | --- |
| Model access | Connect OpenAI Chat Completions / Responses, Anthropic Messages, and Gemini interfaces. Manage providers, groups, quotas, and usage records. |
| Client setup | Connect Pi, DSH, OpenCode, and Codewhale through OAuth. VS Code and Zed integrations are in preview. |
| Tool marketplace | Connect an existing HTTPS Remote MCP service. Set prices per tool and publish validated, reviewed versions. |
| Access and spending | Control tool loading, call approval, client permissions, call counts, expiry, and spending limits separately. |
| Open-source work | Publish bounties, lock rewards, submit Issue / PR evidence, and handle review, acceptance, and disputes. |
| Administration | Manage users, roles, balances, top-ups, and usage. Use desktop or mobile layouts in light or dark mode. |

Available models and features depend on provider setup and account permissions. Tool earnings become platform balance. **Withdrawals are not supported.** Loading a tool does not approve payment. Built-in drawing has no tool invocation fee, but model usage still has a cost.

## Live site

Visit the [home page](https://api.lmm.best), [model pricing](https://api.lmm.best/pricing), or [tool marketplace](https://api.lmm.best/tool-market). You do not need to deploy this repository to use an existing service. Start with [client setup](https://api.lmm.best/guide).

## Quick start

These steps are for local development, **not production installation**. Install Git, Just, Bun 1.3.14, Node.js 22.12+, and Go 1.25.1+. Prepare separate PostgreSQL and Valkey services.

```bash
git clone https://github.com/TokenNotIncluded/api.lmm.best.git
cd api.lmm.best
just setup
cp .env.example apps/api-go/.env
```

Edit `apps/api-go/.env` first. Set `SQL_DSN` and `REDIS_CONN_STRING`. Set independent random values for `SESSION_SECRET` and `CRYPTO_SECRET`. Use a development database: startup applies database migrations by default.

Start the backend from the repository root:

```bash
just dev-go
```

Start the frontend in a second terminal. Use a separate port because the backend uses port 3000:

```bash
bun run --filter @lmm/web dev --port 5173 --host 127.0.0.1 --strict-port
```

Open <http://localhost:5173> and complete setup. See the [development guide](docs/development.md) for configuration, checks, and Rust preview notes. This repository does not include a development Compose file or Dockerfiles. `just dev` and Docker build recipes therefore need extra local setup.

## Deploy and upgrade

Go and Web have separate release tags: `go-vX.Y.Z` and `web-vX.Y.Z`. **A merge or release does not deploy to production.**

| Existing installation | Start here |
| --- | --- |
| Standalone systemd service | [Inspect, upgrade, confirm, and roll back](docs/manual-systemd-deployment.md) |
| Package-managed Go / Web | [Signed releases and upgrade transactions](docs/seamless-upgrades.md) |
| Frontend-only update | [Release boundaries](docs/release-architecture.md) · [Deployment workflow](.github/workflows/deploy-web-frontend.yml) |
| Database and cache | [PostgreSQL migration](docs/postgresql-migration.md) · [Production cutover](docs/postgresql-cutover.md) · [Valkey operations](docs/valkey-lmm-api.md) |

## Documentation

The [documentation index](docs/README.md) groups guides by user, developer, and operator tasks. Common starting points:

- **Use and contribute:** [Tool publishing](docs/tool-market-guide.md), [connections and permissions](docs/tool-market-connections.md), [bounties and settlement](docs/open-source-bounties.md).
- **Development and APIs:** [Local development](docs/development.md), [contribution guide](CONTRIBUTING.md), [admin API](docs/openapi/api.json), [relay API](docs/openapi/relay.json).
- **Releases and maintenance:** [Release architecture](docs/release-architecture.md), [authentication and sessions](docs/authentication.md), [Rust preview](docs/rust-blue-green.md).

## Contribute and report security issues

Read [CONTRIBUTING.md](CONTRIBUTING.md), use the [Issue templates](.github/ISSUE_TEMPLATE), and record the checks you actually ran. See [SUPPORT.md](SUPPORT.md) for support and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for community rules.

Follow [SECURITY.md](SECURITY.md) to report a vulnerability. Do not publish keys or account data in issues, logs, or screenshots. The default edge policy blocks requests geolocated to Mainland China (`CN`). Administrators can set explicit IP routing rules.

[User agreement](docs/legal/user-agreement.md) · [Privacy policy](docs/legal/privacy-policy.md) · [Terms of service](docs/legal/terms-of-service.md) · [Logo assets and usage](.github/assets/README.md)

## License and acknowledgements

Released under [AGPL-3.0](LICENSE). Upstream attribution and required notices remain in [NOTICE](NOTICE), [FORK.md](FORK.md), and [third-party notices](THIRD-PARTY-LICENSES.md).

The README structure takes inspiration from [TokenRouter](https://github.com/TokenFlux/TokenRouter). Features, deployment instructions, licensing, and logo assets describe LMM Forge, not the reference project.
