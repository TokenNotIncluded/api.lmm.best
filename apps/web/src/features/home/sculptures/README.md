# Homepage sculptures

The five chapters, page content, native scrolling, controls and point brush stay in place. Each chapter owns a looping object sequence. Geometry and object motion are separate from page layout.

`home-sculptures.ts` defines the typed factory registry and sequences. Each object holds for 7.2 seconds, then disperses and reforms for 2.4 seconds. The last object returns to the first. Only visible canvases advance their clocks. Pause, reduced motion and data-saving keep the existing lifecycle behavior. All canvases share the existing animation loop.

| File | Objects |
| --- | --- |
| `nature.ts` | Lotus, fish, jellyfish, dandelion, cloud, dragonfly, rose |
| `market.ts` | Shop, vending machine, market stall |
| `abstract.ts` | Radial mechanism, gyroscope, Mobius strip, braided trefoil, double helix, wave ribbons |
| `cosmos.ts` | Earth, Moon, Mars, Sun, solar system, galaxy, black hole |
| `future.ts` | Pelican cycling, emperor cycling, cat with a cartoon fuse |
| `bicycle.ts` | Shared frame, wheels, crank, pedals and fixed-length rider legs |

## Add an object

Export a factory from a category module, returning a `Sculpture` with immutable sampled `points` and `animate(seconds)`. Add its name to `HOME_SEQUENCES`. TypeScript rejects unknown names. Keep helpers private or in a separate module: exported category functions become factories.

Coordinates use x to the right, y up and z towards the viewer. Keep the animated silhouette near the origin and within about 1.3 units, including fins, tassels and other moving parts. Check the projected bounds at phone and desktop sizes, not only the source coordinates. A positive view pitch looks up at a surface; the lotus uses a negative pitch to expose its centre.

Use the shared `Shape` surfaces, ellipsoids, tubes, rings and boxes. Do not add a separate animation loop, video, SVG or remote asset for an object. `part` identifies moving wings, wheels, eyes and limbs. Prepare transforms once per frame. Change only the supplied scratch vector, never a shared point. Do not allocate a new vector per particle. The shared model cache is limited to ten entries.

Lotus and rose petals have sampled surface lighting, rims, veins and a shared open/closed pose. The lotus is now procedural in the main renderer, not replaced by image sampling. The existing packaged lotus remains the page's non-canvas fallback. Readiness is reported after the first drawn frame, once, and never after disposal.

Both cyclists use the same crank and foot positions. The upper and lower legs have fixed lengths. Shoes translate with the pedals instead of wobbling independently. Keep their contact tests when changing body proportions. The emperor is a stylized fictional depiction, not a historical reconstruction.

## Theme and verification

`--poster-ground` in `forge-home.css` is a six-digit hex colour. Bitmap fill, dot coverage and trails use that same ground. Light mode uses deeper material colours and greater dot coverage, while preserving surface shading. The theme observer in `home-motion.ts` refreshes paused scenes and is removed on cleanup.

Run the existing frontend tests, type check, format check and build from `apps/web`. The sculpture and poster tests cover all 26 models, sequence boundaries, looping, geometry, immutable samples, motion, palette contrast, independent clocks, pause, first-frame readiness and disposal. Additional checks cover feet/pedals, fixed leg lengths, gyroscope tilt and the fish/helix projected bounds. The 3:1 palette assertion concerns solid decorative dots only, not page-wide text accessibility.

Browser review must include light/dark themes, phone/desktop widths, several phases of each motion, transitions, pointer recovery, offscreen behavior and reduced motion. A standalone canvas capture verifies the renderer and sculptures; it does not establish a successful full-app build, real-device performance or production deployment. Continent outlines and astronomical scenes remain stylized, not maps or scale models.
