# Shared PostgreSQL schema maintenance

Use this only when two or more existing writer nodes share one PostgreSQL
database and a candidate requires a schema migration. A standalone `--migrate`
upgrade stops only its own node. It cannot establish a cluster-wide backup
boundary while another writer is running.

This coordinator performs a separate maintenance episode before ordinary
package/native or standalone deployment. It never installs a provider, edits
native/standalone transaction state, changes nginx, creates credentials, or
restores production data automatically. Keep the application schema additions
compatible with every captured old binary.

## Reviewed inputs

Use a clean signed release source. Obtain candidate and every actual old binary
through the normal signed-artifact verification procedure. Record their exact
SHA256 values; a release version label alone is insufficient. Each target needs
the exact source-owned coordinator and `deploy-systemd.py`, its candidate binary,
and identical immutable plan bytes in a private input directory. Verify transfer
hashes and modes normally before running anything.

The plan is JSON with exactly these fields:

| Field | Meaning |
| --- | --- |
| `format` | Integer `1` |
| `id`, `confirmation` | Unique maintenance ID and the explicit operator confirmation string |
| `source_sha` | The signed source commit, 40 lowercase hex characters |
| `script_sha256`, `helper_sha256` | Exact coordinator and `deploy-systemd.py` hashes |
| `candidate` | `{ "path": "/local/verified/candidate", "sha256": "..." }` on the non-root controller |
| `nodes` | All writer nodes, two to eight, with the fields below |
| `database_owner` | One node name; only it can dump/apply |
| `public_probes` | Public closed-admission probes with exact 503 body hashes |
| `expected_units` | Exact existing option strings for `CreditsPerUSD`, `LegacyPricingQuotaPerUnit`, `QuotaPerUnit` |
| `rehearsal_root` | A new absent, short absolute local temporary directory |

Each node has exactly `name`, `ssh` (existing authenticated SSH alias),
`hostname`, `script`, `helper`, `plan`, `candidate` (remote paths),
`provider_sha256`, `version`, `invocation` (the live systemd InvocationID),
`rollback` (the verified local old-binary path), and `probes`.
Each probe is `{ "url": "https://example.invalid/closed-path", "body_sha256": "..." }`.
Probe URLs must contain no credentials, query or fragment. Probes issue ordinary
GET requests and require status 503 plus the exact reviewed body; they create no
authenticated request. Plan files must contain no DSN, password, key or token.

The controller needs `ssh`, `scp`, Bubblewrap, `ip`, and PostgreSQL tools of a
version capable of restoring the full dump. It runs as an ordinary user. Node
agents run through existing root SSH authority and ordinary systemd commands.
The effective database access stays in the node's process environment and is
never copied to the controller or printed. A separate log database is unsupported.

## Normal execution

First freeze competing frontend/package/standalone writers through the normal
operator process. Capture the exact live binary hash, version and InvocationID
for every node. Confirm no native transaction lease is active. Use the reviewed
normal nginx/peer procedure to close both public and origin billing admission
with a fixed 503 body. Drain peer keepalive connections normally; do not kill
connections or replace admission gates. Backup jobs and other database clients
must be idle. The coordinator rejects remaining local port-3000 sockets and
unrecognized PostgreSQL client sessions.

Validate and run once, using a new private evidence directory:

```sh
scripts/lmm-api-deploy.sh shared-postgres validate \
  --plan /private/plan.json --plan-sha256 PLAN_SHA256
scripts/lmm-api-deploy.sh shared-postgres run \
  --plan /private/plan.json --plan-sha256 PLAN_SHA256 \
  --work /private/new-maintenance-evidence --confirm CONFIRMATION \
  --execute-migration
```

The coordinator holds the existing native global, standalone and frontend
deployment locks on every node. A separate systemd guardian receives their open
file descriptions with `SCM_RIGHTS`; controller or SSH EOF does not release
those locks. This is a supervised process, not a reboot-proof fence. A lost
guardian, reboot, service/environment change or input hash change blocks work
and requires a new operator review.

It stops each old service with ordinary `systemctl stop`, verifies the actual
PID/cgroup/port drain and journal-bound refund/flush completion, and establishes
the common database identity. The owner takes a full database custom dump,
including other application schemas. It copies that dump into private local
evidence, performs an actual `pg_restore --clean --if-exists --single-transaction`
in a new isolated PostgreSQL instance, and compares all application-table row
counts and the selected financial fingerprints.

