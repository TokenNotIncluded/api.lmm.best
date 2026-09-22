# UI foundation migration

Research checked 2026-09-22. This migrates the existing shadcn Base UI Nova foundation to **Luma + Base UI 1.8.0**, not a claim that the project previously had no component system.

## Selection

- **shadcn/ui Luma + Base UI 1.8:** Luma was introduced in March 2026 with softer geometry and more spacing. Base UI became shadcn's default in July; 1.8 was released September 4. Selected to preserve Tailwind 4, React 19, owned-source component APIs and brand tokens.
- **HeroUI 3:** latest verified 3.2.6, September 17. A strong integrated alternative built on React Aria. Replacing this project's existing Base UI compositions would also replace focus and form contracts; it is not added as a parallel runtime.
- **Mantine 9:** latest verified 9.6.0, September 1. Broad application components, but adopting its styling/providers is a larger architectural change than this primitive consolidation requires.

Sources: https://ui.shadcn.com/docs/changelog/2026-03-luma ; https://ui.shadcn.com/docs/changelog/2026-07-base-ui-default ; https://base-ui.com/react/overview/releases ; https://heroui.com/en/docs/react/releases ; https://mantine.dev/changelog/9-6-0/ .

## Scope

Luma recipes are applied to existing primitive source files, not a global override stylesheet. Existing exports, custom sizing, semantic colors, keyboard behavior, menu event adapters and page-level class overrides remain. `src/components/ui/luma-migration.json` records the inventory and upstream CSS checksum; `LUMA-LICENSE.txt` records source attribution.

`vaul` and `input-otp` are removed from direct dependencies and the lockfile. Drawer composes Base UI Portal, Backdrop, Viewport, Popup, Content and VirtualKeyboardProvider. The trigger uses `render` instead of `asChild`. OTP renders actual Base UI inputs. Its authentication consumer explicitly maps React Hook Form's value/onChange/ref, retains numeric validation and backup-code mode, and sets `autoSubmit={false}`. OTPField is a project-specific migration; the Luma OTP registry still uses input-otp at this revision.

TanStack Table, React Hook Form, cmdk, Sonner and React Day Picker remain. This does not replace charts, the editor, brand icons, the token cloud, every business layout or every specialized dependency. Access permissions, payment, price locks and login endpoints are unchanged.

Local adaptations preserve 44px native selects on phones, controlled multiline height, paper cards, line/vertical tabs and reduced-motion support.

## Review

`LMM_ENABLE_PERSONA_DEBUG=1 bun run dev --port 4174` enables the existing loopback-only runtime. `/?ui_review=1` opens the component gallery through a separate debug entry. No production route imports it.

`node scripts/ui-foundation-review.mjs` exercises tabs/drafts, switch geometry, OTP paste/validation/explicit submission, nested dialog select, drawer input/Escape/focus restoration and reduced motion at 1440, 834, 390 and 320 pixels. Set `CONSOLE_REVIEW_OUTPUT` for artifacts; `PLAYWRIGHT_MODULE` may point to an installed Playwright module.

Run existing console page, interaction and navigation scripts plus frontend quality gates. The gallery blocks external/API traffic. Review uses synthetic local data, does not sign in to production, make payments, buy numbers or change management settings. Fixture screenshots are not production service-health evidence.
