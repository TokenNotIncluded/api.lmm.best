# Operations content

This module owns persisted release publications and an installation-reviewed
filesystem script snapshot. It does not access Core storage, run scripts, fetch
repositories or manage repository credentials. Repository configuration and pull
remain unsupported (HTTP 501 after fresh administrator authorization).

## Parent integration contract

- `operations.New(ctx, operations.Config{Releases: runtimePool,
  Scripts: &ScriptConfig{Root: ..., Manifest: ...}})` checks and borrows the
  dedicated operations pool created by composition's `openStorage`, and loads a
  script snapshot. Either component may be omitted; a requested component failure
  fails construction. Composition owns and closes the pool on failed assembly or
  shutdown; the module never closes a supplied pool. Constructor and HTTP requests
  never initialize a schema.
- `webcompat.RegisterOperations(mux, existingAuthenticator, service)` registers
  the exact Web routes: `/api/release-notes/latest`, `/api/release-notes/{id}/read`,
  `/api/release-notes/admin`, `/api/scripts`, `/api/scripts/{name}/raw`,
  `/scripts/{name}` and the explicitly unsupported repository routes.
  **Wrap the actual outer Web admission handler in
  `webcompat.OperationsGuard(handler)` before any shared ServeMux.** Registration
  alone cannot reject dot/double-slash traversal before ServeMux redirects.
- `webcompat.NewOperationsModule(existingAuthenticator, service)` implements
  `modules.Module` for explicit host registration. Its relative paths retain the
  `/api/...` and `/scripts/...` spelling, e.g.
  `/extensions/v1/operations/api/release-notes/latest`. Host admission is not user
  authority: the separate `X-LMM-User-Credential` is verified by Core. The module
  guards its own mux; also wrap the outer shared module host/admission handler
  with `OperationsGuard` so no earlier shared mux can redirect script aliases.
  The guard accepts public and module-prefixed script paths, rejects escaped
  aliases (including `%2e`, `%2E`, `%69`) and leaves unrelated routes unchanged.
- The parent owns shared selection, admission/draining, configuration, offline
  installer dispatch, proxy routing (including `/scripts/*`) and capability
  advertisement. `Service.Availability()` reports assembled components, not
  live dependency health; `script_repository` is always false in this patch.
  HTTP dependency outages return unsuccessful 503 responses, never empty data.

## Offline database installation

Use a fresh, private operations-only database owned by an installation role and
an existing separate runtime login with no elevated attributes or role memberships.
Explicitly call `operations.Initialize(ctx, ownerPool, runtimeRole)` once with
owner credentials, offline. It refuses existing operations or other application
schemas and public relations/functions; this is not an existing-site migration.

The helper revokes PUBLIC database/schema rights and grants runtime only:

- database CONNECT and operations schema USAGE;
- SELECT on the owner-installed schema contract;
- SELECT and INSERT on publications and read acknowledgements;
- SELECT and USAGE on the publication ID sequence (not UPDATE/setval).

No runtime DDL, database CREATE/TEMP, schema CREATE, table UPDATE/DELETE/TRUNCATE,
column grants or grant options are accepted. `Check(ctx, runtimePool)` verifies
isolation, effective privileges and an owner-installed catalog fingerprint.
Isolation excludes only the literal `pg_` system prefix, not lookalikes such as
`pgx_core`. Both initialization and checks reject runtime CREATE on **every**
schema, including `information_schema` and actual PostgreSQL schemas. The
post-grants fingerprint includes relation/schema ownership and ACLs as well as
RLS flags and policy definitions; any RLS flag or policy in this schema is
explicitly rejected. These are installation/startup checks, not continuous drift
monitoring of an already-open pool.
`NewPostgres` calls this check before borrowing the supplied pool. The parent
first applies composition's database identity, cross-database CONNECT and role
path guards, table allowlist and sequence grant checks. There are no Core or
other-extension foreign keys/queries. Preserve installation credentials outside
runtime configuration; no runtime connection should use the installation owner.

