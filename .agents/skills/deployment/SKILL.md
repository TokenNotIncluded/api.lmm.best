---
name: deployment
description: "Use for api.lmm.best releases, deployment, upgrades, rollback, CI publication, or slow deployment workflows. Select the existing installation path, reuse signed artifacts, and verify the exact deployment result."
---

# Release and deployment

Read `docs/deployment-workflow.md` before acting. Read the selected workflow and
its caller at the current revision. Do not rely on a previous conversation's
release number, a stale local binary, or an assumed automatic deployment trigger.

## Establish the boundary

Identify the repository, requested component, immutable source revision, target
installation type, release tag, and whether the user authorized production writes.
Use connected repository/server reads when available. Do not ask for facts those
reads can establish. Changes to tooling alone do not authorize a release or a
production rollout. Never use the archived personal fork as the default target.

Capture one read-only preflight for the frozen batch: component Git objects,
both hosts' installed and active Go/Web identities, PostgreSQL/schema identity,
native owner/transaction status, `lmm*` units and the exact existing release/run.
Use `docs/deployment-workflow.md` ("Read the live state first"). Re-read mutable
facts at native phase boundaries; do not repeat the entire source audit between
every field or host. Also look for unpushed fixes in other worktrees and
earlier sessions' last reported step. An extra transient unit (for example a
`lmm-merchant-portable-*` writer-capsule holder) means a backend transaction is
unfinished: report it, do not stop it, and do not start another backend rollout.

Choose backend-only, frontend-only or combined before building. Compare the
frozen candidate with the last release using the complete component paths in
`scripts/local-release-tests.py`, including packaging and controller inputs.
Ship only changed components; an unchanged frontend keeps its verified signed
package and active identity during a backend update.

Choose one path:

- Web-only, compatible with both active Go backends: check installation ownership
  first. Package-owned hosts require the signed installed-package/native path;
  the current archive-only Actions deploy does not update pacman. Use
  `just release-web` for publication only until the required native installation
  is complete. `just ship-web` applies to archive-managed targets. The existing
  hybrid order is `just release-web`, then native installation/confirmation and
  external proof, then `just deploy-web TAG`; ship cannot pause for that native
  step. See the canonical prerequisites in `docs/production-release-transaction.md`
  and the frontend path warning in `docs/deployment-workflow.md`.
- Standalone Go/systemd: the Python `systemd` path in the same entrypoint; read
  `docs/manual-systemd-deployment.md`. Do not route package-owned files here.
- Package-owned Go/Web: installed `/usr/bin/lmm-api-deploy production`; read
  `docs/production-release-transaction.md` and `docs/seamless-upgrades.md`.
- Shared PostgreSQL migration: its reviewed plan and dedicated maintenance path,
  not a normal code update. Rust is an explicit preview only.

## Avoid repeated work

Build once through the official publisher, then deploy the same verified files.
Do not install toolchains or rebuild the app on production to update static assets.
Do not use `just build`, `just package`, or `go run` for a Web-only deployment.

For a new component release, inspect `.github/workflows/release-web.yml` or
`release-go.yml`; both currently use manual dispatch against their component tag.
Run relevant tests locally and retain a local-release-tests.py record before
publication. Reuse completed logs with its explicit import mode when appropriate.
The record must match the component Git objects at the release revision. Keep
the actual tested revision, command, exit and log hashes when importing results.
For packaging-only deltas, retain unchanged application checks and append the
necessary packaging checks plus exact source equivalence; do not claim a reused
run happened on the merge commit or repeat unchanged application builds. Keep
workflow paths: they are part of signature identity. Keep ancestry, artifacts
and production acceptance gates.

Tests run locally by user policy. Do not run or wait for test CI to publish.
Both component workflows accept required local_test_evidence JSON and verify it
without querying GitHub check runs. `just ship-web` verifies
LMM_LOCAL_TEST_EVIDENCE before tagging and sends that record to the publisher.
GitHub Actions owns release builds and official Sigstore signing; frontend
deployment must also match the host's installation ownership. Test/review
workflows remain manual diagnostics only.

For an existing release, inspect the complete signed asset set first. Reuse it.
If publication failed after signing, inspect the preserved signed workflow
artifact and exact release state before recovery. Do not rebuild/resign the same
version or overwrite partial assets by default.

