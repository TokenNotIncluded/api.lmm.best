# GitHub Actions

Keep nine upstream workflow entry points, including the four existing qualification workflows. Consolidate shared steps under `.github/actions/`
instead of adding another independently triggered workflow.

| Workflow | Triggers | Responsibility |
| --- | --- | --- |
| `ci.yml` | PR code events, main pushes, tag pushes, manual runs | Existing Go, Rust, web, provider, integration and package checks, plus translation regressions |
| `pr-check.yml` | PR metadata/code events, including description edits | Read-only description policy using trusted base code |
| `release-go.yml` | `go-v*.*.*` tags | Verify, test, build both architectures, sign, publish, then deploy the Go backend |
| `release-web.yml` | `web-v*.*.*` tags; manual runs on a release tag | Verify, test, build, sign, publish, then deploy the web frontend |
| `server-ops.yml` | Manual runs on `main`; explicit owner diagnostic request | Authorized server diagnosis or reviewed repairs; shares the production deployment lock |

The independent `server-release-qualification.yml`, `rust-root-route-acceptance.yml`,
`rust-security-audit.yml`, and `assistant-support-regressions.yml` remain intact.
The standalone deploy subscriber and i18n workflow are folded into their callers;
their protections are not removed.

## CI and translations

The `Translation regression check` job keeps its existing check name, full
comparison history, checker tests, and PR merge-base behavior. Manual CI runs
accept `base-ref` (default `HEAD^`). Tag pushes skip only the translation
comparison, not the checker tests. Translations are a required CI Quality Gate
dependency; merge-queue comparisons use `merge_group.base_sha`. All original CI
release gates remain. Feature-branch pushes do not
start another copy of PR CI.

PR description policy stays separate deliberately: `pull_request_target` reads
trusted base policy only. It does not execute PR head code, gain write permissions,
or receive production secrets. Editing a PR description reruns that small policy
check, not the entire build and integration suite.

## Release and deployment

Deployment is the final job of the corresponding release run, not a separate
`workflow_run` subscriber. Go deployment needs `prepare` and `publish`; web
deployment needs `web`. Publication failure or a non-release branch cannot reach
deployment. A deployment failure is visible in the same run; retry the failed
`deploy` job rather than rebuilding or attempting to overwrite signed assets.

Both jobs retain the `production` environment, a 50-minute timeout, read-only
repository permissions, and the shared `production-auto-deploy` concurrency group
with `cancel-in-progress: false`. Environment approvals and secrets still apply. The environment deployment-ref
policy must allow `go-v*` and `web-v*` tags; unlike the old default-branch
`workflow_run` subscriber, the final job now runs in the release tag context.
The checkout and local deployment action are pinned to the exact released commit.
The common action checks checkout and tag identity before invoking the
existing signed-package verification, rollback preparation, staging and promotion
script and the same public version/page/asset acceptance. No production rollout is needed just to test this topology change.

Do not rename `release-go.yml` or `release-web.yml` casually: their paths are part
of the Sigstore certificate identities used by existing releases and package
verifiers. Sharing deployment steps does not change those identities.

When applying this consolidation, let any already-running legacy release and
deployment runs finish first. Old tags retain their historical workflow definitions
and do not gain the new in-workflow deployment job retroactively. New release tags
must include this change. Historical Actions run records are not deleted.

## Manual server operations

Keep `server-ops.yml` separate from tag-triggered releases. Its default remains
read-only diagnosis, and repairs still require explicit confirmation and a reviewed
script from `main`. It retains the protected `production` environment, pinned
checkout and SSH keys, operator authorization, and the same
`production-auto-deploy` concurrency group with cancellation disabled.
See `docs/server-ops.md` for the operator contract. The fixed owner-request diagnostic
trigger lives only in `server-ops.yml`; ordinary code pushes cannot execute repairs.

## Regression checks

Run `node --test scripts/workflow-topology.test.mjs` and actionlint before merging.
The tests cover the five entry points, existing CI gates, trusted PR policy,
publication dependencies, production concurrency, signing identities, and actual
release-context shell guards using disposable local Git repositories. They never
use SSH credentials or contact production.
