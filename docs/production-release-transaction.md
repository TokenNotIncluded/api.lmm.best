# Manual release acceptance and rollback

Server deployment is manual. GitHub Actions only builds, tests, signs, and
publishes artifacts; it does not connect to production. The local
`scripts/production-release-transaction.py` wrapper remains available to an
operator controlling the native CLI and a verified immutable plan.

## One transaction owns acceptance

The operator runs the following transaction locally:

```text
verify signed candidate and previous Go/Web artifacts
  -> native plan and stage
  -> native promote (one dispatch)
  -> read-only reconciliation of the immutable deployment ID and plan digest
  -> public backend version, entry pages and referenced assets
  -> native confirm
  -> read-only verification of CONFIRMED
```

A Web-only release verifies the unchanged Go version too. Public checks run
before native confirmation while the local controller plan is still available;
they are no longer a separate step after the deployment script has deleted its
temporary workspace. Only a matching native `CONFIRMED` result plus successful
public acceptance gives the local wrapper a zero exit code.

The native controller remains responsible for signatures, host identity,
package integrity, the global deployment lock, billing drain, single-writer
ownership, migrations, schema compatibility, observation and health gates.
The wrapper does not restore the database or weaken a native rollback refusal.

If a durable merchant holder becomes ready after promotion fails with
`FAILED_PREARM`, do not stop it or replay promotion. A verified signed recovery
provider can close the original target transaction explicitly:

```sh
sudo /absolute/verified-recovery/lmm-api-go operator production rollback \
  --workspace /absolute/original/target-workspace --reason late-holder-prearm-recovery
```

This target-native recovery is separate from the old controller plan's pinned
candidate. It reacquires only the original workspace's transaction lock and
requires the unchanged signed N-1 process to predate workspace creation. It
rechecks packages, startup, environment, schema, frontend, ingress, authenticated
business probes and the original holder/session/nonce. It records that ingress
is still open (`admission_closed=false`), then releases the original owner by
CAS and returns `ROLLED_BACK`; it does not drain, stop, restart or install a
provider. Missing evidence or drift retains the original failed phase and lock.
An uncertain CAS response requires read-only reconciliation before any further
action. Holder readiness is bounded to 120 seconds and honors cancellation.

## Go upgrades with an unchanged database schema

The native CLI supports an explicitly sealed `verify-existing` schema mode for
a Go binary change whose candidate and rollback artifacts have the same route
contract. The route contract is necessary but is **not** a database schema
revision. A separate PostgreSQL contract binds the cluster system identifier,
database name/OID, schema name/OID, and a digest of the observed catalog
definitions, ownership and ACLs. It includes columns, defaults, constraints,
indexes, sequence definitions and ownership, policies, triggers, routines,
types, views/rules, partition metadata, extension metadata and default ACLs.
Live business rows and sequence positions are excluded so normal user activity
does not invalidate the plan. The native checks inspect this catalog only in
an explicit read-only transaction.

Capture the contract with the qualified, signed candidate's native operator on
the target; an older installed CLI may not implement this command:

```sh
sudo /absolute/verified-candidate/lmm-api-go operator production schema-contract --schema public
```

Save that JSON privately, without editing it, and compute its exact SHA-256.
The controller requires a canonical JSON file owned by its current operator,
mode `0600`, without symlinks or hard links. Add these parameters to the normal
signed-artifact plan command:

```text
--schema-mode verify-existing
--schema-contract /absolute/private/existing-schema.json
--schema-contract-sha256 EXACT_SHA256
```

The contract and mode become part of the immutable plan digest (plan format 7)
and the target manifest (format 9). Target apply, recovery and confirmation
re-read the already-staged plan and bind the exact package tuple; they cannot
change mode with an unsealed target flag or downgrade a new manifest to old
apply semantics. A missing contract, schema identity mismatch, catalog drift,
unchanged Go artifact, maintenance handoff, or incompatible route contract
rejects this mode.

Stage and preflight check the actual catalog. Before live mutation, the signed
candidate and installed N−1 both execute `migrate --verify`. After normal
billing drain and verified writer shutdown, both execute `--verify` again;
`migrate --apply` never runs in this mode. Verification children receive
read-only PostgreSQL startup parameters in both the DSN and `PGOPTIONS`, and
inherit neither historical financial-transition variable. The running service
remains writable for ordinary user requests. Its loaded systemd environment,
the private environment file, and its actual PID generation must enforce
`LMM_DB_MIGRATION_MODE=verify` and have no financial-transition request. A loaded
unit with unsealed additional environment files, unchecked environment overrides,
or custom start/stop lifecycle commands is rejected before start or shutdown.

