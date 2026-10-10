# AUR packages

The backend providers are independently versioned real executables. Production
services enter through a separately managed one-hop provider link. Deployment
actions use the separately built and signed `/usr/lib/lmm-api-deploy/engine`.

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

Production services use `/usr/bin/lmm-api`. Deployment and Web package hooks
use `/usr/bin/lmm-api-deploy`, whose modern signed wrapper runs the separate
tool. Modern Go bundles carry `lmm-api-deploy-engine`; recipes install it at
`/usr/lib/lmm-api-deploy/engine` from the same signed bundle. Missing tool bytes
must not be replaced by a local backend. See
[tool separation](../../docs/standalone-deployment-tool.md).

## Legacy migration

The signed `lmm-api-go-bin 0.1.69-1` layout may own a real
`/usr/bin/lmm-api` and expose `lmm-api-go -> lmm-api`. Accept that exact layout
only as N-1 migration or rollback evidence. A package at or above 0.2.0 must
contain the real provider, plus the separate deployment tool when its signed
release contains one. It must not contain `CLI_TRANSITION_PHASE`, a generic
provider executable or reverse alias. Old rollback bundles stay unchanged.

The first 0.2.x upgrade runs from a signed workspace symlink, upgrades the Go
package, then atomically creates `/usr/bin/lmm-api -> lmm-api-go` before service
start. Explicit rollback removes that verified link before reinstalling the
exact legacy package. There is no timed or automatic rollback.

## Package ownership

Go currently owns the shared systemd service, operator policy, protected Go
environment, memory limits, and edge-policy assets; it does not own Web bytes.
The service always executes `/usr/bin/lmm-api serve`. Rust may coexist for CLI
and parity work but may not own production business traffic until the route
route gate and provider handover are explicitly approved.

`lmm-api-web-bin` solely owns immutable frontend bytes. Releases at or above
0.1.52 use the signed hook in `packaging/common/lmm-api/lmm-api-web.install`,
which calls:

```text
/usr/bin/lmm-api-deploy frontend package-activate --package-version <version>
```

Those releases do not package `frontend-release.sh`, `lmm-api-web-activate`, or
another shell publisher. The pinned 0.1.51 recipe remains an explicit immutable
legacy reproduction until 0.1.52 is published; the post-release pin commit then
replaces its local hook and removes its legacy publishers. Frontend activation
and explicit rollback otherwise belong to the separate deployment tool with retained transaction contracts.

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
TMPDIR="$TMPDIR" bash packaging/aur/test-verify-go-release-pins.sh
TMPDIR="$TMPDIR" bash packaging/aur/verify-go-release-pins.sh --pinned
TMPDIR="$TMPDIR" bash packaging/aur/test-bin-makepkg.sh
cd apps/api-go && go test ./internal/appcli
cd apps/api-rust && cargo test --locked
```

CI uses `--pinned` to verify the checked-in Go release's signed tag, ancestry,
final-release status, checksums, GitHub asset digests, and Sigstore bundles. A
subsequent release or AUR update does not invalidate an existing authentic pin.
Ancestry is satisfied by commit reachability, or, for a pin that predates a
content-neutral history rewrite, by the released tree still being reachable
from main. Renaming commits must not orphan a release that still reproduces
byte for byte; publishing source main never carried must still be rejected.
Before publishing an AUR update, run `verify-go-release-pins.sh --latest` in the
same workspace. This also requires the latest final Go release and rejects
candidates older than the live AUR packages. Omitting the flag retains this
stricter publication audit.

Regenerate every changed `.SRCINFO` with:

```bash
makepkg --printsrcinfo > .SRCINFO
```

Export a Go recipe into a new standalone package-base directory only through
`packaging/aur/export-go-package-base.sh`; the destination must not already
exist. The export verifies a bounded file inventory and copies regular package
inputs. It must not materialize the retired CLI phase helper or a provider-link
payload.
