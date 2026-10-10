<div align="center">
  <img src=".github/assets/lmm-logo.svg" alt="LMM Forge" width="96" height="96" />
  <h1>LMM Forge</h1>
  <p>Model access, MCP tools, and open-source work. One console.</p>
  <p>
    <a href="https://github.com/TokenNotIncluded/api.lmm.best/actions/workflows/ci.yml"><img src="https://github.com/TokenNotIncluded/api.lmm.best/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI" /></a>
    <a href="https://github.com/TokenNotIncluded/api.lmm.best/releases?q=web-v"><img src="https://img.shields.io/github/v/release/TokenNotIncluded/api.lmm.best?filter=web-v%2A&amp;label=Web&amp;display_name=tag" alt="Web release" /></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue" alt="License: AGPL-3.0" /></a>
  </p>
  <p><a href="README.md">简体中文</a> · <strong>English</strong></p>
  <p><a href="https://api.lmm.best">Live site</a> · <a href="https://api.lmm.best/guide">Client setup</a> · <a href="docs/README.md">Documentation</a> · <a href="https://github.com/TokenNotIncluded/api.lmm.best/issues">Report an issue</a></p>
</div>

## Overview

LMM Forge is an open-source console for AI services. Model access, a Remote MCP tool marketplace, and open-source bounties share one account and credit system. Connect a client, publish paid tools, or earn platform balance by completing open-source tasks.

This project is a maintained fork of [QuantumNous/new-api](https://github.com/QuantumNous/new-api). Go is the default backend. The web app uses React and TypeScript. The Rust core / Go extensions migration is WIP and cannot receive production traffic. The standalone CLI remains a preview.

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

**This branch is a breaking, fresh-install WIP.** It is not the current live site's backend. It has no legacy import, schema upgrade, balance conversion or automatic database migration path.

Use the [Docker development stack](deployment/docker/README.md). It explicitly installs an empty core database with `lmm-core-admin init-db`. A second installation or an existing application database is rejected. Normal service startup does not alter tables.

`apps/core-rust` is the Rust core. `apps/api-go` is the Go extension host. Go currently provides only the read-only identity module, not the old shop, assistant, payment or console APIs. Model endpoints remain unavailable. Frontend source is retained, but this branch is not a working replacement for the whole site.

For local commands and separately exported service configuration, use the [development guide](docs/development.md). Do not copy an old Go database environment into the new extension process.

## Deployment boundary

Core and extensions have independent Docker projects. Rebuilding Go must not restart the core or its database. This branch no longer ships the old Go server packages or systemd database-upgrade tools. No production deployment or data deletion is performed by these checks.

See [fresh installation](deployment/docker/README.md), [core identity](docs/core-identity.md), and [Protobuf communication](docs/core-protocol.md). Historical deployment documents describe the retired architecture and must not be used to install this branch.

## Documentation

The [documentation index](docs/README.md) groups guides by user, developer, and operator tasks. Common starting points:

- **Use and contribute:** [Tool publishing](docs/tool-market-guide.md), [connections and permissions](docs/tool-market-connections.md), [bounties and settlement](docs/open-source-bounties.md).
- **Development and APIs:** [Local development](docs/development.md), [contribution guide](CONTRIBUTING.md), [admin API](docs/openapi/api.json), [relay API](docs/openapi/relay.json).
- **Releases and maintenance:** [Release architecture](docs/release-architecture.md), [authentication and sessions](docs/authentication.md), [Core and extensions migration](docs/core-migration.md).

## Contribute and report security issues

Read [CONTRIBUTING.md](CONTRIBUTING.md), use the [Issue templates](.github/ISSUE_TEMPLATE), and record the checks you actually ran. See [SUPPORT.md](SUPPORT.md) for support and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for community rules.

Follow [SECURITY.md](SECURITY.md) to report a vulnerability. Do not publish keys or account data in issues, logs, or screenshots. The default edge policy blocks requests geolocated to Mainland China (`CN`). Administrators can set explicit IP routing rules.

[User agreement](docs/legal/user-agreement.md) · [Privacy policy](docs/legal/privacy-policy.md) · [Terms of service](docs/legal/terms-of-service.md) · [Logo assets and usage](.github/assets/README.md)

## License and acknowledgements

Released under [AGPL-3.0](LICENSE). Upstream attribution and required notices remain in [NOTICE](NOTICE), [FORK.md](FORK.md), and [third-party notices](THIRD-PARTY-LICENSES.md).

The README structure takes inspiration from [TokenRouter](https://github.com/TokenFlux/TokenRouter). Features, deployment instructions, licensing, and logo assets describe LMM Forge, not the reference project.
