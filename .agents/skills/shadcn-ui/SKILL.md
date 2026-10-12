---
name: shadcn-ui
description: "Implement React components, forms, themes and responsive layouts in apps/web. Reuse installed components and tokens. Not for backend work or a blanket performance audit."
---

# Project UI workflow

Start with the affected component and a nearby working example. Reuse current
components, tokens and composition APIs. For visual changes read
[DESIGN.md](../../../apps/web/DESIGN.md). Do not assume Next.js, a component base,
icon library or alias from an upstream example.

For an existing component edit, work directly. Adding components or using the
CLI needs [components.json](../../../apps/web/components.json) and
[package.json](../../../apps/web/package.json). Use the installed CLI:

```bash
cd apps/web
mise exec -- bun run shadcn info --json
```

Install locked dependencies only when needed. Do not use an unreviewed `@latest`,
replace the UI foundation, or reinstall skills to fix a widget. Changed UI copy
also uses [translations](../i18n-translate/SKILL.md).

Check affected interaction, keyboard focus, labels and loading/error states.
Review relevant light/dark and mobile layouts. For TypeScript changes, run
related tests and `mise exec -- bun run typecheck:web` from the repository root.
Format/lint changed files. Use `mise exec -- just build-web` for imports,
dependencies, resources or bundle changes, not every text/class-name edit.
It already includes the bundle check.

Use browser evidence from the actual code, never a generated mock image. Report
missing visual checks. Stop after the requested UI and checks; do not add a
whole-site redesign or performance audit.

Preserve [upstream attribution](vendor/shadcn/UPSTREAM.txt). Load only the relevant
[CLI](vendor/shadcn/cli.md), [theme](vendor/shadcn/customization.md),
[composition](vendor/shadcn/rules/composition.md) or
[component-base](vendor/shadcn/rules/base-vs-radix.md) reference. The full upstream
workflow is optional background. Installed versions and project code take priority.
