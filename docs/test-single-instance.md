# Isolated Rust provider test host

This path is only for an explicitly approved non-production test host. It does
not authorize routing, package, provider-link, deployment, or configuration
changes on `api.lmm.best`.

The package installs a real `/usr/bin/lmm-api-rs`. Test-host service and
operator actions still enter through a one-hop `/usr/bin/lmm-api -> lmm-api-rs`
symlink. Do not install a regular generic binary, reverse alias, or shell
deployment wrapper.

Use provider-owned CLI commands for version, status, health, migration,
deployment status, confirmation, and manual rollback. CLI dispatch occurs
before server configuration/database initialization. Missing production-only
environment must not break read-only CLI commands.

Real PostgreSQL/Valkey URLs and secrets are created directly as mode-0600
configuration on the test host and never committed, packaged, logged, or passed
on command lines. The test uses fresh isolated PostgreSQL and dedicated Valkey,
not production data or snapshots.

The test surface may expose incomplete Rust routes. Expected parity failures are
not permission to transfer production traffic. Rust business ownership remains
controlled by `apps/api-rust/tests/fixtures/routes/migration-gate.tsv`.

Build a commit-bound provider package from a clean checkout:

```bash
cd apps/api-rust
LMM_BUILD_REVISION="$(git rev-parse HEAD)" cargo build --release --locked --bin lmm-api-rs
```

Before activation verify package ownership/hash/revision, provider-link safety,
PostgreSQL/Valkey identity, isolated ports, and manual rollback evidence. A
healthy activation stops at `AWAITING_CONFIRMATION`; explicitly confirm or roll
back through `/usr/bin/lmm-api`.