For local legacy packaging, use `just package-go`, with the marker-owned workspace
set first. It no longer has an unconditional `build` prerequisite. Rebuild the
operator once if its source changed; otherwise reuse a reviewed compatible
provider. A first-time CLI bootstrap is distinct from an application rebuild.

Run targeted checks while editing. For deployment-entrypoint changes, use
`just test-deploy-entrypoint` and the relevant existing
standalone/native tests. Record the completed local checks before release. Do not
repeat a successful full build just because a network/readback step failed.
Reuse qualified native packages and exact target-side bytes as described in
`docs/production-release-transaction.md`; metadata differences belong in one
reviewed delta, not a newly copied, fully re-audited caller for each version.

## Frontend release in two steps

Follow the executable two-step guide in
`docs/deployment-workflow.md` ("Two explicit steps"). First fast-forward a clean
local `main`, run `bun install --frozen-lockfile`, then record Web typecheck,
test, build and bundle checks with `local-release-tests.py run --component web`.
Its gate requires matching source objects and successful recorded commands; it
does not require seven named slots. Add change-specific checks when needed.
Choose an actually unused, newer Web tag, sign and push it, then dispatch
`release-web.yml` at that tag with `local_test_evidence` and await its exact run.
Only after the signed Release succeeds, dispatch `deploy-web-frontend.yml` at
`main` with `release_tag`; require that exact run's success and public acceptance.
Review compatibility with both active Go providers first. This is the existing
archive workflow: it still does not update an Arch Web package or run its keep
hook. The existing hybrid path's native preinstallation, exact-ID receiver and
explicit retention prerequisites remain in `docs/production-release-transaction.md`.
These commands do not perform them or prove external package/full-tree equality.

## Execute and observe

Use the selected frontend path only after compatibility review and production
authorization. The archive-managed workstation path uses authenticated `gh`,
not a local Go provider or unrestricted server SSH. Host/port/public-origin settings belong in
the documented Actions variables; secrets belong in the existing secret store.
Never print or persist credentials in notes, patches or test evidence.

Run dependent caller phases continuously to their next real state boundary.
Keep this batch's immutable source fixed while newer remote changes form the
next batch; do not chase moving `main` during qualification or rollout.
Keep both hosts ordered in the same batch and version with only one shared-PG
owner active at a time; parallelize read-only preparation, not owners. If a
delegated agent is idle, use `followup_task` to resume it; `send_message` alone
does not start a new turn. Source-only templates are not `READY`: bind real file
profiles and successful qualification before handing off an executable caller.
Review schema changes in every batch; new tables or migrations cannot use an
ordinary same-schema capsule merely because a previous batch did.
Also verify the retained provider's sealed per-start restart compatibility after
the proposed schema change; additive DDL can invalidate its full catalog digest.

Record the exact run ID, release tag and source revision. Use `web status RUN_ID`
or `web watch RUN_ID`, not whichever run happens to be newest. A dispatch return
code, release page, or HTTP 200 alone does not prove deployment. Require the run's
completed success, both origins' publication checks, the public content hash,
and relevant functional checks. Distinguish requested, released and deployed.

## Failure and recovery

Retry bounded read/download operations only. Do not automatically repeat a
possibly accepted workflow dispatch, remote publish, migration or promotion.
Do not parallelize host mutations merely to reduce time; preserve rollout order.
Never disable signatures, revision checks, host-key verification, deployment
locks, backups or explicit transaction confirmation.

After a partial rollout, inspect each host and the recorded transaction. Existing
release IDs are not safe to overwrite. Use the documented explicit recovery or
rollback path; do not delete state to get past an error. Schema changes may
require database recovery, not just a binary rollback.

## Completion evidence

Report changed files, tests actually run, commit/PR, and what remains unverified.
State whether production was touched. Report duration improvements only from
comparable measured runs; otherwise report eliminated build/download operations.
A local fixture test does not prove a production migration or rollout succeeded.
For backend transactions, require final exact-ID `CONFIRMED`, the expected
provider/start generation, its single controlled restart, shared-PG owner/
integrity checks and public facts. Frontend-only updates must not restart Go.
