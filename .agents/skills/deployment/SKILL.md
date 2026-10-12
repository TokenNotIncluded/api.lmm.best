---
name: deployment
description: "Build or verify WIP component packages, prepare isolated installation, or review recovery. Select the component and artifact first. Not a gate for ordinary source edits or the old site's deployment flow."
---

# WIP packages and installation

Select the requested outcome before reading procedures. Commands and artifact
rules live in [distributions](../../../docs/release-architecture.md).

| Outcome | Read and do |
| --- | --- |
| Docs/tooling edit | Relevant local checks only; no host, database or release preflight |
| Verify an existing archive | Use `build-distribution.py verify` and its trusted source record; no rebuild |
| Build a component archive | Build only that component, then package its actual output |
| Convert to DEB/RPM/APK/Arch | Use a verified archive and [native packaging](../../../packaging/linux/README.md); do not recompile per format |
| Isolated core or extension installation | Select only the matching [Docker](../../../deployment/docker/README.md) or native path |
| Existing-site change or recovery | Establish that installation's reviewed procedure and authorization; do not use WIP fresh-install examples |

## Component and input rules

There are three programs: `lmm-core` in `services/core`, `lmm-extensions` in
`services/extensions`, and client `lmm` in the `apps/cli` submodule. Web is static
content. Core must run without Go. Do not add a deployment daemon or restore
retired operator, Web release or single-server scripts from main/history.

Freeze the source, component, platform and format once. Reuse matching test
results, binaries and trusted archives. Preserve their original source identity;
do not stamp old binaries with a new HEAD. Hashes alone do not prove who built
an artifact. Relevant source, toolchain, packaging or configuration changes may
invalidate reuse. Build core alone for core work, not core plus Go plus Web.

`--target binaries` exports programs, not runtime images. APK needs a verified
static ELF; a glibc archive cannot be reused there by renaming it. Service nodes,
especially 1C1G nodes, receive qualified artifacts rather than compiling them.
A packaging-only format conversion needs packaging checks, not every app build.

## Installation and verification

Use the selected path's current TOML, private credential files, service ownership
and readiness checks. Keep owner and restricted runtime credentials separate.
`init-db` is explicit, once on a verified empty isolated database; ordinary
startup checks schema and permissions. Never rerun initialization, clear a volume
or restore old environment-variable configuration as an update shortcut.

Core and Go update independently. Keep all required Compose overrides and RPC
bindings when updating an extension; do not restart core or the database for it.
Native package installation does not activate services. Check the running process
after an authorized restart; package contents alone do not show which code is active.
No current preview workflow proves a production rollout or uninterrupted streams.

## Stop at the requested result

Tests run locally unless the task specifically requests another environment.
Preview workflows retain artifacts, not a production Release. Do not dispatch
one as a side effect of editing this skill. Select checks with the
[working guide](../../../docs/agent-workflows.md), without repeating aggregates.

Read immutable facts once and mutable state at action boundaries. After an
uncertain write, inspect its exact ID/state before retrying. Preserve signatures,
source checks, host identity, locks, backups and required confirmations. Reuse
existing authorization for an unchanged action; changing target or scope needs
its own authorization.

Report separately: edited, built, verified, installed, published or deployed.
Record actual commands, source/artifact identity and missing checks. Readiness
or HTTP 200 is not proof of model traffic, billing, payment or full recovery.
Do not claim deployment speed or memory savings without comparable measurements.
