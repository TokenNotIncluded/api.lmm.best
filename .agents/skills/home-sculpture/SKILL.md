---
name: home-sculpture
description: Build or improve api.lmm.best homepage carousel sculptures from a user image, an accessible video, a physical reference, or a text description. Use for 首页动画、图片建模、同款粒子动画、轮播场景、人物表情 and reference-led object animation. Edit source code; do not use image-generation tools.
---

# Reference-led homepage sculptures

Create a subject that belongs to the existing three-dimensional point field. Do not redesign the page around it.

## Read the local contract first

Read `apps/web/src/features/home/home-sculptures.ts`, `home-poster.ts`, `home-motion.ts` and `sculptures/geometry.ts`. Check the current branch, related open PRs and `sculptures/README.md`. Do not assume the old scene list or restore retired objects. Scene factories return immutable `points`, a camera `view` and `animate(seconds)`.

## Establish the reference

Use `references/scene-brief.md` before coding. Confirm the actual input is available. For a local image, inspect its real pixel dimensions before mapping landmarks. A displayed preview can have a different size. Never identify the person in a reference image.

For video, inspect the file and representative frames, including the beginning, middle, ending and any contact or direction change. `ffprobe` and `ffmpeg` can inspect an uploaded file. Never treat a failed URL fetch as a viewed video. Complete independent work, leave that scene unregistered and ask for the file. Do not invent a substitute or insert an empty carousel slot.

For a real animal or product, inspect a reliable primary reference. Record the source and the features actually observed. Do not call invented proportions or a single-view reconstruction a scan or a scale model. A text-only concept must also have a written silhouette and motion brief.

## Model, do not paste

Remove the reference background from the design. Do not embed the source photograph, a video player, an iframe or a flat texture card as the animation. Use sampled surfaces with depth. Keep the shared dot brush, light/dark material treatment, dispersal and reconstruction.

Prefer `Shape.surface`, `ellipsoid`, `tube`, `ring` and `box` for geometric subjects. For a reference portrait, preserve the hairline, head tilt, eye spacing, nose, mouth and clothing silhouette. Small facial features need enough samples at the phone budget.

A single-view relief may use baked subject-only material indices on a sculpted surface. Clearly document that the unseen back is approximate and limit camera rotation accordingly. Keep materials local, bounded and synchronously decoded; do not fetch the original image at runtime. `sculptures/portrait.ts` and `portrait-materials.ts` are a reference implementation, not a generic full-body scan. The optional `scripts/bake-materials.py` creates a compact material grid from an image plus an explicit subject mask. It does not infer anatomy or provide a finished model.

## Build coherent motion

Use local part IDs or precomputed pose weights. A smile must move mouth corners and cheeks relative to the skull, not merely rotate the head. Eyelids must cover the eye during blinking without exposing the page background. Keep neck/head, fin/body, feet/pedals and other contact points joined. Use phase-continuous motion at loop boundaries.

Prepare per-frame transforms once. Modify only the supplied scratch vector. Never mutate shared points, allocate a vector per particle, add a second animation loop, create React state updates per frame or alter unrelated scrolling and account logic.

Keep small additions at or below 22,000 points so mobile sampling retains their details. The existing renderer supports 60,000 desktop samples, 22,000 phone samples and a bounded ten-model cache. Do not increase those limits to conceal a modelling problem. Check the current implementation before relying on these values.

## Register and test

Explicitly import the factory into `home-sculptures.ts`, add it to the typed registry and put its ID at the requested position. A request for the first item means `HOME_SEQUENCES[0][0]`. Preserve the other chapters. Update sequence-count, first-frame and pause/clock tests when the order changes; do not weaken their assertions.

Run the focused sculpture/poster tests and the existing Web test, type, lint, format and build checks from `apps/web`. Use the commands in the current `package.json`, not commands copied from an older toolchain.

Check every sampled point for finite position/color, immutable rest data, valid colors, actual local motion, point count and projected bounds. Include narrow phone widths, desktop, smile/blink keyframes and loop boundaries. For materials, test complete decoding and transparent corners.

Use browser screenshots of the actual code for visual review. Check dark/light, phone/desktop, pointer/touch recovery, pause, reduced motion, hidden/offscreen behavior and transition to the next object. Do not use an image generator for a preview. A standalone renderer review is not a full-app build or a real-device performance test.

## Deliver

Update the sculpture documentation and reference brief. State the exact changed scenes, checks actually run, remaining blocked references, PR/commit and deployment status. Never report a pending video, unrun build or production deployment as complete. Keep temporary previews, uploaded originals and unrelated code out of the commit.
