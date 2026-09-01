# SQLite to PostgreSQL migration contract

This document describes the auditable offline rehearsal and verification
workflow for the PostgreSQL 18 target. It does not authorize or implement a
production cutover. Production may already be running Go with PostgreSQL after
a historical coordinator run, but that runtime fact does not replace current
boundary, schema, canary, backup, and operator evidence.

The fresh contract-1 PostgreSQL baseline requires `users.console_activated_at BIGINT NOT NULL DEFAULT 0`. This fresh-schema contract does not upgrade an existing PostgreSQL schema. Contract 2 is the separately reviewed, forward-only expand step for the eight open-source-bounty tables used by the mounted Rust routes; it never changes the frozen 34-table manifest.

## Evidence and scope

The versioned manifest defines 34 application tables, their columns, keys, indexes, converters, sequence ownership, and verification rules. CI rehearses the contract on PostgreSQL 18 and validates 34 tables, 424 columns, 172 indexes, and 29 owned sequences.

## Commands

Run from `apps/api-rust/`:

```bash
cargo run -p lmm-db-migrate -- manifest-validate \
  --manifest crates/lmm-db-migrate/schema/table-map.json

cargo run -p lmm-db-migrate -- inspect \
  --sqlite /path/to/offline/one-api.db \
  --manifest crates/lmm-db-migrate/schema/table-map.json \
  --report /path/to/audit/inspect.json

export LMM_MIGRATE_DATABASE_URL='postgresql://migration-role@/database?host=/run/postgresql'

cargo run -p lmm-db-migrate -- rehearse \
  --sqlite /path/to/offline/one-api.db \
  --manifest crates/lmm-db-migrate/schema/table-map.json \
  --baseline crates/lmm-db-migrate/schema/postgresql-baseline.sql \
  --catalog-sql crates/lmm-db-migrate/schema/export-postgres-catalog.sql \
  --schema lmm_rehearsal_20260801 \
  --report /path/to/audit/rehearse.json

cargo run -p lmm-db-migrate -- verify \
  --sqlite /path/to/offline/one-api.db \
  --manifest crates/lmm-db-migrate/schema/table-map.json \
  --schema lmm_rehearsal_20260801 \
  --report /path/to/audit/verify.json

# Only after contract 1 is installed in the existing target schema:
export LMM_MIGRATE_DATABASE_URL='postgresql://migration-role@/database?host=/run/postgresql'
cargo run -p lmm-db-migrate -- forward \
  --schema lmm_rehearsal_20260801 \
  --contract-migration migrations/0002_open_source_bounty_schema.sql \
  --report /path/to/audit/forward.json \
  --contract-id 2 \
  --contract-sha256 "$(sha256sum migrations/0002_open_source_bounty_schema.sql | awk '{print $1}')" \
  --min-reader-version 1 --max-reader-version 2 \
  --min-writer-version 1 --max-writer-version 2 \
  --release-id release-contract-2 \
  --release-sha256 SHA256_RELEASE_ARTIFACT \
  --component-sha256 api-server-binary=SHA256 \
  --component-sha256 api-server-revision=SHA256 \
  --component-sha256 db-migrator-binary=SHA256 \
  --component-sha256 postgresql-baseline=SHA256 \
  --component-sha256 table-manifest=SHA256 \
  --component-sha256 postgres-catalog-exporter=SHA256 \
  --component-sha256 platform-contract-sql=SHA256 \
  --component-sha256 migration-provenance=SHA256 \
  --component-sha256 legacy-route-oracle=SHA256

bash crates/lmm-db-migrate/scripts/rehearse-postgres.sh
crates/lmm-db-migrate/scripts/verify-provenance.sh
```

The PostgreSQL DSN is accepted only through `LMM_MIGRATE_DATABASE_URL`. It is never accepted as a command-line argument or written to an audit report.

## Offline source boundary

All source commands reject symlinks, special files, and SQLite `-wal`, `-journal`, or `-shm` sidecars. Before opening the source, `rehearse` and `verify` capture its canonical path, device, inode, size, nanosecond modification time, and SHA-256. The opened file descriptor is immediately measured and hashed again, then retained while SQLite is opened read-only through its `/proc/self/fd` path. This closes the path-replacement window while preserving SQLite read-lock semantics.

A single SQLite read-only transaction remains open throughout COPY and source-side verification. Immediately before PostgreSQL commit, the path identity, metadata, SHA-256, schema contract, and absence of sidecars are checked again. The SQLite transaction is committed only after PostgreSQL commits successfully, so the protected source snapshot is never released early.

## Rehearsal transaction

The target schema identifier must match the strict lower-case identifier contract. A PostgreSQL transaction-scoped advisory lock serializes creation of that schema, and schema existence is checked under the same transaction.

The baseline is applied to a new isolated schema. All 34 tables are streamed through PostgreSQL COPY using the manifest's explicit column lists and converters. Rows use complete primary-key order: SQLite text keys use byte ordering and PostgreSQL text keys use `COLLATE "C"`. JSON, booleans, UTC timestamps, fixed-scale decimals, finite REAL values, NULL, and COPY control characters have explicit canonical behavior.

After COPY, all 29 owned sequences are advanced with `setval`; an empty table correctly produces `nextval = 1`. The live PostgreSQL catalog is validated against the manifest. SQLite and PostgreSQL are then read independently and compared using per-table counts and canonical BLAKE3 table hashes. Financial aggregate checks cover users, tokens, logs, quota data, top-ups, subscription orders, and channels without publishing aggregate values.

COPY, catalog, sequence, or verification failure rolls back the complete target schema transaction. `verify` uses a read-only, repeatable-read PostgreSQL snapshot.

## Audit output

Success and failure reports are created with mode `0600`, written through a same-directory temporary file, fsynced, atomically renamed, and followed by a parent-directory fsync. Reports contain no DSN, row value, primary-key value, financial value, or underlying error text. Failure reports contain only a stable stage and error category. Standard error uses the same classifications and does not print conversion values or PostgreSQL/SQLite error details.

## Production transaction

The retired shell coordinator is not a production entry point. Provider-native
migration and deployment operations are invoked only through `/usr/bin/lmm-api`
and follow [`postgresql-cutover.md`](postgresql-cutover.md) plus the normative
[`backend-cli-deployment-contract.md`](backend-cli-deployment-contract.md).

A migration command may create or verify an isolated schema or apply an
explicitly bound forward contract, but it cannot silently authorize traffic or
provider ownership. If a live target is PostgreSQL-backed, first verify the
active schema, durable write boundary, candidate/package hashes, and
authenticated canaries. Missing or failed evidence blocks migration and backend
selection.

After the PostgreSQL write boundary may have been crossed, application rollback
is manual and restores only N-1 code, provider link, frontend, and configuration
that remain compatible with the current schema. It never restores SQLite or a
database snapshot. PostgreSQL remains the persistent authority; Valkey carries
reconstructable cache, session/revocation, and rate-limit state.
