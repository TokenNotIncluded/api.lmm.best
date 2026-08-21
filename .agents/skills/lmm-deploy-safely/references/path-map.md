# LMM deployment path map

The tooling-only `lmm-api-deploy-bin` package owns the canonical operator
`/usr/bin/lmm-api-deploy`, which resolves to its independent signed payload at
`/usr/lib/lmm-api-deploy/lmm-api-go`. Use `lmm-api-deploy deploy ...` for every
deployment phase and verify systemd separately uses `/usr/bin/lmm-api serve`.
Never use the application package's `/usr/bin/lmm-api-go` as the production
operator after bootstrap.

## Controller and package inputs

| Purpose | Current path or entry point |
| --- | --- |
| Go artifact | `apps/api-go/out/lmm-api-go` |
| Rust artifacts | `apps/api-rust/target/release/lmm-api-rs`, `lmm-db-migrate` |
| Frontend build | `apps/web/dist` |
| Deployment operator AUR recipe | `packaging/aur/lmm-api-deploy-bin` |
| Go AUR recipe | `packaging/aur/lmm-api-go-bin` |
| Web AUR recipe | `packaging/aur/lmm-api-web-bin` |
| API/route compatibility contract | `deploy/production/API_ROUTE_CONTRACT` |
| Contract revision generator | `deploy/production/api-route-contract-revision.sh` |
| Persistent controller work | `${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api/deploy-work/<deployment-id>` |
| Durable controller backups | `$HOME/backup/lmm-api/<verified-host>/<deployment-id>` |
| Read-only production pressure report | `.agents/skills/lmm-deploy-safely/scripts/resource-pressure-report.sh` |

Already published legacy Go packages may still install a bundled fallback at
`/usr/share/lmm-api-go/frontend-dist`. The next Go release must not contain or
own that path. The independent Web package owns the immutable payload under
`/usr/share/lmm-api-web/frontend-dist` and activation under
`/srv/lmm-api-frontend`; the shared service default is the package-owned
`/srv/lmm-api-frontend/current` link. A legacy fallback is rollback evidence,
not an allowed source for a new split release.

## Historical parity oracle input

Historical Go/Rust differential scripts accept an optional external immutable
Go source tree through `LMM_GO_ORACLE_ROOT`. Set it to the absolute path of the
exact revision-named tree, for example:

```bash
LMM_GO_ORACLE_ROOT=/absolute/path/to/5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
```

Keep this input outside the repository, require the consumer's existing
revision and checksum checks to pass, and treat it only as parity evidence.
Never treat the current dirty `apps/api-go` working tree as a frozen oracle or
substitute it when `LMM_GO_ORACLE_ROOT` is absent. This contract does not alter
deployment backup, rollback, or retention requirements.

## Shared installed package layout

| Purpose | Current path |
| --- | --- |
| Canonical operator CLI | `/usr/bin/lmm-api-deploy` (owned by `lmm-api-deploy-bin`) |
| Independent operator payload | `/usr/lib/lmm-api-deploy/lmm-api-go` |
| Operator identity metadata | `/usr/share/doc/lmm-api-deploy-bin/{REVISION,OPERATOR_SHA256,RELEASE_ASSET_SHA256}` |
| Service entry | `/usr/bin/lmm-api` (owned by `lmm-api-go-bin`) |
| Application environment | `/etc/lmm-api-go/lmm-api-go.env` |
| systemd unit | `/usr/lib/systemd/system/lmm-api.service` |
| Package memory drop-in | `/usr/lib/systemd/system/lmm-api.service.d/20-memory.conf` |
| Runtime state | `/var/lib/lmm-api-go` via `StateDirectory=lmm-api-go` |
| Legacy-only bundled fallback | `/usr/share/lmm-api-go/frontend-dist` |
| Web package payload | `/usr/share/lmm-api-web/frontend-dist` |
| Web activation tool | `/usr/lib/lmm-api-web/lmm-api-web-activate` |
| Service port | `3000` |

Production uses independent `lmm-api-go-bin` and `lmm-api-web-bin` packages.
Rust has a separate ownership gate and is out of scope for a Go/Web update.
Package discovery alone does not authorize a switch; the guarded transaction
does. The service unit invokes exactly `/usr/bin/lmm-api serve`.

Before using the transaction on an existing target, verify that the installed
core really provides this launcher protocol. A legacy `/usr/bin/lmm-api` may be
the provider binary itself and may start the backend when given an unknown
subcommand. Such a target is pre-transaction: inspect it with systemd, package,
process, and sanitized HTTP probes, then upgrade the core package through the
guarded path before calling `deploy` phases.

## Production transaction

| Purpose | Current path or entry point |
| --- | --- |
| Controller entry point | `/usr/bin/lmm-api-deploy deploy production ...` |
| Target activator | Immutable payload under the marker-owned deployment workspace |
| Default SSH alias | `ArchDmit` |
| Required static hostname | `arch-dmit` |
| Target work root | `/var/lib/lmm-api-go-deploy/work/<deployment-id>` (root-owned, outside service state) |
| Target backup root | `/var/lib/lmm-api-go-deploy/backups/<deployment-id>` (root-owned, outside service state) |
| Off-host backup root | `/home/arch/.local/state/lmm-api-production-backups/<deployment-id>` on the ArchCzy host through the case-sensitive SSH alias `archczy` |
| Frontend release root | `/srv/lmm-api-frontend` |
| Frontend releases | `/srv/lmm-api-frontend/releases/<version>` |
| Active frontend | `/srv/lmm-api-frontend/current` |
| Backend service | `lmm-api.service` |

