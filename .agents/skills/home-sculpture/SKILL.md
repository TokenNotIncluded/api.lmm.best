---
name: home-sculpture
description: "Edit homepage particle sculptures, carousel scenes and motion in apps/web, including 首页动画 and reference-led geometry. Not general UI or static artwork. Edit source code, not generated images."
---

# Reference-led homepage sculptures

Preserve the existing page and three-dimensional point field. Start with the
requested scene, `apps/web/src/features/home/sculptures/README.md` and registry
`home-sculptures.ts`. Read `home-poster.ts`, `home-motion.ts` or
`sculptures/geometry.ts` only for affected rendering, motion or geometry contracts.
Check the current scene list; do not restore retired objects from an old guide.

Use [the scene brief](references/scene-brief.md) to record the observed silhouette
and motion. Inspect real image dimensions or representative video frames; a failed
fetch is not a viewed reference. Complete independent work if input is unavailable,
but leave the blocked scene unregistered, not an invented substitute or empty slot.
Do not identify people in reference images.

Build sampled surfaces with depth, not a pasted photo, iframe or flat card. Reuse
the point brush, materials and transition system. A single-view relief must note
its approximate unseen back and limit rotation. The optional
[bake helper](scripts/bake-materials.py) needs an explicit subject mask; it is
material baking, not anatomy reconstruction or a scan.

Keep points immutable. Prepare transforms once per frame and modify only the
supplied scratch vector. No per-particle allocation, extra animation loop or
per-frame React state. Keep connected parts joined and loops continuous. Use the
current renderer budgets, not higher point/cache limits to hide poor geometry.

Register the factory at the requested position. The first item means
`HOME_SEQUENCES[0][0]`. Preserve other chapters; update affected order, first-frame
and clock assertions without weakening them.

Run focused sculpture/poster tests, changed-file format/lint and the Web typecheck
for TypeScript changes. Check finite positions/colors, immutable rest data, local
motion, counts and bounds at relevant keyframes and phone budgets. Material
changes need decode and transparent-corner checks. Registration, imports, assets
or bundle changes need `mise exec -- just build-web`, not another bundle check.

Review actual code in a browser: changed scene, relevant themes, phone/desktop,
pointer/touch, pause, reduced motion, hidden/offscreen state and adjacent transition.
Reuse evidence for unchanged behavior. A standalone renderer is not full-app or
real-device acceptance. Never use image generation as test evidence.

Done means the requested scene/motion, focused checks and any affected scene docs.
Report blocked references, unavailable browser checks and commit/PR separately
from deployment. Keep uploaded originals and temporary previews out of the commit.
