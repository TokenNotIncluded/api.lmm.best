# Project skills

[Agent guide](../../AGENTS.md) · [Working guide](../../docs/agent-workflows.md) · [Documentation](../../docs/README.md)

Read only the skill that matches the task. Add another only when the work crosses
its scope, such as UI code with new translations. References are not startup reading.

| Skill | Trigger | Do not load for |
| --- | --- | --- |
| [docs-maintenance](docs-maintenance/SKILL.md) | Docs, AGENTS.md, skill text, links or examples | Ordinary code edits with no documentation work |
| [deployment](deployment/SKILL.md) | Release, deploy, upgrade or recover | A code edit that does not publish or change a server |
| [home-sculpture](home-sculpture/SKILL.md) | Homepage particle geometry, carousel scenes or motion | General UI or static artwork |
| [i18n-translate](i18n-translate/SKILL.md) | UI copy, translation values or locale keys | Prose outside the UI |
| [shadcn-ui](shadcn-ui/SKILL.md) | Components, forms, themes or responsive layout | Backend-only work |
| [vercel-react-best-practices](vercel-react-best-practices/SKILL.md) | Rendering, loading or bundle performance | Every React edit by default |
| [publish-open-source-bounty](publish-open-source-bounty/SKILL.md) | Draft, price or publish a bounty | Ordinary GitHub issues |
| [review-open-source-bounty](review-open-source-bounty/SKILL.md) | Review or settle a bounty-linked submission | Ordinary PR review or merge |

Keep the directory and frontmatter `name` identical. Put precise trigger words
first in a short `description`. Keep instructions local; move optional detail
into references. Preserve vendored attribution and do not create duplicate
`SKILL.md` entry points for upstream material.

Skills describe a method. They do not provide credentials, connect tools, grant
permissions or authorize a paid operation. Use the
[working guide](../../docs/agent-workflows.md) for check selection and completion.
For skill text, run `python3 -B scripts/check-docs-brand.py --docs-only` once;
helper self-tests are needed when the corresponding helper changes.
