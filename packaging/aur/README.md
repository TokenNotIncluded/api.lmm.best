# AUR packages

Production converges on one public backend and operator command:
`/usr/bin/lmm-api`, owned by the Go package. Go and Web remain independently
versioned application packages.

| Role | Stable source | Prebuilt release | Build from Git | Installed command/payload |
| --- | --- | --- | --- | --- |
| Go backend/operator | `lmm-api-go` | `lmm-api-go-bin` | `lmm-api-go-git` | `/usr/bin/lmm-api` (`serve`, `deploy`, health and maintenance commands) |
| Web frontend | — | `lmm-api-web-bin` | — | `/usr/share/lmm-api-web/frontend-dist` |
| Rust preview | — | `lmm-api-rs-bin` | `lmm-api-rs-git` | `/usr/bin/lmm-api-rs` |
| T0 legacy rollback only | — | `lmm-api-deploy-bin` | — | `/usr/bin/lmm-api-deploy`; removed in T1 |

The compatibility release T0 keeps a package-owned `/usr/bin/lmm-api-go`
symlink and does not yet remove an already-installed `lmm-api-deploy-bin`, so a
rollback to N-1 cannot strand the host. New services, docs, automation, and
release archives use `lmm-api`. No new deploy-only artifact is published.

The following T1 Go package removes the alias and declares an exact
conflict/replacement for `lmm-api-deploy-bin`; the historical T0 Go package is
its rollback package and already owns the operator user, sudoers, sysusers, and
tmpfiles resources needed after rollback.

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
`API_ROUTE_CONTRACT_REVISION` metadata are present. After publication, a
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
TMPDIR="$TMPDIR" bash packaging/aur/test-bin-makepkg.sh
TMPDIR="$TMPDIR" bash deploy/production/test-release-artifact-contract.sh
```

Regenerate every changed tracked `.SRCINFO` with
`makepkg --printsrcinfo > .SRCINFO` and compare it before commit.
