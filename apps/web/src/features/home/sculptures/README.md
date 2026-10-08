# Homepage sculptures

The five existing chapters, content, controls, scroll layout and point brush remain in place. Each chapter now owns a looping object sequence. Geometry, object motion and the sequence are separate from the page layout.

`home-sculptures.ts` defines the typed factory registry and the five sequences. Each object holds for 7.2 seconds, then disperses and reforms for 2.4 seconds. The last object returns to the first. Only visible canvases advance their clocks; pause, reduced motion and data-saving stop them. All canvases share the existing single animation loop.

| File | Objects |
| --- | --- |
| `nature.ts` | Lotus, fish, jellyfish, dandelion, cloud, dragonfly |
| `market.ts` | Shop, vending machine, market stall |
| `abstract.ts` | Socket, gyroscope, Möbius strip, trefoil, double helix, wave ribbons |
| `cosmos.ts` | Earth, Moon, Mars, Sun, solar system, galaxy, black hole |
| `future.ts` | Pelican cycling, emperor riding a polar bear, cat lighting a cartoon fuse |

## Add an object

Export a factory from one of these files. Return a `Sculpture` with immutable sampled `points` and an `animate(seconds)` function. Add its factory name to the appropriate `HOME_SEQUENCES` entry. Unknown names are rejected by TypeScript. A new category module also needs an import and registry entry in `home-sculptures.ts`.

Coordinates use x to the right, y up and z towards the viewer. Keep the main silhouette within about 1.3 units of the origin. The shared `Shape` builder provides sampled surfaces, ellipsoids, tubes, rings and boxes. Reuse the shared palette and fine-point renderer; do not introduce a separate SVG, video, CSS animation or canvas loop for an object.

Use `part` to identify wings, wheels, seeds and other moving parts. `animate` prepares a transform once per frame. Its returned function changes only the supplied scratch vector, never the shared source point. Do not create timers or allocate a new vector for each particle. The shared model cache is bounded to ten entries, excluding the original sampled lotus.

The original packaged lotus image still supplies the normal first scene. The procedural lotus is a fallback for image or canvas decode failure. No new images, fonts, remote assets or runtime dependencies are needed. Continent outlines and astronomical scenes are stylized, not scientific maps or scale models.

## Theme and tests

The cinema's `--poster-ground` token in `forge-home.css` is a six-digit hex color. The bitmap, dot coverage and brush trails all use that same ground. Its light/dark values must not be changed separately in the renderer. The root theme observer also refreshes paused scenes and is removed on cleanup.

Run the frontend test and type-check commands from `apps/web`. The sculpture tests check every object's geometry and motion, sequence boundaries, looping, independent clocks, pause, image failure, theme pixels and disposal. Keep the existing motion lifecycle tests. Browser review must cover both themes, phone and desktop widths, the moving objects, page transitions, pointer recovery, hidden/offscreen behavior and reduced motion.
