# Fixed-credit financial coordinator

`scripts/run-credit-financial-maintenance.py` is the financial coordinator. It
does not reuse or weaken the schema-only shared-PostgreSQL coordinator. Normal
deployment tools remain owners of admission, capture, shutdown, packages,
rollback, health, transaction state and cleanup. The coordinator never edits
their state/leases, unlinks locks, fabricates a previous binary, or restores a
production database.

Publish one signed annotated `go-v0.2.82` release from the final integrated
source. Preparation and strict business service use the **same binary and
package SHA**. Preparation does not initialize business routes or background
jobs; with its sealed environment removed, that exact compatible binary is the
strict fixed-500000 runtime and the actual N-1. Old 0.2.78/0.2.81 binaries are
not post-financial rollback candidates. Both maintenance deployments require
`WebChanged=false`; retain the actual Arch web 120 and Ubuntu web 119. Web 121
can be published independently and activated through an ordinary deployment
after strict maintenance release.

## Files sealed before closing admission

The controller plan has `format=lmm-credit-financial-maintenance-v1`, a safe
`transition_id`, `transition_intent_sha256`, final 40-character `source_sha`,
`confirmation=api.lmm.best`, and immutable `{path,sha256}` bindings for `intent`,
`controller`, `provider`, `verifier`, `generator`, `fingerprint_generator` and
each `generator_helpers` module. Files are absolute canonical regular files,
single-link, without group/other write. Controller work is owner-only 0700;
all generated state, receipts, archives and logs are 0600 and fsynced.

`intent` is canonical JSON with `format=lmm-credit-transition-intent-v1`,
`transition_id`, final `source_sha`, `provider_sha256`,
`target_credits_per_usd=500000`, the exact `database` object and `writer_nodes`.
The latter is the ordered node projection of `name,ssh,hostname,
deployment_tool,service,writers,guardian_unit`. Its file-byte hash is the
transition intent hash. This preliminary workflow identity is distinct from
the business plan hash sealed after the final freeze.

Each node records those identity fields, `service=lmm-api.service`,
`writers=["lmm-api.service"]`, immutable remote `artifacts`, an immutable base
unstopped prebridge `handoff`, unstopped post `post_intent`, `prepare_config`,
`receipt_directory`, and origin `probes`. All three files must appear in
`artifacts`. The post intent is only for normal plan/staging; activation always
requires actual confirmed bridge stop evidence. It has the same stable
transition/provider/config/socket identity without a fake previous writer.
The current
production writer inventory is Arch `arch-dmit` and Ubuntu `dmit-ubuntu`, each
with its own old PID/invocation/version and master jobs in the same cgroup.
The independent Ubuntu SQLite `go-lmm-best-api.service` is outside this
inventory. An unrecognized DB client blocks the financial boundary.

The exact supported node `commands` are:

| Operation | Formal owner action |
| --- | --- |
| `guardian_start` | Start a root-preprovisioned sealed guardian unit with the base handoff |
| `guardian_inspect` | Guardian `inspect` |
| `capture`, `close`, `stop` | Normal `maintenance-capture`, `maintenance-close`, `maintenance-stop` |
| `capture_status` | Normal capture-workspace status |
| `seal_prebridge`, `seal_post` | Guardian `seal-stopped` from the actual FROZEN workspace |
| `prebridge_apply`, `prebridge_confirm`, `prebridge_stop` | Normal preparation deployment apply/confirm/maintenance-stop |
| `prebridge_status` | Normal preparation-workspace status |
| `post_apply`, `post_confirm`, `post_status` | Normal strict deployment apply/confirm/status |
| `post_retry` | Native `maintenance-retry`; standalone safe pristine staged apply |
| `writer_inspect` | Guardian `inspect-writer` with the latest stopped handoff |
| `publish_receipt` | This script's `_publish-receipt` immutable transport |
| `maintenance_release` | Normal owner release with the aggregate receipt |
| `guardian_release` | Stop the supervised guardian unit, after all owner releases |

Every command is `{argv:[absolute executable,literal args...],
timeout_seconds:1..3600}`. Review and seal the exact formal CLI invocations;
do not put shell wrappers or financial SQL in node commands. Placeholders are
`{base_handoff_path}`, `{base_handoff_sha256}`, `{handoff_path}`,
`{post_intent_path}`, `{post_intent_sha256}`,
`{handoff_sha256}`, `{handoff_output_path}`, `{all_closed_path}`,
`{all_closed_sha256}`, `{global_confirmation_path}`,
`{global_confirmation_sha256}`, `{receipt_path}`, `{receipt_sha256}`. The
guardian's stopped-seal command is:

