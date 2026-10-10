# Billing-safe backend activation and recovery

Go activation now installs a transaction-owned 503 barrier in the existing
`/etc/nginx/lmm-api-locations.conf` (root-owned regular file, mode 0644).
It affects only servers including that LMM-specific file. It does not stop or
restart nginx and does not alter independent 9002/8317 configurations. No HTTP
API route, API route contract revision, or release signature constraint changes.

The CLI saves the exact original locations bytes privately, records their hash
and a sequence in `deployment.json.billing_gate`, installs the barrier, runs
`nginx -t`, and reloads nginx. Before stopping Go it requires an actual public
`/v1/models` probe to return both 503 and the exact deployment-ID marker, and
requires all non-listening/non-TIME_WAIT sockets on the verified loopback Go
port 3000 to drain. Existing generation can finish naturally; a five-minute
deadline leaves the backend running and fails the transaction instead of
force-killing it. A CDN or error-page body rewrite fails the marker check.
This assumes the package-owned loopback backend is reached through the verified
LMM edge; additional local writers must be quiesced independently.

Only then does the CLI stop Go. It records the live PID and systemd InvocationID
before dispatch, and verifies MainPID=0, matching ExecMainPID, inactive/dead,
Result=success, normal exit code/status, empty cgroup and absence of the old PID.
It reads the PID/invocation-bound shutdown journal from the stop boundary,
requires the signal and exit markers, rejects error/timeout/batch-failure output
and incomplete batch start/finish pairs, and records the journal hash. Missing
or ambiguous evidence blocks migration. Go 0.2.17 has a void FlushBatchUpdates:
these checks do not invent a durable success receipt or recover already-lost
legacy in-memory data. Any failure requires reconciliation before proceeding.

Legacy gopool refunds are not covered by HTTP/loop shutdown. Both before stop
and after exit the CLI reads the whole invocation's journal (bounded to 16 MiB,
fewer than 20,000 records), requires retained pre-listen startup and readiness
markers, and audits journald for known suppression/loss in that window. Missing
history, truncated/invalid output or a single `请求失败, 返还预扣费` intent blocks
activation: no amount of sleeping proves that refund completed. Only zero
intents in the retained history can pass this legacy-path gate. This is
observational evidence with the system journal as its authority, not an invented
refund completion receipt; manually deleted or otherwise untrustworthy journals
cannot be used as absence proof.

After the new backend passes its local health and restart checks, the CLI
restores the exact original locations bytes, tests/reloads nginx and rechecks
the hash. Managed edge-policy installation happens after that restoration,
so it cannot overwrite or prematurely remove the active barrier. Normal public
release probes still gate observation and confirmation.

Rollback closes the same LMM barrier and verifies writer shutdown first. Before
installing an old package, it queries the recorded PostgreSQL schema. Any
managed subscription record (including settled/refunded), malformed result,
missing table or unavailable database blocks old-writer activation and retains
`ROLLBACK_REQUIRED`, the transaction lock and the barrier. Old tables without
the added column are supported by the to_jsonb query. There is no bypass flag,
external receipt or claimed N-1 runtime compatibility. Use compatible forward
recovery for managed records; do not delete ledger rows to pass this check.

All apply/stage/promote/rollback arguments remain unchanged. Promotion still
requires the signed candidate operator and probe to be the same verified binary.
Recovery alone can use a verified installed package/link/payload when candidate
archives are damaged, preserving the existing rollback artifact contract.
Only marker-owned tests were run during development; production activation must
use a newly signed release (planned Go 0.2.19, not overwritten 0.2.18). Web 0.1.63
can be retained if its existing route contract matches the candidate.
