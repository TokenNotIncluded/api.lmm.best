# GitHub Actions in TokenNotIncluded/api.lmm.best

GitHub Actions builds, tests, signs, and publishes artifacts. Server deployment
and operations are manual, using operator-controlled SSH and the native CLI.
PR title and description formatting is not enforced by Actions.
No workflow connects to a production server or receives server credentials.

- `ci.yml`: Go/Rust/Web, integration, package, translation, and quality gates.
- `server-release-qualification.yml`: isolated PostgreSQL/Valkey migration and
  recovery qualification on the runner, not a production deployment.
- `release-go.yml` and `release-web.yml`: manually dispatched signed publication
  on immutable component tags. Publication ends with a GitHub Release.

Release workflow paths are part of the Sigstore certificate identity. Keep their
names and the existing source-check, signature, and immutability gates.
Tag pushes never deploy, and release workflows have no deployment input or job.

Server workflows, their shared deployment action, incident-request trigger, and
workflow-only SSH controllers have been removed. The production SSH private key
and known-hosts secrets must not be configured in GitHub Actions. Historical
workflow files still exist in Git history; deleting these secrets prevents those
old revisions from authenticating to servers through the removed configuration.

Manual package deployment retains the native signature, rollback, migration,
observation, and health checks. See `docs/production-release-transaction.md` and
`docs/manual-systemd-deployment.md` for the separate manual deployment paths.

Validate changes with actionlint, `node --test scripts/workflow-topology.test.mjs`,
and the CI/release Python tests. Workflow boundary tests reject production SSH
references and server access steps while retaining runner-only qualification.

Open issues and pull requests do not block publication. Release eligibility is
based on the exact revision, required CI checks, signatures, and immutable assets.
