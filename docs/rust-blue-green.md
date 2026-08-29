# Rust backend provider rollout

The Rust backend is an independently built provider. Its real executable is
`/usr/bin/lmm-api-rs`; production and operator commands enter through the
one-hop `/usr/bin/lmm-api` symlink. The old shell-managed blue/green slot
framework has been retired.

The executable/link, manual rollback, shared command schemas, and package rules
are normative in [`backend-cli-deployment-contract.md`](backend-cli-deployment-contract.md).

## Current ownership

Go still owns production business traffic. Rust route ownership is determined
only by the current result of:

```text
apps/api-rust/tests/fixtures/routes/migration-gate.tsv
```

A compiled binary, mounted candidate route, successful `/livez` or `/readyz`
probe, provider symlink, historical rehearsal, or passing deployment CLI test
does not transfer business ownership to Rust.

The Rust provider MUST fail closed when a requested production switch would
claim routes that lack independent differential evidence and approval.

## Build and package

Build the real provider binary from an immutable revision:

```bash
cd apps/api-rust
LMM_BUILD_REVISION="$(git rev-parse HEAD)" cargo build --release --locked --bin lmm-api-rs
sha256sum target/release/lmm-api-rs
```

The Rust package installs that real binary and its signed identity metadata. It
must not install a regular `/usr/bin/lmm-api`, a reverse
`lmm-api-rs -> lmm-api` alias, or shell deployment wrappers. Go and Rust
provider packages may coexist.

Provider selection is performed only through the currently verified public CLI:

```bash
/usr/bin/lmm-api backend status
/usr/bin/lmm-api backend select rust
```

Selection verifies package ownership, release identity, target mode and hash,
then atomically replaces `/usr/bin/lmm-api` with a relative `lmm-api-rs`
symlink. Confirmation and rollback after a provider switch must also be invoked
through `/usr/bin/lmm-api`.

## Configuration and service lifecycle

CLI dispatch occurs before service configuration or database initialization.
Commands such as `version`, `backend status`, deployment status, confirmation,
and rollback must not fail merely because server-only environment variables are
absent.

Go and Rust use the same PostgreSQL authority and dedicated Valkey deployment.
Rust accepts the provider-neutral production keys and the documented Go-key
aliases without logging their values. Conflicting aliases are rejected rather
than guessed.

The package-owned `lmm-api.service` always executes:

```text
/usr/bin/lmm-api serve
```

A provider transition records the previous link target and package identity in
the immutable deployment manifest. There is no systemd rollback timer and no
automatic rollback. A post-mutation failure becomes `ROLLBACK_REQUIRED`; the
operator explicitly runs `confirm` or `rollback` through the public symlink.

## Production enablement gate

Rust may own business traffic only after all of the following are independently
verified:

1. Active PostgreSQL identity, schema contract, and forward-only boundary.
2. Route, authentication, quota, billing, streaming, and error-contract
   differentials against the immutable Go oracle.
3. N/N-1 expand-and-contract compatibility and singleton background-job leases.
4. Shared Valkey session, revocation, and fail-closed rate-limit semantics.
5. HTTP, SSE, and WebSocket draining/reconnection behavior without retrying
   non-idempotent requests.
6. Explicit route-by-route approval in `migration-gate.tsv`.
7. A manual rollback rehearsal using the same signed provider packages and
   shared deployment-state schema that production will use.

Until those gates pass and ownership is explicitly handed over, selecting Rust
for production business traffic is rejected.

## Current verification

Use read-only route evidence checks:

```bash
awk -F '\t' 'NR > 1 { owner[$8]++; mount[$5]++; diff[$6]++ }
  END { for (k in owner) print "owner", k, owner[k];
        for (k in mount) print "mount", k, mount[k];
        for (k in diff) print "differential", k, diff[k] }' \
  apps/api-rust/tests/fixtures/routes/migration-gate.tsv
bash apps/api-rust/tests/scripts/check-migration-plan.sh
bash apps/api-rust/tests/scripts/check-real-integration-gates.sh
```

Historical differential suites require an external immutable Go tree:

```bash
LMM_GO_ORACLE_ROOT=/absolute/path/to/5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
```

Never use a dirty `apps/api-go` tree as oracle or rollback evidence. Never edit
gate rows merely to make a check pass.
