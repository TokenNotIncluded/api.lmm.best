# Build once, deploy an existing release

Go and Web are independent components. Choose the requested outcome and installation
owner first. A source edit, merge, signed release and production deployment are
separate outcomes; do not do the next one without authorization.

| Task | Shortest applicable path |
| --- | --- |
| Edit deployment docs/tooling | Local checks only; no production preflight |
| Publish a new Web release | `just release-web` with matching local test evidence |
| Deploy an existing qualified Web archive | `just deploy-web TAG`; no build or new tag |
| Publish and deploy Web to archive-managed hosts | `just ship-web`; same signed artifact |
| Update package-owned Go/Web | Installed `/usr/bin/lmm-api-deploy production` and the native plan |
| Update standalone Go/systemd | Separate tool's `systemd` path |
| Change schema or recover an interrupted transaction | The reviewed native maintenance/recovery plan |

Use the [separate deployment tool](standalone-deployment-tool.md), not the API
binary's removed `operator` command. Historical recovery keeps its pinned tool.
Select only the applicable sections below; do not read every recovery guide first.

## Read the live state first

Freeze one source revision and compare the complete component inputs defined in
`scripts/local-release-tests.py`, including packaging/controller/workflow inputs.
Choose Web-only, Go-only or combined. Keep that batch fixed; newer main changes
belong to the next batch, not an endless restart of qualification.

Read only the state needed for the requested action:

- Publication only: source, local test evidence, intended tag and existing signed
  release/run. No SSH, server tool installation or PostgreSQL audit is required.
- Web deployment: installation ownership on both origins, active/candidate Web
  identities and compatibility with both active Go providers. Inspect the exact
  release/run and the selected path's existing transaction state.
- Backend/schema deployment: also capture native owner/transaction status,
  installed/provider/start identities, PostgreSQL/schema identity and the native
  plan's required backup, integrity and retained-provider compatibility evidence.

Refresh mutable host/owner state at phase boundaries, not the complete source and
artifact audit after each operation. An unfinished backend transaction or writer
capsule blocks another backend rollout; do not stop it or delete state to proceed.
A Web-only release must not mutate that backend transaction.

## Frontend-only update

Select installation ownership before dispatch. `deploy-web-frontend.yml` publishes
an archive; it does not update Arch's `lmm-api-web-bin` package or run the package
activation/retention hook. Package-owned files must use the signed native path in
[signed upgrades](seamless-upgrades.md) and
[production transactions](production-release-transaction.md).

