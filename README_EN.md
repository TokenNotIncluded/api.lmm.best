<div align="center">
  <img src=".github/assets/lmm-logo.svg" alt="LMM Forge" width="96" height="96" />
  <h1>LMM Forge</h1>
  <p>
    <a href="https://github.com/TokenNotIncluded/api.lmm.best/actions/workflows/core-protocol.yml"><img src="https://github.com/TokenNotIncluded/api.lmm.best/actions/workflows/core-protocol.yml/badge.svg?branch=wip%2Frust-core-go-extensions" alt="Microkernel checks" /></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0-blue" alt="License: AGPL-3.0" /></a>
  </p>
  <p><a href="README.md">简体中文</a> · <a href="README_EN.md">English</a></p>
</div>

The Rust core owns identity, permissions, billing and model routing. Go extensions hold separately updated business modules. The React console and CLI have separate builds.

**This fresh-install microservice branch is WIP, not a deployable replacement for the existing site.** Passing module tests does not prove a complete model, payment or funds flow. There is no old-database import, automatic schema upgrade or legacy-server fallback.

Initialize a new core database explicitly with `lmm-core-admin init-db`; normal startup does not create or alter tables.

## Start here

| Task | Entry point |
| --- | --- |
| Understand ownership and remaining work | [Architecture and status](docs/core-migration.md) |
| Start an isolated environment | [Docker development stack](deployment/docker/README.md) |
| Develop and test | [Development](docs/development.md) |
| Configure user, privacy and refund policies | [Policy templates](docs/legal/README.md) |
| Build and inspect separate archives | [Packaging and distribution](docs/release-architecture.md) |
| Find detailed contracts and historical material | [Documentation](docs/README.md) |

## Layout and runtime boundaries

`apps/lmm-core` is the Rust core; `apps/lmm-extensions` is the Go host; `apps/web` is the frontend; `apps/lmm` is the CLI. `contracts` holds cross-process contracts, `config/legal` holds editable policy drafts, and `packaging/distribution.json` defines archive contents.

Core and extensions use separate Compose projects. Updating extensions must not restart the core or its database. A responding health route does not make model routes ready. Public policy reads do not need the core. Other business flows still require explicit integration and acceptance tests.

## Contribution and license

Read [CONTRIBUTING.md](CONTRIBUTING.md). Follow [SECURITY.md](SECURITY.md) for vulnerabilities; do not disclose keys or user data. Merging, packaging, publishing and deploying are separate decisions. Checks produce test evidence or preview archives only.

The project builds on [QuantumNous/new-api](https://github.com/QuantumNous/new-api) under [AGPL-3.0](LICENSE). Preserve [NOTICE](NOTICE), [FORK.md](FORK.md) and [third-party notices](THIRD-PARTY-LICENSES.md). The [hosted service](https://api.lmm.best) and this branch have different implementation states.
