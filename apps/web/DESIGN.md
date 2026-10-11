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

## Frontend behavior boundaries

These rules describe retained browser behavior, not completed server integration. The current core HTTP host and extension host do not expose the legacy assistant, tool-market, store or ranked-game APIs. Pages, mocks, fixtures and browser receipts cannot establish backend availability, payment settlement or production acceptance. Current server contracts live in the module READMEs, including [tool market](../lmm-extensions/internal/modules/toolmarket/README.md); financial safeguards belong to the [payment contract](../../docs/payment-money-contract.md).

### Navigation, motion and optional experiences

- Keep the homepage directory entry a real link with click and Enter fallback. Wheel/swipe progress is bounded; reverse movement, multiple touches and interactive controls cancel the gesture. Remove owned listeners on unmount.
- Use one mobile scroll-chrome provider per layout. Menus, dialogs, text editing and short pages remain reachable; route/keyboard changes reset hiding. Hidden chrome is inert and releases reserved height. Respect explicit scroll roots, ignore markers and reduced motion.
- Waiting companions are optional local play after four seconds. They never delay output/navigation, send network requests, award rewards or move money. Reset per task, preserve 48px controls, accessible status and restored focus; collapsing play does not cancel the task.
- Shared shader surfaces use at most one GPU context, bounded initial resolution and frame rate, offscreen/hidden pausing and a static reduced-motion fallback. Preserve generated-source attribution/checksums and composition sync checks. They must not obstruct controls or imply financial status. The homepage poster retains its separately documented rendering budget.
- Signal Path is optional play, never an authentication or anti-bot gate. Agent identity is self-reported. Publishing requires the signed-in user and explicit confirmation; WebMCP state excludes claim tokens and private email. A local game or submission UI does not establish ranked-server availability.

Sources: `src/features/forge/home-directory-gesture.ts`, `src/components/layout/lib/mobile-scroll-controller.ts`, `src/components/wait-companion.tsx`, `src/components/shaders/shader-policy.ts`, `src/features/signal-game/webmcp.ts` and their adjacent tests.

### Assistant and tool consent

- OpenUI accepts only its bounded Stack, Metric, BarChart, DataTable and ConsoleLink catalog. No arbitrary HTML, script, forms or network actions. Keep units consistent, distinguish missing values from zero, retain Markdown explanation and show source fallback on invalid output. Streaming content is noninteractive; a navigation link does not complete a business action.
- Preference actions are bound to the current user/session and run at most once. Mode/theme previews expire without saving; language/currency patch only their fields, and currency changes display only. Undo touches matching fields from the latest action and preserves later manual changes. Prepared actions are not successful receipts; failed or uncertain writes are not replayed.
- Human handoff scopes requests/polling to the visible user, session and conversation and discards stale responses. Takeover pauses AI and disables old operation cards. A saved request does not promise a human is available. Administrative actions keep their actual confirmation and receipt paths, including explicitly validated chat confirmation where implemented; ambiguity does not authorize replay.
- Tool-policy edits remain drafts until saved. Group disable wins; unavailable policy never enables tools. Gate advanced rules on advertised capabilities, preserve drafts on conflicts and require review instead of silent retry or broader permissions.
- Connection secrets stay in transient account-scoped component state, outside caches and JSON previews. Copy requires explicit action; account/provider switches, hiding, expiry or revocation clear them, with a five-minute display limit. Failed permission reads are failures, not empty grants. Failed delegation preserves the original once-only token instead of minting a replacement. Disconnecting does not cancel or refund accepted work.
- Login and installed connections do not authorize paid calls. Consent names the client, tool version, permissions, price ceiling, finite budget and expiry; unsupported quotes are never shown as free. Preserve exact integers and raw business JSON through editing and locale changes. Browser validation does not replace server authorization.
- A submitted call freezes parameters and request identity. Confirm each paid round; after network errors or unknown/unsettled results, query or retry that same identity. Only an explicit refusal before execution permits edited parameters and a new identity. Show immutable pricing/usage evidence; provider-reported or unknown usage is not platform-verified.

Sources: `src/components/ai-elements/openui/policy.ts`, `src/features/assistant/use-assistant-ui-preferences.ts`, `use-assistant-support.ts`, `src/features/system-settings/content/assistant-tool-policy-editor.tsx`, and `src/features/tool-market/{connections.tsx,service-editor.tsx,invoke-body.ts,call-utils.ts}` with their focused tests. UI presets, old MetaMCP descriptions and paid-test examples do not establish supported server capabilities.

This section consolidates the useful browser constraints from sixteen former guides in the [fixed source revision](https://github.com/TokenNotIncluded/api.lmm.best/tree/c33503f212cbcfab0695d554f57b5153cd4f3bfd/docs): `frontend-design`, `home-directory-navigation`, `mobile-scroll-chrome`, `waiting-experience`, `shaders`, `games/signal-path`, `assistant-openui`, `assistant-ui-preferences`, `assistant-human-support`, `assistant-tool-policy`, `tool-market-connections`, `tool-market-guide`, `tool-market-metamcp`, `tool-market-metering`, `tool-market-paid-test-mcp` and `tool-market-provider-presets`. Historical commands and deployment claims remain history; current source and versioned contracts determine behavior.

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

### Language Model Mesh logo

- [observed] The default brand is LMM API (Language Model Mesh, 语言模型网络). Its folded ribbon symbol and geometric lettering are editable SVG adaptations of the user-supplied master in `.github/assets/lmm-api-source.png`; shared paths live in `.github/assets/logo-geometry.json`.
- [observed] React components inherit `currentColor`, with neutral opacity/gradients for the folds. No external image or font request is needed for built-in branding. Public headers, sidebars and footers retain their existing compact-symbol or wordmark layout.
- [observed] Built-in cached names and logo URLs normalize to LMM API. Tenant-defined names and logos remain configured. Favicon and touch icon derive from the same symbol on a light square.

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
