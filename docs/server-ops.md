# Manual assistant server operations

`.github/workflows/server-ops.yml` in **TokenNotIncluded/api.lmm.best** is the
production operator entry. Manual `workflow_dispatch` remains separate from the
existing, fixed read-only owner-commit diagnosis described in
`docs/incident-343-owner-ops.md`. Neither PRs nor releases trigger arbitrary repairs. The safe default is `diagnose`; it does not modify services or
application configuration. The standalone entry replaces the diagnostic job previously embedded in deployment.

## Connection and authorization

The workflow reuses the `production` environment, `PRODUCTION_SSH_PRIVATE_KEY`
and `PRODUCTION_SSH_KNOWN_HOSTS`, and the same ArchDmit host/port as automatic
production deployment. It does not create credentials, disable host-key checks,
or install an inbound management service. Missing secrets fail before SSH.

Only the maintainer `LIghtJUNction` is allowed by default. The organization name
`TokenNotIncluded` is not a user login and must not be inferred as an operator. To authorize a specific
GitHub App/operator, set the repository/environment variable
`PRODUCTION_OPS_ALLOWED_ACTORS` to comma-separated **exact GitHub actor logins**.
Both the original dispatcher and a rerun's triggering actor must be authorized.
An assistant uses its connected GitHub identity; there is no separate AI bypass.
Protect `main` and the `production` environment with appropriate reviewers and
restrict production environment deployments to trusted branches. The workflow's
checks are not a substitute for those repository/environment settings, and this
change does not modify those settings.

## Use

In Actions, select **LMM assistant server ops**, **Run workflow**, branch `main`.
Supply a public, non-sensitive reason or issue reference.

```sh
# Diagnose: disk, memory, selected systemd metadata, package versions, backend
# selector, local HTTP status, and external JSON success status.
gh workflow run server-ops.yml --repo TokenNotIncluded/api.lmm.best --ref main \
  -f operation=diagnose -f reason='Investigate service availability' \
  -f timeout_seconds=180

# Example repair: validate nginx configuration, then gracefully reload it.
gh workflow run server-ops.yml --repo TokenNotIncluded/api.lmm.best --ref main \
  -f operation=repair -f reason='Apply reviewed nginx configuration' \
  -f repair_script=scripts/server-repairs/reload-nginx.sh \
  -f confirm=api.lmm.best -f timeout_seconds=180
```

The caller needs GitHub workflow-dispatch permission. A connector that can edit
files or read runs does not necessarily expose dispatch; in that case an operator
must use the UI or an authorized CLI/API. Then the assistant can inspect the run.
No credential should be pasted into chat, workflow inputs, scripts, or reports.

## Adding a repair

Commit a single UTF-8/LF Bash file at `scripts/server-repairs/<name>.sh` to `main`.
Review the intended changes, recovery plan, current deployment state, and data
impact before dispatching. The controller reads the file from the immutable
**dispatch commit**, not a moving branch, URL, or modified working-tree file.
It rejects symlinks, path traversal, missing files, invalid Bash, and files over
64 KiB; the transferred script is SHA-256 checked again on the server.

Repairs run as the existing SSH user (currently root): **they are trusted code,
not sandboxed commands**. Keep application/deployment operations in the native
`/usr/bin/lmm-api` CLI. Do not bypass its transaction locks, drain gates, signature
checks, edge policy, or explicit confirm/rollback contract. Never directly delete
transaction locks/recovery evidence, adjust customer balances, or destructively
change databases as a generic repair. A failed operation is not automatically
retried or rolled back. An existing repair run cannot be rerun; inspect the
outcome first and explicitly dispatch a new run when appropriate.

Within this production repository, repairs share the `production-auto-deploy`
concurrency group and also take a
server-side manual-ops flock. This serializes Actions deployments and this
transport; it does **not** replace the native deployment transaction lock or
coordinate unrelated root sessions. The transport itself makes no claims about
the safety or reversibility of a future repair script.

## Results, privacy, and deadlines

Actions receives selected diagnostic metadata and a summary containing actor,
commit, repair path/hash, exit code, and public health result. It does not dump
process arguments, application environment files, raw journal messages, HTTP
bodies, private repair output, or database contents.

Private repair output and audit metadata stay under
`/var/log/lmm-server-ops/<run-id>-<attempt>/` (root-only). No automatic audit-log
pruning is installed. Scripts can deliberately publish a **sanitized** report by
writing to `$LMM_OPS_REPORT`; only its first 16 KiB is returned. These reports and
workflow inputs are visible in a public repository: secret redaction cannot be
guaranteed, so never put raw configuration, tokens, or user data there. Raw repair
stdout/stderr is never uploaded, even on failure. Remote output is prefixed so
it cannot inject GitHub workflow commands.

The remote deadline is 60/180/300/600 seconds, including diagnostics and local
post-repair checks. SSH has connection/keepalive limits and the runner has an
additional deadline. Cancellation, disconnection, or timeout **does not undo
changes** and may leave the outcome unknown; the remote timeout is the fallback,
not a transaction rollback. Deliberately daemonized work is outside that timeout
contract. Repair failures remain failures even when the public endpoint is healthy.
Local checks require both services active and HTTP success; the external check
also requires `success: true`. These are availability checks, not a complete
payment, database, or release acceptance test.

The fork does not inherit upstream secrets or share its Actions concurrency.
Do not copy credentials into the fork to work around this repository boundary.

Run offline controller tests with `python3 scripts/test-server-ops.py`.
