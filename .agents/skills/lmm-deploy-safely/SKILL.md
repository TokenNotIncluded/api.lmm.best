---
name: lmm-deploy-safely
description: Safely inspect, stage, back up, deploy, update, confirm, roll back, or clean up this LMM repository across the local Arch workstation, isolated test hosts, and production. Use for local installation, signed core/provider or independent web AUR releases, paru updates, systemd service changes, backup verification, release promotion, rollback timers, memory/state-budget checks, or deployment cleanup. Defaults to local-only work and requires explicit current-turn authorization plus exact host and role verification for test or production mutations.
---

# LMM Safe Deployment

Apply one controlled deployment transaction. Do not infer production authority
from an earlier turn, a generic request to “update,” or access to an SSH host.
The tooling-only `lmm-api-deploy-bin` AUR package owns the canonical
`/usr/bin/lmm-api-deploy` operator and its independent signed payload at
`/usr/lib/lmm-api-deploy/lmm-api-go`. Use `lmm-api-deploy deploy ...` for every
deployment phase; verify separately that systemd invokes `/usr/bin/lmm-api
serve`. Do not invoke the application package's CLI, a source-tree helper, a
temporary wrapper, or an invented deploy command.

## Read the deployment map

Read [references/path-map.md](references/path-map.md) before choosing a build,
package, service, frontend, database, or rollback path. Read
[references/safety-contract.md](references/safety-contract.md) before any
mutation, backup, retention, confirmation, rollback, or cleanup operation.

Use the canonical installed CLI transaction and its immutable package payloads.
Backups are optional and operator-managed: do not create, transfer, verify, or
prune them unless the user explicitly requests backups in the current turn.
The transaction always requires a checksum-verified rollback package, captured
frontend/configuration restore state, a persistent ten-minute watchdog armed
before switching, and exact-release confirmation. These rollback artifacts are
transaction state, not business or database backups.

## Classify authority

Choose exactly one role:

- `local`: the shared Arch workstation. This is the default.
- `test`: an isolated non-production host. Require explicit authorization for
  the current turn and verify the exact expected hostname and test marker.
- `production`: `api.lmm.best` on the explicitly approved production host.
  Require explicit authorization for the current turn, the expected SSH alias,
  the static hostname, the service role, and the exact release identity.

Stop on an ambiguous host, role, database engine, current release, dirty or
unexpected repository state, an unverified required checksum, active deployment
lock, or unconfirmed previous deployment. When backups were explicitly enabled,
also stop on a missing or unverified backup copy.

## Create persistent deployment work

Never use `/tmp` or `/var/tmp` for artifacts, caches, package staging, database
dumps, or release state.

Use [scripts/create-workspace.sh](scripts/create-workspace.sh) to create one
marker-owned deployment directory:

- controller default:
  `${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api/deploy-work`
- target default: `/var/lib/lmm-api-go-deploy/work` (root-owned, outside the
  service-writable StateDirectory)

Export the emitted task-specific `TMPDIR`, `GOCACHE`, `GOMODCACHE`,
`CARGO_TARGET_DIR`, and `BUN_INSTALL_CACHE_DIR` values for every build or
package command. Do not reuse another deployment ID's caches or staging.

## Strict local acceptance preview

For this repository's local acceptance preview, SQLite is unconditionally
forbidden, including fallback, autodetection, and default behavior. Require a
fresh marker-owned local PostgreSQL database and role plus a fresh isolated
marker-owned Valkey instance. Start both with zero production or business data;
never use production DSNs, snapshots, or cache dumps. Bind them to loopback or
Unix sockets only, do not reuse an existing database, schema, role, cache
namespace, or port, and verify each identity before startup. Missing or
ambiguous PostgreSQL/Valkey configuration, an unexpected engine, a nonempty
target, or an unverifiable identity is a `STOP` condition. This strict rule
overrides the generic engine-detection guidance below for local acceptance
previews.

## Observe local host health proportionally

Before heavy local validation or preview work, observe host health on a
best-effort, proportional basis. Treat PSI, blocked tasks, swap use or churn,
available RAM, load, zram, and similar metrics as context only: never impose a
fixed numerical readiness threshold, and never stop solely because one metric
is high.

