# Project skills

[Agent guide](../../AGENTS.md) · [Working guide](../../docs/agent-workflows.md) · [Documentation](../../docs/README.md)

Load the matching `SKILL.md` before the task. Combine skills when necessary: a UI
change with new text needs both the UI and translation skills. Do not load every
reference document into each task.

| Skill | Use it for |
| --- | --- |
| [docs-maintenance](docs-maintenance/SKILL.md) | Check README, docs, skills, source paths, and command examples. |
| [deployment](deployment/SKILL.md) | Publish, deploy, upgrade, or recover the requested component. Production authorization is separate. |
| [i18n-translate](i18n-translate/SKILL.md) | Add or change UI text and translations, including a single key. |
| [shadcn-ui](shadcn-ui/SKILL.md) | Work on components, forms, themes, and responsive layouts. |
| [vercel-react-best-practices](vercel-react-best-practices/SKILL.md) | Investigate React performance in this Rsbuild application. |
| [publish-open-source-bounty](publish-open-source-bounty/SKILL.md) | Draft and publish a bounty with a verified financial preview and confirmation. |
| [review-open-source-bounty](review-open-source-bounty/SKILL.md) | Review delivery evidence and handle confirmed settlement actions. |

The folder name and frontmatter `name` must match. Each skill needs a clear,
non-empty `description`. Keep the entry point short; link to scripts or references
for detail. A skill describes a workflow. It does not grant account permissions,
provide a credential, connect an unavailable tool, or authorize a paid operation.

Project paths and commands belong in the local entry point. Keep upstream material
under `vendor/` or `references/`, with its attribution intact. Do not create a
second discoverable entry point for the same upstream skill.

From the repository root:

```bash
python3 -B scripts/check-docs-brand.py --docs-only
python3 -B -m unittest discover -s scripts -p test_check_docs_brand.py
node --test .agents/skills/i18n-translate/scripts/apply-translations.test.mjs
```

The first check covers maintained entry documents, skill links and required
metadata, stale frontend paths in local skills, and README badges. It does not
validate every Markdown file, all YAML syntax, remote URLs, heading anchors, or
application behavior. Run the full check without `--docs-only` for logo changes.
