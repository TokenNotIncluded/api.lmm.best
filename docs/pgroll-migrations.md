# pgroll migration rehearsal

[Development](development.md) · [Documentation index](README.md) · [Historical SQLite to PostgreSQL contract](postgresql-migration.md)

This repository provides a pinned pgroll installer and an isolated PostgreSQL
rehearsal. **New PostgreSQL schema changes use pgroll migration files and
rehearsal evidence as their default delivery format.** This does not yet
integrate pgroll with the Go or Rust application, replace the current migration
owner, or authorize a production migration. Application startup and
provider-native deployment keep their existing migration contracts.

The [SQLite to PostgreSQL 18 contract](postgresql-migration.md) remains the guide
for that historical conversion and its reviewed forward contracts. The JSON
files under [migrations/pgroll/rehearsal](../migrations/pgroll/rehearsal) are
independent synthetic examples, not application schema migrations.

## Install and inspect the pinned tool

Run from the repository root with Python 3.10 or newer and Just:

```bash
just pgroll-install
just pgroll-version
```

[tools/pgroll.lock.json](../tools/pgroll.lock.json) pins pgroll **v0.16.3**, the
official repository and asset names, the release checksum manifest digest, and
a binary SHA-256 for each supported platform: Linux amd64/arm64 and macOS
amd64/arm64. [scripts/pgroll.py](../scripts/pgroll.py) constructs the official
versioned release URLs and verifies both the checksum manifest and the selected
binary before installation.
It does not select `latest`, run an installation shell script, use `sudo`, or
install a system package.

The binary lives under
`${XDG_CACHE_HOME:-$HOME/.cache}/lmm-api/tools/pgroll/0.16.3/` with its official
asset name, such as `pgroll.linux.amd64`. The wrapper uses that pinned binary
rather than an unrelated `pgroll` from `PATH`. `pgroll-version` requires an
already installed binary and verifies it without downloading. Installation
requires access to the official GitHub release when the verified binary is not
already cached.
Updating pgroll requires a reviewed lock change and a new rehearsal.

## Run the isolated rehearsal

Install PostgreSQL server tools of the same major version, **17 or newer**:
`initdb`, `pg_ctl`, `postgres`, and `psql`. Run as an ordinary user who can create
a local cluster. Installing pgroll does not install PostgreSQL or start an
application service.

```bash
just test-pgroll
just pgroll-rehearse
```

`test-pgroll` runs the wrapper's offline unit tests. `pgroll-rehearse` runs the
real database exercise. Select a separate PostgreSQL installation or an evidence
directory with the underlying wrapper:

```bash
python3 -B scripts/pgroll.py rehearse \
  --pg-bin /path/to/postgresql-17/bin \
  --evidence /path/to/local/pgroll-evidence
```