Serialize heavy work, start only the minimum required components, and monitor
the responsiveness of required commands and services while proceeding on a
degraded-but-functional machine. Stop only for concrete imminent or actual
failure: relevant recent OOM-killer or kernel storage errors, insufficient disk
for known artifacts, a required-port bind failure after verifying ownership and
conflicts, ambiguous PostgreSQL or Valkey identity, an uncontrolled deployment,
or sustained resource exhaustion that actually makes a required command or
service fail or become unresponsive.

Report the evidence and preserve partial state. Do not clear swap or caches, or
kill user applications, merely to satisfy a metric. This policy does not weaken
the SQLite prohibition; fresh marker-owned PostgreSQL and Valkey requirements;
production backup, rollback, and identity controls; workspace ownership; or
heavy-work serialization.

## Production resource safety lines and performance cadence

The production host is a small 20 GiB root filesystem with a sub-1 GiB RAM
budget. Treat these lines as release gates, not as targets to approach:

| Signal | Green | Warning / action | Stop or emergency |
| --- | --- | --- | --- |
| root filesystem used | `<70%` | `70-80%`: no new heavy build until the workspace is pruned and headroom is rechecked | `>=80%`: stop builds/releases; `>=90%` or any write failure: incident cleanup first |
| root inode used | `<70%` | `70-80%`: investigate generated-file growth | `>=80%`: stop builds/releases; `>=90%`: emergency cleanup |
| `MemAvailable` | `>=30%` of RAM | `20-30%` for 5 minutes: serialize work and reduce concurrency | `<20%`, OOM, or sustained reclaim/thrash: stop mutation |
| swap used | `<10%` | `10-25%`: no additional heavy work without a fresh check | `>25%` or swap-in/out churn with latency: stop mutation |
| CPU / load | `<70%` for 5 minutes | `70-85%`: one heavy job only | `>85%` for 5 minutes, `>95%` for 2 minutes, or required requests timing out: stop mutation |
| service memory cgroup | below `MemoryHigh=320M` | at/above high for 5 minutes: investigate | `MemoryMax=384M`, repeated restart, or OOM: rollback/incident path |

The filesystem line is absolute: a successful build is not safe when it
leaves less free space than the largest candidate package plus three backup
copies and 1 GiB of operating headroom. On this host, keep at least 4 GiB
free before starting a production package/backup transaction. A yellow metric
may be observed and reported, but a red metric blocks the mutation.

Run a read-only baseline before every production mutation and again at least
every 30 seconds during a build or backup transfer:

```bash
df -h /; df -i /
free -h
vmstat 1 5
systemctl show lmm-api.service -p ActiveState -p SubState -p MainPID \
  -p NRestarts -p MemoryCurrent -p MemoryHigh -p MemoryMax -p MemorySwapMax
pg_isready
/usr/bin/lmm-api-deploy request --base-url http://127.0.0.1:3000 \
  --path /api/status --show-status --timeout 10s
/usr/bin/lmm-api-deploy request --base-url http://127.0.0.1:3000 \
  --path /api/livez --show-status --timeout 10s
journalctl --no-pager -u lmm-api.service -u nginx.service \
  --since '5 minutes ago' -p err..alert
```

After a switch, repeat native CLI status/livez probes three times, verify
`NRestarts` is unchanged, check PostgreSQL and Valkey readiness, inspect the
error journal, and record disk/RAM/swap before confirming. Continue a
read-only check at least every 15 minutes while a release is in its watchdog
window, and at least hourly during normal operation. A failed check is an
incident signal; do not hide it by clearing journals or restarting blindly.

### Read-only ArchDmit pressure report

For a repeatable, low-overhead point-in-time report, run
[scripts/resource-pressure-report.sh](scripts/resource-pressure-report.sh) on
the verified production host. It is intentionally not a service, timer, or
repair command: it only reads `/proc`, `df`, `systemctl show`, and HTTP health
responses. It reports `MemAvailable`, swap total/used/percentage and sampled
change (`pswpin`/`pswpout` deltas), root filesystem total/used/free space,
`lmm-api.service` memory/restart counters, and `/api/status` plus `/api/livez`.

