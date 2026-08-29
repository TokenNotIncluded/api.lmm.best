# AUR packages

The backend providers are independently versioned real executables. Production
and operator actions enter through a separately managed one-hop provider link.

| Role | Stable source | Prebuilt release | Build from Git | Installed payload |
| --- | --- | --- | --- | --- |
| Go provider | `lmm-api-go` | `lmm-api-go-bin` | `lmm-api-go-git` | real `/usr/bin/lmm-api-go` plus current shared runtime assets |
| Rust provider | — | — | `lmm-api-rs-git` | real `/usr/bin/lmm-api-rs` |
| Web frontend | — | `lmm-api-web-bin` | — | `/usr/share/lmm-api-web/frontend-dist` and signed CLI install hook |

`/usr/bin/lmm-api` is not a regular provider payload and is not a reverse alias.
It is a one-hop relative link to exactly `lmm-api-go` or `lmm-api-rs`, selected
atomically by the already verified public CLI. New provider packages do not own
the link and do not conflict merely because the other provider is installed.
They provide the virtual `lmm-api-provider` capability for packages that require
a working backend CLI.

Production services, package hooks, and operator commands invoke only
`/usr/bin/lmm-api`. Package inspection may name provider files, and a release
candidate may construct a verified workspace symlink named `lmm-api`, but no
deployment command directly executes `lmm-api-go` or `lmm-api-rs`.

## Legacy migration

The signed `lmm-api-go-bin 0.1.69-1` layout may own a real
`/usr/bin/lmm-api` and expose `lmm-api-go -> lmm-api`. Accept that exact layout
only as N-1 migration or rollback evidence. A package at or above 0.2.0 must
contain only the real provider executable and must not contain
`CLI_TRANSITION_PHASE`, a generic executable, reverse alias, or deploy-only CLI.

The first 0.2.x upgrade runs from a signed workspace symlink, upgrades the Go
package, then atomically creates `/usr/bin/lmm-api -> lmm-api-go` before service
start. Explicit rollback removes that verified link before reinstalling the
exact legacy package. There is no timed or automatic rollback.

## Package ownership

Go currently owns the shared systemd service, operator policy, protected Go
environment, memory limits, and edge-policy assets; it does not own Web bytes.
The service always executes `/usr/bin/lmm-api serve`. Rust may coexist for CLI
and parity work but may not own production business traffic until the route
migration gate and provider handover are explicitly approved.

`lmm-api-web-bin` solely owns immutable frontend bytes. Its signed install hook
calls:

```text
/usr/bin/lmm-api deploy frontend package-activate --package-version <version>
```

It does not package `frontend-release.sh`, `lmm-api-web-activate`, or any other
shell publisher. Frontend activation and explicit rollback are provider CLI
operations with shared state contracts.

Go production packages must not contain `.INSTALL`. Web releases include
`lmm-api-web.install` in the signed release and the local AUR hook must match it
exactly. Archive verification requires root ownership, safe file types/modes,
no setuid/setgid or writable payloads, signed-member parity, immutable release
SHA-256 metadata, provider-correct filenames, and exact route-contract revision.

## Immutable release pins

Tracked binary recipes remain pinned to already published immutable assets until
a new signed release exists. After publication, a separate authorized pin commit
updates only exact `pkgver`, asset/checksum/revision metadata, descriptions, and
regenerated `.SRCINFO`. Never use `SKIP`, placeholders, mutable URLs, or
unverified metadata.

Rust remains source-built through `lmm-api-rs-git` until an independent signed
Rust binary-release workflow and pinned `lmm-api-rs-bin` recipe exist. A Rust
package or provider link is not production ownership evidence.

## Validation

Run from a marker-owned workspace:

```bash
TMPDIR="${TMPDIR:?marker-owned workspace required}" bash packaging/aur/test-matrix.sh
TMPDIR="$TMPDIR" bash packaging/aur/verify-go-release-pins.sh
TMPDIR="$TMPDIR" bash packaging/aur/test-bin-makepkg.sh
cd apps/api-go && go test ./internal/appcli
cd apps/api-rust && cargo test --locked
```

Regenerate every changed `.SRCINFO` with:

```bash
makepkg --printsrcinfo > .SRCINFO
```

Export a Go recipe into a new standalone package-base directory only through
`packaging/aur/export-go-package-base.sh`; the destination must not already
exist. The export verifies a bounded file inventory and copies regular package
inputs. It must not materialize the retired CLI phase helper or a provider-link
payload.
