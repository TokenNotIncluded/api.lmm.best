# Console page review

`console-page-review.mjs` captures the actual development UI in Chromium, not a design mockup. The review workflow is read-only and uses synthetic identities and explicit local API fixtures.

## Coverage

- 28 user-facing entry routes, including the temporary-activation page.
- 12 administrative routes.
- Every registered section in the seven system-settings categories (47 sections at this revision).
- Additional 390px mobile, profile tabs, SMS order/history/email, wallet disclosure, expanded filter, and dark-theme views.
- `/workspace` and `/playground` are compatibility redirects; the report records the resolved URL rather than inventing separate screens.

The fixture data deliberately includes empty lists, unconfigured services, and a low-balance ordinary account. These screenshots do not represent production balances, prices, inventory, or service availability. The review does not purchase numbers, refund/cancel orders, submit payments, or save administrative settings.

## Running

Install the repository's locked dependencies and Playwright Chromium. Start the development-only persona entry with `LMM_ENABLE_PERSONA_DEBUG=1 bun run dev --port 4174` from `apps/web`.

Set `CONSOLE_REVIEW_OUTPUT` to an isolated output directory, optionally set `PLAYWRIGHT_MODULE` to an installed Playwright module path, and run `node scripts/console-page-review.mjs`.

The target is fixed to `http://127.0.0.1:4174`. Requests to external origins are blocked; unexpected backend requests fail the review. `console_review=1` selects the explicit GET-only fixture catalog in the development entry. Production builds use `main.tsx`, not `debug-main.tsx`.

`report.json` records identity, route, resolved URL, viewport, screenshot, errors, and visible page text. A review fails on route exceptions, error toasts, known data-load error states, or document-width overflow. Keep the artifact's `revision.txt` with the screenshots when comparing versions.

## Regressions

- Temporary-activation balance rendering covers `en`, `zhCN`, `zhTW`, an invalid locale, unavailable/pending balance, account changes, and threshold recovery. Locale normalization follows the fix identified in PR #454 by kilisamemarisaaa.
- HTTP tests cover independent cancellation lifetimes, retained signal-free GET deduplication, rejected cancellation without false failure notifications, and visible genuine network errors.
- Disclosure/filter tests verify that hiding a section does not discard an input draft. Wallet payment tests retain their original assertions and now mount the required router context.
- The serial test runner streams child output so verbose translation tests are not terminated by `spawnSync`'s output-buffer limit.
