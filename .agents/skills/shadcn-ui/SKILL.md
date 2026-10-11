---
name: shadcn-ui
description: "Implement React components, forms, themes and responsive layouts in apps/web. Reuse installed components and tokens. Not for backend work or a blanket performance audit."
---

# Project UI workflow

Start with the affected component and a nearby working example. Reuse the current
components, tokens and composition APIs. For visual changes, consult
[frontend design](../../../docs/frontend-design.md). Do not assume Next.js,
a component base, icon library or alias from an upstream example.

## Select the necessary preparation

For an existing component edit, work directly; no CLI survey or dependency
installation is required when the relevant configuration is already known.
When adding a component, changing its base or using the CLI, read
[components.json](../../../apps/web/components.json) and
[package.json](../../../apps/web/package.json). Use the installed CLI:

```bash
cd apps/web
bun run shadcn info --json
```

Install locked dependencies with `just setup` only when needed. Do not use an
unreviewed `@latest`, replace the UI foundation, or reinstall skills to fix a widget.
For changed UI copy, also use [translations](../i18n-translate/SKILL.md).

## Verify and finish

Check the affected interaction, keyboard focus, accessible labels and relevant
loading/error states. Review affected light/dark and mobile layouts. For changed
TypeScript, run its tests and `just typecheck-web` from the repository root.
Format/lint changed files. Build with `just build-web` when imports, dependencies
or bundle behavior change, not for every copy or class-name edit.

Use actual browser evidence for visual acceptance, never a generated mock image.
Report missing browser checks. Finish with the change and its checks; do not add
a full-site redesign or performance investigation unless needed for this task.

## Optional references

Keep [upstream attribution](vendor/shadcn/UPSTREAM.txt). Load only the relevant
[CLI](vendor/shadcn/cli.md), [theme](vendor/shadcn/customization.md),
[composition](vendor/shadcn/rules/composition.md) or
[component-base](vendor/shadcn/rules/base-vs-radix.md) reference. The
[full workflow](vendor/shadcn/official-shadcn-ui-workflow.md) is background, not a
mandatory first step. Installed versions and project code take precedence over
generic examples. Verify an external API before an actual dependency upgrade.
