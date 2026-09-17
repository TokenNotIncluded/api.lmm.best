# Paired A/B deployment preparation

Status: draft, not wired into production. No release, package update, database migration, or service operation is performed by this change.

`apps/api-go/internal/deployslots` is the pure state-transition core for a pair of immutable Go/Web artifacts. A slot is a pair, never two independently selected versions. Even a Go-only update carries and verifies the unchanged Web identity. Package versions, source revisions and SHA-256 digests are retained together.

A confirmed native baseline initializes A. Staging reserves the inactive slot while retaining both confirmed pairs. `Begin` records an in-flight transaction before native activation is attempted; after that, timeouts or crashes cannot be treated as an aborted preparation. Only a matching native `CONFIRMED` or `ROLLED_BACK` receipt can resolve it. HTTP 200, `AWAITING_CONFIRMATION` and `ROLLBACK_REQUIRED` are not terminal acceptance. A generation mismatch or another pending transaction blocks a competing update. Each transition copies its input, so failed validation cannot mutate the last accepted state.

Rollback selects the previously confirmed Go/Web pair. It does NOT authorize a package downgrade against an incompatible database and does NOT roll back business data. The native single-writer, signature verification, billing-drain, migration compatibility, observation and health gates remain authoritative. Receipt is an internal adapter input, not a public authorization token or a user-supplied JSON proof.

## Remaining release blockers

- Wire this core into the native controller under the existing global deployment lock. Persist the generation and pending intent with fsync and atomic replacement; reject unsafe paths and do not run multiple writers.
- Stage the actual two signed artifacts into the inactive physical slot without overwriting the retained rollback archives. Bind package identities and Web index/assets to native evidence.
- Reconcile process crashes at each side-effect boundary. During `Pending.Started`, `Active` means last confirmed, not a claim about what is currently serving.
- Feed only verified native terminal records to `Finish`. Enforce current database compatibility again before rollback, retaining all original billing and backup gates.
- Provide explicit prepare/activate/confirm/rollback entry points in existing deployment/ops workflows, not a new conditional-test workflow. Exercise failed migrations, startup failures, health failures, interrupted confirmation and resource constraints on a disposable host.
- Perform full source CI and production-shaped acceptance before removing draft status. Do not tag, publish or deploy during preparation.

## Executed local validation

```sh
cd apps/api-go/internal/deployslots
GO111MODULE=off go test -race -count=1 -v .
```

Eight test functions (plus Go-only/Web-only subtests) pass with the installed Go 1.23.2 using only the standard library. This is not a full Go 1.25 application build or a server deployment test. Existing `go test ./...` jobs include this package; the previous extra A/B job that silently passed when test files were missing has been removed by taking the current main CI unchanged. No new workflow is introduced.
