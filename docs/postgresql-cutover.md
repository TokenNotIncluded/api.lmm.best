# PostgreSQL production boundary

The historical SQLite-to-PostgreSQL shell coordinator has been retired. New
production migration, verification, deployment, confirmation, and recovery
operations are backend CLI responsibilities and are invoked through
`/usr/bin/lmm-api`.

This document does not authorize a new database cutover or Rust business-route
ownership.

## Current-state rule

A historical audit observed Go using PostgreSQL and the dedicated Valkey, but
historical logs are not current acceptance. Before any database-changing release
or provider switch, inspect the live process environment without printing
values and verify:

- exact PostgreSQL database/schema/application-role identity;
- current forward-only write boundary and migration contract;
- package/provider/frontend identities;
- PostgreSQL and Valkey readiness;
- authenticated canaries and representative business requests;
- N/N-1 schema compatibility.

Conflicting, missing, failed, or stale evidence is a hard stop. Do not run a new
SQLite copy, point the service back to SQLite, edit the production environment
by hand, or invent a boundary marker.

## CLI contract

Use the verified public provider symlink only:

```text
/usr/bin/lmm-api migrate --apply
/usr/bin/lmm-api migrate --verify
/usr/bin/lmm-api deploy production status ...
/usr/bin/lmm-api deploy production confirm ...
/usr/bin/lmm-api deploy production rollback ...
```

CLI dispatch and status inspection must not expose DSNs or require unrelated
server startup. A release-scoped candidate runs through a validated workspace
symlink named `lmm-api`, never by directly executing `lmm-api-go` or
`lmm-api-rs`.

Migration input comes only from root-only environment configuration. Reports
contain stable stages/categories and immutable hashes, not DSNs, row values,
keys, tokens, financial values, or raw database errors.

## Forward-only boundary

Before a provider or release can write through a new schema contract, persist
and fsync an immutable boundary that binds:

- deployment and contract identifiers;
- database/schema identity without credentials;
- candidate/N-1 package and Git revisions;
- provider and migrator hashes;
- migration/provenance hashes;
- allowed reader/writer compatibility range.

Once evidence shows the new PostgreSQL write boundary may have been crossed,
SQLite is not an automatic or manual application rollback target. Application
rollback restores only N-1 provider/package/link/frontend/configuration state
that is proven compatible with the current PostgreSQL schema. Database restore
is always a separate, explicitly authorized disaster-recovery operation.

## Manual recovery

Production deployment has no timer and no automatic rollback. Before the first
live mutation the CLI stores verified N/N-1 artifacts and a rollback-eligible
state while holding the transaction lock.

- A pre-mutation failure becomes `FAILED_PREARM`.
- A post-boundary failure becomes `ROLLBACK_REQUIRED` and retains evidence.
- Healthy activation completes at least 120 seconds of observation and stops at
  `AWAITING_CONFIRMATION`.
- Only an explicit exact-ID `confirm` or `rollback` is terminal.

A failed rollback remains retryable. It never restores the database and never
reports success while service, provider-link, schema, or health identity is
uncertain.

## Rehearsal

Before a new schema contract reaches production, rehearse it with a fresh
marker-owned PostgreSQL database and dedicated Valkey instance containing no
production data. Prove:

1. migration apply and verification against the immutable contract;
2. package/provider-link switch and process identity;
3. interruption at each post-mutation phase yields `ROLLBACK_REQUIRED`;
4. explicit rollback restores compatible N-1 application state without a
   database restore;
5. explicit confirmation verifies health, restart count, PostgreSQL/Valkey,
   provider link, packages, and authenticated canaries;
6. no report, log, manifest, or process argument exposes secrets.

Repository fake-runner tests are necessary but do not authorize production.
