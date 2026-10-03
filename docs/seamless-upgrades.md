# Frontend and backend upgrades

## Choose the installation path

This guide describes the **signed package transaction** path. For an existing
standalone Go installation on systemd, use
[standalone systemd deployment](manual-systemd-deployment.md): `doctor`,
`upgrade`, then explicit `confirm`. That workflow refuses package-owned
providers and does not replace the signed package controller or its gates.
Its software rollback does not restore a database. The standalone workflow and
signed package controller have separate transaction and recovery contracts.

Packages install a real `lmm-api-go` or `lmm-api-rs` provider and a one-hop
`/usr/bin/lmm-api` symlink. Service and native operator actions enter through
that symlink. The reviewed `/usr/bin/lmm-api-deploy` package script is the public
deployment entry; it dispatches native operator actions through `lmm-api`.
Backend and frontend release versions remain independent.

Use the native CLI for application-level server control:

```bash
ssh ArchDmit /usr/bin/lmm-api status
ssh ArchDmit /usr/bin/lmm-api doctor
ssh ArchDmit /usr/bin/lmm-api request --show-status /api/status
```

SSH is only the transport here. Host-level inspection (systemd, filesystem,
memory, and journal reads) remains separate and read-only unless a guarded
deployment transaction has been explicitly authorized.

## Verify the production boundary

Before every production change, verify the installed provider/package/frontend
identities, active PostgreSQL database and schema, forward-only write boundary,
Valkey readiness, native transaction status, and authenticated canaries. Missing,
failed, or stale evidence blocks the change. Follow the
[PostgreSQL production boundary](postgresql-cutover.md) and normative
[backend CLI deployment contract](backend-cli-deployment-contract.md);
historical observations do not establish current acceptance.

## Frontend: zero-downtime static releases

Build the frontend into an immutable signed release in CI, then build the
exact `lmm-api-web-bin` package from that asset. A production release plan
pairs candidate and rollback Go/Web packages, their signed release archives
and Sigstore bundles, and the candidate `lmm-api` probe binary. The controller
verifies tags, ancestry, signatures, checksums, package payloads, and route
contract revisions before writing canonical immutable JSON.

Production uses resumable controller phases:

```bash
/usr/bin/lmm-api-deploy production plan ...
/usr/bin/lmm-api-deploy production stage \
  --plan <release-plan.json> --plan-sha256 <sha256> --confirm api.lmm.best
/usr/bin/lmm-api-deploy production promote \
  --plan <release-plan.json> --plan-sha256 <sha256> --confirm api.lmm.best
/usr/bin/lmm-api-deploy production status|confirm|rollback \
  --plan <release-plan.json> --plan-sha256 <sha256> --confirm api.lmm.best
```

`stage` only creates the marker-owned target workspace and transfers exact
verified artifacts. `promote` performs the guarded package transaction and
health observation, then stops at `AWAITING_CONFIRMATION`. The operator must
explicitly confirm or roll back the exact transaction; there is no scheduled
or automatic rollback. Release plans select `disabled` or `controller-only`
backup mode, as defined by the
[controller-only backup evidence contract](controller-only-backup-format.md).
Remote mutations require operator authorization and exact host identity.

The frontend transaction validates `index.html` and local asset references,
copies into same-filesystem staging, and atomically replaces
`/srv/lmm-api-frontend/current`. Before switching, it copies `/static` files
into the cumulative immutable store at `/srv/lmm-api-frontend/assets`,
rejecting a same-name/different-content collision, then makes the versioned
release read-only. A browser holding the previous `index.html` can therefore
continue lazy-loading its hashed chunks after a switch. `flock` serializes
publishers and retention always includes the current release. Static assets
are not garbage-collected in this phase; any future GC must preserve assets
referenced by every retained release.

Rollback is performed by the same guarded transaction and restores the exact
verified prior frontend release. Do not invoke a source-tree publisher or
construct a parallel public helper command for package-owned installations.

