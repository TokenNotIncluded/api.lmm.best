---
name: 'LMM Forge Console'
colors:
  background: 'var(--background)'
  surface: 'var(--card)'
  text: 'var(--foreground)'
  primary: 'var(--primary)'
---

# Design System: LMM Forge Console

## Visual Theme & Atmosphere

- [observed] Authenticated product surfaces use a compact console shell with one restrained emphasis color, neutral semantic surfaces, and persistent inverted/subtle navigation.
- [observed] Public and authentication surfaces may use the warmer Forge editorial palette; authenticated utilities remain in the console semantic-token layer unless an existing route explicitly opts into an editorial preset.
- [inferred confidence=high] New operational pages should feel direct, quiet, and information-dense rather than promotional. One workflow region leads; secondary history and metadata recede.

## Color Palette & Roles

- [observed] Components consume semantic variables: `--background`, `--foreground`, `--card`, `--muted`, `--primary`, `--border`, `--success`, `--warning`, `--info`, `--destructive`, and their foreground pairs.
- [observed] Both light and dark modes define complete semantic roles. State is never encoded by a literal page-local color alone.
- [observed] Forge raw identity tokens use warm paper/ink, cactus, clay, and sage values, but they are mapped through shared theme variables before components consume them.
- [inferred confidence=high] Third-party providers do not introduce their own palette. Use a provider mark only when an approved shared identity token already exists.

## Typography Rules

- [observed] The default authenticated body face is Public Sans through `--font-body`; the optional Lora editorial axis belongs to declared editorial presets, not routine settings or utility screens.
- [observed] `SectionPageLayout` uses compact page titles (`text-base` to `text-lg`) with bold weight and tight tracking.
- [inferred confidence=high] Use hierarchy, spacing, tabular numerals, and weight before increasing type scale. Reserve display/serif treatments for established editorial scenes.

## Component Stylings

- [observed] shadcn/ui is configured as `base-luma`, neutral, CSS-variable driven, with Hugeicons and an inverted subtle menu.
- [observed] Existing primitives carry hover, focus, disabled, error, loading, light/dark, and reduced-motion behavior. Prefer them over page-local replacements.
- [observed] Settings forms use a two-column desktop grid, full-span switch/textarea rows, square grouped control surfaces (`rounded-none border`), and compact spacing.
- [observed] Error states combine a named icon, title, optional description, and an explicit retry/action; toast is not the only recovery path.
- [inferred confidence=high] Cards represent true grouped tools or independently actionable objects, not every row or section. Badges represent state/count only; filters use controls with real selection semantics.

### Component foundation

Luma recipes are applied to the owned primitive source files on Base UI 1.8.0. Preserve existing exports, custom sizing, semantic colors, keyboard behavior, menu event adapters and page-level class overrides. `src/components/ui/luma-migration.json` records the component inventory and upstream CSS checksum; `src/components/ui/LUMA-LICENSE.txt` preserves source attribution.

Drawer composes Base UI Portal, Backdrop, Viewport, Popup, Content and VirtualKeyboardProvider. Its trigger uses `render` instead of `asChild`. OTP renders actual Base UI inputs; its authentication consumer maps React Hook Form's value/onChange/ref, keeps numeric validation and backup-code mode, and sets `autoSubmit={false}`. OTPField is project-specific rather than the Luma registry's input-otp recipe. `vaul` and `input-otp` are not direct runtime dependencies.

TanStack Table, React Hook Form, cmdk, Sonner and React Day Picker retain their own roles. Primitive updates do not replace charts, the editor, brand icons, the token cloud or every business layout. Preserve 44px native selects on phones, controlled multiline height, paper cards, line/vertical tabs and reduced-motion support. Component changes must preserve access permissions, payment, price locks and login endpoint behavior.

### Component review

Run from `apps/web`:

```sh
LMM_ENABLE_PERSONA_DEBUG=1 bun run dev --port 4174
node scripts/ui-foundation-review.mjs
```