```text
python3 maintenance-deploy-guardian.py seal-stopped
  --handoff BASE --handoff-sha256 BASE_SHA
  --workspace NORMAL_OWNER_WORKSPACE --stage prebridge|post
  --output {handoff_output_path}
```

It verifies actual stopped unit/cgroup/process and immutable capture,
environment/journal evidence under a real guardian lease. It writes a new
handoff; the stable transition/intent/provider/prepare identity never changes.
The coordinator persists each returned handoff binding before continuing.
Use the prebridge base for `seal_prebridge` and the post staging intent for
`seal_post`. Normal status proves the latter's stopped-only refinement against
the original immutable staged plan; it does not modify that plan or state.

Native confirmation uses its normal 120-second observation. Standalone
`post_confirm` and `prebridge_confirm` must include normal `confirm --wait`,
with timeout exceeding 120 seconds. These are real health observations, not
sleep-based success. Maintenance confirmation retains admission closure and
the actual compatible rollback package until aggregate release.

Preparation config for a DynamicUser service must be staged by root as
`root:lmm-credit-transition` 0640 in root-owned, non-writable ancestors that
permit traversal. The normal owner validates readability and installs its
SupplementaryGroups drop-in. Root CLI-only 0600 config is not service-readable.

`database` is the exact production connection context:

```json
{"owner_node":"arch","transport":"local_peer","os_user":"postgres",
 "peer_role":"postgres","owner_role":"lmm_api","runtime_role":"lmm_api",
 "database":"lmm_api","schema":"lmm_prod_20260802",
 "socket_directory":"/run/postgresql","port":5432}
```

Financial/verification SQL connects through local peer as postgres and then
`SET ROLE lmm_api`, so audit tables belong to the actual writer. Client-drain
inspection alone uses the peer administrator to see every DB client. Full
`pg_dump` has no schema/exclude/no-owner filters. `backup_commands` contains
reviewed `offhost_copy` and `offhost_verify`; copy receives the archive on stdin
and must safely accept repeat transport of the same bytes. Verify returns
`backup_sha256,size_bytes`. Keep verified archive copies outside cleanup roots.
The coordinator additionally publishes `full-financial-backup.receipt.json`:
`lmm-credit-financial-backup-v1` binds transition/intent/provider/source, actual
database identity, full-database/custom-format/ownership-preserved flags, archive
hash and byte size, and the original guardian/three-lock bindings. Formal
cleanup requires this exact root receipt and actual archive, including database
identity, rather than treating an arbitrary PGDMP header/hash as a full backup.

All public/origin `probes` are `{url,body_sha256}` and require exact HTTP 503
body `lmm-credit-transition:<transition_id>`. No forwarded health shortcut is
accepted as preparation readiness. `public_probes` cannot be empty.
For a node whose origin requires the public TLS hostname, bind its verified
numeric address in optional `probe_resolve_address`. The controller then uses
fixed `curl -q --resolve api.lmm.best:443:ADDRESS` probes for that node, preserving
Host, SNI and normal certificate verification without DNS or redirect fallback.
Only HTTPS `/api/status` and `/v1/models` on that hostname are supported by this
transport. The verified address must reach the node's actual admission entry;
an unrelated backend response is not evidence of the node's writer or barrier.
Public edge probes remain independent. Neither a stopped backend's loopback
port nor an origin URL with an invalid certificate can replace admission proof.

`clone` is `{database:"credit_rebase_clone_<id>",role:"lmm_api",
schema:<same production schema>,port:<private local port>}`. There is no clone
host, DSN, restore command or SQL command supplied by the plan. The unprivileged
local controller creates its own fresh cluster under `work/local-clone`, with
private Unix socket and empty `listen_addresses`; restoration keeps ownership.
It creates `lmm_api` without superuser/createDB/createRole/bypassRLS and grants
only the required `pg_control_system()` execution privilege. Clone SQL uses
`SET ROLE lmm_api`; the candidate's migration CLI connects as that role.

`regression` contains a clean committed `source_directory`, the same final
`source_sha`, explicit Go `packages` and a test `run` expression. The controller
executes `go test -p 1 ... -count=1` with GOMAXPROCS=2 and a scrubbed environment.
It never starts an HTTP server against the clone.