The supported phases are `preflight`, `inspect`, `build`, `package`,
`backup`, `watchdog`, `switch`, `confirm`, `rollback`, and `cleanup`. The
default is read-only preflight. Remote mutation requires explicit execution,
verified role/host identity, and current-turn authorization.

The pressure report is a separate read-only observer, not a deployment phase.
Run it on `ArchDmit` with the exact `arch-dmit` hostname check; it does not
install a timer, restart `lmm-api`, clear swap, or remove files. Its report
uses the 20 GiB root / 951 MiB RAM production profile as a visible reference
and includes the actual service cgroup memory and restart counters.

The transaction is marker-owned and persistent. Backups are optional and are
created only with explicit current-turn authorization and `--with-backups`.
The only pre-transaction bootstrap is a non-root `paru` installation of
`lmm-api-deploy-bin`. It is tooling-only: installing it must not touch the
service, database, environment, nginx, Web payload, or active link, and is not
an application switch. Before invoking it, verify `pacman -Qo` ownership for
the command and resolved payload, compare the resolved bytes with the package
payload, and verify `OPERATOR_SHA256`, release-asset hash, package version, and
`REVISION`.

Every application switch stages checksum-verified N and N-1 Go and Web package
pairs, assembles both candidates with non-root `paru`, captures configuration
restore state, and arms a persistent ten-minute watchdog before either switch.
A switch ends in `AWAITING_CONFIRMATION`; observe at least 120 seconds and only
exact Go/Web versions, Git revisions, API/route contract revision, binary,
frontend link, and health/resource checks plus explicit confirmation produce
`CONFIRMED`. Automatic rollback never restores a database.

## Existing database and cutover state

The live service may already use Go with PostgreSQL and dedicated Valkey after a
historical cutover. Inspect the running process environment, active listeners,
`/var/lib/lmm-api-cutover`, and `/var/log/lmm-api-cutover` together. A historical
`SUCCESS_POSTGRES` result is not acceptance when the post-cutover verification
failed or the current `PG_WRITE_BOUNDARY`/journal is absent; stop and reconcile
before any migration, backend selection, or rollback. Never infer Rust business
ownership from PostgreSQL, Valkey, Rust artifacts, or an internal-probe
rehearsal.

For the 2026-08-09 read-only audit, ArchDmit's running service classified as Go
with PostgreSQL and Valkey on port 6380, while Rust slots were inactive and the
business route gate remained Go-owned. This is a time-stamped observation, not
a substitute for the next preflight.

## Rust internal-probe blue/green

| Purpose | Current path |
| --- | --- |
| Immutable releases | `/opt/lmm-api-rs/releases/<revision>` |
| Slot links | `/opt/lmm-api-rs/slots/{blue,green}/current` |
| Configuration | `/etc/lmm-api-rs` |
| Durable incoming artifacts | `/var/lib/lmm-api-rs/artifacts` |
| Deployment audit | `/var/log/lmm-api-rs/deployments/<transaction>` |
| Blue/green ports | `3100`, `3101` |
| Entrypoint | `deploy/backend-rust/deploy-lmm-api-rs.sh` |

This mechanism owns internal probes only, not production business traffic.

## Workspace and backup lifecycle

The controller workspace is a transaction workspace, not durable backup
storage. Keep its marker and terminal status for audit, but remove exact
`artifacts`, `staging`, `tmp`, and cache children after `CONFIRMED`, `ROLLED_BACK`, a
controller-only pre-switch `VALIDATED`, or a verified pre-switch `ABORTED`
state and,
when backups were requested, after their checksum/decryption verification.
Production target workspaces follow the same rule;
the target backup root and off-host root are durable and must not be removed
by workspace cleanup. Private directories are `0700`, manifests/status and
encrypted archives are `0600`, and no secret-bearing plaintext may leave the
target. Never use `/tmp`, `/var/tmp`, an unresolved glob, or a broad root as a
deployment or cleanup target.

On the small production root filesystem, stop new builds at 80% used (90% is
an emergency) and keep at least 4 GiB free before a package/backup
transaction. A terminal workspace older than 24 hours may be pruned oldest
first only after its durable copies and checksums are verified; retain the
active release, latest-known-good snapshot, and any unconfirmed transaction.

## Other retained deployment state

| Component | Backup or state root |
| --- | --- |
| nginx split installer | `/var/lib/lmm-api-nginx-deploy/backups` |
| lmm-api-go edge-policy assets | `/usr/share/lmm-api-go/edge-policy` |
| edge-policy transaction restore | `<deployment>/config-restore/nginx-edge` |
| DB-IP country database | `/var/lib/geoip2/DBIP-Country-Lite.mmdb` |
| fallback nginx installer | `/var/lib/lmm-api-rs-fallback-nginx/backups` |
| dedicated Valkey installer | `/var/lib/valkey-lmm-api-deploy/backups` |
| database cutover | `/var/lib/lmm-api-cutover`, `/var/log/lmm-api-cutover` |

These older mechanisms have independent retention. Do not broaden cleanup to
`/tmp`, backup roots, release roots, application history, or another
deployment's workspace.

The controller state root is
`${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api`; a deployment-side directory
named `states/api.lmm.best` is subject to the same marker and size rules. Keep
only terminal markers/status files plus active operational state. Warn at
256 MiB and stop new builds at 512 MiB or earlier when the filesystem gate is
yellow; measure with `du -sx --bytes` before pruning exact terminal workspaces.

## Temporary-path findings

Future deployment work must redirect task-specific caches, staging, manifests,
and logs into the marker-owned persistent work directory. Never use `/tmp` or
`/var/tmp` for deployment artifacts or cleanup targets, and never perform a
broad temporary-directory deletion.
