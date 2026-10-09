# Mobile scrolling controls

On viewports below 768 CSS pixels, scrolling down hides the surrounding page controls. Scrolling up brings them back without returning to the top. Desktop layout and form state are unchanged.

## Scope

`AuthenticatedLayout` shares one `MobileScrollChromeProvider` with its app header, section headings, page-footer actions and bottom service notice. `SectionPageLayout` marks its content as a scroll root, so settings pages and other pages using this layout behave alike. Other vertical lists inside `main` are also observed. The content gains the released height; transparent controls do not remain over it.

`PublicLayout` uses the same controller for document scrolling. Its fixed header slides above the viewport. Ordinary document footers remain in the document flow. Browser address bars, dialogs, text editors and horizontal-only widgets are not controlled by this feature.

The assistant form's inline save row is a standalone fallback. It is not sticky and is hidden when the settings page has rendered its shared save actions. This prevents two save rows from covering the tool list. Saving, validation and draft state use the existing form and portal.

## Interaction rules

- Accumulate 24 pixels down to hide and 12 pixels up to show. Reverse direction before counting a new movement; small alternating movements do not cause flicker.
- Keep controls visible at the top and on short pages. Before hiding, leave enough scroll range for an upward movement to restore the controls.
- Clamp overscroll at both ends. Rebase scroll dimensions when controls collapse or content changes. A layout-induced clamp at the bottom must not be treated as an upward gesture.
- Keep controls available while editing text, using an expanded header menu, or interacting with a modal dialog. Keyboard navigation, route changes and viewport-width changes restore them.
- Hidden controls are inert and excluded from the accessibility tree. Respect the device's reduced-motion setting. Height-only changes from mobile browser bars do not reset the scroll direction.

## Reuse

Wrap a non-scrolling control region in `MobileScrollChrome` inside the layout provider. Flow regions release height. Use `overlay` only for an already-fixed header. Do not wrap the scrolling content itself. A custom scroll area outside `main` can opt in with `data-mobile-scroll-root`; a nested widget can opt out with `data-mobile-scroll-ignore`.

There is one passive capture listener for scroll events per mounted layout, not one listener per bar. Pointer and wheel input seed newly mounted scroll containers. Each container has its own direction history. Layout cleanup removes the listeners. No dependencies or backend settings are added.

## Verification

From `apps/web`, run the controller tests with the repository's Bun setup:

```sh
bun test src/components/layout/lib/mobile-scroll-controller.test.ts \
  src/components/layout/lib/mobile-scroll-controller.dom.test.ts
bun run typecheck
bun run lint
bun run build
```

For browser acceptance, check a long settings tool list at 360 and 390 pixels wide, a desktop page, and a public document page. Scroll down, reverse mid-page, repeat at the bottom, open a menu, focus a text input, change routes, and enable reduced motion. Verify that only one save row is visible and that the content expands when the controls close. A controller fixture is not a substitute for checking the built application.
