# Homepage directory entry

The homepage keeps its animated sculptures, except the retired cloud, and no
longer renders the small caption in the top-right corner of the hero.

The AI directory entry is a link with a progress bar, not a second large banner.
A mouse wheel can fill the bar by scrolling upward while the page is already at
the top. On touch screens, swipe upward **on the entry** and release after the bar
fills. A 72-pixel swipe fits below the phone header. Swipes elsewhere retain the
homepage's normal scrolling. Clicking the link or pressing Enter still works.

A reverse, short, cancelled or multi-touch gesture resets progress. Wheel zoom,
horizontal scrolling, nested scroll areas, dialogs and text controls do not open
the directory. Wheel progress expires after 700 ms of inactivity. Unmounting the
entry cancels pending navigation and removes its listeners.

The directory puts its title, search and category filters before the sponsored
section. Category counts come from enabled links with valid URLs. Card headers
are large links; the description disclosure remains a separate control. Paid ad
behavior, admin permissions, public API requests and URL validation are unchanged.

## Verification

Run the focused tests from `apps/web`:

```sh
bun src/features/forge/home-directory-link.test.ts
bun src/features/forge/home-directory-gesture.test.ts
bun src/features/home/home-sculptures.test.ts
```

The read-only `Homepage design review` workflow builds the real frontend and
runs `scripts/directory-design-review.mjs` alongside the existing homepage review.
It checks desktop and phone gestures, dark/light themes, reduced motion, search,
category filtering, empty results and description expansion. API responses are
fictional local fixtures; it does not access customer data or make purchases.
Screenshots and the tested revision are retained in the workflow artifact.

Local isolated DOM/CSS checks are useful for the gesture controller but are not a
substitute for the production-bundle browser review or the full frontend checks.
