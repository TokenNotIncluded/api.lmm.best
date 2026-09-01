---
name: lmm-deploy-safely
description: Safely inspect, stage, back up, deploy, update, confirm, manually roll back, or clean up this LMM repository across the local Arch workstation, isolated test hosts, and production. Use for provider CLI releases, the lmm-api provider symlink, independent Web/AUR releases, paru updates, systemd service changes, optional backup verification, production promotion, manual rollback, resource/state budgets, or deployment cleanup. Defaults to local-only and requires explicit current-turn authorization plus exact host/role verification for test or production mutation.
---

# LMM Safe Deployment

Apply one controlled transaction. Read
[references/path-map.md](references/path-map.md) and
[references/safety-contract.md](references/safety-contract.md) before mutation,
backup, retention, confirmation, rollback, or cleanup.

The provider contract is exact:

```text
/usr/bin/lmm-api-go   # real Go provider
/usr/bin/lmm-api-rs   # real Rust provider
/usr/bin/lmm-api      # one-hop relative symlink to one provider
```

Systemd and every operator/deployment action invoke `/usr/bin/lmm-api`. Never
invoke a provider filename directly as a deployment command, publish a regular
provider payload at `/usr/bin/lmm-api`, create a reverse alias, or delegate to a
source-tree/shell deployment helper. A release-scoped candidate is invoked
through a strictly validated workspace symlink named `lmm-api`.

The root `deploy/` directory is retired. Both provider CLIs implement the shared
command/state contract; immutable assets live under `packaging/`.

## Authority

Choose one role:

- `local`: shared Arch workstation; default.
- `test`: isolated non-production host; require current-turn authorization and
  exact hostname/test marker.
- `production`: `api.lmm.best` on the explicitly approved host; require
  current-turn authorization, expected SSH alias, static hostname, service
  role, and exact release identities.

A deployment request does not by itself authorize Git repair, branch changes,
commits, pushes, tags/releases, AUR publication, backups, or Rust route ownership.
Obtain explicit authorization for each class. Stop on ambiguous authority or
host identity.

## Prove the public CLI before invocation

A historical Go `0.1.x` package may expose a real `/usr/bin/lmm-api` and reverse
`lmm-api-go -> lmm-api` alias. Unknown commands on that binary may start the
server, so do not probe it with `status`, `deploy`, `--help`, or no arguments.

Inspect first with `systemctl show`, `lstat`, `readlink`, `realpath`,
`pacman -Qo`, package version/integrity, process executable, sanitized live
environment key names, and direct HTTP health probes. Invoke the public CLI only
after proving:

- `/usr/bin/lmm-api` is a one-hop relative link to exactly `lmm-api-go` or
  `lmm-api-rs`;
- the target is a root-owned, executable, non-writable real file from an approved
  package;
- package bytes, signed release metadata, SHA-256, Git revision, route contract,
  and expected command protocol agree;
- `lmm-api.service` executes `/usr/bin/lmm-api serve` and its running process
  resolves to the selected provider.

A verified legacy layout is N-1 migration/rollback evidence only. Use the
signed candidate through a workspace symlink named `lmm-api`; never improvise a
wrapper or direct provider invocation.

## Persistent workspaces

Never use `/tmp` or `/var/tmp` for deployment artifacts, caches, packages,
database dumps, or transaction state.

Create one marker-owned workspace with
[scripts/create-workspace.sh](scripts/create-workspace.sh):

- controller root:
  `${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api/deploy-work`;
- production target root: `/var/lib/lmm-api-go-deploy/work` until a separately
  rehearsed state-root migration.

Deployment IDs match `[A-Za-z0-9][A-Za-z0-9._-]{0,127}`. Export the emitted
`TMPDIR`, `GOCACHE`, `GOMODCACHE`, `CARGO_TARGET_DIR`, and
`BUN_INSTALL_CACHE_DIR`; do not reuse another transaction's caches. Private
directories are `0700`; status/manifests/configuration evidence are `0600`.

## Build once and publish exact bytes

Use one frozen clean revision equal to `origin/main`. Build once in the
controller workspace, record provider/Web package versions, release IDs, Git
revision, route contract revision, and SHA-256, and promote identical bytes.
Never rebuild on production or overwrite immutable release bytes.

Reconcile tag patterns, artifact/provider filenames, `PKGBUILD`/`.SRCINFO`
URLs and hashes, Sigstore workflow identity, package metadata, and
`packaging/aur/README.md` before publication. Go assets contain `lmm-api-go`;
Rust assets contain `lmm-api-rs`; neither new provider package owns a generic
`lmm-api` file or reverse alias.

