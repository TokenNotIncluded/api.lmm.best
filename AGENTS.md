# Agent guide

This is the repository guide for `TokenNotIncluded/api.lmm.best`. Read the
[contribution guide](CONTRIBUTING.md) and the [skill index](.agents/skills/README.md)
before editing. Load only the skills needed for the task.

## Establish the current state

- Read the current branch, base commit, working-tree changes, and related PRs.
  Do not replace another contributor's work or force-push a shared branch.
- Use code and configuration at that revision as evidence. A design proposal,
  a WIP PR, a merge, a release, and a production deployment are different states.
- The default-branch development entry points are in [justfile](justfile) and
  [package.json](package.json). Do not apply a refactor branch's installation
  instructions to another branch.

## Find the work

| Task | Start here |
| --- | --- |
| Local setup, checks, and repository paths | [Development](docs/development.md) |
| UI and component changes | [shadcn/ui skill](.agents/skills/shadcn-ui/SKILL.md) |
| UI text or translations, including one key | [Translation skill](.agents/skills/i18n-translate/SKILL.md) |
| React performance | [Performance skill](.agents/skills/vercel-react-best-practices/SKILL.md) |
| README, docs, or skills | [Documentation skill](.agents/skills/docs-maintenance/SKILL.md) |
| Release, deployment, or recovery | [Deployment skill](.agents/skills/deployment/SKILL.md) |
| Publishing or reviewing a bounty | [Skill index](.agents/skills/README.md) |

Commands use the repository root unless a command explicitly changes directory.
The frontend is `apps/web`, not a root-level frontend directory. Keep app changes,
translation changes, and generated output limited to the requested scope.

## Safety and verification

Never commit credentials, cookies, private keys, production data, or unredacted
logs. Do not use a production database for development or tests. When using
api.lmm.best, each user may have only one account; do not register accounts in
bulk or create extra accounts to obtain rewards or bypass limits.

Run relevant checks locally. Record the exact commands, tested revision, results,
and skipped checks. Do not turn a syntax check or a fixture test into a claim of
full application, browser, database, or production validation.

Preserve source attribution, copyright headers, and license notices. Keep
vendored skill references separate from project-owned instructions.

A request to edit code or docs is not permission to publish a release, move funds,
change production, or deploy. Keep changes on a task branch and report the commit
and PR. Merge or deploy only when authorized. Never infer completion from a
successful request alone; read back the resulting state.