The report uses the ArchDmit profile of a 20 GiB root filesystem and 951 MiB
RAM as explicit reference values, while separately applying the resource
gates above. A profile-size mismatch is reported as a warning; the command
never changes the host to make a metric pass. It exits `0` for green, `1` for
warning, `3` for an expected-host mismatch, and `4` for stop-level pressure or
a failed service/health gate. Invalid input or an unavailable required local
probe exits `2`.

Run it without copying files to production:

```bash
ssh ArchDmit 'bash -s -- --expected-host arch-dmit --format kv' \
  < .agents/skills/lmm-deploy-safely/scripts/resource-pressure-report.sh
```

Use `--format json` for machine collection, or adjust the read-only sampling
with `--samples 1..5` and `--interval 0..60`. Do not add a systemd timer for
this entry point. Its offline fixture seam is `--proc-root`; the regression
test is:

```bash
bash .agents/skills/lmm-deploy-safely/scripts/tests/test-resource-pressure-report.sh
```

The production package also owns the regional edge policy. Its Nginx templates
and Go-rendered access error page are installed from
`/usr/share/lmm-api-go/edge-policy` by the native transaction; do not edit
`/etc/nginx/site-policy`, install a second GeoIP shell hook, or keep the old
APNIC prefix units. Use `lmm-api-deploy deploy production edge-policy verify`
after an activation. The monthly DB-IP update is `lmm-api-deploy geoip update` via the
package-owned `geoip2-country-update.timer`.

## Inspect without exposing secrets

Run [scripts/inspect-state.sh](scripts/inspect-state.sh) before planning a
mutation. It reports sanitized key/value or JSON state and never sources an
environment file or prints a DSN.

For PostgreSQL, inspection also validates the canonical lowercase
`pg-write-boundary`, `cutover-journal`, and `post-cutover-verify.json` records.
Their transaction/schema identities must agree, the journal must be complete,
and the verification must attest PostgreSQL plus the historical migration.
Anything other than `cutover_state=verified` is a production stop condition.

Reconcile the reported database engine with the chosen deployer and backup
method. Fail closed when SQLite, PostgreSQL, MySQL, or configuration evidence
disagrees. Do not select an engine from stale prose documentation.

### Prove the installed CLI before invoking it

Production may still have an older provider binary at `/usr/bin/lmm-api`. Do
not assume that `status`, `deploy`, or even `--help` is read-only: a legacy
binary can interpret an unknown subcommand as a request to start the backend.
Before invoking any subcommand, inspect the package owner and version, resolved
path, systemd `ExecStart`, protocol/revision, and current service PID. The
canonical service must execute `/usr/bin/lmm-api serve`. The canonical operator
must be owned by `lmm-api-deploy-bin`, resolve to
`/usr/lib/lmm-api-deploy/lmm-api-go`, match those bytes and package-owned
`OPERATOR_SHA256`, match `RELEASE_ASSET_SHA256` and `REVISION`, and expose the
`deploy production` transaction.

If the target lacks that protocol, classify it as pre-transaction legacy. Use
`systemctl show`, `readlink`, `pacman -Qo`, running-process identity, sanitized
`/proc/<MainPID>/environ` scheme classification, and explicit HTTP probes for
read-only inspection. Do not run ambiguous `lmm-api`, `lmm-api-go`, `status`,
`deploy`, `--help`, or no-argument invocations. The only permitted bootstrap is
a non-root `paru` installation of tooling-only `lmm-api-deploy-bin`; it is not
an application switch and must leave service, database, environment, nginx,
Web payload, and frontend link unchanged. If a bad probe starts a process or
creates a local database file, preserve the evidence, verify production stayed
unchanged, and do not delete it without exact ownership and scope confirmation.

### Distinguish database runtime from historical cutover prose

The live service can already run Go against PostgreSQL and the dedicated
Valkey even when older documentation describes Go/SQLite. The process
environment, service identity, active database/cache listeners, and durable
cutover journal are the evidence hierarchy. A PostgreSQL runtime without a
current verified `PG_WRITE_BOUNDARY`, or with a failed post-cutover result, is
an unverified state: stop before migration, backend selection, or rollback and
reconcile it through the cutover coordinator. Never infer Rust readiness from
the presence of PostgreSQL, Valkey, Rust artifacts, or an old rehearsal.

## Build once and promote identical bytes

