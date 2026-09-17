# Paired A/B deployment preparation

Status: draft, not wired into production. No release, package update, database migration, or service operation is performed by this change.

`apps/api-go/internal/deployslots` contains the state-transition core and a Linux file store for immutable Go/Web artifact pairs. A slot is a pair, never two independently selected versions. Even a Go-only update carries and verifies the unchanged Web identity. Package versions, source revisions and SHA-256 digests are retained together.

A confirmed native baseline initializes A. Staging reserves the inactive slot while retaining both confirmed pairs. `Begin` records an in-flight transaction before native activation is attempted; after that, timeouts or crashes cannot be treated as an aborted preparation. Only a matching native `CONFIRMED` or `ROLLED_BACK` receipt can resolve it. HTTP 200, `AWAITING_CONFIRMATION` and `ROLLBACK_REQUIRED` are not terminal acceptance. A generation mismatch or another pending transaction blocks a competing update. Each transition copies its input, so failed validation cannot mutate the last accepted state.

Rollback selects the previously confirmed Go/Web pair. It does NOT authorize a package downgrade against an incompatible database and does NOT roll back business data. The native single-writer, signature verification, billing-drain, migration compatibility, observation and health gates remain authoritative. Receipt is an internal adapter input, not a public authorization token or a user-supplied JSON proof.

## Durable planning state

`OpenFileStore` requires an existing absolute private directory owned by the process UID. The Linux implementation walks directories using no-follow directory descriptors, rejects symlinked ancestors, and accepts only private regular single-linked state/lock files. State JSON is bounded to 64 KiB, rejects duplicate/case-aliased keys, unknown fields, invalid UTF-8, excessive nesting and trailing documents, and must pass the transition core's validation.

`Initialize`, `Load` and `Update` serialize access with an OS file lock. `Update` compares the stored generation under that lock and requires exactly one increment. The caller must also hold the existing native production deployment lock; the store does not replace it. Transition callbacks are trusted internal code, not HTTP inputs, and must not switch services or perform other external effects.

Updates write a fresh exclusive temporary file in the same directory, fsync the file, atomically rename it to `state.json`, then fsync the directory. A failure before rename leaves the old state intact. Failure after rename is returned as `ErrDurability`: the replacement may already be visible, so native code must reload and reconcile, never blindly repeat activation. Interrupted writes do not clear a pending transaction. Orphan temporary files left by a crashed process are not treated as accepted state or automatically deleted as recovery evidence.

## Remaining release blockers

- Wire the transition core and FileStore into the native controller under the existing global deployment lock. Require a successful durable `Begin` before any activation side effect. The store alone is not production integration.
- Stage the actual two signed artifacts into the inactive physical slot without overwriting retained rollback archives. Bind package identities and Web index/assets to native evidence.
- Reconcile process crashes at each real side-effect boundary. During `Pending.Started`, `Active` means last confirmed, not a claim about what is currently serving.
- Feed only verified native terminal records to `Finish`. Enforce current database compatibility again before rollback, retaining all original billing and backup gates.
- Provide explicit prepare/activate/confirm/rollback entry points in existing deployment/ops workflows, not a new conditional-test workflow. Exercise failed migrations, startup failures, health failures, interrupted confirmation and resource constraints on a disposable host.
- Perform full source CI and production-shaped acceptance before removing draft status. Do not tag, publish or deploy during preparation.

## Executed local validation

The original transition core has eight regression test functions. This update adds nine Linux file-store test functions, including concurrent independent store instances, failed writes, post-rename fsync uncertainty, cancelled lock waits, unsafe files/directories, malformed JSON, and actual child-process exits immediately before/after rename. These process-crash tests are NOT physical power-loss or production traffic tests.

The new store tests were run against the exact original state.go blob `675575348790178344d81a99248cc69e993a7a39` with Go 1.23.2 and only the standard library:

```sh
GO111MODULE=off go vet .
GO111MODULE=off go test -race -count=1 -timeout=45s -v .
GO111MODULE=off go test -race -count=10 -timeout=40s .
```

All nine new test functions and their subtests passed; the ten-repeat race run also passed. No full Go 1.25 application build or server deployment was run locally. Existing `go test ./...` jobs include this package and its original tests; remote results must be reviewed separately. No workflow, version file, package pin or production request is changed.
