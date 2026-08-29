# Component release architecture

Production release identities are component-scoped:

| Component | Tag | Workflow | Published artifact |
| --- | --- | --- | --- |
| Go backend/operator | `go-vX.Y.Z` | `release-go.yml` | signed Go archives and package contracts |
| Web frontend | `web-vX.Y.Z` | `release-web.yml` | signed immutable frontend archive |
| Rust candidate | none | CI only | short-lived preview/test artifacts, never production evidence |

The historical root `VERSION`, `prepare-release.yml`, `promote-release.yml`,
`release.yml`, and `scripts/release.mjs` coupled three independently moving
components behind a generic `v*` identity. They are retired. Existing generic
tags and releases remain immutable historical records; they must not be moved,
deleted, recreated, or used as rollback evidence.

## Publication gate

A component tag must resolve to an exact commit reachable from the default
branch. Its workflow then requires successful CI, CodeQL, and release-contract
checks for that commit before building. The tracked AUR version must be older
than the proposed tag. Assets are checksum-bound, signed with the component
workflow's Sigstore identity, and verified before publication.

Creating a tag is deliberately an operator-controlled action. There is no
workflow that infers a release from a root version-file change. A future
component promoter may automate tag creation only after it proves the same
commit checks and uses separately reviewed Go/Web version metadata.

## Compatibility

Semantic versions remain independent. Compatibility is established by the
content hash emitted by:

```bash
deploy/production/api-route-contract-revision.sh print
```

Both candidate packages must carry the expected
`API_ROUTE_CONTRACT_REVISION`, and the production deployment controller rejects
mixed candidates or rollback pairs whose contract revisions differ. The
contract revision is a compatibility assertion, not a shared product version.

## Rust boundary

Rust remains a loopback-only migration candidate. CI may build and test it, but
there is no stable Rust tag, prebuilt AUR package, or production publication
workflow. `lmm-api-rs-git` is source-preview-only and cannot be used as cutover
evidence. Reintroducing a signed Rust binary requires a dedicated tag namespace,
immutable asset contract, checksum-pinned AUR recipe, Sigstore identity, and an
approved route-ownership cutover.

## Rollback

Rollback selects previously verified component packages by their own versions
and matching route-contract revision. Never translate a historical generic
`vX.Y.Z` into assumed Go, Web, or Rust component versions.
