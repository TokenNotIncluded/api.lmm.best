# Native merchant writer guard

This source adds a strict ordinary Go upgrade guard. It does not deploy a
provider, grant database privileges, activate a merchant capability, or authorize
the first Go86-to-capable-provider bridge. That first transition remains a
separately reviewed operation with all incoming traffic closed.

## Artifact and status contract

Candidate and retained providers must both have capability 1 through 4, at least
the stored `MerchantStoreMinimumWriterCapability`. Capability zero, missing
floor, malformed markers, an unsupported command, and all below-floor targets
block before writer stop or package mutation. There is no frozen-only rollback
exception. Freezing new writes does not prove payment/refund callback safety.

The signed release archive and native package must contain exactly the same
`MERCHANT_STORE_WRITER_CAPABILITY` bytes (`1\n` through `4\n`). Qualification
executes the actual retained payload's `merchant-store-writer-gate status` and
accepts exactly these five JSON fields:

- `required_capability`
- `writer_capability`
- `new_writes_allowed`
- `supports_writer_gate`
- `supports_variants`

Duplicate, missing, unknown, null, contradictory and future values are rejected.
The canonical release plan seals both package SHA, executable SHA, source
revision, official asset SHA, capability and raw status-output SHA. Its writer
contract also seals physical PG system/database/schema identity, actual business
role, the ordered root-private effective EnvFiles and signed startup-unit digest.
Candidate and retained qualification use those same sealed read-only database
connections, not a privileged maintenance role or ambient shell credentials.

## Durable owner

The guardian owns one dedicated physical PG connection. It takes shared advisory
key `0x4c4d4d4150490002`, independently of the migration key ending `0001`, then
repeats both actual status and actual `migrate --verify` providers under that
fence. It never holds the migration key while executing verification.

After qualification it inserts one reserved Options row with key
`MerchantStoreDeploymentFence:<deployment_id>:<host>` and canonical compact JSON:
`format=1`, `state=ACTIVE`, deployment/host, plan/contract/provider SHA, random
nonce, holder unit/PID/systemd invocation, actual PG backend PID and physical
database/schema/business-role tuple. INSERT never overwrites an existing row.

The separate unit is `lmm-merchant-fence-<deployment_id>.service`, `Type=simple`,
`User=root`, `Restart=no`, `KillMode=control-group`. Its executable is the exact
qualified signed candidate payload retained in the root-owned workspace. It runs:

```text
<qualified-provider> operator production writer-fence hold \
  --workspace <root-workspace> \
  --release-plan <workspace>/staging/release-plan.json \
  --release-plan-sha256 <canonical-plan-sha256>
```

The root-private owner receipt and Unix socket locate this holder. Every caller
also verifies actual systemd PID/invocation/Restart policy, `/proc/<PID>/exe` SHA,
Unix peer credentials, and the holder's actual PG session/shared lock/floor and
exact ACTIVE value. A file or PID alone never grants permission. Ordinary native
drain, stop, package install, start, unchanged-provider recovery, reopen,
observation and confirmation reuse this check.

Only actual CONFIRMED or completed ROLLED_BACK state, reopened admission,
qualified installed/running target and unchanged database contract allow that
same physical session to DELETE its exact original key/value with CAS. A crash,
connection loss, invalid record or failed nonterminal release leaves ACTIVE
blocked. No expiration, sweep, upsert, generic option API, or automatic recovery
removes another owner's record. Activation takes exclusive `0002`, refuses any
casefold/Unicode-trimmed reserved-prefix row, then takes migration `0001` on the
same physical connection. Both owners release locks in reverse order.

## Startup and host boundaries

The held window uses this exact read-only, privileged startup hook, with both
paths bound to the same native workspace:

```ini
[Service]
ExecStartPre=
ExecStartPre=+/var/lib/lmm-api-go-deploy/work/<deployment_id>/tmp/migrations/merchant-store-candidate/lmm-api operator production writer-start-check \
  --workspace /var/lib/lmm-api-go-deploy/work/<deployment_id>
```

