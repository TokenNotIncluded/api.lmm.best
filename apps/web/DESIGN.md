---
name: "LMM Forge Console"
colors:
  background: "var(--background)"
  surface: "var(--card)"
  text: "var(--foreground)"
  primary: "var(--primary)"
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
  sha256: `16177439bcda07f750eeadb7882bf5b5b3023c7b4c98b5cb70f786505a1ad686`
  confidence: high
- [observed] path: `src/styles/theme.css`
  sha256: `feb9173866b51ca2fe8177974f2d5514c0c1cb951cf7a21c2d475ee4b10d9f94`
  confidence: high
- [observed] path: `src/styles/forge-tokens.css`
  sha256: `f9941c89671a166774165f4bd667fa1765e21fc537c75218bc8b2ddb3546d84d`
  confidence: high
- [observed] path: `src/components/layout/components/authenticated-layout.tsx`
  sha256: `0e81f675fc00fca989529291c93105d529175da071e3ad257c0a85376ac832b8`
  confidence: high
- [observed] path: `src/components/layout/components/section-page-layout.tsx`
  sha256: `494eb7db68889ba6feda69e381d436710556d8c0ee6061888a050d228d56ba50`
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
