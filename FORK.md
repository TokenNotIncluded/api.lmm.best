# LMM API fork

LMM API is a maintained fork of [QuantumNous/new-api](https://github.com/QuantumNous/new-api).

- Upstream Go snapshot: commit `ba2e9287bb7a8002116c03daa4c457a330054871`, dated 2026-08-29
- Upstream head reviewed: `7aa3531ef4c247ad4891c06cc8ed9d0ffb73fecc` (QuantumNous/new-api main as of 2026-10-08)
- License: GNU Affero General Public License v3.0 (`AGPL-3.0`)
- Local user-facing brand: `LMM API`
- Go module identity: `github.com/LIghtJUNction/api.lmm.best`
- Fork-specific modifications: `Copyright (C) 2026 LIghtJUNction`

Commits `ac381acf..7aa3531e` were reviewed. This batch ports `feefe09f2` (isolate TLS configs; the Waffo Pancake store-ID checks in that commit were not applied), `0f2a2075a` (relay request validation returns HTTP 400), `2506e1b98` (reject non-standard roles on user creation), `789c97019` (preserve Claude `safeguards`), `8c8c4153d` (keep quota when scanning usage rpm/tpm), and `b7017c251` (confirm a system-task lease before treating a no-op state write as lock loss). `1751f43ee` (SQLite WAL, pragma busy timeout, and `_txlock=immediate`) was left out: the Redis-off local runtime still opens `common.SQLitePath`, and `lmm-db-migrate` rejects any source that has `-wal`, `-journal`, or `-shm` sidecars (`docs/postgresql-migration.md`). Turning WAL on by default would make that local database look live to the migrator, and `_txlock=immediate` would change lock timing for every local transaction. The rest of the range was deferred or skipped: the task/JS plugin host (`docs/task-plugin-host-decision.md`), upstream auth/access-token refactors, the model/vendor and pricing rework, web-only changes, and docs/build.

The upstream copyright notices, attribution, `NOTICE`, and
`THIRD-PARTY-LICENSES.md` are preserved. Modified user interfaces must retain
the original-project link and attribution required by `NOTICE`.

New or fork-modified frontend source files retain the upstream header and may
add the separate LIghtJUNction modification notice described in `NOTICE`.

The current synchronization intentionally covers the Go backend subtree. It
preserves the fork's JS-safe wallet bounds, payment/subscription/assistant/
HeroSMS/bounty transaction contracts, and Redis-off local runtime. The shared
frontend and repository-specific CI remain maintained at the root.

Upstream updates should be imported as reviewed, pinned snapshots. Compare the
new snapshot with the recorded commit, preserve local branding as a small
focused patch, retain compatibility identifiers, and run root plus relaykit
`go test ./...`, `go vet ./...`, the CGO-disabled production build, wallet and
billing race tests, migration dry-run/rollback, and provider-path checks before
accepting the update.

## Native TypeSafe Jev support

The Go System One decision relay is a native behavioral port of the official
TypeSafe `1.0.0` task plugin at `QuantumNous/new-api-plugins` commit
`97a16e9a8d98b73ffb76e1c5a00b4d0f855f4d0a` (Apache-2.0). It retains LMM's native
relay and billing owners; it does not import the upstream JavaScript task host.
See [`docs/jev-native-relay.md`](docs/jev-native-relay.md) for the protocol,
configuration, and acceptance boundary.
