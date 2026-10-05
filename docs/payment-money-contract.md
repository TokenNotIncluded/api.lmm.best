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

Credits are independent of model tokens. There is no platform dollar, and one CNY is never defined to be one USD. The platform contract is always 1 USD = 500000 credits. Fiat FX applies only when converting between USD and CNY.

Initialization persists `K = 500000`, independently of `R` and
`TopUpPlatformUnitsPerCNY`. CREDIT display uses the exact integer wallet balance;
`PublicCreditsPerUSD` is a compatibility option fixed at 500000, not a second
adjustable denomination. Existing non-500000 anchors fail startup verification
and require an explicit audited credit-balance migration. Initialization never
silently rewrites user balances, token limits, subscription quotas, pending
orders, or historical prices.

To correct the historical 1 CNY = 1 USD recharge convention, divide affected
remaining integer credit balances by the explicitly selected migration FX rate
(for example 6.8), using a documented integer rounding rule. The integer ledger
must actually change; relabeling fiat or changing `K` is not a correction. Such a
migration needs an idempotent journal, a snapshot of affected balances, writer
quiescence and cache invalidation. Paid order and historical usage snapshots
remain historical evidence. Group multipliers are a separate policy change.

An absent or invalid denomination prevents financial operations; it never
implies 1:1 fiat. Migration verification is read-only; initialization requires
migration apply.

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

The new wallet keeps its amount as an integer credit count and sends
`{amount:q,amount_unit:"CREDIT"}` only to the distinct
`/api/user/topup/currency/` quote, checkout and discount-validation routes.
An older server returns 404 for these routes; the client must not fall back to
an older route that could interpret the same integer as legacy batches. The
canonical route accepts only an ordinary decimal integer within the safe wallet
range, without fractions, strings or exponents. Fiat entry uses the decimal
inverse of `K` and `R`; changing the display unit preserves the chosen integer.

`/api/user/topup/info` retains compatibility fields and publishes versioned raw
`credit_amount_options`, `credit_discount`, provider credit minimums and
per-method `min_topup_credit` / `max_topup_credit`. New clients require these
authoritative values and disable arbitrary-amount checkout when they are
unavailable. Older routes still accept their explicit USD, CNY, CREDIT or LEGACY
units and old omitted-unit behavior, including TOKENS configuration. Existing
presets and coupon thresholds retain their original qualification; canonical
TOKENS qualification compares raw credits directly.

`max_topup` is a credited-USD limit, not an amount of platform credit. The Go
backend derives `max_topup_amount` in the units accepted by the top-up request,
using the fixed credit denomination and the request's explicit amount unit.
Duplicate payment types share their strictest configured limit. Custom gateway
pricing, discounts, and display currencies do not change this ceiling.
The new frontend compares integer credits with the authoritative credit limits.
Legacy `max_topup_amount` remains a compatibility projection for older clients;
it is not an input to the canonical wallet. Backend quote and checkout still
enforce every configured and provider limit independently of the UI.

Standard recharge pricing uses the fixed denomination and current real-fiat FX.
Explicit custom gateway prices and promotions remain separate invoice pricing;
they can make the amount paid differ from the credited balance value. The
confirmation shows both values and the settlement currency. Neither a discount
nor a custom gateway price changes the ledger denomination or model USD prices.

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
