# GitHub Actions in TokenNotIncluded/api.lmm.best

The production repository is **TokenNotIncluded/api.lmm.best**. The personal fork
is not a production operations entry point and receives no production credentials.

## Migrated workflows

- `server-ops.yml`: manual main-only diagnose/repair and the existing explicit
  owner-only, request-only incident diagnosis or fixed schema recovery. Both use the original protected
  production environment and the same `production-auto-deploy` concurrency group.
- `ci.yml`: all upstream Go/Rust/Web/integration/package gates, merge-queue support,
  and the migrated translation checker. CI Quality Gate requires translations too.
  A tag still tests the checker; only its branch-to-branch comparison is skipped.
- `release-go.yml` and `release-web.yml` are manual-only signed publication
  workflows. Dispatch on an immutable component tag after its source checks pass.
  Publication does not deploy by default; deployment additionally requires
  `deploy=true` and `confirm=api.lmm.best`. Tag pushes never publish or deploy.
  The existing signed-package, rollback, observation and native confirmation
  contracts remain mandatory.

Do not delete upstream qualification workflows to match the former fork's count
of five files. Server release qualification, root-route acceptance, security audit
and assistant regressions remain. Their checks and native migration/observation
contracts are not replaced by topology tests or a successful package publication.

## Trust and rollout

Operations require main, the actual authorized dispatcher and triggering actor,
an exact committed script, confirmation, and a new run for mutations. Fixed
owner requests retain their separate non-forced single-parent request-only commit,
age and digest checks. No caller identity is synthesized. Recovery retains its freshly tested, digest-bound helper and original native
confirmation boundary. One canonical `scripts/server-ops.py` transport serves both paths and is hash-checked before
credentials. Raw repair logs stay on the host. See `docs/server-ops.md`.

GitHub concurrency is repository-scoped. All maintained production jobs must stay
in this upstream repository; the server's native transaction lock remains required.
The production environment remains available to explicitly authorized manual
operations; removing the tag workflows does not change its reviewers or secrets.

New and legacy operations never auto-confirm a transaction or bypass a
pending recovery.

Run `node --test scripts/workflow-topology.test.mjs`, all server-ops Python tests,
`python3 -m unittest discover -s scripts -p test_ci_quality_gate.py`, and actionlint.
These local tests do not establish production health. No release or server change
is requested merely by merging this migration.