An operator-managed `ExecStartPre` can use it only after the complete loaded
command is captured into a fresh immutable startup seal; arbitrary commands,
shells, extra flags and ignore-error hooks remain rejected. The command executes
actual installed status and database checks and requires a still-live durable
owner both before and after qualification. Only the actual root ExecStartPre
ControlPID in the same service InvocationID may inspect `activating/MainPID=0`;
ordinary lifecycle checks still reject that state. The verified workspace
candidate checker handles both candidate and retained installed writers, so a
rollback does not depend on the older installed checker's startup behavior.
ACTIVE continues blocking activation between its exit and ExecStart, including
a holder crash in that interval.

Protect the complete workspace, staged evidence and extracted candidate checker
throughout this held window. They remain required until rollback completes or
the permanent portable capsule startup binding is confirmed.

This is not a permanent standalone restart policy. After normal confirmation the
ordinary holder releases its owner. A manual/automatic post-confirmation restart
therefore needs a newly reviewed holder binding; a stale completed receipt is
rejected. Do not install a permanent drop-in or call a generic once-only status
probe a restart fence. The separate capsule per-start protocol below provides
the source for permanent restarts; its actual host installation still needs
the separate qualification described there.

The existing ordinary native plan remains host-bound to Arch's native package
workflow. Ubuntu/shared-PG wrappers must carry an independently reviewed sealed
host/artifact/effective-environment binding and call the same guard before every
start/recover/reopen. The portable capsule below is that separate binding; it
does not silently accept an Arch plan on Ubuntu or retrofit an old financial
guardian. No production helper unit, service,
drop-in, package or server state was changed by this implementation.

## Qualification levels

Local source tests cover the closed protocol, canonical contracts, retained
Go86/87 actual unsupported CLI, independent disposable PG shared-lock and CAS
behavior, crash/termination/privilege-loss negatives, real local Unix-peer/ELF/PG
RPC proof, and all ordinary transaction hook positions. Historical transaction
component fixtures use a private explicit authority replacement to preserve
their billing/drain/rollback assertions; production constructors leave this
dependency nil and no flag, environment variable or serialized field enables it.
Those mocked component flows are not signed-provider or installed-unit evidence.

Before release qualification the final signed candidate must separately execute
status and candidate/N-1 actual `migrate --verify` on the fresh production clone,
and real two-process activation/deploy contention plus each host's actual helper
unit and startup/recovery path must be proven. Source-test PASS does not establish
publication, deployment, production no-DDL preservation or payment acceptance.

## Portable standalone capsule and permanent startup owner

`writer-capsule seal` derives a portable capsule only from an actual validated
`verify-existing` controller plan. It reuses the official Cosign certificate
identity, tag/main ancestry and complete archive/package verification. It does
not accept a caller's approval flag. The target repeats Cosign and complete
archive/package, signed capability, source revision, unit fragment and ELF
checks without using pacman. Candidate and retained providers must both pass
actual status and `migrate --verify` under the independent shared fence.

The capsule also contains the exact source SHA of these files as obtained with
`git show` from that verified candidate revision. Git object integrity is checked
first, and replacement refs, textconv and ambient Git configuration are disabled:

- `scripts/native-shared-pg-deploy.py`
- `scripts/deploy-systemd.py`
- `scripts/maintenance-deploy-guardian.py`

The last file is an existing transitive import of the ordinary wrapper. The
new wrapper cannot enter its financial maintenance paths. It refuses DDL,
financial replay, historical financial owner states and maintenance actions;
it replaces the old unsealed verification child with the native capsule
checker. Its hooks prove the live owner immediately before stop, install,
start, frontend publication, health/observation, confirmation and rollback.
A terminal native release additionally checks the original root-private
standalone state, installed and running explicit provider, actual service
PID/invocation, actual status, schema metadata and fixed loopback readiness.

Capsules live at
`/var/lib/lmm-api-go-deploy/merchant-capsules/<deployment-id>/capsule.json`.
The canonical capsule is root-owned, singly linked and exactly 0600. Its
artifact paths are relative and closed: `assets/{candidate,rollback}.pkg.tar.zst`,
`assets/{candidate,rollback}.release.tar.gz`, and
`assets/{candidate,rollback}.sigstore.json`. Root-private source copies reside
under `source/scripts/`. Copying these inputs is a separately reviewable host
preparation step, not performed automatically by this source change.
All extraction and state directories must be real protected ancestors and exactly
0700 at their private leaves. The checker inspects every existing destination
before creating any missing directory. Assets are exactly 0600 and single linked;
qualified provider files are exactly 0700. Installed execution additionally proves
the root-owned canonical `lmm-api -> lmm-api-go` link, safe provider ancestors and
the explicit signed ELF hash. The standalone native child has a fixed clean
environment and no alternate executable or inherited command lookup path.

