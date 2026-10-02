<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/assets/readme-cover-dark.svg">
    <img src=".github/assets/readme-cover-light.svg" alt="LMM Forge — model access, MCP tools, and open-source collaboration" width="1200">
  </picture>
</p>

<h3 align="center">AI APIs. MCP tools. Open-source work.</h3>

<p align="center">
  Model access, tool publishing, and bounty collaboration in one open-source console.<br>
  Built for the people using AI, the creators extending it, and the contributors maintaining it.
</p>

<p align="center">
  <a href="https://lmm.best"><strong>Explore LMM</strong></a> ·
  <a href="https://lmm.best/guide">Connect a client</a> ·
  <a href="https://lmm.best/tool-market">Publish a tool</a> ·
  <a href="docs/README.md">Read the docs</a>
</p>

<p align="center">
  <a href="https://github.com/TokenNotIncluded/api.lmm.best/actions/workflows/ci.yml"><img src="https://github.com/TokenNotIncluded/api.lmm.best/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/TokenNotIncluded/api.lmm.best/releases"><img src="https://img.shields.io/github/v/release/TokenNotIncluded/api.lmm.best?display_name=tag" alt="Latest component release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-AGPL--3.0-blue.svg" alt="License: AGPL-3.0"></a>
</p>

## Start here

