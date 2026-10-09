# Standalone systemd deployment

This is the upgrade path for an **existing, standalone Go installation** on
systemd Linux. It does not provision a new server. Package-owned providers are
rejected: use the signed package transaction described in
[frontend and backend upgrades](seamless-upgrades.md) instead.

## Before upgrading

The target requires Python 3.11+, systemd, nginx, the existing
`lmm-api.service`, `/usr/bin/lmm-api -> lmm-api-go`,
`/etc/lmm-api-go/lmm-api-go.env`, and `/srv/lmm-api-frontend/current`.
The local health listener is `127.0.0.1:3000`.
Ubuntu is the first live-validated target for the original granular workflow;
other systemd distributions must meet these prerequisites. The composed
workflow is covered by isolated tests, not a new live-host qualification.

Capture installed/active component identities, native owner status and the real
input file profiles once for the frozen batch; reuse qualified unchanged Web
files and successful local checks. See
[artifact reuse](production-release-transaction.md#prepare-once-reuse-exact-artifacts).
Shared-PostgreSQL hosts additionally require their qualified native owner/
capsule path. The ordinary `scripts/native-shared-pg-deploy.py` wrapper is
same-schema only: it does not authorize migrations or financial maintenance.

Run these commands on the target, from the reviewed repository checkout:

```sh
sudo bash scripts/lmm-api-deploy.sh systemd doctor
sudo bash scripts/lmm-api-deploy.sh systemd status
```

`doctor` checks prerequisites, installation type, service environment metadata,
frontend layout, and unfinished transactions. It does not execute a provider,
read out secrets, install dependencies, or change configuration. A passing
result does not prove schema compatibility or validate candidate artifacts.
`status` without an ID lists transactions; `--release ID` inspects one.
Neither command creates a deployment root, lock, or transaction.

## Upgrade, then confirm

Build or obtain reviewed backend and frontend artifacts and transfer them to a
private directory on the target. Keep their independent component release
versions; `--release` below is a unique **deployment transaction ID**, not a new
shared backend/frontend version. This standalone path does not download or
verify signed release bundles on your behalf. Signed package installations
must not be converted to this path to avoid their verification gates.

```sh
sudo bash scripts/lmm-api-deploy.sh systemd upgrade \
  --release deployment-001 \
  --binary /absolute/path/lmm-api-go \
  --frontend /absolute/path/frontend \
  --confirm api.lmm.best
```

`upgrade` composes the existing `stage` and `apply` phases under one lock.
It verifies the candidate, records artifact hashes, checks the schema and nginx,
retains the previous binary/environment/frontend reference, drains the old
writer and refunds, activates the candidate, and checks its health and version.
It stops at `AWAITING_CONFIRMATION`, not `CONFIRMED`.

Inspect the public site and authenticated flows and observe the service for at
least 120 seconds after readiness. Then explicitly confirm:

```sh
sudo bash scripts/lmm-api-deploy.sh systemd confirm \
  --release deployment-001 --confirm api.lmm.best
```

Confirmation rechecks readiness, the installed binary hash and active frontend
release. Restarting the sole backend interrupts connections; this is **not** a
zero-downtime deployment. The standalone path has no automatic confirmation,
rollback watchdog, or database restoration. Those package-transaction features
must not be assumed to apply here.

## Schema changes

Review each candidate for new tables, migrations and required seed changes
before selecting this path. A previous same-schema upgrade is not evidence
that the next candidate needs no schema change. Use the existing reviewed
maintenance path for shared-PG changes; do not pass `--migrate` through its
ordinary native capsule wrapper.
For a provider with a sealed per-start capsule, also review its restart/recovery
compatibility: additive tables still change the catalog digest. Maintenance does
not automatically rebind the old capsule; follow the
[restart contract review](production-release-transaction.md#go-upgrades-with-an-unchanged-database-schema).

The default requires an already-compatible PostgreSQL schema. For a reviewed,
backward-compatible schema change, add `--migrate` to `upgrade`. First run
`doctor --migrate` to check for `psql`, `pg_dump`, and `pg_restore` as well.
Migration supports the active PostgreSQL schema without a separate log database.

The upgrade verifies backup connectivity before stopping the writer, waits for
clean shutdown and refund completion, saves a custom-format dump, checks its
archive inventory, applies native migrations, and verifies the resulting schema.
Archive validation is not a full restore rehearsal or proof of N-1 compatibility.

`--backup-exclude-table schema.table` is an advanced preparation-only option
for an explicitly reviewed unrelated archive table. It requires `--migrate`;
exact names are recorded in the staged plan and cannot be changed at apply.

## Failure and recovery

Transactions and private logs remain in
`/var/lib/lmm-api-deploy-systemd/ID`. A failed preparation is shown as
`PREPARATION_INCOMPLETE`. Existing IDs are never overwritten or automatically
resumed by `upgrade`; use the printed status and next-action commands.

```sh
sudo bash scripts/lmm-api-deploy.sh systemd status --release deployment-001 --human
# After inspecting the transaction logs and service journal:
sudo bash scripts/lmm-api-deploy.sh systemd rollback \
  --release deployment-001 --confirm api.lmm.best
```

Rollback is allowed for a pending activation, not a confirmed transaction. It
restores the previous binary and frontend, **not the database**. Migrations must
remain compatible with the old binary; destructive migrations or authorization
baseline incompatibilities require a separate, rehearsed database recovery plan.
Do not repeat `upgrade`/`apply` after an ambiguous interruption.

## Automation and granular control

The original `stage`, `apply`, `status`, `confirm`, and `rollback` commands
remain supported. `stage` prepares without stopping the service; `apply` must
repeat the staged `--migrate` choice exactly. `upgrade` carries that choice
through both phases automatically.

`--json` provides machine-readable output and disables progress messages;
`--human` requests a readable summary and next commands. Existing granular
commands keep their JSON output when stdout is not a terminal. New `doctor`,
`upgrade`, and all-transactions `status` default to human output. Errors return
nonzero; interrupted operations return 130 and retain recovery evidence.

The direct equivalent is `python3 scripts/deploy-systemd.py`. Launcher help is
available without an installed or compiled provider:

```sh
bash scripts/lmm-api-deploy.sh --help
```

## Preserve released financial history during ordinary upgrades

A completed financial maintenance episode can leave its capture and bridge
owners `FROZEN`. Those are audit records, not abandoned ordinary deployments.
The ordinary owner accepts them only after an explicit, independently verified
history registration; it never changes their phase or removes their payloads.

Use a reviewed normal owner script and its unchanged guardian module, from a
private directory. First provide private copies of the original financial
controller's final `RELEASED` state and exact all-nodes confirmation receipt:

```sh
python3 -B OWNER register-released-history \
  --release CONFIRMED_POST_OWNER \
  --released-controller RELEASED_STATE --released-controller-sha256 EXACT_SHA \
  --global-confirmation ALL_NODES_RECEIPT --global-confirmation-sha256 EXACT_SHA \
  --confirm api.lmm.best --json
```

The default is a dry run. Review it, then repeat with `--execute`. Registration
requires the installed post provider, `CONFIRMED` and reopened admission, the
controller's exact transition and stopped-handoff bindings, the sealed original
handoffs, capture receipts, and immutable owner-transfer chain. The released
Ubuntu guardian must be absent, the native lease absent, and all three owner
locks independently available. Unknown `FROZEN` records continue to block.

The owner stores proof copies and a receipt under
`/var/lib/lmm-api-deploy-systemd-history/released/POST_OWNER`; it revalidates
original states and transfer evidence before later ordinary activation. It
does not compare the later installed provider with the historical provider:
a legitimate newer release does not invalidate the preserved audit chain.

A different case is a preparation directory with no `state.json`, left before
staging completed. It must not simply be ignored by ordinary upgrades. Review
and archive it with the same owner:

```sh
python3 -B OWNER archive-incomplete --release INCOMPLETE_ID \
  --confirm api.lmm.best --json
# After reviewing the complete inventory:
python3 -B OWNER archive-incomplete --release INCOMPLETE_ID \
  --confirm api.lmm.best --execute --json
```

Only known pre-stage files and regular log/frontend entries are allowed. Any
owner state, mutation marker, unknown entry, unsafe link, running process
reference, native lease, or occupied owner/guardian lock rejects archival.
Reference checks use current/N-1 and active transaction state, rather than
historical descriptive logs or inventories. The complete private copy is
hashed, flushed and read back before the original directory moves to
`/var/lib/lmm-api-deploy-systemd-history/incomplete/ID/original`; the copy,
original logs, modes, fixed provider link, intent and immutable completion
receipt all remain. No phase is fabricated and no file is discarded.

A partial archive or registration still blocks ordinary activation. Do not
remove its intent, rerun blindly, or edit original state to bypass it. Inspect
the preserved copy, original and receipt before an explicit owner recovery.
After completed registration and archival, rerun `doctor --json`, then use the
normal `stage`/`apply` or `upgrade`/`confirm` flow without a maintenance handoff
or `--migrate`. The ordinary schema checks remain `migrate --verify`.