Production `paru` runs as the established unprivileged account and assembles
only exact pinned packages. Validate package archive headers and `.MTREE` for
root ownership, safe modes/types, signed-member parity, critical files, and the
provider-link contract. Re-hash packages and candidate symlink targets before
every dispatch.

Do not make a Web-only update reinstall/restart the backend. Do not make a
backend-only update replace Web. Installing Rust does not transfer business
route ownership.

## Strict local acceptance

Local acceptance forbids SQLite fallback/autodetection. Use a fresh marker-owned
PostgreSQL database/role and fresh isolated Valkey instance containing no
production or business data. Bind to loopback or Unix sockets, use unique
schema/role/namespace/ports, and verify identities. Ambiguity, reuse, nonempty
targets, or inability to prove identity is a stop.

## Database and cache evidence

Inspect configuration without sourcing it and without printing values. Never
put DSNs, credentials, tokens, or private configuration in logs, manifests,
process titles, or off-host plaintext.

Live process environment and listeners outrank historical prose. Prove the
active PostgreSQL authority, schema/write boundary, dedicated Valkey identity,
and N/N-1 migration compatibility before backend mutation. Stop on conflicting
engines, keys, listeners, journals, or failed post-cutover evidence.

Rust accepts provider-neutral production keys and documented Go aliases without
logging values. Conflicting aliases fail closed.

## Production resource gates

ArchDmit has a 20 GiB root filesystem and sub-1 GiB RAM. Before mutation,
during transfers, during observation, and before confirmation collect:

```bash
df -h /; df -i /
free -h
vmstat 1 5
systemctl show lmm-api.service -p ActiveState -p SubState -p MainPID \
  -p NRestarts -p MemoryCurrent -p MemoryHigh -p MemoryMax -p MemorySwapMax
pg_isready
curl --silent --show-error --output /dev/null --write-out '%{http_code}\n' \
  --max-time 10 http://127.0.0.1:3000/api/status
curl --silent --show-error --output /dev/null --write-out '%{http_code}\n' \
  --max-time 10 http://127.0.0.1:3000/api/livez
journalctl --no-pager -u lmm-api.service -u nginx.service \
  --since '5 minutes ago' -p err..alert
```

| Signal | Green | Warning/action | Stop/emergency |
| --- | --- | --- | --- |
| root/inodes | `<70%` | `70-80%`: serialize and prune only terminal state | `>=80%`; `>=90%` incident |
| `MemAvailable` | `>=30%` | `20-30%`: reduce concurrency | `<20%`, OOM, or thrash |
| swap | `<10%` | `10-25%`: no additional heavy work | `>25%` with churn |
| CPU | `<70%` | `70-85%`: one heavy job | `>85%` for 5m or request timeout |
| service memory | below `MemoryHigh=320M` | sustained high: investigate | `MemoryMax=384M`, restart, or OOM |

Keep at least 4 GiB free before production package/backup work. Do not clear
swap, journals, caches, or databases to make a metric green; do not kill
unrelated processes or hide failed gates with blind restarts.

For a read-only report run, without copying files:

```bash
ssh ArchDmit 'bash -s -- --expected-host arch-dmit --format kv' \
  < .agents/skills/lmm-deploy-safely/scripts/resource-pressure-report.sh
```

Run [scripts/inspect-state.sh](scripts/inspect-state.sh) before planning. It
reports sanitized state and never sources environment files or prints DSNs.

## Optional backups

Backups are optional and require explicit current-turn authorization. When
selected, require:

| Role | Verified copies |
| --- | --- |
| local | controller |
| test | target, controller |
| production | target, controller, off-host |

Canonical production roots:

- target: `/var/lib/lmm-api-go-deploy/backups/<deployment-id>`;
- controller: `$HOME/backup/lmm-api/<verified-host>/<deployment-id>`;
- off-host: `/home/arch/.local/state/lmm-api-production-backups/<deployment-id>`
  on verified SSH alias `archczy` / hostname `archczy`.

Use [scripts/verify-backup-set.sh](scripts/verify-backup-set.sh). Encrypt every
secret-bearing archive before it leaves the target; checksums cover transferred
encrypted bytes. Never print contents. Never delete active/unconfirmed or
latest-known-good copies.

## Manual rollback only

