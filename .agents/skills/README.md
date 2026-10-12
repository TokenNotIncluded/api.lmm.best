# Project skills

[Agent guide](../../AGENTS.md) · [Working guide](../../docs/agent-workflows.md)

Read only the matching skill. Add another when work crosses its scope, such as
UI code with new translations. References are not startup reading.

| Skill | Trigger | Not for |
| --- | --- | --- |
| [docs-maintenance](docs-maintenance/SKILL.md) | Docs, AGENTS.md, skill text, links or examples | Every code edit |
| [deployment](deployment/SKILL.md) | WIP packages, isolated installation or recovery planning | Ordinary source edits without packaging |
| [home-sculpture](home-sculpture/SKILL.md) | Homepage particle scenes, geometry or motion | General UI or static artwork |
| [i18n-translate](i18n-translate/SKILL.md) | UI copy or locale keys, including one-key fixes | Non-UI prose |
| [shadcn-ui](shadcn-ui/SKILL.md) | Components, forms, themes or layout | Backend-only work |
| [vercel-react-best-practices](vercel-react-best-practices/SKILL.md) | React request, render or bundle performance | Every React edit |
| [publish-open-source-bounty](publish-open-source-bounty/SKILL.md) | Bounty drafts, prices or publication | Ordinary GitHub issues |
| [review-open-source-bounty](review-open-source-bounty/SKILL.md) | Bounty delivery review or settlement | Ordinary PR review or merge |

Keep the directory and frontmatter `name` identical. Use a short `description`
with clear triggers and exclusions. Keep one task per skill, share check policy
through the working guide, and load large references only when needed. Preserve
upstream attribution; do not create duplicate discoverable `SKILL.md` files.

A skill does not connect tools, provide credentials or authorize an operation.
The bounty skills describe the connected service, not proof that this WIP has
implemented or deployed every corresponding server module.

For skill text, run `python3 -B scripts/check-docs-brand.py --docs-only` once.
Only changes to a helper need that helper's self-tests. The checker does not
verify remote URLs, anchors, actual skill selection or command behavior.
