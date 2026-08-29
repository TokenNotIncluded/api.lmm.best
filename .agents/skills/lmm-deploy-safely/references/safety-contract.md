# LMM deployment safety contract

## Authorization and identity

- Default to `local`. Require explicit current-turn authorization for `test` or
  `production` and verify the exact SSH alias, static hostname, role marker,
  service name, package identities, active provider link, frontend link, and
  CLI protocol before mutation.
- The real providers are `/usr/bin/lmm-api-go` and `/usr/bin/lmm-api-rs`.
  `/usr/bin/lmm-api` is a one-hop relative symlink to exactly one provider.
  Production services and operator actions invoke only `/usr/bin/lmm-api`.
- Verify the symlink with `lstat` and `readlink`, the real target with
  `realpath`, `pacman -Qo`, package integrity, mode/owner checks, signed release
  metadata, SHA-256, Git revision, and API/route contract revision. Reject a
  regular `/usr/bin/lmm-api`, reverse alias, chain, absolute target, missing
  provider, unowned/writable provider, or identity mismatch.
- A verified legacy Go `0.1.x` regular `/usr/bin/lmm-api` is accepted only as
  explicit N-1 migration/rollback evidence. Never publish that layout again.
- Host, role, package, protocol, link, or route-ownership disagreement is a
  hard stop.
- A deploy request does not implicitly authorize Git repair, branch switching,
  commits, pushes, tags, release publication, AUR publication, backups, or
  provider ownership transfer. Obtain explicit authorization for each class.

## Workspace and build contract

- Create one deployment ID matching `[A-Za-z0-9][A-Za-z0-9._-]{0,127}`.
- Use marker-owned persistent workspaces, never `/tmp` or `/var/tmp`:
  - controller: `${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api/deploy-work/<id>`;
  - production target: `/var/lib/lmm-api-go-deploy/work/<id>` until a separately
    rehearsed generic-state migration.
- Put `TMPDIR`, Go/Cargo/Bun caches, dependency trees, packages, manifests, and
  logs beneath that exact workspace. Private directories are `0700`; manifests,
  status, configuration evidence, and secret-bearing files are `0600`.
- Build once from one frozen clean revision. Record and verify the same artifact
  SHA-256 at every release, package, staging, and activation boundary. Never
  overwrite an immutable release with different bytes.
- Serialize heavy builds on the small controller (`GOMAXPROCS=2`, Go package
  parallelism `2`, Cargo jobs `2`) unless fresh resource evidence permits more.
- The root `deploy/` directory is retired. Deployment behavior belongs in both
  provider CLIs; immutable assets live under `packaging/`; language tests replace
  shell-only deployment tests.

### Bounded state

- Measure `${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api` before builds and after
  terminal cleanup. Warn at 256 MiB and stop new builds at 512 MiB or earlier
  when the storage gate is yellow.
- Remove only exact disposable children of marker-owned terminal workspaces.
  Never prune active/nonterminal workspaces, transaction locks, application or
  database history, active releases, backups, or another deployment.
- A large or unexplained state root is a stop-and-report condition, not
  permission for broad deletion.

## Release and package identity

- Reconcile tag patterns, artifact/provider filenames, `PKGBUILD`/`.SRCINFO`
  URLs and hashes, Sigstore workflow identities, package metadata, and
  `packaging/aur/README.md` from one frozen revision before publication.
- Go release/package payloads install a real `lmm-api-go`; Rust payloads install
  a real `lmm-api-rs`. New provider packages do not own `/usr/bin/lmm-api` and
  do not conflict with the other provider merely for existing.
- A candidate operator is invoked through a release-scoped one-hop symlink named
  `lmm-api`; validate its target name and hash before every dispatch. Package
  inspection may name provider files, but deployment commands never execute a
  provider filename directly.
- Production `paru` runs as the established unprivileged account, never root,
  and assembles only the pinned verified package set. `pacman -U` may run only
  through the exact reviewed privilege path.
