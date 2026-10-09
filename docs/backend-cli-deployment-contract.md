# Backend CLI deployment contract

This document is normative for backend packaging and production deployment.

## Executable layout

The backend providers are real, independently packaged executables:

```text
/usr/bin/lmm-api-go
/usr/bin/lmm-api-rs
```

The public backend entry point is a relative symbolic link:

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

## Separate deployment executable

Services, migration and API probes use the backend entry. Deployment actions use
`/usr/bin/lmm-api-deploy`, a fixed script that executes
`/usr/lib/lmm-api-deploy/engine`. The Go backend rejects `operator` and `deploy`;
it does not link the deployment implementation or fall back to executing it.

```text
/usr/bin/lmm-api serve
/usr/bin/lmm-api migrate --verify
/usr/bin/lmm-api-deploy production status ...
/usr/bin/lmm-api-deploy production confirm ...
/usr/bin/lmm-api-deploy production rollback ...
```

The shared Go release includes two separately built executables: `lmm-api-go`
and `lmm-api-deploy-engine`. Signed archive/package parity covers both. Modern
plans bind `deploy_engine_sha256` independently from the backend payload hash.
The operator must match the tool hash; the HTTP/migration probe must match the
backend hash. A missing or different tool is an error, not permission to use the
backend as a substitute. Candidate backend validation still uses a verified
release-scoped one-hop `lmm-api` symlink.

The API owns `serve`, `version`, `status`, `doctor`, `request`, explicit migrations
and the merchant writer gate. The separate tool owns publication, transactions,
backups, recovery, edge policy and package construction. Provider selection must
not change an unfinished transaction's retained tool identity. This change does
not claim Rust deployment parity or authorize a Rust production switch.

See [build, installation and transition steps](standalone-deployment-tool.md).

## Optional production backups

Release-plan format 6 requires an explicit `disabled` or `controller-only` backup
mode. Go-only, Web-only, and combined releases may disable backups. Selected
controller-only backups require authenticated verification of the complete local
collection; target hosts receive signed metadata rather than archives or keys.
The deployment tool MUST validate evidence format 3 and retain legacy readers
for existing transactions. Optional backups do not replace verified N-1 packages or
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

The API dispatcher lives in `apps/api-go/internal/appcli`. Deployment code and
its existing safety tests live in `apps/api-go/internal/deploycli`; only
`apps/api-go/cmd/lmm-api-deploy` links that implementation. External scripts
remain in `scripts/` and immutable service/configuration assets remain in
`packaging/`. The removed root `deploy/` directory is not reintroduced.

`scripts/check-deploy-isolation.sh` checks the API's actual Go dependency graph,
removed command exit codes, separate capabilities, and local frontend
publication/rollback. Deployment tests must run for both command packages.

## Public outage information

The edge serves a standalone LMM Best document when it cannot reach the service.
Browser GET/HEAD requests accepting HTML receive that document; API clients
receive a structured 503 response. Upstream API error bodies are not intercepted.
The document does not depend on the application, its JavaScript bundle, or fonts.
Its readable source is `packaging/common/lmm-api/edge-policy/service-unavailable.html`;
run `node scripts/generate-nginx-error-page.mjs` after editing it.

The separate deployment tool publishes only a small public status document beside
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

The deployment tool can consume a one-instance acknowledgement at
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
rollback payload hash. For signed Go 0.2.52 through 0.2.62 releases, it selects
the entry point from the exact release tag and Git revision in the already
verified package metadata. An unfamiliar retained legacy release must provide its read-only
`/usr/bin/lmm-api operator capabilities` command. Modern signed releases instead
use the independently verified deployment tool for that capability read. Its JSON format 1 response is
`{"format":1,"workspace_create":"operator"}`. The controller rejects missing,
oversized, malformed, duplicate, or unknown capability fields before creating
a workspace. Human-readable help is not a capability contract. The public
deployment entry point remains `/usr/bin/lmm-api-deploy`.

An SSH error after workspace creation is not permission to invoke a second
command spelling. The controller can recover the response only from the exact
root-owned native workspace and ACTIVE transaction markers. A manifest, status,
foreign transaction, unsafe path or incomplete marker requires reconciliation;
it is never relabelled WORKSPACE_CREATED. Existing controller workspace state
continues directly through signed artifact staging and native validation.

This bootstrap compatibility does not prove database rollback compatibility.
The authorization baseline change tracked in #386 requires a separate isolated
upgrade rehearsal and an approved manual rollout plan.
