# LMM Forge

[![CI](https://github.com/TokenNotIncluded/api.lmm.best/actions/workflows/ci.yml/badge.svg)](https://github.com/TokenNotIncluded/api.lmm.best/actions/workflows/ci.yml)
[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](./LICENSE)
[![Release](https://img.shields.io/github/v/release/TokenNotIncluded/api.lmm.best?display_name=tag)](https://github.com/TokenNotIncluded/api.lmm.best/releases)
[![Issues](https://img.shields.io/github/issues/TokenNotIncluded/api.lmm.best)](https://github.com/TokenNotIncluded/api.lmm.best/issues)
[![Last Commit](https://img.shields.io/github/last-commit/TokenNotIncluded/api.lmm.best)](https://github.com/TokenNotIncluded/api.lmm.best/commits/main)

> **Access policy:** Access from China is prohibited.

LMM Forge is a production-grade, web-first bounty collaboration system for open-source maintenance, with delivery tracking, review/audit trails, and settlement workflows.

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Quick start](#quick-start)
- [Deployment and upgrades](#deployment-and-upgrades)
- [What LMM Forge supports](#what-lmm-forge-supports)
- [Repository layout](#repository-layout)
- [Documentation and operations](#documentation-and-operations)
- [Workflow and command reference](#workflow-and-command-reference)
- [Contribution and support](#contribution-and-support)
- [Security and legal](#security-and-legal)
- [License and attribution](#license-and-attribution)

## Overview

LMM Forge is a maintained derivative of `QuantumNous/new-api` with retained upstream compatibility and additional product features for bounty management.

Key differentiators:

- Public bounty board with authenticated workflow controls
- Evidence-driven acceptance for Issue/PR submissions
- Escrowed reward handling with transparent settlement state
- Dispute-aware delivery lifecycle and rating trail

`FORK.md` captures the formal fork and attribution constraints that govern derivative distribution and branding.

## Architecture

| Concern | Status |
| --- | --- |
| Frontend | Shared React application in `apps/web` |
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

- `git`, `bun`, `just`
- Database + cache services suitable for local development
- Optional: Go toolchain for native backend work, Rust toolchain for Rust preview runs

### Bootstrap

```bash

git clone https://github.com/TokenNotIncluded/api.lmm.best.git
cd api.lmm.best
just setup
```

### Run local services

```bash
just infra-up   # Starts default PostgreSQL + Valkey when docker-compose.dev.yml exists
just dev        # Starts web + Go backend together
```

Alternative flows:

```bash
just dev-web
just dev-go
just dev-rust
```

Open <http://localhost:3000> and complete the setup flow.

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

`bash scripts/lmm-api-deploy.sh --help` works without a compiled backend.
The standalone workflow updates an existing server, not a clean installation.
Backend and frontend versions remain independent. Neither path is automatically
run against production when code is merged or a release is published.

## What LMM Forge supports

- Challenge publication and acceptance
- Contributor submission and evidence intake
- Multi-step review with payout and dispute logic
- User and administrator workflow surfaces for accountability and governance
- Session and security controls documented under authentication contracts
- Operational guardrails for upgrades, cutovers, and cache/auth topology constraints

See [`docs/open-source-bounties.md`](./docs/open-source-bounties.md) for full bounty behavior and settlement rules.

## Repository layout

| Path | Purpose |
| --- | --- |
| [`apps/web`](./apps/web) | Shared React frontend |
| [`apps/api-go`](./apps/api-go) | Go API backend and production default |
| [`apps/api-rust`](./apps/api-rust) | Rust preview backend |
| [`deploy`](./deploy) | Migration/cutover/deploy documentation and scripts |
| [`packaging`](./packaging) | Packaging workflows and local package content |
| [`docs`](./docs) | Operational guides, legal policy, and API references |

## Documentation and operations

- `docs/README.md`: canonical docs index
- `docs/authentication.md`: authentication and session model
- `docs/seamless-upgrades.md`: upgrade workflow and constraints
- `docs/postgresql-migration.md`: migration rehearsal contract
- `docs/postgresql-cutover.md`: production cutover contract
- `docs/valkey-lmm-api.md`: dedicated cache architecture
- `docs/rust-blue-green.md`: ownership and route migration checkpoints
- `docs/openapi/api.json`: admin API spec
- `docs/openapi/relay.json`: relay API spec
- `docs/legal`: legal policy corpus
- `THIRD-PARTY-LICENSES.md`: dependency and license inventory

## Workflow and command reference

### Primary command groups

```text
just setup           Install workspace dependencies
just dev             Frontend + Go backend in one process group
just build           Build frontend and Go backend artifacts
just test            Run backend and frontend tests
just check           Formatting, lint, typecheck, and contract checks
just deploy-production  Promote an already-staged signed package release plan
```

### Additional commands

- `just infra-up` / `just infra-down` for local infrastructure
- `just dev-go`, `just dev-web`, `just dev-rust` for focused runs
- `just clean-generated` to clear generated build artifacts
- `just docker` or `just package` for environment-specific release outputs

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