## Execution and late business seal

Run each action with the same immutable plan/work binding:

```text
python3 scripts/run-credit-financial-maintenance.py ACTION
  --plan /private/controller-plan.json --plan-sha256 PLAN_FILE_SHA
  --work /private/financial-work --confirm api.lmm.best
```

For native production maintenance use the explicit late-stage plan. The
capture and post workspaces are already normally staged; the distinct
prebridge workspace is reserved in `prebridge_stage.workspace`, with its full
normal target `apply_contract` sealed, but `operator`, `staged_plan` and
`stage_handoff` are all JSON null. `prebridge_staging=pending` omits exactly the
four prebridge commands and the two unavailable staged artifacts. The base
`node.handoff` is never replaced or rewritten.

Sequence: `validate → prepare → normal prebridge plan/stage → refine-prebridge
→ continue-prepare → backup → clone → seal → rehearse → apply → deploy → release`.
`prepare` first captures both old writers, closes **all** admission, publishes
the verified closure receipt, then drains/stops both old writers. It formally
seals each stopped handoff and returns at `FROZEN_AWAITING_PREBRIDGE_STAGE`.
The old writers remain stopped, admission remains 503 and the same three OFD
locks remain held. `prepare` and `resume` cannot replay that boundary.

Now securely copy the actual sealed stopped handoff to the controller's local
private input; its bytes and SHA must equal `state.stopped_handoffs[NODE]`.
Run the existing normal native plan and stage into the reserved **new**
prebridge workspace using this stopped handoff. Native stage publishes it at
`/var/lib/lmm-api-go-deploy/handoffs/STOP_SHA.json`. Keep capture, prebridge and
post as three distinct workspaces: capture has already written manifest/status
and cannot be used as an apply workspace. No transaction marker, normal state
or immutable argv is manually edited.

Fill only the reserved prebridge owner bindings in a new private seed:
`operator=WORKSPACE/staging/lmm-api` (canonical normal target link),
`staged_plan=WORKSPACE/staging/release-plan.json`, and separate `stage_handoff`
at the normal global stopped-handoff path. Use the real sealed normal plan and
stage evidence to fill their hashes and actual apply argv. Rebuild the bound
plan with the same intent. The prospective `apply_contract` includes package,
rollback, provider/probe, version, observation and edge-policy arguments in
normal native order; only handoff path/SHA use runtime placeholders. This
late-stage path requires native `WithBackups=false`; native encrypted-backup
arguments are rejected here. The coordinator's complete financial backup,
offhost verification and clone rehearsal remain mandatory.

```text
python3 scripts/run-credit-financial-maintenance.py refine-prebridge
  --plan /private/partial-plan.json --plan-sha256 PARTIAL_FILE_SHA
  --refined-plan /private/bound-plan.json --refined-plan-sha256 BOUND_FILE_SHA
  --work /private/financial-work --confirm api.lmm.best

python3 scripts/run-credit-financial-maintenance.py continue-prepare
  --plan /private/bound-plan.json --plan-sha256 BOUND_FILE_SHA
  --work /private/financial-work --confirm api.lmm.best
```

`refine-prebridge` is accepted only once at the waiting boundary. It rejects
any change to source, provider, intent, database, node inventory, base handoff,
capture/post commands, package arguments, timeout or reserved workspace. It
reads the actual root-owned stopped-handoff files, original FROZEN capture
receipt/status and normally staged release plan. It verifies the formal stop
SHA and reconstructs the complete normal target argv from that plan. The
controller writes an immutable old/new plan-hash receipt and checkpoints the
new binding itself. All later actions use the bound plan and the same work
directory. A transport failure is inspected/reconciled; neither stage nor
capture/stop is guessed successful or blindly replayed.

`continue-prepare` rechecks the three locks, their original guardian generation,
503 barriers, complete stopped-writer evidence and unknown database clients
before installing anything. It resumes only the bridge apply/confirm/stop
half; it does not dispatch financial SQL or capture/stop the old writers again.
Only then does normal preparation schema/install/confirmation run.
Both bridges are stopped again and formally sealed as the actual post-financial
N-1 before the final snapshot/backup. Exact old anchors and FX must still match;
drift fails closed without altering the fixed conversion constant.

