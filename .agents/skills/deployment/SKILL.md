---
name: deployment
description: "Release, deploy, upgrade or recover api.lmm.best. Select the component and installation owner, reuse signed artifacts, and verify exact IDs. Not a gate for ordinary code edits."
---

# Release and deployment

Choose the requested outcome before doing work: tooling edit, signed release,
existing-release deployment, or recovery. Read only the matching section of
[the deployment workflow](../../../docs/deployment-workflow.md).

## Select one path

| Outcome or target | Path |
| --- | --- |
| Tooling/docs edit only | Relevant local checks; no host preflight, tag or production access |
| New Web release only | `just release-web`; source-matched local evidence; no server deployment |
| Existing qualified Web release, archive-managed hosts | `just deploy-web TAG`; no build or new tag |
| New Web release and authorized archive deployment | `just ship-web`; publish and deploy the same artifact |
| Package-owned Go/Web | Installed `/usr/bin/lmm-api-deploy production`; [native transactions](../../../docs/production-release-transaction.md) |
| Standalone Go/systemd | Separate tool's `systemd` path; [standalone guide](../../../docs/manual-systemd-deployment.md) |
| Schema change or interrupted native transaction | Reviewed maintenance/recovery path; not an ordinary binary update |

The archive Web workflow does not update an Arch Web package or run its retention
hook. Package-owned hosts must use the native path. The existing hybrid path
requires native installation/confirmation, same-ID receiver and retention proof
before archive dispatch; use the canonical native guide, not `ship-web`.
Rust remains an explicit preview. Never route package-owned files through the
standalone path. API binaries do not accept `operator`; use the
[separate deployment tool](../../../docs/standalone-deployment-tool.md).

For new PostgreSQL schema changes, use pgroll migration files and isolated
rehearsal evidence as the default delivery format; read the
[pgroll integration boundary](../../../docs/pgroll-migrations.md). Production
still requires application view compatibility, one shared-PG migration owner,
catalog contracts and compatible per-start startup seals. Do not default to
`migrate --apply` for a new pgroll change. Existing application bootstrap and
historical recovery contracts keep their documented paths.

## Avoid repeated work

Freeze one source revision and choose backend-only, frontend-only or combined.
Read immutable source and artifact facts once per batch. Release-only work does
not need SSH or a database audit. Deployment preflight covers the selected hosts,
installation owner, active/candidate identities and compatibility. Backend/schema
work also requires native transaction, shared-PG owner and schema checks.
Refresh mutable state at phase boundaries, not the whole audit after every step.

Reuse source-matched local checks with `scripts/local-release-tests.py`. Keep
actual tested revisions, commands, exits and logs. Relevant packaging/workflow
changes can invalidate evidence. Never fabricate a passing record or relabel an
import as a fresh run. Tests run locally; the publisher builds and signs the
release. Do not wait for an assumed automatic test CI run.

Reuse complete signed releases and qualified native packages. An unchanged
component needs no rebuild, republish or restart. If only the deployment tool
changed, use `just build-deploy`, not a Go API build. Tool edits use
`just test-deploy-entrypoint` plus tests for the changed native behavior.

## Execute to the next real boundary

Use existing authorization; do not request it again for the unchanged action.
Read the selected workflow and caller when behavior is uncertain. Keep one
ordered production batch, one backend owner/mutation at a time, and both hosts
compatible. Review schema and retained-provider restart compatibility before
backend changes. An unfinished transaction or writer capsule must be resolved,
not stopped or deleted to permit another rollout.

Dispatch once. Retain and observe the exact run/transaction ID. If a write's
result is uncertain, read that state before doing anything else. Retry only
bounded safe reads/downloads, not promotion, migration or publication. A failed
read is not a reason to rebuild. Preserve signatures, source/ancestry checks,
host-key verification, locks, backups and required transaction confirmations.

## Done when

Report separately: requested, released, deployed, or recovered. A successful
request or HTTP 200 is insufficient. Web acceptance needs exact-run success,
both origin checks, public hashes/version and affected UI checks. Web-only work
must not restart Go. Native completion needs exact-ID `CONFIRMED`, expected
provider/start generation, controlled restart and owner/integrity/public checks
specified by the selected native plan. State what ran and what remains unverified.
Do not claim a time saving without comparable measurements.
