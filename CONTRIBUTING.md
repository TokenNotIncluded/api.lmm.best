# Contributing to LMM Forge

This file defines the process for code, documentation, and operational changes.
Agents should also read [AGENTS.md](AGENTS.md) and the [skill index](.agents/skills/README.md).

## Scope

This repository accepts focused improvements to model access, client integrations,
tool and bounty workflows, the frontend, backend compatibility, security, and
operational reliability. Keep work limited to the requested branch and objective.
A pending architecture change is not the default branch's current behavior.

For third-party deployment/hosting issues, cloud pricing issues, or private fork
customization, contact the corresponding owner instead of opening unrelated issues.

## Before opening an Issue

- Check existing issues and pull requests to avoid duplicate work.
- Remove API keys, cookies, DSN, passwords, and tokens from screenshots and logs.
- Give reproducible details: exact endpoint, expected behavior, actual behavior,
  revision, and relevant environment information without secrets.

Use the [Issue templates](.github/ISSUE_TEMPLATE) and
[PR template](.github/PULL_REQUEST_TEMPLATE.md). Security reports follow
[SECURITY.md](SECURITY.md), not a public issue containing exploit details or secrets.

## Development setup

Follow the [local development guide](docs/development.md) to configure PostgreSQL,
Valkey, and `apps/api-go/.env`. Start Go with `just dev-go`, then run the frontend
in a separate terminal on port 5173 as shown in that guide.

`just dev` and `just dev-rust` require a locally supplied `docker-compose.dev.yml`;
the repository does not include it. They are not fresh-checkout shortcuts.

## Select checks for the change

For README, documentation, or skill changes, run from the repository root:

```bash
python3 -B scripts/check-docs-brand.py --docs-only
python3 -B -m unittest discover -s scripts -p test_check_docs_brand.py
```

For translation helper changes, also run:

```bash
node --test .agents/skills/i18n-translate/scripts/apply-translations.test.mjs
```

For logo changes, run `python3 -B scripts/check-docs-brand.py` without the filter
and inspect light/dark placements and small icon sizes. The documentation checker
covers maintained entry documents and project skill entry points, not all Markdown
files, remote URLs, heading anchors, or application behavior.

For application changes, run relevant formatting checks, lint, and tests. The
root recipes include `just format-check`, `just lint`, and `just test`. Apply
formatting only to files in scope; do not run whole-tree `just format` for a docs
change. For production-facing code, also run `just build`, `just check`, and the
relevant app-level checks in `apps/api-go`, `apps/api-rust`, or `apps/web`.

Record actual commands, results, tested revision, and skipped checks with reasons.
A syntax check or fixture test is not a full application, database, browser, or
production acceptance test.

## CI and release evidence

Tests run locally. The current [CI workflow](.github/workflows/ci.yml) supports
manual `workflow_dispatch`; a PR, main push, or tag does not automatically trigger
that workflow. Check each auxiliary workflow's actual `on` block separately.
Do not infer a trigger from a job name, old comment, or documentation snapshot.

Go/Web signing workflows require source-matched `local_test_evidence`. They do
not use an assumed automatic PR test run as publication evidence. See
[Actions](docs/ci-workflow-layout.md) and the [deployment workflow](docs/deployment-workflow.md).
Do not change triggers or publish a release as a side effect of a docs update.

The AUR verification tools distinguish checking a pinned release from checking
release freshness. Before publishing an AUR update, use
`bash packaging/aur/verify-go-release-pins.sh --latest` as described in the
[AUR guide](packaging/aur/README.md). Do not treat this network/publishing workflow
as a prerequisite for a documentation-only change.

## PR expectations

Describe the change, compatibility impact, related issue, tests, and documentation
updates. Use the current PR template even when no automatic description check runs.
Preserve source attribution, copyright headers, `NOTICE`, `FORK.md`, and licenses.
Keep unrelated formatting, generated output, dependency upgrades, and refactors out.

A checked checklist item does not mean every possible test ran. Explain skipped
checks. Maintainers decide to merge, request changes, or close based on the code,
tests, scope, and review. Account age, profile completeness, and use of AI tools do
not determine the quality of a contribution.

For an incorrect closure, comment with the relevant evidence. Before closing a
duplicate, identify the replacement. Do not close an underlying bug merely because
one proposed fix was rejected.

A request to edit does not authorize merging, releasing, production access, or
moving funds. Keep commits on a task branch, report the PR, and respect the user's
explicit merge and deployment instructions.
