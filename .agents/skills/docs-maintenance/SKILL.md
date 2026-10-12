---
name: docs-maintenance
description: "Update repository docs, AGENTS.md, skills, links and command examples. Use for documentation work, not as a prerequisite for every code edit."
---

# Maintain documentation and skills

Use [the working guide](../../../docs/agent-workflows.md) for shared check policy.
Work from the selected WIP revision, not a default-branch command copied from history.

1. Read the affected guide and the implementation behind its claims. Follow
   only links needed for this change. A typo does not require both READMEs,
   every skill, all PRs or deployment history.
2. Edit the canonical guidance. Update indexes and translations only when their
   content is affected. Remove duplicate instructions; preserve current branch
   constraints, historical recovery links, licenses and attribution.
3. Run `python3 -B scripts/check-docs-brand.py --docs-only` and review the diff.
   Check changed documents outside its maintained set separately. It does not
   validate remote URLs, anchors, full YAML syntax or command behavior.
4. Compare changed examples with their scripts and callers. Use syntax checks
   or isolated fixtures where useful. Never execute production, initialization,
   migration, registration or payment examples just to check prose.

Run checker self-tests only when its code changes; helper self-tests only when
that helper changes. Logo changes need the full asset check. Markdown-only work
needs no application dependencies, CLI submodule, database, app build or full
quality run unless that broader acceptance is explicitly part of the task.

Done means accurate guidance, checked entry links, actual checks and stated
limits, plus the requested commit/PR. No extra plan document, workflow trigger
change, release or production operation is needed.
