# LMM API brand assets

The product name is **LMM API**, short for **Language Model Mesh** (语言模型网络).
The user-supplied [original master](lmm-api-source.png) defines the folded ribbon
symbol, charcoal/gray shading and geometric wordmark. The project SVG versions
are editable adaptations; they are not pixel-exact traces of that raster.

| Asset | Use |
| --- | --- |
| [lmm-logo.svg](lmm-logo.svg) | Horizontal symbol, LMM API wordmark and Language Model Mesh tagline; both READMEs. |
| [lmm-symbol.svg](lmm-symbol.svg) | Folded ribbon symbol for light and dark surfaces. |
| [lmm-wordmark.svg](lmm-wordmark.svg) | Geometric LMM API lettering. |
| [logo-geometry.json](logo-geometry.json) | Shared paths, original source hash and consumer list. |

`LmmBrandMark` and `LmmBrandWordmark` reuse those paths inline. They inherit
`currentColor`, retain accessible labels and require no image or font request.
The SVG gradient IDs are unique per rendered symbol. Tenant-provided names and
logos keep their existing behavior. Old built-in names and logo URLs resolve to
the new identity; static legacy SVG paths remain aliases for cached bookmarks.

The favicon and Apple touch icon are rasterized from the same symbol on a light
square, with hashes and commands in their sidecars. The old responsive README
cover paths also reuse the new horizontal logo. No old Forge artwork is shipped
at those paths.

Run `python3 -B scripts/check-docs-brand.py` and the checker tests through mise.
The check validates safe SVG structure, shared symbol/wordmark paths, source and
raster hashes. Inspect light/dark surfaces and native favicon sizes in a browser;
source checks do not establish deployment or backend compatibility.
