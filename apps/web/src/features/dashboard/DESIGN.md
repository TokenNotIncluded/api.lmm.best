# Overview

The overview separates a person's account from administrator-only site totals.
Balance is the leading value, with a compact wallet action and a quieter runway
estimate. The three usage measures form aligned rows rather than repeated charts.
The existing usage query, currency preference and error/retry states remain the
source of these values; no illustrative trend or derived balance history is shown.

A three-way segmented control selects USD, CNY or Credits without a menu. It
uses the existing account preference writer: save only wallet_display_currency,
keep the acknowledged unit while saving, and show an error on failure. No local
optimistic currency state is added. Wallet and header displays share this setting.
Localized currency names come from Intl.DisplayNames, and the existing lowercase
credits translation supplies the point label. A selected segment cannot be cleared.

Site totals follow the same display currency. Their integer strings go through
the existing rational conversion basis, never a floating-point Number. Exact
credit counts remain visible beneath fiat estimates. Missing conversion metadata
shows an unknown amount, never zero. Original payment records never convert.
Site credit totals stay exact, including signed balances and large integers.
Payment rows show the order currency once visually and retain the currency on
each amount for assistive technology. Gross payment and recorded refunds support
the more prominent net payment. Cash currencies are never added together or
combined with LDC. Empty data is a named empty state, not an invented zero.

Long scope notes and payment explanations use native, initially closed details.
Unconfirmed/invalid records keep their warning title and combined record count
visible even when collapsed. A failed statistics request is always visible and
never shows cached amounts as confirmed values.

Layout responds to the available content width: balance and usage sit side by
side on wide panels, while payment rows put currency and net payment first on
phones. On wider content areas the site totals form a narrow column beside the
payment breakdown, rather than stretching each payment row across the page.
Semantic theme tokens supply both light and dark colors. Wallet,
refresh, retry and disclosure targets are at least 44px tall; no new motion or
page-local palette is introduced.

## Review

Run the overview data-state, exact-quota-currency and site-statistics tests,
the console fixture tests,
and the frontend type, lint and build checks. The local-only persona console has
an explicit admin statistics fixture, and the route gallery includes the admin
and mobile overview. Fixtures are synthetic and never authorize real requests.
Inspect wide/narrow content areas, both themes, keyboard disclosure toggles,
empty/error states, account demotion, long translations and large signed totals.
Component screenshots do not establish production login, route integration or
service availability; repeat full-route checks in an environment that permits
local browser navigation before release.
