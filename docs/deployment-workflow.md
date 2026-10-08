# Build once, deploy an existing release

Go and Web are separate release components. A source merge, a successful build,
a signed release, and a completed production deployment are different states.
Do not rebuild both components just to publish a frontend change.

## Frontend-only update

Check that the new frontend works with **both active Go backends** first. Changes
that require a newer backend must use the combined signed transaction instead.
The existing `release-web.yml` workflow builds, checks, signs, and publishes the
archive. It runs manually against an immutable `web-vX.Y.Z` tag; a branch is not
a release identity. Keep all required release checks.

From a workstation with an authenticated GitHub CLI:

```bash
# Use an existing, fully published signed release; substitute its actual tag.
just deploy-web web-vX.Y.Z

# List runs, then inspect the exact run created for that tag.
bash scripts/lmm-api-deploy.sh web list
just deploy-web-status RUN_ID
just deploy-web-watch RUN_ID
```

Without Just, run `bash scripts/lmm-api-deploy.sh web deploy TAG`, `web status
RUN_ID`, or `web watch RUN_ID`. These commands need neither a compiled Go
provider nor Go/Bun/Rust build tools. They do not read a server SSH key locally.
`LMM_API_GITHUB_REPOSITORY=owner/repo` selects another repository. Deployment
always uses its `main` workflow, not an arbitrary branch supplied by a caller.

A successful dispatch means **requested**, not **deployed**. Record the run ID.
`status` shows its current state; only a completed successful run confirms the
workflow passed. `watch` waits for that exact run and returns failure when it
fails. Use `status` when the credential type cannot use GitHub CLI watch.

The workflow downloads the existing archive, verifies its hash, source revision
and signature, publishes to the API origin followed by the public ingress, then
compares the public `index.html` hash with the release. It never rebuilds or
restarts the backend. Check affected UI flows after the workflow succeeds.

### Production configuration

These GitHub Actions variables can be set on the `production` environment (or
repository). Current host defaults remain for existing installations:

| Variable | Purpose |
| --- | --- |
| `LMM_WEB_API_HOST`, `LMM_WEB_API_PORT` | API origin DNS name/IPv4 address and SSH port |
| `LMM_WEB_INGRESS_HOST`, `LMM_WEB_INGRESS_PORT` | Public ingress DNS name/IPv4 address and SSH port |
| `LMM_WEB_PUBLIC_ORIGIN` | HTTPS origin used for the final content check |

A host change also requires matching `LMM_WEB_DEPLOY_KNOWN_HOSTS` and the existing
restricted deploy-key setup. Do not disable host-key verification or replace the
forced command with unrestricted SSH. The deployment topology remains two
ordered origins. A different topology needs a reviewed rollout change.

Only failed artifact GETs are retried. A transient signature-download error no
longer discards a successfully downloaded archive. A missing asset, a bad hash,
a bad signature, or a revision mismatch blocks publication.

**Do not blindly rerun a failed or interrupted deployment.** The first host may
already have switched, and the remote forced command rejects an existing release
ID. Inspect both hosts through the approved operator path; complete or roll back
the recorded transaction explicitly. Do not delete release directories to force
a retry. See [server operations](server-ops.md).

## Backend updates

Use the workflow that owns the existing installation. Do not turn a package-owned
server into a standalone installation to bypass its checks.

For standalone systemd, use `systemd doctor`, then stage/apply or `upgrade`,
verify the changed functions, and explicitly `confirm`. Keep the previous binary,
frontend and database recovery evidence. See [the standalone guide](manual-systemd-deployment.md).

For package-owned servers, use the installed `/usr/bin/lmm-api-deploy production`
workflow with its immutable signed plan. See [signed upgrades](seamless-upgrades.md)
and [production transactions](production-release-transaction.md). These native
checks remain in place; this change does not replace database migration or
recovery logic. Rust remains an explicit preview, not a deployment default.

## Local package development

`just package-go` uses `scripts/lmm-api-deploy.sh package`. Set
`LMM_API_BUILD_WORKSPACE` to the existing marker-owned workspace first. The command
reuses the selected/local/installed provider; a fresh checkout without a provider
bootstraps only the Go CLI. The native build then builds the frontend and backend
once. It no longer runs `just build` before repeating both builds.

This is the legacy local package path, not a shortcut around signed production
releases. Use a reviewed, compatible operator. When native operator code changes,
run `just build-go` once before packaging; an existing binary is not automatically
proof that it matches new source. A cold bootstrap still compiles the CLI before
the final versioned binary. An explicit but missing `LMM_API_PROVIDER_BINARY`
fails rather than silently selecting another program.

## Verify changes to this workflow

Run `just test-deploy-entrypoint` or `python3 -B scripts/test-deploy-entrypoint.py -v`.
The tests use fake local commands and a loopback HTTP server, never production
services. They cover argument validation, build reuse, failure propagation,
transient downloads and revision/hash rejection. The standalone deployment CI
also retains the existing state-machine and recovery tests.
