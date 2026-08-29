# LMM API fork

LMM API is a maintained fork of [QuantumNous/new-api](https://github.com/QuantumNous/new-api).

- Upstream Go snapshot: commit `ba2e9287bb7a8002116c03daa4c457a330054871`, dated 2026-08-29
- Upstream head reviewed: `ac381acf4bf41204b97bb26b4c58c83275877a2e` (later commits through this head were Web/docs/build-only)
- License: GNU Affero General Public License v3.0 (`AGPL-3.0`)
- Local user-facing brand: `LMM API`
- Go module identity: `github.com/LIghtJUNction/api.lmm.best`
- Fork-specific modifications: `Copyright (C) 2026 LIghtJUNction`

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