- Validate package archive headers and `.MTREE` for root ownership, safe
  types/modes, signed-member parity, exact critical files, provider layout, and
  absence of forbidden generic/reverse aliases.
- Persist transient-unit identity and bounded attempt count before remote
  dispatch. On transport ambiguity, reconcile the unit, manifest, and status.
  Redispatch the exact plan at most once and only when all three are absent.

## Production resource gates

The production root is 20 GiB and `lmm-api.service` is bounded by
`MemoryHigh=320M`, `MemoryMax=384M`, and `MemorySwapMax=256M`. Before mutation,
during transfers, throughout observation, and before confirmation record:

- `df -h /`, `df -i /`, `free -h`, and `vmstat 1 5`;
- service `MainPID`, `NRestarts`, memory/swap limits and counters;
- PostgreSQL and Valkey readiness;
- `/api/status`, `/api/livez`, provider link, package, process executable, and
  frontend-link identities;
- relevant error journal entries.

Thresholds:

- Green: root/inodes `<70%`, `MemAvailable >=30%`, swap `<10%`, CPU `<70%`
  for five minutes, and at least 4 GiB free before production packages/backups.
- Warning: root/inodes `70-80%`, memory `20-30%`, swap `10-25%`, or CPU
  `70-85%`; serialize work and prune only measured terminal state.
- Stop: root/inodes `>=80%`, insufficient package + requested backup + 1 GiB
  headroom, memory `<20%`, swap `>25%` with churn, CPU `>85%` for five minutes,
  restart/OOM evidence, write failures, or required-probe timeout. At `>=90%`
  storage treat the host as an incident.

Do not clear swap, journals, caches, or databases to make a gate green. Do not
kill unrelated processes or hide failed checks with blind restarts.

## Database and cache identity

- Inspect configuration without sourcing it and without printing values.
- Classify only recognized SQLite, PostgreSQL, or MySQL settings; fail on
  disagreement, ambiguity, or unknown engines for database-changing releases.
- Never place DSNs, credentials, tokens, or private configuration in command
  output, manifests, process titles, logs, or off-host plaintext.
- Live process environment and listeners are current evidence. Historical
  cutover prose is not.
- Production PostgreSQL and dedicated Valkey identities, schema boundary, and
  N/N-1 migration compatibility must be proven before backend mutation.
- Local acceptance uses fresh marker-owned PostgreSQL and Valkey instances only;
  SQLite fallback and production data are forbidden.

## Optional backup copies

Backups are optional and require explicit current-turn authorization. When
selected, require verified copies:

| Role | Required copies |
| --- | --- |
| local | controller |
| test | target, controller |
| production | target, controller, off-host |

Production roots:

- target: `/var/lib/lmm-api-go-deploy/backups/<id>`;
- controller: `$HOME/backup/lmm-api/<verified-host>/<id>`;
- off-host: `/home/arch/.local/state/lmm-api-production-backups/<id>` on the
  verified `archczy` host.

Each copy contains a manifest, `SHA256SUMS`, nonempty application/frontend/
configuration archives, and a database backup when applicable. Controller and
off-host secret-bearing archives are encrypted before transfer; checksums cover
the transferred encrypted bytes. Never prune an active/unconfirmed release,
latest-known-good backup, or a copy whose remaining peers are unverified.

## Manual rollback state machine

There is no scheduled rollback service/timer and no automatic rollback on
activation failure, observation failure, cancellation, process exit, or reboot.

Before the first live mutation:

1. Verify and persist immutable N/N-1 provider, package, frontend, configuration,
   schema, and optional-backup evidence.
2. Persist a rollback-eligible state while holding the transaction lock.
3. Re-hash all artifacts and provider-link targets.

Failure semantics:

- Before the mutation boundary: write `FAILED_PREARM`, release the lock, and
  retain bounded audit evidence.
- At or after the boundary: write `ROLLBACK_REQUIRED`, keep the lock and all
  rollback evidence, and stop further mutation.
