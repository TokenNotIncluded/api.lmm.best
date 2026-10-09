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
- Settings use a floating directory with search and grouped sections. Hold the
  handle for 280 ms, move 32 px per section, and release to navigate. Moving
  before the hold, cancelling the pointer, or losing focus does not navigate.
  Existing unsaved-form navigation guards remain responsible for confirmation.
- Long Markdown documents have a shared section reader. All original text stays
  mounted; section links open their target, and printing temporarily expands all
  sections. Configured HTML continues through the existing sanitized renderer.
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