After `clone`, export the final stopped production snapshot and the restored
clone snapshot using the reviewed private exporters. Both must explicitly
attest `snapshot_state=frozen_writers_stopped`; make new plans from these
snapshots. Provisional/unspecified snapshots cannot dispatch SQL. The clone
plan must change target identity only; canonical business source/plan hashes
must match production and every other plan field must match. The coordinator
verifies the clone's actual system identifier, DB/schema OIDs and the original
postmaster PID/start time/data directory/socket/port.

`seal` additionally takes `--business-seal PATH --business-seal-sha256 SHA`.
The seal has `format=lmm-credit-business-seal-v1`, transition ID/intent hash,
`business_plan_sha256`, `backup_frozen_state_sha256` copied from controller
state, and bindings for `source_snapshot,clone_snapshot,business_plan,
production_sql,before_sql,after_sql,backup,clone_plan,clone_sql,
clone_before_sql,clone_after_sql`.

It also binds `original_fingerprint`: original `inventory`, frozen actual
`before_result`, and `production_before_sql,production_after_sql,
clone_before_sql,clone_after_sql` plus the corresponding `_receipt` bindings.
Generate all four through the sealed whole-original-table fingerprint helper
and independent verifier. Receipts are deterministic. Inventory is captured
once before conversion and covers every original non-system table/column in
the database, including other schemas. Use the same `psql -XqAt` output format
for `before_result`. After verification first proves every declared change,
inverse projections restore only those fields and exact new declared audit
rows; every historical row, original field and table count must fingerprint
as before. The controller independently regenerates all financial/verifier/
fingerprint SQL and receipts and compares the sealed bytes before dispatch.

Rehearsal restores the complete archive before sealing, verifies clone before,
dispatches only sealed clone SQL, verifies clone after/full fingerprints,
applies/verifies candidate schema and checks the same strict compatible N-1
artifact, then runs the source regression. All three fingerprint result groups
(frozen production before, clone before/after, production before/after) remain
private immutable byte-hash evidence.

Production `apply` first validates stopped writers, unchanged guard generation
and three lock inodes, no other client, target/source/before/fingerprint checks.
It fsyncs a dispatch intent before sending exactly one atomic SQL file. Any
unknown outcome becomes AMBIGUOUS. `inspect` reads after/audits/fingerprint,
then before if necessary, and classifies APPLIED/NOT_APPLIED/AMBIGUOUS without
retrying SQL, starting writers or restoring a database. A crash after intent
but before state checkpoint is recoverable through this same exact-bound
inspect path. NOT_APPLIED still requires review; it is not automatic retry.

`deploy` uses normal stopped-owner handoff and strict local health while ingress
is closed. `release` rereads all current owner confirmations and probes all
nodes again before publishing the durable aggregate intent/receipt or opening
the first node. Only normal owners reopen/probe/finalize. Guardian units are
stopped last, with their original PID/invocation verified, never by unlinking
lock paths. An optional reviewed node `cleanup` command runs after all releases
and before guardian shutdown; it uses formal `--superseded-by`,
`--retain-rollback`, financial backup and `--execute` gates. It retains current,
compatible N-1 and financial backup, and receives `{backup_sha256}`,
`{financial_backup_receipt_path}` and `{financial_backup_receipt_sha256}`.
Cleanup holds all three real guardian lock descriptions until it finishes.

## Interrupted operations

`status` is a sanitized controller summary. `reconcile` only reads normal owner
state for an existing intent; it does not replay apply/stop/release. `resume`
uses formal NOT_DISPATCHED proofs, pending confirmation or the owner's explicit
MAINTENANCE_PREARM_FAILED retry; ROLLBACK_REQUIRED needs normal owner repair.
Already confirmed nodes are not reapplied. Successful post rollback to the real
compatible bridge is confirmed under the same strict health/120-second gates.
Partial release can observe a terminal confirmed owner after guardian shutdown.
Off-host transport interruption resumes the same immutable archive under
FULL_BACKUP_COPY_INTENT, without taking another backup. Local failed clone or
rehearsal needs review; production remains frozen and no SQL is automatically
retried. Abandoned unique checkpoint files are preserved, not guessed to be
committed state or removed to unlock progress.

Validation:

```sh
python3 scripts/test-credit-financial-maintenance.py
CREDIT_FINANCIAL_RUNNER_REAL_PG=1 python3 scripts/test-credit-financial-maintenance.py
```

The opt-in test creates two new local Unix-only PostgreSQL clusters, exercises
the complete archive restore/owned clone/role/SQL path, proves the source is
unchanged and stops both clusters in `finally`. It has no production DSN.