The existing hybrid path is not automatic: publish with `just release-web`, then
complete native installation/confirmation, same-ID receiver, retention and external
package/full-tree proof, then dispatch the archive. Its canonical prerequisites
are in [production transactions](production-release-transaction.md#evidence-and-automation-boundary).
`ship-web` cannot pause for that native step and is not a package updater.

The candidate Web must work with both active Go backends. If it needs a newer
backend, use a combined native transaction. `/api/status.data.version` is the Go
version, not the Web build version.

### One command

For a new release, retain meaningful local checks once. This example runs the Web
tests; add only checks needed for the actual change:

```bash
set -euo pipefail
umask 077
evidence_dir="$(mktemp -d)"
python3 scripts/local-release-tests.py run --component web \
  --output "$evidence_dir/web-tests.json" -- bun run --filter @lmm/web test
export LMM_LOCAL_TEST_EVIDENCE="$evidence_dir/web-tests.json"
just release-web
# Instead, for an authorized archive-managed rollout: just ship-web
```

Use an existing valid record instead when available. Both commands can take an
explicit unused `web-vX.Y.Z` tag. The caller fetches `origin/main`, selects/checks
the next tag, verifies component source objects and local evidence, then dispatches
the publisher. Make the record against the intended frozen source; the validator
must reject relevant changes, not be bypassed. Confirm a tag is unused from a
successful read, not from a network error.

The publisher runs against the immutable component tag, builds and signs the
release, and requires `local_test_evidence`. Tests run locally. Do not wait for an
assumed automatic PR test run or rebuild Go for Web. Use `just build-web` locally
when bundler, import, dependency or artifact behavior needs verification; do not
mandate another full build for every text edit. The record states what actually ran,
not that all possible suites passed.

To import completed tests, use `scripts/local-release-tests.py import` with the
actual tested revision, command, exit code and original stdout/stderr logs. An
import does not execute a test. Keep logs private and redact secrets at their source.
Packaging-only changes still need matching final component evidence: prove unchanged
application objects and add the necessary packaging checks; never relabel old runs
as new or hand-edit hashes. Reuse a complete signed release instead of rebuilding
or resigning an existing version.

### Two explicit steps

Prefer the wrapper above. Use explicit steps only when release and deployment must
be separated, such as the hybrid native path. Use a clean checkout of the intended
main revision, authenticated `gh`, a signing key and valid local test evidence.
Do not switch or overwrite another contributor's working tree to obtain it.

**1. Publish once.** Verify the selected tag is unused and newer than existing Web
tags and AUR `pkgver`. Set the two variables to actual reviewed values:

```bash
set -euo pipefail
web_tag="${LMM_WEB_RELEASE_TAG:?Set the verified unused web-vX.Y.Z tag}"
evidence="${LMM_LOCAL_TEST_EVIDENCE:?Set the matching local evidence file}"
revision="$(git rev-parse HEAD)"
bash scripts/verify-release-commit-checks.sh "$revision" \
  --component web --evidence "$evidence"
git tag -s "$web_tag" "$revision" -m "LMM web $web_tag"
git push origin "refs/tags/$web_tag"
gh workflow run release-web.yml --ref "$web_tag" \
  -F "local_test_evidence=@$evidence"
```

A tag push is not a workflow dispatch. Capture the exact release run ID. If the
reply is lost, find the run matching the tag, source and dispatch time; do not
send another write. Require completed success and the full signed asset set.
Use `gh run watch RELEASE_RUN_ID --exit-status`, or read its exact status when
that credential type cannot watch. Inspect preserved signed artifacts after a
partial publication before choosing the documented recovery path.

**2. Deploy the published release once.** Only after installation-path prerequisites
and production authorization are satisfied:

```bash
just deploy-web "$web_tag"
# Use the exact ID returned by this dispatch, not the newest run:
just deploy-web-watch DEPLOY_RUN_ID
```

For an already qualified release, start here; skip publication and all builds.
`just deploy-web-status RUN_ID` reads the same run. Without Just, use
`bash scripts/lmm-api-deploy.sh web deploy TAG`, `web status RUN_ID` or
`web watch RUN_ID`. This workstation path needs `gh`, not a Go provider, application
toolchains or an unrestricted local server SSH key. The deployment workflow comes
from `main`; `LMM_API_GITHUB_REPOSITORY=owner/repo` selects another repository.

Require exact-run success, both ordered origin publication checks and public
`index.html`/asset hashes and Web version matching the signed release. Check the
affected UI flow and that both Go identities remain compatible and unchanged for
a Web-only update. HTTP 200, a release page or successful dispatch alone is not
acceptance. Native/hybrid installations also require their native confirmation
and external proof. Report requested, released and deployed separately.

### Production configuration

Existing Actions configuration remains the source of truth:

| Variable | Purpose |
| --- | --- |
| `LMM_WEB_API_HOST`, `LMM_WEB_API_PORT` | API origin and SSH port |
| `LMM_WEB_INGRESS_HOST`, `LMM_WEB_INGRESS_PORT` | Public ingress and SSH port |
| `LMM_WEB_PUBLIC_ORIGIN` | HTTPS origin for public acceptance |

Host changes need matching `LMM_WEB_DEPLOY_KNOWN_HOSTS` and the restricted deploy
key. Preserve host-key verification and forced commands. The archive workflow
updates the API origin and public ingress in order; another topology requires an
actual rollout change, not a documentation shortcut.

Retry bounded safe artifact reads, retaining verified downloads. Hash, signature,
source, ancestry and installation checks remain required. Never blindly rerun an
interrupted deployment: one host may already have switched and release IDs cannot
be overwritten. Inspect the exact state and use explicit recovery or rollback;
do not delete directories to force a retry. See [server operations](server-ops.md).

## Backend updates

Use the installation's owner. An unchanged signed Web package/release remains in
place during a Go-only update; do not rebuild, republish, reactivate or restart it.
Reuse source-matched local evidence and complete signed Go assets.

Standalone systemd uses `systemd doctor`, stage/apply or `upgrade`, affected-function
checks and explicit `confirm`. Keep prior binaries and recovery evidence. Follow
[the standalone guide](manual-systemd-deployment.md).

Package-owned systems use the installed `/usr/bin/lmm-api-deploy production` with
an immutable signed plan. Follow [signed upgrades](seamless-upgrades.md) and
[production transactions](production-release-transaction.md), not the standalone
path. Rust remains an explicit preview, not a production default.

New tables, migrations or required seed changes use reviewed schema maintenance
and N−1 compatibility, not an ordinary same-schema capsule. Check the retained
provider's sealed per-start restart compatibility too; additive DDL can alter its
catalog digest. Keep both hosts in one ordered batch, each with its own binding,
and only one shared-PG owner/mutation at a time. Parallelize independent reads,
not production owners.

Completion is the native plan's exact-ID `CONFIRMED`, expected provider/start
generation, controlled restart, owner/integrity checks and public acceptance.
Partial promotion needs native recovery, not another build. Preserve signatures,
locks, backups and required transaction confirmations.

## Local package development

`just package-go` uses the separate deployment tool through
`scripts/lmm-api-deploy.sh package`. Set `LMM_API_BUILD_WORKSPACE` to the existing
marker-owned workspace. Reuse a reviewed compatible tool; if its source changed,
run `just build-deploy` once. Do not build the Go API to bootstrap a deployment CLI,
and do not prepend `just build` to a package operation that builds the components.
This legacy local package path does not replace official signed publication.

## Verify changes to this workflow

For deployment-entrypoint code, run `just test-deploy-entrypoint` once. It includes
`test-deploy-entrypoint.py` and `test-web-ship.py`; do not also run their wrapper
prerequisites again. Add native state-machine, package or recovery checks only for
the behavior changed. These local fixtures do not prove a production rollout.

For prose-only changes, use [documentation checks](agent-workflows.md), not app
builds or production operations. Report eliminated operations separately from
measured time savings; a shorter guide alone is not a deployment benchmark.