- If the status write itself fails after mutation, the previously persisted
  rollback-eligible state and lock remain authoritative.
- Failed rollback remains retryable, retains evidence, and never reports a
  terminal success.

A healthy switch observes for at least 120 seconds and ends in
`AWAITING_CONFIRMATION`. Only these explicit commands make progress:

```text
/usr/bin/lmm-api deploy production confirm ...
/usr/bin/lmm-api deploy production rollback ...
```

Confirmation names the exact deployment, verifies completed observation,
provider symlink/target/package/hash, backend process identity, frontend link,
service restart baseline, PostgreSQL/Valkey, health canaries, journals, memory
limits, and immutable archives before writing `CONFIRMED` and releasing the
lock.

Rollback names the exact deployment, re-verifies untampered N-1 evidence,
restores only the approved provider package/link, frontend link, and
configuration snapshot, verifies health/identity, writes `ROLLED_BACK`, and
then releases the lock. It never restores a database automatically.

## Go/Web update order

1. Freeze a clean revision equal to `origin/main`; pass Go, Web, route-contract,
   package, and provider-link checks.
2. Publish immutable signed Go and Web assets with matching API/route contract
   revisions and provider-correct filenames.
3. Update, test, commit, and publish exact pinned AUR metadata; read it back.
4. Assemble exact candidate and N-1 Go/Web packages as non-root and verify
   package/archive/provider identities.
5. Create requested backup copies, when authorized, and persist manual rollback
   evidence before mutation.
6. Run candidate `migrate --apply` and `migrate --verify` through a validated
   candidate symlink named `lmm-api`; migrations must remain N/N-1 compatible.
7. Install exact packages, atomically establish/verify `/usr/bin/lmm-api ->
   lmm-api-go`, start the service, activate Web, and verify local/public probes.
8. Observe at least 120 seconds. Stop at `AWAITING_CONFIRMATION` and perform
   explicit confirmation or rollback.

A Web-only publication must not reinstall/restart Go. A Go-only publication
must not replace the active Web payload. A Rust provider install does not grant
Rust route ownership.

## Rust ownership gate

- `migration-gate.tsv` is authoritative for route ownership. Reject unresolved,
  inconsistent, unverified, or unapproved auth/quota/billing/streaming routes.
- Rust CLI/package parity, a provider symlink, health probes, mounted routes, or
  historical rehearsals do not transfer business ownership.
- PostgreSQL/Valkey identity, shared session/rate-limit semantics, singleton
  jobs, SSE/WebSocket drain/reconnect, N/N-1 migration compatibility, and
  explicit route-by-route approval are required before a Rust production switch.

## Cleanup

- Clean only an exact marker-owned workspace whose state is `CONFIRMED`,
  `ROLLED_BACK`, controller-only `VALIDATED`, or verified pre-switch `ABORTED`.
- Preview first. Reject roots, home, `/tmp`, `/var/tmp`, backup/release roots,
  unresolved variables, globs, tildes, symlinks, and paths outside the marker.
- Remove only disposable staging, temporary files, dependencies, caches, and
  package archives. Retain marker/status audit evidence and durable releases,
  rollback packages, and requested backups.
- Re-run resource, service, database/cache, provider-link, and health checks and
  measure the bounded state root after cleanup.

## Minimum validation before production

- Go and Rust tests cover provider-link safety, shared command/schema parity,
  safe IDs/paths, host mismatch, optional backups, manual rollback states,
  interruption/reboot recovery, explicit confirmation/rollback, tampered
  evidence, retryable rollback, and exact cleanup.
- No tracked runtime, workflow, package, test, or documentation depends on the
  removed root `deploy/` directory.
- Release/AUR/Sigstore/provider identities are consistent for the frozen
  revision and identical bytes pass local PostgreSQL/Valkey acceptance.
- Authenticated canaries and representative business requests pass; affected
  browser, SSE, and WebSocket behavior is reviewed.
- An independent reviewer approves deployment behavior and residual risk.
