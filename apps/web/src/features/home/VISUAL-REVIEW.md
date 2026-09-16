# Homepage visual review — 2026-09-16

## Implementation boundary

`ForgeHome` keeps account routing, access approval, assistant validation/redaction,
Pi OAuth, pricing, scripts and purchase integration. `HomeLanding` and
`CodePreview` own presentation; `mountHomeMotion` owns one disposable animation
lifecycle. `forge-home.css` replaces the original stylesheet instead of adding
another override layer. The old motion wrapper and SVG background are removed.

The hero is an original, locally rendered copper sculpture, not a Framer asset,
MP4 background or a real service-status display. The request card is explicitly
an example. Motion is optional; it does not gate links or account actions.

## Actually executed before pushing

- 31 Chromium checks passed in an offline presentation harness: scene-relative
  pointer parallax, moving frames, pause/resume, three scroll chapters, keyboard
  code tabs, clipboard contents, focus retention, offscreen suspension,
  reduced-motion behavior, cleanup/remount, and no horizontal overflow at
  320/360/390/768/1024/1440/1920px. No browser runtime errors were observed.
- Screenshots inspected: English desktop, Chinese desktop light/dark, Chinese
  mobile, the full hero, and all three scroll stages. Fixed an orphaned Chinese
  title fragment and the mobile/reduced-motion control visibility discovered
  during review. A Chromium frame-capture walkthrough was supplied separately.
- Six geometry tests passed with Node 22:
  `node --experimental-strip-types --test apps/web/src/features/home/home-motion.test.ts`.
- Controller-only strict TypeScript checking passed:
  `tsc --strict --noEmit --moduleResolution bundler --module esnext --target es2022 --lib ES2022,DOM,DOM.Iterable apps/web/src/features/home/home-motion.ts`.
- Changed TS/TSX transpilation reported no syntax diagnostics.

## Limits — not a production acceptance claim

Network/DNS and project dependency installation were unavailable. The harness
rendered the actual changed presentation components, motion controller and CSS
using locally available React 16, with shared-header, translation and account/API
slots supplied as fixtures. Production uses React 19 and Public Sans. This does
not validate the production router, real backend, final locale/font metrics,
shared header or complete application integration. Full repository lint,
typecheck, Bun tests, formatting and production build were not executed locally;
GitHub CI and an application preview remain merge gates. No deployment was made.

All UI translation keys are reused, including `Example`, `Pause` and `Resume`.

## Application-preview review after CI

Verify the real `/` route in both themes at the viewports above. Exercise guest,
unapproved and approved account actions, assistant-disabled behavior, copy denial,
language changes, touch input, keyboard navigation, reduced motion and route
unmount/remount. Confirm that real account access rules and notices remain intact.
