# Homepage sculptures

The five chapters, page content, scrolling, controls and point brush stay in place. Each chapter owns a looping object sequence. Geometry and object motion are separate from page layout.

`home-sculptures.ts` defines the typed registry and sequences. Each object holds for 7.2 seconds, then disperses and reforms for 2.4 seconds. The last object returns to the first. Only visible canvases advance their clocks. Pause, reduced motion and data-saving use the existing lifecycle. All canvases share the existing animation loop.

## Current scene modules

| File | Active objects |
| --- | --- |
| `portrait.ts` | Reference-led smiling portrait, first in chapter one |
| `portrait-materials.ts` | Subject-only material indices for the sculpted portrait surface |
| `whale.ts` | Swimming blue whale |
| `nature.ts` | Fish, jellyfish, dandelion, cloud, dragonfly |
| `marks.ts`, `moon-far-side.ts` | Claude/OpenAI marks and the Moon's far side |
| `market.ts` | Shop, vending machine, market stall |
| `abstract.ts` | Radial mechanism, gyroscope, Mobius strip, braided trefoil, double helix, wave ribbons |
| `cosmos.ts` | Earth, Moon, Mars, Sun, solar system, galaxy, black hole |
| `future.ts`, `spectacle.ts` | Pelican cycling, rocket launch, stylized explosion, rotating chair |
| `bicycle.ts` | Shared frame, wheels, crank, pedals and fixed-length rider legs |

There are 30 registered scenes. Retired factories are not restored to the carousel. The packaged whale remains the loading/no-canvas fallback; it is not the portrait model.

## Add an object

Use the project skill at `.agents/skills/home-sculpture/SKILL.md` for image, video or text references. The reference brief and modelling limits for this update are in [REFERENCES.md](REFERENCES.md). An inaccessible reference must remain pending, not become an invented scene.

Export a factory returning a `Sculpture` with immutable sampled `points` and `animate(seconds)`. Explicitly import it into the registry and add its name to `HOME_SEQUENCES`. Keep helpers private or in a separately imported module: wildcard category exports become factories. Update sequence-order and first-frame tests when inserting a scene.

Coordinates use x to the right, y up and z towards the viewer. Keep the animated silhouette near the origin and within about 1.3 units. Check projected bounds on phone and desktop, including moving parts. A positive view pitch looks up at a surface.

Use shared `Shape` geometry. Do not add a separate animation loop, video player or remote asset. A reference portrait may use local material indices on a sculpted surface; this is a single-view relief with limited head movement, not a measured 360-degree scan. Do not paste a photograph on a plane.

`part` or precomputed weights identify moving wings, wheels, eyes and limbs. Prepare transforms once per frame. Change only the supplied scratch vector, never a shared point. Do not allocate a vector per particle. Keep small new scenes below the 22,000-point phone budget; the desktop budget is 60,000. The shared cache is limited to ten models.

The bicycle crank determines foot positions. Upper and lower legs have fixed lengths. Keep contact tests when changing proportions. The whale's pectoral fins rotate about their body attachment points and its flukes move vertically.

## Theme and verification

`--poster-ground` in `forge-home.css` is a six-digit hex colour. Bitmap fill, dot coverage and trails use the same ground. Light mode uses deeper material colours and greater dot coverage while preserving shading. The theme observer refreshes paused scenes and is removed on cleanup.

Run the existing frontend tests, type check, lint, format check and build from `apps/web`. Sculpture/poster tests cover sequence boundaries, looping, geometry, immutable samples, motion, palette contrast, independent clocks, pause, first-frame readiness and disposal. The focused `portrait.test.ts` adds expression loops, mouth-relative motion, subject-only bounds, reference depth, particle budgets and whale geometry checks.

Browser review must include light/dark themes, phone/desktop widths, motion phases, transitions, pointer recovery, offscreen behavior and reduced motion. A standalone canvas capture does not establish a full-app build, real-device performance or production deployment. Record what actually ran in the PR. Continent outlines and astronomical scenes remain stylized, not maps or scale models.
