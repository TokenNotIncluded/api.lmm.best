# Contributing to LMM Forge

Keep changes focused on the requested outcome and branch. Agents start with
[AGENTS.md](AGENTS.md); detailed setup is in [Development](docs/development.md).
Do not treat a pending architecture change as default-branch behavior.

## Issues

Search for duplicates before opening a new issue, not before every code edit.
Provide the affected revision, expected and actual behavior, and reproducible
steps. Remove secrets from screenshots and logs. Use the
[Issue templates](.github/ISSUE_TEMPLATE). Report security issues through
[SECURITY.md](SECURITY.md), not a public thread containing secrets or exploit data.
Third-party hosting, cloud pricing and private-fork requests belong with their owner.

## Development and verification

Install only the tools required for the task. Documentation changes do not need
Go, Bun, Rust, PostgreSQL or a running app. Existing development configuration
must not be overwritten. The Go process reads `apps/api-go/.env`.
`just dev` requires a local Compose file that this branch does not include.
Use the explicit startup commands in the development guide.

Select checks with the [working guide](docs/agent-workflows.md). Start with the
affected behavior and callers. Expand for shared interfaces, dependencies,
security, billing, persistence or other demonstrated risks; do not require a
full application build for every production-source edit.

For documentation or skill text:

```bash
python3 -B scripts/check-docs-brand.py --docs-only
```

Run the checker's unit tests when changing the checker, not for each Markdown
edit. For broad Go/Web changes, `just check` runs the combined checks once;
do not also repeat its formatting, lint, typecheck and test prerequisites.
Build the affected component only when artifact behavior needs verification.
Run the full documentation/asset check for logo changes.

Review changed examples against their implementation. Do not execute deployment,
registration, migration or payment examples merely to check documentation.
Record real commands, tested source, outcomes and skipped checks. Reuse valid
results rather than relabeling old tests as new runs. A local test is not proof
of browser, database or production acceptance.

## CI and release evidence

Read the selected workflow's `on` block before assuming a trigger.
[CI](.github/workflows/ci.yml) is a manual diagnostic; a PR or push is not proof
that tests ran. Tests run locally. The Go/Web publishers require source-matched
`local_test_evidence` and perform the official release build and signing.
See [Actions](docs/ci-workflow-layout.md) and [Deployment](docs/deployment-workflow.md).
Do not change workflow triggers or publish as a side effect of a documentation edit.

AUR release freshness is checked with
`bash packaging/aur/verify-go-release-pins.sh --latest` before an AUR publication,
not before unrelated work. See [the AUR guide](packaging/aur/README.md).

## Pull requests

Use the [PR template](.github/PULL_REQUEST_TEMPLATE.md). Describe the outcome,
compatibility impact, relevant issue and actual checks. Keep unrelated formatting,
generated output, dependency upgrades and refactors out. Preserve attribution,
headers, `NOTICE`, `FORK.md` and licenses. Update the canonical documentation when
behavior changes; update translations or indexes only when their content changes.

Review the final diff once. Additional review should resolve a concrete finding,
not repeat the same checklist. Judge the contribution by its code and evidence,
not account age, profile details or whether AI tools were used.

Rejecting a proposed fix does not resolve the underlying bug. Keep that issue
open unless it is actually resolved, out of scope, or a duplicate with an
identified replacement. Reuse an existing accurate explanation instead of posting
duplicate comments.

Keep changes on a task branch and report the commit/PR. Merge, release, production
access and fund movement need authorization covering that action. Continue when
that authorization already exists; do not create another approval step merely
because the work moved from editing to verification.
