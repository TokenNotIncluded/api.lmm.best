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

The single exception is frontend-only: `deploy-web-frontend.yml` uses a key
restricted to `/usr/local/sbin/lmm-web-deploy` on both origins, which can only
run `frontend publish` for a new release id. It cannot invoke this wrapper, the
backend CLI, or any other command.

## Local validation

```sh
python3 -B scripts/test-production-release-transaction.py
node --test scripts/workflow-topology.test.mjs
```

Tests cover successful confirmation, failed public acceptance, rollback failures,
ambiguous transport, stale evidence, and the absence of workflow server access.
They do not prove a real server migration or production traffic switch.