The loopback-only `/?ui_review=1` component gallery uses a separate debug entry and is not imported by production routes. The review script exercises tabs/drafts, switch geometry, OTP paste/validation/explicit submission, nested dialog select, drawer input/Escape/focus restoration and reduced motion at 1440, 834, 390 and 320 pixels. Set `CONSOLE_REVIEW_OUTPUT` for artifacts; `PLAYWRIGHT_MODULE` may point to an installed Playwright module.

Also run the console page, interaction and navigation scripts and frontend quality gates. The gallery blocks external/API traffic and uses synthetic local data. It does not sign in to production, make payments, buy numbers or change management settings; its screenshots do not establish production service health.

## Layout Principles

- [observed] The authenticated shell is height-bounded, responsive, safe-area aware, sidebar-based at desktop widths, and reserves bottom navigation space on compact screens.
- [observed] `SectionPageLayout` owns a compact header/action row, scrollable content region, and optional footer portal with `px-3` mobile / `px-4` desktop rhythm.
- [observed] Product forms become two columns at `lg`; mobile content remains a single readable column with 16px inputs to avoid focus zoom.
- [inferred confidence=high] Lists and tables should preserve comparison hierarchy on desktop and switch to deliberate mobile cards or stacked rows instead of horizontal overflow.
- [inferred confidence=high] Avoid nested decorative frames. Separate regions with spacing, dividers, muted bands, and one interaction-owned border.

## Motion & Interaction

- [observed] Loading may use the shared skeleton shimmer; page and state transitions remain restrained.
- [observed] Focus-visible controls expose a clear semantic ring, interactive elements expose pointer affordance, and fixed chrome accounts for safe areas.
- [inferred confidence=high] Animate only named state changes with short opacity/transform transitions, preserve layout stability, and honor reduced motion. Never use motion to decorate financial or status changes.

## Accessibility

- [observed] The authenticated layout includes a skip-to-main control and semantic layout regions.
- [observed] Shared feedback primitives pair icons with text and explicit actions.
- [inferred confidence=high] Every icon-only action needs an accessible name and tooltip where recognition is not universal. Status requires text/icon in addition to color.
- [inferred confidence=high] Preserve keyboard reachability, focus after dialogs/sheets, comfortable compact-screen hit targets, and readable light/dark contrast.

## Source references

- `components.json`
- `src/styles/index.css`, `src/styles/theme.css`, `src/styles/forge-tokens.css`
- `src/components/layout/components/authenticated-layout.tsx`
- `src/components/layout/components/section-page-layout.tsx`
- `src/features/system-settings/components/settings-form-layout.tsx`
- `src/components/error-state.tsx`

## Known Gaps & Exceptions

- [inferred confidence=medium] Runtime contrast, touch targets, text expansion, focus restoration, reduced motion, and both declared target viewports require rendered verification for each new surface.
- [inferred confidence=medium] A feature may depart from console density only when its own confirmed product declaration or first-party reference explicitly introduces a different visual world.

### L0 welcome surface exception

- The unactivated `/getting-started` branch uses the existing console theme, a compact route header, and a single centered composition. The activated setup branch is unchanged.
- A borderless introduction explains the two upgrade paths: free conversation or a qualifying top-up. Two equal-width controls lead into the existing inline conversation and wallet. There is no duplicate LMM/top-up bar, payment progress ring, or application-letter form.
- The token cloud, view tabs, and mounted conversation remain in order. Smaller cloud heights and tighter mobile spacing keep the question form near the first screen. Input text stays at 16px, controls are at least 44px high, and reduced motion is preserved.
- Account status and contact support are secondary actions below the workspace. The access-details tab contains the exact backend-derived payment amount, eligibility conditions, confirmation action, account status, Pi guide, and source questionnaire. The amount never comes from wallet balance or a historical policy amount.
- Disabled or unknown paid activation never promises a paid upgrade. A completed payment asks for account refresh instead of another top-up. Only the authoritative server access decision advances the account; neither answer text nor a browser click grants access.
- The existing focused-onboarding shell, compact service disclosure, scrolling chrome, and hidden sidebar assistant are preserved. No production payment, deployment, or access-policy change is part of this presentation update.
- Coverage lives in `l0-paid-welcome.test.tsx`, `getting-started.test.tsx`, and `l0-upgrade-copy.test.ts`. Repeat real-browser review for layout changes; DOM tests alone do not establish visual quality.

