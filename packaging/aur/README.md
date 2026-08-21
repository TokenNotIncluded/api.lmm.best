# AUR packages

Production uses one tooling-only deployment operator plus independently owned
Go backend and Web frontend packages.

| Role | Stable source | Prebuilt release | Build from Git | Installed command/payload |
| --- | --- | --- | --- | --- |
| Deployment operator | — | `lmm-api-deploy-bin` | — | `/usr/bin/lmm-api-deploy` → `/usr/lib/lmm-api-deploy/lmm-api-go` |
| Go backend | `lmm-api-go` | `lmm-api-go-bin` | `lmm-api-go-git` | `/usr/bin/lmm-api` → `lmm-api-go` |
| Web frontend | — | `lmm-api-web-bin` | — | `/usr/share/lmm-api-web/frontend-dist` |
| Rust preview | — | `lmm-api-rs-bin` | `lmm-api-rs-git` | `/usr/bin/lmm-api-rs` |

`lmm-api-deploy-bin` is a canonical tooling-only bootstrap. It extracts the
signed Go release binary into an independent package payload under
`/usr/lib/lmm-api-deploy`, owns the `/usr/bin/lmm-api-deploy` link, and installs
license, Git revision, operator-byte hash, release-asset hash, and contract
metadata when the source release provides it. It must not own a systemd unit,
application environment, database state, nginx configuration, frontend
payload, or active frontend link. Installing it with non-root `paru` is not an
application switch.

The next `lmm-api-go-bin` release is Go-only. It owns the backend, stable
`/usr/bin/lmm-api` service entry, `/etc/lmm-api-go/lmm-api-go.env`, edge policy,
and `/usr/lib/systemd/system/lmm-api.service.d/20-memory.conf`; it does not own
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