The permanent service hook has exactly this shape (values must be concrete):

```
/usr/bin/lmm-api operator production writer-start --capsule /var/lib/lmm-api-go-deploy/merchant-capsules/ID/capsule.json --capsule-sha256 SHA256
```

A capsule hash cannot include its own literal hash recursively. Only the above
exactly parsed hook's self-reference is normalized to `@CAPSULE_SHA256@` while
computing the ordered startup digest. Before execution, the literal must still
match the actual canonical root-private capsule file hash. All other arguments,
commands, fragment bytes, environment values, ordered EnvFile paths/content
hashes and override checks remain sealed. The old ordinary startup parser and
historical default policy are unchanged; they do not acquire a skip option.

For reviewable preparation, `writer-capsule startup-seal` is a read-only command
that captures the actually loaded typed hook and ordered private startup files:

```
lmm-api operator production writer-capsule startup-seal --startup-capsule-path /var/lib/lmm-api-go-deploy/merchant-capsules/ID/capsule.json --startup-capsule-sha256 LITERAL_LOADED_HASH
```

The initial loaded literal may be a 64-character preparation placeholder. It
is replaced with the final canonical capsule SHA before any start. This capture
prints only host/service/path and startup/unit hashes. It does not establish a
database role, schema or financial qualification, or authorize a configuration
change. Host-specific physical identity, business role, status and schema
contracts still require actual read-only qualification. The controller derives
those exact host contracts together with the official artifact plan:

```
lmm-api operator production writer-capsule seal --release-plan PLAN --release-plan-sha256 PLAN_SHA --schema-contract HOST_SCHEMA --schema-contract-sha256 HOST_SCHEMA_SHA --writer-contract HOST_WRITER --writer-contract-sha256 HOST_WRITER_SHA --host ACTUAL_HOST --startup-policy per-start --output CONTROLLER_WORKSPACE/merchant-writer-capsule-ACTUAL_HOST.json
```

The controller may produce this private source artifact as its own user. The
host installation must subsequently make the unchanged bytes and closure root
owned with the reviewed paths and exact digests. The target checker and startup
commands themselves require root. The standalone guarded entry is invoked with
`/usr/bin/python3 -I` and the exact capsule path/hash, followed by the original
ordinary `stage`, `upgrade`, `apply`, `confirm`, or `rollback` arguments.

Each normal automatic/manual start proves the actual ExecStartPre ControlPID
and service InvocationID. It starts a separate non-restarting systemd holder
using the qualified signed candidate ELF. That holder acquires shared key
`...0002`, repeats both status/verification children, then inserts ACTIVE before
ExecStartPre returns. It stays alive until the new actual MainPID, invocation,
installed/running ELF, status, schema and readiness checks pass, and then CAS
removes only its exact original owner value on that same physical PG session.
Successful attempts permit a new invocation, including after a clean reboot.
Failures after ACTIVE leave a permanent blocker. No PID inference, reboot,
expiry, retry or missing socket deletes that blocker. Ordinary deployment may
explicitly reuse only its same capsule's live parent holder, proved over the
actual root Unix peer/session protocol.

Portable/start owner claims additionally serialize on transaction key
`...0003`, after shared `...0002`, and recheck all reserved rows within that
same INSERT transaction. This closes the check/claim race between two starts.
This narrow transaction explicitly uses READ COMMITTED even if the business role
defaults to repeatable read, so a claim waiting on key `...0003` sees a preceding
committed owner rather than an obsolete snapshot.
The claim never acquires migration key `...0001` and never clears another row.

The capsule does not permit retained Go86/87 or other unsupported/under-floor
providers. The first cap0-to-supported bridge still requires a distinct reviewed
all-ingress-closed bootstrap; ordinary startup/deployment has no legacy exception.
This source's closed known range remains capabilities 1 through 4 until the
actual centrally qualified cap5 contract is integrated.

Local owned systemd ControlPID/invocation proofs, disposable-PG start retry/crash
proofs and wrapper hook tests are component evidence. They do not establish a
production unit installation, an actual reboot, public deployment, or final
signed-candidate qualification on a fresh production database copy.