Build and validate once in the controller workspace. Record the Git commit,
release ID, package identity, and SHA-256 in the deployment manifest. Promote
the identical checksum-verified artifact to test and production. Never rebuild
on the target and never substitute a later artifact under the same release ID.

Use the repository's existing package and release mechanisms only for their
documented roles. AUR or paru work must preserve the independent
`lmm-api-go-bin` and `lmm-api-web-bin` packages and run their packaging checks.
Do not publish or deploy Rust unless the current task explicitly includes it
and its separate ownership gates pass.

Treat a dirty or changing worktree as a release blocker. Freeze the exact
revision and build manifest before creating the core package, Rust provider,
migrator, or frontend archive; do not promote a dist directory or binary that
was built from an unrecorded working-tree state.

The Rust blue/green mechanism owns internal liveness/readiness/build probes
only. It is not business-route ownership. Before a backend transaction is
allowed to target `rs`, the migration gate must validate in both source and
activation modes, every production route must be independently verified and
approved for Rust, and PostgreSQL, Valkey, authentication, quota, billing,
streaming, and drain/reconnect evidence must be current. A mounted candidate,
an active slot link, or a successful `/readyz` probe is insufficient.

## Default production update: split `-bin` AUR packages and `paru`

Install the deployment operator independently, then publish the production
frontend and Go backend as two independently versioned application packages:

- `lmm-api-deploy-bin` owns only `/usr/bin/lmm-api-deploy`, its independent
  `/usr/lib/lmm-api-deploy/lmm-api-go` bytes, licenses, revision, hash, and
  optional contract metadata. Installing it is tooling bootstrap, not a switch.
- `lmm-api-go-bin` owns only the Go backend, service, environment, package-owned
  memory drop-in, and backend policy assets. New releases must not contain or
  own `frontend-dist`.
- `lmm-api-web-bin` owns only the immutable production frontend payload and its
  package-owned atomic activation hook.

Do not make a frontend-only release reinstall or restart the Go backend. Do not
make a backend-only release replace the active frontend. `paru` may assemble a
verified prebuilt `-bin` archive on production, but it must never compile the
application or substitute another artifact. Do not deploy Rust or make a
Go/web release wait for Rust unless the user explicitly requests that cutover.

Apply this sequence:

1. Freeze a clean `main` revision that equals `origin/main`; run the relevant
   Go, Web, route-contract, and all three AUR package checks.
2. If the target lacks the canonical operator, publish and install only the
   exact tooling-only `lmm-api-deploy-bin` with non-root `paru`. Before and
   after bootstrap prove service PID/restarts, database/cache, Web link and
   bytes are unchanged. Verify `pacman -Qo` ownership of command and resolved
   payload, byte equality, `OPERATOR_SHA256`, `RELEASE_ASSET_SHA256`, package
   version, Git `REVISION`, Sigstore identity, and contract metadata.
3. Publish immutable, signed GitHub release assets for Go and Web separately.
   Go is Go-only; both assets record their Git revision and the SHA-256 revision
   generated from `deploy/production/API_ROUTE_CONTRACT`. Independent versions
   may differ only when the compatibility gate proves that pair is supported.
4. Update the separate AUR repositories for every changed artifact:
   `lmm-api-go-bin` and `lmm-api-web-bin` (and `lmm-api-deploy-bin` only when
   operator bytes change). Pin exact versions, URLs, checksums, Git identity and
   Sigstore workflow; regenerate `.SRCINFO`; run `packaging/aur/test-matrix.sh`
   and clean `makepkg`; then read published metadata back.
5. On verified `arch-dmit`, run `paru` as the established unprivileged OS
   account to assemble exact candidate Go/Web `-bin` packages and exact N-1
   rollback Go/Web packages. Record both package SHA-256 values, Git and
   contract revisions, binary identity, and frontend link for N and N-1. Never
   run `paru` as root or substitute source, `-git`, or Rust packages.
6. Stage all four verified packages, arm the persistent 600-second watchdog
   with both package/link identities, stop Go, then run the candidate binary as
   a root transient unit with `/etc/lmm-api-go/lmm-api-go.env`: `migrate
   --apply`, then `migrate --verify`. Do not use the service's `DynamicUser`.
   Migrations must remain N/N-1 compatible for the entire confirmation window.
