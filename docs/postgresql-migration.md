# PostgreSQL migration boundary

The previous Rust-only SQLite importer, schema manifests, and numbered SQL
contracts have been removed with the retired backend. They are not executable
instructions for the new core. Historical test counts and checksums do not
certify a new database schema.

## Current Go installation

Keep the current Go database and its existing units, users, keys and ledger.
Use the native `/usr/bin/lmm-api migrate --apply|--verify` procedure only inside
an approved deployment transaction. Read [production cutover](postgresql-cutover.md)
and [the operator contract](backend-cli-deployment-contract.md) before any change.
A schema verification command is not an authorization to cut over traffic.

## New core (not implemented yet)

The [core migration](core-migration.md) must introduce a reviewed importer and
versioned, additive schema changes with a single owner for each table. Do not
point a scaffold at production or assume Go and Rust can both mutate balances.
Go extensions must not run migrations on core-owned tables.

Before cutover, rehearse with an isolated, access-controlled copy. Compare keys,
authentication and revocation versions, users and team memberships, account
ownership, integer balances, reservations, subscriptions, quotas and audit
records. Keep identifiers and old API key behavior stable. Neither regenerating
keys nor resetting user sessions is an acceptable migration shortcut.

Verify the exact source revision, schema, counts and financial invariants; test
concurrent spending, process termination, duplicate callbacks and recovery.
Rehearse N/N-1 readers and writers against the expanded schema. Publish only
sanitized test evidence, never credentials, balances or user rows.

Only after parity passes may the approved core become the sole billing writer.
Application rollback requires compatible code and schema; it must not restore
an old database snapshot and discard settled charges. Database restoration is
a separate disaster-recovery procedure with explicit authorization.
