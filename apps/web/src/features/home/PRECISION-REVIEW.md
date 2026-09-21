# Precision hero review

## Scope

A dark precision-engineering hero precedes the existing homepage cinema. The
existing `ForgeHome` primary/pricing React nodes are reused, so account routing,
access approval, checkout, OAuth and authorization rules are not reimplemented.
The original cinema, token field, guide, assistant, code tabs and resources remain.
The cinema heading becomes an h2 to retain a single page h1.

The production integration uses existing React and compiled CSS, not Tailwind's
runtime CDN. Google fonts use `display=swap` with local fallbacks. The supplied
abstract MP4 is decorative, and a local inline chrome SVG is available immediately
without remote media. The motion dialog is explicitly labelled a brand film,
not a product demo or performance benchmark.

## Data integrity

The three figures describe the displayed product interface: one gateway, three
protocol formats, two connection methods. Counts derive from the displayed
protocol/method constants, not fabricated customers, SLAs or throughput.

The HUD reuses `useStatus().capabilitiesReady` and labels success as a configuration
snapshot. It never calls that snapshot a live health check. No latency, uptime,
account balance, model inventory or inference-success figure is fabricated.

## Verified locally

- TypeScript strict checking: motion controller and static protocol data.
- TypeScript TSX syntax/transpilation: component and integration.
- Two Node unit tests: all playback vetoes and bounded counter progression.
- Chromium on a static rendering of the actual hero JSX, in English and Chinese,
  at 1440x1000, 1920x1080, 768x1024, 390x844, 320x740 and 844x390.
- No horizontal or heading overflow and no uncaught JS errors in those fixtures.
- Native video dialog opening, Escape closing, focus restoration, source release;
  pause control and final inventory counters.
- Standalone preview navigation drawer (the production app retains its existing
  drawer instead of introducing a duplicate).
- Reduced-motion mode assigns no background MP4 source and requests no MP4.

## Limits before merge

The container cannot resolve GitHub/npm/CDN hosts. Repository sources were read
through the GitHub connector; dependencies were not installed. Full React app
build, repository lint/format/i18n checks, real account-flow E2E and hosted-font /
video playback verification were NOT run. The static fixture uses a stubbed
status hook and fallback fonts; it is not a screenshot of production.

Run the normal repository CI and review the integrated homepage in both account
states and both themes before merging. Existing translation keys are reused;
new t(...) keys should be included in the normal i18n sync/review. Verify the
operator's media/font CSP and the supplied clip's hosting/licensing before
production deployment; a blocked CDN must continue to show the fallback artwork.

Nothing in this change deploys the site or modifies backend configuration.