7. Only after both migration phases pass, install the split candidate packages
   through `paru -U`, apply exact package memory limits, and start Go. Activate
   Web after local backend health succeeds. Public probes remain required.
8. Verify compatibility again, observe at least 120 seconds with the watchdog
   armed, and confirm only exact Go/Web package hashes and versions, Git and
   contract revisions, frontend link, binary, unchanged restart count,
   database/cache readiness, journals, three status/livez probes, and exact
   memory limits. Retain terminal marker/status and remove only exact disposable
   staging/cache children.

The `paru` path does not weaken the rollback/watchdog or exact-release checks.
The two packages are live, but `paru` alone is not a deployment transaction.
The migration gate, watchdog, exact-release confirmation and rollback package
remain mandatory around every production activation.

Before creating a tag or touching AUR, reconcile the release workflow tag
pattern and artifact names with the `_release_tag`/source URLs in every changed
`PKGBUILD`, tracked `.SRCINFO`, `packaging/aur/README.md`, and the Sigstore
workflow identity from the same frozen commit. A `vX.Y.Z` workflow cannot
satisfy a `web-vX.Y.Z` source (or the reverse) without an explicit matching
asset; treat that mismatch as a hard `STOP` rather than inventing a tag,
reusing an old archive, or bypassing verification.

## Bound deployment state and build memory

Treat `${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api` and any deployment-side
alias such as `states/api.lmm.best` as bounded operational state, not a dump for
artifacts or application history. Before a build and after every terminal
cleanup, measure it with `du -sx --bytes`; warn at `256 MiB` and stop new builds
at `512 MiB` (or earlier when the filesystem gate is yellow). Keep only the
marker and final status in terminal deployment workspaces. Remove disposable
`artifacts`, `staging`, `tmp`, Bun/Go/Cargo caches, `node_modules`, `dist`, and
package archives by exact marker-owned path after `CONFIRMED`, `ROLLED_BACK`,
`VALIDATED`, or `ABORTED`.

Never delete conversation/history state, PostgreSQL/Valkey data, active release
trees, backups, or another deployment's workspace to satisfy this budget. If a
state directory is larger than expected, report its top-level owners and stop;
do not use `rm -rf`, a wildcard, or a broad `/tmp` cleanup. Keep one heavy build
active, cap Go/Bun/Cargo concurrency, and re-run the memory/resource baseline
before resuming.

## Keep backups optional

Do not include backup work in a deployment unless the user explicitly requests
it. Use `deploy production release --with-backups` only for that opt-in path.
When requested, create and verify the role-specific copies described in the
safety contract:

- local: controller copy;
- test: target rollback snapshot and controller copy;
- production: target rollback snapshot, controller copy under
  `$HOME/backup/lmm-api/<verified-host>/<deployment-id>`, and a verified
  off-host archive on the ArchCzy host through the case-sensitive SSH alias
  `archczy`, or explicitly configured object storage.

Run [scripts/verify-backup-set.sh](scripts/verify-backup-set.sh) for an opted-in
backup set. Encrypt every archive that can contain secrets before it leaves the
target. Never print archive contents or secret values.

## Canonical backup and workspace layout

Use these exact, marker-owned paths; do not invent a per-run path under `/tmp`:

| Scope | Canonical path | Retention rule |
| --- | --- | --- |
| controller build/workspace | `${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api/deploy-work/<deployment-id>` | keep the marker and final status; remove `staging`, `tmp`, and caches after terminal state |
| controller durable backup | `$HOME/backup/lmm-api/<verified-host>/<deployment-id>` | encrypted controller copy, `manifest.env`, `SHA256SUMS`; never delete active/latest-known-good |
| production target workspace | `/var/lib/lmm-api-go-deploy/work/<deployment-id>` | root-only, outside the service-writable StateDirectory; keep only marker/status after confirmation |
| production target backup | `/var/lib/lmm-api-go-deploy/backups/<deployment-id>` | root-only, checksum-verified target snapshot; retain the configured latest-known-good set |
| off-host backup | `/home/arch/.local/state/lmm-api-production-backups/<deployment-id>` on the ArchCzy host (SSH alias `archczy`) | encrypted controller/off-host archives; verify checksum after transfer |

