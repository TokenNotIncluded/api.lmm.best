# Mobile and settings review

This change keeps the existing routes, account checks, server settings APIs,
billing logic and particle renderer. It does not deploy the application.

## Interaction changes

- The homepage uses the same sticky scene and native scroll position on narrow
  and wide screens. One active canvas handles the particle brush. Single-finger
  touch updates the brush without blocking scrolling or pinch zoom. Reduced
  motion uses the complete stacked reading layout instead.
- The console uses the authorized navigation registry for its mobile bottom
  shortcuts. The shortcuts occupy layout space and hide during text entry.
  Service and privacy details share an expandable footer instead of displacing
  the controls. Expanding it still exposes the existing consent controls.
  The mobile scroll chrome from main is retained around the header and service
  details; the quick navigation dock remains a separate, in-flow control.
- Settings use a floating directory with search and grouped sections. Hold the
  handle for 280 ms, move 32 px per section, and release to navigate. Moving
  before the hold, cancelling the pointer, or losing focus does not navigate.
  Existing unsaved-form navigation guards remain responsible for confirmation.
- Long Markdown documents have a shared section reader. All original text stays
  mounted; section links open their target, and printing temporarily expands all
  sections. Document layout classes apply once, never to every chapter body.
  Configured HTML continues through the existing sanitized renderer.
- Existing real dashboard statistics and charts remain the data source. The
  WebMCP page's distribution strip is computed from its tool registry, not sample
  percentages or invented activity.

## WebMCP settings scope

`lmm_settings_form` lists explicitly supported fields in a mounted form.
`lmm_settings_preview` validates and prepares a visible draft in that same form.
Both require the current root administrator account (L6). Nothing is submitted
or saved automatically. The existing Save button and server authorization still
control persistence.

The supported fields are SystemName, DisplayTokenStatEnabled,
DefaultCollapseSidebar, DataExportEnabled, DataExportInterval, and
DataExportDefaultTime. Credentials, payment values, model rates and access
policies are not exposed. Cancellation, account/page changes, concurrent
previews and closed forms are checked. Rollback does not overwrite a newer human
edit made during validation.

## Browser regression script

Run `scripts/mobile-experience-review.mjs` against loopback servers only. Build
and serve the public app at port 4175. Start the development-only persona app
at port 4174 with `LMM_ENABLE_PERSONA_DEBUG=1`. Supply an installed Playwright module
through `PLAYWRIGHT_MODULE`, and set `MOBILE_REVIEW_OUTPUT` to a temporary output
folder. Set `MOBILE_CONSOLE_ORIGIN=http://127.0.0.1:4174` to include console tests.

The script uses synthetic accounts and intercepted responses. It never signs
into production or saves a settings draft. Its WebMCP adapter is a test double;
it verifies tool/form integration, not support in a particular released browser.
Screenshots and `report.json` record the observed results. Device emulation does
not replace a physical Android/iOS check for browser chrome, thermal behaviour,
keyboard resizing or platform-specific zoom gestures.

## Review record

The final source review uses commit
`bca2a2ca7cc82810792173050d3bfd2dab4dc40a` in GitHub Actions run
[37972929250](https://github.com/TokenNotIncluded/api.lmm.best/actions/runs/37972929250).
This includes main at `5d7fab5d432025d59d69490501f2a1d57e47fb1c`. The
authenticated-layout conflict was resolved by preserving its scroll chrome and
adding the settings state, compact service details and quick navigation dock.
The artifact revision files identify the exact source for each result. The
source-export and patch-transport workflow is temporary and is not part of the
feature diff. The permanent browser script remains under `scripts/`.
After that source review, two upstream Extore `if` statements receive the
required braces, and the merged layout is formatted. These do not change the
rendered UI or callback logic. The final local lint check and the 9 focused
Extore protocol/callback tests verify those corrections separately.

The browser matrix includes 15 public cases and 17 authenticated console cases.
Public cases include 320 px narrow screens, short portrait, landscape, tablet,
desktop, light/dark themes and reduced motion. Console cases cover overview,
models, keys, wallet, profile, usage logs, authenticated About and five settings
sections at 390 px and 1440 px. These are selected representative routes, not a
claim that every route or physical device was individually inspected.

Baseline formatting differences remain in nine unchanged files after integrating main:

- `src/components/layout/components/mobile-scroll-chrome.css`
- `src/components/layout/lib/mobile-scroll-controller.ts`
- `src/features/system-settings/api.ts`
- `src/features/home/home-sculptures.ts`
- `src/features/home/sculptures/marks.ts`
- `src/features/home/sculptures/moon-far-side.ts`
- `src/features/home/sculptures/showcase.test.ts`
- `src/features/home/sculptures/spectacle.ts`
- `src/features/home/sculptures/whale.ts`

The full format check still reports these files. Do not interpret the workflow
as entirely green or confuse this baseline failure with a successful full
format check. Runtime, build, lint, type, test and format results are retained
separately in the review artifacts. Changed files were formatted without
rewriting the upstream copyright headers.
