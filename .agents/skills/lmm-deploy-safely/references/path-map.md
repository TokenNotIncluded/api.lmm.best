# LMM deployment path map

This map follows the normative provider and manual-rollback contract in
`docs/backend-cli-deployment-contract.md`.

## Repository inputs

| Purpose | Path |
| --- | --- |
| Go provider source | `apps/api-go` |
| Rust provider source | `apps/api-rust` |
| Web source | `apps/web` |
| API/route compatibility version | `contracts/api-route/VERSION` |
| Go provider package recipes | `packaging/aur/lmm-api-go{,-bin,-git}` |
| Rust provider package recipe | `packaging/aur/lmm-api-rs-git` |
| Web package recipe | `packaging/aur/lmm-api-web-bin` |
| Shared service/config assets | `packaging/common/lmm-api` |
| Edge-policy assets | `packaging/common/lmm-api/edge-policy` |
| Dedicated Valkey assets | `packaging/common/valkey` |
| Backend CLI deployment contract | `docs/backend-cli-deployment-contract.md` |
| Rust ownership gate | `apps/api-rust/tests/fixtures/routes/migration-gate.tsv` |

The root `deploy/` directory is retired. Deployment behavior belongs in both
backend CLIs; immutable files belong under `packaging/`; language tests replace
shell-only deployment contract tests.

## Installed executable layout

| Purpose | Path or rule |
| --- | --- |
| Public service/operator entry | `/usr/bin/lmm-api` — one-hop relative symlink only |
| Go provider | `/usr/bin/lmm-api-go` — real package-owned executable |
| Rust provider | `/usr/bin/lmm-api-rs` — real package-owned executable |
| Go identity metadata | `/usr/share/doc/lmm-api-go-bin/{REVISION,API_ROUTE_CONTRACT_REVISION,RELEASE_ASSET_SHA256}` |
| Rust identity metadata | `/usr/share/doc/lmm-api-rs-git/{REVISION,API_ROUTE_CONTRACT_REVISION,RELEASE_ASSET_SHA256}` |
| Go environment | `/etc/lmm-api-go/lmm-api-go.env` |
| Rust environment | `/etc/lmm-api-rs/lmm-api-rs.env` |
| systemd service | `/usr/lib/systemd/system/lmm-api.service` |
| Runtime state | `/var/lib/lmm-api-go` until a separately rehearsed generic-state migration |
| Active frontend | `/srv/lmm-api-frontend/current` |

`/usr/bin/lmm-api` targets exactly `lmm-api-go` or `lmm-api-rs`. It must not be
a regular provider binary, absolute link, chain, or reverse alias. Production
service and operator actions always invoke `/usr/bin/lmm-api`; provider names
may appear only in package/release inspection and as a verified symlink target.

Legacy Go `0.1.x` packages may own a real `/usr/bin/lmm-api` and a reverse
`lmm-api-go -> lmm-api` alias. Treat that layout only as verified N-1 migration
or rollback evidence. A new release must never recreate it.

## Production transaction

| Purpose | Path or entry point |
| --- | --- |
| Controller command | `/usr/bin/lmm-api deploy production {plan,stage,promote,status,confirm,rollback}` |
| Backend selector | `/usr/bin/lmm-api backend {status,select}` |
| Default SSH alias | `ArchDmit` |
| Required hostname | `arch-dmit` |
| Controller workspace | `${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api/deploy-work/<deployment-id>` |
| Target workspace | `/var/lib/lmm-api-go-deploy/work/<deployment-id>` |
| Target optional backup | `/var/lib/lmm-api-go-deploy/backups/<deployment-id>` |
| Controller optional backup | `$HOME/backup/lmm-api/<verified-host>/<deployment-id>` |
| Off-host optional backup | `/home/arch/.local/state/lmm-api-production-backups/<deployment-id>` on `archczy` |
| Frontend releases | `/srv/lmm-api-frontend/releases/<version>` |
| Backend service | `lmm-api.service` |

The controller and target workspaces are marker-owned, persistent, private, and
never under `/tmp` or `/var/tmp`. Keep deployment IDs within
`[A-Za-z0-9][A-Za-z0-9._-]{0,127}`. A release-scoped candidate operator is
invoked through a strictly validated workspace symlink named `lmm-api`.

Before the first live mutation, persist the immutable manifest, exact N/N-1
package and provider-link identities, rollback artifacts, and a rollback-eligible
state. There is no systemd rollback service/timer and no automatic rollback.
Failures after the mutation boundary retain the lock/evidence and become
`ROLLBACK_REQUIRED`. Healthy activation observes at least 120 seconds and ends
in `AWAITING_CONFIRMATION`; only an explicit exact-ID `confirm` or `rollback`
produces a terminal state.

## Service and data paths

| Component | Path |
| --- | --- |
| PostgreSQL authority | live DSN from the root-only service environment; never print it |
| Dedicated Valkey | live URL from the root-only service environment; never print it |
| Go state | `/var/lib/lmm-api-go` |
| Frontend immutable payload | `/usr/share/lmm-api-web/frontend-dist` |
| Edge-policy restore evidence | `<target-workspace>/config-restore/nginx-edge` |
| DB-IP country database | `/var/lib/geoip2/DBIP-Country-Lite.mmdb` |

The live process environment, listeners, package ownership, process executable,
health probes, and current route gate are authoritative. Historical cutover or
rehearsal prose is not.

## Rust ownership

Go owns production business traffic until every affected row in
`apps/api-rust/tests/fixtures/routes/migration-gate.tsv` has independent
approval and the explicit provider handover is authorized. Rust CLI parity,
package installation, a provider symlink, or successful health probes do not
by themselves transfer route ownership.

Historical differential suites may use `LMM_GO_ORACLE_ROOT`, but it must point
to an external immutable revision tree. Never use a dirty local Go tree as an
oracle or rollback baseline.

## Workspace and backup lifecycle

Backups are optional and created only with explicit current-turn authorization.
When selected for production, verify target, controller, and off-host copies.
A backup root, active release, latest-known-good rollback package, transaction
lock, and nonterminal workspace are never cleanup targets.

After `CONFIRMED`, `ROLLED_BACK`, controller-only `VALIDATED`, or verified
pre-switch `ABORTED`, preview then remove only the exact workspace's disposable
`staging`, `tmp`, build cache, dependency cache, and package archive children.
Retain marker/status audit evidence. Reject unresolved variables, globs,
symlinks, `/`, home roots, `/tmp`, `/var/tmp`, backup roots, and broad release
roots.

The controller state root is bounded: warn at 256 MiB and stop new builds at
512 MiB or earlier when filesystem evidence is yellow. On production require
at least 4 GiB free before package/backup work; 80% root use is a stop and 90%
is an incident.
