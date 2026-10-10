# LMM Forge logo assets

[Project overview](../../README.md) · [Design record](../../DESIGN.md)

The identity keeps the original black-and-white cut lettering. The compact mark now separates the two M forms from the L foundation. Wider gaps make its parts easier to distinguish in small placements.

## Choose an asset

| Asset | Use |
| --- | --- |
| [lmm-logo.svg](lmm-logo.svg) | README cover mark: white symbol on a black, corner-cut tile. Fixed colors work on light and dark pages. |
| [lmm-symbol.svg](lmm-symbol.svg) | Transparent 128 × 128 symbol. Black by default; white in dark mode. |
| [lmm-wordmark.svg](lmm-wordmark.svg) | Full LMM Forge lettering, unchanged. Use when there is enough horizontal space. |
| [logo-geometry.json](logo-geometry.json) | Master geometry and asset provenance. |

The website uses the same symbol in `LmmBrandMark` and `apps/web/public/lmm-cut-mark.svg`. The component inherits `currentColor`, so the application's selected theme controls the ink. Tenant-defined logos are not replaced.

## Size and spacing

Keep the full 128-unit symbol viewBox. The mark uses a 16-unit L spine and 16-unit gaps between the main parts and above the base. Inspect at 16, 24, and 32 CSS pixels before using it in a small slot. Do not add padding inside a 16px favicon; use the transparent symbol rather than shrinking the README tile into it.

Use the full wordmark at a cap height of at least 20px. Allow at least one wordmark stem of clear space. Scale all artwork proportionally. Keep the open gaps and 45-degree terminal cut.

## Existing covers

The [desktop light](readme-cover-light.svg) / [dark](readme-cover-dark.svg) and [mobile light](readme-cover-mobile-light.svg) / [dark](readme-cover-mobile-dark.svg) covers remain available for existing links. They use the unchanged full lettering. The README now uses the compact tile instead of an oversized wordmark cover.

## Edit and verify

Update `symbol.path` in `logo-geometry.json`, the transparent SVG, the tile, the website SVG, and `LmmBrandMark` together. Recreate the Apple touch icon and ICO from that same path. Their JSON sidecars record their source, not an image-generation prompt.

```bash
python3 scripts/check-docs-brand.py
```

Run this from the repository root. The check detects different symbol paths, unsafe SVG content, broken local entry-document links, and mismatched README badge sets. Also inspect the logo on light and dark surfaces; a source check cannot judge visual quality.

## Source and license

The logo uses original SVG coordinates, not a font or stock artwork. No scripts, external fonts, or remote resources are embedded. Raster app icons are derived from the same SVG path. The assets remain under the repository's [AGPL-3.0 license](../../LICENSE).

TokenRouter inspired the README information order, not this logo. Do not use the reference project's logo, screenshots, license, or feature claims as LMM Forge assets.