Do not create or arm a systemd rollback timer/service. Do not automatically
rollback on activation/observation failure, cancellation, exit, disconnect, or
reboot.

Before the first live mutation, persist and fsync:

- immutable N/N-1 provider, package, link, frontend, configuration, schema, and
  optional backup evidence;
- exact hashes and package/release identities;
- a rollback-eligible state while holding the transaction lock.

State semantics:

- pre-mutation failure: `FAILED_PREARM`, release lock;
- post-boundary failure: `ROLLBACK_REQUIRED`, retain lock/evidence;
- healthy switch: observe at least 120 seconds, then
  `AWAITING_CONFIRMATION`;
- failed rollback: remain retryable with evidence and lock;
- only explicit exact-ID `confirm` or `rollback` is terminal.

Use only:

```text
/usr/bin/lmm-api deploy production confirm ...
/usr/bin/lmm-api deploy production rollback ...
```

Confirmation re-verifies completed observation, provider link/target/package/
hash, backend process, frontend link, service restart baseline, PostgreSQL,
Valkey, health canaries, journals, memory limits, and immutable evidence before
writing `CONFIRMED` and releasing the lock.

Rollback re-verifies untampered N-1 evidence, restores only approved provider
package/link, frontend link, and configuration snapshot, verifies identity and
health, writes `ROLLED_BACK`, then releases the lock. Never restore a database
automatically.

## Production sequence

1. Record UTC time, exact Git/origin revision, host/role, installed provider and
   Web identities, service PID/restarts, database/cache identity, provider link,
   and resource baseline.
2. Create one marker-owned controller workspace. Build/test identical bytes;
   no production build.
3. Publish signed provider/Web assets and exact pinned AUR metadata only after
   explicit Git/release/AUR authorization; read back and verify publication.
4. Assemble exact candidate and N-1 packages as non-root. Verify package,
   release, route-contract, provider filename/link, and artifact hashes.
5. If authorized, create and verify role-specific backup copies.
6. Persist manual rollback evidence/lock before mutation. Persist transient-unit
   identity and attempt count before remote dispatch. On transport ambiguity,
   reconcile unit + manifest + status; redispatch once only on three-way absence.
7. Invoke the candidate through a verified workspace symlink named `lmm-api`;
   run `migrate --apply` then `migrate --verify` when required.
8. Install exact packages, atomically establish/verify `/usr/bin/lmm-api` link,
   start service, activate Web, and run local/public/authenticated canaries.
9. Observe at least 120 seconds. End in `AWAITING_CONFIRMATION`; explicitly
   confirm or roll back. Never report deployment complete while pending.
10. After terminal state, clean exact disposable workspace children, retain
    marker/status and authorized backups, and repeat resource/health evidence.

## Rust ownership gate

`apps/api-rust/tests/fixtures/routes/route-gate.tsv` is authoritative. Rust
CLI parity, package installation, provider selection, mounted routes, health
probes, or historical rehearsals do not transfer business ownership.

Before selecting Rust for production business traffic require explicit current
authorization plus independent route, auth, quota, billing, streaming, error,
PostgreSQL/Valkey, singleton-job, SSE/WebSocket drain/reconnect, N/N-1, and
manual rollback evidence. Any unresolved or unapproved row fails closed.

## Bounded state and cleanup

Measure `${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api` before builds and after
cleanup. Warn at 256 MiB; stop new builds at 512 MiB or earlier when storage is
yellow. A large unexplained owner is a stop, not permission for broad deletion.

Use [scripts/cleanup-owned-workspace.sh](scripts/cleanup-owned-workspace.sh) only
for an exact marker-owned workspace in `CONFIRMED`, `ROLLED_BACK`, controller-
only `VALIDATED`, or verified pre-switch `ABORTED`. Preview first. Remove only
exact `staging`, `tmp`, dependency/build caches, and package archives. Retain
marker/status and durable release/rollback/backup evidence.

Reject roots, home, `/tmp`, `/var/tmp`, backup/release roots, active/nonterminal
workspaces, locks, unresolved variables, globs, tildes, symlinks, and other
transactions. Re-run disk/inode/RAM/swap, service, PostgreSQL/Valkey, provider
link, and health checks after cleanup.

## Report

Report role/host, provider symlink and package identities, release versions and
hashes, optional backup locations/results, service/frontend health, observation,
manual confirmation/rollback state, cleanup, and every failed/skipped gate.
Never imply completion while `AWAITING_CONFIRMATION` or
`ROLLBACK_REQUIRED` remains.
