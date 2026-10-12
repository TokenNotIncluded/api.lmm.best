---
name: docs-maintenance
description: "Update repository docs, AGENTS.md, skills, links and command examples. Use for documentation work, not as a prerequisite for every code edit."
---

# Maintain documentation and skills

Make the changed guidance accurate and easy to find at the selected revision.
Use [the working guide](../../../docs/agent-workflows.md) for shared check policy.

## Short path

1. Read the affected document and the code, configuration or script behind its
   claims. Follow only the links needed to resolve the change. A typo does not
   need an audit of both READMEs, all skills and the full repository history.
2. Edit the canonical guidance. Update an index, translation or other entry
   only if the change affects it. Remove duplicate instructions rather than
   adding another copy. Keep current behavior distinct from proposals and
   historical recovery procedures; preserve needed recovery details and licenses.
3. Run `python3 -B scripts/check-docs-brand.py --docs-only` from the repository
   root and inspect the diff. Check changed documents outside its maintained
   set separately. It does not validate remote URLs, anchors or command behavior.
4. Validate changed examples against their implementation. Use syntax checks or
   isolated fixtures where useful. Never run production, migration, registration
   or payment examples merely to validate prose.

Run the checker's unit tests only if the checker changes. Run the full asset
check only for logo changes, and a helper's tests only when that helper changes.
Do not install application dependencies or build Go/Web for Markdown-only work.

## Done when

The requested guidance is correct, its entry links work, and actual checks and
limitations are reported with the commit/PR. No extra plan, audit document,
workflow trigger change, release or production operation is required.
