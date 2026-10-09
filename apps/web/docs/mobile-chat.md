# Mobile onboarding conversation

The introductory account options and token cloud remain visible until the user
focuses the input or starts a conversation. Below 768 CSS pixels, that action
opens a bounded chat layout. The message pane is the only vertical scroll area;
the input stays below it, not over the last reply. Explore and Access restore the
normal page layout without clearing the conversation or an unsent draft.

Keyboard and content resizes retain either the latest reply or the reader's
chosen scroll position. A resize must not count as an intentional scroll. The
transcript and its rendered content are observed, and observers are removed when
the conversation pane unmounts. Transcript scrolling does not toggle the shared
mobile header. Desktop layout, account authorization and billing are unchanged.

Run `bun test src/features/onboarding/l0-transcript-scroll.test.ts` from `apps/web`
for the scroll controller. Run the L0 mobile chat review workflow for the
onboarding tests, type check, build and browser checks. The browser script uses
only a local development persona and synthetic replies; all other backend and
external requests are blocked.

The browser checks cover 320, 390, 430 and 767 pixel mobile widths, a 1440 pixel
desktop, both themes, two turns, tab changes, drafts, and reduced viewport heights
of 420 and 360 pixels. These are keyboard-sized viewport tests, not a substitute
for a physical Android/iOS keyboard test. For device review, open Getting started,
send a long question, repeatedly open and close the keyboard, and read older
messages while a reply arrives. The input must stay visible and reading must not
jump to the newest text unless the user asks it to.
