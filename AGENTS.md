# Agent guide

Finish the requested change with the shortest reliable read → edit → check → deliver loop.
Use this file as a map, not a checklist to expand for every task.

## Start once

- Identify the branch, base revision and working-tree changes. Preserve other work;
  use a task branch. Inspect PR history only when the task depends on it.
- Read the affected code and its callers, plus the matching guide or skill below.
  Do not read every guide, scan all PRs, or install every toolchain first.
- Small, clear changes need no separate plan file or approval round. For complex
  work, keep a short plan and continue through implementation and verification.
- Use information and authorization already supplied. Resolve discoverable facts
  with available tools; ask only for a missing decision that changes the outcome.

## Find the work

| Task | Entry point |
| --- | --- |
| Setup and repository paths | [Development](docs/development.md) |
| Select checks; reuse results | [Working guide](docs/agent-workflows.md) |
| README, docs, AGENTS.md or skills | [Documentation skill](.agents/skills/docs-maintenance/SKILL.md) |
| UI components and layout | [UI skill](.agents/skills/shadcn-ui/SKILL.md) |
| Homepage particle scenes | [Sculpture skill](.agents/skills/home-sculpture/SKILL.md) |
| UI copy or locale keys | [Translation skill](.agents/skills/i18n-translate/SKILL.md) |
| React performance investigation | [Performance skill](.agents/skills/vercel-react-best-practices/SKILL.md) |
| Release, deploy or recover | [Deployment skill](.agents/skills/deployment/SKILL.md) |
| Bounty publication or settlement | [Skill index](.agents/skills/README.md) |

Commands start at the repository root unless stated otherwise. The frontend is
`apps/web`. Use [justfile](justfile) and [package.json](package.json) at the selected
revision; do not apply a refactor branch's commands to another branch.

## Verify and finish

Run the smallest checks that cover the changed behavior. Expand for affected
callers, shared contracts, security, billing or data integrity. Do not stack
`just test`, lint and formatting checks on top of `just check`, which includes them.
Do not rebuild an unchanged component or rerun a passed check without a reason.

Review the diff, then report the change, actual checks, unverified items and
commit/PR. Stop when the requested outcome is met; do not add unrelated cleanup,
benchmarking, plan documents or repeated review rounds.

## Keep these constraints

Never expose credentials or user data. Preserve other contributors' changes,
attribution and licenses. Never use production databases or account registration for tests.
Follow the [public account policy](apps/web/public/AGENTS.md) when using the site.
A code edit, merge, signed release and deployment are different outcomes.
Merge, release, deploy or move funds only when the request authorizes that action;
do not ask again for unchanged authorization already given. Keep signing, locks,
source checks, rollback safeguards and financial confirmation flows intact.
PR details and contribution policy: [CONTRIBUTING.md](CONTRIBUTING.md).