### Cut logo rollout

- [observed] The default LMM Forge identity uses the original filled symbol and wordmark from `.github/assets/logo-geometry.json`. React components inherit neutral `currentColor`; typography and semantic colors for operational content remain unchanged.
- [observed] Public navigation and expanded desktop sidebar use the horizontal wordmark when space allows. Narrow mobile navigation and collapsed sidebars use the compact symbol. The default home footer reuses the full wordmark; tenant-defined logos and names retain their own artwork.
- [observed] Built-in cached names and logo URLs normalize to the default identity before rendering or preloading. The favicon and touch icon derive from the same symbol. They have explicit black backgrounds and white ink; transparent SVG placements select ink for the current surface.

The platform-owned legacy name `lmm.best` also resolves to LMM Forge. Other tenant names stay as configured.

### Cut system in the application

- [observed] The default preset uses a tighter `--radius` (0.5rem) and a shared `--cut-size` token. A solid 45° cut mark — the logo's base cut — precedes console page titles (`.console-page-title::before`) and homepage section headings (`.lmm-section h2::before`), and replaces round bullets in the homepage protocol and access-note lists. The mark inherits `currentColor`; it adds no new palette entry.
- [observed] The active console navigation row is a full-contrast block (`--sidebar-primary` on `--sidebar-primary-foreground`) with a 45° trailing notch. The notch is removed under `:focus-visible` so the focus outline is never clipped. Sidebar rows use an explicit 0.375rem radius because the Luma sidebar rescopes `--radius`.
- [observed] Display headings on the homepage and console titles use heavy weights (650–750) with tight tracking. The homepage primary call to action is a square-cornered block carrying the same notch, with the notch removed when it has keyboard focus.
- [observed] `--font-sans` names the bundled family `'Public Sans Variable'` first, followed by a deterministic CJK sans stack. The previous stack named only `'Public Sans'`, which never matched the Fontsource face, so the app silently rendered the platform sans.
- [observed] Card primitives use a light `shadow-xs`; borders and the theme's slightly stronger `--border` carry structure. Named presets and the user's radius axis continue to override these defaults.

### Public homepage pointillist poster exception

- [confirmed] The public homepage uses the user's [Oriku video reference](https://x.com/Oriku175/status/2065701075202806030/video/1) as a code-led poster direction. Its runtime marker is `data-poster-direction="oriku-pointillist-sculpture"`; no approved composition or randomly selected concept is claimed.
- [observed] The cinema has a black ground (#070707), pink petals, gold centre and green stem/leaves rendered as a regular fine-point lattice. Self-hosted Bodoni Moda supplies the thin display wordmark; existing body typography and real controls carry the content. Upper-left branding, lower-left headline/actions, a large right-hand lotus and bottom chapter controls establish the desktop composition. This exception belongs to the homepage cinema, not shared header, console or L0 styling.
- [observed] Intro → store → tool market → Pi/dsh/directory ecosystem → future makes four native-scroll transitions. Pointer strokes advect nearby points with vertical scatter; time-based critical damping restores their grid after release. Image samples morph into shop, socket and planet sculptures.
- [observed] The renderer budgets 60fps desktop/30fps mobile, uses 2.7px/1.7px grids and cached typed buffers. Mobile, short viewports and reduced motion retain five readable chapters; static imagery, passive pointer/scroll listeners, offscreen/hidden pausing and owned cleanup support fallback behavior. Lotus provenance and the font's SIL OFL remain beside their assets.