## Release semantics and bounds

Publications are immutable. Each version has increasing revisions; changing
content appends a revision. Replaying the latest content of that version returns
the original publication and original publisher, including concurrent retries by
different administrators. Returning to older content after a change appends a new
revision. A transaction-scoped PostgreSQL lock serializes publication/cap checks
across processes. IDs are stable, positive JavaScript-safe integers. Publication
epoch seconds must satisfy `0 < published_at <= 8640000000000`, matching both the
exact JavaScript integer range and Date's inclusive `8.64e15` millisecond limit.
The database constraint and row scanner enforce the same bounds; invalid stored
values fail as unavailable, never get clamped or marshalled into a success DTO.

Latest means the greatest unread publication ID for the verified session user.
Acknowledgements store `(user_id, note_id, revision)` durably and idempotently;
reading an old publication never acknowledges a newer revision. Null is returned
only after an actual successful query finds no unread publication. Administrator
history and publication require fresh Core platform level **5 or 6**, never
service tiers 0–4; the publisher comes from that actor, not JSON.

Version names are 1–64 ASCII characters matching
`[A-Za-z0-9][A-Za-z0-9._+-]*`; content is nonblank valid UTF-8 without NUL,
maximum 32,768 bytes. The database holds at most 10,000 publications through this
service; reaching capacity rejects new publications (exact replay still works).
History returns 1–50 notes, newest first. JSON input and escaped output are
bounded (197,120-byte input; 10 MiB output). Queries are strict, acknowledgements
accept no body, and publication JSON rejects unknown/duplicate fields. No update,
retention deletion or financial action is part of this module.

## Reviewed script snapshot

Configure a non-symlink root and an explicit `[]ReviewedScript` manifest containing
basename and lowercase SHA-256 for every reviewed file. Names are bounded ASCII
basenames ending in `.sh`, `.zsh` or `.ps1`; URLs must use these exact unescaped
basenames on canonical routes. Traversal, directories and symlinks
(including configured root ancestors) are rejected. A nil manifest is unconfigured;
a non-nil empty manifest is an explicitly reviewed empty catalogue and still
requires a real accessible root. Missing files, mismatching hashes and unavailable
filesystems fail construction, not successful empty responses.

The loader opens within a pinned root, rejects raced leaf symlinks/FIFOs, verifies
regular file identity and hashes all bytes. Maximum 128 files, 256 KiB per file,
8 MiB per snapshot. Only manifest files are served. Catalogue metadata is captured
at load; raw routes serve exact immutable bytes even if the filesystem changes.
The module neither mutates source files nor counts fetches. Returned byte slices
and catalogue slices are defensive copies. Refresh requires explicit reconstruction
with a reviewed manifest; there is no automatic network or filesystem reload.

## Local self-tests

Ordinary package tests cover real temporary files and HTTP/authentication rejection.
The persistent tests additionally need `OPERATIONS_TEST_OWNER_DSN`,
`OPERATIONS_TEST_RUNTIME_DSN` and `OPERATIONS_TEST_ADMIN_DSN` pointing to a
**fresh private test database**, with runtime login named `operations_runtime`
and an additional restricted `operations_observer` role. The fixture-admin DSN
is a private-cluster superuser used only to introduce/revoke deliberately excessive
CREATE on actual PostgreSQL schemas and test relation ownership drift; neither
installation owner nor runtime is elevated. The domain test explicitly initializes
and exercises test-only schema/grant/RLS/timestamp drift; handlers use only runtime.
HTTP canonical/escaped-path rejection tests use actual temporary-file bytes and
local HTTP servers and verify source files are unchanged. Run the operations package first, restart the private PostgreSQL
server, then run webcompat to verify actual persisted data after server restart.
Without these variables, database tests are skipped, not accepted as persistence
verification. Rust RPC identity is a boundary test double; a real Core session and
browser/proxy integration require separate parent-owned acceptance.
