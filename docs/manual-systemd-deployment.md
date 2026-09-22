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
