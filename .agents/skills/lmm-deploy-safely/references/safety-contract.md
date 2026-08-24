# LMM deployment safety contract

## Authorization and identity

- Default to `local`.
- Require explicit current-turn authorization for `test` or `production`.
- Verify the expected SSH alias, static hostname, role marker, service name,
  installed package identity, current backend artifact, frontend symlink, and
  installed CLI protocol/service entry point.
- Classify the installed layout before invoking a command. The approved
  `lmm-api-go-bin` package owns the single canonical backend/operator entry at
  `/usr/bin/lmm-api`. T0 may retain old command paths only as rollback
  compatibility; T1 removes them. Already published Go packages may still own
  a bundled legacy frontend, but the next split Go package must not. Never mix
  legacy paths, package identities, rollback archives, or state roots with a
  split transaction.
- Treat host or role disagreement as a stop condition.
- Preserve the repository's one-branch, one-worktree, one-diff rules. A deploy
  request does not authorize Git repair, branch switching, commit, or push.

## Workspace and build contract

- Create one deployment ID matching `[A-Za-z0-9][A-Za-z0-9._-]{0,127}`.
- Use marker-owned persistent storage, never `/tmp` or `/var/tmp`.
- Place `TMPDIR`, Go build cache, Go module cache, Cargo target output, Bun
  cache, package staging, manifests, and logs under that deployment directory.
- The controller workspace is
  `${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api/deploy-work/<deployment-id>`;
  do not leave build or package bytes in the repository or in `/tmp`.
- Build once. Record and verify the same artifact SHA-256 at every promotion
  boundary.
- Never overwrite an existing immutable release with different bytes.
- Serialize heavy builds on the small controller (`GOMAXPROCS=2`, Go package
  parallelism `2`, Cargo jobs `2`) unless fresh resource evidence justifies a
  higher limit. Keep all caches in the marker-owned workspace.

### Bounded state

- Treat `${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api` and any deployment-side
  alias such as `states/api.lmm.best` as bounded operational state. Measure the
  resolved root with `du -sx --bytes` before a build and after terminal cleanup.
- Warn at `256 MiB`; stop new builds at `512 MiB` or earlier when the storage
  gate is yellow. Keep only the marker/status in terminal workspaces and remove
  exact `staging`, `tmp`, build-cache, `node_modules`, `dist`, and package
  archive children after `CONFIRMED` or `ROLLED_BACK`.
- Never prune application history, PostgreSQL/Valkey data, active releases,
  backups, or another deployment's workspace to satisfy the budget. A large or
  unexplained state root is a stop-and-report condition, not permission for a
  broad deletion.

### Release and AUR identity

- Reconcile the release workflow tag pattern, artifact names, each changed
  `PKGBUILD`/`.SRCINFO` source URL, Sigstore identity, and
  `packaging/aur/README.md` from one frozen revision before publishing.
- A `vX.Y.Z` workflow cannot satisfy a `web-vX.Y.Z` AUR source (or the reverse)
  without an explicit compatibility/release asset. Treat that mismatch as a
  hard stop; never retag, reuse, or hand-edit around it.
- Push package metadata only to the matching AUR repository. Production
  `paru` runs as the established unprivileged OS account, never root, and may
  assemble only the exact verified package set. A plain `paru` invocation never
  replaces the watchdog, confirmation, or health gates.
- Do not publish or install a deploy-only bootstrap. Move a pre-T0 target
  through the exact signed T0 `lmm-api-go-bin` package and immutable controller
plan. T0 must establish the unified CLI and integrated operator resources
while retaining rollback compatibility; T1 separately removes legacy paths.
For a local T1 package, do not assume `replaces` removes an installed package:
after the watchdog is armed, require package-owned T0 sudoers/sysusers/tmpfiles,
verify the exact legacy package owner and zero altered files, remove only that
package, and then install T1.
- Persist the activation transient-unit identity and bounded attempt count before
remote dispatch. A transport-ambiguous result must reconcile the unit, target
manifest, and target status. Only when all three are absent may the exact same
plan/workspace be redispatched once. If any evidence exists, observe that job;
never issue another apply.
- Before any operator invocation, require `pacman -Qo` to identify the approved
  Go package as owner of `/usr/bin/lmm-api`; require a real non-symlink binary
  and zero altered package files. Verify the package payload against the signed
  release, `RELEASE_ASSET_SHA256`, package version, `REVISION`, Sigstore
  workflow identity, and API/route contract metadata. Stop on any mismatch.

