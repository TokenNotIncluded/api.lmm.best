# Payment money contract

This document defines the units used by wallet top-ups and subscription payments. A bare number or `$` symbol is not a sufficient money type.

## Dimensions

| Name | Meaning | Unit |
| --- | --- | --- |
| `platform_units` (`P`) | Internal platform balance credited to a user | platform units |
| `cny_per_usd` (`R`) | Live operator-configured real-fiat FX rate | CNY / USD |
| `platform_units_per_cny` (`B`) | Operator-configured wallet purchase ratio | platform units / CNY |
| `settlement_amount` | Amount sent to and verified from a provider | real ISO fiat |
| `price_multiplier` | Group, channel, tier, or coupon adjustment | dimensionless |

`R` and `B` are independent. Neither display-currency settings nor deprecated provider `unit_price` values may replace them. Example rates in tests are fixtures, never production constants.

## Wallet top-up

For a top-up that grants `P` platform units before a dimensionless multiplier `M`:

```text
base_cny = P / B
net_cny  = base_cny * M
```

Provider settlement is then a real-fiat conversion:

```text
Epay (CNY)           = net_cny CNY
USD provider         = net_cny / R USD
```

A quote must persist all of the following before redirecting to a provider:

- credited platform units/quota;
- expected settlement amount in integer micros (or provider minor units);
- uppercase ISO settlement currency;
- provider, product/store binding, and stable trade number;
- applied dimensionless multipliers.

Callbacks grant value only after matching the persisted amount, currency, provider, and product binding.

### Frontend quote display

The wallet preserves the display fields returned by `/api/user/topup/info`,
including `settlement_currency`, `platform_units_per_usd`,
`settlement_units_per_usd`, `settlement_units_per_platform_unit`, and `max_topup`.
Keep decimal strings intact during parsing. These fields describe the quote's
currency and conversion; the existing amount endpoint remains authoritative for
the payable amount. Platform credit and real-fiat payment appear separately.

An amount, payment-method, or discount-code change invalidates pending discount
validation and payment confirmation. A successful response for an older input
must not authorize confirmation for the current input. Checkout still recalculates
and enforces the payment rules on the server.

Homepage purchase actions follow the server's developer-access decision. Pending
accounts continue to access review; approved accounts can enter the wallet.
Registration links wait for live registration capabilities. Payment does not
grant developer access, and the pricing overview does not expose the protected
model catalog before approval.

## Subscription plans

`SubscriptionPlan.PriceAmount` plus `SubscriptionPlan.Currency` is a real ISO-fiat list price. It is not a wallet top-up amount and must not pass through `P / B`.

Legacy version-0 plans were forcibly tagged `USD` while behaving like platform/CNY amounts. Startup migrates them once to `CNY` and sets `PriceCurrencyVersion=1`; explicitly created version-1 USD plans remain USD.

For a plan priced `F USD`:

```text
Epay                 = F * R CNY
Waffo/Pancake        = F USD
Stripe/Creem (USD)   = F USD
wallet balance       = F * R * B platform units
```

If another supported ISO source currency is introduced, convert real fiat to real fiat first, then convert to platform units only for wallet-balance payment.

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
