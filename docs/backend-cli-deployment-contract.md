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
/usr/bin/lmm-api-deploy production status ...
/usr/bin/lmm-api-deploy production confirm ...
/usr/bin/lmm-api-deploy production rollback ...
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

## Optional production backups

Release-plan format 6 requires an explicit `disabled` or `controller-only` backup
mode. Go-only, Web-only, and combined releases may disable backups. Selected
controller-only backups require authenticated verification of the complete local
collection; target hosts receive signed metadata rather than archives or keys.
Both providers MUST validate evidence format 3 and retain legacy readers for
existing transactions. Optional backups do not replace verified N-1 packages or
configuration rollback state.

[Controller-only backup evidence](controller-only-backup-format.md) defines the
wire format, freshness rules, transfer/retry behavior and recovery requirements.

## Manual rollback

Production deployment has no scheduled or automatic rollback. It MUST NOT create
systemd rollback services or timers and MUST NOT invoke rollback from activation,
observation, cancellation, or process-exit handlers.

Before the first live mutation, the CLI persists an immutable manifest,
verified rollback artifacts, and a rollback-eligible state while holding the
transaction lock. A failure after that boundary becomes `ROLLBACK_REQUIRED` and
retains the lock and evidence. Recovery requires an explicit operator command:

```text
/usr/bin/lmm-api-deploy production rollback ...
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

## Public outage information

The edge serves a standalone LMM Best document when it cannot reach the service.
Browser GET/HEAD requests accepting HTML receive that document; API clients
receive a structured 503 response. Upstream API error bodies are not intercepted.
The document does not depend on the application, its JavaScript bundle, or fonts.
Its readable source is `packaging/common/lmm-api/edge-policy/service-unavailable.html`;
run `node scripts/generate-nginx-error-page.mjs` after editing it.

The Go production operator publishes only a small public status document beside
the frontend releases. It never copies private failure messages, credentials,
configuration, or transaction manifests into that document. Deployment phases
update the explanation automatically. An estimate is absent unless an operator
supplies one. Expired estimates are labeled as expired; a recorded recovery is
confirmed with the live health endpoint before the page announces availability.

An operator can publish a reviewed explanation and a real estimate with:

```sh
/usr/bin/lmm-api-deploy production maintenance \
  --deployment-id "$DEPLOYMENT_ID" --confirm api.lmm.best \
  --state maintenance --service "LMM Best API" \
  --message "Scheduled service update" \
  --expected-recovery-at "$RECOVERY_AT"
```

`RECOVERY_AT` must be a future RFC3339 timestamp. Omit the last option when the
recovery time is unknown. Service/model names and explanations must describe the
actual affected scope. This command changes the notice only; it does not stop,
start, or bypass access controls for the service.

Memory override validation runs before stopping the backend on both activation
and rollback. Strictly parsed Go heap limits at or below the packaged 256 MiB
ceiling are retained, including the existing 192 MiB mitigation. Unknown cgroup
or executable directives still fail closed before the service is stopped.

## Legacy refund migration acceptance

The Go operator can consume a one-instance acknowledgement at
`<workspace>/state/legacy-refund-risk.json` for the historical
`lmm-api-go-bin 0.2.17-1` writer. It requires an explicit user decision and a
fresh, verified controller backup. The private, root-owned acknowledgement binds
the deployment ID, exact rollback package identity and digest, writer PID and
systemd invocation, and the digest and capture time of
`controller-backup-reference.json`. The reference is an unchanged copy of the
controller's verified database/roles/environment backup manifest; it is not
native format-3 backup evidence and does not claim target-side decryption.
Unknown outcomes remain `unknown`. Both records expire after 24 hours; malformed,
symlinked, incorrectly owned, or mismatched records fail closed.

This acknowledgement replaces only the historical refund-absence proof for
that exact writer. Admission closure, connection drain, normal process exit,
empty cgroup, shutdown journal, signed package verification, migrations and
health checks remain mandatory. The records are retained with the transaction
for later reconciliation. No credit adjustments are performed by this path.

New signed Go packages advertise `REFUND_TASK_DRAIN_CAPABILITY` with exact
contents `v1\n`. It is accepted only when package ownership, release metadata,
and the running executable match the transaction. Such writers must emit the
validated refund execution report during graceful shutdown: accepted equals
finished, active and failed are zero, and execution_complete is true. This
establishes execution completion; it does not certify historical financial
correctness. Historical intent-log absence is not required for these writers.
## Staging from a legacy native CLI

The controller supports the signed Go 0.2.52 `deploy` bootstrap and the current
private `operator` protocol. Before creating a workspace it verifies the
installed one-hop provider link, root ownership, safe mode and exact frozen
rollback payload hash, then selects the entry point from a read-only help call.
Unknown or ambiguous help fails before workspace creation. The public entry
point remains `/usr/bin/lmm-api-deploy`.

An SSH error after workspace creation is not permission to invoke a second
command spelling. The controller can recover the response only from the exact
root-owned native workspace and ACTIVE transaction markers. A manifest, status,
foreign transaction, unsafe path or incomplete marker requires reconciliation;
it is never relabelled WORKSPACE_CREATED. Existing controller workspace state
continues directly through signed artifact staging and native validation.

This bootstrap compatibility does not prove database rollback compatibility.
The authorization baseline change tracked in #386 requires a separate isolated
upgrade rehearsal and an approved manual rollout plan.