The controller workspace is not a backup. When backups were requested, prove
the durable controller, target, and off-host copies exist and decrypt
verification passes before removing them. Production
cleanup may remove only the exact `<deployment-id>/staging`, `<deployment-id>/tmp`,
and cache paths; it must not remove a backup root, release root, active
workspace, transaction lock, or unresolved glob. Use mode `0700` for private
directories and `0600` for manifests, status, encrypted archives, and identity
files. Keep package bytes in the release package store or durable backup, not
in a long-lived staging tree.

If the root filesystem reaches the warning line, stop creating new workspaces,
measure each candidate directory, and prune only terminal, inactive LMM
workspaces after checksum verification. If it reaches the stop line, repair
storage before touching application state. Never “solve” a full disk with
`rm -rf /tmp`, a wildcard over `/var`, journal deletion, or deletion of the
database/cache.

## Production mutation checklist

1. Record current UTC time, Git revision, remote `origin/main`, installed
   package/version, service PID, backend, PostgreSQL/Valkey identity, and the
   resource baseline. Verify SSH alias `ArchDmit` resolves to `arch-dmit`.
   Verify `archczy` only when backups were explicitly requested.
2. Require a clean source checkout whose HEAD equals `origin/main`. Create one
   marker-owned controller workspace and export all build caches into it.
3. Build once, record artifact/package/frontend SHA-256, and run Go/frontend
   tests plus the native CLI preflight. Do not build on the production host.
4. If backups were explicitly requested, create target, controller, and
   off-host copies, verify encrypted archives offline, and compare checksums.
5. Arm the persistent 600-second rollback watchdog, apply the immutable package,
   run migrations only when N/N-1 compatible, and observe for at least 120
   seconds while checking the resource and error-journal gates.
6. Confirm only the exact deployment ID after three successful native CLI
   status/livez checks, stable service restart count, clean DB/cache readiness,
   and acceptable resource headroom. Confirming disables the watchdog; if any
   gate fails, leave it armed and use the scoped rollback path.
7. After a terminal state, remove remote/controller staging and caches by exact
   path, retain status and any requested backups, and re-run resource probes.
   Record what was removed and the remaining free-space margin.

## Arm rollback before switching

Production and production-like test switches require a persistent systemd
watchdog armed before the application or frontend switch. The deadline is ten
minutes. A successful switch ends in `AWAITING_CONFIRMATION`, not `DEPLOYED`.

The watchdog restores only the verified application package, frontend release,
and configuration snapshot. It never restores the database automatically.
Block migrations unless both the new and previous application releases remain
compatible with the database for the whole confirmation window.

Manual confirmation must re-verify the exact backend artifact, frontend
symlink, service state, and health identity before disabling the timer. A
generic healthy response is insufficient.

## Retain and clean safely

For explicitly requested backups, default retention is five target snapshots,
ten controller copies, and thirty off-host copies. Never remove the active
release, an unconfirmed deployment, or the latest known-good snapshot.

Use [scripts/cleanup-owned-workspace.sh](scripts/cleanup-owned-workspace.sh)
only for the exact marker-owned deployment directory after a durable
`CONFIRMED`, `ROLLED_BACK`, controller-only pre-switch `VALIDATED`, or
pre-switch `ABORTED` state. `VALIDATED` records completed pre-release checks;
`ABORTED` records an interrupted attempt. Both require that no application or
frontend switch occurred and all workspace-owned processes have stopped.
Preview first. Never clean a broad temp
directory, backup root, release root, unresolved variable, glob, symlink, or
another deployment's workspace.

For production, a terminal workspace must not retain release artifacts,
package staging, or build caches: remove only its exact `artifacts`, `staging`,
`tmp`, and cache children after the
durable terminal status and, when requested, backup verification. Retain the marker and
status as a small audit record. A scheduled cleanup may prune only terminal,
inactive workspaces older than 24 hours, oldest first, and must stop before the
filesystem crosses the 70% warning line. If the host is already above that
line, run a measured, exact-path cleanup and recheck `df`, inodes, RAM, swap,
service state, and database readiness before any other production operation.

## Report the outcome

Report the role and verified host, release and artifact SHA-256, backup copy
locations and verification result, service/frontend health identity, rollback
timer and deadline, confirmation state, cleanup performed, and every skipped or
failed gate. Never imply production completion while confirmation is pending.