| Your goal | Your next step |
| --- | --- |
| Use models with your preferred client | [Client setup](https://lmm.best/guide) · [Model pricing](https://lmm.best/pricing) |
| Publish tools and earn platform balance | [Tool marketplace](https://lmm.best/tool-market) · [Publishing workflow](#tool-marketplace) |
| Contribute to open-source projects | [Bounty workflow](docs/open-source-bounties.md) |
| Develop or operate your own instance | [Run locally](#run-locally) · [Deploy and upgrade](#deploy-and-upgrade) |

## What you can build

**Model access.** Route requests through OpenAI Chat Completions and Responses, Anthropic Messages, or Gemini interfaces. Account quotas, provider routing, and usage records share one console. Available models depend on the configured providers and account permissions.

OAuth-based client setup supports Pi, DSH, OpenCode, and Codewhale; VS Code and Zed integrations are in preview. Each client receives the permissions explicitly approved by the user.

**A tool business.** Connect an existing Remote MCP service, price individual tools, and earn platform balance from successful paid calls. Users choose the versions they load and control client permissions and spending limits.

**Open-source collaboration.** Publish challenges, lock rewards, submit Issue/PR evidence, and follow review, acceptance, and dispute workflows. See the [bounty guide](docs/open-source-bounties.md) for settlement rules.

Administrators manage users, roles, groups, balances, top-ups, and usage through desktop and mobile console views.

## Tool marketplace

### Publish a service

1. **Connect.** Register a public HTTPS Remote MCP endpoint, inspect its tool definitions, and set a free or paid price for each tool. Bearer and API Key authentication are supported for remote services.
2. **Validate and review.** Submit a specific validated version for administrator review. Validated, free drafts visible only to their author can be activated directly; public, shared, or paid services require review.
3. **Load and authorize.** Users load a tool version and separately approve its use, including client, call-count, expiry, and spending limits. Loading a tool does not authorize payment.

### Earn platform balance

Successful paid calls split credits between the creator and the configured platform recipient. Creators can spend their balance on models and other tools. **Withdrawals are not supported.**

The super administrator configures the platform fee and recipient account. The software does not impose a fixed fee percentage.

Built-in drawing, wallet, and bounty tools have no tool invocation fee. Drawing still incurs model usage costs, and wallet transfers move the specified balance. Publishing connects an existing Remote MCP service; serverless code uploads are not currently supported.

[Marketplace implementation](docs/tool-market-implementation.md) · [Client connections and permissions](docs/tool-market-connections.md)

## Run locally

### Requirements

- Git, [Just](https://github.com/casey/just), Bun **1.3.14**, and Node.js **22+**
- Go **1.25.1 or newer** for the default backend
- PostgreSQL and Valkey services for a dedicated local development environment
- Optional: Rust **1.91.0** for the preview backend

### Set up the workspace

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

### Start both services

Run the backend from the repository root:

```bash
just dev-go
```

In a second terminal, start the frontend on a separate port:

```bash
bun run --filter @lmm/web dev --port 5173 --host 127.0.0.1 --strict-port
```

Open <http://localhost:5173> and complete the setup flow. The frontend proxies API requests to `http://localhost:3000`; set `VITE_REACT_APP_SERVER_URL` when using a different backend address.

`just dev`, `just infra-up`, and `just dev-rust` require a local `docker-compose.dev.yml`, which is not included in this repository. The two-terminal flow above uses your existing development database and cache services.

Check the default production components locally:

```bash
just build
just test
```

## Deploy and upgrade

**Go and Web ship independently.** Their release tags are `go-vX.Y.Z` and `web-vX.Y.Z`. Rust remains a preview backend. Merging code or publishing a release does not deploy it to production.

Choose the workflow for your existing installation. Local development commands are not production installers; package-owned files should be updated through their signed transaction workflow.

| Installation | Workflow |
| --- | --- |
| Standalone Go on systemd | Start with `sudo bash scripts/lmm-api-deploy.sh systemd doctor`, then follow `upgrade` and explicit `confirm` in the [standalone guide](docs/manual-systemd-deployment.md). This updates an existing server. |
| Package-owned Go/Web | Use the installed `/usr/bin/lmm-api-deploy production` entry point and its [signed release plan](docs/seamless-upgrades.md). |
| Frontend-only update | Check compatibility with the active Go backend, then manually dispatch [`deploy-web-frontend.yml`](.github/workflows/deploy-web-frontend.yml) with a signed `web-vX.Y.Z` release. |

Frontend-only deployment switches the static release and preserves the previous frontend for explicit rollback. Provider packages install real `lmm-api-go` or `lmm-api-rs` binaries; production and operator commands enter through the one-hop `lmm-api` provider symlink.

`bash scripts/lmm-api-deploy.sh --help` works without a compiled backend.

[Component release architecture](docs/release-architecture.md) · [PostgreSQL migration](docs/postgresql-migration.md) · [Production cutover](docs/postgresql-cutover.md) · [Valkey operations](docs/valkey-lmm-api.md)

## Explore the codebase

The production default is a Go API with a React and TypeScript frontend built with Rsbuild. Rust backend and CLI work remain explicit previews.

| Path | Responsibility |
| --- | --- |
| [`apps/web`](apps/web) | Shared web console and public pages |
| [`apps/api-go`](apps/api-go) | Default API backend and provider CLI |
| [`apps/api-rust`](apps/api-rust) | Preview backend |
| [`apps/lmm`](apps/lmm/README.md) | Preview setup CLI: discovery, planning, and read-only OAuth login |
| [`packages`](packages) | Client integrations |
| [`scripts`](scripts) | Development, verification, and deployment tooling |
| [`packaging`](packaging) | Provider packages and immutable runtime assets |
| [`docs`](docs/README.md) | Product, API, and operations references |

<details>
<summary><strong>Development and operator commands</strong></summary>

```text
just setup              Install dependencies from the committed lockfile
just dev-go             Start the Go development backend
just dev-web            Start the frontend (default port 3000)
just build              Build the frontend and Go backend
just test               Run backend and frontend tests
just check              Format, lint, typecheck, tests, and deployment contracts
just deploy-production  Promote an already-staged signed package release plan
```

Use the explicit frontend command in [Run locally](#run-locally) to keep the frontend and API ports separate.

- `just --list` shows all available recipes.
- `just dev` and `just infra-up` / `just infra-down` use a locally supplied Compose file.
- `just build-all` / `just test-all` include the Rust preview backend.
- `just clean-generated` clears generated build artifacts.
- `just package` requires a configured `LMM_API_BUILD_WORKSPACE`; see the [AUR packaging guide](packaging/aur/README.md).
- Docker recipes require locally supplied Dockerfiles, which are not included in this repository.

</details>

<details>
<summary><strong>Product, API, and operations documentation</strong></summary>

| Topic | Reference |
| --- | --- |
| Documentation index | [All guides](docs/README.md) |
| Authentication and sessions | [Authentication](docs/authentication.md) |
| Tools and permissions | [Marketplace implementation](docs/tool-market-implementation.md) · [Connections](docs/tool-market-connections.md) |
| Open-source work | [Bounties and settlement](docs/open-source-bounties.md) |
| Editor integrations | [OpenCode](docs/opencode-provider.md) · [VS Code and Zed](docs/editor-providers.md) |
| Releases and upgrades | [Architecture](docs/release-architecture.md) · [Signed upgrades](docs/seamless-upgrades.md) |
| Backend preview | [Rust blue-green](docs/rust-blue-green.md) |
| API specifications | [Admin API](docs/openapi/api.json) · [Relay API](docs/openapi/relay.json) |
| Dependency notices | [Third-party licenses](THIRD-PARTY-LICENSES.md) |

</details>

## Contribute

LMM Forge is a maintained fork of [QuantumNous/new-api](https://github.com/QuantumNous/new-api). [FORK.md](FORK.md) records the upstream relationship and attribution requirements.

For development requirements and scoped changes, start with [CONTRIBUTING.md](CONTRIBUTING.md). Use the repository's [Issue templates](.github/ISSUE_TEMPLATE) for bug reports and feature requests.

[Support](SUPPORT.md) · [Code of Conduct](CODE_OF_CONDUCT.md) · [Security policy](SECURITY.md)

## Security and terms

The default edge policy blocks requests geolocated to Mainland China (`CN`). Administrators can configure explicit IP routing rules.

Report vulnerabilities through [SECURITY.md](SECURITY.md). User-facing policies are documented in the [user agreement](docs/legal/user-agreement.md), [privacy policy](docs/legal/privacy-policy.md), and [terms of service](docs/legal/terms-of-service.md).

## License

Released under [AGPL-3.0](LICENSE). Upstream attribution and required notices are preserved in [NOTICE](NOTICE) and [FORK.md](FORK.md).