The wrapper creates its own temporary cluster and connects through a private
Unix socket with TCP listening disabled. It does not accept a caller-provided
DSN, use `SQL_DSN`, or run the application's migrations. It clears inherited
PostgreSQL and pgroll connection settings before configuring its own fixture.
All fixture subprocesses use a new private working directory and an empty
private `HOME`/`XDG_CONFIG_HOME`, so caller configuration and password files are
outside their configuration search locations. The pinned v0.16.3
[CLI setup](https://github.com/xataio/pgroll/blob/v0.16.3/cmd/root.go) uses Viper
environment/flag bindings without config-file discovery; its state and roll
connections receive the same explicit fixture URL. The private-directory
boundary is enforced independently of that upstream implementation detail.
No production database, role, application process, or deployment transaction
participates in this exercise.

The synthetic exercise checks the pgroll lifecycle against its own small test
table:

1. Create and seed an existing synthetic table, initialize pgroll state, and
   baseline that test schema with pgroll. Baseline records state; it does not
   create the old version views. Start and complete the separate empty-operation
   interface migration to establish those views in the fixture.
2. Validate and start the integer-to-text column change; verify that old and new
   version schemas coexist and the initial rows are backfilled.
3. Read and write through both versions, including overlapping writers and
   sample `ON CONFLICT` queries. Observe that the views have no physical indexes
   and that `TRUNCATE` on a view is rejected.
4. Roll back the in-progress change and verify the old version remains usable.
5. Start the change again, complete it, and verify the surviving version and
   final data.

Evidence defaults to
`${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api/pgroll-rehearsals/TIMESTAMP-ID/`.
`--evidence` selects a new directory; the wrapper refuses to overwrite an
existing one. `result.json` records the tool version and digest, both migration
JSON digests, PostgreSQL client/server versions, completed checks, step exit codes, and cleanup
result. Each step retains private stdout/stderr logs. The temporary cluster is
stopped and removed after the run. A shutdown failure is reported, with any
retained fixture path recorded for recovery.

A run using PostgreSQL 18.6 establishes
that local PostgreSQL 18.6 exercise only; it does not establish a PostgreSQL 17
run. To claim PostgreSQL 17 compatibility, run with PostgreSQL 17 binaries and
retain that separate result. A successful synthetic exercise is not a Go
startup, application query, real-data, browser, or production acceptance test.

## Native database commands also need a scope review

The wrapper's `version` action only runs the cached binary's `--version`.
Native commands that connect to a database have a separate boundary. In
v0.16.3, opening [pgroll state](https://github.com/xataio/pgroll/blob/v0.16.3/pkg/state/state.go)
can automatically upgrade an older state schema. Do not assume database
`status`, `validate`, or `latest` commands are read-only when changing the binary
version; review the state upgrade before allowing that binary to connect.

[State initialization](https://github.com/xataio/pgroll/blob/v0.16.3/pkg/state/init.sql)
creates database-level event triggers `pg_roll_handle_ddl` and
`pg_roll_handle_drop`, along with a `SECURITY DEFINER` function that records
external DDL. PostgreSQL 17 restricts
[event trigger creation](https://www.postgresql.org/docs/17/sql-createeventtrigger.html)
to superusers. The fixture uses the role created by `initdb` in its disposable
cluster. A production design must separately review initialization privileges,
event-trigger ownership and database-wide effects. Native `--role` controls the
roll DDL connection, not the separate state connection; it does not make state
initialization an unprivileged operation. A separate schema alone does not
isolate these database-level effects.

## Why changing SQL_DSN is not an application integration

pgroll exposes schema versions as views over underlying tables. During an
in-progress migration the application can use the old or new view schema;
`complete` retires the old version. This is the upstream
[expand/contract model](https://github.com/xataio/pgroll/blob/v0.16.3/docs/concepts.md).
The current application has additional requirements:

| Boundary | Current contract and work still required |
| --- | --- |
| Schema identity and catalog | Go `LMM_DB_MIGRATION_MODE=verify` requires one explicit configured `search_path`; its effective path must be `pg_catalog` followed by that schema. It checks tables, columns, indexes, primary keys, and foreign keys in `current_schema()`. A pgroll version schema containing views does not automatically satisfy those physical-table checks. Adapt the verifier explicitly; do not add a fallback schema or disable verification. |
| Startup seal | The existing-schema contract binds cluster/database/schema identity and a target-schema metadata digest, including triggers, functions, and views. pgroll DDL can change this catalog during start, rollback, and complete. An old seal cannot automatically qualify the changed catalog. Define how the contract binds the physical schema and supported view versions, and rehearse both application generations before changing it. |
| Migration ownership | The existing application/deployment advisory lock is `0x4c4d4d4150490001`; pgroll state initialization uses `0x2c03057fb9525b`. pgroll is not integrated with the application lock protocol in this repository. A future integration must establish one migration owner and serialize all schema writers; running the two migrators independently is not sufficient. |
| SQL and ORM behavior | Test the exact GORM-generated and handwritten queries, including targeted `ON CONFLICT` upserts, constraint/error handling, sequences, permissions, and `TRUNCATE`. Simple automatically updatable views support some upserts, but that does not prove all application queries work through pgroll views. `TRUNCATE` needs a separately reviewed physical-table or application alternative. |

The local implementation is in
[migration_mode.go](../apps/api-go/model/migration_mode.go),
[postgres_catalog.go](../apps/api-go/model/postgres_catalog.go), and the
[production schema contract](../apps/api-go/internal/deploycli/deploy_production_schema_contract.go).
Concrete query examples include the
[assistant profile upsert](../apps/api-go/model/assistant_lead.go) and
[ability table repair](../apps/api-go/model/ability.go).
For view behavior, compare the upstream
[ORM guide](https://github.com/xataio/pgroll/blob/v0.16.3/docs/guides/orms.mdx)
with PostgreSQL's [CREATE VIEW](https://www.postgresql.org/docs/17/sql-createview.html)
and [TRUNCATE](https://www.postgresql.org/docs/17/sql-truncate.html) contracts.
Application releases must pin their expected version schema in the connection
configuration, including every pooled connection, background worker, and CLI.
A one-off `SET search_path` on one connection is not pool-wide configuration;
automatically selecting `latest` does not bind an application revision to its
expected schema. Schema-qualified table names alone do not set the connection's
schema context for pgroll synchronization triggers. The fixture explicitly sets
the selected version's `search_path` for each connection, including overlapping
writers. See upstream [client connection guidance](https://github.com/xataio/pgroll/blob/v0.16.3/docs/guides/clientapps.mdx).

## Future application rollout contract

The following is required design and acceptance work, not an executable
production procedure supplied by this change:

1. Bind the migration JSON, pgroll binary digest, physical schema, old/new view
   schemas, and compatible application revisions to one reviewed plan. Resolve
   the verifier, startup seal, permissions, SQL behavior, and migration owner
   before any application switches its schema.
2. Rehearse with isolated data and both application revisions. Start the change,
   finish and verify backfill, exercise reads and writes from both revisions,
   and prove rollback while the old version is still available. Record lock
   waits and query behavior as well as row checks.
3. In a separately authorized rollout, let the designated owner start the
   expansion. Keep the old application generation usable while verifying the
   new generation. Reconcile durable migration state after an interruption
   before issuing another mutation.
4. Complete only after old application generations on both origins, their
   connection pools, and all old background readers and writers have drained,
   and acceptance evidence confirms the new version. Completion removes the old schema
   interface; rollback of an in-progress migration and rollback after completion
   are different contracts. Never treat database snapshot restoration as an
   automatic application rollback after new production writes.

Use the [deployment workflow](deployment-workflow.md) and
[backend CLI contract](backend-cli-deployment-contract.md) for existing
production operations. The pinned upstream
[CLI reference](https://github.com/xataio/pgroll/blob/v0.16.3/docs/cli/README.md)
describes native pgroll commands; this repository wrapper deliberately exposes
installation, version inspection, and isolated rehearsal only.
Application availability also requires compatible service generations, traffic
handoff, and streaming connection draining. This database exercise does not
prove an application rollout without downtime.