Hosts using root-owned tool/cluster environment sidecars can capture an explicit
startup seal with `schema-contract --schema NAME --seal-startup`. The optional
`startup_sha256` and `signed_unit_sha256` fields remain inside the canonical plan
and manifest contract. The native reader preserves the actual systemd order of
repeated `EnvironmentFiles` records and accepts omitted unset array properties;
it never treats a duplicated scalar property as valid. Every sealed EnvFile is
a stable root-owned `0600` regular file without symlinks or hard links. The first
file must be the canonical Go environment. Sealed same-schema installation and
rollback preserve its exact bytes instead of replaying optional environment
hardening; the historical default path retains its hardening behavior.
Current and future startup database identities are both checked read-only.

An optional sealed readiness pair accepts only the exact existing curl GET to
`http://127.0.0.1:3000/api/livez`, with proxy bypass and output to `/dev/null`,
followed by `sleep 2`. Its complete argv and order are sealed; shells, other
URLs, redirects, bodies, output files and arbitrary lifecycle hooks are refused.
The root-owned `0644` loaded unit digest must also match the unit payload in
both Sigstore-qualified candidate and N−1 packages before the plan authorizes
mutation, and target apply repeats that binding. Command runtime timestamps
and PIDs do not enter the startup seal. `GOMEMLIMIT` remains governed by the
existing signed memory-drop-in validation and recognized override retirement.
No other loaded environment assignments escape the startup digest. Changes to
sidecar bytes, file order, PostgreSQL overrides or command semantics invalidate
the sealed policy. Without these optional seal fields, the original single-file,
no-lifecycle-hook policy still applies.
The inspected process's effective search path is verified read-only; when
stopped, the same check uses the loaded unit and private environment file in
their real precedence order before restarting. It binds the effective cluster,
database and schema identity, including PostgreSQL environment overrides.
An explicit `LOG_SQL_DSN` must be exactly the main connection and receives the
same child read-only fence. Independent log databases require a separate
qualified contract and are rejected by this mode. Schema and startup invariants are checked again during
rollback, observation and final confirmation.

This mode preserves signature verification, package checks, controller backups,
locks, billing drain, writer ownership, rollback and the 120-second observation
gate. It does not seed missing built-ins or repair a changed schema: either
provider's actual verification failure stops activation. Database-changing
releases still use the original apply path. Omitted mode retains historical
plan format 6 / manifest format 8 semantics; older plans remain recoverable.
Operators and candidate packages lacking this native feature cannot be used as
if they supported it. Source tests are separate from official artifact and
production qualification.

## Failure and uncertainty

A settled `ROLLBACK_REQUIRED` activation or failed public acceptance while
`AWAITING_CONFIRMATION` triggers one native rollback request. The wrapper
re-reads the state immediately before rollback and requires the final native
state to be `ROLLED_BACK`. A recovered failed release still exits nonzero.
A successful command that returned `CONFIRMED` instead of `ROLLED_BACK` is not
accepted as rollback evidence.

A lost promotion or rollback reply is reconciled with read-only status calls,
not by repeating a mutation. In-flight phases are polled for a bounded interval.
Unknown phases, malformed JSON, mismatched deployment IDs/plan digests/versions,
unavailable status and incomplete native rollback all fail closed. The last
known phase is never reused as current evidence after transport is lost.

A failed confirmation response may hide a still-running remote confirmation.
The controller only reconciles it. It must not race confirmation with rollback.
An unresolved confirmation therefore leaves `recovery-required`, not success.
Native `CONFIRMED` is terminal; this wrapper cannot roll back a previously
confirmed release. Selecting and activating the previously confirmed A/B pair
is still a separate native integration blocker, not implemented here.

Controller interruption or power loss does not magically execute Python cleanup.
The native target workspace remains the recovery authority. The initial result
marker means "recovery required" until a terminal result replaces it. A host-side
A/B boot/recovery mechanism and production-shaped crash tests are still needed.

## Evidence and automation boundary

The local wrapper writes a private, atomic result file with the deployment ID,
plan digest, expected version, native status, outcome, and reason. Keep that file
outside temporary controller directories. Preserve recovery evidence after an
interrupted operation; a nonterminal receipt is not success.

No GitHub workflow invokes the Go deployment wrapper: backend deployment stays
operator-controlled, and no workflow holds a credential that can reach the
backend CLI. Offline regression tests still run in the isolated server
qualification workflow; these tests do not access production.

The single exception is frontend-only: an operator may manually dispatch
`deploy-web-frontend.yml` for a signed Web release after verifying compatibility
with both active Go backends. Its key is restricted to
`/usr/local/sbin/lmm-web-deploy` on both origins, which can only run `frontend
publish` for a new release id. Web changes requiring a new Go backend use the
native combined transaction. The key cannot invoke this wrapper, the backend
CLI, or any other command.

## Local validation

```sh
python3 -B scripts/test-production-release-transaction.py
node --test scripts/workflow-topology.test.mjs
```

Tests cover successful confirmation, failed public acceptance, rollback failures,
ambiguous transport, stale evidence, and the absence of workflow server access.
They do not prove a real server migration or production traffic switch.
