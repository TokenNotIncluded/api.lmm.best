# LMM Forge repository identity

This record covers the README, reusable logo assets, and the shared default symbol. Application layouts follow their own design records.

## Identity

Keep the established black-and-white cut lettering. The updated compact mark has an L foundation below two separate M forms. A 16-unit gap separates the main parts and the foundation; a 45-degree cut ends the base. These gaps replace the previous joined, tightly spaced silhouette.

The source is [logo-geometry.json](.github/assets/logo-geometry.json). The full wordmark is unchanged. Do not substitute a font, stretch the glyphs, close the gaps, or add gradients, shadows, or motion to the logo.

## README

Use a compact, centered logo, the project name, one descriptive line, four useful badges, and native text links. Chinese is the default README; English is available in `README_EN.md`. Both versions must describe the same features and limits.

The badges show the actual CI workflow, separate Go and Web releases, and this project's AGPL license. Do not use a general latest-release badge that can confuse Go and Web. Do not add a container badge while the documented checkout lacks Dockerfiles.

Below the header, show the purpose, core features, live site, local start, deployment choices, documentation, and contribution and license details. Keep detailed environment and recipe notes in [the development guide](docs/development.md). Use GitHub-native text and tables; do not put essential instructions inside an image.

TokenRouter is the information-layout reference. No reference-project artwork, screenshots, feature claims, or license text are copied.

## Logo placements

| Placement | Asset or behavior |
| --- | --- |
| README | [Fixed black tile with a white mark](.github/assets/lmm-logo.svg), displayed at 96px. |
| Reusable symbol | [Transparent SVG](.github/assets/lmm-symbol.svg), black or white according to the color scheme. |
| Website | `LmmBrandMark` inherits `currentColor`; the public SVG uses the same geometry. Custom tenant logos keep their existing behavior. |
| Small icons | Keep the full 128-unit viewBox; inspect at 16, 24, and 32px without extra inner padding. |
| Full wordmark | Preserve the existing paths and spacing; minimum cap height is 20px. |

The old responsive wordmark covers remain available at their original paths. They are no longer the main README image. Do not remove them without checking external use.

## Verification

`python3 scripts/check-docs-brand.py` checks local entry-document links, matching badge sets, safe SVG structure, and shared symbol paths. Use a browser to inspect narrow and wide README layouts, light and dark themes, and the logo at native icon sizes. A source check does not establish full application compatibility or production deployment.

Artwork is authored vector geometry. App-icon rasters must identify that source in their sidecars. Do not claim a visual approval, build result, screenshot, or deployment that was not obtained.
