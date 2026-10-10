# Deployment is separate from the Go API

`lmm-api-go` no longer implements `operator` or `deploy`. The scripts do not
resolve a backend provider to run deployment operations. Existing native safety
code has moved, not been replaced by a second simplified implementation.

## Build and invoke

```sh
bun run build:go
bun run build:deploy
bash scripts/lmm-api-deploy.sh --help
bash scripts/lmm-api-deploy.sh frontend --help
```

The build outputs are `apps/api-go/out/lmm-api-go` (server) and
`apps/api-go/out/lmm-api-deploy-engine` (deployment tool). The repository script
selects the local tool, then `/usr/lib/lmm-api-deploy/engine`. An explicit
`LMM_API_DEPLOY_BINARY` selects a reviewed tool file. A missing or invalid
explicit path fails; it never falls back to a backend. `LMM_API_PROVIDER_BINARY`
is no longer a deployment setting.

`just package-go` requires the existing marker-owned `LMM_API_BUILD_WORKSPACE`.
It bootstraps only the separate tool when missing; the native build operation
then creates both backend and tool artifacts and the frontend once. Web-only
commands continue to use GitHub CLI/Python without building either Go program.

The installed public script is `/usr/bin/lmm-api-deploy`; its only native target
is `/usr/lib/lmm-api-deploy/engine`. Service start, explicit database migrations
and HTTP probes continue to use the backend. Installing a new source tree alone
does not update the installed scripts, service or either executable.

## Signed packages and retained recovery

The official Go publisher signs a bundle containing both executables and the
matching public script. New packages install both files. The native controller
checks archive/package byte parity, file types and permissions, and an independent
`deploy_engine_sha256`. Pass the verified backend as `--probe-binary` and the
verified tool as `--operator-binary` when creating a modern release plan.
A backend cannot satisfy the tool binding. Missing, changed or symlinked tool
payloads are rejected before proceeding.

Previously signed packages do not contain a separate tool. Reconstruct them
only from their original signed bundle; do not add the current wrapper or tool
to an old rollback package. Old immutable plans and retained recovery processes
keep their exact historical operator identity. The separate tool accepts the
private `operator` protocol prefix for those command records, but the new API
does not. This compatibility path is not an automatic fallback for new plans.
Do not rewrite original manifests, hashes, locks or recovery acknowledgements.

## Standalone systemd transactions

After checking the host and obtaining reviewed compatible artifacts:

```sh
sudo bash scripts/lmm-api-deploy.sh systemd upgrade \
  --release UNIQUE_TRANSACTION_ID \
  --binary /private/reviewed/lmm-api-go \
  --deploy-engine /private/reviewed/lmm-api-deploy-engine \
  --frontend /private/reviewed/frontend \
  --confirm api.lmm.best
```

`--deploy-engine` is a preparation-only option for `stage` and `upgrade`. Without
it, preparation requires the fixed installed tool. It checks the machine
capability response before stopping a service, stages a private copy, records
its hash and rechecks it on apply/confirm/rollback. Existing transaction records
without this field retain their original pinned recovery behavior. `doctor`
and `status` stay read-only and do not require execution of a tool.

Shared-PostgreSQL native capsules validate and retain both signed executables.
Per-start hooks additionally require the matching signed tool at the fixed
installed path. A standalone host using these hooks must install that exact
signed tool through its reviewed installation procedure before activating a
new capsule. A private staged copy alone does not satisfy the installed-hook
requirement. An absent or mismatched tool blocks the operation; do not bypass
the check or use ordinary systemd deployment to bypass native writer ownership.

This separation does not provide zero-downtime upgrades, authorize DDL, remove
connection drain, relax signatures, or introduce automatic confirmation or
rollback. Production rollout still requires its own explicit authorization,
installation review, schema/restart rehearsal and public acceptance checks.

## Local validation

```sh
(cd apps/api-go && go test ./internal/appcli ./internal/deploycli -count=1)
(cd apps/api-go && go test -race ./internal/appcli ./internal/deploycli -count=1)
(cd apps/api-go && go test -run '^$' ./...)
python3 -B scripts/test-deploy-entrypoint.py -v
python3 -B scripts/test-deploy-systemd.py -v
python3 -B scripts/test-deploy-systemd-cli.py -v
bash scripts/check-deploy-isolation.sh \
  apps/api-go/out/lmm-api-go apps/api-go/out/lmm-api-deploy-engine
```

The executable smoke test uses temporary frontend files only. Isolated command,
archive and script tests do not prove a production upgrade, database migration,
real systemd lifecycle or full `makepkg` build. Keep those results distinct.
