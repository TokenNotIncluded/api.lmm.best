# Backend CLI deployment contract

This document is normative for backend packaging and production deployment.

## Executable layout

The backend providers are real, independently packaged executables:

```text
/usr/bin/lmm-api-go
/usr/bin/lmm-api-rs
```

The public backend and operator entry point is always a relative symbolic link:

```text
/usr/bin/lmm-api -> lmm-api-go
# or
/usr/bin/lmm-api -> lmm-api-rs
```

`/usr/bin/lmm-api` MUST NOT be a regular provider executable. Provider packages
MUST NOT install reverse aliases such as `lmm-api-go -> lmm-api`. Both provider
packages may coexist; neither package owns a fixed `/usr/bin/lmm-api` payload.

A backend selection operation MUST create a temporary relative symlink in
`/usr/bin`, verify its one-hop target is exactly `lmm-api-go` or `lmm-api-rs`,
and atomically rename it over `/usr/bin/lmm-api`. It MUST sync `/usr/bin` before
reporting success. Symlink chains, absolute targets, missing targets, writable
provider binaries, and provider binaries without verified package ownership are
hard failures.

## Invocation invariant

Production services and operator actions MUST invoke `/usr/bin/lmm-api`:

```text
/usr/bin/lmm-api serve
/usr/bin/lmm-api migrate --verify
/usr/bin/lmm-api deploy production status ...
/usr/bin/lmm-api deploy production confirm ...
/usr/bin/lmm-api deploy production rollback ...
```

Deployment code MUST NOT directly execute `/usr/bin/lmm-api-go` or
`/usr/bin/lmm-api-rs`. Candidate validation uses a release-scoped symlink named
`lmm-api` whose one-hop target is the staged provider binary. Package inspection
may refer to provider filenames but may not use them as an operator entry point.

## CLI parity

`lmm-api-go` and `lmm-api-rs` MUST implement the same public command contract,
exit codes, deployment-state formats, and safety checks. At minimum this covers:

- `serve`, `version`, `status`, `doctor`, and `request`;
- `migrate --apply|--verify`;
- frontend publication and rollback;
- production planning, staging, promotion, status, confirmation, and rollback;
- backup creation, export, verification, and restore preflight;
- edge-policy installation and verification;
- build/release validation needed by packaging and CI.

A provider switch can occur while a deployment is awaiting confirmation.
Therefore either provider MUST be able to read, validate, confirm, or manually
roll back a transaction created by the other provider.

## Manual rollback

Production deployment has no scheduled or automatic rollback. It MUST NOT create
systemd rollback services or timers and MUST NOT invoke rollback from activation,
observation, cancellation, or process-exit handlers.

Before the first live mutation, the CLI persists an immutable manifest,
verified rollback artifacts, and a rollback-eligible state while holding the
transaction lock. A failure after that boundary becomes `ROLLBACK_REQUIRED` and
retains the lock and evidence. Recovery requires an explicit operator command:

```text
/usr/bin/lmm-api deploy production rollback ...
```

Healthy promotion completes the observation gate and stops at
`AWAITING_CONFIRMATION`. Only an explicit `confirm` or `rollback` makes the
transaction terminal. Failed rollback remains retryable and MUST retain its
recovery evidence.

## Repository layout

The root `deploy/` directory is not part of the target architecture. Runtime,
release, validation, migration, and recovery behavior belongs in both backend
CLIs or their provider-owned libraries. Immutable service/configuration assets
belong under packaging-owned directories. Shell-only deployment logic and shell
contract tests must be replaced by Go and Rust tests before `deploy/` is removed.

CI MUST fail if tracked code, workflows, packages, or documentation reintroduce
a runtime dependency on the removed `deploy/` path or invokes a provider binary
as the production operator entry point.
