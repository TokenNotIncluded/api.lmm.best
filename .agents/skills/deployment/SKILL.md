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

Choose one path:

- Web-only, compatible with both active Go backends: existing signed Web archive
  through `deploy-web-frontend.yml` and `scripts/lmm-api-deploy.sh web`.
- Standalone Go/systemd: the Python `systemd` path in the same entrypoint; read
  `docs/manual-systemd-deployment.md`. Do not route package-owned files here.
- Package-owned Go/Web: installed `/usr/bin/lmm-api-deploy production`; read
  `docs/production-release-transaction.md` and `docs/seamless-upgrades.md`.
- Shared PostgreSQL migration: its reviewed plan and dedicated maintenance path,
  not a normal code update. Rust is an explicit preview only.

## Avoid repeated work

Build once on the build machine/CI, then deploy the same verified files. Do not
install toolchains or rebuild the app on production to update static assets.
Do not use `just build`, `just package`, or `go run` for a Web-only deployment.

For a new component release, inspect `.github/workflows/release-web.yml` or
`release-go.yml`; both currently use manual dispatch against their component tag.
Verify required checks for that exact commit before publication. Keep the workflow
paths: they are part of signature identity. A green PR at another revision is not
a substitute. Do not weaken release checks to save time.

For an existing release, inspect the complete signed asset set first. Reuse it.
If publication failed after signing, inspect the preserved signed workflow
artifact and exact release state before recovery. Do not rebuild/resign the same
version or overwrite partial assets by default.

For local legacy packaging, use `just package-go`, with the marker-owned workspace
set first. It no longer has an unconditional `build` prerequisite. Rebuild the
operator once if its source changed; otherwise reuse a reviewed compatible
provider. A first-time CLI bootstrap is distinct from an application rebuild.

Run targeted checks while editing. For deployment-entrypoint changes, use
`python3 -B scripts/test-deploy-entrypoint.py -v` and the relevant existing
standalone/native tests. Run the required release checks before release. Do not
repeat a successful full build just because a network/readback step failed.

## Execute and observe

Use `just deploy-web TAG` only after compatibility review and production
authorization. The workstation path uses authenticated `gh`, not a local Go
provider or unrestricted server SSH. Host/port/public-origin settings belong in
the documented Actions variables; secrets belong in the existing secret store.
Never print or persist credentials in notes, patches or test evidence.

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
