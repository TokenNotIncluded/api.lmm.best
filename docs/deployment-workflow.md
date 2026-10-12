# Deploy with scripts for the actual hosts

Every update and database migration requires a temporary shell script written
and checked against the current host state. This is the deployment method; do
not rebuild a generic Go deployment CLI or controller. The
repository deployment CLI and its build/install entry points have been removed.
Go and Web remain separate components; a code edit, signed release and live
deployment are separate outcomes.

## Read the live state first

Freeze one source revision and candidate artifact set. Inspect the active service,
provider and frontend, package ownership, shared database/schema, deployment locks,
maintenance owner and existing receipts. Continue interrupted work from its actual
state. Removing source must not stop its process, delete private state or trigger
a rollback of the interrupted batch. Do not roll back, kill an owner or remove state merely to start a new batch.

Reuse successful source-matched local checks and exact signed release bytes.
`scripts/local-release-tests.py` records local release evidence. Official release
workflows build, sign and publish; publication does not update a server.

## Write the deployment script

Check shell syntax and applicable local fixtures before executing. Use explicit
paths, artifact hashes, expected current identities and reviewed
phase boundaries for the selected hosts. A package-owned host must retain package
ownership; a standalone systemd host must retain its service and environment
contracts. There is no generic deployment command to choose instead.

Verify release signatures, checksums and source identity before installing. Keep
host-key verification, file/database locks, credential protection, encrypted
backup and actual independent restore validation before migrations, old-data
integrity,
schema compatibility, financial drains, exclusive writers and startup seals.
Changes to data or schema require the specific reviewed maintenance procedure.
Preserve runtime financial, writer and startup safety commands in the API binary.

Stage, mutate and inspect sequentially, recording each exact result. After an
uncertain write, reconcile before proceeding; do not rerun the entire script.
Unchanged components need no rebuild, republication or restart. Frontend package
installation only installs files; activation and retention are explicit script
steps. Verify referenced assets before atomically changing the active frontend.

## Verify and finish

Confirm the exact installed and running backend, service generation, frontend
release, origin routing and public version/assets. Complete any existing owner
confirmation and financial/writer integrity checks before declaring success.
Health alone does not prove deployment. Report versions and receipts, actual
checks and any remaining incomplete phase separately.

## Historical recovery material

The [native transaction guide](production-release-transaction.md),
[systemd guide](manual-systemd-deployment.md),
[shared PostgreSQL maintenance](shared-postgres-maintenance.md) and associated
scripts retain historical recovery details. They may depend on the exact archived
deployment tool that owned an interrupted transaction. They are not current
installation instructions. Keep those exact artifacts and private state until
that transaction is resolved; never rebuild or replace its owner from new source.