The clone has a new mount/network/PID/user namespace, private loopback only,
synthetic credentials, no host HOME or credential mounts, and no HTTP provider
or background service process. It runs the actual candidate `migrate --apply`,
candidate `--verify`, and every captured old binary `--verify`. The production
apply is armed only after that succeeds and all stopped-node/barrier/baseline
checks pass again. Production uses the normal ordered service EnvironmentFiles;
`NODE_TYPE=master` is applied after them. The effective database identity is
rechecked inside that exact oneshot process before executing the binary. An
ambiguous or failed apply is never dispatched again.

Financial fingerprints cover the immutable unit options, configured model/tool
prices and ratios, group ratios and fee/moderation policy options, plus user
`quota`/`used_quota` and token `remain_quota`/`used_quota`. They do not claim a
byte-for-byte audit of every ledger table or PostgreSQL role/global objects.
Full restored row counts and real old-version migration verification are
additional independent gates.

On success, all old binaries pass production schema verification. The original
old services restart normally and every node must become healthy with its
captured version and unchanged binary. Only then are all three maintenance
locks released, by stopping each guardian and closing the SSH agents. No native
lease or transaction state is deleted. **Ingress stays closed.** Continue the
normal deployment procedure with a fresh release/plan ID: package-owned nodes
use native stage/promote, complete observation and confirmation; standalone
nodes use the default migration-verify stage/apply/confirm. Maintain all normal
N-1 inputs, migration and rollback gates. Public and each-origin verification
and normal peer restoration remain required.

## Failure and recovery

Keep every failed evidence directory and the closed ingress. Do not repeat
`run`, hand-edit state, force-confirm, or automatically restore the dump.
The retained guardian JSON identifies the ordinary systemd lock-holder unit.
Private logs can contain connection diagnostics; keep them private.

When the all-stopped baseline exists, inspect the actual apply outcome first.
Ensure the original agents have exited. If a previous recovery partially
restarted a writer, stop that service normally first; the next attempt must
prove its fresh PID/InvocationID shutdown and drain. Use a new recovery ID and
new controller evidence directory:

Recovery reads the complete PID/Invocation journal, retains its SHA256, and
validates the unique received-signal through server-exited window. Earlier
periodic quota flushes are not shutdown reports; duplicate shutdown/refund/flush
reports, missing completion or a different PID/Invocation remain failures.
Like the normal native gate, an empty dashboard batch may emit no flush report;
zero or one valid report is accepted, and no `persisted=0` report is invented.

```sh
scripts/lmm-api-deploy.sh shared-postgres recover \
  --plan /private/plan.json --plan-sha256 PLAN_SHA256 \
  --work /private/new-recovery-evidence --recovery-id recovery-002 \
  --confirm CONFIRMATION
```

Recovery does not replay apply or restore. It verifies the effective database
identity, closed barriers, original input/environment hashes, unchanged
financial fingerprints and the actual old binary on **every** stopped node
before any restart. Candidate verification is recorded; an un-applied old
schema may reject candidate additions while still accepting the old binary.
Only verified old services restart. All must be healthy before the guardian
locks release. Failed attempts remain immutable and a subsequent reviewed
attempt uses a different ID. Ingress remains closed even after recovery.

If failure precedes the common stopped baseline, this command deliberately
refuses recovery. Review whether any stop/apply was dispatched and inspect the
real unit/journal state. When proof establishes that no database migration ran
and the untouched original writers remain healthy, the operator can stop the
recorded guardian units normally. An incomplete shutdown, unknown apply
outcome, lost lock owner, money drift or incompatible old schema requires an
explicit reviewed recovery using the verified backup and the ordinary release
tools; it never authorizes manual transaction-state edits.

## Verification

The mandatory qualification harness runs the offline failure suite:

```sh
python3 -B scripts/test-deploy-shared-postgres.py
python3 -B scripts/test-deploy-systemd.py
```

Run the opt-in real isolated fixture with already verified executable artifacts.
The first rollback binary creates the old synthetic schema. The candidate must
initially reject it, so this test proves an actual schema upgrade rather than
an apply that did nothing. The fixture also includes a second schema.

```sh
python3 -B scripts/test-shared-postgres-real.py \
  --candidate /verified/candidate \
  --rollback /verified/old-node-a --rollback /verified/old-node-b \
  --work /private/new-real-pg-evidence
```

This proves actual full restore and candidate/N-1 migration compatibility in
synthetic isolated PostgreSQL. It does not prove production SSH, systemd,
admission closure, HTTP readiness or final release deployment. Repeat it with
the final candidate artifact; evidence from an earlier candidate is not final
release proof.
