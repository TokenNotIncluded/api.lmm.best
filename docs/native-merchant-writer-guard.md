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

The source supports this exact read-only, sealed held-owner startup command:

```text
/usr/bin/lmm-api operator production writer-start-check \
  --workspace /var/lib/lmm-api-go-deploy/work/<deployment_id>
```

An operator-managed `ExecStartPre` can use it only after the complete loaded
command is captured into a fresh immutable startup seal; arbitrary commands,
shells, extra flags and ignore-error hooks remain rejected. The command executes
actual installed status and database checks and requires a still-live durable
owner both before and after qualification. ACTIVE continues blocking activation
between its exit and ExecStart, including a holder crash in that interval.

This is not a permanent standalone restart policy. After normal confirmation the
ordinary holder releases its owner. A manual/automatic post-confirmation restart
therefore needs a newly reviewed holder binding; a stale completed receipt is
rejected. Do not install a permanent drop-in or call a generic once-only status
probe a restart fence. A separate per-start holder or serve-lifetime protocol
needs its own source and real systemd qualification before such automation.

The existing ordinary native plan remains host-bound to Arch's native package
workflow. Ubuntu/shared-PG wrappers must carry an independently reviewed sealed
host/artifact/effective-environment binding and call the same guard before every
start/recover/reopen. This source does not silently accept an Arch plan on Ubuntu
or retrofit an old financial guardian. No production helper unit, service,
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
