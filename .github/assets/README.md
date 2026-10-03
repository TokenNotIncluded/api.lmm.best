# LMM Forge brand assets

Original black-and-white vector lettering for the repository README. The public web console keeps its own visual system; these files do not change application icons.

## Assets

| File | Use |
| --- | --- |
| [lmm-symbol.svg](lmm-symbol.svg) | Compact, transparent 128 × 128 symbol. Black ink in light mode, white ink in dark mode. |
| [lmm-wordmark.svg](lmm-wordmark.svg) | Complete, transparent LMM Forge wordmark. Same automatic ink switch. |
| [readme-cover-light.svg](readme-cover-light.svg) / [dark](readme-cover-dark.svg) | 1440 × 600 stepped desktop composition. |
| [readme-cover-mobile-light.svg](readme-cover-mobile-light.svg) / [dark](readme-cover-mobile-dark.svg) | 720 × 480 stacked composition, selected at a viewport width of 640px or less. |
| [logo-geometry.json](logo-geometry.json) | Exact master paths, letter positions, proportions, and provenance. |

## Form

The symbol joins a thick L spine and base to two open M shapes. A single 45-degree terminal cut gives the base its finish. The wordmark carries the same broad strokes, clear counterforms, and cut terminals through original uppercase glyphs.

Use the symbol from 16px. Use the full wordmark at a cap height of at least 20px so the R counter and G aperture stay distinct. Keep the symbol's full viewBox in small icon slots. At larger sizes, allow clear space equal to one symbol spine or one wordmark stem around the artwork.

Scale proportionally and retain the open counters. For fixed light or dark backgrounds, set the SVG `.ink` fill explicitly to black or white instead of its media query. The cover files already have explicit backgrounds and ink.

## Source and license

All shipping artwork is original, precisely authored SVG geometry. No font outlines, generated raster, stock artwork, remote fonts, scripts, or external resources are embedded. Every cover reuses the exact word paths from `logo-geometry.json`; the LMM and FORGE groups can be arranged separately without changing their internal spacing.

These assets are covered by this repository's [AGPL-3.0 license](../../LICENSE). Inspection screenshots are derived verification artifacts and are not shipping brand assets.
