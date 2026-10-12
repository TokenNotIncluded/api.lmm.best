---
name: vercel-react-best-practices
description: "Investigate a measured React rendering, request, event or bundle bottleneck in apps/web. Use for performance work, not every React, copy or styling edit."
---

# React performance in this project

This is an Rsbuild application with TanStack Router and Query, not Next.js.
Use [installed dependencies](../../../apps/web/package.json) and the existing
data layer. Do not introduce Server Components, Server Actions, Next.js routing,
`next/dynamic` or SWR just because a generic example uses them.

## Short path

1. Define the affected interaction and representative workload. Reuse an
   available trustworthy baseline instead of starting a whole-site benchmark.
2. Inspect the relevant request order, fetches, renders, imports, listeners or
   animation work. Fix the largest supported cost, not every possible rule.
3. Search [the reference](references/full-guide.md) and read only the applicable
   section when needed. Skip framework-specific rules that do not apply.
4. Repeat the same measurement and affected correctness checks. Keep request
   ordering, authentication, cancellation and account/permission isolation.

Run affected tests and the Web typecheck for TypeScript changes. Import/loading
changes also need `just build-web`, which includes the bundle check. Do not run
that check again. For motion changes, inspect affected mobile and reduced-motion
behavior. Reuse valid checks from the same task.

## Done when

The observed bottleneck has a supported fix and correctness checks. Report the
comparable measurement, or state that performance remains unmeasured. A smaller
bundle or passing build alone does not prove a faster interaction. Do not add
cross-account caches or unrelated rewrites to improve a benchmark.

Preserve attribution in the [compiled reference](references/full-guide.md) and
its [Vercel source](https://github.com/vercel-labs/agent-skills/tree/main/skills/react-best-practices).
