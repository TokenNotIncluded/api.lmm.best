---
name: docs-maintenance
description: >-
  Inspect and update this repository's README, contributor guides, documentation,
  and project skills. Use for stale commands, broken local links, missing indexes,
  mismatched language versions, or claims that differ from current code.
---

# Maintain documentation and skills

Read [AGENTS.md](../../../AGENTS.md) and the
[working guide](../../../docs/agent-workflows.md). Work from a known branch and
commit; do not treat a pending refactor or an earlier conversation as current code.

## Inspect before editing

1. Find the affected entry points and their readers. Follow links from both
   READMEs, the documentation index, and the skill index.
2. Compare commands with `justfile`, the root/app `package.json`, scripts, and
   workflow definitions. Check working directory, prerequisites, output, and
   side effects. A command name or old comment is not proof of behavior.
3. Separate current behavior, design proposals, historical recovery procedures,
   and release/production state. Preserve old recovery guidance when an installed
   version still needs it; label its scope instead of deleting it blindly.
4. Change the canonical guide first, then update both README languages and indexes.
   Keep upstream references and license notices. Avoid copying long procedures
   into multiple skills.

## Verify

Run from the repository root:

```bash
python3 -B scripts/check-docs-brand.py --docs-only
python3 -B -m unittest discover -s scripts -p test_check_docs_brand.py
```

Use the full check without `--docs-only` when assets change. For translation
helper changes, also run:

```bash
node --test .agents/skills/i18n-translate/scripts/apply-translations.test.mjs
```

The checker covers selected entry documents and project-owned skill entry points,
not all Markdown semantics or remote links. Inspect changed command examples
separately. Run safe examples in an isolated environment when possible. Do not
execute a deployment, migration, registration, payment, or settlement example
merely to validate documentation.

Report scope, changed files, actual checks, commit/PR, and any unverified step.
Never claim a full repository or production check from local fixtures. Do not
change workflow triggers or production state as a side effect of this task.
