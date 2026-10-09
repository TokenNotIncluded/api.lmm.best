# Component release architecture

Production release identities are component-scoped:

| Component | Tag | Workflow | Published artifact |
| --- | --- | --- | --- |
| Go provider | `go-vX.Y.Z` | `release-go.yml` | signed archives containing real `lmm-api-go` plus package contracts |
| Web frontend | `web-vX.Y.Z` | `release-web.yml` | signed immutable frontend archive |
| Rust core / Go extensions | none yet | WIP checks only | independent Docker build contexts; no production publication or cutover |

The historical root `VERSION`, `prepare-release.yml`, `promote-release.yml`,
`release.yml`, and `scripts/release.mjs` coupled three independently moving
components behind a generic `v*` identity. They are retired. Existing generic
tags and releases remain immutable historical records; they must not be moved,
deleted, recreated, or used as rollback evidence.

## Publication gate

A component tag must resolve to an exact commit reachable from the default
branch. Its workflow then requires successful CI, CodeQL, and release-contract
checks for that commit before building. Re-verifying an already published pin
accepts the released tree being reachable from the default branch as well, so
that a content-neutral history rewrite cannot retroactively orphan a release
that still reproduces byte for byte. The tracked AUR version must be older
than the proposed tag. Assets are checksum-bound, signed with the component
workflow's Sigstore identity, and verified before publication.

Go and Web publishers explicitly select their 12-check inventory with
`verify-release-commit-checks.sh COMMIT_SHA --component go|web`. It retains Go,
Web, package, mixed route-safety, non-Rust CodeQL, the Go/Web CI aggregate, and
the existing server qualification gate, including real database safety. Only
the three pure Rust checks are outside this component inventory. A completed CI
or dynamic CodeQL workflow may have failed in its Rust portion only when every
selected check and aggregate succeeds for the same main commit and selected
workflow run. CI and CodeQL may still be running when every selected check has
completed successfully; the newest check in that run must succeed, so an older
success cannot cover a queued or running retry. Cancellation, missing checks,
and failed or unfinished selected checks still reject publication; the Go-only
server workflow must complete successfully. The
default invocation and `--component rust` retain the original 14-check inventory
and require successful parent workflows.

Creating a tag is deliberately an operator-controlled action. There is no
workflow that infers a release from a root version-file change. A future
component promoter may automate tag creation only after it proves the same
commit checks and uses separately reviewed Go/Web version metadata.

## Compatibility

Semantic versions remain independent. Compatibility is established by the
content hash emitted by:

```bash
cd /path/to/api.lmm.best
/usr/bin/lmm-api-deploy contract route print
```

Both candidate packages must carry the expected
`API_ROUTE_CONTRACT_REVISION`, and the production deployment controller rejects
mixed candidates or rollback pairs whose contract revisions differ. The
contract revision checks route shape, not business behavior. A signed Web
release does not deploy automatically: independent Web updates require an
operator to check both active Go backends before manually dispatching the
frontend-only workflow. Changes requiring a new Go backend use the native
combined transaction until an explicit paired/independent compatibility gate
exists.

## New core and extension boundary

The former Rust native package and provider-switch deployment have been removed.
The [new services](core-migration.md) have separate Docker build contexts and
Compose projects. No new production image publication pipeline exists yet.
Before publication, add independent version tags, immutable image digests,
provenance/signature checks and compatibility tests. An extension release must
not restart the core or mutate its database schema.

## Rollback

Rollback is explicit and selects previously verified component packages by
their own versions and matching route-contract revision. Provider restoration
also restores the verified one-hop `/usr/bin/lmm-api` target. There is no timed
or automatic rollback. Never translate a historical generic `vX.Y.Z` into
assumed Go, Web, or Rust component versions.
