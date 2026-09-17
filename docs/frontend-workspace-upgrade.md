# Workspace frontend upgrade

Source-only follow-up to #365, based on the merged session fix #363. No version, tag, release, production operation, workflow or dependency change.

## Scope

The authenticated overview now starts with a personal workspace heading and quick access to the available keys, model panel, wallet and usage-log destinations. Metrics and information panels use clearer spacing and responsive groups. The existing SummaryCards implementation still owns all balance, usage, loading, error and retry behavior; no example values or synthetic health indicators are added to the application.

The sidebar gains local multi-word filtering of its existing translated navigation. Matching nested children become directly visible results instead of remaining inside closed accordions. Escape clears a nonempty filter and restores input focus, without interfering with an IME composition. Empty results are announced and offer a clear action. A view switch remounts its filter; an account change remounts the animation boundary immediately, so the previous account's exiting navigation is not retained.

## Permissions and compatibility

Both search and launch actions consume `useSidebarView` output, after the existing role/configuration filtering. A shortcut priority list is not authorization: missing links remain missing, disabled links are omitted, role requirements on parents and children remain effective, and only the four exact registered destinations are eligible. The model shortcut remains an in-context button using the existing ModelPlazaProvider and its trigger focus restoration. Other destinations remain real router links. Route-level authentication and authorization stay unchanged.

The global command menu is reused, not replaced. No new queries are introduced by the presentation components. The existing status/content visibility, assistant rail, mobile safe-area padding, theme presets, home canvas experience, seven-day logout and pricing/referral changes are preserved.

## Responsive and accessible presentation

Workspace colors reference the active theme variables. Named content-container breakpoints adapt the layout to the actual available panel width, including an open assistant rail. New search/clear/command controls have 44px targets, keyboard focus remains visible, long labels wrap, and reduced-motion users do not receive new hover transforms. The sidebar filter is styled independently of the authenticated ancestor because mobile navigation is portalled. The feature-local copy catalog includes all seven configured locales; existing destination labels still use the normal translated registry.

## Validation boundary

Local TypeScript syntax transpilation and PostCSS parsing were executed. The 14 navigation/copy unit tests were transpiled and run with Node: 14 passed, zero skipped. Full application typecheck, formatting, React interaction tests and build require the existing remote CI; local syntax checks do not replace them. The new five-test UI fixture mounts the real router, SidebarNavigation and WorkspaceLaunchpad with the existing providers, including scope change, nested search, Escape/clear, real link vs panel behavior and escaped display names.

Browser screenshots made from a static layout fixture are visual/CSS evidence only, not an authenticated production visit or end-to-end permission verification. Production and payment paths are not exercised by this frontend preparation.
