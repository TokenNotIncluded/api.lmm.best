---
name: vercel-react-best-practices
description: >-
  Investigate React rendering, data fetching, event handling, and bundle size in
  this project's Rsbuild frontend. Use measured evidence and the relevant Vercel
  reference sections; do not apply Next.js-only rules to this application.
---

# React performance in this project

Read [apps/web/package.json](../../../apps/web/package.json) and the relevant
source first. This frontend uses React, Rsbuild, TanStack Router, and TanStack
Query. It is not a Next.js application. Do not introduce Server Components,
Server Actions, Next.js routing, `next/dynamic`, or SWR solely because an upstream
example uses them. Reuse the installed libraries and the existing data layer.

## Workflow

1. Define the slow interaction, route, device, and reproducible workload.
2. Inspect request order, repeated fetches, event listeners, render frequency,
   large imports, and animation work. Measure before changing behavior.
3. Search [the reference guide](references/full-guide.md) for the relevant rule.
   Read only that section. Skip framework-specific advice that does not apply.
4. Fix the largest supported cost first. Use parallel requests only when they
   are independent; retain authentication, cancellation, and ordering rules.
5. Repeat the same measurement and relevant correctness checks. Do not claim a
   percentage improvement without comparable before/after results.

Prioritize unnecessary sequential requests and large initial bundles, then
repeated client work, rendering, listeners, and small JavaScript optimizations.
Do not add caching across accounts or permissions to improve a benchmark.

## Checks

From `apps/web`, run the affected tests and `bun run typecheck`. For import or
loading changes, also run `bun run build` and `bun run bundle:check`. The bundle
check does not prove that an interaction is faster; retain the measured trace
or timing evidence. For user-facing motion, test mobile interaction and reduced
motion when a browser is available, and report any missing check.

## Reference

[Compiled upstream guide](references/full-guide.md) ·
[Original Vercel project](https://github.com/vercel-labs/agent-skills/tree/main/skills/react-best-practices)

Keep upstream attribution. This project-specific entry point does not replace
or silently rewrite the reference material.
