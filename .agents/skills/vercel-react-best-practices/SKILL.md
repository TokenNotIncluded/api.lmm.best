---
name: vercel-react-best-practices
description: "Investigate a measured React rendering, request, event or bundle bottleneck in apps/web. Use for performance work, not every React, copy or styling edit."
---

# React performance in this project

This is an Rsbuild application with TanStack Router and Query, not Next.js.
Use [installed dependencies](../../../apps/web/package.json) and the existing
data layer. Do not introduce Server Components, Server Actions, Next.js routing,
`next/dynamic` or SWR just because a generic example uses them.

1. Define the affected interaction and representative workload. Reuse a valid
   baseline instead of starting a whole-site benchmark.
2. Inspect the relevant request order, fetches, renders, imports, listeners or
   animation work. Fix the largest supported cost, not every possible rule.
3. Search [the reference](references/full-guide.md) and read only the needed
   section. Skip framework-specific rules that do not apply.
4. Repeat the same measurement and affected correctness checks. Preserve request
   ordering, authentication, cancellation and account/permission isolation.

TypeScript changes need related tests and `mise exec -- bun run typecheck:web`
from the repository root. Import/loading changes need `mise exec -- just build-web`,
which includes the bundle check. Do not run it twice. Motion changes also need
relevant mobile and reduced-motion review. Reuse valid same-task check results.

Done means a supported fix for the observed cost and recorded correctness checks.
Report comparable measurements, or state performance is unmeasured. A smaller
bundle alone does not prove faster interaction. Do not add cross-account caches
or unrelated rewrites to improve a benchmark.

Preserve attribution in the compiled reference and its
[Vercel source](https://github.com/vercel-labs/agent-skills/tree/main/skills/react-best-practices).
