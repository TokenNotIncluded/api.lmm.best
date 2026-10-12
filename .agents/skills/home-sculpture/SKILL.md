---
name: home-sculpture
description: "Edit homepage particle sculptures, carousel scenes and motion in apps/web, including 首页动画 and reference-led geometry. Not general UI or static artwork. Use source code, not image generation."
---

# Reference-led homepage sculptures

Preserve the existing page and three-dimensional point field. Start with the
requested scene, `apps/web/src/features/home/sculptures/README.md`, and its registry
in `home-sculptures.ts`. Read `home-poster.ts`, `home-motion.ts` or
`sculptures/geometry.ts` only for affected rendering, motion or geometry contracts.
Check the current scene list; do not restore retired objects from an old guide.

## Inputs and implementation

Use [the scene brief](references/scene-brief.md) to record the observed silhouette
and motion. Inspect actual image dimensions or representative video frames;
a failed fetch is not a viewed reference. Complete independent work if a needed
reference is unavailable, but leave that scene unregistered rather than inventing
it or inserting an empty slot. Do not identify people in reference images.

Build sampled surfaces with depth, not a pasted photograph, iframe or flat card.
Reuse the shared point brush, materials and transition system. A single-view
relief must state its approximate unseen back and limit rotation. The optional
[bake helper](scripts/bake-materials.py) needs an explicit subject mask; use it
only for material baking, not as a claim of anatomy reconstruction or a scan.

Keep scene points immutable. Prepare transforms once per frame and modify only
the supplied scratch vector. No per-particle allocation, extra animation loop
or per-frame React state. Keep connected parts joined and loops continuous.
Use current renderer budgets; do not raise point/cache limits to hide bad geometry.

Register the factory and place its ID at the requested position. The first item
means `HOME_SEQUENCES[0][0]`. Preserve other chapters and adjust affected order,
first-frame or clock assertions without weakening them.

## Verify the changed scene

Run focused sculpture/poster tests, changed-file format/lint and the Web typecheck
for changed TypeScript. Check finite positions/colors, immutable rest data, local
motion, sample count and projected bounds at relevant keyframes and phone budgets.
Material changes need decode and transparent-corner checks. Build only when
registration, imports, assets or bundle behavior changed; use `just build-web`.

Review the actual code in a browser: the changed scene, light/dark, phone/desktop,
pointer/touch, pause, reduced motion, hidden/offscreen state and adjacent transition
as affected. Reuse existing valid tests and screenshots for unchanged behavior.
Never use an image generator as test evidence. Report unavailable browser/device
checks rather than treating a standalone renderer as full-app acceptance.

## Done when

The requested scene and motion work, focused checks are recorded, and any changed
scene documentation is current. Report blocked references and commit/PR separately
from deployment. Keep uploaded originals and temporary previews out of the commit.