### Production resource gates

The production root filesystem is 20 GiB and the service cgroup is bounded by
`MemoryHigh=320M`, `MemoryMax=384M`, and `MemorySwapMax=256M`. Before a
mutation, during every build/backup transfer, and throughout the watchdog
window, record `df -h /`, `df -i /`, `free -h`, `vmstat 1 5`, the service
`NRestarts`/memory counters, PostgreSQL readiness, Valkey readiness, and native
CLI `/api/status` plus `/api/livez` probes.

- Green: root and inode use `<70%`, `MemAvailable >=30%`, swap `<10%`, CPU
  `<70%` for 5 minutes, and at least 4 GiB free before a production package.
- Warning: root/inode use `70-80%`, `MemAvailable 20-30%`, swap `10-25%`, or
  CPU `70-85%`; serialize heavy work and prune only measured terminal work.
- Stop: root/inode `>=80%`, root free space below the known package plus three
  backup copies and 1 GiB headroom, `MemAvailable <20%`, swap `>25%` with
  churn, CPU `>85%` for 5 minutes, repeated restart/OOM, or a required probe
  timeout. At `>=90%` storage use, treat the host as an emergency and clean
  storage before touching application state.

Do not clear swap, journals, caches, or the database to make a metric green.
Do not kill unrelated processes. A failed resource or health check is an
incident signal and must be recorded before any retry.

## Database detection

- Inspect configuration without sourcing it and without printing values.
- Classify only SQLite, PostgreSQL, or MySQL from recognized configuration
  keys and safe value prefixes.
- Fail when multiple engines are present, when the deployer and live engine
  disagree, or when the engine is unknown for a database-changing release.
- Never put a DSN in command output, a manifest, `SWAP.md`, or a process title.
- Treat the running service environment and active listeners as current
  evidence; historical prose or a previous cutover result is not enough.
- If the live process uses PostgreSQL but the current `PG_WRITE_BOUNDARY`,
  cutover journal, or post-cutover verification is missing or failed, stop and
  reconcile through the coordinator before migration, backend selection, or
  rollback. Do not silently classify that state as a fresh SQLite migration.

## Canonical operator and legacy CLI safety

- Invoke public deployment phases only as `/usr/bin/lmm-api deploy production
  ...` after package-owner, real-path, byte, SHA-256, release revision, and
  command-protocol checks pass. Do not invoke source helpers, copied binaries,
  shell wrappers, `/tmp` tools, or an improvised command. The controller may
  invoke only its digest-verified staged probe for target-private actions.
- `/usr/bin/lmm-api-go` and `/usr/bin/lmm-api-deploy` are T0 compatibility
  paths, not public operators. A historical binary may start the backend for an
  unknown command, so `status`, `deploy`, `--help`, and no-argument calls are
  not read-only until the exact protocol is proven.
- For a legacy target without the proven unified operator, inspect with
  `systemctl show`, `readlink`, `pacman -Qo`, sanitized process-environment
  scheme classification, and explicit health probes only. Use the signed T0
  Go package plan rather than a deploy-only bootstrap. Preserve and report any
  artifact created by an unsafe probe; remove it only after exact ownership and
  scope are confirmed.

## Local acceptance preview override

For this repository's local acceptance preview, SQLite is unconditionally
forbidden, including fallback, autodetection, and default behavior. Require a
fresh marker-owned local PostgreSQL database and role plus a fresh isolated
marker-owned Valkey instance, both initialized with zero production or
business data. Do not use production DSNs, snapshots, or cache dumps. Bind
both to loopback or Unix sockets only; do not reuse an existing database,
schema, role, cache namespace, or port. Verify database and cache identities
before startup. Missing or ambiguous configuration, an unexpected engine, a
nonempty target, or inability to verify identity is an immediate `STOP`, never
a fallback. This override applies only to local acceptance previews; generic
engine detection above remains applicable to other deployment roles.

