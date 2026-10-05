# Payment money contract

This document defines the units used by wallet top-ups and subscription payments. A bare number or `$` symbol is not a sufficient money type.

## Dimensions

| Name | Meaning | Unit |
| --- | --- | --- |
| `credits` (`q`) | Integer wallet ledger and smallest balance unit | credit |
| `credits_per_usd` (`K`) | Persisted, immutable ledger denomination | credit / real USD |
| `cny_per_usd` (`R`) | Live operator-configured real-fiat FX rate | CNY / USD |
| `legacy_batch` (`P`) | Compatibility input for old top-up and price tables | legacy pricing unit |
| `QuotaPerUnit` (`Q`) | Retained conversion of a legacy batch to credits | credit / legacy pricing unit |
| `settlement_amount` | Amount sent to and verified from a provider | real ISO fiat |
| `price_multiplier` | Group, channel, tier, or coupon adjustment | dimensionless |

Credits are independent of model tokens. There is no platform dollar, and one CNY is never defined to be one USD. Example rates in tests are fixtures, never production constants.

The first migration persists `K = old Q * old R * old TopUpPlatformUnitsPerCNY` from one authoritative database snapshot. This preserves every existing integer balance and the actual cost of existing prices. Concurrent nodes use the same winning persisted value. Subsequent FX changes, discounts, display preferences, and configuration changes cannot rewrite `K`. An absent or invalid denomination prevents financial operations; it never implies 1:1 fiat. Migration verification is read-only; initialization requires migration apply.

```text
USD = q / K
CNY = q / K * R
```

Display currency is a separate per-account preference: CREDIT, CNY, USD, or automatic. Automatic chooses CNY for Chinese and USD for English; a manual preference survives language changes. Settlement currency remains a real fiat ISO code. Changing display units changes neither credits nor the payment invoice.

## Wallet top-up

For a top-up that grants `q` credits before a dimensionless price multiplier `M`:

```text
base_usd = q / K
net_usd  = base_usd * M
```

Provider settlement is then a real-fiat conversion:

```text
Epay (CNY)           = net_usd * R CNY
USD provider         = net_usd USD
```

A quote must persist all of the following before redirecting to a provider:

- credited integer credits;
- expected settlement amount in integer micros (or provider minor units);
- uppercase ISO settlement currency;
- provider, product/store binding, and stable trade number;
- applied dimensionless multipliers.

Callbacks grant value only after matching the persisted amount, currency, provider, and product binding.

### Frontend quote display

Every new request specifies `amount_unit`: USD, CNY, CREDIT, or LEGACY. Fiat inputs convert to credits using `K` and `R`; a LEGACY input converts using `Q`. The new wallet explicitly sends LEGACY for compatibility with existing preset and discount configuration, while rendering it through integer credits into the selected display unit. It never labels that wire number as USD. Omitted units are only for old clients, including old TOKENS configuration. `/api/user/topup/info` retains old fields and adds explicit LEGACY preset/discount fields for new clients.

`max_topup` is a credited-USD limit, not an amount of platform credit. The Go
backend derives `max_topup_amount` in the units accepted by the top-up request,
using the fixed credit denomination and the request's explicit amount unit.
Duplicate payment types share their strictest configured limit. Custom gateway
pricing, discounts, and display currencies do not change this ceiling.
The frontend compares its input only with `max_topup_amount`. If an older
backend omits it, quote and checkout endpoints continue enforcing the USD limit;
the client must not guess a conversion and block an otherwise valid purchase.

An amount, payment-method, or discount-code change invalidates pending discount
validation and payment confirmation. A successful response for an older input
must not authorize confirmation for the current input. Checkout still recalculates
and enforces the payment rules on the server.

Checkout-link discounts are validated against the amount, payment method, code,
and current input revision. Changing payment methods revalidates the discount;
confirmation waits for the matching discounted quote. A failed validation or
quote unlocks the code for editing and manual retry without an automatic retry
loop. Normalizing a valid code must not start a duplicate validation.

Homepage purchase actions follow the server's developer-access decision. Pending
accounts continue to access review; approved accounts can enter the wallet.
Registration links wait for live registration capabilities. Payment does not
grant developer access, and the pricing overview does not expose the protected
model catalog before approval.

## Subscription plans

`SubscriptionPlan.PriceAmount` plus `SubscriptionPlan.Currency` is a real ISO-fiat list price. It is independent of top-up presets or promotional multipliers.

Legacy version-0 plans were forcibly tagged `USD` while behaving like platform/CNY amounts. Startup migrates them once to `CNY` and sets `PriceCurrencyVersion=1`; explicitly created version-1 USD plans remain USD.

For a plan priced `F USD`:

```text
Epay                 = F * R CNY
Waffo/Pancake        = F USD
Stripe/Creem (USD)   = F USD
wallet balance       = F * K credits
```

For a CNY plan, wallet payment charges `F / R * K` credits. A wallet order captures the charged credits and the original plan/currency/entitlement snapshot. Refunds use that captured charge rather than today's FX or a changed plan. Paid legacy orders and all external payment snapshots remain unchanged.

## Model prices and historical usage

Model-price APIs and the new administrator pricing endpoints expose real USD with schema version 2. USD is the canonical price denomination; presentation can also show CNY or credits. Existing legacy maps, pricing expressions, locks, and in-flight snapshots remain byte-for-byte compatible with the previous backend. The compatibility scale is `S = K / Q`:

```text
true USD fixed/minute/tool price = legacy price / S
true USD per million tokens     = ModelRatio * 1,000,000 / K
```

Completion, cache, audio, image, and group ratios remain dimensionless. Expressions receive a whole-expression conversion, rather than rewritten constants. Historical expressions use their captured `Q / K`; absolute historical prices without a captured basis are unavailable, while the billed integer credit total is always preserved.

The new administrator editor uses separate versioned GET/validate/bulk routes with a revision check. It must not fall back to saving USD numbers through the old legacy option route. Reading and saving an unchanged price keeps the original stored bytes, avoiding a floating-point round-trip that could change the last charged credit.

A pending subscription order stores an immutable plan/entitlement snapshot, expected settlement micros, ISO currency, provider product/price ID, and provider subscription ID. Provider callbacks and renewals must never re-price from mutable settings or a mutable plan row.

## Recurring lifecycle

- Initial successful settlement creates one local entitlement.
- Every renewal payment has a provider event/transaction idempotency key and a recorded billing period.
- Failed payments and `past_due` do not grant a new period.
- Cancel-at-period-end retains already-paid access through the paid period.
- Immediate cancellation/refund revokes only according to verified provider evidence and the product policy.
- USD and CNY totals are grouped by ISO currency; they are never directly summed.

## Rounding

Business calculations use decimal arithmetic. Round only at the provider boundary using that currency's supported minor-unit policy, and persist the exact rounded amount that the callback must match.
