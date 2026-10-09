# Build once, deploy an existing release

Go and Web are separate release components. A source merge, a successful build,
a signed release, and a completed production deployment are different states.
Do not rebuild both components just to publish a frontend change.

## Read the live state first

Before building or dispatching, freeze one source revision and capture a single
read-only preflight for the batch. Start with these reads:

```bash
curl -s https://api.lmm.best/api/status | jq -r .data.version   # served Go version
ssh ArchDmit   'readlink -f /srv/lmm-api-frontend/current; systemctl list-units --no-legend "lmm*"'
ssh DmitUbuntu 'readlink -f /srv/lmm-api-frontend/current; systemctl list-units --no-legend "lmm*"'
gh run list --workflow deploy-web-frontend.yml --limit 3
```

Also record each host's installed Go/Web package or standalone identity, active
frontend revision/index hash, native transaction/owner status and PostgreSQL
database/schema identity through the selected operator. Inspect the exact
release and run, not just the newest run. Compare complete component Git objects
using `scripts/local-release-tests.py`; its paths include packaging/controller
inputs beyond application source. Select backend-only, frontend-only or combined.
Refresh mutable host/owner facts at phase boundaries without restarting the
entire source, artifact and test review.

Anything besides `lmm-api.service` (and Ubuntu's cluster tunnel) — for example
a transient `lmm-merchant-portable-*` writer-capsule holder — belongs to an
unfinished backend transaction. Do not start another backend deployment and do
not stop it to get unstuck; resolve that transaction first. A Web-only release
does not touch it.

## Frontend-only update

**Select the installation path before an automatic deploy.** The current
`deploy-web-frontend.yml` uses `frontend publish` with a static archive. It does
not install or update Arch's `lmm-api-web-bin` package: `current/index.html` can
then differ from the installed package used for rollback. Its archive publish
also does not run the package activation/retention hook. For package-owned
hosts, use the signed installed-package/native transaction in
[signed upgrades](seamless-upgrades.md), preserving the installed tuple and
letting its package hook activate the frontend. Automatic frontend callers must
select that installed path for those hosts; the existing archive-only workflow
is not that implementation. The two-step workflow below also applies to the
existing hybrid path only after its native installation, same-ID receiver and
retention prerequisites are satisfied; see the single canonical explanation in
[production transactions](production-release-transaction.md#evidence-and-automation-boundary).
An archive dispatch does not satisfy those prerequisites by itself.

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

`just ship-web` is for archive-managed targets. For the existing hybrid path,
use `just release-web`, complete its canonical native installation/confirmation
and external proof, then deploy explicitly in step 2; ship cannot pause there.

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
the same completed tests; changed bound source objects reject the old record.
For a packaging-only change, preserve the application checks' actual revision,
exit and raw log hashes, prove unchanged application objects, and append the
necessary packaging checks to a new record bound to the final source. Do not
present imported tests as fresh runs or rebuild unchanged application bytes. It
then creates a signed annotated tag with your local Git signing key, pushes
it, dispatches `release-web.yml`, watches that exact run, dispatches
`deploy-web-frontend.yml` once and watches it. Each dispatch happens at most
once; on failure it prints the run ID and stops. The same evidence JSON is a
required `local_test_evidence` input to either component release workflow.
The publisher checks it against its immutable tag checkout, then only builds,
signs and publishes. Test/review workflows are manual diagnostics, with no
push, pull-request, tag, merge-group or scheduled triggers.

### Two explicit steps

Use a clean local `main` checkout and authenticated `gh`. First review the Web
changes against **both** hosts' active Go providers; the deploy workflow does
not perform that compatibility review. Choose an actual unused `web-vX.Y.Z`
tag, newer than existing Web tags and the AUR `pkgver`. Check local/remote tags
and the Release by tag; only a confirmed absence counts, not a failed network
read. No example version is an assertion that a tag is available.

**1. Test locally, then publish the signed Release.**

```bash
set -euo pipefail
git switch main
git pull --ff-only origin main
bun install --frozen-lockfile
web_tag="${LMM_WEB_RELEASE_TAG:?Set the verified unused web-vX.Y.Z tag}"
web_revision="$(git rev-parse HEAD)"
web_evidence_dir="$(mktemp -d "${TMPDIR:-/tmp}/lmm-web-local.XXXXXX")"
web_evidence="$web_evidence_dir/checks.json"
VITE_REACT_APP_VERSION="${web_tag#web-v}" \
  python3 scripts/local-release-tests.py run --component web \
    --revision "$web_revision" --output "$web_evidence" -- bash -euc '
      bun run --filter @lmm/web typecheck
      bun run --filter @lmm/web test
      bun run --filter @lmm/web build
      bun run --filter @lmm/web bundle:check
    '
bash scripts/verify-release-commit-checks.sh "$web_revision" \
  --component web --evidence "$web_evidence"
git tag -s "$web_tag" "$web_revision" -m "LMM web $web_tag"
git push origin "refs/tags/$web_tag"
gh workflow run release-web.yml --ref "$web_tag" \
  -F "local_test_evidence=@$web_evidence"
```

The evidence validator accepts this successful compound command and binds its
actual source, exit and log hashes; it does not mandate seven named checks.
These four checks do not claim formatting, lint or copyright checks ran. Add
`format:check`, `lint`, `copyright:check` or route/packaging checks when relevant
to the change, preserving their actual outcomes. Reuse qualifying completed
records instead of running unchanged checks again. Retain the evidence and logs.

Record the dispatch's exact run ID. If no run URL is returned, list
`gh run list --workflow release-web.yml --branch "$web_tag"` and match the tag,
source SHA and dispatch time. Run `gh run watch RELEASE_RUN_ID --exit-status`;
continue only on that run's completed success and the Release's complete signed
asset set. A tag push alone does not start `release-web.yml`; its dispatch ref
must be the tag, not `main`, and its required input is `local_test_evidence`.

**2. Deploy the published Release, then accept the public result.**

```bash
gh workflow run deploy-web-frontend.yml --ref main -f "release_tag=$web_tag"
# Record this exact deploy run ID, matching title "Deploy $web_tag frontend".
gh run watch DEPLOY_RUN_ID --exit-status
curl --fail --silent --show-error https://api.lmm.best/api/status
curl --fail --silent --show-error https://api.lmm.best/index.html | sha256sum
```

Require the exact workflow's completed success, both origin publication checks
and public `index.html`/asset hashes matching the signed release. Check the
browser's Web build version against the tag suffix and affected user flows.
`/api/status.data.version` is the **Go** version; compare it with preflight and
confirm both active backends remain compatible. HTTP 200 alone is not acceptance.
This deploy still publishes an archive: it does not update Arch's installed
Web package or execute its keep hook. Apply the installation-path warning above;
the two-step guide does not implement an installed-package automatic caller or
replace the hybrid path's external package/full-tree evidence.

For an already published qualified release, skip step 1 and use
`just deploy-web TAG`; `just deploy-web-status RUN_ID` and
`just deploy-web-watch RUN_ID` inspect the exact deployment.

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

A backend-only update reuses the unchanged signed Web release/package; do not
rebuild, republish or reactivate Web just to change Go. Reuse successful local
checks and the complete official signed Go asset set when source objects and
release identity match. Inspect partial publication or an uncertain dispatch
before recovery; never create another release or resign the same version to
work around a lost reply.

For standalone systemd, use `systemd doctor`, then stage/apply or `upgrade`,
verify the changed functions, and explicitly `confirm`. Keep the previous binary,
frontend and database recovery evidence. See [the standalone guide](manual-systemd-deployment.md).

For package-owned servers, use the installed `/usr/bin/lmm-api-deploy production`
workflow with its immutable signed plan. See [signed upgrades](seamless-upgrades.md)
and [production transactions](production-release-transaction.md). These native
checks remain in place; this change does not replace database migration or
recovery logic. Rust remains an explicit preview, not a deployment default.

Review database changes before choosing an ordinary code-update path. New
tables, migrations or required seed changes need the existing reviewed schema/
maintenance path and N−1 compatibility; route compatibility alone does not prove
an unchanged database. Keep both hosts on one ordered batch and component
version, with only one backend owner/mutation at a time and each host's own
manifest/owner binding. See the artifact reuse and caller
handoff steps in [production transactions](production-release-transaction.md).

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
