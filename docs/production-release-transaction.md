# Release acceptance and rollback workflow

Status: preparation on draft PR #352. This change repairs the existing native
package-deployment workflow. It does **not** connect the physical A/B slots to the
native service/traffic switch, and it is not evidence that production uses A/B.
The remaining A/B blockers in `production-ab-preparation.md` still apply.

## One transaction owns acceptance

The shared deployment action used by the Go and Web release workflows now runs:

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
public acceptance gives the workflow a zero exit code.

The native controller remains responsible for signatures, host identity,
package integrity, the global deployment lock, billing drain, single-writer
ownership, migrations, schema compatibility, observation and health gates.
The workflow does not restore the database or weaken a native rollback refusal.

## Failure and uncertainty

A settled `ROLLBACK_REQUIRED` activation or failed public acceptance while
`AWAITING_CONFIRMATION` triggers one native rollback request. The workflow
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

Runner cancellation or power loss does not magically execute Python cleanup.
The native target workspace remains the recovery authority. The initial result
marker means "recovery required" until a terminal result replaces it. A host-side
A/B boot/recovery mechanism and production-shaped crash tests are still needed.

## Evidence and workflow boundaries

The action retains only six allowlisted result fields: deployment ID, plan
SHA-256, expected backend version, observed native status, outcome and reason.
The private result file lives outside the deleted temporary controller folder;
it is atomically replaced and uploaded with a 14-day retention period. Do not
upload the controller folder, SSH keys, environment, database backups, or raw
native stdout/stderr. A cancelled runner can leave only the initial marker;
that is not a completion receipt.

The separate `workflow_run` fallback no longer deploys old releases lacking the
shared action. It fails without loading production credentials. Release jobs
that already own their deployment action are still skipped by this fallback.
This does not rewrite historical tagged workflow files or revoke their ability
to be manually rerun; production environment policy remains relevant.

No new workflow is added. The offline regression suite is a mandatory step in
the existing `Server release qualification` harness and is rerun by the shared
deployment action before it loads production credentials.

## Local validation

```sh
python3 -B scripts/test-production-release-transaction.py
bash -n scripts/auto-deploy-production-release.sh
node --test --test-name-pattern='shared deployment|deployment guard' scripts/workflow-topology.test.mjs
```

29 test methods pass, including real local subprocess fixtures with paths
containing spaces, successful confirmation, failed public acceptance, native
rollback failure, ambiguous responses, stale evidence and workflow wiring.
These are offline controller tests, not a real SSH/server migration or A/B
traffic-switch acceptance test. The 16 selected existing Node context/action
guard tests also pass locally. The topology assertions for the retired fallback
and relocated public check were updated rather than removed; the complete
repository topology suite still requires remote full-source CI.
No tag, release, production deployment or database operation was performed.