## Optional backup copies

Backups are not a deployment prerequisite. Do not create, transfer, verify, or
prune them unless the user explicitly requests backups in the current turn.
When the opt-in backup path is selected, require:

| Role | Required verified copies |
| --- | --- |
| local | controller |
| test | target, controller |
| production | target, controller, off-host |

The production controller copy lives at
`$HOME/backup/lmm-api/<verified-host>/<deployment-id>`. The off-host copy lives
on the ArchCzy host, reached through the case-sensitive SSH alias `archczy`,
under a fixed root or in explicitly configured object storage. Verify that
`ssh archczy` resolves to static hostname `archczy`; do not assume the display
name `ArchCzy` is a configured SSH alias.

Each copy contains `manifest.env`, `SHA256SUMS`, a nonempty application archive,
frontend archive, configuration archive, and database backup. The manifest
records:

- format and deployment ID;
- copy role, deployment role, and verified host;
- release ID, artifact SHA-256, and Git revision;
- database engine;
- service state and frontend release identity;
- archive paths, sizes, modes, and UTC timestamps.

The target may retain a root-only plaintext configuration snapshot. Only its
non-secret `manifest.env` and `SHA256SUMS` proof may leave the target in clear
text. Controller and off-host configuration or database archives must be
encrypted before transfer. Checksums cover the encrypted bytes that were
actually transferred.

Default retention is five target, ten controller, and thirty off-host copies.
Prune only after confirmation or verified rollback. Never remove the active
release, an unconfirmed deployment, the latest known-good backup, or a copy
whose remaining peers have not been verified.

Canonical roots are fixed:

- controller workspace: `${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api/deploy-work/<deployment-id>`;
- durable controller copy: `$HOME/backup/lmm-api/<verified-host>/<deployment-id>`;
- production target workspace: `/var/lib/lmm-api-go-deploy/work/<deployment-id>`
  (root-owned and outside the service-writable StateDirectory);
- guarded core target workspace: `/var/lib/lmm-api/deploy-work/<deployment-id>`
  when the installed core contract selects the guarded layout;
- production target backup: `/var/lib/lmm-api-go-deploy/backups/<deployment-id>`;
- off-host copy: `/home/arch/.local/state/lmm-api-production-backups/<deployment-id>`
  on the ArchCzy host through SSH alias `archczy`.

Only the exact workspace's `staging`, `tmp`, and cache children are disposable.
After terminal state, retain its marker/status audit record and any explicitly
requested durable copies; remove staging by exact path. Never recursively clean a backup
root, release root, home/root, `/tmp`, `/var/tmp`, an unresolved variable, or a
glob. A terminal workspace older than 24 hours may be pruned oldest first only
after checksum/decryption verification and only while the storage gate remains
green.

## Rollback watchdog

- Install a persistent systemd watchdog before the first switch.
- Store a root-only, checksum-verified guard containing the exact deployment,
  old/new backend identities, old/new frontend identities, rollback artifacts,
  status, and deadline.
- Arm the watchdog before switching application or frontend state.
- After successful identity and health probes, set a ten-minute deadline and
  return `AWAITING_CONFIRMATION`.
- Manual confirmation must name the exact deployment and re-verify backend
  checksum/version, frontend symlink, relevant services, and health identity.
- Confirmation writes `CONFIRMED` durably before stopping and disabling the
  timer.
- Expiry restores the prior application package, frontend link, and approved
  configuration snapshot, verifies health, writes `ROLLED_BACK`, and preserves
  evidence for review.
- Automatic rollback never restores a database. Block the release unless its
  migrations are compatible with both N and N-1 during the confirmation
  window.

## Go/Web AUR migration order

- Require separately signed immutable Go-only and Web release assets, matching
  API/route contract revisions, and pinned AUR hashes before target package
  assembly. The Go package owns the service, environment, edge policy, exact
  memory drop-in, and backend only; it must not contain `frontend-dist`. The Web
  package owns the immutable frontend and activation hook.
- With the canonical operator already verified, use non-root `paru` to assemble
  both exact candidate packages and both checksum-verified N-1 rollback
  packages. Record Go/Web package versions, package SHA-256, Git revisions,
  contract revisions, binary hash, and frontend link for N and N-1. Do not
  compile or replace signed bytes, install one half of an unsupported pair, or
  use root `paru`.
