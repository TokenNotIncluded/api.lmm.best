# Build once, deploy an existing release

Go and Web are separate release components. A source merge, a successful build,
a signed release, and a completed production deployment are different states.
Do not rebuild both components just to publish a frontend change.

## Read the live state first

Before choosing a path, spend one minute on read-only facts. It is faster than
discovering a half-finished transaction halfway through a new one:

```bash
curl -s https://api.lmm.best/api/status | jq -r .data.version   # served Go version
ssh ArchDmit   'readlink -f /srv/lmm-api-frontend/current; systemctl list-units --no-legend "lmm*"'
ssh DmitUbuntu 'readlink -f /srv/lmm-api-frontend/current; systemctl list-units --no-legend "lmm*"'
gh run list --workflow deploy-web-frontend.yml --limit 3
```

Anything besides `lmm-api.service` (and Ubuntu's cluster tunnel) — for example
a transient `lmm-merchant-portable-*` writer-capsule holder — belongs to an
unfinished backend transaction. Do not start another backend deployment and do
not stop it to get unstuck; resolve that transaction first. A Web-only release
does not touch it.

## Frontend-only update

Check that the new frontend works with **both active Go backends** first. Changes
that require a newer backend must use the combined signed transaction instead.
The existing `release-web.yml` workflow builds, checks the artifact, signs, and publishes the
archive. It runs manually against an immutable `web-vX.Y.Z` tag; a branch is not
a release identity. Tests run locally; Actions does not repeat them or wait for
test CI. Keep signature, ancestry, artifact and production acceptance checks.

### One command

```bash
# Run selected local checks once and retain their command, exit code and logs.
python3 scripts/local-release-tests.py run --component web --output /private/web-tests.json \
  -- bun run --filter @lmm/web test
export LMM_LOCAL_TEST_EVIDENCE=/private/web-tests.json
just ship-web            # next patch version, or: just ship-web web-vX.Y.Z
just release-web         # same, but stop after the signed release is published
```

Choose the local checks appropriate to the change; the record lists what actually
ran and does not claim every suite passed. To reuse tests already completed, use
`local-release-tests.py import --component web --revision TESTED_SHA --output
/private/web-tests.json --command 'completed command' --exit-code 0 --stdout
/private/completed.log` (and `--stderr` when recorded separately). Import explicitly
records the operator's observed exit code; it does not execute or invent a test.
Retain the original logs. Do not import incomplete or unknown outcomes as success.
The Go path uses the same entry point with `--component go`.

`web ship` fetches `origin/main`, picks the next patch tag (above both the
latest `web-v*` tag and the AUR `pkgver`), refuses when nothing under
`apps/web`, `packages`, `package.json` or `bun.lock` changed since the last
tag, and verifies the local test record before creating anything. The record
binds the component's Git objects, so a merge or unrelated source change can reuse
the same completed tests; any changed component source rejects the record. It
then creates a signed annotated tag with your local Git signing key, pushes
it, dispatches `release-web.yml`, watches that exact run, dispatches
`deploy-web-frontend.yml` once and watches it. Each dispatch happens at most
once; on failure it prints the run ID and stops. The same evidence JSON is a
required `local_test_evidence` input to either component release workflow.
The publisher checks it against its immutable tag checkout, then only builds,
signs and publishes. Test/review workflows are manual diagnostics, with no
push, pull-request, tag, merge-group or scheduled triggers.

### Step by step

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

Run `just test-deploy-entrypoint` (it runs `scripts/test-deploy-entrypoint.py`
and `scripts/test-web-ship.py`).
The tests use fake local commands and a loopback HTTP server, never production
services. They cover argument validation, build reuse, failure propagation,
transient downloads and revision/hash rejection. The standalone deployment CI
also retains the existing state-machine and recovery tests.
