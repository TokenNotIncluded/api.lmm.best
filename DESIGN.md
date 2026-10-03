---
name: LMM Forge README brand
description: Original cut lettering in black and white for the repository entry and reusable SVG assets.
colors:
  black: "#000000"
  white: "#ffffff"
---

# Design System: LMM Forge README brand

## Overview

**Creative North Star: "Protocol punch"**

Broad, original cut lettering gives LMM Forge a direct silhouette. Open counterforms, sharp terminals, and strong black-and-white contrast carry the identity. The wordmark leads; flat space gives the glyphs room to read.

This record governs the root README and reusable logo assets in `.github/assets` only. Application surfaces keep their own closer-scoped design records. The user confirmed monochrome, sharp lettering, and strong contrast; no visual comp was approved. The built SVGs and [master geometry](.github/assets/logo-geometry.json) are the visual source of truth.

**Key Characteristics:**

- Original filled vector lettering with open counterforms.
- Pure black and white, with explicit light and dark placements.
- Shared master geometry, recomposed for desktop and mobile.

## Colors

The palette uses two exact colors; polarity follows the placement background.

### Primary

- **Black** (`colors.black`): lettering on the light covers and default ink in the transparent logo assets.

### Neutral

- **White** (`colors.white`): light-cover background and ink on dark placements. Dark covers reverse the same pair.

**The Full Contrast Rule.** Use black on white or white on black for the brand artwork. Keep the independent logo assets transparent; set their ink explicitly when the placement background is fixed.

## Typography

The logo is original SVG geometry, not a font. Reuse its paths rather than typing a replacement. Its master cap height is 100 geometry units, with broad vertical strokes (20 units), horizontal strokes (18 units), and small bowl overshoots (1 unit). These are drawing proportions, not CSS typography tokens.

README body text, headings, links, tables, and code blocks use GitHub-native typography and behavior. This record defines no body font, type scale, or interactive control library.

## Layout

Scale logos proportionally and preserve each word group's internal spacing. The horizontal wordmark keeps a larger word break (40 geometry units). Ordinary placements allow at least one wordmark stem (20 units) or symbol spine (18 units) of clear space, scaled with the artwork. Small icon slots retain the symbol's full viewBox without additional internal padding.

The README cover is a surface-specific composition:

| Placement | SVG canvas | Arrangement |
| --- | --- | --- |
| Desktop | 1440 × 600 | LMM at the upper left; FORGE steps down and right. |
| Mobile | 720 × 480 | Both words form a left-aligned stack; FORGE scales to fit. |

The README `<picture>` selects the mobile cover at viewport widths of 640px or less and selects the matching dark cover with `prefers-color-scheme: dark`. The light desktop cover is the fallback. Product copy and working links sit below the static artwork; GitHub controls their wrapping and interaction.

## Elevation & Depth

The artwork is flat. It uses no custom shadows, gradients, textures, animation, or transitions. Depth comes from filled silhouettes and open space. GitHub's surrounding interface remains host-controlled.

## Shapes

Filled paths, squared bowls, open M counterforms, and consistent diagonal terminal cuts define the form. The compact symbol connects a thick L spine and base to two open M shapes; its final base cut is 45 degrees. Use `fill-rule="evenodd"` so internal counterforms remain open.

**The Master Geometry Rule.** Preserve the exact paths and within-group positions from `logo-geometry.json`. LMM and FORGE may move and scale uniformly as separate groups; never stretch or redraw the glyphs.

## Components

### Compact symbol

[lmm-symbol.svg](.github/assets/lmm-symbol.svg) is a transparent 128 × 128 asset with `viewBox="0 0 128 128"`. Use it from 16px. At that size, the L-to-M gap is one pixel and each M stem is 1.5 pixels, so preserve the full viewBox and inspect at native size. Prefer the symbol when the wordmark would fall below its minimum cap height.

### Full wordmark

[lmm-wordmark.svg](.github/assets/lmm-wordmark.svg) is a transparent 718 × 104 asset with `viewBox="0 -2 718 104"`. Keep its cap height at least 20px so the R counter and G aperture remain distinct. Both standalone logo assets default to black ink and switch to white with the dark color-scheme media query.

### README covers

The [desktop light](.github/assets/readme-cover-light.svg) / [dark](.github/assets/readme-cover-dark.svg) and [mobile light](.github/assets/readme-cover-mobile-light.svg) / [dark](.github/assets/readme-cover-mobile-dark.svg) pairs embed explicit backgrounds and the same exact word paths. Preserve their accessible titles and descriptions and the README image's descriptive alt text. Links below the image remain native text links.

All shipping artwork is precisely authored SVG with no font outlines, generated raster, stock art, external fonts, scripts, or external resources. [Usage guidance](.github/assets/README.md) and the repository license apply. Local inspection evidence is kept in `output/brand-review/` and `.impeccable/review/`. Those captures and the finish review (`disposition: ship`) are verification records, separate from the published brand assets.

## Do's and Don'ts

### Do:

- **Do** reuse the exact master paths, open counterforms, and proportional scaling.
- **Do** choose ink for the actual placement background and retain black-and-white contrast.
- **Do** keep the symbol's full viewBox and honor the minimum symbol size and wordmark cap height.
- **Do** preserve accessible image descriptions and native text links outside the artwork.

### Don't:

- **Don't** substitute a font, add outlines, fill counterforms, or stretch the glyphs.
- **Don't** add gradients, route graphs, circles, shadows, textures, or motion to these brand assets.
- **Don't** treat the README composition as an application-wide layout rule or an application logo rollout.
