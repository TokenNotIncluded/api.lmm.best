# LMM API repository identity

The product is **LMM API — Language Model Mesh**, 中文「语言模型网络」。
The user's [original master](.github/assets/lmm-api-source.png) is authoritative:
a folded ribbon symbol in charcoal/gray with a geometric LMM API wordmark.
[Shared geometry](.github/assets/logo-geometry.json) records the editable SVG
adaptation and provenance. Keep the ribbon forms, proportions and neutral shading.

Both README languages use the horizontal logo, accessible project name, slogan,
WIP badges and native text links. Essential instructions remain selectable text.
The badges represent WIP checks and the AGPL license; they do not claim a release
or production deployment. Development and installation details remain in the
[development guide](docs/development.md).

The website's built-in symbol and lettering share those paths and inherit the
surface's text color. Neutral opacity and gradients preserve the ribbon folds
on light and dark backgrounds. Custom tenant names and logos retain their own
behavior. Old built-in logo paths alias the new symbol for cached clients.

Small icons keep a square viewBox around the complete wide symbol. Browser
favicons and Apple touch icons use a light square. Native 16, 24 and 32px sizes,
light/dark surfaces and narrow/wide headers require visual inspection; automated
source checks alone cannot establish rendering quality.

Run `python3 -B scripts/check-docs-brand.py` through mise to check maintained
entry links, badge sets, safe SVG structure, matching paths and recorded hashes.
Application layouts follow [their own design record](apps/web/DESIGN.md). This
identity change does not authorize a release, deployment or page redesign.
