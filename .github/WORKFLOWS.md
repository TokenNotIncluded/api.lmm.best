# GitHub Actions in TokenNotIncluded/api.lmm.best

The production repository is **TokenNotIncluded/api.lmm.best**. The personal fork
is not a production operations entry point and receives no production credentials.

## Migrated workflows

- `server-ops.yml`: manual main-only diagnose/repair and the existing explicit
  owner-only, request-only incident diagnosis. Both use the original protected
  production environment and the same `production-auto-deploy` concurrency group.
- `ci.yml`: all upstream Go/Rust/Web/integration/package gates, merge-queue support,
  and the migrated translation checker. CI Quality Gate requires translations too.
  A tag still tests the checker; only its branch-to-branch comparison is skipped.
- `release-go.yml` and `release-web.yml`: retain exact-source release checks,
  unresolved-work barriers, and signing identities. New releases own their final
  serialized deployment job through `.github/actions/deploy-production/`.
- `deploy-production.yml`: compatibility adapter for historical tags only. It
  inspects the immutable source and skips releases with the inline deployment
  action, preventing duplicate deployments. It no longer handles owner requests.

Do not delete upstream qualification workflows to match the former fork's count
of five files. Server release qualification, root-route acceptance, security audit
and assistant regressions remain. Their checks and native migration/observation
contracts are not replaced by topology tests or a successful package publication.

## Trust and rollout

Operations require main, the actual authorized dispatcher and triggering actor,
an exact committed script, confirmation, and a new run for mutations. Fixed
owner requests retain their separate non-forced single-parent request-only commit,
age and digest checks. No caller identity is synthesized. One canonical
`scripts/server-ops.py` transport serves both paths and is hash-checked before
credentials. Raw repair logs stay on the host. See `docs/server-ops.md`.

GitHub concurrency is repository-scoped. All maintained production jobs must stay
in this upstream repository; the server's native transaction lock remains required.
The tag-based deployment environment must permit the intended Go/Web release tags;
this migration does not change its reviewers, secrets or deployment rules.

Old immutable tags keep their historical workflow definitions. Retaining the
legacy adapter lets those tags work without rewriting them. New and legacy
operations never auto-confirm a transaction or bypass a pending recovery.

Run `node --test scripts/workflow-topology.test.mjs`, all server-ops Python tests,
`python3 -m unittest discover -s scripts -p test_ci_quality_gate.py`, and actionlint.
These local tests do not establish production health. No release or server change
is requested merely by merging this migration.
