---
name: deployment
description: "Use for current lmm, lmm-core and lmm-extensions packaging, isolated installation, release review or recovery boundaries. Build and verify preview artifacts; production changes require separate authorization."
---

# Packages and installation boundaries

Read the current [architecture and status](../../../docs/core-migration.md),
[preview distributions](../../../docs/release-architecture.md),
[Linux packages](../../../packaging/linux/README.md) and
[Docker development stack](../../../deployment/docker/README.md).
Check the selected component's source, manifest and build command at the actual
revision before acting. Historical operation guides are references, not current
installation or recovery commands.

## Three programs

- `lmm` is the Rust client in `apps/lmm`.
- `lmm-core` is the Rust authority for identity, permissions, funds, billing and
  model routing in `apps/lmm-core`. Explicit database initialization and initial
  user creation are subcommands of this same program.
- `lmm-extensions` is the Go module host in `apps/lmm-extensions`. It does not
  own core balances, model traffic or the core database.

The Web build is static content, not another executable or server process.
Preview archive definitions live in `packaging/distribution.json`; current
native package definitions live in `packaging/linux`. Do not add a deployment
executable, backend wrapper or extra service to replace a removed operator tool.

## Establish scope

Inspect the branch, base commit, worktree changes, related PR and exact requested
component. Preserve unrelated work and other active worktrees. An implementation,
a tested commit, an archive, an installed package, a published release and an
active production deployment are different states.

Use a fresh isolated database and credentials for this WIP. Do not import,
upgrade or clear an existing site database. Core and extensions build and update
independently; extensions cannot receive the core database connection or use a
host credential as the user's identity or a spending grant.

Code or documentation changes authorize local work and checks. They do not
authorize publication, service restarts, production configuration, database
mutation, funds or deployment. Do not execute an example merely to validate its
syntax. Production work requires an explicit target, authorization, reviewed
artifacts and a recovery procedure for that installation.

## Build and verify locally

Use `just --list`, `package.json` and the component manifests as the command
source. Run relevant tests locally and record the source revision, working-tree
state, exact commands, exit codes and skipped checks. Never replace a failed
assertion, real database check or permission boundary with a successful fixture.

Build each component once with its pinned toolchain, then use the existing
`build` and `verify` commands in `scripts/build-distribution.py`. Check archive
revision, component, executable inventory, legal resources and checksums.
Native formats use `packaging/linux/build.py` and its existing isolated tests.
Package construction does not initialize databases, start services or prove a
working installation. Do not add install hooks that do those operations.

`microservice-distribution.yml`, `linux-packages.yml` and `lmm.yml` preserve
preview artifacts; they do not publish a release or deploy. Do not dispatch CI
as a substitute for the requested local checks. The retired single-server Web
release/deployment workflows, package recipes and operator commands are not
alternate entry points on this branch.

`scripts/local-release-tests.py` and `scripts/verify-release-commit-checks.sh`
can bind local command results to immutable component source. Importing a prior
log records that prior command; it does not rerun it or prove complete acceptance.
Independent signature and read-only public verification tools retain their
limited purpose. Neither a signature, a successful request nor HTTP 200 proves
that the intended application behavior is active.

## Recovery and delivery

Never disable signatures, source binding, host identity checks, transaction
ownership or explicit confirmation to make a rollout pass. After an ambiguous
mutation, inspect durable state before retrying. Preserve existing installed
versions, backup material, locks and recovery evidence; deleting old source
resources from this WIP does not authorize deleting them from an active host.

Report changed paths, actual tests, commit/PR and unverified work. State whether
anything was published, installed or deployed. This branch currently supplies
isolated preview packages and implementation checks; full permission, payment,
refund, model traffic and recovery acceptance remains separate evidence.
