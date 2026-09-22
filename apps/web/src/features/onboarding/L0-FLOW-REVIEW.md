# L0 text → cloud → answer review

Scope: L0 onboarding presentation and interaction only. Public home, L1 UI,
shared assistant transport, payment checkout, authorization and server settings
are unchanged. Keep PR #444 as a draft until repository checks and full-app
integration pass.

## Interaction

- Committed input graphemes fly from their measured native-input position to
  the cloud. IME candidates are not animated or submitted. Typing is local;
  only an explicit submit or preset click starts a request.
- A preset sentence splits into grapheme spans at its exact button position,
  including wrapping. All visible fragments launch immediately together. The
  preset never detours through the input or opens a second chat drawer.
- `sendAssistantMessage` remains the production transport. Its real text
  deltas populate an inline answer; matching visual fragments originate in
  the cloud and land at their laid-out answer positions. The animation does
  not invent replies or delay the network call.
- These visual pieces are Unicode grapheme clusters, not claimed model token
  IDs. The existing response API exposes text deltas, not tokenizer IDs.
- Native answer text remains selectable; a final render uses the existing
  safe markdown Response component. No HTML from a response is injected into
  the flight layer. Special actions still require the existing assistant
  interface: Continue opens an editable question, not an automatic replay or
  confirmation of the server action.

## Bounds and lifecycle

One request per chat session, AbortController stop, late-result guards,
account/session-keyed remount, canonical retry reset, bounded 12-item history.
Display and request use the existing message redactor. No localStorage,
recording, typed-text telemetry, extra media or dependency was introduced.

At most 80 concurrent flying fragments; arrivals take 340 ms with no staggered
queue. Bursts and offscreen text are shown immediately. A long answer retains
its full content while only its final 192 graphemes are eligible for animation.
Resize/scroll/hidden/reduced-motion/pause settle text immediately. DOM copies,
animations and listeners are removed on unmount. The decorative cloud has
1000 mobile / 2600 desktop points, DPR <= 2 and a 30-fps drawing cap.

Top-up remains a direct wallet action. Paid activation conditions and refresh
come from the pre-existing policy helper. Review feedback stays in a visible
summary; detailed explanations, request details and secondary setup are
progressively disclosed.

## Executed locally

- TypeScript strict compilation of the dependency-free session, grapheme,
  animation and cloud modules plus their Node tests: pass.
- Node tests: 19/19 pass (12 session/grapheme and 7 cloud lifecycle).
- Chromium fixture: 43 assertions pass, including exact preset origins,
  received-text flights, composition, stop, retry reset, burst limits, motion
  fallback, policy states and 24 viewport/theme/language layouts.
- The browser fixture uses real component TSX and production CSS/animation,
  but mocked React hooks/router/translation/transport/markdown. The local
  React runtime is an older UMD runtime with hook shims. It is NOT evidence
  of a full React 19 application build or real authenticated server requests.
- The MP4/GIF are actual browser captures of that isolated fixture. The
  streaming response and account amounts in the recording are test data.

## Not verified locally

Full workspace dependencies cannot be installed in this environment.
`getting-started.test.tsx` was adapted to assert an inline transport request
instead of the obsolete queued-drawer expectation; the React/router tests
are committed but not executed here. Full oxfmt/oxlint/tsgo/rsbuild, supported
browser integration, actual assistant actions and real payment callbacks
still need repository CI and a full authenticated application test.

Do not merge or deploy on the strength of fixture tests alone.
