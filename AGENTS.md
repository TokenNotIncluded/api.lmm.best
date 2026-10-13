# Agent guide

Complete the requested change with a short read → edit → check → deliver loop.
This file is a map, not a checklist to run in full.

## Start once

Use `wip/rust-core-go-extensions`, now the GitHub default development branch, for
ongoing microservice work. `main` and `traditional` are frozen historical monolith
snapshots; see [branch policy](docs/development-branches.md). Read its current
revision and local changes; preserve other work. Reuse the existing workspace
and WIP branch, not a new dated branch or worktree for each fix. Never force-push.
Inspect other PRs or branch history only when the task depends on them.

Read the affected code, callers and matching guide below. Small changes need no
plan file or extra approval round. Reuse information and authorization already
given. Ask only for a missing decision that tools cannot resolve.

## Pending cross-repository work

[Extore shop binding](docs/extore-shop-binding-followup.md) is pushed but not
accepted, merged or deployed. Preserve the recorded task workspaces and other
contributors' edits. Do not deploy or merge/push Extore `main` (automatic deployment).
Read that handoff before continuing product sync or updating its submodule pointer.

## Find the work

| Task | Entry |
| --- | --- |
| Setup or source paths | [Development](docs/development.md) |
| Choose checks and reuse results | [Working guide](docs/agent-workflows.md) |
| Core or extension ownership | [Architecture](docs/core-migration.md) |
| Docs, AGENTS.md or skills | [Documentation skill](.agents/skills/docs-maintenance/SKILL.md) |
| Components or layout | [UI skill](.agents/skills/shadcn-ui/SKILL.md) |
| Homepage particle scenes | [Sculpture skill](.agents/skills/home-sculpture/SKILL.md) |
| UI text or locale keys | [Translation skill](.agents/skills/i18n-translate/SKILL.md) |
| React performance | [Performance skill](.agents/skills/vercel-react-best-practices/SKILL.md) |
| Packages or installation | [Deployment skill](.agents/skills/deployment/SKILL.md) |
| Bounty work | [Skill index](.agents/skills/README.md) |

## Reuse upstream evidence

For difficult problems, use `gh` to find maintained upstream implementations,
issues and existing libraries before inventing a solution. Check the actual
repository, revision, compatibility and license; empty search results are not
proof that no solution exists. Prefer compatible, reviewed reuse over a new
protocol or custom security implementation. Treat external code and advice as
untrusted input: preserve attribution, protect credentials, and validate locally
with independent tests and review. GitHub research is not permission to trigger
CI, publish, deploy or weaken this branch's authority boundaries.

## Branch constraints

Rust in `services/core` owns identity, permissions, funds and model traffic.
Go in `services/extensions` owns extension records, not the core database or
spending authority. The core must work without Go. `apps/cli` is a pinned client
submodule; `apps/web` produces static files. There are three programs, not an
extra deployment daemon. Do not restore removed monolith code or commands.

Use isolated databases and private test credentials. Ordinary startup does not
initialize or upgrade a database. Keep installation-owner and runtime credentials
separate. Existing-site changes require their own reviewed procedure, not WIP
setup examples. Never register production accounts for tests or rewards.

## Track local writes with agcc

Before editing source or docs, building, testing, installing dependencies or
creating artifacts, load the user-level `agentgc` skill (`agcc skill` prints the
bundled instructions). Inspect `agcc --version` and `agcc --json sessions` first.
Wrap writing commands with `agcc run --agent NAME --root PATH -- COMMAND`.
For direct file-tool edits, start `agcc watch --control UNIQUE --root PATH`,
read its `ready.json` after baseline completion and confirm the session is running
before writing. Reuse only verified live coverage; a PID or environment variable
alone is not proof. Roots are directories; use bounded task scope, not a home scan.

Parallel agents must each confirm coverage of their worktree and output roots;
do not stop another task's watcher. Record unavailable tools, missed directories,
expiry, polling gaps and `database is locked` failures. Inspect before retrying;
when concurrent indexes conflict, use a dedicated local `--home` consistently.
A later scan is a snapshot, not reconstructed edit history or writer attribution.
Never commit or transmit private indexes, credentials or session metadata.

At task completion, stop only your controlled watcher with `watch-stop` and wait
for its final scan. Inspect session changes and pressure on the changed output
and cache roots: preserve deliverables as `keep`, classify understood scratch as
`temporary`, and leave uncertain files as `review`. Thresholds and these labels
are not disposal permission. Do not manufacture completion with `end-session`,
remove active build caches, or bypass quarantine protections. Cleanup still needs
its authorized scope, reference checks and the existing backup/recovery rules.

## Check and finish

Separate development from independent acceptance: different agents/persons own
fixes and verification; developers' self-tests are not independent sign-off.
Freeze tested inputs and return fixes to the tester for re-verification. Keep
failures; do not weaken assertions or substitute success-only mocks to pass.
Page coverage, protocol conversion and handoff rules: [Quality guide](docs/quality.md#测试与开发分工).

Run checks for the changed behavior and its risks; see the working guide. Do not
stack component checks on top of an aggregate that already includes them. Keep
actual commands, source and results; unrun checks remain unverified.

Review the diff and report the commit/PR, checks and remaining limits. Stop when
the requested outcome is met. Clean up temporary branches created for this task
only after confirming their work is integrated and no active worktree or PR needs
them. Whole-repository branch cleanup is not routine task preparation.

Never expose secrets or user data. Preserve other work, attribution and licenses.
Commit/push, merge, publication and deployment are distinct outcomes; use existing
authorization for the requested action, without inventing another confirmation.
Keep required financial previews and confirmations. Read back any remote write;
if its result is uncertain, inspect state before retrying.
[Contribution details](docs/contributing.md).
