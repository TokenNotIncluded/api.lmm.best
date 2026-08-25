# AUR packages

Production converges on one public backend and operator command:
`/usr/bin/lmm-api`, owned by the Go package. Go and Web remain independently
versioned application packages.

| Role | Stable source | Prebuilt release | Build from Git | Installed command/payload |
| --- | --- | --- | --- | --- |
| Go backend/operator | `lmm-api-go` | `lmm-api-go-bin` | `lmm-api-go-git` | `/usr/bin/lmm-api` (`serve`, `deploy`, health and maintenance commands) |
| Web frontend | — | `lmm-api-web-bin` | — | `/usr/share/lmm-api-web/frontend-dist` |
| Rust preview | — | `lmm-api-rs-bin` | `lmm-api-rs-git` | `/usr/bin/lmm-api-rs` |

The historical compatibility release T0 (`lmm-api-go-bin` 0.1.59) keeps a
package-owned `/usr/bin/lmm-api-go` symlink and does not remove an
already-installed `lmm-api-deploy-bin`, so a rollback to N-1 cannot strand the
host. Releases 0.1.60 through 0.1.62 use the historical implicit T1 boundary.
Releases from 0.1.63 onward must include signed `CLI_TRANSITION_PHASE` metadata
(`t0` or `t1`); version ordering no longer selects the transition. Stable
source, `-git`, local, and prebuilt recipes all consume the same canonical
`lmm-api-cli-phase.sh` contract; they must not enter T1 independently. New
services, docs, automation, and release archives use `lmm-api`. No new
deploy-only artifact is published. For a binary release at or above 0.1.63,
the post-release pin must set `_lmm_declared_cli_phase` before the PKGBUILD's
top-level provides/conflicts/replaces arrays are evaluated, and `prepare()`
must match that declaration byte-for-byte with the signed release metadata.
Late variable overrides are invalid.

An explicit T1 package removes the alias and declares an exact
conflict/replacement for `lmm-api-deploy-bin`; its confirmed T0 Go package is
its rollback package and
already owns the operator user, sudoers, sysusers, and tmpfiles resources
needed after rollback. Local `pacman -U` does not apply `replaces`, so the armed
target controller verifies those T0 resources and the legacy package owner/Qkk,
removes that exact package, and only then installs T1.

The next `lmm-api-go-bin` release is Go-only. It owns the backend, stable
`/usr/bin/lmm-api` service/operator entry, `/etc/lmm-api-go/lmm-api-go.env`,
operator policy, edge policy, and
`/usr/lib/systemd/system/lmm-api.service.d/20-memory.conf`; it does not own
`frontend-dist`. `lmm-api-web-bin` is the sole owner of immutable production
frontend bytes and atomically activates them under `/srv/lmm-api-frontend`.
The shared service resolves frontend files through
`/srv/lmm-api-frontend/current`.

Tracked Go/Web binary recipes intentionally remain pinned to their already
published immutable assets until the next signed releases exist. Their
explicit legacy branches keep those old archives verifiable and buildable;
future versions fail closed unless split ownership and
`API_ROUTE_CONTRACT_REVISION` metadata are present. Go production packages
must not contain `.INSTALL`; their runtime dependency/provides/conflicts/
replaces contract is verified before promotion. The verifier reads actual
archive headers to require root ownership, reject setuid/setgid or writable
payloads, match signed-member modes, and cross-check sudoers headers with
`.MTREE`. Web
0.1.41/0.1.42 retain one pinned install-hook digest for compatibility; Web
releases from 0.1.43 onward must include `lmm-api-web.install` in the signed
release, and the local AUR hook must match it exactly. After publication, a
separate post-release pin commit must update only the exact `pkgver`, immutable
asset SHA-256 values, release revision metadata where applicable, descriptions
that still mention legacy ownership, and regenerated `.SRCINFO`. Never use
`SKIP`, a placeholder hash, or unverified metadata for that pin.

The Rust packages remain separate until the Rust backend satisfies the same
production route and ownership gates. A Rust cutover is not part of a Go/Web
release.

Run these checks after changing a recipe:

```bash
TMPDIR="${TMPDIR:?marker-owned workspace required}" bash packaging/aur/test-matrix.sh
TMPDIR="$TMPDIR" bash packaging/aur/verify-go-release-pins.sh
TMPDIR="$TMPDIR" bash packaging/aur/test-bin-makepkg.sh
TMPDIR="$TMPDIR" bash deploy/production/test-release-artifact-contract.sh
```

Regenerate every changed tracked `.SRCINFO` with
`makepkg --printsrcinfo > .SRCINFO` and compare it before commit. Export a Go
recipe into a new standalone package-base directory only through the canonical
stager:

```bash
packaging/aur/export-go-package-base.sh lmm-api-go-bin "$DESTINATION"
```

The destination must not already exist. The stager restricts the file
inventory, verifies the canonical helper digest, and materializes
`lmm-api-cli-phase.sh` as a regular file. Never copy or publish the monorepo
symlink directly.
