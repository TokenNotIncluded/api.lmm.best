---
name: shadcn-ui
description: >-
  Build or review this project's React UI, shadcn components, forms, themes,
  responsive layouts, and component presets. Read apps/web configuration first
  and use the installed CLI and project conventions before upstream references.
---

# Project UI workflow

## Establish project context

Read [components.json](../../../apps/web/components.json),
[package.json](../../../apps/web/package.json), and
[frontend design](../../../docs/frontend-design.md) before changing components.
The app is in `apps/web`. Do not assume Next.js, a particular component base,
icon library, or import alias from a generic example.

Install locked dependencies with `just setup` from the repository root when
needed. Use the installed CLI, not an unreviewed `@latest` version:

```bash
cd apps/web
bun run shadcn info --json
```

Read its output together with the checked-in configuration. Inspect nearby
components before adding a dependency, registry, preset, or replacement widget.
A skill update is not permission to reinstall skills or change the UI foundation.

## Implement and check

1. Reuse the project's components and tokens. Follow the current base library's
   composition APIs, not a copied example from another library.
2. For new UI copy, also load the
   [translation skill](../i18n-translate/SKILL.md).
3. Check keyboard focus, accessible labels, loading, empty and error states,
   light/dark themes, and mobile layout. Do not generate a mock screenshot and
   present it as a browser test.
4. Run relevant tests and `bun run typecheck` from `apps/web`. For bundle changes,
   also run `bun run build` and `bun run bundle:check`. Record actual browser
   checks and any missing visual evidence separately.

## Load only the needed reference

The upstream snapshot and its attribution remain in
[UPSTREAM.txt](vendor/shadcn/UPSTREAM.txt). Project paths and installed versions
come from this entry point and the repository, not the snapshot.

| Topic | Reference |
| --- | --- |
| Full upstream workflow | [Workflow](vendor/shadcn/official-shadcn-ui-workflow.md) |
| CLI and registries | [CLI](vendor/shadcn/cli.md) |
| Themes | [Customization](vendor/shadcn/customization.md) |
| MCP | [MCP](vendor/shadcn/mcp.md) |
| Forms and composition | [Forms](vendor/shadcn/rules/forms.md), [composition](vendor/shadcn/rules/composition.md) |
| Styling and icons | [Styling](vendor/shadcn/rules/styling.md), [icons](vendor/shadcn/rules/icons.md) |
| Component base | [Base vs Radix](vendor/shadcn/rules/base-vs-radix.md) |

Read the full upstream workflow only for a task that needs it. Confirm external
API changes against the relevant official documentation before upgrading a tool.
Do not overwrite the vendored reference to fix a project path.