- Arm the persistent 600-second watchdog before stopping Go or changing the Web
  link. Its guard must contain both N and N-1 package/link identities and the
  configuration restore state. Run the candidate binary as a root transient
  unit with the production environment file; do not use the stopped service's
  `DynamicUser` identity.
- Run `migrate --apply` and then `migrate --verify` before the split `paru -U`
  install. A failed verification blocks both package installations. Never
  repair the gate with ad-hoc production SQL. Migrations must be compatible
  with both N and N-1 for the complete watchdog window.
- Apply the package-owned memory drop-in and start Go before final Web
  activation. A local Web activation probe avoids a DNS-only package-hook
  failure; public status remains a confirmation gate.
- Observe at least 120 seconds with the watchdog armed. Confirm only the exact
  Go/Web package versions and hashes, Git and contract revisions, binary,
  frontend link, unchanged restart count, database/cache readiness, journals,
  three native status/livez probes, and exact memory limits. Generic health or
  confirmation of only one package is insufficient.

## Rust ownership gate

- The Rust blue/green slots own internal GET/HEAD probes only until the route
  gate and independent business differential evidence approve production
  ownership.
- `migration-gate.tsv` must pass source-mode validation before packaging and
  activation-mode validation before a Rust switch. Reject inconsistent
  `legacy-go`/mounted rows, unresolved routes, unverified auth/quota/billing or
  streaming behavior, and any route without independent approval.
- PostgreSQL and dedicated Valkey identity, shared rate-limit/session state,
  background-job singleton behavior, and SSE/WebSocket drain/reconnect are
  required evidence; a mounted slot, active symlink, or `/readyz` response is
  not production proof.

## Cleanup

- Clean only the exact deployment workspace carrying the expected marker and
  matching deployment ID.
- Require a durable `CONFIRMED`, `ROLLED_BACK`, controller-only pre-switch
  `VALIDATED`, or pre-switch `ABORTED` final state. Use `VALIDATED` for
  completed pre-release checks and `ABORTED` for an interrupted attempt, only
  after proving no switch occurred and stopping every workspace-owned process.
- Reject `/`, home roots, workspace roots, `/tmp`, `/var/tmp`, backup roots,
  release roots, unresolved variables, tildes, globs, symlinks, and paths not
  owned by the marker.
- Preview cleanup before execution.
- Remove release artifacts together with staging, temporary files, and build
  caches; durable releases and rollback packages belong outside deploy-work.
- Never implement “clear tmp” as a broad directory deletion. Identify and
  remove only stale, inactive, marker-owned LMM deployment workspaces.
- Re-run disk/inode/RAM/swap, service state, PostgreSQL/Valkey readiness, and
  native CLI status/livez checks after cleanup; record exact paths removed and
  the remaining free-space margin.
- Re-run the bounded-state measurement after cleanup and record the remaining
  bytes. Do not report success if the state root still exceeds the warning
  budget without an owner and an explicit follow-up.

## Minimum validation before production enablement

- Skill validation and shell syntax checks pass.
- Release workflow, AUR source tags, package identities, and Sigstore
  identities are mutually consistent for the exact frozen revision.
- Offline tests cover safe IDs and paths, host mismatch, database disagreement,
  optional-backup selection and verification, timer
  arming, exact-release confirmation, expiry rollback, reboot recovery, and
  scoped cleanup.
- Existing production contract tests require watchdog state and
  `AWAITING_CONFIRMATION`; opted-in backup tests require all requested copies.
- A fresh runtime audit reconciles any historical PostgreSQL cutover result and
  proves the active schema, Valkey endpoint, and forward-only boundary.
- Frontend-only publication is proven compatible with the current Go API before
  it is promoted independently of Rust.
- A local deployment of the identical artifact passes application and frontend
  checks before any test or production promotion.
- Authenticated canaries and representative business requests pass in addition
  to generic health checks; browser, SSE, and WebSocket behavior is reviewed
  when affected by the release.
- An independent Reviewer approves the deployment behavior and residual risk.
