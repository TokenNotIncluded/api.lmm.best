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

- [observed] shadcn/ui is configured as `base-nova`, neutral, CSS-variable driven, with Hugeicons and an inverted subtle menu.
- [observed] Existing primitives carry hover, focus, disabled, error, loading, light/dark, and reduced-motion behavior. Prefer them over page-local replacements.
- [observed] Settings forms use a two-column desktop grid, full-span switch/textarea rows, square grouped control surfaces (`rounded-none border`), and compact spacing.
- [observed] Error states combine a named icon, title, optional description, and an explicit retry/action; toast is not the only recovery path.
- [inferred confidence=high] Cards represent true grouped tools or independently actionable objects, not every row or section. Badges represent state/count only; filters use controls with real selection semantics.

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

## Source Evidence & Confidence

- [observed] path: `components.json`
  sha256: `bf0b375a3d805346e05a2226a2ff29cb5bb72e92662d938eda31c6282dcac053`
  confidence: high
- [observed] path: `src/styles/index.css`
  sha256: `8b01fba83e33b6699f9873beba640e0bea3daf04c2d69838b8b1862166ab377e`
  confidence: high
- [observed] path: `src/styles/theme.css`
  sha256: `feb9173866b51ca2fe8177974f2d5514c0c1cb951cf7a21c2d475ee4b10d9f94`
  confidence: high
- [observed] path: `src/styles/forge-tokens.css`
  sha256: `83bae3f180421622a7e235a4c3c4c45ce9bfbf9702c117b2e5a2720a19a98ec7`
  confidence: high
- [observed] path: `src/components/layout/components/authenticated-layout.tsx`
  sha256: `6130102eae35da3412e96e2491c3035e03833219b5110f3308a94ba8bf4a4c7a`
  confidence: high
- [observed] path: `src/components/layout/components/section-page-layout.tsx`
  sha256: `c01116b8edecf578f324aaa27afc4daa220916f7bbcb2ceed605cfdf5e28443f`
  confidence: high
- [observed] path: `src/features/system-settings/components/settings-form-layout.tsx`
  sha256: `ec38e8b80a48bba51062ab7d0b6f90018b82a3b92dabcccefe45b2b2579dca05`
  confidence: high
- [observed] path: `src/components/error-state.tsx`
  sha256: `24542de766ffd190961f5ecb05b062cffff5180ceae109daad5bbb6f39b5a918`
  confidence: high

## Known Gaps & Exceptions

- [inferred confidence=medium] Runtime contrast, touch targets, text expansion, focus restoration, reduced motion, and both declared target viewports require rendered verification for each new surface.
- [inferred confidence=medium] A feature may depart from console density only when its own confirmed product declaration or first-party reference explicitly introduces a different visual world.

### L0 welcome surface exception

- [observed] The unactivated-account branch of `/getting-started` uses a spacious welcome composition within the existing console editorial theme. It retains shared semantic colors and typography in light and dark modes; it does not establish a new palette or change other operational pages. Surface intent lives in `.impeccable/surfaces/ps-web-src-features-onboarding-getting-started-tsx.md`.
- [observed] Content is a single centered column of at most 48rem on a 64rem composition. The order is fixed and reads top to bottom as one sentence: who you are and what is missing (the access rail), the token cloud, the three view tabs, then the active panel. Narrower screens keep that order and only tighten spacing; nothing is reordered off the first screen.
- [observed] Depth comes from shared card/muted surfaces and dividers. The question form and access rail have softly rounded corners; exploration links are full-width divided rows. This composition is a local exception to routine console density, not a prescription to wrap other pages in welcome panels.
- [observed] The access rail (`.l0-rail`) is a persistent first-screen element outside the tabpanels. Review status leads: pending requests show “Awaiting review”; rejected and approved requests retain their actual states; an account with no submitted request gets an application action. The rail has no payment progress ring or upgrade badge. Its primary action opens the application or refreshes confirmed account state. A secondary outlined top-up button stays directly reachable alongside a short, backend-derived amount when paid activation is available. The topbar also keeps `data-testid='l0-topbar-topup'` and links to `/wallet` in every scene.
- [observed] The `/getting-started` shell hides the sidebar, sidebar trigger, and mobile assistant launcher, and removes the launcher's reserved bottom padding. The L0 header has no assistant toggle, and the sidebar assistant component is not mounted, even when a previous rail or request exists. Questions stay in the existing inline conversation; application and revision actions switch to the access tab and open the existing application form in the page. Preserve this route-specific behavior: a fixed mobile pill must not obscure the question form, access actions, or exploration links. The welcome content retains its own bottom spacing inside the scroll region.
- [observed] Primary question and access actions have 44px minimum heights; quick actions and payment copy can wrap. The input remains 16px on compact screens. The question form has a visible label and shared focus treatment, and exploration rows and the Pi disclosure expose keyboard focus rings. On compact screens the rail is a single column with wrapping actions below the status, so neither action is squeezed or occluded.
- [observed] The rail always displays account review status. Its optional `l0-paid-progress` payment line uses only an enabled, finite, positive backend threshold without a trust-level override; with no such threshold no upgrade price is displayed. The access panel contains status and the inline application, without a second payment panel or upgrade meter. Application actions scroll the form into view and focus its reason field; pending requests scroll to their status. Payment actions can continue to the existing L0-aware wallet checkout, still governed by server payment rules; status, retry, pending/rejected feedback, polling, and account refresh remain part of the existing onboarding flow. These conditions constrain displayed content and must not be replaced by a decorative fixed price or a claim that the browser grants payment or developer access.
- [observed] Review and payment status are static. The welcome scene keeps its existing reduced-motion-aware cloud; account amounts and status never animate.
- [observed] Pi setup stays behind a disclosure, with optional source feedback below. The welcome surface does not open or restore a sidebar conversation. Applications retain the existing confirmed request endpoint, validation, error recovery, and approval refresh; submitting never grants access in the browser. Existing activated-account onboarding remains a separate branch.
- [observed] Implementation evidence: `src/features/onboarding/l0-welcome.tsx`, `src/features/onboarding/l0-welcome.css`, `src/features/onboarding/l0-access-copy.ts`, `src/features/onboarding/getting-started.tsx`, `src/components/layout/components/authenticated-layout.tsx`, and `src/features/assistant/assistant-launcher.tsx`. The persistent top-up action and its link to `/wallet` are asserted from the default chat scene in `src/features/onboarding/l0-paid-welcome.test.tsx`. The focused tests assert the review-first headline, backend-derived optional amount, secondary top-up styling, navigation from the default chat scene, inline application submission, and absence of the sidebar assistant for L0. Rendered verification must be repeated after layout changes; earlier screenshots are not evidence for the current revision.