The site uses its own `/etc/nginx/lmm-api-mime.types`; it never creates or
overwrites nginx's global `/etc/nginx/mime.types`. Publish all nginx inputs
with the controlled transaction rather than copying them individually. The
installer serializes with `flock`, reserves a unique non-overwritable backup,
and manages `/etc/nginx/lmm-api-mime.types`, the HTTP `map`, server locations,
and `/etc/nginx/conf.d/new-api.conf`. Each candidate is installed as a
root-owned `0644` same-directory temporary file and published atomically. Only
after every file is in place does it run `nginx -t`, reload nginx, and verify
the unit remains active. Failure restores the prior presence or exact content
of every managed file and repeats validation, reload, and service checks.

The server-scoped template explicitly includes `/etc/nginx/lmm-api-mime.types`.
The template sends known backend route families to port 3000, preserves
WebSocket/SSE behavior, serves the shared `/static` asset store with immutable
caching, and makes entry points revalidate. Missing static or root-public
assets return 404 instead of SPA HTML. Production `/terms` and `/privacy`
remain exact aliases to the legal HTML files.

## Backend service and upgrades

Systemd runs the canonical service entry directly:

```ini
ExecStart=/usr/bin/lmm-api serve
```

Status, diagnostics, HTTP probes, deployment, and GeoIP maintenance are all
`lmm-api` subcommands. The signed package controller holds the deployment lock
and persists the immutable manifest, verified rollback artifacts, and recovery
state before the first live mutation. A failure after that boundary becomes
`ROLLBACK_REQUIRED`; healthy activation stops at `AWAITING_CONFIRMATION` after
the observation gate. Only an explicit exact-ID `confirm` or `rollback` makes
the transaction terminal. Lost replies require read-only status reconciliation
before another action. See the
[manual release acceptance flow](production-release-transaction.md).
Restarting the sole backend interrupts active connections.

Before invoking a subcommand on an unknown historical target, first classify
the installed package and systemd `ExecStart` without executing an unproven
legacy binary. The supported service contract is
`ExecStart=/usr/bin/lmm-api serve`; the canonical path must be owned by an
approved Go package with zero altered files. Use package/systemd metadata, the
running PID, sanitized process-environment scheme checks, and explicit HTTP
probes for legacy classification, then follow the supported
[legacy native CLI bootstrap](backend-cli-deployment-contract.md#staging-from-a-legacy-native-cli)
before relying on the unified controller.

Always read `apps/api-rust/tests/fixtures/routes/route-gate.tsv` for the
current route ownership and approval state; prose is not an authority for
route counts.

## Rust provider boundary

Rust remains a migration candidate. Build, package, provider-link, and service
operations follow the [Rust provider rollout contract](rust-blue-green.md).
The old shell-managed blue/green slot framework is retired. A compiled binary,
mounted route, successful readiness probe, or historical rehearsal does not
transfer production business ownership. Ownership remains on the approved
backend until the independent differential and deployment gates pass.

## PostgreSQL production migration and reconciliation prerequisite

The historical SQLite-to-PostgreSQL shell coordinator is retired. Production
migration and verification use `/usr/bin/lmm-api migrate --apply|--verify`
under the [PostgreSQL production boundary](postgresql-cutover.md).
Use the [offline migration rehearsal](postgresql-migration.md) for fresh,
isolated schema preparation and verification; it does not authorize traffic.

Verify the active schema, durable write boundary, signed package identities,
N/N-1 compatibility, and authenticated canaries before a database-changing
release or provider switch. Missing or failed evidence requires reconciliation.
Once the PostgreSQL write boundary may have been crossed, application rollback
restores only compatible N-1 code, provider link, frontend, and configuration.
It never restores SQLite or a database snapshot. Database restoration requires
a separate disaster-recovery operation, and connection interruption must be
accounted for in the maintenance plan.

Before business traffic moves to Rust, route/auth/quota/billing/streaming
parity, expand/contract migrations compatible with N and N-1, singleton
background-job ownership, authenticated canaries, and graceful SSE/WebSocket
draining and reconnection remain mandatory. Nginx must not automatically retry
non-idempotent requests.

An independent frontend release requires compatibility with the active Go
backends. Rust internal probes and business canaries must pass before a guarded
provider switch. A mounted candidate or a successful `/readyz` never replaces
the route gate and paired listener evidence.
